<script setup lang="ts">
import { ref, watch, onUnmounted, nextTick } from 'vue'
import { taskApi } from '@/api/task'
import { useAuthStore } from '@/stores/auth'

const props = defineProps<{
  visible: boolean
  taskId: number | null
  taskName: string
}>()

const emit = defineEmits<{
  'update:visible': [value: boolean]
}>()

const logs = ref<string[]>([])
const done = ref(false)
const error = ref<string | null>(null)
const loading = ref(false)
const logContainerRef = ref<HTMLElement>()
const autoScroll = ref(true)
let eventSource: EventSource | null = null

watch(() => props.visible, (visible) => {
  if (visible && props.taskId) {
    startStream()
  } else {
    cleanup()
  }
})

async function startStream() {
  logs.value = []
  done.value = false
  error.value = null
  loading.value = true

  const authStore = useAuthStore()
  const url = `/api/v1/logs/${props.taskId}/stream?token=${authStore.accessToken}`

  cleanup()
  eventSource = new EventSource(url)

  eventSource.onopen = () => {
    loading.value = false
  }

  eventSource.onmessage = (e) => {
    loading.value = false
    if (e.data) {
      logs.value.push(e.data)
      if (autoScroll.value) {
        nextTick(() => scrollToBottom())
      }
    }
  }

  eventSource.addEventListener('done', (e: any) => {
    done.value = true
    cleanup()
    if (e.data === 'reconnect') {
      setTimeout(() => startStream(), 500)
      return
    }
    if (logs.value.length === 0) {
      fetchLatestLog()
    }
  })

  eventSource.onerror = () => {
    loading.value = false
    done.value = true
    cleanup()
    if (logs.value.length === 0) {
      fetchLatestLog()
    }
  }
}

async function fetchLatestLog() {
  try {
    const res = await taskApi.latestLog(props.taskId!) as any
    if (!res) {
      error.value = '暂无日志记录'
      return
    }
    if (res.content) {
      logs.value = res.content.split('\n').filter((line: string) => line !== '')
    } else {
      error.value = '日志已过期，文件已被清理'
    }
  } catch (err: any) {
    if (err?.response?.status === 404) {
      error.value = '暂无日志记录'
    } else {
      error.value = '获取日志失败'
    }
  }
}

function scrollToBottom() {
  if (logContainerRef.value) {
    logContainerRef.value.scrollTop = logContainerRef.value.scrollHeight
  }
}

function cleanup() {
  if (eventSource) {
    eventSource.close()
    eventSource = null
  }
}

onUnmounted(cleanup)

function handleClose() {
  emit('update:visible', false)
}
</script>

<template>
  <el-dialog
    :model-value="visible"
    :title="`任务日志 - ${taskName}`"
    width="85%"
    top="5vh"
    @close="handleClose"
  >
    <div class="log-viewer">
      <div class="log-toolbar">
        <el-checkbox v-model="autoScroll" size="small">自动滚动</el-checkbox>
        <el-tag v-if="!done" type="warning" size="small" class="running-tag">
          <span class="spinner"></span> 运行中
        </el-tag>
        <el-tag v-else type="success" size="small">已完成</el-tag>
      </div>

      <div ref="logContainerRef" class="log-container" v-loading="loading">
        <div v-if="error" class="log-error">{{ error }}</div>
        <div v-else-if="logs.length === 0 && !loading" class="log-empty">暂无日志输出</div>
        <pre v-else class="log-content">{{ logs.join('\n') }}</pre>
      </div>
    </div>
  </el-dialog>
</template>

<style scoped lang="scss">
.log-viewer {
  display: flex;
  flex-direction: column;
  height: 75vh;
  min-height: 500px;
}

.log-toolbar {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 8px 12px;
  background: var(--el-fill-color-lighter);
  border-radius: 4px;
  margin-bottom: 12px;
}

.log-container {
  flex: 1;
  overflow-y: auto;
  background: #1e1e1e;
  border-radius: 4px;
  padding: 12px;
  font-family: var(--dd-font-mono);
  font-size: 13px;
  line-height: 1.6;
}

.log-content {
  margin: 0;
  color: #d4d4d4;
  white-space: pre-wrap;
  word-break: break-all;
}

.log-error, .log-empty {
  color: #8c8c8c;
  text-align: center;
  padding: 40px 20px;
}

.running-tag {
  display: inline-flex;
  align-items: center;
  gap: 6px;
}

.spinner {
  display: inline-block;
  width: 12px;
  height: 12px;
  border: 2px solid rgba(230, 162, 60, 0.3);
  border-top-color: #e6a23c;
  border-radius: 50%;
  animation: spin 0.8s linear infinite;
}

@keyframes spin {
  0% { transform: rotate(0deg); }
  100% { transform: rotate(360deg); }
}
</style>
