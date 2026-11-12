ALTER TABLE payments DROP CONSTRAINT payments_method_check;
ALTER TABLE payments ADD CONSTRAINT payments_method_check CHECK (method IN ('alipay','wechat','transfer','balance'));
ALTER TABLE subscription_payments ADD COLUMN payment_id bigint UNIQUE REFERENCES payments(id);
-- Historical receipts without an actual payment remain unverifiable; never backfill from amounts alone.
