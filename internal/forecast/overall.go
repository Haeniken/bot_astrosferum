package forecast

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"time"
)

// DefaultOverallPrecipitationDetectMM is the minimum precipitation amount in
// an hourly surface frame that makes that hour operationally unsuitable. The
// threshold is deliberately a detection threshold, not a continuous penalty:
// precipitation can wet exposed optics regardless of its intensity.
const (
	DefaultOverallPrecipitationDetectMM = 0.05
	// OverallIndexAlgorithmVersion identifies the scientific interpretation of
	// OverallIndexFrame independently from any renderer or transport.
	OverallIndexAlgorithmVersion = "overall-astronomy-index-v2-fog-heuristic-availability"
)

const (
	minimumOverallTurbulenceProfileCoverage = 0.90
	minimumOverallTurbulenceProfileTopAGLM  = 15000.0

	// ModelSurfacePressureProvenance identifies the pressure column used by the
	// Reference-V radiative calculation. PMSL is intentionally excluded: the
	// lowest native ICON full-level pressure is hydrostatically transferred over
	// the short HHL height offset to the ICON model surface.
	ModelSurfacePressureProvenance = "icon-lowest-model-level-p-hydrostatic-to-hhl-surface-v1"
)

// OverallDataCompleteness describes whether every input used by the Overall
// index was available. A partial frame remains usable, but consumers must not
// present the absence of an optional diagnostic as evidence of good weather.
type OverallDataCompleteness string

const (
	OverallDataComplete OverallDataCompleteness = "complete"
	OverallDataPartial  OverallDataCompleteness = "partial"
)

// Stable penalty keys let renderers assign colors without coupling the
// forecast package to presentation details.
const (
	OverallPenaltyOpticalTurbulence = "optical_turbulence"
	OverallPenaltyCloudObstruction  = "cloud_obstruction"
	OverallPenaltySurfaceWind       = "surface_wind"
	OverallPenaltyFog               = "fog"
	OverallPenaltyPrecipitation     = "precipitation"
)

// OverallPenaltyFactor is one bounded utility factor in a multiplicative
// score. One means no loss and zero means a complete veto.
type OverallPenaltyFactor struct {
	Key    string
	Factor float64
}

// OverallPenaltyContribution is a Shapley allocation of the total
// multiplicative loss. LossFraction is expressed on 0..1, not in percent.
type OverallPenaltyContribution struct {
	Key          string  `json:"key"`
	LossFraction float64 `json:"loss_fraction"`
}

// OverallIndexCalibration maps model seeing and direct observing obstructions
// to a 1..10 astronomy-suitability index. Seeing/cloud weights are exponents;
// optical turbulence, coherence, and surface wind are bounded utility terms.
type OverallIndexCalibration struct {
	SeeingWeight        float64
	CloudWeight         float64
	CoherenceTimeWeight float64
	// OpticalTurbulenceMaxPenalty bounds the combined seeing/tau0 utility
	// penalty. Physical seeing and coherence time remain unchanged diagnostics;
	// only their contribution to the general-purpose score is capped because
	// turbulence blurs detail while opaque cloud can remove a target.
	OpticalTurbulenceMaxPenalty float64
	PossibleFogFactor           float64
	HighFogFactor               float64
	// PrecipitationDetectMM is the operational-veto threshold for one hourly
	// frame. Zero selects DefaultOverallPrecipitationDetectMM so older callers
	// that construct the calibration explicitly remain safe.
	PrecipitationDetectMM float64
	GoodSeeingArcsec      float64
	BadSeeingArcsec       float64
	BestCoherenceTimeMS   float64
	BadCoherenceTimeMS    float64
	BoundaryLayerMinM     float64
	BoundaryLayerTopM     float64
	GroundCn2Scale        float64
	// UnresolvedCloudObstruction is the maximum low-cloud obstruction used
	// only when diagnostic CLC is not represented by grid-scale QC/QI. Middle
	// and high cloud use smaller fractions of this configurable guard.
	UnresolvedCloudObstruction   float64
	SurfaceWindMaxPenalty        float64
	SurfaceWindStartMS           float64
	SurfaceWindFullMS            float64
	SurfaceGustStartMS           float64
	SurfaceGustFullMS            float64
	CloudLiquidRadiusMicrometers float64
	CloudIceRadiusMicrometers    float64
}

func DefaultOverallIndexCalibration() OverallIndexCalibration {
	return OverallIndexCalibration{
		SeeingWeight: 1, CloudWeight: 2, CoherenceTimeWeight: 0.25,
		OpticalTurbulenceMaxPenalty: 0.25,
		PossibleFogFactor:           0.75, HighFogFactor: 0.10,
		PrecipitationDetectMM: DefaultOverallPrecipitationDetectMM,
		GoodSeeingArcsec:      0.5, BadSeeingArcsec: 2.0,
		BestCoherenceTimeMS: 5.2, BadCoherenceTimeMS: 1.6,
		BoundaryLayerMinM: 500, BoundaryLayerTopM: 2000, GroundCn2Scale: 1,
		UnresolvedCloudObstruction: 0.45,
		SurfaceWindMaxPenalty:      0.20,
		SurfaceWindStartMS:         8.5, SurfaceWindFullMS: 15,
		SurfaceGustStartMS: 12, SurfaceGustFullMS: 22,
		CloudLiquidRadiusMicrometers: 10, CloudIceRadiusMicrometers: 25,
	}
}

