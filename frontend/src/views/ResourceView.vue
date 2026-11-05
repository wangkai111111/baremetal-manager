<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import {
  NButton,
  NInput,
  NDataTable,
  NModal,
  NForm,
  NFormItem,
  NSelect,
  NInputNumber,
  NPopconfirm,
  NTag,
  useMessage,
  NUpload,
  useDialog
} from 'naive-ui'
import type { DataTableColumns, DataTableRowKey } from 'naive-ui'
import { h } from 'vue'
import type { Section } from '../catalog'
import { labels } from '../catalog'
import { errorText, resource, rows, type RecordData, api, exportToExcel, batchOps } from '../api'
import { useAuth } from '../stores/auth'

const props = defineProps<{ section: Section }>()
const route = useRoute()
const router = useRouter()
const message = useMessage()
const dialog = useDialog()
const auth = useAuth()

const items = ref<RecordData[]>([])
const total = ref(0)
const loading = ref(false)
const search = ref('')
const page = ref(1)
const pageSize = ref(20)

const editing = ref(false)
const saving = ref(false)
const scanning = ref(false)
const scanIds = ref<number[]>([])
const currentId = ref<string | number | null>(null)
const form = ref<RecordData>({})

// === 高级筛选 ===
const filterStatus = ref<string | null>(null)
const filterTag = ref<string[]>([])
const filterDatacenter = ref<string | null>(null)

// === 批量操作 ===
const checkedRowKeys = ref<DataTableRowKey[]>([])
const batchLoading = ref(false)

// === 机房选项列表 ===
const datacenterOptions = ref<{ label: string; value: string }[]>([])

// === 标签选项列表 ===
const tagOptions = ref<{ label: string; value: string }[]>([])

// === 状态选项列表 ===
const statusOptions = [
  { label: '在线', value: 'online' },
  { label: '离线', value: 'offline' },
  { label: '活跃', value: 'active' },
  { label: '就绪', value: 'ready' },
  { label: '故障', value: 'error' },
  { label: '维护中', value: 'maintenance' }
]

const canWrite = computed(() =>
  (props.section.api === '/projects'
    ? auth.user?.role === 'admin'
    : auth.can(props.section.permission.replace(':read', ':write'))) &&
  ['/projects', '/assets', '/networks', '/ip-allocations', '/tags', '/ip-pools'].includes(props.section.api)
)

// === 列定义（含排序） ===
const columns = computed<DataTableColumns<RecordData>>(() => {
  const fields = props.section.columns || [
    ...props.section.fields.map(f => f.key).filter(key => !key.includes('password')),
    'status',
    'created_at'
  ]
  const cols: DataTableColumns<RecordData> = fields.map(key => ({
    title: labels[key] || props.section.fields.find(f => f.key === key)?.label || key,
    key,
    minWidth: 125,
    sorter: (a: RecordData, b: RecordData) => {
      const va = a[key]
      const vb = b[key]
      if (va == null && vb == null) return 0
      if (va == null) return -1
      if (vb == null) return 1
      // 数值排序
      if (typeof va === 'number' && typeof vb === 'number') return va - vb
      // 字符串排序
      return String(va).localeCompare(String(vb))
    },
    render: row =>
      key === 'status' || key === 'severity'
        ? h(
            NTag,
            { size: 'small', bordered: false, type: tagType(row[key]) },
            { default: () => String(row[key] ?? '—') }
          )
        : String(row[key] ?? '—')
  }))
  if (
    props.section.api === '/assets' ||
    props.section.api === '/images' ||
    props.section.api === '/alerts'
  ) {
    cols.push({
      title: '操作',
      key: 'actions',
      width: 145,
      fixed: 'right',
      render: row =>
        h('div', { class: 'table-actions' }, [
          props.section.api === '/assets'
            ? h(
                NButton,
                { text: true, type: 'primary', onClick: () => router.push(`/assets/${row.id}`) },
                { default: () => '详情' }
              )
            : null,
          props.section.api === '/images'
            ? h(
                NButton,
                { text: true, type: 'primary', onClick: () => downloadImage(row) },
                { default: () => '下载' }
              )
            : null,
          props.section.api === '/alerts' && !row.resolved && auth.can('alerts:write')
            ? h(
                NButton,
                { text: true, type: 'primary', onClick: () => resolveAlert(row) },
                { default: () => '处理' }
              )
            : null,
          canWrite.value && ['/assets'].includes(props.section.api)
            ? h(
                NButton,
                { text: true, onClick: () => openEdit(row) },
                { default: () => '编辑' }
              )
            : null,
          canWrite.value && props.section.api === '/assets'
            ? h(
                NPopconfirm,
                { onPositiveClick: () => remove(row) },
                {
                  trigger: () =>
                    h(NButton, { text: true, type: 'error' }, { default: () => '删除' }),
                  default: () => '确认删除这条记录？'
                }
              )
            : null
        ])
    })
  }
  return cols
})

