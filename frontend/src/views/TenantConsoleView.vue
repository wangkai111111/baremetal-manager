<script setup lang="ts">
import { onMounted, ref, computed, watch, h } from 'vue'
import { useRouter } from 'vue-router'
import {
  NButton, NTag, NDataTable, NModal, NForm, NFormItem, NInput, NSelect,
  NDatePicker, NTabPane, NTabs, NEmpty,
  NSpin, NAlert, NSpace, NInputNumber, useMessage
} from 'naive-ui'
import { resource, rows, unwrap, errorText, accountApi, type RecordData } from '../api'

const router = useRouter()
const message = useMessage()

// === 加载状态 ===
const loading = ref(false)
const assets = ref<RecordData[]>([])
const bills = ref<RecordData[]>([])
const payments = ref<RecordData[]>([])
const workOrders = ref<RecordData[]>([])
const accountInfo = ref<RecordData | null>(null)
const orders = ref<RecordData[]>([])

// === 分页 ===
const serverPage = ref(1)
const serverPageSize = ref(10)
const serverTotal = ref(0)
const billPage = ref(1)
const billPageSize = ref(10)
const billTotal = ref(0)
const paymentPage = ref(1)
const paymentPageSize = ref(10)
const paymentTotal = ref(0)
const woPage = ref(1)
const woPageSize = ref(10)
const woTotal = ref(0)

// === 选项卡 ===
const activeTab = ref('servers')
const billingSubTab = ref('bills')

// === 账单日期筛选 ===
const billDateRange = ref<[number, number] | null>(null)

// === 工单弹窗 ===
const showTicketModal = ref(false)
const ticketForm = ref({ type: 'technical', title: '', description: '', priority: 'medium' })
const ticketSubmitting = ref(false)

// === 余额预警设置 ===
const balanceWarningEnabled = ref(false)
const balanceWarningThreshold = ref(100)

// === 资产统计 ===
const runningCount = computed(() => assets.value.filter(a => a.status === 'running' || a.power_state === 'on').length)
const stoppedCount = computed(() => assets.value.filter(a => a.status === 'stopped' || a.power_state === 'off').length)
const expiringCount = computed<number | null>(() => {
  // 当前数据模型中 orders 无 expires_at / end_date 字段，无法计算到期数
  // 后端新增字段后此处应替换为实际逻辑：筛选 30 天内到期的记录
  const hasExpiryField = orders.value.some(o => o.expires_at || o.end_date)
  if (!hasExpiryField) return null
  const now = Date.now()
  const days30 = 30 * 86400000
  return orders.value.filter(o => {
    const expiry = o.expires_at || o.end_date
    if (!expiry) return false
    const expiryTime = new Date(expiry as string).getTime()
    return expiryTime > now && expiryTime - now <= days30
  }).length
})
const monthlyCost = computed(() => {
  const now = new Date()
  const monthStart = new Date(now.getFullYear(), now.getMonth(), 1).toISOString()
  return bills.value
    .filter(b => b.period_start && b.period_start >= monthStart)
    .reduce((sum, b) => sum + Number(b.total_amount || 0), 0)
})
const accountBalance = computed(() => Number(accountInfo.value?.balance || 0))

// === 分页对象 ===
const serverPagination = computed(() => ({
  page: serverPage.value,
  pageSize: serverPageSize.value,
  itemCount: serverTotal.value,
  showSizePicker: false,
  onUpdatePage: handleServerPageChange
}))
const billPagination = computed(() => ({
  page: billPage.value,
  pageSize: billPageSize.value,
  itemCount: billTotal.value,
  showSizePicker: false,
  onUpdatePage: handleBillPageChange
}))
const paymentPagination = computed(() => ({
  page: paymentPage.value,
  pageSize: paymentPageSize.value,
  itemCount: paymentTotal.value,
  showSizePicker: false,
  onUpdatePage: handlePaymentPageChange
}))
const woPagination = computed(() => ({
  page: woPage.value,
  pageSize: woPageSize.value,
  itemCount: woTotal.value,
  showSizePicker: false,
  onUpdatePage: handleWoPageChange
}))

