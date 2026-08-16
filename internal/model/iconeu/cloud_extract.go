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

const iconEUSurfaceHalfLevel = 75

func (store VerticalStore) Cloud(ctx context.Context, location forecast.Location) (forecast.CloudSeries, error) {
	manifest, err := LoadCurrent(store.DataRoot)
	if err != nil {
		return forecast.CloudSeries{}, err
	}
	if !manifest.HasHourlyCloud() {
		return forecast.CloudSeries{}, fmt.Errorf("current ICON-EU run has no hourly cloud bundle")
	}
	if !manifest.Grid.Contains(location) {
		return forecast.CloudSeries{}, fmt.Errorf("coordinates are outside the current ICON-EU domain")
	}
	runner := store.Runner
	if runner == nil {
		runner = execRunner{}
	}
	heights, err := ExtractCloudHeights(ctx, runner, filepath.Join(manifest.Directory, manifest.CloudGeometry.File), location)
	if err != nil {
		return forecast.CloudSeries{}, err
	}
	surfaceElevationM, err := CloudSurfaceElevation(heights, iconEUSurfaceHalfLevel, "ICON-EU")
	if err != nil {
		return forecast.CloudSeries{}, err
	}
	frames := make([]forecast.CloudFrame, len(manifest.CloudSteps))
	workers := store.Workers
	if workers < 1 {
		workers = 4
	}
	type result struct {
		index int
		frame forecast.CloudFrame
		err   error
	}
	workContext, cancel := context.WithCancel(ctx)
	defer cancel()
	jobs, results := make(chan int), make(chan result, len(frames))
	var group sync.WaitGroup
	for range workers {
		group.Add(1)
		go func() {
			defer group.Done()
			for index := range jobs {
				step := manifest.CloudSteps[index]
				frame, extractionError := ExtractCloudFrame(workContext, runner, filepath.Join(manifest.Directory, step.File), location, step.ValidAt, manifest.CloudModelLevels, DefaultCloudGroundModelLevels, heights)
				results <- result{index: index, frame: frame, err: extractionError}
				if extractionError != nil {
					cancel()
					return
				}
			}
		}()
	}
	go func() {
		defer close(jobs)
		for index := range frames {
			select {
			case jobs <- index:
			case <-workContext.Done():
				return
			}
		}
	}()
	go func() { group.Wait(); close(results) }()
	completed := 0
	for item := range results {
		if item.err != nil {
			return forecast.CloudSeries{}, item.err
		}
		frames[item.index] = item.frame
		completed++
	}
	if completed != len(frames) {
		return forecast.CloudSeries{}, fmt.Errorf("extracted %d of %d cloud frames", completed, len(frames))
	}
	return forecast.CloudSeries{
		Location: location, Provider: manifest.Provider, Product: cloudProductName,
		RunID: manifest.RunID, BaseTime: manifest.BaseTime, GeneratedAt: time.Now().UTC(),
		TurbulenceValidUntil: manifest.CloudSteps[len(manifest.CloudSteps)-1].ValidAt,
		SurfaceElevationM:    surfaceElevationM, Frames: frames,
	}, nil
}

// ExtractCloudHeights reads native HHL geometry after any provider-specific
// horizontal remapping has been applied.
func ExtractCloudHeights(ctx context.Context, runner CommandRunner, path string, location forecast.Location) (map[int]float64, error) {
	values, err := extractCloudValues(ctx, runner, path, location)
	if err != nil {
		return nil, err
	}
	heights := make(map[int]float64, len(values))
	for key, value := range values {
		if key.name == "HHL" {
			heights[key.level] = value
		}
	}
	return heights, nil
}

// CloudSurfaceElevation returns the provider's lowest half-level height.
func CloudSurfaceElevation(heights map[int]float64, surfaceHalfLevel int, provider string) (float64, error) {
	value, ok := heights[surfaceHalfLevel]
	if !ok || math.IsNaN(value) || math.IsInf(value, 0) {
		return 0, fmt.Errorf("current %s cloud geometry has no valid HHL%d surface elevation", provider, surfaceHalfLevel)
	}
	return value, nil
}

// ExtractCloudFrame normalizes one provider's native model-layer bundle.
func ExtractCloudFrame(ctx context.Context, runner CommandRunner, path string, location forecast.Location, validAt time.Time, modelLevels, groundModelLevels []int, heights map[int]float64) (forecast.CloudFrame, error) {
	values, err := extractCloudValues(ctx, runner, path, location)
	if err != nil {
		return forecast.CloudFrame{}, err
	}
	return cloudFrameFromValues(values, validAt, modelLevels, groundModelLevels, heights, filepath.Base(path))
}

