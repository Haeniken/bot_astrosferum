package iconeu

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	"bot_astrosferum/internal/forecast"
)

type surfaceRunner struct{}

func (surfaceRunner) CombinedOutput(_ context.Context, _ string, _ ...string) ([]byte, error) {
	return []byte("2t 283.15\n2d 281.15\n2r 87\nCLCT 75\nCLCL 60\nCLCM 35\nCLCH 20\ntp 1.5\n10u 3\n10v 4\nVMAX_10M 8\nprmsl 101325\nvis 39876\nTQV 17.77246094\nTQC 0.081\nTQI 0.027\nmld 725\n"), nil
}

type surfaceRunnerEcCodes245 struct{}

func (surfaceRunnerEcCodes245) CombinedOutput(_ context.Context, _ string, _ ...string) ([]byte, error) {
	return []byte("2t 283.15\n2d 281.15\n2r 87\nCLCT 75\nCLCL 60\nCLCM 35\nCLCH 20\ntp 1.5\n10u 3\n10v 4\nmax_i10fg 8\nprmsl 101325\nvis 39876\nTQV 17.77246094\nMH 725\n"), nil
}

type surfaceTextRunner string

func (runner surfaceTextRunner) CombinedOutput(_ context.Context, _ string, _ ...string) ([]byte, error) {
	return []byte(runner), nil
}

type surfaceValidationRunner struct {
	mixedLayerUnits string
}

func (runner surfaceValidationRunner) CombinedOutput(_ context.Context, name string, arguments ...string) ([]byte, error) {
	if name == "grib_count" {
		return []byte(strconv.Itoa(SurfaceBundleSchemaVersion) + "\n"), nil
	}
	for _, argument := range arguments {
		if argument == "shortName=mld" {
			return []byte(runner.mixedLayerUnits + "\n"), nil
		}
	}
	shortNames := make([]string, len(surfaceFields))
	for index, field := range surfaceFields {
		shortNames[index] = field.ShortName
	}
	return []byte(strings.Join(shortNames, "\n") + "\n"), nil
}

