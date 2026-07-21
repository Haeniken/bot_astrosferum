package forecast

import (
	"testing"
	"time"
)

func TestParseLocationText(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		latitude  float64
		longitude float64
	}{
		{"comma", "59.9386, 30.3141", 59.9386, 30.3141},
		{"space", "59.9386 30.3141", 59.9386, 30.3141},
		{"command", "/forecast 59.9386 30.3141", 59.9386, 30.3141},
		{"signed", "/forecast +59.9386, +30.3141", 59.9386, 30.3141},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			latitude, longitude, err := ParseLocationText(test.input)
			if err != nil {
				t.Fatalf("ParseLocationText returned error: %v", err)
			}
			if latitude != test.latitude || longitude != test.longitude {
				t.Fatalf("got %v,%v; want %v,%v", latitude, longitude, test.latitude, test.longitude)
			}
		})
	}
}

func TestParseLocationTextRejectsInvalidInput(t *testing.T) {
	for _, input := range []string{"", "59.9386", "91, 30", "59, 181", "NaN, 20", "1, 2, 3"} {
		t.Run(input, func(t *testing.T) {
			if _, _, err := ParseLocationText(input); err == nil {
				t.Fatalf("expected %q to fail", input)
			}
		})
	}
}

func TestTimeZoneResolverPriorityCities(t *testing.T) {
	resolver, err := NewTimeZoneResolver()
	if err != nil {
		t.Fatal(err)
	}
	for name, coordinates := range map[string][2]float64{
		"saint_petersburg": {59.9386, 30.3141},
		"moscow":           {55.7558, 37.6173},
	} {
		t.Run(name, func(t *testing.T) {
			if got := resolver.Resolve(coordinates[0], coordinates[1]); got != "Europe/Moscow" {
				t.Fatalf("got %q; want Europe/Moscow", got)
			}
		})
	}
}

func TestTimeZoneLabel(t *testing.T) {
	at := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	if got := TimeZoneLabel("Europe/Moscow", at); got != "Europe/Moscow · MSK (UTC+3)" {
		t.Fatalf("unexpected label: %q", got)
	}
	if got := TimeZoneLabel("not/a-zone", at); got != "UTC" {
		t.Fatalf("invalid zone must fall back to UTC, got %q", got)
	}
}
