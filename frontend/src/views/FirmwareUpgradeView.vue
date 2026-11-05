<script setup lang="ts">
// 固件升级任务页面 — 上传固件与查看升级任务记录
import { computed, ref, watch } from 'vue'
import {
  NButton,
  NDataTable,
  NTag,
  NProgress,
  NUpload,
  useMessage
} from 'naive-ui'
import type { DataTableColumns } from 'naive-ui'
import { h } from 'vue'
import { errorText, resource, rows, type RecordData, api } from '../api'
import { useAuth } from '../stores/auth'

const message = useMessage()
const auth = useAuth()

// === 列表数据状态 ===
const items = ref<RecordData[]>([])
const total = ref(0)
const loading = ref(false)
const page = ref(1)
const pageSize = 20

// === 上传弹窗状态 ===
const showUpload = ref(false)

// === 权限控制 ===
const canWrite = computed(() => auth.can('firmware:write'))

// === 状态标签颜色映射 ===
function statusTagType(status: unknown): 'success' | 'warning' | 'error' | 'info' | 'default' {
  const s = String(status || '').toLowerCase()
  return s === 'completed' ? 'success'
    : s === 'running' ? 'info'
    : s === 'failed' ? 'error'
    : s === 'pending' ? 'warning'
    : 'default'
}

// === 表格列定义 ===
const columns = computed<DataTableColumns<RecordData>>(() => [
  { title: '资产名称', key: 'asset_name', minWidth: 140, render: (row: RecordData) => String(row.asset_name || row.asset_id || '—') },
  { title: '组件类型', key: 'component', minWidth: 120 },
  { title: '当前版本', key: 'current_version', minWidth: 120 },
  { title: '目标版本', key: 'target_version', minWidth: 120 },
  {
    title: '升级状态',
    key: 'status',
    minWidth: 100,
    render: (row: RecordData) =>
      h(
        NTag,
        { size: 'small', bordered: false, type: statusTagType(row.status) },
        { default: () => String(row.status || 'pending') }
      )
  },
  {
    title: '进度',
    key: 'progress',
    minWidth: 140,
    render: (row: RecordData) => {
      const pct = Number(row.progress || 0)
      const status = row.status === 'failed' ? 'error' : row.status === 'completed' ? 'success' : 'default'
      return h(NProgress, { type: 'line', percentage: pct, status, height: 6 })
    }
  },
  { title: '创建时间', key: 'created_at', minWidth: 150 }
])

// === 加载固件任务列表（仅显示 firmware 类型）===
async function load() {
  loading.value = true
  try {
    const data = rows(await resource.list('/tasks', { page: page.value, page_size: pageSize, kind: 'firmware' }))
    items.value = data.items
    total.value = data.total
  } catch (e) {
    message.error(errorText(e))
  } finally {
    loading.value = false
  }
}

watch(
  () => auth.projectId,
  () => {
    page.value = 1
    load()
  },
  { immediate: true }
)

// === 上传固件文件 ===
async function uploadFirmware(file: File) {
  const data = new FormData()
  data.append('file', file)
  try {
    await api.post('/firmware/upload', data)
    message.success('固件上传成功')
    showUpload.value = false
    load()
  } catch (e) {
    message.error(errorText(e))
  }
}
</script>

<template>
  <div class="page">
    <div class="page-head">
      <div>
        <div class="eyebrow dark">OPERATIONS / FIRMWARE UPGRADE</div>
        <h1>固件升级</h1>
        <p>上传固件文件并查看升级任务记录；批量升级执行通道尚未配置</p>
      </div>
      <div class="head-actions">
        <n-upload
          v-if="canWrite"
          :show-file-list="false"
          :custom-request="({ file }) => { if (file.file) uploadFirmware(file.file as File) }"
        >
          <n-button>上传固件</n-button>
        </n-upload>
        <n-button v-if="canWrite" type="primary" disabled>批量升级（暂不可用）</n-button>
      </div>
    </div>
    <div class="panel">
      <div class="panel-toolbar">
        <div>
          <div class="panel-title">固件任务列表 <span class="count">{{ total }}</span></div>
          <p class="panel-caption">查看固件升级进度与状态</p>
        </div>
        <div class="toolbar-actions">
          <n-button quaternary @click="load">刷新</n-button>
        </div>
      </div>
      <n-data-table
        remote
        :columns="columns"
        :data="items"
        :loading="loading"
        :row-key="(row: RecordData) => row.id ?? ''"
        :scroll-x="Math.max(columns.length * 145, 600)"
        :pagination="{
          page,
          pageSize,
          itemCount: total,
          showSizePicker: false,
          onUpdatePage: (p: number) => { page = p; load() }
        }"
      />
      <div v-if="!loading && !items.length" class="empty-hint">
        当前没有固件升级任务记录。
      </div>
    </div>
  </div>
</template>
