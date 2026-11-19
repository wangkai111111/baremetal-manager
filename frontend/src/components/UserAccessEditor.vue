<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { NAlert, NButton, NCheckbox, NForm, NFormItem, NModal, NSelect, NSpin, useMessage } from 'naive-ui'
import { api, errorText, resource, rows } from '../api'
import { useAuth } from '../stores/auth'
import { flattenDepartments } from '../utils/departments'
const props = defineProps<{show: boolean; userId?: string | number; username?: string}>()
const emit = defineEmits<{ 'update:show': [v: boolean]; saved: [] }>()
const auth = useAuth()
const message = useMessage()
const grants = ref<{department_id: number; is_admin: boolean; department_name?: string}[]>([])
const departments = ref<{label: string; value: number}[]>([])
const loading = ref(false)
const saving = ref(false)
const error = ref('')
let sequence = 0
const organization = computed(() => String(auth.tenants.find(t => String(t.id) === auth.tenantId)?.name || '当前组织'))
const scopeOptions = computed(() => {
 const options: {label:string;value:number;disabled?:boolean}[] = [
  ...(auth.user?.is_platform_admin || auth.user?.can_manage_organization ? [{label: `整个组织：${organization.value}`, value: 0}] : []),
  ...departments.value
 ]
 for (const grant of grants.value) {
  if (!options.some(option => option.value === grant.department_id)) options.push({label: grant.department_name || '授权部门（已不可用）', value: grant.department_id, disabled:true})
 }
 return options
})
watch(() => [props.show, props.userId, auth.projectId] as const, async ([show, userId, project], previous) => {
  const key = ++sequence
  if (previous && previous[2] !== project) {emit('update:show', false); return}
  if (!show || !userId || !project) return
  loading.value = true; error.value = ''; grants.value = []; departments.value = []
  try {
    const [response, treeResponse] = await Promise.all([
      api.get(`/projects/${project}/users/${userId}/access-grants`), resource.list('/departments')
    ])
    if (key !== sequence) return
    grants.value = rows(response).items.map(g => ({ department_id: g.department_id == null ? 0 : Number(g.department_id), is_admin: g.is_admin === true, department_name: String(g.department_name || '') }))
    const tree = rows(treeResponse).items
    const manageable = new Set<number>()
    const walk = (nodes: typeof tree) => nodes.forEach(d => {if (d.can_manage === true) manageable.add(Number(d.id)); if (Array.isArray(d.children)) walk(d.children as typeof tree)})
    walk(tree)
    departments.value = flattenDepartments(tree).filter(d => manageable.has(d.id)).map(d => ({label:d.name, value:d.id}))
  } catch (e) {if (key === sequence) error.value = errorText(e)}
  finally {if (key === sequence) loading.value = false}
}, { immediate: true })
async function save() {
  if (saving.value || loading.value || error.value) return
  if (new Set(grants.value.map(g => g.department_id)).size !== grants.value.length) return message.warning('请勿重复选择同一范围')
  const project = auth.projectId
  saving.value = true
  try {
    await api.put(`/projects/${project}/users/${props.userId}/access-grants`, {grants:grants.value.map(g => ({department_id:g.department_id || null,is_admin:g.is_admin}))})
    if (auth.projectId !== project) return
    message.success('访问授权已保存，后续请求立即生效'); emit('saved'); emit('update:show',false)
  } catch (e) {message.error(errorText(e))}
  finally {saving.value = false}
}
</script>
<template>
  <n-modal :show="show" preset="card" :title="`访问授权 · ${username || ''}`" style="width:min(640px,94vw)" :mask-closable="!saving" :closable="!saving" @update:show="emit('update:show',$event)">
    <n-alert type="info" :show-icon="false" style="margin-bottom:20px">当前组织：{{ organization }}。组织授权覆盖各项目；部门授权包含子部门。普通用户可对授权范围内的机器执行开关机、重启、重装系统、硬件采集、远程控制，并查看初始账号和任务日志。新增、删除、分配服务器及管理用户授权需要管理员权限。勾选管理员后，可在该范围内管理服务器和用户授权。</n-alert>
    <n-alert v-if="error" type="error">{{ error }}</n-alert>
    <n-spin :show="loading">
      <n-form :disabled="saving || loading" label-placement="top">
        <div v-for="(grant,index) in grants" :key="index" class="grant-row">
          <n-form-item :label="`授权范围 ${index + 1}`"><n-select v-model:value="grant.department_id" :options="scopeOptions" filterable /></n-form-item>
          <n-checkbox v-model:checked="grant.is_admin">范围管理员</n-checkbox>
          <n-button text type="error" @click="grants.splice(index,1)">移除</n-button>
        </div>
        <p v-if="!grants.length" class="empty-hint">尚未授权该组织的机器。保存空列表将撤销你有权管理的全部授权。</p>
        <n-button :disabled="!scopeOptions.length || !!error" dashed @click="grants.push({department_id: scopeOptions[0]!.value,is_admin:false})">+ 添加组织 / 部门授权</n-button>
      </n-form>
    </n-spin>
    <div class="modal-actions" style="margin-top:24px"><n-button :disabled="saving" @click="emit('update:show',false)">取消</n-button><n-button type="primary" :loading="saving" :disabled="loading || !!error" @click="save">保存授权</n-button></div>
  </n-modal>
</template>
<style scoped>
.grant-row {display:grid;grid-template-columns:minmax(0,1fr) auto auto;gap:16px;align-items:center}
.grant-row :deep(.n-form-item) {min-width:0}
@media(max-width:520px) {.grant-row {grid-template-columns:minmax(0,1fr) auto;gap:10px}.grant-row :deep(.n-form-item) {grid-column:1 / -1}}
</style>
