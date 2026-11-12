<script setup lang="ts">
// 订单管理 — 对外运营模式的订单全生命周期视图
// 后端契约：GET/POST /orders、GET /orders/:id、PUT /orders/:id/:transition、GET /orders/:id/export
import { computed, h, ref, watch } from 'vue'
import {
  NAlert, NButton, NDataTable, NDatePicker, NDescriptions, NDescriptionsItem,
  NEmpty, NInputNumber, NModal, NPopconfirm, NSelect, NSpace, NTag, useMessage
} from 'naive-ui'
import type { DataTableColumns } from 'naive-ui'
import dayjs from 'dayjs'
import {
  batchOps, errorText, orderApi, productApi, rows, unwrap,
  type OrderTransition, type RecordData
} from '../api'
import { useAuth } from '../stores/auth'

const message = useMessage()
const auth = useAuth()

// ============================================================
// 字典：订单类型与状态
// ============================================================
type TagType = 'success' | 'warning' | 'error' | 'info' | 'default'

const typeLabels: Record<string, string> = {
  purchase: '新购',
  renew: '续费',
  upgrade: '升配',
  refund: '退款'
}

const statusLabels: Record<string, string> = {
  pending: '待支付',
  paid: '已支付',
  processing: '处理中',
  completed: '已完成',
  refunded: '已退款',
  cancelled: '已取消'
}

const statusTagTypes: Record<string, TagType> = {
  pending: 'warning',
  paid: 'info',
  processing: 'info',
  completed: 'success',
  refunded: 'error',
  cancelled: 'default'
}

// 正向流转主线，用于状态机可视化；退款与取消属于旁路终态
const flowSteps = ['pending', 'paid', 'processing', 'completed'] as const

// 平台侧推进主线，与后端 orderTransitions 逐值对齐：
// paid → processing、processing → completed；paid 另可旁路 refunded
const adminAdvance: Record<string, OrderTransition | undefined> = {
  paid: 'processing',
  processing: 'completed'
}

// 筛选下拉的「全部」以空串表示：既能让 NSelect 正常回显中文标签，
// 又天然是 falsy，拼装查询参数时直接跳过，无需 value: null 这种需要
// naive-ui SelectIgnoredOption（带 type 字段）才能通过的选项形态。
const typeOptions = [
  { label: '全部类型', value: '' },
  ...Object.entries(typeLabels).map(([value, label]) => ({ label, value }))
]

const statusOptions = [
  { label: '全部状态', value: '' },
  ...Object.entries(statusLabels).map(([value, label]) => ({ label, value }))
]

// ============================================================
// 列表状态
// ============================================================
const orders = ref<RecordData[]>([])
const total = ref(0)
const page = ref(1)
const pageSize = 20
const loading = ref(false)

// 后端 listOrders 仅支持 status / type / product_id 过滤，没有租期参数，
// 因此日期区间只能在已取回的数据上二次筛选；启用日期区间时改为一次性拉取
// 较大窗口并在前端分页，避免「远程分页 + 本地过滤」出现总数与页码不一致。
const EXPANDED_PAGE_SIZE = 200
const filters = ref<{ type: string; status: string; range: [number, number] | null }>({
  type: '',
  status: '',
  range: null
})
const dateFiltered = computed(() => !!filters.value.range)

const tableRows = computed(() => {
  if (!dateFiltered.value) return orders.value
  const [start, end] = filters.value.range as [number, number]
  return orders.value.filter(row => {
    const created = String(row.created_at || '')
    if (!created) return false
    const time = dayjs(created).valueOf()
    return time >= start && time <= end
  })
})

const tableTotal = computed(() => (dateFiltered.value ? tableRows.value.length : total.value))

const pagination = computed(() => dateFiltered.value
  ? { pageSize, showSizePicker: false }
  : {
      page: page.value,
      pageSize,
      itemCount: total.value,
      showSizePicker: false,
      onUpdatePage: (next: number) => { page.value = next; load() }
    })

// ============================================================
// 状态机统计（用于顶部流转看板）
// ============================================================
const statusCounts = ref<Record<string, number>>({})
const countsLoading = ref(false)

