<script setup lang="ts">
// 发票管理 — 运营模式财务中心
// 契约：GET /invoices?status=&page=&page_size=、POST /invoices、PUT /invoices/:id/:status
// 后端不存在 GET/PUT/DELETE /invoices/:id，因此本视图只做「列表 + 申请 + 状态流转」三件事。
import { computed, h, ref, watch } from 'vue'
import {
  NButton,
  NDataTable,
  NInput,
  NInputNumber,
  NModal,
  NPopconfirm,
  NSelect,
  NTag,
  useMessage
} from 'naive-ui'
import type { DataTableColumns, SelectOption } from 'naive-ui'
import dayjs from 'dayjs'
import {
  errorText,
  invoiceApi,
  paged,
  resource,
  type BillRecord,
  type InvoiceCreateInput,
  type InvoiceRecord,
  type InvoiceStatus,
  type InvoiceTransition,
  type InvoiceType
} from '../api'
import { useAuth } from '../stores/auth'

const message = useMessage()
const auth = useAuth()

// ============================================================
// 枚举与流转矩阵（与后端 validStatusTransitions 逐值一致）
// ============================================================
const statusMeta: Record<InvoiceStatus, { label: string; type: 'success' | 'warning' | 'default' }> = {
  requested: { label: '待开具', type: 'warning' },
  issued: { label: '已开具', type: 'success' },
  cancelled: { label: '已作废', type: 'default' }
}
const typeLabels: Record<InvoiceType, string> = { normal: '普通发票', special: '专用发票' }
const transitionLabels: Record<InvoiceTransition, string> = { issued: '确认开具', cancelled: '作废发票' }
const transitionMatrix: Record<InvoiceStatus, InvoiceTransition[]> = {
  requested: ['issued', 'cancelled'],
  issued: ['cancelled'],
  cancelled: []
}
const statusFilterLabel: Record<InvoiceStatus, string> = {
  requested: '待开具',
  issued: '已开具',
  cancelled: '已作废'
}
const typeOptions: SelectOption[] = [
  { label: '全部类型', value: '' },
  { label: '普通发票', value: 'normal' },
  { label: '专用发票', value: 'special' }
]

const canWrite = computed(() => auth.can('finance:write'))

// ============================================================
// 列表
// ============================================================
const items = ref<InvoiceRecord[]>([])
const total = ref(0)
const page = ref(1)
const pageSize = 20
const loading = ref(false)
const statusFilter = ref<InvoiceStatus | ''>('')
// 后端 GET /invoices 仅接受 status 与分页，type 筛选只能在当前页内收敛
const typeFilter = ref<InvoiceType | ''>('')
const selected = ref<InvoiceRecord | null>(null)
const busyId = ref<number | null>(null)

const visibleItems = computed(() =>
  typeFilter.value ? items.value.filter(row => row.type === typeFilter.value) : items.value
)
const pageCounts = computed(() => {
  const counts: Record<InvoiceStatus, number> = { requested: 0, issued: 0, cancelled: 0 }
  items.value.forEach(row => {
    if (row.status in counts) counts[row.status] += 1
  })
  return counts
})

async function load() {
  loading.value = true
  try {
    const response = await invoiceApi.list({
      status: statusFilter.value,
      page: page.value,
      page_size: pageSize
    })
    const data = paged<InvoiceRecord>(response)
    items.value = data.items
    total.value = data.total
    selected.value = selected.value ? data.items.find(row => row.id === selected.value?.id) ?? null : null
  } catch (e) {
    message.error(errorText(e))
  } finally {
    loading.value = false
  }
}

watch(() => auth.projectId, () => {
  page.value = 1
  selected.value = null
  loadBills()
  load()
}, { immediate: true })
watch(page, () => load())
watch(statusFilter, () => { page.value = 1; load() })

function select(row: InvoiceRecord) {
  selected.value = row.id === selected.value?.id ? null : row
}

