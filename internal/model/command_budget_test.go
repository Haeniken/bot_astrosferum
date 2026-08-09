package model

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestBotCommandBudgetPreservesOrdinaryCapacity(t *testing.T) {
	budget := NewBotCommandBudget()
	permits := make([]*CommandPermit, 0, BotCommandTotalLimit)
	for range BotCommandSyncLimit {
		permit, err := budget.Acquire(context.Background(), CommandSync)
		if err != nil {
			t.Fatal(err)
		}
		permits = append(permits, permit)
	}
	for range BotCommandTotalLimit - BotCommandSyncLimit {
		permit, err := budget.Acquire(context.Background(), CommandOrdinary)
		if err != nil {
			t.Fatal(err)
		}
		permits = append(permits, permit)
	}
	stats := budget.Stats()
	if stats.TotalInUse != 6 || stats.SyncInUse != 4 {
		t.Fatalf("budget stats = %+v", stats)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	if _, err := budget.Acquire(ctx, CommandOrdinary); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("seventh command error = %v", err)
	}
	for _, permit := range permits {
		permit.Release()
		permit.Release()
	}
}

func TestCommandBudgetCancellationReleasesPartialHierarchy(t *testing.T) {
	budget, err := NewCommandBudget(1, 1)
	if err != nil {
		t.Fatal(err)
	}
	held, err := budget.Acquire(context.Background(), CommandOrdinary)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, acquireErr := budget.Acquire(ctx, CommandSync)
		done <- acquireErr
	}()
	deadline := time.Now().Add(time.Second)
	for budget.Stats().SyncInUse != 1 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if budget.Stats().SyncInUse != 1 {
		t.Fatal("sync acquire never held its subquota")
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled acquire error = %v", err)
	}
	if stats := budget.Stats(); stats.SyncInUse != 0 || stats.TotalInUse != 1 {
		t.Fatalf("partial acquire leaked a slot: %+v", stats)
	}
	held.Release()
}

func TestCommandBudgetConcurrentLimits(t *testing.T) {
	budget := NewBotCommandBudget()
	var total, syncCount, maxTotal, maxSync atomic.Int32
	var wait sync.WaitGroup
	start := make(chan struct{})
	for index := range 96 {
		class := CommandOrdinary
		if index%2 == 0 {
			class = CommandSync
		}
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			permit, err := budget.Acquire(context.Background(), class)
			if err != nil {
				t.Errorf("Acquire: %v", err)
				return
			}
			currentTotal := total.Add(1)
			updateAtomicMaximum(&maxTotal, currentTotal)
			if class == CommandSync {
				currentSync := syncCount.Add(1)
				updateAtomicMaximum(&maxSync, currentSync)
			}
			time.Sleep(time.Millisecond)
			if class == CommandSync {
				syncCount.Add(-1)
			}
			total.Add(-1)
			permit.Release()
		}()
	}
	close(start)
	wait.Wait()
	if got := maxTotal.Load(); got > BotCommandTotalLimit {
		t.Fatalf("maximum total concurrency = %d", got)
	}
	if got := maxSync.Load(); got > BotCommandSyncLimit {
		t.Fatalf("maximum sync concurrency = %d", got)
	}
	if stats := budget.Stats(); stats.TotalInUse != 0 || stats.SyncInUse != 0 {
		t.Fatalf("slots leaked after concurrent run: %+v", stats)
	}
}

func TestWorkerCommandBudgetTotalLimit(t *testing.T) {
	budget := NewWorkerCommandBudget()
	first, _ := budget.Acquire(context.Background(), CommandSync)
	second, _ := budget.Acquire(context.Background(), CommandOrdinary)
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	if _, err := budget.Acquire(ctx, CommandOrdinary); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("third worker command error = %v", err)
	}
	first.Release()
	second.Release()
}

func updateAtomicMaximum(maximum *atomic.Int32, candidate int32) {
	for {
		current := maximum.Load()
		if candidate <= current || maximum.CompareAndSwap(current, candidate) {
			return
		}
	}
}
