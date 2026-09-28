# ADR 0010: Opaque sessions and fail-closed tenancy

Status: accepted (Phase 3 foundation)

## Decision

Forge uses opaque, high-entropy bearer sessions for the initial API
authentication surface. Only a SHA-256 digest is stored. A resolver loads the
current subject, tenant membership, expiration, and permissions on every
request. Authorization permissions are explicit `resource:action` values and
deny by default.

Tenant state is a typed request context value with no default. PostgreSQL
repositories use `InTenantTx`, which installs a transaction-local
`forge.tenant_id` for RLS policies. Other databases receive no false claim of
database-enforced tenant isolation.

## Consequences

- Role and membership changes take effect on the next resolved request.
- Tokens are revocable server-side and contain no authorization claims.
- Database compromise exposes digests rather than usable bearer values.
- Every tenant-owned PostgreSQL operation needs a transaction, even a read.
- PostgreSQL is the only built-in identity persistence adapter. Applications
  using other adapters can implement the auth ports, but must not assume a
  database-level isolation guarantee.
- Browser cookie sessions, CSRF, password authentication, MFA, and recovery
  are provided as explicit framework APIs; generated applications wire only
  the default bearer login/logout/session flow and optional MFA completion.
- MySQL, MariaDB, and SQLite use application-layer tenant scoping as described
  in `MULTI_TENANCY.md`; this is weaker than PostgreSQL RLS.
