package bot

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"bot_astrosferum/internal/app/directional"
	"bot_astrosferum/internal/forecast"
)

const (
	accountResultManifestVersion = 1
	accountResultFileLimit       = 64 << 20
	accountResultTotalLimit      = 192 << 20
)

var (
	accountJobIDPattern    = regexp.MustCompile(`^[a-f0-9]{32}$`)
	accountFileNamePattern = regexp.MustCompile(`^(forecast|horizon)\.json$`)
)

type accountResultJob struct {
	owner          int64
	idempotencyKey string
	requestDigest  [sha256.Size]byte
	status         directional.AccountJobStatus
	cancel         context.CancelFunc
	capture        *accountResultCapture
	cancelled      bool
}

type accountResultManifest struct {
	Version int                          `json:"version"`
	Owner   int64                        `json:"owner"`
	Status  directional.AccountJobStatus `json:"status"`
}

// AccountResultDispatcher exposes the existing forecast and Horizon renderers
// as owner-scoped website jobs. It never sends a platform message and never
// duplicates model acquisition or scientific calculations.
type AccountResultDispatcher struct {
	root            context.Context
	forecastTimeout time.Duration
	horizonTimeout  time.Duration
	resultTTL       time.Duration
	resultRoot      string
	logf            func(string, ...any)
	forecastSlots   chan struct{}
	horizonSlots    chan struct{}

	mu          sync.Mutex
	handler     *Handler
	jobs        map[string]*accountResultJob
	active      map[string]string
	idempotency map[string]string
}

func NewAccountResultDispatcher(
	root context.Context,
	forecastTimeout, horizonTimeout, resultTTL time.Duration,
	resultRoot string,
	forecastCapacity, horizonCapacity int,
	logf func(string, ...any),
) (*AccountResultDispatcher, error) {
	if root == nil {
		return nil, errors.New("account result root context is required")
	}
	if forecastTimeout <= 0 || horizonTimeout < 0 || resultTTL <= 0 {
		return nil, errors.New("account result forecast/retention durations must be positive and Horizon duration non-negative")
	}
	if strings.TrimSpace(resultRoot) == "" {
		return nil, errors.New("account result root is required")
	}
	if forecastCapacity < 1 || horizonCapacity < 1 {
		return nil, errors.New("account result capacities must be positive")
	}
	if err := os.MkdirAll(resultRoot, 0o750); err != nil {
		return nil, fmt.Errorf("create account result root: %w", err)
	}
	if logf == nil {
		logf = func(string, ...any) {}
	}
	dispatcher := &AccountResultDispatcher{
		root: root, forecastTimeout: forecastTimeout, horizonTimeout: horizonTimeout,
		resultTTL: resultTTL, resultRoot: resultRoot, logf: logf,
		forecastSlots: make(chan struct{}, forecastCapacity), horizonSlots: make(chan struct{}, horizonCapacity),
		jobs: make(map[string]*accountResultJob), active: make(map[string]string), idempotency: make(map[string]string),
	}
	dispatcher.prune(time.Now())
	go dispatcher.maintain()
	return dispatcher, nil
}

// SetHandler installs one platform-independent, fully configured calculation
// handler. Per-job messenger facades capture files instead of contacting a bot.
func (dispatcher *AccountResultDispatcher) SetHandler(handler *Handler) error {
	if handler == nil {
		return errors.New("account result handler is required")
	}
	dispatcher.mu.Lock()
	defer dispatcher.mu.Unlock()
	if dispatcher.handler != nil {
		return errors.New("account result handler is already configured")
	}
	dispatcher.handler = handler
	return nil
}

