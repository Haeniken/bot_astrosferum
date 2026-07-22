package config

import (
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	App             AppConfig             `yaml:"app"`
	HorizonAnalysis HorizonAnalysisConfig `yaml:"horizon_analysis"`
	Paths           PathsConfig           `yaml:"paths"`
	Providers       ProvidersConfig       `yaml:"providers"`
	Sync            SyncConfig            `yaml:"sync"`
	Algorithms      AlgorithmsConfig      `yaml:"algorithms"`
	Render          RenderConfig          `yaml:"render"`
	Platforms       PlatformsConfig       `yaml:"platforms"`
	Database        DatabaseConfig        `yaml:"database"`
}

// HorizonAnalysisConfig deliberately exposes only operational limits. The
// scientific geometry and formula versions are constants in internal/forecast
// so a configuration edit cannot silently change the meaning of cached data.
type HorizonAnalysisConfig struct {
	Enabled           bool     `yaml:"enabled"`
	QueueSize         int      `yaml:"queue_size"`
	CDOWorkers        int      `yaml:"cdo_workers"`
	JobTimeout        Duration `yaml:"job_timeout"`
	CacheTTL          Duration `yaml:"cache_ttl"`
	CacheEntries      int      `yaml:"cache_entries"`
	EstimatedDuration Duration `yaml:"estimated_duration"`
}

type DatabaseConfig struct {
	Host     string `yaml:"host"`
	Port     int    `yaml:"port"`
	Name     string `yaml:"name"`
	User     string `yaml:"user"`
	Password string `yaml:"-"`
	MaxConns int32  `yaml:"max_conns"`
}

type AppConfig struct {
	Locale                string   `yaml:"locale"`
	Horizon               Duration `yaml:"horizon"`
	Step                  Duration `yaml:"step"`
	Workers               int      `yaml:"workers"`
	ECCodesWorkers        int      `yaml:"eccodes_workers"`
	PointCacheEntries     int      `yaml:"point_cache_entries"`
	PointCacheMemoryLimit ByteSize `yaml:"point_cache_memory_limit"`
	RequestTimeout        Duration `yaml:"request_timeout"`
}

type PathsConfig struct {
	Data string `yaml:"data"`
	Temp string `yaml:"temp"`
}

type ProvidersConfig struct {
	ICONEU         ProviderConfig       `yaml:"icon_eu"`
	ICONGlobal     ProviderConfig       `yaml:"icon_global"`
	ICONRu         ProviderConfig       `yaml:"icon_ru"`
	LightPollution LightPollutionConfig `yaml:"light_pollution"`
}

type LightPollutionConfig struct {
	AtlasYear int `yaml:"atlas_year"`
}

type ProviderConfig struct {
	Enabled     bool     `yaml:"enabled"`
	Role        string   `yaml:"role"`
	KeepRuns    int      `yaml:"keep_runs"`
	MaxStaleAge Duration `yaml:"max_stale_age"`
}

type SyncConfig struct {
	PollInterval        Duration `yaml:"poll_interval"`
	DownloadParallelism int      `yaml:"download_parallelism"`
	MinFreeSpace        ByteSize `yaml:"min_free_space"`
}

