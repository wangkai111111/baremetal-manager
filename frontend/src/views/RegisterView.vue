<script setup lang="ts">
// 租户自助注册页 — 仅对外运营模式开放，模式与开关由后端 registerTenant 权威校验
import { computed, onMounted, onUnmounted, reactive, ref } from 'vue'
import { useRouter } from 'vue-router'
import {
  NAlert, NButton, NCard, NForm, NFormItem, NIcon, NInput, useMessage
} from 'naive-ui'
import { CheckmarkOutline } from '@vicons/ionicons5'
import { authRegister, errorText } from '../api'
import { useAuth } from '../stores/auth'
import { loadBrand, platformLogo, platformName, registrationEnabled } from '../stores/brand'

// 与后端 auth_register.go 的校验规则保持一致
const EMAIL_RE = /^[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}$/
const PHONE_RE = /^1[3-9]\d{9}$/

const router = useRouter()
const auth = useAuth()
const message = useMessage()

const model = reactive({
  username: '',
  email: '',
  phone: '',
  password: '',
  confirm: '',
  company: ''
})

const submitting = ref(false)
const submitError = ref('')
const submitAttempted = ref(false)
const success = ref(false)
const countdown = ref(2)
const modeResolved = ref(false)
const brandResolved = ref(false)

let redirectTimer: ReturnType<typeof setTimeout> | null = null
let tickTimer: ReturnType<typeof setInterval> | null = null

// 部署模式不是对外运营时，页面直接进入“未开放”状态，不让人白填一遍表单
const modeBlocked = computed(() => modeResolved.value && auth.systemMode !== 'external_operation')
// 模式允许、但管理员尚未打开注册开关时给出预告提示
const switchHint = computed(() => brandResolved.value && !modeBlocked.value && !registrationEnabled.value)

const errors = computed(() => {
  const out: Partial<Record<'username' | 'email' | 'phone' | 'password' | 'confirm' | 'company' | 'contact', string>> = {}
  const username = model.username.trim()
  const email = model.email.trim()
  const phone = model.phone.trim()
  if (!username) {
    if (submitAttempted.value) out.username = '请输入用户名'
  } else if (username.length < 3) {
    out.username = '用户名至少 3 个字符'
  } else if (username.length > 255) {
    out.username = '用户名长度不超过 255 个字符'
  }
  if (email && !EMAIL_RE.test(email)) out.email = '邮箱格式不正确'
  if (phone && !PHONE_RE.test(phone)) out.phone = '手机号格式不正确'
  if (!email && !phone) {
    if (submitAttempted.value) out.contact = '请至少填写邮箱或手机号'
  } else if (email && email.length > 255) {
    out.email = '邮箱长度不超过 255 个字符'
  }
  if (!model.password) {
    if (submitAttempted.value) out.password = '请输入密码'
  } else if (model.password.length < 6) {
    out.password = '密码至少 6 个字符'
  } else if (model.password.length > 72) {
    out.password = '密码最多 72 个字符'
  }
  if (!model.confirm) {
    if (submitAttempted.value) out.confirm = '请再次输入密码'
  } else if (model.confirm !== model.password) {
    out.confirm = '两次输入的密码不一致'
  }
  if (model.company.trim().length > 100) out.company = '公司/组织名称长度不超过 100 个字符'
  return out
})

const passwordLevel = computed(() => {
  const p = model.password
  if (!p) return { score: 0, label: '未设置', tone: 'idle' as const }
  let variety = 0
  if (/[a-z]/.test(p)) variety++
  if (/[A-Z]/.test(p)) variety++
  if (/\d/.test(p)) variety++
  if (/[^A-Za-z0-9]/.test(p)) variety++
  if (p.length < 6) return { score: 1, label: '太短', tone: 'weak' as const }
  if (p.length >= 12 && variety >= 3) return { score: 3, label: '强度良好', tone: 'strong' as const }
  if (p.length >= 8 && variety >= 2) return { score: 2, label: '强度中等', tone: 'medium' as const }
  return { score: 1, label: '强度偏弱', tone: 'weak' as const }
})

onMounted(async () => {
  // 品牌与部署模式并行拉取，两者均为公开接口
  await Promise.all([
    loadBrand().finally(() => { brandResolved.value = true }),
    auth.checkSetupStatus().finally(() => { modeResolved.value = true })
  ])
})

