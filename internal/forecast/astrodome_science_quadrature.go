package forecast

import (
	"context"
	"fmt"
	"math"
)

type astrodomeScienceVector [astrodomeScienceIntegralCount]float64

type astrodomeScienceCloudBlockKey struct {
	cellID string
	tier   AstrodomeScienceCloudTier
}

type astrodomeScienceCompensatedSum struct {
	sum        float64
	correction float64
}

func (sum *astrodomeScienceCompensatedSum) add(value float64) {
	t := sum.sum + value
	if math.Abs(sum.sum) >= math.Abs(value) {
		sum.correction += (sum.sum - t) + value
	} else {
		sum.correction += (value - t) + sum.sum
	}
	sum.sum = t
}

func (sum astrodomeScienceCompensatedSum) value() float64 {
	return sum.sum + sum.correction
}

type astrodomeScienceCloudAccumulator struct {
	liquid                           astrodomeScienceCompensatedSum
	ice                              astrodomeScienceCompensatedSum
	liquidError                      astrodomeScienceCompensatedSum
	iceError                         astrodomeScienceCompensatedSum
	maximumCloudFractionNominal      float64
	maximumCloudFractionConservative float64
}

type astrodomeSciencePass struct {
	values                       astrodomeScienceVector
	errors                       astrodomeScienceVector
	blocks                       map[astrodomeScienceCloudBlockKey]astrodomeScienceCloudAccumulator
	subdivisions                 int
	approximationLengthM         float64
	shortPanelApproximationCount int
}

type astrodomeScienceEvaluationFunction func(context.Context, float64, astrodomeScienceAtomicInterval) (astrodomeScienceEvaluation, error)

type astrodomeSciencePanel struct {
	values                           astrodomeScienceVector
	errors                           astrodomeScienceVector
	maximumCloudFractionNominal      float64
	maximumCloudFractionConservative float64
	blockKey                         astrodomeScienceCloudBlockKey
	regime                           string
	approximationLengthM             float64
}

// Kronrod abscissae and weights for the embedded G7/K15 pair, ordered from
// the outer positive nodes to zero. Gauss nodes are xgk indices 1,3,5 and 7.
var astrodomeScienceKronrodAbscissae = [...]float64{
	0.991455371120812639206854697526329,
	0.949107912342758524526189684047851,
	0.864864423359769072789712788640926,
	0.741531185599394439863864773280788,
	0.586087235467691130294144838258730,
	0.405845151377397166906606412076961,
	0.207784955007898467600689403773245,
	0,
}

var astrodomeScienceKronrodWeights = [...]float64{
	0.022935322010529224963732008058970,
	0.063092092629978553290700663189204,
	0.104790010322250183839876322541518,
	0.140653259715525918745189590510238,
	0.169004726639267902826583426598550,
	0.190350578064785409913256402421014,
	0.204432940075298892414161999234649,
	0.209482141084727828012999174891714,
}

var astrodomeScienceGaussWeights = [...]float64{
	0.129484966168869693270611432679082,
	0.279705391489276667901467771423780,
	0.381830050505118944950369775488975,
	0.417959183673469387755102040816327,
}

// Nodes and weights for independent positive-weight Gauss--Legendre rules.
// On a short interval every sampled point remains well clear of a side-
// sensitive physical root. Successively lower pairs retain ordinary nodes on
// the complete physical panel; no node is compressed into the guarded
// interior and no moment-fitted extrapolation is used.
var astrodomeScienceGaussLegendre5Abscissae = [...]float64{
	0.906179845938663992797626878299393,
	0.538469310105683091036314420700209,
}

var astrodomeScienceGaussLegendre5Weights = [...]float64{
	0.236926885056189087514264040719917,
	0.478628670499366468041291514835638,
}

const (
	astrodomeScienceGaussLegendre5CentreWeight = 0.568888888888888888888888888888889
	astrodomeScienceGaussLegendre3Abscissa     = 0.774596669241483377035853079956480
	astrodomeScienceGaussLegendre3PairWeight   = 0.555555555555555555555555555555556
	astrodomeScienceGaussLegendre3CentreWeight = 0.888888888888888888888888888888889
	astrodomeScienceGaussLegendre2Abscissa     = 0.577350269189625764509148780501957
	astrodomeScienceGaussLegendre2PairWeight   = 1.0
	astrodomeScienceGaussLegendre1CentreWeight = 2.0
	astrodomeScienceFloatUnitRoundoff          = 0x1p-53
)

