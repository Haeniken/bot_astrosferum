package astroweb

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"math/bits"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

const (
	AstrodomeVisualizationTTL    = 96 * time.Hour
	maximumArchivedDatasetBytes  = 128 << 20
	maximumVisualizationBytes    = 32 << 30
	maximumVisualizationEntries  = 8192
	visualizationCleanupInterval = time.Hour
	visualizationPendingInterval = 15 * time.Second
	visualizationPendingBackoff  = 2 * time.Minute
	visualizationStatusTimeout   = 5 * time.Second
	maximumPendingVisualizations = 64
)

var (
	ErrVisualizationNotFound = errors.New("astrodome visualization not found")
	visualizationFilePattern = regexp.MustCompile(`^viz_[0-9a-f]{32}\.json\.gz$`)
)

// AstrodomeVisualization is small owner-scoped catalogue metadata. The
// scientific dataset itself remains a compressed JSON file on the data
// volume and is never stored in PostgreSQL.
type AstrodomeVisualization struct {
	ID             string    `json:"id"`
	Name           string    `json:"name"`
	Latitude       float64   `json:"latitude"`
	Longitude      float64   `json:"longitude"`
	Provider       string    `json:"provider"`
	RunID          string    `json:"run_id"`
	GridProfile    string    `json:"grid_profile"`
	GeometryDigest string    `json:"grid_geometry_digest"`
	DatasetBytes   int64     `json:"dataset_bytes"`
	GeneratedAt    time.Time `json:"generated_at"`
	ExpiresAt      time.Time `json:"expires_at"`
	AdminFixture   bool      `json:"admin_fixture"`
	SourceJobID    string    `json:"-"`
	DatasetFile    string    `json:"-"`
	DatasetSHA256  string    `json:"-"`
	ETag           string    `json:"-"`
}

type PendingVisualization struct {
	ID             string
	TelegramUserID int64
	CoordinateKey  string
	Name           string
	Latitude       float64
	Longitude      float64
	JobID          string
	CreatedAt      time.Time
}

type CompletedVisualization struct {
	JobID          string
	DatasetFile    string
	DatasetBytes   int64
	DatasetSHA256  string
	ETag           string
	Provider       string
	RunID          string
	GridProfile    string
	GeometryDigest string
	GeneratedAt    time.Time
	ExpiresAt      time.Time
}

// visualizationAvailability measures data completeness independently of a
// grid profile's cardinality. Terrain-blocked nodes are resolved physical
// results; only the explicit unavailable state contributes to Unavailable.
type visualizationAvailability struct {
	unavailable uint64
	total       uint64
}

// VisualizationMetadataStore owns only catalogue metadata and conditional
// publication. CompleteVisualization must update a row only while JobID is
// still its pending job, so concurrent completion attempts cannot replace one
// another or delete the winning file.
type VisualizationMetadataStore interface {
	BeginVisualization(context.Context, PendingVisualization) error
	PendingVisualization(context.Context, int64, string) (PendingVisualization, error)
	PendingVisualizations(context.Context, int) ([]PendingVisualization, error)
	CompleteVisualization(context.Context, int64, CompletedVisualization) (bool, string, error)
	AdminFixture(context.Context, string) (AstrodomeVisualization, error)
	ReplaceAdminFixture(context.Context, string, AstrodomeVisualization, CompletedVisualization) (bool, error)
	AbandonVisualization(context.Context, int64, string) error
	Visualizations(context.Context, int64, time.Time, bool) ([]AstrodomeVisualization, error)
	Visualization(context.Context, int64, string, time.Time, bool) (AstrodomeVisualization, error)
	VisualizationByJob(context.Context, int64, string, time.Time, bool) (AstrodomeVisualization, error)
	DeleteExpiredVisualizations(context.Context, time.Time) ([]string, error)
	ReferencedVisualizationFiles(context.Context) ([]string, error)
}

type VisualizationDataset struct {
	Body  *os.File
	Bytes int64
	ETag  string
}

// VisualizationCatalog coordinates the one ready-row/one compressed-file
// publication boundary. Its mutex also prevents periodic orphan collection
// racing an in-process publication between rename and PostgreSQL commit.
type VisualizationCatalog struct {
	root    string
	entries string
	staging string
	store   VisualizationMetadataStore
	gateway AstrodomeGateway
	now     func() time.Time
	mutex   sync.Mutex

	checksMutex sync.Mutex
	checks      map[string]visualizationPendingCheck

	validationMutex sync.Mutex
	validation      map[string]visualizationValidation
	validateDataset func([]byte) error
}

