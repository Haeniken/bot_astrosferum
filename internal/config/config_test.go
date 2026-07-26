package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadExample(t *testing.T) {
	path := filepath.Join("..", "..", "config", "config.example.yaml")
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.App.Horizon.Duration != 72*time.Hour {
		t.Fatalf("unexpected horizon: %s", cfg.App.Horizon.Duration)
	}
	if cfg.App.RequestTimeout.Duration != 15*time.Minute {
		t.Fatalf("unexpected request timeout: %s", cfg.App.RequestTimeout.Duration)
	}
	if cfg.App.ForecastConcurrency != 2 || cfg.App.ECCodesWorkers != 8 || cfg.App.PointCacheEntries != 512 || cfg.App.PointCacheMemoryLimit != ByteSize(20<<30) {
		t.Fatalf("unexpected performance configuration: %+v", cfg.App)
	}
	if !cfg.HorizonAnalysis.Enabled || cfg.HorizonAnalysis.QueueSize != 4 || cfg.HorizonAnalysis.Concurrency != 1 || cfg.HorizonAnalysis.CDOWorkers != 2 ||
		cfg.HorizonAnalysis.JobTimeout.Duration != 10*time.Minute || cfg.HorizonAnalysis.EstimatedDuration.Duration != 3*time.Minute {
		t.Fatalf("unexpected horizon-analysis configuration: %+v", cfg.HorizonAnalysis)
	}
	if cfg.Sync.MinFreeSpace != ByteSize(150<<30) {
		t.Fatalf("unexpected minimum free space: %d", cfg.Sync.MinFreeSpace)
	}
	if !cfg.Providers.GEOSCF.Enabled || cfg.Providers.GEOSCF.RequestTimeout.Duration != time.Minute ||
		cfg.Providers.GEOSCF.CacheEntries != 256 {
		t.Fatalf("unexpected GEOS-CF configuration: %+v", cfg.Providers.GEOSCF)
	}
	if !cfg.Platforms.Telegram.Enabled || cfg.Platforms.VK.Enabled {
		t.Fatalf("unexpected platform configuration")
	}
}

func TestGEOSCFEnvironmentToggleAndDisabledValidation(t *testing.T) {
	t.Setenv("ASTRO_GEOS_CF_ENABLED", "false")
	cfg, err := Load(filepath.Join("..", "..", "config", "config.example.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Providers.GEOSCF.Enabled {
		t.Fatal("environment did not disable GEOS-CF")
	}
	cfg.Providers.GEOSCF.DatasetURL = ""
	cfg.Providers.GEOSCF.RequestTimeout = Duration{}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("disabled optional GEOS-CF provider rejected: %v", err)
	}

	t.Setenv("ASTRO_GEOS_CF_ENABLED", "not-a-boolean")
	if _, err := Load(filepath.Join("..", "..", "config", "config.example.yaml")); err == nil ||
		!strings.Contains(err.Error(), "ASTRO_GEOS_CF_ENABLED") {
		t.Fatalf("invalid GEOS-CF toggle error = %v", err)
	}
}

func TestEnabledGEOSCFConfigurationValidation(t *testing.T) {
	tests := []func(*GEOSCFConfig){
		func(value *GEOSCFConfig) { value.DatasetURL = "" },
		func(value *GEOSCFConfig) { value.RequestTimeout = Duration{} },
		func(value *GEOSCFConfig) { value.MetadataCacheTTL = Duration{25 * time.Hour} },
		func(value *GEOSCFConfig) { value.DataCacheTTL = Duration{49 * time.Hour} },
		func(value *GEOSCFConfig) { value.MaxStaleAge = Duration{8 * 24 * time.Hour} },
		func(value *GEOSCFConfig) { value.CacheEntries = 0 },
	}
	for index, mutate := range tests {
		cfg := Defaults()
		mutate(&cfg.Providers.GEOSCF)
		if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "providers.geos_cf") {
			t.Fatalf("case %d validation error = %v", index, err)
		}
	}
}

