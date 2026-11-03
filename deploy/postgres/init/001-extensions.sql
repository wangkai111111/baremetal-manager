-- 应用首次启动时初始化常用扩展；脚本仅在空数据卷上执行。
CREATE EXTENSION IF NOT EXISTS "uuid-ossp";
CREATE EXTENSION IF NOT EXISTS "pgcrypto";
