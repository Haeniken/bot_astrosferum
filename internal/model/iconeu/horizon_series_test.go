package iconeu

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"bot_astrosferum/internal/forecast"
)

type horizonSeriesRunner struct {
	mu        sync.Mutex
	calls     int
	active    int
	maxActive int
}

func (runner *horizonSeriesRunner) CombinedOutput(_ context.Context, name string, args ...string) ([]byte, error) {
	runner.mu.Lock()
	runner.calls++
	runner.active++
	if runner.active > runner.maxActive {
		runner.maxActive = runner.active
	}
	runner.mu.Unlock()
	defer func() {
		runner.mu.Lock()
		runner.active--
		runner.mu.Unlock()
	}()
	if name == "grib_get" {
		if len(args) < horizonForecastHours {
			return nil, fmt.Errorf("synthetic packing-error request has %d arguments", len(args))
		}
		return []byte(strings.Repeat("0.001\n", horizonForecastHours)), nil
	}
	for _, argument := range args {
		if strings.HasPrefix(argument, "gennn,") {
			if err := os.WriteFile(args[len(args)-1], []byte("synthetic weights"), 0o600); err != nil {
				return nil, err
			}
			return nil, nil
		}
	}
	points, err := horizonSeriesRunnerPoints(args)
	if err != nil {
		return nil, err
	}
	source := filepath.Base(args[len(args)-1])
	var output strings.Builder
	row := func(name string, level, value float64) {
		for _, point := range points {
			fmt.Fprintf(&output, "%s %.0f %.8f %.8f %.12g\n", name, level, point.Longitude, point.Latitude, value)
		}
	}
	switch {
	case source == "geometry":
		for _, level := range cloudGeometryLevels() {
			row("HHL", float64(level), 100+float64(75-level)*300)
		}
	case strings.HasPrefix(source, "pressure-f"):
		hour := horizonTestForecastHour(source)
		for _, pressure := range DefaultPressureLevelsHPA {
			level := float64(pressure * 100)
			row("u", level, float64(hour))
			row("v", level, 2)
			row("z", level, standardPressureHeight(float64(pressure))*9.80665)
			row("t", level, 210+0.07*float64(pressure))
		}
	case strings.HasPrefix(source, "surface-f"):
		hour := horizonTestSurfaceForecastHour(source)
		accumulatedPrecipitation := float64(hour)
		switch hour {
		case 2:
			// This small decrease is inside the combined 0.002-mm packing
			// enclosure and must be classified as indistinguishable from zero.
			accumulatedPrecipitation = 0.999
		}
		values := map[string]float64{
			"2t": 283.15, "2d": 278.15, "2r": 70,
			"CLCT": 0, "CLCL": 0, "CLCM": 0, "CLCH": 0,
			"tp": accumulatedPrecipitation, "10u": 3, "10v": 2, "VMAX_10M": 5,
			"prmsl": 101325, "vis": 50000, "TQV": 12,
			"TQC": 0, "TQI": 0, "mld": 500,
		}
		for _, field := range surfaceFields {
			row(field.ShortName, 0, values[field.ShortName])
		}
	case strings.HasPrefix(source, "cloud-f"):
		for _, level := range DefaultCloudModelLevels {
			row("ccl", float64(level), 0)
			row("pres", float64(level), 100000-float64(75-level)*1500)
			row("t", float64(level), 280-float64(75-level)*0.9)
			row("qc", float64(level), 0)
			row("qi", float64(level), 0)
		}
		for _, level := range DefaultCloudGroundModelLevels {
			row("u", float64(level), 4)
			row("v", float64(level), 2)
		}
		for _, level := range cloudTKEHalfLevels() {
			row("tke", float64(level), 0.1)
		}
	default:
		return nil, fmt.Errorf("unexpected synthetic horizon source %q", source)
	}
	return []byte(output.String()), nil
}

