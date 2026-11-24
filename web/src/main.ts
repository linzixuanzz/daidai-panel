import { createApp } from "vue";
import { createPinia } from "pinia";
import { ElMessageBox, provideGlobalConfig } from "element-plus";
import zhCn from "element-plus/es/locale/lang/zh-cn";
import "element-plus/theme-chalk/dark/css-vars.css";
import "element-plus/theme-chalk/el-loading.css";
import "element-plus/theme-chalk/el-message.css";
import "element-plus/theme-chalk/el-message-box.css";
import {
  ArrowLeft,
  ArrowRight,
  Bell,
  Box,
  Check,
  CircleCheck,
  CircleCheckFilled,
  CircleClose,
  Clock,
  Close,
  Connection,
  CopyDocument,
  Delete,
  Document,
  DocumentAdd,
  DocumentCopy,
  Download,
  Edit,
  Expand,
  Fold,
  Folder,
  FolderAdd,
  Hide,
  InfoFilled,
  Key,
  Lock,
  Menu,
  Monitor,
  Moon,
  More,
  MoreFilled,
  Odometer,
  Operation,
  Plus,
  Rank,
  Refresh,
  RefreshRight,
  Search,
  Setting,
  SetUp,
  Sort,
  Star,
  Sunny,
  Tickets,
  Timer,
  Top,
  Unlock,
  Upload,
  User,
  UserFilled,
  VideoPause,
  VideoPlay,
  View,
} from "@element-plus/icons-vue";
import App from "./App.vue";
import LoadingMotion from "./components/LoadingMotion.vue";
import { installSpotlight, vMagnetic } from "./directives/pointerEffects";
import router from "./router";
import { isChunkLoadError, reloadOnce } from "./utils/chunkReload";
import {
  applyMotionPreference,
  applyPanelShapeStyle,
  applyThemeAppearance,
  applyThemeMode,
  fetchAndApplyPanelAppearance,
} from "./utils/panelAppearance";
import "./styles/global.scss";
import "./styles/animations.css";
// v4.0 主题引擎：7 套主题 × 明暗的令牌、排版级令牌、EP 映射、背景 / 玻璃 / 密度开关。
// 必须排在 global.scss 之后：两者靠特异性分胜负（主题块 0,1,1 / 0,2,1 压 :root 与 html.dark），
// 但 _switches.scss 里少数与 global.scss 同特异性的规则（密度下的表格行高）要靠后出现。
import "./styles/themes/index.scss";
// v4.0 动效系统：弹簧缓动、交错入场、光斑、流光、烟花、View Transitions（档位见 data-dd-motion）
import "./styles/motion.scss";
// visual-enhancements.css 已删除（v3.0.8）：209 行 / 22 个工具类【全部零引用】，
// 逐个 grep 确认过。唯一看似有引用的 `.status-dot` 是 api-docs 页自己在 scoped 里
// 定义的同名类（且属性更全、取色走令牌），删掉全局那份不影响它。
//
// 删掉的另一个理由是它在留着当隐患：里面还有 `backdrop-filter: blur(10px)` 的玻璃拟态
// （.glass-card / .btn-glass）、`transition: all 0.3s ease` 这种写死时长的过渡，
// 以及 #67c23a / #f56c6c 这类硬编码语义色（暗色下不跟随）——
// 全都违反「全屏纯扁平直角」的硬规则，谁顺手复用一个类名就等于引入一处违规。

// Edge / Chromium 在窗口最小化后，如果弹窗、编辑器或第三方组件延迟调用 focus()，
// 可能会把已经最小化的浏览器窗口重新拉回前台。面板后台不可见时不需要抢焦点，
// 所以统一拦截后台状态下的程序化聚焦，避免用户点击最小化后窗口又闪回。
const daidaiWindow = window as Window & {
  __DAIDAI_SAFE_FOCUS_PATCHED__?: boolean;
};

if (!daidaiWindow.__DAIDAI_SAFE_FOCUS_PATCHED__) {
  daidaiWindow.__DAIDAI_SAFE_FOCUS_PATCHED__ = true;
  const rawHTMLElementFocus = HTMLElement.prototype.focus;

  HTMLElement.prototype.focus = function safeFocus(
    this: HTMLElement,
    options?: FocusOptions,
  ) {
    if (document.visibilityState === "hidden" || !document.hasFocus()) {
      return;
    }

    rawHTMLElementFocus.call(this, options);
  };
}

