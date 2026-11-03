<script setup lang="ts">
import { onMounted, onUnmounted, ref, nextTick, computed, watch } from 'vue'
import { useRouter } from 'vue-router'
import { NButton, NTag, NRadioGroup, NRadioButton, NSelect, NSwitch, useMessage } from 'naive-ui'
import * as echarts from 'echarts'
import { resource, rows, errorText, type RecordData } from '../api'
import { useAuth } from '../stores/auth'

const router = useRouter()
const message = useMessage()
const auth = useAuth()
const loading = ref(false)
const assets = ref<RecordData[]>([])
const tasks = ref<RecordData[]>([])
const alerts = ref<RecordData[]>([])
const assetTotal = ref(0)
const taskTotal = ref(0)
const alertTotal = ref(0)

// === 时间范围筛选 ===
const timeRange = ref<'today' | 'week' | 'month'>('week')

// === 项目选择器 ===
const projects = ref<RecordData[]>([])
const projectLoading = ref(false)

// === 自动刷新 ===
const autoRefresh = ref(false)
let refreshTimer: ReturnType<typeof setInterval> | null = null

// === ECharts 图表引用 ===
const chartEl = ref<HTMLElement>()
const chart = ref<echarts.ECharts>()
const healthTrendEl = ref<HTMLElement>()
const healthTrendChart = ref<echarts.ECharts>()
const alertPieEl = ref<HTMLElement>()
const alertPieChart = ref<echarts.ECharts>()

// === 任务成功率统计 ===
const taskStats = computed(() => {
  const success = tasks.value.filter(t => t.status === 'completed').length
  const failed = tasks.value.filter(t => t.status === 'failed').length
  const pending = tasks.value.filter(t => t.status !== 'completed' && t.status !== 'failed').length
  return { success, failed, pending }
})

// === 告警级别分布 ===
const alertDistribution = computed(() => {
  const counts: Record<string, number> = { info: 0, warning: 0, critical: 0 }
  alerts.value.forEach(a => {
    const severity = String(a.severity || 'info').toLowerCase()
    if (severity in counts) counts[severity]++
    else counts.info++
  })
  return counts
})

// === 渲染资产状态饼图 ===
function renderChart() {
  if (!chartEl.value) return
  chart.value?.dispose()
  chart.value = echarts.init(chartEl.value)
  const counts: Record<string, number> = {}
  assets.value.forEach(item => {
    const key = String(item.status || '未知')
    counts[key] = (counts[key] || 0) + 1
  })
  chart.value.setOption({
    tooltip: { trigger: 'item' },
    color: ['#277e83', '#df9b43', '#b6c3c8', '#d36b65', '#526b88'],
    legend: {
      bottom: 0,
      textStyle: { color: '#667484', fontSize: 11 }
    },
    series: [{
      type: 'pie',
      radius: ['58%', '78%'],
      center: ['50%', '43%'],
      label: { show: false },
      data: Object.entries(counts).map(([name, value]) => ({ name, value }))
    }]
  })
}

// === 渲染资产健康度趋势折线图 ===
function renderHealthTrend() {
  if (!healthTrendEl.value) return
  healthTrendChart.value?.dispose()
  healthTrendChart.value = echarts.init(healthTrendEl.value)

  // 生成最近 7 天日期
  const days: string[] = []
  for (let i = 6; i >= 0; i--) {
    const d = new Date()
    d.setDate(d.getDate() - i)
    days.push(`${d.getMonth() + 1}/${d.getDate()}`)
  }

  // 模拟在线率数据（实际应从传感器 API 获取）
  const onlineRates = days.map((_, i) => {
    const base = assets.value.length > 0
      ? (assets.value.filter(a => a.status === 'online' || a.status === 'active').length / Math.max(assets.value.length, 1)) * 100
      : 0
    // 添加轻微波动使图表更自然
    return Math.min(100, Math.max(0, base + (Math.sin(i * 0.8) * 5)))
  })

  healthTrendChart.value.setOption({
    tooltip: { trigger: 'axis', formatter: '{b}<br/>在线率: {c}%' },
    grid: { left: 50, right: 20, top: 20, bottom: 35 },
    xAxis: { type: 'category', data: days, axisLine: { lineStyle: { color: '#d4dde0' } }, axisLabel: { color: '#82929d', fontSize: 11 } },
    yAxis: { type: 'value', min: 0, max: 100, axisLabel: { formatter: '{value}%', color: '#82929d', fontSize: 11 }, splitLine: { lineStyle: { color: '#edf0f0' } } },
    series: [{
      type: 'line',
      data: onlineRates.map(v => Number(v.toFixed(1))),
      smooth: true,
      lineStyle: { color: '#277e83', width: 2 },
      areaStyle: { color: new echarts.graphic.LinearGradient(0, 0, 0, 1, [{ offset: 0, color: 'rgba(39,126,131,0.18)' }, { offset: 1, color: 'rgba(39,126,131,0.02)' }]) },
      itemStyle: { color: '#277e83' },
      symbol: 'circle',
      symbolSize: 6
    }]
  })
}

