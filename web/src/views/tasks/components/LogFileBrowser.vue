<script setup lang="ts">
import { ref, watch } from 'vue'
import { taskApi } from '@/api/task'
import { ElMessage, ElMessageBox } from 'element-plus'

const props = defineProps<{
  visible: boolean
  taskId: number | null
  taskName: string
}>()

const emit = defineEmits<{
  'update:visible': [value: boolean]
}>()

const logFiles = ref<any[]>([])
const loading = ref(false)
const selectedFile = ref<string | null>(null)
const fileContent = ref('')
const contentLoading = ref(false)

watch(() => props.visible, (visible) => {
  if (visible && props.taskId) {
    loadLogFiles()
  } else {
    logFiles.value = []
    selectedFile.value = null
    fileContent.value = ''
  }
})

async function loadLogFiles() {
  loading.value = true
  try {
    const res = await taskApi.logFiles(props.taskId!)
    logFiles.value = res || []
  } catch {
    ElMessage.error('加载日志文件列表失败')
  } finally {
    loading.value = false
  }
}

async function viewFile(filename: string) {
  selectedFile.value = filename
  contentLoading.value = true
  try {
    const res = await taskApi.logFileContent(props.taskId!, filename)
    fileContent.value = res.content || ''
  } catch {
    ElMessage.error('加载文件内容失败')
    fileContent.value = ''
  } finally {
    contentLoading.value = false
  }
}

async function deleteFile(filename: string) {
  try {
    await ElMessageBox.confirm(`确定删除日志文件 "${filename}"？`, '确认删除', { type: 'warning' })
    await taskApi.deleteLogFile(props.taskId!, filename)
    ElMessage.success('删除成功')
    loadLogFiles()
    if (selectedFile.value === filename) {
      selectedFile.value = null
      fileContent.value = ''
    }
  } catch {}
}

function formatBytes(bytes: number) {
  if (!bytes) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB']
  let i = 0
  let val = bytes
  while (val >= 1024 && i < units.length - 1) { val /= 1024; i++ }
  return val.toFixed(1) + ' ' + units[i]
}

function handleClose() {
  emit('update:visible', false)
}
</script>

<template>
  <el-dialog
    :model-value="visible"
    :title="`日志文件 - ${taskName}`"
    width="1000px"
    @close="handleClose"
  >
    <div class="log-files-browser">
      <div class="file-list" v-loading="loading">
        <div v-if="logFiles.length === 0" class="empty-hint">暂无日志文件</div>
        <div
          v-for="file in logFiles"
          :key="file.name"
          class="file-item"
          :class="{ active: selectedFile === file.name }"
          @click="viewFile(file.name)"
        >
          <div class="file-info">
            <el-icon><Document /></el-icon>
            <span class="file-name">{{ file.name }}</span>
          </div>
          <div class="file-actions">
            <span class="file-size">{{ formatBytes(file.size) }}</span>
            <el-button
              type="danger"
              text
              size="small"
              @click.stop="deleteFile(file.name)"
            >
              <el-icon><Delete /></el-icon>
            </el-button>
          </div>
        </div>
      </div>

      <div class="file-content" v-loading="contentLoading">
        <div v-if="!selectedFile" class="content-placeholder">
          <el-icon :size="48" color="var(--el-text-color-placeholder)"><Document /></el-icon>
          <span>选择文件查看内容</span>
        </div>
        <pre v-else class="content-text">{{ fileContent || '(空文件)' }}</pre>
      </div>
    </div>
  </el-dialog>
</template>

<style scoped lang="scss">
.log-files-browser {
  display: flex;
  gap: 16px;
  height: 600px;
}

.file-list {
  width: 280px;
  border: 1px solid var(--el-border-color);
  border-radius: 4px;
  overflow-y: auto;
  flex-shrink: 0;
}

.file-item {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 10px 12px;
  cursor: pointer;
  border-bottom: 1px solid var(--el-border-color-lighter);
  transition: background 0.2s;

  &:hover {
    background: var(--el-fill-color-light);
  }

  &.active {
    background: var(--el-color-primary-light-9);
    border-left: 3px solid var(--el-color-primary);
  }

  &:last-child {
    border-bottom: none;
  }
}

.file-info {
  display: flex;
  align-items: center;
  gap: 8px;
  flex: 1;
  min-width: 0;
}

.file-name {
  font-size: 13px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.file-actions {
  display: flex;
  align-items: center;
  gap: 8px;
}

.file-size {
  font-size: 12px;
  color: var(--el-text-color-secondary);
}

.file-content {
  flex: 1;
  border: 1px solid var(--el-border-color);
  border-radius: 4px;
  overflow: hidden;
  display: flex;
  flex-direction: column;
}

.content-placeholder {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  height: 100%;
  color: var(--el-text-color-placeholder);
  gap: 12px;
}

.content-text {
  flex: 1;
  margin: 0;
  padding: 12px;
  overflow-y: auto;
  background: #1e1e1e;
  color: #d4d4d4;
  font-family: var(--dd-font-mono);
  font-size: 13px;
  line-height: 1.6;
  white-space: pre-wrap;
  word-break: break-all;
}

.empty-hint {
  padding: 40px 20px;
  text-align: center;
  color: var(--el-text-color-placeholder);
}
</style>
