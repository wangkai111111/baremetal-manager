<script setup lang="ts">
// 计费管理页面 — 运营模式财务中心
import { computed, ref, watch, h } from 'vue'
import {
  NButton,
  NDataTable,
  NDatePicker,
  NModal,
  NForm,
  NFormItem,
  NInputNumber,
  NSelect,
  NTabPane,
  NTabs,
  NTag,
  useMessage
} from 'naive-ui'
import type { DataTableColumns } from 'naive-ui'
import dayjs from 'dayjs'
import * as XLSX from 'xlsx'
import { cloneDeep } from 'lodash-es'
import {
  accountApi,
  billApi,
  resource,
  rows,
  unwrap,
  errorText,
  type RecordData,
  type RechargeMethod
} from '../api'
import { useAuth } from '../stores/auth'

const message = useMessage()
const auth = useAuth()

// ============================================================
// 账户概览数据
// ============================================================
const account = ref<RecordData>({})
const accountLoading = ref(false)

async function loadAccount() {
  accountLoading.value = true
  try {
    const res = await accountApi.get()
    account.value = unwrap(res)
  } catch (e) {
    // 账户可能不存在，使用默认值
    account.value = { balance: 0, frozen_amount: 0, credit_limit: 0 }
  } finally {
    accountLoading.value = false
  }
}

// ============================================================
// 充值弹窗
// ============================================================
const rechargeVisible = ref(false)
const rechargeForm = ref<{ amount: number; method: RechargeMethod }>({ amount: 0, method: 'transfer' })
const rechargeLoading = ref(false)
// 后端 POST /accounts/recharge 仅接受 alipay|wechat|transfer（balance 为余额支付，不属于充值渠道）
const methodOptions: { label: string; value: RechargeMethod }[] = [
  { label: '银行转账', value: 'transfer' },
  { label: '支付宝', value: 'alipay' },
  { label: '微信支付', value: 'wechat' }
]

function openRecharge() {
  rechargeForm.value = { amount: 0, method: 'transfer' }
  rechargeVisible.value = true
}

async function doRecharge() {
  if (rechargeForm.value.amount <= 0) {
    message.warning('请输入有效的充值金额')
    return
  }
  rechargeLoading.value = true
  try {
    await accountApi.recharge(rechargeForm.value)
    message.success('充值成功')
    rechargeVisible.value = false
    loadAccount()
    loadPayments()
  } catch (e) {
    message.error(errorText(e))
  } finally {
    rechargeLoading.value = false
  }
}

// ============================================================
// 账单列表
// ============================================================
const bills = ref<RecordData[]>([])
const billsTotal = ref(0)
const billsPage = ref(1)
const billsPageSize = 20
const billsLoading = ref(false)
const billDateRange = ref<[number, number] | null>(null)
const billStatusFilter = ref<string | null>(null)
const billStatusOptions = [
  { label: '全部', value: null },
  { label: '未付款', value: 'unpaid' },
  { label: '已付款', value: 'paid' },
  { label: '逾期', value: 'overdue' }
] as any