type visualizationPendingCheck struct {
	attempt int
	next    time.Time
}

type visualizationValidation struct {
	availability visualizationAvailability
	valid        bool
}

func NewVisualizationCatalog(
	root string,
	store VisualizationMetadataStore,
	gateway AstrodomeGateway,
	now func() time.Time,
	validateDataset func([]byte) error,
) (*VisualizationCatalog, error) {
	if strings.TrimSpace(root) == "" || store == nil || gateway == nil || validateDataset == nil {
		return nil, errors.New("visualization root, metadata store, Astrodome gateway, and dataset validator are required")
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve visualization root: %w", err)
	}
	if now == nil {
		now = time.Now
	}
	catalog := &VisualizationCatalog{
		root: absolute, entries: filepath.Join(absolute, "entries"), staging: filepath.Join(absolute, "staging"),
		store: store, gateway: gateway, now: now, checks: make(map[string]visualizationPendingCheck),
		validation: make(map[string]visualizationValidation), validateDataset: validateDataset,
	}
	if err := os.MkdirAll(catalog.entries, 0o750); err != nil {
		return nil, fmt.Errorf("create visualization entries directory: %w", err)
	}
	if err := os.RemoveAll(catalog.staging); err != nil {
		return nil, fmt.Errorf("clean visualization staging directory: %w", err)
	}
	if err := os.MkdirAll(catalog.staging, 0o750); err != nil {
		return nil, fmt.Errorf("create visualization staging directory: %w", err)
	}
	return catalog, nil
}

func (catalog *VisualizationCatalog) Begin(ctx context.Context, userID int64, jobID string, point SavedPoint) error {
	if catalog == nil || userID <= 0 || validateJobID(jobID) != nil || !validCoordinates(point.Latitude, point.Longitude) {
		return errors.New("invalid visualization admission metadata")
	}
	id, err := newVisualizationID()
	if err != nil {
		return err
	}
	name := strings.TrimSpace(point.Name)
	if characters := []rune(name); len(characters) > 64 {
		name = string(characters[:64])
	}
	now := catalog.now().UTC()
	return catalog.store.BeginVisualization(ctx, PendingVisualization{
		ID: id, TelegramUserID: userID, CoordinateKey: canonicalCoordinateKey(point.Latitude, point.Longitude),
		Name: name, Latitude: canonicalCoordinate(point.Latitude), Longitude: canonicalLongitude(point.Latitude, point.Longitude),
		JobID: jobID, CreatedAt: now,
	})
}

func (catalog *VisualizationCatalog) Reconcile(ctx context.Context, userID int64, status AstrodomeJobStatus) error {
	if catalog == nil || userID <= 0 || validateJobID(status.ID) != nil {
		return errors.New("invalid visualization job status")
	}
	switch status.State {
	case "ready":
		return catalog.archive(ctx, userID, status)
	case "failed", "cancelled":
		return catalog.store.AbandonVisualization(ctx, userID, status.ID)
	default:
		return nil
	}
}

func (catalog *VisualizationCatalog) List(ctx context.Context, userID int64, includeAdminFixtures bool) ([]AstrodomeVisualization, error) {
	if catalog == nil || userID < 0 || (userID == 0 && !includeAdminFixtures) {
		return nil, ErrVisualizationNotFound
	}
	items, err := catalog.store.Visualizations(ctx, userID, catalog.now().UTC(), includeAdminFixtures)
	if err != nil {
		return nil, err
	}
	current := items[:0]
	for _, item := range items {
		if catalog.recordIsCurrent(ctx, item) {
			current = append(current, item)
		}
	}
	return current, nil
}

func (catalog *VisualizationCatalog) Open(ctx context.Context, userID int64, id string, includeAdminFixtures bool) (VisualizationDataset, error) {
	if catalog == nil || userID < 0 || (userID == 0 && !includeAdminFixtures) || !validVisualizationID(id) {
		return VisualizationDataset{}, ErrVisualizationNotFound
	}
	catalog.mutex.Lock()
	record, err := catalog.store.Visualization(ctx, userID, id, catalog.now().UTC(), includeAdminFixtures)
	if err != nil {
		catalog.mutex.Unlock()
		return VisualizationDataset{}, err
	}
	file, err := catalog.openRecordFile(record)
	catalog.mutex.Unlock()
	if err != nil {
		return VisualizationDataset{}, err
	}
	dataset, _, err := catalog.verifyRecord(ctx, record, file)
	return dataset, err
}

