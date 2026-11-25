<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from 'vue'
import { ElMessage } from 'element-plus'
import {
  Check,
  Close,
  DocumentCopy,
  Download,
  InfoFilled,
  Loading,
  Operation,
  Switch,
  Warning,
} from '@element-plus/icons-vue'
import { taskApi } from '@/api/task'
import { openAuthorizedEventStream, type EventStreamConnection } from '@/utils/sse'
import { useResponsive } from '@/composables/useResponsive'
import { useLogAutoFollow } from '@/composables/useLogAutoFollow'
import { readListPreference } from '@/utils/listPreferences'
import { createTerminalLineBuffer, TERMINAL_RENDER_CHUNK_SIZE } from '@/utils/ansi'

const props = defineProps<{
  visible: boolean
  taskId: number | null
  taskName: string
  mode?: 'live' | 'latest'
}>()

const emit = defineEmits<{
  'update:visible': [value: boolean]
}>()

// #133：日志自动跟随改为「最新一行在可视区就跟随、用户上翻即暂停」的青龙式判定，
// 由 useLogAutoFollow 统一承担，原来的「跟随」开关与其持久化偏好已删除。
// 旧的持久化键清一次，避免留下无用的孤儿键（历史值不再读取）。
try {
  if (typeof window !== 'undefined') {
    window.localStorage.removeItem('dd:tasks:log_follow')
  }
} catch {
  // 隐私模式 / 存储不可用：清不清都不影响本次查看，静默忽略
}

// 日志正文改成「按行 + 按块」增量渲染。
// 历史行一旦落定就不会再变，块 HTML 只解析一次并缓存；
// 每次 SSE flush 只需重算「最后一个未写满的块」和「正在刷新的当前行」，
// 不再像以前那样每帧对整份日志重跑 ansiToHtml + 整块 v-html 替换。
const logBuffer = createTerminalLineBuffer()
// 行缓冲本身是普通对象，用这个计数器把它的变更接进 Vue 的响应式
const logRevision = ref(0)
const done = ref(false)
const error = ref<string | null>(null)
const emptyMessage = ref<string | null>(null)
// 「日志还没出现，但任务确实在排队/运行中」这一态。issue #115：这段时间 task_logs 里一行都没有，
// 直接报「该任务还没有日志记录」是谎报，要用等待文案 + 继续轮询，见 keepWaitingForPendingTask。
const waitingForLog = ref(false)
// 空态下 emptyMessage 底下那行小字。以前它是写死的「任务执行一次后就会出现日志」，
// 于是「日志已过期，文件已被清理」「日志已按保留策略清理」这些明明跑过的情况，
// 底下也跟着说「执行一次后就会出现」——同一块区域里两句话自相矛盾。
// 现在跟着 emptyMessage 一起设，语义对不上就留空。
const emptyHint = ref('')
// 「从来没跑过」时的那句引导，也是后端 latest-log 在这种情况下回的原话（server/handler/task_logs.go）。
const NEVER_RAN_MESSAGE = '该任务还没有日志记录'
const NEVER_RAN_HINT = '任务执行一次后就会出现日志'
const loading = ref(false)
const logContainerRef = ref<HTMLElement>()
// 日志自动跟随：运行态由日志流自己判定，容器随 destroy-on-close 弹窗重建时组合式会自动重挂监听
const follow = useLogAutoFollow(logContainerRef)
// 供模板读取的跟随态（顶层 ref 在模板里会自动解包）
const isFollowing = follow.following
const fontSize = ref<'sm' | 'md' | 'lg'>('md')
const wrap = ref(true)
// 渲染窗口封顶：默认只渲染最后 5000 行，避免超长日志把 DOM 撑到几十万节点，
// 让每次滚动/布局都变慢。用户可以点顶部提示展开完整日志。
const RENDER_WINDOW_CHUNKS = 50
const MAX_RENDERED_LINES = RENDER_WINDOW_CHUNKS * TERMINAL_RENDER_CHUNK_SIZE
const renderWindowExpanded = ref(false)
const { dialogFullscreen } = useResponsive()
let eventSource: EventStreamConnection | null = null
let pendingSseChunks: string[] = []
let logFlushTimer: ReturnType<typeof setTimeout> | null = null
let reconnectTimer: ReturnType<typeof setTimeout> | null = null
let hiddenAt: number | null = null
let pendingScrollRestore: number | null = null
// latest-log 预取：startStream() 里与 SSE 同时发起，done 分支直接消费这个已经在飞的 promise。
// 以前是等 done 事件到了才开始第二个往返（JWT + 查库 + 解压 + 全量 ANSI 解析），
// 在弱机上这段串行开销肉眼可见（issue #109-1 的后半段）。这里只把请求提前，语义完全不变。
let prefetchedLatestLog: Promise<any> | null = null
let prefetchedLatestLogTaskId: number | null = null
let prefetchedLatestLogAt = 0
// 作废预取时用它掐断在途响应体，见 dropPrefetchedLatestLog
let prefetchedLatestLogAbort: AbortController | null = null
// 预取的有效期。超过这个窗口就当它过期、老老实实重发一次请求。
//
// 【预取只在一条路径上可以被复用】
// 预取拿到的是「打开弹窗那一刻」的最近一次日志。只有「任务早就跑完了、服务端立刻回 done、
// 流里一个字节都没有」这条路径上，done 与预取几乎同时发生、两者内容必然一致——
// 这正是本次要优化的场景（issue #109-1 的后半段）。
// 其余任何情况下那份快照都比屏幕上的内容旧，复用它会把刚跑完的日志覆盖成旧的甚至空的。
//
// 🔴 挡住误用的**主**手段是 dropPrefetchedLatestLog()：流里一收到真实数据、或收到
// done:reconnect，立刻作废预取。**不能只靠下面这条 TTL** —— 任务在 1.2s 内跑完时
// done 到达时预取仍在有效期内；done:reconnect 更可能 150ms 就回来（服务端一发现
// TinyLog 就 break，并不会等满轮询窗口）。TTL 只是「谁都没作废它、它却放了很久」的兜底。
const LATEST_LOG_PREFETCH_TTL = 1200
// reconnect 风暴熔断：连续重连且无新数据时累加，超过上限即按完成处理，避免无限重连+全量重渲染卡顿。
// done:reconnect 与实时流出错后的重连共用这一个计数（见 scheduleStreamReconnect）。
let reconnectAttempts = 0
const MAX_RECONNECT_ATTEMPTS = 5

// 任务状态取值，与后端 model.TaskStatus* 对齐：0=已禁用 / 0.5=排队中 / 1=已启用 / 2=运行中
const TASK_STATUS_QUEUED = 0.5
const TASK_STATUS_RUNNING = 2
// 「等日志出现」的轮询上限：1s 一次、最多 60 次（约 1 分钟）。
// 随机延迟会走延迟入队、并发数被占满时任务也会一直停在「排队中」，这段窗口必须留够；
// 但任务可能长期卡在排队中，所以要有这个兜底，不能无限轮询。
const WAITING_POLL_LIMIT = 60
let waitingPollCount = 0
// C-log-follow-4 ②：等待链（keepWaitingForPendingTask / 排队轮询 pollPendingTaskUntilRunning）查到任务已在运行、
// 从轮询切回实时 SSE 之后置 true；流里收到真实数据、或重新打开 / 切换任务起新会话时复位。
// 作用是「一次等待里只切一次」，防的是「日志行已建、日志文件还没写出第一行」这个窗口：
// 没有 TinyLog 的运行任务（conc / SuppressLiveOutput）切回流后，服务端运行中分支要空等约 60s 才回 done:finished
// （server/handler/log.go）；收口时 latest-log 回退读日志文件，文件还是空的就又落回等待链，不设闸会每轮再切一次、再空等 60s。
// 文件写出第一行后（执行器一开跑就写「=== 开始执行 ===」），latest-log 拿得到正文、渲染后不再进等待链，这道闸就用不上了。
let waitingSwitchedToStream = false