func integrateAstrodomeSciencePath(
	ctx context.Context,
	evaluate astrodomeScienceEvaluationFunction,
	intervals []astrodomeScienceAtomicInterval,
	calibration AstrodomeScienceCalibration,
	toleranceScale float64,
) (astrodomeSciencePass, error) {
	if len(intervals) == 0 || !finite(toleranceScale) || toleranceScale <= 0 {
		return astrodomeSciencePass{}, fmt.Errorf("invalid astrodome science integration request")
	}
	totalLength := 0.0
	for _, interval := range intervals {
		totalLength += interval.endM - interval.startM
	}
	if !finite(totalLength) || totalLength <= 0 {
		return astrodomeSciencePass{}, fmt.Errorf("invalid astrodome science path length")
	}
	pass := astrodomeSciencePass{blocks: make(map[astrodomeScienceCloudBlockKey]astrodomeScienceCloudAccumulator)}
	var valueSums [astrodomeScienceIntegralCount]astrodomeScienceCompensatedSum
	var errorSums [astrodomeScienceIntegralCount]astrodomeScienceCompensatedSum
	var convergenceErrorSums [astrodomeScienceIntegralCount]astrodomeScienceCompensatedSum
	var approximationLengthSum astrodomeScienceCompensatedSum
	remaining := calibration.MaximumSubdivisions
	forceIndependentRootSplit := toleranceScale < 1
	for _, interval := range intervals {
		absolute := calibration.AbsoluteTolerance.vector()
		fraction := (interval.endM - interval.startM) / totalLength
		for index := range absolute {
			absolute[index] *= toleranceScale * fraction
		}
		panels, err := integrateAstrodomeScienceInterval(
			ctx, evaluate, interval, absolute,
			calibration.RelativeTolerance*toleranceScale,
			calibration.MaximumDepth, &remaining, forceIndependentRootSplit,
		)
		if err != nil {
			return astrodomeSciencePass{}, err
		}
		for _, panel := range panels {
			for component := 0; component < astrodomeScienceIntegralCount; component++ {
				valueSums[component].add(panel.values[component])
				errorSums[component].add(panel.errors[component])
				if panel.approximationLengthM == 0 {
					convergenceErrorSums[component].add(panel.errors[component])
				}
			}
			if panel.approximationLengthM > 0 {
				approximationLengthSum.add(panel.approximationLengthM)
				pass.shortPanelApproximationCount++
			}
			block := pass.blocks[panel.blockKey]
			block.liquid.add(panel.values[astrodomeScienceLiquidExtinctionIndex])
			block.ice.add(panel.values[astrodomeScienceIceExtinctionIndex])
			block.liquidError.add(panel.errors[astrodomeScienceLiquidExtinctionIndex])
			block.iceError.add(panel.errors[astrodomeScienceIceExtinctionIndex])
			block.maximumCloudFractionNominal = math.Max(
				block.maximumCloudFractionNominal,
				panel.maximumCloudFractionNominal,
			)
			block.maximumCloudFractionConservative = math.Max(
				block.maximumCloudFractionConservative,
				panel.maximumCloudFractionConservative,
			)
			pass.blocks[panel.blockKey] = block
		}
	}
	for index := range pass.values {
		pass.values[index] = valueSums[index].value()
		pass.errors[index] = errorSums[index].value()
		if pass.values[index] < 0 && math.Abs(pass.values[index]) <= pass.errors[index] {
			pass.values[index] = 0
		}
		if !finite(pass.values[index]) || pass.values[index] < 0 || !finite(pass.errors[index]) || pass.errors[index] < 0 {
			return astrodomeSciencePass{}, fmt.Errorf("%w: invalid joint integral component %d", ErrAstrodomeScienceNonConvergence, index)
		}
		globalAllowed := calibration.AbsoluteTolerance.vector()[index]*toleranceScale +
			calibration.RelativeTolerance*toleranceScale*math.Abs(pass.values[index])
		convergenceError := convergenceErrorSums[index].value()
		if convergenceError > globalAllowed {
			return astrodomeSciencePass{}, fmt.Errorf(
				"%w: accumulated component %s error %.9g exceeds global allowance %.9g",
				ErrAstrodomeScienceNonConvergence,
				astrodomeScienceIntegralComponentName(index),
				convergenceError,
				globalAllowed,
			)
		}
	}
	pass.approximationLengthM = approximationLengthSum.value()
	if !finite(pass.approximationLengthM) || pass.approximationLengthM < 0 ||
		pass.approximationLengthM > AstrodomeScienceMaximumApproximatePathLengthM {
		return astrodomeSciencePass{}, fmt.Errorf(
			"%w: short-panel approximation length %.9g m exceeds the %.9g m publication ceiling",
			ErrAstrodomeScienceNonConvergence,
			pass.approximationLengthM,
			AstrodomeScienceMaximumApproximatePathLengthM,
		)
	}
	pass.subdivisions = calibration.MaximumSubdivisions - remaining
	return pass, nil
}

