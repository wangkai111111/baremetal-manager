<script setup lang="ts">
// 机房管理页面 — 管理物理站点与容量信息
import { computed, ref, watch } from 'vue'
import {
  NButton,
  NInput,
  NDataTable,
  NModal,
  NForm,
  NFormItem,
  NInputNumber,
  NPopconfirm,
  useMessage
} from 'naive-ui'
import type { DataTableColumns } from 'naive-ui'
import { h } from 'vue'
import { errorText, resource, rows, type RecordData } from '../api'
import { useAuth } from '../stores/auth'

const message = useMessage()
const auth = useAuth()

// === 列表数据状态 ===
const items = ref<RecordData[]>([])
const total = ref(0)
const loading = ref(false)
const search = ref('')
const page = ref(1)
const pageSize = 20

// === 弹窗与表单状态 ===
const editing = ref(false)
const saving = ref(false)
const currentId = ref<string | number | null>(null)
const form = ref<RecordData>({})

// === 权限控制：仅 assets:write 可操作 ===
const canWrite = computed(() => auth.can('assets:write'))

// === 表格列定义 ===
const columns = computed<DataTableColumns<RecordData>>(() => [
  { title: '机房名称', key: 'name', minWidth: 140 },
  { title: '编码', key: 'code', minWidth: 100 },
  { title: '位置', key: 'location', minWidth: 140 },
  { title: '联系人', key: 'contact', minWidth: 120 },
  { title: '容量', key: 'capacity', minWidth: 80 },
  { title: '描述', key: 'description', minWidth: 180, ellipsis: { tooltip: true } },
  { title: '创建时间', key: 'created_at', minWidth: 150 },
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
                { text: true, onClick: () => openEdit(row) },
                { default: () => '编辑' }
              ),
              h(
                NPopconfirm,
                { onPositiveClick: () => remove(row) },
                {
                  trigger: () =>
                    h(NButton, { text: true, type: 'error' }, { default: () => '删除' }),
                  default: () => '确认删除此机房？'
                }
              )
            ])
        }
      ]
    : [])
])

// === 加载机房列表（后端分页）===
async function load() {
  loading.value = true
  try {
    const data = rows(await resource.list('/datacenters', { page: page.value, page_size: pageSize, search: search.value }))
    items.value = data.items
    total.value = data.total
  } catch (e) {
    message.error(errorText(e))
  } finally {
    loading.value = false
  }
}

// === 监听搜索和分页变化 ===
watch(
  () => auth.projectId,
  () => {
    page.value = 1
    search.value = ''
    load()
  },
  { immediate: true }
)

// === 打开新建/编辑弹窗 ===
function openEdit(row?: RecordData) {
  currentId.value = row?.id ?? null
  form.value = row ? { ...row } : {}
  editing.value = true
}

// === 保存机房数据 ===
async function save() {
  if (!form.value.name) {
    message.warning('请填写机房名称')
    return
  }
  saving.value = true
  try {
    const payload = {
      name: form.value.name,
      code: form.value.code || '',
      location: form.value.location || '',
      description: form.value.description || '',
      contact: form.value.contact || '',
      capacity: form.value.capacity || 0
    }
    if (currentId.value != null) {
      await resource.update('/datacenters', currentId.value, payload)
    } else {
      await resource.create('/datacenters', payload)
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

// === 删除机房 ===
async function remove(row: RecordData) {
  try {
    await resource.remove('/datacenters', row.id!)
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
        <div class="eyebrow dark">INFRASTRUCTURE / DATACENTERS</div>
        <h1>机房管理</h1>
        <p>管理物理站点与机房容量信息</p>
      </div>
      <div class="head-actions">
        <n-button v-if="canWrite" type="primary" @click="openEdit()">+ 新建机房</n-button>
      </div>
    </div>
    <div class="panel">
      <div class="panel-toolbar">
        <div>
          <div class="panel-title">机房列表 <span class="count">{{ total }}</span></div>
          <p class="panel-caption">查看和管理所有机房信息</p>
        </div>
        <div class="toolbar-actions">
          <n-input
            v-model:value="search"
            placeholder="搜索机房..."
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
        当前没有机房数据，请新建机房。
      </div>
    </div>
    <!-- 新建/编辑机房弹窗 -->
    <n-modal
      v-model:show="editing"
      preset="card"
      :title="currentId != null ? '编辑机房' : '新建机房'"
      style="width: min(560px, 94vw)"
    >
      <n-form label-placement="top">
        <div class="resource-form-grid">
          <n-form-item label="机房名称" required>
            <n-input v-model:value="(form.name as string)" placeholder="请输入机房名称" />
          </n-form-item>
          <n-form-item label="编码">
            <n-input v-model:value="(form.code as string)" placeholder="请输入机房编码" />
          </n-form-item>
          <n-form-item label="位置">
            <n-input v-model:value="(form.location as string)" placeholder="请输入机房位置" />
          </n-form-item>
          <n-form-item label="联系人">
            <n-input v-model:value="(form.contact as string)" placeholder="请输入联系人" />
          </n-form-item>
          <n-form-item label="容量（机柜数）">
            <n-input-number v-model:value="(form.capacity as number)" :min="0" style="width: 100%" placeholder="请输入容量" />
          </n-form-item>
          <n-form-item label="描述" class="form-item-wide">
            <n-input v-model:value="(form.description as string)" type="textarea" placeholder="请输入描述信息" />
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