function tagType(value: unknown): 'success' | 'warning' | 'error' | 'info' | 'default' {
  const s = String(value || '').toLowerCase()
  return ['active', 'online', 'ready', 'success', 'completed', 'healthy'].includes(s)
    ? 'success'
    : ['failed', 'error', 'critical', 'offline'].includes(s)
      ? 'error'
      : ['pending', 'warning', 'running'].includes(s)
        ? 'warning'
        : 'default'
}

// === 后端分页加载 ===
async function load() {
  if (props.section.api !== '/projects' && !auth.projectId) {
    items.value = []
    total.value = 0
    return
  }
  loading.value = true
  try {
    // 构建查询参数
    const params: Record<string, unknown> = {
      page: page.value,
      page_size: pageSize.value
    }
    if (search.value) params.search = search.value
    if (filterStatus.value) params.status = filterStatus.value
    if (filterTag.value.length) params.tags = filterTag.value.join(',')
    if (filterDatacenter.value) params.datacenter_id = filterDatacenter.value

    const data = rows(await resource.list(props.section.api, params))
    const scoped = ['/installations', '/firmware'].includes(route.path)
      ? data.items.filter(
          item => item.kind === (route.path === '/installations' ? 'reinstall' : 'firmware')
        )
      : data.items
    items.value = scoped
    total.value = data.total
  } catch (e) {
    message.error(errorText(e))
  } finally {
    loading.value = false
  }
}

// === 加载筛选选项 ===
async function loadFilterOptions() {
  if (props.section.api !== '/assets') return
  try {
    // 加载机房列表
    const dcResult = rows(await resource.list('/datacenters'))
    datacenterOptions.value = dcResult.items.map(dc => ({
      label: String(dc.name || dc.id),
      value: String(dc.id)
    }))
  } catch {
    // 加载机房失败不影响主流程
  }
  try {
    // 加载标签列表
    const tagResult = rows(await resource.list('/tags'))
    tagOptions.value = tagResult.items.map(t => ({
      label: String(t.name || t.id),
      value: String(t.name || t.id)
    }))
  } catch {
    // 加载标签失败不影响主流程
  }
}

watch(
  [() => props.section, () => auth.projectId],
  () => {
    page.value = 1
    search.value = ''
    filterStatus.value = null
    filterTag.value = []
    filterDatacenter.value = null
    checkedRowKeys.value = []
    load()
    loadFilterOptions()
  },
  { immediate: true }
)

// === 监听筛选条件变化，重新加载 ===
watch([filterStatus, filterTag, filterDatacenter], () => {
  page.value = 1
  load()
})

function openEdit(row?: RecordData) {
  currentId.value = row?.id ?? null
  form.value = row ? { ...row } : {}
  editing.value = true
}

async function resolveAlert(row: RecordData) {
  try {
    await api.post(`/alerts/${row.id}/resolve`)
    message.success('告警已处理')
    load()
  } catch (e) {
    message.error(errorText(e))
  }
}

