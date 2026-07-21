package iconeu

import (
	"strings"
	"testing"
	"time"
)

func TestFieldURL(t *testing.T) {
	client := NewClient()
	got := client.fieldURL("2026071912", 3, 1000, "u")
	want := "https://opendata.dwd.de/weather/nwp/icon-eu/grib/12/u/icon-eu_europe_regular-lat-lon_pressure-level_2026071912_003_1000_U.grib2.bz2"
	if got != want {
		t.Fatalf("URL = %q, want %q", got, want)
	}
}

func TestManifestValidationRejectsUnsafeStepPath(t *testing.T) {
	manifest := Manifest{
		SchemaVersion: ManifestSchemaVersion, Provider: "icon-eu", Product: ProductName,
		RunID: "2026071912", BaseTime: time.Now(), PublishedAt: time.Now(), Complete: true,
		PressureLevelsHPA: []float64{1000, 50},
		Steps: []StepFile{
			{File: "../escape", ValidAt: time.Now(), Bytes: 1, Messages: 1, SHA256: strings.Repeat("a", 64)},
			{File: "steps/f003.grib2", ValidAt: time.Now(), Bytes: 1, Messages: 1, SHA256: strings.Repeat("b", 64)},
		},
	}
	if err := manifest.Validate(); err == nil {
		t.Fatal("expected unsafe path validation error")
	}
}