function rowProps(row: InvoiceRecord) {
  return {
    class: ['invoice-row', { 'invoice-row--active': selected.value?.id === row.id }],
    onClick: () => select(row)
  }
}

// ============================================================
// 关联账单（用于「账单选择」与列表账单列，来源 GET /bills 首页数据）
// ============================================================
const bills = ref<BillRecord[]>([])
const billOptions = computed<SelectOption[]>(() => bills.value.map(bill => ({
  label: `#${bill.id} · ${dateText(bill.period_start)} ~ ${dateText(bill.period_end)} · ${moneyText(bill.total_amount)}（${billStatusLabel(bill.status)}）`,
  value: bill.id
})))

function billOf(id?: number | null) {
  if (id == null) return null
  return bills.value.find(bill => bill.id === Number(id)) ?? null
}

async function loadBills() {
  if (!auth.projectId) return
  try {
    bills.value = paged<BillRecord>(await resource.list('/bills', { page: 1, page_size: 100 })).items
  } catch {
    // 账单缺失不阻塞发票列表
    bills.value = []
  }
}

// ============================================================
// 申请开票
// ============================================================
const applyVisible = ref(false)
const submitting = ref(false)
const applyForm = ref<{
  bill_id: number | null
  type: InvoiceType
  title: string
  tax_number: string
  amount: number | null
}>({ bill_id: null, type: 'normal', title: '', tax_number: '', amount: null })

const taxNumberInvalid = computed(() => {
  const value = applyForm.value.tax_number.trim()
  if (!value) return false
  return !/^[A-Za-z0-9]{15,20}$/.test(value)
})

function openApply() {
  applyForm.value = { bill_id: null, type: 'normal', title: '', tax_number: '', amount: null }
  applyVisible.value = true
}

function onPickBill(value: number | null) {
  const bill = billOf(value)
  // 关联账单后默认按账单金额开票，仍可手工调整（后端要求 amount > 0）
  if (bill && applyForm.value.amount == null) applyForm.value.amount = Number(bill.total_amount)
}

async function submitApply() {
  const form = applyForm.value
  const payload: InvoiceCreateInput = {
    bill_id: form.bill_id ?? undefined,
    type: form.type,
    title: form.title.trim(),
    tax_number: form.tax_number.trim(),
    amount: Number(form.amount ?? 0)
  }
  if (!payload.title) { message.warning('请填写发票抬头'); return }
  // 后端 createInvoice 未强制校验税号，前端按业务规则拦截，避免开出发票无法抵扣
  if (!payload.tax_number) { message.warning('请填写纳税人识别号'); return }
  if (taxNumberInvalid.value) { message.warning('税号应为 15–20 位字母或数字'); return }
  if (!(payload.amount > 0)) { message.warning('开票金额必须大于 0（后端强制校验）'); return }
  submitting.value = true
  try {
    await invoiceApi.create(payload)
    message.success('开票申请已提交，等待平台开具')
    applyVisible.value = false
    await load()
  } catch (e) {
    message.error(errorText(e))
  } finally {
    submitting.value = false
  }
}

// ============================================================
// 状态流转
// ============================================================
async function transition(row: InvoiceRecord, target: InvoiceTransition) {
  if (busyId.value != null) return
  busyId.value = row.id
  try {
    await invoiceApi.transition(row.id, target)
    message.success(target === 'issued' ? `发票 #${row.id} 已开具` : `发票 #${row.id} 已作废`)
    await load()
  } catch (e) {
    // 非法流转由后端返回「不允许从 X 流转到 Y」，原样提示便于对账
    message.error(errorText(e))
  } finally {
    busyId.value = null
  }
}

// ============================================================
// 展示辅助
// ============================================================
function moneyText(value: unknown) {
  return `¥${Number(value || 0).toLocaleString('zh-CN', { minimumFractionDigits: 2, maximumFractionDigits: 2 })}`
}
function dateText(value?: string | null, template = 'YYYY-MM-DD') {
  if (!value) return '—'
  const parsed = dayjs(String(value))
  return parsed.isValid() ? parsed.format(template) : '—'
}
function billStatusLabel(value: string) {
  return value === 'paid' ? '已付款' : value === 'overdue' ? '逾期' : '未付款'
}
function stubNo(id: number) {
  return `INV-${String(id).padStart(6, '0')}`
}

