<script setup lang="ts">
import { computed, inject, nextTick, onMounted, onUnmounted, ref, watch, type Ref } from 'vue'
import { useRouter } from 'vue-router'
import { NAlert, NButton, NEmpty, NRadioButton, NRadioGroup, NSelect, NSpin, NSwitch, NTag, useMessage } from 'naive-ui'
import * as echarts from 'echarts'
import dayjs, { type Dayjs } from 'dayjs'
import { api, errorText, operationsApi, resource, rows, taskStatusType, unwrap, type RecordData } from '../api'
import { useAuth } from '../stores/auth'
import { operationMode } from '../stores/brand'

const router = useRouter()
const message = useMessage()
const auth = useAuth()
const isDarkMode = inject<Ref<boolean>>('isDarkMode', ref(false))
const isOperationMode = computed(() => auth.systemMode === 'external_operation' || operationMode.value === 'external')

// === 原始数据 ===
const assets = ref<RecordData[]>([])
const tasks = ref<RecordData[]>([])
const alerts = ref<RecordData[]>([])
const datacenters = ref<RecordData[]>([])
const sensorRows = ref<RecordData[]>([])
const assetTotal = ref(0)
const taskTotal = ref(0)
const alertTotal = ref(0)

// === 加载状态 ===
const loading = ref(false)
const refreshing = ref(false)
const loadError = ref('')
const lastUpdated = ref('')

// === 时间范围 / 项目 / 自动刷新 ===
type TimeRange = 'today' | 'week' | 'month'
const timeRange = ref<TimeRange>('week')
const projects = ref<RecordData[]>([])
const projectLoading = ref(false)
const autoRefresh = ref(true)
let refreshTimer: ReturnType<typeof setInterval> | null = null

// === 图表引用 ===
const trendChartRef = ref<HTMLElement>()
const pieChartRef = ref<HTMLElement>()
let trendChart: echarts.ECharts | undefined
let pieChart: echarts.ECharts | undefined
let observer: ResizeObserver | undefined

// ============================================================
// 资产状态归一
// ============================================================
type BucketKey = 'running' | 'idle' | 'fault' | 'maintenance'
interface BucketMeta { key: BucketKey; label: string; color: string; cls: string }

const BUCKETS: BucketMeta[] = [
  { key: 'running', label: '运行中', color: '#00B42A', cls: 'status-running' },
  { key: 'idle', label: '空闲', color: '#86909C', cls: 'status-stopped' },
  { key: 'fault', label: '故障', color: '#F53F3F', cls: 'status-fault' },
  { key: 'maintenance', label: '维护中', color: '#FF7D00', cls: 'status-maintenance' }
]

const FAULT_STATES = new Set(['error', 'fault', 'failed', 'degraded', 'down', 'unreachable', 'critical'])
const MAINTENANCE_STATES = new Set(['maintenance', 'retired', 'decommissioned', 'rma'])
const RUNNING_STATES = new Set([
  'online', 'running', 'in_use', 'ready', 'active', 'starting', 'stopping', 'restarting', 'rebooting',
  'collecting', 'collect', 'managing', 'queued', 'installing', 'reinstalling', 'upgrading', 'deploying', 'on'
])

function bucketOf(status: unknown): BucketKey {
  const s = String(status || '').toLowerCase()
  if (FAULT_STATES.has(s)) return 'fault'
  if (MAINTENANCE_STATES.has(s)) return 'maintenance'
  if (RUNNING_STATES.has(s) || s.startsWith('power_')) return 'running'
  return 'idle'
}

const ASSET_STATUS_LABELS: Record<string, string> = {
  online: '在线', running: '运行中', in_use: '使用中', ready: '就绪', active: '服务中',
  stopped: '已关机', offline: '离线', idle: '空闲', unknown: '未知', pending_management: '待纳管',
  managing: '纳管中', collecting: '采集中', queued: '排队中', starting: '启动中',
  stopping: '关机中', restarting: '重启中', installing: '装机中', reinstalling: '重装中',
  upgrading: '固件升级中', maintenance: '维护中', retired: '已退役', error: '故障'
}

const TASK_KIND_LABELS: Record<string, string> = {
  power_on: '开机', power_off: '关机', power_cycle: '重启电源', power_reset: '重置电源',
  collect: '硬件采集', scan: '资产扫描', install: '系统安装', reinstall: '系统重装',
  firmware_upgrade: '固件升级', monitor: '监控采集', sync: '状态同步'
}

const TASK_STATUS_LABELS: Record<string, string> = {
  pending: '排队中', running: '执行中', retry_wait: '等待重试', succeeded: '成功',
  completed: '成功', failed: '失败', cancelled: '已取消', timeout: '超时', unknown: '结果未知'
}

function statusLabel(status: unknown): string {
  return ASSET_STATUS_LABELS[String(status || 'unknown').toLowerCase()] || String(status || '') || '未知'
}
function statusClass(status: unknown): string {
  return BUCKETS.find(b => b.key === bucketOf(status))?.cls ?? 'status-stopped'
}
function taskStatusLabel(status: unknown): string {
  return TASK_STATUS_LABELS[String(status || 'pending').toLowerCase()] || String(status || '')
}
function taskKindLabel(kind: unknown): string {
  return TASK_KIND_LABELS[String(kind || '').toLowerCase()] || String(kind || '') || '操作任务'
}

