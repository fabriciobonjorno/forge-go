CREATE TABLE forge_agent_executions (
    execution_id uuid PRIMARY KEY,
    intent_id uuid NOT NULL,
    actor_id uuid NOT NULL,
    tenant_id uuid NOT NULL,
    action text NOT NULL CHECK (action ~ '^[a-z][a-z0-9_.-]{1,62}$'),
    effect smallint NOT NULL CHECK (effect BETWEEN 0 AND 4),
    status text NOT NULL CHECK (status IN ('reserved', 'denied', 'succeeded', 'failed', 'unresolved')),
    failure_code text CHECK (failure_code IS NULL OR failure_code ~ '^[a-z][a-z0-9_.-]{2,63}$'),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    completed_at timestamptz,
    CHECK ((status = 'reserved' AND completed_at IS NULL) OR
           (status <> 'reserved' AND completed_at IS NOT NULL)),
    UNIQUE (tenant_id, actor_id, intent_id),
    UNIQUE (tenant_id, execution_id)
);

ALTER TABLE forge_agent_executions ENABLE ROW LEVEL SECURITY;
ALTER TABLE forge_agent_executions FORCE ROW LEVEL SECURITY;

CREATE POLICY forge_agent_executions_tenant ON forge_agent_executions
    USING (tenant_id = NULLIF(current_setting('forge.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('forge.tenant_id', true), '')::uuid);

CREATE INDEX forge_agent_executions_recent_idx
    ON forge_agent_executions (tenant_id, created_at DESC, execution_id DESC);

CREATE INDEX forge_agent_executions_unresolved_idx
    ON forge_agent_executions (tenant_id, created_at ASC, execution_id ASC)
    WHERE status IN ('reserved', 'unresolved');

CREATE TABLE forge_agent_reconciliations (
    reconciliation_id uuid PRIMARY KEY,
    execution_id uuid NOT NULL,
    tenant_id uuid NOT NULL,
    reconciled_by uuid NOT NULL,
    result text NOT NULL CHECK (result IN ('effect_applied', 'effect_not_applied')),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    UNIQUE (execution_id),
    FOREIGN KEY (tenant_id, execution_id)
        REFERENCES forge_agent_executions (tenant_id, execution_id) ON DELETE RESTRICT
);

ALTER TABLE forge_agent_reconciliations ENABLE ROW LEVEL SECURITY;
ALTER TABLE forge_agent_reconciliations FORCE ROW LEVEL SECURITY;

CREATE POLICY forge_agent_reconciliations_tenant ON forge_agent_reconciliations
    USING (tenant_id = NULLIF(current_setting('forge.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('forge.tenant_id', true), '')::uuid);

CREATE INDEX forge_agent_reconciliations_recent_idx
    ON forge_agent_reconciliations (tenant_id, created_at DESC, reconciliation_id DESC);
