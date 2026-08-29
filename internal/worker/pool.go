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

func NewStats() *Stats {
	return &Stats{startedAt: time.Now()}
}

func (stats *Stats) PrintSummary(workDir string) {
	fmt.Printf("\n📊 Summary:\n")
	fmt.Printf("✅ Successfully processed: %d repositories\n", stats.succeeded.Load())
	fmt.Printf("❌ Failed: %d repositories\n", stats.failed.Load())
	if workDir != "" {
		fmt.Printf("📁 Output directory: %s\n", workDir)
	}
	fmt.Printf("🕐 Total time: %v\n", time.Since(stats.startedAt).Round(time.Second))
}

func Run[T any](ctx context.Context, jobs <-chan T, maxWorkers int, stats *Stats, process func(T) error) error {
	if maxWorkers < 1 {
		maxWorkers = 1
	}
	var waitGroup sync.WaitGroup
	var errorsMu sync.Mutex
	var workerErrors []error
	fmt.Printf("Processing repositories with %d worker(s)...\n", maxWorkers)

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
		fmt.Println("Repository processing complete.")
		return nil
	}

	for _, err := range workerErrors {
		fmt.Printf("Error processing: %v\n", err)
	}
	return fmt.Errorf("failed to process %d repositories: %w", stats.failed.Load(), errors.Join(workerErrors...))
}
