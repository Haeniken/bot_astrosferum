package iconeu

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"bot_astrosferum/internal/model"
)

const (
	// The input-volume allowance is deliberately identical for both rendering
	// profiles. A profile never changes the native ICON fields downloaded by
	// this package. Three MiB per decompressed regular-grid message is a
	// conservative admission estimate, not a scientific or compression model.
	domeProjectedBytesPerMessage uint64 = 3 << 20
	domeAcquisitionOverheadBytes uint64 = 64 << 20
	domeAcquisitionOverheadFiles uint64 = 256

	// Only the future immutable result-cache allowance differs by profile.
	// Consequently sparse can be selected only by DiskBudget.ReserveProfile
	// when the dense projected project peak crosses the configured hard cap;
	// transport failures and latency can never trigger a profile downgrade.
	domeDenseResultCacheBytes  uint64 = 16 << 30
	domeSparseResultCacheBytes uint64 = 4 << 30
	domeDenseResultCacheFiles  uint64 = 32 << 10
	domeSparseResultCacheFiles uint64 = 8 << 10

	domeStagingProvider  = "icon-eu-astrodome-staging"
	domeCheckpointSchema = 1
)

type domeMessage struct {
	ShortName    string
	TypeOfLevel  string
	Level        string
	StepRange    string
	ValidityDate int
	ValidityTime int
}

type domeInventory map[domeMessage]struct{}

type domeBaseInput struct {
	loaded               LoadedManifest
	manifestSHA256       string
	providerRoot         string
	geometry             DomeStepFile
	modelSteps           map[int]DomeStepFile
	modelBaseInventories map[int]domeInventory
	surfaceIdentity      map[string]domeMessage
}

type domeTaskKind uint8

const (
	domeTaskModel domeTaskKind = iota
	domeTaskSurface
)

type domeSyncTask struct {
	index               int
	kind                domeTaskKind
	forecastHour        int
	validAt             time.Time
	destination         string
	manifestFile        string
	expected            domeInventory
	baseManifestSHA256  string
	inputContractSHA256 string
}

type domeTaskResult struct {
	index int
	file  DomeStepFile
	err   error
}

type domeObjectCheckpoint struct {
	Schema              int          `json:"schema"`
	BaseManifestSHA256  string       `json:"base_manifest_sha256"`
	InputContractSHA256 string       `json:"input_contract_sha256"`
	File                DomeStepFile `json:"file"`
}

// SyncDome acquires one immutable native ICON-EU Astrodome volume. It reuses
// every base cloud/surface message and the complete HHL geometry, downloads
// only the missing model-level extension, and atomically advances
// dome-ready-current only after every object and the bound base manifest have
// been revalidated. A stable staging directory survives process restarts.
func (client *Client) SyncDome(
	ctx context.Context,
	dataRoot string,
	loaded LoadedManifest,
	diskBudget *model.DiskBudget,
) (LoadedDomeManifest, error) {
	return client.syncDome(ctx, dataRoot, loaded, diskBudget, domeSyncProjections)
}

