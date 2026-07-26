package forecast

import (
	"fmt"
	"math"
	"sort"
	"time"
)

const (
	ReferenceVBandAtmosphereVersion = "reference-v-band-spectrl2-ks91-v3"
	ReferenceVBandBenchmarkVersion  = "reference-v-band-benchmark-v1"
	ReferenceVBandNaturalSkyMag     = 21.7
	ReferenceVBandBenchmarkSeeing   = 1.0
	ReferenceVBandAerosolAlpha      = 1.14
	ReferenceVBandFilterProvenance  = "SVO Generic/Johnson.V; canonical curve sha256:941cc5859ba08b7a7d74f359fc144fe2e947a34250f83d2a7655ea1ef90e543d"
)

// AtmosphericCompositionFrame is the provider-neutral subset of an
// independent composition forecast needed by the V-band reference model.
// AOD is vertical aerosol optical depth at 550 nm. TotalColumnOzoneDU uses
// Dobson units. The meteorological and composition runs are deliberately kept
// separate because they do not share a run identifier or initialization time.
type AtmosphericCompositionFrame struct {
	ValidAt                   time.Time `json:"valid_at"`
	AerosolOpticalDepth550    float64   `json:"aerosol_optical_depth_550"`
	TotalColumnOzoneDU        float64   `json:"total_column_ozone_du"`
	Provider                  string    `json:"provider"`
	RunID                     string    `json:"run_id"`
	BaseTime                  time.Time `json:"base_time"`
	Grid                      string    `json:"grid"`
	AerosolSpectralAssumption string    `json:"aerosol_spectral_assumption"`
}

// ReferenceVBandSkyGeometry is calculated by internal/astronomy and passed as
// values so this package remains independent from ephemeris implementation.
// The Moon fields are topocentric; phase angle is zero at full Moon.
type ReferenceVBandSkyGeometry struct {
	ValidAt               time.Time
	SunAltitudeDegrees    float64
	MoonAltitudeDegrees   float64
	MoonZenithDistanceDeg float64
	MoonPhaseAngleDegrees float64
	MoonEarthDistanceKM   float64
	MoonGeometryAvailable bool
}

// ReferenceVBandDiagnostic exposes the physical inputs behind Result. It is a
// declared grid-cell-mean Johnson-V reference, not a universal score and not a
// claim about a user's telescope or local artificial sky brightness. ICON's
// all-sky cloud transmission attenuates the source, while the background is
// conservatively held at the modeled clear-air natural/lunar floor because a
// deterministic NWP column cannot predict cloud-scattered sky radiance.
type ReferenceVBandDiagnostic struct {
	Result                         ReferenceVBandResult `json:"result"`
	AtmosphericTransmissionPercent float64              `json:"atmospheric_transmission_percent"`
	ClearAirTransmissionPercent    float64              `json:"clear_air_transmission_percent"`
	ExtinctionMag                  *float64             `json:"extinction_mag,omitempty"`
	OpaqueTransmission             bool                 `json:"opaque_transmission"`
	SkyBrightnessMagArcsec2        float64              `json:"sky_brightness_mag_arcsec2"`
	PrecipitableWaterMM            float64              `json:"precipitable_water_mm"`
	SurfacePressureHPA             float64              `json:"surface_pressure_hpa,omitempty"`
	SurfacePressureProvenance      string               `json:"surface_pressure_provenance,omitempty"`
	AerosolOpticalDepth550         float64              `json:"aerosol_optical_depth_550"`
	TotalColumnOzoneDU             float64              `json:"total_column_ozone_du"`
	MoonAltitudeDegrees            float64              `json:"moon_altitude_degrees"`
	MoonPhaseAngleDegrees          float64              `json:"moon_phase_angle_degrees"`
	CompositionProvider            string               `json:"composition_provider,omitempty"`
	CompositionRunID               string               `json:"composition_run_id,omitempty"`
	CompositionBaseTime            time.Time            `json:"composition_base_time,omitempty"`
	CompositionGrid                string               `json:"composition_grid,omitempty"`
	OperationallyUnavailable       bool                 `json:"operationally_unavailable"`
	OperationalReason              string               `json:"operational_reason,omitempty"`
	ModelVersion                   string               `json:"model_version"`
	SpectralLimitation             string               `json:"spectral_limitation"`
	BackgroundLimitation           string               `json:"background_limitation"`
}

