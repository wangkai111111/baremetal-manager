<script setup lang="ts">
// 工单工作台 — 自用 / 运营两种模式通用（后端 /work-orders 无模式门禁）
// 契约：GET/POST /work-orders、GET/PUT /work-orders/:id、PUT /work-orders/:id/:transition
// 指派（assigned_to 写入）与回复依赖后端尚未提供的端点，属任务 9，本视图仅只读展示处理人。
import { computed, h, ref, watch } from 'vue'
import {
  NButton,
  NDataTable,
  NDrawer,
  NDrawerContent,
  NInput,
  NModal,
  NSelect,
  NTag,
  useMessage
} from 'naive-ui'
import type { DataTableColumns, SelectOption } from 'naive-ui'
import dayjs from 'dayjs'
import {
  errorText,
  paged,
  unwrap,
  workOrderApi,
  type WorkOrderCreateInput,
  type WorkOrderPriority,
  type WorkOrderRecord,
  type WorkOrderStatus,
  type WorkOrderTransition,
  type WorkOrderType
} from '../api'
import { useAuth } from '../stores/auth'

const message = useMessage()
const auth = useAuth()

// ============================================================
// 枚举与流转矩阵（与后端 workOrderTransitions 逐值一致）
// ============================================================
const typeLabels: Record<WorkOrderType, string> = {
  fault: '故障报修',
  technical: '技术支持',
  consultation: '使用咨询',
  other: '其他'
}
const statusMeta: Record<WorkOrderStatus, { label: string; type: 'warning' | 'info' | 'success' | 'default'; rank: number }> = {
  open: { label: '待受理', type: 'warning', rank: 0 },
  in_progress: { label: '处理中', type: 'info', rank: 1 },
  resolved: { label: '已解决', type: 'success', rank: 2 },
  closed: { label: '已关闭', type: 'default', rank: 3 }
}
const priorityMeta: Record<WorkOrderPriority, { label: string; level: number; tone: string }> = {
  urgent: { label: '紧急', level: 4, tone: 'var(--color-error)' },
  high: { label: '高', level: 3, tone: 'var(--color-warning)' },
  medium: { label: '中', level: 2, tone: 'var(--color-primary)' },
  low: { label: '低', level: 1, tone: 'var(--color-info)' }
}
const transitionLabels: Record<WorkOrderTransition, string> = {
  in_progress: '开始处理',
  resolved: '标记已解决',
  closed: '关闭工单'
}
const transitionMatrix: Record<WorkOrderStatus, WorkOrderTransition[]> = {
  open: ['in_progress', 'closed'],
  in_progress: ['resolved', 'closed'],
  resolved: ['closed', 'in_progress'],
  closed: []
}

const statusOptions: SelectOption[] = [
  { label: '全部状态', value: '' },
  { label: '待受理', value: 'open' },
  { label: '处理中', value: 'in_progress' },
  { label: '已解决', value: 'resolved' },
  { label: '已关闭', value: 'closed' }
]
const typeFilterOptions: SelectOption[] = [
  { label: '全部类型', value: '' },
  ...Object.entries(typeLabels).map(([value, label]) => ({ label, value }))
]
const priorityOptions: SelectOption[] = [
  { label: '全部优先级', value: '' },
  ...Object.entries(priorityMeta).map(([value, meta]) => ({ label: meta.label, value }))
]
const typeChoices = Object.entries(typeLabels).map(([value, label]) => ({ label, value: value as WorkOrderType }))
const priorityChoices = Object.entries(priorityMeta).map(([value, meta]) => ({ label: meta.label, value: value as WorkOrderPriority }))

// 后端 canAll = 平台管理员 或 work_orders:manage（客服）
const isStaff = computed(() => Boolean(auth.user?.is_platform_admin) || auth.can('work_orders:manage'))
const canWrite = computed(() => auth.can('work_orders:write'))
const viewerId = computed(() => String(auth.user?.id ?? ''))

