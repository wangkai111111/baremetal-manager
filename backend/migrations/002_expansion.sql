-- 002_expansion.sql: 数据库扩展迁移
-- 新增机房、机柜、标签、传感器、告警规则、API密钥、系统设置、装机模板等表
-- 扩展现有 assets、projects、operations、alerts、users、networks、images、firmware_inventories 表字段

-- ============================================================
-- 一、新增表
-- ============================================================

-- 1. 机房信息表
CREATE TABLE IF NOT EXISTS datacenters (
  id BIGSERIAL PRIMARY KEY,
  name VARCHAR(200) NOT NULL,
  code VARCHAR(50),
  location VARCHAR(500),
  description TEXT,
  project_id BIGINT NOT NULL REFERENCES projects(id),
  contact VARCHAR(200),
  capacity INTEGER DEFAULT 0,
  create_by BIGINT,
  update_by BIGINT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  deleted_at TIMESTAMPTZ
);
COMMENT ON TABLE datacenters IS '机房信息表';
COMMENT ON COLUMN datacenters.id IS '机房主键';
COMMENT ON COLUMN datacenters.name IS '机房名称';
COMMENT ON COLUMN datacenters.code IS '机房编码';
COMMENT ON COLUMN datacenters.location IS '机房位置';
COMMENT ON COLUMN datacenters.description IS '机房描述';
COMMENT ON COLUMN datacenters.project_id IS '所属项目主键';
COMMENT ON COLUMN datacenters.contact IS '联系人';
COMMENT ON COLUMN datacenters.capacity IS '容量（机柜数）';
COMMENT ON COLUMN datacenters.create_by IS '创建用户主键';
COMMENT ON COLUMN datacenters.update_by IS '更新用户主键';
COMMENT ON COLUMN datacenters.created_at IS '创建时间';
COMMENT ON COLUMN datacenters.updated_at IS '更新时间';
COMMENT ON COLUMN datacenters.deleted_at IS '软删除时间';

-- 2. 机柜信息表
CREATE TABLE IF NOT EXISTS racks (
  id BIGSERIAL PRIMARY KEY,
  name VARCHAR(200) NOT NULL,
  datacenter_id BIGINT NOT NULL REFERENCES datacenters(id),
  project_id BIGINT NOT NULL REFERENCES projects(id),
  height_u INTEGER DEFAULT 42,
  location VARCHAR(500),
  power_capacity VARCHAR(100),
  create_by BIGINT,
  update_by BIGINT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  deleted_at TIMESTAMPTZ
);
COMMENT ON TABLE racks IS '机柜信息表';
COMMENT ON COLUMN racks.id IS '机柜主键';
COMMENT ON COLUMN racks.name IS '机柜名称';
COMMENT ON COLUMN racks.datacenter_id IS '所属机房主键';
COMMENT ON COLUMN racks.project_id IS '所属项目主键';
COMMENT ON COLUMN racks.height_u IS 'U位高度';
COMMENT ON COLUMN racks.location IS '机柜位置';
COMMENT ON COLUMN racks.power_capacity IS '供电容量';
COMMENT ON COLUMN racks.create_by IS '创建用户主键';
COMMENT ON COLUMN racks.update_by IS '更新用户主键';
COMMENT ON COLUMN racks.created_at IS '创建时间';
COMMENT ON COLUMN racks.updated_at IS '更新时间';
COMMENT ON COLUMN racks.deleted_at IS '软删除时间';

-- 3. 标签定义表
CREATE TABLE IF NOT EXISTS tags (
  id BIGSERIAL PRIMARY KEY,
  name VARCHAR(100) NOT NULL,
  color VARCHAR(20),
  project_id BIGINT NOT NULL REFERENCES projects(id),
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  UNIQUE (project_id, name)
);
COMMENT ON TABLE tags IS '标签定义表';
COMMENT ON COLUMN tags.id IS '标签主键';
COMMENT ON COLUMN tags.name IS '标签名称';
COMMENT ON COLUMN tags.color IS '标签颜色';
COMMENT ON COLUMN tags.project_id IS '所属项目主键';
COMMENT ON COLUMN tags.created_at IS '创建时间';
COMMENT ON COLUMN tags.updated_at IS '更新时间';

