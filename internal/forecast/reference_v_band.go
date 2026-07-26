package forecast

import (
	"fmt"
	"math/bits"
	"time"
)

// Reference V-band zenith efficiency is deliberately a single, explicit
// synthetic observation rather than a universal astronomy score. Callers must
// pre-integrate every spectral quantity with the pinned passband and source
// spectrum before invoking this package.
const (
	ReferenceVBandContractVersion = "reference-v-band-zenith-efficiency-v2"
	ReferenceVBandPassbandID      = "SVO:Generic/Johnson.V@2021-07-27"
	ReferenceVBandSourceSpectrum  = "AB0-flat-fnu"
	ReferenceVBandDirection       = "grid-cell-mean-geometric-zenith"
	ReferenceVBandRegime          = "faint-point-source-background-limited-long-exposure-seeing-limited-non-ao"
	ReferenceVBandABZeroPointJy   = 3631.0

	// The strict boundary follows the astronomical-night contract: a term at
	// exactly -18 degrees is twilight and is therefore not applicable.
	ReferenceVBandMaximumSunAltitudeDegrees = -18.0
)

// ReferenceVBandComponent is a bit mask of independently sourced physical
// inputs. A set bit asserts both a valid numeric value and traceable
// provenance; missing inputs are never replaced with neutral factors.
type ReferenceVBandComponent uint32

const (
	ReferenceVBandSolarGeometry ReferenceVBandComponent = 1 << iota
	ReferenceVBandSourcePhotonRate
	ReferenceVBandSkyPhotonRadiance
	ReferenceVBandNoiseEquivalentPSFSolidAngle
	ReferenceVBandBenchmarkEfficiency
)

// ReferenceVBandRequiredComponents is the complete input contract. Consumers
// can use it to construct explicit availability masks without duplicating the
// list of required physical inputs.
const ReferenceVBandRequiredComponents = ReferenceVBandSolarGeometry |
	ReferenceVBandSourcePhotonRate |
	ReferenceVBandSkyPhotonRadiance |
	ReferenceVBandNoiseEquivalentPSFSolidAngle |
	ReferenceVBandBenchmarkEfficiency

const referenceVBandPhysicalComponents = ReferenceVBandSourcePhotonRate |
	ReferenceVBandSkyPhotonRadiance |
	ReferenceVBandNoiseEquivalentPSFSolidAngle

// Has reports whether all requested components are present in the mask.
func (components ReferenceVBandComponent) Has(requested ReferenceVBandComponent) bool {
	return components&requested == requested
}

// Names returns stable machine-readable names for diagnostics and user-facing
// adapters. Unknown bits are intentionally omitted; Compute rejects them.
func (components ReferenceVBandComponent) Names() []string {
	names := make([]string, 0, bits.OnesCount32(uint32(components)))
	for _, component := range []struct {
		mask ReferenceVBandComponent
		name string
	}{
		{ReferenceVBandSolarGeometry, "solar_geometry"},
		{ReferenceVBandSourcePhotonRate, "source_photon_rate"},
		{ReferenceVBandSkyPhotonRadiance, "sky_photon_radiance"},
		{ReferenceVBandNoiseEquivalentPSFSolidAngle, "noise_equivalent_psf_solid_angle"},
		{ReferenceVBandBenchmarkEfficiency, "benchmark_efficiency"},
	} {
		if components.Has(component.mask) {
			names = append(names, component.name)
		}
	}
	return names
}

type ReferenceVBandDataStatus string

const (
	ReferenceVBandDataUnavailable ReferenceVBandDataStatus = "unavailable"
	ReferenceVBandDataPartial     ReferenceVBandDataStatus = "partial"
	ReferenceVBandDataComplete    ReferenceVBandDataStatus = "complete"
)

// ReferenceVBandProvenance identifies the independently computed inputs. The
// fixed passband and source spectrum are part of the contract constants above;
// these fields identify the algorithms, data runs, and benchmark actually used
// to produce the pre-integrated values.
type ReferenceVBandProvenance struct {
	SolarGeometry           string `json:"solar_geometry,omitempty"`
	AtmosphericTransmission string `json:"atmospheric_transmission,omitempty"`
	SkyRadiance             string `json:"sky_radiance,omitempty"`
	AtmosphericPSF          string `json:"atmospheric_psf,omitempty"`
	Benchmark               string `json:"benchmark,omitempty"`
	PassbandResponse        string `json:"passband_response,omitempty"`
}

