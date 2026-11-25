<script setup lang="ts">
/**
 * 任务健康画像（v4.0 F5）。任务页桌面端的展开行、移动端的任务详情、仪表板健康卡（compact）共用。
 *
 * 契约（wave1-handoff §8）：props `taskId` `compact?`；自己调 `taskApi.health` / `latestLog` 取数，
 * 切换 taskId 时丢弃上一个请求。
 * - 完整版：13 周热力格 + 三项统计、耗时趋势（最近 20 次）、最近一次日志尾部。
 * - compact：只有热力格和三项统计，不请求 latest-log（那份日志正文可能有几十 MB，仪表板不该为它买单）。
 */
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { taskApi } from '@/api/task'
import type { TaskHealth } from '@/api/task'
import { formatDuration } from '@/utils/duration'

const props = withDefaults(defineProps<{
  taskId: number
  compact?: boolean
}>(), {
  compact: false,
})

// 热力格固定取 13 周（91 天，正好是接口的默认值）
const HEALTH_DAYS = 91
// 日志尾部显示的行数，以及为了取尾部最多从正文末尾截多少字符（正文可能很大，不能整份 split）
const LOG_TAIL_LINES = 8
const LOG_TAIL_CHARS = 6000
// ESC 开头的 ANSI 控制序列，以及 ESC 被吞掉后残留的 [31m 这类裸序列
const ANSI_PATTERN = /\x1b\[[0-9;?]*[A-Za-z]|\[(?:\d{1,3};)*\d{1,3}m/g

const health = ref<TaskHealth | null>(null)
const healthLoading = ref(false)
const healthError = ref('')

const latestLog = ref<any>(null)
const latestLogLoading = ref(false)
const latestLogError = ref('')

// 请求序号：taskId 一变就自增，回来的响应序号对不上就丢掉。
// taskApi.health 目前不收 AbortSignal（api/task.ts 本波不改），旧请求只能「回来后作废」；
// latest-log 收 signal，可以真正中断——它才是可能很大的那一份。
let loadSeq = 0
let latestLogAbort: AbortController | null = null

async function load() {
  const seq = ++loadSeq
  latestLogAbort?.abort()
  latestLogAbort = null

  health.value = null
  healthError.value = ''
  latestLog.value = null
  latestLogError.value = ''
  // taskId 为 0 / 非法时什么都不发（仪表板还没选出任务时会传 0）
  if (!props.taskId || props.taskId <= 0) {
    healthLoading.value = false
    latestLogLoading.value = false
    return
  }

  healthLoading.value = true
  taskApi.health(props.taskId, HEALTH_DAYS)
    .then((res) => {
      if (seq !== loadSeq) return
      health.value = res
    })
    .catch((err: any) => {
      if (seq !== loadSeq) return
      healthError.value = err?.response?.data?.error || '健康画像加载失败'
    })
    .finally(() => {
      if (seq === loadSeq) healthLoading.value = false
    })

  if (props.compact) {
    latestLogLoading.value = false
    return
  }
  const controller = typeof AbortController === 'undefined' ? null : new AbortController()
  latestLogAbort = controller
  latestLogLoading.value = true
  taskApi.latestLog(props.taskId, controller?.signal)
    .then((res) => {
      if (seq !== loadSeq) return
      latestLog.value = res
    })
    .catch((err: any) => {
      // 被自己中断的请求不算失败
      if (seq !== loadSeq || controller?.signal.aborted) return
      // 404 时服务端会给出「还没有日志 / 已按保留策略清理」两种说法，原样透出
      latestLogError.value = err?.response?.data?.error || '最近一次日志加载失败'
    })
    .finally(() => {
      if (seq === loadSeq) latestLogLoading.value = false
    })
}

watch(() => [props.taskId, props.compact], () => {
  void load()
}, { immediate: true })

onBeforeUnmount(() => {
  // 卸载后回来的响应一律作废
  loadSeq++
  latestLogAbort?.abort()
  latestLogAbort = null
})

// ---- 热力格 ----
// 骨架屏按今天倒推 HEALTH_DAYS 天的起点补齐周一前置空格，
// 使加载态与真实日历列数（13 或 14 列）保持一致，消除加载完成时的高度跳动。
const skeletonCells = computed(() => {
  const start = new Date()
  start.setDate(start.getDate() - (HEALTH_DAYS - 1))
  const offset = (start.getDay() + 6) % 7
  const cells: Array<{ key: string; pad: boolean }> = []
  for (let i = 0; i < offset; i++) {
    cells.push({ key: `sk-pad-${i}`, pad: true })
  }
  for (let i = 0; i < HEALTH_DAYS; i++) {
    cells.push({ key: `sk-${i}`, pad: false })
  }
  return cells
})

// 每列一周、每行一个星期几（周一在最上面）。第一天前面补空格，让同一行永远是同一个星期几。
const heatCells = computed(() => {
  const days = health.value?.days || []
  const firstDay = days[0]
  if (!firstDay) return []
  const [y = NaN, m = 1, d = 1] = firstDay.date.split('-').map(Number)
  const first = new Date(y, m - 1, d)
  const offset = Number.isNaN(first.getTime()) ? 0 : (first.getDay() + 6) % 7
  const cells: { key: string; level: string; title: string; index: number }[] = []
  for (let i = 0; i < offset; i++) {
    cells.push({ key: `pad-${i}`, level: 'pad', title: '', index: i })
  }
  days.forEach((day, i) => {
    const [, mm, dd] = day.date.split('-')
    const label = `${Number(mm)}月${Number(dd)}日`
    let level = 'none'
    let title = `${label} · 没有运行`
    if (day.total > 0) {
      const ratio = day.failed / day.total
      // 颜色按当天失败比例：全成功 / 少量失败（< 50%）/ 多半失败
      if (ratio === 0) level = 'ok'
      else if (ratio < 0.5) level = 'warn'
      else level = 'err'
      title = day.failed > 0
        ? `${label} · 运行 ${day.total} 次，失败 ${day.failed} 次`
        : `${label} · 运行 ${day.total} 次，全部成功`
      if (day.avg_duration > 0) title += ` · 平均 ${formatDuration(day.avg_duration)}`
    }
    cells.push({ key: day.date, level, title, index: offset + i })
  })
  return cells
})

const failedDays = computed(() => (health.value?.days || []).filter(day => day.failed > 0).length)

// ---- 三项统计 ----
const statItems = computed(() => {
  const stats = health.value?.stats
  const hasRuns = !!stats && stats.runs > 0
  return [
    { key: 'rate', label: '成功率', value: hasRuns ? `${stats!.success_rate}%` : '—' },
    { key: 'avg', label: '平均耗时', value: hasRuns ? formatDuration(stats!.avg_duration, '—') : '—' },
    { key: 'p90', label: 'P90 耗时', value: hasRuns ? formatDuration(stats!.p90_duration, '—') : '—' },
  ]
})

// 成功率低于 90% 时把数字染成警示色、低于 60% 染成危险色（墨色，保证对比度）
const rateTone = computed(() => {
  const stats = health.value?.stats
  if (!stats || stats.runs === 0) return ''
  if (stats.success_rate < 60) return 'is-err'
  if (stats.success_rate < 90) return 'is-warn'
  return ''
})

// ---- 耗时趋势 ----
// SVG 用 300×60 的坐标系、preserveAspectRatio="none" 拉满宽度；圆点改用 HTML 绝对定位叠上去，
// 否则会跟着 SVG 被横向拉成椭圆。
const TREND_W = 300
const TREND_H = 60
const trend = computed(() => {
  const runs = (health.value?.recent || []).filter(run => typeof run.duration === 'number')
  if (runs.length < 2) return null
  const max = Math.max(...runs.map(run => run.duration as number), 0.001)
  const points = runs.map((run, i) => {
    const x = (i / (runs.length - 1)) * TREND_W
    // 上下各留 4 个单位，线不贴边
    const y = TREND_H - 4 - ((run.duration as number) / max) * (TREND_H - 8)
    return { x, y, run }
  })
  const line = points.map((p, i) => `${i === 0 ? 'M' : 'L'}${p.x.toFixed(1)},${p.y.toFixed(1)}`).join(' ')
  const area = `${line} L${TREND_W},${TREND_H} L0,${TREND_H} Z`
  const dots = points.map((p, i) => ({
    key: i,
    left: `${(p.x / TREND_W) * 100}%`,
    top: `${(p.y / TREND_H) * 100}%`,
    tone: runTone(p.run.status),
    title: `${runStatusText(p.run.status)} · ${formatDuration(p.run.duration)}`,
  }))
  return { line, area, dots, count: runs.length }
})

function runTone(status: number) {
  if (status === 0) return 'ok'
  if (status === 1) return 'err'
  return 'warn'
}

// 日志状态：0 成功 / 1 失败 / 2 运行中 / 3 已终止（超时或手动停止）
function runStatusText(status: number | null | undefined) {
  if (status === 0) return '成功'
  if (status === 1) return '失败'
  if (status === 2) return '运行中'
  if (status === 3) return '已终止'
  return '未知'
}

// ---- 最近一次日志尾部 ----
const logTail = computed(() => {
  const content = latestLog.value?.content
  if (typeof content !== 'string' || content === '') return ''
  const sliced = content.length > LOG_TAIL_CHARS
  let text = sliced ? content.slice(-LOG_TAIL_CHARS) : content
  // 截断时第一行多半是半截，丢掉
  if (sliced) {
    const firstBreak = text.indexOf('\n')
    if (firstBreak >= 0) text = text.slice(firstBreak + 1)
  }
  const lines = text
    .replace(ANSI_PATTERN, '')
    .split('\n')
    .map(line => line.replace(/\r/g, '').trimEnd())
    .filter(line => line !== '')
  return lines.slice(-LOG_TAIL_LINES).join('\n')
})

const latestLogTone = computed(() => {
  const status = latestLog.value?.status
  if (status === 0) return 'ok'
  if (status === 1) return 'err'
  if (status === 2) return 'run'
  return 'muted'
})
</script>

<template>
  <div class="task-health" :class="{ 'task-health--compact': compact }">
    <div v-if="healthError" class="task-health__error">
      <span>{{ healthError }}</span>
      <el-button link type="primary" size="small" @click="load">重试</el-button>
    </div>

    <template v-else>
      <!-- 第一块：三项统计 + 13 周热力格（compact 版只有这一块） -->
      <section class="task-health__panel task-health__panel--heat">
        <header v-if="!compact" class="task-health__head">
          <span>近 13 周每天</span>
          <span class="task-health__faint">{{ failedDays > 0 ? `${failedDays} 天有失败` : '' }}</span>
        </header>
        <div class="task-health__stats">
          <div v-for="item in statItems" :key="item.key" class="task-health__stat">
            <span>{{ item.label }}</span>
            <b :class="item.key === 'rate' ? rateTone : ''">{{ healthLoading ? '…' : item.value }}</b>
          </div>
        </div>
        <div v-if="healthLoading" class="task-health__heat is-loading" aria-hidden="true">
          <span
            v-for="cell in skeletonCells"
            :key="cell.key"
            class="task-health__cell"
            :class="{ 'is-pad': cell.pad }"
          />
        </div>
        <div v-else class="task-health__heat" role="img" :aria-label="`近 13 周每天的运行结果，${failedDays} 天有失败`">
          <span
            v-for="cell in heatCells"
            :key="cell.key"
            class="task-health__cell"
            :class="`is-${cell.level}`"
            :style="{ '--thp-i': cell.index }"
            :title="cell.title || undefined"
          />
        </div>
        <footer class="task-health__legend">
          <span>每格 = 一天</span>
          <span class="task-health__legend-scale">
            <i class="task-health__cell is-none" />无运行
            <i class="task-health__cell is-ok" />成功
            <i class="task-health__cell is-warn" />部分失败
            <i class="task-health__cell is-err" />多半失败
          </span>
        </footer>
      </section>

      <template v-if="!compact">
        <!-- 第二块：耗时趋势 -->
        <section class="task-health__panel">
          <header class="task-health__head">
            <span>耗时趋势（最近 {{ trend?.count || 20 }} 次）</span>
            <span class="task-health__faint">均值 {{ statItems[1]?.value }}</span>
          </header>
          <div v-if="healthLoading" class="task-health__placeholder">加载中…</div>
          <div v-else-if="!trend" class="task-health__placeholder">运行次数不足，暂时画不出趋势</div>
          <div v-else class="task-health__trend">
            <svg :viewBox="`0 0 ${TREND_W} ${TREND_H}`" preserveAspectRatio="none" aria-hidden="true">
              <path class="task-health__trend-area" :d="trend.area" />
              <path class="task-health__trend-line" :d="trend.line" vector-effect="non-scaling-stroke" />
            </svg>
            <i
              v-for="dot in trend.dots"
              :key="dot.key"
              class="task-health__dot"
              :class="`is-${dot.tone}`"
              :style="{ left: dot.left, top: dot.top }"
              :title="dot.title"
            />
          </div>
        </section>

        <!-- 第三块：最近一次日志尾部 -->
        <section class="task-health__panel">
          <header class="task-health__head">
            <span>最近一次日志</span>
            <span v-if="latestLog" class="task-health__log-meta">
              <span class="task-health__log-status" :class="`is-${latestLogTone}`">{{ runStatusText(latestLog.status) }}</span>
              <span v-if="latestLog.id" class="task-health__faint">#{{ latestLog.id }}</span>
            </span>
          </header>
          <div v-if="latestLogLoading" class="task-health__placeholder">加载中…</div>
          <div v-else-if="latestLogError" class="task-health__placeholder">{{ latestLogError }}</div>
          <pre v-else-if="logTail" class="task-health__log">{{ logTail }}</pre>
          <div v-else class="task-health__placeholder">这次运行没有输出</div>
        </section>
      </template>
    </template>
  </div>
</template>

<style scoped lang="scss">
.task-health {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(min(260px, 100%), 1fr));
  gap: var(--dd-gap, 16px);
  min-width: 0;
  color: var(--dd-text);
  font-size: 13px;
}

// compact：只剩一块，不要卡片底，直接融进外层卡片
.task-health--compact {
  display: block;

  .task-health__panel {
    padding: 0;
    border: none;
    background: transparent;
  }
}

.task-health__panel {
  display: flex;
  flex-direction: column;
  gap: 10px;
  min-width: 0;
  padding: 14px;
  border: var(--dd-border-width, 1px) solid var(--dd-divider, var(--dd-border));
  border-radius: var(--dd-radius-control);
  background: var(--dd-surface-2);
}

.task-health__head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  font-size: 12.5px;
  font-weight: 600;
  color: var(--dd-text-2);
}

