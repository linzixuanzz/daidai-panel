<script setup lang="ts">
/**
 * 未来 24 小时调度时间轴（design §5 F4）。自绘 SVG，不用 echarts。
 *
 * 结构（从上到下）：每小时调度次数柱 + 高峰标注 → 每个任务一条泳道（每个调度点一根小竖线）→ 时间刻度。
 * - 超过 12 个任务时：显示最忙的 11 个 + 「其余 N 个」合并泳道（合并泳道不可点）；
 * - 悬停：竖向参考线 + 提示框列出前后 12 分钟内要跑的任务；
 * - 点泳道名：跳到定时任务页并按名称定位（任务页已支持 ?keyword=）；
 * - 连续失败的任务（来自「需要关注」）调度点画成红色。
 * 数据来自 GET /tasks/upcoming（Unix 秒），刻度完全按服务端 pkg/cron 的计算结果画，前端不自己推算 cron。
 * 颜色全部走 CSS 类读 --dd-* 令牌，切主题不需要重画。
 */
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { taskApi, type TaskUpcoming } from '@/api/task'
import { useAttentionStore } from '@/stores/attention'
import { usePageActivity } from '@/composables/usePageActivity'

/** 泳道上限：超过这个数就把最闲的那些合并成一条 */
const MAX_LANES = 12
/** 悬停时向前、向后各看多少分钟 */
const HOVER_WINDOW_MIN = 12
/** 后台自动重取的间隔：「现在」那条线会走，5 分钟重取一次足够 */
const AUTO_REFRESH_MS = 5 * 60_000

const router = useRouter()
const attention = useAttentionStore()
const { isPageActive } = usePageActivity()

const data = ref<TaskUpcoming | null>(null)
const loading = ref(false)
const failed = ref(false)

const wrapRef = ref<HTMLElement | null>(null)
const width = ref(760)
let resizeObserver: ResizeObserver | null = null
let timer: ReturnType<typeof setInterval> | null = null

const hover = ref<{ x: number; y: number; minute: number } | null>(null)

async function refresh() {
  loading.value = true
  try {
    data.value = await taskApi.upcoming(24)
    failed.value = false
  } catch {
    // 拉取失败保留上一次的图，只在从来没拿到过时显示失败提示
    failed.value = !data.value
  } finally {
    loading.value = false
  }
}

defineExpose({ refresh })

/** 连续失败的任务 id（调度点画红色） */
const failingIds = computed(() => {
  const set = new Set<number>()
  for (const item of attention.items) {
    if (item.type === 'task_failing' && item.target?.id) set.add(item.target.id)
  }
  return set
})

// —— 几何参数：宽度跟随容器（ResizeObserver），viewBox 与像素 1:1，文字不会被缩放糊掉 ——
const NAME_W = computed(() => (width.value < 520 ? 86 : 118))
const LANE_H = 22
const TOP_H = 70
const AXIS_H = 24
const RIGHT_PAD = 8

const totalMinutes = computed(() => {
  if (!data.value) return 1440
  return Math.max(60, Math.round((data.value.to - data.value.from) / 60))
})

const plotW = computed(() => Math.max(60, width.value - NAME_W.value - RIGHT_PAD))

function xOf(minute: number) {
  return NAME_W.value + (minute / totalMinutes.value) * plotW.value
}

/** 泳道：超过上限时取最忙的 11 个（保持服务端顺序显示），其余合并 */
const lanes = computed(() => {
  const tasks = data.value?.tasks || []
  if (tasks.length <= MAX_LANES) {
    return tasks.map((t) => ({ id: t.id, name: t.name, times: t.times, merged: false }))
  }
  const busiest = new Set(
    [...tasks]
      .sort((a, b) => b.times.length - a.times.length)
      .slice(0, MAX_LANES - 1)
      .map((t) => t.id),
  )
  const kept = tasks.filter((t) => busiest.has(t.id)).map((t) => ({ id: t.id, name: t.name, times: t.times, merged: false }))
  const rest = tasks.filter((t) => !busiest.has(t.id))
  kept.push({
    id: 0,
    name: `其余 ${rest.length} 个`,
    times: rest.flatMap((t) => t.times),
    merged: true,
  })
  return kept
})

