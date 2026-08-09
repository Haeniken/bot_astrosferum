package astroweb

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

type memoryVisualizationStore struct {
	mutex  sync.Mutex
	rows   map[string]visualizationMemoryRow
	byUser map[int64]map[string]string
}

type visualizationMemoryRow struct {
	owner   int64
	pending PendingVisualization
	ready   *AstrodomeVisualization
}

func newTestVisualizationCatalog(
	t *testing.T,
	store VisualizationMetadataStore,
	gateway AstrodomeGateway,
	now func() time.Time,
) (*VisualizationCatalog, error) {
	t.Helper()
	return NewVisualizationCatalog(t.TempDir(), store, gateway, now, func(source []byte) error {
		_, err := validateVisualizationAvailabilityFixture(bytes.NewReader(source))
		return err
	})
}

func validateVisualizationAvailabilityFixture(source io.Reader) (visualizationAvailability, error) {
	decoder := json.NewDecoder(io.LimitReader(source, maximumArchivedDatasetBytes+1))
	decoder.DisallowUnknownFields()
	var document struct {
		Frames []struct {
			Nodes []struct {
				State string `json:"state"`
			} `json:"nodes"`
		} `json:"frames"`
	}
	if err := decoder.Decode(&document); err != nil {
		return visualizationAvailability{}, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return visualizationAvailability{}, errors.New("fixture must contain exactly one JSON value")
	}
	if len(document.Frames) == 0 {
		return visualizationAvailability{}, errors.New("fixture has no frames")
	}
	availability := visualizationAvailability{}
	for _, frame := range document.Frames {
		if len(frame.Nodes) == 0 {
			return visualizationAvailability{}, errors.New("fixture has an empty frame")
		}
		for _, node := range frame.Nodes {
			availability.total++
			switch node.State {
			case "unavailable":
				availability.unavailable++
			case "valid", "precipitation_veto", "terrain_blocked":
			default:
				return visualizationAvailability{}, fmt.Errorf("unsupported fixture node state %q", node.State)
			}
		}
	}
	return availability, nil
}

func newMemoryVisualizationStore() *memoryVisualizationStore {
	return &memoryVisualizationStore{rows: make(map[string]visualizationMemoryRow), byUser: make(map[int64]map[string]string)}
}

func (store *memoryVisualizationStore) BeginVisualization(_ context.Context, pending PendingVisualization) error {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	keys := store.byUser[pending.TelegramUserID]
	if keys == nil {
		keys = make(map[string]string)
		store.byUser[pending.TelegramUserID] = keys
	}
	id := keys[pending.CoordinateKey]
	if row := store.rows[id]; id != "" && row.ready != nil && row.ready.AdminFixture {
		id = ""
	}
	if id == "" {
		id = pending.ID
		keys[pending.CoordinateKey] = id
	}
	row := store.rows[id]
	row.owner = pending.TelegramUserID
	pending.ID = id
	row.pending = pending
	store.rows[id] = row
	return nil
}

func (store *memoryVisualizationStore) PendingVisualization(_ context.Context, userID int64, jobID string) (PendingVisualization, error) {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	for _, row := range store.rows {
		if row.pending.TelegramUserID == userID && row.pending.JobID == jobID {
			return row.pending, nil
		}
	}
	return PendingVisualization{}, ErrVisualizationNotFound
}

func (store *memoryVisualizationStore) PendingVisualizations(_ context.Context, limit int) ([]PendingVisualization, error) {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	pending := make([]PendingVisualization, 0)
	for _, row := range store.rows {
		if row.pending.JobID != "" {
			pending = append(pending, row.pending)
		}
	}
	sort.Slice(pending, func(i, j int) bool {
		if pending[i].CreatedAt.Equal(pending[j].CreatedAt) {
			return pending[i].ID < pending[j].ID
		}
		return pending[i].CreatedAt.Before(pending[j].CreatedAt)
	})
	if len(pending) > limit {
		pending = pending[:limit]
	}
	return pending, nil
}

func (store *memoryVisualizationStore) CompleteVisualization(_ context.Context, userID int64, completed CompletedVisualization) (bool, string, error) {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	for id, row := range store.rows {
		if row.pending.TelegramUserID != userID || row.pending.JobID != completed.JobID {
			continue
		}
		old := ""
		if row.ready != nil {
			old = row.ready.DatasetFile
		}
		row.ready = &AstrodomeVisualization{
			ID: id, Name: row.pending.Name, Latitude: row.pending.Latitude, Longitude: row.pending.Longitude,
			Provider: completed.Provider, RunID: completed.RunID, GridProfile: completed.GridProfile,
			GeometryDigest: completed.GeometryDigest, DatasetBytes: completed.DatasetBytes,
			GeneratedAt: completed.GeneratedAt, ExpiresAt: completed.ExpiresAt, SourceJobID: completed.JobID,
			DatasetFile: completed.DatasetFile, DatasetSHA256: completed.DatasetSHA256, ETag: completed.ETag,
		}
		row.pending = PendingVisualization{}
		store.rows[id] = row
		return true, old, nil
	}
	return false, "", nil
}

func (store *memoryVisualizationStore) AdminFixture(_ context.Context, coordinateKey string) (AstrodomeVisualization, error) {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	for _, row := range store.rows {
		if row.ready != nil && row.ready.AdminFixture &&
			canonicalCoordinateKey(row.ready.Latitude, row.ready.Longitude) == coordinateKey {
			return *row.ready, nil
		}
	}
	return AstrodomeVisualization{}, ErrVisualizationNotFound
}