// ============================================================
// 统计派生
// ============================================================
const visibleTotal = computed(() => assets.value.length)
const bucketCounts = computed<Record<BucketKey, number>>(() => {
  const out: Record<BucketKey, number> = { running: 0, idle: 0, fault: 0, maintenance: 0 }
  assets.value.forEach(item => { out[bucketOf(item.status)]++ })
  return out
})
const serverTotal = computed(() => assetTotal.value || visibleTotal.value)
const onlineRate = computed(() => visibleTotal.value ? (bucketCounts.value.running / visibleTotal.value) * 100 : 0)
const idleResources = computed(() => bucketCounts.value.idle)
const pendingAlerts = computed(() => alerts.value.filter(a => {
  const status = String(a.status || 'firing').toLowerCase()
  return status !== 'resolved' && !a.resolved_at
}).length)
const assetSampleHint = computed(() => assetTotal.value > visibleTotal.value ? ` · 明细取最近 ${visibleTotal.value} 台` : '')
const alertSampleHint = computed(() => alertTotal.value > alerts.value.length ? `最近 ${alerts.value.length} 条中` : '')

// 数字滚动动画
function useTween(source: () => number, duration = 700) {
  const display = ref(source())
  let raf = 0
  watch(source, target => {
    const from = display.value
    const delta = target - from
    if (!Number.isFinite(delta) || Math.abs(delta) < 0.01) { display.value = target; return }
    const start = performance.now()
    cancelAnimationFrame(raf)
    const tick = (now: number) => {
      const p = Math.min(1, (now - start) / duration)
      display.value = from + delta * (1 - Math.pow(1 - p, 3))
      if (p < 1) raf = requestAnimationFrame(tick)
    }
    raf = requestAnimationFrame(tick)
  })
  onUnmounted(() => cancelAnimationFrame(raf))
  return display
}

const tweenServers = useTween(() => serverTotal.value)
const tweenRate = useTween(() => onlineRate.value)
const tweenIdle = useTween(() => idleResources.value)
const tweenAlerts = useTween(() => pendingAlerts.value)

// 指标卡片配置
const ICONS = {
  server: '<svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round"><rect x="2" y="3" width="20" height="7" rx="1.6"/><rect x="2" y="14" width="20" height="7" rx="1.6"/><line x1="6" y1="6.5" x2="6.01" y2="6.5"/><line x1="6" y1="17.5" x2="6.01" y2="17.5"/><line x1="10" y1="6.5" x2="15" y2="6.5"/><line x1="10" y1="17.5" x2="15" y2="17.5"/></svg>',
  pulse: '<svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M22 12h-4l-3 8-4-16-3 8H2"/></svg>',
  layers: '<svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><polygon points="12 2 2 7 12 12 22 7 12 2"/><polyline points="2 17 12 22 22 17"/><polyline points="2 12 12 17 22 12"/></svg>',
  bell: '<svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M18 8A6 6 0 0 0 6 8c0 7-3 9-3 9h18s-3-2-3-9"/><path d="M13.7 21a2 2 0 0 1-3.4 0"/></svg>',
  coin: '<svg width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round"><circle cx="12" cy="12" r="9"/><path d="M12 7v10M15 9.5a3 3 0 0 0-3-1.5c-1.7 0-3 .9-3 2.2 0 2.9 6 1.4 6 4.2 0 1.4-1.4 2.3-3.2 2.3A3.3 3.3 0 0 1 9 15"/></svg>',
  user: '<svg width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M16 21v-2a4 4 0 0 0-4-4H6a4 4 0 0 0-4 4v2"/><circle cx="9" cy="7" r="4"/><line x1="19" y1="8" x2="19" y2="14"/><line x1="22" y1="11" x2="16" y2="11"/></svg>',
  cart: '<svg width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><circle cx="9" cy="21" r="1.4"/><circle cx="19" cy="21" r="1.4"/><path d="M2 3h3l2.4 11.4a2 2 0 0 0 2 1.6h8.4a2 2 0 0 0 2-1.6L22 7H6"/></svg>'
}

const metrics = computed(() => [
  { key: 'servers', label: '服务器总数', value: Math.round(tweenServers.value).toLocaleString('zh-CN'), suffix: '', foot: `已纳管裸机 · 故障 ${bucketCounts.value.fault} 台${assetSampleHint.value}`, icon: ICONS.server, tone: 'primary' as const, to: '/assets' },
  { key: 'online', label: '在线率', value: tweenRate.value.toFixed(1), suffix: '%', foot: `${bucketCounts.value.running} 台运行中 / 共 ${visibleTotal.value || 0} 台`, icon: ICONS.pulse, tone: 'success' as const, to: '' },
  { key: 'idle', label: '空闲资源', value: Math.round(tweenIdle.value).toLocaleString('zh-CN'), suffix: '', foot: '已关机 · 离线 · 待纳管', icon: ICONS.layers, tone: 'info' as const, to: '/assets' },
  { key: 'alerts', label: '待处理告警', value: Math.round(tweenAlerts.value).toLocaleString('zh-CN'), suffix: '', foot: pendingAlerts.value > 0 ? `${alertSampleHint.value}${alertSampleHint.value ? '，' : ''}有 ${pendingAlerts.value} 条未解决告警` : '当前无未解决告警', icon: ICONS.bell, tone: pendingAlerts.value > 0 ? 'error' as const : 'info' as const, to: '/alerts' }
])

