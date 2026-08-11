package bot

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"bot_astrosferum/internal/app/directional"
)

func TestHorizonUsesSharedDirectionalFIFOAfterAstrodome(t *testing.T) {
	root := t.TempDir()
	source := &horizonFakeSource{currentRun: horizonTestRunID, supported: true}
	jobs := newHorizonTestJobs(t, filepath.Join(root, "horizon-cache"), source, HorizonJobsConfig{})
	jobs.logf = t.Logf
	coordinator := newBotDirectionalCoordinator(t, filepath.Join(root, "directional-cache"))
	if err := jobs.UseDirectionalCoordinator(coordinator); err != nil {
		t.Fatalf("UseDirectionalCoordinator: %v", err)
	}
	astrodomeStarted := make(chan struct{})
	releaseAstrodome := make(chan struct{})
	if err := coordinator.Register(directional.KindAstrodome, directional.RunnerFunc(func(ctx context.Context, execution directional.Execution) (directional.RunnerResult, error) {
		close(astrodomeStarted)
		select {
		case <-ctx.Done():
			return directional.RunnerResult{}, ctx.Err()
		case <-releaseAstrodome:
		}
		path := filepath.Join(execution.Workspace, "astrodome.json")
		if err := os.WriteFile(path, []byte(`{"ok":true}`), 0o640); err != nil {
			return directional.RunnerResult{}, err
		}
		return directional.RunnerResult{
			DatasetPath: path, Provider: execution.Source.Provider, RunID: execution.Source.RunID,
			GridProfile: execution.Source.GridProfile, GeometryDigest: execution.Source.GeometryDigest,
		}, nil
	})); err != nil {
		t.Fatalf("register Astrodome: %v", err)
	}
	if err := coordinator.Start(context.Background()); err != nil {
		t.Fatalf("start coordinator: %v", err)
	}
	t.Cleanup(func() { _ = coordinator.Close() })
	startHorizonTestJobs(t, jobs)

	_, err := coordinator.Submit(context.Background(), directional.Request{
		Kind: directional.KindAstrodome, OwnerID: "telegram:100", IdempotencyKey: "astrodome-request",
		ScienceCacheKey: "astrodome-science", Source: directional.SourceIdentity{
			Provider: "ICON-EU", RunID: horizonTestRunID, GridProfile: "dense-v1",
			GeometryDigest: strings.Repeat("b", 64),
		}, Payload: json.RawMessage(`{"point":1}`),
	})
	if err != nil {
		t.Fatalf("submit Astrodome: %v", err)
	}
	select {
	case <-astrodomeStarted:
	case <-time.After(time.Second):
		t.Fatal("Astrodome did not become the shared active job")
	}

	messenger := newHorizonFakeMessenger()
	handler := mustHorizonActionHandler(t, jobs, "telegram", messenger)
	invokeHorizonAction(t, handler, horizonTestPayload(t, jobs, horizonTestButtonRequest()), 200, "en")
	time.Sleep(30 * time.Millisecond)
	if calls, _ := source.counts(); calls != 0 {
		t.Fatalf("Horizon started outside the shared FIFO while Astrodome was active: calls=%d", calls)
	}
	close(releaseAstrodome)
	waitHorizonTest(t, 2*time.Second, func() bool {
		messages, photos, _ := messenger.snapshot()
		return len(photos) == 1 || containsHorizonText(messages, "could not be calculated")
	})
	messages, photos, _ := messenger.snapshot()
	if len(photos) != 1 {
		t.Fatalf("Horizon was not delivered; messages=%q", messages)
	}
	if calls, maximum := source.counts(); calls != 1 || maximum != 1 {
		t.Fatalf("Horizon source calls/max = %d/%d, want 1/1", calls, maximum)
	}
}

func TestHorizonDirectionalRejectsWorkerScienceIdentityDrift(t *testing.T) {
	jobs := newHorizonTestJobs(t, filepath.Join(t.TempDir(), "horizon-cache"), &horizonFakeSource{currentRun: horizonTestRunID, supported: true}, HorizonJobsConfig{})
	request, _, err := jobs.canonicalButtonRequest(horizonTestButtonRequest(), languageEnglish)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	_, err = jobs.runDirectional(context.Background(), directional.Execution{
		JobID: "job_abcdefghijklmnopqrstuvwxyz", Kind: directional.KindHorizon,
		ScienceCacheKey: "different-worker-science-identity", Payload: payload, Workspace: t.TempDir(),
	})
	var coded directional.CodedError
	if !errors.As(err, &coded) || coded.Code != "science_identity_mismatch" {
		t.Fatalf("science identity drift error = %v", err)
	}
}

