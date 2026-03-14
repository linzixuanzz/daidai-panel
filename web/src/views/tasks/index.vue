<script setup lang="ts">
import { ref, onMounted, onBeforeUnmount, onActivated, computed } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { taskApi } from '@/api/task'
import { ElMessage, ElMessageBox } from 'element-plus'
import TaskForm from './components/TaskForm.vue'
import LogViewer from './components/LogViewer.vue'
import TaskDetail from './components/TaskDetail.vue'
import LogFileBrowser from './components/LogFileBrowser.vue'

const route = useRoute()
const router = useRouter()
let statusTimer: ReturnType<typeof setInterval> | null = null

const tasks = ref<any[]>([])
const total = ref(0)
const page = ref(1)
const pageSize = ref(20)
const keyword = ref('')
const statusFilter = ref<string>('')
const loading = ref(false)
const selectedIds = ref<number[]>([])
const formVisible = ref(false)
const editingTask = ref<any>(null)
const prefillData = ref<any>(null)
const logViewerVisible = ref(false)
const logViewerTaskId = ref<number | null>(null)
const logViewerTaskName = ref('')
const detailVisible = ref(false)
const detailTask = ref<any>(null)
const logFilesVisible = ref(false)
const logFilesTaskId = ref<number | null>(null)
const logFilesTaskName = ref('')

const hasRunningTasks = computed(() => tasks.value.some(t => t.status === 2))

function startStatusPolling() {
  stopStatusPolling()
  statusTimer = setInterval(async () => {
    if (!hasRunningTasks.value) {
      stopStatusPolling()
      return
    }
    try {
      const params: any = { page: page.value, page_size: pageSize.value }
      if (keyword.value) params.keyword = keyword.value
      if (statusFilter.value !== '') params.status = statusFilter.value
      const res = await taskApi.list(params)
      tasks.value = res.data
      total.value = res.total
    } catch {}
  }, 3000)
}

function stopStatusPolling() {
  if (statusTimer) {
    clearInterval(statusTimer)
    statusTimer = null
  }
}

async function loadTasks() {
  loading.value = true
  try {
    const params: any = { page: page.value, page_size: pageSize.value }
    if (keyword.value) params.keyword = keyword.value
    if (statusFilter.value !== '') params.status = statusFilter.value
    const res = await taskApi.list(params)
    tasks.value = res.data
    total.value = res.total
    if (hasRunningTasks.value && !statusTimer) {
      startStatusPolling()
    }
  } catch {
    ElMessage.error('加载任务列表失败')
  } finally {
    loading.value = false
  }
}

function checkAutoCreate() {
  if (route.query.autoCreate === '1') {
    const name = route.query.name as string || ''
    const command = route.query.command as string || ''
    if (name && command) {
      editingTask.value = null
      prefillData.value = { name, command, cron_expression: '0 0 * * *' }
      formVisible.value = true
      router.replace({ path: '/tasks' })
    }
  }
}

onMounted(() => {
  loadTasks()
  checkAutoCreate()
})

onActivated(() => {
  loadTasks()
  checkAutoCreate()
})

onBeforeUnmount(() => {
  stopStatusPolling()
})

function handleSearch() {
  page.value = 1
  loadTasks()
}

function getStatusType(status: number) {
  if (status === 0) return 'info'
  if (status === 2) return 'warning'
  return 'success'
}

function getStatusText(status: number) {
  if (status === 0) return '已禁用'
  if (status === 0.5) return '排队中'
  if (status === 2) return '运行中'
  return '已启用'
}

function getRunStatusType(status: number | null) {
  if (status === null) return 'info'
  return status === 0 ? 'success' : 'danger'
}

function getRunStatusText(status: number | null) {
  if (status === null) return '未运行'
  return status === 0 ? '成功' : '失败'
}

function openCreate() {
  editingTask.value = null
  prefillData.value = null
  formVisible.value = true
}

function openEdit(task: any) {
  editingTask.value = task
  formVisible.value = true
}

function openDetail(task: any) {
  detailTask.value = task
  detailVisible.value = true
}

function openLogViewer(task: any) {
  logViewerTaskId.value = task.id
  logViewerTaskName.value = task.name
  logViewerVisible.value = true
}

