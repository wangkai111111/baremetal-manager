CREATE TABLE IF NOT EXISTS image_download_jobs (
    id bigserial PRIMARY KEY,
    tenant_id bigint NOT NULL REFERENCES tenants(id),
    project_id bigint NOT NULL,
    user_id bigint NOT NULL REFERENCES users(id),
    catalog_id text NOT NULL CHECK (catalog_id = 'ubuntu-22.04.5-amd64'),
    status text NOT NULL CHECK (status IN ('queued','downloading','verifying','ready','failed')),
    downloaded_bytes bigint NOT NULL DEFAULT 0 CHECK (downloaded_bytes >= 0),
    total_bytes bigint NOT NULL DEFAULT 0 CHECK (total_bytes >= 0),
    error text NOT NULL DEFAULT '',
    image_id bigint,
    claim_token text,
    locked_until timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, project_id, catalog_id),
    FOREIGN KEY (tenant_id, project_id) REFERENCES projects(tenant_id, id),
    FOREIGN KEY (tenant_id, project_id, image_id) REFERENCES images(tenant_id, project_id, id),
    CHECK (status <> 'ready' OR image_id IS NOT NULL),
    CHECK ((status IN ('downloading','verifying')) = (claim_token IS NOT NULL AND locked_until IS NOT NULL))
);
CREATE INDEX IF NOT EXISTS ix_image_download_jobs_poll ON image_download_jobs (status, locked_until, id);