const billColumns = computed<DataTableColumns<RecordData>>(() => [
  { title: '账单ID', key: 'id', minWidth: 80 },
  {
    title: '账期',
    key: 'period',
    minWidth: 200,
    render: (row: RecordData) => {
      const start = row.period_start ? dayjs(row.period_start as string).format('YYYY-MM-DD') : '—'
      const end = row.period_end ? dayjs(row.period_end as string).format('YYYY-MM-DD') : '—'
      return `${start} ~ ${end}`
    }
  },
  {
    title: '金额（元）',
    key: 'total_amount',
    minWidth: 120,
    render: (row: RecordData) => h('span', { style: 'font-weight:600;font-variant-numeric:tabular-nums' }, `¥${Number(row.total_amount || 0).toFixed(2)}`)
  },
  {
    title: '状态',
    key: 'status',
    minWidth: 100,
    render: (row: RecordData) => {
      const statusMap: Record<string, { label: string; type: 'success' | 'warning' | 'error' | 'info' | 'default' }> = {
        unpaid: { label: '未付款', type: 'warning' },
        paid: { label: '已付款', type: 'success' },
        overdue: { label: '逾期', type: 'error' }
      }
      const s = statusMap[String(row.status || '')] || { label: String(row.status || '—'), type: 'default' as const }
      return h(NTag, { size: 'small', bordered: false, type: s.type }, { default: () => s.label })
    }
  },
  {
    title: '到期日',
    key: 'due_date',
    minWidth: 120,
    render: (row: RecordData) => row.due_date ? dayjs(row.due_date as string).format('YYYY-MM-DD') : '—'
  },
  {
    title: '创建时间',
    key: 'created_at',
    minWidth: 160,
    render: (row: RecordData) => row.created_at ? dayjs(row.created_at as string).format('YYYY-MM-DD HH:mm') : '—'
  },
  {
    title: '操作',
    key: 'actions',
    width: 150,
    fixed: 'right' as const,
    render: (row: RecordData) => h('div', { class: 'table-actions' }, [
      h(NButton, { text: true, type: 'primary', onClick: () => viewBill(row) }, { default: () => '详情' }),
      h(NButton, { text: true, onClick: () => exportBill(row) }, { default: () => '导出' })
    ])
  }
])

async function loadBills() {
  billsLoading.value = true
  try {
    const params: Record<string, unknown> = { page: billsPage.value, page_size: billsPageSize }
    if (billStatusFilter.value) params.status = billStatusFilter.value
    const res = await resource.list('/bills', params)
    const data = rows(res)
    bills.value = data.items
    billsTotal.value = data.total
  } catch (e) {
    message.error(errorText(e))
  } finally {
    billsLoading.value = false
  }
}

function viewBill(row: RecordData) {
  // TODO: 实现账单详情弹窗或跳转
  message.info(`查看账单 #${row.id} 详情`)
}

async function exportBill(row: RecordData) {
  try {
    // GET /bills/:id/export 返回 JSON 附件（Content-Disposition），直接使用后端 Blob，避免二次序列化丢失内容
    const res = await billApi.exportBill(String(row.id))
    const payload = res.data instanceof Blob
      ? res.data
      : new Blob([JSON.stringify(res.data ?? {}, null, 2)], { type: 'application/json' })
    const url = URL.createObjectURL(payload)
    const a = document.createElement('a')
    a.href = url
    a.download = `bill_${row.id}_${dayjs().format('YYYYMMDD')}.json`
    a.click()
    URL.revokeObjectURL(url)
    message.success('账单已导出')
  } catch (e) {
    message.error(errorText(e))
  }
}

function exportBillsToExcel() {
  const exportData = cloneDeep(bills.value).map(row => ({
    '账单ID': row.id,
    '账期开始': row.period_start ? dayjs(row.period_start as string).format('YYYY-MM-DD') : '',
    '账期结束': row.period_end ? dayjs(row.period_end as string).format('YYYY-MM-DD') : '',
    '金额': Number(row.total_amount || 0).toFixed(2),
    '状态': row.status === 'unpaid' ? '未付款' : row.status === 'paid' ? '已付款' : row.status === 'overdue' ? '逾期' : String(row.status || ''),
    '到期日': row.due_date ? dayjs(row.due_date as string).format('YYYY-MM-DD') : '',
    '创建时间': row.created_at ? dayjs(row.created_at as string).format('YYYY-MM-DD HH:mm:ss') : ''
  }))
  const workbook = XLSX.utils.book_new()
  XLSX.utils.book_append_sheet(workbook, XLSX.utils.json_to_sheet(exportData), '账单列表')
  XLSX.writeFile(workbook, `账单列表_${dayjs().format('YYYYMMDD_HHmmss')}.xlsx`)
  message.success('已导出到 Excel')
}

// ============================================================
// 充值记录（支付流水）
// ============================================================
const payments = ref<RecordData[]>([])
const paymentsTotal = ref(0)
const paymentsPage = ref(1)
const paymentsPageSize = 20
const paymentsLoading = ref(false)