// ============================================================
// 列表 + 多维筛选（全部走后端查询参数）
// ============================================================
const items = ref<WorkOrderRecord[]>([])
const total = ref(0)
const page = ref(1)
const pageSize = 20
const loading = ref(false)
const statusFilter = ref<WorkOrderStatus | ''>('')
const typeFilter = ref<WorkOrderType | ''>('')
const priorityFilter = ref<WorkOrderPriority | ''>('')
const search = ref('')
const scope = ref<'' | 'mine' | 'assigned'>('')
const busyId = ref<number | null>(null)

// 泳道按后端 status 枚举顺序排列（open → in_progress → resolved → closed）
const lanes = (Object.keys(statusMeta) as WorkOrderStatus[])
  .sort((a, b) => statusMeta[a].rank - statusMeta[b].rank)
  .map(key => ({ key, ...statusMeta[key] }))

const laneCounts = computed(() => {
  const counts: Record<WorkOrderStatus, number> = { open: 0, in_progress: 0, resolved: 0, closed: 0 }
  items.value.forEach(row => {
    if (row.status in counts) counts[row.status] += 1
  })
  return counts
})

async function load() {
  loading.value = true
  try {
    const me = Number(viewerId.value)
    const data = paged<WorkOrderRecord>(await workOrderApi.list({
      status: statusFilter.value,
      type: typeFilter.value,
      priority: priorityFilter.value,
      search: search.value.trim() || undefined,
      // 仅客服/管理员可用范围筛选：后端对普通用户传 user_id 会返回 403
      user_id: isStaff.value && scope.value === 'mine' && me > 0 ? me : undefined,
      assigned_to: isStaff.value && scope.value === 'assigned' && me > 0 ? me : undefined,
      page: page.value,
      page_size: pageSize
    }))
    items.value = data.items
    total.value = data.total
    if (drawerVisible.value && current.value) {
      current.value = data.items.find(row => row.id === current.value?.id) ?? current.value
    }
  } catch (e) {
    message.error(errorText(e))
  } finally {
    loading.value = false
  }
}

watch(() => auth.projectId, () => { page.value = 1; load() }, { immediate: true })
watch([page, statusFilter, typeFilter, priorityFilter, scope], () => load())
let searchTimer: ReturnType<typeof setTimeout> | undefined
watch(search, () => {
  clearTimeout(searchTimer)
  searchTimer = setTimeout(() => { page.value = 1; load() }, 320)
})

function resetFilters() {
  statusFilter.value = ''
  typeFilter.value = ''
  priorityFilter.value = ''
  search.value = ''
  scope.value = ''
  page.value = 1
}

// ============================================================
// 创建工单
// ============================================================
const createVisible = ref(false)
const submitting = ref(false)
const createForm = ref<{
  type: WorkOrderType
  title: string
  description: string
  priority: WorkOrderPriority
}>({ type: 'fault', title: '', description: '', priority: 'medium' })

const createErrors = computed(() => {
  const errors: string[] = []
  if (!createForm.value.title.trim()) errors.push('请填写工单标题')
  if (createForm.value.title.trim().length > 256) errors.push('标题不能超过 256 字')
  if (!createForm.value.type) errors.push('请选择工单类型')
  return errors
})

function openCreate() {
  createForm.value = { type: 'fault', title: '', description: '', priority: 'medium' }
  createVisible.value = true
}

async function submitCreate() {
  if (createErrors.value.length) { message.warning(createErrors.value[0]); return }
  const payload: WorkOrderCreateInput = {
    type: createForm.value.type,
    title: createForm.value.title.trim(),
    description: createForm.value.description.trim() || undefined,
    priority: createForm.value.priority
  }
  submitting.value = true
  try {
    await workOrderApi.create(payload)
    message.success('工单已提交，等待受理')
    createVisible.value = false
    page.value = 1
    await load()
  } catch (e) {
    message.error(errorText(e))
  } finally {
    submitting.value = false
  }
}

// ============================================================
// 详情抽屉
// ============================================================
const drawerVisible = ref(false)
const current = ref<WorkOrderRecord | null>(null)
const detailLoading = ref(false)

