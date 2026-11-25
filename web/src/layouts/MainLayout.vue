<script setup lang="ts">
import { ref, computed, onMounted, onBeforeUnmount, watch, nextTick } from 'vue'
import { useRouter, useRoute } from 'vue-router'
import { useAuthStore } from '@/stores/auth'
import { useThemeStore } from '@/stores/theme'
import { useBadgesStore } from '@/stores/badges'
import { useUiStore } from '@/stores/ui'
import { useAttentionStore } from '@/stores/attention'
import { useRunningStore } from '@/stores/running'
import { installDesktopNotify } from '@/utils/desktopNotify'
import DdBadge from '@/components/ui/DdBadge.vue'
import CommandPalette from '@/components/CommandPalette.vue'
import ThemeStudio from '@/components/ThemeStudio.vue'
import RunningDock from '@/components/RunningDock.vue'
import AttentionList from '@/components/AttentionList.vue'
import ShortcutHelpDialog from '@/components/ShortcutHelpDialog.vue'
import SidebarCritters from '@/components/SidebarCritters.vue'
import { systemApi } from '@/api/system'
import { loadPanelSettings as loadCachedPanelSettings } from '@/utils/panelSettings'
import { reloadIfWebVersionStale } from '@/utils/chunkReload'
import { warmMonacoOnEngineSwitch } from '@/utils/monacoWarmup'
import { useResponsive } from '@/composables/useResponsive'
import { preloadPanelRoutes, preloadRouteByPath } from '@/router'
import { PANEL_MENU_GROUPS, GOTO_SHORTCUTS, findMenuItem, hasMinRole, type PanelMenuItem } from './menu'
import {
  Bell,
  Brush,
  Expand,
  Fold,
  Monitor,
  Moon,
  Operation,
  Search,
  Sunny,
} from '@element-plus/icons-vue'

const router = useRouter()
const route = useRoute()
const authStore = useAuthStore()
const themeStore = useThemeStore()
const badgesStore = useBadgesStore()
const ui = useUiStore()
const attentionStore = useAttentionStore()
const runningStore = useRunningStore()
const { isMobile } = useResponsive()
const isCollapsed = ref(false)
const drawerVisible = ref(false)
const panelTitle = ref('呆呆面板')
const panelIcon = ref('')
const panelVersion = ref('')
// keep-alive 上限：菜单里的路由页 14 个（主题工坊是抽屉不是页面）+ 留 1 个给 /dev/motion 这类不在菜单里的页
const routeCacheMax = 15

// 侧边栏 logo 的兜底图标。
// 这里不能写死 '/favicon-512.webp'：:src 是 v-bind 表达式，不经过 Vite 的 transformAssetUrls，
// 构建产物里会原样保留裸绝对路径。面板挂在反代子路径或 GitHub Pages 项目站下时，
// 这个路径会指到站点根而 404，侧边栏 logo 变破图（这两处 <img> 没有 @error 兜底）。
// import.meta.env.BASE_URL 结尾恒带 '/'，根路径部署时就是 '/'，行为与改动前一致。
const defaultPanelIcon = `${import.meta.env.BASE_URL}favicon-512.webp`
const panelIconSrc = computed(() => panelIcon.value || defaultPanelIcon)

// Mac 上搜索胶囊显示 ⌘ K，其余显示 Ctrl K（快捷键本身两种修饰键都认）
const isMac = typeof navigator !== 'undefined' && /Mac|iPhone|iPad|iPod/i.test(navigator.platform || navigator.userAgent)
const paletteKbd = isMac ? '⌘ K' : 'Ctrl K'

const canAccessAdmin = computed(() => hasMinRole(authStore.user?.role, 'admin'))

// 菜单来自 layouts/menu.ts（与命令面板共用）。按角色过滤，过滤后为空的分组整组不显示
const visibleGroups = computed(() =>
  PANEL_MENU_GROUPS
    .map(group => ({
      ...group,
      items: group.items.filter(item => hasMinRole(authStore.user?.role, item.minRole)),
    }))
    .filter(group => group.items.length > 0)
)

// 当前项：路径完全相同，或是它的子路径（例如将来的 /tasks/123）。有 action 的项（主题工坊）永远不是「当前页」
function isItemActive(item: PanelMenuItem) {
  if (!item.path) return false
  return route.path === item.path || route.path.startsWith(`${item.path}/`)
}

const activeKey = computed(() => {
  for (const group of visibleGroups.value) {
    const found = group.items.find(isItemActive)
    if (found) return found.key
  }
  return ''
})

const preloadableMenuPaths = computed(() =>
  visibleGroups.value
    .flatMap(group => group.items)
    .map(item => item.path)
    .filter((path): path is string => !!path && path !== route.path)
)

const breadcrumb = computed(() => {
  const matched = [...route.matched].reverse().find(item => item.meta.section)
  const section = matched?.meta.section === 'admin' ? '管理后台' : '工作台'
  const title = route.meta.title as string || ''
  return { section, title }
})

// 呆呆主题的侧栏小人：这是「内容」差异（多渲染一个组件），不是样式分支，所以允许按主题名判断
const showCritters = computed(() => themeStore.preset === 'daidai' && !isCollapsed.value)

// 主题三档（v3.3.2，issue #145）：跟随系统时显示显示器图标，与「明亮/暗夜」两个显式档区分开。
// 这里的图标必须【局部 import】：themeIcon 是 JS computed 返回组件对象，
// main.ts 里那份全局注册只对模板中直接写 <Monitor /> 生效，对这里完全无效。
const themeIcon = computed(() =>
  themeStore.mode === 'system' ? Monitor : themeStore.isDark ? Sunny : Moon
)
// 三档之后光看图标更难猜「现在是哪一档、点下去会怎样」，补一句悬浮提示。
// 按钮本身仍是二态：点一下脱离跟随、落到显式明/暗，第三档回设置页选。
const themeTitle = computed(() => {
  if (themeStore.mode === 'system') return '跟随系统 · 点击切换明暗'
  return themeStore.isDark ? '暗夜 · 点击切换明暗' : '明亮 · 点击切换明暗'
})

// 顶栏铃铛角标 = 「需要关注」条目数（服务端已按角色裁剪，前端再合并「有新版本」）
const attentionCount = computed(() => attentionStore.count)
const attentionCountText = computed(() => (attentionCount.value > 99 ? '99+' : String(attentionCount.value)))
// 运行中胶囊：为 0 时整个隐藏
const runningCount = computed(() => runningStore.count)

/**
 * 侧栏菜单角标。
 *
 * 只挂在「有事要处理」的菜单上：运行中的任务、今天失败的日志、拉取失败的订阅、
 * 装失败或正在装的依赖、以及有新版本时的系统设置。数据由 stores/badges.ts 轮询，
 * 服务端已按角色裁剪，看不到某个菜单的用户拿到的就是 0，这里不必再判权限。
 *
 * 刻意【不】给每个菜单都挂数字：角标是打断，全挂满等于全不挂，用户会直接学会忽略它。
 */
function badgeOf(item: PanelMenuItem) {
  return item.path ? badgesStore.menuBadges[item.path] : undefined
}

// ==================== 侧栏当前项指示条 ====================
// 一个绝对定位的元素，按当前项的 offsetTop / offsetHeight 用 --dd-spring 滑过去（design §3）。
// 第一次定位不带过渡（否则每次进页面都从顶上滑下来），之后才打开过渡。
const navRef = ref<HTMLElement | null>(null)
const indicator = ref({ top: 0, height: 40, visible: false })
const indicatorAnimated = ref(false)

function placeIndicator() {
  const nav = navRef.value
  if (!nav) return
  const active = nav.querySelector<HTMLElement>('.nav-item.is-active')
  if (!active) {
    // 当前页不在菜单里（例如 /dev/motion）：指示条原地淡出
    indicator.value = { ...indicator.value, visible: false }
    return
  }
  indicator.value = { top: active.offsetTop, height: active.offsetHeight, visible: true }
  if (!indicatorAnimated.value) {
    window.requestAnimationFrame(() => {
      indicatorAnimated.value = true
    })
  }
}

