# Roadmap

Phases are sequential in intent but may overlap. Status reflects the `main`
branch. Nothing in a phase marked *planned* is implemented.

| Phase | Scope                                              | Status  |
| ----- | -------------------------------------------------- | ------- |
| 0     | Architecture, ADRs, threat model                   | Done    |
| 1     | Core runtime, config, CLI, lifecycle, HTTP         | Done    |
| 2     | Databases, migrations, repositories, transactions  | Done    |
| 3     | Authentication, authorization, tenancy             | In progress |
| 4     | Jobs, events, outbox, scheduling                   | Planned |
| 5     | OpenTelemetry                                      | Planned |
| 6     | OpenAPI and tooling                                | Planned |
| 7     | AI API and CLI subsystem                           | Planned |
| 8     | Code generation                                    | Planned |
| 9     | Performance                                        | Planned |
| 10    | Security hardening                                 | Planned |

## Phase 0 - Architecture

Architecture overview, deployment guide, threat model, security policy, and
ADRs 0001-0006.

## Phase 1 - Core runtime (done)

- `config`: environment loading, defaults, fail-fast validation, production
  transport rule.
- `router`: `ServeMux` patterns, freeze on first request, traversal and
  encoded-slash rejection.
- `httpserver`: timeouts, body limit, security headers, HSTS, `X-Request-ID`,
  panic recovery, graceful shutdown.
- `health`: readiness registry and `/health`, `/health/live`, `/health/ready`.
- `uuid`: monotonic RFC 9562 UUIDv7 with JSON, text, and SQL support.
- `fault`: typed application errors.
- `forge.Main` / `forge.Execute`: conventional entrypoint with `serve` and
  `healthcheck` subcommands, environment-dependent log format, signal
  handling.
- CLI: `new`, `dev`, `build`, `version`, `uuid`, `doctor`.
- `forge new`: application skeleton with Dockerfile, Compose, and CI.

## Phase 2 - Databases (done)

Database:

- `config.Database` with `FORGE_DATABASE_*` variables; the URL is a
  redacted `config.Secret`; `production` requires TLS to the database unless
  explicitly allowed.
- `postgres` adapter on pgx v5 ([ADR 0007](adr/0007-postgresql-adapter-with-pgx.md)):
  pool with statement timeout and startup ping, `DBTX` compatible with sqlc,
  `InTx`/`InTxWith` with isolation levels and retries on serialization
  failures and deadlocks, error translation to `fault`, transaction-scoped
  advisory locks, and the `Pool()` escape hatch.
- SQL migrations embedded in the binary
  ([ADR 0008](adr/0008-sql-migrations-embedded-in-the-binary.md)):
  checksums, advisory lock, per-migration transactions with a
  `no-transaction` opt-out, fail-closed checks, `migrate`, `migrate status`,
  and `rollback` subcommands.
- `pagination`: UUIDv7 keyset pagination with opaque cursors.
- `postgrestest`: a fresh database per test; CI mode that fails instead of
  skipping.
- Core/adapter boundary enforced by `architecture_test.go`.
- CLI: `forge migrate [status]`, `forge rollback [-steps N]`,
  `forge generate migration NAME`, `forge test`, `.env.development` /
  `.env.local` loading, `forge doctor` showing the database target without
  credentials.
- `forge new` generates PostgreSQL by default (`--skip-database` to omit it):
  database wiring, readiness check, embedded migrations directory, Compose
  stack with PostgreSQL 18 and a one-off `migrate` service, and a CI
  PostgreSQL service.
- `examples/basic-api`: a task API showing repositories, transactions,
  optimistic locking, and pagination.
- Documentation: [DATABASE.md](DATABASE.md).

Multiple databases, completed before Phase 3
([ADR 0009](adr/0009-multiple-databases.md)):

- `forge new --database postgresql|mysql|mariadb|sqlite|none` (`-d`), with
  per-database Compose service, CI service, development settings, and
  Dockerfile (a nonroot storage volume for SQLite).
- Adapter selection by the `FORGE_DATABASE_URL` scheme (`postgres://`,
  `mysql://`, `sqlite:`), with production TLS rules for PostgreSQL and MySQL.
- Standard-library core packages shared by all adapters: `sqldb` (one error
  translation for every database, retries, bounded close, `database/sql`
  wrapper), `migrate` (the migration engine, moved out of `postgres`, with a
  `Dialect` interface and a portable bookkeeping table), and `dbtest`.
- `mysql` adapter for MySQL 8.4 and MariaDB 11.8: hardened driver
  configuration, strict session, flavor-specific statement timeout,
  `GET_LOCK` migration lock.
- `sqlite` adapter on the pure-Go modernc.org/sqlite driver: enforced
  pragmas, operating-system file lock for migrations.