func TestHorizonAnalysisLimitsValidation(t *testing.T) {
	tests := []func(*Config){
		func(cfg *Config) { cfg.HorizonAnalysis.QueueSize = 0 },
		func(cfg *Config) { cfg.HorizonAnalysis.Concurrency = 0 },
		func(cfg *Config) { cfg.HorizonAnalysis.CDOWorkers = 5 },
		func(cfg *Config) { cfg.HorizonAnalysis.JobTimeout = Duration{30 * time.Second} },
		func(cfg *Config) { cfg.HorizonAnalysis.CacheTTL = Duration{30 * time.Minute} },
		func(cfg *Config) { cfg.HorizonAnalysis.CacheEntries = 0 },
		func(cfg *Config) { cfg.HorizonAnalysis.EstimatedDuration = Duration{time.Second} },
	}
	for index, mutate := range tests {
		cfg := Defaults()
		mutate(&cfg)
		if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "horizon_analysis") {
			t.Fatalf("case %d validation error = %v", index, err)
		}
	}
	cfg := Defaults()
	cfg.HorizonAnalysis.Enabled = false
	cfg.HorizonAnalysis.QueueSize = 0
	if err := cfg.Validate(); err != nil {
		t.Fatalf("disabled horizon analysis rejected: %v", err)
	}
}

func TestHorizonAnalysisEnvironmentToggle(t *testing.T) {
	t.Setenv("ASTRO_HORIZON_ANALYSIS_ENABLED", "false")
	cfg, err := Load(filepath.Join("..", "..", "config", "config.example.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HorizonAnalysis.Enabled {
		t.Fatal("environment did not disable horizon analysis")
	}
	t.Setenv("ASTRO_HORIZON_ANALYSIS_ENABLED", "not-a-boolean")
	if _, err := Load(filepath.Join("..", "..", "config", "config.example.yaml")); err == nil || !strings.Contains(err.Error(), "ASTRO_HORIZON_ANALYSIS_ENABLED") {
		t.Fatalf("invalid toggle error = %v", err)
	}
}

func TestForecastConcurrencyEnvironmentOverride(t *testing.T) {
	t.Setenv("ASTRO_FORECAST_CONCURRENCY", "3")
	t.Setenv("ASTRO_HORIZON_CONCURRENCY", "2")
	t.Setenv("ASTRO_ICON_DOWNLOAD_LIMIT_MBIT", "75.5")
	cfg, err := Load(filepath.Join("..", "..", "config", "config.example.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.App.ForecastConcurrency != 3 {
		t.Fatalf("forecast concurrency = %d, want 3", cfg.App.ForecastConcurrency)
	}
	if cfg.HorizonAnalysis.Concurrency != 2 {
		t.Fatalf("horizon concurrency = %d, want 2", cfg.HorizonAnalysis.Concurrency)
	}
	if cfg.Sync.DownloadLimitMbit != 75.5 {
		t.Fatalf("ICON download limit = %g, want 75.5", cfg.Sync.DownloadLimitMbit)
	}
}

func TestLoadRejectsUnknownFields(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "config.yaml")
	content := "app:\n  locale: ru\n  unexpected: true\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), "field unexpected not found") {
		t.Fatalf("expected unknown-field error, got %v", err)
	}
}