const billPeriodText = computed(() => {
  const row = selected.value
  if (!row?.bill_id) return '未关联账单'
  const bill = billOf(row.bill_id)
  if (!bill) return `账单 #${row.bill_id}`
  return `#${bill.id} ${dateText(bill.period_start)} ~ ${dateText(bill.period_end)}`
})

const columns = computed<DataTableColumns<InvoiceRecord>>(() => [
  {
    title: '发票存根号',
    key: 'id',
    minWidth: 130,
    render: row => h('span', { class: 'stub-no' }, stubNo(row.id))
  },
  {
    title: '发票抬头',
    key: 'title',
    minWidth: 220,
    render: row => h('div', { class: 'title-cell' }, [
      h('strong', row.title),
      h('span', { class: 'title-tax' }, row.tax_number || '—')
    ])
  },
  {
    title: '种类',
    key: 'type',
    minWidth: 110,
    render: row => h('span', { class: row.type === 'special' ? 'kind kind--special' : 'kind' }, typeLabels[row.type] ?? String(row.type))
  },
  {
    title: '金额',
    key: 'amount',
    minWidth: 130,
    render: row => h('span', { class: 'money' }, moneyText(row.amount))
  },
  {
    title: '关联账单',
    key: 'bill_id',
    minWidth: 150,
    render: row => {
      const bill = billOf(row.bill_id)
      if (!bill) return row.bill_id ? `账单 #${row.bill_id}` : '—'
      return h('div', { class: 'bill-cell' }, [
        h('span', `#${bill.id} ${dateText(bill.period_start, 'MM-DD')}~${dateText(bill.period_end, 'MM-DD')}`),
        h('em', billStatusLabel(bill.status))
      ])
    }
  },
  {
    title: '状态',
    key: 'status',
    minWidth: 110,
    render: row => {
      const meta = statusMeta[row.status] ?? { label: String(row.status), type: 'default' as const }
      return h(NTag, { size: 'small', bordered: false, type: meta.type }, { default: () => meta.label })
    }
  },
  {
    title: '申请 / 开具时间',
    key: 'created_at',
    minWidth: 190,
    render: row => h('div', { class: 'time-cell' }, [
      h('span', dateText(row.created_at, 'YYYY-MM-DD HH:mm')),
      h('em', row.issued_at ? `开具于 ${dateText(row.issued_at, 'YYYY-MM-DD HH:mm')}` : '尚未开具')
    ])
  },
  {
    title: '操作',
    key: 'actions',
    width: 210,
    fixed: 'right',
    render: row => {
      const allowed = transitionMatrix[row.status] ?? []
      if (!canWrite.value || !allowed.length) {
        return h('span', { class: 'action-muted' }, row.status === 'cancelled' ? '已终结' : '只读')
      }
      return h('div', { class: 'table-actions' }, allowed.map(target =>
        target === 'cancelled'
          ? h(NPopconfirm, { onPositiveClick: () => transition(row, target) }, {
              trigger: () => h(NButton, { text: true, type: 'error', disabled: busyId.value != null }, { default: () => transitionLabels[target] }),
              default: () => `作废后不可恢复，确认作废发票 ${stubNo(row.id)}？`
            })
          : h(NButton, {
              text: true, type: 'primary',
              disabled: busyId.value != null, loading: busyId.value === row.id,
              onClick: () => transition(row, target)
            }, { default: () => transitionLabels[target] })
      ))
    }
  }
])
</script>

