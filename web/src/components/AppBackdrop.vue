<script setup lang="ts">
/**
 * 全屏背景层（design §1、§2.2「背景流动」）：底色 / 背景图 / 极光块 / 网格 / 噪点 / 扫描线。
 * 挂在 App.vue，登录页和主布局都在它上面。所有颜色、透明度都读主题令牌（--dd-bg-*），
 * 背景效果（data-dd-bg）在 styles/themes/_switches.scss 里改的也是这些令牌，这里不关心是哪个主题。
 *
 * 层级：position:fixed + z-index:-1，画在根层叠上下文的「负层」，在所有普通流内容之下。
 * 为此 global.scss 在有主题时把 body / #app 的底色清成了透明（见那边「主题引擎接入」一段）。
 *
 * 流动：只有 rich 档极光块才漂移；页面切到后台时暂停（不是移除，回来时接着原位置动，不会跳）。
 * subtle / off 档直接不挂动画（animation: none），「关闭」档 document.getAnimations() 里不会有它们。
 * v4.0.2（#182）起 rich 只能由用户显式选择（「跟随系统」解析为 subtle），所以默认背景是静止的。
 *
 * 性能（v4.0.2，#182）：光斑原来是 56vmax 的纯色圆 + filter: blur(90px)，漂移时每帧整屏重新模糊，
 * 没开硬件加速时仪表板空闲只有 12fps。现在改成 radial-gradient 直接画出「中心实、向外渐隐」的柔光，
 * 不再有任何 filter；元素放大一圈、位置同步外移，补回原来模糊溢出到圆外的那圈光晕，中心位置不变。
 * 流畅模式（themeStore.lite）下只留底色与背景图（含它的可读遮罩），光斑 / 网格 / 噪点 / 扫描线都不渲染。
 */
import { computed } from 'vue'
import { usePageActivity } from '@/composables/usePageActivity'
import { useThemeStore } from '@/stores/theme'
import { THEME_PRESETS } from '@/utils/panelAppearance'

const themeStore = useThemeStore()
const { isDocumentVisible } = usePageActivity()

// 4 个铺满大半屏的光斑层即使不再模糊也有合成开销，用不到时直接不渲染：
// 背景效果选了「极光」一定要；「跟随主题」看主题有没有极光块（经典、纸墨没有）；其余都不要
const showBlobs = computed(() => {
  if (themeStore.lite) return false
  if (themeStore.background === 'aurora') return true
  if (themeStore.background !== 'theme') return false
  return THEME_PRESETS.find((item) => item.id === themeStore.preset)?.blobs ?? false
})

// 背景图只挂在本组件的根上（--dd-bg-image），不写进 <html> 的内联样式：
// data URL 可能有几 MB，放在 <html> 上会让每次读写 style 属性、DevTools 查看元素都很卡
const imageStyle = computed(() => {
  if (themeStore.background !== 'image' || !themeStore.backgroundImage) return null
  return { '--dd-bg-image': `url("${themeStore.backgroundImage.replace(/"/g, '\\"')}")` }
})
</script>

<template>
  <div
    class="dd-backdrop"
    :class="{ 'is-flowing': themeStore.motionLevel === 'rich', 'is-paused': !isDocumentVisible }"
    :style="imageStyle ?? undefined"
    aria-hidden="true"
  >
    <div v-if="imageStyle" class="dd-backdrop__image" />
    <template v-if="showBlobs">
      <i class="dd-backdrop__blob is-1" />
      <i class="dd-backdrop__blob is-2" />
      <i class="dd-backdrop__blob is-3" />
      <i class="dd-backdrop__blob is-4" />
    </template>
    <!-- 流畅模式：网格 / 噪点（mix-blend-mode）/ 扫描线三层一起不渲染，只剩纯底色 -->
    <template v-if="!themeStore.lite">
      <div class="dd-backdrop__grid" />
      <div class="dd-backdrop__noise" />
      <div class="dd-backdrop__scan" />
    </template>
  </div>
</template>

<style scoped lang="scss">
.dd-backdrop {
  position: fixed;
  inset: 0;
  z-index: -1;
  overflow: hidden;
  pointer-events: none;
  background: var(--dd-bg);
  transition: background-color 0.6s var(--dd-ease-standard);
}

.dd-backdrop__image {
  position: absolute;
  inset: 0;
  background: var(--dd-bg-image) center / cover no-repeat;

  // 压一层底色（52%），背景图再花哨，未包卡片的页面标题与边缘文字也清晰可读
  &::after {
    content: "";
    position: absolute;
    inset: 0;
    background: color-mix(in srgb, var(--dd-bg) 52%, transparent);
  }
}