-- 4. 资产-标签关联表
CREATE TABLE IF NOT EXISTS asset_tags (
  asset_id BIGINT NOT NULL REFERENCES assets(id),
  tag_id BIGINT NOT NULL REFERENCES tags(id),
  PRIMARY KEY (asset_id, tag_id)
);
COMMENT ON TABLE asset_tags IS '资产与标签关联表';
COMMENT ON COLUMN asset_tags.asset_id IS '资产主键';
COMMENT ON COLUMN asset_tags.tag_id IS '标签主键';

-- 5. 传感器历史数据表
CREATE TABLE IF NOT EXISTS sensor_records (
  id BIGSERIAL PRIMARY KEY,
  asset_id BIGINT NOT NULL REFERENCES assets(id),
  sensor_type VARCHAR(50) NOT NULL,
  value DECIMAL(10,2),
  unit VARCHAR(20),
  collected_at TIMESTAMPTZ NOT NULL
);
COMMENT ON TABLE sensor_records IS '传感器历史数据表';
COMMENT ON COLUMN sensor_records.id IS '记录主键';
COMMENT ON COLUMN sensor_records.asset_id IS '所属资产主键';
COMMENT ON COLUMN sensor_records.sensor_type IS '传感器类型（如cpu_temp/fan_speed/power等）';
COMMENT ON COLUMN sensor_records.value IS '传感器数值';
COMMENT ON COLUMN sensor_records.unit IS '计量单位';
COMMENT ON COLUMN sensor_records.collected_at IS '采集时间';

-- 6. 告警规则表
CREATE TABLE IF NOT EXISTS alert_rules (
  id BIGSERIAL PRIMARY KEY,
  name VARCHAR(200) NOT NULL,
  project_id BIGINT NOT NULL REFERENCES projects(id),
  metric VARCHAR(100),
  operator VARCHAR(10),
  threshold DECIMAL(10,2),
  severity VARCHAR(20),
  enabled BOOLEAN DEFAULT true,
  time_window INTEGER DEFAULT 300,
  create_by BIGINT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  deleted_at TIMESTAMPTZ
);
COMMENT ON TABLE alert_rules IS '告警规则表';
COMMENT ON COLUMN alert_rules.id IS '规则主键';
COMMENT ON COLUMN alert_rules.name IS '规则名称';
COMMENT ON COLUMN alert_rules.project_id IS '所属项目主键';
COMMENT ON COLUMN alert_rules.metric IS '监控指标';
COMMENT ON COLUMN alert_rules.operator IS '比较运算符（如>、<、=）';
COMMENT ON COLUMN alert_rules.threshold IS '告警阈值';
COMMENT ON COLUMN alert_rules.severity IS '告警等级（INFO/WARNING/CRITICAL）';
COMMENT ON COLUMN alert_rules.enabled IS '是否启用';
COMMENT ON COLUMN alert_rules.time_window IS '生效时间窗口（秒）';
COMMENT ON COLUMN alert_rules.create_by IS '创建用户主键';
COMMENT ON COLUMN alert_rules.created_at IS '创建时间';
COMMENT ON COLUMN alert_rules.updated_at IS '更新时间';
COMMENT ON COLUMN alert_rules.deleted_at IS '软删除时间';