type vBandSpectralPoint struct {
	wavelengthNM float64
	response     float64
	water        float64
	ozone        float64
	mixedGas     float64
}

var genericJohnsonVResponse = []struct {
	wavelengthNM float64
	response     float64
}{
	{460, 0}, {480, 0.02}, {500, 0.38}, {520, 0.91}, {540, 0.98},
	{560, 0.72}, {580, 0.62}, {600, 0.40}, {620, 0.20}, {640, 0.08},
	{660, 0.02}, {680, 0.01}, {700, 0.01}, {720, 0.01}, {740, 0},
}

// The Bird--Riordan SPECTRL2 coefficients below are the model's published
// grid restricted to the Generic/Johnson.V support. Values between nodes are
// linearly interpolated, matching the discrete spectral model's intended use.
var spectrl2VBandCoefficients = []struct {
	wavelengthNM float64
	water        float64
	ozone        float64
	mixedGas     float64
}{
	{450, 0, 0.003, 0}, {460, 0, 0.006, 0}, {470, 0, 0.009, 0},
	{480, 0, 0.014, 0}, {490, 0, 0.021, 0}, {500, 0, 0.030, 0},
	{510, 0, 0.040, 0}, {520, 0, 0.048, 0}, {530, 0, 0.063, 0},
	{540, 0, 0.075, 0}, {550, 0, 0.085, 0}, {570, 0, 0.120, 0},
	{593, 0.075, 0.119, 0}, {610, 0, 0.120, 0}, {630, 0, 0.090, 0},
	{656, 0, 0.065, 0}, {667.6, 0, 0.051, 0}, {690, 0.016, 0.028, 0.15},
	{710, 0.0125, 0.018, 0}, {718, 1.8, 0.015, 0}, {724.4, 2.5, 0.012, 0},
	{740, 0.061, 0.010, 0},
}