async function openDetail(row: WorkOrderRecord) {
  current.value = row
  drawerVisible.value = true
  detailLoading.value = true
  try {
    current.value = unwrap<WorkOrderRecord>(await workOrderApi.get(row.id))
  } catch (e) {
    // 详情不可读时退回列表数据，仍允许基于列表状态进行流转
    message.warning(errorText(e))
  } finally {
    detailLoading.value = false
  }
}

// ============================================================
// 状态流转
// ============================================================
function transitionBlock(row: WorkOrderRecord, target: WorkOrderTransition): string | null {
  if (!canWrite.value) return '缺少 work_orders:write 权限'
  if (!isStaff.value && (target === 'in_progress' || target === 'resolved')) {
    const assignee = String(row.assigned_to ?? '')
    if (!assignee) return '尚未指派处理人，指派能力待后端提供'
    if (assignee !== viewerId.value) return '仅被指派的处理人可执行该流转'
  }
  return null
}

async function transition(row: WorkOrderRecord, target: WorkOrderTransition) {
  if (busyId.value != null || transitionBlock(row, target)) return
  busyId.value = row.id
  try {
    await workOrderApi.transition(row.id, target)
    message.success(`${row.title.slice(0, 18)} → ${statusMeta[target].label}`)
    await load()
  } catch (e) {
    // 非法流转后端返回「不允许从 X 流转到 Y」，原样透出便于核对
    message.error(errorText(e))
  } finally {
    busyId.value = null
  }
}

// ============================================================
// 展示辅助
// ============================================================
function stamp(value?: string | null, template = 'YYYY-MM-DD HH:mm') {
  if (!value) return '—'
  const parsed = dayjs(String(value))
  return parsed.isValid() ? parsed.format(template) : '—'
}

const timeline = computed(() => {
  const row = current.value
  if (!row) return []
  const rank = statusMeta[row.status]?.rank ?? 0
  return [
    { label: '已提交', detail: `提交人 #${row.user_id}`, at: row.created_at ?? null, done: true },
    { label: '开始处理', detail: row.assigned_to ? `处理人 #${row.assigned_to}` : '等待指派处理人', at: null as string | null, done: rank >= 1 },
    { label: '已解决', detail: '解决方案待客服回填', at: row.resolved_at ?? null, done: !!row.resolved_at },
    { label: '已关闭', detail: '提交人确认或超时关闭', at: row.closed_at ?? null, done: !!row.closed_at }
  ]
})

const columns = computed<DataTableColumns<WorkOrderRecord>>(() => [
  {
    title: '优先级',
    key: 'priority',
    minWidth: 104,
    render: row => {
      const meta = priorityMeta[row.priority] ?? { label: String(row.priority), level: 0, tone: 'var(--color-info)' }
      return h('span', { class: ['prio', { 'prio--urgent': row.priority === 'urgent' }] }, [
        h('i', { class: 'prio-bar', style: { background: meta.tone, width: `${meta.level * 25}%` } }),
        h('em', { style: { color: meta.tone } }, meta.label)
      ])
    }
  },
  {
    title: '工单',
    key: 'title',
    minWidth: 280,
    render: row => h('div', { class: 'wo-title' }, [
      h('strong', row.title),
      h('span', row.description ? String(row.description).slice(0, 46) : '（无补充描述）')
    ])
  },
  {
    title: '类型',
    key: 'type',
    minWidth: 104,
    render: row => h('span', { class: 'wo-type' }, typeLabels[row.type] ?? String(row.type))
  },
  {
    title: '状态',
    key: 'status',
    minWidth: 104,
    render: row => {
      const meta = statusMeta[row.status] ?? { label: String(row.status), type: 'default' as const, rank: 0 }
      return h(NTag, { size: 'small', bordered: false, type: meta.type }, { default: () => meta.label })
    }
  },
  {
    title: '提交 / 处理人',
    key: 'user_id',
    minWidth: 132,
    render: row => h('div', { class: 'wo-people' }, [
      h('span', `提交人 #${row.user_id}`),
      h('em', row.assigned_to ? `处理人 #${row.assigned_to}` : '未指派')
    ])
  },
  {
    title: '创建时间',
    key: 'created_at',
    minWidth: 148,
    render: row => h('span', { class: 'wo-time' }, stamp(row.created_at))
  },
  {
    title: '操作',
    key: 'actions',
    width: 230,
    fixed: 'right',
    render: row => {
      const allowed = transitionMatrix[row.status] ?? []
      const buttons = [
        h(NButton, { text: true, onClick: () => openDetail(row) }, { default: () => '详情' })
      ]
      if (canWrite.value && allowed.length) {
        allowed.forEach(target => {
          const block = transitionBlock(row, target)
          buttons.push(h(NButton, {
            text: true,
            type: target === 'closed' ? 'default' : 'primary',
            disabled: !!block || busyId.value != null,
            loading: busyId.value === row.id,
            title: block ?? transitionLabels[target],
            onClick: () => transition(row, target)
          }, { default: () => transitionLabels[target] }))
        })
      } else if (!allowed.length) {
        buttons.push(h('span', { class: 'action-muted' }, '已终结'))
      }
      return h('div', { class: 'table-actions' }, buttons)
    }
  }
])
</script>