async function downloadImage(row: RecordData) {
  try {
    const response = await api.get(`/images/${row.id}/download`, { responseType: 'blob' })
    const url = URL.createObjectURL(response.data)
    const link = document.createElement('a')
    link.href = url
    link.download = String(row.name || `image-${row.id}`)
    link.click()
    URL.revokeObjectURL(url)
  } catch (e) {
    message.error(errorText(e))
  }
}

function confirmScan() {
  dialog.warning({
    title: '确认重新扫描',
    content: `将对 ${scanIds.value.length} 台已纳管资产执行 BMC 扫描。请确认目标资产。`,
    positiveText: '确认扫描',
    negativeText: '取消',
    onPositiveClick: scan
  })
}

// === IP 地址格式验证 ===
function validateIP(value: string): boolean {
  if (!value) return true
  const ipRegex = /^(\d{1,3}\.){3}\d{1,3}(\/\d{1,2})?$/
  return ipRegex.test(value)
}

// === CIDR 格式验证 ===
function validateCIDR(value: string): boolean {
  if (!value) return true
  const cidrRegex = /^(\d{1,3}\.){3}\d{1,3}\/\d{1,2}$/
  return cidrRegex.test(value)
}

async function save() {
  for (const field of props.section.fields) {
    if (field.required && !form.value[field.key]) {
      message.warning(`请填写${field.label}`)
      return
    }
    // 格式验证
    const val = String(form.value[field.key] || '')
    if (field.key === 'bmc_address' || field.key === 'ip_address') {
      if (!validateIP(val)) {
        message.warning(`${field.label} 格式不正确，请输入有效的 IP 地址`)
        return
      }
    }
    if (field.key === 'cidr') {
      if (!validateCIDR(val)) {
        message.warning(`${field.label} 格式不正确，请输入有效的 CIDR 格式（如 192.168.1.0/24）`)
        return
      }
    }
  }
  saving.value = true
  try {
    const payload = Object.fromEntries(
      props.section.fields
        .filter(f => form.value[f.key] !== undefined && form.value[f.key] !== '')
        .map(f => [f.key, form.value[f.key]])
    )
    if (currentId.value != null) {
      await resource.update(props.section.api, currentId.value, payload)
    } else {
      await resource.create(props.section.api, payload)
    }
    message.success('保存成功')
    editing.value = false
    load()
  } catch (e) {
    message.error(errorText(e))
  } finally {
    saving.value = false
  }
}

async function remove(row: RecordData) {
  try {
    await resource.remove(props.section.api, row.id!)
    message.success('已删除')
    load()
  } catch (e) {
    message.error(errorText(e))
  }
}

async function upload(file: File) {
  const data = new FormData()
  data.append('file', file)
  try {
    await api.post('/images', data)
    message.success('镜像上传成功')
    load()
  } catch (e) {
    message.error(errorText(e))
  }
}

async function scan() {
  if (!scanIds.value.length) {
    message.warning('请选择要扫描的资产')
    return
  }
  saving.value = true
  try {
    await api.post('/assets/scan', { asset_ids: scanIds.value })
    message.success('扫描任务已提交')
    scanning.value = false
    scanIds.value = []
    load()
  } catch (e) {
    message.error(errorText(e))
  } finally {
    saving.value = false
  }
}

// === Excel 导出 ===
async function handleExportExcel() {
  try {
    const fields = props.section.columns || props.section.fields.map(f => f.key)
    await exportToExcel(items.value, fields.filter(k => !k.includes('password') && !k.includes('secret')), props.section.title)
    message.success('导出成功')
  } catch (e) {
    message.error(errorText(e))
  }
}

