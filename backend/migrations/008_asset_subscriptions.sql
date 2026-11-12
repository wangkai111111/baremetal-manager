ALTER TABLE assets ADD COLUMN subscription_state text NOT NULL DEFAULT 'legacy' CHECK (subscription_state IN ('legacy','active','reclaim_pending'));
ALTER TABLE assets ADD COLUMN lease_end timestamptz;
ALTER TABLE assets ADD COLUMN unsubscribed_at timestamptz;
ALTER TABLE orders ADD COLUMN project_id bigint REFERENCES projects(id);
ALTER TABLE orders ADD COLUMN payable_cents bigint CHECK (payable_cents > 0);
ALTER TABLE orders ADD COLUMN term_cycle text CHECK (term_cycle IN ('hourly','daily','monthly','yearly'));
ALTER TABLE orders ADD COLUMN term_count integer CHECK (term_count BETWEEN 1 AND 36);
CREATE TABLE subscription_payments (
 id bigserial PRIMARY KEY, order_id bigint NOT NULL UNIQUE REFERENCES orders(id),
 account_id bigint NOT NULL REFERENCES accounts(id), paid_cents bigint NOT NULL CHECK (paid_cents > 0),
 paid_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE subscription_terms (
 id bigserial PRIMARY KEY, asset_id bigint NOT NULL REFERENCES assets(id),
 order_id bigint NOT NULL UNIQUE REFERENCES orders(id), payment_id bigint NOT NULL UNIQUE REFERENCES subscription_payments(id),
 starts_at timestamptz NOT NULL, ends_at timestamptz NOT NULL,
 paid_cents bigint NOT NULL CHECK (paid_cents > 0), refunded_cents bigint NOT NULL DEFAULT 0 CHECK (refunded_cents >= 0 AND refunded_cents <= paid_cents),
 CHECK (ends_at > starts_at)
);
CREATE INDEX subscription_terms_asset ON subscription_terms(asset_id);
CREATE TABLE subscription_refunds (
 id bigserial PRIMARY KEY, term_id bigint NOT NULL UNIQUE REFERENCES subscription_terms(id),
 account_id bigint NOT NULL REFERENCES accounts(id), amount_cents bigint NOT NULL CHECK (amount_cents >= 0),
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE subscription_requests (
 tenant_id bigint NOT NULL REFERENCES tenants(id), project_id bigint NOT NULL REFERENCES projects(id),
 request_key varchar(128) NOT NULL, fingerprint text NOT NULL, response jsonb NOT NULL,
 PRIMARY KEY (tenant_id,project_id,request_key)
);
