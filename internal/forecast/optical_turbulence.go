package forecast

import (
	"math"
	"sort"
)

const (
	seeingWavelengthM      = 500e-9
	friedR0Coefficient     = 0.423
	friedSeeingCoefficient = 0.98
	// coherencePhaseStructureCoefficient is the published coefficient in the
	// temporal phase-structure definition D_phi(t)=2.910*k^2*J_V*t^(5/3).
	// Keeping the base form avoids another rounding step through the commonly
	// printed expanded shorthand 0.058.
	coherencePhaseStructureCoefficient = 2.910
	// isoplanaticPhaseStructureCoefficient is the published coefficient in
	// sigma_aniso^2=2.914*k^2*theta^(5/3)*integral(Cn2*h^(5/3)dh).
	// It is deliberately distinct from the source-published 2.910 temporal
	// coefficient above.
	isoplanaticPhaseStructureCoefficient = 2.914
	radiansToArcsec                      = 206264.80624709636
	dryAirPoisson                        = 287.05 / 1004.0
	// Height-sensitive theta0 diagnostics require the retained profile to
	// reach the lower stratosphere. ProfileTopAGLM remains exposed so callers
	// can impose a stricter, instrument-specific gate.
	minimumCompleteTurbulenceProfileTopAGLM = 18000.0
)

// OpticalTurbulenceProfileQuality reports structural suitability of the
// retained vertical profile for height-sensitive diagnostics such as theta0.
type OpticalTurbulenceProfileQuality string

const (
	// OpticalTurbulenceProfileUnavailable means no valid integrated profile is
	// available.
	OpticalTurbulenceProfileUnavailable OpticalTurbulenceProfileQuality = "unavailable"
	// OpticalTurbulenceProfileLimited retains physical diagnostics but signals
	// incomplete sampling or an insufficient model top.
	OpticalTurbulenceProfileLimited OpticalTurbulenceProfileQuality = "limited"
	// OpticalTurbulenceProfileModelDomainComplete has at least 99% structural
	// coverage and reaches the lower-stratosphere model-domain boundary. It does
	// not claim that unmodelled turbulence above ProfileTopAGLM is zero.
	OpticalTurbulenceProfileModelDomainComplete OpticalTurbulenceProfileQuality = "model_domain_complete"
)

// OpticalTurbulenceMetrics keeps the standard moments and derived parameters
// of one provider-neutral Cn2 profile. Wavelength-dependent values are
// evaluated at 500 nm and at zenith.
type OpticalTurbulenceMetrics struct {
	SeeingArcsec                   float64
	CoherenceTimeMS                float64
	IsoplanaticAngleArcsec         float64
	IntegratedCn2                  float64 // J = integral(Cn^2 dz), m^(1/3)
	WindWeightedCn2                float64 // JV = integral(Cn^2 |V|^(5/3) dz), m^2 s^(-5/3)
	HeightWeightedCn2              float64 // Jh = integral(Cn^2 h_AGL^(5/3) dz), m^2
	EffectiveTurbulenceHeightM     float64
	EffectiveWindSpeedMS           float64
	BoundaryLayerCn2               float64
	BoundaryLayerFraction          float64
	FracGL250                      float64
	FracGL500                      float64
	FracGL1000                     float64
	FreeAtmosphereSeeing500MArcsec float64
	// Coverage fields describe structural sampling of the reported vertical
	// span, not probabilistic forecast confidence. HeightMomentCoverage uses
	// the geometric h^(5/3) weighting that makes theta0 sensitive to gaps aloft.
	ProfileVerticalCoverage     float64
	ProfileHeightMomentCoverage float64
	ProfileTopAGLM              float64
	ProfileQuality              OpticalTurbulenceProfileQuality

	// GroundLayerCn2 and GroundLayerFraction are compatibility aliases for the
	// current dynamic ICON mixed-layer/PBL split. They are not MASS FracGL;
	// consumers requiring a fixed cutoff must use FracGL250/500/1000.
	GroundLayerCn2      float64
	GroundLayerFraction float64
	ValidLayerFraction  float64
}

