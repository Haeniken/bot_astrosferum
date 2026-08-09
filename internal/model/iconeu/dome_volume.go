package iconeu

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"bot_astrosferum/internal/forecast"
	"bot_astrosferum/internal/model"
)

const (
	domeVolumeMemoryColumns = 64
	domeVolumeGroupHints    = 1024
	// Positions this close to an integer native-grid coordinate are assigned
	// exactly to that grid line. Root and smoothness certificates must include
	// the resulting finite jump in the evaluated bilinear weights.
	domeGridLineSnapTolerance = 1e-12
)

var (
	domeFullAvailable = forecast.AstrodomePrimitiveFieldSet(forecast.AstrodomePrimitivePressure) |
		forecast.AstrodomePrimitiveFieldSet(forecast.AstrodomePrimitiveTemperature) |
		forecast.AstrodomePrimitiveFieldSet(forecast.AstrodomePrimitiveSpecificHumidity) |
		forecast.AstrodomePrimitiveFieldSet(forecast.AstrodomePrimitiveCloudLiquid) |
		forecast.AstrodomePrimitiveFieldSet(forecast.AstrodomePrimitiveCloudIce) |
		forecast.AstrodomePrimitiveFieldSet(forecast.AstrodomePrimitiveCloudFraction) |
		forecast.AstrodomePrimitiveFieldSet(forecast.AstrodomePrimitiveEastwardWind) |
		forecast.AstrodomePrimitiveFieldSet(forecast.AstrodomePrimitiveNorthwardWind)
	domeHalfAvailable = forecast.AstrodomePrimitiveFieldSet(forecast.AstrodomePrimitiveVerticalWind) |
		forecast.AstrodomePrimitiveFieldSet(forecast.AstrodomePrimitiveTKE)
)

// DomeVolume is an immutable view of one published ICON-EU Astrodome run.
// It extracts only native primitives at exact regular-grid nodes. Any spatial,
// temporal, vertical, or nonlinear science reconstruction belongs to the
// provider-neutral forecast package.
type DomeVolume struct {
	providerRoot string
	tempRoot     string
	manifest     LoadedDomeManifest
	base         LoadedManifest
	identity     forecast.AstrodomePrimitiveVolumeIdentity
	runner       CommandRunner
	fieldTimes   map[forecast.AstrodomePrimitiveField][]time.Time
	surfaceFiles map[int]domeVolumeSource

	mu                         sync.Mutex
	clock                      uint64
	cache                      map[string]domeVolumeCacheEntry
	groups                     map[string][]domeColumnAddress
	flights                    map[string]*domeVolumeFlight
	precipitationPackingErrors map[int]float64
	sourceGridMu               sync.Mutex
	sourceGrids                map[string]domeNativeSourceGrid
	cacheLimit                 int
	extractionWorkers          int
	pinnedColumns              atomic.Pointer[domePinnedColumnSnapshot]
}

// domePinnedColumnSnapshot is published only after a complete Astrodome
// preload. Its map and column arrays are immutable for the lifetime of the
// footprint, so concurrent numerical workers may read them without touching
// the ordinary mutable LRU clock and mutex.
type domePinnedColumnSnapshot struct {
	columns map[string]forecast.AstrodomePrimitiveColumn
}

type domeVolumeSource struct {
	relative string
	bytes    int64
	messages int
}

type domeColumnAddress struct {
	id             string
	latitudeIndex  int
	longitudeIndex int
	location       forecast.Location
}

type domeVolumeCacheEntry struct {
	column forecast.AstrodomePrimitiveColumn
	used   uint64
}

type domeVolumeFlight struct {
	done    chan struct{}
	columns map[string]forecast.AstrodomePrimitiveColumn
	err     error
}

var _ forecast.AstrodomePrimitiveVolume = (*DomeVolume)(nil)
var _ forecast.AstrodomeScienceNativeContextResolver = (*DomeVolume)(nil)

