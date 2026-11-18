<script setup lang="ts">
// 用户管理页面（增强版）— 用户账户与访问控制
import { computed, ref, watch } from 'vue'
import dayjs from 'dayjs'
const formatTime = (value: unknown) => value && dayjs(String(value)).isValid() ? dayjs(String(value)).format('YYYY-MM-DD HH:mm') : '—'
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
  useMessage
} from 'naive-ui'
import type { DataTableColumns } from 'naive-ui'
import { h } from 'vue'
import { api, errorText, resource, rows, unwrap, type RecordData } from '../api'
import { useAuth } from '../stores/auth'
import { flattenDepartments } from '../utils/departments'
import UserAccessEditor from '../components/UserAccessEditor.vue'
const accessOpen = ref(false)
const accessUser = ref<RecordData>({})
function openAccess(row: RecordData) { accessUser.value = row; accessOpen.value = true }

const message = useMessage()
const auth = useAuth()

// === 平台角色选项（后端仅支持 admin / user）===
const roleOptions = [
  { label: '平台管理员（全部组织）', value: 'admin' },
  { label: '普通用户', value: 'user' }
]

// === 列表数据状态 ===
const items = ref<RecordData[]>([])
const total = ref(0)
const departmentNames = ref<{id: number; name: string}[]>([])
const loading = ref(false)
const search = ref('')
const page = ref(1)
const pageSize = 20

// === 弹窗与表单状态 ===
const editing = ref(false)
const creating = ref(false)
const saving = ref(false)
const currentId = ref<string | number | null>(null)
const form = ref<RecordData>({})
const newUser = ref<{ username: string; password: string; role: string }>({
  username: '',
  password: '',
  role: 'user'
})

// === 权限控制 ===
const canWrite = computed(() => auth.user?.is_platform_admin === true)