type turbulenceNode struct {
	heightM         float64
	cn2             float64
	windWeightedCn2 float64
	uMS             float64
	vMS             float64
}

// HMNSP99SeeingArcsec estimates zenith long-exposure FWHM at 500 nm from a
// pressure-level profile. It follows the Tatarskii refractive-index model and
// the HMNSP99 outer-scale parametrization. The result is model-derived rather
// than a local DIMM measurement and does not include dome turbulence.
func HMNSP99SeeingArcsec(levels []VerticalLevel) float64 {
	return HMNSP99Metrics(levels).SeeingArcsec
}

// HybridOpticalTurbulenceMetrics uses the native ICON prognostic TKE field in
// the unresolved boundary layer and HMNSP99 on pressure levels above it. This
// is intentionally a single turbulence integral: HMNSP99 uses vector wind
// shear (including direction changes), the native ground layer uses ICON's
// prognostic TKE, and |V|^(5/3) supplies the wind dependence of tau0. Adding
// the legacy wind index on top would count the same flow twice.
//
// The boundary-layer expression follows the Masciadri parametrization:
//
//	Cn2 = 3.35e-6 P^[2(1-2R/cp)] theta^(-10/3)
//	      |dtheta/dz|^(4/3) TKE^(2/3)
//
// with P in hPa, z in metres, and TKE in m2/s2 (equivalent to J/kg).
func HybridOpticalTurbulenceMetrics(pressureLevels []VerticalLevel, modelLevels []CloudLevel, surfaceElevationM, boundaryLayerTopAGLM, groundCn2Scale float64) (OpticalTurbulenceMetrics, bool) {
	nodes, ok := masciadriTurbulenceNodes(modelLevels, surfaceElevationM, boundaryLayerTopAGLM, groundCn2Scale)
	if !ok {
		return invalidOpticalTurbulenceMetrics(), false
	}
	topM := surfaceElevationM + boundaryLayerTopAGLM
	boundaryLayers, coveredM, ok := turbulenceLayersFromNodes(nodes, surfaceElevationM, topM)
	if !ok || coveredM/boundaryLayerTopAGLM < 0.99 {
		return invalidOpticalTurbulenceMetrics(), false
	}
	freeLayers, validLayerFraction, expectedTopM, ok := hmnsp99TurbulenceLayersAbove(pressureLevels, topM)
	if !ok || expectedTopM <= topM {
		return invalidOpticalTurbulenceMetrics(), false
	}
	layers := make([]opticalTurbulenceLayer, 0, len(boundaryLayers)+len(freeLayers))
	layers = append(layers, boundaryLayers...)
	layers = append(layers, freeLayers...)
	profile := opticalTurbulenceProfile{
		layers:             layers,
		surfaceElevationM:  surfaceElevationM,
		boundaryLayerTopM:  topM,
		expectedTopM:       expectedTopM,
		validLayerFraction: validLayerFraction,
	}
	metrics := opticalTurbulenceMetricsFromProfile(profile)
	if !finite(metrics.SeeingArcsec) {
		return invalidOpticalTurbulenceMetrics(), false
	}
	return metrics, true
}

