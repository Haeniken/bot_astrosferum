package forecast

import (
	"encoding/json"
	"math"
	"testing"
	"time"
)

func TestReferenceVBandAtmosphereUsesPWVAerosolSeeingAndMoonOnce(t *testing.T) {
	surface, overall, composition, sky := completeVBandAtmosphereFixture()
	baseline, err := ComputeReferenceVBandAtmosphere(surface, overall, &composition, sky)
	if err != nil {
		t.Fatal(err)
	}
	if !baseline.Result.Available || !baseline.Result.Applicable || baseline.Result.DataStatus != ReferenceVBandDataComplete {
		t.Fatalf("complete night result is unavailable: %+v", baseline.Result)
	}
	if baseline.AtmosphericTransmissionPercent <= 0 || baseline.AtmosphericTransmissionPercent >= 100 ||
		baseline.ExtinctionMag == nil || *baseline.ExtinctionMag <= 0 || baseline.SkyBrightnessMagArcsec2 <= 0 {
		t.Fatalf("invalid physical diagnostics: %+v", baseline)
	}

	wetSurface := surface
	wetSurface.PrecipitableWaterMM = 80
	wet, err := ComputeReferenceVBandAtmosphere(wetSurface, overall, &composition, sky)
	if err != nil {
		t.Fatal(err)
	}
	if !(wet.AtmosphericTransmissionPercent < baseline.AtmosphericTransmissionPercent) {
		t.Fatalf("PWV did not lower V-band transmission: wet=%g baseline=%g", wet.AtmosphericTransmissionPercent, baseline.AtmosphericTransmissionPercent)
	}

	hazyComposition := composition
	hazyComposition.AerosolOpticalDepth550 = 0.8
	hazy, err := ComputeReferenceVBandAtmosphere(surface, overall, &hazyComposition, sky)
	if err != nil {
		t.Fatal(err)
	}
	if !(hazy.Result.Index < baseline.Result.Index && hazy.AtmosphericTransmissionPercent < baseline.AtmosphericTransmissionPercent) {
		t.Fatalf("AOD did not lower efficiency: hazy=%+v baseline=%+v", hazy.Result, baseline.Result)
	}

	highOzoneComposition := composition
	highOzoneComposition.TotalColumnOzoneDU = 500
	highOzone, err := ComputeReferenceVBandAtmosphere(surface, overall, &highOzoneComposition, sky)
	if err != nil {
		t.Fatal(err)
	}
	if !(highOzone.Result.Index < baseline.Result.Index && highOzone.AtmosphericTransmissionPercent < baseline.AtmosphericTransmissionPercent) {
		t.Fatalf("ozone did not lower efficiency: high=%+v baseline=%+v", highOzone.Result, baseline.Result)
	}

	cloudyOverall := overall
	cloudyOverall.CloudTransmissionPercent = 50
	cloudy, err := ComputeReferenceVBandAtmosphere(surface, cloudyOverall, &composition, sky)
	if err != nil {
		t.Fatal(err)
	}
	if ratio := cloudy.Result.SourcePhotonRate / baseline.Result.SourcePhotonRate; math.Abs(ratio-0.5) > 1e-12 {
		t.Fatalf("cloud transmission entered source rate more or less than once: ratio=%g", ratio)
	}
	if !(cloudy.Result.RelativeEfficiency < baseline.Result.RelativeEfficiency) {
		t.Fatalf("cloud did not lower efficiency: cloudy=%+v baseline=%+v", cloudy.Result, baseline.Result)
	}
	// Cloud-scattered radiance is unavailable from the deterministic input, so
	// the declared conservative contract attenuates S once and retains the
	// clear-air background floor: G=S^2/(B*NEA) therefore scales as T_cloud^2.
	if ratio := cloudy.Result.RelativeEfficiency / baseline.Result.RelativeEfficiency; math.Abs(ratio-0.25) > 1e-12 {
		t.Fatalf("source-only cloud/background contract ratio = %g, want 0.25", ratio)
	}

	poorSeeing := overall
	poorSeeing.SeeingArcsec = 2.5
	blurred, err := ComputeReferenceVBandAtmosphere(surface, poorSeeing, &composition, sky)
	if err != nil {
		t.Fatal(err)
	}
	if !(blurred.Result.NoiseEquivalentPSFSolidAngleArcsec2 > baseline.Result.NoiseEquivalentPSFSolidAngleArcsec2 &&
		blurred.Result.Index < baseline.Result.Index) {
		t.Fatalf("seeing did not enlarge NEA/lower efficiency: blurred=%+v baseline=%+v", blurred.Result, baseline.Result)
	}

	moon := sky
	moon.MoonGeometryAvailable = true
	moon.MoonAltitudeDegrees = 70
	moon.MoonZenithDistanceDeg = 20
	moon.MoonPhaseAngleDegrees = 0
	moon.MoonEarthDistanceKM = 384400
	moonlit, err := ComputeReferenceVBandAtmosphere(surface, overall, &composition, moon)
	if err != nil {
		t.Fatal(err)
	}
	if !(moonlit.SkyBrightnessMagArcsec2 < baseline.SkyBrightnessMagArcsec2 && moonlit.Result.Index < baseline.Result.Index) {
		t.Fatalf("Moon did not brighten sky/lower efficiency: moon=%+v baseline=%+v", moonlit, baseline)
	}
}