.task-health__faint {
  font-weight: 400;
  color: var(--dd-text-3);
}

.task-health__error {
  display: flex;
  align-items: center;
  gap: 8px;
  color: var(--dd-err-ink);
}

.task-health__stats {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 8px;

  b {
    display: block;
    margin-top: 2px;
    font-size: clamp(14px, 4vw, 18px);
    font-weight: 650;
    color: var(--dd-text);
    font-variant-numeric: tabular-nums;
    line-height: 1.3;
    white-space: nowrap;

    &.is-warn { color: var(--dd-warn-ink); }
    &.is-err { color: var(--dd-err-ink); }
  }
}

.task-health__stat {
  min-width: 0;
  font-size: 12px;
  color: var(--dd-text-3);
}

// 热力格：7 行（周一 → 周日），按列（周）往右排
.task-health__heat {
  display: grid;
  grid-template-rows: repeat(7, minmax(0, 1fr));
  grid-auto-flow: column;
  grid-auto-columns: minmax(0, 1fr);
  gap: 3px;
  max-width: 420px;

  // 加载中：按真实日历对齐前置空格占位，避免数据回来时整块跳高
  &.is-loading .task-health__cell {
    opacity: 0.6;
  }
}

.task-health__cell {
  display: block;
  aspect-ratio: 1;
  min-width: 0;
  // 直角模式下 --dd-radius-control 为 0，圆角模式最多收敛到 3px
  border-radius: min(3px, var(--dd-radius-control));
  background: var(--dd-surface-3);

  &.is-pad { background: transparent; }
  &.is-ok { background: color-mix(in srgb, var(--dd-ok) 78%, transparent); }
  &.is-warn { background: var(--dd-warn); }
  &.is-err { background: var(--dd-err); }
}

