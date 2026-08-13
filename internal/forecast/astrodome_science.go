package forecast

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

const (
	AstrodomeScienceVersion                = "astrodome-science-kernel-v30-explicit-heuristics"
	AstrodomeScienceGeometryStraight       = "straight-compat"
	AstrodomeScienceGeometryRefractionFull = "refraction-full"
	AstrodomeSciencePathContractVersion    = "astrodome-science-path-v23"
	// Root evidence is localized to a maximum 0.2-millimetre radius. The
	// independent cluster scale rejects distinct unresolved events, while the
	// larger side guard keeps every branch-sensitive quadrature sample strictly
	// outside the certified root envelope.
	AstrodomeScienceRootToleranceM         = 0.2e-3
	AstrodomeScienceRootMergeToleranceM    = 0.4e-3
	AstrodomeScienceCompoundRootSideGuardM = 0.5e-3
	AstrodomeScienceGK15EndpointFraction   = (1 - 0.991455371120812639206854697526329) / 2
	// Event-forced intervals use a hierarchy of independent, positive-weight,
	// non-extrapolatory Gauss--Legendre pairs. The outer node of each higher
	// rule must remain strictly outside the 0.5-mm endpoint envelopes. Panels
	// below the GL2/GL1 sampling floor use the v29 limited midpoint path; the
	// ill-conditioned moment-fitted Q5/Q3 extrapolation remains removed. Every limit
	// includes a relative 1e-12 representation margin above the exact geometric
	// quotient.
	AstrodomeScienceGL5EndpointFraction = (1 - 0.906179845938663992797626878299393) / 2
	AstrodomeScienceGL3EndpointFraction = (1 - 0.774596669241483377035853079956480) / 2
	AstrodomeScienceGL2EndpointFraction = (1 - 0.577350269189625764509148780501957) / 2
	// AstrodomeScienceGK15MinimumEventIntervalLengthM is the smallest interval
	// whose nearest K15 node clears the 0.5-mm side envelope.
	AstrodomeScienceGK15MinimumEventIntervalLengthM = (AstrodomeScienceCompoundRootSideGuardM / AstrodomeScienceGK15EndpointFraction) * (1 + 1e-12)
	// AstrodomeScienceGL5MinimumEventIntervalLengthM is the lower bound for the
	// standard GL5/GL3 rule. Shorter physical panels use the positive-weight
	// GL3/GL2 or GL2/GL1 pairs when their own endpoint guards remain valid.
	AstrodomeScienceGL5MinimumEventIntervalLengthM = (AstrodomeScienceCompoundRootSideGuardM / AstrodomeScienceGL5EndpointFraction) * (1 + 1e-12)
	// AstrodomeScienceGL3MinimumEventIntervalLengthM is the lower bound for the
	// standard positive-weight GL3/GL2 pair.
	AstrodomeScienceGL3MinimumEventIntervalLengthM = (AstrodomeScienceCompoundRootSideGuardM / AstrodomeScienceGL3EndpointFraction) * (1 + 1e-12)
	// AstrodomeScienceGL2MinimumEventIntervalLengthM is the lower bound for the
	// standard positive-weight GL2/GL1 pair. A lone midpoint below this floor
	// is published only as an explicitly limited approximation.
	AstrodomeScienceGL2MinimumEventIntervalLengthM = (AstrodomeScienceCompoundRootSideGuardM / AstrodomeScienceGL2EndpointFraction) * (1 + 1e-12)
	// AstrodomeScienceMinimumEventIntervalLengthM is the absolute publishable
	// event-interval floor used while validating retained root evidence. At or
	// below twice the side guard no non-empty safe sampling interval exists
	// between the two physical endpoint envelopes, so integration fails closed.
	// No extrapolation condition number is accepted. Each adaptive child
	// receives its proportional absolute tolerance.
	AstrodomeScienceMinimumEventIntervalLengthM = (2 * AstrodomeScienceCompoundRootSideGuardM) * (1 + 1e-12)
	// AstrodomeScienceMaximumApproximatePathLengthM is the absolute per-ray
	// publication ceiling for the union length of unresolved endpoint slivers
	// plus the sum of positive midpoint-only physical-panel lengths. It is a
	// product calibration, not
	// a quadrature step: ordinary panels still have to satisfy their unchanged
	// embedded error budget. A result above this ceiling remains fail-closed.
	AstrodomeScienceMaximumApproximatePathLengthM = 1.0
	AstrodomeScienceMinimumPartitionLengthM       = 1e-2
	astrodomeScienceIntegralCount                 = 5
	astrodomeScienceCn2Index                      = 0
	astrodomeScienceWindCn2Index                  = 1
	astrodomeScienceWaterIndex                    = 2
	astrodomeScienceLiquidExtinctionIndex         = 3
	astrodomeScienceIceExtinctionIndex            = 4
	astrodomeSciencePoissonExponent               = 0.286
	AstrodomeScienceCloudLowTopAGLM               = 2000.0
	AstrodomeScienceCloudMiddleTopAGLM            = 7000.0
	astrodomeSciencePartitionEndpointInsetM       = AstrodomeScienceCompoundRootSideGuardM
)

// AstrodomeSciencePartitionInteriorEndpoints returns metric one-sided probes
// for a numerically located path partition. A floating-point Nextafter in the
// path coordinate is many orders of magnitude smaller than a solved event
// bracket and therefore cannot establish which side of the event is sampled.
func AstrodomeSciencePartitionInteriorEndpoints(startM, endM float64) (float64, float64, error) {
	return astrodomeSciencePartitionInteriorEndpoints(astrodomeScienceAtomicInterval{
		startM: startM,
		endM:   endM,
	})
}

func astrodomeSciencePartitionInteriorEndpoints(interval astrodomeScienceAtomicInterval) (float64, float64, error) {
	startM, endM := interval.startM, interval.endM
	if !finite(startM) || !finite(endM) || endM <= startM {
		return 0, 0, fmt.Errorf("astrodome science partition endpoints are invalid")
	}
	lengthM := endM - startM
	minimumLengthM := astrodomeScienceMinimumIntervalLength(interval)
	if lengthM <= minimumLengthM {
		return 0, 0, fmt.Errorf("astrodome science partition does not clear its %.9g m endpoint/numerical floor", minimumLengthM)
	}
	if interval.certifiedShort != nil {
		certificate := interval.certifiedShort
		leftM, rightM := startM, endM
		if !interval.startNumericalBoundary {
			leftM = math.Nextafter(math.Max(startM, certificate.OpenSafeStartPathM), math.Inf(1))
		}
		if !interval.endNumericalBoundary {
			rightM = math.Nextafter(math.Min(endM, certificate.OpenSafeEndPathM), math.Inf(-1))
		}
		if !finite(leftM) || !finite(rightM) || leftM < startM || rightM > endM || leftM >= rightM {
			return 0, 0, fmt.Errorf("certified astrodome science partition has no representable safe interior")
		}
		return leftM, rightM, nil
	}
	insetM := math.Max(astrodomeSciencePartitionEndpointInsetM, lengthM*1e-12)
	if insetM <= AstrodomeScienceCompoundRootSideGuardM {
		insetM = math.Nextafter(AstrodomeScienceCompoundRootSideGuardM, math.Inf(1))
	}
	leftM, rightM := startM, endM
	if !interval.startNumericalBoundary {
		leftM = startM + insetM
		for leftM-startM <= AstrodomeScienceCompoundRootSideGuardM {
			leftM = math.Nextafter(leftM, math.Inf(1))
		}
	}
	if !interval.endNumericalBoundary {
		rightM = endM - insetM
		for endM-rightM <= AstrodomeScienceCompoundRootSideGuardM {
			rightM = math.Nextafter(rightM, math.Inf(-1))
		}
	}
	if !finite(leftM) || !finite(rightM) || leftM < startM || rightM > endM || leftM >= rightM {
		return 0, 0, fmt.Errorf("astrodome science partition has no representable interior probes")
	}
	return leftM, rightM, nil
}

// ErrAstrodomeScienceNonConvergence means that the numerical result must not
// be published. The last quadrature estimate is deliberately not returned as
// an available science node.
var ErrAstrodomeScienceNonConvergence = errors.New("astrodome science integration did not converge")

// ErrAstrodomeScienceTerrainBlocked represents a physical obstruction, not
// failed meteorology and not an Overall value of one.
var ErrAstrodomeScienceTerrainBlocked = errors.New("astrodome line of sight is blocked by model terrain")

// ErrAstrodomeScienceIncompletePartition means a declared interval crossed a
// physical branch or cloud-block boundary. The path planner must provide the
// exact event; the kernel never hides it with a midpoint approximation.
var ErrAstrodomeScienceIncompletePartition = errors.New("astrodome science path omits a physical breakpoint")

// AstrodomeScienceFogHeuristic is an explicit native-site assessment. Fog is not
// inferred from a slant-path proxy inside the physical kernel.
type AstrodomeScienceFogHeuristic string

const (
	AstrodomeScienceFogUnavailable AstrodomeScienceFogHeuristic = "unavailable"
	AstrodomeScienceFogNone        AstrodomeScienceFogHeuristic = "none"
	AstrodomeScienceFogPossible    AstrodomeScienceFogHeuristic = "possible"
	AstrodomeScienceFogHigh        AstrodomeScienceFogHeuristic = "high"
)

// AstrodomeScienceCloudTier identifies the fixed AGL bands of one cloud
// overlap block. A horizontal grid cell and a tier form one unique block.
type AstrodomeScienceCloudTier string

const (
	AstrodomeScienceCloudLow    AstrodomeScienceCloudTier = "low"
	AstrodomeScienceCloudMiddle AstrodomeScienceCloudTier = "middle"
	AstrodomeScienceCloudHigh   AstrodomeScienceCloudTier = "high"
)

type AstrodomeScienceTerrainState string

const (
	AstrodomeScienceTerrainClear   AstrodomeScienceTerrainState = "clear"
	AstrodomeScienceTerrainGrazing AstrodomeScienceTerrainState = "grazing"
	AstrodomeScienceTerrainBlocked AstrodomeScienceTerrainState = "blocked"
)

type AstrodomeScienceNodeState string

const (
	AstrodomeScienceNodeUnavailable    AstrodomeScienceNodeState = "unavailable"
	AstrodomeScienceNodeAvailable      AstrodomeScienceNodeState = "available"
	AstrodomeScienceNodeTerrainGrazing AstrodomeScienceNodeState = "terrain_grazing"
	AstrodomeScienceNodeTerrainBlocked AstrodomeScienceNodeState = "terrain_blocked"
)

// AstrodomeScienceThermalPrimitive is a vertically ordered reconstruction of
// native P/T primitives used only to diagnose the WMO thermal tropopause.
// It is not a precomputed turbulence product.
type AstrodomeScienceThermalPrimitive struct {
	HeightM      float64 `json:"height_m"`
	PressurePa   float64 `json:"pressure_pa"`
	TemperatureK float64 `json:"temperature_k"`
}

// AstrodomeScienceNativeContext contains only raw/reconstructed primitive
// context at the exact quadrature location. HSURF and MH are interpolated as
// native fields first; no PBL branch, tropopause, Cn2, transmission, or score
// is accepted from this boundary.
type AstrodomeScienceNativeContext struct {
	HorizontalCellID string                             `json:"horizontal_cell_id"`
	SurfaceHeightM   float64                            `json:"surface_height_m"`
	MixedLayerDepthM float64                            `json:"mixed_layer_depth_m"`
	ThermalProfile   []AstrodomeScienceThermalPrimitive `json:"thermal_profile,omitempty"`
}

// AstrodomeScienceBoundaryHeights contains the branch surfaces that a path
// planner must solve on the curved ray before adaptive science quadrature.
// They are diagnosed from native primitives by the same functions used by
// the integrand, avoiding a second provider-specific approximation.
type AstrodomeScienceBoundaryHeights struct {
	BoundaryLayerTopM         float64                                `json:"boundary_layer_top_m"`
	TropopauseHeightM         float64                                `json:"tropopause_height_m"`
	TropopauseMethod          string                                 `json:"tropopause_method"`
	TropopauseBoundaryKind    AstrodomeScienceTropopauseBoundaryKind `json:"tropopause_boundary_kind"`
	TropopauseLowerLevelIndex int                                    `json:"tropopause_lower_level_index"`
	TropopauseUpperLevelIndex int                                    `json:"tropopause_upper_level_index"`
}

type AstrodomeScienceTropopauseBoundaryKind string

const (
	AstrodomeScienceTropopauseBoundaryNone             AstrodomeScienceTropopauseBoundaryKind = "none"
	AstrodomeScienceTropopauseBoundaryWMOLevel         AstrodomeScienceTropopauseBoundaryKind = "wmo-level"
	AstrodomeScienceTropopauseBoundaryPressureFallback AstrodomeScienceTropopauseBoundaryKind = "pressure-fallback"
)

func ResolveAstrodomeScienceBoundaryHeights(
	native AstrodomeScienceNativeContext,
	calibration AstrodomeScienceCalibration,
) (AstrodomeScienceBoundaryHeights, error) {
	if !finite(native.SurfaceHeightM) || !finite(native.MixedLayerDepthM) || native.MixedLayerDepthM < 0 {
		return AstrodomeScienceBoundaryHeights{}, fmt.Errorf("astrodome native boundary context is invalid")
	}
	if err := calibration.Validate(); err != nil {
		return AstrodomeScienceBoundaryHeights{}, err
	}
	tropopause, method, kind, lowerLevel, upperLevel, err := astrodomeScienceTropopauseBoundary(native.ThermalProfile)
	if err != nil {
		return AstrodomeScienceBoundaryHeights{}, err
	}
	return AstrodomeScienceBoundaryHeights{
		BoundaryLayerTopM: native.SurfaceHeightM + clamp(native.MixedLayerDepthM,
			calibration.Overall.BoundaryLayerMinM, calibration.Overall.BoundaryLayerTopM),
		TropopauseHeightM: tropopause, TropopauseMethod: method,
		TropopauseBoundaryKind: kind, TropopauseLowerLevelIndex: lowerLevel,
		TropopauseUpperLevelIndex: upperLevel,
	}, nil
}

// AstrodomeScienceNativeContextResolver supplies the exact local native
// context and is immutable for Path.SourceIdentity/ValidAt.
type AstrodomeScienceNativeContextResolver interface {
	ResolveAstrodomeScienceNativeContext(
		ctx context.Context,
		validAt time.Time,
		point AstrodomeRayPoint,
		stencil AstrodomeHorizontalStencil,
	) (AstrodomeScienceNativeContext, error)
}

