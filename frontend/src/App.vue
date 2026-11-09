<script setup lang="ts">
import { ref, computed, provide, onMounted } from 'vue'
import {
  NConfigProvider,
  NDialogProvider,
  NMessageProvider,
  NNotificationProvider,
  darkTheme,
  zhCN,
  dateZhCN,
  type GlobalThemeOverrides
} from 'naive-ui'

// 响应式暗色模式状态：初始化时从 localStorage 读取，默认 false
const isDarkMode = ref(false)

onMounted(() => {
  const stored = localStorage.getItem('darkMode')
  if (stored !== null) {
    isDarkMode.value = stored === 'true'
  } else {
    // 如果 localStorage 没有值，检测系统偏好
    isDarkMode.value = window.matchMedia('(prefers-color-scheme: dark)').matches
  }
  // 同步 data-theme 属性到 html 元素
  updateThemeAttribute()
})

// 切换主题函数
function toggleDarkMode() {
  isDarkMode.value = !isDarkMode.value
  localStorage.setItem('darkMode', String(isDarkMode.value))
  updateThemeAttribute()
}

// 设置 data-theme 属性
function updateThemeAttribute() {
  document.documentElement.setAttribute(
    'data-theme',
    isDarkMode.value ? 'dark' : 'light'
  )
}

// 向子组件提供主题切换函数
provide('toggleDarkMode', toggleDarkMode)
provide('isDarkMode', isDarkMode)

// 动态主题
const theme = computed(() => (isDarkMode.value ? darkTheme : null))

// 新的设计令牌
const themeOverrides = computed<GlobalThemeOverrides>(() => ({
  common: {
    primaryColor: '#165DFF',
    primaryColorHover: '#4080FF',
    primaryColorPressed: '#0E42D2',
    successColor: '#00B42A',
    successColorHover: '#27C346',
    successColorPressed: '#009A29',
    warningColor: '#FF7D00',
    warningColorHover: '#FF9A2E',
    warningColorPressed: '#D25F00',
    errorColor: '#F53F3F',
    errorColorHover: '#F76560',
    errorColorPressed: '#CB2634',
    infoColor: '#86909C',
    infoColorHover: '#A9B3BD',
    infoColorPressed: '#6B7785',
    borderRadius: '6px',
    fontFamily: "'PingFang SC', 'Microsoft YaHei', 'Inter', -apple-system, sans-serif",
    fontSize: '14px',
    bodyColor: isDarkMode.value ? '#17171A' : '#F2F3F5'
  },
  Card: {
    borderRadius: '8px',
    borderColor: isDarkMode.value ? '#333335' : '#E5E6EB',
    color: isDarkMode.value ? '#232324' : '#FFFFFF'
  },
  Button: {
    borderRadiusMedium: '6px',
    borderRadiusSmall: '4px',
    borderRadiusLarge: '8px'
  },
  Input: {
    borderRadius: '6px'
  },
  DataTable: {
    borderRadius: '8px'
  }
}))


</script>

<template>
  <n-config-provider
    :theme="theme"
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