func (client *Client) syncDome(
	ctx context.Context,
	dataRoot string,
	loaded LoadedManifest,
	diskBudget *model.DiskBudget,
	project func([]domeSyncTask) (model.DiskProjection, model.DiskProjection, error),
) (result LoadedDomeManifest, resultErr error) {
	client.defaults()
	if ctx == nil {
		return LoadedDomeManifest{}, errors.New("ICON-EU Astrodome sync context is required")
	}
	if diskBudget == nil {
		return LoadedDomeManifest{}, errors.New("ICON-EU Astrodome disk budget is required")
	}
	if project == nil {
		return LoadedDomeManifest{}, errors.New("ICON-EU Astrodome disk projection is required")
	}
	if strings.TrimSpace(dataRoot) == "" {
		return LoadedDomeManifest{}, errors.New("ICON-EU Astrodome data root is required")
	}

	contractDigest := DomeInputContractDigest()
	leaseManager, err := model.NewRunLeaseManager(filepath.Join(dataRoot, "state", "run-leases"))
	if err != nil {
		return LoadedDomeManifest{}, fmt.Errorf("create ICON-EU Astrodome run lease manager: %w", err)
	}
	lease, err := leaseManager.AcquireExclusive(ctx, domeStagingProvider, loaded.RunID, contractDigest)
	if err != nil {
		return LoadedDomeManifest{}, fmt.Errorf("acquire ICON-EU Astrodome staging lease: %w", err)
	}
	defer func() { resultErr = errors.Join(resultErr, lease.Close()) }()

	base, err := client.prepareDomeBase(ctx, dataRoot, loaded)
	if err != nil {
		return LoadedDomeManifest{}, err
	}
	finalDirectory := filepath.Join(base.providerRoot, "dome-runs", loaded.RunID, contractDigest)
	if info, statErr := os.Stat(finalDirectory); statErr == nil {
		if !info.IsDir() {
			return LoadedDomeManifest{}, fmt.Errorf("ICON-EU Astrodome final path is not a directory: %s", finalDirectory)
		}
		ready, loadErr := LoadDomeManifest(filepath.Join(finalDirectory, "manifest.json"))
		if loadErr != nil {
			return LoadedDomeManifest{}, fmt.Errorf("existing ICON-EU Astrodome publication is invalid: %w", loadErr)
		}
		if ready.BaseManifestSHA256 != base.manifestSHA256 || ready.InputContractSHA256 != contractDigest {
			return LoadedDomeManifest{}, errors.New("existing ICON-EU Astrodome publication is bound to a different base manifest or input contract")
		}
		if err := client.verifyPublishedDome(ctx, base, ready); err != nil {
			return LoadedDomeManifest{}, err
		}
		if err := publishCurrentDomeManifest(dataRoot, ready); err != nil {
			return LoadedDomeManifest{}, fmt.Errorf("publish current ICON-EU Astrodome run: %w", err)
		}
		return ready, nil
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return LoadedDomeManifest{}, fmt.Errorf("inspect ICON-EU Astrodome final path: %w", statErr)
	}

	stagingDirectory := filepath.Join(base.providerRoot, "dome-staging", loaded.RunID, contractDigest)
	if err := validateDomeStagingBinding(stagingDirectory, base.manifestSHA256); err != nil {
		return LoadedDomeManifest{}, err
	}
	tasks, err := buildDomeSyncTasks(base, stagingDirectory, contractDigest)
	if err != nil {
		return LoadedDomeManifest{}, err
	}
	denseProjection, sparseProjection, err := project(tasks)
	if err != nil {
		return LoadedDomeManifest{}, err
	}
	reservation, profile, err := diskBudget.ReserveProfile(
		ctx, "icon-eu-astrodome-"+loaded.RunID+"-"+contractDigest,
		denseProjection, sparseProjection,
	)
	if err != nil {
		return LoadedDomeManifest{}, fmt.Errorf("reserve ICON-EU Astrodome disk budget: %w", err)
	}
	defer func() { resultErr = errors.Join(resultErr, reservation.Release()) }()

	if err := os.MkdirAll(filepath.Join(stagingDirectory, "model"), 0o750); err != nil {
		return LoadedDomeManifest{}, fmt.Errorf("create ICON-EU Astrodome model staging: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(stagingDirectory, "surface"), 0o750); err != nil {
		return LoadedDomeManifest{}, fmt.Errorf("create ICON-EU Astrodome surface staging: %w", err)
	}
	if err := bindDomeStaging(stagingDirectory, base.manifestSHA256); err != nil {
		return LoadedDomeManifest{}, err
	}

	files, err := client.runDomeTasks(ctx, loaded.RunID, tasks)
	if err != nil {
		return LoadedDomeManifest{}, err
	}
	if err := requireUnchangedDomeBase(filepath.Join(base.loaded.Directory, "manifest.json"), base.manifestSHA256); err != nil {
		return LoadedDomeManifest{}, err
	}

	manifest := NewDomeManifest(loaded.RunID, loaded.BaseTime, base.manifestSHA256)
	manifest.InputContractSHA256 = contractDigest
	manifest.GridProfile = profile
	manifest.Geometry = base.geometry
	manifest.ModelSteps = make([]DomeModelStep, 0, len(DomeNativeForecastHours()))
	for index, hour := range DomeNativeForecastHours() {
		parts := make([]DomeStepFile, 0, 2)
		if hour <= 78 {
			parts = append(parts, base.modelSteps[hour])
		}
		parts = append(parts, files[index])
		manifest.ModelSteps = append(manifest.ModelSteps, DomeModelStep{
			ForecastHour: hour, ValidAt: loaded.BaseTime.Add(time.Duration(hour) * time.Hour),
			Parts: parts, Messages: domeModelMessagesPerStep,
		})
	}
	modelTaskCount := len(DomeNativeForecastHours())
	manifest.SurfaceExtensionSteps = append([]DomeStepFile(nil), files[modelTaskCount:]...)
	manifest.PublishedAt = time.Now().UTC()
	manifest.Complete = true
	if err := writeDomeManifest(filepath.Join(stagingDirectory, "manifest.json"), manifest); err != nil {
		return LoadedDomeManifest{}, fmt.Errorf("write ICON-EU Astrodome manifest: %w", err)
	}
	if err := requireUnchangedDomeBase(filepath.Join(base.loaded.Directory, "manifest.json"), base.manifestSHA256); err != nil {
		return LoadedDomeManifest{}, err
	}
	if err := os.MkdirAll(filepath.Dir(finalDirectory), 0o750); err != nil {
		return LoadedDomeManifest{}, fmt.Errorf("create ICON-EU Astrodome publication parent: %w", err)
	}
	if err := os.Rename(stagingDirectory, finalDirectory); err != nil {
		return LoadedDomeManifest{}, fmt.Errorf("publish immutable ICON-EU Astrodome directory: %w", err)
	}
	if err := syncDomeDirectory(filepath.Dir(finalDirectory)); err != nil {
		return LoadedDomeManifest{}, fmt.Errorf("sync ICON-EU Astrodome publication parent: %w", err)
	}
	ready, err := LoadDomeManifest(filepath.Join(finalDirectory, "manifest.json"))
	if err != nil {
		return LoadedDomeManifest{}, fmt.Errorf("reload published ICON-EU Astrodome manifest: %w", err)
	}
	if err := publishCurrentDomeManifest(dataRoot, ready); err != nil {
		return LoadedDomeManifest{}, fmt.Errorf("publish current ICON-EU Astrodome run: %w", err)
	}
	return ready, nil
}

func (client *Client) prepareDomeBase(ctx context.Context, dataRoot string, supplied LoadedManifest) (domeBaseInput, error) {
	if err := supplied.Validate(); err != nil {
		return domeBaseInput{}, fmt.Errorf("invalid ICON-EU Astrodome base manifest: %w", err)
	}
	parsedRun, err := time.Parse("2006010215", supplied.RunID)
	if err != nil || !parsedRun.Equal(supplied.BaseTime) || supplied.BaseTime.Location() != time.UTC {
		return domeBaseInput{}, errors.New("ICON-EU Astrodome base run identity is inconsistent")
	}
	if !supplied.HasHourlyCloud() || !supplied.HasAstrodomeSurface() {
		return domeBaseInput{}, errors.New("ICON-EU Astrodome requires complete hourly cloud and surface base bundles")
	}
	providerRoot, err := filepath.Abs(filepath.Join(dataRoot, "models", "icon-eu"))
	if err != nil {
		return domeBaseInput{}, fmt.Errorf("resolve ICON-EU provider root: %w", err)
	}
	wantDirectory := filepath.Join(providerRoot, "runs", supplied.RunID)
	directory, err := filepath.Abs(supplied.Directory)
	if err != nil || directory != wantDirectory {
		return domeBaseInput{}, errors.New("ICON-EU Astrodome base manifest is outside its immutable run directory")
	}
	manifestPath := filepath.Join(directory, "manifest.json")
	fresh, err := LoadManifest(manifestPath)
	if err != nil {
		return domeBaseInput{}, fmt.Errorf("reload ICON-EU Astrodome base manifest: %w", err)
	}
	suppliedJSON, _ := json.Marshal(supplied.Manifest)
	freshJSON, _ := json.Marshal(fresh.Manifest)
	if !bytes.Equal(suppliedJSON, freshJSON) {
		return domeBaseInput{}, errors.New("ICON-EU Astrodome caller manifest differs from the immutable manifest on disk")
	}
	manifestSHA, _, err := fileDigest(manifestPath)
	if err != nil {
		return domeBaseInput{}, fmt.Errorf("hash ICON-EU Astrodome base manifest: %w", err)
	}

	base := domeBaseInput{
		loaded: fresh, manifestSHA256: manifestSHA, providerRoot: providerRoot,
		modelSteps:           make(map[int]DomeStepFile, HourlySurfaceStepCount),
		modelBaseInventories: make(map[int]domeInventory, HourlySurfaceStepCount),
	}
	if fresh.CloudGeometry == nil || fresh.CloudGeometry.Messages != domeHalfLevelCount {
		return domeBaseInput{}, errors.New("ICON-EU Astrodome base HHL bundle must contain all native half levels 1 through 75")
	}
	geometryPath, geometryRelative, err := resolveDomeBaseFile(base, fresh.CloudGeometry.File)
	if err != nil {
		return domeBaseInput{}, err
	}
	geometryExpected := domeGeometryInventory(fresh.BaseTime)
	base.geometry, err = client.verifyDomeBaseBundle(ctx, geometryPath, geometryRelative, 0, fresh.BaseTime, *fresh.CloudGeometry, geometryExpected)
	if err != nil {
		return domeBaseInput{}, fmt.Errorf("validate ICON-EU Astrodome HHL base bundle: %w", err)
	}

	for hour := range HourlySurfaceStepCount {
		step := fresh.CloudSteps[hour]
		if step.ForecastHour != hour || !step.ValidAt.Equal(fresh.BaseTime.Add(time.Duration(hour)*time.Hour)) || step.Messages != cloudStepMessageCount() {
			return domeBaseInput{}, fmt.Errorf("ICON-EU Astrodome cloud base f%03d is inconsistent", hour)
		}
		path, relative, resolveErr := resolveDomeBaseFile(base, step.File)
		if resolveErr != nil {
			return domeBaseInput{}, resolveErr
		}
		expected := domeBaseCloudInventory(fresh.BaseTime, hour)
		bundle := BundleFile{File: step.File, Bytes: step.Bytes, SHA256: step.SHA256, Messages: step.Messages}
		verified, verifyErr := client.verifyDomeBaseBundle(ctx, path, relative, hour, step.ValidAt, bundle, expected)
		if verifyErr != nil {
			return domeBaseInput{}, fmt.Errorf("validate ICON-EU Astrodome cloud base f%03d: %w", hour, verifyErr)
		}
		base.modelSteps[hour] = verified
		base.modelBaseInventories[hour] = expected
	}

	var surfaceIdentity map[string]domeMessage
	for hour := range HourlySurfaceStepCount {
		step := fresh.SurfaceSteps[hour]
		if step.ForecastHour != hour || !step.ValidAt.Equal(fresh.BaseTime.Add(time.Duration(hour)*time.Hour)) || step.Messages != SurfaceBundleSchemaVersion {
			return domeBaseInput{}, fmt.Errorf("ICON-EU Astrodome surface base f%03d is inconsistent", hour)
		}
		path, _, resolveErr := resolveDomeBaseFile(base, step.File)
		if resolveErr != nil {
			return domeBaseInput{}, resolveErr
		}
		bundle := BundleFile{File: step.File, Bytes: step.Bytes, SHA256: step.SHA256, Messages: step.Messages}
		if _, _, verifyErr := verifyDomeFileIdentity(path, bundle); verifyErr != nil {
			return domeBaseInput{}, fmt.Errorf("validate ICON-EU Astrodome surface base f%03d: %w", hour, verifyErr)
		}
		if hour == 0 {
			surfaceIdentity, err = client.readCanonicalSurfaceIdentity(ctx, path, fresh.BaseTime, hour)
		} else {
			err = client.validateDomeInventory(ctx, path, domeSurfaceInventory(fresh.BaseTime, hour, surfaceIdentity))
		}
		if err != nil {
			return domeBaseInput{}, fmt.Errorf("validate ICON-EU Astrodome surface base f%03d: %w", hour, err)
		}
	}
	base.surfaceIdentity = surfaceIdentity
	if err := requireUnchangedDomeBase(manifestPath, manifestSHA); err != nil {
		return domeBaseInput{}, err
	}
	return base, nil
}

func resolveDomeBaseFile(base domeBaseInput, relative string) (string, string, error) {
	if unsafeRelativePath(relative) {
		return "", "", errors.New("ICON-EU Astrodome base bundle has an unsafe path")
	}
	absolute := filepath.Join(base.loaded.Directory, relative)
	providerRelative, err := filepath.Rel(base.providerRoot, absolute)
	wantPrefix := filepath.Join("runs", base.loaded.RunID) + string(filepath.Separator)
	if err != nil || filepath.IsAbs(providerRelative) || !strings.HasPrefix(providerRelative, wantPrefix) {
		return "", "", errors.New("ICON-EU Astrodome base bundle is outside the provider run")
	}
	return absolute, providerRelative, nil
}

func (client *Client) verifyDomeBaseBundle(
	ctx context.Context,
	path, manifestFile string,
	hour int,
	validAt time.Time,
	bundle BundleFile,
	expected domeInventory,
) (DomeStepFile, error) {
	allocated, size, err := verifyDomeFileIdentity(path, bundle)
	if err != nil {
		return DomeStepFile{}, err
	}
	if err := client.validateDomeInventory(ctx, path, expected); err != nil {
		return DomeStepFile{}, err
	}
	return DomeStepFile{
		Source: DomeFileSourceBaseRun, ForecastHour: hour, ValidAt: validAt,
		File: manifestFile, Bytes: size, AllocatedBytes: allocated,
		SHA256: bundle.SHA256, Messages: len(expected),
	}, nil
}

func verifyDomeFileIdentity(path string, want BundleFile) (allocated, size int64, resultErr error) {
	digest, size, err := fileDigest(path)
	if err != nil {
		return 0, 0, err
	}
	if size != want.Bytes || digest != want.SHA256 {
		return 0, 0, fmt.Errorf("%s does not match its base manifest size/SHA-256", filepath.Base(path))
	}
	allocated, err = domeAllocatedBytes(path)
	if err != nil {
		return 0, 0, err
	}
	return allocated, size, nil
}

func domeAllocatedBytes(path string) (int64, error) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Blocks < 0 || stat.Blocks > math.MaxInt64/512 {
		return 0, fmt.Errorf("read allocated blocks for %s", filepath.Base(path))
	}
	allocated := stat.Blocks * 512
	if allocated <= 0 {
		return 0, fmt.Errorf("%s has no allocated data blocks", filepath.Base(path))
	}
	return allocated, nil
}