func (dispatcher *AccountResultDispatcher) AdmitAccountJob(
	_ context.Context,
	kind directional.AccountJobKind,
	admission directional.AccountJobAdmission,
) (directional.AccountJobStatus, error) {
	if err := admission.Validate(); err != nil {
		return directional.AccountJobStatus{}, err
	}
	slots, err := dispatcher.slots(kind)
	if err != nil {
		return directional.AccountJobStatus{}, err
	}
	dispatcher.mu.Lock()
	if dispatcher.root.Err() != nil || dispatcher.handler == nil {
		dispatcher.mu.Unlock()
		return directional.AccountJobStatus{}, directional.ErrAccountUnavailable
	}
	activeKey := fmt.Sprintf("%s:%d", kind, admission.TelegramUserID)
	idempotencyKey := activeKey + ":" + admission.IdempotencyKey
	requestDigest := accountAdmissionDigest(kind, admission)
	if existingID := dispatcher.idempotency[idempotencyKey]; existingID != "" {
		if existing := dispatcher.jobs[existingID]; existing != nil {
			if existing.requestDigest != requestDigest {
				dispatcher.mu.Unlock()
				return directional.AccountJobStatus{}, directional.ErrIdempotencyConflict
			}
			status := cloneAccountJobStatus(existing.status)
			dispatcher.mu.Unlock()
			return status, nil
		}
		delete(dispatcher.idempotency, idempotencyKey)
	}
	if _, exists := dispatcher.active[activeKey]; exists {
		dispatcher.mu.Unlock()
		return directional.AccountJobStatus{}, directional.ErrOwnerBusy
	}
	select {
	case slots <- struct{}{}:
	default:
		dispatcher.mu.Unlock()
		return directional.AccountJobStatus{}, directional.ErrQueueFull
	}
	jobID, err := newAccountJobID()
	if err != nil {
		dispatcher.mu.Unlock()
		<-slots
		return directional.AccountJobStatus{}, directional.ErrAccountUnavailable
	}
	now := time.Now().UTC()
	operationContext, cancel := dispatcher.operationContext(kind)
	job := &accountResultJob{
		owner: admission.TelegramUserID, idempotencyKey: idempotencyKey, requestDigest: requestDigest, cancel: cancel,
		status: directional.AccountJobStatus{
			ID: jobID, Kind: kind, State: directional.StateQueued, CreatedAt: now, UpdatedAt: now,
		},
	}
	dispatcher.jobs[jobID] = job
	dispatcher.active[activeKey] = jobID
	dispatcher.idempotency[idempotencyKey] = jobID
	handler := dispatcher.handler
	dispatcher.mu.Unlock()
	if err := dispatcher.persist(job.owner, cloneAccountJobStatus(job.status)); err != nil {
		cancel()
		dispatcher.release(jobID, activeKey, slots)
		dispatcher.mu.Lock()
		dispatcher.removeJobLocked(jobID, job)
		dispatcher.mu.Unlock()
		_ = os.RemoveAll(filepath.Join(dispatcher.resultRoot, jobID))
		return directional.AccountJobStatus{}, directional.ErrAccountUnavailable
	}
	initial := cloneAccountJobStatus(job.status)
	go dispatcher.run(operationContext, jobID, activeKey, slots, handler, admission)
	return initial, nil
}

func (dispatcher *AccountResultDispatcher) operationContext(kind directional.AccountJobKind) (context.Context, context.CancelFunc) {
	timeout := dispatcher.forecastTimeout
	if kind == directional.AccountJobHorizon {
		timeout = dispatcher.horizonTimeout
	}
	if timeout == 0 {
		return context.WithCancel(dispatcher.root)
	}
	return context.WithTimeout(dispatcher.root, timeout)
}

func (dispatcher *AccountResultDispatcher) AccountJobStatus(_ context.Context, owner int64, jobID string) (directional.AccountJobStatus, error) {
	if owner <= 0 || !accountJobIDPattern.MatchString(jobID) {
		return directional.AccountJobStatus{}, directional.ErrNotFound
	}
	dispatcher.mu.Lock()
	if job, ok := dispatcher.jobs[jobID]; ok {
		if terminalAccountJobState(job.status.State) && time.Since(job.status.UpdatedAt) > dispatcher.resultTTL {
			dispatcher.removeJobLocked(jobID, job)
			dispatcher.mu.Unlock()
			_ = os.RemoveAll(filepath.Join(dispatcher.resultRoot, jobID))
			return directional.AccountJobStatus{}, directional.ErrNotFound
		}
		status := cloneAccountJobStatus(job.status)
		jobOwner := job.owner
		dispatcher.mu.Unlock()
		if jobOwner != owner {
			return directional.AccountJobStatus{}, directional.ErrNotFound
		}
		return status, nil
	}
	dispatcher.mu.Unlock()
	manifest, err := dispatcher.loadManifest(jobID)
	if err != nil || manifest.Owner != owner {
		return directional.AccountJobStatus{}, directional.ErrNotFound
	}
	return cloneAccountJobStatus(manifest.Status), nil
}

