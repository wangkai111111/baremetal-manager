<script setup lang="ts">
import { computed, onBeforeUnmount, ref } from 'vue'
import { NAlert, NTag } from 'naive-ui'
import dayjs from 'dayjs'
export type InstallationStep = { key: string; title: string; status: string; timeout_seconds: number; started_at?: string; completed_at?: string; deadline_at?: string; message?: string }
export type InstallationProgressData = { id: number; operation_id: number; status: string; error?: string; steps: InstallationStep[]; deadline_at?: string; started_at?: string; completed_at?: string; cleanup_pending?: boolean }
const props = defineProps<{ installation: InstallationProgressData; serverTime?: string }>()
const clock = ref(Date.now())
const timer = setInterval(() => { clock.value = Date.now() }, 1000)
onBeforeUnmount(() => clearInterval(timer))
const offset = computed(() => props.serverTime ? Date.parse(props.serverTime) - Date.now() : 0)
const now = computed(() => clock.value + offset.value)
const current = computed(() => props.installation.steps?.find(s => s.status === 'running'))
const active = computed(() => ['pending', 'preparing', 'booting', 'installing', 'post_install', 'unknown'].includes(props.installation.status))
const labels: Record<string, string> = { pending: '等待执行', running: '进行中', succeeded: '已完成', failed: '失败', cancelled: '已取消', skipped: '未单独执行' }
const stamp = (date?: string) => date ? dayjs(date).format('MM-DD HH:mm:ss') : '—'
function duration(seconds: number) { const n = Math.max(0, Math.floor(seconds)); return n >= 60 ? `${Math.floor(n / 60)} 分 ${n % 60} 秒` : `${n} 秒` }
function elapsed(step: InstallationStep) { return step.started_at ? duration(((step.completed_at ? Date.parse(step.completed_at) : now.value) - Date.parse(step.started_at)) / 1000) : '—' }
function remaining(date?: string) { if (!date) return '—'; const seconds = (Date.parse(date) - now.value) / 1000; return seconds <= 0 ? '已到时限，后台正在结束任务' : duration(seconds) }
</script>
<template>
  <section class="install-progress" aria-label="安装步骤进度">
    <div class="progress-heading">
      <div><strong>{{ current ? `当前：${current.title}` : installation.status === 'succeeded' ? '安装及磁盘启动验证已完成' : active ? '等待阶段确认' : '安装任务已结束' }}</strong><small v-if="active">阶段剩余 {{ remaining(current?.deadline_at) }} · 总剩余 {{ remaining(installation.deadline_at) }}</small></div>
      <NTag v-if="installation.steps?.length" :bordered="false" size="small">{{ installation.steps?.filter(s => s.status === 'succeeded').length || 0 }} / {{ installation.steps?.length || 0 }} 步已完成</NTag>
    </div>
    <NAlert v-if="installation.error" type="error" :bordered="false">{{ installation.error }}</NAlert>
    <NAlert v-if="installation.cleanup_pending" type="warning" :bordered="false">正在重试停止 VM。确认停止前，新安装与电源操作已锁定。</NAlert>
    <ol v-if="installation.steps?.length" class="progress-list">
      <li v-for="(step, i) in installation.steps" :key="step.key" :class="`step-${step.status}`">
        <span class="progress-index">{{ step.status === 'succeeded' ? '✓' : String(i + 1).padStart(2, '0') }}</span>
        <div class="progress-content">
          <div class="progress-title"><strong>{{ step.title }}</strong><NTag size="small" :bordered="false" :type="step.status === 'succeeded' ? 'success' : step.status === 'running' ? 'info' : step.status === 'failed' ? 'error' : 'default'">{{ labels[step.status] || step.status }}</NTag></div>
          <p v-if="step.message">{{ step.message }}</p>
          <small>开始 {{ stamp(step.started_at) }} · 结束 {{ stamp(step.completed_at) }} · 耗时 {{ elapsed(step) }} · 时限 {{ duration(step.timeout_seconds) }}</small>
        </div>
      </li>
    </ol>
    <p v-else class="legacy-note">此历史任务未记录独立步骤时间，可在下方查看已有安装事件。新安装将记录完整步骤。</p>
  </section>
</template>
<style scoped>
.install-progress { min-width: 0; margin: 16px 0; text-wrap: pretty; }
.progress-heading, .progress-title { display: flex; justify-content: space-between; align-items: center; gap: 12px; }
.progress-heading { margin-bottom: 12px; flex-wrap: wrap; }
.progress-heading small { display: block; margin-top: 6px; color: var(--color-text-secondary); }
.progress-list { list-style: none; padding: 0; margin: 12px 0 0; border-top: 1px solid var(--color-border); }
.progress-list li { display: flex; gap: 14px; padding: 12px 4px; border-bottom: 1px solid var(--color-border-light); }
.progress-index { font: 13px/24px ui-monospace, monospace; color: var(--color-text-tertiary); width: 24px; flex-shrink: 0; }
.step-succeeded .progress-index { color: var(--color-success); }
.step-running .progress-index { color: var(--color-primary-hover); }
.step-running strong { color: var(--color-text-primary); }
.progress-list .step-running { background: var(--color-primary-light); }
.step-failed .progress-index { color: var(--color-error); }
.progress-content { flex: 1; min-width: 0; }
.progress-content p { margin: 5px 0; color: var(--color-text-secondary); font-size: 13px; overflow-wrap: anywhere; }
.progress-content small, .legacy-note { color: var(--color-text-secondary); font-size: 12px; line-height: 1.7; overflow-wrap: anywhere; }
.install-progress :deep(.n-alert) { margin: 8px 0; overflow-wrap: anywhere; }
@media(max-width: 540px) { .progress-title { align-items: start; } .progress-content small { display: block; } }
</style>
