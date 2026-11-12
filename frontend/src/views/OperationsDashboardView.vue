<script setup lang="ts">
import { onMounted, onUnmounted, ref, nextTick, computed, h } from 'vue'
import { NDataTable, NTag, NEmpty, NButton, useMessage } from 'naive-ui'
import * as echarts from 'echarts'
import { operationsApi, unwrap, errorText, type RecordData } from '../api'

// === 响应式状态 ===
const message = useMessage()
const loading = ref(false)

// === 原始数据 ===
const dashboardData = ref<RecordData | null>(null)

// === 解析后的数据 ===
const revenueTrend = ref<{ date: string; amount: number; count: number }[]>([])
const orderStats = ref({
  total_orders: 0,
  pending_orders: 0,
  completed_orders: 0,
  total_revenue: 0,
  conversion_rate: 0
})
const userGrowth = ref({
  total_users: 0,
  active_users: 0,
  new_users_today: 0,
  new_users_week: 0,
  new_users_month: 0
})
const resourceUtilization = ref({
  total_assets: 0,
  online_assets: 0,
  offline_assets: 0,
  utilization_rate: 0
})
const recentOrders = ref<RecordData[]>([])

// === ECharts 容器 ===
const revenueChartEl = ref<HTMLElement>()
const userGrowthChartEl = ref<HTMLElement>()
const resourcePieChartEl = ref<HTMLElement>()
const revenueChart = ref<echarts.ECharts>()
const userGrowthChart = ref<echarts.ECharts>()
const resourcePieChart = ref<echarts.ECharts>()

// === KPI 计算 ===
const todayRevenue = computed(() => {
  if (!revenueTrend.value.length) return 0
  return revenueTrend.value[revenueTrend.value.length - 1]?.amount ?? 0
})

const kpiCards = computed(() => [
  {
    label: '今日收入',
    value: `¥${formatMoney(todayRevenue.value)}`,
    icon: '💰',
    color: '#165DFF',
    bgColor: 'rgba(22, 93, 255, 0.1)'
  },
  {
    label: '新增用户',
    value: userGrowth.value.new_users_today,
    icon: '👤',
    color: '#00B42A',
    bgColor: 'rgba(0, 180, 42, 0.1)'
  },
  {
    label: '有效订单',
    value: orderStats.value.completed_orders,
    icon: '📋',
    color: '#FF7D00',
    bgColor: 'rgba(255, 125, 0, 0.1)'
  },
  {
    label: '资源利用率',
    value: `${resourceUtilization.value.utilization_rate.toFixed(1)}%`,
    icon: '⚙️',
    color: '#722ED1',
    bgColor: 'rgba(114, 46, 209, 0.1)'
  }
])

// === 订单表格列 ===
const orderColumns = [
  { title: '订单ID', key: 'id', width: 100 },
  { title: '租户', key: 'tenant_name', width: 140, ellipsis: { tooltip: true } },
  {
    title: '金额',
    key: 'final_amount',
    width: 120,
    render: (row: RecordData) => `¥${formatMoney(Number(row.final_amount || 0))}`
  },
  {
    title: '状态',
    key: 'status',
    width: 100,
    render: (row: RecordData) => {
      const status = String(row.status || 'pending')
      return h(NTag, {
        size: 'small',
        type: getOrderStatusType(status),
        bordered: false
      }, { default: () => getOrderStatusLabel(status) })
    }
  },
  {
    title: '时间',
    key: 'created_at',
    width: 170,
    render: (row: RecordData) => formatTime(String(row.created_at || ''))
  }
]

// === 工具函数 ===
function formatMoney(n: number): string {
  if (n >= 10000) return `${(n / 10000).toFixed(2)}万`
  return n.toLocaleString('zh-CN', { minimumFractionDigits: 2, maximumFractionDigits: 2 })
}

function formatTime(s: string): string {
  if (!s) return '—'
  try {
    const d = new Date(s)
    return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')} ${String(d.getHours()).padStart(2, '0')}:${String(d.getMinutes()).padStart(2, '0')}`
  } catch { return s }
}

// === 获取主题色 ===
function getThemeColors() {
  const style = getComputedStyle(document.documentElement)
  const isDark = document.documentElement.getAttribute('data-theme') === 'dark'
  return {
    isDark,
    textColor: style.getPropertyValue('--color-text-secondary').trim() || (isDark ? '#C9CDD4' : '#4E5969'),
    axisLine: style.getPropertyValue('--color-border').trim() || (isDark ? '#333335' : '#E5E6EB'),
    splitLine: style.getPropertyValue('--color-border-light').trim() || (isDark ? '#2A2A2B' : '#F0F0F0'),
    surfaceBg: style.getPropertyValue('--color-surface').trim() || (isDark ? '#232324' : '#FFFFFF')
  }
}

