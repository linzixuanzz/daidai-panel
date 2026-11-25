<script setup lang="ts">
/**
 * 呆呆主题的侧栏小人（design §3）：把登录页那四个小人请进侧栏底部，趴在墙头偷看，眼睛跟着鼠标转。
 *
 * - 只由 MainLayout 在 data-dd-theme="daidai" 且侧栏展开时渲染（这是内容差异，不是样式分支）。
 * - 眼睛跟随只在动效档位为 rich 时生效；subtle / off 档眼睛停在正中。
 *   档位每次 pointermove 现算（motionLevel() 很便宜），切档后不用重新挂载。
 * - 触屏设备（hover: none）没有「鼠标位置」，不挂监听。
 * - 小人身上的颜色是插画色（与登录页 Characters.vue 同一组），不随主题变；描边读 --dd-border，
 *   这样呆呆暗色下自动变成浅色描边。
 */
import { onBeforeUnmount, onMounted, ref } from 'vue'
import { motionLevel } from '@/utils/motion'

const rootRef = ref<HTMLElement | null>(null)

let frame = 0
let lastX = 0
let lastY = 0

function updateEyes() {
  frame = 0
  const root = rootRef.value
  if (!root) return
  const pupils = root.querySelectorAll<HTMLElement>('.critter-eye > i')
  if (motionLevel() !== 'rich') {
    // 非 rich 档：眼睛回正后不再动
    pupils.forEach((pupil) => { pupil.style.transform = '' })
    return
  }
  pupils.forEach((pupil) => {
    const eye = pupil.parentElement
    if (!eye) return
    const rect = eye.getBoundingClientRect()
    const cx = rect.left + rect.width / 2
    const cy = rect.top + rect.height / 2
    const angle = Math.atan2(lastY - cy, lastX - cx)
    // 离得越远瞳孔越贴边，最多偏 2.5px（眼白 10px、瞳孔 5px）
    const distance = Math.min(2.5, Math.hypot(lastX - cx, lastY - cy) / 60)
    pupil.style.transform = `translate(${Math.cos(angle) * distance}px, ${Math.sin(angle) * distance}px)`
  })
}

function handlePointerMove(event: PointerEvent) {
  lastX = event.clientX
  lastY = event.clientY
  // rAF 节流：一帧最多算一次
  if (!frame) frame = window.requestAnimationFrame(updateEyes)
}

onMounted(() => {
  if (window.matchMedia?.('(hover: none)').matches) return
  window.addEventListener('pointermove', handlePointerMove, { passive: true })
})

onBeforeUnmount(() => {
  window.removeEventListener('pointermove', handlePointerMove)
  if (frame) window.cancelAnimationFrame(frame)
  frame = 0
})
</script>

<template>
  <div ref="rootRef" class="sidebar-critters" aria-hidden="true">
    <span class="critter critter--blue" style="--k: 0">
      <span class="critter-eye" style="left: 8px; top: 13px"><i /></span>
      <span class="critter-eye" style="left: 25px; top: 13px"><i /></span>
    </span>
    <span class="critter critter--black" style="--k: 1">
      <span class="critter-eye" style="left: 5px; top: 12px"><i /></span>
      <span class="critter-eye" style="left: 20px; top: 12px"><i /></span>
    </span>
    <span class="critter critter--yellow" style="--k: 2">
      <span class="critter-eye" style="left: 8px; top: 15px"><i /></span>
      <span class="critter-eye" style="left: 23px; top: 15px"><i /></span>
    </span>
    <span class="critter critter--orange" style="--k: 3">
      <span class="critter-eye" style="left: 22px; top: 13px"><i /></span>
      <span class="critter-eye" style="left: 47px; top: 13px"><i /></span>
    </span>
  </div>
</template>

<style scoped lang="scss">
.sidebar-critters {
  // 插画色：与登录页小人同一组（views/login/Characters.vue），不随主题变
  --critter-blue: #1e7bff;
  --critter-black: #232326;
  --critter-yellow: #f5c542;
  --critter-orange: #f5821f;
  --critter-eye-white: #ffffff;
  --critter-pupil: #111111;

  flex: none;
  display: flex;
  align-items: flex-start;
  justify-content: center;
  height: 58px;
  overflow: hidden;
  padding: 0 8px;
  pointer-events: none;
}

.critter {
  position: relative;
  display: block;
  flex: none;
  // 描边读主题边框：呆呆亮色是粗黑线，暗色是浅色线
  border: var(--dd-border-width) solid var(--dd-border);
  border-bottom: 0;
  // 从墙后探出来：依次延迟 110ms；关闭档由全局统一去掉动画，克制档的弹簧已被换成减速曲线
  animation: dd-critter-peek 0.9s var(--dd-spring) both;
  animation-delay: calc(var(--k) * 110ms + 0.2s);
}

@keyframes dd-critter-peek {
  from {
    transform: translateY(60px);
  }
}

// 4 只小人总宽 195px（46 + 32 + 41 + 76），在 232px 侧栏底宽里居中排布；
// 负外边距按各小人右眼安全区精确收窄，保证相邻小人不遮挡任何一只眼睛（8 只眼全部完整露出）
.critter--blue {
  z-index: 1;
  width: 46px;
  height: 80px;
  margin-top: 4px;
  background: var(--critter-blue);
  border-radius: 10px 10px 0 0;
}

.critter--black {
  z-index: 2;
  width: 38px;
  height: 70px;
  margin-top: 18px;
  margin-left: -6px;
  background: var(--critter-black);
  border-radius: 9px 9px 0 0;
}

.critter--yellow {
  z-index: 3;
  width: 44px;
  height: 62px;
  margin-top: 10px;
  margin-left: -3px;
  background: var(--critter-yellow);
  border-radius: 22px 22px 0 0;
}

.critter--orange {
  z-index: 4;
  width: 82px;
  height: 46px;
  margin-top: 22px;
  margin-left: -6px;
  background: var(--critter-orange);
  border-radius: 41px 41px 0 0;
}

.critter-eye {
  position: absolute;
  width: 10px;
  height: 10px;
  // 眼睛是插画元素，天然正圆，不吃圆角令牌
  border-radius: 50%;
  background: var(--critter-eye-white);

  > i {
    position: absolute;
    left: 2.5px;
    top: 2.5px;
    width: 5px;
    height: 5px;
    border-radius: 50%;
    background: var(--critter-pupil);
    transition: transform 0.12s linear;
  }
}

// 黄色小人是「黑眼珠白高光」的画法（同登录页），眼白透明
.critter--yellow .critter-eye {
  background: transparent;

  > i {
    background: var(--critter-black);
  }
}
</style>
