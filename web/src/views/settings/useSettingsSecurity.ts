import { onBeforeUnmount, ref } from 'vue'
import { useRouter } from 'vue-router'
import { systemApi, type BackupSelection, type RestoreProgressState } from '@/api/system'
import { securityApi } from '@/api/security'
import { authApi } from '@/api/auth'
import { useAuthStore } from '@/stores/auth'
import { ElMessage, ElMessageBox } from 'element-plus'
import { createQrCodeDataUrl } from '@/utils/qrcode'
import { toast } from '@/utils/toast'

const backupUploadMaxSize = 512 * 1024 * 1024

type RestoreVisualStatus =
  | 'idle'
  | 'running'
  | 'completed'
  | 'queued-restart'
  | 'restarting'
  | 'failed'
  | 'restart-timeout'

export function useSettingsSecurity() {
  const router = useRouter()
  const authStore = useAuthStore()

  const securityTab = ref('password-2fa')

  const backups = ref<any[]>([])
  const backupsLoading = ref(false)
  const showBackupDialog = ref(false)
  const backupName = ref('')
  const backupPassword = ref('')
  const backupSelection = ref<BackupSelection>({
    configs: true,
    tasks: true,
    subscriptions: true,
    env_vars: true,
    logs: true,
    scripts: true,
    dependencies: true,
    task_views: true,
  })
  const backupScheduleSelection = ref<BackupSelection>({
    configs: true,
    tasks: true,
    subscriptions: true,
    env_vars: true,
    logs: true,
    scripts: true,
    dependencies: true,
    task_views: true,
  })
  const uploadProgress = ref(-1)
  const uploadUploading = ref(false)
  // 创建备份的在途锁。备份是长耗时操作，连点最容易打出多份重复备份，
  // 刻意与导入用的 uploadUploading 分开，避免一个动作把另一个按钮也锁住。
  const backupCreating = ref(false)

  const showRestoreDialog = ref(false)
  const restoreFilename = ref('')
  const restorePassword = ref('')
  let restoreTimer: ReturnType<typeof setInterval> | null = null
  const restoreProgressVisible = ref(false)
  const restoreProgressStatus = ref<RestoreVisualStatus>('idle')
  const restoreProgressStage = ref('preparing')
  const restoreProgressMessage = ref('')
  const restoreProgressPercent = ref(0)
  const restoreProgressSource = ref('')
  const restoreProgressSelection = ref<Partial<BackupSelection>>({})
  const restoreProgressStartedAt = ref('')
  const restoreProgressError = ref('')
  const restoreRestartCountdown = ref(0)
  let restoreProgressPollTimer: ReturnType<typeof setInterval> | null = null
  let restartProbeTimer: ReturnType<typeof setInterval> | null = null
  let restartProbeDelayTimer: ReturnType<typeof setTimeout> | null = null

  const oldPassword = ref('')
  const newPassword = ref('')
  const confirmPassword = ref('')

  const twoFAEnabled = ref(false)
  const twoFASecret = ref('')
  const twoFAUri = ref('')
  const twoFAQrUrl = ref('')
  const twoFACode = ref('')
  const showSetup2FA = ref(false)

  const loginLogs = ref<any[]>([])
  const loginLogsLoading = ref(false)
  const loginLogsTotal = ref(0)
  const loginLogsPage = ref(1)

  const sessions = ref<any[]>([])
  const sessionsLoading = ref(false)

  const ipWhitelist = ref<any[]>([])
  const ipWhitelistLoading = ref(false)
  const showAddIPDialog = ref(false)
  const newIP = ref('')
  const newIPRemarks = ref('')
  // 添加 IP 白名单的在途锁，独立一把，不与备份/导入共用
  const ipAdding = ref(false)

  async function loadBackups() {
    backupsLoading.value = true
    try {
      const res = await systemApi.backupList()
      backups.value = res.data || []
    } catch {
      ElMessage.error('加载备份列表失败')
    } finally {
      backupsLoading.value = false
    }
  }

  async function handleCreateBackup() {
    showBackupDialog.value = true
    backupName.value = ''
    backupPassword.value = ''
    backupSelection.value = {
      configs: true,
      tasks: true,
      subscriptions: true,
      env_vars: true,
      logs: true,
      scripts: true,
      dependencies: true,
      task_views: true,
    }
  }

  async function handleUploadBackup(e: Event) {
    const input = e.target as HTMLInputElement
    const file = input.files?.[0]
    if (!file) return
    if (file.size > backupUploadMaxSize) {
      ElMessage.error('备份文件过大，当前上传上限为 512MB')
      input.value = ''
      return
    }
    uploadUploading.value = true
    uploadProgress.value = 0
    try {
      await systemApi.uploadBackup(file, (percent) => {
        uploadProgress.value = percent
      })
      ElMessage.success('备份文件导入成功')
      void loadBackups()
    } catch (err: any) {
      if (err?.response?.status === 413) {
        ElMessage.error('备份文件超过当前上传上限，请检查反向代理或容器上传限制')
      } else {
        ElMessage.error(err?.response?.data?.error || err?.message || '导入备份失败')
      }
    }
    uploadUploading.value = false
    uploadProgress.value = -1
    input.value = ''
  }

  async function confirmCreateBackup() {
    try {
      const hasSelection = Object.values(backupSelection.value).some(Boolean)
      if (!hasSelection) {
        ElMessage.warning('请至少选择一个备份项')
        return
      }
      // 校验通过后才置位，复位统一放 finally（上面的早退分支也会走到 finally，不会把按钮锁死）
      backupCreating.value = true
      await systemApi.backup(backupPassword.value, backupSelection.value, backupName.value)
      ElMessage.success('备份创建成功')
      showBackupDialog.value = false
      backupName.value = ''
      backupPassword.value = ''
      void loadBackups()
    } catch {
      ElMessage.error('备份失败')
    } finally {
      backupCreating.value = false
    }
  }

  async function readBlobErrorMessage(err: any, fallback: string) {
    const data = err?.response?.data
    if (data instanceof Blob) {
      const text = await data.text().catch(() => '')
      if (text) {
        try {
          const parsed = JSON.parse(text)
          return parsed?.error || parsed?.message || text
        } catch {
          return text
        }
      }
    }
    return err?.response?.data?.error || err?.message || fallback
  }

  async function handleDownloadBackup(filename: string) {
    try {
      const blob = await systemApi.downloadBackup(filename)
      const url = URL.createObjectURL(blob)
      const link = document.createElement('a')
      link.href = url
      link.download = filename
      document.body.appendChild(link)
      link.click()
      document.body.removeChild(link)
      URL.revokeObjectURL(url)
    } catch (err: any) {
      ElMessage.error(await readBlobErrorMessage(err, '下载备份失败'))
    }
  }

  async function handleRestoreBackup(filename: string) {
    restoreFilename.value = filename
    restorePassword.value = ''
    showRestoreDialog.value = true
  }

  function stopRestoreCountdown() {
    if (restoreTimer) {
      clearInterval(restoreTimer)
      restoreTimer = null
    }
  }

  function stopRestoreProgressPolling() {
    if (restoreProgressPollTimer) {
      clearInterval(restoreProgressPollTimer)
      restoreProgressPollTimer = null
    }
  }

  function stopRestartProbe() {
    if (restartProbeDelayTimer) {
      clearTimeout(restartProbeDelayTimer)
      restartProbeDelayTimer = null
    }
    if (restartProbeTimer) {
      clearInterval(restartProbeTimer)
      restartProbeTimer = null
    }
  }

  function resetRestoreProgressState() {
    stopRestoreCountdown()
    stopRestoreProgressPolling()
    stopRestartProbe()
    restoreProgressVisible.value = false
    restoreProgressStatus.value = 'idle'
    restoreProgressStage.value = 'preparing'
    restoreProgressMessage.value = ''
    restoreProgressPercent.value = 0
    restoreProgressSource.value = ''
    restoreProgressSelection.value = {}
    restoreProgressStartedAt.value = ''
    restoreProgressError.value = ''
    restoreRestartCountdown.value = 0
  }

  function applyRestoreProgress(snapshot: RestoreProgressState) {
    restoreProgressVisible.value = true
    restoreProgressStage.value = snapshot.stage || restoreProgressStage.value || 'preparing'
    restoreProgressMessage.value = snapshot.message || restoreProgressMessage.value || '正在恢复备份内容...'
    restoreProgressPercent.value = Math.max(0, Math.min(100, Number(snapshot.percent || 0)))
    restoreProgressSource.value = snapshot.source || restoreProgressSource.value || ''
    restoreProgressSelection.value = snapshot.selection || restoreProgressSelection.value || {}
    restoreProgressError.value = snapshot.error || ''
    if (snapshot.started_at) {
      restoreProgressStartedAt.value = snapshot.started_at
    }

    if (snapshot.status === 'failed') {
      restoreProgressStatus.value = 'failed'
      return
    }
    if (snapshot.status === 'completed') {
      restoreProgressStatus.value = 'completed'
      return
    }
    if (snapshot.active || snapshot.status === 'running') {
      restoreProgressStatus.value = 'running'
    }
  }

  async function fetchRestoreProgress(force = false) {
    try {
      const res = await systemApi.restoreProgress()
      const snapshot = (res.data || {}) as RestoreProgressState
      if (!force && !snapshot.active && snapshot.status === 'idle') {
        return
      }
      if (!force && !snapshot.active && snapshot.updated_at && restoreProgressStartedAt.value) {
        const snapshotUpdatedAt = Date.parse(snapshot.updated_at)
        const currentRestoreStartedAt = Date.parse(restoreProgressStartedAt.value)
        if (!Number.isNaN(snapshotUpdatedAt) && !Number.isNaN(currentRestoreStartedAt) && snapshotUpdatedAt < currentRestoreStartedAt) {
          return
        }
      }
      applyRestoreProgress(snapshot)
    } catch {
      // ignore progress probe errors while restore request is still running
    }
  }

  function startRestoreProgressPolling() {
    stopRestoreProgressPolling()
    void fetchRestoreProgress()
    restoreProgressPollTimer = setInterval(() => {
      void fetchRestoreProgress()
    }, 700)
  }

  function openRestoreProgress() {
    stopRestoreCountdown()
    stopRestartProbe()
    restoreProgressVisible.value = true
    restoreProgressStatus.value = 'running'
    restoreProgressStage.value = 'preparing'
    restoreProgressMessage.value = '正在提交恢复请求并准备恢复环境...'
    restoreProgressPercent.value = 3
    restoreProgressSource.value = ''
    restoreProgressSelection.value = {}
    restoreProgressStartedAt.value = new Date().toISOString()
    restoreProgressError.value = ''
    restoreRestartCountdown.value = 0
  }

  function startRestoreCountdown() {
    stopRestoreCountdown()
    restoreProgressStatus.value = 'queued-restart'
    restoreProgressStage.value = 'completed'
    restoreProgressPercent.value = 100
    restoreProgressMessage.value = restoreProgressMessage.value || '数据恢复完成，正在准备重启面板...'
    restoreRestartCountdown.value = 10

    restoreTimer = setInterval(() => {
      restoreRestartCountdown.value -= 1
      if (restoreRestartCountdown.value <= 0) {
        stopRestoreCountdown()
        void doRestart()
      }
    }, 1000)
  }

  async function confirmRestore() {
    showRestoreDialog.value = false
    openRestoreProgress()
    startRestoreProgressPolling()

    try {
      await systemApi.restore(restoreFilename.value, restorePassword.value)
      stopRestoreProgressPolling()
      await fetchRestoreProgress(true)
      startRestoreCountdown()
    } catch (e: any) {
      stopRestoreProgressPolling()
      await fetchRestoreProgress(true)
      restoreProgressVisible.value = true
      restoreProgressStatus.value = 'failed'
      restoreProgressPercent.value = restoreProgressPercent.value || 0
      restoreProgressStage.value = restoreProgressStage.value || 'preparing'
      restoreProgressMessage.value = restoreProgressMessage.value || '恢复过程中出现异常'
      restoreProgressError.value = e?.response?.data?.error || e?.message || '恢复失败'
      ElMessage.error(restoreProgressError.value)
    }
  }

  async function restartRestoreNow() {
    stopRestoreCountdown()
    await doRestart()
  }

  async function doRestart() {
    restoreProgressVisible.value = true
    restoreProgressStatus.value = 'restarting'
    restoreProgressStage.value = 'completed'
    restoreProgressPercent.value = 100
    restoreProgressMessage.value = '恢复数据已写入，正在等待面板重新启动...'
    restoreProgressError.value = ''
    try {
      await systemApi.restart()
    } catch {
      // ignore
    }
    waitForRestart()
  }

  function waitForRestart() {
    // 在线演示 Demo 分叉：整段短路，一次探针都不能发。
    //
    // 下面的轮询是 `fetch('/', { method: 'HEAD', cache: 'no-store' })` → res.ok
    // → window.location.reload()，而演示站是纯静态站：本地静态预览下 `/` 恒 200，
    // GitHub Pages 上目前是 404 但只要建了同名根仓库就会变成 200。
    // 一旦 reload，纯内存的演示数据全部清零。
    //
    // 这一处是三处 HEAD 探针里【唯一真的够得着】的：doRestart() 里的
    // `await systemApi.restart()` 被包在 try/catch 里，演示环境返回 403 也照样往下走，
    // 直接就调到了这里。另外两处（useSettingsOverview）会被各自的 catch 挡在门外。
    if (import.meta.env.VITE_DEMO === '1') return

    stopRestartProbe()
    let attempts = 0
    restartProbeDelayTimer = setTimeout(() => {
      restartProbeDelayTimer = null
      restartProbeTimer = setInterval(async () => {
        attempts += 1
        try {
          const res = await fetch('/', { method: 'HEAD', cache: 'no-store' })
          if (res.ok) {
            stopRestartProbe()
            window.location.reload()
          }
        } catch {
          // ignore
        }

        if (attempts >= 60) {
          stopRestartProbe()
          restoreProgressStatus.value = 'restart-timeout'
          restoreProgressMessage.value = '恢复已经完成，但暂时还没有检测到面板重新上线。'
          restoreProgressError.value = '重启等待超时，请稍后手动刷新页面，或检查容器/反向代理是否仍在重建。'
          // 与 useSettingsOverview 的两处超时保持一致：把「请手动刷新」变成一个能点的按钮。
          // 恢复完成后面板往往已经起来了，只是探针没赶上，用户不该还要自己去按 F5。
          toast.warning('重启超时，面板可能已经起来了', {
            action: { text: '立即刷新', handler: () => window.location.reload() },
          })
        }
      }, 2000)
    }, 3000)
  }

  function closeRestoreProgress() {
    if (restoreProgressStatus.value === 'running' || restoreProgressStatus.value === 'queued-restart' || restoreProgressStatus.value === 'restarting') {
      return
    }
    resetRestoreProgressState()
  }

  async function handleDeleteBackup(filename: string) {
    try {
      await ElMessageBox.confirm('确定要删除该备份吗？', '确认', { type: 'warning' })
      await systemApi.deleteBackup(filename)
      ElMessage.success('删除成功')
      void loadBackups()
    } catch (err: any) {
      if (err === 'cancel' || err?.toString?.() === 'cancel') return
      ElMessage.error('删除备份失败')
    }
  }

  async function load2FAStatus() {
    try {
      const res = await securityApi.get2FAStatus()
      twoFAEnabled.value = res.data.enabled
    } catch {
      // ignore
    }
  }

  async function handleChangePassword() {
    if (!oldPassword.value || !newPassword.value) {
      ElMessage.warning('请填写密码')
      return
    }
    if (newPassword.value !== confirmPassword.value) {
      ElMessage.warning('两次输入的密码不一致')
      return
    }
    if (newPassword.value.length < 6) {
      ElMessage.warning('密码至少 6 位')
      return
    }
    try {
      const res = await authApi.changePassword(oldPassword.value, newPassword.value)
      // 后端文案会说明通行密钥已一并清除，优先用它
      ElMessage.success(res?.message || '密码修改成功，即将跳转到登录页')
      oldPassword.value = ''
      newPassword.value = ''
      confirmPassword.value = ''
      setTimeout(() => {
        authStore.logout()
      }, 1500)
    } catch {
      ElMessage.error('密码修改失败')
    }
  }

  async function handleSetup2FA() {
    try {
      const res = await securityApi.setup2FA()
      twoFASecret.value = res.data.secret
      twoFAUri.value = res.data.uri
      twoFAQrUrl.value = await createQrCodeDataUrl(res.data.uri, 200)
      twoFACode.value = ''
      showSetup2FA.value = true
    } catch {
      ElMessage.error('初始化 2FA 失败')
    }
  }

  async function handleVerify2FA() {
    if (!twoFACode.value) {
      ElMessage.warning('请输入验证码')
      return
    }
    try {
      await securityApi.verify2FA(twoFACode.value)
      ElMessage.success('2FA 已启用')
      twoFAEnabled.value = true
      showSetup2FA.value = false
    } catch {
      ElMessage.error('验证码错误')
    }
  }

  async function handleDisable2FA() {
    let prompted: { value: string }
    try {
      prompted = await ElMessageBox.prompt(
        '禁用前请输入当前的动态验证码（认证器 App 上 6 位数字）以确认操作。',
        '禁用双因素认证',
        {
          inputPattern: /^\d{6}$/,
          inputErrorMessage: '请输入 6 位动态验证码',
          confirmButtonText: '确认禁用',
          cancelButtonText: '取消',
          type: 'warning',
          inputPlaceholder: '6 位数字验证码'
        }
      ) as { value: string }
    } catch {
      return
    }
    try {
      await securityApi.disable2FA(prompted.value.trim())
      ElMessage.success('2FA 已禁用')
      twoFAEnabled.value = false
    } catch (err: any) {
      ElMessage.error(err?.response?.data?.error || '禁用 2FA 失败')
    }
  }

  async function loadLoginLogs() {
    loginLogsLoading.value = true
    try {
      const res = await securityApi.loginLogs({ page: loginLogsPage.value, page_size: 15 })
      loginLogs.value = res.data || []
      loginLogsTotal.value = res.total || 0
    } catch {
      ElMessage.error('加载登录日志失败')
    } finally {
      loginLogsLoading.value = false
    }
  }

  async function loadSessions() {
    sessionsLoading.value = true
    try {
      const res = await securityApi.sessions()
      sessions.value = res.data || []
    } catch {
      ElMessage.error('加载会话列表失败')
    } finally {
      sessionsLoading.value = false
    }
  }

  async function handleRevokeSession(id: number) {
    try {
      await ElMessageBox.confirm('确定要撤销该会话吗？被撤销的设备将需要重新登录。', '确认', { type: 'warning' })
      await securityApi.revokeSession(id)
      ElMessage.success('会话已撤销')
      try {
        await loadSessions()
      } catch {
        // If loading sessions fails (401), current session was revoked
        authStore.clearAuth()
        void router.push('/login')
      }
    } catch (err: any) {
      if (err === 'cancel' || err?.toString?.() === 'cancel') return
      ElMessage.error('操作失败')
    }
  }

  async function handleRevokeAllSessions() {
    try {
      await ElMessageBox.confirm('确定要撤销所有其他会话吗？', '确认', { type: 'warning' })
      await securityApi.revokeAllSessions()
      ElMessage.success('已撤销所有其他会话')
      void loadSessions()
    } catch {
      // cancelled
    }
  }

  async function loadIPWhitelist() {
    ipWhitelistLoading.value = true
    try {
      const res = await securityApi.ipWhitelist()
      ipWhitelist.value = res.data || []
    } catch {
      ElMessage.error('加载 IP 白名单失败')
    } finally {
      ipWhitelistLoading.value = false
    }
  }

  async function handleAddIP() {
    if (!newIP.value.trim()) {
      ElMessage.warning('IP 或网段不能为空')
      return
    }
    // IP 非空校验通过后才置位，复位放 finally
    ipAdding.value = true
    try {
      await securityApi.addIPWhitelist({ ip: newIP.value.trim(), remarks: newIPRemarks.value })
      ElMessage.success('添加成功')
      showAddIPDialog.value = false
      newIP.value = ''
      newIPRemarks.value = ''
      void loadIPWhitelist()
    } catch {
      ElMessage.error('添加失败')
    } finally {
      ipAdding.value = false
    }
  }

  async function handleRemoveIP(id: number) {
    try {
      await ElMessageBox.confirm('确定要移除该 IP 吗？', '确认', { type: 'warning' })
      await securityApi.removeIPWhitelist(id)
      ElMessage.success('删除成功')
      void loadIPWhitelist()
    } catch {
      // cancelled
    }
  }

  async function handleClearLoginLogs() {
    try {
      await ElMessageBox.confirm('确定要清除所有登录日志吗？此操作不可恢复。', '确认', { type: 'warning' })
      const res = await securityApi.clearLoginLogs() as any
      ElMessage.success(res.message || '清除成功')
      void loadLoginLogs()
    } catch (e: any) {
      if (e !== 'cancel' && e?.toString() !== 'cancel') {
        ElMessage.error('清除失败')
      }
    }
  }

  function handleSecurityTabChange(tab: string) {
    if (tab === 'login-logs') void loadLoginLogs()
    else if (tab === 'sessions') void loadSessions()
    else if (tab === 'ip-whitelist') void loadIPWhitelist()
  }

  onBeforeUnmount(() => {
    stopRestoreCountdown()
    stopRestoreProgressPolling()
    stopRestartProbe()
  })

  return {
    securityTab,
    backups,
    backupsLoading,
    showBackupDialog,
    backupName,
    backupPassword,
    backupSelection,
    backupScheduleSelection,
    uploadProgress,
    uploadUploading,
    backupCreating,
    showRestoreDialog,
    restoreFilename,
    restorePassword,
    restoreProgressVisible,
    restoreProgressStatus,
    restoreProgressStage,
    restoreProgressMessage,
    restoreProgressPercent,
    restoreProgressSource,
    restoreProgressSelection,
    restoreProgressStartedAt,
    restoreRestartCountdown,
    restoreProgressError,
    oldPassword,
    newPassword,
    confirmPassword,
    twoFAEnabled,
    twoFASecret,
    twoFAUri,
    twoFAQrUrl,
    twoFACode,
    showSetup2FA,
    loginLogs,
    loginLogsLoading,
    loginLogsTotal,
    loginLogsPage,
    sessions,
    sessionsLoading,
    ipWhitelist,
    ipWhitelistLoading,
    showAddIPDialog,
    newIP,
    newIPRemarks,
    ipAdding,
    loadBackups,
    handleCreateBackup,
    handleUploadBackup,
    confirmCreateBackup,
    handleDownloadBackup,
    handleRestoreBackup,
    confirmRestore,
    closeRestoreProgress,
    restartRestoreNow,
    handleDeleteBackup,
    load2FAStatus,
    handleChangePassword,
    handleSetup2FA,
    handleVerify2FA,
    handleDisable2FA,
    loadLoginLogs,
    loadSessions,
    handleRevokeSession,
    handleRevokeAllSessions,
    loadIPWhitelist,
    handleAddIP,
    handleRemoveIP,
    handleClearLoginLogs,
    handleSecurityTabChange
  }
}
