<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useResponsive } from '@/composables/useResponsive'
import { generateRandomCron, describeRandomCron } from '@/utils/cronRandom'

const props = defineProps<{
  visible: boolean
}>()

const emit = defineEmits<{
  'update:visible': [value: boolean]
  // 应用时把生成好的一组表达式交回去，由 CronInput 决定写进哪几条规则
  'apply': [expressions: string[]]
}>()

const { dialogFullscreen } = useResponsive()

// 时间窗预设。默认「白天 6-23」而不是全天：随机落到凌晨 2~5 点的脚本一旦失败，
// 用户第二天早上才看得到，排障成本高得多。
const WINDOW_PRESETS: Record<string, { label: string; start: number; end: number }> = {
  all: { label: '全天 0-23', start: 0, end: 23 },
  day: { label: '白天 6-23', start: 6, end: 23 },
  night: { label: '夜间 0-6', start: 0, end: 6 },
}

const count = ref(2)
const windowPreset = ref<'all' | 'day' | 'night' | 'custom'>('day')
const customStart = ref(6)
const customEnd = ref(23)
const randomizeSecond = ref(true)
const mode = ref<'split' | 'merged'>('split')
const previews = ref<string[]>([])

const hourOptions = Array.from({ length: 24 }, (_, hour) => hour)

const windowRange = computed(() => {
  const preset = WINDOW_PRESETS[windowPreset.value]
  if (preset) {
    return { start: preset.start, end: preset.end }
  }
  return { start: customStart.value, end: customEnd.value }
})

// 跨零点（结束小时早于起始小时）不生成：generateRandomCron 对这种入参返回空数组，
// 这里把它显式变成一条界面提示，而不是让用户看着空预览发懵。
const windowInvalid = computed(() => windowRange.value.end < windowRange.value.start)

// 窗口内可选小时数不够时，生成器会把点数钳制下来，提前告诉用户免得以为是 bug
const windowHourCount = computed(() => windowInvalid.value ? 0 : windowRange.value.end - windowRange.value.start + 1)
const countClamped = computed(() => !windowInvalid.value && count.value > windowHourCount.value)

function regenerate() {
  if (windowInvalid.value) {
    previews.value = []
    return
  }
  previews.value = generateRandomCron({
    count: count.value,
    startHour: windowRange.value.start,
    endHour: windowRange.value.end,
    randomizeSecond: randomizeSecond.value,
    mode: mode.value,
  })
}

// 任何一项参数变化都重新随机一次，做到「所见即所得」；打开弹层时也生成一份初值
watch([count, windowPreset, customStart, customEnd, randomizeSecond, mode], regenerate)
watch(
  () => props.visible,
  (value) => {
    if (value) regenerate()
  },
  { immediate: true }
)

function handleApply() {
  if (previews.value.length === 0) {
    return
  }
  emit('apply', [...previews.value])
  emit('update:visible', false)
}
</script>

