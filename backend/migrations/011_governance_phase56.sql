-- ============================================================
-- 011 Phase 5/6 治理增强：租户审核、告警四级分级与屏蔽、工单协作、批量任务框架、指标历史。
-- 全部语句可重复执行：补列使用 IF NOT EXISTS，CHECK 约束先 DROP IF EXISTS 再重建，
-- 不做破坏性回填，存量三级告警与存量 active 租户原样可读。
-- ============================================================

-- ------------------------------------------------------------
-- 1. 租户审核（运营模式自助注册进入 pending，平台管理员审核后方可使用项目侧能力）
-- ------------------------------------------------------------
ALTER TABLE tenants ADD COLUMN IF NOT EXISTS reviewed_at timestamptz;
ALTER TABLE tenants ADD COLUMN IF NOT EXISTS reviewed_by bigint REFERENCES users(id);
ALTER TABLE tenants ADD COLUMN IF NOT EXISTS review_note text;
ALTER TABLE tenants DROP CONSTRAINT IF EXISTS tenants_status_check;
ALTER TABLE tenants ADD CONSTRAINT tenants_status_check CHECK (status IN ('pending', 'active', 'disabled', 'rejected'));
COMMENT ON COLUMN tenants.status IS '租户状态：pending 待审核/active 正常/disabled 已停用/rejected 已拒绝';
COMMENT ON COLUMN tenants.reviewed_at IS '最近一次审核时间';
COMMENT ON COLUMN tenants.reviewed_by IS '审核用户主键';
COMMENT ON COLUMN tenants.review_note IS '审核意见或拒绝/停用原因';
CREATE INDEX IF NOT EXISTS idx_tenants_status ON tenants (status) WHERE deleted_at IS NULL;

-- ------------------------------------------------------------
-- 2. 告警分级由三级扩为四级（info/warning/critical/emergency），存量值全部保留合法
-- ------------------------------------------------------------
ALTER TABLE alerts DROP CONSTRAINT IF EXISTS alerts_severity_check;
ALTER TABLE alerts ADD CONSTRAINT alerts_severity_check CHECK (severity IN ('info', 'warning', 'critical', 'emergency'));
ALTER TABLE alert_rules DROP CONSTRAINT IF EXISTS alert_rules_severity_check;
ALTER TABLE alert_rules ADD CONSTRAINT alert_rules_severity_check CHECK (severity IN ('info', 'warning', 'critical', 'emergency'));
COMMENT ON COLUMN alerts.severity IS '严重级别：info/warning/critical/emergency';
COMMENT ON COLUMN alert_rules.severity IS '规则命中后使用的严重级别：info/warning/critical/emergency';

-- ------------------------------------------------------------
-- 3. 告警认领（认领人/认领时间；认领不改变既有状态机，仍归入 acknowledged）
-- ------------------------------------------------------------
ALTER TABLE alerts ADD COLUMN IF NOT EXISTS claimed_at timestamptz;
ALTER TABLE alerts ADD COLUMN IF NOT EXISTS claimed_by bigint REFERENCES users(id);
COMMENT ON COLUMN alerts.claimed_at IS '认领时间；空表示未认领';
COMMENT ON COLUMN alerts.claimed_by IS '认领用户主键';
CREATE INDEX IF NOT EXISTS ix_alerts_claimed_by ON alerts (claimed_by) WHERE claimed_by IS NOT NULL;

