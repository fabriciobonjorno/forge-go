# Roadmap

Phases are sequential in intent but may overlap. Status reflects the current
branch. Nothing in a phase marked *planned* is implemented.

| Phase | Scope                                              | Status  |
| ----- | -------------------------------------------------- | ------- |
| 0     | Architecture, ADRs, threat model                   | Done    |
| 1     | Core runtime, config, CLI, lifecycle, HTTP         | Done    |
| 2     | Databases, migrations, repositories, transactions  | Done    |
| 3     | Authentication, authorization, tenancy             | In progress |
| 4     | Jobs, events, outbox, scheduling                   | Done    |
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
  login/logout HTTP adapters;
- account/source login throttling with a bounded in-memory default, HTTP 429
  plus Retry-After, and a shared PostgreSQL adapter with bounded pruning
  ([ADR 0011](adr/0011-login-throttling.md));
- explicit browser-cookie sessions with secure `__Host-` cookies, same-origin
  login protection, double-submit CSRF validation, CSRF-protected logout, and
  no-store login responses
  ([ADR 0012](adr/0012-cookie-sessions-and-csrf.md));
- structured security-audit event contract plus append-only PostgreSQL
  persistence with request-ID correlation and no arbitrary secret-bearing
  metadata ([ADR 0016](adr/0016-structured-security-audit.md));
- one-time password recovery with digest-only tokens, non-enumerating request
  responses, global session revocation on reset, and bounded cleanup
  ([ADR 0013](adr/0013-password-recovery.md));
- replay-safe TOTP MFA with encrypted seeds, short-lived digest-only challenges,
  one-time backup codes, factor rotation/disable, credential-version-bound
  session creation, and bearer/browser completion flows
  ([ADR 0014](adr/0014-totp-mfa.md), [ADR 0015](adr/0015-mfa-factor-lifecycle.md)).
- PostgreSQL identity wiring in generated applications, including migrations,
  login/logout/session routes, optional MFA challenge completion, and the
  `FORGE_AUTH_MFA_KEY` configuration.
- Structured audit events for login, logout, password recovery/reset, MFA
  challenge and factor lifecycle operations. Audit persistence is opt-in for
  framework services; generated PostgreSQL login, MFA, and logout flows wire
  the repository as their auditor.

Remaining boundaries: generated applications do not choose account
provisioning, password-recovery delivery, browser-cookie routes, or MFA
enrollment/rotation step-up policy. Those framework primitives require
application-specific decisions and are documented in
[AUTHENTICATION.md](AUTHENTICATION.md) and [MFA.md](MFA.md). PostgreSQL is the
only built-in identity persistence adapter and the only adapter with a
database-enforced RLS helper. MySQL, MariaDB, and SQLite use the documented
application-layer tenant-scoping contract; Forge does not claim equivalent
database enforcement ([MULTI_TENANCY.md](MULTI_TENANCY.md)). Broader audit
coverage for authorization denials and rejected session resolution remains a
separate scope decision.

## Phase 4 - Asynchronous work

The event envelope, atomic PostgreSQL persistence, and bounded concurrent
dispatcher with renewable leases, token-fenced acknowledgements, exponential
retry, and a terminal dead-letter state are implemented. Dead-letter replay,
queue metrics (`Dispatcher.Stats`, `ListDeadLetters`), an in-process job
scheduler (`jobs.Scheduler`), and generated-application wiring are complete.
See [ADR 0018](adr/0018-transactional-outbox-foundation.md),
[ADR 0019](adr/0019-outbox-dispatch.md), and
[ADR 0020](adr/0020-background-jobs-scheduling.md).

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
