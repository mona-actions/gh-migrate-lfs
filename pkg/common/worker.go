package common

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pterm/pterm"
)

type ProcessStats struct {
	Processed int32
	Failed    int32
	StartTime time.Time
}

func NewProcessStats() *ProcessStats {
	return &ProcessStats{
		StartTime: time.Now(),
	}
}

func (s *ProcessStats) PrintSummary(workDir string) {
	fmt.Printf("\n📊 Summary:\n")
	fmt.Printf("✅ Successfully processed: %d repositories\n", s.Processed)
	fmt.Printf("❌ Failed: %d repositories\n", s.Failed)
	if workDir != "" {
		fmt.Printf("📁 Output directory: %s\n", workDir)
	}
	fmt.Printf("🕐 Total time: %v\n", time.Since(s.StartTime).Round(time.Second))
}

// WorkerPool manages a pool of workers processing repository operations
func WorkerPool[T any](
	jobs chan T,
	maxWorkers int,
	stats *ProcessStats,
	processFunc func(T) error,
) error {
	var wg sync.WaitGroup
	spinner, _ := pterm.DefaultSpinner.Start("Processing repositories...")

	// Start worker pool
	for i := 0; i < maxWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for job := range jobs {
				if err := processFunc(job); err != nil {
					fmt.Printf("Error processing: %v\n", err)
					atomic.AddInt32(&stats.Failed, 1)
				} else {
					atomic.AddInt32(&stats.Processed, 1)
				}
			}
		}()
	}

	// Wait for all workers to complete
	wg.Wait()
	spinner.Success()

	if stats.Failed > 0 {
		return fmt.Errorf("failed to process %d repositories", stats.Failed)
	}

	return nil
}
