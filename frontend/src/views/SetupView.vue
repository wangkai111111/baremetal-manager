<script setup lang="ts">
// 系统初始化向导页面 — 将在下一阶段实现
import { computed } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { NCard, NStep, NSteps, NButton } from 'naive-ui'
import { useAuth } from '../stores/auth'

const route = useRoute()
const router = useRouter()
const auth = useAuth()

const steps = [
  { title: '欢迎', description: '系统初始化引导' },
  { title: '选择模式', description: '自用或对外运营' },
  { title: '基础配置', description: '管理员账户与平台设置' },
  { title: '完成', description: '确认并进入系统' }
]

const currentStep = computed(() => {
  const step = route.params.step
  if (!step) return 0
  const idx = parseInt(String(step), 10) - 1
  return idx >= 0 && idx < steps.length ? idx : 0
})

function nextStep() {
  const next = currentStep.value + 1
  if (next < steps.length) {
    router.push(`/setup/${next + 1}`)
  }
}

function prevStep() {
  const prev = currentStep.value - 1
  if (prev >= 0) {
    router.push(prev === 0 ? '/setup' : `/setup/${prev + 1}`)
  }
}

async function finishSetup() {
  // 完成初始化后跳转到首页
  await auth.checkSetupStatus()
  router.push('/')
}
</script>

<template>
  <div class="setup-page">
    <div class="setup-container">
      <div class="setup-header">
        <h1 class="setup-title">Baremetal Manager 初始化</h1>
        <p class="setup-subtitle">欢迎使用裸机管理平台，请完成以下配置步骤</p>
      </div>

      <n-steps :current="currentStep + 1" size="small" style="margin-bottom: 32px;">
        <n-step v-for="(s, i) in steps" :key="i" :title="s.title" :description="s.description" />
      </n-steps>

      <n-card class="setup-card">
        <div v-if="currentStep === 0" class="step-content">
          <h2>欢迎使用 Baremetal Manager</h2>
          <p>本向导将帮助您完成系统的初始配置。请按照步骤操作。</p>
        </div>

        <div v-else-if="currentStep === 1" class="step-content">
          <h2>选择运行模式</h2>
          <p>请选择适合您业务场景的运行模式：</p>
          <div class="mode-cards">
            <div class="mode-card">
              <h3>自用管理模式</h3>
              <p>管理自有裸机资产，侧重基础设施运维</p>
            </div>
            <div class="mode-card">
              <h3>对外运营模式</h3>
              <p>向租户提供裸机服务，包含计费、订单等功能</p>
            </div>
          </div>
        </div>

        <div v-else-if="currentStep === 2" class="step-content">
          <h2>基础配置</h2>
          <p>设置管理员账户和平台基础参数。</p>
        </div>

        <div v-else-if="currentStep === 3" class="step-content">
          <h2>配置完成</h2>
          <p>所有配置已就绪，点击下方按钮进入系统。</p>
        </div>

        <div class="step-actions">
          <n-button v-if="currentStep > 0" @click="prevStep">上一步</n-button>
          <n-button v-if="currentStep < steps.length - 1" type="primary" @click="nextStep">下一步</n-button>
          <n-button v-if="currentStep === steps.length - 1" type="primary" @click="finishSetup">进入系统</n-button>
        </div>
      </n-card>
    </div>
  </div>
</template>

<style scoped>
.setup-page {
  min-height: 100vh;
  display: flex;
  align-items: center;
  justify-content: center;
  background: var(--n-color, #f5f7fa);
  padding: 24px;
}
.setup-container {
  width: 100%;
  max-width: 720px;
}
.setup-header {
  text-align: center;
  margin-bottom: 32px;
}
.setup-title {
  font-size: 28px;
  font-weight: 700;
  margin: 0 0 8px;
  color: var(--n-text-color, #333);
}
.setup-subtitle {
  font-size: 14px;
  color: var(--n-text-color-3, #999);
  margin: 0;
}
.setup-card {
  min-height: 300px;
}
.step-content {
  padding: 16px 0;
}
.step-content h2 {
  margin: 0 0 12px;
  font-size: 20px;
}
.step-content p {
  color: var(--n-text-color-2, #666);
  line-height: 1.6;
}
.mode-cards {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 16px;
  margin-top: 16px;
}
.mode-card {
  border: 1px solid var(--n-border-color, #e5e6eb);
  border-radius: 8px;
  padding: 20px;
  cursor: pointer;
  transition: border-color 0.2s;
}
.mode-card:hover {
  border-color: var(--n-color-target, #165dff);
}
.mode-card h3 {
  margin: 0 0 8px;
  font-size: 16px;
}
.mode-card p {
  margin: 0;
  font-size: 13px;
}
.step-actions {
  display: flex;
  gap: 12px;
  justify-content: flex-end;
  margin-top: 24px;
  padding-top: 16px;
  border-top: 1px solid var(--n-border-color, #e5e6eb);
}
</style>