func horizonSeriesRunnerPoints(args []string) ([]batchPoint, error) {
	var gridPath string
	for _, argument := range args {
		if strings.HasPrefix(argument, "-remapnn,") {
			gridPath = strings.TrimPrefix(argument, "-remapnn,")
			break
		}
		if strings.HasPrefix(argument, "-remap,") {
			gridPath = strings.SplitN(strings.TrimPrefix(argument, "-remap,"), ",", 2)[0]
			break
		}
	}
	if gridPath == "" {
		return nil, fmt.Errorf("synthetic horizon runner did not receive a target grid")
	}
	content, err := os.ReadFile(gridPath)
	if err != nil {
		return nil, err
	}
	var longitudeFields, latitudeFields []string
	for _, line := range strings.Split(string(content), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 || fields[1] != "=" {
			continue
		}
		switch fields[0] {
		case "xvals":
			longitudeFields = fields[2:]
		case "yvals":
			latitudeFields = fields[2:]
		}
	}
	if len(longitudeFields) == 0 || len(longitudeFields) != len(latitudeFields) {
		return nil, fmt.Errorf("synthetic horizon target grid is invalid")
	}
	points := make([]batchPoint, len(longitudeFields))
	for index := range points {
		longitude, longitudeErr := strconv.ParseFloat(longitudeFields[index], 64)
		latitude, latitudeErr := strconv.ParseFloat(latitudeFields[index], 64)
		if longitudeErr != nil || latitudeErr != nil {
			return nil, fmt.Errorf("synthetic horizon target coordinate is invalid")
		}
		points[index] = batchPoint{Latitude: latitude, Longitude: longitude}
	}
	return points, nil
}

func horizonTestForecastHour(name string) int {
	value := strings.TrimSuffix(strings.TrimPrefix(name, "pressure-f"), ".grib2")
	hour, _ := strconv.Atoi(value)
	return hour
}

func horizonTestSurfaceForecastHour(name string) int {
	value := strings.TrimSuffix(strings.TrimPrefix(name, "surface-f"), ".grib2")
	hour, _ := strconv.Atoi(value)
	return hour
}

func (runner *horizonSeriesRunner) counts() (calls, maximum int) {
	runner.mu.Lock()
	defer runner.mu.Unlock()
	return runner.calls, runner.maxActive
}

func TestHorizonSeriesBuilds72PublishedHoursAndInterpolatesRawPressureState(t *testing.T) {
	base := time.Date(2026, 7, 22, 6, 0, 0, 0, time.UTC)
	manifest := syntheticHorizonSeriesManifest(base)
	runner := &horizonSeriesRunner{}
	store := NewHorizonStore("/models", t.TempDir(), 2, nil)
	store.extractor.runner = runner
	store.loadCurrent = func(string) (LoadedManifest, error) { return manifest, nil }
	store.loadRun = func(string, string) (LoadedManifest, error) { return manifest, nil }
	location := forecast.Location{Latitude: 59.9386, Longitude: 30.3141, TimeZone: "Europe/Moscow"}
	plan, err := forecast.NewHorizonPlan(location, 100)
	if err != nil {
		t.Fatal(err)
	}
	snapshots, err := store.Series(context.Background(), manifest.RunID, plan)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshots) != 72 || !snapshots[0].ValidAt.Equal(base.Add(time.Hour)) || !snapshots[71].ValidAt.Equal(base.Add(72*time.Hour)) {
		t.Fatalf("unexpected series bounds: %d %v..%v", len(snapshots), snapshots[0].ValidAt, snapshots[len(snapshots)-1].ValidAt)
	}
	intermediate := snapshots[0].Directions[0].Samples[0].Vertical
	if !intermediate.ValidAt.Equal(base.Add(time.Hour)) || intermediate.Levels[0].UMS != 1 {
		t.Fatalf("raw pressure interpolation at f001 = %+v", intermediate)
	}
	if got := snapshots[0].ObserverSurface.PrecipitationMM; got != 1 {
		t.Fatalf("physical f000..f001 precipitation interval = %v mm, want 1", got)
	}
	if got := snapshots[1].ObserverSurface.PrecipitationMM; got != 0 {
		t.Fatalf("packing-ambiguous f001..f002 precipitation interval = %v mm, want 0", got)
	}
	frames, err := forecast.ComputeHorizonSeries(context.Background(), snapshots, plan, forecast.DefaultOverallIndexCalibration())
	if err != nil {
		t.Fatal(err)
	}
	if len(frames) != 72 || len(frames[0].Results) != forecast.HorizonDirectionCount {
		t.Fatalf("computed horizon frames = %d", len(frames))
	}
	calls, maximum := runner.counts()
	wantCalls := 1 + 1 + 1 + 25 + 2*72
	if calls != wantCalls || maximum > 2 {
		t.Fatalf("CDO batch calls/max concurrency = %d/%d, want %d/<=2", calls, maximum, wantCalls)
	}
}

