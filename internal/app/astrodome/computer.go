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
	if config.Logf == nil {
		config.Logf = func(string, ...any) {}
	}
	return &Computer{
		dataRoot: config.DataRoot, tempRoot: config.TempRoot, ecCodesWorkers: config.ECCodesWorkers,
		nodeWorkers: config.NodeWorkers, residentLimitBytes: config.ResidentLimitBytes, now: config.Now,
		scienceCalibration: config.ScienceCalibration, calibrationDigest: calibrationDigest, logf: config.Logf,
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
	nodeDefinitions, err := profile.Nodes()
	if err != nil {
		return result, err
	}
	scienceCalibration := computer.scienceCalibration
	refractivityCalibration := forecast.DefaultAstrodomeRefractivityCalibration()
	frames := make([]directional.AstrodomeDatasetFrameInput, len(request.ValidTimes))
	scienceStartedAt := time.Now()
	for frameIndex, validAt := range request.ValidTimes {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		site, siteErr := footprint.AstrodomeScienceSiteAt(ctx, validAt, request.RequestedLocation)
		if siteErr != nil {
			return result, siteErr
		}
		field, fieldErr := forecast.NewAstrodomeReconstructedRefractivityField(
			reconstructor, validAt, refractivityCalibration,
		)
		if fieldErr != nil {
			return result, fieldErr
		}
		nodes := make([]forecast.AstrodomeScienceNode, len(nodeDefinitions))
		if nodeErr := computer.computeFrameNodes(ctx, footprint, reconstructor, field, request.RequestedLocation,
			observerHeightM, validAt, request.SourceIdentity, nodeDefinitions, nodes, site,
			refractionCalibration, scienceCalibration); nodeErr != nil {
			return result, nodeErr
		}
		frames[frameIndex] = directional.AstrodomeDatasetFrameInput{
			ValidAt: validAt, Surface: site, SolarAltitudeDeg: series.SunAltitudeDegrees(validAt), Nodes: nodes,
		}
		completedFrames := frameIndex + 1
		if completedFrames == 1 || completedFrames%6 == 0 || completedFrames == len(request.ValidTimes) {
			totalElapsed := time.Since(calculationStartedAt)
			scienceElapsed := time.Since(scienceStartedAt)
			remaining := scienceElapsed * time.Duration(len(request.ValidTimes)-completedFrames) / time.Duration(completedFrames)
			computer.logf(
				"Astrodome calculation progress: run=%s completed_frames=%d total_frames=%d total_elapsed=%s science_elapsed=%s estimated_remaining=%s",
				request.SourceIdentity.RunID, completedFrames, len(request.ValidTimes),
				totalElapsed.Round(time.Second), scienceElapsed.Round(time.Second), remaining.Round(time.Second),
			)
		}
	}
	height := surfaceHeightM
	generatedAt := computer.now().UTC()
	computer.logf("Astrodome calculation complete: run=%s frames=%d nodes_per_frame=%d duration=%s",
		request.SourceIdentity.RunID, len(frames), len(nodeDefinitions), time.Since(calculationStartedAt).Round(time.Millisecond))
	requestedLocation, modelLocation := astrodomeDatasetLocations(request.RequestedLocation, height)
	return directional.AstrodomeDatasetInput{
		SourceIdentity: request.SourceIdentity, GeneratedAt: generatedAt,
		SourceColumnPlanDigest: preloadReport.SourceColumnPlanDigest,
		RequestedLocation:      requestedLocation,
		ModelLocation:          modelLocation,
		Profile:                profile, RayGeometryVersion: forecast.AstrodomeRefractionGeometryVersion,
		RefractionVersion:   forecast.AstrodomeRefractionIntegratorVersion,
		RefractivityVersion: forecast.AstrodomeCiddorVersion,
		CalibrationVersion:  scienceCalibration.Version, CalibrationSHA256: computer.calibrationDigest,
		Frames: frames,
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

func (computer *Computer) computeFrameNodes(
	ctx context.Context,
	footprint *iconeu.DomeAstrodomeFootprint,
	reconstructor *forecast.AstrodomePrimitiveReconstructor,
	field forecast.AstrodomeRefractionField,
	observer forecast.Location,
	observerHeightM float64,
	validAt time.Time,
	identity forecast.AstrodomePrimitiveVolumeIdentity,
	definitions []forecast.AstrodomeGridNode,
	destination []forecast.AstrodomeScienceNode,
	site forecast.AstrodomeScienceSiteInputs,
	refractionCalibration forecast.AstrodomeRefractionCalibration,
	scienceCalibration forecast.AstrodomeScienceCalibration,
) error {
	type task struct{ index int }
	work := make(chan task)
	workerContext, cancel := context.WithCancel(ctx)
	defer cancel()
	var wait sync.WaitGroup
	var firstErr error
	var failedNodes int
	var failureMu sync.Mutex
	workerCount := min(computer.nodeWorkers, len(definitions))
	for range workerCount {
		wait.Add(1)
		go func() {
			defer wait.Done()
			for item := range work {
				definition := definitions[item.index]
				initial, err := forecast.NewAstrodomeRay(observer, observerHeightM,
					definition.ElevationDegrees, definition.AzimuthDegrees)
				if err == nil {
					var traced forecast.AstrodomeRefractedRay
					traced, err = forecast.TraceAstrodomeRefractedRay(workerContext, field, initial, refractionCalibration)
					if errors.Is(err, forecast.ErrAstrodomeRefractionTerrain) {
						destination[item.index] = astrodomeTerrainBlockedNode(definition, validAt,
							identity, site)
						err = nil
					} else if err == nil {
						var path forecast.AstrodomeSciencePath
						path, err = footprint.BuildAstrodomeSciencePath(workerContext, traced, validAt, scienceCalibration)
						if err == nil {
							destination[item.index], err = forecast.ComputeAstrodomeScienceNodeRefracted(
								workerContext, reconstructor, traced, validAt, path, site, scienceCalibration,
							)
						}
					}
				}
				if err != nil {
					if workerContext.Err() != nil {
						return
					}
					destination[item.index] = astrodomeUnavailableNode(definition, validAt, identity, site, err)
					failureMu.Lock()
					failedNodes++
					if firstErr == nil {
						firstErr = fmt.Errorf("astrodome node %d at %s: %w",
							item.index, validAt.Format(time.RFC3339), err)
					}
					failureMu.Unlock()
				}
			}
		}()
	}
enqueue:
	for index := range definitions {
		select {
		case <-workerContext.Done():
			break enqueue
		case work <- task{index: index}:
		}
	}
	close(work)
	wait.Wait()
	if err := ctx.Err(); err != nil {
		return err
	}
	if failedNodes == len(definitions) {
		return fmt.Errorf("all astrodome nodes failed at %s: %w", validAt.Format(time.RFC3339), firstErr)
	}
	if failedNodes > 0 {
		computer.logf(
			"Astrodome retained partial frame: valid_at=%s unavailable_nodes=%d total_nodes=%d first_error=%v",
			validAt.Format(time.RFC3339), failedNodes, len(definitions), firstErr,
		)
	}
	return nil
}

func astrodomeTerrainBlockedNode(
	definition forecast.AstrodomeGridNode,
	validAt time.Time,
	identity forecast.AstrodomePrimitiveVolumeIdentity,
	site forecast.AstrodomeScienceSiteInputs,
) forecast.AstrodomeScienceNode {
	return forecast.AstrodomeScienceNode{
		Available: false, State: forecast.AstrodomeScienceNodeTerrainBlocked,
		UnavailableReason: forecast.ErrAstrodomeScienceTerrainBlocked.Error(),
		ScienceVersion:    forecast.AstrodomeScienceVersion, SourceIdentity: identity, ValidAt: validAt.UTC(),
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
		UnavailableReason: reason,
		ScienceVersion:    forecast.AstrodomeScienceVersion, SourceIdentity: identity, ValidAt: validAt.UTC(),
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
		LeadQuality:             forecast.AstrodomeForecastLeadQuality(site.ForecastLeadHours),
		TemporalResolutionHours: 1,
		Category:                forecast.AstrodomeScienceQualityUnavailable,
	}
}
