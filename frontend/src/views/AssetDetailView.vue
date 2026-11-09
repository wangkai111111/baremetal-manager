<script setup lang="ts">
import { computed, ref, watch, onMounted, onUnmounted, nextTick } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import {
  NButton,
  NTabs,
  NTabPane,
  NDescriptions,
  NDescriptionsItem,
  NDataTable,
  NTag,
  NModal,
  NForm,
  NFormItem,
  NInput,
  NSelect,
  NPopconfirm,
  useMessage,
  useDialog
} from 'naive-ui'
import * as echarts from 'echarts'
import { api, resource, unwrap, rows, errorText, type RecordData } from '../api'
import { labels } from '../catalog'
import { useAuth } from '../stores/auth'

const route = useRoute()
const router = useRouter()
const message = useMessage()
const dialog = useDialog()
const auth = useAuth()
const asset = ref<RecordData>({})
const tab = ref('overview')
const detail = ref<RecordData[]>([])
const loading = ref(false)

// === 编辑弹窗 ===
const editing = ref(false)
const saving = ref(false)
const editForm = ref<{ name?: string; management_ip?: string; bmc_address?: string; bmc_user?: string; bmc_secret?: string; mac_address?: string }>({})

// === 标签管理 ===
const tagModal = ref(false)
const assetTags = ref<RecordData[]>([])
const allTags = ref<RecordData[]>([])
const selectedTags = ref<string[]>([])
const tagLoading = ref(false)

// === 传感器数据 ===
const sensorData = ref<RecordData | null>(null)
const cpuTempGaugeEl = ref<HTMLElement>()
const fanSpeedGaugeEl = ref<HTMLElement>()
const powerBarChartEl = ref<HTMLElement>()
const cpuTempChart = ref<echarts.ECharts>()
const fanSpeedChart = ref<echarts.ECharts>()
const powerBarChart = ref<echarts.ECharts>()

// === SOL 会话状态 ===
const solSessionActive = ref(false)

const id = computed(() => String(route.params.id))

const tabs = [
  { key: 'overview', label: '基本信息', path: '' },
  { key: 'hardware', label: '硬件信息', path: 'monitor' },
  { key: 'network', label: '网络接口', path: 'monitor' },
  { key: 'storage', label: '存储设备', path: 'monitor' },
  { key: 'bmc', label: 'BMC 信息', path: 'monitor' },
  { key: 'power', label: '电源状态', path: 'monitor' },
  { key: 'firmware', label: '固件版本', path: 'monitor' },
  { key: 'installations', label: '装机记录', path: 'tasks' },
  { key: 'tasks', label: '操作任务', path: 'tasks' },
  { key: 'events', label: '事件日志', path: 'audit' }
]

async function loadAsset() {
  loading.value = true
  try {
    asset.value = unwrap<RecordData>(await resource.get('/assets', id.value))
    // 加载资产标签
    await loadAssetTags()
    // 检查 SOL 会话状态
    checkSolSession()
  } catch (e) {
    message.error(errorText(e))
  } finally {
    loading.value = false
  }
}

// === 加载资产标签 ===
async function loadAssetTags() {
  try {
    const result = rows(await api.get(`/assets/${encodeURIComponent(id.value)}/tags`))
    assetTags.value = result.items
    selectedTags.value = result.items.map(t => String(t.name || t.id))
  } catch {
    assetTags.value = []
  }
}

// === 加载所有可用标签 ===
async function loadAllTags() {
  try {
    const result = rows(await resource.list('/tags'))
    allTags.value = result.items
  } catch {
    allTags.value = []
  }
}

// === 检查 SOL 会话状态 ===
async function checkSolSession() {
  solSessionActive.value = false
  try {
    const result = rows(await resource.list('/sol-sessions', { asset_id: id.value, status: 'active' }))
    solSessionActive.value = result.items.some(session => String(session.asset_id) === id.value && session.status === 'active')
  } catch { /* 未获取会话时不显示活跃状态 */ }
}

