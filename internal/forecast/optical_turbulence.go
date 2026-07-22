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
	radiansToArcsec                    = 206264.80624709636
	dryAirPoisson                      = 287.05 / 1004.0
)

// OpticalTurbulenceMetrics keeps the two standard integrals needed by an
// observer. Seeing describes the long-exposure blur, while coherence time
// describes how quickly the turbulent wavefront changes. Both are evaluated
// at 500 nm and at zenith.
type OpticalTurbulenceMetrics struct {
	SeeingArcsec        float64
	CoherenceTimeMS     float64
	IntegratedCn2       float64 // J = integral(Cn^2 dz), m^(1/3)
	WindWeightedCn2     float64 // integral(Cn^2 |V|^(5/3) dz)
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
	groundCn2, groundWindWeightedCn2, coveredM := integrateTurbulenceNodes(nodes, surfaceElevationM, topM)
	if coveredM/boundaryLayerTopAGLM < 0.99 || groundCn2 < 0 || groundWindWeightedCn2 < 0 {
		return invalidOpticalTurbulenceMetrics(), false
	}
	free := hmnsp99MetricsAbove(pressureLevels, topM)
	if !finite(free.IntegratedCn2) || !finite(free.WindWeightedCn2) {
		return invalidOpticalTurbulenceMetrics(), false
	}
	totalCn2 := groundCn2 + free.IntegratedCn2
	totalWindWeightedCn2 := groundWindWeightedCn2 + free.WindWeightedCn2
	metrics := opticalTurbulenceMetricsFromMoments(totalCn2, totalWindWeightedCn2, free.ValidLayerFraction)
	if !finite(metrics.SeeingArcsec) {
		return invalidOpticalTurbulenceMetrics(), false
	}
	metrics.GroundLayerCn2 = groundCn2
	if totalCn2 > 0 {
		metrics.GroundLayerFraction = groundCn2 / totalCn2
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

func integrateTurbulenceNodes(nodes []turbulenceNode, bottomM, topM float64) (integratedCn2, integratedWindCn2, coveredM float64) {
	if len(nodes) == 0 || topM <= bottomM || nodes[0].heightM > topM {
		return 0, 0, 0
	}
	// ICON's first full level is roughly 10 m AGL. Extend that value through
	// the thin unresolved slab down to the model surface.
	firstTop := math.Min(nodes[0].heightM, topM)
	if firstTop > bottomM {
		dz := firstTop - bottomM
		integratedCn2 += nodes[0].cn2 * dz
		integratedWindCn2 += nodes[0].windWeightedCn2 * dz
		coveredM += dz
	}
	for index := 0; index+1 < len(nodes); index++ {
		lower, upper := nodes[index], nodes[index+1]
		segmentBottom := math.Max(bottomM, lower.heightM)
		segmentTop := math.Min(topM, upper.heightM)
		if segmentTop <= segmentBottom || upper.heightM <= lower.heightM {
			continue
		}
		span := upper.heightM - lower.heightM
		bottomFraction := (segmentBottom - lower.heightM) / span
		topFraction := (segmentTop - lower.heightM) / span
		bottomCn2 := lower.cn2 + bottomFraction*(upper.cn2-lower.cn2)
		topCn2 := lower.cn2 + topFraction*(upper.cn2-lower.cn2)
		bottomWind := lower.windWeightedCn2 + bottomFraction*(upper.windWeightedCn2-lower.windWeightedCn2)
		topWind := lower.windWeightedCn2 + topFraction*(upper.windWeightedCn2-lower.windWeightedCn2)
		dz := segmentTop - segmentBottom
		integratedCn2 += (bottomCn2 + topCn2) * 0.5 * dz
		integratedWindCn2 += (bottomWind + topWind) * 0.5 * dz
		coveredM += dz
		if segmentTop >= topM {
			break
		}
	}
	return integratedCn2, integratedWindCn2, coveredM
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
	if finite(minimumHeightM) {
		levels = clipVerticalProfileAbove(levels, minimumHeightM)
		minimumHeightM = math.Inf(-1)
	}
	if len(levels) < 2 {
		return invalidOpticalTurbulenceMetrics()
	}
	var integratedCn2 float64
	var windWeightedCn2 float64
	validLayers := 0
	candidateLayers := 0
	tropopauseM := thermalTropopauseHeight(levels)
	for index := 0; index+1 < len(levels); index++ {
		lower, upper := levels[index], levels[index+1]
		if upper.HeightM <= minimumHeightM {
			continue
		}
		candidateLayers++
		cn2, ok := hmnsp99LayerCn2(lower, upper, tropopauseM)
		if !ok {
			continue
		}
		includedDZ := upper.HeightM - math.Max(lower.HeightM, minimumHeightM)
		if includedDZ <= 0 {
			continue
		}
		layerWindMS := (math.Hypot(lower.UMS, lower.VMS) + math.Hypot(upper.UMS, upper.VMS)) / 2
		integratedCn2 += cn2 * includedDZ
		windWeightedCn2 += cn2 * math.Pow(math.Max(0, layerWindMS), 5.0/3.0) * includedDZ
		validLayers++
	}
	if candidateLayers == 0 || validLayers*2 < candidateLayers || integratedCn2 <= 0 {
		return invalidOpticalTurbulenceMetrics()
	}
	return opticalTurbulenceMetricsFromMoments(
		integratedCn2,
		windWeightedCn2,
		float64(validLayers)/float64(candidateLayers),
	)
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
		PressureHPA:  lower.PressureHPA + fraction*(upper.PressureHPA-lower.PressureHPA),
		HeightM:      minimumHeightM,
		TemperatureK: interpolateFinite(lower.TemperatureK, upper.TemperatureK, fraction),
		UMS:          interpolateFinite(lower.UMS, upper.UMS, fraction),
		VMS:          interpolateFinite(lower.VMS, upper.VMS, fraction),
	}
	result := make([]VerticalLevel, 1, len(levels)-index+1)
	result[0] = cut
	return append(result, levels[index:]...)
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
		SeeingArcsec:        seeingRadians * radiansToArcsec,
		CoherenceTimeMS:     coherenceTimeMS,
		IntegratedCn2:       integratedCn2,
		WindWeightedCn2:     windWeightedCn2,
		GroundLayerCn2:      0,
		GroundLayerFraction: 0,
		ValidLayerFraction:  clamp(validLayerFraction, 0, 1),
	}
}

