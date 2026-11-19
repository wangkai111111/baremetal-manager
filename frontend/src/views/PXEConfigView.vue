<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { NAlert, NButton, NCard, NForm, NFormItem, NSelect, NSpace, NSwitch, NTag, useDialog, useMessage } from 'naive-ui'
import { api, errorText, rows, unwrap, type RecordData } from '../api'
import { hostIP, networkLabel } from '../utils/networkOptions'
import { useAuth } from '../stores/auth'
import { hostAddressOptions, hostInterfaceOptions, includeSavedOption, retainOrChooseAddress, type HostOptions } from '../utils/pxeOptions'
const auth = useAuth()
const message = useMessage()
const dialog = useDialog()
const busy = ref(false)
const known = ref(false)
const failure = ref('')
const optionsError = ref('')
const dhcp = ref(false)
const tftp = ref(false)
const binding = ref(true)
const snapshot = ref('')
const config = reactive<Record<string, string>>({ tenant_id: '', project_id: '', network_id: '', pool_id: '', interface: '', subnet: '', start: '', end: '', server: '', root: '/var/lib/tftpboot', bios: 'undionly.kpxe', efi: 'ipxe.efi' })
const tenants = ref<RecordData[]>([])
const projects = ref<RecordData[]>([])
const networks = ref<RecordData[]>([])
const pools = ref<RecordData[]>([])
const optionBusy = ref(false)
const hostBusy = ref(false)
const hostError = ref('')
const hostOptions = ref<HostOptions>({ interfaces: [], roots: [] })
const hostKnown = ref(false)
let optionSequence = 0
const locked = computed(() => busy.value || !known.value || dhcp.value || tftp.value)
const dirty = computed(() => snapshot.value !== JSON.stringify(config) || binding.value !== !!Number(config.network_id))
const boundNetwork = computed(() => networks.value.find(row => String(row.id) === config.network_id))
const boundPool = computed(() => pools.value.find(row => String(row.id) === config.pool_id))
const selectedInterface = computed(() => hostOptions.value.interfaces.find(row => row.name === config.interface))
const interfaceChoices = computed(() => includeSavedOption(hostInterfaceOptions(hostOptions.value.interfaces), config.interface!))
const addressChoices = computed(() => hostAddressOptions(selectedInterface.value,
  binding.value ? String(boundNetwork.value?.subnet || '') : '',
  binding.value ? hostIP(boundPool.value?.start_address) : '', binding.value ? hostIP(boundPool.value?.end_address) : ''))
