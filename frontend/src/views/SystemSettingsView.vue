<script setup lang="ts">
// 系统设置页面 — 平台参数与集成配置（仅 admin 可访问）
import { computed, ref, watch } from 'vue'
import {
  NButton,
  NAlert,
  NCheckbox,
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

// === 设置项分类 ===
type SettingCategory = 'theme' | 'app' | 'operation'

// === 列表数据状态 ===
const items = ref<RecordData[]>([])
const loading = ref(false)
const activeTab = ref<SettingCategory>('theme')

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
const smtpClearPassword = ref(false)
const smtpPasswordSaved = computed(() => !!items.value.find(item => item.key === 'smtp.password')?.value)
const smtpFrom = ref('')
const smtpTls = ref(true)
const smtpTestTo = ref('')
const smtpTesting = ref(false)
const smtpTestResult = ref('')
const smtpTestSucceeded = ref(false)

async function testSMTP() {
  if (!isAdmin.value || smtpTesting.value || loading.value || operationSaving.value || smtpClearPassword.value) return
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
  smtpPassword.value = ''
  smtpClearPassword.value = false
  smtpHost.value = String(items.value.find(item => String(item.key) === 'smtp.host')?.value || '')
  smtpPort.value = Number(items.value.find(item => String(item.key) === 'smtp.port')?.value || 587)
  smtpUsername.value = String(items.value.find(item => String(item.key) === 'smtp.username')?.value || '')
  smtpFrom.value = String(items.value.find(item => String(item.key) === 'smtp.from')?.value || '')
  smtpTls.value = String(items.value.find(item => String(item.key) === 'smtp.tls')?.value) !== 'false'
}

async function saveOperationSettings() {
  if (!isAdmin.value || operationSaving.value || loading.value) return
  operationSaving.value = true
  try {
    const smtpEntries: Record<string, unknown> = {
      'smtp.host': smtpHost.value,
      'smtp.port': Number(smtpPort.value),
      'smtp.username': smtpUsername.value,
      ...(smtpClearPassword.value ? { 'smtp.password': '' } : smtpPassword.value ? { 'smtp.password': smtpPassword.value } : {}),
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
    const data = unwrap<{items: RecordData[]}>(await api.get('/settings'))
    items.value = data.items
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
  theme: '主题设置', app: '应用设置', operation: '邮件通知'
}
</script>

<template>
  <div class="page">
    <div class="page-head">
      <div>
        <div class="eyebrow dark">ADMINISTRATION / SYSTEM SETTINGS</div>
        <h1>系统设置</h1>
        <p>直接修改平台外观、应用显示和邮件通知，保存后生效</p>
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
      <div v-if="activeTab === 'theme'" class="theme-settings">
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
            <h3>导航栏配色</h3>
            <p>同时应用于顶部和侧边导航，保存后立即生效</p>
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
        <section class="operation-card identity-card">
          <div class="operation-card-head">
            <div><h3>邮件通知</h3><p>填写系统通知使用的 SMTP 发信配置，在本页直接保存。</p></div>
          </div>
          <div class="smtp-block">
            <n-form label-placement="top" class="smtp-form" :disabled="!isAdmin || loading || operationSaving || smtpTesting">
              <n-form-item label="SMTP 主机">
                <n-input v-model:value="smtpHost" placeholder="smtp.example.com" />
              </n-form-item>
              <n-form-item label="SMTP 端口">
                <n-input-number v-model:value="smtpPort" :min="1" :max="65535" :precision="0" />
              </n-form-item>
              <n-form-item label="SMTP 用户名">
                <n-input v-model:value="smtpUsername" placeholder="无账号认证的邮件中继可留空" />
              </n-form-item>
              <n-form-item label="SMTP 密码">
                <div class="smtp-password-field">
                  <n-input v-model:value="smtpPassword" type="password" show-password-on="click" :disabled="!isAdmin || loading || operationSaving || smtpTesting || smtpClearPassword" placeholder="留空保留已保存的密码" :input-props="{ autocomplete: 'new-password' }" />
                  <div class="smtp-password-actions">
                    <span>{{ smtpPasswordSaved ? '已保存密码' : '尚未设置密码' }}</span>
                    <n-checkbox v-model:checked="smtpClearPassword" :disabled="!isAdmin || loading || operationSaving || smtpTesting">清除已保存密码</n-checkbox>
                  </div>
                </div>
              </n-form-item>
              <n-form-item label="发件人地址">
                <n-input v-model:value="smtpFrom" placeholder="noreply@example.com" />
              </n-form-item>
              <n-form-item label="传输加密 TLS">
                <n-switch v-model:value="smtpTls" />
              </n-form-item>
            </n-form>
            <p class="smtp-hint">587 等端口启用 STARTTLS；465 端口始终使用直接 TLS。认证发信必须加密。</p>
            <div class="smtp-test-panel">
              <n-form-item label="测试收件邮箱" label-placement="top">
                <n-input v-model:value="smtpTestTo" :disabled="!isAdmin || loading || operationSaving || smtpTesting" placeholder="输入用于接收测试邮件的邮箱" />
              </n-form-item>
              <n-button :disabled="!isAdmin || loading || operationSaving || smtpClearPassword" :loading="smtpTesting" @click="testSMTP">发送测试邮件</n-button>
              <p>{{ smtpClearPassword ? '清除密码后，请先保存配置再测试。' : '使用当前表单配置发送，不会自动保存；密码留空时使用已保存的密码。' }}</p>
              <n-alert v-if="smtpTestResult" :type="smtpTestSucceeded ? 'success' : 'error'" role="status">{{ smtpTestResult }}</n-alert>
            </div>
          </div>
        </section>
        <div class="operation-savebar">
          <div>保存 SMTP 发信配置</div>
          <div class="operation-actions">
            <n-button quaternary :disabled="loading || operationSaving || smtpTesting" @click="syncOperation">重置</n-button>
            <n-button v-if="isAdmin" type="primary" :disabled="loading || smtpTesting" :loading="operationSaving" @click="saveOperationSettings">保存邮件设置</n-button>
          </div>
        </div>
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


    </div>
  </div>
</template>

<style scoped>
.smtp-password-field { width:100%; }
.smtp-password-actions { display:flex; align-items:center; flex-wrap:wrap; gap:12px; margin-top:8px; font-size:12px; color:var(--color-text-secondary); }
.smtp-hint { color:var(--color-text-secondary); font-size:12px; line-height:1.7; margin:0 0 20px; }
.operation-settings { background: #f6f8f9; padding: 28px; color: #1f3038; }.subsection-label { color:#74838a; font-size:10px; font-weight:700; letter-spacing:.14em; text-transform:uppercase; }.operation-card-head p { margin:0; color:#74838a; font-size:13px; }.savebar-mark { width:7px; height:7px; display:inline-block; border-radius:50%; background:#d49644; }.operation-card { background:#fff; border:1px solid #dfe6e8; border-radius:4px; padding:24px; margin-bottom:16px; box-shadow:0 4px 14px rgba(31,48,56,.035); }.operation-card-head { display:flex; justify-content:space-between; gap:20px; align-items:flex-start; margin-bottom:22px; }.operation-card-head > div { min-width:0; }.operation-card h3 { margin:0 0 5px; font-size:17px; }.smtp-block { border-top:1px solid #edf0f1; padding-top:18px; margin-top:10px; }.subsection-label { margin-bottom:14px; }.subsection-label span { color:#a2adb1; letter-spacing:0; text-transform:none; font-weight:400; }.smtp-form { display:grid; grid-template-columns:repeat(2,minmax(0,1fr)); column-gap:18px; }.smtp-form :deep(.n-form-item) { margin-bottom:16px; }.smtp-form :deep(.n-input-number) { width:100%; }.operation-savebar { display:flex; justify-content:space-between; align-items:center; padding:15px 4px 2px; color:#75848a; font-size:12px; }.operation-savebar > div:first-child { display:flex; align-items:center; gap:8px; }.operation-actions { display:flex; gap:10px; }
@media (min-width: 1050px) {.operation-settings { display:grid; grid-template-columns:minmax(0,1fr) minmax(0,1fr); gap:16px; }.operation-savebar,.identity-card { grid-column:1/-1; } }.smtp-test-panel { border-top: 1px solid #e4e9ed; padding-top: 18px; }.smtp-test-panel > p { color: #64748b; font-size: 12px; line-height: 1.7; margin: 12px 0; }
@media (max-width: 680px) {.operation-settings { padding:18px 14px; }.operation-card { padding:18px 16px; }.smtp-form { grid-template-columns:1fr; }.operation-savebar { align-items:flex-start; gap:14px; flex-direction:column; } }
</style>