// 等待链此刻能不能切回实时 SSE（C-log-follow-4 ②）：
// - latest 模式本来就是一次性拉「最近结果」、后面没有 SSE，不能被等待链悄悄变成实时流；
// - reconnect 熔断已跳闸（连续空重连超过上限）：那边已判定为无可续流的实时日志，再开流就是把风暴请回来；
// - 这次等待已经切过一次、切回去一条数据都没来（见 waitingSwitchedToStream）。
function canSwitchWaitingToStream() {
  return props.mode !== 'latest'
    && reconnectAttempts <= MAX_RECONNECT_ATTEMPTS
    && !waitingSwitchedToStream
}
// 组件卸载标记：等待轮询中间夹着一次 await（查任务状态），
// 卸载/关闭正好落在这一拍时，后面绝不能再把新的定时器挂上去。
let disposed = false
// 每次 cleanup（关窗、切任务、重开流、改走一次性拉取、卸载）都换一代。
// 实时流出错后的异步收尾（recoverStreamAfterError）靠它认出：await 期间别的路径已经接手，自己该作废了。
let streamGeneration = 0
// 运行态是否已定性。首个信号（首包 / done:reconnect / 等待态查到运行中）到来时置 true，
// 只认定一次——认定后用户的暂停/跟随选择由 useLogAutoFollow 维护，不能每来一条消息就重新贴底。
// 用 ref 是因为「已暂停跟随」弱提示要靠它把「已判定为运行中」这一态接进模板：
// 未判定时（流刚开、正文还空）headerState 也是 'running'，不 gate 的话提示会在每次打开时闪一下。
const runStateDecided = ref(false)

// 下面这批 computed 都要跟着行缓冲走，读一下 logRevision 建立依赖即可
const activeRenderWindow = computed(() => renderWindowExpanded.value ? 0 : RENDER_WINDOW_CHUNKS)

const hasLogs = computed(() => {
  void logRevision.value
  return !logBuffer.isEmpty
})

const renderChunks = computed(() => {
  void logRevision.value
  return logBuffer.visibleChunks(activeRenderWindow.value)
})

const omittedLineCount = computed(() => {
  void logRevision.value
  return logBuffer.omittedLineCount(activeRenderWindow.value)
})

const pendingLineHtml = computed(() => {
  void logRevision.value
  return logBuffer.pendingLineHtml()
})

const lineCount = computed(() => {
  void logRevision.value
  return logBuffer.displayLineCount
})