// iOS 聚焦字号 <16px 的输入框会自动放大整页（v3.3.3 / issue #148 下半），EP 的输入框默认就是 14px。
// 放大之后 iOS 不再遵守 body 的 overflow:hidden（WebKit bug 240860），弹窗背后的整页都能被拖动；
// 收起键盘后也不缩回，fixed 的弹窗与顶栏错位、底部露出背后的列表。
// 只对 iOS 追加 maximum-scale=1.0：iOS 10 起它只挡「自动放大」，用户仍可双指缩放；
// 安卓上它会真的禁掉双指缩放（无障碍问题），所以不写死在 index.html，这里按设备追加。
// iPadOS 13 起默认发桌面 UA（MacIntel），只能靠多点触控认出来。
// 和上面的 safeFocus 一样挂在模块顶层：只影响之后的聚焦行为，不需要抢首帧，也不依赖 app 挂载。
if (
  /iP(hone|ad|od)/.test(navigator.userAgent) ||
  (navigator.platform === "MacIntel" && navigator.maxTouchPoints > 1)
) {
  const viewportMeta = document.querySelector<HTMLMetaElement>(
    'meta[name="viewport"]',
  );
  // 在 index.html 原有内容后面追加，不整串覆盖：以后那边加了 viewport-fit 之类的参数也不会被这里冲掉
  if (viewportMeta) {
    viewportMeta.content += ", maximum-scale=1.0";
  }
}

// 升级后旧页面拿不到新版文件时自动刷新一次（issue #126，细节见 utils/chunkReload.ts）。
// Vite 的预加载助手在任何动态 import 失败时都会先派发这个事件（切页、Monaco、懒加载的弹窗都算），
// 它是 chunk 失效最早、也最全的信号。模块自身求值时抛的普通异常也会走到这里，由 isChunkLoadError 挡在外面：
// 那是代码 bug，刷新解决不了。
//
// ⚠️ preventDefault 只能在真的安排了刷新时才调：它会让那次 import 静默 resolve 成 undefined 而不是 reject。
//    被限次拦下（60 秒内刚刷过）、页面上有未保存的内容（这时 reloadOnce 改为提示用户保存后手动刷新）时，
//    reloadOnce 都返回 false。这时还吞掉的话，调用方就拿不到错误 —— 编辑器停在「加载中」出不来，
//    切页报的是一句不相干的「组件解析失败」—— 比原样抛出去更难懂。
//    reloadOnce 把真正的跳转推迟到下一个宏任务，所以先 reloadOnce 再 preventDefault 与反过来写效果相同。
// 挂在模块顶层：bootstrap() 里第一个动态 import（演示站的 mock 层）之前就必须就位。
window.addEventListener("vite:preloadError", (event) => {
  if (!isChunkLoadError(event.payload)) return;
  if (reloadOnce("preload")) {
    event.preventDefault();
  }
});

const globalIcons = {
  ArrowLeft,
  ArrowRight,
  Bell,
  Box,
  Check,
  CircleCheck,
  CircleCheckFilled,
  CircleClose,
  Clock,
  Close,
  Connection,
  CopyDocument,
  Delete,
  Document,
  DocumentAdd,
  DocumentCopy,
  Download,
  Edit,
  Expand,
  Fold,
  Folder,
  FolderAdd,
  Hide,
  InfoFilled,
  Key,
  Lock,
  Menu,
  Monitor,
  Moon,
  More,
  MoreFilled,
  Odometer,
  Operation,
  Plus,
  Rank,
  Refresh,
  RefreshRight,
  Search,
  Setting,
  SetUp,
  Sort,
  Star,
  Sunny,
  Tickets,
  Timer,
  Top,
  Unlock,
  Upload,
  User,
  UserFilled,
  VideoPause,
  VideoPlay,
  View,
};

