package bot

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"bot_astrosferum/internal/app/directional"
	"bot_astrosferum/internal/forecast"
	"bot_astrosferum/internal/render"
)

const horizonDirectionalGridProfile = forecast.HorizonGridProfile

// UseDirectionalCoordinator moves Horizon extraction, calculation, and
// rendering into the same strict FIFO used by Astrodome. Delivery remains in
// HorizonJobs because it is localized and messenger-specific, not heavy
// scientific work. It must be called before either component is started.
func (jobs *HorizonJobs) UseDirectionalCoordinator(coordinator *directional.Coordinator) error {
	runner, err := jobs.DirectionalRunner()
	if err != nil {
		return err
	}
	return jobs.UseDirectionalCoordinatorWithRunner(coordinator, runner)
}

// DirectionalRunner exposes the existing provider-neutral Horizon pipeline to
// either the in-process coordinator or an isolated worker composition.
func (jobs *HorizonJobs) DirectionalRunner() (directional.Runner, error) {
	if jobs == nil {
		return nil, errors.New("horizon jobs are required")
	}
	return directional.RunnerFunc(jobs.runDirectional), nil
}

// UseDirectionalCoordinatorWithRunner is the production isolation seam. A
// remote worker adapter can execute the same Horizon contract out of process,
// while the coordinator still owns the sole FIFO and active slot.
func (jobs *HorizonJobs) UseDirectionalCoordinatorWithRunner(coordinator *directional.Coordinator, runner directional.Runner) error {
	if jobs == nil || coordinator == nil {
		return errors.New("horizon jobs and directional coordinator are required")
	}
	if runner == nil {
		return errors.New("horizon directional runner is required")
	}
	jobs.mu.Lock()
	defer jobs.mu.Unlock()
	if jobs.started || jobs.closed {
		return errors.New("directional coordinator must be configured before Horizon jobs start")
	}
	if jobs.directional != nil {
		return errors.New("directional coordinator is already configured for Horizon")
	}
	if err := coordinator.Register(directional.KindHorizon, runner); err != nil {
		return err
	}
	jobs.directional = coordinator
	return nil
}

