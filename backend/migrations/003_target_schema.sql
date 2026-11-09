-- 裸机 BMC 运维平台：第 3 阶段目标数据库 DDL
-- 目标版本 PostgreSQL 15+；不依赖额外扩展。
-- 仅在独立空数据库的 public schema 执行；不是现有数据库增量迁移。
-- 不要接入当前启动自动迁移：现有 Go 模型尚未同步租户、字段与状态定义。
-- 后续阶段须关闭 AutoMigrate 并使用版本化迁移；本文件不含默认用户或默认密码。
-- 所有时间使用 timestamptz，业务展示转换到用户时区。
-- 复合外键阻止跨租户/项目引用；并不替代服务层鉴权，不启用虚假的无策略 RLS。
-- 软删除不会触发外键级联；写入时必须在事务内校验父资源未删除并按顺序删除子资源。
-- 资产跨项目迁移不得批量改写历史归属；应建立新资产身份，旧记录留在原项目。
-- 同用户租户成员状态、U 位重叠、跨表网络范围、任务状态转换均由服务层锁定校验。
-- 不使用含 now() 的部分索引；租约过期通过原子条件更新接管。
-- 认证凭据只能密文/哈希保存；任何 JSON、日志、模板快照都必须脱敏。
-- 应用角色不得为数据库 owner；audits 禁止 UPDATE/DELETE/TRUNCATE 的授权在部署阶段配置。
BEGIN;
SET LOCAL search_path = public, pg_catalog;
SET LOCAL lock_timeout = '10s';
CREATE FUNCTION bmm_touch_updated_at() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    NEW.updated_at = clock_timestamp();
    RETURN NEW;
END;
$$;
COMMENT ON FUNCTION bmm_touch_updated_at() IS '统一维护行更新时间；不替代调用方记录 update_by';

CREATE TABLE users (
    id bigserial PRIMARY KEY,
    username text NOT NULL,
    password_hash text NOT NULL,
    name text NOT NULL DEFAULT '',
    email text,
    status text NOT NULL DEFAULT 'active' CHECK (status IN ('active','disabled','locked')),
    is_platform_admin boolean NOT NULL DEFAULT false,
    last_login_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz,
    create_by bigint REFERENCES users(id),
    update_by bigint REFERENCES users(id)
);
COMMENT ON TABLE users IS '全局用户；禁止写入默认密码';
COMMENT ON COLUMN users.id IS '行主键';
COMMENT ON COLUMN users.username IS '登录名';
COMMENT ON COLUMN users.password_hash IS '强密码哈希；由服务端生成，不存明文';
COMMENT ON COLUMN users.name IS '显示姓名';
COMMENT ON COLUMN users.email IS '联系邮箱';
COMMENT ON COLUMN users.status IS '账户状态';
COMMENT ON COLUMN users.is_platform_admin IS '平台管理员标记；需独立鉴权';
COMMENT ON COLUMN users.last_login_at IS '最近登录时间';
COMMENT ON COLUMN users.created_at IS '创建时间';
COMMENT ON COLUMN users.updated_at IS '更新时间；触发器自动维护';
COMMENT ON COLUMN users.deleted_at IS '软删除时间；空表示未删除';
COMMENT ON COLUMN users.create_by IS '创建用户；系统任务为空';
COMMENT ON COLUMN users.update_by IS '最后修改用户；系统任务为空';
CREATE UNIQUE INDEX uq_users_1 ON users (lower(username)) WHERE deleted_at IS NULL;
CREATE INDEX ix_users_deleted ON users (deleted_at) WHERE deleted_at IS NOT NULL;
CREATE INDEX ix_users_create_by ON users (create_by) WHERE create_by IS NOT NULL;
CREATE INDEX ix_users_update_by ON users (update_by) WHERE update_by IS NOT NULL;
CREATE TRIGGER trg_users_updated BEFORE UPDATE ON users FOR EACH ROW EXECUTE FUNCTION bmm_touch_updated_at();

CREATE TABLE permissions (
    id bigserial PRIMARY KEY,
    code text NOT NULL,
    name text NOT NULL,
    resource text NOT NULL,
    action text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz,
    create_by bigint REFERENCES users(id),
    update_by bigint REFERENCES users(id)
);
COMMENT ON TABLE permissions IS '全局权限字典；角色与密钥共用明确权限码';
COMMENT ON COLUMN permissions.id IS '行主键';
COMMENT ON COLUMN permissions.code IS '权限码';
COMMENT ON COLUMN permissions.name IS '权限名称';
COMMENT ON COLUMN permissions.resource IS '资源类型';
COMMENT ON COLUMN permissions.action IS '操作类型';
COMMENT ON COLUMN permissions.created_at IS '创建时间';
COMMENT ON COLUMN permissions.updated_at IS '更新时间；触发器自动维护';
COMMENT ON COLUMN permissions.deleted_at IS '软删除时间；空表示未删除';
COMMENT ON COLUMN permissions.create_by IS '创建用户；系统任务为空';
COMMENT ON COLUMN permissions.update_by IS '最后修改用户；系统任务为空';
CREATE UNIQUE INDEX uq_permissions_1 ON permissions (code) WHERE deleted_at IS NULL;
CREATE INDEX ix_permissions_deleted ON permissions (deleted_at) WHERE deleted_at IS NOT NULL;
CREATE INDEX ix_permissions_create_by ON permissions (create_by) WHERE create_by IS NOT NULL;
CREATE INDEX ix_permissions_update_by ON permissions (update_by) WHERE update_by IS NOT NULL;
CREATE TRIGGER trg_permissions_updated BEFORE UPDATE ON permissions FOR EACH ROW EXECUTE FUNCTION bmm_touch_updated_at();

CREATE TABLE system_settings (
    id bigserial PRIMARY KEY,
    key text NOT NULL,
    value jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz,
    create_by bigint REFERENCES users(id),
    update_by bigint REFERENCES users(id)
);
COMMENT ON TABLE system_settings IS '全局系统设置；敏感值必须加密';
COMMENT ON COLUMN system_settings.id IS '行主键';
COMMENT ON COLUMN system_settings.key IS '设置键';
COMMENT ON COLUMN system_settings.value IS '设置内容；不得存放明文凭据';
COMMENT ON COLUMN system_settings.created_at IS '创建时间';
COMMENT ON COLUMN system_settings.updated_at IS '更新时间；触发器自动维护';
COMMENT ON COLUMN system_settings.deleted_at IS '软删除时间；空表示未删除';
COMMENT ON COLUMN system_settings.create_by IS '创建用户；系统任务为空';
COMMENT ON COLUMN system_settings.update_by IS '最后修改用户；系统任务为空';
CREATE UNIQUE INDEX uq_system_settings_1 ON system_settings (key) WHERE deleted_at IS NULL;
CREATE INDEX ix_system_settings_deleted ON system_settings (deleted_at) WHERE deleted_at IS NOT NULL;
CREATE INDEX ix_system_settings_create_by ON system_settings (create_by) WHERE create_by IS NOT NULL;
CREATE INDEX ix_system_settings_update_by ON system_settings (update_by) WHERE update_by IS NOT NULL;
CREATE TRIGGER trg_system_settings_updated BEFORE UPDATE ON system_settings FOR EACH ROW EXECUTE FUNCTION bmm_touch_updated_at();

CREATE TABLE tenants (
    id bigserial PRIMARY KEY,
    name text NOT NULL,
    code text NOT NULL,
    status text NOT NULL DEFAULT 'active' CHECK (status IN ('active','disabled')),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz,
    create_by bigint REFERENCES users(id),
    update_by bigint REFERENCES users(id)
);
COMMENT ON TABLE tenants IS '租户组织';
COMMENT ON COLUMN tenants.id IS '行主键';
COMMENT ON COLUMN tenants.name IS '租户名称';
COMMENT ON COLUMN tenants.code IS '租户编码';
COMMENT ON COLUMN tenants.status IS '租户状态';
COMMENT ON COLUMN tenants.created_at IS '创建时间';
COMMENT ON COLUMN tenants.updated_at IS '更新时间；触发器自动维护';
COMMENT ON COLUMN tenants.deleted_at IS '软删除时间；空表示未删除';
COMMENT ON COLUMN tenants.create_by IS '创建用户；系统任务为空';
COMMENT ON COLUMN tenants.update_by IS '最后修改用户；系统任务为空';
CREATE UNIQUE INDEX uq_tenants_1 ON tenants (code) WHERE deleted_at IS NULL;
CREATE INDEX ix_tenants_deleted ON tenants (deleted_at) WHERE deleted_at IS NOT NULL;
CREATE INDEX ix_tenants_create_by ON tenants (create_by) WHERE create_by IS NOT NULL;
CREATE INDEX ix_tenants_update_by ON tenants (update_by) WHERE update_by IS NOT NULL;
CREATE TRIGGER trg_tenants_updated BEFORE UPDATE ON tenants FOR EACH ROW EXECUTE FUNCTION bmm_touch_updated_at();

CREATE TABLE tenant_memberships (
    id bigserial PRIMARY KEY,
    tenant_id bigint NOT NULL REFERENCES tenants(id),
    user_id bigint NOT NULL REFERENCES users(id),
    status text NOT NULL DEFAULT 'active' CHECK (status IN ('active','disabled')),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz,
    create_by bigint REFERENCES users(id),
    update_by bigint REFERENCES users(id),
    UNIQUE (tenant_id, id),
    UNIQUE (tenant_id, user_id)
);
COMMENT ON TABLE tenant_memberships IS '租户成员关系';
COMMENT ON COLUMN tenant_memberships.id IS '行主键';
COMMENT ON COLUMN tenant_memberships.tenant_id IS '所属租户';
COMMENT ON COLUMN tenant_memberships.user_id IS '用户';
COMMENT ON COLUMN tenant_memberships.status IS '成员状态';
COMMENT ON COLUMN tenant_memberships.created_at IS '创建时间';
COMMENT ON COLUMN tenant_memberships.updated_at IS '更新时间；触发器自动维护';
COMMENT ON COLUMN tenant_memberships.deleted_at IS '软删除时间；空表示未删除';
COMMENT ON COLUMN tenant_memberships.create_by IS '创建用户；系统任务为空';
COMMENT ON COLUMN tenant_memberships.update_by IS '最后修改用户；系统任务为空';
CREATE UNIQUE INDEX uq_tenant_memberships_1 ON tenant_memberships (tenant_id, user_id) WHERE deleted_at IS NULL;
CREATE INDEX ix_tenant_memberships_scope ON tenant_memberships (tenant_id, created_at DESC) WHERE deleted_at IS NULL;
CREATE INDEX ix_tenant_memberships_deleted ON tenant_memberships (deleted_at) WHERE deleted_at IS NOT NULL;
CREATE INDEX ix_tenant_memberships_user_id ON tenant_memberships (tenant_id, user_id);
CREATE INDEX ix_tenant_memberships_create_by ON tenant_memberships (create_by) WHERE create_by IS NOT NULL;
CREATE INDEX ix_tenant_memberships_update_by ON tenant_memberships (update_by) WHERE update_by IS NOT NULL;
CREATE TRIGGER trg_tenant_memberships_updated BEFORE UPDATE ON tenant_memberships FOR EACH ROW EXECUTE FUNCTION bmm_touch_updated_at();

CREATE TABLE projects (
    id bigserial PRIMARY KEY,
    tenant_id bigint NOT NULL REFERENCES tenants(id),
    name text NOT NULL,
    code text NOT NULL,
    description text NOT NULL DEFAULT '',
    owner_id bigint REFERENCES users(id),
    status text NOT NULL DEFAULT 'active' CHECK (status IN ('active','archived')),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz,
    create_by bigint REFERENCES users(id),
    update_by bigint REFERENCES users(id),
    UNIQUE (tenant_id, id)
);
COMMENT ON TABLE projects IS '租户内资源隔离项目';
COMMENT ON COLUMN projects.id IS '行主键';
COMMENT ON COLUMN projects.tenant_id IS '所属租户';
COMMENT ON COLUMN projects.name IS '项目名称';
COMMENT ON COLUMN projects.code IS '项目编码';
COMMENT ON COLUMN projects.description IS '项目说明';
COMMENT ON COLUMN projects.owner_id IS '负责人';
COMMENT ON COLUMN projects.status IS '项目状态';
COMMENT ON COLUMN projects.created_at IS '创建时间';
COMMENT ON COLUMN projects.updated_at IS '更新时间；触发器自动维护';
COMMENT ON COLUMN projects.deleted_at IS '软删除时间；空表示未删除';
COMMENT ON COLUMN projects.create_by IS '创建用户；系统任务为空';
COMMENT ON COLUMN projects.update_by IS '最后修改用户；系统任务为空';
CREATE UNIQUE INDEX uq_projects_1 ON projects (tenant_id, code) WHERE deleted_at IS NULL;
CREATE INDEX ix_projects_scope ON projects (tenant_id, created_at DESC) WHERE deleted_at IS NULL;
CREATE INDEX ix_projects_deleted ON projects (deleted_at) WHERE deleted_at IS NOT NULL;
CREATE INDEX ix_projects_owner_id ON projects (tenant_id, owner_id);
CREATE INDEX ix_projects_create_by ON projects (create_by) WHERE create_by IS NOT NULL;
CREATE INDEX ix_projects_update_by ON projects (update_by) WHERE update_by IS NOT NULL;
CREATE TRIGGER trg_projects_updated BEFORE UPDATE ON projects FOR EACH ROW EXECUTE FUNCTION bmm_touch_updated_at();

CREATE TABLE roles (
    id bigserial PRIMARY KEY,
    tenant_id bigint NOT NULL REFERENCES tenants(id),
    name text NOT NULL,
    code text NOT NULL,
    description text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz,
    create_by bigint REFERENCES users(id),
    update_by bigint REFERENCES users(id),
    UNIQUE (tenant_id, id)
);
COMMENT ON TABLE roles IS '租户自定义角色';
COMMENT ON COLUMN roles.id IS '行主键';
COMMENT ON COLUMN roles.tenant_id IS '所属租户';
COMMENT ON COLUMN roles.name IS '角色名称';
COMMENT ON COLUMN roles.code IS '角色编码';
COMMENT ON COLUMN roles.description IS '说明';
COMMENT ON COLUMN roles.created_at IS '创建时间';
COMMENT ON COLUMN roles.updated_at IS '更新时间；触发器自动维护';
COMMENT ON COLUMN roles.deleted_at IS '软删除时间；空表示未删除';
COMMENT ON COLUMN roles.create_by IS '创建用户；系统任务为空';
COMMENT ON COLUMN roles.update_by IS '最后修改用户；系统任务为空';
CREATE UNIQUE INDEX uq_roles_1 ON roles (tenant_id, code) WHERE deleted_at IS NULL;
CREATE INDEX ix_roles_scope ON roles (tenant_id, created_at DESC) WHERE deleted_at IS NULL;
CREATE INDEX ix_roles_deleted ON roles (deleted_at) WHERE deleted_at IS NOT NULL;
CREATE INDEX ix_roles_create_by ON roles (create_by) WHERE create_by IS NOT NULL;
CREATE INDEX ix_roles_update_by ON roles (update_by) WHERE update_by IS NOT NULL;
CREATE TRIGGER trg_roles_updated BEFORE UPDATE ON roles FOR EACH ROW EXECUTE FUNCTION bmm_touch_updated_at();

CREATE TABLE role_permissions (
    id bigserial PRIMARY KEY,
    tenant_id bigint NOT NULL REFERENCES tenants(id),
    role_id bigint NOT NULL,
    permission_id bigint NOT NULL REFERENCES permissions(id),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz,
    create_by bigint REFERENCES users(id),
    update_by bigint REFERENCES users(id),
    UNIQUE (tenant_id, id),
    FOREIGN KEY (tenant_id, role_id) REFERENCES roles(tenant_id, id)
);
COMMENT ON TABLE role_permissions IS '角色权限关联';
COMMENT ON COLUMN role_permissions.id IS '行主键';
COMMENT ON COLUMN role_permissions.tenant_id IS '所属租户';
COMMENT ON COLUMN role_permissions.role_id IS '角色';
COMMENT ON COLUMN role_permissions.permission_id IS '权限';
COMMENT ON COLUMN role_permissions.created_at IS '创建时间';
COMMENT ON COLUMN role_permissions.updated_at IS '更新时间；触发器自动维护';
COMMENT ON COLUMN role_permissions.deleted_at IS '软删除时间；空表示未删除';
COMMENT ON COLUMN role_permissions.create_by IS '创建用户；系统任务为空';
COMMENT ON COLUMN role_permissions.update_by IS '最后修改用户；系统任务为空';
CREATE UNIQUE INDEX uq_role_permissions_1 ON role_permissions (tenant_id, role_id, permission_id) WHERE deleted_at IS NULL;
CREATE INDEX ix_role_permissions_scope ON role_permissions (tenant_id, created_at DESC) WHERE deleted_at IS NULL;
CREATE INDEX ix_role_permissions_deleted ON role_permissions (deleted_at) WHERE deleted_at IS NOT NULL;
CREATE INDEX ix_role_permissions_role_id ON role_permissions (tenant_id, role_id);
CREATE INDEX ix_role_permissions_permission_id ON role_permissions (tenant_id, permission_id);
CREATE INDEX ix_role_permissions_create_by ON role_permissions (create_by) WHERE create_by IS NOT NULL;
CREATE INDEX ix_role_permissions_update_by ON role_permissions (update_by) WHERE update_by IS NOT NULL;
CREATE TRIGGER trg_role_permissions_updated BEFORE UPDATE ON role_permissions FOR EACH ROW EXECUTE FUNCTION bmm_touch_updated_at();

CREATE TABLE memberships (
    id bigserial PRIMARY KEY,
    tenant_id bigint NOT NULL REFERENCES tenants(id),
    project_id bigint NOT NULL,
    user_id bigint NOT NULL REFERENCES users(id),
    role_id bigint NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz,
    create_by bigint REFERENCES users(id),
    update_by bigint REFERENCES users(id),
    UNIQUE (tenant_id, project_id, id),
    FOREIGN KEY (tenant_id, project_id) REFERENCES projects(tenant_id, id),
    FOREIGN KEY (tenant_id, role_id) REFERENCES roles(tenant_id, id),
    FOREIGN KEY (tenant_id, user_id) REFERENCES tenant_memberships(tenant_id, user_id)
);
COMMENT ON TABLE memberships IS '项目成员角色；用户须为同租户有效成员';
COMMENT ON COLUMN memberships.id IS '行主键';
COMMENT ON COLUMN memberships.tenant_id IS '所属租户';
COMMENT ON COLUMN memberships.project_id IS '所属项目';
COMMENT ON COLUMN memberships.user_id IS '用户';
COMMENT ON COLUMN memberships.role_id IS '角色';
COMMENT ON COLUMN memberships.created_at IS '创建时间';
COMMENT ON COLUMN memberships.updated_at IS '更新时间；触发器自动维护';
COMMENT ON COLUMN memberships.deleted_at IS '软删除时间；空表示未删除';
COMMENT ON COLUMN memberships.create_by IS '创建用户；系统任务为空';
COMMENT ON COLUMN memberships.update_by IS '最后修改用户；系统任务为空';
CREATE UNIQUE INDEX uq_memberships_1 ON memberships (tenant_id, project_id, user_id, role_id) WHERE deleted_at IS NULL;
CREATE INDEX ix_memberships_scope ON memberships (tenant_id, project_id, created_at DESC) WHERE deleted_at IS NULL;
CREATE INDEX ix_memberships_deleted ON memberships (deleted_at) WHERE deleted_at IS NOT NULL;
CREATE INDEX ix_memberships_user_id ON memberships (tenant_id, project_id, user_id);
CREATE INDEX ix_memberships_role_id ON memberships (tenant_id, project_id, role_id);
CREATE INDEX ix_memberships_create_by ON memberships (create_by) WHERE create_by IS NOT NULL;
CREATE INDEX ix_memberships_update_by ON memberships (update_by) WHERE update_by IS NOT NULL;
CREATE TRIGGER trg_memberships_updated BEFORE UPDATE ON memberships FOR EACH ROW EXECUTE FUNCTION bmm_touch_updated_at();

