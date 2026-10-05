ALTER TABLE forge_outbox_events
    ADD COLUMN attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    ADD COLUMN available_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
    ADD COLUMN lease_token uuid,
    ADD COLUMN lease_until timestamptz,
    ADD COLUMN delivered_at timestamptz,
    ADD COLUMN dead_lettered_at timestamptz,
    ADD CONSTRAINT forge_outbox_lease_pair CHECK ((lease_token IS NULL) = (lease_until IS NULL)),
    ADD CONSTRAINT forge_outbox_terminal_state CHECK (NOT (delivered_at IS NOT NULL AND dead_lettered_at IS NOT NULL));