// 逐个状态借用 listOrders 的 total 字段做计数：page_size=1 只回一行，
// 但 total 是该状态的准确总数，比在前端累加分页结果可靠。
async function loadStatusCounts() {
  countsLoading.value = true
  const keys = [...flowSteps, 'refunded', 'cancelled']
  const results = await Promise.allSettled(
    keys.map(status => orderApi.list('/orders', { status, page: 1, page_size: 1 }))
  )
  const next: Record<string, number> = {}
  keys.forEach((status, index) => {
    const result = results[index]
    next[status] = result.status === 'fulfilled' ? rows(result.value).total : 0
  })
  statusCounts.value = next
  countsLoading.value = false
}

// ============================================================
// 产品名映射（listOrders 只返回 product_id）
// ============================================================
const productNameMap = ref<Record<string, string>>({})

async function loadProductNames() {
  try {
    const data = rows(await productApi.list('/products', { page: 1, page_size: 200 }))
    productNameMap.value = Object.fromEntries(data.items.map(p => [String(p.id), String(p.name || '')]))
  } catch {
    // 无 products:read 权限时退化为展示产品 ID
    productNameMap.value = {}
  }
}

function productLabel(row: RecordData): string {
  const id = row.product_id
  if (id === null || id === undefined || id === '') return row.asset_id ? `资产 #${row.asset_id}` : '—'
  return productNameMap.value[String(id)] || `产品 #${id}`
}

// ============================================================
// 金额展示
// ============================================================
function money(value: unknown): string {
  return `¥${Number(value || 0).toFixed(2)}`
}

function timeText(value: unknown): string {
  return value ? dayjs(value as string).format('YYYY-MM-DD HH:mm') : '—'
}

// ============================================================
// 权限与流转能力判定
// ============================================================
const canWrite = computed(() => auth.can('orders:write'))
const isPlatformAdmin = computed(() => auth.user?.is_platform_admin === true)

// 后端 transitionOrder 在运营模式下限制非管理员只能取消待支付订单，
// 已支付订单必须由平台或余额支付流程推进，因此手动流转仅对平台管理员开放。
function canTransition(row: RecordData, target: string): boolean {
  if (!canWrite.value) return false
  if (target === 'cancelled') return String(row.status) === 'pending'
  return isPlatformAdmin.value
}

// 订阅订单（payable_cents 非空）必须走余额支付，禁止手动标记已支付
async function isSubscriptionOrder(row: RecordData): Promise<boolean> {
  try {
    const detail = unwrap<RecordData>(await orderApi.get('/orders', String(row.id)))
    return detail.payable_cents !== null && detail.payable_cents !== undefined
  } catch (e) {
    message.error(errorText(e))
    return false
  }
}

const paying = ref<string | number | null>(null)

async function payOrder(row: RecordData) {
  paying.value = row.id ?? null
  try {
    // 列表接口不返回 payable_cents，先取详情决定用哪条真实链路收款
    const subscription = await isSubscriptionOrder(row)
    if (subscription) {
      if (!auth.can('finance:write')) {
        message.warning('余额支付需要财务写权限，请联系平台处理')
        return
      }
      await batchOps.paySubscription(Number(row.id))
      message.success('余额支付成功，租期已生效')
    } else {
      await orderApi.transition(String(row.id), 'paid')
      message.success('订单已标记为已支付')
    }
    await refresh()
  } catch (e) {
    message.error(errorText(e))
  } finally {
    paying.value = null
  }
}

const cancelling = ref<string | number | null>(null)

async function cancelOrder(row: RecordData) {
  cancelling.value = row.id ?? null
  try {
    await orderApi.transition(String(row.id), 'cancelled')
    message.success('订单已取消')
    await refresh()
  } catch (e) {
    message.error(errorText(e))
  } finally {
    cancelling.value = null
  }
}

// ============================================================
// 交付：购买订单转已完成时，后端要求必须绑定资产
// ============================================================
const deliverVisible = ref(false)
const deliverSaving = ref(false)
const deliverTarget = ref<RecordData | null>(null)
const deliverAssetId = ref<number | null>(null)

