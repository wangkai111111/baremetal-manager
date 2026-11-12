<script setup lang="ts">
// 优惠券运营台 — 运营模式营销中心
// 契约：GET/POST /coupons、PUT/DELETE /coupons/:id、POST /coupons/:code/redeem（需 orders:write）
// 有效期以 RFC3339 提交；type 与 code 创建后不可变（后端 PUT 不接受这两个字段）。
import { computed, ref, watch } from 'vue'
import {
  NButton,
  NDatePicker,
  NInput,
  NInputNumber,
  NModal,
  NPagination,
  NPopconfirm,
  NSelect,
  NTag,
  useMessage
} from 'naive-ui'
import type { SelectOption } from 'naive-ui'
import dayjs from 'dayjs'
import {
  couponApi,
  errorText,
  paged,
  unwrap,
  type CouponCreateInput,
  type CouponRecord,
  type CouponRedeemResult,
  type CouponStatus,
  type CouponType,
  type CouponUpdateInput
} from '../api'
import { useAuth } from '../stores/auth'

const message = useMessage()
const auth = useAuth()

// ============================================================
// 枚举
// ============================================================
const typeLabels: Record<CouponType, string> = { fixed: '代金券', percent: '折扣券' }
const statusMeta: Record<CouponStatus, { label: string; type: 'success' | 'warning' | 'default'; tone: string }> = {
  active: { label: '生效中', type: 'success', tone: 'var(--color-success)' },
  inactive: { label: '已停用', type: 'default', tone: 'var(--color-text-tertiary)' },
  // expired 由后端运行期（兑换时命中过期）写入，不可手工设置
  expired: { label: '已过期', type: 'warning', tone: 'var(--color-warning)' }
}
const statusOptions: SelectOption[] = [
  { label: '全部状态', value: '' },
  { label: '生效中', value: 'active' },
  { label: '已停用', value: 'inactive' },
  { label: '已过期', value: 'expired' }
]
const typeOptions: SelectOption[] = [
  { label: '全部类型', value: '' },
  { label: '代金券', value: 'fixed' },
  { label: '折扣券', value: 'percent' }
]
const typeChoices: { label: string; value: CouponType }[] = [
  { label: '代金券（直接减免金额）', value: 'fixed' },
  { label: '折扣券（按比例折扣）', value: 'percent' }
]
const statusChoices: { label: string; value: CouponStatus }[] = [
  { label: '生效中', value: 'active' },
  { label: '已停用', value: 'inactive' }
]

const canManage = computed(() => auth.can('marketing:write'))
const canRedeem = computed(() => auth.can('orders:write'))

// ============================================================
// 列表（status / type / search / 分页全部为后端参数）
// ============================================================
const items = ref<CouponRecord[]>([])
const total = ref(0)
const page = ref(1)
const pageSize = 12
const loading = ref(false)
const statusFilter = ref<CouponStatus | ''>('')
const typeFilter = ref<CouponType | ''>('')
const search = ref('')
const busyId = ref<number | null>(null)

async function load() {
  loading.value = true
  try {
    const data = paged<CouponRecord>(await couponApi.list({
      status: statusFilter.value,
      type: typeFilter.value,
      search: search.value.trim() || undefined,
      page: page.value,
      page_size: pageSize
    }))
    items.value = data.items
    total.value = data.total
  } catch (e) {
    message.error(errorText(e))
  } finally {
    loading.value = false
  }
}

watch(() => auth.projectId, () => { page.value = 1; load() }, { immediate: true })
watch([page, statusFilter, typeFilter], () => load())
let searchTimer: ReturnType<typeof setTimeout> | undefined
watch(search, () => {
  clearTimeout(searchTimer)
  searchTimer = setTimeout(() => { page.value = 1; load() }, 320)
})

// ============================================================
// 表单（新建 / 编辑复用）
// ============================================================
type CouponForm = {
  code: string
  name: string
  type: CouponType
  value: number | null
  min_amount: number | null
  max_discount: number | null
  from: number | null
  until: number | null
  usage_limit: number | null
  status: CouponStatus
}
const blankForm = (): CouponForm => ({
  code: '',
  name: '',
  type: 'fixed',
  value: null,
  min_amount: null,
  max_discount: null,
  from: null,
  until: null,
  usage_limit: null,
  status: 'active'
})

const editorVisible = ref(false)
const editingId = ref<number | null>(null)
const submitting = ref(false)
const form = ref<CouponForm>(blankForm())
const isEdit = computed(() => editingId.value != null)

