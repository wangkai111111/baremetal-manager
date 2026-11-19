# 裸机运维平台 · Baremetal Manager

面向内部运维的服务器资产管理与 PXE 装机平台。通过组织、项目和部门分配机器，集中处理硬件信息、电源操作、系统安装、监控告警和审计追踪。

当前产品为内部管理平台，不提供自助购买、订单、计费或支付功能。界面名称、Logo、主题及 SMTP 可在系统设置中直接配置。

## 功能与边界

| 模块 | 当前能力 |
| --- | --- |
| 服务器管理 | 新增、编辑、分配部门、标签与机房；查看序列号、IP、规格、电源和健康状态；复制 IP 和初始登录信息 |
| 硬件与运维 | Redfish / IPMI 接入、硬件采集、受支持的电源操作、SOL 串口控制台；具体能力取决于设备及 BMC 协议 |
| 资产管理 | 部门树、机房、采购信息、资产折旧和维保信息编辑、维保到期提醒 |
| 系统镜像 | 镜像上传、目录下载、校验与装机方案；镜像文件存放在 MinIO 或受管本地 ISO 目录 |
| PXE 装机 | Ubuntu Server 22.04 / 24.04 Live ISO，amd64 / x86_64，autoinstall；测试 VM 自动网络引导，物理机手动选择 PXE |
| 装机网络 | 子网、地址池、宿主机网卡 / IPv4 选择；同网段直连、三层 DHCP Relay、Option 82 / MAC 固定 IP 规则及 Excel / 粘贴导入 |
| 任务中心 | 执行事件、装机步骤、起止时间、阶段截止时间、超时和取消；安装成功须有首次磁盘启动回报 |
| 告警与工单 | 告警规则、推送渠道、维护相关配置、工单处理；支持企业微信、钉钉、Webhook、SMTP |
| 用户与授权 | 平台管理员、组织 / 部门授权及管理员；随机初始密码、可选欢迎邮件、首次登录强制改密 |
| 审计 | 按管理范围查看登录、退出、授权、服务器及配置操作；记录来源 IP，支持筛选、详情和 CSV 导出 |

固件包、基线和清单可管理，实际固件升级执行接口仍返回 `501`。不要将页面入口视为已支持所有厂商的升级、RAID 或 BMC 配置能力。物理机 PXE 已有执行流程，但仍需在目标硬件上完成端到端验收；现有成功实测以 UEFI 测试 VM 为主。

## 部署

### 环境要求

- Linux 宿主机、Docker Engine 和 Docker Compose v2。
- PXE 使用宿主网络；测试 VM 需要 `/dev/net/tun`。当前 Compose 还挂载 `/dev/kvm`，宿主必须提供该设备，否则需先调整部署配置。
- 宿主机有足够空间保存镜像、解包缓存及 VM 磁盘；测试 VM 固定使用 2 vCPU、8 GB 内存和 40 GB 虚拟磁盘。
- 仅构建与运行容器时不需要宿主安装 Go / Node.js；本地开发使用 Go 1.26、Node.js 22.18+ 和 npm。

### 首次启动

```bash
git clone https://github.com/wangkai111111/baremetal-manager.git
cd baremetal-manager
cp .env.example .env
mkdir -p provisioning/tftp provisioning/iso provisioning/netboot
```

编辑根目录 `.env`，启动前至少确认以下配置：

| 配置 | 要求 |
| --- | --- |
| `POSTGRES_PASSWORD`、`DATABASE_MIGRATION_URL` | 数据库 owner 口令及对应连接串，保持一致 |
| `DATABASE_APP_USER`、`DATABASE_APP_PASSWORD`、`DATABASE_URL` | 独立运行账户及对应连接串；应用口令至少 16 字符 |
| `POSTGRES_VOLUME` | 全新部署使用未占用的卷名；升级时保留原卷名，不能随意更换 |
| `JWT_SECRET` | 至少 32 字节的随机密钥 |
| `BMC_ENCRYPTION_KEY` | 恰好 32 字节的原始字符串，不是将 64 位 hex 解码后使用 |
| `MINIO_ROOT_PASSWORD` | 替换示例值；本地开发使用的 `MINIO_SECRET_KEY` 也应一致 |
| `SOL_ALLOWED_ORIGIN` | 浏览器实际访问的 `scheme://host[:port]` |
| `PXE_HTTP_BASE` | 装机客户端可达的后端入口，以 `/api/v1/provision` 结尾；不能使用前端开发端口 5173 |
| `POSTGRES_PORT`、`DB_PORT` | 当前 Compose 控制器使用 `DB_PORT` 访问宿主映射的 PostgreSQL 端口；两者须显式设为相同值，例如均为 `15432` |