function needsAssetBinding(row: RecordData): boolean {
  return String(row.type) === 'purchase' && (row.asset_id === null || row.asset_id === undefined || row.asset_id === '')
}

function advanceOrder(row: RecordData) {
  const next = adminAdvance[String(row.status)]
  if (!next) return
  if (next === 'completed' && needsAssetBinding(row)) {
    deliverTarget.value = row
    deliverAssetId.value = null
    deliverVisible.value = true
    return
  }
  transition(row, next)
}

async function confirmDelivery() {
  const row = deliverTarget.value
  if (!row) return
  if (!deliverAssetId.value || deliverAssetId.value <= 0) {
    message.warning('请填写有效的交付资产 ID')
    return
  }
  deliverSaving.value = true
  try {
    await orderApi.transition(row.id as number, 'completed', { asset_id: deliverAssetId.value })
    message.success('订单已交付完成，资产已绑定')
    deliverVisible.value = false
    await refresh()
  } catch (e) {
    message.error(errorText(e))
  } finally {
    deliverSaving.value = false
  }
}

// ============================================================
// 导出：GET /orders/:id/export 返回 JSON 附件
// ============================================================
const exporting = ref(false)

async function exportOrder(row: RecordData) {
  exporting.value = true
  try {
    const response = await orderApi.exportOrder(String(row.id))
    const blob = response.data instanceof Blob
      ? response.data
      : new Blob([JSON.stringify(response.data ?? {}, null, 2)], { type: 'application/json' })
    const url = URL.createObjectURL(blob)
    const link = document.createElement('a')
    link.href = url
    link.download = `order_${row.id}_${dayjs().format('YYYYMMDD')}.json`
    link.click()
    URL.revokeObjectURL(url)
    message.success('订单已导出')
  } catch (e) {
    message.error(errorText(e))
  } finally {
    exporting.value = false
  }
}

// ============================================================
// 详情弹窗
// ============================================================
const detailVisible = ref(false)
const detailLoading = ref(false)
const detail = ref<RecordData | null>(null)

async function openDetail(row: RecordData) {
  detail.value = { ...row }
  detailVisible.value = true
  detailLoading.value = true
  try {
    detail.value = unwrap<RecordData>(await orderApi.get('/orders', String(row.id)))
  } catch (e) {
    message.error(errorText(e))
  } finally {
    detailLoading.value = false
  }
}

// 详情内的状态机进度：主线走到第几步、是否旁路终态
const detailStepIndex = computed(() => {
  const status = String(detail.value?.status || '')
  const index = flowSteps.indexOf(status as typeof flowSteps[number])
  return index >= 0 ? index : flowSteps.length
})
const detailIsTerminal = computed(() => {
  const status = String(detail.value?.status || '')
  return status === 'refunded' || status === 'cancelled'
})