watch([activeKey, isCollapsed, visibleGroups, isMobile, () => themeStore.density], () => {
  void nextTick(placeIndicator)
})

// 监听侧栏内容高度变化：例如用户在主题工坊开启「隐藏分组标题」自定义 CSS 后，
// .nav-item 的 offsetTop 会立刻变化，靠 ResizeObserver 自动把滑动指示条对齐到新位置。
let navResizeObserver: ResizeObserver | null = null
let navMutationObserver: MutationObserver | null = null

// 切到 Monaco 引擎时立即预热（#133，utils/monacoWarmup.ts）。挂在布局上而不是某个编辑器页：
// 引擎可能在任一编辑器页的齿轮菜单里切，布局是它们登录后一直在的共同祖先。
let stopEngineSwitchWarmup: (() => void) | null = null
// 桌面通知（F10）的清理函数：installDesktopNotify() 返回
let stopDesktopNotify: (() => void) | null = null

onMounted(() => {
  loadPanelSettings()
  loadVersion()
  if (authStore.isLoggedIn && !authStore.user) {
    authStore.fetchUser()
  }
  scheduleMenuPreload()
  stopEngineSwitchWarmup = warmMonacoOnEngineSwitch()
  // 只在布局挂上之后才开始轮询：登录页不该去打受保护的接口
  badgesStore.start()
  attentionStore.start()
  runningStore.start()
  stopDesktopNotify = installDesktopNotify()
  // 全站唯一的全局快捷键入口（F9，handoff §8）。其他组件不要再挂全局 keydown
  window.addEventListener('keydown', handleGlobalKeydown)
  void nextTick(() => {
    placeIndicator()
    const nav = navRef.value
    if (nav && typeof ResizeObserver !== 'undefined') {
      navResizeObserver = new ResizeObserver(() => placeIndicator())
      navResizeObserver.observe(nav)
      nav.querySelectorAll<HTMLElement>('.nav-group').forEach((group) => navResizeObserver?.observe(group))
    }
    if (typeof MutationObserver !== 'undefined') {
      // 主题工坊实时预览/保存自定义 CSS 会修改 <style id="dd-custom-css">，触发后补对齐一次指示条
      navMutationObserver = new MutationObserver(() => window.requestAnimationFrame(placeIndicator))
      navMutationObserver.observe(document.head, { childList: true, subtree: true, characterData: true })
    }
  })
})

onBeforeUnmount(() => {
  // 退出登录会卸载整个布局。不停表的话定时器会继续打 /system/badges，
  // 每次都 401，把用户刚跳到的登录页反复推一遍。下面两个轮询同理。
  badgesStore.stop()
  attentionStore.stop()
  runningStore.stop()
  stopDesktopNotify?.()
  stopDesktopNotify = null
  stopEngineSwitchWarmup?.()
  stopEngineSwitchWarmup = null
  navResizeObserver?.disconnect()
  navResizeObserver = null
  navMutationObserver?.disconnect()
  navMutationObserver = null
  window.removeEventListener('keydown', handleGlobalKeydown)
  if (gotoTimer) window.clearTimeout(gotoTimer)
  // 浮层开关在 store 里，布局卸载（退出登录）后不清的话，下次登录一进来命令面板 / 浮窗还开着
  ui.closeAll()
})

// 记住桌面端用户的折叠偏好：窗口缩到移动端再拉回桌面时恢复展开态，不永久锁在收起状态
let desktopCollapsedPreference = false

watch(isMobile, (mobile, prevMobile) => {
  if (mobile) {
    if (prevMobile === false) {
      desktopCollapsedPreference = isCollapsed.value
    }
    isCollapsed.value = true
    return
  }
  drawerVisible.value = false
  if (prevMobile === true) {
    isCollapsed.value = desktopCollapsedPreference
  }
}, { immediate: true })

watch(() => authStore.user?.role, () => {
  scheduleMenuPreload()
})

function handleMenuClick(item: PanelMenuItem) {
  if (isMobile.value) drawerVisible.value = false
  if (item.action === 'theme-studio') {
    ui.openStudio()
    return
  }
  if (!item.path) return
  preloadPage(item.path)
  router.push(item.path)
}

function preloadPage(path?: string) {
  if (path) void preloadRouteByPath(path)
}

function scheduleMenuPreload() {
  // 首屏先让当前页稳定渲染；空闲时再分批预加载菜单页，减少首次切页等待 chunk 下载导致的白屏。
  preloadPanelRoutes(preloadableMenuPaths.value)
}

function toggleSidebar() {
  if (isMobile.value) {
    drawerVisible.value = !drawerVisible.value
  } else {
    isCollapsed.value = !isCollapsed.value
    desktopCollapsedPreference = isCollapsed.value
  }
}

async function handleLogout() {
  await authStore.logout()
}

// ==================== F9 全局快捷键 ====================
// 规则（design §5 F9 + handoff §8）：
//   - Ctrl/⌘ K：开关命令面板。在输入框里也生效；焦点在 Monaco / CodeMirror 里时【不拦截】，交给编辑器。
//   - g 后接字母：跳页（映射见 menu.ts 的 GOTO_SHORTCUTS）；? 打开帮助；Esc 关掉全部浮层。
//   - 输入框、可编辑区、代码编辑器里一律不触发单键快捷键（g / ? / Esc），放心打字。
//   - 有 EP 弹窗 / 抽屉开着时也不触发 g / ?（不在弹窗背后偷偷换页）；Esc 照常交给弹窗自己处理。
let gotoPending = false
let gotoTimer = 0

function isEditableTarget(target: EventTarget | null) {
  if (!(target instanceof Element)) return false
  if (target instanceof HTMLElement && target.isContentEditable) return true
  return !!target.closest('input, textarea, select, [contenteditable=""], [contenteditable="true"], .monaco-editor, .cm-editor')
}

function hasOpenOverlay() {
  // 与 global.scss 锁滚动那条同一个判断：EP 关掉的遮罩会留在 DOM 里，只是 display: none
  return !!document.querySelector('.el-overlay:not([style*="display: none"])')
}

// 是否开着「业务页自己的」EP 弹窗 / 抽屉（不算本布局挂的主题工坊抽屉与快捷键帮助弹窗）。
// 命令面板 z-index 是 1990，EP 弹窗从 2000 起：业务弹窗开着时再打开命令面板，面板会被压在遮罩下面看不见。
// 主题工坊、快捷键帮助由 ui store 管，打开面板时会被互斥关掉，所以不拦。
function hasForeignOverlay() {
  return Array.from(document.querySelectorAll('.el-overlay:not([style*="display: none"])')).some(
    el => !el.classList.contains('dd-studio-mask') && !el.querySelector('.dd-shortcut-dialog')
  )
}

function clearGotoPending() {
  gotoPending = false
  if (gotoTimer) window.clearTimeout(gotoTimer)
  gotoTimer = 0
}

