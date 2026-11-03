<script setup lang="ts">
// 机柜管理页面 — 追踪机柜空间与部署位置，含 U 位可视化
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

// === U 位可视化状态 ===
const showUVis = ref(false)
const selectedRack = ref<RecordData | null>(null)
const uSlots = ref<{ u: number; status: 'used' | 'free' }[]>([])

// === 机房选项列表（用于下拉筛选）===
const datacenterOptions = ref<{ label: string; value: string | number }[]>([])
const filterDatacenterId = ref<string | null>(null)

// === 权限控制 ===
const canWrite = computed(() => auth.can('assets:write'))

// === 表格列定义 ===
const columns = computed<DataTableColumns<RecordData>>(() => [
  { title: '机柜名称', key: 'name', minWidth: 140 },
  {
    title: '所属机房',
    key: 'datacenter_id',
    minWidth: 120,
    render: (row: RecordData) => {
      const dc = datacenterOptions.value.find(o => o.value === row.datacenter_id)
      return dc ? String(dc.label) : String(row.datacenter_id ?? '—')
    }
  },
  { title: 'U 位高度', key: 'height_u', minWidth: 90 },
  { title: '位置', key: 'location', minWidth: 120 },
  { title: '供电容量', key: 'power_capacity', minWidth: 100 },
  { title: '创建时间', key: 'created_at', minWidth: 150 },
  {
    title: '操作',
    key: 'actions',
    width: 180,
    fixed: 'right',
    render: (row: RecordData) =>
      h('div', { class: 'table-actions' }, [
        h(
          NButton,
          { text: true, type: 'info', onClick: () => openUVisualization(row) },
          { default: () => 'U 位图' }
        ),
        ...(canWrite.value
          ? [
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
                  default: () => '确认删除此机柜？'
                }
              )
            ]
          : [])
      ])
  }
])

// === 加载机房选项 ===
async function loadDatacenters() {
  try {
    const data = rows(await resource.list('/datacenters', { page: 1, page_size: 200 }))
    datacenterOptions.value = data.items.map(dc => ({
      label: String(dc.name || dc.id),
      value: dc.id as string | number
    }))
  } catch {
    // 加载机房列表失败，忽略
  }
}

// === 加载机柜列表（后端分页）===
async function load() {
  loading.value = true
  try {
    const params: Record<string, unknown> = { page: page.value, page_size: pageSize, search: search.value }
    if (filterDatacenterId.value) params.datacenter_id = filterDatacenterId.value
    const data = rows(await resource.list('/racks', params))
    items.value = data.items
    total.value = data.total
  } catch (e) {
    message.error(errorText(e))
  } finally {
    loading.value = false
  }
}

// === 监听变化 ===
watch(
  () => auth.projectId,
  () => {
    page.value = 1
    search.value = ''
    filterDatacenterId.value = null
    loadDatacenters()
    load()
  },
  { immediate: true }
)

// === 打开新建/编辑弹窗 ===
function openEdit(row?: RecordData) {
  currentId.value = row?.id ?? null
  form.value = row ? { ...row } : { height_u: 42 }
  editing.value = true
}

// === 打开 U 位可视化 ===
function openUVisualization(row: RecordData) {
  selectedRack.value = row
  const heightU = Number(row.height_u || 42)
  // 模拟 U 位状态：随机标记部分为已用（实际应从后端获取）
  uSlots.value = Array.from({ length: heightU }, (_, i) => ({
    u: heightU - i,
    status: Math.random() > 0.7 ? 'used' : 'free'
  }))
  showUVis.value = true
}

