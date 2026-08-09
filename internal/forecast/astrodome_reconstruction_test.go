package forecast

import (
	"context"
	"math"
	"reflect"
	"testing"
	"time"
)

func TestAstrodomePreparedRefractiveFrameMatchesDirectReconstruction(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, time.July, 28, 12, 0, 0, 0, time.UTC)
	end := start.Add(3 * time.Hour)
	validAt := start.Add(time.Hour)
	volume := newAstrodomeTestVolume([]time.Time{start, end})
	for supportIndex, support := range volume.stencil.Supports {
		column := volume.columns[support.ColumnID]
		for level := range column.Frames[0].FullLevels {
			column.Frames[0].FullLevels[level].PressurePa += float64(100*supportIndex + 10*level)
			column.Frames[1].FullLevels[level].PressurePa += float64(700 + 80*supportIndex + 20*level)
			column.Frames[0].FullLevels[level].TemperatureK += float64(supportIndex + level)
			column.Frames[1].FullLevels[level].TemperatureK += float64(6 + 2*supportIndex + level)
			column.Frames[0].FullLevels[level].SpecificHumidityKgKg += float64(supportIndex+level) * 1e-5
			column.Frames[1].FullLevels[level].SpecificHumidityKgKg += float64(5+supportIndex+level) * 1e-5
		}
		column.Frames[0].Surface.SurfacePressurePa += float64(20 * supportIndex)
		column.Frames[1].Surface.SurfacePressurePa += float64(300 + 10*supportIndex)
		column.Frames[0].Surface.Temperature2MK += float64(supportIndex)
		column.Frames[1].Surface.Temperature2MK += float64(3 + supportIndex)
		column.Frames[0].Surface.SpecificHumidity2MKgKg += float64(supportIndex) * 1e-5
		column.Frames[1].Surface.SpecificHumidity2MKgKg += float64(3+supportIndex) * 1e-5
		volume.columns[support.ColumnID] = column
	}
	reconstructor, err := NewAstrodomePrimitiveReconstructor(volume)
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := newAstrodomePreparedRefractiveFrame(reconstructor, validAt)
	if err != nil {
		t.Fatal(err)
	}
	location := Location{Latitude: 50.5, Longitude: 30.5, TimeZone: "UTC"}
	for _, heightM := range []float64{1000, 1002, 1251, 1500, 2000, 2500, 3000, 3100} {
		query := AstrodomeReconstructionQuery{ValidAt: validAt, Location: location, HeightM: heightM}
		wantState, wantGradients, directErr := reconstructor.ReconstructRefractivePrimitives(context.Background(), query)
		if directErr != nil {
			t.Fatalf("direct reconstruction at %.3f m: %v", heightM, directErr)
		}
		got, preparedErr := prepared.reconstruct(context.Background(), query)
		if preparedErr != nil {
			t.Fatalf("prepared reconstruction at %.3f m: %v", heightM, preparedErr)
		}
		if !reflect.DeepEqual(got.state, wantState) {
			t.Fatalf("prepared state at %.3f m differs:\n got: %#v\nwant: %#v", heightM, got.state, wantState)
		}
		if !reflect.DeepEqual(got.gradients, wantGradients) {
			t.Fatalf("prepared gradients at %.3f m differ:\n got: %#v\nwant: %#v", heightM, got.gradients, wantGradients)
		}
		if !finite(got.domain.SurfaceHeightM) || !finite(got.domain.ModelTopHeightM) ||
			got.domain.SurfaceHeightM != 1000 || got.domain.ModelTopHeightM != 3000 ||
			got.domain.PartitionID == "" {
			t.Fatalf("prepared domain at %.3f m is invalid: %#v", heightM, got.domain)
		}
	}
}

func BenchmarkAstrodomeRefractiveReconstructionProductionShape(b *testing.B) {
	start := time.Date(2026, time.July, 28, 0, 0, 0, 0, time.UTC)
	validTimes := make([]time.Time, 81)
	for index := range validTimes {
		validTimes[index] = start.Add(time.Duration(index) * time.Hour)
	}
	validAt := validTimes[40]
	volume := newAstrodomeTestVolume(validTimes)
	for _, support := range volume.stencil.Supports {
		column := volume.columns[support.ColumnID]
		column.HalfLevelGeometry = make([]AstrodomeHalfLevelGeometry, astrodomeMaximumTerrainFollowingHalfLevels)
		for level := range column.HalfLevelGeometry {
			column.HalfLevelGeometry[level] = AstrodomeHalfLevelGeometry{
				ModelHalfLevel: level + 1,
				HeightM:        30_000 - float64(level)*(29_000/float64(astrodomeMaximumTerrainFollowingHalfLevels-1)),
			}
		}
		column.HSURFHeightM = column.HalfLevelGeometry[len(column.HalfLevelGeometry)-1].HeightM
		for frameIndex := range column.Frames {
			frame := &column.Frames[frameIndex]
			frame.FullLevels = make([]AstrodomeFullLevelPrimitives, astrodomeMaximumTerrainFollowingHalfLevels-1)
			frame.HalfLevels = make([]AstrodomeHalfLevelPrimitives, astrodomeMaximumTerrainFollowingHalfLevels)
			for level := range frame.FullLevels {
				fraction := float64(level) / float64(len(frame.FullLevels)-1)
				frame.FullLevels[level] = AstrodomeFullLevelPrimitives{
					ModelLevel: level + 1,
					Available: astrodomeTestFieldSet(
						AstrodomePrimitivePressure, AstrodomePrimitiveTemperature,
						AstrodomePrimitiveSpecificHumidity, AstrodomePrimitiveCloudLiquid,
						AstrodomePrimitiveCloudIce, AstrodomePrimitiveCloudFraction,
						AstrodomePrimitiveEastwardWind, AstrodomePrimitiveNorthwardWind,
					),
					PressurePa:           1200 + 88_000*fraction,
					TemperatureK:         225 + 55*fraction,
					SpecificHumidityKgKg: 1e-6 + 0.006*fraction,
				}
			}
			for level := range frame.HalfLevels {
				frame.HalfLevels[level] = AstrodomeHalfLevelPrimitives{
					ModelHalfLevel: level + 1,
					Available: astrodomeTestFieldSet(
						AstrodomePrimitiveVerticalWind, AstrodomePrimitiveTKE,
					),
					TKEJkg: 0.1,
				}
			}
		}
		volume.columns[support.ColumnID] = column
	}
	reconstructor, err := NewAstrodomePrimitiveReconstructor(volume)
	if err != nil {
		b.Fatal(err)
	}
	prepared, err := newAstrodomePreparedRefractiveFrame(reconstructor, validAt)
	if err != nil {
		b.Fatal(err)
	}
	query := AstrodomeReconstructionQuery{
		ValidAt:  validAt,
		Location: Location{Latitude: 50.5, Longitude: 30.5, TimeZone: "UTC"},
		HeightM:  12_345,
	}
	if _, err := prepared.reconstruct(context.Background(), query); err != nil {
		b.Fatal(err)
	}
	b.Run("direct", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			if _, _, err := reconstructor.ReconstructRefractivePrimitives(context.Background(), query); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("prepared", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			if _, err := prepared.reconstruct(context.Background(), query); err != nil {
				b.Fatal(err)
			}
		}
	})
}

