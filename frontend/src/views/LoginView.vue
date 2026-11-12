<script setup lang="ts">
// 登录页 — 展示平台品牌并校验账号密码
import { computed, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { NButton, NInput, NForm, NFormItem, useMessage } from 'naive-ui'
import { useAuth } from '../stores/auth'
import { errorText } from '../api'
import { loadBrand, loginNotice, platformLogo, platformName, registrationEnabled } from '../stores/brand'

const username = ref('')
const password = ref('')
const loading = ref(false)
const auth = useAuth()
const router = useRouter()
const message = useMessage()

// 注册入口只在“对外运营模式 + 管理员已开放注册”时出现，自用模式不暴露该入口。
const showRegister = computed(
  () => auth.systemMode === 'external_operation' && registrationEnabled.value
)

onMounted(async () => {
  // 读取管理员配置的平台名称与 Logo
  await loadBrand()
  // 公开路由守卫不会拉取部署模式，此处补齐（供注册入口判断）
  if (!auth.systemMode) await auth.checkSetupStatus()
})

async function submit() {
  if (!username.value || !password.value) return
  loading.value = true
  try {
    await auth.login(username.value, password.value)
    router.replace('/')
  } catch (error) {
    message.error(errorText(error))
  } finally {
    loading.value = false
  }
}
</script>

<template>
  <div class="login-screen">
    <div class="login-side">
      <div class="login-side-top">
        <img v-if="platformLogo" class="brand-logo" :src="platformLogo" alt="平台 Logo" />
        <span v-else class="brand-mark">M<span>·</span></span>
        <strong>{{ platformName }}</strong>
      </div>
      <div class="login-side-body">
        <div class="eyebrow">INFRASTRUCTURE OPERATIONS / 01</div>
        <h1>
          基础设施，<br /><em>尽在掌控。</em>
        </h1>
        <p>从物理资产到操作系统交付，统一管理您的裸机基础设施。</p>
        <div class="login-rule"></div>
        <div class="login-metrics">
          <div>
            <strong>01</strong>
            <span>资产发现</span>
          </div>
          <div>
            <strong>02</strong>
            <span>远程运维</span>
          </div>
          <div>
            <strong>03</strong>
            <span>自动交付</span>
          </div>
        </div>
      </div>
      <small>BAREMETAL CONTROL PLANE © 2026</small>
    </div>
    <div class="login-panel">
      <div class="login-card">
        <div class="eyebrow dark">WELCOME BACK</div>
        <h2>登录控制台</h2>
        <p>请输入账户信息以继续访问。</p>
        <p v-if="loginNotice" class="login-notice">{{ loginNotice }}</p>
        <n-form @submit.prevent="submit">
          <n-form-item label="用户名">
            <n-input
              v-model:value="username"
              placeholder="请输入用户名"
              autocomplete="username"
              size="large"
            />
          </n-form-item>
          <n-form-item label="密码">
            <n-input
              v-model:value="password"
              type="password"
              show-password-on="click"
              placeholder="请输入密码"
              autocomplete="current-password"
              size="large"
              @keyup.enter="submit"
            />
          </n-form-item>
          <n-button
            type="primary"
            block
            size="large"
            :loading="loading"
            attr-type="submit"
            class="login-submit"
          >
            登录平台 →
          </n-button>
        </n-form>
        <div class="login-note">
          <span class="login-note-main"><span class="status-dot"></span> 安全连接 · 操作全程审计</span>
          <router-link v-if="showRegister" class="login-register" to="/register">注册账号 →</router-link>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.login-note {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: center;
  gap: 8px 16px;
}

/* 注册入口有意做得低调：仅在运营模式且开放注册时出现的一行辅助文字 */
.login-register {
  color: var(--color-text-tertiary);
  font-size: var(--font-size-xs);
  letter-spacing: 0.02em;
  border-bottom: 1px dashed transparent;
  transition: color var(--transition-fast), border-color var(--transition-fast);
}

.login-register:hover {
  color: var(--color-primary);
  border-bottom-color: var(--color-primary-light);
}
</style>