// ComputeReferenceVBandAtmosphere evaluates a physically declared zenith,
// long-exposure, background-limited V-band reference. PWV enters only through
// the SPECTRL2 spectral water-vapour term; it is never converted to an
// unrelated seeing penalty. A nil composition frame yields a partial result
// instead of silently assuming clean air.
func ComputeReferenceVBandAtmosphere(
	surface SurfaceFrame,
	overall OverallIndexFrame,
	composition *AtmosphericCompositionFrame,
	sky ReferenceVBandSkyGeometry,
) (ReferenceVBandDiagnostic, error) {
	if surface.ValidAt.IsZero() || !surface.ValidAt.Equal(overall.ValidAt) || !surface.ValidAt.Equal(sky.ValidAt) {
		return ReferenceVBandDiagnostic{}, fmt.Errorf("reference V-band inputs must share one non-zero validity time")
	}
	if !finite(sky.SunAltitudeDegrees) || sky.SunAltitudeDegrees < -90 || sky.SunAltitudeDegrees > 90 {
		return ReferenceVBandDiagnostic{}, fmt.Errorf("reference V-band solar altitude is invalid")
	}
	provenance := ReferenceVBandProvenance{
		SolarGeometry:    "pure-Go Meeus solar geometry",
		AtmosphericPSF:   "Kolmogorov lambda^(-1/5) Gaussian-mixture NEA from hybrid ICON Cn2 seeing",
		Benchmark:        ReferenceVBandBenchmarkVersion,
		PassbandResponse: ReferenceVBandFilterProvenance,
	}
	input := ReferenceVBandInput{
		ValidAt:             surface.ValidAt,
		SunAltitudeDegrees:  sky.SunAltitudeDegrees,
		AvailableComponents: ReferenceVBandSolarGeometry | ReferenceVBandBenchmarkEfficiency,
		Provenance:          provenance,
	}
	diagnostic := ReferenceVBandDiagnostic{
		PrecipitableWaterMM:       surface.PrecipitableWaterMM,
		SurfacePressureHPA:        overall.SurfacePressureHPA,
		SurfacePressureProvenance: overall.SurfacePressureProvenance,
		ModelVersion:              ReferenceVBandAtmosphereVersion,
		SpectralLimitation:        "GEOS-CF supplies AOD at 550 nm only; SPECTRL2 rural Angstrom exponent alpha=1.14 is a declared spectral-shape assumption",
		BackgroundLimitation:      "ICON cloud transmission attenuates the source only; cloud-scattered sky radiance is unavailable, so the background remains at the clear-air natural/lunar floor",
	}

	if finite(overall.SeeingArcsec) && overall.SeeingArcsec > 0 {
		psfArea, err := vBandGaussianMixtureNEA(overall.SeeingArcsec, nil)
		if err != nil {
			return ReferenceVBandDiagnostic{}, err
		}
		input.NoiseEquivalentPSFSolidAngleArcsec2 = psfArea
		input.AvailableComponents |= ReferenceVBandNoiseEquivalentPSFSolidAngle
	}

	benchmark, err := referenceVBandBenchmarkEfficiency()
	if err != nil {
		return ReferenceVBandDiagnostic{}, err
	}
	input.BenchmarkEfficiency = benchmark

	surfacePressureAvailable := finite(overall.SurfacePressureHPA) && overall.SurfacePressureHPA > 0 &&
		overall.SurfacePressureHPA <= 1200 && overall.SurfacePressureProvenance != ""
	if composition != nil && surfacePressureAvailable {
		if err := validateCompositionFrame(*composition, surface.ValidAt); err != nil {
			return ReferenceVBandDiagnostic{}, err
		}
		clearTransmission, sourceRate, spectralPoints, err := vBandAtmosphericTransmission(
			overall.SurfacePressureHPA,
			surface.PrecipitableWaterMM,
			composition.TotalColumnOzoneDU,
			composition.AerosolOpticalDepth550,
			clampSurfaceValue(overall.CloudTransmissionPercent/100, 0, 1),
		)
		if err != nil {
			return ReferenceVBandDiagnostic{}, err
		}
		if finite(overall.SeeingArcsec) && overall.SeeingArcsec > 0 {
			psfArea, psfErr := vBandGaussianMixtureNEA(overall.SeeingArcsec, spectralPoints)
			if psfErr != nil {
				return ReferenceVBandDiagnostic{}, psfErr
			}
			input.NoiseEquivalentPSFSolidAngleArcsec2 = psfArea
			input.AvailableComponents |= ReferenceVBandNoiseEquivalentPSFSolidAngle
		}
		cloudTransmission := clampSurfaceValue(overall.CloudTransmissionPercent/100, 0, 1)
		atmosphericTransmission := clearTransmission * cloudTransmission
		var extinctionMag *float64
		if atmosphericTransmission > 0 {
			value := -2.5 * math.Log10(atmosphericTransmission)
			extinctionMag = &value
		} else {
			diagnostic.OpaqueTransmission = true
		}
		clearExtinctionMag := -2.5 * math.Log10(math.Max(clearTransmission, math.SmallestNonzeroFloat64))
		skyMagnitude := ReferenceVBandNaturalSkyMag
		if sky.MoonGeometryAvailable {
			var moonError error
			skyMagnitude, moonError = referenceVBandMoonSkyMagnitude(sky, clearExtinctionMag)
			if moonError != nil {
				return ReferenceVBandDiagnostic{}, moonError
			}
		}
		topRate := vBandABZeroPhotonRate(nil)
		skyRate := topRate * math.Pow(10, -0.4*skyMagnitude)

		input.SourcePhotonRate = sourceRate
		input.SkyPhotonRadiance = skyRate
		input.AvailableComponents |= ReferenceVBandSourcePhotonRate
		if sky.MoonGeometryAvailable {
			input.AvailableComponents |= ReferenceVBandSkyPhotonRadiance
		}
		input.Provenance.AtmosphericTransmission = "Bird-Riordan SPECTRL2 direct beam + lowest native ICON model-level P hydrostatically transferred to HHL surface + ICON TQV/cloud transmission + NASA GEOS-CF AOD550/O3"
		input.Provenance.SkyRadiance = "KS91 V-band zenith Moon fallback + declared 21.7 mag/arcsec2 natural dark reference; artificial light and cloud-scattered radiance excluded"

		diagnostic.AtmosphericTransmissionPercent = atmosphericTransmission * 100
		diagnostic.ClearAirTransmissionPercent = clearTransmission * 100
		diagnostic.ExtinctionMag = extinctionMag
		if sky.MoonGeometryAvailable {
			diagnostic.SkyBrightnessMagArcsec2 = skyMagnitude
		}
		diagnostic.AerosolOpticalDepth550 = composition.AerosolOpticalDepth550
		diagnostic.TotalColumnOzoneDU = composition.TotalColumnOzoneDU
		diagnostic.CompositionProvider = composition.Provider
		diagnostic.CompositionRunID = composition.RunID
		diagnostic.CompositionBaseTime = composition.BaseTime
		diagnostic.CompositionGrid = composition.Grid
	}
	if sky.MoonGeometryAvailable {
		diagnostic.MoonAltitudeDegrees = sky.MoonAltitudeDegrees
		diagnostic.MoonPhaseAngleDegrees = sky.MoonPhaseAngleDegrees
	}

	result, err := ComputeReferenceVBandZenithEfficiency(input)
	if err != nil {
		return ReferenceVBandDiagnostic{}, err
	}
	if overall.PrecipitationVeto {
		diagnostic.OperationallyUnavailable = true
		diagnostic.OperationalReason = "precipitation"
		result.Available = false
	} else if overall.HighFog {
		diagnostic.OperationallyUnavailable = true
		diagnostic.OperationalReason = "high_fog_risk"
		result.Available = false
	}
	diagnostic.Result = result
	return diagnostic, nil
}

