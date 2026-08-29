package worker

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
)

func TestRunProcessesEveryJob(t *testing.T) {
	t.Parallel()

	jobs := make(chan int, 100)
	for job := range 100 {
		jobs <- job
	}
	close(jobs)

	stats := NewStats()
	var processed atomic.Int32
	if err := Run(context.Background(), jobs, 8, stats, func(int) error {
		processed.Add(1)
		return nil
	}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if processed.Load() != 100 || stats.succeeded.Load() != 100 || stats.failed.Load() != 0 {
		t.Fatalf("processed=%d succeeded=%d failed=%d", processed.Load(), stats.succeeded.Load(), stats.failed.Load())
	}
}

func TestRunReturnsUnderlyingErrors(t *testing.T) {
	t.Parallel()

	want := errors.New("repository failed")
	jobs := make(chan int, 1)
	jobs <- 1
	close(jobs)

	stats := NewStats()
	err := Run(context.Background(), jobs, 1, stats, func(int) error { return want })
	if !errors.Is(err, want) {
		t.Fatalf("Run() error = %v", err)
	}
}

func TestRunStopsSchedulingAfterCancellation(t *testing.T) {
	t.Parallel()

	jobs := make(chan int, 100)
	for job := range 100 {
		jobs <- job
	}
	close(jobs)
	ctx, cancel := context.WithCancel(context.Background())
	stats := NewStats()
	var processed atomic.Int32
	err := Run(ctx, jobs, 1, stats, func(int) error {
		processed.Add(1)
		cancel()
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run() error = %v", err)
	}
	if processed.Load() != 1 {
		t.Fatalf("Run() processed %d jobs after cancellation", processed.Load())
	}
}