// 整个启动流程必须是异步的，唯一原因是：演示环境的 mock 层要抢在
// 【app.use(router)】之前装到 axios 上，而它只能靠动态 import() 加载（见下方 D2 说明）。
//
// ⚠️ 触发首次导航的是 app.use(router)，不是 app.mount()。
//    vue-router 的 install() 里直接就是 `push(routerHistory.location)`
//    （node_modules/vue-router/dist/vue-router.mjs:1502-1507，同步执行），
//    所以 app.use(router) 一执行，router.beforeEach 就跑起来了：
//    已登录状态下刷新页面时 authStore.user 是空的，守卫会 await fetchUser()
//    发出 GET /api/auth/user；这一发晚一步被接管，就会 404 → clearAuth() → 打回登录页。
//    （首次访问看不出来：守卫更早的 `!isLoggedIn` 分支会先短路掉，走不到 fetchUser。）
//    ⇒ 只把 mount 推迟到 demo 加载完是【不够】的，必须连 app.use(router) 一起推迟。
//    改动这里前请先确认这条时序，不要退回「装 mock 与建 app 并行」的写法。
async function bootstrap() {
  // 首屏防闪形：圆角风格来自异步的 panel-settings，首帧一定先按 :root 的直角画一遍，
  // 圆角用户会看到「先方后圆」的形状跳变 —— 比闪色刺眼得多。
  // 这里先用 localStorage 里上次记住的值【同步】写一遍三条 --dd-radius-*，
  // 等下面那发请求回来 applyPanelAppearance() 再以服务端值重写（值一致时什么都不会变）。
  //
  // ⚠️ 必须放在下面 demo 的 await import() 之前：演示版构建里那一 await 会让出主线程，
  //    浏览器很可能已经把首帧画出去了，放在它后面就等于没做。
  applyPanelShapeStyle();
  // 界面动效偏好（跟随系统 / 始终开启 / 减少动效，个人设置页可切）同理：<html> 上的
  // dd-motion-force / dd-motion-off 必须在首帧之前挂好，挂晚了首屏动画会先按系统设置跑一遍。
  // 同样必须早于下面 demo 的 await。
  applyMotionPreference();
  // 首屏防闪色（v3.3.2，issue #145）：主题三档（明亮 / 暗夜 / 跟随系统）存在 localStorage 的
  // 裸 `theme` 键里，而 <html class="dark"> 原本只由 stores/theme.ts 的 watch 在 store 首次
  // 实例化（MainLayout / 登录页 setup）时才挂上，那已经在 app.mount 之后 —— 暗色用户每次刷新
  // 都会先闪一帧白底。这里同步重放一遍缓存值，跟随系统档会现读一次 matchMedia。
  // 同样必须早于下面 demo 的 await。
  applyThemeMode();
  // v4.0 主题引擎（风格 / 主色 / 背景 / 模糊 / 密度 / 自定义 CSS）：index.html 的内联脚本已经按同样规则写过一遍
  // <html> 的属性，这里用正式实现再写一次（顺带把自定义 CSS 注入 <head> 末尾，内联脚本不管它）。
  // 同样必须早于下面 demo 的 await；panel-settings 回来后 fetchAndApplyPanelAppearance 会以服务端值重算。
  applyThemeAppearance();

  // 这段刻意写成「编译期常量守卫 + 动态 import()」：
  // VITE_DEMO 在发布版构建里被 define 成 ''（见 vite.config.ts），条件恒假，
  // rollup 会把整个 if 分支连同 web/src/demo/** 的 chunk 一起剔除——
  // 发布版产物必须 0 字节 demo 代码，真实面板的请求绝不能被 mock 顶替。
  // 不要改成静态 import，也不要把条件换成运行期判断（那样无法做死代码消除）。
  if (import.meta.env.VITE_DEMO === "1") {
    try {
      const { installDemo } = await import("./demo");
      installDemo();
    } catch (error) {
      // demo chunk 拉取失败（CDN 抖动、缓存过期后旧 hash 404）时也要继续把 app 挂上去。
      // 少了这个 catch，异常会中断 bootstrap，访客看到的是纯白页面，
      // 连「哪里出错了」都无从判断。兜住之后至少还能看到登录页。
      console.error("[demo] mock 层加载失败，演示环境将无法正常工作:", error);
    }
  }

  // ⚠️ 别被位置误导：把这一行挪到 demo 安装前后，【都改变不了】那一发网络请求的时机。
  //    真正发请求的是 utils/panelSettings.ts 的裸 fetch('/api/system/panel-settings')，
  //    而它早在 `import router from "./router"` 这个静态 import 求值时就被打出去了
  //    （router/index.ts 顶层有一句 `void loadPanelSettings()`），
  //    也就是说它先于 bootstrap() 的函数体、更先于 installDemo()。
  //    loadPanelSettings() 内部对 promise 做了记忆化，这里只是复用同一发的结果。
  //    ⇒ 想让演示站不发这一枪，唯一的办法是按 design.md C4 在 panelSettings.ts 内部短路，
  //      而不是在这里调顺序。（目前它在演示站上会 404，被那边的空 catch 吞掉，不影响挂载。）
  //    留在 createApp 之前的理由只有一个：外观 CSS 变量尽早写进 documentElement，减少首屏闪色。
  void fetchAndApplyPanelAppearance();

  const app = createApp(App);

  // Element Plus 的 locale 走 provide/inject，取不到就回落英文默认包
  // （es/hooks/use-locale/index.mjs:17-18，`locale.value || en_default`），
  // 表现为确认框「Cancel / OK」、表格空态「No Data」、分页器「20/page」。
  //
  // ① provideGlobalConfig({ locale: zhCn }, app, true) —— 【唯一的真开关】。
  //    删掉它全站立刻回英文，而且构建全绿、控制台无声，属于典型的静默回归。
  //    传了 app ⇒ 走 app.provide 做【app 级】provide，组件树里的 el-table /
  //    el-pagination 才 inject 得到；第三个参数 global=true 另外把这份配置写进
  //    element-plus 的模块级 globalConfig（es/…/config-provider/src/hooks/use-global-config.mjs:54）。
  //    它与 app.use(ElementPlus, { locale }) 内部做的是同一件事（es/make-installer.mjs:11），
  //    但不会把整包组件拉进来，按需引入的体积优化保持不变。
  //
  // ② app.use(ElMessageBox) —— 【加固项，不是必需项】。
  //    别把它读成「缺了确认框就是英文」：ElMessageBox 虽然是命令式 API，用 render()
  //    脱离组件树挂载、vnode.appContext 默认为 null（es/…/message-box/src/messageBox.mjs:114），
  //    但它取 locale 的路径是 useGlobalComponentSettings -> useGlobalConfig ->
  //    inject(configProviderContextKey, globalConfig)（use-global-config.mjs:14）。
  //    appContext 为 null 时 Vue 的 inject 取不到 provides，会走「有默认值就返回默认值」
  //    那条分支（runtime-core inject: instance.parent == null -> provides 取自
  //    vnode.appContext，为 null 则 return defaultValue），返回的正是 ① 写好的
  //    那个模块级 globalConfig。⇒ 只有 ① 时确认框【已经】是中文。
  //    保留 ② 的理由：把 _context 指到 app._context，让它走真正的 app 级 provide，
  //    而不是长期依赖「inject 默认参数兜底」这个 EP 内部实现细节
  //    （哪天那个默认值变了，靠兜底的写法会静默失效）。
  //
  // ⚠️ 想加 CI 门禁守这件事时注意：能被产物断言守住的只有 ①（删了它 zhCn 这个 import
  //    就没人用，element-plus 的 sideEffects 不含 es/locale/**，rollup 会把整份中文包
  //    剔掉，产物里就搜不到 `共 {total} 条` 了）。英文包 en.mjs 被 use-locale 静态 import，
  //    两种情况下都在产物里，所以任何拿英文串做判据的断言都是恒绿的空转门禁。
  provideGlobalConfig({ locale: zhCn }, app, true);
  app.use(ElMessageBox);

  app.use(createPinia());
  // ↓ 首次导航在这一行触发，此时 demo adapter 已经就位
  app.use(router);
  app.component("LoadingMotion", LoadingMotion);
  // 动效 2.0：主按钮磁吸（v-magnetic）与卡片光斑（.dd-spot 的全局 pointermove 监听）
  app.directive("magnetic", vMagnetic);
  installSpotlight();

  for (const [key, component] of Object.entries(globalIcons)) {
    app.component(key, component);
  }

  app.mount("#app");
}

void bootstrap();
