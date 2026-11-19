<script setup lang="ts">
import { ref } from 'vue'
import { NButton, NModal, useMessage } from 'naive-ui'
import { api, errorText } from '../api'
import { useAuth } from '../stores/auth'

const props = defineProps<{ assetId: number; disabled?: boolean; text?: boolean }>()
const emit = defineEmits<{ deleted: []; busy: [boolean] }>()
const auth = useAuth()
const message = useMessage()
const deleting = ref(false)
const showing = ref(false)
let confirmationProject = ''
let confirmationAsset = 0
function openConfirmation() {
  if (props.disabled || deleting.value) return
  confirmationProject = auth.projectId
  confirmationAsset = props.assetId
  showing.value = true
}
async function remove() {
  if (deleting.value) return false
  if (confirmationProject !== auth.projectId || confirmationAsset !== props.assetId) {
    message.error('项目或测试 VM 已变化，请重新确认要删除的测试 VM')
    return false
  }
  deleting.value = true
  emit('busy', true)
  try {
    await api.delete('/assets/test-vm', { data: { confirmed: true, asset_id: confirmationAsset } })
    message.success('测试 VM 及系统磁盘已删除，安装历史和 PXE 资源已保留')
    showing.value = false
    emit('deleted')
    return true
  } catch (e) {
    message.error(errorText(e))
    return false
  } finally {
    deleting.value = false
    emit('busy', false)
  }
}
</script>

<template>
  <template v-if="auth.user?.is_platform_admin">
    <NButton type="error" :text="text" :secondary="!text" :disabled="disabled || deleting" :loading="deleting" @click="openConfirmation">删除测试 VM</NButton>
    <NModal :show="showing" preset="dialog" type="warning" title="永久删除 BMM Test VM？" positive-text="永久删除 VM 和磁盘" negative-text="取消" :closable="!deleting" :mask-closable="!deleting" :close-on-esc="!deleting" :positive-button-props="{ type: 'error', loading: deleting, disabled: deleting }" :negative-button-props="{ disabled: deleting }" style="width: min(480px, calc(100vw - 32px)); max-height: calc(100vh - 48px); overflow: auto" @update:show="!deleting && (showing = $event)" @negative-click="showing = false" @positive-click="remove">
      <p>将停止虚拟机，清除 40 GB 系统磁盘、固件状态和串口日志，并移除服务器记录。磁盘数据无法恢复。</p>
      <p>保留历史任务、安装记录、镜像、安装方案和 PXE 网络。安装进行中须先取消安装并等待清理完成。</p>
    </NModal>
  </template>
</template>