// AstrodomeScienceRootEvidence is the retained enclosure of one path event.
// RepresentativePathM is only the partition coordinate; the physical root is
// certified to lie inside [LeftPathM, RightPathM]. Exact events have a
// zero-width enclosure. EventID and Kind preserve provider proof provenance.
type AstrodomeScienceRootEvidence struct {
	RepresentativePathM float64 `json:"representative_path_m"`
	LeftPathM           float64 `json:"left_path_m"`
	RightPathM          float64 `json:"right_path_m"`
	EventID             string  `json:"event_id"`
	Kind                string  `json:"kind"`
	Exact               bool    `json:"exact"`
}

// AstrodomeScienceCertifiedShortInterval is an alternative ownership proof
// for two distinct events whose representatives are more than the 0.4-mm
// cluster rejection threshold but no more than the ordinary two-sided
// 0.5-mm-guard floor apart. OpenSafeStartPathM/OpenSafeEndPathM exclude both
// retained root enclosures and the provider-evaluated path/coordinate error.
// Every represented quadrature node is independently rechecked by Verifier.
type AstrodomeScienceCertifiedShortInterval struct {
	CertificateID      string                       `json:"certificate_id"`
	HorizontalCellID   string                       `json:"horizontal_cell_id"`
	Start              AstrodomeScienceRootEvidence `json:"start"`
	End                AstrodomeScienceRootEvidence `json:"end"`
	OpenSafeStartPathM float64                      `json:"open_safe_start_path_m"`
	OpenSafeEndPathM   float64                      `json:"open_safe_end_path_m"`
}

// AstrodomeScienceShortIntervalVerifier is the small consumer-owned boundary
// used only by certified sub-millimetre panels. The provider must fail closed
// unless the represented node remains in the expected horizontal cell, stays
// outside both evidence enclosures after coordinate/trajectory uncertainty,
// and has the certified physical-boundary/branch residual signs.
type AstrodomeScienceShortIntervalVerifier interface {
	VerifyAstrodomeScienceShortIntervalNode(
		ctx context.Context,
		validAt time.Time,
		pathM float64,
		certificate AstrodomeScienceCertifiedShortInterval,
	) error
}

// AstrodomeSciencePathCell is an exact LOS interval within one horizontal
// model cell. BreakpointsPathM contains exact native vertical and solved PBL,
// tropopause, cloud-tier, terrain, and top events. The kernel verifies at all
// quadrature nodes that an atomic interval stays in one physical regime and
// rejects an incomplete partition instead of approximating it.
type AstrodomeSciencePathCell struct {
	StartPathM                       float64                                  `json:"start_path_m"`
	EndPathM                         float64                                  `json:"end_path_m"`
	HorizontalCellID                 string                                   `json:"horizontal_cell_id"`
	BreakpointsPathM                 []float64                                `json:"breakpoints_path_m,omitempty"`
	StartEvidence                    *AstrodomeScienceRootEvidence            `json:"start_evidence,omitempty"`
	EndEvidence                      *AstrodomeScienceRootEvidence            `json:"end_evidence,omitempty"`
	BreakpointEvidence               []AstrodomeScienceRootEvidence           `json:"breakpoint_evidence,omitempty"`
	CertifiedShortIntervals          []AstrodomeScienceCertifiedShortInterval `json:"certified_short_intervals,omitempty"`
	NativeVerticalPredicatesIsolated bool                                     `json:"native_vertical_predicates_isolated"`
	TropopausePredicatesIsolated     bool                                     `json:"tropopause_predicates_isolated"`
	TerrainState                     AstrodomeScienceTerrainState             `json:"terrain_state"`
}

// AstrodomeSciencePathAvailability is the binary D31 gate. A high weighted
// coverage cannot compensate for even a short missing mandatory panel.
type AstrodomeSciencePathAvailability struct {
	Geometry         bool `json:"geometry"`
	Turbulence       bool `json:"turbulence"`
	Cloud            bool `json:"cloud"`
	Humidity         bool `json:"humidity"`
	TemporalBrackets bool `json:"temporal_brackets"`
	Terrain          bool `json:"terrain"`
}

// AstrodomeSciencePath is the provider-neutral output of exact native-grid
// ray tracing. It is immutable for the lifetime of one calculation.
type AstrodomeSciencePath struct {
	ContractVersion         string                                `json:"contract_version"`
	SourceIdentity          AstrodomePrimitiveVolumeIdentity      `json:"source_identity"`
	ValidAt                 time.Time                             `json:"valid_at"`
	GeometryMode            string                                `json:"geometry_mode"`
	NativeContext           AstrodomeScienceNativeContextResolver `json:"-"`
	ShortIntervalVerifier   AstrodomeScienceShortIntervalVerifier `json:"-"`
	Cells                   []AstrodomeSciencePathCell            `json:"cells"`
	Availability            AstrodomeSciencePathAvailability      `json:"availability"`
	TopClosed               bool                                  `json:"top_closed"`
	GeometryCoverage        float64                               `json:"geometry_coverage"`
	TurbulencePathCoverage  float64                               `json:"turbulence_path_coverage"`
	CloudPathCoverage       float64                               `json:"cloud_path_coverage"`
	HumidityPathCoverage    *float64                              `json:"humidity_path_coverage,omitempty"`
	TemporalResolutionHours float64                               `json:"temporal_resolution_hours"`
	ApproximationLengthM    float64                               `json:"approximation_length_m"`
}

// AstrodomeScienceSiteInputs contains observer-local operational primitives.
// PrecipitationRateMMPerHour must already be obtained by differencing the
// native GRIB accumulation over the explicit interval; it is never temporally
// interpolated as an instantaneous value.
type AstrodomeScienceSiteInputs struct {
	SourceIdentity             AstrodomePrimitiveVolumeIdentity `json:"source_identity"`
	ValidAt                    time.Time                        `json:"valid_at"`
	WindSpeed10MMS             float64                          `json:"wind_speed_10m_ms"`
	WindGust10MMS              float64                          `json:"wind_gust_10m_ms"`
	FogHeuristic               AstrodomeScienceFogHeuristic     `json:"fog_heuristic"`
	PrecipitationRateMMPerHour float64                          `json:"precipitation_rate_mm_per_hour"`
	PrecipitationIntervalStart time.Time                        `json:"precipitation_interval_start"`
	PrecipitationIntervalEnd   time.Time                        `json:"precipitation_interval_end"`
	ForecastLeadHours          float64                          `json:"forecast_lead_hours"`
}

// AstrodomeScienceIntegralTolerances declares an absolute numerical scale for
// every jointly integrated component. Relative error alone is undefined near
// clear/calm limits and is therefore insufficient.
type AstrodomeScienceIntegralTolerances struct {
	IntegratedCn2      float64 `json:"integrated_cn2"`
	WindWeightedCn2    float64 `json:"wind_weighted_cn2"`
	SlantWaterKgM2     float64 `json:"slant_water_kg_m2"`
	LiquidOpticalDepth float64 `json:"liquid_optical_depth"`
	IceOpticalDepth    float64 `json:"ice_optical_depth"`
}

// AstrodomeScienceIntegrationVerification selects how the adaptive integral
// is accepted. Production publishes the error estimate already embedded in
// each G7/K15 or positive-weight non-extrapolatory Gauss--Legendre pair. The
// reference mode repeats the complete integral with half tolerances and is reserved for regression,
// calibration, and release verification; it is not the production hot path.
type AstrodomeScienceIntegrationVerification string

const (
	AstrodomeScienceVerificationEmbedded          AstrodomeScienceIntegrationVerification = "embedded-adaptive"
	AstrodomeScienceVerificationIndependentRepeat AstrodomeScienceIntegrationVerification = "independent-repeat"
)

func (tolerances AstrodomeScienceIntegralTolerances) vector() astrodomeScienceVector {
	return astrodomeScienceVector{
		tolerances.IntegratedCn2,
		tolerances.WindWeightedCn2,
		tolerances.SlantWaterKgM2,
		tolerances.LiquidOpticalDepth,
		tolerances.IceOpticalDepth,
	}
}

// AstrodomeScienceCalibration makes every physical/project assumption and
// numerical stopping scale explicit. Wavelength is fixed to 500 nm for this
// version so downstream wavelength/airmass IQ transformations remain a
// separate, auditable product.
type AstrodomeScienceCalibration struct {
	Version                    string                                  `json:"version"`
	WavelengthM                float64                                 `json:"wavelength_m"`
	DryAirGasConstantJKgK      float64                                 `json:"dry_air_gas_constant_j_kg_k"`
	WaterVapourGasConstantJKgK float64                                 `json:"water_vapour_gas_constant_j_kg_k"`
	LiquidDensityKgM3          float64                                 `json:"liquid_density_kg_m3"`
	IceDensityKgM3             float64                                 `json:"ice_density_kg_m3"`
	LiquidExtinctionEfficiency float64                                 `json:"liquid_extinction_efficiency"`
	IceExtinctionEfficiency    float64                                 `json:"ice_extinction_efficiency"`
	LiquidEffectiveRadiusM     float64                                 `json:"liquid_effective_radius_m"`
	IceEffectiveRadiusM        float64                                 `json:"ice_effective_radius_m"`
	RelativeTolerance          float64                                 `json:"relative_tolerance"`
	AbsoluteTolerance          AstrodomeScienceIntegralTolerances      `json:"absolute_tolerance"`
	MaximumSubdivisions        int                                     `json:"maximum_subdivisions"`
	MaximumDepth               int                                     `json:"maximum_depth"`
	IntegrationVerification    AstrodomeScienceIntegrationVerification `json:"integration_verification"`
	OverallRepeatTolerance     float64                                 `json:"overall_repeat_tolerance"`
	CloudRepeatTolerance       float64                                 `json:"cloud_repeat_tolerance"`
	Overall                    OverallIndexCalibration                 `json:"overall"`
}

// DefaultAstrodomeScienceCalibration is the declared versioned project calibration.
// Absolute tolerances are numerical resolution targets, not observational
// uncertainties and not probabilities.
func DefaultAstrodomeScienceCalibration() AstrodomeScienceCalibration {
	overall := DefaultOverallIndexCalibration()
	return AstrodomeScienceCalibration{
		Version:                    AstrodomeScienceVersion,
		WavelengthM:                seeingWavelengthM,
		DryAirGasConstantJKgK:      287.05,
		WaterVapourGasConstantJKgK: 461.5,
		LiquidDensityKgM3:          1000,
		IceDensityKgM3:             916.7,
		LiquidExtinctionEfficiency: 2.0,
		IceExtinctionEfficiency:    2.1,
		LiquidEffectiveRadiusM:     overall.CloudLiquidRadiusMicrometers * 1e-6,
		IceEffectiveRadiusM:        overall.CloudIceRadiusMicrometers * 1e-6,
		RelativeTolerance:          1e-3,
		AbsoluteTolerance: AstrodomeScienceIntegralTolerances{
			IntegratedCn2:      1e-20,
			WindWeightedCn2:    1e-19,
			SlantWaterKgM2:     1e-6,
			LiquidOpticalDepth: 1e-8,
			IceOpticalDepth:    1e-8,
		},
		MaximumSubdivisions:     8192,
		MaximumDepth:            24,
		IntegrationVerification: AstrodomeScienceVerificationEmbedded,
		OverallRepeatTolerance:  0.01,
		CloudRepeatTolerance:    1e-4,
		Overall:                 overall,
	}
}

// AstrodomeScienceCalibrationWithOverall binds the shared operational Overall
// calibration to the otherwise fixed Astrodome physics and numerical contract.
// Cloud effective radii occur in both contracts and are therefore updated as
// one atomic calibration instead of allowing the score and optical-depth
// integrand to diverge.
func AstrodomeScienceCalibrationWithOverall(overall OverallIndexCalibration) (AstrodomeScienceCalibration, error) {
	if err := overall.Validate(); err != nil {
		return AstrodomeScienceCalibration{}, err
	}
	calibration := DefaultAstrodomeScienceCalibration()
	calibration.Overall = overall
	calibration.LiquidEffectiveRadiusM = overall.CloudLiquidRadiusMicrometers * 1e-6
	calibration.IceEffectiveRadiusM = overall.CloudIceRadiusMicrometers * 1e-6
	if err := calibration.Validate(); err != nil {
		return AstrodomeScienceCalibration{}, err
	}
	return calibration, nil
}