func (store *memoryVisualizationStore) ReplaceAdminFixture(
	_ context.Context,
	coordinateKey string,
	expected AstrodomeVisualization,
	replacement CompletedVisualization,
) (bool, error) {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	row, exists := store.rows[expected.ID]
	if !exists || row.ready == nil || !row.ready.AdminFixture ||
		canonicalCoordinateKey(row.ready.Latitude, row.ready.Longitude) != coordinateKey ||
		row.ready.DatasetFile != expected.DatasetFile || row.ready.DatasetSHA256 != expected.DatasetSHA256 ||
		row.ready.ETag != expected.ETag {
		return false, nil
	}
	updated := *row.ready
	updated.Provider = replacement.Provider
	updated.RunID = replacement.RunID
	updated.GridProfile = replacement.GridProfile
	updated.GeometryDigest = replacement.GeometryDigest
	updated.DatasetBytes = replacement.DatasetBytes
	updated.GeneratedAt = replacement.GeneratedAt
	updated.DatasetFile = replacement.DatasetFile
	updated.DatasetSHA256 = replacement.DatasetSHA256
	updated.ETag = replacement.ETag
	row.ready = &updated
	store.rows[expected.ID] = row
	return true, nil
}

func (store *memoryVisualizationStore) AbandonVisualization(_ context.Context, userID int64, jobID string) error {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	for id, row := range store.rows {
		if row.pending.TelegramUserID == userID && row.pending.JobID == jobID {
			row.pending = PendingVisualization{}
			if row.ready == nil {
				delete(store.rows, id)
			} else {
				store.rows[id] = row
			}
		}
	}
	return nil
}

func (store *memoryVisualizationStore) Visualizations(_ context.Context, userID int64, now time.Time, includeAdminFixtures bool) ([]AstrodomeVisualization, error) {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	var result []AstrodomeVisualization
	for _, row := range store.rows {
		if row.ready != nil && ((row.ready.ExpiresAt.After(now) && visualizationUser(row) == userID && !row.ready.AdminFixture) ||
			row.ready.AdminFixture && includeAdminFixtures) {
			result = append(result, *row.ready)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].AdminFixture != result[j].AdminFixture {
			return result[i].AdminFixture
		}
		if !result[i].GeneratedAt.Equal(result[j].GeneratedAt) {
			return result[i].GeneratedAt.After(result[j].GeneratedAt)
		}
		return result[i].ID < result[j].ID
	})
	return result, nil
}

func (store *memoryVisualizationStore) Visualization(_ context.Context, userID int64, id string, now time.Time, includeAdminFixtures bool) (AstrodomeVisualization, error) {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	row, exists := store.rows[id]
	visible := exists && row.ready != nil && ((visualizationUser(row) == userID && row.ready.ExpiresAt.After(now) && !row.ready.AdminFixture) ||
		row.ready.AdminFixture && includeAdminFixtures)
	if !visible {
		return AstrodomeVisualization{}, ErrVisualizationNotFound
	}
	return *row.ready, nil
}

func (store *memoryVisualizationStore) VisualizationByJob(_ context.Context, userID int64, jobID string, now time.Time, includeAdminFixtures bool) (AstrodomeVisualization, error) {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	for _, row := range store.rows {
		if row.ready != nil && row.ready.SourceJobID == jobID &&
			((visualizationUser(row) == userID && row.ready.ExpiresAt.After(now) && !row.ready.AdminFixture) ||
				(includeAdminFixtures && row.ready.AdminFixture)) {
			return *row.ready, nil
		}
	}
	return AstrodomeVisualization{}, ErrVisualizationNotFound
}

func (store *memoryVisualizationStore) DeleteExpiredVisualizations(_ context.Context, now time.Time) ([]string, error) {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	var files []string
	for id, row := range store.rows {
		if row.ready != nil && !row.ready.AdminFixture && !row.ready.ExpiresAt.After(now) {
			files = append(files, row.ready.DatasetFile)
			row.ready = nil
		}
		if row.ready == nil && row.pending.JobID == "" {
			delete(store.rows, id)
		} else {
			store.rows[id] = row
		}
	}
	return files, nil
}

func (store *memoryVisualizationStore) ReferencedVisualizationFiles(context.Context) ([]string, error) {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	var files []string
	for _, row := range store.rows {
		if row.ready != nil {
			files = append(files, row.ready.DatasetFile)
		}
	}
	return files, nil
}

func visualizationUser(row visualizationMemoryRow) int64 {
	return row.owner
}

type visualizationGateway struct {
	body        string
	encoding    string
	statuses    map[string]AstrodomeJobStatus
	statusErr   error
	statusCalls int
}

func (gateway *visualizationGateway) Availability(context.Context, int64) (AstrodomeAvailability, error) {
	return AstrodomeAvailability{}, nil
}
func (gateway *visualizationGateway) Admit(context.Context, AstrodomeAdmission) (AstrodomeJobStatus, error) {
	return AstrodomeJobStatus{}, nil
}
func (gateway *visualizationGateway) Status(_ context.Context, _ int64, jobID string) (AstrodomeJobStatus, error) {
	gateway.statusCalls++
	if gateway.statusErr != nil {
		return AstrodomeJobStatus{}, gateway.statusErr
	}
	if status, exists := gateway.statuses[jobID]; exists {
		return status, nil
	}
	return AstrodomeJobStatus{ID: jobID, State: "running"}, nil
}
func (gateway *visualizationGateway) Dataset(context.Context, int64, string) (AstrodomeDataset, error) {
	return AstrodomeDataset{Body: io.NopCloser(strings.NewReader(gateway.body)), Bytes: int64(len(gateway.body)), ContentEncoding: gateway.encoding}, nil
}
func (gateway *visualizationGateway) Cancel(context.Context, int64, string) error { return nil }

