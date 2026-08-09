package iconeu

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	"bot_astrosferum/internal/forecast"
	"bot_astrosferum/internal/model"
)

type domeAstrodomePBLIsolationTrace struct {
	localSmoothCertificates int
	localKinkEnclosures     int
	secantCertificates      int
}

func TestDomeAstrodomePBLMixedCellRootUsesLocalSmoothCertificate(t *testing.T) {
	t.Parallel()

	grid := Coverage()
	observer := forecast.Location{
		Latitude:  grid.MinLat + 10.5*grid.Increment,
		Longitude: grid.MinLon + 10.5*grid.Increment,
		TimeZone:  "UTC",
	}
	ray := traceDomeAstrodomePBLTestRay(t, observer, 45, 90)
	interval := domeAstrodomePBLTestInterval(t, newDomeAstrodomeGridTestVolume(grid), ray, 400, 1400)
	validAt := time.Date(2026, time.July, 30, 0, 0, 0, 0, time.UTC)
	volume := newDomeAstrodomePBLTestVolume(t, grid, interval.stencil, validAt,
		[4]float64{100, 100, 100, 100},
		[4]float64{400, 800, 400, 800},
	)

	roots, trace, valueAt, err := isolateDomeAstrodomePBLTestRoot(
		volume, ray, interval, validAt, forecast.DefaultAstrodomeScienceCalibration(),
	)
	if err != nil {
		t.Fatalf("isolate locally smooth PBL root: %v", err)
	}
	if len(roots) != 1 {
		t.Fatalf("locally smooth PBL roots = %v; want exactly one", roots)
	}
	if trace.localSmoothCertificates == 0 || trace.secantCertificates == 0 {
		t.Fatalf("PBL certificate trace = %+v; local smooth and secant/curvature paths must both run", trace)
	}
	if residual, residualErr := valueAt(roots[0]); residualErr != nil ||
		math.Abs(residual) > forecast.AstrodomeScienceRootToleranceM {
		t.Fatalf("localized PBL residual = %.12g, %v at %.12g m", residual, residualErr, roots[0])
	}
	leftValue, err := valueAt(interval.startM)
	if err != nil {
		t.Fatal(err)
	}
	rightValue, err := valueAt(interval.endM)
	if err != nil {
		t.Fatal(err)
	}
	wantRoot, err := domeBisectionPathRoot(interval.startM, interval.endM, 1e-9, valueAt)
	if err != nil || leftValue >= 0 || rightValue <= 0 ||
		math.Abs(roots[0]-wantRoot) > domeAstrodomePhysicalRootToleranceM {
		t.Fatalf("localized PBL root = %.12g; reference %.12g, endpoints %.12g .. %.12g, err %v",
			roots[0], wantRoot, leftValue, rightValue, err)
	}
	breakpoints, err := volume.domePhysicalBreakpoints(
		context.Background(), ray, validAt, interval, forecast.DefaultAstrodomeScienceCalibration(),
	)
	if err != nil {
		t.Fatalf("production physical-breakpoint planner rejected locally smooth PBL root: %v", err)
	}
	foundPBL := false
	for _, breakpoint := range breakpoints {
		if math.Abs(breakpoint-roots[0]) <= domeAstrodomePhysicalRootToleranceM {
			foundPBL = true
			break
		}
	}
	if !foundPBL {
		t.Fatalf("production physical breakpoints %v omit PBL root %.12g", breakpoints, roots[0])
	}
}

