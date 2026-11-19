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
| **PXE 测试实验室** | 创建隔离测试 VM，验证 Ubuntu 网络下载安装及首次磁盘启动，支持实时串口日志和取消安装；见 [操作说明](PXE_VM_TEST.md) |
| **网络管理** | 子网规划、IP 地址池、全局 DHCP/TFTP 服务配置与控制 |
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
docker compose up -d --build

# 3. 查看服务状态
docker compose ps

# 4. 访问平台
#    前端：http://localhost:8080
#    HAProxy Stats：http://localhost:8404
```

物理机装机测试步骤见 [物理机 PXE 装机测试](./PXE_PHYSICAL_TEST.md)，隔离 VM 验证见 [PXE 测试 VM](./PXE_VM_TEST.md)。

### PXE 宿主机与内置引导文件

PXE 控制服务通过 Linux 宿主机网络读取真实网卡及其 IPv4 地址，不使用 API 容器的网卡信息。在「PXE 配置」点击「刷新宿主机选项」即可重新获取；网卡切换后 IPv4 联动更新，只有一个匹配地址时自动选择。未启用或没有 IPv4 的网卡不可选，服务器地址不能位于 DHCP 地址池内。「同网段直连」要求网卡与客户端子网相同；「三层 DHCP Relay」允许不同网段，在客户端三层网关配置 Relay 指向 PXE 服务器。子网网关和 IPv4 DNS 随 DHCP 下发，Relay 模式必须填写客户端网关。VLAN、路由和交换机 Relay 需在网络设备上配置，具体示例见 [物理机 PXE 装机测试](./PXE_PHYSICAL_TEST.md)。

后端镜像通过 Debian 的 `ipxe` 软件包提供 BIOS 的 `undionly.kpxe`、`ipxe.pxe`，以及 x86_64 UEFI 的 `ipxe.efi`、`snponly.efi`。控制器首次启动将缺失文件初始化到挂载目录 `/var/lib/tftpboot`；已有文件不会被覆盖。界面按文件实际状态提供 TFTP 根目录、BIOS 和 UEFI 下拉选项，保留已保存的自定义引导文件，并排除越界链接。UEFI 文件不适用于 ARM 或要求签名的 Secure Boot。

部署前将 `.env` 中的 `PXE_HTTP_BASE` 改为装机客户端可达的 `http(s)://宿主机地址:HTTP_PORT/api/v1/provision`，不要使用前端开发端口 5173。控制器会据此生成缺失的 `default.ipxe`。已有该文件时保留原内容，若迁移到其他宿主机或修改 HTTP 基址，需要同步修改 `provisioning/tftp/default.ipxe`。装机内核、initrd 和 ISO 仍按所选镜像/安装方案准备，不由引导文件初始化过程提供。

操作顺序：子网 → 地址池 → 组织/项目绑定 → 选择宿主机网卡和 IPv4 → 选择目录及引导文件 → 保存 → 手动启动 DHCP/TFTP。保存配置不会自动启动服务，运行期间配置锁定，存在活动安装时禁止服务变更。

「PXE 配置 → 标签与固定 IP 规则」支持把交换机 Option 82 的 Circuit-ID / Remote-ID 精确匹配到固定 IPv4，也可限定服务器 MAC 或使用独立 MAC 规则。同一规则的多项条件按 AND 匹配；支持文本与十六进制标识、启停和删除、未匹配设备的动态分配或拒绝分配，以及可选的 `dhcp-proxy` 续租代理。规则保存在全局 PXE 状态中，随全局配置一起保存；平台生成 dnsmasq 标签，无需手写配置。详细操作与续租说明见 [物理机 PXE 装机测试](./PXE_PHYSICAL_TEST.md)。

规则支持 Excel（`.xlsx` / `.xls`）及粘贴批量导入，内置 Option 82 和 MAC 两种模板；提供按行校验预览、追加及替换全部，最多 128 条，校验通过后统一加入待保存配置。

GitHub 仓库只提交代码、构建配置和环境变量示例；`.env`、`.env.target`、本地凭据目录及 `provisioning/` 下的 ISO、引导运行数据、安装凭据和 VM 磁盘均不提交。新机器通过构建镜像初始化引导文件，无需复制本机测试数据。

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

前端开发服务器默认监听 `http://localhost:5173`，通过 Vite 代理将 `/api` 请求转发至 `http://localhost:18080`（含 WebSocket 支持）。直接运行本地后端时，可用 `BMM_API_TARGET=http://localhost:8000 npm run dev` 指定目标。

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
| `TRUSTED_PROXY_CIDRS` | 后端可信代理 IP/CIDR，逗号分隔 | 空（忽略转发头） |
| `TRUSTED_UPSTREAM_PROXY` | HAProxy 信任的上游入口 IP/CIDR | `127.0.0.1` |

操作 IP 来自访问者的网络连接。使用 Compose 入口时，后端的 `TRUSTED_PROXY_CIDRS` 填实际 app-net 子网；使用宿主机 Vite 入口时，还需将 `TRUSTED_UPSTREAM_PROXY` 设置为 HAProxy 看到的宿主机网关地址。请通过 `docker network inspect` 核对地址，避免信任所有来源。Vite 会覆盖客户端提交的转发头，HAProxy 仅接受可信上游的来源链；直接访问后端时，保持可信代理为空即可。经 NAT 或额外代理访问时，能够记录的地址取决于入口实际接收到的连接和可信代理链。历史错误 IP 无法自动还原。

默认 `docker compose` 和 `dev.sh` 读取根目录 `.env`；`--env-file .env.target` 则读取指定配置，两者不会互相继承。若交替使用这两种启动方式，需同时配置上述两项，再更新 API 与 HAProxy 容器；仅修改其中一个文件会在另一种方式重建服务时丢失来源 IP 设置。

### 管理员初始化

首次访问平台时，通过初始化向导创建管理员并配置内部管理平台，无需预先配置管理员环境变量。用户名留空默认为 `admin`；密码留空时由后端安全随机生成，仅在初始化成功页面展示一次，请保存后再进入系统。刷新或离开页面后无法再次查看生成密码。自行填写密码时须为 6–72 字节。

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

平台仅提供内部基础设施管理，不提供自助注册、租售产品、订单、计费、支付、发票或营销功能。历史迁移保留以校验已有数据库；016 迁移统一初始化标记并停用运营配置及权限，不删除历史业务记录。
