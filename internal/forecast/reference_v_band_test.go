package forecast

import (
	"math"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestReferenceVBandZenithEfficiencyCompleteNight(t *testing.T) {
	input := completeReferenceVBandInput()
	result, err := ComputeReferenceVBandZenithEfficiency(input)
	if err != nil {
		t.Fatal(err)
	}
	if result.ContractVersion != ReferenceVBandContractVersion ||
		result.PassbandID != ReferenceVBandPassbandID ||
		result.SourceSpectrum != ReferenceVBandSourceSpectrum ||
		result.Direction != ReferenceVBandDirection || result.Regime != ReferenceVBandRegime {
		t.Fatalf("reference contract metadata changed: %+v", result)
	}
	if !result.ApplicabilityKnown || !result.Applicable || !result.Available || !result.EfficiencyAvailable {
		t.Fatalf("complete astronomical-night input is unavailable: %+v", result)
	}
	assertReferenceVBandNear(t, "Sun altitude", result.SunAltitudeDegrees, -25)
	if result.DataStatus != ReferenceVBandDataComplete || result.Completeness != 1 || result.MissingComponents != 0 {
		t.Fatalf("complete input has incomplete data status: %+v", result)
	}
	assertReferenceVBandNear(t, "efficiency", result.Efficiency, 200)
	assertReferenceVBandNear(t, "relative efficiency", result.RelativeEfficiency, 0.5)
	assertReferenceVBandNear(t, "exposure multiplier", result.ExposureTimeMultiplier, 2)
	assertReferenceVBandNear(t, "index", result.Index, 5.5)
}

func TestReferenceVBandMissingBackgroundIsPartialAndNeverNeutral(t *testing.T) {
	input := completeReferenceVBandInput()
	input.AvailableComponents &^= ReferenceVBandSkyPhotonRadiance
	input.Provenance.SkyRadiance = ""
	result, err := ComputeReferenceVBandZenithEfficiency(input)
	if err != nil {
		t.Fatal(err)
	}
	if result.DataStatus != ReferenceVBandDataPartial || result.Completeness != 0.8 {
		t.Fatalf("missing background status = %+v", result)
	}
	if !result.MissingComponents.Has(ReferenceVBandSkyPhotonRadiance) || result.EfficiencyAvailable || result.Available {
		t.Fatalf("missing background became an available neutral input: %+v", result)
	}
	if result.Efficiency != 0 || result.RelativeEfficiency != 0 || result.ExposureTimeMultiplier != 0 || result.Index != 0 {
		t.Fatalf("missing background produced derived values: %+v", result)
	}
}

func TestReferenceVBandMissingTransmissionIsPartialAndNeverNeutral(t *testing.T) {
	input := completeReferenceVBandInput()
	input.AvailableComponents &^= ReferenceVBandSourcePhotonRate
	input.Provenance.AtmosphericTransmission = ""
	result, err := ComputeReferenceVBandZenithEfficiency(input)
	if err != nil {
		t.Fatal(err)
	}
	if !result.MissingComponents.Has(ReferenceVBandSourcePhotonRate) || result.EfficiencyAvailable || result.Available || result.Index != 0 {
		t.Fatalf("missing radiative transfer produced a score: %+v", result)
	}
}

func TestReferenceVBandMissingBenchmarkPreservesPhysicalEfficiencyWithoutScore(t *testing.T) {
	input := completeReferenceVBandInput()
	input.AvailableComponents &^= ReferenceVBandBenchmarkEfficiency
	input.Provenance.Benchmark = ""
	result, err := ComputeReferenceVBandZenithEfficiency(input)
	if err != nil {
		t.Fatal(err)
	}
	if !result.EfficiencyAvailable || result.Available || result.DataStatus != ReferenceVBandDataPartial {
		t.Fatalf("benchmark-only gap was not represented as partial: %+v", result)
	}
	assertReferenceVBandNear(t, "physical efficiency", result.Efficiency, 200)
	if result.RelativeEfficiency != 0 || result.ExposureTimeMultiplier != 0 || result.Index != 0 {
		t.Fatalf("missing benchmark produced normalized output: %+v", result)
	}
}

func TestReferenceVBandRequiresStrictAstronomicalNight(t *testing.T) {
	for _, sunAltitude := range []float64{-18, -12, 0} {
		input := completeReferenceVBandInput()
		input.SunAltitudeDegrees = sunAltitude
		result, err := ComputeReferenceVBandZenithEfficiency(input)
		if err != nil {
			t.Fatal(err)
		}
		if !result.ApplicabilityKnown || result.Applicable || result.Available || result.EfficiencyAvailable || result.Index != 0 {
			t.Fatalf("Sun altitude %v unexpectedly applicable: %+v", sunAltitude, result)
		}
		if result.DataStatus != ReferenceVBandDataComplete {
			t.Fatalf("daylight changed input completeness: %+v", result)
		}
	}

	input := completeReferenceVBandInput()
	input.SunAltitudeDegrees = math.Nextafter(-18, math.Inf(-1))
	result, err := ComputeReferenceVBandZenithEfficiency(input)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Applicable || !result.Available {
		t.Fatalf("Sun below -18 degrees is not applicable: %+v", result)
	}
}

func TestReferenceVBandOpaquePathHasNoFiniteExposureAndMinimumIndex(t *testing.T) {
	input := completeReferenceVBandInput()
	input.SourcePhotonRate = 0
	result, err := ComputeReferenceVBandZenithEfficiency(input)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Available || !result.EfficiencyAvailable || !result.NoFiniteExposure {
		t.Fatalf("opaque complete path was treated as missing: %+v", result)
	}
	if result.Efficiency != 0 || result.RelativeEfficiency != 0 || result.ExposureTimeMultiplier != 0 || result.Index != 1 {
		t.Fatalf("opaque path output = %+v", result)
	}
}

func TestReferenceVBandIndexSaturatesButPhysicalSpeedDoesNot(t *testing.T) {
	input := completeReferenceVBandInput()
	input.BenchmarkEfficiency = 100
	result, err := ComputeReferenceVBandZenithEfficiency(input)
	if err != nil {
		t.Fatal(err)
	}
	assertReferenceVBandNear(t, "relative efficiency", result.RelativeEfficiency, 2)
	assertReferenceVBandNear(t, "exposure multiplier", result.ExposureTimeMultiplier, 0.5)
	if result.Index != 10 {
		t.Fatalf("index = %v, want saturated 10", result.Index)
	}
}

func TestReferenceVBandMissingSolarGeometryKeepsApplicabilityUnknown(t *testing.T) {
	input := completeReferenceVBandInput()
	input.AvailableComponents &^= ReferenceVBandSolarGeometry
	input.Provenance.SolarGeometry = ""
	result, err := ComputeReferenceVBandZenithEfficiency(input)
	if err != nil {
		t.Fatal(err)
	}
	if result.ApplicabilityKnown || result.Applicable || result.Available || result.EfficiencyAvailable {
		t.Fatalf("missing solar geometry was treated as night: %+v", result)
	}
	if !result.MissingComponents.Has(ReferenceVBandSolarGeometry) || result.DataStatus != ReferenceVBandDataPartial {
		t.Fatalf("missing solar geometry mask/status = %+v", result)
	}
}

func TestReferenceVBandRejectsInvalidAvailableInputs(t *testing.T) {
	tests := []struct {
		name      string
		change    func(*ReferenceVBandInput)
		wantError string
	}{
		{
			name: "unknown component", wantError: "unknown component",
			change: func(input *ReferenceVBandInput) { input.AvailableComponents |= 1 << 31 },
		},
		{
			name: "solar altitude", wantError: "solar altitude",
			change: func(input *ReferenceVBandInput) { input.SunAltitudeDegrees = math.NaN() },
		},
		{
			name: "source rate", wantError: "source photon rate",
			change: func(input *ReferenceVBandInput) { input.SourcePhotonRate = -1 },
		},
		{
			name: "sky radiance", wantError: "sky photon radiance",
			change: func(input *ReferenceVBandInput) { input.SkyPhotonRadiance = 0 },
		},
		{
			name: "PSF solid angle", wantError: "PSF solid angle",
			change: func(input *ReferenceVBandInput) { input.NoiseEquivalentPSFSolidAngleArcsec2 = math.Inf(1) },
		},
		{
			name: "benchmark", wantError: "benchmark efficiency",
			change: func(input *ReferenceVBandInput) { input.BenchmarkEfficiency = 0 },
		},
		{
			name: "provenance", wantError: "sky-radiance provenance",
			change: func(input *ReferenceVBandInput) { input.Provenance.SkyRadiance = "" },
		},
		{
			name: "passband provenance", wantError: "passband-response provenance",
			change: func(input *ReferenceVBandInput) { input.Provenance.PassbandResponse = "" },
		},
		{
			name: "validity time", wantError: "validity time",
			change: func(input *ReferenceVBandInput) { input.ValidAt = time.Time{} },
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := completeReferenceVBandInput()
			test.change(&input)
			_, err := ComputeReferenceVBandZenithEfficiency(input)
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("error = %v, want substring %q", err, test.wantError)
			}
		})
	}
}

