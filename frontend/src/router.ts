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
  '/racks',
  '/tags',
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
    // === 公开路由 ===
    { path: '/login', component: () => import('./views/LoginView.vue'), meta: { public: true } },
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
        { path: 'racks', component: () => import('./views/RackView.vue'), meta: { title: '机柜管理', permission: 'racks:read' } },
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
        { path: 'assets/:id', component: () => import('./views/AssetDetailView.vue'), meta: { title: '资产详情', permission: 'assets:read' } },
        { path: 'assets/:id/sol', component: () => import('./views/SolView.vue'), meta: { title: 'SOL 串口控制台', permission: 'assets:operate' } },

        // === 自用模式专用路由 ===
        { path: 'departments', component: () => import('./views/DepartmentView.vue'), meta: { title: '部门管理', permission: 'departments:read', mode: 'self_use' } },
        { path: 'asset-depreciation', component: () => import('./views/ResourceView.vue'), props: { section: sections.find(s => s.path === '/asset-depreciation') }, meta: { title: '资产折旧', permission: 'assets:read', mode: 'self_use' } },
        { path: 'warranty-alerts', component: () => import('./views/ResourceView.vue'), props: { section: sections.find(s => s.path === '/warranty-alerts') }, meta: { title: '维保告警', permission: 'assets:read', mode: 'self_use' } },

        // === 对外运营模式专用路由 ===
        { path: 'tenant-console', component: () => import('./views/TenantConsoleView.vue'), meta: { title: '租户控制台', permission: 'projects:read', mode: 'external_operation' } },
        { path: 'purchase', component: () => import('./views/PurchaseView.vue'), meta: { title: '采购管理', permission: 'orders:write', mode: 'external_operation' } },
        { path: 'billing', component: () => import('./views/BillingView.vue'), meta: { title: '计费管理', permission: 'billing:read', mode: 'external_operation' } },
        { path: 'orders', component: () => import('./views/ResourceView.vue'), props: { section: sections.find(s => s.path === '/orders') }, meta: { title: '订单管理', permission: 'orders:read', mode: 'external_operation' } },
        { path: 'bills', component: () => import('./views/ResourceView.vue'), props: { section: sections.find(s => s.path === '/bills') }, meta: { title: '账单管理', permission: 'billing:read', mode: 'external_operation' } },
        { path: 'accounts', component: () => import('./views/ResourceView.vue'), props: { section: sections.find(s => s.path === '/accounts') }, meta: { title: '账户管理', permission: 'finance:read', mode: 'external_operation' } },
        { path: 'payments', component: () => import('./views/ResourceView.vue'), props: { section: sections.find(s => s.path === '/payments') }, meta: { title: '支付记录', permission: 'finance:read', mode: 'external_operation' } },
        { path: 'invoices', component: () => import('./views/ResourceView.vue'), props: { section: sections.find(s => s.path === '/invoices') }, meta: { title: '发票管理', permission: 'finance:read', mode: 'external_operation' } },
        { path: 'coupons', component: () => import('./views/ResourceView.vue'), props: { section: sections.find(s => s.path === '/coupons') }, meta: { title: '优惠券', permission: 'marketing:read', mode: 'external_operation' } },
        { path: 'work-orders', component: () => import('./views/ResourceView.vue'), props: { section: sections.find(s => s.path === '/work-orders') }, meta: { title: '工单管理', permission: 'work_orders:read', mode: 'external_operation' } },
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
    // 已登录用户访问 /login 或 /setup 时重定向到首页
    return auth.token ? '/' : true
  }

  // 2. 检查系统初始化状态
  // 仅在尚未检查过时调用 API（避免每次导航都请求）
  if (auth.setupStatus === null) {
    await auth.checkSetupStatus()
  }

  // 系统未初始化时，强制跳转到 /setup
  if (auth.systemMode === 'uninitialized' && to.path !== '/setup' && !to.path.startsWith('/setup/')) {
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