const height = computed(() => TOP_H + lanes.value.length * LANE_H + AXIS_H)

/** 每条泳道的刻度 x 坐标：同一像素上的多个调度点只画一根（每分钟跑一次的任务一天 1440 个点） */
const laneTicks = computed(() => {
  const from = data.value?.from || 0
  return lanes.value.map((lane) => {
    const xs = new Set<number>()
    for (const t of lane.times) {
      xs.add(Math.round(xOf((t - from) / 60)))
    }
    return [...xs]
  })
})

/** 名字太长时截断（SVG 文本没有省略号），中文按 12px 一个字估算 */
function clipName(name: string) {
  const max = Math.max(3, Math.floor((NAME_W.value - 14) / 12))
  return name.length > max ? `${name.slice(0, max - 1)}…` : name
}

const hourly = computed(() => data.value?.hourly || [])
const barW = computed(() => (hourly.value.length ? plotW.value / hourly.value.length : 0))
const barMax = computed(() => Math.max(1, ...hourly.value))
const peakIndex = computed(() => {
  if (!hourly.value.length) return -1
  const max = Math.max(...hourly.value)
  return max > 0 ? hourly.value.indexOf(max) : -1
})

function hhmm(unixSec: number) {
  const d = new Date(unixSec * 1000)
  return `${String(d.getHours()).padStart(2, '0')}:${String(d.getMinutes()).padStart(2, '0')}`
}

const peakLabel = computed(() => {
  if (peakIndex.value < 0 || !data.value) return ''
  return `高峰 ${hhmm(data.value.from + peakIndex.value * 3600)} 起 · ${hourly.value[peakIndex.value]} 次`
})

/** 底部时间刻度：每 3 小时一个，第一个写「现在」，最后补一个 +24h */
const hourTicks = computed(() => {
  if (!data.value) return []
  const out: Array<{ x: number; text: string; anchor: string }> = []
  const hours = Math.round(totalMinutes.value / 60)
  // 窄屏每 6 小时一个，免得字挤在一起
  const step = width.value < 520 ? 6 : 3
  for (let h = 0; h < hours; h += step) {
    out.push({ x: xOf(h * 60), text: h === 0 ? '现在' : hhmm(data.value.from + h * 3600), anchor: h === 0 ? 'start' : 'middle' })
  }
  out.push({ x: xOf(totalMinutes.value), text: `+${hours}h`, anchor: 'end' })
  return out
})

/** 悬停提示：前后 12 分钟内的调度点（全部任务，不只是泳道里的） */
const hoverHits = computed(() => {
  if (!hover.value || !data.value) return []
  const from = data.value.from
  const center = from + hover.value.minute * 60
  const hits: Array<{ time: number; name: string }> = []
  for (const task of data.value.tasks) {
    for (const t of task.times) {
      if (Math.abs(t - center) <= HOVER_WINDOW_MIN * 60) hits.push({ time: t, name: task.name })
    }
  }
  hits.sort((a, b) => a.time - b.time)
  return hits
})

const hoverTitle = computed(() => {
  if (!hover.value || !data.value) return ''
  return `${hhmm(data.value.from + hover.value.minute * 60)} 前后 ${HOVER_WINDOW_MIN} 分钟`
})

function handlePointerMove(e: PointerEvent) {
  const wrap = wrapRef.value
  if (!wrap) return
  const rect = wrap.getBoundingClientRect()
  const x = e.clientX - rect.left
  const y = e.clientY - rect.top
  if (x < NAME_W.value || x > NAME_W.value + plotW.value) {
    hover.value = null
    return
  }
  const minute = Math.round(((x - NAME_W.value) / plotW.value) * totalMinutes.value)
  hover.value = { x, y, minute }
}

/** 提示框位置：跟着指针，靠右时翻到左边，避免被卡片裁掉 */
const tipStyle = computed(() => {
  if (!hover.value) return {}
  const flip = hover.value.x > width.value - 230
  return {
    left: flip ? 'auto' : `${hover.value.x + 14}px`,
    right: flip ? `${width.value - hover.value.x + 14}px` : 'auto',
    top: `${Math.max(0, hover.value.y - 10)}px`,
  }
})

