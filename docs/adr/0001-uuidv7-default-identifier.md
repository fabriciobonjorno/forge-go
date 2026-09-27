# ADR 0001: UUIDv7 as the default identifier

- **Status:** Accepted
- **Date:** 2026-09-26

## Context

Applications need identifiers for entities, requests, and events. Common
choices have drawbacks:

- Auto-increment integers leak volume and ordering, require a database round
  trip to allocate, and collide across shards and environments.
- UUIDv4 is random, which fragments B-tree indexes and makes insert
  performance degrade as tables grow.
- ULID and similar formats sort well but are not the IETF standard and need
  custom column types or string storage.

RFC 9562 defines UUIDv7: a 48-bit Unix millisecond timestamp followed by
random bits, in the standard 128-bit UUID layout. It sorts by creation time,
fits a native `uuid` column, and can be generated anywhere without
coordination.

## Decision

Forge uses UUIDv7 as its default identifier, provided by the `uuid` package
with no third-party dependency.

- `uuid.UUID` is a `[16]byte`. `uuid.New()` uses a process-wide generator
  seeded from `crypto/rand`.
- Generation is monotonic within a process: within one millisecond, or when
  the clock moves backwards, the random field is incremented instead of
  reseeded. Exhausting the sequence in one millisecond returns
  `ErrSequenceExhausted` rather than producing an out-of-order value.
- `Parse` accepts only canonical, version 7, RFC-variant strings.
- `UUID` implements text, JSON, `driver.Valuer`, and `sql.Scanner`.
- The HTTP server uses UUIDv7 for `X-Request-ID`. Generators (Phase 8) will
  use it for primary keys.
- `forge uuid` prints UUIDv7 values from the CLI.

## Consequences

- Inserts into time-ordered indexes stay mostly append-only.
- IDs expose creation time to millisecond precision. Where that is sensitive,
  applications must use a different, opaque identifier for public exposure.
- Ordering is guaranteed only within one process; across processes it is
  approximate (clock skew).
- Because `Parse` and `Scan` reject non-v7 UUIDs, existing data using other
  versions needs a different type.
- One package-level mutex serialises generation; see Phase 9 if it becomes a
  bottleneck.
