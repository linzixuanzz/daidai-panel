<script setup lang="ts">
import { computed, onBeforeUnmount, reactive, ref, watch } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { ArrowUp, Close, VideoPause } from '@element-plus/icons-vue'
import { taskApi, type TaskRunningItem } from '@/api/task'
import { useAuthStore } from '@/stores/auth'
import { useRunningStore } from '@/stores/running'
import { useUiStore } from '@/stores/ui'
import { useResponsive } from '@/composables/useResponsive'
import { canOperate } from '@/utils/roles'
import { formatDuration } from '@/utils/duration'
import { extractError, isCancel } from '@/utils/error'
import { openAuthorizedEventStream, type EventStreamConnection } from '@/utils/sse'

/**
 * 运行中浮窗（F6）。挂在 MainLayout，跨页面保持；展开状态读写 ui.dockOpen，数据读 useRunningStore()。
 *
 * 实时日志只给列表里的【前 2 个】任务开 SSE（`/api/v1/logs/:taskId/stream`）：
 * HTTP/1.1 下同一个域名最多 6 条并发连接，日志流是长连接，开多了会把页面自己的请求挤到排队。
 * 其余任务只显示已运行时长。浮窗收起、任务结束、任务掉出前 2 名时都会关掉对应的流。
 */

/** 同时开实时日志流的任务数上限 */
const MAX_STREAMS = 2
/** 每个任务显示的日志尾部行数 */
const TAIL_LINES = 4
/** 每个任务只留这么多字节的尾巴来切行：连上时服务端会把整份历史一次推过来，可能有几 MB */
const TAIL_CHARS = 8192
/** 服务端说「任务还在跑，请重连」时，最多连续重连几次（防止一直回 reconnect 时变成重连风暴） */
const MAX_RECONNECT = 3
/** 任务结束后等多久再刷新列表：让用户看清最后几行和「已结束」再收走这一项 */
const ENDED_LINGER_MS = 2500

// 终端颜色等控制序列（CSI 与 OSC），浮窗只显示纯文本
const ANSI_PATTERN = new RegExp('\\u001b\\[[0-9;?]*[ -/]*[@-~]|\\u001b\\][^\\u0007\\u001b]*(?:\\u0007|\\u001b\\\\)', 'g')

type StreamState = 'connecting' | 'live' | 'ended' | 'error'

interface StreamView {
  state: StreamState
  /** 开流时这个任务正在写的日志 id：已结束的任务又跑了一次（log_id 变了）时据此重新开流 */
  logId: number | null
  /** key 是这一行在整份日志里的行号：进度条原地刷新时 key 不变，不会重放入场动画 */
  lines: Array<{ key: number; text: string }>
}

const ui = useUiStore()
const running = useRunningStore()
const authStore = useAuthStore()
const { isMobile } = useResponsive()

const canStop = computed(() => canOperate(authStore.user?.role))

// ---- 实时日志流 ----

// 连接对象与原始尾巴不需要响应式，只有渲染用的 views 是响应式的
const connections = new Map<number, EventStreamConnection>()
const tails = new Map<number, { text: string; newlines: number }>()
const reconnects = new Map<number, number>()
const reconnectTimers = new Map<number, ReturnType<typeof setTimeout>>()
const views = reactive<Record<number, StreamView>>({})
const stopping = reactive<Record<number, boolean>>({})

// 日志很密的任务一秒能来上百条消息，切行放到下一帧统一做，一帧最多算一次
const dirtyIds = new Set<number>()
let flushRaf = 0
let endedRefreshTimer: ReturnType<typeof setTimeout> | null = null

/** 应该开着实时日志的任务：浮窗展开时列表的前 2 个 */
const streamItems = computed(() => (ui.dockOpen ? running.items.slice(0, MAX_STREAMS) : []))

function closeConnection(taskId: number) {
  connections.get(taskId)?.close()
  connections.delete(taskId)
  const timer = reconnectTimers.get(taskId)
  if (timer) {
    clearTimeout(timer)
    reconnectTimers.delete(taskId)
  }
}

