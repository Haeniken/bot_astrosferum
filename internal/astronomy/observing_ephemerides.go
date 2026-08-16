package astronomy

import (
	"fmt"
	"math"
	"time"

	"bot_astrosferum/internal/forecast"

	"github.com/soniakeys/meeus/v3/base"
	"github.com/soniakeys/meeus/v3/julian"
)

const (
	sunEquatorialRadiusKM  = 695700.0
	moonEquatorialRadiusKM = 1737.4
	uranusFlattening       = 0.0022927
)

var planetEquatorialRadiusKM = map[CelestialBody]float64{
	CelestialMercury: 2440.53,
	CelestialVenus:   6051.8,
	CelestialMars:    3396.19,
	CelestialJupiter: 71492,
	CelestialSaturn:  60268,
	CelestialUranus:  25559,
	CelestialNeptune: 24764,
	CelestialPluto:   1188.3,
}

type MarsSeason string

type ApparentMagnitudeStatus string

const (
	MarsNorthernSpring MarsSeason = "northern_spring"
	MarsNorthernSummer MarsSeason = "northern_summer"
	MarsNorthernAutumn MarsSeason = "northern_autumn"
	MarsNorthernWinter MarsSeason = "northern_winter"

	ApparentMagnitudeAvailable                   ApparentMagnitudeStatus = "available"
	ApparentMagnitudePlanningApproximation       ApparentMagnitudeStatus = "planning_approximation"
	ApparentMagnitudeOutsidePublishedModelDomain ApparentMagnitudeStatus = "outside_published_model_domain"
)

type CelestialCulmination struct {
	CivilDate               string        `json:"civil_date"`
	DayStart                time.Time     `json:"day_start"`
	DayEndExclusive         time.Time     `json:"day_end_exclusive"`
	ValidAt                 time.Time     `json:"valid_at"`
	Body                    CelestialBody `json:"body"`
	ApparentAltitudeDegrees float64       `json:"apparent_altitude_deg"`
}

func populateCommonObservingDiagnostics(position *CelestialHorizontalPosition, at time.Time, location forecast.Location) error {
	if position == nil {
		return fmt.Errorf("celestial observing diagnostics require a position")
	}
	radius, err := celestialEquatorialRadiusKM(position.Body)
	if err != nil {
		return err
	}
	position.AngularDiameterArcsec = 2 * math.Asin(radius/position.ObserverDistanceKM) / degree * 3600

	switch position.Body {
	case CelestialSun:
		return nil
	case CelestialMoon:
		elongation, elongationErr := topocentricSolarElongationDegrees(at, location, position)
		if elongationErr != nil {
			return elongationErr
		}
		sunCoordinates := sunCoordinates(at)
		_, _, observerSunDistanceKM := topocentricHorizontalDistance(at, location, sunCoordinates)
		elongationRadians := elongation * degree
		phaseRadians := math.Atan2(
			observerSunDistanceKM*math.Sin(elongationRadians),
			position.ObserverDistanceKM-observerSunDistanceKM*math.Cos(elongationRadians),
		)
		phaseDegrees := phaseRadians / degree
		illuminatedPercent := 50 * (1 + math.Cos(phaseRadians))
		position.PhaseAngleDegrees = &phaseDegrees
		position.IlluminatedPercent = &illuminatedPercent
		position.SolarElongationDegrees = &elongation
		return nil
	default:
		if !position.Body.isPlanet() {
			return fmt.Errorf("unsupported celestial body %q", position.Body)
		}
		elongation, elongationErr := topocentricSolarElongationDegrees(at, location, position)
		if elongationErr != nil {
			return elongationErr
		}
		position.SolarElongationDegrees = &elongation
		return nil
	}
}

func populatePlanetObservingDiagnostics(position *CelestialHorizontalPosition, at time.Time, _ forecast.Location, geometry planetGeometry) error {
	phaseRadians, err := planetPhaseAngleRadians(geometry)
	if err != nil {
		return fmt.Errorf("compute %s phase angle: %w", position.Body, err)
	}
	phaseDegrees := phaseRadians / degree
	illuminatedPercent := 50 * (1 + math.Cos(phaseRadians))
	magnitude, magnitudeStatus, err := planetApparentVMagnitude(position.Body, at, geometry, phaseDegrees)
	if err != nil {
		return fmt.Errorf("compute %s apparent V magnitude: %w", position.Body, err)
	}
	position.PhaseAngleDegrees = &phaseDegrees
	position.IlluminatedPercent = &illuminatedPercent
	position.ApparentVMagnitudeStatus = magnitudeStatus
	if magnitudeStatus == ApparentMagnitudeAvailable || magnitudeStatus == ApparentMagnitudePlanningApproximation {
		position.ApparentVMagnitude = &magnitude
	}
	if position.Body == CelestialMars {
		solarLongitude := marsSolarLongitudeDegrees(at)
		season := marsSeason(solarLongitude)
		position.MarsSolarLongitudeDegrees = &solarLongitude
		position.MarsSeason = &season
	}
	return nil
}

