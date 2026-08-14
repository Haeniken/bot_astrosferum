package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"bot_astrosferum/internal/app"
	"bot_astrosferum/internal/app/bot"
	"bot_astrosferum/internal/app/directional"
	"bot_astrosferum/internal/astronomy"
	"bot_astrosferum/internal/config"
	"bot_astrosferum/internal/forecast"
	"bot_astrosferum/internal/lightpollution"
	"bot_astrosferum/internal/model"
	"bot_astrosferum/internal/model/copdem"
	"bot_astrosferum/internal/model/eccodes"
	"bot_astrosferum/internal/model/geoscf"
	"bot_astrosferum/internal/model/iconeu"
	"bot_astrosferum/internal/model/iconglobal"
	"bot_astrosferum/internal/platform/telegram"
	vkplatform "bot_astrosferum/internal/platform/vk"
	"bot_astrosferum/internal/render"
	pgstore "bot_astrosferum/internal/store"
)

const version = "0.1.0-dev"

type accountResultDiscardMessenger struct{}

func astrodomeVolumeRequired(cfg config.Config) bool {
	return cfg.Astrodome.Enabled || len(cfg.Platforms.Telegram.AdminIDs) > 0
}

func (accountResultDiscardMessenger) SendMessage(context.Context, int64, string, bool) error {
	return nil
}
func (accountResultDiscardMessenger) SendPhoto(context.Context, int64, string, string) error {
	return nil
}
func (accountResultDiscardMessenger) SendDocument(context.Context, int64, string, string) error {
	return nil
}
func (accountResultDiscardMessenger) AnswerAction(context.Context, string, string) error {
	return nil
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stdout, os.Stderr); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return printUsage(stdout)
	}
	switch args[0] {
	case "help", "-h", "--help":
		return printUsage(stdout)
	case "version":
		_, err := fmt.Fprintf(stdout, "%s %s\n", version, runtime.Version())
		return err
	case "parse-location":
		return runParseLocation(args[1:], stdout, stderr)
	case "extract-point":
		return runExtractPoint(ctx, args[1:], stdout, stderr)
	case "light-pollution":
		return runLightPollution(ctx, args[1:], stdout, stderr)
	case "doctor":
		return runDoctor(ctx, args[1:], stdout, stderr)
	case "render-sample":
		return runRenderSample(args[1:], stdout, stderr)
	case "serve":
		return runServe(ctx, args[1:], stdout, stderr)
	case "sync-icon-eu":
		return runSyncICONEU(ctx, args[1:], stdout, stderr)
	case "sync-icon-global":
		return runSyncICONGlobal(ctx, args[1:], stdout, stderr)
	case "render-point":
		return runRenderPoint(ctx, args[1:], stdout, stderr)
	case "render-horizon":
		return runRenderHorizon(ctx, args[1:], stdout, stderr)
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func runRenderHorizon(ctx context.Context, args []string, stdout, stderr io.Writer) (resultErr error) {
	flags := flag.NewFlagSet("render-horizon", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "/app/config/config.yaml", "configuration file")
	latitude := flags.Float64("lat", 0, "latitude inside ICON-EU")
	longitude := flags.Float64("lon", 0, "longitude inside ICON-EU")
	output := flags.String("output", "/app/data/verification/horizon-live.png", "output PNG")
	language := flags.String("language", "en", "chart language: en or ru")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("render-horizon accepts flags only")
	}
	if err := forecast.ValidateCoordinates(*latitude, *longitude); err != nil {
		return err
	}
	if *language != "en" && *language != "ru" {
		return errors.New("render language must be en or ru")
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	if !cfg.HorizonAnalysis.Enabled {
		return errors.New("horizon analysis is disabled")
	}
	resolver, err := forecast.NewTimeZoneResolver()
	if err != nil {
		return err
	}
	location, err := forecast.NewLocation(*latitude, *longitude, resolver.Resolve(*latitude, *longitude))
	if err != nil {
		return err
	}
	logf := func(format string, values ...any) { writeLog(stderr, format, values...) }
	started := time.Now()
	pointStore := iconeu.NewCachedStore(
		cfg.Paths.Data, cfg.App.ECCodesWorkers, cfg.App.PointCacheEntries,
		int64(cfg.App.PointCacheMemoryLimit), logf,
	)
	cloud, err := pointStore.Cloud(ctx, location)
	if err != nil {
		return err
	}
	plan, err := forecast.NewHorizonPlan(location, cloud.SurfaceElevationM)
	if err != nil {
		return err
	}
	horizonStore := iconeu.NewHorizonStore(
		cfg.Paths.Data, filepath.Join(cfg.Paths.Temp, "horizon-batch-cli"),
		cfg.HorizonAnalysis.CDOWorkers, logf,
	)
	if !horizonStore.Supports(plan) {
		return errors.New("horizon footprint is outside ICON-EU")
	}
	executionGate, err := directional.NewExecutionGate(
		filepath.Join(cfg.Paths.Data, "state", "run-leases"),
		cfg.Directional.Concurrency,
	)
	if err != nil {
		return err
	}
	executionLease, err := executionGate.Acquire(ctx)
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, executionLease.Close()) }()
	terrainStore, err := copdem.NewStore(copdem.Config{
		Enabled:         cfg.Terrain.CopernicusDEMGLO30Enabled,
		Root:            filepath.Join(cfg.Paths.Data, "terrain", "copernicus-dem-glo30-2021"),
		CacheLimitBytes: int64(cfg.Terrain.CacheLimit), Logf: logf,
	})
	if err != nil {
		return err
	}
	terrainSkyline, err := terrainStore.Resolve(ctx, location)
	if err != nil {
		return fmt.Errorf("prepare Copernicus DEM GLO-30 skyline: %w", err)
	}
	currentRun, err := horizonStore.CurrentRunID()
	if err != nil || currentRun != cloud.RunID {
		return errors.New("ICON-EU horizon run changed before calculation")
	}
	snapshots, err := horizonStore.Series(ctx, cloud.RunID, plan)
	if err != nil {
		return err
	}
	if len(snapshots) != 72 {
		return errors.New("ICON-EU horizon series is incomplete")
	}
	frames, err := forecast.ComputeHorizonSeries(ctx, snapshots, plan, app.OverallCalibration(cfg.Algorithms))
	if err != nil {
		return err
	}
	if err := forecast.ApplyTerrainSkylineToHorizon(frames, terrainSkyline); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(*output), 0o750); err != nil {
		return fmt.Errorf("create horizon output directory: %w", err)
	}
	if err := render.Horizon(ctx, *output, render.HorizonInput{
		Location: location, Provider: "ICON-EU", RunID: cloud.RunID,
		Grid: iconeu.Coverage().GridName, Frames: frames, TerrainSkyline: terrainSkyline,
	}, render.Options{Language: *language}); err != nil {
		return err
	}
	currentRun, err = horizonStore.CurrentRunID()
	if err != nil || currentRun != cloud.RunID {
		_ = os.Remove(*output)
		return errors.New("ICON-EU horizon run changed during rendering")
	}
	return writeJSON(stdout, struct {
		RunID             string    `json:"run_id"`
		PeriodFrom        time.Time `json:"period_from"`
		PeriodTo          time.Time `json:"period_to"`
		Frames            int       `json:"frames"`
		Duration          string    `json:"duration"`
		File              string    `json:"file"`
		SurfaceElevationM float64   `json:"model_surface_elevation_m"`
	}{
		RunID: cloud.RunID, PeriodFrom: frames[0].ValidAt, PeriodTo: frames[len(frames)-1].ValidAt,
		Frames: len(frames), Duration: time.Since(started).Round(time.Millisecond).String(), File: *output,
		SurfaceElevationM: cloud.SurfaceElevationM,
	})
}

