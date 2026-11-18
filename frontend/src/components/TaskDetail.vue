<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { NAlert, NButton, NModal, NSpin, NTag } from 'naive-ui'
import dayjs from 'dayjs'
import { api, errorText, unwrap } from '../api'
import { useAuth } from '../stores/auth'
import InstallationProgress, { type InstallationProgressData } from './InstallationProgress.vue'
const props = defineProps<{ taskId: number | null }>()
const emit = defineEmits<{ close: [] }>()
const auth = useAuth()
type Entry = { message: string; occurred_at: string; stage?: string; level?: string }
type Task = { id: number; kind: string; status: string; error?: string; asset?: { id: number; name: string }; created_at: string; completed_at?: string; installation?: InstallationProgressData; events?: Entry[]; recent_logs?: Entry[]; server_time?: string }
const task = ref<Task | null>(null)
const logs = ref<Entry[]>([])
const failure = ref('')
const loading = ref(false)
let timer: ReturnType<typeof setTimeout> | undefined
let generation = 0
let disposed = false
const statusLabels: Record<string, string> = { pending: '等待执行', running: '执行中', unknown: '需核实', retry_wait: '等待重试', succeeded: '成功', failed: '失败', timeout: '超时', cancelled: '已取消' }
const kindLabels: Record<string, string> = { install: 'PXE 系统安装', power_on: '开机', power_off: '关机', power_cycle: '重启', collect: '硬件采集' }
const entries = computed(() => task.value?.installation ? task.value.events || [] : logs.value)
const stamp = (s?: string) => s ? dayjs(s).format('YYYY-MM-DD HH:mm:ss') : '—'
async function refresh(initial = false) {
  clearTimeout(timer)
  const id = props.taskId; const version = ++generation
  if (!id) return
  if (initial) loading.value = true
  try {
    const result = unwrap<Task>(await api.get(`/tasks/${id}`))
    let logRows: Entry[] = []
    if (!result.installation) logRows = unwrap<{ items: Entry[] }>(await api.get(`/tasks/${id}/logs`, { params: { page_size: 200 } })).items
    if (disposed || version !== generation || id !== props.taskId) return
    task.value = result; logs.value = logRows; failure.value = ''
  } catch (e) { if (!disposed && version === generation) failure.value = errorText(e) }
  finally {
    if (!disposed && version === generation && props.taskId) {
      loading.value = false
      if (failure.value || !task.value || ['pending', 'running', 'retry_wait', 'unknown'].includes(task.value.status) || task.value.installation?.cleanup_pending) timer = setTimeout(() => refresh(), 3000)
    }
  }
}
watch([() => props.taskId, () => auth.projectId], () => { generation++; clearTimeout(timer); task.value = null; logs.value = []; failure.value = ''; if (props.taskId) refresh(true) }, { immediate: true })
onBeforeUnmount(() => { disposed = true; generation++; clearTimeout(timer) })
</script>
<template>
  <NModal :show="!!taskId" preset="card" :title="`任务 #${taskId} · ${kindLabels[task?.kind || ''] || '执行详情'}`" @update:show="!$event && emit('close')" style="width: min(880px, 96vw); max-height: 92vh; overflow: auto">
    <NSpin :show="loading">
      <NAlert v-if="failure" type="error" :bordered="false">{{ failure }}</NAlert>
      <template v-if="task">
        <div class="task-meta"><span>{{ task.asset?.name || `服务器 #${task.asset?.id || '—'}` }}</span><NTag :bordered="false" :type="task.status === 'succeeded' ? 'success' : ['failed', 'timeout'].includes(task.status) ? 'error' : 'info'">{{ statusLabels[task.status] || task.status }}</NTag><NButton text size="small" @click="refresh()">刷新进度</NButton></div>
        <p class="task-times">提交 {{ stamp(task.created_at) }} · 结束 {{ stamp(task.completed_at) }} · 执行中每 3 秒同步</p>
        <InstallationProgress v-if="task.installation" :installation="task.installation" :server-time="task.server_time" />
        <NAlert v-else-if="task.error" type="error" :bordered="false">{{ task.error }}</NAlert>
        <h3>执行事件</h3>
        <ol v-if="entries.length" class="task-events"><li v-for="(entry, i) in entries" :key="i"><time>{{ stamp(entry.occurred_at) }}</time><span>{{ entry.message }}</span></li></ol>
        <p v-else class="task-times">暂无执行事件，等待执行器回报。</p>
      </template>
      <div v-else-if="loading" style="min-height: 160px" />
      <NButton v-if="failure" @click="refresh(true)">重试加载</NButton>
    </NSpin>
  </NModal>
</template>
<style scoped>
.task-meta { display: flex; flex-wrap: wrap; gap: 12px; align-items: center; }
.task-meta > :last-child { margin-left: auto; }
.task-times { color: var(--color-text-secondary); font-size: 12px; line-height: 1.8; }
h3 { font-size: 14px; margin: 24px 0 12px; }
.task-events { list-style: none; padding: 0; max-height: 320px; overflow: auto; }
.task-events li { display: grid; grid-template-columns: 150px 1fr; gap: 14px; padding: 8px 0; font-size: 12px; line-height: 1.7; border-bottom: 1px solid var(--color-border-light); }
.task-events time { color: var(--color-text-tertiary); font-variant-numeric: tabular-nums; }
.task-events span { overflow-wrap: anywhere; }
@media(max-width: 540px) { .task-events li { grid-template-columns: 1fr; gap: 2px; } }
</style>
