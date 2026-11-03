<script setup lang="ts">
// 推送渠道配置页面 — 管理告警通知渠道
import { computed, ref, watch } from 'vue'
import {
  NButton,
  NInput,
  NDataTable,
  NModal,
  NForm,
  NFormItem,
  NSelect,
  NPopconfirm,
  NTag,
  NSwitch,
  useMessage
} from 'naive-ui'
import type { DataTableColumns } from 'naive-ui'
import { h } from 'vue'
import { errorText, resource, rows, type RecordData, api } from '../api'
import { useAuth } from '../stores/auth'

const message = useMessage()
const auth = useAuth()

// === 渠道类型选项 ===
const channelTypes = [
  { label: '企业微信', value: 'wechat' },
  { label: '钉钉', value: 'dingtalk' },
  { label: 'Webhook', value: 'webhook' },
  { label: '邮件', value: 'email' }
]

// === 类型图标映射 ===
const typeIcons: Record<string, string> = {
  wechat: '💬',
  dingtalk: '🔔',
  webhook: '🔗',
  email: '📧'
}

// === 列表数据状态 ===
const items = ref<RecordData[]>([])
const total = ref(0)
const loading = ref(false)
const page = ref(1)
const pageSize = 20

// === 弹窗与表单状态 ===
const editing = ref(false)
const saving = ref(false)
const testing = ref(false)
const currentId = ref<string | number | null>(null)
const form = ref<RecordData>({})

// === 权限控制 ===
const canManage = computed(() => auth.can('alerts:manage'))

// === 表格列定义 ===
const columns = computed<DataTableColumns<RecordData>>(() => [
  {
    title: '渠道名称',
    key: 'name',
    minWidth: 160,
    render: (row: RecordData) =>
      h('span', {}, [
        h('span', { style: 'margin-right: 6px' }, typeIcons[String(row.type)] || '📢'),
        String(row.name || '—')
      ])
  },
  {
    title: '类型',
    key: 'type',
    minWidth: 100,
    render: (row: RecordData) =>
      h(
        NTag,
        { size: 'small', bordered: false },
        { default: () => String(row.type || '—') }
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
          width: 180,
          fixed: 'right' as const,
          render: (row: RecordData) =>
            h('div', { class: 'table-actions' }, [
              h(
                NButton,
                { text: true, type: 'info', onClick: () => testChannel(row), loading: testing.value },
                { default: () => '测试' }
              ),
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
                  default: () => '确认删除此渠道？'
                }
              )
            ])
        }
      ]
    : [])
])

// === 加载推送渠道列表 ===
async function load() {
  loading.value = true
  try {
    const data = rows(await resource.list('/alert-channels', { page: page.value, page_size: pageSize }))
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
function openEdit(row?: RecordData) {
  currentId.value = row?.id ?? null
  form.value = row ? { ...row } : { type: 'wechat', enabled: true }
  editing.value = true
}

// === 切换启用/禁用 ===
async function toggleEnabled(row: RecordData, val: boolean) {
  try {
    await resource.update('/alert-channels', row.id!, { enabled: val })
    message.success(val ? '渠道已启用' : '渠道已禁用')
    load()
  } catch (e) {
    message.error(errorText(e))
  }
}

// === 测试推送 ===
async function testChannel(row: RecordData) {
  testing.value = true
  try {
    await api.post(`/alert-channels/${row.id}/test`)
    message.success('测试消息已发送')
  } catch (e) {
    message.error(errorText(e))
  } finally {
    testing.value = false
  }
}

// === 保存推送渠道 ===
async function save() {
  if (!form.value.name || !form.value.type || !form.value.config) {
    message.warning('请填写必填字段')
    return
  }
  // 验证 JSON 格式
  try {
    JSON.parse(String(form.value.config))
  } catch {
    message.warning('配置内容必须是有效的 JSON 格式')
    return
  }
  saving.value = true
  try {
    const payload = {
      name: form.value.name,
      type: form.value.type,
      config: form.value.config,
      enabled: form.value.enabled ?? true
    }
    if (currentId.value != null) {
      await resource.update('/alert-channels', currentId.value, payload)
    } else {
      await resource.create('/alert-channels', payload)
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

// === 删除推送渠道 ===
async function remove(row: RecordData) {
  try {
    await resource.remove('/alert-channels', row.id!)
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
        <div class="eyebrow dark">MONITORING / ALERT CHANNELS</div>
        <h1>推送渠道</h1>
        <p>配置告警通知渠道与推送方式</p>
      </div>
      <div class="head-actions">
        <n-button v-if="canManage" type="primary" @click="openEdit()">+ 新建渠道</n-button>
      </div>
    </div>
    <div class="panel">
      <div class="panel-toolbar">
        <div>
          <div class="panel-title">渠道列表 <span class="count">{{ total }}</span></div>
          <p class="panel-caption">管理告警推送目标与通知方式</p>
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
        当前没有推送渠道，请新建渠道。
      </div>
    </div>
    <!-- 新建/编辑渠道弹窗 -->
    <n-modal
      v-model:show="editing"
      preset="card"
      :title="currentId != null ? '编辑推送渠道' : '新建推送渠道'"
      style="width: min(560px, 94vw)"
    >
      <n-form label-placement="top">
        <div class="resource-form-grid">
          <n-form-item label="渠道名称" required>
            <n-input v-model:value="(form.name as string)" placeholder="请输入渠道名称" />
          </n-form-item>
          <n-form-item label="渠道类型" required>
            <n-select v-model:value="(form.type as string)" :options="channelTypes" placeholder="请选择类型" />
          </n-form-item>
          <n-form-item label="配置 (JSON)" required class="form-item-wide">
            <n-input
              v-model:value="(form.config as string)"
              type="textarea"
              :rows="6"
              placeholder='请输入 JSON 配置，例如：{"webhook_url": "https://..."}'
            />
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
