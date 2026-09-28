# ADR 0014: TOTP MFA with encrypted seeds and replay-safe challenges

Status: accepted (Phase 3 foundation)

## Decision

Forge's first built-in second factor is TOTP. The core implementation uses only
the Go standard library and the interoperable RFC 6238 profile: a 160-bit
random seed, HMAC-SHA-1, six digits and a 30-second period. Verification accepts
the current period plus one adjacent period in either direction.

TOTP seeds are treated as long-lived credentials. `MFAService` therefore
requires authenticated encryption before persistence. The built-in
`AESGCMSecretCipher` uses AES-256-GCM with an application-supplied 32-byte
key. Applications with KMS or key-rotation requirements implement
`SecretCipher` instead.

Enrollment is two-phase. A pending encrypted seed is created first and becomes
active only after the user proves possession with a valid TOTP code. Initial
enrollment refuses to replace an existing active factor. The HTTP adapter also
requires an application-supplied step-up authorizer instead of treating an
ordinary authenticated session as sufficient authority to change MFA.

Confirming enrollment increments `session_version`, revokes current sessions
and invalidates outstanding MFA challenges. The TOTP counter used for
confirmation becomes the factor's initial replay watermark.

Password login remains tenant-scoped. After a correct password,
`LoginService` checks the server-side password identity's MFA state. An MFA
account receives a fresh opaque challenge rather than a session.

PostgreSQL stores only the challenge digest. Each challenge is bound to the
selected membership, current user credential version, active factor digest,
challenge expiry and intended session expiry. Creating a newer challenge for
the same membership consumes older outstanding challenges.

Completing MFA loads the current encrypted factor, verifies a TOTP code, then
in one transaction locks the challenge/factor/account state, revalidates all
bindings, requires a strictly greater TOTP counter, advances that counter and
consumes the challenge. Only after this transaction succeeds is the normal
opaque application session created.

Session persistence is version-bound. Password lookup returns the
`session_version` that was verified. Direct password session creation and MFA
challenge issuance both require that version to remain current, and MFA
completion creates its final session only at the version stored on the
challenge. A password reset or global revocation racing any of those boundaries
therefore prevents the stale authentication from creating a live session.

## Consequences

- A password alone cannot create a session for MFA-enabled accounts.
- A database-only disclosure does not directly reveal plaintext TOTP seeds or
  challenge credentials, though compromise of both the database and encryption
  key defeats this protection.
- The same TOTP time counter cannot authenticate twice, including concurrent
  attempts.
- Password reset/global revocation invalidates outstanding MFA challenges via
  the credential-version binding.
- Factor replacement invalidates outstanding challenges via the factor-digest
  binding.
- Enabling MFA signs out existing sessions, forcing the next login to exercise
  the new factor.
- The confirmation TOTP cannot immediately be reused for login; users may need
  to wait for the next 30-second period.
- The built-in AES-GCM cipher is single-key. Managed rotation requires a custom
  `SecretCipher` or a future versioned keyring.
- Backup codes, factor disable/rotation, cookie-session reconciliation,
  generated wiring and MFA audit events remain follow-up work.
