import axios, { type AxiosRequestConfig } from 'axios'
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
const projectResources = new Set(['assets', 'operations', 'operation-logs', 'tasks', 'images', 'alerts', 'audit', 'networks', 'ip-allocations', 'members', 'datacenters', 'tags', 'alert-rules', 'alert-channels', 'kickstart-templates', 'ip-pools', 'firmware-packages', 'firmware-baselines', 'firmware-upgrades', 'install-plans', 'sol-sessions', 'hardware', 'firmware-inventory', 'installations', 'installation-events', 'asset-tags', 'install-plan-scripts', 'monitor-configs', 'pxe-configs', 'image-templates', 'partition-templates', 'post-install-scripts', 'sensor-records', 'asset-monitor-configs', 'maintenance-windows', 'roles', 'api-keys', 'departments', 'products', 'orders', 'accounts', 'payments', 'bills', 'invoices', 'coupons', 'work-orders'])
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
    if (!projectId) throw new Error('请先选择租户与项目')
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
  paySubscription: (orderId: number) => api.post(`/orders/${orderId}/pay-balance`),
  power: (assetIds: number[], action: string, key: string = crypto.randomUUID()) => api.post('/assets/batch-actions', { asset_ids: assetIds, action, idempotency_key: key }),
  renew: (assetIds: number[], cycle: string, count: number, key: string) => api.post('/assets/subscription/batch-renew', { asset_ids: assetIds, cycle, count, request_key: key }),
  unsubscribe: (assetIds: number[], key: string) => api.post('/assets/subscription/batch-unsubscribe', { asset_ids: assetIds, request_key: key }),
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

// ============================================================
// 运营侧（计费 / 财务 / 营销 / 工单）契约类型
// 枚举值与 backend/migrations/004_mode_system.sql 的 CHECK 约束逐字对齐，
// payments.method 另含 010_subscription_balance_payment.sql 扩展的 balance。
// 列表响应统一为 {items,total,page,page_size}，外层再包 {code,message,data}。
// ============================================================
export interface Envelope<T> { code: number; message: string; data: T }
export interface Paged<T> { items: T[]; total: number; page: number; page_size: number }

// 把 axios 响应收敛成具名分页结构，避免调用方使用 Record<string, unknown> 或 any。
export function paged<T>(value: unknown): Paged<T> {
  const result = rows(value)
  return { items: result.items as unknown as T[], total: result.total, page: result.page, page_size: result.page_size }
}

// --- 账户与充值 ---
export interface AccountRecord {
  id: number
  tenant_id: number
  balance: number
  frozen_amount: number
  credit_limit: number
  created_at?: string
  updated_at?: string
}
export type RechargeMethod = 'alipay' | 'wechat' | 'transfer'
export interface RechargeInput { amount: number; method: RechargeMethod }
export interface RechargeResult { message: string; amount: number }

// --- 支付流水 ---
export type PaymentMethod = RechargeMethod | 'balance'
export type PaymentStatus = 'pending' | 'success' | 'failed'
export interface PaymentRecord {
  id: number
  account_id: number
  amount: number
  method: PaymentMethod
  status: PaymentStatus
  transaction_id?: string | null
  paid_at?: string | null
  created_at?: string
  updated_at?: string
}

// --- 账单 ---
export type BillStatus = 'unpaid' | 'paid' | 'overdue'
export interface BillRecord {
  id: number
  tenant_id: number
  period_start: string
  period_end: string
  total_amount: number
  status: BillStatus
  due_date?: string | null
  created_at?: string
  updated_at?: string
}
export interface BillListQuery { status?: BillStatus | ''; page?: number; page_size?: number }
export interface BillGenerateInput { period_start: string; period_end: string; tenant_id?: number }
export interface BillGenerateResult { message: string; generated: number }

// --- 发票 ---
export type InvoiceType = 'normal' | 'special'
export type InvoiceStatus = 'requested' | 'issued' | 'cancelled'
export type InvoiceTransition = 'issued' | 'cancelled'
export interface InvoiceRecord {
  id: number
  tenant_id: number
  bill_id?: number | null
  type: InvoiceType
  title: string
  tax_number: string
  amount: number
  status: InvoiceStatus
  issued_at?: string | null
  created_at?: string
  updated_at?: string
}
export interface InvoiceListQuery { status?: InvoiceStatus | ''; page?: number; page_size?: number }
// 后端 createInvoice 要求 title 非空且 amount > 0，type 缺省为 normal
export interface InvoiceCreateInput {
  bill_id?: number | null
  type: InvoiceType
  title: string
  tax_number: string
  amount: number
}