数据库密码包含 `@`、`:`、`/` 等字符时，连接串中的密码需要 URL 编码。`.env` 中保留原始口令；`dev.sh` 通过 shell 加载该文件，含 shell 特殊字符的值需正确引用。运行密钥应独立生成；升级时保留原凭据加密密钥，否则无法解密已有 BMC 与装机凭据。

例如在 `.env` 补充端口配置：

```dotenv
POSTGRES_PORT=15432
DB_PORT=15432
```

构建并启动：

```bash
docker compose build
docker compose up -d
docker compose ps -a
curl -fsS http://localhost:8080/health
curl -fsS http://localhost:8080/ready
```

默认平台入口为 `http://宿主机地址:8080`，可用 `HTTP_PORT` 修改。首次访问按向导创建平台管理员；没有预置管理员口令。初始化用户名留空默认为 `admin`，密码留空会随机生成，并只在完成页面展示一次。

`migrate`、`minio-init` 是一次性任务，正常完成后显示退出码 0。DHCP / TFTP 在控制器每次启动后均保持关闭，需要管理员在界面明确启用。

当前 Compose 会向宿主发布 PostgreSQL（默认 15432）、MinIO API（19000）和控制台（19001）；对外开放范围应按实际运维网络配置。HAProxy 统计端口默认仅绑定回环地址 8404。默认入口为 HTTP，生产 HTTPS 入口由外部代理提供。

### 配置生效位置

- **部署连接和密钥**：根目录 `.env`，完整变量说明见 [.env.example](.env.example)。默认 `docker compose` 和 [dev.sh](dev.sh) 使用它；`--env-file` 指定的其他文件不会自动继承 `.env`。
- **名称、Logo、主题、应用展示、SMTP**：系统设置，使用内置可编辑项，无需自行猜测配置键。
- **告警规则和接收目标**：告警规则、推送渠道。用户欢迎邮件使用系统设置中保存的 SMTP。
- **地址规划**：子网管理和地址池。网关、IPv4 DNS 从选定子网读取并用于 DHCP 下发。
- **DHCP / TFTP 与固定 IP**：PXE 配置。选择宿主机实际网卡和 IPv4；保存配置后再启用服务，活动安装期间有配置保护。

当前服务入口直接读取环境变量与数据库配置；不要把 [config.yaml](backend/config/config.yaml) 当成运行入口自动加载的主配置文件。

### 操作人 IP

任务和审计使用同一可信代理来源链。直接访问后端时可保持 `TRUSTED_PROXY_CIDRS` 为空；经过 Compose HAProxy 时，将其设为实际 `app-net` 的代理网段，可用 `docker network inspect` 核对。

如入口是宿主机 Vite，还需把 `TRUSTED_UPSTREAM_PROXY` 设置为 HAProxy 看到的宿主机网关地址。不要信任任意来源的转发头。经 NAT 只能记录入口可见的来源地址，历史错误 IP 无法还原。修改环境变量后需要重新创建相应服务，仅刷新页面不会生效。

## 使用顺序

1. 完成管理员初始化，配置组织、项目、部门和机房。
2. 新增服务器并设置实际所属部门；配置 BMC 接入或创建测试 VM。
3. 创建普通用户，在用户管理中分配组织 / 部门访问范围；需要管理权限时设置对应范围管理员。
4. 上传镜像，配置子网、地址池、PXE 服务与装机方案。
5. 从服务器“更多 → 重装系统”提交安装，任务中心查看步骤、日志和截止时间。
6. 从“更多 → 初始登录信息”复制目标系统账号、密码和 SSH 连接命令；在审计日志查看操作记录。

### 授权规则

组织授权覆盖该组织内的机器；部门授权覆盖该部门及下级部门。项目仍是资源上下文，授权、列表、详情和实际操作均由后端校验。

普通用户可操作其授权范围内的机器，包括支持的电源、手动采集、重装、初始登录信息及 SOL；不能新增、编辑、删除服务器或自行分配权限。全局 PXE 配置、测试 VM 创建 / 删除及测试网络准备由平台管理员执行。