func validateCompositionFrame(frame AtmosphericCompositionFrame, at time.Time) error {
	if frame.ValidAt.IsZero() || !frame.ValidAt.Equal(at) {
		return fmt.Errorf("composition frame does not match reference V-band validity time")
	}
	if !finite(frame.AerosolOpticalDepth550) || frame.AerosolOpticalDepth550 < 0 || frame.AerosolOpticalDepth550 > 10 {
		return fmt.Errorf("composition AOD550 must be finite and between 0 and 10")
	}
	if !finite(frame.TotalColumnOzoneDU) || frame.TotalColumnOzoneDU <= 0 || frame.TotalColumnOzoneDU > 1000 {
		return fmt.Errorf("composition total ozone must be finite and between 0 and 1000 DU")
	}
	if frame.Provider == "" || frame.RunID == "" || frame.BaseTime.IsZero() || frame.Grid == "" {
		return fmt.Errorf("composition provenance is incomplete")
	}
	return nil
}

func vBandAtmosphericTransmission(pressureHPA, pwvMM, ozoneDU, aod550, cloudTransmission float64) (clearTransmission, sourceRate float64, points []vBandSpectralPoint, err error) {
	if !finite(pressureHPA) || pressureHPA <= 0 || pressureHPA > 1200 ||
		!finite(pwvMM) || pwvMM < 0 || pwvMM > 150 ||
		!finite(ozoneDU) || ozoneDU <= 0 || ozoneDU > 1000 ||
		!finite(aod550) || aod550 < 0 || aod550 > 10 ||
		!finite(cloudTransmission) || cloudTransmission < 0 || cloudTransmission > 1 {
		return 0, 0, nil, fmt.Errorf("reference V-band atmospheric input is outside its physical domain")
	}
	points = vBandSpectralGrid(pressureHPA, pwvMM, ozoneDU, aod550)
	topRate := vBandABZeroPhotonRate(nil)
	clearRate := vBandABZeroPhotonRate(points)
	if topRate <= 0 || clearRate <= 0 || !finite(clearRate) {
		return 0, 0, nil, fmt.Errorf("reference V-band photon integration failed")
	}
	clearTransmission = clampSurfaceValue(clearRate/topRate, 0, 1)
	sourceRate = clearRate * cloudTransmission
	return clearTransmission, sourceRate, points, nil
}

