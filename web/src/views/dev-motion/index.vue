<script setup lang="ts">
/**
 * 动效组件预览页（内部用，不进侧栏菜单）。
 *
 * 访问方式：手动输入 /dev/motion。
 * 它不在 MainLayout 的 workspaceItems / adminItems 里，也不在 router 的
 * routePreloaders 里，所以既不会出现在导航中，也不会被空闲预加载拉下来——
 * 不访问就不下载这个 chunk。
 *
 * 存在的意义：七类动效组件的状态散落在 15 个业务页里，逐个点过去既慢又容易漏。
 * 这里把它们的【全部状态和全部动效】排在一页，改完动效可以一次性看完，
 * 也方便用浏览器做实测截图。
 *
 * 每一块都必须是【可交互】的：只摆静态样子没有意义，动效要能被真的触发出来。
 *
 * v4.0 起它同时是「主题预览页」（第 00 节）：一页列出常用 Element Plus 组件和项目自己的 .dd-* 类，
 * 顶部可以直接切 7 种风格 × 明暗，逐主题检查颜色、对比度和圆角。这里切风格写的是【本机】偏好，
 * 与主题工坊同一个开关；看完点「跟随全站」即可还原。
 */
import { ref, computed, onMounted, onActivated } from 'vue'
import { toast } from '@/utils/toast'
import { ElMessage, ElMessageBox } from 'element-plus'
import { readMotionPreference, THEME_PRESETS, type ThemePreset } from '@/utils/panelAppearance'
import { useThemeStore } from '@/stores/theme'
import { burst, motionLevel } from '@/utils/motion'
import DdCountUp from '@/components/ui/DdCountUp.vue'
import DdBadge from '@/components/ui/DdBadge.vue'
import DdSplitButton from '@/components/ui/DdSplitButton.vue'
import type { SplitButtonItem } from '@/components/ui/DdSplitButton.vue'
import DdMultiSelect from '@/components/ui/DdMultiSelect.vue'
import DdDateRangePicker from '@/components/ui/DdDateRangePicker.vue'

// ===== 0. 主题预览 =====
const themeStore = useThemeStore()

function pickPreset(id: ThemePreset, event: MouseEvent) {
  // 从点击点圆形扩散切过去（rich 档才有动画，其余直接切）
  themeStore.setPreset(id, event)
}

function followSitePreset(event: MouseEvent) {
  themeStore.setPreset(null, event)
}

// 组件示例用的假数据：只为把各种状态摆出来
const previewInput = ref('呆呆面板')
const previewTextarea = ref('多行输入框\n第二行')
const previewNumber = ref(3)
const previewSelect = ref('running')
const previewRadio = ref('7d')
const previewChecks = ref<string[]>(['mail'])
const previewDate = ref<Date | null>(new Date())
const previewDateRange = ref<[Date, Date] | null>(null)
const previewSwitch = ref(true)
const previewSlider = ref(40)
const previewPage = ref(2)
const previewTab = ref('overview')
const previewCardTab = ref('a')
const previewSegment = ref('近 7 天')
const previewSegButton = ref('all')
const previewCount = ref(1234)
const previewTable = [
  { name: '系统健康检查', cron: '*/5 * * * *', status: 'success', duration: '1.2s' },
  { name: '更新 IP 数据库', cron: '0 3 * * *', status: 'failed', duration: '30.0s' },
  { name: '同步配置文件', cron: '0 */2 * * *', status: 'running', duration: '—' },
  { name: '发送运营日报', cron: '30 9 * * 1-5', status: 'disabled', duration: '—' },
]
const statusTagType: Record<string, 'success' | 'danger' | 'primary' | 'info'> = {
  success: 'success',
  failed: 'danger',
  running: 'primary',
  disabled: 'info',
}
const statusText: Record<string, string> = {
  success: '成功',
  failed: '失败',
  running: '运行中',
  disabled: '已停用',
}
// 墨色令牌：主色 / 状态色当文字时必须用它们，这里并排摆出来看对比度
const inkSwatches = [
  { name: '--dd-accent-ink', label: '主色墨色' },
  { name: '--dd-accent-2-ink', label: '副色墨色' },
  { name: '--dd-ok-ink', label: '成功墨色' },
  { name: '--dd-warn-ink', label: '警告墨色' },
  { name: '--dd-err-ink', label: '危险墨色' },
  { name: '--dd-info-ink', label: '信息墨色' },
]
// .dd-rise 只在元素首次创建时播一次：换 key 让列表重建，就能反复看交错入场
const riseKey = ref(0)

function fireBurst(event: MouseEvent) {
  if (motionLevel() !== 'rich') {
    ElMessage.info('烟花只在「丰富」档出现')
    return
  }
  burst(event.clientX, event.clientY)
}

// ===== 1. 角标 =====
const badgeCount = ref(3)
const badgeBig = ref(120)

// ===== 4. Select =====
const selectValue = ref('')
const selectOptions = [
  { value: 'pending', label: '待处理' },
  { value: 'running', label: '进行中' },
  { value: 'done', label: '已完成' },
]

// ===== 5. Multi-select =====
const multiValue = ref<string[]>(['design'])
const multiOptions = [
  { value: 'design', label: '设计' },
  { value: 'writing', label: '写作' },
  { value: 'data', label: '数据' },
  { value: 'ops', label: '运维' },
  { value: 'qa', label: '测试' },
  { value: 'pm', label: '产品' },
]
const multiLimited = ref<string[]>([])

