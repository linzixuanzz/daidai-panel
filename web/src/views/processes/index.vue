<script setup lang="ts">
// 「后台进程」页（#176）：任务里 setsid nohup … & 拉起的进程会逃出任务的进程组、在任务结束后继续跑，
// 这里列出带任务标识（DAIDAI_TASK_ID）的进程，可以查看并结束。只给管理员。
import { ref, onMounted, onBeforeUnmount, watch } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Cpu, Refresh } from '@element-plus/icons-vue'
import { processApi, type PanelProcess } from '@/api/process'
import { useResponsive } from '@/composables/useResponsive'
import { usePageActivity } from '@/composables/usePageActivity'
import { formatDateTime } from '@/utils/datetime'

const router = useRouter()
const { isMobile } = useResponsive()
const { isPageActive } = usePageActivity()

const processes = ref<PanelProcess[]>([])
const loading = ref(false)
// 后端说不支持（非 Linux）时整页换成提示；all_supported=false（Magisk）时隐藏「显示全部进程」开关
const supported = ref(true)
const allSupported = ref(false)
const showAll = ref(false)
// 正在结束的那一行，按钮转圈、防连点
const killingPid = ref(0)
// 「已运行」按这个时刻算，每次刷新列表时更新
const now = ref(Date.now())

let refreshTimer: ReturnType<typeof setInterval> | null = null
// 每次请求编号，只认最后一次的结果：切换「显示全部进程」时，之前那次自动刷新晚回来也不能把列表盖回去
let loadSeq = 0
// 在途请求数：自动刷新遇到上一次还没回来（慢机器）就跳过这一轮，不往上叠
let inFlight = 0

async function loadProcesses(showLoading = false) {
  const seq = ++loadSeq
  inFlight += 1
  if (showLoading) loading.value = true
  try {
    const res = await processApi.list(showAll.value ? 'all' : 'task')
    if (seq !== loadSeq) return
    supported.value = res.supported !== false
    allSupported.value = !!res.all_supported
    processes.value = res.data || []
    now.value = Date.now()
  } catch (err: any) {
    if (seq !== loadSeq) return
    // 「全部进程」被拒（Magisk）时退回只看任务进程，免得每 5 秒弹一次错
    const fallbackFromAll = showAll.value
    if (fallbackFromAll) showAll.value = false
    // 后台自动刷新失败不弹提示（面板重启期间会连着弹），手动刷新、切开关时才提示
    if (showLoading || fallbackFromAll) {
      ElMessage.error(err?.response?.data?.error || '读取进程列表失败')
    }
  } finally {
    inFlight -= 1
    if (seq === loadSeq) loading.value = false
  }
}

function stopAutoRefresh() {
  if (refreshTimer) {
    clearInterval(refreshTimer)
    refreshTimer = null
  }
}

// 页面可见（当前菜单 + 浏览器标签页没隐藏）且平台支持时每 5 秒刷新一次，否则停
function syncAutoRefresh() {
  if (!isPageActive.value || !supported.value) {
    stopAutoRefresh()
    return
  }
  if (refreshTimer) return
  refreshTimer = setInterval(() => {
    if (inFlight === 0) void loadProcesses()
  }, 5000)
}

// 切到别的菜单（keep-alive 停用）或标签页隐藏时停；回来时立刻补拉一次
watch(isPageActive, (active, wasActive) => {
  if (active && !wasActive) void loadProcesses()
  syncAutoRefresh()
})
watch(supported, syncAutoRefresh)

watch(showAll, () => {
  void loadProcesses(true)
})

onMounted(() => {
  void loadProcesses(true)
  syncAutoRefresh()
})

onBeforeUnmount(() => {
  stopAutoRefresh()
})

function stateLabel(state: PanelProcess['state']) {
  if (state === 'running_with_task') return '随任务运行中'
  if (state === 'detached') return '已脱离任务'
  return '非任务进程'
}

function stateTagType(state: PanelProcess['state']) {
  if (state === 'running_with_task') return 'success'
  if (state === 'detached') return 'warning'
  return 'info'
}

function taskLabel(row: PanelProcess) {
  if (row.task_id == null) return '-'
  return row.task_name || `已删除的任务 #${row.task_id}`
}

// 任务还在才能跳过去：带上任务名，定时任务页按它搜索、把这条任务定位出来
function goToTask(row: PanelProcess) {
  if (row.task_id == null || !row.task_name) return
  void router.push({ path: '/tasks', query: { keyword: row.task_name } })
}

function formatMemory(bytes: number) {
  if (!bytes || bytes <= 0) return '-'
  const mb = bytes / 1024 / 1024
  if (mb >= 1024) return `${(mb / 1024).toFixed(2)} GB`
  return `${mb.toFixed(1)} MB`
}

