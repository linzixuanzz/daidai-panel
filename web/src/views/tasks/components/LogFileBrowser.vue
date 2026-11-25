<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import { taskApi } from '@/api/task'
import { readListPreference } from '@/utils/listPreferences'
import { ElMessage, ElMessageBox } from 'element-plus'
import { useAuthStore } from '@/stores/auth'
import { useResponsive } from '@/composables/useResponsive'
import { ansiToHtml, normalizeAnsi } from '@/utils/ansi'
import { canOperate } from '@/utils/roles'
import { toDateRangeParams } from '@/utils/datetime'
import { startRawLogDownload } from '@/utils/rawLogDownload'
import { extractError } from '@/utils/error'
import DdSplitButton from '@/components/ui/DdSplitButton.vue'
import type { SplitButtonItem } from '@/components/ui/DdSplitButton.vue'

const props = defineProps<{
  visible: boolean
  taskId: number | null
  taskName: string
}>()

const emit = defineEmits<{
  'update:visible': [value: boolean]
}>()

// 与后端 handler/log_archive_download.go 的 maxArchiveFiles / maxArchiveBytes 一一对应。
// 两边必须同时改：前端偏小会挡掉本来能下的包，偏大则等后端 400 才告诉用户，白跑一个来回。
const MAX_ARCHIVE_FILES = 2000
const MAX_ARCHIVE_BYTES = 512 * 1024 * 1024

const authStore = useAuthStore()
const logFiles = ref<any[]>([])
const loading = ref(false)
const selectedFile = ref<string | null>(null)
const fileContent = ref('')
const contentLoading = ref(false)
// 右侧正文 <pre>，它自己就是滚动容器（.content-text 的 overflow-y:auto）
const contentTextRef = ref<HTMLElement>()
// 正在换取下载票据的文件 key，避免连点重复请求
const downloadingKey = ref<string | null>(null)
// 打包下载同样只允许一个在飞，写法与 downloadingKey 一致
const archiveDownloading = ref(false)
const { dialogFullscreen } = useResponsive()
const canOperateLogs = computed(() => canOperate(authStore.user?.role))

function getFileKey(file: any) {
  return file?.path || file?.filename
}

// 列表本来就带 size，总量直接在前端算，不用为了一行汇总再问后端要一次
const totalBytes = computed(() => logFiles.value.reduce((sum, file) => sum + (Number(file?.size) || 0), 0))

const archiveOverLimit = computed(
  () => logFiles.value.length > MAX_ARCHIVE_FILES || totalBytes.value > MAX_ARCHIVE_BYTES
)

const archiveLimitHint = computed(() => {
  if (!archiveOverLimit.value) return ''
  // 上限那一半必须由 MAX_ARCHIVE_BYTES 现算：写死「512.0 MB」的话，以后调常量这行会静默变成谎话
  // （纯文案，没有任何测试守着，只能靠和常量绑死来保证不撒谎）
  return `共 ${logFiles.value.length} 个文件 / ${formatBytes(totalBytes.value)}，超过打包上限（最多 ${MAX_ARCHIVE_FILES} 个文件 / ${formatBytes(MAX_ARCHIVE_BYTES)}），请改用「最近 7 天」或「最近 30 天」`
})

const archiveItems = computed<SplitButtonItem[]>(() => [
  { key: 'last7', label: '最近 7 天', disabled: archiveDownloading.value },
  { key: 'last30', label: '最近 30 天', disabled: archiveDownloading.value },
])

const renderedFileContent = computed(() => {
  let currentLine = ''
  let pendingCarriageReturn = false
  const lines: string[] = []

  for (let i = 0; i < fileContent.value.length; i++) {
    const char = fileContent.value[i]
    if (char === '\r') {
      if (fileContent.value[i + 1] === '\n') {
        lines.push(currentLine)
        currentLine = ''
        pendingCarriageReturn = false
        i++
        continue
      }
      // 裸 \r 代表光标回到行首；等后续普通字符到来时再覆盖当前行。
      // 这样日志文件预览和实时日志弹窗的进度条显示保持一致。
      pendingCarriageReturn = true
      continue
    }

    if (char === '\n') {
      lines.push(currentLine)
      currentLine = ''
      pendingCarriageReturn = false
      continue
    }

    if (pendingCarriageReturn) {
      currentLine = ''
      pendingCarriageReturn = false
    }
    currentLine += char
  }

  if (currentLine !== '' || lines.length === 0) {
    lines.push(currentLine)
  }

  return lines.join('\n')
})

const renderedFileHtml = computed(() => {
  return ansiToHtml(normalizeAnsi(renderedFileContent.value))
})

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