const paymentColumns = computed<DataTableColumns<RecordData>>(() => [
  { title: '流水号', key: 'id', minWidth: 80 },
  {
    title: '充值金额（元）',
    key: 'amount',
    minWidth: 130,
    render: (row: RecordData) => h('span', { style: 'font-weight:600;color:var(--color-success);font-variant-numeric:tabular-nums' }, `+¥${Number(row.amount || 0).toFixed(2)}`)
  },
  {
    title: '支付方式',
    key: 'method',
    minWidth: 100,
    render: (row: RecordData) => {
      const map: Record<string, string> = { alipay: '支付宝', wechat: '微信支付', transfer: '银行转账', balance: '余额支付' }
      return map[String(row.method || '')] || String(row.method || '—')
    }
  },
  {
    title: '状态',
    key: 'status',
    minWidth: 90,
    render: (row: RecordData) => {
      const statusMap: Record<string, { label: string; type: 'success' | 'warning' | 'error' | 'info' | 'default' }> = {
        success: { label: '成功', type: 'success' },
        pending: { label: '处理中', type: 'warning' },
        failed: { label: '失败', type: 'error' }
      }
      const s = statusMap[String(row.status || '')] || { label: String(row.status || '—'), type: 'default' as const }
      return h(NTag, { size: 'small', bordered: false, type: s.type }, { default: () => s.label })
    }
  },
  {
    title: '交易时间',
    key: 'paid_at',
    minWidth: 160,
    render: (row: RecordData) => row.paid_at ? dayjs(row.paid_at as string).format('YYYY-MM-DD HH:mm:ss') : '—'
  },
  {
    title: '创建时间',
    key: 'created_at',
    minWidth: 160,
    render: (row: RecordData) => row.created_at ? dayjs(row.created_at as string).format('YYYY-MM-DD HH:mm:ss') : '—'
  }
])

async function loadPayments() {
  paymentsLoading.value = true
  try {
    const res = await resource.list('/payments', { page: paymentsPage.value, page_size: paymentsPageSize })
    const data = rows(res)
    payments.value = data.items
    paymentsTotal.value = data.total
  } catch (e) {
    // 支付记录可能暂无数据
    payments.value = []
    paymentsTotal.value = 0
  } finally {
    paymentsLoading.value = false
  }
}

// ============================================================
// 消费明细（按量计费 + 订单汇总）
// ============================================================
const consumptionItems = ref<RecordData[]>([])
const consumptionTotal = ref(0)
const consumptionPage = ref(1)
const consumptionPageSize = 20
const consumptionLoading = ref(false)
const consumptionDateRange = ref<[number, number] | null>(null)

// TODO: 后端消费明细接口待完善，当前使用订单数据模拟
const consumptionColumns = computed<DataTableColumns<RecordData>>(() => [
  { title: '订单ID', key: 'id', minWidth: 80 },
  {
    title: '类型',
    key: 'type',
    minWidth: 90,
    render: (row: RecordData) => {
      const map: Record<string, string> = { purchase: '购买', renew: '续费', upgrade: '升级', refund: '退款' }
      return map[String(row.type || '')] || String(row.type || '—')
    }
  },
  {
    title: '金额（元）',
    key: 'final_amount',
    minWidth: 120,
    render: (row: RecordData) => h('span', { style: 'font-weight:600;font-variant-numeric:tabular-nums' }, `¥${Number(row.final_amount || 0).toFixed(2)}`)
  },
  {
    title: '状态',
    key: 'status',
    minWidth: 100,
    render: (row: RecordData) => {
      const statusMap: Record<string, { label: string; type: 'success' | 'warning' | 'error' | 'info' | 'default' }> = {
        pending: { label: '待支付', type: 'warning' },
        paid: { label: '已支付', type: 'success' },
        processing: { label: '处理中', type: 'info' },
        completed: { label: '已完成', type: 'success' },
        cancelled: { label: '已取消', type: 'default' },
        refunded: { label: '已退款', type: 'error' }
      }
      const s = statusMap[String(row.status || '')] || { label: String(row.status || '—'), type: 'default' as const }
      return h(NTag, { size: 'small', bordered: false, type: s.type }, { default: () => s.label })
    }
  },
  {
    title: '支付时间',
    key: 'paid_at',
    minWidth: 160,
    render: (row: RecordData) => row.paid_at ? dayjs(row.paid_at as string).format('YYYY-MM-DD HH:mm:ss') : '—'
  },
  {
    title: '创建时间',
    key: 'created_at',
    minWidth: 160,
    render: (row: RecordData) => row.created_at ? dayjs(row.created_at as string).format('YYYY-MM-DD HH:mm:ss') : '—'
  }
])