func (jobs *HorizonJobs) handleDirectionalAdmission(ctx context.Context, job *horizonJob, waiter horizonWaiter) error {
	jobs.mu.Lock()
	if !jobs.started || jobs.closed || jobs.root == nil || jobs.root.Err() != nil {
		jobs.mu.Unlock()
		jobs.sendStatus(waiter.messenger, waiter.chatID, waiter.language.text(
			"Анализ горизонта сейчас недоступен.", "Horizon analysis is currently unavailable."))
		completeHorizonMessenger(waiter.messenger, ErrHorizonUnsupported)
		return nil
	}
	jobs.pruneRecentLocked(jobs.now())
	if activeKey, exists := jobs.active[waiter.identity]; exists {
		jobs.mu.Unlock()
		message := waiter.language.text(
			"У вас уже выполняется анализ горизонта. Дождитесь результата.",
			"You already have a horizon analysis in progress. Please wait for it.")
		if activeKey == job.key {
			message = waiter.language.text(
				"Этот запрос уже обрабатывается.", "This request is already being processed.")
		}
		jobs.sendStatus(waiter.messenger, waiter.chatID, message)
		completeHorizonMessenger(waiter.messenger, ErrHorizonUserBusy)
		return nil
	}
	jobs.active[waiter.identity] = job.key
	coordinator := jobs.directional
	jobs.mu.Unlock()

	payload, err := json.Marshal(job.request)
	if err != nil {
		jobs.releaseDirectionalWaiter(waiter.identity, job.key, false)
		jobs.sendStatus(waiter.messenger, waiter.chatID, waiter.language.text(
			"Не удалось подготовить расчёт горизонта.", "Could not prepare the horizon calculation."))
		completeHorizonMessenger(waiter.messenger, err)
		return nil
	}
	geometryDigest, err := horizonPlanDigest(job.plan)
	if err != nil {
		jobs.releaseDirectionalWaiter(waiter.identity, job.key, false)
		jobs.sendStatus(waiter.messenger, waiter.chatID, waiter.language.text(
			"Не удалось подготовить расчёт горизонта.", "Could not prepare the horizon calculation."))
		completeHorizonMessenger(waiter.messenger, err)
		return nil
	}
	requestFamilyKey, err := horizonRequestFamilyKey(job.request, jobs.calibration, jobs.config.RenderAlgorithmVersion)
	if err != nil {
		jobs.releaseDirectionalWaiter(waiter.identity, job.key, false)
		jobs.sendStatus(waiter.messenger, waiter.chatID, waiter.language.text(
			"Не удалось подготовить расчёт горизонта.", "Could not prepare the horizon calculation."))
		completeHorizonMessenger(waiter.messenger, err)
		return nil
	}
	ticket, err := coordinator.Submit(ctx, directional.Request{
		Kind: directional.KindHorizon, OwnerID: horizonDirectionalOwner(waiter.identity),
		IdempotencyKey: requestFamilyKey, RequestFamilyKey: requestFamilyKey, ScienceCacheKey: job.key,
		Source: directional.SourceIdentity{
			Provider: "ICON-EU", RunID: job.request.RunID,
			GridProfile: horizonDirectionalGridProfile, GeometryDigest: geometryDigest,
		},
		Payload: payload,
	})
	if err != nil {
		jobs.releaseDirectionalWaiter(waiter.identity, job.key, false)
		switch {
		case errors.Is(err, directional.ErrOwnerBusy):
			jobs.sendStatus(waiter.messenger, waiter.chatID, waiter.language.text(
				"У вас уже выполняется другой направленный расчёт. Дождитесь результата.",
				"You already have another directional calculation in progress. Please wait for it."))
		case errors.Is(err, directional.ErrQueueFull):
			jobs.sendStatus(waiter.messenger, waiter.chatID, waiter.language.text(
				"Очередь направленных расчётов заполнена. Попробуйте позже.",
				"The directional-calculation queue is full. Please try again later."))
		default:
			jobs.sendStatus(waiter.messenger, waiter.chatID, waiter.language.text(
				"Анализ горизонта сейчас недоступен.", "Horizon analysis is currently unavailable."))
		}
		completeHorizonMessenger(waiter.messenger, err)
		return nil
	}

	status, statusErr := ticket.Status()
	jobs.mu.Lock()
	if jobs.closed || jobs.root == nil || jobs.root.Err() != nil {
		jobs.mu.Unlock()
		_ = ticket.Cancel()
		jobs.releaseDirectionalWaiter(waiter.identity, job.key, false)
		completeHorizonMessenger(waiter.messenger, context.Canceled)
		return nil
	}
	root := jobs.root
	jobs.directionalTickets[waiter.identity] = ticket
	jobs.wait.Add(1)
	jobs.mu.Unlock()
	go jobs.awaitDirectional(root, ticket, job, waiter)

	if statusErr != nil {
		jobs.sendStatus(waiter.messenger, waiter.chatID, waiter.language.text(
			"Расчёт горизонта принят.", "Horizon calculation accepted."))
		return nil
	}
	if status.State == directional.StateReady {
		jobs.sendStatus(waiter.messenger, waiter.chatID, waiter.language.text(
			"Отправляю готовый расчёт из кэша.", "Sending the completed calculation from cache."))
		return nil
	}
	position := 1
	if status.QueuePosition != nil && *status.QueuePosition > 0 {
		position = *status.QueuePosition
	}
	eta := jobs.config.EstimatedDuration
	if status.EstimatedAt != nil {
		eta = status.EstimatedAt.Sub(jobs.now())
		if eta < 0 {
			eta = 0
		}
	}
	jobs.sendStatus(waiter.messenger, waiter.chatID, fmt.Sprintf(waiter.language.text(
		"Анализ горизонта поставлен в общую очередь: позиция %d, ориентировочно %s.",
		"Horizon analysis queued in the shared queue: position %d, approximately %s."),
		position, compactHorizonDuration(eta, waiter.language)))
	return nil
}