// NewDomeVolume opens exactly the supplied immutable publication. The
// supplied manifest is reloaded and hashed, and its base manifest is rebound
// before any extractor is exposed. ecCodesWorkers bounds concurrent CDO
// subprocesses across all callers of this volume.
func NewDomeVolume(
	dataRoot string,
	tempRoot string,
	loaded LoadedDomeManifest,
	ecCodesWorkers int,
) (*DomeVolume, error) {
	if ecCodesWorkers < 1 {
		return nil, errors.New("ICON-EU Astrodome ecCodes worker count must be positive")
	}
	runner := &limitedRunner{runner: execRunner{}, semaphore: make(chan struct{}, ecCodesWorkers)}
	volume, err := newDomeVolume(dataRoot, tempRoot, loaded, runner, domeVolumeMemoryColumns)
	if err != nil {
		return nil, err
	}
	volume.extractionWorkers = ecCodesWorkers
	return volume, nil
}

func newDomeVolume(
	dataRoot string,
	tempRoot string,
	loaded LoadedDomeManifest,
	runner CommandRunner,
	cacheLimit int,
) (*DomeVolume, error) {
	if strings.TrimSpace(dataRoot) == "" {
		return nil, errors.New("ICON-EU Astrodome data root is required")
	}
	if runner == nil {
		return nil, errors.New("ICON-EU Astrodome command runner is required")
	}
	if cacheLimit < 1 {
		return nil, errors.New("ICON-EU Astrodome column cache limit must be positive")
	}
	if err := loaded.Validate(); err != nil {
		return nil, fmt.Errorf("invalid ICON-EU Astrodome volume manifest: %w", err)
	}
	if !validDomeDigest(loaded.ManifestSHA256) {
		return nil, errors.New("ICON-EU Astrodome loaded manifest needs its exact SHA-256")
	}

	providerRoot, err := filepath.Abs(filepath.Join(dataRoot, "models", "icon-eu"))
	if err != nil {
		return nil, fmt.Errorf("resolve ICON-EU Astrodome provider root: %w", err)
	}
	wantDirectory := filepath.Join(providerRoot, "dome-runs", loaded.RunID, loaded.InputContractSHA256)
	directory, err := filepath.Abs(loaded.Directory)
	if err != nil || directory != wantDirectory {
		return nil, errors.New("ICON-EU Astrodome manifest is outside its immutable publication directory")
	}
	fresh, err := LoadDomeManifest(filepath.Join(wantDirectory, "manifest.json"))
	if err != nil {
		return nil, fmt.Errorf("reload immutable ICON-EU Astrodome manifest: %w", err)
	}
	if fresh.ManifestSHA256 != loaded.ManifestSHA256 {
		return nil, errors.New("ICON-EU Astrodome manifest changed after it was selected")
	}
	loadedJSON, err := canonicalDomeManifestJSON(loaded.DomeManifest)
	if err != nil {
		return nil, err
	}
	freshJSON, err := canonicalDomeManifestJSON(fresh.DomeManifest)
	if err != nil {
		return nil, err
	}
	if string(loadedJSON) != string(freshJSON) {
		return nil, errors.New("ICON-EU Astrodome caller manifest differs from the immutable manifest on disk")
	}

	basePath := filepath.Join(providerRoot, "runs", fresh.RunID, "manifest.json")
	baseDigest, _, err := fileDigest(basePath)
	if err != nil {
		return nil, fmt.Errorf("hash ICON-EU Astrodome base manifest: %w", err)
	}
	if baseDigest != fresh.BaseManifestSHA256 {
		return nil, errors.New("ICON-EU Astrodome base manifest changed after publication")
	}
	base, err := LoadManifest(basePath)
	if err != nil {
		return nil, fmt.Errorf("reload ICON-EU Astrodome base manifest: %w", err)
	}
	if base.RunID != fresh.RunID || !base.BaseTime.Equal(fresh.BaseTime) || !base.HasAstrodomeSurface() || !base.HasHourlyCloud() {
		return nil, errors.New("ICON-EU Astrodome base manifest no longer satisfies its acquisition contract")
	}

	surfaceFiles := make(map[int]domeVolumeSource, len(DomeNativeForecastHours()))
	for _, step := range base.SurfaceSteps {
		if step.ForecastHour > 78 {
			continue
		}
		surfaceFiles[step.ForecastHour] = domeVolumeSource{
			relative: filepath.Join("runs", base.RunID, step.File), bytes: step.Bytes, messages: step.Messages,
		}
	}
	for _, step := range fresh.SurfaceExtensionSteps {
		surfaceFiles[step.ForecastHour] = domeVolumeSource{
			relative: step.File, bytes: step.Bytes, messages: step.Messages,
		}
	}
	if len(surfaceFiles) != len(DomeNativeForecastHours()) {
		return nil, errors.New("ICON-EU Astrodome surface native-time inventory is incomplete")
	}

	fieldTimes := make(map[forecast.AstrodomePrimitiveField][]time.Time)
	allTimes := make([]time.Time, len(fresh.ModelSteps))
	for index, step := range fresh.ModelSteps {
		allTimes[index] = step.ValidAt.UTC()
	}
	for _, field := range domeAtmosphericFields() {
		fieldTimes[field] = append([]time.Time(nil), allTimes...)
	}
	for _, field := range domeSurfacePrimitiveFields() {
		fieldTimes[field] = append([]time.Time(nil), allTimes...)
	}
	// f000 contains a zero-length accumulation rather than an observable
	// precipitation or maximum-gust interval. Keep that distinction explicit
	// in their field-specific axes.
	fieldTimes[forecast.AstrodomePrimitivePrecipitationAccumulation] = append([]time.Time(nil), allTimes[1:]...)
	fieldTimes[forecast.AstrodomePrimitiveWindGust10M] = append([]time.Time(nil), allTimes[1:]...)

	return &DomeVolume{
		providerRoot: providerRoot,
		tempRoot:     tempRoot,
		manifest:     fresh,
		base:         base,
		identity: forecast.AstrodomePrimitiveVolumeIdentity{
			Provider: fresh.Provider, Product: fresh.Product, Grid: fresh.Grid.GridName,
			RunID: fresh.RunID, RunBaseTime: fresh.BaseTime.UTC(),
			RunManifestDigest:    "sha256:" + fresh.ManifestSHA256,
			InputContractVersion: fresh.InputContractVersion,
		},
		runner: runner, fieldTimes: fieldTimes, surfaceFiles: surfaceFiles,
		cache: make(map[string]domeVolumeCacheEntry), groups: make(map[string][]domeColumnAddress),
		flights: make(map[string]*domeVolumeFlight), precipitationPackingErrors: make(map[int]float64),
		sourceGrids: make(map[string]domeNativeSourceGrid),
		cacheLimit:  cacheLimit,
	}, nil
}

