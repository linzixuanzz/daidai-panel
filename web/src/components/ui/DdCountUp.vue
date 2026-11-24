<script setup lang="ts">
/**
 * 数字滚动（design §2.2）。rich 档从旧值滚到新值（首次挂载从 0 开始），subtle / off 档直接显示。
 * 缓动是 easeOutExpo：前段快、尾段慢，最后几位数字会「落定」。
 *
 * props：
 *   value     目标数值（非有限数显示 '-'）
 *   decimals  小数位，默认 0
 *   suffix    后缀，例如 '%'、' 次'
 *   duration  动画时长 ms，默认 1400
 * 千分位按 en-US 格式化（1,234.5），与仪表板现有数字一致。
 */
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { motionLevel } from '@/utils/motion'

const props = withDefaults(
  defineProps<{
    value: number
    decimals?: number
    suffix?: string
    duration?: number
  }>(),
  { decimals: 0, suffix: '', duration: 1400 },
)

// 非 rich 档首帧就显示目标值，避免先闪一帧 0
const display = ref(motionLevel() === 'rich' ? 0 : props.value)
let frame = 0

const text = computed(() => {
  if (!Number.isFinite(props.value)) return '-'
  const formatted = display.value.toLocaleString('en-US', {
    minimumFractionDigits: props.decimals,
    maximumFractionDigits: props.decimals,
  })
  return `${formatted}${props.suffix}`
})

function animateTo(target: number) {
  if (frame) {
    window.cancelAnimationFrame(frame)
    frame = 0
  }
  // 非有限数、非 rich 档、页面在后台（rAF 不跑）时直接落到目标值
  if (!Number.isFinite(target) || motionLevel() !== 'rich' || document.visibilityState === 'hidden') {
    display.value = Number.isFinite(target) ? target : 0
    return
  }
  const from = Number.isFinite(display.value) ? display.value : 0
  const start = performance.now()
  const step = (now: number) => {
    const progress = Math.min(1, (now - start) / Math.max(1, props.duration))
    const eased = progress === 1 ? 1 : 1 - 2 ** (-10 * progress)
    display.value = from + (target - from) * eased
    frame = progress < 1 ? window.requestAnimationFrame(step) : 0
  }
  frame = window.requestAnimationFrame(step)
}

onMounted(() => animateTo(props.value))
watch(() => props.value, (next) => animateTo(next))
onBeforeUnmount(() => {
  if (frame) window.cancelAnimationFrame(frame)
})
</script>

<template>
  <span class="dd-count-up">{{ text }}</span>
</template>

<style scoped>
/* 等宽数字：滚动时宽度不跳 */
.dd-count-up {
  font-variant-numeric: tabular-nums;
}
</style>
