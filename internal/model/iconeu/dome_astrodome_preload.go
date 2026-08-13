package iconeu

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unsafe"

	"bot_astrosferum/internal/forecast"
	"bot_astrosferum/internal/model"
)

const (
	DomeAstrodomeDefaultResidentLimitBytes uint64 = 10 << 30
	DomeAstrodomePreloadBatchColumns              = 4096
	// Four native-grid indices give at least about 13.8 km cross-track
	// allowance at 60 degrees north on the 0.0625-degree grid. This is in
	// addition to the geometry-derived path envelope and its explicit guard.
	DomeAstrodomeRefractionGridMargin = 4
	DomeAstrodomeEnvelopeSampleM      = 2000.0
	DomeAstrodomeTopGuardM            = 2000.0
	DomeAstrodomePathGuardM           = 5000.0
)

type DomeAstrodomePreloadRequest struct {
	Observer           forecast.Location
	Profile            forecast.AstrodomeGridProfile
	Refraction         forecast.AstrodomeRefractionCalibration
	ResidentLimitBytes uint64
}

type DomeAstrodomePreloadReport struct {
	UniqueColumns          int     `json:"unique_columns"`
	ProjectedResidentBytes uint64  `json:"projected_resident_bytes"`
	ExtractionBatches      int     `json:"extraction_batches"`
	GridMarginColumns      int     `json:"grid_margin_columns"`
	EnvelopePathM          float64 `json:"envelope_path_m"`
	SourceColumnPlanDigest string  `json:"source_column_plan_digest"`
}

type domeAstrodomeEnvelopeRay struct {
	elevationDegrees float64
	azimuthDegrees   *float64
	pathLimitM       float64
}

// DomeAstrodomeFootprint pins a conservative raw-primitive corridor for one
// complete job. Close restores the volume's ordinary LRU size. It contains no
// derived meteorology and never interpolates a finished science product.
type DomeAstrodomeFootprint struct {
	volume        *DomeVolume
	columnIDs     map[string]struct{}
	snapshot      *domePinnedColumnSnapshot
	previousLimit int
	report        DomeAstrodomePreloadReport
	once          sync.Once
}

var _ forecast.AstrodomeImmutableColumnViewVolume = (*DomeAstrodomeFootprint)(nil)
var _ forecast.AstrodomeRefractionDomainResolver = (*DomeAstrodomeFootprint)(nil)
var _ forecast.AstrodomeScienceNativeContextResolver = (*DomeAstrodomeFootprint)(nil)

func (footprint *DomeAstrodomeFootprint) TrustAstrodomeImmutableColumnViews() bool { return true }

func (footprint *DomeAstrodomeFootprint) Identity() forecast.AstrodomePrimitiveVolumeIdentity {
	if footprint == nil || footprint.volume == nil {
		return forecast.AstrodomePrimitiveVolumeIdentity{}
	}
	return footprint.volume.Identity()
}

func (footprint *DomeAstrodomeFootprint) NativeValidTimes(field forecast.AstrodomePrimitiveField) []time.Time {
	if footprint == nil || footprint.volume == nil {
		return nil
	}
	return footprint.volume.NativeValidTimes(field)
}

func (footprint *DomeAstrodomeFootprint) HorizontalStencil(
	ctx context.Context,
	location forecast.Location,
) (forecast.AstrodomeHorizontalStencil, error) {
	if footprint == nil || footprint.volume == nil {
		return forecast.AstrodomeHorizontalStencil{}, errors.New("ICON-EU Astrodome footprint is required")
	}
	stencil, err := footprint.volume.horizontalStencil(ctx, location, false)
	if err != nil {
		return forecast.AstrodomeHorizontalStencil{}, err
	}
	if err := footprint.requireStencil(stencil); err != nil {
		return forecast.AstrodomeHorizontalStencil{}, err
	}
	return stencil, nil
}

func (footprint *DomeAstrodomeFootprint) Column(
	ctx context.Context,
	columnID string,
) (forecast.AstrodomePrimitiveColumn, error) {
	if !footprint.ContainsColumn(columnID) {
		return forecast.AstrodomePrimitiveColumn{}, fmt.Errorf("ICON-EU Astrodome ray escaped preloaded footprint at %q", columnID)
	}
	return footprint.volume.domeCachedColumnView(ctx, columnID)
}

