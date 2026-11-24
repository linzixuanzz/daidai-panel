<script setup lang="ts">
import { onActivated, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import * as echarts from 'echarts/core'
import { LineChart } from 'echarts/charts'
import { GridComponent, TooltipComponent, LegendComponent } from 'echarts/components'
import { CanvasRenderer } from 'echarts/renderers'
import { motionLevel } from '@/utils/motion'

const props = defineProps<{
  stats: Array<{
    date?: string
    success?: number
    failed?: number
    aborted?: number
  }>
  /** 字号倍数：仪表板大屏模式传 1.25（design §5 F8），平时 1 */
  fontScale?: number
}>()

echarts.use([LineChart, GridComponent, TooltipComponent, LegendComponent, CanvasRenderer])

const chartRef = ref<HTMLElement>()
let chart: echarts.ECharts | null = null

// 尺寸跟随改用 ResizeObserver 盯容器本身，不再听 window 的 resize：
//   1. 侧栏收起 / 展开是 .layout-aside 的宽度过渡，不触发 window resize，原来图表宽度不跟着变；
//   2. 仪表板被 keep-alive 缓存时 DOM 被移进脱离文档的容器，这时窗口一变（移动端地址栏伸缩就会），
//      zrender 量到 0×0、把画布设成 0 尺寸；回到仪表板后没人再 resize，图表一直空白。
let resizeObserver: ResizeObserver | null = null
let resizeFrame = 0

// v4.0：配色改读主题令牌。主题 / 明暗 / 自定义主色都落在 <html> 的属性、类与内联样式上，
// 用一个 MutationObserver 盯住它们，变了就重读令牌重画（rAF 合并，一帧最多重画一次）。
let themeObserver: MutationObserver | null = null
let themeFrame = 0
let lastPaletteKey = ''

/**
 * 把一个 --dd-* 令牌解析成 [r, g, b, a]。
 * 令牌的值可能是 #hex、rgba()，也可能是 color-mix() —— echarts 的渐变要自己拼透明度，认不了 color-mix，
 * 所以先挂一个隐藏探针读出计算后的颜色，再画到 1×1 的 canvas 上取像素，任何浏览器认得的颜色格式都能归一成 rgba。
 */
function readTokenRgba(name: string, fallback: [number, number, number, number]): [number, number, number, number] {
  const probe = document.createElement('span')
  probe.style.cssText = `position:absolute;visibility:hidden;pointer-events:none;color:var(${name})`
  document.body.appendChild(probe)
  const computed = getComputedStyle(probe).color
  probe.remove()
  const canvas = document.createElement('canvas')
  canvas.width = 1
  canvas.height = 1
  const ctx = canvas.getContext('2d', { willReadFrequently: true })
  if (!ctx || !computed) return fallback
  ctx.clearRect(0, 0, 1, 1)
  ctx.fillStyle = computed
  ctx.fillRect(0, 0, 1, 1)
  const d = ctx.getImageData(0, 0, 1, 1).data
  if (d[3] === 0) return fallback
  return [d[0] ?? 0, d[1] ?? 0, d[2] ?? 0, Math.round(((d[3] ?? 255) / 255) * 100) / 100]
}

/** rgba 数组 → css 字符串；alpha 传了就覆盖令牌自带的透明度 */
function rgba(c: [number, number, number, number], alpha?: number) {
  return `rgba(${c[0]},${c[1]},${c[2]},${alpha ?? c[3]})`
}

function readPalette() {
  return {
    total: readTokenRgba('--dd-text-3', [140, 140, 140, 1]),
    ok: readTokenRgba('--dd-chart-ok', [16, 185, 129, 1]),
    err: readTokenRgba('--dd-err', [239, 68, 68, 1]),
    warn: readTokenRgba('--dd-warn', [245, 158, 11, 1]),
    text: readTokenRgba('--dd-text', [30, 30, 30, 1]),
    text3: readTokenRgba('--dd-text-3', [140, 140, 140, 1]),
    border: readTokenRgba('--dd-border', [220, 220, 220, 1]),
    overlay: readTokenRgba('--dd-overlay', [255, 255, 255, 1]),
    surface: readTokenRgba('--dd-surface-solid', [255, 255, 255, 1]),
  }
}

/**
 * 把容器尺寸同步给图表。
 *
 * rAF 合并：侧栏宽度过渡期间 ResizeObserver 会连续回调，一帧只 resize 一次。
 * 两种情况直接跳过：
 * - 量出来是 0（被 keep-alive 摘出文档、或祖先 display:none）：这时 resize 会把画布缩成 0×0；
 * - 尺寸与图表当前尺寸相同：observe() 之后 ResizeObserver 会立刻回调一次，这时首次加载的
 *   入场动画还在跑，resize 会以 0 时长重绘、把那段动画直接跳到终点。
 */
function scheduleResize() {
  if (resizeFrame) return
  resizeFrame = requestAnimationFrame(() => {
    resizeFrame = 0
    const el = chartRef.value
    if (!chart || !el) return
    const width = el.clientWidth
    const height = el.clientHeight
    if (width === 0 || height === 0) return
    if (width === chart.getWidth() && height === chart.getHeight()) return
    chart.resize()
  })
}

function renderChart() {
  if (!chartRef.value) return
  if (!chart) {
    chart = echarts.init(chartRef.value)
  }

  const p = readPalette()
  const fs = (px: number) => Math.round(px * (props.fontScale || 1))
  lastPaletteKey = JSON.stringify(p)
  const stats = props.stats || []
  const last = stats.length - 1

  // 「今天断崖」的处理：最后一个点是今天，当天还没过完，计数天然偏少。
  // 实线只画到昨天；昨天 → 今天这一段单独用一条同名虚线系列画出来，并在 X 轴把今天标成「今天·进行中」。
  // 同名系列在图例里合成一项、一起显隐；提示框按名字去重（见 formatter）。只有 1 个点时没有「昨天」，直接画实线。
  const splitToday = stats.length >= 2
  const metrics = [
    { name: '执行总数', color: p.total, width: 1.6, area: 0, pick: (s: (typeof stats)[number]) => (s.success || 0) + (s.failed || 0) + (s.aborted || 0) },
    { name: '成功', color: p.ok, width: 2.6, area: 0.34, pick: (s: (typeof stats)[number]) => s.success || 0 },
    { name: '失败', color: p.err, width: 2.2, area: 0.18, pick: (s: (typeof stats)[number]) => s.failed || 0 },
    // Aborted 是用户主动终止的独立状态，不再混进成功或失败。
    { name: '终止', color: p.warn, width: 2, area: 0, pick: (s: (typeof stats)[number]) => s.aborted || 0 },
  ]

  const series: any[] = []
  for (const m of metrics) {
    const values = stats.map(m.pick)
    const base = {
      name: m.name,
      type: 'line',
      smooth: 0.5,
      showSymbol: false,
      symbol: 'none',
      // 关掉高亮态：symbol 本来就是 none，高亮不带来任何可见信息，只会触发整图重绘。
      // 也不要改用 focus:'series'：带渐变面积时它会让 hover 每帧重绘整图，掉帧（#132）。
      emphasis: { disabled: true },
      itemStyle: { color: rgba(m.color, 1), borderWidth: 2, borderColor: rgba(p.surface, 1) },
    }
    series.push({
      ...base,
      data: splitToday ? values.map((v, i) => (i === last ? null : v)) : values,
      lineStyle: { width: m.width, color: rgba(m.color, 1) },
      areaStyle: m.area
        ? {
            color: new echarts.graphic.LinearGradient(0, 0, 0, 1, [
              { offset: 0, color: rgba(m.color, m.area) },
              { offset: 1, color: rgba(m.color, 0) },
            ]),
          }
        : undefined,
    })
    if (splitToday) {
      series.push({
        ...base,
        data: values.map((v, i) => (i >= last - 1 ? v : null)),
        lineStyle: { width: m.width, color: rgba(m.color, 0.85), type: [5, 5] },
        // 今天的点单独描一个空心圆，提示「数还在涨」
        showSymbol: true,
        symbol: (_v: unknown, params: { dataIndex: number }) => (params.dataIndex === last ? 'emptyCircle' : 'none'),
        symbolSize: 6,
      })
    }
  }

  chart.setOption(
    {
      // 动效档位不是 rich 时不播入场动画
      animation: motionLevel() === 'rich',
      tooltip: {
        trigger: 'axis',
        // hover 掉帧的根因是 ECharts 默认的三层交互动画叠在一起（#132），这里逐层关掉：
        //   ① transitionDuration 默认 0.4s：提示框位置走 CSS transform 过渡并被 50ms 节流，表现为拖影、跳格；
        //   ② axisPointer.animation：类目轴带宽 >15px 时指示线移动带 200ms 动画；
        //   ③ axisPointer.triggerEmphasis：每跨一个类目就对全部系列 highlight / downplay，整张 canvas 重绘。
        transitionDuration: 0,
        axisPointer: { type: 'line', animation: false, triggerEmphasis: false, lineStyle: { color: rgba(p.text3, 0.6), type: 'dashed' } },
        backgroundColor: rgba(p.overlay),
        borderColor: rgba(p.border),
        borderWidth: 1,
        textStyle: { color: rgba(p.text, 1), fontSize: fs(12) },
        // ECharts 把 tooltip 挂在 <body> 下，CSS 变量沿 :root 继承下来，圆角与阴影直接读令牌
        extraCssText: 'border-radius: var(--dd-radius-surface); box-shadow: var(--dd-shadow-popper);',
        // 拆出来的「今天」虚线系列与实线系列同名：同一天两条都有值时只列一次
        formatter: (params: any) => {
          const list = Array.isArray(params) ? params : [params]
          if (!list.length) return ''
          const idx = list[0].dataIndex
          const seen = new Set<string>()
          const rows: string[] = []
          for (const item of list) {
            // 空点（null）在不同版本里可能给成 null / '-' / NaN，一律跳过
            const empty = item.value == null || item.value === '-' || (typeof item.value === 'number' && Number.isNaN(item.value))
            if (empty || seen.has(item.seriesName)) continue
            seen.add(item.seriesName)
            rows.push(`${item.marker}${item.seriesName}&nbsp;&nbsp;<b>${item.value}</b>`)
          }
          const title = idx === last && splitToday ? `${list[0].axisValue}（今天，还没过完）` : list[0].axisValue
          return `<div style="margin-bottom:4px">${title}</div>${rows.join('<br/>')}`
        },
      },
      legend: {
        data: metrics.map((m) => m.name),
        // 图例色标用方块，与页面其余色标形状统一
        icon: 'rect',
        itemWidth: 8,
        textStyle: { fontSize: fs(12), color: rgba(p.text3, 1) },
        inactiveColor: rgba(p.border),
        top: 0,
      },
      // 这里改用 outerBounds 语义，避免新版 ECharts 对 containLabel 给出兼容性警告。
      grid: {
        left: '3%',
        right: '4%',
        bottom: '3%',
        top: 40,
        outerBoundsMode: 'same',
        outerBoundsContain: 'axisLabel',
      },
      xAxis: {
        type: 'category',
        data: stats.map((item) => item.date),
        axisLine: { lineStyle: { color: rgba(p.border) } },
        axisTick: { show: false },
        axisLabel: {
          color: rgba(p.text3, 1),
          fontSize: fs(11),
          formatter: (value: string, index: number) => (splitToday && index === last ? '今天·进行中' : value),
        },
      },
      yAxis: {
        type: 'value',
        minInterval: 1,
        axisLine: { show: false },
        splitLine: { lineStyle: { color: rgba(p.border), type: 'dashed' } },
        axisLabel: { color: rgba(p.text3, 1), fontSize: fs(11) },
      },
      series,
    },
    // 系列数会随「只有 1 天」与否变化，整体替换，避免残留旧系列
    { notMerge: true },
  )
}

function schedulePaletteCheck() {
  if (themeFrame) return
  themeFrame = requestAnimationFrame(() => {
    themeFrame = 0
    if (!chart) return
    // 只有令牌真的变了才重画（<html> 的 style 还会被别的东西改，例如圆角滑杆）
    if (JSON.stringify(readPalette()) !== lastPaletteKey) renderChart()
  })
}

watch(() => props.stats, renderChart, { deep: true })
watch(() => props.fontScale, renderChart)

onMounted(() => {
  renderChart()
  if (chartRef.value) {
    resizeObserver = new ResizeObserver(() => scheduleResize())
    resizeObserver.observe(chartRef.value)
  }
  themeObserver = new MutationObserver(() => schedulePaletteCheck())
  themeObserver.observe(document.documentElement, {
    attributes: true,
    attributeFilter: ['class', 'style', 'data-dd-theme'],
  })
})

// keep-alive 再次激活时补一次尺寸校正，并核对一次令牌（缓存期间可能切过主题，那时 rAF 不一定跑）
onActivated(() => {
  scheduleResize()
  schedulePaletteCheck()
})

onBeforeUnmount(() => {
  // keep-alive 缓存期间组件不会卸载，observer 只能在这里断开
  resizeObserver?.disconnect()
  resizeObserver = null
  themeObserver?.disconnect()
  themeObserver = null
  if (resizeFrame) {
    cancelAnimationFrame(resizeFrame)
    resizeFrame = 0
  }
  if (themeFrame) {
    cancelAnimationFrame(themeFrame)
    themeFrame = 0
  }
  chart?.dispose()
  chart = null
})
</script>

<template>
  <div ref="chartRef" class="trend-chart"></div>
</template>

<style scoped>
.trend-chart {
  height: 280px;
}
</style>
