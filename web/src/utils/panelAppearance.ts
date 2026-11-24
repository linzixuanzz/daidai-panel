import { loadPanelSettings, type PanelSettingsPayload, type PanelShapeStyle } from './panelSettings'

export interface PanelAppearanceSettings extends PanelSettingsPayload {}

/**
 * 圆角三档刻度表。
 *
 * 刻度集中在这一处，不要把数值散到分支里：调一档、加一档都只改这里。
 * square 各项全 0，与 styles/global.scss `:root` 里的默认值一致 ——
 * 这样「没写过内联变量」和「显式写成直角」看到的是同一个结果。
 *
 * 对应关系（详见 spec/frontend/design-system.md）：
 * - control：按钮、输入框、tag、分段项、图标按钮、浮层菜单项
 * - surface：卡片、表格容器、弹窗、日志面板等容器类表面
 * - pill：状态 chip、计数角标、进度条这类天然胶囊
 * - mobileControl / mobileSurface（v3.3.1，issue #143）：≤768px 下「按钮」「卡片」两个角色的刻度。
 *   它们不直接被组件消费，而是由 global.scss 里的角色令牌 --dd-radius-button / --dd-radius-card
 *   在移动端媒体查询里取用（桌面上这两个角色令牌仍等于 control / surface）。
 *   square 档同样全 0：形状是全局配置，默认直角的面板在手机上也必须是直角，不能自己圆起来。
 * - theme（v4.0，默认）：写成 var(--dd-theme-radius-*)，取当前主题给的刻度（styles/themes/*.scss）。
 *   内联值里的 var() 在 <html> 上代换，<html> 上正好挂着主题块，切风格时圆角自动跟着变，不用重写内联值。
 */
const PANEL_SHAPE_RADIUS: Record<
  PanelShapeStyle,
  { control: string; surface: string; pill: string; mobileControl: string; mobileSurface: string }
> = {
  theme: {
    control: 'var(--dd-theme-radius-control)',
    surface: 'var(--dd-theme-radius-surface)',
    pill: 'var(--dd-theme-radius-pill)',
    mobileControl: 'var(--dd-theme-radius-control-mobile)',
    mobileSurface: 'var(--dd-theme-radius-surface-mobile)',
  },
  square: { control: '0', surface: '0', pill: '0', mobileControl: '0', mobileSurface: '0' },
  rounded: { control: '6px', surface: '10px', pill: '999px', mobileControl: '16px', mobileSurface: '20px' },
}

// v4.0 起默认跟随主题（与服务端注册表的默认值一致）。只在「本机没有缓存」时用得到。
const DEFAULT_PANEL_SHAPE: PanelShapeStyle = 'theme'

/**
 * 圆角风格的本机缓存键（命名沿用仓库既有的 `dd:<模块>:<名字>`）。
 *
 * 存在的唯一理由是首屏防闪形：panel-settings 是异步 fetch，首帧一定先按 :root 的
 * 直角画一遍，圆角用户会看到「先方后圆」的形状跳变，比闪色刺眼得多。
 * 这里只是一份加速缓存，服务端值一回来就以服务端为准。
 */
const PANEL_SHAPE_STORAGE_KEY = 'dd:appearance:shape'

const DEFAULT_LOG_BACKGROUND_COLOR_LIGHT = '#f8fafc'
const DEFAULT_LOG_BACKGROUND_COLOR_DARK = '#0f172a'
// 编辑器底色的「留空默认值」跟随面板明暗主题：浅色模式白底深字，深色模式深底浅字。
// 注意：只有 editor_background_color 留空时才走这里；用户在系统设置里显式设过颜色一律以用户值为准。
const DEFAULT_EDITOR_BACKGROUND_COLOR_LIGHT = '#ffffff'
const DEFAULT_EDITOR_BACKGROUND_COLOR_DARK = '#111827'
const DEFAULT_EDITOR_FOREGROUND_COLOR = '#e5e7eb'
const DEFAULT_LOG_TEXT_COLOR_LIGHT = '#111827'
const DEFAULT_LOG_TEXT_COLOR_DARK = '#e2e8f0'

/**
 * 面板外观（编辑器/日志底色等）变更事件。
 * 代码编辑器的主题是在挂载时一次性构建的（CodeMirror 的 extensions 只在建 state 时读一次），
 * 切主题或改配置后必须靠这个事件重新构建主题并通过 Compartment 换上去，
 * 否则已挂载的编辑器不会跟着变色。
 */
export const PANEL_APPEARANCE_CHANGE_EVENT = 'dd:panel-appearance-change'

function toCSSImageValue(image?: string) {
  const trimmed = image?.trim() || ''
  if (!trimmed) {
    return 'none'
  }

  return `url("${trimmed.replace(/"/g, '\\"')}")`
}

function parseColor(color?: string) {
  const text = color?.trim() || ''
  if (!text) return null

  if (text.startsWith('#')) {
    const hex = text.slice(1)
    if (hex.length === 3) {
      const r = Number.parseInt(hex.charAt(0) + hex.charAt(0), 16)
      const g = Number.parseInt(hex.charAt(1) + hex.charAt(1), 16)
      const b = Number.parseInt(hex.charAt(2) + hex.charAt(2), 16)
      return Number.isNaN(r) || Number.isNaN(g) || Number.isNaN(b) ? null : { r, g, b }
    }
    if (hex.length === 6 || hex.length === 8) {
      const offset = hex.length === 8 ? 2 : 0
      const r = Number.parseInt(hex.slice(offset, offset + 2), 16)
      const g = Number.parseInt(hex.slice(offset + 2, offset + 4), 16)
      const b = Number.parseInt(hex.slice(offset + 4, offset + 6), 16)
      return Number.isNaN(r) || Number.isNaN(g) || Number.isNaN(b) ? null : { r, g, b }
    }
  }

  const match = text.match(/^rgba?\(\s*(\d{1,3})\s*,\s*(\d{1,3})\s*,\s*(\d{1,3})(?:\s*,\s*[0-9.]+\s*)?\)$/i)
  if (!match) {
    return null
  }

  const r = Number.parseInt(match[1] ?? '', 10)
  const g = Number.parseInt(match[2] ?? '', 10)
  const b = Number.parseInt(match[3] ?? '', 10)
  return Number.isNaN(r) || Number.isNaN(g) || Number.isNaN(b) ? null : { r, g, b }
}

