<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { NAlert, NButton, NEmpty, NFormItem, NInput, NModal, NSelect, NSpace, NTabPane, NTabs, NTag, useDialog, useMessage } from 'naive-ui'
import type { DHCPReservation } from '../utils/dhcpReservations'
import { parseReservationText, previewReservationImport, readReservationWorkbook, reservationTemplate, type ImportMode, type ImportSheet, type ImportTable, type ReservationContext, type TemplateKind } from '../utils/dhcpReservationImport'
import { copyToClipboard } from '../utils/clipboard'

const props = defineProps<{ show: boolean; disabled: boolean; rules: DHCPReservation[]; context: ReservationContext }>()
const emit = defineEmits<{ 'update:show': [value: boolean]; apply: [value: DHCPReservation[]] }>()
const message = useMessage(), dialog = useDialog()
const source = ref<'excel' | 'paste'>('excel'), mode = ref<ImportMode>('append'), text = ref('')
const fileInput = ref<HTMLInputElement>(), loading = ref(false), filename = ref(''), fileError = ref('')
const sheets = ref<ImportSheet[]>([]), sheet = ref(''), parsed = ref<ImportTable | null>(null)
const templateKind = ref<TemplateKind>('option82')
const template = computed(() => reservationTemplate(templateKind.value, props.context).map(row => row.join('\t')).join('\n'))
const preview = computed(() => parsed.value ? previewReservationImport(parsed.value, props.rules, mode.value, props.context) : null)
const invalidCount = computed(() => preview.value?.rows.filter(row => row.error).length || 0)
const unavailable = computed(() => props.disabled || loading.value)
const canApply = computed(() => !unavailable.value && !!preview.value?.valid)
const modeOptions = [{label: '追加规则（保留现有规则）', value: 'append'}, {label: '替换全部规则', value: 'replace'}]
const sheetOptions = computed(() => sheets.value.map(row => ({label: row.name, value: row.name})))
let readGeneration = 0
watch(() => props.show, show => {
  readGeneration++; loading.value = false
  if (!show) return
  mode.value = 'append'; text.value = ''; sheets.value = []; filename.value = ''; fileError.value = ''; parsed.value = null; sheet.value = ''
  source.value = 'excel'; templateKind.value = props.context.mode === 'relay' ? 'option82' : 'mac'
})
watch(source, () => { parsed.value = source.value === 'excel' ? sheets.value.find(row => row.name === sheet.value)?.table || null : null })
watch(text, () => { if (source.value === 'paste') parsed.value = null })
watch(sheet, name => { if (source.value === 'excel') parsed.value = sheets.value.find(row => row.name === name)?.table || null })

