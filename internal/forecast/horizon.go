package forecast

import (
	"context"
	"fmt"
	"math"
	"sort"
	"time"
)

const (
	// HorizonAlgorithmVersion identifies the published fast spherical
	// straight-line-of-sight product. It is intentionally distinct from the
	// full-refraction Astrodome kernel and from the retired Horizon v7 cache
	// identity, even though it retains the validated v7 equations.
	HorizonAlgorithmVersion = "horizon-spherical-straight-los-native-mh-logp-glo30-informational-v13"
	HorizonGridProfile      = "horizon-8x10deg-straight-glo30-informational-v5"

	HorizonEarthRadiusM              = 6371008.8
	HorizonGeometricElevationDegrees = 10.0
	HorizonAtmosphereTopM            = 22300.0
	// A 500 m ground-track step changes ray height by about 87 m at 10°.
	// Horizontal model lookups still deduplicate to ICON's ~7 km cells, while
	// the finer quadrature resolves steep near-surface TKE/cloud gradients.
	HorizonSurfaceSegmentLengthM       = 500.0
	HorizonDirectionCount              = 8
	horizonMinimumCompletePathRatio    = 1 - 1e-9
	horizonMaximumDataQualityHeuristic = 0.85
	horizonLimitedDataQualityHeuristic = 0.60
	horizonUsableLeadQualityHeuristic  = 0.75
	horizonGoodLeadQualityHeuristic    = 0.85
	horizonCoordinateComparisonEpsilon = 1e-8
	horizonObserverElevationToleranceM = 1.0
)

// HorizonDirection is one of the eight fixed compass sectors. Values are
// deliberately short because they are also suitable as chart labels.
type HorizonDirection string

const (
	HorizonNorth     HorizonDirection = "N"
	HorizonNorthEast HorizonDirection = "NE"
	HorizonEast      HorizonDirection = "E"
	HorizonSouthEast HorizonDirection = "SE"
	HorizonSouth     HorizonDirection = "S"
	HorizonSouthWest HorizonDirection = "SW"
	HorizonWest      HorizonDirection = "W"
	HorizonNorthWest HorizonDirection = "NW"
)

var fixedHorizonDirections = [HorizonDirectionCount]struct {
	direction HorizonDirection
	azimuth   float64
}{
	{HorizonNorth, 0},
	{HorizonNorthEast, 45},
	{HorizonEast, 90},
	{HorizonSouthEast, 135},
	{HorizonSouth, 180},
	{HorizonSouthWest, 225},
	{HorizonWest, 270},
	{HorizonNorthWest, 315},
}

// HorizonDirections returns the canonical N, NE, E, SE, S, SW, W, NW order.
func HorizonDirections() []HorizonDirection {
	result := make([]HorizonDirection, len(fixedHorizonDirections))
	for index, fixed := range fixedHorizonDirections {
		result[index] = fixed.direction
	}
	return result
}

// HorizonSample describes one midpoint-quadrature segment. Surface distances
// follow the spherical ground track; LOSPathLengthM is the straight ray length
// between those two surface-distance boundaries.
type HorizonSample struct {
	StartSurfaceDistanceM float64  `json:"start_surface_distance_m"`
	EndSurfaceDistanceM   float64  `json:"end_surface_distance_m"`
	LOSPathLengthM        float64  `json:"los_path_length_m"`
	Midpoint              Location `json:"midpoint"`
	RayHeightM            float64  `json:"ray_height_m"` // absolute MSL height
}

type HorizonDirectionPlan struct {
	Direction      HorizonDirection `json:"direction"`
	AzimuthDegrees float64          `json:"azimuth_degrees"`
	Samples        []HorizonSample  `json:"samples"`
}

// HorizonPlan is provider-neutral geometry. Model adapters only need to fetch
// the observer and every midpoint returned by FootprintLocations.
type HorizonPlan struct {
	AlgorithmVersion          string                 `json:"algorithm_version"`
	Observer                  Location               `json:"observer"`
	ObserverSurfaceElevationM float64                `json:"observer_surface_elevation_m"`
	GeometricElevationDegrees float64                `json:"geometric_elevation_degrees"`
	AtmosphereTopM            float64                `json:"atmosphere_top_m"`
	SurfaceSegmentLengthM     float64                `json:"surface_segment_length_m"`
	Directions                []HorizonDirectionPlan `json:"directions"`
}

// NewHorizonPlan builds the published straight-ray Horizon plan:
// eight azimuths at geometric elevation 10 degrees, with 0.5 km
// surface-distance boundaries up to the straight-ray intersection with
// 22.3 km absolute altitude.
func NewHorizonPlan(observer Location, observerSurfaceElevationM float64) (HorizonPlan, error) {
	return newHorizonPlan(observer, observerSurfaceElevationM, HorizonSurfaceSegmentLengthM, HorizonAtmosphereTopM)
}

func newHorizonPlan(observer Location, observerSurfaceElevationM, surfaceStepM, atmosphereTopM float64) (HorizonPlan, error) {
	if err := ValidateCoordinates(observer.Latitude, observer.Longitude); err != nil {
		return HorizonPlan{}, fmt.Errorf("horizon observer: %w", err)
	}
	// Geographic azimuth has no unique meaning at an exact pole. Near-polar
	// sites remain supported by the vector-safe great-circle formula below.
	if math.Abs(observer.Latitude) == 90 {
		return HorizonPlan{}, fmt.Errorf("horizon azimuth is degenerate at an exact geographic pole")
	}
	if !finite(observerSurfaceElevationM) || !finite(atmosphereTopM) || observerSurfaceElevationM >= atmosphereTopM ||
		observerSurfaceElevationM <= -HorizonEarthRadiusM {
		return HorizonPlan{}, fmt.Errorf("horizon observer elevation and atmosphere top must be finite and ordered")
	}
	if !finite(surfaceStepM) || surfaceStepM <= 0 {
		return HorizonPlan{}, fmt.Errorf("horizon surface segment length must be positive")
	}
	observer.Longitude = normalizeHorizonLongitude(observer.Longitude)
	endLOS, endAngle, err := horizonRayIntersection(observerSurfaceElevationM, atmosphereTopM)
	if err != nil {
		return HorizonPlan{}, err
	}
	endSurfaceDistanceM := HorizonEarthRadiusM * endAngle
	plan := HorizonPlan{
		AlgorithmVersion:          HorizonAlgorithmVersion,
		Observer:                  observer,
		ObserverSurfaceElevationM: observerSurfaceElevationM,
		GeometricElevationDegrees: HorizonGeometricElevationDegrees,
		AtmosphereTopM:            atmosphereTopM,
		SurfaceSegmentLengthM:     surfaceStepM,
		Directions:                make([]HorizonDirectionPlan, len(fixedHorizonDirections)),
	}
	for directionIndex, fixed := range fixedHorizonDirections {
		direction := HorizonDirectionPlan{Direction: fixed.direction, AzimuthDegrees: fixed.azimuth}
		for startDistanceM := 0.0; startDistanceM < endSurfaceDistanceM; startDistanceM += surfaceStepM {
			endDistanceM := math.Min(startDistanceM+surfaceStepM, endSurfaceDistanceM)
			startLOS, err := horizonLOSPathAtSurfaceDistance(observerSurfaceElevationM, startDistanceM)
			if err != nil {
				return HorizonPlan{}, err
			}
			endSegmentLOS := endLOS
			if endDistanceM < endSurfaceDistanceM {
				endSegmentLOS, err = horizonLOSPathAtSurfaceDistance(observerSurfaceElevationM, endDistanceM)
				if err != nil {
					return HorizonPlan{}, err
				}
			}
			midpointLOS := (startLOS + endSegmentLOS) / 2
			midpointAngle := horizonCentralAngleAtLOS(observerSurfaceElevationM, midpointLOS)
			direction.Samples = append(direction.Samples, HorizonSample{
				StartSurfaceDistanceM: startDistanceM,
				EndSurfaceDistanceM:   endDistanceM,
				LOSPathLengthM:        endSegmentLOS - startLOS,
				Midpoint:              horizonDestination(observer, fixed.azimuth, midpointAngle),
				RayHeightM:            horizonRayHeightAtLOS(observerSurfaceElevationM, midpointLOS),
			})
		}
		plan.Directions[directionIndex] = direction
	}
	return plan, nil
}