func celestialEquatorialRadiusKM(body CelestialBody) (float64, error) {
	switch body {
	case CelestialSun:
		return sunEquatorialRadiusKM, nil
	case CelestialMoon:
		return moonEquatorialRadiusKM, nil
	default:
		radius, ok := planetEquatorialRadiusKM[body]
		if !ok || !finite(radius) || radius <= 0 {
			return 0, fmt.Errorf("equatorial radius is unavailable for %q", body)
		}
		return radius, nil
	}
}

func planetPhaseAngleRadians(geometry planetGeometry) (float64, error) {
	targetToSun := scaleVector(geometry.targetHeliocentric, -1)
	targetToObserver := scaleVector(geometry.earthToTarget, -1)
	angle, err := vectorAngle(targetToSun, targetToObserver)
	if err != nil {
		return 0, err
	}
	return angle, nil
}

func topocentricSolarElongationDegrees(at time.Time, location forecast.Location, position *CelestialHorizontalPosition) (float64, error) {
	sun := sunCoordinates(at)
	sunAltitude, sunAzimuth, _ := topocentricHorizontalDistance(at, location, sun)
	bodyAltitude := position.GeometricAltitudeDegrees * degree
	bodyAzimuth := position.AzimuthDegrees * degree
	cosine := math.Sin(bodyAltitude)*math.Sin(sunAltitude) +
		math.Cos(bodyAltitude)*math.Cos(sunAltitude)*math.Cos(bodyAzimuth-sunAzimuth)
	return math.Acos(math.Max(-1, math.Min(1, cosine))) / degree, nil
}