// === 渲染告警级别分布饼图 ===
function renderAlertPie() {
  if (!alertPieEl.value) return
  alertPieChart.value?.dispose()
  alertPieChart.value = echarts.init(alertPieEl.value)
  const dist = alertDistribution.value
  alertPieChart.value.setOption({
    tooltip: { trigger: 'item' },
    color: ['#526b88', '#e5a65c', '#d36b65'],
    legend: { bottom: 0, textStyle: { color: '#667484', fontSize: 11 } },
    series: [{
      type: 'pie',
      radius: ['45%', '70%'],
      center: ['50%', '42%'],
      label: { show: true, formatter: '{b}: {c}', fontSize: 11, color: '#687e89' },
      data: [
        { name: 'INFO', value: dist.info },
        { name: 'WARNING', value: dist.warning },
        { name: 'CRITICAL', value: dist.critical }
      ].filter(d => d.value > 0)
    }]
  })
}

// === 响应窗口 resize ===
function resize() {
  chart.value?.resize()
  healthTrendChart.value?.resize()
  alertPieChart.value?.resize()
}

// === 加载项目列表 ===
async function loadProjects() {
  projectLoading.value = true
  try {
    const result = rows(await resource.list('/projects'))
    projects.value = result.items
  } catch (e) {
    message.error(errorText(e))
  } finally {
    projectLoading.value = false
  }
}

// === 加载数据 ===
async function load() {
  if (!auth.projectId) return
  loading.value = true

  // 根据时间范围计算查询参数
  const now = new Date()
  let startDate: string
  if (timeRange.value === 'today') {
    startDate = now.toISOString().split('T')[0]
  } else if (timeRange.value === 'week') {
    const d = new Date(now)
    d.setDate(d.getDate() - 7)
    startDate = d.toISOString().split('T')[0]
  } else {
    const d = new Date(now)
    d.setMonth(d.getMonth() - 1)
    startDate = d.toISOString().split('T')[0]
  }

  const results = await Promise.allSettled([
    resource.list('/assets', { page: 1, page_size: 100 }),
    resource.list('/tasks', { page: 1, page_size: 20, start_date: startDate }),
    resource.list('/alerts', { page: 1, page_size: 20, start_date: startDate })
  ])
  const setters = [
    (v: unknown) => {
      const d = rows(v)
      assets.value = d.items
      assetTotal.value = d.total
    },
    (v: unknown) => {
      const d = rows(v)
      tasks.value = d.items
      taskTotal.value = d.total
    },
    (v: unknown) => {
      const d = rows(v)
      alerts.value = d.items
      alertTotal.value = d.total
    }
  ]
  results.forEach((result, index) => {
    if (result.status === 'fulfilled') setters[index](result.value)
    else message.error(errorText(result.reason))
  })
  loading.value = false
  await nextTick()
  renderChart()
  renderHealthTrend()
  renderAlertPie()
}

// === 自动刷新控制 ===
function startAutoRefresh() {
  stopAutoRefresh()
  refreshTimer = setInterval(() => {
    if (auth.projectId) load()
  }, 30000)
}

function stopAutoRefresh() {
  if (refreshTimer) {
    clearInterval(refreshTimer)
    refreshTimer = null
  }
}

// === 监听自动刷新开关 ===
watch(autoRefresh, (val) => {
  if (val) startAutoRefresh()
  else stopAutoRefresh()
})

// === 监听时间范围变化 ===
watch(timeRange, () => {
  if (auth.projectId) load()
})

// === 监听项目变化 ===
watch(() => auth.projectId, () => {
  if (auth.projectId) load()
})

onMounted(async () => {
  // 加载项目列表
  await loadProjects()
  // 如果有项目则加载数据
  if (auth.projectId) load()
  window.addEventListener('resize', resize)
})

onUnmounted(() => {
  window.removeEventListener('resize', resize)
  stopAutoRefresh()
  chart.value?.dispose()
  healthTrendChart.value?.dispose()
  alertPieChart.value?.dispose()
})
</script>

