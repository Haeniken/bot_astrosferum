package forecast

import (
	"context"
	"errors"
	"fmt"
	"math"
	"reflect"
	"testing"
	"time"
)

// astrodomeSlopedTestVolume keeps the four native columns from the common
// reconstruction fixture, but recomputes their bilinear weights at every ray
// point. It therefore exercises the same sloping four-column support that a
// production refraction field sees instead of freezing the observer stencil.
type astrodomeSlopedTestVolume struct {
	*astrodomeTestVolume
}

type astrodomeLegacyReconstructedRefractionField struct {
	reconstructor *AstrodomePrimitiveReconstructor
	domain        AstrodomeRefractionDomainResolver
	validAt       time.Time
	calibration   AstrodomeRefractivityCalibration
}

func (field astrodomeLegacyReconstructedRefractionField) EvaluateAstrodomeRefraction(
	ctx context.Context,
	positionECEF AstrodomeECEFVector,
) (AstrodomeRefractionFieldSample, error) {
	point, err := astrodomeRayPointFromECEF(positionECEF, 0, field.validAt.Location())
	if err != nil {
		return AstrodomeRefractionFieldSample{}, err
	}
	state, gradients, err := field.reconstructor.ReconstructRefractivePrimitives(ctx, AstrodomeReconstructionQuery{
		ValidAt: field.validAt, Location: point.Location, HeightM: point.HeightM,
	})
	if err != nil {
		return AstrodomeRefractionFieldSample{}, err
	}
	domain, err := field.domain.ResolveAstrodomeRefractionDomain(ctx, field.validAt, point, state.HorizontalStencil)
	if err != nil {
		return AstrodomeRefractionFieldSample{}, err
	}
	refractivity, err := AstrodomeCiddorPhaseRefractivity(
		state.PressurePa, state.TemperatureK, state.SpecificHumidityKgKg,
		field.calibration.WavelengthM, field.calibration.CarbonDioxidePPM,
	)
	if err != nil {
		return AstrodomeRefractionFieldSample{}, err
	}
	gradient := gradients.PressurePaPerM.scale(refractivity.DerivativePressurePa).
		add(gradients.TemperatureKPerM.scale(refractivity.DerivativeTemperatureK)).
		add(gradients.SpecificHumidityPerM.scale(refractivity.DerivativeSpecificHumidityKgKg))
	return AstrodomeRefractionFieldSample{
		RefractivityVersion:     refractivity.Version,
		RefractiveIndex:         refractivity.RefractiveIndex,
		GradientECEF:            gradient,
		PartitionID:             domain.PartitionID,
		SignedSurfaceDistanceM:  point.HeightM - domain.SurfaceHeightM,
		SignedModelTopDistanceM: point.HeightM - domain.ModelTopHeightM,
	}, nil
}

func TestAstrodomePreparedRefractionMatchesLegacyFullRay(t *testing.T) {
	t.Parallel()

	validAt := time.Date(2026, time.July, 28, 12, 0, 0, 0, time.UTC)
	volume := &astrodomeSlopedTestVolume{astrodomeTestVolume: newAstrodomeTestVolume([]time.Time{validAt})}
	reconstructor, err := NewAstrodomePrimitiveReconstructor(volume)
	if err != nil {
		t.Fatal(err)
	}
	calibration := DefaultAstrodomeRefractivityCalibration()
	prepared, err := NewAstrodomeReconstructedRefractivityField(reconstructor, validAt, calibration)
	if err != nil {
		t.Fatal(err)
	}
	legacy := astrodomeLegacyReconstructedRefractionField{
		reconstructor: reconstructor,
		domain:        volume,
		validAt:       validAt,
		calibration:   calibration,
	}
	observer := Location{Latitude: 50.5, Longitude: 30.5, TimeZone: "UTC"}
	for _, elevation := range []float64{10, 45, 80} {
		azimuth := 90.0
		initial, rayErr := NewAstrodomeRay(observer, 1002, elevation, &azimuth)
		if rayErr != nil {
			t.Fatal(rayErr)
		}
		want, legacyErr := TraceAstrodomeRefractedRay(
			context.Background(), legacy, initial, DefaultAstrodomeRefractionCalibration(),
		)
		if legacyErr != nil {
			t.Fatalf("legacy %.0f-degree ray: %v", elevation, legacyErr)
		}
		got, preparedErr := TraceAstrodomeRefractedRay(
			context.Background(), prepared, initial, DefaultAstrodomeRefractionCalibration(),
		)
		if preparedErr != nil {
			t.Fatalf("prepared %.0f-degree ray: %v", elevation, preparedErr)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("prepared %.0f-degree ray differs from the legacy solver:\n got: %#v\nwant: %#v", elevation, got, want)
		}
	}
}

