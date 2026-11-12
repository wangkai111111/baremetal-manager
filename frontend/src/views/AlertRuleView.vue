<script setup lang="ts">
// 告警规则配置页面 — 自定义监控阈值与告警策略
import { computed, ref, watch } from 'vue'
import {
  NButton,
  NInput,
  NDataTable,
  NModal,
  NForm,
  NFormItem,
  NSelect,
  NInputNumber,
  NPopconfirm,
  NTag,
  NSwitch,
  useMessage
} from 'naive-ui'
import type { DataTableColumns } from 'naive-ui'
import { h } from 'vue'
import { errorText, resource, rows, type RecordData } from '../api'
import { useAuth } from '../stores/auth'

const message = useMessage()
const auth = useAuth()

// === 等级选项 ===
const severityOptions = [
  { label: 'INFO', value: 'info' },
  { label: 'WARNING', value: 'warning' },
  { label: 'CRITICAL', value: 'critical' }
]

// === 运算符选项 ===
const operatorOptions = [
  { label: '大于 (>)', value: 'gt' },
  { label: '大于等于 (>=)', value: 'gte' },
  { label: '小于 (<)', value: 'lt' },
  { label: '小于等于 (<=)', value: 'lte' },
  { label: '等于 (=)', value: 'eq' },
  { label: '连续采集失败', value: 'collection_failed' }
]
const channels = ref<RecordData[]>([])
const channelLinks = ref<RecordData[]>([])
const selectedChannels = ref<number[]>([])

// === 列表数据状态 ===
const items = ref<RecordData[]>([])
const total = ref(0)
const loading = ref(false)
const page = ref(1)
const pageSize = 20

// === 弹窗与表单状态 ===
const editing = ref(false)
const saving = ref(false)
const currentId = ref<string | number | null>(null)
const form = ref<RecordData>({})

// === 权限控制 ===
const canManage = computed(() => auth.can('alerts:write'))

// === 等级标签颜色映射 ===
function severityTagType(severity: unknown): 'info' | 'warning' | 'error' | 'default' {
  const s = String(severity || '').toLowerCase()
  return s === 'critical' ? 'error' : s === 'warning' ? 'warning' : s === 'info' ? 'info' : 'default'
}

// === 表格列定义 ===
const columns = computed<DataTableColumns<RecordData>>(() => [
  { title: '规则名称', key: 'name', minWidth: 140 },
  { title: '监控指标', key: 'metric', minWidth: 120 },
  { title: '运算符', key: 'operator', minWidth: 80 },
  { title: '阈值', key: 'threshold', minWidth: 80 },
  {
    title: '等级',
    key: 'severity',
    minWidth: 100,
    render: (row: RecordData) =>
      h(
        NTag,
        { size: 'small', bordered: false, type: severityTagType(row.severity) },
        { default: () => String(row.severity || '—').toUpperCase() }
      )
  },
  {
    title: '启用状态',
    key: 'enabled',
    minWidth: 100,
    render: (row: RecordData) =>
      canManage.value
        ? h(NSwitch, {
            value: !!row.enabled,
            onUpdateValue: (val: boolean) => toggleEnabled(row, val)
          })
        : h(
            NTag,
            { size: 'small', bordered: false, type: row.enabled ? 'success' : 'default' },
            { default: () => (row.enabled ? '已启用' : '已禁用') }
          )
  },
  { title: '创建时间', key: 'created_at', minWidth: 150 },
  ...(canManage.value
    ? [
        {
          title: '操作',
          key: 'actions',
          width: 145,
          fixed: 'right' as const,
          render: (row: RecordData) =>
            h('div', { class: 'table-actions' }, [
              h(
                NButton,
                { text: true, onClick: () => openEdit(row) },
                { default: () => '编辑' }
              ),
              h(
                NPopconfirm,
                { onPositiveClick: () => remove(row) },
                {
                  trigger: () =>
                    h(NButton, { text: true, type: 'error' }, { default: () => '删除' }),
                  default: () => '确认删除此规则？'
                }
              )
            ])
        }
      ]
    : [])
])

// === 加载告警规则列表 ===
async function load() {
  loading.value = true
  try {
    const data = rows(await resource.list('/alert-rules', { page: page.value, page_size: pageSize }))
    items.value = data.items
    total.value = data.total
  } catch (e) {
    message.error(errorText(e))
  } finally {
    loading.value = false
  }
}

watch(
  () => auth.projectId,
  () => {
    page.value = 1
    load()
  },
  { immediate: true }
)

// === 打开新建/编辑弹窗 ===
async function openEdit(row?: RecordData) {
  try {
    channels.value = rows(await resource.list('/alert-channels', { page_size: 200 })).items
    channelLinks.value = rows(await resource.list('/alert-rule-channels', { page_size: 200, rule_id: row?.id || 0 })).items.filter(link => String(link.rule_id) === String(row?.id))
    selectedChannels.value = channelLinks.value.map(link => Number(link.channel_id))
    currentId.value = row?.id ?? null
    form.value = row ? { ...row } : { severity: 'warning', operator: 'collection_failed', metric: 'bmc.collection_failed', duration_seconds: 0 }
    editing.value = true
  } catch (e) { message.error(errorText(e)) }
}

