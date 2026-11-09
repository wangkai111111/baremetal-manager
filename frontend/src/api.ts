import axios from 'axios'
import { ref } from 'vue'
import dayjs from 'dayjs'
import { cloneDeep } from 'lodash-es'
import NProgress from 'nprogress'
import 'nprogress/nprogress.css'
import * as XLSX from 'xlsx'

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
const projectResources = new Set(['assets', 'tasks', 'images', 'alerts', 'audit', 'networks', 'ip-allocations', 'members', 'datacenters', 'racks', 'tags', 'alert-rules', 'alert-channels', 'kickstart-templates', 'ip-pools', 'firmware-packages', 'firmware-baselines', 'firmware-upgrades', 'install-plans', 'sol-sessions', 'hardware', 'firmware-inventory', 'installations', 'installation-events', 'asset-tags', 'install-plan-scripts', 'monitor-configs', 'pxe-configs', 'image-templates', 'partition-templates', 'post-install-scripts', 'sensor-records', 'asset-monitor-configs', 'maintenance-windows', 'roles', 'api-keys', 'departments', 'products', 'orders', 'payments', 'bills', 'invoices', 'coupons', 'work-orders'])
export const api = axios.create({ baseURL: '/api/v1', timeout: 30000 })
api.interceptors.request.use(config => {
  const token = sessionStorage.getItem('bmm_token')
  if (token) config.headers.Authorization = `Bearer ${token}`
  const selectedProject = sessionStorage.getItem('bmm_project_id')
  if (selectedProject) config.headers['X-Project-ID'] = selectedProject
  const name = config.url?.split('/')[1] || ''
  if (projectResources.has(name)) {
    const projectId = sessionStorage.getItem('bmm_project_id')
    if (!projectId) throw new Error('请先选择租户与项目')
    config.url = `/projects/${encodeURIComponent(projectId)}${config.url}`
  }
  const controller = new AbortController()
  config.signal = controller.signal
  Object.assign(config, { _controller: controller, _contextVersion: contextVersion })
  pending.add(controller)
  NProgress.start()
  globalLoading.value = true
  return config
})
function finish(config: unknown) {
  const controller = (config as { _controller?: AbortController } | undefined)?._controller
  if (controller) pending.delete(controller)
  globalLoading.value = pending.size > 0
  if (!pending.size) NProgress.done()
}
api.interceptors.response.use(response => {
  finish(response.config)
  if ((response.config as typeof response.config & { _contextVersion: number })._contextVersion !== contextVersion) {
    return Promise.reject(new Error('项目上下文已切换，请重新加载'))
  }
  const body = response.data
  if (body && typeof body === 'object' && 'code' in body && body.code !== 0) {
    return Promise.reject(new Error(body.message || '请求失败'))
  }
  return response
}, error => {
  finish(error.config)
  const status = error.response?.status
  if (status === 401 && !error.config?.url?.includes('/auth/login')) {
    clearProjectRequests()
    ;['bmm_token', 'bmm_project_id', 'bmm_tenant_id'].forEach(key => sessionStorage.removeItem(key))
    if (window.location.pathname !== '/login') window.location.assign('/login')
  }
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
    if (path === '/settings') {
      const settings = rows(response)
      response.data = { code: 0, data: { ...settings, items: settings.items.map(item => ({ ...item, value: typeof item.value === 'string' ? item.value : JSON.stringify(item.value) })) } }
    }
    return response
  },
  get: (path: string, id: string | number) => api.get(`${path}/${encodeURIComponent(id)}`),
  create: (path: string, payload: RecordData) => api.post(path, payload),
  update: (path: string, id: string | number, payload: RecordData) => {
    if (path === '/settings') return api.put(path, { [id]: id === 'app.footer_visible' ? payload.value === true || payload.value === 'true' : payload.value })
    return api.put(`${path}/${encodeURIComponent(id)}`, payload)
  },
  remove: (path: string, id: string | number) => api.delete(`${path}/${encodeURIComponent(id)}`)
}
export async function exportToExcel(data: RecordData[], columns: string[], filename: string) {
  const exportData = cloneDeep(data).map(row => Object.fromEntries(columns.map(col => [col, row[col] ?? ''])))
  const workbook = XLSX.utils.book_new()
  XLSX.utils.book_append_sheet(workbook, XLSX.utils.json_to_sheet(exportData), 'Sheet1')
  XLSX.writeFile(workbook, `${filename}_${dayjs().format('YYYYMMDD_HHmmss')}.xlsx`)
}
export const batchOps = {
  power: (assetIds: number[], action: string) => Promise.all(assetIds.map(id => api.post(`/assets/${id}/actions`, { action }))),
  scan: (assetIds: number[]) => Promise.all(assetIds.map(id => api.post(`/assets/${id}/collect`))),
  remove: (path: string, ids: (string | number)[]) => Promise.all(ids.map(id => resource.remove(path, id)))
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

// === Setup API ===
export const setupApi = {
  getStatus: () => api.get('/setup/status'),
  setMode: (mode: string) => api.post('/setup/mode', { mode }),
  setConfig: (config: Record<string, any>) => api.post('/setup/config', config)
}

// === Department API ===
export const departmentApi = resource
export const departmentStats = (id: string) => api.get(`/departments/${id}/stats`)

// === Depreciation API ===
export const depreciationApi = {
  get: (assetId: string) => api.get(`/assets/${assetId}/depreciation`),
  warrantyAlerts: (days?: number) => api.get('/assets/warranty-alerts', { params: { days } })
}

// === Asset Assignment API ===
export const assetAssignApi = {
  assign: (assetId: string, departmentId: string) => api.post(`/assets/${assetId}/assign`, { department_id: departmentId }),
  batchAssign: (assetIds: string[], departmentId: string) => api.post('/assets/batch-assign', { asset_ids: assetIds, department_id: departmentId })
}

// === Product API ===
export const productApi = resource

// === Order API ===
export const orderApi = {
  ...resource,
  transition: (id: string, action: string) => api.post(`/orders/${id}/${action}`),
  exportOrder: (id: string) => api.get(`/orders/${id}/export`, { responseType: 'blob' })
}

// === Account API ===
export const accountApi = {
  get: () => api.get('/accounts'),
  recharge: (data: any) => api.post('/accounts/recharge', data)
}

// === Payment API ===
export const paymentApi = resource

// === Bill API ===
export const billApi = {
  ...resource,
  generate: (data: any) => api.post('/bills/generate', data),
  exportBill: (id: string) => api.get(`/bills/${id}/export`, { responseType: 'blob' })
}

// === Invoice API ===
export const invoiceApi = {
  ...resource,
  updateStatus: (id: string, status: string) => api.put(`/invoices/${id}/${status}`)
}

// === Coupon API ===
export const couponApi = {
  ...resource,
  redeem: (code: string) => api.post(`/coupons/${code}/redeem`)
}

// === Work Order API ===
export const workOrderApi = {
  ...resource,
  transition: (id: string, action: string) => api.put(`/work-orders/${id}/${action}`)
}

// === Operations Dashboard API ===
export const operationsApi = {
  getDashboard: () => api.get('/admin/operations-dashboard')
}

// === Auth Register API ===
export const authRegister = (data: any) => api.post('/auth/register', data)