// HorizonSurfaceDistanceAtHeight returns the exact spherical ground distance
// beneath the 10-degree ray when it reaches target absolute MSL height.
func HorizonSurfaceDistanceAtHeight(observerSurfaceElevationM, targetHeightM float64) (float64, error) {
	_, angle, err := horizonRayIntersection(observerSurfaceElevationM, targetHeightM)
	if err != nil {
		return 0, err
	}
	return HorizonEarthRadiusM * angle, nil
}

func horizonRayIntersection(observerSurfaceElevationM, targetHeightM float64) (losPathM, centralAngleRadians float64, err error) {
	if !finite(observerSurfaceElevationM) || !finite(targetHeightM) || targetHeightM <= observerSurfaceElevationM ||
		observerSurfaceElevationM <= -HorizonEarthRadiusM {
		return 0, 0, fmt.Errorf("horizon ray target height must be above the observer")
	}
	elevation := HorizonGeometricElevationDegrees * math.Pi / 180
	observerRadius := HorizonEarthRadiusM + observerSurfaceElevationM
	targetRadius := HorizonEarthRadiusM + targetHeightM
	discriminant := targetRadius*targetRadius - observerRadius*observerRadius*math.Cos(elevation)*math.Cos(elevation)
	if discriminant <= 0 || !finite(discriminant) {
		return 0, 0, fmt.Errorf("horizon ray does not intersect the requested atmosphere top")
	}
	losPathM = -observerRadius*math.Sin(elevation) + math.Sqrt(discriminant)
	if losPathM <= 0 || !finite(losPathM) {
		return 0, 0, fmt.Errorf("horizon ray intersection is not forward of the observer")
	}
	centralAngleRadians = horizonCentralAngleAtLOS(observerSurfaceElevationM, losPathM)
	return losPathM, centralAngleRadians, nil
}

func horizonLOSPathAtSurfaceDistance(observerSurfaceElevationM, surfaceDistanceM float64) (float64, error) {
	if !finite(observerSurfaceElevationM) || observerSurfaceElevationM <= -HorizonEarthRadiusM ||
		!finite(surfaceDistanceM) || surfaceDistanceM < 0 {
		return 0, fmt.Errorf("invalid horizon surface distance")
	}
	angle := surfaceDistanceM / HorizonEarthRadiusM
	elevation := HorizonGeometricElevationDegrees * math.Pi / 180
	denominator := math.Cos(elevation + angle)
	if denominator <= 0 {
		return 0, fmt.Errorf("horizon surface distance lies beyond the forward ray")
	}
	return (HorizonEarthRadiusM + observerSurfaceElevationM) * math.Sin(angle) / denominator, nil
}

func horizonCentralAngleAtLOS(observerSurfaceElevationM, losPathM float64) float64 {
	elevation := HorizonGeometricElevationDegrees * math.Pi / 180
	observerRadius := HorizonEarthRadiusM + observerSurfaceElevationM
	return math.Atan2(losPathM*math.Cos(elevation), observerRadius+losPathM*math.Sin(elevation))
}

func horizonRayHeightAtLOS(observerSurfaceElevationM, losPathM float64) float64 {
	elevation := HorizonGeometricElevationDegrees * math.Pi / 180
	observerRadius := HorizonEarthRadiusM + observerSurfaceElevationM
	radius := math.Sqrt(observerRadius*observerRadius + losPathM*losPathM + 2*observerRadius*losPathM*math.Sin(elevation))
	return radius - HorizonEarthRadiusM
}

func horizonDestination(origin Location, azimuthDegrees, centralAngleRadians float64) Location {
	latitude := origin.Latitude * math.Pi / 180
	longitude := origin.Longitude * math.Pi / 180
	azimuth := azimuthDegrees * math.Pi / 180
	sinLatitude := math.Sin(latitude)*math.Cos(centralAngleRadians) +
		math.Cos(latitude)*math.Sin(centralAngleRadians)*math.Cos(azimuth)
	destinationLatitude := math.Asin(clamp(sinLatitude, -1, 1))
	destinationLongitude := longitude + math.Atan2(
		math.Sin(azimuth)*math.Sin(centralAngleRadians)*math.Cos(latitude),
		math.Cos(centralAngleRadians)-math.Sin(latitude)*math.Sin(destinationLatitude),
	)
	return Location{
		Latitude:  destinationLatitude * 180 / math.Pi,
		Longitude: normalizeHorizonLongitude(destinationLongitude * 180 / math.Pi),
		TimeZone:  origin.TimeZone,
	}
}

func normalizeHorizonLongitude(longitude float64) float64 {
	longitude = math.Mod(longitude+180, 360)
	if longitude < 0 {
		longitude += 360
	}
	return longitude - 180
}

// FootprintLocations returns precisely the model lookup points: the observer
// followed by every segment midpoint. Segment endpoints are geometry only and
// must not make an otherwise covered provider footprint fail.
func (plan HorizonPlan) FootprintLocations() []Location {
	locations := make([]Location, 0, 1+horizonSampleCount(plan))
	locations = append(locations, plan.Observer)
	for _, direction := range plan.Directions {
		for _, sample := range direction.Samples {
			locations = append(locations, sample.Midpoint)
		}
	}
	return locations
}

func horizonSampleCount(plan HorizonPlan) int {
	count := 0
	for _, direction := range plan.Directions {
		count += len(direction.Samples)
	}
	return count
}

// HorizonFootprintCovered checks all and only actual lookup points. A method
// value such as model.Coverage.Contains can be passed directly without adding
// a forecast-to-model package dependency.
func HorizonFootprintCovered(plan HorizonPlan, contains func(Location) bool) bool {
	if contains == nil {
		return false
	}
	if err := validateHorizonPlan(plan); err != nil {
		return false
	}
	for _, location := range plan.FootprintLocations() {
		if !contains(location) {
			return false
		}
	}
	return true
}

// HorizonSampleSnapshot is the provider-neutral value bundle for one plan
// midpoint. Frames retain their ordinary normalized forecast shapes, making
// the adapter a direct spatial lookup rather than a second forecast model.
type HorizonSampleSnapshot struct {
	Vertical          VerticalFrame `json:"vertical"`
	Surface           SurfaceFrame  `json:"surface"`
	Cloud             CloudFrame    `json:"cloud"`
	SurfaceElevationM float64       `json:"surface_elevation_m"` // coarse model HHL surface
	// HorizontalCellID is an opaque snapshot-local identifier supplied by the
	// model adapter. It prevents sub-grid cloud cover from being applied once
	// per quadrature segment when many segments share one model cell.
	HorizontalCellID int `json:"horizontal_cell_id"`
}

type HorizonDirectionSnapshot struct {
	Direction HorizonDirection        `json:"direction"`
	Samples   []HorizonSampleSnapshot `json:"samples"`
}

