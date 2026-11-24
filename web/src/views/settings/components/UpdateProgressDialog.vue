<script setup lang="ts">
import {
  Box,
  CircleCheckFilled,
  Download,
  RefreshRight,
  Select,
  WarningFilled,
} from '@element-plus/icons-vue'
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import type { PanelUpdateStatus } from '@/api/system'
import { useResponsive } from '@/composables/useResponsive'

type UpdateVisualStatus = 'idle' | 'running' | 'restarting' | 'completed' | 'failed' | 'timeout'

const props = defineProps<{
  visible: boolean
  currentVersion: string
  latestVersion?: string
  releaseUrl?: string
  status: UpdateVisualStatus
  updateStatus: PanelUpdateStatus | null
  errorMessage: string
  onClose: () => void | Promise<void>
}>()

const { dialogFullscreen } = useResponsive()
const now = ref(Date.now())
let elapsedTimer: ReturnType<typeof setInterval> | null = null

const deploymentType = computed(() => props.updateStatus?.deployment_type || 'docker')
const isBinaryUpdate = computed(() => deploymentType.value === 'binary')
const isMagiskUpdate = computed(() => deploymentType.value === 'magisk')
// 二进制部署与 Magisk 模块版都是「下载包 -> 替换文件 -> 重启进程」的形态，
// 阶段序列一致，进度与步骤索引共用同一套映射。
const isFileReplaceUpdate = computed(() => isBinaryUpdate.value || isMagiskUpdate.value)
const isWatchtowerUpdate = computed(() => props.updateStatus?.update_manager === 'watchtower')

const watchtowerSteps = [
  {
    key: 'trigger',
    title: '提交更新检查',
    hint: '由 Watchtower 接管镜像检查和容器更新',
  },
] as const

const dockerSteps = [
  {
    key: 'prepare',
    title: '校验环境',
    hint: '检查 Docker、Socket 与当前容器信息',
  },
  {
    key: 'pull',
    title: '拉取镜像',
    hint: '下载并准备最新版本镜像',
  },
  {
    key: 'helper',
    title: '启动辅助容器',
    hint: '由辅助容器接管平滑更新流程',
  },
  {
    key: 'switch',
    title: '切换新版本',
    hint: '移除旧容器并重建同配置实例',
  },
  {
    key: 'wait',
    title: '等待上线',
    hint: '轮询检测新版本服务重新连通',
  },
] as const

const binarySteps = [
  {
    key: 'prepare',
    title: '校验环境',
    hint: '识别当前平台、安装目录与 Release 更新包',
  },
  {
    key: 'download',
    title: '下载更新包',
    hint: '从 GitHub Release 下载当前平台二进制包',
  },
  {
    key: 'extract',
    title: '解压校验',
    hint: '安全解压并校验目标程序文件',
  },
  {
    key: 'apply',
    title: '后台替换',
    hint: '保留配置与数据目录，替换程序和前端文件',
  },
  {
    key: 'wait',
    title: '等待上线',
    hint: '轮询检测新版本服务重新连通',
  },
] as const

const magiskSteps = [
  {
    key: 'prepare',
    title: '校验环境',
    hint: '识别模块目录、外壳版本与 Release 更新包',
  },
  {
    key: 'download',
    title: '下载更新包',
    hint: '从 GitHub Release 下载当前架构的面板程序与前端',
  },
  {
    key: 'extract',
    title: '解压校验',
    hint: '安全解压并校验面板程序与前端目录',
  },
  {
    key: 'apply',
    title: '替换并同步模块',
    hint: '替换容器内的面板程序与前端，并写回模块目录防止重启回滚',
  },
  {
    key: 'wait',
    title: '等待上线',
    hint: '轮询检测新版本服务重新连通',
  },
] as const