// ===== 6. Split Button =====
const taskRunning = ref(false)
/**
 * 动态主操作的范例：运行中时主体变「停止」，且菜单里不能再出现「停止」。
 * 这正是 SplitButtonItem 的 visible 字段要解决的问题——把互斥关系写在同一份数组里，
 * 而不是在调用点用 v-if 拆成两套按钮。
 */
const taskItems = computed<SplitButtonItem[]>(() => [
  { key: 'run', label: '立即运行', visible: taskRunning.value },
  { key: 'stop', label: '停止运行', visible: !taskRunning.value },
  { key: 'log', label: '查看日志' },
  { key: 'edit', label: '编辑任务' },
  { key: 'delete', label: '删除', danger: true, divided: true },
])

const staticItems: SplitButtonItem[] = [
  { key: 'schedule', label: '定时发布' },
  { key: 'draft', label: '保存草稿' },
  { key: 'discard', label: '丢弃更改', danger: true, divided: true },
]

// ===== 7. Date Picker =====
const dateRange = ref<[Date, Date] | null>(null)
const dateRangeInline = ref<[Date, Date] | null>(null)

function onSplitCommand(key: string) {
  if (key === 'run') taskRunning.value = true
  else if (key === 'stop') taskRunning.value = false
  ElMessage.info(`菜单项：${key}`)
}

// ===== 8. 弹窗 / 抽屉 / 确认框 =====
const dialogVisible = ref(false)
const fullscreenDialogVisible = ref(false)
const drawerVisible = ref(false)

function openConfirmDemo() {
  ElMessageBox.confirm(
    '确认框的进出场复用弹窗那组关键帧：从下方升起、轻微放大到位，离场向下淡出。',
    '确认框',
    { type: 'warning', confirmButtonText: '知道了', cancelButtonText: '取消' },
  ).catch(() => {})
}

/**
 * 当前生效的动效档位。验收动效前先看这一行：选的是「跟随系统」而系统又开着「减少动态效果」时，
 * 本页所有动效都会被压到 1ms，看上去就是「没有动画」，别误判成改动没生效。
 * 本页同样被 MainLayout 的 keep-alive 缓存，去个人设置切完档位再回来只触发 onActivated，所以两处都刷新。
 */
const motionStatus = ref('')

function refreshMotionStatus() {
  // v4.0 起四档（旧值 always → rich、reduce → off）。本页第二波由 G1 扩成主题预览页，这里只做最小适配
  const preference = readMotionPreference()
  if (preference === 'rich') {
    motionStatus.value = '丰富：无论系统怎么设都完整播放'
    return
  }
  if (preference === 'subtle') {
    motionStatus.value = '克制：无论系统怎么设都播放，但只保留必要过渡'
    return
  }
  if (preference === 'off') {
    motionStatus.value = '关闭：本页所有动效都被压到 1ms'
    return
  }
  const systemReduced = window.matchMedia?.('(prefers-reduced-motion: reduce)').matches ?? false
  motionStatus.value = systemReduced
    ? '跟随系统：系统开着「减少动态效果」，本页所有动效都被压到 1ms'
    : '跟随系统：系统没开减少动态效果，按「克制」播放（v4.0.2 起丰富档只能手动选）'
}

onMounted(refreshMotionStatus)
onActivated(refreshMotionStatus)
</script>