// === 服务器列表列定义 ===
const serverColumns = [
  { title: '名称', key: 'name', ellipsis: { tooltip: true }, render: (row: RecordData) => h('strong', { style: 'color:var(--color-text-primary)' }, String(row.name || '—')) },
  { title: '管理 IP', key: 'management_ip', ellipsis: { tooltip: true } },
  { title: '规格', key: 'spec', ellipsis: { tooltip: true }, render: (row: RecordData) => h('span', {}, String(row.manufacturer || row.model || '—')) },
  {
    title: '状态', key: 'status', width: 110,
    render: (row: RecordData) => {
      const s = String(row.status || row.power_state || 'unknown')
      const map: Record<string, { label: string; cls: string }> = {
        running: { label: '运行中', cls: 'status-running' },
        active: { label: '运行中', cls: 'status-running' },
        on: { label: '运行中', cls: 'status-running' },
        stopped: { label: '已关机', cls: 'status-stopped' },
        off: { label: '已关机', cls: 'status-stopped' },
        fault: { label: '故障', cls: 'status-fault' },
        error: { label: '故障', cls: 'status-fault' },
        maintenance: { label: '维护中', cls: 'status-maintenance' }
      }
      const info = map[s] || { label: s, cls: 'status-stopped' }
      return h('span', { class: info.cls }, info.label)
    }
  },
  {
    title: '操作', key: 'actions', width: 200,
    render: (row: RecordData) => h(NSpace, { size: 4 }, {
      default: () => [
        h(NButton, { size: 'small', quaternary: true, type: 'primary', onClick: () => router.push(`/assets/${row.id}`) }, { default: () => '详情' }),
        h(NButton, { size: 'small', quaternary: true, type: 'info', onClick: () => router.push(`/assets/${row.id}/sol`) }, { default: () => '控制台' }),
        h(NButton, { size: 'small', quaternary: true, type: 'warning', onClick: () => handleRestart(row) }, { default: () => '重启' })
      ]
    })
  }
]

// === 账单列定义 ===
const billColumns = [
  { title: '账单周期', key: 'period', render: (row: RecordData) => `${String(row.period_start || '').slice(0, 10)} ~ ${String(row.period_end || '').slice(0, 10)}` },
  { title: '金额', key: 'total_amount', render: (row: RecordData) => `¥${Number(row.total_amount || 0).toFixed(2)}` },
  {
    title: '状态', key: 'status', width: 100,
    render: (row: RecordData) => {
      const s = String(row.status || '')
      const typeMap: Record<string, 'success' | 'warning' | 'error' | 'info' | 'default'> = {
        paid: 'success', issued: 'info', draft: 'default', overdue: 'error', unpaid: 'warning'
      }
      const labelMap: Record<string, string> = { paid: '已支付', issued: '已出账', draft: '草稿', overdue: '已逾期', unpaid: '待支付' }
      return h(NTag, { size: 'small', type: typeMap[s] || 'default', bordered: false }, { default: () => labelMap[s] || s })
    }
  },
  { title: '到期日', key: 'due_date', render: (row: RecordData) => String(row.due_date || '').slice(0, 10) },
  { title: '创建时间', key: 'created_at', render: (row: RecordData) => String(row.created_at || '').slice(0, 10) }
]

