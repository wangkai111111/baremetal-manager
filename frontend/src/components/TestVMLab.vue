<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue'
import { NAlert, NButton, NCheckbox, NFormItem, NModal, NPopconfirm, NSelect, NTag, useMessage } from 'naive-ui'
import { api, errorText, unwrap } from '../api'
import { useRouter } from 'vue-router'
import { useAuth } from '../stores/auth'
import InstallationProgress, { type InstallationProgressData } from './InstallationProgress.vue'
import InstallAccessSettings from './InstallAccessSettings.vue'
import { installAccessError, installAccessPayload, newInstallAccess } from '../utils/installAccess'

const props = defineProps<{ show: boolean }>()
const emit = defineEmits<{ 'update:show': [boolean]; updated: [] }>()
type LabState = {
  tenant_id: number; firmware: string; running: boolean; serial_log?: string; error?: string
  asset_id: number; plan_id: number; network_known: boolean; network_ready: boolean
  installation: InstallationProgressData
}
const state = ref<LabState | null>(null)
const firmware = ref('uefi')
const busy = ref('')
const failure = ref('')
const confirmed = ref(false)
const access = ref(newInstallAccess())
const consoleElement = ref<HTMLElement>()
const message = useMessage()
const router = useRouter()
const auth = useAuth()
function viewTask() {if (!state.value?.installation.operation_id) return;emit('update:show',false);router.push({path:'/tasks',query:{task:String(state.value.installation.operation_id)}})}
let timer: ReturnType<typeof setTimeout> | undefined
let sequence = 0
let disposed = false
const active = computed(() => !!state.value?.installation?.id && (state.value.installation.cleanup_pending || ['pending', 'preparing', 'booting', 'installing', 'post_install', 'unknown'].includes(state.value.installation.status)))
const installed = computed(() => state.value?.installation?.status === 'succeeded')
const stages: Record<string, string> = { pending: '等待任务执行', preparing: '准备安装配置', booting: 'PXE 引导 / 下载系统镜像', installing: '安装操作系统', post_install: '完成配置 / 等待首次启动', succeeded: '系统安装成功', failed: '安装失败', cancelled: '已取消', unknown: '结果待核实' }
const statusLabel = computed(() => stages[state.value?.installation?.status || ''] || '尚未开始安装')
const steps = computed(() => [
  { title: '创建虚拟机', detail: '2 vCPU · 8 GB 内存 · 40 GB 磁盘', done: !!state.value?.asset_id },
  { title: '准备 PXE 网络', detail: '隔离网段 192.168.77.0/24 · DHCP + TFTP', done: !!state.value?.network_ready },
  { title: '网络安装 Ubuntu', detail: state.value?.installation?.id ? statusLabel.value : 'Ubuntu Server 22.04.5 · 自动分区安装', done: installed.value }
])

async function refresh(initial = false) {
  const current = ++sequence
  clearTimeout(timer)
  try {
    const result = unwrap<LabState>(await api.get('/assets/test-vm'))
    if (disposed || !props.show || current !== sequence) return
    const el = consoleElement.value
    const atBottom = !el || el.scrollHeight - el.scrollTop - el.clientHeight < 60
    state.value = result
    if (initial && result.firmware) firmware.value = result.firmware
    await nextTick()
    if (atBottom && consoleElement.value) consoleElement.value.scrollTop = consoleElement.value.scrollHeight
  } catch (e) {
    if (current === sequence && props.show && !disposed) { state.value = null; failure.value = errorText(e) }
  } finally {
    if (!disposed && props.show && current === sequence) timer = setTimeout(() => refresh(), 5000)
  }
}
async function perform(name: string, action: () => Promise<void>) {
  if (busy.value) return
  busy.value = name; failure.value = ''
  try { await action(); emit('updated') }
  catch (e) { failure.value = errorText(e) }
  finally { busy.value = ''; await refresh() }
}
function create() {
  perform('create', async () => {
    await api.post('/assets/test-vm', { firmware: firmware.value }, { timeout: 120000 })
    message.success('测试虚拟机与 Ubuntu 安装方案已准备好')
  })
}
function prepareNetwork() {
  perform('network', async () => {
    await api.post('/assets/test-vm/prepare-network')
    message.success('隔离网络 DHCP / TFTP 已启动')
  })
}
function power(action: string) {
  perform(action, async () => { await api.post(`/assets/test-vm/${action}`) })
}
function install() {
  if (!confirmed.value || !state.value?.asset_id || !state.value.plan_id) return
  const accessError = installAccessError(access.value)
  if (accessError) { message.error(accessError); return }
  perform('install', async () => {
    const vm = state.value!
    await api.post(`/assets/${vm.asset_id}/install`, { install_plan_id: vm.plan_id, confirmed: true, access: installAccessPayload(access.value) })
    confirmed.value = false
    access.value = newInstallAccess()
    message.success('已提交 PXE 安装，进度由虚拟机实际回报')
  })
}
watch(() => props.show, show => {
  clearTimeout(timer); sequence++
  access.value = newInstallAccess()
  if (show) { state.value = null; confirmed.value = false; failure.value = ''; refresh(true) }
}, { immediate: true })
onBeforeUnmount(() => { disposed = true; sequence++; clearTimeout(timer) })
</script>