// masciadriTurbulenceNodes is shared by the vertical integral and directional
// LOS sampling. Keeping the node construction in one place is important: the
// two products must use exactly the same potential-temperature gradient and
// native TKE interpretation in the model-resolved boundary layer.
func masciadriTurbulenceNodes(modelLevels []CloudLevel, surfaceElevationM, boundaryLayerTopAGLM, groundCn2Scale float64) ([]turbulenceNode, bool) {
	if len(modelLevels) < 3 || !finite(surfaceElevationM) || !finite(boundaryLayerTopAGLM) || boundaryLayerTopAGLM <= 0 ||
		!finite(groundCn2Scale) || groundCn2Scale <= 0 {
		return nil, false
	}
	levels := append([]CloudLevel(nil), modelLevels...)
	sort.Slice(levels, func(i, j int) bool { return levels[i].HeightM < levels[j].HeightM })
	topM := surfaceElevationM + boundaryLayerTopAGLM

	// Keep the contiguous near-surface chain only. Sparse levels above it are
	// useful for the cloud chart, but are deliberately not differentiated.
	chain := make([]CloudLevel, 0, len(levels))
	for _, level := range levels {
		if level.HeightM <= surfaceElevationM {
			continue
		}
		if len(chain) > 0 && absInt(chain[len(chain)-1].ModelLevel-level.ModelLevel) != 1 {
			break
		}
		chain = append(chain, level)
	}
	if len(chain) < 3 || chain[0].HeightM-surfaceElevationM > 100 || chain[len(chain)-1].HeightM < topM {
		return nil, false
	}
	theta := make([]float64, len(chain))
	for index, level := range chain {
		if !validProfileTemperature(level.TemperatureK) || !finite(level.PressureHPA) || level.PressureHPA <= 0 ||
			!finite(level.TKEJkg) || level.TKEJkg < 0 || !finite(level.UMS) || !finite(level.VMS) {
			return nil, false
		}
		theta[index] = level.TemperatureK * math.Pow(1000/level.PressureHPA, dryAirPoisson)
	}
	nodes := make([]turbulenceNode, len(chain))
	for index, level := range chain {
		left, right := index-1, index+1
		if left < 0 {
			left = 0
		}
		if right >= len(chain) {
			right = len(chain) - 1
		}
		dz := chain[right].HeightM - chain[left].HeightM
		if dz <= 0 {
			return nil, false
		}
		thetaGradient := math.Abs(theta[right]-theta[left]) / dz
		cn2 := groundCn2Scale * 3.35e-6 *
			math.Pow(level.PressureHPA, 2*(1-2*dryAirPoisson)) *
			math.Pow(theta[index], -10.0/3.0) *
			math.Pow(thetaGradient, 4.0/3.0) *
			math.Pow(level.TKEJkg, 2.0/3.0)
		if !finite(cn2) || cn2 < 0 {
			return nil, false
		}
		speed := math.Hypot(level.UMS, level.VMS)
		nodes[index] = turbulenceNode{
			heightM: level.HeightM, cn2: cn2,
			windWeightedCn2: cn2 * math.Pow(speed, 5.0/3.0),
			uMS:             level.UMS, vMS: level.VMS,
		}
	}
	return nodes, true
}

// HMNSP99Metrics evaluates the free-atmosphere parametrization over the whole
// supplied pressure-level profile. The wind-weighted integral is used for the
// atmospheric coherence time tau0; it is not an independent empirical wind
// penalty and therefore does not double-count changes in wind direction.
func HMNSP99Metrics(levels []VerticalLevel) OpticalTurbulenceMetrics {
	return hmnsp99MetricsAbove(levels, math.Inf(-1))
}

// hmnsp99MetricsAbove evaluates only the part of each layer above the given
// geometric height. This lets the hybrid estimator use native ICON TKE in the
// boundary layer and HMNSP99 aloft without counting the overlap twice.
func hmnsp99MetricsAbove(levels []VerticalLevel, minimumHeightM float64) OpticalTurbulenceMetrics {
	layers, validLayerFraction, expectedTopM, ok := hmnsp99TurbulenceLayersAbove(levels, minimumHeightM)
	if !ok {
		return invalidOpticalTurbulenceMetrics()
	}
	referenceHeightM := layers[0].bottomM
	profile := opticalTurbulenceProfile{
		layers:             layers,
		surfaceElevationM:  referenceHeightM,
		boundaryLayerTopM:  referenceHeightM,
		expectedTopM:       expectedTopM,
		validLayerFraction: validLayerFraction,
	}
	return opticalTurbulenceMetricsFromProfile(profile)
}