// === 充值记录列 ===
const paymentColumns = [
  { title: '金额', key: 'amount', render: (row: RecordData) => `¥${Number(row.amount || 0).toFixed(2)}` },
  {
    title: '方式', key: 'method', width: 100,
    render: (row: RecordData) => {
      const m: Record<string, string> = { alipay: '支付宝', wechat: '微信', transfer: '银行转账', balance: '余额' }
      return m[String(row.method || '')] || String(row.method || '—')
    }
  },
  {
    title: '状态', key: 'status', width: 100,
    render: (row: RecordData) => {
      const s = String(row.status || '')
      const typeMap: Record<string, 'success' | 'warning' | 'error' | 'default'> = { success: 'success', completed: 'success', pending: 'warning', failed: 'error' }
      const labelMap: Record<string, string> = { success: '成功', completed: '成功', pending: '处理中', failed: '失败' }
      return h(NTag, { size: 'small', type: typeMap[s] || 'default', bordered: false }, { default: () => labelMap[s] || s })
    }
  },
  { title: '支付时间', key: 'paid_at', render: (row: RecordData) => String(row.paid_at || '').slice(0, 16).replace('T', ' ') },
  { title: '创建时间', key: 'created_at', render: (row: RecordData) => String(row.created_at || '').slice(0, 10) }
]

// === 工单列定义 ===
const woColumns = [
  { title: 'ID', key: 'id', width: 60 },
  { title: '标题', key: 'title', ellipsis: { tooltip: true } },
  {
    title: '类型', key: 'type', width: 90,
    render: (row: RecordData) => {
      const m: Record<string, string> = { fault: '故障', technical: '技术', consultation: '咨询', other: '其他', incident: '故障', request: '请求', maintenance: '维护', inspection: '巡检' }
      return m[String(row.type || '')] || String(row.type || '—')
    }
  },
  {
    title: '优先级', key: 'priority', width: 90,
    render: (row: RecordData) => {
      const p = String(row.priority || '')
      const typeMap: Record<string, 'error' | 'warning' | 'info' | 'default'> = { urgent: 'error', high: 'warning', critical: 'error', medium: 'info', low: 'default' }
      const labelMap: Record<string, string> = { urgent: '紧急', high: '高', critical: '紧急', medium: '中', low: '低' }
      return h(NTag, { size: 'small', type: typeMap[p] || 'default', bordered: false }, { default: () => labelMap[p] || p })
    }
  },
  {
    title: '状态', key: 'status', width: 100,
    render: (row: RecordData) => {
      const s = String(row.status || '')
      const typeMap: Record<string, 'warning' | 'info' | 'success' | 'default'> = { open: 'warning', in_progress: 'info', waiting: 'warning', resolved: 'success', closed: 'default' }
      const labelMap: Record<string, string> = { open: '待处理', in_progress: '处理中', waiting: '等待中', resolved: '已解决', closed: '已关闭' }
      return h(NTag, { size: 'small', type: typeMap[s] || 'default', bordered: false }, { default: () => labelMap[s] || s })
    }
  },
  { title: '创建时间', key: 'created_at', width: 120, render: (row: RecordData) => String(row.created_at || '').slice(0, 10) }
]

// === 数据加载 ===
async function loadAssets() {
  try {
    const result = rows(await resource.list('/assets', { page: serverPage.value, page_size: serverPageSize.value }))
    assets.value = result.items
    serverTotal.value = result.total
  } catch (e) {
    message.error(errorText(e))
  }
}

async function loadAccount() {
  try {
    accountInfo.value = unwrap(await accountApi.get())
  } catch {
    // 账户可能不存在，静默处理
    accountInfo.value = null
  }
}

async function loadBills() {
  try {
    const params: Record<string, unknown> = { page: billPage.value, page_size: billPageSize.value }
    if (billDateRange.value) {
      params.period_start = new Date(billDateRange.value[0]).toISOString()
      params.period_end = new Date(billDateRange.value[1]).toISOString()
    }
    const result = rows(await resource.list('/bills', params))
    bills.value = result.items
    billTotal.value = result.total
  } catch (e) {
    message.error(errorText(e))
  }
}

async function loadPayments() {
  try {
    const result = rows(await resource.list('/payments', { page: paymentPage.value, page_size: paymentPageSize.value }))
    payments.value = result.items
    paymentTotal.value = result.total
  } catch (e) {
    message.error(errorText(e))
  }
}

