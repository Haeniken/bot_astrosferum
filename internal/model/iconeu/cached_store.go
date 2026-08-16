package iconeu

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/gob"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"bot_astrosferum/internal/forecast"
)

const (
	pointCacheVersion       = "point-v8-native-mh-support"
	pointCacheRuns          = 2
	pointCacheEntriesPerRun = 512
	pointCacheTempMaxAge    = time.Hour
)

type pointSource interface {
	Vertical(context.Context, forecast.Location) (forecast.VerticalSeries, error)
	Surface(context.Context, forecast.Location) (forecast.SurfaceSeries, error)
	Cloud(context.Context, forecast.Location) (forecast.CloudSeries, error)
}

type pointBundle struct {
	Schema   string                  `json:"schema"`
	RunID    string                  `json:"run_id"`
	CellID   string                  `json:"cell_id"`
	Created  time.Time               `json:"created_at"`
	Vertical forecast.VerticalSeries `json:"vertical"`
	Surface  forecast.SurfaceSeries  `json:"surface"`
	Cloud    forecast.CloudSeries    `json:"cloud"`
}

type memoryPoint struct {
	bundle pointBundle
	used   uint64
	bytes  int64
}

type pointFlight struct {
	done   chan struct{}
	bundle pointBundle
	err    error
}

// CachedStore converts immutable GRIB publications into a compact point
// bundle once per model grid cell and run. It provides an in-memory LRU,
// atomic gzip disk cache, and per-key request coalescing.
type CachedStore struct {
	dataRoot         string
	source           pointSource
	loadCurrent      func(string) (LoadedManifest, error)
	memoryLimit      int
	memoryBytes      int64
	memoryLimitBytes int64
	logf             func(string, ...any)

	mu      sync.Mutex
	clock   uint64
	memory  map[string]memoryPoint
	flights map[string]*pointFlight
}

func NewCachedStore(dataRoot string, ecCodesWorkers, memoryEntries int, memoryLimitBytes int64, logf func(string, ...any)) *CachedStore {
	if ecCodesWorkers < 1 {
		ecCodesWorkers = 1
	}
	if memoryEntries < 1 {
		memoryEntries = 32
	}
	if memoryLimitBytes < 1 {
		memoryLimitBytes = 1 << 30
	}
	if logf == nil {
		logf = func(string, ...any) {}
	}
	runner := &limitedRunner{runner: execRunner{}, semaphore: make(chan struct{}, ecCodesWorkers)}
	store := &CachedStore{
		dataRoot:         dataRoot,
		source:           VerticalStore{DataRoot: dataRoot, Runner: runner, Workers: ecCodesWorkers},
		loadCurrent:      LoadCurrent,
		memoryLimit:      memoryEntries,
		memoryLimitBytes: memoryLimitBytes,
		logf:             logf,
		memory:           make(map[string]memoryPoint),
		flights:          make(map[string]*pointFlight),
	}
	cacheRoot := filepath.Join(dataRoot, "cache", "points", pointCacheVersion)
	go pruneObsoletePointCaches(filepath.Dir(cacheRoot), pointCacheVersion)
	go prunePointCache(cacheRoot, pointCacheRuns, pointCacheEntriesPerRun, pointCacheTempMaxAge)
	return store
}

func pruneObsoletePointCaches(root, currentVersion string) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.IsDir() && strings.HasPrefix(entry.Name(), "point-v") && entry.Name() != currentVersion {
			_ = os.RemoveAll(filepath.Join(root, entry.Name()))
		}
	}
}

type limitedRunner struct {
	runner    CommandRunner
	semaphore chan struct{}
}