func integrateAstrodomeScienceInterval(
	ctx context.Context,
	evaluate astrodomeScienceEvaluationFunction,
	interval astrodomeScienceAtomicInterval,
	absoluteTolerance astrodomeScienceVector,
	relativeTolerance float64,
	depth int,
	remaining *int,
	forceIndependentRootSplit bool,
) ([]astrodomeSciencePanel, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	intervalLengthM := interval.endM - interval.startM
	minimumIntervalLengthM := astrodomeScienceMinimumIntervalLength(interval)
	if !finite(intervalLengthM) || intervalLengthM <= minimumIntervalLengthM {
		return nil, fmt.Errorf("%w: quadrature interval in cell %q does not clear its %.9g m endpoint/numerical floor",
			ErrAstrodomeScienceNonConvergence, interval.cellID, minimumIntervalLengthM)
	}
	if *remaining <= 0 || depth <= 0 {
		return nil, fmt.Errorf("%w: subdivision budget exhausted", ErrAstrodomeScienceNonConvergence)
	}
	// The tightened repeat must not reuse the coarse pass's complete root
	// panel, regardless of which positive-weight rule that panel would select.
	// Split each original physical/physical interval once
	// before evaluation, then clear the flag: adaptive descendants must not
	// recursively repeat this independence split.
	if forceIndependentRootSplit && intervalLengthM > AstrodomeScienceGL2MinimumEventIntervalLengthM &&
		!interval.startNumericalBoundary && !interval.endNumericalBoundary {
		midpoint, splitErr := astrodomeScienceStableIndependentSplit(interval, 6)
		if splitErr != nil {
			return nil, fmt.Errorf("%w: independent repeat split: %w", ErrAstrodomeScienceNonConvergence, splitErr)
		}
		left := interval
		left.endM = midpoint
		left.endNumericalBoundary = true
		right := interval
		right.startM = midpoint
		right.startNumericalBoundary = true
		leftMinimumLengthM := astrodomeScienceMinimumIntervalLength(left)
		rightMinimumLengthM := astrodomeScienceMinimumIntervalLength(right)
		if midpoint-interval.startM <= leftMinimumLengthM || interval.endM-midpoint <= rightMinimumLengthM {
			return nil, fmt.Errorf(
				"%w: independent repeat cannot create certified one-sided children in cell %q above floors %.9g/%.9g m",
				ErrAstrodomeScienceNonConvergence,
				interval.cellID,
				leftMinimumLengthM,
				rightMinimumLengthM,
			)
		}
		leftFraction := (midpoint - interval.startM) / intervalLengthM
		leftAbsolute, rightAbsolute := absoluteTolerance, absoluteTolerance
		for component := range leftAbsolute {
			leftAbsolute[component] *= leftFraction
			rightAbsolute[component] -= leftAbsolute[component]
		}
		leftPanels, err := integrateAstrodomeScienceInterval(
			ctx, evaluate, left, leftAbsolute, relativeTolerance, depth-1, remaining, false,
		)
		if err != nil {
			return nil, err
		}
		rightPanels, err := integrateAstrodomeScienceInterval(
			ctx, evaluate, right, rightAbsolute, relativeTolerance, depth-1, remaining, false,
		)
		if err != nil {
			return nil, err
		}
		return append(leftPanels, rightPanels...), nil
	}
	*remaining = *remaining - 1
	var panel astrodomeSciencePanel
	var err error
	ruleName := "G7/K15"
	switch {
	case interval.startNumericalBoundary && interval.endNumericalBoundary:
		ruleName = "G7/K15 (numerical child)"
		panel, err = astrodomeScienceGaussKronrod15(ctx, evaluate, interval)
	case intervalLengthM <= AstrodomeScienceGL2MinimumEventIntervalLengthM:
		ruleName = "GL1 limited midpoint"
		panel, err = astrodomeScienceGaussLegendre1Limited(ctx, evaluate, interval)
	case intervalLengthM <= AstrodomeScienceGL3MinimumEventIntervalLengthM:
		ruleName = "GL2/GL1"
		panel, err = astrodomeScienceGaussLegendre2_1(ctx, evaluate, interval)
	case intervalLengthM <= AstrodomeScienceGL5MinimumEventIntervalLengthM:
		ruleName = "GL3/GL2"
		panel, err = astrodomeScienceGaussLegendre3_2(ctx, evaluate, interval)
	case intervalLengthM <= AstrodomeScienceGK15MinimumEventIntervalLengthM:
		ruleName = "GL5/GL3"
		panel, err = astrodomeScienceGaussLegendre5_3(ctx, evaluate, interval)
	default:
		panel, err = astrodomeScienceGaussKronrod15(ctx, evaluate, interval)
	}
	if err != nil {
		return nil, err
	}
	if panel.approximationLengthM > 0 {
		return []astrodomeSciencePanel{panel}, nil
	}
	for component := 0; component < astrodomeScienceIntegralCount; component++ {
		if panel.values[component] >= 0 {
			continue
		}
		if math.Abs(panel.values[component]) > panel.errors[component] {
			return nil, fmt.Errorf(
				"%w: non-negative component %s has unsupported negative panel estimate %.9g with error %.9g",
				ErrAstrodomeScienceNonConvergence,
				astrodomeScienceIntegralComponentName(component),
				panel.values[component],
				panel.errors[component],
			)
		}
		panel.values[component] = 0
	}
	accepted := true
	worstComponent := -1
	worstRatio := -1.0
	worstAllowed := 0.0
	for component := 0; component < astrodomeScienceIntegralCount; component++ {
		allowed := absoluteTolerance[component] + relativeTolerance*math.Abs(panel.values[component])
		if panel.errors[component] > allowed {
			accepted = false
			ratio := math.Inf(1)
			if allowed > 0 {
				ratio = panel.errors[component] / allowed
			}
			if ratio > worstRatio {
				worstComponent, worstRatio, worstAllowed = component, ratio, allowed
			}
		}
	}
	if accepted {
		return []astrodomeSciencePanel{panel}, nil
	}
	midpoint := interval.startM + intervalLengthM/2
	if midpoint == interval.startM || midpoint == interval.endM {
		return nil, fmt.Errorf("%w: floating-point interval cannot be subdivided", ErrAstrodomeScienceNonConvergence)
	}
	left := interval
	left.endM = midpoint
	left.endNumericalBoundary = true
	right := interval
	right.startM = midpoint
	right.startNumericalBoundary = true
	leftMinimumLengthM := astrodomeScienceMinimumIntervalLength(left)
	rightMinimumLengthM := astrodomeScienceMinimumIntervalLength(right)
	if midpoint-interval.startM <= leftMinimumLengthM ||
		interval.endM-midpoint <= rightMinimumLengthM {
		return nil, fmt.Errorf("%w: adaptive %s interval %.9g m long in cell %q did not meet tolerance for component %s (error %.9g, allowed %.9g, ratio %.9g) and cannot be bisected above child endpoint floors %.9g/%.9g m",
			ErrAstrodomeScienceNonConvergence, ruleName, intervalLengthM, interval.cellID,
			astrodomeScienceIntegralComponentName(worstComponent), panel.errors[worstComponent], worstAllowed,
			worstRatio, leftMinimumLengthM, rightMinimumLengthM)
	}
	leftFraction := (midpoint - interval.startM) / intervalLengthM
	leftAbsolute, rightAbsolute := absoluteTolerance, absoluteTolerance
	for component := range leftAbsolute {
		leftAbsolute[component] *= leftFraction
		rightAbsolute[component] -= leftAbsolute[component]
	}
	leftPanels, err := integrateAstrodomeScienceInterval(ctx, evaluate, left, leftAbsolute, relativeTolerance, depth-1, remaining, false)
	if err != nil {
		return nil, err
	}
	rightPanels, err := integrateAstrodomeScienceInterval(ctx, evaluate, right, rightAbsolute, relativeTolerance, depth-1, remaining, false)
	if err != nil {
		return nil, err
	}
	return append(leftPanels, rightPanels...), nil
}