func (footprint *DomeAstrodomeFootprint) ResolveAstrodomeRefractionDomain(
	ctx context.Context,
	validAt time.Time,
	point forecast.AstrodomeRayPoint,
	stencil forecast.AstrodomeHorizontalStencil,
) (forecast.AstrodomeRefractionDomainPoint, error) {
	if err := footprint.requireStencil(stencil); err != nil {
		return forecast.AstrodomeRefractionDomainPoint{}, err
	}
	return footprint.volume.ResolveAstrodomeRefractionDomain(ctx, validAt, point, stencil)
}

func (footprint *DomeAstrodomeFootprint) ResolveAstrodomeScienceNativeContext(
	ctx context.Context,
	validAt time.Time,
	point forecast.AstrodomeRayPoint,
	stencil forecast.AstrodomeHorizontalStencil,
) (forecast.AstrodomeScienceNativeContext, error) {
	if err := footprint.requireStencil(stencil); err != nil {
		return forecast.AstrodomeScienceNativeContext{}, err
	}
	return footprint.volume.ResolveAstrodomeScienceNativeContext(ctx, validAt, point, stencil)
}

func (footprint *DomeAstrodomeFootprint) requireStencil(stencil forecast.AstrodomeHorizontalStencil) error {
	if footprint == nil || footprint.volume == nil {
		return errors.New("ICON-EU Astrodome footprint is required")
	}
	for _, support := range stencil.Supports {
		if !footprint.ContainsColumn(support.ColumnID) {
			return fmt.Errorf("ICON-EU Astrodome ray escaped preloaded footprint at %q", support.ColumnID)
		}
	}
	return nil
}

func (footprint *DomeAstrodomeFootprint) AstrodomeScienceSiteAt(
	ctx context.Context,
	validAt time.Time,
	location forecast.Location,
) (forecast.AstrodomeScienceSiteInputs, error) {
	stencil, err := footprint.HorizontalStencil(ctx, location)
	if err != nil {
		return forecast.AstrodomeScienceSiteInputs{}, err
	}
	if err := footprint.requireStencil(stencil); err != nil {
		return forecast.AstrodomeScienceSiteInputs{}, err
	}
	return footprint.volume.AstrodomeScienceSiteAt(ctx, validAt, location)
}

func (footprint *DomeAstrodomeFootprint) AstrodomeSurfaceHeightAt(
	ctx context.Context,
	location forecast.Location,
) (float64, error) {
	if _, err := footprint.HorizontalStencil(ctx, location); err != nil {
		return 0, err
	}
	return footprint.volume.AstrodomeSurfaceHeightAt(ctx, location)
}

func (footprint *DomeAstrodomeFootprint) BuildAstrodomeSciencePath(
	ctx context.Context,
	ray forecast.AstrodomeRefractedRay,
	validAt time.Time,
	calibration forecast.AstrodomeScienceCalibration,
) (forecast.AstrodomeSciencePath, error) {
	path, err := footprint.volume.BuildAstrodomeSciencePath(ctx, ray, validAt, calibration)
	if err != nil {
		return path, err
	}
	for _, cell := range path.Cells {
		// Resolve one interior point and verify all four native supports. This is
		// a preflight assertion, not an on-demand extraction fallback.
		point, pointErr := ray.PointAtPathLength((cell.StartPathM + cell.EndPathM) / 2)
		if pointErr != nil {
			return forecast.AstrodomeSciencePath{}, pointErr
		}
		if _, stencilErr := footprint.HorizontalStencil(ctx, point.Location); stencilErr != nil {
			return forecast.AstrodomeSciencePath{}, stencilErr
		}
	}
	path.NativeContext = footprint
	return path, nil
}

func (footprint *DomeAstrodomeFootprint) Report() DomeAstrodomePreloadReport {
	if footprint == nil {
		return DomeAstrodomePreloadReport{}
	}
	return footprint.report
}

func (footprint *DomeAstrodomeFootprint) ContainsColumn(columnID string) bool {
	if footprint == nil {
		return false
	}
	_, ok := footprint.columnIDs[columnID]
	return ok
}

func (footprint *DomeAstrodomeFootprint) Close() error {
	if footprint == nil || footprint.volume == nil {
		return nil
	}
	footprint.once.Do(func() {
		footprint.volume.pinnedColumns.CompareAndSwap(footprint.snapshot, nil)
		footprint.volume.mu.Lock()
		footprint.volume.cacheLimit = footprint.previousLimit
		footprint.volume.pruneDomeColumnCacheLocked()
		footprint.volume.mu.Unlock()
	})
	return nil
}

