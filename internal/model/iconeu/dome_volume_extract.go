package iconeu

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"bot_astrosferum/internal/forecast"
)

type domeExtractedValue struct {
	value     float64
	available bool
}

type domeExtractedBatch []map[batchValueKey]domeExtractedValue

// batchRemapPlan binds one exact native-point target grid to nearest-neighbour
// SCRIP weights generated from the immutable ICON-EU grid. CDO documents the
// gennn/remap split specifically for reusing the expensive grid search across
// multiple files on the same source grid. The plan is private to one preload
// batch and contains no meteorological values.
type batchRemapPlan struct {
	directory   string
	gridPath    string
	weightsPath string
	domeProof   *domeRemapProof
}

func (plan *batchRemapPlan) Close() error {
	if plan == nil || strings.TrimSpace(plan.directory) == "" {
		return nil
	}
	return os.RemoveAll(plan.directory)
}

func (volume *DomeVolume) prepareDomeRemapPlan(
	ctx context.Context,
	geometryPath string,
	points []batchPoint,
) (*batchRemapPlan, error) {
	if volume == nil || strings.TrimSpace(geometryPath) == "" || len(points) == 0 {
		return nil, errors.New("ICON-EU Astrodome remap plan input is incomplete")
	}
	sourceGrid, err := readDomeNativeSourceGrid(ctx, volume.runner, geometryPath)
	if err != nil {
		return nil, fmt.Errorf("read exact native source grid: %w", err)
	}
	if err := volume.validateDomeNativeSourceGrid(sourceGrid); err != nil {
		return nil, fmt.Errorf("validate exact native source grid: %w", err)
	}
	plan, err := prepareBatchRemapPlan(ctx, volume.runner, volume.tempRoot, geometryPath, points)
	if err != nil {
		return nil, fmt.Errorf("prepare ICON-EU Astrodome remap plan: %w", err)
	}
	proof, err := proveDomeRemapPlan(ctx, volume.runner, plan.weightsPath, sourceGrid, points)
	if err != nil {
		_ = plan.Close()
		return nil, fmt.Errorf("prove exact native-node remap plan: %w", err)
	}
	plan.domeProof = &proof
	volume.rememberDomeSourceGrid(geometryPath, sourceGrid)
	return plan, nil
}

func (volume *DomeVolume) validateDomeNativeSourceGrid(source domeNativeSourceGrid) error {
	grid := volume.manifest.Grid
	if !finiteDomeVolume(grid.MinLat) || !finiteDomeVolume(grid.MaxLat) ||
		!finiteDomeVolume(grid.MinLon) || !finiteDomeVolume(grid.MaxLon) ||
		!finiteDomeVolume(grid.Increment) || grid.Increment <= 0 ||
		grid.MaxLat <= grid.MinLat || grid.MaxLon <= grid.MinLon {
		return errors.New("manifest regular-grid contract is invalid")
	}
	expectedNi := int(math.Round((grid.MaxLon-grid.MinLon)/grid.Increment)) + 1
	expectedNj := int(math.Round((grid.MaxLat-grid.MinLat)/grid.Increment)) + 1
	closeCoordinate := func(actual, expected float64) bool {
		return math.Abs(actual-expected) <= 1e-10
	}
	firstLat, firstLon, lastLat, lastLon := source.firstLat, source.firstLon, source.lastLat, source.lastLon
	iIncrement, jIncrement := source.iIncrement, source.jIncrement
	expectedFirstLon, expectedLastLon := grid.MinLon, grid.MaxLon
	if source.iScansNegatively == 1 {
		expectedFirstLon, expectedLastLon = grid.MaxLon, grid.MinLon
	} else if source.iScansNegatively != 0 {
		return errors.New("HHL iScansNegatively is not binary")
	}
	expectedFirstLat, expectedLastLat := grid.MaxLat, grid.MinLat
	if source.jScansPositively == 1 {
		expectedFirstLat, expectedLastLat = grid.MinLat, grid.MaxLat
	} else if source.jScansPositively != 0 {
		return errors.New("HHL jScansPositively is not binary")
	}
	if source.gridType != "regular_ll" || source.ni != expectedNi || source.nj != expectedNj ||
		!closeCoordinate(firstLat, expectedFirstLat) || !closeCoordinate(lastLat, expectedLastLat) ||
		!closeCoordinate(firstLon, expectedFirstLon) || !closeCoordinate(lastLon, expectedLastLon) ||
		!closeCoordinate(iIncrement, grid.Increment) || !closeCoordinate(jIncrement, grid.Increment) {
		return fmt.Errorf(
			"HHL regular grid differs from manifest: Ni/Nj=%d/%d first=(%.12g,%.12g) last=(%.12g,%.12g) increments=(%.12g,%.12g)",
			source.ni, source.nj, firstLat, firstLon, lastLat, lastLon, iIncrement, jIncrement,
		)
	}
	return nil
}

