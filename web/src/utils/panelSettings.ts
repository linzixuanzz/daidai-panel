/**
 * 界面圆角风格。真源是服务端注册项 `panel_shape_style`
 * （server/model/system_config_registry.go，theme=跟随主题 默认 / square=直角 / rounded=圆角）。
 *
 * v4.0 新增 theme 并改为默认：圆角取当前主题的 --dd-theme-radius-*（styles/themes/*.scss）。
 * 升级时服务端会把已存的值一次性改成 theme（标记键 ui_v4_shape_migrated 保证只跑一次）。
 *
 * 只有网页端消费它：applyPanelAppearance() 会按它往 documentElement 写三档
 * `--dd-radius-*` 刻度令牌。APP 侧的 schema 设置页也会渲染出这个开关，但不生效
 * （服务端 description 里已写明）。
 */
export type PanelShapeStyle = 'theme' | 'square' | 'rounded'

export interface PanelSettingsPayload {
  panel_title?: string
  panel_icon?: string
  editor_background_color?: string
  log_background_color?: string
  log_background_image?: string
  // 服务端下发的原始值仍是字符串，这里按已知枚举收窄；真正的取值校验在
  // panelAppearance.ts 的 normalizePanelShape() 里做（历史脏值 / 将来新增枚举值都要兜住）。
  panel_shape_style?: PanelShapeStyle
  // ===== v4.0 主题引擎的全站默认值（design §1.3，category branding，仅网页端生效）=====
  // 全部是字符串原样下发，取值校验在 panelAppearance.ts 的 normalize* 里做；空串 = 跟随主题 / 未设置。
  /** 界面风格预设：classic / aurora / mist / terminal / paper / daidai / clean，默认 aurora */
  panel_theme_preset?: string
  /** 主色：'' = 跟随主题，否则 #rrggbb */
  panel_accent_color?: string
  /** 背景效果：theme / plain / aurora / grid / image，默认 theme */
  panel_background_effect?: string
  /** 背景图 data URL（背景效果为 image 时使用），默认 '' */
  panel_background_image?: string
  /** 玻璃模糊：'' = 跟随主题，否则 0~40 的整数（0 = 不透明卡片） */
  panel_glass_blur?: string
  /** 界面密度：compact / cozy / roomy，默认 cozy */
  panel_density?: string
  /** 管理员自定义 CSS（≤32KB，服务端已校验），注入为 <head> 末尾的 <style id="dd-custom-css">；对未登录访客也可见 */
  panel_custom_css?: string
}

let cachedPanelSettings: PanelSettingsPayload | null = null
let panelSettingsPromise: Promise<PanelSettingsPayload | null> | null = null

export function getCachedPanelSettings() {
  return cachedPanelSettings
}

export function getCachedPanelTitle() {
  return cachedPanelSettings?.panel_title?.trim() || '呆呆面板'
}

export async function loadPanelSettings(options?: { force?: boolean }) {
  if (!options?.force && panelSettingsPromise) {
    return panelSettingsPromise
  }

  const requestPromise = (async () => {
    // 在线演示 Demo 分叉：静态站没有后端，下面那发裸 fetch 必然 404。
    //
    // 这一处是整个应用打出去的【第一发请求】——router/index.ts 的模块顶层就有一句
    // `void loadPanelSettings()`，它在 main.ts 的 bootstrap() 函数体之前就求值了，
    // 比 installDemo() 还早。也就是说 demo 的 axios adapter 无论如何都拦不到它
    // （何况它压根不走 axios），只能在这里短路。
    //
    // 守卫形状不能动：`import.meta.env.VITE_DEMO` 是编译期常量，发布版里是 ''，
    // 整个 if 连同 '@/demo/shortcuts' 那个异步 chunk 会被 rollup 剔除。
    // 不要改成运行期判断，也不要静态 import demo 目录。
    if (import.meta.env.VITE_DEMO === '1') {
      const { demoPanelSettings } = await import('@/demo/shortcuts')
      cachedPanelSettings = demoPanelSettings()
      return cachedPanelSettings
    }

    try {
      const response = await fetch('/api/system/panel-settings', { cache: 'no-store' })
      if (!response.ok) {
        return cachedPanelSettings
      }

      const payload = await response.json() as { data?: PanelSettingsPayload }
      cachedPanelSettings = payload.data || null
      return cachedPanelSettings
    } catch {
      return cachedPanelSettings
    }
  })()

  panelSettingsPromise = requestPromise
  const result = await requestPromise
  if (!cachedPanelSettings) {
    panelSettingsPromise = null
  }
  return result
}