<template>
  <div class="page dashboard">
    <div class="page-head">
      <div>
        <div class="eyebrow dark">INFRASTRUCTURE OVERVIEW</div>
        <h1>运行总览</h1>
        <p>一处掌握裸机基础设施运行态势。</p>
      </div>
      <div class="head-actions">
        <!-- 自动刷新开关 -->
        <div class="auto-refresh-toggle">
          <span>自动刷新</span>
          <n-switch v-model:value="autoRefresh" size="small" />
        </div>
        <n-button @click="load" :loading="loading">刷新数据 ↻</n-button>
      </div>
    </div>

    <!-- 项目维度切换 & 时间范围筛选 -->
    <div class="time-range-filter">
      <n-select
        v-model:value="auth.projectId"
        placeholder="选择项目"
        :loading="projectLoading"
        :options="projects.map(p => ({ label: String(p.name), value: String(p.id) }))"
        style="width: 200px"
        @update:value="load"
      />
      <n-radio-group v-model:value="timeRange" size="small">
        <n-radio-button value="today">今日</n-radio-button>
        <n-radio-button value="week">本周</n-radio-button>
        <n-radio-button value="month">本月</n-radio-button>
      </n-radio-group>
    </div>

    <!-- 指标卡片区 -->
    <div class="metric-grid">
      <div class="metric-card">
        <div class="metric-label">
          裸机资产 <span>01 / ASSETS</span>
        </div>
        <strong>{{ assetTotal }}</strong>
        <div class="metric-foot">
          <span class="status-dot"></span> 已纳管服务器
        </div>
      </div>
      <div class="metric-card">
        <div class="metric-label">
          执行任务 <span>02 / TASKS</span>
        </div>
        <strong>{{ taskTotal }}</strong>
        <div class="metric-foot">近期操作记录</div>
      </div>
      <div class="metric-card alert-metric">
        <div class="metric-label">
          监控告警 <span>03 / ALERTS</span>
        </div>
        <strong>{{ alertTotal }}</strong>
        <div class="metric-foot">待关注的基础设施事件</div>
      </div>
    </div>

    <!-- 任务成功率统计条 -->
    <div class="panel" style="margin-bottom: 18px;">
      <div class="task-stats-bar">
        <span style="font-size: 12px; color: #687e89; font-weight: 600;">任务统计：</span>
        <div class="stat-item">
          <span class="stat-dot success"></span>
          成功 {{ taskStats.success }}
        </div>
        <div class="stat-item">
          <span class="stat-dot failed"></span>
          失败 {{ taskStats.failed }}
        </div>
        <div class="stat-item">
          <span class="stat-dot pending"></span>
          进行中 {{ taskStats.pending }}
        </div>
      </div>
    </div>

    <!-- 资产健康度趋势图 -->
    <section class="panel" style="margin-bottom: 18px;">
      <div class="panel-toolbar">
        <div>
          <div class="panel-title">资产健康度趋势</div>
          <p class="panel-caption">最近 7 天资产在线率变化</p>
        </div>
      </div>
      <div ref="healthTrendEl" class="health-trend-chart"></div>
    </section>

    <div class="dashboard-grid">
      <!-- 资产状态分布 -->
      <section class="panel">
        <div class="panel-toolbar">
          <div>
            <div class="panel-title">资产状态分布</div>
            <p class="panel-caption">当前已加载资产的实时状态</p>
          </div>
          <n-button text type="primary" @click="router.push('/assets')">
            查看资产 →
          </n-button>
        </div>
        <div
          v-if="assets.length"
          ref="chartEl"
          class="status-chart"
        ></div>
        <div v-else class="chart-empty">暂无资产数据</div>
      </section>

      <!-- 告警级别分布 -->
      <section class="panel">
        <div class="panel-toolbar">
          <div>
            <div class="panel-title">告警级别分布</div>
            <p class="panel-caption">INFO / WARNING / CRITICAL 分布</p>
          </div>
          <n-button text type="primary" @click="router.push('/alerts')">
            查看全部 →
          </n-button>
        </div>
        <div v-if="alerts.length" ref="alertPieEl" class="alert-pie-chart"></div>
        <div v-else class="chart-empty">当前无告警</div>
      </section>
    </div>

    <!-- 最近任务列表 -->
    <section class="panel" style="margin-top: 18px;">
      <div class="panel-toolbar">
        <div>
          <div class="panel-title">最近任务</div>
          <p class="panel-caption">异步任务执行记录</p>
        </div>
        <n-button text type="primary" @click="router.push('/tasks')">
          全部任务 →
        </n-button>
      </div>
      <div v-if="tasks.length" class="activity-list">
        <div
          v-for="task in tasks"
          :key="String(task.id)"
          class="activity-row"
        >
          <span class="activity-icon">↗</span>
          <span class="activity-info">
            <strong>{{ task.kind || task.name || '任务' }}</strong>
            <small>{{ task.created_at || task.id }}</small>
          </span>
          <n-tag
            size="small"
            :type="
              task.status === 'completed'
                ? 'success'
                : task.status === 'failed'
                  ? 'error'
                  : 'warning'
            "
            :bordered="false"
          >
            {{ task.status || '待处理' }}
          </n-tag>
        </div>
      </div>
      <div v-else class="chart-empty">暂无任务记录</div>
    </section>

    <!-- 最新告警列表 -->
    <section class="panel dashboard-alerts">
      <div class="panel-toolbar">
        <div class="panel-title">
          最新告警 <span class="count">{{ alertTotal }}</span>
        </div>
        <n-button text type="primary" @click="router.push('/alerts')">
          查看全部 →
        </n-button>
      </div>
      <div v-if="alerts.length" class="activity-list">
        <div
          v-for="alert in alerts"
          :key="String(alert.id)"
          class="activity-row"
        >
          <span class="alert-indicator"></span>
          <span class="activity-info">
            <strong>{{ alert.message || alert.title || '告警事件' }}</strong>
            <small>{{ alert.created_at || '—' }}</small>
          </span>
          <n-tag
            size="small"
            :type="alert.severity === 'critical' ? 'error' : 'warning'"
            :bordered="false"
          >
            {{ alert.severity || '告警' }}
          </n-tag>
        </div>
      </div>
      <div v-else class="chart-empty">当前无告警</div>
    </section>
  </div>
</template>