// HorizonSnapshot contains a single common valid time. Observer surface data
// is intentionally separate: fog and operational wind are local to the site,
// never borrowed from remote points along a direction.
type HorizonSnapshot struct {
	ValidAt                   time.Time                  `json:"valid_at"`
	ObserverSurface           SurfaceFrame               `json:"observer_surface"`
	ObserverSurfaceElevationM float64                    `json:"observer_surface_elevation_m"`
	Directions                []HorizonDirectionSnapshot `json:"directions"`
}

type HorizonDataQuality string

const (
	HorizonDataUnavailable HorizonDataQuality = "unavailable"
	HorizonDataLimited     HorizonDataQuality = "limited"
	HorizonDataUsable      HorizonDataQuality = "usable"
	HorizonDataGood        HorizonDataQuality = "good"
)

type HorizonLimitingFactor string

const (
	HorizonFactorNone          HorizonLimitingFactor = "none"
	HorizonFactorUnavailable   HorizonLimitingFactor = "unavailable_data"
	HorizonFactorTerrain       HorizonLimitingFactor = "model_terrain"
	HorizonFactorCloud         HorizonLimitingFactor = "cloud"
	HorizonFactorSeeing        HorizonLimitingFactor = "seeing"
	HorizonFactorCoherence     HorizonLimitingFactor = "coherence_time"
	HorizonFactorFog           HorizonLimitingFactor = "fog"
	HorizonFactorSurfaceWind   HorizonLimitingFactor = "surface_wind"
	HorizonFactorPrecipitation HorizonLimitingFactor = "precipitation"
)

type HorizonTerrainAssessment string

const (
	HorizonTerrainModelHHL HorizonTerrainAssessment = "icon_hhl_model_surface"
	HorizonTerrainGLO30HHL HorizonTerrainAssessment = "copernicus_dem_glo30_2021_and_icon_hhl"
)

type HorizonResult struct {
	ValidAt                                          time.Time                `json:"valid_at"`
	Direction                                        HorizonDirection         `json:"direction"`
	AzimuthDegrees                                   float64                  `json:"azimuth_degrees"`
	GeometricElevationDegrees                        float64                  `json:"geometric_elevation_degrees"`
	Index                                            float64                  `json:"index"`
	SeeingArcsec                                     float64                  `json:"seeing_arcsec"`
	CoherenceTimeMS                                  float64                  `json:"coherence_time_ms"`
	CoherenceTimeUnbounded                           bool                     `json:"coherence_time_unbounded"`
	IntegratedCn2                                    float64                  `json:"integrated_cn2"`
	WindWeightedCn2                                  float64                  `json:"wind_weighted_cn2"`
	CloudOpticalDepth                                float64                  `json:"cloud_optical_depth"`
	CloudTransmission                                float64                  `json:"cloud_transmission"` // fraction 0..1
	CloudTransmissionPercent                         float64                  `json:"cloud_transmission_percent"`
	CloudUnresolvedGuard                             bool                     `json:"cloud_unresolved_guard"`
	FogHeuristic                                     int                      `json:"fog_heuristic"`
	HighFogHeuristic                                 bool                     `json:"high_fog_heuristic"`
	TerrainBlocked                                   bool                     `json:"terrain_blocked"`
	TerrainAssessment                                HorizonTerrainAssessment `json:"terrain_assessment"`
	TerrainSkylineAvailable                          bool                     `json:"terrain_skyline_available"`
	TerrainSectorMeanElevationDegrees                float64                  `json:"terrain_sector_mean_elevation_degrees"`
	TerrainSectorMaximumElevationDegrees             float64                  `json:"terrain_sector_maximum_elevation_degrees"`
	TerrainSectorHasObstructionAtEvaluationElevation bool                     `json:"terrain_sector_has_obstruction_at_evaluation_elevation"`
	Available                                        bool                     `json:"available"`
	ResolvedPathFraction                             float64                  `json:"resolved_path_fraction"`
	PathCoverage                                     float64                  `json:"path_coverage"`
	TurbulenceProfileCoverage                        float64                  `json:"turbulence_profile_coverage"`
	CloudProfileCoverage                             float64                  `json:"cloud_profile_coverage"`
	LeadTimeQualityHeuristic                         float64                  `json:"lead_time_quality_heuristic"`
	ResolvableDirectionFraction                      float64                  `json:"resolvable_direction_fraction"`
	DataQualityHeuristic                             float64                  `json:"data_quality_heuristic"`
	DataQuality                                      HorizonDataQuality       `json:"data_quality"`
	LimitingFactor                                   HorizonLimitingFactor    `json:"limiting_factor"`
	LimitingFactors                                  []HorizonLimitingFactor  `json:"limiting_factors"`
}

// HorizonFrame is the eight-direction result for one forecast hour. A normal
// user-visible straight-ray Horizon series contains the immutable ICON-EU
// model-run period f001..f072. The missing f000 term is not synthesised because
// it has no preceding physical one-hour precipitation interval.
type HorizonFrame struct {
	ValidAt time.Time       `json:"valid_at"`
	Results []HorizonResult `json:"results"`
}

type horizonDirectionComputation struct {
	result                 HorizonResult
	weightedLeadQuality    float64
	resolvedPathM          float64
	turbulenceQualityPathM float64
	cloudQualityPathM      float64
	totalPathM             float64
}

// ComputeHorizon evaluates the published straight-ray Horizon inputs.
// Invalid or incomplete directional input produces an explicit unavailable
// result at index 1.
func ComputeHorizon(snapshot HorizonSnapshot, plan HorizonPlan, calibration OverallIndexCalibration) ([]HorizonResult, error) {
	return computeHorizon(context.Background(), snapshot, plan, calibration)
}

func computeHorizon(ctx context.Context, snapshot HorizonSnapshot, plan HorizonPlan, calibration OverallIndexCalibration) ([]HorizonResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := calibration.Validate(); err != nil {
		return nil, err
	}
	if err := validateHorizonPlan(plan); err != nil {
		return nil, err
	}
	return computeHorizonForValidatedPlan(ctx, snapshot, plan, calibration)
}

