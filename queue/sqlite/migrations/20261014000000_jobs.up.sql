CREATE TABLE forge_jobs (
    id TEXT NOT NULL PRIMARY KEY CHECK (length(id) = 36),
    queue TEXT NOT NULL CHECK (length(queue) BETWEEN 1 AND 64 AND queue GLOB '[a-z]*' AND queue NOT GLOB '*[^a-z0-9_.-]*'),
    kind TEXT NOT NULL CHECK (length(kind) BETWEEN 1 AND 64 AND kind GLOB '[a-z]*' AND kind NOT GLOB '*[^a-z0-9_.-]*'),
    payload TEXT NOT NULL CHECK (json_valid(payload) AND length(CAST(payload AS BLOB)) <= 1048576),
    state TEXT NOT NULL DEFAULT 'pending' CHECK (state IN ('pending', 'running', 'succeeded', 'dead')),
    priority INTEGER NOT NULL DEFAULT 0,
    attempts INTEGER NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    max_attempts INTEGER NOT NULL CHECK (max_attempts BETWEEN 1 AND 1000),
    -- Timestamps are UTC text, "YYYY-MM-DD HH:MM:SS.ffffff", so they compare
    -- and sort chronologically.
    run_at TEXT NOT NULL,
    idempotency_key TEXT CHECK (idempotency_key IS NULL OR length(idempotency_key) > 0),
    lease_token TEXT,
    lease_until TEXT,
    locked_by TEXT,
    last_error TEXT,
    enqueued_at TEXT NOT NULL,
    finished_at TEXT,
    CHECK ((state = 'running') = (lease_token IS NOT NULL AND lease_until IS NOT NULL)),
    CHECK ((state IN ('succeeded', 'dead')) = (finished_at IS NOT NULL))
) STRICT;

-- Claiming: the next due job of a queue, by priority then due time.
CREATE INDEX forge_jobs_claim_idx ON forge_jobs (queue, priority, run_at, id) WHERE state = 'pending';
-- Lease recovery: running jobs by lease expiry.
CREATE INDEX forge_jobs_lease_idx ON forge_jobs (lease_until) WHERE state = 'running';
-- Dead job listing and succeeded job pruning.
CREATE INDEX forge_jobs_dead_idx ON forge_jobs (finished_at, id) WHERE state = 'dead';
CREATE INDEX forge_jobs_prune_idx ON forge_jobs (finished_at) WHERE state = 'succeeded';
-- Statistics per queue and state.
CREATE INDEX forge_jobs_state_idx ON forge_jobs (state, queue);
-- Idempotency: one active job per kind and key.
CREATE UNIQUE INDEX forge_jobs_idempotency_idx ON forge_jobs (kind, idempotency_key)
    WHERE idempotency_key IS NOT NULL AND state IN ('pending', 'running');