<template>
  <div class="motion-page dd-scroll-page">
    <header class="motion-header">
      <h1>主题与动效预览</h1>
      <p>
        内部验收页，不进侧栏菜单。第 00 节是主题预览：切换风格与明暗，逐主题检查常用组件与 <code>.dd-*</code> 类；
        后面七节是动效组件的全部状态与交互，每一块都可以直接点出来看。
        颜色一律读 <code>--dd-*</code> 令牌，动效时长吃 <code>--dd-motion-*</code> 令牌，并跟随动效档位降级。
      </p>
    </header>

    <!-- ============ 0. 主题预览 ============ -->
    <section class="motion-section dd-glass">
      <h2><span class="motion-section__no">00</span> 主题预览</h2>
      <p class="motion-section__desc">
        当前：{{ themeStore.presetMeta.name }} · {{ themeStore.isDark ? '暗色' : '亮色' }}
        （{{ themeStore.isLocal('preset') ? '本机选择' : '跟随全站' }}）。
        这里切风格写的是本机偏好，与主题工坊同一个开关；看完点「跟随全站」还原。
      </p>

      <div class="motion-demo">
        <div class="motion-row">
          <span class="motion-label">风格</span>
          <el-button
            v-for="preset in THEME_PRESETS"
            :key="preset.id"
            size="small"
            :type="themeStore.preset === preset.id ? 'primary' : 'default'"
            @click="pickPreset(preset.id, $event)"
          >
            {{ preset.name }}
          </el-button>
          <el-button size="small" text :disabled="!themeStore.isLocal('preset')" @click="followSitePreset">
            跟随全站
          </el-button>
        </div>
        <div class="motion-row">
          <span class="motion-label">明暗</span>
          <el-button size="small" @click="themeStore.toggleTheme()">
            切到{{ themeStore.isDark ? '亮色' : '暗色' }}
          </el-button>
          <span class="motion-hint">与顶栏明暗按钮同一个开关</span>
        </div>
      </div>

      <div class="theme-grid">
        <!-- 按钮 -->
        <div class="theme-cell theme-cell--wide">
          <h3>按钮</h3>
          <div class="motion-row">
            <el-button type="primary">主要</el-button>
            <el-button type="success">成功</el-button>
            <el-button type="warning">警告</el-button>
            <el-button type="danger">危险</el-button>
            <el-button type="info">信息</el-button>
            <el-button>默认</el-button>
          </div>
          <div class="motion-row">
            <el-button type="primary" plain>主要</el-button>
            <el-button type="success" plain>成功</el-button>
            <el-button type="warning" plain>警告</el-button>
            <el-button type="danger" plain>危险</el-button>
            <el-button type="primary" text>文字按钮</el-button>
            <el-button type="primary" link>链接按钮</el-button>
          </div>
          <div class="motion-row">
            <el-button type="primary" disabled>禁用</el-button>
            <el-button disabled>默认禁用</el-button>
            <el-button type="primary" loading>加载中</el-button>
            <el-button type="primary" round>圆角</el-button>
            <el-button size="small" type="primary">小号</el-button>
            <el-button size="large" type="primary">大号</el-button>
          </div>
        </div>

        <!-- 标签 -->
        <div class="theme-cell">
          <h3>标签</h3>
          <div class="motion-row">
            <el-tag>主要</el-tag>
            <el-tag type="success">成功</el-tag>
            <el-tag type="warning">警告</el-tag>
            <el-tag type="danger">危险</el-tag>
            <el-tag type="info">信息</el-tag>
          </div>
          <div class="motion-row">
            <el-tag effect="dark">主要</el-tag>
            <el-tag type="success" effect="dark">成功</el-tag>
            <el-tag type="warning" effect="dark">警告</el-tag>
            <el-tag type="danger" effect="dark">危险</el-tag>
            <el-tag type="info" effect="dark">信息</el-tag>
          </div>
          <div class="motion-row">
            <el-tag effect="plain">主要</el-tag>
            <el-tag type="success" effect="plain">成功</el-tag>
            <el-tag type="danger" effect="plain" closable>可关闭</el-tag>
            <el-tag type="info" effect="plain" round>胶囊</el-tag>
          </div>
        </div>

        <!-- 状态 chip 与墨色 -->
        <div class="theme-cell">
          <h3>状态 chip（.dd-status-chip）与墨色令牌</h3>
          <div class="motion-row">
            <span class="dd-status-chip is-success">成功</span>
            <span class="dd-status-chip is-failed">失败</span>
            <span class="dd-status-chip is-running">运行中</span>
            <span class="dd-status-chip is-warning">警告</span>
            <span class="dd-status-chip is-info">已停用</span>
            <span class="dd-status-chip is-purple">副色</span>
          </div>
          <div class="motion-row">
            <span
              v-for="swatch in inkSwatches"
              :key="swatch.name"
              class="theme-ink"
              :style="{ color: `var(${swatch.name})` }"
              :title="swatch.name"
            >
              {{ swatch.label }}
            </span>
          </div>
        </div>

        <!-- 输入 -->
        <div class="theme-cell">
          <h3>输入</h3>
          <div class="theme-stack">
            <el-input v-model="previewInput" placeholder="普通输入框" clearable />
            <el-input placeholder="带前缀图标" prefix-icon="Search" />
            <el-input placeholder="禁用" disabled />
            <el-input v-model="previewTextarea" type="textarea" :rows="2" />
            <el-input-number v-model="previewNumber" :min="0" :max="10" />
          </div>
        </div>

        <!-- 选择 -->
        <div class="theme-cell">
          <h3>选择</h3>
          <div class="theme-stack">
            <el-select v-model="previewSelect" placeholder="请选择">
              <el-option v-for="o in selectOptions" :key="o.value" :label="o.label" :value="o.value" />
            </el-select>
            <el-radio-group v-model="previewRadio">
              <el-radio value="7d">近 7 天</el-radio>
              <el-radio value="30d">近 30 天</el-radio>
              <el-radio value="all" disabled>全部</el-radio>
            </el-radio-group>
            <el-radio-group v-model="previewRadio" size="small">
              <el-radio-button value="7d">近 7 天</el-radio-button>
              <el-radio-button value="30d">近 30 天</el-radio-button>
            </el-radio-group>
            <el-checkbox-group v-model="previewChecks">
              <el-checkbox value="mail">邮件</el-checkbox>
              <el-checkbox value="bark">Bark</el-checkbox>
              <el-checkbox value="tg" disabled>Telegram</el-checkbox>
            </el-checkbox-group>
          </div>
        </div>

        <!-- 日期 / 开关 / 滑块 -->
        <div class="theme-cell">
          <h3>日期 · 开关 · 滑块</h3>
          <div class="theme-stack">
            <el-date-picker v-model="previewDate" type="date" placeholder="选择日期" />
            <el-date-picker
              v-model="previewDateRange"
              type="datetimerange"
              start-placeholder="开始时间"
              end-placeholder="结束时间"
            />
            <div class="motion-row">
              <el-switch v-model="previewSwitch" />
              <el-switch v-model="previewSwitch" active-text="启用" inactive-text="停用" />
              <el-switch :model-value="false" disabled />
            </div>
            <el-slider v-model="previewSlider" />
          </div>
        </div>

        <!-- 表格 + 分页 -->
        <div class="theme-cell theme-cell--wide">
          <h3>表格 · 分页</h3>
          <el-table :data="previewTable" stripe>
            <el-table-column prop="name" label="任务名" min-width="140" />
            <el-table-column prop="cron" label="定时规则" min-width="130">
              <template #default="{ row }">
                <span class="dd-mono">{{ row.cron }}</span>
              </template>
            </el-table-column>
            <el-table-column label="状态" width="100">
              <template #default="{ row }">
                <el-tag :type="statusTagType[row.status]" size="small">{{ statusText[row.status] }}</el-tag>
              </template>
            </el-table-column>
            <el-table-column prop="duration" label="耗时" width="90" />
            <el-table-column label="操作" width="150" fixed="right">
              <template #default>
                <el-button type="primary" link size="small">运行</el-button>
                <el-button type="primary" link size="small">日志</el-button>
                <el-button type="danger" link size="small">删除</el-button>
              </template>
            </el-table-column>
          </el-table>
          <div class="motion-row theme-pagination">
            <el-pagination
              v-model:current-page="previewPage"
              background
              layout="total, prev, pager, next"
              :total="128"
              :page-size="20"
            />
          </div>
        </div>

        <!-- 标签页 / 分段 -->
        <div class="theme-cell">
          <h3>标签页 · 分段</h3>
          <el-tabs v-model="previewTab">
            <el-tab-pane label="概览" name="overview" />
            <el-tab-pane label="日志" name="logs" />
            <el-tab-pane label="设置" name="settings" />
          </el-tabs>
          <el-tabs v-model="previewCardTab" type="card">
            <el-tab-pane label="卡片 A" name="a" />
            <el-tab-pane label="卡片 B" name="b" />
          </el-tabs>
          <div class="theme-stack">
            <el-segmented v-model="previewSegment" :options="['近 7 天', '近 30 天', '近 90 天']" />
            <div class="dd-seg-group">
              <button
                v-for="seg in ['all', 'running', 'failed']"
                :key="seg"
                type="button"
                class="dd-seg-btn"
                :class="{ 'is-active': previewSegButton === seg }"
                @click="previewSegButton = seg"
              >
                {{ seg === 'all' ? '全部' : seg === 'running' ? '运行中' : '失败' }}
              </button>
            </div>
          </div>
        </div>

        <!-- 弹层与消息 -->
        <div class="theme-cell">
          <h3>对话框 · 消息 · 提示</h3>
          <div class="motion-row">
            <el-button size="small" @click="dialogVisible = true">打开对话框</el-button>
            <el-button size="small" @click="drawerVisible = true">打开抽屉</el-button>
            <el-button size="small" @click="openConfirmDemo">确认框</el-button>
          </div>
          <div class="motion-row">
            <el-button size="small" @click="ElMessage.success('保存成功')">成功消息</el-button>
            <el-button size="small" @click="ElMessage.warning('磁盘占用偏高')">警告消息</el-button>
            <el-button size="small" @click="ElMessage.error('执行失败')">错误消息</el-button>
            <el-button size="small" @click="ElMessage.info('正在同步')">信息消息</el-button>
          </div>
          <div class="theme-stack">
            <el-alert title="成功提示" type="success" show-icon :closable="false" />
            <el-alert title="警告提示" type="warning" show-icon :closable="false" />
            <el-alert title="错误提示" type="error" show-icon :closable="false" />
            <el-alert title="信息提示" type="info" show-icon :closable="false" />
          </div>
        </div>

        <!-- 进度 -->
        <div class="theme-cell">
          <h3>进度</h3>
          <div class="theme-stack">
            <el-progress :percentage="62" />
            <el-progress :percentage="100" status="success" />
            <el-progress :percentage="78" status="warning" />
            <el-progress :percentage="45" status="exception" />
            <div class="motion-row">
              <el-progress type="circle" :percentage="47" :width="88" />
              <el-progress type="dashboard" :percentage="78" :width="88" status="warning" />
            </div>
          </div>
        </div>

        <!-- 项目自己的 .dd-* 类 -->
        <div class="theme-cell theme-cell--wide">
          <h3>项目类：.dd-page-header · .dd-stat-card · .dd-spot · .dd-running-flow · .dd-glass · .dd-rise</h3>
          <div class="dd-page-header">
            <div class="dd-page-header__main">
              <h2 class="dd-page-header__title">页面大标题</h2>
              <p class="dd-page-header__subtitle">副标题读 .dd-page-header__subtitle；素白主题是 34px / 800</p>
            </div>
            <el-button v-magnetic type="primary">磁吸主按钮</el-button>
          </div>

          <div class="theme-cards">
            <div class="dd-stat-card dd-spot">
              <div class="dd-stat-card__main">
                <span class="dd-stat-card__label">.dd-stat-card + .dd-spot</span>
                <span class="dd-stat-card__value"><DdCountUp :value="previewCount" /></span>
              </div>
              <el-button size="small" @click="previewCount = Math.round(Math.random() * 5000)">换个数</el-button>
            </div>
            <div class="theme-card dd-running-flow">
              <strong>.dd-running-flow</strong>
              <span>运行中流光：rich 档转动，其余静止描边</span>
            </div>
            <div class="theme-card dd-glass">
              <strong>.dd-glass</strong>
              <span>半透明表面 + 背景模糊；玻璃关闭时退回实色</span>
            </div>
            <div class="theme-card">
              <strong>结果烟花 burst()</strong>
              <el-button size="small" type="primary" @click="fireBurst">放一个</el-button>
            </div>
          </div>

          <div class="motion-row">
            <span class="motion-label">交错入场</span>
            <el-button size="small" @click="riseKey += 1">重播 .dd-rise</el-button>
          </div>
          <div :key="riseKey" class="theme-rise-list">
            <span v-for="n in 6" :key="n" class="theme-rise-item dd-rise" :style="{ '--dd-i': n - 1 }">{{ n }}</span>
          </div>
        </div>
      </div>
    </section>

    <!-- ============ 1. 角标 ============ -->
    <section class="motion-section dd-glass">
      <h2><span class="motion-section__no">01</span> 角标提示 Badge</h2>
      <p class="motion-section__desc">
        数字变化时做翻牌：新值从上方落下、旧值向下退出。等宽数字，跳字时宽度不抖。
      </p>

      <div class="motion-demo">
        <div class="motion-row">
          <span class="motion-label">语义级别</span>
          <span class="motion-chip">危险 <DdBadge :value="3" level="danger" /></span>
          <span class="motion-chip">警告 <DdBadge :value="7" level="warning" /></span>
          <span class="motion-chip">进行中 <DdBadge :value="2" level="primary" /></span>
          <span class="motion-chip">中性计数 <DdBadge :value="18" level="info" /></span>
        </div>

        <div class="motion-row">
          <span class="motion-label">小圆点</span>
          <span class="motion-chip">有新动态 <DdBadge dot level="danger" /></span>
          <span class="motion-chip">进行中 <DdBadge dot level="primary" /></span>
        </div>

        <div class="motion-row">
          <span class="motion-label">数字翻牌</span>
          <el-button size="small" @click="badgeCount = Math.max(0, badgeCount - 1)">−1</el-button>
          <DdBadge :value="badgeCount" level="danger" title="待处理" />
          <el-button size="small" @click="badgeCount += 1">+1</el-button>
          <span class="motion-hint">减到 0 时整个角标会淡出消失</span>
        </div>

        <div class="motion-row">
          <span class="motion-label">超出上限</span>
          <DdBadge :value="badgeBig" :max="99" level="danger" />
          <el-button size="small" @click="badgeBig = badgeBig > 99 ? 8 : 120">
            在 8 / 120 之间切换
          </el-button>
          <span class="motion-hint">超过 max 显示 99+，避免把菜单项撑开</span>
        </div>

        <div class="motion-row">
          <span class="motion-label">值为 0</span>
          <span class="motion-chip">默认（不渲染）<DdBadge :value="0" /></span>
          <span class="motion-chip">show-zero <DdBadge :value="0" level="info" show-zero /></span>
          <span class="motion-hint">提醒型角标该消失；统计型「失败 0」是有信息量的</span>
        </div>
      </div>
    </section>

    <!-- ============ 2. 轻提示 ============ -->
    <section class="motion-section dd-glass">
      <h2><span class="motion-section__no">02</span> 轻提示 Toast</h2>
      <p class="motion-section__desc">
        左缘 3px 语义色实心条替代「浅底 + 同色图标」的弱区分；
        停留时长按「这条信息要不要读完」分档，而不是一律 3 秒。
      </p>

      <div class="motion-demo">
        <div class="motion-row">
          <span class="motion-label">四种语义</span>
          <el-button size="small" type="success" @click="toast.success('任务已保存')">
            成功 2.5s
          </el-button>
          <el-button size="small" @click="toast.info('正在后台同步')">信息 3s</el-button>
          <el-button size="small" type="warning" @click="toast.warning('日志目录占用已超过 90% 磁盘空间，建议清理旧日志')">
            警告 4.5s
          </el-button>
          <el-button size="small" type="danger" @click="toast.error('执行失败：command not found: python3')">
            错误 6s
          </el-button>
        </div>

        <div class="motion-row">
          <span class="motion-label">长文本换行</span>
          <el-button
            size="small"
            @click="toast.error('执行失败：拉取远端仓库超时，请检查网络代理设置以及该仓库是否需要鉴权凭据')"
          >
            触发一条长报错
          </el-button>
          <span class="motion-hint">EP 默认 line-height:1 会让两行糊在一起，这里改成 1.5</span>
        </div>

        <div class="motion-row">
          <span class="motion-label">相同内容合并</span>
          <el-button size="small" @click="toast.error('批量操作中有一项失败', { grouping: true })">
            连点几次看右侧计数
          </el-button>
          <span class="motion-hint">批量操作循环报错时用，否则会刷出一屏一样的提示</span>
        </div>

        <div class="motion-row">
          <span class="motion-label">不自动关闭</span>
          <el-button size="small" @click="toast.warning('这条需要你手动关掉', { duration: 0 })">
            duration: 0
          </el-button>
          <span class="motion-hint">传 0 时会自动带上关闭按钮，否则会永久占住页面顶部</span>
        </div>
      </div>
    </section>

    <!-- ============ 3. 带操作的轻提示 ============ -->
    <section class="motion-section dd-glass">
      <h2><span class="motion-section__no">03</span> 带操作的轻提示</h2>
      <p class="motion-section__desc">
        把「打断式弹窗」降级成「非阻塞提示 + 一个后续入口」。
        按钮颜色继承提示条的语义色，成功/失败各自成套；hover 反色，不做位移。
      </p>

      <div class="motion-demo">
        <div class="motion-row">
          <span class="motion-label">查看详情</span>
          <el-button
            size="small"
            @click="
              toast.error('测试发送失败', {
                action: {
                  text: '查看详情',
                  handler: () =>
                    ElMessageBox.alert('HTTP 401：Bot Token 无效或已被吊销', '测试发送失败', {
                      confirmButtonText: '知道了',
                      type: 'error',
                    }).catch(() => {}),
                },
              })
            "
          >
            通知渠道测试失败
          </el-button>
          <span class="motion-hint">替代原来一失败就弹 alert 挡住整个界面</span>
        </div>

        <div class="motion-row">
          <span class="motion-label">立即刷新</span>
          <el-button
            size="small"
            @click="
              toast.warning('重启超时，面板可能已经起来了', {
                action: { text: '立即刷新', handler: () => ElMessage.info('这里会 location.reload()') },
              })
            "
          >
            重启超时
          </el-button>
          <span class="motion-hint">替代「请手动刷新页面」——说了动作却让用户自己按 F5</span>
        </div>

        <div class="motion-row">
          <span class="motion-label">成功后的后续入口</span>
          <el-button
            size="small"
            @click="
              toast.withAction('已导出 12 个任务', {
                text: '查看',
                handler: () => ElMessage.success('跳转到任务列表'),
              })
            "
          >
            导出完成
          </el-button>
        </div>

        <div class="motion-row">
          <span class="motion-label">重试</span>
          <el-button
            size="small"
            @click="
              toast.error('加载依赖列表失败', {
                action: { text: '重试', handler: () => ElMessage.success('重新请求中…') },
              })
            "
          >
            加载失败
          </el-button>
        </div>
      </div>
    </section>

    <!-- ============ 4. Select ============ -->
    <section class="motion-section dd-glass">
      <h2><span class="motion-section__no">04</span> Select 单选</h2>
      <p class="motion-section__desc">
        面板展开从 <code>scaleY</code> 卷帘改成淡入 + 8px 位移，朝上弹出时位移自动反向。
        位移走 CSS 的 <code>translate</code> 独立属性——EP 用内联 <code>transform</code> 给浮层定位，
        class 里写 <code>transform</code> 会被它压掉。面板底色是半透明毛玻璃，
        系统开了「降低透明度」或浏览器不支持时自动退回实色。
        箭头翻转与选项 hover 都吃令牌，不再是 EP 默认的 all 0.3s。
      </p>

      <div class="motion-demo">
        <div class="motion-row">
          <span class="motion-label">基础</span>
          <el-select v-model="selectValue" placeholder="请选择状态" style="width: 220px">
            <el-option v-for="o in selectOptions" :key="o.value" :label="o.label" :value="o.value" />
          </el-select>
          <span class="motion-hint">当前值：{{ selectValue || '未设置' }}</span>
        </div>

        <div class="motion-row">
          <span class="motion-label">可筛选 + 可清空</span>
          <el-select
            v-model="selectValue"
            placeholder="输入关键字筛选"
            filterable
            clearable
            style="width: 220px"
          >
            <el-option v-for="o in selectOptions" :key="o.value" :label="o.label" :value="o.value" />
          </el-select>
        </div>

        <div class="motion-row">
          <span class="motion-label">禁用</span>
          <el-select model-value="pending" disabled style="width: 220px">
            <el-option label="待处理" value="pending" />
          </el-select>
        </div>
      </div>
    </section>

    <!-- ============ 5. Multi-select ============ -->
    <section class="motion-section dd-glass">
      <h2><span class="motion-section__no">05</span> Multi-select 多选</h2>
      <p class="motion-section__desc">
        EP 自带标签化、单独删除和一键清空，缺的是「已选几项 / 还能选几项」——
        尤其在标签折叠成「+N」之后，屏幕上只剩一个数字时更无从判断。
      </p>

      <div class="motion-demo">
        <div class="motion-row motion-row--top">
          <span class="motion-label">标签化多选</span>
          <div style="width: 320px">
            <DdMultiSelect v-model="multiValue" :options="multiOptions" placeholder="选择技能" filterable />
          </div>
        </div>

        <div class="motion-row motion-row--top">
          <span class="motion-label">带上限</span>
          <div style="width: 320px">
            <DdMultiSelect v-model="multiLimited" :options="multiOptions" :max="3" placeholder="最多选 3 项" />
          </div>
          <span class="motion-hint">
            到上限后未选中的项【置灰】而不是静默忽略点击；已选中的仍可点，否则会被锁死
          </span>
        </div>

        <div class="motion-row motion-row--top">
          <span class="motion-label">折叠标签</span>
          <div style="width: 320px">
            <DdMultiSelect
              v-model="multiValue"
              :options="multiOptions"
              collapse-tags
              :max-collapse-tags="2"
              placeholder="选多了会折叠"
            />
          </div>
        </div>
      </div>
    </section>

    <!-- ============ 6. Split Button ============ -->
    <section class="motion-section dd-glass">
      <h2><span class="motion-section__no">06</span> Split Button</h2>
      <p class="motion-section__desc">
        主体点了【直接执行】，右侧箭头才展开更多——主体不能执行操作的话，那只是个带标签的下拉。
        危险操作一律进菜单并标红加分隔线，绝不能放在「点了就执行」的主体上。
        菜单普通项的字色加深到主文字色（EP 默认偏浅，不像可点的操作），危险项保持红字。
      </p>

      <div class="motion-demo">
        <div class="motion-row">
          <span class="motion-label">动态主操作</span>
          <DdSplitButton
            :label="taskRunning ? '停止' : '运行'"
            :type="taskRunning ? 'danger' : 'primary'"
            size="small"
            :items="taskItems"
            @click="taskRunning = !taskRunning"
            @command="onSplitCommand"
          />
          <span class="motion-hint">
            点主体切换运行状态——注意菜单里的「立即运行 / 停止运行」跟着互斥，不会出现矛盾项
          </span>
        </div>

        <div class="motion-row">
          <span class="motion-label">各语义色</span>
          <DdSplitButton label="发布" type="primary" :items="staticItems" @command="onSplitCommand" />
          <DdSplitButton label="保存" type="success" :items="staticItems" @command="onSplitCommand" />
          <DdSplitButton label="默认" type="default" :items="staticItems" @command="onSplitCommand" />
          <span class="motion-hint">中缝在实心底色上用 #ffffff80，浅底按钮上用边框灰</span>
        </div>

        <div class="motion-row">
          <span class="motion-label">禁用</span>
          <DdSplitButton label="发布" type="primary" disabled :items="staticItems" />
        </div>
      </div>
    </section>

    <!-- ============ 7. Date Picker ============ -->
    <section class="motion-section dd-glass">
      <h2><span class="motion-section__no">07</span> Date Picker 日期范围</h2>
      <p class="motion-section__desc">
        面板里的日期格原本是 <code>border-radius: 50%</code> 的圆点、范围起止是半个胶囊——
        在一片直角界面里非常突兀。这里全部方化，范围条也去掉了两端各 5px 的缩进，连成一条实心带。
      </p>

      <div class="motion-demo">
        <div class="motion-row motion-row--top">
          <span class="motion-label">竖排（表单里）</span>
          <DdDateRangePicker v-model="dateRange" />
        </div>

        <div class="motion-row motion-row--top">
          <span class="motion-label">横排（工具栏里）</span>
          <DdDateRangePicker v-model="dateRangeInline" inline />
          <span class="motion-hint">竖排会把整条工具栏撑成两行高，列表页要用 inline</span>
        </div>

        <div class="motion-row motion-row--top">
          <span class="motion-label">限制可回溯天数</span>
          <DdDateRangePicker v-model="dateRange" inline :max-past-days="7" :shortcuts="[]" />
          <span class="motion-hint">
            日志有保留期，让用户选到 90 天前再发现没数据，等于把清理策略的后果扔给他去猜
          </span>
        </div>
      </div>
    </section>

    <!-- ============ 8. 弹窗 / 抽屉 / 确认框 ============ -->
    <section class="motion-section dd-glass">
      <h2><span class="motion-section__no">08</span> 弹窗 / 抽屉 / 确认框</h2>
      <p class="motion-section__desc">
        弹窗从下方 16px 升起、同时从 0.98 放大到位；全屏弹窗只位移不缩放（缩放会让四条边露出遮罩）。
        遮罩比弹窗快一拍先铺好底，确认框复用同一组关键帧，抽屉滑入吃同一档时长。
        动画挂在 <code>.el-overlay-dialog</code> 上而不是 <code>.el-dialog</code> 上——
        同一个弹窗第二次打开时 DOM 被复用，挂在 <code>.el-dialog</code> 上的动画不会重放，验收务必连开两次。
      </p>

      <div class="motion-demo">
        <div class="motion-row">
          <span class="motion-label">当前动效档位</span>
          <span class="motion-hint">{{ motionStatus }}</span>
          <router-link to="/profile" class="motion-link">去个人设置切换</router-link>
        </div>

        <div class="motion-row">
          <span class="motion-label">弹窗</span>
          <el-button size="small" @click="dialogVisible = true">普通弹窗</el-button>
          <el-button size="small" @click="fullscreenDialogVisible = true">全屏弹窗</el-button>
          <span class="motion-hint">关掉后再点一次：第二次打开也必须有升起动效</span>
        </div>

        <div class="motion-row">
          <span class="motion-label">抽屉</span>
          <el-button size="small" @click="drawerVisible = true">右侧抽屉</el-button>
          <span class="motion-hint">滑入与遮罩同一时长，离场换更短的一档，尽快让位</span>
        </div>

        <div class="motion-row">
          <span class="motion-label">确认框</span>
          <el-button size="small" @click="openConfirmDemo">ElMessageBox.confirm</el-button>
          <span class="motion-hint">EP 默认是从上方落下，这里改成与弹窗同向：从下方升起</span>
        </div>
      </div>
    </section>

    <footer class="motion-footer">
      系统「减少动态效果」开启时，以上全部动效会自动降级为 1ms；
      在「个人设置 → 界面动效」或主题工坊里可以改成「丰富 / 克制 / 关闭」。
      时长与延迟都吃 <code>--dd-motion-*</code> 令牌，不写死毫秒数。
    </footer>

    <!-- 演示用的弹窗 / 抽屉刻意放在 .motion-section 外面：分节卡片的 rise-in 入场写了 both，
         收尾后残留 transform: translateY(0)，会让卡片成为 position:fixed 后代的包含块，
         弹窗放进卡片里，遮罩就只盖住那一张卡片。业务页的弹窗同样都挂在页面根下。 -->
    <el-dialog v-model="dialogVisible" title="普通弹窗" width="480px">
      <p class="motion-dialog-text">
        关掉后再打开一次，确认第二次也有「从下方升起 + 轻微放大」的进场。
        离场向下淡出，比进场短，尽快把焦点还给页面。
      </p>
      <template #footer>
        <el-button @click="dialogVisible = false">取消</el-button>
        <el-button type="primary" @click="dialogVisible = false">确定</el-button>
      </template>
    </el-dialog>

    <el-dialog v-model="fullscreenDialogVisible" title="全屏弹窗" fullscreen>
      <p class="motion-dialog-text">
        全屏弹窗贴满视口四边，进场只有位移、没有缩放，否则四条边会同时露出底下的遮罩。
      </p>
      <template #footer>
        <el-button type="primary" @click="fullscreenDialogVisible = false">关闭</el-button>
      </template>
    </el-dialog>

    <!-- 宽度用 min()：320~359px 宽的手机上写死 360px 会超出视口，而抽屉遮罩是 overflow:hidden，
         标题左半截会被直接裁掉。EP 的 addUnit 对非纯数字字符串原样透传，min() 可以直接当宽度用。 -->
    <el-drawer v-model="drawerVisible" title="右侧抽屉" size="min(360px, 100%)">
      <p class="motion-dialog-text">
        抽屉从右侧滑入，遮罩与面板取同一时长；离场换成更短的一档。
      </p>
    </el-drawer>
  </div>
