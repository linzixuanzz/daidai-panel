<template>
  <div class="dd-multi-select">
    <el-select
      :model-value="modelValue"
      multiple
      :filterable="filterable"
      :clearable="clearable"
      :placeholder="placeholder"
      :disabled="disabled"
      :size="size"
      :collapse-tags="collapseTags"
      :collapse-tags-tooltip="collapseTags"
      :max-collapse-tags="maxCollapseTags"
      :multiple-limit="max ?? 0"
      class="dd-multi-select__control"
      @update:model-value="onUpdate"
    >
      <!-- 允许调用方自己渲染选项（带图标、分组、自定义内容），
           不传 slot 时按 options 渲染，覆盖绝大多数场景。 -->
      <slot>
        <el-option
          v-for="option in options"
          :key="String(option.value)"
          :label="option.label"
          :value="option.value"
          :disabled="option.disabled || isOverLimit(option.value)"
        />
      </slot>
    </el-select>

    <!-- 计数行。EP 的 multiple 自带标签化、单独删除和一键清空，唯独没有
         「已选几项 / 还能选几项」——而这正是多选里用户最需要知道的一件事，
         尤其在 collapse-tags 折叠之后，屏幕上只剩「+3」时更是无从判断。 -->
    <div v-if="showFooter" class="dd-multi-select__footer">
      <span class="dd-multi-select__count">
        已选 {{ selectedCount }} 项<template v-if="max"> · 最多 {{ max }} 项</template>
      </span>
      <button
        v-if="clearable && selectedCount > 0 && !disabled"
        type="button"
        class="dd-multi-select__clear"
        @click="clearAll"
      >
        清空
      </button>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'

type OptionValue = string | number

export interface MultiSelectOption {
  value: OptionValue
  label: string
  disabled?: boolean
}

const props = withDefaults(
  defineProps<{
    modelValue: OptionValue[]
    options?: MultiSelectOption[]
    placeholder?: string
    /** 最多可选几项。不传表示不限。达到上限后未选中的项会置灰而不是静默失败 */
    max?: number
    filterable?: boolean
    clearable?: boolean
    disabled?: boolean
    size?: 'large' | 'default' | 'small'
    /** 标签多时折叠成「+N」，并挂 tooltip 显示全部 */
    collapseTags?: boolean
    maxCollapseTags?: number
    /** 关掉底部计数行（选项极少、或外部已有计数时） */
    hideFooter?: boolean
  }>(),
  {
    options: () => [],
    placeholder: '请选择',
    filterable: false,
    clearable: true,
    disabled: false,
    size: 'default',
    collapseTags: false,
    maxCollapseTags: 3,
    hideFooter: false,
  }
)

const emit = defineEmits<{
  (e: 'update:modelValue', value: OptionValue[]): void
  (e: 'change', value: OptionValue[]): void
}>()

const selectedCount = computed(() => props.modelValue?.length ?? 0)

const showFooter = computed(() => !props.hideFooter)

/**
 * 到达上限后，把【尚未选中】的选项置灰。
 *
 * EP 的 multiple-limit 到达上限时是直接忽略点击——用户会以为是卡了。
 * 置灰能把「不能再选了」这件事提前告诉他，而已选中的项必须保持可点，
 * 否则就取消不掉了，会把用户锁死在上限状态。
 */
function isOverLimit(value: OptionValue) {
  if (!props.max) return false
  if (selectedCount.value < props.max) return false
  return !props.modelValue.includes(value)
}

function onUpdate(value: OptionValue[]) {
  emit('update:modelValue', value)
  emit('change', value)
}

function clearAll() {
  onUpdate([])
}
</script>

<style scoped lang="scss">
.dd-multi-select {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

// ⚠️ 用 :deep()，不能直接写 `.dd-multi-select__control { ... }`。
// 那个 class 加在 <el-select> 上，而 el-select 内部是 el-tooltip 包装的多根结构，
// 拿不到父组件的 scopeId（实测过同款结构的 el-date-picker，根元素上只有 class 和 tabindex，
// 没有任何 data-v-*）。scoped 编译出的 `.dd-multi-select__control[data-v-xxx]` 命中不了。
.dd-multi-select :deep(.el-select) {
  width: 100%;
  min-width: 0;
}

.dd-multi-select__footer {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  font-size: 12px;
  line-height: 1;
  color: var(--el-text-color-secondary);
}

.dd-multi-select__count {
  // 计数在增减时会变宽，等宽数字避免右侧「清空」跟着左右跳
  font-variant-numeric: tabular-nums;
}

.dd-multi-select__clear {
  padding: 0;
  border: none;
  // 纯文字按钮本身没有底色，圆角只影响 :focus-visible 的 outline 形状，跟着控件档走
  border-radius: var(--dd-radius-control);
  background: transparent;
  color: var(--el-text-color-secondary);
  font-size: 12px;
  line-height: 1;
  cursor: pointer;
  // 扁平规则：hover 只换颜色
  transition: color var(--dd-motion-fast) var(--dd-ease-standard);

  &:hover {
    color: var(--dd-err-ink);
  }

  &:focus-visible {
    outline: 1px solid var(--el-color-primary);
    outline-offset: 2px;
  }
}
</style>
