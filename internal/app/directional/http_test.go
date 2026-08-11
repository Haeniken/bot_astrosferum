package directional

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type testAstrodomeBackend struct {
	mutex     sync.Mutex
	admission AstrodomeAdmission
	prepared  *PreparedAstrodome
}

type testWorkerHealth struct{ err error }

func (health testWorkerHealth) Health(context.Context) error { return health.err }

type testAccountJobBackend struct {
	kind      AccountJobKind
	admission AccountJobAdmission
}

func (backend *testAccountJobBackend) AdmitAccountJob(_ context.Context, kind AccountJobKind, admission AccountJobAdmission) (AccountJobStatus, error) {
	backend.kind, backend.admission = kind, admission
	return AccountJobStatus{ID: strings.Repeat("a", 32), Kind: kind, State: StateReady, Files: []AccountJobFile{{
		Name: "horizon.png", MediaType: "image/png", Bytes: 3, ETag: `"etag"`,
	}}}, nil
}

func (*testAccountJobBackend) AccountJobStatus(_ context.Context, owner int64, jobID string) (AccountJobStatus, error) {
	if owner != 42 || jobID != strings.Repeat("a", 32) {
		return AccountJobStatus{}, ErrNotFound
	}
	return AccountJobStatus{ID: jobID, Kind: AccountJobHorizon, State: StateReady, Files: []AccountJobFile{{
		Name: "horizon.png", MediaType: "image/png", Bytes: 3, ETag: `"etag"`,
	}}}, nil
}

func (*testAccountJobBackend) OpenAccountJobFile(_ context.Context, owner int64, jobID, name string) (AccountJobOutput, error) {
	if owner != 42 || jobID != strings.Repeat("a", 32) || name != "horizon.png" {
		return AccountJobOutput{}, ErrNotFound
	}
	return AccountJobOutput{Body: io.NopCloser(strings.NewReader("png")), Bytes: 3, MediaType: "image/png", ETag: `"etag"`}, nil
}

func (*testAccountJobBackend) CancelAccountJob(_ context.Context, owner int64, jobID string) error {
	if owner != 42 || jobID != strings.Repeat("a", 32) {
		return ErrNotFound
	}
	return nil
}

func (backend *testAstrodomeBackend) Availability(_ context.Context, userID int64) (AstrodomeAvailability, error) {
	if userID != 42 {
		return AstrodomeAvailability{}, ErrDisabled
	}
	return AstrodomeAvailability{
		Enabled: true, Available: true, Provider: "icon-eu", RunID: "2026072800",
		RunBaseTime:      time.Date(2026, 7, 28, 0, 0, 0, 0, time.UTC),
		FreshnessSeconds: 3600, Stale: true, GridProfile: "dense-v1",
	}, nil
}

func (backend *testAstrodomeBackend) Prepare(_ context.Context, admission AstrodomeAdmission) (PreparedAstrodome, error) {
	backend.mutex.Lock()
	backend.admission = admission
	override := backend.prepared
	backend.mutex.Unlock()
	if override != nil {
		return *override, nil
	}
	return PreparedAstrodome{
		ScienceCacheKey: "run=2026072800;point=canonical;grid=dense-v1",
		Source: SourceIdentity{
			Provider: "icon-eu", RunID: "2026072800", GridProfile: "dense-v1",
			GeometryDigest: "sha256:" + strings.Repeat("a", 64),
		},
		Payload: func() json.RawMessage {
			payload, _ := JSONPayload(admission)
			return payload
		}(),
	}, nil
}