// ============================================================
// 表格列
// ============================================================
const columns = computed<DataTableColumns<RecordData>>(() => [
  { title: '订单号', key: 'id', minWidth: 90, render: (row: RecordData) => h('span', { class: 'order-no' }, `#${row.id}`) },
  {
    title: '类型', key: 'type', minWidth: 84,
    render: (row: RecordData) => h(NTag, { size: 'small', bordered: false, type: 'info' }, { default: () => typeLabels[String(row.type || '')] || String(row.type || '—') })
  },
  {
    title: '产品 / 资产', key: 'product_id', minWidth: 150, ellipsis: { tooltip: true },
    render: (row: RecordData) => productLabel(row)
  },
  {
    title: '原价', key: 'amount', minWidth: 104, align: 'right',
    render: (row: RecordData) => money(row.amount)
  },
  {
    title: '优惠', key: 'discount_amount', minWidth: 96, align: 'right',
    render: (row: RecordData) => {
      const value = Number(row.discount_amount || 0)
      return value > 0
        ? h('span', { class: 'discount-amount' }, `-${money(value)}`)
        : h('span', { class: 'muted' }, '—')
    }
  },
  {
    title: '实付', key: 'final_amount', minWidth: 110, align: 'right',
    render: (row: RecordData) => h('span', { class: 'final-amount' }, money(row.final_amount))
  },
  {
    title: '状态', key: 'status', minWidth: 100,
    render: (row: RecordData) => {
      const status = String(row.status || '')
      return h(NTag, { size: 'small', bordered: false, type: statusTagTypes[status] || 'default' }, { default: () => statusLabels[status] || status })
    }
  },
  {
    title: '创建时间', key: 'created_at', minWidth: 142,
    render: (row: RecordData) => timeText(row.created_at)
  },
  {
    title: '操作', key: 'actions', width: 240, fixed: 'right',
    render: (row: RecordData) => {
      const status = String(row.status || '')
      const actions = [
        h(NButton, { text: true, type: 'primary', onClick: () => openDetail(row) }, { default: () => '详情' })
      ]
      if (status === 'pending' && canWrite.value) {
        actions.push(h(NButton, {
          text: true, type: 'success',
          loading: paying.value === row.id,
          onClick: () => payOrder(row)
        }, { default: () => '支付' }))
      }
      if (status === 'pending' && canTransition(row, 'cancelled')) {
        actions.push(h(NPopconfirm, {
          onPositiveClick: () => cancelOrder(row)
        }, {
          trigger: () => h(NButton, { text: true, type: 'error', loading: cancelling.value === row.id }, { default: () => '取消' }),
          default: () => `确认取消订单 #${row.id}？取消后不可恢复。`
        }))
      }
      // 平台侧推进：已支付 → 处理中 → 已完成（购买单转已完成需绑定交付资产）
      if (isPlatformAdmin.value && adminAdvance[status]) {
        actions.push(h(NButton, {
          text: true, type: 'info', onClick: () => advanceOrder(row)
        }, { default: () => (status === 'paid' ? '转处理' : '交付完成') }))
      }
      // 平台侧旁路：已支付订单可整单退款（订阅单由后端 409 拦截）
      if (isPlatformAdmin.value && status === 'paid') {
        actions.push(h(NPopconfirm, {
          onPositiveClick: () => transition(row, 'refunded')
        }, {
          trigger: () => h(NButton, { text: true, type: 'warning' }, { default: () => '退款' }),
          default: () => `确认对订单 #${row.id} 发起退款？实付金额将退回租户账户余额。`
        }))
      }
      actions.push(h(NButton, { text: true, onClick: () => exportOrder(row) }, { default: () => '导出' }))
      return h('div', { class: 'table-actions' }, actions)
    }
  }
])

async function transition(row: RecordData, target: OrderTransition) {
  try {
    await orderApi.transition(String(row.id), target)
    message.success(`订单已流转为${statusLabels[target]}`)
    await refresh()
  } catch (e) {
    message.error(errorText(e))
  }
}

// ============================================================
// 数据加载
// ============================================================
async function load() {
  loading.value = true
  try {
    const params: Record<string, unknown> = {
      page: dateFiltered.value ? 1 : page.value,
      page_size: dateFiltered.value ? EXPANDED_PAGE_SIZE : pageSize
    }
    if (filters.value.type) params.type = filters.value.type
    if (filters.value.status) params.status = filters.value.status
    const data = rows(await orderApi.list('/orders', params))
    orders.value = data.items
    total.value = data.total
  } catch (e) {
    message.error(errorText(e))
  } finally {
    loading.value = false
  }
}

async function refresh() {
  await Promise.allSettled([load(), loadStatusCounts()])
}

function resetFilters() {
  filters.value = { type: '', status: '', range: null }
  page.value = 1
  load()
}

// 点击流转看板即按状态过滤，再次点击同一状态恢复全部
function toggleStatusFilter(status: string) {
  filters.value.status = filters.value.status === status ? '' : status
  page.value = 1
  load()
}

watch(() => auth.projectId, () => {
  page.value = 1
  filters.value = { type: '', status: '', range: null }
  refresh()
  loadProductNames()
}, { immediate: true })

