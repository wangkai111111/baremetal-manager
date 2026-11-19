<script setup lang="ts">
import { computed, h, reactive, ref, watch } from 'vue'
import { NAlert, NButton, NDataTable, NForm, NFormItem, NInput, NModal, NPopconfirm, NSelect, NSpace, NSwitch, useMessage, type DataTableColumns } from 'naive-ui'
import dayjs from 'dayjs'
import { errorText, resource, rows, type RecordData } from '../api'
import { useAuth } from '../stores/auth'
import { metricOptions, severityOptions, operatorOptions, healthOptions, channelName, metricName, severityName, conditionName } from '../utils/notificationOptions'
type RuleRow = RecordData & { name?: string; metric?: string; operator?: string; threshold?: number | null; severity?: string; enabled?: boolean; channel_ids?: number[]; selector?: {tags?: string[]}; created_at?: string }
const message = useMessage(), auth = useAuth()
const canManage = computed(() => auth.can('alerts:write'))
const items = ref<RuleRow[]>([]), channels = ref<RecordData[]>([]), tags = ref<RecordData[]>([])
const loading = ref(false), saving = ref(false), opening = ref(false), editing = ref(false), total = ref(0), page = ref(1), pageSize = 20
const currentId = ref<string | number | null>(null), toggling = ref<string | number | null>(null)
const form = reactive({name:'', metric:'bmc.collection_failed', operator:'collection_failed', threshold:null as number | null, severity:'warning', enabled:true, channel_ids:[] as number[], tags:[] as string[]})
const channelChoices = computed(() => {
  const choices = channels.value.map(row => ({label:`${row.name} · ${channelName(row.kind)}${row.enabled ? '' : '（已停用）'}`, value:Number(row.id)}))
  for (const id of form.channel_ids) if (!choices.some(row => row.value === id)) choices.push({label:'已删除的渠道（请取消选择）', value:id})
  return choices
})
const tagChoices = computed(() => [...new Set([...tags.value.map(row => String(row.name)), ...form.tags])].map(name => ({label:name, value:name})))
const columns = computed<DataTableColumns<RuleRow>>(() => [
  {title:'规则名称',key:'name',minWidth:160}, {title:'监控指标',key:'metric',minWidth:160,render:row=>metricName(row.metric)},
  {title:'通知条件',key:'condition',minWidth:180,render:row=>conditionName(row)}, {title:'最低通知等级',key:'severity',minWidth:140,render:row=>severityName(row.severity)},
  {title:'通知渠道',key:'channel_ids',minWidth:180,render:row=>(row.channel_ids || []).map((id:number)=>channels.value.find(c=>Number(c.id)===id)?.name || '已删除渠道').join('、') || '未关联渠道'},
  {title:'设备范围',key:'selector',minWidth:140,render:row=>row.selector?.tags?.length ? `包含全部标签：${row.selector.tags.join('、')}` : '当前项目全部设备'},
  {title:'启用状态',key:'enabled',minWidth:100,render:row=>h(NSwitch,{value:!!row.enabled,disabled:!canManage.value || toggling.value!==null,loading:toggling.value===row.id,'aria-label':`启用规则 ${row.name}`,onUpdateValue:(value:boolean)=>toggle(row,value)})},
  {title:'创建时间',key:'created_at',minWidth:160,render:row=>row.created_at ? dayjs(row.created_at).format('YYYY-MM-DD HH:mm') : '—'},
  ...(canManage.value ? [{title:'操作',key:'actions',width:130,fixed:'right' as const,render:(row:RuleRow)=>h(NSpace,{},()=>[h(NButton,{text:true,disabled:opening.value,onClick:()=>openEdit(row)},()=> '编辑'),h(NPopconfirm,{onPositiveClick:()=>remove(row)}, {trigger:()=>h(NButton,{text:true,type:'error'},()=> '删除'),default:()=>`删除“${row.name}”及渠道关联？历史告警记录会保留。`})])}] : [])
])
async function all(path:string) {
  const result:RecordData[]=[]
  for(let p=1;;p++){ const data=rows(await resource.list(path,{page:p,page_size:200}));result.push(...data.items);if(!data.items.length || result.length>=data.total)return result }
}
async function load(){
  loading.value=true
  try{const [data, channelRows]=await Promise.all([resource.list('/alert-rules',{page:page.value,page_size:pageSize}),all('/alert-channels')]);const list=rows(data);items.value=list.items as RuleRow[];total.value=list.total;channels.value=channelRows}
  catch(e){message.error(errorText(e))}finally{loading.value=false}
}
watch(()=>auth.projectId,()=>{editing.value=false;items.value=[];channels.value=[];page.value=1;void load()},{immediate:true})
async function openEdit(row?:RuleRow){
  if(!canManage.value || opening.value)return
  opening.value=true
  try{
    const [channelRows,tagRows]=await Promise.all([all('/alert-channels'),auth.can('tags:read') ? all('/tags') : Promise.resolve([])])
    channels.value=channelRows;tags.value=tagRows;currentId.value=row?.id ?? null
    Object.assign(form,{name:row?.name || '',metric:row?.metric || 'bmc.collection_failed',operator:row?.operator || 'collection_failed',threshold:row?.threshold ?? null,severity:row?.severity || 'warning',enabled:row?.enabled ?? true,channel_ids:[...(row?.channel_ids || [])].map(Number),tags:[...(row?.selector?.tags || [])]})
    editing.value=true
  }catch(e){message.error(errorText(e))}finally{opening.value=false}
}
function selectMetric(metric:string){form.metric=metric;form.operator=metric==='bmc.health' ? 'gte' : 'collection_failed';form.threshold=metric==='bmc.health' ? 1 : null}
async function toggle(row:RuleRow,enabled:boolean){
  toggling.value=row.id!
  try{await resource.update('/alert-rules',row.id!,{enabled});await load();message.success(enabled ? '规则已启用':'规则已停用')}catch(e){message.error(errorText(e))}finally{toggling.value=null}
}
async function save(){
  if(!canManage.value || saving.value)return
  if(!form.name.trim()){message.warning('请填写规则名称');return}
  if(form.channel_ids.some(id=>!channels.value.some(row=>Number(row.id)===id))){message.warning('请移除已删除的通知渠道');return}
  saving.value=true
  try{
    const payload={name:form.name.trim(),metric:form.metric,operator:form.operator,threshold:form.metric==='bmc.collection_failed' ? null : form.threshold,duration_seconds:0,severity:form.severity,enabled:form.enabled,selector:form.tags.length ? {tags:form.tags} : {},channel_ids:form.channel_ids}
    if(currentId.value===null)await resource.create('/alert-rules',payload);else await resource.update('/alert-rules',currentId.value,payload)
    editing.value=false;message.success('规则和通知渠道已保存');await load()
  }catch(e){message.error(errorText(e))}finally{saving.value=false}
}
async function remove(row:RuleRow){try{await resource.remove('/alert-rules',row.id!);if(items.value.length===1 && page.value>1)page.value--;await load();message.success('规则已删除')}catch(e){message.error(errorText(e))}}
</script>
<template>
  <div class="page"><div class="page-head"><div><div class="eyebrow dark">MONITORING / ALERT RULES</div><h1>告警规则</h1><p>选择内置监控事件的通知条件、设备范围与接收渠道</p></div><NButton v-if="canManage" type="primary" :loading="opening" @click="openEdit()">新建规则</NButton></div>
    <NAlert type="info" :bordered="false" class="rule-notice">设备健康异常或 BMC 连续采集失败 2 次时，内置监控会记录告警。这里配置哪些事件需要通知、发给谁；持续异常不重复发送，恢复时通知原接收渠道。</NAlert>
    <div class="panel"><div class="panel-toolbar"><div class="panel-title">规则列表 <span class="count">{{ total }}</span></div><NButton quaternary :disabled="loading" @click="load">刷新</NButton></div><NDataTable remote :columns="columns" :data="items" :loading="loading" :row-key="row=>row.id" :scroll-x="1350" :pagination="{page,pageSize,itemCount:total,onUpdatePage:(value:number)=>{page=value;load()}}" /></div>
    <NModal v-model:show="editing" preset="card" :title="currentId===null ? '新建告警规则':'编辑告警规则'" class="alert-rule-modal" :style="{width:'min(720px, calc(100vw - 32px))',maxHeight:'calc(100dvh - 32px)'}" content-style="min-height:0; overflow-y:auto;" :mask-closable="!saving" :closable="!saving">
      <NForm label-placement="top" :disabled="saving"><div class="resource-form-grid">
        <NFormItem label="规则名称" required class="form-item-wide"><NInput v-model:value="form.name" placeholder="例如：机房 A 严重健康告警" :maxlength="255" /></NFormItem>
        <NFormItem label="监控指标" required><NSelect :value="form.metric" :options="metricOptions" @update:value="selectMetric" /></NFormItem>
        <NFormItem label="最低通知等级" required><NSelect v-model:value="form.severity" :options="severityOptions" /></NFormItem>
        <template v-if="form.metric==='bmc.health'"><NFormItem label="比较条件" required><NSelect v-model:value="form.operator" :options="operatorOptions" /></NFormItem><NFormItem label="健康阈值" required><NSelect v-model:value="form.threshold" :options="healthOptions" /></NFormItem></template>
        <NAlert v-else type="info" :bordered="false" class="form-item-wide">匹配 BMC 连续采集失败事件：连续失败 2 次时产生，后续采集成功时恢复，无需手填指标名称和阈值。</NAlert>
        <NFormItem label="设备标签（留空选择当前项目全部设备）" class="form-item-wide"><NSelect v-model:value="form.tags" multiple filterable clearable :options="tagChoices" placeholder="选择标签；多项必须全部满足" :disabled="saving || (!auth.can('tags:read') && !form.tags.length)" /></NFormItem>
        <NFormItem label="通知渠道" class="form-item-wide"><NSelect v-model:value="form.channel_ids" multiple filterable clearable :options="channelChoices" placeholder="选择一个或多个推送渠道" /></NFormItem>
        <NAlert v-if="!channels.length" type="warning" :bordered="false" class="form-item-wide">当前项目还没有推送渠道，请先到“推送渠道”创建接收目标。也可以先保存规则，稍后编辑关联。</NAlert>
        <NAlert v-else-if="form.channel_ids.some(id=>channels.some(row=>Number(row.id)===id && !row.enabled))" type="warning" :bordered="false" class="form-item-wide">所选渠道中有已停用的渠道；启用后才会发送通知。</NAlert>
        <NFormItem label="启用规则"><NSwitch v-model:value="form.enabled" /></NFormItem>
      </div></NForm>
      <template #footer><NSpace justify="end"><NButton :disabled="saving" @click="editing=false">取消</NButton><NButton type="primary" :loading="saving" :disabled="!form.name.trim() || !metricOptions.some(row=>row.value===form.metric)" @click="save">保存规则</NButton></NSpace></template>
    </NModal>
  </div>
</template>
<style scoped>.rule-notice { margin-bottom:20px; }.alert-rule-modal :deep(.n-form-item) { min-width:0; }</style>