type AlgorithmsConfig struct {
	SeeingVersion                      string  `yaml:"seeing_version"`
	DewVersion                         string  `yaml:"dew_version"`
	ConditionsVersion                  string  `yaml:"conditions_version"`
	OverallSeeingWeight                float64 `yaml:"overall_seeing_weight"`
	OverallCloudWeight                 float64 `yaml:"overall_cloud_weight"`
	OverallCoherenceTimeWeight         float64 `yaml:"overall_coherence_time_weight"`
	OverallOpticalTurbulenceMaxPenalty float64 `yaml:"overall_optical_turbulence_max_penalty"`
	OverallPossibleFogFactor           float64 `yaml:"overall_possible_fog_factor"`
	OverallHighFogFactor               float64 `yaml:"overall_high_fog_factor"`
	OverallGoodSeeingArcsec            float64 `yaml:"overall_good_seeing_arcsec"`
	OverallBadSeeingArcsec             float64 `yaml:"overall_bad_seeing_arcsec"`
	OverallBestCoherenceTimeMS         float64 `yaml:"overall_best_coherence_time_ms"`
	OverallBadCoherenceTimeMS          float64 `yaml:"overall_bad_coherence_time_ms"`
	OverallBoundaryLayerMinM           float64 `yaml:"overall_boundary_layer_min_m"`
	OverallBoundaryLayerTopM           float64 `yaml:"overall_boundary_layer_top_m"`
	OverallGroundCn2Scale              float64 `yaml:"overall_ground_cn2_scale"`
	OverallUnresolvedCloudObstruction  float64 `yaml:"overall_unresolved_cloud_obstruction"`
	OverallSurfaceWindMaxPenalty       float64 `yaml:"overall_surface_wind_max_penalty"`
	OverallSurfaceWindStartMS          float64 `yaml:"overall_surface_wind_start_ms"`
	OverallSurfaceWindFullMS           float64 `yaml:"overall_surface_wind_full_ms"`
	OverallSurfaceGustStartMS          float64 `yaml:"overall_surface_gust_start_ms"`
	OverallSurfaceGustFullMS           float64 `yaml:"overall_surface_gust_full_ms"`
	CloudLiquidRadiusMicrometers       float64 `yaml:"cloud_liquid_radius_micrometers"`
	CloudIceRadiusMicrometers          float64 `yaml:"cloud_ice_radius_micrometers"`
}

type RenderConfig struct {
	Version string `yaml:"version"`
	Width   int    `yaml:"width"`
	Height  int    `yaml:"height"`
}

type PlatformsConfig struct {
	Telegram PlatformConfig `yaml:"telegram"`
	VK       PlatformConfig `yaml:"vk"`
}

type PlatformConfig struct {
	Enabled   bool    `yaml:"enabled"`
	TokenFile string  `yaml:"token_file"`
	GroupID   int64   `yaml:"group_id,omitempty"`
	AdminIDs  []int64 `yaml:"admin_ids"`
}

func Defaults() Config {
	return Config{
		App: AppConfig{
			Locale:                "ru",
			Horizon:               Duration{72 * time.Hour},
			Step:                  Duration{3 * time.Hour},
			Workers:               6,
			ECCodesWorkers:        8,
			PointCacheEntries:     512,
			PointCacheMemoryLimit: ByteSize(20 << 30),
			RequestTimeout:        Duration{15 * time.Minute},
		},
		HorizonAnalysis: HorizonAnalysisConfig{
			Enabled: true, QueueSize: 4, CDOWorkers: 2,
			JobTimeout: Duration{10 * time.Minute}, CacheTTL: Duration{48 * time.Hour},
			CacheEntries: 128, EstimatedDuration: Duration{3 * time.Minute},
		},
		Paths: PathsConfig{Data: "/app/data", Temp: "/app/data/tmp"},
		Providers: ProvidersConfig{
			LightPollution: LightPollutionConfig{AtlasYear: 2024},
		},
		Sync: SyncConfig{
			PollInterval:        Duration{15 * time.Minute},
			DownloadParallelism: 4,
			MinFreeSpace:        ByteSize(150 << 30),
		},
		Algorithms: AlgorithmsConfig{
			SeeingVersion:                      "seeing-hybrid-tke-mh-hmnsp99-v6",
			DewVersion:                         "dew-v1",
			ConditionsVersion:                  "conditions-v7-phase-structure-coherence",
			OverallSeeingWeight:                1,
			OverallCloudWeight:                 2,
			OverallCoherenceTimeWeight:         0.25,
			OverallOpticalTurbulenceMaxPenalty: 0.25,
			OverallPossibleFogFactor:           0.75,
			OverallHighFogFactor:               0.10,
			OverallGoodSeeingArcsec:            0.5,
			OverallBadSeeingArcsec:             2.0,
			OverallBestCoherenceTimeMS:         5.2,
			OverallBadCoherenceTimeMS:          1.6,
			OverallBoundaryLayerMinM:           500,
			OverallBoundaryLayerTopM:           2000,
			OverallGroundCn2Scale:              1,
			OverallUnresolvedCloudObstruction:  0.45,
			OverallSurfaceWindMaxPenalty:       0.20,
			OverallSurfaceWindStartMS:          8.5,
			OverallSurfaceWindFullMS:           15,
			OverallSurfaceGustStartMS:          12,
			OverallSurfaceGustFullMS:           22,
			CloudLiquidRadiusMicrometers:       10,
			CloudIceRadiusMicrometers:          25,
		},
		Render:   RenderConfig{Version: "render-v9-dynamic-mh", Width: 1280, Height: 960},
		Database: DatabaseConfig{Host: "postgres", Port: 5432, Name: "bot_astrosferum", User: "bot_astrosferum", MaxConns: 10},
	}
}