func TestReferenceVBandOpaqueCloudHasFiniteJSONAndNoExtinctionMagnitude(t *testing.T) {
	surface, overall, composition, sky := completeVBandAtmosphereFixture()
	overall.CloudTransmissionPercent = 0
	result, err := ComputeReferenceVBandAtmosphere(surface, overall, &composition, sky)
	if err != nil {
		t.Fatal(err)
	}
	if !result.OpaqueTransmission || result.ExtinctionMag != nil || !result.Result.NoFiniteExposure || result.Result.Index != 1 {
		t.Fatalf("opaque-cloud contract is inconsistent: %+v", result)
	}
	if _, err := json.Marshal(result); err != nil {
		t.Fatalf("opaque-cloud diagnostic is not valid JSON: %v", err)
	}
}

func TestReferenceVBandOperationalVetoSuppressesDisplayedResult(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*OverallIndexFrame)
		want   string
	}{
		{name: "precipitation", mutate: func(frame *OverallIndexFrame) { frame.PrecipitationVeto = true }, want: "precipitation"},
		{name: "high fog", mutate: func(frame *OverallIndexFrame) { frame.HighFog = true }, want: "high_fog_risk"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			surface, overall, composition, sky := completeVBandAtmosphereFixture()
			test.mutate(&overall)
			result, err := ComputeReferenceVBandAtmosphere(surface, overall, &composition, sky)
			if err != nil {
				t.Fatal(err)
			}
			if !result.OperationallyUnavailable || result.OperationalReason != test.want || result.Result.Available {
				t.Fatalf("operational obstruction was not explicit: %+v", result)
			}
		})
	}
}

func TestReferenceVBandMissingSeeingRemainsPartialWithComposition(t *testing.T) {
	surface, overall, composition, sky := completeVBandAtmosphereFixture()
	overall.SeeingArcsec = math.NaN()
	result, err := ComputeReferenceVBandAtmosphere(surface, overall, &composition, sky)
	if err != nil {
		t.Fatal(err)
	}
	if result.Result.Available || result.Result.DataStatus != ReferenceVBandDataPartial ||
		!result.Result.MissingComponents.Has(ReferenceVBandNoiseEquivalentPSFSolidAngle) ||
		!result.Result.AvailableComponents.Has(ReferenceVBandSourcePhotonRate|ReferenceVBandSkyPhotonRadiance) {
		t.Fatalf("missing seeing was not represented as a partial component: %+v", result.Result)
	}
}