// ReferenceVBandInput contains telescope-independent quantities already
// integrated over the contract passband.
//
// SourcePhotonRate is the passband-weighted photon rate per square metre at the
// entrance aperture for the contract's flat-f_nu, AB=0 source after atmospheric
// transmission. Telescope and detector throughput are deliberately excluded.
// SkyPhotonRadiance is the corresponding sky rate per square metre and square
// arcsecond. NoiseEquivalentPSFSolidAngleArcsec2 is
// 1/integral(PSF^2 dΩ) for a unit-normalized, band-integrated atmospheric PSF.
// Their combination
//
//	G = S^2 / (B * Omega_NEA)
//
// is proportional to SNR^2 per exposure time in the declared faint-source,
// background-limited regime. BenchmarkEfficiency must use exactly the same
// units, passband, source normalization, and PSF-area convention.
type ReferenceVBandInput struct {
	ValidAt                             time.Time                `json:"valid_at"`
	SunAltitudeDegrees                  float64                  `json:"sun_altitude_degrees"`
	SourcePhotonRate                    float64                  `json:"source_photon_rate"`
	SkyPhotonRadiance                   float64                  `json:"sky_photon_radiance"`
	NoiseEquivalentPSFSolidAngleArcsec2 float64                  `json:"noise_equivalent_psf_solid_angle_arcsec2"`
	BenchmarkEfficiency                 float64                  `json:"benchmark_efficiency"`
	AvailableComponents                 ReferenceVBandComponent  `json:"available_components"`
	Provenance                          ReferenceVBandProvenance `json:"provenance"`
}

// ReferenceVBandResult keeps applicability, physical efficiency, and the
// normalized presentation score distinct. Available means that the final
// exposure multiplier and 1..10 mapping are both scientifically supported.
// EfficiencyAvailable may still be true when only the benchmark is missing.
type ReferenceVBandResult struct {
	ValidAt                             time.Time                `json:"valid_at"`
	ContractVersion                     string                   `json:"contract_version"`
	PassbandID                          string                   `json:"passband_id"`
	SourceSpectrum                      string                   `json:"source_spectrum"`
	Direction                           string                   `json:"direction"`
	Regime                              string                   `json:"regime"`
	SunAltitudeDegrees                  float64                  `json:"sun_altitude_degrees"`
	ApplicabilityKnown                  bool                     `json:"applicability_known"`
	Applicable                          bool                     `json:"applicable"`
	Available                           bool                     `json:"available"`
	EfficiencyAvailable                 bool                     `json:"efficiency_available"`
	DataStatus                          ReferenceVBandDataStatus `json:"data_status"`
	Completeness                        float64                  `json:"completeness"`
	AvailableComponents                 ReferenceVBandComponent  `json:"available_components"`
	MissingComponents                   ReferenceVBandComponent  `json:"missing_components"`
	SourcePhotonRate                    float64                  `json:"source_photon_rate"`
	SkyPhotonRadiance                   float64                  `json:"sky_photon_radiance"`
	NoiseEquivalentPSFSolidAngleArcsec2 float64                  `json:"noise_equivalent_psf_solid_angle_arcsec2"`
	Efficiency                          float64                  `json:"efficiency"`
	RelativeEfficiency                  float64                  `json:"relative_efficiency"`
	ExposureTimeMultiplier              float64                  `json:"exposure_time_multiplier"`
	NoFiniteExposure                    bool                     `json:"no_finite_exposure"`
	Index                               float64                  `json:"index"`
	Provenance                          ReferenceVBandProvenance `json:"provenance"`
}

