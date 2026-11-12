# MetalOps — 裸金属资产管理与 BMC 运维平台

一站式裸金属服务器全生命周期管理平台，涵盖资产发现、BMC 远程控制、硬件监控、系统重装、网络管理、告警推送与固件升级，助力数据中心运维团队高效管理大规模裸金属基础设施。

---

## 功能特性

| 模块 | 说明 |
|------|------|
| **项目与租户管理** | 多项目隔离，成员邀请与角色分配 |
| **裸机资产管理** | 资产 CRUD、Excel 批量导入导出、二维码标识、网段自动扫描发现、标签管理 |
| **机房与机柜管理** | 机房/机柜层级管理，U位可视化布局 |
| **BMC 远程控制** | 电源开关机、启动顺序设置、传感器数据采集（温度/风扇/电源/电压）、RAID 信息读取、BMC 用户管理、BMC 网络配置，Redfish 优先 + ipmitool 兜底，令牌桶并发限流 |
| **硬件监控** | 结构化 Monitor 数据面板（CPU / 内存 / 存储 / 网络 / 电源 / 固件 / 传感器） |
| **镜像与系统重装** | 镜像上传下载、Kickstart 模板管理、PXE 批量装机任务编排 |
| **网络管理** | 网段管理、IP 地址池、自动分配/释放、VLAN/DHCP 配置 |
| **告警管理** | 自定义告警规则，支持企业微信、钉钉、Webhook、SMTP 邮件 4 种推送渠道 |
| **固件管理** | 固件版本跟踪、批量升级任务、实时进度监控 |
| **用户与 RBAC** | 23 个细粒度权限（`resource:action` 格式），4 级角色体系（admin / manager / operator / viewer） |
| **开放 API** | API Key 认证，67 个 RESTful 端点，Swagger 文档 |
| **系统设置** | 可配置运行参数，种子数据自动初始化 |

---

## 技术栈

### 后端

- **语言**：Go 1.26
- **HTTP 框架**：Gin
- **ORM**：GORM + PostgreSQL 16
- **缓存 / 消息队列**：Redis 7 + asynq（异步任务调度）
- **认证**：JWT (HS256) + RBAC
- **安全**：AES-256-GCM 加密 BMC 凭据
- **协议适配**：Redfish / IPMI（ipmitool 兜底）
- **对象存储**：MinIO
- **配置管理**：YAML + 环境变量覆盖

### 前端

- **框架**：Vue 3.5 (Composition API) + TypeScript
- **UI 组件库**：Naive UI
- **状态管理**：Pinia
- **数据可视化**：ECharts
- **终端模拟**：xterm.js
- **Excel 处理**：xlsx
- **构建工具**：Vite

### 部署

- **容器编排**：Docker Compose（11 个服务）
- **负载均衡**：HAProxy 3.0
- **Web 服务**：Nginx（前端静态资源）
- **数据库**：PostgreSQL 16 + Redis 7
- **对象存储**：MinIO
- **网络引导**：dnsmasq（PXE / DHCP / TFTP，可选 profile）

---

## 项目结构

```
baremetal-manager/
├── backend/
│   ├── cmd/server/          # 应用入口（server / worker / scheduler）
│   ├── config/
│   │   ├── config.yaml      # 默认配置
│   │   ├── config.go        # 配置加载逻辑
│   │   ├── openapi.yaml     # OpenAPI 3.0 规范文档
│   │   └── example.env      # 环境变量示例
│   ├── internal/
│   │   ├── adapter/         # 外部适配器（BMC、通知渠道、限流）
│   │   ├── handler/         # HTTP 请求处理器
│   │   ├── middleware/      # 中间件（JWT 认证、API Key）
│   │   ├── model/           # 数据模型定义
│   │   ├── repository/      # 数据访问层
│   │   ├── service/         # 业务逻辑层（含 AES-256-GCM 加密）
│   │   └── task/            # asynq 异步任务（扫描、重装、固件升级）
│   ├── migrations/          # 数据库迁移 SQL（go:embed）
│   ├── seeds/               # 种子数据
│   ├── Dockerfile
│   └── go.mod
├── frontend/
│   ├── src/
│   │   ├── views/           # 页面视图（19 个页面，含 1 个通用 ResourceView）
│   │   ├── components/      # 通用组件
│   │   ├── stores/          # Pinia 状态仓库
│   │   ├── api.ts           # Axios API 封装
│   │   ├── router.ts        # Vue Router 路由配置
│   │   ├── catalog.ts       # 导航目录
│   │   └── style.css        # 全局样式
│   ├── index.html
│   ├── vite.config.ts
│   ├── nginx.conf           # 生产 Nginx 配置
│   └── package.json
├── deploy/
│   ├── haproxy/             # HAProxy 负载均衡配置
│   ├── postgres/            # PostgreSQL 初始化脚本
│   ├── minio/               # MinIO 桶初始化脚本
│   ├── dnsmasq/             # PXE/DHCP/TFTP 配置
│   └── external-lb/         # 外部 LB 配置示例
├── docker-compose.yaml      # 容器编排（11 个服务）
└── .env.example             # 环境变量模板
```

