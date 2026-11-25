<template>
  <div class="dd-date-range" :class="{ 'is-inline': inline }">
    <!-- 快捷范围做成分段按钮放在选择器旁边，而不是用 EP 的 shortcuts 侧栏。
         两个理由：分段控件是本项目已有的语汇（.dd-seg-group）；
         EP 的 shortcuts 挂在面板左侧，移动端面板本来就窄，再切一条侧栏会挤到没法用。 -->
    <div v-if="shortcuts.length > 0" class="dd-seg-group dd-date-range__shortcuts">
      <button
        v-for="item in shortcuts"
        :key="item.key"
        type="button"
        class="dd-seg-btn"
        :class="{ 'is-active': activeShortcut === item.key }"
        @click="applyShortcut(item)"
      >
        {{ item.label }}
      </button>
    </div>

    <el-date-picker
      :model-value="modelValue"
      type="daterange"
      range-separator="→"
      :start-placeholder="startPlaceholder"
      :size="size"
      :end-placeholder="endPlaceholder"
      :disabled="disabled"
      :clearable="clearable"
      :disabled-date="disabledDate"
      :editable="false"
      unlink-panels
      popper-class="dd-date-range__popper"
      class="dd-date-range__control"
      @update:model-value="onUpdate"
    />

    <!-- 汇总行。选完之后用一句话把「到底选了多长」说明白，
         省得用户对着两个日期自己数天数——参考图里的「共 3 晚」就是这个作用。 -->
    <div v-if="summary" class="dd-date-range__summary">{{ summary }}</div>
  </div>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import {
  DATE_RANGE_SHORTCUTS,
  countDaysInRange,
  formatDate,
  startOfDay,
  type DateRangeShortcut,
} from '@/utils/datetime'

type RangeValue = [Date, Date] | null

const props = withDefaults(
  defineProps<{
    modelValue: RangeValue
    /** 传空数组可关掉快捷范围 */
    shortcuts?: DateRangeShortcut[]
    startPlaceholder?: string
    endPlaceholder?: string
    disabled?: boolean
    clearable?: boolean
    size?: 'large' | 'default' | 'small'
    /**
     * 禁止选择未来日期。
     * 日志、调用记录这类「已经发生过的事」应当开启——选到明天只会得到空结果，
     * 用户会以为是筛选坏了。
     */
    disableFuture?: boolean
    /**
     * 可回溯的最大天数。日志有保留期（log_retention_days，默认 7 天），
     * 让用户选到 90 天前再告诉他没数据，是把清理策略的后果扔给用户去猜。
     */
    maxPastDays?: number
    /** 覆盖上面两条的自定义禁用规则 */
    customDisabledDate?: (date: Date) => boolean
    /** 汇总文案的单位，默认「天」 */
    unit?: string
    /**
     * 横排模式：快捷项、选择器、汇总在同一行。
     * 放进列表页 `.toolbar` 时必须开启——默认的竖排会把整条工具栏撑成两行高。
     */
    inline?: boolean
  }>(),
  {
    shortcuts: () => DATE_RANGE_SHORTCUTS,
    startPlaceholder: '开始日期',
    endPlaceholder: '结束日期',
    disabled: false,
    clearable: true,
    size: 'default',
    disableFuture: true,
    unit: '天',
    inline: false,
  }
)

const emit = defineEmits<{
  (e: 'update:modelValue', value: RangeValue): void
  (e: 'change', value: RangeValue): void
}>()

/** 当前命中的快捷项。用户手动改了日期后要取消高亮，否则分段按钮会说谎 */
const activeShortcut = ref<string>('')

const summary = computed(() => {
  const range = props.modelValue
  if (!range || !range[0] || !range[1]) return ''
  const days = countDaysInRange(range)
  if (days <= 0) return ''
  return `${formatDate(range[0])} 至 ${formatDate(range[1])} · 共 ${days} ${props.unit}`
})

function disabledDate(date: Date): boolean {
  if (props.customDisabledDate) return props.customDisabledDate(date)

  if (props.disableFuture) {
    // 与「今天」比的是自然日起点，不是此刻：否则今天早些时候的日期会被判成过去而禁掉
    const today = startOfDay(new Date())
    if (startOfDay(date).getTime() > today.getTime()) return true
  }

  if (props.maxPastDays && props.maxPastDays > 0) {
    const earliest = startOfDay(new Date(Date.now() - (props.maxPastDays - 1) * 86_400_000))
    if (startOfDay(date).getTime() < earliest.getTime()) return true
  }

  return false
}