- A uniform adapter surface (`Open`, `Translate`, `NewMigrator`, `Commands`,
  and `New`/`NewMigrated`/`Config`/`NewWithConfig` test helpers); readiness
  check named `database` in generated applications.
- CI: tests against PostgreSQL 18, MySQL 8.4, and MariaDB 11.8, and a Docker
  smoke test of a generated application per database.

Core changes made alongside:

- `web`: JSON responses, a single error body
  (`{"error":{"code","message","request_id"}}`), strict JSON decoding,
  request ID and request-scoped logger in the context.
- `httpserver`: request ID and logger set before panic recovery; recovered
  panics, oversized bodies, and request-ID failures answer in the JSON error
  format; requests still running at the shutdown deadline have their
  connections closed, cancelling their contexts.
- `router`: JSON `400`, `404` and `405` (with `Allow`) responses; atomic
  freeze flag.
- `fault`: category default statuses (`Status()`), `From`, and `Is` by code.
- `health`: per-check timeout (2 seconds by default).
- `forge`: `Configure` receives a context; `App.Config`, `App.Logger`,
  `App.OnShutdown`, `App.Close`; one shutdown budget shared by the HTTP drain
  and the hooks; application-defined subcommands (`forge.Command`, with
  `forge.ErrUsage` for exit status 2) and `help`.
- `forge new` adds `.github/dependabot.yml`.

## Known gaps

Not scheduled in a specific phase unless noted.

- `X-Forwarded-For` / `X-Forwarded-Proto` are not interpreted; handlers see
  the proxy's address behind `trusted-proxy`.
- Readiness results are not cached; every probe runs the checks.
- Matched requests are looked up in `ServeMux` twice (once to detect
  unmatched routes); tracked for Phase 9.
- Multi-tenancy and PostgreSQL row-level security: Phase 3.
- Soft delete and audit fields (`created_by`, `updated_by`, `deleted_at`)
  have no framework support yet; applications add them in their own SQL.
- Keyset pagination orders by UUIDv7 only; there is no cursor for other sort
  orders.
- `forge migrate` and `forge rollback` require a single command under
  `cmd/`.
- MySQL/MariaDB TLS verifies against the system certificate pool only; a
  private CA cannot be configured.
- SQLite applications run as a single instance on a single host.
- Application-level lock helpers exist for PostgreSQL only.

## Phase 3 - Identity and tenancy

Implemented foundation:

- opaque 256-bit bearer tokens with digest-only persistence;
- a resolver port that must load current session, subject, membership and
  permissions on every request;
- strict bearer middleware and deny-by-default `resource:action` permissions;
- fail-closed typed tenant context;
- PostgreSQL `InTenantTx` with a transaction-local RLS setting and a real
  cross-tenant integration test;
- versioned PBKDF2-HMAC-SHA256 password hashing using Go's standard library;
- PostgreSQL user/organization/tenant/membership/role/permission/session schema;
- a concrete PostgreSQL session repository that implements `auth.Resolver`,
  recomputes current permissions, and supports per-session and user-wide
  revocation;
- tenant-scoped email/password login with uniform authentication failures,
  dummy-hash timing equalization, password-work-factor upgrades, and thin
  login/logout HTTP adapters.

Still planned: native login/brute-force throttling, cookie sessions and CSRF,
account recovery, MFA, audit events, generated application wiring, and a
decided database-enforcement strategy for MySQL, MariaDB and SQLite. See [AUTHENTICATION.md](AUTHENTICATION.md) and
[MULTI_TENANCY.md](MULTI_TENANCY.md).

## Phase 4 - Asynchronous work

Background jobs, domain events, a transactional outbox, and scheduling.

## Phase 5 - Observability

OpenTelemetry traces, metrics, and log correlation.

## Phase 6 - API tooling

OpenAPI description and related tooling.

## Phase 7 - AI subsystem

An API and CLI surface for AI-driven operations. Every action passes through a
fixed pipeline:

```text
Intent -> Policy -> Validation -> Authorization -> Execution -> Audit
```

No step may be skipped, and execution never runs with more privilege than the
authorizing principal.

## Phase 8 - Code generation

`forge generate resource` and related generators that create hexagonal layers
(`app/domain`, `app/application`, `app/adapters`, `app/infrastructure`) only
when they have code to hold. `forge generate migration` already exists.

## Phase 9 - Performance

Benchmarks, profiling, and allocation work on hot paths (router, middleware,
UUID generation).

## Phase 10 - Security hardening

Review against the [threat model](THREAT_MODEL.md), broader fuzzing, and
supply-chain hardening (pinned base images and actions, SBOM, signed images).

## Also planned (unscheduled)

- Kamal-style deploy configuration.
- Dev container definition.
