-- 按平台授权将现有资产统一切换为默认不校验证书；逐机器仍可显式开启校验。
-- 这是一次性数据重置，避免历史资产继续沿用旧的 true 配置。
ALTER TABLE assets ALTER COLUMN bmc_tls_verify SET DEFAULT false;
UPDATE assets SET bmc_tls_verify = false WHERE bmc_tls_verify = true;
COMMENT ON COLUMN assets.bmc_tls_verify IS '是否校验证书；平台默认不校验，逐机器开启需审计';
