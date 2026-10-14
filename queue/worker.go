package queue

import (
	"context"
	cryptorand "crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"runtime/debug"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/fabriciobonjorno/forge-go/uuid"
)

const (
	maxQueueConcurrency = 256
	maxTotalConcurrency = 1024
	maxWorkerDuration   = 24 * time.Hour
	maxWorkerIDLength   = 128

	defaultConcurrency         = 5
	defaultPollInterval        = time.Second
	defaultMaxPollInterval     = 10 * time.Second
	defaultLease               = time.Minute
	defaultShutdownTimeout     = 30 * time.Second
	defaultReleaseTimeout      = 5 * time.Second
	defaultRetainSucceeded     = 24 * time.Hour
	defaultMaintenanceInterval = time.Minute
	minLease                   = 10 * time.Millisecond

	claimTimeout   = 30 * time.Second
	persistTimeout = 10 * time.Second
	pruneBatch     = 1000
	maxPruneRounds = 10
	maxClaimErrGap = 30 * time.Second
)

var (
	errShutdown       = errors.New("queue: worker is shutting down")
	errHandlerTimeout = errors.New("queue: handler timeout")
)

// QueueConfig bounds how many jobs of one queue run at once in this worker.
type QueueConfig struct {
	Name        string
	Concurrency int // default 5, at most 256
}

// Outcome says how an attempt ended.
type Outcome string

const (
	OutcomeSucceeded Outcome = "succeeded"
	// OutcomeRetry means the attempt failed and another is scheduled.
	OutcomeRetry Outcome = "retry"
	// OutcomeDead means the job failed for good.
	OutcomeDead Outcome = "dead"
	// OutcomeReleased means shutdown interrupted the attempt, which was not
	// counted.
	OutcomeReleased Outcome = "released"
	// OutcomeLeaseLost means another worker owns the job now; this worker's
	// result was discarded.
	OutcomeLeaseLost Outcome = "lease_lost"
)

// Result describes one finished attempt for Hooks.Finished. Error is the
// sanitized text also stored with the job, never the payload.
type Result struct {
	Job      Job
	Outcome  Outcome
	Duration time.Duration
	Error    string
	// RetryIn is the delay before the next attempt for OutcomeRetry.
	RetryIn time.Duration
}

// Hooks observe a worker without importing a telemetry library: bridge them
// to logs, metrics or traces in the application. Hooks run on the worker's
// goroutines; keep them fast. A panic in a hook is recovered and logged.
type Hooks struct {
	Started  func(Job)
	Finished func(Result)
}

// WorkerOptions configure a Worker. Zero values select the documented
// defaults; unsafe values are rejected.
type WorkerOptions struct {
	// Queues lists the queues to work and their concurrency. The default is
	// DefaultQueue with 5 concurrent jobs.
	Queues []QueueConfig
	// WorkerID labels leases for diagnostics; it defaults to host, process ID
	// and a random suffix, and must be unique per worker.
	WorkerID string
	// PollInterval is the delay between claim attempts of an idle queue. It
	// grows exponentially up to MaxPollInterval while the queue stays empty
	// and resets as soon as a job is found; every sleep is jittered. Defaults
	// 1s and 10s.
	PollInterval    time.Duration
	MaxPollInterval time.Duration
	// Lease is how long a claim lasts without renewal. A running handler
	// renews it every Lease/3. A crashed worker's jobs are reclaimed at most
	// Lease after its last renewal. Default 1 minute.
	Lease time.Duration
	// ShutdownTimeout is how long Run drains in-flight jobs after its
	// context ends. Default 30s. Applications that call Shutdown themselves
	// (for example from App.OnShutdown) pass their own deadline instead.
	ShutdownTimeout time.Duration
	// ReleaseTimeout is how long Shutdown waits, after canceling handlers
	// that outlived its deadline, for them to return before releasing their
	// leases anyway. Default 5s.
	ReleaseTimeout time.Duration
	// Backoff is the default retry delay policy.
	Backoff Backoff
	// RetainSucceeded is how long finished jobs stay before the worker's
	// maintenance deletes them. Default 24h; a negative value keeps them
	// forever (prune with the jobs command instead).
	RetainSucceeded     time.Duration
	MaintenanceInterval time.Duration // default 1 minute
	Logger              *slog.Logger
	Hooks               Hooks
}

