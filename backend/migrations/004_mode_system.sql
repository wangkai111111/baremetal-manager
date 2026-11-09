-- 004_mode_system.sql: 系统部署模式与运营体系迁移
-- 新增自用模式（部门、资产折旧）与运营模式（租户档案、产品、订单、账单、账户、支付、发票、优惠券）通用表（工单、审计日志）
-- 所有表使用 IF NOT EXISTS 支持幂等重放；时间统一使用 timestamptz
-- 依赖 003_target_schema.sql 中的 bmm_touch_updated_at() 触发器函数与 tenants 表

-- ============================================================
-- 一、确保触发器函数存在（003 已定义则跳过）
-- ============================================================
CREATE OR REPLACE FUNCTION bmm_touch_updated_at() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    NEW.updated_at = clock_timestamp();
    RETURN NEW;
END;
$$;

-- ============================================================
-- 二、新增表
-- ============================================================

-- 1. 系统部署模式表
CREATE TABLE IF NOT EXISTS system_modes (
  id BIGSERIAL PRIMARY KEY,
  mode VARCHAR(32) NOT NULL CHECK (mode IN ('self_use', 'external_operation')),
  locked BOOLEAN NOT NULL DEFAULT false,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
COMMENT ON TABLE system_modes IS '系统部署模式';
COMMENT ON COLUMN system_modes.id IS '主键';
COMMENT ON COLUMN system_modes.mode IS '部署模式：self_use=自用, external_operation=运营';
COMMENT ON COLUMN system_modes.locked IS '是否已锁定不可变更';
COMMENT ON COLUMN system_modes.created_at IS '创建时间';
COMMENT ON COLUMN system_modes.updated_at IS '更新时间；触发器自动维护';
CREATE TRIGGER trg_system_modes_updated BEFORE UPDATE ON system_modes FOR EACH ROW EXECUTE FUNCTION bmm_touch_updated_at();

-- 2. 部门表（自用模式）
CREATE TABLE IF NOT EXISTS departments (
  id BIGSERIAL PRIMARY KEY,
  tenant_id BIGINT NOT NULL REFERENCES tenants(id),
  name VARCHAR(128) NOT NULL,
  parent_id BIGINT REFERENCES departments(id),
  description TEXT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  deleted_at TIMESTAMPTZ
);
COMMENT ON TABLE departments IS '部门信息表（自用模式）';
COMMENT ON COLUMN departments.id IS '部门主键';
COMMENT ON COLUMN departments.tenant_id IS '所属租户';
COMMENT ON COLUMN departments.name IS '部门名称';
COMMENT ON COLUMN departments.parent_id IS '上级部门主键（自引用）';
COMMENT ON COLUMN departments.description IS '部门描述';
COMMENT ON COLUMN departments.created_at IS '创建时间';
COMMENT ON COLUMN departments.updated_at IS '更新时间；触发器自动维护';
COMMENT ON COLUMN departments.deleted_at IS '软删除时间；空表示未删除';
CREATE UNIQUE INDEX IF NOT EXISTS uq_departments_tenant_name ON departments (tenant_id, name) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_departments_tenant_id ON departments (tenant_id);
CREATE INDEX IF NOT EXISTS idx_departments_parent_id ON departments (parent_id) WHERE parent_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_departments_deleted_at ON departments (deleted_at) WHERE deleted_at IS NOT NULL;
CREATE TRIGGER trg_departments_updated BEFORE UPDATE ON departments FOR EACH ROW EXECUTE FUNCTION bmm_touch_updated_at();

-- 3. 资产折旧记录表
CREATE TABLE IF NOT EXISTS asset_depreciation (
  id BIGSERIAL PRIMARY KEY,
  tenant_id BIGINT NOT NULL REFERENCES tenants(id),
  asset_id BIGINT NOT NULL REFERENCES assets(id),
  depreciation_method VARCHAR(32),
  purchase_cost DECIMAL(12,2),
  salvage_value DECIMAL(12,2),
  useful_life_months INTEGER,
  monthly_depreciation DECIMAL(12,2),
  accumulated_depreciation DECIMAL(12,2),
  net_value DECIMAL(12,2),
  start_date DATE,
  status VARCHAR(16),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  deleted_at TIMESTAMPTZ
);
COMMENT ON TABLE asset_depreciation IS '资产折旧记录';
COMMENT ON COLUMN asset_depreciation.id IS '折旧记录主键';
COMMENT ON COLUMN asset_depreciation.tenant_id IS '所属租户';
COMMENT ON COLUMN asset_depreciation.asset_id IS '关联资产主键';
COMMENT ON COLUMN asset_depreciation.depreciation_method IS '折旧方式（如直线法、双倍余额递减法等）';
COMMENT ON COLUMN asset_depreciation.purchase_cost IS '资产原值';
COMMENT ON COLUMN asset_depreciation.salvage_value IS '预计残值';
COMMENT ON COLUMN asset_depreciation.useful_life_months IS '预计使用月数';
COMMENT ON COLUMN asset_depreciation.monthly_depreciation IS '月折旧额';
COMMENT ON COLUMN asset_depreciation.accumulated_depreciation IS '累计折旧额';
COMMENT ON COLUMN asset_depreciation.net_value IS '资产净值';
COMMENT ON COLUMN asset_depreciation.start_date IS '折旧起始日期';
COMMENT ON COLUMN asset_depreciation.status IS '折旧状态（如进行中、已完成、已报废）';
COMMENT ON COLUMN asset_depreciation.created_at IS '创建时间';
COMMENT ON COLUMN asset_depreciation.updated_at IS '更新时间；触发器自动维护';
COMMENT ON COLUMN asset_depreciation.deleted_at IS '软删除时间；空表示未删除';
CREATE INDEX IF NOT EXISTS idx_asset_depreciation_tenant_id ON asset_depreciation (tenant_id);
CREATE INDEX IF NOT EXISTS idx_asset_depreciation_asset_id ON asset_depreciation (asset_id);
CREATE INDEX IF NOT EXISTS idx_asset_depreciation_status ON asset_depreciation (status) WHERE status IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_asset_depreciation_deleted_at ON asset_depreciation (deleted_at) WHERE deleted_at IS NOT NULL;
CREATE TRIGGER trg_asset_depreciation_updated BEFORE UPDATE ON asset_depreciation FOR EACH ROW EXECUTE FUNCTION bmm_touch_updated_at();

-- 4. 租户扩展档案表（运营模式）
CREATE TABLE IF NOT EXISTS tenant_profiles (
  id BIGSERIAL PRIMARY KEY,
  tenant_id BIGINT NOT NULL UNIQUE REFERENCES tenants(id),
  contact_name VARCHAR(128),
  contact_phone VARCHAR(32),
  contact_email VARCHAR(255),
  company_name VARCHAR(256),
  company_address TEXT,
  id_number VARCHAR(64),
  verified BOOLEAN NOT NULL DEFAULT false,
  verified_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  deleted_at TIMESTAMPTZ
);
COMMENT ON TABLE tenant_profiles IS '租户扩展档案（运营模式）';
COMMENT ON COLUMN tenant_profiles.id IS '档案主键';
COMMENT ON COLUMN tenant_profiles.tenant_id IS '所属租户（一对一）';
COMMENT ON COLUMN tenant_profiles.contact_name IS '联系人姓名';
COMMENT ON COLUMN tenant_profiles.contact_phone IS '联系电话';
COMMENT ON COLUMN tenant_profiles.contact_email IS '联系邮箱';
COMMENT ON COLUMN tenant_profiles.company_name IS '企业名称';
COMMENT ON COLUMN tenant_profiles.company_address IS '企业地址';
COMMENT ON COLUMN tenant_profiles.id_number IS '统一社会信用代码/身份证号';
COMMENT ON COLUMN tenant_profiles.verified IS '是否已实名认证';
COMMENT ON COLUMN tenant_profiles.verified_at IS '认证通过时间';
COMMENT ON COLUMN tenant_profiles.created_at IS '创建时间';
COMMENT ON COLUMN tenant_profiles.updated_at IS '更新时间；触发器自动维护';
COMMENT ON COLUMN tenant_profiles.deleted_at IS '软删除时间；空表示未删除';
CREATE INDEX IF NOT EXISTS idx_tenant_profiles_tenant_id ON tenant_profiles (tenant_id);
CREATE INDEX IF NOT EXISTS idx_tenant_profiles_deleted_at ON tenant_profiles (deleted_at) WHERE deleted_at IS NOT NULL;
CREATE TRIGGER trg_tenant_profiles_updated BEFORE UPDATE ON tenant_profiles FOR EACH ROW EXECUTE FUNCTION bmm_touch_updated_at();

-- 5. 产品/型号上架表（运营模式）
CREATE TABLE IF NOT EXISTS products (
  id BIGSERIAL PRIMARY KEY,
  tenant_id BIGINT NOT NULL REFERENCES tenants(id),
  name VARCHAR(128) NOT NULL,
  description TEXT,
  spec_json JSONB,
  pricing_json JSONB,
  billing_cycle VARCHAR(16) CHECK (billing_cycle IN ('hourly', 'daily', 'monthly', 'yearly')),
  status VARCHAR(16) NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'inactive', 'sold_out')),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  deleted_at TIMESTAMPTZ
);
COMMENT ON TABLE products IS '产品/型号上架列表（运营模式）';
COMMENT ON COLUMN products.id IS '产品主键';
COMMENT ON COLUMN products.tenant_id IS '所属租户';
COMMENT ON COLUMN products.name IS '产品名称';
COMMENT ON COLUMN products.description IS '产品描述';
COMMENT ON COLUMN products.spec_json IS '规格参数（JSON）';
COMMENT ON COLUMN products.pricing_json IS '定价信息（JSON）';
COMMENT ON COLUMN products.billing_cycle IS '计费周期：hourly/daily/monthly/yearly';
COMMENT ON COLUMN products.status IS '产品状态：active/inactive/sold_out';
COMMENT ON COLUMN products.created_at IS '创建时间';
COMMENT ON COLUMN products.updated_at IS '更新时间；触发器自动维护';
COMMENT ON COLUMN products.deleted_at IS '软删除时间；空表示未删除';
CREATE INDEX IF NOT EXISTS idx_products_tenant_id ON products (tenant_id);
CREATE INDEX IF NOT EXISTS idx_products_status ON products (status) WHERE status IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_products_deleted_at ON products (deleted_at) WHERE deleted_at IS NOT NULL;
CREATE TRIGGER trg_products_updated BEFORE UPDATE ON products FOR EACH ROW EXECUTE FUNCTION bmm_touch_updated_at();