func planetApparentVMagnitude(body CelestialBody, at time.Time, geometry planetGeometry, phaseDegrees float64) (float64, ApparentMagnitudeStatus, error) {
	r := vectorNorm(geometry.targetHeliocentric)
	delta := geometry.distanceAU
	if !finite(r) || r <= 0 || !finite(delta) || delta <= 0 || !finite(phaseDegrees) || phaseDegrees < 0 || phaseDegrees > 180 {
		return 0, "", fmt.Errorf("invalid photometric geometry")
	}
	distanceFactor := 5 * math.Log10(r*delta)
	a := phaseDegrees
	a2 := a * a
	a3 := a2 * a
	a4 := a3 * a
	a5 := a4 * a
	a6 := a5 * a
	var magnitude float64
	status := ApparentMagnitudeAvailable
	switch body {
	case CelestialMercury:
		magnitude = -0.613 + distanceFactor +
			6.3280e-2*a - 1.6336e-3*a2 + 3.3644e-5*a3 -
			3.4265e-7*a4 + 1.6893e-9*a5 - 3.0334e-12*a6
	case CelestialVenus:
		if a > 0 && a <= 163.7 {
			magnitude = -4.384 + distanceFactor - 1.044e-3*a + 3.687e-4*a2 -
				2.814e-6*a3 + 8.938e-9*a4
		} else if a > 163.7 && a < 179 {
			magnitude = 236.05828 + distanceFactor - 2.81914*a + 8.39034e-3*a*a
		} else {
			return 0, ApparentMagnitudeOutsidePublishedModelDomain, nil
		}
	case CelestialMars:
		if a <= 50 {
			magnitude = -1.601 + distanceFactor + 0.02267*a - 0.0001302*a*a
		} else {
			magnitude = -0.367 + distanceFactor - 0.02573*a + 0.0003445*a*a
		}
		status = ApparentMagnitudePlanningApproximation
	case CelestialJupiter:
		if a <= 12 {
			magnitude = -9.395 + distanceFactor - 3.7e-4*a + 6.16e-4*a*a
		} else if a < 130 {
			fraction := a / 180
			phaseFunction := ((((-1.876*fraction+2.809)*fraction-0.062)*fraction-0.363)*fraction-1.507)*fraction + 1
			if phaseFunction <= 0 {
				return 0, "", fmt.Errorf("jupiter phase function is non-positive")
			}
			magnitude = -9.428 + distanceFactor - 2.5*math.Log10(phaseFunction)
		} else {
			return 0, ApparentMagnitudeOutsidePublishedModelDomain, nil
		}
	case CelestialSaturn:
		sunLatitude, observerLatitude, latErr := saturnSubLatitudesDegrees(geometry)
		if latErr != nil {
			return 0, "", latErr
		}
		effective := 0.0
		if sunLatitude*observerLatitude >= 0 {
			effective = math.Sqrt(math.Abs(sunLatitude * observerLatitude))
		}
		if a >= 6.5 || effective >= 27 {
			return 0, ApparentMagnitudeOutsidePublishedModelDomain, nil
		}
		sinBeta := math.Sin(effective * degree)
		magnitude = -8.914 + distanceFactor - 1.825*sinBeta + 0.026*a - 0.378*sinBeta*math.Exp(-2.25*a)
	case CelestialUranus:
		sunLatitude, observerLatitude, latErr := uranusSubLatitudesDegrees(geometry)
		if latErr != nil {
			return 0, "", latErr
		}
		meanPlanetographic := (math.Abs(uranusPlanetographicLatitude(sunLatitude)) + math.Abs(uranusPlanetographicLatitude(observerLatitude))) / 2
		// Mallama & Hilton Eq. 14 is the Earth-observer formula.  Its
		// spacecraft-only large-phase extension (Eq. 15) is not used here.
		magnitude = -7.110 + distanceFactor - 8.4e-4*meanPlanetographic
	case CelestialNeptune:
		year := float64(at.UTC().Year()) + float64(at.UTC().YearDay()-1)/365.2425
		baseMagnitude := math.Max(-7.00, math.Min(-6.89, -6.89-0.0054*(year-1980)))
		// Eq. 16 is the Earth-observer model and deliberately ignores the
		// millimagnitude phase term.  Eq. 17 describes spacecraft viewing.
		magnitude = baseMagnitude + distanceFactor
	case CelestialPluto:
		// JPL publishes V(1,0)=-1.0.  Pluto's Earth-observable phase is small;
		// the planning approximation has no independently calibrated phase term.
		magnitude = -1.0 + distanceFactor
		status = ApparentMagnitudePlanningApproximation
	default:
		return 0, "", fmt.Errorf("apparent magnitude is unavailable for %q", body)
	}
	if !finite(magnitude) {
		return 0, "", fmt.Errorf("apparent magnitude is not finite")
	}
	return magnitude, status, nil
}

func saturnSubLatitudesDegrees(geometry planetGeometry) (float64, float64, error) {
	centuries := (geometry.emissionJDE - 2451545.0) / 36525
	poleRA := (40.589 - 0.036*centuries) * degree
	poleDec := (83.537 - 0.004*centuries) * degree
	pole := vector3{x: math.Cos(poleDec) * math.Cos(poleRA), y: math.Cos(poleDec) * math.Sin(poleRA), z: math.Sin(poleDec)}
	targetToSun := eclipticToEquatorial(scaleVector(geometry.targetHeliocentric, -1))
	targetToObserver := eclipticToEquatorial(scaleVector(geometry.earthToTarget, -1))
	sunLatitude, err := subObserverLatitudeDegrees(pole, targetToSun)
	if err != nil {
		return 0, 0, err
	}
	observerLatitude, err := subObserverLatitudeDegrees(pole, targetToObserver)
	return sunLatitude, observerLatitude, err
}

func uranusSubLatitudesDegrees(geometry planetGeometry) (float64, float64, error) {
	// IAU/J2000 Uranian north pole used by the published magnitude model.
	pole := vector3{x: -0.21199958, y: -0.94155916, z: -0.26176809}
	targetToSun := eclipticToEquatorial(scaleVector(geometry.targetHeliocentric, -1))
	targetToObserver := eclipticToEquatorial(scaleVector(geometry.earthToTarget, -1))
	sunLatitude, err := subObserverLatitudeDegrees(pole, targetToSun)
	if err != nil {
		return 0, 0, err
	}
	observerLatitude, err := subObserverLatitudeDegrees(pole, targetToObserver)
	return sunLatitude, observerLatitude, err
}

