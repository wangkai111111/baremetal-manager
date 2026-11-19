import { createRouter, createWebHistory } from 'vue-router'
import { useAuth } from './stores/auth'
import { sections } from './catalog'

// 使用专用组件的页面路径（不走通用 ResourceView）
const dedicatedRoutePaths = [
  '/audit-logs',
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
  '/images',
  '/firmware',
  '/users',
  '/roles',
  '/settings',
  '/pxe',
  // 新增专用路由
  '/departments',
  '/asset-depreciation',
  '/warranty-alerts',
  '/work-orders',
]

// 过滤出仍使用通用 ResourceView 的资源
const catalogSections = sections.filter(s => !dedicatedRoutePaths.includes(s.path))

const router = createRouter({
  history: createWebHistory(),
  routes: [
    // === 公开路由（白名单：/login、/setup，不做认证重定向）===
    { path: '/login', component: () => import('./views/LoginView.vue'), meta: { public: true } },
    { path: '/change-password', component: () => import('./views/ChangePasswordView.vue'), meta: { title: '修改初始密码' } },
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

        { path: 'users', redirect: '/user-management' },
        { path: 'roles', redirect: '/role-management' },
        { path: 'settings', redirect: '/system-settings' },
        { path: 'pxe', redirect: '/pxe-config' },

        // === 专用页面路由（使用独立组件）===
        { path: 'audit-logs', component: () => import('./views/AuditLogsView.vue'), meta: { title: '审计日志', permission: 'audit:read' } },
        { path: 'datacenters', component: () => import('./views/DatacenterView.vue'), meta: { title: '机房管理', permission: 'datacenters:read' } },
        { path: 'tags', component: () => import('./views/TagManage.vue'), meta: { title: '标签管理', permission: 'assets:write' } },
        { path: 'alert-rules', component: () => import('./views/AlertRuleView.vue'), meta: { title: '告警规则', permission: 'alerts:read' } },
        { path: 'alert-channels', component: () => import('./views/AlertChannelView.vue'), meta: { title: '推送渠道', permission: 'alerts:read' } },
        { path: 'images', component: () => import('./views/ImagesView.vue'), meta: { title: '镜像仓库', permission: 'images:read' } },
        { path: 'installations', redirect: '/images' },
        { path: 'kickstart-templates', redirect: '/images' },
        { path: 'firmware', component: () => import('./views/FirmwareUpgradeView.vue'), meta: { title: '固件升级', permission: 'firmware:read' } },
        { path: 'api-keys', component: () => import('./views/ApiKeysView.vue'), meta: { title: 'API 密钥', permission: 'iam:write' } },
        { path: 'api-docs', component: () => import('./views/ApiDocsView.vue'), meta: { title: 'API 文档' } },
        { path: 'project-members', component: () => import('./views/ProjectMembersView.vue'), meta: { title: '项目成员', permission: 'iam:write' } },
        { path: 'pxe-config', component: () => import('./views/PXEConfigView.vue'), meta: { title: 'PXE 配置', permission: 'network:write' } },
        { path: 'system-settings', component: () => import('./views/SystemSettingsView.vue'), meta: { title: '系统设置', permission: 'settings:read' } },
        { path: 'user-management', component: () => import('./views/UserManageView.vue'), meta: { title: '用户管理', permission: 'iam:manage' } },
        { path: 'role-management', component: () => import('./views/RoleManageView.vue'), meta: { title: '角色权限', permission: 'iam:write' } },

        // === 资产详情与 SOL 控制台 ===
        { path: 'assets', component: () => import('./views/ServerListView.vue'), meta: { title: '服务器管理', permission: 'assets:read' } },
        { path: 'assets/:id', component: () => import('./views/AssetDetailView.vue'), meta: { title: '资产详情', permission: 'assets:read' } },
        { path: 'assets/:id/sol', component: () => import('./views/SolView.vue'), meta: { title: 'SOL 串口控制台', permission: 'sol:access' } },

        // === 组织与资产管理 ===
        { path: 'departments', component: () => import('./views/DepartmentView.vue'), meta: { title: '部门管理', permission: 'departments:read' } },
        // 资产折旧与维保告警使用专用视图：两者响应形态不同于通用 ResourceView 契约
        // （折旧详情需 has_depreciation 空态；维保告警仅返回 items/total/alert_days，无分页字段）
        { path: 'asset-depreciation', component: () => import('./views/AssetDepreciationView.vue'), meta: { title: '资产折旧', permission: 'assets:read' } },
        { path: 'warranty-alerts', component: () => import('./views/WarrantyAlertsView.vue'), meta: { title: '维保告警', permission: 'assets:read' } },

        { path: 'work-orders', component: () => import('./views/WorkOrdersView.vue'), meta: { title: '工单工作台', permission: 'work_orders:read' } },
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
    // /setup 路由特殊处理：系统未初始化时允许访问初始化向导
    if (to.path === '/setup' || to.path.startsWith('/setup/')) {
      if (auth.setupStatus === null) {
        await auth.checkSetupStatus()
      }
      // 系统已初始化完成，已登录用户跳转到首页
      if (auth.setupStatus?.initialized && auth.token) return '/'
      return true
    }
    // 其他公开路由（/login），已登录用户重定向到首页
    return auth.token ? '/' : true
  }

  // 2. 检查系统初始化状态
  // 仅在尚未检查过时调用 API（避免每次导航都请求）
  if (auth.setupStatus === null) {
    await auth.checkSetupStatus()
  }

  // 系统尚未初始化（未初始化或状态接口不可用）时，强制跳转到 /setup
  if (!auth.setupStatus?.initialized && to.path !== '/setup' && !to.path.startsWith('/setup/')) {
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

  if (auth.user?.must_change_password && to.path !== '/change-password') return '/change-password'
  if (!auth.user?.must_change_password && to.path === '/change-password') return '/'

  // 4. 权限检查
  if (to.meta.permission && !auth.user?.is_platform_admin) await auth.loadProjectPermissions()
  if (to.meta.permission && !auth.can(String(to.meta.permission))) return '/'

})

export default router
