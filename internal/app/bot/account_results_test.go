package bot

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"bot_astrosferum/internal/app/directional"
	"bot_astrosferum/internal/forecast"
	"bot_astrosferum/internal/render"
)

type blockingAccountResultProvider struct{ started chan struct{} }

func (provider *blockingAccountResultProvider) Vertical(ctx context.Context, _ forecast.Location) (forecast.VerticalSeries, error) {
	select {
	case provider.started <- struct{}{}:
	default:
	}
	<-ctx.Done()
	return forecast.VerticalSeries{}, ctx.Err()
}

func (*blockingAccountResultProvider) Surface(context.Context, forecast.Location) (forecast.SurfaceSeries, error) {
	return forecast.SurfaceSeries{}, nil
}

func (*blockingAccountResultProvider) Cloud(context.Context, forecast.Location) (forecast.CloudSeries, error) {
	return forecast.CloudSeries{}, nil
}

type accountResultTestMessenger struct{}

func (accountResultTestMessenger) SendMessage(context.Context, int64, string, bool) error { return nil }
func (accountResultTestMessenger) SendPhoto(context.Context, int64, string, string) error { return nil }
func (accountResultTestMessenger) SendDocument(context.Context, int64, string, string) error {
	return nil
}

func TestAccountResultDispatcherFailsClosedUntilHandlerIsConfigured(t *testing.T) {
	dispatcher, err := NewAccountResultDispatcher(t.Context(), time.Minute, time.Minute, time.Hour, t.TempDir(), 1, 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = dispatcher.AdmitAccountJob(t.Context(), directional.AccountJobForecast, directional.AccountJobAdmission{
		TelegramUserID: 42, Latitude: 59.9, Longitude: 30.2, Language: "ru", IdempotencyKey: "0123456789abcdef0123456789abcdef",
	})
	if !errors.Is(err, directional.ErrAccountUnavailable) {
		t.Fatalf("unconfigured dispatcher error = %v", err)
	}
}

func TestAccountResultDispatcherConfigurationValidation(t *testing.T) {
	var nilContext context.Context
	if _, err := NewAccountResultDispatcher(nilContext, time.Minute, time.Minute, time.Hour, t.TempDir(), 1, 1, nil); err == nil {
		t.Fatal("nil root accepted")
	}
	if _, err := NewAccountResultDispatcher(context.Background(), 0, time.Minute, time.Hour, t.TempDir(), 1, 1, nil); err == nil {
		t.Fatal("zero request timeout accepted")
	}
	if _, err := NewAccountResultDispatcher(context.Background(), time.Minute, time.Minute, 0, t.TempDir(), 1, 1, nil); err == nil {
		t.Fatal("zero retention accepted")
	}
	if _, err := NewAccountResultDispatcher(context.Background(), time.Minute, -time.Second, time.Hour, t.TempDir(), 1, 1, nil); err == nil {
		t.Fatal("negative Horizon timeout accepted")
	}
	if _, err := NewAccountResultDispatcher(context.Background(), time.Minute, 0, time.Hour, t.TempDir(), 0, 1, nil); err == nil {
		t.Fatal("zero forecast capacity accepted")
	}
}

func TestAccountResultDispatcherBoundsWorkAndCancelsByOwner(t *testing.T) {
	root, cancel := context.WithCancel(context.Background())
	defer cancel()
	handler, err := NewHandler(accountResultTestMessenger{})
	if err != nil {
		t.Fatal(err)
	}
	provider := &blockingAccountResultProvider{started: make(chan struct{}, 1)}
	if err := handler.EnableForecast(provider, t.TempDir(), render.Options{Width: 3200, Height: 960}); err != nil {
		t.Fatal(err)
	}
	dispatcher, err := NewAccountResultDispatcher(root, time.Minute, time.Minute, time.Hour, t.TempDir(), 2, 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := dispatcher.SetHandler(handler); err != nil {
		t.Fatal(err)
	}
	admission := directional.AccountJobAdmission{TelegramUserID: 42, Latitude: 59.9, Longitude: 30.2, Language: "en", IdempotencyKey: "0123456789abcdef0123456789abcdef"}
	status, err := dispatcher.AdmitAccountJob(t.Context(), directional.AccountJobForecast, admission)
	if err != nil || status.ID == "" || status.State != directional.StateQueued {
		t.Fatalf("admission = %+v, error=%v", status, err)
	}
	select {
	case <-provider.started:
	case <-time.After(time.Second):
		t.Fatal("forecast result job did not start")
	}
	retried, err := dispatcher.AdmitAccountJob(t.Context(), directional.AccountJobForecast, admission)
	if err != nil || retried.ID != status.ID {
		t.Fatalf("idempotent retry = %+v, error = %v", retried, err)
	}
	overlap := admission
	overlap.IdempotencyKey = "abcdef0123456789abcdef0123456789"
	overlap.Latitude = 60.1
	overlapStatus, err := dispatcher.AdmitAccountJob(t.Context(), directional.AccountJobForecast, overlap)
	if err != nil || overlapStatus.ID == status.ID {
		t.Fatalf("same-owner distinct admission = %+v, error = %v", overlapStatus, err)
	}
	other := admission
	other.TelegramUserID = 43
	if _, err := dispatcher.AdmitAccountJob(t.Context(), directional.AccountJobForecast, other); !errors.Is(err, directional.ErrQueueFull) {
		t.Fatalf("capacity error = %v", err)
	}
	if err := dispatcher.CancelAccountJob(t.Context(), 43, status.ID); !errors.Is(err, directional.ErrNotFound) {
		t.Fatalf("cross-owner cancellation = %v", err)
	}
	if err := dispatcher.CancelAccountJob(t.Context(), 42, status.ID); err != nil {
		t.Fatal(err)
	}
	if err := dispatcher.CancelAccountJob(t.Context(), 42, overlapStatus.ID); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		current, statusErr := dispatcher.AccountJobStatus(t.Context(), 42, status.ID)
		if statusErr == nil && current.State == directional.StateCancelled {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("cancelled state was not published")
}

func TestAccountResultCaptureCopiesStructuredDatasetWithoutChangingBytes(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(t.TempDir(), "forecast.json")
	payload := "{\"schema_version\":\"forecast-interactive-v2\"}\n"
	if err := os.WriteFile(source, []byte(payload), 0o600); err != nil {
		t.Fatal(err)
	}
	capture := newAccountResultCapture(root, nil)
	if err := capture.SendForecastDataset(t.Context(), source); err != nil {
		t.Fatal(err)
	}
	files := capture.files()
	if len(files) != 1 || files[0].Name != "forecast.json" || files[0].MediaType != "application/json" || files[0].Bytes != int64(len(payload)) || !strings.HasPrefix(files[0].ETag, `"`) {
		t.Fatalf("captured files = %+v", files)
	}
	stored, err := os.ReadFile(filepath.Join(root, files[0].Name))
	if err != nil {
		t.Fatal(err)
	}
	if string(stored) != payload {
		t.Fatalf("stored JSON changed: %q", stored)
	}
}

func TestAccountResultDispatcherListsRetainedJobsAndRecoversInterruptedState(t *testing.T) {
	root := t.TempDir()
	dispatcher, err := NewAccountResultDispatcher(t.Context(), time.Minute, time.Minute, time.Hour, root, 1, 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	readyID := strings.Repeat("e", 32)
	readyDirectory := filepath.Join(root, readyID)
	if err := os.MkdirAll(readyDirectory, 0o750); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(t.TempDir(), "forecast.json")
	if err := os.WriteFile(source, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	capture := newAccountResultCapture(readyDirectory, nil)
	if err := capture.SendForecastDataset(t.Context(), source); err != nil {
		t.Fatal(err)
	}
	ready := directional.AccountJobStatus{
		ID: readyID, Kind: directional.AccountJobForecast, State: directional.StateReady,
		CreatedAt: now.Add(-time.Minute), UpdatedAt: now, ProgressPercent: 100,
		PointName: "Плавск", Latitude: 53.65, Longitude: 37.3462, Files: capture.files(),
	}
	if err := dispatcher.persist(42, ready); err != nil {
		t.Fatal(err)
	}
	runningID := strings.Repeat("f", 32)
	runningETA := now.Add(time.Minute)
	running := directional.AccountJobStatus{
		ID: runningID, Kind: directional.AccountJobHorizon, State: directional.StateRunning,
		CreatedAt: now.Add(-time.Minute), UpdatedAt: now, Latitude: 53.65, Longitude: 37.3462,
		EstimatedAt: &runningETA, EstimateBasis: "cold",
	}
	if err := dispatcher.persist(42, running); err != nil {
		t.Fatal(err)
	}
	restarted, err := NewAccountResultDispatcher(t.Context(), time.Minute, time.Minute, time.Hour, root, 1, 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	jobs, err := restarted.AccountJobs(t.Context(), 42, directional.AccountJobForecast)
	if err != nil || len(jobs) != 1 || jobs[0].ID != readyID || jobs[0].PointName != "Плавск" {
		t.Fatalf("retained jobs = %+v, error = %v", jobs, err)
	}
	recovered, err := restarted.AccountJobStatus(t.Context(), 42, runningID)
	if err != nil || recovered.State != directional.StateFailed || recovered.FailureCode != "service_restarted" {
		t.Fatalf("recovered interrupted job = %+v, error = %v", recovered, err)
	}
}

func TestAccountResultForecastProgressUsesConfirmedWarmEstimate(t *testing.T) {
	dispatcher, err := NewAccountResultDispatcher(t.Context(), time.Minute, time.Minute, time.Hour, t.TempDir(), 1, 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := dispatcher.SetEstimatedDurations(time.Minute, 10*time.Second, time.Minute); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	started := now.Add(-5 * time.Second)
	warm := dispatcher.enrichStatusLocked(directional.AccountJobStatus{
		ID: strings.Repeat("c", 32), Kind: directional.AccountJobForecast, State: directional.StateRunning,
		CreatedAt: started, UpdatedAt: started, StartedAt: &started, EstimateBasis: "warm",
	}, now)
	if warm.EstimateBasis != "warm" || warm.EstimatedAt == nil || !warm.EstimatedAt.Equal(started.Add(10*time.Second)) ||
		warm.ProgressPercent < 49 || warm.ProgressPercent > 51 {
		t.Fatalf("warm forecast status = %+v", warm)
	}
}

func TestAccountResultOpenRejectsSameSizeCorruption(t *testing.T) {
	root := t.TempDir()
	dispatcher, err := NewAccountResultDispatcher(t.Context(), time.Minute, 0, time.Hour, root, 1, 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	jobID := strings.Repeat("d", 32)
	directory := filepath.Join(root, jobID)
	if err := os.MkdirAll(directory, 0o750); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(t.TempDir(), "forecast.json")
	if err := os.WriteFile(source, []byte("{\"schema_version\":\"forecast-interactive-v2\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	capture := newAccountResultCapture(directory, nil)
	if err := capture.SendForecastDataset(t.Context(), source); err != nil {
		t.Fatal(err)
	}
	files := capture.files()
	now := time.Now().UTC()
	dispatcher.jobs[jobID] = &accountResultJob{owner: 42, cancel: func() {}, status: directional.AccountJobStatus{
		ID: jobID, Kind: directional.AccountJobForecast, State: directional.StateReady,
		CreatedAt: now, UpdatedAt: now, Files: files,
	}}
	stored := filepath.Join(directory, "forecast.json")
	payload, err := os.ReadFile(stored)
	if err != nil {
		t.Fatal(err)
	}
	payload[len(payload)-2] ^= 1
	if err := os.WriteFile(stored, payload, 0o640); err != nil {
		t.Fatal(err)
	}
	if _, err := dispatcher.OpenAccountJobFile(t.Context(), 42, jobID, "forecast.json"); !errors.Is(err, directional.ErrNotFound) {
		t.Fatalf("same-size corrupted result error = %v", err)
	}
}

func TestAccountResultStatusExpiresTerminalJobDuringProcessLifetime(t *testing.T) {
	root := t.TempDir()
	dispatcher, err := NewAccountResultDispatcher(t.Context(), time.Minute, 0, time.Hour, root, 1, 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	jobID := strings.Repeat("a", 32)
	directory := filepath.Join(root, jobID)
	if err := os.MkdirAll(directory, 0o750); err != nil {
		t.Fatal(err)
	}
	job := &accountResultJob{
		owner: 42, idempotencyKey: "forecast:42:" + strings.Repeat("b", 32), cancel: func() {},
		status: directional.AccountJobStatus{
			ID: jobID, Kind: directional.AccountJobForecast, State: directional.StateFailed,
			CreatedAt: time.Now().Add(-2 * time.Hour), UpdatedAt: time.Now().Add(-2 * time.Hour),
		},
	}
	dispatcher.mu.Lock()
	dispatcher.jobs[jobID] = job
	dispatcher.idempotency[job.idempotencyKey] = jobID
	dispatcher.mu.Unlock()
	if _, err := dispatcher.AccountJobStatus(t.Context(), 42, jobID); !errors.Is(err, directional.ErrNotFound) {
		t.Fatalf("expired job status error = %v", err)
	}
	if _, err := os.Stat(directory); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expired result directory still exists: %v", err)
	}
	dispatcher.mu.Lock()
	defer dispatcher.mu.Unlock()
	if dispatcher.idempotency[job.idempotencyKey] != "" {
		t.Fatal("expired job retained its idempotency mapping")
	}
}

func TestAccountResultReadyIsNotPublishedWhenManifestWriteFails(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "results")
	dispatcher, err := NewAccountResultDispatcher(t.Context(), time.Minute, 0, time.Hour, root, 1, 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	jobID := strings.Repeat("c", 32)
	job := &accountResultJob{owner: 42, cancel: func() {}, status: directional.AccountJobStatus{
		ID: jobID, Kind: directional.AccountJobForecast, State: directional.StateRunning,
		CreatedAt: time.Now().Add(-time.Minute), UpdatedAt: time.Now(),
	}}
	dispatcher.jobs[jobID] = job
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(root, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	file := directional.AccountJobFile{
		Name: "forecast.json", MediaType: "application/json", Bytes: 8,
		ETag: `"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"`,
	}
	if err := dispatcher.publishReady(jobID, []directional.AccountJobFile{file}); err == nil {
		t.Fatal("ready state published despite manifest storage failure")
	}
	if job.status.State == directional.StateReady {
		t.Fatal("in-memory job became ready before durable publication")
	}
}

func TestAccountResultCaptureRecordsRejectedStructuredOutput(t *testing.T) {
	source := filepath.Join(t.TempDir(), "forecast.json")
	if err := os.WriteFile(source, []byte("not-json"), 0o600); err != nil {
		t.Fatal(err)
	}
	capture := newAccountResultCapture(t.TempDir(), nil)
	if err := capture.SendForecastDataset(t.Context(), source); err == nil {
		t.Fatal("non-JSON output accepted")
	}
	if capture.err() == nil || len(capture.files()) != 0 {
		t.Fatalf("capture error/files = %v/%+v", capture.err(), capture.files())
	}
}