func invalidOpticalTurbulenceMetrics() OpticalTurbulenceMetrics {
	return OpticalTurbulenceMetrics{
		SeeingArcsec:        math.NaN(),
		CoherenceTimeMS:     math.NaN(),
		IntegratedCn2:       math.NaN(),
		WindWeightedCn2:     math.NaN(),
		GroundLayerCn2:      math.NaN(),
		GroundLayerFraction: math.NaN(),
		ValidLayerFraction:  0,
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

func validProfileTemperature(value float64) bool {
	return finite(value) && value >= 150 && value <= 350
}

// thermalTropopauseHeight approximates the WMO lapse-rate definition on the
// available pressure levels: the first level above 5 km with lapse rate at or
// below 2 K/km and an average lapse rate no greater than 2 K/km through the
// following 2 km.
func thermalTropopauseHeight(levels []VerticalLevel) float64 {
	for index := 0; index+1 < len(levels); index++ {
		if levels[index].HeightM < 5000 || !validProfileTemperature(levels[index].TemperatureK) {
			continue
		}
		dz := levels[index+1].HeightM - levels[index].HeightM
		if dz <= 0 || !validProfileTemperature(levels[index+1].TemperatureK) {
			continue
		}
		lapseKPerKM := -(levels[index+1].TemperatureK - levels[index].TemperatureK) / dz * 1000
		if lapseKPerKM > 2 {
			continue
		}
		top := index + 1
		for top+1 < len(levels) && levels[top].HeightM-levels[index].HeightM < 2000 {
			top++
		}
		if levels[top].HeightM-levels[index].HeightM < 1500 || !validProfileTemperature(levels[top].TemperatureK) {
			continue
		}
		meanLapse := -(levels[top].TemperatureK - levels[index].TemperatureK) /
			(levels[top].HeightM - levels[index].HeightM) * 1000
		if meanLapse <= 2 {
			return levels[index].HeightM
		}
	}
	return math.NaN()
}