func vBandSpectralGrid(pressureHPA, pwvMM, ozoneDU, aod550 float64) []vBandSpectralPoint {
	wavelengths := make([]float64, 0, len(genericJohnsonVResponse)+len(spectrl2VBandCoefficients))
	for _, item := range genericJohnsonVResponse {
		wavelengths = append(wavelengths, item.wavelengthNM)
	}
	for _, item := range spectrl2VBandCoefficients {
		if item.wavelengthNM >= genericJohnsonVResponse[0].wavelengthNM &&
			item.wavelengthNM <= genericJohnsonVResponse[len(genericJohnsonVResponse)-1].wavelengthNM {
			wavelengths = append(wavelengths, item.wavelengthNM)
		}
	}
	sort.Float64s(wavelengths)
	unique := wavelengths[:0]
	for _, wavelength := range wavelengths {
		if len(unique) == 0 || math.Abs(wavelength-unique[len(unique)-1]) > 1e-9 {
			unique = append(unique, wavelength)
		}
	}
	points := make([]vBandSpectralPoint, 0, len(unique))
	for _, wavelength := range unique {
		water, ozone, mixed := interpolateSPECTRL2Coefficients(wavelength)
		wavelengthUM := wavelength / 1000
		absoluteAirmass := pressureHPA * 100 / 101300
		wavelengthUM2 := wavelengthUM * wavelengthUM
		wavelengthUM4 := wavelengthUM2 * wavelengthUM2
		tRayleigh := math.Exp(-absoluteAirmass /
			(wavelengthUM4 * (115.6406 - 1.3366/wavelengthUM2)))
		tAerosol := math.Exp(-aod550 * math.Pow(wavelength/550, -ReferenceVBandAerosolAlpha))
		aWM := water * (pwvMM / 10)
		tWater := math.Exp(-0.2385 * aWM / math.Pow(1+20.07*aWM, 0.45))
		tOzone := math.Exp(-ozone * (ozoneDU / 1000))
		aM := mixed * absoluteAirmass
		tMixed := math.Exp(-1.41 * aM / math.Pow(1+118.3*aM, 0.45))
		points = append(points, vBandSpectralPoint{
			wavelengthNM: wavelength,
			response:     interpolateFilterResponse(wavelength) * tRayleigh * tAerosol * tWater * tOzone * tMixed,
			water:        water,
			ozone:        ozone,
			mixedGas:     mixed,
		})
	}
	return points
}

func interpolateFilterResponse(wavelength float64) float64 {
	return interpolatePair(wavelength, len(genericJohnsonVResponse), func(index int) (float64, float64) {
		item := genericJohnsonVResponse[index]
		return item.wavelengthNM, item.response
	})
}

func interpolateSPECTRL2Coefficients(wavelength float64) (water, ozone, mixed float64) {
	water = interpolatePair(wavelength, len(spectrl2VBandCoefficients), func(index int) (float64, float64) {
		item := spectrl2VBandCoefficients[index]
		return item.wavelengthNM, item.water
	})
	ozone = interpolatePair(wavelength, len(spectrl2VBandCoefficients), func(index int) (float64, float64) {
		item := spectrl2VBandCoefficients[index]
		return item.wavelengthNM, item.ozone
	})
	mixed = interpolatePair(wavelength, len(spectrl2VBandCoefficients), func(index int) (float64, float64) {
		item := spectrl2VBandCoefficients[index]
		return item.wavelengthNM, item.mixedGas
	})
	return water, ozone, mixed
}