function openCreate() {
  editingId.value = null
  form.value = blankForm()
  editorVisible.value = true
}

function openEdit(row: CouponRecord) {
  editingId.value = row.id
  form.value = {
    code: row.code,
    name: row.name,
    type: row.type,
    value: Number(row.value),
    min_amount: row.min_amount != null ? Number(row.min_amount) : null,
    max_discount: row.max_discount != null ? Number(row.max_discount) : null,
    from: row.valid_from ? dayjs(String(row.valid_from)).valueOf() : null,
    until: row.valid_until ? dayjs(String(row.valid_until)).valueOf() : null,
    usage_limit: row.usage_limit != null ? Number(row.usage_limit) : null,
    status: row.status === 'expired' ? 'inactive' : row.status
  }
  editorVisible.value = true
}

// 券面值展示：fixed 为金额，percent 为折扣
function faceText(row: CouponRecord) {
  return row.type === 'percent'
    ? `${trimNumber(Number(row.value))}%`
    : `¥${Number(row.value || 0).toFixed(2)}`
}
function trimNumber(value: number) {
  return Number.isInteger(value) ? String(value) : value.toFixed(2)
}
function money(value: unknown) {
  return `¥${Number(value || 0).toFixed(2)}`
}
function stamp(value?: string | null, template = 'YYYY-MM-DD HH:mm') {
  if (!value) return '—'
  const parsed = dayjs(String(value))
  return parsed.isValid() ? parsed.format(template) : '—'
}
// 后端要求 RFC3339：带本地时区偏移，清空时提交空串（PUT 会把字段置为 NULL）
function rfc(value: number | null) {
  if (value == null) return ''
  const parsed = dayjs(value)
  return parsed.isValid() ? parsed.format('YYYY-MM-DDTHH:mm:ssZ') : ''
}
function validityText(row: CouponRecord) {
  if (!row.valid_from && !row.valid_until) return '长期有效'
  return `${stamp(row.valid_from, 'YYYY-MM-DD')} ~ ${row.valid_until ? stamp(row.valid_until, 'YYYY-MM-DD') : '不限'}`
}
function usageText(row: CouponRecord) {
  const used = Number(row.used_count || 0)
  if (row.usage_limit == null) return `已领用 ${used} 次 · 不限总量`
  const limit = Number(row.usage_limit)
  return `已领用 ${used} / ${limit} 次`
}
function usagePercent(row: CouponRecord) {
  if (row.usage_limit == null) return 0
  const limit = Number(row.usage_limit)
  if (limit <= 0) return 100
  return Math.min(100, Math.round((Number(row.used_count || 0) / limit) * 100))
}

const formErrors = computed(() => {
  const value = Number(form.value.value ?? 0)
  const errors: string[] = []
  if (!isEdit.value && !/^[A-Za-z0-9_-]{4,64}$/.test(form.value.code.trim())) {
    errors.push('券码需为 4–64 位字母、数字、下划线或短横线')
  }
  if (!form.value.name.trim()) errors.push('请填写券名称')
  if (!(value > 0)) errors.push('优惠面值必须大于 0')
  if (form.value.type === 'percent' && value > 100) errors.push('百分比折扣不能超过 100')
  if (form.value.min_amount != null && Number(form.value.min_amount) < 0) errors.push('最低消费金额不能为负数')
  if (form.value.max_discount != null && Number(form.value.max_discount) < 0) errors.push('最高优惠金额不能为负数')
  if (form.value.from != null && form.value.until != null && form.value.until <= form.value.from) {
    errors.push('失效时间必须晚于生效时间')
  }
  if (form.value.usage_limit != null && Number(form.value.usage_limit) < 0) errors.push('发放上限不能为负数')
  return errors
})