func runSyncICONGlobal(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("sync-icon-global", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "/app/config/config.yaml", "configuration file")
	runID := flags.String("run", "", "specific complete run as YYYYMMDDHH; latest when empty")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("sync-icon-global accepts flags only")
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	logf := func(format string, values ...any) { writeLog(stderr, format, values...) }
	client := iconglobal.NewClient()
	downloadLimiter, err := model.NewDownloadLimiter(cfg.Sync.DownloadLimitMbit)
	if err != nil {
		return err
	}
	client.HTTPClient = downloadLimiter.WrapHTTPClient(client.HTTPClient)
	if err := iconglobal.EnsureGrid(ctx, cfg.Paths.Data, logf, client.HTTPClient); err != nil {
		return err
	}
	client.Workers = cfg.Sync.DownloadParallelism
	client.Progress = logf
	var remote model.RemoteRun
	if *runID == "" {
		remote, err = client.ProbeLatest(ctx)
		if err != nil {
			return err
		}
	} else {
		baseTime, parseError := time.Parse("2006010215", *runID)
		if parseError != nil {
			return fmt.Errorf("parse --run: %w", parseError)
		}
		remote = model.RemoteRun{ID: *runID, BaseTime: baseTime}
	}
	if _, err := fmt.Fprintf(stderr, "syncing ICON Global run %s\n", remote.ID); err != nil {
		return err
	}
	manifest, err := client.Sync(ctx, remote, cfg.Paths.Data)
	if err != nil {
		return err
	}
	manifest, err = client.AugmentCloud(ctx, cfg.Paths.Data, manifest)
	if err != nil {
		return err
	}
	if err := iconglobal.PublishCurrent(cfg.Paths.Data, manifest); err != nil {
		return err
	}
	return writeJSON(stdout, manifest.Manifest)
}

