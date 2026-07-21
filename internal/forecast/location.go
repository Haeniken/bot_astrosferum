package forecast

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

type Location struct {
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
	TimeZone  string  `json:"time_zone"`
}

func NewLocation(latitude, longitude float64, timeZone string) (Location, error) {
	if err := ValidateCoordinates(latitude, longitude); err != nil {
		return Location{}, err
	}
	if strings.TrimSpace(timeZone) == "" {
		timeZone = "UTC"
	}
	return Location{Latitude: latitude, Longitude: longitude, TimeZone: timeZone}, nil
}

func ParseLocationText(input string) (float64, float64, error) {
	text := strings.TrimSpace(input)
	if strings.HasPrefix(strings.ToLower(text), "/forecast") {
		text = strings.TrimSpace(text[len("/forecast"):])
	}
	text = strings.ReplaceAll(text, ",", " ")
	parts := strings.Fields(text)
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("expected coordinates as 'latitude, longitude' or 'latitude longitude'")
	}

	latitude, err := strconv.ParseFloat(parts[0], 64)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid latitude %q", parts[0])
	}
	longitude, err := strconv.ParseFloat(parts[1], 64)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid longitude %q", parts[1])
	}
	if err := ValidateCoordinates(latitude, longitude); err != nil {
		return 0, 0, err
	}
	return latitude, longitude, nil
}

func ValidateCoordinates(latitude, longitude float64) error {
	if math.IsNaN(latitude) || math.IsInf(latitude, 0) || latitude < -90 || latitude > 90 {
		return fmt.Errorf("latitude must be a finite number between -90 and 90")
	}
	if math.IsNaN(longitude) || math.IsInf(longitude, 0) || longitude < -180 || longitude > 180 {
		return fmt.Errorf("longitude must be a finite number between -180 and 180")
	}
	return nil
}
