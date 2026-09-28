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

## Password login

`auth.LoginService` is the application use case for password authentication.
It depends only on `PasswordStore` and `SessionCreator` ports. The PostgreSQL
repository implements both.

A login always names its tenant explicitly:

```json
{
  "email": "alice@example.com",
  "password": "correct horse battery staple",
  "tenant": "acme"
}
```

Forge trims surrounding identifier whitespace, case-folds email and tenant
identifiers, and validates the tenant slug. Password bytes are never trimmed or
normalized. An unknown email, wrong password, unknown tenant, disabled user,
disabled organization/tenant, or inactive membership all produce the same
public `credentials_invalid` result. Missing identities still execute PBKDF2
against a dummy hash to avoid a dramatically cheaper account-miss path.

The default session lifetime is 24 hours and may be configured between five
minutes and 30 days. A successful login can upgrade an old password work factor
with a compare-and-swap update. If the password changed concurrently, Forge
does not issue a session from the stale credential.

`auth.NewLoginHandler` and `auth.NewLogoutHandler` are thin HTTP adapters.
The login response returns `access_token`, `token_type: "Bearer"`, and
`expires_at`. Logout accepts exactly one bearer credential and is idempotent;
a syntactically valid token that is unknown or already revoked still returns
`204`.

Applications should register these handlers only on POST routes.

## Login throttling

`LoginService` rate-limits every syntactically valid password attempt before
credential lookup. It derives SHA-256 keys for the tenant+email account and,
when available, the network source. The limiter therefore does not need to
store plaintext email addresses or IP addresses.

The default `MemoryLoginThrottler` is bounded to 20,000 buckets and uses a
15-minute window with five attempts per account and 300 attempts per source.
A successful login clears the account bucket but not the source bucket, so one
successful account does not reset password-spraying pressure from that source.

For horizontally scaled PostgreSQL deployments, use
`auth/postgres.NewLoginThrottler` through `WithLoginThrottler`. It stores
shared hashed counters in `forge_login_throttle` and updates account/source
counters atomically in one transaction. `PruneExpired` deletes expired buckets
in bounded batches using `SKIP LOCKED`, so multiple cleanup workers can run
without blocking each other.

A blocked login returns `login_throttled` with HTTP `429`; the HTTP adapter
also sends `Retry-After`. Source detection uses the socket peer from
`RemoteAddr` only. Forge does not trust `X-Forwarded-For` until an explicit
trusted-proxy policy exists.

See [ADR 0011](adr/0011-login-throttling.md).

## Browser cookie sessions and CSRF

Browser applications can use `auth.NewCookieLoginHandler`,
`Middleware.AuthenticateCookie`, `auth.RequireCSRF`, and
`auth.NewCookieLogoutHandler`. Cookie authentication is an explicit transport
and never falls back to bearer authentication.

A successful cookie login sets two host-only cookies:

- `__Host-forge_session`: the opaque session token with `Secure`,
  `HttpOnly`, `Path=/`, and `SameSite=Lax`;
- `__Host-forge_csrf`: a separate random 256-bit token with `Secure`,
  `Path=/`, and `SameSite=Lax`.

The cookie login response never contains the session token. It returns the CSRF
token and session expiry, and is marked `Cache-Control: no-store` and
`Pragma: no-cache`. The bearer login response now uses the same no-cache
headers because it contains the plaintext session credential.

Cookie login requires an HTTPS `Origin` header whose host exactly matches the
request host. This protects the login endpoint itself from login CSRF before a
CSRF cookie exists. Cookie mode therefore intentionally requires HTTPS;
non-browser/API clients can continue using the bearer login endpoint.

For unsafe methods, `RequireCSRF` requires exactly one
`X-CSRF-Token` header whose canonical base64url value matches the CSRF cookie
in constant time. Safe methods (`GET`, `HEAD`, `OPTIONS`, `TRACE`) pass
through and must remain side-effect free.

Cookie logout requires CSRF, revokes a valid session credential when present,
clears both cookies, and remains idempotent for unknown or already-revoked
sessions.

When MFA is enabled, cookie login returns `202 mfa_required` without setting
session or CSRF cookies. `NewCookieMFACompletionHandler` performs the second
factor exchange and only then creates both browser cookies. This prevents the
password step from accidentally creating an authenticated browser session.

See [ADR 0012](adr/0012-cookie-sessions-and-csrf.md) and [MFA.md](MFA.md).

## Remaining Phase 3 work

Generated application wiring, full audit instrumentation of the remaining
identity flows, encryption-key configuration for generated MFA deployments,
and equivalent database-enforcement strategies for MySQL, MariaDB and SQLite
are still planned.
