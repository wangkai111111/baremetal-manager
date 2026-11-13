<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { NAlert, NButton, NCard, NEmpty, NForm, NFormItem, NInput, NModal, NPagination, NProgress, NSelect, NSpace, NSpin, NTabPane, NTabs, NTag, useMessage } from 'naive-ui'
import { imageApi, errorText, rows, unwrap, type RecordData } from '../api'
import { useAuth } from '../stores/auth'

type ImageSource = { id: string; name: string; url: string }
type ImageEntry = { id: string; name: string; version: string; architecture: string; filename: string; source: string; sources: ImageSource[]; source_url: string; status: string; downloaded_bytes: number; total_bytes: number; error?: string; image_id?: number }
const selectedSources = ref<Record<string, string>>({})
function sourceId(item: ImageEntry) { return selectedSources.value[item.id] || item.source || 'official' }
function sourceURL(item: ImageEntry) { return item.sources.find(source => source.id === sourceId(item))?.url || item.source_url }
const auth = useAuth()
const message = useMessage()
const tab = ref('public')
const catalog = ref<ImageEntry[]>([])
const images = ref<RecordData[]>([])
const total = ref(0)
const page = ref(1)
const loading = ref(false)
const failure = ref('')
const submitting = ref('')
const showUpload = ref(false)
const uploading = ref(false)
const uploadProgress = ref(0)
const file = ref<File | null>(null)
const osType = ref('ubuntu')
const version = ref('')
const architecture = ref('x86_64')
const canWrite = computed(() => auth.can('images:write'))
const busy = (status: string) => ['queued', 'downloading', 'verifying'].includes(status)
const labels: Record<string, string> = { available: '待下载', queued: '排队中', downloading: '下载中', verifying: '校验与存储中', ready: '可用', failed: '失败', uploading: '上传中' }
const statusLabel = (value: unknown) => labels[String(value)] || String(value || '未知')
function bytes(value: unknown) {
  const n = Number(value)
  if (!Number.isFinite(n) || n <= 0) return '0 B'
  const unit = Math.min(Math.floor(Math.log(n) / Math.log(1024)), 3)
  return `${(n / 1024 ** unit).toFixed(unit ? 2 : 0)} ${['B', 'KiB', 'MiB', 'GiB'][unit]}`
}
function percent(item: ImageEntry) { return item.total_bytes > 0 ? Math.min(100, Math.round(item.downloaded_bytes / item.total_bytes * 100)) : 0 }
let controller = new AbortController()
let uploadController: AbortController | undefined
let timer: ReturnType<typeof setTimeout> | undefined
let generation = 0
let disposed = false
function stopPoll() { if (timer) clearTimeout(timer); timer = undefined }
async function load(silent = false) {
  const current = generation
  stopPoll()
  if (!auth.projectId || disposed) return
  if (!silent) loading.value = true
  try {
    const [publicResult, privateResult] = await Promise.all([imageApi.catalog(controller.signal), imageApi.list(page.value, controller.signal)])
    if (current !== generation || disposed) return
    catalog.value = unwrap<ImageEntry[]>(publicResult)
    for (const item of catalog.value) {
      if (busy(item.status) || item.status === 'ready' || !selectedSources.value[item.id]) selectedSources.value[item.id] = item.source
    }
    const result = rows(privateResult)
    images.value = result.items
    total.value = result.total
    failure.value = ''
  } catch (e) {
    if (current === generation && !disposed) failure.value = errorText(e)
  } finally {
    if (current === generation && !disposed) {
      loading.value = false
      if (catalog.value.some(item => busy(item.status))) timer = setTimeout(() => load(true), 2500)
    }
  }
}
async function download(item: ImageEntry) {
  const current = generation
  submitting.value = item.id
  try {
    await imageApi.download(item.id, sourceId(item), controller.signal)
    if (current !== generation || disposed) return
    message.success('下载任务已提交到平台服务器')
    await load(true)
  } catch (e) { if (current === generation && !disposed) message.error(errorText(e)) }
  finally { if (current === generation) submitting.value = '' }
}
function chooseFile(event: Event) { file.value = (event.target as HTMLInputElement).files?.[0] || null }
async function upload() {
  if (!file.value || !osType.value.trim() || !version.value.trim()) { message.warning('请选择镜像文件并填写系统与版本'); return }
  if (file.value.size <= 0 || file.value.size > 20 * 1024 ** 3) { message.warning('镜像大小必须大于 0 且不超过 20 GiB'); return }
  const current = generation
  const body = new FormData()
  body.append('file', file.value)
  body.append('os_type', osType.value.trim())
  body.append('version', version.value.trim())
  body.append('architecture', architecture.value)
  uploadController = new AbortController()
  uploading.value = true
  uploadProgress.value = 0
  try {
    await imageApi.upload(body, uploadController.signal, event => {
      if (current === generation) uploadProgress.value = event.total ? Math.min(100, Math.round(event.loaded / event.total * 100)) : 0
    })
    if (current !== generation || disposed) return
    message.success('镜像已上传到当前项目')
    showUpload.value = false
    file.value = null
    version.value = ''
    page.value = 1
    await load()
  } catch (e) { if (current === generation && !disposed) message.error(errorText(e)) }
  finally { if (current === generation) uploading.value = false }
}
watch(() => auth.projectId, () => {
  generation++
  controller.abort()
  uploadController?.abort()
  controller = new AbortController()
  stopPoll()
  catalog.value = []
  selectedSources.value = {}
  images.value = []
  total.value = 0
  page.value = 1
  showUpload.value = false
  uploading.value = false
  submitting.value = ''
  failure.value = ''
  void load()
}, { immediate: true })
onBeforeUnmount(() => { disposed = true; generation++; stopPoll(); controller.abort(); uploadController?.abort() })
</script>