func (volume *astrodomeSlopedTestVolume) HorizontalStencil(
	_ context.Context,
	location Location,
) (AstrodomeHorizontalStencil, error) {
	south, north := volume.stencil.Supports[0].Location.Latitude, volume.stencil.Supports[0].Location.Latitude
	west, east := volume.stencil.Supports[0].Location.Longitude, volume.stencil.Supports[0].Location.Longitude
	for _, support := range volume.stencil.Supports[1:] {
		south = math.Min(south, support.Location.Latitude)
		north = math.Max(north, support.Location.Latitude)
		west = math.Min(west, support.Location.Longitude)
		east = math.Max(east, support.Location.Longitude)
	}
	if north <= south || east <= west || location.Latitude < south || location.Latitude > north ||
		location.Longitude < west || location.Longitude > east {
		return AstrodomeHorizontalStencil{}, fmt.Errorf("sloped test query escaped its grid cell")
	}
	stencil := volume.stencil
	latitudeT := (location.Latitude - south) / (north - south)
	longitudeT := (location.Longitude - west) / (east - west)
	for index, support := range stencil.Supports {
		latitudeWeight := 1 - latitudeT
		if support.Location.Latitude == north {
			latitudeWeight = latitudeT
		}
		longitudeWeight := 1 - longitudeT
		if support.Location.Longitude == east {
			longitudeWeight = longitudeT
		}
		stencil.Supports[index].Weight = latitudeWeight * longitudeWeight
	}
	return stencil, stencil.Validate()
}

func (volume *astrodomeSlopedTestVolume) ResolveAstrodomeRefractionDomain(
	ctx context.Context,
	_ time.Time,
	point AstrodomeRayPoint,
	stencil AstrodomeHorizontalStencil,
) (AstrodomeRefractionDomainPoint, error) {
	expected, err := volume.HorizontalStencil(ctx, point.Location)
	if err != nil {
		return AstrodomeRefractionDomainPoint{}, err
	}
	surfaceHeightM, modelTopHeightM := 0.0, 0.0
	halfHeights := make([]float64, len(volume.columns[expected.Supports[0].ColumnID].HalfLevelGeometry))
	for index, support := range expected.Supports {
		provided := stencil.Supports[index]
		if support.ColumnID != provided.ColumnID || math.Abs(support.Weight-provided.Weight) > 1e-12 {
			return AstrodomeRefractionDomainPoint{}, fmt.Errorf("sloped test domain received a different stencil")
		}
		column := volume.columns[support.ColumnID]
		surfaceHeightM += support.Weight * column.HSURFHeightM
		modelTopHeightM += support.Weight * column.HalfLevelGeometry[0].HeightM
		for level := range halfHeights {
			halfHeights[level] += support.Weight * column.HalfLevelGeometry[level].HeightM
		}
	}
	partition := "sloped-test-cell:lower-boundary"
	topFull := (halfHeights[0] + halfHeights[1]) / 2
	bottomFull := (halfHeights[len(halfHeights)-2] + halfHeights[len(halfHeights)-1]) / 2
	if point.HeightM > topFull {
		partition = "sloped-test-cell:upper-hydrostatic"
	} else if point.HeightM >= bottomFull {
		partition = "sloped-test-cell:full-level"
	}
	return AstrodomeRefractionDomainPoint{
		PartitionID: partition, SurfaceHeightM: surfaceHeightM, ModelTopHeightM: modelTopHeightM,
	}, nil
}