func (volume *DomeVolume) extractDomeColumns(
	ctx context.Context,
	addresses []domeColumnAddress,
) (map[string]forecast.AstrodomePrimitiveColumn, error) {
	if len(addresses) < 1 || len(addresses) > 4 {
		return nil, fmt.Errorf("ICON-EU Astrodome extraction needs one to four exact columns")
	}
	if err := volume.ensureDomeVolumeIdentity(); err != nil {
		return nil, err
	}
	points := make([]batchPoint, len(addresses))
	seen := make(map[string]struct{}, len(addresses))
	for index, address := range addresses {
		if _, duplicate := seen[address.id]; duplicate {
			return nil, fmt.Errorf("ICON-EU Astrodome extraction repeats column %q", address.id)
		}
		seen[address.id] = struct{}{}
		points[index] = batchPoint{Latitude: address.location.Latitude, Longitude: address.location.Longitude}
	}

	geometrySource := domeVolumeSource{
		relative: volume.manifest.Geometry.File,
		bytes:    volume.manifest.Geometry.Bytes,
		messages: volume.manifest.Geometry.Messages,
	}
	geometryPath, err := volume.resolveDomeVolumeSource(geometrySource)
	if err != nil {
		return nil, fmt.Errorf("resolve ICON-EU Astrodome HHL geometry: %w", err)
	}
	geometryValues, err := volume.extractDomeSources(ctx, []string{geometryPath}, points, domeHalfLevelCount)
	if err != nil {
		return nil, fmt.Errorf("extract ICON-EU Astrodome HHL geometry: %w", err)
	}

	columns := make(map[string]forecast.AstrodomePrimitiveColumn, len(addresses))
	for index, address := range addresses {
		geometry, hsurf, parseErr := domeGeometryFromValues(geometryValues[index])
		if parseErr != nil {
			return nil, fmt.Errorf("ICON-EU Astrodome column %q geometry: %w", address.id, parseErr)
		}
		columns[address.id] = forecast.AstrodomePrimitiveColumn{
			ColumnID: address.id, Location: address.location, HSURFHeightM: hsurf,
			HalfLevelGeometry: geometry,
			Frames:            make([]forecast.AstrodomePrimitiveColumnFrame, 0, len(volume.manifest.ModelSteps)),
		}
	}

	for _, step := range volume.manifest.ModelSteps {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		sources := make([]string, 0, len(step.Parts)+1)
		expectedMessages := 0
		for _, part := range step.Parts {
			path, resolveErr := volume.resolveDomeVolumeSource(domeVolumeSource{
				relative: part.File, bytes: part.Bytes, messages: part.Messages,
			})
			if resolveErr != nil {
				return nil, fmt.Errorf("resolve ICON-EU Astrodome model f%03d: %w", step.ForecastHour, resolveErr)
			}
			sources = append(sources, path)
			expectedMessages += part.Messages
		}
		surfaceSource, ok := volume.surfaceFiles[step.ForecastHour]
		if !ok {
			return nil, fmt.Errorf("ICON-EU Astrodome surface f%03d is absent", step.ForecastHour)
		}
		surfacePath, resolveErr := volume.resolveDomeVolumeSource(surfaceSource)
		if resolveErr != nil {
			return nil, fmt.Errorf("resolve ICON-EU Astrodome surface f%03d: %w", step.ForecastHour, resolveErr)
		}
		sources = append(sources, surfacePath)
		expectedMessages += surfaceSource.messages
		if expectedMessages != domeModelMessagesPerStep+SurfaceBundleSchemaVersion {
			return nil, fmt.Errorf("ICON-EU Astrodome f%03d extraction inventory has %d messages", step.ForecastHour, expectedMessages)
		}

		values, extractErr := volume.extractDomeSources(ctx, sources, points, expectedMessages)
		if extractErr != nil {
			return nil, fmt.Errorf("extract ICON-EU Astrodome f%03d: %w", step.ForecastHour, extractErr)
		}
		for index, address := range addresses {
			frame, parseErr := domeFrameFromValues(values[index], step.ValidAt.UTC(), volume.manifest.BaseTime.UTC())
			if parseErr != nil {
				return nil, fmt.Errorf("ICON-EU Astrodome column %q f%03d: %w", address.id, step.ForecastHour, parseErr)
			}
			column := columns[address.id]
			column.Frames = append(column.Frames, frame)
			columns[address.id] = column
		}
	}
	if err := volume.ensureDomeVolumeIdentity(); err != nil {
		return nil, err
	}
	return columns, nil
}