type astrodomeTestVolume struct {
	identity               AstrodomePrimitiveVolumeIdentity
	fieldTimes             map[AstrodomePrimitiveField][]time.Time
	stencil                AstrodomeHorizontalStencil
	columns                map[string]AstrodomePrimitiveColumn
	mutateIdentityOnColumn bool
}

func (volume *astrodomeTestVolume) Identity() AstrodomePrimitiveVolumeIdentity {
	return volume.identity
}

func (volume *astrodomeTestVolume) NativeValidTimes(field AstrodomePrimitiveField) []time.Time {
	return volume.fieldTimes[field]
}

func (volume *astrodomeTestVolume) HorizontalStencil(_ context.Context, _ Location) (AstrodomeHorizontalStencil, error) {
	return volume.stencil, nil
}

func (volume *astrodomeTestVolume) Column(_ context.Context, columnID string) (AstrodomePrimitiveColumn, error) {
	column := volume.columns[columnID]
	if volume.mutateIdentityOnColumn {
		volume.identity.RunID += "-changed"
	}
	return column, nil
}

func TestAstrodomeReconstructionInterpolatesPrimitivesBeforeNonlinearPhysics(t *testing.T) {
	t.Parallel()

	validAt := time.Date(2026, time.July, 28, 12, 0, 0, 0, time.UTC)
	volume := newAstrodomeTestVolume([]time.Time{validAt})
	weights := [4]float64{0.1, 0.2, 0.3, 0.4}
	temperatures := [4]float64{250, 260, 270, 280}
	for index := range volume.stencil.Supports {
		volume.stencil.Supports[index].Weight = weights[index]
		column := volume.columns[volume.stencil.Supports[index].ColumnID]
		for frameIndex := range column.Frames {
			for levelIndex := range column.Frames[frameIndex].FullLevels {
				column.Frames[frameIndex].FullLevels[levelIndex].TemperatureK = temperatures[index]
			}
		}
		volume.columns[column.ColumnID] = column
	}
	reconstructor, err := NewAstrodomePrimitiveReconstructor(volume)
	if err != nil {
		t.Fatalf("NewAstrodomePrimitiveReconstructor: %v", err)
	}
	result, err := reconstructor.Reconstruct(context.Background(), AstrodomeReconstructionQuery{
		ValidAt:  validAt,
		Location: Location{Latitude: 50.5, Longitude: 30.5, TimeZone: "UTC"},
		HeightM:  2000,
	})
	if err != nil {
		t.Fatalf("Reconstruct: %v", err)
	}
	wantPrimitive := astrodomeCompensatedWeightedSum(temperatures[:], weights[:])
	assertAstrodomeClose(t, "bilinear temperature primitive", result.TemperatureK, wantPrimitive, 1e-13)

	// A deliberately nonlinear stand-in F(x)=x^2 proves the contract's
	// ordering. The science kernel receives I(x) and computes F(I(x)); it does
	// not receive or interpolate the already-derived F(x).
	derivedAfterInterpolation := result.TemperatureK * result.TemperatureK
	derivedAtColumns := [4]float64{}
	for index, value := range temperatures {
		derivedAtColumns[index] = value * value
	}
	interpolatedDerived := astrodomeCompensatedWeightedSum(derivedAtColumns[:], weights[:])
	if math.Abs(derivedAfterInterpolation-interpolatedDerived) < 1 {
		t.Fatalf("nonlinear ordering fixture collapsed: F(I(x))=%g, I(F(x))=%g", derivedAfterInterpolation, interpolatedDerived)
	}
}

func TestAstrodomeTerrainFollowingGeometryBuildDoesNotAllocate(t *testing.T) {
	validAt := time.Date(2026, time.July, 28, 12, 0, 0, 0, time.UTC)
	volume := newAstrodomeTestVolume([]time.Time{validAt})
	columns := [4]AstrodomePrimitiveColumn{}
	weights := [4]float64{}
	for index, support := range volume.stencil.Supports {
		columns[index] = volume.columns[support.ColumnID]
		weights[index] = support.Weight
	}
	allocations := testing.AllocsPerRun(100, func() {
		geometry := astrodomeTerrainFollowingGeometry{}
		if err := astrodomeBuildTerrainFollowingGeometry(columns, weights, nil, &geometry); err != nil {
			panic(err)
		}
	})
	if allocations != 0 {
		t.Fatalf("terrain-following geometry allocations/run = %.1f, want 0", allocations)
	}
}

func TestAstrodomeReconstructionProvidesAnalyticECEFPrimitiveGradients(t *testing.T) {
	t.Parallel()

	validAt := time.Date(2026, time.July, 28, 12, 0, 0, 0, time.UTC)
	volume := newAstrodomeTestVolume([]time.Time{validAt})
	queryLocation := Location{Latitude: 50.25, Longitude: 30.4, TimeZone: "UTC"}
	for index, support := range volume.stencil.Supports {
		latitudeT := (queryLocation.Latitude - 50) / (51 - 50)
		longitudeT := (queryLocation.Longitude - 30) / (31 - 30)
		north := support.Location.Latitude == 51
		east := support.Location.Longitude == 31
		latitudeWeight := 1 - latitudeT
		if north {
			latitudeWeight = latitudeT
		}
		longitudeWeight := 1 - longitudeT
		if east {
			longitudeWeight = longitudeT
		}
		volume.stencil.Supports[index].Weight = latitudeWeight * longitudeWeight

		column := volume.columns[support.ColumnID]
		value := 250 + 2*support.Location.Latitude + 3*support.Location.Longitude +
			4*(support.Location.Latitude-50)*(support.Location.Longitude-30)
		for frameIndex := range column.Frames {
			for levelIndex := range column.Frames[frameIndex].FullLevels {
				column.Frames[frameIndex].FullLevels[levelIndex].TemperatureK = value
			}
		}
		volume.columns[column.ColumnID] = column
	}
	reconstructor, err := NewAstrodomePrimitiveReconstructor(volume)
	if err != nil {
		t.Fatal(err)
	}
	state, gradients, err := reconstructor.ReconstructRefractivePrimitives(context.Background(), AstrodomeReconstructionQuery{
		ValidAt: validAt, Location: queryLocation, HeightM: 2000,
	})
	if err != nil {
		t.Fatalf("ReconstructRefractivePrimitives: %v", err)
	}
	wantLatitudePerRadian := (2 + 4*0.4) * 180 / math.Pi
	wantLongitudePerRadian := (3 + 4*0.25) * 180 / math.Pi
	radius := AstrodomeICONSphereRadiusM + state.HeightM
	_, east, north, _ := astrodomeObserverBasis(queryLocation, state.HeightM)
	want := north.scale(wantLatitudePerRadian / radius).
		add(east.scale(wantLongitudePerRadian / (radius * math.Cos(queryLocation.Latitude*math.Pi/180))))
	assertAstrodomeClose(t, "temperature gradient X", gradients.TemperatureKPerM.X, want.X, 1e-14)
	assertAstrodomeClose(t, "temperature gradient Y", gradients.TemperatureKPerM.Y, want.Y, 1e-14)
	assertAstrodomeClose(t, "temperature gradient Z", gradients.TemperatureKPerM.Z, want.Z, 1e-14)
}