func subObserverLatitudeDegrees(pole, direction vector3) (float64, error) {
	denominator := vectorNorm(pole) * vectorNorm(direction)
	if !finite(denominator) || denominator <= 0 {
		return 0, fmt.Errorf("invalid pole geometry")
	}
	dot := (pole.x*direction.x + pole.y*direction.y + pole.z*direction.z) / denominator
	return math.Asin(math.Max(-1, math.Min(1, dot))) / degree, nil
}

func uranusPlanetographicLatitude(planetocentricDegrees float64) float64 {
	axisRatio := 1 - uranusFlattening
	return math.Atan(math.Tan(planetocentricDegrees*degree)/(axisRatio*axisRatio)) / degree
}

func marsSolarLongitudeDegrees(at time.Time) float64 {
	jde := timeToEphemerisDay(at)
	days := jde - 2451545.0
	meanAnomaly := (19.3871 + 0.52402073*days) * degree
	fictionalMeanSun := 270.3871 + 0.524038496*days
	perturbers := [...]struct{ amplitude, period, phase float64 }{
		{0.0071, 2.2353, 49.409}, {0.0057, 2.7543, 168.173}, {0.0039, 1.1177, 191.837},
		{0.0037, 15.7866, 21.736}, {0.0021, 2.1354, 15.704}, {0.0020, 2.4694, 95.528},
		{0.0018, 32.8493, 49.095},
	}
	perturbation := 0.0
	for _, term := range perturbers {
		perturbation += term.amplitude * math.Cos((0.985626*days/term.period+term.phase)*degree)
	}
	equationOfCenter := (10.691+3e-7*days)*math.Sin(meanAnomaly) +
		0.623*math.Sin(2*meanAnomaly) + 0.050*math.Sin(3*meanAnomaly) +
		0.005*math.Sin(4*meanAnomaly) + 0.0005*math.Sin(5*meanAnomaly) + perturbation
	return normalizeRadians((fictionalMeanSun+equationOfCenter)*degree) / degree
}

func marsSeason(solarLongitudeDegrees float64) MarsSeason {
	switch {
	case solarLongitudeDegrees < 90:
		return MarsNorthernSpring
	case solarLongitudeDegrees < 180:
		return MarsNorthernSummer
	case solarLongitudeDegrees < 270:
		return MarsNorthernAutumn
	default:
		return MarsNorthernWinter
	}
}

func timeToEphemerisDay(at time.Time) float64 {
	return julian.TimeToJD(at) + deltaTSeconds(at)/86400
}

func validateObservingDiagnostics(position CelestialHorizontalPosition) error {
	planet := position.Body.isPlanet()
	if position.Body == CelestialSun {
		if position.PhaseAngleDegrees != nil || position.IlluminatedPercent != nil || position.ApparentVMagnitude != nil ||
			position.ApparentVMagnitudeStatus != "" || position.SolarElongationDegrees != nil {
			return fmt.Errorf("sun unexpectedly carries planet illumination diagnostics")
		}
	} else {
		if position.PhaseAngleDegrees == nil || !finite(*position.PhaseAngleDegrees) || *position.PhaseAngleDegrees < 0 || *position.PhaseAngleDegrees > 180 ||
			position.IlluminatedPercent == nil || !finite(*position.IlluminatedPercent) || *position.IlluminatedPercent < 0 || *position.IlluminatedPercent > 100 ||
			position.SolarElongationDegrees == nil || !finite(*position.SolarElongationDegrees) || *position.SolarElongationDegrees < 0 || *position.SolarElongationDegrees > 180 {
			return fmt.Errorf("%s phase/illumination/elongation diagnostics are invalid", position.Body)
		}
	}
	if planet {
		switch position.ApparentVMagnitudeStatus {
		case ApparentMagnitudeAvailable:
			if position.Body == CelestialMars || position.Body == CelestialPluto {
				return fmt.Errorf("%s must identify its planning V-magnitude approximation", position.Body)
			}
			if position.ApparentVMagnitude == nil || !finite(*position.ApparentVMagnitude) {
				return fmt.Errorf("%s apparent V magnitude is invalid", position.Body)
			}
		case ApparentMagnitudePlanningApproximation:
			if position.Body != CelestialMars && position.Body != CelestialPluto {
				return fmt.Errorf("%s unexpectedly claims an approximate V magnitude", position.Body)
			}
			if position.ApparentVMagnitude == nil || !finite(*position.ApparentVMagnitude) {
				return fmt.Errorf("%s apparent V magnitude is invalid", position.Body)
			}
		case ApparentMagnitudeOutsidePublishedModelDomain:
			if (position.Body != CelestialVenus && position.Body != CelestialJupiter && position.Body != CelestialSaturn) || position.ApparentVMagnitude != nil {
				return fmt.Errorf("%s carries an out-of-domain V magnitude", position.Body)
			}
		default:
			return fmt.Errorf("%s apparent V magnitude is invalid", position.Body)
		}
	} else if position.ApparentVMagnitude != nil || position.ApparentVMagnitudeStatus != "" {
		return fmt.Errorf("%s unexpectedly carries a planetary V magnitude", position.Body)
	}
	if position.Body == CelestialMars {
		if position.MarsSolarLongitudeDegrees == nil || !finite(*position.MarsSolarLongitudeDegrees) ||
			*position.MarsSolarLongitudeDegrees < 0 || *position.MarsSolarLongitudeDegrees >= 360 || position.MarsSeason == nil ||
			!validMarsSeason(*position.MarsSeason) {
			return fmt.Errorf("mars season diagnostics are invalid")
		}
	} else if position.MarsSolarLongitudeDegrees != nil || position.MarsSeason != nil {
		return fmt.Errorf("%s unexpectedly carries Mars season diagnostics", position.Body)
	}
	return nil
}

