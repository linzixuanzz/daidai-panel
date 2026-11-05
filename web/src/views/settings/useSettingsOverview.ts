import { onBeforeUnmount, ref } from 'vue'
import { configApi, systemApi, type PanelUpdateStatus } from '@/api/system'
import { ElMessage, ElMessageBox } from 'element-plus'
import { useBadgesStore } from '@/stores/badges'
import { toast } from '@/utils/toast'

type UpdateVisualStatus = 'idle' | 'running' | 'restarting' | 'completed' | 'failed' | 'timeout'

export function useSettingsOverview() {
  // 「有新版本」这一项刻意不放进 /system/badges：检查更新每次都要打一发 GitHub
  // 外网请求且服务端没有缓存，挂进 30s 轮询等于替用户持续刷 GitHub。
  // 改由本页在真正调用过 checkUpdate 之后把结果回填给 store，侧栏角标随之亮起。
  const badgesStore = useBadgesStore()
  const systemInfo = ref<any>({})
  const systemStats = ref<any>(null)
  const currentVersion = ref('')
  const updateInfo = ref<any>(null)
  const updateStatus = ref<PanelUpdateStatus | null>(null)
  const checkingUpdate = ref(false)
  const updatingPanel = ref(false)
  const stoppingPanel = ref(false)
  const releaseNotesVisible = ref(false)
  const updateProgressVisible = ref(false)
  const updateProgressStatus = ref<UpdateVisualStatus>('idle')
  const updateProgressError = ref('')
  let updateStatusPollTimer: ReturnType<typeof setTimeout> | null = null
  let updateAvailabilityDelayTimer: ReturnType<typeof setTimeout> | null = null
  let updateAvailabilityTimer: ReturnType<typeof setTimeout> | null = null
  let restartDelayTimer: ReturnType<typeof setTimeout> | null = null
  let restartPollTimer: ReturnType<typeof setInterval> | null = null

  function formatBytes(bytes: number): string {
    if (!bytes) return '0 B'
    const k = 1024
    const sizes = ['B', 'KB', 'MB', 'GB', 'TB']
    const i = Math.floor(Math.log(bytes) / Math.log(k))
    return (bytes / Math.pow(k, i)).toFixed(1) + ' ' + sizes[i]
  }

  function getUsageClass(percent: number): string {
    if (!percent) return ''
    if (percent < 60) return 'usage-success'
    if (percent < 80) return 'usage-warning'
    return 'usage-danger'
  }

  async function loadSystemInfo() {
    try {
      const res = await systemApi.info()
      systemInfo.value = res.data || {}
    } catch {
      // ignore
    }
  }

  async function loadSystemStats() {
    try {
      const res = await systemApi.stats()
      systemStats.value = res.data || {}
    } catch {
      // ignore
    }
  }

  async function loadVersion() {
    try {
      const res = await systemApi.version()
      currentVersion.value = res.data.version || ''
    } catch {
      // ignore
    }
  }

  async function handleCheckUpdate() {
    checkingUpdate.value = true
    try {
      const res = await systemApi.checkUpdate()
      updateInfo.value = res.data
      // 回填侧栏角标。放在拿到结果之后、任何分支之前，
      // 「已是最新」也要回填（false），否则更新完角标会一直亮着。
      badgesStore.noteUpdateAvailable(Boolean(res.data?.has_update))
      // 手动检查也记一笔「上次检查更新时间」：后端静默更新巡检按它判断是否满 24 小时，
      // 「代理设置」页静默更新开关下方也只读展示它。概览页不再展示，但这次写入不能跟着删
      // 但 GitHub API 被限流、结果来自网页回退（rate_limited）时不记：那次只拿到了版本号、没拿到更新包校验值，
      // 记了会让静默更新以为「刚查过」而再等 24 小时（#177）
      if (!res.data?.rate_limited) {
        const now = new Date().toISOString()
        void configApi.set({ key: 'auto_update_last_checked_at', value: now }).catch(() => {})
      }
      if (res.data.has_update) {
        releaseNotesVisible.value = true
        if (res.data.auto_update_supported) {
          // 必须把 current 一起报出来：只显示 latest 时用户会把它读成「我已经是这个版本了」，
          // 于是把左上角显示的旧版本号当成「版本号显示 bug」来报障（issue #104 就是这么来的）。
          // 两个数字并排放，「当前 → 最新」一眼就能看出这是升级失败而不是显示错。
          ElMessage.success(`当前 v${res.data.current} → 最新 v${res.data.latest}`)
        } else {
          // 这条分支同理要带版本号：只讲「不支持一键更新」而不报版本，
          // 用户照样不知道自己现在是哪版、该升到哪版，还是会当成版本号显示错误来报障。
          const reason = res.data.update_disabled_reason || '当前部署暂不支持面板内一键更新'
          ElMessage.warning(`当前 v${res.data.current}，最新 v${res.data.latest}：${reason}`)
        }
      } else {
        ElMessage.success(`当前版本 v${res.data.current} 已经是最新版了`)
      }
    } catch (err: any) {
      const msg = err?.response?.data?.error || '检查更新失败，请稍后重试'
      ElMessage.error(msg)
    } finally {
      checkingUpdate.value = false
    }
  }

  function closeReleaseNotes() {
    releaseNotesVisible.value = false
  }

  function openReleaseNotes() {
    if (updateInfo.value?.has_update) {
      releaseNotesVisible.value = true
    }
  }

  async function handleUpdatePanel() {
    if (updatingPanel.value) {
      ElMessage.warning('更新任务已经在进行中，请稍候')
      return
    }

    if (updateInfo.value?.auto_update_supported === false) {
      ElMessage.warning(updateInfo.value?.update_disabled_reason || '当前部署暂不支持面板内一键更新')
      return
    }

    try {
      const updateTarget = updateInfo.value?.update_target || {}
      const isBinaryUpdate = updateTarget.deployment_type === 'binary'
      const isMagiskUpdate = updateTarget.deployment_type === 'magisk'
      const isWatchtowerManaged = updateTarget.update_manager === 'watchtower' || updateTarget.watchtower_managed === true
      const mirrorHost = updateTarget.mirror_host
      const pullImageName = updateTarget.pull_image_name
      const confirmMessage = isWatchtowerManaged
        ? buildWatchtowerUpdateConfirmMessage(updateTarget)
        : isMagiskUpdate
        ? buildMagiskUpdateConfirmMessage(updateTarget)
        : isBinaryUpdate
        ? buildBinaryUpdateConfirmMessage(updateTarget)
        : buildDockerUpdateConfirmMessage(mirrorHost, pullImageName)
      await ElMessageBox.confirm(
        confirmMessage,
        '立即更新',
        {
          confirmButtonText: '开始更新',
          cancelButtonText: '取消',
          type: 'warning'
        }
      )
    } catch (err: any) {
      if (err === 'cancel' || err?.toString?.() === 'cancel') {
        return
      }
      ElMessage.error(err?.message || '无法确认更新操作')
      return
    }

    updatingPanel.value = true
    openUpdateProgress({
      status: 'running',
      phase: 'preparing',
      message: '正在提交更新任务',
      started_at: new Date().toISOString(),
    })

    try {
      const res = await systemApi.updatePanel()
      applyUpdateSnapshot(res.data || updateStatus.value)
      if (updateInfo.value?.update_target?.update_manager === 'watchtower' || updateInfo.value?.update_target?.watchtower_managed === true) {
        updatingPanel.value = false
        ElMessage.success('已触发 Watchtower 检查更新，请稍后查看 Watchtower 日志或等待容器重建结果')
        return
      }
      startUpdateStatusPolling()
    } catch (err: any) {
      failUpdateProgress(err?.response?.data?.error || err?.message || '更新失败，请手动更新')
    }
  }

  function buildDockerUpdateConfirmMessage(mirrorHost?: string, pullImageName?: string) {
    const mirrorText = mirrorHost
      ? `当前将通过镜像源 ${mirrorHost} 拉取更新镜像。`
      : '当前将直接从默认镜像仓库拉取更新镜像。'
    const pullTargetText = pullImageName ? `\n拉取目标：${pullImageName}` : ''
    return `确认开始更新面板吗？系统会先拉取最新镜像，再自动重建容器。更新期间服务会短暂中断。\n${mirrorText}${pullTargetText}`
  }

  function buildWatchtowerUpdateConfirmMessage(updateTarget: any) {
    const scheduleText = updateTarget.watchtower_schedule
      ? `\n当前调度：${updateTarget.watchtower_schedule}`
      : ''
    return `确认手动触发 Watchtower 立即检查更新吗？\n这会请求 Watchtower 立刻执行一次更新检查，而不是等待下一次定时窗口。${scheduleText}`
  }

  function buildBinaryUpdateConfirmMessage(updateTarget: any) {
    const assetText = updateTarget.asset_name ? `\n更新包：${updateTarget.asset_name}` : ''
    const installDirText = updateTarget.install_dir ? `\n安装目录：${updateTarget.install_dir}` : ''
    return `确认开始更新面板吗？系统会在后台下载当前平台的二进制更新包，替换程序与前端文件，并自动重启面板。\n后台更新会保留 config.yaml、Dumb-Panel、data、logs、backups 等本地配置与数据目录。${assetText}${installDirText}`
  }

  function buildMagiskUpdateConfirmMessage(updateTarget: any) {
    const assetText = updateTarget.asset_name ? `\n更新包：${updateTarget.asset_name}` : ''
    return `确认开始在线升级吗？系统只会替换面板程序与前端文件，然后重启面板进程。\n容器 rootfs、已安装的 Python / Node 依赖、config.yaml 和 ports.conf 都不会被动到，也不需要重启手机。${assetText}`
  }

  function openUpdateProgress(snapshot?: PanelUpdateStatus | null) {
    updateProgressVisible.value = true
    updateProgressStatus.value = 'running'
    updateProgressError.value = ''
    updateStatus.value = snapshot || {
      status: 'running',
      phase: 'preparing',
      message: '正在准备更新任务...',
      started_at: new Date().toISOString(),
    }
  }

  function applyUpdateSnapshot(snapshot?: PanelUpdateStatus | null) {
    updateStatus.value = snapshot || {}
    updateProgressVisible.value = true

    if (updateStatus.value?.status === 'failed') {
      updateProgressStatus.value = 'failed'
      updateProgressError.value = updateStatus.value?.error || updateStatus.value?.message || '更新失败'
      updatingPanel.value = false
      return
    }

    if (updateStatus.value?.status === 'completed') {
      updateProgressStatus.value = 'completed'
      updateProgressError.value = ''
      updatingPanel.value = false
      return
    }

    if (updateStatus.value?.status === 'restarting') {
      updateProgressStatus.value = 'restarting'
      updateProgressError.value = ''
      return
    }

    updateProgressStatus.value = 'running'
    updateProgressError.value = ''
  }

  function failUpdateProgress(message: string) {
    stopUpdateStatusPolling()
    stopUpdateAvailabilityChecks()
    updateProgressVisible.value = true
    updateProgressStatus.value = 'failed'
    updateProgressError.value = message
    updateStatus.value = {
      ...(updateStatus.value || {}),
      status: 'failed',
      phase: updateStatus.value?.phase || 'failed',
      message,
      error: message,
    }
    updatingPanel.value = false
    ElMessage.error(message)
  }

  function startUpdateStatusPolling() {
    stopUpdateStatusPolling()
    const startedAt = Date.now()

    const poll = async () => {
      try {
        const res = await systemApi.updateStatus()
        applyUpdateSnapshot(res.data || {})

        if (updateStatus.value?.status === 'failed') {
          return
        }

        if (updateStatus.value?.status === 'completed') {
          stopUpdateStatusPolling()
          updatingPanel.value = false
          ElMessage.success(updateStatus.value.message || '更新请求已完成')
          return
        }

        if (updateStatus.value?.status === 'restarting') {
          stopUpdateStatusPolling()
          waitForAvailability()
          return
        }

        if (Date.now() - startedAt >= 12 * 60 * 1000) {
          failUpdateProgress('更新超时，请手动刷新页面检查')
          return
        }

        updateStatusPollTimer = setTimeout(() => {
          void poll()
        }, 2000)
      } catch (err: any) {
        if (shouldTreatAsRestart(err)) {
          stopUpdateStatusPolling()
          updateProgressStatus.value = 'restarting'
          waitForAvailability()
          return
        }
        failUpdateProgress(err?.response?.data?.error || err?.message || '更新状态获取失败')
      }
    }

    void poll()
  }

  async function handleRestartPanel() {
    try {
      await ElMessageBox.confirm('确定要重启面板吗？重启期间服务将短暂中断。', '重启面板', {
        confirmButtonText: '确认重启',
        cancelButtonText: '取消',
        type: 'warning'
      })
      await systemApi.restart()
      waitForRestart()
    } catch {
      // cancelled
    }
  }

  // 停止面板服务（仅 Magisk 模块版）。
  //
  // ⚠️ 这里【绝对不能】复用 waitForRestart：那是给「重启」用的 3 秒延迟 + 每 2 秒 × 60 次
  // 的轮询窗口，而停止之后面板永远不会自己回来，轮询只会白等 123 秒再弹一句「重启超时」，
  // 反而让用户以为出了故障。
  //
  // 二次确认必须把恢复路径写死在文案里 —— 点下去之后 Web 页面就没了，
  // 用户能看到的最后一段文字就是这个弹窗。
  async function handleStopPanel() {
    try {
      await ElMessageBox.confirm(
        '停止后面板进程会退出，<b>这个网页将永久失联</b>（刷新也打不开），重启手机同样不会自动启动。<br/><br/>'
          + '只有两种方式能再启动回来：<br/>'
          + '1. 在 Magisk / KernelSU / APatch 管理器里点模块卡片的「运行 / Action」按钮（再点一次就是启动）；<br/>'
          + '2. 用 adb shell 或 Termux 执行：<code>su -c "sh /data/adb/modules/daidai-panel/action.sh"</code><br/><br/>'
          + '确定要停止吗？',
        '停止面板服务',
        {
          confirmButtonText: '确认停止',
          cancelButtonText: '取消',
          type: 'warning',
          dangerouslyUseHTMLString: true
        }
      )
    } catch {
      return
    }

    stoppingPanel.value = true
    try {
      await systemApi.stopPanel()
      ElMessage.success('面板即将停止；恢复请到模块管理器点动作按钮')
    } catch (err: any) {
      ElMessage.error(err?.response?.data?.error || err?.message || '停止面板失败')
    } finally {
      stoppingPanel.value = false
    }
  }

  function stopRestartPolling() {
    if (restartDelayTimer) {
      clearTimeout(restartDelayTimer)
      restartDelayTimer = null
    }
    if (restartPollTimer) {
      clearInterval(restartPollTimer)
      restartPollTimer = null
    }
  }

  function waitForRestart() {
    // 在线演示 Demo 分叉：整段短路，一次探针都不能发。
    //
    // 下面的轮询是 `fetch('/', { method: 'HEAD' })` → res.ok → window.location.reload()。
    // 演示站是纯静态站，它的 `/` 在本地静态预览下恒 200；GitHub Pages 上当前恰好是 404
    // （实测 https://linzixuanzz.github.io/ 没有根仓库），但只要哪天建了同名根仓库
    // 就会变成 200 —— 属于会自己引爆的定时地雷。一旦 reload，演示数据（纯内存）全部清零。
    //
    // 正常路径其实走不到这里：POST /system/restart 在演示环境是 403，
    // handleRestartPanel 的 catch 会吞掉它。这条守卫防的是「以后有人改了那条路径」。
    // 用不着补提示：mock 层拒绝时已经弹过「演示环境不可用」了。
    if (import.meta.env.VITE_DEMO === '1') return

    stopRestartPolling()
    let attempts = 0
    restartDelayTimer = setTimeout(() => {
      restartDelayTimer = null
      restartPollTimer = setInterval(async () => {
        attempts++
        try {
          const res = await fetch('/', { method: 'HEAD' })
          if (res.ok) {
            stopRestartPolling()
            window.location.reload()
          }
        } catch {
          // ignore
        }
        if (attempts >= 60) {
          stopRestartPolling()
          // 原来是一条「请手动刷新页面」的纯文本提示：把动作说出来了，却要用户自己去按 F5。
          // 面板重启后大概率已经好了，只是探针没赶上——直接给一个按钮。
          toast.warning('重启超时，面板可能已经起来了', {
            action: { text: '立即刷新', handler: () => window.location.reload() },
          })
        }
      }, 2000)
    }, 3000)
  }

  function waitForAvailability() {
    // 在线演示 Demo 分叉：理由同上面的 waitForRestart —— 这里的探针同样是
    // `fetch('/', { method: 'HEAD', cache: 'no-store' })` → res.ok → location.reload()。
    // 演示环境的 POST /system/update 是 403，正常走不到这里；守卫防的是路径变化。
    if (import.meta.env.VITE_DEMO === '1') return

    stopUpdateAvailabilityChecks()
    updateProgressVisible.value = true
    updateProgressStatus.value = 'restarting'

    let attempts = 0
    updateAvailabilityDelayTimer = setTimeout(() => {
      updateAvailabilityDelayTimer = null
      const probe = async () => {
        attempts++
        try {
          const res = await fetch('/', { method: 'HEAD', cache: 'no-store' })
          if (res.ok) {
            stopUpdateAvailabilityChecks()
            window.location.reload()
            return
          }
        } catch {
          // ignore
        }

        if (attempts >= 80) {
          stopUpdateAvailabilityChecks()
          updateProgressStatus.value = 'timeout'
          updateProgressError.value = '等待新版本启动超时，请稍后手动刷新页面检查'
          updatingPanel.value = false
          toast.warning('等待新版本启动超时', {
            action: { text: '立即刷新', handler: () => window.location.reload() },
          })
          return
        }

        updateAvailabilityTimer = setTimeout(() => {
          void probe()
        }, 3000)
      }

      void probe()
    }, 2000)
  }

  function stopUpdateStatusPolling() {
    if (updateStatusPollTimer) {
      clearTimeout(updateStatusPollTimer)
      updateStatusPollTimer = null
    }
  }

  function stopUpdateAvailabilityChecks() {
    if (updateAvailabilityDelayTimer) {
      clearTimeout(updateAvailabilityDelayTimer)
      updateAvailabilityDelayTimer = null
    }
    if (updateAvailabilityTimer) {
      clearTimeout(updateAvailabilityTimer)
      updateAvailabilityTimer = null
    }
  }

  function closeUpdateProgress() {
    if (updateProgressStatus.value === 'running' || updateProgressStatus.value === 'restarting') {
      return
    }
    stopUpdateStatusPolling()
    stopUpdateAvailabilityChecks()
    updateProgressVisible.value = false
    updateProgressStatus.value = 'idle'
    updateProgressError.value = ''
  }

  function shouldTreatAsRestart(err: any) {
    if (!updateStatus.value?.status) {
      return false
    }

    if (err?.response) {
      return false
    }

    return updateStatus.value.status === 'running' || updateStatus.value.status === 'restarting'
  }

  function openGitHub() {
    const url = updateInfo.value?.has_update && updateInfo.value?.release_url
      ? updateInfo.value.release_url
      : 'https://github.com/linzixuanzz/daidai-panel/releases'
    window.open(url, '_blank')
  }

  onBeforeUnmount(() => {
    stopUpdateStatusPolling()
    stopUpdateAvailabilityChecks()
    stopRestartPolling()
  })

  return {
    systemInfo,
    systemStats,
    currentVersion,
    updateInfo,
    updateStatus,
    checkingUpdate,
    updatingPanel,
    stoppingPanel,
    releaseNotesVisible,
    updateProgressVisible,
    updateProgressStatus,
    updateProgressError,
    formatBytes,
    getUsageClass,
    loadSystemInfo,
    loadSystemStats,
    loadVersion,
    handleCheckUpdate,
    handleUpdatePanel,
    handleRestartPanel,
    handleStopPanel,
    openReleaseNotes,
    closeReleaseNotes,
    openGitHub,
    closeUpdateProgress
  }
}