// PreloadAstrodomeFootprint obtains the exact native-grid maximum HHL1 from
// the immutable geometry object, then enumerates each canonical node at its
// own elevation through that conservative model-top bound plus explicit
// vertical, path, and cross-track guards. It extracts each native timestamp
// in multipoint batches and pins the immutable columns for the complete job.
func (volume *DomeVolume) PreloadAstrodomeFootprint(
	ctx context.Context,
	request DomeAstrodomePreloadRequest,
) (*DomeAstrodomeFootprint, error) {
	if volume == nil {
		return nil, errors.New("ICON-EU Astrodome volume is required")
	}
	if err := forecast.ValidateCoordinates(request.Observer.Latitude, request.Observer.Longitude); err != nil {
		return nil, err
	}
	if err := request.Profile.Validate(); err != nil {
		return nil, err
	}
	if err := request.Refraction.Validate(); err != nil {
		return nil, err
	}
	nodes, err := request.Profile.Nodes()
	if err != nil {
		return nil, err
	}
	return volume.preloadAstrodomeNodeFootprint(ctx, request.Observer, nodes, request.Refraction, request.ResidentLimitBytes)
}

func (volume *DomeVolume) preloadAstrodomeNodeFootprint(
	ctx context.Context,
	observer forecast.Location,
	nodes []forecast.AstrodomeGridNode,
	refraction forecast.AstrodomeRefractionCalibration,
	residentLimitBytes uint64,
) (*DomeAstrodomeFootprint, error) {
	limit := residentLimitBytes
	if limit == 0 {
		limit = DomeAstrodomeDefaultResidentLimitBytes
	}
	addresses, envelopePathM, err := volume.domeAstrodomeNodeFootprintAddresses(
		ctx,
		observer,
		nodes,
		refraction.MaximumPathLengthM,
	)
	if err != nil {
		return nil, err
	}
	sourceColumnPlanDigest, err := domeAstrodomeSourceColumnPlanDigest(addresses, volume.manifest.Grid)
	if err != nil {
		return nil, fmt.Errorf("validate ICON-EU Astrodome source-column plan: %w", err)
	}
	projection := domeAstrodomeProjectedResidentBytes(len(addresses), len(volume.manifest.ModelSteps))
	if projection > limit {
		return nil, fmt.Errorf("ICON-EU Astrodome footprint projects %d bytes for %d columns, above %d-byte worker limit",
			projection, len(addresses), limit)
	}
	previousLimit := 0
	volume.mu.Lock()
	previousLimit = volume.cacheLimit
	if volume.cacheLimit < len(addresses) {
		volume.cacheLimit = len(addresses)
	}
	volume.mu.Unlock()
	rollback := func() {
		volume.mu.Lock()
		volume.cacheLimit = previousLimit
		volume.pruneDomeColumnCacheLocked()
		volume.mu.Unlock()
	}
	snapshot := &domePinnedColumnSnapshot{
		columns: make(map[string]forecast.AstrodomePrimitiveColumn, len(addresses)),
	}
	for offset := 0; offset < len(addresses); offset += DomeAstrodomePreloadBatchColumns {
		if err := ctx.Err(); err != nil {
			rollback()
			return nil, err
		}
		end := min(offset+DomeAstrodomePreloadBatchColumns, len(addresses))
		columns, extractErr := volume.extractDomeAstrodomeBatch(ctx, addresses[offset:end])
		if extractErr != nil {
			rollback()
			return nil, extractErr
		}
		volume.mu.Lock()
		for id, column := range columns {
			volume.clock++
			volume.cache[id] = domeVolumeCacheEntry{column: column, used: volume.clock}
			snapshot.columns[id] = column
		}
		volume.mu.Unlock()
	}
	if len(snapshot.columns) != len(addresses) {
		rollback()
		return nil, fmt.Errorf("ICON-EU Astrodome preload retained %d columns, want %d", len(snapshot.columns), len(addresses))
	}
	if !volume.pinnedColumns.CompareAndSwap(nil, snapshot) {
		rollback()
		return nil, errors.New("ICON-EU Astrodome volume already has a pinned footprint")
	}
	ids := make(map[string]struct{}, len(addresses))
	for _, address := range addresses {
		ids[address.id] = struct{}{}
	}
	return &DomeAstrodomeFootprint{
		volume: volume, columnIDs: ids, snapshot: snapshot, previousLimit: previousLimit,
		report: DomeAstrodomePreloadReport{
			UniqueColumns: len(addresses), ProjectedResidentBytes: projection,
			ExtractionBatches:      (len(addresses) + DomeAstrodomePreloadBatchColumns - 1) / DomeAstrodomePreloadBatchColumns,
			GridMarginColumns:      DomeAstrodomeRefractionGridMargin,
			EnvelopePathM:          envelopePathM,
			SourceColumnPlanDigest: sourceColumnPlanDigest,
		},
	}, nil
}

