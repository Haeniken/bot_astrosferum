package astronomy

import (
	"fmt"
	"math"
	"time"

	"bot_astrosferum/internal/forecast"

	"github.com/soniakeys/meeus/v3/apparent"
	"github.com/soniakeys/meeus/v3/base"
	"github.com/soniakeys/meeus/v3/coord"
	"github.com/soniakeys/meeus/v3/julian"
	"github.com/soniakeys/meeus/v3/pluto"
	"github.com/soniakeys/unit"
)

const (
	lightTimeDayPerAU  = 0.0057755183
	astronomicalUnitKM = 149597870.7
)

type jplElements struct {
	axis, eccentricity, inclination, longitude, perihelion, node float64
	axisRate, eccentricityRate, inclinationRate                  float64
	longitudeRate, perihelionRate, nodeRate                      float64
	b, c, s, f                                                   float64
}

// Short-interval coefficients are JPL's fitted 1800--2050 approximation.
var jplShortElements = map[CelestialBody]jplElements{
	CelestialMercury: {0.38709927, 0.20563593, 7.00497902, 252.25032350, 77.45779628, 48.33076593, 0.00000037, 0.00001906, -0.00594749, 149472.67411175, 0.16047689, -0.12534081, 0, 0, 0, 0},
	CelestialVenus:   {0.72333566, 0.00677672, 3.39467605, 181.97909950, 131.60246718, 76.67984255, 0.00000390, -0.00004107, -0.00078890, 58517.81538729, 0.00268329, -0.27769418, 0, 0, 0, 0},
	celestialEarth:   {1.00000261, 0.01671123, -0.00001531, 100.46457166, 102.93768193, 0, 0.00000562, -0.00004392, -0.01294668, 35999.37244981, 0.32327364, 0, 0, 0, 0, 0},
	CelestialMars:    {1.52371034, 0.09339410, 1.84969142, -4.55343205, -23.94362959, 49.55953891, 0.00001847, 0.00007882, -0.00813131, 19140.30268499, 0.44441088, -0.29257343, 0, 0, 0, 0},
	CelestialJupiter: {5.20288700, 0.04838624, 1.30439695, 34.39644051, 14.72847983, 100.47390909, -0.00011607, -0.00013253, -0.00183714, 3034.74612775, 0.21252668, 0.20469106, 0, 0, 0, 0},
	CelestialSaturn:  {9.53667594, 0.05386179, 2.48599187, 49.95424423, 92.59887831, 113.66242448, -0.00125060, -0.00050991, 0.00193609, 1222.49362201, -0.41897216, -0.28867794, 0, 0, 0, 0},
	CelestialUranus:  {19.18916464, 0.04725744, 0.77263783, 313.23810451, 170.95427630, 74.01692503, -0.00196176, -0.00004397, -0.00242939, 428.48202785, 0.40805281, 0.04240589, 0, 0, 0, 0},
	CelestialNeptune: {30.06992276, 0.00859048, 1.77004347, -55.12002969, 44.96476227, 131.78422574, 0.00026291, 0.00005105, 0.00035372, 218.45945325, -0.32241464, -0.00508664, 0, 0, 0, 0},
}

func planetEventsForLocalDay(date time.Time, location forecast.Location, body CelestialBody) horizonEvents {
	return findHorizonEvents(date, date.AddDate(0, 0, 1), func(at time.Time) float64 {
		coordinates, err := planetCoordinates(at, body)
		if err != nil {
			return math.NaN()
		}
		// Standard stellar/planetary horizon: topocentric centre at -34
		// arcminutes, accounting for average near-horizon refraction.
		altitude, _ := topocentricHorizontal(at, location, coordinates)
		return altitude/degree + 34.0/60.0
	})
}

