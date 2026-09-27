# Authentication and Authorization

Phase 3 now includes an API-oriented security core plus an opinionated
PostgreSQL identity/session adapter. The core remains database-independent.

## Opaque bearer sessions

`auth.NewToken` creates 32 random bytes with `crypto/rand` and encodes them as
unpadded base64url. Applications deliver `Token.Reveal()` once and persist
only `Token.Digest()` (SHA-256). A database disclosure therefore does not
directly disclose the active bearer credential. UUIDv7 values are identifiers,
not session tokens.

`auth.Middleware` accepts exactly one `Authorization: Bearer <token>` header.
It rejects missing, duplicate, combined, malformed, unknown, and expired
credentials with a stable JSON `401` and `WWW-Authenticate`. Cookies, query
parameters, `X-User-*`, and `X-Tenant-*` headers are never authentication
fallbacks.

The application supplies an `auth.Resolver`. Its contract is deliberately
strict: for every request it must resolve the current session, active user,
active tenant membership, and current role permissions. Do not copy roles or
permissions into a long-lived token. A resolver infrastructure failure is a
generic `500`; an unknown credential is a `401`.

```go
middleware, err := auth.NewMiddleware(sessionRepository)
if err != nil {
    return err
}

permission, _ := auth.NewPermission("tasks:read")
handler, err := auth.Require(permission, listTasks)
if err != nil {
    return err
}

app.Handle("GET /v1/tasks", middleware.Authenticate(handler))
```

Permissions use explicit lowercase `resource:action` names and have no
wildcards. `auth.Require` denies by default: no principal is `401`; a current
principal without the permission is `403`. Authentication installs both the
principal and its validated tenant in the request context.

## Password hashing

`auth.HashPassword` uses the Go 1.27 standard-library `crypto/pbkdf2`
implementation with HMAC-SHA-256, a random 128-bit salt, a 256-bit derived key,
and a default work factor of 600,000 iterations. The encoded form carries a
version and work factor so future releases can identify hashes that should be
upgraded after successful authentication.

Forge never trims or normalizes passwords before hashing. Malformed hashes are
rejected fail-closed and comparisons use constant-time equality.

## PostgreSQL identity schema

`auth/postgres.Migrations()` provides the Phase 3 identity schema:

- users with normalized unique email, password hash, active/disabled status,
  and a credential/session version;
- organizations and tenants;
- memberships, roles, permissions, and role assignments;
- sessions containing only token digests, membership, credential version,
  expiry, and revocation time.

All entity identifiers default to PostgreSQL `uuidv7()`.

`auth/postgres.Repository` implements `auth.Resolver`. Every resolution
joins the live session, user, organization, tenant and membership state and
recomputes permissions from current role assignments. `RevokeSession` is
idempotent, while `RevokeUserSessions` increments the user's credential
version and revokes existing sessions atomically.

The plaintext bearer token is never stored.

## Remaining Phase 3 work

Login endpoints and login-identifier policy, cookie sessions and CSRF, account
recovery, MFA, audit events, generated application wiring, and equivalent
database-enforcement strategies for MySQL, MariaDB and SQLite are still
planned.
