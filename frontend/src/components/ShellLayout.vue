<script setup lang="ts">
import { computed, ref, onMounted, onUnmounted, h } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import {
  NButton, NIcon, NDropdown, NBadge, NDrawer, NDrawerContent,
  NModal, NForm, NFormItem, NInput, NTag, NDescriptions,
  NDescriptionsItem, NSelect, NAvatar, useMessage
} from 'naive-ui'
import type { MenuOption } from 'naive-ui'
import {
  GridOutline, ServerOutline, LayersOutline, BusinessOutline,
  CubeOutline, DiscOutline, FlashOutline, GitNetworkOutline,
  WarningOutline, HardwareChipOutline, PeopleOutline, SettingsOutline,
  ListOutline, ReceiptOutline, MenuOutline, ChevronDownOutline,
  NotificationsOutline, ChevronBackOutline, ChevronForwardOutline,
  SearchOutline, SunnyOutline, MoonOutline,
  PersonOutline, AnalyticsOutline,
  PricetagsOutline, CardOutline, WalletOutline, DocumentTextOutline,
  TicketOutline, CubeOutline as CubeAltOutline
} from '@vicons/ionicons5'
import { useAuth } from '../stores/auth'
import {
  footerText, footerVisible, loadBrand, navBackground,
  navTextColor, operationMode, platformLogo, platformName, platformTagline
} from '../stores/brand'
import { resource, rows, api, errorText, type RecordData } from '../api'

const route = useRoute()
const router = useRouter()
const message = useMessage()
const auth = useAuth()

// === 响应式状态 ===
const mobileOpen = ref(false)
const collapsed = ref(localStorage.getItem('sidebar_collapsed') === 'true')
const menuSearch = ref('')
const notificationOpen = ref(false)
const notifications = ref<RecordData[]>([])
const unreadCount = ref(0)
const notifLoading = ref(false)
const passwordModal = ref(false)
const passwordForm = ref({ oldPassword: '', newPassword: '', confirmPassword: '' })
const passwordSaving = ref(false)
const settingsModal = ref(false)

// === 主题注入 ===
const isDarkMode = ref(document.documentElement.getAttribute('data-theme') === 'dark')
const toggleDarkMode = () => {
  isDarkMode.value = !isDarkMode.value
  document.documentElement.setAttribute(
    'data-theme',
    isDarkMode.value ? 'dark' : 'light'
  )
  localStorage.setItem('theme', isDarkMode.value ? 'dark' : 'light')
}

// === 计算属性 ===
const projects = computed(() => auth.projects)
const isExternalMode = computed(() => operationMode.value === 'external')

const navStyle = computed<Record<string, string>>(() => ({
  '--nav-bg': navBackground.value,
  '--nav-text': navTextColor.value
}))

const projectOptions = computed(() =>
  projects.value.map(p => ({
    label: String(p.name ?? p.id),
    value: String(p.id)
  }))
)

const tokenExpiryText = computed(() =>
  auth.tokenExpiry > 0 ? new Date(auth.tokenExpiry).toLocaleString('zh-CN') : '未知'
)

const userMenuOptions = computed(() => [
  { label: '个人设置', key: 'settings' },
  { label: '修改密码', key: 'password' },
  { type: 'divider', key: 'd1' },
  { label: '退出登录', key: 'logout' }
])

// === 图标渲染工具 ===
const icon = (c: typeof GridOutline) => () => h(NIcon, null, { default: () => h(c) })

// === 菜单权限映射 ===
function permission(path: string) {
  return ({
    projects: 'projects:read',
    datacenters: 'datacenters:read',
    racks: 'racks:read',
    assets: 'assets:read',
    tags: 'assets:write',
    images: 'images:read',
    'kickstart-templates': 'images:write',
    installations: 'tasks:read',
    'install-plans': 'tasks:read',
    subnets: 'network:read',
    'ip-addresses': 'network:read',
    'ip-pools': 'network:write',
    'pxe-config': 'network:read',
    pxe: 'network:read',
    alerts: 'alerts:read',
    'alert-rules': 'alerts:manage',
    'alert-channels': 'alerts:manage',
    firmware: 'firmware:read',
    tasks: 'tasks:read',
    'audit-logs': 'audit:read',
    'work-orders': 'tasks:read',
    'operations-dashboard': 'alerts:read',
    'user-management': 'iam:read',
    users: 'iam:read',
    'role-management': 'iam:read',
    roles: 'iam:read',
    'api-keys': 'iam:read',
    'system-settings': 'settings:read',
    settings: 'settings:read',
    products: 'projects:read',
    orders: 'projects:read',
    bills: 'projects:read',
    accounts: 'projects:read',
    invoices: 'projects:read',
    coupons: 'projects:read',
    departments: 'projects:read',
    'project-members': 'projects:write',
    'tenant-console': 'projects:read'
  } as Record<string, string>)[path.slice(1)]
}