func TestReferenceVBandComponentNamesAreStable(t *testing.T) {
	got := ReferenceVBandRequiredComponents.Names()
	want := []string{
		"solar_geometry",
		"source_photon_rate",
		"sky_photon_radiance",
		"noise_equivalent_psf_solid_angle",
		"benchmark_efficiency",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("component names = %v, want %v", got, want)
	}
}

func completeReferenceVBandInput() ReferenceVBandInput {
	return ReferenceVBandInput{
		ValidAt:                             time.Date(2026, 7, 26, 22, 0, 0, 0, time.UTC),
		SunAltitudeDegrees:                  -25,
		SourcePhotonRate:                    100,
		SkyPhotonRadiance:                   25,
		NoiseEquivalentPSFSolidAngleArcsec2: 2,
		BenchmarkEfficiency:                 400,
		AvailableComponents:                 ReferenceVBandRequiredComponents,
		Provenance: ReferenceVBandProvenance{
			SolarGeometry:           "iau-sofa-test-v1",
			AtmosphericTransmission: "lblrtm-test-v1",
			SkyRadiance:             "sky-test-v1",
			AtmosphericPSF:          "cn2-test-v1",
			Benchmark:               "reference-dark-sky-test-v1",
			PassbandResponse:        "SVO:Generic/Johnson.V@2021-07-27#sha256:test",
		},
	}
}

func assertReferenceVBandNear(t *testing.T, name string, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-12 {
		t.Fatalf("%s = %.15g, want %.15g", name, got, want)
	}
}