// === 加载传感器数据 ===
async function loadSensorData() {
  try {
    const result = rows(await resource.list('/sensor-records', { asset_id: id.value, page_size: 1 }))
    sensorData.value = result.items[0] ?? null
    await nextTick()
    renderSensorCharts()
  } catch {
    sensorData.value = null
  }
}

// === 渲染传感器仪表盘 ===
function renderSensorCharts() {
  if (!sensorData.value) return

  // CPU 温度仪表盘
  if (cpuTempGaugeEl.value) {
    cpuTempChart.value?.dispose()
    cpuTempChart.value = echarts.init(cpuTempGaugeEl.value)
    const cpuTemp = Number(sensorData.value.cpu_temperature ?? sensorData.value.cpu_temp ?? NaN)
    cpuTempChart.value.setOption({
      series: [{
        type: 'gauge',
        startAngle: 200,
        endAngle: -20,
        min: 0,
        max: 100,
        radius: '90%',
        progress: { show: true, width: 12, roundCap: true },
        pointer: { show: false },
        axisLine: { lineStyle: { width: 12, color: [[0.6, '#48b7a3'], [0.8, '#e5a65c'], [1, '#d36b65']] } },
        axisTick: { show: false },
        splitLine: { show: false },
        axisLabel: { show: false },
        detail: { valueAnimation: true, fontSize: 20, fontWeight: 700, color: '#29404d', offsetCenter: [0, 0], formatter: '{value}°C' },
        data: [{ value: cpuTemp }]
      }]
    })
  }

  // 风扇转速仪表盘
  if (fanSpeedGaugeEl.value) {
    fanSpeedChart.value?.dispose()
    fanSpeedChart.value = echarts.init(fanSpeedGaugeEl.value)
    const fanSpeed = Number(sensorData.value.fan_speed ?? sensorData.value.fan_rpm ?? NaN)
    fanSpeedChart.value.setOption({
      series: [{
        type: 'gauge',
        startAngle: 200,
        endAngle: -20,
        min: 0,
        max: 10000,
        radius: '90%',
        progress: { show: true, width: 12, roundCap: true },
        pointer: { show: false },
        axisLine: { lineStyle: { width: 12, color: [[0.5, '#526b88'], [0.8, '#267b82'], [1, '#48b7a3']] } },
        axisTick: { show: false },
        splitLine: { show: false },
        axisLabel: { show: false },
        detail: { valueAnimation: true, fontSize: 18, fontWeight: 700, color: '#29404d', offsetCenter: [0, 0], formatter: '{value} RPM' },
        data: [{ value: fanSpeed }]
      }]
    })
  }

  // 电源功耗柱状图
  if (powerBarChartEl.value) {
    powerBarChart.value?.dispose()
    powerBarChart.value = echarts.init(powerBarChartEl.value)
    const powerWatts = Number(sensorData.value.power_watts ?? sensorData.value.power ?? NaN)
    const voltage = Number(sensorData.value.voltage ?? sensorData.value.power_voltage ?? NaN)
    powerBarChart.value.setOption({
      tooltip: { trigger: 'axis' },
      grid: { left: 50, right: 20, top: 20, bottom: 35 },
      xAxis: { type: 'category', data: ['功耗 (W)', '电压 (V)'], axisLine: { lineStyle: { color: '#d4dde0' } }, axisLabel: { color: '#82929d', fontSize: 11 } },
      yAxis: { type: 'value', axisLabel: { color: '#82929d', fontSize: 11 }, splitLine: { lineStyle: { color: '#edf0f0' } } },
      series: [{
        type: 'bar',
        data: [
          { value: powerWatts, itemStyle: { color: '#267b82' } },
          { value: voltage, itemStyle: { color: '#48b7a3' } }
        ],
        barWidth: '40%',
        label: { show: true, position: 'top', fontSize: 12, color: '#29404d' }
      }]
    })
  }
}

// === 响应窗口 resize ===
function resize() {
  cpuTempChart.value?.resize()
  fanSpeedChart.value?.resize()
  powerBarChart.value?.resize()
}

