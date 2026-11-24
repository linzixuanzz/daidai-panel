<script setup lang="ts">
import { computed } from 'vue'

const props = withDefaults(defineProps<{
  expressions?: string[]
  compact?: boolean
  numbered?: boolean
}>(), {
  expressions: () => [],
  compact: false,
  numbered: true,
})

const normalizedExpressions = computed(() =>
  (props.expressions || [])
    .map(expression => String(expression || '').trim())
    .filter(Boolean)
)

const isMultiLine = computed(() => normalizedExpressions.value.length > 1)
</script>

<template>
  <div
    class="task-cron-list"
    :class="{
      'task-cron-list--compact': compact,
      'task-cron-list--multi': isMultiLine
    }"
  >
    <div
      v-for="(expression, index) in normalizedExpressions"
      :key="`${index}-${expression}`"
      class="task-cron-list__item"
    >
      <span
        v-if="numbered && isMultiLine"
        class="task-cron-list__index"
      >
        {{ index + 1 }}
      </span>
      <!-- title 是窄屏单行省略时的兜底：列被压窄后表达式会被截断，鼠标悬停仍能看全 -->
      <code class="task-cron-list__code" :title="expression">{{ expression }}</code>
    </div>
  </div>
</template>

<style scoped lang="scss">
.task-cron-list {
  display: flex;
  flex-direction: column;
  gap: 6px;
  min-width: 0;
}

.task-cron-list--compact {
  gap: 4px;
}

.task-cron-list__item {
  display: flex;
  align-items: flex-start;
  gap: 8px;
  min-width: 0;
}

.task-cron-list__index {
  width: 18px;
  height: 18px;
  flex-shrink: 0;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  margin-top: 1px;
  // 18×18 的序号角标，属于天然胶囊那一类 → pill 档（圆角模式下正好是个圆序号）
  border-radius: var(--dd-radius-pill);
  background: color-mix(in srgb, var(--dd-accent) 10%, var(--dd-surface-solid));
  color: var(--dd-accent-ink);
  font-size: 11px;
  font-weight: 700;
  line-height: 1;
}

.task-cron-list__code {
  display: block;
  min-width: 0;
  width: 100%;
  padding: 6px 10px;
  // 表格单元格里的 cron 小代码块，尺寸接近输入框 → control 档（不是整块日志面板，不用 surface）
  border-radius: var(--dd-radius-control);
  // 底色不再混 white：暗色主题下会被顶成一块亮斑，上面的浅色字读不清（v4.0 巡检的低对比度项）
  background: var(--dd-surface-3);
  border: 1px solid var(--dd-border);
  color: var(--dd-text);
  font-family: var(--dd-font-mono);
  font-size: 12px;
  line-height: 1.5;
  white-space: pre-wrap;
  word-break: break-all;
}

.task-cron-list--compact .task-cron-list__code {
  padding: 4px 8px;
  font-size: 11px;
  line-height: 1.45;
  // 紧凑模式只收内边距和字号，圆角仍与常规态同档
  border-radius: var(--dd-radius-control);
}

.task-cron-list--multi .task-cron-list__code {
  color: var(--dd-text);
}
</style>
