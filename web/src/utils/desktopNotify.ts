import router from '@/router'
import type { AttentionItem } from '@/api/system'
import { useAttentionStore } from '@/stores/attention'

/**
 * 桌面通知（F10，契约见 wave1-handoff §8）。
 *
 * - 开关存本机 localStorage（不跟账号走：授权本来就是「这个浏览器」给的）；
 * - 开启后订阅 `useAttentionStore().onNewItems`，只对【新出现】的 task_failing 弹通知；
 * - 同一浏览器开了多个面板页签时，用 BroadcastChannel 选出一个「主页签」，只有它弹，避免一次失败弹 N 遍；
 * - 主页签在后台时，attention store 自己的轮询是暂停的（页面不可见就停），由这里每 60s 替它刷一次，
 *   否则用户切走之后就永远收不到通知了——那正是桌面通知最有用的时候；
 * - 生产环境且不是演示站时注册 `sw.js`：它只处理通知点击（聚焦已有页签或打开新页签），没有 fetch 监听，
 *   不缓存、不拦截任何请求。PWA 可安装也靠它。
 */

const STORAGE_KEY = 'dd:desktop-notify'
const CHANNEL_NAME = 'dd-desktop-notify'
/** 主页签的心跳间隔 */
const HEARTBEAT_MS = 5_000
/**
 * 多久没听到主页签的心跳就接管。
 * 刻意放得很宽：Chrome 对后台超过 5 分钟的页签会把定时器压到「每分钟一次」，
 * 主页签挂在后台时心跳也就一分钟一跳；超时短于这个，其它页签会频繁抢主，同一条失败可能弹两遍。
 * 正常关页签时主页签会发 bye，其它页签立刻接管，不用等这么久。
 */
const LEADER_TIMEOUT_MS = 90_000
/** 主页签在后台时替 attention store 刷新的间隔（与它自己的 60s 轮询一致） */
const HIDDEN_POLL_MS = 60_000
/** 一批新条目最多弹几条，再多就是刷屏了 */
const MAX_PER_BATCH = 3

export function isDesktopNotifySupported(): boolean {
  return typeof window !== 'undefined' && 'Notification' in window
}

/** 开关打开且浏览器已授权才算开启（用户可能事后在浏览器设置里撤销授权） */
export function getDesktopNotifyEnabled(): boolean {
  if (!isDesktopNotifySupported()) return false
  try {
    if (window.localStorage.getItem(STORAGE_KEY) !== '1') return false
  } catch {
    // 隐私模式下读 localStorage 可能直接抛错，按「没开」处理
    return false
  }
  return Notification.permission === 'granted'
}

/** 打开时请求授权；返回实际生效的状态（用户拒绝授权时返回 false） */
export async function setDesktopNotifyEnabled(on: boolean): Promise<boolean> {
  let enabled = false
  if (on && isDesktopNotifySupported()) {
    let permission = Notification.permission
    if (permission === 'default') {
      try {
        permission = await Notification.requestPermission()
      } catch {
        permission = Notification.permission
      }
    }
    enabled = permission === 'granted'
  }
  try {
    window.localStorage.setItem(STORAGE_KEY, enabled ? '1' : '0')
  } catch {
    // 写不进去只是下次打开要重新开一次，不打断用户
  }
  return enabled
}