function canAccess(path: string) {
  const perm = permission(path)
  if (!perm) return true
  // 平台管理专属页面
  if (['/users', '/user-management', '/roles', '/role-management', '/api-keys', '/settings', '/system-settings'].includes(path)) {
    return auth.user?.is_platform_admin === true
  }
  return auth.can(perm)
}

// === 菜单组定义 ===
const allMenuGroups = computed(() => {
  const selfHosted = [
    {
      label: '工作台',
      items: [
        { label: '运行总览', path: '/', icon: GridOutline }
      ]
    },
    {
      label: '资产管理',
      items: [
        { label: '服务器管理', path: '/assets', icon: ServerOutline },
        { label: '部门管理', path: '/departments', icon: LayersOutline },
        { label: '标签管理', path: '/tags', icon: PricetagsOutline }
      ]
    },
    {
      label: '基础设施',
      items: [
        { label: '机房管理', path: '/datacenters', icon: BusinessOutline },
        { label: '机柜管理', path: '/racks', icon: CubeOutline }
      ]
    },
    {
      label: '交付与网络',
      items: [
        { label: '镜像管理', path: '/images', icon: DiscOutline },
        { label: '装机方案', path: '/installations', icon: FlashOutline },
        { label: '装机模板', path: '/kickstart-templates', icon: DiscOutline },
        { label: '子网管理', path: '/subnets', icon: GitNetworkOutline },
        { label: 'IP 地址', path: '/ip-addresses', icon: GitNetworkOutline },
        { label: '地址池', path: '/ip-pools', icon: GitNetworkOutline },
        { label: 'PXE 配置', path: '/pxe-config', icon: GitNetworkOutline }
      ]
    },
    {
      label: '运维治理',
      items: [
        { label: '告警记录', path: '/alerts', icon: WarningOutline },
        { label: '告警规则', path: '/alert-rules', icon: WarningOutline },
        { label: '推送渠道', path: '/alert-channels', icon: WarningOutline },
        { label: '固件升级', path: '/firmware', icon: HardwareChipOutline },
        { label: '任务中心', path: '/tasks', icon: ListOutline },
        { label: '工单管理', path: '/work-orders', icon: DocumentTextOutline },
        { label: '审计日志', path: '/audit-logs', icon: ReceiptOutline }
      ]
    },
    {
      label: '系统设置',
      items: [
        { label: '用户管理', path: '/user-management', icon: PeopleOutline },
        { label: '角色权限', path: '/role-management', icon: PeopleOutline },
        { label: 'API 密钥', path: '/api-keys', icon: SettingsOutline },
        { label: '系统设置', path: '/system-settings', icon: SettingsOutline }
      ]
    }
  ]

  if (!isExternalMode.value) return selfHosted

  // 对外运营模式：追加额外菜单
  return [
    {
      label: '工作台',
      items: [{ label: '运行总览', path: '/', icon: GridOutline }]
    },
    {
      label: '资产管理',
      items: [
        { label: '服务器管理', path: '/assets', icon: ServerOutline },
        { label: '产品管理', path: '/products', icon: CubeAltOutline },
        { label: '部门管理', path: '/departments', icon: LayersOutline },
        { label: '标签管理', path: '/tags', icon: PricetagsOutline }
      ]
    },
    {
      label: '基础设施',
      items: [
        { label: '机房管理', path: '/datacenters', icon: BusinessOutline },
        { label: '机柜管理', path: '/racks', icon: CubeOutline }
      ]
    },
    {
      label: '交付与网络',
      items: [
        { label: '镜像管理', path: '/images', icon: DiscOutline },
        { label: '装机方案', path: '/installations', icon: FlashOutline },
        { label: '装机模板', path: '/kickstart-templates', icon: DiscOutline },
        { label: '子网管理', path: '/subnets', icon: GitNetworkOutline },
        { label: 'IP 地址', path: '/ip-addresses', icon: GitNetworkOutline },
        { label: '地址池', path: '/ip-pools', icon: GitNetworkOutline },
        { label: 'PXE 配置', path: '/pxe-config', icon: GitNetworkOutline }
      ]
    },
    {
      label: '计费中心',
      items: [
        { label: '订单管理', path: '/orders', icon: CardOutline },
        { label: '账单管理', path: '/bills', icon: WalletOutline },
        { label: '账户管理', path: '/accounts', icon: PersonOutline },
        { label: '发票管理', path: '/invoices', icon: DocumentTextOutline },
        { label: '优惠券', path: '/coupons', icon: TicketOutline }
      ]
    },
    {
      label: '运维治理',
      items: [
        { label: '运营数据大盘', path: '/operations-dashboard', icon: AnalyticsOutline },
        { label: '告警记录', path: '/alerts', icon: WarningOutline },
        { label: '告警规则', path: '/alert-rules', icon: WarningOutline },
        { label: '推送渠道', path: '/alert-channels', icon: WarningOutline },
        { label: '固件升级', path: '/firmware', icon: HardwareChipOutline },
        { label: '任务中心', path: '/tasks', icon: ListOutline },
        { label: '工单管理', path: '/work-orders', icon: DocumentTextOutline },
        { label: '审计日志', path: '/audit-logs', icon: ReceiptOutline }
      ]
    },
    {
      label: '租户控制台',
      items: [
        { label: '控制台概览', path: '/tenant-console', icon: GridOutline }
      ]
    },
    {
      label: '系统设置',
      items: [
        { label: '用户管理', path: '/user-management', icon: PeopleOutline },
        { label: '角色权限', path: '/role-management', icon: PeopleOutline },
        { label: 'API 密钥', path: '/api-keys', icon: SettingsOutline },
        { label: '系统设置', path: '/system-settings', icon: SettingsOutline }
      ]
    }
  ]
})