func TestReferenceVBandRejectsInconsistentMoonGeometry(t *testing.T) {
	surface, overall, composition, sky := completeVBandAtmosphereFixture()
	sky.MoonAltitudeDegrees = 30
	sky.MoonZenithDistanceDeg = 140
	if _, err := ComputeReferenceVBandAtmosphere(surface, overall, &composition, sky); err == nil {
		t.Fatal("inconsistent Moon altitude/zenith distance was accepted")
	}
}

func TestReferenceVBandAtmosphereBenchmarkMapsExactlyToTen(t *testing.T) {
	surface, overall, composition, sky := completeVBandAtmosphereFixture()
	overall.SurfacePressureHPA = 1013.25
	surface.PrecipitableWaterMM = 5
	overall.SeeingArcsec = ReferenceVBandBenchmarkSeeing
	overall.CloudTransmissionPercent = 100
	composition.AerosolOpticalDepth550 = 0.05
	composition.TotalColumnOzoneDU = 300
	sky.MoonAltitudeDegrees = -5
	sky.MoonZenithDistanceDeg = 95

	result, err := ComputeReferenceVBandAtmosphere(surface, overall, &composition, sky)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Result.Available || math.Abs(result.Result.RelativeEfficiency-1) > 1e-12 || result.Result.Index != 10 || result.Result.ExposureTimeMultiplier != 1 {
		t.Fatalf("declared benchmark does not map to index 10: %+v", result.Result)
	}
}

func TestReferenceVBandAtmosphereDoesNotUseSeaLevelPressure(t *testing.T) {
	surface, overall, composition, sky := completeVBandAtmosphereFixture()
	// PressureHPA is the weather-table PMSL field. Changing it must not alter
	// the optical column, which uses the pressure derived from native model P.
	baseline, err := ComputeReferenceVBandAtmosphere(surface, overall, &composition, sky)
	if err != nil {
		t.Fatal(err)
	}
	surface.PressureHPA = 800
	changedPMSL, err := ComputeReferenceVBandAtmosphere(surface, overall, &composition, sky)
	if err != nil {
		t.Fatal(err)
	}
	if baseline.AtmosphericTransmissionPercent != changedPMSL.AtmosphericTransmissionPercent {
		t.Fatalf("PMSL changed Reference V transmission: baseline=%g changed=%g", baseline.AtmosphericTransmissionPercent, changedPMSL.AtmosphericTransmissionPercent)
	}

	overall.SurfacePressureHPA = 800
	changedSurface, err := ComputeReferenceVBandAtmosphere(surface, overall, &composition, sky)
	if err != nil {
		t.Fatal(err)
	}
	if changedSurface.AtmosphericTransmissionPercent == baseline.AtmosphericTransmissionPercent {
		t.Fatal("actual surface pressure did not change Reference V transmission")
	}
}

func TestReferenceVBandMissingSurfacePressureIsPartial(t *testing.T) {
	surface, overall, composition, sky := completeVBandAtmosphereFixture()
	overall.SurfacePressureHPA = 0
	overall.SurfacePressureProvenance = ""
	result, err := ComputeReferenceVBandAtmosphere(surface, overall, &composition, sky)
	if err != nil {
		t.Fatal(err)
	}
	if result.Result.Available || result.Result.DataStatus != ReferenceVBandDataPartial ||
		!result.Result.MissingComponents.Has(ReferenceVBandSourcePhotonRate|ReferenceVBandSkyPhotonRadiance) {
		t.Fatalf("missing surface pressure was not represented as partial: %+v", result.Result)
	}
}

func TestReferenceVBandAtmosphereMissingMoonGeometryDoesNotAssumeDarkSky(t *testing.T) {
	surface, overall, composition, sky := completeVBandAtmosphereFixture()
	sky.MoonGeometryAvailable = false
	result, err := ComputeReferenceVBandAtmosphere(surface, overall, &composition, sky)
	if err != nil {
		t.Fatal(err)
	}
	if result.Result.Available || result.Result.DataStatus != ReferenceVBandDataPartial ||
		!result.Result.MissingComponents.Has(ReferenceVBandSkyPhotonRadiance) {
		t.Fatalf("missing lunar geometry was silently treated as a dark sky: %+v", result.Result)
	}
}

