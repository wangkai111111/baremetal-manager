<script setup lang="ts">
// API 文档页面 — 解析 OpenAPI 规范并按标签分组展示接口
import { computed, onMounted, ref } from 'vue'
import { NEmpty, NInput, NTag } from 'naive-ui'

const METHODS = ['get', 'post', 'put', 'patch', 'delete'] as const

interface Parameter {
  name?: string
  in?: string
  required?: boolean
  description?: string
  schema?: Record<string, unknown>
}

interface MediaType {
  schema?: Record<string, unknown>
}

interface RequestBody {
  required?: boolean
  description?: string
  content?: Record<string, MediaType>
}

interface ResponseItem {
  description?: string
  content?: Record<string, MediaType>
}

interface Operation {
  summary?: string
  description?: string
  tags?: string[]
  parameters?: Parameter[]
  requestBody?: RequestBody
  responses?: Record<string, ResponseItem>
  security?: unknown[]
}

interface PathItem {
  parameters?: Parameter[]
  [method: string]: unknown
}

interface Spec {
  info?: { title?: string; version?: string; description?: string }
  paths?: Record<string, PathItem>
  components?: {
    schemas?: Record<string, Record<string, unknown>>
    securitySchemes?: Record<string, Record<string, unknown>>
  }
  security?: unknown[]
}

interface ApiOperation {
  method: string
  path: string
  summary: string
  description: string
  tags: string[]
  parameters: Parameter[]
  requestBody?: RequestBody
  responses: Array<{ status: string; description: string; schema: string }>
  security: string
}

const spec = ref<Spec>({})
const loading = ref(true)
const failed = ref(false)
const keyword = ref('')

// 从 $ref 中提取模型名称
function refName(schema?: Record<string, unknown>) {
  const ref = schema?.['$ref']
  return typeof ref === 'string' ? ref.split('/').pop() || '' : ''
}

function schemaText(schema?: Record<string, unknown>) {
  if (!schema) return '未定义'
  const name = refName(schema)
  if (name) return name
  const type = schema.type
  return typeof type === 'string' ? type : 'object'
}

function bodySchema(body?: RequestBody) {
  if (!body?.content) return ''
  const first = Object.values(body.content)[0]
  return schemaText(first?.schema)
}

function responseSchema(response: ResponseItem) {
  if (!response.content) return ''
  const first = Object.values(response.content)[0]
  return schemaText(first?.schema)
}

// 汇总所有接口，合并路径级参数
const operations = computed<ApiOperation[]>(() => {
  const result: ApiOperation[] = []
  const globalSecurity = Array.isArray(spec.value.security) && spec.value.security.length > 0
  Object.entries(spec.value.paths || {}).forEach(([path, item]) => {
    METHODS.forEach(method => {
      const operation = item[method] as Operation | undefined
      if (!operation) return
      const parameters = [...(item.parameters || []), ...(operation.parameters || [])]
      const responses = Object.entries(operation.responses || {}).map(([status, response]) => ({
        status,
        description: response?.description || '',
        schema: responseSchema(response || {})
      }))
      result.push({
        method: method.toUpperCase(),
        path,
        summary: operation.summary || operation.description || '',
        description: operation.description || '',
        tags: operation.tags?.length ? operation.tags : ['其他接口'],
        parameters,
        requestBody: operation.requestBody,
        responses,
        security: operation.security
          ? operation.security.length
            ? '需要认证'
            : '公开接口'
          : globalSecurity
            ? '需要认证'
            : '公开接口'
      })
    })
  })
  return result
})

const filtered = computed(() => {
  const term = keyword.value.trim().toLowerCase()
  if (!term) return operations.value
  return operations.value.filter(op =>
    [op.path, op.summary, op.description, op.tags.join(' ')]
      .join(' ')
      .toLowerCase()
      .includes(term)
  )
})

// 按标签分组，保持规范中的出现顺序
const groups = computed(() => {
  const map = new Map<string, ApiOperation[]>()
  filtered.value.forEach(op => {
    op.tags.forEach(tag => {
      if (!map.has(tag)) map.set(tag, [])
      map.get(tag)?.push(op)
    })
  })
  return Array.from(map.entries()).map(([label, items]) => ({ label, items }))
})

function methodClass(method: string) {
  return `method-${method.toLowerCase()}`
}

function parameterType(parameter: Parameter) {
  const type = parameter.schema?.type
  return typeof type === 'string' ? type : 'string'
}

onMounted(async () => {
  try {
    const response = await fetch('/api/v1/openapi.json')
    if (!response.ok) throw new Error('failed')
    spec.value = await response.json()
  } catch {
    failed.value = true
  } finally {
    loading.value = false
  }
})
</script>