<template>
  <section class="image-repository">
    <header class="repository-heading">
      <div><h1>镜像仓库</h1><p>按需下载官方镜像，或上传项目自有镜像。镜像文件存储在平台服务器。</p></div>
      <n-button :loading="loading" @click="load()">刷新</n-button>
    </header>
    <n-alert v-if="failure" type="error" title="仓库加载失败" class="repository-alert">{{ failure }}。请确认服务已更新，并重试。</n-alert>
    <n-alert v-if="!auth.projectId" type="info">请先选择项目。</n-alert>
    <n-spin :show="loading">
      <n-tabs v-model:value="tab" type="line" animated>
        <n-tab-pane name="public" tab="公有镜像">
          <p class="section-note">官方镜像目录仅内置链接，不占用镜像存储。点击下载后由服务器获取文件，完整性校验通过后才标记可用。</p>
          <n-empty v-if="!catalog.length && !loading" description="暂无公有镜像目录" />
          <n-card v-for="item in catalog" :key="item.id" class="catalog-card">
            <div class="catalog-top"><div><span class="vendor">UBUNTU · SERVER</span><h2>{{ item.name }}</h2><p>{{ item.architecture }} · ISO 安装介质</p></div><n-tag :type="item.status === 'ready' ? 'success' : item.status === 'failed' ? 'error' : 'info'" :bordered="false">{{ statusLabel(item.status) }}</n-tag></div>
            <div class="source"><label :for="`source-${item.id}`">镜像来源</label><n-select :id="`source-${item.id}`" class="source-select" :value="sourceId(item)" :options="item.sources.map(source => ({ label: source.name, value: source.id }))" :disabled="busy(item.status) || item.status === 'ready' || submitting === item.id" @update:value="value => selectedSources[item.id] = value" /><a :href="sourceURL(item)" target="_blank" rel="noopener noreferrer">{{ item.filename }}</a></div>
            <div v-if="busy(item.status)" class="download-progress"><n-progress type="line" :percentage="percent(item)" :processing="true" :show-indicator="item.total_bytes > 0" /><p>{{ bytes(item.downloaded_bytes) }} / {{ item.total_bytes > 0 ? bytes(item.total_bytes) : '等待获取文件大小' }} · {{ statusLabel(item.status) }}</p></div>
            <n-alert v-if="item.error" type="error" class="repository-alert">{{ item.error }}</n-alert>
            <div class="catalog-bottom"><span>{{ item.status === 'ready' ? '已完成校验并存入当前项目' : '下载到平台服务器 · SHA256 完整性校验' }}</span><n-button v-if="canWrite" type="primary" :loading="submitting === item.id" :disabled="busy(item.status) || item.status === 'ready'" @click="download(item)">{{ item.status === 'ready' ? '已可用' : item.status === 'failed' ? '重新下载' : busy(item.status) ? statusLabel(item.status) : '下载到服务器' }}</n-button></div>
          </n-card>
        </n-tab-pane>
        <n-tab-pane name="private" tab="私有镜像">
          <div class="private-heading"><p class="section-note">仅显示当前项目自行上传的镜像；公有目录下载的镜像保留在公有镜像中，不会重复列出。</p><n-button v-if="canWrite" type="primary" @click="showUpload = true">上传镜像</n-button></div>
          <n-empty v-if="!images.length && !loading" description="当前项目尚无私有镜像，请上传镜像文件" />
          <div v-else class="image-table-wrap"><table class="image-table"><thead><tr><th>镜像名称</th><th>系统 / 版本</th><th>架构</th><th>大小</th><th>状态</th></tr></thead><tbody><tr v-for="image in images" :key="String(image.id)"><td>{{ image.name }}</td><td>{{ image.os_type }} / {{ image.version }}</td><td>{{ image.architecture }}</td><td>{{ bytes(image.size_bytes) }}</td><td><n-tag :type="image.status === 'ready' ? 'success' : image.status === 'failed' ? 'error' : 'info'" :bordered="false">{{ statusLabel(image.status) }}</n-tag></td></tr></tbody></table></div>
          <n-pagination v-if="total > 20" v-model:page="page" :page-size="20" :item-count="total" class="pagination" @update:page="load()" />
        </n-tab-pane>
      </n-tabs>
    </n-spin>
    <n-modal v-model:show="showUpload" preset="card" title="上传私有镜像" style="width: min(540px, 94vw)" :mask-closable="!uploading" :close-on-esc="!uploading" :closable="!uploading">
      <n-form label-placement="top" @submit.prevent="upload">
        <n-form-item label="镜像文件（最大 20 GiB）"><input type="file" :disabled="uploading" @change="chooseFile" /></n-form-item>
        <n-form-item label="操作系统"><n-input v-model:value="osType" placeholder="例如 ubuntu" :disabled="uploading" /></n-form-item>
        <n-form-item label="版本"><n-input v-model:value="version" placeholder="例如 22.04.5" :disabled="uploading" /></n-form-item>
        <n-form-item label="架构"><n-select v-model:value="architecture" :options="[{ label: 'x86_64 (amd64)', value: 'x86_64' }, { label: 'aarch64 (arm64)', value: 'aarch64' }]" :disabled="uploading" /></n-form-item>
        <div v-if="uploading"><n-progress type="line" :percentage="uploadProgress" :processing="true" /><p class="section-note">{{ uploadProgress === 100 ? '文件已传输，等待服务器完成存储，请勿关闭页面。' : '正在上传到平台服务器，请勿关闭页面。' }}</p></div>
        <n-space justify="end"><n-button :disabled="uploading" @click="showUpload = false">取消</n-button><n-button type="primary" attr-type="submit" :loading="uploading" :disabled="!canWrite || !file">上传</n-button></n-space>
      </n-form>
    </n-modal>
  </section>
