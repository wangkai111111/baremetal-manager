<script setup lang="ts">
// 服务器管理页 —— 裸机资产 fleet 视图
// 取代通用 ResourceView 的 /assets 页面：更贴合运维场景的筛选栏、批量操作栏与行内操作。
import { computed, h, onBeforeUnmount, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import {
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
import { useWindowSize } from '@vueuse/core'
import dayjs from 'dayjs'
import { api, batchOps, errorText, exportToExcel, resource, rows, unwrap, type RecordData } from '../api'
import { useAuth } from '../stores/auth'

const router = useRouter()
const message = useMessage()
const dialog = useDialog()
const auth = useAuth()
const { width: viewportWidth } = useWindowSize()

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

// === 筛选条件 ===
const filterStatus = ref<string | null>(null)
const searchField = ref('auto')
const searchFieldOptions = [
  { label: '自动识别', value: 'auto' },
  { label: '服务器 ID', value: 'id' },
  { label: '服务器名称', value: 'name' },
  { label: '管理 IP', value: 'management_ip' },
  { label: 'BMC IP', value: 'bmc_address' },
  { label: '序列号', value: 'serial_number' },
  { label: '机房', value: 'datacenter' },
  { label: '厂商', value: 'manufacturer' },
  { label: '型号', value: 'model' }
]
const filterTags = ref<string[]>([])
const keyword = ref('')

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
const canSubscriptions = computed(() => auth.systemMode === 'external_operation' && auth.can('orders:write'))
const selectable = computed(() => canWrite.value || canOperate.value || canPower.value || canSubscriptions.value)
const canSubscriptionFinance = computed(() => canSubscriptions.value && auth.can('finance:write'))
const renewShowing = ref(false)
const renewCycle = ref('monthly')
const renewCount = ref<number | null>(1)
const cycleOptions = [{ label: '小时', value: 'hourly' }, { label: '天', value: 'daily' }, { label: '月', value: 'monthly' }, { label: '年', value: 'yearly' }]
const batchResults = ref<RecordData[]>([])
const resultsShowing = ref(false)
const resultColumns: DataTableColumns<RecordData> = [
  { title: '服务器 ID', key: 'asset_id' },
  { title: '结果', key: 'status', render: row => ({ accepted: '任务已提交', pending: '待支付订单已生成', reclaim_pending: '已退订 / 待人工回收', failed: '失败' })[String(row.status)] || String(row.status) },
  { title: '任务 / 订单', key: 'reference', render: row => String(row.operation_id || row.power_operation_id || row.order_id || '—') },
  { title: '金额', key: 'amount', render: row => row.refund_cents != null ? `退款 ¥${(Number(row.refund_cents) / 100).toFixed(2)}` : row.paid_cents != null ? `已支付 ¥${(Number(row.paid_cents) / 100).toFixed(2)}` : row.payable_cents != null ? `待支付 ¥${(Number(row.payable_cents) / 100).toFixed(2)}` : '—' },
  { title: '说明', key: 'reason', render: row => String(row.reason || row.message || '已入任务队列，请在任务中心查看实际执行结果') }
]
let pendingBatch: { fingerprint: string; key: string } | null = null
async function submitBatchResult(kind: 'power' | 'renew' | 'unsubscribe', ids: number[], projectId: string, cycle = '', count = 1) {
  if (batchLoading.value || auth.projectId !== projectId || !ids.length) return
  if (kind === 'power' ? !canPower.value : !canSubscriptions.value) return
  const fingerprint = `${projectId}:${kind}:${ids.join(',')}:${cycle}:${count}`
  if (pendingBatch?.fingerprint !== fingerprint) pendingBatch = { fingerprint, key: crypto.randomUUID() }
  const key = pendingBatch.key
  batchLoading.value = true
  try {
    const response = kind === 'power' ? await batchOps.power(ids, cycle, key)
      : kind === 'renew' ? await batchOps.renew(ids, cycle, count, key) : await batchOps.unsubscribe(ids, key)
    if (auth.projectId !== projectId || disposed) return
    batchResults.value = unwrap<{ results: RecordData[] }>(response).results
    resultsShowing.value = true
    renewShowing.value = false
    checkedRowKeys.value = []
    pendingBatch = null
    await load()
  } catch (e) {
    message.error(`${errorText(e)}；未收到可靠结果时再次提交将复用本次请求键，请勿更换选中项后重试。`)
  } finally { batchLoading.value = false }
}
function confirmPayment() {
  if (!canSubscriptionFinance.value || batchLoading.value) return
  const rows = batchResults.value.filter(row => row.status === 'pending' && row.order_id)
  const projectId = auth.projectId
  if (!rows.length) return
  dialog.warning({
    title: '确认余额支付',
    content: `将逐笔支付 ${rows.length} 个续费订单，展示总额 ¥${(rows.reduce((sum, row) => sum + Number(row.payable_cents || 0), 0) / 100).toFixed(2)}。实际扣款以服务端订单为准，支付成功才延长租期；失败可重试，不重复扣款。`,
    positiveText: '确认扣款并续期', negativeText: '取消',
    onPositiveClick: async () => {
      if (auth.projectId !== projectId) return
      batchLoading.value = true
      try {
        for (const row of rows) {
          if (auth.projectId !== projectId || disposed) return
          try {
            const paid = unwrap<RecordData>(await batchOps.paySubscription(Number(row.order_id)))
            Object.assign(row, paid)
          } catch (e) { row.message = errorText(e) }
        }
        if (auth.projectId === projectId && !disposed) await load()
      } finally { batchLoading.value = false }
    }
  })
}
function confirmSubscriptions(kind: 'renew' | 'unsubscribe') {
  if (kind === 'unsubscribe' && !canSubscriptionFinance.value) return
  const ids = selectedIds()
  if (!canSubscriptions.value || batchLoading.value || !ids.length) return
  if (ids.length > 100) { message.warning('运营批量操作每次最多100台'); return }
  const cycle = renewCycle.value
  const count = renewCount.value || 1
  const projectId = auth.projectId
  dialog.warning({
    title: kind === 'renew' ? '确认批量续费' : '确认立即退订',
    content: kind === 'renew'
      ? `将为 ${ids.length} 台服务器各生成 ${count} ${cycleOptions.find(option => option.value === cycle)?.label}的待支付续费订单，按产品当前价格计费，不会自动支付或延长到期时间。`
      : `将立即退订 ${ids.length} 台服务器：按核验后的剩余已付租期退款至租户余额、提交关机、撤销客户使用权限并标记待人工回收。不清盘。缺少可靠租期或支付依据的服务器将逐台失败，不自动退款。此操作不可撤销。`,
    positiveText: kind === 'renew' ? '确认生成订单' : '确认立即退订', negativeText: '取消',
    onPositiveClick: () => submitBatchResult(kind, ids, projectId, cycle, count)
  })
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

function datacenterName(value: unknown) {
  if (value == null || value === '') return '未分配机房'
  const hit = datacenterOptions.value.find(option => option.value === Number(value))
  return hit ? String(hit.label) : `机房 #${value}`
}

// === 归属列：运营模式看租户，自用模式看部门 ===
const currentTenantName = computed(
  () => String(auth.tenants.find(tenant => String(tenant.id) === String(auth.tenantId))?.name || '')
)
function ownerLabel(row: RecordData): { primary: string; scope: string } {
  if (auth.systemMode === 'external_operation') {
    const id = row.tenant_id
    const hit = auth.tenants.find(tenant => String(tenant.id) === String(id))
    if (hit?.name) return { primary: String(hit.name), scope: '租户' }
    if (String(id) === String(auth.tenantId)) return { primary: currentTenantName.value || '当前租户', scope: '租户' }
    return { primary: id == null ? '—' : `租户 #${id}`, scope: '租户' }
  }
  const id = row.department_id
  const hit = departments.value.find(dept => String(dept.id) === String(id))
  if (hit?.name) return { primary: String(hit.name), scope: '部门' }
  return { primary: id == null ? '未分配' : `部门 #${id}`, scope: '部门' }
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
    await navigator.clipboard.writeText(value)
    message.success(`已复制 ${value}`)
  } catch {
    message.warning('当前浏览器不支持剪贴板，请手动复制')
  }
}

// === 列定义 ===
const compactView = computed(() => viewportWidth.value < 1080)

const columns = computed<DataTableColumns<RecordData>>(() => {
  const cols: DataTableColumns<RecordData> = []
  if (selectable.value) cols.push({ type: 'selection', width: 44, align: 'center', fixed: 'left', disabled: () => batchLoading.value })

  cols.push({
    title: '服务器',
    key: 'name',
    minWidth: 208,
    render: row =>
      h('div', { class: 'cell-server' }, [
        h('span', { class: 'server-rack', 'aria-hidden': 'true' }),
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
            String(row.name || `#${row.id}`)
          )
        ])
      ])
  })

  cols.push({
    title: '序列号',
    key: 'serial_number',
    minWidth: 150,
    ellipsis: { tooltip: true },
    render: row => h('span', { class: 'mono' }, String(row.serial_number || '未登记'))
  })

  cols.push({
    title: 'IP 地址',
    key: 'ip_address',
    minWidth: 168,
    render: row => {
      const service = String(row.ip_address || row.management_ip || '')
      const bmc = String(row.bmc_address || '')
      return h('div', { class: 'cell-ip' }, [
        service
          ? h(
              'button',
              { class: 'ip-line', type: 'button', title: '点击复制业务/管理 IP', onClick: () => copyText(service) },
              [h('i', {}, '管理'), h('code', {}, service)]
            )
          : h('div', { class: 'ip-line empty' }, [h('i', {}, '管理'), h('code', {}, '未分配')]),
        bmc
          ? h(
              'button',
              { class: 'ip-line dim', type: 'button', title: '点击复制 BMC 地址', onClick: () => copyText(bmc) },
              [h('i', {}, 'BMC'), h('code', {}, bmc)]
            )
          : h('div', { class: 'ip-line dim empty' }, [h('i', {}, 'BMC'), h('code', {}, '—')])
      ])
    }
  })

  cols.push({
    title: '配置规格',
    key: 'spec',
    minWidth: 150,
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
    width: 116,
    render: row => h(NTag, { size: 'small', bordered: false, type: metaOf(row).tag }, { default: () => metaOf(row).label })
  })

  if (!compactView.value) {
    cols.push({
      title: '所属租户/部门',
      key: 'owner',
      minWidth: 150,
      render: row => {
        const owner = ownerLabel(row)
        return h('div', { class: 'cell-owner' }, [h('span', {}, owner.primary), h('small', {}, owner.scope)])
      }
    })
    cols.push({
      title: '机房',
      key: 'datacenter_id',
      minWidth: 118,
      render: row => h('span', { class: 'cell-muted' }, datacenterName(row.datacenter_id))
    })
  }

  cols.push({
    title: '创建时间',
    key: 'created_at',
    width: 160,
    render: row => h('span', { class: 'cell-muted mono', style: { whiteSpace: 'nowrap' } }, formatTime(row.created_at))
  })

  cols.push({
    title: '操作',
    key: 'actions',
    width: 246,
    fixed: 'right',
    render: row => {
      const intent = powerIntent(row)
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
        canPower.value
          ? h(NButton, { text: true, type: 'warning', disabled: powerLoading.value === row.id, loading: powerLoading.value === row.id, onClick: () => confirmSinglePower(row, 'restart', '重启') }, { default: () => '重启' })
          : null,
        canConsole.value
          ? h(NButton, { text: true, onClick: () => openConsoleChooser(row) }, { default: () => '控制台' })
          : null,
        h(
          NDropdown,
          { trigger: 'click', options: menu, onSelect: (key: string) => handleRowMenu(key, row) },
          { default: () => h(NButton, { text: true, 'aria-label': '更多操作' }, { default: () => '更多 ▾' }) }
        )
      ])
    }
  })
  return cols
})

