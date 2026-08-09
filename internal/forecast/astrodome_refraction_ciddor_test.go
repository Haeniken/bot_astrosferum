package forecast

import (
	"math"
	"testing"
)

func TestAstrodomeCiddorMatchesPublishedMoistAirTable(t *testing.T) {
	t.Parallel()

	const (
		pressurePa       = 102094.8
		waterPressurePa  = 1065.0
		carbonDioxidePPM = 510.0
	)
	molarMassDry := 1e-3 * (28.9635 + 12.011e-6*(carbonDioxidePPM-400))
	temperatureC := 19.526
	enhancementFactor := 1.00062 + 3.14e-8*pressurePa + 5.6e-7*temperatureC*temperatureC
	waterMoleFraction := enhancementFactor * waterPressurePa / pressurePa
	specificHumidity := waterMoleFraction * astrodomeCiddorMolarMassWater /
		(waterMoleFraction*astrodomeCiddorMolarMassWater + (1-waterMoleFraction)*molarMassDry)
	result, err := AstrodomeCiddorPhaseRefractivity(
		pressurePa, temperatureC+273.15, specificHumidity, 633e-9, carbonDioxidePPM,
	)
	if err != nil {
		t.Fatal(err)
	}
	// Ciddor (1996), Table 2, first row: 10^8(n-1)=27392.9.
	want := 1 + 27392.9e-8
	if math.Abs(result.RefractiveIndex-want) > 5.1e-10 {
		t.Fatalf("Ciddor table regression: got %.12f, want %.12f", result.RefractiveIndex, want)
	}
}

func TestAstrodomeCiddorAnalyticPrimitiveDerivatives(t *testing.T) {
	t.Parallel()

	const (
		pressurePa       = 93450.0
		temperatureK     = 281.35
		specificHumidity = 0.0072
		wavelengthM      = 500e-9
		carbonDioxidePPM = 425.0
	)
	result, err := AstrodomeCiddorPhaseRefractivity(
		pressurePa, temperatureK, specificHumidity, wavelengthM, carbonDioxidePPM,
	)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name       string
		step       float64
		derivative float64
		evaluate   func(delta float64) (float64, error)
	}{
		{
			name: "pressure", step: 0.1, derivative: result.DerivativePressurePa,
			evaluate: func(delta float64) (float64, error) {
				value, evaluateErr := AstrodomeCiddorPhaseRefractivity(
					pressurePa+delta, temperatureK, specificHumidity, wavelengthM, carbonDioxidePPM,
				)
				return value.RefractiveIndex, evaluateErr
			},
		},
		{
			name: "temperature", step: 1e-3, derivative: result.DerivativeTemperatureK,
			evaluate: func(delta float64) (float64, error) {
				value, evaluateErr := AstrodomeCiddorPhaseRefractivity(
					pressurePa, temperatureK+delta, specificHumidity, wavelengthM, carbonDioxidePPM,
				)
				return value.RefractiveIndex, evaluateErr
			},
		},
		{
			name: "specific humidity", step: 1e-6, derivative: result.DerivativeSpecificHumidityKgKg,
			evaluate: func(delta float64) (float64, error) {
				value, evaluateErr := AstrodomeCiddorPhaseRefractivity(
					pressurePa, temperatureK, specificHumidity+delta, wavelengthM, carbonDioxidePPM,
				)
				return value.RefractiveIndex, evaluateErr
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			plus, err := test.evaluate(test.step)
			if err != nil {
				t.Fatal(err)
			}
			minus, err := test.evaluate(-test.step)
			if err != nil {
				t.Fatal(err)
			}
			finiteDifference := (plus - minus) / (2 * test.step)
			scale := math.Max(math.Abs(finiteDifference), math.Abs(test.derivative))
			if math.Abs(finiteDifference-test.derivative) > 2e-6*scale+1e-14 {
				t.Fatalf("analytic derivative %.12g, central difference %.12g", test.derivative, finiteDifference)
			}
		})
	}
}

func TestAstrodomeCiddorRejectsUndeclaredDispersionExtrapolation(t *testing.T) {
	t.Parallel()

	if _, err := AstrodomeCiddorPhaseRefractivity(101325, 288.15, 0, 200e-9, 425); err == nil {
		t.Fatal("wavelength below Ciddor's published range was accepted")
	}
	if _, err := AstrodomeCiddorPhaseRefractivity(101325, 288.15, 0, 2e-6, 425); err == nil {
		t.Fatal("wavelength above Ciddor's published range was accepted")
	}
}

func TestAstrodomeHydrostaticPressureUsesExplicitTwoMetreMoistAnchor(t *testing.T) {
	t.Parallel()

	const (
		surfacePressure = 101325.0
		temperature2M   = 288.15
		humidity2M      = 0.01
		rd              = 287.05
		rv              = 461.5
	)
	got, err := AstrodomeHydrostaticPressureAtAperture(surfacePressure, temperature2M, humidity2M, rd, rv)
	if err != nil {
		t.Fatal(err)
	}
	rMix := (1-humidity2M)*rd + humidity2M*rv
	want := surfacePressure * math.Exp(-AstrodomeICONReferenceGravityMS2*2/(rMix*temperature2M))
	if math.Abs(got-want) > 1e-12*want {
		t.Fatalf("two-metre hydrostatic pressure %.12g, want %.12g", got, want)
	}
	if !(got < surfacePressure && surfacePressure-got < 30) {
		t.Fatalf("two-metre pressure decrement is implausible: surface=%g aperture=%g", surfacePressure, got)
	}
}
