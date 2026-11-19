<script setup lang="ts">
// 服务器管理页 —— 裸机资产 fleet 视图
// 取代通用 ResourceView 的 /assets 页面：更贴合运维场景的筛选栏、批量操作栏与行内操作。
import { computed, h, onBeforeUnmount, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import {
  NAlert,
  NButton,
  NDataTable,
  NDropdown,
  NForm,
  NFormItem,
  NInput,
  NInputNumber,
  NModal,
  NPopconfirm,
  NSelect,
  NSpace,
  NTag,
  useDialog,
  useMessage
} from 'naive-ui'
import type { DataTableColumns, DataTableRowKey, DropdownOption, SelectOption } from 'naive-ui'
import { useElementSize } from '@vueuse/core'
import { CopyOutline, ChevronDownOutline, AddOutline, RefreshOutline, ServerOutline } from '@vicons/ionicons5'
import dayjs from 'dayjs'
import { api, batchOps, errorText, exportToExcel, resource, rows, unwrap, type RecordData } from '../api'
import { useAuth } from '../stores/auth'
import TestVMLab from '../components/TestVMLab.vue'
import InstallAccessSettings from '../components/InstallAccessSettings.vue'
import { installAccessError, installAccessPayload, newInstallAccess } from '../utils/installAccess'
import { flattenDepartments } from '../utils/departments'
import { isTestVM as isTestVm } from '../utils/testVM'
import { assetLabel } from '../utils/display'
import { copyToClipboard } from '../utils/clipboard'

const router = useRouter()
const route = useRoute()
const message = useMessage()
const dialog = useDialog()
const auth = useAuth()
const fleetElement = ref<HTMLElement | null>(null)
const { width: fleetWidth } = useElementSize(fleetElement)

// === 列表状态 ===
const items = ref<RecordData[]>([])
const total = ref(0)
const loading = ref(false)
const loadError = ref('')
const lastSyncedAt = ref<Date | null>(null)
const page = ref(1)
const pageSize = ref(20)
const checkedRowKeys = ref<DataTableRowKey[]>([])
const batchLoading = ref(false)
const powerLoading = ref<string | number | null>(null)
const reinstallShowing = ref(false)
const reinstallTarget = ref<RecordData | null>(null)
const installPlans = ref<RecordData[]>([])
const installPlan = ref<number | null>(null)
const reinstallConfirmName = ref('')
const reinstallLoading = ref(false)
const reinstallAccess = ref(newInstallAccess())
watch(reinstallShowing, () => { reinstallAccess.value = newInstallAccess() })
const testVmShowing = ref(false)

// === 筛选条件 ===
const filterStatus = ref<string | null>(null)
const searchField = ref('auto')
const searchFieldOptions = [
  { label: '自动识别', value: 'auto' },
  { label: '服务器名称', value: 'name' },
  { label: '管理 IP', value: 'management_ip' },
  { label: 'BMC IP', value: 'bmc_address' },
  { label: '序列号', value: 'serial_number' },
  { label: '机房', value: 'datacenter' },
  { label: '厂商', value: 'manufacturer' },
  { label: '型号', value: 'model' }
]
const filterTags = ref<string[]>([])
const keyword = ref(String(route.query.search || ''))

// === 权限派生 ===
const canWrite = computed(() => auth.can('assets:write'))
const canOperate = computed(() => auth.can('assets:operate'))
const canPower = computed(() => auth.can('bmc:operate'))
const canConsole = computed(() => auth.can('assets:operate'))
const consoleShowing = ref(false)
const consoleTarget = ref<RecordData | null>(null)

function openConsoleChooser(row: RecordData) {
  if (!canConsole.value) return
  consoleTarget.value = row
  consoleShowing.value = true
}

function openSolConsole() {
  if (consoleTarget.value?.id == null) return
  consoleShowing.value = false
  router.push(`/assets/${consoleTarget.value.id}/sol`)
}

function openBmcConsole() {
  if (!consoleTarget.value) return
  const url = bmcWebUrl(consoleTarget.value)
  if (!url) return message.warning('BMC 地址无效，无法打开管理界面')
  consoleShowing.value = false
  window.open(url, '_blank', 'noopener,noreferrer')
}
const selectable = computed(() => canWrite.value || canOperate.value || canPower.value)
const batchResults = ref<RecordData[]>([])
const resultsShowing = ref(false)
const resultColumns: DataTableColumns<RecordData> = [
  { title: '服务器', key: 'asset_label', minWidth: 260, render: row => String(row.asset_label || '服务器不可访问或已移除') },
  { title: '结果', key: 'status', render: row => ({ accepted: '任务已提交', failed: '失败' })[String(row.status)] || String(row.status) },
  { title: '任务进度', key: 'reference', render: row => row.operation_id ? h(NButton, {text: true, type: 'primary', onClick: () => router.push({path: '/tasks', query: {task: String(row.operation_id)}})}, {default: () => '查看步骤与日志'}) : '未创建任务' },
  { title: '说明', key: 'reason', render: row => String(row.reason || row.message || '已入任务队列，请在任务中心查看实际执行结果') }
]
let pendingBatch: { fingerprint: string; key: string } | null = null
async function submitBatchResult(kind: 'power', ids: number[], projectId: string, cycle = '') {
  if (batchLoading.value || auth.projectId !== projectId || !ids.length) return
  if (!canPower.value) return
  const fingerprint = `${projectId}:${kind}:${ids.join(',')}:${cycle}`
  if (pendingBatch?.fingerprint !== fingerprint) pendingBatch = { fingerprint, key: crypto.randomUUID() }
  const key = pendingBatch.key
  batchLoading.value = true
  try {
    const response = await batchOps.power(ids, cycle, key)
    if (auth.projectId !== projectId || disposed) return
    batchResults.value = unwrap<{ results: RecordData[] }>(response).results
    resultsShowing.value = true

    checkedRowKeys.value = []
    pendingBatch = null
    await load()
  } catch (e) {
    message.error(`${errorText(e)}；未收到可靠结果时再次提交将复用本次请求键，请勿更换选中项后重试。`)
  } finally { batchLoading.value = false }
}

// === 运行状态元数据（display_status → 标签文案 / 色板） ===
type StatusTone = 'running' | 'stopped' | 'fault' | 'maintenance'
type StatusMeta = { label: string; tone: StatusTone; tag: 'success' | 'warning' | 'error' | 'info' | 'default' }
// tone 只用于本页 chip 圆点配色，不沿用全局 .status-* 类（那些类自带背景与 ::before 圆点）
const statusMeta: Record<string, StatusMeta> = {
  running: { label: '运行中', tone: 'running', tag: 'success' },
  in_use: { label: '已分配', tone: 'running', tag: 'success' },
  stopped: { label: '已关机', tone: 'stopped', tag: 'default' },
  offline: { label: '空闲/离线', tone: 'stopped', tag: 'default' },
  error: { label: '故障', tone: 'fault', tag: 'error' },
  maintenance: { label: '维护中', tone: 'maintenance', tag: 'warning' },
  retired: { label: '已退役', tone: 'stopped', tag: 'default' },
  pending_management: { label: '待纳管', tone: 'stopped', tag: 'default' },
  managing: { label: '纳管中', tone: 'maintenance', tag: 'info' },
  collecting: { label: '采集中', tone: 'maintenance', tag: 'info' },
  starting: { label: '开机中', tone: 'maintenance', tag: 'info' },
  stopping: { label: '关机中', tone: 'maintenance', tag: 'info' },
  restarting: { label: '重启中', tone: 'maintenance', tag: 'info' },
  reinstalling: { label: '重装中', tone: 'maintenance', tag: 'info' },
  upgrading: { label: '固件升级中', tone: 'maintenance', tag: 'info' },
  queued: { label: '等待执行', tone: 'maintenance', tag: 'info' },
  unknown: { label: '状态未知', tone: 'stopped', tag: 'default' }
}
const statusOf = (row: RecordData) => String(row.display_status || row.status || 'unknown')
const metaOf = (row: RecordData): StatusMeta => statusMeta[statusOf(row)] || statusMeta.unknown

// === 筛选项数据源 ===
const datacenterOptions = ref<SelectOption[]>([])
const tagOptions = ref<SelectOption[]>([])
const departments = ref<RecordData[]>([])
const departmentOptions = computed(() => departments.value.map(dept => ({ label: String(dept.name), value: Number(dept.id), disabled: dept.can_manage !== true })))
const departmentRequired = computed(() => !auth.user?.is_platform_admin && !auth.user?.can_manage_organization)
const departmentLoadError = ref('')
let filterSequence = 0

function datacenterName(value: unknown) {
  if (value == null || value === '') return '未分配机房'
  const hit = datacenterOptions.value.find(option => option.value === Number(value))
  return hit ? String(hit.label) : '机房信息不可用'
}

function projectName(row: RecordData): string {
  const project = auth.projects.find(item => String(item.id) === String(row.project_id))
  return String(row.project_name || project?.name || (row.project_id == null ? '未分配项目' : '项目信息不可用'))
}

function departmentName(row: RecordData): string {
  const id = row.department_id
  const hit = departments.value.find(dept => String(dept.id) === String(id))
  return String(row.department_name || hit?.name || (id == null ? '未分配部门' : '部门信息不可用'))
}

const healthOf = (row: RecordData): { label: string; tag: 'success' | 'warning' | 'error' | 'default' } => {
  switch (String(row.monitor_health || 'unknown').toLowerCase()) {
    case 'ok': return { label: '健康', tag: 'success' }
    case 'warning': return { label: '警告', tag: 'warning' }
    case 'critical': return { label: '严重', tag: 'error' }
    default: return { label: '未知', tag: 'default' }
  }
}

// === 硬件规格：列表行仅有型号字段，规格明细来自逐台采集快照 ===
type SpecInfo = { text: string; detail: string }
const specCache = ref<Record<string, SpecInfo>>({})
const specPending = ref<Record<string, boolean>>({})

function gigantaLabel(bytes: number) {
  if (bytes >= 1e12) {
    const tera = bytes / 1e12
    return `${tera >= 10 ? Math.round(tera) : Number(tera.toFixed(1))}T`
  }
  if (bytes >= 1e9) return `${Math.round(bytes / 1e9)}G`
  return ''
}

/** 由采集快照拼装 “8C 32G 2×4T SSD” 形式的规格串 */
function composeSpec(dto: RecordData): SpecInfo {
  const components = (dto.components || {}) as Record<string, RecordData[]>
  const processors = Array.isArray(components.processors) ? components.processors : []
  const drives = (Array.isArray(components.storage) ? components.storage : []).filter(
    drive => String(drive.kind || '').toLowerCase() === 'drive'
  )
  const parts: string[] = []
  const detail: string[] = []

  const coreSum = processors.reduce((sum, cpu) => sum + Number(cpu.cores || 0), 0)
  const sockets = processors.length || Number(dto.cpu_count || 0)
  if (coreSum > 0) parts.push(`${coreSum}C`)
  else if (sockets > 0) parts.push(`${sockets}路`)
  const cpuModel = String(processors[0]?.model || dto.cpu_model || '')
  if (cpuModel) detail.push(cpuModel + (sockets > 1 ? ` ×${sockets}` : ''))

  const memoryGiB = Number(dto.memory_gib || 0)
  if (memoryGiB > 0) {
    parts.push(memoryGiB >= 1024 ? `${Math.round(memoryGiB / 1024)}T` : `${Math.round(memoryGiB)}G`)
    detail.push(`${memoryGiB >= 1024 ? `${Math.round(memoryGiB / 1024)} TiB 内存` : `${Math.round(memoryGiB)} GiB 内存`}`)
  }

  if (drives.length) {
    const sizes = drives.map(
      drive => Number(drive.capacity_bytes || 0) || Number(drive.capacity_mib || 0) * 1024 * 1024
    )
    const media = String(drives[0]?.media_type || '').toUpperCase()
    const protocol = String(drives[0]?.protocol || '').toUpperCase()
    const uniform = sizes.every(size => size > 0 && size === sizes[0])
    if (uniform && sizes[0] > 0) {
      parts.push(`${drives.length}×${gigantaLabel(sizes[0])}${media ? ` ${media}` : ''}`)
      detail.push(`${drives.length} × ${gigantaLabel(sizes[0])} ${media || protocol || '磁盘'}`)
    } else {
      const sum = sizes.reduce((acc, size) => acc + size, 0)
      parts.push(`${drives.length}盘 ${sum > 0 ? gigantaLabel(sum) : ''}`.trim())
      detail.push(`${drives.length} 块磁盘，合计 ${gigantaLabel(sum) || '容量未知'}`)
    }
  }

  const model = [String(dto.manufacturer || ''), String(dto.model || '')].filter(Boolean).join(' ')
  return { text: parts.join(' ') || model, detail: detail.join(' · ') || model }
}

function specOf(row: RecordData): SpecInfo {
  if (isTestVm(row)) return { text: '2 vCPU · 8 GB · 40 GB', detail: 'QEMU PXE 测试 VM：2 vCPU、8 GB 内存、40 GB 虚拟磁盘' }
  const cached = specCache.value[String(row.id)]
  if (cached && (cached.text || cached.detail)) return cached
  const model = [String(row.manufacturer || ''), String(row.model || '')].filter(Boolean).join(' ')
  return cached ? { text: model || '—', detail: '' } : { text: model, detail: '' }
}

/**
 * 逐台拉取采集快照拼装规格：并发 4、单页最多富集 40 台，
 * 未命中或失败的直接回落型号，避免大页量触发过多快照请求。
 */
const SPEC_ENRICH_LIMIT = 40
async function enrichSpecs(list: RecordData[]) {
  const queue = list
    .filter(row => !isTestVm(row))
    .map(row => String(row.id))
    .filter(id => id && specCache.value[id] === undefined)
    .slice(0, SPEC_ENRICH_LIMIT)
  if (!queue.length) return
  const pending = { ...specPending.value }
  queue.forEach(id => { pending[id] = true })
  specPending.value = pending
  const pump = async () => {
    while (queue.length) {
      const id = queue.shift()!
      try {
        specCache.value[id] = composeSpec(unwrap<RecordData>(await api.get(`/assets/${id}/collection`)))
      } catch {
        specCache.value[id] = { text: '', detail: '' }
      } finally {
        const next = { ...specPending.value }
        delete next[id]
        specPending.value = next
      }
    }
  }
  await Promise.allSettled(Array.from({ length: Math.min(4, queue.length) }, pump))
}

// === 地址与电源 ===
function bmcWebUrl(row: RecordData): string | null {
  const address = String(row.bmc_address || '').trim()
  if (!address || /[\s/@?#\\]/.test(address)) return null
  const host = address.includes(':') && !address.startsWith('[') ? `[${address}]` : address
  const port = Number(row.bmc_port || (row.bmc_protocol === 'ipmi' ? 623 : 443))
  if (!Number.isInteger(port) || port < 1 || port > 65535) return null
  try {
    return new URL(`https://${host}:${port}/`).href
  } catch {
    return null
  }
}

function powerIntent(row: RecordData): { action: 'on' | 'off' | 'restart'; label: string } {
  const power = String(row.power_state || '').trim().toLowerCase()
  const status = statusOf(row)
  if (power === 'on' || ['running', 'in_use'].includes(status)) return { action: 'off', label: '关机' }
  if (power === 'off' || status === 'stopped') return { action: 'on', label: '开机' }
  return { action: 'restart', label: '重启' }
}

const formatTime = (value: unknown) => {
  if (value == null || value === '') return '—'
  const parsed = dayjs(String(value))
  return parsed.isValid() ? parsed.format('YYYY-MM-DD HH:mm') : '—'
}

function idempotencyKey(row: RecordData, action: string) {
  return `${row.id}-${action}-${Date.now().toString(36)}`
}

async function copyText(value: string) {
  if (!value) return
  try {
    await copyToClipboard(value)
    message.success(`已复制 ${value}`)
  } catch {
    message.error('复制失败，请选中地址后按 Ctrl/Cmd+C 复制')
  }
}

// === 列定义 ===
const columns = computed<DataTableColumns<RecordData>>(() => {
  const cols: DataTableColumns<RecordData> = []
  if (selectable.value) cols.push({ type: 'selection', width: 44, align: 'center', disabled: row => batchLoading.value || isTestVm(row) })

  cols.push({
    title: '服务器',
    key: 'name',
    width: 160,
    render: row =>
      h('div', { class: 'cell-server' }, [
        h(ServerOutline, { class: 'server-rack', 'aria-hidden': true }),
        h('div', { class: 'server-id' }, [
          h(
            'a',
            {
              class: 'server-name',
              href: `/assets/${row.id}`,
              onClick: (event: MouseEvent) => {
                event.preventDefault()
                router.push(`/assets/${row.id}`)
              }
            },
            String(row.name || row.management_ip || row.bmc_address || '服务器信息不可用')
          )
        ])
      ])
  })

  cols.push({
    title: '序列号',
    key: 'serial_number',
    width: 166,
    render: row => h('span', { class: 'server-sn' }, String(row.serial_number || '未登记'))
  })

  cols.push({
    title: 'IP 地址',
    key: 'ip_address',
    width: 220,
    render: row => {
      const service = String(row.ip_address || row.management_ip || '').trim()
      const bmc = isTestVm(row) ? '' : String(row.bmc_address || '').trim()
      const addressLine = (label: string, value: string, dim = false) => h('div', { class: ['ip-line', { dim, empty: !value }] }, [
        h('i', {}, label),
        h('code', {}, value || (dim ? '—' : '未分配')),
        value ? h('button', {
          class: 'ip-copy-button', type: 'button', title: `复制${label} IP`,
          'aria-label': `复制${label === 'BMC' ? ' BMC' : label} IP ${value}`,
          onClick: () => copyText(value)
        }, [h(CopyOutline, { class: 'copy-icon', 'aria-hidden': true })]) : null
      ])
      return h('div', { class: 'cell-ip' }, [
        addressLine('管理', service),
        addressLine('BMC', bmc, true)
      ])
    }
  })

  cols.push({
    title: '配置规格',
    key: 'spec',
    width: 172,
    render: row => {
      const info = specOf(row)
      if (specPending.value[String(row.id)]) return h('span', { class: 'spec-skeleton' })
      return h(
        'span',
        { class: 'spec-code', title: info.detail || '等待硬件采集后展示完整规格' },
        info.text || '—'
      )
    }
  })

  cols.push({
    title: '运行状态',
    key: 'status',
    width: 108,
    render: row => h(NTag, { size: 'small', bordered: false, type: metaOf(row).tag }, { default: () => metaOf(row).label })
  })

  cols.push({
    title: '设备健康',
    key: 'monitor_health',
    width: 108,
    render: row => h(NTag, { size: 'small', bordered: false, type: healthOf(row).tag }, { default: () => healthOf(row).label })
  }, {
    title: '项目/部门',
    key: 'owner',
    width: 160,
    render: row => h('div', { class: 'cell-owner' }, [
      h('span', { title: `项目：${projectName(row)}` }, projectName(row)),
      h('small', { title: `部门：${departmentName(row)}` }, departmentName(row))
    ])
  }, {
    title: '机房',
    key: 'datacenter_id',
    width: 130,
    render: row => h('span', { class: 'cell-muted' }, datacenterName(row.datacenter_id))
  })

  cols.push({
    title: '创建时间',
    key: 'created_at',
    width: 160,
    render: row => h('span', { class: 'cell-muted mono', style: { whiteSpace: 'nowrap' } }, formatTime(row.created_at))
  })

  cols.push({
    title: '操作',
    key: 'actions',
    width: 204,
    fixed: fleetWidth.value >= 760 ? 'right' : undefined,
    render: row => {
      const intent = powerIntent(row)
      if (isTestVm(row)) return h('div', { class: 'table-actions row-actions' }, [
        h(NButton, { text: true, type: 'primary', onClick: openTestVm }, { default: () => 'PXE 实验室' }),
        h(NButton, { text: true, onClick: () => router.push(`/assets/${row.id}`) }, { default: () => '详情' })
      ])
      const menu = rowMenuOptions(row)
      return h('div', { class: 'table-actions row-actions' }, [
        canPower.value
          ? h(
              NButton,
              {
                text: true,
                type: intent.action === 'off' ? 'warning' : 'success',
                disabled: powerLoading.value === row.id,
                loading: powerLoading.value === row.id,
                onClick: () => confirmSinglePower(row, intent.action, intent.label)
              },
              { default: () => intent.label }
            )
          : null,
        canPower.value && intent.action !== 'restart'
          ? h(NButton, { text: true, type: 'warning', disabled: powerLoading.value === row.id, loading: powerLoading.value === row.id, onClick: () => confirmSinglePower(row, 'restart', '重启') }, { default: () => '重启' })
          : null,
        canConsole.value
          ? h(NButton, { text: true, onClick: () => openConsoleChooser(row) }, { default: () => '控制台' })
          : null,
        h(
          NDropdown,
          { trigger: 'click', options: menu, onSelect: (key: string) => handleRowMenu(key, row) },
          { default: () => h(NButton, { text: true, 'aria-label': '更多操作' }, { default: () => ['更多', h(ChevronDownOutline, { class: 'menu-chevron', 'aria-hidden': true })] }) }
        )
      ])
    }
  })
  return cols
})

const scrollX = computed(() => columns.value.reduce((width, column) => width + Number(column.width || 0), 0))

// 行主键：资产 id 为后端必填数值主键
const rowKey = (row: RecordData) => row.id as string | number

function rowMenuOptions(row: RecordData): DropdownOption[] {
  const options: DropdownOption[] = [{ label: '查看详情', key: 'detail' }]
  if (canOperate.value) options.push({ label: '重装系统', key: 'reinstall' })
  if (canOperate.value) options.push({ label: '采集硬件信息', key: 'collect' })
  options.push({ label: '打开 BMC 界面', key: 'bmc', disabled: !bmcWebUrl(row) })
  if (canWrite.value && row.can_manage === true) options.push({ type: 'divider', key: 'divider' }, { label: '编辑', key: 'edit' }, { label: '删除', key: 'delete' })
  return options
}

// === 数据加载（后端分页 + 筛选） ===
let refreshTimer: ReturnType<typeof setTimeout> | undefined
let loadSequence = 0
let disposed = false

async function load(silent = false) {
  const sequence = ++loadSequence
  clearTimeout(refreshTimer)
  if (!auth.projectId) {
    items.value = []
    total.value = 0
    loading.value = false
    return
  }
  if (!silent) {
    loading.value = true
    items.value = []
    total.value = 0
  }
  loadError.value = ''
  try {
    const params: Record<string, unknown> = { page: page.value, page_size: pageSize.value }
    if (filterStatus.value) params.status = filterStatus.value
    if (filterTags.value.length) params.tags = filterTags.value.join(',')
    if (keyword.value.trim()) {
      params.search_field = searchField.value
      params.search = keyword.value.trim()
    }
    const data = rows(await resource.list('/assets', params))
    if (sequence !== loadSequence || disposed) return
    items.value = data.items
    total.value = data.total
    lastSyncedAt.value = new Date()
    void enrichSpecs(data.items)
  } catch (e) {
    if (sequence !== loadSequence || disposed) return
    loadError.value = errorText(e)
    if (!silent) message.error(loadError.value)
  } finally {
    if (sequence === loadSequence && !disposed) {
      loading.value = false
      // 电源/采集等状态由后台任务推进，静默刷新保持列表鲜活
      refreshTimer = setTimeout(() => load(true), 15000)
    }
  }
}

async function loadFilterOptions() {
  if (!auth.projectId) return
  const sequence = ++filterSequence
  departmentLoadError.value = ''
  try {
    const result = rows(await resource.list('/datacenters', { page_size: 200 }))
    if (sequence !== filterSequence) return
    datacenterOptions.value = result.items.map(dc => ({ label: String(dc.name || '机房名称未填写'), value: Number(dc.id) }))
  } catch (e) {
    if (sequence !== filterSequence) return
    message.warning(`机房选项加载失败：${errorText(e)}`)
  }
  try {
    const result = rows(await resource.list('/tags', { page_size: 200 }))
    if (sequence !== filterSequence) return
    tagOptions.value = result.items.map(tag => ({ label: String(tag.name || '标签名称未填写'), value: String(tag.name || '标签名称未填写') }))
  } catch {
    if (sequence !== filterSequence) return
    // 标签加载失败不阻塞主流程
  }
  if (auth.can('departments:read')) {
    try {
      const result = rows(await resource.list('/departments'))
      if (sequence !== filterSequence) return
      const managed = new Set<number>()
      const visit = (nodes: RecordData[]) => nodes.forEach(d => {if (d.can_manage === true) managed.add(Number(d.id));if (Array.isArray(d.children)) visit(d.children as RecordData[])})
      visit(result.items)
      departments.value = flattenDepartments(result.items).map(d => ({...d, can_manage:managed.has(d.id)}))
    } catch (e) {
      if (sequence !== filterSequence) return
      departments.value = []
      departmentLoadError.value = `部门加载失败：${errorText(e)}`
    }
  }
}

const lastSyncedLabel = computed(() => {
  if (loadError.value) return '列表同步失败'
  if (!lastSyncedAt.value) return '正在同步…'
  return `已于 ${dayjs(lastSyncedAt.value).format('HH:mm:ss')} 同步`
})

function applyFilter() {
  page.value = 1
  checkedRowKeys.value = []
  load()
}

function resetFilters() {
  filterStatus.value = null
  searchField.value = 'auto'
  filterTags.value = []
  keyword.value = ''
  applyFilter()
}

// === 本页状态统计（点击快捷筛选） ===
const fleetChips = computed(() => {
  const order = ['running', 'stopped', 'error', 'maintenance', 'offline']
  return order.map(key => ({
    key,
    label: statusMeta[key].label,
    cls: 'dot-' + statusMeta[key].tone,
    count: items.value.filter(row => statusOf(row) === key).length
  }))
})

// === 批量操作 ===
const batchActions = [
  { key: 'on', label: '开机', action: 'on' },
  { key: 'off', label: '关机', action: 'off' },
  { key: 'restart', label: '重启', action: 'restart' },
  { key: 'scan', label: '扫描', action: '' },
  { key: 'delete', label: '删除', action: '' }
]
const listToolOptions = computed<DropdownOption[]>(() => [
  ...(canWrite.value ? [{ key: 'register', label: '批量登记服务器' }] : []),
  ...(auth.user?.is_platform_admin ? [{ key: 'test-vm', label: '创建测试 VM' }] : [])
])

function handleListTool(key: string) {
  if (key === 'register') openBatchAdding()
  if (key === 'test-vm') openTestVm()
}

function selectedIds() {
  return checkedRowKeys.value.map(Number).filter(id => Number.isFinite(id))
}

async function runBatch(action: { key: string; label: string; action: string }) {
  if (batchLoading.value) return
  const ids = selectedIds()
  const projectId = auth.projectId
  if (ids.length > 500) { message.warning('每次最多操作500台'); return }
  if (!ids.length) {
    message.warning('请先勾选服务器')
    return
  }
  if (action.key === 'delete') {
    // 顶部按钮组内的删除走对话框确认（批量条上的删除使用 NPopconfirm）
    if (!canWrite.value) return message.error('缺少 assets:write 权限')
    dialog.error({
      title: '确认批量删除',
      content: `将删除 ${ids.length} 台服务器记录，BMC 凭据与采集历史不可恢复。`,
      positiveText: '确认删除',
      negativeText: '取消',
      onPositiveClick: () => void confirmBatchDelete()
    })
    return
  }
  if (action.key === 'scan') {
    if (!canOperate.value) return message.error('缺少 assets:operate 权限')
    return submit(() => batchOps.scan(ids), `已为 ${ids.length} 台服务器提交硬件采集`)
  }
  if (!canPower.value) return message.error('缺少 bmc:operate 权限')
  dialog.warning({
    title: `确认批量${action.label}`,
    content: `将对 ${ids.length} 台服务器执行${action.label}操作，可能影响在跑业务，请确认。`,
    positiveText: '确认执行',
    negativeText: '取消',
    onPositiveClick: () => submitBatchResult('power', ids, projectId, action.action)
  })
}

async function submit(task: () => Promise<unknown>, success: string) {
  batchLoading.value = true
  try {
    await task()
    message.success(success)
    checkedRowKeys.value = []
    await load()
  } catch (e) {
    message.error(errorText(e))
  } finally {
    batchLoading.value = false
  }
}

async function confirmBatchDelete() {
  const ids = selectedIds()
  if (!ids.length || !canWrite.value || batchLoading.value) return
  batchLoading.value = true
  try {
    await batchOps.remove('/assets', ids)
    message.success(`已删除 ${ids.length} 台服务器记录`)
    checkedRowKeys.value = []
    await load()
  } catch (e) {
    message.error(errorText(e))
  } finally {
    batchLoading.value = false
  }
}

// === 行内操作 ===
function confirmSinglePower(row: RecordData, action: 'on' | 'off' | 'restart', label: string) {
  if (!canPower.value || powerLoading.value != null) return
  dialog.warning({
    title: `确认${label}`,
    content: `将对 ${assetLabel(row)} 执行${label}操作，该操作可能影响业务。`,
    positiveText: '确认执行',
    negativeText: '取消',
    onPositiveClick: async () => {
      powerLoading.value = row.id ?? null
      try {
        await api.post(`/assets/${encodeURIComponent(String(row.id))}/actions`, {
          action,
          idempotency_key: idempotencyKey(row, action)
        })
        message.success(`${label}指令已提交`)
        await load(true)
      } catch (e) {
        message.error(errorText(e))
      } finally {
        powerLoading.value = null
      }
    }
  })
}

async function collectAsset(row: RecordData) {
  if (!canOperate.value) return
  try {
    await api.post(`/assets/${encodeURIComponent(String(row.id))}/collect`)
    delete specCache.value[String(row.id)]
    message.success('硬件采集任务已提交，稍后刷新查看规格')
    await load(true)
  } catch (e) {
    message.error(errorText(e))
  }
}

async function startReinstall(row: RecordData) {
  reinstallTarget.value = row
  installPlan.value = null
  reinstallConfirmName.value = ''
  reinstallShowing.value = true
  try {
    const result = rows(await resource.list('/install-plans', { page_size: 200 }))
    installPlans.value = result.items
  } catch (e) {
    installPlans.value = []
    message.error(`装机方案加载失败：${errorText(e)}`)
  }
}

async function submitReinstall() {
  const target = reinstallTarget.value
  if (!target || installPlan.value == null || reinstallConfirmName.value !== String(target.name || target.management_ip || target.bmc_address || '')) return
  const accessError = installAccessError(reinstallAccess.value)
  if (accessError) { message.error(accessError); return }
  reinstallLoading.value = true
  try {
    await api.post(`/assets/${encodeURIComponent(String(target.id))}/install`, { install_plan_id: Number(installPlan.value), confirmed: true, access: installAccessPayload(reinstallAccess.value) })
    message.success(`${target.name || '服务器'}的装机任务已提交，请到任务中心查看步骤与日志`)
    reinstallShowing.value = false
    await load(true)
  } catch (e) { message.error(errorText(e)) }
  finally { reinstallLoading.value = false }
}

function openTestVm() { testVmShowing.value = true }


function removeAsset(row: RecordData) {
  if (!canWrite.value) return
  dialog.warning({
    title: '确认删除服务器',
    content: `确认删除 ${assetLabel(row)}？删除后 BMC 凭据与采集历史将不可恢复。`,
    positiveText: '确认删除',
    negativeText: '取消',
    onPositiveClick: async () => {
      try {
        await resource.remove('/assets', row.id!)
        message.success('已删除')
        await load()
      } catch (e) {
        message.error(errorText(e))
      }
    }
  })
}

function handleRowMenu(key: string, row: RecordData) {
  if (key === 'reinstall') {
    startReinstall(row)
    return
  }
  if (key === 'detail') {
    router.push(`/assets/${row.id}`)
    return
  }
  if (key === 'restart') {
    confirmSinglePower(row, 'restart', '重启')
    return
  }
  if (key === 'collect') {
    void collectAsset(row)
    return
  }
  if (key === 'bmc') {
    const url = bmcWebUrl(row)
    if (url) window.open(url, '_blank', 'noopener,noreferrer')
    else message.warning('BMC 地址无效，无法打开管理界面')
    return
  }
  if (key === 'edit') openEdit(row)
  if (key === 'delete') removeAsset(row)
}

// === 新增 / 编辑 ===
// 表单单独定型，保证双向绑定与控件值类型一致（记录行本身仍是松散的 RecordData）
type AssetForm = {
  name?: string
  bmc_address?: string
  bmc_user?: string
  bmc_secret?: string
  bmc_protocol?: string
  bmc_port?: number | null
  management_ip?: string
  serial_number?: string
  model?: string
  datacenter_id?: number | null
  department_id?: number | null
  datacenter_name?: string
  department_name?: string
}
const editing = ref(false)
const saving = ref(false)
const currentId = ref<string | number | null>(null)
const form = ref<AssetForm>({})
const editDatacenterOptions = computed(() => {
  const value = form.value.datacenter_id
  return value == null || datacenterOptions.value.some(option => option.value === value) ? datacenterOptions.value
    : [...datacenterOptions.value, {label:form.value.datacenter_name || '当前机房（待加载或已不可用）', value, disabled:true}]
})
const editDepartmentOptions = computed(() => {
  const value = form.value.department_id
  return value == null || departmentOptions.value.some(option => option.value === value) ? departmentOptions.value
    : [...departmentOptions.value, {label:form.value.department_name || '当前部门（待加载或已不可用）', value, disabled:true}]
})
const protocols = [
  { label: 'Redfish', value: 'redfish' },
  { label: 'IPMI', value: 'ipmi' }
]

const toText = (value: unknown) => (value == null ? '' : String(value))

function openEdit(row?: RecordData) {
  if (!canWrite.value || (row && row.can_manage !== true)) return
  currentId.value = row?.id ?? null
  form.value = row
    ? {
        name: toText(row.name),
        bmc_address: toText(row.bmc_address),
        bmc_user: toText(row.bmc_user),
        // 出于凭据保护不回填现有密码，留空即保留
        bmc_secret: '',
        bmc_protocol: row.bmc_protocol === 'ipmi' ? 'ipmi' : 'redfish',
        bmc_port: row.bmc_port == null ? null : Number(row.bmc_port),
        management_ip: toText(row.management_ip),
        serial_number: toText(row.serial_number),
        model: toText(row.model),
        datacenter_name: toText(row.datacenter_name),
        department_name: toText(row.department_name),
        datacenter_id: row.datacenter_id == null ? null : Number(row.datacenter_id),
        department_id: row.department_id == null ? null : Number(row.department_id)
      }
    : { bmc_protocol: 'redfish', bmc_port: 443 }
  editing.value = true
}

function isIPv4(value: string) {
  return /^(\d{1,3}\.){3}\d{1,3}$/.test(value) && value.split('.').every(part => Number(part) <= 255)
}
function isIPv6(value: string) {
  return /^[0-9a-f:]+$/i.test(value) && value.includes(':')
}

async function saveAsset() {
  if (!canWrite.value) return
  const name = String(form.value.name || '').trim()
  const address = String(form.value.bmc_address || '').trim()
  if (departmentRequired.value && !form.value.department_id) return message.warning('请选择管理范围内的部门')
  if (!name) return message.warning('请填写服务器名称')
  if (!address || (!isIPv4(address) && !isIPv6(address))) return message.warning('BMC 地址需为合法 IPv4 / IPv6 地址')
  const management = String(form.value.management_ip || '').trim()
  if (management && !isIPv4(management)) return message.warning('管理 IP 格式不正确')
  const port = Number(form.value.bmc_port ?? (form.value.bmc_protocol === 'ipmi' ? 623 : 443))
  if (!Number.isInteger(port) || port < 1 || port > 65535) return message.warning('BMC 端口需为 1–65535 的整数')
  if (currentId.value == null && (!String(form.value.bmc_user || '').trim() || !String(form.value.bmc_secret || '').trim()))
    return message.warning('新增服务器需要填写 BMC 用户名与密码')

  const payload: RecordData = {
    name,
    bmc_address: address,
    bmc_user: String(form.value.bmc_user || '').trim(),
    bmc_protocol: form.value.bmc_protocol === 'ipmi' ? 'ipmi' : 'redfish',
    bmc_port: port,
    management_ip: management || null,
    serial_number: String(form.value.serial_number || '').trim(),
    model: String(form.value.model || '').trim(),
    datacenter_id: form.value.datacenter_id == null ? null : Number(form.value.datacenter_id)
  }
  if (auth.can('departments:read')) payload.department_id = form.value.department_id ?? null
  if (String(form.value.bmc_secret || '').trim()) payload.bmc_secret = String(form.value.bmc_secret).trim()
  saving.value = true
  try {
    if (currentId.value != null) await resource.update('/assets', currentId.value, payload)
    else await resource.create('/assets', payload)
    message.success('保存成功')
    editing.value = false
    form.value = {}
    await load()
    void loadFilterOptions()
  } catch (e) {
    message.error(errorText(e))
  } finally {
    saving.value = false
  }
}

// === 批量登记（CIDR 网段） ===
const batchAdding = ref(false)
const batchSubmitting = ref(false)
const batchForm = ref({ cidr: '', username: '', password: '', protocol: 'redfish', port: 443 as number | null, prefix: '', datacenterId: null as number | null, departmentId: null as number | null })
type RegisterResult = { address: string; status: 'queued' | 'pending' | 'success' | 'failed' | 'unknown' | 'stopped'; detail: string }
const registerResults = ref<RegisterResult[]>([])
let registerRun: { stopped: boolean } | null = null

function closeBatchAdding(show: boolean) {
  if (batchSubmitting.value) return
  batchAdding.value = show
  if (!show) stopRegistration()
}

function stopRegistration() {
  if (registerRun) registerRun.stopped = true
  batchForm.value.password = ''
  registerResults.value.forEach(item => {
    if (item.status === 'queued') {
      item.status = 'stopped'
      item.detail = '已取消，未提交'
    } else if (item.status === 'pending') {
      item.status = 'unknown'
      item.detail = '请求已发出，请在列表核对后决定是否重试'
    }
  })
}

function registrationAddresses(cidr: string): string[] {
  const match = /^(\d{1,3})\.(\d{1,3})\.(\d{1,3})\.(\d{1,3})\/(\d{1,2})$/.exec(cidr.trim())
  if (!match) throw new Error('请输入有效的 BMC IPv4 CIDR，例如 192.168.1.0/24')
  const octets = match.slice(1, 5).map(Number)
  const prefix = Number(match[5])
  if (octets.some(n => n > 255) || match.slice(1, 5).some(part => part.length > 1 && part.startsWith('0')) || prefix > 32)
    throw new Error('IPv4 地址或前缀长度无效（不允许前导零）')
  const size = 2 ** (32 - prefix)
  const count = prefix <= 30 ? size - 2 : size
  if (count > 256) throw new Error('每次最多登记 256 个地址，请缩小网段')
  const ip = octets.reduce((value, n) => value * 256 + n, 0)
  const network = Math.floor(ip / size) * size
  const start = network + (prefix <= 30 ? 1 : 0)
  const addresses: string[] = []
  for (let n = start; n < start + count; n++) {
    const first = Math.floor(n / 2 ** 24)
    if (n === 0 || first === 127 || (first >= 224 && first <= 239)) throw new Error('网段包含不可登记地址（未指定/环回/组播）')
    addresses.push([first, Math.floor(n / 65536) % 256, Math.floor(n / 256) % 256, n % 256].join('.'))
  }
  return addresses
}

function openBatchAdding() {
  if (!canWrite.value || batchSubmitting.value) return
  registerResults.value = []
  batchForm.value.password = ''
  batchAdding.value = true
}

async function confirmBatchAdd() {
  if (batchSubmitting.value || !auth.projectId || !canWrite.value) return
  if (departmentRequired.value && !batchForm.value.departmentId) return message.warning('请选择管理范围内的部门')
  let addresses: string[]
  try {
    addresses = registrationAddresses(batchForm.value.cidr)
    if (!batchForm.value.username.trim() || !batchForm.value.password.trim()) throw new Error('请填写 BMC 用户名和密码')
    const port = batchForm.value.port
    if (port == null || !Number.isInteger(port) || port < 1 || port > 65535) throw new Error('端口需为 1–65535 的整数')
  } catch (e) {
    message.warning(errorText(e))
    return
  }
  dialog.warning({
    title: '确认批量登记',
    content: `${addresses[0]} 至 ${addresses[addresses.length - 1]}，共 ${addresses.length} 个地址。仅新建记录、不覆盖已有资产，登记成功不代表在线。`,
    positiveText: '确认登记',
    negativeText: '取消',
    onPositiveClick: () => void registerAssets(addresses)
  })
}

async function registerAssets(addresses: string[]) {
  const run = { stopped: false }
  registerRun = run
  batchSubmitting.value = true
  const { username, protocol, port, prefix, datacenterId, departmentId } = batchForm.value
  const projectId = auth.projectId
  const password = batchForm.value.password
  registerResults.value = addresses.map(address => ({ address, status: 'queued' as const, detail: '' }))
  let next = 0
  const active = () => !run.stopped && auth.projectId === projectId && auth.can('assets:write')
  const pump = async () => {
    while (active() && next < addresses.length) {
      const item = registerResults.value[next++]!
      item.status = 'pending'
      try {
        await resource.create(
          '/assets',
          {
            name: `${prefix.trim()}${item.address}`,
            bmc_address: item.address,
            bmc_user: username.trim(),
            bmc_secret: password,
            bmc_protocol: protocol,
            bmc_port: port,
            datacenter_id: datacenterId,
            department_id: departmentId
          },
          { projectId, sensitive: true }
        )
        if (active()) {
          item.status = 'success'
          item.detail = '已登记，未验证在线状态'
        }
      } catch (e) {
        if (active()) {
          const failed = e instanceof Error && e.message.includes('拒绝')
          item.status = failed ? 'failed' : 'unknown'
          item.detail = failed ? '服务器拒绝登记，请检查权限或重复地址' : '结果待确认，请在列表核对'
        }
      }
    }
  }
  try {
    await Promise.all(Array.from({ length: Math.min(4, addresses.length) }, pump))
  } finally {
    const shouldReload = !run.stopped && auth.projectId === projectId
    stopRegistration()
    batchSubmitting.value = false
    registerRun = null
    if (shouldReload) await load()
  }
}

const registerSummary = computed(() => ({
  success: registerResults.value.filter(item => item.status === 'success').length,
  failed: registerResults.value.filter(item => item.status === 'failed').length,
  pending: registerResults.value.filter(item => ['queued', 'pending'].includes(item.status)).length
}))

const registerColumns: DataTableColumns<RegisterResult> = [
  { title: 'BMC 地址', key: 'address' },
  {
    title: '结果',
    key: 'status',
    width: 110,
    render: row =>
      ({ queued: '等待', pending: '登记中', success: '成功', failed: '失败', unknown: '待确认', stopped: '未提交' })[row.status]
  },
  { title: '说明', key: 'detail' }
]

// === 导出 Excel ===
async function handleExport() {
  if (!items.value.length) return
  try {
    const exportRows = items.value.map(row => ({
      服务器名称: row.name ?? '',
      序列号: String(row.serial_number || ''),
      内网IP: String(row.ip_address || row.management_ip || ''),
      BMC地址: String(row.bmc_address || ''),
      配置规格: specOf(row).text,
      运行状态: metaOf(row).label,
      设备健康: healthOf(row).label,
      项目: projectName(row),
      部门: departmentName(row),
      机房: datacenterName(row.datacenter_id),
      创建时间: formatTime(row.created_at)
    }))
    const first = exportRows[0] || {}
    await exportToExcel(exportRows, Object.keys(first), '服务器列表')
    message.success('导出成功')
  } catch (e) {
    message.error(errorText(e))
  }
}

// === 生命周期与上下文切换 ===
watch(() => route.query.search, value => {
  keyword.value = typeof value === 'string' ? value : ''
  filterStatus.value = null
  searchField.value = 'auto'
  filterTags.value = []
  applyFilter()
})
watch(
  [() => auth.projectId, () => auth.tenantId],
  () => {
    page.value = 1
    checkedRowKeys.value = []
    specCache.value = {}
    batchResults.value = []
    resultsShowing.value = false

    pendingBatch = null
    editing.value = false
    form.value = {}
    stopRegistration()
    batchAdding.value = false
    batchForm.value.datacenterId = null
    batchForm.value.departmentId = null
    departments.value = []
    datacenterOptions.value = []
    tagOptions.value = []
    load()
    loadFilterOptions()
  },
  { immediate: true }
)

onBeforeUnmount(() => {
  disposed = true
  filterSequence++
  loadSequence++
  clearTimeout(refreshTimer)
  stopRegistration()
})
</script>

<template>
  <div ref="fleetElement" class="page server-list">
    <div class="page-head">
      <div>
        <div class="eyebrow dark">SERVER FLEET / ASSETS</div>
        <h1>服务器管理</h1>
        <p>集中查看服务器配置、网络地址与运行状态。</p>
      </div>
      <div class="head-actions">
        <n-space :size="10" align="center">
          <n-button :disabled="!auth.projectId" :loading="loading" @click="load()">
            <template #icon><RefreshOutline /></template>
            刷新列表
          </n-button>
          <n-button v-if="canWrite" type="primary" :disabled="!auth.projectId" @click="openEdit()">
            <template #icon><AddOutline /></template>
            新增服务器
          </n-button>
        </n-space>
      </div>
    </div>

    <div class="data-table-wrapper">
      <div class="list-heading">
        <div class="list-title"><h2>服务器列表</h2><span class="list-total">{{ total }} 台</span></div>
        <span class="sync-state" :class="{ failed: !!loadError }" title="每 15 秒自动同步">
          <i class="sync-dot" aria-hidden="true" />
          {{ lastSyncedLabel }}
        </span>
      </div>
      <!-- 顶部筛选工具栏 -->
      <div class="table-toolbar">
        <div class="filter-cluster">
          <div class="filter-field filter-search">
            <span id="fleet-keyword-label" class="filter-label">搜索服务器</span>
            <div class="search-composer">
              <n-select
                v-model:value="searchField"
                :options="searchFieldOptions"
                aria-label="搜索字段"
                size="small"
                class="search-field-select"
                @update:value="keyword.trim() && applyFilter()"
              />
              <n-input
                v-model:value="keyword"
                :input-props="{ 'aria-labelledby': 'fleet-keyword-label' }"
                size="small"
                placeholder="名称、IP 或序列号"
                clearable
                class="search-content"
                @keyup.enter="applyFilter"
                @clear="applyFilter"
              />
              <n-button size="small" aria-label="搜索" :loading="loading" class="search-submit" @click="applyFilter">
                <svg viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true">
                  <circle cx="10.5" cy="10.5" r="6.5" />
                  <path d="m16 16 4.5 4.5" />
                </svg>
              </n-button>
            </div>
          </div>
          <div class="filter-field filter-tags">
            <span id="fleet-tags-label" class="filter-label">资产标签</span>
            <n-select
              v-model:value="filterTags"
              :options="tagOptions"
              aria-labelledby="fleet-tags-label"
              placeholder="全部标签"
              size="small"
              multiple
              clearable
              :max-tag-count="1"
              @update:value="applyFilter"
            />
          </div>
          <div class="filter-actions">
            <n-button size="small" @click="resetFilters">重置</n-button>
          </div>
        </div>

        <div class="primary-cluster">
          <n-button size="small" :disabled="!items.length" @click="handleExport">导出 Excel</n-button>
          <n-dropdown v-if="listToolOptions.length" trigger="click" :options="listToolOptions" @select="handleListTool">
            <n-button size="small">更多工具 <ChevronDownOutline class="menu-chevron" aria-hidden="true" /></n-button>
          </n-dropdown>
        </div>
      </div>

      <!-- 本页状态分布：点击即筛选 -->
      <div class="fleet-strip">
        <span class="strip-label">本页状态</span>
        <button type="button" class="fleet-chip" :class="{ active: !filterStatus }" :aria-pressed="!filterStatus" @click="filterStatus = null; applyFilter()">全部状态</button>
        <button
          v-for="chip in fleetChips"
          :key="chip.key"
          type="button"
          class="fleet-chip"
          :class="{ active: filterStatus === chip.key }"
          :aria-pressed="filterStatus === chip.key"
          :title="`筛选${chip.label}的服务器`"
          @click="filterStatus = filterStatus === chip.key ? null : chip.key; applyFilter()"
        >
          <span class="chip-dot" :class="chip.cls" />
          {{ chip.label }}
          <strong>{{ chip.count }}</strong>
        </button>
        <span v-if="fleetWidth < scrollX" class="strip-note">左右滑动查看完整信息</span>
      </div>

      <!-- 选中后浮出的批量操作条 -->
      <div v-if="checkedRowKeys.length" class="batch-action-bar">
        <span class="batch-count">已选 {{ checkedRowKeys.length }} 台</span>
        <n-button v-if="canPower" size="tiny" tertiary @click="runBatch(batchActions[0])">开机</n-button>
        <n-button v-if="canPower" size="tiny" tertiary @click="runBatch(batchActions[1])">关机</n-button>
        <n-button v-if="canPower" size="tiny" tertiary @click="runBatch(batchActions[2])">重启</n-button>
        <n-button v-if="canOperate" size="tiny" tertiary @click="runBatch(batchActions[3])">扫描</n-button>
        <n-popconfirm v-if="canWrite" @positive-click="confirmBatchDelete">
          <template #trigger><n-button size="tiny" tertiary type="error">删除</n-button></template>
          确认删除 {{ checkedRowKeys.length }} 台服务器记录？不可撤销。
        </n-popconfirm>
        <n-button size="tiny" text class="bar-cancel" :disabled="batchLoading" @click="checkedRowKeys = []">取消选择</n-button>
      </div>

      <n-data-table
        class="fleet-table"
        :bordered="false"
        remote
        size="small"
        :columns="columns"
        :data="items"
        :loading="loading"
        :row-key="rowKey"
        :scroll-x="scrollX"
        :checked-row-keys="checkedRowKeys"
        :pagination="{
          page,
          pageSize,
          itemCount: total,
          showSizePicker: true,
          pageSizes: [10, 20, 50, 100],
          prefix: () => `共 ${total} 台`,
          onUpdatePage: (next: number) => { page = next; load() },
          onUpdatePageSize: (size: number) => { pageSize = size; page = 1; load() }
        }"
        @update:checked-row-keys="(keys: DataTableRowKey[]) => { if (!batchLoading) checkedRowKeys = keys }"
      />
      <div v-if="!loading && !items.length" class="empty-hint">
        {{ loadError ? `加载失败：${loadError}` : '没有符合条件的服务器，调整筛选条件或新增服务器。' }}
      </div>
    </div>

    <n-modal v-model:show="consoleShowing" preset="card" title="选择控制台" class="console-picker-modal" style="width: min(540px, 92vw)">
      <p class="console-picker-target">{{ consoleTarget?.name || '当前服务器' }} · 根据运维场景选择连接方式</p>
      <div class="console-option">
        <strong>BMC Web · 硬件管理</strong>
        <p>进入服务器带外管理页面，查看硬件健康、传感器和电源状态；远程画面等功能以设备支持为准。</p>
        <small>在新标签页打开，需使用 BMC 账号登录，并确保浏览器可访问 BMC 网络。</small>
        <n-button type="primary" @click="openBmcConsole">打开 BMC Web ↗</n-button>
      </div>
      <div class="console-option">
        <strong>SOL 控制台 · 串口终端</strong>
        <p>通过网络访问服务器串口，查看启动日志、排查系统故障，或在系统支持时进行命令行操作。</p>
        <small>需启用 BMC SOL 和 BIOS / 操作系统串口重定向；此方式不提供图形桌面。</small>
        <n-button @click="openSolConsole">打开 SOL 控制台 →</n-button>
      </div>
    </n-modal>
    <n-modal v-model:show="resultsShowing" preset="card" title="逐台操作结果" style="width: min(1050px, 96vw)">
      <p>共 {{ batchResults.length }} 台；失败 {{ batchResults.filter(row => row.status === 'failed').length }} 台。电源操作异步执行，请到任务中心核对实际执行结果。</p>
      <n-data-table :columns="resultColumns" :data="batchResults" :row-key="row => row.asset_id" :scroll-x="850" :pagination="{ pageSize: 20 }" />
    </n-modal>
    <n-modal v-model:show="reinstallShowing" preset="card" title="确认重装系统" style="width: min(620px, 94vw)">
      <n-alert type="warning" :bordered="false">重装将清空服务器磁盘上的现有数据并重新安装系统。当前仅支持 BMM Test VM，物理服务器会明确拒绝。
        <p><n-button text type="primary" @click="router.push('/pxe-config')">去 PXE 配置页查看启动指引</n-button></p>
      </n-alert>
      <n-form label-placement="top" style="margin-top: 16px">
        <n-form-item label="装机方案" required>
          <n-select v-model:value="installPlan" :options="installPlans.map(plan => ({ label: String(plan.name || '安装方案信息不可用'), value: Number(plan.id) }))" :disabled="reinstallLoading" placeholder="请选择已有装机方案" />
          <small v-if="!installPlans.length">暂无可用方案。测试 VM 可在“PXE 实验室”自动创建 Ubuntu 安装方案。</small>
        </n-form-item>
        <InstallAccessSettings v-model="reinstallAccess" :disabled="reinstallLoading" />
        <n-form-item label="输入服务器名称确认" required>
          <n-input v-model:value="reinstallConfirmName" :placeholder="String(reinstallTarget?.name || reinstallTarget?.management_ip || reinstallTarget?.bmc_address || '')" :disabled="reinstallLoading" />
        </n-form-item>
      </n-form>
      <div class="modal-actions">
        <n-button @click="reinstallShowing = false">取消</n-button>
        <n-button type="error" :loading="reinstallLoading" :disabled="reinstallLoading || !installPlans.length || installPlan == null || reinstallConfirmName !== String(reinstallTarget?.name || reinstallTarget?.management_ip || reinstallTarget?.bmc_address || '') || !!installAccessError(reinstallAccess)" @click="submitReinstall">确认清盘并提交</n-button>
      </div>
    </n-modal>
    <TestVMLab v-model:show="testVmShowing" @updated="load(true)" />
    <!-- 新增 / 编辑服务器 -->
    <n-modal v-model:show="editing" preset="card" :title="currentId != null ? '编辑服务器' : '新增服务器'" style="width: min(560px, 94vw)">
      <n-form label-placement="top">
        <div class="form-grid">
          <n-form-item label="服务器名称" required>
            <n-input v-model:value="form.name" placeholder="例如 beijing-a01" />
          </n-form-item>
          <n-form-item label="BMC 地址" required>
            <n-input v-model:value="form.bmc_address" placeholder="IPv4 / IPv6，例如 192.168.1.10" />
          </n-form-item>
          <n-form-item label="BMC 协议">
            <n-select
              v-model:value="form.bmc_protocol"
              :options="protocols"
              @update:value="(value: string) => { form.bmc_port = value === 'ipmi' ? 623 : 443 }"
            />
          </n-form-item>
          <n-form-item label="BMC 端口">
            <n-input-number v-model:value="form.bmc_port" :min="1" :max="65535" :precision="0" style="width: 100%" />
          </n-form-item>
          <n-form-item label="BMC 用户名" :required="currentId == null">
            <n-input v-model:value="form.bmc_user" autocomplete="off" />
          </n-form-item>
          <n-form-item label="BMC 密码" :required="currentId == null">
            <n-input
              v-model:value="form.bmc_secret"
              type="password"
              autocomplete="new-password"
              :placeholder="currentId != null ? '留空保留现有凭据' : '请输入 BMC 密码'"
            />
          </n-form-item>
          <n-form-item label="管理 IP">
            <n-input v-model:value="form.management_ip" placeholder="留空表示未分配" />
          </n-form-item>
          <n-form-item v-if="auth.can('departments:read')" label="所属部门" :feedback="departmentLoadError" :validation-status="departmentLoadError ? 'error' : undefined">
            <n-select v-model:value="form.department_id" :options="editDepartmentOptions" :disabled="!!departmentLoadError" :clearable="!departmentRequired" filterable :placeholder="departmentRequired ? '请选择管理范围内的部门' : '未分配部门（可选）'" />
          </n-form-item>
          <n-form-item label="所属机房">
            <n-select v-model:value="form.datacenter_id" :options="editDatacenterOptions" clearable filterable placeholder="未分配机房" />
          </n-form-item>
          <n-form-item label="序列号">
            <n-input v-model:value="form.serial_number" placeholder="采集后可自动回填" />
          </n-form-item>
          <n-form-item label="型号">
            <n-input v-model:value="form.model" placeholder="采集后可自动回填" />
          </n-form-item>
        </div>
      </n-form>
      <div class="modal-actions">
        <n-button @click="editing = false">取消</n-button>
        <n-button type="primary" :loading="saving" @click="saveAsset">保存</n-button>
      </div>
    </n-modal>

    <!-- 批量登记（CIDR） -->
    <n-modal
      :show="batchAdding"
      preset="card"
      title="批量登记服务器"
      style="width: min(680px, 94vw)"
      :closable="!batchSubmitting"
      :mask-closable="!batchSubmitting"
      @update:show="closeBatchAdding"
    >
      <p role="note">每次最多 256 个地址；/30 及以下排除网络与广播地址。登记成功后由后台自动采集，不代表设备在线。</p>
      <n-form label-placement="top" :disabled="batchSubmitting">
        <div class="form-grid">
          <n-form-item label="BMC IPv4 网段（CIDR）" required class="form-wide">
            <n-input v-model:value="batchForm.cidr" placeholder="例如 192.168.1.0/24" />
          </n-form-item>
          <n-form-item label="BMC 用户名" required>
            <n-input v-model:value="batchForm.username" autocomplete="off" />
          </n-form-item>
          <n-form-item label="BMC 密码" required>
            <n-input v-model:value="batchForm.password" type="password" autocomplete="new-password" />
          </n-form-item>
          <n-form-item label="协议">
            <n-select
              v-model:value="batchForm.protocol"
              :options="protocols"
              @update:value="(value: string) => { batchForm.port = value === 'ipmi' ? 623 : 443 }"
            />
          </n-form-item>
          <n-form-item label="端口">
            <n-input-number v-model:value="batchForm.port" :min="1" :max="65535" :precision="0" style="width: 100%" />
          </n-form-item>
          <n-form-item label="命名前缀">
            <n-input v-model:value="batchForm.prefix" placeholder="名称 = 前缀 + IP，例如 server-" />
          </n-form-item>
          <n-form-item v-if="auth.can('departments:read')" label="所属部门" :feedback="departmentLoadError" :validation-status="departmentLoadError ? 'error' : undefined">
            <n-select v-model:value="batchForm.departmentId" :options="departmentOptions" :disabled="!!departmentLoadError" :clearable="!departmentRequired" filterable :placeholder="departmentRequired ? '请选择管理范围内的部门' : '统一分配部门（可选）'" />
          </n-form-item>
          <n-form-item label="所属机房">
            <n-select v-model:value="batchForm.datacenterId" :options="datacenterOptions" clearable filterable placeholder="未分配机房" />
          </n-form-item>
        </div>
      </n-form>
      <div v-if="registerResults.length" aria-live="polite">
        <p class="result-summary">
          共 {{ registerResults.length }} 项：成功 {{ registerSummary.success }}，失败 {{ registerSummary.failed }}，待确认 {{ registerSummary.pending }}。
        </p>
        <n-data-table size="small" :columns="registerColumns" :data="registerResults" :row-key="(row: RegisterResult) => row.address" :max-height="260" />
      </div>
      <div class="modal-actions">
        <n-button :disabled="batchSubmitting" @click="closeBatchAdding(false)">{{ registerResults.length ? '关闭' : '取消' }}</n-button>
        <n-button v-if="!registerResults.length" type="primary" :loading="batchSubmitting" @click="confirmBatchAdd">
          确认批量登记
        </n-button>
      </div>
    </n-modal>
  </div>
</template>

<style scoped>
.server-list { container-type: inline-size; }
.server-list .page-head { align-items: center; }
.list-heading { display: flex; align-items: center; justify-content: space-between; gap: 12px; padding: 20px 20px 0; flex-wrap: wrap; }
.list-title { display: flex; align-items: center; gap: 10px; }
.list-title h2 { margin: 0; font-size: 15px; font-weight: 600; }
.list-total { padding: 2px 8px; border-radius: var(--radius-sm); background: var(--color-surface-hover); color: var(--color-text-secondary); font-size: 12px; font-variant-numeric: tabular-nums; }
.table-toolbar { display: flex; align-items: flex-end; gap: 14px; padding: 18px 20px; flex-wrap: wrap; }
.filter-cluster { display: flex; gap: 12px; align-items: end; flex: 1; min-width: 0; }
.filter-search { flex: 0 0 calc((100cqw - 40px) / 3); }
.filter-tags { flex: 0 0 140px; }
.filter-field { display: flex; flex-direction: column; gap: 7px; min-width: 0; }
.filter-label { font-size: 12px; color: var(--color-text-secondary); }
.filter-field :deep(.n-select), .filter-field :deep(.n-input) { width: 100%; min-width: 0; }
.search-composer { display: grid; grid-template-columns: 105px minmax(0, 1fr) 32px; align-items: center; }
.search-composer :deep(.n-base-selection), .search-composer :deep(.n-input), .search-submit { border-radius: 0; }
.search-field-select :deep(.n-base-selection) { border-radius: var(--radius-sm) 0 0 var(--radius-sm); }
.search-composer .search-content, .search-submit { margin-left: -1px; width: calc(100% + 1px); }
.search-submit { border-radius: 0 var(--radius-sm) var(--radius-sm) 0; }
.search-composer :deep(.n-base-selection--focus), .search-composer :deep(.n-input--focus), .search-submit:focus-visible { z-index: 1; }
.primary-cluster { display: flex; align-items: center; justify-content: flex-end; gap: 8px; flex-wrap: wrap; margin-left: auto; }
.menu-chevron, :deep(.menu-chevron) { width: 13px; height: 13px; margin-left: 5px; }
.sync-state { display: inline-flex; align-items: center; gap: 7px; font-size: 12px; color: var(--color-text-tertiary); white-space: nowrap; }
.sync-state.failed { color: var(--color-error); }
.sync-dot { width: 6px; height: 6px; border-radius: 50%; background: var(--color-success); }
.sync-state.failed .sync-dot { background: var(--color-error); }
.fleet-strip { display: flex; align-items: center; gap: 4px; flex-wrap: wrap; padding: 10px 16px; border-bottom: 1px solid var(--color-border-light); background: var(--color-surface); }
.strip-label { margin-right: 8px; font-size: 12px; color: var(--color-text-tertiary); }
.fleet-chip { display: inline-flex; align-items: center; gap: 6px; padding: 6px 9px; border: 1px solid transparent; border-radius: var(--radius-md); background: transparent; color: var(--color-text-secondary); font: inherit; font-size: 12px; cursor: pointer; transition: background var(--transition-fast), color var(--transition-fast); }
.fleet-chip:hover { background: var(--color-surface-hover); color: var(--color-text-primary); }
.fleet-chip.active { color: var(--color-primary); background: var(--color-primary-light); }
.fleet-chip:focus-visible { outline: 2px solid var(--color-primary); outline-offset: 2px; }
.fleet-chip strong { font-weight: 600; font-variant-numeric: tabular-nums; color: inherit; }
.chip-dot { width: 6px; height: 6px; border-radius: 50%; }
.dot-running { background: var(--color-success); }
.dot-stopped { background: var(--color-text-tertiary); }
.dot-fault { background: var(--color-error); }
.dot-maintenance { background: var(--color-warning); }
.strip-note { margin-left: auto; font-size: 11px; color: var(--color-text-tertiary); }
.fleet-table :deep(.n-data-table-th) { font-size: 12px; font-weight: 500; background: var(--color-surface-hover); color: var(--color-text-secondary); }
.fleet-table :deep(.n-data-table-td) { padding-top: 18px; padding-bottom: 18px; }
.fleet-table :deep(.n-data-table__pagination) { padding: 12px 16px; margin: 0; border-top: 1px solid var(--color-border-light); }
:deep(.cell-server) { display: flex; gap: 12px; align-items: flex-start; min-width: 0; }
:deep(.server-rack) { width: 30px; height: 30px; padding: 6px; flex: 0 0 30px; box-sizing: border-box; border-radius: var(--radius-md); color: var(--color-primary); background: var(--color-primary-light); }
:deep(.server-id) { display: flex; flex-direction: column; gap: 4px; min-width: 0; }
:deep(.server-name) { font-size: 14px; font-weight: 600; color: var(--color-text-primary); overflow-wrap: anywhere; }
:deep(.server-name):hover { color: var(--color-primary); }
:deep(.server-name):focus-visible { outline: 2px solid var(--color-primary); outline-offset: 2px; }
:deep(.server-sn) { font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace; font-size: 12px; color: var(--color-text-secondary); overflow-wrap: anywhere; }
:deep(.cell-ip) { display: flex; flex-direction: column; gap: 6px; min-width: 0; }
:deep(.ip-line) { display: flex; align-items: center; gap: 6px; min-width: 0; max-width: 100%; padding: 2px 0; text-align: left; }
:deep(.ip-line) i { flex: 0 0 32px; font-size: 10px; font-weight: 500; font-style: normal; line-height: 20px; text-align: center; border-radius: 3px; color: var(--color-primary); background: var(--color-primary-light); }
:deep(.ip-line).dim i { color: var(--color-text-secondary); background: var(--color-surface-hover); }
:deep(.ip-line) code { min-width: 0; overflow-wrap: anywhere; font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace; font-size: 12px; font-variant-numeric: tabular-nums; color: var(--color-text-primary); }
:deep(.copy-icon) { width: 13px; height: 13px; flex: 0 0 13px; color: var(--color-text-tertiary); }
:deep(.ip-line).empty code { color: var(--color-text-tertiary); font-family: inherit; }
:deep(.ip-copy-button) { display: inline-flex; align-items: center; justify-content: center; flex: 0 0 24px; width: 24px; height: 24px; padding: 0; border: 1px solid var(--color-border-light); border-radius: var(--radius-sm); background: transparent; cursor: pointer; transition: background var(--transition-fast); }
:deep(.ip-copy-button):hover { background: var(--color-primary-light); }
:deep(.ip-copy-button):focus-visible { outline: 2px solid var(--color-primary); outline-offset: 2px; }
:deep(.ip-copy-button):hover .copy-icon { color: var(--color-primary); }
:deep(.spec-code) { font-size: 12px; line-height: 1.6; color: var(--color-text-secondary); overflow-wrap: anywhere; }
:deep(.spec-skeleton) { display: inline-block; width: 84px; height: 10px; border-radius: var(--radius-xs); background: var(--color-border-light); }
:deep(.cell-owner) { display: flex; flex-direction: column; gap: 5px; min-width: 0; }
:deep(.cell-owner) span { overflow-wrap: anywhere; color: var(--color-text-primary); }
:deep(.cell-owner) small { font-size: 12px; color: var(--color-text-tertiary); overflow-wrap: anywhere; }
:deep(.cell-muted) { color: var(--color-text-tertiary); }
:deep(.mono) { font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace; font-size: 12px; }
:deep(.row-actions) { gap: 10px; flex-wrap: nowrap; }
:deep(.row-actions .n-button) { font-size: 12px; }
.batch-action-bar { position: static; box-shadow: none; border-radius: 0; padding: 12px 20px; flex-wrap: wrap; }
.batch-action-bar :deep(.n-button) { background: rgba(255, 255, 255, 0.16); color: #fff; }
.batch-action-bar :deep(.n-button:hover) { background: rgba(255, 255, 255, 0.28); }
.bar-cancel { margin-left: auto; }
.console-picker-target { margin: 0 0 16px; color: var(--color-text-secondary); }
.console-option { padding: 18px; margin-top: 12px; border: 1px solid var(--color-border); border-radius: var(--radius-lg); }
.console-option strong { display: block; font-size: 15px; }
.console-option p { margin: 8px 0; line-height: 1.7; }
.console-option small { display: block; margin-bottom: 14px; color: var(--color-text-secondary); line-height: 1.6; }
.form-grid { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 2px 16px; }
.form-grid .form-wide { grid-column: 1 / -1; }
.result-summary { margin: 12px 0 8px; font-size: 12px; color: var(--color-text-secondary); }
@container (max-width: 760px) {
  .filter-cluster { display: grid; width: 100%; min-width: 100%; grid-template-columns: minmax(0, 1fr) auto; }
  .filter-search { grid-column: 1 / -1; }
  .filter-tags { max-width: 240px; }
  .list-heading { padding: 16px 16px 0; }
  .table-toolbar { padding: 16px; }
  .strip-label { flex-basis: 100%; margin: 0 0 3px; }
  .strip-note { flex-basis: 100%; margin: 4px 0 0; }
}
@media (max-width: 580px) {
  .search-composer { grid-template-columns: 105px minmax(0, 1fr) 36px; }
  .form-grid { grid-template-columns: minmax(0, 1fr); }
  .server-list .head-actions { width: 100%; }
}
</style>