---

## 快速开始

### 环境要求

| 依赖 | 最低版本 |
|------|---------|
| **Go** | 1.26+ |
| **Node.js** | 18+ |
| Docker & Docker Compose | v20.10+ / v2+ |
| PostgreSQL | 16 |
| Redis | 7 |

### 克隆仓库

```bash
git clone https://github.com/your-org/baremetal-manager.git
cd baremetal-manager
```

### Docker 一键部署（推荐）

```bash
# 1. 复制环境变量模板并修改关键配置
cp .env.example .env
# 编辑 .env，务必替换所有密码和密钥

# 2. 启动全部服务
docker compose up -d

# 3. 查看服务状态
docker compose ps

# 4. 访问平台
#    前端：http://localhost:8080
#    HAProxy Stats：http://localhost:8404
```

> **注意**：如需启用 PXE 网络引导（dnsmasq），使用 `docker compose --profile provisioning up -d`。

### 本地开发 — 后端

```bash
cd backend

# 安装依赖
go mod download

# 复制并编辑配置
cp config/example.env ../.env
# 确保 PostgreSQL、Redis、MinIO 已启动

# 启动 API 服务
go run cmd/server/main.go

# 启动异步 Worker（新终端）
go run cmd/server/main.go worker

# 启动任务调度器（新终端）
go run cmd/server/main.go scheduler
```

后端默认监听 `:8000`，健康检查端点：`GET /health`。

### 本地开发 — 前端

```bash
cd frontend

# 安装依赖
npm install

# 启动开发服务器（热重载）
npm run dev

# 构建生产版本
npm run build
```

前端开发服务器默认监听 `http://localhost:5173`，通过 Vite 代理将 `/api` 请求转发至后端 `:8000`（含 WebSocket 支持）。

---

## 配置说明

所有配置项均可通过 `backend/config/config.yaml` 或同名**环境变量**设置，环境变量优先级更高。

### 核心配置

| 变量 | 说明 | 默认值 |
|------|------|--------|
| `DATABASE_URL` | PostgreSQL 连接串 | `postgresql://bmm:change-me@postgres:5432/bmm` |
| `REDIS_URL` | Redis 连接串 | `redis://redis:6379/0` |
| `JWT_SECRET` | JWT 签名密钥（≥32 字符） | **必须设置** |
| `BMC_ENCRYPTION_KEY` | BMC 凭据 AES-256-GCM 加密密钥（32 字节 hex） | **必须设置** |
| `MINIO_ENDPOINT` | MinIO 服务端地址 | `minio:9000` |
| `MINIO_ACCESS_KEY` | MinIO 访问密钥 | `bmm-minio` |
| `MINIO_SECRET_KEY` | MinIO 秘密密钥 | **必须设置** |
| `MINIO_BUCKET` | MinIO 存储桶名称 | `baremetal-images` |
| `HTTP_PORT` | 对外 HTTP 端口 | `8080` |
| `LOG_LEVEL` | 日志级别 | `info` |

### 管理员初始化

首次访问平台时，通过初始化向导创建管理员并选择运行模式，无需预先配置管理员环境变量。用户名留空默认为 `admin`；密码留空时由后端安全随机生成，仅在初始化成功页面展示一次，请保存后再进入系统。刷新或离开页面后无法再次查看生成密码。自行填写密码时须为 6–72 字节。

数据库迁移保留默认组织、项目、角色、权限和系统设置种子，不预建管理员，也不会重置已有管理员凭据。生产部署应先限制初始化入口的网络访问，并通过 HTTPS 完成向导。

### 告警推送渠道

| 变量 | 说明 |
|------|------|
| `ALERT_WECHAT_WEBHOOK` | 企业微信机器人 Webhook 地址 |
| `ALERT_DINGTALK_WEBHOOK` | 钉钉机器人 Webhook 地址 |
| `ALERT_WEBHOOK_URL` | 通用 Webhook 地址 |
| `ALERT_SMTP_HOST` | SMTP 邮件服务器地址 |
| `ALERT_SMTP_PORT` | SMTP 端口（默认 `587`） |
| `ALERT_SMTP_USER` | SMTP 用户名 |
| `ALERT_SMTP_PASS` | SMTP 密码 |

### BMC 与 SOL

| 变量 | 说明 | 默认值 |
|------|------|--------|
| `BMC_MAX_CONCURRENT` | BMC 最大并发连接数 | `10` |
| `SENSOR_INTERVAL` | 传感器采集间隔（秒） | `300` |
| `SOL_ALLOWED_ORIGIN` | SOL WebSocket 允许的 Origin | `http://localhost:8080` |
| `SOL_SESSION_TIMEOUT` | SOL 会话超时（分钟） | `15` |
| `SOL_LOG_ENABLED` | 是否启用 SOL 日志记录 | `true` |