function openLogFiles(task: any) {
  logFilesTaskId.value = task.id
  logFilesTaskName.value = task.name
  logFilesVisible.value = true
}

async function handleFormSubmit(data: any) {
  try {
    if (editingTask.value) {
      await taskApi.update(editingTask.value.id, data)
      ElMessage.success('任务更新成功')
    } else {
      await taskApi.create(data)
      ElMessage.success('任务创建成功')
    }
    formVisible.value = false
    loadTasks()
  } catch (err: any) {
    ElMessage.error(err?.response?.data?.error || '操作失败')
  }
}

async function handleRun(task: any) {
  try {
    await ElMessageBox.confirm(`确认运行定时任务「${task.name}」吗？`, '运行确认', { type: 'info' })
    await taskApi.run(task.id)
    ElMessage.success('任务已启动')
    task.status = 2
    openLogViewer(task)
    loadTasks()
    startStatusPolling()
  } catch (err: any) {
    ElMessage.error(err?.response?.data?.error || '启动失败')
  }
}

async function handleStop(task: any) {
  try {
    await taskApi.stop(task.id)
    ElMessage.success('任务已停止')
    task.status = 1
    loadTasks()
  } catch (err: any) {
    ElMessage.error(err?.response?.data?.error || '停止失败')
  }
}

async function handleToggle(task: any) {
  try {
    if (task.status === 0) {
      await taskApi.enable(task.id)
      ElMessage.success('已启用')
    } else {
      await taskApi.disable(task.id)
      ElMessage.success('已禁用')
    }
    loadTasks()
  } catch (err: any) {
    ElMessage.error(err?.response?.data?.error || '操作失败')
  }
}

async function handleDelete(task: any) {
  await ElMessageBox.confirm(`确定删除任务 "${task.name}"？`, '确认删除', { type: 'warning' })
  try {
    await taskApi.delete(task.id)
    ElMessage.success('任务已删除')
    loadTasks()
  } catch (err: any) {
    ElMessage.error(err?.response?.data?.error || '删除失败')
  }
}

async function handleCopy(task: any) {
  try {
    await taskApi.copy(task.id)
    ElMessage.success('任务已复制')
    loadTasks()
  } catch (err: any) {
    ElMessage.error(err?.response?.data?.error || '复制失败')
  }
}

async function handlePin(task: any) {
  try {
    if (task.is_pinned) {
      await taskApi.unpin(task.id)
    } else {
      await taskApi.pin(task.id)
    }
    loadTasks()
  } catch { /* ignore */ }
}

function handleSelectionChange(rows: any[]) {
  selectedIds.value = rows.map(r => r.id)
}

async function handleBatchAction(action: string) {
  if (selectedIds.value.length === 0) {
    ElMessage.warning('请先选择任务')
    return
  }
  if (action === 'delete') {
    await ElMessageBox.confirm(`确定删除选中的 ${selectedIds.value.length} 个任务？`, '批量删除', { type: 'warning' })
  }
  try {
    await taskApi.batch(selectedIds.value, action)
    ElMessage.success('操作成功')
    loadTasks()
  } catch (err: any) {
    ElMessage.error(err?.response?.data?.error || '操作失败')
  }
}

async function handleBatchPin() {
  if (selectedIds.value.length === 0) {
    ElMessage.warning('请先选择任务')
    return
  }
  try {
    for (const id of selectedIds.value) {
      await taskApi.pin(id)
    }
    ElMessage.success('批量置顶成功')
    loadTasks()
  } catch (err: any) {
    ElMessage.error(err?.response?.data?.error || '操作失败')
  }
}

async function handleCleanLogs() {
  try {
    const { value } = await ElMessageBox.prompt('清理多少天前的日志？', '日志清理', {
      confirmButtonText: '确定',
      cancelButtonText: '取消',
      inputPattern: /^\d+$/,
      inputErrorMessage: '请输入有效的天数',
      inputValue: '30',
    })
    await taskApi.cleanLogs(Number(value))
    ElMessage.success('日志清理成功')
  } catch {}
}

async function handleExport() {
  try {
    const res = await taskApi.export()
    const blob = new Blob([JSON.stringify(res.data, null, 2)], { type: 'application/json' })
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = `tasks_export_${new Date().toISOString().slice(0, 10)}.json`
    a.click()
    URL.revokeObjectURL(url)
  } catch {
    ElMessage.error('导出失败')
  }
}

