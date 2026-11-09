<script setup lang="ts">
import { onMounted, onUnmounted, ref, nextTick, computed } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { NButton, NModal, useMessage } from 'naive-ui'
import { Terminal } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import '@xterm/xterm/css/xterm.css'
import { api, unwrap, errorText } from '../api'

const route = useRoute()
const router = useRouter()
const message = useMessage()
const container = ref<HTMLElement>()
const state = ref('连接中')

// === 自动重连 ===
const reconnectCount = ref(0)
const maxReconnects = 3
const reconnecting = ref(false)
const reconnectDelay = computed(() => Math.min(1000 * Math.pow(2, reconnectCount.value), 8000))

// === 会话超时倒计时（15 分钟） ===
const sessionDuration = 15 * 60 // 秒
const sessionRemaining = ref(sessionDuration)
const sessionTimer = ref<ReturnType<typeof setInterval> | null>(null)
const showSessionWarning = computed(() => sessionRemaining.value <= 120 && sessionRemaining.value > 0)
const sessionTimerText = computed(() => {
  const mins = Math.floor(sessionRemaining.value / 60)
  const secs = sessionRemaining.value % 60
  return `${mins}:${String(secs).padStart(2, '0')}`
})

// === 全屏模式 ===
const isFullscreen = ref(false)

// === 快捷键提示弹窗 ===
const showShortcuts = ref(false)

// === 快捷键列表 ===
const shortcuts = [
  { keys: 'Ctrl + C', desc: '发送中断信号（SIGINT）' },
  { keys: 'Ctrl + D', desc: '发送 EOF 信号' },
  { keys: 'Ctrl + Z', desc: '挂起当前进程' },
  { keys: 'Esc', desc: '退出全屏模式' },
  { keys: 'Ctrl + L', desc: '清屏' },
  { keys: 'Arrow Up/Down', desc: '浏览历史命令' },
  { keys: 'Tab', desc: '命令自动补全' }
]

const terminal = new Terminal({
  cursorBlink: true,
  convertEol: true,
  fontSize: 13,
  fontFamily: 'ui-monospace, SFMono-Regular, Menlo, monospace',
  theme: {
    background: '#101c2b',
    foreground: '#d4e4e8',
    cursor: '#66c9c8'
  }
})
const fit = new FitAddon()

let socket: WebSocket | undefined
let observer: ResizeObserver | undefined
let disposed = false
let reconnectTimer: ReturnType<typeof setTimeout> | undefined
terminal.onData(data => {
  if (socket?.readyState === WebSocket.OPEN) socket.send(data)
})

// === 建立 WebSocket 连接 ===
async function connectSocket() {
  if (disposed) return
  const protocol = location.protocol === 'https:' ? 'wss:' : 'ws:'
  const token = sessionStorage.getItem('bmm_token')
  const projectId = sessionStorage.getItem('bmm_project_id')

  if (!token || !projectId) {
    state.value = '缺少认证或项目'
    return
  }

  state.value = '连接中'
  reconnecting.value = true

  try {
    const ticket = unwrap<{ url: string }>(await api.post(`/assets/${encodeURIComponent(String(route.params.id))}/sol-ticket`))
    if (disposed) return
    const url = new URL(ticket.url, location.origin)
    if (url.origin !== location.origin) throw new Error('拒绝非同源 SOL 地址')
    socket = new WebSocket(`${protocol}//${location.host}${url.pathname}${url.search}`)
  } catch (e) {
    if (disposed) return
    state.value = errorText(e)
    reconnecting.value = false
    message.error(state.value)
    return
  }
  socket.binaryType = 'arraybuffer'

  socket.onopen = () => {
    state.value = '已连接'
    reconnecting.value = false
    reconnectCount.value = 0
    startSessionTimer()
  }

  socket.onmessage = e => {
    terminal.write(
      e.data instanceof ArrayBuffer ? new Uint8Array(e.data) : e.data
    )
  }

  socket.onerror = () => {
    message.error('SOL 连接失败')
  }

  socket.onclose = () => {
    if (disposed) return
    state.value = '连接已断开'
    terminal.write('\r\n[连接已断开]\r\n')
    stopSessionTimer()
    // 自动重连
    if (reconnectCount.value < maxReconnects) {
      reconnecting.value = true
      state.value = `重连中 (${reconnectCount.value + 1}/${maxReconnects})...`
      terminal.write(`\r\n[尝试重连 ${reconnectCount.value + 1}/${maxReconnects}，${reconnectDelay.value / 1000}秒后...]\r\n`)
      reconnectTimer = setTimeout(() => {
        reconnectCount.value++
        connectSocket()
      }, reconnectDelay.value)
    } else {
      reconnecting.value = false
      state.value = '连接失败，已达最大重试次数'
      terminal.write('\r\n[已达最大重连次数，请手动刷新页面]\r\n')
    }
  }

}