func (volume *DomeVolume) ensureDomeVolumeIdentity() error {
	digest, _, err := fileDigest(filepath.Join(volume.manifest.Directory, "manifest.json"))
	if err != nil {
		return fmt.Errorf("rehash ICON-EU Astrodome manifest: %w", err)
	}
	if digest != volume.manifest.ManifestSHA256 {
		return errors.New("ICON-EU Astrodome manifest changed during extraction")
	}
	baseDigest, _, err := fileDigest(filepath.Join(volume.base.Directory, "manifest.json"))
	if err != nil {
		return fmt.Errorf("rehash ICON-EU Astrodome base manifest: %w", err)
	}
	if baseDigest != volume.manifest.BaseManifestSHA256 {
		return errors.New("ICON-EU Astrodome base manifest changed during extraction")
	}
	return nil
}

func (volume *DomeVolume) resolveDomeVolumeSource(source domeVolumeSource) (string, error) {
	if unsafeRelativePath(source.relative) || source.bytes <= 0 || source.messages <= 0 {
		return "", errors.New("invalid immutable GRIB source identity")
	}
	path := filepath.Join(volume.providerRoot, source.relative)
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	relative, err := filepath.Rel(volume.providerRoot, absolute)
	if err != nil || filepath.IsAbs(relative) || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", errors.New("immutable GRIB source escapes the ICON-EU provider root")
	}
	info, err := os.Lstat(absolute)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Size() != source.bytes {
		return "", fmt.Errorf("immutable GRIB source %s changed size or type", filepath.Base(absolute))
	}
	return absolute, nil
}

func (volume *DomeVolume) extractDomeSources(
	ctx context.Context,
	sources []string,
	points []batchPoint,
	expectedMessages int,
) (domeExtractedBatch, error) {
	if len(sources) == 0 || len(points) == 0 || expectedMessages < 1 {
		return nil, errors.New("ICON-EU Astrodome CDO extraction has an empty input")
	}
	tempRoot := volume.tempRoot
	if strings.TrimSpace(tempRoot) == "" {
		tempRoot = os.TempDir()
	}
	if err := os.MkdirAll(tempRoot, 0o750); err != nil {
		return nil, fmt.Errorf("create ICON-EU Astrodome temp root: %w", err)
	}
	temporary, err := os.MkdirTemp(tempRoot, "astrodome-columns-")
	if err != nil {
		return nil, fmt.Errorf("create ICON-EU Astrodome extraction directory: %w", err)
	}
	defer func() { _ = os.RemoveAll(temporary) }()
	gridPath := filepath.Join(temporary, "points.grid")
	if err := writeBatchGrid(gridPath, points); err != nil {
		return nil, err
	}
	args := []string{
		"-s", "--precision", "12",
		"-outputtab,name:16,lev:12,lon:16,lat:16,value:24",
		"-remapnn," + gridPath,
	}
	if len(sources) > 1 {
		// CDO requires an explicit input subgroup when a variable-input
		// operator consumes more than one file. Without the brackets, the
		// second source is parsed as another top-level input operator.
		args = append(args, "-merge", "[")
		args = append(args, sources...)
		args = append(args, "]")
	} else {
		args = append(args, sources[0])
	}
	output, err := volume.runner.CombinedOutput(ctx, "cdo", args...)
	if err != nil {
		return nil, fmt.Errorf("CDO native-column extraction failed: %w", err)
	}
	return parseDomeBatchOutput(output, points, expectedMessages)
}

