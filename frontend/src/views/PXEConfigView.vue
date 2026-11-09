<script setup lang="ts">
import ResourceView from './ResourceView.vue'
import type { Section } from '../catalog'
const section: Section = {
  path: '/pxe-config', title: 'PXE 配置', subtitle: '保存网络引导配置；配置保存不代表引导服务已部署。', api: '/pxe-configs', permission: 'network:read',
  fields: [{ key: 'name', label: '配置名称', required: true }, { key: 'network_id', label: '网络 ID', type: 'number', required: true }, { key: 'boot_mode', label: '启动模式', type: 'select', options: ['uefi', 'bios'], required: true }, { key: 'next_server', label: '引导服务器', required: true }, { key: 'boot_file', label: '引导文件', required: true }],
  columns: ['name', 'network_id', 'boot_mode', 'next_server', 'boot_file', 'created_at']
}
</script>
<template><ResourceView :section="section" /></template>
