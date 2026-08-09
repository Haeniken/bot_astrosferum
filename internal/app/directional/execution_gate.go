package directional

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"bot_astrosferum/internal/model"
)

const (
	directionalExecutionLeaseProvider = "directional-execution"
	directionalExecutionLeaseRunID    = "all-kinds"
	directionalExecutionLeaseDigest   = "directional-execution-v1"
	directionalExecutionPollInterval  = 20 * time.Millisecond
)

// ExecutionGate is the process-shared last line of defence for expensive
// directional work. The bot-side Coordinator owns the FIFO and the worker HTTP
// handler has the same N-slot guard. Advisory slot leases cap aggregate work
// across extra worker processes and the operator-only local Horizon CLI.
type ExecutionGate struct {
	leases      *model.RunLeaseManager
	concurrency int
	next        atomic.Uint64
}

func NewExecutionGate(lockRoot string, concurrency int) (*ExecutionGate, error) {
	if concurrency < 1 || concurrency > MaxConcurrency {
		return nil, fmt.Errorf("directional execution gate concurrency must be between 1 and %d", MaxConcurrency)
	}
	leases, err := model.NewRunLeaseManager(lockRoot)
	if err != nil {
		return nil, fmt.Errorf("initialize directional execution lease: %w", err)
	}
	return &ExecutionGate{leases: leases, concurrency: concurrency}, nil
}

// Acquire waits for any host-level directional slot and observes ctx while all
// configured slots are occupied. Closing the returned lease releases its slot.
func (gate *ExecutionGate) Acquire(ctx context.Context) (*model.RunLease, error) {
	if gate == nil || gate.leases == nil || gate.concurrency < 1 {
		return nil, errors.New("directional execution gate is not initialized")
	}
	if ctx == nil {
		return nil, errors.New("directional execution gate context is required")
	}
	start := int((gate.next.Add(1) - 1) % uint64(gate.concurrency))
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		for offset := range gate.concurrency {
			slot := (start + offset) % gate.concurrency
			lease, acquired, err := gate.leases.TryExclusive(
				directionalExecutionLeaseProvider,
				directionalExecutionSlotRunID(slot),
				directionalExecutionLeaseDigest,
			)
			if err != nil {
				return nil, fmt.Errorf("acquire directional execution slot %d: %w", slot+1, err)
			}
			if !acquired {
				continue
			}
			if err := ctx.Err(); err != nil {
				return nil, errors.Join(err, lease.Close())
			}
			return lease, nil
		}
		start = (start + 1) % gate.concurrency
		timer := time.NewTimer(directionalExecutionPollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

// Slot zero deliberately keeps the former one-slot identity so a rolling
// upgrade counts an old worker as one of the configured N slots.
func directionalExecutionSlotRunID(slot int) string {
	if slot == 0 {
		return directionalExecutionLeaseRunID
	}
	return fmt.Sprintf("%s-slot-%02d", directionalExecutionLeaseRunID, slot+1)
}

// Wrap applies the same host-level slot to one Horizon or Astrodome runner.
func (gate *ExecutionGate) Wrap(runner Runner) (Runner, error) {
	if gate == nil || gate.leases == nil || gate.concurrency < 1 {
		return nil, errors.New("directional execution gate is not initialized")
	}
	if runner == nil {
		return nil, errors.New("directional runner is required")
	}
	return RunnerFunc(func(ctx context.Context, execution Execution) (result RunnerResult, resultErr error) {
		lease, err := gate.Acquire(ctx)
		if err != nil {
			return RunnerResult{}, err
		}
		defer func() { resultErr = errors.Join(resultErr, lease.Close()) }()
		return runner.Run(ctx, execution)
	}), nil
}