func (volume *DomeVolume) extractDomeSourcesWithRemapPlan(
	ctx context.Context,
	sources []string,
	points []batchPoint,
	expectedMessages int,
	plan *batchRemapPlan,
) (domeExtractedBatch, error) {
	if len(sources) == 0 || len(points) == 0 || expectedMessages < 1 || plan == nil ||
		strings.TrimSpace(plan.gridPath) == "" || strings.TrimSpace(plan.weightsPath) == "" || plan.domeProof == nil {
		return nil, errors.New("ICON-EU Astrodome planned CDO extraction has an empty input")
	}
	if err := plan.domeProof.validateTargets(points); err != nil {
		return nil, err
	}
	for _, source := range sources {
		if err := volume.validateDomeRemapSource(ctx, source, plan.domeProof.sourceGrid); err != nil {
			return nil, fmt.Errorf("validate reusable remap source %s: %w", filepath.Base(source), err)
		}
	}
	args := []string{
		"-s", "--precision", "12",
		"-outputtab,name:16,lev:12,lon:16,lat:16,value:24",
		"-remap," + plan.gridPath + "," + plan.weightsPath,
	}
	if len(sources) > 1 {
		args = append(args, "-merge", "[")
		args = append(args, sources...)
		args = append(args, "]")
	} else {
		args = append(args, sources[0])
	}
	output, err := volume.runner.CombinedOutput(ctx, "cdo", args...)
	if err != nil {
		return nil, fmt.Errorf("CDO native-column extraction with reusable weights failed: %w", err)
	}
	return parseDomeBatchOutput(output, points, expectedMessages)
}

func parseDomeBatchOutput(output []byte, points []batchPoint, expectedMessages int) (domeExtractedBatch, error) {
	pointIndex := make(map[batchCoordinateIdentity]int, len(points))
	for index, point := range points {
		pointIndex[batchCoordinateKey(point.Latitude, point.Longitude)] = index
	}
	result := make(domeExtractedBatch, len(points))
	for index := range result {
		result[index] = make(map[batchValueKey]domeExtractedValue, expectedMessages)
	}
	groupCounts := make(map[batchValueKey]int, expectedMessages)
	rows := 0
	scanner := bufio.NewScanner(strings.NewReader(string(output)))
	scanner.Buffer(make([]byte, 64*1024), 32<<20)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 5 {
			return nil, fmt.Errorf("unexpected CDO output row with %d fields", len(fields))
		}
		level, levelErr := strconv.ParseFloat(fields[1], 64)
		longitude, lonErr := strconv.ParseFloat(fields[2], 64)
		latitude, latErr := strconv.ParseFloat(fields[3], 64)
		value, valueErr := strconv.ParseFloat(fields[4], 64)
		if levelErr != nil || lonErr != nil || latErr != nil || valueErr != nil ||
			!finiteDomeVolume(level) || !finiteDomeVolume(longitude) || !finiteDomeVolume(latitude) || math.IsInf(value, 0) {
			return nil, errors.New("invalid numeric value in CDO output")
		}
		longitude = normalizeBatchLongitude(longitude)
		index, ok := pointIndex[batchCoordinateKey(latitude, longitude)]
		if !ok {
			return nil, errors.New("CDO output contains an unknown target coordinate")
		}
		key := batchValueKey{name: fields[0], level: level}
		if _, duplicate := result[index][key]; duplicate {
			return nil, fmt.Errorf("duplicate %s level %.10g for target %d", key.name, key.level, index)
		}
		available := !math.IsNaN(value) && math.Abs(value) <= 1e20
		result[index][key] = domeExtractedValue{value: value, available: available}
		groupCounts[key]++
		rows++
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	wantedRows := expectedMessages * len(points)
	if rows != wantedRows || len(groupCounts) != expectedMessages {
		return nil, fmt.Errorf("CDO returned %d rows/%d fields, expected %d rows/%d fields",
			rows, len(groupCounts), wantedRows, expectedMessages)
	}
	for key, count := range groupCounts {
		if count != len(points) {
			return nil, fmt.Errorf("%s level %.10g has %d targets, expected %d", key.name, key.level, count, len(points))
		}
	}
	return result, nil
}

