package forecast

import "math"

// opticalTurbulenceLayer is a provider-neutral, piecewise-linear layer of an
// optical-turbulence profile. Heights are geometric metres above mean sea
// level; callers supply the model-surface height when evaluating AGL moments.
//
// windCn2 is Cn2*|V|^(5/3). Retaining it beside Cn2 preserves the existing
// HMNSP99/Masciadri wind convention while allowing every integrated metric to
// be derived from the same ordered profile.
type opticalTurbulenceLayer struct {
	bottomM       float64
	topM          float64
	bottomCn2     float64
	topCn2        float64
	bottomWindCn2 float64
	topWindCn2    float64
}

type opticalTurbulenceProfile struct {
	layers             []opticalTurbulenceLayer
	surfaceElevationM  float64
	boundaryLayerTopM  float64
	expectedTopM       float64
	validLayerFraction float64
}

type opticalTurbulenceMoments struct {
	integratedCn2       float64
	windWeightedCn2     float64
	heightWeightedCn2   float64
	coveredM            float64
	heightWeightCovered float64
}

// turbulenceLayersFromNodes turns the native boundary-layer nodes into the
// same piecewise-linear representation used by the free atmosphere. The first
// full model level is extended to the model surface exactly as in the previous
// direct integrator.
func turbulenceLayersFromNodes(nodes []turbulenceNode, bottomM, topM float64) ([]opticalTurbulenceLayer, float64, bool) {
	if len(nodes) == 0 || !finite(bottomM) || !finite(topM) || topM <= bottomM || nodes[0].heightM > topM {
		return nil, 0, false
	}
	layers := make([]opticalTurbulenceLayer, 0, len(nodes))
	coveredM := 0.0
	firstTop := math.Min(nodes[0].heightM, topM)
	if firstTop > bottomM {
		layers = append(layers, opticalTurbulenceLayer{
			bottomM: bottomM, topM: firstTop,
			bottomCn2: nodes[0].cn2, topCn2: nodes[0].cn2,
			bottomWindCn2: nodes[0].windWeightedCn2, topWindCn2: nodes[0].windWeightedCn2,
		})
		coveredM += firstTop - bottomM
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
		layers = append(layers, opticalTurbulenceLayer{
			bottomM:       segmentBottom,
			topM:          segmentTop,
			bottomCn2:     lower.cn2 + bottomFraction*(upper.cn2-lower.cn2),
			topCn2:        lower.cn2 + topFraction*(upper.cn2-lower.cn2),
			bottomWindCn2: lower.windWeightedCn2 + bottomFraction*(upper.windWeightedCn2-lower.windWeightedCn2),
			topWindCn2:    lower.windWeightedCn2 + topFraction*(upper.windWeightedCn2-lower.windWeightedCn2),
		})
		coveredM += segmentTop - segmentBottom
		if segmentTop >= topM {
			break
		}
	}
	return layers, coveredM, len(layers) > 0
}

func profileMomentsBetween(profile opticalTurbulenceProfile, bottomM, topM float64) (opticalTurbulenceMoments, bool) {
	if !finite(bottomM) || math.IsNaN(topM) || topM <= bottomM {
		return opticalTurbulenceMoments{}, false
	}
	var result opticalTurbulenceMoments
	previousTopM := math.Inf(-1)
	for _, layer := range profile.layers {
		if !validOpticalTurbulenceLayer(layer) || layer.bottomM < previousTopM-1e-9 {
			return opticalTurbulenceMoments{}, false
		}
		previousTopM = layer.topM
		segmentBottom := math.Max(bottomM, layer.bottomM)
		segmentTop := math.Min(topM, layer.topM)
		if segmentTop <= segmentBottom {
			continue
		}
		bottomCn2, topCn2 := interpolateLayerEndpoints(layer.bottomCn2, layer.topCn2, layer.bottomM, layer.topM, segmentBottom, segmentTop)
		bottomWindCn2, topWindCn2 := interpolateLayerEndpoints(
			layer.bottomWindCn2, layer.topWindCn2, layer.bottomM, layer.topM, segmentBottom, segmentTop,
		)
		dz := segmentTop - segmentBottom
		result.integratedCn2 += 0.5 * (bottomCn2 + topCn2) * dz
		result.windWeightedCn2 += 0.5 * (bottomWindCn2 + topWindCn2) * dz
		bottomAGLM := math.Max(0, segmentBottom-profile.surfaceElevationM)
		topAGLM := math.Max(0, segmentTop-profile.surfaceElevationM)
		result.heightWeightedCn2 += integrateLinearTimesHeightPower(bottomAGLM, topAGLM, bottomCn2, topCn2, 5.0/3.0)
		result.coveredM += dz
		result.heightWeightCovered += integrateHeightPower(bottomAGLM, topAGLM, 5.0/3.0)
	}
	if !finite(result.integratedCn2) || result.integratedCn2 < 0 ||
		!finite(result.windWeightedCn2) || result.windWeightedCn2 < 0 ||
		!finite(result.heightWeightedCn2) || result.heightWeightedCn2 < 0 {
		return opticalTurbulenceMoments{}, false
	}
	return result, true
}

func validOpticalTurbulenceLayer(layer opticalTurbulenceLayer) bool {
	return finite(layer.bottomM) && finite(layer.topM) && layer.topM > layer.bottomM &&
		finite(layer.bottomCn2) && layer.bottomCn2 >= 0 && finite(layer.topCn2) && layer.topCn2 >= 0 &&
		finite(layer.bottomWindCn2) && layer.bottomWindCn2 >= 0 && finite(layer.topWindCn2) && layer.topWindCn2 >= 0
}

func interpolateLayerEndpoints(bottomValue, topValue, bottomM, topM, segmentBottomM, segmentTopM float64) (float64, float64) {
	span := topM - bottomM
	bottomFraction := (segmentBottomM - bottomM) / span
	topFraction := (segmentTopM - bottomM) / span
	return bottomValue + bottomFraction*(topValue-bottomValue),
		bottomValue + topFraction*(topValue-bottomValue)
}

func integrateHeightPower(bottomM, topM, exponent float64) float64 {
	if topM <= bottomM || bottomM < 0 {
		return 0
	}
	power := exponent + 1
	return (math.Pow(topM, power) - math.Pow(bottomM, power)) / power
}

// integrateLinearTimesHeightPower integrates the linearly interpolated value
// f(h) times h^exponent. It avoids midpoint approximations in the h^(5/3)
// isoplanatic moment, whose upper-atmosphere weighting is especially strong.
func integrateLinearTimesHeightPower(bottomM, topM, bottomValue, topValue, exponent float64) float64 {
	if topM <= bottomM || bottomM < 0 {
		return 0
	}
	slope := (topValue - bottomValue) / (topM - bottomM)
	intercept := bottomValue - slope*bottomM
	return slope*integrateHeightPower(bottomM, topM, exponent+1) +
		intercept*integrateHeightPower(bottomM, topM, exponent)
}

func profileStructuralCoverage(profile opticalTurbulenceProfile, moments opticalTurbulenceMoments) (vertical, heightWeighted float64) {
	expectedDepthM := profile.expectedTopM - profile.surfaceElevationM
	if !finite(expectedDepthM) || expectedDepthM <= 0 {
		return 0, 0
	}
	vertical = clamp(moments.coveredM/expectedDepthM, 0, 1)
	expectedHeightWeight := integrateHeightPower(0, expectedDepthM, 5.0/3.0)
	if expectedHeightWeight > 0 {
		heightWeighted = clamp(moments.heightWeightCovered/expectedHeightWeight, 0, 1)
	}
	return vertical, heightWeighted
}