// hmnsp99TurbulenceLayersAbove preserves the HMNSP99 constant-layer kernel in
// an ordered provider-neutral profile. Invalid source layers remain gaps in
// the profile, so structural coverage can gate height-sensitive diagnostics.
func hmnsp99TurbulenceLayersAbove(levels []VerticalLevel, minimumHeightM float64) ([]opticalTurbulenceLayer, float64, float64, bool) {
	if finite(minimumHeightM) {
		levels = clipVerticalProfileAbove(levels, minimumHeightM)
	}
	if len(levels) < 2 {
		return nil, 0, 0, false
	}
	validLayers := 0
	candidateLayers := 0
	layers := make([]opticalTurbulenceLayer, 0, len(levels)-1)
	tropopauseM := thermalTropopauseHeight(levels)
	for index := 0; index+1 < len(levels); index++ {
		lower, upper := levels[index], levels[index+1]
		candidateLayers++
		cn2, ok := hmnsp99LayerCn2(lower, upper, tropopauseM)
		if !ok {
			continue
		}
		if upper.HeightM <= lower.HeightM {
			continue
		}
		// Integrate the nonlinear |V|^(5/3) moment at the native endpoints.
		// Applying the exponent only after averaging the speeds would
		// systematically underestimate JV by Jensen's inequality.
		lowerWindCn2 := cn2 * math.Pow(math.Hypot(lower.UMS, lower.VMS), 5.0/3.0)
		upperWindCn2 := cn2 * math.Pow(math.Hypot(upper.UMS, upper.VMS), 5.0/3.0)
		layers = append(layers, opticalTurbulenceLayer{
			bottomM: lower.HeightM, topM: upper.HeightM,
			bottomCn2: cn2, topCn2: cn2,
			bottomWindCn2: lowerWindCn2, topWindCn2: upperWindCn2,
		})
		validLayers++
	}
	if candidateLayers == 0 || validLayers*2 < candidateLayers || len(layers) == 0 {
		return nil, 0, 0, false
	}
	profile := opticalTurbulenceProfile{
		layers: layers, surfaceElevationM: levels[0].HeightM,
		expectedTopM: levels[len(levels)-1].HeightM,
	}
	moments, ok := profileMomentsBetween(profile, levels[0].HeightM, levels[len(levels)-1].HeightM)
	if !ok || moments.integratedCn2 <= 0 {
		return nil, 0, 0, false
	}
	return layers, float64(validLayers) / float64(candidateLayers), levels[len(levels)-1].HeightM, true
}

// hmnsp99LayerCn2 evaluates the constant layer value used by HMNSP99. Both
// zenith integration and the horizon midpoint sampler call this kernel, so a
// directional product does not silently acquire another shear parametrization.
func hmnsp99LayerCn2(lower, upper VerticalLevel, tropopauseM float64) (float64, bool) {
	if !validProfileTemperature(lower.TemperatureK) || !validProfileTemperature(upper.TemperatureK) ||
		!finite(lower.UMS) || !finite(lower.VMS) || !finite(upper.UMS) || !finite(upper.VMS) {
		return 0, false
	}
	dz := upper.HeightM - lower.HeightM
	if dz <= 0 {
		return 0, false
	}
	pressure := (lower.PressureHPA + upper.PressureHPA) / 2
	temperature := (lower.TemperatureK + upper.TemperatureK) / 2
	lowerTheta := potentialTemperature(lower.TemperatureK, lower.PressureHPA)
	upperTheta := potentialTemperature(upper.TemperatureK, upper.PressureHPA)
	thetaGradient := (upperTheta - lowerTheta) / dz
	temperatureGradient := (upper.TemperatureK - lower.TemperatureK) / dz
	shear := math.Hypot(upper.UMS-lower.UMS, upper.VMS-lower.VMS) / dz

	// Ruggiero & DeBenedictis' HMNSP99 parametrization gives L0^(4/3).
	// The coefficient switch follows a thermal tropopause diagnosed from the
	// same temperature profile, with 200 hPa only as a fallback.
	exponent := 0.362 + 16.728*shear - 192.347*temperatureGradient
	layerHeight := (lower.HeightM + upper.HeightM) / 2
	if (finite(tropopauseM) && layerHeight >= tropopauseM) || (!finite(tropopauseM) && pressure < 200) {
		exponent = 0.757 + 13.819*shear - 57.784*temperatureGradient
	}
	outerScalePow := math.Pow(0.1, 4.0/3.0) * math.Pow(10, exponent)
	potentialRefractiveGradient := -79e-6 * pressure / (temperature * temperature) * thetaGradient
	cn2 := 2.8 * outerScalePow * potentialRefractiveGradient * potentialRefractiveGradient
	if !finite(cn2) || cn2 < 0 {
		return 0, false
	}
	return cn2, true
}