const importFileRef = ref<HTMLInputElement>()

function triggerImport() {
  importFileRef.value?.click()
}

async function handleImport(event: Event) {
  const file = (event.target as HTMLInputElement).files?.[0]
  if (!file) return
  try {
    const text = await file.text()
    const data = JSON.parse(text)
    const tasksData = Array.isArray(data) ? data : data.data || data.tasks
    const res = await taskApi.import(tasksData)
    ElMessage.success(res.message)
    if (res.errors?.length) {
      ElMessage.warning(`${res.errors.length} 个导入错误`)
    }
    loadTasks()
  } catch (err: any) {
    ElMessage.error(err?.response?.data?.error || '导入失败')
  }
  (event.target as HTMLInputElement).value = ''
}
</script>

<template>
  <div class="tasks-page">
    <div class="page-header">
      <h2>定时任务</h2>
      <div class="header-actions">
        <el-button type="primary" @click="openCreate">
          <el-icon><Plus /></el-icon> 新建任务
        </el-button>
        <el-dropdown trigger="click">
          <el-button><el-icon><More /></el-icon></el-button>
          <template #dropdown>
            <el-dropdown-menu>
              <el-dropdown-item @click="handleExport">导出任务</el-dropdown-item>
              <el-dropdown-item @click="triggerImport">导入任务</el-dropdown-item>
              <el-dropdown-item divided @click="handleCleanLogs">清理日志</el-dropdown-item>
            </el-dropdown-menu>
          </template>
        </el-dropdown>
        <input ref="importFileRef" type="file" accept=".json" style="display:none" @change="handleImport" />
      </div>
    </div>

    <div class="filter-bar">
      <el-input v-model="keyword" placeholder="搜索任务名称/命令" clearable style="width: 260px" @keyup.enter="handleSearch" @clear="handleSearch">
        <template #prefix><el-icon><Search /></el-icon></template>
      </el-input>
      <el-select v-model="statusFilter" placeholder="状态筛选" clearable style="width: 130px" @change="handleSearch">
        <el-option label="已启用" value="1" />
        <el-option label="已禁用" value="0" />
        <el-option label="运行中" value="2" />
      </el-select>

      <div v-if="selectedIds.length > 0" class="batch-actions">
        <el-button size="small" @click="handleBatchAction('enable')">批量启用</el-button>
        <el-button size="small" @click="handleBatchAction('disable')">批量禁用</el-button>
        <el-button size="small" @click="handleBatchAction('run')">批量运行</el-button>
        <el-button size="small" @click="handleBatchPin">批量置顶</el-button>
        <el-button size="small" type="danger" @click="handleBatchAction('delete')">批量删除</el-button>
      </div>
    </div>

    <el-table
      v-loading="loading"
      :data="tasks"
      @selection-change="handleSelectionChange"
      stripe
      style="width: 100%"
    >
      <el-table-column type="selection" width="40" />
      <el-table-column label="名称" min-width="180">
        <template #default="{ row }">
          <div class="task-name">
            <el-icon v-if="row.is_pinned" class="pin-icon" @click="handlePin(row)"><Star /></el-icon>
            <span>{{ row.name }}</span>
            <el-tag v-for="label in row.labels" :key="label" size="small" effect="plain" class="task-label">{{ label }}</el-tag>
          </div>
        </template>
      </el-table-column>
      <el-table-column label="命令" min-width="160" show-overflow-tooltip>
        <template #default="{ row }">
          <code class="command-text">{{ row.command }}</code>
        </template>
      </el-table-column>
      <el-table-column label="定时规则" width="130">
        <template #default="{ row }">
          <el-tooltip :content="row.cron_expression">
            <code>{{ row.cron_expression }}</code>
          </el-tooltip>
        </template>
      </el-table-column>
      <el-table-column label="状态" width="90" align="center">
        <template #default="{ row }">
          <el-tag :type="getStatusType(row.status)" size="small">
            {{ getStatusText(row.status) }}
          </el-tag>
        </template>
      </el-table-column>
      <el-table-column label="上次结果" width="90" align="center">
        <template #default="{ row }">
          <el-tag :type="getRunStatusType(row.last_run_status)" size="small">
            {{ getRunStatusText(row.last_run_status) }}
          </el-tag>
        </template>
      </el-table-column>
      <el-table-column label="耗时" width="80" align="center">
        <template #default="{ row }">
          <span v-if="row.last_running_time != null">{{ row.last_running_time.toFixed(1) }}s</span>
          <span v-else class="text-muted">-</span>
        </template>
      </el-table-column>
      <el-table-column label="操作" width="260" fixed="right">
        <template #default="{ row }">
          <el-button-group size="small">
            <el-button v-if="row.status !== 2" type="primary" text @click="handleRun(row)">运行</el-button>
            <el-button v-else type="warning" text @click="handleStop(row)">停止</el-button>
            <el-button text @click="openLogViewer(row)">日志</el-button>
            <el-button text @click="openEdit(row)">编辑</el-button>
            <el-dropdown trigger="click">
              <el-button text><el-icon><More /></el-icon></el-button>
              <template #dropdown>
                <el-dropdown-menu>
                  <el-dropdown-item @click="openDetail(row)">详情</el-dropdown-item>
                  <el-dropdown-item @click="openLogFiles(row)">日志文件</el-dropdown-item>
                  <el-dropdown-item divided @click="handleToggle(row)">
                    {{ row.status === 0 ? '启用' : '禁用' }}
                  </el-dropdown-item>
                  <el-dropdown-item @click="handleCopy(row)">复制</el-dropdown-item>
                  <el-dropdown-item @click="handlePin(row)">{{ row.is_pinned ? '取消置顶' : '置顶' }}</el-dropdown-item>
                  <el-dropdown-item divided @click="handleDelete(row)">
                    <span style="color: var(--el-color-danger)">删除</span>
                  </el-dropdown-item>
                </el-dropdown-menu>
              </template>
            </el-dropdown>
          </el-button-group>
        </template>
      </el-table-column>
    </el-table>

    <div class="pagination-bar">
      <el-pagination
        v-model:current-page="page"
        v-model:page-size="pageSize"
        :total="total"
        :page-sizes="[10, 20, 50, 100]"
        layout="total, sizes, prev, pager, next"
        @current-change="loadTasks"
        @size-change="loadTasks"
      />
    </div>

    <TaskForm
      v-model:visible="formVisible"
      :task="editingTask"
      :prefill="prefillData"
      @submit="handleFormSubmit"
    />

    <LogViewer
      v-model:visible="logViewerVisible"
      :task-id="logViewerTaskId"
      :task-name="logViewerTaskName"
    />

    <TaskDetail
      v-model:visible="detailVisible"
      :task="detailTask"
    />

    <LogFileBrowser
      v-model:visible="logFilesVisible"
      :task-id="logFilesTaskId"
      :task-name="logFilesTaskName"
    />
  </div>