func runSyncICONEU(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("sync-icon-eu", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "/app/config/config.yaml", "configuration file")
	runID := flags.String("run", "", "specific complete run as YYYYMMDDHH; latest when empty")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("sync-icon-eu accepts flags only")
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	client := iconeu.NewClient()
	downloadLimiter, err := model.NewDownloadLimiter(cfg.Sync.DownloadLimitMbit)
	if err != nil {
		return err
	}
	client.HTTPClient = downloadLimiter.WrapHTTPClient(client.HTTPClient)
	client.Workers = cfg.Sync.DownloadParallelism
	client.Progress = func(format string, values ...any) { writeLog(stderr, format, values...) }
	var remote model.RemoteRun
	if *runID == "" {
		remote, err = client.ProbeLatest(ctx)
		if err != nil {
			return err
		}
	} else {
		baseTime, parseError := time.Parse("2006010215", *runID)
		if parseError != nil {
			return fmt.Errorf("parse --run: %w", parseError)
		}
		remote = model.RemoteRun{ID: *runID, BaseTime: baseTime}
	}
	if _, err := fmt.Fprintf(stderr, "syncing ICON-EU run %s\n", remote.ID); err != nil {
		return err
	}
	manifest, err := client.Sync(ctx, remote, cfg.Paths.Data)
	if err != nil {
		return err
	}
	manifest, err = client.AugmentWindThermodynamics(ctx, cfg.Paths.Data, manifest)
	if err != nil {
		return err
	}
	manifest, err = client.AugmentSurface(ctx, cfg.Paths.Data, manifest)
	if err != nil {
		return err
	}
	manifest, err = client.AugmentCloud(ctx, cfg.Paths.Data, manifest)
	if err != nil {
		return err
	}
	if err := iconeu.PublishCurrent(cfg.Paths.Data, manifest); err != nil {
		return err
	}
	return writeJSON(stdout, manifest.Manifest)
}

func runRenderPoint(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("render-point", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "/app/config/config.yaml", "configuration file")
	latitude := flags.Float64("lat", 0, "latitude")
	longitude := flags.Float64("lon", 0, "longitude")
	outputDirectory := flags.String("output", "/app/data/verification/render-live", "output directory")
	language := flags.String("language", "en", "chart language: en or ru")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("render-point accepts flags only")
	}
	if err := forecast.ValidateCoordinates(*latitude, *longitude); err != nil {
		return err
	}
	if *language != "en" && *language != "ru" {
		return errors.New("render language must be en or ru")
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	resolver, err := forecast.NewTimeZoneResolver()
	if err != nil {
		return err
	}
	location, err := forecast.NewLocation(*latitude, *longitude, resolver.Resolve(*latitude, *longitude))
	if err != nil {
		return err
	}
	logf := func(format string, values ...any) { writeLog(stderr, format, values...) }
	iconEUStore := iconeu.NewCachedStore(cfg.Paths.Data, cfg.App.ECCodesWorkers, cfg.App.PointCacheEntries, int64(cfg.App.PointCacheMemoryLimit), logf)
	var store model.ForecastStore = iconEUStore
	if cfg.Providers.ICONGlobal.Enabled {
		store = model.CoverageFallback{
			PrimaryCoverage: iconeu.Coverage(), Primary: iconEUStore,
			Fallback: iconglobal.NewStore(cfg.Paths.Data, cfg.App.ECCodesWorkers, logf),
		}
	}
	series, err := store.Vertical(ctx, location)
	if err != nil {
		return err
	}
	surface, err := store.Surface(ctx, location)
	if err != nil {
		return err
	}
	surface = surface.Window(time.Now(), 72)
	cloud, cloudError := store.Cloud(ctx, location)
	hasCloud := cloudError == nil
	if cloudError != nil {
		writeLog(stderr, "cloud profile unavailable: %v", cloudError)
	} else {
		cloud = cloud.Window(time.Now(), 72)
		hasCloud = len(cloud.Frames) >= 2
	}
	result, err := render.All(*outputDirectory, series, render.Options{Width: cfg.Render.Width, Height: cfg.Render.Height, Language: *language})
	if err != nil {
		return err
	}
	sky, err := astronomy.Compute(location, surface.Frames[0].ValidAt, surface.Frames[len(surface.Frames)-1].ValidAt)
	if err != nil {
		return err
	}
	celestialTracks, err := celestialTracksForSurface(surface)
	if err != nil {
		return err
	}
	result.Weather = filepath.Join(*outputDirectory, "weather-hourly.png")
	if err := render.Weather(result.Weather, surface, sky, celestialTracks, render.Options{Language: *language}); err != nil {
		return err
	}
	calibration := app.OverallCalibration(cfg.Algorithms)
	if hasCloud {
		result.CloudObstruction = filepath.Join(*outputDirectory, "cloud-obstruction-height-hourly.png")
		if err := render.CloudObstruction(result.CloudObstruction, cloud, calibration, render.Options{Width: 3200, Height: 1100, Language: *language}); err != nil {
			return err
		}
	}
	overall, err := forecast.ComputeHourlyOverallIndex(series, surface, cloud, calibration)
	if err != nil {
		return err
	}
	var composition forecast.AtmosphericCompositionSeries
	if cfg.Providers.GEOSCF.Enabled {
		validTimes := make([]time.Time, len(surface.Frames))
		for index := range surface.Frames {
			validTimes[index] = surface.Frames[index].ValidAt
		}
		compositionStore := model.GEOSCFCompositionStore{Client: newGEOSCFClient(cfg)}
		composition, err = compositionStore.AtmosphericComposition(ctx, location, validTimes)
		if err != nil {
			writeLog(stderr, "GEOS-CF composition unavailable; Reference V-band remains partial: %v", err)
			composition = forecast.AtmosphericCompositionSeries{}
		}
	}
	if err := bot.AttachReferenceVBand(overall, surface, composition, sky); err != nil {
		return err
	}
	result.OverallIndex = filepath.Join(*outputDirectory, "overall-astronomy-index-hourly.png")
	if err := render.OverallIndex(result.OverallIndex, series, overall, sky, render.Options{Width: render.OverallWidth, Height: render.OverallHeight, Language: *language}); err != nil {
		return err
	}
	return writeJSON(stdout, struct {
		RunID         string                       `json:"run_id"`
		TimeZoneLabel string                       `json:"time_zone_label"`
		Surface       forecast.SurfaceSeries       `json:"surface"`
		Overall       []forecast.OverallIndexFrame `json:"overall_index"`
		Files         render.Result                `json:"files"`
	}{RunID: series.RunID, TimeZoneLabel: forecast.TimeZoneLabel(location.TimeZone, series.BaseTime), Surface: surface, Overall: overall, Files: result})
}

