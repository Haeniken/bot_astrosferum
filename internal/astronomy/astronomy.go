package astronomy

import (
	"fmt"
	"math"
	"time"

	"bot_astrosferum/internal/forecast"
)

const synodicMonthDays = 29.53059

type Day struct {
	Date                    time.Time `json:"date"`
	Sunrise                 time.Time `json:"sunrise,omitempty"`
	Sunset                  time.Time `json:"sunset,omitempty"`
	Moonrise                time.Time `json:"moonrise,omitempty"`
	Moonset                 time.Time `json:"moonset,omitempty"`
	MoonAlwaysUp            bool      `json:"moon_always_up,omitempty"`
	MoonAlwaysDown          bool      `json:"moon_always_down,omitempty"`
	MoonPhase               string    `json:"moon_phase"`
	MoonCycle               float64   `json:"moon_cycle"`
	MoonIlluminationPercent float64   `json:"moon_illumination_percent"`
	MoonAgeDays             float64   `json:"moon_age_days"`
	JupiterRise             time.Time `json:"jupiter_rise,omitempty"`
	JupiterSet              time.Time `json:"jupiter_set,omitempty"`
	SaturnRise              time.Time `json:"saturn_rise,omitempty"`
	SaturnSet               time.Time `json:"saturn_set,omitempty"`
}

type Series struct {
	Location forecast.Location `json:"location"`
	Days     []Day             `json:"days"`
}

func Compute(location forecast.Location, start, end time.Time) (Series, error) {
	zone, err := time.LoadLocation(location.TimeZone)
	if err != nil {
		return Series{}, fmt.Errorf("load coordinate timezone: %w", err)
	}
	if end.Before(start) {
		return Series{}, fmt.Errorf("astronomy range ends before it starts")
	}
	first := localMidnight(start, zone)
	last := localMidnight(end, zone)
	series := Series{Location: location}
	for date := first; !date.After(last); date = date.AddDate(0, 0, 1) {
		sun, moon := eventsForLocalDay(date, location.Latitude, location.Longitude)
		jupiter := planetEventsForLocalDay(date, location.Latitude, location.Longitude, planetJupiter)
		saturn := planetEventsForLocalDay(date, location.Latitude, location.Longitude, planetSaturn)
		phase := moonIllumination(date.Add(12 * time.Hour))
		day := Day{
			Date: date, Sunrise: validEvent(sun.rise, date), Sunset: validEvent(sun.set, date),
			Moonrise: validEvent(moon.rise, date), Moonset: validEvent(moon.set, date),
			MoonAlwaysUp: moon.alwaysUp, MoonAlwaysDown: moon.alwaysDown,
			MoonPhase: phaseName(phase.phase), MoonCycle: phase.phase, MoonIlluminationPercent: phase.fraction * 100,
			MoonAgeDays: phase.phase * synodicMonthDays,
			JupiterRise: validEvent(jupiter.rise, date), JupiterSet: validEvent(jupiter.set, date),
			SaturnRise: validEvent(saturn.rise, date), SaturnSet: validEvent(saturn.set, date),
		}
		series.Days = append(series.Days, day)
	}
	return series, nil
}

func (series Series) DayAt(at time.Time) (Day, bool) {
	zone, err := time.LoadLocation(series.Location.TimeZone)
	if err != nil {
		zone = time.UTC
	}
	local := at.In(zone)
	for _, day := range series.Days {
		date := day.Date.In(zone)
		if date.Year() == local.Year() && date.YearDay() == local.YearDay() {
			return day, true
		}
	}
	return Day{}, false
}

func (series Series) IsDay(at time.Time) bool {
	if day, ok := series.DayAt(at); ok && !day.Sunrise.IsZero() && !day.Sunset.IsZero() {
		return !at.Before(day.Sunrise) && at.Before(day.Sunset)
	}
	return sunGeometricAltitude(at, series.Location.Latitude, series.Location.Longitude) > 0
}

// SunAltitudeDegrees returns the geometric solar altitude for twilight and
// daylight presentation. Standard boundaries are 0, -6, -12, and -18°.
func (series Series) SunAltitudeDegrees(at time.Time) float64 {
	return sunGeometricAltitude(at, series.Location.Latitude, series.Location.Longitude) / degree
}

func localMidnight(at time.Time, zone *time.Location) time.Time {
	local := at.In(zone)
	return time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, zone)
}

func validEvent(event, date time.Time) time.Time {
	if event.IsZero() || event.Year() < date.Year()-1 || event.Year() > date.Year()+1 {
		return time.Time{}
	}
	return event
}

func phaseName(phase float64) string {
	index := int(math.Floor((phase+1.0/16.0)*8)) % 8
	if index < 0 {
		index += 8
	}
	return []string{
		"new", "waxing_crescent", "first_quarter", "waxing_gibbous",
		"full", "waning_gibbous", "last_quarter", "waning_crescent",
	}[index]
}

func PhaseNameRU(value string) string {
	return map[string]string{
		"new": "новолуние", "waxing_crescent": "растущий серп", "first_quarter": "первая четверть",
		"waxing_gibbous": "растущая Луна", "full": "полнолуние", "waning_gibbous": "убывающая Луна",
		"last_quarter": "последняя четверть", "waning_crescent": "убывающий серп",
	}[value]
}
