package iconeu

import (
	"bufio"
	"context"
	"fmt"
	"math"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"bot_astrosferum/internal/forecast"
)

type VerticalStore struct {
	DataRoot string
	Runner   CommandRunner
	Workers  int
}

func (store VerticalStore) Vertical(ctx context.Context, location forecast.Location) (forecast.VerticalSeries, error) {
	manifest, err := LoadCurrent(store.DataRoot)
	if err != nil {
		return forecast.VerticalSeries{}, err
	}
	if !manifest.Grid.Contains(location) {
		return forecast.VerticalSeries{}, fmt.Errorf("coordinates are outside the current ICON-EU domain")
	}
	runner := store.Runner
	if runner == nil {
		runner = execRunner{}
	}
	workers := store.Workers
	if workers < 1 {
		workers = 4
	}

	type extraction struct {
		index int
		frame forecast.VerticalFrame
		err   error
	}
	workContext, cancel := context.WithCancel(ctx)
	defer cancel()
	jobs := make(chan int)
	results := make(chan extraction, len(manifest.Steps))
	var waitGroup sync.WaitGroup
	for range workers {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			for index := range jobs {
				step := manifest.Steps[index]
				frame, err := ExtractFrame(workContext, runner, filepath.Join(manifest.Directory, step.File), location, step)
				results <- extraction{index: index, frame: frame, err: err}
				if err != nil {
					cancel()
					return
				}
			}
		}()
	}
	go func() {
		defer close(jobs)
		for index := range manifest.Steps {
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
	frames := make([]forecast.VerticalFrame, len(manifest.Steps))
	completed := 0
	for result := range results {
		if result.err != nil {
			return forecast.VerticalSeries{}, result.err
		}
		frames[result.index] = result.frame
		completed++
	}
	if completed != len(frames) {
		return forecast.VerticalSeries{}, fmt.Errorf("extracted %d of %d ICON-EU frames", completed, len(frames))
	}
	series := forecast.VerticalSeries{
		Location: location, Provider: manifest.Provider, Product: manifest.Product,
		RunID: manifest.RunID, Grid: manifest.Grid.GridName,
		AlgorithmVersion: forecast.SeeingPrototypeVersion, BaseTime: manifest.BaseTime,
		GeneratedAt: time.Now().UTC(), Frames: frames,
	}
	if err := series.Validate(); err != nil {
		return forecast.VerticalSeries{}, fmt.Errorf("validate extracted ICON-EU series: %w", err)
	}
	return series, nil
}

// ExtractFrame reads one regular-grid pressure-level bundle at a point.  It is
// shared with ICON Global after that provider has remapped its native grid to
// a one-point regular grid.
func ExtractFrame(ctx context.Context, runner CommandRunner, path string, location forecast.Location, step StepFile) (forecast.VerticalFrame, error) {
	coordinates := strconv.FormatFloat(location.Latitude, 'f', 6, 64) + "," + strconv.FormatFloat(location.Longitude, 'f', 6, 64) + ",1"
	output, err := runner.CombinedOutput(ctx, "grib_get", "-f", "-F", "%.10g", "-p", "shortName,level", "-l", coordinates, path)
	if err != nil {
		return forecast.VerticalFrame{}, fmt.Errorf("extract %s: %s", filepath.Base(path), limitedOutput(output))
	}
	type vector struct {
		u, v, height, temperature float64
		hasU, hasV, hasZ, hasT    bool
	}
	vectors := make(map[float64]vector)
	scanner := bufio.NewScanner(strings.NewReader(string(output)))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) != 3 {
			return forecast.VerticalFrame{}, fmt.Errorf("unexpected point output in %s", filepath.Base(path))
		}
		level, levelError := strconv.ParseFloat(fields[1], 64)
		value, valueError := strconv.ParseFloat(fields[2], 64)
		if levelError != nil || valueError != nil {
			return forecast.VerticalFrame{}, fmt.Errorf("invalid point value in %s", filepath.Base(path))
		}
		item := vectors[level]
		switch fields[0] {
		case "u":
			item.u, item.hasU = value, true
		case "v":
			item.v, item.hasV = value, true
		case "z":
			item.height, item.hasZ = value/9.80665, true
		case "t":
			item.temperature, item.hasT = value, true
		default:
			return forecast.VerticalFrame{}, fmt.Errorf("unexpected variable %q", fields[0])
		}
		vectors[level] = item
	}
	levels := make([]forecast.VerticalLevel, 0, len(vectors))
	for pressure, vector := range vectors {
		if !vector.hasU || !vector.hasV {
			return forecast.VerticalFrame{}, fmt.Errorf("incomplete wind vector at %.0f hPa", pressure)
		}
		height := vector.height
		if !vector.hasZ {
			height = standardPressureHeight(pressure)
		}
		temperature := math.NaN()
		if vector.hasT {
			temperature = vector.temperature
		}
		levels = append(levels, forecast.VerticalLevel{PressureHPA: pressure, HeightM: height, TemperatureK: temperature, UMS: vector.u, VMS: vector.v})
	}
	sort.Slice(levels, func(i, j int) bool { return levels[i].PressureHPA > levels[j].PressureHPA })
	expectedLevels := len(DefaultPressureLevelsHPA)
	if step.Messages > 0 && step.Messages%4 == 0 {
		expectedLevels = step.Messages / 4
	}
	if len(levels) != expectedLevels {
		return forecast.VerticalFrame{}, fmt.Errorf("%s produced %d pressure levels, expected %d", filepath.Base(path), len(levels), expectedLevels)
	}
	confidence := math.Max(0.65, 0.96-0.26*float64(step.ForecastHour)/72)
	return forecast.VerticalFrame{ValidAt: step.ValidAt, Levels: levels, Confidence: confidence}, nil
}

func standardPressureHeight(pressureHPA float64) float64 {
	return 44330 * (1 - math.Pow(pressureHPA/1013.25, 0.190284))
}

func limitedOutput(output []byte) string {
	text := strings.TrimSpace(string(output))
	if len(text) > 300 {
		return text[:300] + "…"
	}
	return text
}
