package forecast

import (
	"context"
	"fmt"
	"math"
	"strings"
)

var astrodomeDOPRIA = [7][7]float64{
	{},
	{1.0 / 5.0},
	{3.0 / 40.0, 9.0 / 40.0},
	{44.0 / 45.0, -56.0 / 15.0, 32.0 / 9.0},
	{19372.0 / 6561.0, -25360.0 / 2187.0, 64448.0 / 6561.0, -212.0 / 729.0},
	{9017.0 / 3168.0, -355.0 / 33.0, 46732.0 / 5247.0, 49.0 / 176.0, -5103.0 / 18656.0},
	{35.0 / 384.0, 0, 500.0 / 1113.0, 125.0 / 192.0, -2187.0 / 6784.0, 11.0 / 84.0},
}

var astrodomeDOPRIB5 = [7]float64{35.0 / 384.0, 0, 500.0 / 1113.0, 125.0 / 192.0, -2187.0 / 6784.0, 11.0 / 84.0, 0}
var astrodomeDOPRIB4 = [7]float64{5179.0 / 57600.0, 0, 7571.0 / 16695.0, 393.0 / 640.0, -92097.0 / 339200.0, 187.0 / 2100.0, 1.0 / 40.0}

var astrodomeDOPRIC = [7]float64{0, 1.0 / 5.0, 3.0 / 10.0, 4.0 / 5.0, 8.0 / 9.0, 1, 1}

// Shampine's quartic continuous extension for the Dormand--Prince 5(4)
// pair, in the same seven-stage ordering as astrodomeDOPRIA. It is used for
// physical event roots and science sampling along an accepted curved ray.
var astrodomeDOPRIDenseP = [7][4]float64{
	{1, -8048581381.0 / 2820520608.0, 8663915743.0 / 2820520608.0, -12715105075.0 / 11282082432.0},
	{},
	{0, 131558114200.0 / 32700410799.0, -68118460800.0 / 10900136933.0, 87487479700.0 / 32700410799.0},
	{0, -1754552775.0 / 470086768.0, 14199869525.0 / 1410260304.0, -10690763975.0 / 1880347072.0},
	{0, 127303824393.0 / 49829197408.0, -318862633887.0 / 49829197408.0, 701980252875.0 / 199316789632.0},
	{0, -282668133.0 / 205662961.0, 2019193451.0 / 616988883.0, -1453857185.0 / 822651844.0},
	{0, 40617522.0 / 29380423.0, -110615467.0 / 29380423.0, 69997945.0 / 29380423.0},
}

type astrodomeRefractionStep struct {
	candidate        astrodomeRefractionState
	segment          astrodomeRefractionDenseSegment
	endSample        AstrodomeRefractionFieldSample
	stageSamples     [7]AstrodomeRefractionFieldSample
	errorRatio       float64
	tangentNormError float64
	partitionCrossed bool
}

func astrodomeIntegrateRefractionToTop(
	ctx context.Context,
	field AstrodomeRefractionField,
	initial AstrodomeRay,
	calibration AstrodomeRefractionCalibration,
	toleranceScale float64,
) (astrodomeRefractionPass, error) {
	state := astrodomeInitialRefractionState(initial)
	return astrodomeIntegrateRefraction(ctx, field, state, 0, true, calibration, toleranceScale)
}

func astrodomeIntegrateRefractionFixedLength(
	ctx context.Context,
	field AstrodomeRefractionField,
	initial astrodomeRefractionState,
	pathLengthM float64,
	calibration AstrodomeRefractionCalibration,
	toleranceScale float64,
) (astrodomeRefractionPass, error) {
	if !finite(pathLengthM) || pathLengthM <= 0 {
		return astrodomeRefractionPass{}, fmt.Errorf("fixed refraction path length must be positive")
	}
	return astrodomeIntegrateRefraction(ctx, field, initial, pathLengthM, false, calibration, toleranceScale)
}

