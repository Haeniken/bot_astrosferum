package iconglobal

import (
	"strings"
	"testing"
	"time"

	"bot_astrosferum/internal/forecast"
)

func TestGlobalURLsUseNativeProducts(t *testing.T) {
	client := NewClient()
	pressure := client.pressureFieldURL("2026072200", 72, 1000, "u")
	if !strings.HasSuffix(pressure, "/00/u/icon_global_icosahedral_pressure-level_2026072200_072_1000_U.grib2.bz2") {
		t.Fatalf("unexpected pressure URL: %s", pressure)
	}
	surface := client.surfaceFieldURL("2026072200", 78, surfaceFields[len(surfaceFields)-1])
	if !strings.HasSuffix(surface, "/00/h_ml_lk/icon_global_icosahedral_single-level_2026072200_078_H_ML_LK.grib2.bz2") {
		t.Fatalf("unexpected surface URL: %s", surface)
	}
	cloud := client.globalModelLevelFieldURL("2026072200", 78, 104, cloudField{directory: "clc", code: "CLC"})
	if !strings.HasSuffix(cloud, "/00/clc/icon_global_icosahedral_model-level_2026072200_078_104_CLC.grib2.bz2") {
		t.Fatalf("unexpected cloud URL: %s", cloud)
	}
	height := client.globalHHLFieldURL("2026072200", 121)
	if !strings.HasSuffix(height, "/00/hhl/icon_global_icosahedral_time-invariant_2026072200_121_HHL.grib2.bz2") {
		t.Fatalf("unexpected HHL URL: %s", height)
	}
}

func TestGlobalCloudSubsetMatchesICONPhysicalHeights(t *testing.T) {
	if len(globalCloudModelLevels) != 27 || len(globalCloudGroundModelLevels) != 34 || globalCloudStepMessageCount(48) != 260 || globalCloudStepMessageCount(49) != 225 {
		t.Fatalf("unexpected Global cloud contract: levels=%d ground=%d messages=%d/%d", len(globalCloudModelLevels), len(globalCloudGroundModelLevels), globalCloudStepMessageCount(48), globalCloudStepMessageCount(49))
	}
	for index, level := range globalCloudGroundModelLevels {
		if level != 87+index {
			t.Fatalf("ground level %d = %d", index, level)
		}
	}
	if got := len(globalCloudGroundThermodynamicOnlyLevels()); got != 11 {
		t.Fatalf("additional Global P/T levels = %d, want 11", got)
	}
	geometryLevels := globalCloudGeometryLevels()
	if geometryLevels[len(geometryLevels)-1] != globalSurfaceHalfLevel {
		t.Fatalf("last HHL = %d, want %d", geometryLevels[len(geometryLevels)-1], globalSurfaceHalfLevel)
	}
	geometrySeen := make(map[int]bool, len(geometryLevels))
	for _, level := range geometryLevels {
		geometrySeen[level] = true
	}
	for level := 87; level <= globalSurfaceHalfLevel; level++ {
		if !geometrySeen[level] {
			t.Fatalf("native-MH turbulence geometry is missing HHL%d", level)
		}
	}
}

func TestManifestRequiresCompleteHorizon(t *testing.T) {
	base := time.Date(2026, time.July, 22, 0, 0, 0, 0, time.UTC)
	manifest := Manifest{
		SchemaVersion: manifestSchemaVersion, Provider: "icon-global", Product: "native", RunID: "2026072200",
		BaseTime: base, PublishedAt: base.Add(time.Hour), Grid: Coverage(), Complete: true,
		PressureSteps: make([]StepFile, 25), SurfaceSteps: make([]StepFile, 79),
	}
	for index := range manifest.PressureSteps {
		manifest.PressureSteps[index] = validTestStep(index*3, base)
	}
	for index := range manifest.SurfaceSteps {
		manifest.SurfaceSteps[index] = validTestStep(index, base)
	}
	if err := manifest.Validate(); err != nil {
		t.Fatal(err)
	}
	published := base.Add(2 * time.Hour)
	manifest.CloudVariables = []string{"ccl", "pres", "qc", "qi", "t", "u", "v", "tke", "HHL"}
	manifest.CloudModelLevels = append([]int(nil), globalCloudModelLevels...)
	manifest.CloudPublishedAt = &published
	manifest.CloudGeometry = &BundleFile{File: "cloud/geometry.grib2", Bytes: 1, SHA256: "sha", Messages: len(globalCloudGeometryLevels())}
	manifest.CloudSteps = make([]StepFile, 79)
	for index := range manifest.CloudSteps {
		manifest.CloudSteps[index] = validTestStep(index, base)
		manifest.CloudSteps[index].Messages = globalCloudStepMessageCount(index)
	}
	if err := manifest.Validate(); err != nil || !manifest.HasHourlyCloud() {
		t.Fatalf("complete Global cloud manifest rejected: %v", err)
	}
	manifest.SurfaceSteps[78].SHA256 = ""
	if err := manifest.Validate(); err == nil {
		t.Fatal("incomplete Global manifest was accepted")
	}
}

func validTestStep(hour int, base time.Time) StepFile {
	return StepFile{ForecastHour: hour, ValidAt: base.Add(time.Duration(hour) * time.Hour), File: "f.grib2", Bytes: 1, SHA256: "sha", Messages: 1}
}

func TestGlobalCellIsStableAtCacheResolution(t *testing.T) {
	location, err := forecast.NewLocation(60.01, 90.03, "Asia/Krasnoyarsk")
	if err != nil {
		t.Fatal(err)
	}
	cell, center := globalCell(location)
	if cell != "lat+60.000-lon+090.000" || center.Latitude != 60 || center.Longitude != 90 {
		t.Fatalf("cell=%q center=%+v", cell, center)
	}
}
