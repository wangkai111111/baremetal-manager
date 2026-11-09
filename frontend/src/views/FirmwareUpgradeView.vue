<script setup lang="ts">
import { ref } from 'vue'
import { NTabs, NTabPane, NButton } from 'naive-ui'
import ResourceView from './ResourceView.vue'
import type { Section } from '../catalog'
const active = ref('packages')
const resources: { key: string; section: Section }[] = [
  { key: 'packages', section: { path: '/firmware', title: '固件包', subtitle: '管理固件包元数据；实际文件上传和升级执行尚未开放。', api: '/firmware-packages', permission: 'firmware:read', fields: [{ key: 'name', label: '名称', required: true }, { key: 'version', label: '版本', required: true }, { key: 'component', label: '组件', required: true }, { key: 'vendor', label: '厂商', required: true }, { key: 'compatible_models', label: '兼容型号 JSON 数组', type: 'json', required: true }, { key: 'object_key', label: '存储对象键', required: true }, { key: 'size_bytes', label: '文件大小（字节）', type: 'number', required: true }, { key: 'sha256', label: 'SHA256', required: true }], columns: ['name', 'version', 'component', 'created_at'] } },
  { key: 'baselines', section: { path: '/firmware', title: '固件基线', subtitle: '维护目标版本基线，不会执行设备升级。', api: '/firmware-baselines', permission: 'firmware:read', fields: [{ key: 'name', label: '名称', required: true }, { key: 'component', label: '组件', required: true }, { key: 'firmware_package_id', label: '固件包 ID', type: 'number', required: true }], columns: ['name', 'component', 'firmware_package_id', 'created_at'] } },
  { key: 'upgrades', section: { path: '/firmware', title: '升级记录', subtitle: '仅查看后端已有记录；执行通道尚不可用。', api: '/firmware-upgrades', permission: 'firmware:read', fields: [], columns: ['asset_id', 'firmware_package_id', 'status', 'error', 'created_at'], readonly: true } }
]
</script>
<template>
  <div>
    <n-button disabled>执行固件升级（尚不可用）</n-button>
    <n-tabs v-model:value="active" type="line">
      <n-tab-pane v-for="item in resources" :key="item.key" :name="item.key" :tab="item.section.title">
        <ResourceView v-if="active === item.key" :section="item.section" :key="item.key" />
      </n-tab-pane>
    </n-tabs>
  </div>
</template>