func TestAstrodomeTerrainFollowingGradientsIncludeMovingHHLAnchors(t *testing.T) {
	t.Parallel()

	validAt := time.Date(2026, time.July, 28, 12, 0, 0, 0, time.UTC)
	base := newAstrodomeTestVolume([]time.Time{validAt})
	for _, support := range base.stencil.Supports {
		column := base.columns[support.ColumnID]
		offsetM := 200*(support.Location.Latitude-50) + 100*(support.Location.Longitude-30)
		for level := range column.HalfLevelGeometry {
			// ICON terrain influence diminishes with height. Deliberately give the
			// two vertical anchors different horizontal slopes so this regression
			// exercises both terms in d(beta)/da, not only a rigidly translated
			// native column.
			terrainFraction := float64(level) / float64(len(column.HalfLevelGeometry)-1)
			column.HalfLevelGeometry[level].HeightM += offsetM * (0.25 + 0.75*terrainFraction)
		}
		column.HSURFHeightM = column.HalfLevelGeometry[len(column.HalfLevelGeometry)-1].HeightM
		base.columns[column.ColumnID] = column
	}
	volume := &astrodomeSlopedTestVolume{astrodomeTestVolume: base}
	reconstructor, err := NewAstrodomePrimitiveReconstructor(volume)
	if err != nil {
		t.Fatal(err)
	}
	location := Location{Latitude: 50.4, Longitude: 30.3, TimeZone: "UTC"}
	const heightM = 2100.0
	evaluate := func(location Location, height float64) (AstrodomeReconstructedAtmosphere, AstrodomeReconstructedSpatialGradients) {
		state, gradients, evaluateErr := reconstructor.ReconstructRefractivePrimitives(
			context.Background(), AstrodomeReconstructionQuery{ValidAt: validAt, Location: location, HeightM: height},
		)
		if evaluateErr != nil {
			t.Fatalf("terrain-following reconstruction at %.8f, %.8f, %.3f m: %v",
				location.Latitude, location.Longitude, height, evaluateErr)
		}
		return state, gradients
	}
	state, gradients := evaluate(location, heightM)
	const deltaDegrees = 1e-5
	northLocation, southLocation := location, location
	northLocation.Latitude += deltaDegrees
	southLocation.Latitude -= deltaDegrees
	northState, _ := evaluate(northLocation, heightM)
	southState, _ := evaluate(southLocation, heightM)
	eastLocation, westLocation := location, location
	eastLocation.Longitude += deltaDegrees
	westLocation.Longitude -= deltaDegrees
	eastState, _ := evaluate(eastLocation, heightM)
	westState, _ := evaluate(westLocation, heightM)

	radius := AstrodomeICONSphereRadiusM + heightM
	latitudeRadians := location.Latitude * math.Pi / 180
	_, east, north, up := astrodomeObserverBasis(location, heightM)
	compare := func(
		name string,
		gradient AstrodomeECEFVector,
		value func(AstrodomeReconstructedAtmosphere) float64,
		tolerance float64,
	) {
		northFinite := (value(northState) - value(southState)) /
			(2 * deltaDegrees * math.Pi / 180 * radius)
		eastFinite := (value(eastState) - value(westState)) /
			(2 * deltaDegrees * math.Pi / 180 * radius * math.Cos(latitudeRadians))
		assertAstrodomeClose(t, name+" moving-HHL north gradient", gradient.dot(north), northFinite, tolerance)
		assertAstrodomeClose(t, name+" moving-HHL east gradient", gradient.dot(east), eastFinite, tolerance)
	}
	compare("pressure", gradients.PressurePaPerM,
		func(value AstrodomeReconstructedAtmosphere) float64 { return value.PressurePa }, 2e-8)
	compare("temperature", gradients.TemperatureKPerM,
		func(value AstrodomeReconstructedAtmosphere) float64 { return value.TemperatureK }, 2e-11)
	compare("specific humidity", gradients.SpecificHumidityPerM,
		func(value AstrodomeReconstructedAtmosphere) float64 { return value.SpecificHumidityKgKg }, 2e-14)
	if math.Abs(gradients.TemperatureKPerM.dot(north)) < 1e-8 ||
		math.Abs(gradients.TemperatureKPerM.dot(east)) < 1e-8 {
		t.Fatal("moving HHL anchors made no horizontal contribution to the analytic gradient")
	}
	assertAstrodomeClose(t, "pressure radial gradient", gradients.PressurePaPerM.dot(up),
		state.VerticalDerivatives.PressurePaPerM, 1e-12)
	assertAstrodomeClose(t, "temperature radial gradient", gradients.TemperatureKPerM.dot(up),
		state.VerticalDerivatives.TemperatureKPerM, 1e-15)
	assertAstrodomeClose(t, "humidity radial gradient", gradients.SpecificHumidityPerM.dot(up),
		state.VerticalDerivatives.SpecificHumidityKgKgPerM, 1e-18)
}