func runServe(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("serve", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "/app/config/config.yaml", "configuration file")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("serve accepts flags only")
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	if !cfg.Platforms.Telegram.Enabled && !cfg.Platforms.VK.Enabled {
		return errors.New("at least one messaging platform must be enabled")
	}
	logf := func(format string, values ...any) { writeLog(stderr, format, values...) }
	downloadLimiter, err := model.NewDownloadLimiter(cfg.Sync.DownloadLimitMbit)
	if err != nil {
		return err
	}
	iconEUStore := iconeu.NewCachedStore(
		cfg.Paths.Data, cfg.App.ECCodesWorkers, cfg.App.PointCacheEntries,
		int64(cfg.App.PointCacheMemoryLimit), logf,
	)
	terrainStore, err := copdem.NewStore(copdem.Config{
		Enabled:         cfg.Terrain.CopernicusDEMGLO30Enabled,
		Root:            filepath.Join(cfg.Paths.Data, "terrain", "copernicus-dem-glo30-2021"),
		CacheLimitBytes: int64(cfg.Terrain.CacheLimit),
		Logf:            logf,
	})
	if err != nil {
		return err
	}
	var forecastStore model.ForecastStore = iconEUStore
	if cfg.Providers.ICONGlobal.Enabled {
		globalStore := iconglobal.NewStore(cfg.Paths.Data, cfg.App.ECCodesWorkers, logf)
		forecastStore = model.CoverageFallback{
			PrimaryCoverage: iconeu.Coverage(), Primary: iconEUStore, Fallback: globalStore,
		}
	}
	var compositionStore *model.GEOSCFCompositionStore
	if cfg.Providers.GEOSCF.Enabled {
		compositionStore = &model.GEOSCFCompositionStore{Client: newGEOSCFClient(cfg)}
	}
	forecastQueue, err := bot.NewForecastQueue(cfg.App.ForecastConcurrency)
	if err != nil {
		return err
	}
	horizonAccountCapacity := 1
	horizonAccountTimeout := time.Duration(0)
	if cfg.HorizonAnalysis.Enabled {
		horizonAccountCapacity = cfg.HorizonAnalysis.QueueSize + cfg.HorizonAnalysis.Concurrency
		if cfg.HorizonAnalysis.JobTimeout.Duration > 0 {
			horizonAccountTimeout = cfg.HorizonAnalysis.JobTimeout.Duration + 5*time.Minute
		}
	}
	accountResults, err := bot.NewAccountResultDispatcher(
		ctx, cfg.App.RequestTimeout.Duration, horizonAccountTimeout, 96*time.Hour,
		filepath.Join(cfg.Paths.Data, "cache", "account-results"),
		cfg.App.Workers, horizonAccountCapacity, logf,
	)
	if err != nil {
		return err
	}
	// The fast straight-ray Horizon uses the ordinary immutable ICON-EU point
	// bundles. Only Astrodome and its administrator preview require the much
	// larger native three-dimensional Dome volume.
	directionalVolumeOperational := astrodomeVolumeRequired(cfg)
	var astrodomeDiskBudget *model.DiskBudget
	if directionalVolumeOperational {
		astrodomeDiskBudget, err = newAstrodomeDiskBudget(cfg)
		if err != nil {
			return err
		}
	}
	var horizonJobs *bot.HorizonJobs
	if cfg.HorizonAnalysis.Enabled {
		horizonSource := iconeu.NewHorizonStore(
			cfg.Paths.Data, filepath.Join(cfg.Paths.Temp, "horizon-batch"),
			cfg.HorizonAnalysis.CDOWorkers, logf,
		)
		horizonJobs, err = bot.NewHorizonJobs(bot.HorizonJobsConfig{
			QueueSize: cfg.HorizonAnalysis.QueueSize, Concurrency: cfg.HorizonAnalysis.Concurrency,
			JobTimeout: cfg.HorizonAnalysis.JobTimeout.Duration,
			CacheRoot:  filepath.Join(cfg.Paths.Data, "cache", "horizon"),
			CacheTTL:   cfg.HorizonAnalysis.CacheTTL.Duration, CacheEntries: cfg.HorizonAnalysis.CacheEntries,
			EstimatedDuration:      cfg.HorizonAnalysis.EstimatedDuration.Duration,
			MaxStaleAge:            cfg.Providers.ICONEU.MaxStaleAge.Duration,
			RenderAlgorithmVersion: render.HorizonRenderVersion,
			Terrain:                terrainStore,
		}, horizonSource, func(renderContext context.Context, destination string, input bot.HorizonRenderInput, language string) error {
			if err := renderContext.Err(); err != nil {
				return err
			}
			if err := render.Horizon(renderContext, destination, render.HorizonInput{
				Location: input.Location, Provider: input.Provider, RunID: input.RunID,
				Grid: input.Grid, Frames: input.Frames, TerrainSkyline: input.TerrainSkyline,
			}, render.Options{Language: language}); err != nil {
				return err
			}
			return renderContext.Err()
		}, app.OverallCalibration(cfg.Algorithms), logf)
		if err != nil {
			return err
		}
	}
	var directionalService *directionalRuntime
	directionalService, err = newDirectionalRuntime(ctx, cfg, horizonJobs, terrainStore, accountResults, logf)
	if err != nil {
		return err
	}
	if err := directionalService.Start(ctx); err != nil {
		return err
	}
	defer func() {
		if closeErr := directionalService.Close(); closeErr != nil {
			logf("directional runtime shutdown failed: %v", closeErr)
		}
	}()
	database, err := pgstore.Open(ctx, cfg.Database.Host, cfg.Database.Port, cfg.Database.Name, cfg.Database.User, cfg.Database.Password, cfg.Database.MaxConns)
	if err != nil {
		return err
	}
	defer database.Close()
	go database.RunMaintenance(ctx, logf)
	lightAtlas, err := lightpollution.NewAtlas(
		filepath.Join(cfg.Paths.Data, "light-pollution", "lorenz-atlas"),
		lightpollution.Options{AtlasYear: cfg.Providers.LightPollution.AtlasYear}, logf,
	)
	if err != nil {
		return err
	}
	worldPath, err := lightpollution.EnsureWorldAtlas2015(ctx, filepath.Join(cfg.Paths.Data, "light-pollution", "world-atlas-2015"), logf)
	if err != nil {
		return err
	}
	worldAtlas, err := lightpollution.OpenWorldAtlas2015(ctx, worldPath)
	if err != nil {
		return fmt.Errorf("open World Atlas 2015: %w", err)
	}
	defer func() { _ = worldAtlas.Close() }()
	configureHandler := func(handler *bot.Handler, platform string, adminIDs []int64, renderDirectory string) error {
		handler.SetLogger(logf)
		if err := handler.EnablePersistence(database, adminIDs); err != nil {
			return err
		}
		if err := handler.EnableLightPollution(lightAtlas); err != nil {
			return err
		}
		if err := handler.EnableWorldAtlas2015(worldAtlas); err != nil {
			return err
		}
		if compositionStore != nil {
			if err := handler.EnableAtmosphericComposition(ctx, compositionStore); err != nil {
				return err
			}
		}
		if err := handler.SetOverallIndexCalibration(app.OverallCalibration(cfg.Algorithms)); err != nil {
			return err
		}
		if err := handler.EnableForecast(forecastStore, filepath.Join(cfg.Paths.Temp, renderDirectory), render.Options{Width: cfg.Render.Width, Height: cfg.Render.Height}); err != nil {
			return err
		}
		if err := handler.EnableForecastQueue(forecastQueue); err != nil {
			return err
		}
		if err := handler.SetForecastMaxStaleAge(cfg.Providers.ICONEU.MaxStaleAge.Duration); err != nil {
			return err
		}
		if cfg.Providers.ICONGlobal.Enabled {
			if err := handler.SetFallbackForecastMaxStaleAge(cfg.Providers.ICONGlobal.MaxStaleAge.Duration); err != nil {
				return err
			}
		}
		if err := handler.EnableRenderCache(filepath.Join(cfg.Paths.Data, "cache", "renders", "shared-render-v1")); err != nil {
			return err
		}
		if horizonJobs != nil {
			return handler.EnableHorizon(platform, horizonJobs)
		}
		return nil
	}
	accountHandler, err := bot.NewHandler(accountResultDiscardMessenger{})
	if err != nil {
		return err
	}
	if err := configureHandler(accountHandler, "web", nil, "account-renders"); err != nil {
		return err
	}
	if err := accountResults.SetHandler(accountHandler); err != nil {
		return err
	}
	type platformAdapter struct {
		name string
		run  func(context.Context) error
	}
	adapters := make([]platformAdapter, 0, 2)
	if cfg.Platforms.Telegram.Enabled {
		token, err := os.ReadFile(cfg.Platforms.Telegram.TokenFile)
		if err != nil {
			return fmt.Errorf("read Telegram token file: %w", err)
		}
		client, err := telegram.NewClient(string(token))
		if err != nil {
			return err
		}
		handler, err := bot.NewHandler(client)
		if err != nil {
			return err
		}
		if err := configureHandler(handler, "telegram", cfg.Platforms.Telegram.AdminIDs, "telegram-renders"); err != nil {
			return err
		}
		adapters = append(adapters, platformAdapter{name: "Telegram", run: func(runContext context.Context) error {
			return client.Run(runContext, handler, cfg.App.Workers, cfg.App.RequestTimeout.Duration, logf)
		}})
	}
	if cfg.Platforms.VK.Enabled {
		token, err := os.ReadFile(cfg.Platforms.VK.TokenFile)
		if err != nil {
			return fmt.Errorf("read VK token file: %w", err)
		}
		client, err := vkplatform.NewClient(string(token), cfg.Platforms.VK.GroupID)
		if err != nil {
			return err
		}
		handler, err := bot.NewHandler(client)
		if err != nil {
			return err
		}
		adminIDs := make([]int64, 0, len(cfg.Platforms.VK.AdminIDs))
		for _, id := range cfg.Platforms.VK.AdminIDs {
			key, keyError := vkplatform.UserKey(id)
			if keyError != nil {
				return keyError
			}
			adminIDs = append(adminIDs, key)
		}
		if err := configureHandler(handler, "vk", adminIDs, "vk-renders"); err != nil {
			return err
		}
		adapters = append(adapters, platformAdapter{name: "VK", run: func(runContext context.Context) error {
			return client.Run(runContext, handler, cfg.App.Workers, cfg.App.RequestTimeout.Duration, logf)
		}})
	}
	syncClient := iconeu.NewClient()
	syncClient.HTTPClient = downloadLimiter.WrapHTTPClient(syncClient.HTTPClient)
	syncClient.Workers = cfg.Sync.DownloadParallelism
	syncClient.Progress = func(format string, values ...any) {
		writeLog(stderr, format, values...)
	}
	scheduler := app.ICONEUScheduler{
		Client: syncClient, DataRoot: cfg.Paths.Data,
		PollInterval: cfg.Sync.PollInterval.Duration, KeepRuns: cfg.Providers.ICONEU.KeepRuns,
		MaxStaleAge: cfg.Providers.ICONEU.MaxStaleAge.Duration,
		DomeEnabled: directionalVolumeOperational, DomeBudget: astrodomeDiskBudget,
		Logf: func(format string, values ...any) { writeLog(stderr, format, values...) },
	}
	go scheduler.Run(ctx)
	if cfg.Providers.ICONGlobal.Enabled {
		globalClient := iconglobal.NewClient()
		globalClient.HTTPClient = downloadLimiter.WrapHTTPClient(globalClient.HTTPClient)
		globalClient.Workers = cfg.Sync.DownloadParallelism
		globalClient.Progress = func(format string, values ...any) { writeLog(stderr, format, values...) }
		globalScheduler := app.ICONGlobalScheduler{
			Client: globalClient, DataRoot: cfg.Paths.Data,
			PollInterval: cfg.Sync.PollInterval.Duration, KeepRuns: cfg.Providers.ICONGlobal.KeepRuns,
			MaxStaleAge: cfg.Providers.ICONGlobal.MaxStaleAge.Duration, Logf: logf,
		}
		go globalScheduler.Run(ctx)
	}
	for _, adapter := range adapters {
		if _, err := fmt.Fprintf(stdout, "%s long polling starting\n", adapter.name); err != nil {
			return err
		}
	}
	var adapterGroup sync.WaitGroup
	for _, adapter := range adapters {
		adapterGroup.Add(1)
		go func() {
			defer adapterGroup.Done()
			for ctx.Err() == nil {
				err := adapter.run(ctx)
				if ctx.Err() != nil {
					return
				}
				if err != nil {
					logf("%s adapter stopped: %v; retrying", adapter.name, err)
				} else {
					logf("%s adapter stopped unexpectedly; retrying", adapter.name)
				}
				select {
				case <-ctx.Done():
					return
				case <-time.After(5 * time.Second):
				}
			}
		}()
	}
	<-ctx.Done()
	adapterGroup.Wait()
	return nil
}

