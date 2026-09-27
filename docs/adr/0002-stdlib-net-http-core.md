# ADR 0002: Standard library `net/http` as the HTTP core

- **Status:** Accepted
- **Date:** 2026-09-26

## Context

Go 1.22 added method matching and path wildcards to `http.ServeMux`
(`GET /users/{id}`, `r.PathValue("id")`). This removed the main reason most
applications adopted third-party routers. Third-party HTTP frameworks often
introduce their own handler and context types, which couples application code
to the framework and complicates reuse of standard middleware.

## Decision

Forge's HTTP layer is built on `net/http`:

- Handlers are `http.Handler` / `http.HandlerFunc`. There is no Forge-specific
  handler or context type.
- `router.Router` wraps `http.ServeMux` and adds only: mandatory
  `METHOD /path` patterns, conflict errors instead of panics, a route listing,
  freezing after the first request, and rejection of traversal and encoded
  slash paths.
- `httpserver.Server` wraps `http.Server` with configured timeouts, header
  and body limits, security headers, request IDs, panic recovery, and
  context-driven graceful shutdown.
- Middleware uses the `func(http.Handler) http.Handler` shape.

## Consequences

- Any standard `net/http` handler or middleware works with Forge.
- Routing semantics and precedence follow `ServeMux`; Forge does not add
  regex routes or route groups at this stage.
- Router performance is bounded by `ServeMux`; measured in Phase 9.
- Forge tracks the Go release cycle for HTTP improvements and security fixes.