</template>

<style scoped lang="scss">
.motion-page {
  padding-bottom: 40px;
}

.motion-header {
  padding: 4px 0 20px;
  border-bottom: 1px solid var(--dd-border-strong);
  margin-bottom: 20px;

  h1 {
    margin: 0 0 8px;
    font-size: 20px;
    font-weight: 600;
    color: var(--dd-text);
  }

  p {
    margin: 0;
    max-width: 900px;
    font-size: 13px;
    line-height: 1.7;
    color: var(--dd-text-2);
  }
}

.motion-section {
  padding: 20px;
  margin-bottom: 16px;
  // v4.0：卡片外观读主题令牌；半透明底与模糊由模板上的 .dd-glass 提供（玻璃关闭时自动退回实色）
  border: var(--dd-border-width) solid var(--dd-border);
  box-shadow: var(--dd-shadow-card);
  // 分节卡片属容器类表面 → surface 档（与业务页的 .table-card / .dd-data-card 同档，
  // 这样这页作为动效/形状预览入口时，观感与业务页一致，不会自成一套）
  border-radius: var(--dd-radius-surface);
  // 入场：与全站 dd-*-rise-in 同一语汇（opacity + translateY），时长吃令牌
  animation: dd-motion-rise-in var(--dd-motion-page) var(--dd-ease-decelerate) both;

  h2 {
    display: flex;
    align-items: center;
    gap: 10px;
    margin: 0 0 6px;
    font-size: 15px;
    font-weight: 600;
    color: var(--dd-text);
  }
}