func TestAstrodomeRefractiveReconstructionUsesNativeTwoMetreAnchor(t *testing.T) {
	t.Parallel()

	validAt := time.Date(2026, time.July, 28, 12, 0, 0, 0, time.UTC)
	volume := newAstrodomeTestVolume([]time.Time{validAt})
	reconstructor, err := NewAstrodomePrimitiveReconstructor(volume)
	if err != nil {
		t.Fatal(err)
	}
	query := AstrodomeReconstructionQuery{
		ValidAt: validAt, Location: Location{Latitude: 50.5, Longitude: 30.5, TimeZone: "UTC"}, HeightM: 1002,
	}
	state, gradients, err := reconstructor.ReconstructRefractivePrimitives(context.Background(), query)
	if err != nil {
		t.Fatalf("ReconstructRefractivePrimitives at aperture: %v", err)
	}
	calibration := DefaultAstrodomeScienceCalibration()
	wantPressure, err := AstrodomeHydrostaticPressureAtAperture(
		90000, 280, 0.005, calibration.DryAirGasConstantJKgK, calibration.WaterVapourGasConstantJKgK,
	)
	if err != nil {
		t.Fatal(err)
	}
	assertAstrodomeClose(t, "aperture pressure", state.PressurePa, wantPressure, 1e-9)
	assertAstrodomeClose(t, "aperture temperature", state.TemperatureK, 280, 1e-14)
	assertAstrodomeClose(t, "aperture humidity", state.SpecificHumidityKgKg, 0.005, 1e-14)
	if !finite(gradients.PressurePaPerM.Norm()) || gradients.PressurePaPerM.Norm() == 0 {
		t.Fatal("lower-layer pressure gradient is unavailable")
	}

	query.HeightM = 1251
	middle, _, err := reconstructor.ReconstructRefractivePrimitives(context.Background(), query)
	if err != nil {
		t.Fatalf("ReconstructRefractivePrimitives inside lower layer: %v", err)
	}
	wantTemperature := 280.0 + (275.0-280.0)*(1251.0-1002.0)/(1500.0-1002.0)
	assertAstrodomeClose(t, "lower-layer linear temperature", middle.TemperatureK, wantTemperature, 1e-13)
	wantPressureMiddle := math.Exp(math.Log(wantPressure) +
		(math.Log(85000)-math.Log(wantPressure))*(1251-1002)/(1500-1002))
	assertAstrodomeClose(t, "lower-layer log pressure", middle.PressurePa, wantPressureMiddle, 1e-11)

	query.HeightM = 1000
	surface, _, err := reconstructor.ReconstructRefractivePrimitives(context.Background(), query)
	if err != nil {
		t.Fatalf("ReconstructRefractivePrimitives at model surface: %v", err)
	}
	assertAstrodomeClose(t, "surface pressure", surface.PressurePa, 90000, 1e-9)
	assertAstrodomeClose(t, "surface closure temperature", surface.TemperatureK, 280, 1e-14)
	assertAstrodomeClose(t, "surface closure humidity", surface.SpecificHumidityKgKg, 0.005, 1e-14)

	query.HeightM = 1000 - AstrodomeRefractionPrimitiveEventGuardM - 0.001
	if _, _, err := reconstructor.ReconstructRefractivePrimitives(context.Background(), query); err == nil {
		t.Fatal("refractive reconstruction escaped the lower terrain-event guard")
	}
}

func TestAstrodomeRefractiveUpperFiniteVolumeClosureIsHydrostaticAndSmall(t *testing.T) {
	t.Parallel()

	validAt := time.Date(2026, time.July, 28, 12, 0, 0, 0, time.UTC)
	volume := newAstrodomeTestVolume([]time.Time{validAt})
	for _, support := range volume.stencil.Supports {
		column := volume.columns[support.ColumnID]
		column.HSURFHeightM = 28_000
		column.HalfLevelGeometry = []AstrodomeHalfLevelGeometry{
			{ModelHalfLevel: 1, HeightM: 30_000},
			{ModelHalfLevel: 2, HeightM: 29_000},
			{ModelHalfLevel: 3, HeightM: 28_000},
		}
		for frameIndex := range column.Frames {
			column.Frames[frameIndex].FullLevels[0].PressurePa = 1_200
			column.Frames[frameIndex].FullLevels[0].TemperatureK = 230
			column.Frames[frameIndex].FullLevels[0].SpecificHumidityKgKg = 1e-6
			column.Frames[frameIndex].FullLevels[1].PressurePa = 1_600
			column.Frames[frameIndex].FullLevels[1].TemperatureK = 235
			column.Frames[frameIndex].FullLevels[1].SpecificHumidityKgKg = 2e-6
		}
		volume.columns[column.ColumnID] = column
	}
	reconstructor, err := NewAstrodomePrimitiveReconstructor(volume)
	if err != nil {
		t.Fatal(err)
	}
	query := AstrodomeReconstructionQuery{
		ValidAt: validAt, Location: Location{Latitude: 50.5, Longitude: 30.5, TimeZone: "UTC"},
	}
	const (
		topFullHeightM = 29_500.0
		topHHLHeightM  = 30_000.0
		segments       = 100
	)
	query.HeightM = topFullHeightM
	base, _, err := reconstructor.ReconstructRefractivePrimitives(context.Background(), query)
	if err != nil {
		t.Fatalf("reconstruct top full level: %v", err)
	}
	query.HeightM = topHHLHeightM
	top, _, err := reconstructor.ReconstructRefractivePrimitives(context.Background(), query)
	if err != nil {
		t.Fatalf("reconstruct HHL1: %v", err)
	}
	assertAstrodomeClose(t, "upper closure temperature", top.TemperatureK, 230, 1e-14)
	assertAstrodomeClose(t, "upper closure humidity", top.SpecificHumidityKgKg, 1e-6, 1e-18)
	if !(top.PressurePa < base.PressurePa) || !(top.VerticalDerivatives.PressurePaPerM < 0) {
		t.Fatalf("upper hydrostatic closure did not decrease pressure: base=%g top=%g derivative=%g",
			base.PressurePa, top.PressurePa, top.VerticalDerivatives.PressurePaPerM)
	}
	calibration := DefaultAstrodomeScienceCalibration()
	rMix := (1-1e-6)*calibration.DryAirGasConstantJKgK +
		1e-6*calibration.WaterVapourGasConstantJKgK
	wantTopPressure := 1_200 * math.Exp(-AstrodomeICONReferenceGravityMS2*
		(topHHLHeightM-topFullHeightM)/(rMix*230))
	assertAstrodomeClose(t, "upper hydrostatic pressure", top.PressurePa, wantTopPressure, 1e-12)
	assertAstrodomeClose(t, "upper hydrostatic pressure derivative", top.VerticalDerivatives.PressurePaPerM,
		-AstrodomeICONReferenceGravityMS2*wantTopPressure/(rMix*230), 1e-15)

	// Numerically integrate the phase-index excess of the finite-volume outer
	// half-cell. At a representative ICON model-top state it stays below
	// 3 mm over 500 m, while the total index change is below 4e-7. The latter
	// bounds the refraction sensitivity introduced by the declared closure;
	// this is a regression for a physical upper-atmosphere state, not a claim
	// that a common-mode optical path is relevant to interferometric delay.
	stepM := (topHHLHeightM - topFullHeightM) / segments
	opticalPathExcessM := 0.0
	baseIndex := 0.0
	topIndex := 0.0
	for index := 0; index <= segments; index++ {
		query.HeightM = topFullHeightM + float64(index)*stepM
		state, _, stateErr := reconstructor.ReconstructRefractivePrimitives(context.Background(), query)
		if stateErr != nil {
			t.Fatalf("reconstruct upper closure point %d: %v", index, stateErr)
		}
		refractivity, refractivityErr := AstrodomeCiddorPhaseRefractivity(
			state.PressurePa, state.TemperatureK, state.SpecificHumidityKgKg, 500e-9, 450,
		)
		if refractivityErr != nil {
			t.Fatalf("Ciddor upper closure point %d: %v", index, refractivityErr)
		}
		if index == 0 {
			baseIndex = refractivity.RefractiveIndex
		}
		if index == segments {
			topIndex = refractivity.RefractiveIndex
		}
		weight := 1.0
		if index == 0 || index == segments {
			weight = 0.5
		}
		opticalPathExcessM += weight * (refractivity.RefractiveIndex - 1) * stepM
	}
	if opticalPathExcessM >= 0.003 {
		t.Fatalf("representative upper closure optical-path excess = %.9g m, want < 0.003 m", opticalPathExcessM)
	}
	if baseIndex-topIndex >= 4e-7 {
		t.Fatalf("representative upper closure index decrement = %.9g, want < 4e-7", baseIndex-topIndex)
	}

	query.HeightM = topHHLHeightM + AstrodomeRefractionPrimitiveEventGuardM + 0.001
	if _, _, err := reconstructor.ReconstructRefractivePrimitives(context.Background(), query); err == nil {
		t.Fatal("upper thermodynamic closure escaped its DOPRI event guard")
	}
}