// === 会话倒计时 ===
function startSessionTimer() {
  stopSessionTimer()
  sessionRemaining.value = sessionDuration
  sessionTimer.value = setInterval(() => {
    if (sessionRemaining.value > 0) {
      sessionRemaining.value--
    } else {
      stopSessionTimer()
      state.value = '会话已超时'
      terminal.write('\r\n[会话已超时，连接将被关闭]\r\n')
      socket?.close()
    }
  }, 1000)
}

function stopSessionTimer() {
  if (sessionTimer.value) {
    clearInterval(sessionTimer.value)
    sessionTimer.value = null
  }
}

// === 导出日志 ===
function exportLog() {
  // 获取终端内容
  let content = ''
  const buffer = terminal.buffer.active
  for (let i = 0; i < buffer.length; i++) {
    const line = buffer.getLine(i)
    if (line) {
      const text = line.translateToString(true)
      if (text.trim()) content += text + '\n'
    }
  }

  // 创建并下载文件
  const blob = new Blob([content], { type: 'text/plain;charset=utf-8' })
  const url = URL.createObjectURL(blob)
  const link = document.createElement('a')
  link.href = url
  link.download = `sol-log-${route.params.id}-${new Date().toISOString().slice(0, 10)}.txt`
  link.click()
  URL.revokeObjectURL(url)
  message.success('日志已导出')
}

// === 全屏切换 ===
function toggleFullscreen() {
  isFullscreen.value = !isFullscreen.value
  nextTick(() => {
    fit.fit()
  })
}

// === 键盘事件监听（Esc 退出全屏） ===
function handleKeydown(e: KeyboardEvent) {
  if (e.key === 'Escape' && isFullscreen.value) {
    isFullscreen.value = false
    nextTick(() => fit.fit())
  }
}

onMounted(async () => {
  await nextTick()
  terminal.loadAddon(fit)
  terminal.open(container.value!)
  fit.fit()

  connectSocket()

  observer = new ResizeObserver(() => fit.fit())
  observer.observe(container.value!)

  window.addEventListener('keydown', handleKeydown)
})

onUnmounted(() => {
  disposed = true
  if (reconnectTimer) clearTimeout(reconnectTimer)
  observer?.disconnect()
  socket?.close()
  terminal.dispose()
  stopSessionTimer()
  window.removeEventListener('keydown', handleKeydown)
})
</script>

<template>
  <div :class="['page', { 'terminal-fullscreen': isFullscreen }]">
    <div v-if="!isFullscreen" class="page-head">
      <div>
        <div class="eyebrow dark">REMOTE CONSOLE / SOL</div>
        <h1>串口控制台</h1>
        <p>
          资产 {{ route.params.id }} · {{ state }}。请注意，串口输入将直接发送至目标设备。
        </p>
      </div>
      <n-button @click="router.push(`/assets/${route.params.id}`)">
        ← 返回资产详情
      </n-button>
    </div>

    <!-- SOL 工具栏 -->
    <div class="sol-toolbar" :style="isFullscreen ? 'position: absolute; top: 8px; right: 8px; z-index: 10; background: rgba(16,28,43,0.9); padding: 8px 12px; border-radius: 4px;' : ''">
      <!-- 会话倒计时 -->
      <div :class="['sol-timer', { warning: showSessionWarning }]">
        剩余时间：{{ sessionTimerText }}
      </div>

      <!-- 重连状态 -->
      <div v-if="reconnecting" class="sol-reconnect-info">
        {{ state }}
      </div>

      <!-- 全屏按钮 -->
      <n-button size="small" @click="toggleFullscreen">
        {{ isFullscreen ? '退出全屏' : '全屏' }}
      </n-button>

      <!-- 导出日志按钮 -->
      <n-button size="small" @click="exportLog">
        导出日志
      </n-button>

      <!-- 快捷键提示按钮 -->
      <n-button size="small" @click="showShortcuts = true">
        快捷键
      </n-button>

      <!-- 返回按钮（全屏模式下显示） -->
      <n-button v-if="isFullscreen" size="small" @click="router.push(`/assets/${route.params.id}`)">
        ← 返回
      </n-button>
    </div>

    <div class="terminal-frame">
      <div class="terminal-header">
        <span class="status-dot"></span>
        SERIAL OVER LAN
        <span class="terminal-state">{{ state }}</span>
      </div>
      <div ref="container" class="terminal-body" :style="isFullscreen ? 'height: calc(100vh - 50px);' : ''"></div>
    </div>

    <!-- 快捷键弹窗 -->
    <n-modal
      v-model:show="showShortcuts"
      preset="card"
      title="串口快捷键"
      style="width: min(420px, 94vw)"
    >
      <div style="display: flex; flex-direction: column; gap: 10px;">
        <div
          v-for="item in shortcuts"
          :key="item.keys"
          style="display: flex; align-items: center; gap: 12px; padding: 6px 0; border-bottom: 1px solid #edf0f0;"
        >
          <code style="background: #f0f7f7; padding: 4px 8px; border-radius: 3px; font-size: 12px; min-width: 100px; text-align: center; color: #267b82;">
            {{ item.keys }}
          </code>
          <span style="font-size: 13px; color: #29404d;">{{ item.desc }}</span>
        </div>
      </div>
    </n-modal>
  </div>
</template>