// --- 优惠券 ---
export type CouponType = 'fixed' | 'percent'
export type CouponStatus = 'active' | 'inactive' | 'expired'
export interface CouponRecord {
  id: number
  tenant_id?: number | null
  code: string
  name: string
  type: CouponType
  value: number
  min_amount?: number
  max_discount?: number | null
  valid_from?: string | null
  valid_until?: string | null
  usage_limit?: number | null
  used_count?: number
  status: CouponStatus
  created_at?: string
  updated_at?: string
}
export interface CouponListQuery {
  status?: CouponStatus | ''
  type?: CouponType | ''
  search?: string
  page?: number
  page_size?: number
}
// valid_from / valid_until 必须为 RFC3339，usage_limit 为空表示不限次
export interface CouponCreateInput {
  code: string
  name: string
  type: CouponType
  value: number
  min_amount?: number
  max_discount?: number | null
  valid_from?: string
  valid_until?: string
  usage_limit?: number | null
  tenant_id?: number | null
}
export type CouponUpdateInput = Partial<Omit<CouponCreateInput, 'code' | 'type' | 'tenant_id'>> & { status?: CouponStatus }

// POST /coupons/:code/redeem 返回兑换成功的最小快照（非完整券对象）
export interface CouponRedeemResult {
  id: number
  code: string
  name: string
  type: CouponType
  value: number
  min_amount: number
  redeemed_at: string
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
// 折旧记录表没有 project_id，列表接口由后端通过所属资产限定租户与项目作用域。
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

// === Product API ===
export const productApi = resource

// === Order API ===
// 后端 orderTransitions：pending → paid|cancelled、paid → processing|refunded、processing → completed。
// 订阅单（payable_cents 非空）禁止手动流转，只能走 POST /orders/:id/pay-balance。
export type OrderStatus = 'pending' | 'paid' | 'processing' | 'completed' | 'refunded' | 'cancelled'
export type OrderType = 'purchase' | 'renew' | 'upgrade' | 'refund'
export type OrderTransition = 'paid' | 'processing' | 'completed' | 'cancelled' | 'refunded'
export interface OrderTransitionBody { asset_id?: number }
export const orderApi = {
  ...resource,
  // 后端订单流转注册为 PUT /orders/:id/:transition，POST 会命中 NoRoute 返回 404；
  // body 可选 asset_id，仅平台管理员 + completed + 购买单时用于绑定交付资产。
  transition: (id: string | number, action: OrderTransition, body?: OrderTransitionBody) =>
    api.put<Envelope<{ id: number; status: OrderStatus }>>(`/orders/${encodeURIComponent(id)}/${action}`, body),
  // GET /orders/:id/export 为 JSON 附件（order + product + payments[]）
  exportOrder: (id: string | number) => api.get<Blob>(`/orders/${encodeURIComponent(id)}/export`, { responseType: 'blob' })
}

// === Account API ===
export const accountApi = {
  get: () => api.get<Envelope<AccountRecord>>('/accounts'),
  // 后端 rechargeAccount 仅接受 alipay|wechat|transfer，balance 属支付流水方式而非充值渠道
  recharge: (data: RechargeInput) => api.post<Envelope<RechargeResult>>('/accounts/recharge', data)
}

// === Payment API ===
export const paymentApi = resource

// === Bill API ===
export const billApi = {
  ...resource,
  // period_start / period_end 必须为 RFC3339，且 period_end 需晚于 period_start
  generate: (data: BillGenerateInput) => api.post<Envelope<BillGenerateResult>>('/bills/generate', data),
  exportBill: (id: string) => api.get<Blob>(`/bills/${id}/export`, { responseType: 'blob' })
}

// === Invoice API ===
// 后端仅提供 GET /invoices、POST /invoices 与 PUT /invoices/:id/:status（流转），
// 不存在 GET/PUT/DELETE /invoices/:id，任何按 id 读改删的请求都会落到 404 NoRoute。
export const invoiceApi = {
  list: (params?: InvoiceListQuery) => api.get<Envelope<Paged<InvoiceRecord>>>('/invoices', { params }),
  create: (data: InvoiceCreateInput) => api.post<Envelope<{ id: number }>>('/invoices', data),
  // transition 走路径参数：requested → issued|cancelled；issued → cancelled
  transition: (id: string | number, status: InvoiceTransition) =>
    api.put<Envelope<{ id: number; status: InvoiceStatus }>>(`/invoices/${encodeURIComponent(id)}/${status}`)
}

// === Coupon API ===
export const couponApi = {
  list: (params?: CouponListQuery) => api.get<Envelope<Paged<CouponRecord>>>('/coupons', { params }),
  create: (data: CouponCreateInput) => api.post<Envelope<{ id: number }>>('/coupons', data),
  update: (id: string | number, data: CouponUpdateInput) =>
    api.put<Envelope<null>>(`/coupons/${encodeURIComponent(id)}`, data),
  remove: (id: string | number) => api.delete<Envelope<null>>(`/coupons/${encodeURIComponent(id)}`),
  // 兑换需要 orders:write；已过期会由后端把 status 置为 expired 并返回 400「优惠券已过期」
  redeem: (code: string) => api.post<Envelope<CouponRedeemResult>>(`/coupons/${encodeURIComponent(code)}/redeem`)
}

// === Work Order API ===
// 工单路由无模式门禁，自用 / 运营两种模式均可访问。
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

// === Operations Dashboard API ===
export const operationsApi = {
  getDashboard: () => api.get('/admin/operations-dashboard')
}

// === Auth Register API ===
export const authRegister = (data: any) => api.post('/auth/register', data)
