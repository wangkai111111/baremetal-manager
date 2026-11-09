<script setup lang="ts">
import { ref } from 'vue'
import { NTabs, NTabPane, NButton } from 'naive-ui'
import ResourceView from './ResourceView.vue'
import type { Section } from '../catalog'
const active = ref('plans')
const resources: { key: string; section: Section }[] = [
  { key: 'images', section: { path: '/installations', title: '镜像模板', subtitle: '配置镜像引用与安装参数', api: '/image-templates', permission: 'images:read', fields: [{ key: 'name', label: '名称', required: true }, { key: 'image_id', label: '镜像 ID', type: 'number', required: true }, { key: 'config', label: '参数 JSON', type: 'json' }], columns: ['name', 'image_id', 'created_at'] } },
  { key: 'partitions', section: { path: '/installations', title: '分区模板', subtitle: '维护真实分区布局', api: '/partition-templates', permission: 'install:read', fields: [{ key: 'name', label: '名称', required: true }, { key: 'layout', label: '布局 JSON', type: 'json', required: true }], columns: ['name', 'revision', 'created_at'] } },
  { key: 'scripts', section: { path: '/installations', title: '安装后脚本', subtitle: '仅保存配置，不执行脚本', api: '/post-install-scripts', permission: 'install:read', fields: [{ key: 'name', label: '名称', required: true }, { key: 'interpreter', label: '解释器', required: true }, { key: 'content', label: '脚本内容', type: 'textarea', required: true }, { key: 'sha256', label: 'SHA256', required: true }, { key: 'timeout_seconds', label: '超时秒数', type: 'number' }], columns: ['name', 'interpreter', 'revision', 'created_at'] } },
  { key: 'plans', section: { path: '/installations', title: '装机方案', subtitle: '仅管理方案配置；自动化安装执行尚不可用，不会触发安装。', api: '/install-plans', permission: 'tasks:read', fields: [{ key: 'name', label: '方案名称', required: true }, { key: 'image_template_id', label: '镜像模板 ID', type: 'number', required: true }, { key: 'kickstart_template_id', label: '应答模板 ID', type: 'number', required: true }, { key: 'partition_template_id', label: '分区模板 ID', type: 'number', required: true }, { key: 'network_id', label: '网络 ID', type: 'number' }], columns: ['name', 'image_template_id', 'kickstart_template_id', 'partition_template_id', 'created_at'] } },
  { key: 'tasks', section: { path: '/tasks', title: '装机任务记录', subtitle: '查看已有任务的真实状态', api: '/installations', permission: 'tasks:read', fields: [], columns: ['kind', 'asset_id', 'status', 'error', 'created_at'], readonly: true } }
]
</script>
<template>
  <div>
    <n-button disabled>执行安装（尚不可用）</n-button>
    <n-tabs v-model:value="active" type="line">
      <n-tab-pane v-for="item in resources" :key="item.key" :name="item.key" :tab="item.section.title">
        <ResourceView v-if="active === item.key" :section="item.section" :key="item.key" />
      </n-tab-pane>
    </n-tabs>
  </div>
</template>
