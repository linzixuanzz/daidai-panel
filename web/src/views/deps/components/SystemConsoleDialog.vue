<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { ElMessage } from 'element-plus'
import { Monitor, RefreshRight, Tickets, VideoPause, VideoPlay } from '@element-plus/icons-vue'
import {
  systemConsoleApi,
  type SystemConsoleMeta,
  type SystemConsoleRunLogs,
} from '@/api/system'
import { createTerminalLineBuffer, TERMINAL_RENDER_CHUNK_SIZE } from '@/utils/ansi'
import { useLogAutoFollow } from '@/composables/useLogAutoFollow'

/**
 * 网页版系统命令行。
 *
 * 形态照脚本页的「代码运行器」全屏弹窗：左输入、右输出、底部运行/停止，
 * 提交拿 run_id 后 500ms 轮询取输出。它刻意【不是】交互式终端：
 * 每次运行都是一个独立的一次性进程，cd / export 的状态不会带到下一条命令。
 * 这条约束在表头和说明里都写了一遍——用户按终端直觉一条条敲，
 * 然后发现「目录没切过去」是最容易踩的坑。
 *
 * 同源的第二个坑是后台进程：`&` / `nohup` / `setsid` 起出来的孙进程会继承本次运行的
 * 输出管道。不自行重定向的话，本次运行会在命令本体退出约 2 秒后收尾（服务端给命令行设了
 * WaitDelay），输出末尾留下一句「[命令已结束；仍有后台进程持有输出管道…]」，
 * 此后那个进程的输出面板再也收不到；进程本身则留在容器里继续跑——运行已经结束了，
 * 之后关窗或清除记录都不会再动它。只有【命令还在跑】的时候关窗或撞上超时，
 * 才会终止整个进程组。说明块里为此单列了一条提示。
 */
const visible = defineModel<boolean>('visible', { required: true })

const props = defineProps<{ isMobile: boolean }>()

const meta = ref<SystemConsoleMeta | null>(null)
const metaLoading = ref(false)
// 移动端默认折叠冗长的使用说明，给命令输入框和输出区留出可视高度；桌面默认展开
const noticeExpanded = ref(!props.isMobile)
const command = ref('')
const running = ref(false)
const exitCode = ref<number | null>(null)
/**
 * 本次运行的服务端状态：running / success / failed / stopped（server/handler/system_console.go
 * 里 debugRun.Status 的取值）。
 *
 * 【为什么要单独记，而不是结束那一刻取一次】
 * 手动停止走的是「PUT stop」+「再补取一次 logs」两步，结束点不止一个；
 * 而且每次轮询快照里本来就带 status，只在 done 那一帧取值会漏掉后到的那一份。
 * 逐次记下来，关窗再开、或停止后仍有在途请求回来时，标签都还是对的。
 */
const runStatus = ref('')
const errorText = ref('')

// 正在轮询的 run。停止/结束后清空，用来判断轮询要不要继续。
const activeRunId = ref('')
// 还没在服务端清除掉的 run（含已经跑完的）。关闭弹窗 / 组件卸载时要对它调 DELETE，
// 否则服务端会一直留着这次运行的输出缓冲。
let pendingCleanupRunId = ''
let pollTimer: ReturnType<typeof setInterval> | null = null

// 输出区按「行 + 块」增量渲染，与执行日志页同一套写法。
// 轮询接口每次回的是【全量 logs 数组】，所以这里自己记水位，只追加新增的那几行。
const outputBuffer = createTerminalLineBuffer()
const outputRevision = ref(0)
/**
 * 已经渲染到的【全局行号】水位，等于上一轮的 `discarded + logs.length`。
 *
 * 刻意不记「已渲染的数组下标」：服务端单次运行最多留 20 万行，触顶后会一次性丢掉
 * 最前面的 5 万行，logs[0] 的全局行号（即响应里的 discarded）随之整体左移。
 * 按下标记的话，「本轮既发生了截断、又新增了更多行」时 logs.length 反而比它大，
 * 既不触发重灌、切片下标又指到了别的全局行——实测每命中一次就静默吞掉近 5 万行。
 */