CREATE TABLE api_keys (
    id bigserial PRIMARY KEY,
    tenant_id bigint NOT NULL REFERENCES tenants(id),
    project_id bigint NOT NULL,
    user_id bigint NOT NULL REFERENCES users(id),
    name text NOT NULL,
    key_prefix text NOT NULL,
    secret_hash text NOT NULL,
    expires_at timestamptz NOT NULL,
    revoked_at timestamptz,
    last_used_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz,
    create_by bigint REFERENCES users(id),
    update_by bigint REFERENCES users(id),
    UNIQUE (tenant_id, project_id, id),
    FOREIGN KEY (tenant_id, project_id) REFERENCES projects(tenant_id, id)
);
COMMENT ON TABLE api_keys IS '项目 API 密钥；只保存哈希';
COMMENT ON COLUMN api_keys.id IS '行主键';
COMMENT ON COLUMN api_keys.tenant_id IS '所属租户';
COMMENT ON COLUMN api_keys.project_id IS '所属项目';
COMMENT ON COLUMN api_keys.user_id IS '所属用户';
COMMENT ON COLUMN api_keys.name IS '密钥名称';
COMMENT ON COLUMN api_keys.key_prefix IS '展示前缀；不得用于认证';
COMMENT ON COLUMN api_keys.secret_hash IS '完整高熵密钥的哈希';
COMMENT ON COLUMN api_keys.expires_at IS '失效时间';
COMMENT ON COLUMN api_keys.revoked_at IS '吊销时间';
COMMENT ON COLUMN api_keys.last_used_at IS '最近使用时间';
COMMENT ON COLUMN api_keys.created_at IS '创建时间';
COMMENT ON COLUMN api_keys.updated_at IS '更新时间；触发器自动维护';
COMMENT ON COLUMN api_keys.deleted_at IS '软删除时间；空表示未删除';
COMMENT ON COLUMN api_keys.create_by IS '创建用户；系统任务为空';
COMMENT ON COLUMN api_keys.update_by IS '最后修改用户；系统任务为空';
CREATE UNIQUE INDEX uq_api_keys_1 ON api_keys (tenant_id, project_id, secret_hash) WHERE deleted_at IS NULL;
CREATE INDEX ix_api_keys_scope ON api_keys (tenant_id, project_id, created_at DESC) WHERE deleted_at IS NULL;
CREATE INDEX ix_api_keys_deleted ON api_keys (deleted_at) WHERE deleted_at IS NOT NULL;
CREATE INDEX ix_api_keys_user_id ON api_keys (tenant_id, project_id, user_id);
CREATE INDEX ix_api_keys_create_by ON api_keys (create_by) WHERE create_by IS NOT NULL;
CREATE INDEX ix_api_keys_update_by ON api_keys (update_by) WHERE update_by IS NOT NULL;
CREATE TRIGGER trg_api_keys_updated BEFORE UPDATE ON api_keys FOR EACH ROW EXECUTE FUNCTION bmm_touch_updated_at();

CREATE TABLE api_key_permissions (
    id bigserial PRIMARY KEY,
    tenant_id bigint NOT NULL REFERENCES tenants(id),
    project_id bigint NOT NULL,
    api_key_id bigint NOT NULL,
    permission_id bigint NOT NULL REFERENCES permissions(id),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz,
    create_by bigint REFERENCES users(id),
    update_by bigint REFERENCES users(id),
    UNIQUE (tenant_id, project_id, id),
    FOREIGN KEY (tenant_id, project_id) REFERENCES projects(tenant_id, id),
    FOREIGN KEY (tenant_id, project_id, api_key_id) REFERENCES api_keys(tenant_id, project_id, id)
);
COMMENT ON TABLE api_key_permissions IS 'API 密钥授权；调用时仍须与用户当前权限求交集';
COMMENT ON COLUMN api_key_permissions.id IS '行主键';
COMMENT ON COLUMN api_key_permissions.tenant_id IS '所属租户';
COMMENT ON COLUMN api_key_permissions.project_id IS '所属项目';
COMMENT ON COLUMN api_key_permissions.api_key_id IS '密钥';
COMMENT ON COLUMN api_key_permissions.permission_id IS '权限';
COMMENT ON COLUMN api_key_permissions.created_at IS '创建时间';
COMMENT ON COLUMN api_key_permissions.updated_at IS '更新时间；触发器自动维护';
COMMENT ON COLUMN api_key_permissions.deleted_at IS '软删除时间；空表示未删除';
COMMENT ON COLUMN api_key_permissions.create_by IS '创建用户；系统任务为空';
COMMENT ON COLUMN api_key_permissions.update_by IS '最后修改用户；系统任务为空';
CREATE UNIQUE INDEX uq_api_key_permissions_1 ON api_key_permissions (tenant_id, project_id, api_key_id, permission_id) WHERE deleted_at IS NULL;
CREATE INDEX ix_api_key_permissions_scope ON api_key_permissions (tenant_id, project_id, created_at DESC) WHERE deleted_at IS NULL;
CREATE INDEX ix_api_key_permissions_deleted ON api_key_permissions (deleted_at) WHERE deleted_at IS NOT NULL;
CREATE INDEX ix_api_key_permissions_api_key_id ON api_key_permissions (tenant_id, project_id, api_key_id);
CREATE INDEX ix_api_key_permissions_permission_id ON api_key_permissions (tenant_id, project_id, permission_id);
CREATE INDEX ix_api_key_permissions_create_by ON api_key_permissions (create_by) WHERE create_by IS NOT NULL;
CREATE INDEX ix_api_key_permissions_update_by ON api_key_permissions (update_by) WHERE update_by IS NOT NULL;
CREATE TRIGGER trg_api_key_permissions_updated BEFORE UPDATE ON api_key_permissions FOR EACH ROW EXECUTE FUNCTION bmm_touch_updated_at();

CREATE TABLE datacenters (
    id bigserial PRIMARY KEY,
    tenant_id bigint NOT NULL REFERENCES tenants(id),
    project_id bigint NOT NULL,
    name text NOT NULL,
    location text NOT NULL DEFAULT '',
    contact text,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz,
    create_by bigint REFERENCES users(id),
    update_by bigint REFERENCES users(id),
    UNIQUE (tenant_id, project_id, id),
    FOREIGN KEY (tenant_id, project_id) REFERENCES projects(tenant_id, id)
);
COMMENT ON TABLE datacenters IS '机房';
COMMENT ON COLUMN datacenters.id IS '行主键';
COMMENT ON COLUMN datacenters.tenant_id IS '所属租户';
COMMENT ON COLUMN datacenters.project_id IS '所属项目';
COMMENT ON COLUMN datacenters.name IS '机房名称';
COMMENT ON COLUMN datacenters.location IS '物理地址';
COMMENT ON COLUMN datacenters.contact IS '联系信息';
COMMENT ON COLUMN datacenters.created_at IS '创建时间';
COMMENT ON COLUMN datacenters.updated_at IS '更新时间；触发器自动维护';
COMMENT ON COLUMN datacenters.deleted_at IS '软删除时间；空表示未删除';
COMMENT ON COLUMN datacenters.create_by IS '创建用户；系统任务为空';
COMMENT ON COLUMN datacenters.update_by IS '最后修改用户；系统任务为空';
CREATE UNIQUE INDEX uq_datacenters_1 ON datacenters (tenant_id, project_id, name) WHERE deleted_at IS NULL;
CREATE INDEX ix_datacenters_scope ON datacenters (tenant_id, project_id, created_at DESC) WHERE deleted_at IS NULL;
CREATE INDEX ix_datacenters_deleted ON datacenters (deleted_at) WHERE deleted_at IS NOT NULL;
CREATE INDEX ix_datacenters_create_by ON datacenters (create_by) WHERE create_by IS NOT NULL;
CREATE INDEX ix_datacenters_update_by ON datacenters (update_by) WHERE update_by IS NOT NULL;
CREATE TRIGGER trg_datacenters_updated BEFORE UPDATE ON datacenters FOR EACH ROW EXECUTE FUNCTION bmm_touch_updated_at();