function handleGlobalKeydown(event: KeyboardEvent) {
  // 输入法组字中（中文输入的回车 / 字母）一律不管
  if (event.isComposing || event.keyCode === 229) return
  // 挂在 window 的冒泡阶段：命令面板等组件先处理自己的按键（Esc 会 stopPropagation 根本到不了这里）；
  // 已经被组件 preventDefault 的按键（包括面板自己处理的 Ctrl K）一律不再处理，避免开了又关
  if (event.defaultPrevented) return
  const target = event.target

  // Ctrl/⌘ K
  if ((event.ctrlKey || event.metaKey) && !event.altKey && !event.shiftKey && event.key.toLowerCase() === 'k') {
    if (target instanceof Element && target.closest('.monaco-editor, .cm-editor')) return
    event.preventDefault()
    clearGotoPending()
    // 业务弹窗开着时不打开（面板会被遮罩压住，见 hasForeignOverlay）；已经开着的面板照常可以关
    if (!ui.paletteOpen && hasForeignOverlay()) return
    // 快捷键帮助不参与 ui store 的互斥：从帮助里按 Ctrl K 时先关掉它，免得帮助弹窗（2000+）盖住面板
    if (!ui.paletteOpen) ui.closeShortcutHelp()
    ui.togglePalette()
    return
  }

  // 以下都是单键快捷键：带修饰键的组合、组件自己已经处理过的按键、可编辑区里的按键都放过
  if (event.ctrlKey || event.metaKey || event.altKey) return
  if (isEditableTarget(target)) {
    clearGotoPending()
    return
  }

  if (event.key === 'Escape') {
    clearGotoPending()
    // EP 弹窗 / 抽屉（含主题工坊、快捷键帮助）开着时，Esc 归 EP 自己（它在 document 上先处理、关掉那一层）。
    // 这里再 closeAll 会把运行中浮窗、铃铛弹出层一起关掉，一次 Esc 关两层。
    if (hasOpenOverlay()) return
    if (ui.paletteOpen || ui.studioOpen || ui.dockOpen || ui.attentionOpen || ui.shortcutHelpOpen) {
      ui.closeAll()
    }
    return
  }

  if (hasOpenOverlay()) {
    clearGotoPending()
    return
  }

  // g 之后的第二个键
  if (gotoPending) {
    const key = event.key.toLowerCase()
    clearGotoPending()
    const shortcut = GOTO_SHORTCUTS.find(row => row.key === key)
    const item = shortcut ? findMenuItem(shortcut.itemKey) : undefined
    if (item?.path && hasMinRole(authStore.user?.role, item.minRole)) {
      event.preventDefault()
      if (route.path !== item.path) router.push(item.path)
    }
    return
  }

  if (event.key === 'g' && !event.shiftKey && !event.repeat) {
    gotoPending = true
    // 1.2 秒内没按第二个键就作废，免得之后随手按个 t 被当成「跳到任务」
    gotoTimer = window.setTimeout(clearGotoPending, 1200)
    return
  }

  if (event.key === '?') {
    event.preventDefault()
    ui.toggleShortcutHelp()
  }
}

async function loadPanelSettings() {
  try {
    const settings = await loadCachedPanelSettings()
    if (settings?.panel_title) panelTitle.value = settings.panel_title
    if (settings?.panel_icon) panelIcon.value = settings.panel_icon
    const routeTitle = route.meta.title as string | undefined
    document.title = routeTitle ? `${panelTitle.value} - ${routeTitle}` : panelTitle.value
  } catch {}
}

async function loadVersion() {
  try {
    const res = await systemApi.version() as any
    const serverVersion = res.data?.version
    if (serverVersion) panelVersion.value = serverVersion
    // 版本自检（#126，契约 C10）：上面的徽标取的是后端版本，浏览器跑着缓存里的旧前端壳时它照样显示新版本号，
    // 用户看不出来。这里拿后端版本和本前端包构建时写死的 __DD_WEB_VERSION__ 比，后端版本比前端新时自动刷新一次
    // （相等或后端更旧都不刷；有未保存内容时改为提示）。限次，比较前两边都去掉前缀 v，细节见 utils/chunkReload.ts。
    // 守卫必须是编译期常量：开发环境没有「旧壳」这回事，版本号改到一半时还会误刷；演示站的 /system/version
    // 返回部署时注入的 VITE_DEMO_VERSION，跟 package.json 无关，不关掉就会一进来就刷。
    if (import.meta.env.PROD && import.meta.env.VITE_DEMO !== '1') {
      reloadIfWebVersionStale(serverVersion)
    }
  } catch {}
}

</script>