// === 切换启用/禁用状态 ===
async function toggleEnabled(row: RecordData, val: boolean) {
  try {
    await resource.update('/alert-rules', row.id!, { enabled: val })
    message.success(val ? '规则已启用' : '规则已禁用')
    load()
  } catch (e) {
    message.error(errorText(e))
  }
}

// === 保存告警规则 ===
async function save() {
  if (!form.value.name || !form.value.metric || (form.value.operator !== 'collection_failed' && form.value.threshold == null)) {
    message.warning('请填写必填字段')
    return
  }
  saving.value = true
  try {
    const payload = {
      name: form.value.name,
      metric: form.value.metric,
      operator: form.value.operator,
      threshold: form.value.operator === 'collection_failed' ? null : form.value.threshold,
      severity: form.value.severity || 'warning',
      duration_seconds: form.value.duration_seconds || 0,
      enabled: form.value.enabled ?? true
    }
    let ruleId = currentId.value
    if (ruleId != null) {
      await resource.update('/alert-rules', ruleId, payload)
    } else {
      const result = await resource.create('/alert-rules', payload)
      ruleId = result?.data?.data?.id
      currentId.value = ruleId
    }
    if (ruleId == null) throw new Error('规则已保存但未返回 ID，请刷新后重新关联渠道')
    for (const link of channelLinks.value) {
      if (!selectedChannels.value.includes(Number(link.channel_id))) await resource.remove('/alert-rule-channels', link.id!)
    }
    for (const channelId of selectedChannels.value) {
      if (!channelLinks.value.some(link => Number(link.channel_id) === channelId)) await resource.create('/alert-rule-channels', { rule_id: Number(ruleId), channel_id: channelId })
    }
    message.success('保存成功')
    editing.value = false
    load()
  } catch (e) {
    message.error(errorText(e))
  } finally {
    saving.value = false
  }
}

// === 删除告警规则 ===
async function remove(row: RecordData) {
  try {
    await resource.remove('/alert-rules', row.id!)
    message.success('已删除')
    load()
  } catch (e) {
    message.error(errorText(e))
  }
}
</script>

<template>
  <div class="page">
    <div class="page-head">
      <div>
        <div class="eyebrow dark">MONITORING / ALERT RULES</div>
        <h1>告警规则</h1>
        <p>自定义监控阈值与告警策略</p>
      </div>
      <div class="head-actions">
        <n-button v-if="canManage" type="primary" @click="openEdit()">+ 新建规则</n-button>
      </div>
    </div>
    <div class="panel">
      <div class="panel-toolbar">
        <div>
          <div class="panel-title">规则列表 <span class="count">{{ total }}</span></div>
          <p class="panel-caption">配置和管理告警触发规则</p>
        </div>
        <div class="toolbar-actions">
          <n-button quaternary @click="load">刷新</n-button>
        </div>
      </div>
      <n-data-table
        remote
        :columns="columns"
        :data="items"
        :loading="loading"
        :row-key="(row: RecordData) => row.id ?? ''"
        :scroll-x="Math.max(columns.length * 145, 600)"
        :pagination="{
          page,
          pageSize,
          itemCount: total,
          showSizePicker: false,
          onUpdatePage: (p: number) => { page = p; load() }
        }"
      />
      <div v-if="!loading && !items.length" class="empty-hint">
        当前没有告警规则，请新建规则。
      </div>
    </div>
    <!-- 新建/编辑规则弹窗 -->
    <n-modal
      v-model:show="editing"
      preset="card"
      :title="currentId != null ? '编辑告警规则' : '新建告警规则'"
      style="width: min(560px, 94vw)"
    >
      <n-form label-placement="top">
        <div class="resource-form-grid">
          <n-form-item label="规则名称" required>
            <n-input v-model:value="(form.name as string)" placeholder="请输入规则名称" />
          </n-form-item>
          <n-form-item label="监控指标" required>
            <n-input v-model:value="(form.metric as string)" placeholder="bmc.collection_failed 或 bmc.health" />
          </n-form-item>
          <n-form-item label="运算符">
            <n-select v-model:value="(form.operator as string)" :options="operatorOptions" />
          </n-form-item>
          <n-form-item v-if="form.operator !== 'collection_failed'" label="阈值（健康：警告=1，严重=2）" required>
            <n-input-number v-model:value="(form.threshold as number)" style="width: 100%" placeholder="请输入阈值" />
          </n-form-item>
          <n-form-item label="告警等级">
            <n-select v-model:value="(form.severity as string)" :options="severityOptions" />
          </n-form-item>
          <n-form-item label="持续时间（内置监控填写 0 秒）">
            <n-input-number v-model:value="(form.duration_seconds as number)" :min="0" :max="0" style="width: 100%" />
          </n-form-item>
          <n-form-item label="通知渠道" class="form-item-wide">
            <n-select v-model:value="selectedChannels" multiple :options="channels.map(channel => ({ label: String(channel.name), value: Number(channel.id) }))" />
          </n-form-item>
        </div>
        <div class="modal-actions">
          <n-button @click="editing = false">取消</n-button>
          <n-button type="primary" :loading="saving" @click="save">保存</n-button>
        </div>
      </n-form>
    </n-modal>
  </div>
</template>