func TestAstrodomeRefractionUsesBilinearTerrainRatherThanHighestSupportCorner(t *testing.T) {
	t.Parallel()

	validAt := time.Date(2026, time.July, 28, 12, 0, 0, 0, time.UTC)
	base := newAstrodomeTestVolume([]time.Time{validAt})
	// The centre has H_SURF=1004 m and an aperture at 1006 m. Its north-east
	// support has H_SURF=1016 m, so the valid observer is deliberately below
	// that individual corner. A max-corner terrain test would reject it even
	// though it is exactly two metres above the bilinear model surface.
	offsets := [4]float64{8, 16, -8, 0}
	for index, support := range base.stencil.Supports {
		column := base.columns[support.ColumnID]
		column.HSURFHeightM += offsets[index]
		for level := range column.HalfLevelGeometry {
			column.HalfLevelGeometry[level].HeightM += offsets[index]
		}
		base.columns[column.ColumnID] = column
	}
	volume := &astrodomeSlopedTestVolume{astrodomeTestVolume: base}
	reconstructor, err := NewAstrodomePrimitiveReconstructor(volume)
	if err != nil {
		t.Fatal(err)
	}
	observer := Location{Latitude: 50.5, Longitude: 30.5, TimeZone: "UTC"}
	stencil, err := volume.HorizontalStencil(context.Background(), observer)
	if err != nil {
		t.Fatal(err)
	}
	domain, err := volume.ResolveAstrodomeRefractionDomain(context.Background(), validAt,
		AstrodomeRayPoint{Location: observer}, stencil)
	if err != nil {
		t.Fatal(err)
	}
	observerHeightM := domain.SurfaceHeightM + AstrodomeRefractionApertureHeightAGLM
	field, err := NewAstrodomeReconstructedRefractivityField(
		reconstructor, validAt, DefaultAstrodomeRefractivityCalibration(),
	)
	if err != nil {
		t.Fatal(err)
	}
	observerECEF, _, _, _ := astrodomeObserverBasis(observer, observerHeightM)
	sample, err := field.EvaluateAstrodomeRefraction(context.Background(), observerECEF)
	if err != nil {
		t.Fatalf("bilinear aperture was rejected by a higher support corner: %v", err)
	}
	assertAstrodomeClose(t, "bilinear aperture terrain clearance", sample.SignedSurfaceDistanceM,
		AstrodomeRefractionApertureHeightAGLM, 1e-7)

	azimuth := 0.0
	initial, err := NewAstrodomeRay(observer, observerHeightM, 10, &azimuth)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = TraceAstrodomeRefractedRay(context.Background(), field, initial,
		DefaultAstrodomeRefractionCalibration()); err != nil {
		t.Fatalf("10-degree ray above bilinear terrain was rejected: %v", err)
	}
}

func TestAstrodomeRefractionRejectsObserverBelowBilinearTerrain(t *testing.T) {
	t.Parallel()

	azimuth := 0.0
	initial, err := NewAstrodomeRay(
		Location{Latitude: 0, Longitude: 0, TimeZone: "UTC"}, 999.5, 10, &azimuth,
	)
	if err != nil {
		t.Fatal(err)
	}
	field := astrodomeAnalyticRefractionField{
		refractiveIndex: func(AstrodomeECEFVector) float64 { return 1.00027 },
		gradient:        func(AstrodomeECEFVector) AstrodomeECEFVector { return AstrodomeECEFVector{} },
		surfaceDistance: func(position AstrodomeECEFVector) float64 {
			return position.Norm() - (AstrodomeICONSphereRadiusM + 1000)
		},
		topDistance: func(position AstrodomeECEFVector) float64 {
			return position.Norm() - (AstrodomeICONSphereRadiusM + 3000)
		},
	}
	_, err = TraceAstrodomeRefractedRay(context.Background(), field, initial,
		DefaultAstrodomeRefractionCalibration())
	if !errors.Is(err, ErrAstrodomeRefractionTerrain) {
		t.Fatalf("observer below bilinear terrain returned %v, want ErrAstrodomeRefractionTerrain", err)
	}
}

