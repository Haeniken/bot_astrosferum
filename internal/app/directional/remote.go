package directional

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

const (
	workerServiceCredentialHeader = "X-Astrosferum-Worker-Service"
	maximumWorkerRequestBytes     = 128 << 10
	maximumWorkerResponseBytes    = 32 << 10
)

// RemoteRunner keeps queue ownership in the bot while executing the expensive
// calculation in the isolated directional-worker process. Both processes see
// the same staging root at the same absolute path; no model or result bytes
// are copied over HTTP.
type RemoteRunner struct {
	endpoint   *url.URL
	credential string
	client     *http.Client
}

type workerExecutionRequest struct {
	JobID     string          `json:"job_id"`
	Kind      Kind            `json:"kind"`
	Source    SourceIdentity  `json:"source"`
	Payload   json.RawMessage `json:"payload"`
	Workspace string          `json:"workspace"`
}

type workerExecutionResponse struct {
	Result RunnerResult `json:"result"`
}

type workerProblem struct {
	Error string `json:"error"`
}

func NewRemoteRunner(endpoint string, credential []byte, client *http.Client) (*RemoteRunner, error) {
	parsed, err := url.Parse(strings.TrimRight(endpoint, "/"))
	if err != nil || parsed.Scheme != "http" || parsed.Host == "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("directional worker URL must be an absolute internal HTTP URL")
	}
	if len(credential) < minimumServiceCredentialBytes {
		return nil, fmt.Errorf("directional worker credential must contain at least %d bytes", minimumServiceCredentialBytes)
	}
	if client == nil {
		client = http.DefaultClient
	}
	return &RemoteRunner{endpoint: parsed, credential: string(credential), client: client}, nil
}

// Health checks only process liveness. Source readiness, queue state, and
// model metadata remain owned by the bot-side availability endpoint.
func (runner *RemoteRunner) Health(ctx context.Context) error {
	if runner == nil || runner.endpoint == nil || runner.client == nil {
		return errors.New("directional remote runner is not initialized")
	}
	if ctx == nil {
		return errors.New("directional remote runner context is required")
	}
	target := *runner.endpoint
	target.Path = strings.TrimRight(runner.endpoint.Path, "/") + "/healthz"
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return errors.New("construct directional worker health request")
	}
	response, err := runner.client.Do(request)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		return errors.New("directional worker health transport failed")
	}
	defer func() { _ = response.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 1024))
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("directional worker health returned HTTP %d", response.StatusCode)
	}
	return nil
}

func (runner *RemoteRunner) Run(ctx context.Context, execution Execution) (RunnerResult, error) {
	if runner == nil || runner.endpoint == nil || runner.client == nil {
		return RunnerResult{}, errors.New("directional remote runner is not initialized")
	}
	if ctx == nil {
		return RunnerResult{}, errors.New("directional remote runner context is required")
	}
	requestBody, err := json.Marshal(workerExecutionRequest(execution))
	if err != nil {
		return RunnerResult{}, fmt.Errorf("encode directional worker request: %w", err)
	}
	target := *runner.endpoint
	target.Path = strings.TrimRight(runner.endpoint.Path, "/") + "/internal/v1/execute/" + string(execution.Kind)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, target.String(), bytes.NewReader(requestBody))
	if err != nil {
		return RunnerResult{}, fmt.Errorf("construct directional worker request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(workerServiceCredentialHeader, runner.credential)
	response, err := runner.client.Do(request)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return RunnerResult{}, ctxErr
		}
		return RunnerResult{}, CodedError{Code: "worker_unavailable", Err: errors.New("directional worker transport failed")}
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		problem := workerProblem{Error: "worker_unavailable"}
		_ = decodeWorkerJSON(response.Body, maximumWorkerResponseBytes, &problem)
		if !failureCodePattern.MatchString(problem.Error) {
			problem.Error = "worker_unavailable"
		}
		return RunnerResult{}, CodedError{Code: problem.Error, Err: errors.New("directional worker rejected the calculation")}
	}
	var decoded workerExecutionResponse
	if err := decodeWorkerJSON(response.Body, maximumWorkerResponseBytes, &decoded); err != nil {
		return RunnerResult{}, CodedError{Code: "worker_protocol", Err: err}
	}
	if !runnerResultMatchesSource(decoded.Result, execution.Source) {
		return RunnerResult{}, CodedError{Code: "source_mismatch", Err: errors.New("directional worker returned another source identity")}
	}
	return decoded.Result, nil
}

type WorkerHTTPConfig struct {
	WorkspaceRoot     string
	ServiceCredential []byte
	Concurrency       int
	Runners           map[Kind]Runner
	Logf              func(string, ...any)
}

// WorkerHTTPHandler exposes only synchronous execution to the trusted bot.
// Queueing, ownership, idempotency, caching, and publication remain solely in
// the bot-side Coordinator. The configured active-request guard is defence in
// depth if a future caller bypasses that coordinator accidentally.
type WorkerHTTPHandler struct {
	workspaceRoot    string
	credentialDigest [sha256.Size]byte
	runners          map[Kind]Runner
	active           chan struct{}
	logf             func(string, ...any)
}