async function loadTab() {
  detail.value = []
  if (tab.value === 'overview') return
  if (monitorTabs.includes(tab.value)) {
    if (tab.value === 'hardware') await loadSensorData()
    return
  }
  const target = tabs.find(t => t.key === tab.value)!
  loading.value = true
  try {
    const response = await api.get(
      `/${target.path}`,
      { params: { asset_id: id.value, ...(tab.value === 'installations' ? { kind: 'reinstall' } : {}) } }
    )
    const result = unwrap<unknown>(response)
    if (
      target.path === 'monitor' &&
      result &&
      typeof result === 'object' &&
      !Array.isArray(result)
    ) {
      const monitor = result as Record<string, unknown>
      const keys: Record<string, string[]> = {
        hardware: ['system', 'cpu', 'memory'],
        network: ['network', 'interfaces', 'nics'],
        storage: ['storage', 'drives', 'disks'],
        bmc: ['bmc', 'manager'],
        power: ['power'],
        firmware: ['firmware', 'bios']
      }
      const selected = Object.fromEntries(
        (keys[tab.value] || [])
          .filter(key => key in monitor)
          .map(key => [key, monitor[key]])
      )
      detail.value = [
        Object.keys(selected).length ? selected : (monitor as RecordData)
      ]
      // 如果是硬件 Tab，加载传感器数据
      if (tab.value === 'hardware') {
        await loadSensorData()
      }
    } else {
      const values = Array.isArray(result)
        ? (result as RecordData[])
        : rows({ data: result }).items
      detail.value = values.filter(item =>
        target.path === 'tasks'
          ? String(item.asset_id) === id.value &&
            (tab.value !== 'installations' || item.kind === 'reinstall')
          : String(item.target || '').includes(id.value)
      )
    }
  } catch (e) {
    message.error(errorText(e))
  } finally {
    loading.value = false
  }
}

watch(id, () => {
  tab.value = 'overview'
  loadAsset()
}, { immediate: true })

watch(tab, loadTab)

const columns = computed(() => {
  const keys = [
    ...new Set(detail.value.flatMap(item => Object.keys(item)))
  ].filter(
    key =>
      !key.toLowerCase().includes('password') &&
      !key.toLowerCase().includes('secret')
  )
  return keys.map(key => ({
    title: labels[key] || key,
    key,
    minWidth: 130,
    render: (row: RecordData) => formatField(key, row[key])
  }))
})

// === 监控数据展示（结构化卡片，避免直接展示 JSON）===
const monitorTabs = ['hardware', 'network', 'storage', 'bmc', 'power', 'firmware']
const isMonitorTab = computed(() => monitorTabs.includes(tab.value))

const monitorLabels: Record<string, string> = {
  system: '系统信息',
  cpu: 'CPU',
  memory: '内存',
  network: '网卡',
  storage: '存储',
  bmc: 'BMC 控制器',
  power: '电源',
  firmware: '固件',
  manufacturer: '制造商',
  model: '型号',
  serial_number: '序列号',
  part_number: '部件号',
  sku: 'SKU',
  uuid: 'UUID',
  status: '状态',
  state: '状态',
  health: '健康状态',
  count: '数量',
  cores: '核心数',
  threads: '逻辑处理器',
  max_speed_mhz: '最大频率',
  total_capacity_mib: '内存容量',
  interfaces: '网卡列表',
  drives: '硬盘列表',
  mac: 'MAC 地址',
  speed_mbps: '速率',
  link: '连接状态',
  capacity_bytes: '容量',
  media_type: '介质类型',
  protocol: '接口类型',
  manager_type: '管理器类型',
  firmware_version: '固件版本',
  bios_version: 'BIOS 版本',
  bmc_firmware: 'BMC 固件版本',
  version: '版本',
  date: '发布日期',
  bios: 'BIOS'
}

// Redfish 枚举值转中文
const valueLabels: Record<string, string> = {
  On: '已开机',
  Off: '已关机',
  OK: '正常',
  Warning: '警告',
  Critical: '严重',
  Enabled: '启用',
  Disabled: '禁用',
  LinkUp: '已连接',
  LinkDown: '未连接',
  Unknown: '未知',
  Absent: '未安装',
  Present: '在位',
  PoweringOn: '开机中',
  PoweringOff: '关机中'
}

