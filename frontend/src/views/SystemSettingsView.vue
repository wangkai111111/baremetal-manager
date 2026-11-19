<script setup lang="ts">
// 系统设置页面 — 平台参数与集成配置（仅 admin 可访问）
import { computed, ref, watch } from 'vue'
import {
  NButton,
  NAlert,
  NInput,
  NInputNumber,
  NForm,
  NFormItem,
  NRadioGroup,
  NRadioButton,
  NColorPicker,
  NSwitch,

  useMessage
} from 'naive-ui'
import { api, errorText, unwrap, type RecordData } from '../api'
import { useAuth } from '../stores/auth'
import { useRouter } from 'vue-router'
import SettingsCatalog, { type SettingOption } from '../components/SettingsCatalog.vue'
import {
  applySavedSettings,
  footerText,
  footerVisible,
  loginNotice,
  navBackground,
  navTextColor,
  platformLogo,
  platformName,
  platformTagline
} from '../stores/brand'

const message = useMessage()
const auth = useAuth()
const router = useRouter()
const catalog = ref<SettingOption[]>([])

// === 设置项分类 ===
type SettingCategory = 'all' | 'theme' | 'app' | 'operation' | 'features'

// === 列表数据状态 ===
const items = ref<RecordData[]>([])
const loading = ref(false)
const saving = ref(false)
const activeTab = ref<SettingCategory>('all')

const isAdmin = computed(() => auth.user?.is_platform_admin === true)

// === 主题设置状态（平台品牌 + 侧边栏配色） ===
const brandName = ref(platformName.value)
const brandTagline = ref(platformTagline.value)
const brandLogo = ref(platformLogo.value)
const themeNavBg = ref(navBackground.value)
const themeNavText = ref(navTextColor.value)
const themeSaving = ref(false)

// === Logo 来源：上传 / 图片 URL / Base64 ===
const logoMode = ref('upload')

