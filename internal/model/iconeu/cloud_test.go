package iconeu

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"
	"testing"
	"time"

	"bot_astrosferum/internal/forecast"
)

type cloudRunner struct{ output string }

func (runner cloudRunner) CombinedOutput(context.Context, string, ...string) ([]byte, error) {
	return []byte(runner.output), nil
}

type cloudMetadataRunner struct {
	count    int
	metadata string
}

func (runner cloudMetadataRunner) CombinedOutput(_ context.Context, name string, _ ...string) ([]byte, error) {
	if name == "grib_count" {
		return []byte(strconv.Itoa(runner.count)), nil
	}
	return []byte(runner.metadata), nil
}

func TestExtractCloudFrameUsesModelPressureAndHHLHeight(t *testing.T) {
	location, _ := forecast.NewLocation(55.7558, 37.6173, "Europe/Moscow")
	frame, err := ExtractCloudFrame(context.Background(), cloudRunner{"ccl 58 43\npres 58 75687\nclwmr 58 0.00012\nQI 58 0.00003\nt 58 265.5\nu 58 12\nv 58 -4\ntke 58 0.8\ntke 59 0.4\n"}, "f000.grib2", location, time.Unix(1, 0), []int{58}, []int{58}, map[int]float64{58: 3150, 59: 2850})
	if err != nil {
		t.Fatal(err)
	}
	level := frame.Levels[0]
	if level.PressureHPA != 756.87 || level.HeightM != 3000 || level.LayerThicknessM != 300 || level.TemperatureK != 265.5 ||
		level.UMS != 12 || level.VMS != -4 || math.Abs(level.TKEJkg-0.6) > 1e-12 || level.CoverPercent != 43 ||
		level.CloudLiquidKgKg != 0.00012 || level.CloudIceKgKg != 0.00003 {
		t.Fatalf("unexpected cloud level: %+v", level)
	}
}

func TestExtractCloudFrameKeepsUpperTemperatureAndLeavesDynamicsUnavailable(t *testing.T) {
	location, _ := forecast.NewLocation(55.7558, 37.6173, "Europe/Moscow")
	frame, err := ExtractCloudFrame(context.Background(), cloudRunner{"ccl 25 5\npres 25 20687\nt 25 220\nclwmr 25 0\nQI 25 0\n"}, "f000.grib2", location, time.Unix(1, 0), []int{25}, nil, map[int]float64{25: 12150, 26: 11850})
	if err != nil {
		t.Fatal(err)
	}
	level := frame.Levels[0]
	if level.TemperatureK != 220 || level.LayerThicknessM != 300 || !math.IsNaN(level.UMS) || !math.IsNaN(level.VMS) || !math.IsNaN(level.TKEJkg) {
		t.Fatalf("sparse upper dynamics should be unavailable: %+v", level)
	}
}

func TestCloudModelLevelsAreContinuousNearGround(t *testing.T) {
	seen := make(map[int]bool, len(DefaultCloudModelLevels))
	for _, level := range DefaultCloudModelLevels {
		seen[level] = true
	}
	for level := 58; level <= 74; level++ {
		if !seen[level] {
			t.Fatalf("ground-layer model level %d is missing", level)
		}
	}
	if len(DefaultCloudModelLevels) != 27 {
		t.Fatalf("cloud model level count = %d, want 27", len(DefaultCloudModelLevels))
	}
}

func TestCloudTKEUsesUniqueAdjacentHalfLevels(t *testing.T) {
	levels := cloudTKEHalfLevels()
	if len(levels) != 18 {
		t.Fatalf("TKE half-level count = %d, want 18", len(levels))
	}
	seen := make(map[int]bool, len(levels))
	for _, level := range levels {
		if seen[level] {
			t.Fatalf("duplicate TKE half-level %d", level)
		}
		seen[level] = true
	}
	for level := 58; level <= 75; level++ {
		if !seen[level] {
			t.Fatalf("near-ground TKE half-level %d is missing", level)
		}
	}
	if got, want := cloudStepMessageCount(), 187; got != want {
		t.Fatalf("cloud step message count = %d, want %d", got, want)
	}
}

func TestCloudSurfaceElevationUsesHHL75(t *testing.T) {
	value, err := CloudSurfaceElevation(map[int]float64{74: 240, 75: 219.75}, 75, "ICON-EU")
	if err != nil {
		t.Fatal(err)
	}
	if value != 219.75 {
		t.Fatalf("surface elevation = %v, want 219.75", value)
	}
	if _, err := CloudSurfaceElevation(map[int]float64{74: 240}, 75, "ICON-EU"); err == nil {
		t.Fatal("missing HHL75 was accepted as surface elevation")
	}
}