<template>
  <el-dialog
    :model-value="visible"
    title="随机时间"
    width="600px"
    :fullscreen="dialogFullscreen"
    :lock-scroll="false"
    :close-on-click-modal="false"
    @update:model-value="emit('update:visible', $event)"
  >
    <el-form :label-width="dialogFullscreen ? 'auto' : '92px'" :label-position="dialogFullscreen ? 'top' : 'right'">
      <el-form-item label="时间点数量">
        <div class="random-field-block">
          <el-input-number v-model="count" :min="1" :max="6" />
          <div v-if="countClamped" class="random-field-hint is-warning">
            当前时间窗只有 {{ windowHourCount }} 个小时可选，实际会按 {{ windowHourCount }} 个时间点生成。
          </div>
        </div>
      </el-form-item>

      <el-form-item label="时间窗">
        <div class="random-field-block">
          <el-radio-group v-model="windowPreset">
            <el-radio v-for="(preset, key) in WINDOW_PRESETS" :key="key" :value="key">{{ preset.label }}</el-radio>
            <el-radio value="custom">自定义</el-radio>
          </el-radio-group>
          <!-- 宽度靠外层 :deep(.el-select) 控制：给 el-select 直接挂 class 在 scoped 里命中不到它的根元素 -->
          <div v-if="windowPreset === 'custom'" class="random-inline-input">
            <el-select v-model="customStart">
              <el-option v-for="hour in hourOptions" :key="`s-${hour}`" :label="`${hour} 点`" :value="hour" />
            </el-select>
            <span>至</span>
            <el-select v-model="customEnd">
              <el-option v-for="hour in hourOptions" :key="`e-${hour}`" :label="`${hour} 点`" :value="hour" />
            </el-select>
          </div>
          <div v-if="windowInvalid" class="random-field-hint is-error">
            结束小时不能早于起始小时。跨零点的时间窗请分两次生成（例如先 22-23 点，再 0-5 点）。
          </div>
        </div>
      </el-form-item>

      <el-form-item label="随机到秒">
        <div class="random-field-block">
          <el-switch v-model="randomizeSecond" />
          <div class="random-field-hint">
            关掉后秒固定为 0，生成的时间点只精确到分钟。
          </div>
        </div>
      </el-form-item>

      <el-form-item label="生成形态">
        <div class="random-field-block">
          <el-radio-group v-model="mode">
            <el-radio value="split">拆成 N 条</el-radio>
            <el-radio value="merged">合并一条</el-radio>
          </el-radio-group>
          <div class="random-field-hint">
            拆成 N 条时每个时间点的分秒各自随机，打散得更彻底；合并一条形如
            <code>10 50 9,21 * * *</code>，N 个小时共用同一个分秒。
          </div>
        </div>
      </el-form-item>

      <el-form-item label="预览">
        <div class="random-preview">
          <div v-if="previews.length === 0" class="random-preview-empty">
            当前参数生成不出规则，请先修正时间窗。
          </div>
          <div v-for="(expression, index) in previews" :key="`${index}-${expression}`" class="random-preview-item">
            <code class="preview-expr">{{ expression }}</code>
            <span class="preview-desc">{{ describeRandomCron(expression) }}</span>
          </div>
        </div>
      </el-form-item>
    </el-form>

    <template #footer>
      <div class="random-footer">
        <el-button :disabled="windowInvalid" @click="regenerate">重新随机</el-button>
        <div class="random-footer-right">
          <el-button @click="emit('update:visible', false)">取消</el-button>
          <el-button type="primary" :disabled="previews.length === 0" @click="handleApply">应用</el-button>
        </div>
      </div>
    </template>
  </el-dialog>
</template>

<style scoped lang="scss">
.random-field-block {
  display: flex;
  flex-direction: column;
  gap: 8px;
  width: 100%;
}

.random-inline-input {
  display: flex;
  align-items: center;
  gap: 8px;

  // 必须用 :deep 且锚点选自己模板里的 div：el-select 内部是 el-tooltip 包装的结构，
  // 直接给它挂 class 再在 scoped 里写规则会静默失效（见 spec/frontend/design-system.md §5）。
  :deep(.el-select) {
    width: 110px;
  }
}

.random-field-hint {
  font-size: 12px;
  color: var(--dd-text-2);
  line-height: 1.6;

  code {
    padding: 0 4px;
    background: var(--el-fill-color-light);
    font-family: var(--dd-font-mono);
    font-size: 11px;
  }

  &.is-warning {
    color: var(--dd-warn-ink);
  }

  &.is-error {
    color: var(--dd-err-ink);
  }
}

.random-preview {
  display: flex;
  flex-direction: column;
  gap: 6px;
  width: 100%;
}

.random-preview-empty {
  font-size: 12px;
  color: var(--dd-text-2);
}

.random-preview-item {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
  padding: 6px 10px;
  border: 1px solid var(--dd-border);
  // 预览列表里的一行卡片 → surface 档
  border-radius: var(--dd-radius-surface);
  background: var(--el-fill-color-lighter);
}

.preview-expr {
  font-family: var(--dd-font-mono);
  font-size: 12px;
  color: var(--dd-text);
}

.preview-desc {
  font-size: 12px;
  color: var(--dd-text-2);
}

.random-footer {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  flex-wrap: wrap;
}

.random-footer-right {
  display: flex;
  align-items: center;
  gap: 10px;

  // 间距统一交给 flex gap，清掉 EP 默认的相邻按钮 12px 左外边距
  :deep(.el-button + .el-button) {
    margin-left: 0;
  }
}

@media (max-width: 768px) {
  .random-inline-input {
    flex-wrap: wrap;

    :deep(.el-select) {
      width: 100%;
    }
  }
}
</style>