</template>

<style scoped>
.image-repository { padding: 24px; }
.repository-heading, .catalog-top, .catalog-bottom, .private-heading { display: flex; justify-content: space-between; gap: 20px; align-items: center; flex-wrap: wrap; }
h1 { font-size: 24px; margin: 0 0 8px; }
h2 { font-size: 21px; margin: 8px 0; }
p { margin: 6px 0; }
.repository-heading p, .section-note, .catalog-top p, .catalog-bottom > span, .download-progress p { color: var(--text-secondary, #8793a5); }
.repository-heading { margin-bottom: 24px; }
.repository-alert { margin: 16px 0; }
.section-note { line-height: 1.8; margin: 12px 0 20px; }
.catalog-card { max-width: 840px; margin: 16px 0; }
.vendor { font-size: 11px; letter-spacing: 2px; color: var(--text-secondary, #8793a5); }
.source { display: flex; align-items: center; gap: 16px; flex-wrap: wrap; margin: 24px 0; font-size: 13px; }
.source-select { width: 190px; max-width: 100%; }
.source a { color: var(--primary-color, #4098fc); overflow-wrap: anywhere; }
.catalog-bottom { border-top: 1px solid var(--border-color, #8883); padding-top: 20px; font-size: 13px; }
.download-progress { margin-bottom: 20px; }
.image-table-wrap { overflow-x: auto; }
.image-table { width: 100%; border-collapse: collapse; text-align: left; }
.image-table th, .image-table td { padding: 16px; border-bottom: 1px solid var(--border-color, #8883); }
.image-table td:first-child { min-width: 200px; overflow-wrap: anywhere; }
.image-table th { color: var(--text-secondary, #8793a5); font-weight: 500; white-space: nowrap; }
.pagination { justify-content: flex-end; margin-top: 20px; }
@media (max-width: 600px) { .image-repository { padding: 16px; } .catalog-bottom .n-button { width: 100%; } }
</style>