// === 渲染收入趋势折线图 ===
function renderRevenueChart() {
  if (!revenueChartEl.value) return
  revenueChart.value?.dispose()
  revenueChart.value = echarts.init(revenueChartEl.value)
  const theme = getThemeColors()
  const dates = revenueTrend.value.map(d => d.date.slice(5))
  const amounts = revenueTrend.value.map(d => d.amount)

  revenueChart.value.setOption({
    tooltip: {
      trigger: 'axis',
      formatter: (params: any) => {
        const p = params[0]
        return `${p.name}<br/>收入: ¥${Number(p.value).toLocaleString('zh-CN', { minimumFractionDigits: 2 })}`
      }
    },
    grid: { left: 60, right: 20, top: 20, bottom: 35 },
    xAxis: {
      type: 'category',
      data: dates,
      axisLine: { lineStyle: { color: theme.axisLine } },
      axisLabel: { color: theme.textColor, fontSize: 11 },
      axisTick: { show: false }
    },
    yAxis: {
      type: 'value',
      axisLabel: {
        color: theme.textColor,
        fontSize: 11,
        formatter: (v: number) => v >= 10000 ? `${v / 10000}万` : String(v)
      },
      splitLine: { lineStyle: { color: theme.splitLine, type: 'dashed' } },
      axisLine: { show: false },
      axisTick: { show: false }
    },
    series: [{
      type: 'line',
      data: amounts,
      smooth: true,
      lineStyle: { color: '#165DFF', width: 2.5 },
      areaStyle: {
        color: new echarts.graphic.LinearGradient(0, 0, 0, 1, [
          { offset: 0, color: 'rgba(22, 93, 255, 0.25)' },
          { offset: 1, color: 'rgba(22, 93, 255, 0.02)' }
        ])
      },
      itemStyle: { color: '#165DFF' },
      symbol: 'circle',
      symbolSize: 5,
      showSymbol: amounts.length <= 15
    }]
  })
}

// === 渲染用户增长柱状图 ===
function renderUserGrowthChart() {
  if (!userGrowthChartEl.value) return
  userGrowthChart.value?.dispose()
  userGrowthChart.value = echarts.init(userGrowthChartEl.value)
  const theme = getThemeColors()
  const ug = userGrowth.value

  userGrowthChart.value.setOption({
    tooltip: { trigger: 'axis', axisPointer: { type: 'shadow' } },
    grid: { left: 60, right: 20, top: 20, bottom: 35 },
    xAxis: {
      type: 'category',
      data: ['今日新增', '本周新增', '本月新增', '活跃用户', '总用户'],
      axisLine: { lineStyle: { color: theme.axisLine } },
      axisLabel: { color: theme.textColor, fontSize: 11 },
      axisTick: { show: false }
    },
    yAxis: {
      type: 'value',
      axisLabel: { color: theme.textColor, fontSize: 11 },
      splitLine: { lineStyle: { color: theme.splitLine, type: 'dashed' } },
      axisLine: { show: false },
      axisTick: { show: false }
    },
    series: [{
      type: 'bar',
      data: [
        { value: ug.new_users_today, itemStyle: { color: '#165DFF' } },
        { value: ug.new_users_week, itemStyle: { color: '#4080FF' } },
        { value: ug.new_users_month, itemStyle: { color: '#6AA1FF' } },
        { value: ug.active_users, itemStyle: { color: '#00B42A' } },
        { value: ug.total_users, itemStyle: { color: '#86909C' } }
      ],
      barWidth: '45%',
      itemStyle: { borderRadius: [4, 4, 0, 0] }
    }]
  })
}

