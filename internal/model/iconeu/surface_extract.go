package iconeu

import (
	"bufio"
	"context"
	"fmt"
	"math"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"bot_astrosferum/internal/forecast"
)

type extractedSurface struct {
	frame         forecast.SurfaceFrame
	precipitation float64
}

func (store VerticalStore) Surface(ctx context.Context, location forecast.Location) (forecast.SurfaceSeries, error) {
	manifest, err := LoadCurrent(store.DataRoot)
	if err != nil {
		return forecast.SurfaceSeries{}, err
	}
	if !manifest.HasSurface() {
		return forecast.SurfaceSeries{}, fmt.Errorf("current ICON-EU run has no surface bundle")
	}
	if !manifest.Grid.Contains(location) {
		return forecast.SurfaceSeries{}, fmt.Errorf("coordinates are outside the current ICON-EU domain")
	}
	steps := manifest.SurfaceForecastSteps()
	if len(steps) < 2 {
		return forecast.SurfaceSeries{}, fmt.Errorf("current ICON-EU run has no usable surface steps")
	}
	runner := store.Runner
	if runner == nil {
		runner = execRunner{}
	}
	workers := store.Workers
	if workers < 1 {
		workers = 4
	}
	type result struct {
		index int
		data  extractedSurface
		err   error
	}
	workContext, cancel := context.WithCancel(ctx)
	defer cancel()
	jobs := make(chan int)
	results := make(chan result, len(steps))
	var waitGroup sync.WaitGroup
	for range workers {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			for index := range jobs {
				step := steps[index]
				data, err := extractSurfaceFrame(workContext, runner, filepath.Join(manifest.Directory, step.File), location, step.ValidAt)
				results <- result{index: index, data: data, err: err}
				if err != nil {
					cancel()
					return
				}
			}
		}()
	}
	go func() {
		defer close(jobs)
		for index := range steps {
			select {
			case jobs <- index:
			case <-workContext.Done():
				return
			}
		}
	}()
	go func() {
		waitGroup.Wait()
		close(results)
	}()
	extracted := make([]extractedSurface, len(steps))
	completed := 0
	for item := range results {
		if item.err != nil {
			return forecast.SurfaceSeries{}, item.err
		}
		extracted[item.index] = item.data
		completed++
	}
	if completed != len(extracted) {
		return forecast.SurfaceSeries{}, fmt.Errorf("extracted %d of %d ICON-EU surface frames", completed, len(extracted))
	}
	frames := make([]forecast.SurfaceFrame, len(extracted))
	previousTotal := 0.0
	for index, data := range extracted {
		frame := data.frame
		if index == 0 {
			frame.PrecipitationMM = math.Max(0, data.precipitation)
		} else {
			frame.PrecipitationMM = math.Max(0, data.precipitation-previousTotal)
		}
		previousTotal = data.precipitation
		frames[index] = frame
	}
	return forecast.SurfaceSeries{
		Location: location, Provider: manifest.Provider, Product: "ICON-EU single-level",
		RunID: manifest.RunID, BaseTime: manifest.BaseTime, GeneratedAt: time.Now().UTC(),
		StepHours: steps[1].ForecastHour - steps[0].ForecastHour, Frames: frames,
	}, nil
}

func extractSurfaceFrame(ctx context.Context, runner CommandRunner, path string, location forecast.Location, validAt time.Time) (extractedSurface, error) {
	coordinates := fmt.Sprintf("%.6f,%.6f,1", location.Latitude, location.Longitude)
	output, err := runner.CombinedOutput(ctx, "grib_get", "-f", "-F", "%.10g", "-p", "shortName", "-l", coordinates, path)
	if err != nil {
		return extractedSurface{}, fmt.Errorf("extract surface %s: %s", filepath.Base(path), limitedOutput(output))
	}
	values := make(map[string]float64, len(surfaceFields))
	scanner := bufio.NewScanner(strings.NewReader(string(output)))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) != 2 {
			return extractedSurface{}, fmt.Errorf("unexpected surface output in %s", filepath.Base(path))
		}
		value, err := strconv.ParseFloat(fields[1], 64)
		if err != nil {
			return extractedSurface{}, fmt.Errorf("invalid %s value in %s", fields[0], filepath.Base(path))
		}
		values[canonicalSurfaceShortName(fields[0])] = value
	}
	for _, shortName := range []string{"2t", "2d", "2r", "CLCT", "tp", "10u", "10v", "VMAX_10M", "prmsl", "mld"} {
		if _, ok := values[shortName]; !ok {
			return extractedSurface{}, fmt.Errorf("%s is missing %s", filepath.Base(path), shortName)
		}
	}
	mixedLayerDepthM := values["mld"]
	if math.IsNaN(mixedLayerDepthM) || math.IsInf(mixedLayerDepthM, 0) || mixedLayerDepthM < 0 {
		return extractedSurface{}, fmt.Errorf("%s has invalid mixed-layer depth %v m", filepath.Base(path), mixedLayerDepthM)
	}
	// Old manifests remain readable while a richer hourly field set is being
	// published. Until the atomic switch, total cover is the honest fallback
	// for the layer rows rather than a fabricated zero.
	for _, shortName := range []string{"CLCL", "CLCM", "CLCH"} {
		if _, ok := values[shortName]; !ok {
			values[shortName] = values["CLCT"]
		}
	}
	_, hasVisibility := values["vis"]
	_, hasWaterVapour := values["TQV"]
	_, hasCloudLiquidPath := values["TQC"]
	_, hasCloudIcePath := values["TQI"]
	u, v := values["10u"], values["10v"]
	frame := forecast.SurfaceFrame{
		ValidAt: validAt, TemperatureC: values["2t"] - 273.15, DewPointC: values["2d"] - 273.15,
		RelativeHumidityPercent: clampSurface(values["2r"], 0, 100),
		CloudCoverPercent:       clampSurface(values["CLCT"], 0, 100),
		LowCloudCoverPercent:    clampSurface(values["CLCL"], 0, 100),
		MidCloudCoverPercent:    clampSurface(values["CLCM"], 0, 100),
		HighCloudCoverPercent:   clampSurface(values["CLCH"], 0, 100),
		WindSpeedMS:             math.Hypot(u, v),
		WindGustMS:              math.Max(0, values["VMAX_10M"]), PressureHPA: values["prmsl"] / 100,
		WindDirectionDegrees:     math.Mod(math.Atan2(-u, -v)*180/math.Pi+360, 360),
		VisibilityKM:             math.Max(0, values["vis"]/1000),
		PrecipitableWaterMM:      math.Max(0, values["TQV"]),
		CloudLiquidPathKgM2:      math.Max(0, values["TQC"]),
		CloudIcePathKgM2:         math.Max(0, values["TQI"]),
		MixedLayerDepthM:         mixedLayerDepthM,
		CloudCondensateAvailable: hasCloudLiquidPath && hasCloudIcePath,
		TransparencyAvailable:    hasVisibility && hasWaterVapour,
	}
	return extractedSurface{frame: frame, precipitation: math.Max(0, values["tp"])}, nil
}

func clampSurface(value, minimum, maximum float64) float64 {
	return math.Max(minimum, math.Min(maximum, value))
}
