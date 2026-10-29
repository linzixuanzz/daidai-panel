<script setup lang="ts">
import { ref, computed, onMounted, onBeforeUnmount, watch } from 'vue'
import { useRouter, useRoute } from 'vue-router'
import { useAuthStore } from '@/stores/auth'
import { useThemeStore } from '@/stores/theme'
import { useBadgesStore } from '@/stores/badges'
import DdBadge from '@/components/ui/DdBadge.vue'
import { systemApi } from '@/api/system'
import { loadPanelSettings as loadCachedPanelSettings } from '@/utils/panelSettings'
import { reloadIfWebVersionStale } from '@/utils/chunkReload'
import { warmMonacoOnEngineSwitch } from '@/utils/monacoWarmup'
import { useResponsive } from '@/composables/useResponsive'
import { preloadPanelRoutes, preloadRouteByPath } from '@/router'
import {
  Bell,
  Box,
  Connection,
  Document,
  Download,
  Expand,
  Fold,
  Key,
  Monitor,
  Moon,
  Odometer,
  Operation,
  Setting,
  SetUp,
  Sunny,
  Tickets,
  Timer,
  User,
  UserFilled,
} from '@element-plus/icons-vue'

const router = useRouter()
const route = useRoute()
const authStore = useAuthStore()
const themeStore = useThemeStore()
const badgesStore = useBadgesStore()
const { isMobile } = useResponsive()
const isCollapsed = ref(false)
const drawerVisible = ref(false)
const panelTitle = ref('呆呆面板')
const panelIcon = ref('')
const panelVersion = ref('')
const routeCacheMax = 14

// 侧边栏 logo 的兜底图标。
// 这里不能写死 '/favicon-512.webp'：:src 是 v-bind 表达式，不经过 Vite 的 transformAssetUrls，
// 构建产物里会原样保留裸绝对路径。面板挂在反代子路径或 GitHub Pages 项目站下时，
// 这个路径会指到站点根而 404，侧边栏 logo 变破图（这两处 <img> 没有 @error 兜底）。
// import.meta.env.BASE_URL 结尾恒带 '/'，根路径部署时就是 '/'，行为与改动前一致。
const defaultPanelIcon = `${import.meta.env.BASE_URL}favicon-512.webp`
const panelIconSrc = computed(() => panelIcon.value || defaultPanelIcon)

const roleLevel: Record<string, number> = {
  viewer: 1,
  operator: 2,
  admin: 3,
}

function hasRole(minRole: string) {
  const currentRole = authStore.user?.role
  if (!currentRole) return false
  return (roleLevel[currentRole] || 0) >= (roleLevel[minRole] || 0)
}

const canAccessAdmin = computed(() => hasRole('admin'))

const workspaceItems = [
  { index: '/dashboard', title: '仪表板', icon: Odometer, minRole: 'viewer' },
  { index: '/tasks', title: '定时任务', icon: Timer, minRole: 'viewer' },
  { index: '/subscriptions', title: '订阅管理', icon: Download, minRole: 'operator' },
  { index: '/envs', title: '环境变量', icon: Setting, minRole: 'operator' },
  { index: '/config-file', title: '配置文件', icon: Document, minRole: 'admin' },
  { index: '/logs', title: '执行日志', icon: Tickets, minRole: 'viewer' },
  { index: '/scripts', title: '脚本管理', icon: Document, minRole: 'operator' },
  { index: '/deps', title: '依赖管理', icon: Box, minRole: 'admin' },
  { index: '/docs/api', title: '接口文档', icon: Connection, minRole: 'viewer' },
  { index: '/profile', title: '个人设置', icon: User, minRole: 'viewer' },
]

const adminItems = [
  { index: '/admin/settings', title: '系统设置', icon: SetUp, minRole: 'admin' },
  { index: '/admin/notifications', title: '通知渠道', icon: Bell, minRole: 'admin' },
  { index: '/admin/users', title: '用户管理', icon: UserFilled, minRole: 'admin' },
  { index: '/admin/open-api', title: 'Open API', icon: Key, minRole: 'admin' },
]

const filteredWorkspaceItems = computed(() =>
  workspaceItems.filter(item => hasRole(item.minRole))
)

const filteredAdminItems = computed(() =>
  canAccessAdmin.value ? adminItems : []
)

const activeMenu = computed(() => route.path)

const preloadableMenuPaths = computed(() =>
  [...filteredWorkspaceItems.value, ...filteredAdminItems.value]
    .map(item => item.index)
    .filter(index => index !== route.path)
)

