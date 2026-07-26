package bot

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestForecastQueueLimitsConcurrencyAndReportsPositions(t *testing.T) {
	queue, err := NewForecastQueue(2)
	if err != nil {
		t.Fatal(err)
	}
	first, err := queue.Wait(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	second, err := queue.Wait(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer first()
	defer second()

	positions := make(chan int, 2)
	acquired := make(chan func(), 2)
	var group sync.WaitGroup
	for index := range 2 {
		group.Add(1)
		go func() {
			defer group.Done()
			release, waitError := queue.Wait(context.Background(), func(position int) error {
				positions <- position
				return nil
			})
			if waitError != nil {
				t.Errorf("Wait: %v", waitError)
				return
			}
			acquired <- release
		}()
		select {
		case position := <-positions:
			if position != index+1 {
				t.Fatalf("queue position = %d, want %d", position, index+1)
			}
		case <-time.After(time.Second):
			t.Fatal("queue position was not reported")
		}
	}

	first()
	releaseThird := <-acquired
	releaseThird()
	second()
	releaseFourth := <-acquired
	releaseFourth()
	group.Wait()
}

func TestForecastQueueWaitHonorsCancellation(t *testing.T) {
	queue, err := NewForecastQueue(1)
	if err != nil {
		t.Fatal(err)
	}
	release, err := queue.Wait(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer release()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := queue.Wait(ctx, nil); err == nil {
		t.Fatal("cancelled wait succeeded")
	}
}