async function submit() {
  if (formErrors.value.length) { message.warning(formErrors.value[0]); return }
  submitting.value = true
  try {
    if (isEdit.value && editingId.value != null) {
      // PUT /coupons/:id 不接受 code / type，仅提交可变字段
      const payload: CouponUpdateInput = {
        name: form.value.name.trim(),
        value: Number(form.value.value),
        min_amount: form.value.min_amount != null ? Number(form.value.min_amount) : undefined,
        max_discount: form.value.max_discount != null ? Number(form.value.max_discount) : undefined,
        valid_from: rfc(form.value.from),
        valid_until: rfc(form.value.until),
        status: form.value.status
      }
      if (form.value.usage_limit != null) payload.usage_limit = Number(form.value.usage_limit)
      await couponApi.update(editingId.value, payload)
      message.success('优惠券已更新')
    } else {
      const payload: CouponCreateInput = {
        code: form.value.code.trim().toUpperCase(),
        name: form.value.name.trim(),
        type: form.value.type,
        value: Number(form.value.value),
        min_amount: form.value.min_amount != null ? Number(form.value.min_amount) : undefined,
        max_discount: form.value.max_discount != null ? Number(form.value.max_discount) : undefined,
        valid_from: rfc(form.value.from) || undefined,
        valid_until: rfc(form.value.until) || undefined,
        usage_limit: form.value.usage_limit != null ? Number(form.value.usage_limit) : undefined
      }
      await couponApi.create(payload)
      message.success('优惠券已创建并生效')
    }
    editorVisible.value = false
    await load()
  } catch (e) {
    message.error(errorText(e))
  } finally {
    submitting.value = false
  }
}

async function remove(row: CouponRecord) {
  if (busyId.value != null) return
  busyId.value = row.id
  try {
    await couponApi.remove(row.id)
    message.success(`券码 ${row.code} 已删除`)
    await load()
  } catch (e) {
    message.error(errorText(e))
  } finally {
    busyId.value = null
  }
}

// ============================================================
// 兑换（POST /coupons/:code/redeem）
// ============================================================
const redeemCode = ref('')
const redeeming = ref(false)
const redeemed = ref<CouponRedeemResult | null>(null)
const redeemError = ref('')

async function redeem() {
  const code = redeemCode.value.trim().toUpperCase()
  if (!code) { message.warning('请输入券码'); return }
  redeeming.value = true
  redeemError.value = ''
  redeemed.value = null
  try {
    redeemed.value = unwrap<CouponRedeemResult>(await couponApi.redeem(code))
    message.success(`兑换成功：${redeemed.value.name}`)
    await load()
  } catch (e) {
    // 后端对「尚未生效 / 已过期 / 达到上限 / 不适用当前租户」分别返回 400 文案，原样透出
    redeemError.value = errorText(e)
    await load()
  } finally {
    redeeming.value = false
  }
}
</script>