// 极光块：径向渐变画的柔光（不用 filter），属于「形状承载语义」的装饰光斑，不吃圆角令牌。
// 尺寸比原来的 56vmax / 40vmax 各大一圈（68 / 50），四边偏移同步外扩 6 / 5vmax：中心点与原来一致，
// 多出来的一圈留给渐隐的外沿，近似原来 blur(90px) 溢出到圆外的光晕。
// 渐变按原来「圆 + 高斯模糊」的亮度轮廓取点：原圆半径约等于新半径的 82%，模糊让它在 ±2σ（约 ±28% 半径）内渐隐，
// 所以 0~50% 半径是实色、80% 处（过渡中点提示）亮度减半、100% 处透明。
.dd-backdrop__blob {
  position: absolute;
  width: 68vmax;
  height: 68vmax;
  background: radial-gradient(circle closest-side, var(--dd-blob-color) 0%, var(--dd-blob-color) 50%, 80%, transparent 100%);
  opacity: var(--dd-bg-blob-opacity);
  transition: opacity 0.8s var(--dd-ease-standard);

  &.is-1 {
    top: -28vmax;
    left: -18vmax;
    --dd-blob-color: var(--dd-bg-blob-1);
  }

  &.is-2 {
    top: -14vmax;
    right: -26vmax;
    --dd-blob-color: var(--dd-bg-blob-2);
  }

  &.is-3 {
    bottom: -36vmax;
    left: 12vmax;
    --dd-blob-color: var(--dd-bg-blob-3);
  }

  &.is-4 {
    bottom: -31vmax;
    right: -13vmax;
    width: 50vmax;
    height: 50vmax;
    --dd-blob-color: var(--dd-bg-blob-4);
  }
}

// 只有 rich 档才漂移；时长互不整除，四块不会同步
.is-flowing .dd-backdrop__blob {
  will-change: transform;

  &.is-1 {
    animation: dd-drift-1 24s var(--dd-ease-io) infinite alternate;
  }

  &.is-2 {
    animation: dd-drift-2 29s var(--dd-ease-io) infinite alternate;
  }

  &.is-3 {
    animation: dd-drift-3 33s var(--dd-ease-io) infinite alternate;
  }

  &.is-4 {
    animation: dd-drift-4 21s var(--dd-ease-io) infinite alternate;
  }
}

.is-paused .dd-backdrop__blob {
  animation-play-state: paused;
}

@keyframes dd-drift-1 {
  to {
    transform: translate(20vmax, 14vmax) scale(1.18) rotate(30deg);
  }
}

@keyframes dd-drift-2 {
  to {
    transform: translate(-16vmax, 18vmax) scale(0.86);
  }
}

@keyframes dd-drift-3 {
  to {
    transform: translate(-14vmax, -12vmax) scale(1.12);
  }
}

@keyframes dd-drift-4 {
  to {
    transform: translate(-22vmax, -8vmax) scale(1.25);
  }
}

.dd-backdrop__grid {
  position: absolute;
  inset: 0;
  background-image:
    linear-gradient(var(--dd-bg-grid-line) 1px, transparent 1px),
    linear-gradient(90deg, var(--dd-bg-grid-line) 1px, transparent 1px);
  background-size: 46px 46px;
  opacity: var(--dd-bg-grid-opacity);
  -webkit-mask-image: radial-gradient(ellipse 85% 70% at 50% 25%, #000 25%, transparent 78%);
  mask-image: radial-gradient(ellipse 85% 70% at 50% 25%, #000 25%, transparent 78%);
  transition: opacity 0.8s var(--dd-ease-standard);
}

// 噪点 / 纸纹：SVG feTurbulence 内联，不发请求。亮底 multiply、暗底 screen（主题令牌 --dd-bg-noise-blend）
.dd-backdrop__noise {
  position: absolute;
  inset: 0;
  opacity: var(--dd-bg-noise-opacity);
  background-image: url("data:image/svg+xml;utf8,<svg xmlns='http://www.w3.org/2000/svg' width='180' height='180'><filter id='n'><feTurbulence type='fractalNoise' baseFrequency='.85' numOctaves='3' stitchTiles='stitch'/></filter><rect width='100%' height='100%' filter='url(%23n)'/></svg>");
  mix-blend-mode: var(--dd-bg-noise-blend, multiply);
  transition: opacity 0.8s var(--dd-ease-standard);
}

// 扫描线（终端主题）：深色细横纹，靠 --dd-bg-scan-opacity 控制浓淡
.dd-backdrop__scan {
  position: absolute;
  inset: 0;
  background: repeating-linear-gradient(0deg, rgba(0, 0, 0, 0.32) 0 1px, transparent 1px 3px);
  opacity: var(--dd-bg-scan-opacity);
  transition: opacity 0.8s var(--dd-ease-standard);
}
</style>
