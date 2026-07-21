package astronomy

// The position and illumination calculations in this file use the Meeus
// algorithms implemented by github.com/soniakeys/meeus/v3 (MIT). Rise/set
// detection is deliberately kept here: it scans the exact local civil day
// and refines every horizon crossing by bisection, so timezone and DST rules
// remain under the application's control.

import (
	"math"
	"time"

	"github.com/soniakeys/meeus/v3/julian"
	"github.com/soniakeys/meeus/v3/moonposition"
	"github.com/soniakeys/meeus/v3/nutation"
	"github.com/soniakeys/meeus/v3/solar"
)

const (
	degree      = math.Pi / 180
	earthRadius = 6378.14
	sunDistance = 149598000.0
)

type equatorial struct {
	ra, dec, distance float64
}

type horizonEvents struct {
	rise, set            time.Time
	alwaysUp, alwaysDown bool
}

type illumination struct {
	phase, fraction float64
}

func eventsForLocalDay(date time.Time, latitude, longitude float64) (horizonEvents, horizonEvents) {
	end := date.AddDate(0, 0, 1)
	sun := findHorizonEvents(date, end, func(at time.Time) float64 {
		// -0.833 degrees is the standard apparent sunrise/set altitude,
		// including average refraction and the solar semidiameter.
		return sunGeometricAltitude(at, latitude, longitude)/degree + 0.833
	})
	moon := findHorizonEvents(date, end, func(at time.Time) float64 {
		return moonUpperLimbAltitude(at, latitude, longitude)
	})
	return sun, moon
}

func findHorizonEvents(start, end time.Time, height func(time.Time) float64) horizonEvents {
	const step = 5 * time.Minute
	previousTime := start
	previous := height(previousTime)
	minimum, maximum := previous, previous
	result := horizonEvents{}
	for at := start.Add(step); !at.After(end); at = at.Add(step) {
		value := height(at)
		minimum = math.Min(minimum, value)
		maximum = math.Max(maximum, value)
		if previous <= 0 && value > 0 && result.rise.IsZero() {
			result.rise = refineCrossing(previousTime, at, height).In(start.Location())
		}
		if previous >= 0 && value < 0 && result.set.IsZero() {
			result.set = refineCrossing(previousTime, at, height).In(start.Location())
		}
		previousTime, previous = at, value
	}
	if result.rise.IsZero() && result.set.IsZero() {
		result.alwaysUp = minimum > 0
		result.alwaysDown = maximum <= 0
	}
	return result
}

func refineCrossing(low, high time.Time, height func(time.Time) float64) time.Time {
	lowValue := height(low)
	for high.Sub(low) > time.Second {
		mid := low.Add(high.Sub(low) / 2)
		midValue := height(mid)
		if (lowValue <= 0 && midValue <= 0) || (lowValue >= 0 && midValue >= 0) {
			low, lowValue = mid, midValue
		} else {
			high = mid
		}
	}
	return low.Add(high.Sub(low) / 2)
}

func sunGeometricAltitude(at time.Time, latitude, longitude float64) float64 {
	coordinates := sunCoordinates(at)
	return geometricAltitude(at, latitude, longitude, coordinates)
}

func moonUpperLimbAltitude(at time.Time, latitude, longitude float64) float64 {
	coordinates := moonCoordinates(at)
	hourAngle := localHourAngle(at, longitude, coordinates.ra)
	geocentric := altitude(hourAngle, latitude*degree, coordinates.dec)
	// Topocentric parallax lowers the lunar centre along its vertical circle.
	topocentric := geocentric - math.Asin(earthRadius/coordinates.distance*math.Cos(geocentric))
	apparent := topocentric + atmosphericRefraction(topocentric)
	semidiameter := 0.2725 * math.Asin(earthRadius/coordinates.distance)
	// Residual 0.09 degree horizon correction follows the validated SunCalc
	// v2 upper-limb convention used against USNO event tables.
	return (apparent+semidiameter)/degree + 0.09
}

func geometricAltitude(at time.Time, latitude, longitude float64, coordinates equatorial) float64 {
	return altitude(localHourAngle(at, longitude, coordinates.ra), latitude*degree, coordinates.dec)
}