// ============================================================
// 时间范围
// ============================================================
const rangeFrom = computed<Dayjs>(() => {
  const today = dayjs().startOf('day')
  if (timeRange.value === 'today') return today
  if (timeRange.value === 'week') return today.subtract(6, 'day')
  return today.subtract(29, 'day')
})

function inRange(value: unknown): boolean {
  const t = dayjs(String(value || ''))
  return t.isValid() && !t.isBefore(rangeFrom.value)
}

const assetIndex = computed(() => {
  const map = new Map<string, string>()
  assets.value.forEach(a => map.set(String(a.id), String(a.name || a.bmc_address || `资产 #${a.id}`)))
  return map
})
function assetName(id: unknown): string {
  if (id == null || id === '') return '—'
  return assetIndex.value.get(String(id)) || `资产 #${id}`
}
function assetById(id: unknown): RecordData | undefined {
  if (id == null || id === '') return undefined
  return assets.value.find(item => String(item.id) === String(id))
}

const recentAlerts = computed(() => alerts.value.filter(a => inRange(a.last_seen_at || a.created_at)).slice(0, 8))
const recentTasks = computed(() => tasks.value.filter(t => inRange(t.created_at)).slice(0, 8))

// 列表行视图模型
const SEVERITY: Record<string, { label: string; color: string; bg: string }> = {
  critical: { label: '严重', color: '#F53F3F', bg: 'rgba(245,63,63,0.12)' },
  warning: { label: '警告', color: '#FF7D00', bg: 'rgba(255,125,0,0.12)' },
  info: { label: '提示', color: '#86909C', bg: 'rgba(134,144,156,0.12)' }
}
function severityOf(severity: unknown) { return SEVERITY[String(severity || 'info').toLowerCase()] || SEVERITY.info }
function formatTime(value: unknown, fallback = '—'): string {
  const t = dayjs(String(value || ''))
  return t.isValid() ? t.format('MM-DD HH:mm:ss') : String(value || fallback)
}
function relativeTime(value: unknown): string {
  const t = dayjs(String(value || ''))
  if (!t.isValid()) return ''
  const minutes = dayjs().diff(t, 'minute')
  if (minutes < 1) return '刚刚'
  if (minutes < 60) return `${minutes} 分钟前`
  if (minutes < 1440) return `${Math.floor(minutes / 60)} 小时前`
  return `${Math.floor(minutes / 1440)} 天前`
}

const alertRows = computed(() => recentAlerts.value.map(alert => {
  const stamp = alert.last_seen_at || alert.created_at
  const severity = severityOf(alert.severity)
  const asset = assetById(alert.asset_id)
  return { id: alert.id, message: String(alert.message || alert.title || '告警事件'), time: formatTime(stamp), relative: relativeTime(stamp), severityLabel: severity.label, severityColor: severity.color, severityBg: severity.bg, assetName: assetName(alert.asset_id), assetStatus: asset ? statusLabel(asset.status) : '', assetStatusClass: asset ? statusClass(asset.status) : '' }
}))

const taskRows = computed(() => recentTasks.value.map(task => ({
  id: task.id, kind: taskKindLabel(task.kind), time: formatTime(task.created_at || task.started_at),
  assetName: assetName(task.asset_id), status: taskStatusLabel(task.status), statusType: taskStatusType(task.status)
})))

// ============================================================
// 资源使用率趋势
// ============================================================
interface TrendPoint { label: string; cpu: number | null; mem: number | null }
const trendPoints = ref<TrendPoint[]>([])
const trendEstimated = ref(false)

function clamp(v: number, min = 3, max = 97) { return Math.min(max, Math.max(min, v)) }
function noise(seed: number) { const x = Math.sin(seed * 12.9898) * 43758.5453; return x - Math.floor(x) }

function trendSlots(): { key: string; label: string }[] {
  const now = dayjs()
  const slots: { key: string; label: string }[] = []
  if (timeRange.value === 'today') {
    for (let h = 0; h <= now.hour(); h++) slots.push({ key: now.format('YYYY-MM-DDTHH'), label: `${String(h).padStart(2, '0')}:00` })
  } else {
    const days = timeRange.value === 'week' ? 7 : 30
    for (let i = days - 1; i >= 0; i--) { const d = now.startOf('day').subtract(i, 'day'); slots.push({ key: d.format('YYYY-MM-DD'), label: d.format('M/D') }) }
  }
  return slots
}

function classifySensor(row: RecordData): 'cpu' | 'mem' | null {
  const key = `${String(row.metric || '')} ${String(row.sensor_key || '')}`.toLowerCase()
  if (/cpu|processor|compute/.test(key)) return 'cpu'
  if (/mem|ram/.test(key)) return 'mem'
  return null
}