function openStream(taskId: number, logId: number | null) {
  // 重连时服务端会把整份历史重新推一遍，所以尾巴从零开始
  tails.set(taskId, { text: '', newlines: 0 })
  if (!views[taskId]) {
    views[taskId] = { state: 'connecting', logId, lines: [] }
  } else {
    views[taskId].state = 'connecting'
    views[taskId].logId = logId
  }

  const connection = openAuthorizedEventStream(`/api/v1/logs/${taskId}/stream`, {
    onOpen() {
      const view = views[taskId]
      if (view) view.state = 'live'
    },
    onMessage(data) {
      // 有数据说明连接是健康的，重连计数清零
      reconnects.delete(taskId)
      appendChunk(taskId, data)
    },
    onEvent(event) {
      if (event.event !== 'done') return
      closeConnection(taskId)
      const view = views[taskId]
      if (!view) return

      // reconnect：任务其实还在跑（面板刚重启、日志刚建好等），稍后重连续流
      if (event.data === 'reconnect') {
        const tried = (reconnects.get(taskId) ?? 0) + 1
        reconnects.set(taskId, tried)
        if (tried > MAX_RECONNECT) {
          view.state = 'error'
          return
        }
        reconnectTimers.set(taskId, setTimeout(() => {
          reconnectTimers.delete(taskId)
          const item = streamItems.value.find((row) => row.task_id === taskId)
          if (item) openStream(taskId, item.log_id)
        }, 1000))
        return
      }

      // finished / finished-late / 其它未知值：都当作任务已结束（与 LogViewer 的兜底一致）
      flushNow()
      view.state = 'ended'
      scheduleRefreshAfterEnded()
    },
    onError() {
      closeConnection(taskId)
      const view = views[taskId]
      if (view) view.state = 'error'
    },
  })
  connections.set(taskId, connection)
}

function appendChunk(taskId: number, data: string) {
  const tail = tails.get(taskId)
  if (!tail) return
  // 行号要按整份日志算（尾巴会被截断），所以换行数在截断前累计
  let index = data.indexOf('\n')
  while (index !== -1) {
    tail.newlines += 1
    index = data.indexOf('\n', index + 1)
  }
  tail.text = data.length >= TAIL_CHARS ? data.slice(-TAIL_CHARS) : (tail.text + data).slice(-TAIL_CHARS)

  dirtyIds.add(taskId)
  if (!flushRaf) {
    flushRaf = requestAnimationFrame(() => {
      flushRaf = 0
      flushNow()
    })
  }
}

function flushNow() {
  for (const taskId of dirtyIds) {
    const tail = tails.get(taskId)
    const view = views[taskId]
    if (!tail || !view) continue
    const parts = tail.text.replace(ANSI_PATTERN, '').split('\n')
    // parts 的最后一段对应整份日志的第 newlines 行（从 0 数）
    const base = tail.newlines - (parts.length - 1)
    const lines: StreamView['lines'] = []
    for (let i = parts.length - 1; i >= 0 && lines.length < TAIL_LINES; i -= 1) {
      // 进度条用裸 \r 回到行首覆盖：一行里有多段时显示最后一段非空的内容
      const segments = (parts[i] ?? '').split('\r')
      let text = ''
      for (let j = segments.length - 1; j >= 0; j -= 1) {
        if ((segments[j] ?? '').trim()) {
          text = segments[j] ?? ''
          break
        }
      }
      if (!text) continue
      lines.unshift({ key: base + i, text })
    }
    view.lines = lines
  }
  dirtyIds.clear()
}

// 任务结束后稍等再刷新列表；几个任务前后脚结束时只刷一次
function scheduleRefreshAfterEnded() {
  if (endedRefreshTimer) return
  endedRefreshTimer = setTimeout(() => {
    endedRefreshTimer = null
    void running.refresh()
  }, ENDED_LINGER_MS)
}

// 每次列表或展开状态变化都对一遍账：多出来的流关掉，缺的流补开。
// 已结束 / 出错的任务不自动重开（出错的要收起再展开浮窗才会重试，避免断网时反复重连）；
// 例外是已结束的任务紧接着又跑了一次（log_id 换了），那是新的一次执行，重新开流。
watch(
  streamItems,
  (items) => {
    const ids = items.map((item) => item.task_id)
    for (const key of Object.keys(views)) {
      const taskId = Number(key)
      if (ids.includes(taskId)) continue
      closeConnection(taskId)
      tails.delete(taskId)
      reconnects.delete(taskId)
      delete views[taskId]
    }
    for (const item of items) {
      const view = views[item.task_id]
      if (view?.state === 'ended' && item.log_id != null && item.log_id !== view.logId) {
        openStream(item.task_id, item.log_id)
        continue
      }
      if (view && (view.state === 'ended' || view.state === 'error')) continue
      if (connections.has(item.task_id) || reconnectTimers.has(item.task_id)) continue
      openStream(item.task_id, item.log_id)
    }
  },
  { immediate: true }
)

// ---- 已运行时长：只在浮窗展开时每秒走一次 ----

