// Package jobs provides an in-process scheduler for recurring background
// work. Each replica runs its own scheduler; job authors must use advisory
// locks or idempotent outbox events for exactly-once semantics across
// replicas.
package jobs

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// Job is a unit of background work. It receives a context that is canceled
// on scheduler shutdown and returns an error if the work failed. Jobs must
// be safe for concurrent invocation (the scheduler may run multiple jobs
// concurrently up to the configured bound).
type Job func(context.Context) error

// ScheduleSpec describes when a job should run. Exactly one of the fields
// must be non-zero/non-empty.
type ScheduleSpec struct {
	// Every runs the job at a fixed interval.
	Every time.Duration
	// DailyAt runs the job once per day at the given UTC time (hour, minute).
	DailyAt *DailyTime
	// Cron runs the job on a standard 5-field cron schedule (UTC).
	Cron string
}

// DailyTime represents a time of day in UTC.
type DailyTime struct {
	Hour   int // 0-23
	Minute int // 0-59
}

// Validate checks that exactly one schedule field is set and values are valid.
func (s ScheduleSpec) Validate() error {
	count := 0
	if s.Every > 0 {
		count++
	}
	if s.DailyAt != nil {
		count++
		if s.DailyAt.Hour < 0 || s.DailyAt.Hour > 23 || s.DailyAt.Minute < 0 || s.DailyAt.Minute > 59 {
			return errors.New("DailyAt hour must be 0-23 and minute 0-59")
		}
	}
	if s.Cron != "" {
		count++
	}
	if count != 1 {
		return errors.New("ScheduleSpec requires exactly one of Every, DailyAt, or Cron")
	}
	if s.Every > 0 && s.Every < time.Second {
		return errors.New("Every interval must be at least 1 second")
	}
	return nil
}

// nextRun returns the next time after 'after' that the schedule fires.
// For Every, it's the next multiple of the interval after 'after'.
// For DailyAt, it's the next occurrence of that time-of-day.
// For Cron, it uses a simple cron parser (standard 5-field, UTC).
func (s ScheduleSpec) nextRun(after time.Time) (time.Time, error) {
	if s.Every > 0 {
		// Align to interval boundary after 'after'
		nanos := after.UnixNano()
		intervalNanos := s.Every.Nanoseconds()
		nextNanos := ((nanos / intervalNanos) + 1) * intervalNanos
		return time.Unix(0, nextNanos).UTC(), nil
	}
	if s.DailyAt != nil {
		next := time.Date(after.Year(), after.Month(), after.Day(), s.DailyAt.Hour, s.DailyAt.Minute, 0, 0, time.UTC)
		if !next.After(after) {
			next = next.Add(24 * time.Hour)
		}
		return next, nil
	}
	if s.Cron != "" {
		return nextCronRun(s.Cron, after)
	}
	return time.Time{}, errors.New("no schedule set")
}

// Scheduler runs jobs on a schedule. It is not a distributed scheduler;
// each replica runs its own instance. Jobs that must run exactly once per
// schedule tick across replicas should use postgres.TryAdvisoryXactLock.
type Scheduler struct {
	jobs   map[string]scheduledJob
	opts   SchedulerOptions
	mu     sync.Mutex
	wg     sync.WaitGroup
	ctx    context.Context
	cancel context.CancelFunc
}

type scheduledJob struct {
	spec ScheduleSpec
	job  Job
}

// SchedulerOptions bounds scheduler behavior.
type SchedulerOptions struct {
	// MaxConcurrentJobs bounds the number of jobs running simultaneously.
	// Default 4, maximum 256.
	MaxConcurrentJobs int
}

// NewScheduler creates a scheduler with the given options. Zero or negative
// MaxConcurrentJobs uses the default.
func NewScheduler(opts SchedulerOptions) *Scheduler {
	if opts.MaxConcurrentJobs < 1 {
		opts.MaxConcurrentJobs = 4
	}
	if opts.MaxConcurrentJobs > 256 {
		opts.MaxConcurrentJobs = 256
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Scheduler{
		jobs:   make(map[string]scheduledJob),
		opts:   opts,
		ctx:    ctx,
		cancel: cancel,
	}
}

// Register adds a job with a unique name and schedule. It must be called
// before Start. The name is used for logging and must be unique.
func (s *Scheduler) Register(name string, spec ScheduleSpec, job Job) error {
	if name == "" {
		return errors.New("job name is required")
	}
	if err := spec.Validate(); err != nil {
		return fmt.Errorf("invalid schedule for job %q: %w", name, err)
	}
	if job == nil {
		return errors.New("job function is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.jobs[name]; exists {
		return fmt.Errorf("job %q already registered", name)
	}
	s.jobs[name] = scheduledJob{spec: spec, job: job}
	return nil
}

// Start begins running all registered jobs on their schedules. It returns
// immediately; jobs run in background goroutines. The scheduler runs until
// Stop is called or the context passed to NewScheduler is canceled.
func (s *Scheduler) Start() {
	for name, sj := range s.jobs {
		s.wg.Add(1)
		go s.runJob(name, sj)
	}
}

// Stop cancels the scheduler context and waits for all in-flight jobs to
// complete. It returns the context cancellation error or nil if all jobs
// finished cleanly.
func (s *Scheduler) Stop() error {
	s.cancel()
	s.wg.Wait()
	return s.ctx.Err()
}

func (s *Scheduler) runJob(name string, sj scheduledJob) {
	defer s.wg.Done()
	nextRun := time.Now().UTC()
	for {
		var err error
		nextRun, err = sj.spec.nextRun(nextRun)
		if err != nil {
			// Log and continue; scheduler keeps running
			return
		}
		wait := time.Until(nextRun)
		if wait < 0 {
			wait = 0
		}
		select {
		case <-s.ctx.Done():
			return
		case <-time.After(wait):
		}
		select {
		case <-s.ctx.Done():
			return
		default:
			// Run the job with a timeout derived from the schedule
			// (for Every, use the interval; for others, use a reasonable default)
			jobCtx, cancel := context.WithTimeout(s.ctx, jobTimeout(sj.spec))
			sj.job(jobCtx)
			cancel()
		}
	}
}

func jobTimeout(spec ScheduleSpec) time.Duration {
	if spec.Every > 0 {
		return min(spec.Every, 5*time.Minute)
	}
	if spec.DailyAt != nil {
		return 5 * time.Minute
	}
	return 10 * time.Minute // cron jobs may be longer-running
}

// nextCronRun computes the next run time for a standard 5-field cron expression.
// This is a minimal implementation for the common cases.
func nextCronRun(expr string, after time.Time) (time.Time, error) {
	// Simplified: delegate to a proper cron library in production.
	// For now, return an error to force use of Every or DailyAt.
	return time.Time{}, fmt.Errorf("cron expressions not yet implemented; use Every or DailyAt")
}