func astrodomeIntegrateRefraction(
	ctx context.Context,
	field AstrodomeRefractionField,
	initial astrodomeRefractionState,
	fixedPathLengthM float64,
	stopAtTop bool,
	calibration AstrodomeRefractionCalibration,
	toleranceScale float64,
) (astrodomeRefractionPass, error) {
	if !finite(toleranceScale) || toleranceScale <= 0 || toleranceScale > 1 {
		return astrodomeRefractionPass{}, fmt.Errorf("refraction tolerance scale must be in (0,1]")
	}
	eventPathToleranceM := toleranceScale * calibration.EventPathToleranceM
	partitionTransitionToleranceM := toleranceScale * calibration.PartitionTransitionToleranceM
	pass := astrodomeRefractionPass{}
	state := astrodomeNormaliseRefractionState(initial)
	startSample, err := astrodomeEvaluateRefractionField(ctx, field, astrodomeStatePosition(state), &pass)
	if err != nil {
		return pass, err
	}
	pass.refractivityVersion = startSample.RefractivityVersion
	if stopAtTop {
		if startSample.SignedSurfaceDistanceM < -eventPathToleranceM {
			return pass, ErrAstrodomeRefractionTerrain
		}
		if startSample.SignedModelTopDistanceM >= 0 {
			return pass, fmt.Errorf("astrodome observer is not below the ICON model top")
		}
	}
	terrainDeparted := startSample.SignedSurfaceDistanceM > calibration.TerrainDepartureClearanceM
	pathLengthM := 0.0
	stepM := calibration.InitialStepM
	for pass.diagnostics.AcceptedSteps+pass.diagnostics.RejectedSteps < calibration.MaximumSteps {
		if err := ctx.Err(); err != nil {
			return pass, err
		}
		remaining := calibration.MaximumPathLengthM - pathLengthM
		if !stopAtTop {
			remaining = fixedPathLengthM - pathLengthM
			if remaining <= eventPathToleranceM*1e-6 {
				pass.finalState = state
				pass.pathLengthM = fixedPathLengthM
				pass.finalRefractiveIndex = startSample.RefractiveIndex
				return pass, nil
			}
		}
		if remaining <= 0 {
			return pass, fmt.Errorf("%w: ray exceeded maximum path length", ErrAstrodomeRefractionNonConvergence)
		}
		stepM = math.Min(stepM, remaining)
		step, stepErr := astrodomeDOPRIStep(ctx, field, state, pathLengthM, stepM, calibration, toleranceScale, &pass)
		if stepErr != nil {
			return pass, stepErr
		}
		// Internal ICON partitions mark a C0 refractivity field whose spatial
		// derivative changes branch. They are not terrain/model-top roots. Bound
		// the one step that straddles such a branch independently, while retaining
		// the embedded DOPRI error test below. True top and terrain events continue
		// to use the much tighter eventPathToleranceM.
		if step.partitionCrossed && stepM > partitionTransitionToleranceM {
			pass.diagnostics.RejectedSteps++
			stepM = math.Max(calibration.MinimumStepM, 0.5*stepM)
			continue
		}
		if step.errorRatio > 1 {
			pass.diagnostics.RejectedSteps++
			if stepM <= calibration.MinimumStepM*(1+1e-12) {
				return pass, fmt.Errorf("%w: embedded RK error %.6g at minimum step", ErrAstrodomeRefractionNonConvergence, step.errorRatio)
			}
			stepM = math.Max(calibration.MinimumStepM, stepM*astrodomeRefractionStepFactor(step.errorRatio, false))
			continue
		}

		pass.diagnostics.AcceptedSteps++
		pass.maximumAcceptedErrorRatio = math.Max(pass.maximumAcceptedErrorRatio, step.errorRatio)
		pass.maximumTangentNormError = math.Max(pass.maximumTangentNormError, step.tangentNormError)
		if step.partitionCrossed {
			pass.diagnostics.PartitionEvents++
			pass.diagnostics.MaximumPartitionStepM = math.Max(pass.diagnostics.MaximumPartitionStepM, stepM)
		}
		if stopAtTop && terrainDeparted {
			intersects, terrainErr := astrodomeAcceptedRefractionStepIntersectsTerrain(
				ctx, field, step, eventPathToleranceM, &pass,
			)
			if terrainErr != nil {
				return pass, terrainErr
			}
			if intersects {
				return pass, ErrAstrodomeRefractionTerrain
			}
		}

		if stopAtTop && startSample.SignedModelTopDistanceM < 0 && step.endSample.SignedModelTopDistanceM >= 0 {
			fraction, bracketM, rootErr := astrodomeRefractionDenseRoot(
				ctx, field, step.segment, eventPathToleranceM, true,
				func(sample AstrodomeRefractionFieldSample) float64 { return sample.SignedModelTopDistanceM }, &pass,
			)
			if rootErr != nil {
				return pass, rootErr
			}
			endPathM := pathLengthM + fraction*stepM
			step.segment.endPathM = endPathM
			pass.segments = append(pass.segments, step.segment)
			pass.finalState = step.segment.evaluate(endPathM)
			pass.pathLengthM = endPathM
			pass.topRootBracketM = bracketM
			finalSample, sampleErr := astrodomeEvaluateRefractionField(ctx, field, astrodomeStatePosition(pass.finalState), &pass)
			if sampleErr != nil {
				return pass, sampleErr
			}
			pass.finalRefractiveIndex = finalSample.RefractiveIndex
			return pass, nil
		}

		if stopAtTop {
			if !terrainDeparted && step.endSample.SignedSurfaceDistanceM > calibration.TerrainDepartureClearanceM {
				terrainDeparted = true
			}
			if terrainDeparted && startSample.SignedSurfaceDistanceM >= 0 && step.endSample.SignedSurfaceDistanceM < 0 {
				_, _, rootErr := astrodomeRefractionDenseRoot(
					ctx, field, step.segment, eventPathToleranceM, false,
					func(sample AstrodomeRefractionFieldSample) float64 { return sample.SignedSurfaceDistanceM }, &pass,
				)
				if rootErr != nil {
					return pass, rootErr
				}
				return pass, ErrAstrodomeRefractionTerrain
			}
		}

		pass.segments = append(pass.segments, step.segment)
		pathLengthM += stepM
		state = step.candidate
		startSample = step.endSample
		stepM = math.Min(calibration.MaximumStepM, math.Max(calibration.MinimumStepM,
			stepM*astrodomeRefractionStepFactor(step.errorRatio, true)))
	}
	return pass, fmt.Errorf("%w: maximum RK step count exceeded", ErrAstrodomeRefractionNonConvergence)
}

