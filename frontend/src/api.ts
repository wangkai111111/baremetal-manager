import axios, { AxiosError, InternalAxiosRequestConfig } from 'axios'
import { ref } from 'vue'
import { useStorage } from '@vueuse/core'
import dayjs from 'dayjs'
import { cloneDeep } from 'lodash-es'
import NProgress from 'nprogress'
import 'nprogress/nprogress.css'
import * as XLSX from 'xlsx'

// === NProgress 配置 ===
NProgress.configure({ showSpinner: false })

// === 全局 loading 状态 ===
// 用于全局加载指示器的响应式引用
export const globalLoading = ref(false)

// === 请求重试配置 ===
const MAX_RETRIES = 3
const RETRY_DELAY = 1000 // 毫秒

// === 请求取消/去重管理 ===
// 使用 Map 存储进行中的请求，key 为请求标识
const pendingRequests = new Map<string, AbortController>()

// 生成请求唯一标识
function generateRequestKey(config: InternalAxiosRequestConfig): string {
  const method = config.method || 'get'
  const url = config.url || ''
  const params = JSON.stringify(config.params || {})
  const data = JSON.stringify(config.data || {})
  return `${method}:${url}:${params}:${data}`
}

// 移除已完成的请求
function removePendingRequest(key: string) {
  const controller = pendingRequests.get(key)
  if (controller) {
    controller.abort()
    pendingRequests.delete(key)
  }
}

// === 延迟函数（用于重试间隔）===
function delay(ms: number): Promise<void> {
  return new Promise(resolve => setTimeout(resolve, ms))
}

export const api = axios.create({
  baseURL: '/api/v1',
  timeout: 30000
})

// === 请求拦截器 ===
api.interceptors.request.use(config => {
  // NProgress 开始
  NProgress.start()
  globalLoading.value = true

  // 请求去重：取消前一个相同请求
  const requestKey = generateRequestKey(config)
  removePendingRequest(requestKey)

  // 创建新的 AbortController
  const controller = new AbortController()
  config.signal = controller.signal
  pendingRequests.set(requestKey, controller)

  // 重试计数初始化
  if (!(config as InternalAxiosRequestConfig & { _retryCount?: number })._retryCount) {
    (config as InternalAxiosRequestConfig & { _retryCount?: number })._retryCount = 0
  }

  // Token 与项目 ID 注入
  const token = sessionStorage.getItem('bmm_token')
  if (token) config.headers.Authorization = `Bearer ${token}`
  const projectId = sessionStorage.getItem('bmm_project_id')
  if (
    projectId &&
    config.url &&
    /^\/(assets|tasks|images|alerts|audit|networks|ip-allocations|members|datacenters|racks|tags|alert-rules|alert-channels|kickstart-templates|ip-pools|firmware|install-plans|sol-sessions|pxe-config)(\/|$)/.test(config.url)
  ) {
    config.url = `/projects/${encodeURIComponent(projectId)}${config.url}`
  }
  return config
})

// === 响应拦截器 ===
api.interceptors.response.use(
  response => {
    // 请求成功，移除记录
    const requestKey = generateRequestKey(response.config)
    pendingRequests.delete(requestKey)
    NProgress.done()
    globalLoading.value = false
    return response
  },
  async (error: AxiosError<{ message?: string; error?: string }>) => {
    const config = error.config as (InternalAxiosRequestConfig & { _retryCount?: number }) | undefined

    // 移除失败的请求记录
    if (config) {
      const requestKey = generateRequestKey(config)
      pendingRequests.delete(requestKey)
    }

    NProgress.done()
    globalLoading.value = false

    // === 请求重试机制 ===
    // 对网络错误（无响应）自动重试，最多 3 次，间隔 1 秒
    if (config && !error.response && config._retryCount !== undefined && config._retryCount < MAX_RETRIES) {
      config._retryCount += 1
      await delay(RETRY_DELAY)
      return api.request(config)
    }

    // === 统一错误 toast 提示 ===
    const status = error.response?.status
    const errorMessage =
      error.response?.data?.message ||
      error.response?.data?.error ||
      error.message ||
      '请求失败'

    // 根据状态码显示不同的提示信息
    let toastMessage: string
    switch (status) {
      case 403:
        toastMessage = '权限不足'
        break
      case 404:
        toastMessage = '资源不存在'
        break
      case 500:
        toastMessage = '服务器错误，请稍后重试'
        break
      default:
        toastMessage = errorMessage
    }

    // 使用 useStorage 存储最后一次错误信息（供调试使用）
    const lastError = useStorage<string>('bmm_last_error', '')
    lastError.value = `[${dayjs().format('HH:mm:ss')}] ${toastMessage}`

    // 401 特殊处理：跳转到登录页
    if (status === 401 && !error.config?.url?.includes('/auth/login')) {
      sessionStorage.removeItem('bmm_token')
      if (window.location.pathname !== '/login') window.location.assign('/login')
    }

    return Promise.reject(new Error(toastMessage))
  }
)

