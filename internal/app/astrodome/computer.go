package astrodome

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"bot_astrosferum/internal/app/directional"
	"bot_astrosferum/internal/astronomy"
	"bot_astrosferum/internal/forecast"
	"bot_astrosferum/internal/model"
	"bot_astrosferum/internal/model/iconeu"
)

type ComputerConfig struct {
	DataRoot           string
	TempRoot           string
	ECCodesWorkers     int
	NodeWorkers        int
	ResidentLimitBytes uint64
	ScienceCalibration forecast.AstrodomeScienceCalibration
	Terrain            TerrainSkylineSource
	Now                func() time.Time
	Logf               func(string, ...any)
}

type Computer struct {
	dataRoot           string
	tempRoot           string
	ecCodesWorkers     int
	nodeWorkers        int
	residentLimitBytes uint64
	scienceCalibration forecast.AstrodomeScienceCalibration
	calibrationDigest  string
	terrain            TerrainSkylineSource
	now                func() time.Time
	logf               func(string, ...any)
}

var _ directional.AstrodomeDatasetComputer = (*Computer)(nil)

func NewComputer(config ComputerConfig) (*Computer, error) {
	if strings.TrimSpace(config.DataRoot) == "" {
		return nil, errors.New("astrodome computer data root is required")
	}
	if config.ECCodesWorkers < 1 || config.ECCodesWorkers > 16 {
		return nil, errors.New("astrodome computer ecCodes workers must be between 1 and 16")
	}
	if config.NodeWorkers == 0 {
		config.NodeWorkers = max(1, runtime.GOMAXPROCS(0))
	}
	if config.NodeWorkers < 1 || config.NodeWorkers > 64 {
		return nil, errors.New("astrodome computer node workers must be between 1 and 64")
	}
	if config.ResidentLimitBytes == 0 {
		config.ResidentLimitBytes = iconeu.DomeAstrodomeDefaultResidentLimitBytes
	}
	if err := config.ScienceCalibration.Validate(); err != nil {
		return nil, fmt.Errorf("astrodome computer science calibration: %w", err)
	}
	calibrationDigest, err := config.ScienceCalibration.Digest()
	if err != nil {
		return nil, fmt.Errorf("digest astrodome computer science calibration: %w", err)
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	if config.Terrain == nil {
		config.Terrain = disabledTerrainSkylineSource{}
	}
	if config.Logf == nil {
		config.Logf = func(string, ...any) {}
	}
	return &Computer{
		dataRoot: config.DataRoot, tempRoot: config.TempRoot, ecCodesWorkers: config.ECCodesWorkers,
		nodeWorkers: config.NodeWorkers, residentLimitBytes: config.ResidentLimitBytes, now: config.Now,
		scienceCalibration: config.ScienceCalibration, calibrationDigest: calibrationDigest, terrain: config.Terrain, logf: config.Logf,
	}, nil
}

func (computer *Computer) ComputeAstrodomeDataset(
	ctx context.Context,
	source directional.SourceIdentity,
	payload json.RawMessage,
) (result directional.AstrodomeDatasetInput, resultErr error) {
	if computer == nil {
		return result, errors.New("astrodome computer is required")
	}
	calculationStartedAt := time.Now()
	request, err := DecodeCalculationRequest(bytes.NewReader(payload))
	if err != nil {
		return result, directional.CodedError{Code: "invalid_request", Err: err}
	}
	_, admissionScienceCacheKey, err := calculationScienceCacheKey(request)
	if err != nil {
		return result, directional.CodedError{Code: "invalid_request", Err: err}
	}
	if request.TerrainSkyline.Source == "pending" {
		key, _, keyErr := computer.terrain.CacheKey(request.RequestedLocation)
		if keyErr != nil || key != request.TerrainPreparationKey {
			return result, directional.CodedError{Code: "terrain_identity_mismatch", Err: errors.New("terrain preparation identity differs from worker configuration")}
		}
		resolved, resolveErr := computer.terrain.Resolve(ctx, request.RequestedLocation)
		if resolveErr != nil {
			return result, directional.CodedError{Code: "terrain_unavailable", Err: resolveErr}
		}
		request.TerrainSkyline = resolved
	} else {
		key, _, keyErr := computer.terrain.CacheKey(request.RequestedLocation)
		if keyErr != nil || key != request.TerrainPreparationKey {
			return result, directional.CodedError{Code: "terrain_identity_mismatch", Err: errors.New("terrain profile identity differs from worker configuration")}
		}
	}
	_, finalScienceCacheKey, err := calculationScienceCacheKey(request)
	if err != nil {
		return result, directional.CodedError{Code: "invalid_request", Err: err}
	}
	if request.ScienceCalibrationSHA256 != computer.calibrationDigest ||
		request.ScienceCalibrationVersion != computer.scienceCalibration.Version {
		return result, directional.CodedError{Code: "calibration_mismatch", Err: errors.New("astrodome request calibration differs from worker calibration")}
	}
	if source.Provider != request.SourceIdentity.Provider || source.RunID != request.SourceIdentity.RunID ||
		source.GridProfile != string(request.GridProfile) || source.GeometryDigest != request.GridGeometryDigest {
		return result, directional.CodedError{Code: "source_mismatch", Err: errors.New("astrodome request differs from pinned source")}
	}
	leaseManager, err := model.NewRunLeaseManager(filepath.Join(computer.dataRoot, "state", "run-leases"))
	if err != nil {
		return result, err
	}
	lease, err := leaseManager.AcquireShared(ctx, "icon-eu", request.SourceIdentity.RunID, model.RunRetentionLeaseDigest)
	if err != nil {
		return result, fmt.Errorf("acquire ICON-EU Astrodome run retention lease: %w", err)
	}
	defer func() { resultErr = errors.Join(resultErr, lease.Close()) }()
	contractDigest := strings.TrimPrefix(request.InputContractSHA256, "sha256:")
	manifestPath := filepath.Join(computer.dataRoot, "models", "icon-eu", "dome-runs",
		request.SourceIdentity.RunID, contractDigest, "manifest.json")
	loaded, err := iconeu.LoadDomeManifest(manifestPath)
	if err != nil {
		return result, fmt.Errorf("load pinned ICON-EU Astrodome manifest: %w", err)
	}
	if "sha256:"+loaded.ManifestSHA256 != request.SourceIdentity.RunManifestDigest {
		return result, directional.CodedError{Code: "source_mismatch", Err: errors.New("pinned ICON-EU Astrodome manifest digest changed")}
	}
	volume, err := iconeu.NewDomeVolume(computer.dataRoot, computer.tempRoot, loaded, computer.ecCodesWorkers)
	if err != nil {
		return result, err
	}
	profile, err := forecast.NewAstrodomeGridProfile(request.GridProfile)
	if err != nil {
		return result, err
	}
	refractionCalibration := forecast.DefaultAstrodomeRefractionCalibration()
	preloadStartedAt := time.Now()
	footprint, err := volume.PreloadAstrodomeFootprint(ctx, iconeu.DomeAstrodomePreloadRequest{
		Observer: request.RequestedLocation, Profile: profile, Refraction: refractionCalibration,
		ResidentLimitBytes: computer.residentLimitBytes,
	})
	if err != nil {
		return result, directional.CodedError{Code: "footprint_unavailable", Err: err}
	}
	defer func() { resultErr = errors.Join(resultErr, footprint.Close()) }()
	preloadReport := footprint.Report()
	computer.logf(
		"Astrodome primitive footprint ready: run=%s profile=%s columns=%d projected_resident_bytes=%d extraction_batches=%d envelope_path_m=%.0f source_column_plan=%s duration=%s",
		request.SourceIdentity.RunID,
		request.GridProfile,
		preloadReport.UniqueColumns,
		preloadReport.ProjectedResidentBytes,
		preloadReport.ExtractionBatches,
		preloadReport.EnvelopePathM,
		preloadReport.SourceColumnPlanDigest,
		time.Since(preloadStartedAt).Round(time.Millisecond),
	)
	reconstructor, err := forecast.NewAstrodomePrimitiveReconstructor(footprint)
	if err != nil {
		return result, err
	}
	surfaceHeightM, err := footprint.AstrodomeSurfaceHeightAt(ctx, request.RequestedLocation)
	if err != nil {
		return result, err
	}
	observerHeightM := surfaceHeightM + forecast.AstrodomeRefractionApertureHeightAGLM
	series, err := astronomy.Compute(request.RequestedLocation, request.ValidTimes[0], request.ValidTimes[len(request.ValidTimes)-1])
	if err != nil {
		return result, err
	}
	celestialTracks, err := astronomy.ComputeCelestialTracks(request.RequestedLocation, request.ValidTimes)
	if err != nil {
		return result, fmt.Errorf("compute astrodome celestial tracks: %w", err)
	}
	polarisTrack, err := astronomy.ComputePolarisTrack(request.RequestedLocation, request.ValidTimes)
	if err != nil {
		return result, fmt.Errorf("compute astrodome Polaris track: %w", err)
	}
	nodeDefinitions, err := profile.Nodes()
	if err != nil {
		return result, err
	}
	scienceCalibration := computer.scienceCalibration
	refractivityCalibration := forecast.DefaultAstrodomeRefractivityCalibration()
	scienceStartedAt := time.Now()
	frames, err := computer.computeFrames(ctx, astrodomeFrameCalculation{
		footprint: footprint, reconstructor: reconstructor,
		observer: request.RequestedLocation, observerHeightM: observerHeightM,
		identity: request.SourceIdentity, validTimes: request.ValidTimes,
		definitions: nodeDefinitions, series: series,
		refractionCalibration: refractionCalibration, refractivityCalibration: refractivityCalibration,
		scienceCalibration: scienceCalibration, calculationStartedAt: calculationStartedAt,
		scienceStartedAt: scienceStartedAt,
	})
	if err != nil {
		return result, err
	}
	height := surfaceHeightM
	generatedAt := computer.now().UTC()
	computer.logf("Astrodome calculation complete: run=%s frames=%d nodes_per_frame=%d duration=%s",
		request.SourceIdentity.RunID, len(frames), len(nodeDefinitions), time.Since(calculationStartedAt).Round(time.Millisecond))
	requestedLocation, modelLocation := astrodomeDatasetLocations(request.RequestedLocation, height)
	return directional.AstrodomeDatasetInput{
		AdmissionScienceCacheKey: admissionScienceCacheKey,
		FinalScienceCacheKey:     finalScienceCacheKey,
		SourceIdentity:           request.SourceIdentity, GeneratedAt: generatedAt,
		SourceColumnPlanDigest: preloadReport.SourceColumnPlanDigest,
		RequestedLocation:      requestedLocation,
		ModelLocation:          modelLocation,
		Profile:                profile, RayGeometryVersion: forecast.AstrodomeRefractionGeometryVersion,
		RefractionVersion:   forecast.AstrodomeRefractionIntegratorVersion,
		RefractivityVersion: forecast.AstrodomeCiddorVersion,
		CalibrationVersion:  scienceCalibration.Version, CalibrationSHA256: computer.calibrationDigest,
		CelestialTracks: celestialTracks,
		PolarisTrack:    polarisTrack,
		TerrainSkyline:  request.TerrainSkyline,
		Frames:          frames,
	}, nil
}

func astrodomeDatasetLocations(
	location forecast.Location,
	surfaceElevationM float64,
) (directional.AstrodomeDatasetLocation, directional.AstrodomeDatasetLocation) {
	requested := directional.AstrodomeDatasetLocation{
		Latitude: location.Latitude, Longitude: location.Longitude, TimeZone: location.TimeZone,
	}
	model := directional.AstrodomeDatasetLocation{
		Latitude: location.Latitude, Longitude: location.Longitude, SurfaceElevationM: &surfaceElevationM,
	}
	return requested, model
}

const astrodomePreparedFrameWindow = 2

type astrodomeFrameCalculation struct {
	footprint               *iconeu.DomeAstrodomeFootprint
	reconstructor           *forecast.AstrodomePrimitiveReconstructor
	observer                forecast.Location
	observerHeightM         float64
	identity                forecast.AstrodomePrimitiveVolumeIdentity
	validTimes              []time.Time
	definitions             []forecast.AstrodomeGridNode
	series                  astronomy.Series
	refractionCalibration   forecast.AstrodomeRefractionCalibration
	refractivityCalibration forecast.AstrodomeRefractivityCalibration
	scienceCalibration      forecast.AstrodomeScienceCalibration
	calculationStartedAt    time.Time
	scienceStartedAt        time.Time
}

type astrodomeFrameWork struct {
	validAt      time.Time
	site         forecast.AstrodomeScienceSiteInputs
	field        forecast.AstrodomeRefractionField
	scienceFrame *forecast.AstrodomePreparedScienceFrame
	nativeFrame  *iconeu.DomeAstrodomeScienceNativeFrame
	nodes        []forecast.AstrodomeScienceNode
	remaining    atomic.Int32
	done         chan struct{}
	failureMu    sync.Mutex
	failedNodes  int
	firstErr     error
}

type astrodomeNodeTask struct {
	frame *astrodomeFrameWork
	index int
}

func (computer *Computer) computeFrames(
	ctx context.Context,
	calculation astrodomeFrameCalculation,
) ([]directional.AstrodomeDatasetFrameInput, error) {
	if len(calculation.validTimes) == 0 || len(calculation.definitions) == 0 {
		return nil, errors.New("astrodome frame calculation is empty")
	}
	workerContext, cancel := context.WithCancel(ctx)
	tasks := make(chan astrodomeNodeTask, astrodomePreparedFrameWindow*len(calculation.definitions))
	workerCount := min(computer.nodeWorkers, len(calculation.definitions))
	var workers sync.WaitGroup
	for range workerCount {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for task := range tasks {
				computer.computeNodeTask(workerContext, calculation, task)
				if task.frame.remaining.Add(-1) == 0 {
					close(task.frame.done)
				}
			}
		}()
	}
	closed := false
	cleanup := func() {
		cancel()
		if !closed {
			close(tasks)
			closed = true
		}
		workers.Wait()
	}
	defer cleanup()

	result := make([]directional.AstrodomeDatasetFrameInput, len(calculation.validTimes))
	resident := make([]*astrodomeFrameWork, len(calculation.validTimes))
	nextPrepare := 0
	for completed := 0; completed < len(calculation.validTimes); completed++ {
		for nextPrepare < len(calculation.validTimes) && nextPrepare-completed < astrodomePreparedFrameWindow {
			frame, err := computer.prepareFrame(workerContext, calculation, nextPrepare)
			if err != nil {
				return nil, err
			}
			resident[nextPrepare] = frame
			for nodeIndex := range calculation.definitions {
				tasks <- astrodomeNodeTask{frame: frame, index: nodeIndex}
			}
			nextPrepare++
		}
		frame := resident[completed]
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-frame.done:
		}
		frame.failureMu.Lock()
		failedNodes, firstErr := frame.failedNodes, frame.firstErr
		frame.failureMu.Unlock()
		if failedNodes == len(calculation.definitions) {
			return nil, fmt.Errorf("all astrodome nodes failed at %s: %w", frame.validAt.Format(time.RFC3339), firstErr)
		}
		if failedNodes > 0 {
			computer.logf(
				"Astrodome retained partial frame: valid_at=%s unavailable_nodes=%d total_nodes=%d first_error=%v",
				frame.validAt.Format(time.RFC3339), failedNodes, len(calculation.definitions), firstErr,
			)
		}
		result[completed] = directional.AstrodomeDatasetFrameInput{
			ValidAt: frame.validAt, Surface: frame.site,
			SolarAltitudeDeg: calculation.series.SunAltitudeDegrees(frame.validAt), Nodes: frame.nodes,
		}
		resident[completed] = nil
		completedFrames := completed + 1
		if completedFrames == 1 || completedFrames%6 == 0 || completedFrames == len(calculation.validTimes) {
			totalElapsed := time.Since(calculation.calculationStartedAt)
			scienceElapsed := time.Since(calculation.scienceStartedAt)
			remaining := scienceElapsed * time.Duration(len(calculation.validTimes)-completedFrames) /
				time.Duration(completedFrames)
			computer.logf(
				"Astrodome calculation progress: run=%s completed_frames=%d total_frames=%d total_elapsed=%s science_elapsed=%s estimated_remaining=%s",
				calculation.identity.RunID, completedFrames, len(calculation.validTimes),
				totalElapsed.Round(time.Second), scienceElapsed.Round(time.Second), remaining.Round(time.Second),
			)
		}
	}
	close(tasks)
	closed = true
	workers.Wait()
	return result, nil
}

