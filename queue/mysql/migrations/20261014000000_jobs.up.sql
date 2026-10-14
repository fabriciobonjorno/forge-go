CREATE TABLE forge_jobs (
    id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    queue VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    kind VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    payload MEDIUMTEXT CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL,
    state VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT 'pending',
    priority INT NOT NULL DEFAULT 0,
    attempts INT NOT NULL DEFAULT 0,
    max_attempts INT NOT NULL,
    run_at DATETIME(6) NOT NULL,
    idempotency_key VARCHAR(200) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NULL,
    -- The key while the job is pending or running, NULL otherwise: the unique
    -- index below then allows one active job per kind and key, and any number
    -- of finished ones.
    active_key VARCHAR(200) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin
        GENERATED ALWAYS AS (CASE WHEN state IN ('pending', 'running') THEN idempotency_key END) STORED,
    lease_token CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NULL,
    lease_until DATETIME(6) NULL,
    locked_by VARCHAR(128) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NULL,
    last_error TEXT CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NULL,
    enqueued_at DATETIME(6) NOT NULL,
    finished_at DATETIME(6) NULL,
    PRIMARY KEY (id),
    -- Claiming: the next due job of a queue, by priority then due time.
    KEY forge_jobs_claim_idx (state, queue, priority, run_at, id),
    -- Lease recovery.
    KEY forge_jobs_lease_idx (state, lease_until),
    -- Dead job listing and succeeded job pruning.
    KEY forge_jobs_finished_idx (state, finished_at, id),
    UNIQUE KEY forge_jobs_idempotency_idx (kind, active_key),
    CONSTRAINT forge_jobs_queue_name CHECK (queue REGEXP '^[a-z][a-z0-9_.-]{0,63}$'),
    CONSTRAINT forge_jobs_kind_name CHECK (kind REGEXP '^[a-z][a-z0-9_.-]{0,63}$'),
    CONSTRAINT forge_jobs_payload_json CHECK (JSON_VALID(payload) AND LENGTH(payload) <= 1048576),
    CONSTRAINT forge_jobs_state_name CHECK (state IN ('pending', 'running', 'succeeded', 'dead')),
    CONSTRAINT forge_jobs_attempts CHECK (attempts >= 0 AND max_attempts BETWEEN 1 AND 1000),
    CONSTRAINT forge_jobs_key_not_empty CHECK (idempotency_key IS NULL OR idempotency_key <> ''),
    CONSTRAINT forge_jobs_lease_state CHECK ((state = 'running') = (lease_token IS NOT NULL AND lease_until IS NOT NULL)),
    CONSTRAINT forge_jobs_finished_state CHECK ((state IN ('succeeded', 'dead')) = (finished_at IS NOT NULL))
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4;
