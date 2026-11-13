<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { NAlert, NButton, NCard, NForm, NFormItem, NInput, NSpace, NTag, useDialog, useMessage } from 'naive-ui'
import ResourceView from './ResourceView.vue'
import type { Section } from '../catalog'
import { api, errorText, unwrap } from '../api'
import { useAuth } from '../stores/auth'
const auth = useAuth()
const message = useMessage()
const dialog = useDialog()
const busy = ref(false)
const known = ref(false)
const failure = ref('')
const dhcp = ref(false)
const tftp = ref(false)
const config = reactive<Record<string, string>>({ tenant_id: '', project_id: '', network_id: '', pool_id: '', interface: '', subnet: '', start: '', end: '', server: '', root: '/var/lib/tftpboot', bios: 'undionly.kpxe', efi: 'ipxe.efi' })
const fields = [ ['tenant_id', '绑定租户 ID'], ['project_id', '绑定项目 ID'], ['network_id', '装机网络 ID'], ['pool_id', '地址池 ID'], ['interface', '宿主机装机网卡'], ['server', '网卡 IPv4 地址'], ['root', 'TFTP 根目录'], ['bios', 'BIOS 引导文件'], ['efi', 'UEFI 引导文件'] ]
async function load() {
  if (!auth.user?.is_platform_admin) return
  busy.value = true
  try {
    const state = unwrap<any>(await api.get('/pxe-services'))
    known.value = true; dhcp.value = state.dhcp_running; tftp.value = state.tftp_running
    Object.entries(state.config).forEach(([key, value]) => { config[key] = String(value || '') })
    failure.value = state.error || ''
  } catch (e) { known.value = false; failure.value = errorText(e) }
  finally { busy.value = false }
}
async function save() {
  busy.value = true
  try {
    const body: Record<string, string | number> = { ...config }
    for (const key of ['tenant_id', 'project_id', 'network_id', 'pool_id']) body[key] = Number(config[key])
    await api.put('/pxe-services', body)
    message.success('配置已保存，未启动任何服务'); await load()
  } catch (e) { message.error(errorText(e)) }
  finally { busy.value = false }
}
function control(service: string, action: string) {
  dialog.warning({ title: `${action === 'start' ? '启动' : '停止'} ${service.toUpperCase()}`, content: action === 'start' ? '此操作会在宿主机指定装机网卡上实际启动网络服务。请确认隔离网段、地址池和现网 DHCP 无冲突。' : '存在活动装机任务时，平台将拒绝停止。', positiveText: '确认', negativeText: '取消', onPositiveClick: async () => {
    busy.value = true
    try { await api.post(`/pxe-services/${service}/${action}`); await load() }
    catch (e) { message.error(errorText(e)); await load() }
    finally { busy.value = false }
  } })
}
onMounted(load)
const section: Section = {
  path: '/pxe-config', title: 'PXE 配置', subtitle: '项目引导记录与全局服务独立；保存记录不会启动 DHCP/TFTP。真实装机执行尚未实现。', api: '/pxe-configs', permission: 'network:read',
  fields: [{ key: 'name', label: '配置名称', required: true }, { key: 'network_id', label: '网络 ID', type: 'number', required: true }, { key: 'boot_mode', label: '启动模式', type: 'select', options: ['uefi', 'bios'], required: true }, { key: 'next_server', label: '引导服务器', required: true }, { key: 'boot_file', label: '引导文件', required: true }],
  columns: ['name', 'network_id', 'boot_mode', 'next_server', 'boot_file', 'created_at']
}
</script>
<template>
  <NCard title="平台 DHCP / TFTP 服务" style="margin-bottom: 20px">
    <NAlert type="warning" :bordered="false" style="margin-bottom: 16px">默认关闭，重启控制器后不会自动恢复。DHCP 仅绑定明确装机网卡和数据库地址池；网络 DHCP 标记不等于实际运行。只允许平台管理员管理全局配置，不随项目切换覆盖。</NAlert>
    <template v-if="auth.user?.is_platform_admin">
      <NAlert v-if="failure" type="error" style="margin-bottom: 16px">{{ failure }}</NAlert>
      <NSpace align="center" style="margin-bottom: 16px">
        <NTag :type="known && dhcp ? 'success' : 'default'">DHCP：{{ !known ? '未知' : dhcp ? '运行中' : '已停止' }}</NTag>
        <NButton :disabled="busy || !known" @click="control('dhcp', dhcp ? 'stop' : 'start')">{{ dhcp ? '停止 DHCP' : '启动 DHCP' }}</NButton>
        <NTag :type="known && tftp ? 'success' : 'default'">TFTP：{{ !known ? '未知' : tftp ? '运行中' : '已停止' }}</NTag>
        <NButton :disabled="busy || !known" @click="control('tftp', tftp ? 'stop' : 'start')">{{ tftp ? '停止 TFTP' : '启动 TFTP' }}</NButton>
        <NButton :loading="busy" @click="load">刷新实际状态</NButton>
      </NSpace>
      <NForm label-placement="top" :disabled="busy || !known || dhcp || tftp" class="pxe-form">
        <NFormItem v-for="field in fields" :key="field[0]" :label="field[1]"><NInput v-model:value="config[field[0]!]" /></NFormItem>
      </NForm>
      <p>关联网段：{{ config.subnet || '未配置' }} · 地址池：{{ config.start || '未配置' }} — {{ config.end || '未配置' }}（保存时由网络与地址池记录解析，禁止跨项目引用；仅使用 TFTP 可留空全部绑定 ID）</p>
      <NButton type="primary" :disabled="busy || !known || dhcp || tftp" @click="save">保存全局配置（不启动服务）</NButton>
    </template>
    <NAlert v-else type="info">全局服务仅平台管理员可查看和控制；项目配置不能覆盖全局网络服务。</NAlert>
  </NCard>
  <ResourceView :section="section" />
</template>
<style scoped>
.pxe-form { display: grid; grid-template-columns: repeat(auto-fit, minmax(220px, 1fr)); column-gap: 20px; }
</style>