func runRenderSample(args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("render-sample", flag.ContinueOnError)
	flags.SetOutput(stderr)
	outputDirectory := flags.String("output", "/app/data/verification/render-sample", "output directory for synthetic fixture PNGs")
	width := flags.Int("width", render.DefaultWidth, "PNG width in pixels")
	height := flags.Int("height", render.DefaultHeight, "PNG height in pixels")
	language := flags.String("language", "en", "chart language: en or ru")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("render-sample accepts flags only")
	}
	if *language != "en" && *language != "ru" {
		return errors.New("render language must be en or ru")
	}
	series := forecast.SyntheticVerticalFixture()
	result, err := render.All(*outputDirectory, series, render.Options{Width: *width, Height: *height, Language: *language})
	if err != nil {
		return err
	}
	surface := forecast.SyntheticSurfaceFixture()
	sky, err := astronomy.Compute(surface.Location, surface.Frames[0].ValidAt, surface.Frames[len(surface.Frames)-1].ValidAt)
	if err != nil {
		return err
	}
	celestialTracks, err := celestialTracksForSurface(surface)
	if err != nil {
		return err
	}
	result.Weather = filepath.Join(*outputDirectory, "weather-hourly.png")
	if err := render.Weather(result.Weather, surface, sky, celestialTracks, render.Options{Language: *language}); err != nil {
		return err
	}
	result.CloudObstruction = filepath.Join(*outputDirectory, "cloud-obstruction-height-hourly.png")
	if err := render.CloudObstruction(result.CloudObstruction, forecast.SyntheticCloudFixture(), forecast.DefaultOverallIndexCalibration(), render.Options{Width: 3200, Height: 1100, Language: *language}); err != nil {
		return err
	}
	overall, err := forecast.ComputeHourlyOverallIndex(series, surface, forecast.SyntheticCloudFixture(), forecast.DefaultOverallIndexCalibration())
	if err != nil {
		return err
	}
	composition := forecast.AtmosphericCompositionSeries{
		Location: surface.Location, Provider: "synthetic-composition", Product: "deterministic-v1",
		RunID: "synthetic-composition-v1", BaseTime: surface.BaseTime, Grid: "fixture",
		Frames: make([]forecast.AtmosphericCompositionFrame, len(surface.Frames)),
	}
	for index, frame := range surface.Frames {
		composition.Frames[index] = forecast.AtmosphericCompositionFrame{
			ValidAt: frame.ValidAt, AerosolOpticalDepth550: 0.10, TotalColumnOzoneDU: 300,
			Provider: composition.Provider, RunID: composition.RunID, BaseTime: composition.BaseTime, Grid: composition.Grid,
		}
	}
	if err := bot.AttachReferenceVBand(overall, surface, composition, sky); err != nil {
		return err
	}
	result.OverallIndex = filepath.Join(*outputDirectory, "overall-astronomy-index-hourly.png")
	if err := render.OverallIndex(result.OverallIndex, series, overall, sky, render.Options{Width: render.OverallWidth, Height: render.OverallHeight, Language: *language}); err != nil {
		return err
	}
	return writeJSON(stdout, struct {
		Fixture          string        `json:"fixture"`
		AlgorithmVersion string        `json:"algorithm_version"`
		RenderVersion    string        `json:"render_version"`
		TimeZoneLabel    string        `json:"time_zone_label"`
		Files            render.Result `json:"files"`
	}{
		Fixture:          "synthetic-vertical-v1",
		AlgorithmVersion: series.AlgorithmVersion,
		RenderVersion:    render.Version,
		TimeZoneLabel:    forecast.TimeZoneLabel(series.Location.TimeZone, series.BaseTime),
		Files:            result,
	})
}