onUnmounted(() => {
  if (redirectTimer) clearTimeout(redirectTimer)
  if (tickTimer) clearInterval(tickTimer)
})

async function submit() {
  submitAttempted.value = true
  submitError.value = ''
  // 防重复提交：Enter 键可能同时触发隐式表单提交与显式回车监听
  if (submitting.value || success.value) return
  if (Object.keys(errors.value).length > 0 || modeBlocked.value) return
  submitting.value = true
  try {
    await authRegister({
      username: model.username.trim(),
      password: model.password,
      email: model.email.trim(),
      phone: model.phone.trim(),
      company_name: model.company.trim()
    })
    // 注册接口同时返回了自动登录 token，此处刻意不落盘：
    // 让用户回到登录页重新登录，以确保登录态与模式上下文一致地建立。
    success.value = true
    message.success('注册成功，租户空间已创建')
    startRedirect()
  } catch (error) {
    submitError.value = errorText(error)
    message.error(submitError.value)
  } finally {
    submitting.value = false
  }
}

function startRedirect() {
  if (tickTimer) clearInterval(tickTimer)
  tickTimer = setInterval(() => {
    countdown.value = countdown.value > 0 ? countdown.value - 1 : 0
  }, 1000)
  if (redirectTimer) clearTimeout(redirectTimer)
  redirectTimer = setTimeout(() => router.replace('/login'), 2000)
}

function goLogin() {
  if (redirectTimer) clearTimeout(redirectTimer)
  router.replace('/login')
}
</script>