async function viewFile(file: any) {
  const filename = file.filename
  const fileKey = getFileKey(file)
  selectedFile.value = fileKey
  contentLoading.value = true
  try {
    const res = await taskApi.logFileContent(props.taskId!, filename, file.path)
    fileContent.value = res.content || ''
    // #147：账户偏好「打开已结束的日志时定位到底部」对日志文件同样生效（任务页挂载时已 ensure 过，这里同步读缓存）。
    // 关着什么都不做，保持原样：沿用上一个文件的滚动位置。
    if (readListPreference('log_open_at_bottom')) {
      void nextTick(() => {
        const el = contentTextRef.value
        if (el) el.scrollTop = el.scrollHeight
      })
    }
  } catch {
    ElMessage.error('加载文件内容失败')
    fileContent.value = ''
  } finally {
    contentLoading.value = false
  }
}

// 「下载原始文件」：服务端直接把磁盘上的日志文件流式吐给浏览器。
// 右侧预览按终端语义折叠了裸 \r，这里下载到的是未经折叠的原始字节。
// 走浏览器原生下载而不是 axios blob——大日志不会被整份读进 JS 内存。
async function downloadRawFile(file: any) {
  const taskId = props.taskId
  const fileKey = getFileKey(file)
  if (!taskId || downloadingKey.value) return

  downloadingKey.value = fileKey
  try {
    startRawLogDownload(await taskApi.logFileRawDownloadTicket(taskId, file.filename, file.path))
  } catch (err) {
    ElMessage.error(extractError(err, '下载原始日志文件失败'))
  } finally {
    downloadingKey.value = null
  }
}

// 「打包下载」：服务端流式打一个 zip 吐给浏览器，zip 内保留 task_<id>[_名字]/ 目录层级。
// 一个每 5 分钟跑一次的任务，7 天有 2000+ 个日志文件，逐个点「下载原始」根本点不完。
// 同样走「换票 + 浏览器原生下载」，零新增下载路径。
async function downloadArchive(params?: { start?: string; end?: string }) {
  const taskId = props.taskId
  if (!taskId || archiveDownloading.value) return

  archiveDownloading.value = true
  try {
    startRawLogDownload(await taskApi.logArchiveDownloadTicket(taskId, params))
  } catch (err) {
    ElMessage.error(extractError(err, '打包下载日志文件失败'))
  } finally {
    archiveDownloading.value = false
  }
}

// 主体 =「全部」。超上限时就地拦下，不发请求——后端也会 400，但等一个来回才告诉用户太迟了。
// 之所以不是把主体禁掉：EP 的 split-button 主体与箭头共用同一个 disabled，
// 一禁就把「最近 7 天 / 30 天」这两条出路一起禁了，那才是真的没救。
function downloadWholeArchive() {
  if (archiveOverLimit.value) {
    ElMessage.warning(archiveLimitHint.value)
    return
  }
  void downloadArchive()
}

function onArchiveCommand(key: string) {
  void downloadArchive(recentDaysParams(key === 'last30' ? 30 : 7))
}

// 「最近 N 天」含今天，与 utils/datetime.ts 的 DATE_RANGE_SHORTCUTS 口径一致。
// 两端的时分秒必须交给 toDateRangeParams 收拢成「起始日 00:00:00 ~ 结束日 23:59:59.999」，
// 少了它结束日当天的日志会被整天漏掉。
function recentDaysParams(days: number) {
  const end = new Date()
  const start = new Date(Date.now() - (days - 1) * 24 * 60 * 60 * 1000)
  const range = toDateRangeParams([start, end])
  return { start: range.start_time, end: range.end_time }
}