func (calibration OverallIndexCalibration) Validate() error {
	values := []float64{
		calibration.SeeingWeight, calibration.CloudWeight, calibration.CoherenceTimeWeight,
		calibration.OpticalTurbulenceMaxPenalty,
		calibration.PossibleFogFactor, calibration.HighFogFactor, calibration.PrecipitationDetectMM,
		calibration.GoodSeeingArcsec, calibration.BadSeeingArcsec,
		calibration.BestCoherenceTimeMS, calibration.BadCoherenceTimeMS,
		calibration.BoundaryLayerMinM, calibration.BoundaryLayerTopM, calibration.GroundCn2Scale,
		calibration.UnresolvedCloudObstruction, calibration.SurfaceWindMaxPenalty,
		calibration.SurfaceWindStartMS, calibration.SurfaceWindFullMS,
		calibration.SurfaceGustStartMS, calibration.SurfaceGustFullMS,
		calibration.CloudLiquidRadiusMicrometers, calibration.CloudIceRadiusMicrometers,
	}
	for _, value := range values {
		if !finite(value) {
			return fmt.Errorf("overall calibration values must be finite")
		}
	}
	if calibration.SeeingWeight <= 0 || calibration.SeeingWeight > 10 {
		return fmt.Errorf("overall seeing weight must be greater than 0 and no greater than 10")
	}
	if calibration.CloudWeight <= 0 || calibration.CloudWeight > 10 {
		return fmt.Errorf("overall cloud weight must be greater than 0 and no greater than 10")
	}
	if calibration.CoherenceTimeWeight < 0 || calibration.CoherenceTimeWeight > 1 {
		return fmt.Errorf("overall coherence-time weight must be between 0 and 1")
	}
	if calibration.OpticalTurbulenceMaxPenalty < 0 || calibration.OpticalTurbulenceMaxPenalty > 1 {
		return fmt.Errorf("overall optical-turbulence maximum penalty must be between 0 and 1")
	}
	if calibration.PossibleFogFactor < 0 || calibration.PossibleFogFactor > 1 {
		return fmt.Errorf("overall possible-fog factor must be between 0 and 1")
	}
	if calibration.HighFogFactor < 0 || calibration.HighFogFactor > 1 {
		return fmt.Errorf("overall high-fog factor must be between 0 and 1")
	}
	if calibration.PrecipitationDetectMM < 0 || calibration.PrecipitationDetectMM > 10 {
		return fmt.Errorf("overall hourly precipitation detection threshold must be between 0 and 10 mm")
	}
	if calibration.GoodSeeingArcsec <= 0 || calibration.BadSeeingArcsec <= calibration.GoodSeeingArcsec {
		return fmt.Errorf("overall seeing thresholds must be positive and ordered best < bad")
	}
	if calibration.BadCoherenceTimeMS <= 0 || calibration.BestCoherenceTimeMS <= calibration.BadCoherenceTimeMS {
		return fmt.Errorf("overall coherence-time thresholds must be positive and ordered bad < best")
	}
	if !finite(calibration.BoundaryLayerMinM) || !finite(calibration.BoundaryLayerTopM) ||
		calibration.BoundaryLayerMinM < 100 || calibration.BoundaryLayerTopM < calibration.BoundaryLayerMinM ||
		calibration.BoundaryLayerTopM > 4000 {
		return fmt.Errorf("overall boundary-layer bounds must satisfy 100 <= minimum <= maximum <= 4000 metres")
	}
	if calibration.GroundCn2Scale < 0.05 || calibration.GroundCn2Scale > 20 {
		return fmt.Errorf("overall ground Cn2 scale must be between 0.05 and 20")
	}
	if calibration.UnresolvedCloudObstruction < 0 || calibration.UnresolvedCloudObstruction > 1 {
		return fmt.Errorf("overall unresolved-cloud obstruction must be between 0 and 1")
	}
	if calibration.SurfaceWindMaxPenalty < 0 || calibration.SurfaceWindMaxPenalty > 0.5 {
		return fmt.Errorf("overall surface-wind maximum penalty must be between 0 and 0.5")
	}
	if calibration.SurfaceWindStartMS < 0 || calibration.SurfaceWindFullMS <= calibration.SurfaceWindStartMS ||
		calibration.SurfaceGustStartMS < 0 || calibration.SurfaceGustFullMS <= calibration.SurfaceGustStartMS {
		return fmt.Errorf("overall surface wind/gust thresholds must be non-negative and ordered start < full")
	}
	if calibration.CloudLiquidRadiusMicrometers < 2 || calibration.CloudLiquidRadiusMicrometers > 40 ||
		calibration.CloudIceRadiusMicrometers < 5 || calibration.CloudIceRadiusMicrometers > 150 {
		return fmt.Errorf("cloud effective radii are outside supported physical ranges")
	}
	return nil
}