const now = ref(Date.now())
let clockTimer: ReturnType<typeof setInterval> | null = null

watch(
  () => ui.dockOpen,
  (open) => {
    if (clockTimer) {
      clearInterval(clockTimer)
      clockTimer = null
    }
    if (!open) return
    now.value = Date.now()
    clockTimer = setInterval(() => {
      now.value = Date.now()
    }, 1000)
  },
  { immediate: true }
)

function elapsedText(item: TaskRunningItem) {
  if (!item.started_at) return '-'
  const startedAt = new Date(item.started_at).getTime()
  if (!Number.isFinite(startedAt)) return '-'
  // 取整秒：每秒跳一次的计时器带小数位会一直闪
  return formatDuration(Math.max(0, Math.floor((now.value - startedAt) / 1000)))
}

async function handleStop(item: TaskRunningItem) {
  if (!canStop.value || stopping[item.task_id]) return
  try {
    await ElMessageBox.confirm(`确认停止任务「${item.name}」吗？`, '停止确认', { type: 'warning' })
  } catch {
    return
  }
  stopping[item.task_id] = true
  try {
    await taskApi.stop(item.task_id)
    ElMessage.success('任务已停止')
    await running.refresh()
  } catch (err) {
    if (!isCancel(err)) ElMessage.error(extractError(err, '停止失败'))
  } finally {
    delete stopping[item.task_id]
  }
}

// 桌面端顶栏已常驻 .header-runcap 入口，收起态不再于右下角渲染冗余悬浮胶囊，彻底避免遮挡页面底部分页栏；
// 仅在移动端收起态且有运行中任务时显示底部胶囊，方便单手展开。
const showPill = computed(() => isMobile.value && !ui.dockOpen && running.count > 0)
const pillText = computed(() => (running.count > 0 ? `${running.count} 个任务运行中` : '没有运行中的任务'))
const restCount = computed(() => Math.max(0, running.count - MAX_STREAMS))

onBeforeUnmount(() => {
  for (const taskId of [...connections.keys(), ...reconnectTimers.keys()]) {
    closeConnection(taskId)
  }
  if (flushRaf) cancelAnimationFrame(flushRaf)
  if (endedRefreshTimer) clearTimeout(endedRefreshTimer)
  if (clockTimer) clearInterval(clockTimer)
})
</script>

<template>
  <Teleport to="body">
    <div class="running-dock" :class="{ 'is-open': ui.dockOpen, 'is-mobile': isMobile }">
      <Transition name="running-dock-panel">
        <section v-if="ui.dockOpen" class="running-dock__panel" aria-label="运行中的任务">
          <header class="running-dock__head">
            <span class="running-dock__dot" :class="{ 'is-running': running.count > 0 }" />
            <span class="running-dock__title">运行中的任务</span>
            <span v-if="running.count > 0" class="running-dock__hint">实时日志尾部</span>
            <button type="button" class="running-dock__icon-btn" title="收起" aria-label="收起运行中浮窗" @click="ui.closeDock()">
              <el-icon><Close /></el-icon>
            </button>
          </header>

          <div v-if="running.count === 0" class="running-dock__empty">当前没有任务在运行。</div>

          <div v-else class="running-dock__list">
            <article v-for="(item, index) in running.items" :key="item.task_id" class="running-dock__item">
              <div class="running-dock__row">
                <span class="running-dock__dot" :class="views[item.task_id]?.state === 'ended' ? 'is-ended' : 'is-running'" />
                <router-link class="running-dock__name" :to="{ path: '/logs', query: { task_id: String(item.task_id) } }" :title="`查看「${item.name}」的日志`">
                  {{ item.name }}
                </router-link>
                <span class="running-dock__elapsed">{{ elapsedText(item) }}</span>
                <button
                  v-if="canStop && views[item.task_id]?.state !== 'ended'"
                  type="button"
                  class="running-dock__icon-btn is-danger"
                  title="停止"
                  :aria-label="`停止任务「${item.name}」`"
                  :disabled="!!stopping[item.task_id]"
                  @click="handleStop(item)"
                >
                  <el-icon><VideoPause /></el-icon>
                </button>
              </div>

              <!-- 只有前 2 个任务有实时日志，其余只显示已运行时长 -->
              <template v-if="index < MAX_STREAMS">
                <div v-if="views[item.task_id]?.state !== 'ended'" class="running-dock__progress"><i /></div>
                <div class="running-dock__log">
                  <div v-for="line in views[item.task_id]?.lines ?? []" :key="line.key" class="running-dock__line">{{ line.text }}</div>
                  <div v-if="views[item.task_id]?.state === 'ended'" class="running-dock__status is-ended">已结束</div>
                  <div v-else-if="views[item.task_id]?.state === 'error'" class="running-dock__status is-error">实时日志连接中断，收起后再展开可重试</div>
                  <div v-else-if="!(views[item.task_id]?.lines.length)" class="running-dock__status">等待日志输出…</div>
                </div>
              </template>
            </article>
            <div v-if="restCount > 0" class="running-dock__footnote">另外 {{ restCount }} 个任务只显示已运行时长</div>
          </div>
        </section>
      </Transition>

      <button v-if="showPill" type="button" class="running-dock__pill dd-glass" :aria-expanded="ui.dockOpen" @click="ui.toggleDock()">
        <span class="running-dock__dot" :class="{ 'is-running': running.count > 0 }" />
        <span>{{ pillText }}</span>
        <el-icon class="running-dock__chevron"><ArrowUp /></el-icon>
      </button>
    </div>
  </Teleport>
