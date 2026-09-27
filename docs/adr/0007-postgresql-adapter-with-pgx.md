# ADR 0007: PostgreSQL adapter with pgx, in the main module

- **Status:** Accepted (amends [ADR 0006](0006-zero-dependency-core.md));
  amended by [ADR 0009](0009-multiple-databases.md), which adds MySQL,
  MariaDB and SQLite adapters following the same pattern
- **Date:** 2026-09-26

## Context

PostgreSQL is Forge's primary database and the default for generated
applications (see [ADR 0009](0009-multiple-databases.md) for the other
choices). A driver is required; writing one is
out of the question. ADR 0006 left open how a third-party dependency enters
the framework without reaching applications that do not need it.

Options considered:

1. `database/sql` with a registered driver. Portable, but hides
   PostgreSQL-specific capabilities (COPY, LISTEN/NOTIFY, native types,
   pipelining) behind a lowest-common-denominator API and adds reflection.
2. `github.com/jackc/pgx/v5` natively, in a separate Go module
   (`forge-go/postgres`). Maximum isolation, but every change spans two
   modules, releases need coordinated tags, and generated applications need
   two requirements kept in step.
3. pgx natively, in the `postgres` package of the main module, with the core
   packages kept free of it by an automated check.

## Decision

Option 3.

- `postgres` uses pgx v5 and `pgxpool` directly. Its `DBTX` interface
  matches the one sqlc generates for pgx/v5, so hand-written and generated
  repositories are interchangeable.
- The core packages (`forge`, `config`, `fault`, `health`, `httpserver`,
  `pagination`, `router`, `uuid`, `web`, and since ADR 0009 `sqldb`,
  `migrate` and `dbtest`) import only the standard library and each other. `architecture_test.go` enforces this with `go list -deps` and
  fails CI on any violation.
- Go's module graph pruning means an application that never imports
  `postgres` neither builds nor links pgx.
- The core declares the extension point (`forge.Command`, `App.OnShutdown`,
  `App.Readiness`); the adapter depends on the core, never the reverse.

## Consequences

- One module, one version, one tag. `go.mod` now lists pgx and its small
  transitive set (pgpassfile, pgservicefile, puddle, x/sync, x/text), which
  `govulncheck` covers.
- Adding another adapter dependency (Redis, OpenTelemetry) follows the same
  pattern and needs its own ADR, as the MySQL and SQLite drivers did
  ([ADR 0009](0009-multiple-databases.md)).
- If the dependency set grows enough to burden applications, adapters can
  still move to separate modules without changing their import-facing API.
