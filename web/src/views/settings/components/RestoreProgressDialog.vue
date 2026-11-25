<script setup lang="ts">
import {
  Box,
  CircleCheckFilled,
  Files,
  RefreshRight,
  Select,
  WarningFilled,
} from '@element-plus/icons-vue'
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import type { BackupSelection } from '@/api/system'

type RestoreVisualStatus =
  | 'idle'
  | 'running'
  | 'completed'
  | 'queued-restart'
  | 'restarting'
  | 'failed'
  | 'restart-timeout'

const props = defineProps<{
  visible: boolean
  fullscreen: boolean
  filename: string
  status: string
  stage: string
  message: string
  percent: number
  source?: string
  selection?: Partial<BackupSelection>
  startedAt?: string
  countdown: number
  errorMessage: string
  onClose: () => void | Promise<void>
  onRestartNow: () => void | Promise<void>
}>()

const now = ref(Date.now())
let elapsedTimer: ReturnType<typeof setInterval> | null = null

type RestoreStep = { key: string; title: string; hint: string }

const restoreSelection = computed<BackupSelection>(() => ({
  configs: Boolean(props.selection?.configs),
  tasks: Boolean(props.selection?.tasks),
  subscriptions: Boolean(props.selection?.subscriptions),
  env_vars: Boolean(props.selection?.env_vars),
  logs: Boolean(props.selection?.logs),
  scripts: Boolean(props.selection?.scripts),
  dependencies: Boolean(props.selection?.dependencies),
  task_views: Boolean(props.selection?.task_views),
}))

const sourceLabel = computed(() => {
  switch (props.source) {
    case 'qinglong':
      return '青龙备份'
    case 'daidai-panel':
      return '呆呆面板备份'
    default:
      return props.source ? `${props.source} 备份` : '面板备份'
  }
})

const selectedSummaryItems = computed(() => {
  const items: string[] = []
  if (restoreSelection.value.configs) items.push('配置')
  if (restoreSelection.value.tasks) items.push('任务')
  if (restoreSelection.value.subscriptions) items.push('订阅')
  if (restoreSelection.value.env_vars) items.push('环境变量')
  if (restoreSelection.value.scripts) items.push('脚本')
  if (restoreSelection.value.logs) items.push('日志')
  if (restoreSelection.value.dependencies) items.push('依赖')
  if (restoreSelection.value.task_views) items.push('任务视图')
  return items
})

const selectedSummaryText = computed(() => {
  if (selectedSummaryItems.value.length === 0) {
    return '正在按备份内容恢复面板数据'
  }
  return `本次将恢复：${selectedSummaryItems.value.join('、')}`
})

const restoreSteps = computed<RestoreStep[]>(() => {
  const hasCoreData =
    restoreSelection.value.configs ||
    restoreSelection.value.tasks ||
    restoreSelection.value.subscriptions ||
    restoreSelection.value.env_vars ||
    restoreSelection.value.task_views

  const hasFiles =
    restoreSelection.value.scripts ||
    restoreSelection.value.logs

  const hasRuntime =
    restoreSelection.value.dependencies ||
    restoreSelection.value.configs

  const writeHintParts: string[] = []
  if (restoreSelection.value.configs) writeHintParts.push('系统配置')
  if (restoreSelection.value.tasks) writeHintParts.push('任务定义')
  if (restoreSelection.value.subscriptions) writeHintParts.push('订阅与 SSH 密钥')
  if (restoreSelection.value.env_vars) writeHintParts.push('环境变量')
  if (restoreSelection.value.task_views) writeHintParts.push('任务视图')

  const settleHintParts: string[] = []
  if (restoreSelection.value.scripts) settleHintParts.push('脚本文件')
  if (restoreSelection.value.logs) settleHintParts.push('日志文件')
  if (restoreSelection.value.dependencies) settleHintParts.push('依赖重装任务')
  if (restoreSelection.value.configs) settleHintParts.push('镜像与调度')

  return [
    {
      key: 'prepare',
      title: '校验备份',
      hint: props.source === 'qinglong' ? '识别青龙结构并准备转换' : '读取文件并进行安全校验',
    },
    {
      key: 'unpack',
      title: '解包分析',
      hint: '识别清单内容与恢复范围',
    },
    {
      key: 'write',
      title: hasCoreData ? '恢复核心数据' : '整理恢复计划',
      hint: writeHintParts.length > 0 ? `写入 ${writeHintParts.join('、')}` : '本次恢复以文件与运行时状态为主',
    },
    {
      key: 'settle',
      title: hasFiles ? '恢复文件资源' : hasRuntime ? '整理运行环境' : '整理恢复结果',
      hint: settleHintParts.length > 0 ? `处理 ${settleHintParts.join('、')}` : '刷新任务状态与运行时配置',
    },
    {
      key: 'restart',
      title: '准备重启',
      hint: '等待面板重新上线',
    },
  ]
})