func TestHorizonSeriesMapsMultipleHorizontalCells(t *testing.T) {
	base := time.Date(2026, 7, 22, 6, 0, 0, 0, time.UTC)
	manifest := syntheticHorizonSeriesManifest(base)
	manifest.Grid.Increment = 2
	runner := &horizonSeriesRunner{}
	store := NewHorizonStore("/models", t.TempDir(), 2, nil)
	store.extractor.runner = runner
	store.loadCurrent = func(string) (LoadedManifest, error) { return manifest, nil }
	store.loadRun = func(string, string) (LoadedManifest, error) { return manifest, nil }
	location := forecast.Location{Latitude: 59.9386, Longitude: 30.3141, TimeZone: "Europe/Moscow"}
	plan, err := forecast.NewHorizonPlan(location, 100)
	if err != nil {
		t.Fatal(err)
	}
	snapshots, err := store.Series(context.Background(), manifest.RunID, plan)
	if err != nil {
		t.Fatal(err)
	}
	cells := make(map[int]struct{})
	for _, direction := range snapshots[0].Directions {
		for _, sample := range direction.Samples {
			cells[sample.HorizontalCellID] = struct{}{}
		}
	}
	if len(cells) < 2 {
		t.Fatalf("multi-cell horizon series mapped only %d horizontal cell", len(cells))
	}
}

func syntheticHorizonSeriesManifest(base time.Time) LoadedManifest {
	grid := Coverage()
	// Collapse the physical footprint to one fake grid cell so this test covers
	// all native forecast batches without generating millions of synthetic CDO rows.
	grid.Increment = 1000
	pressureSteps := make([]StepFile, 25)
	for index := range pressureSteps {
		hour := index * 3
		pressureSteps[index] = StepFile{
			ForecastHour: hour, ValidAt: base.Add(time.Duration(hour) * time.Hour),
			File: fmt.Sprintf("pressure-f%03d.grib2", hour), Messages: len(DefaultPressureLevelsHPA) * 4,
		}
	}
	surfaceSteps := make([]SurfaceStepFile, HourlySurfaceStepCount)
	cloudSteps := make([]SurfaceStepFile, HourlySurfaceStepCount)
	for hour := range surfaceSteps {
		validAt := base.Add(time.Duration(hour) * time.Hour)
		surfaceSteps[hour] = SurfaceStepFile{ForecastHour: hour, ValidAt: validAt, File: fmt.Sprintf("surface-f%03d.grib2", hour), Messages: len(surfaceFields)}
		cloudSteps[hour] = SurfaceStepFile{ForecastHour: hour, ValidAt: validAt, File: fmt.Sprintf("cloud-f%03d.grib2", hour), Messages: cloudStepMessageCount()}
	}
	published := base.Add(time.Hour)
	return LoadedManifest{Manifest: Manifest{
		Provider: "icon-eu", Product: ProductName, RunID: base.Format("2006010215"), BaseTime: base,
		Grid: grid, Variables: []string{"u", "v", "z", "t"}, Steps: pressureSteps,
		SurfaceVariables: func() []string {
			values := make([]string, len(surfaceFields))
			for index, field := range surfaceFields {
				values[index] = field.ShortName
			}
			return values
		}(), SurfacePublishedAt: &published, SurfaceSteps: surfaceSteps,
		CloudVariables:   []string{"ccl", "pres", "qc", "qi", "t", "u", "v", "tke", "HHL"},
		CloudModelLevels: append([]int(nil), DefaultCloudModelLevels...), CloudPublishedAt: &published,
		CloudGeometry: &BundleFile{File: "geometry", Messages: len(cloudGeometryLevels())}, CloudSteps: cloudSteps,
	}, Directory: "/models/run"}
}
