<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from 'vue'
import { ElMessage } from 'element-plus'
import { taskApi } from '@/api/task'
import { openAuthorizedEventStream, type EventStreamConnection } from '@/utils/sse'
import { useResponsive } from '@/composables/useResponsive'
import { ansiToHtml, normalizeAnsi } from '@/utils/ansi'

const props = defineProps<{
  visible: boolean
  taskId: number | null
  taskName: string
}>()

const emit = defineEmits<{
  'update:visible': [value: boolean]
}>()

const logLines = ref<string[]>([])
const logTail = ref('')
const done = ref(false)
const error = ref<string | null>(null)
const emptyMessage = ref<string | null>(null)
const loading = ref(false)
const logContainerRef = ref<HTMLElement>()
const autoScroll = ref(false)
const fontSize = ref<'sm' | 'md' | 'lg'>('md')
const wrap = ref(true)
const { dialogFullscreen } = useResponsive()
let eventSource: EventStreamConnection | null = null
let logBuffer: string[] = []
let logFlushTimer: ReturnType<typeof setTimeout> | null = null
let reconnectTimer: ReturnType<typeof setTimeout> | null = null
let hiddenAt: number | null = null
let pendingScrollRestore: number | null = null

const hasLogs = computed(() => logLines.value.length > 0 || logTail.value.length > 0)
const renderedLogText = computed(() => {
  const lines = [...logLines.value]
  if (logTail.value !== '' || lines.length === 0) {
    lines.push(logTail.value)
  }
  return lines.join('\n')
})

const renderedLogHtml = computed(() => {
  return ansiToHtml(normalizeAnsi(renderedLogText.value))
})

const lineCount = computed(() => {
  if (!renderedLogText.value) return 0
  return renderedLogText.value.split('\n').length
})