func TestHorizonSharedDirectionalOwnerLimitIncludesAstrodome(t *testing.T) {
	root := t.TempDir()
	source := &horizonFakeSource{currentRun: horizonTestRunID, supported: true}
	jobs := newHorizonTestJobs(t, filepath.Join(root, "horizon-cache"), source, HorizonJobsConfig{})
	coordinator := newBotDirectionalCoordinator(t, filepath.Join(root, "directional-cache"))
	if err := jobs.UseDirectionalCoordinator(coordinator); err != nil {
		t.Fatalf("UseDirectionalCoordinator: %v", err)
	}
	started := make(chan struct{})
	if err := coordinator.Register(directional.KindAstrodome, directional.RunnerFunc(func(ctx context.Context, _ directional.Execution) (directional.RunnerResult, error) {
		close(started)
		<-ctx.Done()
		return directional.RunnerResult{}, ctx.Err()
	})); err != nil {
		t.Fatal(err)
	}
	if err := coordinator.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = coordinator.Close() })
	startHorizonTestJobs(t, jobs)
	ticket, err := coordinator.Submit(context.Background(), directional.Request{
		Kind: directional.KindAstrodome, OwnerID: "telegram:42", IdempotencyKey: "astrodome-owner-request",
		ScienceCacheKey: "astrodome-owner-science", Source: directional.SourceIdentity{
			Provider: "ICON-EU", RunID: horizonTestRunID, GridProfile: "dense-v1",
			GeometryDigest: strings.Repeat("c", 64),
		}, Payload: json.RawMessage(`null`),
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("Astrodome did not start")
	}
	messenger := newHorizonFakeMessenger()
	handler := mustHorizonActionHandler(t, jobs, "telegram", messenger)
	invokeHorizonAction(t, handler, horizonTestPayload(t, jobs, horizonTestButtonRequest()), 42, "en")
	waitHorizonTest(t, time.Second, func() bool {
		messages, _, _ := messenger.snapshot()
		return containsHorizonText(messages, "another directional calculation")
	})
	if calls, _ := source.counts(); calls != 0 {
		t.Fatalf("Horizon source called despite cross-kind owner limit: %d", calls)
	}
	if err := ticket.Cancel(); err != nil {
		t.Fatalf("cancel Astrodome: %v", err)
	}
}

func TestHorizonCancelUserCancelsSharedDirectionalTicket(t *testing.T) {
	root := t.TempDir()
	source := &horizonFakeSource{currentRun: horizonTestRunID, supported: true}
	jobs := newHorizonTestJobs(t, filepath.Join(root, "horizon-cache"), source, HorizonJobsConfig{})
	coordinator := newBotDirectionalCoordinator(t, filepath.Join(root, "directional-cache"))
	started := make(chan struct{})
	cancelled := make(chan error, 1)
	runner := directional.RunnerFunc(func(ctx context.Context, _ directional.Execution) (directional.RunnerResult, error) {
		close(started)
		<-ctx.Done()
		cancelled <- ctx.Err()
		return directional.RunnerResult{}, ctx.Err()
	})
	if err := jobs.UseDirectionalCoordinatorWithRunner(coordinator, runner); err != nil {
		t.Fatal(err)
	}
	if err := coordinator.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = coordinator.Close() })
	startHorizonTestJobs(t, jobs)
	messenger := newHorizonFakeMessenger()
	if err := jobs.Deliver(context.Background(), "web", messenger, 42, 42, horizonTestButtonRequest(), "en"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("Horizon directional ticket did not start")
	}
	jobs.CancelUser("web", 42)
	select {
	case err := <-cancelled:
		if err == nil {
			t.Fatal("directional runner received a nil cancellation error")
		}
	case <-time.After(time.Second):
		t.Fatal("web cancellation did not reach the shared directional runner")
	}
}

func newBotDirectionalCoordinator(t *testing.T, root string) *directional.Coordinator {
	t.Helper()
	coordinator, err := directional.NewCoordinator(directional.Config{
		QueueCapacity: 4, CompletedLimit: 8, CompletedTTL: time.Hour, ResultRoot: root,
		Policies: map[directional.Kind]directional.KindPolicy{
			directional.KindHorizon:   {Estimate: time.Second, Timeout: 2 * time.Second},
			directional.KindAstrodome: {Estimate: time.Second, Timeout: 2 * time.Second},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return coordinator
}
