<script setup lang="ts">
// 角色权限管理页面 — 只读展示角色与权限分配矩阵
import { ref, watch } from 'vue'
import {
  NDataTable,
  NTag,
  NCheckbox,
  NCheckboxGroup,
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
const loading = ref(false)

// === 展开的角色权限矩阵 ===
const expandedRoles = ref<Set<string>>(new Set())

// === 权限分组定义（按资源分组）===
const permissionGroups = [
  { label: '裸机资产', key: 'assets', permissions: ['assets:read', 'assets:write', 'assets:operate'] },
  { label: 'BMC 操作', key: 'bmc', permissions: ['bmc:operate'] },
  { label: 'SOL 串口', key: 'sol', permissions: ['sol:access'] },
  { label: '系统镜像', key: 'images', permissions: ['images:read', 'images:write'] },
  { label: '任务管理', key: 'tasks', permissions: ['tasks:read', 'tasks:write'] },
  { label: '监控告警', key: 'alerts', permissions: ['alerts:read', 'alerts:write', 'alerts:manage'] },
  { label: '网络管理', key: 'network', permissions: ['network:read', 'network:write'] },
  { label: '固件管理', key: 'firmware', permissions: ['firmware:read', 'firmware:write'] },
  { label: '身份权限', key: 'iam', permissions: ['iam:read', 'iam:write'] },
  { label: '审计日志', key: 'audit', permissions: ['audit:read'] },
  { label: '系统设置', key: 'settings', permissions: ['settings:read', 'settings:write'] },
  { label: '项目管理', key: 'projects', permissions: ['projects:read', 'projects:write'] }
]

// === 表格列定义 ===
const columns: DataTableColumns<RecordData> = [
  {
    title: '角色名称',
    key: 'name',
    minWidth: 140,
    render: (row: RecordData) =>
      h('div', { style: 'display: flex; align-items: center; gap: 8px;' }, [
        h(
          NTag,
          { size: 'small', bordered: false, type: row.name === 'admin' ? 'error' : 'default' },
          { default: () => String(row.name || '—') }
        )
      ])
  },
  { title: '描述', key: 'description', minWidth: 200, ellipsis: { tooltip: true } },
  {
    title: '权限数量',
    key: 'permission_count',
    minWidth: 100,
    render: (row: RecordData) => {
      const perms = (row.permissions as string[]) || []
      return h(
        NTag,
        { size: 'small', bordered: false },
        { default: () => `${perms.length} 项` }
      )
    }
  },
  {
    title: '操作',
    key: 'actions',
    width: 100,
    render: (row: RecordData) =>
      h(
        'a',
        {
          style: 'color: #277b82; cursor: pointer; font-size: 12px;',
          onClick: () => toggleExpand(row)
        },
        expandedRoles.value.has(String(row.name)) ? '收起' : '展开权限'
      )
  }
]

// === 加载角色列表 ===
async function load() {
  loading.value = true
  try {
    const data = rows(await resource.list('/roles', { page: 1, page_size: 50 }))
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

// === 展开/收起角色权限矩阵 ===
function toggleExpand(row: RecordData) {
  const roleName = String(row.name)
  if (expandedRoles.value.has(roleName)) {
    expandedRoles.value.delete(roleName)
  } else {
    expandedRoles.value.add(roleName)
  }
  // 触发响应式更新
  expandedRoles.value = new Set(expandedRoles.value)
}

// === 检查权限是否属于该角色 ===
function hasPermission(row: RecordData, perm: string): boolean {
  const perms = (row.permissions as string[]) || []
  return perms.includes(perm)
}
</script>

<template>
  <div class="page">
    <div class="page-head">
      <div>
        <div class="eyebrow dark">ACCESS CONTROL / ROLES</div>
        <h1>角色权限</h1>
        <p>查看系统预定义角色及其权限分配情况（只读）</p>
      </div>
    </div>
    <div class="panel">
      <div class="panel-toolbar">
        <div>
          <div class="panel-title">角色列表 <span class="count">{{ items.length }}</span></div>
          <p class="panel-caption">角色为系统预定义，不可编辑，仅展示权限分配</p>
        </div>
        <div class="toolbar-actions">
          <n-button quaternary @click="load">刷新</n-button>
        </div>
      </div>
      <n-data-table
        :columns="columns"
        :data="items"
        :loading="loading"
        :row-key="(row: RecordData) => row.id ?? ''"
        :scroll-x="600"
        :bordered="false"
      />
      <div v-if="!loading && !items.length" class="empty-hint">
        当前没有角色数据。
      </div>
    </div>
    <!-- 展开的权限矩阵 -->
    <template v-for="item in items" :key="String(item.id)">
      <div v-if="expandedRoles.has(String(item.name))" class="panel permission-matrix">
        <div class="panel-toolbar">
          <div>
            <div class="panel-title">
              {{ item.name }} — 权限矩阵
            </div>
            <p class="panel-caption">按资源分组展示该角色拥有的权限点</p>
          </div>
        </div>
        <div class="matrix-body">
          <div v-for="group in permissionGroups" :key="group.key" class="matrix-group">
            <div class="matrix-group-label">{{ group.label }}</div>
            <n-checkbox-group :value="(item.permissions as string[]) || []" disabled>
              <div class="matrix-checkboxes">
                <n-checkbox
                  v-for="perm in group.permissions"
                  :key="perm"
                  :value="perm"
                  :checked="hasPermission(item, perm)"
                  disabled
                >
                  {{ perm }}
                </n-checkbox>
              </div>
            </n-checkbox-group>
          </div>
        </div>
      </div>
    </template>
  </div>
</template>

<style scoped>
/* 权限矩阵样式 */
.permission-matrix {
  margin-top: 16px;
}
.matrix-body {
  padding: 16px 24px;
}
.matrix-group {
  margin-bottom: 16px;
}
.matrix-group:last-child {
  margin-bottom: 0;
}
.matrix-group-label {
  font-size: 13px;
  font-weight: 700;
  color: #233946;
  margin-bottom: 8px;
  padding-bottom: 6px;
  border-bottom: 1px solid #edf0f0;
}
.matrix-checkboxes {
  display: flex;
  flex-wrap: wrap;
  gap: 8px 16px;
}
</style>