// 根据已有 Logo 值推断来源类型
function detectLogoMode(value: string) {
  if (value.startsWith('data:')) return 'base64'
  if (/^https?:\/\//i.test(value)) return 'url'
  return 'upload'
}

function onLogoModeChange(mode: string) {
  // 切换来源时清空旧值，避免把 Base64 填进 URL 输入框
  if (mode !== detectLogoMode(brandLogo.value)) brandLogo.value = ''
}

// === 应用设置状态 ===
const appFooterText = ref(footerText.value)
const appFooterVisible = ref(footerVisible.value)
const appLoginNotice = ref(loginNotice.value)
const appSaving = ref(false)

const operationSaving = ref(false)
const smtpHost = ref('')
const smtpPort = ref(587)
const smtpUsername = ref('')
const smtpPassword = ref('')
const smtpFrom = ref('')
const smtpTls = ref(true)
const smtpTestTo = ref('')
const smtpTesting = ref(false)
const smtpTestResult = ref('')
const smtpTestSucceeded = ref(false)

async function testSMTP() {
  if (!isAdmin.value || smtpTesting.value) return
  smtpTestResult.value = ''
  if (!smtpHost.value.trim() || !smtpPort.value || !smtpFrom.value.trim() || !/^[^\s@<>]+@[^\s@<>]+\.[^\s@<>]+$/.test(smtpTestTo.value.trim())) {
    message.warning('请填写 SMTP 主机、端口、发件人和有效的测试收件邮箱')
    return
  }
  smtpTesting.value = true
  try {
    await api.post('/settings/smtp-test', {
      host: smtpHost.value.trim(), port: smtpPort.value,
      username: smtpUsername.value.trim(),
      password: smtpPassword.value === '********' ? '' : smtpPassword.value,
      from: smtpFrom.value.trim(), tls: smtpTls.value, to: smtpTestTo.value.trim()
    })
    smtpTestSucceeded.value = true
    smtpTestResult.value = 'SMTP 服务器已接受测试邮件，请检查收件箱或垃圾邮件。'
    message.success(smtpTestResult.value)
  } catch (error) {
    smtpTestSucceeded.value = false
    smtpTestResult.value = errorText(error)
    message.error(smtpTestResult.value)
  } finally {
    smtpTesting.value = false
  }
}

// === 从设置项同步主题字段 ===
function syncBrand() {
  const nameItem = items.value.find(item => String(item.key) === 'brand.platform_name')
  const taglineItem = items.value.find(item => String(item.key) === 'brand.platform_tagline')
  const logoItem = items.value.find(item => String(item.key) === 'brand.logo_url')
  brandName.value = nameItem ? String(nameItem.value || '') : platformName.value
  brandTagline.value = taglineItem ? String(taglineItem.value || '') : platformTagline.value
  brandLogo.value = logoItem ? String(logoItem.value || '') : platformLogo.value
  logoMode.value = detectLogoMode(brandLogo.value)
}

// === 从设置项同步主题字段 ===
function syncTheme() {
  const bgItem = items.value.find(item => String(item.key) === 'theme.nav_bg')
  const textItem = items.value.find(item => String(item.key) === 'theme.nav_text')
  themeNavBg.value = bgItem ? String(bgItem.value || '') : navBackground.value
  themeNavText.value = textItem ? String(textItem.value || '') : navTextColor.value
}

// 重置主题表单为已保存的值
function resetTheme() {
  syncBrand()
  syncTheme()
}

// === 从设置项同步应用配置 ===
function syncOperation() {
  smtpHost.value = String(items.value.find(item => String(item.key) === 'smtp.host')?.value || '')
  smtpPort.value = Number(items.value.find(item => String(item.key) === 'smtp.port')?.value || 587)
  smtpUsername.value = String(items.value.find(item => String(item.key) === 'smtp.username')?.value || '')
  smtpFrom.value = String(items.value.find(item => String(item.key) === 'smtp.from')?.value || '')
  smtpTls.value = String(items.value.find(item => String(item.key) === 'smtp.tls')?.value) !== 'false'
}

async function saveOperationSettings() {
  operationSaving.value = true
  try {
    const smtpEntries: Record<string, unknown> = {
      'smtp.host': smtpHost.value,
      'smtp.port': Number(smtpPort.value),
      'smtp.username': smtpUsername.value,
      ...(smtpPassword.value ? { 'smtp.password': smtpPassword.value } : {}),
      'smtp.from': smtpFrom.value,
      'smtp.tls': smtpTls.value
    }
    await api.put('/settings', smtpEntries)
    smtpPassword.value = ''
    message.success('SMTP 发信配置已保存')
    await load()
  } catch (e) {
    message.error(errorText(e))
  } finally {
    operationSaving.value = false
  }
}

function syncApp() {
  const footerItem = items.value.find(item => String(item.key) === 'app.footer_text')
  const visibleItem = items.value.find(item => String(item.key) === 'app.footer_visible')
  const noticeItem = items.value.find(item => String(item.key) === 'app.login_notice')
  appFooterText.value = footerItem ? String(footerItem.value || '') : footerText.value
  appFooterVisible.value = visibleItem ? String(visibleItem.value) !== 'false' : footerVisible.value
  appLoginNotice.value = noticeItem ? String(noticeItem.value || '') : loginNotice.value
}

// === 加载设置项列表 ===
async function load() {
  loading.value = true
  try {
    const data = unwrap<{items: RecordData[]; catalog: SettingOption[]}>(await api.get('/settings'))
    items.value = data.items
    catalog.value = data.catalog || []
    syncBrand()
    syncTheme()
    syncApp()
    syncOperation()
  } catch (e) {
    message.error(errorText(e))
  } finally {
    loading.value = false
  }
}

watch(
  () => auth.projectId,
  () => load(),
  { immediate: true }
)

async function saveSelectedSettings(values: Record<string, unknown>) {
  if (!isAdmin.value || saving.value || loading.value) return
  saving.value = true
  try {
    applySavedSettings(unwrap<RecordData>(await api.put('/settings', values)))
    message.success('所选配置已保存')
    await load()
  } catch (e) { message.error(errorText(e)) }
  finally { saving.value = false }
}

// === 将图片压缩为 data URL，限制 Logo 体积 ===
function compressImage(file: File): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader()
    reader.onerror = () => reject(new Error('读取图片失败'))
    reader.onload = () => {
      const image = new Image()
      image.onerror = () => reject(new Error('不支持的图片格式'))
      image.onload = () => {
        const maxSize = 192
        const scale = Math.min(1, maxSize / Math.max(image.width, image.height))
        const canvas = document.createElement('canvas')
        canvas.width = Math.round(image.width * scale)
        canvas.height = Math.round(image.height * scale)
        const context = canvas.getContext('2d')
        if (!context) {
          reject(new Error('浏览器不支持图片处理'))
          return
        }
        context.drawImage(image, 0, 0, canvas.width, canvas.height)
        resolve(canvas.toDataURL('image/png'))
      }
      image.src = String(reader.result)
    }
    reader.readAsDataURL(file)
  })
}