export function isDarkColor(background?: string) {
  const rgb = parseColor(background)
  if (!rgb) {
    // 解析不出来时按深色处理，与历史行为一致（前景色回退到浅字）
    return true
  }

  const toLinear = (channel: number) => {
    const value = channel / 255
    return value <= 0.03928 ? value / 12.92 : ((value + 0.055) / 1.055) ** 2.4
  }

  const luminance = 0.2126 * toLinear(rgb.r) + 0.7152 * toLinear(rgb.g) + 0.0722 * toLinear(rgb.b)
  return luminance < 0.45
}

export function getReadableTextColor(background?: string) {
  return isDarkColor(background) ? DEFAULT_EDITOR_FOREGROUND_COLOR : DEFAULT_LOG_TEXT_COLOR_LIGHT
}

export function isPanelDarkMode() {
  return typeof document !== 'undefined' && document.documentElement.classList.contains('dark')
}

export function getDefaultEditorBackgroundColor(isDark: boolean) {
  return isDark ? DEFAULT_EDITOR_BACKGROUND_COLOR_DARK : DEFAULT_EDITOR_BACKGROUND_COLOR_LIGHT
}

function getDefaultLogBackgroundColor(isDark: boolean) {
  return isDark ? DEFAULT_LOG_BACKGROUND_COLOR_DARK : DEFAULT_LOG_BACKGROUND_COLOR_LIGHT
}

function getDefaultLogTextColor(isDark: boolean) {
  return isDark ? DEFAULT_LOG_TEXT_COLOR_DARK : DEFAULT_LOG_TEXT_COLOR_LIGHT
}

// 服务端下发的值不可信（历史脏值、将来新增枚举值、大小写/空格），认不出来一律返回 null
// 交给调用方决定回退策略，而不是在这里直接兜成 square —— 见 lastAppliedShape 的说明。
//
// 回滚兼容（design §7）：v3.4.x 的这个函数只认 rounded / square，读到 v4.0 写下的 'theme'
// （服务端配置或本机缓存 dd:appearance:shape）会返回 null → 沿用上一次 / 缓存认不出时回落直角，不会报错。
function normalizePanelShape(raw?: string | null): PanelShapeStyle | null {
  const value = String(raw ?? '').trim().toLowerCase()
  if (value === 'theme') return 'theme'
  if (value === 'rounded') return 'rounded'
  if (value === 'square') return 'square'
  return null
}

function readCachedPanelShape(): PanelShapeStyle {
  // 隐私模式 / 禁用站点存储时读 localStorage 会直接抛错，这里在模块初始化阶段执行，
  // 不兜住会让整个 main.ts 挂掉、页面全白
  try {
    if (typeof window === 'undefined') return DEFAULT_PANEL_SHAPE
    return normalizePanelShape(window.localStorage.getItem(PANEL_SHAPE_STORAGE_KEY)) ?? DEFAULT_PANEL_SHAPE
  } catch {
    return DEFAULT_PANEL_SHAPE
  }
}

// 最近一次显式取到的圆角风格。刻意与 lastAppliedSettings 分开记：
// 不是每次 applyPanelAppearance 都带着合法的 panel_shape_style —— panel-settings 拉取失败时传的是 null，
// 历史脏值 / 将来新增的枚举值也认不出来。跟着整份设置一起被覆盖的话，这些时候就会被拍回直角。
// ⇒ 只有本次真的带了合法值才更新，否则沿用上一次的结果。
// （v3.2.9 起 panel_shape_style 已并进设置页的 configForm、由「通用设置 → 面板外观」卡编辑，
//   设置页加载/保存时传进 applyPanelAppearance 的快照本身就带着已保存的值，见 useSettingsConfig.ts 的 appearanceSnapshot。）
let lastAppliedShape: PanelShapeStyle = readCachedPanelShape()

/**
 * 把圆角刻度写进 documentElement 的 `--dd-radius-*`（三档刻度 + 两条移动端刻度）。
 *
 * 传了合法值就以它为准并记进本机缓存；不传（或值不认识）就沿用上一次的结果。
 * 两个调用方：
 * 1. main.ts 在 createApp 之前不传参同步调一次（用 localStorage 缓存防首屏闪形）；
 * 2. applyPanelAppearance() 拿 panel_shape_style 调：数据源是免登录的 panel-settings，
 *    或设置页的外观快照（圆角只取已保存的值）—— 「面板外观」卡保存成功后 useSettingsConfig.ts 的
 *    saveConfigKeys() 会重跑 applyPanelAppearance，切换后即时生效，无需刷新页面。
 *
 * square 这一档是显式写 `0` 而不是 removeProperty()：同一次会话里可能先写过 rounded
 * （改配置、切主题都会重跑），显式写死才是幂等的；靠移除内联覆盖去露出 :root 的 0，
 * 还要额外保证 :root 那三条永远存在，反而更脆。
 *
 * v4.0 优先级：本机圆角滑杆（dd:appearance:radius，主题工坊）有值时压过一切 shape：
 *   control = 滑杆值 × 0.6 取整，surface = 滑杆值，pill = 999px（滑杆为 0 时 pill 也是 0，与 square 同义）；
 *   移动端两条直接等于 control / surface（滑杆是用户明确给的像素，不再按手机放大）。
 * 滑杆值不进 lastAppliedShape：它是本机覆盖，清掉滑杆就回到全站的 shape。
 */
export function applyPanelShapeStyle(raw?: string | null) {
  const next = normalizePanelShape(raw)
  if (next) {
    lastAppliedShape = next
    try {
      window.localStorage.setItem(PANEL_SHAPE_STORAGE_KEY, next)
    } catch {
      // 隐私模式 / 禁用站点存储：写不进去只是下次首屏会闪一下形状，不影响本次生效
    }
  }

  if (typeof document === 'undefined') return
  const slider = readLocalAppearance().radius
  const radius = slider === null
    ? PANEL_SHAPE_RADIUS[lastAppliedShape]
    : {
        control: `${Math.round(slider * 0.6)}px`,
        surface: `${slider}px`,
        pill: slider === 0 ? '0' : '999px',
        mobileControl: `${Math.round(slider * 0.6)}px`,
        mobileSurface: `${slider}px`,
      }
  const root = document.documentElement
  root.style.setProperty('--dd-radius-control', radius.control)
  root.style.setProperty('--dd-radius-surface', radius.surface)
  root.style.setProperty('--dd-radius-pill', radius.pill)
  // 移动端刻度必须和三档刻度在同一个函数里一起写：main.ts 首屏预热调用的就是这里，
  // 漏写的话 rounded 面板在手机上会先按 :root 的 0 画一帧直角卡片，再等 panel-settings 回来才变圆。
  // 与三档一样，global.scss 的 :root 里这两条【绝不能带 !important】，否则这里的内联普通声明写不进去。
  root.style.setProperty('--dd-radius-control-mobile', radius.mobileControl)
  root.style.setProperty('--dd-radius-surface-mobile', radius.mobileSurface)
}

