-- forge:no-transaction
CREATE INDEX CONCURRENTLY forge_outbox_pending_age_idx
    ON forge_outbox_events (enqueued_at)
    WHERE delivered_at IS NULL AND dead_lettered_at IS NULL;
