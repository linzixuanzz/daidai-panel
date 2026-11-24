import { defineStore } from 'pinia'
import { computed, ref, watch } from 'vue'
import { taskApi, type TaskRunning } from '@/api/task'
import { useAuthStore } from '@/stores/auth'
import { useUiStore } from '@/stores/ui'

/** 浮窗展开时的轮询间隔：用户正盯着看，任务开始 / 结束要尽快反映出来 */
const POLL_OPEN = 5_000
/** 浮窗收起时的轮询间隔：只给顶栏胶囊和右下角小胶囊提供数字，30s 足够 */
const POLL_CLOSED = 30_000
/** 回到前台后的补刷延迟，理由同 stores/badges.ts：错开业务页自己的刷新 */
const RESUME_DELAY = 300

/**
 * 运行中的任务（F6，契约见 wave1-handoff §8）。
 *
 * 数据源是 `GET /tasks/running`。轮询节奏跟着浮窗走：展开 5s、收起 30s；页面不可见时整个停掉，
 * 回到前台先补刷一次再接着轮询。
 *
 * 和 badges 一样，请求失败时保留上一次的列表：网络抖一下就把「2 个任务运行中」抹掉，
 * 比不显示更误导。
 */
export const useRunningStore = defineStore('running', () => {
  const items = ref<TaskRunning['items']>([])
  const count = computed(() => items.value.length)

  let timer: ReturnType<typeof setTimeout> | null = null
  let resumeTimer: ReturnType<typeof setTimeout> | null = null
  let inFlight = false
  let started = false

  async function refresh() {
    // 防重入：慢网络下上一发还没回来时不叠加
    if (inFlight) return
    if (!useAuthStore().isLoggedIn) return
    inFlight = true
    try {
      const res = await taskApi.running()
      items.value = Array.isArray(res?.items) ? res.items : []
    } catch {
      // 静默：401 由 request 拦截器处理，其余错误保留上一次的列表
    } finally {
      inFlight = false
    }
  }

  function clearTimer() {
    if (timer) {
      clearTimeout(timer)
      timer = null
    }
  }

  // 用 setTimeout 链而不是 setInterval：间隔会随浮窗开合变化，每一轮按当时的状态重新算
  function schedule() {
    clearTimer()
    if (!started) return
    if (typeof document !== 'undefined' && document.visibilityState === 'hidden') return
    const interval = useUiStore().dockOpen ? POLL_OPEN : POLL_CLOSED
    timer = setTimeout(async () => {
      timer = null
      await refresh()
      schedule()
    }, interval)
  }

  function handleVisibilityChange() {
    if (document.visibilityState === 'hidden') {
      clearTimer()
      return
    }
    if (resumeTimer) clearTimeout(resumeTimer)
    resumeTimer = setTimeout(() => {
      resumeTimer = null
      void refresh()
      schedule()
    }, RESUME_DELAY)
  }

  // 浮窗打开时立刻刷一次（打开的那一刻看到的就是最新的），并把轮询切到 5s；收起时切回 30s
  watch(
    () => useUiStore().dockOpen,
    (open) => {
      if (!started) return
      if (open) void refresh()
      schedule()
    }
  )

  /** 由 MainLayout 在挂载时调用，重复调用是安全的 */
  function start() {
    if (started) return
    started = true
    void refresh()
    schedule()
    if (typeof document !== 'undefined') {
      document.addEventListener('visibilitychange', handleVisibilityChange)
    }
  }

  /** 退出登录 / 卸载布局时调用 */
  function stop() {
    started = false
    clearTimer()
    if (resumeTimer) {
      clearTimeout(resumeTimer)
      resumeTimer = null
    }
    if (typeof document !== 'undefined') {
      document.removeEventListener('visibilitychange', handleVisibilityChange)
    }
    items.value = []
  }

  return { items, count, refresh, start, stop }
})