func clipVerticalProfileAbove(levels []VerticalLevel, minimumHeightM float64) []VerticalLevel {
	if len(levels) == 0 || minimumHeightM > levels[len(levels)-1].HeightM {
		return nil
	}
	if minimumHeightM <= levels[0].HeightM {
		return levels
	}
	index := sort.Search(len(levels), func(index int) bool { return levels[index].HeightM >= minimumHeightM })
	if index >= len(levels) {
		return nil
	}
	if levels[index].HeightM == minimumHeightM {
		return levels[index:]
	}
	lower, upper := levels[index-1], levels[index]
	fraction := (minimumHeightM - lower.HeightM) / (upper.HeightM - lower.HeightM)
	cut := VerticalLevel{
		PressureHPA:  interpolatePositiveLog(lower.PressureHPA, upper.PressureHPA, fraction),
		HeightM:      minimumHeightM,
		TemperatureK: interpolateFinite(lower.TemperatureK, upper.TemperatureK, fraction),
		UMS:          interpolateFinite(lower.UMS, upper.UMS, fraction),
		VMS:          interpolateFinite(lower.VMS, upper.VMS, fraction),
	}
	result := make([]VerticalLevel, 1, len(levels)-index+1)
	result[0] = cut
	return append(result, levels[index:]...)
}

func opticalTurbulenceMetricsFromProfile(profile opticalTurbulenceProfile) OpticalTurbulenceMetrics {
	if len(profile.layers) == 0 || !finite(profile.surfaceElevationM) || !finite(profile.expectedTopM) ||
		profile.expectedTopM <= profile.surfaceElevationM {
		return invalidOpticalTurbulenceMetrics()
	}
	moments, ok := profileMomentsBetween(profile, profile.surfaceElevationM, profile.expectedTopM)
	if !ok || moments.integratedCn2 <= 0 {
		return invalidOpticalTurbulenceMetrics()
	}
	metrics := opticalTurbulenceMetricsFromMoments(
		moments.integratedCn2,
		moments.windWeightedCn2,
		profile.validLayerFraction,
	)
	if !finite(metrics.SeeingArcsec) {
		return invalidOpticalTurbulenceMetrics()
	}
	metrics.HeightWeightedCn2 = moments.heightWeightedCn2
	if moments.heightWeightedCn2 > 0 {
		metrics.EffectiveTurbulenceHeightM = math.Pow(moments.heightWeightedCn2/moments.integratedCn2, 3.0/5.0)
		wavenumber := 2 * math.Pi / seeingWavelengthM
		metrics.IsoplanaticAngleArcsec = math.Pow(
			isoplanaticPhaseStructureCoefficient*wavenumber*wavenumber*moments.heightWeightedCn2,
			-3.0/5.0,
		) * radiansToArcsec
	} else {
		// A synthetic profile entirely at the aperture has no angular
		// anisoplanatism. Keep diagnostics finite for downstream JSON.
		metrics.EffectiveTurbulenceHeightM = 0
		metrics.IsoplanaticAngleArcsec = 1e6
	}
	metrics.EffectiveWindSpeedMS = math.Pow(moments.windWeightedCn2/moments.integratedCn2, 3.0/5.0)

	boundaryTopM := clamp(profile.boundaryLayerTopM, profile.surfaceElevationM, profile.expectedTopM)
	boundaryMoments := opticalTurbulenceMoments{}
	if boundaryTopM > profile.surfaceElevationM {
		boundaryMoments, ok = profileMomentsBetween(profile, profile.surfaceElevationM, boundaryTopM)
		if !ok {
			return invalidOpticalTurbulenceMetrics()
		}
	}
	metrics.BoundaryLayerCn2 = boundaryMoments.integratedCn2
	metrics.BoundaryLayerFraction = clamp(boundaryMoments.integratedCn2/moments.integratedCn2, 0, 1)
	metrics.GroundLayerCn2 = metrics.BoundaryLayerCn2
	metrics.GroundLayerFraction = metrics.BoundaryLayerFraction
	metrics.FracGL250 = fixedGroundLayerFraction(profile, moments.integratedCn2, 250)
	metrics.FracGL500 = fixedGroundLayerFraction(profile, moments.integratedCn2, 500)
	metrics.FracGL1000 = fixedGroundLayerFraction(profile, moments.integratedCn2, 1000)
	free500, freeOK := profileMomentsBetween(
		profile,
		math.Min(profile.surfaceElevationM+500, profile.expectedTopM),
		profile.expectedTopM,
	)
	if freeOK {
		metrics.FreeAtmosphereSeeing500MArcsec = seeingArcsecFromIntegratedCn2(free500.integratedCn2)
	}
	metrics.ProfileVerticalCoverage, metrics.ProfileHeightMomentCoverage = profileStructuralCoverage(profile, moments)
	metrics.ProfileTopAGLM = profile.expectedTopM - profile.surfaceElevationM
	metrics.ProfileQuality = OpticalTurbulenceProfileLimited
	if metrics.ProfileVerticalCoverage >= 0.99 && metrics.ProfileHeightMomentCoverage >= 0.99 &&
		metrics.ProfileTopAGLM >= minimumCompleteTurbulenceProfileTopAGLM {
		metrics.ProfileQuality = OpticalTurbulenceProfileModelDomainComplete
	}
	return metrics
}