async function loadOrders() {
  try {
    const result = rows(await resource.list('/orders', { page: 1, page_size: 100 }))
    orders.value = result.items
  } catch {
    orders.value = []
  }
}

async function loadWorkOrders() {
  try {
    const result = rows(await resource.list('/work-orders', { page: woPage.value, page_size: woPageSize.value }))
    workOrders.value = result.items
    woTotal.value = result.total
  } catch (e) {
    message.error(errorText(e))
  }
}

async function loadAll() {
  loading.value = true
  await Promise.allSettled([loadAssets(), loadAccount(), loadBills(), loadPayments(), loadOrders(), loadWorkOrders()])
  loading.value = false
}

// === 操作 ===
async function handleRestart(row: RecordData) {
  try {
    await resource.create(`/assets/${row.id}/actions`, { action: 'restart' })
    message.success(`已发送重启指令到 ${row.name}`)
  } catch (e) {
    message.error(errorText(e))
  }
}

async function submitTicket() {
  if (!ticketForm.value.title.trim()) {
    message.warning('请输入工单标题')
    return
  }
  ticketSubmitting.value = true
  try {
    await resource.create('/work-orders', {
      type: ticketForm.value.type,
      title: ticketForm.value.title,
      description: ticketForm.value.description,
      priority: ticketForm.value.priority
    })
    message.success('工单提交成功')
    showTicketModal.value = false
    ticketForm.value = { type: 'technical', title: '', description: '', priority: 'medium' }
    await loadWorkOrders()
  } catch (e) {
    message.error(errorText(e))
  } finally {
    ticketSubmitting.value = false
  }
}

function exportBills() {
  // TODO: 调用真实导出接口
  message.info('账单导出功能开发中')
}

// === 分页回调 ===
function handleServerPageChange(page: number) {
  serverPage.value = page
  loadAssets()
}
function handleBillPageChange(page: number) {
  billPage.value = page
  loadBills()
}
function handlePaymentPageChange(page: number) {
  paymentPage.value = page
  loadPayments()
}
function handleWoPageChange(page: number) {
  woPage.value = page
  loadWorkOrders()
}

// === 监听日期筛选 ===
watch(billDateRange, () => {
  billPage.value = 1
  loadBills()
})

onMounted(() => {
  loadAll()
})
</script>

