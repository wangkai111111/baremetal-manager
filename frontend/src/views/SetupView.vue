<script setup lang="ts">
// 系统初始化向导 — 四步流程：选择模式 → 基础配置 → 资源接入 → 完成
import { ref, reactive, computed, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import {
  NButton, NInput, NForm, NFormItem, NTag, NSpin,
  NAlert, NIcon, useMessage
} from 'naive-ui'
import {
  CheckmarkCircleOutline, ServerOutline, GlobeOutline,
  BuildOutline, CloudUploadOutline, ColorWandOutline,
  FlashOutline, LockClosedOutline
} from '@vicons/ionicons5'
import { useAuth } from '../stores/auth'
import { setupApi, unwrap, errorText } from '../api'

const router = useRouter()
const auth = useAuth()
const message = useMessage()

// === 步骤定义 ===
const stepDefs = [
  { title: '选择模式', desc: '确定系统运行模式' },
  { title: '基础配置', desc: '管理员与平台设置' },
  { title: '资源接入', desc: '接入裸机资源（可选）' },
  { title: '完成', desc: '确认并进入系统' }
]
const currentStep = ref(0)

// === 模式选择 ===
const selectedMode = ref<'self_use' | 'external_operation' | null>(null)

const modeCards = [
  {
    key: 'self_use' as const,
    title: '自用管理模式',
    desc: '适合企业内部运维、资产统一管理、团队协作',
    features: ['资产管理', '自动化装机', '监控告警', '批量运维'],
    icon: ServerOutline,
    iconClass: 'icon-self-use'
  },
  {
    key: 'external_operation' as const,
    title: '对外运营模式',
    desc: '适合IDC运营商、云服务商、对外售卖裸机资源',
    features: ['租户管理', '计费系统', '订单财务', '运营后台'],
    icon: GlobeOutline,
    iconClass: 'icon-external'
  }
]

// === 基础配置表单 ===
const formData = reactive({
  admin_username: 'admin',
  admin_password: '',
  admin_password_confirm: '',
  admin_email: '',
  bmc_network: '',
  datacenter_name: '',
  datacenter_location: ''
})

const generatedPassword = ref('')

// 验证
const passwordError = computed(() => {
  if (!formData.admin_password) return ''
  if (formData.admin_password.length < 12) return '密码至少 12 个字符'
  return ''
})
const passwordConfirmError = computed(() => {
  if (!formData.admin_password_confirm) return ''
  if (formData.admin_password_confirm !== formData.admin_password) return '两次密码不一致'
  return ''
})
const emailError = computed(() => {
  if (!formData.admin_email) return ''
  if (!/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(formData.admin_email)) return '邮箱格式不正确'
  return ''
})
const networkError = computed(() => {
  if (!formData.bmc_network) return ''
  if (!/^\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}\/\d{1,2}$/.test(formData.bmc_network)) return 'CIDR 格式，如 192.168.1.0/24'
  return ''
})
const configValid = computed(() => {
  if (!formData.admin_username.trim()) return false
  if (passwordError.value) return false
  if (formData.admin_password !== formData.admin_password_confirm) return false
  if (emailError.value || networkError.value) return false
  return true
})

// === 资源接入 ===
const resourceMethod = ref<'scan' | 'manual' | 'import' | null>(null)

const resourceOptions = [
  {
    key: 'scan' as const,
    title: '网络扫描',
    desc: '自动发现 BMC 网段内的裸机设备',
    icon: FlashOutline
  },
  {
    key: 'manual' as const,
    title: '手动添加',
    desc: '逐个输入服务器 BMC 地址和凭据',
    icon: BuildOutline
  },
  {
    key: 'import' as const,
    title: 'Excel 导入',
    desc: '批量导入资产清单文件',
    icon: CloudUploadOutline
  }
]

// === 状态 ===
const loading = ref(false)
const submitting = ref(false)

// === 生命周期 ===
onMounted(async () => {
  loading.value = true
  try {
    const data = unwrap<any>(await setupApi.getStatus())
    if (data.initialized) {
      message.success('系统已完成初始化')
      router.push('/')
    }
  } catch {
    // 未初始化，继续向导
  } finally {
    loading.value = false
  }
})