func TestDomeAstrodomePBLClampKinkRootFailsClosed(t *testing.T) {
	t.Parallel()

	grid := Coverage()
	observer := forecast.Location{
		Latitude:  grid.MinLat + 10.5*grid.Increment,
		Longitude: grid.MinLon + 10.01*grid.Increment,
		TimeZone:  "UTC",
	}
	ray := traceDomeAstrodomePBLTestRay(t, observer, 45, 90)
	const rootM = 500.0
	rootPoint, err := ray.PointAtPathLength(rootM)
	if err != nil {
		t.Fatal(err)
	}
	volumeForStencil := newDomeAstrodomeGridTestVolume(grid)
	rootStencil, err := volumeForStencil.HorizontalStencil(context.Background(), rootPoint.Location)
	if err != nil {
		t.Fatal(err)
	}
	rootWeights := domeStencilWeights(rootStencil)
	eastWeight := rootWeights[1] + rootWeights[3]
	if eastWeight <= 0 || eastWeight >= 1 {
		t.Fatalf("root east weight = %.12g", eastWeight)
	}
	mixedEastM := 500 / eastWeight
	surfaceM := rootPoint.HeightM - 500
	interval := domeAstrodomePBLTestInterval(t, volumeForStencil, ray, rootM-100, rootM+100)
	validAt := time.Date(2026, time.July, 30, 0, 0, 0, 0, time.UTC)
	volume := newDomeAstrodomePBLTestVolume(t, grid, interval.stencil, validAt,
		[4]float64{surfaceM, surfaceM, surfaceM, surfaceM},
		[4]float64{0, mixedEastM, 0, mixedEastM},
	)

	_, trace, valueAt, err := isolateDomeAstrodomePBLTestRoot(
		volume, ray, interval, validAt, forecast.DefaultAstrodomeScienceCalibration(),
	)
	if !errors.Is(err, forecast.ErrAstrodomeScienceIncompletePartition) {
		t.Fatalf("PBL clamp-kink root error = %v; want fail-closed incomplete partition", err)
	}
	if trace.localKinkEnclosures == 0 {
		t.Fatalf("PBL certificate trace = %+v; clamp-kink enclosure was not exercised", trace)
	}
	if residual, residualErr := valueAt(rootM); residualErr != nil || math.Abs(residual) > 1e-7 {
		t.Fatalf("clamp-kink residual = %.12g, %v at %.12g m", residual, residualErr, rootM)
	}
	if _, plannerErr := volume.domePhysicalBreakpoints(
		context.Background(), ray, validAt, interval, forecast.DefaultAstrodomeScienceCalibration(),
	); !errors.Is(plannerErr, forecast.ErrAstrodomeScienceIncompletePartition) {
		t.Fatalf("production physical-breakpoint clamp-kink error = %v; want fail-closed incomplete partition", plannerErr)
	}
}

func TestDomeAstrodomePBLCellWideUpperClampUsesCloudLowAliasOnce(t *testing.T) {
	t.Parallel()

	grid := Coverage()
	observer := forecast.Location{
		Latitude:  grid.MinLat + 10.5*grid.Increment,
		Longitude: grid.MinLon + 10.5*grid.Increment,
		TimeZone:  "UTC",
	}
	ray := traceDomeAstrodomePBLTestRay(t, observer, 45, 0)
	const surfaceM = 100.0
	rootM := domeAstrodomePBLTestHeightRoot(
		t, ray, surfaceM+forecast.AstrodomeScienceCloudLowTopAGLM, 2000, 4000,
	)
	interval := domeAstrodomePBLTestInterval(t, newDomeAstrodomeGridTestVolume(grid), ray, rootM-100, rootM+100)
	validAt := time.Date(2026, time.July, 30, 0, 0, 0, 0, time.UTC)
	volume := newDomeAstrodomePBLTestVolume(t, grid, interval.stencil, validAt,
		[4]float64{surfaceM, surfaceM, surfaceM, surfaceM},
		[4]float64{2400, 2400, 2400, 2400},
	)

	breakpoints, err := volume.domePhysicalBreakpoints(
		context.Background(), ray, validAt, interval, forecast.DefaultAstrodomeScienceCalibration(),
	)
	if err != nil {
		t.Fatalf("cell-wide upper PBL alias planner: %v", err)
	}
	if count := domeAstrodomePBLRootsNear(breakpoints, rootM); count != 1 {
		t.Fatalf("cell-wide upper PBL/cloud-low root count = %d in %v; want one exact alias", count, breakpoints)
	}
}

