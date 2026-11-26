import { defineStore } from 'pinia'
import { computed, ref, watch } from 'vue'
import { toast } from '@/utils/toast'
import {
  applyLitePreference,
  applyMotionPreference,
  applyPanelAppearance,
  applySiteThemeSettings,
  applyThemeAppearance,
  applyThemeMode,
  clearLocalAppearance,
  exitSafeMode as exitSafeModeAndInject,
  getSiteAppearance,
  isSafeMode,
  LITE_PREFERENCE_STORAGE_KEY,
  readLitePreference,
  readLocalAppearance,
  readMotionPreference,
  readThemeMode,
  resolveAppearance,
  scheduleLiteAutoDetect,
  setCustomCssPreview,
  systemPrefersDark,
  THEME_PRESETS,
  THEME_SITE_CHANGE_EVENT,
  writeLocalAppearance,
  type BackgroundEffect,
  type LocalAppearance,
  type LitePreference,
  type LocalAppearanceKey,
  type MotionPreference,
  type PanelDensity,
  type SiteAppearance,
  type ThemeMode,
  type ThemePreset,
} from '@/utils/panelAppearance'
import type { PanelSettingsPayload } from '@/utils/panelSettings'
import { withThemeTransition, type TransitionOrigin } from '@/utils/motion'

/** 主题导出 / 导入的 JSON 结构（design §5 F3）。null = 跟随（全站 / 主题） */
export interface ThemeExportPayload {
  version: 1
  preset: ThemePreset
  accent: string | null
  background: BackgroundEffect
  blur: number | null
  radius: number | null
  density: PanelDensity
  motion: MotionPreference
  /** 只有管理员导出时才带；导入时非管理员忽略 */
  customCSS?: string
}

export type ImportThemeResult =
  | { ok: true; customCss: string | null }
  | { ok: false; error: string }

// 与服务端 panel_custom_css 的长度上限一致（design §1.6）
const CUSTOM_CSS_MAX_BYTES = 32768