// === 菜单搜索过滤 ===
function filterMenuItems(items: any[], search: string): any[] {
  if (!search) return items
  const lower = search.toLowerCase()
  return items.filter(item =>
    item.label.toLowerCase().includes(lower)
  )
}

const filteredMenuGroups = computed(() => {
  const groups = allMenuGroups.value
    .map(group => ({
      ...group,
      items: group.items.filter(item => canAccess(item.path))
    }))
    .filter(group => group.items.length > 0)

  if (!menuSearch.value) return groups

  return groups
    .map(group => ({
      ...group,
      items: filterMenuItems(group.items, menuSearch.value)
    }))
    .filter(group => group.items.length > 0)
})

// === NMenu 选项（用于 NMenu 组件渲染）===
const menuOptions = computed<MenuOption[]>(() =>
  filteredMenuGroups.value.map(group => ({
    type: 'group' as const,
    label: group.label,
    key: group.label,
    children: group.items.map(item => ({
      label: item.label,
      key: item.path,
      icon: icon(item.icon)
    }))
  }))
)

// === 当前激活的菜单路径 ===
const activeMenuPath = computed(() => {
  const path = route.path
  if (path === '/') return '/'
  // 匹配最长路径前缀
  for (const group of allMenuGroups.value) {
    for (const item of group.items) {
      if (item.path !== '/' && path.startsWith(item.path)) {
        return item.path
      }
    }
  }
  return path
})

// === 生命周期 ===
let notifTimer: ReturnType<typeof setInterval> | undefined
let resizeHandler: (() => void) | undefined

onMounted(async () => {
  await auth.loadContext()
  loadNotifications()
  loadBrand()
  notifTimer = setInterval(loadNotifications, 60000)

  // 响应式：窄屏自动折叠
  resizeHandler = () => {
    if (window.innerWidth <= 1100) {
      collapsed.value = true
      localStorage.setItem('sidebar_collapsed', 'true')
    }
  }
  window.addEventListener('resize', resizeHandler)
  resizeHandler()
})

onUnmounted(() => {
  if (notifTimer) clearInterval(notifTimer)
  if (resizeHandler) window.removeEventListener('resize', resizeHandler)
})

// === 折叠切换 ===
function toggleCollapse() {
  collapsed.value = !collapsed.value
  localStorage.setItem('sidebar_collapsed', String(collapsed.value))
}