interface MonitorField {
  key: string
  label: string
  value: string
}

interface MonitorTable {
  key: string
  rows: RecordData[]
  columns: { title: string; key: string; minWidth: number; render: (row: RecordData, index: number) => string }[]
}

interface MonitorCard {
  key: string
  title: string
  fields: MonitorField[]
  tables: MonitorTable[]
}

function isPlainObject(value: unknown): value is Record<string, unknown> {
  return !!value && typeof value === 'object' && !Array.isArray(value)
}

function labelOf(key: string) {
  return monitorLabels[key] || labels[key] || key
}

function humanBytes(value: unknown) {
  const size = Number(value)
  if (!Number.isFinite(size) || size <= 0) return '—'
  const units = ['B', 'KB', 'MB', 'GB', 'TB', 'PB']
  let index = 0
  let rest = size
  while (rest >= 1024 && index < units.length - 1) {
    rest /= 1024
    index += 1
  }
  return `${rest >= 100 ? rest.toFixed(0) : rest.toFixed(1)} ${units[index]}`
}

// formatField 将任意字段值格式化为可读文本，对象转为 "字段: 值" 拼接。
function formatField(key: string, value: unknown): string {
  if (value == null || value === '') return '—'
  if (typeof value === 'boolean') return value ? '是' : '否'
  if (typeof value === 'number') {
    if (key.includes('capacity_mib')) return `${value} GB`
    if (key.includes('capacity') || key.includes('bytes')) return humanBytes(value)
    if (key.includes('mhz')) return `${value} MHz`
    if (key.includes('mbps')) {
      return value >= 1000 ? `${Number((value / 1000).toFixed(1))} Gbps` : `${value} Mbps`
    }
    return String(value)
  }
  if (typeof value === 'string') return valueLabels[value] || value
  if (Array.isArray(value)) return `${value.length} 项`
  if (isPlainObject(value)) {
    const parts = Object.entries(value)
      .filter(([, item]) => item != null && item !== '' && typeof item !== 'object')
      .map(([itemKey, item]) => `${labelOf(itemKey)}: ${formatField(itemKey, item)}`)
    return parts.length ? parts.join(' · ') : '—'
  }
  return String(value)
}

// 该字段只包含空值或空对象时不展示
function isEmptyValue(value: unknown): boolean {
  if (value == null || value === '') return true
  if (Array.isArray(value)) return value.length === 0
  if (isPlainObject(value)) {
    return Object.values(value).every(item => item == null || item === '' || isEmptyValue(item))
  }
  if (typeof value === 'number') return false
  return false
}

const monitorCards = computed<MonitorCard[]>(() => {
  const source = detail.value[0]
  if (!source) return []
  // 这些字段是原始 Redfish 引用或动作描述，不适合直接展示
  const hidden = new Set(['actions', 'detail', 'manager_ref'])
  const cards: MonitorCard[] = []
  for (const [sectionKey, section] of Object.entries(source)) {
    if (!isPlainObject(section)) continue
    const fields: MonitorField[] = []
    const tables: MonitorTable[] = []
    for (const [key, value] of Object.entries(section)) {
      if (hidden.has(key) || isEmptyValue(value)) continue
      if (Array.isArray(value)) {
        const items = value.filter(isPlainObject) as RecordData[]
        if (!items.length) continue
        const itemKeys = [...new Set(items.flatMap(item => Object.keys(item)))].filter(
          itemKey => !itemKey.toLowerCase().includes('password')
        )
        tables.push({
          key,
          rows: items,
          columns: itemKeys.map(itemKey => ({
            title: labelOf(itemKey),
            key: itemKey,
            minWidth: 110,
            render: (row: RecordData) => formatField(itemKey, row[itemKey])
          }))
        })
        continue
      }
      const text = formatField(key, value)
      if (text && text !== '—') fields.push({ key, label: labelOf(key), value: text })
    }
    if (fields.length || tables.length) {
      cards.push({ key: sectionKey, title: labelOf(sectionKey), fields, tables })
    }
  }
  return cards
})