// Digest returns the exact cache/provenance identity of the validated complete
// calibration. The struct contains no maps, so encoding/json's field order is
// stable and the resulting SHA-256 is reproducible across the bot and worker.
func (calibration AstrodomeScienceCalibration) Digest() (string, error) {
	if err := calibration.Validate(); err != nil {
		return "", err
	}
	payload, err := json.Marshal(calibration)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(payload)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

// ReferenceAstrodomeScienceCalibration enables the independent half-
// tolerance repeat used to verify production calibration. It deliberately
// shares the current production physical coefficients (whose present lineage
// was introduced in v24); only the numerical verification workload differs,
// and reference results are not published as a separate scientific product.
func ReferenceAstrodomeScienceCalibration() AstrodomeScienceCalibration {
	calibration := DefaultAstrodomeScienceCalibration()
	calibration.IntegrationVerification = AstrodomeScienceVerificationIndependentRepeat
	return calibration
}

func (calibration AstrodomeScienceCalibration) Validate() error {
	if calibration.Version != AstrodomeScienceVersion {
		return fmt.Errorf("unsupported astrodome science calibration %q", calibration.Version)
	}
	values := []float64{
		calibration.WavelengthM, calibration.DryAirGasConstantJKgK,
		calibration.WaterVapourGasConstantJKgK, calibration.LiquidDensityKgM3,
		calibration.IceDensityKgM3, calibration.LiquidExtinctionEfficiency,
		calibration.IceExtinctionEfficiency, calibration.LiquidEffectiveRadiusM,
		calibration.IceEffectiveRadiusM, calibration.RelativeTolerance,
		calibration.OverallRepeatTolerance, calibration.CloudRepeatTolerance,
	}
	absoluteTolerance := calibration.AbsoluteTolerance.vector()
	values = append(values, absoluteTolerance[:]...)
	for _, value := range values {
		if !finite(value) || value <= 0 {
			return fmt.Errorf("astrodome science calibration values must be finite and positive")
		}
	}
	if math.Abs(calibration.WavelengthM-seeingWavelengthM) > 1e-15 {
		return fmt.Errorf("astrodome science v1 wavelength must be 500 nm")
	}
	if calibration.WaterVapourGasConstantJKgK <= calibration.DryAirGasConstantJKgK {
		return fmt.Errorf("water-vapour gas constant must exceed dry-air gas constant")
	}
	if calibration.RelativeTolerance > 0.1 || calibration.OverallRepeatTolerance > 0.1 ||
		calibration.CloudRepeatTolerance > 0.1 {
		return fmt.Errorf("astrodome science numerical tolerances are too loose")
	}
	if calibration.MaximumSubdivisions < 16 || calibration.MaximumDepth < 4 {
		return fmt.Errorf("astrodome science integration limits are too small")
	}
	switch calibration.IntegrationVerification {
	case AstrodomeScienceVerificationEmbedded, AstrodomeScienceVerificationIndependentRepeat:
	default:
		return fmt.Errorf("unsupported astrodome science integration verification %q", calibration.IntegrationVerification)
	}
	if calibration.Overall.PrecipitationDetectMM <= 0 {
		return fmt.Errorf("astrodome science precipitation threshold must be positive")
	}
	if err := calibration.Overall.Validate(); err != nil {
		return err
	}
	if calibration.LiquidEffectiveRadiusM != calibration.Overall.CloudLiquidRadiusMicrometers*1e-6 ||
		calibration.IceEffectiveRadiusM != calibration.Overall.CloudIceRadiusMicrometers*1e-6 {
		return fmt.Errorf("astrodome cloud effective radii must match the shared Overall calibration")
	}
	return nil
}

// AstrodomeScienceIntegralEstimate contains a point estimate and a
// conservative engineering error allowance formed from the embedded rule and
// the independent tighter repeat. It is not described as a rigorous interval.
type AstrodomeScienceIntegralEstimate struct {
	Value                  float64 `json:"value"`
	EstimatedAbsoluteError float64 `json:"estimated_absolute_error"`
}

type AstrodomeScienceSeeingRange struct {
	MinimumArcsec float64 `json:"minimum_arcsec"`
	MaximumArcsec float64 `json:"maximum_arcsec"`
}

type AstrodomeScienceCloudBlock struct {
	HorizontalCellID               string                    `json:"horizontal_cell_id"`
	Tier                           AstrodomeScienceCloudTier `json:"tier"`
	CloudFractionNominal           float64                   `json:"cloud_fraction_nominal"`
	CloudFractionConservative      float64                   `json:"cloud_fraction_conservative"`
	LiquidOpticalDepthNominal      float64                   `json:"liquid_optical_depth_nominal"`
	LiquidOpticalDepthConservative float64                   `json:"liquid_optical_depth_conservative"`
	IceOpticalDepthNominal         float64                   `json:"ice_optical_depth_nominal"`
	IceOpticalDepthConservative    float64                   `json:"ice_optical_depth_conservative"`
	TransmissionNominal            float64                   `json:"transmission_nominal"`
	TransmissionConservative       float64                   `json:"transmission_conservative"`
}

type AstrodomeScienceFactors struct {
	SeeingQuality    float64 `json:"seeing_quality"`
	CoherenceQuality float64 `json:"coherence_quality"`
	Turbulence       float64 `json:"turbulence"`
	Cloud            float64 `json:"cloud"`
	SurfaceWind      float64 `json:"surface_wind"`
	Fog              float64 `json:"fog"`
	Precipitation    float64 `json:"precipitation"`
}

type AstrodomeScienceQualityCategory string

const (
	AstrodomeScienceQualityUnavailable AstrodomeScienceQualityCategory = "unavailable"
	AstrodomeScienceQualityLimited     AstrodomeScienceQualityCategory = "limited"
	AstrodomeScienceQualityUsable      AstrodomeScienceQualityCategory = "usable"
	AstrodomeScienceQualityGood        AstrodomeScienceQualityCategory = "good"
)

// AstrodomeScienceQuality is deterministic input quality, never forecast
// probability. It is reported beside the score and never multiplied into it.
type AstrodomeScienceQuality struct {
	LeadTimeQualityHeuristic float64                         `json:"lead_time_quality_heuristic"`
	GeometryCoverage         float64                         `json:"geometry_coverage"`
	TurbulencePathCoverage   float64                         `json:"turbulence_path_coverage"`
	CloudPathCoverage        float64                         `json:"cloud_path_coverage"`
	HumidityPathCoverage     *float64                        `json:"humidity_path_coverage,omitempty"`
	TemporalResolutionHours  float64                         `json:"temporal_resolution_hours"`
	QuadratureConverged      bool                            `json:"quadrature_converged"`
	ApproximationLengthM     float64                         `json:"approximation_length_m"`
	TopClosed                bool                            `json:"top_closed"`
	Category                 AstrodomeScienceQualityCategory `json:"category"`
}

type AstrodomeScienceAttribution struct {
	Geometry        string   `json:"geometry"`
	Turbulence      string   `json:"turbulence"`
	TransverseWind  string   `json:"transverse_wind"`
	Density         string   `json:"density"`
	CloudClosure    string   `json:"cloud_closure"`
	WaterColumn     string   `json:"water_column"`
	Tropopause      []string `json:"tropopause"`
	NumericalMethod string   `json:"numerical_method"`
	Precipitation   string   `json:"precipitation"`
}

// AstrodomeScienceNumericalError reports versioned numerical diagnostics. In
// the production embedded-adaptive mode the integral members come from the
// embedded high/low quadrature pair and the derived members propagate those
// estimates through cloud closure and Overall. In independent-repeat mode
// they remain direct deltas between the ordinary and half-tolerance passes.
// Neither mode is a probability or an observational confidence interval.
type AstrodomeScienceNumericalError struct {
	OverallAbsolute            float64  `json:"overall_absolute"`
	CloudTransmissionAbsolute  float64  `json:"cloud_transmission_absolute"`
	TurbulenceIntegralRelative *float64 `json:"turbulence_integral_relative,omitempty"`
	IntegratedCn2Relative      *float64 `json:"integrated_cn2_relative,omitempty"`
	WindWeightedCn2Relative    *float64 `json:"wind_weighted_cn2_relative,omitempty"`
}

// AstrodomeScienceNode is one direction/hour result. Pointer fields are
// deliberately nullable. A calm numerical limit has no finite tau0 payload;
// Tau0UnboundedAbove and Tau0State carry that semantic explicitly.
type AstrodomeScienceNode struct {
	Available                     bool                              `json:"available"`
	State                         AstrodomeScienceNodeState         `json:"state"`
	UnavailableReason             string                            `json:"unavailable_reason,omitempty"`
	ScienceVersion                string                            `json:"science_version"`
	SourceIdentity                AstrodomePrimitiveVolumeIdentity  `json:"source_identity"`
	ValidAt                       time.Time                         `json:"valid_at"`
	ElevationDegrees              float64                           `json:"elevation_deg"`
	AzimuthDegrees                *float64                          `json:"azimuth_deg,omitempty"`
	GeometryMode                  string                            `json:"geometry_mode"`
	RayGeometryVersion            string                            `json:"ray_geometry_version"`
	RefractionVersion             string                            `json:"refraction_version,omitempty"`
	RefractivityVersion           string                            `json:"refractivity_version,omitempty"`
	DirectionAtModelTopECEF       *AstrodomeECEFVector              `json:"direction_at_model_top_ecef,omitempty"`
	IntegratedCn2                 *AstrodomeScienceIntegralEstimate `json:"integrated_cn2,omitempty"`
	WindWeightedCn2               *AstrodomeScienceIntegralEstimate `json:"wind_weighted_cn2,omitempty"`
	SlantWaterKgM2                *AstrodomeScienceIntegralEstimate `json:"slant_water_kg_m2,omitempty"`
	LiquidOpticalDepth            *AstrodomeScienceIntegralEstimate `json:"liquid_optical_depth,omitempty"`
	IceOpticalDepth               *AstrodomeScienceIntegralEstimate `json:"ice_optical_depth,omitempty"`
	Seeing500Arcsec               *float64                          `json:"seeing_500nm_arcsec,omitempty"`
	Seeing500Range                *AstrodomeScienceSeeingRange      `json:"seeing_500nm_range,omitempty"`
	Tau0500MS                     *float64                          `json:"tau0_500nm_ms,omitempty"`
	Tau0ConservativeScoreMS       *float64                          `json:"tau0_conservative_score_ms,omitempty"`
	Tau0UnboundedAbove            bool                              `json:"tau0_unbounded_above"`
	Tau0State                     string                            `json:"tau0_state"`
	CloudTransmissionNominal      *float64                          `json:"cloud_transmission_nominal,omitempty"`
	CloudTransmissionConservative *float64                          `json:"cloud_transmission_conservative,omitempty"`
	// CloudTransmission is the conservative value retained as a temporary
	// compatibility alias for internal consumers of the v26 field name.
	CloudTransmission      *float64                        `json:"cloud_transmission,omitempty"`
	CloudClosureState      string                          `json:"cloud_closure_state,omitempty"`
	CloudClosureBranch     string                          `json:"cloud_closure_branch,omitempty"`
	CloudBlocks            []AstrodomeScienceCloudBlock    `json:"cloud_blocks,omitempty"`
	Overall                *float64                        `json:"overall,omitempty"`
	Factors                *AstrodomeScienceFactors        `json:"factors,omitempty"`
	PenaltyContributions   []OverallPenaltyContribution    `json:"penalty_contributions,omitempty"`
	NumericalError         *AstrodomeScienceNumericalError `json:"numerical_error,omitempty"`
	Quality                AstrodomeScienceQuality         `json:"quality"`
	Attribution            AstrodomeScienceAttribution     `json:"attribution"`
	QuadratureEvaluations  int                             `json:"quadrature_evaluations"`
	QuadratureSubdivisions int                             `json:"quadrature_subdivisions"`
}

type astrodomeScienceAtomicInterval struct {
	startM                 float64
	endM                   float64
	cellID                 string
	startNumericalBoundary bool
	endNumericalBoundary   bool
	certifiedShort         *AstrodomeScienceCertifiedShortInterval
}

func astrodomeScienceMinimumIntervalLength(interval astrodomeScienceAtomicInterval) float64 {
	if interval.certifiedShort != nil {
		// The explicit evidence-aware verifier replaces the ordinary fixed
		// endpoint guard. Only the standard non-extrapolatory G7/K15 and positive-
		// weight Gauss--Legendre pairs remain publishable; a certified interval
		// that is still too short for them fails closed.
		return 0
	}
	physicalBoundaries := 0
	if !interval.startNumericalBoundary {
		physicalBoundaries++
	}
	if !interval.endNumericalBoundary {
		physicalBoundaries++
	}
	switch physicalBoundaries {
	case 2:
		return AstrodomeScienceMinimumEventIntervalLengthM
	case 1:
		return AstrodomeScienceCompoundRootSideGuardM * (1 + 1e-12)
	default:
		return AstrodomeScienceMinimumPartitionLengthM
	}
}

type astrodomeScienceTrajectory interface {
	pointAndTangentAtPathLength(pathLengthM float64) (AstrodomeRayPoint, AstrodomeECEFVector, error)
}

type astrodomeScienceStraightTrajectory struct{ ray AstrodomeRay }

func (trajectory astrodomeScienceStraightTrajectory) pointAndTangentAtPathLength(
	pathLengthM float64,
) (AstrodomeRayPoint, AstrodomeECEFVector, error) {
	point, err := trajectory.ray.pointAtPathLength(pathLengthM)
	return point, trajectory.ray.DirectionECEF, err
}

type astrodomeScienceRefractedTrajectory struct{ ray AstrodomeRefractedRay }

func (trajectory astrodomeScienceRefractedTrajectory) pointAndTangentAtPathLength(
	pathLengthM float64,
) (AstrodomeRayPoint, AstrodomeECEFVector, error) {
	point, err := trajectory.ray.PointAtPathLength(pathLengthM)
	return point.AstrodomeRayPoint, point.TangentECEF, err
}

// ComputeAstrodomeScienceNode performs the complete nonlinear physical
// calculation at one UTC forecast hour. It reconstructs only native
// primitives at quadrature nodes; no derived quantity is spatially or
// temporally interpolated.
func ComputeAstrodomeScienceNode(
	ctx context.Context,
	reconstructor *AstrodomePrimitiveReconstructor,
	ray AstrodomeRay,
	validAt time.Time,
	path AstrodomeSciencePath,
	site AstrodomeScienceSiteInputs,
	calibration AstrodomeScienceCalibration,
) (AstrodomeScienceNode, error) {
	if err := ray.validate(); err != nil {
		return AstrodomeScienceNode{}, err
	}
	if ray.GeometryVersion != AstrodomeGeometryVersion {
		return AstrodomeScienceNode{}, fmt.Errorf("astrodome straight compatibility ray has an unsupported geometry version")
	}
	return computeAstrodomeScienceNode(
		ctx, reconstructor, astrodomeScienceStraightTrajectory{ray: ray}, ray.ElevationDegrees,
		ray.AzimuthDegrees, AstrodomeScienceGeometryStraight, ray.GeometryVersion, "", "", nil,
		validAt, path, site, calibration,
	)
}

// ComputeAstrodomeScienceNodeRefracted applies the same physical kernel to a
// full Ciddor/Dormand--Prince trajectory. Every quadrature evaluation obtains
// its own curved-ray tangent, so V_perp and JV are not evaluated with the
// observer tangent or a 10-degree airmass multiplier.
func ComputeAstrodomeScienceNodeRefracted(
	ctx context.Context,
	reconstructor *AstrodomePrimitiveReconstructor,
	ray AstrodomeRefractedRay,
	validAt time.Time,
	path AstrodomeSciencePath,
	site AstrodomeScienceSiteInputs,
	calibration AstrodomeScienceCalibration,
) (AstrodomeScienceNode, error) {
	if err := ray.validate(); err != nil {
		return AstrodomeScienceNode{}, err
	}
	if len(path.Cells) == 0 || math.Abs(path.Cells[len(path.Cells)-1].EndPathM-ray.PathLengthM) > 1e-3 {
		return AstrodomeScienceNode{}, fmt.Errorf("refracted science path does not close at the traced ICON-top event")
	}
	topDirection := ray.DirectionAtICONTopECEF
	return computeAstrodomeScienceNode(
		ctx, reconstructor, astrodomeScienceRefractedTrajectory{ray: ray}, ray.VisibleElevationDegrees,
		ray.VisibleAzimuthDegrees, AstrodomeScienceGeometryRefractionFull, ray.GeometryVersion,
		ray.IntegratorVersion, ray.RefractivityVersion, &topDirection,
		validAt, path, site, calibration,
	)
}

func computeAstrodomeScienceNode(
	ctx context.Context,
	reconstructor *AstrodomePrimitiveReconstructor,
	trajectory astrodomeScienceTrajectory,
	elevationDegrees float64,
	azimuthDegrees *float64,
	geometryMode string,
	rayGeometryVersion string,
	refractionVersion string,
	refractivityVersion string,
	directionAtModelTop *AstrodomeECEFVector,
	validAt time.Time,
	path AstrodomeSciencePath,
	site AstrodomeScienceSiteInputs,
	calibration AstrodomeScienceCalibration,
) (AstrodomeScienceNode, error) {
	node := AstrodomeScienceNode{
		State:                   AstrodomeScienceNodeUnavailable,
		ScienceVersion:          AstrodomeScienceVersion,
		ValidAt:                 validAt.UTC(),
		ElevationDegrees:        elevationDegrees,
		AzimuthDegrees:          cloneAstrodomeFloatPointer(azimuthDegrees),
		GeometryMode:            geometryMode,
		RayGeometryVersion:      rayGeometryVersion,
		RefractionVersion:       refractionVersion,
		RefractivityVersion:     refractivityVersion,
		DirectionAtModelTopECEF: directionAtModelTop,
		Tau0State:               "unavailable",
		Attribution:             astrodomeScienceAttribution(geometryMode),
	}
	if reconstructor == nil {
		return node, fmt.Errorf("astrodome primitive reconstructor is required")
	}
	node.SourceIdentity = reconstructor.identity
	if err := ctx.Err(); err != nil {
		return node, err
	}
	if err := reconstructor.ensureIdentity(); err != nil {
		return node, err
	}
	if path.GeometryMode != geometryMode {
		return node, fmt.Errorf("astrodome science path geometry %q does not match trajectory %q", path.GeometryMode, geometryMode)
	}
	if err := validateAstrodomeUTCWholeHour(validAt); err != nil {
		return node, fmt.Errorf("astrodome science valid time: %w", err)
	}
	validAt = validAt.UTC()
	if path.ContractVersion != AstrodomeSciencePathContractVersion ||
		!astrodomePrimitiveIdentityEqual(path.SourceIdentity, reconstructor.identity) || !path.ValidAt.Equal(validAt) {
		return node, fmt.Errorf("astrodome science path is not bound to the immutable primitive run and valid time")
	}
	if !astrodomePrimitiveIdentityEqual(site.SourceIdentity, reconstructor.identity) || !site.ValidAt.Equal(validAt) {
		return node, fmt.Errorf("astrodome science site inputs are not bound to the immutable primitive run and valid time")
	}
	if err := calibration.Validate(); err != nil {
		return node, err
	}
	node.Attribution.NumericalMethod = astrodomeScienceNumericalMethod(calibration.IntegrationVerification)
	if err := validateAstrodomeScienceSite(site, validAt); err != nil {
		return node, err
	}
	intervals, err := prepareAstrodomeSciencePath(path)
	if err != nil {
		node.UnavailableReason = err.Error()
		if errors.Is(err, ErrAstrodomeScienceTerrainBlocked) {
			node.State = AstrodomeScienceNodeTerrainBlocked
			node.Quality = astrodomeScienceQuality(path, site.ForecastLeadHours, false, false, path.ApproximationLengthM)
			return node, nil
		}
		return node, err
	}
	node.Quality = astrodomeScienceQuality(path, site.ForecastLeadHours, false, false, path.ApproximationLengthM)

	evaluator := newAstrodomeScienceEvaluator(
		reconstructor, trajectory, validAt, path.NativeContext, path.ShortIntervalVerifier, calibration,
	)
	primary, err := integrateAstrodomeSciencePath(ctx, evaluator.evaluate, intervals, calibration, 1)
	if err != nil {
		node.UnavailableReason = err.Error()
		return node, err
	}
	primaryDerived, err := deriveAstrodomeSciencePass(primary, site, calibration)
	if err != nil {
		return node, err
	}
	publishedPass := primary
	publishedDerived := primaryDerived
	estimates := primary.errors
	numericalError, err := astrodomeScienceEmbeddedNumericalError(primary, primaryDerived, site, calibration)
	if err != nil {
		return node, err
	}
	node.QuadratureSubdivisions = primary.subdivisions
	if calibration.IntegrationVerification == AstrodomeScienceVerificationIndependentRepeat {
		reference, repeatErr := integrateAstrodomeSciencePath(ctx, evaluator.evaluate, intervals, calibration, 0.5)
		if repeatErr != nil {
			node.UnavailableReason = repeatErr.Error()
			return node, repeatErr
		}
		node.QuadratureSubdivisions += reference.subdivisions
		referenceDerived, deriveErr := deriveAstrodomeSciencePass(reference, site, calibration)
		if deriveErr != nil {
			return node, deriveErr
		}
		if repeatErr := verifyAstrodomeScienceRepeat(primary, reference, primaryDerived, referenceDerived, calibration); repeatErr != nil {
			node.UnavailableReason = repeatErr.Error()
			node.Quality = astrodomeScienceQuality(path, site.ForecastLeadHours, false, false, path.ApproximationLengthM)
			return node, repeatErr
		}
		estimates = astrodomeScienceConservativeEstimates(primary, reference)
		publishedPass = reference
		publishedPass.errors = estimates
		publishedDerived, err = deriveAstrodomeSciencePass(publishedPass, site, calibration)
		if err != nil {
			return node, err
		}
		if math.Abs(publishedDerived.overall-referenceDerived.overall) > calibration.OverallRepeatTolerance {
			err = fmt.Errorf("%w: conservative repeat allowance changes Overall by %.6g", ErrAstrodomeScienceNonConvergence, math.Abs(publishedDerived.overall-referenceDerived.overall))
			node.UnavailableReason = err.Error()
			node.Quality = astrodomeScienceQuality(path, site.ForecastLeadHours, false, false, path.ApproximationLengthM)
			return node, err
		}
		numericalError = astrodomeScienceNumericalError(primary, reference, primaryDerived, referenceDerived, calibration)
	}
	node.QuadratureEvaluations = evaluator.evaluations
	node.Attribution.Tropopause = evaluator.tropopauseAttribution()
	if err := reconstructor.ensureIdentity(); err != nil {
		return node, err
	}

	node.NumericalError = numericalError
	node.IntegratedCn2 = &AstrodomeScienceIntegralEstimate{Value: publishedPass.values[astrodomeScienceCn2Index], EstimatedAbsoluteError: estimates[astrodomeScienceCn2Index]}
	node.WindWeightedCn2 = &AstrodomeScienceIntegralEstimate{Value: publishedPass.values[astrodomeScienceWindCn2Index], EstimatedAbsoluteError: estimates[astrodomeScienceWindCn2Index]}
	if path.Availability.Humidity {
		node.SlantWaterKgM2 = &AstrodomeScienceIntegralEstimate{Value: publishedPass.values[astrodomeScienceWaterIndex], EstimatedAbsoluteError: estimates[astrodomeScienceWaterIndex]}
	}
	node.LiquidOpticalDepth = &AstrodomeScienceIntegralEstimate{Value: publishedPass.values[astrodomeScienceLiquidExtinctionIndex], EstimatedAbsoluteError: estimates[astrodomeScienceLiquidExtinctionIndex]}
	node.IceOpticalDepth = &AstrodomeScienceIntegralEstimate{Value: publishedPass.values[astrodomeScienceIceExtinctionIndex], EstimatedAbsoluteError: estimates[astrodomeScienceIceExtinctionIndex]}

	seeing := seeingArcsecFromIntegratedCn2(math.Max(0, publishedPass.values[astrodomeScienceCn2Index]))
	seeingMinimum := seeingArcsecFromIntegratedCn2(math.Max(0, publishedPass.values[astrodomeScienceCn2Index]-estimates[astrodomeScienceCn2Index]))
	seeingMaximum := seeingArcsecFromIntegratedCn2(math.Max(0, publishedPass.values[astrodomeScienceCn2Index]+estimates[astrodomeScienceCn2Index]))
	node.Seeing500Arcsec = &seeing
	node.Seeing500Range = &AstrodomeScienceSeeingRange{MinimumArcsec: seeingMinimum, MaximumArcsec: seeingMaximum}

	jv := math.Max(0, publishedPass.values[astrodomeScienceWindCn2Index])
	jvError := estimates[astrodomeScienceWindCn2Index]
	calm := math.Max(0, jv-jvError) <= calibration.AbsoluteTolerance.WindWeightedCn2
	if calm {
		node.Tau0State = "calm_numerical_limit"
		node.Tau0UnboundedAbove = true
	} else {
		tau := astrodomeTau0MS(jv, calibration.WavelengthM)
		node.Tau0500MS = &tau
		node.Tau0State = "finite"
	}
	jvConservative := math.Max(0, jv+jvError)
	if jvConservative > 0 {
		tau := astrodomeTau0MS(jvConservative, calibration.WavelengthM)
		node.Tau0ConservativeScoreMS = &tau
	}
	node.CloudTransmissionNominal = &publishedDerived.cloudTransmissionNominal
	node.CloudTransmissionConservative = &publishedDerived.cloudTransmissionConservative
	node.CloudTransmission = node.CloudTransmissionConservative
	node.CloudBlocks = publishedDerived.cloudBlocks
	node.CloudClosureState = publishedDerived.cloudState
	node.CloudClosureBranch = publishedDerived.cloudBranch
	node.Overall = &publishedDerived.overall
	node.Factors = &publishedDerived.factors
	node.PenaltyContributions = publishedDerived.penaltyContributions
	approximationLengthM := path.ApproximationLengthM + publishedPass.approximationLengthM
	if !finite(approximationLengthM) || approximationLengthM < 0 ||
		approximationLengthM > AstrodomeScienceMaximumApproximatePathLengthM {
		return node, fmt.Errorf(
			"%w: cumulative approximate path %.9g m exceeds the %.9g m publication ceiling",
			ErrAstrodomeScienceNonConvergence,
			approximationLengthM,
			AstrodomeScienceMaximumApproximatePathLengthM,
		)
	}
	node.Quality = astrodomeScienceQuality(
		path, site.ForecastLeadHours, true, approximationLengthM == 0, approximationLengthM,
	)
	node.Available = true
	node.State = AstrodomeScienceNodeAvailable
	for _, cell := range path.Cells {
		if cell.TerrainState == AstrodomeScienceTerrainGrazing {
			node.State = AstrodomeScienceNodeTerrainGrazing
			break
		}
	}
	return node, nil
}

func validateAstrodomeScienceSite(site AstrodomeScienceSiteInputs, validAt time.Time) error {
	values := []float64{site.WindSpeed10MMS, site.WindGust10MMS, site.PrecipitationRateMMPerHour, site.ForecastLeadHours}
	for _, value := range values {
		if !finite(value) || value < 0 {
			return fmt.Errorf("astrodome science site inputs must be finite and non-negative")
		}
	}
	_, startOffset := site.PrecipitationIntervalStart.Zone()
	_, endOffset := site.PrecipitationIntervalEnd.Zone()
	if startOffset != 0 || endOffset != 0 ||
		!site.PrecipitationIntervalEnd.After(site.PrecipitationIntervalStart) ||
		site.PrecipitationIntervalEnd.Sub(site.PrecipitationIntervalStart) != time.Hour ||
		!site.PrecipitationIntervalEnd.Equal(validAt) {
		return fmt.Errorf("astrodome precipitation needs the explicit differenced one-hour UTC accumulation ending at valid time")
	}
	switch site.FogHeuristic {
	case AstrodomeScienceFogUnavailable, AstrodomeScienceFogNone, AstrodomeScienceFogPossible, AstrodomeScienceFogHigh:
	default:
		return fmt.Errorf("unsupported astrodome fog heuristic %q", site.FogHeuristic)
	}
	return nil
}

func prepareAstrodomeSciencePath(path AstrodomeSciencePath) ([]astrodomeScienceAtomicInterval, error) {
	if len(path.Cells) == 0 {
		return nil, fmt.Errorf("astrodome science path has no traced cells")
	}
	if path.NativeContext == nil {
		return nil, fmt.Errorf("astrodome science path has no immutable native-context resolver")
	}
	coverage := []float64{path.GeometryCoverage, path.TurbulencePathCoverage, path.CloudPathCoverage}
	if path.HumidityPathCoverage != nil {
		coverage = append(coverage, *path.HumidityPathCoverage)
	}
	if path.Availability.Humidity != (path.HumidityPathCoverage != nil) {
		return nil, fmt.Errorf("astrodome humidity availability and nullable coverage disagree")
	}
	for _, value := range coverage {
		if !finite(value) || value < 0 || value > 1 {
			return nil, fmt.Errorf("astrodome science path coverage must be between zero and one")
		}
	}
	if !finite(path.TemporalResolutionHours) || path.TemporalResolutionHours <= 0 || path.TemporalResolutionHours > 3 {
		return nil, fmt.Errorf("astrodome science path temporal resolution is unsupported")
	}
	if !finite(path.ApproximationLengthM) || path.ApproximationLengthM < 0 ||
		path.ApproximationLengthM > AstrodomeScienceMaximumApproximatePathLengthM {
		return nil, fmt.Errorf(
			"%w: path approximation length %.9g m exceeds the %.9g m publication ceiling",
			ErrAstrodomeScienceIncompletePartition,
			path.ApproximationLengthM,
			AstrodomeScienceMaximumApproximatePathLengthM,
		)
	}
	if !path.TopClosed {
		return nil, fmt.Errorf("astrodome science path does not close at the model top")
	}
	if !path.Availability.Geometry || !path.Availability.Turbulence || !path.Availability.Cloud ||
		!path.Availability.TemporalBrackets || !path.Availability.Terrain {
		return nil, fmt.Errorf("astrodome science path is missing a mandatory straight-compat primitive panel")
	}

	atomic := make([]astrodomeScienceAtomicInterval, 0, len(path.Cells)*4)
	previousEnd := path.Cells[0].StartPathM
	if !finite(previousEnd) || math.Abs(previousEnd) > 1e-7 {
		return nil, fmt.Errorf("astrodome science path must start at the observer aperture")
	}
	for index, cell := range path.Cells {
		if !finite(cell.StartPathM) || !finite(cell.EndPathM) || cell.EndPathM <= cell.StartPathM ||
			math.Abs(cell.StartPathM-previousEnd) > 1e-6 {
			return nil, fmt.Errorf("astrodome science path cells must be finite, positive, and contiguous at cell %d", index)
		}
		if strings.TrimSpace(cell.HorizontalCellID) == "" {
			return nil, fmt.Errorf("astrodome science path cell %d has no horizontal cell identity", index)
		}
		if !cell.TropopausePredicatesIsolated {
			return nil, fmt.Errorf(
				"%w: cell %q has no certificate that every raw WMO tropopause predicate was isolated",
				ErrAstrodomeScienceIncompletePartition,
				cell.HorizontalCellID,
			)
		}
		if !cell.NativeVerticalPredicatesIsolated {
			return nil, fmt.Errorf(
				"%w: cell %q has no certificate that every native full-level cloud support predicate was isolated",
				ErrAstrodomeScienceIncompletePartition,
				cell.HorizontalCellID,
			)
		}
		switch cell.TerrainState {
		case AstrodomeScienceTerrainClear, AstrodomeScienceTerrainGrazing:
		case AstrodomeScienceTerrainBlocked:
			return nil, fmt.Errorf("%w in cell %q", ErrAstrodomeScienceTerrainBlocked, cell.HorizontalCellID)
		default:
			return nil, fmt.Errorf("astrodome science cell %q has no terrain event state", cell.HorizontalCellID)
		}
		events, eventErr := astrodomeScienceCellEvents(cell, index)
		if eventErr != nil {
			return nil, eventErr
		}
		certificates, certificateErr := astrodomeScienceShortCertificates(path, cell, events)
		if certificateErr != nil {
			return nil, certificateErr
		}
		usedCertificates := make(map[string]struct{}, len(certificates))
		for part := 0; part+1 < len(events); part++ {
			startEvent, endEvent := events[part], events[part+1]
			gapM := endEvent.RepresentativePathM - startEvent.RepresentativePathM
			if gapM <= AstrodomeScienceRootMergeToleranceM {
				return nil, fmt.Errorf("%w: distinct events %q and %q in cell %q are %.9g m apart inside the %.9g m root cluster",
					ErrAstrodomeScienceIncompletePartition, startEvent.EventID, endEvent.EventID,
					cell.HorizontalCellID, gapM, AstrodomeScienceRootMergeToleranceM)
			}
			key := astrodomeScienceEventPairKey(startEvent, endEvent)
			certificate := certificates[key]
			if gapM <= AstrodomeScienceMinimumEventIntervalLengthM && certificate == nil {
				return nil, fmt.Errorf("%w: interval in cell %q does not clear the %.9g m compound-root side guard and has no short-panel certificate",
					ErrAstrodomeScienceIncompletePartition, cell.HorizontalCellID, AstrodomeScienceMinimumEventIntervalLengthM)
			}
			if certificate != nil {
				usedCertificates[certificate.CertificateID] = struct{}{}
			}
			atomic = append(atomic, astrodomeScienceAtomicInterval{
				startM:         startEvent.RepresentativePathM,
				endM:           endEvent.RepresentativePathM,
				cellID:         cell.HorizontalCellID,
				certifiedShort: certificate,
			})
		}
		if len(usedCertificates) != len(cell.CertifiedShortIntervals) {
			return nil, fmt.Errorf("%w: cell %q contains an unused short-panel certificate",
				ErrAstrodomeScienceIncompletePartition, cell.HorizontalCellID)
		}
		previousEnd = cell.EndPathM
	}
	return atomic, nil
}

type astrodomeScienceEvidencePairKey struct {
	start uint64
	end   uint64
}

func astrodomeScienceEventPairKey(
	start AstrodomeScienceRootEvidence,
	end AstrodomeScienceRootEvidence,
) astrodomeScienceEvidencePairKey {
	return astrodomeScienceEvidencePairKey{
		start: math.Float64bits(start.RepresentativePathM),
		end:   math.Float64bits(end.RepresentativePathM),
	}
}

func astrodomeScienceCellEvents(
	cell AstrodomeSciencePathCell,
	cellIndex int,
) ([]AstrodomeScienceRootEvidence, error) {
	start := AstrodomeScienceRootEvidence{
		RepresentativePathM: cell.StartPathM,
		LeftPathM:           cell.StartPathM,
		RightPathM:          cell.StartPathM,
		EventID:             fmt.Sprintf("cell/%d/start", cellIndex),
		Kind:                "legacy-cell-boundary",
		Exact:               true,
	}
	end := AstrodomeScienceRootEvidence{
		RepresentativePathM: cell.EndPathM,
		LeftPathM:           cell.EndPathM,
		RightPathM:          cell.EndPathM,
		EventID:             fmt.Sprintf("cell/%d/end", cellIndex),
		Kind:                "legacy-cell-boundary",
		Exact:               true,
	}
	if cell.StartEvidence != nil {
		start = *cell.StartEvidence
	}
	if cell.EndEvidence != nil {
		end = *cell.EndEvidence
	}
	if math.Float64bits(start.RepresentativePathM) != math.Float64bits(cell.StartPathM) ||
		math.Float64bits(end.RepresentativePathM) != math.Float64bits(cell.EndPathM) {
		return nil, fmt.Errorf("%w: retained cell-boundary evidence does not match cell %q",
			ErrAstrodomeScienceIncompletePartition, cell.HorizontalCellID)
	}
	events := make([]AstrodomeScienceRootEvidence, 0, len(cell.BreakpointsPathM)+2)
	events = append(events, start)
	if len(cell.BreakpointEvidence) > 0 {
		if len(cell.BreakpointsPathM) != len(cell.BreakpointEvidence) {
			return nil, fmt.Errorf("%w: physical breakpoint values and evidence disagree in cell %q",
				ErrAstrodomeScienceIncompletePartition, cell.HorizontalCellID)
		}
		for index, evidence := range cell.BreakpointEvidence {
			if math.Float64bits(evidence.RepresentativePathM) != math.Float64bits(cell.BreakpointsPathM[index]) {
				return nil, fmt.Errorf("%w: physical breakpoint evidence order disagrees in cell %q",
					ErrAstrodomeScienceIncompletePartition, cell.HorizontalCellID)
			}
			events = append(events, evidence)
		}
	} else {
		for index, value := range cell.BreakpointsPathM {
			events = append(events, AstrodomeScienceRootEvidence{
				RepresentativePathM: value,
				LeftPathM:           value,
				RightPathM:          value,
				EventID:             fmt.Sprintf("cell/%d/legacy-breakpoint/%d", cellIndex, index),
				Kind:                "legacy-physical",
				Exact:               true,
			})
		}
	}
	events = append(events, end)
	for index := range events {
		if err := validateAstrodomeScienceRootEvidence(events[index], cell.StartPathM, cell.EndPathM); err != nil {
			return nil, fmt.Errorf("cell %q: %w", cell.HorizontalCellID, err)
		}
	}
	sort.Slice(events, func(left, right int) bool {
		return events[left].RepresentativePathM < events[right].RepresentativePathM
	})
	for index := 1; index < len(events); index++ {
		if events[index].RepresentativePathM <= events[index-1].RepresentativePathM {
			return nil, fmt.Errorf("%w: non-ordered or coincident retained events %q and %q in cell %q",
				ErrAstrodomeScienceIncompletePartition, events[index-1].EventID, events[index].EventID,
				cell.HorizontalCellID)
		}
	}
	return events, nil
}

func validateAstrodomeScienceRootEvidence(
	evidence AstrodomeScienceRootEvidence,
	cellStartM, cellEndM float64,
) error {
	if !finite(evidence.RepresentativePathM) || !finite(evidence.LeftPathM) || !finite(evidence.RightPathM) ||
		evidence.LeftPathM > evidence.RepresentativePathM || evidence.RepresentativePathM > evidence.RightPathM ||
		evidence.RepresentativePathM < cellStartM || evidence.RepresentativePathM > cellEndM ||
		strings.TrimSpace(evidence.EventID) == "" || strings.TrimSpace(evidence.Kind) == "" {
		return fmt.Errorf("%w: malformed retained root evidence", ErrAstrodomeScienceIncompletePartition)
	}
	if evidence.Exact {
		if math.Float64bits(evidence.LeftPathM) != math.Float64bits(evidence.RepresentativePathM) ||
			math.Float64bits(evidence.RightPathM) != math.Float64bits(evidence.RepresentativePathM) {
			return fmt.Errorf("%w: exact event %q has a nonzero evidence enclosure",
				ErrAstrodomeScienceIncompletePartition, evidence.EventID)
		}
		return nil
	}
	if math.Max(evidence.RepresentativePathM-evidence.LeftPathM,
		evidence.RightPathM-evidence.RepresentativePathM) > AstrodomeScienceRootToleranceM {
		return fmt.Errorf("%w: event %q exceeds the %.9g m root-evidence radius",
			ErrAstrodomeScienceIncompletePartition, evidence.EventID, AstrodomeScienceRootToleranceM)
	}
	return nil
}

func astrodomeScienceShortCertificates(
	path AstrodomeSciencePath,
	cell AstrodomeSciencePathCell,
	events []AstrodomeScienceRootEvidence,
) (map[astrodomeScienceEvidencePairKey]*AstrodomeScienceCertifiedShortInterval, error) {
	result := make(map[astrodomeScienceEvidencePairKey]*AstrodomeScienceCertifiedShortInterval, len(cell.CertifiedShortIntervals))
	ids := make(map[string]struct{}, len(cell.CertifiedShortIntervals))
	for index := range cell.CertifiedShortIntervals {
		certificate := cell.CertifiedShortIntervals[index]
		if path.ShortIntervalVerifier == nil || strings.TrimSpace(certificate.CertificateID) == "" ||
			certificate.HorizontalCellID != cell.HorizontalCellID {
			return nil, fmt.Errorf("%w: malformed short-panel certificate in cell %q",
				ErrAstrodomeScienceIncompletePartition, cell.HorizontalCellID)
		}
		if _, exists := ids[certificate.CertificateID]; exists {
			return nil, fmt.Errorf("%w: duplicate short-panel certificate %q",
				ErrAstrodomeScienceIncompletePartition, certificate.CertificateID)
		}
		ids[certificate.CertificateID] = struct{}{}
		if err := validateAstrodomeScienceRootEvidence(certificate.Start, cell.StartPathM, cell.EndPathM); err != nil {
			return nil, err
		}
		if err := validateAstrodomeScienceRootEvidence(certificate.End, cell.StartPathM, cell.EndPathM); err != nil {
			return nil, err
		}
		key := astrodomeScienceEventPairKey(certificate.Start, certificate.End)
		if _, exists := result[key]; exists {
			return nil, fmt.Errorf("%w: multiple certificates cover one short interval in cell %q",
				ErrAstrodomeScienceIncompletePartition, cell.HorizontalCellID)
		}
		matched := false
		for event := 0; event+1 < len(events); event++ {
			if astrodomeScienceEventPairKey(events[event], events[event+1]) != key {
				continue
			}
			if !astrodomeScienceRootEvidenceEqual(certificate.Start, events[event]) ||
				!astrodomeScienceRootEvidenceEqual(certificate.End, events[event+1]) {
				return nil, fmt.Errorf("%w: certificate %q does not reproduce retained endpoint evidence",
					ErrAstrodomeScienceIncompletePartition, certificate.CertificateID)
			}
			matched = true
			break
		}
		gapM := certificate.End.RepresentativePathM - certificate.Start.RepresentativePathM
		if !matched || gapM <= AstrodomeScienceRootMergeToleranceM ||
			gapM > AstrodomeScienceMinimumEventIntervalLengthM ||
			!finite(certificate.OpenSafeStartPathM) || !finite(certificate.OpenSafeEndPathM) ||
			certificate.OpenSafeStartPathM <= certificate.Start.RightPathM ||
			certificate.OpenSafeEndPathM >= certificate.End.LeftPathM ||
			certificate.OpenSafeStartPathM >= certificate.OpenSafeEndPathM {
			return nil, fmt.Errorf("%w: certificate %q has no valid open evidence-aware safe interval",
				ErrAstrodomeScienceIncompletePartition, certificate.CertificateID)
		}
		left := math.Nextafter(certificate.OpenSafeStartPathM, math.Inf(1))
		right := math.Nextafter(certificate.OpenSafeEndPathM, math.Inf(-1))
		if astrodomeScienceRepresentablePathCapacity(left, right) < 2 {
			return nil, fmt.Errorf("%w: certificate %q has no representable open safe interval",
				ErrAstrodomeScienceIncompletePartition, certificate.CertificateID)
		}
		copy := certificate
		result[key] = &copy
	}
	return result, nil
}

func astrodomeScienceRootEvidenceEqual(left, right AstrodomeScienceRootEvidence) bool {
	return math.Float64bits(left.RepresentativePathM) == math.Float64bits(right.RepresentativePathM) &&
		math.Float64bits(left.LeftPathM) == math.Float64bits(right.LeftPathM) &&
		math.Float64bits(left.RightPathM) == math.Float64bits(right.RightPathM) &&
		left.EventID == right.EventID && left.Kind == right.Kind && left.Exact == right.Exact
}

func compactAstrodomeScienceBreakpoints(values []float64) ([]float64, error) {
	result := values[:0]
	for _, value := range values {
		if len(result) == 0 || value-result[len(result)-1] > AstrodomeScienceRootMergeToleranceM {
			result = append(result, value)
			continue
		}
		if value == result[len(result)-1] {
			continue
		}
		return nil, fmt.Errorf("%w: anonymous non-identical breakpoints are %.9g m apart inside the %.9g m root cluster",
			ErrAstrodomeScienceIncompletePartition, value-result[len(result)-1], AstrodomeScienceRootMergeToleranceM)
	}
	return result, nil
}

func astrodomeScienceTropopause(profile []AstrodomeScienceThermalPrimitive) (float64, string, error) {
	height, method, _, _, _, err := astrodomeScienceTropopauseBoundary(profile)
	return height, method, err
}

func astrodomeScienceTropopauseBoundary(
	profile []AstrodomeScienceThermalPrimitive,
) (float64, string, AstrodomeScienceTropopauseBoundaryKind, int, int, error) {
	if len(profile) == 0 {
		return math.NaN(), "HMNSP99 pressure<200-hPa fallback (thermal profile unavailable)",
			AstrodomeScienceTropopauseBoundaryNone, -1, -1, nil
	}
	levels := make([]VerticalLevel, len(profile))
	for index, level := range profile {
		if !finite(level.HeightM) || !finite(level.PressurePa) || level.PressurePa <= 0 || !validProfileTemperature(level.TemperatureK) {
			return 0, "", "", -1, -1, fmt.Errorf("invalid native P/T primitive at level %d", index)
		}
		if index > 0 && level.HeightM <= profile[index-1].HeightM {
			return 0, "", "", -1, -1, fmt.Errorf("native thermal profile is not bottom-to-top ordered")
		}
		levels[index] = VerticalLevel{HeightM: level.HeightM, PressureHPA: level.PressurePa / 100, TemperatureK: level.TemperatureK}
	}
	if levelIndex := thermalTropopauseLevelIndex(levels); levelIndex >= 0 {
		return levels[levelIndex].HeightM, "WMO lapse-rate thermal tropopause from reconstructed native P/T",
			AstrodomeScienceTropopauseBoundaryWMOLevel, levelIndex, levelIndex, nil
	}
	if fallbackHeight, lowerLevel := astrodomeSciencePressureBoundary(profile, 20000); finite(fallbackHeight) {
		return fallbackHeight, "HMNSP99 reconstructed 200-hPa fallback boundary",
			AstrodomeScienceTropopauseBoundaryPressureFallback, lowerLevel, lowerLevel + 1, nil
	}
	return math.NaN(), "HMNSP99 pressure<200-hPa fallback (thermal profile does not bracket 200 hPa)",
		AstrodomeScienceTropopauseBoundaryNone, -1, -1, nil
}

func astrodomeSciencePressureBoundary(
	profile []AstrodomeScienceThermalPrimitive,
	targetPressurePa float64,
) (float64, int) {
	if !finite(targetPressurePa) || targetPressurePa <= 0 {
		return math.NaN(), -1
	}
	for index := 0; index+1 < len(profile); index++ {
		lower, upper := profile[index], profile[index+1]
		if !finite(lower.HeightM) || !finite(upper.HeightM) || upper.HeightM <= lower.HeightM ||
			!finite(lower.PressurePa) || !finite(upper.PressurePa) || lower.PressurePa <= 0 || upper.PressurePa <= 0 {
			return math.NaN(), -1
		}
		lowerResidual := CompensatedDifferenceResidual(lower.PressurePa, 0, targetPressurePa)
		upperResidual := CompensatedDifferenceResidual(upper.PressurePa, 0, targetPressurePa)
		if lowerResidual == 0 {
			return lower.HeightM, index
		}
		if upperResidual == 0 {
			return upper.HeightM, index
		}
		if (lowerResidual < 0) == (upperResidual < 0) {
			continue
		}
		// log1p preserves the small relative pressure differences near the
		// target and between adjacent levels. The mathematically equivalent
		// difference of two large logarithms loses most significant bits when
		// the native bracket is narrow.
		numerator := math.Log1p((lower.PressurePa - targetPressurePa) / targetPressurePa)
		denominator := math.Log1p((lower.PressurePa - upper.PressurePa) / upper.PressurePa)
		if denominator == 0 || !finite(numerator) || !finite(denominator) {
			return math.NaN(), -1
		}
		fraction := numerator / denominator
		height := lower.HeightM + fraction*(upper.HeightM-lower.HeightM)
		if finite(height) && fraction >= 0 && fraction <= 1 {
			return height, index
		}
		return math.NaN(), -1
	}
	return math.NaN(), -1
}

func astrodomeScienceTier(heightAGLM float64) AstrodomeScienceCloudTier {
	if heightAGLM < AstrodomeScienceCloudLowTopAGLM {
		return AstrodomeScienceCloudLow
	}
	if heightAGLM < AstrodomeScienceCloudMiddleTopAGLM {
		return AstrodomeScienceCloudMiddle
	}
	return AstrodomeScienceCloudHigh
}

type astrodomeScienceEvaluator struct {
	reconstructor     *AstrodomePrimitiveReconstructor
	trajectory        astrodomeScienceTrajectory
	validAt           time.Time
	nativeContext     AstrodomeScienceNativeContextResolver
	shortVerifier     AstrodomeScienceShortIntervalVerifier
	calibration       AstrodomeScienceCalibration
	cache             map[uint64]AstrodomeReconstructedAtmosphere
	nativeCache       map[uint64]AstrodomeScienceNativeContext
	cloudEnvelopes    map[astrodomeScienceCloudEnvelopeKey]astrodomeScienceCloudFractionEnvelope
	tropopauseMethods map[string]struct{}
	evaluations       int
}

type astrodomeScienceEvaluation struct {
	values                         astrodomeScienceVector
	cloudFractionNominal           float64
	cloudFractionConservativeUpper float64
	blockKey                       astrodomeScienceCloudBlockKey
	cloudVerticalSupport           astrodomeCloudFractionVerticalSupport
	regime                         string
}

type astrodomeScienceCloudEnvelopeKey struct {
	blockKey        astrodomeScienceCloudBlockKey
	verticalSupport astrodomeCloudFractionVerticalSupport
}

func newAstrodomeScienceEvaluator(
	reconstructor *AstrodomePrimitiveReconstructor,
	trajectory astrodomeScienceTrajectory,
	validAt time.Time,
	nativeContext AstrodomeScienceNativeContextResolver,
	shortVerifier AstrodomeScienceShortIntervalVerifier,
	calibration AstrodomeScienceCalibration,
) *astrodomeScienceEvaluator {
	return &astrodomeScienceEvaluator{
		reconstructor: reconstructor, trajectory: trajectory, validAt: validAt, nativeContext: nativeContext,
		shortVerifier: shortVerifier,
		calibration:   calibration, cache: make(map[uint64]AstrodomeReconstructedAtmosphere),
		nativeCache:       make(map[uint64]AstrodomeScienceNativeContext),
		cloudEnvelopes:    make(map[astrodomeScienceCloudEnvelopeKey]astrodomeScienceCloudFractionEnvelope),
		tropopauseMethods: make(map[string]struct{}),
	}
}

func (evaluator *astrodomeScienceEvaluator) evaluate(ctx context.Context, pathM float64, interval astrodomeScienceAtomicInterval) (astrodomeScienceEvaluation, error) {
	if interval.certifiedShort != nil {
		if evaluator.shortVerifier == nil {
			return astrodomeScienceEvaluation{}, fmt.Errorf(
				"%w: certified short panel has no node verifier",
				ErrAstrodomeScienceIncompletePartition,
			)
		}
		if err := evaluator.shortVerifier.VerifyAstrodomeScienceShortIntervalNode(
			ctx, evaluator.validAt, pathM, *interval.certifiedShort,
		); err != nil {
			return astrodomeScienceEvaluation{}, fmt.Errorf(
				"%w: short-panel node %.17g failed ownership proof: %w",
				ErrAstrodomeScienceIncompletePartition, pathM, err,
			)
		}
	}
	key := math.Float64bits(pathM)
	point, tangent, err := evaluator.trajectory.pointAndTangentAtPathLength(pathM)
	if err != nil {
		return astrodomeScienceEvaluation{}, err
	}
	state, ok := evaluator.cache[key]
	if !ok {
		state, err = evaluator.reconstructor.Reconstruct(ctx, AstrodomeReconstructionQuery{ValidAt: evaluator.validAt, Location: point.Location, HeightM: point.HeightM})
		if err != nil {
			return astrodomeScienceEvaluation{}, fmt.Errorf("reconstruct astrodome primitive at %.3f m LOS: %w", pathM, err)
		}
		evaluator.cache[key] = state
		evaluator.evaluations++
	}
	native, ok := evaluator.nativeCache[key]
	if !ok {
		native, err = evaluator.nativeContext.ResolveAstrodomeScienceNativeContext(ctx, evaluator.validAt, point, state.HorizontalStencil)
		if err != nil {
			return astrodomeScienceEvaluation{}, fmt.Errorf("resolve astrodome native context at %.3f m LOS: %w", pathM, err)
		}
		evaluator.nativeCache[key] = native
	}
	if native.HorizontalCellID != interval.cellID || !finite(native.SurfaceHeightM) ||
		!finite(native.MixedLayerDepthM) || native.MixedLayerDepthM < 0 {
		return astrodomeScienceEvaluation{}, fmt.Errorf("%w: native context changed outside declared cell %q", ErrAstrodomeScienceIncompletePartition, interval.cellID)
	}
	tropopauseHeightM, tropopauseMethod, err := astrodomeScienceTropopause(native.ThermalProfile)
	if err != nil {
		return astrodomeScienceEvaluation{}, fmt.Errorf("diagnose astrodome thermal tropopause at %.3f m LOS: %w", pathM, err)
	}
	evaluator.tropopauseMethods[tropopauseMethod] = struct{}{}
	values, regime, err := astrodomeScienceIntegrand(state, tangent, native, tropopauseHeightM, evaluator.calibration)
	if err != nil {
		return astrodomeScienceEvaluation{}, fmt.Errorf("astrodome science integrand at %.3f m LOS: %w", pathM, err)
	}
	blockKey := astrodomeScienceCloudBlockKey{
		cellID: interval.cellID,
		tier:   astrodomeScienceTier(state.HeightM - native.SurfaceHeightM),
	}
	verticalSupport := state.cloudFractionSupport
	envelopeKey := astrodomeScienceCloudEnvelopeKey{
		blockKey: blockKey, verticalSupport: verticalSupport,
	}
	supportIDs := astrodomeScienceCloudEnvelopeSupportIDs(state.HorizontalStencil)
	envelope, ok := evaluator.cloudEnvelopes[envelopeKey]
	if ok && envelope.supportIDs != supportIDs {
		return astrodomeScienceEvaluation{}, fmt.Errorf(
			"%w: cloud support changed inside declared cell %q",
			ErrAstrodomeScienceIncompletePartition,
			interval.cellID,
		)
	}
	if !ok {
		envelope, err = evaluator.reconstructor.certifiedCloudFractionUpperEnvelope(
			ctx, evaluator.validAt, state.HorizontalStencil, verticalSupport,
		)
		if err != nil {
			return astrodomeScienceEvaluation{}, fmt.Errorf(
				"certify astrodome cloud-fraction envelope at %.3f m LOS: %w", pathM, err,
			)
		}
		evaluator.cloudEnvelopes[envelopeKey] = envelope
	}
	return astrodomeScienceEvaluation{
		values: values, cloudFractionNominal: state.CloudFraction,
		cloudFractionConservativeUpper: envelope.upperBound,
		blockKey:                       blockKey, cloudVerticalSupport: verticalSupport, regime: regime,
	}, nil
}

func astrodomeScienceCloudEnvelopeSupportIDs(stencil AstrodomeHorizontalStencil) [4]string {
	result := [4]string{}
	for index, support := range stencil.Supports {
		result[index] = support.ColumnID
	}
	sort.Strings(result[:])
	return result
}

func (evaluator *astrodomeScienceEvaluator) tropopauseAttribution() []string {
	methods := make([]string, 0, len(evaluator.tropopauseMethods))
	for method := range evaluator.tropopauseMethods {
		methods = append(methods, method)
	}
	sort.Strings(methods)
	return methods
}

func astrodomeScienceIntegrand(state AstrodomeReconstructedAtmosphere, tangent AstrodomeECEFVector, native AstrodomeScienceNativeContext, tropopauseHeightM float64, calibration AstrodomeScienceCalibration) (astrodomeScienceVector, string, error) {
	values := []float64{state.PressurePa, state.TemperatureK, state.SpecificHumidityKgKg, state.CloudLiquidKgKg, state.CloudIceKgKg, state.CloudFraction, state.TKEJkg}
	for _, value := range values {
		if !finite(value) {
			return astrodomeScienceVector{}, "", fmt.Errorf("non-finite reconstructed primitive")
		}
	}
	if state.PressurePa <= 0 || state.TemperatureK <= 0 || state.SpecificHumidityKgKg < 0 ||
		state.CloudLiquidKgKg < 0 || state.CloudIceKgKg < 0 || state.CloudFraction < 0 ||
		state.CloudFraction > 1 || state.TKEJkg < 0 {
		return astrodomeScienceVector{}, "", fmt.Errorf("reconstructed thermodynamic primitive is outside its physical domain")
	}
	if state.SpecificHumidityKgKg+state.CloudLiquidKgKg+state.CloudIceKgKg >= 1 {
		return astrodomeScienceVector{}, "", fmt.Errorf("reconstructed water mass fractions do not leave a dry-air fraction")
	}

	virtualTemperature := state.TemperatureK * (1 +
		(calibration.WaterVapourGasConstantJKgK/calibration.DryAirGasConstantJKgK-1)*state.SpecificHumidityKgKg -
		state.CloudLiquidKgKg - state.CloudIceKgKg)
	if !finite(virtualTemperature) || virtualTemperature <= 0 {
		return astrodomeScienceVector{}, "", fmt.Errorf("moist virtual temperature is invalid")
	}
	density := state.PressurePa / (calibration.DryAirGasConstantJKgK * virtualTemperature)

	potentialTemperatureK := state.TemperatureK * math.Pow(100000/state.PressurePa, astrodomeSciencePoissonExponent)
	potentialTemperatureGradient := potentialTemperatureK * (state.VerticalDerivatives.TemperatureKPerM/state.TemperatureK -
		astrodomeSciencePoissonExponent*state.VerticalDerivatives.PressurePaPerM/state.PressurePa)
	_, east, north, _ := astrodomeObserverBasis(state.Location, state.HeightM)
	horizontalShear := math.Hypot(state.VerticalDerivatives.WindECEFPerM.dot(east), state.VerticalDerivatives.WindECEFPerM.dot(north))
	pblTopM := native.SurfaceHeightM + clamp(native.MixedLayerDepthM, calibration.Overall.BoundaryLayerMinM, calibration.Overall.BoundaryLayerTopM)
	cn2 := 0.0
	regime := "masciadri-pbl"
	if state.HeightM < pblTopM {
		pressureHPA := state.PressurePa / 100
		cn2 = calibration.Overall.GroundCn2Scale * 3.35e-6 *
			math.Pow(pressureHPA, 2*(1-2*astrodomeSciencePoissonExponent)) *
			math.Pow(potentialTemperatureK, -10.0/3.0) *
			math.Pow(math.Abs(potentialTemperatureGradient), 4.0/3.0) *
			math.Pow(state.TKEJkg, 2.0/3.0)
	} else {
		regime = "hmnsp99-troposphere"
		temperatureGradient := state.VerticalDerivatives.TemperatureKPerM
		exponent := 0.362 + 16.728*horizontalShear - 192.347*temperatureGradient
		if (finite(tropopauseHeightM) && state.HeightM >= tropopauseHeightM) ||
			(!finite(tropopauseHeightM) && state.PressurePa < 20000) {
			exponent = 0.757 + 13.819*horizontalShear - 57.784*temperatureGradient
			regime = "hmnsp99-stratosphere"
		}
		outerScaleFourThirds := math.Pow(0.1, 4.0/3.0) * math.Pow(10, exponent)
		potentialRefractiveGradient := -79e-6 * (state.PressurePa / 100) /
			(state.TemperatureK * state.TemperatureK) * potentialTemperatureGradient
		cn2 = 2.8 * outerScaleFourThirds * potentialRefractiveGradient * potentialRefractiveGradient
	}
	if !finite(cn2) || cn2 < 0 {
		return astrodomeScienceVector{}, "", fmt.Errorf("optical-turbulence kernel returned invalid Cn2")
	}
	tangentNorm := tangent.Norm()
	if !finite(tangentNorm) || math.Abs(tangentNorm-1) > 1e-8 {
		return astrodomeScienceVector{}, "", fmt.Errorf("ray tangent is not a finite unit ECEF vector")
	}
	tangent = tangent.scale(1 / tangentNorm)
	parallel := state.WindECEF.dot(tangent)
	transverse := state.WindECEF.add(tangent.scale(-parallel)).Norm()
	if !finite(transverse) || transverse < 0 {
		return astrodomeScienceVector{}, "", fmt.Errorf("transverse ECEF wind is invalid")
	}
	liquidExtinction := 3 * calibration.LiquidExtinctionEfficiency * density * state.CloudLiquidKgKg /
		(4 * calibration.LiquidDensityKgM3 * calibration.LiquidEffectiveRadiusM)
	iceExtinction := 3 * calibration.IceExtinctionEfficiency * density * state.CloudIceKgKg /
		(4 * calibration.IceDensityKgM3 * calibration.IceEffectiveRadiusM)
	result := astrodomeScienceVector{
		cn2,
		cn2 * math.Pow(transverse, 5.0/3.0),
		density * state.SpecificHumidityKgKg,
		liquidExtinction,
		iceExtinction,
	}
	for _, value := range result {
		if !finite(value) || value < 0 {
			return astrodomeScienceVector{}, "", fmt.Errorf("joint physical integrand is invalid")
		}
	}
	return result, regime, nil
}

type astrodomeScienceDerivedPass struct {
	cloudTransmissionNominal      float64
	cloudTransmissionConservative float64
	cloudBlocks                   []AstrodomeScienceCloudBlock
	overall                       float64
	factors                       AstrodomeScienceFactors
	penaltyContributions          []OverallPenaltyContribution
	cloudState                    string
	cloudBranch                   string
	tauState                      string
}

func deriveAstrodomeSciencePass(pass astrodomeSciencePass, site AstrodomeScienceSiteInputs, calibration AstrodomeScienceCalibration) (astrodomeScienceDerivedPass, error) {
	cloud := astrodomeScienceCloudClosure(pass.blocks, calibration)
	if !finite(cloud.nominalTransmission) || !finite(cloud.conservativeTransmission) ||
		cloud.nominalTransmission < 0 || cloud.nominalTransmission > 1 ||
		cloud.conservativeTransmission < 0 || cloud.conservativeTransmission > cloud.nominalTransmission+1e-15 {
		return astrodomeScienceDerivedPass{}, fmt.Errorf(
			"%w: nominal/conservative cloud closure is invalid",
			ErrAstrodomeScienceNonConvergence,
		)
	}
	seeing := seeingArcsecFromIntegratedCn2(math.Max(0, pass.values[astrodomeScienceCn2Index]+pass.errors[astrodomeScienceCn2Index]))
	seeingQuality := 1.0
	if seeing > 0 {
		seeingQuality = logarithmicLowerIsBetter(seeing, calibration.Overall.GoodSeeingArcsec, calibration.Overall.BadSeeingArcsec)
	}
	jvPlus := math.Max(0, pass.values[astrodomeScienceWindCn2Index]+pass.errors[astrodomeScienceWindCn2Index])
	tauState := "finite"
	if math.Max(0, pass.values[astrodomeScienceWindCn2Index]-pass.errors[astrodomeScienceWindCn2Index]) <= calibration.AbsoluteTolerance.WindWeightedCn2 {
		tauState = "calm_numerical_limit"
	}
	coherenceQuality := 1.0
	if jvPlus > 0 {
		coherenceQuality = logarithmicHigherIsBetter(astrodomeTau0MS(jvPlus, calibration.WavelengthM), calibration.Overall.BadCoherenceTimeMS, calibration.Overall.BestCoherenceTimeMS)
	}
	turbulenceFactor := boundedOpticalTurbulenceFactor(seeingQuality, coherenceQuality, calibration.Overall)
	windRisk := math.Max(
		smoothRisk(site.WindSpeed10MMS, calibration.Overall.SurfaceWindStartMS, calibration.Overall.SurfaceWindFullMS),
		smoothRisk(site.WindGust10MMS, calibration.Overall.SurfaceGustStartMS, calibration.Overall.SurfaceGustFullMS),
	)
	surfaceFactor := 1 - calibration.Overall.SurfaceWindMaxPenalty*windRisk
	fogFactor := 1.0
	switch site.FogHeuristic {
	case AstrodomeScienceFogPossible:
		fogFactor = calibration.Overall.PossibleFogFactor
	case AstrodomeScienceFogHigh:
		fogFactor = calibration.Overall.HighFogFactor
	}
	precipitationFactor := 1.0
	if site.PrecipitationRateMMPerHour >= calibration.Overall.PrecipitationDetectMM {
		precipitationFactor = 0
	}
	cloudFactor := math.Pow(cloud.conservativeTransmission, calibration.Overall.CloudWeight)
	factors := AstrodomeScienceFactors{
		SeeingQuality: seeingQuality, CoherenceQuality: coherenceQuality,
		Turbulence: turbulenceFactor, Cloud: cloudFactor, SurfaceWind: surfaceFactor,
		Fog: fogFactor, Precipitation: precipitationFactor,
	}
	penaltyFactors := []OverallPenaltyFactor{
		{Key: OverallPenaltyOpticalTurbulence, Factor: turbulenceFactor},
		{Key: OverallPenaltyCloudObstruction, Factor: cloudFactor},
		{Key: OverallPenaltySurfaceWind, Factor: surfaceFactor},
		{Key: OverallPenaltyFog, Factor: fogFactor},
		{Key: OverallPenaltyPrecipitation, Factor: precipitationFactor},
	}
	contributions, _, err := ShapleyMultiplicativeLoss(penaltyFactors)
	if err != nil {
		return astrodomeScienceDerivedPass{}, err
	}
	quality := turbulenceFactor * cloudFactor * surfaceFactor * fogFactor * precipitationFactor
	return astrodomeScienceDerivedPass{
		cloudTransmissionNominal:      cloud.nominalTransmission,
		cloudTransmissionConservative: cloud.conservativeTransmission,
		cloudBlocks:                   cloud.blocks,
		overall:                       1 + 9*clampSurfaceValue(quality, 0, 1),
		factors:                       factors,
		penaltyContributions:          contributions,
		cloudState:                    cloud.conservativeState,
		cloudBranch:                   cloud.conservativeBranch,
		tauState:                      tauState,
	}, nil
}

type astrodomeScienceCloudClosureResult struct {
	nominalTransmission      float64
	conservativeTransmission float64
	blocks                   []AstrodomeScienceCloudBlock
	nominalState             string
	conservativeState        string
	nominalBranch            string
	conservativeBranch       string
}

func astrodomeScienceCloudClosure(
	source map[astrodomeScienceCloudBlockKey]astrodomeScienceCloudAccumulator,
	calibration AstrodomeScienceCalibration,
) astrodomeScienceCloudClosureResult {
	keys := make([]astrodomeScienceCloudBlockKey, 0, len(source))
	for key := range source {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].cellID == keys[j].cellID {
			return keys[i].tier < keys[j].tier
		}
		return keys[i].cellID < keys[j].cellID
	})
	blocks := make([]AstrodomeScienceCloudBlock, 0, len(keys))
	nominalTierCover := map[AstrodomeScienceCloudTier]float64{}
	conservativeTierCover := map[AstrodomeScienceCloudTier]float64{}
	nominalLogTransmission := 0.0
	conservativeLogTransmission := 0.0
	nominalState := "clear"
	conservativeState := "clear"
	for _, key := range keys {
		block := source[key]
		nominalLiquid := math.Max(0, block.liquid.value())
		nominalIce := math.Max(0, block.ice.value())
		conservativeLiquid := math.Max(0, nominalLiquid+block.liquidError.value())
		conservativeIce := math.Max(0, nominalIce+block.iceError.value())
		nominalCover := clampSurfaceValue(block.maximumCloudFractionNominal, 0, 1)
		conservativeCover := clampSurfaceValue(block.maximumCloudFractionConservative, 0, 1)
		nominalTransmission := astrodomeScienceCloudBlockTransmission(
			nominalCover,
			nominalLiquid+nominalIce,
		)
		conservativeTransmission := astrodomeScienceCloudBlockTransmission(
			conservativeCover,
			conservativeLiquid+conservativeIce,
		)
		nominalLogTransmission = astrodomeScienceAccumulateLogTransmission(
			nominalLogTransmission,
			nominalTransmission,
		)
		conservativeLogTransmission = astrodomeScienceAccumulateLogTransmission(
			conservativeLogTransmission,
			conservativeTransmission,
		)
		nominalTierCover[key.tier] = math.Max(nominalTierCover[key.tier], nominalCover)
		conservativeTierCover[key.tier] = math.Max(conservativeTierCover[key.tier], conservativeCover)
		if nominalLiquid+nominalIce >= 1e-9 || nominalCover >= 1e-6 {
			nominalState = "cloud"
		}
		if conservativeLiquid+conservativeIce >= 1e-9 || conservativeCover >= 1e-6 {
			conservativeState = "cloud"
		}
		blocks = append(blocks, AstrodomeScienceCloudBlock{
			HorizontalCellID:               key.cellID,
			Tier:                           key.tier,
			CloudFractionNominal:           nominalCover,
			CloudFractionConservative:      conservativeCover,
			LiquidOpticalDepthNominal:      nominalLiquid,
			LiquidOpticalDepthConservative: conservativeLiquid,
			IceOpticalDepthNominal:         nominalIce,
			IceOpticalDepthConservative:    conservativeIce,
			TransmissionNominal:            nominalTransmission,
			TransmissionConservative:       conservativeTransmission,
		})
	}
	nominal, nominalBranch := astrodomeScienceCloseCloudTransmission(
		nominalLogTransmission,
		nominalTierCover,
		calibration,
	)
	conservative, conservativeBranch := astrodomeScienceCloseCloudTransmission(
		conservativeLogTransmission,
		conservativeTierCover,
		calibration,
	)
	if nominal <= 1e-6 {
		nominalState = "opaque"
	}
	if conservative <= 1e-6 {
		conservativeState = "opaque"
	}
	return astrodomeScienceCloudClosureResult{
		nominalTransmission:      nominal,
		conservativeTransmission: conservative,
		blocks:                   blocks,
		nominalState:             nominalState,
		conservativeState:        conservativeState,
		nominalBranch:            nominalBranch,
		conservativeBranch:       conservativeBranch,
	}
}