func TestAstrodomeRefractiveGradientRejectsWeightsInconsistentWithQuery(t *testing.T) {
	t.Parallel()

	validAt := time.Date(2026, time.July, 28, 12, 0, 0, 0, time.UTC)
	volume := newAstrodomeTestVolume([]time.Time{validAt})
	volume.stencil.Supports[0].Weight = 0.1
	volume.stencil.Supports[1].Weight = 0.2
	volume.stencil.Supports[2].Weight = 0.3
	volume.stencil.Supports[3].Weight = 0.4
	reconstructor, err := NewAstrodomePrimitiveReconstructor(volume)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = reconstructor.ReconstructRefractivePrimitives(context.Background(), AstrodomeReconstructionQuery{
		ValidAt:  validAt,
		Location: Location{Latitude: 50.5, Longitude: 30.5, TimeZone: "UTC"},
		HeightM:  2000,
	})
	if err == nil {
		t.Fatal("inconsistent bilinear weights were accepted for analytic gradients")
	}
}

func TestAstrodomeReconstructionBuildsLocalTerrainFollowingColumnBeforeVerticalInterpolation(t *testing.T) {
	t.Parallel()

	validAt := time.Date(2026, time.July, 28, 12, 0, 0, 0, time.UTC)
	volume := newAstrodomeTestVolume([]time.Time{validAt})
	weights := [4]float64{0.1, 0.2, 0.3, 0.4}
	halfHeights := [4][3]float64{
		{3000, 2000, 1000},
		{3400, 2200, 1000},
		{3200, 1800, 800},
		{3600, 2400, 1200},
	}
	temperatures := [4][2]float64{
		{250, 270},
		{240, 280},
		{255, 279},
		{245, 275},
	}
	queryHeightM := 2000.0
	for index, support := range volume.stencil.Supports {
		volume.stencil.Supports[index].Weight = weights[index]
		column := volume.columns[support.ColumnID]
		for geometryIndex := range column.HalfLevelGeometry {
			column.HalfLevelGeometry[geometryIndex].HeightM = halfHeights[index][geometryIndex]
		}
		column.HSURFHeightM = halfHeights[index][2]
		for frameIndex := range column.Frames {
			for levelIndex := range column.Frames[frameIndex].FullLevels {
				column.Frames[frameIndex].FullLevels[levelIndex].TemperatureK = temperatures[index][levelIndex]
			}
		}
		volume.columns[column.ColumnID] = column
	}
	localHalfHeights := [3]float64{}
	localTemperatures := [2]float64{}
	for level := range localHalfHeights {
		values := [4]float64{halfHeights[0][level], halfHeights[1][level], halfHeights[2][level], halfHeights[3][level]}
		localHalfHeights[level] = astrodomeCompensatedWeightedSum(values[:], weights[:])
	}
	for level := range localTemperatures {
		values := [4]float64{temperatures[0][level], temperatures[1][level], temperatures[2][level], temperatures[3][level]}
		localTemperatures[level] = astrodomeCompensatedWeightedSum(values[:], weights[:])
	}
	upperFullHeight := (localHalfHeights[0] + localHalfHeights[1]) / 2
	lowerFullHeight := (localHalfHeights[1] + localHalfHeights[2]) / 2
	fraction := (queryHeightM - lowerFullHeight) / (upperFullHeight - lowerFullHeight)
	wantTemperature := localTemperatures[1] + fraction*(localTemperatures[0]-localTemperatures[1])
	wantTemperatureDerivative := (localTemperatures[0] - localTemperatures[1]) /
		(upperFullHeight - lowerFullHeight)
	lowerLogPressure, upperLogPressure := math.Log(85000), math.Log(70000)
	wantPressure := math.Exp(lowerLogPressure + fraction*(upperLogPressure-lowerLogPressure))
	wantPressureDerivative := wantPressure * (upperLogPressure - lowerLogPressure) /
		(upperFullHeight - lowerFullHeight)
	reconstructor, err := NewAstrodomePrimitiveReconstructor(volume)
	if err != nil {
		t.Fatal(err)
	}
	result, err := reconstructor.Reconstruct(context.Background(), AstrodomeReconstructionQuery{
		ValidAt:  validAt,
		Location: Location{Latitude: 50.5, Longitude: 30.5, TimeZone: "UTC"},
		HeightM:  queryHeightM,
	})
	if err != nil {
		t.Fatalf("Reconstruct: %v", err)
	}
	assertAstrodomeClose(t, "heterogeneous-HHL temperature", result.TemperatureK,
		wantTemperature, 1e-12)
	assertAstrodomeClose(t, "heterogeneous-HHL temperature derivative", result.VerticalDerivatives.TemperatureKPerM,
		wantTemperatureDerivative, 1e-15)
	assertAstrodomeClose(t, "heterogeneous-HHL log-pressure", result.PressurePa,
		wantPressure, 1e-9)
	assertAstrodomeClose(t, "heterogeneous-HHL pressure derivative", result.VerticalDerivatives.PressurePaPerM,
		wantPressureDerivative, 1e-12)
}