func astrodomeScienceIntegralComponentName(component int) string {
	switch component {
	case astrodomeScienceCn2Index:
		return "integrated_cn2"
	case astrodomeScienceWindCn2Index:
		return "wind_weighted_cn2"
	case astrodomeScienceWaterIndex:
		return "slant_water"
	case astrodomeScienceLiquidExtinctionIndex:
		return "liquid_extinction"
	case astrodomeScienceIceExtinctionIndex:
		return "ice_extinction"
	default:
		return fmt.Sprintf("unknown_%d", component)
	}
}

func astrodomeScienceGaussKronrod15(ctx context.Context, evaluate astrodomeScienceEvaluationFunction, interval astrodomeScienceAtomicInterval) (astrodomeSciencePanel, error) {
	centre := (interval.startM + interval.endM) / 2
	halfLength := (interval.endM - interval.startM) / 2
	leftOuterM := centre - halfLength*astrodomeScienceKronrodAbscissae[0]
	rightOuterM := centre + halfLength*astrodomeScienceKronrodAbscissae[0]
	if (!interval.startNumericalBoundary && leftOuterM-interval.startM <= AstrodomeScienceCompoundRootSideGuardM) ||
		(!interval.endNumericalBoundary && interval.endM-rightOuterM <= AstrodomeScienceCompoundRootSideGuardM) {
		return astrodomeSciencePanel{}, fmt.Errorf("%w: represented GK15 outer nodes in cell %q do not clear the %.9g m compound-root envelope",
			ErrAstrodomeScienceIncompletePartition, interval.cellID, AstrodomeScienceCompoundRootSideGuardM)
	}
	centreValue, err := evaluate(ctx, centre, interval)
	if err != nil {
		return astrodomeSciencePanel{}, err
	}
	var kronrod, gauss astrodomeScienceVector
	for component := 0; component < astrodomeScienceIntegralCount; component++ {
		kronrod[component] = astrodomeScienceKronrodWeights[7] * centreValue.values[component]
		gauss[component] = astrodomeScienceGaussWeights[3] * centreValue.values[component]
	}
	maximumCloudFractionNominal := centreValue.cloudFractionNominal
	maximumCloudFractionConservative := centreValue.cloudFractionConservativeUpper
	for node := 0; node < 7; node++ {
		offset := halfLength * astrodomeScienceKronrodAbscissae[node]
		left, err := evaluate(ctx, centre-offset, interval)
		if err != nil {
			return astrodomeSciencePanel{}, err
		}
		right, err := evaluate(ctx, centre+offset, interval)
		if err != nil {
			return astrodomeSciencePanel{}, err
		}
		maximumCloudFractionNominal = math.Max(
			maximumCloudFractionNominal,
			math.Max(left.cloudFractionNominal, right.cloudFractionNominal),
		)
		maximumCloudFractionConservative = math.Max(
			maximumCloudFractionConservative,
			math.Max(left.cloudFractionConservativeUpper, right.cloudFractionConservativeUpper),
		)
		leftPathM, rightPathM := centre-offset, centre+offset
		if !astrodomeScienceSamePartition(centreValue, left) {
			return astrodomeSciencePanel{}, astrodomeSciencePartitionMismatchError(
				interval, leftPathM, left, centre, centreValue,
			)
		}
		if !astrodomeScienceSamePartition(centreValue, right) {
			return astrodomeSciencePanel{}, astrodomeSciencePartitionMismatchError(
				interval, centre, centreValue, rightPathM, right,
			)
		}
		for component := 0; component < astrodomeScienceIntegralCount; component++ {
			sum := left.values[component] + right.values[component]
			kronrod[component] += astrodomeScienceKronrodWeights[node] * sum
			if node == 1 || node == 3 || node == 5 {
				gauss[component] += astrodomeScienceGaussWeights[(node-1)/2] * sum
			}
		}
	}
	return astrodomeScienceFinishQuadraturePanel(
		ctx, evaluate, interval, centre, centreValue,
		maximumCloudFractionNominal, maximumCloudFractionConservative,
		kronrod, gauss, halfLength,
	)
}

