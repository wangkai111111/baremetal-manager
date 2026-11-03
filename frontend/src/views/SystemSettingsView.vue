<script setup lang="ts">
// 系统设置页面 — 平台参数与集成配置（仅 admin 可访问）
import { computed, ref, watch } from 'vue'
import {
  NButton,
  NInput,
  NTabs,
  NTabPane,
  NSwitch,
  NDataTable,
  NModal,
  NForm,
  NFormItem,
  useMessage
} from 'naive-ui'
import type { DataTableColumns } from 'naive-ui'
import { h } from 'vue'
import { errorText, resource, rows, type RecordData } from '../api'
import { useAuth } from '../stores/auth'

const message = useMessage()
const auth = useAuth()

// === 设置项分类 ===
type SettingCategory = 'bmc' | 'monitor' | 'alert' | 'sol' | 'pxe'

// === 列表数据状态 ===
const items = ref<RecordData[]>([])
const loading = ref(false)
const saving = ref(false)
const activeTab = ref<SettingCategory>('bmc')

// === 编辑弹窗状态 ===
const editing = ref(false)
const editKey = ref('')
const editValue = ref('')

// === 权限控制：仅 admin 可访问 ===
const isAdmin = computed(() => auth.user?.role === 'admin' || auth.can('settings:write'))

// === 按分类过滤设置项 ===
const categoryPrefixes: Record<SettingCategory, string[]> = {
  bmc: ['bmc.'],
  monitor: ['monitor.', 'scan.'],
  alert: ['alert.', 'notify.'],
  sol: ['sol.'],
  pxe: ['pxe.', 'dhcp.', 'tftp.', 'http.']
}

const filteredItems = computed(() => {
  const prefixes = categoryPrefixes[activeTab.value] || []
  return items.value.filter(item => {
    const key = String(item.key || '')
    return prefixes.some(prefix => key.startsWith(prefix))
  })
})

// === 表格列定义 ===
const columns = computed<DataTableColumns<RecordData>>(() => [
  {
    title: '配置项',
    key: 'key',
    minWidth: 200,
    render: (row: RecordData) =>
      h('div', {}, [
        h('strong', { style: 'font-size: 12px; color: #233946;' }, String(row.key || '—')),
        row.description ? h('div', { style: 'font-size: 11px; color: #95a3ab; margin-top: 2px;' }, String(row.description)) : null
      ])
  },
  {
    title: '配置值',
    key: 'value',
    minWidth: 240,
    render: (row: RecordData) => {
      const val = String(row.value ?? '')
      // 布尔值用开关展示
      if (val === 'true' || val === 'false') {
        return h(NSwitch, {
          value: val === 'true',
          disabled: !isAdmin.value,
          onUpdateValue: (v: boolean) => updateSetting(row, String(v))
        })
      }
      return h('span', { style: 'font-size: 12px; color: #40545e; word-break: break-all;' }, val || '—')
    }
  },
  ...(isAdmin.value
    ? [
        {
          title: '操作',
          key: 'actions',
          width: 80,
          render: (row: RecordData) =>
            h(
              NButton,
              { text: true, size: 'small', onClick: () => openEdit(row) },
              { default: () => '编辑' }
            )
        }
      ]
    : [])
])

// === 加载设置项列表 ===
async function load() {
  loading.value = true
  try {
    const data = rows(await resource.list('/settings', { page: 1, page_size: 200 }))
    items.value = data.items
  } catch (e) {
    message.error(errorText(e))
  } finally {
    loading.value = false
  }
}

watch(
  () => auth.projectId,
  () => load(),
  { immediate: true }
)

// === 打开编辑弹窗 ===
function openEdit(row: RecordData) {
  editKey.value = String(row.key || '')
  editValue.value = String(row.value ?? '')
  editing.value = true
}

// === 更新单个设置项 ===
async function updateSetting(row: RecordData, value: string) {
  try {
    await resource.update('/settings', row.id!, { value })
    message.success('已更新')
    load()
  } catch (e) {
    message.error(errorText(e))
  }
}

// === 保存编辑 ===
async function saveEdit() {
  const item = items.value.find(i => i.key === editKey.value)
  if (!item) return
  saving.value = true
  try {
    await resource.update('/settings', item.id!, { value: editValue.value })
    message.success('已更新')
    editing.value = false
    load()
  } catch (e) {
    message.error(errorText(e))
  } finally {
    saving.value = false
  }
}

// === Tab 分类标签 ===
const tabLabels: Record<SettingCategory, string> = {
  bmc: 'BMC 配置',
  monitor: '监控配置',
  alert: '告警配置',
  sol: 'SOL 配置',
  pxe: 'PXE 配置'
}
</script>

<template>
  <div class="page">
    <div class="page-head">
      <div>
        <div class="eyebrow dark">ADMINISTRATION / SYSTEM SETTINGS</div>
        <h1>系统设置</h1>
        <p>平台参数与集成配置（仅管理员可修改）</p>
      </div>
    </div>
    <div class="panel">
      <n-tabs v-model:value="activeTab" type="line" style="padding: 0 24px;">
        <n-tab-pane v-for="(label, key) in tabLabels" :key="key" :name="key" :tab="label" />
      </n-tabs>
      <div class="panel-toolbar" style="border-top: none;">
        <div>
          <div class="panel-title">
            {{ tabLabels[activeTab] }} <span class="count">{{ filteredItems.length }}</span>
          </div>
          <p class="panel-caption">当前分类下的配置项</p>
        </div>
        <div class="toolbar-actions">
          <n-button quaternary @click="load">刷新</n-button>
        </div>
      </div>
      <n-data-table
        :columns="columns"
        :data="filteredItems"
        :loading="loading"
        :row-key="(row: RecordData) => row.id ?? ''"
        :scroll-x="600"
        :bordered="false"
      />
      <div v-if="!loading && !filteredItems.length" class="empty-hint">
        当前分类下暂无配置项。
      </div>
    </div>
    <!-- 编辑配置项弹窗 -->
    <n-modal
      v-model:show="editing"
      preset="card"
      title="编辑配置项"
      style="width: min(480px, 94vw)"
    >
      <n-form label-placement="top">
        <n-form-item :label="editKey">
          <n-input v-model:value="editValue" placeholder="请输入配置值" />
        </n-form-item>
        <div class="modal-actions">
          <n-button @click="editing = false">取消</n-button>
          <n-button type="primary" :loading="saving" @click="saveEdit">保存</n-button>
        </div>
      </n-form>
    </n-modal>
  </div>
</template>
