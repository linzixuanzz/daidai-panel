<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { systemApi, type SystemHealthItem, type SystemHealthSnapshot } from '@/api/system'
import { ElMessage } from 'element-plus'
import { Refresh, CircleCheckFilled, CircleCloseFilled, WarningFilled } from '@element-plus/icons-vue'

const checking = ref(false)
const loadingSnapshot = ref(false)
const rawHealthItems = ref<SystemHealthItem[]>([])
const lastCheckedAt = ref('')

type HealthApiResponse = Partial<SystemHealthSnapshot> & {
  data?: Partial<SystemHealthSnapshot>
}

const nameMap: Record<string, string> = {
  database: '数据库连接',
  memory: '内存使用',
  scheduler: '定时任务调度',
  network: '外部网络连接',
}

const checked = computed(() => rawHealthItems.value.length > 0 || Boolean(lastCheckedAt.value))

const healthItems = computed(() =>
  rawHealthItems.value.map(item => ({
    name: nameMap[item.name] || item.name,
    status: item.status,
    message: item.message,
  }))
)

const lastCheckedLabel = computed(() => formatDateTime(lastCheckedAt.value))

function applySnapshot(response?: HealthApiResponse | null) {
  const payload = response?.data ?? response ?? {}
  rawHealthItems.value = Array.isArray(payload.items) ? payload.items : []
  lastCheckedAt.value = typeof payload.last_checked_at === 'string' ? payload.last_checked_at : ''
}

function formatDateTime(value?: string) {
  if (!value) {
    return ''
  }

  const parsed = new Date(value)
  if (Number.isNaN(parsed.getTime())) {
    return value
  }

  return new Intl.DateTimeFormat('zh-CN', {
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
    hour12: false,
  }).format(parsed)
}

async function loadSnapshot() {
  loadingSnapshot.value = true
  try {
    const res = await systemApi.healthStatus()
    applySnapshot(res)
  } catch (err: any) {
    ElMessage.error(err?.response?.data?.error || '加载健康检查记录失败')
  } finally {
    loadingSnapshot.value = false
  }
}

async function handleCheck() {
  checking.value = true
  try {
    const res = await systemApi.healthCheck()
    applySnapshot(res)
  } catch (err: any) {
    ElMessage.error(err?.response?.data?.error || '健康检查失败')
  } finally {
    checking.value = false
  }
}

function isOk(status: string) {
  return status === 'ok' || status === 'normal' || status === '正常'
}

function isWarning(status: string) {
  return status === 'warning' || status === 'warn' || status === '警告'
}

function statusLabel(status: string) {
  if (isOk(status)) return '正常'
  if (isWarning(status)) return '警告'
  return '异常'
}

onMounted(() => {
  void loadSnapshot()
})
</script>

<template>
  <el-card shadow="never" class="health-card dd-spot dd-rise" style="--dd-i: 3">
    <template #header>
      <div class="card-header">
        <div class="card-header__main">
          <span class="card-title"><el-icon><CircleCheckFilled /></el-icon> 系统健康</span>
          <span v-if="lastCheckedLabel" class="card-header__meta">上次检查：{{ lastCheckedLabel }}</span>
        </div>
        <el-button size="small" :loading="checking" @click="handleCheck">
          <el-icon><Refresh /></el-icon> 立即检查
        </el-button>
      </div>
    </template>

    <div v-if="loadingSnapshot && !checked" class="health-placeholder">
      正在加载最近一次健康检查记录...
    </div>

    <div v-else-if="!checked" class="health-placeholder">
      暂无健康检查记录，点击「立即检查」执行一次系统健康检测
    </div>

    <div v-else class="health-list">
      <div v-for="item in healthItems" :key="item.name" class="health-item">
        <div class="health-item__icon">
          <el-icon :size="18" :color="isOk(item.status) ? 'var(--dd-ok)' : (isWarning(item.status) ? 'var(--dd-warn)' : 'var(--dd-err)')">
            <CircleCheckFilled v-if="isOk(item.status)" />
            <WarningFilled v-else-if="isWarning(item.status)" />
            <CircleCloseFilled v-else />
          </el-icon>
        </div>
        <div class="health-item__body">
          <div class="health-item__name">{{ item.name }}</div>
          <div class="health-item__status" :class="{ 'is-ok': isOk(item.status), 'is-warning': isWarning(item.status), 'is-error': !isOk(item.status) && !isWarning(item.status) }">
            {{ statusLabel(item.status) }}
          </div>
        </div>
        <div v-if="item.message" class="health-item__message">{{ item.message }}</div>
      </div>
    </div>
  </el-card>
</template>

<style scoped lang="scss">
@use './config-card-shared.scss' as *;

.health-card {
  // 扁平化：描边宽走主题令牌；卡片本体 → surface 档
  border-radius: var(--dd-radius-surface);
  border: var(--dd-border-width, 1px) solid var(--dd-border, var(--el-border-color-lighter));
  height: 100%;
}

.card-header__main {
  display: flex;
  flex-direction: column;
  gap: 4px;
  min-width: 0;
}

.card-header__meta {
  font-size: 12px;
  color: var(--el-text-color-secondary);
}

.health-placeholder {
  text-align: center;
  padding: 32px 16px;
  color: var(--el-text-color-secondary);
  font-size: 13px;
}

.health-list {
  display: flex;
  flex-direction: column;
  gap: 0;
}

.health-item {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 6px 12px;
  padding: 12px 0;
  border-bottom: 1px dashed var(--dd-divider, var(--el-border-color-lighter));

  &:last-child {
    border-bottom: none;
    padding-bottom: 4px;
  }

  &:first-child {
    padding-top: 0;
  }
}

.health-item__icon {
  flex-shrink: 0;
}

.health-item__body {
  display: flex;
  align-items: center;
  gap: 8px;
  flex: 1 1 140px;
  min-width: 0;
}

.health-item__name {
  font-size: 14px;
  font-weight: 500;
  color: var(--el-text-color-primary);
}

.health-item__status {
  font-size: 12px;
  font-weight: 600;
  padding: 1px 8px;
  flex-shrink: 0;
  // 健康状态 chip 是天然胶囊 → pill 档（与 .dd-status-chip 同档）
  border-radius: var(--dd-radius-pill);

  // chip 文字用墨色、浅底用状态色 12%，都跟随主题
  &.is-ok {
    color: var(--dd-ok-ink);
    background: color-mix(in srgb, var(--dd-ok) 12%, transparent);
  }

  &.is-warning {
    color: var(--dd-warn-ink);
    background: color-mix(in srgb, var(--dd-warn) 12%, transparent);
  }

  &.is-error {
    color: var(--dd-err-ink);
    background: color-mix(in srgb, var(--dd-err) 12%, transparent);
  }
}

// 去掉 flex-shrink: 0 并允许换行/断词，避免长健康诊断文案把窄卡顶出横向溢出
.health-item__message {
  font-size: 12px;
  line-height: 1.5;
  color: var(--el-text-color-secondary);
  min-width: 0;
  max-width: 100%;
  overflow-wrap: anywhere;
  word-break: break-word;
}

@media (max-width: 768px) {
  .card-header__main {
    align-items: flex-start;
  }
}
</style>