func domeGeometryFromValues(values map[batchValueKey]domeExtractedValue) ([]forecast.AstrodomeHalfLevelGeometry, float64, error) {
	normalized := make(map[int]domeExtractedValue, len(values))
	for key, value := range values {
		if canonicalDomeShortName(key.name) != "HHL" || math.Trunc(key.level) != key.level {
			return nil, 0, fmt.Errorf("unexpected geometry field %q level %.10g", key.name, key.level)
		}
		level := int(key.level)
		if _, duplicate := normalized[level]; duplicate {
			return nil, 0, fmt.Errorf("duplicate HHL level %d", level)
		}
		normalized[level] = value
	}
	geometry := make([]forecast.AstrodomeHalfLevelGeometry, domeHalfLevelCount)
	for level := 1; level <= domeHalfLevelCount; level++ {
		value, ok := normalized[level]
		if !ok || !value.available || !finiteDomeVolume(value.value) {
			return nil, 0, fmt.Errorf("HHL level %d is unavailable", level)
		}
		geometry[level-1] = forecast.AstrodomeHalfLevelGeometry{ModelHalfLevel: level, HeightM: value.value}
		if level > 1 && value.value >= geometry[level-2].HeightM {
			return nil, 0, errors.New("HHL geometry is not strictly top-to-surface ordered")
		}
	}
	if len(normalized) != domeHalfLevelCount {
		return nil, 0, fmt.Errorf("HHL geometry has %d levels, expected %d", len(normalized), domeHalfLevelCount)
	}
	return geometry, geometry[len(geometry)-1].HeightM, nil
}

