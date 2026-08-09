package forecast

import (
	"math"
	"math/big"
	"testing"
)

func TestWMOThermalLapseResidualMatchesThresholdAtAndAroundEquality(t *testing.T) {
	t.Parallel()

	lowerHeight, upperHeight := 6000.0, 6500.0
	lowerTemperature := 270.0
	equalTemperature := 269.0
	if residual := WMOThermalLapseResidual(lowerHeight, upperHeight, lowerTemperature, equalTemperature); residual != 0 {
		t.Fatalf("WMO equality residual = %.17g, want zero", residual)
	}
	above := math.Nextafter(equalTemperature, math.Inf(1))
	below := math.Nextafter(equalTemperature, math.Inf(-1))
	if residual := WMOThermalLapseResidual(lowerHeight, upperHeight, lowerTemperature, above); residual <= 0 {
		t.Fatalf("one-ULP stable side residual = %.17g, want positive", residual)
	}
	if residual := WMOThermalLapseResidual(lowerHeight, upperHeight, lowerTemperature, below); residual >= 0 {
		t.Fatalf("one-ULP unstable side residual = %.17g, want negative", residual)
	}
}

func TestWMOThermalLapseResidualSignMatchesExactBinary64Arithmetic(t *testing.T) {
	t.Parallel()

	state := uint64(0x6a09e667f3bcc909)
	next := func() float64 {
		state = state*6364136223846793005 + 1442695040888963407
		return float64(state>>11) / (1 << 53)
	}
	toRat := func(value float64) *big.Rat {
		result := new(big.Rat).SetFloat64(value)
		if result == nil {
			t.Fatalf("cannot represent finite binary64 %.17g as a rational", value)
		}
		return result
	}
	sign := func(value float64) int {
		switch {
		case value < 0:
			return -1
		case value > 0:
			return 1
		default:
			return 0
		}
	}
	for sample := 0; sample < 20_000; sample++ {
		lowerHeight := 5000 + 15_000*next()
		deltaHeight := 10 + 2500*next()
		upperHeight := lowerHeight + deltaHeight
		lowerTemperature := 190 + 100*next()
		threshold := lowerTemperature - deltaHeight/500
		upperTemperature := threshold
		steps := int(state%17) - 8
		for steps > 0 {
			upperTemperature = math.Nextafter(upperTemperature, math.Inf(1))
			steps--
		}
		for steps < 0 {
			upperTemperature = math.Nextafter(upperTemperature, math.Inf(-1))
			steps++
		}

		exactHeight := new(big.Rat).Sub(toRat(upperHeight), toRat(lowerHeight))
		exactTemperature := new(big.Rat).Sub(toRat(upperTemperature), toRat(lowerTemperature))
		exactTemperature.Mul(exactTemperature, big.NewRat(500, 1))
		exact := new(big.Rat).Add(exactHeight, exactTemperature)
		got := WMOThermalLapseResidual(lowerHeight, upperHeight, lowerTemperature, upperTemperature)
		if sign(got) != exact.Sign() {
			t.Fatalf("sample %d WMO sign = %d (%g), exact sign = %d", sample, sign(got), got, exact.Sign())
		}
	}
}

func TestHMNSP99SeeingProducesPlausibleStandardProfile(t *testing.T) {
	levels := standardAtmosphereProfile(0.002)
	seeing := HMNSP99SeeingArcsec(levels)
	if !finite(seeing) || seeing < 0.3 || seeing > 3.0 {
		t.Fatalf("standard-profile seeing = %v arcsec, want a plausible model range", seeing)
	}
	metrics := HMNSP99Metrics(levels)
	assertRelativeClose(t, "HMNSP99 J regression", metrics.IntegratedCn2, 5.067859304649387e-13, 1e-12)
	assertRelativeClose(t, "HMNSP99 JV regression", metrics.WindWeightedCn2, 7.641543670218297e-11, 1e-12)
	assertRelativeClose(t, "HMNSP99 seeing regression", metrics.SeeingArcsec, 0.8363190705161425, 1e-12)
	assertRelativeClose(t, "HMNSP99 tau0 regression", metrics.CoherenceTimeMS, 1.8737157619744427, 1e-12)
}