const byteLabel = computed(() => {
  if (!renderedLogText.value) return ''
  const bytes = new Blob([renderedLogText.value]).size
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`
})

const fontSizeClass = computed(() => `log-font-${fontSize.value}`)

watch(() => props.visible, (visible) => {
  if (visible && props.taskId) {
    void startStream()
  } else {
    cleanup()
  }
})

watch(() => props.taskId, (taskId, previousTaskId) => {
  if (props.visible && taskId && taskId !== previousTaskId) {
    void startStream()
  }
})

watch(autoScroll, (enabled) => {
  if (enabled) {
    scheduleScrollToBottom()
  }
})

async function startStream(isReconnect = false) {
  const savedScrollTop = isReconnect ? logContainerRef.value?.scrollTop ?? null : null
  cleanup()
  resetLogOutput()
  done.value = false
  error.value = null
  emptyMessage.value = null
  loading.value = !isReconnect
  pendingScrollRestore = isReconnect && savedScrollTop !== null ? savedScrollTop : null
  if (!isReconnect) {
    autoScroll.value = false
    scheduleScrollToTop()
  }

  if (!props.taskId) {
    loading.value = false
    return
  }

  const url = `/api/v1/logs/${props.taskId}/stream`
  eventSource = openAuthorizedEventStream(url, {
    onOpen() {
      loading.value = false
    },
    onMessage(data) {
      loading.value = false
      if (!data) {
        return
      }
      logBuffer.push(data)
      scheduleBufferFlush()
    },
    onEvent(event) {
      if (event.event !== 'done') {
        return
      }
      flushBufferedLogs()
      done.value = true
      cleanup()
      if (event.data === 'reconnect') {
        reconnectTimer = setTimeout(() => {
          reconnectTimer = null
          void startStream()
        }, 500)
        return
      }
      if (!hasLogs.value) {
        void fetchLatestLog()
      }
    },
    onError() {
      flushBufferedLogs()
      loading.value = false
      done.value = true
      cleanup()
      if (!hasLogs.value) {
        void fetchLatestLog()
      }
    }
  })
}

async function fetchLatestLog(retryCount = 0) {
  try {
    const res = await taskApi.latestLog(props.taskId!) as any
    if (!res) {
      emptyMessage.value = '该任务还没有日志记录'
      return
    }
    if (res.content) {
      resetLogOutput()
      appendLogChunk(String(res.content))
      scheduleScrollToTop()
    } else {
      emptyMessage.value = '日志已过期，文件已被清理'
    }
  } catch (err: any) {
    if (err?.response?.status === 404) {
      if (retryCount < 5 && props.visible) {
        reconnectTimer = setTimeout(() => {
          reconnectTimer = null
          void fetchLatestLog(retryCount + 1)
        }, 500)
        return
      }
      emptyMessage.value = '该任务还没有日志记录'
    } else {
      error.value = '获取日志失败'
    }
  }
}

function resetLogOutput() {
  logLines.value = []
  logTail.value = ''
}

function pushLogLine() {
  logLines.value.push(logTail.value)
  logTail.value = ''
}

function appendLogChunk(chunk: string, commitBoundary = false) {
  if (!chunk && !commitBoundary) return

  let endedWithLineBreak = false
  for (let i = 0; i < chunk.length; i++) {
    const char = chunk[i]
    if (char === '\r') {
      if (chunk[i + 1] === '\n') {
        pushLogLine()
        endedWithLineBreak = true
        i++
        continue
      }
      logTail.value = ''
      endedWithLineBreak = false
      continue
    }

    if (char === '\n') {
      pushLogLine()
      endedWithLineBreak = true
      continue
    }

    logTail.value += char
    endedWithLineBreak = false
  }

  if (commitBoundary && !endedWithLineBreak) {
    pushLogLine()
  }
}

function scheduleBufferFlush() {
  if (logFlushTimer !== null) {
    return
  }
  logFlushTimer = setTimeout(() => {
    logFlushTimer = null
    flushBufferedLogs()
  }, 16)
}

function flushBufferedLogs() {
  if (logFlushTimer !== null) {
    clearTimeout(logFlushTimer)
    logFlushTimer = null
  }
  if (logBuffer.length === 0) {
    return
  }

  for (const chunk of logBuffer) {
    appendLogChunk(chunk, true)
  }
  logBuffer = []

  if (pendingScrollRestore !== null) {
    const target = pendingScrollRestore
    pendingScrollRestore = null
    void nextTick(() => {
      if (logContainerRef.value) {
        logContainerRef.value.scrollTop = target
      }
    })
  } else if (autoScroll.value) {
    scheduleScrollToBottom()
  }
}

function scheduleScrollToBottom() {
  void nextTick(() => {
    scrollToBottom()
  })
}

function scheduleScrollToTop() {
  void nextTick(() => {
    scrollToTop()
  })
}

function scrollToBottom() {
  if (logContainerRef.value) {
    logContainerRef.value.scrollTop = logContainerRef.value.scrollHeight
  }
}

function scrollToTop() {
  if (logContainerRef.value) {
    logContainerRef.value.scrollTop = 0
  }
}

function cleanup() {
  if (logFlushTimer !== null) {
    clearTimeout(logFlushTimer)
    logFlushTimer = null
  }
  logBuffer = []
  if (eventSource) {
    eventSource.close()
    eventSource = null
  }
  if (reconnectTimer !== null) {
    clearTimeout(reconnectTimer)
    reconnectTimer = null
  }
}

function handleVisibilityChange() {
  flushBufferedLogs()
  if (document.hidden) {
    hiddenAt = Date.now()
    return
  }

  if (!props.visible || !props.taskId) {
    hiddenAt = null
    return
  }

  const wasBackgrounded = hiddenAt !== null && Date.now() - hiddenAt > 1500
  hiddenAt = null

  if (done.value) {
    if (!hasLogs.value) {
      void fetchLatestLog()
    }
    return
  }

  if (wasBackgrounded) {
    void startStream(true)
  }
}

function cycleFontSize() {
  if (fontSize.value === 'sm') fontSize.value = 'md'
  else if (fontSize.value === 'md') fontSize.value = 'lg'
  else fontSize.value = 'sm'
}

async function handleCopy() {
  if (!hasLogs.value) {
    ElMessage.warning('暂无内容可复制')
    return
  }
  try {
    await navigator.clipboard.writeText(renderedLogText.value)
    ElMessage.success('已复制')
  } catch {
    const ta = document.createElement('textarea')
    ta.value = renderedLogText.value
    ta.style.position = 'fixed'
    ta.style.left = '-9999px'
    document.body.appendChild(ta)
    ta.select()
    try {
      document.execCommand('copy')
      ElMessage.success('已复制')
    } catch {
      ElMessage.error('复制失败')
    }
    document.body.removeChild(ta)
  }
}

function handleDownload() {
  if (!hasLogs.value) {
    ElMessage.warning('暂无内容可下载')
    return
  }
  const safeName = (props.taskName || 'task').replace(/[\\/:*?"<>|]/g, '_')
  const filename = `${safeName}-${props.taskId ?? 'log'}.log`
  const blob = new Blob([renderedLogText.value], { type: 'text/plain;charset=utf-8' })
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = filename
  document.body.appendChild(a)
  a.click()
  document.body.removeChild(a)
  URL.revokeObjectURL(url)
  ElMessage.success('已下载')
}

onMounted(() => {
  document.addEventListener('visibilitychange', handleVisibilityChange)
})

onUnmounted(() => {
  document.removeEventListener('visibilitychange', handleVisibilityChange)
  cleanup()
})

function handleClose() {
  emit('update:visible', false)
}
</script>

<template>
  <el-dialog
    :model-value="visible"
    width="88%"
    :fullscreen="dialogFullscreen"
    top="5vh"
    align-center
    :show-close="false"
    class="log-viewer-dialog"
    destroy-on-close
    @close="handleClose"
  >
    <template #header>
      <div class="viewer-hero">
        <div class="viewer-hero-main">
          <div class="viewer-hero-title-row">
            <transition name="status-switch" mode="out-in">
              <span v-if="!done" key="running" class="status-orb status-orb--running" aria-label="运行中">
                <span class="status-orb-core"></span>
                <span class="status-orb-ripple"></span>
              </span>
              <span v-else-if="error" key="error" class="status-orb status-orb--error" aria-label="错误">
                <el-icon :size="12"><Warning /></el-icon>
              </span>
              <span v-else-if="emptyMessage" key="empty" class="status-orb status-orb--empty" aria-label="无日志">
                <el-icon :size="12"><InfoFilled /></el-icon>
              </span>
              <span v-else key="done" class="status-orb status-orb--done" aria-label="已完成">
                <el-icon :size="12"><Check /></el-icon>
              </span>
            </transition>
            <h2 class="viewer-hero-title" :title="taskName">{{ taskName || '任务日志' }}</h2>
            <span v-if="taskId" class="viewer-hero-id">#{{ taskId }}</span>
            <span
              class="viewer-hero-status"
              :class="{
                'viewer-hero-status--running': !done && !error && !emptyMessage,
                'viewer-hero-status--done': done && !error && !emptyMessage,
                'viewer-hero-status--empty': !!emptyMessage && !error,
                'viewer-hero-status--error': !!error
              }"
            >
              {{ error ? '异常' : emptyMessage ? '无日志' : done ? '已完成' : '运行中' }}
            </span>
          </div>
          <div class="viewer-hero-meta">
            <span class="viewer-hero-meta-item">{{ lineCount }} 行</span>
            <span v-if="byteLabel" class="viewer-hero-meta-item">{{ byteLabel }}</span>
            <span class="viewer-hero-meta-item">
              {{ autoScroll ? '自动跟随底部' : '已暂停自动滚动' }}
            </span>
          </div>
        </div>

        <div class="viewer-hero-actions">
          <el-tooltip content="切换字号" placement="bottom">
            <button class="tool-btn" @click="cycleFontSize" aria-label="切换字号">
              <el-icon :size="15"><Operation /></el-icon>
              <span class="tool-btn-label">{{ fontSize.toUpperCase() }}</span>
            </button>
          </el-tooltip>
          <el-tooltip :content="wrap ? '关闭自动换行' : '开启自动换行'" placement="bottom">
            <button
              class="tool-btn"
              :class="{ 'tool-btn--active': wrap }"
              @click="wrap = !wrap"
              aria-label="切换换行"
            >
              <el-icon :size="15"><Switch /></el-icon>
              <span class="tool-btn-label">Wrap</span>
            </button>
          </el-tooltip>
          <el-tooltip content="复制全部" placement="bottom">
            <button class="tool-btn" :disabled="!hasLogs" @click="handleCopy" aria-label="复制">
              <el-icon :size="15"><DocumentCopy /></el-icon>
            </button>
          </el-tooltip>
          <el-tooltip content="下载日志" placement="bottom">
            <button class="tool-btn" :disabled="!hasLogs" @click="handleDownload" aria-label="下载">
              <el-icon :size="15"><Download /></el-icon>
            </button>
          </el-tooltip>
          <div class="auto-scroll-toggle">
            <el-switch v-model="autoScroll" size="small" inline-prompt active-text="跟随" inactive-text="暂停" />
          </div>
          <button class="tool-btn tool-btn--close" @click="handleClose" aria-label="关闭">
            <el-icon :size="16"><Close /></el-icon>
          </button>
        </div>
      </div>
    </template>

    <div class="viewer-body" :class="fontSizeClass">
      <div ref="logContainerRef" class="viewer-log dd-log-surface" v-loading="loading">
        <div v-if="error" class="viewer-message viewer-message--error">
          <el-icon :size="22"><Warning /></el-icon>
          <span>{{ error }}</span>
        </div>
        <div v-else-if="emptyMessage && !hasLogs" class="viewer-message viewer-message--empty">
          <el-icon :size="22"><InfoFilled /></el-icon>
          <span>{{ emptyMessage }}</span>
          <span class="viewer-message-hint">任务执行一次后就会出现日志</span>
        </div>
        <div v-else-if="!hasLogs && !loading" class="viewer-message">
          <el-icon :size="22"><Loading /></el-icon>
          <span>等待日志输出...</span>
        </div>
        <pre v-else class="viewer-log-content" :class="{ 'viewer-log-content--nowrap': !wrap }" v-html="renderedLogHtml"></pre>
      </div>

      <div class="viewer-statusbar">
        <div class="viewer-statusbar-group">
          <span class="viewer-statusbar-item">{{ lineCount }} 行</span>
          <span v-if="byteLabel" class="viewer-statusbar-item">{{ byteLabel }}</span>
          <span class="viewer-statusbar-item">Wrap {{ wrap ? 'ON' : 'OFF' }}</span>
        </div>
        <div class="viewer-statusbar-group">
          <span v-if="!done && !error && !emptyMessage" class="viewer-statusbar-item viewer-statusbar-item--live">实时采集中</span>
          <span v-else-if="error" class="viewer-statusbar-item viewer-statusbar-item--error">{{ error }}</span>
          <span v-else-if="emptyMessage" class="viewer-statusbar-item viewer-statusbar-item--empty">暂无日志</span>
          <span v-else class="viewer-statusbar-item">UTF-8</span>
        </div>
      </div>
    </div>
  </el-dialog>
</template>

<style scoped lang="scss">

/* =============== Hero =============== */
.viewer-hero {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  padding: 14px 18px;
  background: linear-gradient(180deg,
    color-mix(in srgb, #6366f1 6%, transparent) 0%,
    transparent 100%);
  flex-wrap: wrap;
}

.viewer-hero-main {
  display: flex;
  flex-direction: column;
  gap: 6px;
  min-width: 0;
  flex: 1;
}

.viewer-hero-title-row {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
  min-width: 0;
}

.viewer-hero-title {
  font-size: 16px;
  font-weight: 700;
  color: var(--el-text-color-primary);
  margin: 0;
  letter-spacing: 0.2px;
  font-family: var(--dd-font-ui);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  max-width: 360px;
}

.viewer-hero-id {
  font-family: var(--dd-font-mono);
  font-size: 11.5px;
  color: var(--el-text-color-placeholder);
  letter-spacing: 0.3px;
}

.viewer-hero-status {
  display: inline-flex;
  align-items: center;
  height: 20px;
  padding: 0 8px;
  font-size: 10.5px;
  font-weight: 700;
  letter-spacing: 0.5px;
  font-family: var(--dd-font-mono);
  border-radius: 999px;

  &--running {
    background: color-mix(in srgb, var(--el-color-warning) 14%, transparent);
    color: var(--el-color-warning);
  }

  &--done {
    background: color-mix(in srgb, #22c55e 14%, transparent);
    color: color-mix(in srgb, #22c55e 80%, var(--el-text-color-primary));
  }

  &--error {
    background: color-mix(in srgb, var(--el-color-danger) 14%, transparent);
    color: var(--el-color-danger);
  }

  &--empty {
    background: color-mix(in srgb, var(--el-color-info) 18%, transparent);
    color: var(--el-color-info);
  }
}

.viewer-hero-meta {
  display: flex;
  gap: 14px;
  flex-wrap: wrap;
  font-size: 12px;
  color: var(--el-text-color-secondary);
}

.viewer-hero-meta-item {
  font-family: var(--dd-font-ui);
}

/* Status orb */
.status-orb {
  position: relative;
  width: 22px;
  height: 22px;
  border-radius: 50%;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
}

.status-orb--running {
  background: color-mix(in srgb, var(--el-color-warning) 14%, transparent);
}

.status-orb--done {
  background: color-mix(in srgb, #22c55e 14%, transparent);
  color: color-mix(in srgb, #22c55e 80%, var(--el-text-color-primary));
}

.status-orb--error {
  background: color-mix(in srgb, var(--el-color-danger) 14%, transparent);
  color: var(--el-color-danger);
}

.status-orb--empty {
  background: color-mix(in srgb, var(--el-color-info) 18%, transparent);
  color: var(--el-color-info);
}

.status-orb-core {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: var(--el-color-warning);
  box-shadow: 0 0 0 3px color-mix(in srgb, var(--el-color-warning) 22%, transparent);
  animation: orb-core 1.4s ease-in-out infinite;
}

.status-orb-ripple {
  position: absolute;
  inset: 0;
  border-radius: 50%;
  background: color-mix(in srgb, var(--el-color-warning) 40%, transparent);
  animation: orb-ripple 1.8s ease-out infinite;
}

.status-switch-enter-active,
.status-switch-leave-active {
  transition: transform 0.2s ease, opacity 0.2s ease;
}

.status-switch-enter-from,
.status-switch-leave-to {
  opacity: 0;
  transform: scale(0.85);
}

/* Hero actions */
.viewer-hero-actions {
  display: flex;
  align-items: center;
  gap: 6px;
  flex-wrap: wrap;
}

.tool-btn {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  height: 30px;
  padding: 0 10px;
  border: 1px solid var(--viewer-border-soft, var(--el-border-color-light));
  background: var(--el-bg-color);
  color: var(--el-text-color-regular);
  border-radius: 8px;
  font-size: 12px;
  font-family: var(--dd-font-mono);
  cursor: pointer;
  transition: color 0.15s, background 0.15s, border-color 0.15s;

  &:hover:not(:disabled):not(.tool-btn--close) {
    color: var(--el-color-primary);
    border-color: color-mix(in srgb, var(--el-color-primary) 40%, var(--el-border-color-light));
    background: color-mix(in srgb, var(--el-color-primary) 6%, transparent);
  }

  &:disabled {
    opacity: 0.4;
    cursor: not-allowed;
  }

  &--active {
    background: color-mix(in srgb, var(--el-color-primary) 10%, transparent);
    color: var(--el-color-primary);
    border-color: color-mix(in srgb, var(--el-color-primary) 40%, transparent);
  }

  &--close {
    width: 34px;
    height: 34px;
    padding: 0;
    color: var(--el-text-color-secondary);
    border-color: transparent;
    border-radius: 10px;
    margin-left: 6px;
    position: relative;
    overflow: hidden;
    transition: color 0.25s, transform 0.25s cubic-bezier(0.34, 1.56, 0.64, 1), box-shadow 0.25s;

    .el-icon {
      position: relative;
      z-index: 1;
      transition: transform 0.35s cubic-bezier(0.34, 1.56, 0.64, 1);
    }

    &::before {
      content: '';
      position: absolute;
      inset: 0;
      border-radius: inherit;
      background: linear-gradient(135deg, #ef4444, #dc2626);
      opacity: 0;
      transform: scale(0.55);
      transition: opacity 0.2s ease, transform 0.3s cubic-bezier(0.34, 1.56, 0.64, 1);
    }

    &:hover:not(:disabled) {
      color: #fff;
      transform: scale(1.06);
      border-color: transparent;
      background: transparent;
      box-shadow: 0 8px 20px -8px rgba(239, 68, 68, 0.55);

      &::before {
        opacity: 1;
        transform: scale(1);
      }

      .el-icon {
        transform: rotate(90deg);
      }
    }

    &:active {
      transform: scale(0.94);
    }

    &:focus-visible {
      outline: 2px solid color-mix(in srgb, #ef4444 60%, transparent);
      outline-offset: 2px;
    }
  }
}

@media (prefers-reduced-motion: reduce) {
  .tool-btn--close {
    transition: none;

    .el-icon,
    &::before {
      transition: none;
    }

    &:hover .el-icon {
      transform: none;
    }
  }
}

.tool-btn-label {
  font-weight: 600;
  letter-spacing: 0.4px;
}

.auto-scroll-toggle {
  margin-left: 4px;
}

/* =============== Body =============== */
.viewer-body {
  display: flex;
  flex-direction: column;
  flex: 1;
  min-height: 0;

  &.log-font-sm {
    --viewer-log-font-size: 12px;
  }
  &.log-font-md {
    --viewer-log-font-size: 13.5px;
  }
  &.log-font-lg {
    --viewer-log-font-size: 15px;
  }
}

.viewer-log {
  flex: 1;
  min-height: 0;
  overflow: auto;
  padding: 18px 22px;
  font-family: var(--dd-font-mono);
  font-size: var(--viewer-log-font-size, 13.5px);
  line-height: 1.65;
  background: var(--dd-log-bg-color, #0f172a);
  color: var(--dd-log-text-color, #e2e8f0);
}

.viewer-log-content {
  margin: 0;
  white-space: pre-wrap;
  word-break: break-all;

  &--nowrap {
    white-space: pre;
    word-break: normal;
  }
}

.viewer-message {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  height: 100%;
  gap: 10px;
  color: color-mix(in srgb, var(--dd-log-text-color, #e2e8f0) 55%, transparent);
  font-size: 13px;

  &--error {
    color: color-mix(in srgb, var(--el-color-warning) 90%, transparent);
  }

  &--empty {
    color: color-mix(in srgb, var(--dd-log-text-color, #e2e8f0) 70%, transparent);
  }
}

.viewer-message-hint {
  font-size: 11.5px;
  color: color-mix(in srgb, var(--dd-log-text-color, #e2e8f0) 40%, transparent);
  letter-spacing: 0.2px;
}

/* =============== Status bar =============== */
.viewer-statusbar {
  display: flex;
  justify-content: space-between;
  padding: 6px 22px;
  font-family: var(--dd-font-mono);
  font-size: 11px;
  color: var(--el-text-color-placeholder);
  border-top: 1px solid var(--viewer-border-soft, var(--el-border-color-light));
  background: color-mix(in srgb, var(--el-fill-color-lighter) 60%, transparent);
  flex-shrink: 0;
}

.viewer-statusbar-group {
  display: inline-flex;
  gap: 14px;
}

.viewer-statusbar-item {
  letter-spacing: 0.4px;

  &--live {
    color: var(--el-color-warning);
    font-weight: 600;

    &::before {
      content: '● ';
      animation: pulse 1.6s ease-in-out infinite;
    }
  }

  &--error {
    color: var(--el-color-danger);
  }

  &--empty {
    color: var(--el-color-info);
  }
}

/* =============== Animations =============== */
@keyframes orb-core {
  0%, 100% { transform: scale(0.9); }
  50% { transform: scale(1.1); }
}

@keyframes orb-ripple {
  0% { transform: scale(0.7); opacity: 0.7; }
  100% { transform: scale(1.45); opacity: 0; }
}

@keyframes pulse {
  0%, 100% { opacity: 1; }
  50% { opacity: 0.35; }
}

@media (prefers-reduced-motion: reduce) {
  .status-orb-core,
  .status-orb-ripple,
  .viewer-statusbar-item--live::before { animation: none; }
}

/* =============== Mobile =============== */
@media (max-width: 768px) {
  .viewer-hero {
    padding: 10px 12px;
    gap: 10px;
  }

  .viewer-hero-title {
    font-size: 14.5px;
    max-width: 60vw;
  }

  .viewer-hero-actions {
    width: 100%;
    justify-content: flex-end;
    gap: 4px;
  }

  .tool-btn-label {
    display: none;
  }

  .viewer-body {
    &.log-font-md {
      --viewer-log-font-size: 12.5px;
    }
  }

  .viewer-log {
    padding: 14px;
  }

  .viewer-statusbar {
    padding: 5px 14px;
    font-size: 10px;
  }
}
</style>

<!--
  独立的非 scoped style：专门处理 teleport 到 body 的 el-dialog 根元素。
  原因：LogViewer.vue 的 template root 就是 el-dialog 自身，而 el-dialog 会把 DOM teleport 到 body，
  在这种组合下 Vue scoped 的 data-v 属性往 teleport 元素传递不可靠，
  :deep(.log-viewer-dialog) 编译出的 [data-v-xxx] .log-viewer-dialog 选择器在实际 DOM 里没办法命中。
  改用非 scoped 块，编译后是纯 .log-viewer-dialog 选择器，不依赖 scope，一定生效。
  类名唯一 log-viewer-dialog不会污染其他组件。
-->
<style lang="scss">
.log-viewer-dialog {
  --viewer-border-soft: color-mix(in srgb, var(--el-border-color-light) 85%, transparent);

  border-radius: 16px;
  overflow: hidden;
  display: flex;
  flex-direction: column;
  // 固定窗口尺寸：不随日志内容多少变化，内部 .viewer-log 用 flex:1 + overflow:auto 在窗口内滚动。
  // 只写 max-height 会让日志少时 dialog 被塌陷成一条，所以 height 和 max-height 同时写，既固定又兜底。
  height: 90vh;
  max-height: 90vh;
  // align-center 模式下 el-overlay-dialog 是 flex 容器，用 margin: auto 让 dialog 垂直+水平居中；
  // 如果写成 margin: 0 auto 上下 margin 会变成 0，dialog 会贴到容器底部。
  margin: auto;

  // fullscreen 模式（由 :fullscreen="dialogFullscreen" prop 激活，对应 mobile），
  // 由 Element Plus 默认样式 .el-dialog.is-fullscreen { height:100%; ... } 接管，
  // 但我们的 height:90vh 优先级一样，需要显式让 fullscreen 时恢复全屏。
  &.is-fullscreen {
    height: 100%;
    max-height: 100%;
    border-radius: 0;
    margin: 0;
  }

  .el-dialog__header {
    padding: 0;
    margin: 0;
    border-bottom: 1px solid var(--viewer-border-soft);
    flex-shrink: 0;
  }

  .el-dialog__body {
    padding: 0;
    flex: 1;
    min-height: 0;
    display: flex;
    flex-direction: column;
    overflow: hidden;
  }
}
</style>
