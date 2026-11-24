import { defineStore } from 'pinia'
import { computed, ref } from 'vue'
import { systemApi, type AttentionItem } from '@/api/system'
import { useAuthStore } from '@/stores/auth'
import { useBadgesStore } from '@/stores/badges'
import { canAdminister } from '@/utils/roles'

/** 轮询间隔（design §5 F2）：「需要关注」不是秒级监控，60s 足够 */
const POLL_INTERVAL = 60_000

/** 页面重新可见后的补刷延迟，理由同 stores/badges.ts：让业务页自己的 onActivated 刷新先走 */
const RESUME_DELAY = 300

/**
 * 「已处理」压制的最长时间。
 * 点了重跑 / 重新拉取后，服务端要等任务跑完、订阅拉完才会改判；这段时间里条目仍会被下发。
 * 压制规则是「同 key 且 at 没变就不显示」（见 markHandled），这里再兜一个上限：
 * 30 分钟后 at 仍没变（任务跑得特别久、或服务端口径没跟上），就重新显示出来，宁可多提醒不漏提醒。
 */
const HANDLED_TTL = 30 * 60_000

type NewItemsListener = (items: AttentionItem[]) => void

/**
 * 「需要关注」的数据源（design §4.1 / §5 F2，契约见 wave1-handoff §8）。
 *
 * 用法：MainLayout 挂载时 start()、卸载时 stop()；仪表板卡片与顶栏铃铛共用 items。
 * 仪表板自己在进入时也会 refresh() 一次，保证没有外壳轮询时数据也不陈旧。
 *
 * 【update_available 为什么在前端合并】
 * 服务端不下发它：检查更新要打 GitHub 外网请求，不能挂进轮询。这里复用 stores/badges.ts 的
 * updateAvailable（由系统设置页真正调过 checkUpdate 后回填），只对管理员显示，与侧栏角标同一口径。
 *
 * 【为什么请求失败不清空】
 * 同 badges：网络抖一下就把「3 个失败任务」清空，和「真的没事了」看起来一模一样，是更糟的错误信息。
 */
export const useAttentionStore = defineStore('attention', () => {
  const authStore = useAuthStore()
  const badges = useBadgesStore()
  /** 服务端最近一次成功返回的原始条目（未经「已处理」过滤） */
  const serverItems = ref<AttentionItem[]>([])
  const loading = ref(false)
  /** 至少成功加载过一次 */
  const loaded = ref(false)

  /**
   * 已处理的条目：key → { 处理时服务端的 at, 处理时间 }。
   * 只在内存里：刷新页面后重新以服务端为准，不存本机（存了会把真实的新失败压住）。
   */
  const handled = ref<Record<string, { at: string; time: number }>>({})

  const items = computed<AttentionItem[]>(() => {
    const now = Date.now()
    const list = serverItems.value.filter((item) => {
      const mark = handled.value[item.key]
      if (!mark) return true
      // at 变了 = 处理之后又发生了一次（例如重跑后又失败）：重新显示
      if (mark.at !== item.at) return true
      return now - mark.time > HANDLED_TTL
    })

    // 前端合并「面板有新版本」，见文件头说明。badges 的 updateAvailable 只有管理员页会回填，这里再按角色兜一层
    if (badges.updateAvailable && canAdminister(authStore.user?.role) && !handled.value.update_available) {
      list.push({
        key: 'update_available',
        type: 'update_available',
        level: 'info',
        title: '面板有新版本可用',
        detail: '在线升级约 30 秒，数据不受影响',
        target: { kind: 'update', id: 0 },
        at: '',
      })
    }
    return list
  })

  const count = computed(() => items.value.length)

  const listeners = new Set<NewItemsListener>()
  /** 上一次服务端返回的 key 集合；null = 还没有基线（首次加载不算「新出现」） */
  let knownKeys: Set<string> | null = null
  let pending: Promise<void> | null = null
  let timer: ReturnType<typeof setInterval> | null = null
  let resumeTimer: ReturnType<typeof setTimeout> | null = null
  let started = false

  async function doRefresh() {
    if (!authStore.isLoggedIn) return
    loading.value = true
    try {
      const res = await systemApi.attention()
      const next = Array.isArray(res?.items) ? res.items : []
      serverItems.value = next
      loaded.value = true

      // 服务端已经不再下发的 key，把「已处理」标记一并清掉：以后再出现就是新的一轮，要重新提醒
      const nextKeys = new Set(next.map((item) => item.key))
      for (const key of Object.keys(handled.value)) {
        if (key !== 'update_available' && !nextKeys.has(key)) {
          delete handled.value[key]
        }
      }

      // 新出现的条目通知订阅方（G6 的桌面通知）。首次加载只建基线，不通知
      if (knownKeys) {
        const fresh = next.filter((item) => !knownKeys!.has(item.key))
        if (fresh.length) {
          for (const cb of listeners) {
            try {
              cb(fresh)
            } catch {
              // 订阅方自己的异常不能把轮询带崩
            }
          }
        }
      }
      knownKeys = nextKeys
    } catch {
      // 静默：401 由 request 拦截器接管；其余错误保留上一次的条目（理由见文件头）
    } finally {
      loading.value = false
    }
  }

  /** 拉一次最新条目。并发调用共用同一发请求，await 到的都是这一发的结果 */
  function refresh(): Promise<void> {
    if (!pending) {
      pending = doRefresh().finally(() => {
        pending = null
      })
    }
    return pending
  }

  function clearTimer() {
    if (timer) {
      clearInterval(timer)
      timer = null
    }
  }

  function startTimer() {
    clearTimer()
    timer = setInterval(() => {
      void refresh()
    }, POLL_INTERVAL)
  }

  function handleVisibilityChange() {
    if (document.visibilityState === 'hidden') {
      clearTimer()
      return
    }
    if (resumeTimer) clearTimeout(resumeTimer)
    resumeTimer = setTimeout(() => {
      void refresh()
      startTimer()
    }, RESUME_DELAY)
  }

  /** 由 MainLayout 挂载时调用，重复调用安全 */
  function start() {
    if (started) return
    started = true
    void refresh()
    startTimer()
    document.addEventListener('visibilitychange', handleVisibilityChange)
  }

  /** 退出登录 / 卸载布局时调用：停表并清空，换账号登入不继承上一个人的条目与基线 */
  function stop() {
    started = false
    clearTimer()
    if (resumeTimer) {
      clearTimeout(resumeTimer)
      resumeTimer = null
    }
    document.removeEventListener('visibilitychange', handleVisibilityChange)
    serverItems.value = []
    handled.value = {}
    loaded.value = false
    knownKeys = null
  }

  /**
   * 订阅「新出现」的条目（与上一次服务端结果相比新增的 key）。返回取消订阅函数。
   * 首次加载不算；前端合并的 update_available 不经过这里。
   */
  function onNewItems(cb: NewItemsListener): () => void {
    listeners.add(cb)
    return () => {
      listeners.delete(cb)
    }
  }

  /**
   * 标记一条为「已处理」（AttentionList 一键处理成功后调用）：
   * 在服务端改判之前先不显示；服务端的 at 变了（又失败了一次）会立即重新显示。
   */
  function markHandled(item: AttentionItem) {
    handled.value = { ...handled.value, [item.key]: { at: item.at, time: Date.now() } }
  }

  return {
    items,
    count,
    loading,
    loaded,
    refresh,
    start,
    stop,
    onNewItems,
    markHandled,
  }
})