// === 表格列定义 ===
const columns = computed<DataTableColumns<RecordData>>(() => [
  { title: '用户名', key: 'username', minWidth: 120 },
  { title: '姓名', key: 'name', minWidth: 100 },
  { title: '邮箱', key: 'email', minWidth: 160 },
  {
    title: '平台角色',
    key: 'role',
    minWidth: 100,
    render: (row: RecordData) => {
      const role = String(row.role || 'viewer')
      const label = roleOptions.find(o => o.value === role)?.label || role
      return h(
        NTag,
        { size: 'small', bordered: false },
        { default: () => label }
      )
    }
  },
  {
    title: '状态',
    key: 'status',
    minWidth: 80,
    render: (row: RecordData) => {
      const status = String(row.status || 'active')
      return h(
        NTag,
        { size: 'small', bordered: false, type: status === 'active' ? 'success' : 'error' },
        { default: () => status === 'active' ? '正常' : '禁用' }
      )
    }
  },
  { title: '当前组织授权', key: 'access_grants', minWidth: 220, render: row => {
    if (row.is_platform_admin === true) return '全部组织 / 全部服务器'
    const grants = Array.isArray(row.access_grants) ? row.access_grants as RecordData[] : []
    if (!grants.length) return h('span', { style: 'color: var(--color-text-tertiary)' }, '未授权')
    return h('div', { style: 'display:flex; flex-direction:column; gap:4px' }, grants.map(g => h('span', {},
      `${g.department_id == null ? '整个组织' : departmentNames.value.find(d => d.id === Number(g.department_id))?.name || `部门 #${g.department_id}`} · ${g.is_admin ? '管理员' : '普通用户'}`)))
  } },
  { title: '最后登录', key: 'last_login_at', minWidth: 150, render: (row: RecordData) => formatTime(row.last_login_at) },
  { title: '创建时间', key: 'created_at', minWidth: 150, render: (row: RecordData) => formatTime(row.created_at) },
  ...(canWrite.value || auth.can('iam:manage')
    ? [
        {
          title: '操作',
          key: 'actions',
          width: 210,
          fixed: 'right' as const,
          render: (row: RecordData) =>
            h('div', { class: 'table-actions' }, [
              row.is_platform_admin !== true && (auth.user?.is_platform_admin || String(row.id) !== String(auth.user?.id)) ? h(NButton, {text:true, type:'primary', onClick: () => openAccess(row)}, {default: () => '访问授权'}) : null,
              ...(canWrite.value ? [
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
                  default: () => '确认删除此用户？'
                }
              )
            ] : [])])
        }
      ]
    : [])
])

// === 加载用户列表 ===
async function load() {
  loading.value = true
  try {
    const [response, tree] = await Promise.all([resource.list('/users', { page: page.value, page_size: pageSize, search: search.value }), resource.list('/departments')])
    const data = rows(response)
    departmentNames.value = flattenDepartments(rows(tree).items)
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
    search.value = ''
    load()
  },
  { immediate: true }
)

// === 打开编辑弹窗 ===
function openEdit(row: RecordData) {
  currentId.value = row.id ?? null
  form.value = { ...row }
  editing.value = true
}

// === 打开新增用户弹窗 ===
function openCreate() {
  newUser.value = { username: '', password: '', role: 'user' }
  creating.value = true
}

// === 创建新用户 ===
async function saveNewUser() {
  if (!newUser.value.username.trim()) {
    message.warning('请输入用户名')
    return
  }
  if (newUser.value.password.length < 6) {
    message.warning('密码长度不能少于 6 位')
    return
  }
  saving.value = true
  try {
    const response = await api.post('/auth/users', {
      username: newUser.value.username.trim(),
      password: newUser.value.password,
      role: newUser.value.role
    })
    message.success('用户已创建')
    creating.value = false
    if (newUser.value.role === 'user') openAccess({id: unwrap<RecordData>(response).id, username: newUser.value.username})
    page.value = 1
    load()
  } catch (e) {
    message.error(errorText(e))
  } finally {
    saving.value = false
  }
}

// === 保存用户信息 ===
async function save() {
  saving.value = true
  try {
    const payload = {
      name: form.value.name || '',
      email: form.value.email || '',
      role: form.value.role || 'user'
    }
    await resource.update('/users', currentId.value!, payload)
    message.success('保存成功')
    editing.value = false
    load()
  } catch (e) {
    message.error(errorText(e))
  } finally {
    saving.value = false
  }
}

// === 删除用户 ===
async function remove(row: RecordData) {
  try {
    await resource.remove('/users', row.id!)
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
        <div class="eyebrow dark">ACCESS CONTROL / USERS</div>
        <h1>用户管理</h1>
        <p>管理用户账户；通过访问授权指定可访问的组织、部门和范围管理员</p>
      </div>
      <div class="head-actions">
        <n-button v-if="canWrite" type="primary" @click="openCreate">+ 新增用户</n-button>
      </div>
    </div>
    <div class="panel">
      <div class="panel-toolbar">
        <div>
          <div class="panel-title">用户列表 <span class="count">{{ total }}</span></div>
          <p class="panel-caption">查看和编辑用户信息</p>
        </div>
        <div class="toolbar-actions">
          <n-input
            v-model:value="search"
            placeholder="搜索用户..."
            clearable
            style="width: 220px"
            @keyup.enter="page = 1; load()"
            @clear="page = 1; load()"
          />
          <n-button @click="page = 1; load()">查询</n-button>
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
        当前没有用户数据。
      </div>
    </div>
    <!-- 编辑用户弹窗 -->
    <n-modal
      v-model:show="editing"
      preset="card"
      title="编辑用户"
      style="width: min(480px, 94vw)"
    >
      <n-form label-placement="top">
        <n-form-item label="用户名">
          <n-input :value="String(form.username || '')" disabled />
        </n-form-item>
        <n-form-item label="姓名">
          <n-input v-model:value="(form.name as string)" placeholder="请输入姓名" />
        </n-form-item>
        <n-form-item label="邮箱">
          <n-input v-model:value="(form.email as string)" placeholder="请输入邮箱" />
        </n-form-item>
        <n-form-item label="角色">
          <n-select v-model:value="(form.role as string)" :options="roleOptions" />
        </n-form-item>
        <div class="modal-actions">
          <n-button @click="editing = false">取消</n-button>
          <n-button type="primary" :loading="saving" @click="save">保存</n-button>
        </div>
      </n-form>
    </n-modal>
    <!-- 新增用户弹窗 -->
    <n-modal
      v-model:show="creating"
      preset="card"
      title="新增用户"
      style="width: min(460px, 94vw)"
    >
      <n-form label-placement="top">
        <n-form-item label="用户名" required>
          <n-input v-model:value="newUser.username" placeholder="请输入登录用户名" />
        </n-form-item>
        <n-form-item label="初始密码" required>
          <n-input
            v-model:value="newUser.password"
            type="password"
            show-password-on="click"
            placeholder="6–72 字节"
          />
        </n-form-item>
        <n-form-item label="平台角色">
          <n-select v-model:value="newUser.role" :options="roleOptions" />
        </n-form-item>
        <div class="modal-actions">
          <n-button @click="creating = false">取消</n-button>
          <n-button type="primary" :loading="saving" @click="saveNewUser">创建</n-button>
        </div>
      </n-form>
    </n-modal>
    <UserAccessEditor v-model:show="accessOpen" :user-id="accessUser.id" :username="String(accessUser.username || '')" @saved="load" />
  </div>
</template>