</template>

<style scoped lang="scss">
.running-dock {
  position: fixed;
  // 桌面端锚定在右上角顶栏下方（顶栏高 68px，额外留 6px 间隙），完全避开页面底部分页栏与右下角按钮
  top: calc(74px + var(--dd-demo-banner-height, 0px) + var(--dd-safe-banner-h, 0px));
  right: 20px;
  bottom: auto;
  // 低于 Element Plus 的弹窗 / 消息框（从 2000 起），停止确认框要盖在浮窗上面
  z-index: 1500;
  width: 390px;
  max-width: calc(100vw - 32px);
  display: flex;
  flex-direction: column;
  align-items: flex-end;
  gap: 10px;
  // 容器本身不挡点击，只有面板和胶囊能点
  pointer-events: none;

  > * {
    pointer-events: auto;
  }

  &.is-mobile {
    top: auto;
    right: 12px;
    // 移动端预留底部安全区，避免悬浮胶囊与展开浮窗贴底被系统手势条遮挡
    bottom: calc(12px + env(safe-area-inset-bottom));
    width: calc(100vw - 24px);
    max-width: none;
  }
}

// 仪表板大屏模式（G3 在 <html> 上挂 dd-screen-mode）：浮窗是 Teleport 到 body 的，
// 不受大屏对侧栏 / 顶栏的隐藏影响，这里自己藏起来。只藏不改 ui.dockOpen，退出大屏后恢复原来的展开状态。
html.dd-screen-mode .running-dock {
  display: none;
}

.running-dock__panel {
  width: 100%;
  // 使用浮层令牌（接近不透明），避免玻璃主题下底层表格文字透叠进实时日志
  background: var(--dd-overlay);
  border: var(--dd-border-width, 1px) solid var(--dd-border);
  border-radius: var(--dd-radius-surface);
  box-shadow: var(--dd-shadow-popper);
  overflow: hidden;
  transform-origin: top right;
  color: var(--dd-text);
}

.running-dock.is-mobile .running-dock__panel {
  transform-origin: bottom right;
}

.running-dock__head {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 14px 12px 8px 16px;
  font-weight: 600;
}

.running-dock__title {
  min-width: 0;
}

.running-dock__hint {
  margin-left: auto;
  font-size: 12px;
  font-weight: 400;
  color: var(--dd-text-3);
}

.running-dock__icon-btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  flex: none;
  width: 28px;
  height: 28px;
  padding: 0;
  border: none;
  border-radius: var(--dd-radius-control);
  background: transparent;
  color: var(--dd-text-2);
  cursor: pointer;
  transition: background-color 0.2s, color 0.2s;

  &:hover {
    background: var(--dd-surface-3);
    color: var(--dd-text);
  }

  &.is-danger:hover {
    color: var(--dd-err-ink);
  }

  &:disabled {
    opacity: 0.5;
    cursor: not-allowed;
  }

  &:focus-visible {
    outline: 2px solid var(--dd-accent-ring);
    outline-offset: 1px;
  }
}

.running-dock__head .running-dock__icon-btn {
  margin-left: 4px;
}

.running-dock__head .running-dock__title + .running-dock__icon-btn {
  margin-left: auto;
}

.running-dock__empty {
  padding: 8px 16px 16px;
  font-size: 13px;
  color: var(--dd-text-3);
}

.running-dock__list {
  max-height: min(60dvh, 520px);
  overflow-y: auto;
  overscroll-behavior: contain;
}