func TestDomeAstrodomePBLMixedCellLocallyUpperUsesCloudLowAliasOnce(t *testing.T) {
	t.Parallel()

	grid := Coverage()
	observer := forecast.Location{
		Latitude:  grid.MinLat + 10.5*grid.Increment,
		Longitude: grid.MinLon + 10.1*grid.Increment,
		TimeZone:  "UTC",
	}
	ray := traceDomeAstrodomePBLTestRay(t, observer, 45, 90)
	const surfaceM = 100.0
	rootM := domeAstrodomePBLTestHeightRoot(
		t, ray, surfaceM+forecast.AstrodomeScienceCloudLowTopAGLM, 2000, 4000,
	)
	interval := domeAstrodomePBLTestInterval(t, newDomeAstrodomeGridTestVolume(grid), ray, 300, rootM+100)
	validAt := time.Date(2026, time.July, 30, 0, 0, 0, 0, time.UTC)
	mixedLayerDepths := [4]float64{1800, 2400, 1800, 2400}
	// The ray first crosses the raw MH=2000-m decision and only later reaches
	// HSURF+2000 m. The latter belongs to the locally upper partition and must
	// therefore be emitted solely as cloud-low-top, not duplicated as PBL.
	volume := newDomeAstrodomePBLTestVolume(t, grid, interval.stencil, validAt,
		[4]float64{surfaceM, surfaceM, surfaceM, surfaceM},
		mixedLayerDepths,
	)
	if branch, err := domeClassifyAstrodomePBLClampBranch(
		mixedLayerDepths, 500, 2000,
	); err != nil || branch != domeAstrodomePBLClampCrossing {
		t.Fatalf("fixture cell branch = %v, %v; want mixed", branch, err)
	}
	decisionValueAt := func(pathM float64) (float64, error) {
		point, pointErr := ray.PointAtPathLength(pathM)
		if pointErr != nil {
			return 0, pointErr
		}
		stencil, stencilErr := volume.HorizontalStencil(context.Background(), point.Location)
		if stencilErr != nil {
			return 0, stencilErr
		}
		return domeWeighted4(mixedLayerDepths, domeStencilWeights(stencil)) - 2000, nil
	}
	decisionRootM, err := domeBisectionPathRoot(interval.startM, interval.endM, 1e-9, decisionValueAt)
	if err != nil || decisionRootM >= rootM {
		t.Fatalf("raw MH decision root = %.12g, cloud root %.12g, err %v", decisionRootM, rootM, err)
	}

	breakpoints, err := volume.domePhysicalBreakpoints(
		context.Background(), ray, validAt, interval, forecast.DefaultAstrodomeScienceCalibration(),
	)
	if err != nil {
		t.Fatalf("mixed-cell locally-upper PBL alias planner: %v", err)
	}
	if count := domeAstrodomePBLRootsNear(breakpoints, rootM); count != 1 {
		t.Fatalf("mixed-cell local upper PBL/cloud-low root count = %d in %v; want one exact alias", count, breakpoints)
	}
	if count := domeAstrodomePBLRootsNear(breakpoints, decisionRootM); count != 1 {
		t.Fatalf("mixed-cell raw MH decision count = %d in %v; want one", count, breakpoints)
	}
}

func TestDomeAstrodomePBLMixedCellLocallyLowerSolvesPBLRoot(t *testing.T) {
	t.Parallel()

	grid := Coverage()
	observer := forecast.Location{
		Latitude:  grid.MinLat + 10.5*grid.Increment,
		Longitude: grid.MinLon + 10.1*grid.Increment,
		TimeZone:  "UTC",
	}
	ray := traceDomeAstrodomePBLTestRay(t, observer, 45, 0)
	const surfaceM = 100.0
	rootM := domeAstrodomePBLTestHeightRoot(t, ray, surfaceM+500, 400, 1000)
	interval := domeAstrodomePBLTestInterval(t, newDomeAstrodomeGridTestVolume(grid), ray, rootM-100, rootM+100)
	validAt := time.Date(2026, time.July, 30, 0, 0, 0, 0, time.UTC)
	// The cell crosses the 500-m clamp west-to-east. This northward ray keeps
	// longitude fraction 0.1, hence MH=340 m and the local lower branch.
	volume := newDomeAstrodomePBLTestVolume(t, grid, interval.stencil, validAt,
		[4]float64{surfaceM, surfaceM, surfaceM, surfaceM},
		[4]float64{300, 700, 300, 700},
	)

	breakpoints, err := volume.domePhysicalBreakpoints(
		context.Background(), ray, validAt, interval, forecast.DefaultAstrodomeScienceCalibration(),
	)
	if err != nil {
		t.Fatalf("mixed-cell locally-lower PBL planner: %v", err)
	}
	if count := domeAstrodomePBLRootsNear(breakpoints, rootM); count != 1 {
		t.Fatalf("mixed-cell local lower PBL root count = %d in %v; want one", count, breakpoints)
	}
}