func buildDomeSyncTasks(base domeBaseInput, stagingDirectory, contractDigest string) ([]domeSyncTask, error) {
	hours := DomeNativeForecastHours()
	tasks := make([]domeSyncTask, 0, len(hours)+2)
	for _, hour := range hours {
		expected := domeTargetModelInventory(base.loaded.BaseTime, hour)
		if hour <= 78 {
			for message := range base.modelBaseInventories[hour] {
				if _, exists := expected[message]; !exists {
					return nil, fmt.Errorf("ICON-EU Astrodome base f%03d contains a message outside the native contract", hour)
				}
				delete(expected, message)
			}
		}
		name := fmt.Sprintf("f%03d.grib2", hour)
		tasks = append(tasks, domeSyncTask{
			index: len(tasks), kind: domeTaskModel, forecastHour: hour,
			validAt:            base.loaded.BaseTime.Add(time.Duration(hour) * time.Hour),
			destination:        filepath.Join(stagingDirectory, "model", name),
			manifestFile:       filepath.Join("dome-runs", base.loaded.RunID, contractDigest, "model", name),
			expected:           expected,
			baseManifestSHA256: base.manifestSHA256, inputContractSHA256: contractDigest,
		})
	}
	for _, hour := range []int{81, 84} {
		name := fmt.Sprintf("f%03d.grib2", hour)
		tasks = append(tasks, domeSyncTask{
			index: len(tasks), kind: domeTaskSurface, forecastHour: hour,
			validAt:            base.loaded.BaseTime.Add(time.Duration(hour) * time.Hour),
			destination:        filepath.Join(stagingDirectory, "surface", name),
			manifestFile:       filepath.Join("dome-runs", base.loaded.RunID, contractDigest, "surface", name),
			expected:           domeSurfaceInventory(base.loaded.BaseTime, hour, base.surfaceIdentity),
			baseManifestSHA256: base.manifestSHA256, inputContractSHA256: contractDigest,
		})
	}
	return tasks, nil
}