func TestAstrodomeRefractionDetectsTerrainInsideAcceptedStepWithClearEndpoints(t *testing.T) {
	t.Parallel()

	azimuth := 0.0
	initial, err := NewAstrodomeRay(
		Location{Latitude: 0, Longitude: 0, TimeZone: "UTC"}, 1, 10, &azimuth,
	)
	if err != nil {
		t.Fatal(err)
	}
	field := astrodomeAnalyticRefractionField{
		refractiveIndex: func(AstrodomeECEFVector) float64 { return 1.00027 },
		gradient:        func(AstrodomeECEFVector) AstrodomeECEFVector { return AstrodomeECEFVector{} },
		surfaceDistance: func(position AstrodomeECEFVector) float64 {
			// The first accepted constant-index DOPRI step spans about 24.6 m
			// northward. Both endpoints are clear, while its c=0.3 and c=0.8
			// interior probes pass through this synthetic ridge.
			if position.Z > 6 && position.Z < 22 {
				return -1
			}
			return 1
		},
		topDistance: func(position AstrodomeECEFVector) float64 {
			return position.Norm() - (AstrodomeICONSphereRadiusM + 3000)
		},
	}
	calibration := DefaultAstrodomeRefractionCalibration()
	pass := astrodomeRefractionPass{refractivityVersion: AstrodomeCiddorVersion}
	step, err := astrodomeDOPRIStep(context.Background(), field,
		astrodomeInitialRefractionState(initial), 0, calibration.InitialStepM, calibration, 1, &pass)
	if err != nil {
		t.Fatal(err)
	}
	if step.errorRatio > 1 || step.stageSamples[0].SignedSurfaceDistanceM <= 0 ||
		step.endSample.SignedSurfaceDistanceM <= 0 {
		t.Fatalf("terrain regression needs an accepted step with clear endpoints: error=%g start=%g end=%g",
			step.errorRatio, step.stageSamples[0].SignedSurfaceDistanceM, step.endSample.SignedSurfaceDistanceM)
	}
	interiorNegative := false
	for stage := 1; stage <= 4; stage++ {
		interiorNegative = interiorNegative || step.stageSamples[stage].SignedSurfaceDistanceM < 0
	}
	if !interiorNegative {
		t.Fatal("terrain regression has no negative interior DOPRI sample")
	}
	intersects, err := astrodomeAcceptedRefractionStepIntersectsTerrain(
		context.Background(), field, step, calibration.EventPathToleranceM, &pass,
	)
	if err != nil || !intersects {
		t.Fatalf("accepted dense segment interior terrain detection = %v, %v; want true", intersects, err)
	}
	_, err = TraceAstrodomeRefractedRay(context.Background(), field, initial,
		calibration)
	if !errors.Is(err, ErrAstrodomeRefractionTerrain) {
		t.Fatalf("interior terrain contact with clear step endpoints returned %v, want ErrAstrodomeRefractionTerrain", err)
	}
}

func TestAstrodomeReconstructedRefractionDetectsLocalBilinearTerrainBlock(t *testing.T) {
	t.Parallel()

	validAt := time.Date(2026, time.July, 28, 12, 0, 0, 0, time.UTC)
	base := newAstrodomeTestVolume([]time.Time{validAt})
	const (
		south = 50.0
		north = 50.01
		west  = 30.0
		east  = 30.01
	)
	for index, support := range base.stencil.Supports {
		location := support.Location
		if location.Latitude == 51 {
			location.Latitude = north
		} else {
			location.Latitude = south
		}
		if location.Longitude == 31 {
			location.Longitude = east
		} else {
			location.Longitude = west
		}
		base.stencil.Supports[index].Location = location
		column := base.columns[support.ColumnID]
		column.Location = location
		if location.Latitude == north {
			column.HSURFHeightM += 400
			for level := range column.HalfLevelGeometry {
				column.HalfLevelGeometry[level].HeightM += 400
			}
		}
		base.columns[support.ColumnID] = column
	}
	volume := &astrodomeSlopedTestVolume{astrodomeTestVolume: base}
	reconstructor, err := NewAstrodomePrimitiveReconstructor(volume)
	if err != nil {
		t.Fatal(err)
	}
	observer := Location{Latitude: (south + north) / 2, Longitude: (west + east) / 2, TimeZone: "UTC"}
	stencil, err := volume.HorizontalStencil(context.Background(), observer)
	if err != nil {
		t.Fatal(err)
	}
	domain, err := volume.ResolveAstrodomeRefractionDomain(context.Background(), validAt,
		AstrodomeRayPoint{Location: observer}, stencil)
	if err != nil {
		t.Fatal(err)
	}
	field, err := NewAstrodomeReconstructedRefractivityField(
		reconstructor, validAt, DefaultAstrodomeRefractivityCalibration(),
	)
	if err != nil {
		t.Fatal(err)
	}
	azimuth := 0.0
	initial, err := NewAstrodomeRay(observer,
		domain.SurfaceHeightM+AstrodomeRefractionApertureHeightAGLM, 10, &azimuth)
	if err != nil {
		t.Fatal(err)
	}
	_, err = TraceAstrodomeRefractedRay(context.Background(), field, initial,
		DefaultAstrodomeRefractionCalibration())
	if !errors.Is(err, ErrAstrodomeRefractionTerrain) {
		t.Fatalf("reconstructed local bilinear terrain block returned %v, want ErrAstrodomeRefractionTerrain", err)
	}
}
