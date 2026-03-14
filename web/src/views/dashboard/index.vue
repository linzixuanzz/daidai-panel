<template>
  <div class="dashboard-page animate-fade-in">
    <div class="page-header">
      <div>
        <h3 class="page-title">数据仪表</h3>
        <span class="page-subtitle">查看系统运行状态和统计数据</span>
      </div>
    </div>

    <el-row :gutter="16" class="stat-cards">
      <el-col :xs="12" :sm="12" :md="6" v-for="(card, i) in statCards" :key="card.label">
        <div
          class="stat-card animate-fade-in-up"
          :style="{ background: card.bg, animationDelay: `${i * 0.06}s` }"
          @click="$router.push(card.link)"
        >
          <div class="stat-card-content">
            <div class="stat-label">{{ card.label }}</div>
            <div class="stat-value" :style="{ color: card.color }">
              <CountUp :end-val="card.value" :duration="1.5" />
            </div>
          </div>
          <div class="stat-icon-area" :style="{ color: card.color }">
            <el-icon :size="20"><component :is="card.icon" /></el-icon>
            <el-icon :size="12"><ArrowRight /></el-icon>
          </div>
        </div>
      </el-col>
    </el-row>

    <el-row :gutter="16" style="margin-top: 16px">
      <el-col :xs="24" :lg="24">
        <el-card shadow="hover" class="chart-card animate-fade-in-up" style="animation-delay: 0.3s">
          <template #header>
            <div class="card-title-bar">
              <span class="title-dot" style="background: #409EFF"></span>
              <span class="title-text">执行统计</span>
              <span class="title-sub">最近7天任务执行情况</span>
            </div>
          </template>
          <div ref="trendChartRef" style="height: 280px"></div>
        </el-card>
      </el-col>
    </el-row>

    <el-row :gutter="16" style="margin-top: 16px">
      <el-col :xs="24" :lg="12">
        <el-card shadow="hover" class="chart-card animate-fade-in-up" style="animation-delay: 0.35s">
          <template #header>
            <div class="card-title-bar">
              <span class="title-dot" style="background: #E6A23C"></span>
              <span class="title-text">系统资源</span>
              <span class="title-sub">CPU / 内存 / 磁盘使用率</span>
            </div>
          </template>
          <el-row :gutter="24" style="padding: 8px 0">
            <el-col :span="8" v-for="item in resourceBars" :key="item.label">
              <div class="resource-item">
                <div class="resource-header">
                  <span class="resource-label">{{ item.label }}</span>
                  <span class="resource-value" :style="{ color: item.color }">
                    <CountUp :end-val="item.value" :duration="1" :decimals="1" suffix="%" />
                  </span>
                </div>
                <el-progress
                  :percentage="item.value"
                  :color="item.color"
                  :stroke-width="10"
                  :show-text="false"
                  style="margin-top: 8px"
                />
                <div class="resource-detail" v-if="item.detail">{{ item.detail }}</div>
              </div>
            </el-col>
          </el-row>
          <el-descriptions :column="2" border size="small" style="margin-top: 16px">
            <el-descriptions-item label="操作系统">{{ sysInfo.os }} {{ sysInfo.arch }}</el-descriptions-item>
            <el-descriptions-item label="Go版本">{{ sysInfo.go_version }}</el-descriptions-item>
            <el-descriptions-item label="CPU核心">{{ sysInfo.num_cpu }}</el-descriptions-item>
            <el-descriptions-item label="Goroutines">{{ sysInfo.goroutines }}</el-descriptions-item>
            <el-descriptions-item label="运行时间" :span="2">{{ sysInfo.uptime }}</el-descriptions-item>
          </el-descriptions>
        </el-card>
      </el-col>
      <el-col :xs="24" :lg="12">
        <el-card shadow="hover" class="animate-fade-in-up" style="animation-delay: 0.4s">
          <template #header>
            <div class="card-title-bar">
              <span class="title-dot" style="background: #F56C6C"></span>
              <span class="title-text">最近执行记录</span>
              <div style="flex: 1"></div>
              <el-button text type="primary" size="small" @click="$router.push('/logs')">
                查看全部 <el-icon><ArrowRight /></el-icon>
              </el-button>
            </div>
          </template>
          <el-table :data="recentLogs" size="small" :show-header="true" style="width: 100%" max-height="320">
            <el-table-column prop="task_name" label="任务名称" min-width="140" show-overflow-tooltip />
            <el-table-column label="状态" width="90" align="center">
              <template #default="{ row }">
                <el-tag :type="row.status === 0 ? 'success' : 'danger'" size="small" effect="light">
                  {{ row.status === 0 ? '成功' : '失败' }}
                </el-tag>
              </template>
            </el-table-column>
            <el-table-column prop="duration" label="耗时" width="90" align="center">
              <template #default="{ row }">
                {{ row.duration != null ? row.duration.toFixed(1) + 's' : '-' }}
              </template>
            </el-table-column>
            <el-table-column prop="created_at" label="执行时间" width="170">
              <template #default="{ row }">
                {{ formatTime(row.created_at) }}
              </template>
            </el-table-column>
          </el-table>
        </el-card>
      </el-col>
    </el-row>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted, nextTick, defineComponent, h, watch } from 'vue'