---

## API 文档

平台提供 **67 个 RESTful API 端点**，基础路径为 `/api/v1`。

### 认证方式

- **JWT 令牌**：`Authorization: Bearer <token>`（用户登录获取）
- **API Key**：`X-API-Key: <key>`（开放接口调用）

### 完整规范

完整的 OpenAPI 3.0 规范文档见 [`backend/config/openapi.yaml`](backend/config/openapi.yaml)，可导入 Swagger UI 或 Redoc 在线预览。

---

## 数据库迁移

数据库迁移采用 **go:embed 内嵌 SQL** 方式，服务启动时自动执行：

| 文件 | 说明 |
|------|------|
| `migrations/001_initial.sql` | 初始表结构（资产、用户、角色、权限等核心表） |
| `migrations/002_expansion.sql` | 扩展表（机房、机柜、标签、传感器、告警规则、API 密钥、系统设置、装机模板等） |

迁移脚本设计为**幂等可重复执行**，支持 `IF NOT EXISTS` 和兼容补列，首次启动和后续升级均可安全运行。

PostgreSQL 初始化脚本位于 `deploy/postgres/init/001-extensions.sql`，通过 Docker 入口点自动加载。

---

## 开发指南

### 后端架构

采用分层架构，各层职责清晰：

```
HTTP Request
  → middleware/   (JWT 认证 / API Key 校验)
  → handler/      (参数绑定、路由分发、响应序列化)
  → service/      (异步任务提交、BMC 适配器编排、加密解密)
  → repository/   (GORM 数据库操作、分页查询、CRUD)
  → model/        (数据模型定义)
  → adapter/      (BMC Redfish/IPMI 通信、告警推送渠道)
```

**关键设计**：
- `handler` 直接调用 `repository.Store` 完成大部分 CRUD 操作，复杂业务逻辑（异步任务、BMC 操作）通过 `service` 层编排
- `service` 层封装 asynq 队列客户端和 BMC 适配器，负责任务提交与调度
- `adapter` 层实现 Redfish/IPMI 协议通信和 4 种告警推送渠道

**异步任务**（`internal/task/`）使用 asynq 框架，支持 6 类后台任务：
- `power.on/off/restart` — 电源操作
- `inspect` — 硬件检查
- `reinstall` — 系统重装
- `firmware.upgrade` — 固件升级
- `scan.network` — 网段扫描与资产发现
- `batch.action` — 批量操作

**调度器**（`scheduler` 模式）每 2 分钟轮询所有设备 BMC 连通性，不可达时自动生成告警。

**外部适配器**（`internal/adapter/`）：
- `bmc.go` / `bmc_extended.go` — Redfish/IPMI 协议适配（电源、传感器、RAID、启动顺序、用户/网络管理）
- `notify.go` + `notify_{wechat,dingtalk,webhook,email}.go` — 4 种告警推送渠道
- `ratelimit.go` — 令牌桶并发限流

### 前端架构

```
src/
├── views/           # 19 个页面视图（含 1 个通用 ResourceView）
├── components/      # 通用组件（ShellLayout 等）
├── stores/          # Pinia 状态仓库（auth 等）
├── api.ts           # Axios 实例与 API 请求封装
├── router.ts        # Vue Router 路由定义
├── catalog.ts       # 导航目录配置
├── style.css        # 全局样式
├── App.vue          # 根组件
└── main.ts          # 应用入口
```

前端页面与功能模块对应关系：

| 页面文件 | 功能模块 |
|---------|---------|
| `DashboardView.vue` | 总览仪表盘 |
| `ResourceView.vue` | 裸机资产管理 |
| `AssetDetailView.vue` | 资产详情 |
| `DatacenterView.vue` | 机房管理 |
| `RackView.vue` | 机柜管理 |
| `SolView.vue` | SOL 串口控制台 |
| `InstallPlanView.vue` | 装机计划 |
| `KickstartTemplateView.vue` | Kickstart 模板 |
| `PXEConfigView.vue` | PXE 配置 |
| `FirmwareUpgradeView.vue` | 固件升级 |
| `AlertRuleView.vue` | 告警规则 |
| `AlertChannelView.vue` | 告警渠道 |
| `ProjectMembersView.vue` | 项目成员 |
| `TagManage.vue` | 标签管理 |
| `UserManageView.vue` | 用户管理 |
| `RoleManageView.vue` | 角色管理 |
| `ApiKeysView.vue` | API 密钥管理 |
| `SystemSettingsView.vue` | 系统设置 |
| `LoginView.vue` | 登录页 |

---

## 许可证

本项目基于 [MIT License](LICENSE) 开源。