func (jobs *HorizonJobs) awaitDirectional(root context.Context, ticket *directional.Ticket, job *horizonJob, waiter horizonWaiter) {
	defer jobs.wait.Done()
	defer jobs.clearDirectionalTicket(waiter.identity, ticket)
	result, err := ticket.Wait(root)
	if err != nil {
		if root.Err() != nil {
			_ = ticket.Cancel()
			jobs.releaseDirectionalWaiter(waiter.identity, job.key, false)
			return
		}
		jobs.logJobError("directional", job, err)
		jobs.deliverJob(job, "", err)
		return
	}
	jobs.deliverJob(job, result.Path, nil)
}

func (jobs *HorizonJobs) clearDirectionalTicket(identity horizonUserIdentity, ticket *directional.Ticket) {
	jobs.mu.Lock()
	defer jobs.mu.Unlock()
	if jobs.directionalTickets[identity] == ticket {
		delete(jobs.directionalTickets, identity)
	}
}

func (jobs *HorizonJobs) releaseDirectionalWaiter(identity horizonUserIdentity, key string, recent bool) {
	jobs.mu.Lock()
	defer jobs.mu.Unlock()
	if jobs.active[identity] != key {
		return
	}
	delete(jobs.active, identity)
	if recent {
		jobs.recent[identity] = horizonRecentClick{key: key, at: jobs.now()}
	}
}