func TestCloudGeometryPublishesEveryNativeHalfLevel(t *testing.T) {
	levels := cloudGeometryLevels()
	if len(levels) != domeHalfLevelCount {
		t.Fatalf("cloud geometry levels = %d, want %d", len(levels), domeHalfLevelCount)
	}
	for index, level := range levels {
		if want := index + 1; level != want {
			t.Fatalf("cloud geometry level[%d] = %d, want %d", index, level, want)
		}
	}
}

func TestCloudFieldURLs(t *testing.T) {
	client := NewClient()
	want := "https://opendata.dwd.de/weather/nwp/icon-eu/grib/12/clc/icon-eu_europe_regular-lat-lon_model-level_2026071912_003_25_CLC.grib2.bz2"
	if got := client.modelLevelFieldURL("2026071912", 3, 25, "clc", "CLC"); got != want {
		t.Fatalf("CLC URL = %q", got)
	}
	want = "https://opendata.dwd.de/weather/nwp/icon-eu/grib/12/hhl/icon-eu_europe_regular-lat-lon_time-invariant_2026071912_25_HHL.grib2.bz2"
	if got := client.hhlFieldURL("2026071912", 25); got != want {
		t.Fatalf("HHL URL = %q", got)
	}
	want = "https://opendata.dwd.de/weather/nwp/icon-eu/grib/12/tke/icon-eu_europe_regular-lat-lon_model-level_2026071912_003_75_TKE.grib2.bz2"
	if got := client.modelLevelFieldURL("2026071912", 3, 75, "tke", "TKE"); got != want {
		t.Fatalf("TKE URL = %q", got)
	}
}

func TestManifestHourlyCloudRequiresGroundLayerThermodynamics(t *testing.T) {
	published := time.Now().UTC()
	manifest := Manifest{
		CloudVariables:   []string{"ccl", "pres", "qc", "qi", "t", "u", "v", "tke", "HHL"},
		CloudModelLevels: append([]int(nil), DefaultCloudModelLevels...),
		CloudPublishedAt: &published,
		CloudGeometry:    &BundleFile{Messages: len(cloudGeometryLevels())},
		CloudSteps:       make([]SurfaceStepFile, HourlySurfaceStepCount),
	}
	for index := range manifest.CloudSteps {
		manifest.CloudSteps[index].Messages = cloudStepMessageCount()
	}
	if !manifest.HasHourlyCloud() {
		t.Fatal("current ground-layer cloud publication was rejected")
	}
	manifest.CloudGeometry.Messages--
	if manifest.HasHourlyCloud() {
		t.Fatal("legacy partial HHL geometry was accepted as current")
	}
	manifest.CloudGeometry.Messages++
	manifest.CloudVariables = []string{"ccl", "pres", "qc", "qi", "HHL"}
	if manifest.HasHourlyCloud() {
		t.Fatal("legacy cloud publication was accepted as current")
	}
}

func TestValidateCloudStepDistinguishesFullAndHalfLevels(t *testing.T) {
	var metadata strings.Builder
	for _, level := range DefaultCloudModelLevels {
		for _, field := range cloudBaseLevelFields {
			fmt.Fprintf(&metadata, "%s generalVerticalLayer %d\n", field.shortName, level)
		}
	}
	for _, level := range DefaultCloudGroundModelLevels {
		for _, field := range cloudGroundFullLevelFields {
			fmt.Fprintf(&metadata, "%s generalVerticalLayer %d\n", field.shortName, level)
		}
	}
	for _, level := range cloudTKEHalfLevels() {
		fmt.Fprintf(&metadata, "tke generalVertical %d\n", level)
	}
	client := NewClient()
	client.Runner = cloudMetadataRunner{count: cloudStepMessageCount(), metadata: metadata.String()}
	if err := client.validateCloudStep(context.Background(), "f000.grib2"); err != nil {
		t.Fatalf("valid cloud metadata was rejected: %v", err)
	}
	invalid := strings.Replace(metadata.String(), "tke generalVertical ", "tke generalVerticalLayer ", 1)
	client.Runner = cloudMetadataRunner{count: cloudStepMessageCount(), metadata: invalid}
	if err := client.validateCloudStep(context.Background(), "f000.grib2"); err == nil {
		t.Fatal("full-level TKE metadata was accepted as half-level TKE")
	}
}