async function deleteFile(file: any) {
  const filename = file.filename
  const fileKey = getFileKey(file)
  try {
    await ElMessageBox.confirm(`确定删除日志文件 "${filename}"？`, '确认删除', { type: 'warning' })
    await taskApi.deleteLogFile(props.taskId!, filename, file.path)
    ElMessage.success('删除成功')
    loadLogFiles()
    if (selectedFile.value === fileKey) {
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
    width="min(1200px, 92vw)"
    class="task-log-files-dialog"
    :fullscreen="dialogFullscreen"
    :lock-scroll="false"
    @close="handleClose"
  >
    <div class="log-files-browser">
      <div class="file-list-pane">
        <!-- 列表上方常驻一条汇总 + 打包入口：先让用户知道要下多大，再决定下不下 -->
        <div class="file-list-header">
          <span class="file-list-summary">共 {{ logFiles.length }} 个文件 · 合计 {{ formatBytes(totalBytes) }}</span>
          <el-tooltip :content="archiveLimitHint" :disabled="!archiveLimitHint" placement="top">
            <span>
              <DdSplitButton
                label="打包下载"
                type="primary"
                size="small"
                :items="archiveItems"
                :disabled="logFiles.length === 0 || archiveDownloading"
                @click="downloadWholeArchive"
                @command="onArchiveCommand"
              />
            </span>
          </el-tooltip>
        </div>

        <div class="file-list" v-loading="loading">
          <div v-if="logFiles.length === 0" class="empty-hint">暂无日志文件</div>
          <div
            v-for="file in logFiles"
            :key="getFileKey(file)"
            class="file-item"
            :class="{ active: selectedFile === getFileKey(file) }"
            @click="viewFile(file)"
          >
            <div class="file-info">
              <el-icon><Document /></el-icon>
              <span class="file-name" :title="file.filename">{{ file.filename }}</span>
            </div>
            <div class="file-actions">
              <span class="file-size">{{ formatBytes(file.size) }}</span>
              <el-button
                type="primary"
                text
                size="small"
                :loading="downloadingKey === getFileKey(file)"
                title="下载原始日志文件：由服务端直传磁盘上的字节，回车符与终端控制序列一个不少（右侧预览是折叠后的）"
                @click.stop="downloadRawFile(file)"
              >下载原始</el-button>
              <!-- 观察者没有删除权限：按后端 403 之前就别把按钮摆出来，
                   与 views/logs/index.vue 的日志文件列表保持一致 -->
              <el-button
                v-if="canOperateLogs"
                type="danger"
                text
                size="small"
                @click.stop="deleteFile(file)"
              >
                <el-icon><Delete /></el-icon>
              </el-button>
            </div>
          </div>
        </div>
      </div>

      <div class="file-content" v-loading="contentLoading">
        <div v-if="!selectedFile" class="content-placeholder">
          <el-icon :size="48" color="var(--dd-text-3)"><Document /></el-icon>
          <span>选择文件查看内容</span>
        </div>
        <pre v-else ref="contentTextRef" class="content-text dd-log-surface" v-html="renderedFileHtml || '(空文件)'"></pre>
      </div>
    </div>
    <!-- 移动端全屏时关闭入口放右下角（v3.3.1，issue #143 F2）：右上角 × 离拇指最远，iOS 上还曾被顶栏整块盖住。
         有 footer 后 global.scss 会在 ≤768 隐藏 EP 自带的 ×。桌面不渲染 footer，保持原样。
         关闭走 handleClose：父组件把 visible 置 false 后 EP 照常触发 @close，上面 watch(visible) 里的清理逻辑不受影响。 -->
    <template v-if="dialogFullscreen" #footer>
      <el-button @click="handleClose">关闭</el-button>
    </template>
  </el-dialog>
</template>

<style scoped lang="scss">
.log-files-browser {
  display: flex;
  gap: 16px;
  // 高度全权交给弹窗算（定值 810px 写在文件末尾的非 scoped 块里），这里只负责吃满。
  // 原来写死的 700px 是三条滚动条的根因（v3.3.2，issue #144 桌面端评论条）：
  // .el-dialog__body 不是 flex 容器，这块就不是 flex item、矮窗口下压不回去，
  // 于是把 body 的 overflow:auto 顶出第三条滚动条，两个子窗自己的滚动条还被推到视窗外。
  height: 100%;
  min-height: 0;
}

// 左侧改成「汇总 + 打包入口」与列表上下叠的一列，宽度沿用原来 .file-list 的 340px
.file-list-pane {
  width: 340px;
  flex-shrink: 0;
  display: flex;
  flex-direction: column;
  gap: 8px;
  min-height: 0;
}

.file-list-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  flex-wrap: wrap;
}

.file-list-summary {
  font-size: 12px;
  color: var(--dd-text-2);
}

.file-list {
  // 每行多了「下载原始」入口，比原来的 280px 需要更多横向空间
  width: 100%;
  flex: 1;
  min-height: 0;
  border: 1px solid var(--dd-border-strong);
  // 文件列表容器 → surface 档（自身是滚动容器，内部行会被圆角裁掉，不会溢出到角外）
  border-radius: var(--dd-radius-surface);
  overflow-y: auto;
}

.file-item {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 10px 12px;
  cursor: pointer;
  // 列表项分隔线走 --dd-divider，避免素白 clean 下外框透明导致行线隐身
  border-bottom: 1px solid var(--dd-divider, var(--dd-border));
  transition: background-color var(--dd-motion-fast) var(--dd-ease-standard);

  &:hover {
    background: var(--el-fill-color-light);
  }

  &.active {
    background: color-mix(in srgb, var(--dd-accent) 10%, var(--dd-surface-solid));
    border-left: 3px solid var(--dd-accent);
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
  flex-shrink: 0;

  // 间距统一交给 flex gap，清掉 EP 默认相邻按钮 12px 左外边距，释放文件名展示空间
  :deep(.el-button + .el-button) {
    margin-left: 0;
  }
}

.file-size {
  font-size: 12px;
  color: var(--dd-text-2);
}

.file-content {
  flex: 1;
  border: 1px solid var(--dd-border-strong);
  // 日志正文面板 → surface 档
  border-radius: var(--dd-radius-surface);
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
  color: var(--dd-text-3);
  gap: 12px;
}

.content-text {
  flex: 1;
  margin: 0;
  padding: 12px;
  overflow-y: auto;
  font-family: var(--dd-font-mono);
  font-size: 13px;
  line-height: 1.6;
  white-space: pre-wrap;
  word-break: break-all;
  // 🔴 保持 0，不吃令牌 —— 必须写回来压掉 global.scss 里 .dd-log-surface 的 surface 档。
  // 它是【贴边内嵌】于上面的 .file-content：那个外框已经有 1px 边框 + surface 档圆角 +
  // overflow:hidden，而这块 <pre> 铺满外框内部、只隔着 1px 边框。两者都取 10px 的话，
  // 圆角模式下四角会出现两条相距 1px 的同心圆弧，看起来像边框在拐角处凭空变粗一倍。
  // 圆角交给 .file-content 一层去表达 + overflow:hidden 去裁即可。
  border-radius: 0;
}

.empty-hint {
  padding: 40px 20px;
  text-align: center;
  color: var(--dd-text-3);
}

// 中窄屏收窄左侧文件列表栏宽至 240px，给右侧日志正文留足可读宽度
@media (max-width: 960px) {
  .file-list-pane {
    width: 240px;
  }
}

@media (max-width: 768px) {
  .log-files-browser {
    flex-direction: column;
    // 这里刻意不再写高度（v3.3.2，issue #144）：原来那条 calc(100dvh - 140px) 是把全屏弹窗的
    // header / body padding / footer 三段内边距手算出来的魔数，global.scss 一改移动端弹窗内边距就得回来重算。
    // 现在 is-fullscreen 下 EP 给 .el-dialog 设了 height:100%，加上末尾非 scoped 块的 body 规则，
    // 上面的 height:100% 会顺着 flex 链自动吃满整屏，不需要任何魔数。
    gap: 12px;
  }

  .file-list-pane {
    width: 100%;
    flex: none;
  }

  .file-list {
    max-height: 220px;
  }
}
</style>

<!--
  独立的非 scoped style：专门处理 el-dialog 渲染出来的 .el-dialog 元素（class="task-log-files-dialog" 落在它上面）。
  原因同 LogViewer.vue 末尾那一块：本组件 template 的根就是 el-dialog 组件自身，.el-dialog / .el-dialog__body
  都由 Element Plus 在组件内部渲染、带不上本组件的 scopeId，外面也没有任何带 data-v 属性的祖先包着它，
  所以 :deep(.task-log-files-dialog) 编译出的 [data-v-xxx] .task-log-files-dialog 在真实 DOM 里命不中。
  这【不是】teleport 的问题：EP 2.13.5 的 el-dialog 默认原地渲染，本项目也不要为层级问题加 append-to-body。
  🔴 因为是全局规则，类名必须唯一：不能叫 log-files-dialog —— 那个名字已经挂在 views/logs/index.vue
  的「日志文件」弹窗上，撞名会把那个 900px 宽、内含 max-height 420px 表格的小弹窗一起拉成 810px 高并截断。
-->
<style lang="scss">
.task-log-files-dialog {
  // 810 = 内容区 700 + 弹窗的框 109（header 61 + body 上下 padding 48）。
  // global.scss 的 .el-dialog 已有 max-height: calc(100dvh - margin*2) 按视口封顶，
  // 所以这里写定值就够，不用再包一层 min()。视口够高 → 内容区仍是原来的 700px；
  // 视口一矮 → 整窗等比缩小、左右两个小窗一起变矮，而不是把 body 撑出第三条滚动条。
  height: 810px;

  .el-dialog__body {
    // global.scss 给全站弹窗的 body 默认 overflow:auto（别的弹窗正靠它滚），这里定点改成 hidden：
    // 高度只由左右两个小窗各自的 overflow-y:auto 消化，父级永远不出现第三条滚动条
    // （v3.3.2，issue #144 桌面端评论条）。绝不能去改 global.scss 里那条公共规则。
    overflow: hidden;
    min-height: 0;
  }

  // 移动端 :fullscreen 时高度交还给 EP 的 .el-dialog.is-fullscreen{height:100%}，
  // 顺手把上面的定值和 global.scss 的视口封顶一起放开，内容区才能顺着 flex 链吃满整屏。
  &.is-fullscreen {
    height: 100%;
    max-height: 100%;
  }
}
</style>