async function loadConsumption() {
  consumptionLoading.value = true
  try {
    const params: Record<string, unknown> = { page: consumptionPage.value, page_size: consumptionPageSize }
    if (consumptionDateRange.value) {
      params.start_date = dayjs(consumptionDateRange.value[0]).format('YYYY-MM-DD')
      params.end_date = dayjs(consumptionDateRange.value[1]).format('YYYY-MM-DD')
    }
    const res = await resource.list('/orders', params)
    const data = rows(res)
    let items = data.items
    // 客户端日期过滤（当后端尚未支持 start_date/end_date 参数时生效）
    if (consumptionDateRange.value) {
      const [start, end] = consumptionDateRange.value
      const startStr = dayjs(start).startOf('day').format('YYYY-MM-DD HH:mm:ss')
      const endStr = dayjs(end).endOf('day').format('YYYY-MM-DD HH:mm:ss')
      items = items.filter((row: RecordData) => {
        const date = row.created_at ? dayjs(row.created_at as string).format('YYYY-MM-DD HH:mm:ss') : ''
        return date >= startStr && date <= endStr
      })
    }
    consumptionItems.value = items
    consumptionTotal.value = consumptionDateRange.value ? items.length : data.total
  } catch (e) {
    consumptionItems.value = []
    consumptionTotal.value = 0
  } finally {
    consumptionLoading.value = false
  }
}

function exportConsumptionToExcel() {
  const exportData = cloneDeep(consumptionItems.value).map(row => ({
    '订单ID': row.id,
    '类型': row.type === 'purchase' ? '购买' : row.type === 'renew' ? '续费' : row.type === 'upgrade' ? '升级' : row.type === 'refund' ? '退款' : String(row.type || ''),
    '金额': Number(row.final_amount || 0).toFixed(2),
    '状态': row.status,
    '支付时间': row.paid_at ? dayjs(row.paid_at as string).format('YYYY-MM-DD HH:mm:ss') : '',
    '创建时间': row.created_at ? dayjs(row.created_at as string).format('YYYY-MM-DD HH:mm:ss') : ''
  }))
  const workbook = XLSX.utils.book_new()
  XLSX.utils.book_append_sheet(workbook, XLSX.utils.json_to_sheet(exportData), '消费明细')
  XLSX.writeFile(workbook, `消费明细_${dayjs().format('YYYYMMDD_HHmmss')}.xlsx`)
  message.success('已导出到 Excel')
}

// ============================================================
// 计费模型信息
// ============================================================
const billingModels = [
  {
    title: '按量计费（秒级计量）',
    desc: '按实际使用时长计费，精确到秒级计量，适合短期、弹性使用场景',
    features: ['秒级精度计量', '按小时单价 × 实际使用时长', '随时启停，灵活可控', '适合测试、临时扩容等场景'],
    type: 'pay-as-you-go'
  },
  {
    title: '预付费（包年包月）',
    desc: '预先支付固定周期费用，享受折扣优惠，适合长期稳定使用场景',
    features: ['支持小时/天/月/年计费周期', '预付享折扣优惠', '自动续费可选', '适合生产环境长期运行'],
    type: 'subscription'
  }
]

// ============================================================
// Tab 与初始化
// ============================================================
const activeTab = ref('overview')

function handleTabChange(val: string) {
  activeTab.value = val
  if (val === 'bills' && !bills.value.length) loadBills()
  if (val === 'payments' && !payments.value.length) loadPayments()
  if (val === 'consumption' && !consumptionItems.value.length) loadConsumption()
}

