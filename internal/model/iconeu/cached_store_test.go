package iconeu

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"bot_astrosferum/internal/forecast"
	"bot_astrosferum/internal/model"
)

type countingPointSource struct {
	mu                       sync.Mutex
	vertical, surface, cloud int
	delay                    time.Duration
}

func (source *countingPointSource) Vertical(ctx context.Context, location forecast.Location) (forecast.VerticalSeries, error) {
	if err := waitForTest(ctx, source.delay); err != nil {
		return forecast.VerticalSeries{}, err
	}
	source.mu.Lock()
	source.vertical++
	source.mu.Unlock()
	value := forecast.SyntheticVerticalFixture()
	value.Location = location
	return value, nil
}

func (source *countingPointSource) Surface(ctx context.Context, location forecast.Location) (forecast.SurfaceSeries, error) {
	if err := waitForTest(ctx, source.delay); err != nil {
		return forecast.SurfaceSeries{}, err
	}
	source.mu.Lock()
	source.surface++
	source.mu.Unlock()
	value := forecast.SyntheticSurfaceFixture()
	value.Location = location
	return value, nil
}

func (source *countingPointSource) Cloud(ctx context.Context, location forecast.Location) (forecast.CloudSeries, error) {
	if err := waitForTest(ctx, source.delay); err != nil {
		return forecast.CloudSeries{}, err
	}
	source.mu.Lock()
	source.cloud++
	source.mu.Unlock()
	value := forecast.SyntheticCloudFixture()
	value.Location = location
	return value, nil
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
	if _, err := store.Vertical(context.Background(), location); err != nil {
		t.Fatal(err)
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
	if _, err := coldStore.Vertical(context.Background(), location); err != nil {
		t.Fatal(err)
	}
	if coldSource.vertical != 0 || coldSource.surface != 0 || coldSource.cloud != 0 {
		t.Fatal("disk cache unexpectedly extracted source data")
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