func planetCoordinates(at time.Time, body CelestialBody) (equatorial, error) {
	if !body.isPlanet() {
		return equatorial{}, fmt.Errorf("%q is not a supported planet", body)
	}
	geometry, err := planetGeocentricGeometry(at, body)
	if err != nil {
		return equatorial{}, err
	}
	x, y, z := geometry.earthToTarget.x, geometry.earthToTarget.y, geometry.earthToTarget.z
	distance := geometry.distanceAU
	if !finite(distance) || distance <= 0 {
		return equatorial{}, fmt.Errorf("%s geocentric distance is invalid", body)
	}

	// JPL's fitted vectors and the Meeus Pluto series are J2000 ecliptic.
	// Rotate into J2000 equatorial coordinates, then apply precession,
	// nutation and annual aberration to obtain an apparent place of date.
	yEq := y*base.COblJ2000 - z*base.SOblJ2000
	zEq := y*base.SOblJ2000 + z*base.COblJ2000
	from := coord.Equatorial{RA: unit.RAFromRad(math.Atan2(yEq, x)), Dec: unit.Angle(math.Asin(zEq / distance))}
	to := coord.Equatorial{}
	apparent.Position(&from, &to, 2000, base.JDEToJulianYear(geometry.receptionJDE), 0, 0)
	return equatorial{ra: to.RA.Rad(), dec: to.Dec.Rad(), distance: distance * astronomicalUnitKM}, nil
}

type planetGeometry struct {
	earthToTarget vector3
	distanceAU    float64
	receptionJDE  float64
	emissionJDE   float64
}

func planetGeocentricGeometry(at time.Time, body CelestialBody) (planetGeometry, error) {
	if !body.isPlanet() {
		return planetGeometry{}, fmt.Errorf("%q is not a supported planet", body)
	}
	jde := julian.TimeToJD(at) + deltaTSeconds(at)/86400
	earth, err := jplHeliocentricXYZ(jde, celestialEarth)
	if err != nil {
		return planetGeometry{}, err
	}
	target, err := celestialHeliocentricXYZ(jde, body)
	if err != nil {
		return planetGeometry{}, err
	}
	distance := vectorDistance(target, earth)
	emissionJDE := jde - distance*lightTimeDayPerAU
	target, err = celestialHeliocentricXYZ(emissionJDE, body)
	if err != nil {
		return planetGeometry{}, err
	}
	vector := vector3{x: target.x - earth.x, y: target.y - earth.y, z: target.z - earth.z}
	distance = math.Sqrt(vector.x*vector.x + vector.y*vector.y + vector.z*vector.z)
	if !finite(distance) || distance <= 0 {
		return planetGeometry{}, fmt.Errorf("%s geocentric distance is invalid", body)
	}
	return planetGeometry{earthToTarget: vector, distanceAU: distance, receptionJDE: jde, emissionJDE: emissionJDE}, nil
}

func saturnRingOpeningDegrees(at time.Time) (float64, error) {
	geometry, err := planetGeocentricGeometry(at, CelestialSaturn)
	if err != nil {
		return 0, err
	}
	// IAU Saturn north-pole direction in ICRF/J2000 coordinates.  The
	// observer direction is Saturn -> Earth, the negative of the light-time
	// corrected Earth -> Saturn vector used for the apparent place.
	centuries := base.J2000Century(geometry.emissionJDE)
	poleRA := (40.589 - 0.036*centuries) * degree
	poleDec := (83.537 - 0.004*centuries) * degree
	pole := vector3{
		x: math.Cos(poleDec) * math.Cos(poleRA),
		y: math.Cos(poleDec) * math.Sin(poleRA),
		z: math.Sin(poleDec),
	}
	v := geometry.earthToTarget
	observer := vector3{
		x: -v.x / geometry.distanceAU,
		y: -(v.y*base.COblJ2000 - v.z*base.SOblJ2000) / geometry.distanceAU,
		z: -(v.y*base.SOblJ2000 + v.z*base.COblJ2000) / geometry.distanceAU,
	}
	dot := pole.x*observer.x + pole.y*observer.y + pole.z*observer.z
	return math.Asin(math.Max(-1, math.Min(1, dot))) / degree, nil
}