watch([() => filters.value.type, () => filters.value.status], () => {
  page.value = 1
  load()
})

watch(() => filters.value.range, () => {
  page.value = 1
  load()
})
</script>

<template>
  <div class="page orders-view">
    <div class="page-head">
      <div>
        <div class="eyebrow dark">FINANCE / ORDERS</div>
        <h1>订单管理</h1>
        <p>跟踪新购、续费、升配与退款订单的全生命周期。</p>
      </div>
      <div class="head-actions">
        <n-button quaternary :loading="loading" @click="refresh">刷新</n-button>
        <n-button type="primary" @click="$router.push('/purchase')">新购下单</n-button>
      </div>
    </div>

    <!-- 状态流转看板：既是指标也是筛选器 -->
    <div class="flow-board">
      <template v-for="(step, index) in flowSteps" :key="step">
        <button
          type="button"
          class="flow-node"
          :class="{ 'flow-node--active': filters.status === step }"
          :style="{ '--flow-tone': step === 'pending' ? 'var(--color-warning)' : step === 'completed' ? 'var(--color-success)' : 'var(--color-primary)' }"
          @click="toggleStatusFilter(step)"
        >
          <span class="flow-index">{{ String(index + 1).padStart(2, '0') }}</span>
          <span class="flow-count">{{ statusCounts[step] ?? 0 }}</span>
          <span class="flow-label">{{ statusLabels[step] }}</span>
        </button>
        <span v-if="index < flowSteps.length - 1" class="flow-arrow">→</span>
      </template>
      <div class="flow-side">
        <div class="flow-side-item">
          <span>已退款</span>
          <strong>{{ statusCounts['refunded'] ?? 0 }}</strong>
        </div>
        <div class="flow-side-item">
          <span>已取消</span>
          <strong>{{ statusCounts['cancelled'] ?? 0 }}</strong>
        </div>
        <n-button size="tiny" quaternary :loading="countsLoading" @click="resetFilters">重置筛选</n-button>
      </div>
    </div>

    <n-alert v-if="!isPlatformAdmin" type="info" :bordered="false" class="orders-tip">
      租户视角：仅可支付与取消本人的待支付订单，支付完成后由平台完成交付与结算。
    </n-alert>

    <section class="panel">
      <div class="panel-toolbar">
        <div>
          <div class="panel-title">订单列表 <span class="count">{{ tableTotal }}</span></div>
          <p class="panel-caption">
            {{ dateFiltered ? '已按创建时间在前端筛选，最多检索最近 200 条记录' : '按类型、状态与创建时间检索订单' }}
          </p>
        </div>
        <div class="toolbar-actions">
          <n-select
            v-model:value="filters.type"
            :options="typeOptions"
            size="small"
            style="width: 128px"
            placeholder="订单类型"
          />
          <n-select
            v-model:value="filters.status"
            :options="statusOptions"
            size="small"
            style="width: 128px"
            placeholder="订单状态"
          />
          <n-date-picker
            v-model:value="filters.range"
            type="daterange"
            size="small"
            clearable
            style="width: 250px"
          />
        </div>
      </div>

      <n-data-table
        remote
        :columns="columns"
        :data="tableRows"
        :loading="loading"
        :row-key="(row: RecordData) => row.id ?? ''"
        :scroll-x="1240"
        :pagination="pagination"
        size="small"
      >
        <template #empty>
          <n-empty description="暂无订单数据" style="padding: 40px 0" />
        </template>
      </n-data-table>
    </section>

    <!-- 订单详情 -->
    <n-modal v-model:show="detailVisible" preset="card" title="订单详情" style="width: min(680px, 94vw)">
      <div v-if="detail" class="detail-flow">
        <template v-for="(step, index) in flowSteps" :key="step">
          <div
            class="detail-step"
            :class="{
              'detail-step--done': !detailIsTerminal && index <= detailStepIndex,
              'detail-step--current': String(detail.status) === step
            }"
          >
            <span class="detail-step-dot" />
            <span class="detail-step-label">{{ statusLabels[step] }}</span>
          </div>
          <span v-if="index < flowSteps.length - 1" class="detail-step-line" />
        </template>
        <div v-if="detailIsTerminal" class="detail-step detail-step--terminal">
          <span class="detail-step-dot" />
          <span class="detail-step-label">{{ statusLabels[String(detail.status)] }}</span>
        </div>
      </div>

      <n-descriptions :column="2" label-placement="top" bordered size="small" style="margin-top: 16px">
        <n-descriptions-item label="订单号">#{{ detail?.id }}</n-descriptions-item>
        <n-descriptions-item label="订单类型">{{ typeLabels[String(detail?.type || '')] || detail?.type || '—' }}</n-descriptions-item>
        <n-descriptions-item label="产品 / 资产">{{ detail ? productLabel(detail) : '—' }}</n-descriptions-item>
        <n-descriptions-item label="订单状态">
          <n-tag size="small" :bordered="false" :type="statusTagTypes[String(detail?.status || '')] || 'default'">
            {{ statusLabels[String(detail?.status || '')] || detail?.status || '—' }}
          </n-tag>
        </n-descriptions-item>
        <n-descriptions-item label="订单原价">{{ money(detail?.amount) }}</n-descriptions-item>
        <n-descriptions-item label="优惠金额">{{ money(detail?.discount_amount) }}</n-descriptions-item>
        <n-descriptions-item label="实付金额">{{ money(detail?.final_amount) }}</n-descriptions-item>
        <n-descriptions-item label="资产 ID">{{ detail?.asset_id ?? '—' }}</n-descriptions-item>
        <n-descriptions-item label="创建时间">{{ timeText(detail?.created_at) }}</n-descriptions-item>
        <n-descriptions-item label="支付时间">{{ timeText(detail?.paid_at) }}</n-descriptions-item>
        <n-descriptions-item label="完成时间">{{ timeText(detail?.completed_at) }}</n-descriptions-item>
        <n-descriptions-item label="更新时间">{{ timeText(detail?.updated_at) }}</n-descriptions-item>
      </n-descriptions>

      <div class="modal-actions">
        <n-space>
          <n-button
            v-if="String(detail?.status) === 'pending' && canWrite"
            type="primary"
            :loading="paying === detail?.id"
            @click="detail && payOrder(detail)"
          >支付订单</n-button>
          <n-button
            v-if="detail && canTransition(detail, 'cancelled')"
            type="error"
            ghost
            :loading="cancelling === detail?.id"
            @click="detail && cancelOrder(detail)"
          >取消订单</n-button>
          <n-button
            v-if="isPlatformAdmin && detail && adminAdvance[String(detail.status)]"
            type="info"
            @click="detail && advanceOrder(detail)"
          >{{ String(detail?.status) === 'paid' ? '转处理中' : '交付完成' }}</n-button>
          <n-button
            v-if="isPlatformAdmin && String(detail?.status) === 'paid'"
            type="warning"
            ghost
            @click="detail && transition(detail, 'refunded')"
          >整单退款</n-button>
          <n-button v-if="detail" :loading="exporting" @click="detail && exportOrder(detail)">导出订单</n-button>
        </n-space>
        <n-button @click="detailVisible = false">关闭</n-button>
      </div>
    </n-modal>

    <!-- 交付资产绑定：仅平台管理员 + 处理中购买订单 -->
    <n-modal v-model:show="deliverVisible" preset="card" title="绑定交付资产" style="width: min(460px, 94vw)">
      <p class="deliver-hint">
        订单 #{{ deliverTarget?.id }} 为购买类型且尚未关联资产，后端要求转“已完成”时必须绑定
        属于当前租户与项目的裸机资产，且该资产未被其他购买订单交付。
      </p>
      <n-input-number
        v-model:value="deliverAssetId"
        :min="1"
        :show-button="false"
        placeholder="资产 ID"
        style="width: 100%"
      />
      <div class="modal-actions">
        <n-button @click="deliverVisible = false">取消</n-button>
        <n-button type="primary" :loading="deliverSaving" @click="confirmDelivery">确认交付</n-button>
      </div>
    </n-modal>
  </div>