func runParseLocation(args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("parse-location", flag.ContinueOnError)
	flags.SetOutput(stderr)
	atText := flags.String("at", time.Now().UTC().Format(time.RFC3339), "timestamp used for timezone offset")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() == 0 {
		return errors.New("coordinate text is required")
	}
	latitude, longitude, err := forecast.ParseLocationText(strings.Join(flags.Args(), " "))
	if err != nil {
		return err
	}
	resolver, err := forecast.NewTimeZoneResolver()
	if err != nil {
		return err
	}
	timeZone := resolver.Resolve(latitude, longitude)
	location, err := forecast.NewLocation(latitude, longitude, timeZone)
	if err != nil {
		return err
	}
	at, err := time.Parse(time.RFC3339, *atText)
	if err != nil {
		return fmt.Errorf("parse --at: %w", err)
	}
	return writeJSON(stdout, struct {
		forecast.Location
		TimeZoneLabel string `json:"time_zone_label"`
	}{Location: location, TimeZoneLabel: forecast.TimeZoneLabel(timeZone, at)})
}

func runExtractPoint(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("extract-point", flag.ContinueOnError)
	flags.SetOutput(stderr)
	file := flags.String("file", "", "absolute path to one regular-grid GRIB2 file")
	latitude := flags.Float64("lat", 0, "latitude")
	longitude := flags.Float64("lon", 0, "longitude")
	timeout := flags.Duration("timeout", 30*time.Second, "ecCodes command timeout")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *file == "" {
		return errors.New("--file is required")
	}
	if err := forecast.ValidateCoordinates(*latitude, *longitude); err != nil {
		return err
	}
	if *timeout <= 0 {
		return errors.New("--timeout must be positive")
	}
	commandContext, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	cell, sample, err := eccodes.NewExtractor().Extract(commandContext, *file, *latitude, *longitude)
	if err != nil {
		return err
	}
	resolver, err := forecast.NewTimeZoneResolver()
	if err != nil {
		return err
	}
	timeZone := resolver.Resolve(*latitude, *longitude)
	location, err := forecast.NewLocation(*latitude, *longitude, timeZone)
	if err != nil {
		return err
	}
	return writeJSON(stdout, struct {
		Location forecast.Location `json:"location"`
		Cell     any               `json:"cell"`
		Sample   any               `json:"sample"`
	}{Location: location, Cell: cell, Sample: sample})
}

