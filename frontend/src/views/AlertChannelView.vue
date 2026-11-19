<script setup lang="ts">
import { computed, h, reactive, ref, watch } from 'vue'
import { NAlert, NButton, NDataTable, NForm, NFormItem, NInput, NInputNumber, NModal, NPopconfirm, NSelect, NSpace, NSwitch, NTag, useMessage, type DataTableColumns } from 'naive-ui'
import dayjs from 'dayjs'
import { api, errorText, resource, rows, type RecordData } from '../api'
import { useAuth } from '../stores/auth'
import { channelTypes, channelName, channelDraft, channelPayload } from '../utils/notificationOptions'
type ChannelRow = RecordData & {name?: string; kind?: string; enabled?: boolean; config?: {to?: string; host?: string}; has_secret?: boolean; last_test_at?: string; last_test_status?: string; last_test_message?: string}
const message=useMessage(),auth=useAuth()
const canManage=computed(()=>auth.can('alerts:write'))
const items=ref<ChannelRow[]>([]), total=ref(0), page=ref(1), pageSize=20, loading=ref(false), saving=ref(false), editing=ref(false)
const currentId=ref<string|number|null>(null), testing=ref<string|number|null>(null), toggling=ref<string|number|null>(null)
const originalKind=ref(''),hasSecret=ref(false),form=reactive(channelDraft())
const savedSecret=computed(()=>hasSecret.value && form.kind===originalKind.value)
const webhookPlaceholders:Record<string,string>={wechat:'https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=…',dingtalk:'https://oapi.dingtalk.com/robot/send?access_token=…',lark:'https://open.feishu.cn/open-apis/bot/v2/hook/…',webhook:'https://example.com/your-webhook'}
const testState=(status:unknown)=>({succeeded:'服务端已接受',failed:'测试失败',untested:'尚未测试'}[String(status)] || '尚未测试')
const columns=computed<DataTableColumns<ChannelRow>>(()=>[
  {title:'渠道名称',key:'name',minWidth:150},{title:'类型',key:'kind',minWidth:120,render:row=>h(NTag,{size:'small',bordered:false},()=>channelName(row.kind))},
  {title:'接收目标',key:'target',minWidth:220,render:row=>row.kind==='email' ? `${row.config?.to || '未填写邮箱'} · ${row.config?.host || '未填写服务器'}` : row.has_secret ? 'Webhook 地址已加密保存' : '尚未设置 Webhook 地址'},
  {title:'启用状态',key:'enabled',minWidth:100,render:row=>h(NSwitch,{value:!!row.enabled,disabled:!canManage.value || toggling.value!==null,loading:toggling.value===row.id,'aria-label':`启用渠道 ${row.name}`,onUpdateValue:(value:boolean)=>toggle(row,value)})},
  {title:'最近测试',key:'last_test_status',minWidth:130,render:row=>h(NTag,{size:'small',type:row.last_test_status==='succeeded'?'success':row.last_test_status==='failed'?'error':'default'},()=>testState(row.last_test_status))},
  {title:'测试时间',key:'last_test_at',minWidth:160,render:row=>row.last_test_at ? dayjs(row.last_test_at).format('YYYY-MM-DD HH:mm') : '—'},
  {title:'测试说明',key:'last_test_message',minWidth:250,render:row=>row.last_test_message || '保存后可手动发送一条测试消息'},
  ...(canManage.value ? [{title:'操作',key:'actions',fixed:'right' as const,width:175,render:(row:ChannelRow)=>h(NSpace,{},()=>[
    h(NButton,{text:true,type:'info',loading:testing.value===row.id,disabled:testing.value!==null && testing.value!==row.id,onClick:()=>testChannel(row)},()=> '测试'),
    h(NButton,{text:true,onClick:()=>openEdit(row)},()=> '编辑'),
    h(NPopconfirm,{onPositiveClick:()=>remove(row)},{trigger:()=>h(NButton,{text:true,type:'error'},()=> '删除'),default:()=>`删除“${row.name}”？如有规则引用，请先在规则中解除关联。`})])}] : [])
])
async function load(){loading.value=true;try{const data=rows(await resource.list('/alert-channels',{page:page.value,page_size:pageSize}));items.value=data.items as ChannelRow[];total.value=data.total}catch(e){message.error(errorText(e))}finally{loading.value=false}}
watch(()=>auth.projectId,()=>{editing.value=false;items.value=[];page.value=1;void load()},{immediate:true})
function openEdit(row?:ChannelRow){if(!canManage.value)return;currentId.value=row?.id ?? null;Object.assign(form,channelDraft(row));originalKind.value=row?.kind || '';hasSecret.value=!!row?.has_secret;editing.value=true}
function changeKind(kind:string){form.kind=kind;form.secret=''}
async function toggle(row:ChannelRow,enabled:boolean){toggling.value=row.id!;try{await resource.update('/alert-channels',row.id!,{name:row.name,enabled});await load();message.success(enabled?'渠道已启用':'渠道已停用')}catch(e){message.error(errorText(e))}finally{toggling.value=null}}
async function testChannel(row:ChannelRow){
  if(testing.value!==null)return
  testing.value=row.id!
  try{const result=await api.post(`/alert-channels/${row.id}/test`);message.success(result.data?.data?.message || '测试消息已被服务端接受，请检查接收端')}
  catch(e){message.error(errorText(e))}finally{testing.value=null;await load()}
}
async function save(){
  if(!canManage.value || saving.value)return
  let payload
  try{payload=channelPayload(form,savedSecret.value)}catch(e){message.warning(errorText(e));return}
  saving.value=true
  try{if(currentId.value===null)await resource.create('/alert-channels',payload);else await resource.update('/alert-channels',currentId.value,payload);editing.value=false;form.secret='';message.success('渠道已保存');await load()}
  catch(e){message.error(errorText(e))}finally{saving.value=false}
}
async function remove(row:ChannelRow){try{await resource.remove('/alert-channels',row.id!);if(items.value.length===1 && page.value>1)page.value--;await load();message.success('渠道已删除')}catch(e){message.error(errorText(e))}}
</script>
<template>
  <div class="page"><div class="page-head"><div><div class="eyebrow dark">MONITORING / ALERT CHANNELS</div><h1>推送渠道</h1><p>选择通知方式，填写接收目标，在告警规则中关联使用</p></div><NButton v-if="canManage" type="primary" @click="openEdit()">新建渠道</NButton></div>
    <NAlert type="info" :bordered="false" class="channel-notice">渠道只负责发送通知；请在“告警规则”中选择需要使用的渠道。测试会发送一条标记为 TEST 的消息，保存渠道不会自动发送。</NAlert>
    <div class="panel"><div class="panel-toolbar"><div class="panel-title">渠道列表 <span class="count">{{ total }}</span></div><NButton quaternary :disabled="loading" @click="load">刷新</NButton></div><NDataTable remote :columns="columns" :data="items" :loading="loading" :row-key="row=>row.id" :scroll-x="1320" :pagination="{page,pageSize,itemCount:total,onUpdatePage:(value:number)=>{page=value;load()}}" /></div>
    <NModal v-model:show="editing" preset="card" :title="currentId===null ? '新建推送渠道':'编辑推送渠道'" class="alert-channel-modal" :style="{width:'min(720px, calc(100vw - 32px))',maxHeight:'calc(100dvh - 32px)'}" content-style="min-height:0; overflow-y:auto;" :mask-closable="!saving" :closable="!saving">
      <NForm label-placement="top" :disabled="saving"><div class="resource-form-grid">
        <NFormItem label="渠道名称" required><NInput v-model:value="form.name" placeholder="例如：运维值班群" :maxlength="255" /></NFormItem>
        <NFormItem label="渠道类型" required><NSelect :value="form.kind" :options="channelTypes" @update:value="changeKind" /></NFormItem>
        <template v-if="form.kind==='email'">
          <NFormItem label="SMTP 服务器" required><NInput v-model:value="form.host" placeholder="smtp.example.com" /></NFormItem><NFormItem label="SMTP 端口" required><NInputNumber v-model:value="form.port" :min="1" :max="65535" :precision="0" style="width:100%" /></NFormItem>
          <NFormItem label="发件地址" required><NInput v-model:value="form.from" placeholder="noreply@example.com" /></NFormItem><NFormItem label="收件地址" required><NInput v-model:value="form.to" placeholder="填写一个接收邮箱" /></NFormItem>
          <NFormItem label="SMTP 账号认证"><NSwitch v-model:value="form.auth" /></NFormItem><NFormItem label="STARTTLS 加密"><NSwitch v-model:value="form.tls" /></NFormItem>
          <template v-if="form.auth"><NFormItem label="SMTP 账号" required><NInput v-model:value="form.username" placeholder="SMTP 登录账号" /></NFormItem><NFormItem label="SMTP 密码" :required="!savedSecret"><NInput v-model:value="form.secret" type="password" show-password-on="click" :placeholder="savedSecret ? '已保存密码；留空保持原密码':'填写 SMTP 密码或授权码'" :input-props="{autocomplete:'new-password'}" /></NFormItem></template>
          <NAlert type="info" :bordered="false" class="form-item-wide">587 等端口使用 STARTTLS；465 端口始终使用直接 TLS。开启认证时必须加密。关闭账号认证并保存，会清除该渠道的账号和密码。</NAlert>
        </template>
        <template v-else>
          <NFormItem label="Webhook 完整地址" :required="!savedSecret" class="form-item-wide"><NInput v-model:value="form.secret" type="password" show-password-on="click" :placeholder="savedSecret ? '地址已加密保存；留空保持原地址':webhookPlaceholders[form.kind]" :input-props="{autocomplete:'new-password'}" /></NFormItem>
          <NAlert type="info" :bordered="false" class="form-item-wide">从{{ form.kind==='webhook' ? '接收服务' : channelName(form.kind)+'群机器人设置' }}复制完整 HTTPS Webhook 地址（包含令牌参数）。当前支持公网 HTTPS 目标；地址会加密保存，编辑时不会回显。</NAlert>
        </template>
        <NFormItem label="启用渠道"><NSwitch v-model:value="form.enabled" /></NFormItem>
      </div></NForm>
      <template #footer><NSpace justify="end"><NButton :disabled="saving" @click="editing=false">取消</NButton><NButton type="primary" :disabled="!form.name.trim()" :loading="saving" @click="save">保存渠道</NButton></NSpace></template>
    </NModal>
  </div>
</template>
<style scoped>.channel-notice { margin-bottom:20px; }.alert-channel-modal :deep(.n-form-item) { min-width:0; }</style>
