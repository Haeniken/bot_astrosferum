package directional

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestExecutionGateSerializesIndependentRunnerInstances(t *testing.T) {
	root := t.TempDir()
	firstGate, err := NewExecutionGate(root, 1)
	if err != nil {
		t.Fatal(err)
	}
	secondGate, err := NewExecutionGate(root, 1)
	if err != nil {
		t.Fatal(err)
	}
	var concurrent atomic.Int32
	var maximum atomic.Int32
	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	secondStarted := make(chan struct{})
	makeRunner := func(started chan<- struct{}, release <-chan struct{}) Runner {
		return RunnerFunc(func(ctx context.Context, _ Execution) (RunnerResult, error) {
			current := concurrent.Add(1)
			defer concurrent.Add(-1)
			for observed := maximum.Load(); current > observed && !maximum.CompareAndSwap(observed, current); observed = maximum.Load() {
			}
			close(started)
			if release == nil {
				return RunnerResult{}, nil
			}
			select {
			case <-release:
				return RunnerResult{}, nil
			case <-ctx.Done():
				return RunnerResult{}, ctx.Err()
			}
		})
	}
	first, err := firstGate.Wrap(makeRunner(firstStarted, releaseFirst))
	if err != nil {
		t.Fatal(err)
	}
	second, err := secondGate.Wrap(makeRunner(secondStarted, nil))
	if err != nil {
		t.Fatal(err)
	}
	firstDone := make(chan error, 1)
	go func() {
		_, runErr := first.Run(context.Background(), Execution{Kind: KindHorizon})
		firstDone <- runErr
	}()
	select {
	case <-firstStarted:
	case <-time.After(time.Second):
		t.Fatal("first runner did not acquire the execution lease")
	}
	secondDone := make(chan error, 1)
	go func() {
		_, runErr := second.Run(context.Background(), Execution{Kind: KindAstrodome})
		secondDone <- runErr
	}()
	select {
	case <-secondStarted:
		t.Fatal("independent Astrodome runner started while Horizon held the execution lease")
	case <-time.After(50 * time.Millisecond):
	}
	close(releaseFirst)
	if err := <-firstDone; err != nil {
		t.Fatalf("first runner: %v", err)
	}
	select {
	case <-secondStarted:
	case <-time.After(time.Second):
		t.Fatal("second runner did not acquire the released execution lease")
	}
	if err := <-secondDone; err != nil {
		t.Fatalf("second runner: %v", err)
	}
	if got := maximum.Load(); got != 1 {
		t.Fatalf("maximum concurrent directional runners = %d, want 1", got)
	}
}

func TestExecutionGateWaitIsContextAware(t *testing.T) {
	root := t.TempDir()
	first, err := NewExecutionGate(root, 1)
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewExecutionGate(root, 1)
	if err != nil {
		t.Fatal(err)
	}
	held, err := first.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = held.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	if _, err := second.Acquire(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("blocked execution lease error = %v, want deadline exceeded", err)
	}
}

func TestExecutionGateLimitsAggregateToConfiguredSlots(t *testing.T) {
	root := t.TempDir()
	gates := make([]*ExecutionGate, 3)
	for index := range gates {
		gate, err := NewExecutionGate(root, 2)
		if err != nil {
			t.Fatal(err)
		}
		gates[index] = gate
	}
	first, err := gates[0].Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = first.Close() }()
	second, err := gates[1].Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = second.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	if _, err := gates[2].Acquire(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("third execution slot error = %v, want deadline exceeded", err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	reused, err := gates[2].Acquire(context.Background())
	if err != nil {
		t.Fatalf("acquire released execution slot: %v", err)
	}
	if err := reused.Close(); err != nil {
		t.Fatal(err)
	}
}
