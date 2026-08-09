package iconeu

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"strconv"
	"strings"
)

const domeNativeGridMetadataKeys = "gridType,Ni,Nj,latitudeOfFirstGridPointInDegrees,longitudeOfFirstGridPointInDegrees,latitudeOfLastGridPointInDegrees,longitudeOfLastGridPointInDegrees,iDirectionIncrementInDegrees,jDirectionIncrementInDegrees,iScansNegatively,jScansPositively"

type domeNativeSourceGrid struct {
	gridType                           string
	ni, nj                             int
	firstLat, firstLon                 float64
	lastLat, lastLon                   float64
	iIncrement, jIncrement             float64
	iScansNegatively, jScansPositively int
}

type domeRemapProof struct {
	sourceGrid domeNativeSourceGrid
	targets    []batchCoordinateIdentity
}

func readDomeNativeSourceGrid(
	ctx context.Context,
	runner CommandRunner,
	sourcePath string,
) (domeNativeSourceGrid, error) {
	if runner == nil || strings.TrimSpace(sourcePath) == "" {
		return domeNativeSourceGrid{}, errors.New("native source-grid inspection input is incomplete")
	}
	output, err := runner.CombinedOutput(ctx, "grib_get", "-p", domeNativeGridMetadataKeys, sourcePath)
	if err != nil {
		return domeNativeSourceGrid{}, fmt.Errorf("read GRIB source-grid metadata: %w", err)
	}
	scanner := bufio.NewScanner(strings.NewReader(string(output)))
	scanner.Buffer(make([]byte, 64*1024), 16<<20)
	var expected domeNativeSourceGrid
	records := 0
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		grid, parseErr := parseDomeNativeSourceGridRecord(line)
		if parseErr != nil {
			return domeNativeSourceGrid{}, fmt.Errorf("source-grid record %d: %w", records+1, parseErr)
		}
		if records == 0 {
			expected = grid
		} else if !sameDomeNativeSourceGrid(expected, grid) {
			return domeNativeSourceGrid{}, fmt.Errorf("GRIB source contains more than one grid or scanning order")
		}
		records++
	}
	if err := scanner.Err(); err != nil {
		return domeNativeSourceGrid{}, err
	}
	if records == 0 {
		return domeNativeSourceGrid{}, errors.New("GRIB source contains no grid metadata")
	}
	return expected, nil
}

func parseDomeNativeSourceGridRecord(line string) (domeNativeSourceGrid, error) {
	fields := strings.Fields(line)
	if len(fields) != 11 || fields[0] != "regular_ll" {
		return domeNativeSourceGrid{}, errors.New("metadata is not one regular_ll record")
	}
	parseInteger := func(value string) (int, error) {
		parsed, err := strconv.Atoi(value)
		if err != nil {
			return 0, fmt.Errorf("parse integer %q: %w", value, err)
		}
		return parsed, nil
	}
	parseNumber := func(value string) (float64, error) {
		parsed, err := strconv.ParseFloat(value, 64)
		if err != nil || !finiteDomeVolume(parsed) {
			return 0, fmt.Errorf("parse finite number %q", value)
		}
		return parsed, nil
	}
	grid := domeNativeSourceGrid{gridType: fields[0]}
	var err error
	if grid.ni, err = parseInteger(fields[1]); err != nil {
		return domeNativeSourceGrid{}, err
	}
	if grid.nj, err = parseInteger(fields[2]); err != nil {
		return domeNativeSourceGrid{}, err
	}
	numbers := []*float64{
		&grid.firstLat, &grid.firstLon, &grid.lastLat, &grid.lastLon,
		&grid.iIncrement, &grid.jIncrement,
	}
	for index, destination := range numbers {
		*destination, err = parseNumber(fields[index+3])
		if err != nil {
			return domeNativeSourceGrid{}, err
		}
	}
	grid.firstLon = normalizeBatchLongitude(grid.firstLon)
	grid.lastLon = normalizeBatchLongitude(grid.lastLon)
	if grid.iScansNegatively, err = parseInteger(fields[9]); err != nil {
		return domeNativeSourceGrid{}, err
	}
	if grid.jScansPositively, err = parseInteger(fields[10]); err != nil {
		return domeNativeSourceGrid{}, err
	}
	if grid.ni < 2 || grid.nj < 2 || grid.iIncrement <= 0 || grid.jIncrement <= 0 ||
		(grid.iScansNegatively != 0 && grid.iScansNegatively != 1) ||
		(grid.jScansPositively != 0 && grid.jScansPositively != 1) {
		return domeNativeSourceGrid{}, errors.New("regular grid dimensions, increments, or scanning flags are invalid")
	}
	return grid, nil
}

func sameDomeNativeSourceGrid(left, right domeNativeSourceGrid) bool {
	return left.gridType == right.gridType && left.ni == right.ni && left.nj == right.nj &&
		left.firstLat == right.firstLat && left.firstLon == right.firstLon &&
		left.lastLat == right.lastLat && left.lastLon == right.lastLon &&
		left.iIncrement == right.iIncrement && left.jIncrement == right.jIncrement &&
		left.iScansNegatively == right.iScansNegatively &&
		left.jScansPositively == right.jScansPositively
}