func domeAstrodomeSourceColumnPlanDigest(
	addresses []domeColumnAddress,
	grid model.Coverage,
) (string, error) {
	if len(addresses) == 0 || !finiteDomeVolume(grid.Increment) || grid.Increment <= 0 {
		return "", errors.New("source-column plan or regular grid is empty")
	}
	latitudeMaximum := int(math.Round((grid.MaxLat - grid.MinLat) / grid.Increment))
	longitudeMaximum := int(math.Round((grid.MaxLon - grid.MinLon) / grid.Increment))
	canonical := append([]domeColumnAddress(nil), addresses...)
	sort.Slice(canonical, func(i, j int) bool { return canonical[i].id < canonical[j].id })
	hash := sha256.New()
	_, _ = hash.Write([]byte("astrodome-native-source-column-plan-v1\n"))
	previousID := ""
	for _, address := range canonical {
		if address.latitudeIndex < 0 || address.latitudeIndex > latitudeMaximum ||
			address.longitudeIndex < 0 || address.longitudeIndex > longitudeMaximum {
			return "", fmt.Errorf("column %q has an out-of-grid source index", address.id)
		}
		expectedID := fmt.Sprintf("lat%04d-lon%04d", address.latitudeIndex, address.longitudeIndex)
		expectedLatitude := grid.MinLat + float64(address.latitudeIndex)*grid.Increment
		expectedLongitude := grid.MinLon + float64(address.longitudeIndex)*grid.Increment
		if address.id != expectedID || address.id == previousID ||
			math.Float64bits(address.location.Latitude) != math.Float64bits(expectedLatitude) ||
			math.Float64bits(address.location.Longitude) != math.Float64bits(expectedLongitude) {
			return "", fmt.Errorf("column %q is not the exact canonical native-grid address", address.id)
		}
		previousID = address.id
		_, _ = fmt.Fprintf(
			hash,
			"%s|%d|%d|%016x|%016x\n",
			address.id,
			address.latitudeIndex,
			address.longitudeIndex,
			math.Float64bits(address.location.Latitude),
			math.Float64bits(address.location.Longitude),
		)
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), nil
}

func (volume *DomeVolume) domeAstrodomeNodeFootprintAddresses(
	ctx context.Context,
	observer forecast.Location,
	nodes []forecast.AstrodomeGridNode,
	maximumPathM float64,
) ([]domeColumnAddress, float64, error) {
	if !finiteDomeVolume(maximumPathM) || maximumPathM <= 0 {
		return nil, 0, errors.New("ICON-EU Astrodome footprint path bound is invalid")
	}
	maximumTopM, err := volume.domeAstrodomeMaximumTopHeight(ctx)
	if err != nil {
		return nil, 0, err
	}
	rays, envelopePathM, err := domeAstrodomeCanonicalEnvelopeRays(observer, nodes, maximumTopM, maximumPathM)
	if err != nil {
		return nil, 0, err
	}
	addresses, err := volume.domeAstrodomeCorridorAddresses(ctx, observer, rays, false)
	if err != nil {
		return nil, 0, err
	}
	return addresses, envelopePathM, nil
}