import { systemApi } from '@/api/system'
import {
  Timer, Check, ArrowRight, VideoPlay,
} from '@element-plus/icons-vue'
import * as echarts from 'echarts'

const CountUp = defineComponent({
  props: {
    endVal: { type: Number, default: 0 },
    duration: { type: Number, default: 1.5 },
    decimals: { type: Number, default: 0 },
    suffix: { type: String, default: '' },
  },
  setup(props) {
    const display = ref('0')
    let animFrame = 0

    function animate() {
      const start = 0
      const end = props.endVal
      const dur = props.duration * 1000
      const startTime = performance.now()

      function step(now: number) {
        const elapsed = now - startTime
        const progress = Math.min(elapsed / dur, 1)
        const eased = 1 - Math.pow(1 - progress, 3)
        const current = start + (end - start) * eased
        display.value = current.toFixed(props.decimals)
        if (progress < 1) {
          animFrame = requestAnimationFrame(step)
        }
      }
      cancelAnimationFrame(animFrame)
      animFrame = requestAnimationFrame(step)
    }

    watch(() => props.endVal, () => animate(), { immediate: true })

    return () => h('span', {}, display.value + props.suffix)
  }
})

const dashboardData = ref<any>({})
const sysInfo = ref<any>({})
const trendChartRef = ref<HTMLElement>()
let trendChart: echarts.ECharts | null = null

const recentLogs = computed(() => dashboardData.value.recent_logs || [])

const statCards = computed(() => {
  const d = dashboardData.value
  return [
    { label: '任务总数', value: d.task_count || 0, icon: 'Timer', color: '#409EFF', bg: 'linear-gradient(135deg, #e6f4ff, #f0f5ff)', link: '/tasks' },
    { label: '正在运行', value: d.running_tasks || 0, icon: 'VideoPlay', color: '#E6A23C', bg: 'linear-gradient(135deg, #fffbe6, #fff1b8)', link: '/tasks' },
    { label: '今日执行', value: d.today_logs || 0, icon: 'Check', color: '#fa541c', bg: 'linear-gradient(135deg, #fff2e8, #fff7e6)', link: '/logs' },
    { label: '成功率', value: d.today_logs ? Math.round((d.success_logs || 0) / d.today_logs * 100) : 0, icon: 'Check', color: '#67C23A', bg: 'linear-gradient(135deg, #f6ffed, #fcffe6)', link: '/logs' },
  ]
})

const resourceBars = computed(() => {
  const s = sysInfo.value
  return [
    { label: 'CPU', value: Number(s.cpu_usage) || 0, color: '#fa541c', detail: `${s.num_cpu || '-'} 核心` },
    { label: '内存', value: Number(s.memory_usage) || 0, color: '#409EFF', detail: `${formatBytes(s.memory_used)} / ${formatBytes(s.memory_total)}` },
    { label: '磁盘', value: Number(s.disk_usage) || 0, color: '#67C23A', detail: `${formatBytes(s.disk_used)} / ${formatBytes(s.disk_total)}` },
  ]
})

const formatTime = (t: string) => {
  if (!t) return '-'
  return new Date(t).toLocaleString('zh-CN')
}

const formatBytes = (bytes: number) => {
  if (!bytes) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let i = 0
  let val = bytes
  while (val >= 1024 && i < units.length - 1) { val /= 1024; i++ }
  return val.toFixed(1) + ' ' + units[i]
}

