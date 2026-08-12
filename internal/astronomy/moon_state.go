package astronomy

import (
	"math"
	"time"

	"bot_astrosferum/internal/forecast"
)

const earthFlattening = 1 / 298.257

// MoonState describes the Moon at one instant for an observer at the supplied
// coordinates. The geometric altitude and zenith distance are topocentric and
// include lunar parallax. ApparentAltitudeDegrees additionally uses the same
// standard refraction approximation as the lunar rise/set calculation; it is
// an indicative sea-level correction rather than a local weather measurement.
// AzimuthDegrees is measured clockwise from true north.
type MoonState struct {
	Time                                time.Time `json:"time"`
	TopocentricGeometricAltitudeDegrees float64   `json:"topocentric_geometric_altitude_degrees"`
	ApparentAltitudeDegrees             float64   `json:"apparent_altitude_degrees"`
	AzimuthDegrees                      float64   `json:"azimuth_degrees"`
	TopocentricZenithDistanceDegrees    float64   `json:"topocentric_zenith_distance_degrees"`
	IlluminatedFraction                 float64   `json:"illuminated_fraction"`
	PhaseAngleDegrees                   float64   `json:"phase_angle_degrees"`
	EarthMoonDistanceKM                 float64   `json:"geocentric_earth_moon_distance_km"`
	Valid                               bool      `json:"valid"`
}

// MoonStateAt computes an hourly-useful lunar ephemeris at an arbitrary
// instant and geographic position. The observer is placed on the reference
// ellipsoid at zero elevation because forecast.Location contains no site
// elevation. Invalid coordinates or a zero time return Valid=false.
func MoonStateAt(location forecast.Location, at time.Time) MoonState {
	state := MoonState{Time: at}
	if at.IsZero() || forecast.ValidateCoordinates(location.Latitude, location.Longitude) != nil {
		return state
	}

	coordinates := moonCoordinates(at)
	if !finite(coordinates.ra) || !finite(coordinates.dec) || !finite(coordinates.distance) ||
		coordinates.distance <= earthRadius {
		return state
	}

	altitude, azimuth := topocentricHorizontal(
		at,
		location.Latitude*degree,
		location.Longitude,
		coordinates,
	)
	phase := moonIllumination(at)
	apparentAltitude := altitude + atmosphericRefraction(altitude)

	state.TopocentricGeometricAltitudeDegrees = altitude / degree
	state.ApparentAltitudeDegrees = apparentAltitude / degree
	state.AzimuthDegrees = azimuth / degree
	state.TopocentricZenithDistanceDegrees = 90 - state.TopocentricGeometricAltitudeDegrees
	state.IlluminatedFraction = phase.fraction
	state.PhaseAngleDegrees = phase.phaseAngle / degree
	state.EarthMoonDistanceKM = coordinates.distance
	state.Valid = finite(state.TopocentricGeometricAltitudeDegrees) &&
		finite(state.ApparentAltitudeDegrees) &&
		finite(state.AzimuthDegrees) &&
		finite(state.TopocentricZenithDistanceDegrees) &&
		finite(state.IlluminatedFraction) &&
		finite(state.PhaseAngleDegrees)
	return state
}

func finite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}
