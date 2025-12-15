package document

import (
	"context"
	"fmt"
	"time"

	"golang.org/x/sync/errgroup"
)

// PeriodicWorker описывает фонового исполнителя, работающего с заданным интервалом.
type PeriodicWorker struct {
	Name     string
	Interval time.Duration
	Run      func(ctx context.Context, interval time.Duration) error
}

// RunBackgroundWorkers запускает все переданные воркеры и завершает работу при первой ошибке.
func RunBackgroundWorkers(ctx context.Context, workers ...PeriodicWorker) error {
	if len(workers) == 0 {
		return nil
	}

	group, ctx := errgroup.WithContext(ctx)

	for i := range workers {
		worker := workers[i]
		group.Go(func() error {
			if worker.Run == nil {
				return fmt.Errorf("worker %s has no run function", worker.Name)
			}

			if err := worker.Run(ctx, worker.Interval); err != nil {
				if worker.Name != "" {
					return fmt.Errorf("%s: %w", worker.Name, err)
				}
				return err
			}

			return nil
		})
	}

	return group.Wait()
}
