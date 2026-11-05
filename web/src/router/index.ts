import { createRouter, createWebHistory } from 'vue-router'
import { ElMessage } from 'element-plus'
import { useAuthStore } from '@/stores/auth'
import {
  isChunkLoadError,
  isChunkReloadPending,
  reloadOnce,
  wasLastReloadDeferred,
} from '@/utils/chunkReload'
import {
  isMonacoEditorRoute,
  scheduleIdleMonacoWarmup,
  shouldWarmMonaco,
  warmMonaco,
} from '@/utils/monacoWarmup'
import { getCachedPanelTitle, loadPanelSettings } from '@/utils/panelSettings'

const roleLevel: Record<string, number> = {
  viewer: 1,
  operator: 2,
  admin: 3,
}

function hasRequiredRole(role: string | undefined, minRole: string | undefined) {
  if (!minRole) return true
  if (!role) return false
  return (roleLevel[role] || 0) >= (roleLevel[minRole] || 0)
}

const legacyRouteMap: Record<string, string> = {
  '/notifications': '/admin/notifications',
  '/users': '/admin/users',
  '/open-api': '/admin/open-api',
  '/admin/deps': '/deps',
  '/api-docs': '/docs/api',
}

const routeComponents = {
  login: () => import('@/views/login/index.vue'),
  layout: () => import('@/layouts/MainLayout.vue'),
  dashboard: () => import('@/views/dashboard/index.vue'),
  tasks: () => import('@/views/tasks/index.vue'),
  processes: () => import('@/views/processes/index.vue'),
  scripts: () => import('@/views/scripts/index.vue'),
  envs: () => import('@/views/envs/index.vue'),
  configFile: () => import('@/views/config-file/index.vue'),
  subscriptions: () => import('@/views/subscriptions/index.vue'),
  logs: () => import('@/views/logs/index.vue'),
  deps: () => import('@/views/deps/index.vue'),
  notifications: () => import('@/views/notifications/index.vue'),
  users: () => import('@/views/users/index.vue'),
  profile: () => import('@/views/profile/index.vue'),
  apiDocs: () => import('@/views/api-docs/index.vue'),
  settings: () => import('@/views/settings/index.vue'),
  openApi: () => import('@/views/open-api/index.vue'),
}

const routePreloaders: Record<string, () => Promise<unknown>> = {
  '/dashboard': routeComponents.dashboard,
  '/tasks': routeComponents.tasks,
  '/processes': routeComponents.processes,
  '/scripts': routeComponents.scripts,
  '/envs': routeComponents.envs,
  '/config-file': routeComponents.configFile,
  '/subscriptions': routeComponents.subscriptions,
  '/logs': routeComponents.logs,
  '/deps': routeComponents.deps,
  '/notifications': routeComponents.notifications,
  '/users': routeComponents.users,
  '/profile': routeComponents.profile,
  '/docs/api': routeComponents.apiDocs,
  '/admin/settings': routeComponents.settings,
  '/admin/notifications': routeComponents.notifications,
  '/admin/users': routeComponents.users,
  '/admin/open-api': routeComponents.openApi,
}

const preloadedRoutes = new Set<string>()