const renderTrendChart = () => {
  if (!trendChartRef.value) return
  const stats = dashboardData.value.daily_stats || []
  if (!trendChart) trendChart = echarts.init(trendChartRef.value)
  trendChart.setOption({
    tooltip: {
      trigger: 'axis',
      backgroundColor: '#fff',
      borderColor: '#f0f0f0',
      borderWidth: 1,
      textStyle: { color: '#333', fontSize: 12 },
      extraCssText: 'border-radius: 8px; box-shadow: 0 2px 8px rgba(0,0,0,0.08);',
    },
    legend: { data: ['执行总数', '成功', '失败'], icon: 'circle', itemWidth: 8, textStyle: { fontSize: 12, color: '#8c8c8c' }, top: 0 },
    grid: { left: '3%', right: '4%', bottom: '3%', top: 40, containLabel: true },
    xAxis: { type: 'category', data: stats.map((s: any) => s.date), axisLine: { lineStyle: { color: '#f0f0f0' } }, axisTick: { show: false }, axisLabel: { color: '#8c8c8c', fontSize: 11 } },
    yAxis: { type: 'value', minInterval: 1, axisLine: { lineStyle: { color: '#f0f0f0' } }, splitLine: { lineStyle: { color: '#f5f5f5' } }, axisLabel: { color: '#8c8c8c', fontSize: 11 } },
    series: [
      { name: '执行总数', type: 'line', data: stats.map((s: any) => (s.success || 0) + (s.failed || 0)), smooth: true, symbol: 'circle', symbolSize: 6, lineStyle: { width: 2, color: '#409EFF' }, itemStyle: { color: '#409EFF' }, areaStyle: { color: new echarts.graphic.LinearGradient(0, 0, 0, 1, [{ offset: 0, color: 'rgba(64,158,255,0.15)' }, { offset: 1, color: 'rgba(64,158,255,0)' }]) } },
      { name: '成功', type: 'line', data: stats.map((s: any) => s.success || 0), smooth: true, symbol: 'circle', symbolSize: 6, lineStyle: { width: 2, color: '#67C23A' }, itemStyle: { color: '#67C23A' } },
      { name: '失败', type: 'line', data: stats.map((s: any) => s.failed || 0), smooth: true, symbol: 'circle', symbolSize: 6, lineStyle: { width: 2, color: '#F56C6C' }, itemStyle: { color: '#F56C6C' } },
    ],
  })
}

const loadDashboard = async () => {
  try {
    const res = await systemApi.dashboard() as any
    dashboardData.value = res.data || {}
    await nextTick()
    renderTrendChart()
  } catch {}
}

const loadSysInfo = async () => {
  try {
    const res = await systemApi.info() as any
    sysInfo.value = res.data || {}
  } catch {}
}

let resizeHandler: () => void

onMounted(() => {
  loadDashboard()
  loadSysInfo()
  resizeHandler = () => { trendChart?.resize() }
  window.addEventListener('resize', resizeHandler)
})

onUnmounted(() => {
  window.removeEventListener('resize', resizeHandler)
  trendChart?.dispose()
})
</script>

<style scoped lang="scss">
.dashboard-page {
  padding: 0;
}

.page-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 20px;
}

.page-title {
  font-size: 20px;
  font-weight: 700;
  margin: 0;
  color: var(--el-text-color-primary);
}

.page-subtitle {
  font-size: 13px;
  color: var(--el-text-color-secondary);
}

.stat-card {
  border-radius: 10px;
  padding: 16px 20px;
  cursor: pointer;
  border: none;
  display: flex;
  justify-content: space-between;
  align-items: flex-start;
  transition: all 0.25s cubic-bezier(0.25, 0.46, 0.45, 0.94);
  will-change: transform, box-shadow;

  &:hover {
    transform: translate3d(0, -3px, 0);
    box-shadow: 0 12px 24px rgba(0, 0, 0, 0.1);
  }
}

.stat-label {
  font-size: 13px;
  color: #8c8c8c;
  margin-bottom: 8px;
}

.stat-value {
  font-size: 28px;
  font-weight: 700;
  line-height: 1.2;
}

.stat-icon-area {
  display: flex;
  align-items: center;
  gap: 4px;
  opacity: 0.5;
  margin-top: 4px;
}

.card-title-bar {
  display: flex;
  align-items: center;
  gap: 8px;
}

.title-dot {
  width: 3px;
  height: 14px;
  border-radius: 2px;
  display: inline-block;
  flex-shrink: 0;
}

.title-text {
  font-weight: 600;
  font-size: 14px;
}

.title-sub {
  font-size: 12px;
  color: var(--el-text-color-secondary);
  font-weight: 400;
}

.resource-item {
  text-align: center;
}

.resource-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
}

.resource-label {
  font-size: 13px;
  color: var(--el-text-color-secondary);
  font-weight: 500;
}

.resource-value {
  font-size: 20px;
  font-weight: 700;
}

.resource-detail {
  font-size: 11px;
  color: var(--el-text-color-placeholder);
  margin-top: 4px;
}

.chart-card {
  :deep(.el-card__header) {
    padding: 14px 20px;
  }
}
</style>
