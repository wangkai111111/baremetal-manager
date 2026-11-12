<script setup lang="ts">
import { computed, h, nextTick, onMounted, onUnmounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import dayjs from 'dayjs'
import * as echarts from 'echarts'
import {
  NButton,
  NCard,
  NCheckbox,
  NCheckboxGroup,
  NDataTable,
  NDatePicker,
  NDescriptions,
  NDescriptionsItem,
  NDropdown,
  NForm,
  NFormItem,
  NInput,
  NModal,
  NSelect,
  NTabPane,
  NTabs,
  NTag,
  useDialog,
  useMessage,
  type DataTableColumns,
  type DropdownOption,
  type SelectOption
} from 'naive-ui'
import { api, errorText, resource, rows, taskStatusType, unwrap, type RecordData } from '../api'
import { labels } from '../catalog'
import { useAuth } from '../stores/auth'

const route = useRoute()
const router = useRouter()
const message = useMessage()
const dialog = useDialog()
const auth = useAuth()

const assetId = computed(() => String(route.params.id))

// ============================================================
// 通用取值助手：后端以 jsonb 透传，字段类型不确定，统一在此收敛
// ============================================================
const str = (value: unknown) => (value == null ? '' : String(value))
const numOf = (value: unknown) => Number(value)
const dash = (value: unknown) => (value == null || value === '' ? '—' : String(value))
const fmtTime = (value: unknown) => {
  const text = str(value)
  if (!text) return '—'
  const at = dayjs(text)
  return at.isValid() ? at.format('YYYY-MM-DD HH:mm:ss') : text
}
const fmtDate = (value: unknown) => {
  const text = str(value)
  if (!text) return '—'
  const at = dayjs(text)
  return at.isValid() ? at.format('YYYY-MM-DD') : text
}

// ============================================================
// 状态语义：与服务器列表共用同一套 display_status 文案 / 色调
// ============================================================
type StatusMeta = { label: string; tone: 'running' | 'stopped' | 'fault' | 'maintenance'; tag: 'success' | 'warning' | 'error' | 'info' | 'default' }
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

const kindLabels: Record<string, string> = {
  power_on: '开机', power_off: '关机', power_cycle: '电源循环', reset: '重置',
  collect: '硬件采集', install: '装机', firmware_upgrade: '固件升级', boot_order: '启动顺序', discover: '设备发现'
}
const opStatusLabels: Record<string, string> = {
  pending: '排队中', running: '执行中', retry_wait: '等待重试', succeeded: '成功',
  failed: '失败', cancelled: '已取消', timeout: '超时', unknown: '结果未知'
}
const kindLabel = (value: unknown) => kindLabels[str(value)] || str(value) || '—'
const opStatusLabel = (value: unknown) => opStatusLabels[str(value)] || str(value) || '—'

// ============================================================
// 主数据
// ============================================================
const asset = ref<RecordData>({})
const loading = ref(false)
const loadError = ref('')
const activeTab = ref<'overview' | 'monitor' | 'hardware' | 'network' | 'logs' | 'console'>('overview')

const collection = ref<RecordData>({})
const collectionLoading = ref(false)
const collectionNotice = ref('')
let collectionGeneration = 0
let collectionTimer: ReturnType<typeof setTimeout> | undefined
let collectionController: AbortController | undefined

const operations = ref<RecordData[]>([])
const operationsLoading = ref(false)
const operationsTruncated = ref(false)
const opPage = ref(1)
const opPageSize = ref(20)
const opKindFilter = ref<string | null>(null)
const opStatusFilter = ref<string | null>(null)

const sensorRows = ref<RecordData[]>([])
const sensorLoading = ref(false)
const sensorError = ref('')
const ledgerHardware = ref<RecordData[]>([])
const ipAllocations = ref<RecordData[]>([])
const networkRows = ref<RecordData[]>([])
const networkLoading = ref(false)
const solSessions = ref<RecordData[]>([])
const solLoading = ref(false)
const tagCatalog = ref<RecordData[]>([])
const tagBindings = ref<RecordData[]>([])
const memberRows = ref<RecordData[]>([])
const datacenterRows = ref<RecordData[]>([])
const departmentRows = ref<RecordData[]>([])

// 监控 Tab 状态
const monitorRange = ref<'1h' | '6h' | '24h' | '7d' | '30d' | 'custom'>('24h')
const customRange = ref<[number, number] | null>(null)
const showMetrics = ref<Record<string, boolean>>({ cpu: true, memory: true, disk: true, network: true })
const monitorChartRef = ref<HTMLElement>()
const cpuTempRef = ref<HTMLElement>()
const fanRef = ref<HTMLElement>()
const powerRef = ref<HTMLElement>()
let monitorChart: echarts.ECharts | null = null
let gaugeCharts: Array<echarts.ECharts | null> = [null, null, null]

// ============================================================
// 采集快照：组件与采集状态
// ============================================================
function componentsOf(group: string): RecordData[] {
  const source = (collection.value.components || {}) as Record<string, unknown>
  return Array.isArray(source[group]) ? (source[group] as RecordData[]) : []
}

function cancelCollection() {
  collectionGeneration++
  clearTimeout(collectionTimer)
  collectionController?.abort()
  collectionLoading.value = false
}

async function loadCollection(generation = collectionGeneration) {
  const controller = new AbortController()
  collectionController = controller
  const data = unwrap<RecordData>(await api.get(`/assets/${encodeURIComponent(assetId.value)}/collection`, { signal: controller.signal }))
  if (generation === collectionGeneration) collection.value = data
}

// 采集为异步操作：提交后按快照时间/操作编号轮询确认，最长约 2 分钟
async function collectHardware() {
  if (collection.value.supported !== true || collectionLoading.value) return
  cancelCollection()
  const generation = collectionGeneration
  const previous = collection.value.collected_at
  collectionLoading.value = true
  collectionNotice.value = ''
  try {
    collectionController = new AbortController()
    const operation = unwrap<RecordData>(await api.post(`/assets/${encodeURIComponent(assetId.value)}/collect`, undefined, { signal: collectionController.signal }))
    if (generation !== collectionGeneration) return
    collectionNotice.value = '采集请求已受理（受理不代表成功），正在等待快照更新。'
    let attempts = 0
    const refresh = async () => {
      if (generation !== collectionGeneration) return
      try {
        await loadCollection(generation)
        if (generation !== collectionGeneration) return
        attempts++
        const finished = String(collection.value.operation_id) === String(operation.id) &&
          ['succeeded', 'failed', 'unknown'].includes(str(collection.value.status))
        if (finished || (collection.value.collected_at && collection.value.collected_at !== previous)) {
          collectionNotice.value = collection.value.collection_error ? str(collection.value.collection_error) : '硬件快照已更新，请查看各组件采集状态。'
          collectionLoading.value = false
          await loadAsset()
          return
        }
        if (attempts < 40) collectionTimer = setTimeout(refresh, 3000)
        else {
          collectionLoading.value = false
          collectionNotice.value = collection.value.collection_error ? str(collection.value.collection_error) : '自动刷新已结束，尚未确认采集完成，请稍后重试或查看操作日志。'
        }
      } catch (e) {
        if (generation !== collectionGeneration) return
        collectionLoading.value = false
        collectionNotice.value = errorText(e)
      }
    }
    collectionTimer = setTimeout(refresh, 3000)
  } catch (e) {
    if (generation !== collectionGeneration) return
    collectionLoading.value = false
    collectionNotice.value = errorText(e)
  }
}

const collectionStatusText = computed(() => ({
  not_collected: '尚未采集', unsupported: '当前协议不支持硬件采集', pending: '采集任务待执行',
  running: '采集中', succeeded: '采集成功', failed: '采集失败', unknown: '采集结果未确认'
} as Record<string, string>)[str(collection.value.status)] || '摘要尚未加载')

const collectionStates = computed<Record<string, RecordData>>(() => (collection.value.component_states || {}) as Record<string, RecordData>)
const failureReasons: Record<string, string> = {
  access_denied: '无读取权限', timeout: '请求超时', request_failed: '请求失败', unsafe_link: '已拒绝不安全链接',
  budget_exhausted: '达到采集预算', invalid_response: '响应无效', pagination_cycle: '分页循环', not_supported: '资源未提供'
}

// 采集态提示：空列表不能等同于「没有硬件」，必须区分「未提供 / 读取失败 / 已采集」
function stateOf(group: string) {
  const state = collectionStates.value[group]
  if (!collection.value.component_details_supported) return '快照尚无组件明细，等待下一次采集'
  const status = !state || state.status === 'not_supported' ? '设备未提供'
    : state.status === 'succeeded' ? '已采集' : state.status === 'partial' ? '部分采集' : '读取失败'
  const failures = Array.isArray(state?.failures) ? (state.failures as string[]).map(r => failureReasons[r] || '读取失败').join('、') : ''
  return failures ? `${status}（${failures}）` : status
}

// ============================================================
// 字段格式化（沿用采集快照的 Redfish 语义）
// ============================================================
const monitorLabels: Record<string, string> = {
  id: '标识', name: '名称', kind: '类型', manufacturer: '制造商', model: '型号', serial_number: '序列号',
  part_number: '部件号', health: '健康状态', state: '在位状态', cores: '物理核心', threads: '逻辑线程',
  max_speed_mhz: '最大主频', capacity_mib: '容量', capacity_bytes: '容量', media_type: '介质', protocol: '接口协议',
  raid_type: 'RAID 类型', mac: 'MAC 地址', speed_mbps: '速率', link: '链路状态', version: '版本',
  reading: '读数', units: '单位', address: 'IP 地址', network_id: '所属网络', status: '状态',
  allocated_at: '分配时间', released_at: '释放时间', category: '类别', vendor: '厂商'
}
const valueLabels: Record<string, string> = {
  On: '已开机', Off: '已关机', OK: '正常', Warning: '警告', Critical: '严重', Enabled: '启用', Disabled: '禁用',
  LinkUp: '已连接', LinkDown: '未连接', Unknown: '未知', Absent: '未安装', Present: '在位',
  PoweringOn: '开机中', PoweringOff: '关机中', allocated: '已分配', reserved: '预留', released: '已释放',
  SSD: '固态硬盘', HDD: '机械硬盘', NVMe: 'NVMe', SAS: 'SAS', SATA: 'SATA'
}
const labelOf = (key: string) => monitorLabels[key] || labels[key] || key

function humanBytes(value: unknown) {
  const size = Number(value)
  if (!Number.isFinite(size) || size <= 0) return '—'
  const units = ['B', 'KB', 'MB', 'GB', 'TB', 'PB']
  let index = 0
  let rest = size
  while (rest >= 1024 && index < units.length - 1) { rest /= 1024; index += 1 }
  return `${rest >= 100 ? rest.toFixed(0) : rest.toFixed(1)} ${units[index]}`
}

function formatField(key: string, value: unknown): string {
  if (value == null || value === '') return '—'
  if (typeof value === 'boolean') return value ? '是' : '否'
  if (typeof value === 'number') {
    if (!Number.isFinite(value)) return '—'
    if (key.includes('capacity_mib')) return `${Number((value / 1024).toFixed(2))} GiB`
    if (key.includes('capacity') || key.includes('bytes')) return humanBytes(value)
    if (key.includes('mhz')) return `${value} MHz`
    if (key.includes('mbps')) return value >= 1000 ? `${Number((value / 1000).toFixed(1))} Gbps` : `${value} Mbps`
    return String(value)
  }
  if (typeof value === 'string') return valueLabels[value] || value
  if (Array.isArray(value)) return `${value.length} 项`
  if (typeof value === 'object') {
    const parts = Object.entries(value as RecordData)
      .filter(([, item]) => item != null && item !== '' && typeof item !== 'object')
      .map(([itemKey, item]) => `${labelOf(itemKey)}: ${formatField(itemKey, item)}`)
    return parts.length ? parts.join(' · ') : '—'
  }
  return String(value)
}

const healthType = (value: unknown): 'success' | 'warning' | 'error' | 'default' =>
  str(value) === 'OK' ? 'success' : str(value) === 'Warning' ? 'warning' : str(value) === 'Critical' ? 'error' : 'default'

// ============================================================
// 数据加载
// ============================================================
async function loadAsset() {
  loading.value = true
  loadError.value = ''
  try {
    const data = unwrap<RecordData>(await resource.get('/assets', assetId.value))
    // 单条 GET 不返回计算列 display_status，用列表接口按 id 精确取回，保证与列表页状态一致
    try {
      const found = rows(await resource.list('/assets', { search: assetId.value, search_field: 'id', page_size: 100 }))
        .items.find(row => String(row.id) === assetId.value)
      if (found?.display_status) data.display_status = found.display_status
    } catch { /* 取不到计算列时退回本地派生 */ }
    asset.value = data
  } catch (e) {
    loadError.value = errorText(e)
    message.error(loadError.value)
  } finally {
    loading.value = false
  }
}

async function loadOperations() {
  operationsLoading.value = true
  try {
    const result = rows(await resource.list('/operations', { asset_id: assetId.value, page_size: 200 }))
    operations.value = result.items
    operationsTruncated.value = result.total > result.items.length
  } catch (e) {
    operations.value = []
    operationsTruncated.value = false
    message.warning(`操作日志加载失败：${errorText(e)}`)
  } finally {
    operationsLoading.value = false
  }
}

async function loadHardwareLedger() {
  try {
    ledgerHardware.value = rows(await resource.list('/hardware', { asset_id: assetId.value, page_size: 200 })).items
  } catch {
    ledgerHardware.value = []
  }
}

async function loadSensors() {
  if (!auth.can('monitor:read')) { sensorError.value = '当前角色缺少监控读取权限（monitor:read）'; return }
  sensorLoading.value = true
  sensorError.value = ''
  try {
    sensorRows.value = rows(await resource.list('/sensor-records', { asset_id: assetId.value, page_size: 200 })).items
  } catch (e) {
    sensorRows.value = []
    sensorError.value = errorText(e)
  } finally {
    sensorLoading.value = false
  }
}

async function loadNetwork() {
  networkLoading.value = true
  try {
    ipAllocations.value = rows(await resource.list('/ip-allocations', { asset_id: assetId.value, page_size: 100 })).items
  } catch {
    ipAllocations.value = []
  }
  try {
    networkRows.value = rows(await resource.list('/networks', { page_size: 200 })).items
  } catch {
    networkRows.value = []
  }
  networkLoading.value = false
}

async function loadSolSessions() {
  solLoading.value = true
  try {
    solSessions.value = rows(await resource.list('/sol-sessions', { asset_id: assetId.value, page_size: 20 })).items
  } catch {
    solSessions.value = []
  }
  solLoading.value = false
}

async function loadTags() {
  try {
    const [bindings, catalog] = await Promise.all([
      resource.list('/asset-tags', { asset_id: assetId.value, page_size: 200 }),
      resource.list('/tags', { page_size: 200 })
    ])
    tagBindings.value = rows(bindings).items
    tagCatalog.value = rows(catalog).items
  } catch {
    tagBindings.value = []
    tagCatalog.value = []
  }
}

// 引用数据静默加载：失败只影响名称展示，不阻塞主流程
async function loadReferences() {
  if (auth.can('datacenters:read')) {
    try { datacenterRows.value = rows(await resource.list('/datacenters', { page_size: 200 })).items } catch { datacenterRows.value = [] }
  }
  if (auth.systemMode === 'self_use' && auth.can('departments:read')) {
    try { departmentRows.value = rows(await resource.list('/departments', { page_size: 200 })).items } catch { departmentRows.value = [] }
  }
  if (auth.can('iam:read')) {
    try { memberRows.value = rows(await resource.list('/members', { page_size: 200 })).items } catch { memberRows.value = [] }
  }
}

function resetData() {
  asset.value = {}
  collection.value = {}
  operations.value = []
  sensorRows.value = []
  ledgerHardware.value = []
  ipAllocations.value = []
  solSessions.value = []
  tagBindings.value = []
  collectionNotice.value = ''
  activeTab.value = 'overview'
  loadedTabs = new Set()
}

async function refreshAll() {
  await Promise.all([loadAsset(), loadCollection().catch(() => undefined), loadOperations(), loadTags()])
}

// ============================================================
// 状态派生
// ============================================================
const inFlightOps = computed(() => operations.value.filter(o => ['running', 'pending', 'retry_wait'].includes(str(o.status))))
const derivedStatus = computed(() => {
  const active = [...inFlightOps.value].sort((a, b) => (str(a.status) === 'running' ? -1 : 1) - (str(b.status) === 'running' ? -1 : 1))[0]
  if (active) {
    if (str(active.status) !== 'running') return 'queued'
    return ({ power_on: 'starting', power_off: 'stopping', power_cycle: 'restarting', collect: str(asset.value.collected_at) ? 'collecting' : 'managing', install: 'reinstalling', firmware_upgrade: 'upgrading' } as Record<string, string>)[str(active.kind)] || 'queued'
  }
  const status = str(asset.value.status)
  if (status === 'maintenance' || status === 'retired') return status
  if (!asset.value.collected_at && str(asset.value.collection_error)) return 'error'
  if (str(asset.value.power_state) === 'off') return 'stopped'
  if (str(asset.value.power_state) === 'on') return auth.systemMode === 'external_operation' ? 'in_use' : 'running'
  if (status === 'offline') return 'offline'
  if (!asset.value.collected_at && status === 'unknown') return 'pending_management'
  return 'unknown'
})
const displayStatus = computed(() => str(asset.value.display_status) || derivedStatus.value)
const statusTag = computed<StatusMeta>(() => statusMeta[displayStatus.value] || statusMeta.unknown)
const powerLabel = computed(() => ({ on: '电源开启', off: '电源关闭', unknown: '电源未知' } as Record<string, string>)[str(asset.value.power_state)] || '电源未知')

const ownerScope = computed(() => (auth.systemMode === 'external_operation' ? '租户' : '部门'))
const ownerName = computed(() => {
  if (auth.systemMode === 'external_operation') {
    const hit = auth.tenants.find(tenant => String(tenant.id) === str(asset.value.tenant_id))
    return str(hit?.name) || (String(asset.value.tenant_id) === String(auth.tenantId) ? '当前租户' : `租户 #${str(asset.value.tenant_id) || '—'}`)
  }
  const hit = departmentRows.value.find(dept => String(dept.id) === str(asset.value.department_id))
  if (hit?.name) return str(hit.name)
  return asset.value.department_id == null ? '未分配' : `部门 #${str(asset.value.department_id)}`
})
const datacenterName = computed(() => {
  const id = str(asset.value.datacenter_id)
  if (!id) return '未分配机房'
  const hit = datacenterRows.value.find(dc => String(dc.id) === id)
  return str(hit?.name) || `机房 #${id}`
})

// ============================================================
// 规格拼装（标题栏与概览共用）
// ============================================================
const cpuCores = computed(() => componentsOf('processors').reduce((sum, row) => sum + numOf(row.cores), 0))
const cpuThreads = computed(() => componentsOf('processors').reduce((sum, row) => sum + numOf(row.threads), 0))
const memoryGiB = computed(() => {
  const fromSummary = numOf(collection.value.memory_gib)
  if (Number.isFinite(fromSummary) && fromSummary > 0) return Math.round(fromSummary)
  const fromModules = componentsOf('memory').reduce((sum, row) => sum + numOf(row.capacity_mib) / 1024, 0)
  return fromModules > 0 ? Math.round(fromModules) : 0
})
const physicalDisks = computed(() => {
  const rows: RecordData[] = []
  for (const source of componentsOf('storage').filter(row => str(row.kind) === 'drive')) {
    if (/backplane|enclosure|expander|背板/i.test(`${str(source.name)} ${str(source.model)}`)) continue
    const serial = str(source.serial_number).trim().toLowerCase()
    const validSerial = serial && !/^(0+|unknown|none|n\/a|not specified)$/i.test(serial)
    const existing = rows.find(row => {
      const otherSerial = str(row.serial_number).trim().toLowerCase()
      if (validSerial && otherSerial && serial !== otherSerial) return false
      if (row.model && source.model && str(row.model).trim().toLowerCase() !== str(source.model).trim().toLowerCase()) return false
      if (row.manufacturer && source.manufacturer && str(row.manufacturer).trim().toLowerCase() !== str(source.manufacturer).trim().toLowerCase()) return false
      return (validSerial && serial === otherSerial)
        || (!!source.name && str(source.name).trim() === str(row.name).trim() && numOf(source.capacity_bytes) > 0 && numOf(source.capacity_bytes) === numOf(row.capacity_bytes))
    })
    if (existing) {
      for (const [key, value] of Object.entries(source)) {
        if (existing[key] == null || existing[key] === '' || existing[key] === 0) existing[key] = value
      }
    } else rows.push({ ...source })
  }
  return rows
})
const networkInterfaces = computed(() => {
  const network = componentsOf('network')
  const kinds = ['port', 'interface', 'network_function', 'adapter', 'ethernet']
  const preferred = kinds.find(kind => network.some(row => str(row.kind) === kind && !!row.mac))
    || kinds.find(kind => network.some(row => str(row.kind) === kind))
  const rows: RecordData[] = []
  for (const source of network.filter(row => str(row.kind) === preferred || !!row.mac)
    .sort((a, b) => Number(str(b.kind) === preferred) - Number(str(a.kind) === preferred))) {
    const mac = str(source.mac).replace(/[^a-f0-9]/gi, '').toLowerCase()
    const validMac = /^[a-f0-9]{12}$/.test(mac) && !/^(0{12}|f{12})$/.test(mac)
    if (str(source.kind) !== preferred && !validMac) continue
    const existing = validMac ? rows.find(row => str(row.mac).replace(/[^a-f0-9]/gi, '').toLowerCase() === mac) : undefined
    if (existing) {
      for (const [key, value] of Object.entries(source)) {
        if (existing[key] == null || existing[key] === '' || existing[key] === 0) existing[key] = value
      }
    } else rows.push({ ...source })
  }
  return rows
})
const raidCards = computed(() => {
  const rows = componentsOf('storage').filter(row => {
    if (!['controller', 'storage'].includes(str(row.kind))) return false
    const description = `${str(row.name)} ${str(row.model)}`
    return /raid|perc|megaraid|smart array/i.test(description)
      && !/backplane|enclosure|expander|\bAHCI\b|\bHBA\b|背板/i.test(description)
  })
  return rows.filter((row, index) => rows.findIndex(other =>
    row.serial_number ? row.serial_number === other.serial_number
      : row.id === other.id && row.name === other.name && row.model === other.model
  ) === index)
})
const diskBytes = computed(() => physicalDisks.value.reduce((sum, row) => sum + numOf(row.capacity_bytes), 0))
const diskCount = computed(() => physicalDisks.value.length)
const diskMedia = computed(() => [...new Set(physicalDisks.value.map(row => str(row.media_type)).filter(Boolean))].join('/'))

const specText = computed(() => {
  const parts: string[] = []
  if (cpuCores.value) parts.push(`${cpuCores.value}C`)
  if (memoryGiB.value) parts.push(`${memoryGiB.value >= 1024 ? `${Number((memoryGiB.value / 1024).toFixed(1))}T` : `${memoryGiB.value}G`}`)
  if (diskBytes.value) parts.push(`${diskCount.value > 1 ? `${diskCount.value}×` : ''}${humanBytes(diskBytes.value).replace(' ', '')}${diskMedia.value ? ` ${diskMedia.value}` : ''}`)
  return parts.length ? parts.join(' ') : '规格待采集'
})

// ============================================================
// 概览：基础配置两列网格
// ============================================================
type ConfigField = { label: string; value: string; mono?: boolean; hint?: string; tone?: 'warn' | 'danger' }
const nicSummaryText = computed(() => {
  const rows = networkInterfaces.value
  if (!rows.length) return '—'
  const label = rows.every(row => str(row.kind) === 'adapter') ? '张适配器' : '个接口'
  const fastest = Math.max(...rows.map(r => numOf(r.speed_mbps)))
  return fastest > 0 ? `${rows.length} ${label} · 最高 ${formatField('speed_mbps', fastest)}` : `${rows.length} ${label}`
})
const warrantyField = computed<ConfigField>(() => {
  const expiry = str(asset.value.warranty_expiry)
  if (!expiry) return { label: '维保到期', value: '—', hint: '台账未登记维保信息' }
  const at = dayjs(expiry)
  const days = at.diff(dayjs(), 'day')
  return {
    label: '维保到期', value: at.format('YYYY-MM-DD'), mono: true,
    tone: days < 0 ? 'danger' : days <= 90 ? 'warn' : undefined,
    hint: days < 0 ? `已过期 ${Math.abs(days)} 天` : days <= 90 ? `剩余 ${days} 天` : undefined
  }
})
const configFields = computed<ConfigField[]>(() => {
  const a = asset.value
  const c = collection.value
  const sockets = componentsOf('processors').length
  const cpuModel = str(c.cpu_model) || str(componentsOf('processors')[0]?.model)
  const memoryModules = componentsOf('memory').length
  return [
    { label: '服务器名称', value: str(a.name) || '—' },
    { label: '序列号 SN', value: str(a.serial_number) || str(c.serial_number), mono: true },
    { label: '制造商 / 型号', value: [str(a.manufacturer) || str(c.manufacturer), str(a.model) || str(c.model)].filter(Boolean).join(' · ') || '—' },
    { label: 'CPU', value: cpuModel ? `${sockets ? `${sockets} 路 ` : ''}${cpuCores.value ? `${cpuCores.value} 核` : ''}${cpuThreads.value ? ` / ${cpuThreads.value} 线程` : ''} ${cpuModel}` : (c.collected_at ? '已采集但未暴露 CPU 型号' : '—'), hint: c.collected_at ? undefined : '需先采集硬件快照' },
    { label: '内存', value: memoryGiB.value ? `${memoryGiB.value} GiB${memoryModules ? ` · ${memoryModules} 条` : ''}` : str(c.memory_gib) ? `${str(c.memory_gib)} GiB` : '—' },
    { label: '磁盘', value: diskBytes.value ? `${humanBytes(diskBytes.value)}${diskCount.value ? ` · ${diskCount.value} 块` : ''}${diskMedia.value ? ` ${diskMedia.value}` : ''}` : '—' },
    { label: '网卡', value: nicSummaryText.value },
    { label: 'BMC 地址', value: str(a.bmc_address), mono: true },
    { label: '业务 IP', value: str(a.management_ip), mono: true },
    { label: '所在机房', value: datacenterName.value },
    { label: '机柜 / U 位', value: '未启用', hint: '当前数据模型（迁移 005 起）已移除机柜与 U 位字段' },
    { label: `${ownerScope.value}`, value: ownerName.value },
    { label: '采购日期', value: fmtDate(a.purchase_date), mono: true },
    warrantyField.value,
    { label: 'MAC 地址', value: str(a.mac_address), mono: true },
    { label: '纳管时间', value: fmtTime(a.created_at), mono: true }
  ]
})

const bmcFields = computed(() => [
  { label: '管理协议', value: (str(asset.value.bmc_protocol) || '—').toUpperCase() },
  { label: '端口', value: str(asset.value.bmc_port) || '—' },
  { label: 'TLS 校验', value: asset.value.bmc_tls_verify === false ? '已关闭' : '开启' },
  { label: '后台监控', value: collection.value.supported === false ? 'IPMI 不支持' : collection.value.monitoring_enabled ? '已开启（约 5 分钟一次）' : '已暂停' },
  { label: '最近采集', value: fmtTime(collection.value.collected_at ?? asset.value.collected_at) },
  { label: '硬件健康', value: valueLabels[str(collection.value.health)] || str(collection.value.health) || '未知' }
])

// ============================================================
// 概览：运行时长与电源历史
// ============================================================
const lastPowerOn = computed(() => {
  const events = operations.value
    .filter(o => str(o.kind) === 'power_on' && str(o.status) === 'succeeded')
    .map(o => dayjs(str(o.completed_at || o.created_at)))
    .filter(at => at.isValid())
    .sort((x, y) => y.valueOf() - x.valueOf())
  return events[0] || null
})
const uptime = computed(() => {
  if (str(asset.value.power_state) !== 'on') return { text: '未运行', detail: `电源状态为 ${powerLabel.value}，不计算连续运行时长。`, tone: 'muted' as const, days: '', hours: '', minutes: '' }
  const since = lastPowerOn.value
  if (!since) return { text: '无法推算', detail: '操作日志中没有成功的开机事件，缺少起算时间。', tone: 'muted' as const, days: '', hours: '', minutes: '' }
  const total = Math.max(0, dayjs().diff(since, 'minute'))
  return {
    text: since.format('YYYY-MM-DD HH:mm') + ' 开机',
    detail: `依据最近一次成功开机事件推算，非操作系统 uptime。${str(collection.value.collected_at) ? `最近快照：${fmtTime(collection.value.collected_at)}。` : ''}`,
    tone: 'live' as const,
    days: String(Math.floor(total / 1440)), hours: String(Math.floor((total % 1440) / 60)).padStart(2, '0'), minutes: String(total % 60).padStart(2, '0')
  }
})

function durationOf(row: RecordData) {
  const start = str(row.started_at), end = str(row.completed_at)
  if (!start || !end) return str(row.status) === 'running' ? '执行中' : '—'
  const ms = dayjs(end).diff(dayjs(start))
  if (!Number.isFinite(ms) || ms < 0) return '—'
  const units: [number, string][] = [[86400000, '天'], [3600000, '时'], [60000, '分'], [1000, '秒']]
  const parts: string[] = []
  let rest = ms
  for (const [size, name] of units) {
    const value = Math.floor(rest / size)
    if (value > 0) { parts.push(`${value}${name}`); rest -= value * size }
    if (parts.length === 2) break
  }
  return parts.length ? parts.join('') : `${ms} 毫秒`
}

function operatorName(userId: unknown) {
  const id = str(userId)
  if (!id) return '系统'
  const hit = memberRows.value.find(m => String(m.user_id) === id)
  return str(hit?.name) || str(hit?.username) || `用户 #${id}`
}

function operationDetail(row: RecordData) {
  const parts: string[] = []
  const attempts = numOf(row.attempts), max = numOf(row.max_attempts)
  if (Number.isFinite(max) && max > 0) parts.push(`尝试 ${attempts || 0}/${max}`)
  if (row.worker_id) parts.push(`执行节点 ${str(row.worker_id)}`)
  if (row.error) parts.push(`错误：${str(row.error)}`)
  return parts.join(' · ') || '—'
}

const operationColumns = computed<DataTableColumns<RecordData>>(() => [
  { title: '时间', key: 'created_at', minWidth: 158, render: row => h('span', { class: 'mono' }, fmtTime(row.created_at)) },
  { title: '操作', key: 'kind', minWidth: 100, render: row => kindLabel(row.kind) },
  { title: '操作人', key: 'user_id', minWidth: 104, render: row => operatorName(row.user_id) },
  { title: '详情', key: 'detail', minWidth: 240, ellipsis: { tooltip: true }, render: row => operationDetail(row) },
  {
    title: '状态', key: 'status', minWidth: 92,
    render: row => h(NTag, { size: 'small', bordered: false, type: taskStatusType(row.status) }, { default: () => opStatusLabel(row.status) })
  },
  { title: '耗时', key: 'duration', minWidth: 96, render: row => h('span', { class: 'mono' }, durationOf(row)) }
])

const powerHistoryColumns: DataTableColumns<RecordData> = [
  { title: '时间', key: 'created_at', minWidth: 158, render: row => h('span', { class: 'mono' }, fmtTime(row.created_at)) },
  { title: '动作', key: 'kind', minWidth: 100, render: row => kindLabel(row.kind) },
  { title: '操作人', key: 'user_id', minWidth: 104, render: row => operatorName(row.user_id) },
  {
    title: '结果', key: 'status', minWidth: 92,
    render: row => h(NTag, { size: 'small', bordered: false, type: taskStatusType(row.status) }, { default: () => opStatusLabel(row.status) })
  },
  { title: '耗时', key: 'duration', minWidth: 96, render: row => h('span', { class: 'mono' }, durationOf(row)) }
]

const powerHistory = computed(() => operations.value.filter(o => str(o.kind).startsWith('power') || str(o.kind) === 'reset').slice(0, 6))
const filteredOperations = computed(() => operations.value.filter(row =>
  (!opKindFilter.value || str(row.kind) === opKindFilter.value) &&
  (!opStatusFilter.value || str(row.status) === opStatusFilter.value)))
const opKindOptions = computed<SelectOption[]>(() => [...new Set(operations.value.map(o => str(o.kind)).filter(Boolean))]
  .map(key => ({ label: kindLabels[key] || key, value: key })))
const opStatusOptions = computed<SelectOption[]>(() => [...new Set(operations.value.map(o => str(o.status)).filter(Boolean))]
  .map(key => ({ label: opStatusLabel(key), value: key })))
const operationPagination = computed(() => ({
  page: opPage.value, pageSize: opPageSize.value, showSizePicker: true, pageSizes: [20, 50, 100],
  prefix: ({ itemCount }: { itemCount?: number }) => `共 ${itemCount ?? 0} 条`,
  onUpdatePage: (page: number) => { opPage.value = page },
  onUpdatePageSize: (size: number) => { opPageSize.value = size; opPage.value = 1 }
}))

// ============================================================
// 硬件信息：分组件结构化表格
// ============================================================
type FieldDef = { key: string; label: string; mono?: boolean; width?: number }
type HardwareSection = { key: string; title: string; group: string; rows: RecordData[]; fields: FieldDef[]; empty: string }

function buildSectionColumns(rows: RecordData[], fields: FieldDef[]): DataTableColumns<RecordData> {
  const columns: DataTableColumns<RecordData> = [
    { title: '#', key: '__no', width: 54, render: (_row, index) => h('span', { class: 'mono row-no' }, String(index + 1)) }
  ]
  fields
    .filter(field => !(['health', 'state'].includes(field.key) && rows.some(row => row.health || row.state)))
    .filter(field => rows.some(row => row[field.key] != null && row[field.key] !== ''))
    .forEach(field => {
      columns.push({
        title: field.label, key: field.key, minWidth: field.width || 118,
        render: row => h('span', { class: field.mono ? 'mono' : '' }, formatField(field.key, row[field.key]))
      })
    })
  if (rows.some(row => row.health || row.state)) {
    columns.push({
      title: '健康', key: 'health', minWidth: 78,
      render: row => row.health ? h(NTag, { size: 'small', bordered: false, type: healthType(row.health) }, { default: () => formatField('health', row.health) }) : '—'
    })
    columns.push({ title: '在位状态', key: 'state', minWidth: 88, render: row => formatField('state', row.state) })
  }
  return columns
}

const hardwareSections = computed<HardwareSection[]>(() => {
  const sensors = componentsOf('sensors')
  const sections: HardwareSection[] = [
    {
      key: 'cpu', title: '处理器', group: 'processors', rows: componentsOf('processors'), empty: '快照中没有处理器明细',
      fields: [
        { key: 'name', label: '插槽 / 名称', width: 140 }, { key: 'model', label: '型号', width: 200 },
        { key: 'manufacturer', label: '制造商' }, { key: 'cores', label: '物理核心' }, { key: 'threads', label: '逻辑线程' },
        { key: 'max_speed_mhz', label: '最大主频' }, { key: 'serial_number', label: '序列号', mono: true, width: 150 }
      ]
    },
    {
      key: 'memory', title: '内存', group: 'memory', rows: componentsOf('memory'), empty: '快照中没有内存明细',
      fields: [
        { key: 'name', label: '插槽 / 名称', width: 140 }, { key: 'manufacturer', label: '制造商' },
        { key: 'capacity_mib', label: '容量', width: 110 }, { key: 'part_number', label: '部件号', mono: true, width: 150 },
        { key: 'serial_number', label: '序列号', mono: true, width: 150 }
      ]
    },
    {
      key: 'disk', title: '磁盘', group: 'storage', rows: physicalDisks.value, empty: '快照中没有物理盘明细',
      fields: [
        { key: 'name', label: '盘位 / 名称', width: 140 }, { key: 'model', label: '型号', width: 170 },
        { key: 'capacity_bytes', label: '容量', width: 110 }, { key: 'media_type', label: '介质' },
        { key: 'protocol', label: '接口协议' }, { key: 'serial_number', label: '序列号', mono: true, width: 150 }
      ]
    },
    {
      key: 'raid', title: 'RAID卡', group: 'storage',
      rows: raidCards.value,
      empty: '未采集到可识别的 RAID 卡',
      fields: [
        { key: 'name', label: '名称', width: 150 }, { key: 'manufacturer', label: '制造商' },
        { key: 'model', label: '型号', width: 170 }, { key: 'serial_number', label: '序列号', mono: true, width: 150 },
        { key: 'version', label: '固件版本' }, { key: 'protocol', label: '接口协议' }
      ]
    },
    {
      key: 'nic', title: '网卡', group: 'network', rows: networkInterfaces.value, empty: '快照中没有网卡明细',
      fields: [
        { key: 'name', label: '名称', width: 140 }, { key: 'kind', label: '类别' }, { key: 'manufacturer', label: '制造商' },
        { key: 'model', label: '型号', width: 150 }, { key: 'mac', label: 'MAC 地址', mono: true, width: 160 },
        { key: 'speed_mbps', label: '速率', width: 100 }, { key: 'link', label: '链路状态' }, { key: 'state', label: '在位状态' }
      ]
    },
    {
      key: 'psu', title: '电源', group: 'sensors', rows: sensors.filter(row => str(row.kind) === 'power_supply' || str(row.units) === 'V' || str(row.name).toLowerCase().includes('power')),
      empty: '快照中没有电源模块明细',
      fields: [
        { key: 'name', label: '名称', width: 140 }, { key: 'manufacturer', label: '制造商' }, { key: 'model', label: '型号', width: 160 },
        { key: 'serial_number', label: '序列号', mono: true, width: 150 }, { key: 'reading', label: '读数' }, { key: 'units', label: '单位' },
        { key: 'state', label: '在位状态' }
      ]
    },
    {
      key: 'firmware', title: 'BIOS / 固件', group: 'firmware', rows: componentsOf('firmware'), empty: '快照中没有固件清单明细',
      fields: [
        { key: 'name', label: '组件', width: 170 }, { key: 'version', label: '版本', mono: true, width: 150 },
        { key: 'manufacturer', label: '制造商' }, { key: 'state', label: '状态' }
      ]
    }
  ]
  return sections
})

const biosRow = computed(() => {
  const version = str(collection.value.bios_version)
  return version ? [{ name: 'System BIOS（快照摘要）', version, state: str(asset.value.power_state) === 'on' ? 'On' : 'Present' }] : []
})

const ledgerColumns: DataTableColumns<RecordData> = [
  { title: '类别', key: 'category', minWidth: 90, render: row => str(row.category) || '—' },
  { title: '名称', key: 'name', minWidth: 150 },
  { title: '厂商', key: 'vendor', minWidth: 110 },
  { title: '型号', key: 'model', minWidth: 140 },
  { title: '序列号', key: 'serial_number', minWidth: 150, render: row => h('span', { class: 'mono' }, dash(row.serial_number)) },
  { title: '明细', key: 'details', minWidth: 200, ellipsis: { tooltip: true }, render: row => formatField('details', row.details) },
  { title: '采集时间', key: 'collected_at', minWidth: 150, render: row => h('span', { class: 'mono' }, fmtTime(row.collected_at)) }
]

// ============================================================
// 网络配置
// ============================================================
const networkName = (id: unknown) => {
  const key = str(id)
  if (!key) return '—'
  const hit = networkRows.value.find(row => String(row.id) === key)
  return str(hit?.name) || `网络 #${key}`
}
const ipColumns = computed<DataTableColumns<RecordData>>(() => [
  { title: 'IP 地址', key: 'address', minWidth: 140, render: row => h('span', { class: 'mono' }, dash(row.address)) },
  { title: '所属网络', key: 'network_id', minWidth: 130, render: row => networkName(row.network_id) },
  {
    title: '分配状态', key: 'status', minWidth: 96,
    render: row => h(NTag, {
      size: 'small', bordered: false,
      type: str(row.status) === 'allocated' ? 'success' : str(row.status) === 'reserved' ? 'warning' : 'default'
    }, { default: () => formatField('status', row.status) })
  },
  { title: 'MAC 地址', key: 'mac_address', minWidth: 160, render: row => h('span', { class: 'mono' }, dash(row.mac_address)) },
  { title: '分配时间', key: 'allocated_at', minWidth: 150, render: row => h('span', { class: 'mono' }, fmtTime(row.allocated_at)) },
  { title: '释放时间', key: 'released_at', minWidth: 150, render: row => h('span', { class: 'mono' }, fmtTime(row.released_at)) }
])
const nicColumns = computed<DataTableColumns<RecordData>>(() => buildSectionColumns(networkInterfaces.value, [
  { key: 'name', label: '接口名称', width: 150 }, { key: 'kind', label: '类别' },
  { key: 'mac', label: 'MAC 地址', mono: true, width: 160 }, { key: 'model', label: '型号', width: 150 },
  { key: 'speed_mbps', label: '协商速率', width: 110 }, { key: 'link', label: '链路状态' }, { key: 'state', label: '在位状态' }
]))

// ============================================================
// 监控：时间窗、趋势与仪表
// ============================================================
type RangeKey = '1h' | '6h' | '24h' | '7d' | '30d' | 'custom'
const RANGE_DEF: Record<Exclude<RangeKey, 'custom'>, { span: number; bucket: number; label: string; fmt: string }> = {
  '1h': { span: 3600e3, bucket: 300e3, label: '1 小时', fmt: 'HH:mm' },
  '6h': { span: 6 * 3600e3, bucket: 30 * 60e3, label: '6 小时', fmt: 'HH:mm' },
  '24h': { span: 24 * 3600e3, bucket: 2 * 3600e3, label: '24 小时', fmt: 'HH:mm' },
  '7d': { span: 7 * 86400e3, bucket: 6 * 3600e3, label: '7 天', fmt: 'MM-DD HH:mm' },
  '30d': { span: 30 * 86400e3, bucket: 24 * 3600e3, label: '30 天', fmt: 'MM-DD' }
}
const rangeItems: { key: RangeKey; label: string }[] = [
  { key: '1h', label: RANGE_DEF['1h'].label }, { key: '6h', label: RANGE_DEF['6h'].label },
  { key: '24h', label: RANGE_DEF['24h'].label }, { key: '7d', label: RANGE_DEF['7d'].label },
  { key: '30d', label: RANGE_DEF['30d'].label }, { key: 'custom', label: '自定义' }
]
const METRICS = [
  { key: 'cpu', label: 'CPU 使用率', color: '#165DFF', axis: 'percent' },
  { key: 'memory', label: '内存使用率', color: '#00B42A', axis: 'percent' },
  { key: 'disk', label: '磁盘使用率', color: '#FF7D00', axis: 'percent' },
  { key: 'network', label: '网络吞吐', color: '#0FC6C2', axis: 'rate' }
]
const activeMetrics = computed(() => METRICS.filter(metric => showMetrics.value[metric.key]).map(metric => metric.key))
function onMetricChange(keys: Array<string | number>) {
  const set = new Set(keys.map(String))
  METRICS.forEach(metric => { showMetrics.value[metric.key] = set.has(metric.key) })
}

function windowBounds() {
  if (monitorRange.value === 'custom' && customRange.value) {
    const [from, to] = customRange.value
    const span = Math.max(60e3, to - from)
    return { from, to, bucket: Math.max(60e3, span / 40), fmt: span <= 86400e3 ? 'MM-DD HH:mm' : 'MM-DD' }
  }
  const def = RANGE_DEF[monitorRange.value === 'custom' ? '24h' : monitorRange.value]
  const to = dayjs().valueOf()
  return { from: to - def.span, to, bucket: def.bucket, fmt: def.fmt }
}

function classifyMetric(row: RecordData): string | null {
  const key = `${str(row.metric)} ${str(row.sensor_key)}`.toLowerCase()
  if (/cpu|processor|compute|proc/.test(key)) return 'cpu'
  if (/mem|ram/.test(key)) return 'memory'
  if (/disk|storage|volume|logicaldrive|raid/.test(key)) return 'disk'
  if (/net|nic|eth|throughput|link.?speed|bandwidth/.test(key)) return 'network'
  return null
}

const trendBuckets = computed(() => {
  const { from, to, bucket, fmt } = windowBounds()
  const slots: { start: number; label: string; values: Record<string, number[]> }[] = []
  for (let start = from; start < to; start += bucket) slots.push({ start, label: dayjs(start).format(fmt), values: {} })
  let hits = 0
  for (const row of sensorRows.value) {
    const key = classifyMetric(row)
    if (!key) continue
    const value = numOf(row.value)
    if (!Number.isFinite(value)) continue
    const unit = str(row.unit || row.units).toLowerCase()
    if (unit.includes('%') || key !== 'network') {
      const percentageLike = unit.includes('%') || (value >= 0 && value <= 100)
      if (!percentageLike) continue
    }
    const at = dayjs(str(row.sampled_at || row.created_at)).valueOf()
    if (!Number.isFinite(at) || at < from || at > to) continue
    const index = Math.min(slots.length - 1, Math.floor((at - from) / bucket))
    if (index < 0) continue
    hits++
    const slot = slots[index]
    ;(slot.values[key] ||= []).push(value)
  }
  const average = (list?: number[]) => (list && list.length ? Number((list.reduce((a, b) => a + b, 0) / list.length).toFixed(1)) : null)
  const series: Record<string, Array<number | null>> = {}
  METRICS.forEach(metric => { series[metric.key] = slots.map(slot => average(slot.values[metric.key])) })
  return { labels: slots.map(slot => slot.label), series, hits, points: slots.length }
})
const hasTrend = computed(() => trendBuckets.value.hits >= 2)
const trendNote = computed(() => {
  if (sensorError.value) return `样本读取失败：${sensorError.value}`
  if (!sensorRows.value.length) return '暂无时序样本'
  return `${trendBuckets.value.hits} 个样本 · 近 ${trendBuckets.value.points} 个时间桶`
})
const trendEmptyReason = computed(() => {
  if (sensorError.value) return `监控样本读取失败：${sensorError.value}`
  if (!auth.can('monitor:read')) return '当前角色缺少 monitor:read 权限，无法读取 sensor_records 时序数据。'
  if (!sensorRows.value.length) return '该设备尚无 sensor_records 时序样本。当前版本的内置 Redfish 轮询只更新资产摘要字段与告警，不写入时序表；趋势曲线需要外部采集器（如 Prometheus / 自定义上报）落库后才会出现。'
  return `已取到 ${sensorRows.value.length} 条记录，但所选时间窗内可用样本不足 2 个（后端单次最多返回 200 条，按最新优先）。可放宽时间范围或扩大窗口。`
})

function chartTokens() {
  const cs = getComputedStyle(document.documentElement)
  const read = (name: string, fallback: string) => cs.getPropertyValue(name).trim() || fallback
  return {
    text: read('--color-text-primary', '#1D2129'), secondary: read('--color-text-secondary', '#4E5969'),
    faint: read('--color-text-tertiary', '#86909C'), line: read('--color-border', '#E5E6EB'),
    soft: read('--color-border-light', '#F0F0F0'), surface: read('--color-surface', '#FFFFFF')
  }
}
function withAlpha(color: string, alpha: number) {
  const hex = color.replace('#', '')
  const full = hex.length === 3 ? hex.split('').map(c => c + c).join('') : hex
  const int = parseInt(full, 16)
  return `rgba(${(int >> 16) & 255}, ${(int >> 8) & 255}, ${int & 255}, ${alpha})`
}

function initMonitorChart() {
  if (!monitorChartRef.value) return
  if (monitorChart && monitorChart.isDisposed()) monitorChart = null
  if (!monitorChart) monitorChart = echarts.init(monitorChartRef.value)
  const t = chartTokens()
  const { labels, series } = trendBuckets.value
  const enabled = METRICS.filter(metric => activeMetrics.value.includes(metric.key))
  const rateVisible = enabled.some(metric => metric.axis === 'rate')
  const percentAxis = {
    type: 'value', name: '使用率', min: 0, max: 100, interval: 25, nameTextStyle: { color: t.faint, fontSize: 11 },
    axisLabel: { formatter: '{value}%', color: t.faint, fontSize: 11 }, axisLine: { show: false }, axisTick: { show: false },
    splitLine: { lineStyle: { color: t.soft, type: 'dashed' } }
  }
  const rateAxis = {
    type: 'value', name: '网络', nameTextStyle: { color: t.faint, fontSize: 11 },
    axisLabel: { color: t.faint, fontSize: 11 }, axisLine: { show: false }, axisTick: { show: false }, splitLine: { show: false }
  }
  monitorChart.setOption({
    animationDuration: 620, animationEasing: 'cubicOut',
    grid: { left: 8, right: 14, top: 44, bottom: 56, containLabel: true },
    legend: { top: 8, left: 0, icon: 'roundRect', itemWidth: 10, itemHeight: 10, itemGap: 18, textStyle: { color: t.secondary, fontSize: 12 } },
    tooltip: {
      trigger: 'axis', backgroundColor: t.surface, borderColor: t.line, borderWidth: 1, padding: [8, 12],
      textStyle: { color: t.text, fontSize: 12 },
      axisPointer: { type: 'line', lineStyle: { color: t.line, type: 'dashed' } }
    },
    dataZoom: [
      { type: 'inside', throttle: 60 },
      { type: 'slider', height: 18, bottom: 12, borderColor: t.line, fillerColor: withAlpha('#165DFF', 0.08), handleStyle: { color: t.surface, borderColor: t.line }, textStyle: { color: t.faint, fontSize: 10 } }
    ],
    xAxis: {
      type: 'category', boundaryGap: false, data: labels,
      axisLine: { lineStyle: { color: t.line } }, axisTick: { show: false },
      axisLabel: { color: t.faint, fontSize: 11, hideOverlap: true }
    },
    yAxis: rateVisible ? [percentAxis, { ...rateAxis, position: 'right', offset: 0 }] : [percentAxis],
    series: enabled.map(metric => ({
      name: metric.label, type: 'line', smooth: true, showSymbol: false, symbol: 'circle', symbolSize: 6,
      yAxisIndex: metric.axis === 'rate' && rateVisible ? 1 : 0,
      connectNulls: true, lineStyle: { color: metric.color, width: 2 }, itemStyle: { color: metric.color },
      areaStyle: { color: new echarts.graphic.LinearGradient(0, 0, 0, 1, [{ offset: 0, color: withAlpha(metric.color, 0.24) }, { offset: 1, color: withAlpha(metric.color, 0) }]) },
      data: series[metric.key]
    }))
  }, true)
  monitorChart.resize()
}

// 传感器仪表：读数来自采集快照 components.sensors，是当前唯一可信的实时数据
const sensorReadings = computed(() => componentsOf('sensors').filter(row => Number.isFinite(numOf(row.reading)) && numOf(row.reading) > 0))
function readingsByUnit(...units: string[]) {
  return sensorReadings.value.filter(row => units.includes(str(row.units)))
}
const cpuTemp = computed(() => {
  const list = readingsByUnit('°C', 'C', 'Celsius')
  if (!list.length) return null
  const named = list.find(row => /cpu|proc|核心|socket/i.test(`${str(row.name)} ${str(row.id)}`))
  const target = named || [...list].sort((a, b) => numOf(b.reading) - numOf(a.reading))[0]
  return numOf(target.reading)
})
const fanRpm = computed(() => {
  const list = readingsByUnit('RPM')
  if (!list.length) return null
  return Math.round(list.reduce((sum, row) => sum + numOf(row.reading), 0) / list.length)
})
const fanCount = computed(() => readingsByUnit('RPM').length)
const powerWatts = computed(() => {
  const list = readingsByUnit('W')
  if (!list.length) return null
  const named = list.find(row => /consum|总功耗|power control|pdu|system/i.test(`${str(row.name)} ${str(row.id)}`))
  if (named) return numOf(named.reading)
  return Math.round(list.reduce((sum, row) => sum + numOf(row.reading), 0))
})
const sensorTableRows = computed(() => sensorReadings.value)
const sensorColumns = computed<DataTableColumns<RecordData>>(() => [
  { title: '传感器', key: 'name', minWidth: 170, render: row => dash(row.name) },
  { title: '类别', key: 'kind', minWidth: 100, render: row => str(row.kind) || '—' },
  { title: '读数', key: 'reading', minWidth: 90, render: row => h('span', { class: 'mono' }, formatField('reading', row.reading)) },
  { title: '单位', key: 'units', minWidth: 70, render: row => str(row.units) || '—' },
  {
    title: '健康', key: 'health', minWidth: 78,
    render: row => row.health ? h(NTag, { size: 'small', bordered: false, type: healthType(row.health) }, { default: () => formatField('health', row.health) }) : '—'
  },
  { title: '状态', key: 'state', minWidth: 88, render: row => formatField('state', row.state) }
])

function gaugeOption(value: number | null, min: number, max: number, unit: string, colors: [number, string][]) {
  const t = chartTokens()
  return {
    series: [{
      type: 'gauge', startAngle: 208, endAngle: -28, min, max, radius: '92%', center: ['50%', '58%'],
      progress: { show: true, width: 10, roundCap: true, itemStyle: { color: colors[0][1] } },
      axisLine: { lineStyle: { width: 10, color: colors } },
      pointer: { show: false }, axisTick: { show: false }, splitLine: { show: false },
      axisLabel: { show: true, distance: 14, color: t.faint, fontSize: 9 },
      anchor: { show: false }, title: { show: false },
      detail: {
        valueAnimation: true, offsetCenter: [0, '6%'], fontSize: 22, fontWeight: 700, color: t.text,
        formatter: (v: number) => (Number.isFinite(v) && v > 0 ? `${Math.round(v)}${unit}` : '—')
      },
      data: [{ value: value == null ? 0 : value }]
    }]
  }
}

function renderGauges() {
  const mounts = [cpuTempRef.value, fanRef.value, powerRef.value]
  const configs = [
    { value: cpuTemp.value, min: 0, max: 110, unit: '°C', colors: [[0.6, '#00B42A'], [0.8, '#FF7D00'], [1, '#F53F3F']] as [number, string][] },
    { value: fanRpm.value, min: 0, max: 12000, unit: ' RPM', colors: [[0.35, '#86909C'], [0.75, '#165DFF'], [1, '#0FC6C2']] as [number, string][] },
    { value: powerWatts.value, min: 0, max: 1200, unit: ' W', colors: [[0.55, '#0FC6C2'], [0.8, '#FF7D00'], [1, '#F53F3F']] as [number, string][] }
  ]
  mounts.forEach((el, index) => {
    if (!el) return
    if (gaugeCharts[index] && gaugeCharts[index]!.isDisposed()) gaugeCharts[index] = null
    if (!gaugeCharts[index]) gaugeCharts[index] = echarts.init(el)
    gaugeCharts[index]!.setOption(gaugeOption(configs[index].value, configs[index].min, configs[index].max, configs[index].unit, configs[index].colors), true)
    gaugeCharts[index]!.resize()
  })
}

function renderMonitor() {
  if (hasTrend.value) initMonitorChart()
  renderGauges()
}
function resizeCharts() {
  monitorChart?.resize()
  gaugeCharts.forEach(chart => chart?.resize())
}

// ============================================================
// 远程控制台
// ============================================================
const solUrl = computed(() => `/assets/${assetId.value}/sol`)
function openSolWindow() {
  window.open(solUrl.value, '_blank', 'noopener')
}
const activeSolSession = computed(() => solSessions.value.find(row => !row.ended_at && dayjs(str(row.expires_at)).isAfter(dayjs())))
const solColumns: DataTableColumns<RecordData> = [
  { title: '开始时间', key: 'started_at', minWidth: 150, render: row => h('span', { class: 'mono' }, fmtTime(row.started_at)) },
  { title: '结束时间', key: 'ended_at', minWidth: 150, render: row => h('span', { class: 'mono' }, fmtTime(row.ended_at)) },
  { title: '到期时间', key: 'expires_at', minWidth: 150, render: row => h('span', { class: 'mono' }, fmtTime(row.expires_at)) },
  { title: '结束原因', key: 'end_reason', minWidth: 120, render: row => dash(row.end_reason) },
  { title: '客户端', key: 'client_ip', minWidth: 130, render: row => h('span', { class: 'mono' }, dash(row.client_ip)) }
]

// ============================================================
// 电源操作与「更多」菜单
// ============================================================
const actionBusy = ref('')
const POWER_ACTIONS: Record<string, string> = { on: '开机', off: '关机', restart: '重启' }

async function handlePowerAction(action: string) {
  if (!auth.can('bmc:operate')) { message.error('缺少 BMC 操作权限（bmc:operate）'); return }
  const label = POWER_ACTIONS[action] || action
  dialog.warning({
    title: `确认执行${label}`,
    content: `将对 ${str(asset.value.name) || assetId.value} 下发 ${label} 指令。该操作直接影响物理服务器，指令提交仅代表受理，结果请在操作日志查看。`,
    positiveText: '确认执行',
    negativeText: '取消',
    onPositiveClick: async () => {
      actionBusy.value = action
      try {
        await api.post(`/assets/${encodeURIComponent(assetId.value)}/actions`, { action, idempotency_key: crypto.randomUUID() })
        message.success(`${label}指令已提交，正在等待执行结果`)
        await Promise.all([loadAsset(), loadOperations()])
      } catch (e) {
        message.error(errorText(e))
      } finally {
        actionBusy.value = ''
      }
    }
  })
}

const canOperate = computed(() => auth.can('assets:operate'))
const canPower = computed(() => auth.can('bmc:operate'))
const canWrite = computed(() => auth.can('assets:write'))

function bmcWebUrl(): string | null {
  const address = str(asset.value.bmc_address).trim()
  if (!address || /[\s/@?#\\]/.test(address)) return null
  const host = address.includes(':') && !address.startsWith('[') ? `[${address}]` : address
  const port = numOf(asset.value.bmc_port) || (str(asset.value.bmc_protocol) === 'ipmi' ? 623 : 443)
  try { return new URL(`https://${host}:${port}/`).href } catch { return null }
}

async function copyText(value: string, label: string) {
  try {
    await navigator.clipboard.writeText(value)
    message.success(`${label}已复制`)
  } catch {
    message.warning('浏览器拒绝了剪贴板访问，请手动复制')
  }
}

const moreOptions = computed<DropdownOption[]>(() => {
  const url = bmcWebUrl()
  const options: DropdownOption[] = [
    { key: 'collect', label: '采集硬件信息', disabled: !canOperate.value || collection.value.supported !== true || collectionLoading.value },
    { key: 'refresh', label: '刷新本页数据' },
    { key: 'ledger', label: '查看硬件台账' },
    { type: 'divider', key: 'd1' },
    { key: 'edit', label: '编辑资产', disabled: !canWrite.value },
    { key: 'tags', label: '管理标签', disabled: !canWrite.value },
    { key: 'bmc', label: '打开 BMC Web', disabled: !url },
    { key: 'copy', label: '复制连接信息' },
    { type: 'divider', key: 'd2' },
    { key: 'delete', label: '删除资产', disabled: !canWrite.value }
  ]
  return options
})

async function onMoreSelect(key: string) {
  if (key === 'collect') { await collectHardware(); if (activeTab.value === 'hardware') await nextTick(); return }
  if (key === 'refresh') { await refreshAll(); message.success('数据已刷新'); return }
  if (key === 'ledger') { activeTab.value = 'hardware'; return }
  if (key === 'edit') { openEdit(); return }
  if (key === 'tags') { openTagManager(); return }
  if (key === 'bmc') { const url = bmcWebUrl(); if (url) window.open(url, '_blank', 'noopener'); return }
  if (key === 'copy') {
    await copyText(`名称：${str(asset.value.name)}\n序列号：${str(asset.value.serial_number)}\nBMC：${str(asset.value.bmc_address)}\n业务 IP：${str(asset.value.management_ip)}`, '连接信息')
    return
  }
  if (key === 'delete') {
    dialog.error({
      title: '确认删除资产',
      content: `将从台账删除 ${str(asset.value.name) || assetId.value}，该操作会记录审计且不可撤销。`,
      positiveText: '确认删除',
      negativeText: '取消',
      onPositiveClick: async () => {
        try {
          await resource.remove('/assets', assetId.value)
          message.success('资产已删除')
          router.push('/assets')
        } catch (e) {
          message.error(errorText(e))
        }
      }
    })
  }
}

// ============================================================
// 编辑资产 / 管理标签
// ============================================================
const editing = ref(false)
const saving = ref(false)
// 表单字段显式声明为 string，避免模板里以 unknown 绑定 n-input
const editForm = ref({ name: '', management_ip: '', bmc_address: '', bmc_user: '', bmc_secret: '', mac_address: '' })
function openEdit() {
  editForm.value = {
    name: str(asset.value.name),
    management_ip: str(asset.value.management_ip),
    bmc_address: str(asset.value.bmc_address),
    bmc_user: str(asset.value.bmc_user),
    bmc_secret: '',
    mac_address: str(asset.value.mac_address)
  }
  editing.value = true
}
async function saveEdit() {
  saving.value = true
  try {
    const payload: RecordData = { ...editForm.value }
    if (!payload.bmc_secret) delete payload.bmc_secret
    await resource.update('/assets', assetId.value, payload)
    message.success('资产信息已更新')
    editing.value = false
    await loadAsset()
  } catch (e) {
    message.error(errorText(e))
  } finally {
    saving.value = false
  }
}

const tagModal = ref(false)
const tagSaving = ref(false)
const selectedTagIds = ref<string[]>([])
const boundTagIds = computed(() => tagBindings.value.map(row => str(row.tag_id)))
const boundTags = computed(() => tagBindings.value
  .map(row => tagCatalog.value.find(tag => String(tag.id) === str(row.tag_id)))
  .filter((tag): tag is RecordData => !!tag))
const tagOptions = computed<SelectOption[]>(() => tagCatalog.value.map(tag => ({ label: str(tag.name) || `#${str(tag.id)}`, value: str(tag.id) })))
function openTagManager() {
  selectedTagIds.value = [...boundTagIds.value]
  tagModal.value = true
}
async function saveTags() {
  tagSaving.value = true
  const removed = tagBindings.value.filter(row => !selectedTagIds.value.includes(str(row.tag_id)))
  const added = selectedTagIds.value.filter(id => !boundTagIds.value.includes(id))
  const results = await Promise.allSettled([
    ...removed.map(row => resource.remove('/asset-tags', row.id!)),
    ...added.map(id => resource.create('/asset-tags', { asset_id: Number(assetId.value), tag_id: Number(id) }))
  ])
  tagSaving.value = false
  const failed = results.filter(item => item.status === 'rejected')
  if (failed.length) message.warning(`${results.length - failed.length} 项已保存，${failed.length} 项失败：${errorText((failed[0] as PromiseRejectedResult).reason)}`)
  else message.success('标签已更新')
  await loadTags()
}

// ============================================================
// Tab 懒加载与生命周期
// ============================================================
let loadedTabs = new Set<string>()
async function ensureTabData(tab: string) {
  if (loadedTabs.has(tab)) return
  loadedTabs.add(tab)
  if (tab === 'monitor') await loadSensors()
  if (tab === 'hardware') await loadHardwareLedger()
  if (tab === 'network') await loadNetwork()
  if (tab === 'console') await loadSolSessions()
}

watch(activeTab, async tab => {
  if (tab === 'console') {
    await router.push(solUrl.value)
    return
  }
  await ensureTabData(tab)
  if (tab === 'monitor') await nextTick()
  if (tab === 'monitor') renderMonitor()
  if (tab === 'logs') opPage.value = 1
})
watch([monitorRange, customRange, showMetrics], () => { if (activeTab.value === 'monitor' && hasTrend.value) initMonitorChart() }, { deep: true })
watch([collection, sensorRows], () => { if (activeTab.value === 'monitor') nextTick(renderMonitor) })

async function reloadAll() {
  cancelCollection()
  resetData()
  await Promise.all([loadAsset(), loadTags(), loadReferences()])
  await Promise.all([loadCollection().catch(() => undefined), loadOperations()])
  await ensureTabData(activeTab.value)
}

watch(() => [assetId.value, auth.projectId], reloadAll, { immediate: true })

onMounted(() => { window.addEventListener('resize', resizeCharts) })
onUnmounted(() => {
  cancelCollection()
  window.removeEventListener('resize', resizeCharts)
  monitorChart?.dispose()
  monitorChart = null
  gaugeCharts.forEach(chart => chart?.dispose())
  gaugeCharts = [null, null, null]
})
</script>

<template>
  <div class="page asset-detail">
    <!-- 面包屑 -->
    <nav class="crumbs">
      <router-link to="/assets">服务器管理</router-link>
      <span class="crumb-sep">/</span>
      <span class="crumb-current">{{ asset.name || `#${assetId}` }}</span>
      <span class="crumb-id mono">ID {{ assetId }}</span>
    </nav>

    <!-- 顶部标题栏 -->
    <header class="titlebar">
      <div class="titlebar-row">
        <div class="title-identity">
          <div class="eyebrow">BARE METAL · {{ str(asset.bmc_protocol).toUpperCase() || 'REDFISH' }} · ID {{ assetId }}</div>
          <h1 class="server-name">
            <span class="name-text">{{ asset.name || (loading ? '正在读取资产…' : '未知资产') }}</span>
            <n-tag size="small" :bordered="false" :type="statusTag.tag" class="status-tag">{{ statusTag.label }}</n-tag>
          </h1>
          <div class="title-facts">
            <span class="fact"><em>SN</em><b class="mono">{{ asset.serial_number || '—' }}</b></span>
            <span class="fact"><em>BMC</em><b class="mono">{{ asset.bmc_address || '—' }}</b></span>
            <span class="fact"><em>IP</em><b class="mono">{{ asset.management_ip || '—' }}</b></span>
            <span class="fact"><em>SPEC</em><b class="mono">{{ specText }}</b></span>
          </div>
        </div>
        <div class="title-power">
          <div class="power-orb" :class="`tone-${statusTag.tone}`"><span class="orb-core"></span></div>
          <div class="power-caption">
            <strong>{{ powerLabel }}</strong>
            <small>{{ collectionStatusText }}</small>
          </div>
        </div>
      </div>

      <div class="titlebar-actions">
        <n-button size="medium" :disabled="!canPower" :loading="actionBusy === 'on'" @click="handlePowerAction('on')">开机</n-button>
        <n-button size="medium" :disabled="!canPower" :loading="actionBusy === 'off'" @click="handlePowerAction('off')">关机</n-button>
        <n-button size="medium" type="warning" ghost :disabled="!canPower" :loading="actionBusy === 'restart'" @click="handlePowerAction('restart')">重启</n-button>
        <n-button size="medium" type="primary" :disabled="!canOperate" @click="router.push(`/assets/${assetId}/sol`)">远程控制台</n-button>
        <n-dropdown trigger="click" :options="moreOptions" @select="onMoreSelect">
          <n-button size="medium">更多 ▾</n-button>
        </n-dropdown>
      </div>

      <p v-if="loadError" class="titlebar-error">资产加载失败：{{ loadError }}</p>
    </header>

    <!-- Tab 区 -->
    <div class="panel detail-panel">
      <n-tabs v-model:value="activeTab" type="line" animated display-directive="show:lazy" class="detail-tabs">
        <!-- ===== Tab 1 概览 ===== -->
        <n-tab-pane name="overview" tab="概览">
          <div class="overview-layout">
            <section class="card section">
              <header class="section-head">
                <h2>基础配置</h2>
                <span class="section-note">台账字段 + 最近一次采集快照</span>
              </header>
              <dl class="config-grid">
                <div v-for="field in configFields" :key="field.label" class="config-cell">
                  <dt>{{ field.label }}</dt>
                  <dd :class="{ mono: field.mono, empty: !field.value || field.value === '—', warn: field.tone === 'warn', danger: field.tone === 'danger' }">
                    {{ field.value || '—' }}
                    <em v-if="field.hint" class="cell-hint">{{ field.hint }}</em>
                  </dd>
                </div>
              </dl>
            </section>

            <aside class="overview-rail">
              <n-card class="uptime-card" :class="{ live: uptime.tone === 'live' }" :bordered="false" size="small">
                <div class="uptime-label">连续运行</div>
                <div class="uptime-value mono">
                  <template v-if="uptime.tone === 'live'">
                    <b>{{ uptime.days }}</b><i>天</i><b>{{ uptime.hours }}</b><i>时</i><b>{{ uptime.minutes }}</b><i>分</i>
                  </template>
                  <template v-else><b class="na">{{ uptime.text }}</b></template>
                </div>
                <div class="uptime-basis">{{ uptime.detail }}</div>
              </n-card>

              <n-card class="tag-card" :bordered="false" size="small">
                <template #header><span class="rail-title">标签</span></template>
                <template #header-extra>
                  <n-button text size="tiny" type="primary" :disabled="!canWrite" @click="openTagManager">管理</n-button>
                </template>
                <div v-if="boundTags.length" class="tag-list">
                  <n-tag v-for="tag in boundTags" :key="String(tag.id)" size="small" :bordered="false"
                    :style="{ background: tag.color ? String(tag.color) : undefined }">{{ tag.name || tag.id }}</n-tag>
                </div>
                <div v-else class="rail-empty">未绑定标签</div>
              </n-card>

              <n-card class="bmc-card" :bordered="false" size="small">
                <template #header><span class="rail-title">带外管理</span></template>
                <n-descriptions label-placement="left" :column="1" size="small" bordered class="rail-desc">
                  <n-descriptions-item v-for="item in bmcFields" :key="item.label" :label="item.label">
                    <span :class="{ mono: item.value.length <= 24 }">{{ item.value }}</span>
                  </n-descriptions-item>
                </n-descriptions>
              </n-card>
            </aside>
          </div>

          <section class="card section">
            <header class="section-head">
              <h2>电源操作历史</h2>
              <span class="section-note">{{ operationsTruncated ? '仅含最近 200 条操作' : '全部操作记录' }}</span>
              <n-button text size="small" type="primary" @click="activeTab = 'logs'">查看全部日志 →</n-button>
            </header>
            <n-data-table size="small" :columns="powerHistoryColumns" :data="powerHistory" :loading="operationsLoading"
              :pagination="false" :max-height="280" />
            <div v-if="!operationsLoading && !powerHistory.length" class="empty-hint">该设备尚无电源类操作记录</div>
          </section>
        </n-tab-pane>

        <!-- ===== Tab 2 监控 ===== -->
        <n-tab-pane name="monitor" tab="监控">
          <div class="monitor-toolbar">
            <div class="range-segment">
              <button v-for="item in rangeItems" :key="item.key" type="button"
                :class="{ active: monitorRange === item.key }" @click="monitorRange = item.key">
                {{ item.label }}
              </button>
            </div>
            <n-date-picker v-if="monitorRange === 'custom'" v-model:value="customRange" type="datetimerange" clearable
              size="small" />
            <n-checkbox-group class="metric-toggles" :value="activeMetrics" @update:value="onMetricChange">
              <n-checkbox v-for="metric in METRICS" :key="metric.key" :value="metric.key">
                <span class="metric-dot" :style="{ background: metric.color }"></span>{{ metric.label }}
              </n-checkbox>
            </n-checkbox-group>
            <n-button size="small" :loading="sensorLoading" @click="loadSensors().then(renderMonitor)">刷新样本</n-button>
          </div>

          <section class="card section">
            <header class="section-head">
              <h2>资源使用趋势</h2>
              <span class="section-note">{{ trendNote }}</span>
            </header>
            <div v-show="hasTrend" ref="monitorChartRef" class="chart-container large"></div>
            <div v-if="!hasTrend" class="trend-empty" role="status">
              <div class="trend-empty-mark mono">∅</div>
              <p>{{ trendEmptyReason }}</p>
            </div>
          </section>

          <section class="card section">
            <header class="section-head">
              <h2>实时传感器读数</h2>
              <span class="section-note">来源：采集快照 · {{ fmtTime(collection.collected_at) }}</span>
            </header>
            <div class="sensor-gauges">
              <div class="sensor-gauge-card">
                <div class="gauge-title">CPU 温度</div>
                <div ref="cpuTempRef" class="gauge-chart"></div>
                <div class="gauge-foot mono">{{ cpuTemp == null ? '无温度读数' : `${cpuTemp} °C` }}</div>
              </div>
              <div class="sensor-gauge-card">
                <div class="gauge-title">风扇平均转速</div>
                <div ref="fanRef" class="gauge-chart"></div>
                <div class="gauge-foot mono">{{ fanRpm == null ? '无转速读数' : `${fanCount} 个风扇 · 均值 ${fanRpm} RPM` }}</div>
              </div>
              <div class="sensor-gauge-card">
                <div class="gauge-title">整机功耗</div>
                <div ref="powerRef" class="gauge-chart"></div>
                <div class="gauge-foot mono">{{ powerWatts == null ? '无功耗读数' : `${powerWatts} W` }}</div>
              </div>
            </div>
            <n-data-table v-if="sensorTableRows.length" size="small" :columns="sensorColumns" :data="sensorTableRows"
              :max-height="260" :pagination="{ pageSize: 12 }" class="sensor-table" />
            <div v-else class="empty-hint">采集中未获得带读数的传感器；空列表不代表设备没有传感器，详见「硬件信息」的采集状态。</div>
          </section>
        </n-tab-pane>

        <!-- ===== Tab 3 硬件信息 ===== -->
        <n-tab-pane name="hardware" tab="硬件信息">
          <div class="hw-toolbar">
            <div class="hw-toolbar-text">
              <strong>{{ collectionStatusText }}</strong>
              <span v-if="collection.partial" class="hw-chip warn">部分采集</span>
              <span class="hw-caption">快照时间 {{ fmtTime(collection.collected_at) }} · 连续失败 {{ collection.consecutive_failures || 0 }} 次
                · {{ valueLabels[str(collection.health)] || '健康未知' }}</span>
            </div>
            <div class="head-actions">
              <n-button size="small" :disabled="collection.supported !== true" :loading="collectionLoading"
                @click="collectHardware">采集硬件信息</n-button>
              <n-button size="small" :loading="collectionLoading" @click="loadCollection().then(() => message.success('快照已刷新')).catch(e => message.error(errorText(e)))">刷新快照</n-button>
            </div>
          </div>
          <p v-if="collection.supported === false" class="hw-notice">当前 BMC 协议为 IPMI，Redfish 组件明细不可用；电源控制不受影响。</p>
          <p v-else-if="collectionNotice" class="hw-notice" role="status">{{ collectionNotice }}</p>

          <section v-for="item in hardwareSections" :key="item.key" class="card section hw-section">
            <header class="section-head">
              <h2>{{ item.title }}<span v-if="item.rows.length" class="count-chip mono">{{ item.rows.length }}</span></h2>
              <span class="section-note">{{ stateOf(item.group) }}</span>
            </header>
            <n-data-table v-if="item.rows.length" size="small" :columns="buildSectionColumns(item.rows, item.fields)"
              :data="item.rows" :pagination="false" :max-height="320" />
            <div v-else class="empty-hint">{{ item.empty }}</div>
          </section>

          <section v-if="biosRow.length" class="card section hw-section">
            <header class="section-head"><h2>BIOS（快照摘要）</h2></header>
            <n-data-table size="small" :columns="buildSectionColumns(biosRow, [{ key: 'name', label: '组件', width: 220 }, { key: 'version', label: '版本', mono: true }])"
              :data="biosRow" :pagination="false" />
          </section>

          <section v-if="ledgerHardware.length" class="card section hw-section">
            <header class="section-head">
              <h2>硬件台账<span class="count-chip mono">{{ ledgerHardware.length }}</span></h2>
              <span class="section-note">asset_hardwares 表（人工/外部录入，非 Redfish 快照）</span>
            </header>
            <n-data-table size="small" :columns="ledgerColumns" :data="ledgerHardware" :pagination="false" :max-height="320" />
          </section>
        </n-tab-pane>

        <!-- ===== Tab 4 网络配置 ===== -->
        <n-tab-pane name="network" tab="网络配置">
          <section class="card section">
            <header class="section-head">
              <h2>IP 分配<span v-if="ipAllocations.length" class="count-chip mono">{{ ipAllocations.length }}</span></h2>
              <span class="section-note">来源 ip_allocations</span>
            </header>
            <n-data-table v-if="ipAllocations.length" size="small" :columns="ipColumns" :data="ipAllocations"
              :loading="networkLoading" :pagination="false" :max-height="360" />
            <div v-else class="empty-hint">该设备没有 IP 分配记录。PXE 交付或静态绑定后会在此显示。</div>
          </section>

          <section class="card section">
            <header class="section-head">
              <h2>网卡接口<span v-if="networkInterfaces.length" class="count-chip mono">{{ networkInterfaces.length }}</span></h2>
              <span class="section-note">{{ stateOf('network') }}</span>
            </header>
            <n-data-table v-if="networkInterfaces.length" size="small" :columns="nicColumns" :data="networkInterfaces"
              :loading="networkLoading" :pagination="false" :max-height="320" />
            <div v-else class="empty-hint">采集快照中没有网卡明细，请先在「硬件信息」执行采集。</div>
          </section>

          <section class="card section">
            <header class="section-head"><h2>地址摘要</h2></header>
            <dl class="config-grid net-grid">
              <div class="config-cell"><dt>BMC 地址</dt><dd class="mono">{{ dash(asset.bmc_address) }}</dd></div>
              <div class="config-cell"><dt>业务 IP</dt><dd class="mono">{{ dash(asset.management_ip) }}</dd></div>
              <div class="config-cell"><dt>MAC 地址</dt><dd class="mono">{{ dash(asset.mac_address) }}</dd></div>
              <div class="config-cell"><dt>管理协议 / 端口</dt><dd class="mono">{{ str(asset.bmc_protocol).toUpperCase() || '—' }} : {{ dash(asset.bmc_port) }}</dd></div>
            </dl>
          </section>
        </n-tab-pane>

        <!-- ===== Tab 5 操作日志 ===== -->
        <n-tab-pane name="logs" tab="操作日志">
          <div class="logs-toolbar">
            <div class="filter-cluster">
              <n-select v-model:value="opKindFilter" :options="opKindOptions" size="small" clearable placeholder="操作类型"
                class="filter-select" @update:value="opPage = 1" />
              <n-select v-model:value="opStatusFilter" :options="opStatusOptions" size="small" clearable placeholder="执行状态"
                class="filter-select" @update:value="opPage = 1" />
              <span class="selection-hint">共 {{ filteredOperations.length }} / {{ operations.length }} 条</span>
            </div>
            <n-button size="small" :loading="operationsLoading" @click="loadOperations">重新加载</n-button>
          </div>
          <n-data-table size="small" :columns="operationColumns" :data="filteredOperations" :loading="operationsLoading"
            :pagination="operationPagination" :scroll-x="960" />
          <div v-if="!operationsLoading && !filteredOperations.length" class="empty-hint">
            {{ operations.length ? '当前筛选条件下没有记录' : '该设备尚无操作记录' }}
          </div>
          <p v-if="operationsTruncated" class="logs-footnote">后端单次最多返回 200 条（按最新优先），更早的记录请按时间范围在审计日志中检索。</p>
        </n-tab-pane>

        <!-- ===== Tab 6 远程控制台 ===== -->
        <n-tab-pane name="console" tab="远程控制台">
          <div class="console-toolbar">
            <div class="console-status" :class="{ active: !!activeSolSession }">
              <span class="session-dot"></span>
              <div>
                <strong>{{ activeSolSession ? '存在活跃 SOL 会话' : '当前无活跃 SOL 会话' }}</strong>
                <small v-if="activeSolSession">到期 {{ fmtTime(activeSolSession.expires_at) }} · 单会话上限 15 分钟</small>
                <small v-else>SOL 为串口重定向，同一时刻仅允许一个活动会话</small>
              </div>
            </div>
            <div class="head-actions">
              <n-button size="small" :loading="solLoading" @click="loadSolSessions">刷新会话</n-button>
              <n-button size="small" @click="copyText(solUrl, '控制台地址')">复制地址</n-button>
              <n-button size="small" @click="openSolWindow">新窗口打开</n-button>
              <n-button size="small" type="primary" :disabled="!canOperate" @click="router.push(solUrl)">进入控制台</n-button>
            </div>
          </div>


          <section class="card section">
            <header class="section-head">
              <h2>会话记录<span v-if="solSessions.length" class="count-chip mono">{{ solSessions.length }}</span></h2>
              <span class="section-note">来源 sol_sessions</span>
            </header>
            <n-data-table v-if="solSessions.length" size="small" :columns="solColumns" :data="solSessions.slice(0, 8)"
              :pagination="false" :max-height="260" />
            <div v-else class="empty-hint">尚无 SOL 会话记录</div>
          </section>
        </n-tab-pane>
      </n-tabs>
    </div>

    <!-- 编辑弹窗 -->
    <n-modal v-model:show="editing" preset="card" title="编辑资产" style="width: min(560px, 94vw)">
      <n-form label-placement="top">
        <div class="resource-form-grid">
          <n-form-item label="资产名称"><n-input v-model:value="editForm.name" placeholder="请输入资产名称" /></n-form-item>
          <n-form-item label="业务 IP"><n-input v-model:value="editForm.management_ip" placeholder="请输入业务 IP" /></n-form-item>
          <n-form-item label="BMC 地址"><n-input v-model:value="editForm.bmc_address" placeholder="请输入 BMC 地址" /></n-form-item>
          <n-form-item label="BMC 用户名"><n-input v-model:value="editForm.bmc_user" placeholder="请输入 BMC 用户名" /></n-form-item>
          <n-form-item label="BMC 密码（留空表示不修改）"><n-input v-model:value="editForm.bmc_secret" type="password" show-password-on="click" placeholder="留空则保持不变" /></n-form-item>
          <n-form-item label="MAC 地址"><n-input v-model:value="editForm.mac_address" placeholder="请输入 MAC 地址" /></n-form-item>
        </div>
        <div class="modal-actions">
          <n-button @click="editing = false">取消</n-button>
          <n-button type="primary" :loading="saving" @click="saveEdit">保存</n-button>
        </div>
      </n-form>
    </n-modal>

    <!-- 标签管理弹窗 -->
    <n-modal v-model:show="tagModal" preset="card" title="管理标签" style="width: min(440px, 94vw)">
      <n-form-item label="选择标签">
        <n-select v-model:value="selectedTagIds" multiple filterable :options="tagOptions" placeholder="请选择要绑定的标签" />
      </n-form-item>
      <p class="tag-modal-note">标签绑定通过 asset_tags 关联表维护，保存时会同时提交新增与解绑。</p>
      <div class="modal-actions">
        <n-button @click="tagModal = false">取消</n-button>
        <n-button type="primary" :loading="tagSaving" @click="saveTags">保存</n-button>
      </div>
    </n-modal>
  </div>
</template>

<style scoped>
/* ============================================================
   视觉基调：深色「精密运维控制台」标题栏 + 高密度数据面板
   约定：技术标识走等宽字体，数值走 tabular-nums，微标签走大写字距
   ============================================================ */
.asset-detail {
  --detail-mono: 'JetBrains Mono', 'SFMono-Regular', ui-monospace, Consolas, Menlo, monospace;
  display: flex;
  flex-direction: column;
  gap: 14px;
}

.mono {
  font-family: var(--detail-mono);
  font-size: .94em;
  letter-spacing: .2px;
}

.row-no {
  font-size: 11px;
  color: var(--color-text-disabled);
}

/* === 面包屑：压在页头之下的轻量导航层 === */
.crumbs {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 8px;
  font-size: var(--font-size-xs);
  color: var(--color-text-tertiary);
}

.crumbs a {
  color: var(--color-text-secondary);
  text-decoration: none;
  transition: color var(--transition-fast);
}

.crumbs a:hover {
  color: var(--color-primary);
}

.crumb-sep {
  color: var(--color-border-dark);
}

.crumb-current {
  font-weight: 600;
  color: var(--color-text-primary);
  word-break: break-all;
}

.crumb-id {
  margin-left: auto;
  letter-spacing: .6px;
}

/* 深色设备信息区独立定义前景色，避免浅色主题按钮对比不足。 */
.titlebar {
  --color-text-primary: #edf3fc;
  --color-text-secondary: #b5c5da;
  --color-border: #34475f;
  --color-bg: #172b43;
  --color-surface: #14263b;
  position: relative;
  overflow: hidden;
  padding: 20px 24px 18px;
  border: 1px solid var(--color-border);
  border-radius: var(--radius-xl);
  color: var(--color-text-primary);
  background: var(--color-surface);
  box-shadow: var(--shadow-sm);
}

/* 细网格底纹：让背景像一块机柜面板，而不是纯色的 hero 区块 */
.titlebar::before {
  content: '';
  position: absolute;
  inset: 0;
  pointer-events: none;
  background-image:
    linear-gradient(to right, rgba(255, 255, 255, .04) 1px, transparent 1px),
    linear-gradient(to bottom, rgba(255, 255, 255, .04) 1px, transparent 1px);
  background-size: 26px 26px;
}

.titlebar > * {
  position: relative;
}

.titlebar-row {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: 22px;
}

.title-identity {
  min-width: 0;
}

.titlebar .eyebrow {
  margin-bottom: 6px;
  font-family: var(--detail-mono);
  font-size: 10px;
  font-weight: 600;
  letter-spacing: 1.6px;
  text-transform: uppercase;
  color: color-mix(in srgb, var(--color-text-secondary) 78%, transparent);
}

.server-name {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 12px;
  margin: 0;
  font-size: 26px;
  font-weight: 700;
  line-height: 1.25;
  letter-spacing: -.2px;
}

.name-text {
  word-break: break-all;
}

.status-tag {
  font-weight: 600;
  letter-spacing: .4px;
}

/* 关键标识摘要：SN / BMC / IP / 规格，扫一眼即可定位机器 */
.title-facts {
  display: flex;
  flex-wrap: wrap;
  gap: 6px 22px;
  margin-top: 12px;
}

.fact {
  display: inline-flex;
  align-items: baseline;
  gap: 6px;
  font-size: var(--font-size-xs);
}

.fact em {
  font-style: normal;
  font-family: var(--detail-mono);
  font-size: 10px;
  letter-spacing: 1.2px;
  text-transform: uppercase;
  color: color-mix(in srgb, var(--color-text-secondary) 72%, transparent);
}

.fact b {
  font-weight: 600;
  color: var(--color-text-primary);
}

.title-power {
  display: flex;
  align-items: center;
  gap: 14px;
  padding: 12px 16px;
  border: 1px solid var(--color-border);
  border-radius: var(--radius-lg);
  background: var(--color-bg);
}

/* 电源光环：用 currentColor 驱动四态，避免为每个状态复制一套色值 */
.power-orb {
  position: relative;
  display: grid;
  place-items: center;
  width: 52px;
  height: 52px;
  border-radius: 50%;
  border: 1px solid color-mix(in srgb, currentColor 45%, transparent);
  box-shadow:
    inset 0 0 18px color-mix(in srgb, currentColor 20%, transparent),
    0 0 0 6px color-mix(in srgb, currentColor 8%, transparent);
}

.orb-core {
  width: 14px;
  height: 14px;
  border-radius: 50%;
  background: currentColor;
  box-shadow: 0 0 12px currentColor;
}

.power-orb.tone-running { color: #23C343; }
.power-orb.tone-stopped { color: #7A8C99; }
.power-orb.tone-fault { color: #F56060; }
.power-orb.tone-maintenance { color: #FF9A2E; }

.power-orb.tone-running .orb-core,
.power-orb.tone-fault .orb-core {
  animation: orb-pulse 2.4s ease-in-out infinite;
}

@keyframes orb-pulse {
  0%, 100% { opacity: 1; transform: scale(1); }
  50% { opacity: .5; transform: scale(.82); }
}

.power-caption {
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.power-caption strong {
  font-size: var(--font-size-base);
  font-weight: 600;
}

.power-caption small {
  font-size: 11px;
  line-height: 1.5;
  color: var(--color-text-secondary);
}

/* 操作台：按钮嵌在半透明托盘中，深色背景下依然清晰 */
.titlebar-actions {
  display: flex;
  flex-wrap: wrap;
  gap: 10px;
  margin-top: 16px;
  padding: 12px 14px;
  border: 1px solid var(--color-border);
  border-radius: var(--radius-lg);
  background: var(--color-bg);
}

.titlebar-actions :deep(.n-button--default-type) {
  --n-text-color: #edf3fc !important;
  --n-text-color-hover: #ffffff !important;
  --n-text-color-pressed: #ffffff !important;
  --n-text-color-focus: #ffffff !important;
  --n-border: 1px solid #61758e !important;
  --n-border-hover: 1px solid #90b6ed !important;
  --n-color: #203650 !important;
  --n-color-hover: #2b4665 !important;
}

.titlebar-error {
  margin: 12px 0 0;
  padding: 8px 12px;
  border-left: 2px solid var(--color-error);
  border-radius: 0 var(--radius-sm) var(--radius-sm) 0;
  background: color-mix(in srgb, var(--color-error) 16%, transparent);
  font-size: var(--font-size-xs);
  line-height: 1.7;
  color: #FFC9C9;
}

/* === Tab 导航 === */
.detail-tabs :deep(.n-tabs-tab) {
  font-weight: 600;
  letter-spacing: .3px;
}

/* === 通用分区 === */
.section {
  padding: 16px 18px 18px;
}

.section + .section,
.section:not(:first-child) {
  margin-top: 14px;
}

.section-head {
  display: flex;
  align-items: baseline;
  flex-wrap: wrap;
  gap: 8px 12px;
  padding-bottom: 12px;
  margin-bottom: 14px;
  border-bottom: 1px solid var(--color-border-light);
}

.section-head h2 {
  display: flex;
  align-items: center;
  margin: 0;
  font-size: var(--font-size-base);
  font-weight: 600;
  letter-spacing: .2px;
  color: var(--color-text-primary);
}

.section-note {
  font-size: var(--font-size-xs);
  color: var(--color-text-tertiary);
}

.count-chip {
  margin-left: 8px;
  padding: 1px 7px;
  border-radius: var(--radius-full);
  background: var(--color-primary-light);
  color: var(--color-primary);
  font-size: 11px;
  font-variant-numeric: tabular-nums;
}

/* === 概览：主配置网格 + 右侧状态轨 === */
.overview-layout {
  display: grid;
  grid-template-columns: minmax(0, 1fr) 320px;
  gap: 14px;
  align-items: start;
}

.config-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 1px;
  margin: 0;
  overflow: hidden;
  border: 1px solid var(--color-border-light);
  border-radius: var(--radius-md);
  background: var(--color-border-light);
}

.net-grid {
  grid-template-columns: repeat(auto-fit, minmax(220px, 1fr));
}

.config-cell {
  display: flex;
  flex-direction: column;
  gap: 4px;
  min-width: 0;
  padding: 11px 14px;
  background: var(--color-surface);
  transition: background var(--transition-fast);
}

.config-cell:hover {
  background: var(--color-surface-raised);
}

.config-cell dt {
  font-size: 12px;
  letter-spacing: .3px;
  color: var(--color-text-secondary);
}

.config-cell dd {
  margin: 0;
  font-size: var(--font-size-sm);
  font-variant-numeric: tabular-nums;
  color: var(--color-text-primary);
  word-break: break-word;
}

.config-cell dd.empty { color: var(--color-text-disabled); }
.config-cell dd.warn { color: var(--color-warning); }
.config-cell dd.danger { color: var(--color-error); }

.cell-hint {
  display: block;
  margin-top: 3px;
  font-style: normal;
  font-size: 11px;
  line-height: 1.6;
  color: var(--color-text-tertiary);
}

.overview-rail {
  position: sticky;
  top: 16px;
  display: flex;
  flex-direction: column;
  gap: 12px;
}

/* 连续运行卡片：延续标题栏的深色语言，形成轨道上的视觉锚点 */
.uptime-card {
  color: var(--color-text-primary);
  border: 1px solid var(--color-border);
  background: var(--color-surface);
}

.uptime-card :deep(.n-card__content) {
  padding: 14px 16px 16px;
}

.uptime-card.live {
  box-shadow: inset 0 0 0 1px color-mix(in srgb, var(--color-success) 38%, transparent);
}

.uptime-label {
  font-size: 11px;
  letter-spacing: 1px;
  text-transform: uppercase;
  color: var(--color-text-secondary);
}

.uptime-value {
  display: flex;
  align-items: baseline;
  gap: 4px;
  margin-top: 8px;
  font-variant-numeric: tabular-nums;
}

.uptime-value b {
  font-size: 30px;
  font-weight: 700;
  line-height: 1;
}

.uptime-value i {
  margin-right: 6px;
  font-style: normal;
  font-size: 12px;
  color: var(--color-text-secondary);
}

.uptime-value .na {
  font-size: 19px;
  font-weight: 600;
  color: var(--color-text-secondary);
}

.uptime-basis {
  margin-top: 10px;
  font-size: 11px;
  line-height: 1.7;
  color: var(--color-text-secondary);
}

.tag-card,
.bmc-card {
  border: 1px solid var(--color-border);
  background: var(--color-surface);
}

.tag-card :deep(.n-card-header),
.bmc-card :deep(.n-card-header) {
  padding: 12px 14px 6px;
}

.tag-card :deep(.n-card__content),
.bmc-card :deep(.n-card__content) {
  padding: 0 14px 14px;
}

.rail-title {
  font-size: var(--font-size-sm);
  font-weight: 600;
}

.rail-empty {
  font-size: var(--font-size-xs);
  color: var(--color-text-tertiary);
}

.rail-desc :deep(.n-descriptions-table-header) {
  width: 96px;
  font-size: 11px;
}

/* === 监控 === */
.monitor-toolbar {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 14px;
  padding: 10px 14px;
  margin-bottom: 14px;
  border: 1px solid var(--color-border);
  border-radius: var(--radius-lg);
  background: linear-gradient(180deg, var(--color-surface-raised), var(--color-surface));
}

.range-segment {
  display: inline-flex;
  flex-wrap: wrap;
  padding: 2px;
  border: 1px solid var(--color-border-light);
  border-radius: var(--radius-md);
  background: var(--color-bg);
}

.range-segment button {
  appearance: none;
  padding: 5px 12px;
  border: 0;
  border-radius: var(--radius-sm);
  background: transparent;
  font-size: var(--font-size-xs);
  font-variant-numeric: tabular-nums;
  color: var(--color-text-secondary);
  white-space: nowrap;
  cursor: pointer;
  transition: background var(--transition-fast), color var(--transition-fast), box-shadow var(--transition-fast);
}

.range-segment button:hover {
  color: var(--color-primary);
}

.range-segment button.active {
  font-weight: 600;
  color: var(--color-primary);
  background: var(--color-surface);
  box-shadow: var(--shadow-sm);
}

.metric-toggles {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 14px;
}

.metric-dot {
  display: inline-block;
  width: 8px;
  height: 8px;
  margin-right: 6px;
  border-radius: 2px;
  vertical-align: middle;
}

.trend-empty {
  display: grid;
  justify-items: center;
  gap: 10px;
  padding: 44px 16px;
  border: 1px dashed var(--color-border-dark);
  border-radius: var(--radius-md);
  background: var(--color-surface-raised);
}

.trend-empty-mark {
  font-size: 24px;
  color: var(--color-text-disabled);
}

.trend-empty p {
  max-width: 640px;
  margin: 0;
  font-size: var(--font-size-xs);
  line-height: 1.8;
  color: var(--color-text-tertiary);
  text-align: center;
}

.sensor-gauges {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(184px, 1fr));
  gap: 12px;
}

.sensor-gauge-card {
  padding: 10px 12px 12px;
  border: 1px solid var(--color-border);
  border-radius: var(--radius-md);
  background: var(--color-surface);
  transition: border-color var(--transition-fast), box-shadow var(--transition-fast);
}

.sensor-gauge-card:hover {
  border-color: var(--color-border-dark);
  box-shadow: var(--shadow-sm);
}

.gauge-title {
  font-size: 11px;
  font-weight: 600;
  letter-spacing: .6px;
  text-transform: uppercase;
  color: var(--color-text-tertiary);
}

.gauge-chart {
  height: 150px;
}

.gauge-foot {
  font-size: var(--font-size-xs);
  font-variant-numeric: tabular-nums;
  text-align: center;
  color: var(--color-text-secondary);
}

.sensor-table {
  margin-top: 14px;
}

/* === 硬件信息 === */
.hw-toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: 14px;
  padding: 12px 16px;
  border: 1px solid var(--color-border);
  border-radius: var(--radius-lg);
  background: linear-gradient(180deg, var(--color-surface-raised), var(--color-surface));
}

.hw-toolbar-text {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 10px;
  min-width: 0;
}

.hw-toolbar-text strong {
  font-size: var(--font-size-sm);
  color: var(--color-text-primary);
}

.hw-chip {
  padding: 2px 9px;
  border: 1px solid;
  border-radius: var(--radius-full);
  font-size: 11px;
}

.hw-chip.warn {
  color: var(--color-warning);
  border-color: color-mix(in srgb, var(--color-warning) 45%, transparent);
  background: var(--color-warning-light);
}

.hw-caption {
  font-size: var(--font-size-xs);
  font-variant-numeric: tabular-nums;
  color: var(--color-text-tertiary);
}

/* 采集类提示：解释「为什么这张表是空的」，而不是留给用户猜 */
.hw-notice {
  margin: 12px 0 0;
  padding: 10px 14px;
  border-left: 2px solid var(--color-warning);
  border-radius: 0 var(--radius-sm) var(--radius-sm) 0;
  background: var(--color-warning-light);
  font-size: var(--font-size-xs);
  line-height: 1.7;
  color: var(--color-text-secondary);
}

.hw-section :deep(.n-data-table) {
  font-variant-numeric: tabular-nums;
}

/* === 操作日志 === */
.logs-toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: 12px;
  margin-bottom: 12px;
}

.filter-cluster {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 8px;
}

.filter-select {
  width: 132px;
}

.selection-hint {
  font-size: var(--font-size-xs);
  color: var(--color-text-tertiary);
  white-space: nowrap;
}

.logs-footnote {
  margin: 12px 0 0;
  font-size: 11px;
  line-height: 1.7;
  color: var(--color-text-tertiary);
}

/* === 远程控制台 === */
.console-toolbar {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: 16px;
  padding: 12px 16px;
  margin-bottom: 14px;
  border: 1px solid var(--color-border);
  border-radius: var(--radius-lg);
  background: linear-gradient(180deg, var(--color-surface-raised), var(--color-surface));
}

.console-status {
  display: flex;
  align-items: flex-start;
  gap: 10px;
  min-width: 0;
}

.console-status strong {
  display: block;
  font-size: var(--font-size-sm);
  color: var(--color-text-primary);
}

.console-status small {
  display: block;
  margin-top: 2px;
  font-size: 11px;
  line-height: 1.6;
  color: var(--color-text-tertiary);
}

.session-dot {
  flex: none;
  width: 8px;
  height: 8px;
  margin-top: 6px;
  border-radius: 50%;
  background: var(--color-info);
  box-shadow: 0 0 0 3px color-mix(in srgb, var(--color-info) 18%, transparent);
}

.console-status.active .session-dot {
  background: var(--color-success);
  box-shadow: 0 0 0 3px color-mix(in srgb, var(--color-success) 20%, transparent);
}

.console-dots {
  display: inline-flex;
  gap: 5px;
}

.console-dots i {
  width: 9px;
  height: 9px;
  border-radius: 50%;
  background: #30363D;
}

.console-dots i:nth-child(1) { background: #CB2634; }
.console-dots i:nth-child(2) { background: #D25F00; }
.console-dots i:nth-child(3) { background: #009A29; }

.console-title {
  flex: 1;
  text-align: center;
  letter-spacing: .6px;
  color: var(--color-text-secondary);
  word-break: break-all;
}

.console-close {
  appearance: none;
  padding: 2px 10px;
  border: 1px solid rgba(255, 255, 255, .2);
  border-radius: var(--radius-full);
  background: transparent;
  font-size: 11px;
  color: var(--color-text-secondary);
  cursor: pointer;
  transition: color var(--transition-fast), border-color var(--transition-fast);
}

.console-close:hover {
  color: #FFFFFF;
  border-color: rgba(255, 255, 255, .45);
}

.console-embed {
  height: min(72vh, 620px);
  background: #0D1117;
}

.console-embed iframe {
  display: block;
  width: 100%;
  height: 100%;
  border: 0;
}

/* 未加载内嵌终端时的启动面板，保持终端外框不塌陷 */
.console-pad {
  display: flex;
  align-items: flex-start;
  gap: 18px;
  min-height: 240px;
  padding: 34px 26px;
  background: #0D1117;
  color: #C6D2DE;
}

.pad-mark {
  font-size: 22px;
  line-height: 1;
  color: var(--color-success);
}

.pad-text strong {
  display: block;
  margin-bottom: 6px;
  font-size: var(--font-size-base);
  color: #E8EEF5;
}

.pad-text p {
  max-width: 660px;
  margin: 0;
  font-size: var(--font-size-xs);
  line-height: 1.85;
  color: var(--color-text-secondary);
}

.pad-actions {
  display: flex;
  flex-wrap: wrap;
  gap: 10px;
  margin-top: 14px;
}

/* === 弹窗补充 === */
.tag-modal-note {
  margin: 0 0 4px;
  font-size: 11px;
  line-height: 1.7;
  color: var(--color-text-tertiary);
}

/* === 暗色主题微调：仅补足对比，不重绘布局 === */
[data-theme='dark'] .titlebar {
  border-color: #22384B;
}

[data-theme='dark'] .config-cell {
  background: var(--color-surface);
}

[data-theme='dark'] .config-cell:hover {
  background: var(--color-surface-hover);
}

[data-theme='dark'] .range-segment {
  background: var(--color-surface-raised);
}

/* === 响应式：窄屏下右轨回落为横向卡片，配置网格退成单列 === */
@media (max-width: 1180px) {
  .overview-layout {
    grid-template-columns: minmax(0, 1fr);
  }

  .overview-rail {
    position: static;
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(240px, 1fr));
    align-items: stretch;
  }
}

@media (max-width: 760px) {
  .config-grid {
    grid-template-columns: minmax(0, 1fr);
  }

  .server-name {
    font-size: 21px;
  }

  .title-power {
    width: 100%;
  }

  .crumb-id {
    margin-left: 0;
  }
}

/* === 降低动效偏好：光环脉冲与过渡全部收起 === */
@media (prefers-reduced-motion: reduce) {
  .orb-core {
    animation: none;
  }

  .crumbs a,
  .config-cell,
  .sensor-gauge-card,
  .range-segment button,
  .console-close {
    transition: none;
  }
}
</style>
