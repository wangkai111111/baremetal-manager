<script setup lang="ts">
// 项目成员管理页面 — 管理项目成员与权限分配
import { computed, ref, watch } from 'vue'
import {
  NButton,
  NDataTable,
  NModal,
  NForm,
  NFormItem,
  NSelect,
  NPopconfirm,
  NTag,
  useMessage
} from 'naive-ui'
import type { DataTableColumns } from 'naive-ui'
import { h } from 'vue'
import { errorText, resource, rows, type RecordData, api } from '../api'
import { useAuth } from '../stores/auth'

const message = useMessage()
const auth = useAuth()

// === 角色选项 ===
const roleOptions = [
  { label: '管理员', value: 'admin' },
  { label: '运维', value: 'operator' },
  { label: '只读', value: 'viewer' }
]

// === 列表数据状态 ===
const items = ref<RecordData[]>([])
const total = ref(0)
const loading = ref(false)

// === 弹窗与表单状态 ===
const adding = ref(false)
const saving = ref(false)
const newMember = ref<RecordData>({})
const isEditing = ref(false)

// === 用户选项列表（用于添加成员下拉）===
const userOptions = ref<{ label: string; value: string | number }[]>([])

// === 权限控制 ===
const canWrite = computed(() => auth.can('projects:write'))

// === 表格列定义 ===
const columns = computed<DataTableColumns<RecordData>>(() => [
  { title: '用户名', key: 'username', minWidth: 140, render: (row: RecordData) => String(row.username || row.user_id || '—') },
  {
    title: '角色',
    key: 'role',
    minWidth: 120,
    render: (row: RecordData) => {
      const role = String(row.role || 'viewer')
      const label = roleOptions.find(o => o.value === role)?.label || role
      const tagType = role === 'admin' ? 'error' : role === 'operator' ? 'info' : 'default'
      return h(
        NTag,
        { size: 'small', bordered: false, type: tagType },
        { default: () => label }
      )
    }
  },
  { title: '加入时间', key: 'created_at', minWidth: 150 },
  ...(canWrite.value
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
                { text: true, onClick: () => openChangeRole(row) },
                { default: () => '修改角色' }
              ),
              h(
                NPopconfirm,
                { onPositiveClick: () => removeMember(row) },
                {
                  trigger: () =>
                    h(NButton, { text: true, type: 'error' }, { default: () => '移除' }),
                  default: () => '确认移除此成员？'
                }
              )
            ])
        }
      ]
    : [])
])

// === 加载成员列表 ===
async function load() {
  if (!auth.projectId) return
  loading.value = true
  try {
    const data = rows(await api.get(`/projects/${auth.projectId}/members`))
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
    load()
  },
  { immediate: true }
)

// === 加载用户列表（用于添加成员）===
async function loadUsers() {
  try {
    const data = rows(await resource.list('/users', { page: 1, page_size: 200 }))
    userOptions.value = data.items.map(u => ({
      label: `${String(u.username || u.id)} (${String(u.name || '')})`,
      value: u.id as string | number
    }))
  } catch {
    // 加载用户列表失败，忽略
  }
}

// === 打开添加成员弹窗 ===
function openAdd() {
  newMember.value = { role: 'viewer' }
  isEditing.value = false
  loadUsers()
  adding.value = true
}

// === 打开修改角色弹窗 ===
function openChangeRole(row: RecordData) {
  newMember.value = { ...row }
  isEditing.value = true
  adding.value = true
}

// === 添加成员 ===
async function addMember() {
  if (!newMember.value.user_id) {
    message.warning('请选择用户')
    return
  }
  saving.value = true
  try {
    await api.post(`/projects/${auth.projectId}/members`, {
      user_id: newMember.value.user_id,
      role: newMember.value.role || 'viewer'
    })
    message.success('成员已添加')
    adding.value = false
    load()
  } catch (e) {
    message.error(errorText(e))
  } finally {
    saving.value = false
  }
}

// === 修改成员角色 ===
async function changeRole() {
  if (!newMember.value.user_id) return
  saving.value = true
  try {
    await api.put(`/projects/${auth.projectId}/members/${newMember.value.user_id}`, {
      role: newMember.value.role
    })
    message.success('角色已更新')
    adding.value = false
    load()
  } catch (e) {
    message.error(errorText(e))
  } finally {
    saving.value = false
  }
}

// === 移除成员 ===
async function removeMember(row: RecordData) {
  try {
    await api.delete(`/projects/${auth.projectId}/members/${row.user_id || row.id}`)
    message.success('已移除')
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
        <div class="eyebrow dark">PROJECT / MEMBERS</div>
        <h1>项目成员</h1>
        <p>管理项目成员与权限分配</p>
      </div>
      <div class="head-actions">
        <n-button v-if="canWrite" type="primary" @click="openAdd()">+ 添加成员</n-button>
      </div>
    </div>
    <div class="panel">
      <div class="panel-toolbar">
        <div>
          <div class="panel-title">成员列表 <span class="count">{{ total }}</span></div>
          <p class="panel-caption">查看和管理当前项目的成员</p>
        </div>
        <div class="toolbar-actions">
          <n-button quaternary @click="load">刷新</n-button>
        </div>
      </div>
      <n-data-table
        :columns="columns"
        :data="items"
        :loading="loading"
        :row-key="(row: RecordData) => (row.user_id ?? row.id ?? '') as string | number"
        :scroll-x="Math.max(columns.length * 145, 600)"
      />
      <div v-if="!loading && !items.length" class="empty-hint">
        当前项目暂无成员，请添加成员。
      </div>
    </div>
    <!-- 添加成员/修改角色弹窗 -->
    <n-modal
      v-model:show="adding"
      preset="card"
                  :title="isEditing ? '修改成员角色' : '添加成员'"
      style="width: min(440px, 94vw)"
    >
      <n-form label-placement="top">
                <n-form-item v-if="!isEditing" label="选择用户" required>
          <n-select
            v-model:value="(newMember.user_id as string | number)"
            :options="userOptions"
            filterable
            placeholder="请选择要添加的用户"
          />
        </n-form-item>
        <n-form-item label="角色">
          <n-select
            v-model:value="(newMember.role as string)"
            :options="roleOptions"
          />
        </n-form-item>
        <div class="modal-actions">
          <n-button @click="adding = false">取消</n-button>
          <n-button
            type="primary"
            :loading="saving"
            @click="isEditing ? changeRole() : addMember()"
          >
            {{ isEditing ? '保存' : '添加' }}
          </n-button>
        </div>
      </n-form>
    </n-modal>
  </div>
</template>
