<script setup lang="ts">
import { computed, h, onBeforeUnmount, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { NAlert, NButton, NDataTable, NDatePicker, NDescriptions, NDescriptionsItem, NEmpty, NInput, NModal, NSelect, NSpace, NTag, useMessage } from 'naive-ui'
import type { DataTableColumns } from 'naive-ui'
import dayjs from 'dayjs'
import { useWindowSize } from '@vueuse/core'
import { api, errorText, rows, unwrap, type RecordData } from '../api'
import { useAuth } from '../stores/auth'
const auth = useAuth(), route = useRoute(), router = useRouter(), message = useMessage()
const search = ref(''), ip = ref(''), action = ref<string | null>(null), result = ref<string | null>(null)
const range = ref<[number, number] | null>(null), items = ref<RecordData[]>([]), total = ref(0), page = ref(1), pageSize = ref(20)
const loading = ref(false), exporting = ref(false), error = ref(''), detail = ref<RecordData | null>(null), showDetail = ref(false)
let sequence = 0, timer: ReturnType<typeof setTimeout> | undefined
const controller = ref<AbortController>()
const { width } = useWindowSize()
const department = computed(() => typeof route.query.department_id === 'string' ? route.query.department_id : '')
const actionOptions = [
  ['auth:login', '登录'], ['auth:logout', '退出登录'], ['auth:password-change', '修改登录密码'],
  ['create', '创建'], ['update', '更新'], ['delete', '删除'], ['submit', '提交运维 / 安装任务'], ['cancel', '取消任务'],
  ['initial-access:read', '查看初始登录信息'], ['sol:access', '打开远程控制台'],
  ['vm:', '测试 VM 电源操作'], ['task:', '任务执行结果'], ['user:', '用户账号管理'], ['pxe:', 'PXE 服务配置'], ['access:manage', '调整访问授权'], ['iam:', '账号与授权管理'], ['settings:write', '修改系统设置']
].map(([value, label]) => ({ value, label }))
const results = [{ value: 'success', label: '成功' }, { value: 'failed', label: '失败' }, { value: 'denied', label: '拒绝' }]
const text = (v: unknown) => v == null || v === '' ? '未记录' : String(v)
const resultName = (v: unknown) => results.find(r => r.value === v)?.label || text(v)
const fields: [string, string][] = [['operator_name','操作人'], ['action_label','操作'], ['target_type_label','对象类型'], ['target_name','操作对象'], ['project_name','所属项目'], ['scope_names','组织 / 部门范围'], ['client_ip','操作 IP'], ['outcome','结果'], ['occurred_at','发生时间'], ['description','说明']]
const display = (row: RecordData, key: string) => key === 'outcome' ? resultName(row[key]) : key === 'occurred_at' ? dayjs(String(row[key])).format('YYYY-MM-DD HH:mm:ss') : text(row[key])
const columns = computed<DataTableColumns<RecordData>>(() => [
  { title:'操作人', key:'operator_name', minWidth:120, render:row=>display(row,'operator_name') },
  { title:'操作', key:'action_label', minWidth:150, render:row=>display(row,'action_label') },
  { title:'操作对象', key:'target_name', minWidth:260, render:row=>h('div',[
    h('div',display(row,'target_name')),h('small',{class:'audit-secondary'},display(row,'target_type_label'))
  ]) },
  { title:'项目 / 组织部门', key:'scope_names', minWidth:190, render:row=>h('div',[
    h('div',display(row,'scope_names')),h('small',{class:'audit-secondary'},display(row,'project_name'))
  ]) },
  { title:'操作 IP', key:'client_ip', minWidth:135, render:row=>display(row,'client_ip') },
  { title:'结果', key:'outcome', minWidth:80, render:row=>h(NTag,{size:'small',bordered:false,type:row.outcome==='success'?'success':row.outcome==='denied'?'warning':'error'},()=>resultName(row.outcome)) },
  { title:'发生时间', key:'occurred_at', minWidth:170, render:row=>display(row,'occurred_at') },
  { title:'操作', key:'actions', width:70, fixed:'right', render:row=>h(NButton,{size:'small',text:true,type:'primary',onClick:()=>openDetail(row)},()=> '详情') }
])
function params() {
  return { search:search.value.trim() || undefined, ip:ip.value.trim() || undefined, action:action.value || undefined, result:result.value || undefined,
    from:range.value ? new Date(range.value[0]).toISOString() : undefined, to:range.value ? new Date(range.value[1]).toISOString() : undefined,
    department_id:department.value || undefined }
}
async function load() {
  const id = ++sequence
  controller.value?.abort(); controller.value = new AbortController()
  loading.value=true; error.value=''; items.value=[]
  try {
    const data=rows(await api.get('/audit',{params:{...params(),page:page.value,page_size:pageSize.value},signal:controller.value.signal}))
    if(id===sequence) {items.value=data.items;total.value=data.total}
  } catch(e) { if(id===sequence) {error.value=errorText(e);total.value=0} }
  finally {if(id===sequence) loading.value=false}
}
function schedule() {page.value=1;clearTimeout(timer);timer=setTimeout(load,300)}
function reset() {search.value='';ip.value='';action.value=null;result.value=null;range.value=null;page.value=1;schedule()}
async function openDetail(row:RecordData) {
  try {detail.value=unwrap(await api.get(`/audit/${row.id}`,{params:params()}));showDetail.value=true} catch(e) {message.error(errorText(e))}
}
async function download() {
  exporting.value=true
  try {
    const response=await api.get('/audit/export',{params:{...params(),format:'csv'},responseType:'blob'})
    const url=URL.createObjectURL(response.data), link=document.createElement('a')
    link.href=url;link.download=`审计日志_${dayjs().format('YYYYMMDD_HHmmss')}.csv`;link.click();setTimeout(()=>URL.revokeObjectURL(url),1000)
    message.success('已导出当前筛选条件下的日志（最多 10000 条）')
  } catch(e) {message.error(errorText(e))} finally {exporting.value=false}
}
watch([search,ip,action,result,range],schedule)
watch([()=>auth.projectId,department],()=>{page.value=1;detail.value=null;showDetail.value=false;load()},{immediate:true})
onBeforeUnmount(()=>{sequence++;clearTimeout(timer);controller.value?.abort()})
</script>

<template>
  <div class="audit-page">
    <div class="audit-heading">
      <div><h1>审计日志</h1><p>{{ auth.user?.is_platform_admin ? '查看全平台操作与登录轨迹' : '查看管理范围内的操作与登录轨迹' }}</p></div>
      <n-space><n-button :loading="loading" @click="load">刷新</n-button><n-button :loading="exporting" @click="download">导出日志</n-button></n-space>
    </div>
    <n-alert type="info" :show-icon="false">
      {{ auth.user?.is_platform_admin ? '平台管理员可查看全部记录。' : '仅显示当前组织中您有管理权限的部门及下级部门记录。' }}
      登录、退出按事件发生时的授权范围记录；运维任务的执行进度与最终结果可在任务中心查看。
    </n-alert>
    <div v-if="department" class="audit-department"><span>部门：{{ route.query.department_name || '所选部门' }}（含下级）</span><n-button size="small" @click="router.replace('/audit-logs')">清除部门筛选</n-button></div>
    <div class="panel audit-panel">
      <div class="audit-filters">
        <n-input v-model:value="search" clearable placeholder="搜索操作人、服务器名称、IP、操作" :input-props="{ 'aria-label': '搜索审计日志' }" />
        <n-input v-model:value="ip" clearable placeholder="操作人的 IP" :input-props="{ 'aria-label': '操作 IP' }" />
        <n-select v-model:value="action" :options="actionOptions" clearable placeholder="全部操作" aria-label="操作类型" />
        <n-select v-model:value="result" :options="results" clearable placeholder="全部结果" aria-label="操作结果" />
        <n-date-picker v-model:value="range" type="datetimerange" clearable start-placeholder="开始时间" end-placeholder="结束时间" />
        <n-button @click="reset">重置</n-button>
      </div>
      <n-alert v-if="error" type="error" style="margin-bottom:16px">{{ error }}<n-button text @click="load">重新加载</n-button></n-alert>
      <n-data-table :columns="columns" :data="items" :loading="loading" :scroll-x="1180" :row-key="row=>row.id" remote
        :pagination="{page,pageSize,simple:width<640,itemCount:total,showSizePicker:true,pageSizes:[20,50,100],onUpdatePage:(v:number)=>{page=v;load()},onUpdatePageSize:(v:number)=>{pageSize=v;page=1;load()}}">
        <template #empty><n-empty description="当前范围和筛选条件下暂无审计记录" /></template>
      </n-data-table>
    </div>
    <n-modal v-model:show="showDetail" preset="card" title="操作详情" style="width:min(720px,calc(100vw - 32px))">
      <n-descriptions v-if="detail" :column="1" label-placement="left" bordered>
        <n-descriptions-item v-for="[key,label] in fields" :key="key" :label="label"><span class="audit-detail-value">{{ display(detail,key) }}</span></n-descriptions-item>
      </n-descriptions>
    </n-modal>
  </div>
</template>

<style scoped>
.audit-page{display:grid;gap:18px;min-width:0}.audit-heading{display:flex;align-items:center;justify-content:space-between;gap:16px;flex-wrap:wrap}.audit-heading h1{font-size:24px;margin:0 0 6px;font-weight:600}.audit-heading p{margin:0;color:var(--color-text-secondary)}.audit-panel{padding:20px;min-width:0}.audit-filters{display:grid;grid-template-columns:minmax(230px,2fr) minmax(140px,1fr) minmax(170px,1fr) 130px;gap:12px;margin-bottom:20px}.audit-filters>:nth-child(5){grid-column:span 3}.audit-department{display:flex;justify-content:space-between;gap:12px;align-items:center;flex-wrap:wrap}.audit-detail-value{overflow-wrap:anywhere;white-space:pre-wrap}.audit-page :deep(.audit-secondary){color:var(--color-text-secondary);font-size:12px}.audit-page :deep(.n-data-table-td){overflow-wrap:anywhere}@media(max-width:900px){.audit-filters{grid-template-columns:1fr 1fr}.audit-filters>:nth-child(5){grid-column:span 2}}@media(max-width:520px){.audit-panel{padding:12px}.audit-filters{grid-template-columns:minmax(0,1fr)}.audit-filters>:nth-child(5){grid-column:auto}.audit-filters :deep(.n-date-picker){min-width:0}.audit-heading h1{font-size:22px}}
</style>