const statusText = computed(() => {
  switch (props.status) {
    case 'queued-restart':
      return '等待重启'
    case 'restarting':
      return '重启中'
    case 'completed':
      return '已完成'
    case 'failed':
      return '恢复失败'
    case 'restart-timeout':
      return '等待超时'
    default:
      return '恢复中'
  }
})

const dialogTitle = computed(() => {
  switch (props.status) {
    case 'queued-restart':
    case 'completed':
      return '恢复完成'
    case 'restarting':
      return '面板正在重启'
    case 'failed':
    case 'restart-timeout':
      return '恢复状态'
    default:
      return '正在恢复备份'
  }
})

const heroIcon = computed(() => {
  switch (props.status) {
    case 'queued-restart':
    case 'completed':
      return CircleCheckFilled
    case 'failed':
    case 'restart-timeout':
      return WarningFilled
    case 'restarting':
      return RefreshRight
    default:
      return Box
  }
})

const themeClass = computed(() => {
  switch (props.status) {
    case 'queued-restart':
    case 'completed':
      return 'is-success'
    case 'failed':
    case 'restart-timeout':
      return 'is-danger'
    case 'restarting':
      return 'is-restarting'
    default:
      return 'is-running'
  }
})

const progressWidth = computed(() => `${Math.max(2, Math.min(100, props.percent || 0))}%`)

const currentStepIndex = computed(() => {
  if (props.status === 'queued-restart' || props.status === 'completed' || props.status === 'restarting' || props.status === 'restart-timeout') {
    return 4
  }

  switch (props.stage) {
    case 'preparing':
    case 'reading':
    case 'decrypting':
      return 0
    case 'extracting':
    case 'analyzing':
      return 1
    case 'restoring-data':
      return 2
    case 'restoring-files':
    case 'restoring-mirrors':
    case 'finalizing':
      return 3
    case 'completed':
      return 4
    case 'failed':
      return 0
    default:
      return 0
  }
})

function stepState(index: number) {
  if (props.status === 'failed') {
    if (index < currentStepIndex.value) {
      return 'is-done'
    }
    if (index === currentStepIndex.value) {
      return 'is-failed'
    }
    return 'is-pending'
  }

  if (index < currentStepIndex.value) {
    return 'is-done'
  }
  if (index === currentStepIndex.value) {
    if (props.status === 'queued-restart' || props.status === 'completed') {
      return 'is-done'
    }
    return 'is-active'
  }
  return 'is-pending'
}

function tagIcon(item: string) {
  switch (item) {
    case '脚本':
    case '日志':
      return Files
    default:
      return Box
  }
}

const stageLabel = computed(() => {
  switch (props.stage) {
    case 'preparing':
      return '准备恢复'
    case 'reading':
      return '读取备份'
    case 'decrypting':
      return '解密备份'
    case 'extracting':
      return '解包校验'
    case 'analyzing':
      return '分析结构'
    case 'restoring-data':
      return '写入数据'
    case 'restoring-files':
      return '恢复文件'
    case 'restoring-mirrors':
      return '恢复镜像'
    case 'finalizing':
      return '收尾整理'
    case 'completed':
      return '恢复完成'
    default:
      return '处理中'
  }
})

