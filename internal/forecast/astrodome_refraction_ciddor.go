package forecast

import (
	"fmt"
	"math"
)

const (
	AstrodomeCiddorVersion            = "ciddor-1996-phase-index-v1"
	AstrodomeCiddorMinimumWavelengthM = 230e-9
	AstrodomeCiddorMaximumWavelengthM = 1690e-9
	astrodomeCiddorMolarMassWater     = 0.018015
	astrodomeCiddorMolarGasConstant   = 8.314510
)

// AstrodomeRefractivityState is the Ciddor (1996) phase refractive index and
// its analytic primitive partial derivatives. The input humidity is native
// model specific humidity (mass fraction), converted exactly to water-vapour
// mole fraction; no relative-humidity approximation is introduced.
type AstrodomeRefractivityState struct {
	Version                        string  `json:"version"`
	RefractiveIndex                float64 `json:"refractive_index"`
	DerivativePressurePa           float64 `json:"derivative_pressure_pa"`
	DerivativeTemperatureK         float64 `json:"derivative_temperature_k"`
	DerivativeSpecificHumidityKgKg float64 `json:"derivative_specific_humidity_kg_kg"`
}

// AstrodomeCiddorPhaseRefractivity evaluates equations (1)--(3), (12), (14),
// and (15) of P. E. Ciddor, Applied Optics 35(9), 1566--1573 (1996), DOI
// 10.1364/AO.35.001566. It uses the published BIPM 1981/91 compressibility
// expression without the optional approximations discussed by Ciddor.
func AstrodomeCiddorPhaseRefractivity(
	pressurePa,
	temperatureK,
	specificHumidityKgKg,
	wavelengthM,
	carbonDioxidePPM float64,
) (AstrodomeRefractivityState, error) {
	if !finite(pressurePa) || pressurePa <= 0 || pressurePa > 2e5 {
		return AstrodomeRefractivityState{}, fmt.Errorf("pressure supplied to Ciddor must be finite and in (0, 200000] Pa")
	}
	if !finite(temperatureK) || temperatureK < 150 || temperatureK > 400 {
		return AstrodomeRefractivityState{}, fmt.Errorf("temperature supplied to Ciddor must be finite and in [150, 400] K")
	}
	if !finite(specificHumidityKgKg) || specificHumidityKgKg < 0 || specificHumidityKgKg >= 1 {
		return AstrodomeRefractivityState{}, fmt.Errorf("specific humidity supplied to Ciddor must be finite and in [0, 1)")
	}
	if !finite(wavelengthM) || wavelengthM < AstrodomeCiddorMinimumWavelengthM ||
		wavelengthM > AstrodomeCiddorMaximumWavelengthM {
		return AstrodomeRefractivityState{}, fmt.Errorf("wavelength for Ciddor must be within the published 230--1690 nm dispersion range")
	}
	if !finite(carbonDioxidePPM) || carbonDioxidePPM < 0 || carbonDioxidePPM > 5000 {
		return AstrodomeRefractivityState{}, fmt.Errorf("carbon-dioxide content for Ciddor must be finite and in [0, 5000] ppm")
	}

	pressure := astrodomeDualVariable(pressurePa, 0)
	temperature := astrodomeDualVariable(temperatureK, 1)
	humidity := astrodomeDualVariable(specificHumidityKgKg, 2)
	index, err := astrodomeCiddorIndexDual(pressure, temperature, humidity, wavelengthM, carbonDioxidePPM)
	if err != nil {
		return AstrodomeRefractivityState{}, err
	}
	if !finite(index.value) || index.value < 1 || index.value > 1.01 {
		return AstrodomeRefractivityState{}, fmt.Errorf("refractive index from Ciddor is outside the physical atmospheric range")
	}
	for _, derivative := range index.derivative {
		if !finite(derivative) {
			return AstrodomeRefractivityState{}, fmt.Errorf("refractive-index derivative from Ciddor is non-finite")
		}
	}
	return AstrodomeRefractivityState{
		Version: AstrodomeCiddorVersion, RefractiveIndex: index.value,
		DerivativePressurePa: index.derivative[0], DerivativeTemperatureK: index.derivative[1],
		DerivativeSpecificHumidityKgKg: index.derivative[2],
	}, nil
}

type astrodomeDual3 struct {
	value      float64
	derivative [3]float64
}

func astrodomeDualConstant(value float64) astrodomeDual3 {
	return astrodomeDual3{value: value}
}

func astrodomeDualVariable(value float64, variable int) astrodomeDual3 {
	result := astrodomeDual3{value: value}
	result.derivative[variable] = 1
	return result
}

func (value astrodomeDual3) add(other astrodomeDual3) astrodomeDual3 {
	result := astrodomeDual3{value: value.value + other.value}
	for index := range result.derivative {
		result.derivative[index] = value.derivative[index] + other.derivative[index]
	}
	return result
}

func (value astrodomeDual3) subtract(other astrodomeDual3) astrodomeDual3 {
	result := astrodomeDual3{value: value.value - other.value}
	for index := range result.derivative {
		result.derivative[index] = value.derivative[index] - other.derivative[index]
	}
	return result
}