-- ------------------------------------------------------------
-- 4. 告警屏蔽规则：按资产 + 指标（去重键）+ 级别维度在生效区间内抑制新告警
-- ------------------------------------------------------------
CREATE TABLE IF NOT EXISTS alert_shields (
    id bigserial PRIMARY KEY,
    tenant_id bigint NOT NULL REFERENCES tenants(id),
    project_id bigint NOT NULL,
    asset_id bigint REFERENCES assets(id),
    metric text,
    severity text CHECK (severity IN ('info', 'warning', 'critical', 'emergency')),
    reason text NOT NULL,
    starts_at timestamptz NOT NULL DEFAULT now(),
    ends_at timestamptz NOT NULL DEFAULT now() + interval '1 hour',
    status text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'cancelled')),
    hit_count integer NOT NULL DEFAULT 0 CHECK (hit_count >= 0),
    last_hit_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz,
    create_by bigint REFERENCES users(id),
    update_by bigint REFERENCES users(id),
    UNIQUE (tenant_id, project_id, id),
    FOREIGN KEY (tenant_id, project_id) REFERENCES projects(tenant_id, id),
    FOREIGN KEY (tenant_id, project_id, asset_id) REFERENCES assets(tenant_id, project_id, id),
    CHECK (ends_at > starts_at)
);
COMMENT ON TABLE alert_shields IS '告警屏蔽规则；命中后不再新建告警活动，仅累计命中次数';
COMMENT ON COLUMN alert_shields.asset_id IS '限定资产；空表示全部资产';
COMMENT ON COLUMN alert_shields.metric IS '限定指标/告警去重键（支持右通配前缀，如 bmc.*）；空表示全部指标';
COMMENT ON COLUMN alert_shields.severity IS '限定最低级别；空表示全部级别';
COMMENT ON COLUMN alert_shields.reason IS '屏蔽原因（必填，供审计复核）';
COMMENT ON COLUMN alert_shields.starts_at IS '生效开始时间';
COMMENT ON COLUMN alert_shields.ends_at IS '失效时间；区间外规则自动不再命中';
COMMENT ON COLUMN alert_shields.status IS '规则状态：active 生效/cancelled 已取消';
COMMENT ON COLUMN alert_shields.hit_count IS '累计命中次数';
COMMENT ON COLUMN alert_shields.last_hit_at IS '最近命中时间';
CREATE INDEX IF NOT EXISTS ix_alert_shields_scope ON alert_shields (tenant_id, project_id, created_at DESC) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS ix_alert_shields_match ON alert_shields (tenant_id, project_id, asset_id) WHERE deleted_at IS NULL AND status = 'active';
CREATE INDEX IF NOT EXISTS ix_alert_shields_deleted ON alert_shields (deleted_at) WHERE deleted_at IS NOT NULL;
CREATE TRIGGER trg_alert_shields_updated BEFORE UPDATE ON alert_shields FOR EACH ROW EXECUTE FUNCTION bmm_touch_updated_at();

-- ------------------------------------------------------------
-- 5. 工单回复消息（客服工作台协作对话）
-- ------------------------------------------------------------
CREATE TABLE IF NOT EXISTS work_order_messages (
    id bigserial PRIMARY KEY,
    tenant_id bigint NOT NULL REFERENCES tenants(id),
    work_order_id bigint NOT NULL REFERENCES work_orders(id),
    user_id bigint NOT NULL REFERENCES users(id),
    content text NOT NULL,
    is_support boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz,
    create_by bigint REFERENCES users(id),
    update_by bigint REFERENCES users(id),
    UNIQUE (tenant_id, id),
    CHECK (length(trim(content)) > 0 AND length(content) <= 8000)
);
COMMENT ON TABLE work_order_messages IS '工单回复消息（两种模式通用）';
COMMENT ON COLUMN work_order_messages.work_order_id IS '所属工单';
COMMENT ON COLUMN work_order_messages.user_id IS '回复用户主键';
COMMENT ON COLUMN work_order_messages.content IS '回复正文';
COMMENT ON COLUMN work_order_messages.is_support IS '是否客服/处理人回复；false 表示提交人回复';
COMMENT ON COLUMN work_order_messages.deleted_at IS '软删除时间；空表示未删除';
CREATE INDEX IF NOT EXISTS ix_work_order_messages_order ON work_order_messages (work_order_id, id) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS ix_work_order_messages_tenant ON work_order_messages (tenant_id) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS ix_work_order_messages_deleted ON work_order_messages (deleted_at) WHERE deleted_at IS NOT NULL;
CREATE TRIGGER trg_work_order_messages_updated BEFORE UPDATE ON work_order_messages FOR EACH ROW EXECUTE FUNCTION bmm_touch_updated_at();