func interpolatePair(value float64, count int, pair func(int) (float64, float64)) float64 {
	firstX, firstY := pair(0)
	if value <= firstX {
		return firstY
	}
	lastX, lastY := pair(count - 1)
	if value >= lastX {
		return lastY
	}
	upper := sort.Search(count, func(index int) bool {
		x, _ := pair(index)
		return x >= value
	})
	x0, y0 := pair(upper - 1)
	x1, y1 := pair(upper)
	return y0 + (value-x0)*(y1-y0)/(x1-x0)
}

func vBandABZeroPhotonRate(atmosphere []vBandSpectralPoint) float64 {
	const (
		abZeroFluxDensityWattM2Hz = ReferenceVBandABZeroPointJy * 1e-26
		planckJouleSecond         = 6.62607015e-34
	)
	points := atmosphere
	if points == nil {
		points = make([]vBandSpectralPoint, len(genericJohnsonVResponse))
		for index, item := range genericJohnsonVResponse {
			points[index] = vBandSpectralPoint{wavelengthNM: item.wavelengthNM, response: item.response}
		}
	}
	if len(points) < 2 {
		return 0
	}
	integral := 0.0
	for index := 1; index < len(points); index++ {
		left, right := points[index-1], points[index]
		leftLambdaM := left.wavelengthNM * 1e-9
		rightLambdaM := right.wavelengthNM * 1e-9
		leftRate := left.response / leftLambdaM
		rightRate := right.response / rightLambdaM
		integral += 0.5 * (leftRate + rightRate) * (rightLambdaM - leftLambdaM)
	}
	return abZeroFluxDensityWattM2Hz / planckJouleSecond * integral
}

// vBandGaussianMixtureNEA analytically integrates every pair of Gaussian PSFs
// in the photon-weighted band mixture. This avoids substituting one effective
// wavelength into the nonlinear noise-equivalent area.
func vBandGaussianMixtureNEA(seeing500Arcsec float64, atmosphere []vBandSpectralPoint) (float64, error) {
	if !finite(seeing500Arcsec) || seeing500Arcsec <= 0 {
		return 0, fmt.Errorf("reference V-band seeing must be finite and positive")
	}
	points := atmosphere
	if points == nil {
		points = make([]vBandSpectralPoint, len(genericJohnsonVResponse))
		for index, item := range genericJohnsonVResponse {
			points[index] = vBandSpectralPoint{wavelengthNM: item.wavelengthNM, response: item.response}
		}
	}
	weights := trapezoidNodeWeights(points)
	totalWeight := 0.0
	for _, weight := range weights {
		totalWeight += weight
	}
	if totalWeight <= 0 {
		return 0, fmt.Errorf("reference V-band PSF has no positive spectral weight")
	}
	integralPSFSquared := 0.0
	for left := range points {
		if weights[left] <= 0 {
			continue
		}
		leftFWHM := seeing500Arcsec * math.Pow(points[left].wavelengthNM/500, -0.2)
		leftSigma := leftFWHM / (2 * math.Sqrt(2*math.Log(2)))
		for right := range points {
			if weights[right] <= 0 {
				continue
			}
			rightFWHM := seeing500Arcsec * math.Pow(points[right].wavelengthNM/500, -0.2)
			rightSigma := rightFWHM / (2 * math.Sqrt(2*math.Log(2)))
			integralPSFSquared += weights[left] * weights[right] /
				(2 * math.Pi * (leftSigma*leftSigma + rightSigma*rightSigma))
		}
	}
	integralPSFSquared /= totalWeight * totalWeight
	if !finite(integralPSFSquared) || integralPSFSquared <= 0 {
		return 0, fmt.Errorf("reference V-band PSF integration failed")
	}
	return 1 / integralPSFSquared, nil
}

