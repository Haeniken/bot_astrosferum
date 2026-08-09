package iconeu

import (
	"errors"
	"fmt"
	"math"

	"bot_astrosferum/internal/forecast"
)

// domeAstrodomeFallbackBoundaryDerivativeBounds encloses the first and second
// path derivatives of the nonlinear native 200-hPa fallback residual
//
//	f(s) = z(s) - H200(s),
//	H200 = h_l + alpha (h_u-h_l),
//	alpha = log(p_l/P200) / log(p_l/p_u).
//
// It differentiates the bilinearly reconstructed native pressure and height
// fields before forming H200. A finished fallback height is never interpolated
// between corners. The second-derivative enclosure lets the generic secant
// certificate retain a strict ancestor sign change after the residual itself
// enters the common one-millimetre evaluation envelope.
func domeAstrodomeFallbackBoundaryDerivativeBounds(
	ray forecast.AstrodomeRefractedRay,
	metric domeAstrodomeMetricBounds,
	profile domeAstrodomeTropopauseProfile,
	lowerLevel int,
	startM, endM float64,
) (float64, float64, float64, error) {
	lipschitz, err := domeAstrodomeFallbackBoundaryLipschitz(metric, profile, lowerLevel)
	if err != nil {
		return 0, 0, 0, err
	}
	upperLevel := lowerLevel + 1
	if lowerLevel < 0 || upperLevel >= len(profile.heights) ||
		len(profile.heights) != len(profile.pressures) || endM <= startM {
		return 0, 0, 0, errors.New("ICON-EU 200-hPa fallback derivative bracket is invalid")
	}

	lowerHeight, upperHeight := profile.heights[lowerLevel], profile.heights[upperLevel]
	lowerPressure, upperPressure := profile.pressures[lowerLevel], profile.pressures[upperLevel]
	pressureOperandScale := math.Max(
		domeAstrodomeCornerOperandScale(lowerPressure),
		domeAstrodomeCornerOperandScale(upperPressure),
	)
	pressureRoundoff := domeAstrodomeClearanceRoundoff(0, 0, pressureOperandScale)
	heightRoundoff := domeAstrodomeClearanceRoundoff(0, 0, domeAstrodomePhysicalHeightOperandScaleM())

	lowerPressureMin, lowerPressureMax := domeAstrodomeCornerRange(lowerPressure)
	upperPressureMin, _ := domeAstrodomeCornerRange(upperPressure)
	lowerPressureMin = math.Nextafter(lowerPressureMin-pressureRoundoff, math.Inf(-1))
	lowerPressureMax = math.Nextafter(lowerPressureMax+pressureRoundoff, math.Inf(1))
	upperPressureMin = math.Nextafter(upperPressureMin-pressureRoundoff, math.Inf(-1))
	minimumPressureGap, maximumHeightGap := math.Inf(1), 0.0
	for corner := range lowerPressure {
		minimumPressureGap = math.Min(minimumPressureGap, domeAstrodomePositiveSubLower(
			lowerPressure[corner], upperPressure[corner], pressureOperandScale,
		))
		maximumHeightGap = math.Max(maximumHeightGap, math.Nextafter(
			math.Abs(upperHeight[corner]-lowerHeight[corner])+heightRoundoff,
			math.Inf(1),
		))
	}
	if lowerPressureMin <= 0 || upperPressureMin <= 0 || lowerPressureMax <= 0 ||
		minimumPressureGap <= 0 || maximumHeightGap <= 0 ||
		!finiteDomeVolume(lowerPressureMax) || !finiteDomeVolume(maximumHeightGap) {
		return 0, 0, 0, errors.New("ICON-EU 200-hPa fallback derivative has no positive native separation")
	}
	denominatorMinimum := math.Nextafter(minimumPressureGap/lowerPressureMax, 0)
	if denominatorMinimum <= 0 || !finiteDomeVolume(denominatorMinimum) {
		return 0, 0, 0, errors.New("ICON-EU 200-hPa fallback derivative has no positive logarithmic denominator")
	}
	targetLog := math.Log(domeAstrodomeFallbackPressurePa)
	numeratorMaximum := math.Max(
		math.Abs(math.Log(lowerPressureMin)-targetLog),
		math.Abs(math.Log(lowerPressureMax)-targetLog),
	)
	logOperandScale := math.Max(
		1,
		math.Max(
			math.Abs(targetLog),
			math.Max(math.Abs(math.Log(lowerPressureMin)), math.Abs(math.Log(lowerPressureMax))),
		),
	)
	logRoundoff := domeAstrodomeClearanceRoundoff(0, 0, logOperandScale)
	numeratorMaximum = domeAstrodomePositiveAddUpper(
		numeratorMaximum,
		2*logRoundoff,
	)

	// The residual evaluator reconstructs H200 with two stable Log1p calls,
	// their subtract/divide arguments, one ratio, and the final affine height expression. Near
	// a vanishing log-pressure separation, the ratio is ill-conditioned even
	// though the exact native pressures remain strictly ordered. Enclose that
	// arithmetic amplification explicitly; the root solver must see it in
	// every sample rather than silently treating the ordinary height-scale ULP
	// allowance as sufficient.
	lowerLogError := domeAstrodomePositiveAddUpper(
		domeAstrodomePositiveDivUpper(pressureRoundoff, lowerPressureMin), logRoundoff,
	)
	upperLogError := domeAstrodomePositiveAddUpper(
		domeAstrodomePositiveDivUpper(pressureRoundoff, upperPressureMin), logRoundoff,
	)
	targetLogError := logRoundoff
	numeratorError := domeAstrodomePositiveAddUpper(
		domeAstrodomePositiveAddUpper(lowerLogError, targetLogError), logRoundoff,
	)
	denominatorError := domeAstrodomePositiveAddUpper(
		domeAstrodomePositiveAddUpper(lowerLogError, upperLogError), logRoundoff,
	)
	denominatorEvaluatedMinimum := math.Nextafter(
		denominatorMinimum-denominatorError, math.Inf(-1),
	)
	if denominatorEvaluatedMinimum <= 0 || !finiteDomeVolume(denominatorEvaluatedMinimum) {
		return 0, 0, 0, errors.New("ICON-EU 200-hPa fallback evaluation denominator is numerically unresolved")
	}
	denominatorEvaluationProductLower := math.Nextafter(
		denominatorMinimum*denominatorEvaluatedMinimum, 0,
	)
	if denominatorEvaluationProductLower <= 0 || !finiteDomeVolume(denominatorEvaluationProductLower) {
		return 0, 0, 0, errors.New("ICON-EU 200-hPa fallback evaluation denominator underflow")
	}
	alphaComputedMaximum := domeAstrodomePositiveDivUpper(
		domeAstrodomePositiveAddUpper(numeratorMaximum, numeratorError),
		denominatorEvaluatedMinimum,
	)
	alphaEvaluationError := domeAstrodomePositiveAddUpper(
		domeAstrodomePositiveDivUpper(numeratorError, denominatorEvaluatedMinimum),
		domeAstrodomePositiveDivUpper(
			domeAstrodomePositiveMulUpper(numeratorMaximum, denominatorError),
			denominatorEvaluationProductLower,
		),
	)
	alphaEvaluationError = domeAstrodomePositiveAddUpper(
		alphaEvaluationError,
		domeAstrodomeClearanceRoundoff(0, 0, alphaComputedMaximum),
	)
	heightValueError := heightRoundoff
	heightGapEvaluationError := domeAstrodomePositiveAddUpper(
		domeAstrodomePositiveAddUpper(heightValueError, heightValueError),
		domeAstrodomeClearanceRoundoff(0, 0, maximumHeightGap),
	)
	productOperandScale := domeAstrodomePositiveMulUpper(alphaComputedMaximum, maximumHeightGap)
	fallbackEvaluationError := domeAstrodomePositiveAddUpper(
		heightValueError,
		domeAstrodomePositiveAddUpper(
			domeAstrodomePositiveMulUpper(alphaEvaluationError, maximumHeightGap),
			domeAstrodomePositiveAddUpper(
				domeAstrodomePositiveMulUpper(alphaComputedMaximum, heightGapEvaluationError),
				domeAstrodomePositiveAddUpper(
					domeAstrodomeClearanceRoundoff(0, 0, productOperandScale),
					heightRoundoff,
				),
			),
		),
	)
	if !finiteDomeVolume(fallbackEvaluationError) || fallbackEvaluationError <= 0 {
		return 0, 0, 0, errors.New("ICON-EU 200-hPa fallback has no finite evaluation-error enclosure")
	}
	if fallbackEvaluationError > domeAstrodomePositionCoordinateEnvelopeM {
		return 0, 0, 0, fmt.Errorf("%w: ICON-EU 200-hPa fallback arithmetic enclosure %.9g m exceeds the %.9g m proof/evaluation contract",
			forecast.ErrAstrodomeScienceIncompletePartition,
			fallbackEvaluationError, domeAstrodomePositionCoordinateEnvelopeM)
	}

	angular, err := domeAstrodomeAngularBounds(ray, metric, startM, endM)
	if err != nil {
		return 0, 0, 0, err
	}
	fieldFirst := func(values [4]float64, operandScale float64) (float64, error) {
		if domeAstrodomeScalarFieldConstant(values) {
			return 0, nil
		}
		return domeAstrodomeScalarFieldLipschitz(metric, values, operandScale)
	}
	fieldSecond := func(values [4]float64, operandScale float64) (float64, error) {
		return domeAstrodomeBilinearFieldsSecondDerivativeBound(
			angular, [][4]float64{values}, operandScale,
		)
	}

	lowerPressureFirst, err := fieldFirst(lowerPressure, domeAstrodomeCornerOperandScale(lowerPressure))
	if err != nil {
		return 0, 0, 0, err
	}
	upperPressureFirst, err := fieldFirst(upperPressure, domeAstrodomeCornerOperandScale(upperPressure))
	if err != nil {
		return 0, 0, 0, err
	}
	lowerHeightFirst, err := fieldFirst(lowerHeight, domeAstrodomePhysicalHeightOperandScaleM())
	if err != nil {
		return 0, 0, 0, err
	}
	upperHeightFirst, err := fieldFirst(upperHeight, domeAstrodomePhysicalHeightOperandScaleM())
	if err != nil {
		return 0, 0, 0, err
	}
	lowerPressureSecond, err := fieldSecond(lowerPressure, domeAstrodomeCornerOperandScale(lowerPressure))
	if err != nil {
		return 0, 0, 0, err
	}
	upperPressureSecond, err := fieldSecond(upperPressure, domeAstrodomeCornerOperandScale(upperPressure))
	if err != nil {
		return 0, 0, 0, err
	}
	lowerHeightSecond, err := fieldSecond(lowerHeight, domeAstrodomePhysicalHeightOperandScaleM())
	if err != nil {
		return 0, 0, 0, err
	}
	upperHeightSecond, err := fieldSecond(upperHeight, domeAstrodomePhysicalHeightOperandScaleM())
	if err != nil {
		return 0, 0, 0, err
	}

	logLowerFirst := domeAstrodomePositiveDivUpper(lowerPressureFirst, lowerPressureMin)
	logUpperFirst := domeAstrodomePositiveDivUpper(upperPressureFirst, upperPressureMin)
	logLowerSecond := domeAstrodomePositiveAddUpper(
		domeAstrodomePositiveDivUpper(lowerPressureSecond, lowerPressureMin),
		domeAstrodomePositiveMulUpper(logLowerFirst, logLowerFirst),
	)
	logUpperSecond := domeAstrodomePositiveAddUpper(
		domeAstrodomePositiveDivUpper(upperPressureSecond, upperPressureMin),
		domeAstrodomePositiveMulUpper(logUpperFirst, logUpperFirst),
	)
	denominatorFirst := domeAstrodomePositiveAddUpper(logLowerFirst, logUpperFirst)
	denominatorSecond := domeAstrodomePositiveAddUpper(logLowerSecond, logUpperSecond)

	denominatorSquaredLower := math.Nextafter(denominatorMinimum*denominatorMinimum, 0)
	denominatorCubedLower := math.Nextafter(denominatorSquaredLower*denominatorMinimum, 0)
	if denominatorSquaredLower <= 0 || denominatorCubedLower <= 0 {
		return 0, 0, 0, errors.New("ICON-EU 200-hPa fallback derivative denominator underflow")
	}
	inverseFirst := domeAstrodomePositiveDivUpper(denominatorFirst, denominatorSquaredLower)
	inverseSecond := domeAstrodomePositiveAddUpper(
		domeAstrodomePositiveDivUpper(denominatorSecond, denominatorSquaredLower),
		domeAstrodomePositiveDivUpper(
			domeAstrodomePositiveMulUpper(
				2,
				domeAstrodomePositiveMulUpper(denominatorFirst, denominatorFirst),
			),
			denominatorCubedLower,
		),
	)
	alphaMaximum := domeAstrodomePositiveDivUpper(numeratorMaximum, denominatorMinimum)
	alphaFirst := domeAstrodomePositiveAddUpper(
		domeAstrodomePositiveDivUpper(logLowerFirst, denominatorMinimum),
		domeAstrodomePositiveMulUpper(numeratorMaximum, inverseFirst),
	)
	alphaSecond := domeAstrodomePositiveAddUpper(
		domeAstrodomePositiveDivUpper(logLowerSecond, denominatorMinimum),
		domeAstrodomePositiveAddUpper(
			domeAstrodomePositiveMulUpper(
				2,
				domeAstrodomePositiveMulUpper(logLowerFirst, inverseFirst),
			),
			domeAstrodomePositiveMulUpper(numeratorMaximum, inverseSecond),
		),
	)
	heightGapFirst := domeAstrodomePositiveAddUpper(lowerHeightFirst, upperHeightFirst)
	heightGapSecond := domeAstrodomePositiveAddUpper(lowerHeightSecond, upperHeightSecond)
	fallbackHeightSecond := domeAstrodomePositiveAddUpper(
		lowerHeightSecond,
		domeAstrodomePositiveAddUpper(
			domeAstrodomePositiveMulUpper(alphaSecond, maximumHeightGap),
			domeAstrodomePositiveAddUpper(
				domeAstrodomePositiveMulUpper(
					2,
					domeAstrodomePositiveMulUpper(alphaFirst, heightGapFirst),
				),
				domeAstrodomePositiveMulUpper(alphaMaximum, heightGapSecond),
			),
		),
	)
	radialSecond := domeAstrodomePositiveAddUpper(
		angular.accelerationMPerS2,
		domeAstrodomePositiveDivUpper(angular.speedSquared, angular.radiusM),
	)
	secondDerivative := domeAstrodomePositiveAddUpper(radialSecond, fallbackHeightSecond)
	if !finiteDomeVolume(lipschitz) || lipschitz <= 0 ||
		!finiteDomeVolume(secondDerivative) || secondDerivative <= 0 {
		return 0, 0, 0, errors.New("ICON-EU 200-hPa fallback has no finite derivative enclosure")
	}
	return lipschitz, secondDerivative, fallbackEvaluationError, nil
}