func TestHTTPHandlerPublishesStableAccountContractAndGzipPassThrough(t *testing.T) {
	credential := []byte(strings.Repeat("s", 32))
	coordinator := newTestCoordinator(t, t.TempDir(), 4, 16, time.Hour, time.Now)
	runner := RunnerFunc(func(_ context.Context, execution Execution) (RunnerResult, error) {
		var admission AstrodomeAdmission
		if err := json.Unmarshal(execution.Payload, &admission); err != nil || admission.TelegramUserID != 42 || admission.Point.ID != 7 || admission.Language != "ru" {
			return RunnerResult{}, errors.New("unexpected Astrodome payload")
		}
		path := filepath.Join(execution.Workspace, "dataset.json.gz")
		file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			return RunnerResult{}, err
		}
		compressed := gzip.NewWriter(file)
		_, writeErr := io.WriteString(compressed, `{"frames":72}`)
		closeErr := errors.Join(compressed.Close(), file.Close())
		if err := errors.Join(writeErr, closeErr); err != nil {
			return RunnerResult{}, err
		}
		return RunnerResult{
			DatasetPath: path, ContentEncoding: "gzip", Provider: "icon-eu",
			RunID: "2026072800", GridProfile: "dense-v1", GeometryDigest: "sha256:" + strings.Repeat("a", 64),
		}, nil
	})
	registerBoth(t, coordinator, runner)
	startCoordinator(t, coordinator)
	backend := &testAstrodomeBackend{}
	handler, err := NewHTTPHandler(HTTPConfig{
		Coordinator: coordinator, AstrodomeBackend: backend, WorkerHealth: testWorkerHealth{}, ServiceCredential: credential,
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	response := internalRequest(t, server.Client(), credential, 42, http.MethodGet,
		server.URL+"/internal/v1/directional/astrodome/availability", nil, "")
	defer func() { _ = response.Body.Close() }()
	var availability AstrodomeAvailability
	if err := json.NewDecoder(response.Body).Decode(&availability); err != nil || response.StatusCode != http.StatusOK ||
		!availability.Enabled || !availability.Available || !availability.WorkerAvailable || !availability.Stale ||
		availability.Provider != "icon-eu" || availability.GridProfile != "dense-v1" {
		t.Fatalf("availability = %+v, status=%d, error=%v", availability, response.StatusCode, err)
	}
	admission := AstrodomeAdmission{
		TelegramUserID: 42,
		Point:          SavedPoint{ID: 7, Name: "private point", Latitude: 59.9, Longitude: 30.2},
		Language:       "ru", IdempotencyKey: "0123456789abcdef",
	}
	body, err := json.Marshal(admission)
	if err != nil {
		t.Fatal(err)
	}
	response = internalRequest(t, server.Client(), credential, 42, http.MethodPost,
		server.URL+"/internal/v1/directional/astrodome/jobs", body, "application/json")
	defer func() { _ = response.Body.Close() }()
	var status AstrodomeJobStatus
	if err := json.NewDecoder(response.Body).Decode(&status); err != nil || response.StatusCode != http.StatusAccepted || status.ID == "" {
		t.Fatalf("admission = %+v, status=%d, error=%v", status, response.StatusCode, err)
	}
	result, err := (&Ticket{coordinator: coordinator, jobID: status.ID, ownerID: "telegram:42"}).Wait(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	response = internalRequest(t, server.Client(), credential, 42, http.MethodGet,
		server.URL+"/internal/v1/directional/astrodome/jobs/"+status.ID, nil, "")
	defer func() { _ = response.Body.Close() }()
	if err := json.NewDecoder(response.Body).Decode(&status); err != nil || response.StatusCode != http.StatusOK || status.State != "ready" || status.Provider != "icon-eu" || status.RunID != "2026072800" ||
		status.GridProfile != "dense-v1" || status.GeometryDigest != "sha256:"+strings.Repeat("a", 64) || status.DatasetBytes != result.Bytes {
		t.Fatalf("ready status = %+v, HTTP=%d, error=%v", status, response.StatusCode, err)
	}
	response = internalRequest(t, server.Client(), credential, 43, http.MethodGet,
		server.URL+"/internal/v1/directional/astrodome/jobs/"+status.ID, nil, "")
	_ = response.Body.Close()
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("cross-owner status = %d", response.StatusCode)
	}
	response = internalRequest(t, server.Client(), credential, 42, http.MethodGet,
		server.URL+"/internal/v1/directional/astrodome/jobs/"+status.ID+"/dataset", nil, "")
	compressedBytes, readErr := io.ReadAll(response.Body)
	closeErr := response.Body.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || response.Header.Get("Content-Encoding") != "gzip" ||
		response.Header.Get("ETag") != result.ETag || int64(len(compressedBytes)) != result.Bytes {
		t.Fatalf("dataset status/headers = %d/%v, compressed bytes=%d", response.StatusCode, response.Header, len(compressedBytes))
	}
	reader, err := gzip.NewReader(strings.NewReader(string(compressedBytes)))
	if err != nil {
		t.Fatal(err)
	}
	plain, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	if string(plain) != `{"frames":72}` {
		t.Fatalf("dataset body = %q", plain)
	}
	response = internalRequest(t, server.Client(), credential, 42, http.MethodDelete,
		server.URL+"/internal/v1/directional/astrodome/jobs/"+status.ID, nil, "")
	_ = response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("cancel status = %d", response.StatusCode)
	}
	response = internalRequest(t, server.Client(), credential, 42, http.MethodGet,
		server.URL+"/internal/v1/directional/astrodome/jobs/"+status.ID, nil, "")
	_ = response.Body.Close()
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("status after owner cancellation = %d", response.StatusCode)
	}
	backend.mutex.Lock()
	captured := backend.admission
	backend.mutex.Unlock()
	if captured.TelegramUserID != 42 || captured.IdempotencyKey != "0123456789abcdef" || captured.Point.Name != "private point" {
		t.Fatalf("captured admission = %+v", captured)
	}
}