.motion-section__no {
  font-size: 12px;
  font-weight: 700;
  color: var(--dd-accent-ink);
  font-variant-numeric: tabular-nums;
}

.motion-section__desc {
  margin: 0 0 16px;
  max-width: 900px;
  font-size: 13px;
  line-height: 1.7;
  color: var(--dd-text-2);

  code {
    padding: 1px 4px;
    // 行内 code 是文字级小块，归控件类 → control 档（与 el-tag 同档）
    border-radius: var(--dd-radius-control);
    background: var(--dd-surface-3);
    font-family: var(--dd-font-mono);
    font-size: 12px;
  }
}

.motion-demo {
  display: flex;
  flex-direction: column;
  gap: 14px;
}

.motion-row {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 10px;
}

// 内含多行控件（多选、日期范围）时顶端对齐，否则标签会被推到中间
.motion-row--top {
  align-items: flex-start;
}

.motion-label {
  flex: 0 0 auto;
  width: 110px;
  font-size: 12px;
  color: var(--dd-text-2);
}

.motion-chip {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: 4px 10px;
  border: 1px solid var(--dd-border-strong);
  // 这是包着「文字 + DdBadge」的描边小块，属控件类表面 → control 档；
  // 不吃 pill：它不是状态灯，内部还嵌了角标，做成胶囊会和角标本身的圆度打架
  border-radius: var(--dd-radius-control);
  font-size: 13px;
  color: var(--dd-text-2);
}

