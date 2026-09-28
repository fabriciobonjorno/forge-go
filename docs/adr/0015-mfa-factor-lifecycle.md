# ADR 0015: MFA factor rotation and disable require step-up

Status: accepted (Phase 3)

## Decision

Forge treats TOTP rotation and disable as security-sensitive credential
changes, not ordinary profile updates.

Rotation is two-phase. Starting rotation creates a pending encrypted seed while
the current factor and backup codes remain active. Confirming the replacement
requires proof of the pending TOTP seed and atomically:

1. replaces the active encrypted factor and replay counter;
2. replaces the complete backup-code set;
3. increments the user's `session_version`;
4. revokes all active sessions;
5. consumes outstanding MFA login challenges;
6. deletes the pending enrollment.

Disabling MFA atomically removes the active factor, pending enrollment and
backup codes, then increments `session_version`, revokes sessions and consumes
outstanding MFA challenges.

The core keeps lifecycle persistence in the optional `MFALifecycleStore`
interface rather than expanding the baseline `MFAStore`. Custom MFA stores
that implement the Phase 3 TOTP login foundation therefore do not silently gain
credential-change capabilities.

HTTP lifecycle handlers require an application-supplied
`MFAChangeAuthorizer` on every sensitive operation: start rotation, confirm
rotation and disable. The framework intentionally does not equate possession
of an ordinary session with sufficient authority to weaken or replace MFA.
Applications should implement recent-password, verified-factor, or equivalent
step-up policy.

## Consequences

- A new seed cannot silently replace the current factor before possession is
  proven.
- Starting a rotation does not lock the user out of the current factor.
- Completing rotation invalidates all previous backup codes and sessions.
- Disabling MFA signs the user out everywhere.
- A stolen ordinary session is not, by itself, sufficient to rotate or disable
  MFA when the application supplies an appropriate step-up authorizer.
- Applications remain responsible for defining the exact step-up policy.
- Audit events for lifecycle changes remain a separate Phase 3 task.