-- 6. 订单表（运营模式）
CREATE TABLE IF NOT EXISTS orders (
  id BIGSERIAL PRIMARY KEY,
  tenant_id BIGINT NOT NULL REFERENCES tenants(id),
  user_id BIGINT NOT NULL REFERENCES users(id),
  type VARCHAR(16) NOT NULL CHECK (type IN ('purchase', 'renew', 'upgrade', 'refund')),
  product_id BIGINT REFERENCES products(id),
  asset_id BIGINT REFERENCES assets(id),
  amount DECIMAL(12,2) NOT NULL DEFAULT 0,
  discount_amount DECIMAL(12,2) NOT NULL DEFAULT 0,
  final_amount DECIMAL(12,2) NOT NULL DEFAULT 0,
  status VARCHAR(16) NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'paid', 'processing', 'completed', 'refunded', 'cancelled')),
  paid_at TIMESTAMPTZ,
  completed_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  deleted_at TIMESTAMPTZ
);
COMMENT ON TABLE orders IS '订单记录（运营模式）';
COMMENT ON COLUMN orders.id IS '订单主键';
COMMENT ON COLUMN orders.tenant_id IS '所属租户';
COMMENT ON COLUMN orders.user_id IS '下单用户主键';
COMMENT ON COLUMN orders.type IS '订单类型：purchase/renew/upgrade/refund';
COMMENT ON COLUMN orders.product_id IS '关联产品主键';
COMMENT ON COLUMN orders.asset_id IS '关联资产主键';
COMMENT ON COLUMN orders.amount IS '订单原始金额';
COMMENT ON COLUMN orders.discount_amount IS '优惠金额';
COMMENT ON COLUMN orders.final_amount IS '实付金额';
COMMENT ON COLUMN orders.status IS '订单状态：pending/paid/processing/completed/refunded/cancelled';
COMMENT ON COLUMN orders.paid_at IS '支付完成时间';
COMMENT ON COLUMN orders.completed_at IS '订单完成时间';
COMMENT ON COLUMN orders.created_at IS '创建时间';
COMMENT ON COLUMN orders.updated_at IS '更新时间；触发器自动维护';
COMMENT ON COLUMN orders.deleted_at IS '软删除时间；空表示未删除';
CREATE INDEX IF NOT EXISTS idx_orders_tenant_id ON orders (tenant_id);
CREATE INDEX IF NOT EXISTS idx_orders_user_id ON orders (user_id);
CREATE INDEX IF NOT EXISTS idx_orders_product_id ON orders (product_id) WHERE product_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_orders_asset_id ON orders (asset_id) WHERE asset_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_orders_status ON orders (status) WHERE status IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_orders_deleted_at ON orders (deleted_at) WHERE deleted_at IS NOT NULL;
CREATE TRIGGER trg_orders_updated BEFORE UPDATE ON orders FOR EACH ROW EXECUTE FUNCTION bmm_touch_updated_at();