// === 保存机柜数据 ===
async function save() {
  if (!form.value.name) {
    message.warning('请填写机柜名称')
    return
  }
  saving.value = true
  try {
    const payload = {
      name: form.value.name,
      datacenter_id: form.value.datacenter_id || null,
      height_u: form.value.height_u || 42,
      location: form.value.location || '',
      power_capacity: form.value.power_capacity || 0
    }
    if (currentId.value != null) {
      await resource.update('/racks', currentId.value, payload)
    } else {
      await resource.create('/racks', payload)
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

// === 删除机柜 ===
async function remove(row: RecordData) {
  try {
    await resource.remove('/racks', row.id!)
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
        <div class="eyebrow dark">INFRASTRUCTURE / RACKS</div>
        <h1>机柜管理</h1>
        <p>追踪机柜空间与设备部署位置</p>
      </div>
      <div class="head-actions">
        <n-button v-if="canWrite" type="primary" @click="openEdit()">+ 新建机柜</n-button>
      </div>
    </div>
    <div class="panel">
      <div class="panel-toolbar">
        <div>
          <div class="panel-title">机柜列表 <span class="count">{{ total }}</span></div>
          <p class="panel-caption">按机房筛选并管理所有机柜</p>
        </div>
        <div class="toolbar-actions">
          <n-select
            v-model:value="filterDatacenterId"
            :options="[{ label: '全部机房', value: null as unknown as string }, ...datacenterOptions]"
            placeholder="按机房筛选"
            clearable
            style="width: 160px"
            @update:value="page = 1; load()"
          />
          <n-input
            v-model:value="search"
            placeholder="搜索机柜..."
            clearable
            style="width: 200px"
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
        当前没有机柜数据，请新建机柜。
      </div>
    </div>
    <!-- 新建/编辑机柜弹窗 -->
    <n-modal
      v-model:show="editing"
      preset="card"
      :title="currentId != null ? '编辑机柜' : '新建机柜'"
      style="width: min(520px, 94vw)"
    >
      <n-form label-placement="top">
        <div class="resource-form-grid">
          <n-form-item label="机柜名称" required>
            <n-input v-model:value="(form.name as string)" placeholder="请输入机柜名称" />
          </n-form-item>
          <n-form-item label="所属机房">
            <n-select
              v-model:value="(form.datacenter_id as string | number)"
              :options="datacenterOptions"
              placeholder="请选择机房"
              clearable
            />
          </n-form-item>
          <n-form-item label="U 位高度">
            <n-input-number v-model:value="(form.height_u as number)" :min="1" :max="50" style="width: 100%" />
          </n-form-item>
          <n-form-item label="位置">
            <n-input v-model:value="(form.location as string)" placeholder="请输入机柜位置" />
          </n-form-item>
          <n-form-item label="供电容量 (W)">
            <n-input-number v-model:value="(form.power_capacity as number)" :min="0" style="width: 100%" />
          </n-form-item>
        </div>
        <div class="modal-actions">
          <n-button @click="editing = false">取消</n-button>
          <n-button type="primary" :loading="saving" @click="save">保存</n-button>
        </div>
      </n-form>
    </n-modal>
    <!-- U 位可视化弹窗 -->
    <n-modal
      v-model:show="showUVis"
      preset="card"
      :title="`机柜 U 位图 — ${selectedRack?.name || ''}`"
      style="width: min(380px, 94vw)"
    >
      <div class="u-vis-container">
        <div class="u-vis-legend">
          <span class="legend-item"><span class="legend-dot used"></span> 已用</span>
          <span class="legend-item"><span class="legend-dot free"></span> 空闲</span>
        </div>
        <div class="u-vis-grid">
          <div
            v-for="slot in uSlots"
            :key="slot.u"
            class="u-slot"
            :class="slot.status"
            :title="`U${slot.u} — ${slot.status === 'used' ? '已用' : '空闲'}`"
          >
            <span class="u-number">{{ slot.u }}</span>
          </div>
        </div>
      </div>
    </n-modal>
  </div>
</template>

<style scoped>
/* U 位可视化样式 */
.u-vis-container {
  max-height: 60vh;
  overflow-y: auto;
  padding: 8px 0;
}
.u-vis-legend {
  display: flex;
  gap: 16px;
  margin-bottom: 12px;
  font-size: 12px;
  color: #667f90;
}
.legend-item {
  display: flex;
  align-items: center;
  gap: 6px;
}
.legend-dot {
  width: 12px;
  height: 12px;
  border-radius: 3px;
}
.legend-dot.used {
  background: #d36b65;
}
.legend-dot.free {
  background: #48b7a3;
}
.u-vis-grid {
  display: flex;
  flex-direction: column;
  gap: 2px;
}
.u-slot {
  height: 22px;
  border-radius: 3px;
  display: flex;
  align-items: center;
  padding: 0 10px;
  font-size: 11px;
  transition: opacity 0.15s;
}
.u-slot:hover {
  opacity: 0.8;
}
.u-slot.used {
  background: #d36b65;
  color: white;
}
.u-slot.free {
  background: #e8f3f1;
  color: #458984;
}
.u-number {
  font-weight: 600;
  font-variant-numeric: tabular-nums;
}
</style>
