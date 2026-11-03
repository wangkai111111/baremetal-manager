<script setup lang="ts">
// PXE 网络引导配置页面 — 表单式配置管理
import { ref, watch } from 'vue'
import {
  NButton,
  NInput,
  NInputNumber,
  NForm,
  NFormItem,
  useMessage
} from 'naive-ui'
import { errorText, resource, rows, type RecordData } from '../api'
import { useAuth } from '../stores/auth'

const message = useMessage()
const auth = useAuth()

// === 表单数据状态 ===
const loading = ref(false)
const saving = ref(false)
const form = ref<RecordData>({
  dhcp_server: '',
  tftp_server: '',
  http_boot_server: '',
  vlan_id: null,
  dhcp_range_start: '',
  dhcp_range_end: ''
})

// === 加载当前 PXE 配置 ===
async function load() {
  loading.value = true
  try {
    const data = rows(await resource.list('/pxe-config', { page: 1, page_size: 1 }))
    if (data.items.length) {
      const item = data.items[0]
      form.value = {
        dhcp_server: item.dhcp_server || '',
        tftp_server: item.tftp_server || '',
        http_boot_server: item.http_boot_server || '',
        vlan_id: item.vlan_id || null,
        dhcp_range_start: item.dhcp_range_start || '',
        dhcp_range_end: item.dhcp_range_end || ''
      }
    }
  } catch (e) {
    message.error(errorText(e))
  } finally {
    loading.value = false
  }
}

watch(
  () => auth.projectId,
  () => load(),
  { immediate: true }
)

// === 保存 PXE 配置 ===
async function save() {
  saving.value = true
  try {
    await resource.create('/pxe-config', {
      dhcp_server: form.value.dhcp_server,
      tftp_server: form.value.tftp_server,
      http_boot_server: form.value.http_boot_server,
      vlan_id: form.value.vlan_id,
      dhcp_range_start: form.value.dhcp_range_start,
      dhcp_range_end: form.value.dhcp_range_end
    })
    message.success('PXE 配置已保存')
  } catch (e) {
    message.error(errorText(e))
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <div class="page">
    <div class="page-head">
      <div>
        <div class="eyebrow dark">NETWORK / PXE CONFIGURATION</div>
        <h1>PXE 配置</h1>
        <p>网络引导服务配置与 DHCP 参数设置</p>
      </div>
    </div>
    <div class="panel" style="max-width: 720px;">
      <div class="panel-toolbar">
        <div>
          <div class="panel-title">PXE 网络引导参数</div>
          <p class="panel-caption">配置 DHCP、TFTP 和 HTTP 启动服务地址</p>
        </div>
        <div class="toolbar-actions">
          <n-button quaternary @click="load" :loading="loading">刷新</n-button>
        </div>
      </div>
      <div style="padding: 24px;">
        <n-form label-placement="left" label-width="160" require-mark-placement="right-hanging">
          <n-form-item label="DHCP 服务地址" required>
            <n-input
              v-model:value="(form.dhcp_server as string)"
              placeholder="例如：192.168.1.1"
            />
          </n-form-item>
          <n-form-item label="TFTP 服务地址" required>
            <n-input
              v-model:value="(form.tftp_server as string)"
              placeholder="例如：192.168.1.1"
            />
          </n-form-item>
          <n-form-item label="HTTP 启动服务地址" required>
            <n-input
              v-model:value="(form.http_boot_server as string)"
              placeholder="例如：http://192.168.1.1:8080"
            />
          </n-form-item>
          <n-form-item label="VLAN ID">
            <n-input-number
              v-model:value="(form.vlan_id as number)"
              :min="0"
              :max="4095"
              style="width: 100%"
              placeholder="留空表示不使用 VLAN"
            />
          </n-form-item>
          <n-form-item label="DHCP 范围起始 IP">
            <n-input
              v-model:value="(form.dhcp_range_start as string)"
              placeholder="例如：192.168.1.100"
            />
          </n-form-item>
          <n-form-item label="DHCP 范围结束 IP">
            <n-input
              v-model:value="(form.dhcp_range_end as string)"
              placeholder="例如：192.168.1.200"
            />
          </n-form-item>
          <div style="display: flex; justify-content: flex-end; gap: 9px; margin-top: 20px; padding-top: 18px; border-top: 1px solid #edf0f0;">
            <n-button type="primary" :loading="saving" @click="save">保存配置</n-button>
          </div>
        </n-form>
      </div>
    </div>
  </div>
</template>