async function readFile(event: Event) {
  const input = event.target as HTMLInputElement, file = input.files?.[0]
  input.value = ''
  if (!file || unavailable.value) return
  parsed.value = null; sheets.value = []; filename.value = file.name; fileError.value = ''
  if (!/\.(xlsx|xls)$/i.test(file.name)) { fileError.value = '请选择 .xlsx 或 .xls 格式的 Excel 文件。'; return }
  if (file.size > 2 * 1024 * 1024) { fileError.value = 'Excel 文件不能超过 2 MB。'; return }
  const generation = ++readGeneration
  loading.value = true
  try {
    const result = await readReservationWorkbook(await file.arrayBuffer())
    if (generation !== readGeneration || !props.show) return
    sheets.value = result
    sheet.value = (sheets.value.find(row => !row.table.error && row.table.rows.length) || sheets.value[0])?.name || ''
    if (source.value === 'excel') parsed.value = sheets.value.find(row => row.name === sheet.value)?.table || null
  } catch { if (generation === readGeneration) fileError.value = '无法读取 Excel 文件，请确认文件未加密且内容完整，或改用粘贴导入。' }
  finally { if (generation === readGeneration) loading.value = false }
}
function parseText() { if (!unavailable.value) parsed.value = parseReservationText(text.value) }
async function copyTemplate() {
  try { await copyToClipboard(template.value); message.success('模板已复制，可粘贴到 Excel 编辑后导入') }
  catch { message.warning('复制失败，请手动选择下方模板内容复制') }
}
async function downloadTemplate() {
  if (unavailable.value) return
  loading.value = true
  try {
    const XLSX = await import('xlsx'), workbook = XLSX.utils.book_new()
    for (const kind of ['option82', 'mac'] as const) {
      const sheet = XLSX.utils.aoa_to_sheet(reservationTemplate(kind, props.context))
      sheet['!cols'] = Array.from({length: kind === 'mac' ? 4 : 8}, () => ({wch: 24}))
      XLSX.utils.book_append_sheet(workbook, sheet, kind === 'mac' ? 'MAC固定IP模板' : 'Option82标签模板')
    }
    XLSX.writeFile(workbook, 'PXE固定IP规则导入模板.xlsx')
  } catch { message.error('模板生成失败，请使用粘贴区的模板') }
  finally { loading.value = false }
}
function apply() {
  if (!canApply.value) return
  const perform = () => {
    // Recheck the current form/context after confirmation, not an old preview.
    if (!canApply.value || !preview.value) return false
    emit('apply', preview.value.rules.map(row => ({...row}))); emit('update:show', false)
    message.success('规则已加入待保存配置，请点击“保存全局配置”生效')
  }
  if (mode.value === 'replace' && props.rules.length) {
    dialog.warning({title: '替换全部固定 IP 规则', content: `将以导入的 ${preview.value?.rows.length} 条规则替换现有 ${props.rules.length} 条规则。点击“保存全局配置”后才会生效。`, positiveText: '确认替换', negativeText: '取消', onPositiveClick: perform})
  } else perform()
}
</script>
<template>
  <NModal :show="show" @update:show="emit('update:show', $event)" preset="card" title="批量导入固定 IP 规则" class="reservation-import-modal" :style="{width:'min(1080px, calc(100vw - 32px))', maxHeight:'calc(100dvh - 32px)'}" content-style="min-height: 0; overflow-y: auto;" :mask-closable="false">
    <NAlert type="info" :bordered="false">每次最多导入 128 条，总规则数最多 128 条。表头必填；启用列留空默认启用，标识格式留空默认文本。固定 IP 必须属于 {{ context.subnet || '所选装机子网' }}。</NAlert>
    <NTabs v-model:value="source" type="line" animated class="import-tabs">
      <NTabPane name="excel" tab="Excel 导入">
        <div class="import-file-actions"><NButton :disabled="unavailable" :loading="loading" @click="fileInput?.click()">选择 Excel 文件</NButton><NButton :disabled="unavailable" @click="downloadTemplate">下载 Excel 模板</NButton><span class="help">{{ filename || '支持 .xlsx / .xls，最大 2 MB' }}</span></div>
        <input ref="fileInput" type="file" accept=".xlsx,.xls" hidden aria-label="导入 Excel 文件" @change="readFile" />
        <NFormItem v-if="sheets.length > 1" label="选择要导入的工作表" label-placement="top" class="sheet-select"><NSelect v-model:value="sheet" :options="sheetOptions" :disabled="unavailable" /></NFormItem>
        <NAlert v-if="fileError" type="error" :bordered="false">{{ fileError }}</NAlert>
        <p class="help">模板含 Option 82 和 MAC 两张示例工作表；修改示例后选择其中一张导入。可删除不需要的匹配列；未知表头会提示错误。</p>
      </NTabPane>
      <NTabPane name="paste" tab="粘贴导入">
        <div class="template-actions"><NButton :type="templateKind === 'option82' ? 'primary' : 'default'" secondary @click="templateKind = 'option82'">模板一：Option 82 标签</NButton><NButton :type="templateKind === 'mac' ? 'primary' : 'default'" secondary @click="templateKind = 'mac'">模板二：MAC 固定 IP</NButton><NButton @click="copyTemplate">复制模板</NButton></div>
        <p class="help">下方是可复制的模板示例，请替换为真实设备信息。Option 82 同时填写 Circuit-ID 和 Remote-ID 时需全部匹配；十六进制标识用冒号分隔。</p>
        <NInput :value="template" type="textarea" readonly :autosize="{minRows:3,maxRows:4}" :input-props="{'aria-label':'粘贴导入模板', wrap:'off'}" class="import-monospace" />
        <NFormItem label="粘贴全部规则（包含表头）" label-placement="top" class="paste-input"><NInput v-model:value="text" type="textarea" :disabled="unavailable" :maxlength="200000" :autosize="{minRows:5,maxRows:10}" placeholder="在 Excel 中选中表头和所有数据行，复制后粘贴到这里；也支持逗号分隔的 CSV。" :input-props="{'aria-label':'粘贴全部规则'}" class="import-monospace" /></NFormItem>
        <NButton :disabled="unavailable || !text.trim()" @click="parseText">解析并预览</NButton>
      </NTabPane>
    </NTabs>
    <div class="import-options"><NFormItem label="导入方式" label-placement="top"><NSelect v-model:value="mode" :options="modeOptions" :disabled="unavailable" /></NFormItem><p class="help">已有 {{ rules.length }} 条规则。{{ mode === 'append' ? '追加会同时检查与现有规则的冲突。' : '替换会移除所有现有规则，包括已停用的规则。' }}导入后仍需保存全局配置。</p></div>
    <NAlert v-if="disabled" type="warning" :bordered="false">配置当前不可编辑，请关闭此窗口并检查服务状态后重试。</NAlert>
    <section class="import-preview" aria-label="导入预览" aria-live="polite">
      <div class="preview-heading"><h3>导入预览</h3><NTag v-if="preview?.rows.length" :type="preview.valid ? 'success' : 'warning'">{{ preview.rows.length }} 条规则 · {{ invalidCount }} 条错误</NTag></div>
      <NAlert v-if="preview?.error" type="error" :bordered="false">{{ preview.error }}</NAlert>
      <p v-if="invalidCount" class="help">请按行号修正源表格或粘贴内容后重新解析；不会跳过错误行或部分导入。</p>
      <div v-if="preview?.rows.length" class="preview-table-wrap" tabindex="0" role="region" aria-label="规则校验明细，可横向滚动">
        <table class="preview-table"><thead><tr><th scope="col">原始行</th><th scope="col">规则名称 / 固定 IP</th><th scope="col">匹配条件</th><th scope="col">启用</th><th scope="col">校验结果</th></tr></thead><tbody>
          <tr v-for="row in preview.rows" :key="row.line"><td>{{ row.line }}</td><td><strong>{{ row.rule.name || '未命名' }}</strong><div class="mono">{{ row.rule.ip || '未填写' }}</div></td><td><div v-if="row.rule.circuit_id" class="mono">Circuit-ID ({{ row.rule.circuit_format === 'hex' ? '十六进制' : '文本' }})：{{ row.rule.circuit_id }}</div><div v-if="row.rule.remote_id" class="mono">Remote-ID ({{ row.rule.remote_format === 'hex' ? '十六进制' : '文本' }})：{{ row.rule.remote_id }}</div><div v-if="row.rule.mac" class="mono">MAC：{{ row.rule.mac }}</div><span v-if="!row.rule.circuit_id && !row.rule.remote_id && !row.rule.mac">未填写</span></td><td>{{ row.rule.enabled ? '是' : '否' }}</td><td><NTag :type="row.error ? 'error' : 'success'" size="small">{{ row.error ? '需修正' : '通过' }}</NTag><p v-if="row.error" class="row-error">{{ row.error }}</p></td></tr>
        </tbody></table>
      </div>
      <NEmpty v-else-if="!preview?.error" description="选择 Excel 文件，或粘贴后点击“解析并预览”" size="small" class="preview-empty" />
    </section>
    <template #footer><div class="import-footer"><span class="help">{{ preview?.valid ? `校验通过，导入后共 ${preview.rules.length} 条规则` : '全部规则校验通过后方可导入' }}</span><NSpace><NButton @click="emit('update:show', false)">取消</NButton><NButton type="primary" :disabled="!canApply" @click="apply">{{ mode === 'replace' ? '替换全部规则' : '追加到规则列表' }}</NButton></NSpace></div></template>
  </NModal>
