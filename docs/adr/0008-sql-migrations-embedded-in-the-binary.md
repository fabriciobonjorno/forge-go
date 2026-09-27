# ADR 0008: SQL migrations embedded in the application binary

- **Status:** Accepted; amended by [ADR 0009](0009-multiple-databases.md): the
  engine moved to the `migrate` package and serves every supported database
- **Date:** 2026-09-26

## Context

Schema changes must be versioned, reviewable, repeatable in every
environment, and safe when several replicas start at once. Rails runs
`db:prepare` from its container entrypoint; Forge images are distroless
(no shell, no extra tools), so migrations must run from the application
binary itself.

## Decision

- Migrations are plain SQL files, `db/migrations/<YYYYMMDDHHMMSS>_<name>.up.sql`
  with an optional `.down.sql`, embedded with `go:embed`. No Go-coded
  migrations and no ORM DSL: the SQL reviewed is the SQL executed.
- Each adapter's `Commands` (`postgres.Commands`, `mysql.Commands`,
  `sqlite.Commands`, built on `migrate.Commands`) adds `migrate`,
  `migrate status` and `rollback` to the application binary. `forge migrate` / `forge rollback` build the binary and
  call those commands, so development and production share one code path.
- State lives in `forge_schema_migrations (version, name, checksum, applied_at)`,
  with portable column types; `applied_at` holds Unix microseconds written
  by the migrator.
- Every `migrate` and `rollback` run holds a cross-process lock on a
  dedicated session, supplied by the database's dialect: on PostgreSQL a
  session advisory lock, on MySQL/MariaDB `GET_LOCK`, on SQLite an
  operating-system lock on a sidecar file. The lock is acquired by polling
  without waiting inside the database (on PostgreSQL,
  `pg_try_advisory_lock`): a blocking
  `pg_advisory_lock` is an open statement, and `CREATE INDEX CONCURRENTLY` in
  the lock holder waits for it, which deadlocks (found by
  `TestConcurrentMigratorsApplyOnce`).
- `migrate status` is read-only: it takes no lock (so it answers while a long
  migration runs) and does not create the table; a database that was never
  migrated shows every migration as pending.
- Each migration and its bookkeeping row run in one transaction; on
  MySQL/MariaDB, DDL commits implicitly, so a failed migration can leave
  part of its changes applied. A first line of `-- forge:no-transaction`
  opts out, for statements that cannot run in a transaction (such as
  PostgreSQL's `CREATE INDEX CONCURRENTLY`).
- The checksum is the SHA-256 of the up script only. Editing a down script
  after its migration was applied is allowed, so a broken rollback can be
  fixed.
- The migrator fails closed: it refuses to run when an applied migration's
  checksum changed, when the database contains a migration the build does not
  know (an older release deploying over a newer schema), when a file name
  does not follow the convention, or when a file has no statements. A
  multi-step rollback checks that every step is reversible before reverting
  any.
- `rollback` reverts the most recently applied migrations, ordered by
  `applied_at` descending and then version descending, so it undoes what was
  actually applied last even after out-of-order application.
- The migration session lifts the statement timeout where the database has
  one; the migrator uses its own connections, never the application pool,
  and closes each session afterwards instead of reusing it.
- Generated `compose.yaml` runs a one-off `migrate` service that the app
  waits for (`service_completed_successfully`). In production, run
  `<app> migrate` from the release image before rolling out (a Kubernetes Job
  or a one-off container).

## Consequences

- No extra migration tool in images or CI.
- Out-of-order application is allowed (a pending version older than the
  newest applied one is applied), which keeps parallel branches workable.
- A failed `no-transaction` migration can leave partial state (for example an
  invalid index) and must be repaired by hand; keep such files to one
  statement.
- Applying migrations at application startup is deliberately not automatic;
  it is an explicit step.
