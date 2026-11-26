/**
 * 跟随指针的两个效果（design §2.2），都只在 rich 档、且是鼠标 / 手写笔时生效：
 *   - installSpotlight()：全局唯一的 pointermove 监听，rAF 节流，给指针下最近的 .dd-spot 写 --dd-mx / --dd-my；
 *   - v-magnetic：主按钮的磁吸（只给主按钮用，别到处挂）。
 * 档位直接读 <html data-dd-motion>（applyMotionPreference 维护），不在每一帧里重算 matchMedia。
 * main.ts 负责调用 installSpotlight() 并注册 app.directive('magnetic', vMagnetic)。
 */
import type { Directive } from 'vue'

function isRichMotion() {
  return document.documentElement.getAttribute('data-dd-motion') === 'rich'
}

let spotlightInstalled = false

/** 光斑描边的全局监听。重复调用无副作用 */
export function installSpotlight() {
  if (spotlightInstalled || typeof document === 'undefined') return
  spotlightInstalled = true

  let frame = 0
  let lastEvent: PointerEvent | null = null
  document.addEventListener(
    'pointermove',
    (event) => {
      // 触屏没有「悬停」，光斑只会在手指划过时乱闪；CSS 那边也用 (hover: none) 关掉了
      if (event.pointerType === 'touch') return
      // 非丰富档（v4.0.2 起是默认）连 rAF 都不排：鼠标移动时不额外占一帧回调
      if (!isRichMotion()) return
      lastEvent = event
      if (frame) return
      frame = window.requestAnimationFrame(() => {
        frame = 0
        const current = lastEvent
        if (!current || !isRichMotion()) return
        const target = current.target instanceof Element ? current.target.closest<HTMLElement>('.dd-spot') : null
        if (!target) return
        const rect = target.getBoundingClientRect()
        target.style.setProperty('--dd-mx', `${current.clientX - rect.left}px`)
        target.style.setProperty('--dd-my', `${current.clientY - rect.top}px`)
      })
    },
    { passive: true },
  )
}

type MagneticElement = HTMLElement & {
  __ddMagnetic?: { move: (event: PointerEvent) => void; leave: () => void }
}

/**
 * v-magnetic：指针在按钮上移动时，按钮朝指针方向偏一点（横 22%、纵 35%），移开后弹簧回位。
 * 回位的过渡写在 motion.scss 的 .dd-magnetic 上（指令挂载时加这个类）。
 */
export const vMagnetic: Directive<HTMLElement> = {
  mounted(el) {
    const element = el as MagneticElement
    element.classList.add('dd-magnetic')
    const move = (event: PointerEvent) => {
      if (event.pointerType === 'touch' || !isRichMotion()) return
      const rect = element.getBoundingClientRect()
      const offsetX = (event.clientX - rect.left - rect.width / 2) * 0.22
      const offsetY = (event.clientY - rect.top - rect.height / 2) * 0.35
      element.style.transform = `translate(${offsetX}px, ${offsetY}px)`
    }
    const leave = () => {
      element.style.transform = ''
    }
    element.addEventListener('pointermove', move)
    element.addEventListener('pointerleave', leave)
    element.__ddMagnetic = { move, leave }
  },
  beforeUnmount(el) {
    const element = el as MagneticElement
    if (element.__ddMagnetic) {
      element.removeEventListener('pointermove', element.__ddMagnetic.move)
      element.removeEventListener('pointerleave', element.__ddMagnetic.leave)
      delete element.__ddMagnetic
    }
    element.classList.remove('dd-magnetic')
    element.style.transform = ''
  },
}