// astrodomeAcceptedRefractionStepIntersectsTerrain closes the event-detection
// gap left by endpoint-only sign tests. The inexpensive RK stage samples first
// identify a possible interior contact within the event tolerance. A contact
// is accepted only after the corresponding point on the accepted Shampine
// dense trajectory is evaluated;
// an internal stage is not itself treated as the published ray. The four
// unique interior DOPRI abscissae are ordered, so a negative confirmed sample
// with positive endpoints proves at least one continuous terrain crossing.
func astrodomeAcceptedRefractionStepIntersectsTerrain(
	ctx context.Context,
	field AstrodomeRefractionField,
	step astrodomeRefractionStep,
	probeToleranceM float64,
	pass *astrodomeRefractionPass,
) (bool, error) {
	for stage := 1; stage <= 4; stage++ {
		if step.stageSamples[stage].SignedSurfaceDistanceM > probeToleranceM {
			continue
		}
		pathM := step.segment.startPathM + astrodomeDOPRIC[stage]*step.segment.baseStepM
		state := step.segment.evaluate(pathM)
		sample, err := astrodomeEvaluateRefractionField(ctx, field, astrodomeStatePosition(state), pass)
		if err != nil {
			return false, err
		}
		if sample.SignedSurfaceDistanceM <= 0 {
			return true, nil
		}
	}
	return false, nil
}

func astrodomeDOPRIStep(
	ctx context.Context,
	field AstrodomeRefractionField,
	initial astrodomeRefractionState,
	pathLengthM,
	stepM float64,
	calibration AstrodomeRefractionCalibration,
	toleranceScale float64,
	pass *astrodomeRefractionPass,
) (astrodomeRefractionStep, error) {
	result := astrodomeRefractionStep{}
	for stage := range result.segment.stages {
		stageState := initial
		if stage > 0 {
			for component := range stageState {
				increment := 0.0
				for prior := 0; prior < stage; prior++ {
					increment += astrodomeDOPRIA[stage][prior] * result.segment.stages[prior][component]
				}
				stageState[component] += stepM * increment
			}
		}
		derivative, sample, err := astrodomeRefractionRHS(ctx, field, stageState, pass)
		if err != nil {
			return result, err
		}
		result.segment.stages[stage] = derivative
		result.stageSamples[stage] = sample
		if stage > 0 && sample.PartitionID != result.stageSamples[0].PartitionID {
			result.partitionCrossed = true
		}
	}

	fifth, fourth := initial, initial
	for component := range fifth {
		for stage := range result.segment.stages {
			fifth[component] += stepM * astrodomeDOPRIB5[stage] * result.segment.stages[stage][component]
			fourth[component] += stepM * astrodomeDOPRIB4[stage] * result.segment.stages[stage][component]
		}
	}
	tangentNorm := astrodomeStateTangent(fifth).Norm()
	result.tangentNormError = math.Abs(tangentNorm - 1)
	positionError := astrodomeStatePosition(fifth).subtract(astrodomeStatePosition(fourth)).Norm()
	directionError := astrodomeStateTangent(fifth).subtract(astrodomeStateTangent(fourth)).Norm()
	opticalError := math.Abs(fifth[6] - fourth[6])
	positionScale := toleranceScale * (calibration.PositionAbsoluteToleranceM + calibration.RelativeTolerance*stepM)
	directionScale := toleranceScale * (calibration.DirectionAbsoluteToleranceRad + calibration.RelativeTolerance)
	opticalScale := toleranceScale * (calibration.OpticalPathAbsoluteToleranceM + calibration.RelativeTolerance*stepM)
	result.errorRatio = math.Max(positionError/positionScale,
		math.Max(math.Max(directionError, result.tangentNormError)/directionScale, opticalError/opticalScale))
	if !finite(result.errorRatio) {
		return result, fmt.Errorf("%w: non-finite embedded RK error", ErrAstrodomeRefractionNonConvergence)
	}
	result.candidate = astrodomeNormaliseRefractionState(fifth)
	result.segment.startPathM = pathLengthM
	result.segment.endPathM = pathLengthM + stepM
	result.segment.baseStepM = stepM
	result.segment.initial = initial
	result.endSample = result.stageSamples[6]
	return result, nil
}

