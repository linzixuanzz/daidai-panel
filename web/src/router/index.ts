import { createRouter, createWebHistory } from 'vue-router'
import { useAuthStore } from '@/stores/auth'
import { systemApi } from '@/api/system'

let cachedPanelTitle = '呆呆面板'
systemApi.panelSettings().then((res: any) => {
  if (res.data?.panel_title) cachedPanelTitle = res.data.panel_title
}).catch(() => {})

const router = createRouter({
  history: createWebHistory(),
  routes: [
    {
      path: '/login',
      name: 'Login',
      component: () => import('@/views/login/index.vue'),
      meta: { requiresAuth: false }
    },
    {
      path: '/',
      component: () => import('@/layouts/MainLayout.vue'),
      meta: { requiresAuth: true },
      children: [
        {
          path: '',
          redirect: '/dashboard'
        },
        {
          path: 'dashboard',
          name: 'Dashboard',
          component: () => import('@/views/dashboard/index.vue'),
          meta: { title: '仪表板', icon: 'Odometer' }
        },
        {
          path: 'tasks',
          name: 'Tasks',
          component: () => import('@/views/tasks/index.vue'),
          meta: { title: '定时任务', icon: 'Timer' }
        },
        {
          path: 'scripts',
          name: 'Scripts',
          component: () => import('@/views/scripts/index.vue'),
          meta: { title: '脚本管理', icon: 'Document' }
        },
        {
          path: 'envs',
          name: 'Envs',
          component: () => import('@/views/envs/index.vue'),
          meta: { title: '环境变量', icon: 'Setting' }
        },
        {
          path: 'subscriptions',
          name: 'Subscriptions',
          component: () => import('@/views/subscriptions/index.vue'),
          meta: { title: '订阅管理', icon: 'Download' }
        },
        {
          path: 'logs',
          name: 'Logs',
          component: () => import('@/views/logs/index.vue'),
          meta: { title: '执行日志', icon: 'Tickets' }
        },
        {
          path: 'notifications',
          name: 'Notifications',
          component: () => import('@/views/notifications/index.vue'),
          meta: { title: '通知渠道', icon: 'Bell' }
        },
        {
          path: 'users',
          name: 'Users',
          component: () => import('@/views/users/index.vue'),
          meta: { title: '用户管理', icon: 'UserFilled' }
        },
        {
          path: 'settings',
          name: 'Settings',
          component: () => import('@/views/settings/index.vue'),
          meta: { title: '系统设置', icon: 'Setting' }
        },
        {
          path: 'open-api',
          name: 'OpenAPI',
          component: () => import('@/views/open-api/index.vue'),
          meta: { title: 'Open API', icon: 'Key' }
        },
        {
          path: 'deps',
          name: 'Deps',
          component: () => import('@/views/deps/index.vue'),
          meta: { title: '依赖管理', icon: 'Box' }
        },
        {
          path: 'api-docs',
          name: 'ApiDocs',
          component: () => import('@/views/api-docs/index.vue'),
          meta: { title: '接口文档', icon: 'Connection' }
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

  next()
})

router.afterEach((to) => {
  const title = to.meta.title as string | undefined
  document.title = title ? `${cachedPanelTitle} - ${title}` : cachedPanelTitle
})

export default router