func (catalog *VisualizationCatalog) OpenByJob(ctx context.Context, userID int64, jobID string, includeAdminFixtures bool) (VisualizationDataset, error) {
	if catalog == nil || userID <= 0 || validateJobID(jobID) != nil {
		return VisualizationDataset{}, ErrVisualizationNotFound
	}
	catalog.mutex.Lock()
	record, err := catalog.store.VisualizationByJob(ctx, userID, jobID, catalog.now().UTC(), includeAdminFixtures)
	if err != nil {
		catalog.mutex.Unlock()
		return VisualizationDataset{}, err
	}
	file, err := catalog.openRecordFile(record)
	catalog.mutex.Unlock()
	if err != nil {
		return VisualizationDataset{}, err
	}
	dataset, _, err := catalog.verifyRecord(ctx, record, file)
	return dataset, err
}

func (catalog *VisualizationCatalog) openRecordFile(record AstrodomeVisualization) (*os.File, error) {
	if !validVisualizationFile(record.DatasetFile) || record.DatasetBytes <= 0 || !validSHA256(record.DatasetSHA256) ||
		record.ETag != `"`+record.DatasetSHA256+`"` {
		return nil, ErrVisualizationNotFound
	}
	path := filepath.Join(catalog.entries, record.DatasetFile)
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() != record.DatasetBytes {
		return nil, ErrVisualizationNotFound
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, ErrVisualizationNotFound
	}
	return file, nil
}

func (catalog *VisualizationCatalog) verifyRecord(
	ctx context.Context,
	record AstrodomeVisualization,
	file *os.File,
) (VisualizationDataset, visualizationAvailability, error) {
	hash := sha256.New()
	if _, err := copyBounded(ctx, hash, file, record.DatasetBytes); err != nil || hex.EncodeToString(hash.Sum(nil)) != record.DatasetSHA256 {
		_ = file.Close()
		return VisualizationDataset{}, visualizationAvailability{}, ErrVisualizationNotFound
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		_ = file.Close()
		return VisualizationDataset{}, visualizationAvailability{}, ErrVisualizationNotFound
	}
	availability, err := catalog.validateRecordDataset(record.DatasetSHA256, file)
	if err != nil {
		_ = file.Close()
		return VisualizationDataset{}, visualizationAvailability{}, ErrVisualizationNotFound
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		_ = file.Close()
		return VisualizationDataset{}, visualizationAvailability{}, ErrVisualizationNotFound
	}
	return VisualizationDataset{Body: file, Bytes: record.DatasetBytes, ETag: record.ETag}, availability, nil
}

func (catalog *VisualizationCatalog) recordIsCurrent(ctx context.Context, record AstrodomeVisualization) bool {
	file, err := catalog.openRecordFile(record)
	if err != nil {
		return false
	}
	dataset, _, err := catalog.verifyRecord(ctx, record, file)
	if dataset.Body != nil {
		_ = dataset.Body.Close()
	}
	if ctx.Err() != nil {
		return false
	}
	return err == nil
}

func (catalog *VisualizationCatalog) validateRecordDataset(
	digest string,
	source *os.File,
) (visualizationAvailability, error) {
	catalog.validationMutex.Lock()
	defer catalog.validationMutex.Unlock()
	if validation, known := catalog.validation[digest]; known {
		if validation.valid {
			return validation.availability, nil
		}
		return visualizationAvailability{}, errors.New("astrodome dataset contract is unsupported")
	}
	availability, err := catalog.validateCompressedDataset(source)
	catalog.validation[digest] = visualizationValidation{availability: availability, valid: err == nil}
	return availability, err
}