const elapsedLabel = computed(() => {
  if (!props.startedAt) {
    return ''
  }
  const startedAt = new Date(props.startedAt).getTime()
  if (Number.isNaN(startedAt)) {
    return ''
  }
  const seconds = Math.max(0, Math.floor((now.value - startedAt) / 1000))
  return seconds < 60 ? `${seconds} 秒` : `${Math.floor(seconds / 60)} 分 ${seconds % 60} 秒`
})

function syncElapsedTimer(visible: boolean) {
  if (elapsedTimer) {
    clearInterval(elapsedTimer)
    elapsedTimer = null
  }
  if (!visible) {
    return
  }
  now.value = Date.now()
  elapsedTimer = setInterval(() => {
    now.value = Date.now()
  }, 1000)
}

watch(
  () => props.visible,
  (visible) => {
    syncElapsedTimer(visible)
  },
  { immediate: true },
)

onBeforeUnmount(() => {
  syncElapsedTimer(false)
})
</script>

<template>
  <el-dialog
    :model-value="visible"
    class="restore-progress-dialog"
    :title="dialogTitle"
    width="620px"
    :fullscreen="fullscreen"
    append-to-body
    :close-on-click-modal="false"
    :close-on-press-escape="status === 'failed' || status === 'restart-timeout'"
    :show-close="status === 'failed' || status === 'restart-timeout'"
    @close="onClose"
  >
    <div class="restore-progress-shell" :class="themeClass">
      <div class="restore-progress-hero">
        <div class="restore-progress-visual">
          <div class="restore-progress-orbit" />
          <div class="restore-progress-rings">
            <span class="ring ring--outer" />
            <span class="ring ring--inner" />
          </div>
          <div class="restore-progress-core">
            <el-icon class="restore-progress-core__icon"><component :is="heroIcon" /></el-icon>
            <strong>{{ percent }}%</strong>
            <span>{{ statusText }}</span>
          </div>
        </div>

        <div class="restore-progress-copy">
          <span class="restore-progress-eyebrow">{{ dialogTitle }}</span>
          <h3>{{ filename || '备份恢复任务' }}</h3>
          <p>{{ message }}</p>
          <div class="restore-progress-meta">
            <span>{{ sourceLabel }}</span>
            <span>{{ stageLabel }}</span>
            <span v-if="elapsedLabel">已耗时 {{ elapsedLabel }}</span>
            <span v-if="countdown > 0"> {{ countdown }} 秒后自动重启 </span>
          </div>
        </div>
      </div>

      <div class="restore-progress-bar">
        <div class="restore-progress-bar__track">
          <div class="restore-progress-bar__fill" :style="{ width: progressWidth }" />
        </div>
        <div class="restore-progress-bar__labels">
          <span>{{ selectedSummaryText }}</span>
          <strong>{{ percent }}%</strong>
        </div>
      </div>

      <div v-if="selectedSummaryItems.length > 0" class="restore-progress-tags">
        <span v-for="item in selectedSummaryItems" :key="item" class="restore-progress-tag">
          <el-icon><component :is="tagIcon(item)" /></el-icon>
          {{ item }}
        </span>
      </div>

      <div class="restore-progress-steps" :class="{ 'is-single': restoreSteps.length === 1 }">
        <div
          v-for="(step, index) in restoreSteps"
          :key="step.key"
          class="restore-step"
          :class="stepState(index)"
        >
          <div class="restore-step__badge">
            <el-icon v-if="stepState(index) === 'is-done'"><Select /></el-icon>
            <el-icon v-else-if="stepState(index) === 'is-failed'"><WarningFilled /></el-icon>
            <span v-else>{{ index + 1 }}</span>
          </div>
          <div class="restore-step__content">
            <strong>{{ step.title }}</strong>
            <span>{{ step.hint }}</span>
          </div>
        </div>
      </div>

      <div v-if="errorMessage && (status === 'failed' || status === 'restart-timeout')" class="restore-progress-error">
        <el-icon><WarningFilled /></el-icon>
        <div>
          <strong>{{ status === 'restart-timeout' ? '面板尚未重新连通' : '恢复失败原因' }}</strong>
          <p>{{ errorMessage }}</p>
        </div>
      </div>
    </div>

    <template #footer>
      <div class="restore-progress-footer">
        <el-button
          v-if="status === 'queued-restart'"
          type="primary"
          @click="onRestartNow"
        >
          立即重启
        </el-button>
        <el-button
          v-if="status === 'failed' || status === 'restart-timeout'"
          @click="onClose"
        >
          关闭
        </el-button>
      </div>
    </template>
  </el-dialog>