function openLane(lane: { id: number; name: string; merged: boolean }) {
  if (lane.merged) return
  void router.push({ path: '/tasks', query: { keyword: lane.name } })
}

onMounted(() => {
  void refresh()
  if (wrapRef.value) {
    resizeObserver = new ResizeObserver((entries) => {
      const w = Math.round(entries[0]?.contentRect.width || 0)
      // keep-alive 摘出文档时会量到 0，忽略
      if (w > 0) width.value = w
    })
    resizeObserver.observe(wrapRef.value)
  }
  timer = setInterval(() => {
    if (isPageActive.value) void refresh()
  }, AUTO_REFRESH_MS)
})

// 切回页面 / 标签页时「现在」那条线可能已经过时很久，回来就重取一次
watch(isPageActive, (active, prev) => {
  if (active && prev === false) void refresh()
})

onBeforeUnmount(() => {
  resizeObserver?.disconnect()
  resizeObserver = null
  if (timer) clearInterval(timer)
  timer = null
})
</script>

<template>
  <div ref="wrapRef" class="tl-wrap" :class="{ 'is-hover': !!hover }">
    <div v-if="!data && loading" class="tl-empty">正在推算调度…</div>
    <div v-else-if="failed" class="tl-empty">调度数据加载失败</div>
    <div v-else-if="data && data.tasks.length === 0" class="tl-empty">未来 24 小时没有要跑的定时任务</div>
    <svg
      v-else-if="data"
      :width="width"
      :height="height"
      :viewBox="`0 0 ${width} ${height}`"
      role="img"
      aria-label="未来 24 小时调度时间轴"
      @pointermove="handlePointerMove"
      @pointerleave="hover = null"
    >
      <!-- 每小时调度次数 -->
      <text class="tl-hour" :x="NAME_W - 10" :y="TOP_H - 14" text-anchor="end">每小时</text>
      <g class="tl-fade">
        <rect
          v-for="(c, h) in hourly"
          :key="'b' + h"
          class="tl-bar"
          :class="{ 'is-peak': h === peakIndex }"
          :x="NAME_W + h * barW + 1.5"
          :y="TOP_H - 10 - (c / barMax) * 34"
          :width="Math.max(1, barW - 3)"
          :height="Math.max(c > 0 ? 2 : 0, (c / barMax) * 34)"
          rx="2"
        />
      </g>
      <text
        v-if="peakLabel"
        class="tl-peak"
        :x="Math.min(width - 70, Math.max(NAME_W + 60, NAME_W + peakIndex * barW + barW / 2))"
        :y="TOP_H - 52"
        text-anchor="middle"
      >{{ peakLabel }}</text>

      <!-- 泳道 -->
      <g class="tl-fade">
        <g v-for="(lane, li) in lanes" :key="'l' + lane.id + '-' + li">
          <rect class="tl-lane" :x="NAME_W" :y="TOP_H + li * LANE_H + 4" :width="plotW" :height="LANE_H - 8" rx="3" />
          <text
            class="tl-name"
            :class="{ 'is-link': !lane.merged }"
            :x="NAME_W - 10"
            :y="TOP_H + li * LANE_H + 15"
            text-anchor="end"
            @click="openLane(lane)"
          >
            <title>{{ lane.merged ? lane.name : `${lane.name}（点击查看任务）` }}</title>
            {{ clipName(lane.name) }}
          </text>
          <rect
            v-for="x in laneTicks[li]"
            :key="x"
            class="tl-tick"
            :class="{ 'is-err': !lane.merged && failingIds.has(lane.id) }"
            :x="x - 1.5"
            :y="TOP_H + li * LANE_H + 5"
            width="3"
            :height="LANE_H - 10"
            rx="1.5"
          />
        </g>
      </g>

      <!-- 「现在」与悬停参考线 -->
      <line class="tl-now" :x1="NAME_W" :x2="NAME_W" :y1="TOP_H - 4" :y2="height - AXIS_H + 2" />
      <line
        v-if="hover"
        class="tl-guide"
        :x1="hover.x"
        :x2="hover.x"
        :y1="TOP_H - 4"
        :y2="height - AXIS_H + 2"
      />

      <!-- 时间刻度 -->
      <text
        v-for="(t, i) in hourTicks"
        :key="'h' + i"
        class="tl-hour"
        :x="t.x"
        :y="height - 6"
        :text-anchor="t.anchor"
      >{{ t.text }}</text>
    </svg>

    <div v-if="hover && data" class="tl-tip" :style="tipStyle">
      <b>{{ hoverTitle }}</b>
      <template v-if="hoverHits.length">
        <div v-for="(hit, i) in hoverHits.slice(0, 6)" :key="i" class="tl-tip__row">
          <span class="tl-tip__time">{{ hhmm(hit.time) }}</span>{{ hit.name }}
        </div>
        <div v-if="hoverHits.length > 6" class="tl-tip__more">…还有 {{ hoverHits.length - 6 }} 次</div>
      </template>
      <div v-else class="tl-tip__more">这段时间没有任务</div>
    </div>

    <div v-if="data?.truncated" class="tl-note">调度点太多，超过 5000 个的部分没有画出来</div>
  </div>
