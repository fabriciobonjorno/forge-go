CREATE TABLE forge_outbox_events (
    id uuid PRIMARY KEY,
    event_type varchar(200) NOT NULL CHECK (event_type ~ '^[a-z][a-z0-9_.-]{2,199}$'),
    schema_version integer NOT NULL CHECK (schema_version > 0),
    occurred_at timestamptz NOT NULL,
    payload jsonb NOT NULL CHECK (octet_length(payload::text) <= 1048576),
    enqueued_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
