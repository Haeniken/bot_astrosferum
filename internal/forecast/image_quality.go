package forecast

import (
	"fmt"
	"math"
)

const (
	kastenYoungHorizonCorrection = 0.50572
	kastenYoungAltitudeOffsetDeg = 6.07995
	kastenYoungExponent          = -1.6364
	seeingReferenceWavelengthNM  = 500.0
)

// KastenYoungRelativeOpticalAirmass returns the relative optical air mass for
// an apparent altitude above the astronomical horizon. It is the unmodified
// Kasten-Young (1989) approximation
//
//	X = 1 / (sin(h) + 0.50572*(h + 6.07995 deg)^(-1.6364)).
//
// Kasten and Young, Applied Optics 28, 4735-4738 (1989),
// DOI 10.1364/AO.28.004735. The input is deliberately limited to the closed
// above-horizon interval [0, 90] degrees; callers must not extrapolate the fit
// to objects below the apparent horizon. The published approximation gives
// X=0.9997... rather than exactly 1 at 90 degrees, so this function does not
// silently renormalize it.
func KastenYoungRelativeOpticalAirmass(apparentAltitudeDegrees float64) (float64, error) {
	if !finite(apparentAltitudeDegrees) || apparentAltitudeDegrees < 0 || apparentAltitudeDegrees > 90 {
		return 0, fmt.Errorf("apparent altitude must be finite and between 0 and 90 degrees")
	}
	altitudeRadians := apparentAltitudeDegrees * math.Pi / 180
	denominator := math.Sin(altitudeRadians) + kastenYoungHorizonCorrection*
		math.Pow(apparentAltitudeDegrees+kastenYoungAltitudeOffsetDeg, kastenYoungExponent)
	airmass := 1 / denominator
	if !finite(airmass) || airmass <= 0 {
		return 0, fmt.Errorf("Kasten-Young relative optical air mass is outside the finite physical range")
	}
	return airmass, nil
}

// ScaleZenithSeeingFWHMArcsec transfers long-exposure atmospheric seeing from
// 500 nm at unit air mass to a requested wavelength and relative optical air
// mass:
//
//	epsilon(lambda, X) = epsilon_500 * X^(3/5) * (lambda/500 nm)^(-1/5).
//
// The exponents follow the Kolmogorov/Fried relation epsilon=0.98*lambda/r0,
// r0 proportional to lambda^(6/5), and a line-of-sight turbulence integral
// proportional to X. See Fried, JOSA 56(10), 1372-1379 (1966),
// DOI 10.1364/JOSA.56.001372. This is also the atmospheric term used by ESO
// when translating zenith seeing to observing wavelength and air mass. It is
// not a substitute for integrating a resolved slant-path Cn2 profile.
func ScaleZenithSeeingFWHMArcsec(seeing500ZenithArcsec, wavelengthNM, relativeOpticalAirmass float64) (float64, error) {
	if !finite(seeing500ZenithArcsec) || seeing500ZenithArcsec <= 0 {
		return 0, fmt.Errorf("zenith seeing at 500 nm must be finite and positive")
	}
	if !finite(wavelengthNM) || wavelengthNM <= 0 {
		return 0, fmt.Errorf("observing wavelength must be finite and positive")
	}
	if !finite(relativeOpticalAirmass) || relativeOpticalAirmass <= 0 {
		return 0, fmt.Errorf("relative optical air mass must be finite and positive")
	}
	seeing := seeing500ZenithArcsec * math.Pow(relativeOpticalAirmass, 3.0/5.0) *
		math.Pow(wavelengthNM/seeingReferenceWavelengthNM, -1.0/5.0)
	if !finite(seeing) || seeing <= 0 {
		return 0, fmt.Errorf("scaled atmospheric seeing is outside the finite physical range")
	}
	return seeing, nil
}

// DeliveredImageQualityFWHMArcsec combines atmospheric FWHM with a supplied
// non-atmospheric equivalent-Gaussian FWHM by quadrature. This is exact for
// the convolution of independent circular Gaussian PSFs and is a useful
// explicitly labelled approximation for comparable seeing-limited blur terms:
//
//	FWHM_delivered = hypot(FWHM_atmosphere, FWHM_non-atmosphere).
//
// It must not be used to collapse an AO core/halo PSF, an Airy pattern,
// asymmetric tracking, or field-dependent aberrations into a Gaussian; those
// cases require the actual PSF or encircled-energy model. A zero
// non-atmospheric term leaves the atmospheric FWHM unchanged.
func DeliveredImageQualityFWHMArcsec(atmosphericFWHMArcsec, nonAtmosphericFWHMArcsec float64) (float64, error) {
	if !finite(atmosphericFWHMArcsec) || atmosphericFWHMArcsec <= 0 {
		return 0, fmt.Errorf("atmospheric FWHM must be finite and positive")
	}
	if !finite(nonAtmosphericFWHMArcsec) || nonAtmosphericFWHMArcsec < 0 {
		return 0, fmt.Errorf("non-atmospheric FWHM must be finite and non-negative")
	}
	delivered := math.Hypot(atmosphericFWHMArcsec, nonAtmosphericFWHMArcsec)
	if !finite(delivered) || delivered <= 0 {
		return 0, fmt.Errorf("delivered image quality is outside the finite physical range")
	}
	return delivered, nil
}
