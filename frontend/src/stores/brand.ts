// 平台品牌、主题与应用配置 — 登录页与侧边栏共用，管理员修改后实时生效。
import { ref } from 'vue'
import { api, unwrap, type RecordData } from '../api'

// 默认值，与后端保持一致
export const platformName = ref('METALOPS')
export const platformTagline = ref('BAREMETAL CONTROL PLANE')
export const platformLogo = ref('')
export const navBackground = ref('#101e2b')
export const navTextColor = ref('#ffffff')
export const footerText = ref('控制平面在线')
export const footerVisible = ref(true)
export const loginNotice = ref('')
// DEPRECATED: use auth.systemMode instead
// 模式判定的唯一来源已收敛到 stores/auth.ts 的 systemMode（GET /setup/status 的 mode 字段）。
// 此处仅为 ResourceView 等历史调用保留读取，新代码不应再依赖它做模式分支。
export const operationMode = ref<'self_hosted' | 'external'>('self_hosted')
export const emailVerificationRequired = ref(false)
export const billingEnabled = ref(false)
// 租户自助注册开关，来自 GET /brand 的 feature_flags.registration_enabled，
// 与 auth.systemMode 一起决定登录页是否展示注册入口。
export const registrationEnabled = ref(false)
export const systemMode = ref('')
export const theme = ref('light')

// 从公开接口读取平台名称、Logo、导航栏配色与应用设置
export async function loadBrand() {
  try {
    const data = unwrap<RecordData>(await api.get('/brand'))
    platformName.value = String(data.platform_name || 'METALOPS')
    platformTagline.value = String(data.platform_tagline || 'BAREMETAL CONTROL PLANE')
    platformLogo.value = String(data.logo_url || '')
    navBackground.value = String(data.nav_bg_color || '#101e2b')
    navTextColor.value = String(data.nav_text_color || '#ffffff')
    footerText.value = String(data.footer_text || '控制平面在线')
    footerVisible.value = data.footer_visible !== false
    loginNotice.value = String(data.login_notice || '')
    // DEPRECATED: 仅写入历史兼容字段，模式判定请使用 auth.systemMode
    operationMode.value = (data.operation_mode === 'external' || data.operation_mode === 'external_operation') ? 'external' : 'self_hosted'
    const flags = (data.feature_flags ?? {}) as RecordData
    emailVerificationRequired.value = flags.email_verification === true
    billingEnabled.value = flags.billing_enabled === true
    registrationEnabled.value = flags.registration_enabled === true
    systemMode.value = String(data.operation_mode || data.operationMode || '')
    theme.value = String(data.theme || 'light')
    // 同步更新浏览器标签页标题
    document.title = platformName.value
  } catch {
    // 读取失败时保留默认配置
  }
}
