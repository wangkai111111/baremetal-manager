<script setup lang="ts">
import { ref } from 'vue'
import { useRouter } from 'vue-router'
import { NButton, NInput, NForm, NFormItem, useMessage } from 'naive-ui'
import { useAuth } from '../stores/auth'
import { errorText } from '../api'

const username = ref('')
const password = ref('')
const loading = ref(false)
const auth = useAuth()
const router = useRouter()
const message = useMessage()

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
        <span class="brand-mark">M<span>·</span></span>
        <strong>METAL<span>OPS</span></strong>
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
          <span class="status-dot"></span> 安全连接 · 操作全程审计
        </div>
      </div>
    </div>
  </div>
</template>