<template>
  <div class="register-screen">
    <!-- 左：品牌叙事侧栏 -->
    <aside class="reg-rail">
      <div class="reg-rail-grid" aria-hidden="true"></div>
      <div class="reg-rail-top">
        <img v-if="platformLogo" class="brand-logo" :src="platformLogo" alt="平台 Logo" />
        <span v-else class="brand-mark">M<span>·</span></span>
        <strong>{{ platformName }}</strong>
      </div>
      <div class="reg-rail-body">
        <div class="eyebrow">TENANT SIGN-UP / SELF SERVICE</div>
        <h1>
          创建独立租户空间，<br /><em>裸机资源即选即用。</em>
        </h1>
        <p>注册即自动开通租户、默认项目与管理员账号，无需等待人工交付。</p>
        <ol class="reg-provision">
          <li>
            <span class="reg-provision-key">01</span>
            <div><strong>租户与默认项目</strong><small>隔离的资源边界与成员体系</small></div>
          </li>
          <li>
            <span class="reg-provision-key">02</span>
            <div><strong>管理员账号与角色</strong><small>预置 admin / manager / operator</small></div>
          </li>
          <li>
            <span class="reg-provision-key">03</span>
            <div><strong>按需采购与结算</strong><small>账户、订单与账单一站管理</small></div>
          </li>
        </ol>
      </div>
      <small>BAREMETAL CONTROL PLANE © 2026</small>
    </aside>

    <!-- 右：注册表单 -->
    <main class="reg-panel">
      <n-card class="reg-card" :bordered="false">
        <template v-if="!success">
          <div class="eyebrow dark">CREATE ACCOUNT</div>
          <h2>注册新账号</h2>
          <p class="reg-sub">填写以下信息以创建您的租户空间，带 * 为必填。</p>

          <n-alert v-if="modeBlocked" class="reg-alert" type="warning" :bordered="false">
            当前部署模式未开放自助注册，请联系平台管理员开通账号。
          </n-alert>
          <n-alert v-else-if="switchHint" class="reg-alert" type="info" :bordered="false">
            管理员尚未开放注册开关，提交可能被拒绝。
          </n-alert>

          <p v-if="submitError" class="reg-error-banner">{{ submitError }}</p>

          <n-form
            label-placement="top"
            size="large"
            @submit.prevent="submit"
          >
            <n-form-item
              label="用户名"
              required
              :validation-status="errors.username ? 'error' : undefined"
              :feedback="errors.username"
            >
              <n-input
                v-model:value="model.username"
                placeholder="用于登录的唯一账号名"
                autocomplete="username"
                maxlength="255"
              />
            </n-form-item>

            <div class="reg-grid">
              <n-form-item
                label="邮箱"
                :validation-status="errors.email ? 'error' : undefined"
                :feedback="errors.email"
              >
                <n-input v-model:value="model.email" placeholder="you@company.com" autocomplete="email" />
              </n-form-item>
              <n-form-item
                label="手机号"
                :validation-status="errors.phone ? 'error' : undefined"
                :feedback="errors.phone"
              >
                <n-input v-model:value="model.phone" placeholder="11 位中国大陆手机号" autocomplete="tel" maxlength="11" />
              </n-form-item>
            </div>
            <p class="reg-hint" :class="{ danger: errors.contact }">
              {{ errors.contact || '邮箱与手机号至少填写一项，用于登录与告警通知。' }}
            </p>

            <div class="reg-field">
              <div class="reg-field-head">
                <label>密码 <b>*</b></label>
                <span class="reg-strength" :class="passwordLevel.tone">
                  <i v-for="n in 3" :key="n" :class="{ on: passwordLevel.score >= n }"></i>
                  <em>{{ passwordLevel.label }}</em>
                </span>
              </div>
              <n-input
                v-model:value="model.password"
                type="password"
                show-password-on="click"
                placeholder="至少 6 位，建议混合大小写与数字"
                autocomplete="new-password"
                maxlength="72"
              />
              <p v-if="errors.password" class="reg-field-error">{{ errors.password }}</p>
            </div>

            <n-form-item
              label="确认密码"
              required
              :validation-status="errors.confirm ? 'error' : undefined"
              :feedback="errors.confirm"
            >
              <n-input
                v-model:value="model.confirm"
                type="password"
                show-password-on="click"
                placeholder="请再次输入密码"
                autocomplete="new-password"
                maxlength="72"
              />
            </n-form-item>

            <n-form-item
              label="公司 / 组织名称"
              :validation-status="errors.company ? 'error' : undefined"
              :feedback="errors.company"
            >
              <n-input v-model:value="model.company" placeholder="选填，用于租户档案与发票抬头" maxlength="100" />
            </n-form-item>

            <n-button
              class="reg-submit"
              type="primary"
              block
              size="large"
              :loading="submitting"
              :disabled="modeBlocked"
              attr-type="submit"
            >
              创建租户账号 →
            </n-button>
          </n-form>

          <div class="reg-foot">
            已有账号？<router-link to="/login">返回登录</router-link>
          </div>
        </template>

        <!-- 注册成功态 -->
        <template v-else>
          <div class="reg-success">
            <div class="reg-success-mark">
              <n-icon :component="CheckmarkOutline" :size="30" />
            </div>
            <div class="eyebrow dark">ACCOUNT CREATED</div>
            <h2>租户空间已创建</h2>
            <p>
              已为 <b>{{ model.username }}</b> 开通租户、默认项目与管理员权限。
            </p>
            <n-button class="reg-submit" type="primary" block size="large" @click="goLogin">
              立即登录 →
            </n-button>
            <div class="reg-countdown">{{ countdown }} 秒后自动跳转到登录页</div>
          </div>
        </template>
      </n-card>
    </main>
  </div>
</template>

<style scoped>
/* === 布局骨架：与登录页同族，左叙事右操作 === */
.register-screen {
  min-height: 100vh;
  display: grid;
  grid-template-columns: 44% 56%;
  background: var(--color-bg);
}

.reg-rail {
  position: relative;
  overflow: hidden;
  display: flex;
  flex-direction: column;
  justify-content: space-between;
  gap: 32px;
  padding: 40px clamp(28px, 4.4vw, 72px);
  background: var(--color-nav-bg);
  color: var(--color-text-inverse);
}

