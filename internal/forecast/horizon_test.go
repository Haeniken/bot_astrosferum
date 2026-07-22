package forecast

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"
)

func TestHorizonPlanUsesExactSphericalIntersectionAndMidpoints(t *testing.T) {
	observer := Location{Latitude: 59.9386, Longitude: 30.3141, TimeZone: "Europe/Moscow"}
	plan, err := NewHorizonPlan(observer, 0)
	if err != nil {
		t.Fatal(err)
	}
	if plan.AlgorithmVersion != HorizonAlgorithmVersion || len(plan.Directions) != 8 {
		t.Fatalf("unexpected horizon plan header: %+v", plan)
	}
	wantSurfaceEnd, err := HorizonSurfaceDistanceAtHeight(0, HorizonAtmosphereTopM)
	if err != nil {
		t.Fatal(err)
	}
	wantLOS, _, err := horizonRayIntersection(0, HorizonAtmosphereTopM)
	if err != nil {
		t.Fatal(err)
	}
	wantSamples := int(math.Ceil(wantSurfaceEnd / HorizonSurfaceSegmentLengthM))
	for _, direction := range plan.Directions {
		if len(direction.Samples) != wantSamples {
			t.Fatalf("%s sample count = %d, want %d", direction.Direction, len(direction.Samples), wantSamples)
		}
		var totalLOS float64
		for index, sample := range direction.Samples {
			if math.Abs(sample.StartSurfaceDistanceM-float64(index)*HorizonSurfaceSegmentLengthM) > 1e-6 {
				t.Fatalf("%s segment %d starts at %v", direction.Direction, index, sample.StartSurfaceDistanceM)
			}
			if sample.EndSurfaceDistanceM-sample.StartSurfaceDistanceM > HorizonSurfaceSegmentLengthM+1e-6 {
				t.Fatalf("%s segment %d exceeds fixed surface step", direction.Direction, index)
			}
			if index > 0 && !(sample.RayHeightM > direction.Samples[index-1].RayHeightM) {
				t.Fatalf("%s ray heights are not increasing", direction.Direction)
			}
			if index > 0 && sample.RayHeightM-direction.Samples[index-1].RayHeightM > 100 {
				t.Fatalf("%s segment %d changes ray height by more than 100 m", direction.Direction, index)
			}
			totalLOS += sample.LOSPathLengthM
		}
		last := direction.Samples[len(direction.Samples)-1]
		if math.Abs(last.EndSurfaceDistanceM-wantSurfaceEnd) > 1e-6 {
			t.Fatalf("%s final surface distance = %.9f, want %.9f", direction.Direction, last.EndSurfaceDistanceM, wantSurfaceEnd)
		}
		if math.Abs(totalLOS-wantLOS) > 1e-6 {
			t.Fatalf("%s LOS length = %.9f, want %.9f", direction.Direction, totalLOS, wantLOS)
		}
		if !(last.RayHeightM < HorizonAtmosphereTopM) {
			t.Fatalf("%s final midpoint is not inside atmosphere: %v", direction.Direction, last.RayHeightM)
		}
	}
}

func TestHorizonTenDegreeSurfaceDistances(t *testing.T) {
	checks := []struct {
		heightM float64
		wantM   float64
	}{
		{1000, 5656.153340077},
		{2000, 11282.354431888},
		{5000, 27985.673161230},
		{10000, 55265.242534717},
		{15000, 81887.182496787},
		{22300, 119662.459323427},
	}
	for _, check := range checks {
		got, err := HorizonSurfaceDistanceAtHeight(0, check.heightM)
		if err != nil {
			t.Fatal(err)
		}
		if math.Abs(got-check.wantM) > 1e-6 {
			t.Fatalf("surface distance at %.1f km = %.9f m, want %.9f", check.heightM/1000, got, check.wantM)
		}
	}
}

