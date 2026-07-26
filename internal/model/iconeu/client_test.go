package iconeu

import (
	"os"
	"path/filepath"
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

func TestPublishCurrentRejectsIncompleteRun(t *testing.T) {
	root := t.TempDir()
	loaded := LoadedManifest{Manifest: Manifest{RunID: "2026072600"}}
	if err := PublishCurrent(root, loaded); err == nil {
		t.Fatal("incomplete run was published")
	}
	if _, err := os.Lstat(filepath.Join(root, "models", "icon-eu", "current")); !os.IsNotExist(err) {
		t.Fatalf("current link exists after rejected publication: %v", err)
	}
}

func TestPublishCurrentSwitchesToCompleteRun(t *testing.T) {
	root := t.TempDir()
	runID := "2026072600"
	runDirectory := filepath.Join(root, "models", "icon-eu", "runs", runID)
	if err := os.MkdirAll(runDirectory, 0o750); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	manifest := Manifest{
		RunID: runID, Variables: []string{"u", "v", "z", "t"},
		Steps: make([]StepFile, 25),
		SurfaceVariables: func() []string {
			result := make([]string, len(surfaceFields))
			for index, field := range surfaceFields {
				result[index] = field.ShortName
			}
			return result
		}(),
		SurfacePublishedAt: &now, SurfaceSteps: make([]SurfaceStepFile, HourlySurfaceStepCount),
		CloudVariables:   []string{"ccl", "pres", "qc", "qi", "t", "u", "v", "tke", "HHL"},
		CloudModelLevels: append([]int(nil), DefaultCloudModelLevels...),
		CloudPublishedAt: &now, CloudGeometry: &BundleFile{Messages: len(cloudGeometryLevels())},
		CloudSteps: make([]SurfaceStepFile, HourlySurfaceStepCount),
	}
	for index := range manifest.Steps {
		manifest.Steps[index].Messages = len(DefaultPressureLevelsHPA) * 4
	}
	for index := range manifest.SurfaceSteps {
		manifest.SurfaceSteps[index].Messages = SurfaceBundleSchemaVersion
		manifest.CloudSteps[index].Messages = cloudStepMessageCount()
	}
	if err := PublishCurrent(root, LoadedManifest{Manifest: manifest, Directory: runDirectory}); err != nil {
		t.Fatal(err)
	}
	target, err := os.Readlink(filepath.Join(root, "models", "icon-eu", "current"))
	if err != nil {
		t.Fatal(err)
	}
	if target != filepath.Join("runs", runID) {
		t.Fatalf("current target = %q", target)
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