// computeHorizonForValidatedPlan contains the numerical evaluator after the
// published geometry contract has been checked. Keeping it separate lets
// package-local convergence tests compare unpublished panel spacings without
// allowing those plans through ComputeHorizon under the production version.
func computeHorizonForValidatedPlan(ctx context.Context, snapshot HorizonSnapshot, plan HorizonPlan, calibration OverallIndexCalibration) ([]HorizonResult, error) {
	if snapshot.ValidAt.IsZero() {
		return nil, fmt.Errorf("horizon snapshot valid time is required")
	}
	if !snapshot.ObserverSurface.ValidAt.Equal(snapshot.ValidAt) {
		return nil, fmt.Errorf("horizon observer surface frame does not match the common valid time")
	}
	for _, value := range []float64{
		snapshot.ObserverSurface.TemperatureC,
		snapshot.ObserverSurface.DewPointC,
		snapshot.ObserverSurface.RelativeHumidityPercent,
		snapshot.ObserverSurface.VisibilityKM,
		snapshot.ObserverSurface.WindSpeedMS,
		snapshot.ObserverSurface.WindGustMS,
		snapshot.ObserverSurface.PrecipitationMM,
	} {
		if !finite(value) {
			return nil, fmt.Errorf("horizon observer surface inputs must be finite")
		}
	}
	if !finite(snapshot.ObserverSurfaceElevationM) ||
		math.Abs(snapshot.ObserverSurfaceElevationM-plan.ObserverSurfaceElevationM) > horizonObserverElevationToleranceM {
		return nil, fmt.Errorf("horizon snapshot observer elevation differs from the geometry plan")
	}

	snapshotDirections, err := horizonSnapshotDirections(snapshot, plan)
	if err != nil {
		return nil, err
	}
	fogHeuristic := snapshot.ObserverSurface.FogHeuristic()
	observerWindFactor := surfaceWindFactor(snapshot.ObserverSurface, calibration)
	precipitationDetectMM := calibration.PrecipitationDetectMM
	if precipitationDetectMM == 0 {
		precipitationDetectMM = DefaultOverallPrecipitationDetectMM
	}
	precipitationFactor := 1.0
	if snapshot.ObserverSurface.PrecipitationMM >= precipitationDetectMM {
		precipitationFactor = 0
	}
	computations := make([]horizonDirectionComputation, len(plan.Directions))
	resolvableDirections := 0
	for index, directionPlan := range plan.Directions {
		computation, err := computeHorizonDirection(
			ctx,
			snapshot.ValidAt, plan.Observer, plan.ObserverSurfaceElevationM, directionPlan,
			snapshotDirections[directionPlan.Direction], fogHeuristic,
			observerWindFactor, precipitationFactor, calibration,
		)
		if err != nil {
			return nil, err
		}
		computations[index] = computation
		if computation.result.Available {
			resolvableDirections++
		}
	}
	resolvableFraction := float64(resolvableDirections) / float64(len(plan.Directions))
	results := make([]HorizonResult, len(computations))
	for index, computation := range computations {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		leadQuality := 0.0
		if computation.resolvedPathM > 0 {
			leadQuality = computation.weightedLeadQuality / computation.resolvedPathM
		}
		// DataQualityHeuristic is a deterministic input-quality score, not a probability:
		// 50% quality-adjusted LOS coverage, 30% normalized model lead-time
		// quality heuristic, and 20% fraction of the eight directions that can be
		// resolved. PathCoverage is the smaller of the turbulence/cloud coverage
		// scores, so sparse-level interpolation and bounded model-top extension
		// are visible rather than silently called complete. The total is capped at
		// 0.85 because this remains a deterministic model-input heuristic rather
		// than an observed skill probability. The independent static DEM profile does
		// not make ICON atmospheric inputs more certain.
		dataQualityHeuristic := math.Min(horizonMaximumDataQualityHeuristic,
			0.50*computation.result.PathCoverage+0.30*clamp(leadQuality, 0, 1)+0.20*resolvableFraction)
		if !computation.result.Available {
			dataQualityHeuristic = 0
		}
		computation.result.LeadTimeQualityHeuristic = leadQuality
		computation.result.ResolvableDirectionFraction = resolvableFraction
		computation.result.DataQualityHeuristic = dataQualityHeuristic
		computation.result.DataQuality = horizonDataQuality(
			computation.result.Available,
			dataQualityHeuristic,
			leadQuality,
		)
		results[index] = computation.result
	}
	return results, nil
}

// ComputeHorizonSeries evaluates a strictly hourly sequence without hiding
// unavailable directions. The source is responsible for pinning every
// snapshot to one immutable model run and for limiting the window to the
// provider's actual published horizon.
func ComputeHorizonSeries(ctx context.Context, snapshots []HorizonSnapshot, plan HorizonPlan, calibration OverallIndexCalibration) ([]HorizonFrame, error) {
	if ctx == nil {
		return nil, fmt.Errorf("horizon calculation context is required")
	}
	if len(snapshots) != 72 {
		return nil, fmt.Errorf("horizon series needs exactly 72 hourly snapshots (f001..f072)")
	}
	frames := make([]HorizonFrame, len(snapshots))
	for index, snapshot := range snapshots {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if index > 0 && snapshot.ValidAt.Sub(snapshots[index-1].ValidAt) != time.Hour {
			return nil, fmt.Errorf("horizon snapshots must be strictly hourly")
		}
		results, err := computeHorizon(ctx, snapshot, plan, calibration)
		if err != nil {
			return nil, fmt.Errorf("compute horizon at %s: %w", snapshot.ValidAt.Format(time.RFC3339), err)
		}
		frames[index] = HorizonFrame{ValidAt: snapshot.ValidAt, Results: results}
	}
	return frames, nil
}

// ApplyTerrainSkylineToHorizon attaches one immutable GLO-30 skyline to an
// already computed hourly series. The profile itself and its sector
// aggregation are calculated once; this pass only copies the static values.
// The GLO-30 skyline is intentionally informational in Horizon: atmospheric
// conditions are still reported for the fixed geometric 10-degree ray. The
// separate boolean tells the presentation whether at least one azimuth in the
// 45-degree sector reaches that elevation. Model-HHL intersections inside the
// atmospheric path retain their independent fail-closed semantics.
func ApplyTerrainSkylineToHorizon(frames []HorizonFrame, profile TerrainSkyline) error {
	if err := profile.Validate(); err != nil {
		return err
	}
	if !profile.Enabled() {
		return nil
	}
	for frameIndex := range frames {
		for resultIndex := range frames[frameIndex].Results {
			result := &frames[frameIndex].Results[resultIndex]
			sector, ok := profile.Sector(result.Direction)
			if !ok {
				return fmt.Errorf("terrain skyline has no sector for %s", result.Direction)
			}
			result.TerrainSkylineAvailable = true
			result.TerrainSectorMeanElevationDegrees = sector.MeanElevationDegrees
			result.TerrainSectorMaximumElevationDegrees = sector.MaximumElevationDegrees
			result.TerrainSectorHasObstructionAtEvaluationElevation =
				sector.MaximumElevationDegrees >= result.GeometricElevationDegrees
			result.TerrainAssessment = HorizonTerrainGLO30HHL
		}
	}
	return nil
}

func horizonSnapshotDirections(snapshot HorizonSnapshot, plan HorizonPlan) (map[HorizonDirection]HorizonDirectionSnapshot, error) {
	result := make(map[HorizonDirection]HorizonDirectionSnapshot, len(snapshot.Directions))
	for index, direction := range snapshot.Directions {
		key := direction.Direction
		// Positional omission is accepted for a compact adapter, but an explicit
		// direction always wins and is checked for duplicates.
		if key == "" && index < len(plan.Directions) {
			key = plan.Directions[index].Direction
			direction.Direction = key
		}
		if _, known := horizonDirectionAzimuth(key); !known {
			return nil, fmt.Errorf("horizon snapshot contains unknown direction %q", key)
		}
		if _, duplicate := result[key]; duplicate {
			return nil, fmt.Errorf("horizon snapshot repeats direction %q", key)
		}
		result[key] = direction
	}
	return result, nil
}

