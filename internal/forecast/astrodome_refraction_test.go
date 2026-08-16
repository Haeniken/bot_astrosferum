package forecast

import (
	"context"
	"errors"
	"fmt"
	"math"
	"reflect"
	"strings"
	"testing"
)

type astrodomeAnalyticRefractionField struct {
	refractiveIndex func(AstrodomeECEFVector) float64
	gradient        func(AstrodomeECEFVector) AstrodomeECEFVector
	surfaceDistance func(AstrodomeECEFVector) float64
	topDistance     func(AstrodomeECEFVector) float64
	partition       func(AstrodomeECEFVector) string
}

func TestAstrodomeDOPRIFSALKeepsExactStepAndSkipsOneFieldEvaluation(t *testing.T) {
	t.Parallel()

	azimuth := 67.5
	initialRay, err := NewAstrodomeRay(
		Location{Latitude: 50, Longitude: 30, TimeZone: "UTC"}, 500, 45, &azimuth,
	)
	if err != nil {
		t.Fatal(err)
	}
	field := astrodomeAnalyticRefractionField{
		refractiveIndex: func(position AstrodomeECEFVector) float64 {
			return 1.00028 - 1e-8*(position.Norm()-AstrodomeICONSphereRadiusM)
		},
		gradient: func(position AstrodomeECEFVector) AstrodomeECEFVector {
			return position.scale(-1e-8 / position.Norm())
		},
		surfaceDistance: func(position AstrodomeECEFVector) float64 {
			return position.Norm() - AstrodomeICONSphereRadiusM
		},
		topDistance: func(position AstrodomeECEFVector) float64 {
			return position.Norm() - (AstrodomeICONSphereRadiusM + 30_000)
		},
	}
	calibration := DefaultAstrodomeRefractionCalibration()
	state := astrodomeNormaliseRefractionState(astrodomeInitialRefractionState(initialRay))
	preparedPass := astrodomeRefractionPass{}
	sample, err := astrodomeEvaluateRefractionField(
		context.Background(), field, astrodomeStatePosition(state), &preparedPass,
	)
	if err != nil {
		t.Fatal(err)
	}
	preparedPass.refractivityVersion = sample.RefractivityVersion
	derivative, err := astrodomeRefractionRHSFromSample(state, sample)
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := astrodomeDOPRIStep(
		context.Background(), field, state, 0, calibration.InitialStepM,
		calibration, 1, &preparedPass,
		astrodomeRefractionPreparedStart{state: state, derivative: derivative, sample: sample},
	)
	if err != nil {
		t.Fatal(err)
	}
	directPass := astrodomeRefractionPass{refractivityVersion: sample.RefractivityVersion}
	direct, err := astrodomeDOPRIStep(
		context.Background(), field, state, 0, calibration.InitialStepM,
		calibration, 1, &directPass,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(prepared, direct) {
		t.Fatalf("FSAL prepared step differs from direct step:\n prepared: %#v\n direct: %#v", prepared, direct)
	}
	if preparedPass.diagnostics.FieldEvaluations != 1+6 || directPass.diagnostics.FieldEvaluations != 7 {
		t.Fatalf("field evaluations prepared/direct = %d/%d, want 7/7 including the separately prepared sample",
			preparedPass.diagnostics.FieldEvaluations, directPass.diagnostics.FieldEvaluations)
	}

	acceptedState := prepared.candidate
	acceptedDerivative, err := astrodomeRefractionRHSFromSample(acceptedState, prepared.endSample)
	if err != nil {
		t.Fatal(err)
	}
	nextPreparedPass := astrodomeRefractionPass{refractivityVersion: sample.RefractivityVersion}
	nextPrepared, err := astrodomeDOPRIStep(
		context.Background(), field, acceptedState, calibration.InitialStepM, calibration.InitialStepM,
		calibration, 1, &nextPreparedPass,
		astrodomeRefractionPreparedStart{
			state: acceptedState, derivative: acceptedDerivative, sample: prepared.endSample,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	nextDirectPass := astrodomeRefractionPass{refractivityVersion: sample.RefractivityVersion}
	nextDirect, err := astrodomeDOPRIStep(
		context.Background(), field, acceptedState, calibration.InitialStepM, calibration.InitialStepM,
		calibration, 1, &nextDirectPass,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(nextPrepared, nextDirect) {
		t.Fatalf("accepted-step FSAL derivative changed the next Dormand--Prince step:\n prepared: %#v\n direct: %#v",
			nextPrepared, nextDirect)
	}
	if nextPreparedPass.diagnostics.FieldEvaluations != 6 || nextDirectPass.diagnostics.FieldEvaluations != 7 {
		t.Fatalf("next-step field evaluations prepared/direct = %d/%d, want 6/7",
			nextPreparedPass.diagnostics.FieldEvaluations, nextDirectPass.diagnostics.FieldEvaluations)
	}
}

func (field astrodomeAnalyticRefractionField) EvaluateAstrodomeRefraction(
	_ context.Context,
	position AstrodomeECEFVector,
) (AstrodomeRefractionFieldSample, error) {
	partition := "analytic"
	if field.partition != nil {
		partition = field.partition(position)
	}
	return AstrodomeRefractionFieldSample{
		RefractivityVersion: AstrodomeCiddorVersion,
		RefractiveIndex:     field.refractiveIndex(position), GradientECEF: field.gradient(position),
		PartitionID: partition, SignedSurfaceDistanceM: field.surfaceDistance(position),
		SignedModelTopDistanceM: field.topDistance(position),
	}, nil
}

func TestAstrodomeRefractionCalibrationReservesPartitionResolutionForFinestPass(t *testing.T) {
	t.Parallel()

	calibration := DefaultAstrodomeRefractionCalibration()
	calibration.MinimumStepM = math.Nextafter(
		math.Ldexp(calibration.PartitionTransitionToleranceM, -astrodomeRefractionMaximumRefinementLevels),
		math.Inf(1),
	)
	if err := calibration.Validate(); err == nil {
		t.Fatal("calibration accepted a minimum step larger than the finest partition-transition tolerance")
	}
}

func TestAstrodomeRefractionCalibrationKeepsDenseEventRootIndependentOfMinimumRKStep(t *testing.T) {
	t.Parallel()

	calibration := DefaultAstrodomeRefractionCalibration()
	finestEventToleranceM := math.Ldexp(
		calibration.EventPathToleranceM,
		-astrodomeRefractionMaximumRefinementLevels,
	)
	if calibration.MinimumStepM <= finestEventToleranceM {
		t.Fatalf("fixture needs minimum RK step %.12g m above dense-event tolerance %.12g m",
			calibration.MinimumStepM, finestEventToleranceM)
	}
	if err := calibration.Validate(); err != nil {
		t.Fatalf("dense-output event root was incorrectly coupled to the minimum RK step: %v", err)
	}
}

func TestAstrodomeRefractionCalibrationKeepsInternalPartitionToleranceNoTighterThanPhysicalEvents(t *testing.T) {
	t.Parallel()

	calibration := DefaultAstrodomeRefractionCalibration()
	calibration.PartitionTransitionToleranceM = math.Nextafter(calibration.EventPathToleranceM, math.Inf(-1))
	if err := calibration.Validate(); err == nil {
		t.Fatal("calibration accepted an internal partition tolerance tighter than a physical-event tolerance")
	}
}

func TestAstrodomeRefractionSeparatesContinuousPartitionsFromPhysicalEventTolerance(t *testing.T) {
	t.Parallel()

	initial, err := NewAstrodomeRay(
		Location{Latitude: 0, Longitude: 0, TimeZone: "UTC"}, 0, 90, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	const (
		topHeightM       = 3000.0
		partitionHeightM = 100.0
	)
	piecewiseRefractivity := func(heightM float64) (float64, float64) {
		level := int(math.Floor(math.Max(0, heightM) / partitionHeightM))
		value := 1.0003
		for index := 0; index < level; index++ {
			slope := -1e-8
			if index%2 != 0 {
				slope = -2e-8
			}
			value += slope * partitionHeightM
		}
		slope := -1e-8
		if level%2 != 0 {
			slope = -2e-8
		}
		value += slope * (heightM - float64(level)*partitionHeightM)
		return value, slope
	}
	field := astrodomeAnalyticRefractionField{
		refractiveIndex: func(position AstrodomeECEFVector) float64 {
			value, _ := piecewiseRefractivity(position.Norm() - AstrodomeICONSphereRadiusM)
			return value
		},
		gradient: func(position AstrodomeECEFVector) AstrodomeECEFVector {
			_, slope := piecewiseRefractivity(position.Norm() - AstrodomeICONSphereRadiusM)
			return AstrodomeECEFVector{X: slope}
		},
		surfaceDistance: func(position AstrodomeECEFVector) float64 {
			return position.Norm() - AstrodomeICONSphereRadiusM
		},
		topDistance: func(position AstrodomeECEFVector) float64 {
			return position.Norm() - (AstrodomeICONSphereRadiusM + topHeightM)
		},
		partition: func(position AstrodomeECEFVector) string {
			heightM := position.Norm() - AstrodomeICONSphereRadiusM
			return fmt.Sprintf("level-%03d", int(math.Floor(heightM/partitionHeightM)))
		},
	}
	fastCalibration := DefaultAstrodomeRefractionCalibration()
	legacyCalibration := fastCalibration
	legacyCalibration.PartitionTransitionToleranceM = legacyCalibration.EventPathToleranceM
	const toleranceScale = 0.5
	fast, err := astrodomeIntegrateRefractionToTop(
		context.Background(), field, initial, fastCalibration, toleranceScale,
	)
	if err != nil {
		t.Fatalf("bounded continuous-partition integration: %v", err)
	}
	legacy, err := astrodomeIntegrateRefractionToTop(
		context.Background(), field, initial, legacyCalibration, toleranceScale,
	)
	if err != nil {
		t.Fatalf("centimetre partition integration: %v", err)
	}
	if fast.diagnostics.PartitionEvents == 0 || legacy.diagnostics.PartitionEvents == 0 {
		t.Fatal("partition fixture did not exercise an internal transition")
	}
	if fast.diagnostics.MaximumPartitionStepM > toleranceScale*fastCalibration.PartitionTransitionToleranceM*(1+1e-12) {
		t.Fatalf("fast transition step %.9g m exceeds %.9g m",
			fast.diagnostics.MaximumPartitionStepM,
			toleranceScale*fastCalibration.PartitionTransitionToleranceM)
	}
	if fast.topRootBracketM > toleranceScale*fastCalibration.EventPathToleranceM ||
		legacy.topRootBracketM > toleranceScale*legacyCalibration.EventPathToleranceM {
		t.Fatalf("physical top event lost centimetre accuracy: fast %.9g m, legacy %.9g m",
			fast.topRootBracketM, legacy.topRootBracketM)
	}
	if endpointDelta := astrodomeStatePosition(fast.finalState).subtract(astrodomeStatePosition(legacy.finalState)).Norm(); endpointDelta > fast.topRootBracketM+legacy.topRootBracketM+1e-9 {
		t.Fatalf("continuous-branch endpoint delta %.9g m exceeds physical-event brackets %.9g+%.9g m",
			endpointDelta, fast.topRootBracketM, legacy.topRootBracketM)
	}
	if opticalDelta := math.Abs(fast.finalState[6] - legacy.finalState[6]); opticalDelta > fastCalibration.RepeatOpticalPathToleranceM {
		t.Fatalf("continuous derivative-branch optical path changed by %.9g m", opticalDelta)
	}
	if fast.diagnostics.FieldEvaluations*2 >= legacy.diagnostics.FieldEvaluations {
		t.Fatalf("separate partition tolerance did not halve field work: fast %d, legacy %d",
			fast.diagnostics.FieldEvaluations, legacy.diagnostics.FieldEvaluations)
	}
	t.Logf("continuous-partition field evaluations: separated=%d centimetre=%d",
		fast.diagnostics.FieldEvaluations, legacy.diagnostics.FieldEvaluations)
}

func TestAstrodomeRefractionProductionModeKeepsForwardRepeatWithoutReversePass(t *testing.T) {
	t.Parallel()

	azimuth := 0.0
	initial, err := NewAstrodomeRay(
		Location{Latitude: 0, Longitude: 0, TimeZone: "UTC"}, 0, 45, &azimuth,
	)
	if err != nil {
		t.Fatal(err)
	}
	field := astrodomeAnalyticRefractionField{
		refractiveIndex: func(AstrodomeECEFVector) float64 { return 1.00027 },
		gradient:        func(AstrodomeECEFVector) AstrodomeECEFVector { return AstrodomeECEFVector{} },
		surfaceDistance: func(position AstrodomeECEFVector) float64 {
			return position.Norm() - AstrodomeICONSphereRadiusM
		},
		topDistance: func(position AstrodomeECEFVector) float64 {
			return position.Norm() - (AstrodomeICONSphereRadiusM + 3000)
		},
	}
	productionCalibration := DefaultAstrodomeRefractionCalibration()
	production, err := TraceAstrodomeRefractedRay(
		context.Background(), field, initial, productionCalibration,
	)
	if err != nil {
		t.Fatalf("production trace: %v", err)
	}
	if production.VerificationMode != AstrodomeRefractionVerificationForwardRepeat ||
		production.NumericalError.ReversibilityChecked || production.Diagnostics.ForwardPasses != 2 ||
		production.Diagnostics.ReversePasses != 0 {
		t.Fatalf("unexpected production verification diagnostics: mode=%q error=%+v work=%+v",
			production.VerificationMode, production.NumericalError, production.Diagnostics)
	}
	if production.NumericalError.ReversibilityPositionM != 0 ||
		production.NumericalError.ReversibilityDirectionRad != 0 {
		t.Fatalf("unchecked reversibility has non-zero residuals: %+v", production.NumericalError)
	}

	strictCalibration := productionCalibration
	strictCalibration.VerificationMode = AstrodomeRefractionVerificationReversible
	strict, err := TraceAstrodomeRefractedRay(context.Background(), field, initial, strictCalibration)
	if err != nil {
		t.Fatalf("strict trace: %v", err)
	}
	if strict.VerificationMode != AstrodomeRefractionVerificationReversible ||
		!strict.NumericalError.ReversibilityChecked || strict.Diagnostics.ForwardPasses != 2 ||
		strict.Diagnostics.ReversePasses != 1 {
		t.Fatalf("unexpected strict verification diagnostics: mode=%q error=%+v work=%+v",
			strict.VerificationMode, strict.NumericalError, strict.Diagnostics)
	}
	if strict.Diagnostics.TotalFieldEvaluations <= production.Diagnostics.TotalFieldEvaluations {
		t.Fatalf("strict mode did not account for reverse work: production %d, strict %d",
			production.Diagnostics.TotalFieldEvaluations, strict.Diagnostics.TotalFieldEvaluations)
	}
	if endpointDelta := production.TopPoint.ECEF.subtract(strict.TopPoint.ECEF).Norm(); endpointDelta > 1e-9 {
		t.Fatalf("verification mode changed accepted forward endpoint by %.9g m", endpointDelta)
	}
	t.Logf("trace field evaluations: production=%d strict=%d",
		production.Diagnostics.TotalFieldEvaluations, strict.Diagnostics.TotalFieldEvaluations)
}

func TestAstrodomeRefractionConstantIndexMatchesExactStraightSphereIntersection(t *testing.T) {
	t.Parallel()

	azimuth := 73.0
	initial, err := NewAstrodomeRay(
		Location{Latitude: 54.2, Longitude: 37.6, TimeZone: "UTC"}, 180, 10, &azimuth,
	)
	if err != nil {
		t.Fatal(err)
	}
	const (
		topHeightM = 22500.0
		index      = 1.000273
	)
	field := astrodomeAnalyticRefractionField{
		refractiveIndex: func(AstrodomeECEFVector) float64 { return index },
		gradient:        func(AstrodomeECEFVector) AstrodomeECEFVector { return AstrodomeECEFVector{} },
		surfaceDistance: func(position AstrodomeECEFVector) float64 {
			return position.Norm() - (AstrodomeICONSphereRadiusM + initial.ObserverHeightM)
		},
		topDistance: func(position AstrodomeECEFVector) float64 {
			return position.Norm() - (AstrodomeICONSphereRadiusM + topHeightM)
		},
	}
	calibration := DefaultAstrodomeRefractionCalibration()
	result, err := TraceAstrodomeRefractedRay(context.Background(), field, initial, calibration)
	if err != nil {
		t.Fatalf("TraceAstrodomeRefractedRay: %v", err)
	}
	want, err := initial.IntersectAltitude(topHeightM)
	if err != nil {
		t.Fatal(err)
	}
	if distance := result.TopPoint.ECEF.subtract(want.ECEF).Norm(); distance > 0.02 {
		t.Fatalf("constant-n endpoint differs from exact straight ray by %.9g m", distance)
	}
	if math.Abs(result.PathLengthM-want.PathLengthM) > 0.02 {
		t.Fatalf("path length %.9f, want %.9f", result.PathLengthM, want.PathLengthM)
	}
	if math.Abs(result.OpticalPathM-index*result.PathLengthM) > 1e-6 {
		t.Fatalf("optical path %.12g, want %.12g", result.OpticalPathM, index*result.PathLengthM)
	}
	if angle := astrodomeVectorAngle(result.DirectionAtICONTopECEF, initial.DirectionECEF); angle > 1e-12 {
		t.Fatalf("constant-n direction changed by %.12g rad", angle)
	}
	middle, err := result.PointAtPathLength(0.5 * result.PathLengthM)
	if err != nil {
		t.Fatal(err)
	}
	wantMiddle, err := initial.PointAtPathLength(0.5 * result.PathLengthM)
	if err != nil {
		t.Fatal(err)
	}
	if distance := middle.ECEF.subtract(wantMiddle.ECEF).Norm(); distance > 1e-6 {
		t.Fatalf("dense output differs from exact straight ray by %.9g m", distance)
	}
	if math.Abs(middle.RefractiveIndex-index) > 1e-13 {
		t.Fatalf("dense refractive index %.12g, want %.12g", middle.RefractiveIndex, index)
	}
	evaluationErrorM, err := result.PositionEvaluationErrorUpperBound(0.5 * result.PathLengthM)
	if err != nil {
		t.Fatal(err)
	}
	if !finite(evaluationErrorM) || evaluationErrorM <= 0 {
		t.Fatalf("dense position evaluation error = %.17g", evaluationErrorM)
	}
	for component, perturbed := range []AstrodomeECEFVector{
		{X: math.Nextafter(middle.ECEF.X, math.Inf(-1)), Y: middle.ECEF.Y, Z: middle.ECEF.Z},
		{X: middle.ECEF.X, Y: math.Nextafter(middle.ECEF.Y, math.Inf(1)), Z: middle.ECEF.Z},
		{X: middle.ECEF.X, Y: middle.ECEF.Y, Z: math.Nextafter(middle.ECEF.Z, math.Inf(-1))},
	} {
		if delta := perturbed.subtract(middle.ECEF).Norm(); delta > evaluationErrorM {
			t.Fatalf("one-ULP ECEF perturbation %d = %.17g m exceeds dense evaluation bound %.17g m",
				component, delta, evaluationErrorM)
		}
	}
}

func TestAstrodomeDensePositionDerivativeBoundCoversContinuousExtension(t *testing.T) {
	t.Parallel()

	segment := astrodomeRefractionDenseSegment{startPathM: 0, endPathM: 1, baseStepM: 1}
	// Deliberately unrelated stage tangents make the quartic extension overshoot
	// the unit endpoint diagnostic. The bound must follow the dense polynomial,
	// not MaximumTangentNormError from accepted RK endpoints.
	for stage := range segment.stages {
		segment.stages[stage][0] = math.Sin(float64(stage+1) * 1.7)
		segment.stages[stage][1] = math.Cos(float64(stage+1) * 0.9)
		segment.stages[stage][2] = math.Sin(float64(stage+1) * 2.3)
	}
	ray := AstrodomeRefractedRay{
		PathLengthM:    1,
		NumericalError: AstrodomeRefractionNumericalError{MaximumTangentNormError: 0},
		segments:       []astrodomeRefractionDenseSegment{segment},
	}
	bound, err := ray.PositionDerivativeNormUpperBound(0.2, 0.8)
	if err != nil {
		t.Fatal(err)
	}
	maximumSample := 0.0
	for sample := 0; sample <= 10000; sample++ {
		pathM := 0.2 + 0.6*float64(sample)/10000
		maximumSample = math.Max(maximumSample, segment.positionDerivative(pathM).Norm())
	}
	if bound < maximumSample {
		t.Fatalf("dense derivative bound %.17g is below sampled maximum %.17g", bound, maximumSample)
	}
	if maximumSample <= 1+ray.NumericalError.MaximumTangentNormError {
		t.Fatalf("fixture did not exceed endpoint-only bound: dense %.9g", maximumSample)
	}
}

func TestAstrodomeDenseFloatingPointAllowanceUsesAuditedGammaBound(t *testing.T) {
	t.Parallel()

	formationSum := 12_345_678.5
	u := math.Ldexp(1, -53)
	nu := math.Nextafter(astrodomeDenseBoundFloatingPointOperationBudget*u, math.Inf(1))
	gamma := math.Nextafter(nu/math.Nextafter(1-nu, 0), math.Inf(1))
	upperFormationSum := math.Nextafter(formationSum/math.Nextafter(1-gamma, 0), math.Inf(1))
	want := math.Nextafter(gamma*upperFormationSum, math.Inf(1))
	got := astrodomeDenseFloatingPointAllowance(formationSum)
	if got != want {
		t.Fatalf("dense gamma allowance = %.17g, want %.17g", got, want)
	}
	if got <= u*formationSum {
		t.Fatalf("dense gamma allowance %.17g does not cover one rounded operation", got)
	}
	former := 4096 * (math.Nextafter(1, 2) - 1) * formationSum
	if got >= former {
		t.Fatalf("audited gamma allowance %.17g did not replace former heuristic %.17g", got, former)
	}
}

func TestAstrodomeDensePointFloatingPointAllowanceUsesAuditedGamma128(t *testing.T) {
	t.Parallel()

	if astrodomeDensePointFloatingPointOperationBudget != 128 ||
		astrodomeDensePointFloatingPointOperationBudget-102 != 26 {
		t.Fatalf("point-evaluation operation budget = %.0f; want 128 with 26-operation margin over 102",
			astrodomeDensePointFloatingPointOperationBudget)
	}
	formationSum := 12_345_678.5
	u := math.Ldexp(1, -53)
	nu := math.Nextafter(astrodomeDensePointFloatingPointOperationBudget*u, math.Inf(1))
	gamma := math.Nextafter(nu/math.Nextafter(1-nu, 0), math.Inf(1))
	upperFormationSum := math.Nextafter(formationSum/math.Nextafter(1-gamma, 0), math.Inf(1))
	want := math.Nextafter(gamma*upperFormationSum, math.Inf(1))
	if got := astrodomeDensePointFloatingPointAllowance(formationSum); got != want {
		t.Fatalf("dense point gamma128 allowance = %.17g, want %.17g", got, want)
	}
	if general := astrodomeDenseFloatingPointAllowance(formationSum); !(want > 0 && want < general) {
		t.Fatalf("point gamma128 allowance %.17g did not remain below general gamma1024 %.17g", want, general)
	}
}

func TestAstrodomeDenseVectorFloatingPointAllowanceIsOutwardComponentwiseL2(t *testing.T) {
	t.Parallel()

	scales := [3]float64{3_800_000, 2_900_000, 5_100_000}
	sumSquares := 0.0
	for _, scale := range scales {
		componentError := astrodomeDensePointFloatingPointAllowance(scale)
		square := math.Nextafter(componentError*componentError, math.Inf(1))
		sumSquares = math.Nextafter(sumSquares+square, math.Inf(1))
	}
	want := math.Nextafter(math.Sqrt(sumSquares), math.Inf(1))
	got := astrodomeDenseVectorFloatingPointAllowance(scales)
	if got != want {
		t.Fatalf("dense vector allowance = %.17g, want %.17g", got, want)
	}
	legacyL1Envelope := astrodomeDensePointFloatingPointAllowance(scales[0] + scales[1] + scales[2])
	if !(got > 0 && got < legacyL1Envelope) {
		t.Fatalf("componentwise L2 allowance %.17g did not tighten L1 envelope %.17g", got, legacyL1Envelope)
	}
	for component, scale := range scales {
		if componentBound := astrodomeDensePointFloatingPointAllowance(scale); got < componentBound {
			t.Fatalf("vector allowance %.17g is below component %d bound %.17g", got, component, componentBound)
		}
	}
}

func TestAstrodomeDenseEvaluationScaleRetainsCancelledShampineMonomials(t *testing.T) {
	t.Parallel()

	segment := astrodomeRefractionDenseSegment{startPathM: 0, endPathM: 1, baseStepM: 1}
	segment.stages[0][0] = 1_000_000
	positionError, _, err := segment.positionAndDerivativeEvaluationErrors(1)
	if err != nil {
		t.Fatal(err)
	}
	absMonomialSum := 0.0
	coefficient := 0.0
	for _, value := range astrodomeDOPRIDenseP[0] {
		absMonomialSum += math.Abs(value)
		coefficient += value
	}
	wantLowerBound := astrodomeDensePointFloatingPointAllowance(1_000_000 * absMonomialSum)
	if positionError < wantLowerBound {
		t.Fatalf("dense position envelope %.17g is below cancelled-monomial bound %.17g", positionError, wantLowerBound)
	}
	cancelledCoefficientEnvelope := astrodomeDensePointFloatingPointAllowance(1_000_000 * math.Abs(coefficient))
	if positionError <= 10*cancelledCoefficientEnvelope {
		t.Fatalf("dense position envelope %.17g collapsed toward cancelled coefficient %.17g", positionError, cancelledCoefficientEnvelope)
	}
}

func TestAstrodomeDensePositionEvaluationStartFastPathIsExact(t *testing.T) {
	t.Parallel()

	segment := astrodomeRefractionDenseSegment{startPathM: 10, endPathM: 11, baseStepM: 1}
	segment.initial[0], segment.initial[1], segment.initial[2] = 3_800_000, 2_900_000, 5_100_000
	segment.stages[0][0] = 1
	positionError, derivativeError, err := segment.positionAndDerivativeEvaluationErrors(segment.startPathM)
	if err != nil {
		t.Fatal(err)
	}
	if positionError != 0 {
		t.Fatalf("dense start fast-path position error = %.17g; want exact zero", positionError)
	}
	if !finite(derivativeError) || derivativeError <= 0 {
		t.Fatalf("dense start derivative error = %.17g; want a positive bound", derivativeError)
	}
}

func TestAstrodomeDenseAccelerationBoundCoversRestrictedContinuousExtension(t *testing.T) {
	t.Parallel()

	segment := astrodomeRefractionDenseSegment{startPathM: 10, endPathM: 12, baseStepM: 2}
	for stage := range segment.stages {
		segment.stages[stage][0] = math.Sin(float64(stage+1)*1.3) + 0.2
		segment.stages[stage][1] = math.Cos(float64(stage+1)*0.7) - 0.1
		segment.stages[stage][2] = math.Sin(float64(stage+1)*2.1) + 0.3
	}
	const (
		startPathM = 10.37
		endPathM   = 11.42
	)
	bound, err := segment.positionAccelerationNormUpperBound(startPathM, endPathM)
	if err != nil {
		t.Fatal(err)
	}
	ray := AstrodomeRefractedRay{PathLengthM: segment.endPathM, segments: []astrodomeRefractionDenseSegment{segment}}
	publicBound, err := ray.PositionAccelerationNormUpperBound(startPathM, endPathM)
	if err != nil {
		t.Fatal(err)
	}
	if publicBound < bound {
		t.Fatalf("public acceleration bound %.17g is below segment bound %.17g", publicBound, bound)
	}
	maximumSample := 0.0
	for sample := 0; sample <= 10000; sample++ {
		pathM := startPathM + (endPathM-startPathM)*float64(sample)/10000
		maximumSample = math.Max(maximumSample, segment.positionAcceleration(pathM).Norm())
	}
	if bound < maximumSample {
		t.Fatalf("dense acceleration bound %.17g is below sampled maximum %.17g", bound, maximumSample)
	}
}

func TestAstrodomeHeightDerivativeBoundsCoverMultipleDenseSegments(t *testing.T) {
	t.Parallel()

	first := astrodomeRefractionDenseSegment{startPathM: 0, endPathM: 1, baseStepM: 1}
	first.initial[0] = AstrodomeICONSphereRadiusM + 100
	first.initial[1] = 25
	first.initial[2] = -10
	for stage := range first.stages {
		first.stages[stage][0] = 0.15 + 0.04*math.Sin(float64(stage+1)*1.1)
		first.stages[stage][1] = 0.95 + 0.03*math.Cos(float64(stage+1)*0.8)
		first.stages[stage][2] = 0.12 * math.Sin(float64(stage+1)*1.9)
	}

	second := astrodomeRefractionDenseSegment{startPathM: 1, endPathM: 2, baseStepM: 1}
	second.initial = first.evaluate(first.endPathM)
	for stage := range second.stages {
		second.stages[stage][0] = 0.28 + 0.05*math.Cos(float64(stage+1)*1.4)
		second.stages[stage][1] = 0.88 + 0.04*math.Sin(float64(stage+1)*0.6)
		second.stages[stage][2] = -0.08 + 0.03*math.Cos(float64(stage+1)*2.2)
	}

	ray := AstrodomeRefractedRay{
		PathLengthM: 2,
		segments:    []astrodomeRefractionDenseSegment{first, second},
	}
	const (
		startPathM = 0.23
		endPathM   = 1.79
	)
	lower, upper, err := ray.HeightDerivativeBounds(startPathM, endPathM)
	if err != nil {
		t.Fatal(err)
	}
	if !finite(lower) || !finite(upper) || lower > upper {
		t.Fatalf("invalid height-derivative bounds [%.17g, %.17g]", lower, upper)
	}
	for sample := 0; sample <= 20000; sample++ {
		pathM := startPathM + (endPathM-startPathM)*float64(sample)/20000
		segment := first
		if pathM > first.endPathM {
			segment = second
		}
		position := astrodomeStatePosition(segment.evaluate(pathM))
		derivative := segment.positionDerivative(pathM)
		heightDerivative := position.dot(derivative) / position.Norm()
		if heightDerivative < lower || heightDerivative > upper {
			t.Fatalf("height derivative %.17g at %.9f m is outside [%.17g, %.17g]",
				heightDerivative, pathM, lower, upper)
		}
	}
}

func TestAstrodomeHeightDerivativeBoundsCertifyRadialStraightRay(t *testing.T) {
	t.Parallel()

	segment := astrodomeRefractionDenseSegment{startPathM: 0, endPathM: 1000, baseStepM: 1000}
	segment.initial[0] = AstrodomeICONSphereRadiusM
	for stage := range segment.stages {
		segment.stages[stage][0] = 1
	}
	ray := AstrodomeRefractedRay{PathLengthM: 1000, segments: []astrodomeRefractionDenseSegment{segment}}
	lower, upper, err := ray.HeightDerivativeBounds(123, 987)
	if err != nil {
		t.Fatal(err)
	}
	if lower > 1 || upper < 1 {
		t.Fatalf("radial straight-ray derivative 1 is outside [%.17g, %.17g]", lower, upper)
	}
	if lower <= 0.999 || upper >= 1.001 {
		t.Fatalf("radial straight-ray bounds are unexpectedly loose: [%.17g, %.17g]", lower, upper)
	}
}

func TestAstrodomeHeightDerivativeBoundsRejectDenseSegmentGap(t *testing.T) {
	t.Parallel()

	first := astrodomeRefractionDenseSegment{startPathM: 0, endPathM: 1, baseStepM: 1}
	second := astrodomeRefractionDenseSegment{startPathM: 1.1, endPathM: 2, baseStepM: 0.9}
	for stage := range first.stages {
		first.stages[stage][0] = 1
		second.stages[stage][0] = 1
	}
	first.initial[0] = AstrodomeICONSphereRadiusM
	second.initial[0] = AstrodomeICONSphereRadiusM + 1.1
	ray := AstrodomeRefractedRay{PathLengthM: 2, segments: []astrodomeRefractionDenseSegment{first, second}}
	if _, _, err := ray.HeightDerivativeBounds(0.5, 1.5); err == nil {
		t.Fatal("height-derivative bounds accepted a gap between dense segments")
	}
}

func TestAstrodomeRefractionPreservesPlaneStratifiedSnellInvariant(t *testing.T) {
	t.Parallel()

	azimuth := 90.0
	initial, err := NewAstrodomeRay(
		Location{Latitude: 0, Longitude: 0, TimeZone: "UTC"}, 0, 30, &azimuth,
	)
	if err != nil {
		t.Fatal(err)
	}
	const (
		n0         = 1.00028
		gradientX  = -1e-8
		topHeightM = 15000.0
	)
	field := astrodomeAnalyticRefractionField{
		refractiveIndex: func(position AstrodomeECEFVector) float64 {
			return n0 + gradientX*(position.X-AstrodomeICONSphereRadiusM)
		},
		gradient: func(AstrodomeECEFVector) AstrodomeECEFVector {
			return AstrodomeECEFVector{X: gradientX}
		},
		surfaceDistance: func(position AstrodomeECEFVector) float64 {
			return position.X - AstrodomeICONSphereRadiusM
		},
		topDistance: func(position AstrodomeECEFVector) float64 {
			return position.X - (AstrodomeICONSphereRadiusM + topHeightM)
		},
		partition: func(position AstrodomeECEFVector) string {
			if position.Y < 10000 {
				return "west"
			}
			return "east"
		},
	}
	calibration := DefaultAstrodomeRefractionCalibration()
	calibration.VerificationMode = AstrodomeRefractionVerificationReversible
	result, err := TraceAstrodomeRefractedRay(context.Background(), field, initial, calibration)
	if err != nil {
		t.Fatalf("TraceAstrodomeRefractedRay: %v", err)
	}
	wantInvariant := n0 * initial.DirectionECEF.Y
	maximumRelative := 0.0
	for index := 0; index <= 200; index++ {
		point, pointErr := result.PointAtPathLength(result.PathLengthM * float64(index) / 200)
		if pointErr != nil {
			t.Fatal(pointErr)
		}
		invariant := point.RefractiveIndex * point.TangentECEF.Y
		maximumRelative = math.Max(maximumRelative, math.Abs(invariant-wantInvariant)/math.Abs(wantInvariant))
	}
	if maximumRelative > 2e-8 {
		t.Fatalf("plane Snell invariant relative drift %.12g", maximumRelative)
	}
	if result.Diagnostics.PartitionEvents == 0 {
		t.Fatal("analytic partition boundary was not detected")
	}
	if result.NumericalError.ReversibilityPositionM > calibration.ReversibilityPositionM ||
		result.NumericalError.ReversibilityDirectionRad > calibration.ReversibilityDirectionRad {
		t.Fatalf("reversibility diagnostics exceed calibration: %+v", result.NumericalError)
	}
}

func TestAstrodomeRefractionRefinesFailedNominalReversibilityPass(t *testing.T) {
	t.Parallel()

	initial, field := astrodomeRefractionRefinementFixture(t)
	calibration := DefaultAstrodomeRefractionCalibration()
	calibration.VerificationMode = AstrodomeRefractionVerificationReversible
	calibration.ReversibilityDirectionRad = 2e-10

	nominal, nominalPosition, nominalDirection := astrodomeTestRefractionPassResiduals(
		t, field, initial, calibration, 0.5,
	)
	if nominalDirection <= calibration.ReversibilityDirectionRad {
		t.Fatalf("nominal reverse direction residual %.12g unexpectedly meets %.12g rad", nominalDirection, calibration.ReversibilityDirectionRad)
	}
	refined, refinedPosition, refinedDirection := astrodomeTestRefractionPassResiduals(
		t, field, initial, calibration, 0.25,
	)
	if refinedPosition > calibration.ReversibilityPositionM || refinedDirection > calibration.ReversibilityDirectionRad {
		t.Fatalf("refined reverse residuals %.12g m, %.12g rad exceed calibration", refinedPosition, refinedDirection)
	}
	if nominal.topRootBracketM > 0.5*calibration.EventPathToleranceM ||
		refined.topRootBracketM > 0.25*calibration.EventPathToleranceM {
		t.Fatalf("event brackets do not follow pass tolerance: nominal %.12g m, refined %.12g m",
			nominal.topRootBracketM, refined.topRootBracketM)
	}

	result, err := TraceAstrodomeRefractedRay(context.Background(), field, initial, calibration)
	if err != nil {
		t.Fatalf("TraceAstrodomeRefractedRay: %v", err)
	}
	if distance := result.TopPoint.ECEF.subtract(astrodomeStatePosition(refined.finalState)).Norm(); distance > 1e-9 {
		t.Fatalf("published endpoint differs from accepted refined pass by %.12g m", distance)
	}
	if result.Diagnostics.AcceptedSteps != refined.diagnostics.AcceptedSteps ||
		result.Diagnostics.AcceptedSteps == nominal.diagnostics.AcceptedSteps {
		t.Fatalf("published diagnostics do not identify the refined pass: got %d steps, nominal %d, refined %d",
			result.Diagnostics.AcceptedSteps, nominal.diagnostics.AcceptedSteps, refined.diagnostics.AcceptedSteps)
	}
	if result.NumericalError.ReversibilityPositionM > calibration.ReversibilityPositionM ||
		result.NumericalError.ReversibilityDirectionRad > calibration.ReversibilityDirectionRad {
		t.Fatalf("published reversibility diagnostics exceed calibration: %+v", result.NumericalError)
	}
	if result.NumericalError.TopRootBracketM != refined.topRootBracketM {
		t.Fatalf("published top-root bracket %.12g m, want refined-pass %.12g m",
			result.NumericalError.TopRootBracketM, refined.topRootBracketM)
	}
	if nominalPosition > calibration.ReversibilityPositionM {
		t.Fatalf("fixture should isolate the angular residual; nominal position residual is %.12g m", nominalPosition)
	}
}

func TestAstrodomeRefractionUsesSeventhBoundedForwardRepeatRefinement(t *testing.T) {
	t.Parallel()

	initial, field := astrodomeRefractionRefinementFixture(t)
	calibration := DefaultAstrodomeRefractionCalibration()
	passes := make([]astrodomeRefractionPass, astrodomeRefractionMaximumRefinementLevels+1)
	for refinement := range passes {
		scale := math.Ldexp(1, -refinement)
		pass, err := astrodomeIntegrateRefractionToTop(
			context.Background(), field, initial, calibration, scale,
		)
		if err != nil {
			t.Fatalf("forward pass at tolerance scale %.6g: %v", scale, err)
		}
		passes[refinement] = pass
	}
	residuals := make([]float64, astrodomeRefractionMaximumRefinementLevels)
	for refinement := 1; refinement < len(passes); refinement++ {
		residuals[refinement-1] = astrodomeVectorAngle(
			astrodomeStateTangent(passes[refinement].finalState),
			astrodomeStateTangent(passes[refinement-1].finalState),
		)
	}
	penultimate := residuals[len(residuals)-2]
	finest := residuals[len(residuals)-1]
	if !finite(penultimate) || !finite(finest) || finest >= penultimate {
		t.Fatalf("fixture does not improve on the seventh pass: penultimate %.12g, finest %.12g rad",
			penultimate, finest)
	}
	calibration.RepeatDirectionToleranceRad = 0.5 * (penultimate + finest)
	for index, residual := range residuals[:len(residuals)-1] {
		if residual <= calibration.RepeatDirectionToleranceRad {
			t.Fatalf("pass %d residual %.12g unexpectedly meets seventh-pass threshold %.12g rad",
				index+1, residual, calibration.RepeatDirectionToleranceRad)
		}
	}
	result, err := TraceAstrodomeRefractedRay(context.Background(), field, initial, calibration)
	if err != nil {
		t.Fatalf("seventh bounded forward repeat: %v", err)
	}
	if result.Diagnostics.ForwardPasses != astrodomeRefractionMaximumRefinementLevels+1 {
		t.Fatalf("forward passes = %d, want %d", result.Diagnostics.ForwardPasses,
			astrodomeRefractionMaximumRefinementLevels+1)
	}
	if result.NumericalError.DirectionAtICONTopRad > calibration.RepeatDirectionToleranceRad {
		t.Fatalf("accepted direction residual %.12g exceeds %.12g rad",
			result.NumericalError.DirectionAtICONTopRad, calibration.RepeatDirectionToleranceRad)
	}
	finestEventToleranceM := math.Ldexp(
		calibration.EventPathToleranceM,
		-astrodomeRefractionMaximumRefinementLevels,
	)
	if result.NumericalError.TopRootBracketM > finestEventToleranceM {
		t.Fatalf("seventh-pass root bracket %.12g exceeds dense-event tolerance %.12g m",
			result.NumericalError.TopRootBracketM, finestEventToleranceM)
	}
}

func TestAstrodomeRefractionRejectsAfterBoundedRefinement(t *testing.T) {
	t.Parallel()

	initial, field := astrodomeRefractionRefinementFixture(t)
	calibration := DefaultAstrodomeRefractionCalibration()
	calibration.VerificationMode = AstrodomeRefractionVerificationReversible
	calibration.ReversibilityDirectionRad = 1e-12

	_, err := TraceAstrodomeRefractedRay(context.Background(), field, initial, calibration)
	if !errors.Is(err, ErrAstrodomeRefractionNonConvergence) {
		t.Fatalf("TraceAstrodomeRefractedRay error = %v, want ErrAstrodomeRefractionNonConvergence", err)
	}
	if !strings.Contains(err.Error(), "tolerance scale 0.0078125") {
		t.Fatalf("bounded-refinement error does not report its finest scale: %v", err)
	}
}

func astrodomeRefractionRefinementFixture(t *testing.T) (AstrodomeRay, astrodomeAnalyticRefractionField) {
	t.Helper()
	azimuth := 90.0
	initial, err := NewAstrodomeRay(
		Location{Latitude: 0, Longitude: 0, TimeZone: "UTC"}, 0, 20, &azimuth,
	)
	if err != nil {
		t.Fatal(err)
	}
	const (
		refractivity = 0.00028
		scaleHeightM = 100.0
		topHeightM   = 22500.0
	)
	field := astrodomeAnalyticRefractionField{
		refractiveIndex: func(position AstrodomeECEFVector) float64 {
			heightM := position.X - AstrodomeICONSphereRadiusM
			return 1 + refractivity*math.Exp(-heightM/scaleHeightM)
		},
		gradient: func(position AstrodomeECEFVector) AstrodomeECEFVector {
			heightM := position.X - AstrodomeICONSphereRadiusM
			return AstrodomeECEFVector{X: -refractivity / scaleHeightM * math.Exp(-heightM/scaleHeightM)}
		},
		surfaceDistance: func(position AstrodomeECEFVector) float64 {
			return position.X - AstrodomeICONSphereRadiusM
		},
		topDistance: func(position AstrodomeECEFVector) float64 {
			return position.X - (AstrodomeICONSphereRadiusM + topHeightM)
		},
	}
	return initial, field
}

func astrodomeTestRefractionPassResiduals(
	t *testing.T,
	field AstrodomeRefractionField,
	initial AstrodomeRay,
	calibration AstrodomeRefractionCalibration,
	toleranceScale float64,
) (astrodomeRefractionPass, float64, float64) {
	t.Helper()
	forward, err := astrodomeIntegrateRefractionToTop(context.Background(), field, initial, calibration, toleranceScale)
	if err != nil {
		t.Fatalf("forward pass at tolerance scale %.6g: %v", toleranceScale, err)
	}
	reverseInitial := forward.finalState
	reverseTangent := astrodomeStateTangent(reverseInitial).scale(-1)
	reverseInitial[3], reverseInitial[4], reverseInitial[5] = reverseTangent.X, reverseTangent.Y, reverseTangent.Z
	reverseInitial[6] = 0
	reversed, err := astrodomeIntegrateRefractionFixedLength(
		context.Background(), field, reverseInitial, forward.pathLengthM, calibration, toleranceScale,
	)
	if err != nil {
		t.Fatalf("reverse pass at tolerance scale %.6g: %v", toleranceScale, err)
	}
	positionResidualM := astrodomeStatePosition(reversed.finalState).subtract(initial.ObserverECEF).Norm()
	directionResidualRad := astrodomeVectorAngle(astrodomeStateTangent(reversed.finalState), initial.DirectionECEF.scale(-1))
	return forward, positionResidualM, directionResidualRad
}

func TestAstrodomeRefractionTerrainIntersectionIsNotPublishedAsIndex(t *testing.T) {
	t.Parallel()

	azimuth := 0.0
	initial, err := NewAstrodomeRay(Location{Latitude: 0, Longitude: 0, TimeZone: "UTC"}, 1, 10, &azimuth)
	if err != nil {
		t.Fatal(err)
	}
	field := astrodomeAnalyticRefractionField{
		refractiveIndex: func(AstrodomeECEFVector) float64 { return 1.00027 },
		gradient:        func(AstrodomeECEFVector) AstrodomeECEFVector { return AstrodomeECEFVector{} },
		surfaceDistance: func(position AstrodomeECEFVector) float64 {
			// A synthetic ridge above the ray after it has first cleared the
			// observer surface.
			pointHeight := position.Norm() - AstrodomeICONSphereRadiusM
			if position.Z > 100 {
				return pointHeight - 1000
			}
			return pointHeight
		},
		topDistance: func(position AstrodomeECEFVector) float64 {
			return position.Norm() - (AstrodomeICONSphereRadiusM + 22500)
		},
	}
	_, err = TraceAstrodomeRefractedRay(context.Background(), field, initial, DefaultAstrodomeRefractionCalibration())
	if !errors.Is(err, ErrAstrodomeRefractionTerrain) {
		t.Fatalf("terrain crossing error = %v, want ErrAstrodomeRefractionTerrain", err)
	}
}
