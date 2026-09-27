# ADR 0005: Environment configuration, fail fast

- **Status:** Accepted
- **Date:** 2026-09-26

## Context

Containers and orchestrators deliver configuration through environment
variables. Configuration errors that surface on the first request, or not at
all, cause incidents. Silent fallbacks are especially dangerous for security
settings, such as running production without TLS.

## Decision

- Runtime configuration is read from `FORGE_*` environment variables by
  `config.Load()`. There are no configuration files at this stage.
- Every setting has a default in `config.Default()` suitable for local
  development (`development`, `127.0.0.1:8080`, `plain`).
- `Validate()` runs on every load and in `forge.New`. Invalid configuration
  stops the process before it listens:
  - unknown `FORGE_ENV` or `FORGE_HTTP_TRANSPORT`;
  - `production` with `plain` transport;
  - `tls` without both certificate and key files;
  - empty address; non-positive timeouts or size limits;
  - malformed durations or integers.
- Errors for malformed values name the variable and the expected format but
  do not echo the value, which may be a misplaced secret.
- Production transport must be explicit: `tls`, or `trusted-proxy` to state
  that TLS is terminated upstream.
- `forge doctor` validates the current environment without starting a
  server.
- `config.LoadWithLookup` accepts an injected lookup function for tests.

## Consequences

- Misconfiguration is detected at deploy time, and a failing container
  restart loop is visible in the orchestrator.
- Environment defaults are development-friendly; the generated Dockerfile
  sets production values explicitly.
- Environment variables are visible to anything that can inspect the
  process; TLS keys are therefore passed as file paths, not values.
- Adding a setting requires a default, validation, and documentation in
  DEPLOYMENT.md.
