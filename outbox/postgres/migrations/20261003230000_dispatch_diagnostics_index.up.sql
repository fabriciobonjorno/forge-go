-- forge:no-transaction
CREATE INDEX CONCURRENTLY forge_outbox_dead_letter_idx
    ON forge_outbox_events (dead_lettered_at, id)
    WHERE dead_lettered_at IS NOT NULL;