func domeAstrodomeCanonicalEnvelopeRays(
	observer forecast.Location,
	nodes []forecast.AstrodomeGridNode,
	maximumTopM,
	maximumPathM float64,
) ([]domeAstrodomeEnvelopeRay, float64, error) {
	if len(nodes) == 0 || !finiteDomeVolume(maximumTopM) || !finiteDomeVolume(maximumPathM) || maximumPathM <= 0 {
		return nil, 0, errors.New("ICON-EU Astrodome canonical envelope input is invalid")
	}
	envelopePathM := 0.0
	rays := make([]domeAstrodomeEnvelopeRay, 0, len(nodes))
	for _, node := range nodes {
		ray, rayErr := forecast.NewAstrodomeRay(observer, 0, node.ElevationDegrees, node.AzimuthDegrees)
		if rayErr != nil {
			return nil, 0, rayErr
		}
		top, topErr := ray.IntersectAltitude(maximumTopM + DomeAstrodomeTopGuardM)
		if topErr != nil {
			return nil, 0, topErr
		}
		pathLimitM := math.Min(maximumPathM, top.PathLengthM+DomeAstrodomePathGuardM)
		envelopePathM = math.Max(envelopePathM, pathLimitM)
		azimuth := cloneDomeAstrodomeAzimuth(node.AzimuthDegrees)
		rays = append(rays, domeAstrodomeEnvelopeRay{
			elevationDegrees: node.ElevationDegrees,
			azimuthDegrees:   azimuth,
			pathLimitM:       pathLimitM,
		})
	}
	return rays, envelopePathM, nil
}

func (volume *DomeVolume) domeAstrodomeCorridorAddresses(
	ctx context.Context,
	observer forecast.Location,
	rays []domeAstrodomeEnvelopeRay,
	clipAtDomain bool,
) ([]domeColumnAddress, error) {
	latMax := int(math.Round((volume.manifest.Grid.MaxLat - volume.manifest.Grid.MinLat) / volume.manifest.Grid.Increment))
	lonMax := int(math.Round((volume.manifest.Grid.MaxLon - volume.manifest.Grid.MinLon) / volume.manifest.Grid.Increment))
	addresses := make(map[string]domeColumnAddress)
	for rayIndex, envelope := range rays {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !finiteDomeVolume(envelope.pathLimitM) || envelope.pathLimitM <= 0 {
			return nil, fmt.Errorf("ICON-EU Astrodome envelope ray %d has an invalid path bound", rayIndex)
		}
		ray, err := forecast.NewAstrodomeRay(observer, 0, envelope.elevationDegrees, envelope.azimuthDegrees)
		if err != nil {
			return nil, err
		}
		steps := int(math.Ceil(envelope.pathLimitM / DomeAstrodomeEnvelopeSampleM))
		for step := 0; step <= steps; step++ {
			pathM := math.Min(envelope.pathLimitM, float64(step)*DomeAstrodomeEnvelopeSampleM)
			point, pointErr := ray.PointAtPathLength(pathM)
			if pointErr != nil {
				return nil, pointErr
			}
			if !volume.manifest.Grid.Contains(point.Location) {
				if clipAtDomain {
					break
				}
				azimuth := "zenith"
				if envelope.azimuthDegrees != nil {
					azimuth = fmt.Sprintf("%.6f degrees", *envelope.azimuthDegrees)
				}
				return nil, fmt.Errorf("ICON-EU Astrodome physical preload envelope exits the provider domain at elevation %.6f degrees, azimuth %s",
					envelope.elevationDegrees, azimuth)
			}
			cell, cellErr := domeGridCell(volume.manifest.Grid, point.Location)
			if cellErr != nil {
				return nil, cellErr
			}
			for latitudeIndex := max(0, cell.south-DomeAstrodomeRefractionGridMargin); latitudeIndex <= min(latMax, cell.north+DomeAstrodomeRefractionGridMargin); latitudeIndex++ {
				for longitudeIndex := max(0, cell.west-DomeAstrodomeRefractionGridMargin); longitudeIndex <= min(lonMax, cell.east+DomeAstrodomeRefractionGridMargin); longitudeIndex++ {
					address := volume.domeColumnAddress(latitudeIndex, longitudeIndex)
					addresses[address.id] = address
				}
			}
		}
	}
	result := make([]domeColumnAddress, 0, len(addresses))
	for _, address := range addresses {
		result = append(result, address)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].id < result[j].id })
	return result, nil
}