func computeHorizonDirection(ctx context.Context, validAt time.Time, observer Location, observerSurfaceElevationM float64, plan HorizonDirectionPlan, snapshot HorizonDirectionSnapshot, fogHeuristic int, observerWindFactor, precipitationFactor float64, calibration OverallIndexCalibration) (horizonDirectionComputation, error) {
	result := HorizonResult{
		ValidAt: validAt, Direction: plan.Direction, AzimuthDegrees: plan.AzimuthDegrees,
		GeometricElevationDegrees: HorizonGeometricElevationDegrees,
		Index:                     1, FogHeuristic: fogHeuristic, HighFogHeuristic: fogHeuristic == 2,
		TerrainAssessment: HorizonTerrainModelHHL, DataQuality: HorizonDataUnavailable,
		LimitingFactor: HorizonFactorUnavailable, LimitingFactors: []HorizonLimitingFactor{HorizonFactorUnavailable},
	}
	computation := horizonDirectionComputation{result: result}
	for _, sample := range plan.Samples {
		if err := ctx.Err(); err != nil {
			return horizonDirectionComputation{}, err
		}
		computation.totalPathM += sample.LOSPathLengthM
	}
	if snapshot.Direction != plan.Direction || len(snapshot.Samples) != len(plan.Samples) || computation.totalPathM <= 0 {
		return computation, nil
	}

	var integratedCn2, windWeightedCn2 float64
	cloudBlocks := make(map[horizonCloudBlockKey]horizonCloudBlock)
	var tierMaximumCover [3]float64
	for index, geometry := range plan.Samples {
		if err := ctx.Err(); err != nil {
			return horizonDirectionComputation{}, err
		}
		data := snapshot.Samples[index]
		if data.Surface.ValidAt.Equal(validAt) && finite(data.SurfaceElevationM) {
			if data.SurfaceElevationM >= geometry.RayHeightM {
				computation.result.TerrainBlocked = true
			}
		}
		if !horizonSampleTimesMatch(data, validAt) || !finite(data.SurfaceElevationM) {
			continue
		}
		cn2, uMS, vMS, turbulenceQuality, turbulenceOK := localHorizonTurbulence(
			data.Vertical.Levels, data.Cloud.Levels, data.Surface, data.SurfaceElevationM,
			geometry.RayHeightM, calibration,
		)
		cloud, cloudQuality, cloudOK := localHorizonCloud(data.Cloud.Levels, geometry.RayHeightM, data.SurfaceElevationM)
		if !turbulenceOK || !cloudOK || !finite(data.Vertical.LeadTimeQualityHeuristic) || data.Vertical.LeadTimeQualityHeuristic < 0 || data.Vertical.LeadTimeQualityHeuristic > 1 {
			continue
		}
		perpendicularWind := horizonPerpendicularWind(
			observer, geometry.Midpoint, uMS, vMS,
			plan.AzimuthDegrees, HorizonGeometricElevationDegrees,
		)
		ds := geometry.LOSPathLengthM
		integratedCn2 += cn2 * ds
		windWeightedCn2 += cn2 * math.Pow(perpendicularWind, 5.0/3.0) * ds
		density := cloud.pressureHPA * 100 / (287.05 * cloud.temperatureK)
		tier := horizonCloudTier(geometry.RayHeightM - data.SurfaceElevationM)
		tierMaximumCover[tier] = math.Max(tierMaximumCover[tier], cloud.cover)
		blockKey := horizonCloudBlockKey{horizontalCellID: data.HorizontalCellID, tier: tier}
		block := cloudBlocks[blockKey]
		block.liquidPathKgM2 += cloud.liquidKgKg * density * ds
		block.icePathKgM2 += cloud.iceKgKg * density * ds
		block.cover = math.Max(block.cover, cloud.cover)
		cloudBlocks[blockKey] = block
		computation.resolvedPathM += ds
		computation.turbulenceQualityPathM += turbulenceQuality * ds
		computation.cloudQualityPathM += cloudQuality * ds
		computation.weightedLeadQuality += data.Vertical.LeadTimeQualityHeuristic * ds
	}
	computation.result.ResolvedPathFraction = clamp(computation.resolvedPathM/computation.totalPathM, 0, 1)
	computation.result.TurbulenceProfileCoverage = clamp(computation.turbulenceQualityPathM/computation.totalPathM, 0, 1)
	computation.result.CloudProfileCoverage = clamp(computation.cloudQualityPathM/computation.totalPathM, 0, 1)
	computation.result.PathCoverage = math.Min(computation.result.TurbulenceProfileCoverage, computation.result.CloudProfileCoverage)
	if computation.result.ResolvedPathFraction < horizonMinimumCompletePathRatio {
		if computation.result.TerrainBlocked {
			computation.result.LimitingFactor = HorizonFactorTerrain
			computation.result.LimitingFactors = []HorizonLimitingFactor{
				HorizonFactorTerrain,
				HorizonFactorUnavailable,
			}
			if precipitationFactor < 1-1e-9 {
				computation.result.LimitingFactor = HorizonFactorPrecipitation
				computation.result.LimitingFactors = append(
					[]HorizonLimitingFactor{HorizonFactorPrecipitation},
					computation.result.LimitingFactors...,
				)
			}
		}
		return computation, nil
	}

	metrics := opticalTurbulenceMetricsFromMoments(integratedCn2, windWeightedCn2, 1)
	if !finite(metrics.SeeingArcsec) || !finite(metrics.CoherenceTimeMS) {
		return computation, nil
	}
	physicalTau, physicalTransmission := 0.0, 1.0
	for _, block := range cloudBlocks {
		blockTau, blockTransmission := horizonCloudBlockTransmission(block, calibration)
		physicalTau += blockTau
		physicalTransmission *= blockTransmission
	}
	lowGuard := unresolvedLayerCloudObstruction(tierMaximumCover[0], 0, calibration.UnresolvedCloudObstruction)
	middleGuard := unresolvedLayerCloudObstruction(tierMaximumCover[1], 3000, calibration.UnresolvedCloudObstruction)
	highGuard := unresolvedLayerCloudObstruction(tierMaximumCover[2], 8000, calibration.UnresolvedCloudObstruction)
	guardObstruction := 1 - (1-lowGuard)*(1-middleGuard)*(1-highGuard)
	// QC/QI are grid-box means. Each unique horizontal-cell/tier block therefore
	// uses the same all-sky closure as Overall, rather than pretending the mean
	// condensate is spatially homogeneous. Diagnostic CLC is additionally used
	// only once per low/middle/high tier (maximum cover + random overlap) as a
	// bounded guard for condensate that the public grid-scale fields miss.
	transmission := math.Min(physicalTransmission, 1-guardObstruction)

	// SeeingArcsec remains the physical slant-path result. Reference anchors
	// are normalized by the exact path-length ratio of this spherical geometry
	// for a homogeneous atmosphere. This is a project comparison convention,
	// not a molecular-air-mass substitution or a change to the physical result.
	// The combined seeing/tau0 utility penalty is deliberately bounded:
	// turbulence blurs detail, whereas effective cloud obstruction can remove
	// the target entirely.
	referenceScale := horizonHomogeneousSeeingScale(observerSurfaceElevationM, computation.totalPathM)
	seeingQuality := horizonSeeingQuality(metrics.SeeingArcsec, referenceScale, calibration)
	coherenceQuality := horizonCoherenceQuality(metrics.CoherenceTimeMS, referenceScale, calibration)
	opticalTurbulenceFactor := boundedOpticalTurbulenceFactor(seeingQuality, coherenceQuality, calibration)
	seeingLimiterFactor, coherenceLimiterFactor := horizonTurbulenceLimiterFactors(
		seeingQuality, coherenceQuality, opticalTurbulenceFactor, calibration,
	)
	fogFactor := 1.0
	switch fogHeuristic {
	case 1:
		fogFactor = calibration.PossibleFogFactor
	case 2:
		fogFactor = calibration.HighFogFactor
	}
	normalized := opticalTurbulenceFactor *
		math.Pow(transmission, calibration.CloudWeight) * observerWindFactor * fogFactor * precipitationFactor

	computation.result.Available = true
	computation.result.SeeingArcsec = metrics.SeeingArcsec
	computation.result.CoherenceTimeMS = metrics.CoherenceTimeMS
	computation.result.IntegratedCn2 = integratedCn2
	computation.result.WindWeightedCn2 = windWeightedCn2
	computation.result.CloudOpticalDepth = physicalTau
	computation.result.CloudTransmission = transmission
	computation.result.CloudTransmissionPercent = transmission * 100
	computation.result.CloudUnresolvedGuard = transmission < physicalTransmission-1e-12
	if computation.result.TerrainBlocked {
		normalized = 0
	}
	computation.result.Index = 1 + 9*clamp(normalized, 0, 1)
	computation.result.LimitingFactors = horizonLimitingFactors(
		computation.result.TerrainBlocked,
		seeingLimiterFactor, coherenceLimiterFactor,
		math.Pow(transmission, calibration.CloudWeight), fogFactor, observerWindFactor,
	)
	if precipitationFactor < 1-1e-9 {
		computation.result.LimitingFactors = append(
			[]HorizonLimitingFactor{HorizonFactorPrecipitation},
			computation.result.LimitingFactors...,
		)
	}
	computation.result.LimitingFactor = computation.result.LimitingFactors[0]
	return computation, nil
}

