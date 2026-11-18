-- Explicit data access is separate from project operation roles. No implicit grants
-- are migrated for non-platform users: administrators assign their scope explicitly.
CREATE TABLE user_access_grants (
 id BIGSERIAL PRIMARY KEY,
 tenant_id BIGINT NOT NULL REFERENCES tenants(id),
 user_id BIGINT NOT NULL REFERENCES users(id),
 department_id BIGINT REFERENCES departments(id),
 is_admin BOOLEAN NOT NULL DEFAULT false,
 create_by BIGINT NOT NULL DEFAULT 0,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX user_access_grants_scope ON user_access_grants(tenant_id,user_id,COALESCE(department_id,0));
CREATE INDEX user_access_grants_user ON user_access_grants(user_id,tenant_id);
COMMENT ON TABLE user_access_grants IS '组织/部门资产访问授权；空部门表示整个组织，部门授权含子部门；管理员仅管理授权范围';
-- Preserve existing duplicate history while making future edits deterministic.
UPDATE asset_depreciation d SET deleted_at=now() WHERE d.deleted_at IS NULL AND EXISTS
 (SELECT 1 FROM asset_depreciation newer WHERE newer.tenant_id=d.tenant_id AND newer.asset_id=d.asset_id AND newer.deleted_at IS NULL AND newer.id>d.id);
CREATE UNIQUE INDEX asset_depreciation_active_asset ON asset_depreciation(tenant_id,asset_id) WHERE deleted_at IS NULL;
