<script setup lang="ts">
// 固件升级任务页面 — 管理固件版本与批量升级
import { computed, ref, watch } from 'vue'
import {
  NButton,
  NDataTable,
  NModal,
  NTag,
  NProgress,
  NUpload,
  NSelect,
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

// === 批量升级弹窗状态 ===
const showBatch = ref(false)
const showUpload = ref(false)
const saving = ref(false)
const selectedAssets = ref<(string | number)[]>([])
const selectedFirmware = ref<string | number | null>(null)

// === 资产和固件选项 ===
const assetOptions = ref<{ label: string; value: string | number }[]>([])
const firmwareOptions = ref<{ label: string; value: string | number }[]>([])

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

// === 打开批量升级弹窗 ===
async function openBatchUpgrade() {
  selectedAssets.value = []
  selectedFirmware.value = null
  try {
    const [assets, firmwares] = await Promise.allSettled([
      resource.list('/assets', { page: 1, page_size: 200 }),
      resource.list('/firmware', { page: 1, page_size: 200 })
    ])
    if (assets.status === 'fulfilled') {
      const d = rows(assets.value)
      assetOptions.value = d.items.map(i => ({ label: String(i.name || i.id), value: i.id as string | number }))
    }
    if (firmwares.status === 'fulfilled') {
      const d = rows(firmwares.value)
      firmwareOptions.value = d.items.map(i => ({ label: `${String(i.name || '')} v${String(i.version || '')}`, value: i.id as string | number }))
    }
  } catch {
    // 加载选项失败，忽略
  }
  showBatch.value = true
}

// === 上传固件文件 ===
async function uploadFirmware(file: File) {
  const data = new FormData()
  data.append('file', file)
  try {
    await api.post('/firmware', data)
    message.success('固件上传成功')
    showUpload.value = false
  } catch (e) {
    message.error(errorText(e))
  }
}

// === 执行批量升级 ===
async function batchUpgrade() {
  if (!selectedAssets.value.length || !selectedFirmware.value) {
    message.warning('请选择资产和固件文件')
    return
  }
  saving.value = true
  try {
    await api.post('/firmware/batch-upgrade', {
      asset_ids: selectedAssets.value,
      firmware_id: selectedFirmware.value
    })
    message.success('批量升级任务已提交')
    showBatch.value = false
    load()
  } catch (e) {
    message.error(errorText(e))
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <div class="page">
    <div class="page-head">
      <div>
        <div class="eyebrow dark">OPERATIONS / FIRMWARE UPGRADE</div>
        <h1>固件升级</h1>
        <p>管理固件版本与批量升级任务</p>
      </div>
      <div class="head-actions">
        <n-upload
          v-if="canWrite"
          :show-file-list="false"
          :custom-request="({ file }) => { if (file.file) uploadFirmware(file.file as File) }"
        >
          <n-button>上传固件</n-button>
        </n-upload>
        <n-button v-if="canWrite" type="primary" @click="openBatchUpgrade()">批量升级</n-button>
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
    <!-- 批量升级弹窗 -->
    <n-modal
      v-model:show="showBatch"
      preset="card"
      title="批量固件升级"
      style="width: min(520px, 94vw)"
    >
      <n-form label-placement="top">
        <n-form-item label="选择目标资产">
          <n-select
            v-model:value="selectedAssets"
            multiple
            filterable
            :options="assetOptions"
            placeholder="请选择要升级的资产"
          />
        </n-form-item>
        <n-form-item label="选择固件版本">
          <n-select
            v-model:value="selectedFirmware"
            filterable
            :options="firmwareOptions"
            placeholder="请选择目标固件版本"
          />
        </n-form-item>
        <div class="modal-actions">
          <n-button @click="showBatch = false">取消</n-button>
          <n-button type="primary" :loading="saving" @click="batchUpgrade">开始升级</n-button>
        </div>
      </n-form>
    </n-modal>
  </div>
</template>
