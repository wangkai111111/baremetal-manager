<script setup lang="ts">
import { computed, ref, onMounted, onUnmounted } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { NButton, NIcon, NDropdown, NBadge, NDrawer, NDrawerContent, NModal, NForm, NFormItem, NInput, useMessage } from 'naive-ui'
import {
  GridOutline,
  ServerOutline,
  LayersOutline,
  BusinessOutline,
  CubeOutline,
  DiscOutline,
  FlashOutline,
  GitNetworkOutline,
  WarningOutline,
  HardwareChipOutline,
  PeopleOutline,
  SettingsOutline,
  ListOutline,
  ReceiptOutline,
  MenuOutline,
  ChevronDownOutline,
  NotificationsOutline,
  ChevronBackOutline,
  ChevronForwardOutline
} from '@vicons/ionicons5'
import { useAuth } from '../stores/auth'
import { resource, rows, api, errorText, type RecordData } from '../api'

const route = useRoute()
const router = useRouter()
const message = useMessage()
const auth = useAuth()
const mobileOpen = ref(false)
const projects = ref<RecordData[]>([])

// === 通知中心 ===
const notificationOpen = ref(false)
const notifications = ref<RecordData[]>([])
const unreadCount = ref(0)
const notifLoading = ref(false)

// === 侧边栏折叠 ===
const sidebarCollapsed = ref(false)

// === 修改密码弹窗 ===
const passwordModal = ref(false)
const passwordForm = ref({ oldPassword: '', newPassword: '', confirmPassword: '' })
const passwordSaving = ref(false)

// === 个人设置弹窗 ===
const settingsModal = ref(false)

// === 用户下拉菜单选项 ===
const userMenuOptions = computed(() => [
  { label: '修改密码', key: 'password' },
  { label: '个人设置', key: 'settings' },
  { type: 'divider', key: 'd1' },
  { label: '退出登录', key: 'logout' }
])

// === 加载项目列表 ===
onMounted(async () => {
  try {
    projects.value = rows(await resource.list('/projects')).items
    if (
      projects.value.length &&
      !projects.value.some(p => String(p.id) === auth.projectId)
    ) {
      auth.selectProject(projects.value[0].id!)
    }
  } catch {
    /* Project list errors are surfaced by page requests. */
  }
  // 加载通知
  loadNotifications()
  // 启动定时刷新通知
  notifTimer = setInterval(loadNotifications, 60000)
})

let notifTimer: ReturnType<typeof setInterval> | undefined

onUnmounted(() => {
  if (notifTimer) clearInterval(notifTimer)
})

// === 加载通知（告警） ===
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