<template>
  <div class="page tenant-console">
    <!-- 页面标题 -->
    <div class="page-head">
      <div>
        <div class="eyebrow dark">TENANT CONSOLE</div>
        <h1>服务器管理</h1>
        <p>管理购买后已交付的裸金属服务器，查看账单与工单。</p>
      </div>
      <div class="head-actions">
        <n-button @click="loadAll" :loading="loading">刷新数据 ↻</n-button>
      </div>
    </div>

    <n-spin :show="loading">
      <!-- 顶部资产卡片 -->
      <div class="metric-grid tenant-metric-grid">
        <div class="metric-card">
          <div class="metric-icon primary">
            <svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><rect x="2" y="3" width="20" height="14" rx="2"/><line x1="8" y1="21" x2="16" y2="21"/><line x1="12" y1="17" x2="12" y2="21"/></svg>
          </div>
          <div class="metric-content">
            <div class="metric-label">运行中服务器</div>
            <div class="metric-value">{{ runningCount }}</div>
            <div class="metric-foot">当前在线实例</div>
          </div>
        </div>
        <div class="metric-card">
          <div class="metric-icon info">
            <svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><rect x="2" y="3" width="20" height="14" rx="2"/><line x1="12" y1="17" x2="12" y2="21"/></svg>
          </div>
          <div class="metric-content">
            <div class="metric-label">已关机服务器</div>
            <div class="metric-value">{{ stoppedCount }}</div>
            <div class="metric-foot">离线实例</div>
          </div>
        </div>
        <div class="metric-card">
          <div class="metric-icon warning">
            <svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><circle cx="12" cy="12" r="10"/><polyline points="12 6 12 12 16 14"/></svg>
          </div>
          <div class="metric-content">
            <div class="metric-label">即将到期资源</div>
            <div class="metric-value">{{ expiringCount === null ? '—' : expiringCount }}</div>
            <div class="metric-foot">{{ expiringCount === null ? '暂无数据' : '30 天内到期' }}</div>
          </div>
        </div>
        <div class="metric-card">
          <div class="metric-icon success">
            <svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><line x1="12" y1="1" x2="12" y2="23"/><path d="M17 5H9.5a3.5 3.5 0 0 0 0 7h5a3.5 3.5 0 0 1 0 7H6"/></svg>
          </div>
          <div class="metric-content">
            <div class="metric-label">本月消费</div>
            <div class="metric-value">¥{{ monthlyCost.toFixed(2) }}</div>
            <div class="metric-foot">当月账单总额</div>
          </div>
        </div>
      </div>

      <!-- 快捷操作入口 -->
      <div class="quick-actions-row">
        <div class="quick-action-card" @click="router.push('/purchase')">
          <div class="qa-icon" style="background: var(--color-primary-light); color: var(--color-primary);">
            <svg width="28" height="28" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><circle cx="9" cy="21" r="1"/><circle cx="20" cy="21" r="1"/><path d="M1 1h4l2.68 13.39a2 2 0 0 0 2 1.61h9.72a2 2 0 0 0 2-1.61L23 6H6"/></svg>
          </div>
          <div class="qa-text">
            <strong>购买服务器</strong>
            <span>选购产品并开通服务</span>
          </div>
          <div class="qa-arrow">→</div>
        </div>
        <div class="quick-action-card" @click="activeTab = 'billing'">
          <div class="qa-icon" style="background: var(--color-warning-light); color: var(--color-warning);">
            <svg width="28" height="28" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><rect x="1" y="4" width="22" height="16" rx="2"/><line x1="1" y1="10" x2="23" y2="10"/></svg>
          </div>
          <div class="qa-text">
            <strong>续费管理</strong>
            <span>查看账单与续费</span>
          </div>
          <div class="qa-arrow">→</div>
        </div>
        <div class="quick-action-card" @click="showTicketModal = true">
          <div class="qa-icon" style="background: var(--color-success-light); color: var(--color-success);">
            <svg width="28" height="28" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M21 15a2 2 0 0 1-2 2H7l-4 4V5a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2z"/></svg>
          </div>
          <div class="qa-text">
            <strong>提交工单</strong>
            <span>技术支持与服务请求</span>
          </div>
          <div class="qa-arrow">→</div>
        </div>
      </div>

      <!-- 余额预警 -->
      <n-alert v-if="accountBalance < balanceWarningThreshold && balanceWarningEnabled" type="warning" style="margin-bottom: 16px;" :bordered="false">
        账户余额不足 ¥{{ balanceWarningThreshold }}，当前余额 ¥{{ accountBalance.toFixed(2) }}，请及时充值。
      </n-alert>

      <!-- 主内容区选项卡 -->
      <n-tabs v-model:value="activeTab" type="line" animated>
        <!-- 服务器管理 -->
        <n-tab-pane name="servers" tab="服务器管理">
          <section class="panel" style="margin-top: 16px;">
            <div class="panel-toolbar">
              <div>
                <div class="panel-title">服务器列表</div>
                <p class="panel-caption">仅展示购买后已完成交付的裸金属服务器</p>
              </div>
              <n-button type="primary" size="small" @click="router.push('/purchase')">购买服务器</n-button>
            </div>
            <n-data-table
              :columns="serverColumns"
              :data="assets"
              :loading="loading"
              :pagination="serverPagination"
              :bordered="false"
              :single-line="false"
              remote
            />
            <div v-if="!assets.length && !loading" class="empty-hint">
              <n-empty description="暂无已交付服务器，请先购买并等待交付" />
            </div>
          </section>
        </n-tab-pane>

        <!-- 计费中心 -->
        <n-tab-pane name="billing" tab="计费中心">
          <div style="margin-top: 16px;">
            <n-tabs v-model:value="billingSubTab" type="bar" size="small">
              <!-- 账单查询 -->
              <n-tab-pane name="bills" tab="账单查询">
                <div class="billing-toolbar">
                  <n-date-picker v-model:value="billDateRange" type="daterange" clearable style="width: 280px;" />
                  <n-button @click="exportBills" size="small">导出账单</n-button>
                </div>
                <n-data-table
                  :columns="billColumns"
                  :data="bills"
                  :loading="loading"
                  :pagination="billPagination"
                  :bordered="false"
                  :single-line="false"
                  remote
                />
                <div v-if="!bills.length && !loading" class="empty-hint"><n-empty description="暂无账单记录" /></div>
              </n-tab-pane>

              <!-- 充值记录 -->
              <n-tab-pane name="payments" tab="充值记录">
                <n-data-table
                  :columns="paymentColumns"
                  :data="payments"
                  :loading="loading"
                  :pagination="paymentPagination"
                  :bordered="false"
                  :single-line="false"
                  remote
                />
                <div v-if="!payments.length && !loading" class="empty-hint"><n-empty description="暂无充值记录" /></div>
              </n-tab-pane>

              <!-- 消费明细 -->
              <n-tab-pane name="consumption" tab="消费明细">
                <div class="consumption-summary">
                  <div class="consumption-card">
                    <span>账户余额</span>
                    <strong>¥{{ accountBalance.toFixed(2) }}</strong>
                  </div>
                  <div class="consumption-card">
                    <span>本月消费</span>
                    <strong>¥{{ monthlyCost.toFixed(2) }}</strong>
                  </div>
                  <div class="consumption-card">
                    <span>信用额度</span>
                    <strong>¥{{ Number(accountInfo?.credit_limit || 0).toFixed(2) }}</strong>
                  </div>
                </div>
                <!-- 余额预警设置 -->
                <div class="warning-setting">
                  <span>余额预警</span>
                  <n-button size="tiny" :quaternary="!balanceWarningEnabled" :type="balanceWarningEnabled ? 'primary' : 'default'" @click="balanceWarningEnabled = !balanceWarningEnabled">
                    {{ balanceWarningEnabled ? '已开启' : '未开启' }}
                  </n-button>
                  <n-input-number v-if="balanceWarningEnabled" v-model:value="balanceWarningThreshold" :min="0" size="small" style="width: 140px;" placeholder="预警阈值" />
                </div>
              </n-tab-pane>
            </n-tabs>
          </div>
        </n-tab-pane>

        <!-- 工单系统 -->
        <n-tab-pane name="tickets" tab="工单系统">
          <section class="panel" style="margin-top: 16px;">
            <div class="panel-toolbar">
              <div>
                <div class="panel-title">我的工单</div>
                <p class="panel-caption">提交的技术支持与服务请求</p>
              </div>
              <n-button type="primary" size="small" @click="showTicketModal = true">+ 提交工单</n-button>
            </div>
            <n-data-table
              :columns="woColumns"
              :data="workOrders"
              :loading="loading"
              :pagination="woPagination"
              :bordered="false"
              :single-line="false"
              remote
            />
            <div v-if="!workOrders.length && !loading" class="empty-hint"><n-empty description="暂无工单记录" /></div>
          </section>
        </n-tab-pane>
      </n-tabs>
    </n-spin>

    <!-- 提交工单弹窗 -->
    <n-modal v-model:show="showTicketModal" preset="card" title="提交工单" style="width: 560px; max-width: 95vw;">
      <n-form :model="ticketForm" label-placement="left" label-width="80">
        <n-form-item label="工单类型">
          <n-select v-model:value="ticketForm.type" :options="[
            { label: '故障报告', value: 'fault' },
            { label: '技术支持', value: 'technical' },
            { label: '咨询', value: 'consultation' },
            { label: '其他', value: 'other' }
          ]" />
        </n-form-item>
        <n-form-item label="标题" required>
          <n-input v-model:value="ticketForm.title" placeholder="请简要描述问题" maxlength="256" show-count />
        </n-form-item>
        <n-form-item label="优先级">
          <n-select v-model:value="ticketForm.priority" :options="[
            { label: '低', value: 'low' },
            { label: '中', value: 'medium' },
            { label: '高', value: 'high' },
            { label: '紧急', value: 'urgent' }
          ]" />
        </n-form-item>
        <n-form-item label="详细描述">
          <n-input v-model:value="ticketForm.description" type="textarea" placeholder="请详细描述您遇到的问题或需求..." :rows="5" />
        </n-form-item>
      </n-form>
      <div class="modal-actions">
        <n-button @click="showTicketModal = false">取消</n-button>
        <n-button type="primary" @click="submitTicket" :loading="ticketSubmitting">提交</n-button>
      </div>
    </n-modal>
  </div>