func domeSyncProjections(tasks []domeSyncTask) (model.DiskProjection, model.DiskProjection, error) {
	missingMessages := uint64(0)
	missingFiles := uint64(0)
	for _, task := range tasks {
		_, err := os.Stat(task.destination)
		switch {
		case err == nil:
			continue
		case !errors.Is(err, os.ErrNotExist):
			return model.DiskProjection{}, model.DiskProjection{}, fmt.Errorf("inspect resumable ICON-EU Astrodome object: %w", err)
		}
		messages := uint64(len(task.expected))
		if math.MaxUint64-missingMessages < messages {
			return model.DiskProjection{}, model.DiskProjection{}, errors.New("ICON-EU Astrodome message projection overflow")
		}
		missingMessages += messages
		missingFiles++
	}
	if missingMessages > (math.MaxUint64-domeAcquisitionOverheadBytes)/domeProjectedBytesPerMessage {
		return model.DiskProjection{}, model.DiskProjection{}, errors.New("ICON-EU Astrodome byte projection overflow")
	}
	inputBytes := missingMessages*domeProjectedBytesPerMessage + domeAcquisitionOverheadBytes
	inputFiles := missingFiles + domeAcquisitionOverheadFiles
	return model.DiskProjection{
			Bytes: inputBytes + domeDenseResultCacheBytes, Inodes: inputFiles + domeDenseResultCacheFiles,
		}, model.DiskProjection{
			Bytes: inputBytes + domeSparseResultCacheBytes, Inodes: inputFiles + domeSparseResultCacheFiles,
		}, nil
}

func validateDomeStagingBinding(stagingDirectory, baseManifestSHA string) error {
	info, err := os.Stat(stagingDirectory)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect ICON-EU Astrodome staging: %w", err)
	}
	if !info.IsDir() {
		return errors.New("ICON-EU Astrodome staging path is not a directory")
	}
	encoded, err := os.ReadFile(filepath.Join(stagingDirectory, "base-manifest.sha256"))
	if errors.Is(err, os.ErrNotExist) {
		entries, readErr := os.ReadDir(stagingDirectory)
		if readErr != nil {
			return fmt.Errorf("inspect unbound ICON-EU Astrodome staging: %w", readErr)
		}
		for _, entry := range entries {
			if entry.IsDir() || strings.HasSuffix(entry.Name(), ".part") {
				continue
			}
			return errors.New("refuse to adopt ICON-EU Astrodome objects without a base-manifest binding")
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("read existing ICON-EU Astrodome base binding: %w", err)
	}
	if strings.TrimSpace(string(encoded)) != baseManifestSHA {
		return errors.New("refuse to resume ICON-EU Astrodome staging after the base manifest changed")
	}
	return nil
}

