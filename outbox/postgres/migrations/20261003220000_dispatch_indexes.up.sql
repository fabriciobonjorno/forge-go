-- forge:no-transaction
CREATE INDEX CONCURRENTLY forge_outbox_claim_idx
    ON forge_outbox_events (available_at, enqueued_at, id)
    WHERE delivered_at IS NULL AND dead_lettered_at IS NULL;
