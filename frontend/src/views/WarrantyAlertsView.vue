<script setup lang="ts">
// 维保到期预警页 — 自用模式：按告警窗口列出即将到保的服务器，并按剩余天数分级预警
// 后端契约：GET /assets/warranty-alerts?days=（默认 90，有效范围 1–365）
// 响应 {items:[{asset_id,name,serial_number,manufacturer,model,status,warranty_expiry,days_remaining}], total, alert_days}
// 注意：该响应不含 page / page_size，因此本页不做服务端分页，筛选与排序在前端完成。
import { computed, h, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import {
  NButton,
  NDataTable,
  NIcon,
  NInput,
  NInputNumber,
  NProgress,
  NTag,
  useMessage
} from 'naive-ui'
import type { DataTableColumns } from 'naive-ui'
import dayjs from 'dayjs'
import { depreciationApi, errorText, exportToExcel, rows, unwrap, type RecordData } from '../api'
import { useAuth } from '../stores/auth'

const router = useRouter()
const message = useMessage()
const auth = useAuth()

// 告警窗口边界与后端 listWarrantyAlerts 保持一致（越界或非法值回退 90）
const DEFAULT_ALERT_DAYS = 90
const MIN_ALERT_DAYS = 1
const MAX_ALERT_DAYS = 365
const PRESET_WINDOWS = [30, 60, 90, 180, 365]

const days = ref<number>(DEFAULT_ALERT_DAYS)
// appliedDays 取服务端回传的 alert_days，确保展示口径与实际查询窗口一致
const appliedDays = ref<number>(DEFAULT_ALERT_DAYS)
const alerts = ref<RecordData[]>([])
const total = ref(0)
const loading = ref(false)
const loadError = ref('')
const search = ref('')

// ============================================================
// 数据加载
// ============================================================
function clampDays(value: number): number {
  if (!Number.isFinite(value)) return DEFAULT_ALERT_DAYS
  return Math.min(Math.max(Math.round(value), MIN_ALERT_DAYS), MAX_ALERT_DAYS)
}

async function load() {
  if (!auth.projectId) return
  loading.value = true
  loadError.value = ''
  const alertWindow = clampDays(days.value)
  try {
    const response = await depreciationApi.warrantyAlerts(alertWindow)
    const payload = unwrap<RecordData>(response)
    const data = rows(response)
    alerts.value = data.items
    total.value = data.total
    appliedDays.value = Number(payload?.alert_days ?? alertWindow)
  } catch (e) {
    loadError.value = errorText(e)
    message.error(loadError.value)
  } finally {
    loading.value = false
  }
}

watch(() => auth.projectId, () => {
  alerts.value = []
  total.value = 0
  void load()
}, { immediate: true })

// ============================================================
// 剩余天数分级
// ============================================================
type Tone = 'urgent' | 'high' | 'mid' | 'low'
type TagType = 'error' | 'warning' | 'info' | 'success' | 'default'

function urgencyOf(remaining: number): { label: string; tone: Tone; type: TagType } {
  if (remaining <= 7) return { label: '危急', tone: 'urgent', type: 'error' }
  if (remaining <= 30) return { label: '紧急', tone: 'high', type: 'error' }
  if (remaining <= 90) return { label: '关注', tone: 'mid', type: 'warning' }
  return { label: '平稳', tone: 'low', type: 'info' }
}

// assets.status DDL 枚举：unknown|online|offline|maintenance|retired
const ASSET_STATUS_LABELS: Record<string, string> = {
  unknown: '状态未知', online: '在线', offline: '离线', maintenance: '维护中', retired: '已退役',
  in_use: '使用中', running: '运行中', error: '纳管失败'
}

function statusLabel(value: unknown): string {
  const key = String(value ?? '')
  return key ? ASSET_STATUS_LABELS[key] || key : '状态未知'
}

function statusType(value: unknown): TagType {
  const key = String(value ?? '')
  if (['online', 'running', 'in_use'].includes(key)) return 'success'
  if (key === 'maintenance') return 'info'
  if (key === 'error') return 'error'
  if (['retired', 'offline'].includes(key)) return 'warning'
  return 'default'
}

function expiryDate(value: unknown): string {
  if (!value) return '—'
  const at = dayjs(String(value))
  return at.isValid() ? at.format('YYYY-MM-DD') : String(value)
}

const remainingOf = (row: RecordData) => Number(row.days_remaining ?? 0)

// ============================================================
// 前端筛选（后端未提供 search / 分页参数）
// ============================================================
const filtered = computed<RecordData[]>(() => {
  const kw = search.value.trim().toLowerCase()
  const list = kw
    ? alerts.value.filter(row => [row.name, row.serial_number, row.manufacturer, row.model, row.asset_id]
      .some(value => String(value ?? '').toLowerCase().includes(kw)))
    : alerts.value.slice()
  return list.sort((a, b) => remainingOf(a) - remainingOf(b))
})

const buckets = computed(() => {
  const counts = { urgent: 0, high: 0, mid: 0, low: 0 }
  for (const row of filtered.value) counts[urgencyOf(remainingOf(row)).tone] += 1
  const earliest = filtered.value.reduce<number | null>((min, row) => {
    const at = dayjs(String(row.warranty_expiry ?? ''))
    if (!at.isValid()) return min
    const stamp = at.valueOf()
    return min == null || stamp < min ? stamp : min
  }, null)
  return {
    ...counts,
    window: appliedDays.value,
    earliest: earliest == null ? '—' : dayjs(earliest).format('YYYY-MM-DD')
  }
})

// ============================================================
// 表格列
// ============================================================
const columns = computed<DataTableColumns<RecordData>>(() => [
  {
    title: '服务器',
    key: 'name',
    minWidth: 190,
    ellipsis: { tooltip: true },
    sorter: (a, b) => String(a.name ?? '').localeCompare(String(b.name ?? ''), 'zh-CN'),
    render: row => h('div', { class: 'wa-asset-cell' }, [
      h(NButton, {
        text: true,
        type: 'primary',
        onClick: () => router.push(`/assets/${row.asset_id}`)
      }, { default: () => String(row.name ?? `资产 #${row.asset_id}`) }),
      h('span', { class: 'wa-asset-meta' }, `#${row.asset_id} · SN ${String(row.serial_number || '—')}`)
    ])
  },
  {
    title: '厂商 / 型号',
    key: 'manufacturer',
    minWidth: 150,
    ellipsis: { tooltip: true },
    render: row => [String(row.manufacturer || ''), String(row.model || '')].filter(Boolean).join(' · ') || '—'
  },
  {
    title: '当前状态',
    key: 'status',
    width: 100,
    render: row => h(NTag,
      { size: 'small', bordered: false, type: statusType(row.status) },
      { default: () => statusLabel(row.status) })
  },
  {
    title: '维保到期',
    key: 'warranty_expiry',
    minWidth: 120,
    sorter: (a, b) => dayjs(String(a.warranty_expiry ?? 0)).valueOf() - dayjs(String(b.warranty_expiry ?? 0)).valueOf(),
    render: row => h('span', { class: 'mono-value' }, expiryDate(row.warranty_expiry))
  },
  {
    title: '剩余天数',
    key: 'days_remaining',
    minWidth: 170,
    defaultSortOrder: 'ascend',
    sorter: (a, b) => remainingOf(a) - remainingOf(b),
    render: row => {
      const remaining = remainingOf(row)
      const urgency = urgencyOf(remaining)
      const percentage = Math.max(Math.min(Math.round((remaining / appliedDays.value) * 100), 100), 2)
      return h('div', { class: ['wa-remaining', `wa-remaining-${urgency.tone}`] }, [
        h('div', { class: 'wa-remaining-head' }, [
          h('span', { class: 'wa-remaining-days' }, `${remaining} 天`),
          h(NTag, { size: 'tiny', bordered: false, type: urgency.type }, { default: () => urgency.label })
        ]),
        h(NProgress, {
          type: 'line',
          percentage,
          displayIndicator: false,
          height: 4,
          fillBorderRadius: 2,
          status: urgency.type === 'error' ? 'error' : urgency.type === 'warning' ? 'warning' : 'success'
        })
      ])
    }
  }
])

// ============================================================
// 导出
// ============================================================
async function exportExcel() {
  if (!filtered.value.length) {
    message.warning('当前没有可导出的维保告警数据')
    return
  }
  const data = filtered.value.map(row => ({
    资产ID: row.asset_id,
    服务器名称: row.name ?? '',
    序列号: row.serial_number ?? '',
    厂商: row.manufacturer ?? '',
    型号: row.model ?? '',
    状态: statusLabel(row.status),
    维保到期: expiryDate(row.warranty_expiry),
    剩余天数: remainingOf(row)
  }))
  try {
    await exportToExcel(data, Object.keys(data[0]), `维保到期预警_近${appliedDays.value}天`)
    message.success('已导出维保到期清单')
  } catch (e) {
    message.error(errorText(e))
  }
}
</script>

<template>
  <div class="page">
    <div class="page-head">
      <div>
        <div class="eyebrow dark">ASSET / WARRANTY</div>
        <h1>维保告警</h1>
        <p>按告警窗口巡检即将到保的服务器，提前安排续保、延保或退役计划</p>
      </div>
      <div class="head-actions">
        <n-button quaternary :loading="loading" @click="load">刷新</n-button>
        <n-button type="primary" ghost :disabled="!filtered.length" @click="exportExcel">导出清单</n-button>
      </div>
    </div>

    <div class="metric-grid wa-metric-grid">
      <div class="metric-card">
        <div class="metric-icon primary">
          <svg width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8"><path d="M12 22s8-4 8-10V5l-8-3-8 3v7c0 6 8 10 8 10z"/></svg>
        </div>
        <div class="metric-content">
          <div class="metric-label">窗口内到保</div>
          <div class="metric-value">{{ total }}</div>
          <div class="metric-foot">近 {{ appliedDays }} 天需处理</div>
        </div>
      </div>
      <div class="metric-card">
        <div class="metric-icon warning">
          <svg width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8"><circle cx="12" cy="12" r="9"/><path d="M12 7v6l4 2"/></svg>
        </div>
        <div class="metric-content">
          <div class="metric-label">危急 / 紧急</div>
          <div class="metric-value">{{ buckets.urgent }} / {{ buckets.high }}</div>
          <div class="metric-foot">≤7 天与 ≤30 天两个分级</div>
        </div>
      </div>
      <div class="metric-card">
        <div class="metric-icon info">
          <svg width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8"><rect x="3" y="5" width="18" height="16" rx="2"/><path d="M8 3v4M16 3v4M3 11h18"/></svg>
        </div>
        <div class="metric-content">
          <div class="metric-label">最早到期日</div>
          <div class="metric-value">{{ buckets.earliest }}</div>
          <div class="metric-foot">关注 {{ buckets.mid }} 台 · 平稳 {{ buckets.low }} 台</div>
        </div>
      </div>
    </div>

    <div class="panel">
      <div class="panel-toolbar">
        <div>
          <div class="panel-title">到保清单 <span class="count">{{ filtered.length }}</span></div>
          <p class="panel-caption">
            <template v-if="loadError">加载失败：{{ loadError }}</template>
            <template v-else>告警窗口 {{ appliedDays }} 天，仅统计维保到期日在今天之后的服务器；已过期维保请查看资产详情</template>
          </p>
        </div>
        <div class="toolbar-actions wa-toolbar">
          <div class="wa-window-group">
            <span class="wa-window-label">提前</span>
            <n-input-number
              v-model:value="days"
              size="small"
              :min="MIN_ALERT_DAYS"
              :max="MAX_ALERT_DAYS"
              :precision="0"
              style="width: 124px"
            />
            <span class="wa-window-label">天</span>
            <n-button size="tiny" type="primary" :loading="loading" @click="load">应用</n-button>
          </div>
          <div class="wa-presets">
            <button
              v-for="preset in PRESET_WINDOWS"
              :key="preset"
              class="wa-preset"
              :class="{ active: appliedDays === preset }"
              @click="days = preset; load()"
            >
              {{ preset }}天
            </button>
          </div>
          <n-input v-model:value="search" size="small" clearable placeholder="搜索名称 / SN / 厂商 / 型号" style="width: 200px" />
        </div>
      </div>

      <n-data-table
        size="small"
        :columns="columns"
        :data="filtered"
        :loading="loading"
        :row-key="(row: RecordData) => String(row.asset_id ?? '')"
        :scroll-x="860"
        :pagination="{ pageSize: 20, showSizePicker: true, pageSizes: [20, 50, 100] }"
      />

      <div v-if="!filtered.length && !loading" class="wa-empty">
        <div class="wa-empty-mark">
          <n-icon :size="22"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8"><path d="M12 22s8-4 8-10V5l-8-3-8 3v7c0 6 8 10 8 10z"/><path d="M9 12l2 2 4-4"/></svg></n-icon>
        </div>
        <div class="wa-empty-title">
          <template v-if="search">没有匹配「{{ search }}」的到保服务器</template>
          <template v-else>未来 {{ appliedDays }} 天内没有需要预警的维保</template>
        </div>
        <p class="wa-empty-text">
          预警依赖资产台账中的「维保到期」日期。若清单为空但仍有设备在保，请在服务器详情补录 warranty_expiry；
          也可放宽告警窗口（最大 {{ MAX_ALERT_DAYS }} 天）查看更远的到期计划。
        </p>
      </div>
    </div>
  </div>
</template>

<style scoped>
.wa-metric-grid {
  grid-template-columns: repeat(3, minmax(0, 1fr));
}

.wa-toolbar {
  align-items: center;
  flex-wrap: wrap;
  gap: 10px;
}

.wa-window-group {
  display: flex;
  align-items: center;
  gap: 6px;
}

.wa-window-label {
  font-size: var(--font-size-xs);
  color: var(--color-text-tertiary);
}

.wa-presets {
  display: flex;
  align-items: center;
  gap: 4px;
}

.wa-preset {
  border: 1px solid var(--color-border);
  background: var(--color-surface);
  color: var(--color-text-secondary);
  border-radius: var(--radius-full);
  padding: 3px 10px;
  font-size: var(--font-size-xs);
  cursor: pointer;
  transition: all var(--transition-fast);
}

.wa-preset:hover {
  border-color: var(--color-primary);
  color: var(--color-primary);
}

.wa-preset.active {
  background: var(--color-primary);
  border-color: var(--color-primary);
  color: #fff;
}

:deep(.wa-asset-cell) {
  display: flex;
  flex-direction: column;
  gap: 2px;
  min-width: 0;
}

:deep(.wa-asset-meta) {
  font-size: 11px;
  color: var(--color-text-tertiary);
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
}

:deep(.mono-value) {
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-variant-numeric: tabular-nums;
}

:deep(.wa-remaining) {
  display: flex;
  flex-direction: column;
  gap: 4px;
}

:deep(.wa-remaining-head) {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
}

:deep(.wa-remaining-days) {
  font-variant-numeric: tabular-nums;
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 13px;
  font-weight: 700;
}

:deep(.wa-remaining-urgent .wa-remaining-days) {
  color: var(--color-error);
}

:deep(.wa-remaining-high .wa-remaining-days) {
  color: var(--color-warning);
}

:deep(.wa-remaining-mid .wa-remaining-days) {
  color: var(--color-warning);
}

:deep(.wa-remaining-low .wa-remaining-days) {
  color: var(--color-primary);
}

.wa-empty {
  padding: 40px 16px;
  text-align: center;
}

.wa-empty-mark {
  width: 46px;
  height: 46px;
  margin: 0 auto 12px;
  border-radius: var(--radius-lg);
  display: flex;
  align-items: center;
  justify-content: center;
  color: var(--color-primary);
  background: var(--color-primary-light);
}

.wa-empty-title {
  font-size: var(--font-size-base);
  font-weight: 600;
  color: var(--color-text-primary);
  margin-bottom: 8px;
}

.wa-empty-text {
  margin: 0 auto;
  max-width: 620px;
  font-size: var(--font-size-xs);
  line-height: 1.8;
  color: var(--color-text-tertiary);
}

@media (max-width: 1180px) {
  .wa-metric-grid {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}

@media (max-width: 640px) {
  .wa-metric-grid {
    grid-template-columns: 1fr;
  }
}
</style>