func astrodomeScienceGaussLegendre5_3(
	ctx context.Context,
	evaluate astrodomeScienceEvaluationFunction,
	interval astrodomeScienceAtomicInterval,
) (astrodomeSciencePanel, error) {
	centre := (interval.startM + interval.endM) / 2
	halfLength := (interval.endM - interval.startM) / 2
	leftOuterM := centre - halfLength*astrodomeScienceGaussLegendre5Abscissae[0]
	rightOuterM := centre + halfLength*astrodomeScienceGaussLegendre5Abscissae[0]
	if (!interval.startNumericalBoundary && leftOuterM-interval.startM <= AstrodomeScienceCompoundRootSideGuardM) ||
		(!interval.endNumericalBoundary && interval.endM-rightOuterM <= AstrodomeScienceCompoundRootSideGuardM) {
		return astrodomeSciencePanel{}, fmt.Errorf("%w: represented GL5 outer nodes in cell %q do not clear the %.9g m compound-root envelope",
			ErrAstrodomeScienceIncompletePartition, interval.cellID, AstrodomeScienceCompoundRootSideGuardM)
	}
	centreValue, err := evaluate(ctx, centre, interval)
	if err != nil {
		return astrodomeSciencePanel{}, err
	}
	var higher, lower astrodomeScienceVector
	for component := 0; component < astrodomeScienceIntegralCount; component++ {
		higher[component] = astrodomeScienceGaussLegendre5CentreWeight * centreValue.values[component]
		lower[component] = astrodomeScienceGaussLegendre3CentreWeight * centreValue.values[component]
	}
	maximumCloudFractionNominal := centreValue.cloudFractionNominal
	maximumCloudFractionConservative := centreValue.cloudFractionConservativeUpper
	for node, abscissa := range astrodomeScienceGaussLegendre5Abscissae {
		offset := halfLength * abscissa
		leftPathM, rightPathM := centre-offset, centre+offset
		left, evalErr := evaluate(ctx, leftPathM, interval)
		if evalErr != nil {
			return astrodomeSciencePanel{}, evalErr
		}
		right, evalErr := evaluate(ctx, rightPathM, interval)
		if evalErr != nil {
			return astrodomeSciencePanel{}, evalErr
		}
		if !astrodomeScienceSamePartition(centreValue, left) {
			return astrodomeSciencePanel{}, astrodomeSciencePartitionMismatchError(
				interval, leftPathM, left, centre, centreValue,
			)
		}
		if !astrodomeScienceSamePartition(centreValue, right) {
			return astrodomeSciencePanel{}, astrodomeSciencePartitionMismatchError(
				interval, centre, centreValue, rightPathM, right,
			)
		}
		maximumCloudFractionNominal = math.Max(
			maximumCloudFractionNominal,
			math.Max(left.cloudFractionNominal, right.cloudFractionNominal),
		)
		maximumCloudFractionConservative = math.Max(
			maximumCloudFractionConservative,
			math.Max(left.cloudFractionConservativeUpper, right.cloudFractionConservativeUpper),
		)
		for component := 0; component < astrodomeScienceIntegralCount; component++ {
			higher[component] += astrodomeScienceGaussLegendre5Weights[node] *
				(left.values[component] + right.values[component])
		}
	}
	lowOffset := halfLength * astrodomeScienceGaussLegendre3Abscissa
	leftPathM, rightPathM := centre-lowOffset, centre+lowOffset
	left, err := evaluate(ctx, leftPathM, interval)
	if err != nil {
		return astrodomeSciencePanel{}, err
	}
	right, err := evaluate(ctx, rightPathM, interval)
	if err != nil {
		return astrodomeSciencePanel{}, err
	}
	if !astrodomeScienceSamePartition(centreValue, left) {
		return astrodomeSciencePanel{}, astrodomeSciencePartitionMismatchError(
			interval, leftPathM, left, centre, centreValue,
		)
	}
	if !astrodomeScienceSamePartition(centreValue, right) {
		return astrodomeSciencePanel{}, astrodomeSciencePartitionMismatchError(
			interval, centre, centreValue, rightPathM, right,
		)
	}
	maximumCloudFractionNominal = math.Max(
		maximumCloudFractionNominal,
		math.Max(left.cloudFractionNominal, right.cloudFractionNominal),
	)
	maximumCloudFractionConservative = math.Max(
		maximumCloudFractionConservative,
		math.Max(left.cloudFractionConservativeUpper, right.cloudFractionConservativeUpper),
	)
	for component := 0; component < astrodomeScienceIntegralCount; component++ {
		lower[component] += astrodomeScienceGaussLegendre3PairWeight *
			(left.values[component] + right.values[component])
	}
	return astrodomeScienceFinishQuadraturePanel(
		ctx, evaluate, interval, centre, centreValue,
		maximumCloudFractionNominal, maximumCloudFractionConservative,
		higher, lower, halfLength,
	)
}

func astrodomeScienceGaussLegendre3_2(
	ctx context.Context,
	evaluate astrodomeScienceEvaluationFunction,
	interval astrodomeScienceAtomicInterval,
) (astrodomeSciencePanel, error) {
	centre := (interval.startM + interval.endM) / 2
	halfLength := (interval.endM - interval.startM) / 2
	leftOuterM := centre - halfLength*astrodomeScienceGaussLegendre3Abscissa
	rightOuterM := centre + halfLength*astrodomeScienceGaussLegendre3Abscissa
	if (!interval.startNumericalBoundary && leftOuterM-interval.startM <= AstrodomeScienceCompoundRootSideGuardM) ||
		(!interval.endNumericalBoundary && interval.endM-rightOuterM <= AstrodomeScienceCompoundRootSideGuardM) {
		return astrodomeSciencePanel{}, fmt.Errorf(
			"%w: represented GL3 outer nodes in cell %q do not clear the %.9g m compound-root envelope",
			ErrAstrodomeScienceIncompletePartition, interval.cellID, AstrodomeScienceCompoundRootSideGuardM,
		)
	}
	centreValue, err := evaluate(ctx, centre, interval)
	if err != nil {
		return astrodomeSciencePanel{}, err
	}
	var higher, lower astrodomeScienceVector
	for component := 0; component < astrodomeScienceIntegralCount; component++ {
		higher[component] = astrodomeScienceGaussLegendre3CentreWeight * centreValue.values[component]
	}
	maximumCloudFractionNominal := centreValue.cloudFractionNominal
	maximumCloudFractionConservative := centreValue.cloudFractionConservativeUpper

	leftPathM := centre - halfLength*astrodomeScienceGaussLegendre3Abscissa
	rightPathM := centre + halfLength*astrodomeScienceGaussLegendre3Abscissa
	left, err := evaluate(ctx, leftPathM, interval)
	if err != nil {
		return astrodomeSciencePanel{}, err
	}
	right, err := evaluate(ctx, rightPathM, interval)
	if err != nil {
		return astrodomeSciencePanel{}, err
	}
	if !astrodomeScienceSamePartition(centreValue, left) {
		return astrodomeSciencePanel{}, astrodomeSciencePartitionMismatchError(
			interval, leftPathM, left, centre, centreValue,
		)
	}
	if !astrodomeScienceSamePartition(centreValue, right) {
		return astrodomeSciencePanel{}, astrodomeSciencePartitionMismatchError(
			interval, centre, centreValue, rightPathM, right,
		)
	}
	maximumCloudFractionNominal = math.Max(
		maximumCloudFractionNominal,
		math.Max(left.cloudFractionNominal, right.cloudFractionNominal),
	)
	maximumCloudFractionConservative = math.Max(
		maximumCloudFractionConservative,
		math.Max(left.cloudFractionConservativeUpper, right.cloudFractionConservativeUpper),
	)
	for component := 0; component < astrodomeScienceIntegralCount; component++ {
		higher[component] += astrodomeScienceGaussLegendre3PairWeight *
			(left.values[component] + right.values[component])
	}

	leftPathM = centre - halfLength*astrodomeScienceGaussLegendre2Abscissa
	rightPathM = centre + halfLength*astrodomeScienceGaussLegendre2Abscissa
	left, err = evaluate(ctx, leftPathM, interval)
	if err != nil {
		return astrodomeSciencePanel{}, err
	}
	right, err = evaluate(ctx, rightPathM, interval)
	if err != nil {
		return astrodomeSciencePanel{}, err
	}
	if !astrodomeScienceSamePartition(centreValue, left) {
		return astrodomeSciencePanel{}, astrodomeSciencePartitionMismatchError(
			interval, leftPathM, left, centre, centreValue,
		)
	}
	if !astrodomeScienceSamePartition(centreValue, right) {
		return astrodomeSciencePanel{}, astrodomeSciencePartitionMismatchError(
			interval, centre, centreValue, rightPathM, right,
		)
	}
	maximumCloudFractionNominal = math.Max(
		maximumCloudFractionNominal,
		math.Max(left.cloudFractionNominal, right.cloudFractionNominal),
	)
	maximumCloudFractionConservative = math.Max(
		maximumCloudFractionConservative,
		math.Max(left.cloudFractionConservativeUpper, right.cloudFractionConservativeUpper),
	)
	for component := 0; component < astrodomeScienceIntegralCount; component++ {
		lower[component] += astrodomeScienceGaussLegendre2PairWeight *
			(left.values[component] + right.values[component])
	}

	return astrodomeScienceFinishQuadraturePanel(
		ctx, evaluate, interval, centre, centreValue,
		maximumCloudFractionNominal, maximumCloudFractionConservative,
		higher, lower, halfLength,
	)
}

