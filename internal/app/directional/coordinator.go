package directional

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

type Coordinator struct {
	config Config
	cache  *resultCache

	mutex       sync.Mutex
	runners     map[Kind]Runner
	queue       []*directionalJob
	jobs        map[string]*directionalJob
	scienceJobs map[string]*directionalJob
	idempotency map[string]*directionalJob
	ownerJobs   map[string]*directionalJob
	completed   []string
	active      map[string]*directionalJob
	wake        chan struct{}
	started     bool
	closed      bool
	ctx         context.Context
	cancel      context.CancelFunc
	wait        sync.WaitGroup
}

type directionalJob struct {
	id                  string
	kind                Kind
	scienceDigest       string
	publishedDigest     string
	requestFamilyDigest string
	scienceCacheKey     string
	source              SourceIdentity
	payload             json.RawMessage
	owners              map[string]struct{}
	idempotencyKeys     map[string]string
	state               State
	createdAt           time.Time
	updatedAt           time.Time
	startedAt           time.Time
	finishedAt          time.Time
	result              Result
	failureCode         string
	err                 error
	runCancel           context.CancelFunc
	done                chan struct{}
	doneOnce            sync.Once
}

type Ticket struct {
	coordinator *Coordinator
	jobID       string
	ownerID     string
}

func NewCoordinator(config Config) (*Coordinator, error) {
	if config.Now == nil {
		config.Now = time.Now
	}
	if config.Concurrency == 0 {
		config.Concurrency = 1
	}
	if err := config.validate(); err != nil {
		return nil, err
	}
	cache, err := newResultCache(config.ResultRoot, config.CompletedTTL, config.CompletedLimit, config.Now)
	if err != nil {
		return nil, err
	}
	policies := make(map[Kind]KindPolicy, len(config.Policies))
	for kind, policy := range config.Policies {
		policies[kind] = policy
	}
	config.Policies = policies
	return &Coordinator{
		config: config, cache: cache, runners: make(map[Kind]Runner),
		jobs: make(map[string]*directionalJob), scienceJobs: make(map[string]*directionalJob),
		idempotency: make(map[string]*directionalJob), ownerJobs: make(map[string]*directionalJob),
		active: make(map[string]*directionalJob), wake: make(chan struct{}, config.Concurrency),
	}, nil
}

// Register installs one provider-neutral runner per calculation kind.
func (coordinator *Coordinator) Register(kind Kind, runner Runner) error {
	if coordinator == nil {
		return errors.New("directional coordinator is nil")
	}
	if !validKind(kind) || runner == nil {
		return errors.New("valid directional kind and runner are required")
	}
	coordinator.mutex.Lock()
	defer coordinator.mutex.Unlock()
	if coordinator.started || coordinator.closed {
		return errors.New("directional runners must be registered before start")
	}
	if _, exists := coordinator.runners[kind]; exists {
		return fmt.Errorf("directional runner for %s is already registered", kind)
	}
	coordinator.runners[kind] = runner
	return nil
}

// Start launches the configured number of workers shared by Horizon and
// Astrodome. The bounded value is common to both kinds.
func (coordinator *Coordinator) Start(parent context.Context) error {
	if coordinator == nil {
		return errors.New("directional coordinator is nil")
	}
	if parent == nil {
		return errors.New("directional coordinator context is required")
	}
	if err := parent.Err(); err != nil {
		return err
	}
	coordinator.mutex.Lock()
	defer coordinator.mutex.Unlock()
	if coordinator.closed {
		return ErrClosed
	}
	if coordinator.started {
		return errors.New("directional coordinator is already started")
	}
	coordinator.ctx, coordinator.cancel = context.WithCancel(parent)
	coordinator.started = true
	coordinator.wait.Add(coordinator.config.Concurrency)
	for range coordinator.config.Concurrency {
		go coordinator.worker()
	}
	return nil
}

