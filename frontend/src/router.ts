import { createRouter, createWebHistory } from 'vue-router'
import { useAuth } from './stores/auth'
import { sections } from './catalog'

// 使用专用组件的页面路径（不走通用 ResourceView）
const dedicatedRoutePaths = [
  '/alert-rules',
  '/alert-channels',
  '/kickstart-templates',
  '/api-keys',
  '/project-members',
  '/datacenters',
  '/tags',
  // 服务器列表使用专用视图 ServerListView（替代通用 ResourceView）
  '/assets',
  '/installations',
  '/firmware',
  '/users',
  '/roles',
  '/settings',
  '/pxe',
  // 新增专用路由
  '/departments',
  '/asset-depreciation',
  '/warranty-alerts',
  '/products',
  '/orders',
  '/bills',
  '/accounts',
  '/payments',
  '/invoices',
  '/coupons',
  '/work-orders',
  '/operations-dashboard'
]

// 过滤出仍使用通用 ResourceView 的资源
const catalogSections = sections.filter(s => !dedicatedRoutePaths.includes(s.path))

const router = createRouter({
  history: createWebHistory(),
  routes: [
    // === 公开路由（白名单：/login、/register、/setup，不做认证重定向）===
    { path: '/login', component: () => import('./views/LoginView.vue'), meta: { public: true } },
    // 租户自助注册：仅对外运营模式开放，由后端 registerTenant 做模式与开关校验
    { path: '/register', component: () => import('./views/RegisterView.vue'), meta: { public: true } },
    {
      path: '/setup',
      component: () => import('./views/SetupView.vue'),
      meta: { public: true },
      children: [
        { path: ':step?', component: () => import('./views/SetupView.vue'), meta: { public: true } }
      ]
    },

    // === ShellLayout 子路由（需认证）===
    {
      path: '/',
      component: () => import('./components/ShellLayout.vue'),
      children: [
        { path: '', component: () => import('./views/DashboardView.vue'), meta: { title: '总览' } },

        // 通用资源页面（从 catalog 动态生成）
        ...catalogSections.map(section => ({
          path: section.path.slice(1),
          component: () => import('./views/ResourceView.vue'),
          props: { section },
          meta: { title: section.title, permission: section.permission }
        })),

        // === 专用页面路由（使用独立组件）===
        { path: 'datacenters', component: () => import('./views/DatacenterView.vue'), meta: { title: '机房管理', permission: 'datacenters:read' } },
        { path: 'tags', component: () => import('./views/TagManage.vue'), meta: { title: '标签管理', permission: 'assets:write' } },
        { path: 'alert-rules', component: () => import('./views/AlertRuleView.vue'), meta: { title: '告警规则', permission: 'alerts:manage' } },
        { path: 'alert-channels', component: () => import('./views/AlertChannelView.vue'), meta: { title: '推送渠道', permission: 'alerts:manage' } },
        { path: 'installations', component: () => import('./views/InstallPlanView.vue'), meta: { title: '装机方案', permission: 'tasks:read' } },
        { path: 'kickstart-templates', component: () => import('./views/KickstartTemplateView.vue'), meta: { title: '装机模板', permission: 'images:write' } },
        { path: 'firmware', component: () => import('./views/FirmwareUpgradeView.vue'), meta: { title: '固件升级', permission: 'firmware:read' } },
        { path: 'api-keys', component: () => import('./views/ApiKeysView.vue'), meta: { title: 'API 密钥', permission: 'iam:read' } },
        { path: 'api-docs', component: () => import('./views/ApiDocsView.vue'), meta: { title: 'API 文档' } },
        { path: 'project-members', component: () => import('./views/ProjectMembersView.vue'), meta: { title: '项目成员', permission: 'projects:write' } },
        { path: 'pxe-config', component: () => import('./views/PXEConfigView.vue'), meta: { title: 'PXE 配置', permission: 'network:write' } },
        { path: 'system-settings', component: () => import('./views/SystemSettingsView.vue'), meta: { title: '系统设置', permission: 'settings:read' } },
        { path: 'user-management', component: () => import('./views/UserManageView.vue'), meta: { title: '用户管理', permission: 'iam:write' } },
        { path: 'role-management', component: () => import('./views/RoleManageView.vue'), meta: { title: '角色权限', permission: 'iam:read' } },

        // === 资产详情与 SOL 控制台 ===
        { path: 'assets', component: () => import('./views/ServerListView.vue'), meta: { title: '服务器管理', permission: 'assets:read' } },
        { path: 'assets/:id', component: () => import('./views/AssetDetailView.vue'), meta: { title: '资产详情', permission: 'assets:read' } },
        { path: 'assets/:id/sol', component: () => import('./views/SolView.vue'), meta: { title: 'SOL 串口控制台', permission: 'assets:operate' } },

        // === 自用模式专用路由 ===
        { path: 'departments', component: () => import('./views/DepartmentView.vue'), meta: { title: '部门管理', permission: 'departments:read', mode: 'self_use' } },
        // 资产折旧与维保告警使用专用视图：两者响应形态不同于通用 ResourceView 契约
        // （折旧详情需 has_depreciation 空态；维保告警仅返回 items/total/alert_days，无分页字段）
        { path: 'asset-depreciation', component: () => import('./views/AssetDepreciationView.vue'), meta: { title: '资产折旧', permission: 'assets:read', mode: 'self_use' } },
        { path: 'warranty-alerts', component: () => import('./views/WarrantyAlertsView.vue'), meta: { title: '维保告警', permission: 'assets:read', mode: 'self_use' } },

        // === 对外运营模式专用路由 ===
        { path: 'tenant-console', component: () => import('./views/TenantConsoleView.vue'), meta: { title: '服务器管理', permission: 'assets:read', mode: 'external_operation' } },
        { path: 'purchase', component: () => import('./views/PurchaseView.vue'), meta: { title: '采购管理', permission: 'orders:write', mode: 'external_operation' } },
        { path: 'billing', component: () => import('./views/BillingView.vue'), meta: { title: '计费管理', permission: 'billing:read', mode: 'external_operation' } },
        // 订单 / 发票 / 优惠券 / 工单使用专用视图：后端写语义为路径参数流转（PUT /orders/:id/:transition、
        // PUT /invoices/:id/:status、PUT /work-orders/:id/:transition）或不存在通用 CRUD，ResourceView 无法表达
        { path: 'orders', component: () => import('./views/OrdersView.vue'), meta: { title: '订单管理', permission: 'orders:read', mode: 'external_operation' } },
        { path: 'bills', component: () => import('./views/ResourceView.vue'), props: { section: sections.find(s => s.path === '/bills') }, meta: { title: '账单管理', permission: 'billing:read', mode: 'external_operation' } },
        { path: 'accounts', component: () => import('./views/ResourceView.vue'), props: { section: sections.find(s => s.path === '/accounts') }, meta: { title: '账户管理', permission: 'finance:read', mode: 'external_operation' } },
        { path: 'payments', component: () => import('./views/ResourceView.vue'), props: { section: sections.find(s => s.path === '/payments') }, meta: { title: '支付记录', permission: 'finance:read', mode: 'external_operation' } },
        { path: 'invoices', component: () => import('./views/InvoiceView.vue'), meta: { title: '发票管理', permission: 'finance:read', mode: 'external_operation' } },
        { path: 'coupons', component: () => import('./views/CouponView.vue'), meta: { title: '优惠券', permission: 'marketing:read', mode: 'external_operation' } },
        // 工单后端无模式门禁（非 admin 仅可见自己创建的工单），自用与运营两种模式均可访问
        { path: 'work-orders', component: () => import('./views/WorkOrdersView.vue'), meta: { title: '工单工作台', permission: 'work_orders:read', mode: 'both' } },
        { path: 'operations-dashboard', component: () => import('./views/OperationsDashboardView.vue'), meta: { title: '运营数据大盘', permission: 'operations:read', mode: 'external_operation' } },
        { path: 'products', component: () => import('./views/ResourceView.vue'), props: { section: sections.find(s => s.path === '/products') }, meta: { title: '产品管理', permission: 'products:read', mode: 'external_operation' } }
      ]
    },

    { path: '/:pathMatch(.*)*', redirect: '/' }
  ]
})

