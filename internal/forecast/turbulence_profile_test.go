package forecast

import (
	"math"
	"testing"
)

func TestOpticalTurbulenceProfileDerivesPhysicalMomentsOnce(t *testing.T) {
	const (
		surfaceM = 100.0
		depthM   = 1000.0
		cn2      = 2e-16
		windMS   = 10.0
	)
	windCn2 := cn2 * math.Pow(windMS, 5.0/3.0)
	profile := opticalTurbulenceProfile{
		layers: []opticalTurbulenceLayer{{
			bottomM: surfaceM, topM: surfaceM + depthM,
			bottomCn2: cn2, topCn2: cn2,
			bottomWindCn2: windCn2, topWindCn2: windCn2,
		}},
		surfaceElevationM:  surfaceM,
		boundaryLayerTopM:  surfaceM + 500,
		expectedTopM:       surfaceM + depthM,
		validLayerFraction: 1,
	}

	metrics := opticalTurbulenceMetricsFromProfile(profile)
	wantJ := cn2 * depthM
	wantJV := wantJ * math.Pow(windMS, 5.0/3.0)
	wantJH := cn2 * (3.0 / 8.0) * math.Pow(depthM, 8.0/3.0)
	wavenumber := 2 * math.Pi / seeingWavelengthM
	wantThetaArcsec := math.Pow(
		isoplanaticPhaseStructureCoefficient*wavenumber*wavenumber*wantJH,
		-3.0/5.0,
	) * radiansToArcsec
	wantEffectiveHeightM := math.Pow(wantJH/wantJ, 3.0/5.0)
	wantFreeSeeingArcsec := seeingArcsecFromIntegratedCn2(cn2 * 500)

	assertRelativeClose(t, "J [m^(1/3)]", metrics.IntegratedCn2, wantJ, 1e-12)
	assertRelativeClose(t, "JV [m^2 s^(-5/3)]", metrics.WindWeightedCn2, wantJV, 1e-12)
	assertRelativeClose(t, "JH [m^2]", metrics.HeightWeightedCn2, wantJH, 1e-12)
	assertRelativeClose(t, "effective height [m]", metrics.EffectiveTurbulenceHeightM, wantEffectiveHeightM, 1e-12)
	assertRelativeClose(t, "effective wind [m/s]", metrics.EffectiveWindSpeedMS, windMS, 1e-12)
	assertRelativeClose(t, "theta0 [arcsec]", metrics.IsoplanaticAngleArcsec, wantThetaArcsec, 1e-12)
	assertRelativeClose(t, "free-atmosphere seeing [arcsec]", metrics.FreeAtmosphereSeeing500MArcsec, wantFreeSeeingArcsec, 1e-12)

	assertRelativeClose(t, "dynamic boundary fraction", metrics.BoundaryLayerFraction, 0.5, 1e-12)
	assertRelativeClose(t, "FracGL250", metrics.FracGL250, 0.25, 1e-12)
	assertRelativeClose(t, "FracGL500", metrics.FracGL500, 0.5, 1e-12)
	assertRelativeClose(t, "FracGL1000", metrics.FracGL1000, 1, 1e-12)
	fracGLFromSeeings := 1 - math.Pow(metrics.FreeAtmosphereSeeing500MArcsec/metrics.SeeingArcsec, 5.0/3.0)
	assertRelativeClose(t, "FracGL500 from total/free seeing", metrics.FracGL500, fracGLFromSeeings, 1e-12)
	if metrics.GroundLayerCn2 != metrics.BoundaryLayerCn2 || metrics.GroundLayerFraction != metrics.BoundaryLayerFraction {
		t.Fatalf("legacy dynamic-boundary aliases diverged: %+v", metrics)
	}
	if metrics.ProfileQuality != OpticalTurbulenceProfileLimited || metrics.ProfileVerticalCoverage != 1 || metrics.ProfileHeightMomentCoverage != 1 {
		t.Fatalf("shallow but structurally complete profile classified incorrectly: %+v", metrics)
	}
	if metrics.ProfileTopAGLM != depthM {
		t.Fatalf("profile top = %v m AGL, want %v", metrics.ProfileTopAGLM, depthM)
	}

	// These numerical values guard the published 2.914 theta0 convention and
	// the dimensional SI calculation at 500 nm without relying only on a second
	// copy of the implementation formula.
	assertRelativeClose(t, "theta0 regression", metrics.IsoplanaticAngleArcsec, 24.64011950621408, 1e-12)
	assertRelativeClose(t, "effective-height regression", metrics.EffectiveTurbulenceHeightM, 555.1607587307295, 1e-12)
	expandedThetaCoefficient := math.Pow(isoplanaticPhaseStructureCoefficient*4*math.Pi*math.Pi, -3.0/5.0)
	assertRelativeClose(t, "expanded 2.914 theta0 coefficient", expandedThetaCoefficient, 0.05800833891608427, 1e-14)
}