<template>
  <div class="page coupon-page">
    <div class="page-head">
      <div>
        <div class="eyebrow dark">MARKETING / COUPON FOUNDRY</div>
        <h1>优惠券</h1>
        <p>配置代金券与折扣券，校验门槛与有效期，并验证券码可用性</p>
      </div>
      <div class="head-actions">
        <n-button quaternary :loading="loading" @click="load">刷新</n-button>
        <n-button type="primary" :disabled="!canManage" @click="openCreate">+ 新建优惠券</n-button>
      </div>
    </div>

    <!-- 券码兑换验证 -->
    <div class="redeem-bench">
      <div class="bench-copy">
        <strong>券码验券台</strong>
        <span>调用 POST /coupons/:code/redeem 计一次领用，用于下单前确认可用性</span>
      </div>
      <div class="bench-form">
        <n-input
          v-model:value="redeemCode"
          placeholder="输入券码，例如 NEWHAND2026"
          clearable
          :disabled="!canRedeem"
          style="width: 240px"
          @keyup.enter="redeem"
        />
        <n-button type="primary" :loading="redeeming" :disabled="!canRedeem" @click="redeem">兑换</n-button>
      </div>
      <p v-if="!canRedeem" class="bench-note">当前项目缺少 orders:write 权限，无法执行兑换。</p>
      <div v-if="redeemed" class="bench-result bench-result--ok">
        <span class="result-label">兑换成功</span>
        <strong>{{ redeemed.name }}</strong>
        <em>面值 {{ redeemed.type === 'percent' ? `${trimNumber(Number(redeemed.value))}%` : money(redeemed.value) }} · 门槛 {{ money(redeemed.min_amount) }}</em>
        <code>{{ redeemed.code }}</code>
        <span class="result-time">{{ stamp(redeemed.redeemed_at, 'YYYY-MM-DD HH:mm:ss') }}</span>
      </div>
      <div v-if="redeemError" class="bench-result bench-result--bad">
        <span class="result-label">兑换失败</span>
        <strong>{{ redeemError }}</strong>
        <em>券已过期时后端会将状态置为「已过期」，列表已同步刷新</em>
      </div>
    </div>

    <!-- 筛选 -->
    <div class="panel">
      <div class="panel-toolbar coupon-toolbar">
        <div class="panel-title">
          券库 <span class="count">{{ total }}</span>
        </div>
        <div class="toolbar-actions">
          <n-input v-model:value="search" size="small" placeholder="搜索券码或名称" clearable class="coupon-search" />
          <n-select v-model:value="statusFilter" :options="statusOptions" size="small" class="coupon-filter" />
          <n-select v-model:value="typeFilter" :options="typeOptions" size="small" class="coupon-filter" />
        </div>
      </div>

      <div v-if="loading" class="coupon-empty">正在读取券库…</div>
      <div v-else-if="!items.length" class="coupon-empty">
        {{ total ? '没有匹配当前筛选的优惠券' : '尚未配置优惠券，点击右上角「新建优惠券」发行第一张券' }}
      </div>
      <div v-else class="coupon-grid">
        <article
          v-for="row in items"
          :key="row.id"
          class="coupon-card"
          :class="[`coupon-card--${row.status}`, { 'coupon-card--percent': row.type === 'percent' }]"
        >
          <div class="coupon-main">
            <div class="coupon-topline">
              <span class="coupon-type">{{ typeLabels[row.type] ?? row.type }}</span>
              <n-tag size="tiny" :bordered="false" :type="statusMeta[row.status]?.type ?? 'default'">
                {{ statusMeta[row.status]?.label ?? row.status }}
              </n-tag>
            </div>
            <h3 class="coupon-name">{{ row.name }}</h3>
            <div class="coupon-face">
              <strong>{{ faceText(row) }}</strong>
              <span>{{ row.type === 'percent' ? '折扣' : '立减' }}</span>
            </div>
            <ul class="coupon-terms">
              <li>门槛：{{ row.min_amount ? `满 ${money(row.min_amount)}` : '无门槛' }}</li>
              <li>封顶：{{ row.max_discount != null ? money(row.max_discount) : (row.type === 'percent' ? '不封顶' : '—') }}</li>
              <li>有效期：{{ validityText(row) }}</li>
              <li>归属：{{ row.tenant_id == null ? '平台级（全租户可见）' : '本租户专属' }}</li>
            </ul>
          </div>

          <div class="coupon-notch" aria-hidden="true"><i v-for="n in 5" :key="n" /></div>

          <div class="coupon-stub">
            <code class="coupon-code">{{ row.code }}</code>
            <div class="coupon-usage">
              <div class="usage-bar" :style="{ width: `${usagePercent(row)}%` }" />
            </div>
            <span class="usage-text">{{ usageText(row) }}</span>
            <div class="stub-actions">
              <template v-if="canManage">
                <n-button text type="primary" size="small" @click="openEdit(row)">编辑</n-button>
                <n-popconfirm @positive-click="remove(row)">
                  <template #trigger>
                    <n-button text type="error" size="small" :disabled="busyId === row.id">删除</n-button>
                  </template>
                  删除后券码 {{ row.code }} 立即失效，确认删除？
                </n-popconfirm>
              </template>
              <span v-else class="stub-muted">只读</span>
            </div>
          </div>
        </article>
      </div>

      <div v-if="total > pageSize" class="coupon-pager">
        <n-pagination
          :page="page"
          :page-size="pageSize"
          :item-count="total"
          @update:page="(value: number) => (page = value)"
        />
      </div>
    </div>

    <!-- 新建 / 编辑 -->
    <n-modal
      v-model:show="editorVisible"
      preset="card"
      :title="isEdit ? `编辑优惠券 · ${form.code}` : '新建优惠券'"
      :style="{ width: '620px' }"
    >
      <div class="form-grid">
        <label class="form-item">
          <span>券码{{ isEdit ? '' : '（自动大写，创建后不可改）' }}</span>
          <n-input v-model:value="form.code" :disabled="isEdit" maxlength="64" placeholder="NEWHAND2026" />
        </label>
        <label class="form-item">
          <span>类型（创建后不可改）</span>
          <n-select v-model:value="form.type" :options="typeChoices" :disabled="isEdit" />
        </label>
        <label class="form-item form-item--wide">
          <span>券名称</span>
          <n-input v-model:value="form.name" maxlength="64" placeholder="新用户首单立减" />
        </label>
        <label class="form-item">
          <span>面值{{ form.type === 'percent' ? '（%，≤100）' : '（元）' }}</span>
          <n-input-number v-model:value="form.value" :min="0" :precision="form.type === 'percent' ? 2 : 2" class="full" />
        </label>
        <label class="form-item">
          <span>最低消费（元）</span>
          <n-input-number v-model:value="form.min_amount" :min="0" :precision="2" class="full" placeholder="留空表示无门槛" />
        </label>
        <label class="form-item">
          <span>最高优惠（元）</span>
          <n-input-number v-model:value="form.max_discount" :min="0" :precision="2" class="full" placeholder="折扣券建议设置封顶" />
        </label>
        <label class="form-item">
          <span>发放上限（次）</span>
          <n-input-number v-model:value="form.usage_limit" :min="0" :precision="0" class="full" placeholder="留空表示不限" />
        </label>
        <label class="form-item">
          <span>生效时间</span>
          <n-date-picker v-model:value="form.from" type="datetime" clearable class="full" />
        </label>
        <label class="form-item">
          <span>失效时间</span>
          <n-date-picker v-model:value="form.until" type="datetime" clearable class="full" />
        </label>
        <label v-if="isEdit" class="form-item">
          <span>状态</span>
          <n-select v-model:value="form.status" :options="statusChoices" />
        </label>
        <p class="form-item form-note">
          时间以 RFC3339 提交（含本地时区偏移），清空即置为不限；「已过期」由后端在兑换时自动写入，不能手工设置。
        </p>
        <ul v-if="formErrors.length" class="form-errors">
          <li v-for="item in formErrors" :key="item">{{ item }}</li>
        </ul>
      </div>
      <div class="modal-actions">
        <n-button quaternary @click="editorVisible = false">取消</n-button>
        <n-button type="primary" :loading="submitting" @click="submit">{{ isEdit ? '保存修改' : '发行优惠券' }}</n-button>
      </div>
    </n-modal>
  </div>