.motion-hint {
  font-size: 12px;
  line-height: 1.6;
  color: var(--dd-text-3);
}

.motion-link {
  font-size: 12px;
  color: var(--dd-accent-ink);

  // hover 只加下划线，不做位移（扁平规则）
  &:hover {
    text-decoration: underline;
  }
}

// 演示弹窗 / 抽屉里的说明文字（slot 内容归本组件作用域，scoped 能命中）
.motion-dialog-text {
  margin: 0;
  font-size: 13px;
  line-height: 1.7;
  color: var(--dd-text-2);
}

.motion-footer {
  padding: 16px 0 0;
  border-top: 1px solid var(--dd-border-strong);
  font-size: 12px;
  color: var(--dd-text-3);

  code {
    font-family: var(--dd-font-mono);
  }
}

@keyframes dd-motion-rise-in {
  from {
    opacity: 0;
    transform: translateY(12px);
  }
  to {
    opacity: 1;
    transform: translateY(0);
  }
}

// ==================== 00 主题预览 ====================
.theme-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(320px, 1fr));
  gap: 14px;
  margin-top: 18px;
}

.theme-cell {
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 12px;
  padding: 16px;
  border: 1px solid var(--dd-border-strong);
  border-radius: var(--dd-radius-surface);
  background: var(--dd-surface-2);

  h3 {
    margin: 0;
    font-size: 13px;
    font-weight: 600;
    color: var(--dd-text);
  }
}

