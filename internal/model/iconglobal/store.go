package iconglobal

import (
	"compress/gzip"
	"context"
	"encoding/gob"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"bot_astrosferum/internal/forecast"
	"bot_astrosferum/internal/model/iconeu"
)

const pointCacheVersion = "point-v2-native-cloud"

type commandRunner struct {
	semaphore chan struct{}
}

func (runner commandRunner) CombinedOutput(ctx context.Context, name string, args ...string) ([]byte, error) {
	select {
	case runner.semaphore <- struct{}{}:
		defer func() { <-runner.semaphore }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
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

type pointFlight struct {
	done   chan struct{}
	bundle pointBundle
	err    error
}

type Store struct {
	dataRoot string
	runner   commandRunner
	workers  int
	logf     func(string, ...any)

	mu      sync.Mutex
	flights map[string]*pointFlight
}

func NewStore(dataRoot string, workers int, logf func(string, ...any)) *Store {
	workers = max(1, workers)
	if logf == nil {
		logf = func(string, ...any) {}
	}
	store := &Store{
		dataRoot: dataRoot, runner: commandRunner{semaphore: make(chan struct{}, workers)},
		workers: workers, logf: logf, flights: make(map[string]*pointFlight),
	}
	go store.prunePointCache(2, 512)
	return store
}

func (store *Store) Vertical(ctx context.Context, location forecast.Location) (forecast.VerticalSeries, error) {
	bundle, err := store.load(ctx, location)
	if err != nil {
		return forecast.VerticalSeries{}, err
	}
	series := bundle.Vertical
	series.Location = location
	// As with ICON-EU, cached point values are raw model inputs. The derived
	// algorithm marker must always describe the code serving this response.
	series.AlgorithmVersion = forecast.SeeingPrototypeVersion
	return series, nil
}

func (store *Store) Surface(ctx context.Context, location forecast.Location) (forecast.SurfaceSeries, error) {
	bundle, err := store.load(ctx, location)
	if err != nil {
		return forecast.SurfaceSeries{}, err
	}
	bundle.Surface.Location = location
	return bundle.Surface, nil
}

func (store *Store) Cloud(ctx context.Context, location forecast.Location) (forecast.CloudSeries, error) {
	manifest, err := LoadCurrent(store.dataRoot)
	if err != nil {
		return forecast.CloudSeries{}, err
	}
	if !manifest.HasHourlyCloud() {
		return forecast.CloudSeries{}, errors.New("current ICON Global run has no hourly model-layer cloud bundle")
	}
	bundle, err := store.load(ctx, location)
	if err != nil {
		return forecast.CloudSeries{}, err
	}
	bundle.Cloud.Location = location
	return bundle.Cloud, nil
}

func (store *Store) load(ctx context.Context, location forecast.Location) (pointBundle, error) {
	manifest, err := LoadCurrent(store.dataRoot)
	if err != nil {
		return pointBundle{}, err
	}
	cellID, cellLocation := globalCell(location)
	key := manifest.RunID + "/" + cellID
	if bundle, err := store.readDisk(manifest.RunID, cellID, manifest.HasHourlyCloud()); err == nil {
		store.logf("ICON Global point cache hit run=%s cell=%s", manifest.RunID, cellID)
		return bundle, nil
	}

	store.mu.Lock()
	if flight, ok := store.flights[key]; ok {
		store.mu.Unlock()
		select {
		case <-flight.done:
			return flight.bundle, flight.err
		case <-ctx.Done():
			return pointBundle{}, ctx.Err()
		}
	}
	flight := &pointFlight{done: make(chan struct{})}
	store.flights[key] = flight
	store.mu.Unlock()

	started := time.Now()
	bundle, err := store.extract(ctx, manifest, cellID, cellLocation)
	if err == nil {
		if writeError := store.writeDisk(bundle); writeError != nil {
			store.logf("ICON Global point cache write failed: %v", writeError)
		}
		store.logf("ICON Global point extracted run=%s cell=%s duration=%s", manifest.RunID, cellID, time.Since(started).Round(time.Millisecond))
	}
	store.mu.Lock()
	flight.bundle, flight.err = bundle, err
	delete(store.flights, key)
	close(flight.done)
	store.mu.Unlock()
	return bundle, err
}

func (store *Store) extract(ctx context.Context, manifest LoadedManifest, cellID string, location forecast.Location) (pointBundle, error) {
	type result struct {
		kind     string
		vertical forecast.VerticalSeries
		surface  forecast.SurfaceSeries
		cloud    forecast.CloudSeries
		err      error
	}
	workContext, cancel := context.WithCancel(ctx)
	defer cancel()
	workCount := 2
	if manifest.HasHourlyCloud() {
		workCount++
	}
	results := make(chan result, workCount)
	go func() {
		value, err := store.extractVertical(workContext, manifest, location)
		results <- result{kind: "vertical", vertical: value, err: err}
	}()
	go func() {
		value, err := store.extractSurface(workContext, manifest, location)
		results <- result{kind: "surface", surface: value, err: err}
	}()
	if manifest.HasHourlyCloud() {
		go func() {
			value, err := store.extractCloud(workContext, manifest, location)
			results <- result{kind: "cloud", cloud: value, err: err}
		}()
	}
	bundle := pointBundle{Schema: pointCacheVersion, RunID: manifest.RunID, CellID: cellID, Created: time.Now().UTC()}
	for range workCount {
		item := <-results
		if item.err != nil {
			cancel()
			return pointBundle{}, item.err
		}
		switch item.kind {
		case "vertical":
			bundle.Vertical = item.vertical
		case "surface":
			bundle.Surface = item.surface
		case "cloud":
			bundle.Cloud = item.cloud
		}
	}
	return bundle, nil
}

func (store *Store) extractCloud(ctx context.Context, manifest LoadedManifest, location forecast.Location) (forecast.CloudSeries, error) {
	var heights map[int]float64
	if err := store.withRemappedPoint(ctx, filepath.Join(manifest.Directory, manifest.CloudGeometry.File), location, func(path string) error {
		var extractionError error
		heights, extractionError = iconeu.ExtractCloudHeights(ctx, store.runner, path, location)
		return extractionError
	}); err != nil {
		return forecast.CloudSeries{}, err
	}
	surfaceElevationM, err := iconeu.CloudSurfaceElevation(heights, globalSurfaceHalfLevel, "ICON Global")
	if err != nil {
		return forecast.CloudSeries{}, err
	}
	frames := make([]forecast.CloudFrame, len(manifest.CloudSteps))
	if err := store.extractSteps(ctx, len(frames), func(index int) error {
		step := manifest.CloudSteps[index]
		return store.withRemappedPoint(ctx, filepath.Join(manifest.Directory, step.File), location, func(path string) error {
			frame, extractionError := iconeu.ExtractCloudFrame(ctx, store.runner, path, location, step.ValidAt, manifest.CloudModelLevels, globalCloudGroundModelLevels, heights)
			frames[index] = frame
			return extractionError
		})
	}); err != nil {
		return forecast.CloudSeries{}, err
	}
	return forecast.CloudSeries{
		Location: location, Provider: manifest.Provider,
		Product: "ICON Global native-layer CLC/QC/QI/T + lower-atmosphere U/V/TKE",
		RunID:   manifest.RunID, BaseTime: manifest.BaseTime, GeneratedAt: time.Now().UTC(),
		TurbulenceValidUntil: manifest.BaseTime.Add(48 * time.Hour),
		SurfaceElevationM:    surfaceElevationM, Frames: frames,
	}, nil
}

func (store *Store) extractVertical(ctx context.Context, manifest LoadedManifest, location forecast.Location) (forecast.VerticalSeries, error) {
	frames := make([]forecast.VerticalFrame, len(manifest.PressureSteps))
	if err := store.extractSteps(ctx, len(frames), func(index int) error {
		step := manifest.PressureSteps[index]
		return store.withRemappedPoint(ctx, filepath.Join(manifest.Directory, step.File), location, func(path string) error {
			frame, err := iconeu.ExtractFrame(ctx, store.runner, path, location, iconeu.StepFile{ForecastHour: step.ForecastHour, ValidAt: step.ValidAt, Messages: step.Messages})
			frames[index] = frame
			return err
		})
	}); err != nil {
		return forecast.VerticalSeries{}, err
	}
	series := forecast.VerticalSeries{
		Location: location, Provider: manifest.Provider, Product: manifest.Product, RunID: manifest.RunID,
		Grid: manifest.Grid.GridName, AlgorithmVersion: forecast.SeeingPrototypeVersion,
		BaseTime: manifest.BaseTime, GeneratedAt: time.Now().UTC(), Frames: frames,
	}
	if err := series.Validate(); err != nil {
		return forecast.VerticalSeries{}, fmt.Errorf("validate ICON Global series: %w", err)
	}
	return series, nil
}

func (store *Store) extractSurface(ctx context.Context, manifest LoadedManifest, location forecast.Location) (forecast.SurfaceSeries, error) {
	extracted := make([]iconeu.ExtractedSurface, len(manifest.SurfaceSteps))
	if err := store.extractSteps(ctx, len(extracted), func(index int) error {
		step := manifest.SurfaceSteps[index]
		return store.withRemappedPoint(ctx, filepath.Join(manifest.Directory, step.File), location, func(path string) error {
			value, err := iconeu.ExtractSurfaceFrame(ctx, store.runner, path, location, step.ValidAt)
			extracted[index] = value
			return err
		})
	}); err != nil {
		return forecast.SurfaceSeries{}, err
	}
	frames := make([]forecast.SurfaceFrame, len(extracted))
	previous := 0.0
	for index, item := range extracted {
		frame := item.Frame
		if index == 0 {
			frame.PrecipitationMM = math.Max(0, item.AccumulatedPrecipMM)
		} else {
			frame.PrecipitationMM = math.Max(0, item.AccumulatedPrecipMM-previous)
		}
		previous = item.AccumulatedPrecipMM
		frames[index] = frame
	}
	return forecast.SurfaceSeries{
		Location: location, Provider: manifest.Provider, Product: "ICON Global single-level", RunID: manifest.RunID,
		BaseTime: manifest.BaseTime, GeneratedAt: time.Now().UTC(), StepHours: 1, Frames: frames,
	}, nil
}

func (store *Store) extractSteps(ctx context.Context, count int, extract func(int) error) error {
	workContext, cancel := context.WithCancel(ctx)
	defer cancel()
	jobs := make(chan int)
	errorsChannel := make(chan error, 1)
	var group sync.WaitGroup
	for range store.workers {
		group.Add(1)
		go func() {
			defer group.Done()
			for index := range jobs {
				if err := extract(index); err != nil {
					select {
					case errorsChannel <- err:
					default:
					}
					cancel()
					return
				}
			}
		}()
	}
	go func() {
		defer close(jobs)
		for index := range count {
			select {
			case jobs <- index:
			case <-workContext.Done():
				return
			}
		}
	}()
	group.Wait()
	select {
	case err := <-errorsChannel:
		return err
	default:
		return workContext.Err()
	}
}

func (store *Store) withRemappedPoint(ctx context.Context, source string, location forecast.Location, use func(string) error) error {
	temporaryRoot := filepath.Join(store.dataRoot, "tmp", "icon-global-points")
	if err := os.MkdirAll(temporaryRoot, 0o750); err != nil {
		return err
	}
	file, err := os.CreateTemp(temporaryRoot, ".point-*.grib2")
	if err != nil {
		return err
	}
	path := file.Name()
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Remove(path); err != nil {
		return err
	}
	defer func() { _ = os.Remove(path) }()
	target := fmt.Sprintf("remapnn,lon=%.6f/lat=%.6f", location.Longitude, location.Latitude)
	grid := GridPath(store.dataRoot)
	if _, err := os.Stat(grid); err != nil {
		return fmt.Errorf("ICON Global static grid is unavailable: %w", err)
	}
	output, err := store.runner.CombinedOutput(ctx, "cdo", "-s", "-f", "grb2", target, "-setgrid,"+grid, source, path)
	if err != nil {
		return fmt.Errorf("remap ICON Global point: %s", stringsLimited(output))
	}
	return use(path)
}

func globalCell(location forecast.Location) (string, forecast.Location) {
	const increment = 0.125
	latitude := math.Round(location.Latitude/increment) * increment
	longitude := math.Round(location.Longitude/increment) * increment
	cell := fmt.Sprintf("lat%+07.3f-lon%+08.3f", latitude, longitude)
	location.Latitude, location.Longitude = latitude, longitude
	return cell, location
}

func stringsLimited(output []byte) string {
	text := string(output)
	if len(text) > 500 {
		return text[:500] + "…"
	}
	return text
}

func (store *Store) cachePath(runID, cellID string) string {
	return filepath.Join(store.dataRoot, "cache", "points", "icon-global", pointCacheVersion, runID, cellID+".gob.gz")
}

func (store *Store) readDisk(runID, cellID string, requireCloud bool) (pointBundle, error) {
	file, err := os.Open(store.cachePath(runID, cellID))
	if err != nil {
		return pointBundle{}, err
	}
	defer func() { _ = file.Close() }()
	compressed, err := gzip.NewReader(file)
	if err != nil {
		return pointBundle{}, err
	}
	defer func() { _ = compressed.Close() }()
	var bundle pointBundle
	if err := gob.NewDecoder(compressed).Decode(&bundle); err != nil {
		return pointBundle{}, err
	}
	if bundle.Schema != pointCacheVersion || bundle.RunID != runID || bundle.CellID != cellID || len(bundle.Vertical.Frames) != 25 || len(bundle.Surface.Frames) != 79 {
		return pointBundle{}, errors.New("ICON Global point cache identity mismatch")
	}
	if requireCloud && len(bundle.Cloud.Frames) != 79 {
		return pointBundle{}, errors.New("ICON Global point cache lacks current cloud profile")
	}
	return bundle, nil
}

func (store *Store) writeDisk(bundle pointBundle) error {
	path := store.cachePath(bundle.RunID, bundle.CellID)
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".point-*.tmp")
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
	compressed := gzip.NewWriter(file)
	if err := gob.NewEncoder(compressed).Encode(bundle); err != nil {
		return err
	}
	if err := compressed.Close(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporary, path); err != nil {
		return err
	}
	failed = false
	go store.prunePointCache(2, 512)
	return nil
}

func (store *Store) prunePointCache(keepRuns, keepEntries int) {
	root := filepath.Join(store.dataRoot, "cache", "points", "icon-global", pointCacheVersion)
	runs, err := os.ReadDir(root)
	if err != nil {
		return
	}
	type entry struct {
		path string
		info os.FileInfo
	}
	var runEntries []entry
	for _, run := range runs {
		info, err := run.Info()
		if err == nil && run.IsDir() {
			runEntries = append(runEntries, entry{path: filepath.Join(root, run.Name()), info: info})
		}
	}
	sort.Slice(runEntries, func(i, j int) bool { return runEntries[i].path > runEntries[j].path })
	for _, old := range runEntries[min(keepRuns, len(runEntries)):] {
		_ = os.RemoveAll(old.path)
	}
	var points []entry
	for _, run := range runEntries[:min(keepRuns, len(runEntries))] {
		files, _ := os.ReadDir(run.path)
		for _, file := range files {
			info, err := file.Info()
			if err == nil && !file.IsDir() {
				points = append(points, entry{path: filepath.Join(run.path, file.Name()), info: info})
			}
		}
	}
	sort.Slice(points, func(i, j int) bool { return points[i].info.ModTime().After(points[j].info.ModTime()) })
	for _, old := range points[min(keepEntries, len(points)):] {
		_ = os.Remove(old.path)
	}
}