// Worker claims jobs from a Store and runs them with the handlers of a
// Registry. Construct one per process; Start it from the application's
// composition root and register Shutdown with App.OnShutdown.
type Worker struct {
	store    Store
	registry *Registry
	opts     WorkerOptions
	logger   *slog.Logger

	mu       sync.Mutex
	state    workerState
	kinds    []string
	pollStop context.CancelFunc
	jobCtx   context.Context
	jobStop  context.CancelCauseFunc
	pollers  sync.WaitGroup
	jobs     sync.WaitGroup
	inflight map[uuid.UUID]*running
	done     chan struct{}
	stopErr  error
}

type workerState int

const (
	stateIdle workerState = iota
	stateRunning
	stateStopping
	stateStopped
)

// running tracks one claimed job so that exactly one party settles it: the
// job's own goroutine or a forced Shutdown.
type running struct {
	job     Job
	lease   Lease
	settled atomic.Bool
}

// NewWorker validates opts and returns a stopped worker.
func NewWorker(store Store, registry *Registry, opts WorkerOptions) (*Worker, error) {
	if store == nil {
		return nil, errors.New("queue: store is required")
	}
	if registry == nil {
		return nil, errors.New("queue: registry is required")
	}
	opts, err := normalizeWorkerOptions(opts)
	if err != nil {
		return nil, err
	}
	logger := opts.Logger
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &Worker{
		store:    store,
		registry: registry,
		opts:     opts,
		logger:   logger.With("component", "queue", "worker_id", opts.WorkerID),
		inflight: make(map[uuid.UUID]*running),
		done:     make(chan struct{}),
	}, nil
}

func normalizeWorkerOptions(opts WorkerOptions) (WorkerOptions, error) {
	if len(opts.Queues) == 0 {
		opts.Queues = []QueueConfig{{Name: DefaultQueue}}
	}
	opts.Queues = slices.Clone(opts.Queues)
	seen := make(map[string]bool, len(opts.Queues))
	total := 0
	for i := range opts.Queues {
		q := &opts.Queues[i]
		if err := ValidateName(q.Name); err != nil {
			return opts, fmt.Errorf("queue: worker queue %q: %w", q.Name, err)
		}
		if seen[q.Name] {
			return opts, fmt.Errorf("queue: worker queue %q is listed twice", q.Name)
		}
		seen[q.Name] = true
		if q.Concurrency < 0 || q.Concurrency > maxQueueConcurrency {
			return opts, fmt.Errorf("queue: concurrency of %q must be between 1 and %d", q.Name, maxQueueConcurrency)
		}
		if q.Concurrency == 0 {
			q.Concurrency = defaultConcurrency
		}
		total += q.Concurrency
	}
	if total > maxTotalConcurrency {
		return opts, fmt.Errorf("queue: total worker concurrency must not exceed %d", maxTotalConcurrency)
	}
	if opts.WorkerID == "" {
		opts.WorkerID = defaultWorkerID()
	}
	if len(opts.WorkerID) > maxWorkerIDLength || validateKey(opts.WorkerID) != nil {
		return opts, fmt.Errorf("queue: worker ID must be printable text of at most %d bytes", maxWorkerIDLength)
	}
	for _, d := range []time.Duration{opts.PollInterval, opts.MaxPollInterval, opts.Lease, opts.ShutdownTimeout, opts.ReleaseTimeout, opts.MaintenanceInterval} {
		if d < 0 || d > maxWorkerDuration {
			return opts, fmt.Errorf("queue: worker durations must be between 0 and %s", maxWorkerDuration)
		}
	}
	if opts.Lease != 0 && opts.Lease < minLease {
		return opts, fmt.Errorf("queue: lease must be at least %s", minLease)
	}
	if opts.PollInterval == 0 {
		opts.PollInterval = defaultPollInterval
	}
	if opts.MaxPollInterval == 0 {
		opts.MaxPollInterval = max(defaultMaxPollInterval, opts.PollInterval)
	}
	if opts.MaxPollInterval < opts.PollInterval {
		return opts, errors.New("queue: maximum poll interval must be at least the poll interval")
	}
	if opts.Lease == 0 {
		opts.Lease = defaultLease
	}
	if opts.ShutdownTimeout == 0 {
		opts.ShutdownTimeout = defaultShutdownTimeout
	}
	if opts.ReleaseTimeout == 0 {
		opts.ReleaseTimeout = defaultReleaseTimeout
	}
	if opts.MaintenanceInterval == 0 {
		opts.MaintenanceInterval = defaultMaintenanceInterval
	}
	if opts.RetainSucceeded == 0 {
		opts.RetainSucceeded = defaultRetainSucceeded
	}
	if opts.RetainSucceeded > 0 && opts.RetainSucceeded > 10*365*24*time.Hour {
		return opts, errors.New("queue: retention must not exceed ten years")
	}
	if err := opts.Backoff.validate(); err != nil {
		return opts, err
	}
	opts.Backoff = opts.Backoff.withDefaults()
	return opts, nil
}