func astrodomeScienceGaussLegendre2_1(
	ctx context.Context,
	evaluate astrodomeScienceEvaluationFunction,
	interval astrodomeScienceAtomicInterval,
) (astrodomeSciencePanel, error) {
	centre := (interval.startM + interval.endM) / 2
	halfLength := (interval.endM - interval.startM) / 2
	leftOuterM := centre - halfLength*astrodomeScienceGaussLegendre2Abscissa
	rightOuterM := centre + halfLength*astrodomeScienceGaussLegendre2Abscissa
	if (!interval.startNumericalBoundary && leftOuterM-interval.startM <= AstrodomeScienceCompoundRootSideGuardM) ||
		(!interval.endNumericalBoundary && interval.endM-rightOuterM <= AstrodomeScienceCompoundRootSideGuardM) {
		return astrodomeSciencePanel{}, fmt.Errorf(
			"%w: represented GL2 outer nodes in cell %q do not clear the %.9g m compound-root envelope",
			ErrAstrodomeScienceIncompletePartition, interval.cellID, AstrodomeScienceCompoundRootSideGuardM,
		)
	}
	centreValue, err := evaluate(ctx, centre, interval)
	if err != nil {
		return astrodomeSciencePanel{}, err
	}
	var higher, lower astrodomeScienceVector
	for component := 0; component < astrodomeScienceIntegralCount; component++ {
		lower[component] = astrodomeScienceGaussLegendre1CentreWeight * centreValue.values[component]
	}
	maximumCloudFractionNominal := centreValue.cloudFractionNominal
	maximumCloudFractionConservative := centreValue.cloudFractionConservativeUpper

	leftPathM := centre - halfLength*astrodomeScienceGaussLegendre2Abscissa
	rightPathM := centre + halfLength*astrodomeScienceGaussLegendre2Abscissa
	left, err := evaluate(ctx, leftPathM, interval)
	if err != nil {
		return astrodomeSciencePanel{}, err
	}
	right, err := evaluate(ctx, rightPathM, interval)
	if err != nil {
		return astrodomeSciencePanel{}, err
	}
	if !astrodomeScienceSamePartition(centreValue, left) {
		return astrodomeSciencePanel{}, astrodomeSciencePartitionMismatchError(
			interval, leftPathM, left, centre, centreValue,
		)
	}
	if !astrodomeScienceSamePartition(centreValue, right) {
		return astrodomeSciencePanel{}, astrodomeSciencePartitionMismatchError(
			interval, centre, centreValue, rightPathM, right,
		)
	}
	maximumCloudFractionNominal = math.Max(
		maximumCloudFractionNominal,
		math.Max(left.cloudFractionNominal, right.cloudFractionNominal),
	)
	maximumCloudFractionConservative = math.Max(
		maximumCloudFractionConservative,
		math.Max(left.cloudFractionConservativeUpper, right.cloudFractionConservativeUpper),
	)
	for component := 0; component < astrodomeScienceIntegralCount; component++ {
		higher[component] += astrodomeScienceGaussLegendre2PairWeight *
			(left.values[component] + right.values[component])
	}

	return astrodomeScienceFinishQuadraturePanel(
		ctx, evaluate, interval, centre, centreValue,
		maximumCloudFractionNominal, maximumCloudFractionConservative,
		higher, lower, halfLength,
	)
}

