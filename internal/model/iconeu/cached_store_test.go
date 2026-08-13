package iconeu

import (
	"compress/gzip"
	"context"
	"encoding/gob"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"bot_astrosferum/internal/forecast"
	"bot_astrosferum/internal/model"
)

type legacyVerticalFrame struct {
	ValidAt    time.Time
	Levels     []forecast.VerticalLevel
	Confidence float64
}

type legacyVerticalSeries struct {
	Location         forecast.Location
	Provider         string
	Product          string
	RunID            string
	Grid             string
	AlgorithmVersion string
	BaseTime         time.Time
	GeneratedAt      time.Time
	Frames           []legacyVerticalFrame
}

type legacySurfaceFrame struct {
	ValidAt                 time.Time
	TemperatureC            float64
	DewPointC               float64
	RelativeHumidityPercent float64
	VisibilityKM            float64
	PrecipitableWaterMM     float64
	TransparencyAvailable   bool
}

type legacySurfaceSeries struct {
	Location    forecast.Location
	Provider    string
	Product     string
	RunID       string
	BaseTime    time.Time
	GeneratedAt time.Time
	StepHours   int
	Frames      []legacySurfaceFrame
}

type legacyPointBundle struct {
	Schema   string
	RunID    string
	CellID   string
	Created  time.Time
	Vertical legacyVerticalSeries
	Surface  legacySurfaceSeries
	Cloud    forecast.CloudSeries
}

type countingPointSource struct {
	mu                       sync.Mutex
	vertical, surface, cloud int
	delay                    time.Duration
	runID                    string
}

func (source *countingPointSource) Vertical(ctx context.Context, location forecast.Location) (forecast.VerticalSeries, error) {
	if err := waitForTest(ctx, source.delay); err != nil {
		return forecast.VerticalSeries{}, err
	}
	source.mu.Lock()
	source.vertical++
	runID := source.currentRunLocked()
	source.mu.Unlock()
	value := forecast.SyntheticVerticalFixture()
	value.Location = location
	value.RunID = runID
	value.AlgorithmVersion = "cached-legacy-seeing-version"
	return value, nil
}

func (source *countingPointSource) Surface(ctx context.Context, location forecast.Location) (forecast.SurfaceSeries, error) {
	if err := waitForTest(ctx, source.delay); err != nil {
		return forecast.SurfaceSeries{}, err
	}
	source.mu.Lock()
	source.surface++
	runID := source.currentRunLocked()
	source.mu.Unlock()
	value := forecast.SyntheticSurfaceFixture()
	value.Location = location
	value.RunID = runID
	return value, nil
}

func (source *countingPointSource) Cloud(ctx context.Context, location forecast.Location) (forecast.CloudSeries, error) {
	if err := waitForTest(ctx, source.delay); err != nil {
		return forecast.CloudSeries{}, err
	}
	source.mu.Lock()
	source.cloud++
	runID := source.currentRunLocked()
	source.mu.Unlock()
	value := forecast.SyntheticCloudFixture()
	value.Location = location
	value.RunID = runID
	return value, nil
}

func (source *countingPointSource) currentRunLocked() string {
	if source.runID == "" {
		return "20260714T0600Z"
	}
	return source.runID
}

func (source *countingPointSource) setRun(runID string) {
	source.mu.Lock()
	source.runID = runID
	source.mu.Unlock()
}