func (dispatcher *AccountResultDispatcher) OpenAccountJobFile(
	ctx context.Context,
	owner int64,
	jobID, fileName string,
) (directional.AccountJobOutput, error) {
	if err := ctx.Err(); err != nil {
		return directional.AccountJobOutput{}, err
	}
	if !accountFileNamePattern.MatchString(fileName) {
		return directional.AccountJobOutput{}, directional.ErrNotFound
	}
	status, err := dispatcher.AccountJobStatus(ctx, owner, jobID)
	if err != nil || status.State != directional.StateReady {
		return directional.AccountJobOutput{}, directional.ErrNotFound
	}
	var descriptor directional.AccountJobFile
	for _, candidate := range status.Files {
		if candidate.Name == fileName {
			descriptor = candidate
			break
		}
	}
	if descriptor.Name == "" {
		return directional.AccountJobOutput{}, directional.ErrNotFound
	}
	path := filepath.Join(dispatcher.resultRoot, jobID, fileName)
	file, err := os.Open(path)
	if err != nil {
		return directional.AccountJobOutput{}, directional.ErrNotFound
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() != descriptor.Bytes {
		_ = file.Close()
		return directional.AccountJobOutput{}, directional.ErrNotFound
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil || `"`+hex.EncodeToString(hash.Sum(nil))+`"` != descriptor.ETag {
		_ = file.Close()
		return directional.AccountJobOutput{}, directional.ErrNotFound
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		_ = file.Close()
		return directional.AccountJobOutput{}, directional.ErrNotFound
	}
	return directional.AccountJobOutput{Body: file, Bytes: descriptor.Bytes, MediaType: descriptor.MediaType, ETag: descriptor.ETag}, nil
}

func (dispatcher *AccountResultDispatcher) CancelAccountJob(_ context.Context, owner int64, jobID string) error {
	if owner <= 0 || !accountJobIDPattern.MatchString(jobID) {
		return directional.ErrNotFound
	}
	dispatcher.mu.Lock()
	job, ok := dispatcher.jobs[jobID]
	if !ok || job.owner != owner {
		dispatcher.mu.Unlock()
		return directional.ErrNotFound
	}
	if job.status.State == directional.StateReady || job.status.State == directional.StateFailed || job.status.State == directional.StateCancelled {
		dispatcher.mu.Unlock()
		return nil
	}
	cancel := job.cancel
	job.cancelled = true
	kind := job.status.Kind
	handler := dispatcher.handler
	if job.capture != nil {
		job.capture.cancel()
	}
	dispatcher.mu.Unlock()
	cancel()
	if kind == directional.AccountJobHorizon && handler != nil {
		handler.CancelHorizon(owner)
	}
	return nil
}

func (dispatcher *AccountResultDispatcher) run(
	ctx context.Context,
	jobID, activeKey string,
	slots chan struct{},
	handler *Handler,
	admission directional.AccountJobAdmission,
) {
	defer dispatcher.release(jobID, activeKey, slots)
	dispatcher.update(jobID, func(status *directional.AccountJobStatus) {
		status.State = directional.StateRunning
		status.UpdatedAt = time.Now().UTC()
	})
	outputRoot := filepath.Join(dispatcher.resultRoot, jobID)
	if err := os.MkdirAll(outputRoot, 0o750); err != nil {
		dispatcher.fail(jobID, "storage_unavailable")
		return
	}
	capture := newAccountResultCapture(outputRoot, func(message string) {
		dispatcher.update(jobID, func(status *directional.AccountJobStatus) {
			status.Summary = message
			status.UpdatedAt = time.Now().UTC()
		})
	})
	dispatcher.mu.Lock()
	if job := dispatcher.jobs[jobID]; job != nil {
		job.capture = capture
	}
	dispatcher.mu.Unlock()
	jobHandler := handler.forMessenger(capture)
	var err error
	switch dispatcher.kind(jobID) {
	case directional.AccountJobForecast:
		err = jobHandler.replyToLocation(ctx, admission.TelegramUserID, admission.TelegramUserID, admission.Latitude, admission.Longitude, languageFromCode(admission.Language))
	case directional.AccountJobHorizon:
		err = jobHandler.DeliverHorizon(ctx, admission.TelegramUserID, admission.Latitude, admission.Longitude, admission.Language)
		if err == nil {
			err = capture.waitHorizon(ctx)
		}
	default:
		err = errors.New("unsupported account result kind")
	}
	if captureErr := capture.err(); captureErr != nil {
		err = captureErr
	}
	if err != nil {
		if dispatcher.kind(jobID) == directional.AccountJobHorizon && ctx.Err() != nil {
			jobHandler.CancelHorizon(admission.TelegramUserID)
		}
		dispatcher.removePartialFiles(outputRoot)
		failure := "calculation_failed"
		switch {
		case errors.Is(ctx.Err(), context.Canceled):
			failure = "cancelled"
		case errors.Is(ctx.Err(), context.DeadlineExceeded):
			failure = "timeout"
		}
		if failure == "cancelled" {
			dispatcher.update(jobID, func(status *directional.AccountJobStatus) {
				status.State, status.FailureCode, status.UpdatedAt = directional.StateCancelled, failure, time.Now().UTC()
			})
		} else {
			dispatcher.fail(jobID, failure)
		}
		dispatcher.logf("account result job %s failed: %v", jobID, err)
		return
	}
	files := capture.files()
	if len(files) == 0 {
		dispatcher.removePartialFiles(outputRoot)
		dispatcher.fail(jobID, "empty_result")
		return
	}
	if err := ctx.Err(); err != nil {
		dispatcher.removePartialFiles(outputRoot)
		dispatcher.update(jobID, func(status *directional.AccountJobStatus) {
			status.State, status.FailureCode, status.UpdatedAt = directional.StateCancelled, "cancelled", time.Now().UTC()
		})
		return
	}
	if err := dispatcher.publishReady(jobID, files); err != nil {
		dispatcher.removePartialFiles(outputRoot)
		if errors.Is(err, context.Canceled) {
			dispatcher.update(jobID, func(status *directional.AccountJobStatus) {
				status.State, status.FailureCode, status.UpdatedAt = directional.StateCancelled, "cancelled", time.Now().UTC()
			})
		} else {
			dispatcher.fail(jobID, "storage_unavailable")
		}
		return
	}
	dispatcher.prune(time.Now())
}

func (dispatcher *AccountResultDispatcher) publishReady(jobID string, files []directional.AccountJobFile) error {
	dispatcher.mu.Lock()
	job := dispatcher.jobs[jobID]
	if job == nil || terminalAccountJobState(job.status.State) {
		dispatcher.mu.Unlock()
		return directional.ErrNotFound
	}
	if job.cancelled {
		dispatcher.mu.Unlock()
		return context.Canceled
	}
	candidate := cloneAccountJobStatus(job.status)
	candidate.State, candidate.Files, candidate.UpdatedAt = directional.StateReady, append([]directional.AccountJobFile(nil), files...), time.Now().UTC()
	candidate.FailureCode = ""
	owner := job.owner
	dispatcher.mu.Unlock()
	if err := dispatcher.persist(owner, candidate); err != nil {
		return err
	}
	dispatcher.mu.Lock()
	defer dispatcher.mu.Unlock()
	job = dispatcher.jobs[jobID]
	if job == nil || terminalAccountJobState(job.status.State) {
		return directional.ErrNotFound
	}
	if job.cancelled {
		return context.Canceled
	}
	job.status = candidate
	return nil
}

func (dispatcher *AccountResultDispatcher) kind(jobID string) directional.AccountJobKind {
	dispatcher.mu.Lock()
	defer dispatcher.mu.Unlock()
	if job := dispatcher.jobs[jobID]; job != nil {
		return job.status.Kind
	}
	return ""
}

func (dispatcher *AccountResultDispatcher) fail(jobID, code string) {
	dispatcher.update(jobID, func(status *directional.AccountJobStatus) {
		status.State, status.FailureCode, status.UpdatedAt = directional.StateFailed, code, time.Now().UTC()
	})
}

func (dispatcher *AccountResultDispatcher) update(jobID string, change func(*directional.AccountJobStatus)) {
	dispatcher.mu.Lock()
	job := dispatcher.jobs[jobID]
	var owner int64
	var status directional.AccountJobStatus
	if job != nil {
		change(&job.status)
		owner, status = job.owner, cloneAccountJobStatus(job.status)
	}
	dispatcher.mu.Unlock()
	if job != nil {
		if err := dispatcher.persist(owner, status); err != nil {
			dispatcher.logf("persist account result job %s: %v", jobID, err)
		}
	}
}

func (dispatcher *AccountResultDispatcher) release(jobID, activeKey string, slots chan struct{}) {
	dispatcher.mu.Lock()
	if dispatcher.active[activeKey] == jobID {
		delete(dispatcher.active, activeKey)
	}
	if job := dispatcher.jobs[jobID]; job != nil {
		job.cancel = func() {}
	}
	dispatcher.mu.Unlock()
	<-slots
}

func (dispatcher *AccountResultDispatcher) slots(kind directional.AccountJobKind) (chan struct{}, error) {
	switch kind {
	case directional.AccountJobForecast:
		return dispatcher.forecastSlots, nil
	case directional.AccountJobHorizon:
		return dispatcher.horizonSlots, nil
	default:
		return nil, errors.New("unsupported account result kind")
	}
}

func (dispatcher *AccountResultDispatcher) persist(owner int64, status directional.AccountJobStatus) error {
	directory := filepath.Join(dispatcher.resultRoot, status.ID)
	if err := os.MkdirAll(directory, 0o750); err != nil {
		return err
	}
	manifest := accountResultManifest{Version: accountResultManifestVersion, Owner: owner, Status: cloneAccountJobStatus(status)}
	encoded, err := json.Marshal(manifest)
	if err != nil {
		return err
	}
	temporary, err := os.CreateTemp(directory, ".manifest-*.json")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer func() { _ = os.Remove(temporaryPath) }()
	if err := temporary.Chmod(0o640); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(encoded); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, filepath.Join(directory, "manifest.json")); err != nil {
		return err
	}
	directoryHandle, err := os.Open(directory)
	if err != nil {
		return err
	}
	syncErr := directoryHandle.Sync()
	closeErr := directoryHandle.Close()
	if syncErr != nil {
		return syncErr
	}
	return closeErr
}

func (dispatcher *AccountResultDispatcher) loadManifest(jobID string) (accountResultManifest, error) {
	var manifest accountResultManifest
	data, err := os.ReadFile(filepath.Join(dispatcher.resultRoot, jobID, "manifest.json"))
	if err != nil || len(data) > 1<<20 {
		return manifest, directional.ErrNotFound
	}
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil || manifest.Version != accountResultManifestVersion || manifest.Status.ID != jobID {
		return accountResultManifest{}, directional.ErrNotFound
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) || !validAccountResultManifest(manifest) {
		return accountResultManifest{}, directional.ErrNotFound
	}
	if time.Since(manifest.Status.UpdatedAt) > dispatcher.resultTTL ||
		(manifest.Status.State != directional.StateReady && manifest.Status.State != directional.StateFailed && manifest.Status.State != directional.StateCancelled) {
		return accountResultManifest{}, directional.ErrNotFound
	}
	return manifest, nil
}

func validAccountResultManifest(manifest accountResultManifest) bool {
	status := manifest.Status
	if manifest.Owner <= 0 || status.CreatedAt.IsZero() || status.UpdatedAt.Before(status.CreatedAt) ||
		(status.Kind != directional.AccountJobForecast && status.Kind != directional.AccountJobHorizon) ||
		len(status.Summary) > 32<<10 || len(status.FailureCode) > 64 {
		return false
	}
	seen := make(map[string]struct{}, len(status.Files))
	var totalBytes int64
	for _, file := range status.Files {
		if !accountFileNamePattern.MatchString(file.Name) || file.MediaType != "application/json" || file.Bytes <= 0 ||
			file.Bytes > accountResultFileLimit || len(file.Caption) > 16<<10 || !validAccountResultETag(file.ETag) ||
			totalBytes > accountResultTotalLimit-file.Bytes {
			return false
		}
		totalBytes += file.Bytes
		if _, exists := seen[file.Name]; exists {
			return false
		}
		seen[file.Name] = struct{}{}
	}
	if status.State == directional.StateReady {
		return len(status.Files) == 1 &&
			((status.Kind == directional.AccountJobForecast && status.Files[0].Name == "forecast.json") ||
				(status.Kind == directional.AccountJobHorizon && status.Files[0].Name == "horizon.json"))
	}
	return len(status.Files) == 0 && (status.State == directional.StateQueued || status.State == directional.StateRunning || status.State == directional.StateFailed || status.State == directional.StateCancelled)
}

func (dispatcher *AccountResultDispatcher) prune(now time.Time) {
	dispatcher.mu.Lock()
	for id, job := range dispatcher.jobs {
		if terminalAccountJobState(job.status.State) && now.Sub(job.status.UpdatedAt) > dispatcher.resultTTL {
			dispatcher.removeJobLocked(id, job)
		}
	}
	dispatcher.mu.Unlock()
	entries, err := os.ReadDir(dispatcher.resultRoot)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if !entry.IsDir() || !accountJobIDPattern.MatchString(entry.Name()) {
			continue
		}
		dispatcher.mu.Lock()
		job := dispatcher.jobs[entry.Name()]
		active := job != nil && (job.status.State == directional.StateQueued || job.status.State == directional.StateRunning)
		dispatcher.mu.Unlock()
		if active {
			continue
		}
		info, statErr := entry.Info()
		if statErr == nil && now.Sub(info.ModTime()) > dispatcher.resultTTL {
			_ = os.RemoveAll(filepath.Join(dispatcher.resultRoot, entry.Name()))
		}
	}
}