</template>

<style scoped>
/* === 状态流转看板 === */
.flow-board {
  display: flex;
  align-items: stretch;
  gap: 8px;
  flex-wrap: wrap;
  background: var(--color-surface);
  border: 1px solid var(--color-border);
  border-radius: var(--radius-lg);
  padding: 16px 20px;
  margin-bottom: 16px;
}

.flow-node {
  flex: 1;
  min-width: 128px;
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: 2px;
  padding: 12px 14px;
  background: var(--color-surface-raised);
  border: 1px solid var(--color-border);
  border-radius: var(--radius-md);
  cursor: pointer;
  text-align: left;
  transition: border-color var(--transition-fast), box-shadow var(--transition-fast), transform var(--transition-fast);
}

.flow-node:hover {
  border-color: var(--flow-tone);
  transform: translateY(-2px);
  box-shadow: var(--shadow-sm);
}

.flow-node--active {
  border-color: var(--flow-tone);
  box-shadow: inset 0 0 0 1px var(--flow-tone);
}

.flow-index {
  font-size: var(--font-size-xs);
  letter-spacing: 1px;
  color: var(--color-text-tertiary);
  font-variant-numeric: tabular-nums;
}

.flow-count {
  font-size: 26px;
  font-weight: 700;
  line-height: 1.15;
  color: var(--flow-tone);
  font-variant-numeric: tabular-nums;
}