func TestCompactDomeAstrodomePBLDecisionRootsRejectsDistinctCloseEvents(t *testing.T) {
	t.Parallel()

	values := []domeAstrodomePBLDecisionRoot{
		{
			eventID:  "pbl-decision/pbl-clamp/minimum",
			evidence: domeAstrodomeRootEvidence{pathM: 50, leftM: 49.995, rightM: 50.005},
		},
		{
			eventID: "pbl-decision/pbl-clamp/maximum",
			evidence: domeAstrodomeRootEvidence{
				pathM: 50 + 0.5*domeAstrodomePhysicalMergeToleranceM,
				leftM: 50.005, rightM: 50.015,
			},
		},
	}
	_, err := compactDomeAstrodomePBLDecisionRoots(values, 100)
	if !errors.Is(err, forecast.ErrAstrodomeScienceIncompletePartition) {
		t.Fatalf("distinct close PBL decisions error = %v; want fail-closed incomplete partition", err)
	}
}

func domeAstrodomePBLTestHeightRoot(
	t *testing.T,
	ray forecast.AstrodomeRefractedRay,
	targetHeightM, startM, endM float64,
) float64 {
	t.Helper()
	valueAt := func(pathM float64) (float64, error) {
		point, err := ray.PointAtPathLength(pathM)
		if err != nil {
			return 0, err
		}
		return point.HeightM - targetHeightM, nil
	}
	rootM, err := domeBisectionPathRoot(startM, endM, 1e-9, valueAt)
	if err != nil {
		t.Fatal(err)
	}
	return rootM
}

func domeAstrodomePBLRootsNear(breakpoints []float64, wantM float64) int {
	count := 0
	for _, breakpoint := range breakpoints {
		if math.Abs(breakpoint-wantM) <= domeAstrodomePhysicalRootToleranceM {
			count++
		}
	}
	return count
}