func canonicalDomeManifestJSON(manifest DomeManifest) ([]byte, error) {
	encoded, err := json.Marshal(manifest)
	if err != nil {
		return nil, fmt.Errorf("encode ICON-EU Astrodome manifest identity: %w", err)
	}
	return encoded, nil
}

// Identity returns a value copy, so callers cannot retarget this volume.
func (volume *DomeVolume) Identity() forecast.AstrodomePrimitiveVolumeIdentity {
	if volume == nil {
		return forecast.AstrodomePrimitiveVolumeIdentity{}
	}
	return volume.identity
}

// NativeValidTimes returns the field's actual published cadence. It never
// manufactures hourly timestamps between the f078, f081 and f084 brackets.
func (volume *DomeVolume) NativeValidTimes(field forecast.AstrodomePrimitiveField) []time.Time {
	if volume == nil {
		return nil
	}
	return append([]time.Time(nil), volume.fieldTimes[field]...)
}

// HorizontalStencil returns the exact regular-grid cell around location. At
// an outer domain edge it uses the final interior cell with a zero-weight
// opposite side; four unique supports remain explicit.
func (volume *DomeVolume) HorizontalStencil(
	ctx context.Context,
	location forecast.Location,
) (forecast.AstrodomeHorizontalStencil, error) {
	return volume.horizontalStencil(ctx, location, true)
}