// === 选择 Logo 图片 ===
const logoInput = ref<HTMLInputElement | null>(null)

function pickLogo() {
  logoInput.value?.click()
}

async function onLogoChange(event: Event) {
  const input = event.target as HTMLInputElement
  const file = input.files?.[0]
  if (!file) return
  if (!file.type.startsWith('image/')) {
    message.warning('请选择 PNG、JPG 等图片文件')
    input.value = ''
    return
  }
  if (file.size > 2 * 1024 * 1024) {
    message.warning('图片大小不能超过 2MB')
    input.value = ''
    return
  }
  try {
    brandLogo.value = await compressImage(file)
  } catch (error) {
    message.error(errorText(error))
  } finally {
    input.value = ''
  }
}

// === 保存主题设置：平台品牌 + 侧边栏配色 ===
async function saveThemeSettings() {
  if (!brandName.value.trim()) {
    message.warning('请输入平台名称')
    return
  }
  const logo = brandLogo.value.trim()
  // Keep supported image sources consistent with the server and tab icon.
  if (logo && !/^(https?:\/\/|\/(?!\/)|data:image\/(png|jpeg);base64,)/i.test(logo)) {
    message.warning('Logo 需为图片 URL 或 data:image Base64 数据')
    return
  }
  if (!isAdmin.value || themeSaving.value || loading.value) return
  const savedNavBg = themeNavBg.value
  const savedNavText = themeNavText.value
  themeSaving.value = true
  try {
    const result = await api.put('/settings', {
      'brand.platform_name': brandName.value.trim(),
      'brand.platform_tagline': brandTagline.value.trim(),
      'brand.logo_url': logo,
      'theme.nav_bg': savedNavBg,
      'theme.nav_text': savedNavText
    })
    applySavedSettings(unwrap<RecordData>(result))
    message.success('主题设置已保存')
    await load()
  } catch (error) {
    message.error(errorText(error))
  } finally {
    themeSaving.value = false
  }
}

// === 保存应用设置 ===
async function saveAppSettings() {
  if (!isAdmin.value || appSaving.value || loading.value) return
  if (!appFooterText.value.trim()) {
    message.warning('请输入侧边栏底部文案')
    return
  }
  appSaving.value = true
  try {
    const result = await api.put('/settings', {
      'app.footer_text': appFooterText.value.trim(),
      'app.footer_visible': appFooterVisible.value,
      'app.login_notice': appLoginNotice.value.trim()
    })
    applySavedSettings(unwrap<RecordData>(result))
    message.success('应用设置已保存')
    await load()
  } catch (error) {
    message.error(errorText(error))
  } finally {
    appSaving.value = false
  }
}

// === 分类标签 ===
const tabLabels: Record<SettingCategory, string> = {
  all: '全部可用配置', theme: '主题设置', app: '应用设置', operation: '邮件通知', features: '功能配置入口'
}
const features = [
  {name:'告警规则', description:'选择监控指标、健康等级或失败次数、设备标签范围及通知渠道。', path:'/alert-rules'},
  {name:'推送渠道', description:'按邮件、企业微信、钉钉、飞书或 Webhook 类型配置接收目标。', path:'/alert-channels'},
  {name:'PXE 装机服务', description:'选择宿主机网卡、子网、地址池、引导文件及标签固定 IP 规则。', path:'/pxe-config'},
  {name:'BMC、硬件监控与 SOL', description:'在服务器中编辑 BMC 地址与凭据，打开服务器详情调整自动监控开关或使用 SOL。', path:'/assets'},
  {name:'子网与地址池', description:'设置实际装机子网、网关、DNS 和可分配地址范围。', path:'/subnets'},
  {name:'维保与资产折旧', description:'在服务器维保和折旧页面设置采购、供应商、维保到期及折旧参数。', path:'/asset-depreciation'}
]
</script>

