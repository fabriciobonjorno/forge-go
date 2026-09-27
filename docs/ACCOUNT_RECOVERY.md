# Account Recovery

Forge provides a provider-neutral password-recovery flow in the `auth`
package and PostgreSQL persistence in `auth/postgres`.

## Requesting recovery

Create a `RecoveryService` with a recovery store and a `RecoverySender`.
The sender receives a redacted `RecoveryToken` value whose plaintext is
available only through `Reveal()`.

```go
recovery, err := auth.NewRecoveryService(repository, sender)
if err != nil {
    return err
}

handler, err := auth.NewPasswordRecoveryRequestHandler(recovery)
if err != nil {
    return err
}

app.Handle("POST /auth/recovery", handler)
```

The request body is:

```json
{
  "email": "alice@example.com",
  "tenant": "acme"
}
```

Known and unknown identities both receive HTTP `202` with
`{"status":"accepted"}`. Invalid email/tenant syntax is also treated as an
accepted no-op. This avoids exposing account or membership existence through
the response body or status.

The default recovery throttle allows three requests per tenant+account and 30
per source per one-hour window. The keys are SHA-256 domain-separated hashes;
plaintext emails and network sources are not stored by the limiter. A blocked
request returns `429 recovery_throttled` with `Retry-After`.

For multi-replica PostgreSQL applications, use
`auth/postgres.NewRecoveryThrottler` through
`auth.WithRecoveryThrottler`. It uses the shared PostgreSQL throttle table,
while recovery keys use a separate hash domain and do not collide with login
keys.

`RecoverySender` should enqueue delivery and return promptly. A synchronous
SMTP sender can create a measurable timing difference between known and unknown
identities even though the HTTP response is otherwise uniform.

## Resetting the password

The reset endpoint accepts:

```json
{
  "token": "<opaque recovery token>",
  "new_password": "<new password>"
}
```

Register it as a POST route:

```go
reset, err := auth.NewPasswordResetHandler(recovery)
if err != nil {
    return err
}

app.Handle("POST /auth/reset-password", reset)
```

Malformed, expired, unknown, consumed, or superseded tokens all return the same
`400 recovery_invalid` response. Reset attempts are throttled before PBKDF2
password hashing to bound CPU abuse.

On PostgreSQL, a successful reset atomically replaces the password hash,
increments `session_version`, revokes all sessions for the user, and consumes
all outstanding recovery tokens. The same token cannot be used twice, and two
different valid links racing for one user cannot both win.

Successful reset responses are `204`. All responses from the recovery request
and reset handlers, including errors and throttling responses, are marked
`Cache-Control: no-store` / `Pragma: no-cache`.

## Storage and cleanup

`auth/postgres.Migrations()` adds `forge_password_recovery` with a unique
32-byte token digest, user ID, expiry, consumption time, and creation time.

`Repository.PruneRecovery(ctx, limit)` deletes at most `limit` consumed or
expired rows using `SKIP LOCKED`. Run it periodically; Phase 4 scheduling can
own that periodic work later.

See [ADR 0013](adr/0013-password-recovery.md).
