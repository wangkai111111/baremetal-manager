<script setup lang="ts">
import { computed, reactive, ref } from 'vue'
import { NAlert, NButton, NEmpty, NForm, NFormItem, NInput, NModal, NSelect, NSpace, NSwitch, NTag, useDialog, useMessage } from 'naive-ui'
import { newDHCPReservation, validateDHCPReservations, type DHCPReservation } from '../utils/dhcpReservations'
const props = defineProps<{ modelValue: DHCPReservation[]; disabled: boolean; subnet: string; gateway: string; server: string; mode: string }>()
const emit = defineEmits<{ 'update:modelValue': [value: DHCPReservation[]] }>()
const dialog = useDialog(), message = useMessage()
const open = ref(false), editing = ref(-1)
const form = reactive(newDHCPReservation())
const formats = [{label: '文本（原文精确匹配）', value: 'text'}, {label: '十六进制（冒号分隔）', value: 'hex'}]
const kind = (rule: DHCPReservation) => [rule.circuit_id && 'Circuit-ID', rule.remote_id && 'Remote-ID', rule.mac && 'MAC'].filter(Boolean).join(' + ')
const validation = computed(() => {
  const list = props.modelValue.map(row => ({...row}))
  if (editing.value < 0) list.push({...form}); else list[editing.value] = {...form}
  return validateDHCPReservations(list, {...props, taggedOnly: false, relayProxy: false})
})
function edit(index = -1) {
  if (props.disabled) return
  editing.value = index; Object.assign(form, index < 0 ? newDHCPReservation() : props.modelValue[index]); open.value = true
}
function save() {
  if (props.disabled) return
  if (validation.value) { message.warning(validation.value); return }
  const list = props.modelValue.map(row => ({...row}))
  if (editing.value < 0) list.push({...form}); else list[editing.value] = {...form}
  emit('update:modelValue', list); open.value = false
}
function toggle(index: number, enabled: boolean) {
  if (props.disabled) return
  emit('update:modelValue', props.modelValue.map((row, i) => i === index ? {...row, enabled} : row))
}
function remove(index: number) {
  if (props.disabled) return
  dialog.warning({title: '删除固定 IP 规则', content: `删除“${props.modelValue[index]?.name}”后，点击页面下方“保存全局配置”才会生效。`, positiveText: '删除规则', negativeText: '取消', onPositiveClick: () => { if (!props.disabled) emit('update:modelValue', props.modelValue.filter((_, i) => i !== index)) }})
}
</script>
<template>
  <section class="dhcp-rules" aria-label="标签与固定 IP 规则">
    <div class="rule-heading"><h3>标签与固定 IP 规则</h3><NButton :disabled="disabled || modelValue.length >= 128" @click="edit()">添加固定 IP 规则</NButton></div>
    <p class="help">按交换机 Option 82 的端口标识（Circuit-ID）、交换机标识（Remote-ID）或服务器 MAC 分配固定 IP。同一规则填写多项时必须全部匹配；不同规则不能同时命中同一设备。</p>
    <NEmpty v-if="!modelValue.length" description="尚未添加规则，默认使用动态地址池" size="small" class="rule-empty" />
    <article v-for="(rule, index) in modelValue" :key="index" class="rule-row">
      <div class="rule-top"><strong>{{ rule.name }}</strong><NTag :type="rule.enabled ? 'success' : 'default'">{{ rule.ip }}</NTag><span class="rule-kind">{{ kind(rule) }}</span></div>
      <dl class="rule-match">
        <div v-if="rule.circuit_id"><dt>Circuit-ID · {{ rule.circuit_format === 'hex' ? '十六进制' : '文本' }}</dt><dd>{{ rule.circuit_id }}</dd></div>
        <div v-if="rule.remote_id"><dt>Remote-ID · {{ rule.remote_format === 'hex' ? '十六进制' : '文本' }}</dt><dd>{{ rule.remote_id }}</dd></div>
        <div v-if="rule.mac"><dt>MAC</dt><dd>{{ rule.mac }}</dd></div>
      </dl>
      <NSpace align="center"><NSwitch :value="rule.enabled" :disabled="disabled" :aria-label="`启用规则 ${rule.name}`" @update:value="value => toggle(index, value)" /><span class="help">{{ rule.enabled ? '已启用' : '已停用' }}</span><NButton size="small" :disabled="disabled" @click="edit(index)">编辑</NButton><NButton size="small" type="error" secondary :disabled="disabled" @click="remove(index)">删除</NButton></NSpace>
    </article>
    <NModal v-model:show="open" preset="card" :title="editing < 0 ? '添加固定 IP 规则' : '编辑固定 IP 规则'" class="reservation-modal" :style="{width:'min(760px, calc(100vw - 32px))', maxHeight:'calc(100dvh - 32px)'}" content-style="min-height: 0; overflow-y: auto;" :mask-closable="false">
      <NAlert type="info" :bordered="false">标识需与交换机实际发送的 Option 82 字节完全一致。文本和十六进制可以分别选择；固定 IP 可在动态池内或池外，但必须属于 {{ subnet || '所选装机子网' }}。匹配的固定地址会保留，不分配给其他动态客户端。</NAlert>
      <NForm label-placement="top" :disabled="disabled" class="rule-form">
        <NFormItem label="规则名称" required><NInput v-model:value="form.name" placeholder="例如：机柜 A / 交换机 1 / 端口 10" :maxlength="80" /></NFormItem>
        <NFormItem label="固定 IPv4 地址" required><NInput v-model:value="form.ip" placeholder="例如 10.30.0.110" /></NFormItem>
        <NFormItem label="Circuit-ID 格式"><NSelect v-model:value="form.circuit_format" :options="formats" /></NFormItem>
        <NFormItem label="Circuit-ID（端口标识）"><NInput v-model:value="form.circuit_id" :placeholder="form.circuit_format === 'hex' ? '例如 00:04:00:64:00:0a' : '填写交换机发送的原文；不限制则留空'" /></NFormItem>
        <NFormItem label="Remote-ID 格式"><NSelect v-model:value="form.remote_format" :options="formats" /></NFormItem>
        <NFormItem label="Remote-ID（交换机标识）"><NInput v-model:value="form.remote_id" :placeholder="form.remote_format === 'hex' ? '例如 00:06:00:11:22:33:44:55' : '填写交换机发送的原文；不限制则留空'" /></NFormItem>
        <NFormItem label="限定服务器 MAC（可选）"><NInput v-model:value="form.mac" placeholder="例如 00:11:22:33:44:55；可单独使用" /></NFormItem>
        <NFormItem label="启用此规则"><NSwitch v-model:value="form.enabled" /></NFormItem>
      </NForm>
      <p class="help">至少填写 Circuit-ID、Remote-ID 或 MAC 中的一项。仅 Remote-ID 通常会匹配整台交换机，不宜用于多个设备共用一个固定 IP。</p>
      <NAlert v-if="validation" type="warning" :bordered="false">{{ validation }}</NAlert>
      <template #footer><NSpace justify="end"><NButton @click="open = false">取消</NButton><NButton type="primary" :disabled="disabled || !!validation" @click="save">保存规则</NButton></NSpace></template>
    </NModal>
  </section>
</template>
<style scoped>
.dhcp-rules { margin: 0 0 24px; min-width: 0; }
.rule-heading, .rule-top { display: flex; flex-wrap: wrap; align-items: center; gap: 12px; }
.rule-heading { justify-content: space-between; }
h3 { margin: 0; font-size: 16px; }
.help, dt, .rule-kind { color: var(--color-text-secondary); font-size: 12px; line-height: 1.6; text-wrap: pretty; }
.rule-empty { padding: 20px 0; }
.rule-row { padding: 16px; margin-top: 12px; border: 1px solid var(--color-border); border-radius: var(--radius-sm); }
.rule-top strong, dd { overflow-wrap: anywhere; }
.rule-match { display: grid; grid-template-columns: repeat(2, minmax(0,1fr)); gap: 12px; margin: 12px 0; }
dt, dd { margin: 0; }
dd { font-family: monospace; }
.rule-form { margin-top: 20px; display: grid; grid-template-columns: repeat(2,minmax(0,1fr)); gap: 0 20px; align-items: start; }
.rule-form :deep(.n-form-item) { min-width: 0; }
@media (max-width:700px) { .rule-match, .rule-form { grid-template-columns: minmax(0,1fr); } }
</style>
