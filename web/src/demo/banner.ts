import { resetDb } from './db'

/**
 * 演示环境的全局横幅 + 「重置演示数据」入口。
 *
 * 为什么用原生 DOM 拼而不是写一个 Vue 组件挂到 MainLayout 里：
 * `.trellis/spec/frontend/directory-structure.md` 规定 web/src/demo/** 只允许被
 * main.ts 那一处动态 import() 引用，业务代码不得反向 import 本目录。
 * 在 MainLayout 里 `import DemoBanner from '@/demo/...'` 会直接打破这条约束
 * （哪怕外面套了 v-if，import 语句本身是静态的）。
 * 这里用 DOM 注入，业务代码零改动，发布版产物也就自然是 0 字节。
 *
 * 样式遵循 .trellis/spec/frontend/design-system.md 的全屏纯扁平基调：
 * 无阴影、无渐变、hover 只换底色，颜色一律引用设计令牌（暗色下不会串色）；
 * 圆角走 --dd-radius-* 三档刻度，跟随 panel_shape_style 开关，不写死像素。
 * 横幅本身是贴视口顶边的通栏，任何模式下都不加圆角（加了会在屏幕角上露缺口）。
 */

const BANNER_ID = 'dd-demo-banner'
const STYLE_ID = 'dd-demo-banner-style'

/** 横幅高度。改这里就够了，下面的 CSS 全部从这个变量派生。 */
const BANNER_HEIGHT = 34

/**
 * 演示站展示的面板版本号。
 *
 * 由 Pages 部署流程通过 VITE_DEMO_VERSION 注入（值是发布 tag 去掉前缀 v）。
 * 本地构建读不到，此时整段版本号不渲染 —— 不能退化成 "vundefined" 这种脏文案。
 */
const DEMO_VERSION = String(import.meta.env.VITE_DEMO_VERSION || '')

/**
 * 横幅样式。
 *
 * 三条 `body` / `.layout-container` / `.login-page` 的补丁是必需的：
 * 横幅是 position: fixed，不占文档流；而外壳写死了 `height: 100dvh`、
 * 登录页写死了 `min-height: 100vh`，不把这 34px 从它们身上减掉，整页就比视口高 34px：
 * 桌面端 html/body 是 overflow:hidden，表现为底部内容被裁掉；手机上整页能被拖动 34px，
 * 横幅、顶栏和弹窗会一起错开一截（issue #148 录屏）。
 *
 * 后两条必须带 !important：外壳和登录页的高度都写在 scoped 规则里（选择器带 [data-v-xxx]，特异性 (0,2,0)），
 * 这里的 (0,1,0) 会被静默压掉。v3.3.3 之前没加，横幅上线起这两条就从来没生效过。
 */