func defaultWorkerID() string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "worker"
	}
	if len(host) > 64 {
		host = host[:64]
	}
	suffix := make([]byte, 4)
	_, _ = cryptorand.Read(suffix) // a failure leaves a zero suffix; the ID is a label, not a secret
	return fmt.Sprintf("%s-%d-%s", host, os.Getpid(), hex.EncodeToString(suffix))
}

// WorkerID returns the identifier written to the leases of this worker.
func (w *Worker) WorkerID() string { return w.opts.WorkerID }

// InFlight reports how many jobs this worker is running now.
func (w *Worker) InFlight() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.inflight)
}

// Start launches the pollers and returns. Canceling ctx begins a graceful
// stop (no new claims) exactly like Shutdown does, but does not wait: pair it
// with Shutdown, typically through App.OnShutdown. ctx's values reach
// handlers; its cancellation does not, so a SIGTERM stops claiming without
// interrupting the jobs being drained.
func (w *Worker) Start(ctx context.Context) error {
	if ctx == nil {
		return errors.New("queue: worker context is required")
	}
	kinds := w.registry.Kinds()
	if len(kinds) == 0 {
		return errors.New("queue: register at least one handler before starting a worker")
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.state != stateIdle {
		return ErrStarted
	}
	w.state = stateRunning
	w.kinds = kinds

	pollCtx, pollStop := context.WithCancel(ctx)
	w.pollStop = pollStop
	w.jobCtx, w.jobStop = context.WithCancelCause(context.WithoutCancel(ctx))
	for _, q := range w.opts.Queues {
		w.pollers.Add(1)
		go w.poll(pollCtx, q)
	}
	if w.opts.RetainSucceeded > 0 {
		w.pollers.Add(1)
		go w.maintain(pollCtx)
	}
	w.logger.Info("job worker started", "queues", queueNames(w.opts.Queues), "kinds", kinds)
	return nil
}

func queueNames(queues []QueueConfig) []string {
	names := make([]string, len(queues))
	for i, q := range queues {
		names[i] = fmt.Sprintf("%s:%d", q.Name, q.Concurrency)
	}
	return names
}

// Run starts the worker, blocks until ctx ends, then drains for at most
// ShutdownTimeout. It returns nil after a clean drain.
func (w *Worker) Run(ctx context.Context) error {
	if err := w.Start(ctx); err != nil {
		return err
	}
	<-ctx.Done()
	drain, cancel := context.WithTimeout(context.WithoutCancel(ctx), w.opts.ShutdownTimeout)
	defer cancel()
	return w.Shutdown(drain)
}

// Shutdown stops claiming and waits for in-flight jobs until ctx ends. Jobs
// still running then have their contexts canceled; the worker waits
// ReleaseTimeout for them to return and then releases every job it still
// holds back to the queue without counting the interrupted attempt, so
// another worker takes over at once instead of after a lease expiry.
//
// Its signature matches forge.ShutdownHook. Register it after the database's
// hook: hooks run in reverse order, so the worker drains before the pool
// closes. Shutdown returns an error wrapping ctx's when it had to interrupt
// jobs; it is safe to call more than once and on a worker never started.
func (w *Worker) Shutdown(ctx context.Context) error {
	w.mu.Lock()
	switch w.state {
	case stateIdle:
		w.state = stateStopped
		w.mu.Unlock()
		return nil
	case stateStopping, stateStopped:
		done := w.done
		w.mu.Unlock()
		select {
		case <-done:
			return w.stopErr
		case <-ctx.Done():
			return fmt.Errorf("queue: wait for worker shutdown: %w", ctx.Err())
		}
	}
	w.state = stateStopping
	w.mu.Unlock()

	w.pollStop()
	drained := make(chan struct{})
	go func() {
		w.pollers.Wait()
		w.jobs.Wait()
		close(drained)
	}()

	var err error
	select {
	case <-drained:
	case <-ctx.Done():
		err = w.interrupt(drained, ctx.Err())
	}
	w.jobStop(errShutdown)
	w.mu.Lock()
	w.state = stateStopped
	w.stopErr = err
	w.mu.Unlock()
	close(w.done)
	w.logger.Info("job worker stopped", "forced", err != nil)
	return err
}

// interrupt cancels handlers, waits briefly for them and releases the jobs
// that are still held.
func (w *Worker) interrupt(drained <-chan struct{}, cause error) error {
	w.jobStop(errShutdown)
	select {
	case <-drained:
	case <-time.After(w.opts.ReleaseTimeout):
	}
	w.mu.Lock()
	pending := make([]*running, 0, len(w.inflight))
	for _, in := range w.inflight {
		pending = append(pending, in)
	}
	w.mu.Unlock()

	var wg sync.WaitGroup
	var released atomic.Int64
	for _, in := range pending {
		if !in.settled.CompareAndSwap(false, true) {
			continue // its goroutine is already acknowledging it
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := w.persist(func(ctx context.Context) error { return w.store.Release(ctx, in.lease) })
			switch {
			case err == nil:
				released.Add(1)
				w.finished(Result{Job: in.job, Outcome: OutcomeReleased})
			case errors.Is(err, ErrLeaseLost):
			default:
				w.logger.Error("release job lease at shutdown", "job_id", in.job.ID, "kind", in.job.Kind, "error", err)
			}
		}()
	}
	wg.Wait()
	return fmt.Errorf("queue: worker shutdown interrupted %d job(s): %w", len(pending), cause)
}

// poll claims jobs of one queue while there is a free slot.
func (w *Worker) poll(ctx context.Context, q QueueConfig) {
	defer w.pollers.Done()
	slots := make(chan struct{}, q.Concurrency)
	idle := w.opts.PollInterval
	failures := 0
	for {
		select {
		case slots <- struct{}{}:
		case <-ctx.Done():
			return
		}
		claimed, ok, err := w.claim(q.Name)
		switch {
		case err != nil:
			<-slots
			failures++
			delay := min(w.opts.PollInterval<<min(failures, 8), maxClaimErrGap)
			w.logger.Warn("claim jobs failed", "queue", q.Name, "error", err, "retry_in", delay)
			if !sleep(ctx, delay/2+jitter(delay/2)) {
				return
			}
			continue
		case !ok:
			<-slots
			failures = 0
			if !sleep(ctx, idle/2+jitter(idle)) {
				return
			}
			idle = min(idle*2, w.opts.MaxPollInterval)
			continue
		}
		failures, idle = 0, w.opts.PollInterval
		w.jobs.Add(1)
		go func() {
			defer func() { <-slots }()
			defer w.jobs.Done()
			w.process(claimed)
		}()
	}
}

// claim uses a context that only a forced shutdown cancels: abandoning a
// claim mid-commit would strand a leased job until its lease expires.
func (w *Worker) claim(queueName string) (Claimed, bool, error) {
	ctx, cancel := context.WithTimeout(w.jobCtx, claimTimeout)
	defer cancel()
	return w.store.Claim(ctx, ClaimRequest{Queue: queueName, Kinds: w.kinds, WorkerID: w.opts.WorkerID, Lease: w.opts.Lease})
}

func sleep(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// maintain deletes finished jobs past their retention.
func (w *Worker) maintain(ctx context.Context) {
	defer w.pollers.Done()
	for {
		interval := w.opts.MaintenanceInterval
		if !sleep(ctx, interval/2+jitter(interval)) {
			return
		}
		for range maxPruneRounds {
			pruneCtx, cancel := context.WithTimeout(w.jobCtx, claimTimeout)
			n, err := w.store.Prune(pruneCtx, w.opts.RetainSucceeded, pruneBatch)
			cancel()
			if err != nil {
				w.logger.Warn("prune finished jobs failed", "error", err)
				break
			}
			if n < pruneBatch {
				break
			}
		}
	}
}

// process runs one claimed job and settles it exactly once.
func (w *Worker) process(claimed Claimed) {
	job := claimed.Job
	in := &running{job: job, lease: claimed.Lease}
	w.mu.Lock()
	w.inflight[job.ID] = in
	w.mu.Unlock()
	defer func() {
		w.mu.Lock()
		delete(w.inflight, job.ID)
		w.mu.Unlock()
	}()

	started := time.Now()
	var outcome attempt
	reg, ok := w.registry.lookup(job.Kind)
	if !ok {
		outcome = attempt{err: Permanent(fmt.Errorf("%w %q", ErrUnknownKind, job.Kind))}
	} else {
		w.hook(func() {
			if w.opts.Hooks.Started != nil {
				w.opts.Hooks.Started(job)
			}
		})
		outcome = w.invoke(reg, claimed)
	}
	duration := time.Since(started)

	switch {
	case outcome.leaseLost:
		w.logger.Warn("job lease lost; result discarded", "job_id", job.ID, "kind", job.Kind, "queue", job.Queue, "attempt", job.Attempt)
		w.finished(Result{Job: job, Outcome: OutcomeLeaseLost, Duration: duration})
	case outcome.err == nil:
		w.settle(in, duration, OutcomeSucceeded, "", 0, func(ctx context.Context) error { return w.store.Complete(ctx, claimed.Lease) })
	case outcome.shutdown:
		w.settle(in, duration, OutcomeReleased, "", 0, func(ctx context.Context) error { return w.store.Release(ctx, claimed.Lease) })
	default:
		failure, message := w.classify(reg, job, claimed.Payload, outcome.err)
		result := OutcomeRetry
		if failure.Dead {
			result = OutcomeDead
		}
		w.settle(in, duration, result, message, failure.RetryIn, func(ctx context.Context) error { return w.store.Fail(ctx, claimed.Lease, failure) })
	}
}

// classify decides between retry and death and builds the stored message.
func (w *Worker) classify(reg *registration, job Job, payload []byte, err error) (Failure, string) {
	message := sanitizeError(err, payload)
	var permanent *permanentError
	var after *retryAfterError
	failure := Failure{Message: message}
	if errors.As(err, &permanent) || job.Attempt >= job.MaxAttempts {
		failure.Dead = true
		return failure, message
	}
	backoff := w.opts.Backoff
	if reg != nil && (reg.settings.backoff != Backoff{}) {
		backoff = reg.settings.backoff
	}
	failure.RetryIn = backoff.Delay(job.Attempt)
	if errors.As(err, &after) {
		failure.RetryIn = after.delay
	}
	return failure, message
}

// settle persists the outcome unless a forced shutdown already released the
// job, logs it and notifies the hook.
func (w *Worker) settle(in *running, duration time.Duration, outcome Outcome, message string, retryIn time.Duration, write func(context.Context) error) {
	if !in.settled.CompareAndSwap(false, true) {
		return
	}
	job := in.job
	err := w.persist(write)
	switch {
	case errors.Is(err, ErrLeaseLost):
		w.logger.Warn("job lease lost before acknowledgement; result discarded", "job_id", job.ID, "kind", job.Kind, "queue", job.Queue, "attempt", job.Attempt, "outcome", outcome)
		w.finished(Result{Job: job, Outcome: OutcomeLeaseLost, Duration: duration})
		return
	case err != nil:
		// The job stays leased; it is reclaimed when the lease expires and
		// runs again. At-least-once delivery covers this window.
		w.logger.Error("store job outcome failed; the job will be reclaimed after its lease expires", "job_id", job.ID, "kind", job.Kind, "queue", job.Queue, "attempt", job.Attempt, "outcome", outcome, "error", err)
		return
	}
	attrs := []any{"job_id", job.ID, "kind", job.Kind, "queue", job.Queue, "attempt", job.Attempt, "max_attempts", job.MaxAttempts, "outcome", outcome, "duration", duration}
	switch outcome {
	case OutcomeSucceeded:
		w.logger.Debug("job succeeded", attrs...)
	case OutcomeReleased:
		w.logger.Info("job released at shutdown", attrs...)
	case OutcomeRetry:
		w.logger.Warn("job failed; retry scheduled", append(attrs, "retry_in", retryIn, "error", message)...)
	case OutcomeDead:
		w.logger.Error("job failed permanently", append(attrs, "error", message)...)
	}
	w.finished(Result{Job: job, Outcome: outcome, Duration: duration, Error: message, RetryIn: retryIn})
}

func (w *Worker) finished(result Result) {
	w.hook(func() {
		if w.opts.Hooks.Finished != nil {
			w.opts.Hooks.Finished(result)
		}
	})
}

func (w *Worker) hook(fn func()) {
	defer func() {
		if r := recover(); r != nil {
			w.logger.Error("job hook panicked", "panic_type", fmt.Sprintf("%T", r))
		}
	}()
	fn()
}

// persist writes an outcome, retrying transient database errors until the
// lease would have expired anyway. ErrLeaseLost is final.
func (w *Worker) persist(write func(context.Context) error) error {
	deadline := time.Now().Add(min(w.opts.Lease, 30*time.Second))
	delay := 50 * time.Millisecond
	for {
		ctx, cancel := context.WithTimeout(context.Background(), persistTimeout)
		err := write(ctx)
		cancel()
		if err == nil || errors.Is(err, ErrLeaseLost) || time.Now().Add(delay).After(deadline) {
			return err
		}
		time.Sleep(delay/2 + jitter(delay/2))
		delay = min(delay*2, 5*time.Second)
	}
}

// attempt is what running a handler produced.
type attempt struct {
	err error
	// leaseLost means the store confirmed another worker owns the job.
	leaseLost bool
	// shutdown means a forced shutdown canceled the handler.
	shutdown bool
}

type panicError struct{ typeName string }

func (e *panicError) Error() string { return "handler panicked with " + e.typeName }

// invoke runs the handler with a context that carries the job, ends on
// timeout, shutdown or a lost lease, and keeps the lease renewed.
func (w *Worker) invoke(reg *registration, claimed Claimed) attempt {
	job := claimed.Job
	ctx, cancel := context.WithCancelCause(w.jobCtx)
	defer cancel(nil)
	ctx = context.WithValue(ctx, jobContextKey{}, job)
	if reg.settings.timeout > 0 {
		var stop context.CancelFunc
		ctx, stop = context.WithTimeoutCause(ctx, reg.settings.timeout, errHandlerTimeout)
		defer stop()
	}

	var lost atomic.Bool
	stopBeat := make(chan struct{})
	beatDone := make(chan struct{})
	go func() {
		defer close(beatDone)
		w.heartbeat(claimed.Lease, stopBeat, &lost, cancel)
	}()

	err := w.call(reg, ctx, claimed)

	close(stopBeat)
	<-beatDone
	cause := context.Cause(ctx)
	switch {
	case lost.Load():
		return attempt{err: err, leaseLost: true}
	case err != nil && errors.Is(cause, errShutdown):
		return attempt{err: err, shutdown: true}
	case err != nil && errors.Is(cause, errHandlerTimeout):
		return attempt{err: fmt.Errorf("attempt exceeded its %s timeout: %w", reg.settings.timeout, err)}
	}
	return attempt{err: err}
}

// call runs the handler and converts a panic into an error. The panic value
// is not stored (it may contain payload data); its type is, and the stack
// goes to the log.
func (w *Worker) call(reg *registration, ctx context.Context, claimed Claimed) (err error) {
	defer func() {
		if r := recover(); r != nil {
			w.logger.Error("job handler panicked", "job_id", claimed.Job.ID, "kind", claimed.Job.Kind, "queue", claimed.Job.Queue, "attempt", claimed.Job.Attempt, "panic_type", fmt.Sprintf("%T", r), "stack", string(debug.Stack()))
			err = &panicError{typeName: fmt.Sprintf("%T", r)}
		}
	}()
	return reg.run(ctx, claimed.Payload)
}

// heartbeat renews the lease every Lease/3. When the store says the lease is
// gone it marks it lost and cancels the handler. When renewals only fail
// (the database is unreachable) it keeps trying until a full lease has passed
// since the last success, then cancels the handler too: past that point
// another worker may legitimately own the job.
func (w *Worker) heartbeat(lease Lease, stop <-chan struct{}, lost *atomic.Bool, cancel context.CancelCauseFunc) {
	interval := max(w.opts.Lease/3, time.Millisecond)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	lastRenewed := time.Now()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
		}
		ctx, done := context.WithTimeout(context.Background(), min(interval*2, persistTimeout))
		err := w.store.Renew(ctx, lease, w.opts.Lease)
		done()
		switch {
		case err == nil:
			lastRenewed = time.Now()
		case errors.Is(err, ErrLeaseLost):
			lost.Store(true)
			cancel(ErrLeaseLost)
			return
		default:
			w.logger.Warn("renew job lease failed", "job_id", lease.JobID, "error", err)
			if time.Since(lastRenewed) >= w.opts.Lease {
				cancel(ErrLeaseLost)
				return
			}
		}
	}
}