// === 处理用户菜单选择 ===
function handleUserMenuSelect(key: string) {
  if (key === 'logout') {
    logout()
  } else if (key === 'password') {
    passwordForm.value = { oldPassword: '', newPassword: '', confirmPassword: '' }
    passwordModal.value = true
  } else if (key === 'settings') {
    settingsModal.value = true
  }
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
  if (passwordForm.value.newPassword.length < 6) {
    message.warning('新密码长度不能少于 6 位')
    return
  }
  passwordSaving.value = true
  try {
    await api.post('/auth/change-password', {
      old_password: passwordForm.value.oldPassword,
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

const groups = [
  {
    label: '工作台',
    items: [{ label: '运行总览', path: '/', icon: GridOutline }]
  },
  {
    label: '基础设施',
    items: [
      { label: '项目管理', path: '/projects', icon: LayersOutline },
      { label: '机房管理', path: '/datacenters', icon: BusinessOutline },
      { label: '机柜管理', path: '/racks', icon: CubeOutline },
      { label: '裸机资产', path: '/assets', icon: ServerOutline }
    ]
  },
  {
    label: '交付与网络',
    items: [
      { label: '系统镜像', path: '/images', icon: DiscOutline },
      { label: '装机任务', path: '/installations', icon: FlashOutline },
      { label: 'IPAM · 子网', path: '/subnets', icon: GitNetworkOutline },
      { label: 'IP 地址', path: '/ip-addresses', icon: GitNetworkOutline },
      { label: 'PXE 配置', path: '/pxe', icon: GitNetworkOutline }
    ]
  },
  {
    label: '运维治理',
    items: [
      { label: '监控告警', path: '/alerts', icon: WarningOutline },
      { label: '固件管理', path: '/firmware', icon: HardwareChipOutline },
      { label: '任务中心', path: '/tasks', icon: ListOutline },
      { label: '审计日志', path: '/audit-logs', icon: ReceiptOutline }
    ]
  },
  {
    label: '平台设置',
    items: [
      { label: '用户管理', path: '/users', icon: PeopleOutline },
      { label: '角色权限', path: '/roles', icon: PeopleOutline },
      { label: '系统设置', path: '/settings', icon: SettingsOutline }
    ]
  }
]

const visibleGroups = computed(() =>
  groups
    .map(group => ({
      ...group,
      items: group.items.filter(
        item => !permission(item.path) || auth.can(permission(item.path))
      )
    }))
    .filter(group => group.items.length)
)

function permission(path: string) {
  return ({
    projects: 'projects:read',
    datacenters: 'datacenters:read',
    racks: 'racks:read',
    assets: 'assets:read',
    images: 'images:read',
    installations: 'installations:read',
    subnets: 'network:read',
    'ip-addresses': 'network:read',
    pxe: 'network:read',
    alerts: 'alerts:read',
    firmware: 'firmware:read',
    tasks: 'tasks:read',
    'audit-logs': 'audit:read',
    users: 'iam:read',
    roles: 'iam:read',
    settings: 'settings:read'
  } as Record<string, string>)[path.slice(1)]
}

function logout() {
  auth.logout()
  router.push('/login')
}

// === 跳转项目成员管理 ===
function goProjectMembers() {
  router.push('/project-members')
}
</script>

<template>
  <div class="app-shell">
    <div
      v-if="mobileOpen"
      class="sidebar-backdrop"
      @click="mobileOpen = false"
    ></div>
    <aside class="sidebar" :class="{ open: mobileOpen, collapsed: sidebarCollapsed }">
      <router-link to="/" class="brand" @click="mobileOpen = false">
        <span class="brand-mark">M<span>·</span></span>
        <span v-if="!sidebarCollapsed">
          <strong>METAL<span>OPS</span></strong>
          <small>BAREMETAL CONTROL PLANE</small>
        </span>
      </router-link>

      <!-- 折叠按钮 -->
      <button class="sidebar-collapse-btn" @click="sidebarCollapsed = !sidebarCollapsed">
        <n-icon size="18">
          <ChevronBackOutline v-if="!sidebarCollapsed" />
          <ChevronForwardOutline v-else />
        </n-icon>
        <span v-if="!sidebarCollapsed" style="margin-left: 6px; font-size: 11px;">折叠菜单</span>
      </button>

      <div v-if="!sidebarCollapsed" class="workspace-label">
        工作空间 <span>PRODUCTION</span>
      </div>
      <select
        v-if="projects.length && !sidebarCollapsed"
        class="project-picker"
        aria-label="选择项目"
        :value="auth.projectId"
        @change="(e: Event) => { auth.selectProject((e.target as HTMLSelectElement).value); router.go(0) }"
      >
        <option
          v-for="project in projects"
          :key="String(project.id)"
          :value="String(project.id)"
        >
          {{ project.name }}
        </option>
      </select>

      <!-- 项目成员入口 -->
      <div v-if="!sidebarCollapsed && auth.can('projects:write')" style="padding: 0 22px 8px;">
        <span class="member-link" @click="goProjectMembers">成员管理 →</span>
      </div>

      <nav aria-label="主导航">
        <div
          v-for="group in visibleGroups"
          :key="group.label"
          class="nav-group"
        >
          <div v-if="!sidebarCollapsed" class="nav-label">{{ group.label }}</div>
          <router-link
            v-for="item in group.items"
            :key="item.path"
            :to="item.path"
            class="nav-item"
            :class="{
              active:
                route.path === item.path ||
                (item.path === '/assets' &&
                  route.path.startsWith('/assets/'))
            }"
            @click="mobileOpen = false"
            :title="sidebarCollapsed ? item.label : ''"
          >
            <n-icon size="18">
              <component :is="item.icon" />
            </n-icon>
            <span v-if="!sidebarCollapsed">{{ item.label }}</span>
          </router-link>
        </div>
      </nav>
      <div v-if="!sidebarCollapsed" class="sidebar-footer">
        <span class="status-dot"></span> 控制平面在线
        <span class="version">v0.1</span>
      </div>
    </aside>
    <div class="main-frame">
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
          <span class="breadcrumb">
            METALOPS <span>/</span> {{ route.meta.title || '总览' }}
          </span>
        </div>
        <div class="topbar-right">
          <span class="environment">
            <span class="status-dot"></span> 生产环境
          </span>
          <span class="topbar-divider"></span>

          <!-- 通知中心铃铛 -->
          <n-badge :value="unreadCount" :max="99" class="notification-badge" @click="notificationOpen = true">
            <n-button quaternary size="small" circle>
              <template #icon>
                <n-icon :size="18"><NotificationsOutline /></n-icon>
              </template>
            </n-button>
          </n-badge>

          <span class="topbar-divider"></span>

          <!-- 用户下拉菜单（增强） -->
          <n-dropdown
            :options="userMenuOptions"
            @select="handleUserMenuSelect"
          >
            <button class="user-trigger">
              <span class="avatar">
                {{
                  (auth.user?.name || auth.user?.username || 'U').charAt(0).toUpperCase()
                }}
              </span>
              <span class="user-name">
                {{ auth.user?.name || auth.user?.username || '用户' }}
              </span>
              <n-icon><ChevronDownOutline /></n-icon>
            </button>
          </n-dropdown>
        </div>
      </header>
      <main class="content">
        <router-view />
      </main>
    </div>

    <!-- 通知面板抽屉层 -->
    <n-drawer
      v-model:show="notificationOpen"
      placement="right"
      class="notification-drawer"
      :width="380"
    >
      <n-drawer-content title="告警通知" closable>
        <div class="notification-panel">
          <div v-if="!notifications.length" style="text-align: center; padding: 40px 0; color: #9caab0; font-size: 13px;">
            暂无未读告警
          </div>
          <div
            v-for="notif in notifications"
            :key="String(notif.id)"
            class="notification-item"
            @click="router.push('/alerts')"
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

    <!-- 修改密码弹窗 -->
    <n-modal
      v-model:show="passwordModal"
      preset="card"
      title="修改密码"
      style="width: min(400px, 94vw)"
    >
      <n-form label-placement="top">
        <n-form-item label="当前密码">
          <n-input v-model:value="passwordForm.oldPassword" type="password" placeholder="请输入当前密码" :show-password-on="'click'" />
        </n-form-item>
        <n-form-item label="新密码">
          <n-input v-model:value="passwordForm.newPassword" type="password" placeholder="请输入新密码（至少6位）" :show-password-on="'click'" />
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

    <!-- 个人设置弹窗（占位） -->
    <n-modal
      v-model:show="settingsModal"
      preset="card"
      title="个人设置"
      style="width: min(460px, 94vw)"
    >
      <div style="padding: 20px 0; color: #82929d; font-size: 13px; text-align: center;">
        个人设置功能开发中...
      </div>
    </n-modal>
  </div>
</template>
