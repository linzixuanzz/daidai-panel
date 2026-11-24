import type { PanelSettingsPayload } from '@/utils/panelSettings'
import { DEMO_BUILD_MARKER } from './marker'

/**
 * 「mock 网络层拦不住」的那几处代码内短路所需要的数据。
 *
 * 为什么需要这个文件：demo/adapter.ts 换掉的是 axios 的 adapter，而面板里有几处
 * 刻意【不走 axios】的调用 —— 裸 fetch（面板设置、登录页版本号）、keepalive fetch
 * （停止调试脚本，axios 做不到 keepalive）、以及重启后探活用的
 * `fetch('/', { method: 'HEAD' })`。它们只能在各自的生产文件里短路。
 *
 * ⚠️ 那些生产文件（utils/panelSettings.ts、views/login/index.vue、api/script.ts、
 *    views/settings/useSettings*.ts、utils/sse.ts）都是【真实面板会执行的代码】，
 *    所以每一处守卫都必须写成编译期常量形式：
 *
 *      if (import.meta.env.VITE_DEMO === '1') {
 *        const { demoXxx } = await import('@/demo/shortcuts')
 *        ...
 *      }
 *
 *    - 不能静态 import 本文件（静态 import 会把整个 demo 层拖进发布版主 chunk）；
 *    - 不能把条件换成运行期判断、也不能抽成返回 boolean 的 isDemo() 再判断
 *      —— 那样 rollup 折叠不掉，分支会被当成活代码保留。
 *    发布版里 VITE_DEMO 被 define 成 ''，`'' === '1'` 恒假，整段连同本文件所在的
 *    异步 chunk 一起被剔除。详见 demo/index.ts 的 D2 说明。
 */

/**
 * 演示站展示的面板版本号。
 *
 * 由 Pages 部署流程通过 VITE_DEMO_VERSION 注入（值就是发布 tag 去掉前缀 v）。
 * 本地构建读不到，保持空串 —— 侧边栏与横幅都是「有值才渲染」，
 * 空串直接不显示，不会出现 "vundefined" 这种脏文案。
 *
 * 三个消费方共用这一份：demo/adapter.ts 的 /system/version 与 /system/public-version、
 * demo/banner.ts 的横幅、以及登录页那处短路。
 */
export const DEMO_PANEL_VERSION = String(import.meta.env.VITE_DEMO_VERSION || '')

/**
 * 面板设置。
 *
 * panel_icon 必须是空串：MainLayout 的图标 <img> 没有 @error 兜底，
 * 给一个取不到的地址就是一个破图（同 fixtures/business.ts 里 avatar_url 的处理）。
 */
const DEMO_PANEL_SETTINGS: PanelSettingsPayload = {
  panel_title: '呆呆面板',
  panel_icon: '',
  // 界面圆角风格，与服务端 SystemHandler.PanelSettings 下发的字段对齐。
  // v4.0 起注册表默认值是 theme（跟随主题），演示站取同一个值，保持与全新安装的面板同一个观感。
  panel_shape_style: 'theme',
  // ===== v4.0 主题引擎的全站默认值，取值逐个对齐注册表默认值（wave1-handoff §6）=====
  // 演示站因此默认是「极光」；访客在主题工坊里改的是本机偏好，刷新后仍在。
  panel_theme_preset: 'aurora',
  panel_accent_color: '',
  panel_background_effect: 'theme',
  panel_background_image: '',
  panel_glass_blur: '',
  panel_density: 'cozy',
  panel_custom_css: '',
}

/**
 * `GET /api/system/panel-settings` 的等价物。
 *
 * 调用方是 utils/panelSettings.ts —— 它由 router/index.ts 的模块顶层直接调用，
 * 也就是【整个应用的第一发请求】，比 main.ts 的 installDemo() 还早，
 * 所以 adapter 天然拦不到它，必须在那边短路。
 *
 * 每次返回新对象：调用方会把结果缓存起来并被多个页面读取，共享同一个引用容易被就地改脏。
 */
export function demoPanelSettings(): PanelSettingsPayload {
  // 这两行 debug 顺带把哨兵字符串钉进产物：本文件是 demo 层的一个【独立入口】，
  // 不引用一次哨兵，CI 那条「发布版不含 mock 代码」的断言就管不到它。
  // 两个导出各写一次，是为了任意一个被单独引用时哨兵都还在（详见 demo/marker.ts）。
  console.debug(`[demo] 面板设置走演示短路 ${DEMO_BUILD_MARKER}`)
  return { ...DEMO_PANEL_SETTINGS }
}

/** `GET /api/system/public-version` 的等价物（登录页那处裸 fetch 用） */
export function demoPublicVersion(): string {
  console.debug(`[demo] 登录页版本号走演示短路 ${DEMO_BUILD_MARKER}`)
  return DEMO_PANEL_VERSION
}