// ComputeReferenceVBandZenithEfficiency evaluates only the versioned reference
// observation. It does not consume seeing weights, cloud penalties, PWV
// thresholds, Moon phase penalties, surface wind, tau0, or any legacy Overall
// calibration. Those physical effects must already be represented exactly once
// in S, B, or the atmospheric PSF solid angle when relevant to this reference
// mode.
func ComputeReferenceVBandZenithEfficiency(input ReferenceVBandInput) (ReferenceVBandResult, error) {
	if unknown := input.AvailableComponents &^ ReferenceVBandRequiredComponents; unknown != 0 {
		return ReferenceVBandResult{}, fmt.Errorf("reference V-band input contains unknown component bits 0x%x", uint32(unknown))
	}
	if err := validateReferenceVBandInput(input); err != nil {
		return ReferenceVBandResult{}, err
	}

	availableComponents := input.AvailableComponents & ReferenceVBandRequiredComponents
	missingComponents := ReferenceVBandRequiredComponents &^ availableComponents
	providedCount := bits.OnesCount32(uint32(availableComponents))
	requiredCount := bits.OnesCount32(uint32(ReferenceVBandRequiredComponents))
	status := ReferenceVBandDataPartial
	switch providedCount {
	case 0:
		status = ReferenceVBandDataUnavailable
	case requiredCount:
		status = ReferenceVBandDataComplete
	}

	result := ReferenceVBandResult{
		ValidAt:             input.ValidAt,
		ContractVersion:     ReferenceVBandContractVersion,
		PassbandID:          ReferenceVBandPassbandID,
		SourceSpectrum:      ReferenceVBandSourceSpectrum,
		Direction:           ReferenceVBandDirection,
		Regime:              ReferenceVBandRegime,
		DataStatus:          status,
		Completeness:        float64(providedCount) / float64(requiredCount),
		AvailableComponents: availableComponents,
		MissingComponents:   missingComponents,
		Provenance:          input.Provenance,
	}
	if availableComponents.Has(ReferenceVBandSourcePhotonRate) {
		result.SourcePhotonRate = input.SourcePhotonRate
	}
	if availableComponents.Has(ReferenceVBandSolarGeometry) {
		result.SunAltitudeDegrees = input.SunAltitudeDegrees
	}
	if availableComponents.Has(ReferenceVBandSkyPhotonRadiance) {
		result.SkyPhotonRadiance = input.SkyPhotonRadiance
	}
	if availableComponents.Has(ReferenceVBandNoiseEquivalentPSFSolidAngle) {
		result.NoiseEquivalentPSFSolidAngleArcsec2 = input.NoiseEquivalentPSFSolidAngleArcsec2
	}

	result.ApplicabilityKnown = availableComponents.Has(ReferenceVBandSolarGeometry)
	result.Applicable = result.ApplicabilityKnown &&
		input.SunAltitudeDegrees < ReferenceVBandMaximumSunAltitudeDegrees
	if !result.Applicable || !availableComponents.Has(referenceVBandPhysicalComponents) {
		return result, nil
	}

	efficiency := input.SourcePhotonRate * input.SourcePhotonRate /
		(input.SkyPhotonRadiance * input.NoiseEquivalentPSFSolidAngleArcsec2)
	if !finite(efficiency) {
		return ReferenceVBandResult{}, fmt.Errorf("reference V-band efficiency is outside the finite numeric range")
	}
	if input.SourcePhotonRate > 0 && efficiency == 0 {
		return ReferenceVBandResult{}, fmt.Errorf("reference V-band efficiency underflowed the finite numeric range")
	}
	result.Efficiency = efficiency
	result.EfficiencyAvailable = true

	if !availableComponents.Has(ReferenceVBandBenchmarkEfficiency) {
		return result, nil
	}
	result.Available = true
	if efficiency == 0 {
		result.NoFiniteExposure = true
		result.Index = 1
		return result, nil
	}

	result.RelativeEfficiency = efficiency / input.BenchmarkEfficiency
	result.ExposureTimeMultiplier = input.BenchmarkEfficiency / efficiency
	if !finite(result.RelativeEfficiency) || !finite(result.ExposureTimeMultiplier) {
		return ReferenceVBandResult{}, fmt.Errorf("reference V-band normalization is outside the finite numeric range")
	}
	result.Index = 1 + 9*clampSurfaceValue(result.RelativeEfficiency, 0, 1)
	return result, nil
}

func validateReferenceVBandInput(input ReferenceVBandInput) error {
	components := input.AvailableComponents
	if components != 0 && input.ValidAt.IsZero() {
		return fmt.Errorf("reference V-band validity time is required when data are available")
	}
	if components&(referenceVBandPhysicalComponents|ReferenceVBandBenchmarkEfficiency) != 0 && input.Provenance.PassbandResponse == "" {
		return fmt.Errorf("reference V-band passband-response provenance is required")
	}
	if components.Has(ReferenceVBandSolarGeometry) {
		if !finite(input.SunAltitudeDegrees) || input.SunAltitudeDegrees < -90 || input.SunAltitudeDegrees > 90 {
			return fmt.Errorf("reference V-band solar altitude must be finite and between -90 and 90 degrees")
		}
		if input.Provenance.SolarGeometry == "" {
			return fmt.Errorf("reference V-band solar geometry provenance is required")
		}
	}
	if components.Has(ReferenceVBandSourcePhotonRate) {
		if !finite(input.SourcePhotonRate) || input.SourcePhotonRate < 0 {
			return fmt.Errorf("reference V-band source photon rate must be finite and non-negative")
		}
		if input.Provenance.AtmosphericTransmission == "" {
			return fmt.Errorf("reference V-band atmospheric-transmission provenance is required")
		}
	}
	if components.Has(ReferenceVBandSkyPhotonRadiance) {
		if !finite(input.SkyPhotonRadiance) || input.SkyPhotonRadiance <= 0 {
			return fmt.Errorf("reference V-band sky photon radiance must be finite and positive")
		}
		if input.Provenance.SkyRadiance == "" {
			return fmt.Errorf("reference V-band sky-radiance provenance is required")
		}
	}
	if components.Has(ReferenceVBandNoiseEquivalentPSFSolidAngle) {
		if !finite(input.NoiseEquivalentPSFSolidAngleArcsec2) || input.NoiseEquivalentPSFSolidAngleArcsec2 <= 0 {
			return fmt.Errorf("reference V-band noise-equivalent PSF solid angle must be finite and positive")
		}
		if input.Provenance.AtmosphericPSF == "" {
			return fmt.Errorf("reference V-band atmospheric-PSF provenance is required")
		}
	}
	if components.Has(ReferenceVBandBenchmarkEfficiency) {
		if !finite(input.BenchmarkEfficiency) || input.BenchmarkEfficiency <= 0 {
			return fmt.Errorf("reference V-band benchmark efficiency must be finite and positive")
		}
		if input.Provenance.Benchmark == "" {
			return fmt.Errorf("reference V-band benchmark provenance is required")
		}
	}
	return nil
}
