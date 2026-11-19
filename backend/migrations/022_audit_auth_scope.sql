-- Authentication/platform events have no project. Runtime audits remain append-only.
ALTER TABLE audits ALTER COLUMN tenant_id DROP NOT NULL;
ALTER TABLE audits ALTER COLUMN project_id DROP NOT NULL;
ALTER TABLE audits ADD CONSTRAINT ck_audits_project_organization CHECK (project_id IS NULL OR tenant_id IS NOT NULL);
CREATE TABLE revoked_login_tokens (
 token_hash text PRIMARY KEY,
 user_id bigint NOT NULL REFERENCES users(id),
 expires_at timestamptz NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX ix_revoked_login_tokens_expiry ON revoked_login_tokens(expires_at);
CREATE INDEX ix_audits_target_submit ON audits (tenant_id, project_id, target_type, target_id, id) WHERE action='submit';
