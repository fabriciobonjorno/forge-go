package jobs

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestSchedulerRunsJobsOnInterval(t *testing.T) {
	var counter atomic.Int32
	s := NewScheduler(SchedulerOptions{MaxConcurrentJobs: 2})
	err := s.Register("counter", ScheduleSpec{Every: 1500 * time.Millisecond}, func(ctx context.Context) error {
		counter.Add(1)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	s.Start()
	time.Sleep(4500 * time.Millisecond)
	err = s.Stop()
	if err != nil && err != context.Canceled {
		t.Fatalf("Stop error: %v", err)
	}
	count := counter.Load()
	if count < 2 {
		t.Fatalf("expected at least 2 runs, got %d", count)
	}
}

func TestSchedulerDailyAt(t *testing.T) {
	var counter atomic.Int32
	s := NewScheduler(SchedulerOptions{})
	now := time.Now().UTC()
	// Schedule for the next minute
	nextMinute := now.Add(time.Minute).Truncate(time.Minute)
	at := DailyTime{Hour: nextMinute.Hour(), Minute: nextMinute.Minute()}
	err := s.Register("daily", ScheduleSpec{DailyAt: &at}, func(ctx context.Context) error {
		counter.Add(1)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	s.Start()
	// Wait for the scheduled time plus a buffer
	wait := time.Until(nextMinute) + 2*time.Second
	if wait < 0 {
		wait = 2 * time.Second
	}
	time.Sleep(wait)
	err = s.Stop()
	if err != nil && err != context.Canceled {
		t.Fatalf("Stop error: %v", err)
	}
	if counter.Load() != 1 {
		t.Fatalf("expected exactly 1 run, got %d", counter.Load())
	}
}

func TestSchedulerValidatesSchedule(t *testing.T) {
	s := NewScheduler(SchedulerOptions{})
	// No schedule
	if err := s.Register("bad", ScheduleSpec{}, func(context.Context) error { return nil }); err == nil {
		t.Fatal("expected error for empty schedule")
	}
	// Multiple schedules
	if err := s.Register("bad", ScheduleSpec{Every: time.Second, DailyAt: &DailyTime{Hour: 1}}, func(context.Context) error { return nil }); err == nil {
		t.Fatal("expected error for multiple schedules")
	}
	// Invalid Every
	if err := s.Register("bad", ScheduleSpec{Every: 100 * time.Millisecond}, func(context.Context) error { return nil }); err == nil {
		t.Fatal("expected error for Every < 1 second")
	}
	// Valid Every
	if err := s.Register("good", ScheduleSpec{Every: time.Second}, func(context.Context) error { return nil }); err != nil {
		t.Fatalf("unexpected error for valid schedule: %v", err)
	}
}

func TestSchedulerConcurrencyBound(t *testing.T) {
	var running atomic.Int32
	var maxRunning atomic.Int32
	s := NewScheduler(SchedulerOptions{MaxConcurrentJobs: 2})
	err := s.Register("slow", ScheduleSpec{Every: 1500 * time.Millisecond}, func(ctx context.Context) error {
		current := running.Add(1)
		for {
			if current > maxRunning.Load() {
				maxRunning.Store(current)
			}
			select {
			case <-ctx.Done():
				running.Add(-1)
				return ctx.Err()
			case <-time.After(200 * time.Millisecond):
				running.Add(-1)
				return nil
			}
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	s.Start()
	time.Sleep(4500 * time.Millisecond)
	err = s.Stop()
	if err != nil && err != context.Canceled {
		t.Fatalf("Stop error: %v", err)
	}
	if maxRunning.Load() > 2 {
		t.Fatalf("concurrency exceeded bound: max running = %d", maxRunning.Load())
	}
}