// 最近一次显式传入的外观设置。
// 主题切换只需要重新推导「留空默认值」，不应该把用户已保存的自定义颜色一起抹掉，
// 所以 applyPanelAppearance() 不传参时复用这里的缓存。
let lastAppliedSettings: PanelAppearanceSettings | null = null

export function applyPanelAppearance(settings?: PanelAppearanceSettings | null) {
  if (typeof settings !== 'undefined') {
    lastAppliedSettings = settings
  }

  const effective = lastAppliedSettings
  const root = document.documentElement
  const isDark = root.classList.contains('dark')
  const editorBackground =
    effective?.editor_background_color?.trim() || getDefaultEditorBackgroundColor(isDark)
  const logBackground = effective?.log_background_color?.trim() || getDefaultLogBackgroundColor(isDark)
  root.style.setProperty('--dd-editor-bg-color', editorBackground)
  root.style.setProperty('--dd-editor-fg-color', getReadableTextColor(editorBackground))
  root.style.setProperty('--dd-log-bg-color', logBackground)
  root.style.setProperty('--dd-log-text-color', getReadableTextColor(logBackground) || getDefaultLogTextColor(isDark))
  root.style.setProperty('--dd-log-theme-mode', isDark ? 'dark' : 'light')
  root.style.setProperty('--dd-log-bg-image', toCSSImageValue(effective?.log_background_image))

  // 圆角刻度：本次带了合法值就更新，没带就沿用上一次的（理由见 lastAppliedShape）
  applyPanelShapeStyle(effective?.panel_shape_style)

  // CSS 变量已经写完，通知已挂载的代码编辑器实例重新构建并换上主题
  if (typeof window !== 'undefined') {
    window.dispatchEvent(new CustomEvent(PANEL_APPEARANCE_CHANGE_EVENT))
  }
}

export async function fetchAndApplyPanelAppearance() {
  try {
    const settings = await loadPanelSettings()
    applyPanelAppearance(settings || null)
    // v4.0 主题引擎的全站默认值也在这一发里：更新首屏快照并重新应用一次（design §1.5）。
    // 拉取失败（settings 为 null）时什么都不做，继续用快照里上一次的全站值。
    if (settings) {
      applySiteThemeSettings(settings)
    }
  } catch {
    // ignore startup appearance load failures
  }
}

/**
 * 界面动效偏好（个人设置页「界面动效」、主题工坊「动效强度」，v4.0 起四档，design §2.1）。
 *
 * - system（默认）：系统开了「减少动态效果」→ off；否则低端设备 → subtle；否则 rich；
 * - rich：全部动效（光斑、烟花、磁吸、背景流动、View Transitions 弹簧切页）；
 * - subtle：只保留必要过渡（入场只淡入、切页只淡入淡出、背景静止，没有光斑 / 烟花 / 磁吸）；
 * - off：不看系统，一律把动效压到几乎不可见。
 *
 * 旧值兼容（v3.2.8 ~ v3.4.x 的三档）：always → rich、reduce → off。只在读取时换算，【不回写】存储 ——
 * 用户回滚到旧版时，旧版读到的仍是它认识的值。
 *
 * 落到 <html> 上的东西（styles/global.scss 末尾「减少动效」段与 styles/motion.scss 按它们分档）：
 *   data-dd-motion = 解析后的 rich / subtle / off；
 *   dd-motion-force：用户【显式】选了 rich 或 subtle。显式选择不看系统，所以要挡住 global.scss 里跟随系统的压缩；
 *                    （design 原文只写了 rich；subtle 也挂是因为「显式选克制」时系统开着减少动效，
 *                     不挂的话会被媒体查询压成 1ms，等于偷偷变成了关闭，与「显式档位原样生效」矛盾）
 *   dd-motion-off：解析结果是 off（显式关闭，或跟随系统且系统开着减少动效）；
 *   dd-vt：浏览器支持 View Transitions 且档位不是 off。切页动画交给 View Transitions，
 *          motion.scss 按它关掉 MainLayout 自带的 page-shell 进出场。
 *
 * 刻意做成本机偏好（localStorage），不进服务端的 system_configs：
 * 它回答的是「这台设备前的人想不想看动画」，和 panel_shape_style 那种「整个面板长什么样」不是一类；
 * 放进系统配置还会让 APP 的 schema 驱动设置页多出一个点了没用的开关。
 * 默认必须是 system：「丰富」会覆盖系统的无障碍偏好，只能由用户自己选。
 */
export type MotionPreference = 'system' | 'rich' | 'subtle' | 'off'

/** 解析后的动效档位（跟随系统已经换算掉） */
export type MotionLevel = 'rich' | 'subtle' | 'off'

// 命名沿用 dd:appearance:* 这一组（与圆角缓存键 dd:appearance:shape 同级）
const MOTION_PREFERENCE_STORAGE_KEY = 'dd:appearance:motion'
const MOTION_FORCE_CLASS = 'dd-motion-force'
const MOTION_OFF_CLASS = 'dd-motion-off'
const MOTION_VT_CLASS = 'dd-vt'

function normalizeMotionPreference(raw?: string | null): MotionPreference | null {
  const value = String(raw ?? '').trim().toLowerCase()
  if (value === 'system' || value === 'rich' || value === 'subtle' || value === 'off') return value
  // 旧三档的两个值：读取时换算，不回写（见上方说明）
  if (value === 'always') return 'rich'
  if (value === 'reduce') return 'off'
  return null
}

