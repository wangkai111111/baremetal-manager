// 平台品牌、主题与应用配置 — 登录页与侧边栏共用，管理员修改后实时生效。
import { ref } from 'vue'
import { api, unwrap, type RecordData } from '../api'

// 默认值，与后端保持一致
export const platformName = ref('METALOPS')
export const platformTagline = ref('BAREMETAL CONTROL PLANE')
export const platformLogo = ref('')
export const navBackground = ref('#101e2b')
export const navTextColor = ref('#a9bbc7')
export const footerText = ref('控制平面在线')
export const footerVisible = ref(true)
export const loginNotice = ref('')

// 从公开接口读取平台名称、Logo、导航栏配色与应用设置
export async function loadBrand() {
  try {
    const data = unwrap<RecordData>(await api.get('/brand'))
    platformName.value = String(data.platform_name || 'METALOPS')
    platformTagline.value = String(data.platform_tagline || 'BAREMETAL CONTROL PLANE')
    platformLogo.value = String(data.logo_url || '')
    navBackground.value = String(data.nav_bg_color || '#101e2b')
    navTextColor.value = String(data.nav_text_color || '#a9bbc7')
    footerText.value = String(data.footer_text || '控制平面在线')
    footerVisible.value = data.footer_visible !== false
    loginNotice.value = String(data.login_notice || '')
    // 同步更新浏览器标签页标题
    document.title = platformName.value
  } catch {
    // 读取失败时保留默认配置
  }
}