func (jobs *HorizonJobs) runDirectional(ctx context.Context, execution directional.Execution) (directional.RunnerResult, error) {
	var request horizonRequest
	if err := json.Unmarshal(execution.Payload, &request); err != nil {
		return directional.RunnerResult{}, directional.CodedError{Code: "invalid_payload", Err: err}
	}
	if err := validateHorizonRequest(request); err != nil {
		return directional.RunnerResult{}, directional.CodedError{Code: "invalid_payload", Err: err}
	}
	if request.TerrainSkyline.Version == "" {
		return directional.RunnerResult{}, directional.CodedError{Code: "invalid_payload", Err: errors.New("terrain skyline is required")}
	}
	admissionArtifactKey, err := horizonCacheKey(request, jobs.calibration, jobs.config.RenderAlgorithmVersion)
	if err != nil || execution.ScienceCacheKey != admissionArtifactKey {
		return directional.RunnerResult{}, directional.CodedError{
			Code: "science_identity_mismatch", Err: errors.New("horizon admission identity differs from the pinned request"),
		}
	}
	if request.TerrainSkyline.Source == "pending" {
		key, _, keyErr := jobs.terrain.CacheKey(request.Location)
		if keyErr != nil || key != request.TerrainPreparationKey {
			return directional.RunnerResult{}, directional.CodedError{Code: "terrain_identity_mismatch", Err: errors.New("terrain preparation identity differs from worker configuration")}
		}
		resolved, resolveErr := jobs.terrain.Resolve(ctx, request.Location)
		if resolveErr != nil {
			return directional.RunnerResult{}, directional.CodedError{Code: "terrain_unavailable", Err: resolveErr}
		}
		request.TerrainSkyline = resolved
	} else {
		key, _, keyErr := jobs.terrain.CacheKey(request.Location)
		if keyErr != nil || key != request.TerrainPreparationKey {
			return directional.RunnerResult{}, directional.CodedError{Code: "terrain_identity_mismatch", Err: errors.New("terrain profile identity differs from worker configuration")}
		}
	}
	artifactKey, err := horizonCacheKey(request, jobs.calibration, jobs.config.RenderAlgorithmVersion)
	if err != nil {
		return directional.RunnerResult{}, directional.CodedError{Code: "invalid_payload", Err: err}
	}
	plan, err := forecast.NewHorizonPlan(request.Location, request.ObserverSurfaceElevationM)
	if err != nil {
		return directional.RunnerResult{}, directional.CodedError{Code: "invalid_geometry", Err: err}
	}
	digest, err := horizonPlanDigest(plan)
	if err != nil || digest != execution.Source.GeometryDigest {
		return directional.RunnerResult{}, directional.CodedError{Code: "geometry_mismatch", Err: errors.New("horizon geometry differs from the pinned request")}
	}
	if execution.Source.Provider != "ICON-EU" || execution.Source.RunID != request.RunID ||
		execution.Source.GridProfile != horizonDirectionalGridProfile {
		return directional.RunnerResult{}, directional.CodedError{Code: "source_mismatch", Err: errors.New("horizon source differs from the pinned request")}
	}
	currentRun, err := jobs.source.CurrentRunID()
	if err != nil || currentRun != request.RunID {
		if err == nil {
			err = ErrHorizonStaleAction
		}
		return directional.RunnerResult{}, directional.CodedError{Code: "stale_run", Err: err}
	}
	snapshots, err := jobs.source.Series(ctx, request.RunID, plan)
	if err != nil {
		return directional.RunnerResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return directional.RunnerResult{}, err
	}
	if err := validateHorizonSeries(request.RunID, snapshots); err != nil {
		return directional.RunnerResult{}, directional.CodedError{Code: "invalid_series", Err: err}
	}
	frames, err := jobs.compute(ctx, snapshots, plan, jobs.calibration)
	if err != nil {
		return directional.RunnerResult{}, err
	}
	if err := forecast.ApplyTerrainSkylineToHorizon(frames, request.TerrainSkyline); err != nil {
		return directional.RunnerResult{}, directional.CodedError{Code: "invalid_terrain", Err: err}
	}
	if err := ctx.Err(); err != nil {
		return directional.RunnerResult{}, err
	}
	imageDestination := filepath.Join(execution.Workspace, horizonCacheImage)
	renderInput := HorizonRenderInput{
		Location: request.Location, Provider: execution.Source.Provider,
		RunID: request.RunID, Grid: "ICON-EU 0.0625°", Frames: frames, TerrainSkyline: request.TerrainSkyline,
	}
	if err := jobs.render(ctx, imageDestination, renderInput, request.Language.renderCode()); err != nil {
		return directional.RunnerResult{}, err
	}
	dataset, err := render.PrepareHorizonInteractiveDataset(render.HorizonInput{
		Location: renderInput.Location, Provider: renderInput.Provider, RunID: renderInput.RunID,
		Grid: renderInput.Grid, Frames: renderInput.Frames, TerrainSkyline: renderInput.TerrainSkyline,
	}, artifactKey, request.ObserverSurfaceElevationM, jobs.calibration)
	if err != nil {
		return directional.RunnerResult{}, err
	}
	datasetDestination := filepath.Join(execution.Workspace, horizonCacheDataset)
	if err := render.SaveHorizonInteractiveDataset(datasetDestination, dataset); err != nil {
		return directional.RunnerResult{}, err
	}
	destination := filepath.Join(execution.Workspace, "horizon.bundle")
	if err := writeHorizonBundle(destination, imageDestination, datasetDestination); err != nil {
		return directional.RunnerResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return directional.RunnerResult{}, err
	}
	currentRun, err = jobs.source.CurrentRunID()
	if err != nil || currentRun != request.RunID {
		if err == nil {
			err = ErrHorizonStaleAction
		}
		return directional.RunnerResult{}, directional.CodedError{Code: "stale_run", Err: err}
	}
	info, err := os.Stat(destination)
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 {
		return directional.RunnerResult{}, directional.CodedError{Code: "render_failed", Err: errors.New("horizon renderer did not produce an image")}
	}
	return directional.RunnerResult{
		DatasetPath: destination, FinalScienceCacheKey: artifactKey,
		Provider: execution.Source.Provider, RunID: execution.Source.RunID,
		GridProfile: execution.Source.GridProfile, GeometryDigest: execution.Source.GeometryDigest,
	}, nil
}

func horizonDirectionalOwner(identity horizonUserIdentity) string {
	return identity.platform + ":" + strconv.FormatInt(identity.userID, 10)
}

func horizonPlanDigest(plan forecast.HorizonPlan) (string, error) {
	identity := plan
	identity.Observer.TimeZone = ""
	identity.Directions = append([]forecast.HorizonDirectionPlan(nil), plan.Directions...)
	for directionIndex := range identity.Directions {
		identity.Directions[directionIndex].Samples = append([]forecast.HorizonSample(nil), plan.Directions[directionIndex].Samples...)
		for sampleIndex := range identity.Directions[directionIndex].Samples {
			identity.Directions[directionIndex].Samples[sampleIndex].Midpoint.TimeZone = ""
		}
	}
	encoded, err := json.Marshal(identity)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}