.theme-cell--wide {
  grid-column: 1 / -1;
}

.theme-stack {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: 10px;

  > .el-input,
  > .el-select,
  > .el-slider {
    width: 100%;
    max-width: 360px;
  }

  > .el-alert {
    width: 100%;
  }
}

.theme-ink {
  font-size: 13px;
  font-weight: 600;
}

.theme-pagination {
  justify-content: flex-end;
}

.theme-cards {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(220px, 1fr));
  gap: 12px;
}

.theme-card {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: 6px;
  padding: 14px 16px;
  border: var(--dd-border-width) solid var(--dd-border);
  border-radius: var(--dd-radius-card);
  box-shadow: var(--dd-shadow-card);
  background: var(--dd-surface-solid);
  font-size: 12px;
  color: var(--dd-text-2);

  strong {
    font-size: 13px;
    color: var(--dd-text);
  }

  // .dd-glass 自己给半透明底，这里不要用实色压住它
  &.dd-glass {
    background: var(--dd-surface);
  }
}

.theme-rise-list {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
}

.theme-rise-item {
  width: 44px;
  height: 44px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  border-radius: var(--dd-radius-control);
  background: var(--dd-accent-soft);
  color: var(--dd-accent-ink);
  font-weight: 700;
}

@media screen and (max-width: 768px) {
  .motion-label {
    width: 100%;
  }
}
</style>