const steps = computed(() =>
  isWatchtowerUpdate.value
    ? watchtowerSteps
    : isMagiskUpdate.value
    ? magiskSteps
    : isBinaryUpdate.value
    ? binarySteps
    : dockerSteps
)
const currentPhase = computed(() => props.updateStatus?.phase || 'preparing')
const progressPercent = computed(() => {
  if (props.status === 'completed') {
    return 100
  }
  if (props.status === 'timeout') {
    return 96
  }
  if (isFileReplaceUpdate.value) {
    switch (currentPhase.value) {
      case 'preparing':
        return 12
      case 'downloading':
        return 38
      case 'extracting':
        return 58
      // syncing 只有 Magisk 模块版会出现：把新文件写回模块目录，防止重启回滚。
      case 'syncing':
        return 68
      case 'scheduling':
        return 76
      case 'restarting':
        return 92
      case 'failed':
        return 18
      default:
        return props.status === 'restarting' ? 92 : 8
    }
  }
  switch (currentPhase.value) {
    case 'preparing':
      return 12
    case 'pulling':
      return 42
    case 'scheduling':
      return 72
    case 'restarting':
      return 92
    case 'failed':
      return 18
    default:
      return props.status === 'restarting' ? 92 : 8
  }
})

const currentStepIndex = computed(() => {
  if (isWatchtowerUpdate.value) {
    return 0
  }
  if (props.status === 'restarting' || props.status === 'timeout') {
    return 4
  }
  if (isFileReplaceUpdate.value) {
    switch (currentPhase.value) {
      case 'preparing':
        return 0
      case 'downloading':
        return 1
      case 'extracting':
        return 2
      case 'syncing':
        return 3
      case 'scheduling':
        return 3
      case 'restarting':
        return 4
      case 'failed':
        return 0
      default:
        return 0
    }
  }
  switch (currentPhase.value) {
    case 'preparing':
      return 0
    case 'pulling':
      return 1
    case 'scheduling':
      return 2
    case 'restarting':
      return 3
    case 'failed':
      return 0
    default:
      return 0
  }
})

const dialogTitle = computed(() => {
  switch (props.status) {
    case 'completed':
      return isWatchtowerUpdate.value ? '已交给 Watchtower' : '更新请求已完成'
    case 'restarting':
      return '正在切换新版本'
    case 'failed':
    case 'timeout':
      return '系统更新状态'
    default:
      return '系统更新中'
  }
})

const statusText = computed(() => {
  switch (props.status) {
    case 'completed':
      return isWatchtowerUpdate.value ? '已接管' : '已完成'
    case 'restarting':
      return '重启切换中'
    case 'failed':
      return '更新失败'
    case 'timeout':
      return '等待超时'
    default:
      return '更新中'
  }
})

const themeClass = computed(() => {
  switch (props.status) {
    case 'completed':
      return 'is-completed'
    case 'failed':
    case 'timeout':
      return 'is-danger'
    case 'restarting':
      return 'is-switching'
    default:
      return 'is-running'
  }
})

const heroIcon = computed(() => {
  switch (props.status) {
    case 'completed':
      return CircleCheckFilled
    case 'failed':
    case 'timeout':
      return WarningFilled
    case 'restarting':
      return RefreshRight
    default:
      return Download
  }
})

const versionLabel = computed(() => {
  if (!props.latestVersion) {
    return `当前版本 ${props.currentVersion || '-'}`
  }
  return `${props.currentVersion || '-'} → ${props.latestVersion}`
})

const summaryMessage = computed(() => {
  if (props.status === 'timeout') {
    return '更新任务大概率已经进入重启切换，但页面还没有重新连通。'
  }
  return props.updateStatus?.message || '正在执行更新任务，请稍候...'
})

const startedAtLabel = computed(() => props.updateStatus?.started_at || '')

const elapsedLabel = computed(() => {
  if (!startedAtLabel.value) {
    return ''
  }
  const startedAt = new Date(startedAtLabel.value).getTime()
  if (Number.isNaN(startedAt)) {
    return ''
  }
  const seconds = Math.max(0, Math.floor((now.value - startedAt) / 1000))
  return seconds < 60 ? `${seconds} 秒` : `${Math.floor(seconds / 60)} 分 ${seconds % 60} 秒`
})

function stepState(index: number) {
  if (props.status === 'completed') {
    return 'is-done'
  }
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
    return 'is-active'
  }
  return 'is-pending'
}