func TestAstrodomeReconstructionUsesFieldSpecificTemporalBracketsWithoutClamping(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, time.July, 28, 12, 0, 0, 0, time.UTC)
	middle := start.Add(time.Hour)
	end := start.Add(3 * time.Hour)
	volume := newAstrodomeTestVolume([]time.Time{start, middle, end})
	for _, field := range astrodomeRequiredAtmosphericPrimitives() {
		volume.fieldTimes[field] = []time.Time{middle}
	}
	volume.fieldTimes[AstrodomePrimitiveTemperature] = []time.Time{start, end}
	for _, support := range volume.stencil.Supports {
		column := volume.columns[support.ColumnID]
		for levelIndex := range column.Frames[0].FullLevels {
			column.Frames[0].FullLevels[levelIndex].TemperatureK = 260
			column.Frames[1].FullLevels[levelIndex].TemperatureK = 999 // not declared on the temperature time axis
			column.Frames[2].FullLevels[levelIndex].TemperatureK = 290
		}
		volume.columns[column.ColumnID] = column
	}
	reconstructor, err := NewAstrodomePrimitiveReconstructor(volume)
	if err != nil {
		t.Fatal(err)
	}
	result, err := reconstructor.Reconstruct(context.Background(), AstrodomeReconstructionQuery{
		ValidAt:  middle,
		Location: Location{Latitude: 50.5, Longitude: 30.5, TimeZone: "UTC"},
		HeightM:  2000,
	})
	if err != nil {
		t.Fatalf("Reconstruct interpolated time: %v", err)
	}
	assertAstrodomeClose(t, "one-third temporal primitive", result.TemperatureK, 270, 1e-13)

	_, err = reconstructor.Reconstruct(context.Background(), AstrodomeReconstructionQuery{
		ValidAt:  start.Add(-time.Hour),
		Location: Location{Latitude: 50.5, Longitude: 30.5, TimeZone: "UTC"},
		HeightM:  2000,
	})
	if err == nil {
		t.Fatal("time before native support was edge-clamped")
	}

	gapVolume := newAstrodomeTestVolume([]time.Time{start, start.Add(4 * time.Hour)})
	if _, err := NewAstrodomePrimitiveReconstructor(gapVolume); err == nil {
		t.Fatal("unsupported four-hour native gap was accepted")
	}
	nonUTCVolume := newAstrodomeTestVolume([]time.Time{start})
	nonUTCVolume.fieldTimes[AstrodomePrimitiveTemperature] = []time.Time{
		time.Date(2026, time.July, 28, 15, 0, 0, 0, time.FixedZone("MSK", 3*60*60)),
	}
	if _, err := NewAstrodomePrimitiveReconstructor(nonUTCVolume); err == nil {
		t.Fatal("non-UTC native field time was accepted")
	}

	missingFrameVolume := newAstrodomeTestVolume([]time.Time{start, end})
	missingID := missingFrameVolume.stencil.Supports[0].ColumnID
	missingColumn := missingFrameVolume.columns[missingID]
	missingColumn.Frames = missingColumn.Frames[:1]
	missingFrameVolume.columns[missingID] = missingColumn
	missingReconstructor, err := NewAstrodomePrimitiveReconstructor(missingFrameVolume)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := missingReconstructor.Reconstruct(context.Background(), AstrodomeReconstructionQuery{
		ValidAt:  start.Add(time.Hour),
		Location: Location{Latitude: 50.5, Longitude: 30.5, TimeZone: "UTC"},
		HeightM:  2000,
	}); err == nil {
		t.Fatal("missing mandatory right raw frame was accepted")
	}
}

func TestAstrodomeReconstructionRotatesSupportWindsToECEFBeforeCombination(t *testing.T) {
	t.Parallel()

	validAt := time.Date(2026, time.July, 28, 12, 0, 0, 0, time.UTC)
	base := newAstrodomeTestVolume([]time.Time{validAt})
	setConstantAstrodomeTestWind(base, 12, -3, 1.5)
	shifted := shiftedAstrodomeTestVolume(base, 30)

	baseReconstructor, err := NewAstrodomePrimitiveReconstructor(base)
	if err != nil {
		t.Fatal(err)
	}
	shiftedReconstructor, err := NewAstrodomePrimitiveReconstructor(shifted)
	if err != nil {
		t.Fatal(err)
	}
	baseResult, err := baseReconstructor.Reconstruct(context.Background(), AstrodomeReconstructionQuery{
		ValidAt:  validAt,
		Location: Location{Latitude: 50.5, Longitude: 30.5, TimeZone: "UTC"},
		HeightM:  2000,
	})
	if err != nil {
		t.Fatal(err)
	}
	shiftedResult, err := shiftedReconstructor.Reconstruct(context.Background(), AstrodomeReconstructionQuery{
		ValidAt:  validAt,
		Location: Location{Latitude: 50.5, Longitude: 60.5, TimeZone: "UTC"},
		HeightM:  2000,
	})
	if err != nil {
		t.Fatal(err)
	}
	angle := 30 * math.Pi / 180
	wantShifted := AstrodomeECEFVector{
		X: math.Cos(angle)*baseResult.WindECEF.X - math.Sin(angle)*baseResult.WindECEF.Y,
		Y: math.Sin(angle)*baseResult.WindECEF.X + math.Cos(angle)*baseResult.WindECEF.Y,
		Z: baseResult.WindECEF.Z,
	}
	assertAstrodomeClose(t, "rotated ECEF wind X", shiftedResult.WindECEF.X, wantShifted.X, 1e-12)
	assertAstrodomeClose(t, "rotated ECEF wind Y", shiftedResult.WindECEF.Y, wantShifted.Y, 1e-12)
	assertAstrodomeClose(t, "rotated ECEF wind Z", shiftedResult.WindECEF.Z, wantShifted.Z, 1e-12)
}