.running-dock__item {
  padding: 10px 12px 12px 16px;
  border-top: 1px solid var(--dd-divider, var(--el-border-color-lighter));
}

.running-dock__row {
  display: flex;
  align-items: center;
  gap: 10px;
  min-height: 28px;
}

.running-dock__name {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-weight: 600;
  color: var(--dd-text);
  text-decoration: none;

  &:hover {
    color: var(--dd-accent-ink);
  }
}

.running-dock__elapsed {
  flex: none;
  font-family: var(--dd-font-mono);
  font-size: 12px;
  color: var(--dd-text-3);
  font-variant-numeric: tabular-nums;
}

.running-dock__dot {
  position: relative;
  flex: none;
  width: 9px;
  height: 9px;
  border-radius: 50%;
  background: var(--dd-text-3);

  &.is-running {
    background: var(--dd-accent);

    // 运行中的涟漪；关闭档由全局规则停掉动画
    &::after {
      content: "";
      position: absolute;
      inset: 0;
      border-radius: 50%;
      background: var(--dd-accent);
      animation: running-dock-ripple 1.6s var(--dd-ease-io, ease-out) infinite;
    }
  }

  &.is-ended {
    background: var(--dd-ok);
  }
}

.running-dock__progress {
  height: 4px;
  margin: 8px 0;
  border-radius: var(--dd-radius-pill);
  background: var(--dd-surface-3);
  overflow: hidden;

  i {
    display: block;
    width: 40%;
    height: 100%;
    border-radius: var(--dd-radius-pill);
    background: linear-gradient(90deg, transparent, var(--dd-accent), var(--dd-accent-2), transparent);
    animation: running-dock-indeterminate 1.6s var(--dd-ease-io, ease-in-out) infinite;
  }
}

.running-dock__log {
  display: flex;
  flex-direction: column;
  justify-content: flex-end;
  // 固定 4 行高，日志滚动时浮窗不跳
  height: calc(11.5px * 1.65 * 4);
  overflow: hidden;
  font-family: var(--dd-font-mono);
  font-size: 11.5px;
  line-height: 1.65;
  color: var(--dd-text-2);
}

.running-dock__line {
  // 显式指定次要文字色，确保经典亮色等主题下实时日志对比度 ≥ 4.5:1
  color: var(--dd-text-2);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: pre;
  animation: running-dock-line-in 0.45s var(--dd-ease-decelerate, ease-out) both;
}

.running-dock__status {
  color: var(--dd-text-2);

  &.is-ended {
    color: var(--dd-ok-ink);
  }

  &.is-error {
    color: var(--dd-err-ink);
  }
}

.running-dock__footnote {
  padding: 8px 16px 12px;
  border-top: 1px solid var(--dd-divider, var(--el-border-color-lighter));
  font-size: 12px;
  color: var(--dd-text-3);
}

.running-dock__pill {
  display: inline-flex;
  align-items: center;
  gap: 10px;
  height: 46px;
  padding: 0 16px 0 14px;
  border: var(--dd-border-width, 1px) solid var(--dd-border);
  border-radius: var(--dd-radius-pill, 999px);
  box-shadow: var(--dd-shadow-popper);
  color: var(--dd-text);
  font: inherit;
  font-weight: 600;
  cursor: pointer;

  &:focus-visible {
    outline: 2px solid var(--dd-accent-ring);
    outline-offset: 2px;
  }
}

.running-dock__chevron {
  transition: transform 0.5s var(--dd-spring, ease);
}

.running-dock.is-open .running-dock__chevron {
  transform: rotate(180deg);
}

// 桌面端从右上角顶栏下方滑落展开；移动端从底部滑出展开
.running-dock-panel-enter-active,
.running-dock-panel-leave-active {
  transition: opacity 0.3s ease, transform 0.5s var(--dd-spring-soft, ease);
}

.running-dock-panel-enter-from,
.running-dock-panel-leave-to {
  opacity: 0;
  transform: translateY(-10px) scale(0.97);
}

.running-dock.is-mobile .running-dock-panel-enter-from,
.running-dock.is-mobile .running-dock-panel-leave-to {
  transform: translateY(12px) scale(0.97);
}

@keyframes running-dock-ripple {
  from {
    opacity: 0.6;
    transform: scale(1);
  }
  to {
    opacity: 0;
    transform: scale(2.6);
  }
}

@keyframes running-dock-indeterminate {
  from {
    transform: translateX(-100%);
  }
  to {
    transform: translateX(260%);
  }
}

@keyframes running-dock-line-in {
  from {
    opacity: 0;
    transform: translateY(8px);
  }
}
</style>
