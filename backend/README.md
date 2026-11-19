# 后端开发说明

[返回项目说明](../README.md)。后端使用 Go 1.26、Gin 和 GORM / PostgreSQL；容器运行环境还提供 `ipmitool`、`xorriso`、dnsmasq、QEMU、OVMF 和 iPXE。

## 实际入口

入口为 [cmd/server/main.go](cmd/server/main.go)，默认运行 `api`。所有模式均要求 `DATABASE_URL`；业务 API、worker、scheduler 还要求至少 32 字节的 `JWT_SECRET` 和恰好 32 字节的 `BMC_ENCRYPTION_KEY`。

| 命令模式 | 职责 |
| --- | --- |
| `api` | HTTP API，默认监听 `:8000`，可用 `API_LISTEN_ADDR` 调整 |
| `worker` | 从数据库 outbox 领取并执行操作、镜像下载与装机任务，推进安装阶段及清理 |
| `scheduler` | 周期监控 / 采集调度；部署时保持单实例，扩容需先核对并发与调度策略 |
| `migrate` | 显式结构迁移、初始化种子、应用角色授权；必须使用数据库 owner |
| `pxe-services` | 宿主网络控制器，监管 DHCP / TFTP 和固定测试 VM；通过 Unix socket 向 API / worker 提供控制能力 |

API 以 `/api/v1` 为基址。健康检查为 `/health` 和 `/ready`。接口清单可在平台“API 文档”查看；以 [实际注册路由](internal/platform/routes.go) 为准，不根据旧 OpenAPI 文件推断已实现功能。

## 目录

- [internal/platform/](internal/platform/)：当前运行的权限、资源、任务、装机、监控、用户和审计逻辑。
- [internal/pxeservice/](internal/pxeservice/)：宿主网卡发现、引导文件、dnsmasq、Option 82 固定地址及 QEMU。
- [internal/adapter/](internal/adapter/)：设备和通知协议适配。
- [internal/service/](internal/service/)：复用的安装渲染、凭据加密等组件。
- [migrations/](migrations/)：目标结构、增量迁移、校验及数据库账户授权。

仓库保留部分旧分层实现。修改前从入口追踪调用关系；当前任务使用 PostgreSQL `operations` / `task_outbox`，不是旧 Asynq worker 链路。

## 本地运行

从仓库根目录根据 [.env.example](../.env.example) 配置 `.env`，完成数据库 owner、应用账户、MinIO 和密钥设置；不要覆盖已有环境文件。

```bash
bash dev.sh deps
bash dev.sh migrate
bash dev.sh backend
```

另开终端，分别运行：

```bash
bash dev.sh worker
```

```bash
bash dev.sh scheduler
```

[dev.sh](../dev.sh) 将数据库、Redis、MinIO 的容器地址转换为本机映射端口，不输出连接口令。它需要 Node.js 解析数据库 URL；默认使用 PostgreSQL 15432、Redis 16379、MinIO 19000，可通过模板中对应变量调整。后端 `go run` 本身不会自动读取根目录 `.env`，因此建议通过脚本运行。

真实 PXE 建议使用 Compose：API / worker 需要控制 socket、TFTP 目录及正确权限；控制器还需宿主网络和设备权限。`dev.sh deps` 不启动控制器。SOL 需要可执行的 `ipmitool`，ISO 准备需要 `xorriso`；纯 Go 编译成功不代表这些外部依赖已具备。

## 检查与测试

在本目录执行：

```bash
go test ./...
go vet ./...
go build -o /tmp/bmm-server ./cmd/server
```

依赖数据库、真实 ISO 或网络命名空间的测试默认可能跳过。需要集成覆盖时，先创建与运行环境完全隔离的一次性数据库：

| 环境变量 | 用途 |
| --- | --- |
| `PLATFORM_TEST_DSN` | 平台集成测试所用数据库；先用 `migrate` 准备目标结构，按测试要求配置 owner 权限 |
| `BMM_MIGRATION_TEST_DSN` | 迁移测试使用的空数据库；测试会修改数据库结构，不能指向生产库 |
| `BMM_REAL_ISO_TEST_FILE` | 真实 Ubuntu ISO 提取测试文件路径，需要 `xorriso` |
| `BMM_PXE_RELAY_NETNS_TEST=1` | Linux root 下运行真实 Relay 报文测试，需要 dnsmasq、iproute2 和网络命名空间能力 |

```bash
go test ./internal/platform ./internal/pxeservice ./migrations -count=1
```

是否实际运行集成测试须检查输出中的 `SKIP`，不能仅凭退出码声称数据库或物理机已验收。不要为了跑测试改变宿主机现网 DHCP 或正在执行的安装。

## 修改业务逻辑时

- 资源读写和机器操作必须使用同一组织 / 项目 / 部门范围过滤；不能只隐藏前端按钮。
- 多步机器操作通过持久化任务处理，保留幂等键、设备锁、截止时间和失败清理；页面刷新不能延长任务时限。
- 人工操作需要有对象名称、来源 IP、结果及当时的部门归属。后台自动采集保持静默；审计不能记录密码、令牌或请求原文。
- 新结构通过新增增量迁移实现，同步迁移注册和目标版本。不要改动已应用迁移、使用 `AutoMigrate`，或让运行账户承担结构变更。
- 安装配置、回调令牌和初始密码需要按现有加密及有效期机制处理，不能通过普通资源接口回显。

更多使用边界见 [审计说明](../AUDIT_LOGS.md)、[VM 指南](../PXE_VM_TEST.md) 和 [物理机指南](../PXE_PHYSICAL_TEST.md)。