<template>
  <div class="page wo-page">
    <div class="page-head">
      <div>
        <div class="eyebrow dark">SUPPORT / DISPATCH BOARD</div>
        <h1>工单工作台</h1>
        <p>提交与流转故障、技术支持与咨询工单，普通用户仅见与自己相关的单</p>
      </div>
      <div class="head-actions">
        <n-button quaternary :loading="loading" @click="load">刷新</n-button>
        <n-button type="primary" :disabled="!canWrite" @click="openCreate">+ 提交工单</n-button>
      </div>
    </div>

    <!-- 受理泳道：点击即筛选，计数为当前页 -->
    <div class="lane-strip">
      <button
        v-for="lane in lanes"
        :key="lane.key"
        class="lane"
        :class="[{ 'lane--on': statusFilter === lane.key }, `lane--${lane.key}`]"
        type="button"
        @click="statusFilter = statusFilter === lane.key ? '' : lane.key"
      >
        <span class="lane-rank">0{{ lane.rank + 1 }}</span>
        <strong>{{ lane.label }}</strong>
        <em>{{ laneCounts[lane.key] }} 条</em>
      </button>
      <div class="lane-sum">
        <span>合计</span>
        <strong>{{ total }}</strong>
        <button type="button" @click="resetFilters">清除筛选</button>
      </div>
    </div>

    <div class="panel">
      <div class="panel-toolbar wo-toolbar">
        <div class="panel-title">
          工单列表 <span class="count">{{ total }}</span>
        </div>
        <div class="toolbar-actions wo-filters">
          <n-input v-model:value="search" size="small" placeholder="搜索标题或描述" clearable class="wo-search" />
          <n-select v-model:value="statusFilter" :options="statusOptions" size="small" class="wo-filter" />
          <n-select v-model:value="typeFilter" :options="typeFilterOptions" size="small" class="wo-filter" />
          <n-select v-model:value="priorityFilter" :options="priorityOptions" size="small" class="wo-filter" />
          <n-select
            v-if="isStaff"
            v-model:value="scope"
            size="small"
            class="wo-filter"
            :options="[
              { label: '全部工单', value: '' },
              { label: '我提交的', value: 'mine' },
              { label: '指派给我的', value: 'assigned' }
            ]"
          />
        </div>
      </div>
      <n-data-table
        remote
        size="small"
        :columns="columns"
        :data="items"
        :loading="loading"
        :row-key="(row: WorkOrderRecord) => row.id"
        :scroll-x="1180"
        :pagination="{
          page,
          pageSize,
          itemCount: total,
          showSizePicker: false,
          onUpdatePage: (value: number) => (page = value)
        }"
      />
      <div v-if="!loading && !items.length" class="empty-hint">没有符合条件的工单</div>
    </div>

    <!-- 详情抽屉 -->
    <n-drawer v-model:show="drawerVisible" :width="460" placement="right">
      <n-drawer-content :title="current ? `工单 #${current.id}` : '工单详情'" closable>
        <div v-if="current" class="wo-detail" :class="`wo-detail--${current.priority}`">
          <div class="detail-head">
            <h2>{{ current.title }}</h2>
            <div class="detail-tags">
              <n-tag size="small" :bordered="false" :type="statusMeta[current.status]?.type ?? 'default'">
                {{ statusMeta[current.status]?.label ?? current.status }}
              </n-tag>
              <span class="detail-prio" :style="{ color: priorityMeta[current.priority]?.tone }">
                {{ priorityMeta[current.priority]?.label ?? current.priority }}
              </span>
              <span class="detail-type">{{ typeLabels[current.type] ?? current.type }}</span>
            </div>
          </div>

          <section class="detail-block">
            <h3>问题描述</h3>
            <p class="detail-desc">{{ current.description || '提交人未填写补充描述。' }}</p>
          </section>

          <section class="detail-block">
            <h3>处理记录</h3>
            <ol class="timeline">
              <li v-for="(step, index) in timeline" :key="step.label" :class="{ 'is-done': step.done }">
                <span class="dot">{{ index + 1 }}</span>
                <div class="step-body">
                  <strong>{{ step.label }}</strong>
                  <em>{{ step.at ? stamp(step.at, 'YYYY-MM-DD HH:mm:ss') : step.done ? '已进入该状态' : step.detail }}</em>
                </div>
              </li>
            </ol>
            <p class="detail-note">
              处理人当前为只读展示：指派与回复接口尚未由后端提供，完成后无法在此填写解决方案。
            </p>
          </section>

          <section class="detail-block detail-meta">
            <div><span>提交人</span><strong>#{{ current.user_id }}</strong></div>
            <div><span>处理人</span><strong>{{ current.assigned_to ? `#${current.assigned_to}` : '未指派' }}</strong></div>
            <div><span>创建时间</span><strong>{{ stamp(current.created_at, 'YYYY-MM-DD HH:mm:ss') }}</strong></div>
            <div><span>最后更新</span><strong>{{ stamp(current.updated_at, 'YYYY-MM-DD HH:mm:ss') }}</strong></div>
          </section>
        </div>
        <div v-else class="empty-hint">未选择工单</div>

        <template #footer>
          <div v-if="current" class="detail-footer">
            <span v-if="!(transitionMatrix[current.status] ?? []).length" class="action-muted">
              {{ current.status === 'closed' ? '工单已关闭，流程终结' : '当前状态无可执行流转' }}
            </span>
            <template v-for="target in transitionMatrix[current.status] ?? []" :key="target">
              <n-button
                :type="target === 'closed' ? 'default' : 'primary'"
                :disabled="!!transitionBlock(current!, target) || busyId != null"
                :loading="busyId === current!.id"
                :title="transitionBlock(current!, target) ?? ''"
                @click="transition(current!, target)"
              >
                {{ transitionLabels[target] }}
              </n-button>
            </template>
          </div>
        </template>
      </n-drawer-content>
    </n-drawer>

    <!-- 提交工单 -->
    <n-modal v-model:show="createVisible" preset="card" title="提交工单" :style="{ width: '560px' }">
      <div class="form-grid">
        <label class="form-item">
          <span>工单类型</span>
          <n-select v-model:value="createForm.type" :options="typeChoices" />
        </label>
        <label class="form-item">
          <span>优先级</span>
          <n-select v-model:value="createForm.priority" :options="priorityChoices" />
        </label>
        <label class="form-item form-item--wide">
          <span>标题<em>必填</em></span>
          <n-input v-model:value="createForm.title" maxlength="256" show-count placeholder="一句话说明现象，例如：GPU 节点开机后 BMC 无法 ping 通" />
        </label>
        <label class="form-item form-item--wide">
          <span>详细描述</span>
          <n-input
            v-model:value="createForm.description"
            type="textarea"
            :autosize="{ minRows: 4, maxRows: 8 }"
            placeholder="复现步骤、影响范围、期望时间；故障类建议附资产 ID"
          />
        </label>
        <p class="form-item form-note">
          工单创建后状态为「待受理」。普通用户需等待客服受理后才能推进状态，处理人指派能力待后端提供。
        </p>
        <ul v-if="createErrors.length" class="form-errors">
          <li v-for="item in createErrors" :key="item">{{ item }}</li>
        </ul>
      </div>
      <div class="modal-actions">
        <n-button quaternary @click="createVisible = false">取消</n-button>
        <n-button type="primary" :loading="submitting" @click="submitCreate">提交工单</n-button>
      </div>
    </n-modal>
  </div>