func astrodomeScienceCloudBlockTransmission(cover, opticalDepth float64) float64 {
	effectiveCover := clampSurfaceValue(cover, 0, 1)
	opticalDepth = math.Max(0, opticalDepth)
	if opticalDepth >= 1e-9 {
		// A non-zero condensate integral with vanishing reconstructed CLC is an
		// inconsistent native state. Repair it with a condensate-derived cover,
		// but apply that lower bound continuously for every declared cover. This
		// keeps T=(1-C)+C*exp(-tau/C) non-increasing in both C and tau instead of
		// introducing a discontinuity at an arbitrary small-cover threshold.
		condensateCover := math.Min(1, math.Max(0.01, -math.Expm1(-opticalDepth)))
		effectiveCover = math.Max(effectiveCover, condensateCover)
	}
	if effectiveCover == 0 {
		return 1
	}
	return clampSurfaceValue(
		(1-effectiveCover)+effectiveCover*math.Exp(-opticalDepth/effectiveCover),
		0,
		1,
	)
}

func astrodomeScienceAccumulateLogTransmission(sum, transmission float64) float64 {
	if transmission == 0 || math.IsInf(sum, -1) {
		return math.Inf(-1)
	}
	return sum + math.Log1p(transmission-1)
}

func astrodomeScienceCloseCloudTransmission(
	logCondensateTransmission float64,
	tierCover map[AstrodomeScienceCloudTier]float64,
	calibration AstrodomeScienceCalibration,
) (float64, string) {
	condensateTransmission := math.Exp(logCondensateTransmission)
	guards := map[AstrodomeScienceCloudTier]float64{
		AstrodomeScienceCloudLow:    calibration.Overall.UnresolvedCloudObstruction,
		AstrodomeScienceCloudMiddle: 0.55 * calibration.Overall.UnresolvedCloudObstruction,
		AstrodomeScienceCloudHigh:   0.18 * calibration.Overall.UnresolvedCloudObstruction,
	}
	guardTransmission := 1.0
	for _, tier := range []AstrodomeScienceCloudTier{
		AstrodomeScienceCloudLow,
		AstrodomeScienceCloudMiddle,
		AstrodomeScienceCloudHigh,
	} {
		guardTransmission *= 1 - guards[tier]*tierCover[tier]
	}
	if guardTransmission < condensateTransmission {
		return clampSurfaceValue(guardTransmission, 0, 1), "diagnosed-cover-guard"
	}
	return clampSurfaceValue(condensateTransmission, 0, 1), "condensate"
}