// OverallCalibrationSHA256 returns the canonical JSON fingerprint carried by
// persisted presentation contracts. JSON field order follows the declared Go
// struct, so the digest is stable across processes and does not depend on a
// diagnostic fmt representation.
func OverallCalibrationSHA256(calibration OverallIndexCalibration) (string, error) {
	if err := calibration.Validate(); err != nil {
		return "", err
	}
	encoded, err := json.Marshal(calibration)
	if err != nil {
		return "", fmt.Errorf("marshal overall calibration: %w", err)
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

// OverallPenaltyPoints maps one dimensionless Shapley loss contribution onto
// the displayed 1..10 Overall scale. It is shared by static and interactive
// renderers so the two presentations cannot drift.
func OverallPenaltyPoints(lossFraction float64) (float64, error) {
	if !finite(lossFraction) || lossFraction < 0 || lossFraction > 1 {
		return 0, fmt.Errorf("overall penalty loss must be finite and between 0 and 1")
	}
	return 9 * lossFraction, nil
}

type OverallIndexFrame struct {
	ValidAt                         time.Time                       `json:"valid_at"`
	Index                           float64                         `json:"index"`
	WindIndex                       float64                         `json:"wind_index"` // legacy wind-only diagnostic, retained for comparison
	SeeingArcsec                    float64                         `json:"seeing_arcsec"`
	CoherenceTimeMS                 float64                         `json:"coherence_time_ms"`
	PhysicalSeeing                  bool                            `json:"physical_seeing"`
	PhysicalCoherence               bool                            `json:"physical_coherence"`
	GroundLayerPhysics              bool                            `json:"ground_layer_physics"`
	BoundaryLayerDepthM             float64                         `json:"boundary_layer_depth_m"`
	GroundLayerFraction             float64                         `json:"ground_layer_fraction"`
	BoundaryLayerTurbulenceFraction float64                         `json:"boundary_layer_turbulence_fraction"`
	IsoplanaticAngleArcsec          float64                         `json:"isoplanatic_angle_arcsec"`
	EffectiveTurbulenceHeightM      float64                         `json:"effective_turbulence_height_m"`
	EffectiveTurbulenceWindMS       float64                         `json:"effective_turbulence_wind_m_s"`
	FracGL250                       float64                         `json:"frac_gl_250"`
	FracGL500                       float64                         `json:"frac_gl_500"`
	FracGL1000                      float64                         `json:"frac_gl_1000"`
	FreeAtmosphereSeeing500MArcsec  float64                         `json:"free_atmosphere_seeing_above_500m_arcsec"`
	TurbulenceProfileQuality        OpticalTurbulenceProfileQuality `json:"turbulence_profile_quality"`
	TurbulenceProfileTopAGLM        float64                         `json:"turbulence_profile_top_agl_m"`
	TurbulenceProfileCoverage       float64                         `json:"turbulence_profile_coverage"`
	TurbulenceHeightMomentCoverage  float64                         `json:"turbulence_height_moment_coverage"`
	SurfacePressureHPA              float64                         `json:"surface_pressure_hpa,omitempty"`
	SurfacePressureProvenance       string                          `json:"surface_pressure_provenance,omitempty"`
	SeeingQualityPercent            float64                         `json:"seeing_quality_percent"`
	CoherenceQualityPercent         float64                         `json:"coherence_quality_percent"`
	OpticalTurbulenceFactorPercent  float64                         `json:"optical_turbulence_factor_percent"`
	ClearSkyPercent                 float64                         `json:"clear_sky_percent"`
	CloudCoverPercent               float64                         `json:"cloud_cover_percent"`
	CloudTransmissionPercent        float64                         `json:"cloud_transmission_percent"`
	CloudOpticalDepth               float64                         `json:"cloud_optical_depth"`
	CloudCondensatePhysics          bool                            `json:"cloud_condensate_physics"`
	CloudUnresolvedGuard            bool                            `json:"cloud_unresolved_guard"`
	SurfaceWindFactorPercent        float64                         `json:"surface_wind_factor_percent"`
	PrecipitationMM                 float64                         `json:"precipitation_mm"`
	PrecipitationVeto               bool                            `json:"precipitation_veto"`
	FogHeuristic                    int                             `json:"fog_heuristic"`
	HighFogHeuristic                bool                            `json:"high_fog_heuristic"`
	FogHeuristicAvailable           bool                            `json:"fog_heuristic_available"`
	DataCompleteness                OverallDataCompleteness         `json:"data_completeness"`
	PenaltyLossFraction             float64                         `json:"penalty_loss_fraction"`
	PenaltyContributions            []OverallPenaltyContribution    `json:"penalty_contributions"`
	ReferenceVBand                  *ReferenceVBandDiagnostic       `json:"reference_v_band,omitempty"`
	LeadTimeQualityHeuristic        float64                         `json:"lead_time_quality_heuristic"`
}

// ComputeHourlyOverallIndex combines a single physical turbulence integral
// (native ICON TKE up to the hourly ICON mixed-layer depth, bounded by the
// configured AGL minimum/maximum, plus HMNSP99 aloft), tau0 from
// that same Cn2/wind profile, effective cloud obstruction, fog, a deliberately
// mild operational surface-wind factor, and a binary precipitation veto. Dew
// risk never enters the formula.
func ComputeHourlyOverallIndex(vertical VerticalSeries, surface SurfaceSeries, cloud CloudSeries, calibration OverallIndexCalibration) ([]OverallIndexFrame, error) {
	if err := calibration.Validate(); err != nil {
		return nil, err
	}
	diagnostics, err := ComputeDiagnostics(vertical)
	if err != nil {
		return nil, err
	}
	if len(surface.Frames) < 2 {
		return nil, fmt.Errorf("surface series needs at least two frames")
	}
	result := make([]OverallIndexFrame, 0, len(surface.Frames))
	precipitationDetectMM := calibration.PrecipitationDetectMM
	if precipitationDetectMM == 0 {
		precipitationDetectMM = DefaultOverallPrecipitationDetectMM
	}
	for index, frame := range surface.Frames {
		if index > 0 && !frame.ValidAt.After(surface.Frames[index-1].ValidAt) {
			return nil, fmt.Errorf("surface frame times must be strictly increasing")
		}
		windIndex, _, leadTimeQualityHeuristic, available := interpolateUpperAirDiagnostics(diagnostics, frame.ValidAt)
		if !available {
			continue
		}
		if err := validateOverallSurfaceFrame(frame); err != nil {
			return nil, fmt.Errorf("invalid surface inputs at %s: %w", frame.ValidAt.Format(time.RFC3339), err)
		}
		profile, profileAvailable := interpolateVerticalProfile(vertical, frame.ValidAt)
		if !profileAvailable {
			return nil, fmt.Errorf("pressure-level turbulence profile is unavailable at %s", frame.ValidAt.Format(time.RFC3339))
		}
		cloudFrame, cloudAvailable := cloudFrameAt(cloud, frame.ValidAt)
		if !cloudAvailable {
			return nil, fmt.Errorf("native ICON ground-layer profile is unavailable at %s", frame.ValidAt.Format(time.RFC3339))
		}
		if !finite(frame.MixedLayerDepthM) || frame.MixedLayerDepthM < 0 {
			return nil, fmt.Errorf("ICON mixed-layer depth is invalid at %s", frame.ValidAt.Format(time.RFC3339))
		}
		boundaryLayerDepthM := clampSurfaceValue(frame.MixedLayerDepthM, calibration.BoundaryLayerMinM, calibration.BoundaryLayerTopM)
		metrics, groundLayerPhysics := HybridOpticalTurbulenceMetrics(
			profile, cloudFrame.Levels, cloud.SurfaceElevationM,
			boundaryLayerDepthM, calibration.GroundCn2Scale,
		)
		if !groundLayerPhysics {
			if !cloud.TurbulenceValidUntil.IsZero() && frame.ValidAt.After(cloud.TurbulenceValidUntil) {
				break
			}
			return nil, fmt.Errorf("native ICON ground-layer turbulence is incomplete at %s", frame.ValidAt.Format(time.RFC3339))
		}
		surfacePressureHPA, surfacePressureAvailable := modelSurfacePressureHPA(cloudFrame.Levels, cloud.SurfaceElevationM)
		if metrics.ProfileVerticalCoverage < minimumOverallTurbulenceProfileCoverage ||
			metrics.ProfileHeightMomentCoverage < minimumOverallTurbulenceProfileCoverage ||
			metrics.ProfileTopAGLM < minimumOverallTurbulenceProfileTopAGLM {
			return nil, fmt.Errorf("hybrid optical-turbulence profile support is insufficient at %s: vertical %.1f%%, height moment %.1f%%, top %.0f m AGL",
				frame.ValidAt.Format(time.RFC3339), 100*metrics.ProfileVerticalCoverage,
				100*metrics.ProfileHeightMomentCoverage, metrics.ProfileTopAGLM)
		}
		seeingArcsec := metrics.SeeingArcsec
		cloudCover := frame.CloudCoverPercent
		cloudFactor, opticalDepth, condensatePhysics, unresolvedGuard := cloudTransmission(frame, calibration)
		physicalSeeing := finite(seeingArcsec) && seeingArcsec > 0
		if !physicalSeeing {
			return nil, fmt.Errorf("hybrid optical seeing is unavailable at %s", frame.ValidAt.Format(time.RFC3339))
		}
		seeingFraction := logarithmicLowerIsBetter(seeingArcsec, calibration.GoodSeeingArcsec, calibration.BadSeeingArcsec)
		coherenceFraction := 1.0
		physicalCoherence := finite(metrics.CoherenceTimeMS) && metrics.CoherenceTimeMS > 0
		if physicalCoherence {
			coherenceFraction = logarithmicHigherIsBetter(metrics.CoherenceTimeMS, calibration.BadCoherenceTimeMS, calibration.BestCoherenceTimeMS)
		}
		// Seeing and tau0 remain physical diagnostics. Their mapping into this
		// target-agnostic utility score is bounded: turbulence can blur fine
		// detail, but unlike opaque cloud it does not remove the target.
		opticalTurbulenceFactor := boundedOpticalTurbulenceFactor(seeingFraction, coherenceFraction, calibration)
		surfaceWindFactor := surfaceWindFactor(frame, calibration)
		cloudObstructionFactor := math.Pow(cloudFactor, calibration.CloudWeight)
		fogAssessmentAvailable := frame.FogHeuristicAvailable
		fogRisk := 0
		if fogAssessmentAvailable {
			fogRisk = frame.FogHeuristic()
		}
		fogFactor := 1.0
		switch fogRisk {
		case 1:
			fogFactor = calibration.PossibleFogFactor
		case 2:
			fogFactor = calibration.HighFogFactor
		}
		highFog := fogRisk == 2
		precipitationVeto := frame.PrecipitationMM >= precipitationDetectMM
		precipitationFactor := 1.0
		if precipitationVeto {
			// This is an operational veto only. It does not alter the physical
			// seeing or cloud diagnostics retained in the frame.
			precipitationFactor = 0
		}
		penaltyContributions, penaltyLoss, err := ShapleyMultiplicativeLoss([]OverallPenaltyFactor{
			{Key: OverallPenaltyOpticalTurbulence, Factor: opticalTurbulenceFactor},
			{Key: OverallPenaltyCloudObstruction, Factor: cloudObstructionFactor},
			{Key: OverallPenaltySurfaceWind, Factor: surfaceWindFactor},
			{Key: OverallPenaltyFog, Factor: fogFactor},
			{Key: OverallPenaltyPrecipitation, Factor: precipitationFactor},
		})
		if err != nil {
			return nil, fmt.Errorf("decompose Overall penalty at %s: %w", frame.ValidAt.Format(time.RFC3339), err)
		}
		normalized := 1 - penaltyLoss
		dataCompleteness := OverallDataComplete
		if !fogAssessmentAvailable || !condensatePhysics || !physicalCoherence || metrics.ProfileQuality != OpticalTurbulenceProfileComplete {
			dataCompleteness = OverallDataPartial
		}
		result = append(result, OverallIndexFrame{
			ValidAt: frame.ValidAt, Index: 1 + 9*clampSurfaceValue(normalized, 0, 1),
			WindIndex: windIndex, ClearSkyPercent: cloudFactor * 100,
			CloudCoverPercent:        clampSurfaceValue(cloudCover, 0, 100),
			CloudTransmissionPercent: cloudFactor * 100, CloudOpticalDepth: finiteOrZero(opticalDepth),
			CloudCondensatePhysics: condensatePhysics, CloudUnresolvedGuard: unresolvedGuard,
			SeeingArcsec: seeingArcsec, CoherenceTimeMS: metrics.CoherenceTimeMS,
			PhysicalSeeing: physicalSeeing, PhysicalCoherence: physicalCoherence,
			GroundLayerPhysics: groundLayerPhysics, BoundaryLayerDepthM: boundaryLayerDepthM,
			GroundLayerFraction:             metrics.GroundLayerFraction,
			BoundaryLayerTurbulenceFraction: metrics.BoundaryLayerFraction,
			IsoplanaticAngleArcsec:          metrics.IsoplanaticAngleArcsec,
			EffectiveTurbulenceHeightM:      metrics.EffectiveTurbulenceHeightM,
			EffectiveTurbulenceWindMS:       metrics.EffectiveWindSpeedMS,
			FracGL250:                       metrics.FracGL250,
			FracGL500:                       metrics.FracGL500,
			FracGL1000:                      metrics.FracGL1000,
			FreeAtmosphereSeeing500MArcsec:  metrics.FreeAtmosphereSeeing500MArcsec,
			TurbulenceProfileQuality:        metrics.ProfileQuality,
			TurbulenceProfileTopAGLM:        metrics.ProfileTopAGLM,
			TurbulenceProfileCoverage:       metrics.ProfileVerticalCoverage,
			TurbulenceHeightMomentCoverage:  metrics.ProfileHeightMomentCoverage,
			SurfacePressureHPA:              surfacePressureHPA,
			SeeingQualityPercent:            seeingFraction * 100, CoherenceQualityPercent: coherenceFraction * 100,
			OpticalTurbulenceFactorPercent: opticalTurbulenceFactor * 100,
			SurfaceWindFactorPercent:       surfaceWindFactor * 100,
			PrecipitationMM:                frame.PrecipitationMM,
			PrecipitationVeto:              precipitationVeto,
			FogHeuristic:                   fogRisk, HighFogHeuristic: highFog,
			FogHeuristicAvailable: fogAssessmentAvailable, DataCompleteness: dataCompleteness,
			PenaltyLossFraction: penaltyLoss, PenaltyContributions: penaltyContributions,
			LeadTimeQualityHeuristic: leadTimeQualityHeuristic,
		})
		if surfacePressureAvailable {
			result[len(result)-1].SurfacePressureProvenance = ModelSurfacePressureProvenance
		}
	}
	if len(result) < 2 {
		return nil, fmt.Errorf("surface and upper-air forecast periods do not overlap")
	}
	return result, nil
}

// modelSurfacePressureHPA derives the pressure at the ICON model surface from
// the lowest valid native full-level P/T/HHL sample. That full level is only a
// short distance above the surface; integrating the dry hydrostatic equation
// over that offset avoids incorrectly using sea-level-reduced PMSL in optical
// depth while introducing no second external field or standard-atmosphere
// assumption. The dry-temperature approximation is deliberately restricted to
// a 1 km offset; actual ICON geometry is normally tens of metres.
func modelSurfacePressureHPA(levels []CloudLevel, surfaceElevationM float64) (float64, bool) {
	if !finite(surfaceElevationM) {
		return 0, false
	}
	const (
		standardGravityMS2 = 9.80665
		dryAirGasConstant  = 287.05
		maximumOffsetM     = 1000.0
	)
	lowest := CloudLevel{}
	lowestHeight := math.Inf(1)
	found := false
	for _, level := range levels {
		if !finite(level.PressureHPA) || level.PressureHPA <= 0 || level.PressureHPA > 1200 ||
			!finite(level.HeightM) || !finite(level.TemperatureK) || level.TemperatureK < 150 || level.TemperatureK > 350 {
			continue
		}
		if level.HeightM < lowestHeight {
			lowest, lowestHeight, found = level, level.HeightM, true
		}
	}
	if !found || math.Abs(lowest.HeightM-surfaceElevationM) > maximumOffsetM {
		return 0, false
	}
	heightOffsetM := lowest.HeightM - surfaceElevationM
	pressure := lowest.PressureHPA * math.Exp(standardGravityMS2*heightOffsetM/(dryAirGasConstant*lowest.TemperatureK))
	if !finite(pressure) || pressure <= 0 || pressure > 1200 {
		return 0, false
	}
	return pressure, true
}

// ShapleyMultiplicativeLoss allocates 1-product(f_i) among the supplied
// factors. For a factor i, its Shapley value is the mean marginal loss over all
// possible insertion orders:
//
//	phi_i = (1-f_i) * sum_{S subset N\\{i}} |S|!(n-|S|-1)!/n! * product_{j in S} f_j
//
// The implementation groups subsets by cardinality using elementary
// symmetric sums. It is O(n^3), symmetric, order-independent, and avoids an
// exponential permutation/subset enumeration. The returned contributions sum
// to totalLoss (within floating-point roundoff).
func ShapleyMultiplicativeLoss(factors []OverallPenaltyFactor) (contributions []OverallPenaltyContribution, totalLoss float64, err error) {
	if len(factors) == 0 {
		return []OverallPenaltyContribution{}, 0, nil
	}
	seen := make(map[string]struct{}, len(factors))
	product := 1.0
	for _, factor := range factors {
		if factor.Key == "" {
			return nil, 0, fmt.Errorf("penalty factor key must not be empty")
		}
		if _, exists := seen[factor.Key]; exists {
			return nil, 0, fmt.Errorf("penalty factor key %q is duplicated", factor.Key)
		}
		seen[factor.Key] = struct{}{}
		if !finite(factor.Factor) || factor.Factor < 0 || factor.Factor > 1 {
			return nil, 0, fmt.Errorf("penalty factor %q must be finite and between 0 and 1", factor.Key)
		}
		product *= factor.Factor
	}
	totalLoss = 1 - product
	contributions = make([]OverallPenaltyContribution, len(factors))
	n := len(factors)
	for index, factor := range factors {
		// sums[k] is the sum of all k-factor products excluding factor i.
		sums := make([]float64, n)
		sums[0] = 1
		included := 0
		for otherIndex, other := range factors {
			if otherIndex == index {
				continue
			}
			included++
			for cardinality := included; cardinality >= 1; cardinality-- {
				sums[cardinality] += sums[cardinality-1] * other.Factor
			}
		}
		meanPrecedingProduct := 0.0
		binomial := 1.0 // C(n-1, 0)
		for cardinality := 0; cardinality < n; cardinality++ {
			meanPrecedingProduct += sums[cardinality] / (float64(n) * binomial)
			if cardinality < n-1 {
				binomial *= float64(n-1-cardinality) / float64(cardinality+1)
			}
		}
		contributions[index] = OverallPenaltyContribution{
			Key: factor.Key, LossFraction: (1 - factor.Factor) * meanPrecedingProduct,
		}
	}
	return contributions, totalLoss, nil
}

// validateOverallSurfaceFrame rejects corrupt values for inputs that directly
// participate in the score. Missing optional visibility and condensate
// diagnostics remain valid and are reported as partial data instead.
func validateOverallSurfaceFrame(frame SurfaceFrame) error {
	percentages := []struct {
		name  string
		value float64
	}{
		{"total cloud cover", frame.CloudCoverPercent},
		{"low cloud cover", frame.LowCloudCoverPercent},
		{"middle cloud cover", frame.MidCloudCoverPercent},
		{"high cloud cover", frame.HighCloudCoverPercent},
	}
	for _, item := range percentages {
		if !finite(item.value) || item.value < 0 || item.value > 100 {
			return fmt.Errorf("%s must be finite and between 0 and 100 percent", item.name)
		}
	}
	nonnegative := []struct {
		name  string
		value float64
	}{
		{"hourly precipitation", frame.PrecipitationMM},
		{"surface wind speed", frame.WindSpeedMS},
		{"surface wind gust", frame.WindGustMS},
	}
	for _, item := range nonnegative {
		if !finite(item.value) || item.value < 0 {
			return fmt.Errorf("%s must be finite and non-negative", item.name)
		}
	}
	if frame.CloudCondensateAvailable {
		if !finite(frame.CloudLiquidPathKgM2) || frame.CloudLiquidPathKgM2 < 0 ||
			!finite(frame.CloudIcePathKgM2) || frame.CloudIcePathKgM2 < 0 {
			return fmt.Errorf("available cloud condensate paths must be finite and non-negative")
		}
	}
	if frame.FogHeuristicAvailable {
		if !finite(frame.TemperatureC) || !finite(frame.DewPointC) {
			return fmt.Errorf("temperature and dew point must be finite when fog assessment is available")
		}
		if !finite(frame.RelativeHumidityPercent) || frame.RelativeHumidityPercent < 0 || frame.RelativeHumidityPercent > 100 {
			return fmt.Errorf("relative humidity must be finite and between 0 and 100 percent when fog assessment is available")
		}
		if !finite(frame.VisibilityKM) || frame.VisibilityKM < 0 {
			return fmt.Errorf("visibility must be finite and non-negative when fog assessment is available")
		}
	}
	return nil
}

// boundedOpticalTurbulenceFactor is a convex mixture of target visibility and
// high-resolution utility. The raw seeing/tau0 quality can reach zero at the
// configured poor reference boundaries, but those are high-resolution
// constraints rather than evidence that all observing is impossible.
func boundedOpticalTurbulenceFactor(seeingQuality, coherenceQuality float64, calibration OverallIndexCalibration) float64 {
	seeingQuality = clampSurfaceValue(seeingQuality, 0, 1)
	coherenceQuality = clampSurfaceValue(coherenceQuality, 0, 1)
	coherenceFactor := 1 - calibration.CoherenceTimeWeight*(1-coherenceQuality)
	rawQuality := math.Pow(seeingQuality, calibration.SeeingWeight) * coherenceFactor
	return 1 - calibration.OpticalTurbulenceMaxPenalty*(1-clampSurfaceValue(rawQuality, 0, 1))
}

func finiteOrZero(value float64) float64 {
	if !finite(value) {
		return 0
	}
	return value
}

// cloudTransmission converts ICON total-column cloud liquid/ice water paths
// into a direct-light transmission proxy at visible wavelengths. TQC/TQI are
// grid-box means, so optical depth inside the cloudy fraction is tau/C. The
// all-sky transmission is then the clear fraction plus attenuated cloudy sky:
//
//	T = (1-C) + C*exp(-tau/C)
//	tau = 3*Qext*CWP/(4*rho*r_eff)
//
// Qext=2.0 for liquid and 2.1 for ice. The phase-specific densities are 1000
// and 916.7 kg/m3. Effective radii are necessarily calibrated assumptions
// because ICON-EU's public one-moment fields contain mass but not particle
// number/size. With no TQC/TQI, the legacy clear-sky fraction is retained.
func cloudTransmission(frame SurfaceFrame, calibration OverallIndexCalibration) (transmission, opticalDepth float64, physical, unresolvedGuard bool) {
	cover := clampSurfaceValue(frame.CloudCoverPercent/100, 0, 1)
	if !frame.CloudCondensateAvailable {
		return 1 - cover, math.NaN(), false, false
	}
	liquidTau := phaseOpticalDepth(frame.CloudLiquidPathKgM2, 2.0, 1000, calibration.CloudLiquidRadiusMicrometers)
	iceTau := phaseOpticalDepth(frame.CloudIcePathKgM2, 2.1, 916.7, calibration.CloudIceRadiusMicrometers)
	opticalDepth = liquidTau + iceTau
	if cover < 1e-6 {
		if opticalDepth < 1e-9 {
			guard := unresolvedColumnCloudObstruction(frame, calibration.UnresolvedCloudObstruction)
			return 1 - guard, opticalDepth, true, guard > 0
		}
		// Resolve the rare inconsistent grid point conservatively: condensate
		// exists despite rounded CLCT=0, so infer a minimal cloudy fraction.
		cover = math.Min(1, math.Max(0.01, 1-math.Exp(-opticalDepth)))
	}
	inCloudTau := opticalDepth / cover
	transmission = (1 - cover) + cover*math.Exp(-inCloudTau)
	// ICON cloud cover is diagnostic and includes sub-grid variability,
	// whereas public TQC/TQI are prognostic grid-box condensate. Near-zero TQ
	// therefore cannot prove that diagnosed cloud is perfectly transparent.
	// Preserve physical optical depth, but bound that uncertainty by the
	// diagnosed low/middle/high tiers: low cloud receives the configured guard,
	// middle 55% of it, and high thin cloud only 18%.
	maximumUnresolvedTransmission := 1 - unresolvedColumnCloudObstruction(frame, calibration.UnresolvedCloudObstruction)
	if transmission > maximumUnresolvedTransmission {
		transmission = maximumUnresolvedTransmission
		unresolvedGuard = true
	}
	return clampSurfaceValue(transmission, 0, 1), opticalDepth, true, unresolvedGuard
}

func logarithmicLowerIsBetter(value, best, bad float64) float64 {
	if !finite(value) || value <= 0 || best <= 0 || bad <= best {
		return 0
	}
	return clampSurfaceValue(math.Log(bad/value)/math.Log(bad/best), 0, 1)
}

func logarithmicHigherIsBetter(value, bad, best float64) float64 {
	if math.IsInf(value, 1) {
		return 1
	}
	if !finite(value) || value <= 0 || bad <= 0 || best <= bad {
		return 0
	}
	return clampSurfaceValue(math.Log(value/bad)/math.Log(best/bad), 0, 1)
}

func surfaceWindFactor(frame SurfaceFrame, calibration OverallIndexCalibration) float64 {
	windRisk := smoothRisk(frame.WindSpeedMS, calibration.SurfaceWindStartMS, calibration.SurfaceWindFullMS)
	gustRisk := smoothRisk(frame.WindGustMS, calibration.SurfaceGustStartMS, calibration.SurfaceGustFullMS)
	return 1 - calibration.SurfaceWindMaxPenalty*math.Max(windRisk, gustRisk)
}

func smoothRisk(value, start, full float64) float64 {
	x := clampSurfaceValue((value-start)/(full-start), 0, 1)
	return x * x * (3 - 2*x)
}

func cloudFrameAt(series CloudSeries, target time.Time) (CloudFrame, bool) {
	index := sort.Search(len(series.Frames), func(index int) bool { return !series.Frames[index].ValidAt.Before(target) })
	if index >= len(series.Frames) || !series.Frames[index].ValidAt.Equal(target) {
		return CloudFrame{}, false
	}
	return series.Frames[index], true
}

func phaseOpticalDepth(pathKgM2, extinctionEfficiency, particleDensityKgM3, radiusMicrometers float64) float64 {
	return 3 * extinctionEfficiency * math.Max(0, pathKgM2) /
		(4 * particleDensityKgM3 * radiusMicrometers * 1e-6)
}

func interpolateVerticalProfile(series VerticalSeries, target time.Time) ([]VerticalLevel, bool) {
	if len(series.Frames) == 0 || target.Before(series.Frames[0].ValidAt) || target.After(series.Frames[len(series.Frames)-1].ValidAt) {
		return nil, false
	}
	right := sort.Search(len(series.Frames), func(index int) bool { return !series.Frames[index].ValidAt.Before(target) })
	if right < len(series.Frames) && series.Frames[right].ValidAt.Equal(target) {
		return series.Frames[right].Levels, true
	}
	if right == 0 || right == len(series.Frames) {
		return nil, false
	}
	left := right - 1
	span := series.Frames[right].ValidAt.Sub(series.Frames[left].ValidAt)
	if span <= 0 || len(series.Frames[left].Levels) != len(series.Frames[right].Levels) {
		return nil, false
	}
	fraction := float64(target.Sub(series.Frames[left].ValidAt)) / float64(span)
	levels := make([]VerticalLevel, len(series.Frames[left].Levels))
	for index, lower := range series.Frames[left].Levels {
		upper := series.Frames[right].Levels[index]
		levels[index] = VerticalLevel{
			PressureHPA:  lower.PressureHPA,
			HeightM:      lower.HeightM + fraction*(upper.HeightM-lower.HeightM),
			TemperatureK: interpolateFinite(lower.TemperatureK, upper.TemperatureK, fraction),
			UMS:          interpolateFinite(lower.UMS, upper.UMS, fraction),
			VMS:          interpolateFinite(lower.VMS, upper.VMS, fraction),
		}
	}
	return levels, true
}

// InterpolateVerticalFrame returns raw pressure-level state on an intermediate
// model hour. Only the linear state variables (height, temperature and wind)
// and the deterministic lead-time quality heuristic are interpolated. Nonlinear Cn2, seeing,
// coherence time and suitability indices must be recomputed from this frame.
func InterpolateVerticalFrame(series VerticalSeries, target time.Time) (VerticalFrame, bool) {
	levels, available := interpolateVerticalProfile(series, target)
	if !available {
		return VerticalFrame{}, false
	}
	right := sort.Search(len(series.Frames), func(index int) bool { return !series.Frames[index].ValidAt.Before(target) })
	if right < len(series.Frames) && series.Frames[right].ValidAt.Equal(target) {
		frame := series.Frames[right]
		frame.Levels = levels
		return frame, true
	}
	if right == 0 || right == len(series.Frames) {
		return VerticalFrame{}, false
	}
	left := right - 1
	span := series.Frames[right].ValidAt.Sub(series.Frames[left].ValidAt)
	if span <= 0 || !finite(series.Frames[left].LeadTimeQualityHeuristic) || !finite(series.Frames[right].LeadTimeQualityHeuristic) {
		return VerticalFrame{}, false
	}
	fraction := float64(target.Sub(series.Frames[left].ValidAt)) / float64(span)
	return VerticalFrame{
		ValidAt: target,
		Levels:  levels,
		LeadTimeQualityHeuristic: series.Frames[left].LeadTimeQualityHeuristic +
			fraction*(series.Frames[right].LeadTimeQualityHeuristic-series.Frames[left].LeadTimeQualityHeuristic),
	}, true
}

func interpolateFinite(left, right, fraction float64) float64 {
	if !finite(left) || !finite(right) {
		return math.NaN()
	}
	return left + fraction*(right-left)
}

func interpolateUpperAirDiagnostics(diagnostics Diagnostics, target time.Time) (float64, float64, float64, bool) {
	if len(diagnostics.Times) == 0 || target.Before(diagnostics.Times[0]) || target.After(diagnostics.Times[len(diagnostics.Times)-1]) {
		return 0, math.NaN(), 0, false
	}
	right := sort.Search(len(diagnostics.Times), func(index int) bool { return !diagnostics.Times[index].Before(target) })
	if right < len(diagnostics.Times) && diagnostics.Times[right].Equal(target) {
		return diagnostics.SeeingIndex[right], diagnostics.OpticalSeeingArcsec[right], diagnostics.LeadTimeQualityHeuristic[right], true
	}
	if right == 0 || right == len(diagnostics.Times) {
		return 0, math.NaN(), 0, false
	}
	left := right - 1
	span := diagnostics.Times[right].Sub(diagnostics.Times[left])
	if span <= 0 || math.IsNaN(diagnostics.SeeingIndex[left]) || math.IsNaN(diagnostics.SeeingIndex[right]) {
		return 0, math.NaN(), 0, false
	}
	fraction := float64(target.Sub(diagnostics.Times[left])) / float64(span)
	windIndex := diagnostics.SeeingIndex[left] + fraction*(diagnostics.SeeingIndex[right]-diagnostics.SeeingIndex[left])
	seeingArcsec := math.NaN()
	if finite(diagnostics.OpticalSeeingArcsec[left]) && finite(diagnostics.OpticalSeeingArcsec[right]) {
		seeingArcsec = diagnostics.OpticalSeeingArcsec[left] + fraction*(diagnostics.OpticalSeeingArcsec[right]-diagnostics.OpticalSeeingArcsec[left])
	}
	leadTimeQualityHeuristic := diagnostics.LeadTimeQualityHeuristic[left] + fraction*(diagnostics.LeadTimeQualityHeuristic[right]-diagnostics.LeadTimeQualityHeuristic[left])
	return windIndex, seeingArcsec, leadTimeQualityHeuristic, true
}