func TestExtractSurfaceAcceptsEcCodes245WindGustAlias(t *testing.T) {
	location, err := forecast.NewLocation(55.7558, 37.6173, "Europe/Moscow")
	if err != nil {
		t.Fatal(err)
	}
	got, err := ExtractSurfaceFrame(context.Background(), surfaceRunnerEcCodes245{}, "f007.grib2", location, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if got.Frame.WindGustMS != 8 {
		t.Fatalf("wind gust = %v, want 8", got.Frame.WindGustMS)
	}
}

func TestCanonicalSurfaceShortNameAcceptsICONGlobalMixedLayerAlias(t *testing.T) {
	if got := canonicalSurfaceShortName("H_ML_LK"); got != "mld" {
		t.Fatalf("canonical mixed-layer name = %q, want mld", got)
	}
}

func TestExtractSurfaceFrameNormalizesUnits(t *testing.T) {
	location, _ := forecast.NewLocation(59.9, 30.3, "Europe/Moscow")
	data, err := ExtractSurfaceFrame(context.Background(), surfaceRunner{}, "surface.grib2", location, time.Unix(1, 0))
	if err != nil {
		t.Fatal(err)
	}
	if data.Frame.TemperatureC != 10 || data.Frame.DewPointSpreadC() != 2 || data.Frame.RelativeHumidityPercent != 87 || data.Frame.LowCloudCoverPercent != 60 || data.Frame.MidCloudCoverPercent != 35 || data.Frame.HighCloudCoverPercent != 20 || data.Frame.WindSpeedMS != 5 || data.Frame.PressureHPA != 1013.25 || data.Frame.VisibilityKM != 39.876 || data.Frame.PrecipitableWaterMM != 17.77246094 || data.Frame.CloudLiquidPathKgM2 != 0.081 || data.Frame.CloudIcePathKgM2 != 0.027 || data.Frame.MixedLayerDepthM != 725 || !data.Frame.CloudCondensateAvailable || !data.Frame.FogHeuristicAvailable || !data.Frame.TransparencyHeuristicAvailable || data.AccumulatedPrecipMM != 1.5 {
		t.Fatalf("unexpected normalized frame: %+v total=%v", data.Frame, data.AccumulatedPrecipMM)
	}
}

func TestExtractSurfaceWithoutVisibilityRemainsFiniteAndJSONSafe(t *testing.T) {
	location, _ := forecast.NewLocation(59.9, 30.3, "Europe/Moscow")
	input := "2t 283.15\n2d 281.15\n2r 99\nCLCT 5\ntp 0\n10u 1\n10v 1\nprmsl 101325\nTQV 8\nTQC 0\nTQI 0\nmld 500\n"
	data, err := ExtractSurfaceFrame(context.Background(), surfaceTextRunner(input), "surface.grib2", location, time.Unix(1, 0))
	if err != nil {
		t.Fatal(err)
	}
	if data.Frame.VisibilityKM != 0 || data.Frame.FogHeuristicAvailable || data.Frame.TransparencyHeuristicAvailable || data.Frame.FogHeuristic() != 0 {
		t.Fatalf("unexpected missing-visibility frame: %+v", data.Frame)
	}
	if _, err := json.Marshal(data.Frame); err != nil {
		t.Fatalf("marshal missing-visibility frame: %v", err)
	}
}

func TestExtractSurfaceRejectsInvalidMixedLayerDepth(t *testing.T) {
	location, _ := forecast.NewLocation(59.9, 30.3, "Europe/Moscow")
	valid := "2t 283.15\n2d 281.15\n2r 87\nCLCT 75\ntp 1.5\n10u 3\n10v 4\nVMAX_10M 8\nprmsl 101325\n"
	for _, value := range []string{"-1", "NaN", "+Inf"} {
		if _, err := ExtractSurfaceFrame(context.Background(), surfaceTextRunner(valid+"mld "+value+"\n"), "surface.grib2", location, time.Unix(1, 0)); err == nil {
			t.Fatalf("mixed-layer depth %q was accepted", value)
		}
	}
}

func TestSurfaceFieldURL(t *testing.T) {
	if len(surfaceFields) != SurfaceBundleSchemaVersion {
		t.Fatalf("surface field count = %d, schema = v%d", len(surfaceFields), SurfaceBundleSchemaVersion)
	}
	client := NewClient()
	got := client.surfaceFieldURL("2026071912", 3, surfaceFields[0])
	want := "https://opendata.dwd.de/weather/nwp/icon-eu/grib/12/t_2m/icon-eu_europe_regular-lat-lon_single-level_2026071912_003_T_2M.grib2.bz2"
	if got != want {
		t.Fatalf("URL = %q, want %q", got, want)
	}
	mixedLayer := surfaceFields[SurfaceLegacySchemaVersion-1]
	got = client.surfaceFieldURL("2026071912", 3, mixedLayer)
	want = "https://opendata.dwd.de/weather/nwp/icon-eu/grib/12/mh/icon-eu_europe_regular-lat-lon_single-level_2026071912_003_MH.grib2.bz2"
	if got != want {
		t.Fatalf("mixed-layer URL = %q, want %q", got, want)
	}
}

func TestValidateSurfaceBundleRequiresMixedLayerDepthInMetres(t *testing.T) {
	client := NewClient()
	client.Runner = surfaceValidationRunner{mixedLayerUnits: "m"}
	if err := client.validateSurfaceBundle(context.Background(), "f000.grib2"); err != nil {
		t.Fatal(err)
	}
	client.Runner = surfaceValidationRunner{mixedLayerUnits: "ft"}
	if err := client.validateSurfaceBundle(context.Background(), "f000.grib2"); err == nil {
		t.Fatal("non-metre mixed-layer depth was accepted")
	}
}

func TestHourlySurfaceDetectsFieldSetUpgrade(t *testing.T) {
	now := time.Now()
	legacyVariables := make([]string, SurfaceLegacySchemaVersion)
	for index, field := range surfaceFields[:SurfaceLegacySchemaVersion] {
		legacyVariables[index] = field.ShortName
	}
	manifest := Manifest{
		SurfaceVariables:   legacyVariables,
		SurfacePublishedAt: &now,
		SurfaceSteps:       make([]SurfaceStepFile, HourlySurfaceStepCount),
	}
	for index := range manifest.SurfaceSteps {
		manifest.SurfaceSteps[index].Messages = SurfaceLegacySchemaVersion
	}
	if !manifest.HasHourlySurface() {
		t.Fatal("legacy field set was not kept operational during upgrade")
	}
	if manifest.HasAstrodomeSurface() {
		t.Fatal("legacy field set was admitted to Astrodome")
	}
	manifest.SurfaceVariables = append(manifest.SurfaceVariables, surfaceFields[SurfaceLegacySchemaVersion].ShortName)
	for index := range manifest.SurfaceSteps {
		manifest.SurfaceSteps[index].Messages++
	}
	if !manifest.HasHourlySurface() {
		t.Fatal("operational bundle with only one lower-bound field was rejected")
	}
	if manifest.HasAstrodomeSurface() {
		t.Fatal("partial Astrodome lower boundary was accepted")
	}
	manifest.SurfaceVariables = append(manifest.SurfaceVariables, surfaceFields[SurfaceLegacySchemaVersion+1].ShortName)
	for index := range manifest.SurfaceSteps {
		manifest.SurfaceSteps[index].Messages++
	}
	if !manifest.HasAstrodomeSurface() {
		t.Fatal("complete Astrodome surface field set was not detected")
	}
	manifest.SurfaceSteps[0].Messages--
	if manifest.HasHourlySurface() || manifest.HasAstrodomeSurface() {
		t.Fatal("bundle with inconsistent message count was accepted")
	}
}

func TestTransparencyHeuristicIsConservativeAndUnavailableForLegacyData(t *testing.T) {
	legacy := forecast.SurfaceFrame{CloudCoverPercent: 5}
	if _, available := legacy.TransparencyHeuristicPercent(); available {
		t.Fatal("legacy frame unexpectedly has transparency inputs")
	}
	clear := forecast.SurfaceFrame{
		CloudCoverPercent: 5, LowCloudCoverPercent: 5, MidCloudCoverPercent: 5, HighCloudCoverPercent: 5,
		VisibilityKM: 50, PrecipitableWaterMM: 5, FogHeuristicAvailable: true, TransparencyHeuristicAvailable: true,
	}
	cloudy := clear
	cloudy.HighCloudCoverPercent = 80
	clearScore, _ := clear.TransparencyHeuristicPercent()
	cloudyScore, _ := cloudy.TransparencyHeuristicPercent()
	if clearScore < 90 || cloudyScore >= clearScore || cloudyScore > 20 {
		t.Fatalf("unexpected transparency scores: clear=%.1f cloudy=%.1f", clearScore, cloudyScore)
	}
}

func TestFogHeuristicRequiresLowVisibilityAndSaturation(t *testing.T) {
	high := forecast.SurfaceFrame{VisibilityKM: .8, RelativeHumidityPercent: 97, TemperatureC: 8, DewPointC: 7, FogHeuristicAvailable: true}
	possible := forecast.SurfaceFrame{VisibilityKM: 3, RelativeHumidityPercent: 92, TemperatureC: 8, DewPointC: 6, FogHeuristicAvailable: true}
	dryHaze := forecast.SurfaceFrame{VisibilityKM: .5, RelativeHumidityPercent: 60, TemperatureC: 8, DewPointC: 0, FogHeuristicAvailable: true}
	if high.FogHeuristic() != 2 || possible.FogHeuristic() != 1 || dryHaze.FogHeuristic() != 0 {
		t.Fatalf("unexpected fog heuristic states: %d/%d/%d", high.FogHeuristic(), possible.FogHeuristic(), dryHaze.FogHeuristic())
	}
}

func TestSurfaceVisibilityWithoutPWVKeepsFogHeuristicAvailable(t *testing.T) {
	values := map[string]float64{
		"2t": 281.15, "2d": 280.15, "2r": 97, "CLCT": 5, "tp": 0,
		"10u": 1, "10v": 1, "prmsl": 101325, "mld": 500, "vis": 800,
	}
	extracted, err := surfaceFromValues(values, time.Unix(1, 0), "surface.grib2")
	if err != nil {
		t.Fatal(err)
	}
	if !extracted.Frame.FogHeuristicAvailable || extracted.Frame.FogHeuristic() != 2 {
		t.Fatalf("visibility-only fog input was not retained: %+v", extracted.Frame)
	}
	if extracted.Frame.TransparencyHeuristicAvailable {
		t.Fatal("transparency heuristic was available without PWV")
	}
}