</template>

<style scoped lang="scss">
.restore-progress-shell {
  // 进度主色跟随主题：恢复中 = 信息色，成功 / 失败 / 重启中 = 对应状态色。
  // -ink 是当文字用的墨色；soft / strong 由 --dd-restore-accent 派生（在同一元素上计算，
  // 下面几个状态类只改 accent / accent-ink，soft / strong 会自动跟着变）。
  --dd-restore-accent: var(--dd-info);
  --dd-restore-accent-ink: var(--dd-info-ink);
  --dd-restore-accent-soft: color-mix(in srgb, var(--dd-restore-accent) 16%, transparent);
  --dd-restore-accent-strong: color-mix(in srgb, var(--dd-restore-accent) 28%, transparent);
  // 扁平化：头图底色改纯色，不再用光晕渐变堆质感
  --dd-restore-surface: var(--el-fill-color-light);
  display: flex;
  flex-direction: column;
  gap: 22px;
  padding: 6px 2px 2px;

  &.is-success {
    --dd-restore-accent: var(--dd-ok);
    --dd-restore-accent-ink: var(--dd-ok-ink);
  }

  &.is-danger {
    --dd-restore-accent: var(--dd-err);
    --dd-restore-accent-ink: var(--dd-err-ink);
  }

  &.is-restarting {
    --dd-restore-accent: var(--dd-warn);
    --dd-restore-accent-ink: var(--dd-warn-ink);
  }
}

.restore-progress-hero {
  display: grid;
  grid-template-columns: 190px minmax(0, 1fr);
  gap: 22px;
  align-items: center;
  padding: 22px;
  // 弹窗内的主视觉区块，属容器类表面 → surface 档
  border-radius: var(--dd-radius-surface);
  // 扁平化：去掉投影，用纯色底 + 1px 描边划分层次
  background: var(--dd-restore-surface);
  border: 1px solid var(--dd-restore-accent-soft);
}

.restore-progress-visual {
  position: relative;
  width: 168px;
  height: 168px;
  margin: 0 auto;
}

// 旋转中的加载圆弧：属于运动中的加载指示，方化会变形失去动效意义，保留圆形
.restore-progress-orbit {
  position: absolute;
  inset: 0;
  border-radius: 50%;
  // 副光弧走主题混色，避免亮色下硬编码纯白与卡片底色融在一起
  background:
    conic-gradient(
      from 210deg,
      transparent 0deg,
      transparent 42deg,
      var(--dd-restore-accent) 120deg,
      transparent 188deg,
      color-mix(in srgb, var(--dd-restore-accent) 42%, transparent) 320deg,
      transparent 360deg
    );
  filter: blur(0.4px);
  animation: restore-spin 5s linear infinite;
}

// 加载圆弧的同心环，与旋转指示同属一个加载部件，保留圆形；仅去掉 inset 高光
.restore-progress-rings .ring {
  position: absolute;
  inset: 18px;
  border-radius: 50%;
  border: 1px solid var(--dd-restore-accent-soft);
}

.restore-progress-rings .ring--outer {
  inset: 4px;
  animation: restore-pulse 2.4s ease-in-out infinite;
}

.restore-progress-rings .ring--inner {
  inset: 30px;
  border-style: dashed;
  animation: restore-pulse 2.4s ease-in-out infinite reverse;
}

.restore-progress-core {
  position: absolute;
  inset: 34px;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 6px;
  // 圆弧中心的读数盘，与外圈加载指示共用同一圆心，保留圆形
  border-radius: 50%;
  text-align: center;
  // 扁平化：去掉渐变与投影，纯色底 + 1px 描边与外圈区分
  background: var(--el-bg-color);
  border: 1px solid var(--el-border-color-lighter);

  strong {
    font-size: 30px;
    line-height: 1;
    color: var(--dd-restore-accent-ink);
  }

  span {
    font-size: 12px;
    color: var(--el-text-color-secondary);
    letter-spacing: 0.08em;
  }
}