// 已运行时长：常驻进程动辄几天，超过一天时带上天数
function formatElapsed(startedAt: string | null) {
  if (!startedAt) return '-'
  const seconds = Math.floor((now.value - Date.parse(startedAt)) / 1000)
  if (!Number.isFinite(seconds) || seconds < 0) return '-'
  const days = Math.floor(seconds / 86400)
  const hours = Math.floor((seconds % 86400) / 3600)
  const minutes = Math.floor((seconds % 3600) / 60)
  if (days > 0) return `${days}天${hours}小时`
  if (hours > 0) return `${hours}小时${minutes}分`
  if (minutes > 0) return `${minutes}分${seconds % 60}秒`
  return `${seconds}秒`
}

async function handleKill(row: PanelProcess) {
  // 与后端判定一致：自己是进程组组长时结束整组，否则只结束它自己
  const wholeGroup = row.pgid === row.pid
  const lines = [
    wholeGroup
      ? `将结束进程 PID ${row.pid} 及其所在的整个进程组（组号 ${row.pgid}），组里的子进程会一起结束。`
      : `将只结束进程 PID ${row.pid}（它不是进程组组长，同组的其它进程不受影响）。`,
  ]
  if (row.state === 'running_with_task') {
    lines.push('该进程属于正在运行的任务，结束后这次任务执行可能会失败。')
  }
  lines.push('先发送 SIGTERM，3 秒内没退出再强制结束。确定继续吗？')
  try {
    await ElMessageBox.confirm(lines.join('\n'), '结束进程', {
      type: 'warning',
      confirmButtonText: '结束',
      cancelButtonText: '取消',
      confirmButtonClass: 'el-button--danger',
      customStyle: { whiteSpace: 'pre-line' },
    })
  } catch {
    return
  }

  killingPid.value = row.pid
  try {
    const res = await processApi.kill(row.pid, row.start_ticks)
    if (res.exited) {
      ElMessage.success(res.message || '进程已结束')
    } else {
      ElMessage.warning(res.message || '进程仍未退出，请稍后刷新查看')
    }
  } catch (err: any) {
    ElMessage.error(err?.response?.data?.error || '结束进程失败')
  } finally {
    killingPid.value = 0
    void loadProcesses()
  }
}
</script>

