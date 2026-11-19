ALTER TABLE users ADD COLUMN IF NOT EXISTS must_change_password boolean NOT NULL DEFAULT false;
ALTER TABLE users ADD COLUMN IF NOT EXISTS password_version bigint NOT NULL DEFAULT 0;
COMMENT ON COLUMN users.must_change_password IS '登录后必须先修改初始密码，再访问业务功能';
COMMENT ON COLUMN users.password_version IS '修改密码时递增，使旧登录令牌失效';
