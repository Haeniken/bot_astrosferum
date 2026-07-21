package astronomy

import (
	"math"
	"time"

	"github.com/soniakeys/meeus/v3/base"
	"github.com/soniakeys/meeus/v3/julian"
	"github.com/soniakeys/meeus/v3/nutation"
	"github.com/soniakeys/meeus/v3/planetelements"
	"github.com/soniakeys/meeus/v3/solar"
)

const (
	planetJupiter     = planetelements.Jupiter
	planetSaturn      = planetelements.Saturn
	lightTimeDayPerAU = 0.0057755183
)

func planetEventsForLocalDay(date time.Time, latitude, longitude float64, planet int) horizonEvents {
	return findHorizonEvents(date, date.AddDate(0, 0, 1), func(at time.Time) float64 {
		// Standard stellar/planetary horizon: centre at −34 arcminutes,
		// accounting for average near-horizon refraction.
		return geometricAltitude(at, latitude, longitude, planetCoordinates(at, planet))/degree + 34.0/60.0
	})
}

func planetCoordinates(at time.Time, planet int) equatorial {
	jde := julian.TimeToJD(at) + deltaTSeconds(at)/86400
	earth := heliocentricXYZ(jde, planetelements.Earth)
	target := heliocentricXYZ(jde, planet)
	distance := vectorDistance(target, earth)
	// One light-time iteration is ample at the precision of mean orbital
	// elements and measurably improves event timing for the outer planets.
	target = heliocentricXYZ(jde-distance*lightTimeDayPerAU, planet)
	x, y, z := target.x-earth.x, target.y-earth.y, target.z-earth.z
	distance = math.Sqrt(x*x + y*y + z*z)
	epsilon := nutation.MeanObliquity(jde).Rad()
	equatorialY := y*math.Cos(epsilon) - z*math.Sin(epsilon)
	equatorialZ := y*math.Sin(epsilon) + z*math.Cos(epsilon)
	return equatorial{ra: math.Atan2(equatorialY, x), dec: math.Asin(equatorialZ / distance), distance: distance}
}

type vector3 struct{ x, y, z float64 }

func heliocentricXYZ(jde float64, planet int) vector3 {
	if planet == planetelements.Earth {
		centuries := base.J2000Century(jde)
		sunLongitude, _ := solar.True(centuries)
		longitude := sunLongitude.Rad() + math.Pi
		radius := solar.Radius(centuries)
		return vector3{x: radius * math.Cos(longitude), y: radius * math.Sin(longitude)}
	}
	var elements planetelements.Elements
	planetelements.Mean(planet, jde, &elements)
	meanAnomaly := normalizeRadians(elements.Lon.Rad() - elements.Peri.Rad())
	eccentricAnomaly := meanAnomaly
	for range 8 {
		eccentricAnomaly -= (eccentricAnomaly - elements.Ecc*math.Sin(eccentricAnomaly) - meanAnomaly) / (1 - elements.Ecc*math.Cos(eccentricAnomaly))
	}
	xOrbital := elements.Axis * (math.Cos(eccentricAnomaly) - elements.Ecc)
	yOrbital := elements.Axis * math.Sqrt(1-elements.Ecc*elements.Ecc) * math.Sin(eccentricAnomaly)
	trueLongitude := math.Atan2(yOrbital, xOrbital) + elements.Peri.Rad()
	radius := math.Hypot(xOrbital, yOrbital)
	node, inclination := elements.Node.Rad(), elements.Inc.Rad()
	argument := trueLongitude - node
	return vector3{
		x: radius * (math.Cos(node)*math.Cos(argument) - math.Sin(node)*math.Sin(argument)*math.Cos(inclination)),
		y: radius * (math.Sin(node)*math.Cos(argument) + math.Cos(node)*math.Sin(argument)*math.Cos(inclination)),
		z: radius * math.Sin(argument) * math.Sin(inclination),
	}
}

func vectorDistance(left, right vector3) float64 {
	return math.Sqrt((left.x-right.x)*(left.x-right.x) + (left.y-right.y)*(left.y-right.y) + (left.z-right.z)*(left.z-right.z))
}

func normalizeRadians(value float64) float64 {
	value = math.Mod(value, 2*math.Pi)
	if value < 0 {
		value += 2 * math.Pi
	}
	return value
}
