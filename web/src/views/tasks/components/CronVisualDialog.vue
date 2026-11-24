<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { useResponsive } from '@/composables/useResponsive'
import {
  WEEKDAY_LABELS,
  buildCronExpression,
  createDefaultCronVisualModel,
  describeCronVisual,
  hasEmptyWeekSelection,
  nextFireHint,
  parseCronExpression
} from '@/utils/cronVisual'

const props = defineProps<{
  visible: boolean
  /** 当前正在编辑的那条规则，打开时现场反解成界面状态 */
  expression: string
}>()

const emit = defineEmits<{
  'update:visible': [value: boolean]
  // 只回传一条表达式，由 CronInput / StopScheduleInput 决定写进哪条规则
  'apply': [expression: string]
}>()

const { dialogFullscreen } = useResponsive()

const model = reactive(createDefaultCronVisualModel())
/** 反解失败：界面按默认值开局，必须显式告诉用户「点应用会覆盖原表达式」 */
const unparsed = ref(false)

const hourOptions = Array.from({ length: 24 }, (_, hour) => hour)
const minuteOptions = Array.from({ length: 60 }, (_, minute) => minute)
const monthDayOptions = Array.from({ length: 31 }, (_, index) => index + 1)
const weekdayOptions = Array.from({ length: 7 }, (_, day) => day)

/**
 * 把「工作日 / 周末 / 每天」这三个快捷档的语义回填进 weekdays，好让「自定义」接手时
 * 看到的就是刚才那个选择：先点「周末」再点「自定义」，勾选框必须是周六周日，
 * 而不是默认的周一~周五。
 *
 * ⚠️ 只动 weekdays 这一个字段（它**只在 custom 档参与生成**），表达式输出一个字都不变：
 * 工作日仍输出 `1-5`、周末仍输出 `0,6`，与后端出厂预设逐字节一致，反解才能命中快捷项。
 * 「每天」回填成七天全勾而不是原样保留：七天全勾生成出来同样是 `*`，
 * 用户切到「自定义」不会发现规则被悄悄改成了 `1-5`。
 * custom 档不碰：那里的勾选就是用户自己的输入。
 */
function syncWeekdaysFromMode(mode: typeof model.weekMode) {
  if (mode === 'workday') {
    model.weekdays = [1, 2, 3, 4, 5]
  } else if (mode === 'weekend') {
    model.weekdays = [0, 6]
  } else if (mode === 'all') {
    model.weekdays = [0, 1, 2, 3, 4, 5, 6]
  }
}

/**
 * 每次打开都重新反解一遍。
 * 刻意不把可视化状态挂到规则行上：规则行的唯一事实来源始终是那串表达式，
 * 用户手改过表达式再打开弹窗，看到的也必须是手改后的状态。
 */
function resetFromExpression(expression: string) {
  const trimmed = (expression || '').trim()
  const parsed = trimmed ? parseCronExpression(trimmed) : null
  unparsed.value = Boolean(trimmed) && parsed === null
  Object.assign(model, parsed || createDefaultCronVisualModel())
  // 反解出来的 weekMode 可能与上次相同（连开两次 `0 0 9 * * *` 都是「每天」），
  // 那样下面那个 watch 不会触发，weekdays 会停在上一轮的值上，所以这里补一次
  syncWeekdaysFromMode(model.weekMode)
}

watch(
  () => props.visible,
  (value) => {
    if (value) resetFromExpression(props.expression)
  },
  { immediate: true }
)

// 「每周」选「每天」等于没选星期，默认落到工作日；其余频率不动用户已有的选择
watch(
  () => model.frequency,
  (frequency) => {
    if (frequency === 'week' && model.weekMode === 'all') {
      model.weekMode = 'workday'
    }
  }
)

// 快捷档切换时回填勾选状态，理由见 syncWeekdaysFromMode
watch(() => model.weekMode, syncWeekdaysFromMode)

// 「每分钟」「每小时」两档本来就不限定小时，不渲染时段控件
const showHourField = computed(() => model.frequency !== 'minute' && model.frequency !== 'hour')
const showWeekField = computed(() => model.frequency === 'week' || model.frequency === 'interval')