func fixedGroundLayerFraction(profile opticalTurbulenceProfile, totalCn2, topAGLM float64) float64 {
	if totalCn2 <= 0 {
		return math.NaN()
	}
	topM := math.Min(profile.surfaceElevationM+topAGLM, profile.expectedTopM)
	moments, ok := profileMomentsBetween(profile, profile.surfaceElevationM, topM)
	if !ok {
		return math.NaN()
	}
	return clamp(moments.integratedCn2/totalCn2, 0, 1)
}

func seeingArcsecFromIntegratedCn2(integratedCn2 float64) float64 {
	if !finite(integratedCn2) || integratedCn2 < 0 {
		return math.NaN()
	}
	if integratedCn2 == 0 {
		return 0
	}
	wavenumber := 2 * math.Pi / seeingWavelengthM
	r0M := math.Pow(friedR0Coefficient*wavenumber*wavenumber*integratedCn2, -3.0/5.0)
	return friedSeeingCoefficient * seeingWavelengthM / r0M * radiansToArcsec
}

func opticalTurbulenceMetricsFromMoments(integratedCn2, windWeightedCn2, validLayerFraction float64) OpticalTurbulenceMetrics {
	if !finite(integratedCn2) || integratedCn2 <= 0 || !finite(windWeightedCn2) || windWeightedCn2 < 0 {
		return invalidOpticalTurbulenceMetrics()
	}
	wavenumber := 2 * math.Pi / seeingWavelengthM
	friedParameterM := math.Pow(friedR0Coefficient*wavenumber*wavenumber*integratedCn2, -3.0/5.0)
	seeingRadians := friedSeeingCoefficient * seeingWavelengthM / friedParameterM
	// A perfectly motionless synthetic profile has an unbounded tau0. Keep a
	// large finite sentinel so JSON diagnostics remain valid; real ICON columns
	// always have a positive wind-weighted moment.
	coherenceTimeMS := 1e6
	if windWeightedCn2 > 0 {
		coherenceTimeMS = 1000 * math.Pow(
			coherencePhaseStructureCoefficient*wavenumber*wavenumber*windWeightedCn2,
			-3.0/5.0,
		)
	}
	return OpticalTurbulenceMetrics{
		SeeingArcsec:                   seeingRadians * radiansToArcsec,
		CoherenceTimeMS:                coherenceTimeMS,
		IsoplanaticAngleArcsec:         math.NaN(),
		IntegratedCn2:                  integratedCn2,
		WindWeightedCn2:                windWeightedCn2,
		HeightWeightedCn2:              math.NaN(),
		EffectiveTurbulenceHeightM:     math.NaN(),
		EffectiveWindSpeedMS:           math.Pow(windWeightedCn2/integratedCn2, 3.0/5.0),
		BoundaryLayerCn2:               math.NaN(),
		BoundaryLayerFraction:          math.NaN(),
		FracGL250:                      math.NaN(),
		FracGL500:                      math.NaN(),
		FracGL1000:                     math.NaN(),
		FreeAtmosphereSeeing500MArcsec: math.NaN(),
		ProfileVerticalCoverage:        0,
		ProfileHeightMomentCoverage:    0,
		ProfileTopAGLM:                 math.NaN(),
		ProfileQuality:                 OpticalTurbulenceProfileUnavailable,
		GroundLayerCn2:                 0,
		GroundLayerFraction:            0,
		ValidLayerFraction:             clamp(validLayerFraction, 0, 1),
	}
}