func (value astrodomeDual3) multiply(other astrodomeDual3) astrodomeDual3 {
	result := astrodomeDual3{value: value.value * other.value}
	for index := range result.derivative {
		result.derivative[index] = value.derivative[index]*other.value + value.value*other.derivative[index]
	}
	return result
}

func (value astrodomeDual3) divide(other astrodomeDual3) astrodomeDual3 {
	result := astrodomeDual3{value: value.value / other.value}
	denominator := other.value * other.value
	for index := range result.derivative {
		result.derivative[index] = (value.derivative[index]*other.value - value.value*other.derivative[index]) / denominator
	}
	return result
}

func (value astrodomeDual3) scale(factor float64) astrodomeDual3 {
	return value.multiply(astrodomeDualConstant(factor))
}

func astrodomeCiddorIndexDual(
	pressure,
	temperature,
	specificHumidity astrodomeDual3,
	wavelengthM,
	carbonDioxidePPM float64,
) (astrodomeDual3, error) {
	wavelengthMicrometres := wavelengthM * 1e6
	sigma := 1 / wavelengthMicrometres
	sigma2 := sigma * sigma
	dryStandardRefractivity := 1e-8 * (5792105/(238.0185-sigma2) + 167917/(57.362-sigma2))
	dryReferenceRefractivity := dryStandardRefractivity * (1 + 0.534e-6*(carbonDioxidePPM-450))
	waterReferenceRefractivity := 1.022e-8 * (295.235 + 2.6422*sigma2 -
		0.032380*math.Pow(sigma, 4) + 0.004028*math.Pow(sigma, 6))

	molarMassDry := 1e-3 * (28.9635 + 12.011e-6*(carbonDioxidePPM-400))
	molarMassWater := astrodomeCiddorMolarMassWater
	// xw = (q/Mw)/[(q/Mw)+(1-q)/Ma], with q the native specific
	// humidity. This is algebraic and introduces no saturation/RH model.
	numerator := specificHumidity.scale(molarMassDry)
	denominator := astrodomeDualConstant(molarMassWater).
		add(specificHumidity.scale(molarMassDry - molarMassWater))
	waterMoleFraction := numerator.divide(denominator)

	compressibility := astrodomeCiddorCompressibility(pressure, temperature, waterMoleFraction)
	if !finite(compressibility.value) || compressibility.value <= 0 {
		return astrodomeDual3{}, fmt.Errorf("moist-air compressibility in Ciddor is non-positive")
	}
	densityDenominator := compressibility.multiply(temperature).scale(astrodomeCiddorMolarGasConstant)
	dryDensity := pressure.scale(molarMassDry).
		multiply(astrodomeDualConstant(1).subtract(waterMoleFraction)).
		divide(densityDenominator)
	waterDensity := pressure.scale(molarMassWater).multiply(waterMoleFraction).divide(densityDenominator)

	dryReferenceZ := astrodomeCiddorCompressibility(
		astrodomeDualConstant(101325), astrodomeDualConstant(288.15), astrodomeDualConstant(0),
	).value
	waterReferenceZ := astrodomeCiddorCompressibility(
		astrodomeDualConstant(1333), astrodomeDualConstant(293.15), astrodomeDualConstant(1),
	).value
	dryReferenceDensity := 101325 * molarMassDry / (dryReferenceZ * astrodomeCiddorMolarGasConstant * 288.15)
	waterReferenceDensity := 1333 * molarMassWater / (waterReferenceZ * astrodomeCiddorMolarGasConstant * 293.15)
	if dryReferenceDensity <= 0 || waterReferenceDensity <= 0 {
		return astrodomeDual3{}, fmt.Errorf("reference density in Ciddor is non-positive")
	}
	refractivity := dryDensity.scale(dryReferenceRefractivity / dryReferenceDensity).
		add(waterDensity.scale(waterReferenceRefractivity / waterReferenceDensity))
	return astrodomeDualConstant(1).add(refractivity), nil
}

func astrodomeCiddorCompressibility(pressure, temperature, waterMoleFraction astrodomeDual3) astrodomeDual3 {
	t := temperature.subtract(astrodomeDualConstant(273.15))
	t2 := t.multiply(t)
	x2 := waterMoleFraction.multiply(waterMoleFraction)
	firstBracket := astrodomeDualConstant(1.58123e-6).
		add(t.scale(-2.9331e-8)).
		add(t2.scale(1.1043e-10)).
		add(astrodomeDualConstant(5.707e-6).add(t.scale(-2.051e-8)).multiply(waterMoleFraction)).
		add(astrodomeDualConstant(1.9898e-4).add(t.scale(-2.376e-6)).multiply(x2))
	pressureOverTemperature := pressure.divide(temperature)
	return astrodomeDualConstant(1).
		subtract(pressureOverTemperature.multiply(firstBracket)).
		add(pressureOverTemperature.multiply(pressureOverTemperature).
			multiply(astrodomeDualConstant(1.83e-11).add(x2.scale(-0.765e-8))))
}