// 只有 rich 档做逐格弹入；subtle 档不做（关闭档由全局统一停掉）
html[data-dd-motion="rich"] .task-health__heat:not(.is-loading) .task-health__cell {
  animation: task-health-pop 0.45s var(--dd-spring) both;
  animation-delay: calc(var(--thp-i, 0) * 6ms);
}

@keyframes task-health-pop {
  from {
    opacity: 0;
    transform: scale(0.3);
  }
}

.task-health__legend {
  display: flex;
  align-items: center;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: 6px 12px;
  font-size: 12px;
  color: var(--dd-text-3);
}

.task-health__legend-scale {
  display: inline-flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 4px;

  .task-health__cell {
    width: 10px;
    margin-left: 6px;
  }

  .task-health__cell:first-child {
    margin-left: 0;
  }
}

.task-health__placeholder {
  display: flex;
  align-items: center;
  min-height: 84px;
  font-size: 12px;
  color: var(--dd-text-3);
}

.task-health__trend {
  position: relative;
  height: 84px;
  // 左右各内缩 5px（正好等于 10px 圆点半径），让首尾圆点与折线端点对齐且不探出面板内容区
  margin: 0 5px;

  svg {
    display: block;
    width: 100%;
    height: 100%;
    overflow: visible;
  }
}