func TestOverallIndexEnvironmentOverrides(t *testing.T) {
	t.Setenv("ASTRO_OVERALL_SEEING_WEIGHT", "1.25")
	t.Setenv("ASTRO_OVERALL_CLOUD_WEIGHT", "2.75")
	t.Setenv("ASTRO_OVERALL_COHERENCE_TIME_WEIGHT", "0.3")
	t.Setenv("ASTRO_OVERALL_OPTICAL_TURBULENCE_MAX_PENALTY", "0.2")
	t.Setenv("ASTRO_OVERALL_POSSIBLE_FOG_FACTOR", "0.7")
	t.Setenv("ASTRO_OVERALL_HIGH_FOG_FACTOR", "0.05")
	t.Setenv("ASTRO_OVERALL_PRECIPITATION_DETECT_MM", "0.08")
	t.Setenv("ASTRO_OVERALL_GOOD_SEEING_ARCSEC", "0.6")
	t.Setenv("ASTRO_OVERALL_BAD_SEEING_ARCSEC", "3.0")
	t.Setenv("ASTRO_OVERALL_BEST_COHERENCE_TIME_MS", "6.0")
	t.Setenv("ASTRO_OVERALL_BAD_COHERENCE_TIME_MS", "1.5")
	t.Setenv("ASTRO_OVERALL_BOUNDARY_LAYER_MIN_M", "700")
	t.Setenv("ASTRO_OVERALL_BOUNDARY_LAYER_TOP_M", "1800")
	t.Setenv("ASTRO_OVERALL_GROUND_CN2_SCALE", "1.2")
	t.Setenv("ASTRO_OVERALL_UNRESOLVED_CLOUD_OBSTRUCTION", "0.4")
	t.Setenv("ASTRO_OVERALL_SURFACE_WIND_MAX_PENALTY", "0.15")
	t.Setenv("ASTRO_OVERALL_SURFACE_WIND_START_MS", "9.0")
	t.Setenv("ASTRO_OVERALL_SURFACE_WIND_FULL_MS", "16.0")
	t.Setenv("ASTRO_OVERALL_SURFACE_GUST_START_MS", "13.0")
	t.Setenv("ASTRO_OVERALL_SURFACE_GUST_FULL_MS", "23.0")
	cfg, err := Load(filepath.Join("..", "..", "config", "config.example.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Algorithms.OverallSeeingWeight != 1.25 || cfg.Algorithms.OverallCloudWeight != 2.75 ||
		cfg.Algorithms.OverallCoherenceTimeWeight != 0.3 || cfg.Algorithms.OverallOpticalTurbulenceMaxPenalty != 0.2 ||
		cfg.Algorithms.OverallPossibleFogFactor != 0.7 ||
		cfg.Algorithms.OverallHighFogFactor != 0.05 || cfg.Algorithms.OverallPrecipitationDetectMM != 0.08 ||
		cfg.Algorithms.OverallGoodSeeingArcsec != 0.6 ||
		cfg.Algorithms.OverallBadSeeingArcsec != 3.0 || cfg.Algorithms.OverallBestCoherenceTimeMS != 6.0 ||
		cfg.Algorithms.OverallBadCoherenceTimeMS != 1.5 || cfg.Algorithms.OverallBoundaryLayerMinM != 700 ||
		cfg.Algorithms.OverallBoundaryLayerTopM != 1800 ||
		cfg.Algorithms.OverallGroundCn2Scale != 1.2 || cfg.Algorithms.OverallUnresolvedCloudObstruction != 0.4 ||
		cfg.Algorithms.OverallSurfaceWindMaxPenalty != 0.15 || cfg.Algorithms.OverallSurfaceWindStartMS != 9.0 ||
		cfg.Algorithms.OverallSurfaceWindFullMS != 16.0 || cfg.Algorithms.OverallSurfaceGustStartMS != 13.0 ||
		cfg.Algorithms.OverallSurfaceGustFullMS != 23.0 {
		t.Fatalf("environment overrides were not applied: %+v", cfg.Algorithms)
	}
}

func TestOverallIndexDefaultsMatchForecastCalibration(t *testing.T) {
	cfg := Defaults()
	algorithms := cfg.Algorithms
	if algorithms.SeeingVersion != "seeing-hybrid-tke-mh-hmnsp99-v7" ||
		algorithms.ConditionsVersion != "conditions-v8-precip-veto-penalty-decomposition" ||
		cfg.Render.Version != "render-v16-overall-penalty-decomposition" {
		t.Fatalf("unexpected algorithm/render versions: %+v %+v", algorithms, cfg.Render)
	}
	if algorithms.OverallGoodSeeingArcsec != 0.5 || algorithms.OverallBadSeeingArcsec != 2.0 ||
		algorithms.OverallCoherenceTimeWeight != 0.25 || algorithms.OverallOpticalTurbulenceMaxPenalty != 0.25 ||
		algorithms.OverallPossibleFogFactor != 0.75 ||
		algorithms.OverallPrecipitationDetectMM != 0.05 ||
		algorithms.OverallBestCoherenceTimeMS != 5.2 || algorithms.OverallBadCoherenceTimeMS != 1.6 ||
		algorithms.OverallBoundaryLayerMinM != 500 || algorithms.OverallBoundaryLayerTopM != 2000 ||
		algorithms.OverallGroundCn2Scale != 1 ||
		algorithms.OverallUnresolvedCloudObstruction != 0.45 || algorithms.OverallSurfaceWindMaxPenalty != 0.20 ||
		algorithms.OverallSurfaceWindStartMS != 8.5 || algorithms.OverallSurfaceWindFullMS != 15 ||
		algorithms.OverallSurfaceGustStartMS != 12 || algorithms.OverallSurfaceGustFullMS != 22 {
		t.Fatalf("unexpected overall-index defaults: %+v", algorithms)
	}
}

func TestOverallBoundaryLayerBoundsValidation(t *testing.T) {
	tests := []struct {
		minimum float64
		maximum float64
	}{
		{minimum: 99, maximum: 2000},
		{minimum: 2100, maximum: 2000},
		{minimum: 500, maximum: 4001},
	}
	for _, test := range tests {
		cfg := Defaults()
		cfg.Algorithms.OverallBoundaryLayerMinM = test.minimum
		cfg.Algorithms.OverallBoundaryLayerTopM = test.maximum
		if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "boundary-layer bounds") {
			t.Fatalf("bounds [%v, %v] validation error = %v", test.minimum, test.maximum, err)
		}
	}
}

func TestOverallOpticalTurbulenceMaximumPenaltyValidation(t *testing.T) {
	for _, value := range []float64{-0.01, 1.01} {
		cfg := Defaults()
		cfg.Algorithms.OverallOpticalTurbulenceMaxPenalty = value
		if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "optical_turbulence_max_penalty") {
			t.Fatalf("maximum penalty %v validation error = %v", value, err)
		}
	}
}