平台管理员可查看全平台审计；组织管理员查看本组织；部门管理员查看管理部门及下级部门。普通用户无管理审计权限。授权变更立即影响访问范围，历史日志保留当时对象名称与部门归属，详见 [审计说明](AUDIT_LOGS.md)。

### 用户初始密码与邮件

新增用户时可留空密码，由服务端随机生成并展示一次；可选择发送欢迎邮件，以及首次登录是否强制改密。启用强制改密的用户，完成改密前只能查看身份、改密或退出。系统登录密码与 PXE 目标系统 root 密码是两套独立凭据。

### PXE 测试与物理机

- [测试 VM 指南](PXE_VM_TEST.md)：隔离网桥、介质准备、可选镜像、安装验证和删除。
- [物理机指南](PXE_PHYSICAL_TEST.md)：真实网卡、三层 Relay、Option 82 标签及批量固定 IP 规则。

装机只支持 Ubuntu Server 22.04 / 24.04 Live ISO 的 x86_64 / amd64 自动安装；UEFI 引导文件不适用于 ARM，测试需关闭 Secure Boot。当前全局 DHCP 一次绑定一个客户端子网 / 地址池，不支持多作用域或 ProxyDHCP。

root 密码留空会随机生成，可选上传或粘贴 SSH 公钥；Ubuntu 默认允许 root SSH 密码登录，也可选择仅公钥或禁止 root SSH。重装会清空目标磁盘，需在界面确认。物理机流程不自动调用 BMC 重启，必须在安装文件就绪后手动选择 PXE 启动。

### provisioning 目录需要提交到 GitHub 吗？

不需要。该目录通过 Git 忽略规则排除，保存安装介质、生成的引导文件和装机运行数据；新拉取仓库后按首次启动步骤创建目录即可。各类文件来源如下：

| 文件 / 内容 | 来源与处理方式 |
| --- | --- |
| BIOS / UEFI 引导文件 | 后端镜像安装 iPXE 软件包；`pxe-services` 启动时从镜像内复制缺失的 `undionly.kpxe`、`ipxe.pxe`、`ipxe.efi`、`snponly.efi` 到 TFTP 根目录。实际可选项以软件包提供的文件为准 |
| `tftp/default.ipxe` | 控制器根据 `PXE_HTTP_BASE` 自动生成；需要已配置该变量，且文件尚不存在 |
| 每台机器的安装引导与 NoCloud 应答 | 提交有效装机任务后由平台生成；应答文件加密保存，不能作为公开仓库素材提交 |
| 内核、initrd 和安装介质缓存 | 执行器从所选 ISO 校验、提取并缓存，无需手动上传到 GitHub |
| 操作系统 ISO | 不随仓库提供，也不会凭空生成。通过镜像仓库上传 / 下载准备；创建测试 VM 仍需先准备指定的本地 Ubuntu 22.04.5 ISO，见 [VM 指南](PXE_VM_TEST.md) |

自动初始化只补齐缺失文件，不覆盖已有引导文件。修改 `PXE_HTTP_BASE` 后须同步已有 `default.ipxe`。自行定制的引导文件应单独备份；迁移现有部署还需保留安装介质与运行状态，不能用重新拉取代码代替数据备份。

## 架构与代码

```text
浏览器 → HAProxy → Vue 静态页面 / Go API
                            │
                         PostgreSQL
                            │
                 worker（操作与装机执行）
                 scheduler（周期采集调度）
                            │
              PXE 控制器（宿主网络，DHCP/TFTP/QEMU）

镜像存储：MinIO / 受管本地 ISO 目录
```

| 目录 / 文件 | 用途 |
| --- | --- |
| [backend/](backend/README.md) | Go 服务、迁移、运行模式与测试说明 |
| [frontend/](frontend/README.md) | Vue 页面、开发代理、构建与测试说明 |
| [backend/internal/platform/](backend/internal/platform/) | 当前 API、权限、任务、审计、监控及装机业务实现 |
| [backend/internal/pxeservice/](backend/internal/pxeservice/) | dnsmasq 服务监管、宿主机选项与固定测试 VM |
| [backend/migrations/](backend/migrations/) | 目标结构、增量迁移、校验和及应用账户授权 |
| [deploy/](deploy/) | HAProxy、PostgreSQL、MinIO 等部署文件 |
| [docker-compose.yaml](docker-compose.yaml) | 单宿主机部署定义 |
| [PLATFORM_AUDIT.md](PLATFORM_AUDIT.md) | 历史验收记录；记录中的旧功能与旧环境不代表当前能力 |