function onUpdate(value: unknown) {
  const range = (value as RangeValue) ?? null
  // 手动改日期就不再属于任何快捷项
  activeShortcut.value = ''
  emit('update:modelValue', range)
  emit('change', range)
}

function applyShortcut(item: DateRangeShortcut) {
  // 再点一次已选中的快捷项 = 取消筛选，与列表页状态标签的行为一致
  if (activeShortcut.value === item.key) {
    activeShortcut.value = ''
    emit('update:modelValue', null)
    emit('change', null)
    return
  }
  const range = item.value()
  activeShortcut.value = item.key
  emit('update:modelValue', range)
  emit('change', range)
}

// 外部清空（比如工具栏的「重置筛选」）时要把快捷项高亮一起摘掉
watch(
  () => props.modelValue,
  (value) => {
    if (!value) activeShortcut.value = ''
  }
)
</script>

<style scoped lang="scss">
.dd-date-range {
  display: inline-flex;
  flex-direction: column;
  gap: 6px;
  max-width: 100%;
  min-width: 0;
}

// 横排：给列表页工具栏用。竖排会把整条 toolbar 撑成两行高。
.dd-date-range.is-inline {
  flex-direction: row;
  align-items: center;
  gap: 8px;
  // 汇总文字长度随选中范围变化，放不下时整体换行而不是把工具栏撑破
  flex-wrap: wrap;
}

.dd-date-range__shortcuts {
  align-self: flex-start;
  // 快捷分段保持单行不折行，窄屏或紧凑容器里平滑横向滚动
  max-width: 100%;
  min-width: 0;
  flex-wrap: nowrap;
  overflow-x: auto;
  overflow-y: hidden;
  overscroll-behavior-x: auto;
  scrollbar-width: none;

  &::-webkit-scrollbar {
    display: none;
  }

  .dd-seg-btn {
    flex-shrink: 0;
    white-space: nowrap;
  }
}

.dd-date-range.is-inline .dd-date-range__shortcuts {
  align-self: center;
}

.dd-date-range__summary {
  font-size: 12px;
  line-height: 1;
  color: var(--el-text-color-secondary);
  font-variant-numeric: tabular-nums;
}

@media screen and (max-width: 768px) {
  .dd-date-range {
    width: 100%;
  }

  .dd-date-range__shortcuts {
    // 窄屏放不下四个快捷项时允许横向滚动，而不是换行把工具栏顶高
    align-self: stretch;
    width: 100%;
    overflow-x: auto;
  }

  // ⚠️ 必须用 :deep()，不能直接写 `.dd-date-range__control { ... }`。
  //
  // 那个 class 是加在 <el-date-picker> 上的，而 el-date-picker 内部是
  // <el-tooltip> 包装的多根结构 —— Vue 只会把父组件的 scopeId 落到【单根】子组件的
  // 根 DOM 上，多根的落不上。实测确认：该元素的属性只有 class 和 tabindex，
  // 没有任何 data-v-*。于是 scoped 编译出的 `.dd-date-range__control[data-v-xxx]`
  // 永远命中不了，规则静默失效。
  //
  // 这个坑很隐蔽：横排模式看着是正常的，但那是 flex-shrink 把它压回去的，
  // 跟这条规则无关；竖排模式主轴是垂直方向、水平不 shrink，EP 写死的宽度就露出来了。
  .dd-date-range :deep(.el-date-editor) {
    // EP 给范围选择器写死了宽度：
    //   .el-date-editor--daterange { width: var(--el-date-editor-daterange-width) }  // 350px
    // 390px 视口（iPhone 常见宽度）下容器可用宽只有约 318px，直接顶破，页面横向滚。
    // max-width 在层叠里优先于 width（先算 width 再被 max-width 钳制），
    // 是唯一能稳定压住写死宽度的写法。min-width: 0 是 flex item 的常规解封。
    width: 100%;
    max-width: 100%;
    min-width: 0;
  }
}
</style>