<template>
  <div class="page">
    <div class="page-head">
      <div>
        <div class="eyebrow dark">DEVELOPER / API REFERENCE</div>
        <h1>API 文档</h1>
        <p>基于 OpenAPI 规范自动生成的接口参考，可直接用于客户端集成</p>
      </div>
      <div class="head-actions">
        <a class="doc-download" href="/api/v1/openapi.yaml" download>
          <n-button quaternary>下载 OpenAPI YAML</n-button>
        </a>
      </div>
    </div>

    <div class="panel api-docs">
      <div class="panel-toolbar">
        <div>
          <div class="panel-title">
            {{ spec.info?.title || 'Baremetal Manager API' }}
            <n-tag size="small" :bordered="false">{{ spec.info?.version || '1.0.0' }}</n-tag>
          </div>
          <p class="panel-caption">
            共 {{ operations.length }} 个接口 · 认证方式 Bearer JWT 或 X-API-Key
          </p>
        </div>
        <div class="toolbar-actions">
          <n-input
            v-model:value="keyword"
            placeholder="搜索路径、说明或标签"
            clearable
            style="width: 240px"
          />
        </div>
      </div>

      <div v-if="loading" class="doc-loading">正在加载接口规范…</div>
      <div v-else-if="failed" class="doc-loading">
        接口规范加载失败，可尝试
        <a href="/api/v1/openapi.yaml" download>下载原始 YAML</a>
      </div>
      <n-empty v-else-if="!filtered.length" description="没有匹配的接口" style="padding: 40px" />

      <div v-else class="doc-groups">
        <section v-for="group in groups" :key="group.label" class="doc-group">
          <div class="doc-group-head">
            <h2>{{ group.label }}</h2>
            <span>{{ group.items.length }} 个接口</span>
          </div>

          <article v-for="op in group.items" :key="op.method + op.path" class="doc-op">
            <div class="doc-op-head">
              <span class="method" :class="methodClass(op.method)">{{ op.method }}</span>
              <code class="doc-path">{{ op.path }}</code>
              <span class="doc-auth">{{ op.security }}</span>
            </div>
            <p v-if="op.summary" class="doc-summary">{{ op.summary }}</p>
            <p v-if="op.description && op.description !== op.summary" class="doc-desc">
              {{ op.description }}
            </p>

            <div v-if="op.parameters.length" class="doc-meta">
              <span class="doc-label">参数</span>
              <span v-for="(parameter, index) in op.parameters" :key="index" class="doc-chip">
                {{ parameter.name }}
                <i>{{ parameter.in }}</i>
                <i>{{ parameterType(parameter) }}</i>
                <em v-if="parameter.required">必填</em>
              </span>
            </div>

            <div v-if="op.requestBody" class="doc-meta">
              <span class="doc-label">请求体</span>
              <span class="doc-chip">
                {{ bodySchema(op.requestBody) }}
                <em v-if="op.requestBody?.required">必填</em>
              </span>
            </div>

            <div v-if="op.responses.length" class="doc-meta">
              <span class="doc-label">响应</span>
              <span v-for="response in op.responses" :key="response.status" class="doc-chip">
                {{ response.status }}
                <i>{{ response.description }}</i>
                <i v-if="response.schema">{{ response.schema }}</i>
              </span>
            </div>
          </article>
        </section>
      </div>
    </div>
  </div>
</template>

<style scoped>
.api-docs {
  padding-bottom: 8px
}

.doc-loading {
  padding: 40px 24px;
  color: #7c8f99;
  font-size: 13px
}

.doc-groups {
  padding: 4px 24px 20px
}

.doc-group {
  padding-top: 18px
}

.doc-group-head {
  display: flex;
  align-items: baseline;
  gap: 10px;
  padding: 8px 0 12px;
  border-bottom: 1px solid #edf0f0
}

.doc-group-head h2 {
  margin: 0;
  font-size: 14px;
  font-weight: 700;
  color: #233946
}

.doc-group-head span {
  font-size: 11px;
  color: #95a3ab
}

.doc-op {
  padding: 14px 0 16px;
  border-bottom: 1px dashed #e9eded
}

.doc-op-head {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 10px
}

.method {
  display: inline-block;
  min-width: 58px;
  padding: 3px 8px;
  border-radius: 4px;
  font-size: 11px;
  font-weight: 700;
  text-align: center;
  letter-spacing: .4px
}

.method-get {
  background: #e8f3f4;
  color: #1f7a82
}

.method-post {
  background: #e9f2ff;
  color: #2362a5
}

.method-put,
.method-patch {
  background: #fff3e2;
  color: #a5650e
}

.method-delete {
  background: #fdeaea;
  color: #b13b3b
}

.doc-path {
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 12px;
  color: #233946;
  word-break: break-all
}

.doc-auth {
  margin-left: auto;
  font-size: 11px;
  color: #7c8f99
}

.doc-summary {
  margin: 8px 0 0;
  font-size: 13px;
  color: #3c525e
}

.doc-desc {
  margin: 6px 0 0;
  font-size: 12px;
  line-height: 1.65;
  color: #687e89
}

.doc-meta {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 6px;
  margin-top: 9px
}

.doc-label {
  min-width: 42px;
  font-size: 11px;
  color: #95a3ab
}

.doc-chip {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: 3px 8px;
  border: 1px solid #e4ebeb;
  border-radius: 4px;
  background: #fafcfc;
  font-size: 11px;
  color: #40545e
}

.doc-chip i {
  font-style: normal;
  color: #95a3ab
}

.doc-chip em {
  font-style: normal;
  color: #b13b3b
}

.doc-download {
  text-decoration: none
}

@media (max-width: 820px) {
  .doc-groups {
    padding: 4px 18px 16px
  }

  .doc-auth {
    margin-left: 0;
    width: 100%
  }
}
</style>
