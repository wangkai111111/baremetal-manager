<script setup lang="ts">
import {
  NConfigProvider,
  NDialogProvider,
  NMessageProvider,
  NNotificationProvider,
  zhCN,
  dateZhCN
} from 'naive-ui'

const themeOverrides = {
  common: {
    primaryColor: '#267b82',
    primaryColorHover: '#31969c',
    primaryColorPressed: '#1a5d65',
    borderRadius: '6px',
    fontFamily: 'Inter, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif'
  }
}
</script>

<template>
  <n-config-provider
    :locale="zhCN"
    :date-locale="dateZhCN"
    :theme-overrides="themeOverrides"
  >
    <n-dialog-provider>
      <n-notification-provider>
        <n-message-provider>
          <router-view />
        </n-message-provider>
      </n-notification-provider>
    </n-dialog-provider>
  </n-config-provider>
</template>