func isolateDomeAstrodomePBLTestRoot(
	volume *DomeVolume,
	ray forecast.AstrodomeRefractedRay,
	interval domeAstrodomeCellInterval,
	validAt time.Time,
	calibration forecast.AstrodomeScienceCalibration,
) ([]float64, domeAstrodomePBLIsolationTrace, func(float64) (float64, error), error) {
	trace := domeAstrodomePBLIsolationTrace{}
	columns, err := volume.domeStencilColumns(context.Background(), interval.stencil)
	if err != nil {
		return nil, trace, nil, err
	}
	metric, err := volume.domeAstrodomeMetricBoundsForInterval(ray, interval)
	if err != nil {
		return nil, trace, nil, err
	}
	boundary := domeAstrodomeBoundary{id: "pbl", kind: domeAstrodomeBoundaryPBL}
	boundaryFields, smoothBoundary, err := volume.domePhysicalBoundaryFields(
		validAt, columns, boundary, calibration,
	)
	if err != nil {
		return nil, trace, nil, err
	}
	if smoothBoundary || len(boundaryFields) != 2 {
		return nil, trace, nil, errors.New("test fixture does not span a PBL clamp branch")
	}
	lipschitz, boundarySlope, certified, err := domePhysicalBoundaryLipschitz(boundaryFields, metric)
	if err != nil {
		return nil, trace, nil, err
	}
	if !certified {
		return nil, trace, nil, errors.New("test PBL boundary has no certified Lipschitz bound")
	}
	mixedLayerOperandScale := domeAstrodomeCornerOperandScale(boundaryFields[1])
	mixedLayerLipschitz, err := domeAstrodomeScalarFieldLipschitz(
		metric, boundaryFields[1], mixedLayerOperandScale,
	)
	if err != nil {
		return nil, trace, nil, err
	}
	sampler := domeAstrodomePhysicalSampler{
		ctx: context.Background(), volume: volume, ray: ray, validAt: validAt,
		cellID: interval.cellID, calibration: calibration,
		cache: make(map[uint64]*domeAstrodomePhysicalSample),
	}
	valueAt := func(pathM float64) (float64, error) {
		sample, sampleErr := sampler.sample(pathM, true)
		if sampleErr != nil {
			return 0, sampleErr
		}
		return sample.point.HeightM - sample.heights.BoundaryLayerTopM, nil
	}
	slopeBounds := func(leftM, rightM, leftValue, rightValue float64) (float64, float64, error) {
		lower, upper, boundsErr := ray.HeightDerivativeBounds(leftM, rightM)
		if boundsErr != nil {
			return 0, 0, boundsErr
		}
		lower = math.Nextafter(lower-boundarySlope, math.Inf(-1))
		upper = math.Nextafter(upper+boundarySlope, math.Inf(1))
		intervalFields, intervalSmooth, boundsErr := domeAstrodomePBLIntervalFields(
			&sampler, boundaryFields[0], boundaryFields[1], mixedLayerLipschitz,
			mixedLayerOperandScale, leftM, rightM,
			calibration.Overall.BoundaryLayerMinM,
			calibration.Overall.BoundaryLayerTopM,
		)
		if boundsErr != nil {
			return 0, 0, boundsErr
		}
		if !intervalSmooth {
			trace.localKinkEnclosures++
			return lower, upper, nil
		}
		trace.localSmoothCertificates++
		secondDerivative, derivativeErr := domeAstrodomePhysicalBoundarySecondDerivativeBound(
			ray, metric, intervalFields, leftM, rightM,
		)
		if derivativeErr != nil {
			return 0, 0, derivativeErr
		}
		secantLower, secantUpper, secantErr := domeAstrodomeSecantSlopeBounds(
			leftM, rightM, leftValue, rightValue,
			domeAstrodomePhysicalHeightOperandScaleM(), secondDerivative,
		)
		if secantErr != nil {
			return 0, 0, secantErr
		}
		trace.secantCertificates++
		lower = math.Max(lower, secantLower)
		upper = math.Min(upper, secantUpper)
		if lower > upper {
			return 0, 0, forecast.ErrAstrodomeScienceIncompletePartition
		}
		return lower, upper, nil
	}
	probes, err := domeAstrodomePhysicalProbePaths(interval)
	if err != nil {
		return nil, trace, valueAt, err
	}
	roots, err := domeAstrodomeIsolateFieldAcrossProbesWithSlopeBounds(
		interval, probes, lipschitz, domeAstrodomePhysicalHeightOperandScaleM(),
		slopeBounds, valueAt,
	)
	return roots, trace, valueAt, err
}

func traceDomeAstrodomePBLTestRay(
	t *testing.T,
	observer forecast.Location,
	elevationDegrees, azimuthDegrees float64,
) forecast.AstrodomeRefractedRay {
	t.Helper()
	const observerHeightM = 100.0
	initial, err := forecast.NewAstrodomeRay(
		observer, observerHeightM, elevationDegrees, &azimuthDegrees,
	)
	if err != nil {
		t.Fatal(err)
	}
	ray, err := forecast.TraceAstrodomeRefractedRay(
		context.Background(),
		domeAstrodomeGridTestRefractionField{surfaceHeightM: observerHeightM, topHeightM: 5000},
		initial, forecast.DefaultAstrodomeRefractionCalibration(),
	)
	if err != nil {
		t.Fatal(err)
	}
	return ray
}

