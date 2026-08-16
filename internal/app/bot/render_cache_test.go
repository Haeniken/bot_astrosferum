package bot

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"bot_astrosferum/internal/astronomy"
	"bot_astrosferum/internal/forecast"
	"bot_astrosferum/internal/render"
)

func TestRenderCachePublishLoadAndPrune(t *testing.T) {
	root := t.TempDir()
	staging, err := makeRenderStaging(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range renderResultPaths(cachedRenderResult(staging)) {
		if err := os.WriteFile(path, []byte("png"), 0o640); err != nil {
			t.Fatal(err)
		}
	}
	key := strings.Repeat("a", 64)
	if _, err := publishRenderCache(root, key, staging); err != nil {
		t.Fatal(err)
	}
	if _, ok := loadRenderCache(root, key); !ok {
		t.Fatal("published render cache was not readable")
	}

	dead, err := os.MkdirTemp(root, ".incoming-")
	if err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(dead, old, old); err != nil {
		t.Fatal(err)
	}
	pruneRenderCache(root, 48*time.Hour, 256)
	if _, err := os.Stat(dead); !os.IsNotExist(err) {
		t.Fatalf("stale staging directory was not removed: %v", err)
	}

	for _, key := range []string{strings.Repeat("b", 64), strings.Repeat("c", 64)} {
		directory := filepath.Join(root, key)
		if err := os.Mkdir(directory, 0o750); err != nil {
			t.Fatal(err)
		}
	}
	pruneRenderCache(root, 48*time.Hour, 2)
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("render cache retained %d entries, want 2", len(entries))
	}
}

func TestRenderCacheKeyIncludesEveryOverallCalibrationField(t *testing.T) {
	vertical := forecast.SyntheticVerticalFixture()
	surface := forecast.SyntheticSurfaceFixture()
	cloud := forecast.SyntheticCloudFixture()
	var sky astronomy.Series
	options := render.Options{Width: 3200, Height: 960}
	calibration := forecast.DefaultOverallIndexCalibration()
	baseline := forecastRenderCacheKey(vertical, surface, cloud, forecast.AtmosphericCompositionSeries{}, sky, options, calibration)

	// Native ICON MH is a provider input, not a configurable clamp. Changing a
	// real calibration term proves that the full calibration struct, rather
	// than a stale field list, participates in the cache identity.
	calibration.GroundCn2Scale *= 1.01
	changed := forecastRenderCacheKey(vertical, surface, cloud, forecast.AtmosphericCompositionSeries{}, sky, options, calibration)
	if baseline == changed {
		t.Fatal("render cache key ignored GroundCn2Scale calibration")
	}
}

func TestRenderCacheKeyIncludesLanguage(t *testing.T) {
	vertical := forecast.SyntheticVerticalFixture()
	surface := forecast.SyntheticSurfaceFixture()
	cloud := forecast.SyntheticCloudFixture()
	calibration := forecast.DefaultOverallIndexCalibration()
	russian := forecastRenderCacheKey(vertical, surface, cloud, forecast.AtmosphericCompositionSeries{}, astronomy.Series{}, render.Options{Width: 3200, Height: 960, Language: "ru"}, calibration)
	english := forecastRenderCacheKey(vertical, surface, cloud, forecast.AtmosphericCompositionSeries{}, astronomy.Series{}, render.Options{Width: 3200, Height: 960, Language: "en"}, calibration)
	if russian == english {
		t.Fatal("render cache key ignored language")
	}
}