const breadcrumb = computed(() => {
  const matched = [...route.matched].reverse().find(item => item.meta.section)
  const section = matched?.meta.section === 'admin' ? '管理后台' : '工作台'
  const title = route.meta.title as string || ''
  return { section, title }
})

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

/**
 * 侧栏菜单角标。
 *
 * 只挂在「有事要处理」的菜单上：运行中的任务、今天失败的日志、拉取失败的订阅、
 * 装失败或正在装的依赖、以及有新版本时的系统设置。数据由 stores/badges.ts 轮询，
 * 服务端已按角色裁剪，看不到某个菜单的用户拿到的就是 0，这里不必再判权限。
 *
 * 刻意【不】给每个菜单都挂数字：角标是打断，全挂满等于全不挂，用户会直接学会忽略它。
 */
function badgeOf(path: string) {
  return badgesStore.menuBadges[path]
}

// 切到 Monaco 引擎时立即预热（#133，utils/monacoWarmup.ts）。挂在布局上而不是某个编辑器页：
// 引擎可能在任一编辑器页的齿轮菜单里切，布局是它们登录后一直在的共同祖先。
let stopEngineSwitchWarmup: (() => void) | null = null

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
})

onBeforeUnmount(() => {
  // 退出登录会卸载整个布局。不停表的话定时器会继续打 /system/badges，
  // 每次都 401，把用户刚跳到的登录页反复推一遍。
  badgesStore.stop()
  stopEngineSwitchWarmup?.()
  stopEngineSwitchWarmup = null
})

watch(isMobile, (mobile) => {
  if (mobile) {
    isCollapsed.value = true
    return
  }
  drawerVisible.value = false
}, { immediate: true })

watch(() => authStore.user?.role, () => {
  scheduleMenuPreload()
})

function handleMenuSelect(index: string) {
  preloadPage(index)
  router.push(index)
  if (isMobile.value) drawerVisible.value = false
}

function preloadPage(index: string) {
  void preloadRouteByPath(index)
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
  }
}