export type RecordData = Record<string, unknown> & { id?: string | number }

export function unwrap<T = RecordData>(value: unknown): T {
  const data = (value as { data?: unknown }).data ?? value
  if (
    data &&
    typeof data === 'object' &&
    !Array.isArray(data) &&
    'data' in data &&
    !('token' in data) &&
    !('items' in data) &&
    !('id' in data)
  ) {
    return (data as { data: T }).data
  }
  return data as T
}

export function rows(value: unknown): { items: RecordData[]; total: number } {
  const result = unwrap<unknown>(value)
  if (Array.isArray(result)) {
    return { items: result as RecordData[], total: result.length }
  }
  const object = result as Record<string, unknown> | null
  const items = (object?.items ?? object?.list ?? object?.results ?? []) as RecordData[]
  return {
    items: Array.isArray(items) ? items : [],
    total: Number(object?.total ?? object?.count ?? items.length)
  }
}

export const resource = {
  list: (path: string, params?: Record<string, unknown>) => api.get(path, { params }),
  get: (path: string, id: string | number) => api.get(`${path}/${encodeURIComponent(id)}`),
  create: (path: string, payload: RecordData) => api.post(path, payload),
  update: (path: string, id: string | number, payload: RecordData) =>
    api.put(`${path}/${encodeURIComponent(id)}`, payload),
  remove: (path: string, id: string | number) =>
    api.delete(`${path}/${encodeURIComponent(id)}`)
}

// === Excel 导出辅助方法 ===
// 使用 xlsx 库将数据导出为 Excel 文件
// columns 参数指定要导出的列名
export async function exportToExcel(data: RecordData[], columns: string[], filename: string) {
  // 使用 cloneDeep 避免修改原始数据
  const clonedData = cloneDeep(data)

  // 提取指定列的数据
  const exportData = clonedData.map(row => {
    const newRow: Record<string, unknown> = {}
    for (const col of columns) {
      newRow[col] = row[col] ?? ''
    }
    return newRow
  })

  // 创建工作表和工作簿
  const worksheet = XLSX.utils.json_to_sheet(exportData)
  const workbook = XLSX.utils.book_new()
  XLSX.utils.book_append_sheet(workbook, worksheet, 'Sheet1')

  // 使用 dayjs 生成带时间戳的文件名
  const timestamp = dayjs().format('YYYYMMDD_HHmmss')
  const fullFilename = `${filename}_${timestamp}.xlsx`

  // 触发下载
  XLSX.writeFile(workbook, fullFilename)
}

// === 批量操作 API 封装 ===
export const batchOps = {
  // 批量电源操作
  power: (assetIds: number[], action: string) =>
    api.post('/assets/batch-actions', { asset_ids: assetIds, action }),
  // 批量扫描
  scan: (assetIds: number[]) =>
    api.post('/assets/scan', { asset_ids: assetIds }),
  // 批量删除
  remove: (path: string, ids: (string | number)[]) =>
    Promise.all(ids.map(id => resource.remove(path, id)))
}

export function errorText(error: unknown) {
  return error instanceof Error ? error.message : '请求失败'
}