func horizonSeeingQuality(seeingArcsec, referenceScale float64, calibration OverallIndexCalibration) float64 {
	return logarithmicLowerIsBetter(
		seeingArcsec,
		calibration.GoodSeeingArcsec*referenceScale,
		calibration.BadSeeingArcsec*referenceScale,
	)
}

func horizonCoherenceQuality(coherenceTimeMS, referenceScale float64, calibration OverallIndexCalibration) float64 {
	return logarithmicHigherIsBetter(
		coherenceTimeMS,
		calibration.BadCoherenceTimeMS/referenceScale,
		calibration.BestCoherenceTimeMS/referenceScale,
	)
}

// horizonTurbulenceLimiterFactors attributes the one bounded turbulence term
// to whichever physical input has poorer raw quality. This keeps the caption
// explicit without multiplying seeing and tau0 a second time.
func horizonTurbulenceLimiterFactors(seeingQuality, coherenceQuality, boundedFactor float64, calibration OverallIndexCalibration) (seeingFactor, coherenceFactor float64) {
	seeingRaw := math.Pow(clampSurfaceValue(seeingQuality, 0, 1), calibration.SeeingWeight)
	coherenceRaw := 1 - calibration.CoherenceTimeWeight*(1-clampSurfaceValue(coherenceQuality, 0, 1))
	if seeingRaw <= coherenceRaw {
		return boundedFactor, 1
	}
	return 1, boundedFactor
}

func horizonSampleTimesMatch(sample HorizonSampleSnapshot, validAt time.Time) bool {
	return sample.Vertical.ValidAt.Equal(validAt) && sample.Surface.ValidAt.Equal(validAt) && sample.Cloud.ValidAt.Equal(validAt)
}

func localHorizonTurbulence(vertical []VerticalLevel, model []CloudLevel, surface SurfaceFrame, surfaceElevationM, rayHeightM float64, calibration OverallIndexCalibration) (cn2, uMS, vMS, quality float64, ok bool) {
	if !finite(surface.MixedLayerDepthM) || surface.MixedLayerDepthM <= 0 || !finite(rayHeightM) || rayHeightM < surfaceElevationM {
		return 0, 0, 0, 0, false
	}
	boundaryDepthM := surface.MixedLayerDepthM
	if rayHeightM <= surfaceElevationM+boundaryDepthM {
		nodes, valid := masciadriTurbulenceNodes(model, surfaceElevationM, boundaryDepthM, calibration.GroundCn2Scale)
		if !valid {
			return 0, 0, 0, 0, false
		}
		cn2, uMS, vMS, valid = interpolateHorizonTurbulenceNodes(nodes, surfaceElevationM, rayHeightM)
		return cn2, uMS, vMS, 1, valid
	}
	return localHMNSP99Turbulence(vertical, rayHeightM)
}

func interpolateHorizonTurbulenceNodes(nodes []turbulenceNode, surfaceElevationM, heightM float64) (cn2, uMS, vMS float64, ok bool) {
	if len(nodes) == 0 || heightM < surfaceElevationM || heightM > nodes[len(nodes)-1].heightM {
		return 0, 0, 0, false
	}
	if heightM <= nodes[0].heightM {
		return nodes[0].cn2, nodes[0].uMS, nodes[0].vMS, true
	}
	upper := sort.Search(len(nodes), func(index int) bool { return nodes[index].heightM >= heightM })
	if upper == 0 || upper >= len(nodes) {
		return 0, 0, 0, false
	}
	lower := upper - 1
	span := nodes[upper].heightM - nodes[lower].heightM
	if span <= 0 {
		return 0, 0, 0, false
	}
	fraction := (heightM - nodes[lower].heightM) / span
	return nodes[lower].cn2 + fraction*(nodes[upper].cn2-nodes[lower].cn2),
		nodes[lower].uMS + fraction*(nodes[upper].uMS-nodes[lower].uMS),
		nodes[lower].vMS + fraction*(nodes[upper].vMS-nodes[lower].vMS), true
}

func localHMNSP99Turbulence(levels []VerticalLevel, heightM float64) (cn2, uMS, vMS, quality float64, ok bool) {
	if len(levels) < 2 || !finite(heightM) {
		return 0, 0, 0, 0, false
	}
	for index := 1; index < len(levels); index++ {
		if !finite(levels[index-1].HeightM) || !finite(levels[index].HeightM) || levels[index].HeightM <= levels[index-1].HeightM {
			return 0, 0, 0, 0, false
		}
	}
	if heightM < levels[0].HeightM {
		return 0, 0, 0, 0, false
	}
	if heightM > levels[len(levels)-1].HeightM {
		// Common public pressure bundles stop at 50 hPa (roughly 20--21 km),
		// slightly below the fixed 22.3 km geometry top. Continue only the final
		// HMNSP99 layer value and clamp wind to the top reported level. This is a
		// bounded coarse-model-top assumption, never an unrestricted fallback;
		// its 0.35 coverage weight visibly lowers the data-quality heuristic.
		lower, upper := levels[len(levels)-2], levels[len(levels)-1]
		value, valid := hmnsp99LayerCn2(lower, upper, thermalTropopauseHeight(levels))
		if !valid || heightM > HorizonAtmosphereTopM+1e-6 {
			return 0, 0, 0, 0, false
		}
		return value, upper.UMS, upper.VMS, 0.35, true
	}
	upper := sort.Search(len(levels), func(index int) bool { return levels[index].HeightM >= heightM })
	if upper == 0 {
		upper = 1
	}
	if upper >= len(levels) {
		upper = len(levels) - 1
	}
	lower := upper - 1
	value, valid := hmnsp99LayerCn2(levels[lower], levels[upper], thermalTropopauseHeight(levels))
	if !valid {
		return 0, 0, 0, 0, false
	}
	fraction := (heightM - levels[lower].HeightM) / (levels[upper].HeightM - levels[lower].HeightM)
	return value,
		levels[lower].UMS + fraction*(levels[upper].UMS-levels[lower].UMS),
		levels[lower].VMS + fraction*(levels[upper].VMS-levels[lower].VMS), 1, true
}

type horizonCloudSample struct {
	pressureHPA  float64
	temperatureK float64
	liquidKgKg   float64
	iceKgKg      float64
	cover        float64
}

type horizonCloudBlockKey struct {
	horizontalCellID int
	tier             int
}

type horizonCloudBlock struct {
	liquidPathKgM2 float64
	icePathKgM2    float64
	cover          float64
}

