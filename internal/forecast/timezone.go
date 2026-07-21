package forecast

import (
	"fmt"
	"time"
	_ "time/tzdata"

	"github.com/ringsaturn/tzf"
)

type TimeZoneResolver struct {
	finder tzf.F
}

func NewTimeZoneResolver() (*TimeZoneResolver, error) {
	finder, err := tzf.NewDefaultFinder()
	if err != nil {
		return nil, fmt.Errorf("initialize timezone finder: %w", err)
	}
	return &TimeZoneResolver{finder: finder}, nil
}

func (r *TimeZoneResolver) Resolve(latitude, longitude float64) string {
	if r == nil || r.finder == nil || ValidateCoordinates(latitude, longitude) != nil {
		return "UTC"
	}
	name := r.finder.GetTimezoneName(longitude, latitude)
	if name == "" {
		return "UTC"
	}
	if _, err := time.LoadLocation(name); err != nil {
		return "UTC"
	}
	return name
}

func TimeZoneLabel(name string, at time.Time) string {
	location, err := time.LoadLocation(name)
	if err != nil {
		location = time.UTC
		name = "UTC"
	}
	localized := at.In(location)
	abbreviation, seconds := localized.Zone()
	sign := "+"
	if seconds < 0 {
		sign = "-"
		seconds = -seconds
	}
	hours := seconds / 3600
	minutes := (seconds % 3600) / 60
	offset := fmt.Sprintf("UTC%s%d", sign, hours)
	if minutes != 0 {
		offset = fmt.Sprintf("UTC%s%02d:%02d", sign, hours, minutes)
	}
	if abbreviation == "UTC" && seconds == 0 {
		return "UTC"
	}
	return fmt.Sprintf("%s · %s (%s)", name, abbreviation, offset)
}