func localHourAngle(at time.Time, longitude, rightAscension float64) float64 {
	days := julian.TimeToJD(at) - 2451545.0
	sidereal := degree * (280.46061837 + 360.98564736629*days)
	return sidereal + longitude*degree - rightAscension
}

func altitude(hourAngle, latitude, declination float64) float64 {
	return math.Asin(math.Sin(latitude)*math.Sin(declination) +
		math.Cos(latitude)*math.Cos(declination)*math.Cos(hourAngle))
}

func atmosphericRefraction(altitude float64) float64 {
	if altitude < 0 {
		altitude = 0
	}
	return 0.0002967 / math.Tan(altitude+0.00312536/(altitude+0.08901179))
}

func sunCoordinates(at time.Time) equatorial {
	jde := julian.TimeToJD(at) + deltaTSeconds(at)/86400
	ra, dec := solar.ApparentEquatorial(jde)
	return equatorial{ra: ra.Rad(), dec: dec.Rad(), distance: sunDistance}
}

func moonCoordinates(at time.Time) equatorial {
	jde := julian.TimeToJD(at) + deltaTSeconds(at)/86400
	longitude, latitude, distance := moonposition.Position(jde)
	deltaLongitude, deltaObliquity := nutation.Nutation(jde)
	obliquity := nutation.MeanObliquity(jde) + deltaObliquity
	lambda := longitude.Rad() + deltaLongitude.Rad()
	beta := latitude.Rad()
	epsilon := obliquity.Rad()
	ra := math.Atan2(math.Sin(lambda)*math.Cos(epsilon)-math.Tan(beta)*math.Sin(epsilon), math.Cos(lambda))
	dec := math.Asin(math.Sin(beta)*math.Cos(epsilon) + math.Cos(beta)*math.Sin(epsilon)*math.Sin(lambda))
	return equatorial{ra: ra, dec: dec, distance: distance}
}

func moonIllumination(at time.Time) illumination {
	sun := sunCoordinates(at)
	moon := moonCoordinates(at)
	separation := math.Acos(math.Sin(sun.dec)*math.Sin(moon.dec) +
		math.Cos(sun.dec)*math.Cos(moon.dec)*math.Cos(sun.ra-moon.ra))
	incidence := math.Atan2(sunDistance*math.Sin(separation), moon.distance-sunDistance*math.Cos(separation))
	brightLimb := math.Atan2(math.Cos(sun.dec)*math.Sin(sun.ra-moon.ra),
		math.Sin(sun.dec)*math.Cos(moon.dec)-math.Cos(sun.dec)*math.Sin(moon.dec)*math.Cos(sun.ra-moon.ra))
	phaseSign := 1.0
	if brightLimb < 0 {
		phaseSign = -1
	}
	return illumination{
		fraction: (1 + math.Cos(incidence)) / 2,
		phase:    0.5 + 0.5*incidence*phaseSign/math.Pi,
	}
}

// Espenak & Meeus polynomial fits for TT−UT, valid for the application's
// practical forecast dates and retained locally to avoid a second time API.
func deltaTSeconds(at time.Time) float64 {
	year := float64(at.UTC().Year()) + float64(at.UTC().YearDay()-1)/365.2425
	var t float64
	switch {
	case year < 1920:
		t = year - 1900
		return -2.79 + t*(1.494119+t*(-0.0598939+t*(0.0061966-t*0.000197)))
	case year < 1941:
		t = year - 1920
		return 21.20 + t*(0.84493+t*(-0.076100+t*0.0020936))
	case year < 1961:
		t = year - 1950
		return 29.07 + t*(0.407+t*(-1.0/233+t/2547))
	case year < 1986:
		t = year - 1975
		return 45.45 + t*(1.067+t*(-1.0/260-t/718))
	case year < 2005:
		t = year - 2000
		return 63.86 + t*(0.3345+t*(-0.060374+t*(0.0017275+t*(0.000651814+t*0.00002373599))))
	case year < 2050:
		t = year - 2000
		return 62.92 + t*(0.32217+t*0.005589)
	default:
		t = (year - 1820) / 100
		return -20 + 32*t*t - 0.5628*(2150-year)
	}
}