// === 批量删除 ===
async function handleBatchDelete() {
  if (!checkedRowKeys.value.length) {
    message.warning('请先选择要操作的记录')
    return
  }
  dialog.warning({
    title: '确认批量删除',
    content: `将删除选中的 ${checkedRowKeys.value.length} 条记录，此不可撤销。`,
    positiveText: '确认删除',
    negativeText: '取消',
    onPositiveClick: async () => {
      batchLoading.value = true
      try {
        await batchOps.remove(props.section.api, checkedRowKeys.value)
        message.success(`已删除 ${checkedRowKeys.value.length} 条记录`)
        checkedRowKeys.value = []
        load()
      } catch (e) {
        message.error(errorText(e))
      } finally {
        batchLoading.value = false
      }
    }
  })
}

// === 批量电源操作（仅资产页） ===
async function handleBatchPower(action: string) {
  if (!checkedRowKeys.value.length) {
    message.warning('请先选择要操作的资产')
    return
  }
  const actionLabel = action === 'on' ? '开机' : action === 'off' ? '关机' : '重启'
  dialog.warning({
    title: `确认批量${actionLabel}`,
    content: `将对 ${checkedRowKeys.value.length} 台资产执行${actionLabel}操作。`,
    positiveText: '确认执行',
    negativeText: '取消',
    onPositiveClick: async () => {
      batchLoading.value = true
      try {
        await batchOps.power(checkedRowKeys.value.map(Number), action)
        message.success(`批量${actionLabel}操作已提交`)
        checkedRowKeys.value = []
        load()
      } catch (e) {
        message.error(errorText(e))
      } finally {
        batchLoading.value = false
      }
    }
  })
}
</script>