func trapezoidNodeWeights(points []vBandSpectralPoint) []float64 {
	weights := make([]float64, len(points))
	for index, point := range points {
		spanNM := 0.0
		if index > 0 {
			spanNM += (point.wavelengthNM - points[index-1].wavelengthNM) / 2
		}
		if index+1 < len(points) {
			spanNM += (points[index+1].wavelengthNM - point.wavelengthNM) / 2
		}
		weights[index] = spanNM * point.response / point.wavelengthNM
	}
	return weights
}

func referenceVBandBenchmarkEfficiency() (float64, error) {
	_, sourceRate, points, err := vBandAtmosphericTransmission(1013.25, 5, 300, 0.05, 1)
	if err != nil {
		return 0, err
	}
	psfArea, err := vBandGaussianMixtureNEA(ReferenceVBandBenchmarkSeeing, points)
	if err != nil {
		return 0, err
	}
	skyRate := vBandABZeroPhotonRate(nil) * math.Pow(10, -0.4*ReferenceVBandNaturalSkyMag)
	return sourceRate * sourceRate / (skyRate * psfArea), nil
}

func referenceVBandMoonSkyMagnitude(sky ReferenceVBandSkyGeometry, extinctionMag float64) (float64, error) {
	if !finite(extinctionMag) || extinctionMag < 0 ||
		!finite(sky.MoonAltitudeDegrees) || sky.MoonAltitudeDegrees < -90 || sky.MoonAltitudeDegrees > 90 ||
		!finite(sky.MoonZenithDistanceDeg) || sky.MoonZenithDistanceDeg < 0 || sky.MoonZenithDistanceDeg > 180 ||
		!finite(sky.MoonPhaseAngleDegrees) || sky.MoonPhaseAngleDegrees < 0 || sky.MoonPhaseAngleDegrees > 180 ||
		!finite(sky.MoonEarthDistanceKM) || sky.MoonEarthDistanceKM <= 0 {
		return 0, fmt.Errorf("reference V-band Moon geometry is invalid")
	}
	if math.Abs(sky.MoonZenithDistanceDeg-(90-sky.MoonAltitudeDegrees)) > 1e-6 {
		return 0, fmt.Errorf("reference V-band Moon altitude and zenith distance are inconsistent")
	}
	if sky.MoonAltitudeDegrees <= 0 {
		return ReferenceVBandNaturalSkyMag, nil
	}
	separation := clampSurfaceValue(sky.MoonZenithDistanceDeg, 0.25, 180)
	rhoRadians := separation * math.Pi / 180
	rayleigh := math.Pow(10, 5.36) * (1.06 + math.Pow(math.Cos(rhoRadians), 2))
	mie := math.Pow(10, 6.15-separation/40)
	if separation < 10 {
		mie = 6.2e7 / (separation * separation)
	}
	phase := sky.MoonPhaseAngleDegrees
	moonMagnitude := -12.73 + 0.026*phase + 4e-9*math.Pow(phase, 4)
	illuminance := math.Pow(10, -0.4*(moonMagnitude+16.57))
	distanceScale := 384400 / sky.MoonEarthDistanceKM
	illuminance *= distanceScale * distanceScale
	moonZenithRadians := clampSurfaceValue(sky.MoonZenithDistanceDeg, 0, 89.9) * math.Pi / 180
	moonAirmass := 1 / math.Sqrt(1-0.96*math.Pow(math.Sin(moonZenithRadians), 2))
	targetAirmass := 1.0
	moonNL := (rayleigh + mie) * illuminance * math.Pow(10, -0.4*extinctionMag*moonAirmass) *
		(1 - math.Pow(10, -0.4*extinctionMag*targetAirmass))
	darkNL := 34.08 * math.Exp(20.7233-0.92104*ReferenceVBandNaturalSkyMag)
	if !finite(moonNL) || moonNL < 0 || !finite(darkNL) || darkNL <= 0 {
		return 0, fmt.Errorf("reference V-band Moon-background calculation failed")
	}
	return (20.7233 - math.Log((darkNL+moonNL)/34.08)) / 0.92104, nil
}
