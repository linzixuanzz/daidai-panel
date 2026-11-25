<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { CopyDocument, Download, Refresh, Search, Tickets } from '@element-plus/icons-vue'
import { ansiToHtml, normalizeAnsi } from '@/utils/ansi'
import { useLogAutoFollow } from '@/composables/useLogAutoFollow'
import type { PanelLogLevel } from '../usePanelLogViewer'

const props = defineProps<{
  loading: boolean
  refreshing?: boolean
  lines: number
  keyword: string
  level: PanelLogLevel
  autoRefresh: boolean
  logs: string[]
  total: number
  lastLoadedAt: string
  byteSizeLabel: string
  activePreset: 'default' | 'updates' | 'errors' | ''
  onRefresh: () => void | Promise<void>
  onApplyUpdatePreset: () => void
  onApplyErrorPreset: () => void
  onResetFilters: () => void
  onCopy: () => void | Promise<void>
  onDownload: () => void
}>()

const emit = defineEmits<{
  'update:lines': [value: number]
  'update:keyword': [value: string]
  'update:level': [value: PanelLogLevel]
  'update:autoRefresh': [value: boolean]
}>()

const levelOptions: Array<{ label: string; value: PanelLogLevel }> = [
  { label: '全部级别', value: '' },
  { label: 'Debug 及以上', value: 'debug' },
  { label: 'Info 及以上', value: 'info' },
  { label: 'Warn 及以上', value: 'warn' },
  { label: '仅 Error', value: 'error' },
]

const lineOptions = [100, 200, 500, 1000]

const renderedHtml = computed(() => ansiToHtml(normalizeAnsi(props.logs.join('\n'))))

const logViewRef = ref<HTMLElement>()
// 面板日志卡的自动跟随。这里只做到「不在底部就不拽回」：内容每 3s 整体替换，
// 跟随中贴到最新，用户上翻超过阈值后就不再被拽回底部（可以停在中间阅读）。
const panelFollow = useLogAutoFollow(logViewRef)

onMounted(() => {
  // 面板日志默认自动刷新，进卡即视为「运行中」并跟随最新
  panelFollow.begin(true)
})

watch(() => props.logs, () => {
  panelFollow.onContentChange()
}, { flush: 'post' })

// 切换关键词 / 级别 / 行数相当于换了一份内容，回到底部看最新
watch(() => [props.keyword, props.level, props.lines], () => {
  panelFollow.begin(true)
})
</script>

<template>
  <el-card shadow="never" class="panel-log-card" v-loading="loading">
    <template #header>
      <div class="card-header">
        <span class="card-title"><el-icon><Tickets /></el-icon> 面板日志</span>
      <div class="panel-log-card__meta">
          <span>共 {{ total }} 行</span>
          <span>{{ byteSizeLabel }}</span>
          <span v-if="lastLoadedAt">更新于 {{ lastLoadedAt }}</span>
          <span v-if="refreshing" class="panel-log-card__badge">同步中</span>
        </div>
      </div>
    </template>

    <div class="panel-log-toolbar">
      <el-input
        :model-value="keyword"
        placeholder="搜索关键词..."
        clearable
        class="panel-log-toolbar__search"
        :prefix-icon="Search"
        @update:model-value="emit('update:keyword', String($event || ''))"
      />

      <el-select
        :model-value="level"
        class="panel-log-toolbar__select"
        @update:model-value="emit('update:level', $event as PanelLogLevel)"
      >
        <el-option
          v-for="option in levelOptions"
          :key="option.value || 'all'"
          :label="option.label"
          :value="option.value"
        />
      </el-select>

      <el-select
        :model-value="lines"
        class="panel-log-toolbar__select panel-log-toolbar__select--lines"
        @update:model-value="emit('update:lines', Number($event))"
      >
        <el-option v-for="count in lineOptions" :key="count" :label="`最近 ${count} 行`" :value="count" />
      </el-select>

      <div class="panel-log-toolbar__actions">
        <el-switch
          :model-value="autoRefresh"
          inline-prompt
          active-text="自动刷新"
          inactive-text="手动"
          @update:model-value="emit('update:autoRefresh', Boolean($event))"
        />
        <div class="panel-log-toolbar__presets">
          <el-button
            size="small"
            :type="activePreset === 'updates' ? 'primary' : 'default'"
            plain
            @click="onApplyUpdatePreset"
          >
            只看更新日志
          </el-button>
          <el-button
            size="small"
            :type="activePreset === 'errors' ? 'danger' : 'default'"
            plain
            @click="onApplyErrorPreset"
          >
            只看错误日志
          </el-button>
          <el-button
            size="small"
            :type="activePreset === 'default' ? 'success' : 'default'"
            plain
            @click="onResetFilters"
          >
            恢复默认视图
          </el-button>
        </div>
        <div class="panel-log-toolbar__ops">
          <el-button @click="onRefresh">
            <el-icon><Refresh /></el-icon>刷新
          </el-button>
          <el-button :disabled="logs.length === 0" @click="onCopy">
            <el-icon><CopyDocument /></el-icon>复制
          </el-button>
          <el-button :disabled="logs.length === 0" @click="onDownload">
            <el-icon><Download /></el-icon>下载
          </el-button>
        </div>
      </div>
    </div>

    <div ref="logViewRef" class="panel-log-view dd-log-surface">
      <div v-if="logs.length === 0" class="panel-log-empty">
        当前筛选条件下暂无日志
      </div>
      <pre v-else class="panel-log-pre" v-html="renderedHtml"></pre>
    </div>
  </el-card>