func TestSeeingUsesAlgebraicallyConsistentFriedCoefficient(t *testing.T) {
	integratedCn2 := 1e-13
	metrics := opticalTurbulenceMetricsFromMoments(integratedCn2, 0, 1)
	wavenumber := 2 * math.Pi / seeingWavelengthM
	r0 := math.Pow(friedR0Coefficient*wavenumber*wavenumber*integratedCn2, -3.0/5.0)
	want := friedSeeingCoefficient * seeingWavelengthM / r0 * radiansToArcsec
	if math.Abs(metrics.SeeingArcsec-want) > 1e-12 {
		t.Fatalf("seeing = %v arcsec, want Fried-consistent %v", metrics.SeeingArcsec, want)
	}
	derived := friedSeeingCoefficient * math.Pow(friedR0Coefficient*4*math.Pi*math.Pi, 3.0/5.0)
	if math.Abs(derived-5.306963958) > 1e-9 {
		t.Fatalf("expanded Fried coefficient = %.12f, want 5.306963958", derived)
	}
}

func TestCoherenceTimeUsesUnexpandedPhaseStructureDefinition(t *testing.T) {
	integratedCn2 := 1e-13
	windWeightedCn2 := 4e-12
	metrics := opticalTurbulenceMetricsFromMoments(integratedCn2, windWeightedCn2, 1)
	wavenumber := 2 * math.Pi / seeingWavelengthM
	wantMS := 1000 * math.Pow(coherencePhaseStructureCoefficient*wavenumber*wavenumber*windWeightedCn2, -3.0/5.0)
	if math.Abs(metrics.CoherenceTimeMS-wantMS) > 1e-12 {
		t.Fatalf("coherence time = %v ms, want phase-structure-consistent %v ms", metrics.CoherenceTimeMS, wantMS)
	}
	derived := math.Pow(coherencePhaseStructureCoefficient*4*math.Pi*math.Pi, -3.0/5.0)
	if math.Abs(derived-0.058056167701097) > 1e-15 {
		t.Fatalf("expanded coherence coefficient = %.15f, want 0.058056167701097", derived)
	}
}

func TestHMNSP99SeeingRespondsToVectorShear(t *testing.T) {
	calm := HMNSP99SeeingArcsec(standardAtmosphereProfile(0.001))
	sheared := HMNSP99SeeingArcsec(standardAtmosphereProfile(0.020))
	if !(sheared > calm) {
		t.Fatalf("seeing did not worsen with shear: calm=%v sheared=%v", calm, sheared)
	}
}

func TestHMNSP99CoherenceTimeRespondsToWindWithoutChangingSeeing(t *testing.T) {
	calm := standardAtmosphereProfile(0.002)
	fast := append([]VerticalLevel(nil), calm...)
	for index := range fast {
		// A uniform vector offset preserves every vertical shear and therefore
		// Cn2/seeing, but advects the same turbulence faster across the aperture.
		fast[index].UMS += 30
	}
	calmMetrics := HMNSP99Metrics(calm)
	fastMetrics := HMNSP99Metrics(fast)
	if math.Abs(calmMetrics.SeeingArcsec-fastMetrics.SeeingArcsec) > 1e-12 {
		t.Fatalf("uniform wind changed seeing: calm=%v fast=%v", calmMetrics.SeeingArcsec, fastMetrics.SeeingArcsec)
	}
	if !(fastMetrics.CoherenceTimeMS < calmMetrics.CoherenceTimeMS) {
		t.Fatalf("coherence time did not respond to wind: calm=%v fast=%v", calmMetrics.CoherenceTimeMS, fastMetrics.CoherenceTimeMS)
	}
}