</template>

<style scoped>
/* === 租户控制台专用样式 === */
.tenant-metric-grid {
  grid-template-columns: repeat(4, minmax(0, 1fr));
}

/* === 快捷操作 === */
.quick-actions-row {
  display: grid;
  grid-template-columns: repeat(3, 1fr);
  gap: 16px;
  margin-bottom: 20px;
}

.quick-action-card {
  background: var(--color-surface);
  border: 1px solid var(--color-border);
  border-radius: var(--radius-lg);
  padding: 20px 24px;
  display: flex;
  align-items: center;
  gap: 16px;
  cursor: pointer;
  transition: all var(--transition-normal);
}

.quick-action-card:hover {
  border-color: var(--color-primary);
  box-shadow: var(--shadow-md);
  transform: translateY(-2px);
}

.quick-action-card:hover .qa-arrow {
  color: var(--color-primary);
  transform: translateX(4px);
}

.qa-icon {
  width: 52px;
  height: 52px;
  border-radius: var(--radius-lg);
  display: flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
}

.qa-text {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.qa-text strong {
  font-size: 15px;
  font-weight: 600;
  color: var(--color-text-primary);
}

.qa-text span {
  font-size: 12px;
  color: var(--color-text-tertiary);
}

.qa-arrow {
  font-size: 20px;
  color: var(--color-text-disabled);
  transition: all var(--transition-normal);
  flex-shrink: 0;
}

/* === 计费工具栏 === */
.billing-toolbar {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 12px 0;
  flex-wrap: wrap;
}

/* === 消费摘要卡片 === */
.consumption-summary {
  display: grid;
  grid-template-columns: repeat(3, 1fr);
  gap: 16px;
  margin: 16px 0;
}

.consumption-card {
  background: var(--color-surface-raised);
  border: 1px solid var(--color-border);
  border-radius: var(--radius-lg);
  padding: 20px;
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.consumption-card span {
  font-size: 12px;
  color: var(--color-text-tertiary);
}

.consumption-card strong {
  font-size: 24px;
  font-weight: 700;
  color: var(--color-text-primary);
  font-variant-numeric: tabular-nums;
}

/* === 余额预警设置 === */
.warning-setting {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 12px 16px;
  background: var(--color-surface-raised);
  border: 1px solid var(--color-border);
  border-radius: var(--radius-md);
  margin-top: 12px;
  font-size: 13px;
  color: var(--color-text-secondary);
}

/* === 响应式 === */
@media (max-width: 1100px) {
  .tenant-metric-grid {
    grid-template-columns: repeat(2, 1fr);
  }
  .quick-actions-row {
    grid-template-columns: 1fr;
  }
  .consumption-summary {
    grid-template-columns: 1fr;
  }
}

@media (max-width: 780px) {
  .tenant-metric-grid {
    grid-template-columns: 1fr;
  }
}
</style>
