<script setup lang="ts">
// 部门管理页面 — 自用模式：部门结构树、部门资源统计与资产归属批量分配
import { computed, h, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import {
  NButton,
  NCheckbox,
  NDataTable,
  NForm,
  NFormItem,
  NIcon,
  NInput,
  NModal,
  NPopconfirm,
  NProgress,
  NSelect,
  NSpace,
  NTag,
  NTree,
  useMessage
} from 'naive-ui'
import type { DataTableColumns, DataTableRowKey, TreeOption } from 'naive-ui'
import { CreateOutline, TrashOutline } from '@vicons/ionicons5'
import {
  assetAssignApi,
  departmentApi,
  departmentStats,
  errorText,
  rows,
  unwrap,
  type RecordData
} from '../api'
import { useAuth } from '../stores/auth'

const router = useRouter()
const message = useMessage()
const auth = useAuth()

// ============================================================
// 状态
// ============================================================
const treeRaw = ref<RecordData[]>([])
const assets = ref<RecordData[]>([])
const assetTotal = ref(0)
const datacenterNames = ref<Record<string, string>>({})
const loading = ref(false)

const selectedKeys = ref<string[]>([])
const expandedKeys = ref<string[]>([])
const pattern = ref('')
const includeSubtree = ref(false)

const stats = ref<RecordData | null>(null)
const statsLoading = ref(false)
const assigning = ref(false)

const checkedDeptAssets = ref<DataTableRowKey[]>([])

// 后端 POST /assets/batch-assign 限制单次 asset_ids ≤ 500（超出返回 400），
// 因此勾选阶段即截断，提交前再校验一次，避免用户在弹窗末尾才收到报错。
const BATCH_LIMIT = 500
function clampBatchKeys(keys: DataTableRowKey[]) {
  if (keys.length <= BATCH_LIMIT) return keys
  message.warning(`单次批量操作最多 ${BATCH_LIMIT} 台资产，已保留前 ${BATCH_LIMIT} 台`)
  return keys.slice(0, BATCH_LIMIT)
}

// 新建 / 编辑弹窗
const editing = ref(false)
const saving = ref(false)
const editingId = ref<string>('')
const form = ref<RecordData>({})

// 分配资产弹窗
const assignModal = ref(false)
const assignSearch = ref('')
const onlyUnassigned = ref(true)
const assignChecked = ref<DataTableRowKey[]>([])

const canWriteDept = computed(() => auth.can('departments:write'))
const canAssign = computed(() => auth.can('assets:write'))

// ============================================================
// 部门索引（扁平化 + 父子关系）
// ============================================================
interface DeptNode {
  id: string
  name: string
  parentId: string | null
  childIds: string[]
  raw: RecordData
}

const deptIndex = computed<Map<string, DeptNode>>(() => {
  const map = new Map<string, DeptNode>()
  function visit(nodes: RecordData[], parentId: string | null) {
    for (const node of nodes) {
      const id = String(node.id ?? '')
      if (!id) continue
      const children = Array.isArray(node.children) ? (node.children as RecordData[]) : []
      const childIds = children.map(c => String(c.id ?? '')).filter(Boolean)
      map.set(id, { id, name: String(node.name ?? '未命名部门'), parentId, childIds, raw: node })
      visit(children, id)
    }
  }
  visit(treeRaw.value, null)
  return map
})

const flatDepts = computed(() => [...deptIndex.value.values()])

// 部门路径，例如「技术中心 / 平台组」
function pathOf(id: string): string {
  const names: string[] = []
  const seen = new Set<string>()
  let cursor: string | null = id
  while (cursor && !seen.has(cursor)) {
    seen.add(cursor)
    const node = deptIndex.value.get(cursor)
    if (!node) break
    names.unshift(node.name)
    cursor = node.parentId
  }
  return names.join(' / ')
}

// 自身 + 全部子孙部门 id
function descendantsOf(id: string): string[] {
  const out: string[] = []
  const seen = new Set<string>()
  const queue = [id]
  while (queue.length) {
    const current = queue.shift()!
    if (seen.has(current)) continue
    seen.add(current)
    out.push(current)
    queue.push(...(deptIndex.value.get(current)?.childIds ?? []))
  }
  return out
}

// ============================================================
// 资产归属派生
// ============================================================
const assetsByDept = computed<Map<string, RecordData[]>>(() => {
  const map = new Map<string, RecordData[]>()
  for (const asset of assets.value) {
    const id = asset.department_id
    if (id == null || id === 0 || id === '') continue
    const key = String(id)
    const list = map.get(key)
    if (list) list.push(asset)
    else map.set(key, [asset])
  }
  return map
})

const unassignedAssets = computed(() =>
  assets.value.filter(a => a.department_id == null || a.department_id === 0 || a.department_id === '')
)

const currentId = computed(() => selectedKeys.value[0] ?? '')
const currentNode = computed(() => (currentId.value ? deptIndex.value.get(currentId.value) ?? null : null))
const currentDept = computed(() => currentNode.value?.raw ?? null)

const currentAssets = computed<RecordData[]>(() => {
  if (!currentId.value) return []
  if (!includeSubtree.value) return assetsByDept.value.get(currentId.value) ?? []
  const ids = new Set(descendantsOf(currentId.value))
  return assets.value.filter(a => a.department_id != null && ids.has(String(a.department_id)))
})

// 与后端统计口径保持一致：status 为 online 视为在线
const isOnline = (asset: RecordData) => String(asset.status ?? '') === 'online'

const metrics = computed(() => {
  const localCount = assetsByDept.value.get(currentId.value)?.length ?? 0
  const localOnline = currentAssets.value.filter(isOnline).length
  const directOnline = (assetsByDept.value.get(currentId.value) ?? []).filter(isOnline).length
  const subDeptCount = currentNode.value ? descendantsOf(currentId.value).length - 1 : 0
  if (includeSubtree.value) {
    return {
      assetCount: currentAssets.value.length,
      onlineCount: localOnline,
      offlineCount: currentAssets.value.length - localOnline,
      subDeptCount,
      scope: '含子部门（本地统计）'
    }
  }
  const assetCount = stats.value ? Number(stats.value.asset_count ?? localCount) : localCount
  const onlineCount = stats.value ? Number(stats.value.online_count ?? directOnline) : directOnline
  // offline_count 由后端按状态口径直接聚合，优先采用；缺失时才用总数减在线数推算
  const backendOffline = stats.value?.offline_count == null ? null : Number(stats.value.offline_count)
  return {
    assetCount,
    onlineCount,
    offlineCount: backendOffline ?? Math.max(assetCount - onlineCount, 0),
    subDeptCount: stats.value ? Number(stats.value.sub_dept_count ?? subDeptCount) : subDeptCount,
    scope: '仅本部门（后端统计）'
  }
})

const onlineRate = computed(() =>
  metrics.value.assetCount > 0 ? Math.round((metrics.value.onlineCount / metrics.value.assetCount) * 100) : 0
)

const STATUS_LABELS: Record<string, string> = {
  online: '在线', running: '运行中', in_use: '使用中', stopped: '已关机', offline: '离线',
  error: '故障', maintenance: '维护中', retired: '已退役', pending_management: '待纳管',
  managing: '纳管中', collecting: '采集中', queued: '等待执行', unknown: '状态未知'
}

function statusLabel(value: unknown): string {
  const key = String(value ?? 'unknown')
  return STATUS_LABELS[key] || key
}

function statusTagType(value: unknown): 'success' | 'warning' | 'error' | 'info' | 'default' {
  const key = String(value ?? '')
  if (['online', 'running', 'in_use'].includes(key)) return 'success'
  if (['maintenance', 'managing', 'collecting', 'queued', 'installing', 'reinstalling', 'upgrading'].includes(key)) return 'info'
  if (['error', 'fault'].includes(key)) return 'error'
  if (['offline', 'stopped', 'retired', 'unknown', 'pending_management'].includes(key)) return 'default'
  return 'default'
}

// 状态分布摘要
const statusSummary = computed<{ label: string; count: number; type: 'success' | 'warning' | 'error' | 'info' | 'default' }[]>(() => {
  const counter = new Map<string, number>()
  for (const asset of currentAssets.value) {
    const key = String(asset.display_status ?? asset.status ?? 'unknown')
    counter.set(key, (counter.get(key) ?? 0) + 1)
  }
  return [...counter.entries()]
    .map(([key, count]) => ({ label: statusLabel(key), count, type: statusTagType(key) }))
    .sort((a, b) => b.count - a.count)
})

// 机房分布摘要
const datacenterSummary = computed(() => {
  const counter = new Map<string, number>()
  for (const asset of currentAssets.value) {
    const id = String(asset.datacenter_id ?? '')
    const name = datacenterNames.value[id] || (id ? `机房 #${id}` : '未归属机房')
    counter.set(name, (counter.get(name) ?? 0) + 1)
  }
  return [...counter.entries()].map(([label, count]) => ({ label, count })).sort((a, b) => b.count - a.count).slice(0, 4)
})

// ============================================================
// 树形数据
// ============================================================
function toTreeOption(node: RecordData): TreeOption {
  const children = Array.isArray(node.children) ? (node.children as RecordData[]) : []
  return {
    key: String(node.id ?? ''),
    label: String(node.name ?? '未命名部门'),
    isLeaf: children.length === 0,
    children: children.length ? children.map(toTreeOption) : undefined
  }
}

const treeOptions = computed<TreeOption[]>(() => treeRaw.value.map(toTreeOption))

const mono = (text: string) => h('span', { class: 'mono-value' }, text)

function renderTreeLabel({ option }: { option: TreeOption }) {
  const id = String(option.key ?? '')
  const count = assetsByDept.value.get(id)?.length ?? 0
  return h('div', { class: 'dept-tree-label' }, [
    h('span', { class: 'dept-tree-name' }, String(option.label ?? '')),
    h('span', { class: ['dept-tree-count', count > 0 ? 'has-asset' : ''] }, count > 0 ? `${count} 台` : '0')
  ])
}

function renderTreeSuffix({ option }: { option: TreeOption }) {
  if (!canWriteDept.value && !canAssign.value) return null
  const id = String(option.key ?? '')
  const node = deptIndex.value.get(id)
  if (!node) return null
  return h('div', {
    class: 'dept-tree-actions',
    onClick: (event: MouseEvent) => event.stopPropagation()
  }, [
    canWriteDept.value
      ? h(NButton, {
          size: 'tiny',
          quaternary: true,
          circle: true,
          title: '编辑部门',
          onClick: () => openEdit(node.raw),
          renderIcon: () => h(NIcon, { size: 14 }, { default: () => h(CreateOutline) })
        })
      : null,
    canWriteDept.value
      ? h(
          NPopconfirm,
          { onPositiveClick: () => remove(node.raw) },
          {
            trigger: () =>
              h(NButton, {
                size: 'tiny',
                quaternary: true,
                circle: true,
                type: 'error',
                title: '删除部门',
                renderIcon: () => h(NIcon, { size: 14 }, { default: () => h(TrashOutline) })
              }),
            default: () => `确认删除部门「${node.name}」？存在子部门或资产时无法删除。`
          }
        )
      : null
  ])
}

function onTreeSelect(keys: Array<string | number>) {
  selectedKeys.value = keys.map(String)
  checkedDeptAssets.value = []
}

// ============================================================
// 数据加载
// ============================================================
async function loadDepartments() {
  const data = unwrap<RecordData[]>(await departmentApi.list('/departments'))
  treeRaw.value = Array.isArray(data) ? data : []
  // 默认展开全部含子节点的部门，便于浏览
  expandedKeys.value = flatDepts.value.filter(d => d.childIds.length).map(d => d.id)
  if (currentId.value && !deptIndex.value.has(currentId.value)) selectedKeys.value = []
}

async function loadAssets() {
  const collected: RecordData[] = []
  let page = 1
  for (;;) {
    const result = rows(await departmentApi.list('/assets', { page, page_size: 200 }))
    collected.push(...result.items)
    assetTotal.value = result.total
    if (collected.length >= result.total || page >= 5) break
    page += 1
  }
  assets.value = collected
}

async function loadDatacenters() {
  if (!auth.can('datacenters:read')) return
  try {
    const result = rows(await departmentApi.list('/datacenters', { page: 1, page_size: 200 }))
    const map: Record<string, string> = {}
    for (const item of result.items) map[String(item.id)] = String(item.name ?? '')
    datacenterNames.value = map
  } catch {
    // 机房信息仅用于摘要展示，加载失败不阻断主流程
  }
}

async function loadStats() {
  if (!currentId.value) {
    stats.value = null
    return
  }
  statsLoading.value = true
  try {
    stats.value = unwrap<RecordData>(await departmentStats(currentId.value))
  } catch {
    // 统计接口不可用时回退到本地资产列表统计
    stats.value = null
  } finally {
    statsLoading.value = false
  }
}

async function loadAll() {
  if (!auth.projectId) return
  loading.value = true
  try {
    await Promise.all([loadDepartments(), loadAssets(), loadDatacenters()])
    await loadStats()
  } catch (e) {
    message.error(errorText(e))
  } finally {
    loading.value = false
  }
}

// ============================================================
// 部门 CRUD
// ============================================================
const parentOptions = computed(() => {
  const excluded = editingId.value ? new Set(descendantsOf(editingId.value)) : new Set<string>()
  const options = [{ label: '无（顶级部门）', value: '' }]
  for (const dept of flatDepts.value) {
    if (excluded.has(dept.id)) continue
    options.push({ label: pathOf(dept.id), value: dept.id })
  }
  return options
})

function openCreate(parentId?: string) {
  editingId.value = ''
  form.value = { name: '', parent_id: parentId ?? currentId.value ?? '', description: '' }
  editing.value = true
}

function openEdit(row: RecordData) {
  editingId.value = String(row.id ?? '')
  form.value = {
    name: String(row.name ?? ''),
    parent_id: row.parent_id == null ? '' : String(row.parent_id),
    description: String(row.description ?? '')
  }
  editing.value = true
}

async function save() {
  const name = String(form.value.name ?? '').trim()
  if (!name) {
    message.warning('请填写部门名称')
    return
  }
  saving.value = true
  try {
    const parentId = Number(form.value.parent_id) || null
    const payload: RecordData = { name, description: String(form.value.description ?? ''), parent_id: parentId }
    if (editingId.value) {
      await departmentApi.update('/departments', editingId.value, payload)
    } else {
      await departmentApi.create('/departments', payload)
    }
    message.success('保存成功')
    editing.value = false
    await loadDepartments()
    await loadStats()
  } catch (e) {
    message.error(errorText(e))
  } finally {
    saving.value = false
  }
}

async function remove(row: RecordData) {
  const id = String(row.id ?? '')
  const children = deptIndex.value.get(id)?.childIds.length ?? 0
  const bound = assetsByDept.value.get(id)?.length ?? 0
  if (children > 0) {
    message.warning(`该部门下仍有 ${children} 个子部门，请先迁移或删除子部门`)
    return
  }
  if (bound > 0) {
    message.warning(`该部门下仍有 ${bound} 台资产，请先解除资产分配`)
    return
  }
  try {
    await departmentApi.remove('/departments', id)
    message.success('已删除')
    if (currentId.value === id) selectedKeys.value = []
    await loadDepartments()
    await loadAssets()
  } catch (e) {
    message.error(errorText(e))
  }
}

// ============================================================
// 资产分配
// ============================================================
const candidateAssets = computed(() => {
  const kw = assignSearch.value.trim().toLowerCase()
  return assets.value.filter(asset => {
    if (onlyUnassigned.value && asset.department_id != null && asset.department_id !== 0 && asset.department_id !== '') return false
    if (!kw) return true
    return [asset.name, asset.serial_number, asset.management_ip, asset.bmc_address]
      .some(value => String(value ?? '').toLowerCase().includes(kw))
  })
})

function openAssign() {
  assignChecked.value = []
  assignSearch.value = ''
  assignModal.value = true
}

function onDeptAssetsChecked(keys: DataTableRowKey[]) {
  checkedDeptAssets.value = clampBatchKeys(keys)
}

function onAssignChecked(keys: DataTableRowKey[]) {
  assignChecked.value = clampBatchKeys(keys)
}

async function confirmAssign() {
  if (!currentId.value) return
  const ids = assignChecked.value.map(String)
  if (!ids.length) {
    message.warning('请先勾选需要分配的资产')
    return
  }
  if (ids.length > BATCH_LIMIT) {
    message.warning(`单次批量分配不能超过 ${BATCH_LIMIT} 台，当前已选 ${ids.length} 台，请减少勾选`)
    return
  }
  assigning.value = true
  try {
    await assetAssignApi.batchAssign(ids, currentId.value)
    message.success(`已将 ${ids.length} 台资产分配至「${currentNode.value?.name ?? ''}」`)
    assignModal.value = false
    checkedDeptAssets.value = []
    await loadAssets()
    await loadStats()
  } catch (e) {
    message.error(errorText(e))
  } finally {
    assigning.value = false
  }
}

async function unassign(ids: DataTableRowKey[]) {
  if (!ids.length) {
    message.warning('请先勾选资产')
    return
  }
  if (ids.length > BATCH_LIMIT) {
    message.warning(`单次批量解除归属不能超过 ${BATCH_LIMIT} 台，当前已选 ${ids.length} 台，请减少勾选`)
    return
  }
  assigning.value = true
  try {
    await assetAssignApi.batchAssign(ids.map(String), null)
    message.success(`已解除 ${ids.length} 台资产的部门归属`)
    checkedDeptAssets.value = []
    await loadAssets()
    await loadStats()
  } catch (e) {
    message.error(errorText(e))
  } finally {
    assigning.value = false
  }
}

const deptAssetColumns = computed<DataTableColumns<RecordData>>(() => [
  { type: 'selection', fixed: 'left', width: 40, disabled: () => !canAssign.value },
  {
    title: '资产名称',
    key: 'name',
    minWidth: 150,
    ellipsis: { tooltip: true },
    render: row =>
      h(NButton, { text: true, type: 'primary', onClick: () => router.push(`/assets/${row.id}`) },
        { default: () => String(row.name ?? '—') })
  },
  { title: '序列号', key: 'serial_number', minWidth: 120, render: row => mono(String(row.serial_number || '—')) },
  { title: '管理 IP', key: 'management_ip', minWidth: 120, render: row => mono(String(row.management_ip || '—')) },
  {
    title: '状态',
    key: 'display_status',
    width: 100,
    render: row => h(NTag, { size: 'small', bordered: false, type: statusTagType(row.display_status ?? row.status) },
      { default: () => statusLabel(row.display_status ?? row.status) })
  },
  ...(canAssign.value
    ? [{
        title: '操作',
        key: 'actions',
        width: 84,
        render: (row: RecordData) =>
          h(NButton, { size: 'tiny', text: true, type: 'error', disabled: assigning.value, onClick: () => unassign([String(row.id)]) },
            { default: () => '移出' })
      }]
    : [])
])

const assignColumns = computed<DataTableColumns<RecordData>>(() => [
  { type: 'selection', fixed: 'left', width: 40 },
  { title: '资产名称', key: 'name', minWidth: 140, ellipsis: { tooltip: true } },
  { title: '管理 IP', key: 'management_ip', minWidth: 120, render: row => mono(String(row.management_ip || '—')) },
  {
    title: '当前部门',
    key: 'department_id',
    minWidth: 110,
    render: row => String(deptIndex.value.get(String(row.department_id ?? ''))?.name ?? '未分配')
  },
  {
    title: '状态',
    key: 'display_status',
    width: 100,
    render: row => h(NTag, { size: 'small', bordered: false, type: statusTagType(row.display_status ?? row.status) },
      { default: () => statusLabel(row.display_status ?? row.status) })
  }
])

// ============================================================
// 生命周期
// ============================================================
watch(() => auth.projectId, () => {
  selectedKeys.value = []
  stats.value = null
  void loadAll()
}, { immediate: true })

watch(currentId, () => { void loadStats() })
</script>

<template>
  <div class="page">
    <div class="page-head">
      <div>
        <div class="eyebrow dark">ORGANIZATION / DEPARTMENTS</div>
        <h1>部门管理</h1>
        <p>维护自用模式下的部门结构，掌握各部门资产归属与在线情况</p>
      </div>
      <div class="head-actions">
        <n-button quaternary @click="loadAll">刷新</n-button>
        <n-button v-if="canWriteDept" type="primary" @click="openCreate()">+ 新建顶级部门</n-button>
      </div>
    </div>

    <div class="dept-workspace">
      <!-- ===== 左侧：部门结构树 ===== -->
      <section class="panel dept-tree-panel">
        <div class="panel-toolbar">
          <div>
            <div class="panel-title">部门结构 <span class="count">{{ flatDepts.length }}</span></div>
            <p class="panel-caption">点击部门查看资源统计与资产归属</p>
          </div>
        </div>
        <div class="dept-tree-toolbar">
          <n-input
            v-model:value="pattern"
            size="small"
            clearable
            placeholder="搜索部门名称"
          >
            <template #prefix>
              <span class="dept-tree-search-icon">⌕</span>
            </template>
          </n-input>
          <n-button size="tiny" quaternary @click="expandedKeys = flatDepts.filter(d => d.childIds.length).map(d => d.id)">
            展开全部
          </n-button>
          <n-button size="tiny" quaternary @click="expandedKeys = []">收起</n-button>
        </div>
        <div class="dept-tree-scroll">
          <n-tree
            v-if="treeOptions.length"
            :data="treeOptions"
            :pattern="pattern"
            :show-irrelevant-nodes="false"
            :selected-keys="selectedKeys"
            :expanded-keys="expandedKeys"
            block-node
            :render-label="renderTreeLabel"
            :render-suffix="canWriteDept ? renderTreeSuffix : undefined"
            @update:expanded-keys="(keys: Array<string | number>) => (expandedKeys = keys.map(String))"
            @update:selected-keys="onTreeSelect"
          />
          <div v-else-if="!loading" class="empty-hint">
            还没有部门，先创建一个顶级部门开始组织资产归属。
          </div>
        </div>
      </section>

      <!-- ===== 右侧：部门详情 ===== -->
      <section class="dept-detail-column">
        <div v-if="!currentDept" class="panel dept-detail-empty">
          <div class="empty-hint">请从左侧选择一个部门，查看资产数量、在线情况与归属明细。</div>
        </div>

        <template v-else>
          <div class="panel dept-detail-head">
            <div class="dept-head-main">
              <div class="dept-breadcrumb">{{ pathOf(currentId) }}</div>
              <h2>{{ currentNode?.name }}</h2>
              <p>{{ String(currentDept.description || '暂无部门描述') }}</p>
            </div>
            <n-space class="dept-head-actions">
              <n-button v-if="canWriteDept" size="small" @click="openEdit(currentDept)">编辑</n-button>
              <n-button v-if="canWriteDept" size="small" @click="openCreate(currentId)">新建子部门</n-button>
              <n-popconfirm v-if="canWriteDept" @positive-click="remove(currentDept)">
                <template #trigger>
                  <n-button size="small" type="error" ghost>删除部门</n-button>
                </template>
                确认删除「{{ currentNode?.name }}」？存在子部门或资产时无法删除。
              </n-popconfirm>
            </n-space>
          </div>

          <!-- 统计卡片 -->
          <div class="metric-grid dept-metric-grid">
            <div class="metric-card">
              <div class="metric-icon primary">
                <svg width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8"><rect x="2" y="3" width="20" height="7" rx="2"/><rect x="2" y="14" width="20" height="7" rx="2"/><circle cx="6" cy="6.5" r="1"/><circle cx="6" cy="17.5" r="1"/></svg>
              </div>
              <div class="metric-content">
                <div class="metric-label">资产数量</div>
                <div class="metric-value">{{ metrics.assetCount }}</div>
                <div class="metric-foot">{{ metrics.scope }}</div>
              </div>
            </div>
            <div class="metric-card">
              <div class="metric-icon success">
                <svg width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8"><path d="M22 12h-4l-3 9L9 3l-3 9H2"/></svg>
              </div>
              <div class="metric-content">
                <div class="metric-label">在线资产</div>
                <div class="metric-value">{{ metrics.onlineCount }}</div>
                <div class="metric-foot">在线率 {{ onlineRate }}%</div>
              </div>
            </div>
            <div class="metric-card">
              <div class="metric-icon warning">
                <svg width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8"><circle cx="12" cy="12" r="9"/><path d="M12 7v6l4 2"/></svg>
              </div>
              <div class="metric-content">
                <div class="metric-label">非在线资产</div>
                <div class="metric-value">{{ metrics.offlineCount }}</div>
                <div class="metric-foot">离线 / 维护 / 未知</div>
              </div>
            </div>
            <div class="metric-card">
              <div class="metric-icon info">
                <svg width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8"><path d="M3 9l9-7 9 7v11a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z"/><polyline points="9 22 9 12 15 12 15 22"/></svg>
              </div>
              <div class="metric-content">
                <div class="metric-label">子部门数量</div>
                <div class="metric-value">{{ metrics.subDeptCount }}</div>
                <div class="metric-foot">未分配资产 {{ unassignedAssets.length }} 台</div>
              </div>
            </div>
          </div>

          <!-- 资源使用摘要 -->
          <div class="panel dept-usage-panel">
            <div class="panel-toolbar">
              <div>
                <div class="panel-title">资源使用摘要</div>
                <p class="panel-caption">{{ includeSubtree ? '含子部门的资产构成' : '本部门直属资产构成' }}</p>
              </div>
              <div class="toolbar-actions">
                <n-checkbox v-model:checked="includeSubtree" size="small">包含子部门</n-checkbox>
              </div>
            </div>
            <div class="dept-usage-body">
              <div class="dept-online-rate">
                <div class="dept-online-label">在线率</div>
                <n-progress
                  type="line"
                  :percentage="onlineRate"
                  :status="onlineRate >= 80 ? 'success' : onlineRate >= 50 ? 'warning' : 'error'"
                  :height="8"
                  :border-radius="4"
                  :fill-border-radius="4"
                  indicator-placement="inside"
                />
              </div>
              <div class="dept-distribution">
                <div class="dept-dist-title">状态分布</div>
                <div v-if="statusSummary.length" class="dept-dist-tags">
                  <n-tag v-for="item in statusSummary" :key="item.label" size="small" :bordered="false" :type="item.type">
                    {{ item.label }} · {{ item.count }}
                  </n-tag>
                </div>
                <div v-else class="dept-dist-empty">该部门暂无资产</div>
              </div>
              <div class="dept-distribution">
                <div class="dept-dist-title">机房分布</div>
                <div v-if="datacenterSummary.length" class="dept-dist-tags">
                  <n-tag v-for="item in datacenterSummary" :key="item.label" size="small" :bordered="false">
                    {{ item.label }} · {{ item.count }}
                  </n-tag>
                </div>
                <div v-else class="dept-dist-empty">该部门暂无资产</div>
              </div>
            </div>
          </div>

          <!-- 部门资产明细 -->
          <div class="panel">
            <div class="panel-toolbar">
              <div>
                <div class="panel-title">部门资产 <span class="count">{{ currentAssets.length }}</span></div>
                <p class="panel-caption">
                  <template v-if="assetTotal > assets.length">已加载前 {{ assets.length }} / {{ assetTotal }} 台资产</template>
                  <template v-else>资产归属与在线状态</template>
                </p>
              </div>
              <div class="toolbar-actions">
                <n-button
                  v-if="canAssign"
                  size="small"
                  type="error"
                  ghost
                  :disabled="!checkedDeptAssets.length || assigning"
                  @click="unassign(checkedDeptAssets)"
                >
                  解除分配{{ checkedDeptAssets.length ? ` (${checkedDeptAssets.length})` : '' }}
                </n-button>
                <n-button v-if="canAssign" size="small" type="primary" @click="openAssign">
                  分配资产到此部门
                </n-button>
              </div>
            </div>
            <div v-if="checkedDeptAssets.length" class="batch-action-bar">
              <span class="batch-count">已选 {{ checkedDeptAssets.length }} / {{ BATCH_LIMIT }} 台资产（单次上限 {{ BATCH_LIMIT }}）</span>
              <n-button size="tiny" quaternary @click="checkedDeptAssets = []">清空选择</n-button>
            </div>
            <n-data-table
              size="small"
              :columns="deptAssetColumns"
              :data="currentAssets"
              :loading="loading || statsLoading"
              :row-key="(row: RecordData) => String(row.id ?? '')"
              :checked-row-keys="checkedDeptAssets"
              :scroll-x="760"
              :max-height="360"
              :pagination="{ pageSize: 20, showSizePicker: false }"
              @update:checked-row-keys="onDeptAssetsChecked"
            />
            <div v-if="!currentAssets.length && !loading" class="empty-hint">
              该部门暂无资产，可点击「分配资产到此部门」批量归属。
            </div>
          </div>
        </template>
      </section>
    </div>

    <!-- ===== 新建 / 编辑部门 ===== -->
    <n-modal
      v-model:show="editing"
      preset="card"
      :title="editingId ? '编辑部门' : '新建部门'"
      style="width: min(560px, 94vw)"
    >
      <n-form label-placement="top">
        <div class="resource-form-grid">
          <n-form-item label="部门名称" required>
            <n-input v-model:value="(form.name as string)" placeholder="请输入部门名称" maxlength="128" show-count />
          </n-form-item>
          <n-form-item label="上级部门">
            <n-select
              v-model:value="(form.parent_id as string)"
              :options="parentOptions"
              placeholder="选择上级部门"
              filterable
              clearable
            />
          </n-form-item>
          <n-form-item label="描述" class="form-item-wide">
            <n-input v-model:value="(form.description as string)" type="textarea" :rows="3" placeholder="请输入部门职责说明" />
          </n-form-item>
        </div>
        <div class="modal-actions">
          <n-button @click="editing = false">取消</n-button>
          <n-button type="primary" :loading="saving" @click="save">保存</n-button>
        </div>
      </n-form>
    </n-modal>

    <!-- ===== 批量分配资产 ===== -->
    <n-modal
      v-model:show="assignModal"
      preset="card"
      :title="`分配资产到「${currentNode?.name ?? ''}」`"
      style="width: min(780px, 94vw)"
    >
      <div class="assign-toolbar">
        <n-input
          v-model:value="assignSearch"
          size="small"
          clearable
          placeholder="按名称、SN、IP 搜索资产"
          style="max-width: 260px"
        />
        <n-checkbox v-model:checked="onlyUnassigned" size="small">仅显示未分配资产</n-checkbox>
        <span class="assign-hint">候选 {{ candidateAssets.length }} 台 · 已选 {{ assignChecked.length }} / {{ BATCH_LIMIT }} 台（单次上限 {{ BATCH_LIMIT }}）</span>
      </div>
      <n-data-table
        size="small"
        :columns="assignColumns"
        :data="candidateAssets"
        :row-key="(row: RecordData) => String(row.id ?? '')"
        :checked-row-keys="assignChecked"
        :scroll-x="620"
        :max-height="380"
        :pagination="{ pageSize: 10, showSizePicker: false }"
        @update:checked-row-keys="onAssignChecked"
      />
      <div v-if="!candidateAssets.length" class="empty-hint">没有符合条件的资产。</div>
      <div class="modal-actions">
        <n-button @click="assignModal = false">取消</n-button>
        <n-button type="primary" :loading="assigning" :disabled="!assignChecked.length" @click="confirmAssign">
          确认分配{{ assignChecked.length ? ` (${assignChecked.length})` : '' }}
        </n-button>
      </div>
    </n-modal>
  </div>
</template>

<style scoped>
.dept-workspace {
  display: grid;
  grid-template-columns: minmax(280px, 340px) minmax(0, 1fr);
  gap: 20px;
  align-items: start;
}

/* === 左侧结构树 === */
.dept-tree-panel {
  display: flex;
  flex-direction: column;
  min-height: 420px;
}

.dept-tree-toolbar {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 0 16px 12px;
  border-bottom: 1px solid var(--color-border-light);
}

.dept-tree-toolbar .n-input {
  flex: 1;
  min-width: 0;
}

.dept-tree-search-icon {
  color: var(--color-text-tertiary);
  font-size: 14px;
  line-height: 1;
}

.dept-tree-scroll {
  flex: 1;
  overflow: auto;
  padding: 12px 8px 16px;
  max-height: 60vh;
}

.dept-tree-scroll :deep(.n-tree-node-content__text) {
  flex: 1;
  min-width: 0;
}

.dept-tree-label {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  min-width: 0;
}

.dept-tree-name {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: var(--font-size-sm);
  color: var(--color-text-primary);
}

.dept-tree-count {
  flex-shrink: 0;
  font-size: 11px;
  padding: 1px 7px;
  border-radius: var(--radius-full);
  background: var(--color-border-light);
  color: var(--color-text-tertiary);
  font-variant-numeric: tabular-nums;
}

.dept-tree-count.has-asset {
  background: var(--color-primary-light);
  color: var(--color-primary);
}

.dept-tree-actions {
  display: flex;
  align-items: center;
  gap: 2px;
  opacity: 0;
  transition: opacity var(--transition-fast);
}

.dept-tree-scroll :deep(.n-tree-node-content:hover) + .dept-tree-actions,
.dept-tree-scroll :deep(.n-tree-treenode:hover) .dept-tree-actions {
  opacity: 1;
}

/* === 右侧详情 === */
.dept-detail-column {
  display: flex;
  flex-direction: column;
  gap: 16px;
  min-width: 0;
}

.dept-detail-empty {
  display: flex;
  align-items: center;
  justify-content: center;
  min-height: 220px;
}

.dept-detail-head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 16px;
  padding: 18px 20px;
}