func bindDomeStaging(stagingDirectory, baseManifestSHA string) error {
	path := filepath.Join(stagingDirectory, "base-manifest.sha256")
	if encoded, err := os.ReadFile(path); err == nil {
		if strings.TrimSpace(string(encoded)) != baseManifestSHA {
			return errors.New("refuse to reuse ICON-EU Astrodome staging with a different base manifest")
		}
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read ICON-EU Astrodome base binding: %w", err)
	}
	temporary := path + ".part"
	_ = os.Remove(temporary)
	file, err := os.OpenFile(temporary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o640)
	if err != nil {
		return fmt.Errorf("create ICON-EU Astrodome base binding: %w", err)
	}
	failed := true
	defer func() {
		_ = file.Close()
		if failed {
			_ = os.Remove(temporary)
		}
	}()
	if _, err := fmt.Fprintln(file, baseManifestSHA); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporary, path); err != nil {
		return err
	}
	if err := syncDomeDirectory(stagingDirectory); err != nil {
		return err
	}
	failed = false
	return nil
}

func requireUnchangedDomeBase(path, wantSHA string) error {
	digest, _, err := fileDigest(path)
	if err != nil {
		return fmt.Errorf("recheck ICON-EU Astrodome base manifest: %w", err)
	}
	if digest != wantSHA {
		return errors.New("refuse to publish ICON-EU Astrodome after the base manifest changed")
	}
	return nil
}

func (client *Client) runDomeTasks(ctx context.Context, runID string, tasks []domeSyncTask) ([]DomeStepFile, error) {
	workContext, cancel := context.WithCancel(ctx)
	defer cancel()
	workers := client.Workers
	if workers < 1 {
		workers = 1
	}
	if workers > len(tasks) {
		workers = len(tasks)
	}
	jobs := make(chan domeSyncTask)
	results := make(chan domeTaskResult, len(tasks))
	var waitGroup sync.WaitGroup
	for range workers {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			for task := range jobs {
				file, err := client.ensureDomeTask(workContext, runID, task)
				results <- domeTaskResult{index: task.index, file: file, err: err}
				if err != nil {
					cancel()
					return
				}
			}
		}()
	}
	go func() {
		defer close(jobs)
		for _, task := range tasks {
			select {
			case jobs <- task:
			case <-workContext.Done():
				return
			}
		}
	}()
	go func() {
		waitGroup.Wait()
		close(results)
	}()

	files := make([]DomeStepFile, len(tasks))
	completed := 0
	var firstErr error
	for item := range results {
		if item.err != nil {
			if firstErr == nil {
				firstErr = item.err
			}
			continue
		}
		files[item.index] = item.file
		completed++
		client.Progress("ICON-EU %s Astrodome: verified f%03d (%d/%d)", runID, item.file.ForecastHour, completed, len(tasks))
	}
	if firstErr != nil {
		return nil, firstErr
	}
	if completed != len(tasks) {
		return nil, fmt.Errorf("ICON-EU Astrodome sync produced %d of %d immutable objects", completed, len(tasks))
	}
	return files, nil
}

func (client *Client) ensureDomeTask(ctx context.Context, runID string, task domeSyncTask) (DomeStepFile, error) {
	if _, err := os.Stat(task.destination); err == nil {
		return client.inspectDomeTask(ctx, task, nil)
	} else if !errors.Is(err, os.ErrNotExist) {
		return DomeStepFile{}, fmt.Errorf("inspect ICON-EU Astrodome f%03d: %w", task.forecastHour, err)
	}
	temporary := task.destination + ".part"
	checkpointPath := domeCheckpointPath(task.destination)
	if checkpoint, err := loadDomeCheckpoint(checkpointPath); err == nil {
		if _, statErr := os.Stat(temporary); statErr != nil {
			return DomeStepFile{}, fmt.Errorf("verified ICON-EU Astrodome checkpoint f%03d has no data object: %w", task.forecastHour, statErr)
		}
		got, inspectErr := client.inspectDomePath(ctx, task, temporary)
		if inspectErr != nil {
			return DomeStepFile{}, inspectErr
		}
		if err := validateDomeCheckpoint(task, checkpoint, got); err != nil {
			return DomeStepFile{}, err
		}
		if err := os.Rename(temporary, task.destination); err != nil {
			return DomeStepFile{}, fmt.Errorf("finish recovered ICON-EU Astrodome f%03d: %w", task.forecastHour, err)
		}
		if err := syncDomeDirectory(filepath.Dir(task.destination)); err != nil {
			return DomeStepFile{}, err
		}
		return client.inspectDomeTask(ctx, task, nil)
	} else if !errors.Is(err, os.ErrNotExist) {
		return DomeStepFile{}, fmt.Errorf("read ICON-EU Astrodome checkpoint f%03d: %w", task.forecastHour, err)
	}
	if err := os.Remove(temporary); err != nil && !errors.Is(err, os.ErrNotExist) {
		return DomeStepFile{}, fmt.Errorf("remove stale ICON-EU Astrodome partial object: %w", err)
	}
	if err := os.Remove(checkpointPath + ".part"); err != nil && !errors.Is(err, os.ErrNotExist) {
		return DomeStepFile{}, fmt.Errorf("remove stale ICON-EU Astrodome checkpoint partial: %w", err)
	}
	file, err := os.OpenFile(temporary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o640)
	if err != nil {
		return DomeStepFile{}, err
	}
	failed := true
	checkpointed := false
	defer func() {
		_ = file.Close()
		if failed && !checkpointed {
			_ = os.Remove(temporary)
		}
	}()
	if task.kind == domeTaskModel {
		err = client.appendDomeModelExtension(ctx, file, runID, task.forecastHour, task.expected)
	} else {
		err = client.appendDomeSurfaceExtension(ctx, file, runID, task.forecastHour)
	}
	if err != nil {
		return DomeStepFile{}, err
	}
	if err := file.Sync(); err != nil {
		return DomeStepFile{}, err
	}
	if err := file.Close(); err != nil {
		return DomeStepFile{}, err
	}
	verified, err := client.inspectDomePath(ctx, task, temporary)
	if err != nil {
		return DomeStepFile{}, err
	}
	checkpoint := domeObjectCheckpoint{
		Schema: domeCheckpointSchema, BaseManifestSHA256: task.baseManifestSHA256,
		InputContractSHA256: task.inputContractSHA256, File: verified,
	}
	if err := writeDomeCheckpoint(checkpointPath, checkpoint); err != nil {
		return DomeStepFile{}, err
	}
	checkpointed = true
	if err := os.Rename(temporary, task.destination); err != nil {
		return DomeStepFile{}, err
	}
	if err := syncDomeDirectory(filepath.Dir(task.destination)); err != nil {
		return DomeStepFile{}, err
	}
	failed = false
	return client.inspectDomeTask(ctx, task, nil)
}