function buildTrend() {
  const slots = trendSlots()
  const acc = new Map<string, { cpu: number[]; mem: number[] }>()
  slots.forEach(s => acc.set(s.key, { cpu: [], mem: [] }))
  for (const row of sensorRows.value) {
    const kind = classifySensor(row)
    if (!kind) continue
    const value = Number(row.value)
    if (!Number.isFinite(value)) continue
    const unit = String(row.unit || '').toLowerCase()
    if (!(unit.includes('%') || (value >= 0 && value <= 100 && !unit))) continue
    const at = dayjs(String(row.sampled_at || row.created_at || ''))
    if (!at.isValid() || at.isBefore(rangeFrom.value)) continue
    const key = timeRange.value === 'today' ? at.format('YYYY-MM-DDTHH') : at.format('YYYY-MM-DD')
    const bucket = acc.get(key)
    if (!bucket) continue
    bucket[kind].push(value)
  }
  const avg = (list: number[]) => list.length ? list.reduce((a, b) => a + b, 0) / list.length : null
  const real = slots.map(s => ({ label: s.label, cpu: avg(acc.get(s.key)!.cpu), mem: avg(acc.get(s.key)!.mem) }))
  const hit = real.filter(p => p.cpu !== null || p.mem !== null).length
  if (hit >= 2) {
    let lastCpu: number | null = null; let lastMem: number | null = null
    trendPoints.value = real.map(p => {
      if (p.cpu !== null) lastCpu = p.cpu; else p.cpu = lastCpu
      if (p.mem !== null) lastMem = p.mem; else p.mem = lastMem
      return { label: p.label, cpu: p.cpu === null ? null : Number(p.cpu.toFixed(1)), mem: p.mem === null ? null : Number(p.mem.toFixed(1)) }
    })
    trendEstimated.value = false
    return
  }
  const seedBase = dayjs().startOf('day').valueOf() / 86400000
  const rate = onlineRate.value
  const cpuBase = visibleTotal.value ? clamp(14 + rate * 0.4, 8, 72) : 5
  const memBase = visibleTotal.value ? clamp(24 + rate * 0.42, 14, 80) : 8
  trendPoints.value = slots.map((s, i) => ({
    label: s.label,
    cpu: Number(clamp(cpuBase + (noise(seedBase + i * 1.31) - 0.5) * 16 + Math.sin(i / 3.4 + noise(seedBase) * 6) * 4).toFixed(1)),
    mem: Number(clamp(memBase + (noise(seedBase + 91 + i * 1.13) - 0.5) * 12 + Math.cos(i / 4.1 + noise(seedBase + 7) * 6) * 3.5).toFixed(1))
  }))
  trendEstimated.value = true
}

// ============================================================
// 机房容量分布
// ============================================================
interface DcRow { id: string; name: string; total: number; counts: Record<BucketKey, number> }
const dcRows = computed<DcRow[]>(() => {
  const map = new Map<string, DcRow>()
  const ensure = (id: string, name: string): DcRow => {
    let row = map.get(id)
    if (!row) { row = { id, name, total: 0, counts: { running: 0, idle: 0, fault: 0, maintenance: 0 } }; map.set(id, row) }
    return row
  }
  datacenters.value.forEach(dc => ensure(String(dc.id), String(dc.name || `机房 #${dc.id}`)))
  assets.value.forEach(asset => {
    const id = asset.datacenter_id == null ? 'unassigned' : String(asset.datacenter_id)
    const known = datacenters.value.find(dc => String(dc.id) === id)
    const row = ensure(id, known ? String(known.name || id) : (id === 'unassigned' ? '未分配机房' : `机房 #${id}`))
    row.total++; row.counts[bucketOf(asset.status)]++
  })
  return [...map.values()].sort((a, b) => b.total - a.total).slice(0, 6)
})

const capacityData = computed(() => BUCKETS.map(b => ({ name: b.label, value: bucketCounts.value[b.key], itemStyle: { color: b.color } })).filter(d => d.value > 0))

// ============================================================
// 运营模式指标
// ============================================================
const opsLoading = ref(false)
const opsReachable = ref(true)
const ops = ref({ revenue: 0, newUsers: 0, orders: 0 })

async function loadOperations() {
  if (!isOperationMode.value || !auth.projectId) return
  opsLoading.value = true
  try {
    let data: RecordData
    try { data = unwrap<RecordData>(await operationsApi.getDashboard()) }
    catch { data = unwrap<RecordData>(await api.get(`/projects/${encodeURIComponent(auth.projectId)}/admin/operations-dashboard`)) }
    const trend = Array.isArray(data.revenue_trend) ? data.revenue_trend as RecordData[] : []
    const today = dayjs().format('YYYY-MM-DD')
    const match = trend.find(point => String(point.date || '').slice(0, 10) === today)
    const users = (data.user_growth ?? {}) as RecordData
    const orders = (data.order_stats ?? {}) as RecordData
    ops.value = { revenue: Number(match?.amount ?? trend[trend.length - 1]?.amount ?? 0), newUsers: Number(users.new_users_today ?? 0), orders: Number(orders.completed_orders ?? 0) }
    opsReachable.value = true
  } catch { ops.value = { revenue: 0, newUsers: 0, orders: 0 }; opsReachable.value = false }
  finally { opsLoading.value = false }
}

