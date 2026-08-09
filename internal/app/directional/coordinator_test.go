package directional

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type controlledRunner struct {
	started chan string
	release chan struct{}
	active  atomic.Int32
	maximum atomic.Int32
	calls   atomic.Int32
}

func newControlledRunner() *controlledRunner {
	return &controlledRunner{started: make(chan string, 16), release: make(chan struct{}, 16)}
}

func (runner *controlledRunner) Run(ctx context.Context, execution Execution) (RunnerResult, error) {
	runner.calls.Add(1)
	active := runner.active.Add(1)
	updateMaximum(&runner.maximum, active)
	defer runner.active.Add(-1)
	var name string
	_ = json.Unmarshal(execution.Payload, &name)
	select {
	case runner.started <- name:
	case <-ctx.Done():
		return RunnerResult{}, ctx.Err()
	}
	select {
	case <-runner.release:
	case <-ctx.Done():
		return RunnerResult{}, ctx.Err()
	}
	return writeRunnerDataset(execution, `{"ok":true}`)
}

func TestCoordinatorMixedKindsStrictFIFOAndSingleActive(t *testing.T) {
	runner := newControlledRunner()
	coordinator := newTestCoordinator(t, t.TempDir(), 8, 32, time.Hour, time.Now)
	registerBoth(t, coordinator, runner)
	startCoordinator(t, coordinator)

	first := submitTest(t, coordinator, KindHorizon, "owner-1", "one", "science-one", "horizon-1")
	if got := receiveString(t, runner.started); got != "horizon-1" {
		t.Fatalf("first started = %q", got)
	}
	second := submitTest(t, coordinator, KindAstrodome, "owner-2", "two", "science-two", "dome-2")
	third := submitTest(t, coordinator, KindHorizon, "owner-3", "three", "science-three", "horizon-3")
	secondStatus, err := second.Status()
	if err != nil || secondStatus.QueuePosition == nil || *secondStatus.QueuePosition != 1 {
		t.Fatalf("second status = %+v, %v", secondStatus, err)
	}
	thirdStatus, err := third.Status()
	if err != nil || thirdStatus.QueuePosition == nil || *thirdStatus.QueuePosition != 2 {
		t.Fatalf("third status = %+v, %v", thirdStatus, err)
	}

	runner.release <- struct{}{}
	if got := receiveString(t, runner.started); got != "dome-2" {
		t.Fatalf("second started = %q", got)
	}
	runner.release <- struct{}{}
	if got := receiveString(t, runner.started); got != "horizon-3" {
		t.Fatalf("third started = %q", got)
	}
	runner.release <- struct{}{}
	for _, ticket := range []*Ticket{first, second, third} {
		if _, err := ticket.Wait(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if maximum := runner.maximum.Load(); maximum != 1 {
		t.Fatalf("maximum mixed-kind active tasks = %d", maximum)
	}
}

func TestCoordinatorUsesConfiguredConcurrencyAcrossKinds(t *testing.T) {
	runner := newControlledRunner()
	coordinator, err := NewCoordinator(Config{
		QueueCapacity: 8, Concurrency: 2, CompletedLimit: 8, CompletedTTL: time.Hour,
		ResultRoot: t.TempDir(),
		Policies: map[Kind]KindPolicy{
			KindHorizon:   {Estimate: time.Second, Timeout: 2 * time.Second},
			KindAstrodome: {Estimate: time.Second, Timeout: 2 * time.Second},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	registerBoth(t, coordinator, runner)
	startCoordinator(t, coordinator)

	first := submitTest(t, coordinator, KindHorizon, "owner-1", "one", "science-one", "horizon")
	second := submitTest(t, coordinator, KindAstrodome, "owner-2", "two", "science-two", "astrodome")
	started := map[string]bool{
		receiveString(t, runner.started): true,
		receiveString(t, runner.started): true,
	}
	if !started["horizon"] || !started["astrodome"] || runner.maximum.Load() != 2 {
		t.Fatalf("configured concurrency did not run both kinds: started=%v maximum=%d", started, runner.maximum.Load())
	}
	runner.release <- struct{}{}
	runner.release <- struct{}{}
	for _, ticket := range []*Ticket{first, second} {
		if _, err := ticket.Wait(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
}

func TestCoordinatorQueueFullStillAllowsSingleflight(t *testing.T) {
	runner := newControlledRunner()
	coordinator := newTestCoordinator(t, t.TempDir(), 1, 16, time.Hour, time.Now)
	registerBoth(t, coordinator, runner)
	startCoordinator(t, coordinator)

	first := submitTest(t, coordinator, KindHorizon, "owner-a", "one", "science-one", "first")
	_ = receiveString(t, runner.started)
	second := submitTest(t, coordinator, KindAstrodome, "owner-b", "two", "science-two", "second")
	if _, err := coordinator.Submit(context.Background(), Request{
		Kind: KindHorizon, OwnerID: "owner-c", IdempotencyKey: "three",
		ScienceCacheKey: "science-three", Source: testSourceIdentity(), Payload: json.RawMessage(`"third"`),
	}); !errors.Is(err, ErrQueueFull) {
		t.Fatalf("third distinct submit error = %v", err)
	}
	joined, err := coordinator.Submit(context.Background(), Request{
		Kind: KindAstrodome, OwnerID: "owner-d", IdempotencyKey: "joined",
		ScienceCacheKey: "science-two", Source: testSourceIdentity(), Payload: json.RawMessage(`"unused"`),
	})
	if err != nil || joined.ID() != second.ID() {
		t.Fatalf("singleflight at queue cap = %q/%v, want %q", joined.ID(), err, second.ID())
	}
	runner.release <- struct{}{}
	_ = receiveString(t, runner.started)
	runner.release <- struct{}{}
	if _, err := first.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := joined.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestCoordinatorOwnershipIdempotencyAndCancellation(t *testing.T) {
	runner := newControlledRunner()
	coordinator := newTestCoordinator(t, t.TempDir(), 4, 16, time.Hour, time.Now)
	registerBoth(t, coordinator, runner)
	startCoordinator(t, coordinator)

	first := submitTest(t, coordinator, KindAstrodome, "owner-a", "idem-a", "same-science", "shared")
	_ = receiveString(t, runner.started)
	joined, err := coordinator.Submit(context.Background(), Request{
		Kind: KindAstrodome, OwnerID: "owner-b", IdempotencyKey: "idem-b",
		ScienceCacheKey: "same-science", Source: testSourceIdentity(), Payload: json.RawMessage(`"ignored"`),
	})
	if err != nil || joined.ID() != first.ID() {
		t.Fatalf("joined ticket = %q/%v, want %q", joined.ID(), err, first.ID())
	}
	idempotent, err := coordinator.Submit(context.Background(), Request{
		Kind: KindAstrodome, OwnerID: "owner-a", IdempotencyKey: "idem-a",
		ScienceCacheKey: "same-science", Source: testSourceIdentity(), Payload: json.RawMessage(`"ignored"`),
	})
	if err != nil || idempotent.ID() != first.ID() {
		t.Fatalf("idempotent ticket = %q/%v", idempotent.ID(), err)
	}
	if _, err := coordinator.Submit(context.Background(), Request{
		Kind: KindAstrodome, OwnerID: "owner-a", IdempotencyKey: "idem-a",
		ScienceCacheKey: "different-science", Source: testSourceIdentity(), Payload: json.RawMessage("null"),
	}); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("idempotency conflict error = %v", err)
	}
	if _, err := coordinator.Status("intruder", first.ID()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("intruder status error = %v", err)
	}
	if err := first.Cancel(); err != nil {
		t.Fatal(err)
	}
	if _, err := first.Status(); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cancelled owner status error = %v", err)
	}
	runner.release <- struct{}{}
	if _, err := joined.Wait(context.Background()); err != nil {
		t.Fatalf("remaining owner lost shared calculation: %v", err)
	}
	if calls := runner.calls.Load(); calls != 1 {
		t.Fatalf("singleflight runner calls = %d", calls)
	}
}

func TestCoordinatorAllowsOneActiveOrQueuedJobPerOwner(t *testing.T) {
	runner := newControlledRunner()
	coordinator := newTestCoordinator(t, t.TempDir(), 4, 16, time.Hour, time.Now)
	registerBoth(t, coordinator, runner)
	startCoordinator(t, coordinator)

	first := submitTest(t, coordinator, KindHorizon, "owner", "same-idempotency", "same-science", "first")
	_ = receiveString(t, runner.started)
	repeated, err := coordinator.Submit(context.Background(), Request{
		Kind: KindHorizon, OwnerID: "owner", IdempotencyKey: "same-idempotency",
		ScienceCacheKey: "same-science", Source: testSourceIdentity(), Payload: json.RawMessage(`"ignored"`),
	})
	if err != nil || repeated.ID() != first.ID() {
		t.Fatalf("same idempotent request = %q/%v, want %q", repeated.ID(), err, first.ID())
	}
	sameScience, err := coordinator.Submit(context.Background(), Request{
		Kind: KindHorizon, OwnerID: "owner", IdempotencyKey: "another-key",
		ScienceCacheKey: "same-science", Source: testSourceIdentity(), Payload: json.RawMessage(`"ignored"`),
	})
	if err != nil || sameScience.ID() != first.ID() {
		t.Fatalf("same science request = %q/%v, want %q", sameScience.ID(), err, first.ID())
	}
	if _, err := coordinator.Submit(context.Background(), Request{
		Kind: KindAstrodome, OwnerID: "owner", IdempotencyKey: "different",
		ScienceCacheKey: "different-science", Source: testSourceIdentity(), Payload: json.RawMessage("null"),
	}); !errors.Is(err, ErrOwnerBusy) {
		t.Fatalf("second mixed-kind owner job error = %v", err)
	}
	runner.release <- struct{}{}
	if _, err := first.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestCoordinatorSnapshotsMutablePayloadAndPinsQueuedSource(t *testing.T) {
	runner := newControlledRunner()
	coordinator := newTestCoordinator(t, t.TempDir(), 4, 16, time.Hour, time.Now)
	registerBoth(t, coordinator, runner)
	startCoordinator(t, coordinator)

	first := submitTest(t, coordinator, KindHorizon, "owner-1", "first", "science-first", "first")
	_ = receiveString(t, runner.started)
	payload := json.RawMessage(`"before"`)
	second, err := coordinator.Submit(context.Background(), Request{
		Kind: KindAstrodome, OwnerID: "owner-2", IdempotencyKey: "second",
		ScienceCacheKey: "science-second", Source: testSourceIdentity(), Payload: payload,
	})
	if err != nil {
		t.Fatal(err)
	}
	copy(payload, json.RawMessage(`"mutate"`))
	status, err := second.Status()
	wantSource := testSourceIdentity()
	if err != nil || status.Provider != wantSource.Provider || status.RunID != wantSource.RunID ||
		status.GridProfile != wantSource.GridProfile || status.GeometryDigest != wantSource.GeometryDigest {
		t.Fatalf("queued pinned source = %+v, %v", status, err)
	}
	runner.release <- struct{}{}
	if got := receiveString(t, runner.started); got != "before" {
		t.Fatalf("runner observed mutable caller payload %q", got)
	}
	runner.release <- struct{}{}
	if _, err := first.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := second.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestCoordinatorCancelsQueuedAndRunningJobs(t *testing.T) {
	runner := newControlledRunner()
	coordinator := newTestCoordinator(t, t.TempDir(), 4, 16, time.Hour, time.Now)
	registerBoth(t, coordinator, runner)
	startCoordinator(t, coordinator)

	running := submitTest(t, coordinator, KindHorizon, "owner-running", "running", "science-running", "running")
	_ = receiveString(t, runner.started)
	queued := submitTest(t, coordinator, KindAstrodome, "owner-queued", "queued", "science-queued", "must-not-run")
	if err := queued.Cancel(); err != nil {
		t.Fatal(err)
	}
	if stats := coordinator.Stats(); stats.QueueLength != 0 || !stats.Running {
		t.Fatalf("stats after queued cancel = %+v", stats)
	}
	if err := running.Cancel(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for coordinator.Stats().Running && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if coordinator.Stats().Running {
		t.Fatal("running cancellation did not stop the runner")
	}
	select {
	case name := <-runner.started:
		t.Fatalf("cancelled queued job started as %q", name)
	default:
	}
}

func TestCoordinatorPerKindTimeout(t *testing.T) {
	coordinator := newTestCoordinatorWithPolicies(t, t.TempDir(), 4, 16, time.Hour, time.Now, map[Kind]KindPolicy{
		KindHorizon:   {Estimate: 10 * time.Millisecond, Timeout: 30 * time.Millisecond},
		KindAstrodome: {Estimate: time.Second, Timeout: time.Second},
	})
	blocking := RunnerFunc(func(ctx context.Context, _ Execution) (RunnerResult, error) {
		<-ctx.Done()
		return RunnerResult{}, ctx.Err()
	})
	if err := coordinator.Register(KindHorizon, blocking); err != nil {
		t.Fatal(err)
	}
	if err := coordinator.Register(KindAstrodome, blocking); err != nil {
		t.Fatal(err)
	}
	startCoordinator(t, coordinator)
	ticket := submitTest(t, coordinator, KindHorizon, "owner", "timeout", "science-timeout", nil)
	if _, err := ticket.Wait(context.Background()); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timed out wait error = %v", err)
	}
	status, err := ticket.Status()
	if err != nil || status.State != StateFailed || status.FailureCode != "timeout" {
		t.Fatalf("timeout status = %+v, %v", status, err)
	}
}

func TestOptionalTimeoutZeroRemainsLiveUntilCancelled(t *testing.T) {
	ctx, cancel := contextWithOptionalTimeout(context.Background(), 0)
	select {
	case <-ctx.Done():
		t.Fatalf("disabled timeout cancelled immediately: %v", ctx.Err())
	default:
	}
	cancel()
	select {
	case <-ctx.Done():
		if !errors.Is(ctx.Err(), context.Canceled) {
			t.Fatalf("explicit cancellation error = %v", ctx.Err())
		}
	case <-time.After(time.Second):
		t.Fatal("explicit cancellation did not propagate")
	}

	coordinator := newTestCoordinatorWithPolicies(t, t.TempDir(), 4, 16, time.Hour, time.Now, map[Kind]KindPolicy{
		KindHorizon:   {Estimate: time.Second, Timeout: time.Second},
		KindAstrodome: {Estimate: 30 * time.Minute, Timeout: 0},
	})
	if coordinator == nil {
		t.Fatal("coordinator rejected disabled Astrodome timeout")
	}
}

func TestCoordinatorCompletedMetadataBoundAndTTL(t *testing.T) {
	var unixNano atomic.Int64
	unixNano.Store(time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC).UnixNano())
	now := func() time.Time { return time.Unix(0, unixNano.Load()) }
	coordinator := newTestCoordinator(t, t.TempDir(), 4, 2, time.Hour, now)
	quick := RunnerFunc(func(_ context.Context, execution Execution) (RunnerResult, error) {
		return writeRunnerDataset(execution, `{"ok":true}`)
	})
	registerBoth(t, coordinator, quick)
	startCoordinator(t, coordinator)
	var tickets []*Ticket
	for index := range 3 {
		unixNano.Add(int64(time.Second))
		ticket := submitTest(t, coordinator, KindHorizon, "owner", fmt.Sprintf("idem-%d", index), fmt.Sprintf("science-%d", index), index)
		if _, err := ticket.Wait(context.Background()); err != nil {
			t.Fatal(err)
		}
		tickets = append(tickets, ticket)
	}
	if _, err := tickets[0].Status(); !errors.Is(err, ErrNotFound) {
		t.Fatalf("oldest bounded metadata error = %v", err)
	}
	unixNano.Add(int64(2 * time.Hour))
	for _, ticket := range tickets[1:] {
		if _, err := ticket.Status(); !errors.Is(err, ErrNotFound) {
			t.Fatalf("expired metadata error = %v", err)
		}
	}
}

func TestCoordinatorRecoversCacheAndRemovesPartialResults(t *testing.T) {
	root := t.TempDir()
	firstCalls := atomic.Int32{}
	first := newTestCoordinator(t, root, 4, 16, time.Hour, time.Now)
	quick := RunnerFunc(func(_ context.Context, execution Execution) (RunnerResult, error) {
		firstCalls.Add(1)
		return writeRunnerDataset(execution, `{"cached":true}`)
	})
	registerBoth(t, first, quick)
	if err := first.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	ticket := submitTest(t, first, KindAstrodome, "owner", "first", "stable-cache-key", nil)
	firstResult, err := ticket.Wait(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	partialWorkspace := filepath.Join(root, "staging", "orphan")
	partialPublish := filepath.Join(root, "entries", ".publish-orphan")
	partialFinal := filepath.Join(root, "entries", strings.Repeat("a", 64))
	for _, path := range []string{partialWorkspace, partialPublish, partialFinal} {
		if err := os.MkdirAll(path, 0o750); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(partialFinal, "dataset.json"), []byte("partial"), 0o640); err != nil {
		t.Fatal(err)
	}

	second := newTestCoordinator(t, root, 4, 16, time.Hour, time.Now)
	secondCalls := atomic.Int32{}
	unexpected := RunnerFunc(func(_ context.Context, execution Execution) (RunnerResult, error) {
		secondCalls.Add(1)
		return writeRunnerDataset(execution, `{}`)
	})
	registerBoth(t, second, unexpected)
	startCoordinator(t, second)
	cachedTicket := submitTest(t, second, KindAstrodome, "owner", "second", "stable-cache-key", nil)
	cachedResult, err := cachedTicket.Wait(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if firstCalls.Load() != 1 || secondCalls.Load() != 0 || cachedResult.ETag != firstResult.ETag {
		t.Fatalf("cache recovery calls/etag = %d/%d %q/%q", firstCalls.Load(), secondCalls.Load(), cachedResult.ETag, firstResult.ETag)
	}
	for _, path := range []string{partialWorkspace, partialPublish, partialFinal} {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("partial path %q survived recovery: %v", path, err)
		}
	}
}

func TestCoordinatorRejectsSameSizeCacheCorruptionOnOpenAndRecovery(t *testing.T) {
	root := t.TempDir()
	first := newTestCoordinator(t, root, 4, 16, time.Hour, time.Now)
	quick := RunnerFunc(func(_ context.Context, execution Execution) (RunnerResult, error) {
		return writeRunnerDataset(execution, `{"good":true}`)
	})
	registerBoth(t, first, quick)
	if err := first.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	ticket := submitTest(t, first, KindAstrodome, "owner", "first", "corruptible", nil)
	result, err := ticket.Wait(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(result.Path, 0o640); err != nil {
		t.Fatal(err)
	}
	corrupt := []byte(`{"evil":true}`)
	if int64(len(corrupt)) != result.Bytes {
		t.Fatalf("test corruption changed size: %d != %d", len(corrupt), result.Bytes)
	}
	if err := os.WriteFile(result.Path, corrupt, 0o640); err != nil {
		t.Fatal(err)
	}
	if file, _, err := first.OpenResult("owner", ticket.ID()); file != nil || !errors.Is(err, ErrNotFound) {
		t.Fatalf("same-size corrupt open = %v/%v", file, err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	second := newTestCoordinator(t, root, 4, 16, time.Hour, time.Now)
	var calls atomic.Int32
	rebuilt := RunnerFunc(func(_ context.Context, execution Execution) (RunnerResult, error) {
		calls.Add(1)
		return writeRunnerDataset(execution, `{"good":true}`)
	})
	registerBoth(t, second, rebuilt)
	startCoordinator(t, second)
	retry := submitTest(t, second, KindAstrodome, "owner", "second", "corruptible", nil)
	rebuiltResult, err := retry.Wait(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatalf("corrupt cache was reused; rebuild calls = %d", calls.Load())
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(rebuiltResult.Path, 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(rebuiltResult.Path, corrupt, 0o640); err != nil {
		t.Fatal(err)
	}

	third := newTestCoordinator(t, root, 4, 16, time.Hour, time.Now)
	var recoveryCalls atomic.Int32
	recovered := RunnerFunc(func(_ context.Context, execution Execution) (RunnerResult, error) {
		recoveryCalls.Add(1)
		return writeRunnerDataset(execution, `{"good":true}`)
	})
	registerBoth(t, third, recovered)
	startCoordinator(t, third)
	thirdTicket := submitTest(t, third, KindAstrodome, "owner", "third", "corruptible", nil)
	if _, err := thirdTicket.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	if recoveryCalls.Load() != 1 {
		t.Fatalf("startup recovery reused same-size corrupt cache; calls = %d", recoveryCalls.Load())
	}
}

func TestCoordinatorRejectsRunnerSourceMismatch(t *testing.T) {
	coordinator := newTestCoordinator(t, t.TempDir(), 2, 8, time.Hour, time.Now)
	mismatch := RunnerFunc(func(_ context.Context, execution Execution) (RunnerResult, error) {
		result, err := writeRunnerDataset(execution, `{}`)
		result.RunID = "2026072806"
		return result, err
	})
	registerBoth(t, coordinator, mismatch)
	startCoordinator(t, coordinator)
	ticket := submitTest(t, coordinator, KindAstrodome, "owner", "mismatch", "mismatch", nil)
	if _, err := ticket.Wait(context.Background()); err == nil {
		t.Fatal("runner source mismatch succeeded")
	}
	status, err := ticket.Status()
	if err != nil || status.State != StateFailed || status.FailureCode != "source_mismatch" {
		t.Fatalf("source mismatch status = %+v, %v", status, err)
	}
}

func TestCoordinatorCloseCancelsActiveAndQueued(t *testing.T) {
	runner := newControlledRunner()
	coordinator := newTestCoordinator(t, t.TempDir(), 4, 8, time.Hour, time.Now)
	registerBoth(t, coordinator, runner)
	if err := coordinator.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	active := submitTest(t, coordinator, KindHorizon, "owner-active", "active", "active", "active")
	_ = receiveString(t, runner.started)
	queued := submitTest(t, coordinator, KindAstrodome, "owner-queued", "queued", "queued", "queued")
	if err := coordinator.Close(); err != nil {
		t.Fatal(err)
	}
	for _, ticket := range []*Ticket{active, queued} {
		if _, err := ticket.Wait(context.Background()); !errors.Is(err, context.Canceled) {
			t.Fatalf("closed ticket wait error = %v", err)
		}
	}
	if stats := coordinator.Stats(); stats.Running || stats.QueueLength != 0 {
		t.Fatalf("closed coordinator stats = %+v", stats)
	}
}

func newTestCoordinator(t *testing.T, root string, capacity, completed int, ttl time.Duration, now func() time.Time) *Coordinator {
	t.Helper()
	return newTestCoordinatorWithPolicies(t, root, capacity, completed, ttl, now, map[Kind]KindPolicy{
		KindHorizon:   {Estimate: 100 * time.Millisecond, Timeout: 2 * time.Second},
		KindAstrodome: {Estimate: 200 * time.Millisecond, Timeout: 2 * time.Second},
	})
}

func newTestCoordinatorWithPolicies(t *testing.T, root string, capacity, completed int, ttl time.Duration, now func() time.Time, policies map[Kind]KindPolicy) *Coordinator {
	t.Helper()
	coordinator, err := NewCoordinator(Config{
		QueueCapacity: capacity, CompletedLimit: completed, CompletedTTL: ttl,
		ResultRoot: root, Policies: policies, Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	return coordinator
}

func registerBoth(t *testing.T, coordinator *Coordinator, runner Runner) {
	t.Helper()
	for _, kind := range []Kind{KindHorizon, KindAstrodome} {
		if err := coordinator.Register(kind, runner); err != nil {
			t.Fatal(err)
		}
	}
}

func startCoordinator(t *testing.T, coordinator *Coordinator) {
	t.Helper()
	if err := coordinator.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := coordinator.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
}

func submitTest(t *testing.T, coordinator *Coordinator, kind Kind, owner, idempotency, science string, payload any) *Ticket {
	t.Helper()
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	ticket, err := coordinator.Submit(context.Background(), Request{
		Kind: kind, OwnerID: owner, IdempotencyKey: idempotency,
		ScienceCacheKey: science, Source: testSourceIdentity(), Payload: encoded,
	})
	if err != nil {
		t.Fatal(err)
	}
	return ticket
}

func writeRunnerDataset(execution Execution, body string) (RunnerResult, error) {
	path := filepath.Join(execution.Workspace, "dataset.json.gz")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		return RunnerResult{}, err
	}
	return RunnerResult{
		DatasetPath: path, ContentEncoding: "gzip", Provider: execution.Source.Provider,
		RunID: execution.Source.RunID, GridProfile: execution.Source.GridProfile,
		GeometryDigest: execution.Source.GeometryDigest,
	}, nil
}

func testSourceIdentity() SourceIdentity {
	return SourceIdentity{
		Provider: "ICON-EU", RunID: "2026072800", GridProfile: "dense-v1",
		GeometryDigest: strings.Repeat("a", 64),
	}
}

func receiveString(t *testing.T, channel <-chan string) string {
	t.Helper()
	select {
	case value := <-channel:
		return value
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for runner")
		return ""
	}
}

func updateMaximum(maximum *atomic.Int32, candidate int32) {
	for {
		current := maximum.Load()
		if candidate <= current || maximum.CompareAndSwap(current, candidate) {
			return
		}
	}
}