<template>
  <div class="page invoice-page">
    <div class="page-head">
      <div>
        <div class="eyebrow dark">FINANCE / INVOICE LEDGER</div>
        <h1>发票管理</h1>
        <p>按账单发起开票申请，跟踪平台开具与作废流转</p>
      </div>
      <div class="head-actions">
        <n-button quaternary :loading="loading" @click="load">刷新</n-button>
        <n-button type="primary" :disabled="!canWrite" @click="openApply">申请开票</n-button>
      </div>
    </div>

    <!-- 流转矩阵说明：与后端 validStatusTransitions 完全一致 -->
    <div class="flow-board">
      <div class="flow-node" :class="{ 'flow-node--on': statusFilter === 'requested' }" @click="statusFilter = 'requested'">
        <span class="flow-index">01</span>
        <strong>待开具</strong>
        <em>本页 {{ pageCounts.requested }} 张</em>
      </div>
      <span class="flow-arrow">→</span>
      <div class="flow-node" :class="{ 'flow-node--on': statusFilter === 'issued' }" @click="statusFilter = 'issued'">
        <span class="flow-index">02</span>
        <strong>已开具</strong>
        <em>本页 {{ pageCounts.issued }} 张</em>
      </div>
      <span class="flow-arrow">/</span>
      <div class="flow-node flow-node--void" :class="{ 'flow-node--on': statusFilter === 'cancelled' }" @click="statusFilter = 'cancelled'">
        <span class="flow-index">✕</span>
        <strong>已作废</strong>
        <em>本页 {{ pageCounts.cancelled }} 张</em>
      </div>
      <button class="flow-reset" type="button" @click="statusFilter = ''">全部 {{ total }}</button>
    </div>

    <div class="invoice-layout">
      <!-- 台账列表 -->
      <div class="panel">
        <div class="panel-toolbar invoice-toolbar">
          <div class="panel-title">
            发票台账 <span class="count">{{ total }}</span>
          </div>
          <div class="toolbar-actions">
            <span v-if="statusFilter" class="active-filter">
              状态：{{ statusFilterLabel[statusFilter] }}
              <button type="button" aria-label="清除状态筛选" @click="statusFilter = ''">✕</button>
            </span>
            <n-select v-model:value="typeFilter" :options="typeOptions" class="invoice-filter" size="small" />
            <span class="toolbar-hint">类型筛选仅作用于当前页（后端未提供 type 参数）</span>
          </div>
        </div>
        <n-data-table
          remote
          size="small"
          :columns="columns"
          :data="visibleItems"
          :loading="loading"
          :row-key="(row: InvoiceRecord) => row.id"
          :row-props="rowProps"
          :scroll-x="1240"
          :pagination="{
            page,
            pageSize,
            itemCount: total,
            showSizePicker: false,
            onUpdatePage: (value: number) => (page = value)
          }"
        />
        <div v-if="!loading && !visibleItems.length" class="empty-hint">
          {{ total ? '当前页没有匹配该类型的发票' : '尚无发票记录，点击「申请开票」发起第一张' }}
        </div>
      </div>

      <!-- 票样存根 -->
      <aside class="ticket-rail">
        <div v-if="selected" class="invoice-ticket">
          <div class="ticket-head">
            <span class="ticket-eyebrow">ELECTRONIC INVOICE</span>
            <span class="ticket-no">{{ stubNo(selected.id) }}</span>
          </div>
          <div class="ticket-amount">
            <span class="ticket-amount-label">开票金额</span>
            <strong>{{ moneyText(selected.amount) }}</strong>
            <span class="ticket-kind">{{ typeLabels[selected.type] ?? selected.type }}</span>
          </div>
          <div class="ticket-perforation"><i /><i /><i /><i /><i /><i /></div>
          <dl class="ticket-body">
            <div><dt>购买方抬头</dt><dd>{{ selected.title }}</dd></div>
            <div><dt>纳税人识别号</dt><dd class="mono">{{ selected.tax_number || '—' }}</dd></div>
            <div><dt>关联账单</dt><dd>{{ billPeriodText }}</dd></div>
            <div><dt>申请时间</dt><dd>{{ dateText(selected.created_at, 'YYYY-MM-DD HH:mm') }}</dd></div>
            <div><dt>开具时间</dt><dd>{{ selected.issued_at ? dateText(selected.issued_at, 'YYYY-MM-DD HH:mm') : '尚未开具' }}</dd></div>
          </dl>
          <div class="ticket-foot">
            <n-tag size="small" :bordered="false" :type="statusMeta[selected.status]?.type ?? 'default'">
              {{ statusMeta[selected.status]?.label ?? selected.status }}
            </n-tag>
            <div v-if="canWrite && (transitionMatrix[selected.status] ?? []).length" class="ticket-actions">
              <template v-for="target in transitionMatrix[selected.status] ?? []" :key="target">
                <n-popconfirm v-if="target === 'cancelled'" @positive-click="transition(selected, target)">
                  <template #trigger>
                    <n-button size="small" type="error" ghost :disabled="busyId != null">作废</n-button>
                  </template>
                  作废后不可恢复，确认作废 {{ stubNo(selected.id) }}？
                </n-popconfirm>
                <n-button
                  v-else
                  size="small"
                  type="primary"
                  :loading="busyId === selected.id"
                  :disabled="busyId != null"
                  @click="transition(selected, target)"
                >
                  确认开具
                </n-button>
              </template>
            </div>
          </div>
          <p class="ticket-note">发票由平台财务开具并交付，此处状态与后端流转记录一致。</p>
        </div>
        <div v-else class="invoice-ticket invoice-ticket--empty">
          <div class="ticket-head">
            <span class="ticket-eyebrow">STUB PREVIEW</span>
          </div>
          <p class="ticket-placeholder">选择台账中的一张发票<br>在此查看票样与可用流转</p>
        </div>
      </aside>
    </div>

    <!-- 申请开票 -->
    <n-modal v-model:show="applyVisible" preset="card" title="申请开票" class="invoice-modal" :style="{ width: '560px' }">
      <div class="form-grid">
        <label class="form-item">
          <span>关联账单</span>
          <n-select
            v-model:value="applyForm.bill_id"
            :options="billOptions"
            placeholder="可选，选择后自动带入账单金额"
            clearable
            filterable
            @update:value="onPickBill"
          />
        </label>
        <label class="form-item">
          <span>发票种类</span>
          <n-select v-model:value="applyForm.type" :options="typeOptions.slice(1)" />
        </label>
        <label class="form-item form-item--wide">
          <span>发票抬头<em>必填</em></span>
          <n-input v-model:value="applyForm.title" placeholder="单位全称或个人姓名" maxlength="100" />
        </label>
        <label class="form-item form-item--wide">
          <span>纳税人识别号<em>必填</em></span>
          <n-input v-model:value="applyForm.tax_number" placeholder="15–20 位统一社会信用代码或身份证号" maxlength="20" />
          <small v-if="taxNumberInvalid" class="form-error">格式不正确：仅允许 15–20 位字母与数字</small>
        </label>
        <label class="form-item">
          <span>开票金额<em>&gt; 0</em></span>
          <n-input-number v-model:value="applyForm.amount" :min="0" :precision="2" class="full" placeholder="人民币元" />
        </label>
        <p class="form-item form-note">
          后端仅校验抬头与金额（&gt; 0）；专用发票还需在开票完成后由平台交付纸质凭证。
        </p>
      </div>
      <div class="modal-actions">
        <n-button quaternary @click="applyVisible = false">取消</n-button>
        <n-button type="primary" :loading="submitting" @click="submitApply">提交申请</n-button>
      </div>
    </n-modal>
  </div>