<template>
  <div class="layout-container" :class="{ 'is-mobile': isMobile, 'is-collapsed': isCollapsed && !isMobile }">
    <!-- Desktop Sidebar：浮岛（四周 12px）+ 侧栏玻璃令牌；收起 / 展开由外层 grid 列宽做弹簧过渡 -->
    <aside v-if="!isMobile" class="layout-aside" :class="{ 'is-collapsed': isCollapsed }" aria-label="主导航">
      <!-- Logo -->
      <div class="sidebar-logo">
        <img :src="panelIconSrc" alt="logo" class="logo-icon" />
        <span class="logo-title">{{ panelTitle }}</span>
        <span v-if="panelVersion" class="logo-version">v{{ panelVersion }}</span>
      </div>

      <!-- Scrollable navigation area -->
      <nav ref="navRef" class="sidebar-nav">
        <span
          class="nav-indicator"
          :class="{ 'is-animated': indicatorAnimated, 'is-hidden': !indicator.visible }"
          :style="{ transform: `translateY(${indicator.top}px)`, height: `${indicator.height}px` }"
          aria-hidden="true"
        />
        <div v-for="group in visibleGroups" :key="group.key" class="nav-group">
          <div class="nav-group-label dd-nav-group-title">{{ group.title }}</div>
          <el-tooltip
            v-for="item in group.items"
            :key="item.key"
            :content="item.title"
            placement="right"
            :disabled="!isCollapsed"
            :show-after="120"
          >
            <button
              type="button"
              class="nav-item"
              :class="{ 'is-active': item.key === activeKey }"
              :aria-current="item.key === activeKey ? 'page' : undefined"
              :aria-label="item.title"
              @click="handleMenuClick(item)"
              @mouseenter="preloadPage(item.path)"
              @focusin="preloadPage(item.path)"
            >
              <span class="nav-icon">
                <el-icon :size="18"><component :is="item.icon" /></el-icon>
                <!-- 收起态的角标：标题与数字都藏起来了，退化成图标右上角一个圆点（显隐纯靠 CSS 的 .is-collapsed） -->
                <span
                  v-if="badgeOf(item)"
                  class="nav-collapsed-dot"
                  :class="`is-${badgeOf(item)!.level}`"
                />
              </span>
              <span class="nav-label">{{ item.title }}</span>
              <DdBadge
                v-if="badgeOf(item)"
                class="nav-badge"
                :value="badgeOf(item)!.count"
                :dot="badgeOf(item)!.dot"
                :level="badgeOf(item)!.level"
                :title="item.title"
              />
            </button>
          </el-tooltip>
        </div>
      </nav>

      <!-- 呆呆主题：四个小人趴在墙头（用过渡包裹容器避免侧栏展开/收起跳动，矮屏自动隐藏） -->
      <div class="sidebar-critters-slot" :class="{ 'is-visible': showCritters }">
        <SidebarCritters v-if="showCritters" />
      </div>

      <!-- User card + collapse toggle -->
      <div class="sidebar-foot">
        <button type="button" class="user-card" :title="authStore.user?.username || 'User'" @click="router.push('/profile')">
          <img
            v-if="authStore.user?.avatar_url"
            :src="authStore.user.avatar_url"
            alt=""
            class="user-avatar"
          />
          <span v-else class="user-avatar user-avatar--placeholder">
            {{ (authStore.user?.username || 'U').charAt(0).toUpperCase() }}
          </span>
          <span class="user-info">
            <span class="user-name">{{ authStore.user?.username || 'User' }}</span>
            <span class="user-role">{{ authStore.user?.role === 'admin' ? '管理员' : '用户' }}</span>
          </span>
        </button>
        <button
          type="button"
          class="sidebar-collapse-btn"
          :title="isCollapsed ? '展开菜单' : '收起菜单'"
          :aria-label="isCollapsed ? '展开菜单' : '收起菜单'"
          @click="toggleSidebar"
        >
          <el-icon :size="18"><component :is="isCollapsed ? Expand : Fold" /></el-icon>
        </button>
      </div>
    </aside>

    <!-- Mobile Drawer
         宽度实际由 global.scss 的 .el-drawer.layout-mobile-drawer 决定（50%，夹在 220~320px）：
         移动端通用的 .el-drawer{width:100% !important} 会压掉这里的 size，只能靠那条更高特异性的例外规则放出来。
         size 仍写 50% 是为了让组件自己的语义与实际一致，免得后人以为抽屉是 260px。 -->
    <el-drawer
      v-if="isMobile"
      v-model="drawerVisible"
      class="layout-mobile-drawer"
      direction="ltr"
      size="50%"
      :with-header="false"
      :show-close="false"
    >
      <div class="sidebar-logo mobile-logo">
        <img :src="panelIconSrc" alt="logo" class="logo-icon" />
        <span class="logo-title">{{ panelTitle }}</span>
        <span v-if="panelVersion" class="logo-version">v{{ panelVersion }}</span>
      </div>

      <!-- 抽屉里不做滑动指示条：抽屉每次打开都是新画面，当前项直接给静态选中底色 -->
      <nav class="sidebar-nav mobile-nav is-static">
        <div v-for="group in visibleGroups" :key="group.key" class="nav-group">
          <div class="nav-group-label dd-nav-group-title">{{ group.title }}</div>
          <button
            v-for="item in group.items"
            :key="item.key"
            type="button"
            class="nav-item"
            :class="{ 'is-active': item.key === activeKey }"
            :aria-current="item.key === activeKey ? 'page' : undefined"
            @click="handleMenuClick(item)"
          >
            <span class="nav-icon">
              <el-icon :size="18"><component :is="item.icon" /></el-icon>
            </span>
            <span class="nav-label">{{ item.title }}</span>
            <DdBadge
              v-if="badgeOf(item)"
              class="nav-badge"
              :value="badgeOf(item)!.count"
              :dot="badgeOf(item)!.dot"
              :level="badgeOf(item)!.level"
              :title="item.title"
            />
          </button>
        </div>
      </nav>

      <div class="sidebar-foot">
        <button type="button" class="user-card" @click="router.push('/profile'); drawerVisible = false">
          <img
            v-if="authStore.user?.avatar_url"
            :src="authStore.user.avatar_url"
            alt=""
            class="user-avatar"
          />
          <span v-else class="user-avatar user-avatar--placeholder">
            {{ (authStore.user?.username || 'U').charAt(0).toUpperCase() }}
          </span>
          <span class="user-info">
            <span class="user-name">{{ authStore.user?.username || 'User' }}</span>
            <span class="user-role">{{ authStore.user?.role === 'admin' ? '管理员' : '用户' }}</span>
          </span>
        </button>
      </div>
    </el-drawer>

    <!-- Main content area -->
    <div class="layout-main-wrap">
      <!-- Header -->
      <header class="layout-header">
        <div class="header-left">
          <!-- 桌面端收起按钮在侧栏底部；这里只给移动端开抽屉 -->
          <button
            v-if="isMobile"
            type="button"
            class="header-toggle-btn dd-glass"
            aria-label="打开导航"
            @click="toggleSidebar"
          >
            <el-icon :size="18"><Operation /></el-icon>
          </button>
          <nav class="header-breadcrumb dd-glass" aria-label="当前位置">
            <span class="breadcrumb-section">{{ breadcrumb.section }}</span>
            <template v-if="breadcrumb.title">
              <span class="breadcrumb-sep">›</span>
              <span class="breadcrumb-current">{{ breadcrumb.title }}</span>
            </template>
          </nav>
        </div>

        <div class="header-right">
          <!-- 1. 搜索胶囊：打开命令面板。移动端只剩图标 -->
          <button
            type="button"
            class="header-search dd-glass"
            :aria-label="`搜索与快速操作（${paletteKbd}）`"
            @click="ui.openPalette()"
          >
            <el-icon :size="16" class="search-icon"><Search /></el-icon>
            <span class="search-placeholder">搜索任务、脚本、变量，或输入操作…</span>
            <kbd class="search-kbd">{{ paletteKbd }}</kbd>
          </button>

          <!-- 2. 运行中胶囊：为 0 时隐藏，点开运行中浮窗 -->
          <button
            v-if="runningCount > 0"
            type="button"
            class="header-runcap dd-glass"
            :class="{ 'is-open': ui.dockOpen }"
            :aria-label="`${runningCount} 个任务运行中`"
            :aria-expanded="ui.dockOpen"
            @click="ui.toggleDock()"
          >
            <span class="runcap-dot" aria-hidden="true" />
            <span class="runcap-num">{{ runningCount }}</span>
            <span class="runcap-label">个运行中</span>
          </button>

          <!-- 3~6. 明暗 / 主题工坊 / 通知铃 / 用户，收在同一个胶囊里 -->
          <div class="header-tools dd-glass">
            <button
              type="button"
              class="header-icon-btn theme-toggle"
              :title="themeTitle"
              :aria-label="themeTitle"
              @click="themeStore.toggleTheme"
            >
              <el-icon :size="18"><component :is="themeIcon" /></el-icon>
            </button>

            <button
              type="button"
              class="header-icon-btn"
              title="主题工坊"
              aria-label="主题工坊"
              @click="ui.openStudio()"
            >
              <el-icon :size="18"><Brush /></el-icon>
            </button>

            <el-popover
              v-model:visible="ui.attentionOpen"
              trigger="click"
              placement="bottom-end"
              width="min(380px, calc(100vw - 24px))"
              :show-arrow="false"
              popper-class="dd-attention-popper"
            >
              <template #reference>
                <button
                  type="button"
                  class="header-icon-btn"
                  :title="attentionCount ? `需要关注 · ${attentionCount} 项` : '需要关注'"
                  :aria-label="attentionCount ? `需要关注，${attentionCount} 项` : '需要关注'"
                >
                  <el-icon :size="18"><Bell /></el-icon>
                  <span v-if="attentionCount" class="bell-count">{{ attentionCountText }}</span>
                </button>
              </template>
              <div class="attention-pop">
                <div class="attention-pop__head">
                  <span class="attention-pop__title">需要关注</span>
                  <span v-if="attentionCount" class="attention-pop__count">{{ attentionCount }} 项</span>
                </div>
                <AttentionList compact />
              </div>
            </el-popover>

            <el-dropdown trigger="click">
              <div class="header-user" tabindex="0">
                <img
                  v-if="authStore.user?.avatar_url"
                  :src="authStore.user.avatar_url"
                  alt=""
                  class="header-user-avatar"
                />
                <div v-else class="header-user-avatar header-user-avatar--placeholder">
                  {{ (authStore.user?.username || 'U').charAt(0).toUpperCase() }}
                </div>
                <span v-if="!isMobile" class="header-user-name">{{ authStore.user?.username || 'User' }}</span>
              </div>
              <template #dropdown>
                <el-dropdown-menu>
                  <el-dropdown-item @click="router.push('/profile')">个人设置</el-dropdown-item>
                  <el-dropdown-item v-if="canAccessAdmin" @click="router.push('/admin/settings')">系统设置</el-dropdown-item>
                  <el-dropdown-item @click="ui.openShortcutHelp()">快捷键</el-dropdown-item>
                  <el-dropdown-item divided @click="handleLogout">退出登录</el-dropdown-item>
                </el-dropdown-menu>
              </template>
            </el-dropdown>
          </div>
        </div>
      </header>

      <!-- Page content -->
      <main class="layout-main">
        <div class="route-shell">
          <router-view v-slot="{ Component, route: viewRoute }">
            <transition name="page-shell">
              <keep-alive :max="routeCacheMax">
                <component :is="Component" :key="viewRoute.name || viewRoute.path" />
              </keep-alive>
            </transition>
          </router-view>
        </div>
      </main>
    </div>

    <!-- 全站浮层：各挂一次，开关都在 stores/ui.ts（handoff §8「MainLayout 挂载点」）。
         放在外壳根上、侧栏与内容区之外：侧栏带 backdrop-filter，会成为 position:fixed 后代的包含块。
         外面包一层零尺寸的绝对定位容器：外壳是 grid，浮层组件的根节点万一是普通流元素，会多占出一行把布局挤坏 -->
    <div class="layout-overlays">
      <CommandPalette />
      <ThemeStudio />
      <RunningDock />
      <ShortcutHelpDialog />
    </div>
  </div>
</template>