export const useThemeStore = defineStore('theme', () => {
  // v3.3.2（issue #145）起主题是三档：明亮 / 暗夜 / 跟随系统。
  // 持久化仍落在 localStorage 的裸 `theme` 键上，读写全部收敛到 utils/panelAppearance.ts，
  // 这个 store 只负责把「档位 + 系统当前明暗」合成对外的 isDark。
  const mode = ref<ThemeMode>(readThemeMode())
  const systemDark = ref(systemPrefersDark())

  // change 监听挂在 store 工厂体内而【不是】 onMounted：Pinia 的 setup 工厂不在组件上下文里，
  // onMounted 根本不会执行；而且这个 store 可能在组件之外（路由守卫、工具函数）先被用到。
  // 监听跟着 store 活一辈子，不需要摘 —— store 本身就是整页生命周期的单例。
  if (typeof window !== 'undefined') {
    window.matchMedia?.('(prefers-color-scheme: dark)')?.addEventListener('change', (event) => {
      systemDark.value = event.matches
    })
  }

  // 对外的只读出口，语义与名字都不变：CodeMirror / Monaco / 图表 / applyPanelAppearance
  // 全都依赖它，三档改造对它们必须完全无感。
  // v4.0：isDark 仍【只】由明暗档位决定，与风格（preset）无关 —— 纸墨亮色、终端暗色都按各自的明暗走。
  const isDark = computed(() =>
    mode.value === 'system' ? systemDark.value : mode.value === 'dark'
  )

  function setThemeMode(next: ThemeMode) {
    // applyThemeMode 会写 localStorage 并立即改 <html> 上的 class，全站即时生效、无需刷新
    mode.value = applyThemeMode(next)
  }

  // 顶栏与登录页那颗图标按钮的语义：点一下 = 脱离「跟随系统」，落到与当前观感相反的显式档位。
  // 第三档「跟随系统」只在个人设置页能选回来，避免顶栏一颗按钮转三态、误触后不知道自己在哪一档。
  function toggleTheme() {
    setThemeMode(isDark.value ? 'light' : 'dark')
  }

  watch(isDark, (val) => {
    document.documentElement.classList.toggle('dark', val)
    // ⚠️ 这里【不】写 localStorage：三档的持久化归 applyThemeMode 管。
    //    v3.3.2 之前这行是 setItem('theme', val ? 'dark' : 'light')，留着的话「跟随系统」
    //    会在首次 watch（immediate）时就被自解成 dark/light 写回存储，用户下次打开便不再跟随。
    // 保留这条 watch 的唯一理由是 applyPanelAppearance()：它是重推编辑器/日志底色默认值、
    // 并派发 PANEL_APPEARANCE_CHANGE_EVENT 让已挂载的编辑器换主题的唯一触发点。
    applyPanelAppearance()
  }, { immediate: true })

  // ===================== v4.0 主题引擎（design §1.3、§2.1） =====================
  // 真正的读写、优先级合并、写 DOM 都在 utils/panelAppearance.ts；这里只是它的响应式镜像 + 带过渡的 setter。
  // 本机值（local）null = 没设过；全站值（site）来自 panel-settings；生效值（resolved）= 本机 ?? 全站 ?? 主题默认。
  const local = ref<LocalAppearance>(readLocalAppearance())
  const site = ref<SiteAppearance>(getSiteAppearance())
  const motion = ref<MotionPreference>(readMotionPreference())
  // 系统「减少动态效果」开关变化时 +1，让依赖动效档位的 computed 重新求值（档位读的是 matchMedia，不是响应式的）
  const motionRevision = ref(0)
  const safeMode = ref(isSafeMode())
  // 流畅模式本机偏好（v4.0.2）：on / off / auto；null = 从没设过（会做一次自动检测，见下方）
  const litePreference = ref<LitePreference | null>(readLitePreference())
  /** 流畅模式此刻是否生效（手动或自动打开） */
  const lite = computed(() => litePreference.value === 'on' || litePreference.value === 'auto')

  const resolved = computed(() => {
    // 下面三行只为建立响应式依赖：resolveAppearance 内部还会现读 matchMedia / 低端设备判定 / 流畅模式
    void motion.value
    void motionRevision.value
    void litePreference.value
    return resolveAppearance(local.value, site.value)
  })

  /** 生效的风格预设 */
  const preset = computed(() => resolved.value.preset)
  /** 生效的主色；'' = 用主题自带的主色 */
  const accent = computed(() => resolved.value.accent)
  const background = computed(() => resolved.value.background)
  /** 全站背景图（data URL），只有背景效果为 image 时才画 */
  const backgroundImage = computed(() => resolved.value.backgroundImage)
  /** 卡片的玻璃模糊（px）：显式设过的值，没设过为 0（实色卡片）。流畅模式下不生效但仍显示设定值 */
  const blur = computed(() => resolved.value.blur)
  /** 本机圆角滑杆；null = 按全站 panel_shape_style */
  const radius = computed(() => resolved.value.radius)
  const density = computed(() => resolved.value.density)
  /** 解析后的动效档位 rich / subtle / off */
  const motionLevel = computed(() => resolved.value.motion)
  /** 当前预设的元信息（预设卡片、滑杆默认值用） */
  const presetMeta = computed(() => THEME_PRESETS.find((item) => item.id === preset.value) ?? THEME_PRESETS[0]!)

  /** 这一项是不是本机设过（主题工坊每项右侧的「本机 / 跟随全站」） */
  function isLocal(key: LocalAppearanceKey) {
    return local.value[key] !== null
  }

  function refreshLocal() {
    local.value = readLocalAppearance()
  }

  // 写一项本机值并整体重新应用（applyThemeAppearance 是幂等的）
  function commitLocal(key: LocalAppearanceKey, value: string | number | null) {
    writeLocalAppearance(key, value)
    refreshLocal()
    applyThemeAppearance()
  }

  /** 切风格。传 null = 回到跟随全站。origin 传点击事件或坐标，从那里圆形扩散 */
  function setPreset(next: ThemePreset | null, origin?: TransitionOrigin | null) {
    withThemeTransition(() => commitLocal('preset', next), origin)
  }

  /** 改主色（#rrggbb）。传 null 或 '' = 回到跟随全站（全站也没设就是主题色） */
  function setAccent(next: string | null, origin?: TransitionOrigin | null) {
    withThemeTransition(() => commitLocal('accent', next || null), origin)
  }

  /** 背景效果。传 null = 跟随全站 */
  function setBackground(next: BackgroundEffect | null, origin?: TransitionOrigin | null) {
    withThemeTransition(() => commitLocal('background', next), origin)
  }

  /** 玻璃模糊 0~40。滑杆连续触发，不走过渡。传 null = 跟随全站 / 主题 */
  function setBlur(next: number | null) {
    commitLocal('blur', next === null ? null : Math.round(next))
  }

  /** 圆角滑杆 0~28，压过全站的 panel_shape_style。传 null = 回到全站的圆角风格 */
  function setRadius(next: number | null) {
    commitLocal('radius', next === null ? null : Math.round(next))
  }

  /** 界面密度。传 null = 跟随全站 */
  function setDensity(next: PanelDensity | null) {
    commitLocal('density', next)
  }

  /** 动效档位（本机偏好，沿用 dd:appearance:motion 键）。会同步写 data-dd-motion 与两个兼容类 */
  function setMotion(next: MotionPreference) {
    motion.value = applyMotionPreference(next)
  }

  /**
   * 流畅模式开关（个人设置、主题工坊、命令面板共用）。手动关闭记为 off，之后永不自动打开。
   * 会同步重算 data-dd-lite / data-dd-glass / data-dd-motion（见 utils/panelAppearance.ts 的 applyLitePreference）。
   */
  function setLite(enabled: boolean) {
    litePreference.value = applyLitePreference(enabled ? 'on' : 'off')
  }

  /** 清掉本机覆盖：传 key 只清这一项，不传 = 全部（不含动效，动效没有全站值） */
  function resetLocal(key?: LocalAppearanceKey) {
    clearLocalAppearance(key)
    refreshLocal()
    applyThemeAppearance()
  }

  /**
   * 应用全站值（管理员「设为全站默认」保存成功后调用，传刚保存的那几个 panel_* 键即可，没传的保持不变）。
   * 会更新首屏快照并派发 THEME_SITE_CHANGE_EVENT。
   */
  function applySiteSettings(payload: Partial<PanelSettingsPayload>) {
    applySiteThemeSettings(payload)
    site.value = getSiteAppearance()
  }

  /**
   * 把「当前生效的外观」换成全站配置键值对（管理员「设为全站默认」用）。
   * 调用方拿去 POST /configs（或 configApi.batchSet），成功后调 applySiteSettings(同一份) + resetLocal()。
   * 圆角滑杆没有对应的全站键（全站只有 panel_shape_style 三档），不在其中；
   * 自定义 CSS 也不在其中，管理员单独保存。
   */
  function buildSiteDefaultConfigs(): Record<string, string> {
    const current = resolved.value
    const explicitBlur = local.value.blur ?? site.value.blur
    return {
      panel_theme_preset: current.preset,
      panel_accent_color: current.accent,
      panel_background_effect: current.background,
      panel_glass_blur: explicitBlur === null ? '' : String(explicitBlur),
      panel_density: current.density,
    }
  }

  /** 导出当前外观为 JSON 字符串。includeCustomCss 只给管理员传 true */
  function exportTheme(options?: { includeCustomCss?: boolean }): string {
    const current = resolved.value
    const payload: ThemeExportPayload = {
      version: 1,
      preset: current.preset,
      accent: current.accent || null,
      background: current.background,
      // 模糊与圆角只导出「明确设过」的值：导出主题默认值的话，导入后会被钉死，换风格也不跟着变
      blur: local.value.blur ?? site.value.blur,
      radius: local.value.radius,
      density: current.density,
      motion: motion.value,
    }
    if (options?.includeCustomCss) {
      payload.customCSS = site.value.customCss
    }
    return JSON.stringify(payload, null, 2)
  }

  /**
   * 导入外观 JSON：逐字段校验，任何一项不合法就整体拒绝（返回中文原因），合法则全部写成本机值并应用。
   * 自定义 CSS 不在这里落地：allowCustomCss（管理员）时原样返回给调用方，由主题工坊决定预览 / 保存为全站值；
   * 非管理员导入时直接忽略该字段。
   */
  function importTheme(json: string, options?: { allowCustomCss?: boolean }): ImportThemeResult {
    let data: unknown
    try {
      data = JSON.parse(json)
    } catch {
      return { ok: false, error: '不是合法的 JSON' }
    }
    if (!data || typeof data !== 'object' || Array.isArray(data)) {
      return { ok: false, error: '内容不是主题对象' }
    }
    const input = data as Record<string, unknown>
    if (input.version !== 1) {
      return { ok: false, error: '不支持的主题版本（只认 version: 1）' }
    }
    if (!THEME_PRESETS.some((item) => item.id === input.preset)) {
      return { ok: false, error: 'preset 不是已知的风格' }
    }
    const isEmpty = (value: unknown) => value === null || value === undefined || value === ''
    const isIntIn = (value: unknown, min: number, max: number) =>
      typeof value === 'number' && Number.isInteger(value) && value >= min && value <= max
    if (!isEmpty(input.accent) && !(typeof input.accent === 'string' && /^#[0-9a-fA-F]{6}$/.test(input.accent))) {
      return { ok: false, error: 'accent 必须是 #rrggbb 或留空' }
    }
    if (!isEmpty(input.background) && !['theme', 'plain', 'aurora', 'grid', 'image'].includes(String(input.background))) {
      return { ok: false, error: 'background 取值不对' }
    }
    if (!isEmpty(input.blur) && !isIntIn(input.blur, 0, 40)) {
      return { ok: false, error: 'blur 必须是 0~40 的整数' }
    }
    if (!isEmpty(input.radius) && !isIntIn(input.radius, 0, 28)) {
      return { ok: false, error: 'radius 必须是 0~28 的整数' }
    }
    if (!isEmpty(input.density) && !['compact', 'cozy', 'roomy'].includes(String(input.density))) {
      return { ok: false, error: 'density 取值不对' }
    }
    if (!isEmpty(input.motion) && !['system', 'rich', 'subtle', 'off'].includes(String(input.motion))) {
      return { ok: false, error: 'motion 取值不对' }
    }
    let customCss: string | null = null
    if (options?.allowCustomCss && !isEmpty(input.customCSS)) {
      if (typeof input.customCSS !== 'string') {
        return { ok: false, error: 'customCSS 必须是字符串' }
      }
      if (new TextEncoder().encode(input.customCSS).length > CUSTOM_CSS_MAX_BYTES) {
        return { ok: false, error: '自定义 CSS 超过 32KB' }
      }
      customCss = input.customCSS
    }

    withThemeTransition(() => {
      writeLocalAppearance('preset', input.preset as ThemePreset)
      writeLocalAppearance('accent', isEmpty(input.accent) ? null : String(input.accent).toLowerCase())
      writeLocalAppearance('background', isEmpty(input.background) ? null : String(input.background))
      writeLocalAppearance('blur', isEmpty(input.blur) ? null : (input.blur as number))
      writeLocalAppearance('radius', isEmpty(input.radius) ? null : (input.radius as number))
      writeLocalAppearance('density', isEmpty(input.density) ? null : String(input.density))
      if (!isEmpty(input.motion)) {
        motion.value = applyMotionPreference(String(input.motion))
      }
      refreshLocal()
      applyThemeAppearance()
    })
    return { ok: true, customCss }
  }

  /** 主题工坊编辑自定义 CSS 时的实时预览（不保存）。传 null 结束预览 */
  function previewCustomCss(css: string | null) {
    setCustomCssPreview(css)
  }

  /** 退出安全模式并立即注入自定义 CSS（地址上的 ?safe=1 由 App.vue 去掉） */
  function exitSafeMode() {
    exitSafeModeAndInject()
    safeMode.value = false
  }

  if (typeof window !== 'undefined') {
    // 全站值变了（启动时 panel-settings 回来、或别处调了 applySiteThemeSettings）
    window.addEventListener(THEME_SITE_CHANGE_EVENT, () => {
      site.value = getSiteAppearance()
    })

    // 系统「减少动态效果」切换：跟随系统档的解析结果会变，重新挂一次 data-dd-motion 与兼容类
    window.matchMedia?.('(prefers-reduced-motion: reduce)')?.addEventListener('change', () => {
      applyMotionPreference()
      motionRevision.value += 1
    })

    // 其他标签页改了外观偏好：本页跟着变（storage 事件只在「别的」标签页写入时触发）
    window.addEventListener('storage', (event) => {
      if (event.key === 'theme') {
        mode.value = applyThemeMode()
      } else if (event.key === LITE_PREFERENCE_STORAGE_KEY) {
        // 必须排在下面 dd:appearance:* 的通用分支之前：流畅模式还要重算动效档位
        litePreference.value = applyLitePreference()
      } else if (event.key === 'dd:appearance:motion') {
        motion.value = applyMotionPreference()
        // 低端设备切 system <-> rich 时模糊降级状态会随之变化，补同步一次外观
        applyThemeAppearance()
        motionRevision.value += 1
      } else if (event.key?.startsWith('dd:appearance:') && event.key !== 'dd:appearance:site' && event.key !== 'dd:appearance:shape') {
        refreshLocal()
        applyThemeAppearance()
      }
    })

    // 慢设备自动开启流畅模式（只在从没手动设过时检测一次；检测本身等页面可见、聚焦、空闲后才做，不卡首屏）。
    // store 是整页单例，这里只会排一次。
    scheduleLiteAutoDetect(() => {
      litePreference.value = readLitePreference()
      // 一次性轻提示：之后存储里是 auto，不会再检测、也不会再提示。顺手给个「关闭」直接退出（记为 off，永不再自动开）
      toast.withAction('已自动开启流畅模式，可在 个人设置 → 界面外观 关闭', {
        text: '关闭',
        handler: () => setLite(false),
      })
    })
  }

  return {
    // 明暗（v3.3.2 起的三档，语义不变）
    isDark,
    mode,
    setThemeMode,
    toggleTheme,
    // v4.0 主题引擎
    local,
    site,
    motion,
    motionLevel,
    safeMode,
    litePreference,
    lite,
    setLite,
    preset,
    presetMeta,
    accent,
    background,
    backgroundImage,
    blur,
    radius,
    density,
    isLocal,
    setPreset,
    setAccent,
    setBackground,
    setBlur,
    setRadius,
    setDensity,
    setMotion,
    resetLocal,
    applySiteSettings,
    buildSiteDefaultConfigs,
    exportTheme,
    importTheme,
    previewCustomCss,
    exitSafeMode,
  }
})
