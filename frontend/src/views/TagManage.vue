<script setup lang="ts">
// 标签管理页面 — 自定义标签分类与颜色标记
import { computed, ref, watch } from 'vue'
import {
  NButton,
  NInput,
  NDataTable,
  NModal,
  NForm,
  NFormItem,
  NPopconfirm,
  NTag,
  useMessage
} from 'naive-ui'
import type { DataTableColumns } from 'naive-ui'
import { h } from 'vue'
import { errorText, resource, rows, type RecordData } from '../api'
import { useAuth } from '../stores/auth'

const message = useMessage()
const auth = useAuth()

// === 预设颜色选项 ===
const presetColors = [
  { label: '青色', value: '#267b82' },
  { label: '橙色', value: '#e5a65c' },
  { label: '红色', value: '#d36b65' },
  { label: '蓝色', value: '#526b88' },
  { label: '绿色', value: '#48b7a3' }
]

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

// === 权限控制 ===
const canWrite = computed(() => auth.can('assets:write'))

// === 表格列定义 ===
const columns = computed<DataTableColumns<RecordData>>(() => [
  { title: '标签名称', key: 'name', minWidth: 180 },
  {
    title: '颜色',
    key: 'color',
    minWidth: 140,
    render: (row: RecordData) =>
      h(
        NTag,
        {
          size: 'small',
          bordered: false,
          style: { background: String(row.color || '#ccc'), color: '#fff' }
        },
        { default: () => String(row.color || '—') }
      )
  },
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
                  default: () => '确认删除此标签？'
                }
              )
            ])
        }
      ]
    : [])
])

// === 加载标签列表 ===
async function load() {
  loading.value = true
  try {
    const data = rows(await resource.list('/tags', { page: page.value, page_size: pageSize, search: search.value }))
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

// === 打开新建/编辑弹窗 ===
function openEdit(row?: RecordData) {
  currentId.value = row?.id ?? null
  form.value = row ? { ...row } : { color: presetColors[0].value }
  editing.value = true
}

// === 选择颜色 ===
function selectColor(color: string) {
  form.value.color = color
}

// === 保存标签 ===
async function save() {
  if (!form.value.name) {
    message.warning('请填写标签名称')
    return
  }
  saving.value = true
  try {
    const payload = {
      name: form.value.name,
      color: form.value.color || presetColors[0].value
    }
    if (currentId.value != null) {
      await resource.update('/tags', currentId.value, payload)
    } else {
      await resource.create('/tags', payload)
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

// === 删除标签 ===
async function remove(row: RecordData) {
  try {
    await resource.remove('/tags', row.id!)
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
        <div class="eyebrow dark">RESOURCE MANAGEMENT / TAGS</div>
        <h1>标签管理</h1>
        <p>自定义标签分类与颜色标记</p>
      </div>
      <div class="head-actions">
        <n-button v-if="canWrite" type="primary" @click="openEdit()">+ 新建标签</n-button>
      </div>
    </div>
    <div class="panel">
      <div class="panel-toolbar">
        <div>
          <div class="panel-title">标签列表 <span class="count">{{ total }}</span></div>
          <p class="panel-caption">管理所有标签及其颜色</p>
        </div>
        <div class="toolbar-actions">
          <n-input
            v-model:value="search"
            placeholder="搜索标签..."
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
        当前没有标签，请新建标签。
      </div>
    </div>
    <!-- 新建/编辑标签弹窗 -->
    <n-modal
      v-model:show="editing"
      preset="card"
      :title="currentId != null ? '编辑标签' : '新建标签'"
      style="width: min(440px, 94vw)"
    >
      <n-form label-placement="top">
        <n-form-item label="标签名称" required>
          <n-input v-model:value="(form.name as string)" placeholder="请输入标签名称" />
        </n-form-item>
        <n-form-item label="标签颜色">
          <div class="color-picker-row">
            <div
              v-for="c in presetColors"
              :key="c.value"
              class="color-swatch"
              :class="{ active: form.color === c.value }"
              :style="{ background: c.value }"
              :title="c.label"
              @click="selectColor(c.value)"
            ></div>
          </div>
        </n-form-item>
        <div class="modal-actions">
          <n-button @click="editing = false">取消</n-button>
          <n-button type="primary" :loading="saving" @click="save">保存</n-button>
        </div>
      </n-form>
    </n-modal>
  </div>
</template>

<style scoped>
/* 颜色选择器样式 */
.color-picker-row {
  display: flex;
  gap: 10px;
  padding: 4px 0;
}
.color-swatch {
  width: 32px;
  height: 32px;
  border-radius: 6px;
  cursor: pointer;
  border: 3px solid transparent;
  transition: border-color 0.15s, transform 0.15s;
}
.color-swatch:hover {
  transform: scale(1.1);
}
.color-swatch.active {
  border-color: #20313d;
}
</style>