/**
 * 跨零点（结束小时早于起始小时）单条 cron 表达不了。
 * 照 RandomCronDialog 的既有做法显式拦住并提示拆成两条，不偷偷展开成两条规则。
 */
const rangeInvalid = computed(() =>
  model.frequency === 'interval' && model.hourMode === 'range' && model.hourEnd < model.hourStart
)

/**
 * 星期选了「自定义」却一天都没勾。
 * 表达式本身是合法的（星期段退回 `*`，生成逻辑不动），但界面上一天没勾、结果却是「每天」，
 * 与用户预期正好相反，所以在这里拦住：预览换成提示文案，「应用」一并禁掉。
 */
const weekSelectionEmpty = computed(() => hasEmptyWeekSelection(model))

const previewExpression = computed(() => buildCronExpression(model))
const previewText = computed(() => describeCronVisual(model))
const closedRangeHint = computed(() => nextFireHint(model))
/** 「应用」的唯一禁用判据，模板与 handleApply 共用，免得两处条件漂开 */
const applyDisabled = computed(() => !previewExpression.value || weekSelectionEmpty.value)

function handleApply() {
  if (applyDisabled.value) {
    return
  }
  emit('apply', previewExpression.value)
  emit('update:visible', false)
}
</script>

<template>
  <el-dialog
    :model-value="visible"
    title="可视化编辑定时规则"
    width="640px"
    :fullscreen="dialogFullscreen"
    :lock-scroll="false"
    :close-on-click-modal="false"
    @update:model-value="emit('update:visible', $event)"
  >
    <div v-if="unparsed" class="visual-warning">
      当前表达式用到了可视化编辑器表达不了的写法（如 <code>L</code>/<code>W</code>/<code>#</code>、英文月份星期、
      <code>9-11,21</code> 这类混合形态、<code>9-22/2</code> 这类带步进的区间，或秒位不为 0）。
      下面按默认值开局，<strong>点「应用」会覆盖原表达式</strong>；想保留原样请直接关掉本窗口。
    </div>

    <el-form :label-width="dialogFullscreen ? 'auto' : '92px'" :label-position="dialogFullscreen ? 'top' : 'right'">
      <el-form-item label="频率">
        <div class="visual-field-block">
          <el-radio-group v-model="model.frequency">
            <el-radio value="minute">每分钟</el-radio>
            <el-radio value="hour">每小时</el-radio>
            <el-radio value="day">每天</el-radio>
            <el-radio value="week">每周</el-radio>
            <el-radio value="month">每月</el-radio>
            <el-radio value="interval">按时段间隔</el-radio>
          </el-radio-group>
          <div class="visual-field-hint">
            「早上 9 点到晚上 10 点、每 10 分钟一次、只在工作日」这类规则选<strong>按时段间隔</strong>，
            分钟步进、时段区间、星期三组条件可以叠加。
          </div>
        </div>
      </el-form-item>

      <el-form-item label="分钟">
        <div class="visual-field-block">
          <!-- 只有「按时段间隔」才让用户自己挑分钟形态，其余频率的形态由频率本身决定 -->
          <el-radio-group v-if="model.frequency === 'interval'" v-model="model.minuteMode">
            <el-radio value="step">每 N 分钟</el-radio>
            <el-radio value="fixed">固定分钟</el-radio>
            <el-radio value="list">指定多个分钟</el-radio>
          </el-radio-group>

          <div
            v-if="model.frequency === 'minute' || (model.frequency === 'interval' && model.minuteMode === 'step')"
            class="visual-inline-input"
          >
            <span>每</span>
            <el-input-number v-model="model.minuteStep" :min="1" :max="59" />
            <span>分钟执行一次</span>
          </div>

          <div
            v-else-if="model.frequency !== 'interval' || model.minuteMode === 'fixed'"
            class="visual-inline-input"
          >
            <span>第</span>
            <el-select v-model="model.minute">
              <el-option v-for="minute in minuteOptions" :key="`m-${minute}`" :label="`${minute} 分`" :value="minute" />
            </el-select>
            <span>分钟执行</span>
          </div>

          <div v-else class="visual-inline-input is-block">
            <el-select v-model="model.minutes" multiple collapse-tags collapse-tags-tooltip placeholder="选择分钟">
              <el-option v-for="minute in minuteOptions" :key="`ml-${minute}`" :label="`${minute} 分`" :value="minute" />
            </el-select>
          </div>
        </div>
      </el-form-item>

      <el-form-item v-if="showHourField" label="时段">
        <div class="visual-field-block">
          <el-radio-group v-if="model.frequency === 'interval'" v-model="model.hourMode">
            <el-radio value="all">全天</el-radio>
            <el-radio value="step">每 N 小时</el-radio>
            <el-radio value="range">时段区间</el-radio>
            <el-radio value="list">指定小时</el-radio>
            <el-radio value="fixed">固定单点</el-radio>
          </el-radio-group>

          <template v-if="model.frequency === 'interval'">
            <!-- 出厂预设「每2小时」「每6小时」就是这一档（0 0 */2 * * *），没有它这些预设点开就报「无法可视化」 -->
            <div v-if="model.hourMode === 'step'" class="visual-inline-input">
              <span>每</span>
              <el-input-number v-model="model.hourStep" :min="1" :max="23" />
              <span>小时执行一次</span>
            </div>
            <div v-else-if="model.hourMode === 'range'" class="visual-inline-input">
              <!-- 宽度靠外层 :deep(.el-select) 控制：给 el-select 直接挂 class 在 scoped 里命中不到它的根元素 -->
              <el-select v-model="model.hourStart">
                <el-option v-for="hour in hourOptions" :key="`hs-${hour}`" :label="`${hour} 点`" :value="hour" />
              </el-select>
              <span>至</span>
              <el-select v-model="model.hourEnd">
                <el-option v-for="hour in hourOptions" :key="`he-${hour}`" :label="`${hour} 点`" :value="hour" />
              </el-select>
            </div>
            <div v-else-if="model.hourMode === 'list'" class="visual-inline-input is-block">
              <el-select v-model="model.hours" multiple collapse-tags collapse-tags-tooltip placeholder="选择小时">
                <el-option v-for="hour in hourOptions" :key="`hl-${hour}`" :label="`${hour} 点`" :value="hour" />
              </el-select>
            </div>
            <div v-else-if="model.hourMode === 'fixed'" class="visual-inline-input">
              <el-select v-model="model.hour">
                <el-option v-for="hour in hourOptions" :key="`hf-${hour}`" :label="`${hour} 点`" :value="hour" />
              </el-select>
            </div>
          </template>
          <div v-else class="visual-inline-input">
            <el-select v-model="model.hour">
              <el-option v-for="hour in hourOptions" :key="`hd-${hour}`" :label="`${hour} 点`" :value="hour" />
            </el-select>
          </div>

          <div v-if="rangeInvalid" class="visual-field-hint is-error">
            结束小时不能早于起始小时。跨零点的时段（如 22 点到次日 6 点）一条 cron 表达不了，
            请拆成两条规则（先 22-23 点，再 0-6 点）。
          </div>
          <div v-else-if="closedRangeHint" class="visual-field-hint is-warning">
            {{ closedRangeHint }}
          </div>
        </div>
      </el-form-item>

      <el-form-item v-if="model.frequency === 'month'" label="每月几号">
        <div class="visual-field-block">
          <div class="visual-inline-input is-block">
            <el-select v-model="model.monthDays" multiple collapse-tags collapse-tags-tooltip placeholder="选择日期">
              <el-option v-for="day in monthDayOptions" :key="`d-${day}`" :label="`${day} 号`" :value="day" />
            </el-select>
          </div>
          <div class="visual-field-hint">
            cron 里「几号」和「星期几」同时限定时取的是<strong>并集</strong>而不是交集，
            所以选了「每月」就不再提供星期条件，避免配出与预期完全不同的调度。
          </div>
        </div>
      </el-form-item>

      <el-form-item v-if="showWeekField" label="星期">
        <div class="visual-field-block">
          <el-radio-group v-model="model.weekMode">
            <!-- 「每周」这一档选「每天」等于没限定星期，与「每天」频率重复，所以只在按时段间隔里给 -->
            <el-radio v-if="model.frequency === 'interval'" value="all">每天</el-radio>
            <el-radio value="workday">工作日</el-radio>
            <el-radio value="weekend">周末</el-radio>
            <el-radio value="custom">自定义</el-radio>
          </el-radio-group>
          <el-checkbox-group v-if="model.weekMode === 'custom'" v-model="model.weekdays">
            <el-checkbox v-for="day in weekdayOptions" :key="`w-${day}`" :value="day">
              {{ WEEKDAY_LABELS[day] }}
            </el-checkbox>
          </el-checkbox-group>
          <!-- 一天没勾时星期段会退回 `*`（等于每天），与用户预期相反，所以明确要求先勾一天 -->
          <div v-if="weekSelectionEmpty" class="visual-field-hint is-error">
            请至少选择一天。一天都不勾等于不限定星期（每天都执行），多半不是你想要的。
          </div>
        </div>
      </el-form-item>

      <el-form-item label="包含秒段">
        <div class="visual-field-block">
          <el-switch v-model="model.withSeconds" />
          <div class="visual-field-hint">
            开启生成 6 段 <code>秒 分 时 日 月 周</code>（秒固定为 0），与出厂预设、随机生成器一致；
            关掉生成 5 段 <code>分 时 日 月 周</code>。两者调度效果相同。
          </div>
        </div>
      </el-form-item>

      <el-form-item label="预览">
        <div class="visual-preview">
          <div v-if="!previewExpression" class="visual-preview-empty">
            当前参数生成不出规则，请先修正时段。
          </div>
          <!-- 表达式拼得出来（星期段退回 `*`），但界面上一天没勾，先让用户补一天再谈预览 -->
          <div v-else-if="weekSelectionEmpty" class="visual-preview-empty">
            {{ previewText }}
          </div>
          <template v-else>
            <code class="preview-expr">{{ previewExpression }}</code>
            <span class="preview-desc">{{ previewText }}</span>
          </template>
        </div>
      </el-form-item>
    </el-form>

    <template #footer>
      <div class="visual-footer">
        <el-button @click="emit('update:visible', false)">取消</el-button>
        <el-button type="primary" :disabled="applyDisabled" @click="handleApply">应用</el-button>
      </div>
    </template>
  </el-dialog>