<style scoped lang="scss">
// ==================== 全屏外壳 ====================
// v4.0：外壳是两列 grid（侧栏列 + 内容列），收起 / 展开只改列宽，用 --dd-spring-soft 做弹簧过渡。
// 底色透明：背景由 App.vue 的 AppBackdrop（position:fixed; z-index:-1）提供，外壳和内容区都不能再铺实色。
// overflow: hidden 用于把内部滚动锁在 layout-main / route-shell 里，不让整页产生滚动条。
// 注意：内部滚动链（layout-main / route-shell / dd-*-page）保持不动。
.layout-container {
  // 侧栏列宽：展开 244px（浮岛本体 232px + 左侧 12px 外边距），收起 84px（本体 72px）
  --dd-shell-aside-w: 244px;
  // 顶栏胶囊统一高度（桌面 40px、移动端 36px），左右两侧所有胶囊与按钮都按它对齐
  --dd-header-chip-height: 40px;

  display: grid;
  grid-template-columns: var(--dd-shell-aside-w) minmax(0, 1fr);
  margin: 0;
  height: calc(100dvh - var(--dd-safe-banner-h, 0px));
  overflow: hidden;
  transition: grid-template-columns 0.6s var(--dd-spring-soft);

  &.is-collapsed {
    --dd-shell-aside-w: 84px;
  }

  // 移动端没有桌面侧栏（抽屉不占列）
  &.is-mobile {
    grid-template-columns: minmax(0, 1fr);
  }
}

// 浮层挂载点：绝对定位的 grid 子项不占格子；零尺寸、不带 transform / filter，不会成为 fixed 浮层的包含块
.layout-overlays {
  position: absolute;
  top: 0;
  left: 0;
  width: 0;
  height: 0;
}

// ==================== Sidebar ====================
// 浮岛：四周留 12px（右侧由内容区自己的左内边距给），读侧栏排版级令牌（_layout.scss，素白覆盖）。
// 侧栏里没有原地渲染的弹窗（菜单气泡与下拉都 teleport 到 body），所以可以放心带 backdrop-filter。
.layout-aside {
  min-width: 0;
  height: calc(100% - 24px);
  margin: 12px 0 12px 12px;
  display: flex;
  flex-direction: column;
  overflow: hidden;
  background: var(--dd-sidebar-bg);
  border: var(--dd-border-width) solid var(--dd-border);
  border-radius: var(--dd-sidebar-radius);
  box-shadow: var(--dd-sidebar-shadow);
  -webkit-backdrop-filter: blur(var(--dd-sidebar-blur)) saturate(150%);
  backdrop-filter: blur(var(--dd-sidebar-blur)) saturate(150%);
  z-index: 10;
}

// 仅用户显式关闭玻璃（blur=0）或低端降级（disabled）与系统「降低透明度」时关侧栏模糊并退回实色；
// 素白主题默认卡片不透明（data-dd-glass="off"）仍保留其标志性的 24px 悬浮玻璃侧栏。
html[data-dd-glass="disabled"] .layout-aside {
  background: var(--dd-surface-solid);
  -webkit-backdrop-filter: none;
  backdrop-filter: none;
}

@media (prefers-reduced-transparency: reduce) {
  .layout-aside {
    background: var(--dd-surface-solid);
    -webkit-backdrop-filter: none;
    backdrop-filter: none;
  }
}

// 收起时这些文字只淡出、不抽掉：列宽在做弹簧过渡，文字被侧栏的 overflow:hidden 裁掉即可，避免中途跳动。
// 角标（DdBadge）不在这里：它根节点上有自己的弹出过渡，这里写 transition 会把那条压掉
.logo-title,
.logo-version,
.nav-label,
.nav-group-label,
.user-info {
  transition: opacity 0.3s var(--dd-ease-standard);
}

// Logo
.sidebar-logo {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 16px 16px 12px 20px;
  flex-shrink: 0;
  white-space: nowrap;
  transition: padding 0.6s var(--dd-spring-soft);
}

.logo-icon {
  width: 32px;
  height: 32px;
  flex-shrink: 0;
  border-radius: var(--dd-radius-control);
  transition: transform 0.6s var(--dd-spring);
}

// 悬停时 logo 轻轻歪一下头：只在丰富档
html[data-dd-motion="rich"] .sidebar-logo:hover .logo-icon {
  transform: rotate(-8deg) scale(1.08);
}

.logo-title {
  flex: 1;
  min-width: 0;
  font-family: var(--dd-font-head);
  font-size: 15px;
  font-weight: 700;
  color: var(--dd-text);
  overflow: hidden;
  text-overflow: ellipsis;
}

// 版本徽标：主色墨色字 + 主色淡底（handoff §7：直接读令牌，不再靠 global.scss 的暗色补丁）
.logo-version {
  flex-shrink: 0;
  padding: 2px 7px;
  border-radius: var(--dd-radius-pill);
  font-family: var(--dd-font-mono);
  font-size: 10px;
  font-weight: 700;
  letter-spacing: 0.04em;
  color: var(--dd-accent-ink);
  background: var(--dd-accent-soft);
  border: 1px solid var(--dd-accent-ring);
}

// Navigation
.sidebar-nav {
  flex: 1;
  min-height: 0;
  // 指示条按 offsetTop 定位，参照物就是这里
  position: relative;
  overflow-y: auto;
  overflow-x: hidden;
  padding: 0 10px 10px;
  scrollbar-width: none;

  &::-webkit-scrollbar {
    display: none;
  }
}

// 分组标题。模板上另挂了一个公开类 .dd-nav-group-title（无样式），给主题工坊的自定义 CSS 片段
// 「隐藏分组标题」当稳定钩子用；改这里的类名时别动那个公开类
.nav-group-label {
  height: 34px;
  padding: 14px 12px 6px;
  font-size: 11px;
  font-weight: 600;
  letter-spacing: 0.12em;
  color: var(--dd-text-3);
  white-space: nowrap;
  overflow: hidden;
  user-select: none;
}

.nav-item {
  position: relative;
  // 压在指示条（z-index 0）之上
  z-index: 1;
  display: flex;
  align-items: center;
  gap: 12px;
  width: 100%;
  height: 40px;
  margin: 0 0 2px;
  padding: 0 10px 0 15px;
  border: none;
  border-radius: var(--dd-nav-item-radius);
  background: transparent;
  color: var(--dd-text-2);
  font: inherit;
  font-size: 14px;
  text-align: left;
  white-space: nowrap;
  cursor: pointer;
  transition:
    color var(--dd-motion-fast) var(--dd-ease-standard),
    background-color var(--dd-motion-fast) var(--dd-ease-standard),
    padding 0.6s var(--dd-spring-soft);

  &:hover:not(.is-active) {
    color: var(--dd-text);
    background: var(--dd-surface-3);
  }

  &:focus-visible {
    outline: 2px solid var(--dd-accent-ring);
    outline-offset: -2px;
  }

  // 选中项：底色由滑动指示条给，这里只改字色与字重（读排版级令牌，素白是正文色）
  &.is-active {
    color: var(--dd-nav-active-fg);
    font-weight: 600;
  }
}

.nav-icon {
  position: relative;
  flex-shrink: 0;
  display: inline-flex;
  width: 18px;
  height: 18px;
}

.nav-label {
  flex: 1;
  min-width: 0;
  // 菜单标题都很短，正常不会溢出；留一手省略而不是撑破侧栏
  overflow: hidden;
  text-overflow: ellipsis;
}

.nav-badge {
  flex-shrink: 0;
}

// 滑动指示条：底色 / 圆角 / 左侧竖条宽度全读 --dd-nav-* 令牌。
// 素白主题给的竖条宽是 0、底色是灰色药丸，所以不需要按主题名写任何分支。
.nav-indicator {
  position: absolute;
  top: 0;
  left: 10px;
  right: 10px;
  z-index: 0;
  border-radius: var(--dd-nav-item-radius);
  background: var(--dd-nav-active-bg);
  pointer-events: none;
  transition: opacity 0.2s var(--dd-ease-standard);

  // 第一次定位之后才带上弹簧，避免一进页面就从顶上滑下来
  &.is-animated {
    transition:
      transform 0.55s var(--dd-spring),
      height 0.55s var(--dd-spring),
      opacity 0.2s var(--dd-ease-standard);
  }

  &.is-hidden {
    opacity: 0;
  }

  &::before {
    content: "";
    position: absolute;
    left: 0;
    top: 9px;
    bottom: 9px;
    width: var(--dd-nav-indicator-width);
    border-radius: var(--dd-radius-pill);
    background: linear-gradient(var(--dd-accent), var(--dd-accent-2));
  }
}