.dept-head-main {
  min-width: 0;
}

.dept-breadcrumb {
  font-size: var(--font-size-xs);
  color: var(--color-text-tertiary);
  letter-spacing: 0.2px;
  margin-bottom: 6px;
}

.dept-head-main h2 {
  margin: 0;
  font-size: var(--font-size-xl);
  font-weight: 700;
  color: var(--color-text-primary);
}

.dept-head-main p {
  margin: 6px 0 0;
  font-size: var(--font-size-sm);
  color: var(--color-text-secondary);
}

.dept-head-actions {
  flex-shrink: 0;
}

.dept-metric-grid {
  grid-template-columns: repeat(4, minmax(0, 1fr));
  margin-bottom: 0;
}

/* === 摘要 === */
.dept-usage-panel {
  padding-bottom: 18px;
}

.dept-usage-body {
  display: grid;
  grid-template-columns: minmax(220px, 1fr) repeat(2, minmax(0, 1fr));
  gap: 20px;
  padding: 0 20px 4px;
}

.dept-online-label {
  font-size: var(--font-size-xs);
  color: var(--color-text-tertiary);
  margin-bottom: 8px;
}

.dept-distribution {
  min-width: 0;
}

.dept-dist-title {
  font-size: var(--font-size-xs);
  color: var(--color-text-tertiary);
  margin-bottom: 8px;
}

.dept-dist-tags {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
}

.dept-dist-empty {
  font-size: var(--font-size-sm);
  color: var(--color-text-disabled);
}

/* === 分配弹窗 === */
.assign-toolbar {
  display: flex;
  align-items: center;
  gap: 14px;
  flex-wrap: wrap;
  margin-bottom: 12px;
}

.assign-hint {
  font-size: var(--font-size-xs);
  color: var(--color-text-tertiary);
  margin-left: auto;
}

@media (max-width: 1180px) {
  .dept-workspace {
    grid-template-columns: 1fr;
  }
  .dept-metric-grid {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
  .dept-usage-body {
    grid-template-columns: 1fr;
  }
}

@media (max-width: 640px) {
  .dept-metric-grid {
    grid-template-columns: 1fr;
  }
  .dept-detail-head {
    flex-direction: column;
  }
}
</style>