export function readMotionPreference(): MotionPreference {
  // 隐私模式 / 禁用站点存储时读 localStorage 会直接抛错；这里在 createApp 之前执行，不兜住会白屏
  try {
    if (typeof window === 'undefined') return 'system'
    return normalizeMotionPreference(window.localStorage.getItem(MOTION_PREFERENCE_STORAGE_KEY)) ?? 'system'
  } catch {
    return 'system'
  }
}

/**
 * 低端设备判定（design §2.1）：CPU 核数 ≤ 4 且内存 ≤ 4GB。
 * deviceMemory 只有 Chromium 系有，拿不到按 8GB 算（即不算低端）；hardwareConcurrency 拿不到同样不算低端 ——
 * 宁可漏判（多播一点动画），也不要把桌面 Firefox / Safari 全判成低端。
 * 命中且动效「跟随系统」时：动效降为 subtle；用户没设过玻璃模糊时模糊按 0（见 applyThemeAppearance）。
 */
export function isLowEndDevice(): boolean {
  if (typeof navigator === 'undefined') return false
  const cores = navigator.hardwareConcurrency
  const memory = (navigator as Navigator & { deviceMemory?: number }).deviceMemory ?? 8
  return typeof cores === 'number' && cores > 0 && cores <= 4 && memory <= 4
}

/**
 * 把偏好解析成实际档位。每次调用都现读系统设置：用户可能在页面开着的时候改系统设置，
 * localStorage 与 matchMedia 浏览器内部都有缓存，很便宜。
 */
export function resolveMotionLevel(preference: MotionPreference = readMotionPreference()): MotionLevel {
  if (preference !== 'system') return preference
  if (typeof window !== 'undefined' && (window.matchMedia?.('(prefers-reduced-motion: reduce)').matches ?? false)) {
    return 'off'
  }
  return isLowEndDevice() ? 'subtle' : 'rich'
}

/**
 * JS 层判断「此刻要不要减少动效」，给 CSS 令牌管不到的地方用（scrollTo 的 smooth、登录页插画跟随鼠标等）。
 * v4.0 起等价于「解析结果是 off」，与 dd-motion-off 类同一口径。
 * subtle 档返回 false（保留必要过渡）；只想在 rich 档才做的装饰效果请用 utils/motion.ts 的 motionLevel() === 'rich'。
 */
export function shouldReduceMotion(): boolean {
  return resolveMotionLevel() === 'off'
}

/**
 * 把动效偏好挂到 <html> 上，返回本机偏好（不是解析后的档位；要档位用 resolveMotionLevel()）。
 *
 * 传了合法值：以它为准并写进本机缓存（个人设置页 / 主题工坊切换时走这条，即时生效、无需刷新）；
 * 不传或值不认识：按本机缓存重放一遍（main.ts 在 createApp 之前、系统设置变化时走这条）。
 *
 * ⚠️ main.ts 里这一次必须在首帧之前同步执行：挂晚了，「丰富」的用户首屏动画会先被系统设置
 *    压成 1ms，「关闭」的用户则会先看到一遍完整动画 —— 与 applyPanelShapeStyle 防闪形同理。
 */
export function applyMotionPreference(raw?: string | null): MotionPreference {
  const explicit = normalizeMotionPreference(raw)
  const next = explicit ?? readMotionPreference()
  if (explicit) {
    try {
      window.localStorage.setItem(MOTION_PREFERENCE_STORAGE_KEY, explicit)
    } catch {
      // 写不进去（隐私模式）只是下次打开要重新选，不影响本次生效
    }
  }

  if (typeof document === 'undefined') return next
  const level = resolveMotionLevel(next)
  const root = document.documentElement
  root.setAttribute('data-dd-motion', level)
  root.classList.toggle(MOTION_FORCE_CLASS, next === 'rich' || next === 'subtle')
  root.classList.toggle(MOTION_OFF_CLASS, level === 'off')
  root.classList.toggle(MOTION_VT_CLASS, level !== 'off' && typeof document.startViewTransition === 'function')
  // 显式切换档位时，低端设备的「跟随系统 → 模糊归 0」可能随之变化，主题外观要重算一次。
  // 不传参的重放（main.ts 首屏）不重算：那时 applyThemeAppearance 紧跟着就会调。
  if (explicit) {
    applyThemeAppearance()
  }
  return next
}

/**
 * 面板主题档位（个人设置页「界面主题」，v3.3.2 / issue #145）。
 *
 * - light：显式明亮，不看系统；
 * - dark：显式暗夜，不看系统；
 * - system（缺键默认）：跟随操作系统的 prefers-color-scheme，系统日落转暗面板跟着转暗。
 *
 * 生效机制是给 <html> 加/去 class `dark`（不是 data-theme）：
 * element-plus/theme-chalk/dark/css-vars.css 与 global.scss、各页 <style> 里那些 `html.dark`
 * 段认的都是这一个 class，换成别的写法等于整套暗色样式全部失配。
 *
 * 与界面动效同理，刻意做成本机偏好（localStorage）、不进服务端：
 * 它回答的是「这块屏幕前的人现在想看亮的还是暗的」，笔记本和手机本就该各存各的；
 * 而且登录页也要能切主题，一旦挪进用户偏好表，未登录时根本读不到。
 *
 * ⚠️ 存储键沿用历史的裸 `theme`，不要改成 `dd:appearance:theme`：
 *    v3.3.2 之前的二态主题就写在这个键上，改键名等于把所有老用户的暗色偏好清零。
 * ⚠️ 缺键默认是 'system' 而不是 'light'：「跟随系统」这个功能要开箱即用，
 *    不能等用户先进一次设置页才生效。代价是「从没手动切过主题 + 系统是深色」的老用户
 *    升级后会被动变暗一次，这一点在发布说明里写明即可。
 */
export type ThemeMode = 'light' | 'dark' | 'system'

const THEME_MODE_STORAGE_KEY = 'theme'

function normalizeThemeMode(raw?: string | null): ThemeMode | null {
  const value = String(raw ?? '').trim().toLowerCase()
  if (value === 'light' || value === 'dark' || value === 'system') return value
  return null
}

/**
 * 系统此刻是不是深色。
 * 老 WebView 可能没有 matchMedia，可选链兜底成「不是深色」—— 拿不到就当系统永远是亮的，
 * 比整个启动流程抛异常白屏强。
 */
