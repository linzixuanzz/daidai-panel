<template>
  <Transition name="dd-badge-pop">
    <span
      v-if="visible"
      class="dd-badge"
      :class="[`dd-badge--${level}`, { 'dd-badge--dot': dot, 'dd-badge--standalone': standalone }]"
      :title="title"
      aria-live="polite"
      :aria-label="ariaLabel"
    >
      <!-- 数字用 out-in 翻牌：新值从上方滑入、旧值向下滑出，像计数器跳字。
           key 绑在 displayText 上，只有文本真变了才触发，同值重渲染不会闪。 -->
      <Transition v-if="!dot" name="dd-badge-roll" mode="out-in">
        <span :key="displayText" class="dd-badge__num">{{ displayText }}</span>
      </Transition>
    </span>
  </Transition>
</template>

<script setup lang="ts">
import { computed } from 'vue'

type BadgeLevel = 'danger' | 'warning' | 'primary' | 'info'

const props = withDefaults(
  defineProps<{
    /** 角标数字。为 0 且非 dot 模式时整个角标不渲染 */
    value?: number
    /** 只显示一个实心小方块，不显示数字。用于「有事在发生但数量不重要」的场景 */
    dot?: boolean
    /** 语义级别：danger 需要用户处理，primary/info 只是告知，warning 介于两者之间 */
    level?: BadgeLevel
    /** 超过这个数显示成 `max+`，避免三位数把菜单项撑开 */
    max?: number
    /** 悬浮说明文字。侧栏角标只有一个数字，没有 title 用户不知道它在数什么 */
    title?: string
    /**
     * 独立模式：不做绝对定位，按普通行内元素排版。
     * 侧栏折叠时角标要贴在图标右上角（绝对定位，由宿主给 position:relative），
     * 展开时则是跟在文字后面的一枚标记（独立模式）。
     */
    standalone?: boolean
    /**
     * 值为 0 时也渲染。
     *
     * 默认不渲染，因为「提醒型」角标（有 3 个失败任务）在没事时就该消失。
     * 但「统计型」角标不一样——状态标签页上的「失败 0」是有信息量的，
     * 它告诉用户「筛过了，确实一条都没有」，直接消失反而像是没加载出来。
     */
    showZero?: boolean
  }>(),
  {
    value: 0,
    dot: false,
    level: 'danger',
    max: 99,
    title: '',
    standalone: true,
    showZero: false,
  }
)

const visible = computed(() => props.dot || props.value > 0 || props.showZero)

const displayText = computed(() => {
  if (props.value > props.max) return `${props.max}+`
  return String(props.value)
})

const ariaLabel = computed(() => {
  if (props.title) {
    return props.dot ? props.title : `${props.title}：${displayText.value}`
  }
  return props.dot ? '有新动态' : displayText.value
})
</script>

<style scoped lang="scss">
/* 计数角标吃 pill 档令牌：直角模式下仍是一枚方块（与 v3.0.0 起的扁平基调一致），
   圆角模式下 999px 会把这枚 18px 高的角标压成胶囊。
   两种模式都不用阴影，层次只靠实心底色与页面反差表达。 */
.dd-badge {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  box-sizing: border-box;
  min-width: 18px;
  height: 18px;
  padding: 0 5px;
  border-radius: var(--dd-radius-pill);
  font-size: 11px;
  font-weight: 600;
  line-height: 1;
  font-variant-numeric: tabular-nums; // 等宽数字，跳字时宽度不抖
  // 实心危险底 + 状态文字色令牌（各主题都 ≥ 4.5:1）
  color: var(--dd-on-status);
  background-color: var(--dd-err-solid);
  // 数字翻牌时超出的那半行必须裁掉，否则会露在角标外面
  overflow: hidden;
}

.dd-badge--dot {
  min-width: 8px;
  width: 8px;
  height: 8px;
  padding: 0;
  // 圆形承载语义：dot 模式是「有事在发生」的状态指示点，圆点是通用可供性。
  // 两种圆角模式下都保持圆形，覆盖上面 .dd-badge 继承来的 pill 档令牌。
  border-radius: 50%;
}

.dd-badge--danger {
  background-color: var(--el-color-danger);
}

.dd-badge--warning {
  background-color: var(--el-color-warning);
}

.dd-badge--primary {
  background-color: var(--el-color-primary);
}

/* info 级用描边而非实心：它只是「有这么多东西」的中性计数，
   实心色会让它和真正需要处理的 danger 抢注意力。 */
.dd-badge--info {
  color: var(--el-text-color-regular);
  background-color: var(--el-fill-color);
  border: 1px solid var(--el-border-color-lighter);
}

.dd-badge__num {
  display: block;
}

/* ===== 出现 / 消失 =====
   走「入场关键帧」那条允许项：只用 opacity + translateY，不做 scale。
   时长吃令牌，prefers-reduced-motion 下自动被压到 1ms。 */
.dd-badge-pop-enter-active {
  transition:
    opacity var(--dd-motion-fast) var(--dd-ease-decelerate),
    transform var(--dd-motion-fast) var(--dd-ease-decelerate);
}

.dd-badge-pop-leave-active {
  transition:
    opacity var(--dd-motion-fast) var(--dd-ease-standard),
    transform var(--dd-motion-fast) var(--dd-ease-standard);
}

.dd-badge-pop-enter-from,
.dd-badge-pop-leave-to {
  opacity: 0;
  transform: translateY(4px);
}

/* ===== 数字翻牌 =====
   新值从上方落下、旧值向下退出，方向一致才像「计数器往上跳」。 */
.dd-badge-roll-enter-active,
.dd-badge-roll-leave-active {
  transition:
    opacity var(--dd-motion-fast) var(--dd-ease-decelerate),
    transform var(--dd-motion-fast) var(--dd-ease-decelerate);
}

.dd-badge-roll-enter-from {
  opacity: 0;
  transform: translateY(-100%);
}

.dd-badge-roll-leave-to {
  opacity: 0;
  transform: translateY(100%);
}
</style>