func Load(path string) (Config, error) {
	file, err := os.Open(path)
	if err != nil {
		return Config{}, fmt.Errorf("open config: %w", err)
	}
	defer func() { _ = file.Close() }()

	result := Defaults()
	decoder := yaml.NewDecoder(file)
	decoder.KnownFields(true)
	if err := decoder.Decode(&result); err != nil {
		return Config{}, fmt.Errorf("decode config: %w", err)
	}
	if err := ensureSingleDocument(decoder); err != nil {
		return Config{}, err
	}
	if err := result.applyEnvironment(); err != nil {
		return Config{}, err
	}
	if err := result.Validate(); err != nil {
		return Config{}, err
	}
	return result, nil
}

func (c *Config) applyEnvironment() error {
	if value, exists := os.LookupEnv("ASTRO_HORIZON_ANALYSIS_ENABLED"); exists {
		parsed, err := strconv.ParseBool(strings.TrimSpace(value))
		if err != nil {
			return fmt.Errorf("parse ASTRO_HORIZON_ANALYSIS_ENABLED: %w", err)
		}
		c.HorizonAnalysis.Enabled = parsed
	}
	overrides := []struct {
		name   string
		target *float64
	}{
		{name: "ASTRO_OVERALL_SEEING_WEIGHT", target: &c.Algorithms.OverallSeeingWeight},
		{name: "ASTRO_OVERALL_CLOUD_WEIGHT", target: &c.Algorithms.OverallCloudWeight},
		{name: "ASTRO_OVERALL_COHERENCE_TIME_WEIGHT", target: &c.Algorithms.OverallCoherenceTimeWeight},
		{name: "ASTRO_OVERALL_OPTICAL_TURBULENCE_MAX_PENALTY", target: &c.Algorithms.OverallOpticalTurbulenceMaxPenalty},
		{name: "ASTRO_OVERALL_POSSIBLE_FOG_FACTOR", target: &c.Algorithms.OverallPossibleFogFactor},
		{name: "ASTRO_OVERALL_HIGH_FOG_FACTOR", target: &c.Algorithms.OverallHighFogFactor},
		{name: "ASTRO_OVERALL_GOOD_SEEING_ARCSEC", target: &c.Algorithms.OverallGoodSeeingArcsec},
		{name: "ASTRO_OVERALL_BAD_SEEING_ARCSEC", target: &c.Algorithms.OverallBadSeeingArcsec},
		{name: "ASTRO_OVERALL_BEST_COHERENCE_TIME_MS", target: &c.Algorithms.OverallBestCoherenceTimeMS},
		{name: "ASTRO_OVERALL_BAD_COHERENCE_TIME_MS", target: &c.Algorithms.OverallBadCoherenceTimeMS},
		{name: "ASTRO_OVERALL_BOUNDARY_LAYER_MIN_M", target: &c.Algorithms.OverallBoundaryLayerMinM},
		{name: "ASTRO_OVERALL_BOUNDARY_LAYER_TOP_M", target: &c.Algorithms.OverallBoundaryLayerTopM},
		{name: "ASTRO_OVERALL_GROUND_CN2_SCALE", target: &c.Algorithms.OverallGroundCn2Scale},
		{name: "ASTRO_OVERALL_UNRESOLVED_CLOUD_OBSTRUCTION", target: &c.Algorithms.OverallUnresolvedCloudObstruction},
		{name: "ASTRO_OVERALL_SURFACE_WIND_MAX_PENALTY", target: &c.Algorithms.OverallSurfaceWindMaxPenalty},
		{name: "ASTRO_OVERALL_SURFACE_WIND_START_MS", target: &c.Algorithms.OverallSurfaceWindStartMS},
		{name: "ASTRO_OVERALL_SURFACE_WIND_FULL_MS", target: &c.Algorithms.OverallSurfaceWindFullMS},
		{name: "ASTRO_OVERALL_SURFACE_GUST_START_MS", target: &c.Algorithms.OverallSurfaceGustStartMS},
		{name: "ASTRO_OVERALL_SURFACE_GUST_FULL_MS", target: &c.Algorithms.OverallSurfaceGustFullMS},
		{name: "ASTRO_CLOUD_LIQUID_RADIUS_MICROMETERS", target: &c.Algorithms.CloudLiquidRadiusMicrometers},
		{name: "ASTRO_CLOUD_ICE_RADIUS_MICROMETERS", target: &c.Algorithms.CloudIceRadiusMicrometers},
	}
	for _, override := range overrides {
		value, exists := os.LookupEnv(override.name)
		if !exists {
			continue
		}
		parsed, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
		if err != nil {
			return fmt.Errorf("parse %s: %w", override.name, err)
		}
		*override.target = parsed
	}
	if value, exists := os.LookupEnv("ASTRO_LIGHT_POLLUTION_ATLAS_YEAR"); exists {
		parsed, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil {
			return fmt.Errorf("parse ASTRO_LIGHT_POLLUTION_ATLAS_YEAR: %w", err)
		}
		c.Providers.LightPollution.AtlasYear = parsed
	}
	if value, exists := os.LookupEnv("ASTRO_DB_PASSWORD"); exists {
		c.Database.Password = value
	}
	if err := applyAdminIDs("ASTRO_TELEGRAM_ADMIN_IDS", &c.Platforms.Telegram.AdminIDs); err != nil {
		return err
	}
	if err := applyAdminIDs("ASTRO_VK_ADMIN_IDS", &c.Platforms.VK.AdminIDs); err != nil {
		return err
	}
	return nil
}