func verifyAstrodomeScienceRepeat(coarse, fine astrodomeSciencePass, coarseDerived, fineDerived astrodomeScienceDerivedPass, calibration AstrodomeScienceCalibration) error {
	absTolerance := calibration.AbsoluteTolerance.vector()
	for index := 0; index < astrodomeScienceIntegralCount; index++ {
		allowed := absTolerance[index] + calibration.RelativeTolerance*math.Abs(fine.values[index])
		if math.Abs(fine.values[index]-coarse.values[index]) > allowed {
			return fmt.Errorf("%w: repeat component %d differs by %.6g, allowed %.6g", ErrAstrodomeScienceNonConvergence, index, math.Abs(fine.values[index]-coarse.values[index]), allowed)
		}
	}
	if math.Abs(fineDerived.cloudTransmissionNominal-coarseDerived.cloudTransmissionNominal) > calibration.CloudRepeatTolerance ||
		math.Abs(fineDerived.cloudTransmissionConservative-coarseDerived.cloudTransmissionConservative) > calibration.CloudRepeatTolerance ||
		fineDerived.cloudState != coarseDerived.cloudState || fineDerived.cloudBranch != coarseDerived.cloudBranch ||
		fineDerived.tauState != coarseDerived.tauState {
		return fmt.Errorf("%w: cloud closure repeat changed materially", ErrAstrodomeScienceNonConvergence)
	}
	if len(fineDerived.cloudBlocks) != len(coarseDerived.cloudBlocks) {
		return fmt.Errorf("%w: cloud block set changed in repeat", ErrAstrodomeScienceNonConvergence)
	}
	for index, fineBlock := range fineDerived.cloudBlocks {
		coarseBlock := coarseDerived.cloudBlocks[index]
		if fineBlock.HorizontalCellID != coarseBlock.HorizontalCellID || fineBlock.Tier != coarseBlock.Tier ||
			math.Abs(fineBlock.CloudFractionNominal-coarseBlock.CloudFractionNominal) > calibration.CloudRepeatTolerance ||
			math.Abs(fineBlock.CloudFractionConservative-coarseBlock.CloudFractionConservative) > calibration.CloudRepeatTolerance {
			return fmt.Errorf("%w: cloud cover extrema or block identity changed in repeat", ErrAstrodomeScienceNonConvergence)
		}
	}
	if math.Abs(fineDerived.overall-coarseDerived.overall) > calibration.OverallRepeatTolerance {
		return fmt.Errorf("%w: Overall repeat differs by %.6g", ErrAstrodomeScienceNonConvergence, math.Abs(fineDerived.overall-coarseDerived.overall))
	}
	return nil
}