func validMarsSeason(value MarsSeason) bool {
	return value == MarsNorthernSpring || value == MarsNorthernSummer || value == MarsNorthernAutumn || value == MarsNorthernWinter
}

func celestialCulminations(location forecast.Location, validTimes []time.Time, body CelestialBody) ([]CelestialCulmination, error) {
	zone, err := time.LoadLocation(location.TimeZone)
	if err != nil {
		return nil, fmt.Errorf("load culmination timezone: %w", err)
	}
	first := localMidnight(validTimes[0], zone)
	last := localMidnight(validTimes[len(validTimes)-1], zone)
	culminations := make([]CelestialCulmination, 0, 4)
	for dayStart := first; !dayStart.After(last); dayStart = dayStart.AddDate(0, 0, 1) {
		dayEnd := dayStart.AddDate(0, 0, 1)
		if err := validateCelestialEphemerisInstant(dayStart); err != nil {
			return nil, err
		}
		if !dayEnd.Before(celestialEphemerisLastInstant) {
			return nil, fmt.Errorf("culmination day reaches beyond the celestial ephemeris domain")
		}
		at, altitudeDegrees, err := maximizeCelestialAltitude(dayStart, dayEnd, location, body)
		if err != nil {
			return nil, err
		}
		culminations = append(culminations, CelestialCulmination{
			CivilDate: dayStart.Format(time.DateOnly), DayStart: dayStart, DayEndExclusive: dayEnd,
			ValidAt: at, Body: body, ApparentAltitudeDegrees: altitudeDegrees,
		})
	}
	return culminations, nil
}

func maximizeCelestialAltitude(start, end time.Time, location forecast.Location, body CelestialBody) (time.Time, float64, error) {
	const step = 10 * time.Minute
	bestAt := start
	bestAltitude, err := celestialApparentAltitudeDegrees(start, location, body)
	if err != nil {
		return time.Time{}, 0, err
	}
	for at := start.Add(step); at.Before(end); at = at.Add(step) {
		value, valueErr := celestialApparentAltitudeDegrees(at, location, body)
		if valueErr != nil {
			return time.Time{}, 0, valueErr
		}
		if value > bestAltitude {
			bestAt, bestAltitude = at, value
		}
	}
	low := bestAt.Add(-step)
	if low.Before(start) {
		low = start
	}
	high := bestAt.Add(step)
	if high.After(end) {
		high = end
	}
	const inversePhi = 0.6180339887498948482
	left := high.Add(-time.Duration(float64(high.Sub(low)) * inversePhi))
	right := low.Add(time.Duration(float64(high.Sub(low)) * inversePhi))
	leftAltitude, err := celestialApparentAltitudeDegrees(left, location, body)
	if err != nil {
		return time.Time{}, 0, err
	}
	rightAltitude, err := celestialApparentAltitudeDegrees(right, location, body)
	if err != nil {
		return time.Time{}, 0, err
	}
	for high.Sub(low) > time.Second {
		if leftAltitude < rightAltitude {
			low, left, leftAltitude = left, right, rightAltitude
			right = low.Add(time.Duration(float64(high.Sub(low)) * inversePhi))
			rightAltitude, err = celestialApparentAltitudeDegrees(right, location, body)
		} else {
			high, right, rightAltitude = right, left, leftAltitude
			left = high.Add(-time.Duration(float64(high.Sub(low)) * inversePhi))
			leftAltitude, err = celestialApparentAltitudeDegrees(left, location, body)
		}
		if err != nil {
			return time.Time{}, 0, err
		}
	}
	bestAt = low.Add(high.Sub(low) / 2)
	bestAltitude, err = celestialApparentAltitudeDegrees(bestAt, location, body)
	return bestAt, bestAltitude, err
}

