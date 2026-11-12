<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
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
  NDropdown,
  useMessage,
  useDialog
} from 'naive-ui'
import type { DataTableColumns, DataTableRowKey, DropdownOption } from 'naive-ui'
import { h } from 'vue'
import dayjs from 'dayjs'
import type { Section } from '../catalog'
import { labels } from '../catalog'
import { errorText, resource, rows, type RecordData, api, exportToExcel, batchOps, unwrap, AssetRegistrationError } from '../api'
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
const loadError = ref('')
const search = ref('')
const searchField = ref('auto')
const assetSearchOptions = [
  { label: '自动识别', value: 'auto' },
  { label: '服务器 ID', value: 'id' },
  { label: '服务器名称', value: 'name' },
  { label: 'BMC 地址', value: 'bmc_address' },
  { label: '管理 IP', value: 'management_ip' },
  { label: '序列号', value: 'serial_number' },
  { label: '厂商', value: 'manufacturer' },
  { label: '型号', value: 'model' },
  { label: '机房名称', value: 'datacenter' }
]
const assetStatusLabels: Record<string, string> = {
  pending_management: '待纳管', managing: '纳管中', collecting: '采集中',
  in_use: '使用中', starting: '开机中', stopping: '关机中', stopped: '关机',
  running: '运行中', restarting: '重启中', reinstalling: '重装中',
  upgrading: '固件升级中', queued: '等待执行', maintenance: '维护中',
  retired: '已退役', offline: '离线', error: '纳管失败', unknown: '状态未知'
}
const assetStatus = (row: RecordData) => String(row.display_status || 'unknown')

// === 运营/财务枚举的中文标签 ===
// 仅覆盖仍由通用 ResourceView 承载的运营 section（账单 / 账户 / 支付记录 / 产品），
// 键值与后端 DDL CHECK 逐值对齐；订单 / 发票 / 优惠券 / 工单已由专用视图承载。
const enumLabelApis = ['/bills', '/accounts', '/payments', '/products']
const enumValueLabels: Record<string, Record<string, string>> = {
  status: {
    unpaid: '未付款', paid: '已付款', overdue: '逾期',
    pending: '待处理', success: '成功', failed: '失败',
    requested: '待开具', issued: '已开具', cancelled: '已作废',
    active: '启用', inactive: '停用', expired: '已过期', sold_out: '已售罄'
  },
  method: { alipay: '支付宝', wechat: '微信支付', transfer: '银行转账', balance: '余额支付' },
  billing_cycle: { hourly: '按小时', daily: '按天', monthly: '按月', yearly: '按年' }
}
function enumLabel(key: string, value: unknown): string {
  const raw = String(value ?? '')
  if (!raw) return '—'
  return enumValueLabels[key]?.[raw] || raw
}
const formatCreatedAt = (value: unknown) => {
  if (value == null || value === '') return '—'
  const parsed = dayjs(String(value))
  return parsed.isValid() ? parsed.format('YYYY-MM-DD HH:mm') : '—'
}
const powerState = (row: RecordData) => String(row.power_state || '').trim().toLowerCase()
const powerAction = (row: RecordData) => powerState(row) === 'on' ? 'off' : powerState(row) === 'off' ? 'on' : 'restart'
const powerActionLabel = (row: RecordData) => powerAction(row) === 'on' ? '开机' : powerAction(row) === 'off' ? '关机' : '重启'
const page = ref(1)
const pageSize = ref(20)

const editing = ref(false)
const saving = ref(false)
const batchAdding = ref(false)
const batchSubmitting = ref(false)
const batchConfirming = ref(false)
const batchForm = ref({ cidr: '', username: '', password: '', protocol: 'redfish', port: 443 as number | null, prefix: '', datacenterId: null as number | null })
type RegistrationResult = { address: string; status: 'queued' | 'pending' | 'success' | 'failed' | 'unknown' | 'stopped'; detail: string }
const registrationResults = ref<RegistrationResult[]>([])
let registrationRun: { stopped: boolean } | null = null
let registrationConfirmation = 0
const registrationNotice = '批量登记成功后 Redfish 服务器立即由后台采集，并每 5 分钟持续监控；登记成功不代表在线或采集成功。IPMI 暂不支持自动采集。'
const registrationSummary = computed(() => ({
  success: registrationResults.value.filter(item => item.status === 'success').length,
  failed: registrationResults.value.filter(item => item.status === 'failed').length,
  unknown: registrationResults.value.filter(item => item.status === 'unknown').length,
  stopped: registrationResults.value.filter(item => item.status === 'stopped').length,
  pending: registrationResults.value.filter(item => ['queued', 'pending'].includes(item.status)).length
}))
const registrationColumns: DataTableColumns<RegistrationResult> = [
  { title: 'BMC 地址', key: 'address' },
  { title: '登记结果', key: 'status', render: row => ({ queued: '等待登记', pending: '登记中', success: '成功', failed: '失败', unknown: '结果待确认', stopped: '未提交' })[row.status] },
  { title: '说明', key: 'detail' }
]
const currentId = ref<string | number | null>(null)
const form = ref<RecordData>({})