func astrodomeScienceNumericalError(
	coarse,
	fine astrodomeSciencePass,
	coarseDerived,
	fineDerived astrodomeScienceDerivedPass,
	calibration AstrodomeScienceCalibration,
) *AstrodomeScienceNumericalError {
	cn2Relative := astrodomeScienceRelativeRepeatDelta(
		coarse.values[astrodomeScienceCn2Index],
		fine.values[astrodomeScienceCn2Index],
		calibration.AbsoluteTolerance.IntegratedCn2,
	)
	windCn2Relative := astrodomeScienceRelativeRepeatDelta(
		coarse.values[astrodomeScienceWindCn2Index],
		fine.values[astrodomeScienceWindCn2Index],
		calibration.AbsoluteTolerance.WindWeightedCn2,
	)
	turbulenceRelative := astrodomeScienceMaximumDefined(cn2Relative, windCn2Relative)
	return &AstrodomeScienceNumericalError{
		OverallAbsolute: math.Abs(fineDerived.overall - coarseDerived.overall),
		CloudTransmissionAbsolute: math.Max(
			math.Abs(fineDerived.cloudTransmissionNominal-coarseDerived.cloudTransmissionNominal),
			math.Abs(fineDerived.cloudTransmissionConservative-coarseDerived.cloudTransmissionConservative),
		),
		TurbulenceIntegralRelative: turbulenceRelative,
		IntegratedCn2Relative:      cn2Relative,
		WindWeightedCn2Relative:    windCn2Relative,
	}
}