export function systemPrefersDark(): boolean {
  if (typeof window === 'undefined') return false
  return window.matchMedia?.('(prefers-color-scheme: dark)').matches ?? false
}

export function readThemeMode(): ThemeMode {
  // 隐私模式 / 禁用站点存储时读 localStorage 会直接抛错；这里在 createApp 之前执行，不兜住会白屏
  try {
    if (typeof window === 'undefined') return 'system'
    return normalizeThemeMode(window.localStorage.getItem(THEME_MODE_STORAGE_KEY)) ?? 'system'
  } catch {
    return 'system'
  }
}

/**
 * 把主题档位落到 <html> 的 class `dark` 上，返回最终生效的档位。
 *
 * 传了合法值：以它为准并写进本机缓存（个人设置页、顶栏按钮切换时走这条，即时生效、无需刷新）；
 * 不传或值不认识：按本机缓存重放一遍（main.ts 在 createApp 之前走这条）。
 *
 * ⚠️ main.ts 里这一次必须在首帧之前同步执行：挂晚了，暗色（含跟随系统落到暗色）的用户
 *    首屏会先闪一帧白底 —— 与 applyPanelShapeStyle 防闪形、applyMotionPreference 同理。
 * ⚠️ 这里【只】管 class 与持久化，不碰编辑器/日志底色：那一发是 applyPanelAppearance()，
 *    由 stores/theme.ts 的 watch(isDark) 触发，切明暗时两者缺一不可。
 */
export function applyThemeMode(raw?: string | null): ThemeMode {
  const explicit = normalizeThemeMode(raw)
  const next = explicit ?? readThemeMode()
  if (explicit) {
    try {
      window.localStorage.setItem(THEME_MODE_STORAGE_KEY, explicit)
    } catch {
      // 写不进去（隐私模式）只是下次打开要重新选，不影响本次生效
    }
  }

  if (typeof document === 'undefined') return next
  // 跟随系统档在这里现读一次 matchMedia：首屏预热时 store 还没建起来，没别人能替它算这一下
  document.documentElement.classList.toggle(
    'dark',
    next === 'dark' || (next === 'system' && systemPrefersDark()),
  )
  return next
}

// ============================================================================
// 主题引擎（v4.0，design §1）
// ----------------------------------------------------------------------------
// 两条独立的轴：
//   风格（预设）→ <html data-dd-theme="aurora">，本节管；
//   明暗        → <html class="dark">，仍由上面的 applyThemeMode 管，语义、存储键、顶栏按钮全部不变。
// 每一项外观都有「本机值」和「全站值」两份：本机设过以本机为准，没设过跟随全站（design §1.3）。
//   本机值：localStorage 的 dd:appearance:<key>（preset / accent / background / blur / radius / density；动效见上一节）；
//   全站值：GET /system/panel-settings 下发的 panel_* 字段，另在 dd:appearance:site 存一份快照给首屏用。
// 首屏：index.html 的内联脚本按同样的规则先写一遍 <html> 的属性与内联令牌；
//       main.ts 在 createApp 之前再同步调一次 applyThemeAppearance()；panel-settings 回来后以服务端值重算。
// 🔴 index.html 里那段脚本是这里的「手抄版」：改了下面的键名、默认值、优先级，必须同步改它。
// ============================================================================

export type ThemePreset = 'classic' | 'aurora' | 'mist' | 'terminal' | 'paper' | 'daidai' | 'clean'
export type BackgroundEffect = 'theme' | 'plain' | 'aurora' | 'grid' | 'image'
export type PanelDensity = 'compact' | 'cozy' | 'roomy'

/** 预设风格的元信息（主题工坊的预设卡片、滑杆默认值用）。颜色只是预览色块，真正的令牌在 styles/themes/*.scss */
export interface ThemePresetMeta {
  id: ThemePreset
  name: string
  desc: string
  /** 主题默认的玻璃模糊（px），与 scss 里的 --dd-glass-blur 一致；0 = 不透明卡片 */
  blur: number
  /** 主题默认的卡片圆角（px），即 --dd-theme-radius-surface；主题工坊圆角滑杆没设过时显示它 */
  radius: number
  /** 「跟随主题」背景下有没有极光块（经典、纸墨没有）；AppBackdrop 据此决定要不要渲染那 4 个大模糊层 */
  blobs: boolean
  swatch: Record<'light' | 'dark', { bg: string; surface: string; accent: string; accent2: string }>
}

export const THEME_PRESETS: ThemePresetMeta[] = [
  {
    id: 'classic', name: '经典', desc: '干净利落的蓝白，最接近旧版', blur: 0, radius: 14, blobs: false,
    swatch: {
      light: { bg: '#f2f4f8', surface: '#ffffff', accent: '#2f6bff', accent2: '#22b8f0' },
      dark: { bg: '#0b1120', surface: '#121a2e', accent: '#4d84ff', accent2: '#38bdf8' },
    },
  },
  {
    id: 'aurora', name: '极光', desc: '流动的紫青极光与玻璃卡片', blur: 22, radius: 18, blobs: true,
    swatch: {
      light: { bg: '#f5f3ff', surface: '#fcfbff', accent: '#7c3aed', accent2: '#0891b2' },
      dark: { bg: '#060814', surface: '#0e1122', accent: '#8b5cf6', accent2: '#22d3ee' },
    },
  },
  {
    id: 'mist', name: '晨雾', desc: '马卡龙色雾气，柔和的大圆角', blur: 26, radius: 22, blobs: true,
    swatch: {
      light: { bg: '#eef1f8', surface: '#f8f9fc', accent: '#5b5bf6', accent2: '#ec4899' },
      dark: { bg: '#0f1226', surface: '#181c38', accent: '#8b8cff', accent2: '#f472b6' },
    },
  },
  {
    id: 'terminal', name: '终端', desc: '等宽字、扫描线、荧光绿', blur: 4, radius: 3, blobs: true,
    swatch: {
      light: { bg: '#f3f1e7', surface: '#fbfaf3', accent: '#0f7a3e', accent2: '#0e7490' },
      dark: { bg: '#030705', surface: '#050c08', accent: '#3cff8f', accent2: '#00e5ff' },
    },
  },
  {
    id: 'paper', name: '纸墨', desc: '米色纸纹、宋体标题、朱砂点缀', blur: 0, radius: 6, blobs: false,
    swatch: {
      light: { bg: '#f1eadd', surface: '#fbf8f1', accent: '#b4441c', accent2: '#2f5d50' },
      dark: { bg: '#16120e', surface: '#1f1a15', accent: '#e0653a', accent2: '#6fae98' },
    },
  },
  {
    id: 'daidai', name: '呆呆', desc: '粗描边、硬阴影，小人趴在侧栏', blur: 0, radius: 18, blobs: true,
    swatch: {
      light: { bg: '#fff6df', surface: '#ffffff', accent: '#1e7bff', accent2: '#ff8a1f' },
      dark: { bg: '#17171a', surface: '#222226', accent: '#4d9bff', accent2: '#ffa24a' },
    },
  },
  {
    // v4.0 Q4 追加：iOS 26 式留白。除颜色外还覆盖一组排版级令牌（styles/themes/_layout.scss 的默认值，clean.scss 覆盖）
    id: 'clean', name: '素白', desc: 'iOS 26 式留白、黑色胶囊、玻璃侧栏', blur: 0, radius: 26, blobs: false,
    swatch: {
      light: { bg: '#f2f2f7', surface: '#ffffff', accent: '#111111', accent2: '#007aff' },
      dark: { bg: '#000000', surface: '#1c1c1e', accent: '#ffffff', accent2: '#0a84ff' },
    },
  },
]