// === 路由守卫 ===
router.beforeEach(async to => {
  const auth = useAuth()

  // 1. 公开路由直接放行
  if (to.meta.public) {
    // /setup 路由特殊处理：系统未初始化时允许已登录用户访问（初始化向导需要认证）
    if (to.path === '/setup' || to.path.startsWith('/setup/')) {
      if (auth.setupStatus === null) {
        await auth.checkSetupStatus()
      }
      // 系统已初始化完成，已登录用户跳转到首页
      if (auth.setupStatus?.initialized && auth.token) return '/'
      return true
    }
    // 其他公开路由（/login、/register），已登录用户重定向到首页
    return auth.token ? '/' : true
  }

  // 2. 检查系统初始化状态
  // 仅在尚未检查过时调用 API（避免每次导航都请求）
  if (auth.setupStatus === null) {
    await auth.checkSetupStatus()
  }

  // 系统模式尚未确定（未初始化或状态接口不可用）时，强制跳转到 /setup
  if (!auth.systemMode && to.path !== '/setup' && !to.path.startsWith('/setup/')) {
    return '/setup'
  }

  // 3. 认证检查
  if (!auth.token) return '/login'
  if (!auth.user) {
    try {
      await auth.loadUser()
    } catch {
      auth.logout()
      return '/login'
    }
  }

  // 运营租户使用已购服务器工作台，不进入通用资产录入页面。
  if (auth.systemMode === 'external_operation' && !auth.user?.is_platform_admin && to.path === '/assets') {
    return '/tenant-console'
  }

  // 4. 权限检查
  if (to.meta.permission && !auth.can(String(to.meta.permission))) return '/'

  // 5. 模式过滤：检查当前路由是否允许在当前系统模式下访问
  const routeMode = to.meta.mode as string | undefined
  if (routeMode && routeMode !== 'both') {
    if (!auth.canAccessMode(routeMode)) {
      return '/'
    }
  }
})

export default router
