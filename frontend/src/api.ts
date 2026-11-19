import axios, { type AxiosRequestConfig } from 'axios'
import { ref } from 'vue'
import dayjs from 'dayjs'
import { cloneDeep } from 'lodash-es'
import NProgress from 'nprogress'
import 'nprogress/nprogress.css'

NProgress.configure({ showSpinner: false })
export const globalLoading = ref(false)
const pending = new Set<AbortController>()
let contextVersion = 0
export function clearProjectRequests() {
  contextVersion++
  pending.forEach(controller => controller.abort())
  pending.clear()
  globalLoading.value = false
  NProgress.done()
}
const projectResources = new Set(['assets', 'operations', 'operation-logs', 'tasks', 'images', 'alerts', 'networks', 'members', 'datacenters', 'tags', 'alert-rules', 'alert-rule-channels', 'alert-channels', 'kickstart-templates', 'ip-pools', 'firmware-packages', 'firmware-baselines', 'firmware-upgrades', 'install-plans', 'sol-sessions', 'hardware', 'firmware-inventory', 'installations', 'installation-events', 'asset-tags', 'install-plan-scripts', 'monitor-configs', 'image-templates', 'partition-templates', 'post-install-scripts', 'sensor-records', 'asset-monitor-configs', 'maintenance-windows', 'roles', 'api-keys', 'departments', 'work-orders'])
export const api = axios.create({ baseURL: '/api/v1', timeout: 30000 })
type CreateOptions = { projectId: string; sensitive: true }
type ScopedConfig = { _expectedProjectId?: string; _expectedContextVersion?: number; _sensitive?: boolean }
export class AssetRegistrationError extends Error {
  constructor(public readonly outcome: 'failed' | 'unknown') {
    super(outcome === 'failed' ? '服务器拒绝登记（请检查权限、重复地址或字段约束）' : '未收到可靠结果，请核对资产列表，勿直接重试')
  }
}
api.interceptors.request.use(config => {
  const scoped = config as typeof config & ScopedConfig
  if (scoped._expectedProjectId !== undefined &&
    (scoped._expectedProjectId !== sessionStorage.getItem('bmm_project_id') || scoped._expectedContextVersion !== contextVersion)) {
    config.data = undefined
    throw new AssetRegistrationError('unknown')
  }
  const token = sessionStorage.getItem('bmm_token')
  if (token) config.headers.Authorization = `Bearer ${token}`
  const selectedProject = sessionStorage.getItem('bmm_project_id')
  if (selectedProject) config.headers['X-Project-ID'] = selectedProject
  const name = config.url?.split('/')[1] || ''
  if (projectResources.has(name)) {
    const projectId = sessionStorage.getItem('bmm_project_id')
    if (!projectId) throw new Error('请先选择组织与项目')
    config.url = `/projects/${encodeURIComponent(projectId)}${config.url}`
  }
  const controller = new AbortController()
  const callerSignal = config.signal
  const abort = () => controller.abort()
  if (callerSignal?.aborted) controller.abort()
  else callerSignal?.addEventListener?.('abort', abort, { once: true })
  config.signal = controller.signal
  Object.assign(config, { _controller: controller, _contextVersion: contextVersion, _cleanupSignal: () => callerSignal?.removeEventListener?.('abort', abort) })
  pending.add(controller)
  NProgress.start()
  globalLoading.value = true
  return config
})
function finish(config: unknown) {
  ;(config as { _cleanupSignal?: () => void } | undefined)?._cleanupSignal?.()
  const controller = (config as { _controller?: AbortController } | undefined)?._controller
  if (controller) pending.delete(controller)
  globalLoading.value = pending.size > 0
  if (!pending.size) NProgress.done()
}
api.interceptors.response.use(response => {
  finish(response.config)
  const sensitive = (response.config as typeof response.config & ScopedConfig)._sensitive
  if (sensitive) response.config.data = undefined
  if ((response.config as typeof response.config & { _contextVersion: number })._contextVersion !== contextVersion) {
    return Promise.reject(sensitive ? new AssetRegistrationError('unknown') : new Error('项目上下文已切换，请重新加载'))
  }
  const body = response.data
  if (body && typeof body === 'object' && 'code' in body && body.code !== 0) {
    return Promise.reject(sensitive ? new AssetRegistrationError('failed') : new Error(body.message || '请求失败'))
  }
  if (sensitive) response.data = undefined
  return response
}, error => {
  if (error instanceof AssetRegistrationError) return Promise.reject(error)
  finish(error.config)
  const sensitive = error.config?._sensitive
  if (sensitive) error.config.data = undefined
  const status = error.response?.status
  if (status === 403 && error.response?.data?.password_change_required && window.location.pathname !== '/change-password') {
    window.location.assign('/change-password')
  }
  if (status === 401 && !error.config?.url?.includes('/auth/login')) {
    clearProjectRequests()
    ;['bmm_token', 'bmm_project_id', 'bmm_tenant_id'].forEach(key => sessionStorage.removeItem(key))
    if (window.location.pathname !== '/login') window.location.assign('/login')
  }
  if (sensitive) return Promise.reject(new AssetRegistrationError(status >= 400 && status < 500 && status !== 408 ? 'failed' : 'unknown'))
  return Promise.reject(new Error(axios.isCancel(error) ? '请求已取消（页面或项目已切换）' : error.response?.data?.message || error.response?.data?.error || error.message || '请求失败'))
})