/** 全站默认风格（与服务端注册表 panel_theme_preset 的默认值一致；演示站同样是极光） */
export const DEFAULT_THEME_PRESET: ThemePreset = 'aurora'

const THEME_PRESET_IDS = THEME_PRESETS.map((item) => item.id)
const BACKGROUND_EFFECTS: BackgroundEffect[] = ['theme', 'plain', 'aurora', 'grid', 'image']
const PANEL_DENSITIES: PanelDensity[] = ['compact', 'cozy', 'roomy']
const ACCENT_PATTERN = /^#[0-9a-f]{6}$/

/** 本机覆盖项。null = 没设过，跟随全站 */
export interface LocalAppearance {
  preset: ThemePreset | null
  /** #rrggbb */
  accent: string | null
  background: BackgroundEffect | null
  /** 玻璃模糊 0~40（px） */
  blur: number | null
  /** 圆角滑杆 0~28（px），有值时压过全站的 panel_shape_style */
  radius: number | null
  density: PanelDensity | null
}

export type LocalAppearanceKey = keyof LocalAppearance

/** 全站默认值（panel-settings 规整后的结果） */
export interface SiteAppearance {
  preset: ThemePreset
  /** '' = 跟随主题 */
  accent: string
  background: BackgroundEffect
  /** data URL；'' = 没有 */
  backgroundImage: string
  /** null = 跟随主题 */
  blur: number | null
  density: PanelDensity
  shape: PanelShapeStyle
  customCss: string
}

/** 本机值与全站值合并后的生效值 */
export interface ResolvedAppearance {
  preset: ThemePreset
  /** '' = 用主题自带的主色 */
  accent: string
  background: BackgroundEffect
  backgroundImage: string
  /** 生效的玻璃模糊（px），已含「低端设备 + 动效跟随系统 → 0」的降级 */
  blur: number
  /** 本机圆角滑杆；null = 按全站 shape 走 */
  radius: number | null
  shape: PanelShapeStyle
  density: PanelDensity
  motion: MotionLevel
}

const LOCAL_APPEARANCE_KEYS: LocalAppearanceKey[] = ['preset', 'accent', 'background', 'blur', 'radius', 'density']
const LOCAL_APPEARANCE_PREFIX = 'dd:appearance:'
/** 全站值快照（JSON，只存首屏要用的字段；背景图可能有几 MB，不进快照，等接口回来再画） */
const SITE_SNAPSHOT_STORAGE_KEY = 'dd:appearance:site'

/** 全站值变化事件（panel-settings 回来、管理员「设为全站默认」后）。stores/theme.ts 靠它同步响应式状态 */
export const THEME_SITE_CHANGE_EVENT = 'dd:theme-site-change'

function normalizeThemePreset(raw?: unknown): ThemePreset | null {
  const value = String(raw ?? '').trim().toLowerCase()
  return (THEME_PRESET_IDS as string[]).includes(value) ? (value as ThemePreset) : null
}

function normalizeBackgroundEffect(raw?: unknown): BackgroundEffect | null {
  const value = String(raw ?? '').trim().toLowerCase()
  return (BACKGROUND_EFFECTS as string[]).includes(value) ? (value as BackgroundEffect) : null
}

function normalizeDensity(raw?: unknown): PanelDensity | null {
  const value = String(raw ?? '').trim().toLowerCase()
  return (PANEL_DENSITIES as string[]).includes(value) ? (value as PanelDensity) : null
}

function normalizeAccent(raw?: unknown): string | null {
  const value = String(raw ?? '').trim().toLowerCase()
  return ACCENT_PATTERN.test(value) ? value : null
}

// 整数且在 [min, max] 内才算数；空串、小数、越界一律当没设过
function normalizeIntInRange(raw: unknown, min: number, max: number): number | null {
  const text = String(raw ?? '').trim()
  if (!/^\d{1,3}$/.test(text)) return null
  const value = Number(text)
  return value >= min && value <= max ? value : null
}

/** 读本机覆盖项。隐私模式下 localStorage 会抛错，按「全都没设过」处理 */
export function readLocalAppearance(): LocalAppearance {
  const read = (key: LocalAppearanceKey) => {
    try {
      return typeof window === 'undefined' ? null : window.localStorage.getItem(LOCAL_APPEARANCE_PREFIX + key)
    } catch {
      return null
    }
  }
  return {
    preset: normalizeThemePreset(read('preset')),
    accent: normalizeAccent(read('accent')),
    background: normalizeBackgroundEffect(read('background')),
    blur: normalizeIntInRange(read('blur'), 0, 40),
    radius: normalizeIntInRange(read('radius'), 0, 28),
    density: normalizeDensity(read('density')),
  }
}

/**
 * 写一项本机覆盖。value 为 null（或不合法）= 删掉这一项，回到「跟随全站」。
 * 只写存储，不应用；调用方写完自己调 applyThemeAppearance()（stores/theme.ts 的 setXxx 已经包好）。
 */
