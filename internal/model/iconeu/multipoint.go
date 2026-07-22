package iconeu

import (
	"bufio"
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const batchCoordinateScale = 1e6

type batchPoint struct {
	Latitude  float64
	Longitude float64
}

type batchValueKey struct {
	name  string
	level float64
}

// batchValues contains one normalized field map for every target point. CDO
// reads the source GRIB once and performs all nearest-neighbour lookups in the
// same process; the work therefore scales with model files rather than with
// files multiplied by points.
type batchValues []map[batchValueKey]float64

type batchExtractor struct {
	runner   CommandRunner
	tempRoot string
}

func (extractor batchExtractor) extract(ctx context.Context, source string, points []batchPoint, expectedMessages int) (batchValues, error) {
	if len(points) == 0 {
		return nil, fmt.Errorf("batch extraction needs at least one point")
	}
	if expectedMessages < 1 {
		return nil, fmt.Errorf("batch extraction needs a positive message count")
	}
	runner := extractor.runner
	if runner == nil {
		runner = execRunner{}
	}
	tempRoot := extractor.tempRoot
	if strings.TrimSpace(tempRoot) == "" {
		tempRoot = os.TempDir()
	}
	if err := os.MkdirAll(tempRoot, 0o750); err != nil {
		return nil, fmt.Errorf("create batch temp root: %w", err)
	}
	temporary, err := os.MkdirTemp(tempRoot, "horizon-batch-")
	if err != nil {
		return nil, fmt.Errorf("create batch temp directory: %w", err)
	}
	defer func() { _ = os.RemoveAll(temporary) }()
	gridPath := filepath.Join(temporary, "points.grid")
	if err := writeBatchGrid(gridPath, points); err != nil {
		return nil, err
	}
	output, err := runner.CombinedOutput(ctx, "cdo",
		"-s", "--precision", "12",
		"-outputtab,name:16,lev:12,lon:16,lat:16,value:24",
		"-remapnn,"+gridPath,
		source,
	)
	if err != nil {
		// outputtab includes target longitudes/latitudes on stdout before a late
		// CDO failure. Never propagate that raw output into application logs.
		return nil, fmt.Errorf("batch remap %s: %w", filepath.Base(source), err)
	}
	values, parseError := parseBatchOutput(output, points, expectedMessages)
	if parseError != nil {
		return nil, fmt.Errorf("parse batch remap %s: %w", filepath.Base(source), parseError)
	}
	return values, nil
}

func writeBatchGrid(path string, points []batchPoint) error {
	seen := make(map[string]struct{}, len(points))
	longitudes := make([]string, len(points))
	latitudes := make([]string, len(points))
	for index, point := range points {
		if !finiteBatchCoordinate(point.Latitude) || !finiteBatchCoordinate(point.Longitude) ||
			point.Latitude < -90 || point.Latitude > 90 || point.Longitude < -180 || point.Longitude >= 180 {
			return fmt.Errorf("batch point %d has invalid coordinates", index)
		}
		key := batchCoordinateKey(point.Latitude, point.Longitude)
		if _, exists := seen[key]; exists {
			return fmt.Errorf("batch point %d duplicates another target", index)
		}
		seen[key] = struct{}{}
		longitudes[index] = strconv.FormatFloat(point.Longitude, 'f', 8, 64)
		latitudes[index] = strconv.FormatFloat(point.Latitude, 'f', 8, 64)
	}
	contents := fmt.Sprintf("gridtype = unstructured\ngridsize = %d\nxvals = %s\nyvals = %s\n",
		len(points), strings.Join(longitudes, " "), strings.Join(latitudes, " "))
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		return fmt.Errorf("write batch target grid: %w", err)
	}
	return nil
}

func parseBatchOutput(output []byte, points []batchPoint, expectedMessages int) (batchValues, error) {
	pointIndex := make(map[string]int, len(points))
	for index, point := range points {
		pointIndex[batchCoordinateKey(point.Latitude, point.Longitude)] = index
	}
	result := make(batchValues, len(points))
	for index := range result {
		result[index] = make(map[batchValueKey]float64, expectedMessages)
	}
	groupCounts := make(map[batchValueKey]int, expectedMessages)
	rows := 0
	scanner := bufio.NewScanner(strings.NewReader(string(output)))
	buffer := make([]byte, 64*1024)
	scanner.Buffer(buffer, 32<<20)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 5 {
			return nil, fmt.Errorf("unexpected output row with %d fields", len(fields))
		}
		level, levelError := strconv.ParseFloat(fields[1], 64)
		longitude, longitudeError := strconv.ParseFloat(fields[2], 64)
		latitude, latitudeError := strconv.ParseFloat(fields[3], 64)
		value, valueError := strconv.ParseFloat(fields[4], 64)
		if levelError != nil || longitudeError != nil || latitudeError != nil || valueError != nil ||
			!finiteBatchCoordinate(level) || !finiteBatchCoordinate(longitude) || !finiteBatchCoordinate(latitude) ||
			!finiteBatchCoordinate(value) || math.Abs(value) > 1e20 {
			return nil, fmt.Errorf("invalid numeric value in output row")
		}
		longitude = normalizeBatchLongitude(longitude)
		index, exists := pointIndex[batchCoordinateKey(latitude, longitude)]
		if !exists {
			return nil, fmt.Errorf("output contains an unknown target coordinate")
		}
		key := batchValueKey{name: fields[0], level: level}
		if _, duplicate := result[index][key]; duplicate {
			return nil, fmt.Errorf("duplicate %s level %.10g for target %d", key.name, key.level, index)
		}
		result[index][key] = value
		groupCounts[key]++
		rows++
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	wantedRows := expectedMessages * len(points)
	if rows != wantedRows {
		return nil, fmt.Errorf("received %d rows, expected %d", rows, wantedRows)
	}
	if len(groupCounts) != expectedMessages {
		return nil, fmt.Errorf("received %d fields, expected %d", len(groupCounts), expectedMessages)
	}
	for key, count := range groupCounts {
		if count != len(points) {
			return nil, fmt.Errorf("%s level %.10g has %d targets, expected %d", key.name, key.level, count, len(points))
		}
	}
	return result, nil
}

func batchCoordinateKey(latitude, longitude float64) string {
	return fmt.Sprintf("%.0f/%.0f", math.Round(latitude*batchCoordinateScale), math.Round(normalizeBatchLongitude(longitude)*batchCoordinateScale))
}

func normalizeBatchLongitude(longitude float64) float64 {
	longitude = math.Mod(longitude+180, 360)
	if longitude < 0 {
		longitude += 360
	}
	return longitude - 180
}

func finiteBatchCoordinate(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}