// astrodomeScienceGaussLegendre1Limited publishes a positive midpoint point
// estimate only when a complete physical panel is too short to place the
// independent GL2 nodes outside both 0.5-mm event guards. It is deliberately
// not called "converged": the two safe one-sided probes only verify ownership
// and establish an engineering magnitude allowance; they are not used to
// interpolate a ready-made derived quantity or to claim a higher-order rule.
func astrodomeScienceGaussLegendre1Limited(
	ctx context.Context,
	evaluate astrodomeScienceEvaluationFunction,
	interval astrodomeScienceAtomicInterval,
) (astrodomeSciencePanel, error) {
	lengthM := interval.endM - interval.startM
	if !finite(lengthM) || lengthM <= astrodomeScienceMinimumIntervalLength(interval) ||
		lengthM > AstrodomeScienceGL2MinimumEventIntervalLengthM {
		return astrodomeSciencePanel{}, fmt.Errorf(
			"%w: invalid limited midpoint interval %.9g m in cell %q",
			ErrAstrodomeScienceNonConvergence,
			lengthM,
			interval.cellID,
		)
	}
	centre := interval.startM + lengthM/2
	leftM, rightM, err := astrodomeSciencePartitionInteriorEndpoints(interval)
	if err != nil {
		return astrodomeSciencePanel{}, err
	}
	if centre <= leftM || centre >= rightM {
		return astrodomeSciencePanel{}, fmt.Errorf(
			"%w: limited midpoint in cell %q does not lie inside the certified event guards",
			ErrAstrodomeScienceNonConvergence,
			interval.cellID,
		)
	}
	centreValue, err := evaluate(ctx, centre, interval)
	if err != nil {
		return astrodomeSciencePanel{}, err
	}
	leftValue, err := evaluate(ctx, leftM, interval)
	if err != nil {
		return astrodomeSciencePanel{}, err
	}
	rightValue, err := evaluate(ctx, rightM, interval)
	if err != nil {
		return astrodomeSciencePanel{}, err
	}
	if !astrodomeScienceSamePartition(centreValue, leftValue) {
		return astrodomeSciencePanel{}, astrodomeSciencePartitionMismatchError(
			interval, leftM, leftValue, centre, centreValue,
		)
	}
	if !astrodomeScienceSamePartition(centreValue, rightValue) {
		return astrodomeSciencePanel{}, astrodomeSciencePartitionMismatchError(
			interval, centre, centreValue, rightM, rightValue,
		)
	}
	panel := astrodomeSciencePanel{
		maximumCloudFractionNominal: math.Max(
			centreValue.cloudFractionNominal,
			math.Max(leftValue.cloudFractionNominal, rightValue.cloudFractionNominal),
		),
		// cloudFractionConservativeUpper is already the raw-native corner/support
		// envelope for this block, rather than the reconstructed point value.
		maximumCloudFractionConservative: math.Max(
			centreValue.cloudFractionConservativeUpper,
			math.Max(leftValue.cloudFractionConservativeUpper, rightValue.cloudFractionConservativeUpper),
		),
		blockKey:             centreValue.blockKey,
		regime:               centreValue.regime,
		approximationLengthM: lengthM,
	}
	for component := 0; component < astrodomeScienceIntegralCount; component++ {
		value := centreValue.values[component] * lengthM
		magnitude := math.Max(
			centreValue.values[component],
			math.Max(leftValue.values[component], rightValue.values[component]),
		) * lengthM
		if !finite(value) || value < 0 || !finite(magnitude) || magnitude < 0 {
			return astrodomeSciencePanel{}, fmt.Errorf(
				"%w: limited midpoint component %s is invalid",
				ErrAstrodomeScienceNonConvergence,
				astrodomeScienceIntegralComponentName(component),
			)
		}
		panel.values[component] = value
		// The symmetric interval reaches zero and at least twice the midpoint
		// estimate; its error radius also covers the largest sampled panel-equivalent
		// magnitude. This is an engineering allowance, not a rigorous enclosure of
		// unsampled behaviour.
		panel.errors[component] = math.Nextafter(math.Max(value, magnitude), math.Inf(1))
	}
	return panel, nil
}

// astrodomeScienceGuardConstrained5_3 is retained only as a narrow internal
// compatibility seam for archived tests. Production v29 must never publish a
// short physical interval by extrapolating values from its guarded interior:
// the corresponding moment fit has no finite conditioning bound as the safe
// interior collapses. A future replacement needs a proved non-negative
// interval enclosure for every integrated component.
func astrodomeScienceGuardConstrained5_3(
	_ context.Context,
	_ astrodomeScienceEvaluationFunction,
	interval astrodomeScienceAtomicInterval,
) (astrodomeSciencePanel, error) {
	return astrodomeSciencePanel{}, fmt.Errorf(
		"%w: physical interval in cell %q has no certified non-extrapolatory short-panel bound",
		ErrAstrodomeScienceNonConvergence,
		interval.cellID,
	)
}

func astrodomeScienceStableIndependentSplit(
	interval astrodomeScienceAtomicInterval,
	requiredNodesPerChild int,
) (float64, error) {
	leftSafeM, rightSafeM, err := astrodomeSciencePartitionInteriorEndpoints(interval)
	if err != nil {
		return 0, err
	}
	capacity := astrodomeScienceRepresentablePathCapacity(leftSafeM, rightSafeM)
	requiredCapacity := uint64(2*requiredNodesPerChild - 1)
	if requiredNodesPerChild < 2 || capacity < requiredCapacity {
		return 0, astrodomeScienceConstrainedCapacityError(
			interval, leftSafeM, rightSafeM, capacity, int(requiredCapacity),
		)
	}
	minimumSplitM := leftSafeM
	for count := 1; count < requiredNodesPerChild; count++ {
		minimumSplitM = math.Nextafter(minimumSplitM, math.Inf(1))
	}
	maximumSplitM := rightSafeM
	for count := 1; count < requiredNodesPerChild; count++ {
		maximumSplitM = math.Nextafter(maximumSplitM, math.Inf(-1))
	}
	midpoint := leftSafeM + (rightSafeM-leftSafeM)/2
	midpoint = math.Max(minimumSplitM, math.Min(maximumSplitM, midpoint))
	if !finite(midpoint) || midpoint <= interval.startM || midpoint >= interval.endM {
		return 0, astrodomeScienceConstrainedCapacityError(
			interval, leftSafeM, rightSafeM, capacity, int(requiredCapacity),
		)
	}
	return midpoint, nil
}