func celestialHeliocentricXYZ(jde float64, body CelestialBody) (vector3, error) {
	if body == CelestialPluto {
		longitude, latitude, radius := pluto.Heliocentric(jde)
		cosLatitude := math.Cos(latitude.Rad())
		return vector3{
			x: radius * cosLatitude * math.Cos(longitude.Rad()),
			y: radius * cosLatitude * math.Sin(longitude.Rad()),
			z: radius * math.Sin(latitude.Rad()),
		}, nil
	}
	return jplHeliocentricXYZ(jde, body)
}

func jplHeliocentricXYZ(jde float64, body CelestialBody) (vector3, error) {
	// The public planning contract is the common half-open UTC domain checked
	// by CelestialPositionAt/Compute. Always use the corresponding JPL
	// short-interval family within that contract; ΔT may move JDE a few seconds
	// across the nominal Julian-year boundary and must not silently switch the
	// coefficient family.
	elements, ok := jplShortElements[body]
	if !ok {
		return vector3{}, fmt.Errorf("JPL elements are unavailable for %q", body)
	}
	centuries := (jde - 2451545.0) / 36525
	axis := elements.axis + elements.axisRate*centuries
	eccentricity := elements.eccentricity + elements.eccentricityRate*centuries
	inclination := (elements.inclination + elements.inclinationRate*centuries) * degree
	longitude := elements.longitude + elements.longitudeRate*centuries
	perihelion := (elements.perihelion + elements.perihelionRate*centuries) * degree
	node := (elements.node + elements.nodeRate*centuries) * degree
	meanAnomaly := longitude - (elements.perihelion + elements.perihelionRate*centuries)
	meanAnomaly += elements.b*centuries*centuries + elements.c*math.Cos(elements.f*centuries*degree) + elements.s*math.Sin(elements.f*centuries*degree)
	meanAnomaly = normalizeSignedDegrees(meanAnomaly) * degree

	eccentricAnomaly := meanAnomaly + eccentricity*math.Sin(meanAnomaly)
	for range 16 {
		delta := (meanAnomaly - (eccentricAnomaly - eccentricity*math.Sin(eccentricAnomaly))) /
			(1 - eccentricity*math.Cos(eccentricAnomaly))
		eccentricAnomaly += delta
		if math.Abs(delta) <= 1e-12 {
			break
		}
	}
	xOrbital := axis * (math.Cos(eccentricAnomaly) - eccentricity)
	yOrbital := axis * math.Sqrt(1-eccentricity*eccentricity) * math.Sin(eccentricAnomaly)
	argument := perihelion - node
	sinArgument, cosArgument := math.Sincos(argument)
	sinNode, cosNode := math.Sincos(node)
	sinInclination, cosInclination := math.Sincos(inclination)
	return vector3{
		x: (cosArgument*cosNode-sinArgument*sinNode*cosInclination)*xOrbital +
			(-sinArgument*cosNode-cosArgument*sinNode*cosInclination)*yOrbital,
		y: (cosArgument*sinNode+sinArgument*cosNode*cosInclination)*xOrbital +
			(-sinArgument*sinNode+cosArgument*cosNode*cosInclination)*yOrbital,
		z: sinArgument*sinInclination*xOrbital + cosArgument*sinInclination*yOrbital,
	}, nil
}

type vector3 struct{ x, y, z float64 }

func vectorDistance(left, right vector3) float64 {
	return math.Sqrt((left.x-right.x)*(left.x-right.x) + (left.y-right.y)*(left.y-right.y) + (left.z-right.z)*(left.z-right.z))
}

func normalizeSignedDegrees(value float64) float64 {
	value = math.Mod(value+180, 360)
	if value < 0 {
		value += 360
	}
	return value - 180
}
