package directional

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRemoteRunnerExecutesInsideSharedWorkspace(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "job_abcdefghijklmnopqrstuvwxyz-work")
	if err := os.Mkdir(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	credential := []byte("0123456789abcdefghijklmnopqrstuvwxyz-credential")
	source := SourceIdentity{Provider: "ICON-EU", RunID: "2026072806", GridProfile: "dense-v1", GeometryDigest: "digest"}
	handler, err := NewWorkerHTTPHandler(WorkerHTTPConfig{
		WorkspaceRoot: root, ServiceCredential: credential,
		Runners: map[Kind]Runner{KindAstrodome: RunnerFunc(func(ctx context.Context, execution Execution) (RunnerResult, error) {
			if execution.Workspace != workspace || execution.JobID != "job_abcdefghijklmnopqrstuvwxyz" || string(execution.Payload) != `{"point":1}` {
				t.Fatalf("unexpected execution: %+v", execution)
			}
			path := filepath.Join(execution.Workspace, "dataset.json")
			if err := os.WriteFile(path, []byte(`{"ok":true}`), 0o600); err != nil {
				return RunnerResult{}, err
			}
			return RunnerResult{DatasetPath: path, Provider: source.Provider, RunID: source.RunID, GridProfile: source.GridProfile, GeometryDigest: source.GeometryDigest}, nil
		})},
	})
	if err != nil {
		t.Fatalf("NewWorkerHTTPHandler: %v", err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	remote, err := NewRemoteRunner(server.URL, credential, server.Client())
	if err != nil {
		t.Fatalf("NewRemoteRunner: %v", err)
	}
	result, err := remote.Run(context.Background(), Execution{
		JobID: "job_abcdefghijklmnopqrstuvwxyz", Kind: KindAstrodome, Source: source,
		Payload: json.RawMessage(`{"point":1}`), Workspace: workspace,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.DatasetPath != filepath.Join(workspace, "dataset.json") || result.Provider != "ICON-EU" {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestWorkerHTTPHandlerRejectsWorkspaceOutsideRoot(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	credential := []byte("0123456789abcdefghijklmnopqrstuvwxyz-credential")
	handler, err := NewWorkerHTTPHandler(WorkerHTTPConfig{
		WorkspaceRoot: root, ServiceCredential: credential,
		Runners: map[Kind]Runner{KindHorizon: RunnerFunc(func(context.Context, Execution) (RunnerResult, error) {
			t.Fatal("runner must not execute")
			return RunnerResult{}, nil
		})},
	})
	if err != nil {
		t.Fatal(err)
	}
	payload := workerExecutionRequest{
		JobID: "job_abcdefghijklmnopqrstuvwxyz", Kind: KindHorizon,
		Source:  SourceIdentity{Provider: "ICON-EU", RunID: "2026072806", GridProfile: "horizon", GeometryDigest: "digest"},
		Payload: json.RawMessage(`{}`), Workspace: outside,
	}
	body, _ := json.Marshal(payload)
	request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/internal/v1/execute/horizon", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(workerServiceCredentialHeader, string(credential))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestWorkerHTTPHandlerHealthIsMetadataFreeAndCredentialFree(t *testing.T) {
	root := t.TempDir()
	handler, err := NewWorkerHTTPHandler(WorkerHTTPConfig{
		WorkspaceRoot: root, ServiceCredential: bytes.Repeat([]byte("h"), 32),
		Runners: map[Kind]Runner{KindHorizon: RunnerFunc(func(context.Context, Execution) (RunnerResult, error) {
			return RunnerResult{}, nil
		})},
	})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/healthz", nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || recorder.Body.String() != "ok\n" || recorder.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("health response = %d %q %q", recorder.Code, recorder.Body.String(), recorder.Header().Get("Cache-Control"))
	}
}

func TestWorkerHTTPHandlerLogsInternalRunnerFailure(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "job")
	if err := os.Mkdir(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	credential := bytes.Repeat([]byte("l"), 32)
	var logged string
	handler, err := NewWorkerHTTPHandler(WorkerHTTPConfig{
		WorkspaceRoot: root, ServiceCredential: credential,
		Logf: func(format string, values ...any) { logged = fmt.Sprintf(format, values...) },
		Runners: map[Kind]Runner{KindAstrodome: RunnerFunc(func(context.Context, Execution) (RunnerResult, error) {
			return RunnerResult{}, CodedError{Code: "footprint_unavailable", Err: errors.New("projected footprint exceeds limit")}
		})},
	})
	if err != nil {
		t.Fatal(err)
	}
	payload := workerExecutionRequest{
		JobID: "job_abcdefghijklmnopqrstuvwxyz", Kind: KindAstrodome,
		Source:  SourceIdentity{Provider: "icon-eu", RunID: "2026072812", GridProfile: "dense-v1", GeometryDigest: "digest"},
		Payload: json.RawMessage(`{}`), Workspace: workspace,
	}
	body, _ := json.Marshal(payload)
	request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/internal/v1/execute/astrodome", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(workerServiceCredentialHeader, string(credential))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusUnprocessableEntity || !strings.Contains(logged, "projected footprint exceeds limit") {
		t.Fatalf("response=%d log=%q", recorder.Code, logged)
	}
}

func TestRemoteRunnerHealthTracksWorkerLiveness(t *testing.T) {
	credential := bytes.Repeat([]byte("h"), 32)
	healthy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet || request.URL.Path != "/healthz" {
			http.NotFound(w, request)
			return
		}
		_, _ = w.Write([]byte("ok\n"))
	}))
	defer healthy.Close()
	runner, err := NewRemoteRunner(healthy.URL, credential, healthy.Client())
	if err != nil {
		t.Fatal(err)
	}
	if err := runner.Health(context.Background()); err != nil {
		t.Fatalf("healthy worker probe: %v", err)
	}

	unhealthy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer unhealthy.Close()
	runner, err = NewRemoteRunner(unhealthy.URL, credential, unhealthy.Client())
	if err != nil {
		t.Fatal(err)
	}
	if err := runner.Health(context.Background()); err == nil {
		t.Fatal("unhealthy worker probe succeeded")
	}
}

func TestWorkerHTTPHandlerUsesConfiguredConcurrency(t *testing.T) {
	handler, err := NewWorkerHTTPHandler(WorkerHTTPConfig{
		WorkspaceRoot: t.TempDir(), ServiceCredential: bytes.Repeat([]byte("c"), 32), Concurrency: 2,
		Runners: map[Kind]Runner{KindHorizon: RunnerFunc(func(context.Context, Execution) (RunnerResult, error) {
			return RunnerResult{}, nil
		})},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := cap(handler.active); got != 2 {
		t.Fatalf("worker active-request capacity = %d, want 2", got)
	}
}

func TestWorkerHTTPHandlerAllowsOnlyOneExecution(t *testing.T) {
	root := t.TempDir()
	credential := []byte("0123456789abcdefghijklmnopqrstuvwxyz-credential")
	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	runner := RunnerFunc(func(ctx context.Context, execution Execution) (RunnerResult, error) {
		once.Do(func() { close(started) })
		select {
		case <-release:
		case <-ctx.Done():
			return RunnerResult{}, ctx.Err()
		}
		path := filepath.Join(execution.Workspace, "dataset.json")
		if err := os.WriteFile(path, []byte(`{}`), 0o600); err != nil {
			return RunnerResult{}, err
		}
		return RunnerResult{DatasetPath: path, Provider: execution.Source.Provider, RunID: execution.Source.RunID, GridProfile: execution.Source.GridProfile, GeometryDigest: execution.Source.GeometryDigest}, nil
	})
	handler, err := NewWorkerHTTPHandler(WorkerHTTPConfig{WorkspaceRoot: root, ServiceCredential: credential, Runners: map[Kind]Runner{KindHorizon: runner}})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	remote, err := NewRemoteRunner(server.URL, credential, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	firstWorkspace := filepath.Join(root, "first")
	secondWorkspace := filepath.Join(root, "second")
	for _, path := range []string{firstWorkspace, secondWorkspace} {
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	source := SourceIdentity{Provider: "ICON-EU", RunID: "2026072806", GridProfile: "horizon", GeometryDigest: "digest"}
	firstDone := make(chan error, 1)
	go func() {
		_, runErr := remote.Run(context.Background(), Execution{JobID: "job_abcdefghijklmnopqrstuvwx1", Kind: KindHorizon, Source: source, Payload: json.RawMessage(`{}`), Workspace: firstWorkspace})
		firstDone <- runErr
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("first execution did not start")
	}
	_, secondErr := remote.Run(context.Background(), Execution{JobID: "job_abcdefghijklmnopqrstuvwx2", Kind: KindHorizon, Source: source, Payload: json.RawMessage(`{}`), Workspace: secondWorkspace})
	var coded FailureCoder
	if !errors.As(secondErr, &coded) || coded.FailureCode() != "worker_busy" {
		t.Fatalf("second error=%v", secondErr)
	}
	close(release)
	if err := <-firstDone; err != nil {
		t.Fatalf("first execution: %v", err)
	}
}

func TestWorkerHTTPHandlerSerializesHorizonAndAstrodome(t *testing.T) {
	root := t.TempDir()
	credential := []byte("0123456789abcdefghijklmnopqrstuvwxyz-credential")
	horizonStarted := make(chan struct{})
	releaseHorizon := make(chan struct{})
	astrodomeCalled := make(chan struct{}, 1)
	horizonRunner := RunnerFunc(func(ctx context.Context, execution Execution) (RunnerResult, error) {
		close(horizonStarted)
		select {
		case <-releaseHorizon:
		case <-ctx.Done():
			return RunnerResult{}, ctx.Err()
		}
		return writeWorkerTestDataset(execution)
	})
	astrodomeRunner := RunnerFunc(func(_ context.Context, execution Execution) (RunnerResult, error) {
		astrodomeCalled <- struct{}{}
		return writeWorkerTestDataset(execution)
	})
	handler, err := NewWorkerHTTPHandler(WorkerHTTPConfig{
		WorkspaceRoot: root, ServiceCredential: credential,
		Runners: map[Kind]Runner{KindHorizon: horizonRunner, KindAstrodome: astrodomeRunner},
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	remote, err := NewRemoteRunner(server.URL, credential, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	firstWorkspace := filepath.Join(root, "horizon")
	secondWorkspace := filepath.Join(root, "astrodome")
	for _, path := range []string{firstWorkspace, secondWorkspace} {
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	source := SourceIdentity{Provider: "ICON-EU", RunID: "2026072806", GridProfile: "directional", GeometryDigest: "digest"}
	firstDone := make(chan error, 1)
	go func() {
		_, runErr := remote.Run(context.Background(), Execution{
			JobID: "job_abcdefghijklmnopqrstuvwx1", Kind: KindHorizon, Source: source,
			Payload: json.RawMessage(`{}`), Workspace: firstWorkspace,
		})
		firstDone <- runErr
	}()
	select {
	case <-horizonStarted:
	case <-time.After(time.Second):
		t.Fatal("Horizon execution did not start")
	}
	_, secondErr := remote.Run(context.Background(), Execution{
		JobID: "job_abcdefghijklmnopqrstuvwx2", Kind: KindAstrodome, Source: source,
		Payload: json.RawMessage(`{}`), Workspace: secondWorkspace,
	})
	var coded FailureCoder
	if !errors.As(secondErr, &coded) || coded.FailureCode() != "worker_busy" {
		t.Fatalf("Astrodome error while Horizon was active = %v", secondErr)
	}
	select {
	case <-astrodomeCalled:
		t.Fatal("Astrodome runner executed concurrently with Horizon")
	default:
	}
	close(releaseHorizon)
	if err := <-firstDone; err != nil {
		t.Fatalf("Horizon execution: %v", err)
	}
}

func writeWorkerTestDataset(execution Execution) (RunnerResult, error) {
	path := filepath.Join(execution.Workspace, "dataset.json")
	if err := os.WriteFile(path, []byte(`{}`), 0o600); err != nil {
		return RunnerResult{}, err
	}
	return RunnerResult{
		DatasetPath: path, Provider: execution.Source.Provider, RunID: execution.Source.RunID,
		GridProfile: execution.Source.GridProfile, GeometryDigest: execution.Source.GeometryDigest,
	}, nil
}