func domeAstrodomePBLTestInterval(
	t *testing.T,
	volume *DomeVolume,
	ray forecast.AstrodomeRefractedRay,
	startM, endM float64,
) domeAstrodomeCellInterval {
	t.Helper()
	middle, err := ray.PointAtPathLength(startM + (endM-startM)/2)
	if err != nil {
		t.Fatal(err)
	}
	stencil, err := volume.HorizontalStencil(context.Background(), middle.Location)
	if err != nil {
		t.Fatal(err)
	}
	cellID, err := volume.HorizontalCellID(stencil)
	if err != nil {
		t.Fatal(err)
	}
	for _, pathM := range []float64{startM, endM} {
		point, pointErr := ray.PointAtPathLength(pathM)
		if pointErr != nil {
			t.Fatal(pointErr)
		}
		pointStencil, stencilErr := volume.HorizontalStencil(context.Background(), point.Location)
		if stencilErr != nil {
			t.Fatal(stencilErr)
		}
		pointCellID, cellErr := volume.HorizontalCellID(pointStencil)
		if cellErr != nil || pointCellID != cellID {
			t.Fatalf("PBL test interval escaped %q at %.12g m into %q: %v", cellID, pathM, pointCellID, cellErr)
		}
	}
	return domeAstrodomeCellInterval{startM: startM, endM: endM, cellID: cellID, stencil: stencil}
}

func newDomeAstrodomePBLTestVolume(
	t *testing.T,
	grid model.Coverage,
	stencil forecast.AstrodomeHorizontalStencil,
	validAt time.Time,
	surfaceHeights, mixedLayerDepths [4]float64,
) *DomeVolume {
	t.Helper()
	volume := newDomeAstrodomeGridTestVolume(grid)
	volume.fieldTimes = map[forecast.AstrodomePrimitiveField][]time.Time{
		forecast.AstrodomePrimitivePressure:        {validAt},
		forecast.AstrodomePrimitiveMixedLayerDepth: {validAt},
	}
	for columnIndex, support := range stencil.Supports {
		geometry := make([]forecast.AstrodomeHalfLevelGeometry, domeHalfLevelCount)
		for levelIndex := range geometry {
			geometry[levelIndex] = forecast.AstrodomeHalfLevelGeometry{
				ModelHalfLevel: levelIndex + 1,
				HeightM: surfaceHeights[columnIndex] +
					487*float64(domeHalfLevelCount-1-levelIndex),
			}
		}
		fullLevels := make([]forecast.AstrodomeFullLevelPrimitives, domeFullLevelCount)
		for levelIndex := range fullLevels {
			heightM := (geometry[levelIndex].HeightM + geometry[levelIndex+1].HeightM) / 2
			fullLevels[levelIndex] = forecast.AstrodomeFullLevelPrimitives{
				ModelLevel: levelIndex + 1,
				Available: forecast.AstrodomePrimitiveFieldSet(forecast.AstrodomePrimitivePressure) |
					forecast.AstrodomePrimitiveFieldSet(forecast.AstrodomePrimitiveTemperature),
				PressurePa:   100_000 * math.Exp(-(heightM-surfaceHeights[columnIndex])/8000),
				TemperatureK: 250,
			}
		}
		column := forecast.AstrodomePrimitiveColumn{
			ColumnID: support.ColumnID, Location: support.Location,
			HSURFHeightM: surfaceHeights[columnIndex], HalfLevelGeometry: geometry,
			Frames: []forecast.AstrodomePrimitiveColumnFrame{{
				ValidAt: validAt, FullLevels: fullLevels,
				Surface: forecast.AstrodomeSurfacePrimitives{
					Available:        forecast.AstrodomePrimitiveFieldSet(forecast.AstrodomePrimitiveMixedLayerDepth),
					MixedLayerDepthM: mixedLayerDepths[columnIndex],
				},
			}},
		}
		volume.cache[support.ColumnID] = domeVolumeCacheEntry{column: column}
	}
	return volume
}