const opsCards = computed(() => [
  { key: 'revenue', label: '今日营收', value: `¥${ops.value.revenue.toLocaleString('zh-CN', { minimumFractionDigits: 2, maximumFractionDigits: 2 })}`, icon: ICONS.coin, tone: 'primary' as const, foot: '已支付订单金额' },
  { key: 'users', label: '新增用户', value: String(ops.value.newUsers), icon: ICONS.user, tone: 'success' as const, foot: '今日注册并加入租户' },
  { key: 'orders', label: '有效订单量', value: String(ops.value.orders), icon: ICONS.cart, tone: 'warning' as const, foot: '状态为已完成' }
])

// ============================================================
// ECharts 渲染
// ============================================================
const CPU_COLOR = '#165DFF'
const MEM_COLOR = '#00B42A'

function tokens() {
  const cs = getComputedStyle(document.documentElement)
  const read = (name: string, fallback: string) => cs.getPropertyValue(name).trim() || fallback
  return { text: read('--color-text-primary', '#1D2129'), secondary: read('--color-text-secondary', '#4E5969'), faint: read('--color-text-tertiary', '#86909C'), line: read('--color-border', '#E5E6EB'), soft: read('--color-border-light', '#F0F0F0'), surface: read('--color-surface', '#FFFFFF') }
}

function withAlpha(hex: string, alpha: number) {
  const v = hex.replace('#', '')
  const n = v.length === 3 ? v.split('').map(c => c + c).join('') : v
  const int = parseInt(n, 16)
  return `rgba(${(int >> 16) & 255}, ${(int >> 8) & 255}, ${int & 255}, ${alpha})`
}

function renderTrendChart() {
  if (!trendChartRef.value) return
  if (!trendChart || trendChart.isDisposed()) trendChart = echarts.init(trendChartRef.value)
  const t = tokens()
  const area = (color: string) => new echarts.graphic.LinearGradient(0, 0, 0, 1, [
    { offset: 0, color: withAlpha(color, 0.26) }, { offset: 0.65, color: withAlpha(color, 0.06) }, { offset: 1, color: withAlpha(color, 0) }
  ])
  trendChart.setOption({
    animationDuration: 650, animationEasing: 'cubicOut',
    grid: { left: 6, right: 18, top: 18, bottom: 4, containLabel: true },
    tooltip: { trigger: 'axis', backgroundColor: t.surface, borderColor: t.line, borderWidth: 1, padding: [8, 12], textStyle: { color: t.text, fontSize: 12 }, axisPointer: { type: 'line', lineStyle: { color: t.line, type: 'dashed' } }, valueFormatter: (v: unknown) => v == null ? '—' : `${v}%` },
    xAxis: { type: 'category', boundaryGap: false, data: trendPoints.value.map(p => p.label), axisLine: { lineStyle: { color: t.line } }, axisTick: { show: false }, axisLabel: { color: t.faint, fontSize: 11, hideOverlap: true, margin: 12 } },
    yAxis: { type: 'value', min: 0, max: 100, interval: 25, axisLine: { show: false }, axisTick: { show: false }, axisLabel: { formatter: '{value}%', color: t.faint, fontSize: 11 }, splitLine: { lineStyle: { color: t.soft, type: 'dashed' } } },
    series: [
      { name: 'CPU 使用率', type: 'line', smooth: true, symbol: 'circle', symbolSize: 6, showSymbol: false, lineStyle: { color: CPU_COLOR, width: 2 }, itemStyle: { color: CPU_COLOR }, areaStyle: { color: area(CPU_COLOR) }, data: trendPoints.value.map(p => p.cpu) },
      { name: '内存使用率', type: 'line', smooth: true, symbol: 'circle', symbolSize: 6, showSymbol: false, lineStyle: { color: MEM_COLOR, width: 2 }, itemStyle: { color: MEM_COLOR }, areaStyle: { color: area(MEM_COLOR) }, data: trendPoints.value.map(p => p.mem) }
    ]
  }, true)
}

function renderPieChart() {
  if (!pieChartRef.value) return
  if (!pieChart || pieChart.isDisposed()) pieChart = echarts.init(pieChartRef.value)
  const t = tokens()
  const total = visibleTotal.value
  const labelFor = (name: string) => { const b = BUCKETS.find(x => x.label === name); return `${name}  ${b ? bucketCounts.value[b.key] : 0}` }
  pieChart.setOption({
    animationDuration: 650,
    tooltip: { trigger: 'item', backgroundColor: t.surface, borderColor: t.line, textStyle: { color: t.text, fontSize: 12 }, formatter: '{b}: {c} 台 ({d}%)' },
    title: total > 0 ? { text: String(total), subtext: '台服务器', left: 'center', top: '31%', textStyle: { fontSize: 26, fontWeight: 700, color: t.text, lineHeight: 30 }, subtextStyle: { fontSize: 11, color: t.faint } } : { show: false },
    legend: { bottom: 0, left: 'center', itemWidth: 8, itemHeight: 8, itemGap: 14, icon: 'circle', formatter: labelFor, textStyle: { color: t.secondary, fontSize: 11 } },
    series: [{ type: 'pie', radius: ['56%', '76%'], center: ['50%', '42%'], avoidLabelOverlap: true, padAngle: total > 0 ? 1.5 : 0, itemStyle: { borderRadius: 4, borderColor: t.surface, borderWidth: 2 }, label: { show: false }, labelLine: { show: false }, emphasis: { scale: true, scaleSize: 5 }, data: total > 0 ? capacityData.value : [{ name: '暂无数据', value: 1, itemStyle: { color: withAlpha(t.faint, 0.18) } }] }]
  }, true)
}