func (client *Client) appendDomeModelExtension(ctx context.Context, file *os.File, runID string, hour int, expected domeInventory) error {
	validAt := timeFromRun(runID).Add(time.Duration(hour) * time.Hour)
	for _, level := range DomeFullModelLevels() {
		for _, field := range DomeModelFields() {
			if field.Stagger != DomeStaggerFull {
				continue
			}
			message := newDomeMessage(field.ShortName, "generalVerticalLayer", level, hour, validAt)
			if _, needed := expected[message]; !needed {
				continue
			}
			if err := client.appendField(ctx, file, client.modelLevelFieldURL(runID, hour, level, field.Directory, field.Code)); err != nil {
				return fmt.Errorf("download Astrodome f%03d full level %d %s: %w", hour, level, field.Code, err)
			}
		}
	}
	for _, level := range DomeHalfModelLevels() {
		for _, field := range DomeModelFields() {
			if field.Stagger != DomeStaggerHalf {
				continue
			}
			message := newDomeMessage(field.ShortName, "generalVertical", level, hour, validAt)
			if _, needed := expected[message]; !needed {
				continue
			}
			if err := client.appendField(ctx, file, client.modelLevelFieldURL(runID, hour, level, field.Directory, field.Code)); err != nil {
				return fmt.Errorf("download Astrodome f%03d half level %d %s: %w", hour, level, field.Code, err)
			}
		}
	}
	return nil
}

func (client *Client) appendDomeSurfaceExtension(ctx context.Context, file *os.File, runID string, hour int) error {
	if len(surfaceFields) != SurfaceBundleSchemaVersion {
		return errors.New("ICON-EU Astrodome surface schema changed without a contract version change")
	}
	for _, field := range surfaceFields {
		if err := client.appendField(ctx, file, client.surfaceFieldURL(runID, hour, field)); err != nil {
			return fmt.Errorf("download Astrodome surface f%03d %s: %w", hour, field.ShortName, err)
		}
	}
	return nil
}

func (client *Client) inspectDomeTask(ctx context.Context, task domeSyncTask, want *DomeStepFile) (DomeStepFile, error) {
	got, err := client.inspectDomePath(ctx, task, task.destination)
	if err != nil {
		return DomeStepFile{}, err
	}
	checkpoint, err := loadDomeCheckpoint(domeCheckpointPath(task.destination))
	if err != nil {
		return DomeStepFile{}, fmt.Errorf("load verified ICON-EU Astrodome checkpoint f%03d: %w", task.forecastHour, err)
	}
	if err := validateDomeCheckpoint(task, checkpoint, got); err != nil {
		return DomeStepFile{}, err
	}
	if want != nil && (got != *want) {
		return DomeStepFile{}, fmt.Errorf("published ICON-EU Astrodome f%03d size/SHA/allocation identity changed", task.forecastHour)
	}
	return got, nil
}

func (client *Client) inspectDomePath(ctx context.Context, task domeSyncTask, path string) (DomeStepFile, error) {
	if err := client.validateDomeInventory(ctx, path, task.expected); err != nil {
		return DomeStepFile{}, fmt.Errorf("validate resumable ICON-EU Astrodome f%03d: %w", task.forecastHour, err)
	}
	digest, size, err := fileDigest(path)
	if err != nil {
		return DomeStepFile{}, err
	}
	allocated, err := domeAllocatedBytes(path)
	if err != nil {
		return DomeStepFile{}, err
	}
	got := DomeStepFile{
		Source: DomeFileSourceDomeRun, ForecastHour: task.forecastHour, ValidAt: task.validAt,
		File: task.manifestFile, Bytes: size, AllocatedBytes: allocated,
		SHA256: digest, Messages: len(task.expected),
	}
	return got, nil
}

func domeCheckpointPath(destination string) string {
	return destination + ".verified.json"
}

func writeDomeCheckpoint(path string, checkpoint domeObjectCheckpoint) error {
	if checkpoint.Schema != domeCheckpointSchema || !validDomeDigest(checkpoint.BaseManifestSHA256) ||
		checkpoint.InputContractSHA256 != DomeInputContractDigest() || !validDomeDigest(checkpoint.File.SHA256) || checkpoint.File.AllocatedBytes <= 0 {
		return errors.New("refuse to write an invalid ICON-EU Astrodome object checkpoint")
	}
	temporary := path + ".part"
	_ = os.Remove(temporary)
	file, err := os.OpenFile(temporary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o640)
	if err != nil {
		return err
	}
	failed := true
	defer func() {
		_ = file.Close()
		if failed {
			_ = os.Remove(temporary)
		}
	}()
	encoder := json.NewEncoder(file)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(checkpoint); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporary, path); err != nil {
		return err
	}
	if err := syncDomeDirectory(filepath.Dir(path)); err != nil {
		return err
	}
	failed = false
	return nil
}

