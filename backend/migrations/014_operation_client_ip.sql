ALTER TABLE operations ADD COLUMN IF NOT EXISTS client_ip inet;
COMMENT ON COLUMN operations.client_ip IS '提交操作的客户端 IP；历史记录或无请求来源的系统任务为空';