-- 7. 账单表（运营模式）
CREATE TABLE IF NOT EXISTS bills (
  id BIGSERIAL PRIMARY KEY,
  tenant_id BIGINT NOT NULL REFERENCES tenants(id),
  period_start TIMESTAMPTZ,
  period_end TIMESTAMPTZ,
  total_amount DECIMAL(12,2) NOT NULL DEFAULT 0,
  status VARCHAR(16) NOT NULL DEFAULT 'unpaid' CHECK (status IN ('unpaid', 'paid', 'overdue')),
  detail_json JSONB,
  due_date TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  deleted_at TIMESTAMPTZ
);
COMMENT ON TABLE bills IS '账单记录（运营模式）';
COMMENT ON COLUMN bills.id IS '账单主键';
COMMENT ON COLUMN bills.tenant_id IS '所属租户';
COMMENT ON COLUMN bills.period_start IS '账单周期起始时间';
COMMENT ON COLUMN bills.period_end IS '账单周期结束时间';
COMMENT ON COLUMN bills.total_amount IS '账单总金额';
COMMENT ON COLUMN bills.status IS '账单状态：unpaid/paid/overdue';
COMMENT ON COLUMN bills.detail_json IS '账单明细（JSON）';
COMMENT ON COLUMN bills.due_date IS '付款截止时间';
COMMENT ON COLUMN bills.created_at IS '创建时间';
COMMENT ON COLUMN bills.updated_at IS '更新时间；触发器自动维护';
COMMENT ON COLUMN bills.deleted_at IS '软删除时间；空表示未删除';
CREATE INDEX IF NOT EXISTS idx_bills_tenant_id ON bills (tenant_id);
CREATE INDEX IF NOT EXISTS idx_bills_status ON bills (status) WHERE status IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_bills_due_date ON bills (due_date) WHERE status = 'unpaid';
CREATE INDEX IF NOT EXISTS idx_bills_deleted_at ON bills (deleted_at) WHERE deleted_at IS NOT NULL;
CREATE TRIGGER trg_bills_updated BEFORE UPDATE ON bills FOR EACH ROW EXECUTE FUNCTION bmm_touch_updated_at();