// astrodomeScienceEmbeddedNumericalError propagates the accepted embedded
// quadrature estimates without a second atmospheric reconstruction. Integral
// diagnostics use the accumulated high/low-rule error. Cloud bounds perturb
// each native cell/tier optical-depth block by its own accumulated panel
// error, and Overall combines those bounds with the lower/upper turbulence
// response while keeping the observer-local operational factors fixed.
func astrodomeScienceEmbeddedNumericalError(
	pass astrodomeSciencePass,
	published astrodomeScienceDerivedPass,
	site AstrodomeScienceSiteInputs,
	calibration AstrodomeScienceCalibration,
) (*AstrodomeScienceNumericalError, error) {
	cn2Relative := astrodomeScienceRelativeEmbeddedError(
		pass.values[astrodomeScienceCn2Index],
		pass.errors[astrodomeScienceCn2Index],
		calibration.AbsoluteTolerance.IntegratedCn2,
	)
	windCn2Relative := astrodomeScienceRelativeEmbeddedError(
		pass.values[astrodomeScienceWindCn2Index],
		pass.errors[astrodomeScienceWindCn2Index],
		calibration.AbsoluteTolerance.WindWeightedCn2,
	)
	cloudMinimum, cloudMaximum, err := astrodomeScienceEmbeddedCloudTransmissionBounds(pass.blocks, calibration)
	if err != nil {
		return nil, err
	}
	cloudError := math.Max(
		math.Abs(published.cloudTransmissionConservative-cloudMinimum),
		math.Abs(cloudMaximum-published.cloudTransmissionConservative),
	)

	optimisticPass := pass
	for index := range optimisticPass.values {
		optimisticPass.values[index] = math.Max(0, pass.values[index]-pass.errors[index])
		optimisticPass.errors[index] = 0
	}
	optimistic, err := deriveAstrodomeSciencePass(optimisticPass, site, calibration)
	if err != nil {
		return nil, err
	}
	fixedOperational := published.factors.SurfaceWind * published.factors.Fog * published.factors.Precipitation
	minimumOverall := 1 + 9*clampSurfaceValue(
		published.factors.Turbulence*
			math.Pow(cloudMinimum, calibration.Overall.CloudWeight)*fixedOperational,
		0, 1,
	)
	maximumOverall := 1 + 9*clampSurfaceValue(
		optimistic.factors.Turbulence*
			math.Pow(cloudMaximum, calibration.Overall.CloudWeight)*fixedOperational,
		0, 1,
	)
	overallError := math.Max(
		math.Abs(published.overall-minimumOverall),
		math.Abs(maximumOverall-published.overall),
	)
	if !finite(overallError) || !finite(cloudError) || overallError < 0 || cloudError < 0 {
		return nil, fmt.Errorf("%w: embedded derived numerical error is invalid", ErrAstrodomeScienceNonConvergence)
	}
	return &AstrodomeScienceNumericalError{
		OverallAbsolute:            overallError,
		CloudTransmissionAbsolute:  cloudError,
		TurbulenceIntegralRelative: astrodomeScienceMaximumDefined(cn2Relative, windCn2Relative),
		IntegratedCn2Relative:      cn2Relative,
		WindWeightedCn2Relative:    windCn2Relative,
	}, nil
}

