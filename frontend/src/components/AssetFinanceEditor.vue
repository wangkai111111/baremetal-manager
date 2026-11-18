<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { NAlert, NButton, NDatePicker, NForm, NFormItem, NInput, NInputNumber, NModal, NSelect, NSpin, useMessage } from 'naive-ui'
import { api, errorText, resource, rows, unwrap, type RecordData } from '../api'
import { useAuth } from '../stores/auth'
const props = defineProps<{ show: boolean; assetId?: string | number; kind: 'depreciation' | 'lifecycle' }>()
const emit = defineEmits<{ 'update:show': [value: boolean]; saved: [] }>()
const auth = useAuth()
const message = useMessage()
const selected = ref<string | null>(null)
const options = ref<{label: string; value: string}[]>([])
const loading = ref(false)
const saving = ref(false)
const error = ref('')
let sequence = 0
let searchSequence = 0
const cost = ref<number | null>(null)
const salvage = ref<number | null>(0)
const months = ref<number | null>(36)
const start = ref<string | null>(null)
const depStatus = ref('active')
const purchased = ref<string | null>(null)
const warranty = ref<string | null>(null)
const vendor = ref('')
const monthly = computed(() => cost.value != null && salvage.value != null && months.value ? Math.max(cost.value - salvage.value, 0) / months.value : null)
const title = computed(() => props.kind === 'depreciation' ? '配置资产折旧' : '编辑采购与维保')
async function searchAssets(search = '') {
  const key = ++searchSequence
  try {
    const data = rows(await resource.list('/assets', { search, manageable: true, page_size: 200 }))
    if (key !== searchSequence || !props.show) return
    options.value = data.items.map(a => ({ label: `${a.name} · #${a.id}`, value: String(a.id) }))
  } catch (e) { if (key === searchSequence) error.value = errorText(e) }
}
watch(() => [props.show, props.assetId, auth.projectId] as const, ([show, id], previous) => {
  if (previous && previous[2] !== auth.projectId) { emit('update:show', false); return }
  if (!show) { sequence++; searchSequence++; return }
  loading.value = false
  selected.value = id == null ? null : String(id)
  error.value = ''
  options.value = []
  void searchAssets()
  if (selected.value) void load()
}, { immediate: true })
async function load() {
  const key = ++sequence
  if (!selected.value) return
  loading.value = true
  error.value = ''
  cost.value = null; salvage.value = 0; months.value = 36; start.value = null; depStatus.value = 'active'
  purchased.value = null; warranty.value = null; vendor.value = ''
  try {
    const [assetResponse, depResponse] = await Promise.all([
      resource.get('/assets', selected.value),
      props.kind === 'depreciation' ? api.get(`/assets/${selected.value}/depreciation`) : Promise.resolve(null)
    ])
    if (key !== sequence || !props.show) return
    const asset = unwrap<RecordData>(assetResponse)
    if (asset.can_manage !== true) throw new Error('当前账号无权编辑此服务器')
    const date = (v: unknown) => v ? String(v).slice(0, 10) : null
    purchased.value = date(asset.purchase_date); warranty.value = date(asset.warranty_expiry); vendor.value = String(asset.vendor || '')
    if (!options.value.some(o => o.value === selected.value)) options.value.push({ label: `${asset.name} · #${asset.id}`, value: selected.value })
    const dep = depResponse ? unwrap<RecordData>(depResponse) : null
    if (dep?.has_depreciation) {
      cost.value = Number(dep.purchase_cost); salvage.value = Number(dep.salvage_value); months.value = Number(dep.useful_life_months)
      start.value = date(dep.start_date); depStatus.value = ['scrapped', 'retired'].includes(String(dep.status)) ? 'scrapped' : 'active'
    } else start.value = purchased.value
  } catch (e) { if (key === sequence) error.value = errorText(e) }
  finally { if (key === sequence) loading.value = false }
}
async function save() {
  if (!selected.value || error.value || loading.value || saving.value) return
  if (props.kind === 'depreciation' && (cost.value == null || salvage.value == null || !months.value || !start.value)) return message.warning('请填写完整的折旧参数')
  if (props.kind === 'depreciation' && salvage.value! > cost.value!) return message.warning('残值不能大于资产原值')
  if (props.kind === 'lifecycle' && purchased.value && warranty.value && warranty.value < purchased.value) return message.warning('维保到期日期不能早于采购日期')
  const project = auth.projectId
  saving.value = true
  try {
    await api.put(`/assets/${selected.value}/${props.kind === 'depreciation' ? 'depreciation' : 'lifecycle'}`, props.kind === 'depreciation' ? {
      purchase_cost: cost.value, salvage_value: salvage.value, useful_life_months: months.value,
      start_date: start.value, depreciation_method: 'straight_line', status: depStatus.value
    } : { purchase_date: purchased.value, warranty_expiry: warranty.value, vendor: vendor.value })
    if (project !== auth.projectId) return
    message.success('已保存'); emit('saved'); emit('update:show', false)
  } catch (e) { message.error(errorText(e)) }
  finally { saving.value = false }
}
</script>
<template>
  <n-modal :show="show" preset="card" :title="title" style="width: min(520px, 94vw)" :mask-closable="!saving" :closable="!saving" @update:show="emit('update:show', $event)">
    <n-form label-placement="top" :disabled="saving || loading">
      <n-form-item label="服务器" required>
        <n-select v-model:value="selected" :options="options" filterable remote :disabled="assetId != null || saving || loading" placeholder="搜索并选择可管理的服务器" @search="searchAssets" @update:value="load" />
      </n-form-item>
      <n-alert v-if="error" type="error" style="margin-bottom: 16px">{{ error }}</n-alert>
      <n-spin :show="loading">
        <template v-if="kind === 'depreciation'">
          <n-form-item label="资产原值（元）" required><n-input-number v-model:value="cost" :min="0" :max="9999999999.99" :precision="2" style="width:100%" /></n-form-item>
          <n-form-item label="预计残值（元）" required><n-input-number v-model:value="salvage" :min="0" :max="cost ?? 9999999999.99" :precision="2" style="width:100%" /></n-form-item>
          <n-form-item label="折旧年限（月）" required><n-input-number v-model:value="months" :min="1" :max="1200" :precision="0" style="width:100%" /></n-form-item>
          <n-form-item label="折旧起始日期" required><n-date-picker v-model:formatted-value="start" value-format="yyyy-MM-dd" type="date" clearable style="width:100%" /></n-form-item>
          <n-form-item label="状态"><n-select v-model:value="depStatus" :options="[{label:'正常折旧（是否折完由系统计算）',value:'active'},{label:'已报废',value:'scrapped'}]" /></n-form-item>
          <n-alert type="info" :show-icon="false">直线法 · 月折旧额 {{ monthly == null ? '—' : `¥ ${monthly.toFixed(2)}` }}。累计折旧和净值由系统计算，净值不低于残值。</n-alert>
        </template>
        <template v-else>
          <n-form-item label="采购日期"><n-date-picker v-model:formatted-value="purchased" value-format="yyyy-MM-dd" type="date" clearable style="width:100%" /></n-form-item>
          <n-form-item label="维保到期日期"><n-date-picker v-model:formatted-value="warranty" value-format="yyyy-MM-dd" type="date" clearable style="width:100%" /></n-form-item>
          <n-form-item label="供应商"><n-input v-model:value="vendor" :maxlength="255" clearable /></n-form-item>
          <n-alert type="info" :show-icon="false">维保预警根据到期日期自动计算，已过期资产也会保留在告警列表中。清空到期日期可移除该资产的维保预警。</n-alert>
        </template>
      </n-spin>
      <div class="modal-actions" style="margin-top:20px"><n-button :disabled="saving" @click="emit('update:show', false)">取消</n-button><n-button type="primary" :loading="saving" :disabled="!selected || loading || !!error" @click="save">保存</n-button></div>
    </n-form>
  </n-modal>
</template>
