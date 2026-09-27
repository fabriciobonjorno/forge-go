# Authentication and Authorization

Phase 3 starts with a small API-oriented security core. It does not invent an
identity database or silently choose business rules for accounts, roles, and
memberships.

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

## Storage contract

A session table should store at least the token digest, user ID, tenant ID,
expiration, revocation time, and a credential/session version. The resolver
must join or otherwise validate the current user and membership state. Index
the digest uniquely. Never log, audit, or persist the plaintext bearer token.

Schemas and a concrete session repository remain application-owned in this
increment because account lifecycle, login identifiers, MFA, invitation, and
role models are product decisions. Password hashing, login endpoints, cookie
sessions/CSRF, recovery, MFA, and a generated identity schema are not yet
implemented and must not be inferred from the bearer middleware.