// Close cancels the active calculation, rejects pending work, and waits for
// the registered runner to observe its context cancellation.
func (coordinator *Coordinator) Close() error {
	if coordinator == nil {
		return nil
	}
	coordinator.mutex.Lock()
	if coordinator.closed {
		coordinator.mutex.Unlock()
		coordinator.wait.Wait()
		return nil
	}
	coordinator.closed = true
	if coordinator.cancel != nil {
		coordinator.cancel()
	}
	coordinator.cancelPendingLocked(context.Canceled)
	coordinator.mutex.Unlock()
	coordinator.signalWorker()
	coordinator.wait.Wait()
	return nil
}

// Submit applies idempotency and science-key singleflight before consuming a
// FIFO queue slot. Identical science work can have multiple authorized owners.
func (coordinator *Coordinator) Submit(ctx context.Context, request Request) (*Ticket, error) {
	if coordinator == nil {
		return nil, errors.New("directional coordinator is nil")
	}
	if ctx == nil {
		return nil, errors.New("directional submit context is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := validateRequest(request); err != nil {
		return nil, err
	}
	if len(request.Payload) == 0 {
		request.Payload = json.RawMessage("null")
	} else {
		request.Payload = append(json.RawMessage(nil), request.Payload...)
	}
	scienceDigest := digestParts(
		string(request.Kind), request.ScienceCacheKey, request.Source.Provider,
		request.Source.RunID, request.Source.GridProfile, request.Source.GeometryDigest,
	)
	requestFamilyKey := strings.TrimSpace(request.RequestFamilyKey)
	if requestFamilyKey == "" {
		requestFamilyKey = request.ScienceCacheKey
	}
	requestFamilyDigest := digestParts(
		string(request.Kind), requestFamilyKey, request.Source.Provider,
		request.Source.RunID, request.Source.GridProfile, request.Source.GeometryDigest,
	)
	idempotencyDigest := digestParts(request.OwnerID, string(request.Kind), request.IdempotencyKey)
	now := coordinator.config.Now().UTC()

	coordinator.mutex.Lock()
	coordinator.pruneCompletedLocked(now)
	if err := ctx.Err(); err != nil {
		coordinator.mutex.Unlock()
		return nil, err
	}
	if coordinator.closed {
		coordinator.mutex.Unlock()
		return nil, ErrClosed
	}
	if coordinator.ctx != nil && coordinator.ctx.Err() != nil {
		coordinator.mutex.Unlock()
		return nil, ErrClosed
	}
	if !coordinator.started {
		coordinator.mutex.Unlock()
		return nil, ErrNotStarted
	}
	if _, exists := coordinator.runners[request.Kind]; !exists {
		coordinator.mutex.Unlock()
		return nil, ErrUnavailable
	}
	if existing, exists := coordinator.idempotency[idempotencyDigest]; exists {
		if existing.requestFamilyDigest != requestFamilyDigest {
			coordinator.mutex.Unlock()
			return nil, ErrIdempotencyConflict
		}
		ticket := coordinator.addOwnerLocked(existing, request.OwnerID, idempotencyDigest)
		coordinator.mutex.Unlock()
		return ticket, nil
	}
	if existing, exists := coordinator.scienceJobs[scienceDigest]; exists && existing.state != StateFailed && existing.state != StateCancelled {
		if owned, busy := coordinator.ownerJobs[request.OwnerID]; (existing.state == StateQueued || existing.state == StateRunning) && busy && owned != existing {
			coordinator.mutex.Unlock()
			return nil, ErrOwnerBusy
		}
		ticket := coordinator.addOwnerLocked(existing, request.OwnerID, idempotencyDigest)
		coordinator.mutex.Unlock()
		return ticket, nil
	}
	if cached, exists := coordinator.cache.lookup(scienceDigest); exists {
		if !resultMatchesSource(cached, request.Source) {
			coordinator.mutex.Unlock()
			return nil, ErrUnavailable
		}
		jobID, err := newJobID()
		if err != nil {
			coordinator.mutex.Unlock()
			return nil, err
		}
		job := &directionalJob{
			id: jobID, kind: request.Kind, scienceDigest: scienceDigest, requestFamilyDigest: requestFamilyDigest,
			scienceCacheKey: request.ScienceCacheKey, source: request.Source,
			owners: make(map[string]struct{}), idempotencyKeys: make(map[string]string),
			state: StateReady, createdAt: now, updatedAt: now, finishedAt: now,
			result: cached, done: make(chan struct{}),
		}
		job.doneOnce.Do(func() { close(job.done) })
		coordinator.jobs[job.id] = job
		coordinator.scienceJobs[scienceDigest] = job
		coordinator.completed = append(coordinator.completed, job.id)
		ticket := coordinator.addOwnerLocked(job, request.OwnerID, idempotencyDigest)
		coordinator.pruneCompletedLocked(now)
		coordinator.mutex.Unlock()
		return ticket, nil
	}
	if _, busy := coordinator.ownerJobs[request.OwnerID]; busy {
		coordinator.mutex.Unlock()
		return nil, ErrOwnerBusy
	}
	if len(coordinator.queue) >= coordinator.config.QueueCapacity {
		coordinator.mutex.Unlock()
		return nil, ErrQueueFull
	}
	jobID, err := newJobID()
	if err != nil {
		coordinator.mutex.Unlock()
		return nil, err
	}
	job := &directionalJob{
		id: jobID, kind: request.Kind, scienceDigest: scienceDigest, requestFamilyDigest: requestFamilyDigest,
		scienceCacheKey: request.ScienceCacheKey, source: request.Source,
		payload: append(json.RawMessage(nil), request.Payload...),
		owners:  make(map[string]struct{}), idempotencyKeys: make(map[string]string),
		state: StateQueued, createdAt: now, updatedAt: now, done: make(chan struct{}),
	}
	coordinator.jobs[job.id] = job
	coordinator.scienceJobs[scienceDigest] = job
	coordinator.queue = append(coordinator.queue, job)
	ticket := coordinator.addOwnerLocked(job, request.OwnerID, idempotencyDigest)
	coordinator.mutex.Unlock()
	coordinator.signalWorker()
	return ticket, nil
}

func (coordinator *Coordinator) Status(ownerID, jobID string) (JobStatus, error) {
	if coordinator == nil || ownerID == "" || jobID == "" {
		return JobStatus{}, ErrNotFound
	}
	now := coordinator.config.Now().UTC()
	coordinator.mutex.Lock()
	defer coordinator.mutex.Unlock()
	coordinator.pruneCompletedLocked(now)
	job, exists := coordinator.jobs[jobID]
	if !exists || !job.hasOwner(ownerID) {
		return JobStatus{}, ErrNotFound
	}
	return coordinator.statusLocked(job, now), nil
}

func (coordinator *Coordinator) Cancel(ownerID, jobID string) error {
	if coordinator == nil || ownerID == "" || jobID == "" {
		return ErrNotFound
	}
	now := coordinator.config.Now().UTC()
	coordinator.mutex.Lock()
	defer coordinator.mutex.Unlock()
	coordinator.pruneCompletedLocked(now)
	job, exists := coordinator.jobs[jobID]
	if !exists || !job.hasOwner(ownerID) {
		return ErrNotFound
	}
	coordinator.removeOwnerLocked(job, ownerID)
	if len(job.owners) > 0 {
		return nil
	}
	if coordinator.scienceJobs[job.scienceDigest] == job {
		delete(coordinator.scienceJobs, job.scienceDigest)
	}
	switch job.state {
	case StateQueued:
		coordinator.removeQueuedLocked(job)
		coordinator.finishJobLocked(job, StateCancelled, Result{}, "cancelled", context.Canceled, now)
	case StateRunning:
		if job.runCancel != nil {
			job.runCancel()
		}
		coordinator.finishJobLocked(job, StateCancelled, Result{}, "cancelled", context.Canceled, now)
	}
	return nil
}

func (coordinator *Coordinator) OpenResult(ownerID, jobID string) (*os.File, Result, error) {
	if coordinator == nil || ownerID == "" || jobID == "" {
		return nil, Result{}, ErrNotFound
	}
	now := coordinator.config.Now().UTC()
	coordinator.mutex.Lock()
	coordinator.pruneCompletedLocked(now)
	job, exists := coordinator.jobs[jobID]
	if !exists || !job.hasOwner(ownerID) || job.state != StateReady {
		coordinator.mutex.Unlock()
		return nil, Result{}, ErrNotFound
	}
	scienceDigest := job.publishedDigest
	if scienceDigest == "" {
		scienceDigest = job.scienceDigest
	}
	coordinator.mutex.Unlock()
	return coordinator.cache.open(scienceDigest)
}

func (coordinator *Coordinator) Stats() QueueStats {
	if coordinator == nil {
		return QueueStats{}
	}
	coordinator.mutex.Lock()
	defer coordinator.mutex.Unlock()
	return QueueStats{QueueLength: len(coordinator.queue), Running: len(coordinator.active) > 0}
}

func (ticket *Ticket) ID() string {
	if ticket == nil {
		return ""
	}
	return ticket.jobID
}

func (ticket *Ticket) Status() (JobStatus, error) {
	if ticket == nil || ticket.coordinator == nil {
		return JobStatus{}, ErrNotFound
	}
	return ticket.coordinator.Status(ticket.ownerID, ticket.jobID)
}

func (ticket *Ticket) Cancel() error {
	if ticket == nil || ticket.coordinator == nil {
		return ErrNotFound
	}
	return ticket.coordinator.Cancel(ticket.ownerID, ticket.jobID)
}

func (ticket *Ticket) Wait(ctx context.Context) (Result, error) {
	if ticket == nil || ticket.coordinator == nil || ctx == nil {
		return Result{}, ErrNotFound
	}
	coordinator := ticket.coordinator
	coordinator.mutex.Lock()
	job, exists := coordinator.jobs[ticket.jobID]
	if !exists || !job.hasOwner(ticket.ownerID) {
		coordinator.mutex.Unlock()
		return Result{}, ErrNotFound
	}
	done := job.done
	coordinator.mutex.Unlock()
	select {
	case <-ctx.Done():
		return Result{}, ctx.Err()
	case <-done:
	}
	coordinator.mutex.Lock()
	defer coordinator.mutex.Unlock()
	if !job.hasOwner(ticket.ownerID) {
		return Result{}, ErrNotFound
	}
	switch job.state {
	case StateReady:
		return job.result, nil
	case StateCancelled:
		return Result{}, context.Canceled
	default:
		if job.err != nil {
			return Result{}, job.err
		}
		return Result{}, errors.New("directional calculation failed")
	}
}

func (coordinator *Coordinator) worker() {
	defer coordinator.wait.Done()
	for {
		job, ctx, runner := coordinator.nextJob()
		if job == nil {
			return
		}
		coordinator.execute(ctx, runner, job)
	}
}

func (coordinator *Coordinator) nextJob() (*directionalJob, context.Context, Runner) {
	for {
		coordinator.mutex.Lock()
		if coordinator.ctx.Err() != nil || coordinator.closed {
			coordinator.closed = true
			coordinator.cancelPendingLocked(context.Canceled)
			coordinator.mutex.Unlock()
			return nil, nil, nil
		}
		if len(coordinator.queue) > 0 {
			job := coordinator.queue[0]
			coordinator.queue = coordinator.queue[1:]
			policy := coordinator.config.Policies[job.kind]
			runCtx, cancel := contextWithOptionalTimeout(coordinator.ctx, policy.Timeout)
			now := coordinator.config.Now().UTC()
			job.state, job.startedAt, job.updatedAt, job.runCancel = StateRunning, now, now, cancel
			coordinator.active[job.id] = job
			runner := coordinator.runners[job.kind]
			coordinator.mutex.Unlock()
			return job, runCtx, runner
		}
		coordinator.mutex.Unlock()
		select {
		case <-coordinator.ctx.Done():
		case <-coordinator.wake:
		}
	}
}

func contextWithOptionalTimeout(parent context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if timeout > 0 {
		return context.WithTimeout(parent, timeout)
	}
	return context.WithCancel(parent)
}

func (coordinator *Coordinator) execute(ctx context.Context, runner Runner, job *directionalJob) {
	workspace, err := coordinator.cache.newWorkspace(job.id)
	var runnerResult RunnerResult
	if err == nil {
		runnerResult, err = runner.Run(ctx, Execution{
			JobID: job.id, Kind: job.kind, ScienceCacheKey: job.scienceCacheKey, Source: job.source,
			Payload: append(json.RawMessage(nil), job.payload...), Workspace: workspace,
		})
	}
	var result Result
	publicationDigest := job.scienceDigest
	finalScienceCacheKey := job.scienceCacheKey
	if err == nil {
		if candidate := strings.TrimSpace(runnerResult.FinalScienceCacheKey); candidate != "" {
			finalScienceCacheKey = candidate
		}
		if !runnerResultMatchesSource(runnerResult, job.source) {
			err = CodedError{Code: "source_mismatch", Err: errors.New("runner result differs from pinned source")}
		} else if len(finalScienceCacheKey) > 4096 {
			err = CodedError{Code: "science_identity_mismatch", Err: errors.New("runner did not return a valid final science cache identity")}
		} else if contextErr := ctx.Err(); contextErr != nil {
			err = contextErr
		} else {
			publicationDigest = digestParts(
				string(job.kind), finalScienceCacheKey, job.source.Provider,
				job.source.RunID, job.source.GridProfile, job.source.GeometryDigest,
			)
			result, err = coordinator.cache.publish(ctx, job.kind, publicationDigest, workspace, runnerResult)
		}
	}
	if workspace != "" {
		_ = os.RemoveAll(workspace)
	}
	now := coordinator.config.Now().UTC()
	coordinator.mutex.Lock()
	delete(coordinator.active, job.id)
	if job.runCancel != nil {
		job.runCancel()
		job.runCancel = nil
	}
	if !job.terminal() {
		if err == nil {
			job.result = result
			if coordinator.scienceJobs[job.scienceDigest] == job {
				delete(coordinator.scienceJobs, job.scienceDigest)
			}
			job.publishedDigest = publicationDigest
			job.scienceCacheKey = finalScienceCacheKey
			if _, exists := coordinator.scienceJobs[publicationDigest]; !exists {
				coordinator.scienceJobs[publicationDigest] = job
			}
			coordinator.finishJobLocked(job, StateReady, result, "", nil, now)
		} else {
			code := failureCode(err)
			state := StateFailed
			if errors.Is(err, context.Canceled) {
				state, code = StateCancelled, "cancelled"
			}
			coordinator.finishJobLocked(job, state, Result{}, code, err, now)
			if coordinator.scienceJobs[job.scienceDigest] == job {
				delete(coordinator.scienceJobs, job.scienceDigest)
			}
		}
	}
	coordinator.mutex.Unlock()
	coordinator.signalWorker()
}

func (coordinator *Coordinator) addOwnerLocked(job *directionalJob, ownerID, idempotencyDigest string) *Ticket {
	job.owners[ownerID] = struct{}{}
	if job.state == StateQueued || job.state == StateRunning {
		coordinator.ownerJobs[ownerID] = job
	}
	job.idempotencyKeys[idempotencyDigest] = ownerID
	coordinator.idempotency[idempotencyDigest] = job
	return &Ticket{coordinator: coordinator, jobID: job.id, ownerID: ownerID}
}

func (coordinator *Coordinator) removeOwnerLocked(job *directionalJob, ownerID string) {
	delete(job.owners, ownerID)
	if coordinator.ownerJobs[ownerID] == job {
		delete(coordinator.ownerJobs, ownerID)
	}
	for digest, owner := range job.idempotencyKeys {
		if owner == ownerID {
			delete(job.idempotencyKeys, digest)
			if coordinator.idempotency[digest] == job {
				delete(coordinator.idempotency, digest)
			}
		}
	}
}

func (coordinator *Coordinator) statusLocked(job *directionalJob, now time.Time) JobStatus {
	status := JobStatus{
		ID: job.id, Kind: job.kind, State: job.state,
		CreatedAt: job.createdAt, UpdatedAt: job.updatedAt, FailureCode: job.failureCode,
		Provider: job.source.Provider, RunID: job.source.RunID,
		GridProfile: job.source.GridProfile, GeometryDigest: job.source.GeometryDigest,
	}
	switch job.state {
	case StateQueued:
		position, estimate := coordinator.queuePositionAndEstimateLocked(job, now)
		status.QueuePosition, status.EstimatedAt = &position, &estimate
	case StateRunning:
		estimate := job.startedAt.Add(coordinator.config.Policies[job.kind].Estimate)
		if estimate.Before(now) {
			estimate = now
		}
		status.EstimatedAt = &estimate
	case StateReady:
		status.DatasetBytes = job.result.Bytes
	}
	return status
}

func (coordinator *Coordinator) queuePositionAndEstimateLocked(target *directionalJob, now time.Time) (int, time.Time) {
	slotReady := make([]time.Time, coordinator.config.Concurrency)
	activeIndex := 0
	for _, active := range coordinator.active {
		ready := active.startedAt.Add(coordinator.config.Policies[active.kind].Estimate)
		if ready.Before(now) {
			ready = now
		}
		slotReady[activeIndex] = ready
		activeIndex++
	}
	for activeIndex < len(slotReady) {
		slotReady[activeIndex] = now
		activeIndex++
	}
	sort.Slice(slotReady, func(left, right int) bool { return slotReady[left].Before(slotReady[right]) })
	position := 0
	for _, queued := range coordinator.queue {
		position++
		estimatedAt := slotReady[0].Add(coordinator.config.Policies[queued.kind].Estimate)
		slotReady[0] = estimatedAt
		sort.Slice(slotReady, func(left, right int) bool { return slotReady[left].Before(slotReady[right]) })
		if queued == target {
			return position, estimatedAt
		}
	}
	return 0, now
}

func (coordinator *Coordinator) finishJobLocked(job *directionalJob, state State, result Result, code string, err error, now time.Time) {
	if job.terminal() {
		return
	}
	job.state, job.result, job.failureCode, job.err = state, result, code, err
	job.updatedAt, job.finishedAt = now, now
	job.doneOnce.Do(func() { close(job.done) })
	for ownerID := range job.owners {
		if coordinator.ownerJobs[ownerID] == job {
			delete(coordinator.ownerJobs, ownerID)
		}
	}
	coordinator.completed = append(coordinator.completed, job.id)
	coordinator.pruneCompletedLocked(now)
}

func (coordinator *Coordinator) cancelPendingLocked(err error) {
	now := coordinator.config.Now().UTC()
	for _, job := range coordinator.queue {
		if coordinator.scienceJobs[job.scienceDigest] == job {
			delete(coordinator.scienceJobs, job.scienceDigest)
		}
		coordinator.finishJobLocked(job, StateCancelled, Result{}, "cancelled", err, now)
	}
	coordinator.queue = nil
	for _, active := range coordinator.active {
		if active.runCancel != nil {
			active.runCancel()
		}
	}
}

func (coordinator *Coordinator) removeQueuedLocked(target *directionalJob) {
	for index, job := range coordinator.queue {
		if job == target {
			copy(coordinator.queue[index:], coordinator.queue[index+1:])
			coordinator.queue[len(coordinator.queue)-1] = nil
			coordinator.queue = coordinator.queue[:len(coordinator.queue)-1]
			return
		}
	}
}

func (coordinator *Coordinator) pruneCompletedLocked(now time.Time) {
	kept := coordinator.completed[:0]
	for _, jobID := range coordinator.completed {
		job, exists := coordinator.jobs[jobID]
		if !exists || !job.terminal() {
			continue
		}
		if !job.finishedAt.Add(coordinator.config.CompletedTTL).After(now) {
			coordinator.removeCompletedLocked(job)
			continue
		}
		kept = append(kept, jobID)
	}
	coordinator.completed = kept
	for len(coordinator.completed) > coordinator.config.CompletedLimit {
		jobID := coordinator.completed[0]
		coordinator.completed = coordinator.completed[1:]
		if job, exists := coordinator.jobs[jobID]; exists {
			coordinator.removeCompletedLocked(job)
		}
	}
}

func (coordinator *Coordinator) removeCompletedLocked(job *directionalJob) {
	delete(coordinator.jobs, job.id)
	if coordinator.scienceJobs[job.scienceDigest] == job {
		delete(coordinator.scienceJobs, job.scienceDigest)
	}
	if job.publishedDigest != "" && coordinator.scienceJobs[job.publishedDigest] == job {
		delete(coordinator.scienceJobs, job.publishedDigest)
	}
	for digest := range job.idempotencyKeys {
		if coordinator.idempotency[digest] == job {
			delete(coordinator.idempotency, digest)
		}
	}
}

func (coordinator *Coordinator) signalWorker() {
	select {
	case coordinator.wake <- struct{}{}:
	default:
	}
}

func (job *directionalJob) hasOwner(ownerID string) bool {
	_, exists := job.owners[ownerID]
	return exists
}

func (job *directionalJob) terminal() bool {
	return job.state == StateReady || job.state == StateFailed || job.state == StateCancelled
}

func validateRequest(request Request) error {
	if !validKind(request.Kind) {
		return errors.New("directional request kind is invalid")
	}
	if strings.TrimSpace(request.OwnerID) == "" || len(request.OwnerID) > 256 {
		return errors.New("directional owner ID is invalid")
	}
	if request.IdempotencyKey == "" || len(request.IdempotencyKey) > 512 {
		return errors.New("directional idempotency key is invalid")
	}
	if request.ScienceCacheKey == "" || len(request.ScienceCacheKey) > 4096 {
		return errors.New("directional science cache key is invalid")
	}
	if len(request.RequestFamilyKey) > 4096 {
		return errors.New("directional request family key is invalid")
	}
	for _, value := range []string{request.Source.Provider, request.Source.RunID, request.Source.GridProfile, request.Source.GeometryDigest} {
		if strings.TrimSpace(value) == "" || len(value) > 4096 {
			return errors.New("directional pinned source identity is invalid")
		}
	}
	if len(request.Payload) == 0 {
		request.Payload = json.RawMessage("null")
	}
	if !json.Valid(request.Payload) {
		return errors.New("directional payload must be valid JSON")
	}
	return nil
}

func runnerResultMatchesSource(result RunnerResult, source SourceIdentity) bool {
	return result.Provider == source.Provider && result.RunID == source.RunID &&
		result.GridProfile == source.GridProfile && result.GeometryDigest == source.GeometryDigest
}

func resultMatchesSource(result Result, source SourceIdentity) bool {
	return result.Provider == source.Provider && result.RunID == source.RunID &&
		result.GridProfile == source.GridProfile && result.GeometryDigest == source.GeometryDigest
}

func digestParts(parts ...string) string {
	hash := sha256.New()
	var length [8]byte
	for _, part := range parts {
		binary.BigEndian.PutUint64(length[:], uint64(len(part)))
		_, _ = hash.Write(length[:])
		_, _ = hash.Write([]byte(part))
	}
	return fmt.Sprintf("%x", hash.Sum(nil))
}

func newJobID() (string, error) {
	buffer := make([]byte, 24)
	if _, err := rand.Read(buffer); err != nil {
		return "", fmt.Errorf("generate directional job ID: %w", err)
	}
	return "job_" + base64.RawURLEncoding.EncodeToString(buffer), nil
}

var failureCodePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

func failureCode(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	var coded FailureCoder
	if errors.As(err, &coded) && failureCodePattern.MatchString(coded.FailureCode()) {
		return coded.FailureCode()
	}
	return "calculation_failed"
}