func TestOpticalTurbulenceProfileQualityRequiresLowerStratosphere(t *testing.T) {
	const cn2 = 1e-17
	profile := opticalTurbulenceProfile{
		layers: []opticalTurbulenceLayer{{
			bottomM: 0, topM: minimumCompleteTurbulenceProfileTopAGLM,
			bottomCn2: cn2, topCn2: cn2,
		}},
		surfaceElevationM:  0,
		boundaryLayerTopM:  500,
		expectedTopM:       minimumCompleteTurbulenceProfileTopAGLM,
		validLayerFraction: 1,
	}
	metrics := opticalTurbulenceMetricsFromProfile(profile)
	if metrics.ProfileQuality != OpticalTurbulenceProfileComplete {
		t.Fatalf("full lower-stratosphere profile quality = %q, want complete", metrics.ProfileQuality)
	}
}

func TestOpticalTurbulenceProfileQualityExposesUpperAtmosphereGap(t *testing.T) {
	const cn2 = 1e-16
	profile := opticalTurbulenceProfile{
		layers: []opticalTurbulenceLayer{
			{bottomM: 0, topM: 500, bottomCn2: cn2, topCn2: cn2},
			{bottomM: 1000, topM: minimumCompleteTurbulenceProfileTopAGLM, bottomCn2: cn2, topCn2: cn2},
		},
		surfaceElevationM:  0,
		boundaryLayerTopM:  500,
		expectedTopM:       minimumCompleteTurbulenceProfileTopAGLM,
		validLayerFraction: 2.0 / 3.0,
	}
	metrics := opticalTurbulenceMetricsFromProfile(profile)
	if metrics.ProfileQuality != OpticalTurbulenceProfileLimited {
		t.Fatalf("gapped profile quality = %q, want limited", metrics.ProfileQuality)
	}
	assertRelativeClose(t, "vertical coverage", metrics.ProfileVerticalCoverage, 17500.0/18000.0, 1e-12)
	if !(metrics.ProfileHeightMomentCoverage > metrics.ProfileVerticalCoverage && metrics.ProfileHeightMomentCoverage < 1) {
		t.Fatalf("height-moment coverage = %v, want (vertical coverage, 1)", metrics.ProfileHeightMomentCoverage)
	}
	if !finite(metrics.IsoplanaticAngleArcsec) || metrics.IsoplanaticAngleArcsec <= 0 {
		t.Fatalf("limited profile lost diagnostic theta0: %+v", metrics)
	}
}

func TestLinearHeightMomentIntegrationMatchesAnalyticIntegral(t *testing.T) {
	const (
		bottomM     = 200.0
		topM        = 1700.0
		bottomValue = 1.2e-16
		topValue    = 4.8e-16
	)
	slope := (topValue - bottomValue) / (topM - bottomM)
	intercept := bottomValue - slope*bottomM
	want := slope*(3.0/11.0)*(math.Pow(topM, 11.0/3.0)-math.Pow(bottomM, 11.0/3.0)) +
		intercept*(3.0/8.0)*(math.Pow(topM, 8.0/3.0)-math.Pow(bottomM, 8.0/3.0))
	got := integrateLinearTimesHeightPower(bottomM, topM, bottomValue, topValue, 5.0/3.0)
	assertRelativeClose(t, "linear h^(5/3) integral", got, want, 1e-13)
}

func assertRelativeClose(t *testing.T, name string, got, want, relativeTolerance float64) {
	t.Helper()
	if !finite(got) || !finite(want) {
		t.Fatalf("%s: got %v, want %v", name, got, want)
	}
	scale := math.Max(math.Abs(want), math.SmallestNonzeroFloat64)
	if math.Abs(got-want)/scale > relativeTolerance {
		t.Fatalf("%s: got %.17g, want %.17g (relative error %.3g)", name, got, want, math.Abs(got-want)/scale)
	}
}
