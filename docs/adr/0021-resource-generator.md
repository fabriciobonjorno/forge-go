# ADR 0021: Resource generator

- **Status:** Accepted
- **Date:** 2026-10-14

## Context

`forge generate migration|view|job` exist, but nothing generates a working
resource: schema, validation, persistence, HTTP and tests. That is the step
that makes `forge new` + `generate` + `migrate` productive. A generator also
fixes conventions in every application that uses it, so its defaults must be
the secure ones: Forge's identifier is UUIDv7 (ADR 0001), tenant isolation
fails closed (ADR 0010, ADR 0017), and Forge never ships public CRUD by
accident.

## Decision

`forge generate resource NAME field:type... (--tenant-scoped | --global)
[--plural PLURAL] [--dry-run]` generates, for the application's database
(read from the project, as `forge inspect` does):

- `db/migrations/<UTC timestamp>_create_<plural>.{up,down}.sql`
- `app/domain/<name>.go` and test: entity, constructor, per-field validation,
  errors as `fault` sentinels with stable codes.
- `app/application/<name>.go` and test: ports, service, use cases; tests use
  an in-memory fake.
- `app/adapters/persistence/<name>.go` and integration test: explicit SQL for
  the application's database, errors through the adapter's `Translate`.
- `app/adapters/httpapi/<name>.go` and test: thin handlers and a
  `Register<Plural>` function.

Rules:

1. **UUIDv7 everywhere, no exceptions.** The primary key, and `tenant_id`
   when scoped, use the database's UUID storage: PostgreSQL
   `uuid PRIMARY KEY DEFAULT uuidv7()`, MySQL/MariaDB `CHAR(36)`, SQLite
   `TEXT` in a `STRICT` table. IDs are generated in Go by an injected
   `uuid.New`; the PostgreSQL default is only a fallback for rows inserted by
   hand. Generated SQL never contains `serial`, `AUTO_INCREMENT`,
   `gen_random_uuid()`, `UUID()` or another identifier strategy, and a test
   enforces it for every database. `field:uuid` references use the same
   storage.
2. **Explicit scope.** Exactly one of `--tenant-scoped` or `--global` is
   required; there is no default to forget. Tenant-scoped resources take the
   tenant from `tenancy.Require(ctx)` in the repository, bind `tenant_id` in
   every predicate and insert, and index `(tenant_id, id)`. On PostgreSQL the
   repository also runs in `InTenantTx` behind `ENABLE`/`FORCE ROW LEVEL
   SECURITY` policies (defense in depth). Generated tests prove two tenants
   with overlapping data never see or modify each other's rows.
3. **No public CRUD.** `Register<Plural>` requires a non-nil `Protect`
   function that wraps each handler with its permission
   (`<plural>:read` for reads, `<plural>:write` for writes). In applications
   with Forge identity (PostgreSQL) the migration inserts those permissions
   into `forge_permissions` and the wiring snippet uses `auth.Require` behind
   the authentication middleware; elsewhere the application supplies its own
   guard explicitly.
4. **Optimistic locking and keyset pagination by default.** Rows carry
   `version`, `created_at`, `updated_at` (UTC); updates and deletes require
   the version the client read (stale → `409`); lists are newest-first keyset
   pages over the UUIDv7 key with the `pagination` package.
5. **Portable, exact types only.** `string` (≤255 characters), `text`, `int`
   (64-bit), `bool`, `time` (UTC), `uuid`; a trailing `?` makes a field
   optional (`NULL`, pointer in Go). There is no floating-point or decimal
   type: money is an `int` in minor units (`price_cents:int`).
6. **No hidden edits.** The generator only creates files and refuses to
   overwrite any of them (all-or-nothing, as the other generators). It does
   not edit `bootstrap.go`; it prints the exact wiring to add.
7. `--dry-run` lists every planned file and the wiring without writing
   anything.

## Consequences

- Every generated table has the same identifier, timestamp, versioning and
  isolation shape on all four databases.
- Applications without Forge identity must provide an authorization guard;
  the generator cannot make that decision for them.
- Pluralization is a small built-in rule set; irregular plurals use
  `--plural`.
- Relationships, decimal types, soft delete and audit columns are future
  work; `field:uuid` gives an unconstrained reference until then.
