// 余额预警设置 — 计费中心与租户控制台共用。
//
// 后端 accounts 表只有 balance / frozen_amount / credit_limit 三列，
// 未提供任何预警阈值字段或配置接口，因此阈值只能作为「当前浏览器 + 当前项目」
// 的本地偏好保存（localStorage），并在前端与实时余额比较后给出提示。
// 一旦后端补上阈值列，此处应替换为 accountApi 的读写，调用方无需改动。
import { computed, ref } from 'vue'

const STORAGE_PREFIX = 'bmm_balance_alert'

// 预警开关与阈值（元）；模块级单例，跨页面共享同一份状态
const enabled = ref(false)
const threshold = ref(100)

function storageKey(projectId: string) {
  return `${STORAGE_PREFIX}:${projectId || 'default'}`
}

// 按项目读取本地预警设置，切换租户/项目时由页面显式调用
export function loadBalanceAlert(projectId: string) {
  try {
    const raw = localStorage.getItem(storageKey(projectId))
    if (!raw) {
      enabled.value = false
      threshold.value = 100
      return
    }
    const saved = JSON.parse(raw) as { enabled?: boolean; threshold?: number }
    enabled.value = saved.enabled === true
    threshold.value = Number(saved.threshold) > 0 ? Number(saved.threshold) : 100
  } catch {
    enabled.value = false
    threshold.value = 100
  }
}

// 保存设置并同步当前生效值
export function saveBalanceAlert(nextEnabled: boolean, nextThreshold: number, projectId: string) {
  enabled.value = nextEnabled
  threshold.value = nextThreshold > 0 ? nextThreshold : 0
  try {
    localStorage.setItem(storageKey(projectId), JSON.stringify({ enabled: enabled.value, threshold: threshold.value }))
  } catch {
    // 隐私模式或存储配额不足时静默降级，预警仍在当前会话内生效
  }
}

// 当前项目下的预警生效值：未开启、无余额数据或余额高于阈值时返回 null
export function useBalanceAlert(getBalance: () => number | null) {
  const active = computed(() => {
    if (!enabled.value) return null
    const balance = getBalance()
    if (balance === null || balance > threshold.value) return null
    return threshold.value
  })
  return { enabled, threshold, active }
}