function phaseLabel(phase: string) {
  if (isFileReplaceUpdate.value) {
    switch (phase) {
      case 'preparing':
        return '环境校验'
      case 'downloading':
        return '下载更新包'
      case 'extracting':
        return '解压校验'
      case 'syncing':
        return '同步模块目录'
      case 'scheduling':
        return '后台替换'
      case 'restarting':
        return '重启切换'
      case 'failed':
        return '更新失败'
      default:
        return '处理中'
    }
  }
  switch (phase) {
    case 'watchtower-triggered':
      return 'Watchtower 已接管'
    case 'preparing':
      return '环境校验'
    case 'pulling':
      return '拉取镜像'
    case 'scheduling':
      return '调度更新'
    case 'restarting':
      return '切换新版本'
    case 'failed':
      return '更新失败'
    default:
      return '处理中'
  }
}

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
    class="update-progress-dialog"
    :title="dialogTitle"
    width="640px"
    :fullscreen="dialogFullscreen"
    append-to-body
    :close-on-click-modal="false"
    :close-on-press-escape="status === 'completed' || status === 'failed' || status === 'timeout'"
    :show-close="status === 'completed' || status === 'failed' || status === 'timeout'"
    @close="onClose"
  >
    <div class="update-progress-shell" :class="themeClass">
      <div class="update-progress-hero">
        <div class="update-progress-visual">
          <div class="update-progress-orbit" />
          <div class="update-progress-rings">
            <span class="ring ring--outer" />
            <span class="ring ring--inner" />
          </div>
          <div class="update-progress-core">
            <el-icon class="update-progress-core__icon"><component :is="heroIcon" /></el-icon>
            <strong>{{ progressPercent }}%</strong>
            <span>{{ statusText }}</span>
          </div>
        </div>

        <div class="update-progress-copy">
          <span class="update-progress-eyebrow">{{ dialogTitle }}</span>
          <h3>系统更新任务</h3>
          <p>{{ summaryMessage }}</p>
          <div class="update-progress-meta">
            <span>{{ versionLabel }}</span>
            <span>{{ phaseLabel(currentPhase) }}</span>
            <span v-if="elapsedLabel">已耗时 {{ elapsedLabel }}</span>
          </div>
        </div>
      </div>

      <div class="update-progress-bar">
        <div class="update-progress-bar__track">
          <div class="update-progress-bar__fill" :style="{ width: `${progressPercent}%` }" />
        </div>
        <div class="update-progress-bar__labels">
          <span>{{ summaryMessage }}</span>
          <strong>{{ progressPercent }}%</strong>
        </div>
      </div>

      <div class="update-progress-targets">
        <span v-if="isFileReplaceUpdate && updateStatus?.asset_name" class="update-target-chip">
          <el-icon><Download /></el-icon>
          更新包：{{ updateStatus.asset_name }}
        </span>
        <span v-if="isBinaryUpdate && updateStatus?.install_dir" class="update-target-chip">
          <el-icon><Box /></el-icon>
          目录：{{ updateStatus.install_dir }}
        </span>
        <span v-if="isBinaryUpdate && updateStatus?.binary_name" class="update-target-chip">
          <el-icon><Download /></el-icon>
          程序：{{ updateStatus.binary_name }}
        </span>
        <span v-if="!isFileReplaceUpdate && updateStatus?.container_name" class="update-target-chip">
          <el-icon><Box /></el-icon>
          容器：{{ updateStatus.container_name }}
        </span>
        <span v-if="!isFileReplaceUpdate && updateStatus?.image_name" class="update-target-chip">
          <el-icon><Download /></el-icon>
          镜像：{{ updateStatus.image_name }}
        </span>
        <span
          v-if="!isFileReplaceUpdate && updateStatus?.pull_image_name && updateStatus.pull_image_name !== updateStatus.image_name"
          class="update-target-chip"
        >
          <el-icon><Download /></el-icon>
          拉取：{{ updateStatus.pull_image_name }}
        </span>
        <span v-if="!isFileReplaceUpdate && updateStatus?.mirror_host" class="update-target-chip">
          <el-icon><RefreshRight /></el-icon>
          镜像源：{{ updateStatus.mirror_host }}
        </span>
      </div>

      <div class="update-progress-steps">
        <div
          v-for="(step, index) in steps"
          :key="step.key"
          class="update-step"
          :class="stepState(index)"
        >
          <div class="update-step__badge">
            <el-icon v-if="stepState(index) === 'is-done'"><Select /></el-icon>
            <el-icon v-else-if="stepState(index) === 'is-failed'"><WarningFilled /></el-icon>
            <span v-else>{{ index + 1 }}</span>
          </div>
          <div class="update-step__content">
            <strong>{{ step.title }}</strong>
            <span>{{ step.hint }}</span>
          </div>
        </div>
      </div>

      <div v-if="errorMessage && (status === 'failed' || status === 'timeout')" class="update-progress-error">
        <el-icon><WarningFilled /></el-icon>
        <div>
          <strong>{{ status === 'timeout' ? '暂未检测到新版本重新上线' : '更新失败原因' }}</strong>
          <p>{{ errorMessage }}</p>
        </div>
      </div>
    </div>

    <template #footer>
      <div class="update-progress-footer">
        <a v-if="releaseUrl" :href="releaseUrl" target="_blank" rel="noreferrer">
          <el-button>查看发布说明</el-button>
        </a>
        <el-button
          v-if="status === 'completed' || status === 'failed' || status === 'timeout'"
          @click="onClose"
        >
          关闭
        </el-button>
      </div>
    </template>
  </el-dialog>