</template>

<style scoped lang="scss">
@use './config-card-shared.scss' as *;

.panel-log-card {
  // 卡片本体属容器类表面 → surface 档
  border-radius: var(--dd-radius-surface);
  border: var(--dd-border-width, 1px) solid var(--dd-border, var(--el-border-color-lighter));
}

.panel-log-card__meta {
  display: inline-flex;
  gap: 12px;
  flex-wrap: wrap;
  font-size: 12px;
  color: var(--el-text-color-secondary);
}

.panel-log-card__badge {
  display: inline-flex;
  align-items: center;
  padding: 2px 8px;
  // 「同步中」状态 chip 是天然胶囊 → pill 档（与 .dd-status-chip 同档）
  border-radius: var(--dd-radius-pill);
  font-size: 11px;
  font-weight: 600;
  color: var(--dd-accent-ink);
  background: color-mix(in srgb, var(--el-color-primary) 10%, transparent);
}

// 双行 Flex 换行布局：筛选行与操作行解耦，防止 769~1366px 中等屏宽下操作按钮压垮搜索框或横向溢出
.panel-log-toolbar {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 10px 12px;
  margin-bottom: 16px;
}

.panel-log-toolbar__search {
  flex: 1 1 220px;
  min-width: 180px;
}

.panel-log-toolbar__select {
  flex: 0 0 160px;
  width: 160px;

  &--lines {
    flex-basis: 136px;
    width: 136px;
  }
}

.panel-log-toolbar__actions {
  flex: 1 1 100%;
  min-width: 0;
  padding: 4px 6px;
  // 按钮组的灰底槽 → control 档（与槽内按钮同档，圆角一致才不会露出内外错位的角）
  border-radius: var(--dd-radius-control);
  background: color-mix(in srgb, var(--el-fill-color-light) 84%, transparent);
  display: flex;
  justify-content: flex-start;
  align-items: center;
  gap: 8px 10px;
  flex-wrap: wrap;

  :deep(.el-button + .el-button) {
    margin-left: 0;
  }
}

.panel-log-toolbar__presets {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;

  :deep(.el-button + .el-button) {
    margin-left: 0;
  }
}

.panel-log-toolbar__ops {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
  margin-left: auto;

  :deep(.el-button + .el-button) {
    margin-left: 0;
  }
}

.panel-log-view {
  min-height: 540px;
  max-height: 76vh;
  overflow: auto;
  padding: 18px 20px;
}

.panel-log-pre {
  margin: 0;
  white-space: pre-wrap;
  word-break: break-all;
  font-family: var(--dd-font-mono);
  font-size: 13px;
  line-height: 1.7;
}

.panel-log-empty {
  min-height: 240px;
  display: flex;
  align-items: center;
  justify-content: center;
  color: color-mix(in srgb, var(--dd-log-text-color) 68%, transparent);
  font-size: 13px;
}

@media (max-width: 768px) {
  .panel-log-toolbar__search,
  .panel-log-toolbar__select,
  .panel-log-toolbar__select--lines {
    flex: 1 1 100%;
    width: 100%;
  }

  .panel-log-toolbar__actions {
    justify-content: flex-start;
  }

  .panel-log-toolbar__ops {
    margin-left: 0;
    width: 100%;
  }

  .panel-log-view {
    min-height: 360px;
    max-height: calc(100dvh - 280px);
    padding: 14px;
  }

  .panel-log-card__meta {
    gap: 8px;
  }
}
</style>