export type RecordData = Record<string, unknown> & { id?: string | number }
export function unwrap<T = RecordData>(value: unknown): T {
  let result = value as any
  if (result && typeof result === 'object' && 'config' in result && 'data' in result) result = result.data
  if (result && typeof result === 'object' && 'code' in result && 'data' in result) result = result.data
  else if (result && typeof result === 'object' && 'data' in result && !('id' in result) && !('items' in result)) result = result.data
  return result as T
}
export function rows(value: unknown): { items: RecordData[]; total: number; page: number; page_size: number } {
  const result = unwrap<any>(value)
  const items = Array.isArray(result) ? result : result?.items ?? []
  return { items: Array.isArray(items) ? items : [], total: Number(result?.total ?? items.length), page: Number(result?.page ?? 1), page_size: Number(result?.page_size ?? items.length) }
}
export const resource = {
  list: async (path: string, params?: Record<string, unknown>) => {
    const response = await api.get(path, { params })
    if (path === '/accounts') {
      const account = unwrap<RecordData>(response)
      const items = account?.id != null ? [account] : []
      response.data = { code: 0, data: { items, total: items.length, page: 1, page_size: 1 } }
    }
    if (path === '/settings') {
      const settings = rows(response)
      response.data = { code: 0, data: { ...settings, items: settings.items.map(item => ({ ...item, value: typeof item.value === 'string' ? item.value : JSON.stringify(item.value) })) } }
    }
    return response
  },
  get: (path: string, id: string | number) => api.get(`${path}/${encodeURIComponent(id)}`),
  create: (path: string, payload: RecordData, options?: CreateOptions) => {
    if (!options) return api.post(path, payload)
    const config: AxiosRequestConfig & ScopedConfig = { _expectedProjectId: options.projectId, _expectedContextVersion: contextVersion, _sensitive: true }
    return api.post(path, payload, config).then(() => undefined)
  },
  update: (path: string, id: string | number, payload: RecordData) => {
    if (path === '/settings') return api.put(path, { [id]: payload.value })
    return api.put(`${path}/${encodeURIComponent(id)}`, payload)
  },
  remove: (path: string, id: string | number) => api.delete(`${path}/${encodeURIComponent(id)}`)
}
export async function exportToExcel(data: RecordData[], columns: string[], filename: string) {
  const XLSX = await import('xlsx')
  const exportData = cloneDeep(data).map(row => Object.fromEntries(columns.map(col => [col, row[col] ?? ''])))
  const workbook = XLSX.utils.book_new()
  XLSX.utils.book_append_sheet(workbook, XLSX.utils.json_to_sheet(exportData), 'Sheet1')
  XLSX.writeFile(workbook, `${filename}_${dayjs().format('YYYYMMDD_HHmmss')}.xlsx`)
}
export const batchOps = {
  power: (assetIds: number[], action: string, key: string = crypto.randomUUID()) => api.post('/assets/batch-actions', { asset_ids: assetIds, action, idempotency_key: key }),
  scan: (assetIds: number[]) => Promise.all(assetIds.map(id => api.post(`/assets/${id}/collect`))),
  remove: (path: string, ids: (string | number)[]) => Promise.all(ids.map(id => resource.remove(path, id)))
}
export interface CatalogImage {
  id: string
  name: string
  os_type: string
  version: string
  architecture: string
  filename: string
  source_url: string
  status: string
  downloaded_bytes: number
  total_bytes: number
  error?: string | null
  image_id?: number | null
}
export const imageApi = {
  catalog: (signal?: AbortSignal) => api.get('/images/catalog', { signal }),
  list: (page: number, signal?: AbortSignal) => api.get('/images', { params: { page, page_size: 20, visibility: 'private' }, signal }),
  download: (id: string, source: string, signal?: AbortSignal) => {
    const config: AxiosRequestConfig & ScopedConfig = { signal, _expectedProjectId: sessionStorage.getItem('bmm_project_id') || '', _expectedContextVersion: contextVersion }
    return api.post(`/images/catalog/${encodeURIComponent(id)}/download`, { source }, config)
  },
  upload: (data: FormData, signal: AbortSignal, onUploadProgress: AxiosRequestConfig['onUploadProgress']) => {
    const config: AxiosRequestConfig & ScopedConfig = { signal, timeout: 0, onUploadProgress, _expectedProjectId: sessionStorage.getItem('bmm_project_id') || '', _expectedContextVersion: contextVersion }
    return api.post('/images', data, config)
  }
}
export function errorText(error: unknown) { return error instanceof Error ? error.message : '请求失败' }
export const taskStatuses = ['pending', 'running', 'retry_wait', 'succeeded', 'failed', 'cancelled', 'timeout', 'unknown']
export function taskStatusType(status: unknown): 'success' | 'warning' | 'error' | 'info' | 'default' {
  if (status === 'succeeded') return 'success'
  if (status === 'failed' || status === 'timeout') return 'error'
  if (status === 'running') return 'info'
  if (status === 'pending' || status === 'retry_wait') return 'warning'
  return 'default'
}