func (catalog *VisualizationCatalog) archive(ctx context.Context, userID int64, status AstrodomeJobStatus) error {
	catalog.mutex.Lock()
	defer catalog.mutex.Unlock()
	pending, err := catalog.store.PendingVisualization(ctx, userID, status.ID)
	if err != nil {
		if errors.Is(err, ErrVisualizationNotFound) {
			return nil
		}
		return err
	}
	dataset, err := catalog.gateway.Dataset(ctx, userID, status.ID)
	if err != nil {
		return fmt.Errorf("retrieve completed Astrodome dataset for archive: %w", err)
	}
	defer func() { _ = dataset.Body.Close() }()
	fileName, bytesWritten, digest, etag, err := catalog.writeDataset(ctx, dataset)
	if err != nil {
		return err
	}
	now := catalog.now().UTC()
	completed := CompletedVisualization{
		JobID: status.ID, DatasetFile: fileName, DatasetBytes: bytesWritten, DatasetSHA256: digest, ETag: etag,
		Provider: status.Provider, RunID: status.RunID, GridProfile: status.GridProfile,
		GeometryDigest: status.GeometryDigest, GeneratedAt: now,
		ExpiresAt: now.Add(AstrodomeVisualizationTTL),
	}
	if err := catalog.updateNoWorseAdminFixture(ctx, pending.CoordinateKey, completed); err != nil {
		return err
	}
	accepted, oldFile, err := catalog.store.CompleteVisualization(ctx, userID, completed)
	if err != nil {
		// Transaction commit failures are outcome-ambiguous. Keep the durable
		// file until catalogue maintenance can compare it with committed DB
		// references; deleting it here could corrupt a successfully committed row.
		return err
	}
	if !accepted {
		_ = os.Remove(filepath.Join(catalog.entries, fileName))
		_ = syncDirectory(catalog.entries)
		return nil
	}
	if oldFile != "" && oldFile != fileName && validVisualizationFile(oldFile) {
		if err := os.Remove(filepath.Join(catalog.entries, oldFile)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove superseded Astrodome visualization: %w", err)
		}
	}
	return syncDirectory(catalog.entries)
}

// updateNoWorseAdminFixture keeps the single shared administrator fixture from
// regressing when a result is archived for its exact canonical coordinates.
// Grid profiles have different node counts, so completeness is compared as an
// exact rational unavailable/total value rather than as a raw count or float.
func (catalog *VisualizationCatalog) updateNoWorseAdminFixture(
	ctx context.Context,
	coordinateKey string,
	completed CompletedVisualization,
) error {
	var candidateAvailability visualizationAvailability
	for range 3 {
		fixture, fixtureErr := catalog.store.AdminFixture(ctx, coordinateKey)
		if errors.Is(fixtureErr, ErrVisualizationNotFound) {
			return nil
		}
		if fixtureErr != nil {
			return fixtureErr
		}
		if candidateAvailability.total == 0 {
			catalog.validationMutex.Lock()
			candidate, known := catalog.validation[completed.DatasetSHA256]
			catalog.validationMutex.Unlock()
			if !known || !candidate.valid {
				return errors.New("validated Astrodome fixture candidate is unavailable")
			}
			candidateAvailability = candidate.availability
		}
		fixtureDataset, openErr := catalog.openRecordFile(fixture)
		if openErr != nil {
			// A current, strictly validated result supersedes an unreadable or
			// unsupported permanent fixture. The latter is not a usable scientific
			// visualization and therefore has no completeness score to preserve.
		} else {
			verified, fixtureAvailability, verifyErr := catalog.verifyRecord(ctx, fixture, fixtureDataset)
			if verified.Body != nil {
				_ = verified.Body.Close()
			}
			if verifyErr != nil {
			} else if !noWorseVisualizationAvailability(candidateAvailability, fixtureAvailability) {
				return nil
			}
		}

		fixtureFile, linkErr := catalog.linkVisualizationDataset(completed.DatasetFile)
		if linkErr != nil {
			return linkErr
		}
		replacement := completed
		replacement.DatasetFile = fixtureFile
		replaced, replaceErr := catalog.store.ReplaceAdminFixture(ctx, coordinateKey, fixture, replacement)
		if replaceErr != nil {
			// A commit error is outcome-ambiguous. Keep the durable link; orphan
			// maintenance removes it if PostgreSQL did not publish the pointer.
			return replaceErr
		}
		if !replaced {
			if removeErr := os.Remove(filepath.Join(catalog.entries, fixtureFile)); removeErr != nil &&
				!errors.Is(removeErr, os.ErrNotExist) {
				return fmt.Errorf("remove unused Astrodome fixture candidate: %w", removeErr)
			}
			if syncErr := syncDirectory(catalog.entries); syncErr != nil {
				return syncErr
			}
			continue
		}
		if fixture.DatasetFile != fixtureFile && validVisualizationFile(fixture.DatasetFile) {
			if removeErr := os.Remove(filepath.Join(catalog.entries, fixture.DatasetFile)); removeErr != nil &&
				!errors.Is(removeErr, os.ErrNotExist) {
				return fmt.Errorf("remove superseded Astrodome administrator fixture: %w", removeErr)
			}
		}
		return syncDirectory(catalog.entries)
	}
	return errors.New("astrodome administrator fixture changed concurrently")
}