</template>

<style scoped lang="scss">
.update-progress-shell {
  // 进度主色跟随主题：进行中 = 主色，失败 = 错误色，切换中 = 信息色。
  // -ink 是当文字用的墨色；soft / strong 由 --dd-update-accent 派生（在同一元素上计算，
  // 下面两个状态类只改 accent / accent-ink，soft / strong 会自动跟着变）。
  --dd-update-accent: var(--dd-accent);
  --dd-update-accent-ink: var(--dd-accent-ink);
  --dd-update-accent-soft: color-mix(in srgb, var(--dd-update-accent) 16%, transparent);
  --dd-update-accent-strong: color-mix(in srgb, var(--dd-update-accent) 28%, transparent);
  display: flex;
  flex-direction: column;
  gap: 22px;
  padding: 6px 2px 2px;

  &.is-danger {
    --dd-update-accent: var(--dd-err);
    --dd-update-accent-ink: var(--dd-err-ink);
  }

  &.is-switching {
    --dd-update-accent: var(--dd-info);
    --dd-update-accent-ink: var(--dd-info-ink);
  }
}

.update-progress-hero {
  display: grid;
  grid-template-columns: 190px minmax(0, 1fr);
  gap: 22px;
  align-items: center;
  padding: 22px;
  // 弹窗内的主视觉区块，属容器类表面 → surface 档
  border-radius: var(--dd-radius-surface);
  // 扁平化：去掉光晕渐变与投影，用纯色底 + 1px 描边划分层次
  background: var(--el-fill-color-light);
  border: 1px solid var(--dd-update-accent-soft);
}

.update-progress-visual {
  position: relative;
  width: 168px;
  height: 168px;
  margin: 0 auto;
}

// 旋转中的加载圆弧：属于运动中的加载指示，方化会变形失去动效意义，保留圆形
.update-progress-orbit {
  position: absolute;
  inset: 0;
  border-radius: 50%;
  background:
    conic-gradient(from 210deg, transparent 0deg, transparent 48deg, var(--dd-update-accent) 128deg, transparent 196deg, rgba(255, 255, 255, 0.82) 322deg, transparent 360deg);
  animation: update-spin 5.2s linear infinite;
}

// 加载圆弧的同心环，与旋转指示同属一个加载部件，保留圆形；仅去掉 inset 高光
.update-progress-rings .ring {
  position: absolute;
  inset: 18px;
  border-radius: 50%;
  border: 1px solid var(--dd-update-accent-soft);
}

.update-progress-rings .ring--outer {
  inset: 4px;
  animation: update-pulse 2.4s ease-in-out infinite;
}

.update-progress-rings .ring--inner {
  inset: 30px;
  border-style: dashed;
  animation: update-pulse 2.4s ease-in-out infinite reverse;
}

.update-progress-core {
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
    color: var(--dd-update-accent-ink);
  }

  span {
    font-size: 12px;
    color: var(--el-text-color-secondary);
    letter-spacing: 0.08em;
  }
}

.update-progress-core__icon {
  font-size: 22px;
  color: var(--dd-update-accent);
}