-- 8. 租户账户表（运营模式）
CREATE TABLE IF NOT EXISTS accounts (
  id BIGSERIAL PRIMARY KEY,
  tenant_id BIGINT NOT NULL UNIQUE REFERENCES tenants(id),
  balance DECIMAL(12,2) NOT NULL DEFAULT 0,
  frozen_amount DECIMAL(12,2) NOT NULL DEFAULT 0,
  credit_limit DECIMAL(12,2) NOT NULL DEFAULT 0,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  deleted_at TIMESTAMPTZ
);
COMMENT ON TABLE accounts IS '租户账户余额（运营模式）';
COMMENT ON COLUMN accounts.id IS '账户主键';
COMMENT ON COLUMN accounts.tenant_id IS '所属租户（一对一）';
COMMENT ON COLUMN accounts.balance IS '可用余额';
COMMENT ON COLUMN accounts.frozen_amount IS '冻结金额';
COMMENT ON COLUMN accounts.credit_limit IS '信用额度';
COMMENT ON COLUMN accounts.created_at IS '创建时间';
COMMENT ON COLUMN accounts.updated_at IS '更新时间；触发器自动维护';
COMMENT ON COLUMN accounts.deleted_at IS '软删除时间；空表示未删除';
CREATE INDEX IF NOT EXISTS idx_accounts_tenant_id ON accounts (tenant_id);
CREATE INDEX IF NOT EXISTS idx_accounts_deleted_at ON accounts (deleted_at) WHERE deleted_at IS NOT NULL;
CREATE TRIGGER trg_accounts_updated BEFORE UPDATE ON accounts FOR EACH ROW EXECUTE FUNCTION bmm_touch_updated_at();