func astrodomeRefractionRHS(
	ctx context.Context,
	field AstrodomeRefractionField,
	state astrodomeRefractionState,
	pass *astrodomeRefractionPass,
) (astrodomeRefractionState, AstrodomeRefractionFieldSample, error) {
	position := astrodomeStatePosition(state)
	tangent := astrodomeStateTangent(state)
	tangentNorm := tangent.Norm()
	if !finite(tangentNorm) || tangentNorm <= 0 {
		return astrodomeRefractionState{}, AstrodomeRefractionFieldSample{}, fmt.Errorf("refraction tangent is invalid")
	}
	tangent = tangent.scale(1 / tangentNorm)
	sample, err := astrodomeEvaluateRefractionField(ctx, field, position, pass)
	if err != nil {
		return astrodomeRefractionState{}, AstrodomeRefractionFieldSample{}, err
	}
	parallel := sample.GradientECEF.dot(tangent)
	acceleration := sample.GradientECEF.subtract(tangent.scale(parallel)).scale(1 / sample.RefractiveIndex)
	return astrodomeRefractionState{
		tangent.X, tangent.Y, tangent.Z,
		acceleration.X, acceleration.Y, acceleration.Z,
		sample.RefractiveIndex,
	}, sample, nil
}

func astrodomeEvaluateRefractionField(
	ctx context.Context,
	field AstrodomeRefractionField,
	position AstrodomeECEFVector,
	pass *astrodomeRefractionPass,
) (AstrodomeRefractionFieldSample, error) {
	if err := ctx.Err(); err != nil {
		return AstrodomeRefractionFieldSample{}, err
	}
	sample, err := field.EvaluateAstrodomeRefraction(ctx, position)
	pass.diagnostics.FieldEvaluations++
	if err != nil {
		return AstrodomeRefractionFieldSample{}, err
	}
	if strings.TrimSpace(sample.RefractivityVersion) == "" || strings.TrimSpace(sample.PartitionID) == "" ||
		!finite(sample.RefractiveIndex) || sample.RefractiveIndex < 1 || sample.RefractiveIndex > 1.01 ||
		!finite(sample.GradientECEF.X) || !finite(sample.GradientECEF.Y) || !finite(sample.GradientECEF.Z) ||
		!finite(sample.SignedSurfaceDistanceM) || !finite(sample.SignedModelTopDistanceM) {
		return AstrodomeRefractionFieldSample{}, fmt.Errorf("astrodome refraction field returned an invalid sample")
	}
	if pass.refractivityVersion != "" && sample.RefractivityVersion != pass.refractivityVersion {
		return AstrodomeRefractionFieldSample{}, fmt.Errorf("astrodome refraction field version changed during tracing")
	}
	return sample, nil
}

func astrodomeRefractionDenseRoot(
	ctx context.Context,
	field AstrodomeRefractionField,
	segment astrodomeRefractionDenseSegment,
	toleranceM float64,
	crossingUp bool,
	value func(AstrodomeRefractionFieldSample) float64,
	pass *astrodomeRefractionPass,
) (float64, float64, error) {
	left, right := 0.0, 1.0
	for iteration := 0; iteration < 96 && (right-left)*segment.baseStepM > toleranceM; iteration++ {
		middle := 0.5 * (left + right)
		state := segment.evaluate(segment.startPathM + middle*segment.baseStepM)
		sample, err := astrodomeEvaluateRefractionField(ctx, field, astrodomeStatePosition(state), pass)
		if err != nil {
			return 0, 0, err
		}
		if (value(sample) >= 0) == crossingUp {
			right = middle
		} else {
			left = middle
		}
	}
	// Return the last point on the incident/valid side. The bracket width is
	// retained as the event-location error; returning the first point beyond
	// ICON top would require an out-of-domain primitive extrapolation.
	return left, (right - left) * segment.baseStepM, nil
}

func astrodomeRefractionStepFactor(errorRatio float64, accepted bool) float64 {
	if errorRatio <= 1e-30 {
		return 5
	}
	factor := 0.9 * math.Pow(errorRatio, -0.2)
	if accepted {
		return clampSurfaceValue(factor, 0.2, 5)
	}
	return clampSurfaceValue(factor, 0.1, 0.5)
}
