# ADR 0016: Structured security audit without arbitrary payloads

Status: accepted (Phase 3 foundation)

## Decision

Forge security audit events use a fixed structured payload instead of arbitrary
JSON or key/value metadata.

A `SecurityEvent` can contain only:

- a validated lowercase event kind;
- one of the fixed outcomes `succeeded`, `denied`, or `failed`;
- optional actor, subject, and membership UUIDv7 identifiers;
- optional account/source/credential SHA-256 digests;
- an optional occurrence time.

This shape is intentionally restrictive. Authentication systems routinely
handle bearer tokens, reset links, TOTP seeds, backup codes and passwords; an
unbounded metadata map makes accidental secret persistence too easy.

The PostgreSQL adapter stores events in `forge_security_audit_events`. Request
correlation is taken from `web.RequestID(ctx)`, not from caller-supplied audit
metadata. The database clock is used when no explicit occurrence time is
provided.

Actor/subject/membership columns intentionally have no foreign keys. Security
history must remain readable after an identity or membership is deleted.

The application repository exposes only append behavior for this table. Forge
does not claim cryptographic tamper evidence: a database owner can still alter
or delete rows. Stronger immutability, external log shipping and signed audit
streams are later hardening concerns.

## Consequences

- Normal audit calls have no field that accepts raw credentials or arbitrary
  secret-bearing payloads.
- Events can be correlated to HTTP logs through request ID.
- Audit history is not lost when identity rows disappear.
- Adding a new event kind does not require a table migration as long as it
  follows the validated kind syntax.
- The audit table is append-only by application contract, not by database-owner
  cryptographic enforcement.
- Service-level instrumentation can be added without changing the storage
  contract; Phase 3 login, logout, recovery/reset and MFA flows support it
  when an auditor is configured. The generated PostgreSQL login, MFA, and
  bearer logout flows wire the repository as auditor; framework cookie logout
  can also be composed with an auditor.
- Authentication middleware can optionally record syntactically valid
  rejected sessions by credential digest, and `RequireAudited` records the
  authenticated actor for permission denials. Missing/malformed credentials
  and resolver infrastructure errors are not persisted; rejected-session
  auditing can generate database writes and should be enabled with request
  limiting and retention/capacity planning.
- `SecurityAuditError.OperationApplied` distinguishes an audit append failure
  after an irreversible state transition from one that prevented a security
  operation from completing. HTTP adapters log the former while preserving the
  already-committed success response; they never encourage unsafe retries.
- Login and MFA session issuance remain stricter: when a success event cannot
  be persisted, the newly created opaque session is revoked when possible and
  its plaintext credential is not returned.