func (catalog *VisualizationCatalog) linkVisualizationDataset(sourceFile string) (string, error) {
	if !validVisualizationFile(sourceFile) {
		return "", errors.New("invalid Astrodome fixture source file")
	}
	destinationFile, err := newVisualizationFileName()
	if err != nil {
		return "", err
	}
	if err := os.Link(
		filepath.Join(catalog.entries, sourceFile),
		filepath.Join(catalog.entries, destinationFile),
	); err != nil {
		return "", fmt.Errorf("link Astrodome administrator fixture: %w", err)
	}
	if err := syncDirectory(catalog.entries); err != nil {
		_ = os.Remove(filepath.Join(catalog.entries, destinationFile))
		_ = syncDirectory(catalog.entries)
		return "", fmt.Errorf("persist Astrodome administrator fixture: %w", err)
	}
	return destinationFile, nil
}

func noWorseVisualizationAvailability(candidate, current visualizationAvailability) bool {
	if candidate.total == 0 || current.total == 0 ||
		candidate.unavailable > candidate.total || current.unavailable > current.total {
		return false
	}
	candidateHigh, candidateLow := bits.Mul64(candidate.unavailable, current.total)
	currentHigh, currentLow := bits.Mul64(current.unavailable, candidate.total)
	return candidateHigh < currentHigh || candidateHigh == currentHigh && candidateLow <= currentLow
}

func (catalog *VisualizationCatalog) archivedVisualizationAvailability(path string) (visualizationAvailability, error) {
	file, err := os.Open(path)
	if err != nil {
		return visualizationAvailability{}, err
	}
	availability, scoreErr := catalog.validateCompressedDataset(file)
	return availability, errors.Join(scoreErr, file.Close())
}

func (catalog *VisualizationCatalog) validateCompressedDataset(source io.Reader) (visualizationAvailability, error) {
	if catalog == nil || catalog.validateDataset == nil {
		return visualizationAvailability{}, errors.New("astrodome dataset validator is unavailable")
	}
	compressed, err := gzip.NewReader(source)
	if err != nil {
		return visualizationAvailability{}, fmt.Errorf("open archived Astrodome dataset: %w", err)
	}
	decoded, readErr := io.ReadAll(io.LimitReader(compressed, maximumArchivedDatasetBytes+1))
	closeErr := compressed.Close()
	if readErr != nil || closeErr != nil {
		return visualizationAvailability{}, fmt.Errorf("read archived Astrodome dataset: %w", errors.Join(readErr, closeErr))
	}
	if len(decoded) > maximumArchivedDatasetBytes {
		return visualizationAvailability{}, errors.New("decoded Astrodome dataset exceeds archive limit")
	}
	if err := catalog.validateDataset(decoded); err != nil {
		return visualizationAvailability{}, fmt.Errorf("validate archived Astrodome dataset contract: %w", err)
	}
	return archivedVisualizationAvailabilityReader(bytes.NewReader(decoded))
}

