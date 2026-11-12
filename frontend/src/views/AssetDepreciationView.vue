<script setup lang="ts">
// 资产折旧页面 — 自用模式：直线法折旧台账、净值趋势与导出
import { computed, h, nextTick, onBeforeUnmount, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { NButton, NEmpty, NInput, NProgress, NSelect, NTag, NDataTable, useMessage } from 'naive-ui'
import type { DataTableColumns } from 'naive-ui'
import * as echarts from 'echarts'
import dayjs from 'dayjs'
import { depreciationApi, errorText, exportToExcel, resource, rows, unwrap, type RecordData } from '../api'
import { useAuth } from '../stores/auth'

const router = useRouter()
const message = useMessage()
const auth = useAuth()

// ============================================================
// 数据状态
// ============================================================
const assets = ref<RecordData[]>([])
const depreciations = ref<RecordData[]>([])
const loading = ref(false)
const search = ref('')
const statusFilter = ref<string>('all')
const selectedAssetId = ref<string>('')
const detail = ref<RecordData | null>(null)
const detailLoading = ref(false)
const truncated = ref(false)
// 资产台账总数（后端单页上限 200，本地只拉取前 5 页，用于提示截断规模）
const assetsTotal = ref(0)

// ============================================================
// 工具函数
// ============================================================
const num = (value: unknown): number | null => {
  if (value == null || value === '') return null
  const parsed = Number(value)
  return Number.isFinite(parsed) ? parsed : null
}

const money = (value: number | null): string => {
  if (value == null) return '—'
  return `¥ ${value.toLocaleString('zh-CN', { minimumFractionDigits: 2, maximumFractionDigits: 2 })}`
}

const dateText = (value: unknown): string => {
  if (!value) return '—'
  const d = dayjs(String(value))
  return d.isValid() ? d.format('YYYY-MM-DD') : String(value)
}

// 与后端 calculateUsedMonths 保持一致：满整月 + 按当月天数比例修正零头
function usedMonthsFrom(start: unknown): number {
  if (!start) return 0
  const begin = dayjs(String(start))
  if (!begin.isValid()) return 0
  const now = dayjs()
  if (begin.isAfter(now)) return 0
  const whole = Math.max(now.diff(begin, 'month'), 0)
  const anchor = begin.add(whole, 'month')
  const monthDays = anchor.endOf('month').date() || 30
  return Math.min(whole + now.diff(anchor, 'day') / monthDays, whole + 1)
}

const METHOD_LABELS: Record<string, string> = {
  straight_line: '直线法',
  straightline: '直线法',
  '直线法': '直线法',
  double_declining: '双倍余额递减法',
  units_of_production: '工作量法'
}

function methodLabel(value: unknown): string {
  const key = String(value ?? '').toLowerCase()
  if (!key) return '直线法'
  return METHOD_LABELS[key] || String(value)
}

const DEP_STATUS_META: Record<string, { label: string; type: 'success' | 'warning' | 'info' | 'default' | 'error' }> = {
  active: { label: '折旧中', type: 'info' },
  in_progress: { label: '折旧中', type: 'info' },
  completed: { label: '已折完', type: 'success' },
  finished: { label: '已折完', type: 'success' },
  scrapped: { label: '已报废', type: 'error' },
  retired: { label: '已报废', type: 'error' },
  unconfigured: { label: '未配置', type: 'warning' }
}

const ASSET_STATUS_LABELS: Record<string, string> = {
  online: '在线', running: '运行中', in_use: '使用中', offline: '离线', stopped: '已关机',
  error: '故障', maintenance: '维护中', retired: '已退役', pending_management: '待纳管', unknown: '状态未知'
}

// ============================================================
// 台账行：资产列表 + 折旧记录合并
// ============================================================
interface DepRow {
  assetId: string
  assetName: string
  serialNumber: string
  departmentId: string
  assetStatus: string
  configured: boolean
  method: string
  purchaseCost: number | null
  salvageValue: number | null
  usefulLife: number | null
  monthly: number | null
  accumulated: number | null
  netValue: number | null
  startDate: string
  startValue: unknown
  status: string
  progress: number
}

const depMap = computed<Record<string, RecordData>>(() => {
  const map: Record<string, RecordData> = {}
  for (const item of depreciations.value) {
    const id = String(item.asset_id ?? '')
    if (id) map[id] = item
  }
  return map
})

const allRows = computed<DepRow[]>(() =>
  assets.value.map(asset => {
    const assetId = String(asset.id ?? '')
    const dep = depMap.value[assetId]
    const purchaseCost = dep ? num(dep.purchase_cost) : null
    const salvageValue = dep ? num(dep.salvage_value) : null
    const usefulLife = dep ? num(dep.useful_life_months) : null
    const storedMonthly = dep ? num(dep.monthly_depreciation) : null
    const monthly = storedMonthly && storedMonthly > 0
      ? storedMonthly
      : (purchaseCost != null && usefulLife && usefulLife > 0
        ? Math.max((purchaseCost - (salvageValue ?? 0)) / usefulLife, 0)
        : null)
    const startValue = dep?.start_date ?? asset.purchase_date ?? null
    const usedMonths = monthly != null && purchaseCost != null ? usedMonthsFrom(startValue) : 0
    const depreciable = purchaseCost != null ? Math.max(purchaseCost - (salvageValue ?? 0), 0) : null
    const accumulated = depreciable != null && monthly != null
      ? Math.min(monthly * usedMonths, depreciable)
      : dep ? num(dep.accumulated_depreciation) : null
    const netValue = purchaseCost != null && accumulated != null
      ? Math.max(purchaseCost - accumulated, salvageValue ?? 0)
      : dep ? num(dep.net_value) : null
    const configured = !!dep || (purchaseCost != null && usefulLife != null)
    const progress = depreciable && accumulated != null
      ? Math.min(Math.round((accumulated / depreciable) * 100), 100)
      : 0
    let status = 'unconfigured'
    if (configured) {
      status = String(dep?.status || '').trim()
        || (progress >= 100 ? 'completed' : 'active')
    }
    return {
      assetId,
      assetName: String(dep?.asset_name ?? asset.name ?? '—'),
      serialNumber: String(asset.serial_number || '—'),
      departmentId: asset.department_id == null ? '' : String(asset.department_id),
      assetStatus: String(asset.display_status ?? asset.status ?? 'unknown'),
      configured,
      method: methodLabel(dep?.depreciation_method),
      purchaseCost,
      salvageValue,
      usefulLife,
      monthly,
      accumulated,
      netValue,
      startDate: startValue ? dateText(startValue) : '—',
      startValue,
      status,
      progress
    }
  })
)

const statusOptions = [
  { label: '全部状态', value: 'all' },
  { label: '折旧中', value: 'active' },
  { label: '已折完', value: 'completed' },
  { label: '已报废', value: 'scrapped' },
  { label: '未配置', value: 'unconfigured' }
]

const filteredRows = computed(() => {
  const kw = search.value.trim().toLowerCase()
  return allRows.value.filter(row => {
    if (statusFilter.value !== 'all') {
      const normalized = row.status === 'in_progress' ? 'active' : row.status
      const bucket = ['completed', 'finished'].includes(row.status) ? 'completed' : normalized
      if (bucket !== statusFilter.value) return false
    }
    if (!kw) return true
    return [row.assetName, row.serialNumber, row.assetId].some(value => String(value).toLowerCase().includes(kw))
  })
})

const summary = computed(() => {
  let cost = 0
  let accumulated = 0
  let net = 0
  let configured = 0
  for (const row of filteredRows.value) {
    if (row.purchaseCost != null) cost += row.purchaseCost
    if (row.accumulated != null) accumulated += row.accumulated
    if (row.netValue != null) net += row.netValue
    if (row.configured) configured += 1
  }
  return {
    cost,
    accumulated,
    net,
    configured,
    total: filteredRows.value.length,
    rate: cost > 0 ? Math.round((net / cost) * 100) : 0
  }
})

// ============================================================
// 表格列
// ============================================================
const statusMetaOf = (row: DepRow) => DEP_STATUS_META[row.status] ?? {
  label: row.status || '折旧中',
  type: 'info' as const
}

// 资产自身状态（assets.status DDL 枚举：unknown|online|offline|maintenance|retired）
function assetStatusLabel(status: string): string {
  return ASSET_STATUS_LABELS[status] || status || '状态未知'
}

function assetStatusType(status: string): 'success' | 'warning' | 'error' | 'info' | 'default' {
  if (['online', 'running', 'in_use'].includes(status)) return 'success'
  if (['maintenance', 'pending_management'].includes(status)) return 'info'
  if (status === 'error' || status === 'fault') return 'error'
  if (['retired', 'offline'].includes(status)) return 'warning'
  return 'default'
}

const columns = computed<DataTableColumns<DepRow>>(() => [
  {
    title: '资产名称',
    key: 'assetName',
    minWidth: 170,
    ellipsis: { tooltip: true },
    sortable: true,
    sorter: (a, b) => a.assetName.localeCompare(b.assetName, 'zh-CN'),
    render: row => h('div', { class: 'dep-asset-cell' }, [
      h('span', { class: 'dep-asset-name' }, row.assetName),
      h('span', { class: 'dep-asset-sn' }, row.serialNumber)
    ])
  },
  {
    title: '采购成本',
    key: 'purchaseCost',
    minWidth: 130,
    align: 'right',
    sortable: true,
    sorter: (a, b) => (a.purchaseCost ?? -1) - (b.purchaseCost ?? -1),
    render: row => h('span', { class: 'money-value' }, money(row.purchaseCost))
  },
  {
    title: '残值',
    key: 'salvageValue',
    minWidth: 120,
    align: 'right',
    render: row => h('span', { class: 'money-value muted' }, money(row.salvageValue))
  },
  {
    title: '使用年限(月)',
    key: 'usefulLife',
    minWidth: 110,
    align: 'right',
    render: row => (row.usefulLife != null ? `${row.usefulLife} 月` : '—')
  },
  {
    title: '月折旧额',
    key: 'monthly',
    minWidth: 120,
    align: 'right',
    render: row => h('span', { class: 'money-value' }, money(row.monthly))
  },
  {
    title: '累计折旧',
    key: 'accumulated',
    minWidth: 130,
    align: 'right',
    render: row => h('div', { class: 'dep-accumulated' }, [
      h('span', { class: 'money-value warn' }, money(row.accumulated)),
      row.configured ? h(NProgress, {
        type: 'line',
        percentage: row.progress,
        indicatorPlacement: 'outside',
        height: 4,
        fillBorderRadius: 2,
        showIndicator: false,
        status: row.progress >= 100 ? 'success' : 'default',
        style: { marginTop: '4px' }
      }) : null
    ])
  },
  {
    title: '当前净值',
    key: 'netValue',
    minWidth: 130,
    align: 'right',
    sortable: true,
    sorter: (a, b) => (a.netValue ?? -1) - (b.netValue ?? -1),
    render: row => h('span', { class: ['money-value', 'net-value', row.configured ? '' : 'muted'] }, money(row.netValue))
  },
  {
    title: '状态',
    key: 'status',
    minWidth: 100,
    render: row => h(NTag, {
      size: 'small',
      bordered: false,
      type: statusMetaOf(row).type
    }, { default: () => statusMetaOf(row).label })
  }
])

function rowProps(row: DepRow) {
  return {
    style: 'cursor: pointer;',
    class: row.assetId === selectedAssetId.value ? 'dep-row-active' : '',
    onClick: () => selectRow(row)
  }
}

// ============================================================
// 详情面板
// ============================================================
const selectedRow = computed(() => allRows.value.find(row => row.assetId === selectedAssetId.value) ?? null)

const breakdown = computed(() => {
  const row = selectedRow.value
  if (!row) return null
  const remote = detail.value
  const remoteReady = !!remote && remote.has_depreciation === true
  const purchaseCost = remoteReady ? num(remote.purchase_cost) : row.purchaseCost
  const salvageValue = remoteReady ? num(remote.salvage_value) : row.salvageValue
  const usefulLife = remoteReady ? num(remote.useful_life_months) : row.usefulLife
  const monthly = remoteReady ? num(remote.monthly_depreciation) : row.monthly
  const accumulated = remoteReady ? num(remote.accumulated_depreciation) : row.accumulated
  const netValue = remoteReady ? num(remote.net_value) : row.netValue
  const start = remoteReady ? remote.start_date : row.startValue
  return {
    purchaseCost,
    salvageValue,
    usefulLife,
    monthly,
    accumulated,
    netValue,
    start,
    usedMonths: usedMonthsFrom(start),
    formula: `(${money(purchaseCost)} − ${money(salvageValue ?? 0)}) ÷ ${usefulLife ?? '—'} 月`,
    configured: remoteReady || row.configured,
    method: remoteReady ? methodLabel(remote.depreciation_method) : row.method,
    status: remoteReady ? String(remote.status || '') : row.status
  }
})

async function selectRow(row: DepRow) {
  selectedAssetId.value = row.assetId
  detail.value = null
  if (!row.configured) return
  detailLoading.value = true
  try {
    detail.value = unwrap<RecordData>(await depreciationApi.get(row.assetId))
  } catch {
    // 详情接口不可用时使用台账行本地计算结果，不阻断页面
    detail.value = null
  } finally {
    detailLoading.value = false
    await renderTrendChart()
  }
}

// ============================================================
// 净值趋势图
// ============================================================
const trendRef = ref<HTMLElement>()
let trendChart: echarts.ECharts | undefined
let observer: ResizeObserver | undefined

const trendPoints = computed<{ label: string; value: number }[]>(() => {
  const info = breakdown.value
  if (!info || info.purchaseCost == null || info.monthly == null || !info.usefulLife || info.usefulLife <= 0) return []
  const start = dayjs(info.start as string)
  if (!start.isValid()) return []
  const floor = info.salvageValue ?? 0
  const months = Math.ceil(info.usefulLife)
  const points: { label: string; value: number }[] = []
  for (let index = 0; index <= months; index += 1) {
    const value = Math.max(info.purchaseCost - info.monthly * index, floor)
    points.push({ label: start.add(index, 'month').format('YYYY-MM'), value: Number(value.toFixed(2)) })
  }
  return points
})

const currentMonthIndex = computed(() => {
  const info = breakdown.value
  if (!info || !info.usefulLife) return -1
  return Math.min(Math.round(info.usedMonths), Math.ceil(info.usefulLife))
})

function tokens() {
  const cs = getComputedStyle(document.documentElement)
  const read = (name: string, fallback: string) => cs.getPropertyValue(name).trim() || fallback
  return {
    text: read('--color-text-primary', '#1D2129'),
    faint: read('--color-text-tertiary', '#86909C'),
    line: read('--color-border', '#E5E6EB'),
    soft: read('--color-border-light', '#F0F0F0'),
    surface: read('--color-surface', '#FFFFFF'),
    primary: read('--color-primary', '#165DFF')
  }
}

function withAlpha(hex: string, alpha: number) {
  const v = hex.replace('#', '')
  const n = v.length === 3 ? v.split('').map(c => c + c).join('') : v
  const int = parseInt(n, 16)
  return `rgba(${(int >> 16) & 255}, ${(int >> 8) & 255}, ${int & 255}, ${alpha})`
}

async function renderTrendChart() {
  await nextTick()
  if (!trendRef.value) return
  const points = trendPoints.value
  if (!points.length) {
    trendChart?.clear()
    return
  }
  if (!trendChart || trendChart.isDisposed()) trendChart = echarts.init(trendRef.value)
  const t = tokens()
  const info = breakdown.value
  trendChart.setOption({
    animationDuration: 620,
    animationEasing: 'cubicOut',
    grid: { left: 8, right: 16, top: 24, bottom: 8, containLabel: true },
    tooltip: {
      trigger: 'axis',
      backgroundColor: t.surface,
      borderColor: t.line,
      textStyle: { color: t.text, fontSize: 12 },
      valueFormatter: (v: unknown) => `¥ ${Number(v ?? 0).toLocaleString('zh-CN', { minimumFractionDigits: 2, maximumFractionDigits: 2 })}`
    },
    xAxis: {
      type: 'category',
      boundaryGap: false,
      data: points.map(p => p.label),
      axisLine: { lineStyle: { color: t.line } },
      axisTick: { show: false },
      axisLabel: { color: t.faint, fontSize: 10, hideOverlap: true, interval: Math.ceil(points.length / 6) - 1 }
    },
    yAxis: {
      type: 'value',
      axisLine: { show: false },
      axisTick: { show: false },
      axisLabel: { color: t.faint, fontSize: 10, formatter: (v: number) => v >= 10000 ? `${(v / 10000).toFixed(1)}万` : String(v) },
      splitLine: { lineStyle: { color: t.soft, type: 'dashed' } }
    },
    series: [{
      name: '资产净值',
      type: 'line',
      smooth: true,
      symbol: 'circle',
      symbolSize: 5,
      showSymbol: false,
      lineStyle: { color: t.primary, width: 2 },
      itemStyle: { color: t.primary },
      areaStyle: {
        color: new echarts.graphic.LinearGradient(0, 0, 0, 1, [
          { offset: 0, color: withAlpha(t.primary, 0.24) },
          { offset: 1, color: withAlpha(t.primary, 0) }
        ])
      },
      data: points.map(p => p.value),
      markPoint: currentMonthIndex.value >= 0 && info
        ? {
            symbol: 'circle',
            symbolSize: 8,
            itemStyle: { color: '#FF7D00' },
            label: { show: true, formatter: '当前', position: 'top', color: '#FF7D00', fontSize: 10 },
            data: [{ coord: [points[currentMonthIndex.value].label, points[currentMonthIndex.value].value] }]
          }
        : undefined
    }]
  }, true)
  trendChart.resize()
}

// ============================================================
// 数据加载
// ============================================================
// 后端单页上限 200，最多取 5 页（1000 台）作为本地汇总与筛选的数据基础。
async function fetchAllPages(
  fetch: (page: number) => Promise<{ items: RecordData[]; total: number }>
) {
  const first = await fetch(1)
  const pages = Math.min(Math.ceil(first.total / 200), 5)
  if (pages <= 1) return { items: first.items, total: first.total, loaded: first.items.length }
  const rest = await Promise.all(
    Array.from({ length: pages - 1 }, (_, index) => fetch(index + 2))
  )
  const items = [first, ...rest].flatMap(result => result.items)
  return { items, total: first.total, loaded: items.length }
}

async function load() {
  if (!auth.projectId) return
  loading.value = true
  try {
    const [assetPage, depreciationPage] = await Promise.all([
      fetchAllPages(async page => rows(await resource.list('/assets', { page, page_size: 200 }))),
      fetchAllPages(async page => rows(await depreciationApi.list({ page, page_size: 200 })))
    ])
    assets.value = assetPage.items
    depreciations.value = depreciationPage.items
    assetsTotal.value = assetPage.total
    truncated.value = assetPage.loaded < assetPage.total
    if (selectedAssetId.value && !assets.value.some(asset => String(asset.id) === selectedAssetId.value)) {
      selectedAssetId.value = ''
      detail.value = null
    }
    await renderTrendChart()
  } catch (e) {
    message.error(errorText(e))
  } finally {
    loading.value = false
  }
}

// ============================================================
// 导出
// ============================================================
async function exportExcel() {
  if (!filteredRows.value.length) {
    message.warning('当前没有可导出的折旧数据')
    return
  }
  const data = filteredRows.value.map(row => ({
    资产名称: row.assetName,
    序列号: row.serialNumber,
    采购成本: row.purchaseCost ?? '',
    残值: row.salvageValue ?? '',
    使用年限_月: row.usefulLife ?? '',
    月折旧额: row.monthly != null ? Number(row.monthly.toFixed(2)) : '',
    累计折旧: row.accumulated != null ? Number(row.accumulated.toFixed(2)) : '',
    当前净值: row.netValue != null ? Number(row.netValue.toFixed(2)) : '',
    折旧起始日: row.startDate,
    折旧方式: row.method,
    状态: statusMetaOf(row).label
  }))
  try {
    await exportToExcel(data, Object.keys(data[0]), '资产折旧台账')
    message.success('已导出折旧台账')
  } catch (e) {
    message.error(errorText(e))
  }
}

watch(() => auth.projectId, () => {
  selectedAssetId.value = ''
  detail.value = null
  search.value = ''
  void load()
}, { immediate: true })

// 选中资产变化、或主题切换时重绘净值趋势曲线
watch(selectedRow, () => { void renderTrendChart() })
watch(() => document.documentElement.getAttribute('data-theme'), () => { void renderTrendChart() })

onBeforeUnmount(() => {
  observer?.disconnect()
  trendChart?.dispose()
  trendChart = undefined
})

watch(trendRef, el => {
  observer?.disconnect()
  if (!el) return
  observer = new ResizeObserver(() => trendChart?.resize())
  observer.observe(el)
})
</script>

<template>
  <div class="page">
    <div class="page-head">
      <div>
        <div class="eyebrow dark">ASSET / DEPRECIATION</div>
        <h1>资产折旧</h1>
        <p>按直线法追踪资产净值变化，掌握设备残值与折余价值</p>
      </div>
      <div class="head-actions">
        <n-button quaternary @click="load">刷新</n-button>
        <n-button type="primary" ghost @click="exportExcel">导出 Excel</n-button>
      </div>
    </div>

    <div class="metric-grid dep-metric-grid">
      <div class="metric-card">
        <div class="metric-icon primary">
          <svg width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8"><path d="M12 2v20M17 5H9.5a3.5 3.5 0 0 0 0 7h5a3.5 3.5 0 0 1 0 7H6"/></svg>
        </div>
        <div class="metric-content">
          <div class="metric-label">资产原值合计</div>
          <div class="metric-value">{{ money(summary.cost) }}</div>
          <div class="metric-foot">共 {{ summary.total }} 台资产参与统计</div>
        </div>
      </div>
      <div class="metric-card">
        <div class="metric-icon warning">
          <svg width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8"><path d="M3 17l6-6 4 4 8-8"/><path d="M14 7h7v7"/></svg>
        </div>
        <div class="metric-content">
          <div class="metric-label">累计折旧合计</div>
          <div class="metric-value">{{ money(summary.accumulated) }}</div>
          <div class="metric-foot">按已使用月份动态计算</div>
        </div>
      </div>
      <div class="metric-card">
        <div class="metric-icon success">
          <svg width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8"><rect x="2" y="4" width="20" height="16" rx="2"/><path d="M2 10h20"/><path d="M7 15h4"/></svg>
        </div>
        <div class="metric-content">
          <div class="metric-label">当前净值合计</div>
          <div class="metric-value">{{ money(summary.net) }}</div>
          <div class="metric-foot">净值率 {{ summary.rate }}%</div>
        </div>
      </div>
      <div class="metric-card">
        <div class="metric-icon info">
          <svg width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8"><circle cx="12" cy="12" r="9"/><path d="M12 7v5l3 2"/></svg>
        </div>
        <div class="metric-content">
          <div class="metric-label">已配置折旧</div>
          <div class="metric-value">{{ summary.configured }}</div>
          <div class="metric-foot">未配置 {{ summary.total - summary.configured }} 台</div>
        </div>
      </div>
    </div>

    <div class="dep-layout">
      <section class="panel">
        <div class="panel-toolbar">
          <div>
            <div class="panel-title">折旧台账 <span class="count">{{ filteredRows.length }}</span></div>
            <p class="panel-caption">
              <template v-if="truncated">仅统计前 {{ assets.length }} / {{ assetsTotal }} 台资产，请缩小搜索范围或分批处理</template>
              <template v-else>点击任意资产行查看折旧明细与净值趋势</template>
            </p>
          </div>
          <div class="toolbar-actions">
            <n-input
              v-model:value="search"
              size="small"
              clearable
              placeholder="搜索资产名称 / SN"
              style="width: 220px"
            />
            <n-select
              v-model:value="statusFilter"
              size="small"
              :options="statusOptions"
              style="width: 130px"
            />
          </div>
        </div>
        <n-data-table
          size="small"
          :columns="columns"
          :data="filteredRows"
          :loading="loading"
          :row-key="(row: DepRow) => row.assetId"
          :row-props="rowProps"
          :scroll-x="1000"
          max-height="calc(100vh - 420px)"
          :pagination="{ pageSize: 20, showSizePicker: true, pageSizes: [20, 50, 100] }"
        />
        <div v-if="!filteredRows.length && !loading" class="empty-hint">
          暂无折旧数据，请先在服务器管理中补充资产信息。
        </div>
      </section>

      <aside class="panel dep-detail-panel">
        <template v-if="selectedRow">
          <div class="dep-detail-head">
            <div>
              <div class="eyebrow dark">DEPRECIATION DETAIL</div>
              <h3>{{ selectedRow.assetName }}</h3>
              <p>SN {{ selectedRow.serialNumber }} · {{ selectedRow.startDate }} 起折旧</p>
              <div class="dep-detail-tags">
                <n-tag size="tiny" :bordered="false" :type="assetStatusType(selectedRow.assetStatus)">
                  {{ assetStatusLabel(selectedRow.assetStatus) }}
                </n-tag>
                <n-tag v-if="selectedRow.departmentId" size="tiny" :bordered="false" type="info">
                  部门 #{{ selectedRow.departmentId }}
                </n-tag>
              </div>
            </div>
            <n-button size="tiny" quaternary type="primary" @click="router.push(`/assets/${selectedRow.assetId}`)">
              资产详情 →
            </n-button>
          </div>

          <div v-if="!breakdown || !breakdown.configured" class="dep-detail-empty">
            <n-empty description="该资产尚未配置折旧参数">
              <template #extra>
                <span class="dep-empty-hint">补充采购成本、残值与使用年限后即可计算净值。</span>
              </template>
            </n-empty>
          </div>

          <template v-else>
            <div class="dep-net-value-card">
              <div class="dep-net-label">当前净值</div>
              <div class="dep-net-amount">{{ money(breakdown.netValue) }}</div>
              <div class="dep-net-foot">
                <n-tag size="tiny" :bordered="false" type="info">{{ breakdown.method }}</n-tag>
                <span>已使用 {{ breakdown.usedMonths.toFixed(1) }} 个月</span>
              </div>
            </div>

            <div class="dep-block">
              <div class="dep-block-title">计算明细</div>
              <dl class="dep-desc-list">
                <div><dt>资产原值</dt><dd>{{ money(breakdown.purchaseCost) }}</dd></div>
                <div><dt>预计残值</dt><dd>{{ money(breakdown.salvageValue) }}</dd></div>
                <div><dt>使用年限</dt><dd>{{ breakdown.usefulLife ?? '—' }} 个月</dd></div>
                <div><dt>月折旧额</dt><dd>{{ money(breakdown.monthly) }}</dd></div>
                <div><dt>累计折旧</dt><dd class="warn-text">{{ money(breakdown.accumulated) }}</dd></div>
                <div><dt>折旧状态</dt><dd>{{ statusMetaOf(selectedRow).label }}</dd></div>
              </dl>
              <div class="dep-formula">{{ breakdown.formula }} = {{ money(breakdown.monthly) }}</div>
              <n-progress
                type="line"
                :percentage="selectedRow.progress"
                :height="6"
                :fill-border-radius="3"
                :show-indicator="false"
                :status="selectedRow.progress >= 100 ? 'success' : 'default'"
                style="margin-top: 12px"
              />
              <div class="dep-progress-hint">折旧进度 {{ selectedRow.progress }}%</div>
            </div>

            <div class="dep-block">
              <div class="dep-block-title">净值趋势</div>
              <div v-if="trendPoints.length" ref="trendRef" class="chart-container small" />
              <div v-else class="chart-empty">缺少起始日期或使用年限，无法绘制净值曲线。</div>
            </div>
          </template>
        </template>

        <div v-else class="dep-detail-placeholder">
          <div class="placeholder-mark">¥</div>
          <div class="placeholder-title">选择资产查看折旧明细</div>
          <p class="placeholder-text">左侧台账展示每台服务器的原值、月折旧额与当前净值；点击行可展开计算过程与净值曲线。</p>
          <p class="placeholder-text">计算口径：直线法，月折旧额 = (原值 − 残值) ÷ 使用年限，净值不低于残值。</p>
        </div>
      </aside>
    </div>
  </div>
</template>

<style scoped>
.dep-metric-grid {
  grid-template-columns: repeat(4, minmax(0, 1fr));
}

.dep-layout {
  display: grid;
  grid-template-columns: minmax(0, 1fr) minmax(320px, 380px);
  gap: 20px;
  align-items: start;
}

.dep-asset-cell {
  display: flex;
  flex-direction: column;
  gap: 2px;
  min-width: 0;
}

.dep-asset-name {
  font-weight: 600;
  color: var(--color-text-primary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.dep-asset-sn {
  font-size: 11px;
  color: var(--color-text-tertiary);
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
}

.money-value {
  font-variant-numeric: tabular-nums;
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 13px;
  color: var(--color-text-primary);
}

.money-value.muted {
  color: var(--color-text-tertiary);
}

.money-value.warn {
  color: var(--color-warning);
}

.money-value.net-value {
  font-weight: 700;
  color: var(--color-primary);
}

.dep-accumulated {
  display: flex;
  flex-direction: column;
  align-items: flex-end;
  width: 100%;
}

:deep(.dep-row-active td) {
  background: var(--color-primary-light) !important;
}

/* === 右侧详情 === */
.dep-detail-panel {
  position: sticky;
  top: 16px;
  padding: 18px 20px 20px;
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.dep-detail-head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 12px;
}

.dep-detail-head h3 {
  margin: 6px 0 2px;
  font-size: var(--font-size-lg);
  font-weight: 700;
  color: var(--color-text-primary);
}

.dep-detail-head p {
  margin: 0;
  font-size: var(--font-size-xs);
  color: var(--color-text-tertiary);
}

.dep-detail-tags {
  display: flex;
  align-items: center;
  gap: 6px;
  margin-top: 8px;
}

.dep-net-value-card {
  background: linear-gradient(135deg, var(--color-primary-light), transparent 70%);
  border: 1px solid var(--color-border);
  border-radius: var(--radius-lg);
  padding: 16px 18px;
}

.dep-net-label {
  font-size: var(--font-size-xs);
  color: var(--color-text-secondary);
  letter-spacing: 0.4px;
}

.dep-net-amount {
  font-size: 26px;
  font-weight: 700;
  line-height: 1.3;
  color: var(--color-primary);
  font-variant-numeric: tabular-nums;
}

.dep-net-foot {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-top: 6px;
  font-size: var(--font-size-xs);
  color: var(--color-text-tertiary);
}

.dep-block-title {
  font-size: var(--font-size-xs);
  color: var(--color-text-tertiary);
  letter-spacing: 0.4px;
  margin-bottom: 10px;
}

.dep-desc-list {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 10px 14px;
  margin: 0;
}

.dep-desc-list > div {
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.dep-desc-list dt {
  font-size: var(--font-size-xs);
  color: var(--color-text-tertiary);
}

.dep-desc-list dd {
  margin: 0;
  font-size: var(--font-size-sm);
  font-weight: 600;
  color: var(--color-text-primary);
  font-variant-numeric: tabular-nums;
}

.dep-desc-list dd.warn-text {
  color: var(--color-warning);
}

.dep-formula {
  margin-top: 12px;
  padding: 8px 10px;
  border-radius: var(--radius-md);
  background: var(--color-surface-hover);
  border: 1px dashed var(--color-border);
  font-size: 11px;
  color: var(--color-text-secondary);
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  word-break: break-all;
}

.dep-progress-hint {
  margin-top: 6px;
  font-size: var(--font-size-xs);
  color: var(--color-text-tertiary);
}

.dep-detail-empty {
  padding: 24px 0;
}

.dep-empty-hint {
  font-size: var(--font-size-xs);
  color: var(--color-text-tertiary);
}

.dep-detail-placeholder {
  text-align: center;
  padding: 40px 12px;
}

.placeholder-mark {
  width: 48px;
  height: 48px;
  margin: 0 auto 14px;
  border-radius: var(--radius-lg);
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: 22px;
  font-weight: 700;
  color: var(--color-primary);
  background: var(--color-primary-light);
}

.placeholder-title {
  font-size: var(--font-size-base);
  font-weight: 600;
  color: var(--color-text-primary);
  margin-bottom: 10px;
}

.placeholder-text {
  margin: 0 0 6px;
  font-size: var(--font-size-xs);
  line-height: 1.7;
  color: var(--color-text-tertiary);
}

@media (max-width: 1180px) {
  .dep-layout {
    grid-template-columns: 1fr;
  }
  .dep-detail-panel {
    position: static;
  }
  .dep-metric-grid {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}

@media (max-width: 640px) {
  .dep-metric-grid {
    grid-template-columns: 1fr;
  }
  .dep-desc-list {
    grid-template-columns: 1fr;
  }
}
</style>