</template>
<style scoped>
.help { color: var(--color-text-secondary); font-size: 12px; line-height: 1.6; text-wrap: pretty; overflow-wrap: anywhere; }
.import-tabs { margin-top: 16px; }
.template-actions, .import-file-actions, .preview-heading, .import-footer { display: flex; align-items: center; flex-wrap: wrap; gap: 12px; }
.import-file-actions .help { min-width: 0; }
.sheet-select, .paste-input { margin-top: 16px; }
.import-monospace :deep(textarea), .mono { font-family: monospace; }
.import-monospace.n-input :deep(.n-input__textarea-el), .import-monospace.n-input :deep(.n-input__textarea-mirror) { white-space: pre; overflow-wrap: normal; word-break: normal; }
.import-options { display: grid; grid-template-columns: minmax(220px, 320px) minmax(0, 1fr); gap: 20px; margin-top: 24px; align-items: center; }
.import-options .help { margin: 0 0 24px; }
.import-preview { min-width: 0; }
.preview-heading { justify-content: space-between; margin-bottom: 12px; }
h3 { font-size: 15px; margin: 0; }
.preview-table-wrap { overflow-x: auto; margin-top: 12px; border: 1px solid var(--color-border); border-radius: var(--radius-sm); }
.preview-table { border-collapse: collapse; width: 100%; min-width: 720px; text-align: left; font-size: 12px; }
.preview-table th, .preview-table td { padding: 12px; border-bottom: 1px solid var(--color-border); vertical-align: top; overflow-wrap: anywhere; }
.preview-table th { color: var(--color-text-secondary); font-weight: 500; }
.preview-table tr:last-child td { border-bottom: 0; }
.preview-table td:nth-child(2) { min-width: 140px; max-width: 200px; }
.preview-table td:nth-child(3) { max-width: 300px; }
.preview-table td:last-child { min-width: 170px; max-width: 240px; }
.row-error { margin: 6px 0 0; line-height: 1.6; }
.preview-empty { padding: 24px 0; }
.import-footer { justify-content: space-between; }
@media (max-width: 700px) { .import-options { grid-template-columns: minmax(0, 1fr); gap: 0; } .import-options .help { margin-top: -12px; } .import-footer { justify-content: flex-end; } }
</style>