let renderedGlobalEnd = 0
const outputRef = ref<HTMLElement>()
// 命令输出的自动跟随：运行中上翻即暂停，滚回底部恢复
const outputFollow = useLogAutoFollow(outputRef)

// 渲染窗口封顶：默认只渲染最后 5000 行，避免 `find /` 这类命令把 DOM 撑爆
const RENDER_WINDOW_CHUNKS = 50
const MAX_RENDERED_LINES = RENDER_WINDOW_CHUNKS * TERMINAL_RENDER_CHUNK_SIZE
const outputExpanded = ref(false)
const renderWindow = computed(() => (outputExpanded.value ? 0 : RENDER_WINDOW_CHUNKS))

const hasOutput = computed(() => {
  void outputRevision.value
  return !outputBuffer.isEmpty
})
const renderChunks = computed(() => {
  void outputRevision.value
  return outputBuffer.visibleChunks(renderWindow.value)
})
const omittedLines = computed(() => {
  void outputRevision.value
  return outputBuffer.omittedLineCount(renderWindow.value)
})
const pendingHtml = computed(() => {
  void outputRevision.value
  return outputBuffer.pendingLineHtml()
})

const consoleAvailable = computed(() => meta.value?.available === true)
// 元数据还没回来时也先别放行：拿不到 available 就贸然提交，只会换来一次注定失败的请求
const canRun = computed(() => consoleAvailable.value && !metaLoading.value)

const packageManagerText = computed(() => meta.value?.package_manager || '未识别')
const timeoutText = computed(() => {
  const minutes = meta.value?.timeout_minutes
  return typeof minutes === 'number' && minutes > 0 ? `${minutes} 分钟` : '未知'
})

/**
 * 常用命令快捷键位。点了只填进输入框，不直接执行——这里跑的是真实系统命令，
 * 「点一下就装东西」的交互代价太大。
 * 编译工具链那条按探测到的包管理器给，探测不到就整条不显示（给了也是错的包名）。
 */
const quickCommands = computed<{ label: string; command: string }[]>(() => {
  const items: { label: string; command: string }[] = []
  const manager = meta.value?.package_manager || ''
  if (manager === 'apk') {
    items.push({ label: '安装编译工具链', command: 'apk add build-base linux-headers cmake' })
  } else if (manager === 'apt') {
    items.push({
      label: '安装编译工具链',
      command: 'apt-get update && apt-get install -y build-essential cmake',
    })
  } else if (manager === 'dnf' || manager === 'yum' || manager === 'microdnf') {
    items.push({ label: '安装编译工具链', command: `${manager} install -y gcc gcc-c++ make cmake` })
  } else if (manager === 'zypper') {
    items.push({ label: '安装编译工具链', command: 'zypper install -y gcc gcc-c++ make cmake' })
  }
  items.push({ label: '查看 pip 版本', command: 'python3 -m pip --version' })
  items.push({ label: '查看磁盘占用', command: 'df -h' })
  return items
})

// 占位示例跟着探测结果走：apt 环境里摆一条 apk 命令只会误导。
// 文案带换行，写在 JS 里而不是模板属性里，避免依赖 HTML 实体转义。
const commandPlaceholder = computed(() => {
  const example = quickCommands.value[0]?.command || 'df -h'
  return `例如：${example}\n（Ctrl + Enter 运行）`
})

function clearPollTimer() {
  if (pollTimer) {
    clearInterval(pollTimer)
    pollTimer = null
  }
}

function resetOutput() {
  outputBuffer.reset()
  renderedGlobalEnd = 0
  outputExpanded.value = false
  outputRevision.value++
}

/**
 * 把轮询回来的全量 logs 数组里【新增的那部分】灌进行缓冲。
 *
 * `discarded` 是 logs[0] 的全局行号（服务端已成块丢弃的行数），老服务端不下发时按 0 处理。
 * 全部下标一律由「全局行号 - discarded」换算，不去猜数组长度。
 */