func TestHybridOpticalTurbulenceAddsResolvedGroundLayer(t *testing.T) {
	pressure := standardAtmosphereProfile(0.002)
	model := make([]CloudLevel, 17)
	for index := range model {
		height := 25 + 150*float64(index)
		pressureHPA := 1013.25 * math.Pow(1-height/44330, 1/0.190284)
		model[index] = CloudLevel{
			ModelLevel: 74 - index, PressureHPA: pressureHPA, HeightM: height,
			TemperatureK: 288.15 - 0.0045*height,
			UMS:          3 + 0.003*height, VMS: 1,
			TKEJkg: 0.08 + 0.5*math.Exp(-height/900),
		}
	}
	metrics, ok := HybridOpticalTurbulenceMetrics(pressure, model, 0, 2000, 1)
	if !ok || !finite(metrics.SeeingArcsec) || !finite(metrics.CoherenceTimeMS) {
		t.Fatalf("hybrid metrics unavailable: ok=%v metrics=%+v", ok, metrics)
	}
	if metrics.GroundLayerCn2 <= 0 || metrics.GroundLayerFraction <= 0 || metrics.GroundLayerFraction > 1 {
		t.Fatalf("invalid ground-layer contribution: %+v", metrics)
	}
	// Height-weighted diagnostics preserve J and seeing. JV and tau0 use the
	// trapezoidal endpoint integral of the nonlinear |V|^(5/3) moment instead
	// of exponentiating an already averaged speed.
	assertRelativeClose(t, "hybrid J regression", metrics.IntegratedCn2, 6.086198914674668e-12, 1e-12)
	assertRelativeClose(t, "hybrid JV regression", metrics.WindWeightedCn2, 1.7590583810709743e-10, 1e-12)
	assertRelativeClose(t, "hybrid seeing regression", metrics.SeeingArcsec, 3.7160791518027287, 1e-12)
	assertRelativeClose(t, "hybrid tau0 regression", metrics.CoherenceTimeMS, 1.1361723544313702, 1e-12)
	assertRelativeClose(t, "hybrid dynamic PBL J regression", metrics.GroundLayerCn2, 5.677780786938006e-12, 1e-12)
	if metrics.ProfileQuality != OpticalTurbulenceProfileComplete {
		t.Fatalf("complete hybrid profile quality = %q", metrics.ProfileQuality)
	}
	if !(metrics.FracGL250 <= metrics.FracGL500 && metrics.FracGL500 <= metrics.FracGL1000) {
		t.Fatalf("fixed ground-layer fractions are not monotonic: %+v", metrics)
	}
	free := hmnsp99MetricsAbove(pressure, 2000)
	if !(metrics.IntegratedCn2 > free.IntegratedCn2) {
		t.Fatalf("ground layer was not added: hybrid=%v free=%v", metrics.IntegratedCn2, free.IntegratedCn2)
	}
}

func TestHMNSP99SeeingRequiresTemperatureProfile(t *testing.T) {
	levels := standardAtmosphereProfile(0.002)
	for index := range levels {
		levels[index].TemperatureK = math.NaN()
	}
	if value := HMNSP99SeeingArcsec(levels); !math.IsNaN(value) {
		t.Fatalf("seeing without temperature = %v, want NaN", value)
	}
}

func standardAtmosphereProfile(shearPerSecond float64) []VerticalLevel {
	pressures := []float64{1000, 950, 925, 900, 875, 850, 825, 800, 775, 700, 600, 500, 400, 300, 250, 200, 150, 100, 70, 50}
	levels := make([]VerticalLevel, len(pressures))
	for index, pressure := range pressures {
		height := 44330 * (1 - math.Pow(pressure/1013.25, 0.190284))
		temperature := 288.15 - 0.0065*math.Min(height, 11000)
		levels[index] = VerticalLevel{
			PressureHPA: pressure, HeightM: height, TemperatureK: temperature,
			UMS: 5 + shearPerSecond*height, VMS: 2,
		}
	}
	return levels
}