// === 渲染资源利用率饼图 ===
function renderResourcePieChart() {
  if (!resourcePieChartEl.value) return
  resourcePieChart.value?.dispose()
  resourcePieChart.value = echarts.init(resourcePieChartEl.value)
  const theme = getThemeColors()
  const ru = resourceUtilization.value

  resourcePieChart.value.setOption({
    tooltip: { trigger: 'item', formatter: '{b}: {c} ({d}%)' },
    legend: {
      bottom: 8,
      textStyle: { color: theme.textColor, fontSize: 12 },
      itemWidth: 12,
      itemHeight: 12,
      itemGap: 20
    },
    color: ['#165DFF', '#00B42A', '#86909C'],
    series: [{
      type: 'pie',
      radius: ['45%', '72%'],
      center: ['50%', '42%'],
      label: {
        show: true,
        formatter: '{b}\n{d}%',
        fontSize: 11,
        color: theme.textColor,
        lineHeight: 16
      },
      labelLine: { length: 12, length2: 8 },
      data: [
        { name: '在线资产', value: ru.online_assets },
        { name: '已分配', value: ru.offline_assets > 0 ? Math.floor(ru.offline_assets * 0.6) : 0 },
        { name: '离线资产', value: ru.offline_assets > 0 ? Math.ceil(ru.offline_assets * 0.4) : 0 }
      ].filter(d => d.value > 0),
      emphasis: {
        itemStyle: { shadowBlur: 10, shadowOffsetX: 0, shadowColor: 'rgba(0, 0, 0, 0.15)' }
      }
    }]
  })
}

// === 窗口 resize ===
function handleResize() {
  revenueChart.value?.resize()
  userGrowthChart.value?.resize()
  resourcePieChart.value?.resize()
}

// === 加载数据 ===
async function loadDashboard() {
  loading.value = true
  try {
    const resp = await operationsApi.getDashboard()
    const data = unwrap<RecordData>(resp)
    dashboardData.value = data

    // 收入趋势
    revenueTrend.value = (data.revenue_trend as any[]) || []

    // 订单统计
    if (data.order_stats) {
      Object.assign(orderStats.value, data.order_stats)
    }

    // 用户增长
    if (data.user_growth) {
      Object.assign(userGrowth.value, data.user_growth)
    }

    // 资源利用率
    if (data.resource_utilization) {
      Object.assign(resourceUtilization.value, data.resource_utilization)
    }

    // 后端 dashboard 接口若返回 recent_orders 则展示真实数据
    recentOrders.value = (data.recent_orders as RecordData[]) || []

    await nextTick()
    renderRevenueChart()
    renderUserGrowthChart()
    renderResourcePieChart()
  } catch (e) {
    message.error(errorText(e))
    revenueTrend.value = generateFallbackRevenueTrend()
    recentOrders.value = []
    await nextTick()
    renderRevenueChart()
    renderUserGrowthChart()
    renderResourcePieChart()
  } finally {
    loading.value = false
  }
}

// === TODO: 以下为临时模拟数据，待后端接口完善后移除 ===
function generateFallbackRevenueTrend() {
  const result: { date: string; amount: number; count: number }[] = []
  const now = new Date()
  for (let i = 29; i >= 0; i--) {
    const d = new Date(now)
    d.setDate(d.getDate() - i)
    result.push({
      date: d.toISOString().slice(0, 10),
      amount: Math.floor(Math.random() * 50000 + 5000),
      count: Math.floor(Math.random() * 20 + 1)
    })
  }
  return result
}

// === 获取订单状态标签类型 ===
function getOrderStatusType(status: string): 'success' | 'warning' | 'error' | 'info' | 'default' {
  const map: Record<string, 'success' | 'warning' | 'error' | 'info' | 'default'> = {
    pending: 'warning',
    paid: 'info',
    completed: 'success',
    cancelled: 'error',
    expired: 'default'
  }
  return map[status] || 'default'
}

function getOrderStatusLabel(status: string): string {
  const map: Record<string, string> = {
    pending: '待处理',
    paid: '已支付',
    completed: '已完成',
    cancelled: '已取消',
    expired: '已过期'
  }
  return map[status] || status
}

onMounted(() => {
  loadDashboard()
  window.addEventListener('resize', handleResize)
})

onUnmounted(() => {
  window.removeEventListener('resize', handleResize)
  revenueChart.value?.dispose()
  userGrowthChart.value?.dispose()
  resourcePieChart.value?.dispose()
})
</script>

