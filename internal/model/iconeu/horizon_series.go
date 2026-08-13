package iconeu

import (
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

const horizonForecastHours = 72

type horizonSeriesJob struct {
	kind     string
	index    int
	file     string
	messages int
}

type horizonSeriesExtraction struct {
	job    horizonSeriesJob
	values batchValues
	err    error
}

// Series is the production straight-ray reader. It reads one immutable
// ICON-EU run and returns exact hourly source snapshots f001..f072. No f000
// meteorological file is sampled: the preceding precipitation origin for
// f001 is the exact mathematical zero of the run accumulation.
// Pressure-level U/V/T/Z are linearly interpolated from their native 3-hour
// files before ComputeHorizon recomputes every nonlinear optical quantity.
// Surface, cloud, TKE and mixed-layer state always come from exact hourly
// files. Raw CDO tables are normalized as workers finish and are not retained
// for the whole run.
func (store *HorizonStore) Series(ctx context.Context, runID string, plan forecast.HorizonPlan) ([]forecast.HorizonSnapshot, error) {
	if !store.Supports(plan) {
		return nil, fmt.Errorf("horizon footprint is outside ICON-EU")
	}
	current, err := store.loadCurrent(store.dataRoot)
	if err != nil {
		return nil, err
	}
	if current.RunID != runID {
		return nil, fmt.Errorf("ICON-EU horizon run changed")
	}
	manifest, err := store.loadRun(store.dataRoot, runID)
	if err != nil {
		return nil, err
	}
	if err := validateHorizonSeriesManifest(manifest); err != nil {
		return nil, err
	}
	precipitationPackingErrors, err := horizonPrecipitationPackingErrors(ctx, store.extractor.runner, manifest)
	if err != nil {
		return nil, err
	}
	locations := plan.FootprintLocations()
	for _, location := range locations {
		if !manifest.Grid.Contains(location) {
			return nil, fmt.Errorf("horizon footprint is outside the selected ICON-EU run")
		}
	}
	points, lookup := canonicalHorizonPoints(manifest, locations)
	if len(points) == 0 || len(lookup) == 0 {
		return nil, fmt.Errorf("ICON-EU horizon footprint is empty")
	}

	started := time.Now()
	geometryPath := filepath.Join(manifest.Directory, manifest.CloudGeometry.File)
	remapPlan, err := store.extractor.prepareRemapPlan(ctx, geometryPath, points)
	if err != nil {
		return nil, err
	}
	defer func() { _ = remapPlan.Close() }()
	geometryValues, err := store.extractor.extractWithPlan(
		ctx,
		geometryPath,
		points,
		manifest.CloudGeometry.Messages,
		remapPlan,
	)
	if err != nil {
		return nil, err
	}
	heights := make([]map[int]float64, len(points))
	surfaceElevations := make([]float64, len(points))
	geometryValid := make([]bool, len(points))
	partialValues := 0
	var firstNormalizationError error
	for pointIndex := range points {
		pointHeights, normalizeError := cloudHeightsFromBatch(
			geometryValues[pointIndex], filepath.Base(manifest.CloudGeometry.File),
		)
		if normalizeError == nil {
			var elevation float64
			elevation, normalizeError = CloudSurfaceElevation(pointHeights, iconEUSurfaceHalfLevel, "ICON-EU horizon")
			if normalizeError == nil {
				heights[pointIndex] = pointHeights
				surfaceElevations[pointIndex] = elevation
				geometryValid[pointIndex] = true
			}
		}
		if normalizeError != nil {
			partialValues++
			if firstNormalizationError == nil {
				firstNormalizationError = normalizeError
			}
		}
	}
	if !geometryValid[lookup[0]] {
		return nil, fmt.Errorf("ICON-EU horizon observer geometry is incomplete: %w", firstNormalizationError)
	}

	verticalByPoint := make([][]forecast.VerticalFrame, len(points))
	surfaceByPoint := make([][]forecast.SurfaceFrame, len(points))
	surfaceAccumulatedPrecipByPoint := make([][]float64, len(points))
	cloudByPoint := make([][]forecast.CloudFrame, len(points))
	for pointIndex := range points {
		verticalByPoint[pointIndex] = make([]forecast.VerticalFrame, 0, len(manifest.Steps))
		surfaceByPoint[pointIndex] = make([]forecast.SurfaceFrame, horizonForecastHours+1)
		surfaceAccumulatedPrecipByPoint[pointIndex] = make([]float64, horizonForecastHours+1)
		cloudByPoint[pointIndex] = make([]forecast.CloudFrame, horizonForecastHours+1)
	}

	jobs := make([]horizonSeriesJob, 0, len(manifest.Steps)+2*horizonForecastHours)
	for index, step := range manifest.Steps {
		jobs = append(jobs, horizonSeriesJob{kind: "pressure", index: index, file: step.File, messages: step.Messages})
	}
	for forecastHour := 1; forecastHour <= horizonForecastHours; forecastHour++ {
		surfaceStep := manifest.SurfaceSteps[forecastHour]
		cloudStep := manifest.CloudSteps[forecastHour]
		jobs = append(jobs,
			horizonSeriesJob{kind: "surface", index: forecastHour, file: surfaceStep.File, messages: surfaceStep.Messages},
			horizonSeriesJob{kind: "cloud", index: forecastHour, file: cloudStep.File, messages: cloudStep.Messages},
		)
	}

	workContext, cancel := context.WithCancel(ctx)
	defer cancel()
	jobChannel := make(chan horizonSeriesJob)
	resultChannel := make(chan horizonSeriesExtraction, max(1, store.workers))
	workers := max(1, store.workers)
	var group sync.WaitGroup
	for range workers {
		group.Add(1)
		go func() {
			defer group.Done()
			for job := range jobChannel {
				values, extractionError := store.extractor.extractWithPlan(
					workContext, filepath.Join(manifest.Directory, job.file), points, job.messages, remapPlan,
				)
				select {
				case resultChannel <- horizonSeriesExtraction{job: job, values: values, err: extractionError}:
				case <-workContext.Done():
					return
				}
				if extractionError != nil {
					cancel()
					return
				}
			}
		}()
	}
	go func() {
		defer close(jobChannel)
		for _, job := range jobs {
			select {
			case jobChannel <- job:
			case <-workContext.Done():
				return
			}
		}
	}()
	go func() {
		group.Wait()
		close(resultChannel)
	}()

	completed := 0
	var extractionError error
	for extracted := range resultChannel {
		if extracted.err != nil {
			if extractionError == nil {
				extractionError = extracted.err
			}
			cancel()
			continue
		}
		completed++
		for pointIndex := range points {
			var normalizeError error
			switch extracted.job.kind {
			case "pressure":
				var frame forecast.VerticalFrame
				step := manifest.Steps[extracted.job.index]
				frame, normalizeError = verticalFromBatch(extracted.values[pointIndex], step, filepath.Base(step.File))
				if normalizeError == nil {
					verticalByPoint[pointIndex] = append(verticalByPoint[pointIndex], frame)
				}
			case "surface":
				step := manifest.SurfaceSteps[extracted.job.index]
				var surface ExtractedSurface
				surface, normalizeError = surfaceExtractedFromBatch(
					extracted.values[pointIndex], step.ValidAt, filepath.Base(step.File),
				)
				if normalizeError == nil {
					surfaceByPoint[pointIndex][extracted.job.index] = surface.Frame
					surfaceAccumulatedPrecipByPoint[pointIndex][extracted.job.index] = surface.AccumulatedPrecipMM
				}
			case "cloud":
				step := manifest.CloudSteps[extracted.job.index]
				if !geometryValid[pointIndex] {
					normalizeError = fmt.Errorf("ICON-EU horizon geometry is unavailable")
				} else {
					cloudByPoint[pointIndex][extracted.job.index], normalizeError = cloudFromBatch(
						extracted.values[pointIndex], heights[pointIndex], step.ValidAt, filepath.Base(step.File),
					)
				}
			default:
				normalizeError = fmt.Errorf("unknown ICON-EU horizon extraction kind")
			}
			if normalizeError != nil {
				partialValues++
				if firstNormalizationError == nil {
					firstNormalizationError = normalizeError
				}
			}
		}
		extracted.values = nil
	}
	if extractionError != nil {
		return nil, extractionError
	}
	if completed != len(jobs) {
		return nil, fmt.Errorf("ICON-EU horizon extracted %d of %d batches", completed, len(jobs))
	}
	for pointIndex := range verticalByPoint {
		sort.Slice(verticalByPoint[pointIndex], func(i, j int) bool {
			return verticalByPoint[pointIndex][i].ValidAt.Before(verticalByPoint[pointIndex][j].ValidAt)
		})
	}
	verticalTimelineValid := make([]bool, len(points))
	for pointIndex, frames := range verticalByPoint {
		if len(frames) != len(manifest.Steps) {
			continue
		}
		verticalTimelineValid[pointIndex] = true
		for frameIndex, frame := range frames {
			if !frame.ValidAt.Equal(manifest.BaseTime.Add(time.Duration(frameIndex*3) * time.Hour)) {
				verticalTimelineValid[pointIndex] = false
				break
			}
		}
	}

	snapshots := make([]forecast.HorizonSnapshot, horizonForecastHours)
	for forecastHour := 1; forecastHour <= horizonForecastHours; forecastHour++ {
		validAt := manifest.BaseTime.Add(time.Duration(forecastHour) * time.Hour)
		profiles := make([]forecast.HorizonSampleSnapshot, len(points))
		profileValid := make([]bool, len(points))
		for pointIndex := range points {
			vertical, verticalOK := forecast.VerticalFrame{}, false
			if verticalTimelineValid[pointIndex] {
				vertical, verticalOK = forecast.InterpolateVerticalFrame(
					forecast.VerticalSeries{Frames: verticalByPoint[pointIndex]}, validAt,
				)
			}
			surface := surfaceByPoint[pointIndex][forecastHour]
			previousAccumulation := surfaceAccumulatedPrecipByPoint[pointIndex][forecastHour-1]
			previousPackingError := precipitationPackingErrors[forecastHour-1]
			if forecastHour == 1 {
				// f000 is the exact zero-length origin of the run accumulation.
				// The packed f000 value is not a preceding one-hour observation.
				previousAccumulation = 0
				previousPackingError = 0
			}
			precipitationMM, precipitationErr := domeHourlyPrecipitationFromPackedAccumulations(
				surfaceAccumulatedPrecipByPoint[pointIndex][forecastHour],
				previousAccumulation,
				precipitationPackingErrors[forecastHour],
				previousPackingError,
			)
			if precipitationErr != nil {
				return nil, fmt.Errorf("ICON-EU horizon point %d f%03d precipitation: %w", pointIndex, forecastHour, precipitationErr)
			}
			surface.PrecipitationMM = precipitationMM
			cloud := cloudByPoint[pointIndex][forecastHour]
			if !verticalOK || !surface.ValidAt.Equal(validAt) || !cloud.ValidAt.Equal(validAt) || !geometryValid[pointIndex] {
				continue
			}
			profiles[pointIndex] = forecast.HorizonSampleSnapshot{
				Vertical: vertical, Surface: surface, Cloud: cloud,
				SurfaceElevationM: surfaceElevations[pointIndex], HorizontalCellID: pointIndex,
			}
			profileValid[pointIndex] = true
		}
		if !profileValid[lookup[0]] {
			if firstNormalizationError != nil {
				return nil, fmt.Errorf("ICON-EU horizon observer profile is incomplete at f%03d: %w", forecastHour, firstNormalizationError)
			}
			return nil, fmt.Errorf("ICON-EU horizon observer profile is incomplete at f%03d", forecastHour)
		}
		observer := profiles[lookup[0]]
		snapshot := forecast.HorizonSnapshot{
			ValidAt: validAt, ObserverSurface: observer.Surface,
			ObserverSurfaceElevationM: observer.SurfaceElevationM,
			Directions:                make([]forecast.HorizonDirectionSnapshot, len(plan.Directions)),
		}
		position := 1
		for directionIndex, direction := range plan.Directions {
			directional := forecast.HorizonDirectionSnapshot{
				Direction: direction.Direction,
				Samples:   make([]forecast.HorizonSampleSnapshot, len(direction.Samples)),
			}
			for sampleIndex := range direction.Samples {
				directional.Samples[sampleIndex] = profiles[lookup[position]]
				position++
			}
			snapshot.Directions[directionIndex] = directional
		}
		snapshots[forecastHour-1] = snapshot
	}

	latest, err := store.loadCurrent(store.dataRoot)
	if err != nil {
		return nil, err
	}
	if latest.RunID != runID {
		return nil, fmt.Errorf("ICON-EU horizon run changed during extraction")
	}
	store.logf("horizon series extracted run=%s published_hours=%d points=%d partial_values=%d batches=%d duration=%s",
		runID, len(snapshots), len(points), partialValues, completed, time.Since(started).Round(time.Millisecond))
	return snapshots, nil
}

func horizonPrecipitationPackingErrors(
	ctx context.Context,
	runner CommandRunner,
	manifest LoadedManifest,
) ([]float64, error) {
	if len(manifest.SurfaceSteps) < horizonForecastHours+1 {
		return nil, fmt.Errorf("ICON-EU horizon surface period is incomplete")
	}
	if runner == nil {
		runner = execRunner{}
	}
	args := []string{"-w", "shortName=tp", "-F", "%.17g", "-p", "packingError"}
	for forecastHour := 1; forecastHour <= horizonForecastHours; forecastHour++ {
		args = append(args, filepath.Join(manifest.Directory, manifest.SurfaceSteps[forecastHour].File))
	}
	output, err := runner.CombinedOutput(ctx, "grib_get", args...)
	if err != nil {
		return nil, fmt.Errorf("read ICON-EU Horizon TOT_PREC packing errors: %w: %s", err, strings.TrimSpace(string(output)))
	}
	fields := strings.Fields(string(output))
	if len(fields) != horizonForecastHours {
		return nil, fmt.Errorf("ICON-EU Horizon TOT_PREC has %d packing errors, expected %d", len(fields), horizonForecastHours)
	}
	errorsMM := make([]float64, horizonForecastHours+1)
	for index, field := range fields {
		packingError, parseErr := strconv.ParseFloat(field, 64)
		if parseErr != nil || math.IsNaN(packingError) || math.IsInf(packingError, 0) || packingError < 0 {
			return nil, fmt.Errorf("ICON-EU Horizon f%03d TOT_PREC packingError is invalid", index+1)
		}
		// Expand outwards so text parsing cannot narrow the GRIB error enclosure.
		errorsMM[index+1] = math.Nextafter(packingError, math.Inf(1))
	}
	// The run origin is a mathematical zero, not a packed observation.
	errorsMM[0] = 0
	return errorsMM, nil
}

func validateHorizonSeriesManifest(manifest LoadedManifest) error {
	if !manifest.HasWindThermodynamics() || !manifest.HasHourlySurface() || !manifest.HasHourlyCloud() {
		return fmt.Errorf("ICON-EU horizon fields are incomplete")
	}
	if len(manifest.Steps) != horizonForecastHours/3+1 {
		return fmt.Errorf("ICON-EU horizon pressure period is incomplete")
	}
	for index, step := range manifest.Steps {
		forecastHour := index * 3
		if step.ForecastHour != forecastHour || !step.ValidAt.Equal(manifest.BaseTime.Add(time.Duration(forecastHour)*time.Hour)) {
			return fmt.Errorf("ICON-EU horizon pressure timeline is invalid")
		}
	}
	if len(manifest.SurfaceSteps) <= horizonForecastHours || len(manifest.CloudSteps) <= horizonForecastHours {
		return fmt.Errorf("ICON-EU horizon hourly period is incomplete")
	}
	for forecastHour := 0; forecastHour <= horizonForecastHours; forecastHour++ {
		validAt := manifest.BaseTime.Add(time.Duration(forecastHour) * time.Hour)
		if manifest.SurfaceSteps[forecastHour].ForecastHour != forecastHour ||
			manifest.CloudSteps[forecastHour].ForecastHour != forecastHour ||
			!manifest.SurfaceSteps[forecastHour].ValidAt.Equal(validAt) ||
			!manifest.CloudSteps[forecastHour].ValidAt.Equal(validAt) {
			return fmt.Errorf("ICON-EU horizon hourly timeline is invalid")
		}
	}
	return nil
}