function appendLogs(logs: string[], discarded = 0) {
  let start = renderedGlobalEnd - discarded
  if (start < 0) {
    // 我们还没渲染的行已经被服务端丢掉了，接不上，只能整份重灌。
    // 重灌后 logs[0] 那行「[前 N 行输出已省略：…]」会一起渲染出来，天然充当断层提示。
    outputBuffer.reset()
    // 水位跟着回落到 logs[0]，这样即使下面提前 return，缓冲和水位也仍然是自洽的
    renderedGlobalEnd = discarded
    start = 0
    outputRevision.value++
  }
  // 没有新增（含服务端还没吐出任何输出）就别动缓冲，省掉一次整块重渲染
  if (start >= logs.length) return

  const fresh = logs.slice(start)
  // 缓冲把最后一行留作「正在刷新的当前行」，所以缓冲里已经有内容时要先补一个换行把上一行收尾。
  // 这里必须问缓冲自己而不是看 start > 0：截断刚好把已渲染的部分整块吃掉时（discarded 恰等于
  // renderedGlobalEnd）start 会是 0，但缓冲里还留着上一轮的内容，不补换行就会把两行粘在一起。
  outputBuffer.append((outputBuffer.isEmpty ? '' : '\n') + fresh.join('\n'))
  renderedGlobalEnd = discarded + logs.length
  outputRevision.value++

  // 跟随中贴到最新，暂停时什么都不做（由 useLogAutoFollow 判定）
  outputFollow.onContentChange()
}

function expandOutputWindow() {
  outputExpanded.value = true
  outputRevision.value++
}

async function loadMeta() {
  metaLoading.value = true
  try {
    const res = await systemConsoleApi.meta()
    // 服务端可能把内容包在 data 里（与脚本调试的轮询接口同形），两种都收下
    meta.value = ((res as any)?.data ?? res) as SystemConsoleMeta
  } catch (err: any) {
    meta.value = {
      available: false,
      unavailable_reason:
        err?.response?.data?.error || '获取系统命令行信息失败，请确认面板版本与当前账号权限',
    }
  } finally {
    metaLoading.value = false
  }
}

/** 结束一次轮询：停表 + 记录退出码与状态。done 之后 run 仍留在服务端，等关闭时统一 DELETE */
function finishRun(payload: SystemConsoleRunLogs) {
  running.value = false
  activeRunId.value = ''
  clearPollTimer()
  // 运行结束：冻结跟随态（此前那一次 appendLogs 已按跟随态贴过底）
  outputFollow.end()
  exitCode.value = payload.exit_code ?? null
  if (payload.status) runStatus.value = payload.status
  if (payload.status === 'failed' && !errorText.value) {
    errorText.value =
      payload.exit_code === undefined ? '命令执行失败' : `命令执行失败，退出码 ${payload.exit_code}`
  }
}

function pollLogs() {
  clearPollTimer()
  pollTimer = setInterval(async () => {
    const runId = activeRunId.value
    if (!runId) {
      clearPollTimer()
      return
    }
    try {
      const res = await systemConsoleApi.logs(runId)
      // 请求发出后用户可能已经点了停止、或者直接关了弹窗（两处都会把 activeRunId 清空）。
      // 这份快照已经过期，再写回去会把「已停止」倒退成上一轮的「运行中」。
      if (activeRunId.value !== runId) return
      const payload = ((res as any)?.data ?? res) as SystemConsoleRunLogs
      appendLogs(payload.logs || [], payload.discarded ?? 0)
      if (payload.status) runStatus.value = payload.status
      if (payload.done) {
        finishRun(payload)
      }
    } catch (err: any) {
      // 同上：停止 / 关窗之后 run 已被 DELETE 掉，这里必然 404，不该当成真错误弹给用户
      if (activeRunId.value !== runId) return
      running.value = false
      activeRunId.value = ''
      clearPollTimer()
      errorText.value = err?.response?.data?.error || '获取命令输出失败，请检查面板服务状态'
    }
  }, 500)
}

/** 清除上一次残留在服务端的 run（不阻塞当前操作，失败也无所谓：服务端本来就会自己回收） */
function cleanupPendingRun() {
  const runId = pendingCleanupRunId
  pendingCleanupRunId = ''
  if (!runId) return
  void systemConsoleApi.clear(runId).catch(() => {})
}

