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

type ExtractedSurface struct {
	Frame               forecast.SurfaceFrame
	AccumulatedPrecipMM float64
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
		data  ExtractedSurface
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
				data, err := ExtractSurfaceFrame(workContext, runner, filepath.Join(manifest.Directory, step.File), location, step.ValidAt)
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
	extracted := make([]ExtractedSurface, len(steps))
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
		frame := data.Frame
		if index == 0 {
			frame.PrecipitationMM = math.Max(0, data.AccumulatedPrecipMM)
		} else {
			frame.PrecipitationMM = math.Max(0, data.AccumulatedPrecipMM-previousTotal)
		}
		previousTotal = data.AccumulatedPrecipMM
		frames[index] = frame
	}
	return forecast.SurfaceSeries{
		Location: location, Provider: manifest.Provider, Product: "ICON-EU single-level",
		RunID: manifest.RunID, BaseTime: manifest.BaseTime, GeneratedAt: time.Now().UTC(),
		StepHours: steps[1].ForecastHour - steps[0].ForecastHour, Frames: frames,
	}, nil
}

// ExtractSurfaceFrame reads one regular-grid single-level bundle at a point.
// Optional visibility is allowed so ICON Global can use the same normalized
// extraction path without fabricating a field that DWD does not publish.
func ExtractSurfaceFrame(ctx context.Context, runner CommandRunner, path string, location forecast.Location, validAt time.Time) (ExtractedSurface, error) {
	coordinates := fmt.Sprintf("%.6f,%.6f,1", location.Latitude, location.Longitude)
	output, err := runner.CombinedOutput(ctx, "grib_get", "-f", "-F", "%.10g", "-p", "shortName", "-l", coordinates, path)
	if err != nil {
		return ExtractedSurface{}, fmt.Errorf("extract surface %s: %s", filepath.Base(path), limitedOutput(output))
	}
	values := make(map[string]float64, len(surfaceFields))
	scanner := bufio.NewScanner(strings.NewReader(string(output)))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) != 2 {
			return ExtractedSurface{}, fmt.Errorf("unexpected surface output in %s", filepath.Base(path))
		}
		value, err := strconv.ParseFloat(fields[1], 64)
		if err != nil {
			return ExtractedSurface{}, fmt.Errorf("invalid %s value in %s", fields[0], filepath.Base(path))
		}
		values[canonicalSurfaceShortName(fields[0])] = value
	}
	if err := scanner.Err(); err != nil {
		return ExtractedSurface{}, err
	}
	return surfaceFromValues(values, validAt, filepath.Base(path))
}

func surfaceFromValues(values map[string]float64, validAt time.Time, sourceName string) (ExtractedSurface, error) {
	for _, shortName := range []string{"2t", "2d", "2r", "CLCT", "tp", "10u", "10v", "prmsl", "mld"} {
		if _, ok := values[shortName]; !ok {
			return ExtractedSurface{}, fmt.Errorf("%s is missing %s", sourceName, shortName)
		}
	}
	mixedLayerDepthM := values["mld"]
	if math.IsNaN(mixedLayerDepthM) || math.IsInf(mixedLayerDepthM, 0) || mixedLayerDepthM < 0 {
		return ExtractedSurface{}, fmt.Errorf("%s has invalid mixed-layer depth %v m", sourceName, mixedLayerDepthM)
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
	gust, hasGust := values["VMAX_10M"]
	if !hasGust {
		// DWD does not publish an interval maximum at forecast hour zero.
		gust = math.Hypot(u, v)
	}
	visibilityKM := 0.0
	if hasVisibility {
		visibilityKM = math.Max(0, values["vis"]/1000)
	}
	frame := forecast.SurfaceFrame{
		ValidAt: validAt, TemperatureC: values["2t"] - 273.15, DewPointC: values["2d"] - 273.15,
		RelativeHumidityPercent: clampSurface(values["2r"], 0, 100),
		CloudCoverPercent:       clampSurface(values["CLCT"], 0, 100),
		LowCloudCoverPercent:    clampSurface(values["CLCL"], 0, 100),
		MidCloudCoverPercent:    clampSurface(values["CLCM"], 0, 100),
		HighCloudCoverPercent:   clampSurface(values["CLCH"], 0, 100),
		WindSpeedMS:             math.Hypot(u, v),
		WindGustMS:              math.Max(0, gust), PressureHPA: values["prmsl"] / 100,
		WindDirectionDegrees:     math.Mod(math.Atan2(-u, -v)*180/math.Pi+360, 360),
		VisibilityKM:             visibilityKM,
		PrecipitableWaterMM:      math.Max(0, values["TQV"]),
		CloudLiquidPathKgM2:      math.Max(0, values["TQC"]),
		CloudIcePathKgM2:         math.Max(0, values["TQI"]),
		MixedLayerDepthM:         mixedLayerDepthM,
		CloudCondensateAvailable: hasCloudLiquidPath && hasCloudIcePath,
		TransparencyAvailable:    hasVisibility && hasWaterVapour,
	}
	return ExtractedSurface{Frame: frame, AccumulatedPrecipMM: math.Max(0, values["tp"])}, nil
}

func clampSurface(value, minimum, maximum float64) float64 {
	return math.Max(minimum, math.Min(maximum, value))
}