// 格式化金额显示
function formatMoney(val: unknown): string {
  const num = Number(val || 0)
  return `¥${num.toLocaleString('zh-CN', { minimumFractionDigits: 2, maximumFractionDigits: 2 })}`
}

// 初始化加载
watch(
  () => auth.projectId,
  () => {
    loadAccount()
    loadBills()
    loadPayments()
    loadConsumption()
  },
  { immediate: true }
)
</script>

<template>
  <div class="page">
    <!-- 页面标题 -->
    <div class="page-head">
      <div>
        <div class="eyebrow dark">FINANCE / BILLING CENTER</div>
        <h1>计费中心</h1>
        <p>管理账户余额、查看账单与消费明细</p>
      </div>
      <div class="head-actions">
        <n-button type="primary" @click="openRecharge">充值</n-button>
      </div>
    </div>

    <!-- 账户概览卡片 -->
    <div class="billing-overview-cards">
      <div class="billing-card billing-card--balance">
        <div class="billing-card-label">账户余额</div>
        <div class="billing-card-value">{{ formatMoney(account.balance) }}</div>
        <div class="billing-card-foot">当前可用余额</div>
      </div>
      <div class="billing-card billing-card--frozen">
        <div class="billing-card-label">冻结金额</div>
        <div class="billing-card-value">{{ formatMoney(account.frozen_amount) }}</div>
        <div class="billing-card-foot">待结算冻结</div>
      </div>
      <div class="billing-card billing-card--credit">
        <div class="billing-card-label">信用额度</div>
        <div class="billing-card-value">{{ formatMoney(account.credit_limit) }}</div>
        <div class="billing-card-foot">可用信用额度</div>
      </div>
    </div>

    <!-- 主内容 Tabs -->
    <div class="panel">
      <n-tabs type="line" :value="activeTab" @update:value="handleTabChange" style="padding: 0 20px;">
        <!-- 概览 Tab -->
        <n-tab-pane name="overview" tab="概览">
          <div class="overview-section">
            <h3 class="section-title">计费模型</h3>
            <p class="section-desc">系统支持以下两种计费模式，可根据业务需求灵活选择</p>
            <div class="billing-models-grid">
              <div v-for="model in billingModels" :key="model.type" class="billing-model-card">
                <div class="model-header">
                  <span class="model-badge" :class="model.type === 'pay-as-you-go' ? 'model-badge--usage' : 'model-badge--sub'">
                    {{ model.type === 'pay-as-you-go' ? '按量' : '预付' }}
                  </span>
                  <strong>{{ model.title }}</strong>
                </div>
                <p class="model-desc">{{ model.desc }}</p>
                <ul class="model-features">
                  <li v-for="f in model.features" :key="f">{{ f }}</li>
                </ul>
              </div>
            </div>
          </div>
          <div class="overview-section">
            <h3 class="section-title">近期账单</h3>
            <n-data-table
              :columns="billColumns"
              :data="bills.slice(0, 5)"
              :loading="billsLoading"
              :row-key="(row: RecordData) => row.id ?? ''"
              :scroll-x="900"
              size="small"
            />
            <div v-if="!billsLoading && !bills.length" class="empty-hint">暂无账单数据</div>
          </div>
        </n-tab-pane>

        <!-- 账单管理 Tab -->
        <n-tab-pane name="bills" tab="账单管理">
          <div class="tab-toolbar">
            <div class="tab-toolbar-left">
              <n-select
                v-model:value="billStatusFilter"
                :options="billStatusOptions"
                style="width: 140px"
                size="small"
                @update:value="() => { billsPage = 1; loadBills() }"
              />
              <n-date-picker
                v-model:value="billDateRange"
                type="daterange"
                size="small"
                clearable
                @update:value="() => { billsPage = 1; loadBills() }"
              />
            </div>
            <div class="tab-toolbar-right">
              <n-button size="small" quaternary @click="loadBills">刷新</n-button>
              <n-button size="small" @click="exportBillsToExcel">导出 Excel</n-button>
            </div>
          </div>
          <n-data-table
            remote
            :columns="billColumns"
            :data="bills"
            :loading="billsLoading"
            :row-key="(row: RecordData) => row.id ?? ''"
            :scroll-x="900"
            size="small"
            :pagination="{
              page: billsPage,
              pageSize: billsPageSize,
              itemCount: billsTotal,
              showSizePicker: false,
              onUpdatePage: (p: number) => { billsPage = p; loadBills() }
            }"
          />
          <div v-if="!billsLoading && !bills.length" class="empty-hint">暂无账单数据</div>
        </n-tab-pane>

        <!-- 充值记录 Tab -->
        <n-tab-pane name="payments" tab="充值记录">
          <div class="tab-toolbar">
            <div class="tab-toolbar-left">
              <span class="toolbar-label">充值记录列表</span>
            </div>
            <div class="tab-toolbar-right">
              <n-button size="small" quaternary @click="loadPayments">刷新</n-button>
            </div>
          </div>
          <n-data-table
            remote
            :columns="paymentColumns"
            :data="payments"
            :loading="paymentsLoading"
            :row-key="(row: RecordData) => row.id ?? ''"
            :scroll-x="800"
            size="small"
            :pagination="{
              page: paymentsPage,
              pageSize: paymentsPageSize,
              itemCount: paymentsTotal,
              showSizePicker: false,
              onUpdatePage: (p: number) => { paymentsPage = p; loadPayments() }
            }"
          />
          <div v-if="!paymentsLoading && !payments.length" class="empty-hint">暂无充值记录</div>
        </n-tab-pane>

        <!-- 消费明细 Tab -->
        <n-tab-pane name="consumption" tab="消费明细">
          <div class="tab-toolbar">
            <div class="tab-toolbar-left">
              <n-date-picker
                v-model:value="consumptionDateRange"
                type="daterange"
                size="small"
                clearable
                @update:value="() => { consumptionPage = 1; loadConsumption() }"
              />
            </div>
            <div class="tab-toolbar-right">
              <n-button size="small" quaternary @click="loadConsumption">刷新</n-button>
              <n-button size="small" @click="exportConsumptionToExcel">导出 Excel</n-button>
            </div>
          </div>
          <n-data-table
            remote
            :columns="consumptionColumns"
            :data="consumptionItems"
            :loading="consumptionLoading"
            :row-key="(row: RecordData) => row.id ?? ''"
            :scroll-x="800"
            size="small"
            :pagination="{
              page: consumptionPage,
              pageSize: consumptionPageSize,
              itemCount: consumptionTotal,
              showSizePicker: false,
              onUpdatePage: (p: number) => { consumptionPage = p; loadConsumption() }
            }"
          />
          <div v-if="!consumptionLoading && !consumptionItems.length" class="empty-hint">暂无消费记录</div>
        </n-tab-pane>
      </n-tabs>
    </div>

    <!-- 充值弹窗 -->
    <n-modal v-model:show="rechargeVisible" preset="card" title="账户充值" style="width: min(420px, 94vw)">
      <n-form label-placement="top">
        <n-form-item label="充值金额（元）" required>
          <n-input-number v-model:value="rechargeForm.amount" :min="0.01" :precision="2" style="width: 100%" placeholder="请输入充值金额" />
        </n-form-item>
        <n-form-item label="支付方式">
          <n-select v-model:value="rechargeForm.method" :options="methodOptions" />
        </n-form-item>
        <div class="modal-actions">
          <n-button @click="rechargeVisible = false">取消</n-button>
          <n-button type="primary" :loading="rechargeLoading" @click="doRecharge">确认充值</n-button>
        </div>
      </n-form>
    </n-modal>
  </div>
