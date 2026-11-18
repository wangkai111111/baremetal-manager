ALTER TABLE installations ADD COLUMN progress jsonb NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(progress)='array');
ALTER TABLE installations ADD COLUMN deadline_at timestamptz;
ALTER TABLE installations ADD COLUMN cleanup_pending boolean NOT NULL DEFAULT false;
UPDATE installations i SET deadline_at=i.created_at+make_interval(secs=>o.timeout_seconds)
FROM operations o WHERE o.id=i.operation_id;
CREATE INDEX ix_installation_deadlines ON installations(deadline_at) WHERE deleted_at IS NULL AND status IN ('pending','preparing','booting','installing','post_install','unknown');
COMMENT ON COLUMN installations.progress IS '持久化安装步骤、状态、时间、阶段截止时间与脱敏说明';
COMMENT ON COLUMN installations.deadline_at IS '整个安装任务的绝对截止时间；事件重复上报不会延长';
COMMENT ON COLUMN installations.cleanup_pending IS '失败后尚未确认测试 VM 停止；禁止新的装机任务并由看门狗继续收口';