/** 由 MainLayout 在挂载时调用一次，返回清理函数（卸载时调用） */
export function installDesktopNotify(): () => void {
  if (typeof window === 'undefined') return () => {}
  const base = import.meta.env.BASE_URL
  const cleanups: Array<() => void> = []

  // ---- 1. Service Worker：只在生产且非演示站注册 ----
  // 守卫写成编译期常量：发布版里 VITE_DEMO 是 ''，演示站构建里是 '1'，开发模式 PROD 为 false。
  // 演示站是 GitHub Pages 上的静态站，没必要也不该在访客浏览器里留一个 SW。
  if (import.meta.env.PROD && import.meta.env.VITE_DEMO !== '1' && 'serviceWorker' in navigator) {
    // 路径带 BASE_URL，子路径部署时 SW 的作用域也跟着落在子路径下
    navigator.serviceWorker.register(`${base}sw.js`, { scope: base }).catch(() => {
      // 注册失败（比如非 HTTPS 的局域网 IP 访问）只是不能安装 PWA，通知会退回页面内的 Notification
    })
    // SW 收到通知点击、找到已打开的页签后，会发消息让页签自己做站内跳转（不整页刷新）
    const onSwMessage = (event: MessageEvent) => {
      const data = event.data as { type?: string; path?: string } | null
      if (data?.type === 'dd-notify-open' && typeof data.path === 'string') {
        void router.push(data.path)
      }
    }
    navigator.serviceWorker.addEventListener('message', onSwMessage)
    navigator.serviceWorker.startMessages()
    cleanups.push(() => navigator.serviceWorker.removeEventListener('message', onSwMessage))
  }

  // ---- 2. 多页签选主 ----
  // 每个页签一个随机 id，前缀是时间戳：两个页签同时自认是主时，id 小的（先打开的）赢。
  const tabId = `${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 10)}`
  const channel = typeof BroadcastChannel !== 'undefined' ? new BroadcastChannel(CHANNEL_NAME) : null
  // 不支持 BroadcastChannel 的浏览器：每个页签都当自己是主（最多重复弹，不会漏弹）
  let leaderId = channel ? '' : tabId
  let leaderSeenAt = 0
  let lastHiddenPollAt = 0

  const isLeader = () => leaderId === tabId

  const announce = () => {
    channel?.postMessage({ type: 'hb', id: tabId })
  }

  // 定时检查：自己是主就发心跳；主页签失联就接管；顺带在后台替 attention store 刷新
  const tick = () => {
    if (channel) {
      if (isLeader()) {
        announce()
      } else if (!leaderId || Date.now() - leaderSeenAt > LEADER_TIMEOUT_MS) {
        leaderId = tabId
        announce()
      }
    }
    if (isLeader() && getDesktopNotifyEnabled() && document.visibilityState === 'hidden') {
      const now = Date.now()
      if (now - lastHiddenPollAt >= HIDDEN_POLL_MS) {
        lastHiddenPollAt = now
        void useAttentionStore().refresh()
      }
    }
  }

  if (channel) {
    channel.onmessage = (event: MessageEvent) => {
      const msg = event.data as { type?: string; id?: string } | null
      if (!msg?.type) return
      if (msg.type === 'who') {
        // 新页签在问谁是主：是自己就马上应一声，免得它等超时后误接管
        if (isLeader()) announce()
        return
      }
      if (msg.type === 'hb' && typeof msg.id === 'string') {
        if (isLeader() && msg.id > tabId) {
          // 对方 id 比自己大：自己留任，再喊一声让对方退下
          announce()
          return
        }
        leaderId = msg.id
        leaderSeenAt = Date.now()
        return
      }
      if (msg.type === 'bye' && msg.id === leaderId) {
        // 主页签关了：立刻重新选（大家同时抢，按 id 比出一个）
        leaderId = ''
        tick()
      }
    }
    channel.postMessage({ type: 'who' })
  }

  // 刚打开时先等 1 秒听听有没有主页签在，再决定要不要自己当
  const firstTick = window.setTimeout(tick, 1000)
  const heartbeat = window.setInterval(tick, HEARTBEAT_MS)

  const sayBye = () => {
    if (isLeader()) channel?.postMessage({ type: 'bye', id: tabId })
  }
  window.addEventListener('pagehide', sayBye)

  cleanups.push(() => {
    window.clearTimeout(firstTick)
    window.clearInterval(heartbeat)
    window.removeEventListener('pagehide', sayBye)
    sayBye()
    channel?.close()
  })

  // ---- 3. 新出现的连续失败 → 弹通知 ----
  const off = useAttentionStore().onNewItems((items: AttentionItem[]) => {
    if (!isLeader() || !getDesktopNotifyEnabled()) return
    const failing = items.filter((item) => item.type === 'task_failing').slice(0, MAX_PER_BATCH)
    for (const item of failing) {
      void showNotification(item, base)
    }
  })
  cleanups.push(off)

  return () => {
    for (const cleanup of cleanups) cleanup()
  }
}

async function showNotification(item: AttentionItem, base: string) {
  // 点击后跳到这个任务的日志（执行日志页会按 task_id 筛选并自动打开最近一条）
  const path = item.target?.kind === 'task' && item.target.id > 0 ? `/logs?task_id=${item.target.id}` : '/dashboard'
  // SW 打开新窗口要完整地址；router.push 用站内路径（router 的 history 已经带了 BASE_URL）
  const url = new URL(`${base.replace(/\/$/, '')}${path}`, window.location.origin).href
  const options: NotificationOptions = {
    body: item.detail || '点击查看最近一次日志',
    // 同一个任务的通知互相替换，不会堆一串
    tag: item.key,
    icon: `${base}pwa-192.png`,
    data: { url, path },
  }

  try {
    // 有 SW 时走 SW 弹：安卓 Chrome 不支持页面里 new Notification，而且点击由 sw.js 统一处理
    const registration = 'serviceWorker' in navigator ? await navigator.serviceWorker.getRegistration(base) : undefined
    if (registration) {
      await registration.showNotification(item.title, options)
      return
    }
    const notification = new Notification(item.title, options)
    notification.onclick = () => {
      window.focus()
      void router.push(path)
      notification.close()
    }
  } catch {
    // 授权被撤销、浏览器不允许等：通知是锦上添花，失败就算了
  }
}