async function handleRun() {
  if (!canRun.value || running.value) return
  const text = command.value.trim()
  if (!text) {
    ElMessage.warning('请输入要执行的命令')
    return
  }

  // 上一次的 run 还挂在服务端就先清掉，避免连续运行时越攒越多
  cleanupPendingRun()
  resetOutput()
  errorText.value = ''
  exitCode.value = null
  runStatus.value = ''
  running.value = true

  try {
    const res = await systemConsoleApi.run(text)
    const runId = res?.run_id || ''
    if (!runId) {
      throw new Error('服务端未返回 run_id')
    }
    activeRunId.value = runId
    pendingCleanupRunId = runId
    // 命令开始运行：开启自动跟随
    outputFollow.begin(true)
    pollLogs()
  } catch (err: any) {
    running.value = false
    activeRunId.value = ''
    errorText.value = err?.response?.data?.error || err?.message || '命令提交失败'
    ElMessage.error(errorText.value)
  }
}

async function handleStop() {
  const runId = activeRunId.value
  if (!runId) return
  clearPollTimer()
  running.value = false
  activeRunId.value = ''
  // 先落一个 stopped：下面两个请求任意一个失败，标签也不会卡在「运行中」，
  // 更不会退回按退出码渲染（服务端停止时把 ExitCode 写死成 -1）。
  runStatus.value = 'stopped'
  try {
    await systemConsoleApi.stop(runId)
  } catch {
    // 停止失败通常是进程已经自己结束了，不打扰用户
  }
  // 停止后再补取一次输出，把进程被杀之前那几行也拿回来
  try {
    const res = await systemConsoleApi.logs(runId)
    const payload = ((res as any)?.data ?? res) as SystemConsoleRunLogs
    appendLogs(payload.logs || [], payload.discarded ?? 0)
    exitCode.value = payload.exit_code ?? null
    // 服务端的 stopConsoleRun 对已经结束的 run 不会改写状态，所以这里可能拿回 success /
    // failed —— 那是进程在点停止之前就自己跑完了，按真实结果显示比一律「已停止」更准。
    if (payload.status) runStatus.value = payload.status
  } catch {
    // 取不到就算了，用户已经看到停止前的输出，标签保持上面落的「已停止」
  } finally {
    // 停止后冻结跟随态（上面补取的那一次 appendLogs 已按跟随态贴过底）
    outputFollow.end()
  }
}

function useQuickCommand(text: string) {
  // 只填进输入框，由用户自己确认后再运行
  command.value = text
}

/** 关闭弹窗 / 组件卸载时的统一收尾：停表 + 清除服务端 run */
function teardown() {
  clearPollTimer()
  activeRunId.value = ''
  running.value = false
  // 关闭弹窗 / 卸载：冻结跟随态
  outputFollow.end()
  // DELETE 会把还在跑的进程一并杀掉（服务端 Clear 里带 killIfRunning），
  // 所以这里不用再单独发一次 stop——两个请求并发过去，后到的那个只会拿到 404。
  cleanupPendingRun()
}

watch(visible, (val) => {
  if (val) {
    // 每次打开按当前端重置说明折叠态：移动端默认折叠腾出输入与输出空间，桌面默认展开
    noticeExpanded.value = !props.isMobile
    // 上一次的输出留到下次打开会让人以为是刚跑出来的，开窗即清空；
    // 命令文本刻意保留，用户多半是想改一改再跑一次。
    resetOutput()
    errorText.value = ''
    exitCode.value = null
    runStatus.value = ''
    // 每次打开都重新拉一次元数据：包管理器、是否 root、超时时间都可能在两次打开之间变了
    void loadMeta()
    return
  }
  teardown()
})

watch(
  () => props.isMobile,
  (mobile) => {
    noticeExpanded.value = !mobile
  }
)

onBeforeUnmount(() => {
  teardown()
})
</script>