// === 高级筛选 ===
const filterStatus = ref<string | null>(null)
const filterTag = ref<string[]>([])
const filterDatacenter = ref<number | null>(null)

// === 批量操作 ===
const checkedRowKeys = ref<DataTableRowKey[]>([])
const batchLoading = ref(false)
const powerLoading = ref<string | number | null>(null)
const canOperatePower = computed(() => auth.can('bmc:operate'))
const canManageSubscriptions = computed(() => props.section.api === '/assets' && auth.systemMode === 'external_operation' && auth.can('orders:write'))
const canSelectAssets = computed(() => props.section.api === '/assets' && (canWrite.value || auth.can('assets:operate') || canOperatePower.value || canManageSubscriptions.value))
const canSubscriptionFinance = computed(() => canManageSubscriptions.value && auth.can('finance:write'))
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
function batchKey(kind: string, ids: number[]) {
  const fingerprint = `${auth.projectId}:${kind}:${ids.join(',')}`
  if (pendingBatch?.fingerprint !== fingerprint) pendingBatch = { fingerprint, key: crypto.randomUUID() }
  return pendingBatch.key
}
async function submitSelectedBatch(kind: string, ids: number[], projectId: string, cycle = '', count = 1) {
  if (batchLoading.value || auth.projectId !== projectId || !ids.length) return
  if (kind === 'power' ? !canOperatePower.value : !canManageSubscriptions.value) return
  batchLoading.value = true
  try {
    const key = batchKey(`${kind}:${cycle}:${count}`, ids)
    const response = kind === 'power' ? await batchOps.power(ids, cycle, key)
      : kind === 'renew' ? await batchOps.renew(ids, cycle, count, key) : await batchOps.unsubscribe(ids, key)
    if (auth.projectId !== projectId || disposed) return
    batchResults.value = unwrap<{ results: RecordData[] }>(response).results
    resultsShowing.value = true
    renewShowing.value = false
    pendingBatch = null
    checkedRowKeys.value = []
    await load()
  } catch (e) {
    message.error(`${errorText(e)}；未收到可靠结果时再次提交将复用本次请求键，勿切换选中项后重试。`)
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
  const ids = checkedRowKeys.value.map(Number)
  if (!canManageSubscriptions.value || batchLoading.value || !ids.length) return
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
    onPositiveClick: () => submitSelectedBatch(kind, ids, projectId, cycle, count)
  })
}

// === 机房选项列表 ===
const datacenterOptions = ref<{ label: string; value: number }[]>([])

function datacenterName(value: unknown) {
  if (value == null) return '未关联机房'
  return datacenterOptions.value.find(option => option.value === Number(value))?.label || `机房 #${value}`
}