/* 蓝图网格底纹 */
.reg-rail-grid {
  position: absolute;
  inset: 0;
  background-image:
    linear-gradient(rgba(64, 128, 255, 0.08) 1px, transparent 1px),
    linear-gradient(90deg, rgba(64, 128, 255, 0.08) 1px, transparent 1px);
  background-size: 34px 34px;
  -webkit-mask-image: radial-gradient(130% 95% at 14% 88%, #000 12%, transparent 72%);
  mask-image: radial-gradient(130% 95% at 14% 88%, #000 12%, transparent 72%);
  pointer-events: none;
}

.reg-rail::after {
  content: '';
  position: absolute;
  left: -16vw;
  bottom: -32vw;
  width: 52vw;
  height: 52vw;
  border: 1px solid rgba(64, 128, 255, 0.22);
  border-radius: 50%;
  pointer-events: none;
  animation: rail-orbit 16s ease-in-out infinite alternate;
}

@keyframes rail-orbit {
  from { transform: translate3d(0, 0, 0) scale(1); opacity: 0.55; }
  to { transform: translate3d(4vw, -3vw, 0) scale(1.08); opacity: 1; }
}

.reg-rail-top,
.reg-rail-body,
.reg-rail > small {
  position: relative;
  z-index: 1;
}

.reg-rail-top {
  display: flex;
  align-items: center;
  gap: 12px;
}

.reg-rail-top strong {
  color: #fff;
  font-size: 18px;
  font-weight: 700;
  letter-spacing: 1px;
}

.reg-rail-body h1 {
  margin: 16px 0 18px;
  font-size: clamp(30px, 3.4vw, 50px);
  line-height: 1.18;
  letter-spacing: -1.2px;
  color: var(--color-text-inverse);
}

.reg-rail-body h1 em {
  font-style: normal;
  color: var(--color-primary-hover);
}

.reg-rail-body > p {
  max-width: 420px;
  margin: 0;
  font-size: var(--font-size-base);
  line-height: 1.75;
  color: var(--color-nav-text);
}

.reg-rail > small {
  font-size: var(--font-size-xs);
  letter-spacing: 0.8px;
  color: var(--color-nav-text);
  opacity: 0.65;
}

/* 开通清单 */
.reg-provision {
  display: flex;
  flex-direction: column;
  gap: 14px;
  margin: 34px 0 0;
  padding: 26px 0 0;
  list-style: none;
  border-top: 1px solid var(--color-nav-border);
}

.reg-provision li {
  display: flex;
  align-items: flex-start;
  gap: 14px;
}

.reg-provision-key {
  flex: none;
  padding: 3px 8px;
  border: 1px solid rgba(64, 128, 255, 0.35);
  border-radius: var(--radius-sm);
  background: rgba(64, 128, 255, 0.1);
  color: var(--color-primary-hover);
  font: 700 11px/1.6 ui-monospace, SFMono-Regular, Menlo, monospace;
  letter-spacing: 0.08em;
}

.reg-provision strong {
  display: block;
  font-size: var(--font-size-base);
  font-weight: 600;
  color: #eaf1f7;
}

.reg-provision small {
  display: block;
  margin-top: 3px;
  font-size: var(--font-size-xs);
  color: var(--color-nav-text);
}

/* 入场节奏：错落浮现 */
.reg-rail-top,
.reg-rail-body .eyebrow,
.reg-rail-body h1,
.reg-rail-body > p,
.reg-provision li {
  opacity: 0;
  animation: rail-rise 0.62s cubic-bezier(0.22, 0.68, 0.36, 1) forwards;
}

.reg-rail-body .eyebrow { animation-delay: 0.06s; }
.reg-rail-body h1 { animation-delay: 0.12s; }
.reg-rail-body > p { animation-delay: 0.2s; }
.reg-provision li:nth-child(1) { animation-delay: 0.28s; }
.reg-provision li:nth-child(2) { animation-delay: 0.36s; }
.reg-provision li:nth-child(3) { animation-delay: 0.44s; }

@keyframes rail-rise {
  from { opacity: 0; transform: translate3d(0, 14px, 0); }
  to { opacity: 1; transform: none; }
}

/* === 表单侧 === */
.reg-panel {
  display: grid;
  place-items: center;
  padding: 34px 26px;
}

.reg-card {
  width: min(540px, 100%);
  border: 1px solid var(--color-border);
  border-radius: var(--radius-xl);
  box-shadow: var(--shadow-lg);
  background: var(--color-surface);
  animation: card-in 0.5s cubic-bezier(0.22, 0.68, 0.36, 1) both;
}

@keyframes card-in {
  from { opacity: 0; transform: translate3d(0, 18px, 0); }
  to { opacity: 1; transform: none; }
}

.reg-card h2 {
  margin: 12px 0 6px;
  font-size: var(--font-size-2xl);
  font-weight: 700;
  letter-spacing: -0.4px;
  color: var(--color-text-primary);
}

.reg-sub {
  margin: 0 0 22px;
  font-size: var(--font-size-sm);
  color: var(--color-text-tertiary);
}

.reg-alert {
  margin-bottom: 16px;
  font-size: var(--font-size-sm);
}

.reg-error-banner {
  margin: 0 0 16px;
  padding: 10px 14px;
  border-left: 3px solid var(--color-error);
  border-radius: var(--radius-sm);
  background: var(--color-error-light);
  color: var(--color-error);
  font-size: var(--font-size-sm);
  line-height: 1.6;
}

.reg-card :deep(.n-form-item) {
  margin-bottom: 16px;
}

.reg-grid {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 0 16px;
}

.reg-hint {
  margin: -6px 0 16px;
  font-size: var(--font-size-xs);
  line-height: 1.6;
  color: var(--color-text-tertiary);
}

.reg-hint.danger {
  color: var(--color-error);
}

/* 密码强度条（不用 n-form-item，避免与强度条同行挤压） */
.reg-field {
  margin-bottom: 16px;
}

.reg-field-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  margin-bottom: 6px;
}

.reg-field-head label {
  font-size: var(--font-size-sm);
  color: var(--color-text-secondary);
}

.reg-field-head b {
  color: var(--color-error);
  font-weight: 600;
}

.reg-strength {
  display: inline-flex;
  align-items: center;
  gap: 4px;
}

.reg-strength i {
  width: 16px;
  height: 3px;
  border-radius: var(--radius-full);
  background: var(--color-border);
  transition: background var(--transition-fast);
}

.reg-strength em {
  margin-left: 4px;
  font-size: var(--font-size-xs);
  font-style: normal;
  color: var(--color-text-tertiary);
}

.reg-strength.weak i.on { background: var(--color-error); }
.reg-strength.weak em { color: var(--color-error); }
.reg-strength.medium i.on { background: var(--color-warning); }
.reg-strength.medium em { color: var(--color-warning); }
.reg-strength.strong i.on { background: var(--color-success); }
.reg-strength.strong em { color: var(--color-success); }

.reg-field-error {
  margin: 6px 0 0;
  font-size: var(--font-size-xs);
  color: var(--color-error);
}

.reg-submit {
  margin-top: 8px;
  height: 44px;
  font-weight: 600;
  letter-spacing: 0.02em;
}

.reg-foot {
  margin-top: 20px;
  text-align: center;
  font-size: var(--font-size-xs);
  color: var(--color-text-tertiary);
}

.reg-foot a {
  color: var(--color-primary);
  font-weight: 500;
}

.reg-foot a:hover {
  color: var(--color-primary-hover);
}

/* 成功态 */
.reg-success {
  text-align: center;
  padding: 10px 0 4px;
}

.reg-success-mark {
  width: 62px;
  height: 62px;
  margin: 0 auto 16px;
  display: grid;
  place-items: center;
  border-radius: 50%;
  color: var(--color-success);
  background: var(--color-success-light);
  box-shadow: 0 0 0 8px rgba(0, 180, 42, 0.06);
  animation: pop-in 0.46s cubic-bezier(0.2, 0.9, 0.3, 1.3) both;
}

@keyframes pop-in {
  from { opacity: 0; transform: scale(0.72); }
  to { opacity: 1; transform: none; }
}

.reg-success h2 {
  margin: 10px 0 8px;
  font-size: var(--font-size-2xl);
  font-weight: 700;
  color: var(--color-text-primary);
}

.reg-success p {
  margin: 0 0 24px;
  font-size: var(--font-size-sm);
  line-height: 1.7;
  color: var(--color-text-tertiary);
}

.reg-success p b {
  color: var(--color-text-primary);
  font-weight: 600;
}

.reg-countdown {
  margin-top: 14px;
  font-size: var(--font-size-xs);
  color: var(--color-text-tertiary);
}

/* === 响应式：窄屏收起叙事侧栏 === */
@media (max-width: 1024px) {
  .register-screen { grid-template-columns: 1fr; }
  .reg-rail { padding: 30px 24px; gap: 20px; }
  .reg-rail-body h1 { font-size: 28px; margin-top: 10px; }
  .reg-rail-body > p,
  .reg-provision,
  .reg-rail > small { display: none; }
  .reg-panel { padding: 26px 16px 40px; }
}

@media (max-width: 560px) {
  .reg-grid { grid-template-columns: 1fr; }
}
</style>