当前任务执行使用 PostgreSQL `operations` / `task_outbox` 持久化及锁控制。Compose 保留 Redis 服务，但不能仅因依赖中存在 Asynq 就将当前任务流程描述为 Redis 队列。仓库中的旧 handler / repository / task 代码也不能替代实际入口判断。

## 开发与检查

前后端的安装、运行、测试和代理参数分别见 [后端开发说明](backend/README.md) 与 [前端开发说明](frontend/README.md)。本地常用启动方式：

```bash
bash dev.sh deps
bash dev.sh migrate
bash dev.sh backend
```

另开终端运行 worker、scheduler 和前端。直接连本地 Go API 时，前端命令为：

```bash
BMM_API_TARGET=http://localhost:8000 bash dev.sh frontend
```

`dev.sh deps` 只启动依赖，不启动 PXE 控制器；宿主机直接运行 API / worker 也不会自动获得容器中的控制 socket 和介质挂载。需要真实 PXE / SOL 时，优先保留 Compose 对应服务，使用前端连接它们。

## 升级与备份

先备份 PostgreSQL、MinIO、`provisioning/`、PXE 状态卷以及部署密钥，并保存当前应用版本。升级前确认没有活动安装，安排 DHCP / TFTP 维护窗口。

```bash
docker compose build
docker compose stop backend worker scheduler
docker compose run --rm migrate
docker compose up -d backend worker scheduler frontend lb
```

上述命令更新业务服务；更新 PXE 控制器需在维护窗口单独执行，重启后重新确认配置并手动启用 DHCP / TFTP。迁移失败时先排查日志，不继续启动新应用。

迁移从 `003_target_schema` 建立目标结构，再执行增量版本；已应用 SQL 有校验和，不可修改后重放。API / worker / scheduler 启动只校验迁移，不自动改表。缺少目标迁移记录的非空旧库不会被自动接管，应使用独立新库规划迁移。数据库 owner 仅用于迁移，业务运行使用应用账户。

数据库备份示例（使用当前容器内配置，不在命令中填写口令）：

```bash
docker compose exec -T postgres sh -c 'pg_dump -U "$POSTGRES_USER" -d "$POSTGRES_DB" -Fc' > baremetal-manager.dump
```

备份含敏感资产和凭据数据，应与密钥分别妥善保管，并在隔离环境验证恢复。不要使用 `docker compose down -v` 升级，也不要将 [start.sh](start.sh) 用作日常启动脚本：该旧脚本包含删除匹配数据卷的操作。

## 常见问题

| 现象 | 优先检查 |
| --- | --- |
| 首次启动失败 | `.env` 必填值、owner / 应用口令是否对应、卷是否已被其他部署占用、`migrate` 退出信息 |
| PXE 页面控制器不可用 | `pxe-services` 日志、`/dev/kvm` / TUN 设备、数据库端口 `DB_PORT` 与 `POSTGRES_PORT` 是否一致 |
| 获取地址后无法下载系统 | 客户端能否访问 `PXE_HTTP_BASE`、HTTP 端口和 TFTP 数据端口；`default.ipxe` 是否仍引用旧入口 |
| VM 重启后没进入安装器 | 应从实验室提交 PXE 安装；“从磁盘重启”只启动现有系统；当前 VM 安装回调固定使用宿主 18080 端口，见 VM 指南 |
| 一直安装中或失败后无法再装 | 查看任务步骤与截止时间、worker 是否运行、是否有清理待确认；物理机须先关机再确认停止 |
| 任务 / 审计显示容器 IP | 检查实际入口与可信代理配置；更新时使用的环境文件必须一致 |
| SOL 连接失败 | BMC 协议与权限、`ipmitool`、WebSocket 代理以及 `SOL_ALLOWED_ORIGIN` 是否与浏览器地址一致 |
| 普通用户看不到机器 | 当前组织 / 项目、组织 / 部门授权及服务器部门归属是否匹配 |

MinIO 用于对象文件，不是 OCI 容器镜像仓库。Compose 管理单宿主机，不提供开箱即用的多机高可用；多节点需要另行设计共享数据库、对象存储、调度和入口。