// === 通知加载 ===
async function loadNotifications() {
  if (!auth.projectId) return
  notifLoading.value = true
  try {
    const result = rows(await resource.list('/alerts', { page: 1, page_size: 5, resolved: false }))
    notifications.value = result.items
    unreadCount.value = result.total
  } catch {
    // 加载通知失败不阻断主流程
  } finally {
    notifLoading.value = false
  }
}

// === 用户菜单 ===
function handleUserMenuSelect(key: string) {
  if (key === 'logout') logout()
  else if (key === 'password') {
    passwordForm.value = { oldPassword: '', newPassword: '', confirmPassword: '' }
    passwordModal.value = true
  } else if (key === 'settings') {
    settingsModal.value = true
  }
}

function logout() {
  auth.logout()
  router.push('/login')
}

// === 修改密码 ===
async function changePassword() {
  if (!passwordForm.value.oldPassword || !passwordForm.value.newPassword) {
    message.warning('请填写完整的密码信息')
    return
  }
  if (passwordForm.value.newPassword !== passwordForm.value.confirmPassword) {
    message.warning('两次输入的密码不一致')
    return
  }
  if (passwordForm.value.newPassword.length < 12) {
    message.warning('新密码长度不能少于 12 位')
    return
  }
  passwordSaving.value = true
  try {
    await api.put('/auth/password', {
      current_password: passwordForm.value.oldPassword,
      new_password: passwordForm.value.newPassword
    })
    message.success('密码修改成功')
    passwordModal.value = false
  } catch (e) {
    message.error(errorText(e))
  } finally {
    passwordSaving.value = false
  }
}

// === 项目/租户切换 ===
function onProjectChange(event: Event) {
  const target = event.target as HTMLSelectElement
  auth.selectProject(target.value)
  notifications.value = []
  unreadCount.value = 0
  router.push('/')
  loadNotifications()
}

function onSettingsProjectChange(value: string) {
  auth.selectProject(value)
  notifications.value = []
  unreadCount.value = 0
  router.push('/')
  loadNotifications()
}

function openPasswordModal() {
  settingsModal.value = false
  passwordForm.value = { oldPassword: '', newPassword: '', confirmPassword: '' }
  passwordModal.value = true
}

function goProjectMembers() {
  router.push('/project-members')
}

// === NMenu 更新选中值 ===
const menuValue = ref<string | null>(null)
function handleMenuSelect(key: string) {
  menuValue.value = key
  router.push(key)
  if (window.innerWidth <= 780) mobileOpen.value = false
}
</script>

