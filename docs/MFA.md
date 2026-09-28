# Multi-Factor Authentication

Forge Phase 3 includes a TOTP MFA foundation in `auth` with PostgreSQL
persistence in `auth/postgres`. It is intentionally fail-closed: an account
whose password identity says MFA is enabled never receives a session from the
password step alone.

## TOTP profile

Forge uses the interoperable RFC 6238 profile commonly supported by
authenticator applications:

- HMAC-SHA-1;
- 6 decimal digits;
- 30-second periods;
- a 160-bit random seed;
- verification window of the current period plus one period before/after.

SHA-1 here is the HMAC algorithm required for broad TOTP interoperability; it
is not used for password hashing. Passwords continue to use the versioned
PBKDF2-HMAC-SHA256 format documented in [AUTHENTICATION.md](AUTHENTICATION.md).

Every accepted TOTP counter is persisted. A code from the same or an older
counter is rejected, preventing replay even when requests race.

## Secret protection

TOTP seeds must not be stored as plaintext. `MFAService` requires a
`SecretCipher`. Forge provides `NewAESGCMSecretCipher`, which uses
AES-256-GCM from the Go standard library and requires an exact 32-byte key.

The encryption key must come from an external secret source and must not be
stored in the identity database. Applications that need key IDs, managed KMS,
or online key rotation can implement the narrow `SecretCipher` port instead
of using the built-in single-key cipher.

PostgreSQL stores both authenticated ciphertext and a SHA-256 seed digest. The
digest is checked after decryption so a wrong key/corrupt ciphertext cannot be
silently accepted.

## Enrollment

`MFAService.BeginTOTPEnrollment` creates a pending seed and returns:

- the Base32 seed;
- an `otpauth://` provisioning URI;
- the enrollment expiry.

The PostgreSQL adapter never replaces an already-active factor through this
initial enrollment path. Rotation/disabling is a separate security-sensitive
flow and is not implemented yet.

The framework HTTP helper also requires an explicit
`MFAEnrollmentAuthorizer`. This is deliberate: merely possessing an existing
session is not sufficient proof for changing MFA. Applications should use the
authorizer for step-up policy such as recent password verification or another
strong reauthentication rule.

Confirmation verifies a live TOTP code, installs the factor atomically,
increments the user's `session_version`, revokes all current sessions, and
invalidates outstanding MFA challenges. The confirmation code's counter is
recorded, so that same code cannot immediately be replayed for login.

## Password login with MFA

`PasswordIdentity.MFARequired` tells `LoginService` that a live TOTP factor
exists. Configure the login service with the same MFA service:

```go
login, err := auth.NewLoginService(
    repository,
    repository,
    auth.WithMFAChallengeIssuer(mfa),
)
```

After a correct password, an MFA account receives HTTP `202`:

```json
{
  "mfa_required": true,
  "challenge_token": "<opaque 256-bit challenge>",
  "expires_at": "..."
}
```

No session is created at this point.

The challenge is digest-only in persistence and is bound to:

- the exact membership selected by the tenant-scoped password login;
- the user's current `session_version`;
- the exact active TOTP factor digest;
- a short challenge expiry;
- the session expiry originally selected by the password login.

Password reset/global revocation, factor replacement, membership/tenant
deactivation, or organization deactivation therefore makes an outstanding
challenge unusable.

Session creation is credential-version-bound as well. The password lookup
returns the exact `session_version` it verified; both direct password sessions
and MFA challenge issuance require that version to still be current. MFA
completion carries the challenge's version into final session creation. This
closes the race where a password reset could otherwise occur after credential
verification but before session persistence.

Submit the challenge and current TOTP code to
`NewMFACompletionHandler`. Successful completion atomically consumes the
challenge and advances the factor counter before creating the normal opaque
session. If session creation later fails, the challenge remains consumed; the
user starts a fresh password login rather than replaying it.

MFA attempts are rate-limited by domain-separated challenge and source hashes.
The default in-memory limiter allows five attempts per challenge and 60 per
source in a five-minute window. Multi-replica applications should supply a
shared throttler.

## PostgreSQL tables

`auth/postgres.Migrations()` adds:

- `forge_totp_enrollments` for short-lived pending seeds;
- `forge_totp_factors` for the active encrypted seed and replay counter;
- `forge_mfa_challenges` for digest-only one-time password-login challenges.

`Repository.PruneMFA(ctx, limit)` removes expired enrollments and
consumed/expired challenges in bounded `SKIP LOCKED` batches.

## Factor rotation and disable

Active TOTP factors are changed only through explicit lifecycle operations.
`BeginTOTPRotation` creates a pending replacement seed while the current
factor stays active. `ConfirmTOTPRotation` proves the new seed, replaces the
factor and backup-code set atomically, increments `session_version`, revokes
all sessions, and invalidates outstanding MFA challenges.

`DisableMFA` removes the active factor, pending enrollment and backup codes,
then also increments `session_version`, revokes sessions, and invalidates
challenges.

The HTTP adapters require an application-supplied `MFAChangeAuthorizer` for
starting rotation, confirming rotation, and disabling MFA. Applications should
use it for strong step-up authorization such as recent password verification or
another independently verified factor. An ordinary authenticated session alone
is deliberately insufficient.

See [ADR 0015](adr/0015-mfa-factor-lifecycle.md).

## Remaining MFA work

Still required before Phase 3 MFA can be considered complete:

- reconciliation with browser-cookie login so MFA challenges never set a
  session cookie before the second factor;
- generated application wiring and encryption-key configuration;
- audit events for enrollment, factor changes, MFA failures and recovery use.

See also [ADR 0014](adr/0014-totp-mfa.md).