<template>
  <div class="page">
    <div class="page-head">
      <div>
        <div class="eyebrow dark">ADMINISTRATION / SYSTEM SETTINGS</div>
        <h1>系统设置</h1>
        <p>平台参数与集成配置（仅管理员可修改）</p>
      </div>
    </div>
    <div class="panel">
      <!-- 设置分类导航：自定义标签，避免标签栏在窄屏下不可见 -->
      <div class="settings-tabs">
        <button
          v-for="(label, key) in tabLabels"
          :key="key"
          type="button"
          class="settings-tab"
          :class="{ active: activeTab === key }"
          @click="activeTab = key"
        >
          {{ label }}
        </button>
      </div>

      <!-- 主题设置：平台品牌 + 侧边栏配色 -->
      <SettingsCatalog v-if="activeTab === 'all'" :options="catalog" :items="items" :disabled="loading || !isAdmin" :saving="saving" @save="saveSelectedSettings" />
      <div v-else-if="activeTab === 'theme'" class="theme-settings">
        <section class="settings-section">
          <div class="settings-section-head">
            <h3>平台品牌</h3>
            <p>平台名称与 Logo 同步用于登录页、侧边栏和浏览器标签页，副标题用于侧边栏</p>
          </div>
          <div class="brand-settings">
            <div class="brand-preview">
              <img v-if="brandLogo" :src="brandLogo" alt="平台 Logo 预览" class="brand-preview-img" />
              <span v-else class="brand-preview-empty">暂无 Logo</span>
              <small>建议使用方形透明 PNG，展示尺寸 36×36</small>
            </div>
            <div class="brand-form">
              <n-form label-placement="top">
                <n-form-item label="平台名称" required>
                  <n-input
                    v-model:value="brandName"
                    :disabled="!isAdmin"
                    placeholder="例如 METALOPS"
                    maxlength="40"
                    show-count
                  />
                </n-form-item>
                <n-form-item label="平台副标题">
                  <n-input
                    v-model:value="brandTagline"
                    :disabled="!isAdmin"
                    placeholder="例如 BAREMETAL CONTROL PLANE"
                    maxlength="60"
                    show-count
                  />
                </n-form-item>
                <n-form-item label="Logo 来源">
                  <n-radio-group
                    v-model:value="logoMode"
                    size="small"
                    :disabled="!isAdmin"
                    @update:value="onLogoModeChange"
                  >
                    <n-radio-button value="upload">上传图片</n-radio-button>
                    <n-radio-button value="url">图片 URL</n-radio-button>
                    <n-radio-button value="base64">Base64</n-radio-button>
                  </n-radio-group>
                </n-form-item>
                <n-form-item label="平台 Logo">
                  <div v-if="logoMode === 'upload'" class="logo-actions">
                    <n-button v-if="isAdmin" @click="pickLogo">上传图片</n-button>
                    <n-button v-if="isAdmin && brandLogo" quaternary @click="brandLogo = ''">移除 Logo</n-button>
                    <span class="logo-hint">支持 PNG / JPG，最大 2MB，上传后自动压缩</span>
                    <input
                      ref="logoInput"
                      type="file"
                      accept="image/*"
                      class="hidden-file"
                      @change="onLogoChange"
                    />
                  </div>
                  <n-input
                    v-else-if="logoMode === 'url'"
                    v-model:value="brandLogo"
                    :disabled="!isAdmin"
                    placeholder="https://example.com/logo.png"
                    clearable
                  />
                  <n-input
                    v-else
                    v-model:value="brandLogo"
                    type="textarea"
                    :rows="3"
                    :disabled="!isAdmin"
                    placeholder="data:image/png;base64,iVBORw0KGgo..."
                    clearable
                  />
                </n-form-item>
              </n-form>
            </div>
          </div>
        </section>

        <section class="settings-section">
          <div class="settings-section-head">
            <h3>侧边栏配色</h3>
            <p>导航栏背景色与文字颜色，保存后立即生效</p>
          </div>
          <div class="theme-form">
            <n-form label-placement="top">
              <n-form-item label="导航栏背景色">
                <n-color-picker
                  v-model:value="themeNavBg"
                  :modes="['hex']"
                  :show-alpha="false"
                  :disabled="!isAdmin"
                />
              </n-form-item>
              <n-form-item label="导航栏文字颜色">
                <n-color-picker
                  v-model:value="themeNavText"
                  :modes="['hex']"
                  :show-alpha="false"
                  :disabled="!isAdmin"
                />
              </n-form-item>
            </n-form>
            <!-- 小样预览：直接使用用户设置的配色 -->
            <div class="theme-preview" :style="{ background: themeNavBg }">
              <span class="theme-preview-title" :style="{ color: themeNavText }">导航栏预览</span>
              <span class="theme-preview-item active">服务器资产</span>
              <span class="theme-preview-item" :style="{ color: themeNavText }">网络与 IP</span>
              <span class="theme-preview-item" :style="{ color: themeNavText }">告警中心</span>
            </div>
          </div>
        </section>

        <div class="modal-actions settings-actions">
          <n-button @click="resetTheme">重置</n-button>
          <n-button v-if="isAdmin" type="primary" :loading="themeSaving" :disabled="loading" @click="saveThemeSettings">
            保存主题设置
          </n-button>
        </div>
      </div>

      <!-- 邮件通知配置 -->
      <div v-else-if="activeTab === 'operation'" class="theme-settings operation-settings">
        <section class="operation-card identity-card"><div class="operation-card-head"><div><h3>邮件通知</h3><p>配置系统通知使用的 SMTP 发信通道。</p></div></div>
          <div class="smtp-block"><div class="subsection-label">SMTP 发信配置</div><n-form label-placement="top" class="smtp-form">
            <n-form-item label="SMTP 主机"><n-input v-model:value="smtpHost" :disabled="!isAdmin" placeholder="smtp.example.com" /></n-form-item><n-form-item label="SMTP 端口"><n-input-number v-model:value="smtpPort" :disabled="!isAdmin" :min="1" :max="65535" /></n-form-item>
            <n-form-item label="SMTP 用户名"><n-input v-model:value="smtpUsername" :disabled="!isAdmin" placeholder="mailer@example.com" /></n-form-item><n-form-item label="SMTP 密码"><n-input v-model:value="smtpPassword" type="password" show-password-on="click" :disabled="!isAdmin" placeholder="留空表示保持原密码" /></n-form-item>
            <n-form-item label="发件人地址"><n-input v-model:value="smtpFrom" :disabled="!isAdmin" placeholder="noreply@example.com" /></n-form-item><n-form-item label="传输加密 TLS"><n-switch v-model:value="smtpTls" :disabled="!isAdmin" /></n-form-item>
          </n-form>
          <div class="smtp-test-panel">
            <n-form-item label="测试收件邮箱" label-placement="top"><n-input v-model:value="smtpTestTo" :disabled="!isAdmin || smtpTesting" placeholder="输入用于接收测试邮件的邮箱" /></n-form-item>
            <n-button :disabled="!isAdmin" :loading="smtpTesting" @click="testSMTP">发送测试邮件</n-button>
            <p>使用当前表单配置发送，不会自动保存；密码留空时使用已保存的密码。认证发信需启用 TLS。</p>
            <n-alert v-if="smtpTestResult" :type="smtpTestSucceeded ? 'success' : 'error'" role="status">{{ smtpTestResult }}</n-alert>
          </div></div>
        </section>
        <div class="operation-savebar"><div><span class="savebar-mark" />保存 SMTP 发信配置</div><div class="operation-actions"><n-button quaternary @click="syncOperation">重置</n-button><n-button v-if="isAdmin" type="primary" :loading="operationSaving" @click="saveOperationSettings">保存设置</n-button></div></div>
      </div>

      <!-- 应用设置：侧边栏底部文案、登录页公告等 -->
      <div v-else-if="activeTab === 'app'" class="theme-settings">
        <div class="app-form">
          <n-form label-placement="top">
            <n-form-item label="侧边栏底部文案">
              <n-input
                v-model:value="appFooterText"
                :disabled="!isAdmin"
                placeholder="例如 控制平面在线"
                maxlength="40"
                show-count
              />
            </n-form-item>
            <n-form-item label="显示侧边栏底部状态">
              <n-switch v-model:value="appFooterVisible" :disabled="!isAdmin" />
            </n-form-item>
            <n-form-item label="登录页公告">
              <n-input
                v-model:value="appLoginNotice"
                type="textarea"
                :rows="3"
                :disabled="!isAdmin"
                placeholder="留空则不显示，例如：系统将于 22:00 进行维护"
                maxlength="120"
                show-count
              />
            </n-form-item>
          </n-form>
          <div class="modal-actions">
            <n-button @click="syncApp()">重置</n-button>
            <n-button v-if="isAdmin" type="primary" :loading="appSaving" @click="saveAppSettings">
              保存应用设置
            </n-button>
          </div>
        </div>
      </div>

      <section v-else class="feature-settings">
        <n-alert type="info" :bordered="false">以下功能都有独立的设置界面，点击即可选择配置，无需输入配置项名称。</n-alert>
        <div class="feature-grid"><article v-for="feature in features" :key="feature.path"><h3>{{ feature.name }}</h3><p>{{ feature.description }}</p><n-button @click="router.push(feature.path)">前往配置</n-button></article></div>
      </section>
    </div>
  </div>
