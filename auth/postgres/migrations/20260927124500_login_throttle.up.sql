CREATE TABLE forge_login_throttle (
    scope smallint NOT NULL CHECK (scope IN (1, 2)),
    key_hash bytea NOT NULL CHECK (octet_length(key_hash) = 32),
    attempt_count integer NOT NULL CHECK (attempt_count > 0),
    reset_at timestamptz NOT NULL,
    PRIMARY KEY (scope, key_hash)
);

CREATE INDEX forge_login_throttle_reset_at_idx
    ON forge_login_throttle (reset_at);
