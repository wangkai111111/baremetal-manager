ALTER TABLE orders ADD COLUMN configuration jsonb NOT NULL DEFAULT '{}'::jsonb;
COMMENT ON COLUMN orders.configuration IS '服务端核验的采购配置快照：租期、带宽、IP、机房及镜像选择';
