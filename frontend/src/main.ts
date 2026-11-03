import { createApp } from 'vue'
import { createPinia } from 'pinia'
import { createDiscreteApi } from 'naive-ui'
import App from './App.vue'
import router from './router'
import './style.css'

const app = createApp(App)
app.use(createPinia()).use(router)
const { message, dialog } = createDiscreteApi(['message', 'dialog'])
app.provide('message', message)
app.provide('dialog', dialog)
app.mount('#app')