-- 9. 支付记录表（运营模式）
CREATE TABLE IF NOT EXISTS payments (
  id BIGSERIAL PRIMARY KEY,
  account_id BIGINT NOT NULL REFERENCES accounts(id),
  amount DECIMAL(12,2) NOT NULL,
  method VARCHAR(16) CHECK (method IN ('alipay', 'wechat', 'transfer')),
  status VARCHAR(16) NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'success', 'failed')),
  transaction_id VARCHAR(128),
  paid_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  deleted_at TIMESTAMPTZ
);
COMMENT ON TABLE payments IS '支付记录（运营模式）';
COMMENT ON COLUMN payments.id IS '支付记录主键';
COMMENT ON COLUMN payments.account_id IS '关联账户主键';
COMMENT ON COLUMN payments.amount IS '支付金额';
COMMENT ON COLUMN payments.method IS '支付方式：alipay/wechat/transfer';
COMMENT ON COLUMN payments.status IS '支付状态：pending/success/failed';
COMMENT ON COLUMN payments.transaction_id IS '第三方支付流水号';
COMMENT ON COLUMN payments.paid_at IS '支付完成时间';
COMMENT ON COLUMN payments.created_at IS '创建时间';
COMMENT ON COLUMN payments.updated_at IS '更新时间；触发器自动维护';
COMMENT ON COLUMN payments.deleted_at IS '软删除时间；空表示未删除';
CREATE INDEX IF NOT EXISTS idx_payments_account_id ON payments (account_id);
CREATE INDEX IF NOT EXISTS idx_payments_status ON payments (status) WHERE status IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_payments_transaction_id ON payments (transaction_id) WHERE transaction_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_payments_deleted_at ON payments (deleted_at) WHERE deleted_at IS NOT NULL;
CREATE TRIGGER trg_payments_updated BEFORE UPDATE ON payments FOR EACH ROW EXECUTE FUNCTION bmm_touch_updated_at();

-- 10. 发票表（运营模式）
CREATE TABLE IF NOT EXISTS invoices (
  id BIGSERIAL PRIMARY KEY,
  tenant_id BIGINT NOT NULL REFERENCES tenants(id),
  bill_id BIGINT REFERENCES bills(id),
  type VARCHAR(16) CHECK (type IN ('normal', 'special')),
  title VARCHAR(256),
  tax_number VARCHAR(64),
  amount DECIMAL(12,2),
  status VARCHAR(16) NOT NULL DEFAULT 'requested' CHECK (status IN ('requested', 'issued', 'cancelled')),
  issued_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  deleted_at TIMESTAMPTZ
);
COMMENT ON TABLE invoices IS '发票记录（运营模式）';
COMMENT ON COLUMN invoices.id IS '发票主键';
COMMENT ON COLUMN invoices.tenant_id IS '所属租户';
COMMENT ON COLUMN invoices.bill_id IS '关联账单主键';
COMMENT ON COLUMN invoices.type IS '发票类型：normal=普通发票, special=专用发票';
COMMENT ON COLUMN invoices.title IS '发票抬头';
COMMENT ON COLUMN invoices.tax_number IS '纳税人识别号';
COMMENT ON COLUMN invoices.amount IS '发票金额';
COMMENT ON COLUMN invoices.status IS '发票状态：requested/issued/cancelled';
COMMENT ON COLUMN invoices.issued_at IS '开票时间';
COMMENT ON COLUMN invoices.created_at IS '创建时间';
COMMENT ON COLUMN invoices.updated_at IS '更新时间；触发器自动维护';
COMMENT ON COLUMN invoices.deleted_at IS '软删除时间；空表示未删除';
CREATE INDEX IF NOT EXISTS idx_invoices_tenant_id ON invoices (tenant_id);
CREATE INDEX IF NOT EXISTS idx_invoices_bill_id ON invoices (bill_id) WHERE bill_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_invoices_status ON invoices (status) WHERE status IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_invoices_deleted_at ON invoices (deleted_at) WHERE deleted_at IS NOT NULL;
CREATE TRIGGER trg_invoices_updated BEFORE UPDATE ON invoices FOR EACH ROW EXECUTE FUNCTION bmm_touch_updated_at();