<template>
  <div class="app-layout">
    <!-- 移动端遮罩 -->
    <div v-if="mobileOpen" class="sidebar-backdrop" @click="mobileOpen = false"></div>

    <!-- ===== 侧边栏 ===== -->
    <aside
      class="sidebar"
      :class="{ open: mobileOpen, collapsed }"
      :style="navStyle"
    >
      <!-- 品牌区域 -->
      <router-link to="/" class="brand" @click="mobileOpen = false">
        <img v-if="platformLogo" class="brand-logo" :src="platformLogo" alt="Logo" />
        <span v-else class="brand-mark">M<span>·</span></span>
        <span v-if="!collapsed" class="brand-text">
          <strong>{{ platformName }}</strong>
          <small>{{ platformTagline }}</small>
        </span>
      </router-link>

      <!-- 折叠按钮 -->
      <button class="sidebar-collapse-btn" @click="toggleCollapse" :title="collapsed ? '展开菜单' : '折叠菜单'">
        <n-icon size="18">
          <ChevronForwardOutline v-if="collapsed" />
          <ChevronBackOutline v-else />
        </n-icon>
        <span v-if="!collapsed" style="margin-left: 6px; font-size: 11px;">折叠菜单</span>
      </button>

      <!-- 工作空间选择器 -->
      <template v-if="!collapsed">
        <div class="workspace-label">
          工作空间 <span>PRODUCTION</span>
        </div>
        <select
          class="project-picker"
          aria-label="选择租户"
          :value="auth.tenantId"
          @change="auth.selectTenant(($event.target as HTMLSelectElement).value); notifications = []; unreadCount = 0; router.push('/')"
        >
          <option value="" disabled>请选择租户</option>
          <option v-for="t in auth.tenants" :key="String(t.id)" :value="String(t.id)">{{ t.name }}</option>
        </select>
        <select
          v-if="projects.length"
          class="project-picker"
          aria-label="选择项目"
          :value="auth.projectId"
          @change="onProjectChange"
        >
          <option v-for="p in projects" :key="String(p.id)" :value="String(p.id)">{{ p.name }}</option>
        </select>

        <!-- 项目成员入口 -->
        <div v-if="auth.can('projects:write')" style="padding: 0 22px 8px;">
          <span class="member-link" @click="goProjectMembers">成员管理 →</span>
        </div>

        <!-- 菜单搜索 -->
        <div style="padding: 4px 14px 8px;">
          <n-input
            v-model:value="menuSearch"
            placeholder="搜索菜单..."
            size="small"
            clearable
          >
            <template #prefix>
              <n-icon :component="SearchOutline" />
            </template>
          </n-input>
        </div>
      </template>

      <!-- 导航菜单 -->
      <nav aria-label="主导航">
        <n-menu
          :options="menuOptions"
          :collapsed
          :collapsed-width="64"
          :collapsed-icon-size="20"
          :value="activeMenuPath"
          @update:value="handleMenuSelect"
          indent="18"
        />
      </nav>

      <!-- 底部状态 -->
      <div v-if="!collapsed && footerVisible" class="sidebar-footer">
        <span class="status-dot"></span> {{ footerText }}
        <span class="version">v0.1</span>
      </div>
    </aside>

    <!-- ===== 主内容区 ===== -->
    <div class="main-frame">
      <!-- 顶部栏 -->
      <header class="topbar">
        <div class="topbar-left">
          <n-button
            quaternary
            class="mobile-menu"
            aria-label="展开菜单"
            @click="mobileOpen = true"
          >
            <template #icon>
              <n-icon><MenuOutline /></n-icon>
            </template>
          </n-button>

          <!-- 模式标签 -->
          <n-tag
            :type="isExternalMode ? 'info' : 'success'"
            size="small"
            :bordered="false"
            round
          >
            {{ isExternalMode ? '对外运营模式' : '自用管理模式' }}
          </n-tag>

          <!-- 全局资源搜索 -->
          <n-input
            placeholder="搜索服务器、IP、SN"
            size="small"
            clearable
            style="max-width: 280px; flex: 1;"
          >
            <template #prefix>
              <n-icon :component="SearchOutline" />
            </template>
          </n-input>
        </div>

        <div class="topbar-right">
          <!-- 通知铃铛 -->
          <n-badge :value="unreadCount" :max="99" class="notification-badge" @click="notificationOpen = true">
            <n-button quaternary size="small" circle>
              <template #icon>
                <n-icon :size="18"><NotificationsOutline /></n-icon>
              </template>
            </n-button>
          </n-badge>

          <span class="topbar-divider"></span>

          <!-- 主题切换 -->
          <n-button quaternary size="small" circle @click="toggleDarkMode" :title="isDarkMode ? '切换亮色模式' : '切换暗色模式'">
            <template #icon>
              <n-icon :size="18">
                <SunnyOutline v-if="isDarkMode" />
                <MoonOutline v-else />
              </n-icon>
            </template>
          </n-button>

          <span class="topbar-divider"></span>

          <!-- 用户下拉菜单 -->
          <n-dropdown
            :options="userMenuOptions"
            trigger="click"
            @select="handleUserMenuSelect"
          >
            <button class="user-trigger">
              <n-avatar
                round
                size="small"
                :style="{ backgroundColor: 'var(--color-primary-light)', color: 'var(--color-primary)', fontWeight: 700, fontSize: '12px' }"
              >
                {{ (auth.user?.name || auth.user?.username || 'U').charAt(0).toUpperCase() }}
              </n-avatar>
              <span class="user-name">{{ auth.user?.name || auth.user?.username || '用户' }}</span>
              <n-icon><ChevronDownOutline /></n-icon>
            </button>
          </n-dropdown>
        </div>
      </header>

      <!-- 内容区域 -->
      <main class="content">
        <div v-if="auth.contextError" class="empty-hint" role="alert">
          {{ auth.contextError }}
          <n-button text @click="auth.loadContext">重试</n-button>
        </div>
        <div v-else-if="!auth.contextReady" class="empty-hint">正在加载租户与项目…</div>
        <template v-else>
          <div v-if="!auth.projectId" class="empty-hint">
            尚无可用项目，请在租户和项目管理中创建或联系管理员授权。
          </div>
          <router-view :key="`${auth.tenantId}:${auth.projectId}:${route.fullPath}`" />
        </template>
      </main>
    </div>

    <!-- ===== 通知抽屉 ===== -->
    <n-drawer v-model:show="notificationOpen" placement="right" class="notification-drawer" :width="380">
      <n-drawer-content title="告警通知" closable>
        <div class="notification-panel">
          <div v-if="!notifications.length" style="text-align: center; padding: 40px 0; color: #9caab0; font-size: 13px;">
            暂无未读告警
          </div>
          <div
            v-for="notif in notifications"
            :key="String(notif.id)"
            class="notification-item"
            @click="router.push('/alerts'); notificationOpen = false"
          >
            <div style="display: flex; align-items: center; gap: 8px;">
              <div class="notification-title">{{ notif.message || notif.title || '告警事件' }}</div>
              <n-tag
                size="small"
                :type="notif.severity === 'critical' ? 'error' : notif.severity === 'warning' ? 'warning' : 'info'"
                :bordered="false"
                class="notification-severity"
              >
                {{ notif.severity || '告警' }}
              </n-tag>
            </div>
            <div class="notification-time">{{ notif.created_at || '—' }}</div>
          </div>
        </div>
        <template #footer>
          <n-button text type="primary" @click="router.push('/alerts'); notificationOpen = false">
            查看全部告警 →
          </n-button>
        </template>
      </n-drawer-content>
    </n-drawer>

    <!-- ===== 修改密码弹窗 ===== -->
    <n-modal v-model:show="passwordModal" preset="card" title="修改密码" style="width: min(400px, 94vw)">
      <n-form label-placement="top">
        <n-form-item label="当前密码">
          <n-input v-model:value="passwordForm.oldPassword" type="password" placeholder="请输入当前密码" :show-password-on="'click'" />
        </n-form-item>
        <n-form-item label="新密码">
          <n-input v-model:value="passwordForm.newPassword" type="password" placeholder="请输入新密码（至少12位）" :show-password-on="'click'" />
        </n-form-item>
        <n-form-item label="确认新密码">
          <n-input v-model:value="passwordForm.confirmPassword" type="password" placeholder="请再次输入新密码" :show-password-on="'click'" />
        </n-form-item>
        <div class="modal-actions">
          <n-button @click="passwordModal = false">取消</n-button>
          <n-button type="primary" :loading="passwordSaving" @click="changePassword">确认修改</n-button>
        </div>
      </n-form>
    </n-modal>

    <!-- ===== 个人设置弹窗 ===== -->
    <n-modal v-model:show="settingsModal" preset="card" title="个人设置" style="width: min(520px, 94vw)">
      <n-descriptions :column="1" label-placement="left" size="small" class="settings-list">
        <n-descriptions-item label="登录账号">{{ auth.user?.username || '—' }}</n-descriptions-item>
        <n-descriptions-item label="平台角色">{{ auth.user?.role || '—' }}</n-descriptions-item>
        <n-descriptions-item label="用户 ID">{{ auth.user?.id ?? '—' }}</n-descriptions-item>
        <n-descriptions-item label="Token 到期">{{ tokenExpiryText }}</n-descriptions-item>
        <n-descriptions-item label="当前项目">
          <n-select
            v-if="projectOptions.length"
            :value="auth.projectId"
            :options="projectOptions"
            size="small"
            style="max-width: 240px"
            @update:value="onSettingsProjectChange"
          />
          <span v-else>未分配项目</span>
        </n-descriptions-item>
        <n-descriptions-item label="权限数">{{ auth.currentPermissions.length }}</n-descriptions-item>
      </n-descriptions>
      <div class="settings-permissions" v-if="auth.currentPermissions.length">
        <n-tag
          v-for="perm in auth.currentPermissions"
          :key="perm"
          size="small"
          :bordered="false"
          type="info"
        >
          {{ perm }}
        </n-tag>
      </div>
      <div class="modal-actions">
        <n-button @click="settingsModal = false">关闭</n-button>
        <n-button type="primary" @click="openPasswordModal">修改密码</n-button>
      </div>
    </n-modal>
  </div>
</template>
