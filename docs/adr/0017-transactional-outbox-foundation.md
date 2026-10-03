# ADR 0017: Transactional outbox foundation

Status: accepted (Phase 4 foundation)

## Context

Application code must not publish externally from a retried database
transaction: retries can duplicate side effects, while rollback can leave an
external message for a state change that never committed. Forge's database
guide already requires those effects to happen after commit and identifies an
outbox as the future mechanism.

## Decision

- Define a portable `events.Event` envelope with UUIDv7 identity, bounded
  lowercase type, positive schema version, occurrence time, and a JSON payload
  capped at 1 MiB.
- Persist events in PostgreSQL through `outbox/postgres.Insert`. The caller
  passes the active `pgx.Tx` used for its domain transaction; the API does not
  accept a pool/connection, and no second transaction is opened by the adapter.
- Keep event payload schemas application-owned and opaque to Forge. Producers
  must avoid secrets and unnecessary personal data.
- Limit persistence support to PostgreSQL for this foundation. Do not claim
  that other adapters provide equivalent outbox guarantees.
- Do not add dispatch, retries, scheduling, or generated-app wiring in this
  slice. Those require a delivery contract and idempotent consumer design.

## Consequences

- A domain change and its event record can commit or roll back atomically.
- Insert is append-only by application contract; duplicate event IDs fail
  rather than silently accepting potentially different content.
- The initial schema migration has no down migration because dropping the table
  would silently destroy durable events. Operators must export and explicitly
  manage data before any manual removal.
- No delivery guarantee exists until a dispatcher is implemented. A future
  dispatcher should assume at-least-once delivery and require idempotent
  consumers, bounded retries, and an explicit retention policy.
- Applications own event version compatibility and payload privacy. Payloads
  are bounded in application validation and by the PostgreSQL schema.