func runLightPollution(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("light-pollution", flag.ContinueOnError)
	flags.SetOutput(stderr)
	dataRoot := flags.String("data", "/app/data", "runtime data directory")
	latitude := flags.Float64("lat", 0, "latitude")
	longitude := flags.Float64("lon", 0, "longitude")
	atlasYear := flags.Int("year", 2024, "explicit Light Pollution Atlas year")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("light-pollution accepts flags only")
	}
	if err := forecast.ValidateCoordinates(*latitude, *longitude); err != nil {
		return err
	}
	atlas, err := lightpollution.NewAtlas(
		filepath.Join(*dataRoot, "light-pollution", "lorenz-atlas"),
		lightpollution.Options{AtlasYear: *atlasYear}, func(format string, values ...any) { writeLog(stderr, format, values...) },
	)
	if err != nil {
		return err
	}
	estimate, err := atlas.At(ctx, *latitude, *longitude)
	if err != nil {
		return err
	}
	return writeJSON(stdout, estimate)
}

func runDoctor(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("doctor", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "/app/config/config.yaml", "configuration file")
	jsonOutput := flags.Bool("json", false, "write JSON")
	if err := flags.Parse(args); err != nil {
		return err
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	checks := app.Doctor(ctx, cfg)
	if *jsonOutput {
		if err := writeJSON(stdout, checks); err != nil {
			return err
		}
	} else {
		for _, check := range checks {
			status := "FAIL"
			if check.OK {
				status = "OK"
			}
			if _, err := fmt.Fprintf(stdout, "%-4s %-24s %s\n", status, check.Name, check.Detail); err != nil {
				return err
			}
		}
	}
	if !app.AllChecksPass(checks) {
		return errors.New("one or more doctor checks failed")
	}
	return nil
}

func writeJSON(writer io.Writer, value any) error {
	encoder := json.NewEncoder(writer)
	encoder.SetIndent("", "  ")
	encoder.SetEscapeHTML(false)
	return encoder.Encode(value)
}

func celestialTracksForSurface(surface forecast.SurfaceSeries) ([]astronomy.CelestialTrack, error) {
	validTimes := make([]time.Time, len(surface.Frames))
	for index := range surface.Frames {
		validTimes[index] = surface.Frames[index].ValidAt
	}
	return astronomy.ComputeCelestialTracks(surface.Location, validTimes)
}

func printUsage(writer io.Writer) error {
	_, err := fmt.Fprintln(writer, `bot_astrosferum commands:
  doctor         validate config, directories, tools, disk, and secret files
  serve          run enabled platform adapters with long polling
  parse-location parse text coordinates and resolve their IANA timezone
  extract-point  extract one value from a regular-grid GRIB2 file
  light-pollution query annual modeled zenith brightness for coordinates
  render-sample  render PNG charts from deterministic synthetic fixtures
  sync-icon-eu   atomically sync one complete ICON-EU pressure-level wind run
  sync-icon-global atomically sync one complete native-grid ICON Global run
  render-point   render charts for a point from the current ICON-EU/Global run
  render-horizon render one real ICON-EU eight-direction horizon analysis
  version        print the build version`)
	return err
}

func writeLog(writer io.Writer, format string, values ...any) {
	_, _ = fmt.Fprintf(writer, format+"\n", values...)
}

func newGEOSCFClient(cfg config.Config) *geoscf.Client {
	provider := cfg.Providers.GEOSCF
	return geoscf.New(geoscf.Options{
		DatasetURL: provider.DatasetURL, RequestTimeout: provider.RequestTimeout.Duration,
		MetadataTTL: provider.MetadataCacheTTL.Duration, DataTTL: provider.DataCacheTTL.Duration,
		MaximumRunAge: provider.MaxStaleAge.Duration, CacheEntries: provider.CacheEntries,
		DataConcurrency: cfg.App.ForecastConcurrency,
	})
}