<template>
  <el-dialog
    v-model="visible"
    title="系统命令行"
    fullscreen
    class="system-console-dialog"
    :close-on-click-modal="false"
    destroy-on-close
  >
    <div class="console-container" :class="{ mobile: props.isMobile }">
      <div class="console-input-panel">
        <div class="panel-header">
          <el-icon><Monitor /></el-icon>
          <span>命令</span>
          <!-- 环境探测标签整组挂在 available 上。available=false 时（Windows 裸机、
               演示站）后端不会给出可信的 is_root / package_manager / timeout_minutes，
               照渲染会得到一个「非 root · 包管理器 未识别 · 超时 未知」的表头——
               尤其「非 root」会被读成「命令行能用，只是权限不够」，与实际不符。
               此时只留一个「不可用」，具体原因由下面的红色 alert 原样展示。 -->
          <template v-if="consoleAvailable">
            <el-tag v-if="meta?.is_root" size="small" type="success" effect="plain">root</el-tag>
            <el-tag v-else size="small" type="warning" effect="plain">非 root</el-tag>
            <el-tag size="small" type="info" effect="plain">包管理器 {{ packageManagerText }}</el-tag>
            <el-tag size="small" type="info" effect="plain">超时 {{ timeoutText }}</el-tag>
          </template>
          <el-tag v-else-if="meta" size="small" type="danger" effect="plain">不可用</el-tag>
        </div>
        <div class="panel-content console-input-content" v-loading="metaLoading">
          <el-alert
            v-if="meta && !consoleAvailable"
            class="console-notice"
            type="error"
            :closable="false"
            show-icon
            :title="meta.unavailable_reason || '当前环境不支持系统命令行'"
          />
          <el-alert v-else class="console-notice" :class="{ 'is-collapsed': !noticeExpanded }" type="info" :closable="false" show-icon>
            <div class="console-notice__summary">
              <span>非交互式终端：单次独立执行，不保留 <code>cd</code> 与环境变量</span>
              <button
                type="button"
                class="console-notice__toggle"
                :aria-expanded="noticeExpanded"
                @click="noticeExpanded = !noticeExpanded"
              >
                {{ noticeExpanded ? '收起说明' : '展开说明' }}
              </button>
            </div>
            <div v-if="noticeExpanded" class="console-notice__details">
              <div class="console-notice__line">
                一次只执行一条命令（可以写多行，但 <code>cd /x &amp;&amp; ls</code> 这种复合写法才连得起来）。
                这不是交互式终端，<b>不保留 cd 和环境变量</b>，下一条命令又是全新的进程。
              </div>
              <div class="console-notice__line">
                工作目录 <code>{{ meta?.work_dir || '面板脚本目录' }}</code>
                <span v-if="meta?.shell">，Shell <code>{{ meta.shell }}</code></span>
                <span v-if="meta?.distribution">，发行版 {{ meta.distribution }}</span>。
                单条命令超过 {{ timeoutText }} 会被强制结束。
              </div>
              <div class="console-notice__line">
                要放到后台的进程请<b>自行重定向输出</b>（<code>setsid nohup xxx &gt;/dev/null 2&gt;&amp;1 &amp;</code>），
                否则本次运行会在命令本体退出约 2 秒后自行收尾，末尾只留一句「仍有后台进程持有输出管道」，
                <b>之后它的输出面板再也收不到</b>。运行结束后再关闭弹窗或清除记录都不会杀掉它，
                后台进程会留在容器里；只有<b>命令还在运行时</b>关窗或撞上超时，才会终止<b>整个进程组</b>。
                真要留常驻进程，也请这样用 <code>setsid</code> 启动：定时 / 手动任务结束时会清理留在进程组里的后台进程（开机任务除外），setsid 起的不受影响。
              </div>
            </div>
          </el-alert>

          <div class="console-quick">
            <span class="console-quick__label">常用命令</span>
            <el-button
              v-for="item in quickCommands"
              :key="item.command"
              size="small"
              plain
              :disabled="!canRun"
              @click="useQuickCommand(item.command)"
            >
              {{ item.label }}
            </el-button>
          </div>

          <el-input
            v-model="command"
            class="console-command-input"
            type="textarea"
            resize="none"
            :disabled="!canRun"
            :placeholder="commandPlaceholder"
            @keydown.ctrl.enter.prevent="handleRun"
            @keydown.meta.enter.prevent="handleRun"
          />
        </div>
      </div>

      <div class="console-log-panel">
        <div class="panel-header">
          <el-icon><Tickets /></el-icon>
          <span>命令输出</span>
          <el-tag v-if="running" type="warning" size="small" effect="plain">运行中</el-tag>
          <!-- 手动停止必须单独一档、且不显示退出码：服务端 stopConsoleRun 把 ExitCode
               写死成 -1、status 置为 stopped（server/handler/system_console.go）。
               照退出码渲染的话，用户点了「停止」看到的是一个红色的「退出码 -1」——
               诚实，但看着像执行出错了。这里给中性的「已停止」。
               success / failed 仍按退出码走：failed 要能看到真实退出码（例如 exit 7）。 -->
          <el-tag v-else-if="runStatus === 'stopped'" type="info" size="small" effect="plain">
            已停止
          </el-tag>
          <el-tag
            v-else-if="exitCode !== null"
            :type="exitCode === 0 ? 'success' : 'danger'"
            size="small"
            effect="plain"
          >
            退出码 {{ exitCode }}
          </el-tag>
        </div>
        <div ref="outputRef" class="panel-content console-log-content dd-log-surface">
          <div v-if="errorText" class="console-error">
            <el-alert type="error" :title="errorText" :closable="false" show-icon />
          </div>
          <div v-if="hasOutput" class="console-logs">
            <!-- 文字必须与标签写在同一行：这一块是 white-space: pre-wrap 的日志区，
                 换行缩进会被原样渲染成按钮里的空白（与执行日志页同一处理）。 -->
            <button
              v-if="omittedLines > 0"
              type="button"
              class="console-omitted-notice"
              @click="expandOutputWindow"
            >已省略前 {{ omittedLines }} 行（默认只渲染最后 {{ MAX_RENDERED_LINES }} 行）· 点击展开</button>
            <span v-for="chunk in renderChunks" :key="chunk.key" v-html="chunk.html"></span>
            <span v-html="pendingHtml"></span>
          </div>
          <el-empty
            v-else-if="!errorText"
            description="输入命令后点击运行"
            :image-size="80"
          />
        </div>
      </div>
    </div>

    <template #footer>
      <el-button v-if="running" type="danger" @click="handleStop">
        <el-icon><VideoPause /></el-icon>停止
      </el-button>
      <el-button v-else-if="hasOutput || errorText" type="primary" :disabled="!canRun" @click="handleRun">
        <el-icon><RefreshRight /></el-icon>再次运行
      </el-button>
      <el-button v-else type="primary" :disabled="!canRun" @click="handleRun">
        <el-icon><VideoPlay /></el-icon>运行
      </el-button>
      <el-button @click="visible = false">关闭</el-button>
    </template>
  </el-dialog>