func (computer *Computer) prepareFrame(
	ctx context.Context,
	calculation astrodomeFrameCalculation,
	index int,
) (*astrodomeFrameWork, error) {
	validAt := calculation.validTimes[index]
	site, err := calculation.footprint.AstrodomeScienceSiteAt(ctx, validAt, calculation.observer)
	if err != nil {
		return nil, err
	}
	field, err := forecast.NewAstrodomeReconstructedRefractivityField(
		calculation.reconstructor, validAt, calculation.refractivityCalibration,
	)
	if err != nil {
		return nil, err
	}
	scienceFrame, err := forecast.NewAstrodomePreparedScienceFrame(calculation.reconstructor, validAt)
	if err != nil {
		return nil, err
	}
	nativeFrame, err := calculation.footprint.NewAstrodomeScienceNativeFrame(validAt)
	if err != nil {
		return nil, err
	}
	frame := &astrodomeFrameWork{
		validAt: validAt, site: site, field: field,
		scienceFrame: scienceFrame, nativeFrame: nativeFrame,
		nodes: make([]forecast.AstrodomeScienceNode, len(calculation.definitions)), done: make(chan struct{}),
	}
	frame.remaining.Store(int32(len(calculation.definitions)))
	return frame, nil
}

func (computer *Computer) computeNodeTask(
	ctx context.Context,
	calculation astrodomeFrameCalculation,
	task astrodomeNodeTask,
) {
	frame := task.frame
	definition := calculation.definitions[task.index]
	initial, err := forecast.NewAstrodomeRay(calculation.observer, calculation.observerHeightM,
		definition.ElevationDegrees, definition.AzimuthDegrees)
	if err == nil {
		var traced forecast.AstrodomeRefractedRay
		traced, err = forecast.TraceAstrodomeRefractedRay(ctx, frame.field, initial, calculation.refractionCalibration)
		if errors.Is(err, forecast.ErrAstrodomeRefractionTerrain) {
			frame.nodes[task.index] = astrodomeModelTerrainBlockedNode(
				definition, frame.validAt, calculation.identity, frame.site,
			)
			err = nil
		} else if err == nil {
			var path forecast.AstrodomeSciencePath
			path, err = calculation.footprint.BuildAstrodomeSciencePath(
				ctx, traced, frame.validAt, calculation.scienceCalibration,
			)
			if err == nil {
				path.NativeContext = frame.nativeFrame
				frame.nodes[task.index], err = forecast.ComputeAstrodomeScienceNodeRefractedPrepared(
					ctx, frame.scienceFrame, traced, frame.validAt, path, frame.site, calculation.scienceCalibration,
				)
			}
		}
	}
	if err == nil || ctx.Err() != nil {
		return
	}
	frame.nodes[task.index] = astrodomeUnavailableNode(
		definition, frame.validAt, calculation.identity, frame.site, err,
	)
	frame.failureMu.Lock()
	frame.failedNodes++
	if frame.firstErr == nil {
		frame.firstErr = fmt.Errorf("astrodome node %d at %s: %w",
			task.index, frame.validAt.Format(time.RFC3339), err)
	}
	frame.failureMu.Unlock()
}