func TestHTTPHandlerAuthenticatesAndValidatesAccountResults(t *testing.T) {
	credential := []byte(strings.Repeat("s", 32))
	coordinator := newTestCoordinator(t, t.TempDir(), 4, 16, time.Hour, time.Now)
	registerBoth(t, coordinator, RunnerFunc(func(context.Context, Execution) (RunnerResult, error) {
		return RunnerResult{}, errors.New("not used")
	}))
	backend := &testAccountJobBackend{}
	handler, err := NewHTTPHandler(HTTPConfig{
		Coordinator: coordinator, AstrodomeBackend: &testAstrodomeBackend{}, WorkerHealth: testWorkerHealth{},
		AccountJobs: backend, ServiceCredential: credential,
	})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequestWithContext(t.Context(), http.MethodPost,
		"http://internal/internal/v1/account/jobs/horizon",
		strings.NewReader(`{"telegram_user_id":42,"latitude":59.9,"longitude":30.2,"language":"ru","idempotency_key":"0123456789abcdef0123456789abcdef"}`))
	request.Header.Set(ServiceCredentialHeader, string(credential))
	request.Header.Set(UserIDHeader, "42")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted || backend.kind != AccountJobHorizon || backend.admission.TelegramUserID != 42 {
		t.Fatalf("job response = %d %q, backend=%+v", response.Code, response.Body.String(), backend)
	}

	request = httptest.NewRequestWithContext(t.Context(), http.MethodGet,
		"http://internal/internal/v1/account/jobs/"+strings.Repeat("a", 32)+"/files/horizon.png", nil)
	request.Header.Set(ServiceCredentialHeader, string(credential))
	request.Header.Set(UserIDHeader, "42")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Body.String() != "png" || response.Header().Get("Content-Type") != "image/png" || response.Header().Get("ETag") != `"etag"` {
		t.Fatalf("file response = %d %q headers=%v", response.Code, response.Body.String(), response.Header())
	}

	request = httptest.NewRequestWithContext(t.Context(), http.MethodPost,
		"http://internal/internal/v1/account/jobs/horizon",
		strings.NewReader(`{"telegram_user_id":43,"latitude":59.9,"longitude":30.2,"language":"ru","idempotency_key":"0123456789abcdef0123456789abcdef"}`))
	request.Header.Set(ServiceCredentialHeader, string(credential))
	request.Header.Set(UserIDHeader, "42")
	request.Header.Set("Content-Type", "application/json")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("cross-user job response = %d %q", response.Code, response.Body.String())
	}
}

func TestHTTPHandlerRequiresServiceAndUserCredentialsAndStrictJSON(t *testing.T) {
	credential := []byte(strings.Repeat("s", 32))
	coordinator := newTestCoordinator(t, t.TempDir(), 2, 8, time.Hour, time.Now)
	quick := RunnerFunc(func(_ context.Context, execution Execution) (RunnerResult, error) {
		return writeRunnerDataset(execution, `{}`)
	})
	registerBoth(t, coordinator, quick)
	startCoordinator(t, coordinator)
	handler, err := NewHTTPHandler(HTTPConfig{
		Coordinator: coordinator, AstrodomeBackend: &testAstrodomeBackend{}, WorkerHealth: testWorkerHealth{}, ServiceCredential: credential,
	})
	if err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/internal/v1/directional/astrodome/availability", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("missing service credential status = %d", response.Code)
	}
	request = httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/internal/v1/directional/astrodome/availability", nil)
	request.Header.Set(ServiceCredentialHeader, string(credential))
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("availability without user credential status = %d", response.Code)
	}

	request = httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/internal/v1/directional/astrodome/jobs", strings.NewReader(`{}`))
	request.Header.Set(ServiceCredentialHeader, string(credential))
	request.Header.Set("Content-Type", "application/json")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("missing user credential status = %d", response.Code)
	}

	body := `{"TelegramUserID":42,"Point":{"id":0,"name":"","latitude":59.9,"longitude":30.2},"Language":"en","IdempotencyKey":"0123456789abcdef","Extra":true}`
	request = httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/internal/v1/directional/astrodome/jobs", strings.NewReader(body))
	request.Header.Set(ServiceCredentialHeader, string(credential))
	request.Header.Set(UserIDHeader, "42")
	request.Header.Set("Content-Type", "application/json")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("unknown JSON field status = %d, body=%q", response.Code, response.Body.String())
	}

	body = `{"TelegramUserID":43,"Point":{"id":0,"name":"","latitude":59.9,"longitude":30.2},"Language":"en","IdempotencyKey":"0123456789abcdef"}`
	request = httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/internal/v1/directional/astrodome/jobs", strings.NewReader(body))
	request.Header.Set(ServiceCredentialHeader, string(credential))
	request.Header.Set(UserIDHeader, "42")
	request.Header.Set("Content-Type", "application/json")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("body/header identity mismatch status = %d", response.Code)
	}
}