</template>

<style scoped lang="scss">
.console-container {
  display: flex;
  gap: 16px;
  height: 100%;
  min-height: 0;
  max-height: none;
  min-width: 0;
}

.console-input-panel,
.console-log-panel {
  flex: 1;
  display: flex;
  flex-direction: column;
  min-width: 0;
  min-height: 0;
  border: 1px solid var(--el-border-color-light);
  // 输入区 / 输出区都是独立成块、带边框的容器类表面 → surface 档；
  // overflow: hidden 把内部贴边铺满的 header / 日志区裁成同样的角
  border-radius: var(--dd-radius-surface);
  overflow: hidden;
  background: var(--el-bg-color);
}

.panel-header {
  padding: 12px 16px;
  background: var(--el-fill-color-light);
  border-bottom: 1px solid var(--el-border-color-light);
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
  font-weight: 600;
  font-size: 14px;
  flex-shrink: 0;
}

.panel-content {
  flex: 1;
  min-height: 0;
  overflow: auto;
  padding: 16px;
  display: flex;
  flex-direction: column;
}

.console-input-content {
  gap: 12px;
}

.console-notice {
  flex-shrink: 0;
  padding: 8px 12px;

  :deep(.el-alert__content) {
    width: 100%;
    min-width: 0;
  }
}

.console-notice__summary {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  flex-wrap: wrap;
  font-size: 12.5px;
  line-height: 1.5;
}

.console-notice__toggle {
  border: 0;
  padding: 0;
  background: transparent;
  color: var(--dd-accent-ink);
  font-size: 12px;
  font-weight: 600;
  cursor: pointer;
  white-space: nowrap;

  &:hover {
    text-decoration: underline;
  }
}