func astrodomeModelTerrainBlockedNode(
	definition forecast.AstrodomeGridNode,
	validAt time.Time,
	identity forecast.AstrodomePrimitiveVolumeIdentity,
	site forecast.AstrodomeScienceSiteInputs,
) forecast.AstrodomeScienceNode {
	return forecast.AstrodomeScienceNode{
		Available: false, State: forecast.AstrodomeScienceNodeTerrainBlocked,
		TerrainObstructionSource: forecast.AstrodomeTerrainObstructionHHL,
		UnavailableReason:        forecast.ErrAstrodomeScienceTerrainBlocked.Error(),
		ScienceVersion:           forecast.AstrodomeScienceVersion, SourceIdentity: identity, ValidAt: validAt.UTC(),
		ElevationDegrees: definition.ElevationDegrees, AzimuthDegrees: definition.AzimuthDegrees,
		GeometryMode:        forecast.AstrodomeScienceGeometryRefractionFull,
		RayGeometryVersion:  forecast.AstrodomeRefractionGeometryVersion,
		RefractionVersion:   forecast.AstrodomeRefractionIntegratorVersion,
		RefractivityVersion: forecast.AstrodomeCiddorVersion,
		Tau0State:           "unavailable",
		Quality:             astrodomeUnavailableQuality(site),
	}
}

