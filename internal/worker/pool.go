package worker

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

type Stats struct {
	succeeded atomic.Int32
	failed    atomic.Int32
	startedAt time.Time
}

type Summary struct {
	Succeeded int
	Failed    int
	Duration  time.Duration
}

func NewStats() *Stats {
	return &Stats{startedAt: time.Now()}
}

func (stats *Stats) Summary() Summary {
	return Summary{
		Succeeded: int(stats.succeeded.Load()),
		Failed:    int(stats.failed.Load()),
		Duration:  time.Since(stats.startedAt),
	}
}

func Run[T any](ctx context.Context, jobs <-chan T, maxWorkers int, stats *Stats, process func(T) error) error {
	if maxWorkers < 1 {
		maxWorkers = 1
	}
	var waitGroup sync.WaitGroup
	var errorsMu sync.Mutex
	var workerErrors []error

	for range maxWorkers {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			for {
				if ctx.Err() != nil {
					return
				}
				var job T
				var ok bool
				select {
				case <-ctx.Done():
					return
				case job, ok = <-jobs:
					if !ok {
						return
					}
				}
				if err := process(job); err != nil {
					stats.failed.Add(1)
					errorsMu.Lock()
					workerErrors = append(workerErrors, err)
					errorsMu.Unlock()
				} else {
					stats.succeeded.Add(1)
				}
			}
		}()
	}

	waitGroup.Wait()
	if err := ctx.Err(); err != nil {
		workerErrors = append(workerErrors, err)
	}
	if len(workerErrors) == 0 {
		return nil
	}
	return fmt.Errorf("failed to process %d repositories: %w", stats.failed.Load(), errors.Join(workerErrors...))
}