func TestVisualizationCatalogSuccessOnlyReplacementAndOwnerIsolation(t *testing.T) {
	now := time.Date(2026, 7, 29, 9, 0, 0, 0, time.UTC)
	store := newMemoryVisualizationStore()
	catalog, err := newTestVisualizationCatalog(t, store, &visualizationGateway{body: visualizationAvailabilityJSON(1, 0, 0)}, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	point := SavedPoint{Name: "Site", Latitude: 53.65, Longitude: 37.35}
	firstJob := "job_abcdefghijklmnopqrstuvwxyz"
	if err := catalog.Begin(t.Context(), 42, firstJob, point); err != nil {
		t.Fatal(err)
	}
	if err := catalog.Reconcile(t.Context(), 42, AstrodomeJobStatus{ID: firstJob, State: "ready", Provider: "icon-eu", RunID: "2026072900", GridProfile: "dense-v1", GeometryDigest: "sha256:first"}); err != nil {
		t.Fatal(err)
	}
	items, err := catalog.List(t.Context(), 42, false)
	if err != nil || len(items) != 1 || items[0].ExpiresAt.Sub(now) != AstrodomeVisualizationTTL {
		t.Fatalf("first archive = %+v, err=%v", items, err)
	}
	firstFile := items[0].DatasetFile
	if _, err := catalog.Open(t.Context(), 7, items[0].ID, false); !errors.Is(err, ErrVisualizationNotFound) {
		t.Fatalf("other owner open error = %v", err)
	}

	failedJob := "job_bcdefghijklmnopqrstuvwxyza"
	failedPoint := point
	failedPoint.Name = "Renamed only if successful"
	if err := catalog.Begin(t.Context(), 42, failedJob, failedPoint); err != nil {
		t.Fatal(err)
	}
	if err := catalog.Reconcile(t.Context(), 42, AstrodomeJobStatus{ID: failedJob, State: "failed"}); err != nil {
		t.Fatal(err)
	}
	items, _ = catalog.List(t.Context(), 42, false)
	if len(items) != 1 || items[0].DatasetFile != firstFile || items[0].Name != point.Name {
		t.Fatalf("failed rerun replaced ready visualization: %+v", items)
	}

	now = now.Add(3 * time.Hour)
	secondJob := "job_cdefghijklmnopqrstuvwxyzab"
	if err := catalog.Begin(t.Context(), 42, secondJob, point); err != nil {
		t.Fatal(err)
	}
	if err := catalog.Reconcile(t.Context(), 42, AstrodomeJobStatus{ID: secondJob, State: "ready", Provider: "icon-eu", RunID: "2026072906", GridProfile: "dense-v1", GeometryDigest: "sha256:second"}); err != nil {
		t.Fatal(err)
	}
	items, _ = catalog.List(t.Context(), 42, false)
	if len(items) != 1 || items[0].DatasetFile == firstFile || items[0].ExpiresAt.Sub(now) != AstrodomeVisualizationTTL {
		t.Fatalf("successful rerun did not replace/reset TTL: %+v", items)
	}
	if _, err := os.Stat(filepath.Join(catalog.entries, firstFile)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("old dataset still exists: %v", err)
	}
	dataset, err := catalog.Open(t.Context(), 42, items[0].ID, false)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := gzip.NewReader(dataset.Body)
	if err != nil {
		t.Fatal(err)
	}
	content, err := io.ReadAll(reader)
	_ = reader.Close()
	_ = dataset.Body.Close()
	if err != nil || string(content) != visualizationAvailabilityJSON(1, 0, 0) {
		t.Fatalf("archived content = %q, err=%v", content, err)
	}
}

func TestVisualizationCleanupPreservesOldPendingJobsAndAbandonIsJobScoped(t *testing.T) {
	now := time.Date(2026, 7, 29, 9, 0, 0, 0, time.UTC)
	store := newMemoryVisualizationStore()
	catalog, err := newTestVisualizationCatalog(t, store, &visualizationGateway{body: visualizationAvailabilityJSON(1, 0, 0)}, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	first := "job_abcdefghijklmnopqrstuvwxyz"
	second := "job_bcdefghijklmnopqrstuvwxyza"
	if err := catalog.Begin(t.Context(), 42, first, SavedPoint{Name: "One", Latitude: 1, Longitude: 2}); err != nil {
		t.Fatal(err)
	}
	if err := catalog.Begin(t.Context(), 42, second, SavedPoint{Name: "Two", Latitude: 3, Longitude: 4}); err != nil {
		t.Fatal(err)
	}
	now = now.Add(7 * 24 * time.Hour)
	if err := catalog.cleanup(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PendingVisualization(t.Context(), 42, first); err != nil {
		t.Fatalf("old but non-terminal pending job was removed: %v", err)
	}
	if err := store.AbandonVisualization(t.Context(), 42, first); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PendingVisualization(t.Context(), 42, second); err != nil {
		t.Fatalf("abandoning one coordinate removed another pending job: %v", err)
	}
}

func TestVisualizationCatalogCleanupRemovesExpiredAndOrphanFiles(t *testing.T) {
	now := time.Date(2026, 7, 29, 9, 0, 0, 0, time.UTC)
	store := newMemoryVisualizationStore()
	catalog, err := newTestVisualizationCatalog(t, store, &visualizationGateway{body: visualizationAvailabilityJSON(1, 0, 0)}, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	jobID := "job_abcdefghijklmnopqrstuvwxyz"
	if err := catalog.Begin(t.Context(), 42, jobID, SavedPoint{Latitude: 1, Longitude: 2}); err != nil {
		t.Fatal(err)
	}
	if err := catalog.Reconcile(t.Context(), 42, AstrodomeJobStatus{ID: jobID, State: "ready", Provider: "icon-eu", RunID: "2026072900", GridProfile: "dense-v1", GeometryDigest: "sha256:value"}); err != nil {
		t.Fatal(err)
	}
	orphan := "viz_0123456789abcdef0123456789abcdef.json.gz"
	if err := os.WriteFile(filepath.Join(catalog.entries, orphan), []byte("orphan"), 0o440); err != nil {
		t.Fatal(err)
	}
	now = now.Add(AstrodomeVisualizationTTL + time.Second)
	if err := catalog.cleanup(t.Context()); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(catalog.entries)
	if err != nil || len(entries) != 0 {
		t.Fatalf("entries after cleanup = %v, err=%v", entries, err)
	}
	items, _ := catalog.List(t.Context(), 42, false)
	if len(items) != 0 {
		t.Fatalf("expired metadata remained: %+v", items)
	}
}

func TestVisualizationCatalogAdminFixtureIsPermanentSharedAndNotReplaced(t *testing.T) {
	now := time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC)
	store := newMemoryVisualizationStore()
	catalog, err := newTestVisualizationCatalog(t, store, &visualizationGateway{body: visualizationAvailabilityJSON(1, 0, 0)}, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	fileName, bytesWritten, digest, etag, err := catalog.writeDataset(t.Context(), AstrodomeDataset{
		Body: io.NopCloser(strings.NewReader(visualizationAvailabilityJSON(1, 0, 0))),
	})
	if err != nil {
		t.Fatal(err)
	}
	const fixtureID = "viz_0123456789abcdef0123456789abcdef"
	coordinateKey := canonicalCoordinateKey(53.650005, 37.346192)
	store.rows[fixtureID] = visualizationMemoryRow{owner: 42, ready: &AstrodomeVisualization{
		ID: fixtureID, Name: "Permanent test point", Latitude: 53.650005, Longitude: 37.346192,
		Provider: "icon-eu", RunID: "2026073012", GridProfile: "sparse-storage-v1",
		GeometryDigest: "sha256:fixture", DatasetBytes: bytesWritten,
		GeneratedAt: now.Add(-7 * 24 * time.Hour), ExpiresAt: now.Add(-3 * 24 * time.Hour), AdminFixture: true,
		SourceJobID: "job_fixtureabcdefghijklmnopqr", DatasetFile: fileName, DatasetSHA256: digest, ETag: etag,
	}}
	store.byUser[42] = map[string]string{coordinateKey: fixtureID}

	if items, listErr := catalog.List(t.Context(), 7, false); listErr != nil || len(items) != 0 {
		t.Fatalf("ordinary user fixtures = %+v, err=%v", items, listErr)
	}
	if items, listErr := catalog.List(t.Context(), 42, false); listErr != nil || len(items) != 0 {
		t.Fatalf("fixture owner without current admin access = %+v, err=%v", items, listErr)
	}
	if _, openErr := catalog.Open(t.Context(), 42, fixtureID, false); !errors.Is(openErr, ErrVisualizationNotFound) {
		t.Fatalf("fixture owner without current admin access opened fixture: %v", openErr)
	}
	if _, openErr := catalog.OpenByJob(t.Context(), 42, "job_fixtureabcdefghijklmnopqr", false); !errors.Is(openErr, ErrVisualizationNotFound) {
		t.Fatalf("fixture owner without current admin access opened fixture by job: %v", openErr)
	}
	if _, listErr := catalog.List(t.Context(), 0, false); !errors.Is(listErr, ErrVisualizationNotFound) {
		t.Fatalf("anonymous catalogue without fixture access = %v", listErr)
	}
	guestItems, err := catalog.List(t.Context(), 0, true)
	if err != nil || len(guestItems) != 1 || guestItems[0].ID != fixtureID || !guestItems[0].AdminFixture {
		t.Fatalf("anonymous fixture catalogue = %+v, err=%v", guestItems, err)
	}
	guestDataset, err := catalog.Open(t.Context(), 0, fixtureID, true)
	if err != nil {
		t.Fatalf("anonymous open fixture: %v", err)
	}
	_ = guestDataset.Body.Close()
	items, err := catalog.List(t.Context(), 7, true)
	if err != nil || len(items) != 1 || items[0].ID != fixtureID || !items[0].AdminFixture {
		t.Fatalf("administrator fixtures = %+v, err=%v", items, err)
	}
	dataset, err := catalog.Open(t.Context(), 7, fixtureID, true)
	if err != nil {
		t.Fatalf("administrator open fixture: %v", err)
	}
	_ = dataset.Body.Close()
	dataset, err = catalog.OpenByJob(t.Context(), 7, "job_fixtureabcdefghijklmnopqr", true)
	if err != nil {
		t.Fatalf("administrator open shared fixture by source job: %v", err)
	}
	_ = dataset.Body.Close()

	if err := catalog.cleanup(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(catalog.entries, fileName)); err != nil {
		t.Fatalf("fixture file was removed by cleanup: %v", err)
	}
	if afterCleanup, listErr := catalog.List(t.Context(), 7, true); listErr != nil || len(afterCleanup) != 1 || !afterCleanup[0].AdminFixture {
		t.Fatalf("fixture metadata was removed by cleanup: %+v, err=%v", afterCleanup, listErr)
	}

	jobID := "job_abcdefghijklmnopqrstuvwxyz"
	if err := catalog.Begin(t.Context(), 42, jobID, SavedPoint{
		Name: "fresh rerun", Latitude: 53.650005, Longitude: 37.346192,
	}); err != nil {
		t.Fatal(err)
	}
	if row := store.rows[fixtureID]; row.ready == nil || !row.ready.AdminFixture || row.pending.JobID != "" {
		t.Fatalf("rerun changed immutable fixture: %+v", row)
	}
	if _, err := store.PendingVisualization(t.Context(), 42, jobID); err != nil {
		t.Fatalf("rerun did not receive a separate mutable row: %v", err)
	}
	if err := catalog.Reconcile(t.Context(), 42, AstrodomeJobStatus{
		ID: jobID, State: "ready", Provider: "icon-eu", RunID: "2026080100",
		GridProfile: "sparse-storage-v1", GeometryDigest: "sha256:fresh",
	}); err != nil {
		t.Fatalf("complete rerun beside fixture: %v", err)
	}
	ownerItems, err := catalog.List(t.Context(), 42, false)
	if err != nil || len(ownerItems) != 1 || ownerItems[0].AdminFixture || ownerItems[0].RunID != "2026080100" {
		t.Fatalf("ordinary owner rerun catalogue = %+v, err=%v", ownerItems, err)
	}
	adminItems, err := catalog.List(t.Context(), 42, true)
	if err != nil || len(adminItems) != 2 || !adminItems[0].AdminFixture || adminItems[1].AdminFixture {
		t.Fatalf("administrator fixture plus rerun catalogue = %+v, err=%v", adminItems, err)
	}
	otherAdminItems, err := catalog.List(t.Context(), 7, true)
	if err != nil || len(otherAdminItems) != 1 || !otherAdminItems[0].AdminFixture {
		t.Fatalf("other administrator shared fixture catalogue = %+v, err=%v", otherAdminItems, err)
	}
}

func TestVisualizationCatalogPromotesNoWorseAdminFixtureAcrossGridProfiles(t *testing.T) {
	for _, test := range []struct {
		name                 string
		oldTotal, oldMissing int
		newTotal, newMissing int
	}{
		{name: "better fraction", oldTotal: 353, oldMissing: 100, newTotal: 129, newMissing: 20},
		{name: "equal fraction with different denominators", oldTotal: 10, oldMissing: 2, newTotal: 5, newMissing: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			now := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
			store := newMemoryVisualizationStore()
			gateway := &visualizationGateway{body: visualizationAvailabilityJSON(test.newTotal, test.newMissing, 0)}
			catalog, err := newTestVisualizationCatalog(t, store, gateway, func() time.Time { return now })
			if err != nil {
				t.Fatal(err)
			}
			oldFile, oldBytes, oldDigest, oldETag, err := catalog.writeDataset(t.Context(), AstrodomeDataset{
				Body: io.NopCloser(strings.NewReader(visualizationAvailabilityJSON(test.oldTotal, test.oldMissing, 3))),
			})
			if err != nil {
				t.Fatal(err)
			}
			const fixtureID = "viz_0123456789abcdef0123456789abcdef"
			const fixtureJobID = "job_fixtureabcdefghijklmnopqr"
			point := SavedPoint{Name: "fresh rerun", Latitude: 53.650005, Longitude: 37.346192}
			fixtureExpiry := now.Add(-72 * time.Hour)
			store.rows[fixtureID] = visualizationMemoryRow{owner: 42, ready: &AstrodomeVisualization{
				ID: fixtureID, Name: "Permanent test point", Latitude: point.Latitude, Longitude: point.Longitude,
				Provider: "icon-eu", RunID: "2026073012", GridProfile: "dense-v1",
				GeometryDigest: "sha256:old", DatasetBytes: oldBytes,
				GeneratedAt: now.Add(-7 * 24 * time.Hour), ExpiresAt: fixtureExpiry, AdminFixture: true,
				SourceJobID: fixtureJobID, DatasetFile: oldFile, DatasetSHA256: oldDigest, ETag: oldETag,
			}}

			jobID := "job_abcdefghijklmnopqrstuvwxyz"
			if err := catalog.Begin(t.Context(), 42, jobID, point); err != nil {
				t.Fatal(err)
			}
			if err := catalog.Reconcile(t.Context(), 42, AstrodomeJobStatus{
				ID: jobID, State: "ready", Provider: "icon-eu", RunID: "2026080100",
				GridProfile: "production-v2", GeometryDigest: "sha256:new",
			}); err != nil {
				t.Fatal(err)
			}

			fixture := store.rows[fixtureID].ready
			if fixture == nil || !fixture.AdminFixture || fixture.RunID != "2026080100" ||
				fixture.GridProfile != "production-v2" || fixture.SourceJobID != fixtureJobID ||
				!fixture.ExpiresAt.Equal(fixtureExpiry) || fixture.Name != "Permanent test point" ||
				fixture.DatasetFile == oldFile {
				t.Fatalf("promoted administrator fixture = %+v", fixture)
			}
			if _, statErr := os.Stat(filepath.Join(catalog.entries, oldFile)); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("superseded fixture file still exists: %v", statErr)
			}
			ordinary, err := catalog.List(t.Context(), 42, false)
			if err != nil || len(ordinary) != 1 || ordinary[0].AdminFixture {
				t.Fatalf("ordinary rerun catalogue = %+v, err=%v", ordinary, err)
			}
			fixtureInfo, err := os.Stat(filepath.Join(catalog.entries, fixture.DatasetFile))
			if err != nil {
				t.Fatal(err)
			}
			ordinaryInfo, err := os.Stat(filepath.Join(catalog.entries, ordinary[0].DatasetFile))
			if err != nil {
				t.Fatal(err)
			}
			if !os.SameFile(fixtureInfo, ordinaryInfo) || fixture.DatasetSHA256 != ordinary[0].DatasetSHA256 {
				t.Fatal("administrator fixture is not an independent durable link to the winning dataset")
			}
			otherAdmin, err := catalog.List(t.Context(), 7, true)
			if err != nil || len(otherAdmin) != 1 || otherAdmin[0].ID != fixtureID || otherAdmin[0].RunID != "2026080100" {
				t.Fatalf("shared promoted fixture = %+v, err=%v", otherAdmin, err)
			}
		})
	}
}

func TestVisualizationCatalogDoesNotPromoteWorseAdminFixture(t *testing.T) {
	for _, test := range []struct {
		name                 string
		oldTotal, oldMissing int
		newTotal, newMissing int
	}{
		{name: "worse fraction", oldTotal: 10, oldMissing: 2, newTotal: 5, newMissing: 2},
		{name: "fewer raw failures but worse fraction", oldTotal: 353, oldMissing: 100, newTotal: 129, newMissing: 40},
	} {
		t.Run(test.name, func(t *testing.T) {
			now := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
			store := newMemoryVisualizationStore()
			gateway := &visualizationGateway{body: visualizationAvailabilityJSON(test.newTotal, test.newMissing, 0)}
			catalog, err := newTestVisualizationCatalog(t, store, gateway, func() time.Time { return now })
			if err != nil {
				t.Fatal(err)
			}
			oldFile, oldBytes, oldDigest, oldETag, err := catalog.writeDataset(t.Context(), AstrodomeDataset{
				Body: io.NopCloser(strings.NewReader(visualizationAvailabilityJSON(test.oldTotal, test.oldMissing, 0))),
			})
			if err != nil {
				t.Fatal(err)
			}
			const fixtureID = "viz_0123456789abcdef0123456789abcdef"
			point := SavedPoint{Name: "rerun", Latitude: 53.650005, Longitude: 37.346192}
			store.rows[fixtureID] = visualizationMemoryRow{owner: 42, ready: &AstrodomeVisualization{
				ID: fixtureID, Name: "Permanent test point", Latitude: point.Latitude, Longitude: point.Longitude,
				Provider: "icon-eu", RunID: "2026073012", GridProfile: "dense-v1",
				GeometryDigest: "sha256:old", DatasetBytes: oldBytes, GeneratedAt: now.Add(-time.Hour),
				ExpiresAt: now.Add(-time.Hour), AdminFixture: true, SourceJobID: "job_fixtureabcdefghijklmnopqr",
				DatasetFile: oldFile, DatasetSHA256: oldDigest, ETag: oldETag,
			}}
			jobID := "job_abcdefghijklmnopqrstuvwxyz"
			if err := catalog.Begin(t.Context(), 42, jobID, point); err != nil {
				t.Fatal(err)
			}
			if err := catalog.Reconcile(t.Context(), 42, AstrodomeJobStatus{
				ID: jobID, State: "ready", Provider: "icon-eu", RunID: "2026080100",
				GridProfile: "production-v2", GeometryDigest: "sha256:new",
			}); err != nil {
				t.Fatal(err)
			}
			fixture := store.rows[fixtureID].ready
			if fixture == nil || fixture.RunID != "2026073012" || fixture.DatasetFile != oldFile ||
				fixture.DatasetSHA256 != oldDigest {
				t.Fatalf("worse rerun changed fixture: %+v", fixture)
			}
			if _, err := os.Stat(filepath.Join(catalog.entries, oldFile)); err != nil {
				t.Fatalf("unchanged fixture file is missing: %v", err)
			}
		})
	}
}

func TestVisualizationCatalogDoesNotPromoteInvalidAdminFixtureCandidate(t *testing.T) {
	now := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	store := newMemoryVisualizationStore()
	gateway := &visualizationGateway{body: `{"frames":[{"nodes":[{"state":"fabricated"}]}]}`}
	catalog, err := newTestVisualizationCatalog(t, store, gateway, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	oldFile, oldBytes, oldDigest, oldETag, err := catalog.writeDataset(t.Context(), AstrodomeDataset{
		Body: io.NopCloser(strings.NewReader(visualizationAvailabilityJSON(10, 2, 0))),
	})
	if err != nil {
		t.Fatal(err)
	}
	const fixtureID = "viz_0123456789abcdef0123456789abcdef"
	point := SavedPoint{Name: "rerun", Latitude: 53.650005, Longitude: 37.346192}
	store.rows[fixtureID] = visualizationMemoryRow{owner: 42, ready: &AstrodomeVisualization{
		ID: fixtureID, Name: "Permanent test point", Latitude: point.Latitude, Longitude: point.Longitude,
		Provider: "icon-eu", RunID: "2026073012", GridProfile: "dense-v1",
		GeometryDigest: "sha256:old", DatasetBytes: oldBytes, GeneratedAt: now.Add(-time.Hour),
		ExpiresAt: now.Add(-time.Hour), AdminFixture: true, SourceJobID: "job_fixtureabcdefghijklmnopqr",
		DatasetFile: oldFile, DatasetSHA256: oldDigest, ETag: oldETag,
	}}
	jobID := "job_abcdefghijklmnopqrstuvwxyz"
	if err := catalog.Begin(t.Context(), 42, jobID, point); err != nil {
		t.Fatal(err)
	}
	if err := catalog.Reconcile(t.Context(), 42, AstrodomeJobStatus{
		ID: jobID, State: "ready", Provider: "icon-eu", RunID: "2026080100",
		GridProfile: "production-v2", GeometryDigest: "sha256:new",
	}); err == nil {
		t.Fatal("invalid candidate was published")
	}
	fixture := store.rows[fixtureID].ready
	if fixture == nil || fixture.RunID != "2026073012" || fixture.DatasetFile != oldFile ||
		fixture.DatasetSHA256 != oldDigest {
		t.Fatalf("invalid rerun changed fixture: %+v", fixture)
	}
	if _, err := os.Stat(filepath.Join(catalog.entries, oldFile)); err != nil {
		t.Fatalf("invalid rerun removed fixture file: %v", err)
	}
}

func TestVisualizationCatalogRejectsUnsupportedDatasetBeforePublicationAndServing(t *testing.T) {
	now := time.Date(2026, 8, 9, 9, 0, 0, 0, time.UTC)
	store := newMemoryVisualizationStore()
	catalog, err := NewVisualizationCatalog(
		t.TempDir(), store, &visualizationGateway{}, func() time.Time { return now },
		func(source []byte) error {
			_, err := validateVisualizationAvailabilityFixture(bytes.NewReader(source))
			return err
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	old := `{"science_version":"astrodome-science-v28","frames":[]}`
	if _, _, _, _, err := catalog.writeDataset(t.Context(), AstrodomeDataset{
		Body: io.NopCloser(strings.NewReader(old)),
	}); err == nil {
		t.Fatal("unsupported dataset passed publication validation")
	}

	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	if _, err := io.WriteString(writer, old); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	const fileName = "viz_0123456789abcdef0123456789abcdef.json.gz"
	if err := os.WriteFile(filepath.Join(catalog.entries, fileName), compressed.Bytes(), 0o440); err != nil {
		t.Fatal(err)
	}
	digest := fmt.Sprintf("%x", sha256.Sum256(compressed.Bytes()))
	const visualizationID = "viz_0123456789abcdef0123456789abcdef"
	store.rows[visualizationID] = visualizationMemoryRow{owner: 42, ready: &AstrodomeVisualization{
		ID: visualizationID, Name: "Unsupported", Latitude: 53.65, Longitude: 37.35,
		Provider: "icon-eu", RunID: "2026080812", GridProfile: "production-v2",
		GeometryDigest: "sha256:old", DatasetBytes: int64(compressed.Len()), GeneratedAt: now,
		ExpiresAt: now.Add(AstrodomeVisualizationTTL), SourceJobID: "job_fixtureabcdefghijklmnopqr",
		DatasetFile: fileName, DatasetSHA256: digest, ETag: `"` + digest + `"`,
	}}
	store.byUser[42] = map[string]string{canonicalCoordinateKey(53.65, 37.35): visualizationID}

	items, err := catalog.List(t.Context(), 42, false)
	if err != nil || len(items) != 0 {
		t.Fatalf("unsupported catalogue entries = %+v, err=%v", items, err)
	}
	if _, err := catalog.Open(t.Context(), 42, visualizationID, false); !errors.Is(err, ErrVisualizationNotFound) {
		t.Fatalf("unsupported dataset open error = %v", err)
	}
}

func TestArchivedVisualizationAvailabilityTreatsTerrainAsResolved(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "dataset.json.gz")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	compressor := gzip.NewWriter(file)
	if _, err := io.WriteString(compressor, visualizationAvailabilityJSON(4, 1, 1)); err != nil {
		t.Fatal(err)
	}
	if err := errors.Join(compressor.Close(), file.Close()); err != nil {
		t.Fatal(err)
	}
	catalog, err := newTestVisualizationCatalog(t, newMemoryVisualizationStore(), &visualizationGateway{}, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	availability, err := catalog.archivedVisualizationAvailability(path)
	if err != nil || availability.total != 4 || availability.unavailable != 1 {
		t.Fatalf("availability = %+v, err=%v", availability, err)
	}
}

func TestNoWorseVisualizationAvailabilityUsesExactFractions(t *testing.T) {
	maximum := ^uint64(0)
	for _, test := range []struct {
		name               string
		candidate, current visualizationAvailability
		want               bool
	}{
		{
			name:      "dense to production improvement",
			candidate: visualizationAvailability{unavailable: 20, total: 129},
			current:   visualizationAvailability{unavailable: 100, total: 353},
			want:      true,
		},
		{
			name:      "equal rational fraction",
			candidate: visualizationAvailability{unavailable: 1, total: 5},
			current:   visualizationAvailability{unavailable: 2, total: 10},
			want:      true,
		},
		{
			name:      "fewer raw failures but worse fraction",
			candidate: visualizationAvailability{unavailable: 40, total: 129},
			current:   visualizationAvailability{unavailable: 100, total: 353},
		},
		{
			name:      "overflow-safe improvement",
			candidate: visualizationAvailability{unavailable: maximum - 2, total: maximum},
			current:   visualizationAvailability{unavailable: maximum - 1, total: maximum},
			want:      true,
		},
		{
			name:      "invalid zero-cardinality candidate",
			candidate: visualizationAvailability{},
			current:   visualizationAvailability{unavailable: 2, total: 10},
		},
		{
			name:      "invalid unavailable count",
			candidate: visualizationAvailability{unavailable: 11, total: 10},
			current:   visualizationAvailability{unavailable: 2, total: 10},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := noWorseVisualizationAvailability(test.candidate, test.current); got != test.want {
				t.Fatalf("noWorseVisualizationAvailability(%+v, %+v) = %v; want %v",
					test.candidate, test.current, got, test.want)
			}
		})
	}
}

func visualizationAvailabilityJSON(total, unavailable, terrainBlocked int) string {
	if total <= 0 || unavailable < 0 || terrainBlocked < 0 || unavailable+terrainBlocked > total {
		panic("invalid visualization availability fixture")
	}
	var result strings.Builder
	result.WriteString(`{"frames":[{"nodes":[`)
	for index := range total {
		if index > 0 {
			result.WriteByte(',')
		}
		state := "valid"
		switch {
		case index < unavailable:
			state = "unavailable"
		case index < unavailable+terrainBlocked:
			state = "terrain_blocked"
		}
		result.WriteString(`{"state":"`)
		result.WriteString(state)
		result.WriteString(`"}`)
	}
	result.WriteString(`]}]}`)
	return result.String()
}

func TestVisualizationMaintenanceArchivesReadyJobWithoutBrowserPolling(t *testing.T) {
	now := time.Date(2026, 7, 29, 9, 0, 0, 0, time.UTC)
	jobID := "job_abcdefghijklmnopqrstuvwxyz"
	gateway := &visualizationGateway{
		body: visualizationAvailabilityJSON(1, 0, 0), statuses: map[string]AstrodomeJobStatus{
			jobID: {ID: jobID, State: "ready", Provider: "icon-eu", RunID: "2026072900", GridProfile: "dense-v1", GeometryDigest: "sha256:value"},
		},
	}
	store := newMemoryVisualizationStore()
	catalog, err := newTestVisualizationCatalog(t, store, gateway, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	if err := catalog.Begin(t.Context(), 42, jobID, SavedPoint{Name: "Site", Latitude: 1, Longitude: 2}); err != nil {
		t.Fatal(err)
	}
	if err := catalog.reconcilePending(t.Context()); err != nil {
		t.Fatal(err)
	}
	items, err := catalog.List(t.Context(), 42, false)
	if err != nil || len(items) != 1 || items[0].SourceJobID != jobID || gateway.statusCalls != 1 {
		t.Fatalf("background archive = %+v, calls=%d, err=%v", items, gateway.statusCalls, err)
	}
}

func TestVisualizationMaintenancePreservesReadyOnTerminalRerunAndBacksOffErrors(t *testing.T) {
	now := time.Date(2026, 7, 29, 9, 0, 0, 0, time.UTC)
	firstJob := "job_abcdefghijklmnopqrstuvwxyz"
	terminalJob := "job_bcdefghijklmnopqrstuvwxyza"
	gateway := &visualizationGateway{body: visualizationAvailabilityJSON(1, 0, 0)}
	store := newMemoryVisualizationStore()
	catalog, err := newTestVisualizationCatalog(t, store, gateway, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	point := SavedPoint{Name: "Site", Latitude: 1, Longitude: 2}
	if err := catalog.Begin(t.Context(), 42, firstJob, point); err != nil {
		t.Fatal(err)
	}
	if err := catalog.Reconcile(t.Context(), 42, AstrodomeJobStatus{ID: firstJob, State: "ready", Provider: "icon-eu", RunID: "2026072900", GridProfile: "dense-v1", GeometryDigest: "sha256:first"}); err != nil {
		t.Fatal(err)
	}
	original, _ := catalog.List(t.Context(), 42, false)
	for _, state := range []string{"failed", "cancelled"} {
		if err := catalog.Begin(t.Context(), 42, terminalJob, point); err != nil {
			t.Fatal(err)
		}
		gateway.statuses = map[string]AstrodomeJobStatus{terminalJob: {ID: terminalJob, State: state}}
		if err := catalog.reconcilePending(t.Context()); err != nil {
			t.Fatal(err)
		}
		items, _ := catalog.List(t.Context(), 42, false)
		if len(items) != 1 || items[0].DatasetFile != original[0].DatasetFile {
			t.Fatalf("%s rerun replaced prior ready item: %+v", state, items)
		}
	}

	errorJob := "job_cdefghijklmnopqrstuvwxyzab"
	if err := catalog.Begin(t.Context(), 42, errorJob, SavedPoint{Latitude: 3, Longitude: 4}); err != nil {
		t.Fatal(err)
	}
	gateway.statusErr = errors.New("temporary gateway outage")
	if err := catalog.reconcilePending(t.Context()); err == nil {
		t.Fatal("expected transient status error")
	}
	calls := gateway.statusCalls
	now = now.Add(visualizationPendingInterval / 2)
	if err := catalog.reconcilePending(t.Context()); err != nil {
		t.Fatalf("backoff pass returned error without polling: %v", err)
	}
	if gateway.statusCalls != calls {
		t.Fatalf("status retried during backoff: calls=%d want=%d", gateway.statusCalls, calls)
	}
	if _, err := store.PendingVisualization(t.Context(), 42, errorJob); err != nil {
		t.Fatalf("transient error removed pending job: %v", err)
	}
}

var _ VisualizationMetadataStore = (*memoryVisualizationStore)(nil)
