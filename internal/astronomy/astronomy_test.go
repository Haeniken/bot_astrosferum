package astronomy

import (
	"testing"
	"time"

	"bot_astrosferum/internal/forecast"
)

func TestSaintPetersburgEventsUseCoordinateTimezone(t *testing.T) {
	location, err := forecast.NewLocation(59.9386, 30.3141, "Europe/Moscow")
	if err != nil {
		t.Fatal(err)
	}
	date := time.Date(2026, time.July, 20, 0, 0, 0, 0, time.UTC)
	series, err := Compute(location, date, date.Add(24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	day, ok := series.DayAt(time.Date(2026, time.July, 20, 12, 0, 0, 0, time.FixedZone("MSK", 3*60*60)))
	if !ok {
		t.Fatal("missing local day")
	}
	if day.Sunrise.IsZero() || day.Sunset.IsZero() || day.Moonrise.IsZero() || day.Moonset.IsZero() {
		t.Fatalf("missing events: %+v", day)
	}
	if day.Sunrise.Location().String() != "Europe/Moscow" || day.Sunset.Location().String() != "Europe/Moscow" {
		t.Fatalf("events use wrong timezone: %+v", day)
	}
	if day.MoonIlluminationPercent < 0 || day.MoonIlluminationPercent > 100 || day.MoonAgeDays < 0 || day.MoonAgeDays > synodicMonthDays {
		t.Fatalf("invalid lunar cycle: %+v", day)
	}
	t.Logf("Sun %s–%s, Moon %s–%s", day.Sunrise.Format("15:04:05"), day.Sunset.Format("15:04:05"), day.Moonrise.Format("15:04:05"), day.Moonset.Format("15:04:05"))
	assertClockNear(t, day.Sunrise, 4, 13, 2*time.Minute)
	assertClockNear(t, day.Sunset, 21, 57, 2*time.Minute)
	assertClockNear(t, day.Moonrise, 13, 6, 2*time.Minute)
	assertClockNear(t, day.Moonset, 22, 50, 2*time.Minute)
}

func TestMoscowEventsAgainstUSNO(t *testing.T) {
	location, err := forecast.NewLocation(55.7558, 37.6173, "Europe/Moscow")
	if err != nil {
		t.Fatal(err)
	}
	date := time.Date(2026, time.July, 20, 0, 0, 0, 0, time.UTC)
	series, err := Compute(location, date, date.Add(24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	day, ok := series.DayAt(time.Date(2026, time.July, 20, 12, 0, 0, 0, time.FixedZone("MSK", 3*60*60)))
	if !ok {
		t.Fatal("missing Moscow local day")
	}
	// USNO Astronomical Applications Department one-day service for
	// Moscow, UTC+3 on 2026-07-20: Sun 04:14/20:57, Moon 12:25/22:33.
	assertClockNear(t, day.Sunrise, 4, 14, 2*time.Minute)
	assertClockNear(t, day.Sunset, 20, 57, 2*time.Minute)
	assertClockNear(t, day.Moonrise, 12, 25, 2*time.Minute)
	assertClockNear(t, day.Moonset, 22, 33, 2*time.Minute)
	if day.JupiterRise.IsZero() || day.JupiterSet.IsZero() || day.SaturnRise.IsZero() || day.SaturnSet.IsZero() {
		t.Fatalf("missing planetary events: %+v", day)
	}
	t.Logf("Moscow Sun %s–%s, Moon %s–%s", day.Sunrise.Format("15:04:05"), day.Sunset.Format("15:04:05"), day.Moonrise.Format("15:04:05"), day.Moonset.Format("15:04:05"))
}

func assertClockNear(t *testing.T, actual time.Time, hour, minute int, tolerance time.Duration) {
	t.Helper()
	expected := time.Date(actual.Year(), actual.Month(), actual.Day(), hour, minute, 0, 0, actual.Location())
	difference := actual.Sub(expected)
	if difference < 0 {
		difference = -difference
	}
	if difference > tolerance {
		t.Fatalf("event %s differs from expected %s by %s", actual, expected, difference)
	}
}