func invalidOpticalTurbulenceMetrics() OpticalTurbulenceMetrics {
	return OpticalTurbulenceMetrics{
		SeeingArcsec:                   math.NaN(),
		CoherenceTimeMS:                math.NaN(),
		IsoplanaticAngleArcsec:         math.NaN(),
		IntegratedCn2:                  math.NaN(),
		WindWeightedCn2:                math.NaN(),
		HeightWeightedCn2:              math.NaN(),
		EffectiveTurbulenceHeightM:     math.NaN(),
		EffectiveWindSpeedMS:           math.NaN(),
		BoundaryLayerCn2:               math.NaN(),
		BoundaryLayerFraction:          math.NaN(),
		FracGL250:                      math.NaN(),
		FracGL500:                      math.NaN(),
		FracGL1000:                     math.NaN(),
		FreeAtmosphereSeeing500MArcsec: math.NaN(),
		ProfileVerticalCoverage:        0,
		ProfileHeightMomentCoverage:    0,
		ProfileTopAGLM:                 math.NaN(),
		ProfileQuality:                 OpticalTurbulenceProfileUnavailable,
		GroundLayerCn2:                 math.NaN(),
		GroundLayerFraction:            math.NaN(),
		ValidLayerFraction:             0,
	}
}

func absInt(value int) int {
	if value < 0 {
		return -value
	}
	return value
}

func potentialTemperature(temperatureK, pressureHPA float64) float64 {
	return temperatureK * math.Pow(1000/pressureHPA, 0.286)
}

// interpolatePositiveLog reconstructs a positive pressure-like primitive
// linearly in log space. For hydrostatic layers this preserves the exponential
// pressure-height relation, returns the exact endpoints, and remains positive
// and monotone for every fraction in [0,1]. Invalid inputs fail closed as NaN.
func interpolatePositiveLog(lower, upper, fraction float64) float64 {
	if !finite(lower) || !finite(upper) || lower <= 0 || upper <= 0 ||
		!finite(fraction) || fraction < 0 || fraction > 1 {
		return math.NaN()
	}
	if fraction == 0 {
		return lower
	}
	if fraction == 1 {
		return upper
	}
	return math.Exp(math.Log(lower) + fraction*math.Log(upper/lower))
}

func validProfileTemperature(value float64) bool {
	return finite(value) && value >= 150 && value <= 350
}

const wmoLapseLimitHeightPerK = 500.0

// wmoFloatExpansion retains an exact sum of binary64 components using the
// error-free TwoSum/TwoProduct transforms. The WMO comparisons sit directly
// on a cancellation boundary, so evaluating an algebraically equivalent
// quotient is not sufficiently reproducible for path partitioning.
type wmoFloatExpansion struct {
	components [16]float64
	length     int
}

func (expansion *wmoFloatExpansion) add(value float64) {
	carry := value
	next := [16]float64{}
	length := 0
	for index := 0; index < expansion.length; index++ {
		sum, residual := wmoTwoSum(carry, expansion.components[index])
		if residual != 0 {
			next[length] = residual
			length++
		}
		carry = sum
	}
	if carry != 0 || length == 0 {
		next[length] = carry
		length++
	}
	expansion.components = next
	expansion.length = length
}

func (expansion wmoFloatExpansion) value() float64 {
	result := 0.0
	for index := 0; index < expansion.length; index++ {
		result += expansion.components[index]
	}
	return result
}