func horizonCloudBlockTransmission(block horizonCloudBlock, calibration OverallIndexCalibration) (opticalDepth, transmission float64) {
	liquidTau := phaseOpticalDepth(block.liquidPathKgM2, 2.0, 1000, calibration.CloudLiquidRadiusMicrometers)
	iceTau := phaseOpticalDepth(block.icePathKgM2, 2.1, 916.7, calibration.CloudIceRadiusMicrometers)
	opticalDepth = liquidTau + iceTau
	cover := clampSurfaceValue(block.cover, 0, 1)
	if cover < 1e-6 {
		if opticalDepth < 1e-9 {
			return opticalDepth, 1
		}
		// Match the ordinary Overall closure for the rare inconsistent case
		// where grid-box condensate exists while diagnostic cover rounds to 0.
		cover = math.Min(1, math.Max(0.01, 1-math.Exp(-opticalDepth)))
	}
	transmission = (1 - cover) + cover*math.Exp(-opticalDepth/cover)
	return opticalDepth, clampSurfaceValue(transmission, 0, 1)
}

func localHorizonCloud(levels []CloudLevel, heightM, surfaceElevationM float64) (horizonCloudSample, float64, bool) {
	if len(levels) == 0 || !finite(heightM) || heightM < surfaceElevationM {
		return horizonCloudSample{}, 0, false
	}
	sortedLevels := append([]CloudLevel(nil), levels...)
	sort.Slice(sortedLevels, func(i, j int) bool { return sortedLevels[i].HeightM < sortedLevels[j].HeightM })
	// CloudLevel is a native full level and LayerThicknessM comes from its two
	// enclosing HHLs. Prefer the actual containing cell. Gaps in the retained
	// profile take the explicitly lower-quality interpolation path below.
	for _, level := range sortedLevels {
		if !finite(level.LayerThicknessM) || level.LayerThicknessM <= 0 {
			continue
		}
		halfThickness := level.LayerThicknessM / 2
		if heightM >= level.HeightM-halfThickness-1e-6 && heightM <= level.HeightM+halfThickness+1e-6 {
			value, valid := horizonCloudLevel(level)
			return value, 1, valid
		}
	}
	upper := sort.Search(len(sortedLevels), func(index int) bool { return sortedLevels[index].HeightM >= heightM })
	if upper == 0 {
		value, valid := horizonCloudLevel(sortedLevels[0])
		return value, 0.50, valid
	}
	if upper == len(sortedLevels) {
		// The highest retained native level can sit just below the fixed model
		// atmosphere top. Clamp its usually negligible condensate only across
		// this bounded edge gap and record a strong quality penalty.
		if heightM > HorizonAtmosphereTopM+1e-6 {
			return horizonCloudSample{}, 0, false
		}
		value, valid := horizonCloudLevel(sortedLevels[len(sortedLevels)-1])
		return value, 0.25, valid
	}
	lower := upper - 1
	lowerValue, lowerOK := horizonCloudLevel(sortedLevels[lower])
	upperValue, upperOK := horizonCloudLevel(sortedLevels[upper])
	span := sortedLevels[upper].HeightM - sortedLevels[lower].HeightM
	if !lowerOK || !upperOK || span <= 0 {
		return horizonCloudSample{}, 0, false
	}
	fraction := (heightM - sortedLevels[lower].HeightM) / span
	// Interpolating the mixing ratio is a coarse vertical-profile heuristic;
	// LayerThicknessM is deliberately not used as mass for the skipped native
	// layers. The represented share of the bracket controls the reported cloud
	// coverage/data-quality penalty.
	represented := (math.Max(0, sortedLevels[lower].LayerThicknessM) + math.Max(0, sortedLevels[upper].LayerThicknessM)) / (2 * span)
	quality := clamp(represented, 0.35, 0.85)
	return horizonCloudSample{
		pressureHPA:  interpolatePositiveLog(lowerValue.pressureHPA, upperValue.pressureHPA, fraction),
		temperatureK: lowerValue.temperatureK + fraction*(upperValue.temperatureK-lowerValue.temperatureK),
		liquidKgKg:   lowerValue.liquidKgKg + fraction*(upperValue.liquidKgKg-lowerValue.liquidKgKg),
		iceKgKg:      lowerValue.iceKgKg + fraction*(upperValue.iceKgKg-lowerValue.iceKgKg),
		cover:        lowerValue.cover + fraction*(upperValue.cover-lowerValue.cover),
	}, quality, true
}

func horizonCloudLevel(level CloudLevel) (horizonCloudSample, bool) {
	if !finite(level.HeightM) || !finite(level.PressureHPA) || level.PressureHPA <= 0 ||
		!validProfileTemperature(level.TemperatureK) || !finite(level.CloudLiquidKgKg) ||
		!finite(level.CloudIceKgKg) || !finite(level.CoverPercent) {
		return horizonCloudSample{}, false
	}
	return horizonCloudSample{
		pressureHPA: level.PressureHPA, temperatureK: level.TemperatureK,
		liquidKgKg: math.Max(0, level.CloudLiquidKgKg), iceKgKg: math.Max(0, level.CloudIceKgKg),
		cover: clampSurfaceValue(level.CoverPercent/100, 0, 1),
	}, true
}

func horizonCloudTier(heightAGLM float64) int {
	switch {
	case heightAGLM >= 7000:
		return 2
	case heightAGLM >= 2000:
		return 1
	default:
		return 0
	}
}

func horizonPerpendicularWind(origin, sample Location, uMS, vMS, azimuthDegrees, elevationDegrees float64) float64 {
	lineOfSightEast, lineOfSightNorth, _ := horizonLocalLOSComponents(origin, sample, azimuthDegrees, elevationDegrees)
	dot := uMS*lineOfSightEast + vMS*lineOfSightNorth
	perpendicularSquared := uMS*uMS + vMS*vMS - dot*dot
	return math.Sqrt(math.Max(0, perpendicularSquared))
}

// horizonLocalLOSComponents projects the fixed straight ECEF ray into the
// local ENU basis of a remote midpoint. This accounts for both the increasing
// local elevation of the chord and great-circle bearing convergence.
func horizonLocalLOSComponents(origin, sample Location, azimuthDegrees, elevationDegrees float64) (east, north, up float64) {
	originLatitude := origin.Latitude * math.Pi / 180
	originLongitude := origin.Longitude * math.Pi / 180
	azimuth := azimuthDegrees * math.Pi / 180
	elevation := elevationDegrees * math.Pi / 180

	originUpX := math.Cos(originLatitude) * math.Cos(originLongitude)
	originUpY := math.Cos(originLatitude) * math.Sin(originLongitude)
	originUpZ := math.Sin(originLatitude)
	originEastX, originEastY := -math.Sin(originLongitude), math.Cos(originLongitude)
	originNorthX := -math.Sin(originLatitude) * math.Cos(originLongitude)
	originNorthY := -math.Sin(originLatitude) * math.Sin(originLongitude)
	originNorthZ := math.Cos(originLatitude)
	horizontalEast := math.Sin(azimuth)
	horizontalNorth := math.Cos(azimuth)
	directionX := math.Cos(elevation)*(horizontalEast*originEastX+horizontalNorth*originNorthX) + math.Sin(elevation)*originUpX
	directionY := math.Cos(elevation)*(horizontalEast*originEastY+horizontalNorth*originNorthY) + math.Sin(elevation)*originUpY
	directionZ := math.Cos(elevation)*(horizontalNorth*originNorthZ) + math.Sin(elevation)*originUpZ

	sampleLatitude := sample.Latitude * math.Pi / 180
	sampleLongitude := sample.Longitude * math.Pi / 180
	sampleEastX, sampleEastY := -math.Sin(sampleLongitude), math.Cos(sampleLongitude)
	sampleNorthX := -math.Sin(sampleLatitude) * math.Cos(sampleLongitude)
	sampleNorthY := -math.Sin(sampleLatitude) * math.Sin(sampleLongitude)
	sampleNorthZ := math.Cos(sampleLatitude)
	sampleUpX := math.Cos(sampleLatitude) * math.Cos(sampleLongitude)
	sampleUpY := math.Cos(sampleLatitude) * math.Sin(sampleLongitude)
	sampleUpZ := math.Sin(sampleLatitude)
	return directionX*sampleEastX + directionY*sampleEastY,
		directionX*sampleNorthX + directionY*sampleNorthY + directionZ*sampleNorthZ,
		directionX*sampleUpX + directionY*sampleUpY + directionZ*sampleUpZ
}