</template>

<style scoped lang="scss">
.tl-wrap {
  position: relative;
  min-width: 0;

  svg {
    display: block;
    max-width: 100%;
    overflow: visible;
    user-select: none;
  }
}

.tl-empty {
  display: grid;
  place-items: center;
  min-height: 160px;
  font-size: 13px;
  color: var(--dd-text-3);
}

.tl-name {
  font-size: 12px;
  fill: var(--dd-text-2);

  &.is-link {
    cursor: pointer;

    &:hover {
      fill: var(--dd-accent-ink);
      text-decoration: underline;
    }
  }
}

.tl-tick {
  fill: var(--dd-chart-ok);
  opacity: 0.85;

  &.is-err {
    fill: var(--dd-err);
  }
}

.tl-lane {
  fill: var(--dd-surface-3);
  opacity: 0.7;
}

.tl-bar {
  fill: var(--dd-accent);
  opacity: 0.28;

  &.is-peak {
    fill: var(--dd-accent-2);
    opacity: 0.8;
  }
}

.tl-peak {
  font-size: calc(11px * var(--dash-scale, 1));
  font-weight: 600;
  font-family: var(--dd-font-mono);
  fill: var(--dd-accent-2-ink);
}

.tl-now {
  stroke: var(--dd-accent-2);
  stroke-width: 2;
  stroke-dasharray: 4 4;
}

.tl-guide {
  stroke: var(--dd-text);
  stroke-width: 1;
  opacity: 0.45;
  pointer-events: none;
}

// 刻度与提示框跟着仪表板的大屏倍数放大（--dash-scale 由仪表板页提供，单独使用时为 1）
.tl-hour {
  font-size: calc(11px * var(--dash-scale, 1));
  font-family: var(--dd-font-mono);
  fill: var(--dd-text-3);
}

// 首次出现淡入；关闭档由全局规则统一去掉动画
.tl-fade {
  animation: dd-fade-in 0.8s var(--dd-ease-standard) both;
}

.tl-tip {
  position: absolute;
  z-index: 3;
  min-width: 160px;
  max-width: 240px;
  padding: 8px 10px;
  border-radius: var(--dd-radius-surface);
  background: var(--dd-overlay);
  border: 1px solid var(--dd-border);
  box-shadow: var(--dd-shadow-popper);
  font-size: calc(12px * var(--dash-scale, 1));
  line-height: 1.6;
  color: var(--dd-text);
  pointer-events: none;

  b {
    display: block;
    margin-bottom: 2px;
  }
}

.tl-tip__row {
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.tl-tip__time {
  font-family: var(--dd-font-mono);
  color: var(--dd-text-3);
  margin-right: 6px;
}

.tl-tip__more {
  color: var(--dd-text-3);
}

.tl-note {
  margin-top: 6px;
  font-size: 12px;
  color: var(--dd-warn-ink);
}
</style>