func TestLightPollutionAtlasYearEnvironmentOverride(t *testing.T) {
	t.Setenv("ASTRO_LIGHT_POLLUTION_ATLAS_YEAR", "2023")
	cfg, err := Load(filepath.Join("..", "..", "config", "config.example.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Providers.LightPollution.AtlasYear != 2023 {
		t.Fatalf("atlas year = %d, want 2023", cfg.Providers.LightPollution.AtlasYear)
	}
}

func TestDatabaseAndAdminEnvironment(t *testing.T) {
	t.Setenv("ASTRO_DB_PASSWORD", "secret-for-test")
	t.Setenv("ASTRO_TELEGRAM_ADMIN_IDS", "1001,1002")
	t.Setenv("ASTRO_VK_ADMIN_IDS", "2001,2002")
	cfg, err := Load(filepath.Join("..", "..", "config", "config.example.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Database.Password != "secret-for-test" || cfg.Database.Host != "postgres" || len(cfg.Platforms.Telegram.AdminIDs) != 2 || cfg.Platforms.Telegram.AdminIDs[1] != 1002 || len(cfg.Platforms.VK.AdminIDs) != 2 || cfg.Platforms.VK.AdminIDs[1] != 2002 {
		t.Fatalf("unexpected database/admin configuration: %+v telegram=%v vk=%v", cfg.Database, cfg.Platforms.Telegram.AdminIDs, cfg.Platforms.VK.AdminIDs)
	}
}

func TestEnabledVKRequiresGroupID(t *testing.T) {
	cfg := Defaults()
	cfg.Platforms.VK.Enabled = true
	cfg.Platforms.VK.TokenFile = "/run/secrets/vk_token"
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "platforms.vk.group_id") {
		t.Fatalf("unexpected validation error: %v", err)
	}
	cfg.Platforms.VK.GroupID = 240376006
	if err := cfg.Validate(); err != nil {
		t.Fatalf("valid VK configuration rejected: %v", err)
	}
}

func TestEmptyAdminEnvironmentDisablesAdminAccess(t *testing.T) {
	t.Setenv("ASTRO_TELEGRAM_ADMIN_IDS", "")
	cfg, err := Load(filepath.Join("..", "..", "config", "config.example.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Platforms.Telegram.AdminIDs) != 0 {
		t.Fatalf("empty admin environment produced IDs: %v", cfg.Platforms.Telegram.AdminIDs)
	}
}

func TestParseByteSize(t *testing.T) {
	tests := map[string]int64{
		"150GiB": 150 << 30,
		"1MiB":   1 << 20,
		"10GB":   10_000_000_000,
		"42B":    42,
	}
	for input, expected := range tests {
		actual, err := parseByteSize(input)
		if err != nil {
			t.Fatalf("parse %q: %v", input, err)
		}
		if actual != expected {
			t.Fatalf("parse %q = %d; want %d", input, actual, expected)
		}
	}
	for _, input := range []string{"", "-1GiB", "12", "1.5GiB", "999XB"} {
		if _, err := parseByteSize(input); err == nil {
			t.Fatalf("expected %q to fail", input)
		}
	}
}