// horizontalStencil resolves the immutable regular-grid geometry. The
// rememberGroup switch is used only by the lazy, non-preloaded extraction
// path: a pinned Astrodome footprint already contains every required column,
// so mutating the extraction hint map for every Runge--Kutta stage would add
// lock contention without changing either data or interpolation weights.
func (volume *DomeVolume) horizontalStencil(
	ctx context.Context,
	location forecast.Location,
	rememberGroup bool,
) (forecast.AstrodomeHorizontalStencil, error) {
	if volume == nil {
		return forecast.AstrodomeHorizontalStencil{}, errors.New("ICON-EU Astrodome volume is required")
	}
	if err := ctx.Err(); err != nil {
		return forecast.AstrodomeHorizontalStencil{}, err
	}
	if err := forecast.ValidateCoordinates(location.Latitude, location.Longitude); err != nil {
		return forecast.AstrodomeHorizontalStencil{}, err
	}
	if !volume.manifest.Grid.Contains(location) {
		return forecast.AstrodomeHorizontalStencil{}, errors.New("coordinates are outside the ICON-EU Astrodome domain")
	}
	cell, err := domeGridCell(volume.manifest.Grid, location)
	if err != nil {
		return forecast.AstrodomeHorizontalStencil{}, err
	}
	addresses := []domeColumnAddress{
		volume.domeColumnAddress(cell.south, cell.west),
		volume.domeColumnAddress(cell.south, cell.east),
		volume.domeColumnAddress(cell.north, cell.west),
		volume.domeColumnAddress(cell.north, cell.east),
	}
	weights := [4]float64{
		(1 - cell.latitudeFraction) * (1 - cell.longitudeFraction),
		(1 - cell.latitudeFraction) * cell.longitudeFraction,
		cell.latitudeFraction * (1 - cell.longitudeFraction),
		cell.latitudeFraction * cell.longitudeFraction,
	}
	stencil := forecast.AstrodomeHorizontalStencil{}
	for index := range addresses {
		stencil.Supports[index] = forecast.AstrodomeHorizontalSupport{
			ColumnID: addresses[index].id, Location: addresses[index].location, Weight: weights[index],
		}
	}
	if err := stencil.Validate(); err != nil {
		return forecast.AstrodomeHorizontalStencil{}, err
	}
	if rememberGroup {
		volume.rememberDomeGroup(addresses)
	}
	return stencil, nil
}

type domeRegularCell struct {
	south, north                        int
	west, east                          int
	latitudeFraction, longitudeFraction float64
}

func domeGridCell(grid model.Coverage, location forecast.Location) (domeRegularCell, error) {
	if grid.Increment <= 0 || !finiteDomeVolume(grid.Increment) {
		return domeRegularCell{}, errors.New("ICON-EU Astrodome grid increment is invalid")
	}
	latMax := int(math.Round((grid.MaxLat - grid.MinLat) / grid.Increment))
	lonMax := int(math.Round((grid.MaxLon - grid.MinLon) / grid.Increment))
	if latMax < 1 || lonMax < 1 {
		return domeRegularCell{}, errors.New("ICON-EU Astrodome grid has no bilinear cell")
	}
	latPosition := (location.Latitude - grid.MinLat) / grid.Increment
	lonPosition := (location.Longitude - grid.MinLon) / grid.Increment
	south, latFraction, err := domeLowerGridIndex(latPosition, latMax)
	if err != nil {
		return domeRegularCell{}, fmt.Errorf("ICON-EU Astrodome latitude: %w", err)
	}
	west, lonFraction, err := domeLowerGridIndex(lonPosition, lonMax)
	if err != nil {
		return domeRegularCell{}, fmt.Errorf("ICON-EU Astrodome longitude: %w", err)
	}
	return domeRegularCell{
		south: south, north: south + 1, west: west, east: west + 1,
		latitudeFraction: latFraction, longitudeFraction: lonFraction,
	}, nil
}

func domeLowerGridIndex(position float64, maximum int) (int, float64, error) {
	if !finiteDomeVolume(position) || position < -1e-10 || position > float64(maximum)+1e-10 {
		return 0, 0, errors.New("coordinate lies outside the regular grid")
	}
	position = math.Max(0, math.Min(float64(maximum), position))
	nearest := math.Round(position)
	if math.Abs(position-nearest) <= domeGridLineSnapTolerance {
		position = nearest
	}
	if position == float64(maximum) {
		return maximum - 1, 1, nil
	}
	lower := int(math.Floor(position))
	return lower, position - float64(lower), nil
}

