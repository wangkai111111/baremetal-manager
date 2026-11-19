# 前端开发说明

[返回项目说明](../README.md)。使用 Vue 3、TypeScript、Vite 6、Naive UI、Pinia 和 Vue Router；图表使用 ECharts，SOL 使用 xterm，固定 IP 规则 Excel 导入使用 SheetJS。

## 安装和启动

推荐 Node.js 22.18+，使现有 TypeScript 测试可直接通过 Node 运行。容器构建使用 Node.js 22。

```bash
npm ci
npm run dev
```

开发服务默认监听所有地址，端口 5173。开发代理默认将 `/api/` 请求（含 WebSocket）转发至 `http://localhost:18080`；如后端 Compose 使用默认 8080，或直接运行本地 Go API，请指定实际入口：

```bash
BMM_API_TARGET=http://localhost:8080 npm run dev
```

```bash
BMM_API_TARGET=http://localhost:8000 npm run dev
```

也可以从仓库根目录运行：

```bash
BMM_API_TARGET=http://localhost:8000 bash dev.sh frontend
```

根目录脚本需要已配置 `.env`。开发服务不包含后端、数据库或 PXE 控制器，启动前先按 [后端说明](../backend/README.md) 准备。

## 构建与测试

在本目录执行：

```bash
npm run build
node --test tests/*.test.ts
```

`build` 先执行 `vue-tsc --noEmit`，再输出 Vite 产物到 `dist/`。生产镜像通过 Nginx 在容器 8080 提供静态文件，统一由 Compose HAProxy 路由 API；不要把 Vite 开发服务当作装机镜像下载入口。

现有 Node 测试覆盖权限、部门、显示格式、装机访问设置、PXE 选项、固定地址规则 / 导入、来源 IP 和通知选项等工具逻辑。它们不替代浏览器交互与真实装机验收。

## 页面与共享逻辑

| 位置 | 用途 |
| --- | --- |
| [src/router.ts](src/router.ts) | 路由、登录和首次改密守卫、页面权限入口 |
| [src/catalog.ts](src/catalog.ts) | 菜单与通用资源页面定义 |
| [src/api.ts](src/api.ts) | API 请求、响应和错误处理 |
| [src/stores/auth.ts](src/stores/auth.ts) | 登录身份、组织 / 项目上下文、权限及退出 |
| [src/stores/brand.ts](src/stores/brand.ts) | 平台名称、主题和品牌配置 |
| [src/views/](src/views/) | 服务器、用户、部门、审计、网络、PXE 等页面 |
| [src/components/](src/components/) | 测试实验室、安装进度、访问凭据、授权和规则导入组件 |
| [src/utils/](src/utils/) | 格式化、权限、网络 / 装机表单校验和复制能力 |
| [dev/clientIp.ts](dev/clientIp.ts) | 开发代理的访问者来源 IP 转发 |
| [tests/](tests/) | Node 原生测试 |

## 开发约定

- 权限以服务端为准；切换组织 / 项目后重新读取对应权限和数据。普通用户的服务器操作入口应与后端授权一致，服务器创建 / 分配等管理功能需单独控制。
- 使用对象名称、序列号、IP 和部门帮助定位；接口仍可使用内部 ID，但不要将其作为用户主要识别信息。
- 空态、接口错误、处理中和成功分别显示；不要用模拟数据或定时器伪造真实任务进度。
- 装机表单中的密码和公钥在关闭、成功或切换上下文时清理；展示初始凭据须走专用接口，不缓存到持久化浏览器存储。
- 主题通过统一状态与组件主题同步，检查刷新后的顶栏和内容；宽表在自身容器内滚动，弹窗窄屏可滚动且按钮可达。
- 代理匹配 `/api/`，保留 `/api-keys`、`/api-docs` 等页面路由。操作来源 IP 还需部署入口的可信代理配置，详见 [项目说明](../README.md)。

涉及电源、清盘重装、删除 VM、启停 DHCP 或外部邮件推送的浏览器验收，应明确测试对象与范围。页面打开成功不等于这些操作已执行通过。