func loadDomeCheckpoint(path string) (domeObjectCheckpoint, error) {
	file, err := os.Open(path)
	if err != nil {
		return domeObjectCheckpoint{}, err
	}
	defer func() { _ = file.Close() }()
	decoder := json.NewDecoder(io.LimitReader(file, 64<<10))
	decoder.DisallowUnknownFields()
	var checkpoint domeObjectCheckpoint
	if err := decoder.Decode(&checkpoint); err != nil {
		return domeObjectCheckpoint{}, err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return domeObjectCheckpoint{}, errors.New("ICON-EU Astrodome checkpoint must contain one JSON value")
	}
	return checkpoint, nil
}

func validateDomeCheckpoint(task domeSyncTask, checkpoint domeObjectCheckpoint, got DomeStepFile) error {
	if checkpoint.Schema != domeCheckpointSchema || checkpoint.BaseManifestSHA256 != task.baseManifestSHA256 ||
		checkpoint.InputContractSHA256 != task.inputContractSHA256 || checkpoint.File != got {
		return fmt.Errorf("verified ICON-EU Astrodome checkpoint f%03d changed or belongs to another base input", task.forecastHour)
	}
	return nil
}

func (client *Client) verifyPublishedDome(ctx context.Context, base domeBaseInput, ready LoadedDomeManifest) error {
	if ready.Geometry != base.geometry {
		return errors.New("published ICON-EU Astrodome geometry differs from the bound base HHL bundle")
	}
	for _, step := range ready.ModelSteps {
		if step.ForecastHour <= 78 && (len(step.Parts) != 2 || step.Parts[0] != base.modelSteps[step.ForecastHour]) {
			return fmt.Errorf("published ICON-EU Astrodome f%03d differs from the bound base cloud bundle", step.ForecastHour)
		}
	}
	tasks, err := buildDomeSyncTasks(base, ready.Directory, ready.InputContractSHA256)
	if err != nil {
		return err
	}
	wants := make([]DomeStepFile, 0, len(tasks))
	for _, step := range ready.ModelSteps {
		wants = append(wants, step.Parts[len(step.Parts)-1])
	}
	wants = append(wants, ready.SurfaceExtensionSteps...)
	for index := range tasks {
		tasks[index].destination = filepath.Join(base.providerRoot, wants[index].File)
		tasks[index].manifestFile = wants[index].File
		if _, err := client.inspectDomeTask(ctx, tasks[index], &wants[index]); err != nil {
			return err
		}
	}
	return requireUnchangedDomeBase(filepath.Join(base.loaded.Directory, "manifest.json"), base.manifestSHA256)
}

func (client *Client) validateDomeInventory(ctx context.Context, path string, expected domeInventory) error {
	countOutput, err := client.Runner.CombinedOutput(ctx, "grib_count", path)
	if err != nil {
		return fmt.Errorf("grib_count %s failed: %s", filepath.Base(path), limitedOutput(countOutput))
	}
	count, err := strconv.Atoi(strings.TrimSpace(string(countOutput)))
	if err != nil || count != len(expected) {
		return fmt.Errorf("%s contains %d messages, expected %d", filepath.Base(path), count, len(expected))
	}
	metadata, err := client.Runner.CombinedOutput(ctx, "grib_get", "-p", "shortName,typeOfLevel,level,stepRange,validityDate,validityTime", path)
	if err != nil {
		return fmt.Errorf("grib_get %s failed: %s", filepath.Base(path), limitedOutput(metadata))
	}
	seen, err := parseDomeInventory(metadata)
	if err != nil {
		return fmt.Errorf("parse %s metadata: %w", filepath.Base(path), err)
	}
	if len(seen) != len(expected) {
		return fmt.Errorf("%s has %d unique canonical messages, expected %d", filepath.Base(path), len(seen), len(expected))
	}
	for message := range expected {
		if _, ok := seen[message]; !ok {
			return fmt.Errorf("%s is missing canonical message %s/%s/%s step %s valid %08d/%04d",
				filepath.Base(path), message.ShortName, message.TypeOfLevel, message.Level,
				message.StepRange, message.ValidityDate, message.ValidityTime)
		}
	}
	return nil
}

func parseDomeInventory(metadata []byte) (domeInventory, error) {
	seen := make(domeInventory)
	scanner := bufio.NewScanner(bytes.NewReader(metadata))
	scanner.Buffer(make([]byte, 4096), 4<<20)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) != 6 {
			return nil, fmt.Errorf("unexpected line %q", scanner.Text())
		}
		date, dateErr := strconv.Atoi(fields[4])
		clock, clockErr := strconv.Atoi(fields[5])
		if dateErr != nil || clockErr != nil {
			return nil, fmt.Errorf("invalid validity in %q", scanner.Text())
		}
		message := domeMessage{
			ShortName: canonicalDomeShortName(fields[0]), TypeOfLevel: fields[1], Level: fields[2],
			StepRange: canonicalDomeStepRange(fields[3]), ValidityDate: date, ValidityTime: clock,
		}
		if _, duplicate := seen[message]; duplicate {
			return nil, fmt.Errorf("duplicate canonical message %q", scanner.Text())
		}
		seen[message] = struct{}{}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return seen, nil
}

// ecCodes renders an analysis/time-invariant zero step as "0m" for DWD
// ICON-EU messages whose encoded step unit is minutes. Forecast terms use
// canonical hour values ("1", "2", ...). Zero has no unit physically, so
// normalize only that exact representation and retain every non-zero or
// accumulated range verbatim for strict inventory validation.
func canonicalDomeStepRange(value string) string {
	value = strings.TrimSpace(value)
	if value == "0m" {
		return "0"
	}
	return value
}

func canonicalDomeShortName(name string) string {
	return canonicalSurfaceShortName(canonicalCloudShortName(strings.TrimSpace(name)))
}

