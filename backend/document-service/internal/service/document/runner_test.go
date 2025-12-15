package document

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestRunBackgroundWorkersStopsOthersOnError(t *testing.T) {
	ctx := context.Background()

	stopped := make(chan struct{})

	failing := PeriodicWorker{
		Name:     "failing",
		Interval: time.Millisecond,
		Run: func(context.Context, time.Duration) error {
			return errors.New("boom")
		},
	}

	waiting := PeriodicWorker{
		Name:     "waiting",
		Interval: time.Hour,
		Run: func(ctx context.Context, _ time.Duration) error {
			<-ctx.Done()
			close(stopped)
			return ctx.Err()
		},
	}

	err := RunBackgroundWorkers(ctx, failing, waiting)
	if err == nil {
		t.Fatalf("expected runner to return error")
	}

	select {
	case <-stopped:
	default:
		t.Fatalf("expected secondary worker to stop after failure")
	}
}