func TestAstrodomeReconstructionCollocatesWAndTKEDirectlyFromHalfLevels(t *testing.T) {
	t.Parallel()

	validAt := time.Date(2026, time.July, 28, 12, 0, 0, 0, time.UTC)
	volume := newAstrodomeTestVolume([]time.Time{validAt})
	for index := range volume.stencil.Supports {
		if index == 0 {
			volume.stencil.Supports[index].Weight = 1
		} else {
			volume.stencil.Supports[index].Weight = 0
		}
		column := volume.columns[volume.stencil.Supports[index].ColumnID]
		for frameIndex := range column.Frames {
			for fullIndex := range column.Frames[frameIndex].FullLevels {
				column.Frames[frameIndex].FullLevels[fullIndex].EastwardWindMS = 0
				column.Frames[frameIndex].FullLevels[fullIndex].NorthwardWindMS = 0
			}
			column.Frames[frameIndex].HalfLevels[0].VerticalWindMS = 0
			column.Frames[frameIndex].HalfLevels[1].VerticalWindMS = 100
			column.Frames[frameIndex].HalfLevels[2].VerticalWindMS = 0
			column.Frames[frameIndex].HalfLevels[0].TKEJkg = 0
			column.Frames[frameIndex].HalfLevels[1].TKEJkg = 4
			column.Frames[frameIndex].HalfLevels[2].TKEJkg = 0
		}
		volume.columns[column.ColumnID] = column
	}
	reconstructor, err := NewAstrodomePrimitiveReconstructor(volume)
	if err != nil {
		t.Fatal(err)
	}
	result, err := reconstructor.Reconstruct(context.Background(), AstrodomeReconstructionQuery{
		ValidAt:  validAt,
		Location: Location{Latitude: 50.5, Longitude: 30.5, TimeZone: "UTC"},
		HeightM:  2250,
	})
	if err != nil {
		t.Fatal(err)
	}
	columnLocation := volume.stencil.Supports[0].Location
	_, _, _, up := astrodomeObserverBasis(columnLocation, 0)
	verticalWind := result.WindECEF.dot(up)
	verticalWindDerivative := result.VerticalDerivatives.WindECEFPerM.dot(up)
	assertAstrodomeClose(t, "half-level W", verticalWind, 75, 1e-12)
	assertAstrodomeClose(t, "half-level dW/dz", verticalWindDerivative, -0.1, 1e-15)
	assertAstrodomeClose(t, "half-level TKE", result.TKEJkg, 3, 1e-15)
	assertAstrodomeClose(t, "half-level dTKE/dz", result.VerticalDerivatives.TKEJkgPerM, -0.004, 1e-18)
}

func TestAstrodomeReconstructionRejectsInvalidNativeOrderingMissingFieldAndRunChange(t *testing.T) {
	t.Parallel()

	validAt := time.Date(2026, time.July, 28, 12, 0, 0, 0, time.UTC)
	query := AstrodomeReconstructionQuery{
		ValidAt:  validAt,
		Location: Location{Latitude: 50.5, Longitude: 30.5, TimeZone: "UTC"},
		HeightM:  2000,
	}
	outsideFullLevelSupport := newAstrodomeTestVolume([]time.Time{validAt})
	reconstructor, err := NewAstrodomePrimitiveReconstructor(outsideFullLevelSupport)
	if err != nil {
		t.Fatal(err)
	}
	belowLowestFullLevel := query
	belowLowestFullLevel.HeightM = 999.999
	if _, err := reconstructor.Reconstruct(context.Background(), belowLowestFullLevel); err == nil {
		t.Fatal("science reconstruction extrapolated below bilinear model terrain")
	}

	badHHL := newAstrodomeTestVolume([]time.Time{validAt})
	columnID := badHHL.stencil.Supports[0].ColumnID
	column := badHHL.columns[columnID]
	column.HalfLevelGeometry[1].HeightM = column.HalfLevelGeometry[0].HeightM + 1
	badHHL.columns[columnID] = column
	reconstructor, err = NewAstrodomePrimitiveReconstructor(badHHL)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reconstructor.Reconstruct(context.Background(), query); err == nil {
		t.Fatal("reversed native HHL ordering was accepted")
	}

	invertedPressure := newAstrodomeTestVolume([]time.Time{validAt})
	columnID = invertedPressure.stencil.Supports[0].ColumnID
	column = invertedPressure.columns[columnID]
	column.Frames[0].FullLevels[1].PressurePa = column.Frames[0].FullLevels[0].PressurePa
	invertedPressure.columns[columnID] = column
	reconstructor, err = NewAstrodomePrimitiveReconstructor(invertedPressure)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reconstructor.Reconstruct(context.Background(), query); err == nil {
		t.Fatal("non-increasing native pressure profile was accepted")
	}

	invertedSurfacePressure := newAstrodomeTestVolume([]time.Time{validAt})
	columnID = invertedSurfacePressure.stencil.Supports[0].ColumnID
	column = invertedSurfacePressure.columns[columnID]
	column.Frames[0].Surface.SurfacePressurePa = column.Frames[0].FullLevels[len(column.Frames[0].FullLevels)-1].PressurePa
	invertedSurfacePressure.columns[columnID] = column
	reconstructor, err = NewAstrodomePrimitiveReconstructor(invertedSurfacePressure)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reconstructor.Reconstruct(context.Background(), query); err == nil {
		t.Fatal("surface pressure not exceeding the lowest full-level pressure was accepted")
	}

	missing := newAstrodomeTestVolume([]time.Time{validAt})
	columnID = missing.stencil.Supports[0].ColumnID
	column = missing.columns[columnID]
	column.Frames[0].FullLevels[0].Available &^= AstrodomePrimitiveFieldSet(AstrodomePrimitiveSpecificHumidity)
	missing.columns[columnID] = column
	reconstructor, err = NewAstrodomePrimitiveReconstructor(missing)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reconstructor.Reconstruct(context.Background(), query); err == nil {
		t.Fatal("missing mandatory native QV level was accepted")
	}

	changedRun := newAstrodomeTestVolume([]time.Time{validAt})
	changedRun.mutateIdentityOnColumn = true
	reconstructor, err = NewAstrodomePrimitiveReconstructor(changedRun)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reconstructor.Reconstruct(context.Background(), query); err == nil {
		t.Fatal("primitive volume run change during column load was accepted")
	}
}

func newAstrodomeTestVolume(validTimes []time.Time) *astrodomeTestVolume {
	locations := [4]Location{
		{Latitude: 51, Longitude: 30, TimeZone: "UTC"},
		{Latitude: 51, Longitude: 31, TimeZone: "UTC"},
		{Latitude: 50, Longitude: 30, TimeZone: "UTC"},
		{Latitude: 50, Longitude: 31, TimeZone: "UTC"},
	}
	volume := &astrodomeTestVolume{
		identity: AstrodomePrimitiveVolumeIdentity{
			Provider:             "test-icon-eu",
			Product:              "regular-lat-lon",
			Grid:                 "0.0625deg",
			RunID:                "2026072812",
			RunBaseTime:          validTimes[0],
			RunManifestDigest:    "sha256:test",
			InputContractVersion: AstrodomePrimitiveInputContractVersion,
		},
		fieldTimes: make(map[AstrodomePrimitiveField][]time.Time),
		columns:    make(map[string]AstrodomePrimitiveColumn),
	}
	for _, field := range astrodomeRequiredAtmosphericPrimitives() {
		volume.fieldTimes[field] = validTimes
	}
	for _, field := range astrodomeRequiredRefractionSurfacePrimitives() {
		volume.fieldTimes[field] = validTimes
	}
	for index, location := range locations {
		columnID := string(rune('a' + index))
		volume.stencil.Supports[index] = AstrodomeHorizontalSupport{
			ColumnID: columnID,
			Location: location,
			Weight:   0.25,
		}
		volume.columns[columnID] = newAstrodomeTestColumn(columnID, location, validTimes)
	}
	return volume
}