function operate(action: string, label: string) {
  dialog.warning({
    title: `确认${label}`,
    content: `将对 ${asset.value.name || id.value} 执行${label}。该操作可能影响业务，请确认目标资产及影响范围。`,
    positiveText: '确认执行',
    negativeText: '取消',
    onPositiveClick: async () => {
      try {
        await api.post(
          `/assets/${encodeURIComponent(id.value)}/actions`,
          { kind: 'power', action }
        )
        message.success('操作已提交，请在任务中心查看结果')
        loadAsset()
      } catch (e) {
        message.error(errorText(e))
      }
    }
  })
}

// === 打开编辑弹窗 ===
function openEdit() {
  editForm.value = {
    name: String(asset.value.name || ''),
    management_ip: String(asset.value.management_ip || ''),
    bmc_address: String(asset.value.bmc_address || ''),
    bmc_user: String(asset.value.bmc_user || ''),
    bmc_secret: '',
    mac_address: String(asset.value.mac_address || '')
  }
  editing.value = true
}

// === 保存编辑 ===
async function saveEdit() {
  saving.value = true
  try {
    const payload: RecordData = { ...editForm.value }
    if (!payload.bmc_secret) delete payload.bmc_secret
    await resource.update('/assets', id.value, payload)
    message.success('资产信息已更新')
    editing.value = false
    loadAsset()
  } catch (e) {
    message.error(errorText(e))
  } finally {
    saving.value = false
  }
}

// === 删除资产 ===
async function deleteAsset() {
  try {
    await resource.remove('/assets', id.value)
    message.success('资产已删除')
    router.push('/assets')
  } catch (e) {
    message.error(errorText(e))
  }
}

// === 打开标签管理弹窗 ===
function openTagManager() {
  loadAllTags()
  tagModal.value = true
}

// === 保存标签绑定 ===
async function saveTags() {
  tagLoading.value = true
  try {
    await api.post(`/assets/${encodeURIComponent(id.value)}/tags`, {
      tag_names: selectedTags.value
    })
    message.success('标签已更新')
    tagModal.value = false
    await loadAssetTags()
  } catch (e) {
    message.error(errorText(e))
  } finally {
    tagLoading.value = false
  }
}

onMounted(() => {
  window.addEventListener('resize', resize)
})

onUnmounted(() => {
  window.removeEventListener('resize', resize)
  cpuTempChart.value?.dispose()
  fanSpeedChart.value?.dispose()
  powerBarChart.value?.dispose()
})
</script>

