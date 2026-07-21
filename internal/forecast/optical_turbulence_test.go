package forecast

import (
	"math"
	"testing"
)

func TestHMNSP99SeeingProducesPlausibleStandardProfile(t *testing.T) {
	levels := standardAtmosphereProfile(0.002)
	seeing := HMNSP99SeeingArcsec(levels)
	if !finite(seeing) || seeing < 0.3 || seeing > 3.0 {
		t.Fatalf("standard-profile seeing = %v arcsec, want a plausible model range", seeing)
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