<template>
  <div class="processes-page dd-fixed-page dd-page-hide-heading">
    <div class="toolbar">
      <div class="toolbar__left">
        <el-icon class="toolbar__icon"><Cpu /></el-icon>
        <span class="toolbar__tip">任务里用 <code>setsid nohup … &amp;</code> 拉起的进程会脱离任务继续运行，在这里查看和结束</span>
      </div>
      <div class="toolbar__right">
        <el-switch v-if="supported && allSupported" v-model="showAll" active-text="显示全部进程" />
        <el-button :icon="Refresh" :loading="loading" @click="loadProcesses(true)">刷新</el-button>
      </div>
    </div>

    <el-empty v-if="!supported" description="仅 Linux 部署支持" />

    <template v-else>
      <div v-if="isMobile" class="dd-mobile-list">
        <div v-for="row in processes" :key="row.pid" class="dd-mobile-card">
          <div class="dd-mobile-card__header">
            <div class="dd-mobile-card__title-wrap">
              <span class="dd-mobile-card__title">PID {{ row.pid }}</span>
              <div class="dd-mobile-card__badges">
                <el-tag size="small" :type="stateTagType(row.state)">{{ stateLabel(row.state) }}</el-tag>
              </div>
            </div>
          </div>
          <div class="dd-mobile-card__body">
            <div class="process-cmdline" :title="row.cmdline">{{ row.cmdline || '-' }}</div>
            <div class="dd-mobile-card__grid">
              <div class="dd-mobile-card__field">
                <span class="dd-mobile-card__label">所属任务</span>
                <span class="dd-mobile-card__value">
                  <el-link v-if="row.task_id != null && row.task_name" type="primary" @click="goToTask(row)">{{ row.task_name }}</el-link>
                  <span v-else class="text-secondary">{{ taskLabel(row) }}</span>
                </span>
              </div>
              <div class="dd-mobile-card__field">
                <span class="dd-mobile-card__label">已运行</span>
                <span class="dd-mobile-card__value">{{ formatElapsed(row.started_at) }}</span>
              </div>
              <div class="dd-mobile-card__field">
                <span class="dd-mobile-card__label">内存</span>
                <span class="dd-mobile-card__value">{{ formatMemory(row.rss_bytes) }}</span>
              </div>
              <div class="dd-mobile-card__field">
                <span class="dd-mobile-card__label">启动时间</span>
                <span class="dd-mobile-card__value">{{ formatDateTime(row.started_at) }}</span>
              </div>
            </div>
            <div class="dd-mobile-card__actions">
              <el-button size="small" type="danger" plain :loading="killingPid === row.pid" @click="handleKill(row)">结束</el-button>
            </div>
          </div>
        </div>
        <el-empty v-if="!loading && processes.length === 0" :description="showAll ? '没有可显示的进程' : '没有任务拉起的后台进程'" />
      </div>

      <div v-else class="table-card">
        <el-table
          :data="processes"
          v-loading="loading"
          row-key="pid"
          style="width: 100%"
          :header-cell-style="{ background: 'var(--el-fill-color-light)', color: 'var(--el-text-color-regular)', fontWeight: 600, fontSize: '13px' }"
        >
          <template #empty>
            <span class="text-secondary">{{ showAll ? '没有可显示的进程' : '没有任务拉起的后台进程' }}</span>
          </template>
          <el-table-column prop="pid" label="PID" width="90" />
          <el-table-column label="所属任务" min-width="160">
            <template #default="{ row }">
              <el-link v-if="row.task_id != null && row.task_name" type="primary" @click="goToTask(row)">{{ row.task_name }}</el-link>
              <span v-else class="text-secondary">{{ taskLabel(row) }}</span>
            </template>
          </el-table-column>
          <el-table-column label="命令" min-width="260" show-overflow-tooltip>
            <template #default="{ row }">
              <span class="process-cmdline-cell">{{ row.cmdline || '-' }}</span>
            </template>
          </el-table-column>
          <el-table-column label="启动时间" width="170">
            <template #default="{ row }">
              <span class="time-text">{{ formatDateTime(row.started_at) }}</span>
            </template>
          </el-table-column>
          <el-table-column label="已运行" width="120">
            <template #default="{ row }">{{ formatElapsed(row.started_at) }}</template>
          </el-table-column>
          <el-table-column label="内存" width="100">
            <template #default="{ row }">{{ formatMemory(row.rss_bytes) }}</template>
          </el-table-column>
          <el-table-column label="状态" width="130" align="center">
            <template #default="{ row }">
              <el-tag size="small" :type="stateTagType(row.state)">{{ stateLabel(row.state) }}</el-tag>
            </template>
          </el-table-column>
          <el-table-column label="操作" width="90" fixed="right" align="center">
            <template #default="{ row }">
              <el-button size="small" text type="danger" :loading="killingPid === row.pid" @click="handleKill(row)">结束</el-button>
            </template>
          </el-table-column>
        </el-table>
      </div>
    </template>
  </div>
</template>

<style scoped lang="scss">
.processes-page { padding: 0; }

// 工具条：与用户管理等页一致——左右两区一行排布，窄屏自动换行
.toolbar {
  display: flex; justify-content: space-between; align-items: center; margin: 14px 0; gap: 12px; flex-wrap: wrap;
  &__left { display: flex; align-items: center; gap: 8px; flex: 1; min-width: 0; }
  &__right { display: flex; align-items: center; gap: 12px; }
  &__icon { color: var(--el-color-primary); flex-shrink: 0; }
  &__tip {
    font-size: 13px; line-height: 1.6; color: var(--el-text-color-secondary);
    code { font-family: var(--dd-font-mono); font-size: 12px; color: var(--el-text-color-regular); }
  }
}

// 表格卡：与其它列表页同一套表面（dd-fixed-page 下的 flex + 内部滚动由全局规则接管）
.table-card {
  background: var(--el-bg-color); border-radius: var(--dd-radius-surface);
  border: 1px solid var(--el-border-color-lighter); overflow: hidden;
}

.process-cmdline-cell { font-family: var(--dd-font-mono); font-size: 12px; }

// 移动端卡片里的命令行：单行省略，长按 / 悬停看 title 全文
.process-cmdline {
  font-family: var(--dd-font-mono); font-size: 12px; color: var(--el-text-color-regular);
  white-space: nowrap; overflow: hidden; text-overflow: ellipsis; margin-bottom: 8px;
}

.text-secondary { color: var(--el-text-color-secondary); }
.time-text { font-family: var(--dd-font-mono); font-size: 12px; color: var(--el-text-color-regular); }

:deep(.el-table) {
  --el-table-border-color: var(--el-border-color-lighter);
}

// 窄屏：与用户管理页同一个断点、同一套写法——说明独占一行（全宽），开关与刷新放到下一行靠右
@media (max-width: 768px) {
  .toolbar { flex-direction: column; align-items: stretch; gap: 10px;
    &__left { align-items: flex-start; }
    &__icon { margin-top: 3px; }
    &__right { justify-content: flex-end; }
  }
}
</style>