</template>

<style scoped>
.wo-page {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

/* 受理泳道 */
.lane-strip {
  display: flex;
  align-items: stretch;
  gap: 10px;
  padding: 12px 16px;
  background: var(--color-surface);
  border: 1px solid var(--color-border);
  border-radius: var(--radius-lg);
  overflow-x: auto;
}
.lane {
  position: relative;
  display: flex;
  flex-direction: column;
  gap: 2px;
  min-width: 128px;
  padding: 8px 14px 8px 16px;
  text-align: left;
  background: var(--color-surface-raised);
  border: 1px solid var(--color-border-light);
  border-radius: var(--radius-md);
  cursor: pointer;
  transition: border-color var(--transition-fast), transform var(--transition-fast), box-shadow var(--transition-fast);
}
.lane:hover { transform: translateY(-1px); box-shadow: var(--shadow-sm); border-color: var(--color-primary-hover); }
.lane::before {
  content: '';
  position: absolute;
  left: 0;
  top: 8px;
  bottom: 8px;
  width: 3px;
  border-radius: var(--radius-full);
  background: var(--color-border-dark);
}
.lane--open::before { background: var(--color-warning); }
.lane--in_progress::before { background: var(--color-primary); }
.lane--resolved::before { background: var(--color-success); }
.lane--closed::before { background: var(--color-text-disabled); }
.lane--on { border-color: var(--color-primary); background: var(--color-primary-light); }
.lane-rank {
  font-size: 10px;
  letter-spacing: 0.16em;
  color: var(--color-text-tertiary);
  font-variant-numeric: tabular-nums;
}
.lane strong { font-size: var(--font-size-base); color: var(--color-text-primary); }
.lane em { font-style: normal; font-size: var(--font-size-xs); color: var(--color-text-secondary); font-variant-numeric: tabular-nums; }
.lane-sum {
  margin-left: auto;
  display: flex;
  align-items: center;
  gap: 10px;
  padding-left: 14px;
  white-space: nowrap;
}
.lane-sum span { font-size: var(--font-size-xs); color: var(--color-text-tertiary); }
.lane-sum strong { font-size: var(--font-size-xl); font-variant-numeric: tabular-nums; color: var(--color-text-primary); }
.lane-sum button {
  padding: 4px 12px;
  font-size: var(--font-size-xs);
  color: var(--color-text-secondary);
  background: transparent;
  border: 1px dashed var(--color-border-dark);
  border-radius: var(--radius-full);
  cursor: pointer;
  transition: color var(--transition-fast), border-color var(--transition-fast);
}
.lane-sum button:hover { color: var(--color-primary); border-color: var(--color-primary); }

.wo-toolbar { display: flex; align-items: center; justify-content: space-between; gap: 12px; flex-wrap: wrap; }
.wo-filters { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; }
.wo-search { width: 200px; }
.wo-filter { width: 140px; }

:deep(.prio) { display: flex; align-items: center; gap: 8px; }
:deep(.prio-bar) {
  display: inline-block;
  height: 4px;
  min-width: 8px;
  border-radius: var(--radius-full);
}
:deep(.prio em) { font-style: normal; font-size: var(--font-size-xs); font-weight: 600; }
:deep(.prio--urgent em::after) {
  content: '';
  display: inline-block;
  width: 5px;
  height: 5px;
  margin-left: 5px;
  border-radius: 50%;
  background: var(--color-error);
  animation: wo-pulse 1.6s ease-in-out infinite;
}
@keyframes wo-pulse {
  0%, 100% { opacity: 1; transform: scale(1); }
  50% { opacity: 0.35; transform: scale(0.7); }
}
:deep(.wo-title) { display: flex; flex-direction: column; gap: 2px; }
:deep(.wo-title strong) { font-weight: 600; color: var(--color-text-primary); }
:deep(.wo-title span) {
  font-size: var(--font-size-xs);
  color: var(--color-text-tertiary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
:deep(.wo-type) { font-size: var(--font-size-sm); color: var(--color-text-secondary); }
:deep(.wo-people) { display: flex; flex-direction: column; gap: 1px; font-size: var(--font-size-xs); color: var(--color-text-secondary); }
:deep(.wo-people em) { font-style: normal; color: var(--color-text-tertiary); }
:deep(.wo-time) { font-variant-numeric: tabular-nums; color: var(--color-text-secondary); }
:deep(.action-muted) { font-size: var(--font-size-xs); color: var(--color-text-disabled); }

/* 抽屉详情 */
.wo-detail { display: flex; flex-direction: column; gap: 20px; }
.detail-head h2 {
  margin: 0 0 8px;
  font-size: var(--font-size-lg);
  line-height: 1.5;
  font-weight: 600;
  color: var(--color-text-primary);
}
.detail-tags { display: flex; align-items: center; gap: 10px; }
.detail-prio { font-size: var(--font-size-xs); font-weight: 700; letter-spacing: 0.04em; }
.detail-type { font-size: var(--font-size-xs); color: var(--color-text-tertiary); }
.detail-block h3 {
  margin: 0 0 8px;
  font-size: 11px;
  letter-spacing: 0.16em;
  text-transform: uppercase;
  color: var(--color-text-tertiary);
  font-weight: 600;
}
.detail-desc {
  margin: 0;
  font-size: var(--font-size-sm);
  line-height: 1.8;
  color: var(--color-text-secondary);
  white-space: pre-wrap;
  word-break: break-word;
}
.timeline {
  list-style: none;
  margin: 0;
  padding: 0;
  display: grid;
  gap: 0;
}
.timeline li {
  position: relative;
  display: flex;
  gap: 12px;
  padding: 0 0 16px 0;
}
.timeline li::before {
  content: '';
  position: absolute;
  left: 9px;
  top: 20px;
  bottom: 0;
  width: 1px;
  background: var(--color-border);
}
.timeline li:last-child::before { display: none; }
.timeline .dot {
  flex: none;
  width: 19px;
  height: 19px;
  display: grid;
  place-content: center;
  font-size: 10px;
  font-variant-numeric: tabular-nums;
  color: var(--color-text-tertiary);
  background: var(--color-surface-raised);
  border: 1px solid var(--color-border);
  border-radius: 50%;
}
.timeline li.is-done .dot {
  color: var(--color-text-inverse);
  background: var(--color-primary);
  border-color: var(--color-primary);
}
.step-body { display: flex; flex-direction: column; gap: 2px; }
.step-body strong { font-size: var(--font-size-sm); color: var(--color-text-primary); }
.step-body em { font-style: normal; font-size: var(--font-size-xs); color: var(--color-text-tertiary); }
.detail-note {
  margin: 4px 0 0;
  padding: 8px 10px;
  font-size: var(--font-size-xs);
  line-height: 1.7;
  color: var(--color-text-secondary);
  background: var(--color-warning-light);
  border-radius: var(--radius-sm);
}
.detail-meta {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 10px 14px;
  padding-top: 14px;
  border-top: 1px solid var(--color-border-light);
}
.detail-meta div { display: flex; flex-direction: column; gap: 2px; }
.detail-meta span { font-size: var(--font-size-xs); color: var(--color-text-tertiary); }
.detail-meta strong { font-size: var(--font-size-sm); font-weight: 600; color: var(--color-text-primary); }
.detail-footer { display: flex; align-items: center; justify-content: flex-end; gap: 10px; flex-wrap: wrap; }

/* 创建表单 */
.form-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 14px;
}
.form-item { display: flex; flex-direction: column; gap: 6px; }
.form-item > span { font-size: var(--font-size-sm); color: var(--color-text-secondary); }
.form-item > span em { margin-left: 6px; font-style: normal; font-size: var(--font-size-xs); color: var(--color-error); }
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
</style>