function bmcWebUrl(row: RecordData): string | null {
  const address = String(row.bmc_address || '').trim()
  if (!address || /[\s/@?#\\]/.test(address)) return null
  const host = address.includes(':') && !address.startsWith('[') ? `[${address}]` : address
  const port = row.bmc_protocol === 'redfish' ? Number(row.bmc_port || 443) : 443
  if (!Number.isInteger(port) || port < 1 || port > 65535) return null
  try {
    return new URL(`https://${host}:${port}/`).href
  } catch {
    return null
  }
}

function assetMenuOptions(row: RecordData): DropdownOption[] {
  const options: DropdownOption[] = [
    { label: '详情', key: 'detail' },
    { label: '打开 BMC 界面', key: 'open-bmc', disabled: !bmcWebUrl(row) }
  ]
  if (auth.can('assets:write')) {
    options.push({ label: '重装（暂不可用）', key: 'reinstall', disabled: true })
    options.push({ label: '编辑', key: 'edit' })
    options.push({ label: '删除', key: 'delete' })
  }
  return options
}

function handleAssetMenu(key: string, row: RecordData) {
  if (key === 'open-bmc') {
    const url = bmcWebUrl(row)
    if (url) window.open(url, '_blank', 'noopener,noreferrer')
    return
  }
  if (key === 'detail') router.push(`/assets/${row.id}`)
  if (key === 'edit') openEdit(row)
  if (key === 'delete') {
    dialog.warning({
      title: '确认删除',
      content: '确认删除这条资产记录？此操作不可撤销。',
      positiveText: '确认删除',
      negativeText: '取消',
      onPositiveClick: () => remove(row)
    })
  }
}

async function operateAssetPower(row: RecordData) {
  if (powerLoading.value != null || !canOperatePower.value) return
  const action = powerAction(row)
  const label = powerActionLabel(row)
  dialog.warning({
    title: `确认${label}`,
    content: `将对 ${row.name || row.id} 执行${label}操作。该操作可能影响业务，请确认目标资产及影响范围。`,
    positiveText: '确认执行',
    negativeText: '取消',
    onPositiveClick: async () => {
      if (powerLoading.value != null || !canOperatePower.value) return
      powerLoading.value = row.id ?? null
      try {
        await api.post(`/assets/${encodeURIComponent(String(row.id))}/actions`, { kind: 'power', action })
        message.success('操作已提交')
        await load()
      } catch (e) {
        message.error(errorText(e))
      } finally {
        powerLoading.value = null
      }
    }
  })
}

// === 标签选项列表 ===
const tagOptions = ref<{ label: string; value: string }[]>([])

// === 状态选项列表 ===
// 非资产类 section 若目录声明了 status 枚举，筛选项直接取自枚举，避免使用与后端不符的占位状态
const sectionStatusOptions = computed(() => {
  const field = props.section.fields.find(item => item.key === 'status' && item.type === 'select' && item.options?.length)
  if (!field?.options) return null
  return field.options.map(value => ({ label: enumLabelApis.includes(props.section.api) ? enumLabel('status', value) : String(value), value: String(value) }))
})
const statusOptions = computed(() => props.section.api === '/assets'
  ? Object.entries(assetStatusLabels).map(([value, label]) => ({ label, value }))
  : props.section.api === '/tasks' ? ['pending', 'running', 'retry_wait', 'succeeded', 'failed', 'cancelled', 'timeout', 'unknown'].map(value => ({ label: value, value })) : sectionStatusOptions.value ?? [
  { label: '在线', value: 'online' },
  { label: '离线', value: 'offline' },
  { label: '活跃', value: 'active' },
  { label: '就绪', value: 'ready' },
  { label: '故障', value: 'error' },
  { label: '维护中', value: 'maintenance' }
])

const canWrite = computed(() =>
  (['/projects', '/tenants'].includes(props.section.api)
    ? auth.can('projects:write')
    : auth.can(props.section.permission.replace(':read', ':write'))) &&
  ['/tenants', '/projects', '/assets', '/networks', '/ip-allocations', '/tags', '/ip-pools', '/images', '/pxe-configs', '/install-plans', '/firmware-packages', '/firmware-baselines', '/image-templates', '/partition-templates', '/post-install-scripts', '/products'].includes(props.section.api)
)

// === 列定义（含排序） ===
const columns = computed<DataTableColumns<RecordData>>(() => {
  const useEnumLabels = enumLabelApis.includes(props.section.api)
  const fields = (props.section.columns || [
    ...props.section.fields.map(f => f.key).filter(key => !key.includes('password')),
    'status',
    'created_at'
  ])
  const cols: DataTableColumns<RecordData> = fields.filter(key => !/password|secret/i.test(key)).map(key => ({
    // 列标题优先取本 section 的字段标签：全局 labels 里存在同名字段（如 method 在折旧语境下为「折旧方式」），
    // 会让支付记录的「支付方式」列显示错误文案
    title: props.section.fields.find(f => f.key === key)?.label || labels[key] || key,
    key,
    minWidth: 125,
    sorter: (a: RecordData, b: RecordData) => {
      const va = key === 'datacenter_id' ? datacenterName(a[key]) : key === 'status' && props.section.api === '/assets' ? assetStatus(a) : a[key]
      const vb = key === 'datacenter_id' ? datacenterName(b[key]) : key === 'status' && props.section.api === '/assets' ? assetStatus(b) : b[key]
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
            { size: 'small', bordered: false, type: key === 'status' && props.section.api === '/assets' && assetStatus(row) === 'running' ? 'success' : tagType(key === 'status' && props.section.api === '/assets' ? assetStatus(row) : row[key]) },
            { default: () => key === 'status' && props.section.api === '/assets' ? assetStatusLabels[assetStatus(row)] || '状态未知' : (useEnumLabels ? enumLabel(key, row[key]) : String(row[key] ?? '—')) }
          )
        : key === 'datacenter_id' ? datacenterName(row[key])
          : key === 'created_at' && props.section.api === '/assets' ? formatCreatedAt(row[key])
          : useEnumLabels ? enumLabel(key, row[key]) : String(row[key] ?? '—')
  }))
  if (
    props.section.api === '/assets' ||
    props.section.api === '/images' ||
    props.section.api === '/alerts' || (canWrite.value && !['/projects', '/tenants'].includes(props.section.api))
  ) {
    cols.push({
      title: '操作',
      key: 'actions',
      width: 175,
      fixed: 'right',
      render: row => props.section.api === '/assets'
        ? h('div', { class: 'table-actions' }, [
            canOperatePower.value
              ? h(
                  NButton,
                  { text: true, type: powerAction(row) === 'off' ? 'warning' : 'success', disabled: powerLoading.value === row.id, loading: powerLoading.value === row.id, onClick: () => operateAssetPower(row) },
                  { default: () => powerActionLabel(row) }
                )
              : null,
            h(
              NDropdown,
              { trigger: 'click', options: assetMenuOptions(row), onSelect: (key: string) => handleAssetMenu(key, row) },
              { default: () => h(NButton, { text: true, 'aria-label': '更多操作' }, { default: () => '⋯' }) }
            )
          ])
        : h('div', { class: 'table-actions' }, [
            props.section.api === '/alert-channels' && auth.can('alerts:write')
              ? h(NButton, { text: true, loading: testingChannel.value === row.id, disabled: testingChannel.value != null, onClick: () => testChannel(row) }, { default: () => '发送测试' })
              : null,
            props.section.api === '/alerts' && !row.resolved && auth.can('alerts:write')
              ? h(NButton, { text: true, type: 'primary', onClick: () => resolveAlert(row) }, { default: () => '处理' })
              : null,
            canWrite.value && !props.section.readonly
              ? h(NButton, { text: true, onClick: () => openEdit(row) }, { default: () => '编辑' })
              : null,
            canWrite.value && !props.section.readonly
              ? h(
                  NPopconfirm,
                  { onPositiveClick: () => remove(row) },
                  { trigger: () => h(NButton, { text: true, type: 'error' }, { default: () => '删除' }), default: () => '确认删除这条记录？' }
                )
              : null
          ])
    })
  }
  if (canSelectAssets.value) cols.unshift({ type: 'selection', disabled: () => batchLoading.value })
  return cols
})

function tagType(value: unknown): 'success' | 'warning' | 'error' | 'info' | 'default' {
  const s = String(value || '').toLowerCase()
  return ['active', 'online', 'ready', 'succeeded', 'healthy', 'in_use'].includes(s)
    ? 'success'
    : ['failed', 'timeout', 'error', 'critical', 'offline'].includes(s)
      ? 'error'
      : ['pending', 'retry_wait', 'warning', 'running', 'managing', 'collecting', 'starting', 'stopping', 'restarting', 'reinstalling', 'upgrading', 'queued', 'maintenance'].includes(s)
        ? 'warning'
        : 'default'
}

// === 后端分页加载 ===
let refreshTimer: ReturnType<typeof setTimeout> | undefined
let loadSequence = 0
let disposed = false
onBeforeUnmount(() => {
  disposed = true
  loadSequence++
  clearTimeout(refreshTimer)
})

async function load(silent = false) {
  const sequence = ++loadSequence
  clearTimeout(refreshTimer)
  let loaded = false
  if (!['/projects', '/tenants'].includes(props.section.api) && !auth.projectId) {
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
    // 构建查询参数
    const params: Record<string, unknown> = {
      page: page.value,
      page_size: pageSize.value
    }
    if (props.section.api === '/tasks' && route.path === '/installations') params.kind = 'reinstall'
    if (props.section.api === '/projects') params.tenant_id = auth.tenantId
    if (search.value) params.search = search.value
    if (props.section.api === '/assets') params.search_field = searchField.value
    if (filterStatus.value) params.status = filterStatus.value
    if (filterTag.value.length) params.tags = filterTag.value.join(',')
    if (filterDatacenter.value) params.datacenter_id = filterDatacenter.value

    const data = rows(await resource.list(props.section.api, params))
    if (sequence !== loadSequence || disposed) return
    const scoped = props.section.api === '/tasks' && ['/installations', '/firmware'].includes(route.path)
      ? data.items.filter(
          item => item.kind === (route.path === '/installations' ? 'reinstall' : 'firmware')
        )
      : data.items
    items.value = scoped
    total.value = data.total
    loaded = true
  } catch (e) {
    if (sequence !== loadSequence || disposed) return
    loadError.value = errorText(e)
    if (!silent) message.error(loadError.value)
  } finally {
    if (sequence === loadSequence && !disposed) {
      loading.value = false
      if (loaded && props.section.api === '/assets' && auth.projectId) {
        refreshTimer = setTimeout(() => load(true), 10000)
      }
    }
  }
}

// === 加载筛选选项 ===
async function loadFilterOptions() {
  if (props.section.api !== '/assets') return
  try {
    // 加载机房列表
    const dcResult = rows(await resource.list('/datacenters', { page_size: 200 }))
    datacenterOptions.value = dcResult.items.map(dc => ({
      label: String(dc.name || dc.id),
      value: Number(dc.id)
    }))
  } catch (e) {
    message.warning(`机房选项加载失败：${errorText(e)}`)
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
  [() => props.section, () => auth.projectId, () => auth.tenantId],
  () => {
    page.value = 1
    search.value = ''
    searchField.value = 'auto'
    filterStatus.value = null
    filterTag.value = []
    filterDatacenter.value = null
    checkedRowKeys.value = []
    batchResults.value = []
    resultsShowing.value = false
    renewShowing.value = false
    pendingBatch = null
    datacenterOptions.value = []
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
  form.value = row ? { ...row, bmc_secret: undefined } : {}
  if (props.section.api === '/assets') {
    form.value.datacenter_id = row?.datacenter_id == null ? null : Number(row.datacenter_id)
  }
  for (const field of props.section.fields) {
    if (field.type === 'json' && row?.[field.key] != null) form.value[field.key] = JSON.stringify(row[field.key], null, 2)
  }
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

function registrationAddresses(cidr: string): string[] {
  const match = /^(\d{1,3})\.(\d{1,3})\.(\d{1,3})\.(\d{1,3})\/(\d{1,2})$/.exec(cidr.trim())
  if (!match) throw new Error('请输入有效的 BMC IPv4 CIDR，例如 192.168.1.0/24')
  const octets = match.slice(1, 5).map(Number)
  const prefix = Number(match[5])
  if (octets.some(n => n > 255) || match.slice(1, 5).some(part => part.length > 1 && part.startsWith('0')) || prefix > 32) throw new Error('IPv4 地址或前缀长度无效（地址不允许前导零）')
  const size = 2 ** (32 - prefix)
  const count = prefix <= 30 ? size - 2 : size
  if (count > 256) throw new Error('每次最多登记 256 个地址，请缩小网段')
  const ip = octets.reduce((value, n) => value * 256 + n, 0)
  const network = Math.floor(ip / size) * size
  const start = network + (prefix <= 30 ? 1 : 0)
  const addresses: string[] = []
  for (let n = start; n < start + count; n++) {
    const first = Math.floor(n / 2 ** 24)
    if (n === 0 || first === 127 || (first >= 224 && first <= 239)) {
      throw new Error('网段包含不可登记的地址：不允许未指定、环回或组播地址')
    }
    addresses.push([first, Math.floor(n / 65536) % 256, Math.floor(n / 256) % 256, n % 256].join('.'))
  }
  return addresses
}

function stopRegistration() {
  registrationConfirmation++
  if (registrationRun) registrationRun.stopped = true
  batchForm.value.password = ''
  batchConfirming.value = false
  for (const item of registrationResults.value) {
    if (item.status === 'queued') {
      item.status = 'stopped'
      item.detail = '上下文已变化，未提交'
    } else if (item.status === 'pending') {
      item.status = 'unknown'
      item.detail = '请求已发出，请在原项目核对资产列表，勿直接重试'
    }
  }
}

function closeBatchAdding(show: boolean) {
  if (batchSubmitting.value) return
  batchAdding.value = show
  if (!show) stopRegistration()
}

watch([() => auth.projectId, () => auth.tenantId, () => props.section.api], () => {
  stopRegistration()
  batchAdding.value = false
  batchForm.value.datacenterId = null
}, { flush: 'sync' })
onBeforeUnmount(stopRegistration)

function confirmBatchAdd() {
  if (batchSubmitting.value || batchConfirming.value || registrationResults.value.length) return
  if (!auth.projectId || !auth.can('assets:write')) return
  let addresses: string[]
  try {
    addresses = registrationAddresses(batchForm.value.cidr)
    if (!batchForm.value.username.trim() || !batchForm.value.password.trim()) throw new Error('请填写 BMC 用户名和密码')
    if (!['redfish', 'ipmi'].includes(batchForm.value.protocol)) throw new Error('请选择有效协议')
    const port = batchForm.value.port
    if (port == null || !Number.isInteger(port) || port < 1 || port > 65535) throw new Error('端口必须为 1–65535 的整数')
  } catch (e) {
    message.warning(errorText(e))
    return
  }
  const projectId = auth.projectId
  const tenantId = auth.tenantId
  const confirmation = ++registrationConfirmation
  batchConfirming.value = true
  dialog.warning({
    title: '确认批量添加',
    content: `${addresses[0]} 至 ${addresses[addresses.length - 1]}，共 ${addresses.length} 个待登记地址。${registrationNotice}。仅创建新记录，不覆盖已有资产，不自动重试。`,
    positiveText: '确认登记',
    negativeText: '取消',
    onPositiveClick: () => {
      if (confirmation !== registrationConfirmation || !batchConfirming.value || !batchAdding.value || auth.projectId !== projectId || auth.tenantId !== tenantId) return
      batchConfirming.value = false
      void registerAssets(addresses, projectId, tenantId)
    },
    onAfterLeave: () => { batchConfirming.value = false }
  })
}

async function registerAssets(addresses: string[], projectId: string, tenantId: string) {
  if (batchSubmitting.value || !auth.can('assets:write')) return
  const run = { stopped: false }
  registrationRun = run
  batchSubmitting.value = true
  const { username, protocol, port, prefix, datacenterId } = batchForm.value
  registrationResults.value = addresses.map(address => ({ address, status: 'queued', detail: '' }))
  let next = 0
  const active = () => !run.stopped && auth.projectId === projectId && auth.tenantId === tenantId && auth.can('assets:write')
  async function worker() {
    while (active() && next < addresses.length) {
      const item = registrationResults.value[next++]!
      item.status = 'pending'
      try {
        await resource.create('/assets', {
          name: `${prefix.trim()}${item.address}`,
          bmc_address: item.address,
          bmc_user: username.trim(),
          bmc_secret: batchForm.value.password,
          bmc_protocol: protocol,
          bmc_port: port,
          datacenter_id: datacenterId
        }, { projectId, sensitive: true })
        if (active()) {
          item.status = 'success'
          item.detail = '已登记，未验证在线状态或凭据'
        }
      } catch (e) {
        if (active()) {
          item.status = e instanceof AssetRegistrationError && e.outcome === 'failed' ? 'failed' : 'unknown'
          item.detail = item.status === 'failed' ? '服务器拒绝登记，请检查权限、重复地址或字段约束' : '未收到可靠结果，请核对资产列表，勿直接重试'
        }
      }
    }
  }
  try {
    await Promise.all(Array.from({ length: Math.min(4, addresses.length) }, () => worker()))
  } finally {
    const shouldReload = !run.stopped && auth.projectId === projectId && auth.tenantId === tenantId && props.section.api === '/assets'
    stopRegistration()
    batchSubmitting.value = false
    registrationRun = null
    if (shouldReload) load()
  }
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

const testingChannel = ref<unknown>(null)
async function testChannel(row: RecordData) {
  testingChannel.value = row.id
  try {
    const response = await api.post(`/alert-channels/${row.id}/test`)
    message.success(response.data?.data?.message || '服务端已接受测试消息，不保证实际收信')
  } catch (e) {
    message.error(errorText(e))
  } finally {
    testingChannel.value = null
    load()
  }
}

async function save() {
  for (const field of props.section.fields) {
    if (field.required && !(currentId.value != null && field.type === 'password') && (form.value[field.key] == null || form.value[field.key] === '')) {
      message.warning(`请填写${field.label}`)
      return
    }
    // 格式验证
    const val = String(form.value[field.key] || '')
    if (field.key === 'bmc_address' || field.key === 'management_ip') {
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
        .map(f => [f.key, f.type === 'json' && typeof form.value[f.key] === 'string' ? JSON.parse(String(form.value[f.key])) : form.value[f.key]])
    )
    if (props.section.api === '/assets') {
      payload.datacenter_id = form.value.datacenter_id == null ? null : Number(form.value.datacenter_id)
    }
    if (props.section.api === '/projects') {
      if (!auth.tenantId) throw new Error('请先选择租户')
      payload.tenant_id = Number(auth.tenantId)
    }
    if (currentId.value != null) {
      await resource.update(props.section.api, currentId.value, payload)
    } else {
      await resource.create(props.section.api, payload)
    }
    message.success('保存成功')
    if (['/projects', '/tenants'].includes(props.section.api)) await auth.loadContext()
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

function openBatchAdding() {
  if (batchSubmitting.value) return
  registrationResults.value = []
  batchForm.value.password = ''
  batchAdding.value = true
}

// === Excel 导出 ===
async function handleExportExcel() {
  try {
    const fields = props.section.columns || props.section.fields.map(f => f.key)
    const exportItems = props.section.api === '/assets'
      ? items.value.map(row => ({ ...row, status: assetStatusLabels[assetStatus(row)] || '状态未知' }))
      : items.value
    await exportToExcel(exportItems, fields.filter(k => !k.includes('password') && !k.includes('secret')), props.section.title)
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

function collectSelectedAssets() {
  if (batchLoading.value || !checkedRowKeys.value.length || !auth.can('assets:operate')) return
  const ids = checkedRowKeys.value.map(Number)
  const projectId = auth.projectId
  dialog.warning({
    title: '确认采集已纳管资产',
    content: `将对选中的 ${ids.length} 台已纳管资产执行 BMC 信息采集，此操作独立于批量登记。`,
    positiveText: '确认采集',
    negativeText: '取消',
    onPositiveClick: async () => {
      if (auth.projectId !== projectId || !auth.can('assets:operate') || batchLoading.value) return
      batchLoading.value = true
      try {
        await batchOps.scan(ids)
        message.success('采集任务已提交')
      } catch (e) {
        message.error(errorText(e))
      } finally {
        batchLoading.value = false
      }
    }
  })
}

// === 批量电源操作（仅资产页） ===
function handleBatchPower(action: string) {
  if (batchLoading.value || !canOperatePower.value || !checkedRowKeys.value.length) return
  const ids = checkedRowKeys.value.map(Number)
  if (ids.length > 500) { message.warning('电源批量操作每次最多500台'); return }
  const projectId = auth.projectId
  const actionLabel = action === 'on' ? '开机' : action === 'off' ? '关机' : '重启'
  dialog.warning({
    title: `确认批量${actionLabel}`,
    content: `将对 ${ids.length} 台资产逐台提交${actionLabel}任务，可能影响业务。提交成功不代表 BMC 执行成功，请在任务中心核对。`,
    positiveText: '确认执行', negativeText: '取消',
    onPositiveClick: () => submitSelectedBatch('power', ids, projectId, action)
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
        <n-button
          v-if="section.api === '/assets' && auth.can('assets:write')"
          :disabled="batchSubmitting || !auth.projectId"
          @click="openBatchAdding"
        >
          批量添加
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
          <p class="panel-caption">{{ section.readonly ? '只读视图：后端未提供单条增删改路由，请在对应业务流程中操作' : '筛选、查看当前资源与操作记录' }}</p>
        </div>
        <div v-if="section.api === '/assets'" class="asset-filter-toolbar">
          <div class="asset-field-search">
            <n-select
              v-model:value="searchField"
              class="asset-search-field"
              :options="assetSearchOptions"
              aria-label="搜索字段"
            />
            <n-input
              v-model:value="search"
              class="asset-search-input"
              placeholder="输入搜索内容"
              clearable
              @keyup.enter="page = 1; load()"
              @clear="page = 1; load()"
            />
            <n-button type="primary" :loading="loading" @click="page = 1; load()">搜索</n-button>
          </div>
          <div class="asset-filter-options">
            <n-select v-model:value="filterStatus" placeholder="服务器状态" :options="statusOptions" clearable aria-label="服务器状态" />
            <n-select v-model:value="filterDatacenter" placeholder="全部机房" :options="datacenterOptions" clearable filterable aria-label="机房" />
            <n-select v-model:value="filterTag" placeholder="全部标签" :options="tagOptions" multiple clearable aria-label="标签" />
            <n-button quaternary :disabled="loading" @click="load()">刷新</n-button>
          </div>
        </div>
        <div v-else class="toolbar-actions resource-actions">
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
            <n-button quaternary @click="load()">刷新</n-button>
          </div>
        </div>
      </div>

      <!-- 批量操作栏 -->
      <div v-if="checkedRowKeys.length" class="batch-action-bar" style="margin: 12px 12px 0;">
        <span class="batch-count">已选 {{ checkedRowKeys.length }} 项</span>
        <n-button
          v-if="section.api === '/assets' && auth.can('assets:operate')"
          size="small"
          :disabled="batchLoading"
          :loading="batchLoading"
          @click="collectSelectedAssets"
        >
          采集已选资产
        </n-button>
        <n-button v-if="canWrite && auth.systemMode !== 'external_operation'" size="small" type="error" :disabled="batchLoading" :loading="batchLoading" @click="handleBatchDelete">
          批量删除
        </n-button>
        <n-button
          v-if="section.api === '/assets' && canOperatePower"
          :disabled="batchLoading"
          size="small"
          type="success"
          :loading="batchLoading"
          @click="handleBatchPower('on')"
        >
          批量开机
        </n-button>
        <n-button
          v-if="section.api === '/assets' && canOperatePower"
          :disabled="batchLoading"
          size="small"
          type="warning"
          :loading="batchLoading"
          @click="handleBatchPower('restart')"
        >
          批量重启
        </n-button>
        <n-button
          v-if="section.api === '/assets' && canOperatePower"
          :disabled="batchLoading"
          size="small"
          :loading="batchLoading"
          @click="handleBatchPower('off')"
        >
          批量关机
        </n-button>
        <n-button v-if="canManageSubscriptions" size="small" type="primary" :disabled="batchLoading" @click="renewShowing = true">批量续费</n-button>
        <n-button v-if="canSubscriptionFinance" size="small" type="error" :disabled="batchLoading" @click="confirmSubscriptions('unsubscribe')">立即退订</n-button>
        <n-button size="small" quaternary :disabled="batchLoading" @click="checkedRowKeys = []">
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
        :checked-row-keys="checkedRowKeys"
        @update:checked-row-keys="(keys: DataTableRowKey[]) => { if (!batchLoading) checkedRowKeys = keys }"
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
        {{ loadError ? `加载失败：${loadError}` : '当前没有数据，或尚未创建相关资源。' }}
      </div>
    </div>
    <n-modal v-model:show="renewShowing" preset="card" title="批量续费" style="width: min(460px, 94vw)">
      <p>已选 {{ checkedRowKeys.length }} 台服务器。逐台生成待支付订单；不会自动支付或直接修改到期时间。</p>
      <n-form label-placement="top">
        <n-form-item label="续费周期"><n-select v-model:value="renewCycle" :options="cycleOptions" :disabled="batchLoading" /></n-form-item>
        <n-form-item label="周期数量"><n-input-number v-model:value="renewCount" :min="1" :max="36" :precision="0" :disabled="batchLoading" /></n-form-item>
      </n-form>
      <n-button type="primary" :loading="batchLoading" :disabled="batchLoading || !renewCount" @click="confirmSubscriptions('renew')">确认续费方案</n-button>
    </n-modal>
    <n-modal v-model:show="resultsShowing" preset="card" title="逐台操作结果" style="width: min(1050px, 96vw)">
      <n-button v-if="canSubscriptionFinance && batchResults.some(row => row.status === 'pending' && row.order_id)" type="primary" :loading="batchLoading" :disabled="batchLoading" @click="confirmPayment">确认余额支付续费订单</n-button>
      <p>共 {{ batchResults.length }} 台；失败 {{ batchResults.filter(row => row.status === 'failed').length }} 台。电源与退订关机均异步执行，请根据任务 ID 核对真实结果；待回收资产需要人工处理。</p>
      <n-data-table :columns="resultColumns" :data="batchResults" :row-key="row => row.asset_id" :scroll-x="850" :pagination="{ pageSize: 20 }" />
    </n-modal>
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
            :required="field.required && !(currentId != null && field.type === 'password')"
            :class="{ 'form-item-wide': field.type === 'textarea' || field.key === 'description' }"
          >
            <n-select
              v-if="field.type === 'select'"
              v-model:value="form[field.key] as string | number | null"
              :options="field.key === 'datacenter_id' ? datacenterOptions : field.options?.map(v => ({ label: v, value: v }))"
              :clearable="field.key === 'datacenter_id' && !field.required"
              :filterable="field.key === 'datacenter_id'"
              :placeholder="field.key === 'datacenter_id' ? '请选择机房（可留空）' : `请选择${field.label}`"
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
                  : field.type === 'textarea' || field.type === 'json'
                    ? 'textarea'
                    : 'text'
              "
              :placeholder="currentId != null && field.type === 'password' ? '留空保留现有密钥' : `请输入${field.label}`"
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
      :show="batchAdding"
      @update:show="closeBatchAdding"
      preset="card"
      title="批量添加服务器"
      :closable="!batchSubmitting"
      :mask-closable="!batchSubmitting"
      :close-on-esc="!batchSubmitting"
      style="width: min(760px, 94vw)"
    >
      <p role="note">{{ registrationNotice }}。</p>
      <p>每次最多 256 个地址。/30 及以下排除网络和广播地址，/31、/32 全部登记；仅新建，不覆盖已有资产，不自动重试。</p>
      <n-form label-placement="top" :disabled="batchSubmitting || batchConfirming || !!registrationResults.length">
        <div class="resource-form-grid">
          <n-form-item label="BMC IPv4 CIDR 网段" required class="form-item-wide">
            <n-input v-model:value="batchForm.cidr" placeholder="例如 192.168.1.0/24" />
          </n-form-item>
          <n-form-item label="BMC 用户名" required>
            <n-input v-model:value="batchForm.username" autocomplete="off" />
          </n-form-item>
          <n-form-item label="BMC 密码" required>
            <n-input v-model:value="batchForm.password" type="password" autocomplete="new-password" />
          </n-form-item>
          <n-form-item label="协议" required>
            <n-select v-model:value="batchForm.protocol" :options="[{ label: 'Redfish', value: 'redfish' }, { label: 'IPMI', value: 'ipmi' }]" @update:value="value => { batchForm.port = value === 'ipmi' ? 623 : 443 }" />
          </n-form-item>
          <n-form-item label="端口" required>
            <n-input-number v-model:value="batchForm.port" :min="1" :max="65535" :precision="0" style="width: 100%" />
          </n-form-item>
          <n-form-item label="命名前缀（可留空）">
            <n-input v-model:value="batchForm.prefix" placeholder="名称为前缀 + IP 地址，例如 server-" />
          </n-form-item>
          <n-form-item label="所属机房（可留空）">
            <n-select v-model:value="batchForm.datacenterId" :options="datacenterOptions" clearable filterable placeholder="未关联机房" />
          </n-form-item>
        </div>
      </n-form>
      <div v-if="registrationResults.length" aria-live="polite">
        <p>共 {{ registrationResults.length }} 项：成功 {{ registrationSummary.success }}，失败 {{ registrationSummary.failed }}，结果待确认 {{ registrationSummary.unknown }}，未提交 {{ registrationSummary.stopped }}，处理中 {{ registrationSummary.pending }}。</p>
        <p>结果待确认的地址可能已经保存，请在原项目资产列表核对后再决定是否重新登记。</p>
        <n-data-table :columns="registrationColumns" :data="registrationResults" :row-key="row => row.address" :pagination="{ pageSize: 10 }" :max-height="300" />
      </div>
      <div class="modal-actions">
        <n-button :disabled="batchSubmitting" @click="closeBatchAdding(false)">{{ registrationResults.length ? '关闭' : '取消' }}</n-button>
        <n-button v-if="!registrationResults.length" type="primary" :disabled="batchSubmitting || batchConfirming" :loading="batchSubmitting" @click="confirmBatchAdd">
          确认批量添加
        </n-button>
      </div>
    </n-modal>
  </div>
</template>

<style scoped>
.asset-filter-toolbar {
  display: flex;
  flex: 1;
  flex-wrap: wrap;
  justify-content: flex-end;
  gap: 12px;
  min-width: 0;
}
.asset-field-search {
  display: flex;
  flex: 1 1 420px;
  max-width: 620px;
  min-width: 0;
}
.asset-search-field {
  width: 150px;
  flex-shrink: 0;
}
.asset-search-input {
  flex: 1;
  min-width: 100px;
  margin-left: -1px;
}
.asset-field-search :deep(.n-base-selection),
.asset-field-search :deep(.n-input),
.asset-field-search :deep(.n-button) {
  border-radius: 0;
}
.asset-filter-options {
  display: flex;
  flex: 1 1 450px;
  align-items: center;
  justify-content: flex-end;
  gap: 8px;
}
.asset-filter-options > .n-select {
  flex: 1;
  min-width: 130px;
  max-width: 200px;
}
@media (max-width: 780px) {
  .asset-filter-toolbar { width: 100%; }
  .asset-field-search { flex-basis: 100%; max-width: none; }
  .asset-search-field { width: 125px; }
  .asset-filter-options { flex-wrap: wrap; justify-content: flex-start; }
  .asset-filter-options > .n-select { max-width: none; }
}
</style>
