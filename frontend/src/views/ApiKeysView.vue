<script setup lang="ts">
// API Key 管理页面 — 管理开放 API 访问密钥
import { computed, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import {
  NButton,
  NInput,
  NDataTable,
  NModal,
  NForm,
  NFormItem,
  NSelect,
  NDatePicker,
  NTag,
  NPopconfirm,
  useMessage
} from 'naive-ui'
import type { DataTableColumns } from 'naive-ui'
import { h } from 'vue'
import { errorText, resource, rows, type RecordData, unwrap } from '../api'
import { useAuth } from '../stores/auth'

const message = useMessage()
const auth = useAuth()
const router = useRouter()

// === 权限选项（多选）===
const permissionOptions = [
  { label: '资产只读', value: 'assets:read' },
  { label: '资产读写', value: 'assets:write' },
  { label: '资产操作', value: 'assets:operate' },
  { label: '镜像只读', value: 'images:read' },
  { label: '镜像读写', value: 'images:write' },
  { label: '任务只读', value: 'tasks:read' },
  { label: '任务读写', value: 'tasks:write' },
  { label: '告警只读', value: 'alerts:read' },
  { label: '告警管理', value: 'alerts:manage' },
  { label: '网络只读', value: 'network:read' },
  { label: '网络读写', value: 'network:write' },
  { label: '固件只读', value: 'firmware:read' },
  { label: '审计只读', value: 'audit:read' },
  { label: '设置只读', value: 'settings:read' }
]

// === 列表数据状态 ===
const items = ref<RecordData[]>([])
const total = ref(0)
const loading = ref(false)
const page = ref(1)
const pageSize = 20

// === 弹窗与表单状态 ===
const creating = ref(false)
const saving = ref(false)
interface NewKeyForm {
  name: string
  permissions: string[]
  expires_at: string | null
}
const newKey = ref<NewKeyForm>({ name: '', permissions: [], expires_at: null })

// === 创建成功后显示的密钥值（仅显示一次）===
const showCreatedKey = ref(false)
const createdKeyValue = ref('')

// === 权限控制 ===
const canWrite = computed(() => auth.can('iam:write'))

// === 表格列定义 ===
const columns = computed<DataTableColumns<RecordData>>(() => [
  { title: '密钥名称', key: 'name', minWidth: 140 },
  {
    title: '权限范围',
    key: 'permissions',
    minWidth: 180,
    render: (row: RecordData) => {
      const perms = (row.permissions as string[]) || []
      return h('div', { style: 'display: flex; flex-wrap: wrap; gap: 4px;' },
        perms.slice(0, 3).map(p => h(NTag, { size: 'tiny', bordered: false, type: 'info' }, { default: () => p }))
          .concat(perms.length > 3 ? [h(NTag, { size: 'tiny', bordered: false }, { default: () => `+${perms.length - 3}` })] : [])
      )
    }
  },
  { title: '过期时间', key: 'expires_at', minWidth: 150, render: (row: RecordData) => String(row.expires_at || '永不过期') },
  { title: '最后使用', key: 'last_used_at', minWidth: 150, render: (row: RecordData) => String(row.last_used_at || '从未使用') },
  {
    title: '状态',
    key: 'status',
    minWidth: 80,
    render: (row: RecordData) => {
      const status = String(row.status || 'active')
      return h(
        NTag,
        { size: 'small', bordered: false, type: status === 'active' ? 'success' : 'default' },
        { default: () => status === 'active' ? '有效' : '已吊销' }
      )
    }
  },
  { title: '创建时间', key: 'created_at', minWidth: 150 },
  ...(canWrite.value
    ? [
        {
          title: '操作',
          key: 'actions',
          width: 80,
          fixed: 'right' as const,
          render: (row: RecordData) =>
            h('div', { class: 'table-actions' }, [
              row.status !== 'revoked'
                ? h(
                    NPopconfirm,
                    { onPositiveClick: () => revoke(row) },
                    {
                      trigger: () =>
                        h(NButton, { text: true, type: 'error', size: 'small' }, { default: () => '吊销' }),
                      default: () => '确认吊销此 API Key？吊销后不可恢复。'
                    }
                  )
                : null
            ])
        }
      ]
    : [])
])

// === 加载 API Key 列表 ===
async function load() {
  loading.value = true
  try {
    const data = rows(await resource.list('/api-keys', { page: page.value, page_size: pageSize }))
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

// === 打开创建弹窗 ===
function openCreate() {
  newKey.value = { name: '', permissions: [], expires_at: null }
  creating.value = true
}

// === 创建 API Key ===
async function createKey() {
  if (!newKey.value.name) {
    message.warning('请填写密钥名称')
    return
  }
  saving.value = true
  try {
    const payload: RecordData = {
      name: newKey.value.name,
      permissions: newKey.value.permissions || []
    }
    if (newKey.value.expires_at) {
      const parsed = new Date(String(newKey.value.expires_at).replace(' ', 'T'))
      if (!Number.isNaN(parsed.getTime())) payload.expires_at = parsed.toISOString()
    }
    const result = unwrap<{ key?: string }>(await resource.create('/api-keys', payload))
    message.success('API Key 创建成功')
    creating.value = false
    // 显示密钥值（仅一次）
    if (result.key) {
      createdKeyValue.value = result.key
      showCreatedKey.value = true
    }
    load()
  } catch (e) {
    message.error(errorText(e))
  } finally {
    saving.value = false
  }
}

// === 吊销 API Key ===
async function revoke(row: RecordData) {
  try {
    await resource.remove('/api-keys', row.id!)
    message.success('API Key 已吊销')
    load()
  } catch (e) {
    message.error(errorText(e))
  }
}

// === 复制密钥到剪贴板 ===
async function copyKey() {
  try {
    await navigator.clipboard.writeText(createdKeyValue.value)
    message.success('已复制到剪贴板')
  } catch {
    message.warning('复制失败，请手动复制')
  }
}
</script>

<template>
  <div class="page">
    <div class="page-head">
      <div>
        <div class="eyebrow dark">ACCESS CONTROL / API KEYS</div>
        <h1>API 密钥</h1>
        <p>管理开放 API 访问密钥与权限范围</p>
      </div>
      <div class="head-actions">
        <n-button quaternary @click="router.push('/api-docs')">API 文档</n-button>
        <n-button v-if="canWrite" type="primary" @click="openCreate()">+ 创建密钥</n-button>
      </div>
    </div>
    <div class="panel">
      <div class="panel-toolbar">
        <div>
          <div class="panel-title">密钥列表 <span class="count">{{ total }}</span></div>
          <p class="panel-caption">管理 API 访问密钥与权限</p>
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
        当前没有 API Key，请创建密钥。
      </div>
    </div>
    <!-- 创建密钥弹窗 -->
    <n-modal
      v-model:show="creating"
      preset="card"
      title="创建 API 密钥"
      style="width: min(520px, 94vw)"
    >
      <n-form label-placement="top">
        <n-form-item label="密钥名称" required>
          <n-input v-model:value="newKey.name" placeholder="请输入密钥名称" />
        </n-form-item>
        <n-form-item label="权限范围">
          <n-select
            v-model:value="newKey.permissions"
            :options="permissionOptions"
            multiple
            placeholder="请选择权限（可多选）"
          />
        </n-form-item>
        <n-form-item label="过期时间">
          <n-date-picker
            v-model:formatted-value="newKey.expires_at"
            type="datetime"
            value-format="yyyy-MM-dd HH:mm:ss"
            style="width: 100%"
            placeholder="留空表示永不过期"
          />
        </n-form-item>
        <div class="modal-actions">
          <n-button @click="creating = false">取消</n-button>
          <n-button type="primary" :loading="saving" @click="createKey">创建</n-button>
        </div>
      </n-form>
    </n-modal>
    <!-- 密钥创建成功弹窗（仅显示一次）=== -->
    <n-modal
      v-model:show="showCreatedKey"
      preset="card"
      title="⚠️ 请立即保存密钥"
      style="width: min(520px, 94vw)"
    >
      <div class="key-display">
        <p class="key-warning">此密钥仅显示一次，关闭后将无法再次查看。请立即复制保存。</p>
        <div class="key-value-box">
          <code>{{ createdKeyValue }}</code>
        </div>
        <div class="modal-actions">
          <n-button @click="copyKey">复制到剪贴板</n-button>
          <n-button type="primary" @click="showCreatedKey = false">我已保存，关闭</n-button>
        </div>
      </div>
    </n-modal>
  </div>
</template>

<style scoped>
/* 密钥展示样式 */
.key-display {
  padding: 4px 0;
}
.key-warning {
  font-size: 13px;
  color: #d36b65;
  font-weight: 600;
  margin: 0 0 14px;
}
.key-value-box {
  background: #f4f6f6;
  border: 1px solid #e4e9ea;
  border-radius: 6px;
  padding: 14px 16px;
  margin-bottom: 16px;
  word-break: break-all;
}
.key-value-box code {
  font-family: 'SF Mono', 'Fira Code', monospace;
  font-size: 13px;
  color: #20313d;
  line-height: 1.6;
}
</style>