.flow-label {
  font-size: var(--font-size-sm);
  color: var(--color-text-secondary);
}

.flow-arrow {
  align-self: center;
  color: var(--color-text-disabled);
  font-size: 18px;
}

.flow-side {
  margin-left: auto;
  display: flex;
  align-items: center;
  gap: 16px;
  padding-left: 16px;
  border-left: 1px solid var(--color-border-light);
}

.flow-side-item {
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.flow-side-item span {
  font-size: var(--font-size-xs);
  color: var(--color-text-tertiary);
}

.flow-side-item strong {
  font-size: var(--font-size-lg);
  color: var(--color-text-primary);
  font-variant-numeric: tabular-nums;
}

.orders-tip {
  margin-bottom: 16px;
}

.deliver-hint {
  margin: 0 0 14px;
  font-size: var(--font-size-sm);
  line-height: 1.6;
  color: var(--color-text-secondary);
}

/* === 表格 === */
.order-no {
  font-variant-numeric: tabular-nums;
  font-weight: 600;
  color: var(--color-text-primary);
}

.final-amount {
  font-weight: 600;
  font-variant-numeric: tabular-nums;
  color: var(--color-text-primary);
}

.discount-amount {
  font-variant-numeric: tabular-nums;
  color: var(--color-success);
}

.muted {
  color: var(--color-text-tertiary);
}

/* === 详情状态机 === */
.detail-flow {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 6px;
}

.detail-step {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 6px 10px;
  border-radius: var(--radius-md);
  background: var(--color-surface-raised);
  border: 1px solid var(--color-border);
  color: var(--color-text-tertiary);
  font-size: var(--font-size-sm);
}

.detail-step-dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: var(--color-border);
}

.detail-step--done {
  color: var(--color-primary);
  border-color: var(--color-primary);
  background: var(--color-primary-light);
}

.detail-step--done .detail-step-dot {
  background: var(--color-primary);
}

.detail-step--current {
  font-weight: 600;
}

.detail-step--terminal {
  color: var(--color-error);
  border-color: var(--color-error);
  background: var(--color-error-light);
}

.detail-step--terminal .detail-step-dot {
  background: var(--color-error);
}

.detail-step-line {
  flex: 1;
  min-width: 16px;
  height: 1px;
  background: var(--color-border);
}

@media (max-width: 900px) {
  .flow-board {
    padding: 12px;
  }
  .flow-side {
    margin-left: 0;
    padding-left: 0;
    border-left: 0;
    width: 100%;
    justify-content: flex-start;
  }
}
</style>