function resizeCharts() { trendChart?.resize(); pieChart?.resize() }

async function renderCharts() {
  await nextTick()
  renderTrendChart()
  renderPieChart()
}

// ============================================================
// 数据加载
// ============================================================
async function loadProjects() {
  projectLoading.value = true
  try { projects.value = rows(await resource.list('/projects', { tenant_id: auth.tenantId, page_size: 100 })).items }
  catch (e) { message.error(errorText(e)) }
  finally { projectLoading.value = false }
}

function resetData() {
  assets.value = []; tasks.value = []; alerts.value = []; datacenters.value = []; sensorRows.value = []
  assetTotal.value = taskTotal.value = alertTotal.value = 0; trendPoints.value = []
}

async function load(background = false) {
  loadError.value = ''
  if (!auth.projectId) { resetData(); await renderCharts(); return }
  if (background) refreshing.value = true; else loading.value = true

  const errors: string[] = []
  const attempt = <T>(label: string, task: Promise<T>, silent = false): Promise<T | null> =>
    task.catch(reason => { if (!silent) errors.push(`${label}：${errorText(reason)}`); return null })

  const [assetRes, taskRes, alertRes, dcRes, sensorRes] = await Promise.all([
    attempt('资产', resource.list('/assets', { page: 1, page_size: 200 })),
    attempt('任务', resource.list('/tasks', { page: 1, page_size: 100 })),
    attempt('告警', resource.list('/alerts', { page: 1, page_size: 200 })),
    auth.can('datacenters:read') ? attempt('机房', resource.list('/datacenters', { page: 1, page_size: 200 }), true) : Promise.resolve(null),
    auth.can('monitor:read') ? attempt('传感器', resource.list('/sensor-records', { page: 1, page_size: 200 }), true) : Promise.resolve(null)
  ])

  if (assetRes) { const d = rows(assetRes); assets.value = d.items; assetTotal.value = d.total }
  if (taskRes) { const d = rows(taskRes); tasks.value = d.items; taskTotal.value = d.total }
  if (alertRes) { const d = rows(alertRes); alerts.value = d.items; alertTotal.value = d.total }
  if (dcRes) datacenters.value = rows(dcRes).items
  if (sensorRes) sensorRows.value = rows(sensorRes).items

  lastUpdated.value = dayjs().format('HH:mm:ss')
  const failures = errors.filter(msg => !msg.includes('请求已取消') && !msg.includes('项目上下文已切换'))
  if (failures.length) { loadError.value = failures.join('；'); if (!background) message.error(loadError.value) }
  loading.value = false; refreshing.value = false

  buildTrend()
  await renderCharts()
  if (isOperationMode.value) void loadOperations()
}

function startAutoRefresh() {
  stopAutoRefresh()
  refreshTimer = setInterval(() => { if (auth.projectId && !document.hidden) load(true) }, 30000)
}
function stopAutoRefresh() { if (refreshTimer) { clearInterval(refreshTimer); refreshTimer = null } }

watch(autoRefresh, on => { if (on) startAutoRefresh(); else stopAutoRefresh() })
watch(timeRange, () => { if (auth.projectId) void load(false) })
watch(() => auth.projectId, () => { void load(false) })
watch(isDarkMode, () => { void renderCharts() })

onMounted(async () => {
  await loadProjects()
  if (auth.projectId) await load(false); else buildTrend()
  await renderCharts()
  window.addEventListener('resize', resizeCharts)
  if (typeof ResizeObserver !== 'undefined') {
    observer = new ResizeObserver(() => resizeCharts())
    if (trendChartRef.value) observer.observe(trendChartRef.value)
    if (pieChartRef.value) observer.observe(pieChartRef.value)
  }
  if (autoRefresh.value) startAutoRefresh()
})

onUnmounted(() => {
  window.removeEventListener('resize', resizeCharts)
  observer?.disconnect(); observer = undefined
  stopAutoRefresh()
  trendChart?.dispose(); pieChart?.dispose()
  trendChart = undefined; pieChart = undefined
})
</script>