func NewWorkerHTTPHandler(config WorkerHTTPConfig) (*WorkerHTTPHandler, error) {
	if config.Concurrency == 0 {
		config.Concurrency = 1
	}
	if config.Concurrency < 1 || config.Concurrency > MaxConcurrency {
		return nil, fmt.Errorf("directional worker concurrency must be between 1 and %d", MaxConcurrency)
	}
	if len(config.ServiceCredential) < minimumServiceCredentialBytes {
		return nil, fmt.Errorf("directional worker credential must contain at least %d bytes", minimumServiceCredentialBytes)
	}
	root, err := filepath.Abs(config.WorkspaceRoot)
	if err != nil || strings.TrimSpace(config.WorkspaceRoot) == "" {
		return nil, errors.New("directional worker workspace root is required")
	}
	if err := os.MkdirAll(root, 0o750); err != nil {
		return nil, fmt.Errorf("create directional worker workspace root: %w", err)
	}
	registered := make(map[Kind]Runner, len(config.Runners))
	for kind, runner := range config.Runners {
		if !validKind(kind) || runner == nil {
			return nil, errors.New("directional worker runners contain an invalid entry")
		}
		registered[kind] = runner
	}
	if len(registered) == 0 {
		return nil, errors.New("directional worker requires at least one runner")
	}
	if config.Logf == nil {
		config.Logf = func(string, ...any) {}
	}
	return &WorkerHTTPHandler{
		workspaceRoot: root, credentialDigest: sha256.Sum256(config.ServiceCredential),
		runners: registered, active: make(chan struct{}, config.Concurrency), logf: config.Logf,
	}, nil
}

func (handler *WorkerHTTPHandler) ServeHTTP(w http.ResponseWriter, request *http.Request) {
	// Liveness exposes no queue, source, or job metadata and is intentionally
	// credential-free so the container runtime can probe an isolated worker.
	if request.Method == http.MethodGet && request.URL.Path == "/healthz" {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "ok\n")
		return
	}
	candidate := sha256.Sum256([]byte(request.Header.Get(workerServiceCredentialHeader)))
	if subtle.ConstantTimeCompare(candidate[:], handler.credentialDigest[:]) != 1 {
		writeWorkerProblem(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if request.Method != http.MethodPost || !strings.HasPrefix(request.URL.Path, "/internal/v1/execute/") {
		http.NotFound(w, request)
		return
	}
	kind := Kind(strings.TrimPrefix(request.URL.Path, "/internal/v1/execute/"))
	runner, exists := handler.runners[kind]
	if !exists || !validKind(kind) {
		http.NotFound(w, request)
		return
	}
	if contentType := strings.ToLower(request.Header.Get("Content-Type")); !strings.HasPrefix(contentType, "application/json") {
		writeWorkerProblem(w, http.StatusUnsupportedMediaType, "json_required")
		return
	}
	var payload workerExecutionRequest
	request.Body = http.MaxBytesReader(w, request.Body, maximumWorkerRequestBytes)
	if err := decodeWorkerJSON(request.Body, maximumWorkerRequestBytes, &payload); err != nil {
		writeWorkerProblem(w, http.StatusBadRequest, "invalid_request")
		return
	}
	if payload.Kind != kind || !validJobID(payload.JobID) || !json.Valid(payload.Payload) {
		writeWorkerProblem(w, http.StatusBadRequest, "invalid_request")
		return
	}
	if err := validateRequest(Request{
		Kind: kind, OwnerID: "internal-worker", IdempotencyKey: payload.JobID,
		ScienceCacheKey: payload.JobID, Source: payload.Source, Payload: payload.Payload,
	}); err != nil {
		writeWorkerProblem(w, http.StatusBadRequest, "invalid_request")
		return
	}
	workspace, err := handler.validateWorkspace(payload.Workspace)
	if err != nil {
		writeWorkerProblem(w, http.StatusBadRequest, "invalid_workspace")
		return
	}
	select {
	case handler.active <- struct{}{}:
		defer func() { <-handler.active }()
	default:
		writeWorkerProblem(w, http.StatusConflict, "worker_busy")
		return
	}
	result, err := runner.Run(request.Context(), Execution{
		JobID: payload.JobID, Kind: payload.Kind, Source: payload.Source,
		Payload: append(json.RawMessage(nil), payload.Payload...), Workspace: workspace,
	})
	if err != nil {
		handler.logf("directional worker %s calculation failed: %v", kind, err)
		writeWorkerProblem(w, http.StatusUnprocessableEntity, failureCode(err))
		return
	}
	if !runnerResultMatchesSource(result, payload.Source) {
		writeWorkerProblem(w, http.StatusUnprocessableEntity, "source_mismatch")
		return
	}
	if _, err := validateWorkspaceResult(workspace, result.DatasetPath); err != nil {
		writeWorkerProblem(w, http.StatusUnprocessableEntity, "invalid_result")
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(workerExecutionResponse{Result: result})
}

func (handler *WorkerHTTPHandler) validateWorkspace(candidate string) (string, error) {
	if !filepath.IsAbs(candidate) {
		return "", errors.New("worker workspace must be absolute")
	}
	realRoot, err := filepath.EvalSymlinks(handler.workspaceRoot)
	if err != nil {
		return "", err
	}
	realWorkspace, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return "", err
	}
	relative, err := filepath.Rel(realRoot, realWorkspace)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
		return "", errors.New("worker workspace escapes configured root")
	}
	info, err := os.Lstat(realWorkspace)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("worker workspace is not a regular directory")
	}
	return realWorkspace, nil
}

func decodeWorkerJSON(reader io.Reader, maximum int64, destination any) error {
	decoder := json.NewDecoder(io.LimitReader(reader, maximum+1))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("directional worker JSON contains trailing values")
	}
	return nil
}

func writeWorkerProblem(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(workerProblem{Error: code})
}

var _ Runner = (*RemoteRunner)(nil)