-- 7. 告警推送渠道表
CREATE TABLE IF NOT EXISTS alert_channels (
  id BIGSERIAL PRIMARY KEY,
  name VARCHAR(200) NOT NULL,
  type VARCHAR(20),
  config JSONB,
  project_id BIGINT NOT NULL REFERENCES projects(id),
  enabled BOOLEAN DEFAULT true,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
COMMENT ON TABLE alert_channels IS '告警推送渠道表';
COMMENT ON COLUMN alert_channels.id IS '渠道主键';
COMMENT ON COLUMN alert_channels.name IS '渠道名称';
COMMENT ON COLUMN alert_channels.type IS '渠道类型（wechat/dingtalk/webhook/email）';
COMMENT ON COLUMN alert_channels.config IS '渠道配置（JSON）';
COMMENT ON COLUMN alert_channels.project_id IS '所属项目主键';
COMMENT ON COLUMN alert_channels.enabled IS '是否启用';
COMMENT ON COLUMN alert_channels.created_at IS '创建时间';
COMMENT ON COLUMN alert_channels.updated_at IS '更新时间';

-- 8. 开放API密钥表
CREATE TABLE IF NOT EXISTS api_keys (
  id BIGSERIAL PRIMARY KEY,
  name VARCHAR(200) NOT NULL,
  key_hash VARCHAR(255) UNIQUE,
  user_id BIGINT NOT NULL REFERENCES users(id),
  project_id BIGINT REFERENCES projects(id),
  permissions TEXT,
  expires_at TIMESTAMPTZ,
  last_used_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  deleted_at TIMESTAMPTZ
);
COMMENT ON TABLE api_keys IS '开放API密钥表';
COMMENT ON COLUMN api_keys.id IS '密钥主键';
COMMENT ON COLUMN api_keys.name IS '密钥名称';
COMMENT ON COLUMN api_keys.key_hash IS '密钥哈希值';
COMMENT ON COLUMN api_keys.user_id IS '所属用户主键';
COMMENT ON COLUMN api_keys.project_id IS '所属项目主键（可为空）';
COMMENT ON COLUMN api_keys.permissions IS '权限列表（逗号分隔）';
COMMENT ON COLUMN api_keys.expires_at IS '过期时间';
COMMENT ON COLUMN api_keys.last_used_at IS '最后使用时间';
COMMENT ON COLUMN api_keys.created_at IS '创建时间';
COMMENT ON COLUMN api_keys.updated_at IS '更新时间';
COMMENT ON COLUMN api_keys.deleted_at IS '软删除时间';

-- 9. 系统设置表
CREATE TABLE IF NOT EXISTS system_settings (
  id BIGSERIAL PRIMARY KEY,
  key VARCHAR(200) UNIQUE,
  value TEXT,
  description TEXT,
  category VARCHAR(100),
  updated_by BIGINT,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
COMMENT ON TABLE system_settings IS '系统设置表';
COMMENT ON COLUMN system_settings.id IS '设置主键';
COMMENT ON COLUMN system_settings.key IS '设置键名';
COMMENT ON COLUMN system_settings.value IS '设置值';
COMMENT ON COLUMN system_settings.description IS '设置描述';
COMMENT ON COLUMN system_settings.category IS '设置分类';
COMMENT ON COLUMN system_settings.updated_by IS '更新用户主键';
COMMENT ON COLUMN system_settings.updated_at IS '更新时间';

-- 10. 装机应答模板表
CREATE TABLE IF NOT EXISTS kickstart_templates (
  id BIGSERIAL PRIMARY KEY,
  name VARCHAR(200) NOT NULL,
  template_content TEXT,
  variables JSONB,
  os_type VARCHAR(50),
  partition_scheme TEXT,
  post_script TEXT,
  project_id BIGINT NOT NULL REFERENCES projects(id),
  create_by BIGINT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  deleted_at TIMESTAMPTZ
);
COMMENT ON TABLE kickstart_templates IS '装机应答模板表';
COMMENT ON COLUMN kickstart_templates.id IS '模板主键';
COMMENT ON COLUMN kickstart_templates.name IS '模板名称';
COMMENT ON COLUMN kickstart_templates.template_content IS '模板内容';
COMMENT ON COLUMN kickstart_templates.variables IS '变量定义（JSON）';
COMMENT ON COLUMN kickstart_templates.os_type IS '操作系统类型';
COMMENT ON COLUMN kickstart_templates.partition_scheme IS '分区方案';
COMMENT ON COLUMN kickstart_templates.post_script IS '装机后执行脚本';
COMMENT ON COLUMN kickstart_templates.project_id IS '所属项目主键';
COMMENT ON COLUMN kickstart_templates.create_by IS '创建用户主键';
COMMENT ON COLUMN kickstart_templates.created_at IS '创建时间';
COMMENT ON COLUMN kickstart_templates.updated_at IS '更新时间';
COMMENT ON COLUMN kickstart_templates.deleted_at IS '软删除时间';

-- ============================================================
-- 二、现有表扩展字段
-- ============================================================

-- assets 表新增列
ALTER TABLE assets ADD COLUMN IF NOT EXISTS serial_number VARCHAR(200);
ALTER TABLE assets ADD COLUMN IF NOT EXISTS manufacturer VARCHAR(200);
ALTER TABLE assets ADD COLUMN IF NOT EXISTS model VARCHAR(200);
ALTER TABLE assets ADD COLUMN IF NOT EXISTS datacenter_id BIGINT REFERENCES datacenters(id);
ALTER TABLE assets ADD COLUMN IF NOT EXISTS rack_id BIGINT REFERENCES racks(id);
ALTER TABLE assets ADD COLUMN IF NOT EXISTS rack_u_position INTEGER;
ALTER TABLE assets ADD COLUMN IF NOT EXISTS purchase_date DATE;
ALTER TABLE assets ADD COLUMN IF NOT EXISTS warranty_expiry DATE;
ALTER TABLE assets ADD COLUMN IF NOT EXISTS vendor VARCHAR(200);

COMMENT ON COLUMN assets.serial_number IS '序列号';
COMMENT ON COLUMN assets.manufacturer IS '制造商';
COMMENT ON COLUMN assets.model IS '型号';
COMMENT ON COLUMN assets.datacenter_id IS '所属机房主键';
COMMENT ON COLUMN assets.rack_id IS '所属机柜主键';
COMMENT ON COLUMN assets.rack_u_position IS 'U位位置';
COMMENT ON COLUMN assets.purchase_date IS '采购日期';
COMMENT ON COLUMN assets.warranty_expiry IS '维保到期日期';
COMMENT ON COLUMN assets.vendor IS '供应商';

-- projects 表新增列
ALTER TABLE projects ADD COLUMN IF NOT EXISTS owner VARCHAR(200);
ALTER TABLE projects ADD COLUMN IF NOT EXISTS description TEXT;
ALTER TABLE projects ADD COLUMN IF NOT EXISTS notes TEXT;
ALTER TABLE projects ADD COLUMN IF NOT EXISTS warranty_expiry DATE;

COMMENT ON COLUMN projects.owner IS '负责人';
COMMENT ON COLUMN projects.notes IS '备注';
COMMENT ON COLUMN projects.warranty_expiry IS '维保到期日期';

-- operations 表新增列
ALTER TABLE operations ADD COLUMN IF NOT EXISTS started_at TIMESTAMPTZ;
ALTER TABLE operations ADD COLUMN IF NOT EXISTS completed_at TIMESTAMPTZ;
ALTER TABLE operations ADD COLUMN IF NOT EXISTS result JSONB;
ALTER TABLE operations ADD COLUMN IF NOT EXISTS retry_count INTEGER DEFAULT 0;

COMMENT ON COLUMN operations.started_at IS '执行开始时间';
COMMENT ON COLUMN operations.completed_at IS '执行完成时间';
COMMENT ON COLUMN operations.result IS '执行结果（JSON）';
COMMENT ON COLUMN operations.retry_count IS '重试次数';

-- alerts 表新增列
ALTER TABLE alerts ADD COLUMN IF NOT EXISTS rule_id BIGINT REFERENCES alert_rules(id);
ALTER TABLE alerts ADD COLUMN IF NOT EXISTS acknowledged_at TIMESTAMPTZ;
ALTER TABLE alerts ADD COLUMN IF NOT EXISTS acknowledged_by BIGINT;
ALTER TABLE alerts ADD COLUMN IF NOT EXISTS resolved_at TIMESTAMPTZ;
ALTER TABLE alerts ADD COLUMN IF NOT EXISTS resolved_by BIGINT;
ALTER TABLE alerts ADD COLUMN IF NOT EXISTS dedup_key VARCHAR(255);

COMMENT ON COLUMN alerts.rule_id IS '关联告警规则主键';
COMMENT ON COLUMN alerts.acknowledged_at IS '告警确认时间';
COMMENT ON COLUMN alerts.acknowledged_by IS '告警确认用户主键';
COMMENT ON COLUMN alerts.resolved_at IS '告警解决时间';
COMMENT ON COLUMN alerts.resolved_by IS '告警解决用户主键';
COMMENT ON COLUMN alerts.dedup_key IS '告警去重键';

-- users 表新增列
ALTER TABLE users ADD COLUMN IF NOT EXISTS name VARCHAR(200);
ALTER TABLE users ADD COLUMN IF NOT EXISTS email VARCHAR(255);
ALTER TABLE users ADD COLUMN IF NOT EXISTS status VARCHAR(20) DEFAULT 'active';
ALTER TABLE users ADD COLUMN IF NOT EXISTS last_login_at TIMESTAMPTZ;

COMMENT ON COLUMN users.name IS '用户姓名';
COMMENT ON COLUMN users.email IS '用户邮箱';
COMMENT ON COLUMN users.status IS '用户状态';
COMMENT ON COLUMN users.last_login_at IS '最后登录时间';

-- networks 表新增列
ALTER TABLE networks ADD COLUMN IF NOT EXISTS vlan_id INTEGER;
ALTER TABLE networks ADD COLUMN IF NOT EXISTS dns_servers VARCHAR(500);
ALTER TABLE networks ADD COLUMN IF NOT EXISTS dhcp_enabled BOOLEAN DEFAULT false;

COMMENT ON COLUMN networks.vlan_id IS 'VLAN ID';
COMMENT ON COLUMN networks.dns_servers IS 'DNS服务器地址';
COMMENT ON COLUMN networks.dhcp_enabled IS 'DHCP开关';

-- images 表新增列
ALTER TABLE images ADD COLUMN IF NOT EXISTS sha256 VARCHAR(64);
ALTER TABLE images ADD COLUMN IF NOT EXISTS category VARCHAR(50);
ALTER TABLE images ADD COLUMN IF NOT EXISTS os_type VARCHAR(50);
ALTER TABLE images ADD COLUMN IF NOT EXISTS version VARCHAR(100);

COMMENT ON COLUMN images.sha256 IS 'SHA256校验值';
COMMENT ON COLUMN images.category IS '镜像分类';
COMMENT ON COLUMN images.os_type IS '操作系统类型';
COMMENT ON COLUMN images.version IS '镜像版本号';

-- firmware_inventories 表新增列
ALTER TABLE firmware_inventories ADD COLUMN IF NOT EXISTS component_type VARCHAR(50);
ALTER TABLE firmware_inventories ADD COLUMN IF NOT EXISTS upgrade_available BOOLEAN DEFAULT false;
ALTER TABLE firmware_inventories ADD COLUMN IF NOT EXISTS target_version VARCHAR(100);

COMMENT ON COLUMN firmware_inventories.component_type IS '组件类型';
COMMENT ON COLUMN firmware_inventories.upgrade_available IS '是否可升级';
COMMENT ON COLUMN firmware_inventories.target_version IS '目标升级版本';

-- ============================================================
-- 三、新增索引
-- ============================================================

-- 资产表索引
CREATE INDEX IF NOT EXISTS idx_assets_datacenter_id ON assets(datacenter_id);
CREATE INDEX IF NOT EXISTS idx_assets_rack_id ON assets(rack_id);
CREATE INDEX IF NOT EXISTS idx_assets_status ON assets(status);
CREATE INDEX IF NOT EXISTS idx_assets_project_id ON assets(project_id);

-- 传感器记录索引
CREATE INDEX IF NOT EXISTS idx_sensor_records_asset_type_time ON sensor_records(asset_id, sensor_type, collected_at);
CREATE INDEX IF NOT EXISTS idx_sensor_records_collected_at ON sensor_records(collected_at);

-- 告警规则索引
CREATE INDEX IF NOT EXISTS idx_alert_rules_project_enabled ON alert_rules(project_id, enabled);

-- API密钥索引
CREATE UNIQUE INDEX IF NOT EXISTS idx_api_keys_key_hash ON api_keys(key_hash);

-- 系统设置索引
CREATE UNIQUE INDEX IF NOT EXISTS idx_system_settings_key ON system_settings(key);

-- 标签索引
CREATE UNIQUE INDEX IF NOT EXISTS idx_tags_project_name ON tags(project_id, name);

-- 告警去重键索引
CREATE INDEX IF NOT EXISTS idx_alerts_dedup_key ON alerts(dedup_key);

-- 机房/机柜项目索引
CREATE INDEX IF NOT EXISTS idx_datacenters_project_id ON datacenters(project_id);
CREATE INDEX IF NOT EXISTS idx_racks_project_id ON racks(project_id);
CREATE INDEX IF NOT EXISTS idx_racks_datacenter_id ON racks(datacenter_id);

-- 迁移完成