func TestHTTPHandlerReportsWorkerHealthSeparatelyFromModelReadiness(t *testing.T) {
	credential := []byte(strings.Repeat("s", 32))
	coordinator := newTestCoordinator(t, t.TempDir(), 2, 8, time.Hour, time.Now)
	quick := RunnerFunc(func(_ context.Context, execution Execution) (RunnerResult, error) {
		return writeRunnerDataset(execution, `{}`)
	})
	registerBoth(t, coordinator, quick)
	startCoordinator(t, coordinator)
	handler, err := NewHTTPHandler(HTTPConfig{
		Coordinator: coordinator, AstrodomeBackend: &testAstrodomeBackend{},
		WorkerHealth: testWorkerHealth{err: errors.New("offline")}, ServiceCredential: credential,
	})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/internal/v1/directional/astrodome/availability", nil)
	request.Header.Set(ServiceCredentialHeader, string(credential))
	request.Header.Set(UserIDHeader, "42")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("availability status = %d, body=%q", response.Code, response.Body.String())
	}
	var availability AstrodomeAvailability
	if err := json.Unmarshal(response.Body.Bytes(), &availability); err != nil {
		t.Fatal(err)
	}
	if !availability.Available || availability.WorkerAvailable {
		t.Fatalf("model and worker readiness were conflated: %+v", availability)
	}
}

func TestHTTPHandlerRejectsUnpinnedOrUnknownAstrodomeSource(t *testing.T) {
	credential := []byte(strings.Repeat("s", 32))
	coordinator := newTestCoordinator(t, t.TempDir(), 2, 8, time.Hour, time.Now)
	var calls atomic.Int32
	runner := RunnerFunc(func(_ context.Context, execution Execution) (RunnerResult, error) {
		calls.Add(1)
		return writeRunnerDataset(execution, `{}`)
	})
	registerBoth(t, coordinator, runner)
	startCoordinator(t, coordinator)
	backend := &testAstrodomeBackend{prepared: &PreparedAstrodome{
		ScienceCacheKey: "invalid-source", Source: SourceIdentity{
			Provider: "icon-global", RunID: "2026072800", GridProfile: "dense-v1",
			GeometryDigest: "sha256:" + strings.Repeat("a", 64),
		}, Payload: json.RawMessage("null"),
	}}
	handler, err := NewHTTPHandler(HTTPConfig{
		Coordinator: coordinator, AstrodomeBackend: backend, WorkerHealth: testWorkerHealth{}, ServiceCredential: credential,
	})
	if err != nil {
		t.Fatal(err)
	}
	body := `{"TelegramUserID":42,"Point":{"id":0,"name":"","latitude":59.9,"longitude":30.2},"Language":"en","IdempotencyKey":"0123456789abcdef"}`
	request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/internal/v1/directional/astrodome/jobs", strings.NewReader(body))
	request.Header.Set(ServiceCredentialHeader, string(credential))
	request.Header.Set(UserIDHeader, "42")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable || calls.Load() != 0 {
		t.Fatalf("invalid source response/calls = %d/%d", response.Code, calls.Load())
	}
}

func TestValidatePreparedAstrodomeAcceptsEveryVersionedGridProfile(t *testing.T) {
	t.Parallel()
	for _, gridProfile := range []string{"dense-v1", "sparse-storage-v1", "production-v2"} {
		gridProfile := gridProfile
		t.Run(gridProfile, func(t *testing.T) {
			t.Parallel()
			prepared := PreparedAstrodome{
				ScienceCacheKey: "science-" + gridProfile,
				Source: SourceIdentity{
					Provider: "icon-eu", RunID: "2026072800", GridProfile: gridProfile,
					GeometryDigest: "sha256:" + strings.Repeat("a", 64),
				},
				Payload: json.RawMessage("null"),
			}
			if _, err := validatePreparedAstrodome(prepared); err != nil {
				t.Fatalf("versioned grid profile rejected: %v", err)
			}
		})
	}
}

func internalRequest(
	t *testing.T,
	client *http.Client,
	credential []byte,
	userID int64,
	method string,
	target string,
	body []byte,
	contentType string,
) *http.Response {
	t.Helper()
	request, err := http.NewRequestWithContext(t.Context(), method, target, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set(ServiceCredentialHeader, string(credential))
	request.Header.Set(UserIDHeader, fmt.Sprintf("%d", userID))
	request.Header.Set("Accept-Encoding", "gzip")
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	return response
}