</template>

<style scoped>
.coupon-page {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

/* 验券台 */
.redeem-bench {
  position: relative;
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 14px;
  padding: 14px 18px;
  border: 1px solid var(--color-border);
  border-left: 3px solid var(--color-primary);
  border-radius: var(--radius-lg);
  background:
    linear-gradient(100deg, var(--color-primary-light) 0%, transparent 42%),
    var(--color-surface);
}
.bench-copy { display: flex; flex-direction: column; gap: 2px; margin-right: auto; }
.bench-copy strong { font-size: var(--font-size-base); color: var(--color-text-primary); }
.bench-copy span { font-size: var(--font-size-xs); color: var(--color-text-tertiary); }
.bench-form { display: flex; align-items: center; gap: 8px; }
.bench-note { width: 100%; margin: 0; font-size: var(--font-size-xs); color: var(--color-text-tertiary); }
.bench-result {
  flex-basis: 100%;
  display: flex;
  flex-wrap: wrap;
  align-items: baseline;
  gap: 10px;
  padding: 10px 14px;
  border-radius: var(--radius-md);
  font-size: var(--font-size-sm);
}
.bench-result--ok { background: var(--color-success-light); color: var(--color-text-primary); }
.bench-result--bad { background: var(--color-error-light); color: var(--color-text-primary); }
.result-label {
  font-size: 10px;
  letter-spacing: 0.18em;
  text-transform: uppercase;
  color: var(--color-text-tertiary);
}
.bench-result strong { font-weight: 600; }
.bench-result em { font-style: normal; color: var(--color-text-secondary); font-size: var(--font-size-xs); }
.bench-result code {
  padding: 1px 8px;
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: var(--font-size-xs);
  background: var(--color-surface-raised);
  border: 1px solid var(--color-border-light);
  border-radius: var(--radius-sm);
}
.result-time { margin-left: auto; font-size: var(--font-size-xs); color: var(--color-text-tertiary); font-variant-numeric: tabular-nums; }

/* 筛选栏 */
.coupon-toolbar { display: flex; align-items: center; justify-content: space-between; gap: 12px; flex-wrap: wrap; }
.coupon-search { width: 210px; }
.coupon-filter { width: 140px; }
.coupon-empty {
  padding: 42px 20px;
  text-align: center;
  font-size: var(--font-size-sm);
  color: var(--color-text-tertiary);
}

/* 券卡网格 */
.coupon-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(330px, 1fr));
  gap: 14px;
  padding: 4px 16px 16px;
}
.coupon-card {
  position: relative;
  display: grid;
  grid-template-columns: minmax(0, 1fr) 118px;
  background: var(--color-surface);
  border: 1px solid var(--color-border);
  border-radius: var(--radius-lg);
  overflow: hidden;
  transition: box-shadow var(--transition-fast), transform var(--transition-fast), border-color var(--transition-fast);
}
.coupon-card:hover {
  transform: translateY(-2px);
  box-shadow: var(--shadow-md);
  border-color: var(--color-primary-hover);
}
.coupon-card--expired,
.coupon-card--inactive { background: var(--color-surface-raised); }
.coupon-card--expired .coupon-face strong,
.coupon-card--inactive .coupon-face strong { color: var(--color-text-tertiary); }
.coupon-card::before {
  /* 左侧色条：按状态着色，替代整卡高饱和背景，暗色主题下同样成立 */
  content: '';
  position: absolute;
  inset: 0 auto 0 0;
  width: 3px;
  background: var(--color-primary);
}
.coupon-card--active::before { background: var(--color-success); }
.coupon-card--expired::before { background: var(--color-warning); }
.coupon-card--inactive::before { background: var(--color-border-dark); }

