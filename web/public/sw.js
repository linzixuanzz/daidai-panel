/*
 * 呆呆面板的 Service Worker（F10）。
 *
 * 只做一件事：处理桌面通知的点击 —— 聚焦已经打开的面板页签并跳到对应日志，没有页签就新开一个。
 *
 * ⚠️ 这里刻意【没有】fetch 监听：不缓存、不拦截任何请求。
 *    面板有「检测到新版本就刷新」的自检（#126），任何离线缓存都会和它打架，让用户卡在旧版本上。
 *    以后也不要在这里加 fetch 监听。
 *
 * 注册方是 web/src/utils/desktopNotify.ts（只在生产且非演示站注册，路径带 BASE_URL）。
 */

// 新版本装好就立刻接管，不等旧页签全部关闭
self.addEventListener('install', () => {
  self.skipWaiting()
})

self.addEventListener('activate', (event) => {
  event.waitUntil(self.clients.claim())
})

self.addEventListener('notificationclick', (event) => {
  event.notification.close()
  const data = event.notification.data || {}
  const scope = self.registration.scope
  // url 是完整地址（新开页签用），path 是站内路径（让已打开的页签自己做路由跳转，不整页刷新）
  const url = typeof data.url === 'string' ? data.url : scope
  const path = typeof data.path === 'string' ? data.path : ''

  event.waitUntil((async () => {
    const windows = await self.clients.matchAll({ type: 'window', includeUncontrolled: true })
    // 只认本作用域下的页签；有正在看的优先用它
    const panels = windows.filter((client) => client.url.startsWith(scope))
    const target = panels.find((client) => client.focused) || panels.find((client) => client.visibilityState === 'visible') || panels[0]
    if (target) {
      await target.focus()
      if (path) target.postMessage({ type: 'dd-notify-open', path })
      return
    }
    await self.clients.openWindow(url)
  })())
})
