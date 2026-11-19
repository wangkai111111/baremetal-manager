<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { NAlert, NButton, NCheckbox, NColorPicker, NEmpty, NInput, NInputNumber, NSpace, NSwitch, NTag, useMessage } from 'naive-ui'
import type { RecordData } from '../api'
export type SettingOption = {key: string; label: string; description: string; kind: 'text' | 'textarea' | 'number' | 'boolean' | 'color' | 'secret'; category: string; default: string | number | boolean}
const props = defineProps<{options: SettingOption[]; items: RecordData[]; disabled: boolean; saving: boolean}>()
const emit = defineEmits<{'save': [values: Record<string, unknown>]}>()
const message = useMessage()
const selected = ref<string[]>([]), draft = reactive<Record<string, any>>({}), clearPassword = ref(false)
const groups = computed(() => [...new Set(props.options.map(row => row.category))].map(category => ({category, options: props.options.filter(row => row.category === category)})))
const allSelected = computed(() => !!props.options.length && selected.value.length === props.options.length)
function reset() {
  selected.value = []; clearPassword.value = false
  for (const option of props.options) draft[option.key] = option.kind === 'secret' ? '' : props.items.find(row => row.key === option.key)?.value ?? option.default
}
watch(() => [props.items, props.options], reset, {immediate:true})
function select(key: string, checked: boolean) { selected.value = checked ? [...selected.value, key] : selected.value.filter(value => value !== key) }
function save() {
  if (props.disabled || props.saving || !selected.value.length) return
  const values: Record<string, unknown> = {}
  for (const option of props.options.filter(row => selected.value.includes(row.key))) {
    const value = draft[option.key]
    if (option.kind === 'secret') {
      if (clearPassword.value) values[option.key] = ''
      else if (value) values[option.key] = value
      continue
    }
    if (option.kind === 'number' && (!Number.isInteger(value) || value < 1 || value > 65535)) { message.warning('邮件服务端口需为 1 到 65535 的整数'); return }
    if (option.key === 'brand.platform_name' && !String(value).trim()) { message.warning('平台名称不能为空'); return }
    values[option.key] = value
  }
  if (!Object.keys(values).length) { message.warning('请输入新密码或勾选“清除已保存密码”'); return }
  emit('save', values)
}
</script>
<template>
  <section class="settings-catalog" aria-label="全部可用配置">
    <NAlert type="info" :bordered="false">所有可用的全局设置都列在这里。勾选需要修改的项目，填写内容后统一保存；未勾选的项目保持原值。</NAlert>
    <div class="catalog-actions"><NCheckbox :checked="allSelected" :indeterminate="selected.length > 0 && !allSelected" :disabled="disabled || saving" @update:checked="selected = $event ? options.map(row => row.key) : []">选择全部 {{ options.length }} 项</NCheckbox><NTag v-if="selected.length" type="info">已选择 {{ selected.length }} 项</NTag><NButton quaternary :disabled="disabled || saving" @click="reset">恢复已保存值</NButton></div>
    <NEmpty v-if="!options.length" description="尚未加载配置目录，请刷新页面重试" />
    <section v-for="group in groups" :key="group.category" class="catalog-group">
      <h3>{{ group.category }}</h3>
      <div class="catalog-grid"><article v-for="option in group.options" :key="option.key" class="catalog-option" :class="{'catalog-option-selected':selected.includes(option.key)}">
        <NCheckbox :checked="selected.includes(option.key)" :disabled="disabled || saving" @update:checked="select(option.key, $event)">{{ option.label }}</NCheckbox>
        <p>{{ option.description }}</p>
        <NSwitch v-if="option.kind === 'boolean'" v-model:value="draft[option.key]" :disabled="disabled || saving || !selected.includes(option.key)" :aria-label="option.label"><template #checked>开启</template><template #unchecked>关闭</template></NSwitch>
        <NInputNumber v-else-if="option.kind === 'number'" v-model:value="draft[option.key]" :min="1" :max="65535" :precision="0" :disabled="disabled || saving || !selected.includes(option.key)" :input-props="{'aria-label':option.label}" />
        <NColorPicker v-else-if="option.kind === 'color'" v-model:value="draft[option.key]" :show-alpha="false" :modes="['hex']" :disabled="disabled || saving || !selected.includes(option.key)" :aria-label="option.label" />
        <template v-else-if="option.kind === 'secret'"><NInput v-model:value="draft[option.key]" type="password" show-password-on="click" :disabled="disabled || saving || !selected.includes(option.key) || clearPassword" placeholder="留空保留已保存密码" :input-props="{'aria-label':option.label, autocomplete:'new-password'}" /><div class="secret-actions"><span>{{ items.find(row => row.key === option.key)?.value ? '已保存密码' : '尚未设置密码' }}</span><NCheckbox v-model:checked="clearPassword" :disabled="disabled || saving || !selected.includes(option.key)">清除已保存密码</NCheckbox></div></template>
        <NInput v-else v-model:value="draft[option.key]" :type="option.kind === 'textarea' ? 'textarea' : 'text'" :disabled="disabled || saving || !selected.includes(option.key)" :input-props="{'aria-label':option.label}" :autosize="option.kind === 'textarea' ? {minRows:2,maxRows:4} : undefined" />
      </article></div>
    </section>
    <div class="catalog-save"><span>本次修改 {{ selected.length }} 项配置</span><NSpace><NButton :disabled="disabled || saving" @click="reset">重置</NButton><NButton type="primary" :disabled="disabled || !selected.length" :loading="saving" @click="save">保存勾选项</NButton></NSpace></div>
  </section>
</template>
<style scoped>
.settings-catalog { padding:24px; }
.catalog-actions,.secret-actions,.catalog-save { display:flex; align-items:center; flex-wrap:wrap; gap:12px; }
.catalog-actions { margin:20px 0; }
.catalog-group { margin-top:24px; }
.catalog-group h3 { font-size:16px; margin:0 0 12px; }
.catalog-grid { display:grid; grid-template-columns:repeat(2,minmax(0,1fr)); gap:16px; }
.catalog-option { border:1px solid var(--color-border); border-radius:var(--radius-sm); padding:16px; min-width:0; }
.catalog-option-selected { border-color:var(--color-primary); }
.catalog-option p,.secret-actions,.catalog-save { color:var(--color-text-secondary); font-size:12px; line-height:1.7; }
.catalog-option p { margin:8px 0 12px; }
.catalog-option :deep(.n-input-number) { width:100%; }
.secret-actions { margin-top:10px; }
.catalog-save { justify-content:space-between; border-top:1px solid var(--color-border); padding-top:20px; margin-top:24px; }
@media(max-width:700px) { .settings-catalog { padding:16px; } .catalog-grid { grid-template-columns:minmax(0,1fr); } }
</style>