function normalizePreloadPath(path: string) {
  const clean = path.split(/[?#]/)[0] || '/'
  return clean.length > 1 ? clean.replace(/\/$/, '') : clean
}

// 只拉路由 chunk，不做别的。空闲批量预加载（preloadPanelRoutes）直接走这里；
// 菜单悬停 / 聚焦 / 点击走下面的 preloadRouteByPath，那边多一步编辑器页的 Monaco 预热。
function preloadRouteChunk(normalizedPath: string): Promise<unknown> {
  const loader = routePreloaders[normalizedPath]
  if (!loader || preloadedRoutes.has(normalizedPath)) return Promise.resolve()

  preloadedRoutes.add(normalizedPath)
  return loader().catch((error) => {
    // 预加载失败不能影响用户正常切页；下次点击时允许重新走 Vue Router 的懒加载。
    preloadedRoutes.delete(normalizedPath)
    console.warn('页面预加载失败', normalizedPath, error)
  })
}

export function preloadRouteByPath(path: string) {
  const normalizedPath = normalizePreloadPath(path)
  const routeLoaded = preloadRouteChunk(normalizedPath)
  // 悬停预热 Monaco（#133）：停在「脚本管理 / 配置文件」上，用户多半马上要进编辑页。
  // 排在路由 chunk 之后：进页面先得有页面本身，Monaco 晚一拍到也比跟页面 chunk 抢带宽强；
  // 路由早就预加载过时 routeLoaded 是已 resolve 的 promise，下一个微任务就开始。
  // warmMonaco 自己记忆化，反复悬停不会重复下载；门槛（引擎、网络、内存、可见性）见 shouldWarmMonaco。
  // 演示站也保留这一条（演示站只关空闲预热）。
  if (isMonacoEditorRoute(normalizedPath) && shouldWarmMonaco()) {
    void routeLoaded.then(() => warmMonaco())
  }
  return routeLoaded
}

export function preloadPanelRoutes(paths: string[]) {
  if (typeof window === 'undefined') return

  const normalizedPaths = [...new Set(paths.map(normalizePreloadPath))]
  const queue = normalizedPaths.filter((path) => routePreloaders[path] && !preloadedRoutes.has(path))
  // Monaco 空闲预热（#133）排在这一批路由 chunk 全部落地之后，不跟切页真正要用的 chunk 抢带宽；
  // 只在这批菜单里有编辑器页时才排：viewer 进不去脚本管理 / 配置文件，替他预热纯属白下几 MB。
  // 用 normalizedPaths 而不是 queue 判断：编辑器页的 chunk 早被悬停预加载过时不在 queue 里，
  // 但 Monaco 未必热过。
  const warmMonacoWhenDrained = normalizedPaths.some(isMonacoEditorRoute)
  const pending: Promise<unknown>[] = []

  const scheduleNext = () => {
    if (queue.length === 0) {
      if (warmMonacoWhenDrained) {
        void Promise.allSettled(pending).then(() => scheduleIdleMonacoWarmup())
      }
      return
    }

    const idleWindow = window as Window & {
      requestIdleCallback?: (
        callback: (deadline: { timeRemaining: () => number; didTimeout?: boolean }) => void,
        options?: { timeout: number },
      ) => number
    }

    const run = (deadline?: { timeRemaining: () => number; didTimeout?: boolean }) => {
      // 每个空闲片段只预加载少量页面，避免后台下载/解析 chunk 反过来抢占切页主线程。
      let count = 0
      // requestIdleCallback 超时触发时 timeRemaining 可能为 0；此时至少推进一小批，避免队列一直空转。
      const shouldForceRun = !deadline || deadline.didTimeout
      while (queue.length > 0 && count < 2 && (shouldForceRun || deadline.timeRemaining() > 8)) {
        pending.push(preloadRouteChunk(queue.shift()!))
        count += 1
      }
      // 队列空了也要再进一次：收尾（排 Monaco 空闲预热）在 scheduleNext 的空队列分支里做
      scheduleNext()
    }

    if (idleWindow.requestIdleCallback) {
      idleWindow.requestIdleCallback(run, { timeout: 1800 })
      return
    }

    window.setTimeout(() => run(), 500)
  }

  scheduleNext()
}

const router = createRouter({
  // 必须把构建期的 base 传进来：面板被挂在反代子路径（如 https://example.com/panel/）
  // 或 GitHub Pages 项目站（/daidai-panel/）下时，不传 base 会让所有路由匹配失败，
  // 被下面的 catch-all 打回站点根，表现为「点任何菜单都跳出面板」。
  // import.meta.env.BASE_URL 由 vite build --base 决定，默认就是 '/'，对根路径部署无影响。
  history: createWebHistory(import.meta.env.BASE_URL),
  routes: [
    {
      path: '/login',
      name: 'Login',
      component: routeComponents.login,
      meta: { requiresAuth: false }
    },
    {
      path: '/',
      component: routeComponents.layout,
      meta: { requiresAuth: true, section: 'workspace' },
      children: [
        {
          path: '',
          redirect: '/dashboard'
        },
        {
          path: 'dashboard',
          name: 'Dashboard',
          component: routeComponents.dashboard,
          meta: { title: '仪表板', icon: 'Odometer', minRole: 'viewer' }
        },
        {
          path: 'tasks',
          name: 'Tasks',
          component: routeComponents.tasks,
          meta: { title: '定时任务', icon: 'Timer', minRole: 'viewer' }
        },
        {
          // 「后台进程」（#176）：查看并结束任务拉起、脱离任务的常驻进程。能看到所有任务的命令行、还能结束进程，只给管理员。
          path: 'processes',
          name: 'Processes',
          component: routeComponents.processes,
          meta: { title: '后台进程', icon: 'Cpu', minRole: 'admin' }
        },
        {
          path: 'scripts',
          name: 'Scripts',
          component: routeComponents.scripts,
          meta: { title: '脚本管理', icon: 'Document', minRole: 'operator' }
        },
        {
          path: 'envs',
          name: 'Envs',
          component: routeComponents.envs,
          meta: { title: '环境变量', icon: 'Setting', minRole: 'operator' }
        },
        {
          path: 'config-file',
          name: 'ConfigFile',
          component: routeComponents.configFile,
          meta: { title: '配置文件', icon: 'Document', minRole: 'admin' }
        },
        {
          path: 'subscriptions',
          name: 'Subscriptions',
          component: routeComponents.subscriptions,
          meta: { title: '订阅管理', icon: 'Download', minRole: 'operator' }
        },
        {
          path: 'logs',
          name: 'Logs',
          component: routeComponents.logs,
          meta: { title: '执行日志', icon: 'Tickets', minRole: 'viewer' }
        },
        {
          path: 'deps',
          name: 'Deps',
          component: routeComponents.deps,
          meta: { title: '依赖管理', icon: 'Box', minRole: 'admin' }
        },
        {
          path: 'notifications',
          name: 'Notifications',
          component: routeComponents.notifications,
          meta: { title: '通知渠道', icon: 'Bell', minRole: 'admin' }
        },
        {
          path: 'users',
          name: 'Users',
          component: routeComponents.users,
          meta: { title: '用户管理', icon: 'UserFilled', minRole: 'admin' }
        },
        {
          path: 'profile',
          name: 'Profile',
          component: routeComponents.profile,
          meta: { title: '个人设置', icon: 'User', minRole: 'viewer' }
        },
        {
          path: 'docs/api',
          name: 'ApiDocs',
          component: routeComponents.apiDocs,
          meta: { title: '接口文档', icon: 'Connection', minRole: 'viewer' }
        },
        {
          // 动效组件预览页（内部验收用）。
          //
          // 刻意【不】加进 MainLayout 的 workspaceItems / adminItems，所以侧栏看不到它，
          // 也刻意【不】加进上面的 routePreloaders —— 那份表驱动空闲预加载，
          // 登进面板就会把里面每个页面的 chunk 都拉下来。预览页只有输入 URL 才会加载。
          //
          // minRole 给 viewer：它不读任何业务数据，纯组件展示，没有需要保护的东西。
          path: 'dev/motion',
          name: 'DevMotion',
          component: () => import('@/views/dev-motion/index.vue'),
          meta: { title: '动效组件预览', minRole: 'viewer' }
        }
      ]
    },
    {
      path: '/admin',
      component: routeComponents.layout,
      meta: { requiresAuth: true, section: 'admin' },
      children: [
        {
          path: '',
          redirect: '/admin/settings'
        },
        {
          path: 'settings',
          name: 'AdminSettings',
          component: routeComponents.settings,
          meta: { title: '系统设置', icon: 'SetUp', minRole: 'admin' }
        },
        {
          path: 'notifications',
          name: 'AdminNotifications',
          component: routeComponents.notifications,
          meta: { title: '通知渠道', icon: 'Bell', minRole: 'admin' }
        },
        {
          path: 'users',
          name: 'AdminUsers',
          component: routeComponents.users,
          meta: { title: '用户管理', icon: 'UserFilled', minRole: 'admin' }
        },
        {
          path: 'open-api',
          name: 'AdminOpenAPI',
          component: routeComponents.openApi,
          meta: { title: 'Open API', icon: 'Key', minRole: 'admin' }
        }
      ]
    },
    {
      path: '/:pathMatch(.*)*',
      redirect: '/'
    }
  ]
})

router.beforeEach(async (to, _from, next) => {
  const authStore = useAuthStore()

  if (to.meta.requiresAuth === false) {
    if (authStore.isLoggedIn && to.name === 'Login') {
      next('/')
      return
    }
    next()
    return
  }

  if (!authStore.isLoggedIn) {
    next('/login')
    return
  }

  if (!authStore.user) {
    try {
      await authStore.fetchUser()
    } catch {
      authStore.clearAuth()
      next('/login')
      return
    }
  }

  if (to.path === '/settings') {
    next(hasRequiredRole(authStore.user?.role, 'admin') ? '/admin/settings' : '/profile')
    return
  }

  const legacyRedirect = legacyRouteMap[to.path]
  if (legacyRedirect) {
    if (!hasRequiredRole(authStore.user?.role, 'admin')) {
      next('/dashboard')
      return
    }
    next(legacyRedirect)
    return
  }

  const minRole = to.meta.minRole as string | undefined
  if (!hasRequiredRole(authStore.user?.role, minRole)) {
    next('/dashboard')
    return
  }

  next()
})

router.afterEach((to) => {
  const title = to.meta.title as string | undefined
  const panelTitle = getCachedPanelTitle()
  document.title = title ? `${panelTitle} - ${title}` : panelTitle
})

// chunk 加载失败（多半是面板升级后浏览器手里还是旧页面，旧文件名已被删除）→ 自动刷新一次，并直接落到
// 用户要去的那一页（#126，细节见 utils/chunkReload.ts）。
// href 用 router.resolve 算，带上 base：面板挂在反代子路径 / Pages 项目站下时，裸 to.fullPath 会跳到站点根。
// 条件里的 isChunkReloadPending()：切页的 chunk 失败时，main.ts 的 vite:preloadError 监听**先**跑，已经安排了
// 刷新并 preventDefault —— 那次 import 被改成 resolve undefined，这里收到的就不是网络错误，而是 vue-router 自己的
// `Couldn't resolve component "default" at "/xxx"`。本页已经在等着刷新，说明这次失败的导航就是那次 chunk 失败，
// 照样把落点补上。
router.onError((error, to) => {
  // 注册了 onError 之后 vue-router 就不再替我们打印未处理的导航错误（triggerError 只在没有监听者时才
  // console.error），这里照旧打出来，别让导航错误从此静默。
  console.error(error)
  if (!isChunkLoadError(error) && !isChunkReloadPending()) return
  if (reloadOnce('route', router.resolve(to.fullPath).href)) return
  // reloadOnce 因「页面上有未保存的内容」暂缓时，它已经弹过「面板已更新，保存后刷新页面即可」（同一批失败
  // 8 秒内只弹一条，切页时先跑的 vite:preloadError 那次多半已经弹过）。这里再叠一条「请检查网络」，
  // 两条说的原因互相矛盾，升级场景下还会误导。
  // 读的是 reloadOnce 自己记下的原因，不是「当前有没有未保存内容」：离线、60 秒限次、sessionStorage 不可用
  // 这三种情况都在未保存那一步之前就返回了，一条提示都没弹，于是自然落到下面的红色提示 —— 离线时
  // 「检查网络」恰好说对了，另外两种至少告诉用户发生了什么。上面那次 reloadOnce 与这里之间全是同步代码，
  // 读到的必定是它那一次的结果。
  if (wasLastReloadDeferred()) return
  // 60 秒内刚自动刷新过（或离线、或当前环境记不住刷新次数），不再自刷：至少告诉用户发生了什么，
  // 否则就是「点了菜单没反应」。
  ElMessage.error('页面文件加载失败，请检查网络后刷新页面重试')
})

void loadPanelSettings().then(() => {
  const currentRoute = router.currentRoute.value
  const title = currentRoute.meta.title as string | undefined
  const panelTitle = getCachedPanelTitle()
  document.title = title ? `${panelTitle} - ${title}` : panelTitle
})

export default router
