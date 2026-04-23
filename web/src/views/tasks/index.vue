<script setup lang="ts">
import { ref, onMounted, onBeforeUnmount, onActivated, computed, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { taskApi } from '@/api/task'
import { ElMessage, ElMessageBox } from 'element-plus'
import TaskForm from './components/TaskForm.vue'
import LogViewer from './components/LogViewer.vue'
import TaskDetail from './components/TaskDetail.vue'
import LogFileBrowser from './components/LogFileBrowser.vue'
import ViewManager from './components/ViewManager.vue'
import { getDisplayTaskLabels } from './taskLabels'
import { splitTaskCommandDisplay } from './taskCommand'
import { usePageActivity } from '@/composables/usePageActivity'
import { useResponsive } from '@/composables/useResponsive'
import type { TaskViewFilter, TaskViewSortRule } from '@/api/taskView'

const route = useRoute()
const router = useRouter()
const { isMobile } = useResponsive()
const { isPageActive } = usePageActivity()
let statusTimer: ReturnType<typeof setInterval> | null = null

const TASK_PAGE_SIZE_STORAGE_KEY = 'dd:tasks:page_size'
const supportedTaskPageSizes = [10, 20, 50, 100]

function readStoredTaskPageSize() {
  if (typeof window === 'undefined') {
    return 20
  }

  const raw = window.localStorage.getItem(TASK_PAGE_SIZE_STORAGE_KEY)
  const parsed = Number(raw)
  return supportedTaskPageSizes.includes(parsed) ? parsed : 20
}

function persistTaskPageSize(value: number) {
  if (typeof window === 'undefined') {
    return
  }
  window.localStorage.setItem(TASK_PAGE_SIZE_STORAGE_KEY, String(value))
}

const tasks = ref<any[]>([])
const total = ref(0)
const page = ref(1)
const pageSize = ref(readStoredTaskPageSize())
const keyword = ref('')
const statusFilter = ref<string>('')
const loading = ref(false)
const selectedIds = ref<number[]>([])
const selectedIdSet = computed(() => new Set(selectedIds.value))
const notificationChannels = ref<{ id: number; name: string; type: string; enabled: boolean }[]>([])
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
const viewFilters = ref<TaskViewFilter[]>([])
const viewSortRules = ref<TaskViewSortRule[]>([])
const canPollTaskStatus = computed(() => hasRunningTasks.value && isPageActive.value && selectedIds.value.length === 0)

function handleViewChange(filters: TaskViewFilter[], sortRules: TaskViewSortRule[]) {
  viewFilters.value = filters
  viewSortRules.value = sortRules
  page.value = 1
  void loadTasks()
}

function getTaskTypeLabel(taskType: string | null | undefined) {
  if (taskType === 'manual') return '手动运行'
  if (taskType === 'startup') return '开机运行'
  return '常规定时'
}

function getCronExpressions(task: any) {
  if (Array.isArray(task?.cron_expressions) && task.cron_expressions.length > 0) {
    return task.cron_expressions
  }
  return String(task?.cron_expression || '')
    .split(/\r?\n/)
    .map((item: string) => item.trim())
    .filter(Boolean)
}

const hasRunningTasks = computed(() => tasks.value.some(t => t.status === 2))

watch(pageSize, (value) => {
  persistTaskPageSize(value)
})

watch(canPollTaskStatus, () => {
  syncStatusPolling()
})

function buildTaskListParams() {
  const params: Record<string, string | number> = {
    page: page.value,
    page_size: pageSize.value,
  }
  if (keyword.value) params.keyword = keyword.value
  if (statusFilter.value !== '') params.status = statusFilter.value
  if (viewFilters.value.length > 0) {
    params.filters = JSON.stringify(viewFilters.value)
  }
  if (viewSortRules.value.length > 0) {
    params.sort_rules = JSON.stringify(viewSortRules.value)
  }
  return params
}

function startStatusPolling() {
  stopStatusPolling()
  statusTimer = setInterval(async () => {
    if (!canPollTaskStatus.value) {
      stopStatusPolling()
      return
    }
    try {
      const res = await taskApi.list(buildTaskListParams())
      tasks.value = res.data
      total.value = res.total
      syncStatusPolling()
    } catch {}
  }, 3000)
}

function stopStatusPolling() {
  if (statusTimer) {
    clearInterval(statusTimer)
    statusTimer = null
  }
}

function syncStatusPolling() {
  if (canPollTaskStatus.value) {
    if (!statusTimer) {
      startStatusPolling()
    }
    return
  }
  stopStatusPolling()
}

async function loadTasks() {
  loading.value = true
  try {
    const res = await taskApi.list(buildTaskListParams())
    tasks.value = res.data
    total.value = res.total
    syncStatusPolling()
  } catch {
    ElMessage.error('加载任务列表失败')
  } finally {
    loading.value = false
  }
}

async function loadNotificationChannels() {
  try {
    const res = await taskApi.notificationChannels()
    notificationChannels.value = res.data || []
  } catch {
    notificationChannels.value = []
  }
}

function checkAutoCreate() {
  if (route.query.autoCreate === '1') {
    const name = route.query.name as string || ''
    const command = route.query.command as string || ''
    if (name && command) {
      editingTask.value = null
      prefillData.value = { name, command, cron_expression: '0 0 * * *', task_type: 'cron' }
      formVisible.value = true
      router.replace({ path: '/tasks' })
    }
  }
}

let mounted = false

onMounted(() => {
  mounted = true
  loadTasks()
  loadNotificationChannels()
  checkAutoCreate()
})

onActivated(() => {
  if (!mounted) {
    loadTasks()
    loadNotificationChannels()
  }
  mounted = false
  checkAutoCreate()
})

onBeforeUnmount(() => {
  stopStatusPolling()
})

function handleSearch() {
  page.value = 1
  void loadTasks()
}

function getStatusType(status: number) {
  if (status === 0) return 'info'
  if (status === 0.5) return 'warning'
  if (status === 2) return 'warning'
  return 'success'
}

function getStatusText(status: number) {
  if (status === 0) return '禁用中'
  if (status === 0.5) return '排队中'
  if (status === 2) return '运行中'
  return '空闲中'
}

function formatTime(time: string | null) {
  if (!time) return '-'
  const d = new Date(time)
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`
}

function navigateToScript(path: string) {
  router.push({ path: '/scripts', query: { file: path } })
}

function handlePageSizeChange() {
  page.value = 1
  void loadTasks()
}

function getRunStatusType(status: number | null) {
  if (status === null) return 'info'
  return status === 0 ? 'success' : 'danger'
}

function getRunStatusText(status: number | null) {
  if (status === null) return '未运行'
  return status === 0 ? '成功' : '失败'
}

function displayTaskLabels(task: any) {
  if (Array.isArray(task?.display_labels) && task.display_labels.length > 0) {
    return task.display_labels
  }
  return getDisplayTaskLabels(task?.labels || [])
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
    syncStatusPolling()
    void loadTasks()
  } catch (err: any) {
    if (err === 'cancel' || err?.toString?.() === 'cancel') return
    ElMessage.error(err?.response?.data?.error || '启动失败')
  }
}

async function handleStop(task: any) {
  try {
    await ElMessageBox.confirm(`确认停止定时任务「${task.name}」吗？`, '停止确认', { type: 'warning' })
    await taskApi.stop(task.id)
    ElMessage.success('任务已停止')
    task.status = 1
    loadTasks()
  } catch (err: any) {
    if (err === 'cancel' || err?.toString() === 'cancel') return
    ElMessage.error(err?.response?.data?.error || '停止失败')
  }
}

async function handleToggle(task: any) {
  try {
    if (task.status === 0) {
      await ElMessageBox.confirm(`确认启用定时任务「${task.name}」吗？`, '启用确认', { type: 'info' })
      const res = await taskApi.enable(task.id)
      ElMessage.success(res.message || '已启用')
    } else {
      const confirmMessage = task.status === 2
        ? `确认禁用定时任务「${task.name}」吗？当前执行不会被中断，禁用会在本次运行结束后生效。`
        : `确认禁用定时任务「${task.name}」吗？`
      await ElMessageBox.confirm(confirmMessage, '禁用确认', { type: 'warning' })
      const res = await taskApi.disable(task.id)
      ElMessage.success(res.message || (task.status === 2 ? '已设置为禁用，当前执行结束后生效' : '已禁用'))
    }
    loadTasks()
  } catch (err: any) {
    if (err === 'cancel' || err?.toString?.() === 'cancel') return
    ElMessage.error(err?.response?.data?.error || '操作失败')
  }
}

async function handleDelete(task: any) {
  try {
    await ElMessageBox.confirm(`确定删除任务 "${task.name}"？`, '确认删除', { type: 'warning' })
    await taskApi.delete(task.id)
    ElMessage.success('任务已删除')
    loadTasks()
  } catch (err: any) {
    if (err === 'cancel' || err?.toString?.() === 'cancel') return
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

function isSelected(id: number) {
  return selectedIdSet.value.has(id)
}

function toggleSelected(id: number, checked: boolean | string | number) {
  const next = new Set(selectedIds.value)
  if (checked) {
    next.add(id)
  } else {
    next.delete(id)
  }
  selectedIds.value = [...next]
}

async function handleBatchAction(action: string) {
  if (selectedIds.value.length === 0) {
    ElMessage.warning('请先选择任务')
    return
  }
  const confirmMap: Record<string, { title: string; msg: string; type: 'warning' | 'info' }> = {
    delete: { title: '批量删除', msg: `确定删除选中的 ${selectedIds.value.length} 个任务？`, type: 'warning' },
    run: { title: '批量运行', msg: `确定运行选中的 ${selectedIds.value.length} 个任务？`, type: 'info' },
    enable: { title: '批量启用', msg: `确定启用选中的 ${selectedIds.value.length} 个任务？`, type: 'info' },
    disable: { title: '批量禁用', msg: `确定禁用选中的 ${selectedIds.value.length} 个任务？`, type: 'warning' },
  }
  const confirm = confirmMap[action]
  if (confirm) {
    await ElMessageBox.confirm(confirm.msg, confirm.title, { type: confirm.type })
  }
  try {
    await taskApi.batch(selectedIds.value, action)
    ElMessage.success('操作成功')
    loadTasks()
  } catch (err: any) {
    if (err === 'cancel' || err?.toString() === 'cancel') return
    ElMessage.error(err?.response?.data?.error || '操作失败')
  }
}

async function handleBatchPin() {
  if (selectedIds.value.length === 0) {
    ElMessage.warning('请先选择任务')
    return
  }
  // 并发发送，单条失败不阻塞其他任务
  const results = await Promise.allSettled(selectedIds.value.map(id => taskApi.pin(id)))
  const failed = results.filter(r => r.status === 'rejected')
  if (failed.length === 0) {
    ElMessage.success(`批量置顶成功（${results.length} 个）`)
  } else if (failed.length === results.length) {
    const first = failed[0] as PromiseRejectedResult
    ElMessage.error((first.reason as any)?.response?.data?.error || '批量置顶全部失败')
  } else {
    ElMessage.warning(`已置顶 ${results.length - failed.length} 个，${failed.length} 个失败`)
  }
  loadTasks()
}

async function handleCleanLogs() {
  let daysStr: string
  try {
    const { value } = await ElMessageBox.prompt('清理多少天前的日志？', '日志清理', {
      confirmButtonText: '确定',
      cancelButtonText: '取消',
      inputPattern: /^\d+$/,
      inputErrorMessage: '请输入有效的天数',
      inputValue: '30',
    })
    daysStr = value
  } catch {
    return
  }
  try {
    await taskApi.cleanLogs(Number(daysStr))
    ElMessage.success('日志清理成功')
  } catch (err: any) {
    ElMessage.error(err?.response?.data?.error || '日志清理失败')
  }
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
  } catch (err: any) {
    ElMessage.error(err?.response?.data?.error || '导出失败')
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
    let data: any
    try {
      data = JSON.parse(text)
    } catch (e: any) {
      ElMessage.error(`JSON 解析失败：${e?.message || '文件格式错误'}`)
      return
    }
    const tasksData = Array.isArray(data) ? data : data.data || data.tasks
    if (!Array.isArray(tasksData)) {
      ElMessage.error('导入数据结构无效：期望数组或 {data: [...]} / {tasks: [...]}')
      return
    }
    const invalid = tasksData.find(t => !t || typeof t !== 'object' || !t.name)
    if (invalid) {
      ElMessage.error('导入数据中存在缺少 name 字段的任务')
      return
    }
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
      <div>
        <h2>定时任务</h2>
        <span class="page-subtitle">管理和调度所有定时执行任务</span>
      </div>
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

    <ViewManager @view-change="handleViewChange" />

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

    <div v-if="isMobile" class="dd-mobile-list">
      <div
        v-for="row in tasks"
        :key="row.id"
        class="dd-mobile-card task-card"
      >
        <div class="dd-mobile-card__header">
          <div class="dd-mobile-card__title-wrap task-card__title-wrap">
            <div class="task-card__title-row">
              <div class="dd-mobile-card__selection">
                <el-checkbox :model-value="isSelected(row.id)" @change="toggleSelected(row.id, $event)" />
                <div class="task-card__name-block">
                  <div class="task-card__name-line">
                    <el-icon v-if="row.is_pinned" class="pin-icon" @click="handlePin(row)"><Star /></el-icon>
                    <span class="dd-mobile-card__title">{{ row.name }}</span>
                  </div>
                </div>
              </div>
              <el-tag :type="getStatusType(row.status)" size="small" :class="row.status === 2 ? 'tag-with-dot' : ''">
                <span v-if="row.status === 2" class="pulse-dot"></span>
                {{ getStatusText(row.status) }}
              </el-tag>
            </div>

            <div class="dd-mobile-card__badges">
              <el-tag size="small" effect="plain" class="task-label task-label--type">
                {{ getTaskTypeLabel(row.task_type) }}
              </el-tag>
              <el-tag
                v-for="label in displayTaskLabels(row)"
                :key="label"
                size="small"
                effect="plain"
                class="task-label"
              >
                {{ label }}
              </el-tag>
            </div>

            <div class="dd-mobile-card__subtitle task-card__command">
              <code class="command-text">
                <template v-if="splitTaskCommandDisplay(row.command).script">
                  <span>{{ splitTaskCommandDisplay(row.command).before }}</span>
                  <span class="script-link" @click.stop="navigateToScript(splitTaskCommandDisplay(row.command).script!)">{{ splitTaskCommandDisplay(row.command).script }}</span>
                  <span>{{ splitTaskCommandDisplay(row.command).after }}</span>
                </template>
                <template v-else>{{ row.command }}</template>
              </code>
            </div>
          </div>
        </div>

        <div class="dd-mobile-card__body">
          <div class="dd-mobile-card__grid">
            <div class="dd-mobile-card__field">
              <span class="dd-mobile-card__label">定时规则</span>
              <div class="dd-mobile-card__value">
                <template v-if="row.task_type === 'cron'">
                  <div class="cron-text-list">
                    <code
                      v-for="expression in getCronExpressions(row)"
                      :key="expression"
                      class="cron-text cron-text--stacked"
                    >
                      {{ expression }}
                    </code>
                  </div>
                </template>
                <span v-else class="text-muted">{{ getTaskTypeLabel(row.task_type) }}</span>
              </div>
            </div>
            <div class="dd-mobile-card__field">
              <span class="dd-mobile-card__label">上次结果</span>
              <div class="dd-mobile-card__value">
                <el-tag :type="getRunStatusType(row.last_run_status)" size="small">
                  {{ getRunStatusText(row.last_run_status) }}
                </el-tag>
              </div>
            </div>
            <div class="dd-mobile-card__field">
              <span class="dd-mobile-card__label">最后运行</span>
              <span class="dd-mobile-card__value time-text">{{ row.last_run_at ? formatTime(row.last_run_at) : '-' }}</span>
            </div>
            <div class="dd-mobile-card__field">
              <span class="dd-mobile-card__label">下次运行</span>
              <span class="dd-mobile-card__value time-text">{{ row.next_run_at ? formatTime(row.next_run_at) : '-' }}</span>
            </div>
            <div class="dd-mobile-card__field">
              <span class="dd-mobile-card__label">耗时</span>
              <span class="dd-mobile-card__value">{{ row.last_running_time != null ? `${row.last_running_time.toFixed(1)}s` : '-' }}</span>
            </div>
          </div>

          <div class="dd-mobile-card__actions task-card__actions">
            <el-button v-if="row.status !== 2" type="primary" size="small" @click="handleRun(row)">运行</el-button>
            <el-button v-else type="warning" size="small" @click="handleStop(row)">停止</el-button>
            <el-button :type="row.status === 0 ? 'success' : 'danger'" size="small" plain @click="handleToggle(row)">
              {{ row.status === 0 ? '启用' : '禁用' }}
            </el-button>
            <el-button size="small" @click="openLogViewer(row)">日志</el-button>
            <el-button size="small" @click="openEdit(row)">编辑</el-button>
            <el-dropdown trigger="click">
              <el-button size="small">
                更多
                <el-icon><More /></el-icon>
              </el-button>
              <template #dropdown>
                <el-dropdown-menu>
                  <el-dropdown-item @click="openDetail(row)">详情</el-dropdown-item>
                  <el-dropdown-item @click="openLogFiles(row)">日志文件</el-dropdown-item>
                  <el-dropdown-item @click="handleCopy(row)">复制</el-dropdown-item>
                  <el-dropdown-item @click="handlePin(row)">{{ row.is_pinned ? '取消置顶' : '置顶' }}</el-dropdown-item>
                  <el-dropdown-item divided @click="handleDelete(row)">
                    <span style="color: var(--el-color-danger)">删除</span>
                  </el-dropdown-item>
                </el-dropdown-menu>
              </template>
            </el-dropdown>
          </div>
        </div>
      </div>

      <el-empty v-if="!loading && tasks.length === 0" description="暂无任务" />
    </div>

    <el-table
      v-else
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
            <el-tag size="small" effect="plain" class="task-label task-label--type">
              {{ getTaskTypeLabel(row.task_type) }}
            </el-tag>
            <el-tag
              v-for="label in displayTaskLabels(row)"
              :key="label"
              size="small"
              effect="plain"
              class="task-label"
            >
              {{ label }}
            </el-tag>
          </div>
        </template>
      </el-table-column>
      <el-table-column label="命令" min-width="160">
        <template #default="{ row }">
          <code class="command-text">
            <template v-if="splitTaskCommandDisplay(row.command).script">
              <span>{{ splitTaskCommandDisplay(row.command).before }}</span>
              <span class="script-link" @click.stop="navigateToScript(splitTaskCommandDisplay(row.command).script!)">{{ splitTaskCommandDisplay(row.command).script }}</span>
              <span>{{ splitTaskCommandDisplay(row.command).after }}</span>
            </template>
            <template v-else>{{ row.command }}</template>
          </code>
        </template>
      </el-table-column>
      <el-table-column label="定时规则" min-width="130" show-overflow-tooltip>
        <template #default="{ row }">
          <template v-if="row.task_type === 'cron'">
            <div class="cron-text-list">
              <code
                v-for="expression in getCronExpressions(row)"
                :key="expression"
                class="cron-text cron-text--stacked"
              >
                {{ expression }}
              </code>
            </div>
          </template>
          <span v-else class="text-muted">{{ getTaskTypeLabel(row.task_type) }}</span>
        </template>
      </el-table-column>
      <el-table-column label="状态" width="100" align="center">
        <template #default="{ row }">
          <el-tag :type="getStatusType(row.status)" size="small" :class="row.status === 2 ? 'tag-with-dot' : ''">
            <span v-if="row.status === 2" class="pulse-dot"></span>
            {{ getStatusText(row.status) }}
          </el-tag>
        </template>
      </el-table-column>
      <el-table-column label="最后运行" width="130" align="center">
        <template #default="{ row }">
          <span v-if="row.last_run_at" class="time-text">{{ formatTime(row.last_run_at) }}</span>
          <span v-else class="text-muted">-</span>
        </template>
      </el-table-column>
      <el-table-column label="下次运行" width="130" align="center">
        <template #default="{ row }">
          <span v-if="row.next_run_at" class="time-text">{{ formatTime(row.next_run_at) }}</span>
          <span v-else class="text-muted">-</span>
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
      <el-table-column label="操作" width="320" fixed="right">
        <template #default="{ row }">
          <el-button-group size="small">
            <el-button v-if="row.status !== 2" type="primary" text @click="handleRun(row)">运行</el-button>
            <el-button v-else type="warning" text @click="handleStop(row)">停止</el-button>
            <el-button :type="row.status === 0 ? 'success' : 'danger'" text @click="handleToggle(row)">
              {{ row.status === 0 ? '启用' : '禁用' }}
            </el-button>
            <el-button text @click="openLogViewer(row)">日志</el-button>
            <el-button text @click="openEdit(row)">编辑</el-button>
            <el-dropdown trigger="click">
              <el-button text><el-icon><More /></el-icon></el-button>
              <template #dropdown>
                <el-dropdown-menu>
                  <el-dropdown-item @click="openDetail(row)">详情</el-dropdown-item>
                  <el-dropdown-item @click="openLogFiles(row)">日志文件</el-dropdown-item>
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
        @size-change="handlePageSizeChange"
      />
    </div>

    <TaskForm
      v-model:visible="formVisible"
      :task="editingTask"
      :prefill="prefillData"
      :notification-channels="notificationChannels"
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

  h2 { margin: 0; font-size: 20px; font-weight: 700; color: var(--el-text-color-primary); }

  .page-subtitle {
    font-size: 13px;
    color: var(--el-text-color-secondary);
    display: block;
    margin-top: 2px;
  }

  .header-actions {
    display: flex;
    gap: 10px;
  }
}

:deep(.tag-with-dot) {
  display: inline-flex !important;
  align-items: center;
  gap: 5px;
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

  .script-link {
    color: var(--el-color-primary);
    cursor: pointer;
    &:hover { text-decoration: underline; }
  }
}

.cron-text {
  font-family: var(--dd-font-mono);
  font-size: 13px;
  color: var(--el-text-color-secondary);
  white-space: nowrap;
}

.cron-text-list {
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.cron-text--stacked {
  white-space: pre-wrap;
  word-break: break-all;
}

.time-text {
  font-family: var(--dd-font-mono);
  font-size: 12px;
  color: var(--el-text-color-regular);
}

.text-muted {
  color: var(--el-text-color-placeholder);
}

.pagination-bar {
  margin-top: 20px;
  display: flex;
  justify-content: flex-end;
}

.task-card {
  .command-text {
    display: block;
    white-space: pre-wrap;
    word-break: break-all;
  }
}

.task-card__title-row {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 10px;
}

.task-card__name-block {
  min-width: 0;
}

.task-card__name-line {
  display: flex;
  align-items: flex-start;
  gap: 8px;
}

.task-card__actions {
  > * {
    flex: 1 1 calc(50% - 4px);
  }
}

:deep(.el-table) {
  font-size: 14px;

  .el-table__cell {
    padding: 14px 0;
  }
}

:deep(.el-button) {
  font-size: 14px;
  padding: 9px 18px;
}

:deep(.el-button--small) {
  font-size: 13px;
  padding: 7px 14px;
}

@media screen and (max-width: 768px) {
  .page-header {
    flex-direction: column;
    align-items: flex-start;
    gap: 10px;
    margin-bottom: 14px;

    h2 { font-size: 18px; }

    .header-actions {
      width: 100%;
      flex-wrap: wrap;
    }
  }

  .filter-bar {
    flex-wrap: wrap;
    gap: 8px;

    :deep(.el-input),
    :deep(.el-select) {
      width: 100% !important;
    }

    .batch-actions {
      width: 100%;
      margin-left: 0;
      flex-wrap: wrap;
    }
  }

  :deep(.el-table) {
    font-size: 12px;

    .el-table__cell {
      padding: 8px 0;
    }
  }
}
</style>
