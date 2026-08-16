package iconeu

import (
	"context"
	"math"
	"testing"
	"time"

	"bot_astrosferum/internal/forecast"
	"bot_astrosferum/internal/model"
)

func TestDomeAstrodomePBLBreakpointsUseNativeMixedLayerDepth(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name             string
		depthM           float64
		elevationDegrees float64
		intervalEndM     float64
	}{
		{name: "below-former-lower-clamp", depthM: 250, elevationDegrees: 45, intervalEndM: 900},
		{name: "above-former-upper-clamp", depthM: 2600, elevationDegrees: 80, intervalEndM: 3200},
	} {
		t.Run(test.name, func(t *testing.T) {
			grid := Coverage()
			observer := forecast.Location{
				Latitude: grid.MinLat + 10.5*grid.Increment, Longitude: grid.MinLon + 10.5*grid.Increment,
				TimeZone: "UTC",
			}
			ray := traceDomeAstrodomePBLTestRay(t, observer, test.elevationDegrees, 90)
			interval := domeAstrodomePBLTestInterval(t, newDomeAstrodomeGridTestVolume(grid), ray, 50, test.intervalEndM)
			validAt := time.Date(2026, time.July, 30, 0, 0, 0, 0, time.UTC)
			volume := newDomeAstrodomePBLTestVolume(t, grid, interval.stencil, validAt,
				[4]float64{100, 100, 100, 100}, [4]float64{test.depthM, test.depthM, test.depthM, test.depthM},
			)

			valueAt := func(pathM float64) (float64, error) {
				point, err := ray.PointAtPathLength(pathM)
				if err != nil {
					return 0, err
				}
				return point.HeightM - 100 - test.depthM, nil
			}
			wantRoot, err := domeBisectionPathRoot(interval.startM, interval.endM, 1e-9, valueAt)
			if err != nil {
				t.Fatal(err)
			}
			breakpoints, err := volume.domePhysicalBreakpoints(
				context.Background(), ray, validAt, interval, forecast.DefaultAstrodomeScienceCalibration(),
			)
			if err != nil {
				t.Fatalf("production physical-breakpoint planner rejected native MH %.0f m: %v", test.depthM, err)
			}
			found := false
			for _, breakpoint := range breakpoints {
				if math.Abs(breakpoint-wantRoot) <= domeAstrodomePhysicalRootToleranceM {
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("physical breakpoints omit native-MH root %.12g for depth %.0f m: %v", wantRoot, test.depthM, breakpoints)
			}
		})
	}
}

func traceDomeAstrodomePBLTestRay(
	t *testing.T,
	observer forecast.Location,
	elevationDegrees, azimuthDegrees float64,
) forecast.AstrodomeRefractedRay {
	t.Helper()
	const observerHeightM = 100.0
	initial, err := forecast.NewAstrodomeRay(observer, observerHeightM, elevationDegrees, &azimuthDegrees)
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
				HeightM:        surfaceHeights[columnIndex] + 487*float64(domeHalfLevelCount-1-levelIndex),
			}
		}
		fullLevels := make([]forecast.AstrodomeFullLevelPrimitives, domeFullLevelCount)
		for levelIndex := range fullLevels {
			heightM := (geometry[levelIndex].HeightM + geometry[levelIndex+1].HeightM) / 2
			fullLevels[levelIndex] = forecast.AstrodomeFullLevelPrimitives{
				ModelLevel: levelIndex + 1,
				Available: forecast.AstrodomePrimitiveFieldSet(forecast.AstrodomePrimitivePressure) |
					forecast.AstrodomePrimitiveFieldSet(forecast.AstrodomePrimitiveTemperature),
				PressurePa: 100_000 * math.Exp(-(heightM-surfaceHeights[columnIndex])/8000), TemperatureK: 250,
			}
		}
		volume.cache[support.ColumnID] = domeVolumeCacheEntry{column: forecast.AstrodomePrimitiveColumn{
			ColumnID: support.ColumnID, Location: support.Location, HSURFHeightM: surfaceHeights[columnIndex],
			HalfLevelGeometry: geometry,
			Frames: []forecast.AstrodomePrimitiveColumnFrame{{
				ValidAt: validAt, FullLevels: fullLevels,
				Surface: forecast.AstrodomeSurfacePrimitives{
					Available:        forecast.AstrodomePrimitiveFieldSet(forecast.AstrodomePrimitiveMixedLayerDepth),
					MixedLayerDepthM: mixedLayerDepths[columnIndex],
				},
			}},
		}}
	}
	return volume
}