func TestHorizonPlanNormalizesDatelineAndSupportsHighLatitude(t *testing.T) {
	dateline, err := NewHorizonPlan(Location{Latitude: 0, Longitude: 179.99, TimeZone: "UTC"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	crossed := false
	for _, sample := range dateline.Directions[2].Samples {
		if sample.Midpoint.Longitude < -179.99 {
			crossed = true
			break
		}
	}
	if !crossed {
		t.Fatal("eastward horizon footprint did not cross the dateline")
	}
	for _, location := range dateline.FootprintLocations() {
		if location.Longitude < -180 || location.Longitude >= 180 {
			t.Fatalf("longitude was not normalized: %+v", location)
		}
	}

	polar, err := NewHorizonPlan(Location{Latitude: 89.999, Longitude: -170, TimeZone: "UTC"}, 20)
	if err != nil {
		t.Fatal(err)
	}
	for _, location := range polar.FootprintLocations() {
		if !finite(location.Latitude) || !finite(location.Longitude) || math.Abs(location.Latitude) > 90 ||
			location.Longitude < -180 || location.Longitude >= 180 {
			t.Fatalf("invalid high-latitude destination: %+v", location)
		}
	}
}

func TestHorizonPlanRejectsExactPoles(t *testing.T) {
	for _, latitude := range []float64{-90, 90} {
		if _, err := NewHorizonPlan(Location{Latitude: latitude, Longitude: 10, TimeZone: "UTC"}, 0); err == nil {
			t.Fatalf("exact pole %v unexpectedly accepted", latitude)
		}
	}
}

func TestHorizonFootprintCoverageChecksEveryActualLookupPoint(t *testing.T) {
	plan, err := NewHorizonPlan(Location{Latitude: 45, Longitude: 10, TimeZone: "UTC"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	wantCalls := len(plan.FootprintLocations())
	calls := 0
	if !HorizonFootprintCovered(plan, func(Location) bool {
		calls++
		return true
	}) {
		t.Fatal("fully covered footprint reported unavailable")
	}
	if calls != wantCalls || len(plan.FootprintLocations()) != wantCalls {
		t.Fatalf("coverage checked %d points, want %d actual observer/midpoint lookups", calls, wantCalls)
	}

	excluded := plan.Directions[len(plan.Directions)-1].Samples[len(plan.Directions[0].Samples)-1].Midpoint
	if HorizonFootprintCovered(plan, func(location Location) bool {
		return math.Abs(location.Latitude-excluded.Latitude) > horizonCoordinateComparisonEpsilon ||
			math.Abs(location.Longitude-excluded.Longitude) > horizonCoordinateComparisonEpsilon
	}) {
		t.Fatal("footprint helper ignored an uncovered boundary midpoint")
	}
}

func TestComputeHorizonHomogeneousSlantSeeingHasExpectedGeometricScale(t *testing.T) {
	// A 2 km test quadrature places its first midpoint above the deliberately
	// bounded 100 m Masciadri ground layer. That keeps this regression focused
	// on the homogeneous HMNSP99 slant/zenith geometry ratio; the hybrid ground
	// layer is covered independently below.
	plan := mustTestHorizonPlan(t, 2000)
	validAt := time.Date(2026, 7, 22, 0, 0, 0, 0, time.UTC)
	snapshot := homogeneousHorizonSnapshot(plan, validAt, 0, 0)
	calibration := DefaultOverallIndexCalibration()
	calibration.BoundaryLayerMinM = 100
	results, err := ComputeHorizon(snapshot, plan, calibration)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 8 || !results[0].Available {
		t.Fatalf("horizon result is unavailable: %+v", results[0])
	}
	firstProfile := snapshot.Directions[0].Samples[0].Vertical.Levels
	localCn2, ok := hmnsp99LayerCn2(firstProfile[0], firstProfile[1], math.NaN())
	if !ok {
		t.Fatal("homogeneous HMNSP layer is invalid")
	}
	zenith := opticalTurbulenceMetricsFromMoments(localCn2*HorizonAtmosphereTopM, 0, 1).SeeingArcsec
	ratio := results[0].SeeingArcsec / zenith
	losPathM := 0.0
	for _, sample := range plan.Directions[0].Samples {
		losPathM += sample.LOSPathLengthM
	}
	want := horizonHomogeneousSeeingScale(plan.ObserverSurfaceElevationM, losPathM)
	if math.Abs(ratio-want)/want > 1e-9 {
		t.Fatalf("homogeneous slant seeing scale = %v, want geometric path scale %v", ratio, want)
	}
	for index := 1; index < len(results); index++ {
		if math.Abs(results[index].SeeingArcsec-results[0].SeeingArcsec) > 1e-12 {
			t.Fatalf("homogeneous seeing differs by azimuth: %v vs %v", results[index].SeeingArcsec, results[0].SeeingArcsec)
		}
	}
	// Eastward wind is mostly along the east ray, so the required 3-D
	// perpendicular component gives it a longer tau0 than the north ray.
	if !(results[2].CoherenceTimeMS > results[0].CoherenceTimeMS) {
		t.Fatalf("perpendicular-wind tau0 not directional: E=%v N=%v", results[2].CoherenceTimeMS, results[0].CoherenceTimeMS)
	}
	if results[0].Index <= 1 {
		t.Fatalf("clear homogeneous slant seeing collapsed a usable direction to the minimum: %+v", results[0])
	}
}

func TestHorizonOpticalTurbulencePenaltyUsesElevationScaledReferencesAndIsBounded(t *testing.T) {
	calibration := DefaultOverallIndexCalibration()
	plan := mustTestHorizonPlan(t, 2000)
	losPathM := 0.0
	for _, sample := range plan.Directions[0].Samples {
		losPathM += sample.LOSPathLengthM
	}
	referenceScale := horizonHomogeneousSeeingScale(plan.ObserverSurfaceElevationM, losPathM)
	if got := horizonSeeingQuality(calibration.GoodSeeingArcsec*referenceScale, referenceScale, calibration); math.Abs(got-1) > 1e-12 {
		t.Fatalf("good elevation-adjusted seeing quality = %v, want 1", got)
	}
	if got := horizonCoherenceQuality(calibration.BestCoherenceTimeMS/referenceScale, referenceScale, calibration); math.Abs(got-1) > 1e-12 {
		t.Fatalf("best elevation-adjusted coherence quality = %v, want 1", got)
	}
	if got := horizonCoherenceQuality(calibration.BadCoherenceTimeMS/referenceScale, referenceScale, calibration); math.Abs(got) > 1e-12 {
		t.Fatalf("bad elevation-adjusted coherence quality = %v, want 0", got)
	}
	zenithSeeing := 1.0
	zenithSeeingQuality := logarithmicLowerIsBetter(zenithSeeing, calibration.GoodSeeingArcsec, calibration.BadSeeingArcsec)
	if got := horizonSeeingQuality(zenithSeeing*referenceScale, referenceScale, calibration); math.Abs(got-zenithSeeingQuality) > 1e-12 {
		t.Fatalf("homogeneous slant seeing quality = %v, want zenith-equivalent %v", got, zenithSeeingQuality)
	}
	zenithTau := 3.0
	zenithTauQuality := logarithmicHigherIsBetter(zenithTau, calibration.BadCoherenceTimeMS, calibration.BestCoherenceTimeMS)
	if got := horizonCoherenceQuality(zenithTau/referenceScale, referenceScale, calibration); math.Abs(got-zenithTauQuality) > 1e-12 {
		t.Fatalf("homogeneous slant coherence quality = %v, want zenith-equivalent %v", got, zenithTauQuality)
	}
	worst := boundedOpticalTurbulenceFactor(0, 0, calibration)
	wantWorst := 1 - calibration.OpticalTurbulenceMaxPenalty
	if math.Abs(worst-wantWorst) > 1e-12 {
		t.Fatalf("worst horizon turbulence factor = %v, want bounded floor %v", worst, wantWorst)
	}
	// With the default cloud exponent, only 20% effective obstruction already
	// penalizes more than the worst possible turbulence term. Cloud can continue
	// to zero, while seeing/tau0 cannot make a clear direction unusable.
	cloudFactor := math.Pow(0.80, calibration.CloudWeight)
	if !(cloudFactor < worst) {
		t.Fatalf("20%% cloud obstruction factor %v is not stronger than worst turbulence %v", cloudFactor, worst)
	}
	moderateQuality := horizonSeeingQuality(calibration.GoodSeeingArcsec*referenceScale*2, referenceScale, calibration)
	moderate := boundedOpticalTurbulenceFactor(moderateQuality, 1, calibration)
	if !(moderate > worst && moderate < 1) {
		t.Fatalf("moderate horizon turbulence factor = %v, want between %v and 1", moderate, worst)
	}
}

func TestComputeHorizonCloudClosureIsStepSizeStable(t *testing.T) {
	validAt := time.Date(2026, 7, 22, 3, 0, 0, 0, time.UTC)
	coarsePlan := mustTestHorizonPlan(t, HorizonSurfaceSegmentLengthM*4)
	productionPlan := mustTestHorizonPlan(t, HorizonSurfaceSegmentLengthM)
	finePlan := mustTestHorizonPlan(t, HorizonSurfaceSegmentLengthM/2)
	coarse, err := ComputeHorizon(homogeneousHorizonSnapshot(coarsePlan, validAt, 1e-8, 60), coarsePlan, testHorizonCalibration())
	if err != nil {
		t.Fatal(err)
	}
	production, err := ComputeHorizon(homogeneousHorizonSnapshot(productionPlan, validAt, 1e-8, 60), productionPlan, testHorizonCalibration())
	if err != nil {
		t.Fatal(err)
	}
	fine, err := ComputeHorizon(homogeneousHorizonSnapshot(finePlan, validAt, 1e-8, 60), finePlan, testHorizonCalibration())
	if err != nil {
		t.Fatal(err)
	}
	coarseError := math.Abs(coarse[0].CloudOpticalDepth - fine[0].CloudOpticalDepth)
	productionError := math.Abs(production[0].CloudOpticalDepth - fine[0].CloudOpticalDepth)
	if productionError > coarseError || productionError/math.Max(fine[0].CloudOpticalDepth, 1e-12) > 0.001 ||
		math.Abs(production[0].CloudTransmission-fine[0].CloudTransmission) > 1e-12 {
		t.Fatalf("cloud closure did not converge with step: coarse tau/T=%v/%v production=%v/%v fine=%v/%v",
			coarse[0].CloudOpticalDepth, coarse[0].CloudTransmission,
			production[0].CloudOpticalDepth, production[0].CloudTransmission,
			fine[0].CloudOpticalDepth, fine[0].CloudTransmission)
	}
	calibration := testHorizonCalibration()
	low := unresolvedLayerCloudObstruction(0.60, 0, calibration.UnresolvedCloudObstruction)
	middle := unresolvedLayerCloudObstruction(0.60, 3000, calibration.UnresolvedCloudObstruction)
	high := unresolvedLayerCloudObstruction(0.60, 8000, calibration.UnresolvedCloudObstruction)
	wantGuardTransmission := (1 - low) * (1 - middle) * (1 - high)
	if math.Abs(production[0].CloudTransmission-wantGuardTransmission) > 1e-12 || !production[0].CloudUnresolvedGuard {
		t.Fatalf("cloud transmission = %v, want tier guard %v", production[0].CloudTransmission, wantGuardTransmission)
	}
}

func TestHorizonLocalLOSComponentsFollowSphericalRayAtRemoteSample(t *testing.T) {
	origin := Location{Latitude: 75, Longitude: 20, TimeZone: "UTC"}
	plan, err := newHorizonPlan(origin, 0, 5000, HorizonAtmosphereTopM)
	if err != nil {
		t.Fatal(err)
	}
	eastPlan := plan.Directions[2]
	sample := eastPlan.Samples[len(eastPlan.Samples)-1]
	east, north, up := horizonLocalLOSComponents(
		origin, sample.Midpoint, eastPlan.AzimuthDegrees, HorizonGeometricElevationDegrees,
	)
	startLOS, err := horizonLOSPathAtSurfaceDistance(0, sample.StartSurfaceDistanceM)
	if err != nil {
		t.Fatal(err)
	}
	endLOS, err := horizonLOSPathAtSurfaceDistance(0, sample.EndSurfaceDistanceM)
	if err != nil {
		t.Fatal(err)
	}
	centralAngle := horizonCentralAngleAtLOS(0, (startLOS+endLOS)/2)
	wantUp := math.Sin((HorizonGeometricElevationDegrees * math.Pi / 180) + centralAngle)
	if math.Abs(up-wantUp) > 2e-4 {
		t.Fatalf("remote local ray up component = %.9f, want %.9f", up, wantUp)
	}
	if math.Abs(east*east+north*north+up*up-1) > 1e-12 {
		t.Fatalf("remote ENU ray is not unit length: E/N/U=%.9f/%.9f/%.9f", east, north, up)
	}
	horizontalNorm := math.Hypot(east, north)
	windSpeed := 20.0
	u := windSpeed * east / horizontalNorm
	v := windSpeed * north / horizontalNorm
	gotPerpendicular := horizonPerpendicularWind(
		origin, sample.Midpoint, u, v, eastPlan.AzimuthDegrees, HorizonGeometricElevationDegrees,
	)
	wantPerpendicular := windSpeed * math.Abs(up)
	if math.Abs(gotPerpendicular-wantPerpendicular) > 1e-9 {
		t.Fatalf("perpendicular wind = %.9f, want %.9f from remote local elevation", gotPerpendicular, wantPerpendicular)
	}
}

func TestHorizonCloudBlockUsesGridBoxCoverClosure(t *testing.T) {
	calibration := testHorizonCalibration()
	block := horizonCloudBlock{liquidPathKgM2: 0.01, cover: 0.20}
	tau, got := horizonCloudBlockTransmission(block, calibration)
	want := 0.80 + 0.20*math.Exp(-tau/0.20)
	if math.Abs(got-want) > 1e-12 {
		t.Fatalf("block transmission = %v, want %v", got, want)
	}
	if !(got > math.Exp(-tau)) {
		t.Fatalf("broken-cloud closure %v did not preserve clear fraction over homogeneous %v", got, math.Exp(-tau))
	}
}

func TestComputeHorizonKeepsSparseNativeProfilesUsableAndPenalizesCoverage(t *testing.T) {
	validAt := time.Date(2026, 7, 22, 4, 0, 0, 0, time.UTC)
	plan, err := newHorizonPlan(Location{Latitude: 45, Longitude: 10, TimeZone: "UTC"}, 25, HorizonSurfaceSegmentLengthM, HorizonAtmosphereTopM)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := homogeneousHorizonSnapshot(plan, validAt, 0, 0)
	pressure := standardAtmosphereProfile(0.002) // 50 hPa top is below 22.3 km.
	sparseCloud := SyntheticCloudFixture().Frames[0].Levels
	for direction := range snapshot.Directions {
		for sample := range snapshot.Directions[direction].Samples {
			point := &snapshot.Directions[direction].Samples[sample]
			point.SurfaceElevationM = 25
			point.Surface.MixedLayerDepthM = 1000
			point.Vertical.Levels = append([]VerticalLevel(nil), pressure...)
			point.Cloud.Levels = append([]CloudLevel(nil), sparseCloud...)
			point.Cloud.ValidAt = validAt
		}
	}
	results, err := ComputeHorizon(snapshot, plan, testHorizonCalibration())
	if err != nil {
		t.Fatal(err)
	}
	for _, result := range results {
		if !result.Available || result.ResolvedPathFraction < horizonMinimumCompletePathRatio {
			t.Fatalf("sparse provider profile made %s unavailable: %+v", result.Direction, result)
		}
		if !(result.CloudProfileCoverage > 0 && result.CloudProfileCoverage < 1) {
			t.Fatalf("sparse cloud coverage was not disclosed for %s: %v", result.Direction, result.CloudProfileCoverage)
		}
		if !(result.TurbulenceProfileCoverage > 0 && result.TurbulenceProfileCoverage < 1) {
			t.Fatalf("bounded 50 hPa top extension was not disclosed for %s: %v", result.Direction, result.TurbulenceProfileCoverage)
		}
		if result.Confidence >= horizonMaximumConfidence {
			t.Fatalf("coarse sparse profile retained maximum confidence in %s: %v", result.Direction, result.Confidence)
		}
	}
}

func TestComputeHorizonUsesObserverLocalFogForEveryDirection(t *testing.T) {
	plan := mustTestHorizonPlan(t, HorizonSurfaceSegmentLengthM)
	validAt := time.Date(2026, 7, 22, 6, 0, 0, 0, time.UTC)
	snapshot := homogeneousHorizonSnapshot(plan, validAt, 0, 0)
	for direction := range snapshot.Directions {
		for sample := range snapshot.Directions[direction].Samples {
			frame := &snapshot.Directions[direction].Samples[sample].Surface
			frame.RelativeHumidityPercent = 99
			frame.DewPointC = frame.TemperatureC
			frame.VisibilityKM = 0.1
		}
	}
	clear, err := ComputeHorizon(snapshot, plan, testHorizonCalibration())
	if err != nil {
		t.Fatal(err)
	}
	for _, result := range clear {
		if result.FogRisk != 0 {
			t.Fatalf("remote sample fog leaked into %s: %+v", result.Direction, result)
		}
	}
	snapshot.ObserverSurface.RelativeHumidityPercent = 98
	snapshot.ObserverSurface.DewPointC = snapshot.ObserverSurface.TemperatureC - 0.5
	snapshot.ObserverSurface.VisibilityKM = 0.5
	foggy, err := ComputeHorizon(snapshot, plan, testHorizonCalibration())
	if err != nil {
		t.Fatal(err)
	}
	for index, result := range foggy {
		if result.FogRisk != 2 || !result.HighFog || !(result.Index < clear[index].Index) {
			t.Fatalf("observer fog not common/effective in %s: clear=%v fog=%+v", result.Direction, clear[index].Index, result)
		}
	}
}

func TestComputeHorizonUnavailableAndCoarseTerrainCannotLookGood(t *testing.T) {
	plan := mustTestHorizonPlan(t, HorizonSurfaceSegmentLengthM)
	validAt := time.Date(2026, 7, 22, 9, 0, 0, 0, time.UTC)
	snapshot := homogeneousHorizonSnapshot(plan, validAt, 0, 0)
	snapshot.Directions[0].Samples[0].Cloud.Levels = nil
	results, err := ComputeHorizon(snapshot, plan, testHorizonCalibration())
	if err != nil {
		t.Fatal(err)
	}
	if results[0].Available || results[0].Index != 1 || results[0].DataQuality != HorizonDataUnavailable ||
		results[0].LimitingFactor != HorizonFactorUnavailable || results[0].Confidence != 0 {
		t.Fatalf("incomplete direction became usable: %+v", results[0])
	}

	terrainSnapshot := homogeneousHorizonSnapshot(plan, validAt, 0, 0)
	blockedSample := len(plan.Directions[3].Samples) / 2
	terrainSnapshot.Directions[3].Samples[blockedSample].SurfaceElevationM = plan.Directions[3].Samples[blockedSample].RayHeightM
	blocked, err := ComputeHorizon(terrainSnapshot, plan, testHorizonCalibration())
	if err != nil {
		t.Fatal(err)
	}
	if !blocked[3].Available || !blocked[3].TerrainBlocked || blocked[3].Index != 1 || blocked[3].LimitingFactor != HorizonFactorTerrain {
		t.Fatalf("coarse terrain block was not an explicit veto: %+v", blocked[3])
	}
	if blocked[3].Confidence > horizonMaximumConfidence {
		t.Fatalf("coarse terrain confidence exceeded cap: %v", blocked[3].Confidence)
	}
}

func TestComputeHorizonReturnsEightOrderedReadableDirectionsAndFactors(t *testing.T) {
	plan := mustTestHorizonPlan(t, HorizonSurfaceSegmentLengthM)
	validAt := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	results, err := ComputeHorizon(homogeneousHorizonSnapshot(plan, validAt, 0, 0), plan, testHorizonCalibration())
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != len(fixedHorizonDirections) {
		t.Fatalf("direction result count = %d", len(results))
	}
	for index, result := range results {
		fixed := fixedHorizonDirections[index]
		if result.Direction != fixed.direction || result.AzimuthDegrees != fixed.azimuth || result.LimitingFactor == "" || len(result.LimitingFactors) == 0 {
			t.Fatalf("direction %d is not ordered/readable: %+v", index, result)
		}
		if result.Confidence > horizonMaximumConfidence || result.DataQuality == "" {
			t.Fatalf("direction %s has invalid quality: %+v", result.Direction, result)
		}
	}
}

func TestComputeHorizonSeriesRequiresAndPreservesHourly72HourWindow(t *testing.T) {
	plan := mustTestHorizonPlan(t, HorizonSurfaceSegmentLengthM)
	start := time.Date(2026, 7, 22, 14, 0, 0, 0, time.UTC)
	snapshots := make([]HorizonSnapshot, 73)
	for index := range snapshots {
		snapshots[index] = homogeneousHorizonSnapshot(plan, start.Add(time.Duration(index)*time.Hour), 0, 0)
	}
	frames, err := ComputeHorizonSeries(context.Background(), snapshots, plan, testHorizonCalibration())
	if err != nil {
		t.Fatal(err)
	}
	if len(frames) != 73 || !frames[0].ValidAt.Equal(start) || !frames[72].ValidAt.Equal(start.Add(72*time.Hour)) {
		t.Fatalf("unexpected horizon series bounds: %d %v..%v", len(frames), frames[0].ValidAt, frames[len(frames)-1].ValidAt)
	}
	for _, frame := range frames {
		if len(frame.Results) != HorizonDirectionCount {
			t.Fatalf("hourly frame has %d directions", len(frame.Results))
		}
	}
	snapshots[4].ValidAt = snapshots[4].ValidAt.Add(time.Minute)
	if _, err := ComputeHorizonSeries(context.Background(), snapshots, plan, testHorizonCalibration()); err == nil {
		t.Fatal("non-hourly horizon series was accepted")
	}
}

func TestComputeHorizonSeriesHonorsCancellation(t *testing.T) {
	plan := mustTestHorizonPlan(t, HorizonSurfaceSegmentLengthM)
	start := time.Date(2026, 7, 22, 14, 0, 0, 0, time.UTC)
	snapshots := []HorizonSnapshot{
		homogeneousHorizonSnapshot(plan, start, 0, 0),
		homogeneousHorizonSnapshot(plan, start.Add(time.Hour), 0, 0),
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ComputeHorizonSeries(ctx, snapshots, plan, testHorizonCalibration()); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled horizon calculation error = %v, want context.Canceled", err)
	}
}

func TestComputeHorizonAllowsRoundedObserverElevationButRejectsDifferentGeometry(t *testing.T) {
	plan := mustTestHorizonPlan(t, HorizonSurfaceSegmentLengthM)
	validAt := time.Date(2026, 7, 22, 13, 0, 0, 0, time.UTC)
	snapshot := homogeneousHorizonSnapshot(plan, validAt, 0, 0)
	snapshot.ObserverSurfaceElevationM += 0.9
	if _, err := ComputeHorizon(snapshot, plan, testHorizonCalibration()); err != nil {
		t.Fatalf("harmless callback elevation rounding was rejected: %v", err)
	}
	snapshot.ObserverSurfaceElevationM += 0.2
	if _, err := ComputeHorizon(snapshot, plan, testHorizonCalibration()); err == nil {
		t.Fatal("materially different observer geometry was accepted")
	}
}

func mustTestHorizonPlan(t *testing.T, stepM float64) HorizonPlan {
	t.Helper()
	plan, err := newHorizonPlan(Location{Latitude: 45, Longitude: 10, TimeZone: "UTC"}, 0, stepM, HorizonAtmosphereTopM)
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func testHorizonCalibration() OverallIndexCalibration {
	calibration := DefaultOverallIndexCalibration()
	calibration.BoundaryLayerMinM = 100
	return calibration
}

func homogeneousHorizonSnapshot(plan HorizonPlan, validAt time.Time, liquidKgKg, coverPercent float64) HorizonSnapshot {
	observerSurface := SurfaceFrame{
		ValidAt: validAt, TemperatureC: 10, DewPointC: 0, RelativeHumidityPercent: 50,
		VisibilityKM: 50, TransparencyAvailable: true,
		WindSpeedMS: 3, WindGustMS: 5,
	}
	snapshot := HorizonSnapshot{
		ValidAt: validAt, ObserverSurface: observerSurface,
		ObserverSurfaceElevationM: plan.ObserverSurfaceElevationM,
		Directions:                make([]HorizonDirectionSnapshot, len(plan.Directions)),
	}
	for directionIndex, direction := range plan.Directions {
		directionSnapshot := HorizonDirectionSnapshot{
			Direction: direction.Direction,
			Samples:   make([]HorizonSampleSnapshot, len(direction.Samples)),
		}
		for sampleIndex, geometry := range direction.Samples {
			height := geometry.RayHeightM
			cloudLevels := []CloudLevel{
				{ModelLevel: 3, PressureHPA: 999, HeightM: 10, LayerThicknessM: 20, TemperatureK: 279.9, UMS: 8, VMS: 3, TKEJkg: 0.1, CoverPercent: coverPercent, CloudLiquidKgKg: liquidKgKg},
				{ModelLevel: 2, PressureHPA: 995, HeightM: 50, LayerThicknessM: 60, TemperatureK: 279.7, UMS: 8, VMS: 3, TKEJkg: 0.1, CoverPercent: coverPercent, CloudLiquidKgKg: liquidKgKg},
				{ModelLevel: 1, PressureHPA: 990, HeightM: 100, LayerThicknessM: 100, TemperatureK: 279.4, UMS: 8, VMS: 3, TKEJkg: 0.1, CoverPercent: coverPercent, CloudLiquidKgKg: liquidKgKg},
			}
			if height > 150 {
				cloudLevels = append(cloudLevels, CloudLevel{
					ModelLevel: 0, PressureHPA: 650, HeightM: height, LayerThicknessM: 100,
					TemperatureK: 255, UMS: 8, VMS: 3, TKEJkg: 0.1,
					CoverPercent: coverPercent, CloudLiquidKgKg: liquidKgKg,
				})
			}
			directionSnapshot.Samples[sampleIndex] = HorizonSampleSnapshot{
				Vertical: VerticalFrame{ValidAt: validAt, Confidence: 0.9, Levels: []VerticalLevel{
					{PressureHPA: 700, HeightM: height - 1000, TemperatureK: 260, UMS: 8, VMS: 3},
					{PressureHPA: 600, HeightM: height + 1000, TemperatureK: 250, UMS: 8, VMS: 3},
				}},
				Surface:           SurfaceFrame{ValidAt: validAt, MixedLayerDepthM: 0},
				Cloud:             CloudFrame{ValidAt: validAt, Levels: cloudLevels},
				SurfaceElevationM: 0,
			}
		}
		snapshot.Directions[directionIndex] = directionSnapshot
	}
	return snapshot
}