</template>

<style scoped>
.feature-settings { padding:24px; }.feature-grid { display:grid; grid-template-columns:repeat(2,minmax(0,1fr)); gap:16px; margin-top:20px; }.feature-grid article { border:1px solid var(--color-border); border-radius:var(--radius-sm); padding:20px; }.feature-grid h3 { margin:0; font-size:16px; }.feature-grid p { color:var(--color-text-secondary); font-size:13px; line-height:1.7; min-height:44px; }@media(max-width:700px) { .feature-grid { grid-template-columns:minmax(0,1fr); }.feature-settings { padding:16px; } }

.operation-settings { background: #f6f8f9; padding: 28px; color: #1f3038; }.subsection-label { color:#74838a; font-size:10px; font-weight:700; letter-spacing:.14em; text-transform:uppercase; }.operation-card-head p { margin:0; color:#74838a; font-size:13px; }.savebar-mark { width:7px; height:7px; display:inline-block; border-radius:50%; background:#d49644; }.operation-card { background:#fff; border:1px solid #dfe6e8; border-radius:4px; padding:24px; margin-bottom:16px; box-shadow:0 4px 14px rgba(31,48,56,.035); }.operation-card-head { display:flex; justify-content:space-between; gap:20px; align-items:flex-start; margin-bottom:22px; }.operation-card-head > div { display:flex; gap:14px; align-items:flex-start; }.operation-card h3 { margin:0 0 5px; font-size:17px; }.smtp-block { border-top:1px solid #edf0f1; padding-top:18px; margin-top:10px; }.subsection-label { margin-bottom:14px; }.subsection-label span { color:#a2adb1; letter-spacing:0; text-transform:none; font-weight:400; }.smtp-form { display:grid; grid-template-columns:repeat(2,minmax(0,1fr)); column-gap:18px; }.smtp-form :deep(.n-form-item) { margin-bottom:16px; }.smtp-form :deep(.n-input-number) { width:100%; }.operation-savebar { display:flex; justify-content:space-between; align-items:center; padding:15px 4px 2px; color:#75848a; font-size:12px; }.operation-savebar > div:first-child { display:flex; align-items:center; gap:8px; }.operation-actions { display:flex; gap:10px; }
@media (min-width: 1050px) {.operation-settings { display:grid; grid-template-columns:minmax(0,1fr) minmax(0,1fr); gap:16px; }.operation-savebar,.identity-card { grid-column:1/-1; } }.smtp-test-panel { border-top: 1px solid #e4e9ed; padding-top: 18px; }.smtp-test-panel > p { color: #64748b; font-size: 12px; line-height: 1.7; margin: 12px 0; }
@media (max-width: 680px) {.operation-settings { padding:18px 14px; }.operation-card { padding:18px 16px; }.smtp-form { grid-template-columns:1fr; }.operation-savebar { align-items:flex-start; gap:14px; flex-direction:column; } }
</style>
