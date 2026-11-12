ALTER TABLE orders ADD COLUMN IF NOT EXISTS payment_id bigint REFERENCES payments(id);
ALTER TABLE orders ADD COLUMN IF NOT EXISTS term_starts_at timestamptz;
ALTER TABLE orders ADD COLUMN IF NOT EXISTS term_ends_at timestamptz;
ALTER TABLE orders ADD CONSTRAINT orders_payment_unique UNIQUE (payment_id);
ALTER TABLE subscription_refunds ADD COLUMN IF NOT EXISTS request_key varchar(128);
CREATE UNIQUE INDEX IF NOT EXISTS subscription_refunds_request_key ON subscription_refunds(account_id, request_key) WHERE request_key IS NOT NULL;
CREATE INDEX IF NOT EXISTS subscription_terms_asset_time ON subscription_terms(asset_id, starts_at, ends_at);