func newAstrodomeTestColumn(columnID string, location Location, validTimes []time.Time) AstrodomePrimitiveColumn {
	fullAvailable := astrodomeTestFieldSet(
		AstrodomePrimitivePressure,
		AstrodomePrimitiveTemperature,
		AstrodomePrimitiveSpecificHumidity,
		AstrodomePrimitiveCloudLiquid,
		AstrodomePrimitiveCloudIce,
		AstrodomePrimitiveCloudFraction,
		AstrodomePrimitiveEastwardWind,
		AstrodomePrimitiveNorthwardWind,
	)
	halfAvailable := astrodomeTestFieldSet(AstrodomePrimitiveVerticalWind, AstrodomePrimitiveTKE)
	column := AstrodomePrimitiveColumn{
		ColumnID:     columnID,
		Location:     location,
		HSURFHeightM: 1000,
		HalfLevelGeometry: []AstrodomeHalfLevelGeometry{
			{ModelHalfLevel: 1, HeightM: 3000},
			{ModelHalfLevel: 2, HeightM: 2000},
			{ModelHalfLevel: 3, HeightM: 1000},
		},
		Frames: make([]AstrodomePrimitiveColumnFrame, len(validTimes)),
	}
	for frameIndex, validAt := range validTimes {
		surfaceAvailable := astrodomeTestFieldSet(
			AstrodomePrimitiveSurfacePressure,
			AstrodomePrimitiveTemperature2M,
			AstrodomePrimitiveSpecificHumidity2M,
		)
		column.Frames[frameIndex] = AstrodomePrimitiveColumnFrame{
			ValidAt: validAt,
			Surface: AstrodomeSurfacePrimitives{
				Available: surfaceAvailable, SurfacePressurePa: 90000,
				Temperature2MK: 280, SpecificHumidity2MKgKg: 0.005,
			},
			FullLevels: []AstrodomeFullLevelPrimitives{
				{
					ModelLevel: 1, Available: fullAvailable, PressurePa: 70000, TemperatureK: 260,
					SpecificHumidityKgKg: 0.003, CloudLiquidKgKg: 0.0001, CloudIceKgKg: 0.00005,
					CloudFraction: 0.2, EastwardWindMS: 5, NorthwardWindMS: 2,
				},
				{
					ModelLevel: 2, Available: fullAvailable, PressurePa: 85000, TemperatureK: 275,
					SpecificHumidityKgKg: 0.006, CloudLiquidKgKg: 0.0002, CloudIceKgKg: 0.0001,
					CloudFraction: 0.4, EastwardWindMS: 7, NorthwardWindMS: 3,
				},
			},
			HalfLevels: []AstrodomeHalfLevelPrimitives{
				{ModelHalfLevel: 1, Available: halfAvailable, VerticalWindMS: 0, TKEJkg: 0.1},
				{ModelHalfLevel: 2, Available: halfAvailable, VerticalWindMS: 0.5, TKEJkg: 0.2},
				{ModelHalfLevel: 3, Available: halfAvailable, VerticalWindMS: 0, TKEJkg: 0.3},
			},
		}
	}
	return column
}

func astrodomeTestFieldSet(fields ...AstrodomePrimitiveField) AstrodomePrimitiveFieldSet {
	set := AstrodomePrimitiveFieldSet(0)
	for _, field := range fields {
		set |= AstrodomePrimitiveFieldSet(field)
	}
	return set
}

func setConstantAstrodomeTestWind(volume *astrodomeTestVolume, eastward, northward, vertical float64) {
	for _, support := range volume.stencil.Supports {
		column := volume.columns[support.ColumnID]
		for frameIndex := range column.Frames {
			for levelIndex := range column.Frames[frameIndex].FullLevels {
				column.Frames[frameIndex].FullLevels[levelIndex].EastwardWindMS = eastward
				column.Frames[frameIndex].FullLevels[levelIndex].NorthwardWindMS = northward
			}
			for levelIndex := range column.Frames[frameIndex].HalfLevels {
				column.Frames[frameIndex].HalfLevels[levelIndex].VerticalWindMS = vertical
			}
		}
		volume.columns[column.ColumnID] = column
	}
}

func TestValidateAstrodomeReconstructedAtmosphereRejectsNonGasMassBudget(t *testing.T) {
	t.Parallel()

	state := AstrodomeReconstructedAtmosphere{
		PressurePa: 80000, TemperatureK: 270,
		SpecificHumidityKgKg: 0.60,
		CloudLiquidKgKg:      0.25,
		CloudIceKgKg:         0.15,
		CloudFraction:        0.5,
		TKEJkg:               0.1,
		VerticalDerivatives: AstrodomeReconstructedVerticalDerivatives{
			PressurePaPerM: -10,
		},
	}
	if err := validateAstrodomeReconstructedAtmosphere(state, false); err == nil {
		t.Fatal("reconstructed condensate plus vapour mass fraction of one was accepted")
	}
	state.CloudIceKgKg = 0.149
	if err := validateAstrodomeReconstructedAtmosphere(state, false); err != nil {
		t.Fatalf("strictly sub-unit reconstructed moist-air mass budget was rejected: %v", err)
	}
	state.VerticalDerivatives.PressurePaPerM = 0
	if err := validateAstrodomeReconstructedAtmosphere(state, false); err == nil {
		t.Fatal("zero reconstructed vertical pressure gradient was accepted")
	}
	state.VerticalDerivatives.PressurePaPerM = 1
	if err := validateAstrodomeReconstructedAtmosphere(state, false); err == nil {
		t.Fatal("positive reconstructed vertical pressure gradient was accepted")
	}
}

func shiftedAstrodomeTestVolume(source *astrodomeTestVolume, longitudeShiftDegrees float64) *astrodomeTestVolume {
	shifted := &astrodomeTestVolume{
		identity:   source.identity,
		fieldTimes: make(map[AstrodomePrimitiveField][]time.Time, len(source.fieldTimes)),
		stencil:    source.stencil,
		columns:    make(map[string]AstrodomePrimitiveColumn, len(source.columns)),
	}
	shifted.identity.RunID += "-shifted"
	shifted.identity.RunManifestDigest += "-shifted"
	for field, times := range source.fieldTimes {
		shifted.fieldTimes[field] = append([]time.Time(nil), times...)
	}
	for index, support := range shifted.stencil.Supports {
		support.Location.Longitude = normalizeAstrodomeLongitude(support.Location.Longitude + longitudeShiftDegrees)
		shifted.stencil.Supports[index] = support
		column := cloneAstrodomePrimitiveColumn(source.columns[support.ColumnID])
		column.Location = support.Location
		shifted.columns[column.ColumnID] = column
	}
	return shifted
}
