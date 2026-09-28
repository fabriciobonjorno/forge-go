# Security Audit Events

Forge Phase 3 provides a structured security-audit foundation in `auth` and
PostgreSQL persistence through `auth/postgres.Repository`.

## Event shape

`auth.SecurityEvent` intentionally has a closed set of fields:

- event kind and outcome;
- optional actor, subject, and membership UUIDv7 identifiers;
- optional account/source/credential SHA-256 digests;
- optional occurrence time.

There is no arbitrary metadata map. Passwords, bearer tokens, password-recovery
links, TOTP seeds, MFA challenge tokens, and backup codes must never be placed
in audit records.

The event kinds defined by the framework cover login, session revocation,
password recovery/reset, MFA enrollment/challenge/backup-code use, factor
rotation, and factor disable. Applications may add names that follow the same
lowercase dotted syntax, but should keep event payloads within the fixed safe
field set.

## PostgreSQL storage

`auth/postgres.Migrations()` adds `forge_security_audit_events`.

The table stores:

- UUIDv7 event ID;
- kind and outcome;
- actor/subject/membership identifiers without foreign keys;
- account/source/credential digests;
- request ID;
- occurrence timestamp.

Identity IDs deliberately have no foreign-key constraints. Audit history must
survive identity deletion or later schema cleanup.

`Repository.RecordSecurityEvent` validates the event before insert and derives
the request ID from `web.RequestID(ctx)`. When `OccurredAt` is omitted, the
PostgreSQL database clock records the event time.

The repository exposes insertion only; it has no update/delete API for audit
events. Database owners can still modify the table, so this is append-only by
application contract rather than tamper-proof storage.

## Operational guidance

Treat the audit table as security-sensitive data. Grant the serving role insert
and the minimum read access required by operations. Administrative reporting
should use a separate read path/role where practical.

Audit events are not a place for debugging payloads. Use request-correlated
application logs for detailed diagnostics and keep secrets out of both systems.

This increment establishes the durable event contract. Wiring every identity
flow to emit events is intentionally incremental, especially while recovery and
MFA are landing on separate Phase 3 branches.

See [ADR 0016](adr/0016-structured-security-audit.md).
