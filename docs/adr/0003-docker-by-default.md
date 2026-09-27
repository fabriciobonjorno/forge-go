# ADR 0003: Docker by default

- **Status:** Accepted
- **Date:** 2026-09-26

## Context

Teams usually add a Dockerfile late, often copied from elsewhere, running as
root, with a full OS image and without a health check. Rails 8 showed the
value of generating a production-ready Dockerfile with every new
application. Go produces static binaries, which suits minimal images.

## Decision

`forge new` generates a production `Dockerfile`, `.dockerignore`, and
`compose.yaml` for every application. There is no opt-in flag.

- Build stage: `golang:1.27-alpine`, BuildKit cache mounts, `CGO_ENABLED=0`,
  `-trimpath`, static binary.
- Runtime: `gcr.io/distroless/static-debian12:nonroot`, running as `nonroot`.
- `ENV FORGE_ENV=production FORGE_HTTP_ADDR=0.0.0.0:8080
  FORGE_HTTP_TRANSPORT=trusted-proxy`; `EXPOSE 8080`. TLS is expected to be
  terminated by a proxy or load balancer, similar to Rails 8 `assume_ssl`
  behind kamal-proxy.
- `HEALTHCHECK` runs `/app/server healthcheck`: the application binary probes
  its own `/health/live` on loopback, because the image has no shell or
  `curl`.
- `compose.yaml` runs the same image locally with `FORGE_ENV=development`
  and `plain` transport on port 8080.
- Generated CI builds the image on every run.

## Consequences

- A new application is deployable as a container immediately, and CI catches
  Dockerfile breakage.
- The image cannot be debugged with a shell; use ephemeral debug containers
  or a local build.
- The `trusted-proxy` default is safe only if the container port is not
  directly reachable by clients. This is documented in DEPLOYMENT.md and the
  threat model.
- Developers who do not use Docker can ignore the files and use `forge dev`.
- The application must implement the `healthcheck` subcommand; `forge.Main`
  provides it.
- A `postgres` service is added to `compose.yaml` when the database module
  lands (Phase 2).