func domeFrameFromValues(
	values map[batchValueKey]domeExtractedValue,
	validAt time.Time,
	baseTime time.Time,
) (forecast.AstrodomePrimitiveColumnFrame, error) {
	modelValues := make(map[batchValueKey]domeExtractedValue, domeModelMessagesPerStep)
	surfaceValues := make(map[string]domeExtractedValue, SurfaceBundleSchemaVersion)
	surfaceNames := make(map[string]struct{}, len(surfaceFields))
	for _, field := range surfaceFields {
		surfaceNames[field.ShortName] = struct{}{}
	}
	for key, value := range values {
		name := canonicalDomeShortName(key.name)
		if _, surface := surfaceNames[name]; surface {
			if _, duplicate := surfaceValues[name]; duplicate {
				return forecast.AstrodomePrimitiveColumnFrame{}, fmt.Errorf("duplicate surface field %q", name)
			}
			surfaceValues[name] = value
			continue
		}
		if math.Trunc(key.level) != key.level {
			return forecast.AstrodomePrimitiveColumnFrame{}, fmt.Errorf("model field %q has non-integer level %.10g", name, key.level)
		}
		modelKey := batchValueKey{name: name, level: key.level}
		if _, duplicate := modelValues[modelKey]; duplicate {
			return forecast.AstrodomePrimitiveColumnFrame{}, fmt.Errorf("duplicate model field %q level %.10g", name, key.level)
		}
		modelValues[modelKey] = value
	}
	if len(modelValues) != domeModelMessagesPerStep || len(surfaceValues) != SurfaceBundleSchemaVersion {
		return forecast.AstrodomePrimitiveColumnFrame{}, fmt.Errorf("native frame has %d model/%d surface fields, expected %d/%d",
			len(modelValues), len(surfaceValues), domeModelMessagesPerStep, SurfaceBundleSchemaVersion)
	}

	frame := forecast.AstrodomePrimitiveColumnFrame{
		ValidAt:    validAt.UTC(),
		FullLevels: make([]forecast.AstrodomeFullLevelPrimitives, domeFullLevelCount),
		HalfLevels: make([]forecast.AstrodomeHalfLevelPrimitives, domeHalfLevelCount),
	}
	for level := 1; level <= domeFullLevelCount; level++ {
		pressure, err := requiredDomeModelValue(modelValues, "pres", level)
		if err != nil || pressure <= 0 {
			return frame, invalidDomePrimitive("pressure", level, err)
		}
		temperature, err := requiredDomeModelValue(modelValues, "t", level)
		if err != nil || temperature <= 0 {
			return frame, invalidDomePrimitive("temperature", level, err)
		}
		humidity, err := requiredDomeModelValue(modelValues, "q", level)
		if err != nil || humidity < 0 || humidity >= 1 {
			return frame, invalidDomePrimitive("specific humidity", level, err)
		}
		liquid, err := requiredDomeModelValue(modelValues, "qc", level)
		if err != nil || liquid < 0 {
			return frame, invalidDomePrimitive("cloud liquid", level, err)
		}
		ice, err := requiredDomeModelValue(modelValues, "qi", level)
		if err != nil || ice < 0 {
			return frame, invalidDomePrimitive("cloud ice", level, err)
		}
		coverPercent, err := requiredDomeModelValue(modelValues, "ccl", level)
		if err != nil || coverPercent < 0 || coverPercent > 100 {
			return frame, invalidDomePrimitive("cloud fraction", level, err)
		}
		u, err := requiredDomeModelValue(modelValues, "u", level)
		if err != nil {
			return frame, invalidDomePrimitive("eastward wind", level, err)
		}
		v, err := requiredDomeModelValue(modelValues, "v", level)
		if err != nil {
			return frame, invalidDomePrimitive("northward wind", level, err)
		}
		frame.FullLevels[level-1] = forecast.AstrodomeFullLevelPrimitives{
			ModelLevel: level, Available: domeFullAvailable,
			PressurePa: pressure, TemperatureK: temperature, SpecificHumidityKgKg: humidity,
			CloudLiquidKgKg: liquid, CloudIceKgKg: ice, CloudFraction: coverPercent / 100,
			EastwardWindMS: u, NorthwardWindMS: v,
		}
	}
	if err := forecast.ValidateAstrodomeNativePressureOrdering(frame.FullLevels); err != nil {
		return frame, fmt.Errorf("native pressure profile: %w", err)
	}
	for level := 1; level <= domeHalfLevelCount; level++ {
		vertical, err := requiredDomeModelValue(modelValues, "wz", level)
		if err != nil {
			return frame, invalidDomePrimitive("vertical wind", level, err)
		}
		tke, err := requiredDomeModelValue(modelValues, "tke", level)
		if err != nil || tke < 0 {
			return frame, invalidDomePrimitive("TKE", level, err)
		}
		frame.HalfLevels[level-1] = forecast.AstrodomeHalfLevelPrimitives{
			ModelHalfLevel: level, Available: domeHalfAvailable, VerticalWindMS: vertical, TKEJkg: tke,
		}
	}
	frame.Surface = domeSurfaceFromValues(surfaceValues, validAt.UTC(), baseTime.UTC())
	if err := forecast.ValidateAstrodomeNativeSurfacePressureBoundary(frame.FullLevels, frame.Surface); err != nil {
		return frame, fmt.Errorf("native pressure boundary: %w", err)
	}
	return frame, nil
}