export function writeLocalAppearance(key: LocalAppearanceKey, value: string | number | null) {
  const normalized = value === null
    ? null
    : key === 'preset' ? normalizeThemePreset(value)
      : key === 'accent' ? normalizeAccent(value)
        : key === 'background' ? normalizeBackgroundEffect(value)
          : key === 'density' ? normalizeDensity(value)
            : normalizeIntInRange(value, 0, key === 'blur' ? 40 : 28)
  try {
    if (normalized === null) {
      window.localStorage.removeItem(LOCAL_APPEARANCE_PREFIX + key)
    } else {
      window.localStorage.setItem(LOCAL_APPEARANCE_PREFIX + key, String(normalized))
    }
  } catch {
    // 隐私模式：写不进去只是刷新后不记得，本次仍由调用方当场应用
  }
}

/** 清掉本机覆盖项；不传 key = 全部清掉（主题工坊「全部恢复为全站默认」） */
export function clearLocalAppearance(key?: LocalAppearanceKey) {
  for (const item of key ? [key] : LOCAL_APPEARANCE_KEYS) {
    writeLocalAppearance(item, null)
  }
}

function normalizeSiteAppearance(payload: Partial<PanelSettingsPayload> | null | undefined): SiteAppearance {
  return {
    preset: normalizeThemePreset(payload?.panel_theme_preset) ?? DEFAULT_THEME_PRESET,
    accent: normalizeAccent(payload?.panel_accent_color) ?? '',
    background: normalizeBackgroundEffect(payload?.panel_background_effect) ?? 'theme',
    backgroundImage: String(payload?.panel_background_image ?? '').trim(),
    blur: normalizeIntInRange(payload?.panel_glass_blur, 0, 40),
    density: normalizeDensity(payload?.panel_density) ?? 'cozy',
    shape: normalizePanelShape(payload?.panel_shape_style) ?? DEFAULT_PANEL_SHAPE,
    customCss: String(payload?.panel_custom_css ?? ''),
  }
}

function readSiteSnapshot(): SiteAppearance {
  try {
    const raw = typeof window === 'undefined' ? null : window.localStorage.getItem(SITE_SNAPSHOT_STORAGE_KEY)
    return normalizeSiteAppearance(raw ? JSON.parse(raw) : null)
  } catch {
    return normalizeSiteAppearance(null)
  }
}

// 当前的全站值。模块加载时先取快照，panel-settings 回来后由 applySiteThemeSettings 覆盖
let siteAppearance: SiteAppearance = readSiteSnapshot()

export function getSiteAppearance(): SiteAppearance {
  return siteAppearance
}

/**
 * 应用服务端下发的全站值：更新内存值与首屏快照，重新应用外观，并派发 THEME_SITE_CHANGE_EVENT。
 * 调用方：fetchAndApplyPanelAppearance()（启动时）、stores/theme.ts 的 applySiteSettings()（管理员保存全站默认后）。
 * 传的是部分字段时，没传的字段沿用当前全站值（不会被拍回默认）。
 */
export function applySiteThemeSettings(payload: Partial<PanelSettingsPayload>) {
  const current: PanelSettingsPayload = {
    panel_theme_preset: siteAppearance.preset,
    panel_accent_color: siteAppearance.accent,
    panel_background_effect: siteAppearance.background,
    panel_background_image: siteAppearance.backgroundImage,
    panel_glass_blur: siteAppearance.blur === null ? '' : String(siteAppearance.blur),
    panel_density: siteAppearance.density,
    panel_shape_style: siteAppearance.shape,
    panel_custom_css: siteAppearance.customCss,
  }
  const merged = { ...current }
  for (const [key, value] of Object.entries(payload)) {
    if (key in current && value !== undefined && value !== null) {
      (merged as Record<string, unknown>)[key] = value
    }
  }
  siteAppearance = normalizeSiteAppearance(merged)
  try {
    // 快照里不放背景图（见 SITE_SNAPSHOT_STORAGE_KEY）；自定义 CSS ≤32KB，放进去首屏就能注入
    const { panel_background_image: _image, ...snapshot } = merged
    window.localStorage.setItem(SITE_SNAPSHOT_STORAGE_KEY, JSON.stringify(snapshot))
  } catch {
    // 隐私模式 / 配额满：只是下次首屏要等接口回来才对，不影响本次
  }
  // panel_shape_style 也随之更新圆角（applyPanelShapeStyle 会记进 dd:appearance:shape 缓存）
  applyPanelShapeStyle(siteAppearance.shape)
  applyThemeAppearance()
  if (typeof window !== 'undefined') {
    window.dispatchEvent(new CustomEvent(THEME_SITE_CHANGE_EVENT))
  }
}

/** 合并本机值与全站值（不碰 DOM）。主题工坊显示「当前生效值」时直接用它 */
export function resolveAppearance(local: LocalAppearance = readLocalAppearance(), site: SiteAppearance = siteAppearance): ResolvedAppearance {
  const preset = local.preset ?? site.preset
  const meta = THEME_PRESETS.find((item) => item.id === preset)
  const motionPreference = readMotionPreference()
  // 玻璃模糊优先级：本机 > 全站 > （低端设备且动效跟随系统 → 0）> 主题默认
  const lowEndFallback = motionPreference === 'system' && isLowEndDevice() ? 0 : null
  return {
    preset,
    accent: local.accent ?? site.accent,
    background: local.background ?? site.background,
    backgroundImage: site.backgroundImage,
    blur: local.blur ?? site.blur ?? lowEndFallback ?? meta?.blur ?? 0,
    radius: local.radius,
    shape: lastAppliedShape,
    density: local.density ?? site.density,
    motion: resolveMotionLevel(motionPreference),
  }
}

/**
 * 把生效的主题外观写到 <html> 上，返回生效值。幂等，任何一项变了都可以整体重跑。
 *   data-dd-theme / data-dd-bg / data-dd-density / data-dd-glass 四个属性；
 *   内联令牌：自定义主色 --dd-accent（+ 按亮度算出的 --dd-on-accent）、玻璃模糊 --dd-glass-blur；
 *   圆角走 applyPanelShapeStyle()（它会顺带读本机滑杆）；自定义 CSS 走 applyCustomCss()。
 * 明暗（.dark）不在这里：那是 applyThemeMode 的事。
 */