func applyAdminIDs(environmentName string, target *[]int64) error {
	value, exists := os.LookupEnv(environmentName)
	if !exists {
		return nil
	}
	*target = nil
	if strings.TrimSpace(value) == "" {
		return nil
	}
	for _, item := range strings.Split(value, ",") {
		id, err := strconv.ParseInt(strings.TrimSpace(item), 10, 64)
		if err != nil || id <= 0 {
			return fmt.Errorf("parse %s: invalid id %q", environmentName, item)
		}
		*target = append(*target, id)
	}
	return nil
}

func ensureSingleDocument(decoder *yaml.Decoder) error {
	var extra any
	err := decoder.Decode(&extra)
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("decode trailing config document: %w", err)
	}
	return fmt.Errorf("config must contain exactly one YAML document")
}

func (c Config) Validate() error {
	var problems []string
	if c.App.Locale != "ru" && c.App.Locale != "en" {
		problems = append(problems, "app.locale must be ru or en")
	}
	if c.App.Horizon.Duration <= 0 {
		problems = append(problems, "app.horizon must be positive")
	}
	if c.App.Step.Duration <= 0 || c.App.Step.Duration > c.App.Horizon.Duration {
		problems = append(problems, "app.step must be positive and no greater than app.horizon")
	}
	if c.App.Workers < 1 || c.App.Workers > 64 {
		problems = append(problems, "app.workers must be between 1 and 64")
	}
	if c.App.ECCodesWorkers < 1 || c.App.ECCodesWorkers > 64 {
		problems = append(problems, "app.eccodes_workers must be between 1 and 64")
	}
	if c.App.PointCacheEntries < 1 || c.App.PointCacheEntries > 4096 {
		problems = append(problems, "app.point_cache_entries must be between 1 and 4096")
	}
	if c.App.PointCacheMemoryLimit < ByteSize(64<<20) || c.App.PointCacheMemoryLimit > ByteSize(30<<30) {
		problems = append(problems, "app.point_cache_memory_limit must be between 64MiB and 30GiB")
	}
	if c.App.RequestTimeout.Duration <= 0 {
		problems = append(problems, "app.request_timeout must be positive")
	}
	if c.HorizonAnalysis.Enabled {
		if c.HorizonAnalysis.QueueSize < 1 || c.HorizonAnalysis.QueueSize > 32 {
			problems = append(problems, "horizon_analysis.queue_size must be between 1 and 32")
		}
		if c.HorizonAnalysis.CDOWorkers < 1 || c.HorizonAnalysis.CDOWorkers > 4 {
			problems = append(problems, "horizon_analysis.cdo_workers must be between 1 and 4")
		}
		if c.HorizonAnalysis.JobTimeout.Duration < time.Minute || c.HorizonAnalysis.JobTimeout.Duration > 30*time.Minute {
			problems = append(problems, "horizon_analysis.job_timeout must be between 1m and 30m")
		}
		if c.HorizonAnalysis.CacheTTL.Duration < time.Hour || c.HorizonAnalysis.CacheTTL.Duration > 7*24*time.Hour {
			problems = append(problems, "horizon_analysis.cache_ttl must be between 1h and 168h")
		}
		if c.HorizonAnalysis.CacheEntries < 1 || c.HorizonAnalysis.CacheEntries > 1024 {
			problems = append(problems, "horizon_analysis.cache_entries must be between 1 and 1024")
		}
		if c.HorizonAnalysis.EstimatedDuration.Duration < 10*time.Second || c.HorizonAnalysis.EstimatedDuration.Duration > c.HorizonAnalysis.JobTimeout.Duration {
			problems = append(problems, "horizon_analysis.estimated_duration must be between 10s and job_timeout")
		}
	}
	if strings.TrimSpace(c.Paths.Data) == "" || strings.TrimSpace(c.Paths.Temp) == "" {
		problems = append(problems, "paths.data and paths.temp are required")
	}
	if c.Sync.PollInterval.Duration <= 0 {
		problems = append(problems, "sync.poll_interval must be positive")
	}
	if c.Sync.DownloadParallelism < 1 || c.Sync.DownloadParallelism > 32 {
		problems = append(problems, "sync.download_parallelism must be between 1 and 32")
	}
	if c.Sync.MinFreeSpace < 0 {
		problems = append(problems, "sync.min_free_space cannot be negative")
	}
	if c.Render.Width < 320 || c.Render.Height < 240 {
		problems = append(problems, "render dimensions are too small")
	}
	if c.Algorithms.OverallSeeingWeight <= 0 || c.Algorithms.OverallSeeingWeight > 10 {
		problems = append(problems, "algorithms.overall_seeing_weight must be greater than 0 and no greater than 10")
	}
	if c.Algorithms.OverallCloudWeight <= 0 || c.Algorithms.OverallCloudWeight > 10 {
		problems = append(problems, "algorithms.overall_cloud_weight must be greater than 0 and no greater than 10")
	}
	if c.Algorithms.OverallCoherenceTimeWeight < 0 || c.Algorithms.OverallCoherenceTimeWeight > 1 {
		problems = append(problems, "algorithms.overall_coherence_time_weight must be between 0 and 1")
	}
	if c.Algorithms.OverallOpticalTurbulenceMaxPenalty < 0 || c.Algorithms.OverallOpticalTurbulenceMaxPenalty > 1 {
		problems = append(problems, "algorithms.overall_optical_turbulence_max_penalty must be between 0 and 1")
	}
	if c.Algorithms.OverallPossibleFogFactor < 0 || c.Algorithms.OverallPossibleFogFactor > 1 {
		problems = append(problems, "algorithms.overall_possible_fog_factor must be between 0 and 1")
	}
	if c.Algorithms.OverallHighFogFactor < 0 || c.Algorithms.OverallHighFogFactor > 1 {
		problems = append(problems, "algorithms.overall_high_fog_factor must be between 0 and 1")
	}
	if c.Algorithms.OverallGoodSeeingArcsec <= 0 || c.Algorithms.OverallBadSeeingArcsec <= c.Algorithms.OverallGoodSeeingArcsec {
		problems = append(problems, "algorithms overall seeing thresholds must be positive and ordered good < bad")
	}
	if c.Algorithms.OverallBadCoherenceTimeMS <= 0 || c.Algorithms.OverallBestCoherenceTimeMS <= c.Algorithms.OverallBadCoherenceTimeMS {
		problems = append(problems, "algorithms overall coherence-time thresholds must be positive and ordered bad < best")
	}
	if math.IsNaN(c.Algorithms.OverallBoundaryLayerMinM) || math.IsInf(c.Algorithms.OverallBoundaryLayerMinM, 0) ||
		math.IsNaN(c.Algorithms.OverallBoundaryLayerTopM) || math.IsInf(c.Algorithms.OverallBoundaryLayerTopM, 0) ||
		c.Algorithms.OverallBoundaryLayerMinM < 100 ||
		c.Algorithms.OverallBoundaryLayerTopM < c.Algorithms.OverallBoundaryLayerMinM ||
		c.Algorithms.OverallBoundaryLayerTopM > 4000 {
		problems = append(problems, "algorithms boundary-layer bounds must satisfy 100 <= overall_boundary_layer_min_m <= overall_boundary_layer_top_m <= 4000")
	}
	if c.Algorithms.OverallGroundCn2Scale < 0.05 || c.Algorithms.OverallGroundCn2Scale > 20 {
		problems = append(problems, "algorithms.overall_ground_cn2_scale must be between 0.05 and 20")
	}
	if c.Algorithms.OverallUnresolvedCloudObstruction < 0 || c.Algorithms.OverallUnresolvedCloudObstruction > 1 {
		problems = append(problems, "algorithms.overall_unresolved_cloud_obstruction must be between 0 and 1")
	}
	if c.Algorithms.OverallSurfaceWindMaxPenalty < 0 || c.Algorithms.OverallSurfaceWindMaxPenalty > 0.5 {
		problems = append(problems, "algorithms.overall_surface_wind_max_penalty must be between 0 and 0.5")
	}
	if c.Algorithms.OverallSurfaceWindStartMS < 0 || c.Algorithms.OverallSurfaceWindFullMS <= c.Algorithms.OverallSurfaceWindStartMS ||
		c.Algorithms.OverallSurfaceGustStartMS < 0 || c.Algorithms.OverallSurfaceGustFullMS <= c.Algorithms.OverallSurfaceGustStartMS {
		problems = append(problems, "algorithms overall surface wind/gust thresholds must be non-negative and ordered start < full")
	}
	if c.Algorithms.CloudLiquidRadiusMicrometers < 2 || c.Algorithms.CloudLiquidRadiusMicrometers > 40 ||
		c.Algorithms.CloudIceRadiusMicrometers < 5 || c.Algorithms.CloudIceRadiusMicrometers > 150 {
		problems = append(problems, "algorithms cloud effective radii are outside supported physical ranges")
	}

	validateProvider := func(name, expectedRole string, provider ProviderConfig) {
		if !provider.Enabled {
			return
		}
		if provider.Role != expectedRole {
			problems = append(problems, fmt.Sprintf("providers.%s.role must be %s", name, expectedRole))
		}
		if provider.KeepRuns < 1 {
			problems = append(problems, fmt.Sprintf("providers.%s.keep_runs must be positive", name))
		}
		if provider.MaxStaleAge.Duration <= 0 {
			problems = append(problems, fmt.Sprintf("providers.%s.max_stale_age must be positive", name))
		}
	}
	validateProvider("icon_eu", "primary", c.Providers.ICONEU)
	validateProvider("icon_global", "fallback", c.Providers.ICONGlobal)
	validateProvider("icon_ru", "shadow", c.Providers.ICONRu)
	if c.Providers.LightPollution.AtlasYear < 2012 || c.Providers.LightPollution.AtlasYear > time.Now().UTC().Year() {
		problems = append(problems, "providers.light_pollution.atlas_year must be between 2012 and the current year")
	}

	validatePlatform := func(name string, platform PlatformConfig) {
		if platform.Enabled && strings.TrimSpace(platform.TokenFile) == "" {
			problems = append(problems, fmt.Sprintf("platforms.%s.token_file is required when enabled", name))
		}
	}
	validatePlatform("telegram", c.Platforms.Telegram)
	validatePlatform("vk", c.Platforms.VK)
	if c.Platforms.VK.Enabled && c.Platforms.VK.GroupID <= 0 {
		problems = append(problems, "platforms.vk.group_id must be positive when enabled")
	}
	if strings.TrimSpace(c.Database.Host) == "" || c.Database.Port < 1 || c.Database.Port > 65535 || strings.TrimSpace(c.Database.Name) == "" || strings.TrimSpace(c.Database.User) == "" {
		problems = append(problems, "database host, valid port, name and user are required")
	}
	if c.Database.MaxConns < 1 || c.Database.MaxConns > 50 {
		problems = append(problems, "database.max_conns must be between 1 and 50")
	}

	if len(problems) > 0 {
		return fmt.Errorf("invalid config: %s", strings.Join(problems, "; "))
	}
	return nil
}

func (c Config) ResolvePaths(base string) Config {
	result := c
	if !filepath.IsAbs(result.Paths.Data) {
		result.Paths.Data = filepath.Join(base, result.Paths.Data)
	}
	if !filepath.IsAbs(result.Paths.Temp) {
		result.Paths.Temp = filepath.Join(base, result.Paths.Temp)
	}
	return result
}
