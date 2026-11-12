ALTER TABLE assets
    ADD COLUMN monitor_next_at timestamptz NOT NULL DEFAULT now(),
    ADD COLUMN monitor_last_attempt_at timestamptz,
    ADD COLUMN monitor_consecutive_failures integer NOT NULL DEFAULT 0 CHECK (monitor_consecutive_failures >= 0),
    ADD COLUMN monitor_health text NOT NULL DEFAULT 'unknown' CHECK (monitor_health IN ('unknown','ok','warning','critical'));

ALTER TABLE operations ADD COLUMN automatic boolean NOT NULL DEFAULT false;
ALTER TABLE asset_monitor_configs ALTER COLUMN interval_seconds SET DEFAULT 300;
ALTER TABLE alert_channels DROP CONSTRAINT alert_channels_kind_check;
ALTER TABLE alert_channels ADD CONSTRAINT alert_channels_kind_check CHECK (kind IN ('email','webhook','lark','wechat','dingtalk'));
ALTER TABLE alert_channels
    ADD COLUMN last_test_status text NOT NULL DEFAULT 'untested' CHECK (last_test_status IN ('untested','succeeded','failed')),
    ADD COLUMN last_test_at timestamptz,
    ADD COLUMN last_test_message text,
    ADD COLUMN test_claim_until timestamptz;

CREATE INDEX ix_assets_redfish_monitor_due ON assets (monitor_next_at, id)
    WHERE deleted_at IS NULL AND status <> 'retired' AND bmc_protocol = 'redfish';
CREATE INDEX ix_operations_collect_active ON operations (tenant_id, project_id, asset_id)
    WHERE deleted_at IS NULL AND kind = 'collect' AND status IN ('pending','running','retry_wait');
CREATE INDEX ix_notification_deliveries_due ON notification_deliveries (next_attempt_at, id)
    WHERE deleted_at IS NULL AND status IN ('pending','sending');