<template>
  <div class="page dashboard">
    <div class="page-head">
      <div>
        <div class="eyebrow dark">INFRASTRUCTURE OVERVIEW</div>
        <h1>运行总览</h1>
        <p class="dash-sub">
          裸机基础设施实时态势
          <span v-if="autoRefresh" class="live-dot" />
          <span v-if="lastUpdated" class="updated-at">更新于 {{ lastUpdated }}</span>
          <span v-else-if="!auth.projectId" class="updated-at">尚未选择项目</span>
        </p>
      </div>
      <div class="head-actions">
        <n-select :value="auth.projectId" placeholder="选择项目" size="small" :loading="projectLoading"
          :options="projects.map(p => ({ label: String(p.name), value: String(p.id) }))" style="width: 176px"
          @update:value="auth.selectProject" />
        <n-radio-group v-model:value="timeRange" size="small">
          <n-radio-button value="today">今日</n-radio-button>
          <n-radio-button value="week">本周</n-radio-button>
          <n-radio-button value="month">本月</n-radio-button>
        </n-radio-group>
        <div class="auto-refresh-toggle"><span>自动刷新</span><n-switch v-model:value="autoRefresh" size="small" /></div>
        <n-button size="small" :loading="loading" @click="load(false)">刷新 ↻</n-button>
      </div>
    </div>

    <n-alert v-if="loadError" type="warning" :bordered="false" class="dash-alert" title="部分数据加载失败">{{ loadError }}</n-alert>
    <n-alert v-if="!auth.projectId" type="info" :bordered="false" class="dash-alert" title="请选择项目">总览数据按项目作用域统计，请先在上方或侧边栏选择一个项目。</n-alert>

    <n-spin :show="loading && !refreshing">
      <!-- 指标卡片行 -->
      <div class="dashboard-grid" style="grid-template-columns: repeat(4, 1fr)">
        <div v-for="(card, index) in metrics" :key="card.key" class="metric-card dash-metric"
          :class="{ clickable: !!card.to, alarm: card.key === 'alerts' && pendingAlerts > 0 }"
          :style="{ '--stagger': `${index * 70}ms` }" @click="card.to && router.push(card.to)">
          <div class="metric-icon" :class="card.tone" v-html="card.icon" />
          <div class="metric-content">
            <div class="metric-label">{{ card.label }}</div>
            <div class="metric-value">{{ card.value }}<small v-if="card.suffix" class="metric-suffix">{{ card.suffix }}</small></div>
            <div class="metric-foot">{{ card.foot }}</div>
          </div>
          <span class="metric-orb" :class="card.tone" />
        </div>
      </div>

      <!-- 运营指标行 -->
      <div v-if="isOperationMode" class="dashboard-grid" style="grid-template-columns: repeat(3, 1fr)">
        <div v-for="item in opsCards" :key="item.key" class="metric-card ops-metric-card">
          <div class="metric-icon" :class="item.tone" v-html="item.icon" />
          <div class="metric-content">
            <div class="metric-label">{{ item.label }}</div>
            <div class="metric-value">{{ item.value }}</div>
            <div class="metric-foot">{{ item.foot }}</div>
          </div>
          <n-tag v-if="!opsReachable" size="tiny" :bordered="false" type="warning">占位</n-tag>
        </div>
      </div>

      <!-- 图表行：趋势 + 容量 -->
      <div class="dashboard-grid" style="grid-template-columns: 2fr 1fr">
        <div class="card chart-card">
          <div class="card-header">
            <span>资源使用率趋势</span>
            <div class="chart-header-meta">
              <n-tag v-if="trendEstimated" size="small" :bordered="false" type="info">估算</n-tag>
              <span class="legend-chip"><i style="background: #165DFF" />CPU</span>
              <span class="legend-chip"><i style="background: #00B42A" />内存</span>
            </div>
          </div>
          <div ref="trendChartRef" class="chart-body" style="height: 320px" />
        </div>
        <div class="card chart-card">
          <div class="card-header">
            <span>机房容量分布</span>
            <n-button text type="primary" size="small" @click="router.push('/datacenters')">机房管理 →</n-button>
          </div>
          <div ref="pieChartRef" class="chart-body" style="height: 240px" />
          <div v-if="dcRows.length" class="dc-list">
            <div v-for="row in dcRows" :key="row.id" class="dc-row">
              <span class="dc-name" :title="row.name">{{ row.name }}</span>
              <span class="dc-bar">
                <i v-for="bucket in BUCKETS" :key="bucket.key"
                  :style="{ width: `${row.total ? (row.counts[bucket.key] / row.total) * 100 : 0}%`, background: bucket.color }" />
              </span>
              <span class="dc-count">{{ row.total }}</span>
            </div>
          </div>
          <div v-else class="dash-empty-inline"><n-empty size="small" description="暂无机房与资产容量数据" /></div>
        </div>
      </div>

      <!-- 列表行：告警 + 任务 -->
      <div class="dashboard-grid" style="grid-template-columns: 1fr 1fr">
        <div class="card list-card">
          <div class="card-header">
            <span>最近告警 <span v-if="alertTotal" class="count">{{ alertTotal }}</span></span>
            <n-button text type="primary" size="small" @click="router.push('/alerts')">查看全部 →</n-button>
          </div>
          <div v-if="alertRows.length" class="activity-list">
            <div v-for="row in alertRows" :key="String(row.id)" class="activity-row">
              <span class="sev-rail" :style="{ background: row.severityColor }" />
              <span class="activity-info">
                <strong>{{ row.message }}</strong>
                <small>{{ row.assetName }} · {{ row.time }}<em v-if="row.relative">（{{ row.relative }}）</em></small>
              </span>
              <span class="row-status">
                <span v-if="row.assetStatus" :class="row.assetStatusClass">{{ row.assetStatus }}</span>
                <n-tag size="small" :bordered="false" :color="{ color: row.severityBg, textColor: row.severityColor }">{{ row.severityLabel }}</n-tag>
              </span>
            </div>
          </div>
          <div v-else class="dash-empty-inline"><n-empty size="small" description="所选时间范围内无告警" /></div>
        </div>

        <div class="card list-card">
          <div class="card-header">
            <span>最近任务 <span v-if="taskTotal" class="count">{{ taskTotal }}</span></span>
            <n-button text type="primary" size="small" @click="router.push('/tasks')">全部任务 →</n-button>
          </div>
          <div v-if="taskRows.length" class="activity-list">
            <div v-for="row in taskRows" :key="String(row.id)" class="activity-row">
              <span class="activity-icon">↗</span>
              <span class="activity-info">
                <strong>{{ row.kind }}</strong>
                <small>{{ row.assetName }} · {{ row.time }}</small>
              </span>
              <span class="row-status"><n-tag size="small" :bordered="false" :type="row.statusType">{{ row.status }}</n-tag></span>
            </div>
          </div>
          <div v-else class="dash-empty-inline"><n-empty size="small" description="所选时间范围内无任务记录" /></div>
        </div>
      </div>
    </n-spin>
  </div>