const selectedRoot = computed(() => hostOptions.value.roots.find(row => row.path === config.root))
const rootChoices = computed(() => includeSavedOption(hostOptions.value.roots.map(row => ({value: row.path, label: `${row.path}${row.path === '/var/lib/tftpboot' ? ' · 平台默认目录' : ''}`, disabled: !row.bios_files.length || !row.efi_files.length || !row.chain_ready})), config.root!))
const biosChoices = computed(() => includeSavedOption((selectedRoot.value?.bios_files || []).map(row => ({value: row.name, label: row.label})), config.bios!))
const efiChoices = computed(() => includeSavedOption((selectedRoot.value?.efi_files || []).map(row => ({value: row.name, label: row.label})), config.efi!))
const selectionError = computed(() => {
  if (!hostKnown.value) return '请先成功读取宿主机选项。'
  if (!selectedInterface.value?.up || !selectedInterface.value.addresses.length) return '请选择已启用且配置了 IPv4 的宿主机网卡。'
  if (!addressChoices.value.some(row => row.value === config.server && !row.disabled)) return '请选择该网卡上与子网、地址池匹配的 IPv4 地址。'
  if (!selectedRoot.value) return '请选择系统已检测到的 TFTP 目录。'
  if (!selectedRoot.value.chain_ready) return '所选目录缺少 default.ipxe；新部署请配置 PXE_HTTP_BASE 后重启 PXE 控制服务。'
  if (!selectedRoot.value.bios_files.some(row => row.name === config.bios) || !selectedRoot.value.efi_files.some(row => row.name === config.efi)) return '所选目录中没有对应的 BIOS / UEFI 引导文件。'
  return ''
})
function chooseAddress() { config.server = retainOrChooseAddress(addressChoices.value, config.server!) }
function selectInterface(value: string) { config.interface = value; chooseAddress() }
function selectRoot(value: string) {
  config.root = value
  const root = selectedRoot.value
  if (!root?.bios_files.some(row => row.name === config.bios)) config.bios = root?.bios_files[0]?.name || ''
  if (!root?.efi_files.some(row => row.name === config.efi)) config.efi = root?.efi_files[0]?.name || ''
}
async function loadHostOptions() {
  if (hostBusy.value || !auth.user?.is_platform_admin) return
  hostBusy.value = true; hostError.value = ''
  try {
    hostOptions.value = unwrap<HostOptions>(await api.get('/pxe-services/options'))
    hostKnown.value = true
  } catch (e) { hostKnown.value = false; hostError.value = errorText(e) }
  finally { hostBusy.value = false }
}
const selectOptions = (values: RecordData[]) => values.map(row => ({label: String(row.name || '名称未填写'), value: String(row.id)}))
function withCurrent(values: {label: string; value: string; disabled?: boolean}[], value: string, label: string) {
  return value && !values.some(row => row.value === value) ? [...values, {label: `${label}（待加载或已不可用）`, value}] : values
}
async function all(path: string, params: Record<string, unknown> = {}) {
  const result: RecordData[] = []
  for (let page = 1; ; page++) {
    const data = rows(await api.get(path, {params: {...params, page, page_size: 200}}))
    result.push(...data.items)
    if (!data.items.length || result.length >= data.total) return result
  }
}
async function loadOptions() {
  const sequence = ++optionSequence
  optionBusy.value = true; optionsError.value = ''
  projects.value = []; networks.value = []; pools.value = []
  const tenant = config.tenant_id, project = config.project_id
  try {
    const [organizations, projectRows] = await Promise.all([all('/tenants'), tenant ? all('/projects', {tenant_id: tenant}) : Promise.resolve([])])
    if (sequence !== optionSequence) return
    tenants.value = organizations; projects.value = projectRows
    if (project && projectRows.some(row => String(row.id) === project)) {
      const [networkRows, poolRows] = await Promise.all([all(`/projects/${project}/networks`), all(`/projects/${project}/ip-pools`)])
      if (sequence !== optionSequence) return
      networks.value = networkRows; pools.value = poolRows
    }
  } catch (e) { if (sequence === optionSequence) optionsError.value = errorText(e) }
  finally { if (sequence === optionSequence) optionBusy.value = false }
}
function selectTenant(value: string | null) {
  config.tenant_id = value || ''; config.project_id = ''; config.network_id = ''; config.pool_id = ''; loadOptions()
}
function selectProject(value: string | null) {
  config.project_id = value || ''; config.network_id = ''; config.pool_id = ''; loadOptions()
}
function selectNetwork(value: string | null) { config.network_id = value || ''; config.pool_id = ''; chooseAddress() }
function selectPool(value: string) { config.pool_id = value; chooseAddress() }
async function load() {
  if (!auth.user?.is_platform_admin) return
  busy.value = true
  try {
    const state = unwrap<any>(await api.get('/pxe-services'))
    known.value = true; dhcp.value = state.dhcp_running; tftp.value = state.tftp_running
    Object.keys(config).forEach(key => { config[key] = String(state.config[key] || '') })
    binding.value = !!Number(config.network_id)
    snapshot.value = JSON.stringify(config)
    failure.value = state.error || ''
    await loadOptions()
  } catch (e) { known.value = false; failure.value = errorText(e) }
  finally { busy.value = false }
}
async function save() {
  if (locked.value) return
  if (hostBusy.value || selectionError.value) { message.warning(selectionError.value || '正在读取宿主机选项'); return }
  if (binding.value && (!boundNetwork.value || !boundPool.value || !boundNetwork.value.dhcp_enabled || Number(boundPool.value.network_id) !== Number(config.network_id))) {
    message.warning('请选择完整的组织、项目、允许 DHCP 的子网和对应地址池'); return
  }
  busy.value = true
  try {
    const body: Record<string, string | number> = { ...config }
    for (const key of ['tenant_id', 'project_id', 'network_id', 'pool_id']) body[key] = binding.value ? Number(config[key]) : 0
    if (!binding.value) { body.subnet = ''; body.start = ''; body.end = '' }
    await api.put('/pxe-services', body)
    message.success('配置已保存，未启动任何服务'); await load()
  } catch (e) { message.error(errorText(e)) }
  finally { busy.value = false }
}
function control(service: string, action: string) {
  if (busy.value || !known.value || dirty.value || (service === 'dhcp' && action === 'start' && !binding.value)) return
  dialog.warning({ title: `${action === 'start' ? '启动' : '停止'} ${service.toUpperCase()}`, content: action === 'start' ? '将在指定宿主机网卡上实际启动服务，请确认装机网络与现网 DHCP 无冲突。' : '存在活动装机任务时，平台将拒绝停止。', positiveText: '确认', negativeText: '取消', onPositiveClick: async () => {
    busy.value = true
    try { await api.post(`/pxe-services/${service}/${action}`); await load() }
    catch (e) { message.error(errorText(e)); await load() }
    finally { busy.value = false }
  } })
}
onMounted(() => { void load(); void loadHostOptions() })
</script>
<template>
  <NCard title="平台 DHCP / TFTP 服务" class="pxe-service-card">
    <NAlert type="info" :bordered="false" class="pxe-notice">配置顺序：子网 → 地址池 → 绑定装机网卡 → 保存 → 启动服务。全局配置不随当前项目切换；控制器重启后需手动启动服务。</NAlert>
    <template v-if="auth.user?.is_platform_admin">
      <NAlert v-if="failure" type="error" class="pxe-notice">{{ failure }}</NAlert>
      <NSpace align="center" class="pxe-notice">
        <NTag :type="known && dhcp ? 'success' : 'default'">DHCP：{{ !known ? '未知' : dhcp ? '运行中' : '已停止' }}</NTag>
        <NButton :disabled="busy || !known || dirty || (!dhcp && !binding)" @click="control('dhcp', dhcp ? 'stop' : 'start')">{{ dhcp ? '停止 DHCP' : '启动 DHCP' }}</NButton>
        <NTag :type="known && tftp ? 'success' : 'default'">TFTP：{{ !known ? '未知' : tftp ? '运行中' : '已停止' }}</NTag>
        <NButton :disabled="busy || !known || dirty" @click="control('tftp', tftp ? 'stop' : 'start')">{{ tftp ? '停止 TFTP' : '启动 TFTP' }}</NButton>
        <NButton :loading="busy" @click="load">{{ dirty ? '放弃修改并刷新' : '刷新实际状态' }}</NButton>
        <NButton :loading="hostBusy" @click="loadHostOptions">刷新宿主机选项</NButton>
      </NSpace>
      <NAlert v-if="known && (dhcp || tftp)" type="warning" :bordered="false" class="pxe-notice">服务运行中，配置已锁定。调整配置前需停止 DHCP 和 TFTP；活动装机期间不允许停止。</NAlert>
      <NAlert v-if="optionsError" type="error" class="pxe-notice">选项加载失败：{{ optionsError }} <NButton text @click="loadOptions">重试加载</NButton></NAlert>
      <NAlert v-if="hostError" type="error" class="pxe-notice">宿主机信息读取失败：{{ hostError }}。请检查 PXE 控制服务，然后刷新宿主机选项。</NAlert>
      <NAlert v-else-if="hostKnown && selectionError" type="warning" class="pxe-notice">{{ selectionError }}</NAlert>
      <NForm label-placement="top" :disabled="locked">
        <NFormItem label="绑定 DHCP 装机网络">
          <div class="pxe-control"><NSwitch v-model:value="binding" /><p class="field-help">关闭后只保存 TFTP 配置；开启后按组织、项目、子网选择地址池。</p></div>
        </NFormItem>
        <div v-if="binding" class="pxe-form">
          <NFormItem label="绑定组织" required><NSelect :value="config.tenant_id || null" :options="withCurrent(selectOptions(tenants), config.tenant_id!, '组织')" :loading="optionBusy" filterable placeholder="请选择组织" @update:value="selectTenant" /></NFormItem>
          <NFormItem label="绑定项目" required><NSelect :value="config.project_id || null" :options="withCurrent(selectOptions(projects), config.project_id!, '项目')" :loading="optionBusy" :disabled="!config.tenant_id || locked" filterable placeholder="请选择项目" @update:value="selectProject" /></NFormItem>
          <NFormItem label="装机子网" required><NSelect :value="config.network_id || null" :options="withCurrent(networks.map(row => ({label: networkLabel(row) + (row.dhcp_enabled ? '' : '（未允许 DHCP）'),value:String(row.id),disabled:!row.dhcp_enabled})), config.network_id!, '子网')" :disabled="!config.project_id || locked" :loading="optionBusy" filterable placeholder="请选择允许 DHCP 的子网" @update:value="selectNetwork" /></NFormItem>
          <NFormItem label="地址池" required><NSelect :value="config.pool_id" :options="withCurrent(pools.filter(row => String(row.network_id) === config.network_id).map(row => ({label:`${row.name} · ${hostIP(row.start_address)} — ${hostIP(row.end_address)}`,value:String(row.id)})), config.pool_id!, '地址池')" :disabled="!config.network_id || locked" :loading="optionBusy" filterable placeholder="请选择对应地址池" @update:value="selectPool" /></NFormItem>
        </div>
        <div v-if="binding" class="pxe-summary">关联网段：{{ boundNetwork?.subnet || config.subnet || '未选择' }} · 地址范围：{{ hostIP(boundPool?.start_address) || config.start || '—' }} — {{ hostIP(boundPool?.end_address) || config.end || '—' }}</div>
        <div class="pxe-form">
          <NFormItem label="宿主机装机网卡" required><div class="pxe-control">
            <NSelect :value="config.interface || null" :options="interfaceChoices" :loading="hostBusy" :disabled="locked || !hostKnown" filterable placeholder="选择宿主机网卡" @update:value="selectInterface" />
            <p class="field-help">从运行 PXE 服务的宿主机读取；未启用或没有 IPv4 的网卡不可选。{{ selectedInterface?.mac ? `MAC：${selectedInterface.mac}` : '' }}</p>
          </div></NFormItem>
          <NFormItem label="装机网卡 IPv4" required><div class="pxe-control">
            <NSelect v-model:value="config.server" :options="includeSavedOption(addressChoices, config.server!)" :loading="hostBusy" :disabled="locked || !hostKnown || !selectedInterface" filterable placeholder="选择该网卡的 IPv4 地址" />
            <p class="field-help">与网卡联动；仅一个可用地址时自动选择。开启 DHCP 时须匹配所选子网且位于地址池之外。</p>
          </div></NFormItem>
          <NFormItem label="TFTP 根目录" required><div class="pxe-control">
            <NSelect :value="config.root" :options="rootChoices" :loading="hostBusy" :disabled="locked || !hostKnown" filterable placeholder="选择已检测到的 TFTP 目录" @update:value="selectRoot" />
            <p class="field-help">平台默认目录已内置引导文件；也会列出含引导文件的已有子目录。文件不完整的目录不可选。</p>
          </div></NFormItem>
          <NFormItem label="BIOS 引导文件" required><div class="pxe-control">
            <NSelect v-model:value="config.bios" :options="biosChoices" :loading="hostBusy" :disabled="locked || !hostKnown || !selectedRoot" filterable placeholder="选择 BIOS 引导文件" />
            <p class="field-help">相对于所选根目录；默认使用 undionly.kpxe，仅显示实际存在的文件。</p>
          </div></NFormItem>
          <NFormItem label="UEFI 引导文件" required><div class="pxe-control">
            <NSelect v-model:value="config.efi" :options="efiChoices" :loading="hostBusy" :disabled="locked || !hostKnown || !selectedRoot" filterable placeholder="选择 UEFI 引导文件" />
            <p class="field-help">适用于 x86_64 UEFI，默认使用 ipxe.efi；不适用于 ARM 或要求签名的 Secure Boot。</p>
          </div></NFormItem>
        </div>
      </NForm>
      <NSpace align="center"><NButton type="primary" :disabled="locked || !dirty || optionBusy || hostBusy || !!selectionError" :loading="busy" @click="save">保存全局配置</NButton><span class="field-help">{{ dirty ? '修改尚未保存；保存后再启动服务。' : '保存配置不会自动启动服务。' }}</span></NSpace>
    </template>
    <NAlert v-else type="info">全局服务仅平台管理员可查看和控制。</NAlert>
  </NCard>
</template>
<style scoped>
.pxe-service-card { margin-bottom: 20px; }
.pxe-notice { margin-bottom: 16px; }
.pxe-form { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); column-gap: 20px; }
.pxe-form :deep(.n-form-item) { min-width: 0; }
.pxe-control { width: 100%; min-width: 0; }
.field-help { margin: 6px 0 0; color: var(--color-text-secondary); font-size: 12px; line-height: 1.6; text-wrap: pretty; }
.pxe-summary { margin: 0 0 20px; padding: 12px 16px; border: 1px solid var(--color-border); border-radius: var(--radius-sm); color: var(--color-text-secondary); overflow-wrap: anywhere; }
@media (max-width: 700px) { .pxe-form { grid-template-columns: minmax(0, 1fr); } }
</style>
