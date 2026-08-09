package model

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

const (
	BotCommandTotalLimit    = 6
	BotCommandSyncLimit     = 4
	WorkerCommandTotalLimit = 2
)

// CommandClass identifies whether a subprocess belongs to model acquisition.
// The sync subquota prevents acquisition from consuming every bot slot.
type CommandClass uint8

const (
	CommandOrdinary CommandClass = iota
	CommandSync
)

// CommandBudget is a goroutine-free hierarchical semaphore. Sync commands
// consume both the sync quota and the total quota; ordinary commands consume
// only the total quota.
type CommandBudget struct {
	total     chan struct{}
	syncQuota chan struct{}
}

// CommandPermit releases one acquired slot. Release is idempotent.
type CommandPermit struct {
	budget *CommandBudget
	class  CommandClass
	once   sync.Once
}

// CommandBudgetStats is an instantaneous diagnostic snapshot.
type CommandBudgetStats struct {
	TotalInUse int
	TotalLimit int
	SyncInUse  int
	SyncLimit  int
}

func NewCommandBudget(totalLimit, syncLimit int) (*CommandBudget, error) {
	if totalLimit < 1 {
		return nil, errors.New("command total limit must be positive")
	}
	if syncLimit < 0 || syncLimit > totalLimit {
		return nil, fmt.Errorf("command sync limit must be between zero and total limit")
	}
	return &CommandBudget{
		total:     make(chan struct{}, totalLimit),
		syncQuota: make(chan struct{}, syncLimit),
	}, nil
}

func NewBotCommandBudget() *CommandBudget {
	budget, err := NewCommandBudget(BotCommandTotalLimit, BotCommandSyncLimit)
	if err != nil {
		panic(err)
	}
	return budget
}

func NewWorkerCommandBudget() *CommandBudget {
	budget, err := NewCommandBudget(WorkerCommandTotalLimit, WorkerCommandTotalLimit)
	if err != nil {
		panic(err)
	}
	return budget
}

// Acquire waits for the required hierarchical slots and respects context
// cancellation without creating a helper goroutine.
func (budget *CommandBudget) Acquire(ctx context.Context, class CommandClass) (*CommandPermit, error) {
	if budget == nil || budget.total == nil || budget.syncQuota == nil {
		return nil, errors.New("command budget is not initialized")
	}
	if ctx == nil {
		return nil, errors.New("command budget context is required")
	}
	if class != CommandOrdinary && class != CommandSync {
		return nil, fmt.Errorf("unknown command class %d", class)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	if class == CommandSync {
		if cap(budget.syncQuota) == 0 {
			return nil, errors.New("sync commands are disabled for this budget")
		}
		if err := acquireCommandSlot(ctx, budget.syncQuota); err != nil {
			return nil, err
		}
		if err := acquireCommandSlot(ctx, budget.total); err != nil {
			<-budget.syncQuota
			return nil, err
		}
		return &CommandPermit{budget: budget, class: class}, nil
	}

	if err := acquireCommandSlot(ctx, budget.total); err != nil {
		return nil, err
	}
	return &CommandPermit{budget: budget, class: class}, nil
}

func acquireCommandSlot(ctx context.Context, slots chan struct{}) error {
	select {
	case slots <- struct{}{}:
		if err := ctx.Err(); err != nil {
			<-slots
			return err
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (permit *CommandPermit) Release() {
	if permit == nil || permit.budget == nil {
		return
	}
	permit.once.Do(func() {
		<-permit.budget.total
		if permit.class == CommandSync {
			<-permit.budget.syncQuota
		}
	})
}

func (budget *CommandBudget) Stats() CommandBudgetStats {
	if budget == nil {
		return CommandBudgetStats{}
	}
	return CommandBudgetStats{
		TotalInUse: len(budget.total), TotalLimit: cap(budget.total),
		SyncInUse: len(budget.syncQuota), SyncLimit: cap(budget.syncQuota),
	}
}