-- ------------------------------------------------------------
-- 6. 批量运维任务框架：批量脚本 / 批量配置 / 批量固件升级
--    任务与明细先落库为 queued，由 worker 执行器推进；无执行通道的类型显式置 unsupported。
-- ------------------------------------------------------------
CREATE TABLE IF NOT EXISTS batch_tasks (
    id bigserial PRIMARY KEY,
    tenant_id bigint NOT NULL REFERENCES tenants(id),
    project_id bigint NOT NULL,
    kind text NOT NULL CHECK (kind IN ('script', 'config', 'firmware_upgrade')),
    title text,
    payload jsonb NOT NULL DEFAULT '{}'::jsonb,
    asset_count integer NOT NULL DEFAULT 0 CHECK (asset_count >= 0),
    succeeded_count integer NOT NULL DEFAULT 0 CHECK (succeeded_count >= 0),
    failed_count integer NOT NULL DEFAULT 0 CHECK (failed_count >= 0),
    status text NOT NULL DEFAULT 'queued' CHECK (status IN ('queued', 'running', 'succeeded', 'partial', 'failed', 'unsupported', 'cancelled')),
    reason text,
    timeout_seconds integer NOT NULL DEFAULT 60 CHECK (timeout_seconds > 0),
    worker_id text,
    started_at timestamptz,
    completed_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz,
    create_by bigint REFERENCES users(id),
    update_by bigint REFERENCES users(id),
    UNIQUE (tenant_id, project_id, id),
    FOREIGN KEY (tenant_id, project_id) REFERENCES projects(tenant_id, id),
    CHECK (completed_at IS NULL OR (started_at IS NOT NULL AND completed_at >= started_at))
);
COMMENT ON TABLE batch_tasks IS '批量运维任务；payload 为脱敏参数，不得存明文凭据';
COMMENT ON COLUMN batch_tasks.kind IS '批量类型：script 脚本/config 配置/firmware_upgrade 固件升级';
COMMENT ON COLUMN batch_tasks.payload IS '执行参数（脚本正文与类型、配置项集合、目标固件包与版本）';
COMMENT ON COLUMN batch_tasks.status IS '任务状态：queued 待执行/running 执行中/succeeded 全部成功/partial 部分成功/failed 失败/unsupported 无执行通道/cancelled 已取消';
COMMENT ON COLUMN batch_tasks.reason IS '失败或 unsupported 的脱敏原因';
COMMENT ON COLUMN batch_tasks.worker_id IS '执行节点';
CREATE INDEX IF NOT EXISTS ix_batch_tasks_scope ON batch_tasks (tenant_id, project_id, created_at DESC) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS ix_batch_tasks_claim ON batch_tasks (id) WHERE deleted_at IS NULL AND status = 'queued';
CREATE INDEX IF NOT EXISTS ix_batch_tasks_deleted ON batch_tasks (deleted_at) WHERE deleted_at IS NOT NULL;
CREATE TRIGGER trg_batch_tasks_updated BEFORE UPDATE ON batch_tasks FOR EACH ROW EXECUTE FUNCTION bmm_touch_updated_at();

CREATE TABLE IF NOT EXISTS batch_task_items (
    id bigserial PRIMARY KEY,
    tenant_id bigint NOT NULL REFERENCES tenants(id),
    project_id bigint NOT NULL,
    batch_task_id bigint NOT NULL,
    asset_id bigint NOT NULL,
    status text NOT NULL DEFAULT 'queued' CHECK (status IN ('queued', 'running', 'succeeded', 'failed', 'unsupported', 'skipped')),
    detail text,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz,
    UNIQUE (tenant_id, project_id, id),
    FOREIGN KEY (tenant_id, project_id) REFERENCES projects(tenant_id, id),
    FOREIGN KEY (tenant_id, project_id, batch_task_id) REFERENCES batch_tasks(tenant_id, project_id, id),
    FOREIGN KEY (tenant_id, project_id, asset_id) REFERENCES assets(tenant_id, project_id, id),
    UNIQUE (batch_task_id, asset_id)
);
COMMENT ON TABLE batch_task_items IS '批量任务的逐资产执行明细';
COMMENT ON COLUMN batch_task_items.status IS '明细状态：queued/running/succeeded/failed/unsupported/skipped';
COMMENT ON COLUMN batch_task_items.detail IS '逐台执行结论或脱敏错误';
CREATE INDEX IF NOT EXISTS ix_batch_task_items_task ON batch_task_items (tenant_id, project_id, batch_task_id);
CREATE INDEX IF NOT EXISTS ix_batch_task_items_asset ON batch_task_items (tenant_id, project_id, asset_id);
CREATE INDEX IF NOT EXISTS ix_batch_task_items_deleted ON batch_task_items (deleted_at) WHERE deleted_at IS NOT NULL;
CREATE TRIGGER trg_batch_task_items_updated BEFORE UPDATE ON batch_task_items FOR EACH ROW EXECUTE FUNCTION bmm_touch_updated_at();