func celestialApparentAltitudeDegrees(at time.Time, location forecast.Location, body CelestialBody) (float64, error) {
	var coordinates equatorial
	var err error
	switch body {
	case CelestialSun:
		coordinates = sunCoordinates(at)
	case CelestialMoon:
		coordinates = moonCoordinates(at)
	default:
		coordinates, err = planetCoordinates(at, body)
	}
	if err != nil {
		return 0, err
	}
	altitudeRadians, _ := topocentricHorizontal(at, location, coordinates)
	return planningApparentAltitude(altitudeRadians) / degree, nil
}

func validateCelestialCulminations(culminations []CelestialCulmination, validTimes []time.Time, body CelestialBody) error {
	if len(culminations) == 0 {
		return fmt.Errorf("at least one culmination is required")
	}
	for index, culmination := range culminations {
		if culmination.Body != body || culmination.CivilDate == "" || culmination.DayStart.IsZero() || culmination.DayEndExclusive.IsZero() ||
			!culmination.DayEndExclusive.After(culmination.DayStart) || culmination.DayEndExclusive.Sub(culmination.DayStart) < 23*time.Hour ||
			culmination.DayEndExclusive.Sub(culmination.DayStart) > 25*time.Hour || culmination.ValidAt.Before(culmination.DayStart) ||
			!culmination.ValidAt.Before(culmination.DayEndExclusive) || !finite(culmination.ApparentAltitudeDegrees) ||
			culmination.ApparentAltitudeDegrees < -90 || culmination.ApparentAltitudeDegrees > 90 {
			return fmt.Errorf("culmination %d is invalid", index)
		}
		if parsed, err := time.Parse(time.DateOnly, culmination.CivilDate); err != nil || parsed.Year() != culmination.DayStart.Year() ||
			parsed.Month() != culmination.DayStart.Month() || parsed.Day() != culmination.DayStart.Day() {
			return fmt.Errorf("culmination %d civil date is invalid", index)
		}
		if index > 0 && !culmination.DayStart.Equal(culminations[index-1].DayEndExclusive) {
			return fmt.Errorf("culmination days are not contiguous")
		}
	}
	for sampleIndex, validAt := range validTimes {
		matches := 0
		for _, culmination := range culminations {
			if !validAt.Before(culmination.DayStart) && validAt.Before(culmination.DayEndExclusive) {
				matches++
			}
		}
		if matches != 1 {
			return fmt.Errorf("sample %d belongs to %d culmination days", sampleIndex, matches)
		}
	}
	return nil
}

func scaleVector(value vector3, scale float64) vector3 {
	return vector3{x: value.x * scale, y: value.y * scale, z: value.z * scale}
}

func vectorNorm(value vector3) float64 {
	return math.Sqrt(value.x*value.x + value.y*value.y + value.z*value.z)
}

func vectorAngle(left, right vector3) (float64, error) {
	denominator := vectorNorm(left) * vectorNorm(right)
	if !finite(denominator) || denominator <= 0 {
		return 0, fmt.Errorf("invalid vector geometry")
	}
	dot := (left.x*right.x + left.y*right.y + left.z*right.z) / denominator
	return math.Acos(math.Max(-1, math.Min(1, dot))), nil
}

func eclipticToEquatorial(value vector3) vector3 {
	return vector3{
		x: value.x,
		y: value.y*base.COblJ2000 - value.z*base.SOblJ2000,
		z: value.y*base.SOblJ2000 + value.z*base.COblJ2000,
	}
}