</template>

<style scoped>
/* ============================================================
   计费中心 — 账户概览卡片
   ============================================================ */
.billing-overview-cards {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 16px;
  margin-bottom: 20px;
}

.billing-card {
  background: var(--color-surface);
  border: 1px solid var(--color-border);
  border-radius: var(--radius-lg);
  padding: 24px 28px;
  position: relative;
  overflow: hidden;
  transition: box-shadow var(--transition-fast), border-color var(--transition-fast);
}

.billing-card:hover {
  box-shadow: var(--shadow-md);
}

.billing-card::before {
  content: '';
  position: absolute;
  top: 0;
  left: 0;
  right: 0;
  height: 3px;
}

.billing-card--balance::before {
  background: var(--color-primary);
}

.billing-card--frozen::before {
  background: var(--color-warning);
}

.billing-card--credit::before {
  background: var(--color-success);
}

.billing-card-label {
  font-size: var(--font-size-sm);
  color: var(--color-text-tertiary);
  margin-bottom: 8px;
  font-weight: 500;
}

.billing-card-value {
  font-size: 32px;
  font-weight: 700;
  color: var(--color-text-primary);
  line-height: 1.2;
  font-variant-numeric: tabular-nums;
  letter-spacing: -0.5px;
}

.billing-card--balance .billing-card-value {
  color: var(--color-primary);
}