const byteLabel = computed(() => {
  void logRevision.value
  const bytes = logBuffer.byteLength
  if (bytes === 0) return ''
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`
})

const fontSizeClass = computed(() => `log-font-${fontSize.value}`)

// 头部状态灯、头部徽标、底部状态栏共用这一个状态口径，避免三处各写一串条件后彼此打架。
// waiting 是本轮新增的一态（issue #115）：日志还没落库，但任务确实在排队/运行中，不能显示成「无日志」。
// error 只会在 done 之后被写入（见 fetchLatestLog），所以把它排在 running 前面不改变原有观感。
const headerState = computed<'error' | 'waiting' | 'empty' | 'done' | 'running'>(() => {
  if (error.value) return 'error'
  if (waitingForLog.value) return 'waiting'
  if (emptyMessage.value) return 'empty'
  return done.value ? 'done' : 'running'
})

function expandRenderWindow() {
  const container = logContainerRef.value
  const previousHeight = container?.scrollHeight ?? 0
  const previousTop = container?.scrollTop ?? 0
  renderWindowExpanded.value = true
  void nextTick(() => {
    const el = logContainerRef.value
    if (!el) return
    // 补齐的内容是往上长的，按高度差补偿滚动位置，避免视口整个跳走。
    // 走 follow.setScrollTop 带上程序标记，避免这次写入被自动跟随误读成用户上翻。
    follow.setScrollTop(previousTop + (el.scrollHeight - previousHeight))
  })
}

watch(() => props.visible, (visible) => {
  if (visible && props.taskId) {
    if (props.mode === 'latest') {
      void loadLatestOnly()
    } else {
      void startStream()
    }
  } else {
    cleanup()
  }
})

watch(() => props.taskId, (taskId, previousTaskId) => {
  if (props.visible && taskId && taskId !== previousTaskId) {
    if (props.mode === 'latest') {
      void loadLatestOnly()
    } else {
      void startStream()
    }
  }
})

// 运行态由日志流自己判定，不新增 prop、不额外请求：收到首包（或 done:reconnect / 等待态查到运行中）
// 才认定任务在跑并开启跟随，且只认定一次。
function markRunning() {
  if (runStateDecided.value) return
  runStateDecided.value = true
  follow.begin(true)
}

// C-log-follow-4：finished-late / 排队态修复（只动前端）。
// finished-late 说明打开弹窗时任务还在排队/启动，此刻按 started_at 取到的很可能是【上一次运行】的日志。
// 这里查一次真实状态：仍在排队就继续等、真正开跑就切回实时 SSE、确实结束了才回落到渲染最近一次日志。
// 另一个入口是 keepWaitingForPendingTask 查到排队中之后的下一拍（#115 等待链，C-log-follow-4 ②）；
// 实时流出错后查到排队中（recoverStreamAfterError）则从 waitQueuedTaskThenPoll 这一步直接进链。
async function pollPendingTaskUntilRunning(scrollMode: 'top' | 'bottom' | 'preserve') {
  const taskId = props.taskId
  // 进链时的代号，作用同 recoverStreamAfterError：一次正常等待（排队→排队→开跑）里没有任何路径会换代——
  // 换代只发生在 cleanup()，而这条链每两步之间只有一个 setTimeout，中途不 cleanup；
  // 切回实时流的 startStream(true) 也排在下面那次检查之后。所以代号一变就只可能是别的路径已经接手
  // （关窗后重开同一个任务、切回前台重开流），这条旧链必须就地作废，
  // 否则它会对新会话 startStream(true) 或再挂一条排队轮询。
  const generation = streamGeneration
  if (disposed || !props.visible || !taskId) {
    return
  }
  let status: number | null = null
  try {
    const live = await taskApi.liveLogs(taskId)
    status = typeof live?.status === 'number' ? live.status : null
  } catch {
    // 状态查不到就不猜，直接退回渲染最近一次日志
  }
  // await 这一拍里用户可能关掉弹窗、切了任务、组件被卸载，或者别的路径已经接手（见上面的代号）
  if (disposed || !props.visible || props.taskId !== taskId || generation !== streamGeneration) {
    return
  }
  if (status === TASK_STATUS_RUNNING) {
    // 已经真正开跑：先开启跟随再切回实时流。startStream(true) 会 begin(true, keepFollowing)，
    // 服务端命中 TinyLog 后先推历史再实时推送，#133 的跟随随之生效。
    // 记下「这次等待已切过一次流」：切回去若一条数据都没来又落回等待链，keepWaitingForPendingTask 就改回轮询
    waitingSwitchedToStream = true
    waitingForLog.value = false
    follow.begin(true)
    void startStream(true)
    return
  }
  if (status === TASK_STATUS_QUEUED) {
    waitQueuedTaskThenPoll(scrollMode)
    return
  }
  // 不再排队/运行、或查不到状态：回落到按最近一次日志渲染（与旧行为一致）
  waitingForLog.value = false
  follow.end()
  void fetchLatestLog(0, follow.following.value ? 'bottom' : 'preserve')
}

// 等待链的「排队中」这一步：显示排队文案，1s 后再查一次状态，到兜底上限就停。
// pollPendingTaskUntilRunning 查到排队中时走这里；recoverStreamAfterError 刚查过、已知是排队中，也直接从这里进链——
// 再绕一趟 pollPendingTaskUntilRunning 会多发一次 live-logs，那一个往返里头部还会先闪一下「已完成」。
function waitQueuedTaskThenPoll(scrollMode: 'top' | 'bottom' | 'preserve') {
  if (waitingPollCount < WAITING_POLL_LIMIT) {
    waitingPollCount++
    waitingForLog.value = true
    emptyMessage.value = '任务排队中，开始执行后会出现日志…'
    emptyHint.value = '正在等待日志写入，出现后会自动显示，不用手动重开'
    // 与等待轮询共用 reconnectTimer，切后台再回来时会另起一条链，不掐掉旧的会叠出多条并行轮询
    if (reconnectTimer !== null) {
      clearTimeout(reconnectTimer)
    }
    reconnectTimer = setTimeout(() => {
      reconnectTimer = null
      void pollPendingTaskUntilRunning(scrollMode)
    }, 1000)
    return
  }
  // 到达兜底上限、任务仍在排队：与 keepWaitingForPendingTask 的上限口径一致，停轮询并如实说明。
  // 不能回落到渲染最近一次日志——此刻取到的必然是【上一次运行】的，拿它冒充本次结果正是这里要修的问题。
  waitingForLog.value = false
  follow.end()
  emptyMessage.value = '还没有产生日志（任务一直停在排队中）'
  emptyHint.value = '已停止自动刷新，重新打开这个窗口可以继续等'
}

async function startStream(isReconnect = false) {
  // 上一次非跟随的重连一条数据都没等到又要重连（实时流出错后的连续重连、切后台两次）时，
  // 正文已被清空、scrollTop 已被夹到 0，要沿用那次还没用上的恢复位置，否则已暂停的用户会被送回顶部。
  // pendingScrollRestore 非空只可能是这种情况：它只在非跟随的重连里写入、在第一次 flush 数据时消费。
  const savedScrollTop = isReconnect
    ? (pendingScrollRestore ?? logContainerRef.value?.scrollTop ?? null)
    : null
  // 重连前先记住当前跟随态：resetLogOutput 会清空容器、scrollTop 被夹到 0，
  // 那一下非程序性的 scroll 会把已暂停的用户误判回跟随，所以必须显式保住（见 useLogAutoFollow 的重置风险）。
  const wasFollowing = isReconnect ? follow.following.value : false
  cleanup()
  resetLogOutput()
  done.value = false
  error.value = null
  emptyMessage.value = null
  emptyHint.value = ''
  waitingForLog.value = false
  loading.value = !isReconnect
  // 只有「非跟随的重连」才需要恢复原滚动位置；跟随中的重连直接贴到最新即可。
  pendingScrollRestore = isReconnect && !wasFollowing && savedScrollTop !== null ? savedScrollTop : null
  if (!isReconnect) {
    // 用户主动打开/切换任务重新起流：清零重连计数，确保熔断只针对一次会话内的连续空重连。
    reconnectAttempts = 0
    // 等日志的轮询次数同理，只在一次会话内累计
    waitingPollCount = 0
    waitingSwitchedToStream = false
    // 运行态先「未判定」：正文此刻为空，先停在顶部，等收到第一个信号再定性为运行/未运行。
    // 用 begin(false) 而不是 end()：begin(false) 会把 following 明确重置为 false，
    // 避免沿用上一次查看留下的跟随态（end() 只冻结、不重置 following）。
    runStateDecided.value = false
    follow.begin(false)
    scheduleScrollToTop()
  } else {
    // 重连即续流，任务确实在跑；keepFollowing 保住重连前的暂停/跟随选择。
    runStateDecided.value = true
    follow.begin(true, { keepFollowing: wasFollowing })
  }

  if (!props.taskId) {
    loading.value = false
    return
  }

  const url = `/api/v1/logs/${props.taskId}/stream`
  // 与下面的 SSE 并行发起 latest-log 预取，不 await：
  // 实时数据仍然只走 SSE，这份预取只在「流里没有实时内容、走到 done」那条既有路径上被消费
  // （见 fetchLatestLog → takeLatestLog），所以不会和流式内容重复渲染。
  // 重连（isReconnect）时不预取：那条路径最多会重试 5 次，每次都白发一个可能要全量解压的请求，
  // 反而加重了本来想优化的开销。
  if (!isReconnect) {
    prefetchLatestLog(props.taskId!)
  }
  eventSource = openAuthorizedEventStream(url, {
    onOpen() {
      loading.value = false
    },
    onMessage(data) {
      loading.value = false
      if (!data) {
        return
      }
      // 收到真实日志数据 = 有实质进展，不算空重连风暴，重置熔断计数。
      reconnectAttempts = 0
      // 同理：等待链切回来的流确实来了数据，这次「切流」有效，之后再掉回等待链可以重新切（见 waitingSwitchedToStream）
      waitingSwitchedToStream = false
      // 🔴 同时作废预取：这一份快照是「打开弹窗那一刻」拍的，比流里正在到达的内容更旧。
      // 只靠 TTL 挡不住这条路径 —— 任务在 1.2s 内跑完时，done 到达时预取仍在有效期内，
      // 复用它就会用旧快照（甚至是上一次运行的日志、或一条 content 为空的新记录）
      // 覆盖掉刚流完的正确输出。预取只服务「服务端立刻回 done、流里一个字节都没有」这一条路径。
      dropPrefetchedLatestLog()
      // 收到首包 = 任务在跑：认定运行中并开启跟随（只认定一次，之后由 useLogAutoFollow 维护）
      markRunning()
      pendingSseChunks.push(data)
      scheduleBufferFlush()
    },
    onEvent(event) {
      if (event.event !== 'done') {
        return
      }
      flushBufferedLogs()
      cleanup()
      if (event.data === 'reconnect') {
        // reconnect 意味着服务端已经找到 TinyLog、任务确实在跑，接下来会重新推一遍历史。
        // 预取那份快照到这里已经没有意义（而且重连很可能 150ms 就回来，TTL 根本挡不住），直接作废。
        dropPrefetchedLatestLog()
        // 服务端已找到 TinyLog、任务确实在跑：认定运行中并开启跟随。
        // 若这是流的第一个信号，下面 startStream(true) 捕获的 wasFollowing 才会是 true、能续上跟随。
        markRunning()
        // 退避的 0.5~5s 里不置 done：任务确实在跑，头部保持「运行中」。以前一进这个分支就置 done，
        // 「点运行后立刻打开日志」这条最常见的路径每次都会闪一下「已完成」。
        // 退避期间 done 为假，切回前台时 handleVisibilityChange 走「重开流」而不是「done 且无正文就补拉」：
        // startStream(true) 先 cleanup() 清掉这里挂的定时器，始终只剩一条流，也不会补拉一份旧快照。
        if (!scheduleStreamReconnect()) {
          // 连续多次重连都没有新数据：判定为无可续流的实时日志，按完成处理，停止重连风暴。
          done.value = true
          follow.end()
          void fetchLatestLog(0, follow.following.value ? 'bottom' : 'preserve')
        }
        return
      }
      // 其余载荷（finished / finished-late / 未知）才是这条流真正收口
      done.value = true
      // 🔴 done 的三种载荷里，只有 'finished'（服务端 0 等待、任务早就结束、日志早已落库）
      // 才允许复用并行预取。'finished-late' 说明服务端在短轮询里真的等过 ——
      // 也就是打开弹窗那一刻任务还在排队/运行，那时按 started_at DESC 取到的很可能是
      // **上一次运行**的记录、或一条 content 还是空的新记录。复用它会把上次的输出
      // 渲染成本次结果，或者错误地显示「日志已过期」。
      // 靠 TTL 挡不住：任务一两百毫秒就跑完时，done 到达时预取仍在有效期内。
      // 未知载荷按 finished 处理（向后兼容旧服务端与演示站的假流）。
      if (event.data === 'finished-late') {
        dropPrefetchedLatestLog()
        // C-log-follow-4：finished-late 说明打开时任务还在排队/启动，此刻按 started_at 取到的
        // 很可能是【上一次运行】的日志。先查真实状态：仍在排队就继续等、真正开跑就切回实时流，
        // 不拿旧快照冒充本次结果；确实结束了才回落到渲染最近一次日志。
        // 上面刚置了 done，而下面这一步要先 await 一次 live-logs：那一个往返里 waitingForLog 还是假、
        // headerState 就是 done，头部会闪一下「已完成」（打开排队超过 1.5s 的任务日志每次都碰得到）。
        // 所以正文还空着时先进「等待中」：头部/底栏与后面的排队文案连贯，这一拍切回前台也不会去补拉
        // 上一次运行的日志（handleVisibilityChange 看 waitingForLog）。等待链的每个出口都会收掉这一态——
        // 查到运行中、不再排队/查不到状态时置 false，查到排队中则换成排队文案接着等。
        // 正文非空说明是快任务、历史已经推完，这一轮确实结束了，「已完成」是对的，不动。
        if (!hasLogs.value) {
          waitingForLog.value = true
        }
        void pollPendingTaskUntilRunning('bottom')
        return
      }
      follow.end()
      void fetchLatestLog(0, follow.following.value ? 'bottom' : 'preserve')
    },
    onError() {
      flushBufferedLogs()
      loading.value = false
      cleanup()
      // 流本身出错时无从判断服务端处于哪条路径，预取一律作废、老老实实重新请求。
      dropPrefetchedLatestLog()
      // 先不置 done、不冻结跟随：任务多半还在跑，查过真实状态再决定重连、进等待链还是收口
      void recoverStreamAfterError()
    }
  })
}

// 退避重连，done:reconnect 与实时流出错两处共用：计一次连续空重连，没超上限就按退避间隔重开流、返回 true；
// 超过上限（熔断跳闸）返回 false，由调用方按完成收口。
function scheduleStreamReconnect() {
  reconnectAttempts++
  if (reconnectAttempts > MAX_RECONNECT_ATTEMPTS) {
    return false
  }
  // 退避重连：第 1 次 500ms，逐次翻倍，封顶 5s，降低无效重连对前端的冲击。
  const delay = Math.min(500 * 2 ** (reconnectAttempts - 1), 5000)
  // 同一时刻只留一个定时器（与等待轮询、404 重试共用 reconnectTimer）
  if (reconnectTimer !== null) {
    clearTimeout(reconnectTimer)
  }
  reconnectTimer = setTimeout(() => {
    reconnectTimer = null
    void startStream(true)
  }, delay)
  return true
}

// 实时流出错（断网、代理重置、移动端切后台被掐断）：sse.ts 不重试，直接交给 onError。
// 以前这里一律 done=true 再渲染 latest-log：运行期间 LatestLog 会回退读日志文件、正文非空，进不了 #115 的等待链，
// 于是任务明明还在跑，头部却变成「已完成」，之后也不再更新。
// 现在先查一次真实状态再分流：
// - 运行中（2）：走 done:reconnect 那套退避重连（同一个熔断计数与上限），头部保持「运行中」，
//   暂停 / 跟随选择由 startStream(true) 带过去；
// - 排队中（0.5）：与 finished-late 同一条路，进 pollPendingTaskUntilRunning 的等待链，开跑那一拍再切回实时流。
//   不能也走空重连：SSE 持续失败时会先白白耗光熔断，跳闸后 canSwitchWaitingToStream() 一直为假，任务开跑也切不回实时流；
// - 查不到状态、任务已结束、熔断已跳闸，或排队中却不该进等待链（见函数体），才照旧按完成收口。
async function recoverStreamAfterError() {
  const taskId = props.taskId
  // onError 里的 cleanup() 已经换过代，这里记下的是出错之后的这一代
  const generation = streamGeneration
  if (!disposed && props.visible && taskId) {
    let status: number | null = null
    try {
      const live = await taskApi.liveLogs(taskId)
      status = typeof live?.status === 'number' ? live.status : null
    } catch {
      // 状态查不到就不猜，按原来的方式收口
    }
    // await 这一拍里用户可能关掉弹窗、切了任务、组件被卸载，或者别的路径（如切回前台重开流）已经接手
    if (disposed || !props.visible || props.taskId !== taskId || generation !== streamGeneration) {
      return
    }
    // 排队中、这条流又一行正文都没收到（多半是打开以来一直在排队；重连刚清空正文、任务跑完又排上队也会走到这里，
    // 与流没断时服务端回 finished-late 的结果一致）：此刻 latest-log 取到的不是正在等的这一轮，不能拿它收口。
    // 按 finished-late 的口径进等待链：先置 done（等待期间切回前台不补拉、不重开流，见 handleVisibilityChange），
    // 头部由 waitingForLog 显示「等待中」；不 markRunning、不动 waitingSwitchedToStream，开跑后由等待链切流时再定。
    // 以下两种落到下面收口：
    // - 流里已经来过正文：刚才看的那一轮已经跑完、任务又排上了队，latest-log 就是这一轮，按完成渲染它
    //   （与流没断时服务端在这一轮结束时回 done:finished 的结果一致）；进等待链反而会让正文和「等待中」「无日志」对不上；
    // - canSwitchWaitingToStream() 为假（这条流是等待链切过来的、一条数据都没等到）：与等待链自己切不了流时一样，
    //   改走 latest-log（此刻它就是切流时开跑的那一轮），取不到正文仍会进 #115 等待链。
    if (status === TASK_STATUS_QUEUED && !hasLogs.value && canSwitchWaitingToStream()) {
      done.value = true
      waitQueuedTaskThenPoll('bottom')
      return
    }
    if (status === TASK_STATUS_RUNNING) {
      // 与 done:reconnect 同理：流出错前一条数据都没来时先认定运行中，
      // 下面 startStream(true) 捕获的 wasFollowing 才会是 true、能续上跟随；已判定过的保住用户的选择
      markRunning()
      if (scheduleStreamReconnect()) {
        return
      }
      // 熔断已跳闸：落到下面按完成收口
    }
  }
  done.value = true
  // 日志流结束：冻结跟随态，后续最后一次整体替换按冻结时的状态处理
  follow.end()
  // 跟随中就贴底（与 done:finished 同口径）。只看 hasLogs 不够：连续重连时每次 startStream(true) 都先清空正文，
  // 熔断跳闸走到这里时 hasLogs 已是 false，跟随中的用户会被甩到快照顶部；
  // 流恰好在任务结束时断开时，快照比屏幕上多出的结尾几行也会留在视口下方。
  void fetchLatestLog(0, follow.following.value ? 'bottom' : (hasLogs.value ? 'preserve' : 'top'))
}

// 作废预取。四处在用：流里收到第一段真实数据、收到 done:reconnect、
// 收到 done:finished-late（服务端等过 = 任务当时正在启动/运行）、以及 latest 模式开场。
// 判据统一成一句话：只要能证明「服务端这条流不是『任务早就结束、直接收口』」，
// 那份打开弹窗时拍的旧快照就不能再被消费。
//
// 除了置空引用，还要 abort 在途请求：运行中的大日志任务那一份可能是几十 MB，
// 不 abort 的话 axios 仍会把它下完并在主线程 JSON.parse，白白压在弱机上。
function dropPrefetchedLatestLog() {
  prefetchedLatestLogAbort?.abort()
  prefetchedLatestLogAbort = null
  prefetchedLatestLog = null
  prefetchedLatestLogTaskId = null
  prefetchedLatestLogAt = 0
}

async function loadLatestOnly() {
  cleanup()
  // latest 模式自己就是一次性拉取，不做预取；同时丢掉上一次 live 会话可能残留的、没人消费的预取，
  // 避免这里读到一份旧快照（TTL 之外还多一道保险）。
  dropPrefetchedLatestLog()
  resetLogOutput()
  done.value = true
  error.value = null
  emptyMessage.value = null
  emptyHint.value = ''
  waitingForLog.value = false
  waitingPollCount = 0
  loading.value = true
  // latest 是一次性拉取的「最近结果」，后面没有 SSE 续流，冻结跟随、从头看即可。
  follow.end()
  scheduleScrollToTop()

  if (!props.taskId) {
    loading.value = false
    return
  }

  await fetchLatestLog(0, 'top')
  loading.value = false
}

function prefetchLatestLog(taskId: number) {
  prefetchedLatestLogTaskId = taskId
  prefetchedLatestLogAt = Date.now()
  prefetchedLatestLogAbort = typeof AbortController === 'undefined' ? null : new AbortController()
  prefetchedLatestLog = taskApi.latestLog(taskId, prefetchedLatestLogAbort?.signal)
  // 必须就地挂一个空 catch：这是一个「可能压根没人消费」的在途请求，
  // 没有 rejection handler 的话浏览器会报 unhandled promise rejection。
  // 真正的错误处理仍在 takeLatestLog / fetchLatestLog 里（退回原来的重发 + 404 重试分支）。
  // 注意 .catch() 返回的是新 promise，prefetchedLatestLog 仍然指向原始那个。
  prefetchedLatestLog.catch(() => {})
}

// 取 latest-log：优先复用 startStream() 里已经在飞的那份预取，拿不到就照原样新发一个请求。
async function takeLatestLog(): Promise<any> {
  const pending = prefetchedLatestLog
  const pendingTaskId = prefetchedLatestLogTaskId
  const pendingAt = prefetchedLatestLogAt
  // 一次性消费：404 重试、切回前台补拉这些后续调用都必须拿最新数据，不能反复复用同一份旧快照。
  prefetchedLatestLog = null
  prefetchedLatestLogTaskId = null
  prefetchedLatestLogAt = 0

  if (pending && pendingTaskId === props.taskId && Date.now() - pendingAt <= LATEST_LOG_PREFETCH_TTL) {
    try {
      return await pending
    } catch {
      // 预取失败（网络抖动、日志还没落库的 404 等）一律静默吞掉，退回原来的行为重新发一次，
      // 让后面的 catch 按既有分支处理。绝不能让预取本身把弹窗打挂。
    }
  }
  return taskApi.latestLog(props.taskId!)
}

// 「日志还没出现」时的统一判断：任务如果还在排队中/运行中，就继续等，别急着收口成静态文案。
// issue #115 的那一屏就出在这里 —— 随机延迟走的是延迟重新入队、并发数被别的任务占满时也一样，
// 任务会长时间停在「排队中」，这段时间 task_logs 里一行都没有，
// 老逻辑重试 5 次（2.5s）就把「该任务还没有日志记录」定死，而且之后再也不会自己纠正。
//
// 返回 true = 已经接管（安排了下一次轮询、切回了实时流，或者写好了「等到上限」的说明），调用方直接 return；
// 返回 false = 可以按调用方原本的文案收口。
// 运行中（2）不再轮询 latest-log、改为切回实时流；排队中（0.5）的下一拍改为先查状态 —— 理由见函数体（C-log-follow-4 ②）。
async function keepWaitingForPendingTask(retryCount: number, scrollMode: 'top' | 'bottom' | 'preserve') {
  const taskId = props.taskId
  if (!disposed && props.visible && taskId) {
    // 顺带查一次任务状态：LiveLogs 的返回体里带着 status（0=已禁用 / 0.5=排队中 / 1=已启用 / 2=运行中）
    let status: number | null = null
    try {
      const live = await taskApi.liveLogs(taskId)
      status = typeof live?.status === 'number' ? live.status : null
    } catch {
      // 状态查不到就不猜，直接按调用方原来的文案收口
    }
    const pending = status === TASK_STATUS_QUEUED || status === TASK_STATUS_RUNNING
    // await 这一拍里用户可能已经关掉弹窗、切了任务、或者组件被卸载，这些情况下不能再挂新的定时器
    if (pending && !disposed && props.visible && props.taskId === taskId) {
      if (waitingPollCount < WAITING_POLL_LIMIT) {
        waitingPollCount++
        // C-log-follow-4 ②：任务已经在跑，就别再每秒拉 latest-log —— 那样一旦拿到正文只会渲染一次快照、
        // 之后既不续流也不再轮询，任务后面的输出进不来。直接切回实时流：服务端命中 TinyLog 后先推历史再实时推送，
        // 还没建出 TinyLog 时它会在运行中分支里等，出现后回 done:reconnect。
        // markRunning()：运行态还没判定时等同 follow.begin(true)（开启跟随）；流里来过数据、已经判定过的，
        // 保住用户当时的暂停 / 跟随选择，交给 startStream(true) 的 keepFollowing 续上。
        // 切不了的场合（见 canSwitchWaitingToStream）落到下面，照旧按「任务已开始」等待、轮询 latest-log。
        if (status === TASK_STATUS_RUNNING && canSwitchWaitingToStream()) {
          waitingSwitchedToStream = true
          waitingForLog.value = false
          markRunning()
          void startStream(true)
          return true
        }
        waitingForLog.value = true
        emptyMessage.value = status === TASK_STATUS_QUEUED
          ? '任务排队中，开始执行后会出现日志…'
          : '任务已开始，正在等待第一条日志…'
        emptyHint.value = '正在等待日志写入，出现后会自动显示，不用手动重开'
        // 同一时刻只留一个等待定时器：切后台再回来时 handleVisibilityChange 会另起一条链，
        // 不掐掉旧的就会变成两条链同时轮询。
        if (reconnectTimer !== null) {
          clearTimeout(reconnectTimer)
        }
        reconnectTimer = setTimeout(() => {
          reconnectTimer = null
          // 排队中：下一拍改走「先查状态」的排队轮询（与它共用 reconnectTimer 和 waitingPollCount 上限）。
          // 继续先拉 latest-log 的话，任务在这 1s 里开跑并写出了第一行，就会被当成快照渲染一次然后停住（同上），
          // 先查状态才能在变成 2 的那一拍切回实时流。切不了流的场合仍按原来的节奏拉 latest-log。
          if (status === TASK_STATUS_QUEUED && canSwitchWaitingToStream()) {
            void pollPendingTaskUntilRunning(scrollMode)
          } else {
            void fetchLatestLog(retryCount, scrollMode)
          }
        }, 1000)
        return true
      }
      // 到达兜底上限：停止轮询。这里退出等待态，头部徽标会显示「无日志」——
      // 这句话是对着日志说的（确实一条都没有），所以正文也要写成「还没有产生日志」的口吻，
      // 任务卡在哪一步放进括号里补充，避免出现「正文说还在跑、徽标说无日志」那种看着矛盾的组合。
      waitingForLog.value = false
      emptyMessage.value = status === TASK_STATUS_QUEUED
        ? '还没有产生日志（任务一直停在排队中）'
        : '还没有产生日志（任务仍在运行，但还没输出第一行）'
      emptyHint.value = '已停止自动刷新，重新打开这个窗口可以继续等'
      return true
    }
  }
  waitingForLog.value = false
  return false
}

async function fetchLatestLog(retryCount = 0, scrollMode: 'top' | 'bottom' | 'preserve' = 'top') {
  const previousScrollTop = logContainerRef.value?.scrollTop ?? 0
  try {
    const res = await takeLatestLog() as any
    if (!res) {
      // 连记录都没返回：也可能只是任务还在排队/运行中，先按状态决定要不要继续等
      if (await keepWaitingForPendingTask(retryCount, scrollMode)) {
        return
      }
      emptyMessage.value = NEVER_RAN_MESSAGE
      emptyHint.value = NEVER_RAN_HINT
      return
    }
    if (res.content) {
      // 拿到正文：等待态要一并收掉，否则头部徽标会一直停在「等待中」
      emptyMessage.value = null
      emptyHint.value = ''
      waitingForLog.value = false
      resetLogOutput()
      appendLogChunk(String(res.content))
      // #147：这一轮流里一条实时数据都没收到（runStateDecided 仍为 false）= 打开的是一份已经结束的日志，
      // 账户偏好「打开已结束的日志时定位到底部」开着就贴底，关着维持原来的顶部 / 原位。
      // 运行中看着它跑完的（已判定），仍按跟随态 bottom / preserve 走，暂停的人不会被拽到底。
      // 放在这里而不是 begin(false) 处：静态正文的所有收口路径（done:finished、finished-late 等待链收口、
      // 出错恢复、404 重试、#115 等待链）都汇到这一处，begin(false) 那一刻正文还是空的。
      // 🔴 隐式不变式：runStateDecided 为 false 时，走到这里之前正文一定是空的——实时数据都要先经 markRunning 才会渲染，
      // 静态正文每个会话也只在这里写一次（切回前台补拉只在正文为空时才发），所以贴底不会把正在读的人拽走。
      // 以后谁在实时会话里绕过 markRunning 渲染正文、或给已渲染的静态正文加刷新，就会打破它。
      if (scrollMode === 'bottom' || (!runStateDecided.value && readListPreference('log_open_at_bottom'))) {
        scheduleScrollToBottom()
      } else if (scrollMode === 'preserve') {
        void nextTick(() => {
          if (logContainerRef.value) {
            logContainerRef.value.scrollTop = previousScrollTop
          }
        })
      } else {
        scheduleScrollToTop()
      }
    } else {
      // 有记录却没有内容有两种可能：日志文件被保留策略清掉了，
      // 或者这次运行刚建好记录、还没写出第一行（记录是在执行开始时就落库的）。
      // 后者不能报「已过期」，同样交给状态判断，两条文案的语义由此保持互斥。
      if (await keepWaitingForPendingTask(retryCount, scrollMode)) {
        return
      }
      emptyMessage.value = '日志已过期，文件已被清理'
      // 这条记录确实跑过，只是正文没了，不能再配「执行一次后就会出现日志」那句引导
      emptyHint.value = ''
    }
  } catch (err: any) {
    if (err?.response?.status === 404) {
      if (retryCount < 5 && props.visible) {
        // 同一时刻只留一个定时器：快速重试和下面的等待轮询共用 reconnectTimer，
        // 切后台再回来时 handleVisibilityChange 会另起一条链，不掐掉旧的就会叠出多条并行轮询。
        if (reconnectTimer !== null) {
          clearTimeout(reconnectTimer)
        }
        reconnectTimer = setTimeout(() => {
          reconnectTimer = null
          void fetchLatestLog(retryCount + 1, scrollMode)
        }, 500)
        return
      }
      // 快速重试用完仍是 404，不等于「从来没跑过」，先看任务是不是还在排队/运行中
      if (await keepWaitingForPendingTask(retryCount, scrollMode)) {
        return
      }
      // 后端 404 的说明比前端这句通用文案更准（例如区分「从来没跑过」和「日志已按保留策略清理」），
      // 统一的错误结构是 { error: string }（server/pkg/response），取不到再退回原文案
      emptyMessage.value = err?.response?.data?.error || NEVER_RAN_MESSAGE
      // 只有「从来没跑过」才配得上那句引导：后端换成「日志已按保留策略清理」时再说
      // 「执行一次后就会出现日志」，就和它自己上一句自相矛盾了
      emptyHint.value = emptyMessage.value === NEVER_RAN_MESSAGE ? NEVER_RAN_HINT : ''
    } else {
      waitingForLog.value = false
      error.value = '获取日志失败'
    }
  }
}

function resetLogOutput() {
  logBuffer.reset()
  renderWindowExpanded.value = false
  logRevision.value++
}

function appendLogChunk(chunk: string) {
  logBuffer.append(chunk)
  logRevision.value++
}

// 合帧间隔按日志体量放大：增量渲染后每次 flush 的渲染开销只和新增行有关，
// 但开启自动跟随时仍要对整篇文档强制一次布局，日志越长这一步越贵。
// 放大到 48 / 120ms 肉眼仍然无感，却能显著减少长日志下的主线程占用。
function flushDelayMs(): number {
  const lines = logBuffer.lineCount
  if (lines < 2000) return 16
  if (lines < 10000) return 48
  return 120
}

function scheduleBufferFlush() {
  if (logFlushTimer !== null) {
    return
  }
  logFlushTimer = setTimeout(() => {
    logFlushTimer = null
    flushBufferedLogs()
  }, flushDelayMs())
}

function flushBufferedLogs() {
  if (logFlushTimer !== null) {
    clearTimeout(logFlushTimer)
    logFlushTimer = null
  }
  if (pendingSseChunks.length === 0) {
    return
  }

  for (const chunk of pendingSseChunks) {
    // 不把“每个 SSE 消息结束”当成真实换行。
    // 真实终端只有遇到 \n / \r\n 才落新行，裸 \r 则继续覆盖当前行。
    logBuffer.append(chunk)
  }
  pendingSseChunks = []
  // 整批只触发一次重渲染
  logRevision.value++

  if (pendingScrollRestore !== null) {
    const target = pendingScrollRestore
    pendingScrollRestore = null
    // 用带程序标记的写入恢复位置，避免这次 scroll 被自动跟随误读成用户上翻
    void nextTick(() => {
      follow.setScrollTop(target)
    })
  } else {
    // 跟随中就贴到最新，暂停时什么都不做（由 useLogAutoFollow 按 live && following 判定）
    follow.onContentChange()
  }
}

function scheduleScrollToBottom() {
  void nextTick(() => {
    // el-dialog 带 destroy-on-close，关闭后 logContainerRef 会被置空；
    // 重新打开时 startStream 是在 watch(props.visible) 里同步调用的，
    // 这一拍弹窗内容不一定已经挂上，只靠 scrollToBottom 内部的空值守卫会静默失效（表现为停在顶部）。
    // 补一次 rAF 重试兜底：下一帧 DOM 必然已经渲染。
    if (!logContainerRef.value) {
      requestAnimationFrame(() => {
        scrollToBottom()
      })
      return
    }
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
  // 换代：让还在 await 里的出错收尾作废，见 streamGeneration
  streamGeneration++
  if (logFlushTimer !== null) {
    clearTimeout(logFlushTimer)
    logFlushTimer = null
  }
  pendingSseChunks = []
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
    // 等待链（排队中轮询 pollPendingTaskUntilRunning / #115 等日志轮询）还在跑时不补拉，由它们按自己的节奏刷新：
    // 排队期间照常 fetchLatestLog 会取到【上一次运行】的日志并当成本次结果渲染出来。
    if (!hasLogs.value && !waitingForLog.value) {
      void fetchLatestLog()
    }
    return
  }

  // done 为假也包括出错后 / done:reconnect 后的退避重连期间：这里直接重开流，
  // startStream 里的 cleanup() 会清掉待发的重连定时器并换代，始终只剩一条流
  if (wasBackgrounded) {
    void startStream(true)
  }
}

function cycleFontSize() {
  if (fontSize.value === 'sm') fontSize.value = 'md'
  else if (fontSize.value === 'md') fontSize.value = 'lg'
  else fontSize.value = 'sm'
  // 字号变化会改变内容总高度，跟随中要重新贴底（原实现漏了这一步）
  follow.onContentChange()
}

function toggleWrap() {
  wrap.value = !wrap.value
  // 换行切换同样会改变内容总高度，跟随中要重新贴底
  follow.onContentChange()
}

async function handleCopy() {
  if (!hasLogs.value) {
    ElMessage.warning('暂无内容可复制')
    return
  }
  // 纯文本按需还原，不进渲染路径
  const text = logBuffer.toText()
  try {
    await navigator.clipboard.writeText(text)
    ElMessage.success('已复制')
  } catch {
    const ta = document.createElement('textarea')
    ta.value = text
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
  const blob = new Blob([logBuffer.toText()], { type: 'text/plain;charset=utf-8' })
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
  // 先置标记再 cleanup：等待轮询里那次 await 如果正好在飞，回来后要靠它拦住「再挂一个定时器」
  disposed = true
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
    :lock-scroll="false"
    class="log-viewer-dialog"
    destroy-on-close
    @close="handleClose"
  >
    <template #header>
      <div class="viewer-hero">
        <div class="viewer-hero-main">
          <div class="viewer-hero-title-row">
            <transition name="status-switch" mode="out-in">
              <!-- 等待日志（任务排队中/运行中但还没日志）复用「运行中」这颗呼吸灯，不另造一套 UI -->
              <span
                v-if="headerState === 'running' || headerState === 'waiting'"
                key="running"
                class="status-orb status-orb--running"
                :aria-label="headerState === 'waiting' ? '等待日志' : '运行中'"
              >
                <span class="status-orb-core"></span>
                <span class="status-orb-ripple"></span>
              </span>
              <span v-else-if="headerState === 'error'" key="error" class="status-orb status-orb--error" aria-label="错误">
                <el-icon :size="12"><Warning /></el-icon>
              </span>
              <span v-else-if="headerState === 'empty'" key="empty" class="status-orb status-orb--empty" aria-label="无日志">
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
                'viewer-hero-status--running': headerState === 'running' || headerState === 'waiting',
                'viewer-hero-status--done': headerState === 'done',
                'viewer-hero-status--empty': headerState === 'empty',
                'viewer-hero-status--error': headerState === 'error'
              }"
            >
              {{ headerState === 'error' ? '异常' : headerState === 'waiting' ? '等待中' : headerState === 'empty' ? '无日志' : headerState === 'done' ? '已完成' : '运行中' }}
            </span>
          </div>
          <div class="viewer-hero-meta">
            <span class="viewer-hero-meta-item">{{ lineCount }} 行</span>
            <span v-if="byteLabel" class="viewer-hero-meta-item">{{ byteLabel }}</span>
            <!-- 删掉了「跟随」开关，只在【已判定为运行中】且已暂停时给一行弱提示，告诉用户怎么恢复。
                 gate 上 runStateDecided：未判定时正文还空、headerState 也是 'running'，
                 不 gate 会在每次打开时闪一下这句提示。 -->
            <span
              v-if="runStateDecided && headerState === 'running' && !isFollowing"
              class="viewer-hero-meta-item viewer-hero-meta-item--paused"
            >已暂停跟随 · 滚到底部恢复</span>
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
              @click="toggleWrap"
              aria-label="切换换行"
            >
              <el-icon :size="15"><Switch /></el-icon>
              <span class="tool-btn-label tool-btn-label--wrap">Wrap</span>
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
          <!-- 移动端（全屏）不渲染：关闭入口挪到右下角的 footer（见模板末尾的 #footer）。
               与 footer 用同一个 dialogFullscreen 判定，任何宽度下两者恰好出现一个，不会一个关闭按钮都没有。 -->
          <button v-if="!dialogFullscreen" class="tool-btn tool-btn--close" @click="handleClose" aria-label="关闭">
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
          <el-icon :size="22">
            <Loading v-if="headerState === 'waiting'" />
            <InfoFilled v-else />
          </el-icon>
          <span>{{ emptyMessage }}</span>
          <span v-if="emptyHint" class="viewer-message-hint">{{ emptyHint }}</span>
        </div>
        <div v-else-if="!hasLogs && !loading" class="viewer-message">
          <el-icon :size="22"><Loading /></el-icon>
          <span>等待日志输出...</span>
        </div>
        <div v-else class="viewer-log-content" :class="{ 'viewer-log-content--nowrap': !wrap }">
          <button
            v-if="omittedLineCount > 0"
            type="button"
            class="log-omitted-notice"
            title="超长日志默认只渲染末尾部分，避免页面卡顿。展开完整日志在内容极多时可能需要等待片刻，也可以直接用上方「下载」拿到全部内容。"
            @click="expandRenderWindow"
          >已省略前 {{ omittedLineCount }} 行（默认只渲染最后 {{ MAX_RENDERED_LINES }} 行）· 点击展开完整日志</button>
          <span v-for="chunk in renderChunks" :key="chunk.key" v-html="chunk.html"></span>
          <span v-html="pendingLineHtml"></span>
        </div>
      </div>

      <div class="viewer-statusbar">
        <div class="viewer-statusbar-group viewer-statusbar-group--meta">
          <span class="viewer-statusbar-item">{{ lineCount }} 行</span>
          <span v-if="byteLabel" class="viewer-statusbar-item">{{ byteLabel }}</span>
          <span class="viewer-statusbar-item">Wrap {{ wrap ? 'ON' : 'OFF' }}</span>
        </div>
        <div class="viewer-statusbar-group viewer-statusbar-group--status">
          <!-- 等日志期间沿用「实时采集中」那颗跳动的点，表示这里还在自动刷新，不是死在「暂无日志」上 -->
          <span v-if="headerState === 'waiting'" class="viewer-statusbar-item viewer-statusbar-item--live">等待日志中</span>
          <span v-else-if="props.mode === 'latest' && !error && !emptyMessage" class="viewer-statusbar-item">最近结果</span>
          <span v-else-if="!done && !error && !emptyMessage" class="viewer-statusbar-item viewer-statusbar-item--live">实时采集中</span>
          <span v-else-if="error" class="viewer-statusbar-item viewer-statusbar-item--error" :title="error">{{ error }}</span>
          <span v-else-if="emptyMessage" class="viewer-statusbar-item viewer-statusbar-item--empty">暂无日志</span>
          <span v-else class="viewer-statusbar-item">UTF-8</span>
        </div>
      </div>
    </div>
    <!-- 移动端全屏时关闭入口放右下角（v3.3.1，issue #143 F2）：自定义头部右上角的 × 离拇指最远，
         iOS 上整个头部还曾被面板顶栏盖住、根本点不到。桌面不渲染 footer，日志区高度不受影响。
         本弹窗是 show-close=false + 自定义头部，EP 自带的 × 本来就没有，所以头部的 .tool-btn--close 在移动端由上面的 v-if 撤掉。 -->
    <template v-if="dialogFullscreen" #footer>
      <el-button @click="handleClose">关闭</el-button>
    </template>
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
  background: var(--dd-surface-solid);
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
  color: var(--dd-text);
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
  color: var(--dd-text-3);
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
  // 运行/成功/失败的状态 chip，属于天然胶囊 → pill 档
  border-radius: var(--dd-radius-pill);

  // #133：运行中标签改用 success 绿，与任务页 getStatusType 的「运行中 success」保持一致。
  // 等待日志态共用这一档（也是「进行中」），一并转绿。
  &--running {
    background: color-mix(in srgb, var(--dd-ok) 14%, transparent);
    color: var(--dd-ok-ink);
  }

  &--done {
    background: color-mix(in srgb, var(--dd-ok) 14%, transparent);
    color: var(--dd-ok-ink);
  }

  &--error {
    background: color-mix(in srgb, var(--dd-err) 14%, transparent);
    color: var(--dd-err-ink);
  }

  &--empty {
    background: color-mix(in srgb, var(--dd-text-3) 18%, transparent);
    color: var(--dd-text-2);
  }
}

.viewer-hero-meta {
  display: flex;
  gap: 14px;
  flex-wrap: wrap;
  font-size: 12px;
  color: var(--dd-text-2);
}

.viewer-hero-meta-item {
  font-family: var(--dd-font-ui);

  // 「已暂停跟随」弱提示：比其它元信息更淡一点，不抢注意力
  &--paused {
    color: var(--dd-text-3);
  }
}

/* Status orb：状态底块，靠底色区分运行/成功/失败 */
.status-orb {
  position: relative;
  width: 22px;
  height: 22px;
  // 22×22 的图标底属于小型交互块 → control 档
  border-radius: var(--dd-radius-control);
  display: inline-flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
}

.status-orb--running {
  // 与头部「运行中」标签一致转为 success 绿（#133）
  background: color-mix(in srgb, var(--dd-ok) 14%, transparent);
}

.status-orb--done {
  background: color-mix(in srgb, var(--dd-ok) 14%, transparent);
  color: var(--dd-ok-ink);
}

.status-orb--error {
  background: color-mix(in srgb, var(--dd-err) 14%, transparent);
  color: var(--dd-err-ink);
}

.status-orb--empty {
  background: color-mix(in srgb, var(--dd-text-3) 18%, transparent);
  color: var(--dd-text-3);
}

.status-orb-core {
  width: 8px;
  height: 8px;
  // 白名单：形状承载语义 —— 8×8 的呼吸点是「运行中」的状态灯，与 global.scss 的 .pulse-dot 同类，
  // 方化后就是一小块色斑、看不出是状态灯。两种 shape 模式下都固定圆形，不吃 --dd-radius-* 刻度。
  border-radius: 50%;
  // 与头部「运行中」标签一致转为 success 绿（#133）
  background: var(--dd-ok);
  animation: orb-core 1.4s ease-in-out infinite;
}

.status-orb-ripple {
  position: absolute;
  inset: 0;
  // 涟漪是从 .status-orb 里放大出来的一层，形状必须跟底块同档，否则放大过程中会露出错位的角
  border-radius: var(--dd-radius-control);
  background: color-mix(in srgb, var(--dd-ok) 40%, transparent);
  animation: orb-ripple 1.8s ease-out infinite;
}

.status-switch-enter-active,
.status-switch-leave-active {
  transition:
    transform var(--dd-motion-fast) var(--dd-ease-standard),
    opacity var(--dd-motion-fast) var(--dd-ease-standard);
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
  background: var(--dd-surface-solid);
  color: var(--el-text-color-regular);
  // 工具栏按钮 → control 档
  border-radius: var(--dd-radius-control);
  font-size: 12px;
  font-family: var(--dd-font-mono);
  cursor: pointer;
  transition:
    color var(--dd-motion-fast) var(--dd-ease-standard),
    background-color var(--dd-motion-fast) var(--dd-ease-standard),
    border-color var(--dd-motion-fast) var(--dd-ease-standard);

  &:hover:not(:disabled):not(.tool-btn--close) {
    color: var(--dd-accent-ink);
    border-color: color-mix(in srgb, var(--dd-accent) 40%, var(--el-border-color-light));
    background: color-mix(in srgb, var(--dd-accent) 6%, transparent);
  }

  &:disabled {
    opacity: 0.4;
    cursor: not-allowed;
  }

  &--active {
    background: color-mix(in srgb, var(--dd-accent) 10%, transparent);
    color: var(--dd-accent-ink);
    border-color: color-mix(in srgb, var(--dd-accent) 40%, transparent);
  }

  &--close {
    width: 34px;
    height: 34px;
    padding: 0;
    display: inline-flex;
    align-items: center;
    justify-content: center;
    color: var(--dd-text-2);
    border-color: transparent;
    // 34×34 的图标按钮，与 .tool-btn 同档
    border-radius: var(--dd-radius-control);
    margin-left: 6px;
    position: relative;
    overflow: hidden;
    transition: color var(--dd-motion-normal) var(--dd-ease-standard);

    .el-icon {
      position: relative;
      z-index: 1;
    }

    // hover 时纯色底铺满，不再用缩放的渐变块浮起
    &::before {
      content: '';
      position: absolute;
      inset: 0;
      // hover 铺满的红底盖在按钮上，圆角必须跟按钮同档，否则圆角模式下四角会溢出按钮轮廓
      border-radius: var(--dd-radius-control);
      background: var(--dd-err-solid);
      opacity: 0;
      transition: opacity var(--dd-motion-fast) var(--dd-ease-standard);
    }

    &:hover:not(:disabled) {
      color: var(--dd-on-status);
      border-color: transparent;
      background: transparent;

      &::before {
        opacity: 1;
      }
    }

    &:focus-visible {
      outline: 2px solid color-mix(in srgb, var(--dd-err) 60%, transparent);
      outline-offset: 2px;
    }
  }
}

// 页面自带的「减少动效」规则统一走这个包装（C8 动效偏好，本文件下方状态灯那段也用它）：
// - 跟随系统：媒体查询里带 :root:not(.dd-motion-force)，个人设置选「始终开启」时不生效；
// - 个人设置选「减少动效」：html.dd-motion-off 下不看系统同样生效。
// 与 global.scss 末尾「减少动效」段同一口径；前缀包在 :where() 里，特异性与改动前的裸选择器相同，层叠结果不变。
@mixin dd-page-reduced-motion {
  @media (prefers-reduced-motion: reduce) {
    :where(:root:not(.dd-motion-force)) {
      @content;
    }
  }

  :where(html.dd-motion-off) {
    @content;
  }
}

@include dd-page-reduced-motion {
  .tool-btn--close {
    transition: none;

    &::before {
      transition: none;
    }
  }
}

.tool-btn-label {
  font-weight: 600;
  letter-spacing: 0.4px;
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
  color: var(--dd-log-text-color);
  // 🔴 保持 0，不吃令牌 —— 必须写回来压掉 global.scss 里 .dd-log-surface 的 surface 档。
  // 这块深色日志区是【贴边内嵌】的：本文件下方把 .log-viewer-dialog .el-dialog__body 的
  // padding 设成了 0，.viewer-body 也没有内边距，所以它从 header 分隔线正下方一路铺满到
  // 弹窗底边、左右也顶到弹窗边框。带着自己的 10px 圆角的话，圆角模式下左上/右上会被切出
  // 两个缺口露出弹窗白底（缺口正好卡在 header 分隔线两端，很显眼）。
  // 外轮廓的圆角由 .log-viewer-dialog 的 surface 档 + overflow:hidden 统一去裁，这里必须是 0。
  // 同形态的另外三处（logs/index.vue、ScriptExecutionDialogs.vue、LogFileBrowser.vue）写法一致。
  border-radius: 0;
}

// 正文容器用 div + white-space:pre-wrap 代替 <pre>：
// Vue 模板编译器会原样保留 <pre> 内部的缩进空白，而这里正文要拆成多个子节点分块渲染，
// 缩进会被当成正文渲染出来。等宽字体来自父级 .viewer-log，换行语义由这里的 white-space 承担，
// 视觉与原来的 <pre> 完全一致。
.viewer-log-content {
  margin: 0;
  white-space: pre-wrap;
  word-break: break-all;

  &--nowrap {
    white-space: pre;
    word-break: normal;
  }
}

// 渲染窗口封顶提示：扁平虚线块，颜色全部从日志前景色派生，明暗两态自动适配
.log-omitted-notice {
  display: block;
  width: 100%;
  margin: 0 0 10px;
  padding: 6px 10px;
  border: 1px dashed color-mix(in srgb, var(--dd-log-text-color) 32%, transparent);
  // 可点击的提示小块（点一下展开更多日志）→ control 档
  border-radius: var(--dd-radius-control);
  background: color-mix(in srgb, var(--dd-log-text-color) 8%, transparent);
  color: color-mix(in srgb, var(--dd-log-text-color) 70%, transparent);
  font-family: var(--dd-font-mono);
  font-size: 11.5px;
  letter-spacing: 0.3px;
  text-align: left;
  white-space: normal;
  word-break: normal;
  cursor: pointer;
  transition:
    color var(--dd-motion-fast) var(--dd-ease-standard),
    background-color var(--dd-motion-fast) var(--dd-ease-standard),
    border-color var(--dd-motion-fast) var(--dd-ease-standard);

  &:hover {
    color: var(--dd-log-text-color);
    border-color: color-mix(in srgb, var(--dd-log-text-color) 52%, transparent);
    background: color-mix(in srgb, var(--dd-log-text-color) 14%, transparent);
  }
}

.viewer-message {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  height: 100%;
  gap: 10px;
  color: color-mix(in srgb, var(--dd-log-text-color) 55%, transparent);
  font-size: 13px;

  // 错误提示改用警告墨色，确保亮色日志底上可读性达标
  &--error {
    color: var(--dd-warn-ink);
  }

  &--empty {
    color: color-mix(in srgb, var(--dd-log-text-color) 70%, transparent);
  }
}

.viewer-message-hint {
  font-size: 11.5px;
  color: color-mix(in srgb, var(--dd-log-text-color) 72%, transparent);
  letter-spacing: 0.2px;
}

/* =============== Status bar =============== */
.viewer-statusbar {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 10px;
  padding: 6px 22px;
  font-family: var(--dd-font-mono);
  font-size: 11px;
  color: var(--dd-text-3);
  border-top: 1px solid var(--viewer-border-soft, var(--el-border-color-light));
  background: color-mix(in srgb, var(--el-fill-color-lighter) 60%, transparent);
  flex-shrink: 0;
}

.viewer-statusbar-group {
  display: inline-flex;
  align-items: center;
  gap: 14px;
  min-width: 0;

  &--meta {
    flex-shrink: 0;
  }

  &--status {
    flex: 0 1 auto;
    min-width: 0;
  }
}

.viewer-statusbar-item {
  letter-spacing: 0.4px;
  white-space: nowrap;

  &--live {
    // #133：与头部「运行中」标签一致用 success 绿（原为 warning 橙：头部改绿之后一橙一绿，两处对不上）。
    // 「等待日志中」共用这一档，与头部等待态同样走 success。
    color: var(--dd-ok-ink);
    font-weight: 600;

    &::before {
      content: '● ';
      animation: pulse 1.6s ease-in-out infinite;
    }
  }

  // 右侧长报错单行省略，防止把左侧行数/大小信息挤出状态栏
  &--error {
    color: var(--dd-err-ink);
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  &--empty {
    color: var(--dd-text-3);
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

// 走上面 dd-page-reduced-motion 的包装（C8 动效偏好）
@include dd-page-reduced-motion {
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

  // 移动端保留字号 SM/MD/LG 文本指示，仅隐藏 Wrap 文案节省工具栏空间
  .tool-btn-label--wrap {
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
  独立的非 scoped style：专门处理 el-dialog 渲染出来的 .el-dialog 元素（class="log-viewer-dialog" 落在它上面）。
  原因：LogViewer.vue 的 template root 就是 el-dialog 组件自身，.el-dialog 这个元素由 Element Plus 在组件内部渲染，
  本组件模板里没有任何一个带本组件 data-v 属性的元素包在它外面，
  所以 :deep(.log-viewer-dialog) 编译出的 [data-v-xxx] .log-viewer-dialog 选择器在实际 DOM 里没办法命中。
  注意这【不是】因为 teleport：EP 2.13.5 的 el-dialog 默认 append-to-body=false，是原地渲染在页面里的（.layout-main 之内）。
  本项目也不要为了层级问题给弹窗加 append-to-body —— 各页面 scoped 的 :deep(.xxx-dialog) 选择器正依赖原地渲染。
  改用非 scoped 块，编译后是纯 .log-viewer-dialog 选择器，不依赖 scope，一定生效。
  类名唯一 log-viewer-dialog，不会污染其他组件。
-->
<style lang="scss">
.log-viewer-dialog {
  --viewer-border-soft: color-mix(in srgb, var(--el-border-color-light) 85%, transparent);

  width: min(1400px, 92vw);
  // 弹窗外壳 → surface 档
  border-radius: var(--dd-radius-surface);
  overflow: hidden;
  display: flex;
  flex-direction: column;
  // 桌面端按内容观感收紧：
  // 保持足够阅读空间，但避免空日志时出现大面积“黑幕感”。
  height: clamp(680px, 85dvh, 920px);
  max-height: calc(100dvh - 56px);
  // align-center 模式下 el-overlay-dialog 是 flex 容器，用 margin: auto 让 dialog 垂直+水平居中；
  // 如果写成 margin: 0 auto 上下 margin 会变成 0，dialog 会贴到容器底部。
  margin: auto;

  // fullscreen 模式（由 :fullscreen="dialogFullscreen" prop 激活，对应 mobile），
  // 由 Element Plus 默认样式 .el-dialog.is-fullscreen { height:100%; ... } 接管，
  // 但我们的 height:90vh 优先级一样，需要显式让 fullscreen 时恢复全屏。
  &.is-fullscreen {
    width: 100%;
    height: 100%;
    max-height: 100%;
    // 保持 0，不吃令牌：全屏态下弹窗铺满整个视口，四角带圆角会露出遮罩层的黑边
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
