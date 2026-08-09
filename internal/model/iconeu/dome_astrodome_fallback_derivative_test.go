package iconeu

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"

	"bot_astrosferum/internal/forecast"
)

func TestDomeAstrodomeFallbackDerivativeCertificateResolvesCrossing(t *testing.T) {
	t.Parallel()

	grid := Coverage()
	observer := forecast.Location{
		Latitude:  grid.MinLat + 328.25*grid.Increment,
		Longitude: grid.MinLon + 856.25*grid.Increment,
		TimeZone:  "UTC",
	}
	initial, err := forecast.NewAstrodomeRay(observer, 100, 90, nil)
	if err != nil {
		t.Fatal(err)
	}
	ray, err := forecast.TraceAstrodomeRefractedRay(
		context.Background(),
		domeAstrodomeGridTestRefractionField{surfaceHeightM: 100, topHeightM: 20_000},
		initial,
		forecast.DefaultAstrodomeRefractionCalibration(),
	)
	if err != nil {
		t.Fatal(err)
	}
	volume := newDomeAstrodomeGridTestVolume(grid)
	interval := domeAstrodomeCellInterval{startM: 0, endM: ray.PathLengthM, cellID: "fallback-certificate"}
	metric, err := volume.domeAstrodomeMetricBoundsForInterval(ray, interval)
	if err != nil {
		t.Fatal(err)
	}
	profile := domeAstrodomeTropopauseProfile{
		heights:   [][4]float64{{10_000, 10_000, 10_000, 10_000}, {12_000, 12_000, 12_000, 12_000}},
		pressures: [][4]float64{{30_000, 30_000, 30_000, 30_000}, {10_000, 10_000, 10_000, 10_000}},
	}
	lipschitz, secondDerivative, evaluationError, err := domeAstrodomeFallbackBoundaryDerivativeBounds(
		ray, metric, profile, 0, interval.startM, interval.endM,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !finiteDomeVolume(lipschitz) || lipschitz <= 0 ||
		!finiteDomeVolume(secondDerivative) || secondDerivative <= 0 ||
		!finiteDomeVolume(evaluationError) || evaluationError <= 0 {
		t.Fatalf("fallback derivative bounds = %.12g, %.12g, %.12g", lipschitz, secondDerivative, evaluationError)
	}

	alpha := math.Log(30_000/domeAstrodomeFallbackPressurePa) / math.Log(30_000.0/10_000.0)
	fallbackHeightM := 10_000 + alpha*(12_000-10_000)
	valueAt := func(pathM float64) (domeAstrodomeResidualSample, error) {
		point, pointErr := ray.PointAtPathLength(pathM)
		if pointErr != nil {
			return domeAstrodomeResidualSample{}, pointErr
		}
		positionErrorM, pointErr := ray.PositionEvaluationErrorUpperBound(pathM)
		if pointErr != nil {
			return domeAstrodomeResidualSample{}, pointErr
		}
		residual, residualErr := domeAstrodomeResidualWithEvaluationError(
			point.HeightM-fallbackHeightM,
			domeAstrodomePositiveAddUpper(positionErrorM, evaluationError),
		)
		if residualErr != nil {
			return domeAstrodomeResidualSample{}, residualErr
		}
		return residual, nil
	}
	slopeBounds := func(
		leftM, rightM float64,
		leftSample, rightSample domeAstrodomeResidualSample,
	) (float64, float64, error) {
		lower, upper, boundsErr := domeAstrodomeSecantSlopeBoundsResiduals(
			leftM, rightM, leftSample, rightSample,
			domeAstrodomePhysicalHeightOperandScaleM(), secondDerivative,
		)
		if boundsErr != nil {
			return 0, 0, boundsErr
		}
		return math.Max(-lipschitz, lower), math.Min(lipschitz, upper), nil
	}
	probes, err := domeAstrodomePhysicalProbePaths(interval)
	if err != nil {
		t.Fatal(err)
	}
	roots, err := domeAstrodomeIsolateFieldAcrossProbesWithSlopeBoundsResiduals(
		interval, probes, lipschitz, domeAstrodomePhysicalHeightOperandScaleM(),
		slopeBounds, valueAt,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(roots) != 1 {
		t.Fatalf("fallback roots = %v; want exactly one", roots)
	}
	point, err := ray.PointAtPathLength(roots[0])
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(point.HeightM-fallbackHeightM) > forecast.AstrodomeScienceRootToleranceM {
		t.Fatalf("fallback root height %.12g m does not enclose %.12g m", point.HeightM, fallbackHeightM)
	}
}

func TestDomeAstrodomeFallbackSecondDerivativeBoundContainsVariableNativeProfile(t *testing.T) {
	t.Parallel()

	grid := Coverage()
	observer := forecast.Location{
		Latitude:  grid.MinLat + 328.25*grid.Increment,
		Longitude: grid.MinLon + 856.25*grid.Increment,
		TimeZone:  "UTC",
	}
	ray := traceDomeAstrodomeGridTestRay(t, observer, 37)
	integrationIntervals, err := ray.IntegrationIntervals()
	if err != nil || len(integrationIntervals) == 0 {
		t.Fatalf("integration intervals = %d, %v", len(integrationIntervals), err)
	}
	selected := integrationIntervals[0]
	for _, candidate := range integrationIntervals[1:] {
		if candidate.EndPathM-candidate.StartPathM > selected.EndPathM-selected.StartPathM {
			selected = candidate
		}
	}
	interval := domeAstrodomeCellInterval{
		startM: selected.StartPathM, endM: selected.EndPathM, cellID: "fallback-variable-profile",
	}
	volume := newDomeAstrodomeGridTestVolume(grid)
	metric, err := volume.domeAstrodomeMetricBoundsForInterval(ray, interval)
	if err != nil {
		t.Fatal(err)
	}
	profile := domeAstrodomeTropopauseProfile{
		heights: [][4]float64{
			{10_000, 10_017, 10_031, 10_054},
			{12_000, 12_026, 12_043, 12_071},
		},
		pressures: [][4]float64{
			{30_000, 30_180, 30_330, 30_570},
			{10_000, 10_090, 10_160, 10_280},
		},
	}
	_, secondDerivative, _, err := domeAstrodomeFallbackBoundaryDerivativeBounds(
		ray, metric, profile, 0, interval.startM, interval.endM,
	)
	if err != nil {
		t.Fatal(err)
	}
	residualAt := func(pathM float64) float64 {
		point, pointErr := ray.PointAtPathLength(pathM)
		if pointErr != nil {
			t.Fatal(pointErr)
		}
		cell, cellErr := domeGridCell(grid, point.Location)
		if cellErr != nil {
			t.Fatal(cellErr)
		}
		weights := [4]float64{
			(1 - cell.latitudeFraction) * (1 - cell.longitudeFraction),
			(1 - cell.latitudeFraction) * cell.longitudeFraction,
			cell.latitudeFraction * (1 - cell.longitudeFraction),
			cell.latitudeFraction * cell.longitudeFraction,
		}
		lowerPressure := domeWeighted4(profile.pressures[0], weights)
		upperPressure := domeWeighted4(profile.pressures[1], weights)
		lowerHeight := domeWeighted4(profile.heights[0], weights)
		upperHeight := domeWeighted4(profile.heights[1], weights)
		alpha := math.Log(lowerPressure/domeAstrodomeFallbackPressurePa) /
			math.Log(lowerPressure/upperPressure)
		return point.HeightM - (lowerHeight + alpha*(upperHeight-lowerHeight))
	}
	widthM := interval.endM - interval.startM
	stepM := math.Min(0.25, widthM/100)
	for sample := 1; sample <= 7; sample++ {
		pathM := interval.startM + float64(sample)*widthM/8
		secondDifference := math.Abs(
			(residualAt(pathM+stepM) - 2*residualAt(pathM) + residualAt(pathM-stepM)) / (stepM * stepM),
		)
		// The finite difference is only a regression oracle; the production bound
		// itself is obtained analytically and rounded outward.
		if secondDifference > secondDerivative*(1+1e-5)+1e-8 {
			t.Fatalf("sample %d second derivative %.12g exceeds bound %.12g", sample, secondDifference, secondDerivative)
		}
	}
}

func TestDomeAstrodomeFallbackEvaluationErrorContainsIllConditionedLogRatio(t *testing.T) {
	t.Parallel()

	grid := Coverage()
	observer := forecast.Location{
		Latitude:  grid.MinLat + 328.25*grid.Increment,
		Longitude: grid.MinLon + 856.25*grid.Increment,
		TimeZone:  "UTC",
	}
	ray := traceDomeAstrodomeGridTestRay(t, observer, 37)
	integrationIntervals, err := ray.IntegrationIntervals()
	if err != nil || len(integrationIntervals) == 0 {
		t.Fatalf("integration intervals = %d, %v", len(integrationIntervals), err)
	}
	selected := integrationIntervals[0]
	interval := domeAstrodomeCellInterval{
		startM: selected.StartPathM, endM: selected.EndPathM, cellID: "fallback-ill-conditioned",
	}
	volume := newDomeAstrodomeGridTestVolume(grid)
	metric, err := volume.domeAstrodomeMetricBoundsForInterval(ray, interval)
	if err != nil {
		t.Fatal(err)
	}
	profile := domeAstrodomeTropopauseProfile{
		heights: [][4]float64{
			{10_000, 10_000, 10_000, 10_000},
			{12_000, 12_000, 12_000, 12_000},
		},
		pressures: [][4]float64{
			{20_000.0000001, 20_000.0000001, 20_000.0000001, 20_000.0000001},
			{19_999.9999999, 19_999.9999999, 19_999.9999999, 19_999.9999999},
		},
	}
	_, _, _, err = domeAstrodomeFallbackBoundaryDerivativeBounds(
		ray, metric, profile, 0, interval.startM, interval.endM,
	)
	if !errors.Is(err, forecast.ErrAstrodomeScienceIncompletePartition) ||
		!strings.Contains(err.Error(), "arithmetic enclosure") {
		t.Fatalf("ill-conditioned fallback error = %v", err)
	}
}