.restore-progress-core__icon {
  font-size: 22px;
  color: var(--dd-restore-accent);
}

.restore-progress-copy {
  min-width: 0;

  h3 {
    margin: 8px 0 10px;
    max-width: 100%;
    overflow-wrap: anywhere;
    word-break: break-word;
    font-size: 24px;
    line-height: 1.2;
    color: var(--el-text-color-primary);
  }

  p {
    margin: 0;
    font-size: 14px;
    line-height: 1.8;
    color: var(--el-text-color-regular);
  }
}

.restore-progress-eyebrow {
  display: inline-flex;
  padding: 6px 10px;
  // 小标签块（不是状态灯）→ control 档，与 el-tag 保持同档
  border-radius: var(--dd-radius-control);
  font-size: 12px;
  font-weight: 700;
  letter-spacing: 0.08em;
  color: var(--dd-restore-accent-ink);
  background: var(--dd-restore-accent-soft);
}

.restore-progress-meta {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  margin-top: 16px;

  span {
    display: inline-flex;
    align-items: center;
    min-height: 30px;
    padding: 0 12px;
    // 元信息小标签属控件类表面 → control 档
    border-radius: var(--dd-radius-control);
    font-size: 12px;
    color: var(--el-text-color-secondary);
    background: var(--el-bg-color);
    border: 1px solid var(--el-border-color-lighter);
  }
}

.restore-progress-bar {
  padding: 18px 20px;
  // 进度区整块是容器类表面 → surface 档
  border-radius: var(--dd-radius-surface);
  background: var(--el-fill-color-lighter);
  border: 1px solid var(--el-border-color-lighter);
}

.restore-progress-bar__track {
  position: relative;
  height: 12px;
  overflow: hidden;
  // 进度条轨道是天然胶囊 → pill 档
  border-radius: var(--dd-radius-pill);
  background: var(--dd-surface-3);
}

.restore-progress-bar__fill {
  position: relative;
  height: 100%;
  // 进度条填充跟随轨道 → pill 档
  border-radius: var(--dd-radius-pill);
  background: linear-gradient(90deg, color-mix(in srgb, var(--dd-restore-accent) 82%, var(--dd-surface-solid)), var(--dd-restore-accent));
  transition: width 0.45s ease;

  &::after {
    content: '';
    position: absolute;
    inset: 0;
    // 流光高光走主题正文色混色，适配明暗双态而不写死纯白
    background: linear-gradient(90deg, transparent, color-mix(in srgb, var(--dd-text) 28%, transparent), transparent);
    transform: translateX(-100%);
    animation: restore-shimmer 1.8s ease-in-out infinite;
  }
}

.restore-progress-bar__labels {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 16px;
  margin-top: 12px;
  font-size: 13px;
  color: var(--el-text-color-secondary);

  span {
    flex: 1;
    min-width: 0;
  }

  strong {
    flex-shrink: 0;
    font-size: 15px;
    color: var(--dd-restore-accent-ink);
  }
}

.restore-progress-tags {
  display: flex;
  flex-wrap: wrap;
  gap: 10px;
}

.restore-progress-tag {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  max-width: 100%;
  min-height: 34px;
  padding: 6px 14px;
  // 恢复目标标签（不是状态灯）→ control 档
  border-radius: var(--dd-radius-control);
  font-size: 12px;
  font-weight: 600;
  color: var(--el-text-color-primary);
  background: var(--el-fill-color-lighter);
  border: 1px solid var(--el-border-color-lighter);
  overflow-wrap: anywhere;
  word-break: break-word;

  .el-icon {
    flex-shrink: 0;
    color: var(--dd-restore-accent);
    font-size: 13px;
  }
}

// 步进卡按实际步骤数自适应：单步全宽，多步自动 2~3 列排布，不再 5 列压成 82px
.restore-progress-steps {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(160px, 1fr));
  gap: 12px;

  &.is-single {
    grid-template-columns: 1fr;
  }
}

