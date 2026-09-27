# ADR 0004: Hexagonal application layout

- **Status:** Accepted
- **Date:** 2026-09-26

## Context

Framework-generated applications need a predictable structure so that
generators, documentation, and developers agree on where code goes. Layouts
that organise by technical type (`models/`, `controllers/`) tend to couple
business rules to HTTP and database details. Layouts that create every
directory up front leave empty packages and placeholder interfaces that
nobody uses.

## Decision

Generated applications follow a hexagonal (ports and adapters) layout under
`app/`:

| Directory            | Responsibility                                                  |
| -------------------- | --------------------------------------------------------------- |
| `app/domain`         | Entities, value objects, domain rules and errors                |
| `app/application`    | Use cases and the ports (interfaces) they depend on             |
| `app/adapters`       | HTTP handlers, repository implementations, other port adapters  |
| `app/infrastructure` | Technical resources: connection pools, clients, telemetry setup |
| `app/bootstrap`      | Composition root that wires dependencies into `forge.App`       |

Dependency rule: `domain` imports nothing from other layers; `application`
imports `domain`; `adapters` and `infrastructure` import inward;
`bootstrap` may import everything. `cmd/<name>/main.go` only calls
`forge.Main(bootstrap.Configure)`, passing the database commands when the
application has a database:
`forge.Main(bootstrap.Configure, postgres.Commands(db.Migrations())...)`.

Directories are created only when there is real code for them. `forge new`
generates `cmd/<name>`, `app/bootstrap`, and `db/` (`db/db.go` embedding
the SQL files in `db/migrations`, see
[ADR 0008](0008-sql-migrations-embedded-in-the-binary.md)); `--skip-database`
omits `db/`. Later generators (for example `forge generate resource`,
Phase 8) add other layers as needed.

## Consequences

- Business logic is testable without HTTP or a database.
- Generators have a fixed target for each kind of code.
- Small applications stay small; there are no empty packages.
- The layer rules are conventions; nothing enforces them automatically yet.
  An import-boundary check may be added later.
- The layout adds indirection that very small services may not need; they
  can keep everything in `app/bootstrap` until it grows.
