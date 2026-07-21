package telegram

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
	baseline := forecastRenderCacheKey(vertical, surface, cloud, sky, options, calibration)

	// BoundaryLayerMinM was added with dynamic ICON MH. Changing it
	// proves that the full calibration struct, rather than a stale field list,
	// participates in the cache identity.
	calibration.BoundaryLayerMinM++
	changed := forecastRenderCacheKey(vertical, surface, cloud, sky, options, calibration)
	if baseline == changed {
		t.Fatal("render cache key ignored BoundaryLayerMinM calibration")
	}
}

func TestRenderCacheKeyIncludesLanguage(t *testing.T) {
	vertical := forecast.SyntheticVerticalFixture()
	surface := forecast.SyntheticSurfaceFixture()
	cloud := forecast.SyntheticCloudFixture()
	calibration := forecast.DefaultOverallIndexCalibration()
	russian := forecastRenderCacheKey(vertical, surface, cloud, astronomy.Series{}, render.Options{Width: 3200, Height: 960, Language: "ru"}, calibration)
	english := forecastRenderCacheKey(vertical, surface, cloud, astronomy.Series{}, render.Options{Width: 3200, Height: 960, Language: "en"}, calibration)
	if russian == english {
		t.Fatal("render cache key ignored language")
	}
}

func TestUpdateShardPreservesChatAffinity(t *testing.T) {
	first := Update{ID: 1, Message: &Message{Chat: Chat{ID: -12345}}}
	second := Update{ID: 2, Message: &Message{Chat: Chat{ID: -12345}}}
	if updateShard(first, 6) != updateShard(second, 6) {
		t.Fatal("updates for one chat must use one worker")
	}
	if updateShard(Update{ID: 3}, 6) != 0 {
		t.Fatal("updates without messages must use shard zero")
	}
}
