# ADR 0019: Background jobs and scheduling

Status: accepted (Phase 4)

## Context

Phase 4 established the transactional outbox and a concurrent dispatcher for
event delivery. The remaining scope is background jobs (recurring work that
produces domain events) and scheduling (when work should run). Both must work
across process replicas without holding database locks during external I/O.

## Decision

- Introduce a `jobs` package that defines a `Job` interface: a function that
  receives a context and returns an error. Jobs run in their own goroutine
  with a configurable concurrency bound.
- Jobs may enqueue outbox events via `outbox/postgres.Insert` using a
  transaction from `postgres.InTx`. This makes the event persistence atomic
  with any domain state change the job performs.
- Scheduling is a separate concern. The scheduler accepts a cron-like
  specification and a `Job` factory. It runs in-process and is not a
  distributed scheduler — each replica runs its own scheduler instance.
- To avoid duplicate job execution across replicas, jobs that must run
  exactly once per schedule tick should use a PostgreSQL advisory lock
  (`postgres.TryAdvisoryXactLock`) around their work. The scheduler does not
  enforce this; it is a job-level decision.
- The scheduler supports: fixed-rate (every N seconds/minutes/hours), daily
  at a specific time (UTC), and cron expressions (standard 5-field).
- Job execution observes the application shutdown context; in-flight jobs
  receive a cancellation signal and must honor it promptly.
- No persistence of job history or results in this phase. Observability is
  via structured logs and the outbox queue metrics (`Dispatcher.Stats`).
- Generated applications get a `jobs` package skeleton and a `bootstrap`
  helper to register jobs and start the scheduler from `forge.Main`.

## Consequences

- Each replica runs its own scheduler; job authors must use advisory locks
  or idempotent outbox events for exactly-once semantics.
- Scheduler precision depends on the process being alive; no catch-up for
  missed ticks while the process was down.
- Outbox metrics (`Dispatcher.Stats`) serve as the primary visibility into
  job-produced event throughput.
- The scheduler is in-process and does not require a separate service or
  database table.