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
  '/firmware'
]

// 过滤出仍使用通用 ResourceView 的资源
const catalogSections = sections.filter(s => !dedicatedRoutePaths.includes(s.path))

const router = createRouter({ history: createWebHistory(), routes: [
  { path: '/login', component: () => import('./views/LoginView.vue'), meta: { public: true } },
  { path: '/', component: () => import('./components/ShellLayout.vue'), children: [
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
    { path: 'api-keys', component: () => import('./views/ApiKeysView.vue'), meta: { title: 'API 密钥', permission: 'iam:write' } },
    { path: 'project-members', component: () => import('./views/ProjectMembersView.vue'), meta: { title: '项目成员', permission: 'projects:write' } },
    { path: 'pxe-config', component: () => import('./views/PXEConfigView.vue'), meta: { title: 'PXE 配置', permission: 'network:write' } },
    { path: 'system-settings', component: () => import('./views/SystemSettingsView.vue'), meta: { title: '系统设置', permission: 'settings:write' } },
    { path: 'user-management', component: () => import('./views/UserManageView.vue'), meta: { title: '用户管理', permission: 'iam:write' } },
    { path: 'role-management', component: () => import('./views/RoleManageView.vue'), meta: { title: '角色权限', permission: 'iam:read' } },
    // === 资产详情与 SOL 控制台 ===
    { path: 'assets/:id', component: () => import('./views/AssetDetailView.vue'), meta: { title: '资产详情', permission: 'assets:read' } },
    { path: 'assets/:id/sol', component: () => import('./views/SolView.vue'), meta: { title: 'SOL 串口控制台', permission: 'assets:operate' } }
  ] },
  { path: '/:pathMatch(.*)*', redirect: '/' }
] })

router.beforeEach(async to => {
  const auth = useAuth()
  if (to.meta.public) return auth.token ? '/' : true
  if (!auth.token) return '/login'
  if (!auth.user) { try { await auth.loadUser() } catch { auth.logout(); return '/login' } }
  if (to.meta.permission && !auth.can(String(to.meta.permission))) return '/'
})

export default router