func (volume *DomeVolume) domeAstrodomeMaximumTopHeight(
	ctx context.Context,
) (float64, error) {
	if volume == nil || volume.runner == nil {
		return 0, errors.New("ICON-EU Astrodome volume runner is required")
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	geometrySource := domeVolumeSource{
		relative: volume.manifest.Geometry.File, bytes: volume.manifest.Geometry.Bytes,
		messages: volume.manifest.Geometry.Messages,
	}
	geometryPath, err := volume.resolveDomeVolumeSource(geometrySource)
	if err != nil {
		return 0, err
	}
	// This is an exact discrete maximum of native HHL1 over the published
	// ICON-EU grid, used only to size the immutable storage footprint. It does
	// not enter the ray or science equations; every node still resolves and
	// interpolates its own native HHL surfaces during integration.
	output, err := volume.runner.CombinedOutput(ctx, "cdo",
		"-s", "--precision", "12", "-outputtab,value", "-fldmax", "-sellevel,1", "-selname,HHL", geometryPath)
	if err != nil {
		return 0, fmt.Errorf("extract ICON-EU Astrodome global HHL1 maximum: %w", err)
	}
	maximumTopM, err := parseDomeAstrodomeScalar(output)
	if err != nil {
		return 0, fmt.Errorf("parse ICON-EU Astrodome global HHL1 maximum: %w", err)
	}
	if !finiteDomeVolume(maximumTopM) || maximumTopM <= 0 || maximumTopM > 200_000 {
		return 0, fmt.Errorf("ICON-EU Astrodome maximum HHL1 envelope %.12g m is outside (0, 200000] m", maximumTopM)
	}
	return maximumTopM, nil
}

func parseDomeAstrodomeScalar(output []byte) (float64, error) {
	var values []float64
	scanner := bufio.NewScanner(strings.NewReader(string(output)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		for _, token := range strings.Fields(line) {
			value, err := strconv.ParseFloat(token, 64)
			if err != nil {
				return 0, fmt.Errorf("unexpected non-numeric token %q", token)
			}
			values = append(values, value)
		}
	}
	if err := scanner.Err(); err != nil {
		return 0, err
	}
	if len(values) != 1 || !finiteDomeVolume(values[0]) {
		return 0, fmt.Errorf("expected one finite scalar, got %d", len(values))
	}
	return values[0], nil
}

func cloneDomeAstrodomeAzimuth(value *float64) *float64 {
	if value == nil {
		return nil
	}
	result := *value
	return &result
}

func domeAstrodomeProjectedResidentBytes(columnCount, frameCount int) uint64 {
	perColumn := uint64(unsafe.Sizeof(forecast.AstrodomePrimitiveColumn{})) +
		uint64(domeHalfLevelCount)*uint64(unsafe.Sizeof(forecast.AstrodomeHalfLevelGeometry{})) +
		uint64(frameCount)*(uint64(unsafe.Sizeof(forecast.AstrodomePrimitiveColumnFrame{}))+
			uint64(domeFullLevelCount)*uint64(unsafe.Sizeof(forecast.AstrodomeFullLevelPrimitives{}))+
			uint64(domeHalfLevelCount)*uint64(unsafe.Sizeof(forecast.AstrodomeHalfLevelPrimitives{})))
	// Map buckets, strings, slice slack, and allocator size classes are
	// explicitly admitted with a conservative 3/2 overhead factor.
	return uint64(columnCount) * (perColumn*3/2 + 512)
}

// extractDomeAstrodomeBatch is the multipoint counterpart of the public
// four-column lazy extractor. One CDO pass is made for geometry and one for
// each native timestamp, independent of the number of points in this batch.
func (volume *DomeVolume) extractDomeAstrodomeBatch(
	ctx context.Context,
	addresses []domeColumnAddress,
) (map[string]forecast.AstrodomePrimitiveColumn, error) {
	if len(addresses) < 1 || len(addresses) > DomeAstrodomePreloadBatchColumns {
		return nil, errors.New("ICON-EU Astrodome preload batch has invalid cardinality")
	}
	if err := volume.ensureDomeVolumeIdentity(); err != nil {
		return nil, err
	}
	points := make([]batchPoint, len(addresses))
	seen := make(map[string]struct{}, len(addresses))
	for index, address := range addresses {
		if _, duplicate := seen[address.id]; duplicate {
			return nil, fmt.Errorf("ICON-EU Astrodome preload repeats column %q", address.id)
		}
		seen[address.id] = struct{}{}
		points[index] = batchPoint{Latitude: address.location.Latitude, Longitude: address.location.Longitude}
	}
	geometrySource := domeVolumeSource{
		relative: volume.manifest.Geometry.File, bytes: volume.manifest.Geometry.Bytes,
		messages: volume.manifest.Geometry.Messages,
	}
	geometryPath, err := volume.resolveDomeVolumeSource(geometrySource)
	if err != nil {
		return nil, err
	}
	remapPlan, err := volume.prepareDomeRemapPlan(ctx, geometryPath, points)
	if err != nil {
		return nil, err
	}
	defer func() { _ = remapPlan.Close() }()
	geometryValues, err := volume.extractDomeSourcesWithRemapPlan(ctx, []string{geometryPath}, points, domeHalfLevelCount, remapPlan)
	if err != nil {
		return nil, fmt.Errorf("extract ICON-EU Astrodome preload geometry: %w", err)
	}
	columns := make(map[string]forecast.AstrodomePrimitiveColumn, len(addresses))
	for index, address := range addresses {
		geometry, hsurf, parseErr := domeGeometryFromValues(geometryValues[index])
		if parseErr != nil {
			return nil, fmt.Errorf("ICON-EU Astrodome preload column %q geometry: %w", address.id, parseErr)
		}
		columns[address.id] = forecast.AstrodomePrimitiveColumn{
			ColumnID: address.id, Location: address.location, HSURFHeightM: hsurf,
			HalfLevelGeometry: geometry,
			Frames:            make([]forecast.AstrodomePrimitiveColumnFrame, 0, len(volume.manifest.ModelSteps)),
		}
	}
	type stepResult struct {
		frames []forecast.AstrodomePrimitiveColumnFrame
	}
	steps := volume.manifest.ModelSteps
	results := make([]stepResult, len(steps))
	workers := volume.extractionWorkers
	if workers < 1 {
		workers = 1
	}
	workers = min(workers, len(steps))
	workContext, cancel := context.WithCancel(ctx)
	defer cancel()
	tasks := make(chan int, len(steps))
	firstError := make(chan error, 1)
	for index := range steps {
		tasks <- index
	}
	close(tasks)
	var wait sync.WaitGroup
	wait.Add(workers)
	for range workers {
		go func() {
			defer wait.Done()
			for stepIndex := range tasks {
				if workContext.Err() != nil {
					return
				}
				frames, extractErr := volume.extractDomeAstrodomeStep(
					workContext, steps[stepIndex], points, addresses, remapPlan,
				)
				results[stepIndex] = stepResult{frames: frames}
				if extractErr != nil {
					select {
					case firstError <- extractErr:
					default:
					}
					cancel()
					return
				}
			}
		}()
	}
	wait.Wait()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	select {
	case err := <-firstError:
		return nil, err
	default:
	}
	for stepIndex, result := range results {
		if len(result.frames) != len(addresses) {
			return nil, fmt.Errorf("ICON-EU Astrodome preload f%03d returned %d columns, want %d",
				steps[stepIndex].ForecastHour, len(result.frames), len(addresses))
		}
		for addressIndex, address := range addresses {
			column := columns[address.id]
			column.Frames = append(column.Frames, result.frames[addressIndex])
			columns[address.id] = column
		}
	}
	for columnID, column := range columns {
		if err := validateDomeAstrodomeImmutableColumn(column, volume.manifest.ModelSteps); err != nil {
			return nil, fmt.Errorf("validate ICON-EU Astrodome immutable preload column %q: %w", columnID, err)
		}
	}
	if err := volume.ensureDomeVolumeIdentity(); err != nil {
		return nil, err
	}
	return columns, nil
}

func (volume *DomeVolume) extractDomeAstrodomeStep(
	ctx context.Context,
	step DomeModelStep,
	points []batchPoint,
	addresses []domeColumnAddress,
	remapPlan *batchRemapPlan,
) ([]forecast.AstrodomePrimitiveColumnFrame, error) {
	sources := make([]string, 0, len(step.Parts)+1)
	expectedMessages := 0
	for _, part := range step.Parts {
		path, err := volume.resolveDomeVolumeSource(domeVolumeSource{
			relative: part.File, bytes: part.Bytes, messages: part.Messages,
		})
		if err != nil {
			return nil, err
		}
		sources = append(sources, path)
		expectedMessages += part.Messages
	}
	surfaceSource, ok := volume.surfaceFiles[step.ForecastHour]
	if !ok {
		return nil, fmt.Errorf("ICON-EU Astrodome preload surface f%03d is absent", step.ForecastHour)
	}
	surfacePath, err := volume.resolveDomeVolumeSource(surfaceSource)
	if err != nil {
		return nil, err
	}
	sources = append(sources, surfacePath)
	expectedMessages += surfaceSource.messages
	values, err := volume.extractDomeSourcesWithRemapPlan(ctx, sources, points, expectedMessages, remapPlan)
	if err != nil {
		return nil, fmt.Errorf("extract ICON-EU Astrodome preload f%03d: %w", step.ForecastHour, err)
	}
	frames := make([]forecast.AstrodomePrimitiveColumnFrame, len(addresses))
	for index, address := range addresses {
		frame, parseErr := domeFrameFromValues(values[index], step.ValidAt.UTC(), volume.manifest.BaseTime.UTC())
		if parseErr != nil {
			return nil, fmt.Errorf("ICON-EU Astrodome preload column %q f%03d: %w", address.id, step.ForecastHour, parseErr)
		}
		frames[index] = frame
	}
	return frames, nil
}

func validateDomeAstrodomeImmutableColumn(
	column forecast.AstrodomePrimitiveColumn,
	steps []DomeModelStep,
) error {
	if strings.TrimSpace(column.ColumnID) == "" || len(column.HalfLevelGeometry) != domeHalfLevelCount ||
		len(column.Frames) != len(steps) || column.HSURFHeightM != column.HalfLevelGeometry[len(column.HalfLevelGeometry)-1].HeightM {
		return errors.New("column identity, HHL geometry, or native frame cardinality is inconsistent")
	}
	for index, geometry := range column.HalfLevelGeometry {
		if geometry.ModelHalfLevel != index+1 || !finiteDomeVolume(geometry.HeightM) ||
			(index > 0 && geometry.HeightM >= column.HalfLevelGeometry[index-1].HeightM) {
			return fmt.Errorf("HHL geometry index %d is invalid", index)
		}
	}
	requiredSurface := forecast.AstrodomePrimitiveFieldSet(forecast.AstrodomePrimitiveSurfacePressure) |
		forecast.AstrodomePrimitiveFieldSet(forecast.AstrodomePrimitiveTemperature2M) |
		forecast.AstrodomePrimitiveFieldSet(forecast.AstrodomePrimitiveSpecificHumidity2M)
	for index, frame := range column.Frames {
		if !frame.ValidAt.Equal(steps[index].ValidAt.UTC()) || len(frame.FullLevels) != domeFullLevelCount ||
			len(frame.HalfLevels) != domeHalfLevelCount {
			return fmt.Errorf("native frame %d identity or vertical cardinality is inconsistent", index)
		}
		if frame.Surface.Available&requiredSurface != requiredSurface ||
			!finiteDomeVolume(frame.Surface.SurfacePressurePa) || frame.Surface.SurfacePressurePa <= 0 ||
			frame.Surface.SurfacePressurePa > 200_000 ||
			!finiteDomeVolume(frame.Surface.Temperature2MK) || frame.Surface.Temperature2MK < 150 ||
			frame.Surface.Temperature2MK > 400 ||
			!finiteDomeVolume(frame.Surface.SpecificHumidity2MKgKg) || frame.Surface.SpecificHumidity2MKgKg < 0 ||
			frame.Surface.SpecificHumidity2MKgKg >= 1 {
			return fmt.Errorf("native frame %d lacks the physical PS/T2M/QV_2M refraction boundary", index)
		}
		if err := forecast.ValidateAstrodomeNativePressureOrdering(frame.FullLevels); err != nil {
			return fmt.Errorf("native frame %d pressure profile: %w", index, err)
		}
		if err := forecast.ValidateAstrodomeNativeSurfacePressureBoundary(frame.FullLevels, frame.Surface); err != nil {
			return fmt.Errorf("native frame %d pressure boundary: %w", index, err)
		}
		for levelIndex, level := range frame.FullLevels {
			if level.ModelLevel != levelIndex+1 || level.Available&domeFullAvailable != domeFullAvailable {
				return fmt.Errorf("native frame %d full level %d is incomplete", index, levelIndex+1)
			}
		}
		for levelIndex, level := range frame.HalfLevels {
			if level.ModelHalfLevel != levelIndex+1 || level.Available&domeHalfAvailable != domeHalfAvailable {
				return fmt.Errorf("native frame %d half level %d is incomplete", index, levelIndex+1)
			}
		}
	}
	return nil
}
