import { defineStore } from 'pinia'
import { ref, onUnmounted } from 'vue'
import { api, unwrap } from '../api'
import dayjs from 'dayjs'

// === 统一权限标识符常量 ===
// 使用 resource:action 格式定义所有权限
export const Permissions = {
  ASSETS_READ: 'assets:read',
  ASSETS_WRITE: 'assets:write',
  ASSETS_OPERATE: 'assets:operate',
  BMC_OPERATE: 'bmc:operate',
  SOL_ACCESS: 'sol:access',
  IMAGES_READ: 'images:read',
  IMAGES_WRITE: 'images:write',
  TASKS_READ: 'tasks:read',
  TASKS_WRITE: 'tasks:write',
  ALERTS_READ: 'alerts:read',
  ALERTS_WRITE: 'alerts:write',
  ALERTS_MANAGE: 'alerts:manage',
  NETWORK_READ: 'network:read',
  NETWORK_WRITE: 'network:write',
  FIRMWARE_READ: 'firmware:read',
  FIRMWARE_WRITE: 'firmware:write',
  IAM_READ: 'iam:read',
  IAM_WRITE: 'iam:write',
  AUDIT_READ: 'audit:read',
  SETTINGS_READ: 'settings:read',
  SETTINGS_WRITE: 'settings:write',
  PROJECTS_READ: 'projects:read',
  PROJECTS_WRITE: 'projects:write'
} as const

// === Membership 数据类型定义 ===
export interface Membership {
  user_id: number
  project_id: number
  role: string
  permissions: string[]
}

export interface User {
  id?: string
  username?: string
  name?: string
  role?: string
  project_ids?: number[]
  permissions?: string[]
  roles?: (string | { name: string; permissions?: string[] })[]
}

export const useAuth = defineStore('auth', () => {
  const token = ref(sessionStorage.getItem('bmm_token') || '')
  const user = ref<User | null>(null)
  const projectId = ref(sessionStorage.getItem('bmm_project_id') || '')

  // === 项目级权限感知 ===
  // 当前项目的权限列表
  const currentPermissions = ref<string[]>([])

  // === Membership 数据管理 ===
  const memberships = ref<Membership[]>([])

  // === Token 过期倒计时 ===
  // 解析 JWT 获取过期时间，到期前 5 分钟提醒
  const tokenExpiry = ref<number>(0)
  const showExpiryWarning = ref(false)
  let expiryCheckTimer: ReturnType<typeof setInterval> | null = null

  // 解析 JWT payload 中的 exp 字段
  function parseTokenExpiry() {
    const parts = token.value.split('.')
    if (parts.length !== 3) return
    try {
      const payload = JSON.parse(atob(parts[1]))
      if (payload.exp) {
        tokenExpiry.value = payload.exp * 1000
        startExpiryCheck()
      }
    } catch {
      // JWT 解析失败，忽略
    }
  }

  // 启动定时器：每分钟检查一次，距过期 < 5分钟时设置警告
  function startExpiryCheck() {
    stopExpiryCheck()
    expiryCheckTimer = setInterval(() => {
      if (!tokenExpiry.value) return
      const now = dayjs().valueOf()
      const fiveMinutesMs = 5 * 60 * 1000
      // 距离过期不足 5 分钟时显示警告
      showExpiryWarning.value = (tokenExpiry.value - now) < fiveMinutesMs
    }, 60 * 1000)
  }

  // 停止过期检查定时器
  function stopExpiryCheck() {
    if (expiryCheckTimer) {
      clearInterval(expiryCheckTimer)
      expiryCheckTimer = null
    }
  }

  // 组件卸载时清理定时器
  onUnmounted(() => {
    stopExpiryCheck()
  })

  // 从 /auth/me 接口加载当前项目的权限列表
  async function loadProjectPermissions() {
    try {
      const me = unwrap<User & { permissions?: string[]; memberships?: Membership[] }>(
        await api.get('/auth/me')
      )
      // 更新用户权限
      if (me.permissions) {
        currentPermissions.value = me.permissions
      }
      // 更新 memberships 数据
      if (me.memberships) {
        memberships.value = me.memberships
      }
    } catch {
      // 加载权限失败，保持现有状态
    }
  }

  function selectProject(id: string | number) {
    projectId.value = String(id)
    sessionStorage.setItem('bmm_project_id', projectId.value)
    // 切换项目时刷新权限
    loadProjectPermissions()
  }

  function syncProject() {
    if (!projectId.value && user.value?.project_ids?.length) {
      selectProject(user.value.project_ids[0])
    }
  }

  async function login(username: string, password: string) {
    const result = unwrap<{
      access_token?: string
      token?: string
      user?: User
    }>(await api.post('/auth/login', { username, password }))
    const accessToken = result.access_token || result.token
    if (!accessToken) throw new Error('登录响应中未包含 token')
    token.value = accessToken
    sessionStorage.setItem('bmm_token', accessToken)
    user.value = result.user || null
    if (!user.value) await loadUser()
    syncProject()
    // 登录成功后解析 token 过期时间
    parseTokenExpiry()
    // 加载项目权限
    await loadProjectPermissions()
  }

  async function loadUser() {
    user.value = unwrap<User>(await api.get('/auth/me'))
    syncProject()
  }

  function logout() {
    token.value = ''
    user.value = null
    projectId.value = ''
    currentPermissions.value = []
    memberships.value = []
    tokenExpiry.value = 0
    showExpiryWarning.value = false
    stopExpiryCheck()
    sessionStorage.removeItem('bmm_token')
    sessionStorage.removeItem('bmm_project_id')
  }

  // === 统一权限检查方法 ===
  // 使用 resource:action 格式，支持通配符和角色继承
  function can(permission?: string) {
    if (!permission) return true

    // 合并用户权限和项目权限
    const allPermissions = new Set([
      ...(user.value?.permissions || []),
      ...currentPermissions.value
    ])

    const roles = user.value?.roles || []

    // 超级管理员拥有所有权限
    if (user.value?.role === 'admin') return true

    // 通配符权限
    if (allPermissions.has('*')) return true

    // 直接权限匹配
    if (allPermissions.has(permission)) return true

    // 只读权限的继承逻辑：拥有 write 权限自动拥有 read 权限
    if (permission.endsWith(':read') && allPermissions.has(permission.replace(':read', ':write'))) {
      return true
    }

    // 基于角色的权限判断
    if (user.value?.role) {
      if (user.value.role === 'admin') return true
      if (permission.endsWith(':read')) return true
      return (
        user.value.role === 'operator' &&
        !permission.startsWith('iam:') &&
        !permission.startsWith('settings:')
      )
    }

    // 检查角色列表中的权限
    return roles.some(
      role =>
        role === 'admin' ||
        role === 'super_admin' ||
        (typeof role === 'object' &&
          (role.name === 'admin' || role.permissions?.includes(permission)))
    )
  }

  return {
    token,
    user,
    projectId,
    currentPermissions,
    memberships,
    tokenExpiry,
    showExpiryWarning,
    selectProject,
    login,
    loadUser,
    logout,
    can,
    loadProjectPermissions,
    parseTokenExpiry
  }
})