func (runner *limitedRunner) CombinedOutput(ctx context.Context, name string, args ...string) ([]byte, error) {
	select {
	case runner.semaphore <- struct{}{}:
		defer func() { <-runner.semaphore }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return runner.runner.CombinedOutput(ctx, name, args...)
}

func (store *CachedStore) Vertical(ctx context.Context, location forecast.Location) (forecast.VerticalSeries, error) {
	bundle, err := store.load(ctx, location)
	if err != nil {
		return forecast.VerticalSeries{}, err
	}
	series := bundle.Vertical
	series.Location = location
	// Point bundles contain extracted model values, not derived seeing output.
	// Stamp the active calculation version on read so a scientific-method
	// update cannot keep an obsolete marker from an otherwise valid raw cache.
	series.AlgorithmVersion = forecast.SeeingPrototypeVersion
	return series, nil
}

func (store *CachedStore) Surface(ctx context.Context, location forecast.Location) (forecast.SurfaceSeries, error) {
	bundle, err := store.load(ctx, location)
	if err != nil {
		return forecast.SurfaceSeries{}, err
	}
	series := bundle.Surface
	series.Location = location
	return series, nil
}

func (store *CachedStore) Cloud(ctx context.Context, location forecast.Location) (forecast.CloudSeries, error) {
	bundle, err := store.load(ctx, location)
	if err != nil {
		return forecast.CloudSeries{}, err
	}
	series := bundle.Cloud
	series.Location = location
	return series, nil
}

func (store *CachedStore) load(ctx context.Context, location forecast.Location) (pointBundle, error) {
	manifest, err := store.loadCurrent(store.dataRoot)
	if err != nil {
		return pointBundle{}, err
	}
	cellID := regularGridCellID(manifest, location)
	key := manifest.RunID + "/" + cellID
	if bundle, ok := store.memoryGet(key); ok {
		if !manifest.HasWindThermodynamics() || bundleHasTemperature(bundle) {
			store.logf("point cache memory hit run=%s", manifest.RunID)
			return bundle, nil
		}
		store.memoryDelete(key)
	}

	store.mu.Lock()
	if flight, ok := store.flights[key]; ok {
		store.mu.Unlock()
		select {
		case <-flight.done:
			if flight.err == nil {
				store.logf("point cache shared hit run=%s", manifest.RunID)
			}
			return flight.bundle, flight.err
		case <-ctx.Done():
			return pointBundle{}, ctx.Err()
		}
	}
	flight := &pointFlight{done: make(chan struct{})}
	store.flights[key] = flight
	store.mu.Unlock()

	started := time.Now()
	bundle, cacheError := store.readDisk(manifest.RunID, cellID)
	if cacheError == nil && manifest.HasWindThermodynamics() && !bundleHasTemperature(bundle) {
		cacheError = fmt.Errorf("point cache predates pressure-level temperature")
		_ = os.Remove(store.cachePath(manifest.RunID, cellID))
	}
	if cacheError == nil {
		store.logf("point cache disk hit run=%s duration=%s", manifest.RunID, time.Since(started).Round(time.Millisecond))
	} else if !errors.Is(cacheError, os.ErrNotExist) {
		store.logf("point cache invalid run=%s: %v", manifest.RunID, cacheError)
	}
	if cacheError != nil {
		bundle, err = store.extract(ctx, manifest, cellID, location)
		if err == nil {
			if writeError := store.writeDisk(bundle); writeError != nil {
				store.logf("point cache write failed run=%s: %v", manifest.RunID, writeError)
			}
			store.logf("point cache miss filled run=%s duration=%s", manifest.RunID, time.Since(started).Round(time.Millisecond))
		}
	}
	if err == nil {
		store.memoryPut(key, bundle)
	}

	store.mu.Lock()
	flight.bundle, flight.err = bundle, err
	delete(store.flights, key)
	close(flight.done)
	store.mu.Unlock()
	return bundle, err
}

func bundleHasTemperature(bundle pointBundle) bool {
	if len(bundle.Vertical.Frames) < 2 {
		return false
	}
	for _, frame := range bundle.Vertical.Frames {
		for _, level := range frame.Levels {
			if !validCachedTemperature(level.TemperatureK) {
				return false
			}
		}
	}
	return true
}

func validCachedTemperature(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 150 && value <= 350
}

func (store *CachedStore) extract(ctx context.Context, manifest LoadedManifest, cellID string, location forecast.Location) (pointBundle, error) {
	type result struct {
		kind     string
		vertical forecast.VerticalSeries
		surface  forecast.SurfaceSeries
		cloud    forecast.CloudSeries
		err      error
		duration time.Duration
	}
	workContext, cancel := context.WithCancel(ctx)
	defer cancel()
	results := make(chan result, 3)
	go func() {
		start := time.Now()
		value, err := store.source.Vertical(workContext, location)
		results <- result{kind: "wind", vertical: value, err: err, duration: time.Since(start)}
	}()
	go func() {
		start := time.Now()
		value, err := store.source.Surface(workContext, location)
		results <- result{kind: "surface", surface: value, err: err, duration: time.Since(start)}
	}()
	go func() {
		start := time.Now()
		value, err := store.source.Cloud(workContext, location)
		results <- result{kind: "cloud", cloud: value, err: err, duration: time.Since(start)}
	}()
	bundle := pointBundle{Schema: pointCacheVersion, RunID: manifest.RunID, CellID: cellID, Created: time.Now().UTC()}
	for range 3 {
		item := <-results
		store.logf("point extraction %s duration=%s", item.kind, item.duration.Round(time.Millisecond))
		if item.err != nil {
			cancel()
			return pointBundle{}, item.err
		}
		switch item.kind {
		case "wind":
			bundle.Vertical = item.vertical
		case "surface":
			bundle.Surface = item.surface
		case "cloud":
			bundle.Cloud = item.cloud
		}
	}
	if bundle.Vertical.RunID != manifest.RunID || bundle.Surface.RunID != manifest.RunID || bundle.Cloud.RunID != manifest.RunID {
		return pointBundle{}, fmt.Errorf("ICON-EU run changed during point extraction")
	}
	return bundle, nil
}

func regularGridCellID(manifest LoadedManifest, location forecast.Location) string {
	increment := manifest.Grid.Increment
	if increment <= 0 {
		increment = 0.0625
	}
	lat := int(math.Round((location.Latitude - manifest.Grid.MinLat) / increment))
	lon := int(math.Round((location.Longitude - manifest.Grid.MinLon) / increment))
	maxLat := int(math.Round((manifest.Grid.MaxLat - manifest.Grid.MinLat) / increment))
	maxLon := int(math.Round((manifest.Grid.MaxLon - manifest.Grid.MinLon) / increment))
	lat = max(0, min(maxLat, lat))
	lon = max(0, min(maxLon, lon))
	return fmt.Sprintf("lat%04d-lon%04d", lat, lon)
}

func (store *CachedStore) cachePath(runID, cellID string) string {
	return filepath.Join(store.dataRoot, "cache", "points", pointCacheVersion, runID, cellID+".gob.gz")
}

func (store *CachedStore) readDisk(runID, cellID string) (pointBundle, error) {
	path := store.cachePath(runID, cellID)
	file, err := os.Open(path)
	if err != nil {
		return pointBundle{}, err
	}
	defer func() { _ = file.Close() }()
	compressed, err := gzip.NewReader(file)
	if err != nil {
		return pointBundle{}, fmt.Errorf("open gzip point cache: %w", err)
	}
	defer func() { _ = compressed.Close() }()
	var bundle pointBundle
	decoder := gob.NewDecoder(compressed)
	if err := decoder.Decode(&bundle); err != nil {
		return pointBundle{}, fmt.Errorf("decode point cache: %w", err)
	}
	if bundle.Schema != pointCacheVersion || bundle.RunID != runID || bundle.CellID != cellID || len(bundle.Vertical.Frames) < 2 || len(bundle.Surface.Frames) < 2 || len(bundle.Cloud.Frames) < 2 {
		return pointBundle{}, fmt.Errorf("point cache identity or content mismatch")
	}
	now := time.Now()
	_ = os.Chtimes(path, now, now)
	return bundle, nil
}

func (store *CachedStore) writeDisk(bundle pointBundle) error {
	path := store.cachePath(bundle.RunID, bundle.CellID)
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o750); err != nil {
		return err
	}
	file, err := os.CreateTemp(directory, ".point-*.tmp")
	if err != nil {
		return err
	}
	temporary := file.Name()
	failed := true
	defer func() {
		_ = file.Close()
		if failed {
			_ = os.Remove(temporary)
		}
	}()
	if err := file.Chmod(0o640); err != nil {
		return err
	}
	compressed := gzip.NewWriter(file)
	if err := gob.NewEncoder(compressed).Encode(bundle); err != nil {
		return err
	}
	if err := compressed.Close(); err != nil {
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
	failed = false
	go prunePointCache(filepath.Join(store.dataRoot, "cache", "points", pointCacheVersion), pointCacheRuns, pointCacheEntriesPerRun, pointCacheTempMaxAge)
	return nil
}

func (store *CachedStore) memoryGet(key string) (pointBundle, bool) {
	store.mu.Lock()
	defer store.mu.Unlock()
	item, ok := store.memory[key]
	if !ok {
		return pointBundle{}, false
	}
	store.clock++
	item.used = store.clock
	store.memory[key] = item
	return item.bundle, true
}

func (store *CachedStore) memoryPut(key string, bundle pointBundle) {
	var encoded bytes.Buffer
	if err := gob.NewEncoder(&encoded).Encode(bundle); err != nil {
		return
	}
	// Go structs retain more memory than their JSON representation. Doubling the
	// serialized size is a deliberately conservative accounting approximation.
	estimatedBytes := int64(encoded.Len()) * 2
	if estimatedBytes > store.memoryLimitBytes {
		return
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	store.clock++
	if existing, ok := store.memory[key]; ok {
		store.memoryBytes -= existing.bytes
	}
	store.memory[key] = memoryPoint{bundle: bundle, used: store.clock, bytes: estimatedBytes}
	store.memoryBytes += estimatedBytes
	for len(store.memory) > store.memoryLimit || store.memoryBytes > store.memoryLimitBytes {
		oldestKey, oldest := "", ^uint64(0)
		for candidate, item := range store.memory {
			if item.used < oldest {
				oldestKey, oldest = candidate, item.used
			}
		}
		store.memoryBytes -= store.memory[oldestKey].bytes
		delete(store.memory, oldestKey)
	}
}

func (store *CachedStore) memoryDelete(key string) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if item, ok := store.memory[key]; ok {
		store.memoryBytes -= item.bytes
		delete(store.memory, key)
	}
}

func prunePointCache(root string, keepRuns, maximumEntriesPerRun int, temporaryMaxAge time.Duration) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	var names []string
	now := time.Now()
	for _, entry := range entries {
		if entry.IsDir() {
			names = append(names, entry.Name())
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(names)))
	if keepRuns < 0 {
		keepRuns = 0
	}
	if keepRuns < len(names) {
		for _, name := range names[keepRuns:] {
			_ = os.RemoveAll(filepath.Join(root, name))
		}
		names = names[:keepRuns]
	}
	for _, name := range names {
		prunePointRun(filepath.Join(root, name), maximumEntriesPerRun, temporaryMaxAge, now)
	}
}

func prunePointRun(root string, maximumEntries int, temporaryMaxAge time.Duration, now time.Time) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	type candidate struct {
		path     string
		modified time.Time
	}
	var candidates []candidate
	for _, entry := range entries {
		path := filepath.Join(root, entry.Name())
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		if strings.HasPrefix(entry.Name(), ".point-") && strings.HasSuffix(entry.Name(), ".tmp") {
			if now.Sub(info.ModTime()) > temporaryMaxAge {
				_ = os.Remove(path)
			}
			continue
		}
		if strings.HasSuffix(entry.Name(), ".gob.gz") {
			candidates = append(candidates, candidate{path: path, modified: info.ModTime()})
		}
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].modified.After(candidates[j].modified) })
	if maximumEntries < 0 {
		maximumEntries = 0
	}
	if maximumEntries < len(candidates) {
		for _, item := range candidates[maximumEntries:] {
			_ = os.Remove(item.path)
		}
	}
}