CREATE TABLE racks (
    id bigserial PRIMARY KEY,
    tenant_id bigint NOT NULL REFERENCES tenants(id),
    project_id bigint NOT NULL,
    datacenter_id bigint NOT NULL,
    name text NOT NULL,
    height_u integer NOT NULL DEFAULT 42 CHECK (height_u > 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz,
    create_by bigint REFERENCES users(id),
    update_by bigint REFERENCES users(id),
    UNIQUE (tenant_id, project_id, id),
    FOREIGN KEY (tenant_id, project_id) REFERENCES projects(tenant_id, id),
    FOREIGN KEY (tenant_id, project_id, datacenter_id) REFERENCES datacenters(tenant_id, project_id, id),
    UNIQUE (tenant_id, project_id, id, datacenter_id)
);
COMMENT ON TABLE racks IS '机柜；U 位重叠由服务层加锁校验';
COMMENT ON COLUMN racks.id IS '行主键';
COMMENT ON COLUMN racks.tenant_id IS '所属租户';
COMMENT ON COLUMN racks.project_id IS '所属项目';
COMMENT ON COLUMN racks.datacenter_id IS '所属机房';
COMMENT ON COLUMN racks.name IS '机柜名称';
COMMENT ON COLUMN racks.height_u IS '机柜总 U 数';
COMMENT ON COLUMN racks.created_at IS '创建时间';
COMMENT ON COLUMN racks.updated_at IS '更新时间；触发器自动维护';
COMMENT ON COLUMN racks.deleted_at IS '软删除时间；空表示未删除';
COMMENT ON COLUMN racks.create_by IS '创建用户；系统任务为空';
COMMENT ON COLUMN racks.update_by IS '最后修改用户；系统任务为空';
CREATE UNIQUE INDEX uq_racks_1 ON racks (tenant_id, project_id, datacenter_id, name) WHERE deleted_at IS NULL;
CREATE INDEX ix_racks_scope ON racks (tenant_id, project_id, created_at DESC) WHERE deleted_at IS NULL;
CREATE INDEX ix_racks_deleted ON racks (deleted_at) WHERE deleted_at IS NOT NULL;
CREATE INDEX ix_racks_datacenter_id ON racks (tenant_id, project_id, datacenter_id);
CREATE INDEX ix_racks_create_by ON racks (create_by) WHERE create_by IS NOT NULL;
CREATE INDEX ix_racks_update_by ON racks (update_by) WHERE update_by IS NOT NULL;
CREATE TRIGGER trg_racks_updated BEFORE UPDATE ON racks FOR EACH ROW EXECUTE FUNCTION bmm_touch_updated_at();

CREATE TABLE tags (
    id bigserial PRIMARY KEY,
    tenant_id bigint NOT NULL REFERENCES tenants(id),
    project_id bigint NOT NULL,
    name text NOT NULL,
    color text,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz,
    create_by bigint REFERENCES users(id),
    update_by bigint REFERENCES users(id),
    UNIQUE (tenant_id, project_id, id),
    FOREIGN KEY (tenant_id, project_id) REFERENCES projects(tenant_id, id)
);
COMMENT ON TABLE tags IS '项目资源标签';
COMMENT ON COLUMN tags.id IS '行主键';
COMMENT ON COLUMN tags.tenant_id IS '所属租户';
COMMENT ON COLUMN tags.project_id IS '所属项目';
COMMENT ON COLUMN tags.name IS '标签名称';
COMMENT ON COLUMN tags.color IS '显示颜色';
COMMENT ON COLUMN tags.created_at IS '创建时间';
COMMENT ON COLUMN tags.updated_at IS '更新时间；触发器自动维护';
COMMENT ON COLUMN tags.deleted_at IS '软删除时间；空表示未删除';
COMMENT ON COLUMN tags.create_by IS '创建用户；系统任务为空';
COMMENT ON COLUMN tags.update_by IS '最后修改用户；系统任务为空';
CREATE UNIQUE INDEX uq_tags_1 ON tags (tenant_id, project_id, name) WHERE deleted_at IS NULL;
CREATE INDEX ix_tags_scope ON tags (tenant_id, project_id, created_at DESC) WHERE deleted_at IS NULL;
CREATE INDEX ix_tags_deleted ON tags (deleted_at) WHERE deleted_at IS NOT NULL;
CREATE INDEX ix_tags_create_by ON tags (create_by) WHERE create_by IS NOT NULL;
CREATE INDEX ix_tags_update_by ON tags (update_by) WHERE update_by IS NOT NULL;
CREATE TRIGGER trg_tags_updated BEFORE UPDATE ON tags FOR EACH ROW EXECUTE FUNCTION bmm_touch_updated_at();

CREATE TABLE assets (
    id bigserial PRIMARY KEY,
    tenant_id bigint NOT NULL REFERENCES tenants(id),
    project_id bigint NOT NULL,
    name text NOT NULL,
    serial_number text,
    manufacturer text,
    model text,
    rack_id bigint,
    rack_u_position integer CHECK (rack_u_position > 0),
    height_u integer NOT NULL DEFAULT 1 CHECK (height_u > 0),
    bmc_address inet NOT NULL CHECK (masklen(bmc_address) = CASE family(bmc_address) WHEN 4 THEN 32 ELSE 128 END),
    bmc_protocol text NOT NULL DEFAULT 'redfish' CHECK (bmc_protocol IN ('redfish','ipmi')),
    bmc_port integer NOT NULL DEFAULT 443 CHECK (bmc_port BETWEEN 1 AND 65535),
    bmc_user text NOT NULL,
    bmc_secret_ciphertext bytea NOT NULL,
    bmc_secret_key_id text NOT NULL,
    bmc_tls_verify boolean NOT NULL DEFAULT true,
    management_ip inet,
    mac_address macaddr,
    status text NOT NULL DEFAULT 'unknown' CHECK (status IN ('unknown','online','offline','maintenance','retired')),
    power_state text NOT NULL DEFAULT 'unknown' CHECK (power_state IN ('on','off','unknown')),
    purchase_date date,
    warranty_expiry date,
    vendor text,
    redfish_snapshot jsonb,
    collected_at timestamptz,
    collection_error text,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz,
    create_by bigint REFERENCES users(id),
    update_by bigint REFERENCES users(id),
    UNIQUE (tenant_id, project_id, id),
    FOREIGN KEY (tenant_id, project_id) REFERENCES projects(tenant_id, id),
    CHECK ((rack_id IS NULL AND rack_u_position IS NULL) OR (rack_id IS NOT NULL AND rack_u_position IS NOT NULL)),
    FOREIGN KEY (tenant_id, project_id, rack_id) REFERENCES racks(tenant_id, project_id, id)
);
COMMENT ON TABLE assets IS '裸金属设备；仅提供 BMC 管理与 SOL，不提供虚拟机或 noVNC';
COMMENT ON COLUMN assets.id IS '行主键';
COMMENT ON COLUMN assets.tenant_id IS '所属租户';
COMMENT ON COLUMN assets.project_id IS '所属项目';
COMMENT ON COLUMN assets.name IS '设备名称';
COMMENT ON COLUMN assets.serial_number IS '整机序列号';
COMMENT ON COLUMN assets.manufacturer IS '制造商';
COMMENT ON COLUMN assets.model IS '设备型号';
COMMENT ON COLUMN assets.rack_id IS '所属机柜；机房通过机柜推导';
COMMENT ON COLUMN assets.rack_u_position IS '起始 U 位';
COMMENT ON COLUMN assets.height_u IS '占用 U 数';
COMMENT ON COLUMN assets.bmc_address IS 'BMC 主机地址';
COMMENT ON COLUMN assets.bmc_protocol IS 'BMC 管理协议';
COMMENT ON COLUMN assets.bmc_port IS 'BMC 端口；IPMI 通常为 623，服务端按协议填充';
COMMENT ON COLUMN assets.bmc_user IS 'BMC 用户名';
COMMENT ON COLUMN assets.bmc_secret_ciphertext IS '认证加密后的 BMC 密码；密文含随机数及认证标签';
COMMENT ON COLUMN assets.bmc_secret_key_id IS '服务端密钥版本；主密钥不入库';
COMMENT ON COLUMN assets.bmc_tls_verify IS '是否校验证书；禁用需审计';
COMMENT ON COLUMN assets.management_ip IS '业务管理地址';
COMMENT ON COLUMN assets.mac_address IS '主网卡 MAC';
COMMENT ON COLUMN assets.status IS '设备可达与管理状态';
COMMENT ON COLUMN assets.power_state IS '电源状态；不可由采集失败推断关机';
COMMENT ON COLUMN assets.purchase_date IS '采购日期';
COMMENT ON COLUMN assets.warranty_expiry IS '维保到期日期';
COMMENT ON COLUMN assets.vendor IS '供应商';
COMMENT ON COLUMN assets.redfish_snapshot IS '脱敏硬件快照';
COMMENT ON COLUMN assets.collected_at IS '最近成功采集时间';
COMMENT ON COLUMN assets.collection_error IS '最近采集错误；需脱敏';
COMMENT ON COLUMN assets.created_at IS '创建时间';
COMMENT ON COLUMN assets.updated_at IS '更新时间；触发器自动维护';
COMMENT ON COLUMN assets.deleted_at IS '软删除时间；空表示未删除';
COMMENT ON COLUMN assets.create_by IS '创建用户；系统任务为空';
COMMENT ON COLUMN assets.update_by IS '最后修改用户；系统任务为空';
CREATE UNIQUE INDEX uq_assets_1 ON assets (tenant_id, project_id, bmc_address, bmc_port) WHERE deleted_at IS NULL;
CREATE UNIQUE INDEX uq_assets_2 ON assets (tenant_id, project_id, serial_number) WHERE deleted_at IS NULL;
CREATE INDEX ix_assets_scope ON assets (tenant_id, project_id, created_at DESC) WHERE deleted_at IS NULL;
CREATE INDEX ix_assets_deleted ON assets (deleted_at) WHERE deleted_at IS NOT NULL;
CREATE INDEX ix_assets_rack_id ON assets (tenant_id, project_id, rack_id);
CREATE INDEX ix_assets_create_by ON assets (create_by) WHERE create_by IS NOT NULL;
CREATE INDEX ix_assets_update_by ON assets (update_by) WHERE update_by IS NOT NULL;
CREATE TRIGGER trg_assets_updated BEFORE UPDATE ON assets FOR EACH ROW EXECUTE FUNCTION bmm_touch_updated_at();

CREATE TABLE asset_tags (
    id bigserial PRIMARY KEY,
    tenant_id bigint NOT NULL REFERENCES tenants(id),
    project_id bigint NOT NULL,
    asset_id bigint NOT NULL,
    tag_id bigint NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz,
    create_by bigint REFERENCES users(id),
    update_by bigint REFERENCES users(id),
    UNIQUE (tenant_id, project_id, id),
    FOREIGN KEY (tenant_id, project_id) REFERENCES projects(tenant_id, id),
    FOREIGN KEY (tenant_id, project_id, asset_id) REFERENCES assets(tenant_id, project_id, id),
    FOREIGN KEY (tenant_id, project_id, tag_id) REFERENCES tags(tenant_id, project_id, id)
);
COMMENT ON TABLE asset_tags IS '资产标签关联';
COMMENT ON COLUMN asset_tags.id IS '行主键';
COMMENT ON COLUMN asset_tags.tenant_id IS '所属租户';
COMMENT ON COLUMN asset_tags.project_id IS '所属项目';
COMMENT ON COLUMN asset_tags.asset_id IS '资产';
COMMENT ON COLUMN asset_tags.tag_id IS '标签';
COMMENT ON COLUMN asset_tags.created_at IS '创建时间';
COMMENT ON COLUMN asset_tags.updated_at IS '更新时间；触发器自动维护';
COMMENT ON COLUMN asset_tags.deleted_at IS '软删除时间；空表示未删除';
COMMENT ON COLUMN asset_tags.create_by IS '创建用户；系统任务为空';
COMMENT ON COLUMN asset_tags.update_by IS '最后修改用户；系统任务为空';
CREATE UNIQUE INDEX uq_asset_tags_1 ON asset_tags (tenant_id, project_id, asset_id, tag_id) WHERE deleted_at IS NULL;
CREATE INDEX ix_asset_tags_scope ON asset_tags (tenant_id, project_id, created_at DESC) WHERE deleted_at IS NULL;
CREATE INDEX ix_asset_tags_deleted ON asset_tags (deleted_at) WHERE deleted_at IS NOT NULL;
CREATE INDEX ix_asset_tags_asset_id ON asset_tags (tenant_id, project_id, asset_id);
CREATE INDEX ix_asset_tags_tag_id ON asset_tags (tenant_id, project_id, tag_id);
CREATE INDEX ix_asset_tags_create_by ON asset_tags (create_by) WHERE create_by IS NOT NULL;
CREATE INDEX ix_asset_tags_update_by ON asset_tags (update_by) WHERE update_by IS NOT NULL;
CREATE TRIGGER trg_asset_tags_updated BEFORE UPDATE ON asset_tags FOR EACH ROW EXECUTE FUNCTION bmm_touch_updated_at();

CREATE TABLE rack_labels (
    id bigserial PRIMARY KEY,
    tenant_id bigint NOT NULL REFERENCES tenants(id),
    project_id bigint NOT NULL,
    rack_id bigint NOT NULL,
    key text NOT NULL,
    value text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz,
    create_by bigint REFERENCES users(id),
    update_by bigint REFERENCES users(id),
    UNIQUE (tenant_id, project_id, id),
    FOREIGN KEY (tenant_id, project_id) REFERENCES projects(tenant_id, id),
    FOREIGN KEY (tenant_id, project_id, rack_id) REFERENCES racks(tenant_id, project_id, id)
);
COMMENT ON TABLE rack_labels IS '机柜附加键值标签';
COMMENT ON COLUMN rack_labels.id IS '行主键';
COMMENT ON COLUMN rack_labels.tenant_id IS '所属租户';
COMMENT ON COLUMN rack_labels.project_id IS '所属项目';
COMMENT ON COLUMN rack_labels.rack_id IS '机柜';
COMMENT ON COLUMN rack_labels.key IS '标签键';
COMMENT ON COLUMN rack_labels.value IS '标签值';
COMMENT ON COLUMN rack_labels.created_at IS '创建时间';
COMMENT ON COLUMN rack_labels.updated_at IS '更新时间；触发器自动维护';
COMMENT ON COLUMN rack_labels.deleted_at IS '软删除时间；空表示未删除';
COMMENT ON COLUMN rack_labels.create_by IS '创建用户；系统任务为空';
COMMENT ON COLUMN rack_labels.update_by IS '最后修改用户；系统任务为空';
CREATE UNIQUE INDEX uq_rack_labels_1 ON rack_labels (tenant_id, project_id, rack_id, key) WHERE deleted_at IS NULL;
CREATE INDEX ix_rack_labels_scope ON rack_labels (tenant_id, project_id, created_at DESC) WHERE deleted_at IS NULL;
CREATE INDEX ix_rack_labels_deleted ON rack_labels (deleted_at) WHERE deleted_at IS NOT NULL;
CREATE INDEX ix_rack_labels_rack_id ON rack_labels (tenant_id, project_id, rack_id);
CREATE INDEX ix_rack_labels_create_by ON rack_labels (create_by) WHERE create_by IS NOT NULL;
CREATE INDEX ix_rack_labels_update_by ON rack_labels (update_by) WHERE update_by IS NOT NULL;
CREATE TRIGGER trg_rack_labels_updated BEFORE UPDATE ON rack_labels FOR EACH ROW EXECUTE FUNCTION bmm_touch_updated_at();

CREATE TABLE asset_hardwares (
    id bigserial PRIMARY KEY,
    tenant_id bigint NOT NULL REFERENCES tenants(id),
    project_id bigint NOT NULL,
    asset_id bigint NOT NULL,
    category text NOT NULL CHECK (category IN ('cpu','memory','disk','nic','gpu','psu','board','other')),
    component_key text NOT NULL,
    name text NOT NULL,
    serial_number text,
    vendor text,
    model text,
    details jsonb NOT NULL DEFAULT '{}'::jsonb,
    collected_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz,
    create_by bigint REFERENCES users(id),
    update_by bigint REFERENCES users(id),
    UNIQUE (tenant_id, project_id, id),
    FOREIGN KEY (tenant_id, project_id) REFERENCES projects(tenant_id, id),
    FOREIGN KEY (tenant_id, project_id, asset_id) REFERENCES assets(tenant_id, project_id, id)
);
COMMENT ON TABLE asset_hardwares IS '设备规范化硬件清单';
COMMENT ON COLUMN asset_hardwares.id IS '行主键';
COMMENT ON COLUMN asset_hardwares.tenant_id IS '所属租户';
COMMENT ON COLUMN asset_hardwares.project_id IS '所属项目';
COMMENT ON COLUMN asset_hardwares.asset_id IS '资产';
COMMENT ON COLUMN asset_hardwares.category IS '硬件类别';
COMMENT ON COLUMN asset_hardwares.component_key IS '设备内稳定组件标识';
COMMENT ON COLUMN asset_hardwares.name IS '组件名称';
COMMENT ON COLUMN asset_hardwares.serial_number IS '组件序列号';
COMMENT ON COLUMN asset_hardwares.vendor IS '厂商';
COMMENT ON COLUMN asset_hardwares.model IS '型号';
COMMENT ON COLUMN asset_hardwares.details IS '容量、频率等结构化信息';
COMMENT ON COLUMN asset_hardwares.collected_at IS '采集时间';
COMMENT ON COLUMN asset_hardwares.created_at IS '创建时间';
COMMENT ON COLUMN asset_hardwares.updated_at IS '更新时间；触发器自动维护';
COMMENT ON COLUMN asset_hardwares.deleted_at IS '软删除时间；空表示未删除';
COMMENT ON COLUMN asset_hardwares.create_by IS '创建用户；系统任务为空';
COMMENT ON COLUMN asset_hardwares.update_by IS '最后修改用户；系统任务为空';
CREATE UNIQUE INDEX uq_asset_hardwares_1 ON asset_hardwares (tenant_id, project_id, asset_id, component_key) WHERE deleted_at IS NULL;
CREATE INDEX ix_asset_hardwares_scope ON asset_hardwares (tenant_id, project_id, created_at DESC) WHERE deleted_at IS NULL;
CREATE INDEX ix_asset_hardwares_deleted ON asset_hardwares (deleted_at) WHERE deleted_at IS NOT NULL;
CREATE INDEX ix_asset_hardwares_asset_id ON asset_hardwares (tenant_id, project_id, asset_id);
CREATE INDEX ix_asset_hardwares_create_by ON asset_hardwares (create_by) WHERE create_by IS NOT NULL;
CREATE INDEX ix_asset_hardwares_update_by ON asset_hardwares (update_by) WHERE update_by IS NOT NULL;
CREATE TRIGGER trg_asset_hardwares_updated BEFORE UPDATE ON asset_hardwares FOR EACH ROW EXECUTE FUNCTION bmm_touch_updated_at();

CREATE TABLE firmware_inventories (
    id bigserial PRIMARY KEY,
    tenant_id bigint NOT NULL REFERENCES tenants(id),
    project_id bigint NOT NULL,
    asset_id bigint NOT NULL,
    component text NOT NULL,
    version text NOT NULL,
    vendor text,
    collected_at timestamptz NOT NULL,
    details jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz,
    create_by bigint REFERENCES users(id),
    update_by bigint REFERENCES users(id),
    UNIQUE (tenant_id, project_id, id),
    FOREIGN KEY (tenant_id, project_id) REFERENCES projects(tenant_id, id),
    FOREIGN KEY (tenant_id, project_id, asset_id) REFERENCES assets(tenant_id, project_id, id)
);
COMMENT ON TABLE firmware_inventories IS '设备固件清单；版本只作标识，禁止按字符串大小判断升级';
COMMENT ON COLUMN firmware_inventories.id IS '行主键';
COMMENT ON COLUMN firmware_inventories.tenant_id IS '所属租户';
COMMENT ON COLUMN firmware_inventories.project_id IS '所属项目';
COMMENT ON COLUMN firmware_inventories.asset_id IS '资产';
COMMENT ON COLUMN firmware_inventories.component IS '组件标识';
COMMENT ON COLUMN firmware_inventories.version IS '当前版本';
COMMENT ON COLUMN firmware_inventories.vendor IS '厂商';
COMMENT ON COLUMN firmware_inventories.collected_at IS '采集时间';
COMMENT ON COLUMN firmware_inventories.details IS '原始脱敏元信息';
COMMENT ON COLUMN firmware_inventories.created_at IS '创建时间';
COMMENT ON COLUMN firmware_inventories.updated_at IS '更新时间；触发器自动维护';
COMMENT ON COLUMN firmware_inventories.deleted_at IS '软删除时间；空表示未删除';
COMMENT ON COLUMN firmware_inventories.create_by IS '创建用户；系统任务为空';
COMMENT ON COLUMN firmware_inventories.update_by IS '最后修改用户；系统任务为空';
CREATE UNIQUE INDEX uq_firmware_inventories_1 ON firmware_inventories (tenant_id, project_id, asset_id, component) WHERE deleted_at IS NULL;
CREATE INDEX ix_firmware_inventories_scope ON firmware_inventories (tenant_id, project_id, created_at DESC) WHERE deleted_at IS NULL;
CREATE INDEX ix_firmware_inventories_deleted ON firmware_inventories (deleted_at) WHERE deleted_at IS NOT NULL;
CREATE INDEX ix_firmware_inventories_asset_id ON firmware_inventories (tenant_id, project_id, asset_id);
CREATE INDEX ix_firmware_inventories_create_by ON firmware_inventories (create_by) WHERE create_by IS NOT NULL;
CREATE INDEX ix_firmware_inventories_update_by ON firmware_inventories (update_by) WHERE update_by IS NOT NULL;
CREATE TRIGGER trg_firmware_inventories_updated BEFORE UPDATE ON firmware_inventories FOR EACH ROW EXECUTE FUNCTION bmm_touch_updated_at();

CREATE TABLE operations (
    id bigserial PRIMARY KEY,
    tenant_id bigint NOT NULL REFERENCES tenants(id),
    project_id bigint NOT NULL,
    asset_id bigint NOT NULL,
    user_id bigint REFERENCES users(id),
    kind text NOT NULL CHECK (kind IN ('power_on','power_off','power_cycle','reset','collect','install','firmware_upgrade','boot_order','discover')),
    idempotency_key text NOT NULL,
    payload jsonb NOT NULL DEFAULT '{}'::jsonb,
    status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','running','retry_wait','succeeded','failed','cancelled','timeout','unknown')),
    attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    max_attempts integer NOT NULL DEFAULT 1 CHECK (max_attempts > 0),
    next_run_at timestamptz NOT NULL DEFAULT now(),
    timeout_seconds integer NOT NULL DEFAULT 300 CHECK (timeout_seconds > 0),
    worker_id text,
    started_at timestamptz,
    completed_at timestamptz,
    error text,
    result jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz,
    create_by bigint REFERENCES users(id),
    update_by bigint REFERENCES users(id),
    UNIQUE (tenant_id, project_id, id),
    FOREIGN KEY (tenant_id, project_id) REFERENCES projects(tenant_id, id),
    CHECK (completed_at IS NULL OR (started_at IS NOT NULL AND completed_at >= started_at)),
    FOREIGN KEY (tenant_id, project_id, asset_id) REFERENCES assets(tenant_id, project_id, id)
);
COMMENT ON TABLE operations IS '异步设备操作；unknown 表示结果不确定，禁止盲目重试不可逆动作';
COMMENT ON COLUMN operations.id IS '行主键';
COMMENT ON COLUMN operations.tenant_id IS '所属租户';
COMMENT ON COLUMN operations.project_id IS '所属项目';
COMMENT ON COLUMN operations.asset_id IS '目标资产';
COMMENT ON COLUMN operations.user_id IS '发起用户；系统任务可空';
COMMENT ON COLUMN operations.kind IS '操作类型';
COMMENT ON COLUMN operations.idempotency_key IS '项目内请求幂等键';
COMMENT ON COLUMN operations.payload IS '脱敏执行参数；不得存明文凭据';
COMMENT ON COLUMN operations.status IS '任务状态';
COMMENT ON COLUMN operations.attempts IS '已执行次数';
COMMENT ON COLUMN operations.max_attempts IS '最大执行次数；不可逆动作默认单次';
COMMENT ON COLUMN operations.next_run_at IS '下一次调度时间';
COMMENT ON COLUMN operations.timeout_seconds IS '执行超时秒数';
COMMENT ON COLUMN operations.worker_id IS '执行节点';
COMMENT ON COLUMN operations.started_at IS '开始时间';
COMMENT ON COLUMN operations.completed_at IS '终态时间';
COMMENT ON COLUMN operations.error IS '脱敏错误';
COMMENT ON COLUMN operations.result IS '脱敏执行结果';
COMMENT ON COLUMN operations.created_at IS '创建时间';
COMMENT ON COLUMN operations.updated_at IS '更新时间；触发器自动维护';
COMMENT ON COLUMN operations.deleted_at IS '软删除时间；空表示未删除';
COMMENT ON COLUMN operations.create_by IS '创建用户；系统任务为空';
COMMENT ON COLUMN operations.update_by IS '最后修改用户；系统任务为空';
CREATE UNIQUE INDEX uq_operations_1 ON operations (tenant_id, project_id, idempotency_key) WHERE deleted_at IS NULL;
CREATE INDEX ix_operations_scope ON operations (tenant_id, project_id, created_at DESC) WHERE deleted_at IS NULL;
CREATE INDEX ix_operations_deleted ON operations (deleted_at) WHERE deleted_at IS NOT NULL;
CREATE INDEX ix_operations_asset_id ON operations (tenant_id, project_id, asset_id);
CREATE INDEX ix_operations_user_id ON operations (tenant_id, project_id, user_id);
CREATE INDEX ix_operations_create_by ON operations (create_by) WHERE create_by IS NOT NULL;
CREATE INDEX ix_operations_update_by ON operations (update_by) WHERE update_by IS NOT NULL;
CREATE TRIGGER trg_operations_updated BEFORE UPDATE ON operations FOR EACH ROW EXECUTE FUNCTION bmm_touch_updated_at();

CREATE TABLE operation_logs (
    id bigserial PRIMARY KEY,
    tenant_id bigint NOT NULL REFERENCES tenants(id),
    project_id bigint NOT NULL,
    operation_id bigint NOT NULL,
    sequence bigint NOT NULL CHECK (sequence >= 0),
    level text NOT NULL CHECK (level IN ('debug','info','warning','error')),
    message text NOT NULL,
    occurred_at timestamptz NOT NULL DEFAULT now(),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz,
    create_by bigint REFERENCES users(id),
    update_by bigint REFERENCES users(id),
    UNIQUE (tenant_id, project_id, id),
    FOREIGN KEY (tenant_id, project_id) REFERENCES projects(tenant_id, id),
    FOREIGN KEY (tenant_id, project_id, operation_id) REFERENCES operations(tenant_id, project_id, id)
);
COMMENT ON TABLE operation_logs IS '任务执行日志；仅记录脱敏元信息';
COMMENT ON COLUMN operation_logs.id IS '行主键';
COMMENT ON COLUMN operation_logs.tenant_id IS '所属租户';
COMMENT ON COLUMN operation_logs.project_id IS '所属项目';
COMMENT ON COLUMN operation_logs.operation_id IS '所属任务';
COMMENT ON COLUMN operation_logs.sequence IS '日志序号';
COMMENT ON COLUMN operation_logs.level IS '日志级别';
COMMENT ON COLUMN operation_logs.message IS '脱敏日志内容';
COMMENT ON COLUMN operation_logs.occurred_at IS '发生时间';
COMMENT ON COLUMN operation_logs.created_at IS '创建时间';
COMMENT ON COLUMN operation_logs.updated_at IS '更新时间；触发器自动维护';
COMMENT ON COLUMN operation_logs.deleted_at IS '软删除时间；空表示未删除';
COMMENT ON COLUMN operation_logs.create_by IS '创建用户；系统任务为空';
COMMENT ON COLUMN operation_logs.update_by IS '最后修改用户；系统任务为空';
CREATE UNIQUE INDEX uq_operation_logs_1 ON operation_logs (tenant_id, project_id, operation_id, sequence) WHERE deleted_at IS NULL;
CREATE INDEX ix_operation_logs_scope ON operation_logs (tenant_id, project_id, created_at DESC) WHERE deleted_at IS NULL;
CREATE INDEX ix_operation_logs_deleted ON operation_logs (deleted_at) WHERE deleted_at IS NOT NULL;
CREATE INDEX ix_operation_logs_operation_id ON operation_logs (tenant_id, project_id, operation_id);
CREATE INDEX ix_operation_logs_create_by ON operation_logs (create_by) WHERE create_by IS NOT NULL;
CREATE INDEX ix_operation_logs_update_by ON operation_logs (update_by) WHERE update_by IS NOT NULL;
CREATE TRIGGER trg_operation_logs_updated BEFORE UPDATE ON operation_logs FOR EACH ROW EXECUTE FUNCTION bmm_touch_updated_at();

CREATE TABLE task_outbox (
    id bigserial PRIMARY KEY,
    tenant_id bigint NOT NULL REFERENCES tenants(id),
    project_id bigint NOT NULL,
    operation_id bigint NOT NULL,
    event_key text NOT NULL,
    event_type text NOT NULL,
    payload jsonb NOT NULL DEFAULT '{}'::jsonb,
    status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','publishing','published','failed')),
    attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    available_at timestamptz NOT NULL DEFAULT now(),
    locked_until timestamptz,
    published_at timestamptz,
    last_error text,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz,
    create_by bigint REFERENCES users(id),
    update_by bigint REFERENCES users(id),
    UNIQUE (tenant_id, project_id, id),
    FOREIGN KEY (tenant_id, project_id) REFERENCES projects(tenant_id, id),
    FOREIGN KEY (tenant_id, project_id, operation_id) REFERENCES operations(tenant_id, project_id, id)
);
COMMENT ON TABLE task_outbox IS '事务发件箱；与业务任务同事务写入，消费方按事件键幂等';
COMMENT ON COLUMN task_outbox.id IS '行主键';
COMMENT ON COLUMN task_outbox.tenant_id IS '所属租户';
COMMENT ON COLUMN task_outbox.project_id IS '所属项目';
COMMENT ON COLUMN task_outbox.operation_id IS '操作任务';
COMMENT ON COLUMN task_outbox.event_key IS '事件幂等键';
COMMENT ON COLUMN task_outbox.event_type IS '事件类型';
COMMENT ON COLUMN task_outbox.payload IS '脱敏消息';
COMMENT ON COLUMN task_outbox.status IS '投递状态';
COMMENT ON COLUMN task_outbox.attempts IS '投递次数';
COMMENT ON COLUMN task_outbox.available_at IS '可投递时间';
COMMENT ON COLUMN task_outbox.locked_until IS '投递认领到期时间';
COMMENT ON COLUMN task_outbox.published_at IS '投递成功时间';
COMMENT ON COLUMN task_outbox.last_error IS '最近投递错误';
COMMENT ON COLUMN task_outbox.created_at IS '创建时间';
COMMENT ON COLUMN task_outbox.updated_at IS '更新时间；触发器自动维护';
COMMENT ON COLUMN task_outbox.deleted_at IS '软删除时间；空表示未删除';
COMMENT ON COLUMN task_outbox.create_by IS '创建用户；系统任务为空';
COMMENT ON COLUMN task_outbox.update_by IS '最后修改用户；系统任务为空';
CREATE UNIQUE INDEX uq_task_outbox_1 ON task_outbox (tenant_id, project_id, event_key) WHERE deleted_at IS NULL;
CREATE INDEX ix_task_outbox_scope ON task_outbox (tenant_id, project_id, created_at DESC) WHERE deleted_at IS NULL;
CREATE INDEX ix_task_outbox_deleted ON task_outbox (deleted_at) WHERE deleted_at IS NOT NULL;
CREATE INDEX ix_task_outbox_operation_id ON task_outbox (tenant_id, project_id, operation_id);
CREATE INDEX ix_task_outbox_create_by ON task_outbox (create_by) WHERE create_by IS NOT NULL;
CREATE INDEX ix_task_outbox_update_by ON task_outbox (update_by) WHERE update_by IS NOT NULL;
CREATE TRIGGER trg_task_outbox_updated BEFORE UPDATE ON task_outbox FOR EACH ROW EXECUTE FUNCTION bmm_touch_updated_at();

CREATE TABLE device_leases (
    id bigserial PRIMARY KEY,
    tenant_id bigint NOT NULL REFERENCES tenants(id),
    project_id bigint NOT NULL,
    asset_id bigint NOT NULL,
    operation_id bigint NOT NULL,
    holder_id text NOT NULL,
    fencing_token bigint NOT NULL DEFAULT 1 CHECK (fencing_token > 0),
    expires_at timestamptz NOT NULL,
    heartbeat_at timestamptz NOT NULL DEFAULT now(),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz,
    create_by bigint REFERENCES users(id),
    update_by bigint REFERENCES users(id),
    UNIQUE (tenant_id, project_id, id),
    FOREIGN KEY (tenant_id, project_id) REFERENCES projects(tenant_id, id),
    CHECK (expires_at > heartbeat_at),
    FOREIGN KEY (tenant_id, project_id, asset_id) REFERENCES assets(tenant_id, project_id, id),
    FOREIGN KEY (tenant_id, project_id, operation_id) REFERENCES operations(tenant_id, project_id, id)
);
COMMENT ON TABLE device_leases IS '设备互斥租约；每设备仅一行，过期接管须原子条件更新并递增 fencing_token';
COMMENT ON COLUMN device_leases.id IS '行主键';
COMMENT ON COLUMN device_leases.tenant_id IS '所属租户';
COMMENT ON COLUMN device_leases.project_id IS '所属项目';
COMMENT ON COLUMN device_leases.asset_id IS '被锁设备';
COMMENT ON COLUMN device_leases.operation_id IS '持锁任务';
COMMENT ON COLUMN device_leases.holder_id IS '持锁节点';
COMMENT ON COLUMN device_leases.fencing_token IS '防止旧执行者写回的单调令牌';
COMMENT ON COLUMN device_leases.expires_at IS '租约到期时间';
COMMENT ON COLUMN device_leases.heartbeat_at IS '最近续租时间';
COMMENT ON COLUMN device_leases.created_at IS '创建时间';
COMMENT ON COLUMN device_leases.updated_at IS '更新时间；触发器自动维护';
COMMENT ON COLUMN device_leases.deleted_at IS '软删除时间；空表示未删除';
COMMENT ON COLUMN device_leases.create_by IS '创建用户；系统任务为空';
COMMENT ON COLUMN device_leases.update_by IS '最后修改用户；系统任务为空';
CREATE INDEX ix_device_leases_scope ON device_leases (tenant_id, project_id, created_at DESC) WHERE deleted_at IS NULL;
CREATE INDEX ix_device_leases_deleted ON device_leases (deleted_at) WHERE deleted_at IS NOT NULL;
CREATE INDEX ix_device_leases_asset_id ON device_leases (tenant_id, project_id, asset_id);
CREATE INDEX ix_device_leases_operation_id ON device_leases (tenant_id, project_id, operation_id);
CREATE INDEX ix_device_leases_create_by ON device_leases (create_by) WHERE create_by IS NOT NULL;
CREATE INDEX ix_device_leases_update_by ON device_leases (update_by) WHERE update_by IS NOT NULL;
CREATE TRIGGER trg_device_leases_updated BEFORE UPDATE ON device_leases FOR EACH ROW EXECUTE FUNCTION bmm_touch_updated_at();

CREATE TABLE sol_sessions (
    id bigserial PRIMARY KEY,
    tenant_id bigint NOT NULL REFERENCES tenants(id),
    project_id bigint NOT NULL,
    asset_id bigint NOT NULL,
    user_id bigint NOT NULL REFERENCES users(id),
    node_id text NOT NULL,
    started_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL,
    ended_at timestamptz,
    end_reason text,
    client_ip inet,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz,
    create_by bigint REFERENCES users(id),
    update_by bigint REFERENCES users(id),
    UNIQUE (tenant_id, project_id, id),
    FOREIGN KEY (tenant_id, project_id) REFERENCES projects(tenant_id, id),
    CHECK (expires_at > started_at),
    CHECK (ended_at IS NULL OR ended_at >= started_at),
    FOREIGN KEY (tenant_id, project_id, asset_id) REFERENCES assets(tenant_id, project_id, id)
);
COMMENT ON TABLE sol_sessions IS 'SOL 串口会话；仅存连接元信息，不存串口正文';
COMMENT ON COLUMN sol_sessions.id IS '行主键';
COMMENT ON COLUMN sol_sessions.tenant_id IS '所属租户';
COMMENT ON COLUMN sol_sessions.project_id IS '所属项目';
COMMENT ON COLUMN sol_sessions.asset_id IS '目标资产';
COMMENT ON COLUMN sol_sessions.user_id IS '操作者';
COMMENT ON COLUMN sol_sessions.node_id IS '承载连接节点';
COMMENT ON COLUMN sol_sessions.started_at IS '开始时间';
COMMENT ON COLUMN sol_sessions.expires_at IS '强制失效时间';
COMMENT ON COLUMN sol_sessions.ended_at IS '结束时间';
COMMENT ON COLUMN sol_sessions.end_reason IS '断开原因';
COMMENT ON COLUMN sol_sessions.client_ip IS '客户端地址';
COMMENT ON COLUMN sol_sessions.created_at IS '创建时间';
COMMENT ON COLUMN sol_sessions.updated_at IS '更新时间；触发器自动维护';
COMMENT ON COLUMN sol_sessions.deleted_at IS '软删除时间；空表示未删除';
COMMENT ON COLUMN sol_sessions.create_by IS '创建用户；系统任务为空';
COMMENT ON COLUMN sol_sessions.update_by IS '最后修改用户；系统任务为空';
CREATE INDEX ix_sol_sessions_scope ON sol_sessions (tenant_id, project_id, created_at DESC) WHERE deleted_at IS NULL;
CREATE INDEX ix_sol_sessions_deleted ON sol_sessions (deleted_at) WHERE deleted_at IS NOT NULL;
CREATE INDEX ix_sol_sessions_asset_id ON sol_sessions (tenant_id, project_id, asset_id);
CREATE INDEX ix_sol_sessions_user_id ON sol_sessions (tenant_id, project_id, user_id);
CREATE INDEX ix_sol_sessions_create_by ON sol_sessions (create_by) WHERE create_by IS NOT NULL;
CREATE INDEX ix_sol_sessions_update_by ON sol_sessions (update_by) WHERE update_by IS NOT NULL;
CREATE TRIGGER trg_sol_sessions_updated BEFORE UPDATE ON sol_sessions FOR EACH ROW EXECUTE FUNCTION bmm_touch_updated_at();

CREATE TABLE networks (
    id bigserial PRIMARY KEY,
    tenant_id bigint NOT NULL REFERENCES tenants(id),
    project_id bigint NOT NULL,
    name text NOT NULL,
    subnet cidr NOT NULL,
    gateway inet,
    vlan_id integer CHECK (vlan_id BETWEEN 1 AND 4094),
    dns_servers inet[] NOT NULL DEFAULT ARRAY[]::inet[],
    dhcp_enabled boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz,
    create_by bigint REFERENCES users(id),
    update_by bigint REFERENCES users(id),
    UNIQUE (tenant_id, project_id, id),
    FOREIGN KEY (tenant_id, project_id) REFERENCES projects(tenant_id, id),
    CHECK (gateway IS NULL OR gateway <<= subnet)
);
COMMENT ON TABLE networks IS '项目网络；子网重叠与网关可用性由服务层事务校验';
COMMENT ON COLUMN networks.id IS '行主键';
COMMENT ON COLUMN networks.tenant_id IS '所属租户';
COMMENT ON COLUMN networks.project_id IS '所属项目';
COMMENT ON COLUMN networks.name IS '网络名称';
COMMENT ON COLUMN networks.subnet IS '网络前缀';
COMMENT ON COLUMN networks.gateway IS '网关主机地址';
COMMENT ON COLUMN networks.vlan_id IS 'VLAN 编号；空表示未打标';
COMMENT ON COLUMN networks.dns_servers IS 'DNS 地址列表';
COMMENT ON COLUMN networks.dhcp_enabled IS 'DHCP 开关';
COMMENT ON COLUMN networks.created_at IS '创建时间';
COMMENT ON COLUMN networks.updated_at IS '更新时间；触发器自动维护';
COMMENT ON COLUMN networks.deleted_at IS '软删除时间；空表示未删除';
COMMENT ON COLUMN networks.create_by IS '创建用户；系统任务为空';
COMMENT ON COLUMN networks.update_by IS '最后修改用户；系统任务为空';
CREATE UNIQUE INDEX uq_networks_1 ON networks (tenant_id, project_id, name) WHERE deleted_at IS NULL;
CREATE INDEX ix_networks_scope ON networks (tenant_id, project_id, created_at DESC) WHERE deleted_at IS NULL;
CREATE INDEX ix_networks_deleted ON networks (deleted_at) WHERE deleted_at IS NOT NULL;
CREATE INDEX ix_networks_create_by ON networks (create_by) WHERE create_by IS NOT NULL;
CREATE INDEX ix_networks_update_by ON networks (update_by) WHERE update_by IS NOT NULL;
CREATE TRIGGER trg_networks_updated BEFORE UPDATE ON networks FOR EACH ROW EXECUTE FUNCTION bmm_touch_updated_at();

CREATE TABLE ip_pools (
    id bigserial PRIMARY KEY,
    tenant_id bigint NOT NULL REFERENCES tenants(id),
    project_id bigint NOT NULL,
    network_id bigint NOT NULL,
    name text NOT NULL,
    start_address inet NOT NULL,
    end_address inet NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz,
    create_by bigint REFERENCES users(id),
    update_by bigint REFERENCES users(id),
    UNIQUE (tenant_id, project_id, id),
    FOREIGN KEY (tenant_id, project_id) REFERENCES projects(tenant_id, id),
    CHECK (family(start_address) = family(end_address) AND start_address <= end_address),
    CHECK (masklen(start_address) = CASE family(start_address) WHEN 4 THEN 32 ELSE 128 END),
    CHECK (masklen(end_address) = CASE family(end_address) WHEN 4 THEN 32 ELSE 128 END),
    FOREIGN KEY (tenant_id, project_id, network_id) REFERENCES networks(tenant_id, project_id, id),
    UNIQUE (tenant_id, project_id, id, network_id)
);
COMMENT ON TABLE ip_pools IS '网络地址池；池范围与网络包含关系由服务层锁定网络后校验';
COMMENT ON COLUMN ip_pools.id IS '行主键';
COMMENT ON COLUMN ip_pools.tenant_id IS '所属租户';
COMMENT ON COLUMN ip_pools.project_id IS '所属项目';
COMMENT ON COLUMN ip_pools.network_id IS '所属网络';
COMMENT ON COLUMN ip_pools.name IS '地址池名称';
COMMENT ON COLUMN ip_pools.start_address IS '起始地址';
COMMENT ON COLUMN ip_pools.end_address IS '结束地址';
COMMENT ON COLUMN ip_pools.created_at IS '创建时间';
COMMENT ON COLUMN ip_pools.updated_at IS '更新时间；触发器自动维护';
COMMENT ON COLUMN ip_pools.deleted_at IS '软删除时间；空表示未删除';
COMMENT ON COLUMN ip_pools.create_by IS '创建用户；系统任务为空';
COMMENT ON COLUMN ip_pools.update_by IS '最后修改用户；系统任务为空';
CREATE UNIQUE INDEX uq_ip_pools_1 ON ip_pools (tenant_id, project_id, network_id, name) WHERE deleted_at IS NULL;
CREATE INDEX ix_ip_pools_scope ON ip_pools (tenant_id, project_id, created_at DESC) WHERE deleted_at IS NULL;
CREATE INDEX ix_ip_pools_deleted ON ip_pools (deleted_at) WHERE deleted_at IS NOT NULL;
CREATE INDEX ix_ip_pools_network_id ON ip_pools (tenant_id, project_id, network_id);
CREATE INDEX ix_ip_pools_create_by ON ip_pools (create_by) WHERE create_by IS NOT NULL;
CREATE INDEX ix_ip_pools_update_by ON ip_pools (update_by) WHERE update_by IS NOT NULL;
CREATE TRIGGER trg_ip_pools_updated BEFORE UPDATE ON ip_pools FOR EACH ROW EXECUTE FUNCTION bmm_touch_updated_at();

CREATE TABLE ip_allocations (
    id bigserial PRIMARY KEY,
    tenant_id bigint NOT NULL REFERENCES tenants(id),
    project_id bigint NOT NULL,
    network_id bigint NOT NULL,
    pool_id bigint,
    asset_id bigint,
    address inet NOT NULL CHECK (masklen(address) = CASE family(address) WHEN 4 THEN 32 ELSE 128 END),
    mac_address macaddr,
    status text NOT NULL DEFAULT 'reserved' CHECK (status IN ('reserved','allocated','released')),
    allocated_at timestamptz,
    released_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz,
    create_by bigint REFERENCES users(id),
    update_by bigint REFERENCES users(id),
    UNIQUE (tenant_id, project_id, id),
    FOREIGN KEY (tenant_id, project_id) REFERENCES projects(tenant_id, id),
    FOREIGN KEY (tenant_id, project_id, network_id) REFERENCES networks(tenant_id, project_id, id),
    FOREIGN KEY (tenant_id, project_id, pool_id) REFERENCES ip_pools(tenant_id, project_id, id),
    FOREIGN KEY (tenant_id, project_id, asset_id) REFERENCES assets(tenant_id, project_id, id),
    FOREIGN KEY (tenant_id, project_id, pool_id, network_id) REFERENCES ip_pools(tenant_id, project_id, id, network_id)
);
COMMENT ON TABLE ip_allocations IS 'IP 地址分配；地址范围、保留地址及池所属网络由服务层校验';
COMMENT ON COLUMN ip_allocations.id IS '行主键';
COMMENT ON COLUMN ip_allocations.tenant_id IS '所属租户';
COMMENT ON COLUMN ip_allocations.project_id IS '所属项目';
COMMENT ON COLUMN ip_allocations.network_id IS '网络';
COMMENT ON COLUMN ip_allocations.pool_id IS '地址池';
COMMENT ON COLUMN ip_allocations.asset_id IS '分配资产';
COMMENT ON COLUMN ip_allocations.address IS '主机地址';
COMMENT ON COLUMN ip_allocations.mac_address IS '绑定 MAC';
COMMENT ON COLUMN ip_allocations.status IS '分配状态';
COMMENT ON COLUMN ip_allocations.allocated_at IS '分配时间';
COMMENT ON COLUMN ip_allocations.released_at IS '释放时间';
COMMENT ON COLUMN ip_allocations.created_at IS '创建时间';
COMMENT ON COLUMN ip_allocations.updated_at IS '更新时间；触发器自动维护';
COMMENT ON COLUMN ip_allocations.deleted_at IS '软删除时间；空表示未删除';
COMMENT ON COLUMN ip_allocations.create_by IS '创建用户；系统任务为空';
COMMENT ON COLUMN ip_allocations.update_by IS '最后修改用户；系统任务为空';
CREATE INDEX ix_ip_allocations_scope ON ip_allocations (tenant_id, project_id, created_at DESC) WHERE deleted_at IS NULL;
CREATE INDEX ix_ip_allocations_deleted ON ip_allocations (deleted_at) WHERE deleted_at IS NOT NULL;
CREATE INDEX ix_ip_allocations_network_id ON ip_allocations (tenant_id, project_id, network_id);
CREATE INDEX ix_ip_allocations_pool_id ON ip_allocations (tenant_id, project_id, pool_id);
CREATE INDEX ix_ip_allocations_asset_id ON ip_allocations (tenant_id, project_id, asset_id);
CREATE INDEX ix_ip_allocations_create_by ON ip_allocations (create_by) WHERE create_by IS NOT NULL;
CREATE INDEX ix_ip_allocations_update_by ON ip_allocations (update_by) WHERE update_by IS NOT NULL;
CREATE TRIGGER trg_ip_allocations_updated BEFORE UPDATE ON ip_allocations FOR EACH ROW EXECUTE FUNCTION bmm_touch_updated_at();

CREATE TABLE pxe_configs (
    id bigserial PRIMARY KEY,
    tenant_id bigint NOT NULL REFERENCES tenants(id),
    project_id bigint NOT NULL,
    network_id bigint NOT NULL,
    name text NOT NULL,
    boot_mode text NOT NULL CHECK (boot_mode IN ('bios','uefi','both')),
    next_server inet NOT NULL,
    boot_file text NOT NULL,
    enabled boolean NOT NULL DEFAULT false,
    config jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz,
    create_by bigint REFERENCES users(id),
    update_by bigint REFERENCES users(id),
    UNIQUE (tenant_id, project_id, id),
    FOREIGN KEY (tenant_id, project_id) REFERENCES projects(tenant_id, id),
    FOREIGN KEY (tenant_id, project_id, network_id) REFERENCES networks(tenant_id, project_id, id)
);
COMMENT ON TABLE pxe_configs IS '网络 PXE 服务配置；DHCP 变更由受控执行器应用';
COMMENT ON COLUMN pxe_configs.id IS '行主键';
COMMENT ON COLUMN pxe_configs.tenant_id IS '所属租户';
COMMENT ON COLUMN pxe_configs.project_id IS '所属项目';
COMMENT ON COLUMN pxe_configs.network_id IS '所属网络';
COMMENT ON COLUMN pxe_configs.name IS '配置名称';
COMMENT ON COLUMN pxe_configs.boot_mode IS '启动模式';
COMMENT ON COLUMN pxe_configs.next_server IS '引导服务器';
COMMENT ON COLUMN pxe_configs.boot_file IS '引导文件相对路径';
COMMENT ON COLUMN pxe_configs.enabled IS '是否启用';
COMMENT ON COLUMN pxe_configs.config IS '结构化引导参数';
COMMENT ON COLUMN pxe_configs.created_at IS '创建时间';
COMMENT ON COLUMN pxe_configs.updated_at IS '更新时间；触发器自动维护';
COMMENT ON COLUMN pxe_configs.deleted_at IS '软删除时间；空表示未删除';
COMMENT ON COLUMN pxe_configs.create_by IS '创建用户；系统任务为空';
COMMENT ON COLUMN pxe_configs.update_by IS '最后修改用户；系统任务为空';
CREATE UNIQUE INDEX uq_pxe_configs_1 ON pxe_configs (tenant_id, project_id, network_id, name) WHERE deleted_at IS NULL;
CREATE INDEX ix_pxe_configs_scope ON pxe_configs (tenant_id, project_id, created_at DESC) WHERE deleted_at IS NULL;
CREATE INDEX ix_pxe_configs_deleted ON pxe_configs (deleted_at) WHERE deleted_at IS NOT NULL;
CREATE INDEX ix_pxe_configs_network_id ON pxe_configs (tenant_id, project_id, network_id);
CREATE INDEX ix_pxe_configs_create_by ON pxe_configs (create_by) WHERE create_by IS NOT NULL;
CREATE INDEX ix_pxe_configs_update_by ON pxe_configs (update_by) WHERE update_by IS NOT NULL;
CREATE TRIGGER trg_pxe_configs_updated BEFORE UPDATE ON pxe_configs FOR EACH ROW EXECUTE FUNCTION bmm_touch_updated_at();

CREATE TABLE images (
    id bigserial PRIMARY KEY,
    tenant_id bigint NOT NULL REFERENCES tenants(id),
    project_id bigint NOT NULL,
    name text NOT NULL,
    object_key text NOT NULL,
    size_bytes bigint NOT NULL CHECK (size_bytes > 0),
    sha256 text NOT NULL CHECK (sha256 ~ '^[0-9a-f]{64}$'),
    os_type text NOT NULL,
    version text NOT NULL,
    architecture text NOT NULL CHECK (architecture IN ('x86_64','aarch64')),
    status text NOT NULL DEFAULT 'uploading' CHECK (status IN ('uploading','verifying','ready','failed')),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz,
    create_by bigint REFERENCES users(id),
    update_by bigint REFERENCES users(id),
    UNIQUE (tenant_id, project_id, id),
    FOREIGN KEY (tenant_id, project_id) REFERENCES projects(tenant_id, id)
);
COMMENT ON TABLE images IS '镜像对象元信息；镜像正文在对象存储';
COMMENT ON COLUMN images.id IS '行主键';
COMMENT ON COLUMN images.tenant_id IS '所属租户';
COMMENT ON COLUMN images.project_id IS '所属项目';
COMMENT ON COLUMN images.name IS '镜像名称';
COMMENT ON COLUMN images.object_key IS '对象存储键';
COMMENT ON COLUMN images.size_bytes IS '文件字节数';
COMMENT ON COLUMN images.sha256 IS '小写 SHA256';
COMMENT ON COLUMN images.os_type IS '操作系统类型';
COMMENT ON COLUMN images.version IS '操作系统版本';
COMMENT ON COLUMN images.architecture IS 'CPU 架构';
COMMENT ON COLUMN images.status IS '上传校验状态';
COMMENT ON COLUMN images.created_at IS '创建时间';
COMMENT ON COLUMN images.updated_at IS '更新时间；触发器自动维护';
COMMENT ON COLUMN images.deleted_at IS '软删除时间；空表示未删除';
COMMENT ON COLUMN images.create_by IS '创建用户；系统任务为空';
COMMENT ON COLUMN images.update_by IS '最后修改用户；系统任务为空';
CREATE UNIQUE INDEX uq_images_1 ON images (tenant_id, project_id, object_key) WHERE deleted_at IS NULL;
CREATE INDEX ix_images_scope ON images (tenant_id, project_id, created_at DESC) WHERE deleted_at IS NULL;
CREATE INDEX ix_images_deleted ON images (deleted_at) WHERE deleted_at IS NOT NULL;
CREATE INDEX ix_images_create_by ON images (create_by) WHERE create_by IS NOT NULL;
CREATE INDEX ix_images_update_by ON images (update_by) WHERE update_by IS NOT NULL;
CREATE TRIGGER trg_images_updated BEFORE UPDATE ON images FOR EACH ROW EXECUTE FUNCTION bmm_touch_updated_at();

CREATE TABLE image_templates (
    id bigserial PRIMARY KEY,
    tenant_id bigint NOT NULL REFERENCES tenants(id),
    project_id bigint NOT NULL,
    name text NOT NULL,
    image_id bigint NOT NULL,
    config jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz,
    create_by bigint REFERENCES users(id),
    update_by bigint REFERENCES users(id),
    UNIQUE (tenant_id, project_id, id),
    FOREIGN KEY (tenant_id, project_id) REFERENCES projects(tenant_id, id),
    FOREIGN KEY (tenant_id, project_id, image_id) REFERENCES images(tenant_id, project_id, id)
);
COMMENT ON TABLE image_templates IS '镜像安装模板';
COMMENT ON COLUMN image_templates.id IS '行主键';
COMMENT ON COLUMN image_templates.tenant_id IS '所属租户';
COMMENT ON COLUMN image_templates.project_id IS '所属项目';
COMMENT ON COLUMN image_templates.name IS '模板名称';
COMMENT ON COLUMN image_templates.image_id IS '镜像';
COMMENT ON COLUMN image_templates.config IS '非敏感安装默认参数';
COMMENT ON COLUMN image_templates.created_at IS '创建时间';
COMMENT ON COLUMN image_templates.updated_at IS '更新时间；触发器自动维护';
COMMENT ON COLUMN image_templates.deleted_at IS '软删除时间；空表示未删除';
COMMENT ON COLUMN image_templates.create_by IS '创建用户；系统任务为空';
COMMENT ON COLUMN image_templates.update_by IS '最后修改用户；系统任务为空';
CREATE UNIQUE INDEX uq_image_templates_1 ON image_templates (tenant_id, project_id, name) WHERE deleted_at IS NULL;
CREATE INDEX ix_image_templates_scope ON image_templates (tenant_id, project_id, created_at DESC) WHERE deleted_at IS NULL;
CREATE INDEX ix_image_templates_deleted ON image_templates (deleted_at) WHERE deleted_at IS NOT NULL;
CREATE INDEX ix_image_templates_image_id ON image_templates (tenant_id, project_id, image_id);
CREATE INDEX ix_image_templates_create_by ON image_templates (create_by) WHERE create_by IS NOT NULL;
CREATE INDEX ix_image_templates_update_by ON image_templates (update_by) WHERE update_by IS NOT NULL;
CREATE TRIGGER trg_image_templates_updated BEFORE UPDATE ON image_templates FOR EACH ROW EXECUTE FUNCTION bmm_touch_updated_at();

CREATE TABLE kickstart_templates (
    id bigserial PRIMARY KEY,
    tenant_id bigint NOT NULL REFERENCES tenants(id),
    project_id bigint NOT NULL,
    name text NOT NULL,
    format text NOT NULL CHECK (format IN ('kickstart','autoinstall','preseed','unattend')),
    content text NOT NULL,
    revision integer NOT NULL DEFAULT 1 CHECK (revision > 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz,
    create_by bigint REFERENCES users(id),
    update_by bigint REFERENCES users(id),
    UNIQUE (tenant_id, project_id, id),
    FOREIGN KEY (tenant_id, project_id) REFERENCES projects(tenant_id, id)
);
COMMENT ON TABLE kickstart_templates IS '无人值守应答模板；支持多种操作系统格式';
COMMENT ON COLUMN kickstart_templates.id IS '行主键';
COMMENT ON COLUMN kickstart_templates.tenant_id IS '所属租户';
COMMENT ON COLUMN kickstart_templates.project_id IS '所属项目';
COMMENT ON COLUMN kickstart_templates.name IS '模板名称';
COMMENT ON COLUMN kickstart_templates.format IS '应答文件格式';
COMMENT ON COLUMN kickstart_templates.content IS '模板正文；使用凭据引用，不嵌入明文密码';
COMMENT ON COLUMN kickstart_templates.revision IS '模板修订号';
COMMENT ON COLUMN kickstart_templates.created_at IS '创建时间';
COMMENT ON COLUMN kickstart_templates.updated_at IS '更新时间；触发器自动维护';
COMMENT ON COLUMN kickstart_templates.deleted_at IS '软删除时间；空表示未删除';
COMMENT ON COLUMN kickstart_templates.create_by IS '创建用户；系统任务为空';
COMMENT ON COLUMN kickstart_templates.update_by IS '最后修改用户；系统任务为空';
CREATE UNIQUE INDEX uq_kickstart_templates_1 ON kickstart_templates (tenant_id, project_id, name, revision) WHERE deleted_at IS NULL;
CREATE INDEX ix_kickstart_templates_scope ON kickstart_templates (tenant_id, project_id, created_at DESC) WHERE deleted_at IS NULL;
CREATE INDEX ix_kickstart_templates_deleted ON kickstart_templates (deleted_at) WHERE deleted_at IS NOT NULL;
CREATE INDEX ix_kickstart_templates_create_by ON kickstart_templates (create_by) WHERE create_by IS NOT NULL;
CREATE INDEX ix_kickstart_templates_update_by ON kickstart_templates (update_by) WHERE update_by IS NOT NULL;
CREATE TRIGGER trg_kickstart_templates_updated BEFORE UPDATE ON kickstart_templates FOR EACH ROW EXECUTE FUNCTION bmm_touch_updated_at();

CREATE TABLE partition_templates (
    id bigserial PRIMARY KEY,
    tenant_id bigint NOT NULL REFERENCES tenants(id),
    project_id bigint NOT NULL,
    name text NOT NULL,
    layout jsonb NOT NULL CHECK (jsonb_typeof(layout) = 'object'),
    revision integer NOT NULL DEFAULT 1 CHECK (revision > 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz,
    create_by bigint REFERENCES users(id),
    update_by bigint REFERENCES users(id),
    UNIQUE (tenant_id, project_id, id),
    FOREIGN KEY (tenant_id, project_id) REFERENCES projects(tenant_id, id)
);
COMMENT ON TABLE partition_templates IS '磁盘分区模板；执行前须确认目标盘及数据擦除';
COMMENT ON COLUMN partition_templates.id IS '行主键';
COMMENT ON COLUMN partition_templates.tenant_id IS '所属租户';
COMMENT ON COLUMN partition_templates.project_id IS '所属项目';
COMMENT ON COLUMN partition_templates.name IS '模板名称';
COMMENT ON COLUMN partition_templates.layout IS '磁盘选择器与分区结构';
COMMENT ON COLUMN partition_templates.revision IS '修订号';
COMMENT ON COLUMN partition_templates.created_at IS '创建时间';
COMMENT ON COLUMN partition_templates.updated_at IS '更新时间；触发器自动维护';
COMMENT ON COLUMN partition_templates.deleted_at IS '软删除时间；空表示未删除';
COMMENT ON COLUMN partition_templates.create_by IS '创建用户；系统任务为空';
COMMENT ON COLUMN partition_templates.update_by IS '最后修改用户；系统任务为空';
CREATE UNIQUE INDEX uq_partition_templates_1 ON partition_templates (tenant_id, project_id, name, revision) WHERE deleted_at IS NULL;
CREATE INDEX ix_partition_templates_scope ON partition_templates (tenant_id, project_id, created_at DESC) WHERE deleted_at IS NULL;
CREATE INDEX ix_partition_templates_deleted ON partition_templates (deleted_at) WHERE deleted_at IS NOT NULL;
CREATE INDEX ix_partition_templates_create_by ON partition_templates (create_by) WHERE create_by IS NOT NULL;
CREATE INDEX ix_partition_templates_update_by ON partition_templates (update_by) WHERE update_by IS NOT NULL;
CREATE TRIGGER trg_partition_templates_updated BEFORE UPDATE ON partition_templates FOR EACH ROW EXECUTE FUNCTION bmm_touch_updated_at();

CREATE TABLE post_install_scripts (
    id bigserial PRIMARY KEY,
    tenant_id bigint NOT NULL REFERENCES tenants(id),
    project_id bigint NOT NULL,
    name text NOT NULL,
    interpreter text NOT NULL CHECK (interpreter IN ('sh','bash','powershell','python3')),
    content text NOT NULL,
    sha256 text NOT NULL CHECK (sha256 ~ '^[0-9a-f]{64}$'),
    timeout_seconds integer NOT NULL DEFAULT 300 CHECK (timeout_seconds > 0),
    revision integer NOT NULL DEFAULT 1 CHECK (revision > 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz,
    create_by bigint REFERENCES users(id),
    update_by bigint REFERENCES users(id),
    UNIQUE (tenant_id, project_id, id),
    FOREIGN KEY (tenant_id, project_id) REFERENCES projects(tenant_id, id)
);
COMMENT ON TABLE post_install_scripts IS '装机后脚本；只允许经授权的受控脚本';
COMMENT ON COLUMN post_install_scripts.id IS '行主键';
COMMENT ON COLUMN post_install_scripts.tenant_id IS '所属租户';
COMMENT ON COLUMN post_install_scripts.project_id IS '所属项目';
COMMENT ON COLUMN post_install_scripts.name IS '脚本名称';
COMMENT ON COLUMN post_install_scripts.interpreter IS '解释器';
COMMENT ON COLUMN post_install_scripts.content IS '脚本正文；不得包含明文凭据';
COMMENT ON COLUMN post_install_scripts.sha256 IS '脚本校验值';
COMMENT ON COLUMN post_install_scripts.timeout_seconds IS '执行超时';
COMMENT ON COLUMN post_install_scripts.revision IS '修订号';
COMMENT ON COLUMN post_install_scripts.created_at IS '创建时间';
COMMENT ON COLUMN post_install_scripts.updated_at IS '更新时间；触发器自动维护';
COMMENT ON COLUMN post_install_scripts.deleted_at IS '软删除时间；空表示未删除';
COMMENT ON COLUMN post_install_scripts.create_by IS '创建用户；系统任务为空';
COMMENT ON COLUMN post_install_scripts.update_by IS '最后修改用户；系统任务为空';
CREATE UNIQUE INDEX uq_post_install_scripts_1 ON post_install_scripts (tenant_id, project_id, name, revision) WHERE deleted_at IS NULL;
CREATE INDEX ix_post_install_scripts_scope ON post_install_scripts (tenant_id, project_id, created_at DESC) WHERE deleted_at IS NULL;
CREATE INDEX ix_post_install_scripts_deleted ON post_install_scripts (deleted_at) WHERE deleted_at IS NOT NULL;
CREATE INDEX ix_post_install_scripts_create_by ON post_install_scripts (create_by) WHERE create_by IS NOT NULL;
CREATE INDEX ix_post_install_scripts_update_by ON post_install_scripts (update_by) WHERE update_by IS NOT NULL;
CREATE TRIGGER trg_post_install_scripts_updated BEFORE UPDATE ON post_install_scripts FOR EACH ROW EXECUTE FUNCTION bmm_touch_updated_at();

CREATE TABLE install_plans (
    id bigserial PRIMARY KEY,
    tenant_id bigint NOT NULL REFERENCES tenants(id),
    project_id bigint NOT NULL,
    name text NOT NULL,
    image_template_id bigint NOT NULL,
    kickstart_template_id bigint NOT NULL,
    partition_template_id bigint NOT NULL,
    network_id bigint,
    config jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz,
    create_by bigint REFERENCES users(id),
    update_by bigint REFERENCES users(id),
    UNIQUE (tenant_id, project_id, id),
    FOREIGN KEY (tenant_id, project_id) REFERENCES projects(tenant_id, id),
    FOREIGN KEY (tenant_id, project_id, image_template_id) REFERENCES image_templates(tenant_id, project_id, id),
    FOREIGN KEY (tenant_id, project_id, kickstart_template_id) REFERENCES kickstart_templates(tenant_id, project_id, id),
    FOREIGN KEY (tenant_id, project_id, partition_template_id) REFERENCES partition_templates(tenant_id, project_id, id),
    FOREIGN KEY (tenant_id, project_id, network_id) REFERENCES networks(tenant_id, project_id, id)
);
COMMENT ON TABLE install_plans IS '装机计划；执行时复制完整快照以免模板变更影响在途任务';
COMMENT ON COLUMN install_plans.id IS '行主键';
COMMENT ON COLUMN install_plans.tenant_id IS '所属租户';
COMMENT ON COLUMN install_plans.project_id IS '所属项目';
COMMENT ON COLUMN install_plans.name IS '计划名称';
COMMENT ON COLUMN install_plans.image_template_id IS '镜像模板';
COMMENT ON COLUMN install_plans.kickstart_template_id IS '应答模板';
COMMENT ON COLUMN install_plans.partition_template_id IS '分区模板';
COMMENT ON COLUMN install_plans.network_id IS '目标网络';
COMMENT ON COLUMN install_plans.config IS '非敏感安装参数';
COMMENT ON COLUMN install_plans.created_at IS '创建时间';
COMMENT ON COLUMN install_plans.updated_at IS '更新时间；触发器自动维护';
COMMENT ON COLUMN install_plans.deleted_at IS '软删除时间；空表示未删除';
COMMENT ON COLUMN install_plans.create_by IS '创建用户；系统任务为空';
COMMENT ON COLUMN install_plans.update_by IS '最后修改用户；系统任务为空';
CREATE UNIQUE INDEX uq_install_plans_1 ON install_plans (tenant_id, project_id, name) WHERE deleted_at IS NULL;
CREATE INDEX ix_install_plans_scope ON install_plans (tenant_id, project_id, created_at DESC) WHERE deleted_at IS NULL;
CREATE INDEX ix_install_plans_deleted ON install_plans (deleted_at) WHERE deleted_at IS NOT NULL;
CREATE INDEX ix_install_plans_image_template_id ON install_plans (tenant_id, project_id, image_template_id);
CREATE INDEX ix_install_plans_kickstart_template_id ON install_plans (tenant_id, project_id, kickstart_template_id);
CREATE INDEX ix_install_plans_partition_template_id ON install_plans (tenant_id, project_id, partition_template_id);
CREATE INDEX ix_install_plans_network_id ON install_plans (tenant_id, project_id, network_id);
CREATE INDEX ix_install_plans_create_by ON install_plans (create_by) WHERE create_by IS NOT NULL;
CREATE INDEX ix_install_plans_update_by ON install_plans (update_by) WHERE update_by IS NOT NULL;
CREATE TRIGGER trg_install_plans_updated BEFORE UPDATE ON install_plans FOR EACH ROW EXECUTE FUNCTION bmm_touch_updated_at();

CREATE TABLE install_plan_scripts (
    id bigserial PRIMARY KEY,
    tenant_id bigint NOT NULL REFERENCES tenants(id),
    project_id bigint NOT NULL,
    install_plan_id bigint NOT NULL,
    script_id bigint NOT NULL,
    sequence integer NOT NULL CHECK (sequence >= 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz,
    create_by bigint REFERENCES users(id),
    update_by bigint REFERENCES users(id),
    UNIQUE (tenant_id, project_id, id),
    FOREIGN KEY (tenant_id, project_id) REFERENCES projects(tenant_id, id),
    FOREIGN KEY (tenant_id, project_id, install_plan_id) REFERENCES install_plans(tenant_id, project_id, id),
    FOREIGN KEY (tenant_id, project_id, script_id) REFERENCES post_install_scripts(tenant_id, project_id, id)
);
COMMENT ON TABLE install_plan_scripts IS '计划脚本顺序关联';
COMMENT ON COLUMN install_plan_scripts.id IS '行主键';
COMMENT ON COLUMN install_plan_scripts.tenant_id IS '所属租户';
COMMENT ON COLUMN install_plan_scripts.project_id IS '所属项目';
COMMENT ON COLUMN install_plan_scripts.install_plan_id IS '装机计划';
COMMENT ON COLUMN install_plan_scripts.script_id IS '脚本';
COMMENT ON COLUMN install_plan_scripts.sequence IS '执行顺序';
COMMENT ON COLUMN install_plan_scripts.created_at IS '创建时间';
COMMENT ON COLUMN install_plan_scripts.updated_at IS '更新时间；触发器自动维护';
COMMENT ON COLUMN install_plan_scripts.deleted_at IS '软删除时间；空表示未删除';
COMMENT ON COLUMN install_plan_scripts.create_by IS '创建用户；系统任务为空';
COMMENT ON COLUMN install_plan_scripts.update_by IS '最后修改用户；系统任务为空';
CREATE UNIQUE INDEX uq_install_plan_scripts_1 ON install_plan_scripts (tenant_id, project_id, install_plan_id, sequence) WHERE deleted_at IS NULL;
CREATE UNIQUE INDEX uq_install_plan_scripts_2 ON install_plan_scripts (tenant_id, project_id, install_plan_id, script_id) WHERE deleted_at IS NULL;
CREATE INDEX ix_install_plan_scripts_scope ON install_plan_scripts (tenant_id, project_id, created_at DESC) WHERE deleted_at IS NULL;
CREATE INDEX ix_install_plan_scripts_deleted ON install_plan_scripts (deleted_at) WHERE deleted_at IS NOT NULL;
CREATE INDEX ix_install_plan_scripts_install_plan_id ON install_plan_scripts (tenant_id, project_id, install_plan_id);
CREATE INDEX ix_install_plan_scripts_script_id ON install_plan_scripts (tenant_id, project_id, script_id);
CREATE INDEX ix_install_plan_scripts_create_by ON install_plan_scripts (create_by) WHERE create_by IS NOT NULL;
CREATE INDEX ix_install_plan_scripts_update_by ON install_plan_scripts (update_by) WHERE update_by IS NOT NULL;
CREATE TRIGGER trg_install_plan_scripts_updated BEFORE UPDATE ON install_plan_scripts FOR EACH ROW EXECUTE FUNCTION bmm_touch_updated_at();

CREATE TABLE installations (
    id bigserial PRIMARY KEY,
    tenant_id bigint NOT NULL REFERENCES tenants(id),
    project_id bigint NOT NULL,
    asset_id bigint NOT NULL,
    operation_id bigint NOT NULL,
    install_plan_id bigint NOT NULL,
    status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','preparing','booting','installing','post_install','succeeded','failed','cancelled','unknown')),
    config_snapshot jsonb NOT NULL,
    callback_token_hash text NOT NULL,
    callback_expires_at timestamptz NOT NULL,
    callback_revoked_at timestamptz,
    confirmed_by bigint NOT NULL REFERENCES users(id),
    confirmed_at timestamptz NOT NULL,
    irreversible_at timestamptz,
    started_at timestamptz,
    completed_at timestamptz,
    error text,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz,
    create_by bigint REFERENCES users(id),
    update_by bigint REFERENCES users(id),
    UNIQUE (tenant_id, project_id, id),
    FOREIGN KEY (tenant_id, project_id) REFERENCES projects(tenant_id, id),
    CHECK (irreversible_at IS NULL OR irreversible_at >= confirmed_at),
    CHECK (completed_at IS NULL OR (started_at IS NOT NULL AND completed_at >= started_at)),
    FOREIGN KEY (tenant_id, project_id, asset_id) REFERENCES assets(tenant_id, project_id, id),
    FOREIGN KEY (tenant_id, project_id, operation_id) REFERENCES operations(tenant_id, project_id, id),
    FOREIGN KEY (tenant_id, project_id, install_plan_id) REFERENCES install_plans(tenant_id, project_id, id)
);
COMMENT ON TABLE installations IS '单设备装机实例；擦盘后不得伪装为可回滚任务';
COMMENT ON COLUMN installations.id IS '行主键';
COMMENT ON COLUMN installations.tenant_id IS '所属租户';
COMMENT ON COLUMN installations.project_id IS '所属项目';
COMMENT ON COLUMN installations.asset_id IS '目标设备';
COMMENT ON COLUMN installations.operation_id IS '执行任务';
COMMENT ON COLUMN installations.install_plan_id IS '来源计划';
COMMENT ON COLUMN installations.status IS '安装状态';
COMMENT ON COLUMN installations.config_snapshot IS '执行时的计划、模板与网络脱敏快照';
COMMENT ON COLUMN installations.callback_token_hash IS '一次性回调凭据哈希';
COMMENT ON COLUMN installations.callback_expires_at IS '回调凭据到期时间';
COMMENT ON COLUMN installations.callback_revoked_at IS '回调吊销时间';
COMMENT ON COLUMN installations.confirmed_by IS '数据擦除确认人';
COMMENT ON COLUMN installations.confirmed_at IS '数据擦除授权时间';
COMMENT ON COLUMN installations.irreversible_at IS '实际进入擦盘等不可逆步骤的时间';
COMMENT ON COLUMN installations.started_at IS '开始时间';
COMMENT ON COLUMN installations.completed_at IS '结束时间';
COMMENT ON COLUMN installations.error IS '脱敏错误';
COMMENT ON COLUMN installations.created_at IS '创建时间';
COMMENT ON COLUMN installations.updated_at IS '更新时间；触发器自动维护';
COMMENT ON COLUMN installations.deleted_at IS '软删除时间；空表示未删除';
COMMENT ON COLUMN installations.create_by IS '创建用户；系统任务为空';
COMMENT ON COLUMN installations.update_by IS '最后修改用户；系统任务为空';
CREATE UNIQUE INDEX uq_installations_1 ON installations (tenant_id, project_id, operation_id) WHERE deleted_at IS NULL;
CREATE UNIQUE INDEX uq_installations_2 ON installations (tenant_id, project_id, callback_token_hash) WHERE deleted_at IS NULL;
CREATE INDEX ix_installations_scope ON installations (tenant_id, project_id, created_at DESC) WHERE deleted_at IS NULL;
CREATE INDEX ix_installations_deleted ON installations (deleted_at) WHERE deleted_at IS NOT NULL;
CREATE INDEX ix_installations_asset_id ON installations (tenant_id, project_id, asset_id);
CREATE INDEX ix_installations_operation_id ON installations (tenant_id, project_id, operation_id);
CREATE INDEX ix_installations_install_plan_id ON installations (tenant_id, project_id, install_plan_id);
CREATE INDEX ix_installations_create_by ON installations (create_by) WHERE create_by IS NOT NULL;
CREATE INDEX ix_installations_update_by ON installations (update_by) WHERE update_by IS NOT NULL;
CREATE TRIGGER trg_installations_updated BEFORE UPDATE ON installations FOR EACH ROW EXECUTE FUNCTION bmm_touch_updated_at();

CREATE TABLE installation_events (
    id bigserial PRIMARY KEY,
    tenant_id bigint NOT NULL REFERENCES tenants(id),
    project_id bigint NOT NULL,
    installation_id bigint NOT NULL,
    event_key text NOT NULL,
    stage text NOT NULL,
    message text NOT NULL,
    occurred_at timestamptz NOT NULL DEFAULT now(),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz,
    create_by bigint REFERENCES users(id),
    update_by bigint REFERENCES users(id),
    UNIQUE (tenant_id, project_id, id),
    FOREIGN KEY (tenant_id, project_id) REFERENCES projects(tenant_id, id),
    FOREIGN KEY (tenant_id, project_id, installation_id) REFERENCES installations(tenant_id, project_id, id)
);
COMMENT ON TABLE installation_events IS '装机进度事件；回调按实例及事件键幂等';
COMMENT ON COLUMN installation_events.id IS '行主键';
COMMENT ON COLUMN installation_events.tenant_id IS '所属租户';
COMMENT ON COLUMN installation_events.project_id IS '所属项目';
COMMENT ON COLUMN installation_events.installation_id IS '装机实例';
COMMENT ON COLUMN installation_events.event_key IS '客户端事件幂等键';
COMMENT ON COLUMN installation_events.stage IS '安装阶段';
COMMENT ON COLUMN installation_events.message IS '脱敏事件内容';
COMMENT ON COLUMN installation_events.occurred_at IS '发生时间';
COMMENT ON COLUMN installation_events.created_at IS '创建时间';
COMMENT ON COLUMN installation_events.updated_at IS '更新时间；触发器自动维护';
COMMENT ON COLUMN installation_events.deleted_at IS '软删除时间；空表示未删除';
COMMENT ON COLUMN installation_events.create_by IS '创建用户；系统任务为空';
COMMENT ON COLUMN installation_events.update_by IS '最后修改用户；系统任务为空';
CREATE UNIQUE INDEX uq_installation_events_1 ON installation_events (tenant_id, project_id, installation_id, event_key) WHERE deleted_at IS NULL;
CREATE INDEX ix_installation_events_scope ON installation_events (tenant_id, project_id, created_at DESC) WHERE deleted_at IS NULL;
CREATE INDEX ix_installation_events_deleted ON installation_events (deleted_at) WHERE deleted_at IS NOT NULL;
CREATE INDEX ix_installation_events_installation_id ON installation_events (tenant_id, project_id, installation_id);
CREATE INDEX ix_installation_events_create_by ON installation_events (create_by) WHERE create_by IS NOT NULL;
CREATE INDEX ix_installation_events_update_by ON installation_events (update_by) WHERE update_by IS NOT NULL;
CREATE TRIGGER trg_installation_events_updated BEFORE UPDATE ON installation_events FOR EACH ROW EXECUTE FUNCTION bmm_touch_updated_at();

CREATE TABLE sensor_records (
    id bigserial PRIMARY KEY,
    tenant_id bigint NOT NULL REFERENCES tenants(id),
    project_id bigint NOT NULL,
    asset_id bigint NOT NULL,
    sensor_key text NOT NULL,
    metric text NOT NULL,
    value double precision,
    unit text,
    quality text NOT NULL CHECK (quality IN ('good','failed','missing','unsupported')),
    error text,
    sampled_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz,
    create_by bigint REFERENCES users(id),
    update_by bigint REFERENCES users(id),
    UNIQUE (tenant_id, project_id, id),
    FOREIGN KEY (tenant_id, project_id) REFERENCES projects(tenant_id, id),
    CHECK ((quality = 'good' AND value IS NOT NULL AND value > '-Infinity'::float8 AND value < 'Infinity'::float8) OR (quality <> 'good' AND value IS NULL)),
    FOREIGN KEY (tenant_id, project_id, asset_id) REFERENCES assets(tenant_id, project_id, id)
);
COMMENT ON TABLE sensor_records IS '传感器历史；采集失败与真实数值必须区分；后续按容量制定保留策略';
COMMENT ON COLUMN sensor_records.id IS '行主键';
COMMENT ON COLUMN sensor_records.tenant_id IS '所属租户';
COMMENT ON COLUMN sensor_records.project_id IS '所属项目';
COMMENT ON COLUMN sensor_records.asset_id IS '资产';
COMMENT ON COLUMN sensor_records.sensor_key IS '稳定传感器标识';
COMMENT ON COLUMN sensor_records.metric IS '指标名称';
COMMENT ON COLUMN sensor_records.value IS '指标值；失败或缺失时为空';
COMMENT ON COLUMN sensor_records.unit IS '计量单位';
COMMENT ON COLUMN sensor_records.quality IS '采集质量';
COMMENT ON COLUMN sensor_records.error IS '脱敏采集错误';
COMMENT ON COLUMN sensor_records.sampled_at IS '采样时间';
COMMENT ON COLUMN sensor_records.created_at IS '创建时间';
COMMENT ON COLUMN sensor_records.updated_at IS '更新时间；触发器自动维护';
COMMENT ON COLUMN sensor_records.deleted_at IS '软删除时间；空表示未删除';
COMMENT ON COLUMN sensor_records.create_by IS '创建用户；系统任务为空';
COMMENT ON COLUMN sensor_records.update_by IS '最后修改用户；系统任务为空';
CREATE INDEX ix_sensor_records_scope ON sensor_records (tenant_id, project_id, created_at DESC) WHERE deleted_at IS NULL;
CREATE INDEX ix_sensor_records_deleted ON sensor_records (deleted_at) WHERE deleted_at IS NOT NULL;
CREATE INDEX ix_sensor_records_asset_id ON sensor_records (tenant_id, project_id, asset_id);
CREATE INDEX ix_sensor_records_create_by ON sensor_records (create_by) WHERE create_by IS NOT NULL;
CREATE INDEX ix_sensor_records_update_by ON sensor_records (update_by) WHERE update_by IS NOT NULL;
CREATE TRIGGER trg_sensor_records_updated BEFORE UPDATE ON sensor_records FOR EACH ROW EXECUTE FUNCTION bmm_touch_updated_at();

CREATE TABLE asset_monitor_configs (
    id bigserial PRIMARY KEY,
    tenant_id bigint NOT NULL REFERENCES tenants(id),
    project_id bigint NOT NULL,
    asset_id bigint NOT NULL,
    enabled boolean NOT NULL DEFAULT true,
    interval_seconds integer NOT NULL DEFAULT 60 CHECK (interval_seconds > 0),
    timeout_seconds integer NOT NULL DEFAULT 15 CHECK (timeout_seconds > 0),
    metrics jsonb NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(metrics) = 'array'),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz,
    create_by bigint REFERENCES users(id),
    update_by bigint REFERENCES users(id),
    UNIQUE (tenant_id, project_id, id),
    FOREIGN KEY (tenant_id, project_id) REFERENCES projects(tenant_id, id),
    CHECK (timeout_seconds < interval_seconds),
    FOREIGN KEY (tenant_id, project_id, asset_id) REFERENCES assets(tenant_id, project_id, id)
);
COMMENT ON TABLE asset_monitor_configs IS '设备监控配置';
COMMENT ON COLUMN asset_monitor_configs.id IS '行主键';
COMMENT ON COLUMN asset_monitor_configs.tenant_id IS '所属租户';
COMMENT ON COLUMN asset_monitor_configs.project_id IS '所属项目';
COMMENT ON COLUMN asset_monitor_configs.asset_id IS '资产';
COMMENT ON COLUMN asset_monitor_configs.enabled IS '监控开关';
COMMENT ON COLUMN asset_monitor_configs.interval_seconds IS '采集间隔秒数';
COMMENT ON COLUMN asset_monitor_configs.timeout_seconds IS '采集超时秒数';
COMMENT ON COLUMN asset_monitor_configs.metrics IS '采集指标列表';
COMMENT ON COLUMN asset_monitor_configs.created_at IS '创建时间';
COMMENT ON COLUMN asset_monitor_configs.updated_at IS '更新时间；触发器自动维护';
COMMENT ON COLUMN asset_monitor_configs.deleted_at IS '软删除时间；空表示未删除';
COMMENT ON COLUMN asset_monitor_configs.create_by IS '创建用户；系统任务为空';
COMMENT ON COLUMN asset_monitor_configs.update_by IS '最后修改用户；系统任务为空';
CREATE UNIQUE INDEX uq_asset_monitor_configs_1 ON asset_monitor_configs (tenant_id, project_id, asset_id) WHERE deleted_at IS NULL;
CREATE INDEX ix_asset_monitor_configs_scope ON asset_monitor_configs (tenant_id, project_id, created_at DESC) WHERE deleted_at IS NULL;
CREATE INDEX ix_asset_monitor_configs_deleted ON asset_monitor_configs (deleted_at) WHERE deleted_at IS NOT NULL;
CREATE INDEX ix_asset_monitor_configs_asset_id ON asset_monitor_configs (tenant_id, project_id, asset_id);
CREATE INDEX ix_asset_monitor_configs_create_by ON asset_monitor_configs (create_by) WHERE create_by IS NOT NULL;
CREATE INDEX ix_asset_monitor_configs_update_by ON asset_monitor_configs (update_by) WHERE update_by IS NOT NULL;
CREATE TRIGGER trg_asset_monitor_configs_updated BEFORE UPDATE ON asset_monitor_configs FOR EACH ROW EXECUTE FUNCTION bmm_touch_updated_at();

CREATE TABLE alert_rules (
    id bigserial PRIMARY KEY,
    tenant_id bigint NOT NULL REFERENCES tenants(id),
    project_id bigint NOT NULL,
    name text NOT NULL,
    metric text NOT NULL,
    operator text NOT NULL CHECK (operator IN ('gt','gte','lt','lte','eq','ne','missing','collection_failed')),
    threshold double precision,
    duration_seconds integer NOT NULL DEFAULT 0 CHECK (duration_seconds >= 0),
    severity text NOT NULL CHECK (severity IN ('info','warning','critical')),
    enabled boolean NOT NULL DEFAULT true,
    selector jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz,
    create_by bigint REFERENCES users(id),
    update_by bigint REFERENCES users(id),
    UNIQUE (tenant_id, project_id, id),
    FOREIGN KEY (tenant_id, project_id) REFERENCES projects(tenant_id, id),
    CHECK ((operator IN ('missing','collection_failed') AND threshold IS NULL) OR (operator NOT IN ('missing','collection_failed') AND threshold IS NOT NULL AND threshold > '-Infinity'::float8 AND threshold < 'Infinity'::float8))
);
COMMENT ON TABLE alert_rules IS '指标告警规则；连续失败规则不能用虚假零值代替采集失败';
COMMENT ON COLUMN alert_rules.id IS '行主键';
COMMENT ON COLUMN alert_rules.tenant_id IS '所属租户';
COMMENT ON COLUMN alert_rules.project_id IS '所属项目';
COMMENT ON COLUMN alert_rules.name IS '规则名称';
COMMENT ON COLUMN alert_rules.metric IS '指标名称';
COMMENT ON COLUMN alert_rules.operator IS '判定操作符';
COMMENT ON COLUMN alert_rules.threshold IS '阈值；缺失和失败规则为空';
COMMENT ON COLUMN alert_rules.duration_seconds IS '持续触发时长';
COMMENT ON COLUMN alert_rules.severity IS '严重级别';
COMMENT ON COLUMN alert_rules.enabled IS '规则开关';
COMMENT ON COLUMN alert_rules.selector IS '资产标签选择器';
COMMENT ON COLUMN alert_rules.created_at IS '创建时间';
COMMENT ON COLUMN alert_rules.updated_at IS '更新时间；触发器自动维护';
COMMENT ON COLUMN alert_rules.deleted_at IS '软删除时间；空表示未删除';
COMMENT ON COLUMN alert_rules.create_by IS '创建用户；系统任务为空';
COMMENT ON COLUMN alert_rules.update_by IS '最后修改用户；系统任务为空';
CREATE UNIQUE INDEX uq_alert_rules_1 ON alert_rules (tenant_id, project_id, name) WHERE deleted_at IS NULL;
CREATE INDEX ix_alert_rules_scope ON alert_rules (tenant_id, project_id, created_at DESC) WHERE deleted_at IS NULL;
CREATE INDEX ix_alert_rules_deleted ON alert_rules (deleted_at) WHERE deleted_at IS NOT NULL;
CREATE INDEX ix_alert_rules_create_by ON alert_rules (create_by) WHERE create_by IS NOT NULL;
CREATE INDEX ix_alert_rules_update_by ON alert_rules (update_by) WHERE update_by IS NOT NULL;
CREATE TRIGGER trg_alert_rules_updated BEFORE UPDATE ON alert_rules FOR EACH ROW EXECUTE FUNCTION bmm_touch_updated_at();

CREATE TABLE alert_channels (
    id bigserial PRIMARY KEY,
    tenant_id bigint NOT NULL REFERENCES tenants(id),
    project_id bigint NOT NULL,
    name text NOT NULL,
    kind text NOT NULL CHECK (kind IN ('email','webhook','lark')),
    config jsonb NOT NULL DEFAULT '{}'::jsonb,
    secret_ciphertext bytea,
    secret_key_id text,
    enabled boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz,
    create_by bigint REFERENCES users(id),
    update_by bigint REFERENCES users(id),
    UNIQUE (tenant_id, project_id, id),
    FOREIGN KEY (tenant_id, project_id) REFERENCES projects(tenant_id, id),
    CHECK ((secret_ciphertext IS NULL) = (secret_key_id IS NULL))
);
COMMENT ON TABLE alert_channels IS '告警通知渠道；令牌密文存储，发送端须限制目标地址防 SSRF';
COMMENT ON COLUMN alert_channels.id IS '行主键';
COMMENT ON COLUMN alert_channels.tenant_id IS '所属租户';
COMMENT ON COLUMN alert_channels.project_id IS '所属项目';
COMMENT ON COLUMN alert_channels.name IS '渠道名称';
COMMENT ON COLUMN alert_channels.kind IS '渠道类型';
COMMENT ON COLUMN alert_channels.config IS '非敏感配置';
COMMENT ON COLUMN alert_channels.secret_ciphertext IS '认证密文';
COMMENT ON COLUMN alert_channels.secret_key_id IS '密钥版本';
COMMENT ON COLUMN alert_channels.enabled IS '是否启用';
COMMENT ON COLUMN alert_channels.created_at IS '创建时间';
COMMENT ON COLUMN alert_channels.updated_at IS '更新时间；触发器自动维护';
COMMENT ON COLUMN alert_channels.deleted_at IS '软删除时间；空表示未删除';
COMMENT ON COLUMN alert_channels.create_by IS '创建用户；系统任务为空';
COMMENT ON COLUMN alert_channels.update_by IS '最后修改用户；系统任务为空';
CREATE UNIQUE INDEX uq_alert_channels_1 ON alert_channels (tenant_id, project_id, name) WHERE deleted_at IS NULL;
CREATE INDEX ix_alert_channels_scope ON alert_channels (tenant_id, project_id, created_at DESC) WHERE deleted_at IS NULL;
CREATE INDEX ix_alert_channels_deleted ON alert_channels (deleted_at) WHERE deleted_at IS NOT NULL;
CREATE INDEX ix_alert_channels_create_by ON alert_channels (create_by) WHERE create_by IS NOT NULL;
CREATE INDEX ix_alert_channels_update_by ON alert_channels (update_by) WHERE update_by IS NOT NULL;
CREATE TRIGGER trg_alert_channels_updated BEFORE UPDATE ON alert_channels FOR EACH ROW EXECUTE FUNCTION bmm_touch_updated_at();

CREATE TABLE alert_rule_channels (
    id bigserial PRIMARY KEY,
    tenant_id bigint NOT NULL REFERENCES tenants(id),
    project_id bigint NOT NULL,
    rule_id bigint NOT NULL,
    channel_id bigint NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz,
    create_by bigint REFERENCES users(id),
    update_by bigint REFERENCES users(id),
    UNIQUE (tenant_id, project_id, id),
    FOREIGN KEY (tenant_id, project_id) REFERENCES projects(tenant_id, id),
    FOREIGN KEY (tenant_id, project_id, rule_id) REFERENCES alert_rules(tenant_id, project_id, id),
    FOREIGN KEY (tenant_id, project_id, channel_id) REFERENCES alert_channels(tenant_id, project_id, id)
);
COMMENT ON TABLE alert_rule_channels IS '告警规则通知渠道关联';
COMMENT ON COLUMN alert_rule_channels.id IS '行主键';
COMMENT ON COLUMN alert_rule_channels.tenant_id IS '所属租户';
COMMENT ON COLUMN alert_rule_channels.project_id IS '所属项目';
COMMENT ON COLUMN alert_rule_channels.rule_id IS '告警规则';
COMMENT ON COLUMN alert_rule_channels.channel_id IS '通知渠道';
COMMENT ON COLUMN alert_rule_channels.created_at IS '创建时间';
COMMENT ON COLUMN alert_rule_channels.updated_at IS '更新时间；触发器自动维护';
COMMENT ON COLUMN alert_rule_channels.deleted_at IS '软删除时间；空表示未删除';
COMMENT ON COLUMN alert_rule_channels.create_by IS '创建用户；系统任务为空';
COMMENT ON COLUMN alert_rule_channels.update_by IS '最后修改用户；系统任务为空';
CREATE UNIQUE INDEX uq_alert_rule_channels_1 ON alert_rule_channels (tenant_id, project_id, rule_id, channel_id) WHERE deleted_at IS NULL;
CREATE INDEX ix_alert_rule_channels_scope ON alert_rule_channels (tenant_id, project_id, created_at DESC) WHERE deleted_at IS NULL;
CREATE INDEX ix_alert_rule_channels_deleted ON alert_rule_channels (deleted_at) WHERE deleted_at IS NOT NULL;
CREATE INDEX ix_alert_rule_channels_rule_id ON alert_rule_channels (tenant_id, project_id, rule_id);
CREATE INDEX ix_alert_rule_channels_channel_id ON alert_rule_channels (tenant_id, project_id, channel_id);
CREATE INDEX ix_alert_rule_channels_create_by ON alert_rule_channels (create_by) WHERE create_by IS NOT NULL;
CREATE INDEX ix_alert_rule_channels_update_by ON alert_rule_channels (update_by) WHERE update_by IS NOT NULL;
CREATE TRIGGER trg_alert_rule_channels_updated BEFORE UPDATE ON alert_rule_channels FOR EACH ROW EXECUTE FUNCTION bmm_touch_updated_at();

CREATE TABLE maintenance_windows (
    id bigserial PRIMARY KEY,
    tenant_id bigint NOT NULL REFERENCES tenants(id),
    project_id bigint NOT NULL,
    name text NOT NULL,
    starts_at timestamptz NOT NULL,
    ends_at timestamptz NOT NULL,
    reason text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz,
    create_by bigint REFERENCES users(id),
    update_by bigint REFERENCES users(id),
    UNIQUE (tenant_id, project_id, id),
    FOREIGN KEY (tenant_id, project_id) REFERENCES projects(tenant_id, id),
    CHECK (ends_at > starts_at)
);
COMMENT ON TABLE maintenance_windows IS '维护窗口；仅抑制通知，保留采集及告警证据';
COMMENT ON COLUMN maintenance_windows.id IS '行主键';
COMMENT ON COLUMN maintenance_windows.tenant_id IS '所属租户';
COMMENT ON COLUMN maintenance_windows.project_id IS '所属项目';
COMMENT ON COLUMN maintenance_windows.name IS '窗口名称';
COMMENT ON COLUMN maintenance_windows.starts_at IS '开始时间';
COMMENT ON COLUMN maintenance_windows.ends_at IS '结束时间';
COMMENT ON COLUMN maintenance_windows.reason IS '维护原因';
COMMENT ON COLUMN maintenance_windows.created_at IS '创建时间';
COMMENT ON COLUMN maintenance_windows.updated_at IS '更新时间；触发器自动维护';
COMMENT ON COLUMN maintenance_windows.deleted_at IS '软删除时间；空表示未删除';
COMMENT ON COLUMN maintenance_windows.create_by IS '创建用户；系统任务为空';
COMMENT ON COLUMN maintenance_windows.update_by IS '最后修改用户；系统任务为空';
CREATE INDEX ix_maintenance_windows_scope ON maintenance_windows (tenant_id, project_id, created_at DESC) WHERE deleted_at IS NULL;
CREATE INDEX ix_maintenance_windows_deleted ON maintenance_windows (deleted_at) WHERE deleted_at IS NOT NULL;
CREATE INDEX ix_maintenance_windows_create_by ON maintenance_windows (create_by) WHERE create_by IS NOT NULL;
CREATE INDEX ix_maintenance_windows_update_by ON maintenance_windows (update_by) WHERE update_by IS NOT NULL;
CREATE TRIGGER trg_maintenance_windows_updated BEFORE UPDATE ON maintenance_windows FOR EACH ROW EXECUTE FUNCTION bmm_touch_updated_at();

CREATE TABLE maintenance_window_assets (
    id bigserial PRIMARY KEY,
    tenant_id bigint NOT NULL REFERENCES tenants(id),
    project_id bigint NOT NULL,
    window_id bigint NOT NULL,
    asset_id bigint NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz,
    create_by bigint REFERENCES users(id),
    update_by bigint REFERENCES users(id),
    UNIQUE (tenant_id, project_id, id),
    FOREIGN KEY (tenant_id, project_id) REFERENCES projects(tenant_id, id),
    FOREIGN KEY (tenant_id, project_id, window_id) REFERENCES maintenance_windows(tenant_id, project_id, id),
    FOREIGN KEY (tenant_id, project_id, asset_id) REFERENCES assets(tenant_id, project_id, id)
);
COMMENT ON TABLE maintenance_window_assets IS '维护窗口设备范围';
COMMENT ON COLUMN maintenance_window_assets.id IS '行主键';
COMMENT ON COLUMN maintenance_window_assets.tenant_id IS '所属租户';
COMMENT ON COLUMN maintenance_window_assets.project_id IS '所属项目';
COMMENT ON COLUMN maintenance_window_assets.window_id IS '维护窗口';
COMMENT ON COLUMN maintenance_window_assets.asset_id IS '设备';
COMMENT ON COLUMN maintenance_window_assets.created_at IS '创建时间';
COMMENT ON COLUMN maintenance_window_assets.updated_at IS '更新时间；触发器自动维护';
COMMENT ON COLUMN maintenance_window_assets.deleted_at IS '软删除时间；空表示未删除';
COMMENT ON COLUMN maintenance_window_assets.create_by IS '创建用户；系统任务为空';
COMMENT ON COLUMN maintenance_window_assets.update_by IS '最后修改用户；系统任务为空';
CREATE UNIQUE INDEX uq_maintenance_window_assets_1 ON maintenance_window_assets (tenant_id, project_id, window_id, asset_id) WHERE deleted_at IS NULL;
CREATE INDEX ix_maintenance_window_assets_scope ON maintenance_window_assets (tenant_id, project_id, created_at DESC) WHERE deleted_at IS NULL;
CREATE INDEX ix_maintenance_window_assets_deleted ON maintenance_window_assets (deleted_at) WHERE deleted_at IS NOT NULL;
CREATE INDEX ix_maintenance_window_assets_window_id ON maintenance_window_assets (tenant_id, project_id, window_id);
CREATE INDEX ix_maintenance_window_assets_asset_id ON maintenance_window_assets (tenant_id, project_id, asset_id);
CREATE INDEX ix_maintenance_window_assets_create_by ON maintenance_window_assets (create_by) WHERE create_by IS NOT NULL;
CREATE INDEX ix_maintenance_window_assets_update_by ON maintenance_window_assets (update_by) WHERE update_by IS NOT NULL;
CREATE TRIGGER trg_maintenance_window_assets_updated BEFORE UPDATE ON maintenance_window_assets FOR EACH ROW EXECUTE FUNCTION bmm_touch_updated_at();

CREATE TABLE alerts (
    id bigserial PRIMARY KEY,
    tenant_id bigint NOT NULL REFERENCES tenants(id),
    project_id bigint NOT NULL,
    asset_id bigint NOT NULL,
    rule_id bigint,
    dedup_key text NOT NULL,
    severity text NOT NULL CHECK (severity IN ('info','warning','critical')),
    status text NOT NULL DEFAULT 'firing' CHECK (status IN ('firing','acknowledged','resolved')),
    message text NOT NULL,
    first_seen_at timestamptz NOT NULL DEFAULT now(),
    last_seen_at timestamptz NOT NULL DEFAULT now(),
    acknowledged_at timestamptz,
    acknowledged_by bigint REFERENCES users(id),
    resolved_at timestamptz,
    resolved_by bigint REFERENCES users(id),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz,
    create_by bigint REFERENCES users(id),
    update_by bigint REFERENCES users(id),
    UNIQUE (tenant_id, project_id, id),
    FOREIGN KEY (tenant_id, project_id) REFERENCES projects(tenant_id, id),
    CHECK (last_seen_at >= first_seen_at),
    CHECK ((status = 'resolved') = (resolved_at IS NOT NULL)),
    CHECK (status <> 'acknowledged' OR acknowledged_at IS NOT NULL),
    FOREIGN KEY (tenant_id, project_id, asset_id) REFERENCES assets(tenant_id, project_id, id),
    FOREIGN KEY (tenant_id, project_id, rule_id) REFERENCES alert_rules(tenant_id, project_id, id)
);
COMMENT ON TABLE alerts IS '告警实例；同设备活动去重键只允许一条';
COMMENT ON COLUMN alerts.id IS '行主键';
COMMENT ON COLUMN alerts.tenant_id IS '所属租户';
COMMENT ON COLUMN alerts.project_id IS '所属项目';
COMMENT ON COLUMN alerts.asset_id IS '资产';
COMMENT ON COLUMN alerts.rule_id IS '触发规则；手动告警可空';
COMMENT ON COLUMN alerts.dedup_key IS '规则、指标、传感器组合的去重键';
COMMENT ON COLUMN alerts.severity IS '严重级别';
COMMENT ON COLUMN alerts.status IS '告警状态';
COMMENT ON COLUMN alerts.message IS '脱敏消息';
COMMENT ON COLUMN alerts.first_seen_at IS '首次触发时间';
COMMENT ON COLUMN alerts.last_seen_at IS '最近触发时间';
COMMENT ON COLUMN alerts.acknowledged_at IS '确认时间';
COMMENT ON COLUMN alerts.acknowledged_by IS '确认用户';
COMMENT ON COLUMN alerts.resolved_at IS '恢复时间';
COMMENT ON COLUMN alerts.resolved_by IS '恢复用户；系统自动恢复可空';
COMMENT ON COLUMN alerts.created_at IS '创建时间';
COMMENT ON COLUMN alerts.updated_at IS '更新时间；触发器自动维护';
COMMENT ON COLUMN alerts.deleted_at IS '软删除时间；空表示未删除';
COMMENT ON COLUMN alerts.create_by IS '创建用户；系统任务为空';
COMMENT ON COLUMN alerts.update_by IS '最后修改用户；系统任务为空';
CREATE INDEX ix_alerts_scope ON alerts (tenant_id, project_id, created_at DESC) WHERE deleted_at IS NULL;
CREATE INDEX ix_alerts_deleted ON alerts (deleted_at) WHERE deleted_at IS NOT NULL;
CREATE INDEX ix_alerts_asset_id ON alerts (tenant_id, project_id, asset_id);
CREATE INDEX ix_alerts_rule_id ON alerts (tenant_id, project_id, rule_id);
CREATE INDEX ix_alerts_create_by ON alerts (create_by) WHERE create_by IS NOT NULL;
CREATE INDEX ix_alerts_update_by ON alerts (update_by) WHERE update_by IS NOT NULL;
CREATE TRIGGER trg_alerts_updated BEFORE UPDATE ON alerts FOR EACH ROW EXECUTE FUNCTION bmm_touch_updated_at();

CREATE TABLE alert_events (
    id bigserial PRIMARY KEY,
    tenant_id bigint NOT NULL REFERENCES tenants(id),
    project_id bigint NOT NULL,
    alert_id bigint NOT NULL,
    event_type text NOT NULL CHECK (event_type IN ('fired','repeated','acknowledged','resolved','suppressed')),
    message text NOT NULL,
    occurred_at timestamptz NOT NULL DEFAULT now(),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz,
    create_by bigint REFERENCES users(id),
    update_by bigint REFERENCES users(id),
    UNIQUE (tenant_id, project_id, id),
    FOREIGN KEY (tenant_id, project_id) REFERENCES projects(tenant_id, id),
    FOREIGN KEY (tenant_id, project_id, alert_id) REFERENCES alerts(tenant_id, project_id, id)
);
COMMENT ON TABLE alert_events IS '告警状态转换事件';
COMMENT ON COLUMN alert_events.id IS '行主键';
COMMENT ON COLUMN alert_events.tenant_id IS '所属租户';
COMMENT ON COLUMN alert_events.project_id IS '所属项目';
COMMENT ON COLUMN alert_events.alert_id IS '告警';
COMMENT ON COLUMN alert_events.event_type IS '事件类型';
COMMENT ON COLUMN alert_events.message IS '事件内容';
COMMENT ON COLUMN alert_events.occurred_at IS '发生时间';
COMMENT ON COLUMN alert_events.created_at IS '创建时间';
COMMENT ON COLUMN alert_events.updated_at IS '更新时间；触发器自动维护';
COMMENT ON COLUMN alert_events.deleted_at IS '软删除时间；空表示未删除';
COMMENT ON COLUMN alert_events.create_by IS '创建用户；系统任务为空';
COMMENT ON COLUMN alert_events.update_by IS '最后修改用户；系统任务为空';
CREATE INDEX ix_alert_events_scope ON alert_events (tenant_id, project_id, created_at DESC) WHERE deleted_at IS NULL;
CREATE INDEX ix_alert_events_deleted ON alert_events (deleted_at) WHERE deleted_at IS NOT NULL;
CREATE INDEX ix_alert_events_alert_id ON alert_events (tenant_id, project_id, alert_id);
CREATE INDEX ix_alert_events_create_by ON alert_events (create_by) WHERE create_by IS NOT NULL;
CREATE INDEX ix_alert_events_update_by ON alert_events (update_by) WHERE update_by IS NOT NULL;
CREATE TRIGGER trg_alert_events_updated BEFORE UPDATE ON alert_events FOR EACH ROW EXECUTE FUNCTION bmm_touch_updated_at();

CREATE TABLE notification_deliveries (
    id bigserial PRIMARY KEY,
    tenant_id bigint NOT NULL REFERENCES tenants(id),
    project_id bigint NOT NULL,
    alert_event_id bigint NOT NULL,
    channel_id bigint NOT NULL,
    status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','sending','sent','failed','suppressed')),
    attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    next_attempt_at timestamptz NOT NULL DEFAULT now(),
    locked_until timestamptz,
    sent_at timestamptz,
    last_error text,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz,
    create_by bigint REFERENCES users(id),
    update_by bigint REFERENCES users(id),
    UNIQUE (tenant_id, project_id, id),
    FOREIGN KEY (tenant_id, project_id) REFERENCES projects(tenant_id, id),
    FOREIGN KEY (tenant_id, project_id, alert_event_id) REFERENCES alert_events(tenant_id, project_id, id),
    FOREIGN KEY (tenant_id, project_id, channel_id) REFERENCES alert_channels(tenant_id, project_id, id)
);
COMMENT ON TABLE notification_deliveries IS '通知投递与重试记录；外部发送不承诺严格一次';
COMMENT ON COLUMN notification_deliveries.id IS '行主键';
COMMENT ON COLUMN notification_deliveries.tenant_id IS '所属租户';
COMMENT ON COLUMN notification_deliveries.project_id IS '所属项目';
COMMENT ON COLUMN notification_deliveries.alert_event_id IS '告警事件';
COMMENT ON COLUMN notification_deliveries.channel_id IS '通知渠道';
COMMENT ON COLUMN notification_deliveries.status IS '发送状态';
COMMENT ON COLUMN notification_deliveries.attempts IS '尝试次数';
COMMENT ON COLUMN notification_deliveries.next_attempt_at IS '下一次尝试时间';
COMMENT ON COLUMN notification_deliveries.locked_until IS '认领到期时间';
COMMENT ON COLUMN notification_deliveries.sent_at IS '发送时间';
COMMENT ON COLUMN notification_deliveries.last_error IS '脱敏发送错误';
COMMENT ON COLUMN notification_deliveries.created_at IS '创建时间';
COMMENT ON COLUMN notification_deliveries.updated_at IS '更新时间；触发器自动维护';
COMMENT ON COLUMN notification_deliveries.deleted_at IS '软删除时间；空表示未删除';
COMMENT ON COLUMN notification_deliveries.create_by IS '创建用户；系统任务为空';
COMMENT ON COLUMN notification_deliveries.update_by IS '最后修改用户；系统任务为空';
CREATE UNIQUE INDEX uq_notification_deliveries_1 ON notification_deliveries (tenant_id, project_id, alert_event_id, channel_id) WHERE deleted_at IS NULL;
CREATE INDEX ix_notification_deliveries_scope ON notification_deliveries (tenant_id, project_id, created_at DESC) WHERE deleted_at IS NULL;
CREATE INDEX ix_notification_deliveries_deleted ON notification_deliveries (deleted_at) WHERE deleted_at IS NOT NULL;
CREATE INDEX ix_notification_deliveries_alert_event_id ON notification_deliveries (tenant_id, project_id, alert_event_id);
CREATE INDEX ix_notification_deliveries_channel_id ON notification_deliveries (tenant_id, project_id, channel_id);
CREATE INDEX ix_notification_deliveries_create_by ON notification_deliveries (create_by) WHERE create_by IS NOT NULL;
CREATE INDEX ix_notification_deliveries_update_by ON notification_deliveries (update_by) WHERE update_by IS NOT NULL;
CREATE TRIGGER trg_notification_deliveries_updated BEFORE UPDATE ON notification_deliveries FOR EACH ROW EXECUTE FUNCTION bmm_touch_updated_at();

CREATE TABLE firmware_packages (
    id bigserial PRIMARY KEY,
    tenant_id bigint NOT NULL REFERENCES tenants(id),
    project_id bigint NOT NULL,
    name text NOT NULL,
    vendor text NOT NULL,
    component text NOT NULL,
    version text NOT NULL,
    compatible_models jsonb NOT NULL CHECK (jsonb_typeof(compatible_models) = 'array'),
    object_key text NOT NULL,
    size_bytes bigint NOT NULL CHECK (size_bytes > 0),
    sha256 text NOT NULL CHECK (sha256 ~ '^[0-9a-f]{64}$'),
    status text NOT NULL DEFAULT 'verifying' CHECK (status IN ('verifying','ready','rejected')),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz,
    create_by bigint REFERENCES users(id),
    update_by bigint REFERENCES users(id),
    UNIQUE (tenant_id, project_id, id),
    FOREIGN KEY (tenant_id, project_id) REFERENCES projects(tenant_id, id)
);
COMMENT ON TABLE firmware_packages IS '固件包；升级前必须校验厂商型号兼容性与校验值';
COMMENT ON COLUMN firmware_packages.id IS '行主键';
COMMENT ON COLUMN firmware_packages.tenant_id IS '所属租户';
COMMENT ON COLUMN firmware_packages.project_id IS '所属项目';
COMMENT ON COLUMN firmware_packages.name IS '固件名称';
COMMENT ON COLUMN firmware_packages.vendor IS '厂商';
COMMENT ON COLUMN firmware_packages.component IS '目标组件';
COMMENT ON COLUMN firmware_packages.version IS '版本标识；不是可字典序比较的版本号';
COMMENT ON COLUMN firmware_packages.compatible_models IS '兼容型号列表';
COMMENT ON COLUMN firmware_packages.object_key IS '固件对象键';
COMMENT ON COLUMN firmware_packages.size_bytes IS '包大小';
COMMENT ON COLUMN firmware_packages.sha256 IS '包校验值';
COMMENT ON COLUMN firmware_packages.status IS '校验状态';
COMMENT ON COLUMN firmware_packages.created_at IS '创建时间';
COMMENT ON COLUMN firmware_packages.updated_at IS '更新时间；触发器自动维护';
COMMENT ON COLUMN firmware_packages.deleted_at IS '软删除时间；空表示未删除';
COMMENT ON COLUMN firmware_packages.create_by IS '创建用户；系统任务为空';
COMMENT ON COLUMN firmware_packages.update_by IS '最后修改用户；系统任务为空';
CREATE UNIQUE INDEX uq_firmware_packages_1 ON firmware_packages (tenant_id, project_id, object_key) WHERE deleted_at IS NULL;
CREATE INDEX ix_firmware_packages_scope ON firmware_packages (tenant_id, project_id, created_at DESC) WHERE deleted_at IS NULL;
CREATE INDEX ix_firmware_packages_deleted ON firmware_packages (deleted_at) WHERE deleted_at IS NOT NULL;
CREATE INDEX ix_firmware_packages_create_by ON firmware_packages (create_by) WHERE create_by IS NOT NULL;
CREATE INDEX ix_firmware_packages_update_by ON firmware_packages (update_by) WHERE update_by IS NOT NULL;
CREATE TRIGGER trg_firmware_packages_updated BEFORE UPDATE ON firmware_packages FOR EACH ROW EXECUTE FUNCTION bmm_touch_updated_at();

CREATE TABLE firmware_baselines (
    id bigserial PRIMARY KEY,
    tenant_id bigint NOT NULL REFERENCES tenants(id),
    project_id bigint NOT NULL,
    name text NOT NULL,
    component text NOT NULL,
    firmware_package_id bigint NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz,
    create_by bigint REFERENCES users(id),
    update_by bigint REFERENCES users(id),
    UNIQUE (tenant_id, project_id, id),
    FOREIGN KEY (tenant_id, project_id) REFERENCES projects(tenant_id, id),
    FOREIGN KEY (tenant_id, project_id, firmware_package_id) REFERENCES firmware_packages(tenant_id, project_id, id)
);
COMMENT ON TABLE firmware_baselines IS '固件基线条目；不同组件按包显式绑定';
COMMENT ON COLUMN firmware_baselines.id IS '行主键';
COMMENT ON COLUMN firmware_baselines.tenant_id IS '所属租户';
COMMENT ON COLUMN firmware_baselines.project_id IS '所属项目';
COMMENT ON COLUMN firmware_baselines.name IS '基线名称';
COMMENT ON COLUMN firmware_baselines.component IS '组件标识';
COMMENT ON COLUMN firmware_baselines.firmware_package_id IS '目标固件包';
COMMENT ON COLUMN firmware_baselines.created_at IS '创建时间';
COMMENT ON COLUMN firmware_baselines.updated_at IS '更新时间；触发器自动维护';
COMMENT ON COLUMN firmware_baselines.deleted_at IS '软删除时间；空表示未删除';
COMMENT ON COLUMN firmware_baselines.create_by IS '创建用户；系统任务为空';
COMMENT ON COLUMN firmware_baselines.update_by IS '最后修改用户；系统任务为空';
CREATE UNIQUE INDEX uq_firmware_baselines_1 ON firmware_baselines (tenant_id, project_id, name, component) WHERE deleted_at IS NULL;
CREATE INDEX ix_firmware_baselines_scope ON firmware_baselines (tenant_id, project_id, created_at DESC) WHERE deleted_at IS NULL;
CREATE INDEX ix_firmware_baselines_deleted ON firmware_baselines (deleted_at) WHERE deleted_at IS NOT NULL;
CREATE INDEX ix_firmware_baselines_firmware_package_id ON firmware_baselines (tenant_id, project_id, firmware_package_id);
CREATE INDEX ix_firmware_baselines_create_by ON firmware_baselines (create_by) WHERE create_by IS NOT NULL;
CREATE INDEX ix_firmware_baselines_update_by ON firmware_baselines (update_by) WHERE update_by IS NOT NULL;
CREATE TRIGGER trg_firmware_baselines_updated BEFORE UPDATE ON firmware_baselines FOR EACH ROW EXECUTE FUNCTION bmm_touch_updated_at();

CREATE TABLE firmware_upgrades (
    id bigserial PRIMARY KEY,
    tenant_id bigint NOT NULL REFERENCES tenants(id),
    project_id bigint NOT NULL,
    asset_id bigint NOT NULL,
    operation_id bigint NOT NULL,
    firmware_package_id bigint NOT NULL,
    status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','prechecking','uploading','flashing','rebooting','verifying','succeeded','failed','cancelled','unknown')),
    from_version text,
    to_version text NOT NULL,
    compatibility_snapshot jsonb NOT NULL,
    confirmed_by bigint NOT NULL REFERENCES users(id),
    confirmed_at timestamptz NOT NULL,
    irreversible_at timestamptz,
    started_at timestamptz,
    completed_at timestamptz,
    error text,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz,
    create_by bigint REFERENCES users(id),
    update_by bigint REFERENCES users(id),
    UNIQUE (tenant_id, project_id, id),
    FOREIGN KEY (tenant_id, project_id) REFERENCES projects(tenant_id, id),
    CHECK (irreversible_at IS NULL OR irreversible_at >= confirmed_at),
    CHECK (completed_at IS NULL OR (started_at IS NOT NULL AND completed_at >= started_at)),
    FOREIGN KEY (tenant_id, project_id, asset_id) REFERENCES assets(tenant_id, project_id, id),
    FOREIGN KEY (tenant_id, project_id, operation_id) REFERENCES operations(tenant_id, project_id, id),
    FOREIGN KEY (tenant_id, project_id, firmware_package_id) REFERENCES firmware_packages(tenant_id, project_id, id)
);
COMMENT ON TABLE firmware_upgrades IS '固件升级实例；版本与设备快照用于追溯';
COMMENT ON COLUMN firmware_upgrades.id IS '行主键';
COMMENT ON COLUMN firmware_upgrades.tenant_id IS '所属租户';
COMMENT ON COLUMN firmware_upgrades.project_id IS '所属项目';
COMMENT ON COLUMN firmware_upgrades.asset_id IS '目标设备';
COMMENT ON COLUMN firmware_upgrades.operation_id IS '执行任务';
COMMENT ON COLUMN firmware_upgrades.firmware_package_id IS '目标固件包';
COMMENT ON COLUMN firmware_upgrades.status IS '升级状态';
COMMENT ON COLUMN firmware_upgrades.from_version IS '升级前版本';
COMMENT ON COLUMN firmware_upgrades.to_version IS '升级目标版本';
COMMENT ON COLUMN firmware_upgrades.compatibility_snapshot IS '兼容性检查与包校验快照';
COMMENT ON COLUMN firmware_upgrades.confirmed_by IS '升级确认人';
COMMENT ON COLUMN firmware_upgrades.confirmed_at IS '风险确认时间';
COMMENT ON COLUMN firmware_upgrades.irreversible_at IS '进入刷写不可逆阶段时间';
COMMENT ON COLUMN firmware_upgrades.started_at IS '开始时间';
COMMENT ON COLUMN firmware_upgrades.completed_at IS '完成时间';
COMMENT ON COLUMN firmware_upgrades.error IS '脱敏错误';
COMMENT ON COLUMN firmware_upgrades.created_at IS '创建时间';
COMMENT ON COLUMN firmware_upgrades.updated_at IS '更新时间；触发器自动维护';
COMMENT ON COLUMN firmware_upgrades.deleted_at IS '软删除时间；空表示未删除';
COMMENT ON COLUMN firmware_upgrades.create_by IS '创建用户；系统任务为空';
COMMENT ON COLUMN firmware_upgrades.update_by IS '最后修改用户；系统任务为空';
CREATE UNIQUE INDEX uq_firmware_upgrades_1 ON firmware_upgrades (tenant_id, project_id, operation_id) WHERE deleted_at IS NULL;
CREATE INDEX ix_firmware_upgrades_scope ON firmware_upgrades (tenant_id, project_id, created_at DESC) WHERE deleted_at IS NULL;
CREATE INDEX ix_firmware_upgrades_deleted ON firmware_upgrades (deleted_at) WHERE deleted_at IS NOT NULL;
CREATE INDEX ix_firmware_upgrades_asset_id ON firmware_upgrades (tenant_id, project_id, asset_id);
CREATE INDEX ix_firmware_upgrades_operation_id ON firmware_upgrades (tenant_id, project_id, operation_id);
CREATE INDEX ix_firmware_upgrades_firmware_package_id ON firmware_upgrades (tenant_id, project_id, firmware_package_id);
CREATE INDEX ix_firmware_upgrades_create_by ON firmware_upgrades (create_by) WHERE create_by IS NOT NULL;
CREATE INDEX ix_firmware_upgrades_update_by ON firmware_upgrades (update_by) WHERE update_by IS NOT NULL;
CREATE TRIGGER trg_firmware_upgrades_updated BEFORE UPDATE ON firmware_upgrades FOR EACH ROW EXECUTE FUNCTION bmm_touch_updated_at();

CREATE TABLE discovery_candidates (
    id bigserial PRIMARY KEY,
    tenant_id bigint NOT NULL REFERENCES tenants(id),
    project_id bigint NOT NULL,
    address inet NOT NULL,
    protocol text NOT NULL CHECK (protocol IN ('redfish','ipmi')),
    status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','confirmed','ignored','failed')),
    serial_number text,
    asset_id bigint,
    snapshot jsonb NOT NULL DEFAULT '{}'::jsonb,
    last_seen_at timestamptz NOT NULL DEFAULT now(),
    error text,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz,
    create_by bigint REFERENCES users(id),
    update_by bigint REFERENCES users(id),
    UNIQUE (tenant_id, project_id, id),
    FOREIGN KEY (tenant_id, project_id) REFERENCES projects(tenant_id, id),
    FOREIGN KEY (tenant_id, project_id, asset_id) REFERENCES assets(tenant_id, project_id, id)
);
COMMENT ON TABLE discovery_candidates IS '待确认发现设备；不得自动覆盖已登记设备凭据';
COMMENT ON COLUMN discovery_candidates.id IS '行主键';
COMMENT ON COLUMN discovery_candidates.tenant_id IS '所属租户';
COMMENT ON COLUMN discovery_candidates.project_id IS '所属项目';
COMMENT ON COLUMN discovery_candidates.address IS '发现地址';
COMMENT ON COLUMN discovery_candidates.protocol IS '探测协议';
COMMENT ON COLUMN discovery_candidates.status IS '发现状态';
COMMENT ON COLUMN discovery_candidates.serial_number IS '探测序列号';
COMMENT ON COLUMN discovery_candidates.asset_id IS '确认登记后的资产';
COMMENT ON COLUMN discovery_candidates.snapshot IS '脱敏发现信息';
COMMENT ON COLUMN discovery_candidates.last_seen_at IS '最近发现时间';
COMMENT ON COLUMN discovery_candidates.error IS '脱敏探测错误';
COMMENT ON COLUMN discovery_candidates.created_at IS '创建时间';
COMMENT ON COLUMN discovery_candidates.updated_at IS '更新时间；触发器自动维护';
COMMENT ON COLUMN discovery_candidates.deleted_at IS '软删除时间；空表示未删除';
COMMENT ON COLUMN discovery_candidates.create_by IS '创建用户；系统任务为空';
COMMENT ON COLUMN discovery_candidates.update_by IS '最后修改用户；系统任务为空';
CREATE UNIQUE INDEX uq_discovery_candidates_1 ON discovery_candidates (tenant_id, project_id, address, protocol) WHERE deleted_at IS NULL;
CREATE INDEX ix_discovery_candidates_scope ON discovery_candidates (tenant_id, project_id, created_at DESC) WHERE deleted_at IS NULL;
CREATE INDEX ix_discovery_candidates_deleted ON discovery_candidates (deleted_at) WHERE deleted_at IS NOT NULL;
CREATE INDEX ix_discovery_candidates_asset_id ON discovery_candidates (tenant_id, project_id, asset_id);
CREATE INDEX ix_discovery_candidates_create_by ON discovery_candidates (create_by) WHERE create_by IS NOT NULL;
CREATE INDEX ix_discovery_candidates_update_by ON discovery_candidates (update_by) WHERE update_by IS NOT NULL;
CREATE TRIGGER trg_discovery_candidates_updated BEFORE UPDATE ON discovery_candidates FOR EACH ROW EXECUTE FUNCTION bmm_touch_updated_at();

CREATE TABLE audits (
    id bigserial PRIMARY KEY,
    tenant_id bigint NOT NULL REFERENCES tenants(id),
    project_id bigint NOT NULL,
    user_id bigint REFERENCES users(id),
    api_key_id bigint,
    request_id text NOT NULL,
    action text NOT NULL,
    target_type text NOT NULL,
    target_id text,
    outcome text NOT NULL CHECK (outcome IN ('success','denied','failed')),
    client_ip inet,
    user_agent text,
    before_snapshot jsonb,
    after_snapshot jsonb,
    occurred_at timestamptz NOT NULL DEFAULT now(),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz,
    create_by bigint REFERENCES users(id),
    update_by bigint REFERENCES users(id),
    UNIQUE (tenant_id, project_id, id),
    FOREIGN KEY (tenant_id, project_id) REFERENCES projects(tenant_id, id),
    FOREIGN KEY (tenant_id, project_id, api_key_id) REFERENCES api_keys(tenant_id, project_id, id)
);
COMMENT ON TABLE audits IS '审计证据；应用账户只允许追加，保留与清理须使用独立授权管理流程';
COMMENT ON COLUMN audits.id IS '行主键';
COMMENT ON COLUMN audits.tenant_id IS '所属租户';
COMMENT ON COLUMN audits.project_id IS '所属项目';
COMMENT ON COLUMN audits.user_id IS '行为用户；系统动作可空';
COMMENT ON COLUMN audits.api_key_id IS '调用密钥';
COMMENT ON COLUMN audits.request_id IS '请求关联标识';
COMMENT ON COLUMN audits.action IS '动作权限码或系统事件码';
COMMENT ON COLUMN audits.target_type IS '目标资源类型';
COMMENT ON COLUMN audits.target_id IS '目标资源标识；多态关联由服务层校验';
COMMENT ON COLUMN audits.outcome IS '执行结果';
COMMENT ON COLUMN audits.client_ip IS '客户端地址';
COMMENT ON COLUMN audits.user_agent IS '客户端标识';
COMMENT ON COLUMN audits.before_snapshot IS '变更前脱敏快照';
COMMENT ON COLUMN audits.after_snapshot IS '变更后脱敏快照';
COMMENT ON COLUMN audits.occurred_at IS '发生时间';
COMMENT ON COLUMN audits.created_at IS '创建时间';
COMMENT ON COLUMN audits.updated_at IS '更新时间；触发器自动维护';
COMMENT ON COLUMN audits.deleted_at IS '软删除时间；空表示未删除';
COMMENT ON COLUMN audits.create_by IS '创建用户；系统任务为空';
COMMENT ON COLUMN audits.update_by IS '最后修改用户；系统任务为空';
CREATE INDEX ix_audits_scope ON audits (tenant_id, project_id, created_at DESC) WHERE deleted_at IS NULL;
CREATE INDEX ix_audits_deleted ON audits (deleted_at) WHERE deleted_at IS NOT NULL;
CREATE INDEX ix_audits_user_id ON audits (tenant_id, project_id, user_id);
CREATE INDEX ix_audits_api_key_id ON audits (tenant_id, project_id, api_key_id);
CREATE INDEX ix_audits_create_by ON audits (create_by) WHERE create_by IS NOT NULL;
CREATE INDEX ix_audits_update_by ON audits (update_by) WHERE update_by IS NOT NULL;
CREATE TRIGGER trg_audits_updated BEFORE UPDATE ON audits FOR EACH ROW EXECUTE FUNCTION bmm_touch_updated_at();

-- 高频调度与历史查询索引。
CREATE INDEX ix_operations_schedule ON operations (next_run_at, id) WHERE deleted_at IS NULL AND status IN ('pending','retry_wait');
CREATE INDEX ix_operations_asset_history ON operations (tenant_id, project_id, asset_id, created_at DESC);
CREATE INDEX ix_outbox_schedule ON task_outbox (available_at, id) WHERE deleted_at IS NULL AND status IN ('pending','failed','publishing');
-- 租约唯一性不受软删除影响；释放使用到期更新，不插入第二行。
CREATE UNIQUE INDEX uq_device_leases_asset ON device_leases (tenant_id, project_id, asset_id);
CREATE INDEX ix_device_leases_expiry ON device_leases (expires_at);
CREATE INDEX ix_sol_active ON sol_sessions (tenant_id, project_id, asset_id, expires_at) WHERE deleted_at IS NULL AND ended_at IS NULL;
CREATE UNIQUE INDEX uq_ip_allocations_active ON ip_allocations (tenant_id, project_id, network_id, address) WHERE deleted_at IS NULL AND status IN ('reserved','allocated');
CREATE UNIQUE INDEX uq_alerts_active ON alerts (tenant_id, project_id, asset_id, dedup_key) WHERE deleted_at IS NULL AND status IN ('firing','acknowledged');
CREATE INDEX ix_alerts_status ON alerts (tenant_id, project_id, status, last_seen_at DESC) WHERE deleted_at IS NULL;
CREATE INDEX ix_sensor_records_history ON sensor_records (tenant_id, project_id, asset_id, sensor_key, sampled_at DESC);
CREATE INDEX ix_sensor_records_time ON sensor_records USING brin (sampled_at);
CREATE INDEX ix_audits_time ON audits (tenant_id, project_id, occurred_at DESC);
CREATE INDEX ix_audits_request ON audits (request_id);
CREATE INDEX ix_notifications_schedule ON notification_deliveries (next_attempt_at, id) WHERE deleted_at IS NULL AND status IN ('pending','failed','sending');
CREATE UNIQUE INDEX uq_installations_active_asset ON installations (tenant_id, project_id, asset_id) WHERE deleted_at IS NULL AND status IN ('pending','preparing','booting','installing','post_install','unknown');
CREATE UNIQUE INDEX uq_firmware_upgrades_active_asset ON firmware_upgrades (tenant_id, project_id, asset_id) WHERE deleted_at IS NULL AND status IN ('pending','prechecking','uploading','flashing','rebooting','verifying','unknown');
-- 装机与升级跨类型互斥由共用 device_leases 保证，不能只依赖上述两条索引。
COMMIT;