func archivedVisualizationAvailabilityReader(source io.Reader) (visualizationAvailability, error) {
	decoder := json.NewDecoder(source)
	var document struct {
		Frames []struct {
			Nodes []struct {
				State string `json:"state"`
			} `json:"nodes"`
		} `json:"frames"`
	}
	if err := decoder.Decode(&document); err != nil {
		return visualizationAvailability{}, fmt.Errorf("decode archived Astrodome dataset for scoring: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return visualizationAvailability{}, errors.New("archived Astrodome dataset must contain exactly one JSON value")
	}
	if len(document.Frames) == 0 {
		return visualizationAvailability{}, errors.New("archived Astrodome dataset has no forecast frames")
	}
	availability := visualizationAvailability{}
	for _, frame := range document.Frames {
		if len(frame.Nodes) == 0 {
			return visualizationAvailability{}, errors.New("archived Astrodome dataset has an empty forecast frame")
		}
		for _, node := range frame.Nodes {
			if availability.total == ^uint64(0) {
				return visualizationAvailability{}, errors.New("astrodome node-hour count overflows uint64")
			}
			availability.total++
			switch node.State {
			case "unavailable":
				availability.unavailable++
			case "valid", "precipitation_veto", "terrain_blocked":
			default:
				return visualizationAvailability{}, fmt.Errorf("unsupported archived Astrodome node state %q", node.State)
			}
		}
	}
	return availability, nil
}

func (catalog *VisualizationCatalog) writeDataset(ctx context.Context, source AstrodomeDataset) (string, int64, string, string, error) {
	if source.Body == nil || (source.ContentEncoding != "" && source.ContentEncoding != "gzip") {
		return "", 0, "", "", errors.New("completed Astrodome dataset has unsupported encoding")
	}
	staging, err := os.CreateTemp(catalog.staging, ".visualization-*.tmp")
	if err != nil {
		return "", 0, "", "", fmt.Errorf("create visualization staging file: %w", err)
	}
	stagingPath := staging.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(stagingPath)
		}
	}()
	hash := sha256.New()
	destination := io.MultiWriter(staging, hash)
	var written int64
	if source.ContentEncoding == "gzip" {
		written, err = copyBounded(ctx, destination, source.Body, maximumArchivedDatasetBytes)
	} else {
		compressor, gzipErr := gzip.NewWriterLevel(destination, gzip.BestSpeed)
		if gzipErr != nil {
			_ = staging.Close()
			return "", 0, "", "", gzipErr
		}
		_, err = copyBounded(ctx, compressor, source.Body, maximumArchivedDatasetBytes)
		closeErr := compressor.Close()
		if err == nil {
			err = closeErr
		}
		if stat, statErr := staging.Stat(); err == nil && statErr == nil {
			written = stat.Size()
		} else if err == nil {
			err = statErr
		}
	}
	if err == nil {
		err = staging.Sync()
	}
	if closeErr := staging.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return "", 0, "", "", fmt.Errorf("archive completed Astrodome dataset: %w", err)
	}
	availability, err := catalog.archivedVisualizationAvailability(stagingPath)
	if err != nil {
		return "", 0, "", "", err
	}
	if written <= 0 || written > maximumArchivedDatasetBytes {
		return "", 0, "", "", errors.New("archived Astrodome dataset size is invalid")
	}
	entries, bytesUsed, err := visualizationStorageUsage(catalog.entries)
	if err != nil {
		return "", 0, "", "", fmt.Errorf("measure Astrodome visualization storage: %w", err)
	}
	if entries >= maximumVisualizationEntries || bytesUsed > maximumVisualizationBytes-written {
		return "", 0, "", "", errors.New("astrodome visualization storage budget is exhausted")
	}
	if err := os.Chmod(stagingPath, 0o440); err != nil {
		return "", 0, "", "", fmt.Errorf("protect archived Astrodome dataset: %w", err)
	}
	fileName, err := newVisualizationFileName()
	if err != nil {
		return "", 0, "", "", err
	}
	destinationPath := filepath.Join(catalog.entries, fileName)
	if err := os.Rename(stagingPath, destinationPath); err != nil {
		return "", 0, "", "", fmt.Errorf("publish archived Astrodome dataset: %w", err)
	}
	cleanup = false
	// Make the renamed file durable before PostgreSQL is allowed to publish
	// metadata that points at it. A crash may leave an unreferenced file, which
	// startup maintenance safely removes; it must never leave a committed row
	// whose replacement file was not persisted.
	if err := syncDirectory(catalog.entries); err != nil {
		_ = os.Remove(destinationPath)
		_ = syncDirectory(catalog.entries)
		return "", 0, "", "", fmt.Errorf("persist archived Astrodome dataset: %w", err)
	}
	digest := hex.EncodeToString(hash.Sum(nil))
	catalog.validationMutex.Lock()
	catalog.validation[digest] = visualizationValidation{availability: availability, valid: true}
	catalog.validationMutex.Unlock()
	return fileName, written, digest, `"` + digest + `"`, nil
}

func (catalog *VisualizationCatalog) RunMaintenance(ctx context.Context, logger *slog.Logger) {
	if catalog == nil {
		return
	}
	if logger == nil {
		logger = slog.Default()
	}
	cleanup := func() {
		if err := catalog.cleanup(ctx); err != nil && ctx.Err() == nil {
			logger.Error("Astrodome visualization maintenance failed", "error", err.Error())
		}
	}
	reconcile := func() {
		if err := catalog.reconcilePending(ctx); err != nil && ctx.Err() == nil {
			logger.Error("Astrodome pending visualization reconciliation failed", "error", err.Error())
		}
	}
	cleanup()
	reconcile()
	cleanupTicker := time.NewTicker(visualizationCleanupInterval)
	pendingTicker := time.NewTicker(visualizationPendingInterval)
	defer cleanupTicker.Stop()
	defer pendingTicker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-cleanupTicker.C:
			cleanup()
		case <-pendingTicker.C:
			reconcile()
		}
	}
}