<template>
  <NModal :show="show" @update:show="emit('update:show', $event)" preset="card" title="PXE 测试实验室" :mask-closable="!busy" :close-on-esc="!busy" style="width: min(880px, 96vw); max-height: 92vh; overflow: auto">
    <p class="lab-intro">用一台虚拟服务器，验证从网络引导到系统安装的完整流程。</p>
    <div class="lab-steps">
      <div v-for="(step, i) in steps" :key="step.title" class="lab-step" :class="{ done: step.done }">
        <span class="step-number">{{ step.done ? '✓' : `0${i + 1}` }}</span>
        <div><strong>{{ step.title }}</strong><small>{{ step.detail }}</small></div>
      </div>
    </div>
    <NAlert v-if="failure || state?.error" type="error" :bordered="false" class="lab-alert">{{ failure || state?.error }}</NAlert>
    <div class="lab-controls">
      <NFormItem label="引导固件" :show-feedback="false">
        <NSelect v-model:value="firmware" aria-label="引导固件" :options="[{ label: 'UEFI（推荐）', value: 'uefi' }, { label: 'Legacy BIOS', value: 'bios' }]" :disabled="!!busy || !!state?.asset_id" style="min-width: 170px" />
      </NFormItem>
      <div class="lab-buttons">
        <NButton v-if="!state?.asset_id" type="primary" :loading="busy === 'create'" :disabled="!!busy || !state" @click="create">创建测试 VM</NButton>
        <NTag v-else :type="state.running ? 'success' : 'default'" :bordered="false">VM {{ state.running ? '运行中' : '已停止' }}</NTag>
        <NButton :loading="busy === 'network'" :disabled="!!busy || !state?.asset_id || active || state.network_ready" @click="prepareNetwork">{{ state?.network_ready ? 'PXE 网络已就绪' : '准备 PXE 网络' }}</NButton>
        <NButton :disabled="!!busy || !state?.asset_id || active" @click="power(state?.running ? 'stop' : 'start')">{{ state?.running ? '停止 VM' : '启动 VM' }}</NButton>
        <NButton v-if="state?.running" :disabled="!!busy || active" @click="power('restart')">从磁盘重启</NButton>
        <NPopconfirm v-if="active && !state?.installation.cleanup_pending" @positive-click="power('cancel-install')" positive-text="停止并取消安装" negative-text="继续等待">
          <template #trigger><NButton type="error" secondary :disabled="!!busy">取消安装</NButton></template>
          停止测试虚机并撤销本次安装回调。磁盘可能已被改写，再次测试需要重新安装。
        </NPopconfirm>
      </div>
    </div>
    <div class="lab-install">
      <div>
        <strong>{{ statusLabel }}</strong>
        <p v-if="active">每 5 秒同步真实状态。每个阶段均有时限，超时会停止测试 VM 并结束任务。</p>
        <p v-else-if="installed">Ubuntu 已安装，并已从虚拟磁盘启动验证通过。再次安装会清空测试磁盘。</p>
        <p v-else>安装镜像、应答文件和装机方案由平台自动准备，无需物理机或 BMC。</p>
      </div>
      <NTag v-if="state?.installation?.id" :type="installed ? 'success' : state.installation.status === 'failed' ? 'error' : 'info'" :bordered="false">安装 #{{ state.installation.id }}</NTag>
    </div>
    <template v-if="state?.installation?.id"><NButton v-if="auth.can('tasks:read')" secondary size="small" @click="viewTask">在任务中心查看 #{{ state.installation.operation_id }}</NButton><InstallationProgress :installation="state.installation" /></template>
    <InstallAccessSettings v-if="!active" v-model="access" :disabled="!!busy" />
    <NCheckbox v-model:checked="confirmed" :disabled="!!busy || active || !state?.network_ready">我确认清空 BMM Test VM 的 40 GB 测试磁盘并安装 Ubuntu 22.04.5</NCheckbox>
    <div class="lab-submit">
      <span>仅操作固定测试 VM；DHCP 绑定隔离网桥，不接入物理网卡。</span>
      <NButton type="primary" :loading="busy === 'install'" :disabled="!!busy || active || !state?.network_ready || !state?.plan_id || !confirmed || !!installAccessError(access)" @click="install">{{ installed ? '重新安装 Ubuntu' : '开始 PXE 安装' }}</NButton>
    </div>
    <div class="console-heading"><strong>实时串口日志</strong><NButton text size="small" :disabled="!!busy" @click="refresh()">刷新状态</NButton></div>
    <pre ref="consoleElement" class="lab-console" aria-label="虚拟机串口日志">{{ state?.serial_log || (state ? '等待虚拟机启动。完成网络准备后，点击“开始 PXE 安装”。' : '正在连接 PXE 控制服务…') }}</pre>
    <p class="lab-footnote">Ubuntu 镜像通过 HTTP 从本机下载。安装进度来自真实文件请求与客户端回调；虚拟机使用 KVM 加速，不可用时自动回退。</p>
  </NModal>
