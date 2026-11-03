<script setup lang="ts">
// 装机方案管理页面 — 查看装机任务进度与状态
import { computed, ref, watch } from 'vue'
import {
  NButton,
  NDataTable,
  NModal,
  NForm,
  NFormItem,
  NSelect,
  NTag,
  NProgress,
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

// === 新建装机弹窗状态 ===
const creating = ref(false)
const saving = ref(false)
const newPlan = ref<RecordData>({})

// === 资产/镜像/模板选项 ===
const assetOptions = ref<{ label: string; value: string | number }[]>([])
const imageOptions = ref<{ label: string; value: string | number }[]>([])
const templateOptions = ref<{ label: string; value: string | number }[]>([])

// === 权限控制 ===
const canWrite = computed(() => auth.can('tasks:write'))

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
  { title: '创建时间', key: 'created_at', minWidth: 150 },
  ...(canWrite.value
    ? [
        {
          title: '操作',
          key: 'actions',
          width: 100,
          fixed: 'right' as const,
          render: (row: RecordData) =>
            h('div', { class: 'table-actions' }, [
              row.status === 'failed'
                ? h(
                    NButton,
                    { text: true, type: 'primary', onClick: () => retryTask(row) },
                    { default: () => '重试' }
                  )
                : null
            ])
        }
      ]
    : [])
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

// === 加载选项数据 ===
async function loadOptions() {
  try {
    const [assets, images, templates] = await Promise.allSettled([
      resource.list('/assets', { page: 1, page_size: 200 }),
      resource.list('/images', { page: 1, page_size: 200 }),
      resource.list('/kickstart-templates', { page: 1, page_size: 200 })
    ])
    if (assets.status === 'fulfilled') {
      const d = rows(assets.value)
      assetOptions.value = d.items.map(i => ({ label: String(i.name || i.id), value: i.id as string | number }))
    }
    if (images.status === 'fulfilled') {
      const d = rows(images.value)
      imageOptions.value = d.items.map(i => ({ label: String(i.name || i.id), value: i.id as string | number }))
    }
    if (templates.status === 'fulfilled') {
      const d = rows(templates.value)
      templateOptions.value = d.items.map(i => ({ label: String(i.name || i.id), value: i.id as string | number }))
    }
  } catch {
    // 加载选项失败，忽略
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

// === 打开新建装机弹窗 ===
function openCreate() {
  newPlan.value = {}
  loadOptions()
  creating.value = true
}

// === 重试失败的任务 ===
async function retryTask(row: RecordData) {
  try {
    await api.post(`/tasks/${row.id}/retry`)
    message.success('重试任务已提交')
    load()
  } catch (e) {
    message.error(errorText(e))
  }
}

// === 创建装机任务 ===
async function savePlan() {
  if (!newPlan.value.asset_id || !newPlan.value.image_id) {
    message.warning('请选择资产和镜像')
    return
  }
  saving.value = true
  try {
    await resource.create('/tasks', {
      kind: 'reinstall',
      asset_id: newPlan.value.asset_id,
      image_id: newPlan.value.image_id,
      template_id: newPlan.value.template_id || null
    })
    message.success('装机任务已创建')
    creating.value = false
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
        <div class="eyebrow dark">OPERATIONS / INSTALL PLANS</div>
        <h1>装机方案</h1>
        <p>管理装机任务进度与状态</p>
      </div>
      <div class="head-actions">
        <n-button v-if="canWrite" type="primary" @click="openCreate()">+ 新建装机</n-button>
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
    <!-- 新建装机弹窗 -->
    <n-modal
      v-model:show="creating"
      preset="card"
      title="新建装机任务"
      style="width: min(520px, 94vw)"
    >
      <n-form label-placement="top">
        <n-form-item label="选择资产" required>
          <n-select
            v-model:value="(newPlan.asset_id as string | number)"
            :options="assetOptions"
            placeholder="请选择目标资产"
            filterable
          />
        </n-form-item>
        <n-form-item label="选择镜像" required>
          <n-select
            v-model:value="(newPlan.image_id as string | number)"
            :options="imageOptions"
            placeholder="请选择系统镜像"
            filterable
          />
        </n-form-item>
        <n-form-item label="Kickstart 模板">
          <n-select
            v-model:value="(newPlan.template_id as string | number)"
            :options="templateOptions"
            placeholder="请选择装机模板（可选）"
            clearable
            filterable
          />
        </n-form-item>
        <div class="modal-actions">
          <n-button @click="creating = false">取消</n-button>
          <n-button type="primary" :loading="saving" @click="savePlan">创建任务</n-button>
        </div>
      </n-form>
    </n-modal>
  </div>
</template>