-- 11. 优惠券表（运营模式）
CREATE TABLE IF NOT EXISTS coupons (
  id BIGSERIAL PRIMARY KEY,
  tenant_id BIGINT REFERENCES tenants(id),
  code VARCHAR(64) UNIQUE,
  name VARCHAR(128),
  type VARCHAR(16) CHECK (type IN ('fixed', 'percent')),
  value DECIMAL(12,2),
  min_amount DECIMAL(12,2) NOT NULL DEFAULT 0,
  max_discount DECIMAL(12,2),
  valid_from TIMESTAMPTZ,
  valid_until TIMESTAMPTZ,
  usage_limit INTEGER,
  used_count INTEGER NOT NULL DEFAULT 0,
  status VARCHAR(16) NOT NULL DEFAULT 'active',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  deleted_at TIMESTAMPTZ
);
COMMENT ON TABLE coupons IS '优惠券（运营模式）';
COMMENT ON COLUMN coupons.id IS '优惠券主键';
COMMENT ON COLUMN coupons.tenant_id IS '所属租户（为空表示平台级优惠券）';
COMMENT ON COLUMN coupons.code IS '优惠券兑换码（唯一）';
COMMENT ON COLUMN coupons.name IS '优惠券名称';
COMMENT ON COLUMN coupons.type IS '优惠类型：fixed=固定金额, percent=百分比';
COMMENT ON COLUMN coupons.value IS '优惠面值或折扣比例';
COMMENT ON COLUMN coupons.min_amount IS '最低消费金额';
COMMENT ON COLUMN coupons.max_discount IS '最大优惠金额（百分比类型适用）';
COMMENT ON COLUMN coupons.valid_from IS '生效时间';
COMMENT ON COLUMN coupons.valid_until IS '失效时间';
COMMENT ON COLUMN coupons.usage_limit IS '使用次数上限';
COMMENT ON COLUMN coupons.used_count IS '已使用次数';
COMMENT ON COLUMN coupons.status IS '优惠券状态';
COMMENT ON COLUMN coupons.created_at IS '创建时间';
COMMENT ON COLUMN coupons.updated_at IS '更新时间；触发器自动维护';
COMMENT ON COLUMN coupons.deleted_at IS '软删除时间；空表示未删除';
CREATE INDEX IF NOT EXISTS idx_coupons_tenant_id ON coupons (tenant_id) WHERE tenant_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_coupons_code ON coupons (code) WHERE code IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_coupons_status ON coupons (status) WHERE status IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_coupons_deleted_at ON coupons (deleted_at) WHERE deleted_at IS NOT NULL;
CREATE TRIGGER trg_coupons_updated BEFORE UPDATE ON coupons FOR EACH ROW EXECUTE FUNCTION bmm_touch_updated_at();

-- 12. 工单表（两种模式通用）
CREATE TABLE IF NOT EXISTS work_orders (
  id BIGSERIAL PRIMARY KEY,
  tenant_id BIGINT NOT NULL REFERENCES tenants(id),
  user_id BIGINT NOT NULL REFERENCES users(id),
  type VARCHAR(16) NOT NULL CHECK (type IN ('fault', 'technical', 'consultation', 'other')),
  title VARCHAR(256) NOT NULL,
  description TEXT,
  status VARCHAR(16) NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'in_progress', 'resolved', 'closed')),
  priority VARCHAR(16) NOT NULL DEFAULT 'medium' CHECK (priority IN ('low', 'medium', 'high', 'urgent')),
  assigned_to BIGINT REFERENCES users(id),
  resolved_at TIMESTAMPTZ,
  closed_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  deleted_at TIMESTAMPTZ
);
COMMENT ON TABLE work_orders IS '工单（两种模式通用）';
COMMENT ON COLUMN work_orders.id IS '工单主键';
COMMENT ON COLUMN work_orders.tenant_id IS '所属租户';
COMMENT ON COLUMN work_orders.user_id IS '提交用户主键';
COMMENT ON COLUMN work_orders.type IS '工单类型：fault/technical/consultation/other';
COMMENT ON COLUMN work_orders.title IS '工单标题';
COMMENT ON COLUMN work_orders.description IS '工单描述';
COMMENT ON COLUMN work_orders.status IS '工单状态：open/in_progress/resolved/closed';
COMMENT ON COLUMN work_orders.priority IS '优先级：low/medium/high/urgent';
COMMENT ON COLUMN work_orders.assigned_to IS '指派处理用户主键';
COMMENT ON COLUMN work_orders.resolved_at IS '解决时间';
COMMENT ON COLUMN work_orders.closed_at IS '关闭时间';
COMMENT ON COLUMN work_orders.created_at IS '创建时间';
COMMENT ON COLUMN work_orders.updated_at IS '更新时间；触发器自动维护';
COMMENT ON COLUMN work_orders.deleted_at IS '软删除时间；空表示未删除';
CREATE INDEX IF NOT EXISTS idx_work_orders_tenant_id ON work_orders (tenant_id);
CREATE INDEX IF NOT EXISTS idx_work_orders_user_id ON work_orders (user_id);
CREATE INDEX IF NOT EXISTS idx_work_orders_assigned_to ON work_orders (assigned_to) WHERE assigned_to IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_work_orders_status ON work_orders (status) WHERE status IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_work_orders_priority ON work_orders (priority) WHERE priority IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_work_orders_deleted_at ON work_orders (deleted_at) WHERE deleted_at IS NOT NULL;
CREATE TRIGGER trg_work_orders_updated BEFORE UPDATE ON work_orders FOR EACH ROW EXECUTE FUNCTION bmm_touch_updated_at();

