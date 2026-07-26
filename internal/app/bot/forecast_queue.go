package bot

import (
	"context"
	"errors"
	"sync"
)

// ForecastQueue limits ordinary forecast calculations across every enabled
// platform. Waiting happens in the platform request goroutine, so no second
// job lifecycle or worker pool is needed.
type ForecastQueue struct {
	slots chan struct{}

	mu      sync.Mutex
	waiting int
}

func NewForecastQueue(concurrency int) (*ForecastQueue, error) {
	if concurrency < 1 {
		return nil, errors.New("forecast concurrency must be positive")
	}
	return &ForecastQueue{slots: make(chan struct{}, concurrency)}, nil
}

// Wait enters the calculation pool. queued is called once, before waiting,
// with the user's current one-based queue position. The returned release
// function is safe to call more than once.
func (queue *ForecastQueue) Wait(ctx context.Context, queued func(int) error) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	select {
	case queue.slots <- struct{}{}:
		return queue.release(), nil
	default:
	}

	queue.mu.Lock()
	queue.waiting++
	position := queue.waiting
	queue.mu.Unlock()

	if queued != nil {
		if err := queued(position); err != nil {
			queue.leaveWaiting()
			return nil, err
		}
	}
	select {
	case queue.slots <- struct{}{}:
		queue.leaveWaiting()
		return queue.release(), nil
	case <-ctx.Done():
		queue.leaveWaiting()
		return nil, ctx.Err()
	}
}

func (queue *ForecastQueue) leaveWaiting() {
	queue.mu.Lock()
	queue.waiting--
	queue.mu.Unlock()
}

func (queue *ForecastQueue) release() func() {
	var once sync.Once
	return func() {
		once.Do(func() { <-queue.slots })
	}
}