// === 步骤导航 ===
function goToStep(step: number) {
  if (step >= 0 && step <= 3) currentStep.value = step
}

// === Step 1: 确认模式 ===
function confirmMode() {
  if (!selectedMode.value) {
    message.warning('请选择一种运行模式')
    return
  }
  goToStep(1)
}

// === Step 2: 提交配置 ===
async function submitConfig() {
  if (!configValid.value) {
    message.warning('请检查表单填写')
    return
  }
  submitting.value = true
  try {
    const payload: Record<string, any> = {
      mode: selectedMode.value,
      admin_username: formData.admin_username.trim(),
      admin_password: formData.admin_password,
      admin_email: formData.admin_email || undefined,
      bmc_network: formData.bmc_network || undefined,
      datacenter_name: formData.datacenter_name || undefined,
      datacenter_location: formData.datacenter_location || undefined
    }
    const result = unwrap<{ generated_password?: string }>(await setupApi.setConfig(payload))
    generatedPassword.value = result.generated_password || ''
    formData.admin_password = ''
    formData.admin_password_confirm = ''
    goToStep(2)
  } catch (e) {
    message.error(errorText(e))
  } finally {
    submitting.value = false
  }
}

// === Step 3: 资源接入（可跳过） ===
function continueResource() {
  goToStep(3)
}

// === Step 4: 完成 ===
async function finishSetup() {
  submitting.value = true
  try {
    await auth.checkSetupStatus()
    message.success('欢迎使用 Baremetal Manager')
    router.push('/')
  } catch {
    message.error('刷新状态失败，请手动刷新页面')
  } finally {
    submitting.value = false
  }
}

// === 选择模式卡片 ===
function selectMode(mode: 'self_use' | 'external_operation') {
  selectedMode.value = mode
}
</script>