<template>
  <div class="page">
    <div class="page-head">
      <div>
        <div class="eyebrow dark">
          RESOURCE MANAGEMENT / {{ section.api.toUpperCase() }}
        </div>
        <h1>{{ section.title }}</h1>
        <p>{{ section.subtitle }}</p>
      </div>
      <div class="head-actions">
        <n-upload
          v-if="section.api === '/images' && auth.can('images:write')"
          :show-file-list="false"
          :custom-request="({ file }) => { if (file.file) upload(file.file as File) }"
        >
          <n-button type="primary">上传镜像</n-button>
        </n-upload>
        <n-button
          v-if="section.api === '/assets' && canWrite"
          @click="scanning = true"
        >
          扫描发现
        </n-button>
        <!-- Excel 导出按钮 -->
        <n-button @click="handleExportExcel" :disabled="!items.length">
          导出 Excel
        </n-button>
        <n-button
          v-if="canWrite && !section.readonly"
          type="primary"
          @click="openEdit()"
        >
          + 新建{{ section.title.replace('管理', '') }}
        </n-button>
      </div>
    </div>
    <div class="panel">
      <div class="panel-toolbar resource-toolbar">
        <div class="toolbar-heading">
          <div class="panel-title">
            资源列表 <span class="count">{{ total }}</span>
          </div>
          <p class="panel-caption">筛选、查看当前资源与操作记录</p>
        </div>
        <div class="toolbar-actions resource-actions">
          <n-input
            v-model:value="search"
            class="filter-search"
            placeholder="搜索资源..."
            clearable
            @keyup.enter="page = 1; load()"
            @clear="page = 1; load()"
          />
          <!-- 高级筛选 -->
          <div class="advanced-filters">
            <n-select
              v-model:value="filterStatus"
              placeholder="状态筛选"
              :options="statusOptions"
              clearable
            />
            <n-select
              v-if="section.api === '/assets'"
              v-model:value="filterTag"
              placeholder="标签筛选"
              :options="tagOptions"
              multiple
              clearable
            />
            <n-select
              v-if="section.api === '/assets'"
              v-model:value="filterDatacenter"
              placeholder="机房筛选"
              :options="datacenterOptions"
              clearable
            />
          </div>
          <div class="filter-buttons">
            <n-button @click="page = 1; load()">查询</n-button>
            <n-button quaternary @click="load">刷新</n-button>
          </div>
        </div>
      </div>

      <!-- 批量操作栏 -->
      <div v-if="checkedRowKeys.length" class="batch-action-bar" style="margin: 12px 12px 0;">
        <span class="batch-count">已选 {{ checkedRowKeys.length }} 项</span>
        <n-button size="small" type="error" :loading="batchLoading" @click="handleBatchDelete">
          批量删除
        </n-button>
        <n-button
          v-if="section.api === '/assets'"
          size="small"
          type="success"
          :loading="batchLoading"
          @click="handleBatchPower('on')"
        >
          批量开机
        </n-button>
        <n-button
          v-if="section.api === '/assets'"
          size="small"
          type="warning"
          :loading="batchLoading"
          @click="handleBatchPower('restart')"
        >
          批量重启
        </n-button>
        <n-button
          v-if="section.api === '/assets'"
          size="small"
          :loading="batchLoading"
          @click="handleBatchPower('off')"
        >
          批量关机
        </n-button>
        <n-button size="small" quaternary @click="checkedRowKeys = []">
          取消选择
        </n-button>
      </div>

      <n-data-table
        remote
        :columns="columns"
        :data="items"
        :loading="loading"
        :row-key="row => row.id"
        :scroll-x="Math.max(columns.length * 145, 600)"
        :row-selection="canWrite ? {
          checkedRowKeys,
          onUpdateCheckedRowKeys: (keys: DataTableRowKey[]) => { checkedRowKeys = keys }
        } : undefined"
        :pagination="{
          page,
          pageSize,
          itemCount: total,
          showSizePicker: true,
          pageSizes: [10, 20, 50, 100],
          onUpdatePage: (p: number) => { page = p; load() },
          onUpdatePageSize: (ps: number) => { pageSize = ps; page = 1; load() }
        }"
      />
      <div v-if="!loading && !items.length" class="empty-hint">
        当前没有数据，或尚未创建相关资源。
      </div>
    </div>
    <n-modal
      v-model:show="editing"
      preset="card"
      :title="currentId != null ? '编辑资源' : '新建资源'"
      style="width: min(520px, 94vw)"
    >
      <n-form label-placement="top">
        <div class="resource-form-grid">
          <n-form-item
            v-for="field in section.fields"
            :key="field.key"
            :label="field.label"
            :required="field.required"
            :class="{ 'form-item-wide': field.type === 'textarea' || field.key === 'description' }"
          >
            <n-select
              v-if="field.type === 'select'"
              v-model:value="form[field.key] as string"
              :options="field.options?.map(v => ({ label: v, value: v }))"
            />
            <n-input-number
              v-else-if="field.type === 'number'"
              v-model:value="form[field.key] as number"
              style="width: 100%"
            />
            <n-input
              v-else
              v-model:value="form[field.key] as string"
              :type="
                field.type === 'password'
                  ? 'password'
                  : field.type === 'textarea'
                    ? 'textarea'
                    : 'text'
              "
              :placeholder="`请输入${field.label}`"
            />
          </n-form-item>
        </div>
        <div class="modal-actions">
          <n-button @click="editing = false">取消</n-button>
          <n-button type="primary" :loading="saving" @click="save">保存</n-button>
        </div>
      </n-form>
    </n-modal>
    <n-modal
      v-model:show="scanning"
      preset="card"
      title="扫描发现资产"
      style="width: min(460px, 94vw)"
    >
      <n-form-item label="选择已纳管资产">
        <n-select
          v-model:value="scanIds"
          multiple
          filterable
          :options="
            items.map(item => ({
              label: String(item.name || item.id),
              value: Number(item.id)
            }))
          "
          placeholder="请选择要重新扫描的资产"
        />
      </n-form-item>
      <div class="modal-actions">
        <n-button @click="scanning = false">取消</n-button>
        <n-button type="primary" :loading="saving" @click="confirmScan">
          提交扫描
        </n-button>
      </div>
    </n-modal>
  </div>
</template>