func astrodomeUnavailableNode(
	definition forecast.AstrodomeGridNode,
	validAt time.Time,
	identity forecast.AstrodomePrimitiveVolumeIdentity,
	site forecast.AstrodomeScienceSiteInputs,
	cause error,
) forecast.AstrodomeScienceNode {
	reason := "directional calculation unavailable"
	if cause != nil {
		reason = cause.Error()
	}
	return forecast.AstrodomeScienceNode{
		Available: false, State: forecast.AstrodomeScienceNodeUnavailable,
		TerrainObstructionSource: forecast.AstrodomeTerrainObstructionNone,
		UnavailableReason:        reason,
		ScienceVersion:           forecast.AstrodomeScienceVersion, SourceIdentity: identity, ValidAt: validAt.UTC(),
		ElevationDegrees: definition.ElevationDegrees, AzimuthDegrees: definition.AzimuthDegrees,
		GeometryMode:        forecast.AstrodomeScienceGeometryRefractionFull,
		RayGeometryVersion:  forecast.AstrodomeRefractionGeometryVersion,
		RefractionVersion:   forecast.AstrodomeRefractionIntegratorVersion,
		RefractivityVersion: forecast.AstrodomeCiddorVersion,
		Tau0State:           "unavailable",
		Quality:             astrodomeUnavailableQuality(site),
	}
}

func astrodomeUnavailableQuality(site forecast.AstrodomeScienceSiteInputs) forecast.AstrodomeScienceQuality {
	return forecast.AstrodomeScienceQuality{
		LeadTimeQualityHeuristic: forecast.AstrodomeForecastLeadTimeQualityHeuristic(site.ForecastLeadHours),
		TemporalResolutionHours:  1,
		Category:                 forecast.AstrodomeScienceQualityUnavailable,
	}
}