async function handleLogout() {
  await authStore.logout()
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
  <el-container class="layout-container">
    <!-- Desktop Sidebar -->
    <aside v-if="!isMobile" class="layout-aside" :class="{ 'is-collapsed': isCollapsed }">
      <!-- Logo -->
      <div class="sidebar-logo" :class="{ 'is-collapsed': isCollapsed }">
        <div class="logo-inner">
          <div class="logo-icon-wrap">
            <img :src="panelIconSrc" alt="logo" class="logo-icon" />
          </div>
          <template v-if="!isCollapsed">
            <span class="logo-title">{{ panelTitle }}</span>
            <span v-if="panelVersion" class="logo-version">v{{ panelVersion }}</span>
          </template>
        </div>
      </div>

      <!-- Scrollable navigation area -->
      <div class="sidebar-nav">
        <el-menu
          :default-active="activeMenu"
          :collapse="isCollapsed"
          :collapse-transition="false"
          background-color="transparent"
          @select="handleMenuSelect"
        >
          <el-menu-item
            v-for="item in filteredWorkspaceItems"
            :key="item.index"
            :index="item.index"
            @mouseenter="preloadPage(item.index)"
            @focusin="preloadPage(item.index)"
          >
            <el-icon>
              <component :is="item.icon" />
              <!-- 折叠态的角标：#title 插槽整体不渲染，数字没地方放，退化成图标右上角一个方点。
                   显隐纯靠 CSS 的 .el-menu--collapse 祖先判断，不用 isCollapsed —— 移动端
                   isCollapsed 恒为 true，但抽屉里的菜单没传 :collapse，实际是展开的，用 JS 状态会误判。 -->
              <span
                v-if="badgeOf(item.index)"
                class="menu-collapsed-dot"
                :class="`is-${badgeOf(item.index)!.level}`"
              />
            </el-icon>
            <template #title>
              <span class="menu-title-text">{{ item.title }}</span>
              <DdBadge
                v-if="badgeOf(item.index)"
                :value="badgeOf(item.index)!.count"
                :dot="badgeOf(item.index)!.dot"
                :level="badgeOf(item.index)!.level"
                :title="item.title"
              />
            </template>
          </el-menu-item>
        </el-menu>

        <!-- Admin section -->
        <template v-if="filteredAdminItems.length > 0">
          <div v-if="!isCollapsed" class="nav-section-label">管理后台</div>
          <div v-else class="nav-section-divider"></div>
          <el-menu
            :default-active="activeMenu"
            :collapse="isCollapsed"
            :collapse-transition="false"
            background-color="transparent"
            @select="handleMenuSelect"
          >
            <el-menu-item
              v-for="item in filteredAdminItems"
              :key="item.index"
              :index="item.index"
              @mouseenter="preloadPage(item.index)"
              @focusin="preloadPage(item.index)"
            >
              <el-icon>
                <component :is="item.icon" />
                <span
                  v-if="badgeOf(item.index)"
                  class="menu-collapsed-dot"
                  :class="`is-${badgeOf(item.index)!.level}`"
                />
              </el-icon>
              <template #title>
                <span class="menu-title-text">{{ item.title }}</span>
                <DdBadge
                  v-if="badgeOf(item.index)"
                  :value="badgeOf(item.index)!.count"
                  :dot="badgeOf(item.index)!.dot"
                  :level="badgeOf(item.index)!.level"
                  :title="item.title"
                />
              </template>
            </el-menu-item>
          </el-menu>
        </template>
      </div>

      <!-- User card -->
      <div class="sidebar-user" :class="{ 'is-collapsed': isCollapsed }">
        <div class="user-card-inner" @click="router.push('/profile')">
          <img
            v-if="authStore.user?.avatar_url"
            :src="authStore.user.avatar_url"
            alt=""
            class="user-avatar"
          />
          <div v-else class="user-avatar user-avatar--placeholder">
            {{ (authStore.user?.username || 'U').charAt(0).toUpperCase() }}
          </div>
          <template v-if="!isCollapsed">
            <div class="user-info">
              <span class="user-name">{{ authStore.user?.username || 'User' }}</span>
              <span class="user-role">{{ authStore.user?.role === 'admin' ? '管理员' : '用户' }}</span>
            </div>
          </template>
        </div>
      </div>

      <!-- Collapse toggle -->
      <button class="sidebar-collapse-btn" @click="toggleSidebar">
        <el-icon><component :is="isCollapsed ? Expand : Fold" /></el-icon>
        <span v-if="!isCollapsed" class="collapse-text">收起菜单</span>
      </button>
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
        <div class="logo-inner">
          <div class="logo-icon-wrap">
            <img :src="panelIconSrc" alt="logo" class="logo-icon" />
          </div>
          <span class="logo-title">{{ panelTitle }}</span>
          <span v-if="panelVersion" class="logo-version">v{{ panelVersion }}</span>
        </div>
      </div>

      <div class="sidebar-nav mobile-nav">
        <el-menu
          :default-active="activeMenu"
          background-color="transparent"
          @select="handleMenuSelect"
        >
          <el-menu-item
            v-for="item in filteredWorkspaceItems"
            :key="item.index"
            :index="item.index"
            @mouseenter="preloadPage(item.index)"
            @focusin="preloadPage(item.index)"
          >
            <el-icon>
              <component :is="item.icon" />
              <!-- 折叠态的角标：#title 插槽整体不渲染，数字没地方放，退化成图标右上角一个方点。
                   显隐纯靠 CSS 的 .el-menu--collapse 祖先判断，不用 isCollapsed —— 移动端
                   isCollapsed 恒为 true，但抽屉里的菜单没传 :collapse，实际是展开的，用 JS 状态会误判。 -->
              <span
                v-if="badgeOf(item.index)"
                class="menu-collapsed-dot"
                :class="`is-${badgeOf(item.index)!.level}`"
              />
            </el-icon>
            <template #title>
              <span class="menu-title-text">{{ item.title }}</span>
              <DdBadge
                v-if="badgeOf(item.index)"
                :value="badgeOf(item.index)!.count"
                :dot="badgeOf(item.index)!.dot"
                :level="badgeOf(item.index)!.level"
                :title="item.title"
              />
            </template>
          </el-menu-item>
        </el-menu>

        <template v-if="filteredAdminItems.length > 0">
          <div class="nav-section-label">管理后台</div>
          <el-menu
            :default-active="activeMenu"
            background-color="transparent"
            @select="handleMenuSelect"
          >
            <el-menu-item
              v-for="item in filteredAdminItems"
              :key="item.index"
              :index="item.index"
              @mouseenter="preloadPage(item.index)"
              @focusin="preloadPage(item.index)"
            >
              <el-icon>
                <component :is="item.icon" />
                <span
                  v-if="badgeOf(item.index)"
                  class="menu-collapsed-dot"
                  :class="`is-${badgeOf(item.index)!.level}`"
                />
              </el-icon>
              <template #title>
                <span class="menu-title-text">{{ item.title }}</span>
                <DdBadge
                  v-if="badgeOf(item.index)"
                  :value="badgeOf(item.index)!.count"
                  :dot="badgeOf(item.index)!.dot"
                  :level="badgeOf(item.index)!.level"
                  :title="item.title"
                />
              </template>
            </el-menu-item>
          </el-menu>
        </template>
      </div>

      <div class="sidebar-user">
        <div class="user-card-inner" @click="router.push('/profile'); drawerVisible = false">
          <img
            v-if="authStore.user?.avatar_url"
            :src="authStore.user.avatar_url"
            alt=""
            class="user-avatar"
          />
          <div v-else class="user-avatar user-avatar--placeholder">
            {{ (authStore.user?.username || 'U').charAt(0).toUpperCase() }}
          </div>
          <div class="user-info">
            <span class="user-name">{{ authStore.user?.username || 'User' }}</span>
            <span class="user-role">{{ authStore.user?.role === 'admin' ? '管理员' : '用户' }}</span>
          </div>
        </div>
      </div>
    </el-drawer>

    <!-- Main content area -->
    <el-container class="layout-main-wrap">
      <!-- Header -->
      <header class="layout-header">
        <div class="header-left">
          <button class="header-toggle-btn" @click="toggleSidebar">
            <el-icon :size="18"><Operation /></el-icon>
          </button>
          <nav class="header-breadcrumb">
            <span class="breadcrumb-sep">›</span>
            <span class="breadcrumb-section">{{ breadcrumb.section }}</span>
            <template v-if="breadcrumb.title">
              <span class="breadcrumb-sep">›</span>
              <span class="breadcrumb-current">{{ breadcrumb.title }}</span>
            </template>
          </nav>
        </div>

        <div class="header-center"></div>

        <div class="header-right">
          <button
            class="header-icon-btn theme-toggle"
            :title="themeTitle"
            :aria-label="themeTitle"
            @click="themeStore.toggleTheme"
          >
            <el-icon :size="18"><component :is="themeIcon" /></el-icon>
          </button>
          <el-dropdown trigger="click">
            <div class="header-user">
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
                <el-dropdown-item divided @click="handleLogout">退出登录</el-dropdown-item>
              </el-dropdown-menu>
            </template>
          </el-dropdown>
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
    </el-container>
  </el-container>
</template>

<style scoped lang="scss">
// ==================== 全屏外壳 ====================
// 桌面与移动端一致：外壳直接铺满视口，无留白、无圆角、无阴影、无外描边。
// overflow: hidden 用于把内部滚动锁在 layout-main / route-shell 里，不让整页产生滚动条。
// 注意：内部滚动链（layout-main / route-shell / dd-*-page）保持不动。
.layout-container {
  margin: 0;
  height: 100dvh;
  background: var(--el-bg-color);
  overflow: hidden;
}

// ==================== Sidebar ====================
.layout-aside {
  width: 220px;
  display: flex;
  flex-direction: column;
  background: var(--el-bg-color);
  border-right: 1px solid var(--el-border-color-light);
  // 宽度过渡吃令牌（原来写死 0.25s，绕过了减少动效的降级）。
  // 去掉了原来的 will-change: width：width 不是合成器能加速的属性，
  // will-change 对它没有任何收益，只会让浏览器常驻一层额外的合成层。
  transition: width var(--dd-motion-normal) var(--dd-ease-standard);
  overflow: hidden;
  z-index: 10;

  &.is-collapsed {
    width: 64px;
  }
}

// Logo
.sidebar-logo {
  height: 60px;
  display: flex;
  align-items: center;
  padding: 8px 10px;
  flex-shrink: 0;
  border-bottom: 1px solid var(--el-border-color-light);
  // 去装饰渐变，用与侧栏一致的纯色底，靠底部 1px 分隔线划分区域
  background: var(--el-bg-color);

  &.is-collapsed {
    justify-content: center;
    padding: 8px;
  }
}

.logo-inner {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 7px 10px;
  border-radius: var(--dd-radius-control);
  background: var(--el-bg-color);
  border: 1px solid color-mix(in srgb, var(--el-color-primary) 10%, var(--el-border-color-lighter));
  transition: border-color var(--dd-motion-fast) var(--dd-ease-standard);
  width: 100%;
  min-height: 42px;

  // hover 只加深描边，不再叠阴影
  &:hover {
    border-color: color-mix(in srgb, var(--el-color-primary) 18%, var(--el-border-color-lighter));
  }
}

.is-collapsed .logo-inner {
  width: 40px;
  min-height: 40px;
  padding: 6px;
  justify-content: center;
}

.logo-icon-wrap {
  width: 28px;
  height: 28px;
  border-radius: var(--dd-radius-control);
  display: flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
  // 纯色底 + 1px 品牌色描边，去掉渐变、内高光与外投影，hover 也不再上浮
  background: var(--el-bg-color);
  border: 1px solid color-mix(in srgb, var(--el-color-primary) 18%, transparent);
}

.logo-icon {
  width: 16px;
  height: 16px;
}

.logo-title {
  font-size: 15px;
  font-weight: 700;
  color: var(--el-text-color-primary);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  flex: 1;
  min-width: 0;
}

.logo-version {
  flex-shrink: 0;
  padding: 3px 8px;
  border-radius: var(--dd-radius-control);
  font-family: var(--dd-font-mono);
  font-size: 10px;
  font-weight: 700;
  letter-spacing: 0.04em;
  color: var(--el-color-primary-dark-2);
  background: color-mix(in srgb, var(--el-color-primary) 10%, white);
  border: 1px solid color-mix(in srgb, var(--el-color-primary) 18%, transparent);
}

// Navigation
.sidebar-nav {
  flex: 1;
  position: relative;
  overflow-y: auto;
  overflow-x: hidden;
  padding: 6px 0;

  &::-webkit-scrollbar {
    width: 3px;
  }

  &::-webkit-scrollbar-thumb {
    background: transparent;
    // 3px 宽的细 thumb 属于天然胶囊，吃 pill 档：直角模式仍是细方条，圆角模式变胶囊条
    border-radius: var(--dd-radius-pill);
  }

  &:hover::-webkit-scrollbar-thumb {
    background: var(--el-border-color);
  }
}

// 收起态：Element Plus 把菜单项内容包进 tooltip 触发层，其默认 padding: 0 20px 会把
// 图标整体右推，导致与已居中的 logo 不在同一竖直轴上。清掉左右内距并居中，让收起态
// 导航图标与 logo / 用户头像 / 收起按钮统一居中对齐。
.sidebar-nav :deep(.el-menu--collapse .el-menu-item .el-menu-tooltip__trigger) {
  padding-left: 0;
  padding-right: 0;
  justify-content: center;
}

// ==================== 菜单角标 ====================
// 展开态：标题占满剩余宽度，把角标顶到菜单项最右侧对齐成一列。
// .el-menu-item 本身就是 flex 容器（EP 的默认样式），这里只需要给标题 flex: 1。
.menu-title-text {
  flex: 1;
  min-width: 0;
  // 菜单标题都很短，正常不会溢出；但面板标题可被用户改，留一手省略而不是撑破侧栏
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

// 收起态：#title 插槽整体不渲染，数字角标跟着消失，只在图标右上角留一个圆点。
// 定位上下文用 el-icon 自己（下面给它 position: relative），
// 而不是依赖 .el-menu-tooltip__trigger —— 那是 EP 的内部实现，位置和定位属性都可能变。
.sidebar-nav :deep(.el-menu-item .el-icon) {
  position: relative;
}

.menu-collapsed-dot {
  // 默认不显示：只有菜单真的处于收起态（EP 给 ul 挂 .el-menu--collapse）才亮出来。
  // 用 CSS 祖先判断而不是 v-if="isCollapsed"：移动端 isCollapsed 恒为 true，
  // 但抽屉里的 el-menu 没传 :collapse，实际是展开的——用 JS 状态会在移动端多冒一个点。
  display: none;
}

.sidebar-nav :deep(.el-menu--collapse) .menu-collapsed-dot {
  display: block;
  position: absolute;
  // el-icon 是 18px 字号的 24px 方框，贴右上角但留 1px 不要压到图标笔画上
  top: -1px;
  right: -3px;
  width: 7px;
  height: 7px;
  // 圆形承载语义：这是「有未读/异常」的状态角标点，圆点是通用可供性，
  // 方块会被误认成图标本身的一部分。两种圆角模式下都保持圆形，不吃 --dd-radius-* 令牌。
  border-radius: 50%;
  background: var(--el-color-danger);
  // 描边取侧栏底色，让圆点从图标上"浮"出来又不用阴影。
  // 侧栏底色是 --el-bg-color（见 .layout-aside），暗色下自动跟着变。
  border: 1.5px solid var(--el-bg-color);
}

.sidebar-nav :deep(.el-menu--collapse) .menu-collapsed-dot.is-primary {
  background: var(--el-color-primary);
}

.sidebar-nav :deep(.el-menu--collapse) .menu-collapsed-dot.is-warning {
  background: var(--el-color-warning);
}

.nav-section-label {
  padding: 16px 20px 6px;
  font-size: 11px;
  font-weight: 700;
  letter-spacing: 0.08em;
  text-transform: uppercase;
  color: var(--el-text-color-placeholder);
  user-select: none;
}

.nav-section-divider {
  height: 1px;
  margin: 8px 16px;
  background: var(--el-border-color-lighter);
}

// Quick Environments
.sidebar-envs {
  flex-shrink: 0;
  padding: 0 12px;
  border-top: 1px solid var(--el-border-color-lighter);
  margin-top: 4px;
}

.envs-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 10px 4px 4px;
}

.envs-title {
  font-size: 11px;
  font-weight: 700;
  letter-spacing: 0.06em;
  color: var(--el-text-color-placeholder);
  text-transform: uppercase;
}

.envs-add-btn {
  width: 20px;
  height: 20px;
  border-radius: var(--dd-radius-control);
  border: 1px solid var(--el-border-color-lighter);
  background: transparent;
  display: flex;
  align-items: center;
  justify-content: center;
  cursor: pointer;
  color: var(--el-text-color-placeholder);
  transition:
    background-color var(--dd-motion-fast) var(--dd-ease-standard),
    color var(--dd-motion-fast) var(--dd-ease-standard),
    border-color var(--dd-motion-fast) var(--dd-ease-standard);

  &:hover {
    border-color: var(--el-color-primary);
    color: var(--el-color-primary);
    background: var(--el-color-primary-light-9);
  }
}

.envs-list {
  display: flex;
  flex-direction: column;
  gap: 2px;
  padding-bottom: 8px;
}

.env-item {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 5px 6px;
  border-radius: var(--dd-radius-control);
  cursor: pointer;
  transition: background-color var(--dd-motion-fast) var(--dd-ease-standard);

  // hover 只换底色
  &:hover {
    background: var(--el-fill-color-light);
  }
}

.env-dot {
  width: 7px;
  height: 7px;
  // 7×7 的状态色点属于天然胶囊：pill 档下正好是圆点，直角档保持现有方点观感
  border-radius: var(--dd-radius-pill);
  flex-shrink: 0;
}

.env-info {
  min-width: 0;
  display: flex;
  flex-direction: column;
}

.env-name {
  font-size: 12px;
  font-weight: 500;
  color: var(--el-text-color-primary);
  line-height: 1.3;
}

.env-key {
  font-size: 10px;
  color: var(--el-text-color-placeholder);
  font-family: var(--dd-font-mono);
}

// User card
.sidebar-user {
  flex-shrink: 0;
  padding: 8px 12px;
  border-top: 1px solid var(--el-border-color-lighter);

  &.is-collapsed {
    padding: 8px;
    display: flex;
    justify-content: center;
  }
}

.user-card-inner {
  display: flex;
  position: relative;
  overflow: hidden;
  align-items: center;
  gap: 10px;
  padding: 8px 10px;
  border-radius: var(--dd-radius-control);
  cursor: pointer;
  transition: background-color var(--dd-motion-fast) var(--dd-ease-standard);

  // hover 只换底色：去掉上浮阴影与顶部白色高光扫过（::after 装饰层已整体移除）
  &:hover {
    background: var(--el-fill-color-light);
  }

  .is-collapsed & {
    padding: 6px;
    justify-content: center;
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

.user-avatar--placeholder {
  display: flex;
  align-items: center;
  justify-content: center;
  background: var(--el-color-primary);
  color: #fff;
  font-size: 14px;
  font-weight: 700;
}

.user-info {
  min-width: 0;
  display: flex;
  flex-direction: column;
}

.user-name {
  font-size: 13px;
  font-weight: 600;
  color: var(--el-text-color-primary);
  line-height: 1.3;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.user-role {
  font-size: 11px;
  color: var(--el-text-color-placeholder);
  line-height: 1.3;
}

// Collapse button
.sidebar-collapse-btn {
  flex-shrink: 0;
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 6px;
  height: 40px;
  margin: 4px 12px 10px;
  border-radius: var(--dd-radius-control);
  border: 1px solid var(--el-border-color-lighter);
  background: transparent;
  cursor: pointer;
  color: var(--el-text-color-secondary);
  font-size: 12px;
  transition:
    background-color var(--dd-motion-fast) var(--dd-ease-standard),
    color var(--dd-motion-fast) var(--dd-ease-standard),
    border-color var(--dd-motion-fast) var(--dd-ease-standard);

  &:hover {
    background: var(--el-fill-color-light);
    color: var(--el-color-primary);
    border-color: var(--el-color-primary-light-7);
  }
}

.collapse-text {
  font-size: 12px;
  white-space: nowrap;
}

// ==================== Header ====================
.layout-header {
  height: 60px;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  padding: 0 20px;
  background: var(--el-bg-color);
  border-bottom: 1px solid var(--el-border-color-light);
  position: sticky;
  top: 0;
  z-index: 20;
  flex-shrink: 0;
  // 顶栏左右两块「胶囊」（面包屑、右侧工具区）与左上角菜单按钮的统一高度（v3.3.1，issue #143 O2）。
  // 原来面包屑靠 padding + 行高撑出约 36px、右侧工具区被 34px 按钮与 38px 用户区撑到约 46px，
  // 两块高度差 10px、底色还差一档；统一定高后两端（桌面与移动端）都对齐。
  // 全局 * { box-sizing: border-box }，height 已包含 padding。
  --dd-header-chip-height: 36px;
}

.header-left {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
}

.header-toggle-btn {
  // 与旁边的面包屑胶囊等高（O2），圆角吃「按钮」角色令牌（桌面 = control 档，移动端 rounded 为 16px）
  width: var(--dd-header-chip-height);
  height: var(--dd-header-chip-height);
  border-radius: var(--dd-radius-button);
  border: 1px solid var(--el-border-color-lighter);
  background: transparent;
  display: flex;
  align-items: center;
  justify-content: center;
  cursor: pointer;
  color: var(--el-text-color-regular);
  // 只过渡 hover 真正会变的三个属性并吃令牌：原来的 all 0.2s 连尺寸变化也一起过渡，
  // 写死的时长还绕过了「减少动效」的降级（顶栏另外两处同理）。
  transition:
    background-color var(--dd-motion-fast) var(--dd-ease-standard),
    color var(--dd-motion-fast) var(--dd-ease-standard),
    border-color var(--dd-motion-fast) var(--dd-ease-standard);
  flex-shrink: 0;

  &:hover {
    background: var(--el-fill-color-light);
    color: var(--el-color-primary);
    border-color: var(--el-color-primary-light-7);
  }
}

.header-breadcrumb {
  display: flex;
  // 定高而不是靠 padding + 行高撑：与 .header-right 同一个高度令牌，两块胶囊才能严格等高（O2）
  height: var(--dd-header-chip-height);
  padding: 0 10px;
  border-radius: var(--dd-radius-button);
  background: color-mix(in srgb, var(--el-fill-color-light) 76%, transparent);
  align-items: center;
  gap: 6px;
  font-size: 13px;
  color: var(--el-text-color-secondary);
  min-width: 0;
}

.breadcrumb-sep {
  color: var(--el-text-color-placeholder);
  font-weight: 300;
  font-size: 15px;
}

.breadcrumb-section {
  white-space: nowrap;
  color: var(--el-text-color-secondary);
}

.breadcrumb-current {
  white-space: nowrap;
  letter-spacing: 0.1px;
  color: var(--el-text-color-primary);
  font-weight: 600;
}

.header-center {
  flex: 1;
  max-width: 480px;
  display: flex;
  justify-content: center;
}

.header-search {
  display: flex;
  align-items: center;
  gap: 8px;
  width: 100%;
  height: 36px;
  padding: 0 14px;
  border-radius: var(--dd-radius-control);
  background: var(--el-fill-color-light);
  border: 1px solid transparent;
  cursor: pointer;
  transition:
    background-color var(--dd-motion-fast) var(--dd-ease-standard),
    border-color var(--dd-motion-fast) var(--dd-ease-standard);

  &:hover {
    background: var(--el-fill-color);
    border-color: var(--el-border-color-lighter);
  }
}

.search-icon {
  color: var(--el-text-color-placeholder);
  flex-shrink: 0;
}

.search-placeholder {
  flex: 1;
  font-size: 13px;
  color: var(--el-text-color-placeholder);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.search-kbd {
  flex-shrink: 0;
  padding: 2px 6px;
  border-radius: var(--dd-radius-control);
  font-size: 11px;
  font-family: var(--dd-font-mono);
  color: var(--el-text-color-placeholder);
  background: var(--el-bg-color);
  border: 1px solid var(--el-border-color-lighter);
  line-height: 1.2;
}

.header-right {
  display: flex;
  // 与面包屑同高同底色（O2）：原来 82% 比面包屑的 76% 深一档，两块并排时像两种控件。
  // padding 2px + 内部 32px 的按钮 / 用户区，正好填满 36px。
  height: var(--dd-header-chip-height);
  padding: 2px;
  border-radius: var(--dd-radius-button);
  background: color-mix(in srgb, var(--el-fill-color-light) 76%, transparent);
  align-items: center;
  gap: 6px;
}

.header-icon-btn {
  // 32px：放进 36px 的胶囊里上下各留 2px；rounded 移动端配 16px 圆角正好是正圆
  width: 32px;
  height: 32px;
  border-radius: var(--dd-radius-button);
  border: none;
  background: transparent;
  display: flex;
  align-items: center;
  justify-content: center;
  cursor: pointer;
  color: var(--el-text-color-regular);
  transition:
    background-color var(--dd-motion-fast) var(--dd-ease-standard),
    color var(--dd-motion-fast) var(--dd-ease-standard);
  position: relative;

  &:hover {
    background: var(--el-fill-color-light);
    color: var(--el-color-primary);
  }
}

// 主题切换按钮的 hover 反馈与其他 header 图标按钮一致（换底色），不再额外做上浮 + 放大

// `.notification-dot` 已删除：它是零引用的死代码（全库只有这一处样式定义，
// 模板里从来没用过），而且背景写死 #f56c6c，暗色下不跟随令牌。
// 菜单角标现在由上面的 .menu-collapsed-dot 承担，取色走 --el-color-danger。

.header-user {
  display: flex;
  position: relative;
  overflow: hidden;
  align-items: center;
  gap: 8px;
  // 与 .header-icon-btn 同高 32px（O2）：原来 padding 5px 10px + 28px 头像约 38px，把右侧胶囊撑到 46px。
  // 左侧只留 2px，让头像贴着 hover 底的左缘；移动端不显示用户名时见下方媒体查询收成 32×32。
  height: 32px;
  padding: 2px 8px 2px 2px;
  border-radius: var(--dd-radius-button);
  cursor: pointer;
  transition: background-color var(--dd-motion-fast) var(--dd-ease-standard);
  outline: none;

  // hover 只换底色：去掉上浮阴影与顶部白色高光扫过（::after 装饰层已整体移除）
  &:hover {
    background: var(--el-fill-color-light);
  }
}

.header-user-avatar {
  width: 28px;
  height: 28px;
  // 圆形承载语义：同 .user-avatar，头像本身按圆形构图，直角硬切会裁掉边缘内容。
  // 两种圆角模式下都保持圆形，不吃 --dd-radius-* 令牌。
  border-radius: 50%;
  object-fit: cover;
  flex-shrink: 0;
}

.header-user-avatar--placeholder {
  display: flex;
  align-items: center;
  justify-content: center;
  background: var(--el-color-primary);
  color: #fff;
  font-size: 12px;
  font-weight: 700;
}

.header-user-name {
  font-size: 13px;
  font-weight: 500;
  color: var(--el-text-color-primary);
  white-space: nowrap;
}

// ==================== Main ====================
.layout-main-wrap {
  display: flex;
  flex-direction: column;
  flex: 1 1 auto;
  width: 0;
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
  padding: 20px;
  background: var(--el-bg-color-page);
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

@media screen and (max-height: 820px) and (min-width: 769px) {
  // 矮屏：外壳本身已铺满视口，这里只收紧 header / main 的内边距，
  // 给内容腾出更多垂直空间
  .layout-header {
    height: 52px;
    padding: 0 16px;
  }

  .layout-main {
    padding: 14px 16px;
  }
}

// ==================== Page transition ====================
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
.mobile-logo {
  border-bottom: 1px solid var(--el-border-color-light);
}

.mobile-nav {
  flex: 1;
}

// ==================== Mobile responsive ====================
@media screen and (max-width: 768px) {
  // 外壳在桌面端也已全屏铺满，这里不需要再复位；只调整内部间距与滚动行为，drawer 行为不变
  .layout-header {
    height: 54px;
    padding: 0 12px;
    gap: 8px;
  }

  .layout-main {
    overflow-y: auto;
    // 🔴 F1（v3.3.1 #143 → v3.3.3 #148）：这个滚动容器在移动端【禁止】带任何会形成层叠上下文的属性——
    //   z-index、transform、filter、opacity 动画、will-change、contain、isolation 一律不许加，
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

  .header-center {
    display: none;
  }

  .header-breadcrumb {
    font-size: 12px;
  }

  .header-right {
    gap: 2px;
  }

  // 移动端不显示用户名，只剩 28px 头像：四周各 2px，收成与主题按钮一样的 32×32
  .header-user {
    padding: 2px;
  }
}
</style>
