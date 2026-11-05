<script setup lang="ts">
// 装机方案管理页面 — 查看装机任务进度与状态（重装执行暂不支持）
import { computed, ref, watch } from 'vue'
import {
  NButton,
  NDataTable,
  NTag,
  NProgress,
  useMessage
} from 'naive-ui'
import type { DataTableColumns } from 'naive-ui'
import { h } from 'vue'
import { errorText, resource, rows, type RecordData } from '../api'
import { useAuth } from '../stores/auth'

const message = useMessage()
const auth = useAuth()

// === 列表数据状态 ===
const items = ref<RecordData[]>([])
const total = ref(0)
const loading = ref(false)
const page = ref(1)
const pageSize = 20

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
  { title: '镜像', key: 'image_name', minWidth: 120, render: (row: RecordData) => String(row.image_name || row.image_id || '—') },
  { title: '模板', key: 'template_name', minWidth: 120, render: (row: RecordData) => String(row.template_name || row.template_id || '—') },
  {
    title: '状态',
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

// === 加载装机任务列表（仅显示 reinstall 类型）===
async function load() {
  loading.value = true
  try {
    const data = rows(await resource.list('/tasks', { page: page.value, page_size: pageSize, kind: 'reinstall' }))
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
</script>

<template>
  <div class="page">
    <div class="page-head">
      <div>
        <div class="eyebrow dark">OPERATIONS / INSTALL PLANS</div>
        <h1>装机方案</h1>
        <p>查看装机任务进度与状态；自动化重装执行暂未开放</p>
      </div>
    </div>
    <div class="panel">
      <div class="panel-toolbar">
        <div>
          <div class="panel-title">装机任务列表 <span class="count">{{ total }}</span></div>
          <p class="panel-caption">查看装机任务执行进度与状态</p>
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
        当前没有装机任务记录。
      </div>
    </div>
  </div>
</template>
