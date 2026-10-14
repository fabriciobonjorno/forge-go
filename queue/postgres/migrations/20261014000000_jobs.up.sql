CREATE TABLE forge_jobs (
    id uuid PRIMARY KEY,
    queue varchar(64) NOT NULL CHECK (queue ~ '^[a-z][a-z0-9_.-]{0,63}$'),
    kind varchar(64) NOT NULL CHECK (kind ~ '^[a-z][a-z0-9_.-]{0,63}$'),
    payload jsonb NOT NULL CHECK (octet_length(payload::text) <= 1048576),
    state varchar(16) NOT NULL DEFAULT 'pending' CHECK (state IN ('pending', 'running', 'succeeded', 'dead')),
    priority integer NOT NULL DEFAULT 0,
    attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    max_attempts integer NOT NULL CHECK (max_attempts BETWEEN 1 AND 1000),
    run_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    idempotency_key varchar(200) CHECK (idempotency_key <> ''),
    lease_token uuid,
    lease_until timestamptz,
    locked_by varchar(128),
    last_error text CHECK (octet_length(last_error) <= 4096),
    enqueued_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    finished_at timestamptz,
    CONSTRAINT forge_jobs_lease_state CHECK ((state = 'running') = (lease_token IS NOT NULL AND lease_until IS NOT NULL)),
    CONSTRAINT forge_jobs_finished_state CHECK ((state IN ('succeeded', 'dead')) = (finished_at IS NOT NULL))
);

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