func (volume *DomeVolume) rememberDomeSourceGrid(path string, grid domeNativeSourceGrid) {
	volume.sourceGridMu.Lock()
	defer volume.sourceGridMu.Unlock()
	if volume.sourceGrids == nil {
		volume.sourceGrids = make(map[string]domeNativeSourceGrid)
	}
	volume.sourceGrids[filepath.Clean(path)] = grid
}

func (volume *DomeVolume) validateDomeRemapSource(
	ctx context.Context,
	path string,
	want domeNativeSourceGrid,
) error {
	key := filepath.Clean(path)
	volume.sourceGridMu.Lock()
	cached, ok := volume.sourceGrids[key]
	volume.sourceGridMu.Unlock()
	if ok {
		if !sameDomeNativeSourceGrid(cached, want) {
			return errors.New("cached source-grid proof differs from the remap plan")
		}
		return nil
	}
	actual, err := readDomeNativeSourceGrid(ctx, volume.runner, path)
	if err != nil {
		return err
	}
	if !sameDomeNativeSourceGrid(actual, want) {
		return errors.New("source grid or scanning order differs from the proven HHL remap grid")
	}
	volume.rememberDomeSourceGrid(key, actual)
	return nil
}

func proveDomeRemapPlan(
	ctx context.Context,
	runner CommandRunner,
	weightsPath string,
	sourceGrid domeNativeSourceGrid,
	points []batchPoint,
) (domeRemapProof, error) {
	if runner == nil || strings.TrimSpace(weightsPath) == "" || len(points) == 0 {
		return domeRemapProof{}, errors.New("remap proof input is incomplete")
	}
	output, err := runner.CombinedOutput(
		ctx,
		"ncdump",
		"-v", "src_grid_dims,dst_grid_dims,src_address,dst_address,remap_matrix",
		weightsPath,
	)
	if err != nil {
		return domeRemapProof{}, fmt.Errorf("inspect SCRIP weights: %w", err)
	}
	dimensions, err := parseDomeNCDumpDimensions(output)
	if err != nil {
		return domeRemapProof{}, err
	}
	wantSourceSize := sourceGrid.ni * sourceGrid.nj
	if dimensions["src_grid_size"] != wantSourceSize || dimensions["dst_grid_size"] != len(points) ||
		dimensions["src_grid_rank"] != 2 || dimensions["dst_grid_rank"] != 1 ||
		dimensions["num_links"] != len(points) || dimensions["num_wgts"] != 1 {
		return domeRemapProof{}, fmt.Errorf("SCRIP dimensions do not describe one unit link per native target")
	}
	sourceDims, err := parseDomeNCDumpIntegers(output, "src_grid_dims")
	if err != nil || len(sourceDims) != 2 || sourceDims[0] != sourceGrid.ni || sourceDims[1] != sourceGrid.nj {
		return domeRemapProof{}, errors.New("SCRIP source dimensions differ from the proven native grid")
	}
	destinationDims, err := parseDomeNCDumpIntegers(output, "dst_grid_dims")
	if err != nil || len(destinationDims) != 1 || destinationDims[0] != len(points) {
		return domeRemapProof{}, errors.New("SCRIP destination dimensions differ from the target list")
	}
	sourceAddresses, err := parseDomeNCDumpIntegers(output, "src_address")
	if err != nil {
		return domeRemapProof{}, err
	}
	destinationAddresses, err := parseDomeNCDumpIntegers(output, "dst_address")
	if err != nil {
		return domeRemapProof{}, err
	}
	weights, err := parseDomeNCDumpFloats(output, "remap_matrix")
	if err != nil {
		return domeRemapProof{}, err
	}
	if len(sourceAddresses) != len(points) || len(destinationAddresses) != len(points) || len(weights) != len(points) {
		return domeRemapProof{}, errors.New("SCRIP link arrays do not match the target list")
	}
	proof := domeRemapProof{sourceGrid: sourceGrid, targets: make([]batchCoordinateIdentity, len(points))}
	for index, point := range points {
		expectedAddress, addressErr := domeCanonicalSourceAddress(sourceGrid, point)
		if addressErr != nil {
			return domeRemapProof{}, fmt.Errorf("target %d: %w", index, addressErr)
		}
		if sourceAddresses[index] != expectedAddress || destinationAddresses[index] != index+1 ||
			math.Float64bits(weights[index]) != math.Float64bits(1) {
			return domeRemapProof{}, fmt.Errorf(
				"target %d has SCRIP source/destination/weight %d/%d/%.17g, want %d/%d/1",
				index, sourceAddresses[index], destinationAddresses[index], weights[index], expectedAddress, index+1,
			)
		}
		proof.targets[index] = batchCoordinateKey(point.Latitude, point.Longitude)
	}
	return proof, nil
}