</template>

<style scoped>
.dashboard { animation: dash-in 0.4s ease both; }
@keyframes dash-in { from { opacity: 0; transform: translateY(8px); } to { opacity: 1; transform: none; } }

.dash-sub { display: flex; align-items: center; gap: 10px; flex-wrap: wrap; }
.updated-at { color: var(--color-text-tertiary); font-size: var(--font-size-xs); font-variant-numeric: tabular-nums; }
.live-dot { width: 7px; height: 7px; border-radius: 50%; background: var(--color-success); box-shadow: 0 0 0 0 rgba(0, 180, 42, 0.5); animation: live-pulse 2.2s ease-out infinite; }
@keyframes live-pulse { 0% { box-shadow: 0 0 0 0 rgba(0, 180, 42, 0.45); } 70% { box-shadow: 0 0 0 7px rgba(0, 180, 42, 0); } 100% { box-shadow: 0 0 0 0 rgba(0, 180, 42, 0); } }
.dash-alert { margin-bottom: 16px; }

/* 指标卡片 */
.dash-metric { position: relative; overflow: hidden; animation: metric-rise 0.5s cubic-bezier(0.22, 0.8, 0.3, 1) both; animation-delay: var(--stagger, 0ms); }
@keyframes metric-rise { from { opacity: 0; transform: translateY(14px); } to { opacity: 1; transform: none; } }
.dash-metric.clickable { cursor: pointer; }
.dash-metric.alarm { border-color: rgba(245, 63, 63, 0.4); }
.metric-orb { position: absolute; right: -34px; top: -34px; width: 96px; height: 96px; border-radius: 50%; opacity: 0.14; pointer-events: none; transition: transform var(--transition-slow); }
.dash-metric:hover .metric-orb { transform: scale(1.25); }
.metric-orb.primary { background: var(--color-primary); }
.metric-orb.success { background: var(--color-success); }
.metric-orb.info { background: var(--color-info); }
.metric-orb.warning { background: var(--color-warning); }
.metric-orb.error { background: var(--color-error); }
.metric-value { display: flex; align-items: baseline; gap: 3px; letter-spacing: -0.5px; }
.metric-suffix { font-size: 15px; font-weight: 600; color: var(--color-text-tertiary); }

/* 运营指标 */
.ops-metric-card { display: flex; align-items: flex-start; gap: 14px; }

/* 图表卡片 */
.chart-card { display: flex; flex-direction: column; min-width: 0; }
.chart-card .card-header { font-size: var(--font-size-base); }
.chart-header-meta { display: flex; align-items: center; gap: 8px; }
.chart-body { width: 100%; padding: 4px 6px 10px; }
.legend-chip { display: inline-flex; align-items: center; gap: 6px; font-size: var(--font-size-xs); color: var(--color-text-secondary); }
.legend-chip i { width: 9px; height: 9px; border-radius: 3px; }

/* 机房容量列表 */
.dc-list { padding: 6px 20px 18px; display: flex; flex-direction: column; gap: 9px; }
.dc-row { display: grid; grid-template-columns: minmax(72px, 108px) 1fr 34px; align-items: center; gap: 10px; font-size: var(--font-size-xs); color: var(--color-text-secondary); }
.dc-name { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.dc-bar { display: flex; height: 6px; border-radius: var(--radius-full); overflow: hidden; background: var(--color-border-light); }
.dc-bar i { height: 100%; transition: width var(--transition-normal); }
.dc-count { text-align: right; font-variant-numeric: tabular-nums; color: var(--color-text-tertiary); }

/* 列表卡片 */
.list-card { display: flex; flex-direction: column; min-width: 0; }
.list-card .activity-list { padding: 4px 20px 12px; }
.list-card .activity-row { padding-left: 4px; padding-right: 4px; }
.sev-rail { width: 3px; height: 26px; border-radius: var(--radius-full); flex-shrink: 0; }
.activity-info small em { font-style: normal; color: var(--color-text-disabled); }
.row-status { flex-shrink: 0; display: inline-flex; align-items: center; gap: 8px; }
.dash-empty-inline { display: grid; place-items: center; min-height: 180px; padding: 12px 0 20px; }

/* 全局行间距 */
.dashboard .dashboard-grid { margin-bottom: 20px; }
.dashboard .dashboard-grid:last-child { margin-bottom: 0; }

/* 响应式 */
@media (max-width: 1280px) { .dashboard-grid { grid-template-columns: repeat(2, 1fr) !important; } }
@media (max-width: 1120px) { .dashboard-grid { grid-template-columns: 1fr !important; } }
@media (max-width: 720px) { .dashboard-grid { grid-template-columns: 1fr !important; } }
</style>
