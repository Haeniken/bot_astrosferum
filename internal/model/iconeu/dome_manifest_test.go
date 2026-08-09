package iconeu

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func validDomeManifestFixture() DomeManifest {
	base := time.Date(2026, time.July, 28, 12, 0, 0, 0, time.UTC)
	manifest := NewDomeManifest("2026072812", base, strings.Repeat("a", 64))
	manifest.PublishedAt = base.Add(4 * time.Hour)
	manifest.Complete = true
	manifest.Geometry = DomeStepFile{
		Source: DomeFileSourceBaseRun, ForecastHour: 0, ValidAt: base,
		File: "runs/2026072812/cloud-hourly-v4/geometry.grib2", Bytes: 100,
		AllocatedBytes: 512, SHA256: strings.Repeat("b", 64), Messages: domeHalfLevelCount,
	}
	for _, hour := range DomeNativeForecastHours() {
		validAt := base.Add(time.Duration(hour) * time.Hour)
		step := DomeModelStep{ForecastHour: hour, ValidAt: validAt, Messages: domeModelMessagesPerStep}
		if hour <= 78 {
			step.Parts = append(step.Parts, DomeStepFile{
				Source: DomeFileSourceBaseRun, ForecastHour: hour, ValidAt: validAt,
				File: fmt.Sprintf("runs/2026072812/cloud-hourly-v4/f%03d.grib2", hour), Bytes: 100,
				AllocatedBytes: 512, SHA256: strings.Repeat("c", 64), Messages: cloudStepMessageCount(),
			})
		}
		step.Parts = append(step.Parts, DomeStepFile{
			Source: DomeFileSourceDomeRun, ForecastHour: hour, ValidAt: validAt,
			File: fmt.Sprintf("dome-runs/2026072812/contract-a/model/f%03d.grib2", hour), Bytes: 100,
			AllocatedBytes: 512, SHA256: strings.Repeat("d", 64),
			Messages: func() int {
				if hour <= 78 {
					return domeModelMessagesPerStep - cloudStepMessageCount()
				}
				return domeModelMessagesPerStep
			}(),
		})
		manifest.ModelSteps = append(manifest.ModelSteps, step)
	}
	for _, hour := range []int{81, 84} {
		manifest.SurfaceExtensionSteps = append(manifest.SurfaceExtensionSteps, DomeStepFile{
			Source: DomeFileSourceDomeRun, ForecastHour: hour, ValidAt: base.Add(time.Duration(hour) * time.Hour),
			File: fmt.Sprintf("dome-runs/2026072812/contract-a/surface/f%03d.grib2", hour), Bytes: 100,
			AllocatedBytes: 512, SHA256: strings.Repeat("e", 64), Messages: SurfaceBundleSchemaVersion,
		})
	}
	return manifest
}

func TestDomeManifestContractIsExactAndSeparateFrom72Frames(t *testing.T) {
	manifest := validDomeManifestFixture()
	if err := manifest.Validate(); err != nil {
		t.Fatalf("valid manifest rejected: %v", err)
	}
	if got := len(DomeNativeForecastHours()); got != 81 {
		t.Fatalf("native bracket count = %d, want 81", got)
	}
	if got := DomeNativeForecastHours()[len(DomeNativeForecastHours())-1]; got != 84 {
		t.Fatalf("last native bracket = f%03d, want f084", got)
	}
	if len(manifest.ModelSteps) == 72 {
		t.Fatal("native input inventory was confused with the 72-frame product")
	}
	if len(manifest.FullModelLevels) != 74 || len(manifest.HalfModelLevels) != 75 || len(manifest.Fields) != 10 {
		t.Fatalf("native inventory = %d full, %d half, %d fields", len(manifest.FullModelLevels), len(manifest.HalfModelLevels), len(manifest.Fields))
	}

	mutated := manifest
	mutated.Fields = append([]DomeFieldSpec(nil), manifest.Fields...)
	mutated.Fields[2].Directory = "relative-humidity"
	if err := mutated.Validate(); err == nil {
		t.Fatal("mutated QV inventory accepted")
	}
	mutated = manifest
	mutated.ModelSteps = append([]DomeModelStep(nil), manifest.ModelSteps[:len(manifest.ModelSteps)-1]...)
	if err := mutated.Validate(); err == nil {
		t.Fatal("missing f084 bracket accepted")
	}
	mutated = manifest
	mutated.SurfaceExtensionSteps = append([]DomeStepFile(nil), manifest.SurfaceExtensionSteps...)
	mutated.SurfaceExtensionSteps[0].ForecastHour = 80
	if err := mutated.Validate(); err == nil {
		t.Fatal("incorrect surface bracket accepted")
	}
}

func TestDomeManifestAtomicRoundTripAndCurrentPointer(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "models", "icon-eu", "dome-runs", "2026072812", "contract-a")
	path := filepath.Join(directory, "manifest.json")
	manifest := validDomeManifestFixture()
	if err := writeDomeManifest(path, manifest); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadDomeManifest(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.ManifestSHA256 == "" || loaded.RunID != manifest.RunID || loaded.Directory != directory {
		t.Fatalf("loaded manifest = %+v", loaded)
	}
	if err := publishCurrentDomeManifest(root, loaded); err != nil {
		t.Fatal(err)
	}
	current, err := LoadCurrentDomeManifest(root)
	if err != nil {
		t.Fatal(err)
	}
	if current.ManifestSHA256 != loaded.ManifestSHA256 || current.Directory != directory {
		t.Fatalf("current manifest differs: %+v != %+v", current, loaded)
	}
	if _, err := os.Stat(filepath.Join(root, "models", "icon-eu", "dome-ready-current")); err != nil {
		t.Fatal(err)
	}
}

func TestDomeManifestRejectsUnsafePathsAndRunMismatch(t *testing.T) {
	manifest := validDomeManifestFixture()
	manifest.ModelSteps[0].Parts[0].File = "../outside.grib2"
	if err := manifest.Validate(); err == nil {
		t.Fatal("unsafe bundle path accepted")
	}
	manifest = validDomeManifestFixture()
	manifest.RunID = "2026072818"
	if err := manifest.Validate(); err == nil {
		t.Fatal("run ID/base time mismatch accepted")
	}
}