.console-notice__details {
  margin-top: 6px;
  padding-top: 6px;
  border-top: 1px dashed var(--dd-divider, var(--el-border-color-lighter));
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.console-notice__line {
  font-size: 12.5px;
  line-height: 1.55;
}

.console-quick {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 8px;
  flex-shrink: 0;

  // EP 自带 `.el-button + .el-button { margin-left: 12px }`，与这里的 gap 叠加会越排越散
  :deep(.el-button + .el-button) {
    margin-left: 0;
  }
}

.console-quick__label {
  font-size: 12px;
  color: var(--el-text-color-secondary);
}

.console-command-input {
  flex: 1;
  min-height: 0;

  :deep(.el-textarea__inner) {
    height: 100%;
    font-family: var(--dd-font-mono);
    font-size: 13px;
    line-height: 1.6;
  }
}

.console-log-content.dd-log-surface {
  // 外层面板已经有边框和圆角，这里去掉重复的边框/圆角，避免两条同心圆角叠出粗边
  border: 0;
  border-radius: 0;
  box-shadow: none;
}

.console-error {
  margin-bottom: 12px;
  flex-shrink: 0;
}

.console-logs {
  font-family: var(--dd-font-mono);
  font-size: 13px;
  line-height: 1.6;
  white-space: pre-wrap;
  word-break: break-all;
  // 必须用 --dd-log-text-color：日志底色来自 log_background_color，
  // 与面板明暗主题是两套独立取值，写面板文字色会出现「深灰字压在深色日志底上」。
  color: var(--dd-log-text-color, var(--el-text-color-primary));
  flex: 1;
}

.console-omitted-notice {
  display: block;
  width: 100%;
  margin-bottom: 8px;
  padding: 4px 8px;
  border: 1px dashed var(--el-border-color);
  border-radius: var(--dd-radius-control);
  background: transparent;
  color: inherit;
  font-family: inherit;
  font-size: 12px;
  text-align: left;
  cursor: pointer;
  opacity: 0.75;
}

.console-container.mobile {
  flex-direction: column;
  // 移动端外层 .el-dialog__body 已开启 overflow-y: auto，容器允许按内容自然增高，
  // 防止软键盘弹出压缩视口时把输入框压塌或把输出区裁切掉。
  height: auto;
  min-height: 100%;

  .console-input-panel {
    flex: 0 0 auto;
    min-height: 0;
  }

  .console-log-panel {
    flex: 1 0 220px;
    min-height: 220px;
  }

  .panel-content {
    padding: 8px;
  }

  .console-command-input {
    min-height: 120px;
    flex-shrink: 0;

    :deep(.el-textarea__inner) {
      min-height: 120px;
    }
  }
}
</style>

<style lang="scss">
/*
  el-dialog 默认原地渲染、不 teleport（EP 2.13 的 append-to-body 默认 false），
  但 .el-dialog 及其 header / body / footer 是 EP 内部组件渲染的节点，带不上本组件的 scopeId，
  scoped 选择器命中不到它们。所以这里用唯一 class 写一个非 scoped 块，
  让全屏工作区在桌面端和移动端都吃满可用高度。写法与脚本页的 .script-execution-fullscreen-dialog 保持一致。
*/
.system-console-dialog {
  display: flex;
  flex-direction: column;

  .el-dialog__header {
    flex-shrink: 0;
    padding: 14px 18px;
    margin: 0;
    border-bottom: 1px solid var(--el-border-color-lighter);
  }

  .el-dialog__body {
    flex: 1 1 0;
    min-height: 0;
    padding: 14px 18px;
    overflow: hidden;
  }

  .el-dialog__footer {
    flex-shrink: 0;
    padding: 12px 18px;
    border-top: 1px solid var(--el-border-color-lighter);
  }
}

@media (max-width: 768px) {
  // 移动端允许弹窗正文纵向滚动，避免软键盘弹起后压塌命令输入框或裁切输出区
  .system-console-dialog .el-dialog__body {
    overflow-y: auto;
  }
}
</style>