</template>

<style scoped>
.lab-intro { margin: -2px 0 20px; color: var(--color-text-secondary); }
.lab-steps { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); border: 1px solid var(--color-border); border-radius: 8px; margin-bottom: 20px; }
.lab-step { display: flex; gap: 10px; padding: 16px 12px; min-width: 0; }
.lab-step + .lab-step { border-left: 1px solid var(--color-border); }
.step-number { font-family: monospace; color: var(--color-text-tertiary); }
.done .step-number { color: var(--color-success); }
.lab-step strong, .lab-step small { display: block; }
.lab-step small { font-size: 12px; color: var(--color-text-secondary); margin-top: 6px; line-height: 1.7; }
.lab-alert { margin: 12px 0; overflow-wrap: anywhere; }
.lab-controls { display: flex; align-items: end; flex-wrap: wrap; gap: 16px; margin-bottom: 20px; }
.lab-buttons { display: flex; flex-wrap: wrap; align-items: center; gap: 8px; }
.lab-install { display: flex; justify-content: space-between; align-items: center; gap: 16px; padding-top: 16px; border-top: 1px solid var(--color-border); }
.lab-install p { color: var(--color-text-secondary); margin: 6px 0 14px; font-size: 13px; }
.lab-submit { display: flex; justify-content: space-between; align-items: center; gap: 16px; margin: 16px 0 24px; }
.lab-submit > span, .lab-footnote { color: var(--color-text-secondary); font-size: 12px; }
.console-heading { display: flex; justify-content: space-between; align-items: center; margin-bottom: 8px; }
.lab-console { background: var(--color-nav-bg); color: #d3e1ec; border-radius: 6px; padding: 16px; height: 240px; overflow: auto; white-space: pre-wrap; overflow-wrap: anywhere; font: 12px/1.65 ui-monospace, SFMono-Regular, Consolas, monospace; margin: 0; }
.lab-footnote { line-height: 1.7; margin-bottom: 0; }
@media (max-width: 640px) { .lab-steps { grid-template-columns: 1fr; } .lab-step + .lab-step { border-left: 0; border-top: 1px solid var(--color-border); } .lab-step { padding: 10px 12px; } .lab-submit { flex-direction: column; align-items: stretch; } }
</style>