function bannerCss() {
  return `
:root { --dd-demo-banner-height: ${BANNER_HEIGHT}px; }

body { padding-top: var(--dd-demo-banner-height); }

/* MainLayout 的外壳写死 100dvh，登录页写死 100vh，都要把横幅高度让出来；!important 的理由见 bannerCss 的注释 */
.layout-container { height: calc(100dvh - var(--dd-demo-banner-height) - var(--dd-safe-banner-h, 0px)) !important; }
.login-page { min-height: calc(100dvh - var(--dd-demo-banner-height) - var(--dd-safe-banner-h, 0px)) !important; }
/* 登录页右上角明暗按钮为 fixed，需下移避开 34px 演示横幅 */
.login-page .theme-toggle { top: calc(24px + var(--dd-demo-banner-height)) !important; }

#${BANNER_ID} {
  position: fixed;
  top: 0;
  left: 0;
  right: 0;
  height: var(--dd-demo-banner-height);
  /*
   * z-index 只要低于 EP 浮层的 2000 即可，保持 20，与 sticky 顶栏并列（body 已让出横幅高度，两者不会重叠）。
   * v3.3.3（issue #148）起移动端 .layout-main 不再自成层叠上下文（见 MainLayout.vue 的 F1），原地渲染的弹窗 / 抽屉
   * （el-dialog、el-drawer 默认不 teleport）的遮罩直接在根上下文里用 2000+ 和横幅比；消息框、下拉 teleport 到 body，
   * 手机侧栏抽屉原地渲染在 .layout-container 下，也都是 2000+，统统盖得住横幅。
   * 横幅一旦 ≥ 2000，就会盖住弹窗和抽屉顶部 34px，抽屉右上角的 × 还会落在「重置演示数据」底下，
   * 点偏一点整页重载、演示数据清空。
   * （v3.3.1 / v3.3.2 时 .layout-main 是 z-index 21 的层叠上下文，这里的理由写的是「必须低于 21」，已作废。）
   */
  z-index: 20;
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 0 14px;
  box-sizing: border-box;
  font-size: 12px;
  line-height: 1;
  color: var(--el-color-primary);
  background: var(--el-color-primary-light-9);
  border-bottom: 1px solid var(--el-color-primary-light-5);
  font-family: var(--dd-font-ui, inherit);
}

#${BANNER_ID} .dd-demo-banner__tag {
  flex: 0 0 auto;
  padding: 3px 8px;
  font-weight: 600;
  letter-spacing: 0.5px;
  color: var(--dd-btn-primary-fg, #fff);
  background: var(--dd-btn-primary-bg, var(--el-color-primary));
  border-radius: var(--dd-radius-control, 0);
}

#${BANNER_ID} .dd-demo-banner__text {
  flex: 1 1 auto;
  min-width: 0;
  overflow: hidden;
  white-space: nowrap;
  text-overflow: ellipsis;
  color: var(--el-text-color-regular);
}

#${BANNER_ID} .dd-demo-banner__version {
  flex: 0 0 auto;
  color: var(--el-text-color-secondary);
  font-family: var(--dd-font-mono, monospace);
}

#${BANNER_ID} .dd-demo-banner__reset {
  flex: 0 0 auto;
  height: 22px;
  padding: 0 10px;
  font-size: 12px;
  font-family: inherit;
  cursor: pointer;
  color: var(--dd-accent-ink, var(--el-color-primary));
  background: var(--el-bg-color);
  border: 1px solid var(--el-color-primary-light-5);
  border-radius: var(--dd-radius-control, 0);
  transition:
    background-color var(--dd-motion-fast, 160ms) var(--dd-ease-standard, ease),
    color var(--dd-motion-fast, 160ms) var(--dd-ease-standard, ease),
    border-color var(--dd-motion-fast, 160ms) var(--dd-ease-standard, ease);
}

#${BANNER_ID} .dd-demo-banner__reset:hover {
  color: var(--dd-btn-primary-fg, #fff);
  background: var(--dd-btn-primary-bg, var(--el-color-primary));
  border-color: var(--dd-btn-primary-bg, var(--el-color-primary));
}

/* 窄屏只留「演示环境 + 重置」，说明文字换成最短的一句，避免挤成两行 */
@media screen and (max-width: 600px) {
  #${BANNER_ID} { gap: 8px; padding: 0 10px; }
  #${BANNER_ID} .dd-demo-banner__version { display: none; }
}
`
}

function bannerText() {
  return window.innerWidth <= 600
    ? '数据仅存于你的浏览器'
    : '这是在线演示环境，所有改动只存在你自己的浏览器里，刷新页面即恢复初始数据。'
}

/**
 * 安装横幅。
 *
 * 重复调用是安全的（installDemo 只会跑一次，但热更新场景下可能重入）。
 */
export function installDemoBanner() {
  if (document.getElementById(BANNER_ID)) return

  if (!document.getElementById(STYLE_ID)) {
    const style = document.createElement('style')
    style.id = STYLE_ID
    style.textContent = bannerCss()
    document.head.appendChild(style)
  }

  const banner = document.createElement('div')
  banner.id = BANNER_ID

  const tag = document.createElement('span')
  tag.className = 'dd-demo-banner__tag'
  tag.textContent = '演示环境'
  banner.appendChild(tag)

  const text = document.createElement('span')
  text.className = 'dd-demo-banner__text'
  text.textContent = bannerText()
  banner.appendChild(text)

  if (DEMO_VERSION) {
    const version = document.createElement('span')
    version.className = 'dd-demo-banner__version'
    version.textContent = `v${DEMO_VERSION}`
    banner.appendChild(version)
  }

  const reset = document.createElement('button')
  reset.className = 'dd-demo-banner__reset'
  reset.type = 'button'
  reset.textContent = '重置演示数据'
  reset.addEventListener('click', () => {
    resetDb()
    // 重置之后必须整页重载：十几个页面各自缓存着自己那份列表（keep-alive 还会留着组件实例），
    // 只改内存数据的话界面上什么都不会变，访客会以为按钮坏了。
    //
    // db 是纯内存的，重载本身就会回到初始 fixture，所以上面那行 resetDb() 从结果上看是多余的。
    // 保留它是为了让「重置」在代码里有一个明确入口，而不是依赖「刷新顺带也重置了」这个副作用 ——
    // 这也正是横幅文案敢承诺「刷新页面即恢复初始数据」的原因。
    window.location.reload()
  })
  banner.appendChild(reset)

  document.body.insertBefore(banner, document.body.firstChild)

  // 横屏/竖屏切换、窗口拖拽缩放时同步一次文案，避免窄屏下长句被截成一个省略号
  window.addEventListener('resize', () => {
    text.textContent = bannerText()
  })
}
