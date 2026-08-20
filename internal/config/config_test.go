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
	if cfg.App.ForecastConcurrency != 2 || cfg.App.ForecastEstimatedDuration.Duration != 2*time.Minute ||
		cfg.App.ForecastWarmEstimatedDuration.Duration != 10*time.Second || cfg.App.ECCodesWorkers != 8 ||
		cfg.App.PointCacheEntries != 512 || cfg.App.PointCacheMemoryLimit != ByteSize(20<<30) {
		t.Fatalf("unexpected performance configuration: %+v", cfg.App)
	}
	if !cfg.HorizonAnalysis.Enabled || cfg.HorizonAnalysis.QueueSize != 4 || cfg.HorizonAnalysis.Concurrency != 1 || cfg.HorizonAnalysis.CDOWorkers != 8 ||
		cfg.HorizonAnalysis.JobTimeout.Duration != 10*time.Minute || cfg.HorizonAnalysis.EstimatedDuration.Duration != 3*time.Minute {
		t.Fatalf("unexpected horizon-analysis configuration: %+v", cfg.HorizonAnalysis)
	}
	if cfg.Directional.QueueSize != 10 || cfg.Directional.Concurrency != 1 || cfg.Directional.Listen != ":18083" || cfg.Directional.WorkerURL != "http://directional_worker:18084" ||
		cfg.Directional.WorkerListen != ":18084" || cfg.Directional.EstimatedAstrodome.Duration != 30*time.Minute ||
		cfg.Directional.InternalRequestTimeout.Duration != 0 {
		t.Fatalf("unexpected directional configuration: %+v", cfg.Directional)
	}
	if !cfg.Astrodome.Enabled || cfg.Astrodome.JobTimeout.Duration != 0 || cfg.Astrodome.ResidentLimit != ByteSize(10<<30) || cfg.Astrodome.ProjectDiskCap != ByteSize(400<<30) {
		t.Fatalf("unexpected astrodome configuration: %+v", cfg.Astrodome)
	}
	if !cfg.Terrain.CopernicusDEMGLO30Enabled || cfg.Terrain.CacheLimit != ByteSize(20<<30) {
		t.Fatalf("unexpected terrain configuration: %+v", cfg.Terrain)
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

func TestCopernicusDEMTerrainEnvironmentAndLimits(t *testing.T) {
	t.Setenv("ASTRO_COPERNICUS_DEM_GLO30_ENABLED", "false")
	t.Setenv("ASTRO_COPERNICUS_DEM_CACHE_LIMIT", "12GiB")
	cfg, err := Load(filepath.Join("..", "..", "config", "config.example.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Terrain.CopernicusDEMGLO30Enabled || cfg.Terrain.CacheLimit != ByteSize(12<<30) {
		t.Fatalf("terrain environment overrides not applied: %+v", cfg.Terrain)
	}
	for _, invalid := range []ByteSize{ByteSize(1<<30) - 1, ByteSize(100<<30) + 1} {
		candidate := Defaults()
		candidate.Terrain.CacheLimit = invalid
		if err := candidate.Validate(); err == nil || !strings.Contains(err.Error(), "terrain.cache_limit") {
			t.Fatalf("invalid terrain cache limit %d accepted: %v", invalid, err)
		}
	}
	t.Setenv("ASTRO_COPERNICUS_DEM_GLO30_ENABLED", "not-a-boolean")
	if _, err := Load(filepath.Join("..", "..", "config", "config.example.yaml")); err == nil ||
		!strings.Contains(err.Error(), "ASTRO_COPERNICUS_DEM_GLO30_ENABLED") {
		t.Fatalf("invalid terrain toggle error = %v", err)
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

func TestForecastPresentationEstimateValidation(t *testing.T) {
	for index, mutate := range []func(*Config){
		func(cfg *Config) { cfg.App.ForecastEstimatedDuration = Duration{500 * time.Millisecond} },
		func(cfg *Config) { cfg.App.ForecastWarmEstimatedDuration = Duration{0} },
		func(cfg *Config) { cfg.App.ForecastWarmEstimatedDuration = Duration{3 * time.Minute} },
	} {
		cfg := Defaults()
		mutate(&cfg)
		if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "forecast_") {
			t.Fatalf("case %d validation error = %v", index, err)
		}
	}
}

func TestHorizonAnalysisLimitsValidation(t *testing.T) {
	tests := []func(*Config){
		func(cfg *Config) { cfg.HorizonAnalysis.QueueSize = 0 },
		func(cfg *Config) { cfg.HorizonAnalysis.Concurrency = 0 },
		func(cfg *Config) { cfg.HorizonAnalysis.CDOWorkers = 17 },
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

func TestAstrodomeAndDirectionalConfiguration(t *testing.T) {
	t.Setenv("ASTRO_ASTRODOME_ENABLED", "false")
	t.Setenv("ASTRO_DIRECTIONAL_QUEUE_SIZE", "7")
	t.Setenv("ASTRO_DIRECTIONAL_CONCURRENCY", "2")
	cfg, err := Load(filepath.Join("..", "..", "config", "config.example.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Astrodome.Enabled || cfg.Directional.QueueSize != 7 || cfg.Directional.Concurrency != 2 {
		t.Fatalf("environment overrides not applied: astrodome=%t directional=%+v", cfg.Astrodome.Enabled, cfg.Directional)
	}
	maximum := Defaults()
	maximum.Directional.Concurrency = 32
	maximum.Astrodome.JobTimeout = Duration{time.Hour}
	maximum.Directional.InternalRequestTimeout = Duration{8 * time.Hour}
	if err := maximum.Validate(); err != nil {
		t.Fatalf("maximum directional/Astrodome limits rejected: %v", err)
	}

	for index, mutate := range []func(*Config){
		func(value *Config) { value.Directional.QueueSize = 11 },
		func(value *Config) { value.Directional.Concurrency = 33 },
		func(value *Config) { value.Directional.Listen = "18083" },
		func(value *Config) { value.Directional.WorkerURL = "https://public.example" },
		func(value *Config) { value.Directional.WorkerListen = "18084" },
		func(value *Config) { value.Directional.CredentialFile = "" },
		func(value *Config) { value.Directional.CompletedEntries = 0 },
		func(value *Config) { value.Directional.InternalRequestTimeout = Duration{time.Hour} },
		func(value *Config) {
			value.Astrodome.JobTimeout = Duration{time.Hour}
			value.Directional.InternalRequestTimeout = Duration{time.Hour}
		},
		func(value *Config) { value.Directional.InternalRequestTimeout = Duration{-time.Second} },
		func(value *Config) { value.Astrodome.JobTimeout = Duration{30 * time.Second} },
		func(value *Config) { value.Astrodome.JobTimeout = Duration{-time.Second} },
		func(value *Config) { value.Astrodome.JobTimeout = Duration{time.Hour + time.Nanosecond} },
		func(value *Config) { value.Astrodome.CacheEntries = 0 },
		func(value *Config) { value.Astrodome.ResidentLimit = ByteSize(512 << 20) },
		func(value *Config) { value.Astrodome.ResidentLimit = ByteSize(21 << 30) },
		func(value *Config) { value.Astrodome.ProjectDiskCap = ByteSize(401 << 30) },
		func(value *Config) { value.Astrodome.MinFreeInodes = 0 },
	} {
		candidate := Defaults()
		mutate(&candidate)
		if err := candidate.Validate(); err == nil {
			t.Fatalf("invalid directional/astrodome case %d accepted", index)
		}
	}

	// Queue estimates are UI metadata, not execution deadlines. A finite job
	// deadline may therefore be shorter than the displayed cold-run estimate;
	// disabling the transport deadline remains valid in that configuration.
	finiteJob := Defaults()
	finiteJob.Astrodome.JobTimeout = Duration{time.Minute}
	finiteJob.Directional.EstimatedAstrodome = Duration{30 * time.Minute}
	if err := finiteJob.Validate(); err != nil {
		t.Fatalf("independent Astrodome estimate rejected: %v", err)
	}

	t.Setenv("ASTRO_ASTRODOME_ENABLED", "not-a-boolean")
	if _, err := Load(filepath.Join("..", "..", "config", "config.example.yaml")); err == nil || !strings.Contains(err.Error(), "ASTRO_ASTRODOME_ENABLED") {
		t.Fatalf("invalid ASTRO_ASTRODOME_ENABLED error = %v", err)
	}
}

func TestAstrodomeTimeoutEnvironmentOverrides(t *testing.T) {
	t.Setenv("ASTRO_ASTRODOME_JOB_TIMEOUT", "45m")
	t.Setenv("ASTRO_DIRECTIONAL_INTERNAL_REQUEST_TIMEOUT", "1h")
	cfg, err := Load(filepath.Join("..", "..", "config", "config.example.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Astrodome.JobTimeout.Duration != 45*time.Minute || cfg.Directional.InternalRequestTimeout.Duration != time.Hour {
		t.Fatalf("timeout overrides not applied: job=%s transport=%s", cfg.Astrodome.JobTimeout.Duration, cfg.Directional.InternalRequestTimeout.Duration)
	}

	t.Setenv("ASTRO_ASTRODOME_JOB_TIMEOUT", "not-a-duration")
	if _, err := Load(filepath.Join("..", "..", "config", "config.example.yaml")); err == nil ||
		!strings.Contains(err.Error(), "ASTRO_ASTRODOME_JOB_TIMEOUT") {
		t.Fatalf("invalid job-timeout error = %v", err)
	}
}

func TestAstrodomeAdminPreviewKeepsRuntimeConfigurationStrict(t *testing.T) {
	cfg := Defaults()
	cfg.HorizonAnalysis.Enabled = false
	cfg.Astrodome.Enabled = false
	cfg.Platforms.Telegram.AdminIDs = []int64{42}
	cfg.Directional.QueueSize = 0
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "directional.queue_size") {
		t.Fatalf("admin preview did not validate directional runtime: %v", err)
	}

	cfg.Directional.QueueSize = Defaults().Directional.QueueSize
	cfg.Directional.Concurrency = Defaults().Directional.Concurrency
	for _, workers := range []int{0, 17} {
		cfg.HorizonAnalysis.CDOWorkers = workers
		if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "horizon_analysis.cdo_workers") {
			t.Fatalf("admin preview accepted cdo_workers=%d: %v", workers, err)
		}
	}
	cfg.HorizonAnalysis.CDOWorkers = Defaults().HorizonAnalysis.CDOWorkers
	cfg.Astrodome.JobTimeout = Duration{30 * time.Second}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "astrodome.job_timeout") {
		t.Fatalf("admin preview did not validate Astrodome runtime: %v", err)
	}

	// With no public rollout, no admins, and Horizon disabled, no directional
	// process is composed and its dormant bounds need not block the bot.
	cfg.Platforms.Telegram.AdminIDs = nil
	if err := cfg.Validate(); err != nil {
		t.Fatalf("fully disabled directional runtime rejected: %v", err)
	}
}

func TestAstrodomeResidentLimitEnvironmentOverride(t *testing.T) {
	t.Setenv("ASTRO_ASTRODOME_RESIDENT_LIMIT", "12GiB")
	cfg, err := Load(filepath.Join("..", "..", "config", "config.example.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Astrodome.ResidentLimit != ByteSize(12<<30) {
		t.Fatalf("resident limit = %d, want %d", cfg.Astrodome.ResidentLimit, ByteSize(12<<30))
	}

	t.Setenv("ASTRO_ASTRODOME_RESIDENT_LIMIT", "not-a-size")
	if _, err := Load(filepath.Join("..", "..", "config", "config.example.yaml")); err == nil ||
		!strings.Contains(err.Error(), "ASTRO_ASTRODOME_RESIDENT_LIMIT") {
		t.Fatalf("invalid resident-limit error = %v", err)
	}
}

func TestForecastConcurrencyEnvironmentOverride(t *testing.T) {
	t.Setenv("ASTRO_FORECAST_CONCURRENCY", "3")
	t.Setenv("ASTRO_FORECAST_ESTIMATED_DURATION", "90s")
	t.Setenv("ASTRO_FORECAST_WARM_ESTIMATED_DURATION", "7s")
	t.Setenv("ASTRO_HORIZON_CONCURRENCY", "2")
	t.Setenv("ASTRO_ICON_DOWNLOAD_LIMIT_MBIT", "75.5")
	cfg, err := Load(filepath.Join("..", "..", "config", "config.example.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.App.ForecastConcurrency != 3 {
		t.Fatalf("forecast concurrency = %d, want 3", cfg.App.ForecastConcurrency)
	}
	if cfg.App.ForecastEstimatedDuration.Duration != 90*time.Second || cfg.App.ForecastWarmEstimatedDuration.Duration != 7*time.Second {
		t.Fatalf("forecast estimates = %s/%s, want 1m30s/7s", cfg.App.ForecastEstimatedDuration.Duration, cfg.App.ForecastWarmEstimatedDuration.Duration)
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
		cfg.Algorithms.OverallBadCoherenceTimeMS != 1.5 ||
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
	if algorithms.SeeingVersion != "seeing-hybrid-tke-native-mh-hmnsp99-logp-v9" ||
		algorithms.ConditionsVersion != "conditions-v8-precip-veto-penalty-decomposition" ||
		cfg.Render.Version != "render-v18-celestial-distance" {
		t.Fatalf("unexpected algorithm/render versions: %+v %+v", algorithms, cfg.Render)
	}
	if algorithms.OverallGoodSeeingArcsec != 0.5 || algorithms.OverallBadSeeingArcsec != 2.0 ||
		algorithms.OverallCoherenceTimeWeight != 0.25 || algorithms.OverallOpticalTurbulenceMaxPenalty != 0.25 ||
		algorithms.OverallPossibleFogFactor != 0.75 ||
		algorithms.OverallPrecipitationDetectMM != 0.05 ||
		algorithms.OverallBestCoherenceTimeMS != 5.2 || algorithms.OverallBadCoherenceTimeMS != 1.6 ||
		algorithms.OverallGroundCn2Scale != 1 ||
		algorithms.OverallUnresolvedCloudObstruction != 0.45 || algorithms.OverallSurfaceWindMaxPenalty != 0.20 ||
		algorithms.OverallSurfaceWindStartMS != 8.5 || algorithms.OverallSurfaceWindFullMS != 15 ||
		algorithms.OverallSurfaceGustStartMS != 12 || algorithms.OverallSurfaceGustFullMS != 22 {
		t.Fatalf("unexpected overall-index defaults: %+v", algorithms)
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