.billing-card--frozen .billing-card-value {
  color: var(--color-warning);
}

.billing-card--credit .billing-card-value {
  color: var(--color-success);
}

.billing-card-foot {
  font-size: var(--font-size-xs);
  color: var(--color-text-tertiary);
  margin-top: 8px;
}

/* ============================================================
   Tab 工具栏
   ============================================================ */
.tab-toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 16px 0 12px;
  flex-wrap: wrap;
}

.tab-toolbar-left {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
}

.tab-toolbar-right {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-left: auto;
}

.toolbar-label {
  font-size: var(--font-size-sm);
  color: var(--color-text-secondary);
  font-weight: 500;
}

/* ============================================================
   概览区域
   ============================================================ */
.overview-section {
  padding: 20px 0;
}

.overview-section + .overview-section {
  border-top: 1px solid var(--color-border-light);
}

.section-title {
  font-size: var(--font-size-lg);
  font-weight: 600;
  color: var(--color-text-primary);
  margin: 0 0 6px;
}

.section-desc {
  font-size: var(--font-size-sm);
  color: var(--color-text-tertiary);
  margin: 0 0 16px;
}

/* ============================================================
   计费模型卡片
   ============================================================ */
.billing-models-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 16px;
}

.billing-model-card {
  background: var(--color-surface-raised);
  border: 1px solid var(--color-border);
  border-radius: var(--radius-lg);
  padding: 20px 24px;
  transition: border-color var(--transition-fast), box-shadow var(--transition-fast);
}

.billing-model-card:hover {
  border-color: var(--color-primary);
  box-shadow: var(--shadow-sm);
}

.model-header {
  display: flex;
  align-items: center;
  gap: 10px;
  margin-bottom: 10px;
}

.model-header strong {
  font-size: var(--font-size-base);
  font-weight: 600;
  color: var(--color-text-primary);
}

.model-badge {
  font-size: 10px;
  font-weight: 600;
  letter-spacing: 0.5px;
  padding: 2px 8px;
  border-radius: var(--radius-sm);
  text-transform: uppercase;
}

.model-badge--usage {
  background: var(--color-primary-light);
  color: var(--color-primary);
}

.model-badge--sub {
  background: var(--color-success-light);
  color: var(--color-success);
}

.model-desc {
  font-size: var(--font-size-sm);
  color: var(--color-text-secondary);
  line-height: 1.6;
  margin: 0 0 12px;
}

.model-features {
  list-style: none;
  padding: 0;
  margin: 0;
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.model-features li {
  font-size: var(--font-size-xs);
  color: var(--color-text-secondary);
  padding-left: 16px;
  position: relative;
}

.model-features li::before {
  content: '✓';
  position: absolute;
  left: 0;
  color: var(--color-success);
  font-weight: 700;
  font-size: 11px;
}

/* ============================================================
   响应式
   ============================================================ */
@media (max-width: 780px) {
  .billing-overview-cards {
    grid-template-columns: 1fr;
  }

  .billing-models-grid {
    grid-template-columns: 1fr;
  }

  .billing-card-value {
    font-size: 26px;
  }
}

@media (max-width: 520px) {
  .tab-toolbar {
    flex-direction: column;
    align-items: stretch;
  }

  .tab-toolbar-right {
    margin-left: 0;
    justify-content: flex-end;
  }
}
</style>