.task-health__trend-area {
  fill: var(--dd-accent);
  opacity: 0.14;
}

.task-health__trend-line {
  fill: none;
  stroke: var(--dd-accent);
  stroke-width: 2;
  stroke-linejoin: round;
}

.task-health__dot {
  position: absolute;
  // 圆心落在折线的点上：总尺寸 10px（含 2px 描边），向左上各收回一半
  width: 10px;
  height: 10px;
  margin: -5px 0 0 -5px;
  box-sizing: border-box;
  border-radius: 50%;
  border: 2px solid var(--dd-surface-solid);

  &.is-ok { background: var(--dd-ok); }
  &.is-warn { background: var(--dd-warn); }
  &.is-err { background: var(--dd-err); }
}

.task-health__log-meta {
  display: inline-flex;
  align-items: center;
  gap: 6px;
}

.task-health__log-status {
  font-weight: 600;

  &.is-ok { color: var(--dd-ok-ink); }
  &.is-err { color: var(--dd-err-ink); }
  &.is-run { color: var(--dd-accent-ink); }
  &.is-muted { color: var(--dd-text-3); }
}

.task-health__log {
  margin: 0;
  max-height: 150px;
  overflow: auto;
  font-family: var(--dd-font-mono);
  font-size: 12px;
  line-height: 1.65;
  color: var(--dd-text-2);
  white-space: pre-wrap;
  word-break: break-all;
}
</style>