<template>
  <div class="page">
    <div class="page-head">
      <div>
        <div class="eyebrow dark">ASSET / {{ id }}</div>
        <h1>{{ asset.name || '资产详情' }}</h1>
        <p>
          业务 IP {{ asset.management_ip || '—' }} · BMC {{ asset.bmc_address || '—' }}
        </p>
      </div>
      <div class="head-actions asset-actions">
        <n-button @click="openEdit" v-if="auth.can('assets:write')">编辑</n-button>
        <n-popconfirm @positive-click="deleteAsset" v-if="auth.can('assets:write')">
          <template #trigger>
            <n-button type="error">删除</n-button>
          </template>
          确认删除此资产？此操作不可撤销。
        </n-popconfirm>
        <n-button @click="router.push('/assets')">← 返回列表</n-button>
      </div>
    </div>

    <!-- 资产摘要信息 -->
    <div class="asset-summary">
      <div>
        <span>当前状态</span>
        <n-tag size="small" type="success" :bordered="false">
          {{ asset.status || '未知' }}
        </n-tag>
      </div>
      <div>
        <span>资产 ID</span>
        <strong>{{ id }}</strong>
      </div>
      <div>
        <span>项目 ID</span>
        <strong>{{ asset.project_id || '—' }}</strong>
      </div>
      <div>
        <span>业务 IP</span>
        <strong>{{ asset.management_ip || '—' }}</strong>
      </div>
    </div>

    <!-- 标签展示 -->
    <div v-if="assetTags.length" style="margin-bottom: 16px;">
      <div style="display: flex; align-items: center; gap: 8px; margin-bottom: 8px;">
        <span style="font-size: 12px; color: #687e89; font-weight: 600;">标签：</span>
        <div class="tag-list">
          <n-tag
            v-for="tag in assetTags"
            :key="String(tag.id)"
            size="small"
            :bordered="false"
            :style="{ background: (tag.color as string) || '#e4f0ef', color: '#29404d' }"
          >
            {{ tag.name || tag.id }}
          </n-tag>
        </div>
        <n-button text size="small" type="primary" @click="openTagManager" v-if="auth.can('assets:write')">
          管理标签
        </n-button>
      </div>
    </div>
    <div v-else style="margin-bottom: 16px;">
      <n-button text size="small" type="primary" @click="openTagManager" v-if="auth.can('assets:write')">
        + 添加标签
      </n-button>
    </div>

    <!-- BMC 远程操作面板 -->
    <div class="panel operation-panel">
      <div>
        <strong>BMC 远程操作</strong>
        <p>电源与串口操作将直接影响物理服务器。</p>
        <!-- SOL 会话状态 -->
        <div class="sol-session-status" style="margin-top: 8px;">
          <span>SOL 会话：</span>
          <span :class="solSessionActive ? 'session-active' : 'session-inactive'">
            {{ solSessionActive ? '● 活跃' : '○ 未连接' }}
          </span>
        </div>
      </div>
      <div class="operation-actions">
        <n-button
          v-if="auth.can('assets:operate')"
          @click="operate('on', '开机')"
        >
          开机
        </n-button>
        <n-button
          v-if="auth.can('assets:operate')"
          @click="operate('off', '关机')"
        >
          关机
        </n-button>
        <n-button
          v-if="auth.can('assets:operate')"
          type="warning"
          @click="operate('restart', '重启')"
        >
          重启
        </n-button>
        <n-button
          v-if="auth.can('assets:operate')"
          type="primary"
          @click="router.push(`/assets/${id}/sol`)"
        >
          SOL 控制台 ↗
        </n-button>
      </div>
    </div>

    <!-- 详情 Tab 面板 -->
    <div class="panel detail-panel">
      <n-tabs v-model:value="tab" type="line" animated>
        <n-tab-pane
          v-for="item in tabs"
          :key="item.key"
          :name="item.key"
          :tab="item.label"
        >
          <div v-if="tab === 'overview'" class="detail-overview">
            <n-descriptions label-placement="left" bordered :column="2">
              <n-descriptions-item
                v-for="(value, key) in asset"
                v-show="
                  !String(key).includes('password') &&
                  !String(key).includes('secret')
                "
                :key="key"
                :label="labels[String(key)] || String(key)"
              >
                {{
                  typeof value === 'object'
                    ? formatField(String(key), value)
                    : String(value ?? '—')
                }}
              </n-descriptions-item>
            </n-descriptions>

            <!-- 维保信息展示 -->
            <div class="warranty-info" v-if="asset.purchase_date || asset.warranty_expiry || asset.vendor">
              <div style="font-size: 13px; font-weight: 600; color: #29404d; margin-bottom: 12px;">维保信息</div>
              <div class="info-row">
                <div class="info-item" v-if="asset.purchase_date">
                  <label>采购日期</label>
                  <span>{{ asset.purchase_date }}</span>
                </div>
                <div class="info-item" v-if="asset.warranty_expiry">
                  <label>维保到期</label>
                  <span>{{ asset.warranty_expiry }}</span>
                </div>
                <div class="info-item" v-if="asset.vendor">
                  <label>供应商</label>
                  <span>{{ asset.vendor }}</span>
                </div>
                <div class="info-item" v-if="asset.serial_number">
                  <label>序列号</label>
                  <span>{{ asset.serial_number }}</span>
                </div>
              </div>
            </div>
          </div>

          <!-- 硬件/网络/存储/BMC/电源/固件 Tab - 结构化展示 -->
          <div v-else-if="isMonitorTab">
            <div v-if="tab === 'hardware' && sensorData" class="sensor-gauges">
              <div class="sensor-gauge-card">
                <div class="gauge-title">CPU 温度</div>
                <div ref="cpuTempGaugeEl" class="gauge-chart"></div>
              </div>
              <div class="sensor-gauge-card">
                <div class="gauge-title">风扇转速</div>
                <div ref="fanSpeedGaugeEl" class="gauge-chart"></div>
              </div>
              <div class="sensor-gauge-card">
                <div class="gauge-title">电源功耗</div>
                <div ref="powerBarChartEl" class="gauge-chart"></div>
              </div>
            </div>
            <div v-if="monitorCards.length" class="monitor-cards">
              <div v-for="card in monitorCards" :key="card.key" class="monitor-card">
                <div class="monitor-card-title">{{ card.title }}</div>
                <n-descriptions
                  v-if="card.fields.length"
                  :column="2"
                  label-placement="left"
                  size="small"
                  bordered
                >
                  <n-descriptions-item
                    v-for="field in card.fields"
                    :key="field.key"
                    :label="field.label"
                  >
                    {{ field.value }}
                  </n-descriptions-item>
                </n-descriptions>
                <n-data-table
                  v-for="table in card.tables"
                  :key="table.key"
                  :columns="table.columns"
                  :data="table.rows"
                  :bordered="false"
                  size="small"
                  :style="card.fields.length ? 'margin-top: 12px' : ''"
                />
              </div>
            </div>
            <div v-if="!loading && !monitorCards.length && !sensorData" class="empty-hint">
              暂无{{ item.label }}数据
            </div>
          </div>

          <div v-else>
            <n-data-table
              :columns="columns"
              :data="detail"
              :loading="loading"
              :scroll-x="Math.max(columns.length * 130, 600)"
            />
            <div v-if="!loading && !detail.length" class="empty-hint">
              暂无{{ item.label }}数据
            </div>
          </div>
        </n-tab-pane>
      </n-tabs>
    </div>

    <!-- 编辑弹窗 -->
    <n-modal
      v-model:show="editing"
      preset="card"
      title="编辑资产"
      style="width: min(560px, 94vw)"
    >
      <n-form label-placement="top">
        <div class="resource-form-grid">
          <n-form-item label="资产名称">
            <n-input v-model:value="editForm.name" placeholder="请输入资产名称" />
          </n-form-item>
          <n-form-item label="业务 IP">
            <n-input v-model:value="editForm.management_ip" placeholder="请输入业务 IP" />
          </n-form-item>
          <n-form-item label="BMC 地址">
            <n-input v-model:value="editForm.bmc_address" placeholder="请输入 BMC 地址" />
          </n-form-item>
          <n-form-item label="BMC 用户名">
            <n-input v-model:value="editForm.bmc_user" placeholder="请输入 BMC 用户名" />
          </n-form-item>
          <n-form-item label="BMC 密码">
            <n-input v-model:value="editForm.bmc_secret" type="password" placeholder="请输入 BMC 密码" />
          </n-form-item>
          <n-form-item label="MAC 地址">
            <n-input v-model:value="editForm.mac_address" placeholder="请输入 MAC 地址" />
          </n-form-item>
        </div>
        <div class="modal-actions">
          <n-button @click="editing = false">取消</n-button>
          <n-button type="primary" :loading="saving" @click="saveEdit">保存</n-button>
        </div>
      </n-form>
    </n-modal>

    <!-- 标签管理弹窗 -->
    <n-modal
      v-model:show="tagModal"
      preset="card"
      title="管理标签"
      style="width: min(420px, 94vw)"
    >
      <n-form-item label="选择标签">
        <n-select
          v-model:value="selectedTags"
          multiple
          filterable
          :options="allTags.map(t => ({ label: String(t.name || t.id), value: String(t.name || t.id) }))"
          placeholder="请选择要绑定的标签"
        />
      </n-form-item>
      <div class="modal-actions">
        <n-button @click="tagModal = false">取消</n-button>
        <n-button type="primary" :loading="tagLoading" @click="saveTags">保存</n-button>
      </div>
    </n-modal>
  </div>
</template>