-- 13. 增强审计日志表
CREATE TABLE IF NOT EXISTS operation_audit_logs (
  id BIGSERIAL PRIMARY KEY,
  tenant_id BIGINT REFERENCES tenants(id),
  project_id BIGINT REFERENCES projects(id),
  user_id BIGINT REFERENCES users(id),
  action VARCHAR(128) NOT NULL,
  target_type VARCHAR(64),
  target_id BIGINT,
  detail JSONB,
  client_ip VARCHAR(64),
  user_agent TEXT,
  result VARCHAR(16) CHECK (result IN ('success', 'failure', 'partial')),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
COMMENT ON TABLE operation_audit_logs IS '增强审计日志（记录所有关键操作）';
COMMENT ON COLUMN operation_audit_logs.id IS '审计记录主键';
COMMENT ON COLUMN operation_audit_logs.tenant_id IS '所属租户';
COMMENT ON COLUMN operation_audit_logs.project_id IS '所属项目';
COMMENT ON COLUMN operation_audit_logs.user_id IS '操作用户主键';
COMMENT ON COLUMN operation_audit_logs.action IS '操作动作';
COMMENT ON COLUMN operation_audit_logs.target_type IS '操作目标类型';
COMMENT ON COLUMN operation_audit_logs.target_id IS '操作目标主键';
COMMENT ON COLUMN operation_audit_logs.detail IS '操作详情（JSON）';
COMMENT ON COLUMN operation_audit_logs.client_ip IS '客户端IP地址';
COMMENT ON COLUMN operation_audit_logs.user_agent IS '客户端User-Agent';
COMMENT ON COLUMN operation_audit_logs.result IS '操作结果：success/failure/partial';
COMMENT ON COLUMN operation_audit_logs.created_at IS '创建时间';
CREATE INDEX IF NOT EXISTS idx_operation_audit_logs_tenant_id ON operation_audit_logs (tenant_id) WHERE tenant_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_operation_audit_logs_project_id ON operation_audit_logs (project_id) WHERE project_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_operation_audit_logs_user_id ON operation_audit_logs (user_id) WHERE user_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_operation_audit_logs_action ON operation_audit_logs (action) WHERE action IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_operation_audit_logs_target ON operation_audit_logs (target_type, target_id) WHERE target_type IS NOT NULL AND target_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_operation_audit_logs_created_at ON operation_audit_logs (created_at DESC);
CREATE INDEX IF NOT EXISTS idx_operation_audit_logs_result ON operation_audit_logs (result) WHERE result IS NOT NULL;

-- ============================================================
-- 三、现有表扩展字段
-- ============================================================

-- assets 表新增 department_id 列
ALTER TABLE assets ADD COLUMN IF NOT EXISTS department_id BIGINT REFERENCES departments(id) ON DELETE SET NULL;
COMMENT ON COLUMN assets.department_id IS '所属部门主键';
CREATE INDEX IF NOT EXISTS idx_assets_department_id ON assets (department_id) WHERE department_id IS NOT NULL;

-- 迁移完成
