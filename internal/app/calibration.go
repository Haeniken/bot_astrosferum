package app

import (
	"bot_astrosferum/internal/config"
	"bot_astrosferum/internal/forecast"
)

// OverallCalibration maps operational configuration onto the single
// platform-neutral calibration used by the bot and the directional worker.
func OverallCalibration(value config.AlgorithmsConfig) forecast.OverallIndexCalibration {
	return forecast.OverallIndexCalibration{
		SeeingWeight: value.OverallSeeingWeight, CloudWeight: value.OverallCloudWeight,
		CoherenceTimeWeight:         value.OverallCoherenceTimeWeight,
		OpticalTurbulenceMaxPenalty: value.OverallOpticalTurbulenceMaxPenalty,
		PossibleFogFactor:           value.OverallPossibleFogFactor, HighFogFactor: value.OverallHighFogFactor,
		PrecipitationDetectMM: value.OverallPrecipitationDetectMM,
		GoodSeeingArcsec:      value.OverallGoodSeeingArcsec, BadSeeingArcsec: value.OverallBadSeeingArcsec,
		BestCoherenceTimeMS:          value.OverallBestCoherenceTimeMS,
		BadCoherenceTimeMS:           value.OverallBadCoherenceTimeMS,
		BoundaryLayerMinM:            value.OverallBoundaryLayerMinM,
		BoundaryLayerTopM:            value.OverallBoundaryLayerTopM,
		GroundCn2Scale:               value.OverallGroundCn2Scale,
		UnresolvedCloudObstruction:   value.OverallUnresolvedCloudObstruction,
		SurfaceWindMaxPenalty:        value.OverallSurfaceWindMaxPenalty,
		SurfaceWindStartMS:           value.OverallSurfaceWindStartMS,
		SurfaceWindFullMS:            value.OverallSurfaceWindFullMS,
		SurfaceGustStartMS:           value.OverallSurfaceGustStartMS,
		SurfaceGustFullMS:            value.OverallSurfaceGustFullMS,
		CloudLiquidRadiusMicrometers: value.CloudLiquidRadiusMicrometers,
		CloudIceRadiusMicrometers:    value.CloudIceRadiusMicrometers,
	}
}

// AstrodomeScienceCalibration applies the same reviewed operational
// calibration to the Astrodome physics contract. The forecast package keeps
// the fixed numerical/physical constants and validates the combined value.
func AstrodomeScienceCalibration(value config.AlgorithmsConfig) (forecast.AstrodomeScienceCalibration, error) {
	return forecast.AstrodomeScienceCalibrationWithOverall(OverallCalibration(value))
}