func domeGeometryInventory(baseTime time.Time) domeInventory {
	inventory := make(domeInventory, domeHalfLevelCount)
	for _, level := range DomeHalfModelLevels() {
		inventory[newDomeMessage("HHL", "generalVertical", level, 0, baseTime)] = struct{}{}
	}
	return inventory
}

func domeBaseCloudInventory(baseTime time.Time, hour int) domeInventory {
	validAt := baseTime.Add(time.Duration(hour) * time.Hour)
	inventory := make(domeInventory, cloudStepMessageCount())
	for _, level := range DefaultCloudModelLevels {
		for _, field := range cloudBaseLevelFields {
			inventory[newDomeMessage(field.shortName, "generalVerticalLayer", level, hour, validAt)] = struct{}{}
		}
	}
	for _, level := range DefaultCloudGroundModelLevels {
		for _, field := range cloudGroundFullLevelFields {
			inventory[newDomeMessage(field.shortName, "generalVerticalLayer", level, hour, validAt)] = struct{}{}
		}
	}
	for _, level := range cloudTKEHalfLevels() {
		inventory[newDomeMessage("tke", "generalVertical", level, hour, validAt)] = struct{}{}
	}
	return inventory
}

func domeTargetModelInventory(baseTime time.Time, hour int) domeInventory {
	validAt := baseTime.Add(time.Duration(hour) * time.Hour)
	inventory := make(domeInventory, domeModelMessagesPerStep)
	for _, level := range DomeFullModelLevels() {
		for _, field := range DomeModelFields() {
			if field.Stagger == DomeStaggerFull {
				inventory[newDomeMessage(field.ShortName, "generalVerticalLayer", level, hour, validAt)] = struct{}{}
			}
		}
	}
	for _, level := range DomeHalfModelLevels() {
		for _, field := range DomeModelFields() {
			if field.Stagger == DomeStaggerHalf {
				inventory[newDomeMessage(field.ShortName, "generalVertical", level, hour, validAt)] = struct{}{}
			}
		}
	}
	return inventory
}

func newDomeMessage(shortName, levelType string, level, hour int, validAt time.Time) domeMessage {
	date, _ := strconv.Atoi(validAt.UTC().Format("20060102"))
	clock, _ := strconv.Atoi(validAt.UTC().Format("1504"))
	return domeMessage{
		ShortName: canonicalDomeShortName(shortName), TypeOfLevel: levelType,
		Level: strconv.Itoa(level), StepRange: strconv.Itoa(hour),
		ValidityDate: date, ValidityTime: clock,
	}
}

func (client *Client) readCanonicalSurfaceIdentity(ctx context.Context, path string, baseTime time.Time, hour int) (map[string]domeMessage, error) {
	countOutput, err := client.Runner.CombinedOutput(ctx, "grib_count", path)
	if err != nil {
		return nil, fmt.Errorf("grib_count %s failed: %s", filepath.Base(path), limitedOutput(countOutput))
	}
	count, err := strconv.Atoi(strings.TrimSpace(string(countOutput)))
	if err != nil || count != SurfaceBundleSchemaVersion {
		return nil, fmt.Errorf("%s contains %d surface messages, expected %d", filepath.Base(path), count, SurfaceBundleSchemaVersion)
	}
	metadata, err := client.Runner.CombinedOutput(ctx, "grib_get", "-p", "shortName,typeOfLevel,level,stepRange,validityDate,validityTime", path)
	if err != nil {
		return nil, fmt.Errorf("grib_get %s failed: %s", filepath.Base(path), limitedOutput(metadata))
	}
	inventory, err := parseDomeInventory(metadata)
	if err != nil {
		return nil, err
	}
	wantNames := make(map[string]struct{}, len(surfaceFields))
	for _, field := range surfaceFields {
		wantNames[field.ShortName] = struct{}{}
	}
	identity := make(map[string]domeMessage, len(surfaceFields))
	validAt := baseTime.Add(time.Duration(hour) * time.Hour)
	date, _ := strconv.Atoi(validAt.Format("20060102"))
	clock, _ := strconv.Atoi(validAt.Format("1504"))
	for message := range inventory {
		if _, ok := wantNames[message.ShortName]; !ok || message.ValidityDate != date || message.ValidityTime != clock || message.StepRange != domeSurfaceStepRange(message.ShortName, hour) {
			return nil, fmt.Errorf("unexpected canonical surface message %+v", message)
		}
		identity[message.ShortName] = domeMessage{ShortName: message.ShortName, TypeOfLevel: message.TypeOfLevel, Level: message.Level}
	}
	if len(identity) != len(surfaceFields) {
		return nil, errors.New("surface bundle does not contain the exact versioned 17-field schema")
	}
	return identity, nil
}

func domeSurfaceInventory(baseTime time.Time, hour int, identities map[string]domeMessage) domeInventory {
	validAt := baseTime.Add(time.Duration(hour) * time.Hour)
	date, _ := strconv.Atoi(validAt.Format("20060102"))
	clock, _ := strconv.Atoi(validAt.Format("1504"))
	inventory := make(domeInventory, len(surfaceFields))
	for _, field := range surfaceFields {
		identity := identities[field.ShortName]
		identity.ShortName = field.ShortName
		identity.StepRange = domeSurfaceStepRange(field.ShortName, hour)
		identity.ValidityDate = date
		identity.ValidityTime = clock
		inventory[identity] = struct{}{}
	}
	return inventory
}

func domeSurfaceStepRange(shortName string, hour int) string {
	if shortName == "tp" && hour > 0 {
		return fmt.Sprintf("0-%d", hour)
	}
	// DWD encodes VMAX_10M as the maximum over the immediately preceding
	// hour.  This remains an hourly window at the sparse f081/f084 terms:
	// f081 is 80-81, not 78-81.
	if shortName == "VMAX_10M" && hour > 0 {
		return fmt.Sprintf("%d-%d", hour-1, hour)
	}
	return strconv.Itoa(hour)
}

func timeFromRun(runID string) time.Time {
	value, _ := time.Parse("2006010215", runID)
	return value
}