func requiredDomeModelValue(values map[batchValueKey]domeExtractedValue, name string, level int) (float64, error) {
	value, ok := values[batchValueKey{name: name, level: float64(level)}]
	if !ok || !value.available || !finiteDomeVolume(value.value) {
		return 0, errors.New("native value is unavailable")
	}
	return value.value, nil
}

func invalidDomePrimitive(name string, level int, cause error) error {
	if cause != nil {
		return fmt.Errorf("%s at model level %d: %w", name, level, cause)
	}
	return fmt.Errorf("%s at model level %d is outside its physical domain", name, level)
}

func domeSurfaceFromValues(values map[string]domeExtractedValue, validAt, baseTime time.Time) forecast.AstrodomeSurfacePrimitives {
	result := forecast.AstrodomeSurfacePrimitives{
		PrecipitationIntervalStart: baseTime,
		PrecipitationIntervalEnd:   validAt,
	}
	assign := func(name string, field forecast.AstrodomePrimitiveField, valid func(float64) bool, destination *float64) {
		value, ok := values[name]
		if !ok || !value.available || !finiteDomeVolume(value.value) || !valid(value.value) {
			return
		}
		*destination = value.value
		result.Available |= forecast.AstrodomePrimitiveFieldSet(field)
	}
	any := func(float64) bool { return true }
	nonnegative := func(value float64) bool { return value >= 0 }
	positive := func(value float64) bool { return value > 0 }
	percent := func(value float64) bool { return value >= 0 && value <= 100 }
	assign("mld", forecast.AstrodomePrimitiveMixedLayerDepth, nonnegative, &result.MixedLayerDepthM)
	assign("vis", forecast.AstrodomePrimitiveVisibility, nonnegative, &result.VisibilityM)
	// PS and QV_2M are the native lower-bound thermodynamic primitives. PS is
	// anchored at HSURF while QV_2M shares the HSURF+2 m anchor with T_2M.
	// PMSL and dew point are deliberately not used as proxies.
	assign("sp", forecast.AstrodomePrimitiveSurfacePressure, positive, &result.SurfacePressurePa)
	assign("2sh", forecast.AstrodomePrimitiveSpecificHumidity2M,
		func(value float64) bool { return value >= 0 && value < 1 }, &result.SpecificHumidity2MKgKg)
	assign("2r", forecast.AstrodomePrimitiveRelativeHumidity, percent, &result.RelativeHumidityFraction)
	if result.Available.Has(forecast.AstrodomePrimitiveRelativeHumidity) {
		result.RelativeHumidityFraction /= 100
	}
	// T_2M is retained at its native HSURF+2 m anchor; it is not silently
	// relocated to the HHL surface.
	assign("2t", forecast.AstrodomePrimitiveTemperature2M, positive, &result.Temperature2MK)
	assign("2d", forecast.AstrodomePrimitiveDewPoint2M, positive, &result.DewPoint2MK)
	assign("10u", forecast.AstrodomePrimitiveEastwardWind10M, any, &result.EastwardWind10MMS)
	assign("10v", forecast.AstrodomePrimitiveNorthwardWind10M, any, &result.NorthwardWind10MMS)
	assign("VMAX_10M", forecast.AstrodomePrimitiveWindGust10M, nonnegative, &result.WindGust10MMS)
	if validAt.After(baseTime) {
		assign("tp", forecast.AstrodomePrimitivePrecipitationAccumulation, nonnegative, &result.PrecipitationAccumulationMM)
	}
	assign("TQV", forecast.AstrodomePrimitiveTotalColumnWaterVapour, nonnegative, &result.TotalColumnWaterVapourKgM2)
	assign("TQC", forecast.AstrodomePrimitiveTotalColumnCloudLiquid, nonnegative, &result.TotalColumnCloudLiquidKgM2)
	assign("TQI", forecast.AstrodomePrimitiveTotalColumnCloudIce, nonnegative, &result.TotalColumnCloudIceKgM2)
	return result
}