func cloudFrameFromValues(values map[cloudValueKey]float64, validAt time.Time, modelLevels, groundModelLevels []int, heights map[int]float64, sourceName string) (forecast.CloudFrame, error) {
	frame := forecast.CloudFrame{
		ValidAt: validAt, Levels: make([]forecast.CloudLevel, 0, len(modelLevels)),
		TurbulenceLevels: make([]forecast.CloudLevel, 0, len(groundModelLevels)),
	}
	for _, level := range modelLevels {
		cover, coverOK := values[cloudValueKey{"ccl", level}]
		pressure, pressureOK := values[cloudValueKey{"pres", level}]
		temperature, temperatureOK := values[cloudValueKey{"t", level}]
		liquid, liquidOK := values[cloudValueKey{"qc", level}]
		ice, iceOK := values[cloudValueKey{"qi", level}]
		halfLevelA, halfLevelAOK := heights[level]
		halfLevelB, halfLevelBOK := heights[level+1]
		if !coverOK || !pressureOK || !temperatureOK || !liquidOK || !iceOK || !halfLevelAOK || !halfLevelBOK {
			return forecast.CloudFrame{}, fmt.Errorf("%s is incomplete at model level %d", sourceName, level)
		}
		layerThicknessM := math.Abs(halfLevelA - halfLevelB)
		if !finitePositiveCloudExtraction(pressure) || !finitePositiveCloudExtraction(temperature) || !finitePositiveCloudExtraction(layerThicknessM) {
			return forecast.CloudFrame{}, fmt.Errorf("%s has invalid pressure, temperature, or HHL thickness at model level %d", sourceName, level)
		}
		frame.Levels = append(frame.Levels, forecast.CloudLevel{
			ModelLevel: level, PressureHPA: pressure / 100, HeightM: (halfLevelA + halfLevelB) / 2, LayerThicknessM: layerThicknessM,
			TemperatureK: temperature, UMS: math.NaN(), VMS: math.NaN(), TKEJkg: math.NaN(),
			CoverPercent: cover, CloudLiquidKgKg: math.Max(0, liquid), CloudIceKgKg: math.Max(0, ice),
		})
	}
	for _, level := range groundModelLevels {
		pressure, pressureOK := values[cloudValueKey{"pres", level}]
		temperature, temperatureOK := values[cloudValueKey{"t", level}]
		u, uOK := values[cloudValueKey{"u", level}]
		v, vOK := values[cloudValueKey{"v", level}]
		lowerTKE, lowerTKEOK := values[cloudValueKey{"tke", level}]
		upperTKE, upperTKEOK := values[cloudValueKey{"tke", level + 1}]
		halfLevelA, halfLevelAOK := heights[level]
		halfLevelB, halfLevelBOK := heights[level+1]
		if !pressureOK || !temperatureOK || !uOK || !vOK || lowerTKEOK != upperTKEOK || !halfLevelAOK || !halfLevelBOK {
			return forecast.CloudFrame{}, fmt.Errorf("%s has incomplete native turbulence state at model level %d", sourceName, level)
		}
		layerThicknessM := math.Abs(halfLevelA - halfLevelB)
		if !finitePositiveCloudExtraction(pressure) || !finitePositiveCloudExtraction(temperature) ||
			!finitePositiveCloudExtraction(layerThicknessM) || math.IsNaN(u) || math.IsInf(u, 0) || math.IsNaN(v) || math.IsInf(v, 0) {
			return forecast.CloudFrame{}, fmt.Errorf("%s has invalid native turbulence state at model level %d", sourceName, level)
		}
		tke := math.NaN()
		if lowerTKEOK {
			if !finiteCloudExtraction(lowerTKE) || lowerTKE < 0 || !finiteCloudExtraction(upperTKE) || upperTKE < 0 {
				return forecast.CloudFrame{}, fmt.Errorf("%s has invalid native TKE at model level %d", sourceName, level)
			}
			tke = (lowerTKE + upperTKE) / 2
		}
		frame.TurbulenceLevels = append(frame.TurbulenceLevels, forecast.CloudLevel{
			ModelLevel: level, PressureHPA: pressure / 100,
			HeightM: (halfLevelA + halfLevelB) / 2, LayerThicknessM: layerThicknessM,
			TemperatureK: temperature, UMS: u, VMS: v, TKEJkg: tke,
		})
	}
	return frame, nil
}

func containsCloudModelLevel(levels []int, wanted int) bool {
	index := sort.SearchInts(levels, wanted)
	return index < len(levels) && levels[index] == wanted
}

func finitePositiveCloudExtraction(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value > 0
}

func finiteCloudExtraction(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}

type cloudValueKey struct {
	name  string
	level int
}

func extractCloudValues(ctx context.Context, runner CommandRunner, path string, location forecast.Location) (map[cloudValueKey]float64, error) {
	coordinates := fmt.Sprintf("%.6f,%.6f,1", location.Latitude, location.Longitude)
	output, err := runner.CombinedOutput(ctx, "grib_get", "-f", "-F", "%.10g", "-p", "shortName,level", "-l", coordinates, path)
	if err != nil {
		return nil, fmt.Errorf("extract cloud %s: %s", filepath.Base(path), limitedOutput(output))
	}
	values := make(map[cloudValueKey]float64)
	scanner := bufio.NewScanner(strings.NewReader(string(output)))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) != 3 {
			return nil, fmt.Errorf("unexpected cloud output in %s", filepath.Base(path))
		}
		level, levelError := strconv.Atoi(fields[1])
		value, valueError := strconv.ParseFloat(fields[2], 64)
		if levelError != nil || valueError != nil {
			return nil, fmt.Errorf("invalid cloud output in %s", filepath.Base(path))
		}
		values[cloudValueKey{canonicalCloudShortName(fields[0]), level}] = value
	}
	return values, scanner.Err()
}