func waitForTest(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func newTestCachedStore(root string, source pointSource) *CachedStore {
	return &CachedStore{
		dataRoot: root,
		source:   source,
		loadCurrent: func(string) (LoadedManifest, error) {
			return LoadedManifest{Manifest: Manifest{
				RunID: "20260714T0600Z",
				Grid:  model.Coverage{MinLat: 20, MaxLat: 80, MinLon: -20, MaxLon: 60, Increment: 0.0625},
			}}, nil
		},
		memoryLimit: 32, memoryLimitBytes: 1 << 30,
		logf: func(string, ...any) {}, memory: make(map[string]memoryPoint), flights: make(map[string]*pointFlight),
	}
}

func TestCachedStoreCachesBundleInMemoryAndOnDisk(t *testing.T) {
	root := t.TempDir()
	location, _ := forecast.NewLocation(55.7558, 37.6173, "Europe/Moscow")
	source := &countingPointSource{}
	store := newTestCachedStore(root, source)
	vertical, err := store.Vertical(context.Background(), location)
	if err != nil {
		t.Fatal(err)
	}
	if vertical.AlgorithmVersion != forecast.SeeingPrototypeVersion {
		t.Fatalf("active algorithm version = %q, want %q", vertical.AlgorithmVersion, forecast.SeeingPrototypeVersion)
	}
	if _, err := store.Surface(context.Background(), location); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Cloud(context.Background(), location); err != nil {
		t.Fatal(err)
	}
	if source.vertical != 1 || source.surface != 1 || source.cloud != 1 {
		t.Fatalf("unexpected extraction counts: %d/%d/%d", source.vertical, source.surface, source.cloud)
	}
	cell := regularGridCellID(storeManifest(t, store), location)
	if _, err := os.Stat(store.cachePath("20260714T0600Z", cell)); err != nil {
		t.Fatalf("point disk cache: %v", err)
	}

	coldSource := &countingPointSource{}
	coldStore := newTestCachedStore(root, coldSource)
	vertical, err = coldStore.Vertical(context.Background(), location)
	if err != nil {
		t.Fatal(err)
	}
	if vertical.AlgorithmVersion != forecast.SeeingPrototypeVersion {
		t.Fatalf("disk-cache algorithm version = %q, want %q", vertical.AlgorithmVersion, forecast.SeeingPrototypeVersion)
	}
	if vertical.Frames[0].LeadTimeQualityHeuristic <= 0 {
		t.Fatal("disk cache lost lead-time quality heuristic")
	}
	coldSurface, err := coldStore.Surface(context.Background(), location)
	if err != nil {
		t.Fatal(err)
	}
	if !coldSurface.Frames[0].FogHeuristicAvailable || !coldSurface.Frames[0].TransparencyHeuristicAvailable {
		t.Fatal("disk cache lost explicit fog/transparency heuristic availability")
	}
	if coldSource.vertical != 0 || coldSource.surface != 0 || coldSource.cloud != 0 {
		t.Fatal("disk cache unexpectedly extracted source data")
	}
}

func TestCachedStoreRejectsLegacyHeuristicFieldBundle(t *testing.T) {
	root := t.TempDir()
	const runID = "20260714T0600Z"
	const cellID = "lat0633-lon0923"
	vertical := forecast.SyntheticVerticalFixture()
	surface := forecast.SyntheticSurfaceFixture()
	legacyVerticalFrames := make([]legacyVerticalFrame, len(vertical.Frames))
	for index, frame := range vertical.Frames {
		legacyVerticalFrames[index] = legacyVerticalFrame{
			ValidAt: frame.ValidAt, Levels: frame.Levels, Confidence: 0.93,
		}
	}
	legacySurfaceFrames := make([]legacySurfaceFrame, len(surface.Frames))
	for index, frame := range surface.Frames {
		legacySurfaceFrames[index] = legacySurfaceFrame{
			ValidAt: frame.ValidAt, TemperatureC: frame.TemperatureC,
			DewPointC: frame.DewPointC, RelativeHumidityPercent: frame.RelativeHumidityPercent,
			VisibilityKM: frame.VisibilityKM, PrecipitableWaterMM: frame.PrecipitableWaterMM,
			TransparencyAvailable: true,
		}
	}
	legacy := legacyPointBundle{
		Schema: "point-v6-native-cloud-mass-mh", RunID: runID, CellID: cellID,
		Created: time.Now().UTC(),
		Vertical: legacyVerticalSeries{
			Location: vertical.Location, Provider: vertical.Provider, Product: vertical.Product,
			RunID: runID, Grid: vertical.Grid, AlgorithmVersion: vertical.AlgorithmVersion,
			BaseTime: vertical.BaseTime, GeneratedAt: vertical.GeneratedAt, Frames: legacyVerticalFrames,
		},
		Surface: legacySurfaceSeries{
			Location: surface.Location, Provider: surface.Provider, Product: surface.Product,
			RunID: runID, BaseTime: surface.BaseTime, GeneratedAt: surface.GeneratedAt,
			StepHours: surface.StepHours, Frames: legacySurfaceFrames,
		},
		Cloud: forecast.SyntheticCloudFixture(),
	}
	store := newTestCachedStore(root, &countingPointSource{})
	path := store.cachePath(runID, cellID)
	writeLegacyPointBundle(t, path, legacy)
	if _, err := store.readDisk(runID, cellID); err == nil {
		t.Fatal("legacy point-cache schema was accepted")
	}
	legacyPath := filepath.Join(root, "cache", "points", legacy.Schema, runID, cellID+".gob.gz")
	if legacyPath == path {
		t.Fatal("legacy and current point-cache versions share a directory")
	}
}

func writeLegacyPointBundle(t *testing.T, path string, bundle legacyPointBundle) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	compressed := gzip.NewWriter(file)
	if err := gob.NewEncoder(compressed).Encode(bundle); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := compressed.Close(); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestCachedStoreCoalescesConcurrentMisses(t *testing.T) {
	location, _ := forecast.NewLocation(59.939, 30.315, "Europe/Moscow")
	source := &countingPointSource{delay: 30 * time.Millisecond}
	store := newTestCachedStore(t.TempDir(), source)
	var group sync.WaitGroup
	errors := make(chan error, 12)
	for range 12 {
		group.Add(1)
		go func() {
			defer group.Done()
			_, err := store.Vertical(context.Background(), location)
			errors <- err
		}()
	}
	group.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	if source.vertical != 1 || source.surface != 1 || source.cloud != 1 {
		t.Fatalf("miss was not coalesced: %d/%d/%d", source.vertical, source.surface, source.cloud)
	}
}

func TestCachedStoreDoesNotReusePreviousRunAfterRollover(t *testing.T) {
	const firstRun = "20260714T0600Z"
	const secondRun = "20260714T1200Z"
	location, _ := forecast.NewLocation(59.939, 30.315, "Europe/Moscow")
	source := &countingPointSource{runID: firstRun}
	store := newTestCachedStore(t.TempDir(), source)
	store.loadCurrent = func(string) (LoadedManifest, error) {
		source.mu.Lock()
		runID := source.currentRunLocked()
		source.mu.Unlock()
		return LoadedManifest{Manifest: Manifest{
			RunID: runID,
			Grid:  model.Coverage{MinLat: 20, MaxLat: 80, MinLon: -20, MaxLon: 60, Increment: 0.0625},
		}}, nil
	}
	vertical, err := store.Vertical(context.Background(), location)
	if err != nil {
		t.Fatal(err)
	}
	if vertical.RunID != firstRun {
		t.Fatalf("first vertical run = %q", vertical.RunID)
	}
	source.setRun(secondRun)
	surface, err := store.Surface(context.Background(), location)
	if err != nil {
		t.Fatal(err)
	}
	cloud, err := store.Cloud(context.Background(), location)
	if err != nil {
		t.Fatal(err)
	}
	if surface.RunID != secondRun || cloud.RunID != secondRun {
		t.Fatalf("post-rollover runs = surface %q cloud %q, want %q", surface.RunID, cloud.RunID, secondRun)
	}
	source.mu.Lock()
	verticalCalls, surfaceCalls, cloudCalls := source.vertical, source.surface, source.cloud
	source.mu.Unlock()
	if verticalCalls != 2 || surfaceCalls != 2 || cloudCalls != 2 {
		t.Fatalf("run rollover extraction counts = %d/%d/%d, want 2/2/2", verticalCalls, surfaceCalls, cloudCalls)
	}
}

func TestPrunePointCacheBoundsRunsEntriesAndTemporaryFiles(t *testing.T) {
	root := t.TempDir()
	old := time.Now().Add(-2 * time.Hour)
	for _, run := range []string{"2026071400", "2026071406", "2026071412"} {
		directory := filepath.Join(root, run)
		if err := os.MkdirAll(directory, 0o750); err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"a.gob.gz", "b.gob.gz", "c.gob.gz", ".point-dead.tmp"} {
			path := filepath.Join(directory, name)
			if err := os.WriteFile(path, []byte("x"), 0o640); err != nil {
				t.Fatal(err)
			}
			if err := os.Chtimes(path, old, old); err != nil {
				t.Fatal(err)
			}
		}
	}
	prunePointCache(root, 2, 2, time.Hour)
	if _, err := os.Stat(filepath.Join(root, "2026071400")); !os.IsNotExist(err) {
		t.Fatalf("old run was not removed: %v", err)
	}
	for _, run := range []string{"2026071406", "2026071412"} {
		entries, err := os.ReadDir(filepath.Join(root, run))
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 2 {
			t.Fatalf("run %s retained %d entries, want 2", run, len(entries))
		}
	}
}

func storeManifest(t *testing.T, store *CachedStore) LoadedManifest {
	t.Helper()
	manifest, err := store.loadCurrent(store.dataRoot)
	if err != nil {
		t.Fatal(err)
	}
	return manifest
}