</template>

<style scoped lang="scss">
.visual-warning {
  margin-bottom: 14px;
  padding: 8px 12px;
  border: 1px solid color-mix(in srgb, var(--dd-warn) 30%, var(--dd-surface-solid));
  background: color-mix(in srgb, var(--dd-warn) 10%, var(--dd-surface-solid));
  // 弹窗顶部的整块提示条 → surface 档
  border-radius: var(--dd-radius-surface);
  color: var(--dd-warn-ink);
  font-size: 12px;
  line-height: 1.7;

  code {
    padding: 0 4px;
    background: var(--el-fill-color-light);
    font-family: var(--dd-font-mono);
    font-size: 11px;
  }
}

.visual-field-block {
  display: flex;
  flex-direction: column;
  gap: 8px;
  width: 100%;
}

.visual-inline-input {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 13px;
  color: var(--el-text-color-regular);

  // 必须用 :deep 且锚点选自己模板里的 div：el-select 内部是 el-tooltip 包装的结构，
  // 直接给它挂 class 再在 scoped 里写规则会静默失效（见 spec/frontend/design-system.md §5）。
  :deep(.el-select) {
    width: 110px;
  }

  // 多选那几个（分钟/小时/日期）标签多，占满整行才够看
  &.is-block :deep(.el-select) {
    width: 100%;
  }
}

.visual-field-hint {
  font-size: 12px;
  color: var(--dd-text-2);
  line-height: 1.7;

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

.visual-preview {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
  width: 100%;
  padding: 8px 10px;
  border: 1px solid var(--dd-border);
  // 预览卡片 → surface 档
  border-radius: var(--dd-radius-surface);
  background: var(--el-fill-color-lighter);
}

.visual-preview-empty {
  font-size: 12px;
  color: var(--dd-text-2);
}

.preview-expr {
  font-family: var(--dd-font-mono);
  font-size: 13px;
  color: var(--dd-accent-ink);
  word-break: break-all;
}

.preview-desc {
  font-size: 12px;
  color: var(--dd-text-2);
}

.visual-footer {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: 10px;
  flex-wrap: wrap;
}

@media (max-width: 768px) {
  .visual-inline-input {
    flex-wrap: wrap;

    :deep(.el-select) {
      width: 100%;
    }
  }
}
</style>