func TestRenderCacheKeyChangesWithModelRun(t *testing.T) {
	vertical := forecast.SyntheticVerticalFixture()
	surface := forecast.SyntheticSurfaceFixture()
	cloud := forecast.SyntheticCloudFixture()
	calibration := forecast.DefaultOverallIndexCalibration()
	baseline := forecastRenderCacheKey(vertical, surface, cloud, forecast.AtmosphericCompositionSeries{}, astronomy.Series{}, render.Options{Width: 3200, Height: 960, Language: "ru"}, calibration)
	checks := []struct {
		name   string
		mutate func(*forecast.VerticalSeries, *forecast.SurfaceSeries, *forecast.CloudSeries)
	}{
		{name: "vertical", mutate: func(value *forecast.VerticalSeries, _ *forecast.SurfaceSeries, _ *forecast.CloudSeries) {
			value.RunID = "2026072218"
		}},
		{name: "surface", mutate: func(_ *forecast.VerticalSeries, value *forecast.SurfaceSeries, _ *forecast.CloudSeries) {
			value.RunID = "2026072218"
		}},
		{name: "cloud", mutate: func(_ *forecast.VerticalSeries, _ *forecast.SurfaceSeries, value *forecast.CloudSeries) {
			value.RunID = "2026072218"
		}},
	}
	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			changedVertical, changedSurface, changedCloud := vertical, surface, cloud
			check.mutate(&changedVertical, &changedSurface, &changedCloud)
			changed := forecastRenderCacheKey(changedVertical, changedSurface, changedCloud, forecast.AtmosphericCompositionSeries{}, astronomy.Series{}, render.Options{Width: 3200, Height: 960, Language: "ru"}, calibration)
			if baseline == changed {
				t.Fatalf("render cache ignored %s run", check.name)
			}
		})
	}
}

func TestRenderCacheKeyChangesWithIndependentCompositionIdentity(t *testing.T) {
	vertical := forecast.SyntheticVerticalFixture()
	surface := forecast.SyntheticSurfaceFixture()
	cloud := forecast.SyntheticCloudFixture()
	calibration := forecast.DefaultOverallIndexCalibration()
	options := render.Options{Width: 3200, Height: 960, Language: "ru"}
	composition := forecast.AtmosphericCompositionSeries{
		Provider: "nasa-geos-cf",
		Product:  "xgc_tavg_1hr_glo_L1440x721_slv",
		RunID:    "20260726T000000Z",
		BaseTime: time.Date(2026, time.July, 26, 0, 0, 0, 0, time.UTC),
		Grid:     "0.25x0.25 degree",
		Frames: []forecast.AtmosphericCompositionFrame{{
			ValidAt:                   time.Date(2026, time.July, 26, 3, 0, 0, 0, time.UTC),
			AerosolOpticalDepth550:    0.08,
			TotalColumnOzoneDU:        320,
			Provider:                  "nasa-geos-cf",
			RunID:                     "20260726T000000Z",
			BaseTime:                  time.Date(2026, time.July, 26, 0, 0, 0, 0, time.UTC),
			Grid:                      "0.25x0.25 degree",
			AerosolSpectralAssumption: "Angstrom alpha=1.14",
		}},
	}
	baseline := forecastRenderCacheKey(vertical, surface, cloud, composition, astronomy.Series{}, options, calibration)

	tests := []struct {
		name   string
		mutate func(*forecast.AtmosphericCompositionSeries)
	}{
		{name: "independent run", mutate: func(value *forecast.AtmosphericCompositionSeries) {
			value.RunID = "20260726T010000Z"
		}},
		{name: "aerosol value", mutate: func(value *forecast.AtmosphericCompositionSeries) {
			value.Frames[0].AerosolOpticalDepth550 = 0.09
		}},
		{name: "ozone value", mutate: func(value *forecast.AtmosphericCompositionSeries) {
			value.Frames[0].TotalColumnOzoneDU = 321
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			changed := composition
			changed.Frames = append([]forecast.AtmosphericCompositionFrame(nil), composition.Frames...)
			test.mutate(&changed)
			key := forecastRenderCacheKey(vertical, surface, cloud, changed, astronomy.Series{}, options, calibration)
			if key == baseline {
				t.Fatalf("render cache key ignored changed composition %s", test.name)
			}
		})
	}
}