export function applyThemeAppearance(): ResolvedAppearance {
  const resolved = resolveAppearance()
  if (typeof document === 'undefined') return resolved
  const root = document.documentElement
  root.setAttribute('data-dd-theme', resolved.preset)
  root.setAttribute('data-dd-bg', resolved.background)
  root.setAttribute('data-dd-density', resolved.density)
  root.setAttribute('data-dd-glass', resolved.blur === 0 ? 'off' : 'on')

  if (resolved.accent) {
    root.style.setProperty('--dd-accent', resolved.accent)
    // 主色上的文字色：白字与近黑字谁的对比度高用谁（WCAG 相对亮度），
    // 否则用户挑一个亮黄当主色时，主按钮上的白字就看不见了
    const rgb = parseColor(resolved.accent)
    const toLinear = (channel: number) => {
      const value = channel / 255
      return value <= 0.03928 ? value / 12.92 : ((value + 0.055) / 1.055) ** 2.4
    }
    const luminance = rgb ? 0.2126 * toLinear(rgb.r) + 0.7152 * toLinear(rgb.g) + 0.0722 * toLinear(rgb.b) : 0
    const whiteContrast = 1.05 / (luminance + 0.05)
    const darkContrast = (luminance + 0.05) / 0.0556
    root.style.setProperty('--dd-on-accent', whiteContrast >= darkContrast ? '#ffffff' : '#111111')
  } else {
    root.style.removeProperty('--dd-accent')
    root.style.removeProperty('--dd-on-accent')
  }

  // 只有「本机 / 全站 / 低端降级」给了值才写内联；否则移除，露出主题块里的默认值
  const local = readLocalAppearance()
  const explicitBlur = local.blur ?? siteAppearance.blur
  if (explicitBlur !== null || resolved.blur === 0) {
    root.style.setProperty('--dd-glass-blur', `${resolved.blur}px`)
  } else {
    root.style.removeProperty('--dd-glass-blur')
  }

  applyPanelShapeStyle()
  applyCustomCss()
  return resolved
}

// ----------------------------------------------------------------------------
// 自定义 CSS（design §1.6）：只有全站一份（panel_custom_css），登录页也生效。
// 注入为 <head> 最后一个 <style id="dd-custom-css">；安全模式下跳过。
// ----------------------------------------------------------------------------

const CUSTOM_CSS_ELEMENT_ID = 'dd-custom-css'
const SAFE_MODE_STORAGE_KEY = 'dd:safe-mode'

// 主题工坊编辑时的实时预览（不落库）。null = 不在预览，用全站值
let customCssPreview: string | null = null
// 让 <style id="dd-custom-css"> 一直待在 <head> 末尾的监听，见 applyCustomCss
let customCssKeeper: MutationObserver | null = null
// 本页的安全模式状态。null = 还没判断过（第一次调用 isSafeMode 时按地址与 sessionStorage 算）
let safeModeOn: boolean | null = null

/**
 * 是否处于安全模式（跳过自定义 CSS）。
 * 地址带 ?safe=1 时进入，并记进 sessionStorage：站内切页地址上没有这个参数了也仍然有效，关掉标签页自然失效。
 * 只在本页第一次调用时看地址（main.ts 首屏那次）：之后地址上即使还挂着 ?safe=1，「退出安全模式」也要立刻生效。
 */
export function isSafeMode(): boolean {
  if (typeof window === 'undefined') return false
  if (safeModeOn !== null) return safeModeOn
  const fromUrl = new URLSearchParams(window.location.search).get('safe') === '1'
  try {
    if (fromUrl) {
      window.sessionStorage.setItem(SAFE_MODE_STORAGE_KEY, '1')
    }
    safeModeOn = fromUrl || window.sessionStorage.getItem(SAFE_MODE_STORAGE_KEY) === '1'
  } catch {
    // sessionStorage 不可用时只认地址参数（站内切页后失效，可以接受）
    safeModeOn = fromUrl
  }
  return safeModeOn
}

/** 退出安全模式并立即注入自定义 CSS。地址上的 ?safe=1 由调用方去掉（App.vue 用 router.replace），否则刷新又会进来 */
export function exitSafeMode() {
  safeModeOn = false
  try {
    window.sessionStorage.removeItem(SAFE_MODE_STORAGE_KEY)
  } catch {
    // ignore
  }
  applyCustomCss()
}

/** 主题工坊的实时预览：传字符串 = 预览这段 CSS；传 null = 结束预览，回到全站值 */
export function setCustomCssPreview(css: string | null) {
  customCssPreview = css
  applyCustomCss()
}

/**
 * 注入 / 更新 / 移除自定义 CSS。幂等。
 *
 * 为什么要盯着 <head> 末尾：同特异性下后出现的样式表赢。EP 组件样式是随懒加载 chunk 追加到 <head> 的，
 * 页面开着的时候还会不断有新的 <style> / <link> 进来，一旦排到自定义 CSS 后面，管理员写的覆盖就会悄悄失效。
 * 所以挂一个只看 <head> 直接子节点增删的 MutationObserver，发现自己不是最后一个就挪回末尾
 * （挪动本身会再触发一次回调，那时已经是最后一个，不会循环）。
 */
export function applyCustomCss() {
  if (typeof document === 'undefined') return
  const css = isSafeMode() ? '' : (customCssPreview ?? siteAppearance.customCss)
  let element = document.getElementById(CUSTOM_CSS_ELEMENT_ID) as HTMLStyleElement | null
  if (!css.trim()) {
    element?.remove()
    customCssKeeper?.disconnect()
    customCssKeeper = null
    return
  }

  if (!element) {
    element = document.createElement('style')
    element.id = CUSTOM_CSS_ELEMENT_ID
  }
  if (element.textContent !== css) {
    element.textContent = css
  }
  if (document.head.lastElementChild !== element) {
    document.head.appendChild(element)
  }
  if (!customCssKeeper && typeof MutationObserver !== 'undefined') {
    customCssKeeper = new MutationObserver(() => {
      const current = document.getElementById(CUSTOM_CSS_ELEMENT_ID)
      if (current && document.head.lastElementChild !== current) {
        document.head.appendChild(current)
      }
    })
    customCssKeeper.observe(document.head, { childList: true })
  }
}
