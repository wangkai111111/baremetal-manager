import { createApp } from 'vue'
import { createPinia } from 'pinia'
import { createDiscreteApi, darkTheme } from 'naive-ui'
import App from './App.vue'
import router from './router'
import './style.css'

// 在挂载前读取主题偏好，确保离散 API 使用正确的主题
const storedTheme = localStorage.getItem('darkMode')
let isDark = false
if (storedTheme !== null) {
  isDark = storedTheme === 'true'
} else {
  isDark = window.matchMedia('(prefers-color-scheme: dark)').matches
}

// 同步 data-theme 属性到 html 元素，确保 CSS 变量在挂载前生效
document.documentElement.setAttribute('data-theme', isDark ? 'dark' : 'light')

// 创建离散 API（消息、对话框等），与 App.vue 中的主题保持一致
const { message, dialog, notification, loadingBar } = createDiscreteApi(
  ['message', 'dialog', 'notification', 'loadingBar'],
  {
    configProviderProps: {
      theme: isDark ? darkTheme : undefined
    }
  }
)

const app = createApp(App)
app.use(createPinia())
app.use(router)
app.provide('message', message)
app.provide('dialog', dialog)
app.provide('notification', notification)
app.provide('loadingBar', loadingBar)
app.mount('#app')