// 收起态：图标在 72px 的浮岛里居中（10px 列表内边距 + 17px 项内边距 + 18px 图标 + 17px + 10px）
.layout-aside.is-collapsed {
  .sidebar-logo {
    padding-left: 20px;
  }

  .nav-item {
    padding-left: 17px;
  }

  .logo-title,
  .logo-version,
  .nav-label,
  .nav-badge,
  .user-info {
    opacity: 0;
    pointer-events: none;
  }

  // 分组标题收成一道 14px 的间隔
  .nav-group-label {
    height: 14px;
    padding: 0;
    opacity: 0;
  }

  .nav-collapsed-dot {
    display: block;
  }

  .sidebar-foot {
    flex-direction: column;
    padding-left: 10px;
    padding-right: 10px;
  }

  // 用户卡只剩头像：名字那一栏收成 0 宽，头像才能在 72px 的浮岛里居中
  .user-card {
    gap: 0;
    padding-left: 0;
    padding-right: 0;
  }

  .user-info {
    flex: 0 0 0;
    width: 0;
    overflow: hidden;
  }
}

// 收起态的角标圆点（展开态不显示）
.nav-collapsed-dot {
  display: none;
  position: absolute;
  top: -3px;
  right: -4px;
  width: 7px;
  height: 7px;
  // 圆形承载语义：这是「有未读/异常」的状态角标点，两种圆角模式下都保持圆形，不吃 --dd-radius-* 令牌。
  border-radius: 50%;
  background: var(--dd-err);
  // 描边取实色卡底，让圆点从图标上"浮"出来又不用阴影（侧栏底可能是半透明，所以不用侧栏底色）
  border: 1.5px solid var(--dd-surface-solid);

  &.is-primary {
    background: var(--dd-accent);
  }

  &.is-warning {
    background: var(--dd-warn);
  }
}

// 抽屉（移动端）：没有滑动指示条，当前项直接给静态选中底色与竖条
.sidebar-nav.is-static .nav-item.is-active {
  background: var(--dd-nav-active-bg);

  &::before {
    content: "";
    position: absolute;
    left: 0;
    top: 9px;
    bottom: 9px;
    width: var(--dd-nav-indicator-width);
    border-radius: var(--dd-radius-pill);
    background: linear-gradient(var(--dd-accent), var(--dd-accent-2));
  }
}

.sidebar-critters-slot {
  flex-shrink: 0;
  max-height: 0;
  opacity: 0;
  overflow: hidden;
  transition:
    max-height var(--dd-motion-normal) var(--dd-ease-standard),
    opacity var(--dd-motion-fast) var(--dd-ease-standard);

  &.is-visible {
    max-height: 58px;
    opacity: 1;
  }
}

@media screen and (max-height: 700px) {
  .sidebar-critters-slot {
    display: none;
  }
}

// 侧栏底部：用户卡 + 收起按钮
.sidebar-foot {
  flex-shrink: 0;
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 10px;
  border-top: var(--dd-border-width, 1px) solid var(--dd-divider, var(--dd-border));
}

.user-card {
  flex: 1;
  min-width: 0;
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 6px 8px;
  border: none;
  border-radius: var(--dd-radius-control);
  background: transparent;
  font: inherit;
  text-align: left;
  cursor: pointer;
  transition: background-color var(--dd-motion-fast) var(--dd-ease-standard);

  // hover 只换底色
  &:hover {
    background: var(--dd-surface-3);
  }
}

.user-avatar {
  width: 32px;
  height: 32px;
  // 圆形承载语义：头像是用户上传/来自 GitHub 的人像，本身按圆形构图，
  // 直角硬切会把边缘内容裁掉。两种圆角模式下都保持圆形，不吃 --dd-radius-* 令牌。
  border-radius: 50%;
  object-fit: cover;
  flex-shrink: 0;
}

// 首字母头像：与实心主按钮同一对颜色（handoff §7，14 套配色都 ≥ 4.5:1）
.user-avatar--placeholder {
  display: flex;
  align-items: center;
  justify-content: center;
  background: var(--dd-btn-primary-bg);
  color: var(--dd-btn-primary-fg);
  font-size: 14px;
  font-weight: 700;
}

.user-info {
  min-width: 0;
  display: flex;
  flex-direction: column;
  white-space: nowrap;
}

.user-name {
  font-size: 13px;
  font-weight: 600;
  color: var(--dd-text);
  line-height: 1.3;
  overflow: hidden;
  text-overflow: ellipsis;
}

.user-role {
  font-size: 11px;
  color: var(--dd-text-3);
  line-height: 1.3;
}

.sidebar-collapse-btn {
  flex-shrink: 0;
  width: 36px;
  height: 36px;
  display: flex;
  align-items: center;
  justify-content: center;
  border: none;
  border-radius: var(--dd-radius-control);
  background: transparent;
  color: var(--dd-text-2);
  cursor: pointer;
  transition:
    background-color var(--dd-motion-fast) var(--dd-ease-standard),
    color var(--dd-motion-fast) var(--dd-ease-standard);

  &:hover {
    background: var(--dd-surface-3);
    color: var(--dd-text);
  }
}

// ==================== Header ====================
// 顶栏本身透明，只有一颗颗玻璃胶囊（.dd-glass 提供半透明底 + 模糊，玻璃关闭时自动退回实色）
.layout-header {
  height: 68px;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 12px 20px 4px;
  position: sticky;
  top: 0;
  z-index: 20;
  flex-shrink: 0;
}

// 顶栏所有胶囊的共同外观：边框、圆角、阴影都读主题令牌
.header-toggle-btn,
.header-breadcrumb,
.header-search,
.header-runcap,
.header-tools {
  height: var(--dd-header-chip-height);
  border: var(--dd-border-width) solid var(--dd-border);
  border-radius: var(--dd-radius-pill);
  box-shadow: var(--dd-shadow-card);
}

.header-left {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
}

.header-toggle-btn {
  width: var(--dd-header-chip-height);
  flex-shrink: 0;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 0;
  cursor: pointer;
  color: var(--dd-text-2);
  transition: color var(--dd-motion-fast) var(--dd-ease-standard);

  &:hover {
    color: var(--dd-text);
  }
}

.header-breadcrumb {
  display: flex;
  align-items: center;
  gap: 6px;
  min-width: 0;
  padding: 0 14px;
  font-size: 13px;
  color: var(--dd-text-2);
}

.breadcrumb-sep {
  color: var(--dd-text-3);
  font-weight: 300;
  font-size: 15px;
}

.breadcrumb-section {
  white-space: nowrap;
  color: var(--dd-text-2);
}

.breadcrumb-current {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  letter-spacing: 0.1px;
  color: var(--dd-text);
  font-weight: 600;
}

.header-right {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-shrink: 0;
}

// 1. 搜索胶囊
.header-search {
  display: flex;
  align-items: center;
  gap: 10px;
  width: clamp(200px, 26vw, 360px);
  padding: 0 7px 0 14px;
  font: inherit;
  text-align: left;
  color: var(--dd-text-3);
  cursor: pointer;
  transition:
    color var(--dd-motion-fast) var(--dd-ease-standard),
    border-color var(--dd-motion-fast) var(--dd-ease-standard);

  &:hover {
    color: var(--dd-text-2);
    border-color: var(--dd-border-strong);
  }
}

.search-icon {
  flex-shrink: 0;
}

