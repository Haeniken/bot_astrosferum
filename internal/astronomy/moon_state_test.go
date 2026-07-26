package astronomy

import (
	"math"
	"testing"
	"time"

	"bot_astrosferum/internal/forecast"
)

func TestMoonStateAtRangesAndDailyIllumination(t *testing.T) {
	location, err := forecast.NewLocation(59.9386, 30.3141, "Europe/Moscow")
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, time.July, 20, 9, 0, 0, 0, time.UTC)
	state := MoonStateAt(location, at)
	if !state.Valid {
		t.Fatal("expected a valid lunar state")
	}
	if state.TopocentricGeometricAltitudeDegrees < -90 || state.TopocentricGeometricAltitudeDegrees > 90 {
		t.Fatalf("altitude outside physical range: %v", state.TopocentricGeometricAltitudeDegrees)
	}
	if state.AzimuthDegrees < 0 || state.AzimuthDegrees >= 360 {
		t.Fatalf("azimuth outside [0, 360): %v", state.AzimuthDegrees)
	}
	if state.TopocentricZenithDistanceDegrees < 0 || state.TopocentricZenithDistanceDegrees > 180 {
		t.Fatalf("zenith distance outside physical range: %v", state.TopocentricZenithDistanceDegrees)
	}
	if math.Abs(state.TopocentricGeometricAltitudeDegrees+state.TopocentricZenithDistanceDegrees-90) > 1e-12 {
		t.Fatalf("altitude and zenith distance are inconsistent: %+v", state)
	}
	if state.IlluminatedFraction < 0 || state.IlluminatedFraction > 1 {
		t.Fatalf("illumination outside [0, 1]: %v", state.IlluminatedFraction)
	}
	if state.PhaseAngleDegrees < 0 || state.PhaseAngleDegrees > 180 {
		t.Fatalf("phase angle outside [0, 180]: %v", state.PhaseAngleDegrees)
	}
	if state.EarthMoonDistanceKM < 340_000 || state.EarthMoonDistanceKM > 410_000 {
		t.Fatalf("implausible Earth-Moon distance: %v km", state.EarthMoonDistanceKM)
	}
	wantIllumination := (1 + math.Cos(state.PhaseAngleDegrees*degree)) / 2
	if math.Abs(state.IlluminatedFraction-wantIllumination) > 1e-12 {
		t.Fatalf("phase angle and illuminated fraction disagree: got %.12f, want %.12f", state.IlluminatedFraction, wantIllumination)
	}

	series, err := Compute(location, at, at)
	if err != nil {
		t.Fatal(err)
	}
	day, ok := series.DayAt(at)
	if !ok {
		t.Fatal("missing daily ephemeris")
	}
	if math.Abs(state.IlluminatedFraction*100-day.MoonIlluminationPercent) > 1e-10 {
		t.Fatalf("hourly illumination %.12f disagrees with daily value %.12f", state.IlluminatedFraction*100, day.MoonIlluminationPercent)
	}
}

func TestMoonStateAtIsContinuousAcrossForecastSteps(t *testing.T) {
	location, err := forecast.NewLocation(55.7558, 37.6173, "Europe/Moscow")
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, time.July, 20, 18, 0, 0, 0, time.UTC)
	before := MoonStateAt(location, at)
	after := MoonStateAt(location, at.Add(time.Minute))
	if !before.Valid || !after.Valid {
		t.Fatalf("invalid state across continuity fixture: before=%+v after=%+v", before, after)
	}
	if difference := math.Abs(after.TopocentricGeometricAltitudeDegrees - before.TopocentricGeometricAltitudeDegrees); difference > 0.5 {
		t.Fatalf("one-minute altitude discontinuity: %.6f degrees", difference)
	}
	azimuthDifference := math.Abs(after.AzimuthDegrees - before.AzimuthDegrees)
	azimuthDifference = math.Min(azimuthDifference, 360-azimuthDifference)
	if azimuthDifference > 1 {
		t.Fatalf("one-minute azimuth discontinuity: %.6f degrees", azimuthDifference)
	}
	if difference := math.Abs(after.IlluminatedFraction - before.IlluminatedFraction); difference > 0.001 {
		t.Fatalf("one-minute illumination discontinuity: %.9f", difference)
	}
}

func TestMoonStateAtMatchesRiseSetHorizonGeometry(t *testing.T) {
	location, err := forecast.NewLocation(55.7558, 37.6173, "Europe/Moscow")
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, time.July, 20, 0, 0, 0, 0, time.UTC)
	series, err := Compute(location, start, start.Add(24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	day, ok := series.DayAt(start.Add(12 * time.Hour))
	if !ok || day.Moonrise.IsZero() || day.Moonset.IsZero() {
		t.Fatalf("missing lunar events: %+v", day)
	}

	rise := MoonStateAt(location, day.Moonrise)
	set := MoonStateAt(location, day.Moonset)
	if !rise.Valid || !set.Valid {
		t.Fatalf("invalid horizon states: rise=%+v set=%+v", rise, set)
	}
	// Rise/set is defined for the apparent upper limb. The apparent centre is
	// therefore still slightly below zero at both refined event times.
	for name, state := range map[string]MoonState{"rise": rise, "set": set} {
		if state.ApparentAltitudeDegrees < -0.8 || state.ApparentAltitudeDegrees > 0.1 {
			t.Fatalf("%s apparent lunar centre is inconsistent with upper-limb event: %.6f degrees", name, state.ApparentAltitudeDegrees)
		}
	}
	if rise.AzimuthDegrees >= 180 {
		t.Fatalf("Moon rise should be in the eastern half of the horizon, got %.3f degrees", rise.AzimuthDegrees)
	}
	if set.AzimuthDegrees <= 180 {
		t.Fatalf("Moon set should be in the western half of the horizon, got %.3f degrees", set.AzimuthDegrees)
	}
}

func TestMoonStateAtRejectsInvalidInput(t *testing.T) {
	validLocation, err := forecast.NewLocation(0, 0, "UTC")
	if err != nil {
		t.Fatal(err)
	}
	if state := MoonStateAt(validLocation, time.Time{}); state.Valid {
		t.Fatalf("zero time unexpectedly valid: %+v", state)
	}
	invalidLocation := forecast.Location{Latitude: math.NaN(), Longitude: 0, TimeZone: "UTC"}
	if state := MoonStateAt(invalidLocation, time.Now()); state.Valid {
		t.Fatalf("invalid coordinates unexpectedly valid: %+v", state)
	}
}
