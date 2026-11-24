/**
 * 动效系统 2.0 的 JS 工具（design §2.2）。CSS 那一半在 styles/motion.scss。
 *
 * 全部按「解析后的动效档位」分档（rich / subtle / off，见 utils/panelAppearance.ts 的 resolveMotionLevel）：
 *   - withThemeTransition：rich 档才做圆形扩散，其余直接切；
 *   - burst：只在 rich 档出现。
 * 浏览器不支持 View Transitions 时一律直接执行更新，不报错。
 */
import { resolveMotionLevel, type MotionLevel } from './panelAppearance'

export type { MotionLevel }

/** 当前生效的动效档位。每次现算（读本机偏好 + 系统「减少动态效果」+ 低端设备判定），很便宜 */
export function motionLevel(): MotionLevel {
  return resolveMotionLevel()
}

/** 过渡的起点：传点击事件（取 clientX / clientY）或直接给坐标 */
export type TransitionOrigin = { x: number; y: number } | { clientX: number; clientY: number }

/**
 * 切主题 / 主色时的圆形扩散过渡：从点击点展开新外观，0.76s。
 *
 * update 里只做「改状态 / 改 <html> 属性」这类同步操作；它在旧画面截图之后、新画面截图之前执行。
 * 没传 origin 时从右上角（顶栏主题按钮附近）展开。
 * 过渡期间 <html> 上挂 dd-vt-theme：motion.scss 用它关掉根快照默认的交叉淡化；
 * 内容区的 dd-main 命名只在切页过渡期间存在（router/index.ts），这里天然不会把内容区单独拎出来。
 */
export function withThemeTransition(update: () => void, origin?: TransitionOrigin | null) {
  if (
    typeof document === 'undefined'
    || typeof document.startViewTransition !== 'function'
    || document.visibilityState === 'hidden'
    || motionLevel() !== 'rich'
  ) {
    update()
    return
  }

  const x = origin ? ('clientX' in origin ? origin.clientX : origin.x) : window.innerWidth - 60
  const y = origin ? ('clientY' in origin ? origin.clientY : origin.y) : 40
  // 扩散半径取点击点到最远一个角的距离，保证最后整屏都被盖住
  const radius = Math.hypot(Math.max(x, window.innerWidth - x), Math.max(y, window.innerHeight - y))
  const root = document.documentElement
  root.classList.add('dd-vt-theme')

  let transition: ViewTransition
  try {
    transition = document.startViewTransition(update)
  } catch {
    // 个别实现在特殊状态下（例如文档正在卸载）会直接抛错：退回直接切换
    root.classList.remove('dd-vt-theme')
    update()
    return
  }

  transition.ready
    .then(() => {
      root.animate(
        { clipPath: [`circle(0px at ${x}px ${y}px)`, `circle(${radius}px at ${x}px ${y}px)`] },
        { duration: 760, easing: 'cubic-bezier(0.22, 1, 0.36, 1)', pseudoElement: '::view-transition-new(root)' },
      )
    })
    // ready 在过渡被跳过（同时又发起了一次过渡、页面切到后台）时会 reject，不影响 update 已经执行
    .catch(() => {})
  const cleanup = () => root.classList.remove('dd-vt-theme')
  transition.finished.then(cleanup, cleanup)
}

/**
 * 结果烟花：在 (x, y)（视口坐标）放 14 个小圆点向四周散开，0.8s 后自动移除。只在 rich 档生效。
 * 典型用法：任务手动运行成功后 burst(event.clientX, event.clientY)。
 */
export function burst(x: number, y: number) {
  if (typeof document === 'undefined' || motionLevel() !== 'rich') return
  const box = document.createElement('div')
  box.className = 'dd-burst'
  box.style.left = `${x}px`
  box.style.top = `${y}px`
  const colors = ['var(--dd-accent)', 'var(--dd-accent-2)', 'var(--dd-ok)', 'var(--dd-warn)']
  for (let index = 0; index < 14; index += 1) {
    const angle = (index / 14) * Math.PI * 2
    const distance = 38 + Math.random() * 30
    const dot = document.createElement('i')
    dot.style.setProperty('--dd-burst-x', `${Math.cos(angle) * distance}px`)
    dot.style.setProperty('--dd-burst-y', `${Math.sin(angle) * distance}px`)
    dot.style.setProperty('--dd-burst-color', colors[index % colors.length] ?? 'var(--dd-accent)')
    box.appendChild(dot)
  }
  document.body.appendChild(box)
  window.setTimeout(() => box.remove(), 900)
}