<template>
  <div class="setup-container">
    <!-- 加载状态 -->
    <n-spin v-if="loading" :show="true" description="正在检测系统状态..." size="large" />

    <template v-else>
      <!-- 步骤指示器 -->
      <div class="wizard-steps">
        <div
          v-for="(s, i) in stepDefs"
          :key="i"
          class="wizard-step"
          :class="{ active: currentStep === i, done: currentStep > i }"
          @click="i < currentStep && goToStep(i)"
        >
          <div class="step-node">
            <n-icon v-if="currentStep > i" :size="16"><CheckmarkCircleOutline /></n-icon>
            <span v-else class="step-num">{{ i + 1 }}</span>
          </div>
          <div class="step-label">
            <strong>{{ s.title }}</strong>
            <small>{{ s.desc }}</small>
          </div>
          <div v-if="i < stepDefs.length - 1" class="step-connector"></div>
        </div>
      </div>

      <!-- === Step 1: 模式选择 === -->
      <div v-if="currentStep === 0" class="wizard-body">
        <h2 class="wizard-heading">选择运行模式</h2>
        <p class="wizard-subtext">确定后不可更改，请根据业务场景选择</p>

        <div class="setup-cards">
          <div
            v-for="card in modeCards"
            :key="card.key"
            class="setup-card"
            :class="{ selected: selectedMode === card.key }"
            @click="selectMode(card.key)"
          >
            <div class="card-top">
              <div class="card-icon" :class="card.iconClass">
                <n-icon :size="28"><component :is="card.icon" /></n-icon>
              </div>
              <div v-if="selectedMode === card.key" class="card-badge">
                <n-icon :size="20" color="#165DFF"><CheckmarkCircleOutline /></n-icon>
              </div>
            </div>
            <h3 class="card-title">{{ card.title }}</h3>
            <p class="card-desc">{{ card.desc }}</p>
            <div class="card-features">
              <n-tag
                v-for="f in card.features"
                :key="f"
                size="small"
                :type="selectedMode === card.key ? 'info' : 'default'"
                :bordered="false"
                round
              >{{ f }}</n-tag>
            </div>
            <div class="card-action">
              <n-button
                :type="selectedMode === card.key ? 'primary' : 'default'"
                size="small"
                @click.stop="selectMode(card.key)"
              >
                {{ selectedMode === card.key ? '已选择' : (card.key === 'self_use' ? '选择自用模式' : '选择运营模式') }}
              </n-button>
            </div>
          </div>
        </div>

        <div class="wizard-footer">
          <div></div>
          <n-button
            type="primary"
            size="large"
            :disabled="!selectedMode"
            :loading="submitting"
            @click="confirmMode"
          >确认并继续</n-button>
        </div>
      </div>

      <!-- === Step 2: 基础配置 === -->
      <div v-else-if="currentStep === 1" class="wizard-body">
        <h2 class="wizard-heading">基础配置</h2>
        <p class="wizard-subtext">设置管理员账户与平台基础设施信息</p>

        <n-form class="config-form" label-placement="top" :show-feedback="true">
          <div class="form-group">
            <div class="form-group-title">
              <n-icon :size="16"><LockClosedOutline /></n-icon>
              <span>管理员账户</span>
            </div>
            <div class="form-row">
              <n-form-item label="用户名">
                <n-input v-model:value="formData.admin_username" placeholder="admin" />
              </n-form-item>
              <n-form-item label="邮箱">
                <n-input
                  v-model:value="formData.admin_email"
                  placeholder="admin@example.com"
                  :status="emailError ? 'error' : undefined"
                />
              </n-form-item>
            </div>
            <div class="form-row">
              <n-form-item label="密码（选填，自定义至少 12 位）">
                <n-input
                  v-model:value="formData.admin_password"
                  type="password"
                  show-password-on="click"
                  placeholder="留空自动生成，完成时显示"
                  :status="passwordError ? 'error' : undefined"
                />
              </n-form-item>
              <n-form-item label="确认密码">
                <n-input
                  v-model:value="formData.admin_password_confirm"
                  type="password"
                  show-password-on="click"
                  placeholder="再次输入密码"
                  :status="passwordConfirmError ? 'error' : undefined"
                />
              </n-form-item>
            </div>
          </div>

          <div class="form-group">
            <div class="form-group-title">
              <n-icon :size="16"><ColorWandOutline /></n-icon>
              <span>基础设施</span>
            </div>
            <div class="form-row">
              <n-form-item label="BMC 管理网段">
                <n-input
                  v-model:value="formData.bmc_network"
                  placeholder="192.168.1.0/24"
                  :status="networkError ? 'error' : undefined"
                />
              </n-form-item>
              <n-form-item label="机房名称">
                <n-input v-model:value="formData.datacenter_name" placeholder="默认机房" />
              </n-form-item>
            </div>
            <n-form-item label="机房位置">
              <n-input v-model:value="formData.datacenter_location" placeholder="如：北京市海淀区中关村" />
            </n-form-item>
          </div>
        </n-form>

        <div class="wizard-footer">
          <n-button quaternary @click="goToStep(0)" :disabled="submitting">上一步</n-button>
          <n-button
            type="primary"
            size="large"
            :disabled="!configValid"
            :loading="submitting"
            @click="submitConfig"
          >保存并继续</n-button>
        </div>
      </div>

      <!-- === Step 3: 资源接入 === -->
      <div v-else-if="currentStep === 2" class="wizard-body">
        <h2 class="wizard-heading">资源接入</h2>
        <p class="wizard-subtext">选择如何接入您的裸机服务器资源（此步骤可跳过）</p>

        <div class="resource-grid">
          <div
            v-for="opt in resourceOptions"
            :key="opt.key"
            class="resource-option"
            :class="{ picked: resourceMethod === opt.key }"
            @click="resourceMethod = opt.key"
          >
            <div class="resource-icon">
              <n-icon :size="24"><component :is="opt.icon" /></n-icon>
            </div>
            <strong>{{ opt.title }}</strong>
            <small>{{ opt.desc }}</small>
          </div>
        </div>

        <div v-if="resourceMethod === 'scan'" class="resource-hint">
          <n-alert type="info" :bordered="false">
            系统将根据您配置的 BMC 网段自动扫描在线设备，完成后可在资产列表中查看结果。
          </n-alert>
        </div>
        <div v-else-if="resourceMethod === 'manual'" class="resource-hint">
          <n-alert type="info" :bordered="false">
            进入系统后，请在「服务器管理」页面手动录入 BMC 地址和凭据信息。
          </n-alert>
        </div>
        <div v-else-if="resourceMethod === 'import'" class="resource-hint">
          <n-alert type="info" :bordered="false">
            进入系统后，请在「服务器管理」页面使用 Excel 批量导入功能上传资产清单。
          </n-alert>
        </div>

        <div class="wizard-footer">
          <n-button quaternary @click="goToStep(1)" :disabled="submitting">上一步</n-button>
          <div class="footer-actions">
            <n-button quaternary @click="continueResource">跳过此步</n-button>
            <n-button type="primary" size="large" @click="continueResource">继续</n-button>
          </div>
        </div>
      </div>

      <!-- === Step 4: 完成 === -->
      <div v-else class="wizard-body wizard-complete">
        <div class="complete-icon">
          <svg width="80" height="80" viewBox="0 0 80 80" fill="none">
            <circle cx="40" cy="40" r="38" stroke="#165DFF" stroke-width="2.5" opacity="0.12"/>
            <circle cx="40" cy="40" r="30" fill="#165DFF" opacity="0.06"/>
            <path d="M28 40l8 8 16-16" stroke="#165DFF" stroke-width="3" stroke-linecap="round" stroke-linejoin="round"/>
          </svg>
        </div>
        <h2 class="wizard-heading">配置完成</h2>
        <p class="wizard-subtext">系统已初始化完毕，下方为配置摘要</p>

        <div class="summary-panel">
          <div class="summary-item">
            <span class="summary-label">运行模式</span>
            <n-tag type="info" size="small" round>
              {{ selectedMode === 'self_use' ? '自用管理模式' : '对外运营模式' }}
            </n-tag>
          </div>
          <div class="summary-item">
            <span class="summary-label">管理员</span>
            <span class="summary-value">{{ formData.admin_username || 'admin' }}</span>
          </div>
          <div v-if="generatedPassword" class="summary-item">
            <span class="summary-label">自动生成密码</span>
            <code style="overflow-wrap: anywhere; user-select: all">{{ generatedPassword }}</code>
          </div>
          <p v-if="generatedPassword" role="note">请立即保存此密码。仅在本次初始化页面显示，刷新或离开后无法再次查看。</p>
          <div v-if="formData.datacenter_name" class="summary-item">
            <span class="summary-label">机房</span>
            <span class="summary-value">{{ formData.datacenter_name }}</span>
          </div>
          <div v-if="formData.bmc_network" class="summary-item">
            <span class="summary-label">BMC 网段</span>
            <span class="summary-value">{{ formData.bmc_network }}</span>
          </div>
        </div>

        <n-alert type="warning" :bordered="false" class="mode-warning">
          <template #icon>
            <n-icon><FlashOutline /></n-icon>
          </template>
          模式选定后不可切换。如需更改，请重置系统数据库。
        </n-alert>

        <div class="wizard-footer center">
          <n-button
            type="primary"
            size="large"
            :loading="submitting"
            @click="finishSetup"
          >进入管理平台</n-button>
        </div>
      </div>
    </template>
  </div>