func (proof domeRemapProof) validateTargets(points []batchPoint) error {
	if len(points) != len(proof.targets) {
		return errors.New("remap plan target cardinality changed")
	}
	for index, point := range points {
		if batchCoordinateKey(point.Latitude, point.Longitude) != proof.targets[index] {
			return fmt.Errorf("remap plan target %d changed", index)
		}
	}
	return nil
}

func domeCanonicalSourceAddress(grid domeNativeSourceGrid, point batchPoint) (int, error) {
	minimumLatitude := math.Min(grid.firstLat, grid.lastLat)
	maximumLatitude := math.Max(grid.firstLat, grid.lastLat)
	minimumLongitude := math.Min(grid.firstLon, grid.lastLon)
	maximumLongitude := math.Max(grid.firstLon, grid.lastLon)
	longitude := normalizeBatchLongitude(point.Longitude)
	latitudeIndex := int(math.Round((point.Latitude - minimumLatitude) / grid.jIncrement))
	longitudeIndex := int(math.Round((longitude - minimumLongitude) / grid.iIncrement))
	if latitudeIndex < 0 || latitudeIndex >= grid.nj || longitudeIndex < 0 || longitudeIndex >= grid.ni ||
		point.Latitude < minimumLatitude-1e-10 || point.Latitude > maximumLatitude+1e-10 ||
		longitude < minimumLongitude-1e-10 || longitude > maximumLongitude+1e-10 {
		return 0, errors.New("target is outside the proven native grid")
	}
	expectedLatitude := minimumLatitude + float64(latitudeIndex)*grid.jIncrement
	expectedLongitude := minimumLongitude + float64(longitudeIndex)*grid.iIncrement
	if math.Abs(point.Latitude-expectedLatitude) > 1e-10 || math.Abs(longitude-expectedLongitude) > 1e-10 {
		return 0, errors.New("target is not an exact native-grid coordinate")
	}
	// CDO 2.5.x SCRIP addresses for regular_ll are one-based and canonicalized
	// west-to-east within south-to-north rows. The real-CDO manufactured-grid
	// regression pins this convention independently of GRIB scanning order.
	return latitudeIndex*grid.ni + longitudeIndex + 1, nil
}

func parseDomeNCDumpDimensions(output []byte) (map[string]int, error) {
	wanted := map[string]struct{}{
		"src_grid_size": {}, "dst_grid_size": {}, "src_grid_rank": {},
		"dst_grid_rank": {}, "num_links": {}, "num_wgts": {},
	}
	result := make(map[string]int, len(wanted))
	scanner := bufio.NewScanner(strings.NewReader(string(output)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "data:" {
			break
		}
		parts := strings.SplitN(strings.TrimSuffix(line, ";"), "=", 2)
		if len(parts) != 2 {
			continue
		}
		name := strings.TrimSpace(parts[0])
		if _, ok := wanted[name]; !ok {
			continue
		}
		value, err := strconv.Atoi(strings.TrimSpace(parts[1]))
		if err != nil || value <= 0 {
			return nil, fmt.Errorf("invalid ncdump dimension %s", name)
		}
		result[name] = value
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if len(result) != len(wanted) {
		return nil, errors.New("ncdump omitted required SCRIP dimensions")
	}
	return result, nil
}

func parseDomeNCDumpIntegers(output []byte, name string) ([]int, error) {
	tokens, err := parseDomeNCDumpArray(output, name)
	if err != nil {
		return nil, err
	}
	values := make([]int, len(tokens))
	for index, token := range tokens {
		values[index], err = strconv.Atoi(token)
		if err != nil {
			return nil, fmt.Errorf("ncdump %s contains invalid integer %q", name, token)
		}
	}
	return values, nil
}

func parseDomeNCDumpFloats(output []byte, name string) ([]float64, error) {
	tokens, err := parseDomeNCDumpArray(output, name)
	if err != nil {
		return nil, err
	}
	values := make([]float64, len(tokens))
	for index, token := range tokens {
		values[index], err = strconv.ParseFloat(token, 64)
		if err != nil || !finiteDomeVolume(values[index]) {
			return nil, fmt.Errorf("ncdump %s contains invalid number %q", name, token)
		}
	}
	return values, nil
}

func parseDomeNCDumpArray(output []byte, name string) ([]string, error) {
	scanner := bufio.NewScanner(strings.NewReader(string(output)))
	collecting := false
	var builder strings.Builder
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !collecting {
			prefix := name + " ="
			if !strings.HasPrefix(line, prefix) {
				continue
			}
			collecting = true
			line = strings.TrimSpace(strings.TrimPrefix(line, prefix))
		}
		if semicolon := strings.IndexByte(line, ';'); semicolon >= 0 {
			builder.WriteString(line[:semicolon])
			break
		}
		builder.WriteString(line)
		builder.WriteByte(' ')
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if !collecting {
		return nil, fmt.Errorf("ncdump omitted %s", name)
	}
	tokens := strings.FieldsFunc(builder.String(), func(r rune) bool {
		return r == ',' || r == ' ' || r == '\t' || r == '\n' || r == '\r'
	})
	if len(tokens) == 0 {
		return nil, fmt.Errorf("ncdump %s is empty", name)
	}
	return tokens, nil
}