</template>

<style scoped>
.invoice-page {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

/* 流转说明条：沿用订单页编号节点语汇 */
.flow-board {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 14px 16px;
  background: var(--color-surface);
  border: 1px solid var(--color-border);
  border-radius: var(--radius-lg);
  overflow-x: auto;
}
.flow-node {
  display: flex;
  flex-direction: column;
  gap: 2px;
  min-width: 132px;
  padding: 8px 14px;
  border: 1px solid var(--color-border-light);
  border-radius: var(--radius-md);
  background: var(--color-surface-raised);
  cursor: pointer;
  transition: border-color var(--transition-fast), transform var(--transition-fast), box-shadow var(--transition-fast);
}
.flow-node:hover {
  transform: translateY(-1px);
  border-color: var(--color-primary-hover);
  box-shadow: var(--shadow-sm);
}
.flow-node--on {
  border-color: var(--color-primary);
  background: var(--color-primary-light);
}
.flow-node--void { min-width: 120px; }
.flow-index {
  font-size: 11px;
  letter-spacing: 0.14em;
  color: var(--color-text-tertiary);
  font-variant-numeric: tabular-nums;
}
.flow-node strong { font-size: var(--font-size-base); color: var(--color-text-primary); }
.flow-node em {
  font-style: normal;
  font-size: var(--font-size-xs);
  color: var(--color-text-secondary);
  font-variant-numeric: tabular-nums;
}
.flow-arrow { color: var(--color-text-disabled); font-size: 15px; }
.flow-reset {
  margin-left: auto;
  padding: 6px 14px;
  font-size: var(--font-size-sm);
  color: var(--color-text-secondary);
  background: transparent;
  border: 1px dashed var(--color-border-dark);
  border-radius: var(--radius-full);
  cursor: pointer;
  white-space: nowrap;
  transition: color var(--transition-fast), border-color var(--transition-fast);
}
.flow-reset:hover { color: var(--color-primary); border-color: var(--color-primary); }

.invoice-layout {
  display: grid;
  grid-template-columns: minmax(0, 1fr) 336px;
  gap: 16px;
  align-items: start;
}
@media (max-width: 1280px) {
  .invoice-layout { grid-template-columns: minmax(0, 1fr); }
}

.invoice-toolbar { display: flex; align-items: center; justify-content: space-between; gap: 12px; flex-wrap: wrap; }
.invoice-filter { width: 148px; }
.active-filter {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: 2px 6px 2px 10px;
  font-size: var(--font-size-xs);
  color: var(--color-primary);
  background: var(--color-primary-light);
  border-radius: var(--radius-full);
}
.active-filter button {
  border: 0;
  background: transparent;
  color: inherit;
  cursor: pointer;
  font-size: 11px;
  line-height: 1;
  padding: 2px;
}
.toolbar-hint { font-size: var(--font-size-xs); color: var(--color-text-tertiary); }
:deep(.invoice-row) { cursor: pointer; }
:deep(.invoice-row--active td) { background: var(--color-primary-light) !important; }
.stub-no {
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: var(--font-size-sm);
  letter-spacing: 0.04em;
  color: var(--color-text-secondary);
}
:deep(.title-cell) { display: flex; flex-direction: column; gap: 2px; }
:deep(.title-cell strong) { font-weight: 600; color: var(--color-text-primary); }
:deep(.title-tax) { font-size: var(--font-size-xs); color: var(--color-text-tertiary); letter-spacing: 0.02em; }
:deep(.money) { font-weight: 600; font-variant-numeric: tabular-nums; }
:deep(.kind) { font-size: var(--font-size-sm); color: var(--color-text-secondary); }
:deep(.kind--special) { color: var(--color-primary); font-weight: 600; }
:deep(.bill-cell) { display: flex; flex-direction: column; gap: 1px; }
:deep(.bill-cell em) { font-style: normal; font-size: var(--font-size-xs); color: var(--color-text-tertiary); }
:deep(.time-cell) { display: flex; flex-direction: column; gap: 1px; font-variant-numeric: tabular-nums; }
:deep(.time-cell em) { font-style: normal; font-size: var(--font-size-xs); color: var(--color-text-tertiary); }
:deep(.action-muted) { font-size: var(--font-size-xs); color: var(--color-text-disabled); }

/* 票样存根 */
.ticket-rail { position: sticky; top: 16px; }
.invoice-ticket {
  position: relative;
  padding: 18px 20px 20px;
  background: var(--color-surface);
  border: 1px solid var(--color-border);
  border-radius: var(--radius-lg);
  box-shadow: var(--shadow-sm);
  overflow: hidden;
}
.invoice-ticket::after {
  /* 票面底纹：细斜纹，只在票样出现，避免整站同质化 */
  content: '';
  position: absolute;
  inset: 0;
  pointer-events: none;
  opacity: 0.5;
  background-image: repeating-linear-gradient(115deg, transparent 0 22px, rgba(22, 93, 255, 0.035) 22px 23px);
}
.ticket-head { display: flex; align-items: baseline; justify-content: space-between; gap: 12px; }
.ticket-eyebrow {
  font-size: 10px;
  letter-spacing: 0.22em;
  color: var(--color-text-tertiary);
}
.ticket-no {
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: var(--font-size-sm);
  font-weight: 600;
  color: var(--color-primary);
}
.ticket-amount {
  display: flex;
  flex-direction: column;
  gap: 2px;
  margin-top: 14px;
  padding-bottom: 16px;
}
.ticket-amount-label { font-size: var(--font-size-xs); color: var(--color-text-tertiary); }
.ticket-amount strong {
  font-size: var(--font-size-3xl);
  line-height: 1.15;
  font-weight: 700;
  letter-spacing: -0.01em;
  font-variant-numeric: tabular-nums;
  color: var(--color-text-primary);
}
.ticket-kind { font-size: var(--font-size-sm); color: var(--color-text-secondary); }
.ticket-perforation {
  position: relative;
  display: flex;
  justify-content: space-between;
  padding: 0 4px;
  border-top: 1px dashed var(--color-border-dark);
}
.ticket-perforation i {
  width: 9px;
  height: 9px;
  margin-top: -5px;
  border-radius: 50%;
  background: var(--color-bg);
  border: 1px solid var(--color-border-light);
}
.ticket-body {
  display: grid;
  gap: 10px;
  margin: 16px 0 0;
}
.ticket-body div {
  display: grid;
  grid-template-columns: 96px minmax(0, 1fr);
  gap: 8px;
  align-items: baseline;
}
.ticket-body dt { font-size: var(--font-size-xs); color: var(--color-text-tertiary); }
.ticket-body dd {
  margin: 0;
  font-size: var(--font-size-sm);
  color: var(--color-text-primary);
  word-break: break-all;
}
.ticket-body .mono { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; letter-spacing: 0.03em; }
.ticket-foot {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  margin-top: 18px;
  padding-top: 14px;
  border-top: 1px solid var(--color-border-light);
}
.ticket-actions { display: flex; gap: 8px; }
.ticket-note {
  margin: 12px 0 0;
  font-size: var(--font-size-xs);
  line-height: 1.6;
  color: var(--color-text-tertiary);
}
.invoice-ticket--empty { display: flex; flex-direction: column; min-height: 220px; }
.ticket-placeholder {
  flex: 1;
  display: grid;
  place-content: center;
  margin: 0;
  text-align: center;
  font-size: var(--font-size-sm);
  line-height: 1.9;
  color: var(--color-text-disabled);
}

/* 申请表单 */
.form-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 14px;
}
.form-item { display: flex; flex-direction: column; gap: 6px; }
.form-item > span { font-size: var(--font-size-sm); color: var(--color-text-secondary); }
.form-item > span em {
  margin-left: 6px;
  font-style: normal;
  font-size: var(--font-size-xs);
  color: var(--color-error);
}
.form-item--wide { grid-column: 1 / -1; }
.form-note {
  grid-column: 1 / -1;
  margin: 0;
  font-size: var(--font-size-xs);
  line-height: 1.7;
  color: var(--color-text-tertiary);
}
.form-error { font-size: var(--font-size-xs); color: var(--color-error); }
.full { width: 100%; }
</style>