func astrodomeScienceRelativeEmbeddedError(value, estimatedError, absoluteScale float64) *float64 {
	if math.Max(math.Abs(value), estimatedError) <= absoluteScale {
		return nil
	}
	scale := math.Max(math.Abs(value), math.Max(estimatedError, absoluteScale))
	relative := estimatedError / scale
	return &relative
}

func astrodomeScienceEmbeddedCloudTransmissionBounds(
	source map[astrodomeScienceCloudBlockKey]astrodomeScienceCloudAccumulator,
	calibration AstrodomeScienceCalibration,
) (float64, float64, error) {
	lowerOpticalDepth := astrodomeSciencePerturbCloudOpticalDepth(source, -1)
	upperOpticalDepth := astrodomeSciencePerturbCloudOpticalDepth(source, 1)
	maximumTransmission := astrodomeScienceCloudClosure(lowerOpticalDepth, calibration).conservativeTransmission
	minimumTransmission := astrodomeScienceCloudClosure(upperOpticalDepth, calibration).conservativeTransmission
	if !finite(minimumTransmission) || !finite(maximumTransmission) ||
		minimumTransmission < 0 || maximumTransmission > 1 || minimumTransmission > maximumTransmission {
		return 0, 0, fmt.Errorf("%w: embedded cloud-transmission bounds are invalid", ErrAstrodomeScienceNonConvergence)
	}
	return minimumTransmission, maximumTransmission, nil
}

func astrodomeSciencePerturbCloudOpticalDepth(
	source map[astrodomeScienceCloudBlockKey]astrodomeScienceCloudAccumulator,
	direction float64,
) map[astrodomeScienceCloudBlockKey]astrodomeScienceCloudAccumulator {
	result := make(map[astrodomeScienceCloudBlockKey]astrodomeScienceCloudAccumulator, len(source))
	for key, block := range source {
		liquid := math.Max(0, block.liquid.value()+direction*block.liquidError.value())
		ice := math.Max(0, block.ice.value()+direction*block.iceError.value())
		result[key] = astrodomeScienceCloudAccumulator{
			liquid:                           astrodomeScienceCompensatedSum{sum: liquid},
			ice:                              astrodomeScienceCompensatedSum{sum: ice},
			maximumCloudFractionNominal:      block.maximumCloudFractionNominal,
			maximumCloudFractionConservative: block.maximumCloudFractionConservative,
		}
	}
	return result
}

// astrodomeScienceRelativeRepeatDelta is deliberately undefined when both
// estimates lie inside the component's declared absolute numerical scale.
// Dividing by either near-zero estimate would otherwise manufacture an
// arbitrarily large relative error in a physically calm/clear limit.
func astrodomeScienceRelativeRepeatDelta(coarse, fine, absoluteScale float64) *float64 {
	scale := math.Max(math.Abs(coarse), math.Abs(fine))
	if scale <= absoluteScale {
		return nil
	}
	value := math.Abs(fine-coarse) / scale
	return &value
}

func astrodomeScienceMaximumDefined(values ...*float64) *float64 {
	var maximum *float64
	for _, value := range values {
		if value == nil {
			continue
		}
		if maximum == nil || *value > *maximum {
			copy := *value
			maximum = &copy
		}
	}
	return maximum
}

func astrodomeScienceConservativeEstimates(coarse, fine astrodomeSciencePass) astrodomeScienceVector {
	var result astrodomeScienceVector
	for index := range result {
		result[index] = fine.errors[index] + math.Abs(fine.values[index]-coarse.values[index])
	}
	return result
}

func astrodomeTau0MS(windWeightedCn2, wavelengthM float64) float64 {
	if windWeightedCn2 <= 0 {
		return math.Inf(1)
	}
	wavenumber := 2 * math.Pi / wavelengthM
	return 1000 * math.Pow(coherencePhaseStructureCoefficient*wavenumber*wavenumber*windWeightedCn2, -3.0/5.0)
}

// AstrodomeForecastLeadTimeQualityHeuristic returns the versioned lead-time component used
// by both completed and explicitly unavailable directional nodes.
func AstrodomeForecastLeadTimeQualityHeuristic(leadHours float64) float64 {
	return 0.96 - 0.26*clampSurfaceValue(leadHours/72, 0, 1)
}

func astrodomeScienceQuality(
	path AstrodomeSciencePath,
	leadHours float64,
	complete bool,
	converged bool,
	approximationLengthM float64,
) AstrodomeScienceQuality {
	lead := AstrodomeForecastLeadTimeQualityHeuristic(leadHours)
	category := AstrodomeScienceQualityUnavailable
	mandatoryAvailable := path.Availability.Geometry && path.Availability.Turbulence && path.Availability.Cloud &&
		path.Availability.TemporalBrackets && path.Availability.Terrain && path.TopClosed
	coarseLimited := path.GeometryCoverage < 1 || path.TurbulencePathCoverage < 1 || path.CloudPathCoverage < 1 ||
		path.TemporalResolutionHours > 1
	for _, cell := range path.Cells {
		coarseLimited = coarseLimited || cell.TerrainState == AstrodomeScienceTerrainGrazing
	}
	if complete && mandatoryAvailable {
		category = AstrodomeScienceQualityLimited
		if converged && approximationLengthM == 0 && !coarseLimited && lead >= 0.85 {
			category = AstrodomeScienceQualityGood
		} else if converged && approximationLengthM == 0 && !coarseLimited && lead >= 0.75 {
			category = AstrodomeScienceQualityUsable
		}
	}
	return AstrodomeScienceQuality{
		LeadTimeQualityHeuristic: lead, GeometryCoverage: path.GeometryCoverage,
		TurbulencePathCoverage: path.TurbulencePathCoverage, CloudPathCoverage: path.CloudPathCoverage,
		HumidityPathCoverage: path.HumidityPathCoverage, TemporalResolutionHours: path.TemporalResolutionHours,
		QuadratureConverged: converged, ApproximationLengthM: approximationLengthM,
		TopClosed: path.TopClosed, Category: category,
	}
}

func astrodomeScienceAttribution(geometryMode string) AstrodomeScienceAttribution {
	geometry := "exact straight ray on the DWD ICON R=6371229 m reference sphere; straight-compat, not refracted"
	if geometryMode == AstrodomeScienceGeometryRefractionFull {
		geometry = "full Ciddor moist-air ray on the DWD ICON R=6371229 m reference sphere; adaptive coupled ECEF ODE with varying tangent and ICON-top event"
	}
	return AstrodomeScienceAttribution{
		Geometry:        geometry,
		Turbulence:      "dynamic native MH PBL: Masciadri TKE kernel below; HMNSP99 above; complete LOS recomputation at 500 nm",
		TransverseWind:  "ECEF V_perp=V-(V dot ray)ray integrated pointwise as Cn2*|V_perp|^(5/3)",
		Density:         "moist density rho=P/(Rd*Tv), Tv=T[1+(Rv/Rd-1)qv-ql-qi]; native quantities are mass fractions",
		CloudClosure:    "liquid/ice extinction integrated per unique native cell/tier block; nominal reconstructed CLC and conservative raw-native CLC/tau-error closures are published separately; Overall uses the conservative closure",
		WaterColumn:     "W_slant=integral(rho*qv ds), kg/m2 (numerically equal to mm water equivalent)",
		NumericalMethod: astrodomeScienceNumericalMethod(AstrodomeScienceVerificationEmbedded),
		Precipitation:   "observer-local native accumulation differenced over its GRIB interval; binary operational veto at the declared mm/h threshold",
	}
}

func astrodomeScienceNumericalMethod(verification AstrodomeScienceIntegrationVerification) string {
	base := "single joint adaptive G7/K15 pass plus positive-weight non-extrapolatory GL5/GL3, GL3/GL2, and GL2/GL1 pairs on event-bounded panels; sub-GL2 physical panels use a positive midpoint estimate with a full-magnitude engineering allowance and limited data quality under the 1 m cumulative approximation ceiling; embedded high/low error estimates, physical splits, and proportional absolute-tolerance allocation"
	if verification == AstrodomeScienceVerificationIndependentRepeat {
		return base + "; reference verification repeats the complete integral independently at half tolerance"
	}
	return base
}

func cloneAstrodomeFloatPointer(value *float64) *float64 {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}
