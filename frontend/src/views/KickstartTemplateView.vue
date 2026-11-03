<script setup lang="ts">
// Kickstart 模板管理页面 — 自动应答模板的创建与维护
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
  useMessage
} from 'naive-ui'
import type { DataTableColumns } from 'naive-ui'
import { h } from 'vue'
import { errorText, resource, rows, type RecordData } from '../api'
import { useAuth } from '../stores/auth'

const message = useMessage()
const auth = useAuth()

// === 操作系统类型选项 ===
const osTypeOptions = [
  { label: 'CentOS 7', value: 'centos7' },
  { label: 'CentOS 8', value: 'centos8' },
  { label: 'Rocky Linux 8', value: 'rocky8' },
  { label: 'Rocky Linux 9', value: 'rocky9' },
  { label: 'Ubuntu 20.04', value: 'ubuntu2004' },
  { label: 'Ubuntu 22.04', value: 'ubuntu2204' },
  { label: 'Debian 11', value: 'debian11' },
  { label: 'Debian 12', value: 'debian12' },
  { label: 'ESXi', value: 'esxi' }
]

// === 可用变量提示 ===
const templateVariables = '$IP, $HOSTNAME, $ROOT_PASSWORD, $MAC, $GATEWAY, $DNS, $SUBNET'

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
const canWrite = computed(() => auth.can('images:write'))

// === 表格列定义 ===
const columns = computed<DataTableColumns<RecordData>>(() => [
  { title: '模板名称', key: 'name', minWidth: 160 },
  { title: '操作系统', key: 'os_type', minWidth: 120 },
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
                  default: () => '确认删除此模板？'
                }
              )
            ])
        }
      ]
    : [])
])

// === 加载模板列表 ===
async function load() {
  loading.value = true
  try {
    const data = rows(await resource.list('/kickstart-templates', { page: page.value, page_size: pageSize }))
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
  form.value = row ? { ...row } : { os_type: 'centos7' }
  editing.value = true
}

// === 保存模板 ===
async function save() {
  if (!form.value.name || !form.value.os_type || !form.value.template_content) {
    message.warning('请填写必填字段')
    return
  }
  saving.value = true
  try {
    const payload = {
      name: form.value.name,
      os_type: form.value.os_type,
      template_content: form.value.template_content,
      partition_scheme: form.value.partition_scheme || '',
      post_script: form.value.post_script || ''
    }
    if (currentId.value != null) {
      await resource.update('/kickstart-templates', currentId.value, payload)
    } else {
      await resource.create('/kickstart-templates', payload)
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

// === 删除模板 ===
async function remove(row: RecordData) {
  try {
    await resource.remove('/kickstart-templates', row.id!)
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
        <div class="eyebrow dark">OPERATIONS / KICKSTART TEMPLATES</div>
        <h1>Kickstart 模板</h1>
        <p>管理自动应答模板，实现无人值守装机</p>
      </div>
      <div class="head-actions">
        <n-button v-if="canWrite" type="primary" @click="openEdit()">+ 新建模板</n-button>
      </div>
    </div>
    <div class="panel">
      <div class="panel-toolbar">
        <div>
          <div class="panel-title">模板列表 <span class="count">{{ total }}</span></div>
          <p class="panel-caption">创建和维护 Kickstart 自动应答模板</p>
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
        当前没有模板，请新建模板。
      </div>
    </div>
    <!-- 新建/编辑模板弹窗 -->
    <n-modal
      v-model:show="editing"
      preset="card"
      :title="currentId != null ? '编辑模板' : '新建模板'"
      style="width: min(680px, 94vw)"
    >
      <n-form label-placement="top">
        <div class="resource-form-grid">
          <n-form-item label="模板名称" required>
            <n-input v-model:value="(form.name as string)" placeholder="请输入模板名称" />
          </n-form-item>
          <n-form-item label="操作系统类型" required>
            <n-select v-model:value="(form.os_type as string)" :options="osTypeOptions" placeholder="请选择操作系统" />
          </n-form-item>
          <n-form-item label="模板内容" required class="form-item-wide">
            <n-input
              v-model:value="(form.template_content as string)"
              type="textarea"
              :rows="10"
              placeholder="请输入 Kickstart 模板内容"
            />
            <div class="template-hint">
              可用变量：{{ templateVariables }}
            </div>
          </n-form-item>
          <n-form-item label="分区方案" class="form-item-wide">
            <n-input
              v-model:value="(form.partition_scheme as string)"
              type="textarea"
              :rows="4"
              placeholder="请输入分区方案（可选）"
            />
          </n-form-item>
          <n-form-item label="装机后脚本" class="form-item-wide">
            <n-input
              v-model:value="(form.post_script as string)"
              type="textarea"
              :rows="4"
              placeholder="请输入装机完成后执行的脚本（可选）"
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

<style scoped>
/* 模板变量提示样式 */
.template-hint {
  font-size: 11px;
  color: #7f95a3;
  margin-top: 6px;
  padding: 6px 10px;
  background: #f4f6f6;
  border-radius: 4px;
  font-family: monospace;
}
</style>