func (volume *DomeVolume) domeColumnAddress(latitudeIndex, longitudeIndex int) domeColumnAddress {
	return domeColumnAddress{
		id:            fmt.Sprintf("lat%04d-lon%04d", latitudeIndex, longitudeIndex),
		latitudeIndex: latitudeIndex, longitudeIndex: longitudeIndex,
		location: forecast.Location{
			Latitude:  volume.manifest.Grid.MinLat + float64(latitudeIndex)*volume.manifest.Grid.Increment,
			Longitude: volume.manifest.Grid.MinLon + float64(longitudeIndex)*volume.manifest.Grid.Increment,
			TimeZone:  "UTC",
		},
	}
}

func (volume *DomeVolume) rememberDomeGroup(addresses []domeColumnAddress) {
	group := append([]domeColumnAddress(nil), addresses...)
	protected := make(map[string]struct{}, len(group))
	volume.mu.Lock()
	defer volume.mu.Unlock()
	for _, address := range group {
		volume.groups[address.id] = group
		protected[address.id] = struct{}{}
	}
	for len(volume.groups) > domeVolumeGroupHints {
		for id := range volume.groups {
			if _, keep := protected[id]; keep {
				continue
			}
			delete(volume.groups, id)
			break
		}
	}
}

// Column extracts one native grid column. If HorizontalStencil was called
// first, all four surrounding columns are extracted in the same CDO pass per
// time step, avoiding four complete reads of every GRIB bundle.
func (volume *DomeVolume) Column(ctx context.Context, columnID string) (forecast.AstrodomePrimitiveColumn, error) {
	if volume == nil {
		return forecast.AstrodomePrimitiveColumn{}, errors.New("ICON-EU Astrodome volume is required")
	}
	if err := ctx.Err(); err != nil {
		return forecast.AstrodomePrimitiveColumn{}, err
	}
	address, err := volume.parseDomeColumnID(columnID)
	if err != nil {
		return forecast.AstrodomePrimitiveColumn{}, err
	}

	volume.mu.Lock()
	if cached, ok := volume.cache[columnID]; ok {
		volume.clock++
		cached.used = volume.clock
		volume.cache[columnID] = cached
		volume.mu.Unlock()
		return cloneDomeColumn(cached.column), nil
	}
	group := append([]domeColumnAddress(nil), volume.groups[columnID]...)
	if len(group) == 0 {
		group = []domeColumnAddress{address}
	}
	groupKey := domeColumnGroupKey(group)
	if flight, ok := volume.flights[groupKey]; ok {
		volume.mu.Unlock()
		select {
		case <-ctx.Done():
			return forecast.AstrodomePrimitiveColumn{}, ctx.Err()
		case <-flight.done:
			if flight.err != nil {
				return forecast.AstrodomePrimitiveColumn{}, flight.err
			}
			column, ok := flight.columns[columnID]
			if !ok {
				return forecast.AstrodomePrimitiveColumn{}, fmt.Errorf("ICON-EU Astrodome extraction omitted column %q", columnID)
			}
			return cloneDomeColumn(column), nil
		}
	}
	flight := &domeVolumeFlight{done: make(chan struct{})}
	volume.flights[groupKey] = flight
	volume.mu.Unlock()

	columns, loadErr := volume.extractDomeColumns(ctx, group)
	volume.mu.Lock()
	flight.columns, flight.err = columns, loadErr
	if loadErr == nil {
		for id, column := range columns {
			volume.clock++
			volume.cache[id] = domeVolumeCacheEntry{column: cloneDomeColumn(column), used: volume.clock}
		}
		volume.pruneDomeColumnCacheLocked()
	}
	for _, address := range group {
		if current := volume.groups[address.id]; domeColumnGroupKey(current) == groupKey {
			delete(volume.groups, address.id)
		}
	}
	delete(volume.flights, groupKey)
	close(flight.done)
	volume.mu.Unlock()
	if loadErr != nil {
		return forecast.AstrodomePrimitiveColumn{}, loadErr
	}
	column, ok := columns[columnID]
	if !ok {
		return forecast.AstrodomePrimitiveColumn{}, fmt.Errorf("ICON-EU Astrodome extraction omitted column %q", columnID)
	}
	return cloneDomeColumn(column), nil
}