func (dispatcher *AccountResultDispatcher) maintain() {
	interval := min(dispatcher.resultTTL/4, time.Hour)
	if interval < time.Minute {
		interval = time.Minute
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-dispatcher.root.Done():
			return
		case now := <-ticker.C:
			dispatcher.prune(now)
		}
	}
}

func (dispatcher *AccountResultDispatcher) removeJobLocked(jobID string, job *accountResultJob) {
	delete(dispatcher.jobs, jobID)
	if job != nil && job.idempotencyKey != "" && dispatcher.idempotency[job.idempotencyKey] == jobID {
		delete(dispatcher.idempotency, job.idempotencyKey)
	}
}

func (dispatcher *AccountResultDispatcher) removePartialFiles(directory string) {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.Type().IsRegular() && accountFileNamePattern.MatchString(entry.Name()) {
			_ = os.Remove(filepath.Join(directory, entry.Name()))
		}
	}
}

func terminalAccountJobState(state directional.State) bool {
	return state == directional.StateReady || state == directional.StateFailed || state == directional.StateCancelled
}

func validAccountResultETag(value string) bool {
	if len(value) != 66 || value[0] != '"' || value[len(value)-1] != '"' {
		return false
	}
	for _, character := range value[1 : len(value)-1] {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

func accountAdmissionDigest(kind directional.AccountJobKind, admission directional.AccountJobAdmission) [sha256.Size]byte {
	hash := sha256.New()
	_, _ = io.WriteString(hash, string(kind))
	var encoded [24]byte
	binary.BigEndian.PutUint64(encoded[0:8], uint64(admission.TelegramUserID))
	binary.BigEndian.PutUint64(encoded[8:16], math.Float64bits(admission.Latitude))
	binary.BigEndian.PutUint64(encoded[16:24], math.Float64bits(admission.Longitude))
	_, _ = hash.Write(encoded[:])
	_, _ = io.WriteString(hash, admission.Language)
	var digest [sha256.Size]byte
	copy(digest[:], hash.Sum(nil))
	return digest
}

func newAccountJobID() (string, error) {
	buffer := make([]byte, 16)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return hex.EncodeToString(buffer), nil
}

func cloneAccountJobStatus(status directional.AccountJobStatus) directional.AccountJobStatus {
	status.Files = append([]directional.AccountJobFile(nil), status.Files...)
	return status
}

type accountResultCapture struct {
	root        string
	onMessage   func(string)
	mu          sync.Mutex
	lastMessage string
	outputs     []directional.AccountJobFile
	totalBytes  int64
	horizon     chan error
	horizonOne  sync.Once
	firstError  error
	cancelled   atomic.Bool
	structured  atomic.Bool
}

func newAccountResultCapture(root string, onMessage func(string)) *accountResultCapture {
	return &accountResultCapture{root: root, onMessage: onMessage, horizon: make(chan error, 1)}
}

func (capture *accountResultCapture) SendMessage(ctx context.Context, _ int64, text string, _ bool) error {
	if capture.cancelled.Load() {
		return context.Canceled
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	capture.mu.Lock()
	capture.lastMessage = strings.TrimSpace(text)
	capture.mu.Unlock()
	if capture.onMessage != nil {
		capture.onMessage(strings.TrimSpace(text))
	}
	return nil
}

func (capture *accountResultCapture) SendPhoto(ctx context.Context, _ int64, path, caption string) error {
	if capture.structured.Load() {
		return ctx.Err()
	}
	return errors.New("website result did not receive its structured dataset before PNG delivery")
}

func (capture *accountResultCapture) SendDocument(ctx context.Context, _ int64, path, caption string) error {
	if capture.structured.Load() {
		return ctx.Err()
	}
	return errors.New("website result did not receive its structured dataset before PNG delivery")
}

func (capture *accountResultCapture) SendForecastDataset(ctx context.Context, path string) error {
	return capture.captureDataset(ctx, path, "forecast.json")
}

func (capture *accountResultCapture) SendHorizonDataset(ctx context.Context, data []byte) error {
	if len(data) == 0 || int64(len(data)) > accountResultFileLimit {
		return errors.New("horizon interactive dataset violates the file contract")
	}
	temporary, err := os.CreateTemp(capture.root, ".horizon-source-*.json")
	if err != nil {
		return err
	}
	path := temporary.Name()
	defer func() { _ = os.Remove(path) }()
	if err := temporary.Chmod(0o640); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return capture.captureDataset(ctx, path, "horizon.json")
}

func (*accountResultCapture) AnswerAction(context.Context, string, string) error { return nil }

func (capture *accountResultCapture) CompleteHorizon(err error) {
	capture.horizonOne.Do(func() { capture.horizon <- err })
}

func (capture *accountResultCapture) waitHorizon(ctx context.Context) error {
	select {
	case err := <-capture.horizon:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (capture *accountResultCapture) captureDataset(ctx context.Context, sourcePath, name string) (captureErr error) {
	defer func() {
		if captureErr != nil {
			capture.mu.Lock()
			if capture.firstError == nil {
				capture.firstError = captureErr
			}
			capture.mu.Unlock()
		}
	}()
	if capture.cancelled.Load() {
		return context.Canceled
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if !accountFileNamePattern.MatchString(name) {
		return errors.New("account result has an invalid dataset name")
	}
	source, err := os.Open(sourcePath)
	if err != nil {
		return err
	}
	defer func() { _ = source.Close() }()
	info, err := source.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > accountResultFileLimit {
		return errors.New("account result dataset violates the file contract")
	}
	first := make([]byte, 1)
	if _, err := io.ReadFull(source, first); err != nil || first[0] != '{' {
		return errors.New("account result file is not a JSON object")
	}
	if _, err := source.Seek(0, io.SeekStart); err != nil {
		return err
	}
	capture.mu.Lock()
	if capture.totalBytes+info.Size() > accountResultTotalLimit {
		capture.mu.Unlock()
		return errors.New("account result exceeds the aggregate file limit")
	}
	for _, output := range capture.outputs {
		if output.Name == name {
			capture.mu.Unlock()
			return errors.New("account result contains a duplicate dataset name")
		}
	}
	capture.mu.Unlock()
	destination, err := os.OpenFile(filepath.Join(capture.root, name), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o640)
	if err != nil {
		return err
	}
	hash := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(destination, hash), io.LimitReader(source, accountResultFileLimit+1))
	syncErr := destination.Sync()
	closeErr := destination.Close()
	if copyErr != nil || syncErr != nil || closeErr != nil || written != info.Size() {
		_ = os.Remove(filepath.Join(capture.root, name))
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
		if syncErr != nil {
			return syncErr
		}
		return errors.New("account result dataset changed while being copied")
	}
	descriptor := directional.AccountJobFile{
		Name: name, MediaType: "application/json", Bytes: written,
		ETag: `"` + hex.EncodeToString(hash.Sum(nil)) + `"`,
	}
	capture.mu.Lock()
	capture.totalBytes += written
	capture.outputs = append(capture.outputs, descriptor)
	capture.mu.Unlock()
	capture.structured.Store(true)
	return nil
}

func (capture *accountResultCapture) files() []directional.AccountJobFile {
	capture.mu.Lock()
	defer capture.mu.Unlock()
	return append([]directional.AccountJobFile(nil), capture.outputs...)
}

func (capture *accountResultCapture) err() error {
	capture.mu.Lock()
	defer capture.mu.Unlock()
	return capture.firstError
}

func (capture *accountResultCapture) cancel() {
	capture.cancelled.Store(true)
}

// forMessenger creates an isolated request facade without copying mutexes or
// atomic counters from a live platform handler.
func (handler *Handler) forMessenger(messenger Messenger) *Handler {
	return &Handler{
		messenger: messenger, resolver: handler.resolver, provider: handler.provider,
		renderRoot: handler.renderRoot, renderCacheRoot: handler.renderCacheRoot, renderOptions: handler.renderOptions,
		overallCalibration: handler.overallCalibration, forecastMaxStaleAge: handler.forecastMaxStaleAge,
		fallbackMaxStaleAge: handler.fallbackMaxStaleAge, lightPollution: handler.lightPollution,
		worldAtlas2015: handler.worldAtlas2015, atmosphericComposition: handler.atmosphericComposition,
		atmosphericCompositionContext: handler.atmosphericCompositionContext,
		atmosphericCompositionJoin:    handler.atmosphericCompositionJoin,
		atmosphericCompositionTimeout: handler.atmosphericCompositionTimeout,
		persistence:                   handler.persistence, admins: map[int64]struct{}{}, actions: ActionRouter{},
		horizon: handler.horizon, forecastQueue: handler.forecastQueue, sessions: map[int64]saveSession{}, logf: handler.logf,
	}
}

// DeliverHorizon resolves the current ICON-EU run and submits the same pinned
// Horizon calculation used by bot actions, but delivers through the supplied
// request facade. Callers that need a terminal signal implement
// HorizonCompletionMessenger.
func (handler *Handler) DeliverHorizon(ctx context.Context, userID int64, latitude, longitude float64, languageCode string) error {
	if userID <= 0 {
		return errors.New("positive user ID is required")
	}
	if handler.provider == nil || handler.horizon == nil {
		return ErrHorizonUnsupported
	}
	messenger, ok := handler.messenger.(HorizonMessenger)
	if !ok {
		return errors.New("result messenger does not support Horizon")
	}
	language := languageFromCode(languageCode)
	location, err := forecast.NewLocation(latitude, longitude, handler.resolver.Resolve(latitude, longitude))
	if err != nil {
		return err
	}
	_ = handler.sendUserMessage(ctx, userID, language.text(
		"Готовлю актуальные данные для расчёта горизонта…",
		"Preparing current data for the Horizon calculation…"), true, language)
	if handler.forecastQueue != nil {
		release, queueErr := handler.forecastQueue.Wait(ctx, func(position int) error {
			return handler.sendUserMessage(ctx, userID, fmt.Sprintf(language.text(
				"Подготовка данных горизонта поставлена в очередь: ваше место — %d.",
				"Horizon data preparation queued: your position is %d."), position), true, language)
		})
		if queueErr != nil {
			return queueErr
		}
		defer release()
	}
	cloud, err := handler.provider.Cloud(ctx, location)
	if err != nil {
		return fmt.Errorf("obtain current ICON-EU Horizon data: %w", err)
	}
	if cloud.Provider != HorizonProviderICONEU || cloud.RunID == "" {
		return ErrHorizonUnsupported
	}
	return handler.horizon.Deliver(ctx, "web", messenger, userID, userID, HorizonButtonRequest{
		Provider: HorizonProviderICONEU, RunID: cloud.RunID, Location: location,
		ObserverSurfaceElevationM: cloud.SurfaceElevationM,
	}, language.renderCode())
}

func (handler *Handler) CancelHorizon(userID int64) {
	if handler != nil && handler.horizon != nil {
		handler.horizon.CancelUser("web", userID)
	}
}

var _ directional.AccountJobBackend = (*AccountResultDispatcher)(nil)
var _ HorizonMessenger = (*accountResultCapture)(nil)
var _ HorizonCompletionMessenger = (*accountResultCapture)(nil)
var _ ForecastDatasetMessenger = (*accountResultCapture)(nil)
var _ HorizonDatasetMessenger = (*accountResultCapture)(nil)