func astrodomeScienceRepresentablePathCapacity(leftPathM, rightPathM float64) uint64 {
	if !finite(leftPathM) || !finite(rightPathM) || rightPathM < leftPathM {
		return 0
	}
	leftKey := astrodomeScienceOrderedFloatKey(leftPathM)
	rightKey := astrodomeScienceOrderedFloatKey(rightPathM)
	if rightKey < leftKey {
		return 0
	}
	distance := rightKey - leftKey
	if distance == ^uint64(0) {
		return distance
	}
	return distance + 1
}

// astrodomeScienceOrderedFloatKey maps every finite binary64 value to an
// unsigned integer whose ordering is the numeric ordering. In particular,
// adjacent values across -0/+0 remain adjacent. This lets the constrained
// rule count representable path coordinates without assuming that a test or
// a future path coordinate system starts at zero.
func astrodomeScienceOrderedFloatKey(value float64) uint64 {
	bits := math.Float64bits(value)
	if bits&(uint64(1)<<63) != 0 {
		return ^bits
	}
	return bits | (uint64(1) << 63)
}

func astrodomeScienceConstrainedCapacityError(
	interval astrodomeScienceAtomicInterval,
	leftSafeM,
	rightSafeM float64,
	capacity uint64,
	required int,
) error {
	boundaryKind := "physical/physical"
	switch {
	case interval.startNumericalBoundary && interval.endNumericalBoundary:
		boundaryKind = "numerical/numerical"
	case interval.startNumericalBoundary:
		boundaryKind = "numerical/physical"
	case interval.endNumericalBoundary:
		boundaryKind = "physical/numerical"
	}
	return fmt.Errorf(
		"constrained represented-node capacity: interval=%.17g..%.17g boundary=%s safe=%.17g..%.17g span=%.9g m capacity=%d required=%d",
		interval.startM,
		interval.endM,
		boundaryKind,
		leftSafeM,
		rightSafeM,
		rightSafeM-leftSafeM,
		capacity,
		required,
	)
}

func astrodomeScienceFinishQuadraturePanel(
	ctx context.Context,
	evaluate astrodomeScienceEvaluationFunction,
	interval astrodomeScienceAtomicInterval,
	centre float64,
	centreValue astrodomeScienceEvaluation,
	maximumCloudFractionNominal float64,
	maximumCloudFractionConservative float64,
	higherOrder astrodomeScienceVector,
	lowerOrder astrodomeScienceVector,
	halfLength float64,
) (astrodomeSciencePanel, error) {
	// CLC is piecewise linear after raw reconstruction. Include one-sided
	// atomic-interval endpoints so max(CLC) is not estimated only at interior
	// quadrature nodes.
	// A one-ULP step in path length can disappear when added to the 6371-km
	// sphere radius. Use the certified metric one-sided endpoint probes, which
	// clear the same 0.5-mm physical-event guard as the quadrature nodes.
	leftEndpoint, rightEndpoint, endpointsErr := astrodomeSciencePartitionInteriorEndpoints(interval)
	if endpointsErr != nil {
		return astrodomeSciencePanel{}, endpointsErr
	}
	for _, pathM := range []float64{leftEndpoint, rightEndpoint} {
		value, endpointErr := evaluate(ctx, pathM, interval)
		if endpointErr != nil {
			return astrodomeSciencePanel{}, endpointErr
		}
		maximumCloudFractionNominal = math.Max(maximumCloudFractionNominal, value.cloudFractionNominal)
		maximumCloudFractionConservative = math.Max(
			maximumCloudFractionConservative,
			value.cloudFractionConservativeUpper,
		)
		if !astrodomeScienceSamePartition(centreValue, value) {
			if pathM < centre {
				return astrodomeSciencePanel{}, astrodomeSciencePartitionMismatchError(
					interval, pathM, value, centre, centreValue,
				)
			}
			return astrodomeSciencePanel{}, astrodomeSciencePartitionMismatchError(
				interval, centre, centreValue, pathM, value,
			)
		}
	}
	panel := astrodomeSciencePanel{
		maximumCloudFractionNominal:      maximumCloudFractionNominal,
		maximumCloudFractionConservative: maximumCloudFractionConservative,
		blockKey:                         centreValue.blockKey,
		regime:                           centreValue.regime,
	}
	for component := 0; component < astrodomeScienceIntegralCount; component++ {
		panel.values[component] = higherOrder[component] * halfLength
		panel.errors[component] = math.Abs((higherOrder[component] - lowerOrder[component]) * halfLength)
	}
	return panel, nil
}

func astrodomeScienceSamePartition(left, right astrodomeScienceEvaluation) bool {
	return left.blockKey == right.blockKey &&
		left.cloudVerticalSupport == right.cloudVerticalSupport &&
		left.regime == right.regime
}

func astrodomeSciencePartitionMismatchError(
	interval astrodomeScienceAtomicInterval,
	leftPathM float64,
	left astrodomeScienceEvaluation,
	rightPathM float64,
	right astrodomeScienceEvaluation,
) error {
	return fmt.Errorf(
		"%w in cell %q: partition at %.9g m (tier=%q cloud-support=%v regime=%q) differs from %.9g m (tier=%q cloud-support=%v regime=%q); provider must isolate every raw WMO/cloud predicate before quadrature",
		ErrAstrodomeScienceIncompletePartition,
		interval.cellID,
		leftPathM,
		left.blockKey.tier,
		left.cloudVerticalSupport,
		left.regime,
		rightPathM,
		right.blockKey.tier,
		right.cloudVerticalSupport,
		right.regime,
	)
}