func (volume *DomeVolume) parseDomeColumnID(columnID string) (domeColumnAddress, error) {
	var latitudeIndex, longitudeIndex int
	if _, err := fmt.Sscanf(columnID, "lat%04d-lon%04d", &latitudeIndex, &longitudeIndex); err != nil ||
		columnID != fmt.Sprintf("lat%04d-lon%04d", latitudeIndex, longitudeIndex) {
		return domeColumnAddress{}, fmt.Errorf("invalid ICON-EU Astrodome column ID %q", columnID)
	}
	latMax := int(math.Round((volume.manifest.Grid.MaxLat - volume.manifest.Grid.MinLat) / volume.manifest.Grid.Increment))
	lonMax := int(math.Round((volume.manifest.Grid.MaxLon - volume.manifest.Grid.MinLon) / volume.manifest.Grid.Increment))
	if latitudeIndex < 0 || latitudeIndex > latMax || longitudeIndex < 0 || longitudeIndex > lonMax {
		return domeColumnAddress{}, fmt.Errorf("ICON-EU Astrodome column %q lies outside the grid", columnID)
	}
	return volume.domeColumnAddress(latitudeIndex, longitudeIndex), nil
}

func domeColumnGroupKey(group []domeColumnAddress) string {
	ids := make([]string, len(group))
	for index, address := range group {
		ids[index] = address.id
	}
	sort.Strings(ids)
	return strings.Join(ids, "/")
}

func (volume *DomeVolume) pruneDomeColumnCacheLocked() {
	for len(volume.cache) > volume.cacheLimit {
		oldestID := ""
		oldestUse := ^uint64(0)
		for id, cached := range volume.cache {
			if cached.used < oldestUse {
				oldestID, oldestUse = id, cached.used
			}
		}
		delete(volume.cache, oldestID)
	}
}

func cloneDomeColumn(column forecast.AstrodomePrimitiveColumn) forecast.AstrodomePrimitiveColumn {
	clone := column
	clone.HalfLevelGeometry = append([]forecast.AstrodomeHalfLevelGeometry(nil), column.HalfLevelGeometry...)
	clone.Frames = append([]forecast.AstrodomePrimitiveColumnFrame(nil), column.Frames...)
	for index := range clone.Frames {
		clone.Frames[index].FullLevels = append([]forecast.AstrodomeFullLevelPrimitives(nil), column.Frames[index].FullLevels...)
		clone.Frames[index].HalfLevels = append([]forecast.AstrodomeHalfLevelPrimitives(nil), column.Frames[index].HalfLevels...)
	}
	return clone
}

func domeAtmosphericFields() []forecast.AstrodomePrimitiveField {
	return []forecast.AstrodomePrimitiveField{
		forecast.AstrodomePrimitivePressure,
		forecast.AstrodomePrimitiveTemperature,
		forecast.AstrodomePrimitiveSpecificHumidity,
		forecast.AstrodomePrimitiveCloudLiquid,
		forecast.AstrodomePrimitiveCloudIce,
		forecast.AstrodomePrimitiveCloudFraction,
		forecast.AstrodomePrimitiveEastwardWind,
		forecast.AstrodomePrimitiveNorthwardWind,
		forecast.AstrodomePrimitiveVerticalWind,
		forecast.AstrodomePrimitiveTKE,
	}
}

func domeSurfacePrimitiveFields() []forecast.AstrodomePrimitiveField {
	return []forecast.AstrodomePrimitiveField{
		forecast.AstrodomePrimitiveMixedLayerDepth,
		forecast.AstrodomePrimitiveVisibility,
		forecast.AstrodomePrimitiveRelativeHumidity,
		forecast.AstrodomePrimitiveTemperature2M,
		forecast.AstrodomePrimitiveDewPoint2M,
		forecast.AstrodomePrimitiveEastwardWind10M,
		forecast.AstrodomePrimitiveNorthwardWind10M,
		forecast.AstrodomePrimitiveWindGust10M,
		forecast.AstrodomePrimitivePrecipitationAccumulation,
		forecast.AstrodomePrimitiveTotalColumnWaterVapour,
		forecast.AstrodomePrimitiveTotalColumnCloudLiquid,
		forecast.AstrodomePrimitiveTotalColumnCloudIce,
		forecast.AstrodomePrimitiveSurfacePressure,
		forecast.AstrodomePrimitiveSpecificHumidity2M,
	}
}

func finiteDomeVolume(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}