const scrollX = computed(() => (compactView.value ? 1198 : 1466) + (selectable.value ? 44 : 0))

// 行主键：资产 id 为后端必填数值主键
const rowKey = (row: RecordData) => row.id as string | number

function rowMenuOptions(row: RecordData): DropdownOption[] {
  const options: DropdownOption[] = [{ label: '详情', key: 'detail' }, { label: '重装', key: 'reinstall' }]
  if (canOperate.value) options.push({ label: '采集硬件信息', key: 'collect' })
  options.push({ label: '打开 BMC 界面', key: 'bmc', disabled: !bmcWebUrl(row) })
  if (canWrite.value) options.push({ type: 'divider', key: 'divider' }, { label: '编辑', key: 'edit' }, { label: '删除', key: 'delete' })
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
  try {
    const result = rows(await resource.list('/datacenters', { page_size: 200 }))
    datacenterOptions.value = result.items.map(dc => ({ label: String(dc.name || dc.id), value: Number(dc.id) }))
  } catch (e) {
    message.warning(`机房选项加载失败：${errorText(e)}`)
  }
  try {
    const result = rows(await resource.list('/tags', { page_size: 200 }))
    tagOptions.value = result.items.map(tag => ({ label: String(tag.name || tag.id), value: String(tag.name || tag.id) }))
  } catch {
    // 标签加载失败不阻塞主流程
  }
  if (auth.systemMode === 'self_use') {
    try {
      departments.value = rows(await resource.list('/departments', { page_size: 200 })).items
    } catch {
      departments.value = []
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
  const order = ['running', 'in_use', 'stopped', 'error', 'maintenance', 'offline']
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
const canBatch = computed(() => checkedRowKeys.value.length > 0 && !batchLoading.value)
const batchMenuOptions = computed<DropdownOption[]>(() => batchActions
  .filter(action => action.key !== 'delete' || auth.systemMode !== 'external_operation')
  .map(action => ({
    label: action.label,
    key: action.key,
    disabled: action.key === 'delete' ? !canWrite.value : action.key === 'scan' ? !canOperate.value : ['on', 'off', 'restart'].includes(action.key) ? !canPower.value : false
  })))

function handleBatchMenuSelect(key: string) {
  const action = batchActions.find(item => item.key === key)
  if (action) runBatch(action)
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
  if (!ids.length || !canWrite.value || batchLoading.value || auth.systemMode === 'external_operation') return
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
    content: `将对 ${row.name || row.id} 执行${label}操作，该操作可能影响业务。`,
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

// 后端 /assets/:id/install 尚未实现真实装机执行，此处优雅降级而不是发起注定失败的请求
function startReinstall(row: RecordData) {
  dialog.info({
    title: '重装系统',
    content: `${row.name || row.id}：装机执行能力尚未开放（后端返回 501）。可在「装机任务」查看历史重装记录，或等待装机流水线上线。`,
    positiveText: '查看装机任务',
    negativeText: '知道了',
    onPositiveClick: () => router.push('/installations')
  })
}

function removeAsset(row: RecordData) {
  if (!canWrite.value) return
  dialog.warning({
    title: '确认删除服务器',
    content: `确认删除 ${row.name || row.id}？删除后 BMC 凭据与采集历史将不可恢复。`,
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
}
const editing = ref(false)
const saving = ref(false)
const currentId = ref<string | number | null>(null)
const form = ref<AssetForm>({})
const protocols = [
  { label: 'Redfish', value: 'redfish' },
  { label: 'IPMI', value: 'ipmi' }
]

const toText = (value: unknown) => (value == null ? '' : String(value))

function openEdit(row?: RecordData) {
  if (!canWrite.value) return
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
        datacenter_id: row.datacenter_id == null ? null : Number(row.datacenter_id)
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
const batchForm = ref({ cidr: '', username: '', password: '', protocol: 'redfish', port: 443 as number | null, prefix: '', datacenterId: null as number | null })
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
  const { username, protocol, port, prefix, datacenterId } = batchForm.value
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
            datacenter_id: datacenterId
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
      内网IP: String(row.ip_address || row.management_ip || ''),
      BMC地址: String(row.bmc_address || ''),
      配置规格: specOf(row).text,
      运行状态: metaOf(row).label,
      [ownerLabel(row).scope]: ownerLabel(row).primary,
      机房: datacenterName(row.datacenter_id),
      序列号: String(row.serial_number || ''),
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
watch(
  [() => auth.projectId, () => auth.tenantId, () => auth.systemMode],
  () => {
    page.value = 1
    checkedRowKeys.value = []
    specCache.value = {}
    batchResults.value = []
    resultsShowing.value = false
    renewShowing.value = false
    pendingBatch = null
    load()
    loadFilterOptions()
  },
  { immediate: true }
)

onBeforeUnmount(() => {
  disposed = true
  loadSequence++
  clearTimeout(refreshTimer)
  stopRegistration()
})
</script>

<template>
  <div class="page server-list">
    <div class="page-head">
      <div>
        <div class="eyebrow dark">SERVER FLEET / ASSETS</div>
        <h1>服务器管理</h1>
        <p>按机房、状态与标签筛选裸机；支持批量电源操作、硬件采集与 BMC 凭据登记。</p>
      </div>
      <div class="head-actions">
        <n-space :size="10" align="center">
          <span class="sync-state" :class="{ failed: !!loadError }">
            <i class="sync-dot" aria-hidden="true" />
            {{ lastSyncedLabel }}
          </span>
          <n-button :disabled="!auth.projectId" :loading="loading" @click="load()">刷新列表</n-button>
        </n-space>
      </div>
    </div>

    <div class="data-table-wrapper">
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
                placeholder="输入搜索内容"
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

        <div class="batch-cluster">
          <span class="selection-hint">{{ checkedRowKeys.length ? `已选 ${checkedRowKeys.length} 台` : '勾选行后可批量操作' }}</span>
          <n-dropdown
            trigger="click"
            placement="bottom-start"
            :options="batchMenuOptions"
            :disabled="!canBatch"
            @select="handleBatchMenuSelect"
          >
            <n-button size="small" :disabled="!canBatch">
              批量操作
              <span class="more-dots" aria-hidden="true">•••</span>
            </n-button>
          </n-dropdown>
        </div>

        <div class="primary-cluster">
          <n-button size="small" :disabled="!items.length" @click="handleExport">导出 Excel</n-button>
          <n-button v-if="canWrite" size="small" tertiary @click="openBatchAdding">批量登记</n-button>
          <n-button v-if="canWrite" size="small" type="primary" :disabled="!auth.projectId" @click="openEdit()">
            + 新增服务器
          </n-button>
        </div>
      </div>

      <!-- 本页状态分布：点击即筛选 -->
      <div class="fleet-strip">
        <span class="strip-label">本页分布</span>
        <button
          v-for="chip in fleetChips"
          :key="chip.key"
          type="button"
          class="fleet-chip"
          :class="{ active: filterStatus === chip.key }"
          :title="`筛选${chip.label}的服务器`"
          @click="filterStatus = filterStatus === chip.key ? null : chip.key; applyFilter()"
        >
          <span class="chip-dot" :class="chip.cls" />
          {{ chip.label }}
          <strong>{{ chip.count }}</strong>
        </button>
        <span class="strip-note">共 {{ total }} 台 · 每 15 秒静默刷新</span>
      </div>

      <!-- 选中后浮出的批量操作条 -->
      <div v-if="checkedRowKeys.length" class="batch-action-bar">
        <span class="batch-count">已选 {{ checkedRowKeys.length }} 台</span>
        <n-button v-if="canPower" size="tiny" tertiary @click="runBatch(batchActions[0])">开机</n-button>
        <n-button v-if="canPower" size="tiny" tertiary @click="runBatch(batchActions[1])">关机</n-button>
        <n-button v-if="canPower" size="tiny" tertiary @click="runBatch(batchActions[2])">重启</n-button>
        <n-button v-if="canOperate" size="tiny" tertiary @click="runBatch(batchActions[3])">扫描</n-button>
        <n-button v-if="canSubscriptions" size="tiny" tertiary :disabled="batchLoading" @click="renewShowing = true">批量续费</n-button>
        <n-button v-if="canSubscriptionFinance" size="tiny" tertiary :disabled="batchLoading" @click="confirmSubscriptions('unsubscribe')">立即退订</n-button>
        <n-popconfirm v-if="canWrite && auth.systemMode !== 'external_operation'" @positive-click="confirmBatchDelete">
          <template #trigger>
            <n-button size="tiny" tertiary type="error">删除</n-button>
          </template>
          确认删除 {{ checkedRowKeys.length }} 台服务器记录？不可撤销。
        </n-popconfirm>
        <n-button size="tiny" text class="bar-cancel" :disabled="batchLoading" @click="checkedRowKeys = []">取消选择</n-button>
      </div>

      <n-data-table
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
    <n-modal v-model:show="renewShowing" preset="card" title="批量续费" style="width: min(460px, 94vw)">
      <p>已选 {{ checkedRowKeys.length }} 台。逐台生成待支付订单，不自动扣款或延长到期时间。</p>
      <n-form label-placement="top">
        <n-form-item label="续费周期"><n-select v-model:value="renewCycle" :options="cycleOptions" :disabled="batchLoading" /></n-form-item>
        <n-form-item label="周期数量"><n-input-number v-model:value="renewCount" :min="1" :max="36" :precision="0" :disabled="batchLoading" /></n-form-item>
      </n-form>
      <n-button type="primary" :loading="batchLoading" :disabled="batchLoading || !renewCount" @click="confirmSubscriptions('renew')">确认续费方案</n-button>
    </n-modal>
    <n-modal v-model:show="resultsShowing" preset="card" title="逐台操作结果" style="width: min(1050px, 96vw)">
      <n-button v-if="canSubscriptionFinance && batchResults.some(row => row.status === 'pending' && row.order_id)" type="primary" :loading="batchLoading" :disabled="batchLoading" @click="confirmPayment">确认余额支付续费订单</n-button>
      <p>共 {{ batchResults.length }} 台；失败 {{ batchResults.filter(row => row.status === 'failed').length }} 台。电源操作异步执行，请到任务中心核对实际执行结果；退订后待人工回收，不清盘。</p>
      <n-data-table :columns="resultColumns" :data="batchResults" :row-key="row => row.asset_id" :scroll-x="850" :pagination="{ pageSize: 20 }" />
    </n-modal>
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
          <n-form-item label="所属机房">
            <n-select v-model:value="form.datacenter_id" :options="datacenterOptions" clearable filterable placeholder="未分配机房" />
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
/* === 工具栏：筛选独立成行，批量与主操作分区 === */
.table-toolbar {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto;
  align-items: center;
  gap: 14px 16px;
}

.filter-cluster {
  grid-column: 1 / -1;
  display: grid;
  grid-template-columns: minmax(0, 620px) minmax(100px, 140px) auto;
  justify-content: start;
  gap: 12px;
  align-items: end;
  padding-bottom: 16px;
  border-bottom: 1px solid var(--color-border-light);
}

.filter-field {
  display: flex;
  flex-direction: column;
  gap: 6px;
  min-width: 0;
}

.filter-label {
  font-size: var(--font-size-xs);
  font-weight: 500;
  color: var(--color-text-secondary);
}

.filter-field :deep(.n-select),
.filter-field :deep(.n-input) {
  width: 100%;
  min-width: 0;
}

.filter-search {
  grid-column: auto;
}

.filter-tags {
  min-width: 0;
  justify-self: start;
  width: 100%;
}

.search-composer {
  display: grid;
  grid-template-columns: 148px minmax(0, 1fr) 44px;
  width: min(100%, 620px);
  align-items: center;
}

.search-composer :deep(.n-base-selection),
.search-composer :deep(.n-input),
.search-submit {
  border-radius: 0;
}

.search-field-select :deep(.n-base-selection) {
  border-radius: var(--radius-sm, 4px) 0 0 var(--radius-sm, 4px);
}

.search-composer .search-content,
.search-submit {
  margin-left: -1px;
  width: calc(100% + 1px);
}

.search-submit {
  border-radius: 0 var(--radius-sm, 4px) var(--radius-sm, 4px) 0;
}

.search-composer :deep(.n-base-selection--focus),
.search-composer :deep(.n-input--focus),
.search-submit:focus-visible {
  z-index: 1;
}

.filter-actions,
.batch-cluster,
.primary-cluster {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
  min-width: 0;
}

.filter-actions {
  flex-wrap: nowrap;
}

.server-meta {
  display: flex;
  gap: 8px;
  align-items: center;
  min-width: 0;
}

.server-id-code {
  color: var(--color-text-tertiary);
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
}

.console-picker-target {
  margin: 0 0 16px;
  color: var(--color-text-secondary);
}

.console-option {
  padding: 18px;
  margin-top: 12px;
  border: 1px solid var(--color-border, #dce1e8);
  border-radius: 10px;
}

.console-option strong {
  display: block;
  font-size: 15px;
}

.console-option p {
  margin: 8px 0;
  line-height: 1.7;
}

.console-option small {
  display: block;
  margin-bottom: 14px;
  color: var(--color-text-secondary);
  line-height: 1.6;
}

.more-dots {
  margin-left: 6px;
  letter-spacing: 1px;
}

.batch-cluster {
  justify-content: flex-start;
}

.batch-cluster :deep(.n-button-group) {
  flex-wrap: wrap;
}

.primary-cluster {
  justify-content: flex-end;
}

.selection-hint {
  font-size: var(--font-size-xs);
  color: var(--color-text-tertiary);
  white-space: nowrap;
}

/* === 列表同步状态 === */
.sync-state {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-size: var(--font-size-xs);
  color: var(--color-text-tertiary);
  white-space: nowrap;
}

.sync-state.failed {
  color: var(--color-error);
}

.sync-dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: var(--color-success);
  box-shadow: 0 0 0 3px color-mix(in srgb, var(--color-success) 18%, transparent);
}

.sync-state.failed .sync-dot {
  background: var(--color-error);
  box-shadow: 0 0 0 3px color-mix(in srgb, var(--color-error) 18%, transparent);
}

/* === 本页状态分布条 === */
.fleet-strip {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
  padding: 10px 18px;
  border-bottom: 1px solid var(--color-border-light);
  background: linear-gradient(180deg, var(--color-surface-raised), var(--color-surface));
}

.strip-label {
  font-size: 10px;
  letter-spacing: 1.2px;
  text-transform: uppercase;
  color: var(--color-text-tertiary);
  font-weight: 600;
}

.fleet-chip {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: 3px 10px;
  border: 1px solid var(--color-border);
  border-radius: var(--radius-full);
  background: var(--color-surface);
  color: var(--color-text-secondary);
  font-size: var(--font-size-xs);
  cursor: pointer;
  transition: border-color var(--transition-fast), box-shadow var(--transition-fast), transform var(--transition-fast);
}

.fleet-chip:hover {
  border-color: var(--color-primary);
  transform: translateY(-1px);
}

.fleet-chip.active {
  border-color: var(--color-primary);
  box-shadow: 0 0 0 2px var(--color-primary-light);
  color: var(--color-primary);
}

.fleet-chip strong {
  font-variant-numeric: tabular-nums;
  font-weight: 700;
  color: var(--color-text-primary);
}

.chip-dot {
  width: 7px;
  height: 7px;
  border-radius: 50%;
  background: currentColor;
}

.chip-dot.dot-running {
  background: var(--color-success);
}

.chip-dot.dot-stopped {
  background: var(--color-text-disabled);
}

.chip-dot.dot-fault {
  background: var(--color-error);
}

.chip-dot.dot-maintenance {
  background: var(--color-warning);
}

.strip-note {
  margin-left: auto;
  font-size: var(--font-size-xs);
  color: var(--color-text-tertiary);
}

/* === 批量操作条按钮在品牌蓝底上的反色处理 === */
.batch-action-bar :deep(.n-button) {
  background: rgba(255, 255, 255, 0.16);
  color: #fff;
}

.batch-action-bar :deep(.n-button:hover) {
  background: rgba(255, 255, 255, 0.28);
}

.bar-cancel {
  margin-left: auto;
}

/* === 表格单元格 === */
.data-table-wrapper :deep(.cell-server) {
  display: flex;
  align-items: center;
  gap: 10px;
  min-width: 0;
}

.server-rack {
  position: relative;
  width: 24px;
  height: 24px;
  flex: 0 0 24px;
  border-radius: 6px;
  background: var(--color-primary-light);
  border: 1px solid var(--color-border);
}

.server-rack::before {
  content: '';
  position: absolute;
  inset: 6px 5px;
  background: repeating-linear-gradient(
    to bottom,
    var(--color-primary) 0 2px,
    transparent 2px 5px
  );
  opacity: 0.75;
}

.server-rack::after {
  content: '';
  position: absolute;
  top: 4px;
  right: 4px;
  width: 3px;
  height: 3px;
  border-radius: 50%;
  background: var(--color-primary);
}

.server-id {
  display: flex;
  flex-direction: column;
  gap: 2px;
  min-width: 0;
}

.server-name {
  font-weight: 600;
  color: var(--color-text-primary);
  font-size: var(--font-size-sm);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.server-name:hover {
  color: var(--color-primary);
}

.server-sn {
  font-family: ui-monospace, 'JetBrains Mono', SFMono-Regular, Menlo, Consolas, monospace;
  font-size: 11px;
  color: var(--color-text-tertiary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

:deep(.cell-ip) {
  display: flex;
  flex-direction: column;
  gap: 6px;
  min-width: 0;
}

:deep(.ip-line) {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
  max-width: 100%;
  padding: 3px 6px;
  border: 0;
  border-radius: 5px;
  background: transparent;
  cursor: pointer;
  text-align: left;
  font: inherit;
  transition: background 0.15s;
}

:deep(.ip-line) i {
  flex: 0 0 32px;
  box-sizing: border-box;
  padding: 2px 0;
  border-radius: 4px;
  background: color-mix(in srgb, var(--color-primary) 9%, transparent);
  color: var(--color-primary);
  text-align: center;
  font-size: 10px;
  font-style: normal;
  font-weight: 600;
  line-height: 16px;
}

:deep(.ip-line).dim i {
  background: color-mix(in srgb, var(--color-text-secondary) 9%, transparent);
  color: var(--color-text-secondary);
}

:deep(.ip-line) code {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  font-size: 12px;
  font-variant-numeric: tabular-nums;
  color: var(--color-text-primary);
}

:deep(.ip-line).empty {
  cursor: default;
}

:deep(.ip-line).empty code {
  color: var(--color-text-tertiary);
  font-family: inherit;
  font-size: 11px;
}

:deep(button.ip-line):hover,
:deep(button.ip-line):focus-visible {
  background: color-mix(in srgb, var(--color-primary) 8%, transparent);
}

:deep(button.ip-line):hover code {
  color: var(--color-primary);
}

.spec-code {
  font-family: ui-monospace, 'JetBrains Mono', SFMono-Regular, Menlo, Consolas, monospace;
  font-size: var(--font-size-xs);
  color: var(--color-text-primary);
  letter-spacing: 0.2px;
}

.spec-skeleton {
  display: inline-block;
  width: 84px;
  height: 10px;
  border-radius: var(--radius-xs);
  background: linear-gradient(90deg, var(--color-border-light), var(--color-border), var(--color-border-light));
  background-size: 200% 100%;
  animation: spec-shimmer 1.2s linear infinite;
}

@keyframes spec-shimmer {
  from {
    background-position: 200% 0;
  }
  to {
    background-position: -200% 0;
  }
}

.cell-owner {
  display: flex;
  flex-direction: column;
  gap: 2px;
  min-width: 0;
}

.cell-owner span {
  color: var(--color-text-primary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.cell-owner small {
  font-size: 10px;
  letter-spacing: 0.6px;
  text-transform: uppercase;
  color: var(--color-text-tertiary);
}

.cell-muted {
  color: var(--color-text-tertiary);
}

.mono {
  font-family: ui-monospace, 'JetBrains Mono', SFMono-Regular, Menlo, Consolas, monospace;
  font-size: var(--font-size-xs);
}

.row-actions {
  gap: 10px;
}

/* === 表单栅格 === */
.form-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 2px 16px;
}

.form-grid .form-wide,
.form-grid :deep(.n-form-item):first-child.form-wide {
  grid-column: 1 / -1;
}

.result-summary {
  font-size: var(--font-size-xs);
  color: var(--color-text-secondary);
  margin: 12px 0 8px;
}

[data-theme='dark'] .spec-skeleton {
  opacity: 0.6;
}

@media (max-width: 1080px) {
  .table-toolbar {
    grid-template-columns: minmax(0, 1fr);
  }
  .primary-cluster {
    justify-content: flex-start;
  }
}

@media (max-width: 580px) {
  .filter-cluster {
    grid-template-columns: minmax(0, 620px) minmax(100px, 140px) auto;
  justify-content: start;
  }
  .filter-actions {
    justify-content: flex-start;
  }
}

@media (max-width: 780px) {
  .strip-note {
    width: 100%;
    margin-left: 0;
  }
  .form-grid {
    grid-template-columns: 1fr;
  }
}

@media (prefers-reduced-motion: reduce) {
  .spec-skeleton {
    animation: none;
  }
}
</style>