// ============================================================
export interface Envelope<T> { code: number; message: string; data: T }
export interface Paged<T> { items: T[]; total: number; page: number; page_size: number }

// 把 axios 响应收敛成具名分页结构，避免调用方使用 Record<string, unknown> 或 any。
export function paged<T>(value: unknown): Paged<T> {
  const result = rows(value)
  return { items: result.items as unknown as T[], total: result.total, page: result.page, page_size: result.page_size }
}

// --- 工单 ---
export type WorkOrderType = 'fault' | 'technical' | 'consultation' | 'other'
export type WorkOrderStatus = 'open' | 'in_progress' | 'resolved' | 'closed'
export type WorkOrderPriority = 'low' | 'medium' | 'high' | 'urgent'
export type WorkOrderTransition = 'in_progress' | 'resolved' | 'closed'
export interface WorkOrderRecord {
  id: number
  tenant_id: number
  user_id: number
  type: WorkOrderType
  title: string
  description?: string | null
  status: WorkOrderStatus
  priority: WorkOrderPriority
  assigned_to?: number | null
  submitter_name?: string | null
  assignee_name?: string | null
  resolved_at?: string | null
  closed_at?: string | null
  created_at?: string
  updated_at?: string
}
export interface WorkOrderListQuery {
  status?: WorkOrderStatus | ''
  type?: WorkOrderType | ''
  priority?: WorkOrderPriority | ''
  assigned_to?: number | ''
  user_id?: number | ''
  search?: string
  page?: number
  page_size?: number
}
export interface WorkOrderCreateInput {
  type: WorkOrderType
  title: string
  description?: string
  priority?: WorkOrderPriority
  assigned_to?: number | null
}
export interface WorkOrderUpdateInput {
  title?: string
  description?: string
  priority?: WorkOrderPriority
  assigned_to?: number | null
}

// === Setup API ===
export const setupApi = {
  getStatus: () => api.get('/setup/status'),
  setConfig: (config: Record<string, any>) => api.post('/setup/config', config)
}

// === Department API ===
export const departmentApi = resource
export const departmentStats = (id: string) => api.get(`/departments/${id}/stats`)

// === Depreciation API ===
// 折旧记录表没有 project_id，列表接口由后端通过所属资产限定组织与项目作用域。
export const depreciationApi = {
  list: (params?: Record<string, unknown>) => api.get('/assets/depreciation', { params }),
  get: (assetId: string | number) => api.get(`/assets/${assetId}/depreciation`),
  warrantyAlerts: (days?: number) => api.get('/assets/warranty-alerts', { params: { days } })
}

// === Asset Assignment API ===
// 后端 department_id 为空或 0 时表示解除资产的部门归属。
function assignmentTarget(departmentId: string | number | null | undefined): number | null {
  const id = Number(departmentId)
  return Number.isFinite(id) && id > 0 ? id : null
}
export const assetAssignApi = {
  assign: (assetId: string | number, departmentId: string | number | null) =>
    api.post(`/assets/${assetId}/assign`, { department_id: assignmentTarget(departmentId) }),
  batchAssign: (assetIds: (string | number)[], departmentId: string | number | null) =>
    api.post('/assets/batch-assign', {
      asset_ids: assetIds.map(id => Number(id)).filter(id => Number.isFinite(id)),
      department_id: assignmentTarget(departmentId)
    })
}

// === Work Order API ===
// 工单协作接口。
export const workOrderApi = {
  list: (params?: WorkOrderListQuery) => api.get<Envelope<Paged<WorkOrderRecord>>>('/work-orders', { params }),
  get: (id: string | number) => api.get<Envelope<WorkOrderRecord>>(`/work-orders/${encodeURIComponent(id)}`),
  create: (data: WorkOrderCreateInput) => api.post<Envelope<{ id: number }>>('/work-orders', data),
  update: (id: string | number, data: WorkOrderUpdateInput) =>
    api.put<Envelope<null>>(`/work-orders/${encodeURIComponent(id)}`, data),
  // transition 走路径参数：open → in_progress|closed、in_progress → resolved|closed、
  // resolved → closed|in_progress
  transition: (id: string | number, action: WorkOrderTransition) =>
    api.put<Envelope<{ id: number; status: WorkOrderStatus }>>(`/work-orders/${encodeURIComponent(id)}/${action}`)
}