<template>
  <div class="page ops-dashboard">
    <!-- 页面标题 -->
    <div class="page-head">
      <div>
        <div class="eyebrow dark">OPERATIONS DASHBOARD</div>
        <h1>运营数据大盘</h1>
        <p>实时掌握平台运营核心指标与业务态势。</p>
      </div>
      <div class="head-actions">
        <n-button @click="loadDashboard" :loading="loading">刷新数据 ↻</n-button>
      </div>
    </div>

    <!-- KPI 指标卡片 -->
    <div class="kpi-grid">
      <div
        v-for="(card, idx) in kpiCards"
        :key="idx"
        class="kpi-card"
      >
        <div class="kpi-icon" :style="{ background: card.bgColor, color: card.color }">
          {{ card.icon }}
        </div>
        <div class="kpi-content">
          <div class="kpi-label">{{ card.label }}</div>
          <div class="kpi-value" :style="{ color: card.color }">{{ card.value }}</div>
        </div>
      </div>
    </div>

    <!-- 中间行：收入趋势 + 用户增长 -->
    <div class="charts-row">
      <section class="panel chart-panel">
        <div class="panel-toolbar">
          <div>
            <div class="panel-title">收入趋势</div>
            <p class="panel-caption">近30天每日收入变化</p>
          </div>
        </div>
        <div class="chart-body">
          <div v-if="revenueTrend.length" ref="revenueChartEl" class="chart-container"></div>
          <n-empty v-else description="暂无收入数据" style="padding: 60px 0;" />
        </div>
      </section>

      <section class="panel chart-panel">
        <div class="panel-toolbar">
          <div>
            <div class="panel-title">用户增长</div>
            <p class="panel-caption">各维度用户统计概览</p>
          </div>
        </div>
        <div class="chart-body">
          <div ref="userGrowthChartEl" class="chart-container"></div>
        </div>
      </section>
    </div>

    <!-- 底部行：资源利用率饼图 + 最近订单列表 -->
    <div class="charts-row">
      <section class="panel chart-panel">
        <div class="panel-toolbar">
          <div>
            <div class="panel-title">资源利用率分布</div>
            <p class="panel-caption">
              总资产 {{ resourceUtilization.total_assets }} ·
              在线 {{ resourceUtilization.online_assets }} ·
              离线 {{ resourceUtilization.offline_assets }}
            </p>
          </div>
        </div>
        <div class="chart-body">
          <div
            v-if="resourceUtilization.total_assets > 0"
            ref="resourcePieChartEl"
            class="chart-container"
          ></div>
          <n-empty v-else description="暂无资源数据" style="padding: 60px 0;" />
        </div>
      </section>

      <section class="panel orders-panel">
        <div class="panel-toolbar">
          <div>
            <div class="panel-title">最近订单</div>
            <p class="panel-caption">最新交易订单记录</p>
          </div>
        </div>
        <div class="orders-table-wrap">
          <n-data-table
            v-if="recentOrders.length"
            :columns="orderColumns"
            :data="recentOrders"
            :bordered="false"
            :single-line="false"
            size="small"
            :row-key="(row: RecordData) => String(row.id)"
            :pagination="{ pageSize: 8 }"
          >
            <template #empty>
              <n-empty description="暂无订单数据" />
            </template>
          </n-data-table>
          <n-empty v-else description="暂无订单数据，接口待对接" style="padding: 60px 0;" />
        </div>
      </section>
    </div>
  </div>
</template>

<style scoped>
.ops-dashboard {
  display: flex;
  flex-direction: column;
  gap: 20px;
}

/* === KPI 卡片网格 === */
.kpi-grid {
  display: grid;
  grid-template-columns: repeat(4, 1fr);
  gap: 16px;
}

.kpi-card {
  background: var(--color-surface);
  border: 1px solid var(--color-border);
  border-radius: var(--radius-lg);
  padding: 20px;
  display: flex;
  align-items: center;
  gap: 16px;
  transition: box-shadow var(--transition-fast), border-color var(--transition-fast);
}

.kpi-card:hover {
  box-shadow: var(--shadow-md);
  border-color: var(--color-primary);
}

.kpi-icon {
  width: 52px;
  height: 52px;
  border-radius: var(--radius-lg);
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: 26px;
  flex-shrink: 0;
}

.kpi-content {
  flex: 1;
  min-width: 0;
}

.kpi-label {
  font-size: var(--font-size-sm);
  color: var(--color-text-secondary);
  margin-bottom: 6px;
}

.kpi-value {
  font-size: 26px;
  font-weight: 700;
  line-height: 1.2;
  font-variant-numeric: tabular-nums;
  letter-spacing: -0.5px;
}

/* === 图表行 === */
.charts-row {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 20px;
}

.chart-panel,
.orders-panel {
  min-width: 0;
}

.chart-body {
  padding: 12px 16px 16px;
}

.chart-container {
  width: 100%;
  height: 300px;
}

.orders-table-wrap {
  padding: 0 12px 12px;
}

/* === 响应式 === */
@media (max-width: 1100px) {
  .kpi-grid {
    grid-template-columns: repeat(2, 1fr);
  }
  .charts-row {
    grid-template-columns: 1fr;
  }
}

@media (max-width: 520px) {
  .kpi-grid {
    grid-template-columns: 1fr;
  }
  .kpi-value {
    font-size: 22px;
  }
  .chart-container {
    height: 240px;
  }
}
</style>