func TestReferenceVBandAtmosphereMissingCompositionIsExplicitlyPartial(t *testing.T) {
	surface, overall, _, sky := completeVBandAtmosphereFixture()
	result, err := ComputeReferenceVBandAtmosphere(surface, overall, nil, sky)
	if err != nil {
		t.Fatal(err)
	}
	if result.Result.DataStatus != ReferenceVBandDataPartial || result.Result.Available ||
		!result.Result.MissingComponents.Has(ReferenceVBandSourcePhotonRate|ReferenceVBandSkyPhotonRadiance) {
		t.Fatalf("missing composition was not exposed: %+v", result.Result)
	}
}

func TestReferenceVBandAtmosphereDayIsNotApplicableButRetainsDiagnostics(t *testing.T) {
	surface, overall, composition, sky := completeVBandAtmosphereFixture()
	sky.SunAltitudeDegrees = 15
	result, err := ComputeReferenceVBandAtmosphere(surface, overall, &composition, sky)
	if err != nil {
		t.Fatal(err)
	}
	if result.Result.Applicable || result.Result.Available || result.Result.EfficiencyAvailable ||
		result.AtmosphericTransmissionPercent <= 0 {
		t.Fatalf("daytime applicability is wrong: %+v", result.Result)
	}
}

func TestReferenceVBandGaussianMixtureNEAScalesWithSeeingSquared(t *testing.T) {
	one, err := vBandGaussianMixtureNEA(1, nil)
	if err != nil {
		t.Fatal(err)
	}
	two, err := vBandGaussianMixtureNEA(2, nil)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(two/one-4) > 1e-12 {
		t.Fatalf("NEA ratio = %.16f, want 4", two/one)
	}
}

func TestReferenceVBandSPECTRL2WaterTermIsSmallButNonzeroInJohnsonV(t *testing.T) {
	dry := vBandSpectralGrid(1013.25, 0, 300, 0.1)
	wet := vBandSpectralGrid(1013.25, 80, 300, 0.1)
	dryRate := vBandABZeroPhotonRate(dry)
	wetRate := vBandABZeroPhotonRate(wet)
	if !(wetRate < dryRate && wetRate/dryRate > 0.95) {
		t.Fatalf("unexpected Johnson-V PWV response: dry=%g wet=%g ratio=%g", dryRate, wetRate, wetRate/dryRate)
	}
}

func completeVBandAtmosphereFixture() (SurfaceFrame, OverallIndexFrame, AtmosphericCompositionFrame, ReferenceVBandSkyGeometry) {
	at := time.Date(2026, 7, 26, 22, 0, 0, 0, time.UTC)
	surface := SurfaceFrame{
		ValidAt: at, PressureHPA: 1005, PrecipitableWaterMM: 18,
	}
	overall := OverallIndexFrame{
		ValidAt: at, SeeingArcsec: 1.2, CloudTransmissionPercent: 100,
		SurfacePressureHPA: 1005, SurfacePressureProvenance: ModelSurfacePressureProvenance,
	}
	composition := AtmosphericCompositionFrame{
		ValidAt: at, AerosolOpticalDepth550: 0.12, TotalColumnOzoneDU: 320,
		Provider: "NASA GEOS-CF v2", RunID: "geos-cf-test", BaseTime: at.Add(-13 * time.Hour), Grid: "0.25 degree",
	}
	sky := ReferenceVBandSkyGeometry{
		ValidAt: at, SunAltitudeDegrees: -25,
		MoonAltitudeDegrees: -10, MoonZenithDistanceDeg: 100,
		MoonPhaseAngleDegrees: 90, MoonEarthDistanceKM: 384400, MoonGeometryAvailable: true,
	}
	return surface, overall, composition, sky
}
