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
	"syscall"
	"time"

	"bot_astrosferum/internal/app"
	"bot_astrosferum/internal/astronomy"
	"bot_astrosferum/internal/config"
	"bot_astrosferum/internal/forecast"
	"bot_astrosferum/internal/lightpollution"
	"bot_astrosferum/internal/model"
	"bot_astrosferum/internal/model/eccodes"
	"bot_astrosferum/internal/model/iconeu"
	"bot_astrosferum/internal/platform/telegram"
	"bot_astrosferum/internal/render"
	pgstore "bot_astrosferum/internal/store"
)

const version = "0.1.0-dev"

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
	case "render-point":
		return runRenderPoint(ctx, args[1:], stdout, stderr)
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
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
	store := iconeu.NewCachedStore(cfg.Paths.Data, cfg.App.ECCodesWorkers, cfg.App.PointCacheEntries, int64(cfg.App.PointCacheMemoryLimit), logf)
	series, err := store.Vertical(ctx, location)
	if err != nil {
		return err
	}
	surface, err := store.Surface(ctx, location)
	if err != nil {
		return err
	}
	surface = surface.Window(time.Now(), 72)
	cloud, err := store.Cloud(ctx, location)
	if err != nil {
		return err
	}
	cloud = cloud.Window(time.Now(), 72)
	result, err := render.All(*outputDirectory, series, render.Options{Width: cfg.Render.Width, Height: cfg.Render.Height, Language: *language})
	if err != nil {
		return err
	}
	sky, err := astronomy.Compute(location, surface.Frames[0].ValidAt, surface.Frames[len(surface.Frames)-1].ValidAt)
	if err != nil {
		return err
	}
	result.Weather = filepath.Join(*outputDirectory, "weather-hourly.png")
	if err := render.Weather(result.Weather, surface, sky, render.Options{Language: *language}); err != nil {
		return err
	}
	calibration := overallCalibration(cfg.Algorithms)
	result.CloudObstruction = filepath.Join(*outputDirectory, "cloud-obstruction-height-hourly.png")
	if err := render.CloudObstruction(result.CloudObstruction, cloud, calibration, render.Options{Width: 3200, Height: 1100, Language: *language}); err != nil {
		return err
	}
	overall, err := forecast.ComputeHourlyOverallIndex(series, surface, cloud, calibration)
	if err != nil {
		return err
	}
	result.OverallIndex = filepath.Join(*outputDirectory, "overall-astronomy-index-hourly.png")
	if err := render.OverallIndex(result.OverallIndex, series, overall, sky, render.Options{Width: 3200, Height: 960, Language: *language}); err != nil {
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
	if !cfg.Platforms.Telegram.Enabled {
		return errors.New("telegram is disabled in configuration")
	}
	token, err := os.ReadFile(cfg.Platforms.Telegram.TokenFile)
	if err != nil {
		return fmt.Errorf("read Telegram token file: %w", err)
	}
	client, err := telegram.NewClient(string(token))
	if err != nil {
		return err
	}
	handler, err := telegram.NewHandler(client)
	if err != nil {
		return err
	}
	logf := func(format string, values ...any) { writeLog(stderr, format, values...) }
	store := iconeu.NewCachedStore(
		cfg.Paths.Data, cfg.App.ECCodesWorkers, cfg.App.PointCacheEntries,
		int64(cfg.App.PointCacheMemoryLimit), logf,
	)
	handler.SetLogger(logf)
	database, err := pgstore.Open(ctx, cfg.Database.Host, cfg.Database.Port, cfg.Database.Name, cfg.Database.User, cfg.Database.Password, cfg.Database.MaxConns)
	if err != nil {
		return err
	}
	defer database.Close()
	go database.RunMaintenance(ctx, logf)
	if err := handler.EnablePersistence(database, cfg.Platforms.Telegram.AdminIDs); err != nil {
		return err
	}
	lightAtlas, err := lightpollution.NewAtlas(
		filepath.Join(cfg.Paths.Data, "light-pollution", "lorenz-atlas"),
		lightpollution.Options{AtlasYear: cfg.Providers.LightPollution.AtlasYear}, logf,
	)
	if err != nil {
		return err
	}
	if err := handler.EnableLightPollution(lightAtlas); err != nil {
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
	if err := handler.EnableWorldAtlas2015(worldAtlas); err != nil {
		return err
	}
	if err := handler.SetOverallIndexCalibration(overallCalibration(cfg.Algorithms)); err != nil {
		return err
	}
	if err := handler.EnableForecast(
		store,
		filepath.Join(cfg.Paths.Temp, "telegram-renders"),
		render.Options{Width: cfg.Render.Width, Height: cfg.Render.Height},
	); err != nil {
		return err
	}
	if err := handler.SetForecastMaxStaleAge(cfg.Providers.ICONEU.MaxStaleAge.Duration); err != nil {
		return err
	}
	if err := handler.EnableRenderCache(filepath.Join(cfg.Paths.Data, "cache", "renders", "telegram-render-v1")); err != nil {
		return err
	}
	syncClient := iconeu.NewClient()
	syncClient.Workers = cfg.Sync.DownloadParallelism
	syncClient.Progress = func(format string, values ...any) {
		writeLog(stderr, format, values...)
	}
	scheduler := app.ICONEUScheduler{
		Client: syncClient, DataRoot: cfg.Paths.Data,
		PollInterval: cfg.Sync.PollInterval.Duration, KeepRuns: cfg.Providers.ICONEU.KeepRuns,
		MaxStaleAge: cfg.Providers.ICONEU.MaxStaleAge.Duration,
		Logf:        func(format string, values ...any) { writeLog(stderr, format, values...) },
	}
	go scheduler.Run(ctx)
	if _, err := fmt.Fprintln(stdout, "Telegram long polling started"); err != nil {
		return err
	}
	return client.Run(ctx, handler, cfg.App.Workers, cfg.App.RequestTimeout.Duration, logf)
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
	result.Weather = filepath.Join(*outputDirectory, "weather-hourly.png")
	if err := render.Weather(result.Weather, surface, sky, render.Options{Language: *language}); err != nil {
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
	result.OverallIndex = filepath.Join(*outputDirectory, "overall-astronomy-index-hourly.png")
	if err := render.OverallIndex(result.OverallIndex, series, overall, sky, render.Options{Width: 3200, Height: 960, Language: *language}); err != nil {
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

func printUsage(writer io.Writer) error {
	_, err := fmt.Fprintln(writer, `bot_astrosferum commands:
  doctor         validate config, directories, tools, disk, and secret files
  serve          run enabled platform adapters with long polling
  parse-location parse text coordinates and resolve their IANA timezone
  extract-point  extract one value from a regular-grid GRIB2 file
  light-pollution query annual modeled zenith brightness for coordinates
  render-sample  render PNG charts from deterministic synthetic fixtures
  sync-icon-eu   atomically sync one complete ICON-EU pressure-level wind run
	  render-point   render seven charts for a point from the current ICON-EU run
  version        print the build version`)
	return err
}

func writeLog(writer io.Writer, format string, values ...any) {
	_, _ = fmt.Fprintf(writer, format+"\n", values...)
}

func overallCalibration(config config.AlgorithmsConfig) forecast.OverallIndexCalibration {
	return forecast.OverallIndexCalibration{
		SeeingWeight: config.OverallSeeingWeight, CloudWeight: config.OverallCloudWeight,
		CoherenceTimeWeight: config.OverallCoherenceTimeWeight,
		PossibleFogFactor:   config.OverallPossibleFogFactor, HighFogFactor: config.OverallHighFogFactor,
		GoodSeeingArcsec: config.OverallGoodSeeingArcsec, BadSeeingArcsec: config.OverallBadSeeingArcsec,
		BestCoherenceTimeMS:          config.OverallBestCoherenceTimeMS,
		BadCoherenceTimeMS:           config.OverallBadCoherenceTimeMS,
		BoundaryLayerMinM:            config.OverallBoundaryLayerMinM,
		BoundaryLayerTopM:            config.OverallBoundaryLayerTopM,
		GroundCn2Scale:               config.OverallGroundCn2Scale,
		UnresolvedCloudObstruction:   config.OverallUnresolvedCloudObstruction,
		SurfaceWindMaxPenalty:        config.OverallSurfaceWindMaxPenalty,
		SurfaceWindStartMS:           config.OverallSurfaceWindStartMS,
		SurfaceWindFullMS:            config.OverallSurfaceWindFullMS,
		SurfaceGustStartMS:           config.OverallSurfaceGustStartMS,
		SurfaceGustFullMS:            config.OverallSurfaceGustFullMS,
		CloudLiquidRadiusMicrometers: config.CloudLiquidRadiusMicrometers,
		CloudIceRadiusMicrometers:    config.CloudIceRadiusMicrometers,
	}
}