func horizonHomogeneousSeeingScale(observerSurfaceElevationM, losPathM float64) float64 {
	verticalPathM := HorizonAtmosphereTopM - observerSurfaceElevationM
	if !finite(verticalPathM) || verticalPathM <= 0 || !finite(losPathM) || losPathM <= 0 {
		return math.NaN()
	}
	return math.Pow(losPathM/verticalPathM, 3.0/5.0)
}

type horizonFactorScore struct {
	factor HorizonLimitingFactor
	score  float64
}

func horizonLimitingFactors(terrainBlocked bool, seeing, coherence, cloud, fog, wind float64) []HorizonLimitingFactor {
	if terrainBlocked {
		return []HorizonLimitingFactor{HorizonFactorTerrain}
	}
	scores := []horizonFactorScore{
		{HorizonFactorSeeing, seeing},
		{HorizonFactorCoherence, coherence},
		{HorizonFactorCloud, cloud},
		{HorizonFactorFog, fog},
		{HorizonFactorSurfaceWind, wind},
	}
	sort.SliceStable(scores, func(i, j int) bool { return scores[i].score < scores[j].score })
	factors := make([]HorizonLimitingFactor, 0, len(scores))
	for _, score := range scores {
		if score.score < 1-1e-9 {
			factors = append(factors, score.factor)
		}
	}
	if len(factors) == 0 {
		return []HorizonLimitingFactor{HorizonFactorNone}
	}
	return factors
}

func horizonDataQuality(available bool, dataQualityHeuristic, leadQualityHeuristic float64) HorizonDataQuality {
	if !available {
		return HorizonDataUnavailable
	}
	switch {
	case dataQualityHeuristic < horizonLimitedDataQualityHeuristic || leadQualityHeuristic < horizonUsableLeadQualityHeuristic:
		return HorizonDataLimited
	case leadQualityHeuristic >= horizonGoodLeadQualityHeuristic:
		return HorizonDataGood
	default:
		return HorizonDataUsable
	}
}

func validateHorizonPlan(plan HorizonPlan) error {
	if plan.AlgorithmVersion != HorizonAlgorithmVersion {
		return fmt.Errorf("horizon plan algorithm version %q is unsupported", plan.AlgorithmVersion)
	}
	if err := ValidateCoordinates(plan.Observer.Latitude, plan.Observer.Longitude); err != nil {
		return fmt.Errorf("horizon plan observer: %w", err)
	}
	if math.Abs(plan.Observer.Latitude) == 90 {
		return fmt.Errorf("horizon azimuth is degenerate at an exact geographic pole")
	}
	if plan.Observer.Longitude >= 180 || !finite(plan.ObserverSurfaceElevationM) ||
		plan.ObserverSurfaceElevationM >= HorizonAtmosphereTopM ||
		plan.GeometricElevationDegrees != HorizonGeometricElevationDegrees ||
		plan.AtmosphereTopM != HorizonAtmosphereTopM ||
		plan.SurfaceSegmentLengthM != HorizonSurfaceSegmentLengthM {
		return fmt.Errorf("horizon plan does not use the fixed normalized geometry")
	}
	if len(plan.Directions) != len(fixedHorizonDirections) {
		return fmt.Errorf("horizon plan needs exactly eight directions")
	}
	seen := make(map[HorizonDirection]bool, len(plan.Directions))
	for directionIndex, direction := range plan.Directions {
		azimuth, known := horizonDirectionAzimuth(direction.Direction)
		if !known || direction.Direction != fixedHorizonDirections[directionIndex].direction ||
			math.Abs(direction.AzimuthDegrees-azimuth) > 1e-9 || seen[direction.Direction] {
			return fmt.Errorf("horizon plan contains an invalid or repeated direction %q", direction.Direction)
		}
		seen[direction.Direction] = true
		if len(direction.Samples) == 0 {
			return fmt.Errorf("horizon direction %s has no samples", direction.Direction)
		}
		previousEnd := 0.0
		for index, sample := range direction.Samples {
			if !finite(sample.StartSurfaceDistanceM) || !finite(sample.EndSurfaceDistanceM) ||
				math.Abs(sample.StartSurfaceDistanceM-previousEnd) > 1e-6 || sample.EndSurfaceDistanceM <= sample.StartSurfaceDistanceM ||
				!finite(sample.LOSPathLengthM) || sample.LOSPathLengthM <= 0 || !finite(sample.RayHeightM) ||
				sample.Midpoint.Longitude < -180 || sample.Midpoint.Longitude >= 180 ||
				!finite(sample.Midpoint.Latitude) || math.Abs(sample.Midpoint.Latitude) > 90 {
				return fmt.Errorf("horizon direction %s sample %d is invalid", direction.Direction, index)
			}
			previousEnd = sample.EndSurfaceDistanceM
		}
	}
	expected, err := newHorizonPlan(
		plan.Observer, plan.ObserverSurfaceElevationM,
		HorizonSurfaceSegmentLengthM, HorizonAtmosphereTopM,
	)
	if err != nil {
		return fmt.Errorf("rebuild horizon plan: %w", err)
	}
	for directionIndex, direction := range plan.Directions {
		wanted := expected.Directions[directionIndex]
		if len(direction.Samples) != len(wanted.Samples) {
			return fmt.Errorf("horizon direction %s sample count differs from fixed geometry", direction.Direction)
		}
		for sampleIndex, sample := range direction.Samples {
			wantedSample := wanted.Samples[sampleIndex]
			if math.Abs(sample.StartSurfaceDistanceM-wantedSample.StartSurfaceDistanceM) > 1e-6 ||
				math.Abs(sample.EndSurfaceDistanceM-wantedSample.EndSurfaceDistanceM) > 1e-6 ||
				math.Abs(sample.LOSPathLengthM-wantedSample.LOSPathLengthM) > 1e-6 ||
				math.Abs(sample.RayHeightM-wantedSample.RayHeightM) > 1e-6 ||
				math.Abs(sample.Midpoint.Latitude-wantedSample.Midpoint.Latitude) > 1e-10 ||
				math.Abs(sample.Midpoint.Longitude-wantedSample.Midpoint.Longitude) > 1e-10 {
				return fmt.Errorf("horizon direction %s sample %d differs from fixed geometry", direction.Direction, sampleIndex)
			}
		}
	}
	return nil
}

func horizonDirectionAzimuth(direction HorizonDirection) (float64, bool) {
	for _, fixed := range fixedHorizonDirections {
		if fixed.direction == direction {
			return fixed.azimuth, true
		}
	}
	return 0, false
}