func wmoTwoSum(left, right float64) (float64, float64) {
	sum := left + right
	rightVirtual := sum - left
	leftVirtual := sum - rightVirtual
	leftResidual := left - leftVirtual
	rightResidual := right - rightVirtual
	return sum, leftResidual + rightResidual
}

func (expansion *wmoFloatExpansion) addProduct(left, right float64) {
	product := left * right
	residual := math.FMA(left, right, -product)
	expansion.add(residual)
	expansion.add(product)
}

// CompensatedDifferenceResidual returns minuend-subtrahend-offset without
// losing a small signed residual to cancellation of the source-scale values.
// It is exported only because the ICON-EU path planner must partition the
// exact same provider-neutral decision used by the science kernel.
func CompensatedDifferenceResidual(minuend, subtrahend, offset float64) float64 {
	expansion := wmoFloatExpansion{}
	expansion.add(minuend)
	expansion.add(-subtrahend)
	expansion.add(-offset)
	return expansion.value()
}

// WMOThermalLapseResidual evaluates the WMO 2 K/km threshold in metres:
//
//	R = (z_upper-z_lower) + 500*(T_upper-T_lower).
//
// For an upward ordered pair, R >= 0 is exactly the same decision as a mean
// lapse rate <= 2 K/km. The factor 500 is exactly representable in binary64;
// the expansion also retains the product residual from the fused operation.
func WMOThermalLapseResidual(lowerHeightM, upperHeightM, lowerTemperatureK, upperTemperatureK float64) float64 {
	expansion := wmoFloatExpansion{}
	expansion.add(upperHeightM)
	expansion.add(-lowerHeightM)
	expansion.addProduct(wmoLapseLimitHeightPerK, upperTemperatureK)
	expansion.addProduct(-wmoLapseLimitHeightPerK, lowerTemperatureK)
	return expansion.value()
}

// thermalTropopauseHeight applies the WMO first-tropopause lapse-rate test to
// the available native levels. The discrete contract is conservative: the
// profile must extend at least 2 km above a candidate, and the mean lapse from
// the candidate to every following native level through the first level at or
// above 2 km must not exceed 2 K/km. Checking only that final level can hide an
// intervening layer that violates the WMO "average lapse ... at any point"
// condition; accepting a profile ending at 1.5 km is likewise insufficient.
func thermalTropopauseHeight(levels []VerticalLevel) float64 {
	index := thermalTropopauseLevelIndex(levels)
	if index < 0 {
		return math.NaN()
	}
	return levels[index].HeightM
}

func thermalTropopauseLevelIndex(levels []VerticalLevel) int {
	for index := 0; index+1 < len(levels); index++ {
		if CompensatedDifferenceResidual(levels[index].HeightM, 0, 5000) < 0 ||
			!validProfileTemperature(levels[index].TemperatureK) {
			continue
		}
		dz := CompensatedDifferenceResidual(levels[index+1].HeightM, levels[index].HeightM, 0)
		if dz <= 0 || !validProfileTemperature(levels[index+1].TemperatureK) {
			continue
		}
		if WMOThermalLapseResidual(
			levels[index].HeightM, levels[index+1].HeightM,
			levels[index].TemperatureK, levels[index+1].TemperatureK,
		) < 0 {
			continue
		}
		top := index + 1
		for top < len(levels) &&
			CompensatedDifferenceResidual(levels[top].HeightM, levels[index].HeightM, 2000) < 0 {
			top++
		}
		if top >= len(levels) {
			continue
		}
		allMeansValid := true
		for upper := index + 1; upper <= top; upper++ {
			deltaHeightM := CompensatedDifferenceResidual(levels[upper].HeightM, levels[index].HeightM, 0)
			if deltaHeightM <= 0 || !validProfileTemperature(levels[upper].TemperatureK) {
				allMeansValid = false
				break
			}
			if WMOThermalLapseResidual(
				levels[index].HeightM, levels[upper].HeightM,
				levels[index].TemperatureK, levels[upper].TemperatureK,
			) < 0 {
				allMeansValid = false
				break
			}
		}
		if allMeansValid {
			return index
		}
	}
	return -1
}