.update-progress-copy {
  min-width: 0;

  h3 {
    margin: 8px 0 10px;
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

.update-progress-eyebrow {
  display: inline-flex;
  padding: 6px 10px;
  // 小标签块（不是状态灯）→ control 档，与 el-tag 保持同档
  border-radius: var(--dd-radius-control);
  font-size: 12px;
  font-weight: 700;
  letter-spacing: 0.08em;
  color: var(--dd-update-accent-ink);
  background: var(--dd-update-accent-soft);
}

.update-progress-meta {
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

.update-progress-bar {
  padding: 18px 20px;
  // 进度区整块是容器类表面 → surface 档
  border-radius: var(--dd-radius-surface);
  background: var(--el-fill-color-lighter);
  border: 1px solid var(--el-border-color-lighter);
}

.update-progress-bar__track {
  position: relative;
  height: 12px;
  overflow: hidden;
  // 进度条轨道是天然胶囊 → pill 档
  border-radius: var(--dd-radius-pill);
  background: var(--dd-surface-3);
}

.update-progress-bar__fill {
  position: relative;
  height: 100%;
  // 进度条填充跟随轨道 → pill 档
  border-radius: var(--dd-radius-pill);
  background: linear-gradient(90deg, color-mix(in srgb, var(--dd-update-accent) 82%, white), var(--dd-update-accent));
  transition: width 0.45s ease;

  &::after {
    content: '';
    position: absolute;
    inset: 0;
    background: linear-gradient(90deg, transparent, rgba(255, 255, 255, 0.55), transparent);
    transform: translateX(-100%);
    animation: update-shimmer 1.8s ease-in-out infinite;
  }
}

.update-progress-bar__labels {
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
    color: var(--dd-update-accent-ink);
  }
}

.update-progress-targets {
  display: flex;
  flex-wrap: wrap;
  gap: 10px;
}

.update-target-chip {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  min-height: 34px;
  padding: 0 14px;
  // 更新目标标签（不是状态灯）→ control 档
  border-radius: var(--dd-radius-control);
  font-size: 12px;
  font-weight: 600;
  color: var(--el-text-color-primary);
  background: var(--el-fill-color-lighter);
  border: 1px solid var(--el-border-color-lighter);

  .el-icon {
    color: var(--dd-update-accent);
    font-size: 13px;
  }
}

.update-progress-steps {
  display: grid;
  grid-template-columns: repeat(5, minmax(0, 1fr));
  gap: 12px;
}

.update-step {
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
    border-color: var(--dd-update-accent);
    background: var(--dd-update-accent-soft);
  }

  &.is-done {
    border-color: color-mix(in srgb, var(--dd-update-accent) 36%, var(--dd-surface-solid));
    background: color-mix(in srgb, var(--dd-update-accent) 8%, var(--el-bg-color));
  }

  &.is-failed {
    border-color: color-mix(in srgb, var(--dd-err) 30%, transparent);
    background: color-mix(in srgb, var(--dd-err) 8%, var(--el-bg-color));
  }
}

.update-step__badge {
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

.update-step.is-active .update-step__badge,
.update-step.is-done .update-step__badge {
  color: var(--dd-update-accent-ink);
  border-color: var(--dd-update-accent);
}

.update-step.is-failed .update-step__badge {
  color: var(--dd-err-ink);
  border-color: color-mix(in srgb, var(--dd-err) 30%, transparent);
}

.update-step__content {
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

.update-progress-error {
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
    // 后端的更新失败诊断是分行文案（判定结论 / 根因 / 逐条自查命令 / 原始错误），
    // 不保留换行的话 \n 会被 HTML 折叠成空格，编号步骤全挤成一行没法照着做。
    // 与下面的 word-break 不冲突：pre-wrap 管「保留换行 + 允许软换行」，
    // word-break 管「给超长无空格串（长 URL、长错误串）补断点」，作用在换行决策的不同环节。
    white-space: pre-wrap;
    word-break: break-word;
  }
}

.update-progress-footer {
  display: flex;
  justify-content: flex-end;
  gap: 10px;
}

@keyframes update-spin {
  from {
    transform: rotate(0deg);
  }
  to {
    transform: rotate(360deg);
  }
}

@keyframes update-pulse {
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

@keyframes update-shimmer {
  to {
    transform: translateX(100%);
  }
}

@media (max-width: 768px) {
  .update-progress-hero {
    grid-template-columns: 1fr;
    text-align: center;
  }

  .update-progress-meta {
    justify-content: center;
  }

  .update-progress-targets {
    justify-content: center;
  }

  .update-progress-steps {
    grid-template-columns: 1fr;
  }
}
</style>