.search-placeholder {
  flex: 1;
  min-width: 0;
  font-size: 13px;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.search-kbd {
  flex-shrink: 0;
  padding: 2px 6px;
  border-radius: var(--dd-radius-control);
  border: 1px solid var(--dd-border-strong);
  background: var(--dd-surface-2);
  color: var(--dd-text-2);
  font-family: var(--dd-font-mono);
  font-size: 11px;
  line-height: 1.4;
}

// 2. 运行中胶囊
.header-runcap {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 0 14px;
  font: inherit;
  font-size: 13px;
  font-weight: 600;
  white-space: nowrap;
  color: var(--dd-text);
  cursor: pointer;
  transition: border-color var(--dd-motion-fast) var(--dd-ease-standard);

  &:hover,
  &.is-open {
    border-color: var(--dd-accent-ring);
  }
}

.runcap-dot {
  position: relative;
  width: 8px;
  height: 8px;
  // 状态灯：天然正圆
  border-radius: 50%;
  background: var(--dd-info);

  // 呼吸扩散的光圈（关闭档由全局去掉动画）
  &::after {
    content: "";
    position: absolute;
    inset: 0;
    border-radius: 50%;
    background: var(--dd-info);
    animation: dd-runcap-ping 1.6s var(--dd-ease-standard) infinite;
  }
}

@keyframes dd-runcap-ping {
  from {
    opacity: 0.6;
    transform: scale(1);
  }
  to {
    opacity: 0;
    transform: scale(2.6);
  }
}

.runcap-num {
  font-variant-numeric: tabular-nums;
}

// 3~6. 工具胶囊：明暗 / 主题工坊 / 铃铛 / 用户
.header-tools {
  display: flex;
  align-items: center;
  gap: 2px;
  padding: 0 3px;
}

.header-icon-btn {
  // 放进 40px 的胶囊里上下各留 3px；按钮本身是正圆（图标按钮天然胶囊）
  width: 32px;
  height: 32px;
  flex-shrink: 0;
  border-radius: var(--dd-radius-pill);
  border: none;
  background: transparent;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 0;
  cursor: pointer;
  color: var(--dd-text-2);
  transition:
    background-color var(--dd-motion-fast) var(--dd-ease-standard),
    color var(--dd-motion-fast) var(--dd-ease-standard);
  position: relative;

  &:hover {
    background: var(--dd-surface-3);
    color: var(--dd-text);
  }
}

// 铃铛角标：实心危险色底 + 状态色上的文字色（实心状态色那一对令牌）
.bell-count {
  position: absolute;
  top: 1px;
  right: -1px;
  min-width: 16px;
  height: 16px;
  padding: 0 4px;
  border-radius: var(--dd-radius-pill);
  background: var(--dd-err-solid);
  color: var(--dd-on-status);
  font-size: 10px;
  font-weight: 700;
  line-height: 16px;
  text-align: center;
  font-variant-numeric: tabular-nums;
  pointer-events: none;
}

.header-user {
  display: flex;
  align-items: center;
  gap: 8px;
  height: 32px;
  padding: 2px 10px 2px 2px;
  border-radius: var(--dd-radius-pill);
  cursor: pointer;
  transition: background-color var(--dd-motion-fast) var(--dd-ease-standard);
  outline: none;

  // hover 只换底色
  &:hover,
  &:focus-visible {
    background: var(--dd-surface-3);
  }
}

.header-user-avatar {
  width: 28px;
  height: 28px;
  // 圆形承载语义：同 .user-avatar，头像本身按圆形构图，直角硬切会裁掉边缘内容。
  border-radius: 50%;
  object-fit: cover;
  flex-shrink: 0;
}

.header-user-avatar--placeholder {
  display: flex;
  align-items: center;
  justify-content: center;
  background: var(--dd-btn-primary-bg);
  color: var(--dd-btn-primary-fg);
  font-size: 12px;
  font-weight: 700;
}

.header-user-name {
  max-width: 120px;
  overflow: hidden;
  text-overflow: ellipsis;
  font-size: 13px;
  font-weight: 500;
  color: var(--dd-text);
  white-space: nowrap;
}

// 铃铛弹出层的内容（slot 内容归本组件作用域，scoped 能命中；外层 .el-popover 由 EP 自己画）
.attention-pop {
  display: flex;
  flex-direction: column;
  gap: 10px;

  // 条目较多时在浮层内滚动，避免在桌面 body overflow:hidden 下冲出视口底边无法查看
  :deep(.attn-list) {
    max-height: min(60dvh, 420px);
    overflow-y: auto;
    overscroll-behavior: contain;
  }
}

.attention-pop__head {
  display: flex;
  align-items: baseline;
  gap: 8px;
  padding: 2px 2px 0;
}

.attention-pop__title {
  font-size: 14px;
  font-weight: 700;
  color: var(--dd-text);
}

.attention-pop__count {
  font-size: 12px;
  color: var(--dd-text-3);
}

// ==================== Main ====================
.layout-main-wrap {
  display: flex;
  flex-direction: column;
  height: 100%;
  min-width: 0;
  min-height: 0;
  overflow: hidden;
}

.layout-main {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
  overflow: hidden;
  // 顶部 12px：顶栏已经自带下内边距，再留 20px 会显得松
  padding: 12px 20px 20px;
}

.route-shell {
  flex: 1;
  min-height: 0;
  min-width: 0;
  display: flex;
  flex-direction: column;
  overflow: hidden;
  position: relative;
  // 脚本页的调试窗 / 代码运行器打开时会撤掉这里的 containment 以铺满视口，见 views/scripts/components/ScriptExecutionDialogs.vue（issue #171）
  contain: layout style;

  > :deep(*) {
    min-width: 0;
  }

  > :deep(.dd-fixed-page) {
    flex: 1 1 auto;
    width: 100%;
    min-height: 0;
  }

  > :deep(.dd-scroll-page) {
    flex: 1 1 auto;
    width: 100%;
    min-height: 0;
  }
}

// 中等宽度：搜索胶囊只留图标与快捷键提示
@media screen and (max-width: 1180px) and (min-width: 769px) {
  .header-search {
    width: auto;
  }

  .search-placeholder {
    display: none;
  }
}

// 窄桌面（769~1024px，例如分屏或横屏平板）：隐藏次要标签，避免右侧头像与工具胶囊被 overflow:hidden 裁切
@media screen and (max-width: 1024px) and (min-width: 769px) {
  .breadcrumb-section,
  .breadcrumb-sep,
  .runcap-label,
  .header-user-name {
    display: none;
  }

  .header-runcap {
    padding: 0 11px;
  }

  .header-user {
    padding: 4px;
  }
}

@media screen and (max-height: 820px) and (min-width: 769px) {
  // 矮屏：外壳本身已铺满视口，这里只收紧 header / main 的内边距，
  // 给内容腾出更多垂直空间
  .layout-header {
    height: 60px;
    padding: 10px 16px 2px;
  }

  .layout-main {
    padding: 10px 16px 14px;
  }
}

// ==================== Page transition ====================
// 不支持 View Transitions（或动效关闭）时的切页动画；支持时由 <html> 上的 dd-vt 类关掉（styles/motion.scss）。
// 切页：新页面淡入并从下方 8px 升起，旧页面纯淡出（#132，v3.2.8）。
// 原来是 180ms + scale 0.992 + decelerate：那条曲线前段极陡，肉眼可见的变化只有头 50~70ms，
// 0.992 的缩放在 1200px 宽的内容区上两边各只动约 5px，再叠上 80ms 的离场，观感接近瞬切。
//   - 进场吃 --dd-motion-page-enter（280ms）+ emphasized。用位移不用缩放：缩放会让子元素在进场期间的
//     getBoundingClientRect 失真，表格列宽、浮层定位这类按尺寸算的逻辑会算错。
//   - 离场吃 --dd-motion-page-leave（120ms），纯淡出，被 z-index:1 的新页压在下面，不抢注意力。
//   - 刻意不直接改 --dd-motion-page：它被十几个页面的 dd-*-rise-in 共用。
// 仍然不用 out-in：旧页面绝对定位短暂淡出，新页面立即进入，避免先卸载旧页后等待新页导致白屏。
// 仍然不用 filter: blur（整页 blur 是切页卡顿的主因）。
.page-shell-enter-active {
  position: relative;
  z-index: 1;
  // 进场不写 both（同 global.scss 里弹窗那条约束）：动画收尾后 transform 自然清空，
  // 页面根上残留的 transform 会成为页内 position:fixed 元素的包含块，没 teleport 的弹窗会跟着错位。
  // 这里没有 delay，起点帧用不着 backwards 填充；终点帧与元素的自然状态一致，去掉 forwards 也不会闪。
  animation: dd-page-shell-enter var(--dd-motion-page-enter) var(--dd-ease-emphasized);
  will-change: opacity, transform;
}

.page-shell-leave-active {
  position: absolute;
  inset: 0;
  z-index: 0;
  width: 100%;
  pointer-events: none;
  // 离场必须留着 both：终点是 opacity 0，去掉 forwards 的话，Vue 摘掉节点前会闪回完全不透明一帧。
  animation: dd-page-shell-leave var(--dd-motion-page-leave) var(--dd-ease-standard) both;
  will-change: opacity;
}

@keyframes dd-page-shell-enter {
  from {
    opacity: 0;
    transform: translate3d(0, 8px, 0);
  }
  to {
    opacity: 1;
    transform: none;
  }
}

@keyframes dd-page-shell-leave {
  from {
    opacity: 1;
  }
  to {
    opacity: 0;
  }
}

// ==================== Mobile drawer ====================
// 抽屉底色由 EP 的 --el-bg-color（实色 overlay）给；这里只排版
.mobile-logo {
  padding: 16px 16px 8px;
}

.mobile-nav {
  flex: 1;
}

// ==================== Mobile responsive ====================
@media screen and (max-width: 768px) {
  .layout-container {
    --dd-header-chip-height: 36px;
  }

  // 外壳在桌面端也已全屏铺满，这里不需要再复位；只调整内部间距与滚动行为，drawer 行为不变
  .layout-header {
    height: 56px;
    padding: 10px 12px 2px;
    gap: 6px;
  }

  .layout-main {
    overflow-y: auto;
    // 🔴 F1（v3.3.1 #143 → v3.3.3 #148）：这个滚动容器在移动端【禁止】带任何会形成层叠上下文的属性——
    //   z-index、transform、filter、opacity 动画、will-change、contain、isolation、backdrop-filter 一律不许加，
    //   也不许加回 -webkit-overflow-scrolling: touch。
    // 原因：本项目的 el-dialog / el-drawer 默认【不 teleport】（EP 2.13.5 appendToBody 默认 false），
    //   遮罩（position:fixed）原地渲染在 .layout-main 里面。iOS WebKit 有个至今未修的缺陷（bug 160953）：
    //   「可滚动 + 自成层叠上下文」的容器会把 fixed 后代的合成层一起裁进自己的矩形，而命中测试不受影响。
    //   iPhone 上表现为：弹窗顶部被横幅 + 顶栏「挡住」一截，点那一截却点不到顶栏（点击已经落在弹窗上）。
    //   这不是 z-index 谁大谁小的问题，是弹窗在那块区域根本没被画出来。
    //   Chromium 按包含块链裁剪，DevTools 设备模拟用的也是 Blink，所以桌面上怎么都复现不出来。
    // 三版都在往这个触发条件上撞，别再踩第四次：
    //   - v3.3.0：这里有 -webkit-overflow-scrolling: touch，WebKit 把它强制成 z-index:0 的层叠上下文，
    //     遮罩被困在里面、整体排在顶栏之下，× 点不到（#143）；
    //   - v3.3.1：删掉了它（对），又加了 position: relative; z-index: 21 当「第二道保险」（错）：
    //     点击层级对了，画面仍被裁进内容区（#144）；
    //   - v3.3.2：只在 global.scss 加了弹窗整屏的几何规则，对裁剪没有作用，所有原地弹窗照旧被裁（#148）。
    // v3.3.3 起这里不成层叠上下文：遮罩直接在根上下文里用 EP 的 2000+ 和顶栏（20）、演示横幅（20）比，天然在上面。
    //   position: relative 一起删了：它只是为了让 z-index 生效才加的；页面里绝对定位元素的包含块是 .route-shell
    //   （自带 position: relative），不受影响。
    // 同一件事的另两处：global.scss 在弹窗开着时把这里切成 overflow-y: hidden（锁住背后的列表）；
    //   public/assets/optimization.css 在手机上撤掉 body 的 transform。
    // iOS 13 起 overflow 滚动默认就带惯性，构建目标 Safari 16，用不着 -webkit-overflow-scrolling。
    //
    // R1：所有页面第一行离顶栏 12px，统一由这里的上内边距（= --dd-page-gutter-x）提供。
    // issue 说的「上边距改 0」本意是去掉工具栏上外边距与这里的上内边距叠出来的双倍空隙，
    // 所以归零的是 .dd-mobile-toolbar / .dd-mobile-batch-bar 的上外边距（global.scss），不是这里 ——
    // 这里一归零，配置文件、仪表板、脚本、设置、用户、通知、OpenAPI、接口文档这些没有移动端工具栏的页面，
    // 内容就直接贴着顶栏（v3.3.1 开发中浏览器实测踩过）。页面不要再自己补「离顶栏」的上外边距，否则又会叠出双倍。
    // 横向留白同样吃 --dd-page-gutter-x。v3.3.2 / issue #144 起它是这个令牌的唯一消费方：
    // 横滑行（.dd-mobile-bleed / .dd-mobile-batch-bar）不再用等量负外边距抵消它贴到屏幕边，
    // 改成跟着这条留白走，屏幕最边上那 12px 让给系统的侧滑返回手势。
    // 顶部安全区不在这里处理：index.html 没开 viewport-fit=cover，env() 恒为 0；
    // 将来要开，也应由顶栏承担顶部安全区，而不是内容区。
    padding: var(--dd-page-gutter-x) var(--dd-page-gutter-x) calc(16px + env(safe-area-inset-bottom));
  }

  // 移动端 .route-shell 必须能被子内容撑高，否则被 flex column 容器压缩到等于 .layout-main 的可视高度，
  // 子页面 overflow visible 的内容画在容器外但不计入 box 尺寸，.layout-main 看不到滚动需求。
  // 桌面端依赖 flex: 1 + min-height: 0 + overflow: hidden 实现内嵌滚动；移动端反过来：禁止收缩 + 无 contain。
  .route-shell {
    flex: 1 0 auto;
    min-height: 0;
    overflow: visible;
    contain: none;

    > :deep(.dd-fixed-page),
    > :deep(.dd-scroll-page) {
      flex: 1 0 auto;
      min-height: 0;
      overflow: visible;
    }
  }

  // 面包屑只留当前页标题，给右侧按钮让位
  .header-breadcrumb {
    padding: 0 12px;
    font-size: 12px;
  }

  .breadcrumb-section,
  .breadcrumb-sep {
    display: none;
  }

  .header-right {
    gap: 6px;
  }

  // 搜索收成一个圆形图标按钮
  .header-search {
    width: var(--dd-header-chip-height);
    padding: 0;
    justify-content: center;
  }

  .search-placeholder,
  .search-kbd {
    display: none;
  }

  // 运行中胶囊只留圆点与数字
  .header-runcap {
    padding: 0 11px;
  }

  .runcap-label {
    display: none;
  }

  .header-tools {
    gap: 0;
    padding: 0 2px;
  }

  // 36px 的胶囊里放 32px 按钮
  .header-icon-btn {
    width: 32px;
    height: 32px;
  }

  // 移动端不显示用户名，只剩 28px 头像：四周各 2px，收成与图标按钮一样的 32×32
  .header-user {
    padding: 2px;
  }
}

// 极窄手机（≤420px，如 360px 屏）：再收紧顶栏胶囊间距与内边距，避免运行中胶囊出现时面包屑被压到仅剩 1~2 字
@media screen and (max-width: 420px) {
  .layout-header {
    gap: 6px;
    padding: 10px 8px 2px;
  }

  .header-breadcrumb {
    padding: 0 8px;
  }

  .header-right {
    gap: 4px;
  }

  .header-runcap {
    padding: 0 8px;
  }

  .header-icon-btn {
    width: 30px;
    height: 30px;
  }
}
</style>