// Ready verifies both catalogue metadata access (via the server's database
// readiness) and this process's current ability to create files on the
// dedicated visualization volume. The probe is private and immediately
// removed; it never enters the user catalogue.
func (catalog *VisualizationCatalog) Ready(ctx context.Context) error {
	if catalog == nil {
		return errors.New("astrodome visualization catalogue is unavailable")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	probe, err := os.CreateTemp(catalog.staging, ".ready-*.tmp")
	if err != nil {
		return fmt.Errorf("create Astrodome visualization readiness probe: %w", err)
	}
	path := probe.Name()
	if _, err = probe.Write([]byte{0}); err == nil {
		err = probe.Close()
	} else {
		err = errors.Join(err, probe.Close())
	}
	removeErr := os.Remove(path)
	return errors.Join(err, removeErr)
}

func (catalog *VisualizationCatalog) reconcilePending(ctx context.Context) error {
	pending, err := catalog.store.PendingVisualizations(ctx, maximumPendingVisualizations)
	if err != nil {
		return err
	}
	now := catalog.now().UTC()
	active := make(map[string]struct{}, len(pending))
	var result error
	for _, item := range pending {
		key := visualizationPendingKey(item.TelegramUserID, item.JobID)
		active[key] = struct{}{}
		if !catalog.pendingCheckDue(key, now) {
			continue
		}
		statusContext, cancel := context.WithTimeout(ctx, visualizationStatusTimeout)
		status, statusErr := catalog.gateway.Status(statusContext, item.TelegramUserID, item.JobID)
		cancel()
		if errors.Is(statusErr, ErrJobNotFound) {
			if abandonErr := catalog.store.AbandonVisualization(ctx, item.TelegramUserID, item.JobID); abandonErr != nil {
				catalog.deferPendingCheck(key, now)
				result = errors.Join(result, fmt.Errorf("discard missing Astrodome job %s: %w", item.JobID, abandonErr))
				continue
			}
			catalog.clearPendingCheck(key)
			continue
		}
		if statusErr != nil {
			catalog.deferPendingCheck(key, now)
			result = errors.Join(result, fmt.Errorf("read pending Astrodome job %s: %w", item.JobID, statusErr))
			continue
		}
		switch status.State {
		case "ready", "failed", "cancelled":
			if reconcileErr := catalog.Reconcile(ctx, item.TelegramUserID, status); reconcileErr != nil {
				catalog.deferPendingCheck(key, now)
				result = errors.Join(result, fmt.Errorf("reconcile pending Astrodome job %s: %w", item.JobID, reconcileErr))
				continue
			}
			catalog.clearPendingCheck(key)
		default:
			catalog.deferPendingCheck(key, now)
		}
	}
	catalog.prunePendingChecks(active)
	return result
}

func (catalog *VisualizationCatalog) pendingCheckDue(key string, now time.Time) bool {
	catalog.checksMutex.Lock()
	defer catalog.checksMutex.Unlock()
	return !catalog.checks[key].next.After(now)
}

func (catalog *VisualizationCatalog) deferPendingCheck(key string, now time.Time) {
	catalog.checksMutex.Lock()
	defer catalog.checksMutex.Unlock()
	state := catalog.checks[key]
	delay := visualizationPendingInterval
	for range min(state.attempt, 3) {
		delay *= 2
	}
	if delay > visualizationPendingBackoff {
		delay = visualizationPendingBackoff
	}
	state.attempt++
	state.next = now.Add(delay)
	catalog.checks[key] = state
}

func (catalog *VisualizationCatalog) clearPendingCheck(key string) {
	catalog.checksMutex.Lock()
	delete(catalog.checks, key)
	catalog.checksMutex.Unlock()
}

func (catalog *VisualizationCatalog) prunePendingChecks(active map[string]struct{}) {
	catalog.checksMutex.Lock()
	defer catalog.checksMutex.Unlock()
	for key := range catalog.checks {
		if _, exists := active[key]; !exists {
			delete(catalog.checks, key)
		}
	}
}

func visualizationPendingKey(userID int64, jobID string) string {
	return fmt.Sprintf("%d:%s", userID, jobID)
}

func visualizationStorageUsage(root string) (int, int64, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return 0, 0, err
	}
	count, bytesUsed := 0, int64(0)
	for _, entry := range entries {
		if !entry.Type().IsRegular() || !validVisualizationFile(entry.Name()) {
			continue
		}
		info, infoErr := entry.Info()
		if infoErr != nil {
			return 0, 0, infoErr
		}
		if info.Size() < 0 || bytesUsed > math.MaxInt64-info.Size() {
			return 0, 0, errors.New("astrodome visualization storage size overflow")
		}
		count++
		bytesUsed += info.Size()
	}
	return count, bytesUsed, nil
}