</template>

<style scoped lang="scss">
.tasks-page {
  padding: 0;
  font-size: 14px;
}

.page-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 20px;

  h2 { margin: 0; font-size: 22px; font-weight: 600; }

  .header-actions {
    display: flex;
    gap: 10px;
  }
}

.filter-bar {
  display: flex;
  gap: 14px;
  margin-bottom: 20px;
  align-items: center;

  .batch-actions {
    display: flex;
    gap: 8px;
    margin-left: auto;
  }
}

.task-name {
  display: flex;
  align-items: center;
  gap: 8px;

  .pin-icon {
    color: var(--el-color-warning);
    cursor: pointer;
    font-size: 16px;
  }

  .task-label {
    font-size: 12px;
  }
}

.command-text {
  font-family: var(--dd-font-mono);
  font-size: 13px;
  color: var(--el-text-color-secondary);
}

.text-muted {
  color: var(--el-text-color-placeholder);
}

.pagination-bar {
  margin-top: 20px;
  display: flex;
  justify-content: flex-end;
}

:deep(.el-table) {
  font-size: 14px;

  .el-table__cell {
    padding: 14px 0;
  }
}

:deep(.el-button) {
  font-size: 14px;
  padding: 8px 16px;
}

:deep(.el-button--small) {
  font-size: 13px;
  padding: 6px 12px;
}
</style>