</template>

<style scoped>
/* === 全局容器 === */
.setup-container {
  min-height: 100vh;
  display: flex;
  flex-direction: column;
  align-items: center;
  background: #f7f8fc;
  padding: 48px 24px;
  position: relative;
  overflow-x: hidden;
}
.setup-container::before {
  content: '';
  position: absolute;
  top: 0; left: 0; right: 0;
  height: 320px;
  background: linear-gradient(175deg, #edf2ff 0%, transparent 100%);
  pointer-events: none;
}

/* === 步骤指示器 === */
.wizard-steps {
  display: flex;
  align-items: flex-start;
  gap: 0;
  margin-bottom: 48px;
  position: relative;
  z-index: 1;
}
.wizard-step {
  display: flex;
  align-items: center;
  gap: 10px;
  position: relative;
  cursor: default;
  padding: 0 20px;
}
.wizard-step.done {
  cursor: pointer;
}
.step-node {
  width: 32px;
  height: 32px;
  border-radius: 50%;
  display: flex;
  align-items: center;
  justify-content: center;
  background: #e5e8f0;
  color: #8c9ab5;
  font-size: 13px;
  font-weight: 700;
  flex-shrink: 0;
  transition: all 0.3s cubic-bezier(0.4, 0, 0.2, 1);
  z-index: 2;
}
.wizard-step.active .step-node {
  background: #165DFF;
  color: #fff;
  box-shadow: 0 0 0 4px rgba(22, 93, 255, 0.15);
}
.wizard-step.done .step-node {
  background: #165DFF;
  color: #fff;
}
.step-num {
  line-height: 1;
}
.step-label {
  display: flex;
  flex-direction: column;
  gap: 2px;
}
.step-label strong {
  font-size: 13px;
  font-weight: 600;
  color: #4e5969;
  white-space: nowrap;
}
.wizard-step.active .step-label strong {
  color: #1a1a2e;
}
.step-label small {
  font-size: 11px;
  color: #a0aab5;
  white-space: nowrap;
}
.step-connector {
  position: absolute;
  top: 16px;
  left: calc(50% + 36px);
  width: calc(100% - 52px);
  height: 2px;
  background: #e5e8f0;
  z-index: 1;
}
.wizard-step.done .step-connector {
  background: #165DFF;
}

/* === 主体内容 === */
.wizard-body {
  width: 100%;
  max-width: 920px;
  position: relative;
  z-index: 1;
}
.wizard-heading {
  font-size: 26px;
  font-weight: 700;
  color: #1a1a2e;
  margin: 0 0 6px;
  text-align: center;
  letter-spacing: -0.5px;
}
.wizard-subtext {
  font-size: 14px;
  color: #8c8ca1;
  text-align: center;
  margin: 0 0 36px;
  line-height: 1.5;
}

/* === Step 1: 模式卡片 === */
.setup-cards {
  display: flex;
  gap: 28px;
  justify-content: center;
  margin-top: 8px;
}
.setup-card {
  flex: 1;
  max-width: 380px;
  padding: 32px 28px 24px;
  cursor: pointer;
  border: 2px solid #e5e8f0;
  border-radius: 16px;
  background: #fff;
  transition: all 0.25s cubic-bezier(0.4, 0, 0.2, 1);
  position: relative;
  display: flex;
  flex-direction: column;
}
.setup-card:hover {
  border-color: #a0b4ff;
  transform: translateY(-3px);
  box-shadow: 0 8px 32px rgba(22, 93, 255, 0.08);
}
.setup-card.selected {
  border-color: #165DFF;
  background: #f8faff;
  box-shadow: 0 0 0 3px rgba(22, 93, 255, 0.1), 0 8px 32px rgba(22, 93, 255, 0.08);
}
.card-top {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  margin-bottom: 16px;
}
.card-icon {
  width: 52px;
  height: 52px;
  border-radius: 12px;
  display: flex;
  align-items: center;
  justify-content: center;
}
.card-icon.icon-self-use {
  background: rgba(22, 93, 255, 0.08);
  color: #165DFF;
}
.card-icon.icon-external {
  background: rgba(0, 180, 42, 0.08);
  color: #00B42A;
}
.card-badge {
  width: 28px;
  height: 28px;
  display: flex;
  align-items: center;
  justify-content: center;
  animation: badge-pop 0.3s ease;
}
@keyframes badge-pop {
  from { transform: scale(0); }
  to { transform: scale(1); }
}
.card-title {
  font-size: 18px;
  font-weight: 700;
  color: #1a1a2e;
  margin: 0 0 8px;
}
.card-desc {
  font-size: 14px;
  color: #6b7280;
  line-height: 1.6;
  margin: 0 0 20px;
}
.card-features {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  margin-top: auto;
  margin-bottom: 20px;
}
.card-action {
  padding-top: 16px;
  border-top: 1px solid #f0f2f5;
}

/* === Step 2: 配置表单 === */
.config-form {
  max-width: 640px;
  margin: 0 auto;
}
.form-group {
  margin-bottom: 28px;
  padding: 20px 24px;
  background: #fff;
  border-radius: 12px;
  border: 1px solid #eef0f5;
}
.form-group-title {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 14px;
  font-weight: 600;
  color: #1a1a2e;
  margin-bottom: 18px;
  padding-bottom: 12px;
  border-bottom: 1px solid #f0f2f5;
}
.form-group-title span {
  color: #4e5969;
}
.form-row {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 16px;
}

/* === Step 3: 资源接入 === */
.resource-grid {
  display: grid;
  grid-template-columns: repeat(3, 1fr);
  gap: 20px;
  max-width: 640px;
  margin: 0 auto 24px;
}
.resource-option {
  padding: 28px 20px;
  border: 2px solid #e5e8f0;
  border-radius: 14px;
  text-align: center;
  cursor: pointer;
  transition: all 0.2s ease;
  background: #fff;
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 10px;
}
.resource-option:hover {
  border-color: #a0b4ff;
  transform: translateY(-2px);
  box-shadow: 0 4px 16px rgba(22, 93, 255, 0.06);
}
.resource-option.picked {
  border-color: #165DFF;
  background: #f8faff;
  box-shadow: 0 0 0 3px rgba(22, 93, 255, 0.1);
}
.resource-icon {
  width: 48px;
  height: 48px;
  border-radius: 12px;
  background: rgba(22, 93, 255, 0.06);
  color: #165DFF;
  display: flex;
  align-items: center;
  justify-content: center;
}
.resource-option strong {
  font-size: 15px;
  color: #1a1a2e;
}
.resource-option small {
  font-size: 12px;
  color: #8c8ca1;
  line-height: 1.5;
}
.resource-hint {
  max-width: 640px;
  margin: 0 auto;
}

/* === Step 4: 完成 === */
.wizard-complete {
  text-align: center;
}
.complete-icon {
  margin-bottom: 20px;
}
.summary-panel {
  max-width: 420px;
  margin: 0 auto 28px;
  background: #fff;
  border-radius: 12px;
  border: 1px solid #eef0f5;
  padding: 20px 28px;
  text-align: left;
}
.summary-item {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 10px 0;
  border-bottom: 1px solid #f5f6fa;
}
.summary-item:last-child {
  border-bottom: none;
}
.summary-label {
  font-size: 13px;
  color: #8c8ca1;
}
.summary-value {
  font-size: 14px;
  font-weight: 500;
  color: #1a1a2e;
}
.mode-warning {
  max-width: 420px;
  margin: 0 auto 28px;
  text-align: left;
}

/* === 底部操作栏 === */
.wizard-footer {
  display: flex;
  align-items: center;
  justify-content: space-between;
  max-width: 720px;
  margin: 40px auto 0;
  padding-top: 24px;
  border-top: 1px solid #eef0f5;
}
.wizard-footer.center {
  justify-content: center;
  border-top: none;
}
.footer-actions {
  display: flex;
  gap: 12px;
  align-items: center;
}

/* === 响应式 === */
@media (max-width: 768px) {
  .setup-container {
    padding: 24px 16px;
  }
  .wizard-steps {
    flex-wrap: wrap;
    justify-content: center;
    gap: 4px;
    margin-bottom: 32px;
  }
  .wizard-step {
    padding: 0 12px;
  }
  .step-connector {
    display: none;
  }
  .step-label small {
    display: none;
  }
  .setup-cards {
    flex-direction: column;
    align-items: center;
    gap: 16px;
  }
  .setup-card {
    max-width: 100%;
    width: 100%;
  }
  .form-row {
    grid-template-columns: 1fr;
  }
  .resource-grid {
    grid-template-columns: 1fr;
    gap: 12px;
  }
}
</style>
