<script setup lang="ts">
import { ref, watch } from 'vue'
import { NAlert, NButton, NInput, NModal, NSpin, useMessage } from 'naive-ui'
import { api, errorText, unwrap } from '../api'
import { useAuth } from '../stores/auth'
import { copyToClipboard } from '../utils/clipboard'
const props = defineProps<{ show: boolean; assetId: number | null }>()
const emit = defineEmits<{ 'update:show': [boolean] }>()
type Access = { asset_name: string; username: string; password: string; password_generated: boolean; address: string; ssh_command: string; root_ssh_login: string; ssh_authorized_keys: string[]; note: string }
const data = ref<Access | null>(null)
const loading = ref(false)
const error = ref('')
const auth = useAuth()
const message = useMessage()
let sequence = 0
async function load() {
  const current = ++sequence
  data.value = null; error.value = ''; loading.value = false
  if (!props.show || !props.assetId) return
  loading.value = true
  try {
    const result = unwrap<Access>(await api.get(`/assets/${props.assetId}/initial-access`))
    if (current === sequence && props.show) data.value = result
  } catch (e) { if (current === sequence) error.value = errorText(e) }
  finally { if (current === sequence) loading.value = false }
}
watch(() => [props.show, props.assetId, auth.projectId], load, { immediate: true })
async function copy(value: string) {
  try { await copyToClipboard(value); message.success('已复制') }
  catch { message.error('复制失败，请手动选择内容复制') }
}
</script>
<template>
  <NModal :show="show" @update:show="emit('update:show', $event)" preset="card" title="初始账号与 SSH 连接" style="width: min(620px, 94vw); max-height: 90vh; overflow: auto">
    <NSpin :show="loading">
      <NAlert v-if="error" type="warning" :bordered="false">{{ error }}</NAlert>
      <template v-if="data">
        <p class="access-target">{{ data.asset_name }}<span v-if="data.address"> · {{ data.address }}</span></p>
        <NAlert type="info" :bordered="false">{{ data.note }}</NAlert>
        <div class="credential-row"><label>初始账号</label><code>{{ data.username }}</code><NButton size="small" @click="copy(data.username)">复制账号</NButton></div>
        <div class="credential-row"><label>初始密码<small v-if="data.password_generated">随机生成</small></label><NInput :value="data.password" type="password" readonly show-password-on="click" placeholder="旧安装未保存初始密码" :input-props="{ 'aria-label': '初始密码' }" /><NButton size="small" :disabled="!data.password" @click="copy(data.password)">复制密码</NButton></div>
        <div class="credential-row"><label>SSH 连接</label><code>{{ data.ssh_command || '暂无系统 IP 地址' }}</code><NButton size="small" :disabled="!data.ssh_command || data.root_ssh_login === 'no'" @click="copy(data.ssh_command)">复制命令</NButton></div>
        <p class="access-note">{{ data.root_ssh_login === 'yes' ? 'root 密码登录已配置。' : data.root_ssh_login === 'prohibit-password' ? '仅允许 root 公钥登录；请使用对应私钥。' : '该安装配置禁止 root SSH 登录。' }}{{ data.ssh_authorized_keys?.length ? `同时配置了 ${data.ssh_authorized_keys.length} 条 SSH 公钥，使用对应私钥连接。` : '未配置 SSH 公钥。' }}</p>
        <div v-if="data.ssh_authorized_keys?.length" class="key-block"><NInput :value="data.ssh_authorized_keys.join('\n')" type="textarea" readonly :autosize="{ minRows: 2, maxRows: 4 }" :input-props="{ 'aria-label': '已安装 SSH 公钥' }" /><NButton size="small" @click="copy(data.ssh_authorized_keys.join('\n'))">复制公钥</NButton></div>
      </template>
      <p v-if="loading">正在读取初始登录信息…</p>
    </NSpin>
  </NModal>
</template>
<style scoped>
.access-target { margin-top: 0; overflow-wrap: anywhere; font-weight: 600; }
.credential-row { display: grid; grid-template-columns: 76px minmax(0, 1fr) auto; align-items: center; gap: 12px; margin: 20px 0; }
.credential-row label { color: var(--color-text-secondary); font-size: 13px; }
.credential-row small { display: block; font-size: 11px; margin-top: 4px; }
.credential-row code { overflow-wrap: anywhere; }
.access-note { font-size: 12px; color: var(--color-text-secondary); line-height: 1.8; }
.key-block { display: flex; flex-direction: column; align-items: flex-end; gap: 10px; }
@media (max-width: 480px) { .credential-row { grid-template-columns: minmax(0, 1fr) auto; gap: 8px; } .credential-row label { grid-column: 1 / -1; } }
</style>