.coupon-main { padding: 14px 16px 16px 18px; display: flex; flex-direction: column; gap: 8px; }
.coupon-topline { display: flex; align-items: center; justify-content: space-between; gap: 10px; }
.coupon-type {
  font-size: 10px;
  letter-spacing: 0.16em;
  text-transform: uppercase;
  color: var(--color-text-tertiary);
}
.coupon-name {
  margin: 0;
  font-size: var(--font-size-base);
  font-weight: 600;
  line-height: 1.4;
  color: var(--color-text-primary);
}
.coupon-face { display: flex; align-items: baseline; gap: 8px; }
.coupon-face strong {
  font-size: 28px;
  line-height: 1.1;
  letter-spacing: -0.02em;
  font-variant-numeric: tabular-nums;
  color: var(--color-primary-active);
}
.coupon-card--percent .coupon-face strong { color: var(--color-primary); }
.coupon-face span { font-size: var(--font-size-xs); color: var(--color-text-tertiary); }
.coupon-terms {
  list-style: none;
  display: grid;
  gap: 3px;
  margin: 4px 0 0;
  padding: 0;
  font-size: var(--font-size-xs);
  color: var(--color-text-secondary);
}
.coupon-terms li { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }

/* 撕孔线 */
.coupon-notch {
  position: relative;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 7px;
  border-left: 1px dashed var(--color-border-dark);
  border-right: 1px dashed transparent;
  background: var(--color-surface-raised);
}
.coupon-notch i {
  width: 3px;
  height: 3px;
  border-radius: 50%;
  background: var(--color-border-dark);
}

.coupon-stub {
  padding: 14px 12px;
  display: flex;
  flex-direction: column;
  align-items: flex-end;
  gap: 8px;
  background: var(--color-surface-raised);
}
.coupon-code {
  writing-mode: vertical-rl;
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: var(--font-size-xs);
  letter-spacing: 0.14em;
  color: var(--color-text-secondary);
  max-height: 116px;
  overflow: hidden;
}
.coupon-usage {
  width: 100%;
  height: 4px;
  border-radius: var(--radius-full);
  background: var(--color-border-light);
  overflow: hidden;
}
.usage-bar {
  height: 100%;
  border-radius: var(--radius-full);
  background: var(--color-primary);
  transition: width var(--transition-normal);
}
.usage-text {
  font-size: 10px;
  color: var(--color-text-tertiary);
  text-align: right;
  font-variant-numeric: tabular-nums;
}
.stub-actions { margin-top: auto; display: flex; flex-direction: column; align-items: flex-end; gap: 4px; }
.stub-muted { font-size: var(--font-size-xs); color: var(--color-text-disabled); }

.coupon-pager { display: flex; justify-content: center; padding: 6px 16px 16px; }

/* 表单 */
.form-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 14px;
}
.form-item { display: flex; flex-direction: column; gap: 6px; }
.form-item > span { font-size: var(--font-size-sm); color: var(--color-text-secondary); }
.form-item--wide { grid-column: 1 / -1; }
.form-note {
  grid-column: 1 / -1;
  margin: 0;
  font-size: var(--font-size-xs);
  line-height: 1.7;
  color: var(--color-text-tertiary);
}
.form-errors {
  grid-column: 1 / -1;
  margin: 0;
  padding-left: 18px;
  font-size: var(--font-size-xs);
  color: var(--color-error);
  list-style: disc;
}
.full { width: 100%; }
</style>