.restore-step {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 10px;
  padding: 16px 12px;
  // 步骤卡片是独立的容器类表面 → surface 档
  border-radius: var(--dd-radius-surface);
  text-align: center;
  background: var(--el-fill-color-lighter);
  border: 1px solid var(--el-border-color-lighter);
  // 扁平化：不再上浮与投影，仅靠描边和底色区分步骤状态
  transition: border-color 0.25s ease, background-color 0.25s ease;

  &.is-active {
    border-color: var(--dd-restore-accent);
    background: var(--dd-restore-accent-soft);
  }

  &.is-done {
    border-color: color-mix(in srgb, var(--dd-restore-accent) 36%, var(--dd-surface-solid));
    background: color-mix(in srgb, var(--dd-restore-accent) 8%, var(--el-bg-color));
  }

  &.is-failed {
    border-color: color-mix(in srgb, var(--dd-err) 30%, transparent);
    background: color-mix(in srgb, var(--dd-err) 8%, var(--el-bg-color));
  }
}

.restore-step__badge {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 36px;
  height: 36px;
  // 静态的步骤序号底块，属控件类表面 → control 档（不做成正圆，避免与状态灯/头像混淆）
  border-radius: var(--dd-radius-control);
  font-size: 14px;
  font-weight: 700;
  color: var(--el-text-color-secondary);
  background: var(--el-bg-color);
  border: 1px solid var(--el-border-color-lighter);
}

.restore-step.is-active .restore-step__badge,
.restore-step.is-done .restore-step__badge {
  color: var(--dd-restore-accent-ink);
  border-color: var(--dd-restore-accent);
}

.restore-step.is-failed .restore-step__badge {
  color: var(--dd-err-ink);
  border-color: color-mix(in srgb, var(--dd-err) 30%, transparent);
}

.restore-step__content {
  strong {
    display: block;
    font-size: 13px;
    color: var(--el-text-color-primary);
  }

  span {
    display: block;
    margin-top: 6px;
    font-size: 12px;
    line-height: 1.55;
    color: var(--el-text-color-secondary);
  }
}

.restore-progress-error {
  display: grid;
  grid-template-columns: 20px minmax(0, 1fr);
  gap: 12px;
  align-items: start;
  padding: 16px 18px;
  // 错误提示是弹窗内的独立区块 → surface 档
  border-radius: var(--dd-radius-surface);
  // 错误框：错误色浅底 + 墨色标题，正文比标题更靠近正文色（明暗两档都成立，不再需要暗色补丁）
  background: color-mix(in srgb, var(--dd-err) 8%, var(--el-bg-color));
  border: 1px solid color-mix(in srgb, var(--dd-err) 24%, transparent);
  color: var(--dd-err-ink);

  strong {
    display: block;
    margin-bottom: 6px;
    font-size: 14px;
  }

  p {
    margin: 0;
    line-height: 1.7;
    color: color-mix(in srgb, var(--dd-err) 30%, var(--dd-text));
    // 与 UpdateProgressDialog 的错误区块对齐：errorMessage 直接透传后端文案，
    // 后端诊断类提示是分行的，不保留换行的话 \n 会被 HTML 折叠成空格，逐条步骤会全挤成一行。
    // 与下面的 word-break 不冲突：pre-wrap 管「保留换行 + 允许软换行」，
    // word-break 管「给超长无空格串（长路径、长错误串）补断点」，作用在换行决策的不同环节。
    white-space: pre-wrap;
    word-break: break-word;
  }
}

.restore-progress-footer {
  display: flex;
  justify-content: flex-end;
  gap: 10px;
}

@keyframes restore-spin {
  from {
    transform: rotate(0deg);
  }
  to {
    transform: rotate(360deg);
  }
}

@keyframes restore-pulse {
  0%,
  100% {
    transform: scale(1);
    opacity: 0.7;
  }
  50% {
    transform: scale(1.03);
    opacity: 1;
  }
}

@keyframes restore-shimmer {
  to {
    transform: translateX(100%);
  }
}

@media (max-width: 768px) {
  .restore-progress-hero {
    grid-template-columns: 1fr;
    text-align: center;
  }

  .restore-progress-meta {
    justify-content: center;
  }

  .restore-progress-steps {
    grid-template-columns: 1fr;
  }
}
</style>
