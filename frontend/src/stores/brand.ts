// Public platform appearance shared by login, navigation and browser chrome.
import { ref, watch } from 'vue'
import { unwrap, type RecordData } from '../api'

const cacheKey = 'bmm_brand_v1'
const defaults = {
  platform_name: 'METALOPS', platform_tagline: 'BAREMETAL CONTROL PLANE', logo_url: '',
  nav_bg_color: '#101e2b', nav_text_color: '#ffffff', footer_text: '控制平面在线',
  footer_visible: true, login_notice: ''
}
function normalize(data: RecordData) {
  const text = (key: keyof typeof defaults) => typeof data[key] === 'string' ? data[key] as string : String(defaults[key])
  return {
    platform_name: text('platform_name').trim() || defaults.platform_name,
    platform_tagline: text('platform_tagline'), logo_url: text('logo_url'),
    nav_bg_color: text('nav_bg_color'), nav_text_color: text('nav_text_color'),
    footer_text: text('footer_text'), footer_visible: typeof data.footer_visible === 'boolean' ? data.footer_visible : defaults.footer_visible,
    login_notice: text('login_notice')
  }
}
function readCache() {
  try {
    const cached: unknown = JSON.parse(localStorage.getItem(cacheKey) || 'null')
    if (cached && typeof cached === 'object' && !Array.isArray(cached)) return normalize(cached as RecordData)
  } catch { /* A missing/invalid cache must never prevent startup. */ }
  return defaults
}
const initial = readCache()
export const platformName = ref(initial.platform_name)
export const platformTagline = ref(initial.platform_tagline)
export const platformLogo = ref(initial.logo_url)
export const navBackground = ref(initial.nav_bg_color)
export const navTextColor = ref(initial.nav_text_color)
export const footerText = ref(initial.footer_text)
export const footerVisible = ref(initial.footer_visible)
export const loginNotice = ref(initial.login_notice)

function snapshot() {
  return { platform_name: platformName.value, platform_tagline: platformTagline.value, logo_url: platformLogo.value,
    nav_bg_color: navBackground.value, nav_text_color: navTextColor.value, footer_text: footerText.value,
    footer_visible: footerVisible.value, login_notice: loginNotice.value }
}
function updateDocument() {
  document.title = platformName.value
  const color = /^#[\da-f]{6}$/i.test(navBackground.value) ? navBackground.value : defaults.nav_bg_color
  const text = /^#[\da-f]{6}$/i.test(navTextColor.value) ? navTextColor.value : defaults.nav_text_color
  const fallback = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 32 32"><rect width="32" height="32" rx="7" fill="${color}"/><text x="16" y="23" text-anchor="middle" font-family="sans-serif" font-size="23" font-weight="700" fill="${text}">M</text></svg>`
  const logo = platformLogo.value
  const validLogo = /^(https?:\/\/|\/(?!\/)|data:image\/(png|jpeg);base64,)/i.test(logo)
  let icon = document.querySelector<HTMLLinkElement>('link[rel="icon"]')
  if (!icon) { icon = document.createElement('link'); icon.rel = 'icon'; document.head.appendChild(icon) }
  icon.href = validLogo ? logo : `data:image/svg+xml,${encodeURIComponent(fallback)}`
  if (validLogo) icon.removeAttribute('type')
  else icon.type = 'image/svg+xml'
  document.querySelector<HTMLMetaElement>('meta[name="theme-color"]')?.setAttribute('content', color)
  // Cache only publicly readable appearance, never authenticated settings.
  try { localStorage.setItem(cacheKey, JSON.stringify(snapshot())) } catch { /* Quota/privacy mode: server remains authoritative. */ }
}
watch([platformName, platformTagline, platformLogo, navBackground, navTextColor, footerText, footerVisible, loginNotice], updateDocument, { immediate: true })

let revision = 0
function applyBrand(data: RecordData) {
  revision++
  const values = normalize(data)
  platformName.value = values.platform_name; platformTagline.value = values.platform_tagline
  platformLogo.value = values.logo_url; navBackground.value = values.nav_bg_color; navTextColor.value = values.nav_text_color
  footerText.value = values.footer_text; footerVisible.value = values.footer_visible; loginNotice.value = values.login_notice
}
export function applySavedSettings(settings: RecordData) {
  const publicValues: RecordData = { ...snapshot() }
  const mapping: Record<string, string> = { 'brand.platform_name': 'platform_name', 'brand.platform_tagline': 'platform_tagline', 'brand.logo_url': 'logo_url', 'theme.nav_bg': 'nav_bg_color', 'theme.nav_text': 'nav_text_color', 'app.footer_text': 'footer_text', 'app.footer_visible': 'footer_visible', 'app.login_notice': 'login_notice' }
  for (const [key, value] of Object.entries(settings)) { if (mapping[key]) publicValues[mapping[key]] = value }
  applyBrand(publicValues)
}
let inFlight: Promise<void> | undefined
export function loadBrand(): Promise<void> {
  if (inFlight) return inFlight
  const started = revision
  const controller = new AbortController()
  const timeout = setTimeout(() => controller.abort(), 15000)
  inFlight = (async () => {
    try {
      // Global public appearance must survive project changes and login redirects.
      const response = await fetch('/api/v1/brand', { cache: 'no-store', signal: controller.signal })
      if (!response.ok) throw new Error('Brand unavailable')
      const body = await response.json()
      if (body.code !== 0 || !body.data || typeof body.data !== 'object') throw new Error('Invalid brand response')
      if (started === revision) applyBrand(unwrap<RecordData>(body))
    } catch { /* Retain last saved appearance if the network is unavailable. */ }
    finally { clearTimeout(timeout); inFlight = undefined }
  })()
  return inFlight
}