func (catalog *VisualizationCatalog) cleanup(ctx context.Context) error {
	catalog.mutex.Lock()
	defer catalog.mutex.Unlock()
	now := catalog.now().UTC()
	files, err := catalog.store.DeleteExpiredVisualizations(ctx, now)
	if err != nil {
		return err
	}
	var cleanupErr error
	for _, name := range files {
		if validVisualizationFile(name) {
			if removeErr := os.Remove(filepath.Join(catalog.entries, name)); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
				cleanupErr = errors.Join(cleanupErr, fmt.Errorf("remove expired Astrodome visualization %s: %w", name, removeErr))
			}
		}
	}
	referenced, err := catalog.store.ReferencedVisualizationFiles(ctx)
	if err != nil {
		return err
	}
	keep := make(map[string]struct{}, len(referenced))
	for _, name := range referenced {
		if validVisualizationFile(name) {
			keep[name] = struct{}{}
		}
	}
	entries, err := os.ReadDir(catalog.entries)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if _, exists := keep[entry.Name()]; !exists && entry.Type().IsRegular() && validVisualizationFile(entry.Name()) {
			if removeErr := os.Remove(filepath.Join(catalog.entries, entry.Name())); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
				cleanupErr = errors.Join(cleanupErr, fmt.Errorf("remove orphan Astrodome visualization %s: %w", entry.Name(), removeErr))
			}
		}
	}
	return errors.Join(cleanupErr, syncDirectory(catalog.entries))
}

func canonicalCoordinateKey(latitude, longitude float64) string {
	latitude = canonicalCoordinate(latitude)
	longitude = canonicalLongitude(latitude, longitude)
	return fmt.Sprintf("%016x%016x", math.Float64bits(latitude), math.Float64bits(longitude))
}

func canonicalCoordinate(value float64) float64 {
	if value == 0 {
		return 0
	}
	return value
}

func canonicalLongitude(latitude, longitude float64) float64 {
	if math.Abs(latitude) == 90 {
		return 0
	}
	if longitude == 180 {
		return -180
	}
	return canonicalCoordinate(longitude)
}

func newVisualizationID() (string, error) {
	var entropy [16]byte
	if _, err := io.ReadFull(rand.Reader, entropy[:]); err != nil {
		return "", fmt.Errorf("generate visualization ID: %w", err)
	}
	return "viz_" + hex.EncodeToString(entropy[:]), nil
}

func newVisualizationFileName() (string, error) {
	id, err := newVisualizationID()
	if err != nil {
		return "", err
	}
	return id + ".json.gz", nil
}

func validVisualizationID(value string) bool {
	return len(value) == 36 && strings.HasPrefix(value, "viz_") && validLowerHex(value[4:])
}

func validVisualizationFile(value string) bool {
	return visualizationFilePattern.MatchString(value) && filepath.Base(value) == value
}

func validSHA256(value string) bool {
	return len(value) == sha256.Size*2 && validLowerHex(value)
}

func validLowerHex(value string) bool {
	for _, character := range value {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return value != ""
}

func copyBounded(ctx context.Context, destination io.Writer, source io.Reader, maximum int64) (int64, error) {
	written, err := io.Copy(destination, io.LimitReader(contextReader{ctx: ctx, reader: source}, maximum+1))
	if err != nil {
		return written, err
	}
	if written > maximum {
		return written, errors.New("astrodome dataset exceeds archive limit")
	}
	return written, nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (reader contextReader) Read(buffer []byte) (int, error) {
	if err := reader.ctx.Err(); err != nil {
		return 0, err
	}
	return reader.reader.Read(buffer)
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	err = directory.Sync()
	return errors.Join(err, directory.Close())
}
