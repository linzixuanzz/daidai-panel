<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import { systemApi } from '@/api/system'
import { configApi } from '@/api/system'
import { securityApi } from '@/api/security'
import { authApi } from '@/api/auth'
import { useAuthStore } from '@/stores/auth'
import { ElMessage, ElMessageBox, ElLoading } from 'element-plus'
import QRCode from 'qrcode'
import {
  Plus, Refresh, Download, Delete, Upload,
  Setting, Clock, Connection, Lock, Key,
  InfoFilled, Monitor, Document, CircleCheck,
} from '@element-plus/icons-vue'

const router = useRouter()
const authStore = useAuthStore()

const activeTab = ref('overview')
const securityTab = ref('password-2fa')

const systemInfo = ref<any>({})
const systemStats = ref<any>(null)
const currentVersion = ref('')
const updateInfo = ref<any>(null)
const checkingUpdate = ref(false)

const backups = ref<any[]>([])
const backupsLoading = ref(false)
const showBackupDialog = ref(false)
const backupPassword = ref('')
const showRestoreDialog = ref(false)
const restoreFilename = ref('')
const restorePassword = ref('')

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

const configsLoading = ref(false)
const configsSaving = ref(false)
const configForm = ref({
  max_concurrent_tasks: 5,
  command_timeout: 300,
  log_retention_days: 7,
  random_delay: '',
  random_delay_extensions: '',
  auto_add_cron: true,
  auto_del_cron: true,
  default_cron_rule: '',
  repo_file_extensions: '',
  cpu_warn: 80,
  memory_warn: 80,
  disk_warn: 90,
  notify_on_resource_warn: false,
  notify_on_login: false,
  proxy_url: '',
  captcha_enabled: false,
  captcha_id: '',
  captcha_key: '',
  panel_title: '',
  panel_icon: '',
})

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
  } catch { /* ignore */ }
}

async function loadSystemStats() {
  try {
    const res = await systemApi.stats()
    systemStats.value = res.data || {}
  } catch { /* ignore */ }
}

async function loadVersion() {
  try {
    const res = await systemApi.version()
    currentVersion.value = res.data.version || ''
  } catch { /* ignore */ }
}

async function loadSystemConfigs() {
  configsLoading.value = true
  try {
    const res = await configApi.list()
    const cfgs = res.data || {}
    const g = (key: string, def: any) => cfgs[key]?.value !== undefined ? cfgs[key].value : def

    configForm.value = {
      max_concurrent_tasks: Number(g('max_concurrent_tasks', 5)),
      command_timeout: Number(g('command_timeout', 300)),
      log_retention_days: Number(g('log_retention_days', 7)),
      random_delay: g('random_delay', ''),
      random_delay_extensions: g('random_delay_extensions', ''),
      auto_add_cron: g('auto_add_cron', 'true') === 'true',
      auto_del_cron: g('auto_del_cron', 'true') === 'true',
      default_cron_rule: g('default_cron_rule', ''),
      repo_file_extensions: g('repo_file_extensions', ''),
      cpu_warn: Number(g('cpu_warn', 80)),
      memory_warn: Number(g('memory_warn', 80)),
      disk_warn: Number(g('disk_warn', 90)),
      notify_on_resource_warn: g('notify_on_resource_warn', 'false') === 'true',
      notify_on_login: g('notify_on_login', 'false') === 'true',
      proxy_url: g('proxy_url', ''),
      captcha_enabled: g('captcha_enabled', 'false') === 'true',
      captcha_id: g('captcha_id', ''),
      captcha_key: g('captcha_key', ''),
      panel_title: g('panel_title', ''),
      panel_icon: g('panel_icon', ''),
    }
  } catch {
    ElMessage.error('加载配置失败')
  } finally {
    configsLoading.value = false
  }
}

async function saveConfigKeys(keys: string[]) {
  configsSaving.value = true
  try {
    const configs: Record<string, string> = {}
    for (const key of keys) {
      const val = (configForm.value as any)[key]
      configs[key] = typeof val === 'boolean' ? (val ? 'true' : 'false') : String(val ?? '')
    }
    await configApi.batchSet(configs)
    ElMessage.success('配置已保存')
  } catch {
    ElMessage.error('保存失败')
  } finally {
    configsSaving.value = false
  }
}

function handleSaveSystemConfig() {
  saveConfigKeys([
    'auto_add_cron', 'auto_del_cron', 'default_cron_rule', 'repo_file_extensions',
    'cpu_warn', 'memory_warn', 'disk_warn', 'notify_on_resource_warn', 'notify_on_login',
    'panel_title', 'panel_icon',
  ])
}

function handleIconUpload(file: File) {
  if (!file.name.endsWith('.svg')) {
    ElMessage.warning('仅支持 SVG 格式图标')
    return false
  }
  if (file.size > 100 * 1024) {
    ElMessage.warning('图标文件不能超过 100KB')
    return false
  }
  const reader = new FileReader()
  reader.onload = (e) => {
    configForm.value.panel_icon = e.target?.result as string
  }
  reader.readAsDataURL(file)
  return false
}

function handleSaveTaskConfig() {
  saveConfigKeys([
    'max_concurrent_tasks', 'command_timeout', 'log_retention_days',
    'random_delay', 'random_delay_extensions',
  ])
}

function handleSaveProxy() {
  saveConfigKeys(['proxy_url'])
}

function handleSaveCaptcha() {
  saveConfigKeys(['captcha_enabled', 'captcha_id', 'captcha_key'])
}

async function handleCheckUpdate() {
  checkingUpdate.value = true
  updateInfo.value = null
  try {
    const res = await systemApi.checkUpdate()
    updateInfo.value = res.data
    if (res.data.has_update) {
      ElMessageBox.confirm(
        `发现新版本可用！\n\n当前版本：v${res.data.current}\n最新版本：v${res.data.latest}\n\n是否立即更新？更新过程中服务将短暂中断。`,
        '发现新版本',
        {
          confirmButtonText: '立即更新',
          cancelButtonText: '稍后手动更新',
          type: 'success',
          center: true
        }
      ).then(async () => {
        await handleUpdatePanel()
      }).catch(() => {
        ElMessage.info('您可以稍后在 GitHub Releases 页面手动下载更新')
      })
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

async function handleUpdatePanel() {
  const loading = ElLoading.service({
    lock: true,
    text: '正在拉取最新镜像，请稍候...',
    background: 'rgba(0, 0, 0, 0.7)'
  })
  try {
    await systemApi.updatePanel()
    loading.close()
    ElMessageBox.alert(
      '镜像拉取已开始，容器将在拉取完成后自动重启。\n\n页面将在 10 秒后自动刷新，如未恢复请手动刷新。',
      '更新进行中',
      {
        confirmButtonText: '知道了',
        type: 'success',
        showClose: false,
        closeOnClickModal: false,
        closeOnPressEscape: false
      }
    )
    setTimeout(() => {
      window.location.reload()
    }, 10000)
  } catch (err: any) {
    loading.close()
    const msg = err?.response?.data?.error || '更新失败，请手动更新'
    ElMessage.error(msg)
  }
}

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
  backupPassword.value = ''
}

async function confirmCreateBackup() {
  try {
    await systemApi.backup(backupPassword.value)
    ElMessage.success('备份创建成功')
    showBackupDialog.value = false
    backupPassword.value = ''
    loadBackups()
  } catch {
    ElMessage.error('备份失败')
  }
}

async function handleDownloadBackup(filename: string) {
  const token = localStorage.getItem('access_token')
  window.open(`${systemApi.downloadBackup(filename)}?token=${token}`, '_blank')
}

async function handleRestoreBackup(filename: string) {
  restoreFilename.value = filename
  restorePassword.value = ''
  showRestoreDialog.value = true
}

async function confirmRestore() {
  try {
    await systemApi.restore(restoreFilename.value, restorePassword.value)
    ElMessage.success('恢复成功，请重启面板')
    showRestoreDialog.value = false
  } catch {
    ElMessage.error('恢复失败')
  }
}

async function handleDeleteBackup(filename: string) {
  try {
    await ElMessageBox.confirm('确定要删除该备份吗？', '确认', { type: 'warning' })
    await systemApi.deleteBackup(filename)
    ElMessage.success('删除成功')
    loadBackups()
  } catch { /* cancelled */ }
}

async function load2FAStatus() {
  try {
    const res = await securityApi.get2FAStatus()
    twoFAEnabled.value = res.data.enabled
  } catch { /* ignore */ }
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
    await authApi.changePassword(oldPassword.value, newPassword.value)
    ElMessage.success('密码修改成功，即将跳转到登录页')
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
    twoFAQrUrl.value = await QRCode.toDataURL(res.data.uri, { width: 200, margin: 2 })
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
  try {
    await ElMessageBox.confirm('确定要禁用两步验证吗？', '确认', { type: 'warning' })
    await securityApi.disable2FA()
    ElMessage.success('2FA 已禁用')
    twoFAEnabled.value = false
  } catch { /* cancelled */ }
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
    await securityApi.revokeSession(id)
    ElMessage.success('会话已撤销，即将重新登录')
    authStore.clearAuth()
    setTimeout(() => router.push('/login'), 500)
  } catch {
    ElMessage.error('操作失败')
  }
}

async function handleRevokeAllSessions() {
  try {
    await ElMessageBox.confirm('确定要撤销所有其他会话吗？', '确认', { type: 'warning' })
    await securityApi.revokeAllSessions()
    ElMessage.success('已撤销所有其他会话')
    loadSessions()
  } catch { /* cancelled */ }
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
    ElMessage.warning('IP 地址不能为空')
    return
  }
  try {
    await securityApi.addIPWhitelist({ ip: newIP.value.trim(), remarks: newIPRemarks.value })
    ElMessage.success('添加成功')
    showAddIPDialog.value = false
    newIP.value = ''
    newIPRemarks.value = ''
    loadIPWhitelist()
  } catch {
    ElMessage.error('添加失败')
  }
}

async function handleRemoveIP(id: number) {
  try {
    await ElMessageBox.confirm('确定要移除该 IP 吗？', '确认', { type: 'warning' })
    await securityApi.removeIPWhitelist(id)
    ElMessage.success('删除成功')
    loadIPWhitelist()
  } catch { /* cancelled */ }
}

async function handleClearLoginLogs() {
  try {
    await ElMessageBox.confirm('确定要清除所有登录日志吗？此操作不可恢复。', '确认', { type: 'warning' })
    const res = await securityApi.clearLoginLogs() as any
    ElMessage.success(res.message || '清除成功')
    loadLoginLogs()
  } catch (e: any) {
    if (e !== 'cancel' && e?.toString() !== 'cancel') {
      ElMessage.error('清除失败')
    }
  }
}

function handleRefresh() {
  handleTabChange(activeTab.value)
}

function handleTabChange(tab: string) {
  if (tab === 'overview') {
    loadVersion()
    loadSystemStats()
    loadSystemInfo()
  } else if (tab === 'config' || tab === 'task-exec' || tab === 'proxy' || tab === 'captcha') {
    loadSystemConfigs()
  } else if (tab === 'backup') {
    loadBackups()
  } else if (tab === 'security') {
    load2FAStatus()
  }
}

function handleSecurityTabChange(tab: string) {
  if (tab === 'login-logs') loadLoginLogs()
  else if (tab === 'sessions') loadSessions()
  else if (tab === 'ip-whitelist') loadIPWhitelist()
}

function openGitHub() {
  const url = updateInfo.value?.has_update && updateInfo.value?.release_url
    ? updateInfo.value.release_url
    : 'https://github.com/linzixuanzz/daidai-panel/releases'
  window.open(url, '_blank')
}

onMounted(() => {
  loadVersion()
  loadSystemStats()
  loadSystemInfo()
  loadSystemConfigs()
  load2FAStatus()
})
</script>

<template>
  <div class="settings-page">
    <div class="page-header">
      <h2 class="page-title">系统设置</h2>
      <el-button @click="handleRefresh">
        <el-icon><Refresh /></el-icon>刷新
      </el-button>
    </div>

    <el-tabs v-model="activeTab" @tab-change="handleTabChange">
      <el-tab-pane label="概览" name="overview">
        <el-card shadow="never" class="overview-card">
          <div class="overview-center">
            <div class="logo">
              <img src="/favicon.svg" alt="呆呆面板" class="logo-img" />
            </div>
            <h2 class="panel-name">呆呆面板</h2>
            <p class="panel-desc">轻量级定时任务管理面板</p>
            <div class="version-stats">
              <div class="version-item">
                <span class="vs-label">系统版本</span>
                <span class="vs-value">{{ currentVersion }}</span>
              </div>
              <div class="version-item">
                <span class="vs-label">技术栈</span>
                <span class="vs-value">Gin + Vue3</span>
              </div>
            </div>
            <div class="overview-buttons">
              <el-button type="primary" :loading="checkingUpdate" @click="handleCheckUpdate">
                <el-icon><Refresh /></el-icon>检查系统更新
              </el-button>
              <el-button @click="openGitHub">
                <svg viewBox="0 0 16 16" width="16" height="16" style="margin-right: 4px; vertical-align: middle; fill: currentColor">
                  <path d="M8 0C3.58 0 0 3.58 0 8c0 3.54 2.29 6.53 5.47 7.59.4.07.55-.17.55-.38 0-.19-.01-.82-.01-1.49-2.01.37-2.53-.49-2.69-.94-.09-.23-.48-.94-.82-1.13-.28-.15-.68-.52-.01-.53.63-.01 1.08.58 1.23.82.72 1.21 1.87.87 2.33.66.07-.52.28-.87.51-1.07-1.78-.2-3.64-.89-3.64-3.95 0-.87.31-1.59.82-2.15-.08-.2-.36-1.02.08-2.12 0 0 .67-.21 2.2.82.64-.18 1.32-.27 2-.27.68 0 1.36.09 2 .27 1.53-1.04 2.2-.82 2.2-.82.44 1.1.16 1.92.08 2.12.51.56.82 1.27.82 2.15 0 3.07-1.87 3.75-3.65 3.95.29.25.54.73.54 1.48 0 1.07-.01 1.93-.01 2.2 0 .21.15.46.55.38A8.013 8.013 0 0016 8c0-4.42-3.58-8-8-8z"/>
                </svg>
                GitHub
              </el-button>
            </div>
            <div v-if="updateInfo" style="margin-top: 20px; width: 100%; max-width: 500px">
              <el-alert
                :type="updateInfo.has_update ? 'success' : 'info'"
                :title="updateInfo.has_update ? `发现新版本 v${updateInfo.latest}` : '当前已是最新版本'"
                :closable="false"
              >
                <div v-if="updateInfo.has_update">
                  <p>发布时间: {{ new Date(updateInfo.published_at).toLocaleString() }}</p>
                  <a :href="updateInfo.release_url" target="_blank">
                    <el-button type="primary" size="small">查看更新说明</el-button>
                  </a>
                </div>
              </el-alert>
            </div>
          </div>
        </el-card>

        <el-card shadow="never" class="mt-card" v-if="systemStats">
          <template #header>
            <div class="card-header">
              <span class="card-title"><el-icon><InfoFilled /></el-icon> 面板概况</span>
            </div>
          </template>
          <div class="overview-stats-row">
            <div class="os-item">
              <div class="os-label">任务总数</div>
              <div class="os-value">{{ systemStats.tasks?.total || 0 }}</div>
            </div>
            <div class="os-item">
              <div class="os-label">已启用</div>
              <div class="os-value color-success">{{ systemStats.tasks?.enabled || 0 }}</div>
            </div>
            <div class="os-item">
              <div class="os-label">运行中</div>
              <div class="os-value color-warning">{{ systemStats.tasks?.running || 0 }}</div>
            </div>
            <div class="os-item">
              <div class="os-label">执行日志</div>
              <div class="os-value">{{ systemStats.logs?.total || 0 }}</div>
            </div>
            <div class="os-item">
              <div class="os-label">成功率</div>
              <div class="os-value color-success">{{ (systemStats.logs?.success_rate || 0).toFixed(1) }}%</div>
            </div>
            <div class="os-item">
              <div class="os-label">脚本数</div>
              <div class="os-value">{{ systemStats.scripts?.total || 0 }}</div>
            </div>
          </div>
        </el-card>

        <el-card shadow="never" class="mt-card" v-if="systemInfo">
          <template #header>
            <div class="card-header">
              <span class="card-title"><el-icon><InfoFilled /></el-icon> 系统信息</span>
            </div>
          </template>
          <div class="system-info-grid">
            <div class="si-item">
              <div class="si-label">主机名</div>
              <div class="si-value">{{ systemInfo.hostname || '-' }}</div>
            </div>
            <div class="si-item">
              <div class="si-label">操作系统</div>
              <div class="si-value">{{ systemInfo.os || '-' }} {{ systemInfo.arch || '' }}</div>
            </div>
            <div class="si-item">
              <div class="si-label">Go</div>
              <div class="si-value">{{ systemInfo.go_version || '-' }}</div>
            </div>
            <div class="si-item">
              <div class="si-label">数据目录</div>
              <div class="si-value">{{ systemInfo.data_dir || '-' }}</div>
            </div>
            <div class="si-item">
              <div class="si-label">CPU 使用率</div>
              <div class="si-value" :class="getUsageClass(systemInfo.cpu_usage)">
                {{ systemInfo.cpu_usage || 0 }}%&nbsp;&nbsp;({{ systemInfo.num_cpu || 0 }} 核)
              </div>
            </div>
            <div class="si-item">
              <div class="si-label">内存使用</div>
              <div class="si-value" :class="getUsageClass(systemInfo.memory_usage)">
                {{ systemInfo.memory_usage || 0 }}%&nbsp;&nbsp;({{ formatBytes(systemInfo.memory_used) }} / {{ formatBytes(systemInfo.memory_total) }})
              </div>
            </div>
            <div class="si-item">
              <div class="si-label">磁盘使用</div>
              <div class="si-value" :class="getUsageClass(systemInfo.disk_usage)">
                {{ systemInfo.disk_usage || 0 }}%&nbsp;&nbsp;({{ formatBytes(systemInfo.disk_used) }} / {{ formatBytes(systemInfo.disk_total) }})
              </div>
            </div>
          </div>
        </el-card>
      </el-tab-pane>

      <el-tab-pane label="系统配置" name="config">
        <el-card shadow="never" v-loading="configsLoading">
          <template #header>
            <div class="card-header">
              <span class="card-title"><el-icon><Setting /></el-icon> 系统配置</span>
              <el-button type="primary" :loading="configsSaving" @click="handleSaveSystemConfig">
                <el-icon><Document /></el-icon>保存配置
              </el-button>
            </div>
          </template>

          <div class="config-section">
            <h4 class="section-title">面板设置</h4>
            <div class="form-field">
              <label>面板标题</label>
              <el-input v-model="configForm.panel_title" placeholder="呆呆面板" />
              <span class="form-hint">自定义面板的站点标题，留空使用默认值"呆呆面板"</span>
            </div>
            <div class="form-field">
              <label>面板图标 (SVG)</label>
              <div style="display: flex; align-items: center; gap: 12px">
                <el-upload
                  :show-file-list="false"
                  :before-upload="handleIconUpload"
                  accept=".svg"
                >
                  <el-button size="small"><el-icon><Upload /></el-icon>上传 SVG 图标</el-button>
                </el-upload>
                <div v-if="configForm.panel_icon" class="icon-preview">
                  <img :src="configForm.panel_icon" alt="icon" style="width: 32px; height: 32px" />
                  <el-button size="small" text type="danger" @click="configForm.panel_icon = ''">移除</el-button>
                </div>
              </div>
              <span class="form-hint">上传 SVG 格式图标自定义面板图标，留空使用默认图标</span>
            </div>
          </div>

          <div class="config-section">
            <h4 class="section-title">订阅设置</h4>
            <div class="switch-row">
              <div class="switch-item">
                <span class="switch-label">自动添加定时任务</span>
                <el-switch v-model="configForm.auto_add_cron" inline-prompt active-text="开" inactive-text="关" />
              </div>
              <div class="switch-item">
                <span class="switch-label">自动删除失效任务</span>
                <el-switch v-model="configForm.auto_del_cron" inline-prompt active-text="开" inactive-text="关" />
              </div>
            </div>
            <div class="form-field">
              <label>默认 Cron 规则</label>
              <el-input v-model="configForm.default_cron_rule" placeholder="0 9 * * *" />
              <span class="form-hint">匹配不到定时规则时使用，如 0 9 * * *</span>
            </div>
            <div class="form-field">
              <label>拉取文件后缀</label>
              <el-input v-model="configForm.repo_file_extensions" placeholder="py js sh ts" />
              <span class="form-hint">空格分隔，如 py js sh ts</span>
            </div>
          </div>

          <div class="config-section">
            <h4 class="section-title">资源告警</h4>
            <el-row :gutter="16">
              <el-col :span="8">
                <div class="form-field">
                  <label>CPU 阈值 (%)</label>
                  <el-input v-model.number="configForm.cpu_warn" />
                </div>
              </el-col>
              <el-col :span="8">
                <div class="form-field">
                  <label>内存阈值 (%)</label>
                  <el-input v-model.number="configForm.memory_warn" />
                </div>
              </el-col>
              <el-col :span="8">
                <div class="form-field">
                  <label>磁盘阈值 (%)</label>
                  <el-input v-model.number="configForm.disk_warn" />
                </div>
              </el-col>
            </el-row>
            <div class="switch-row">
              <div class="switch-item">
                <span class="switch-label">资源超限发送通知</span>
                <el-switch v-model="configForm.notify_on_resource_warn" inline-prompt active-text="开" inactive-text="关" />
              </div>
            </div>
            <div class="switch-row">
              <div class="switch-item">
                <span class="switch-label">登录成功发送通知</span>
                <el-switch v-model="configForm.notify_on_login" inline-prompt active-text="开" inactive-text="关" />
              </div>
            </div>
            <span class="form-hint">开启后，每次登录成功将向所有已启用的通知渠道发送通知</span>
          </div>
        </el-card>
      </el-tab-pane>

      <el-tab-pane label="任务执行" name="task-exec">
        <el-card shadow="never" v-loading="configsLoading">
          <template #header>
            <div class="card-header">
              <span class="card-title"><el-icon><Clock /></el-icon> 任务执行</span>
              <el-button type="primary" :loading="configsSaving" @click="handleSaveTaskConfig">
                <el-icon><Document /></el-icon>保存配置
              </el-button>
            </div>
          </template>

          <div class="form-field">
            <label>全局默认超时（秒）</label>
            <el-input v-model.number="configForm.command_timeout" />
            <span class="form-hint">单个任务未设超时时使用此值</span>
          </div>
          <div class="form-field">
            <label>定时任务并发数</label>
            <el-input v-model.number="configForm.max_concurrent_tasks" />
            <span class="form-hint">同时执行的最大任务数量</span>
          </div>
          <div class="form-field">
            <label>日志删除频率</label>
            <div class="compound-input">
              <span>每</span>
              <el-input v-model.number="configForm.log_retention_days" style="width: 120px" />
              <span>天</span>
            </div>
            <span class="form-hint">自动删除超过指定天数的任务日志（每天凌晨 3 点执行）</span>
          </div>
          <div class="form-field">
            <label>随机延迟最大秒数</label>
            <el-input v-model="configForm.random_delay" placeholder="如 300 表示 1~300 秒随机延迟" />
            <span class="form-hint">留空或 0 表示不延迟</span>
          </div>
          <div class="form-field">
            <label>延迟文件后缀</label>
            <el-input v-model="configForm.random_delay_extensions" placeholder="如 js py" />
            <span class="form-hint">空格分隔，留空表示全部任务</span>
          </div>
        </el-card>
      </el-tab-pane>

      <el-tab-pane label="网络代理" name="proxy">
        <el-card shadow="never">
          <template #header>
            <div class="card-header">
              <span class="card-title"><el-icon><Connection /></el-icon> 网络代理</span>
              <el-button type="primary" :loading="configsSaving" @click="handleSaveProxy">
                <el-icon><Document /></el-icon>保存配置
              </el-button>
            </div>
          </template>

          <div class="form-field">
            <label>代理地址</label>
            <el-input v-model="configForm.proxy_url" placeholder="http://127.0.0.1:7890" />
            <span class="form-hint">支持 HTTP/SOCKS5，如 http://127.0.0.1:7890</span>
          </div>
        </el-card>
      </el-tab-pane>

      <el-tab-pane label="验证码设置" name="captcha">
        <el-card shadow="never">
          <template #header>
            <div class="card-header">
              <span class="card-title"><el-icon><Key /></el-icon> 验证码设置</span>
              <el-button type="primary" :loading="configsSaving" @click="handleSaveCaptcha">
                <el-icon><Document /></el-icon>保存配置
              </el-button>
            </div>
          </template>

          <div class="switch-row" style="margin-bottom: 4px">
            <div class="switch-item">
              <span class="switch-label">启用极验验证码</span>
              <el-switch v-model="configForm.captcha_enabled" inline-prompt active-text="开" inactive-text="关" />
            </div>
          </div>
          <span class="form-hint" style="display: block; margin-bottom: 20px">
            开启后，登录密码错误 3 次将触发极验 V4 验证码
          </span>
          <div class="form-field">
            <label>Captcha ID</label>
            <el-input v-model="configForm.captcha_id" placeholder="请输入极验 Captcha ID" />
            <span class="form-hint">极验后台获取的 Captcha ID</span>
          </div>
          <div class="form-field">
            <label>Captcha Key</label>
            <el-input v-model="configForm.captcha_key" type="password" show-password placeholder="请输入极验 Captcha Key" />
            <span class="form-hint">极验后台获取的 Captcha Key（服务端密钥）</span>
          </div>
        </el-card>
      </el-tab-pane>

      <el-tab-pane label="数据备份" name="backup">
        <el-card shadow="never">
          <template #header>
            <div class="card-header">
              <span class="card-title"><el-icon><Clock /></el-icon> 数据备份与恢复</span>
              <el-button type="primary" @click="handleCreateBackup">
                <el-icon><Upload /></el-icon>创建备份
              </el-button>
            </div>
          </template>

          <el-table :data="backups" v-loading="backupsLoading" empty-text="暂无备份">
            <el-table-column prop="name" label="文件名" min-width="200" />
            <el-table-column label="大小" width="120">
              <template #default="{ row }">{{ (row.size / 1024).toFixed(2) }} KB</template>
            </el-table-column>
            <el-table-column prop="created_at" label="创建时间" width="170">
              <template #default="{ row }">{{ new Date(row.created_at).toLocaleString() }}</template>
            </el-table-column>
            <el-table-column label="操作" width="220" fixed="right" align="center">
              <template #default="{ row }">
                <div style="display: flex; gap: 4px; justify-content: center">
                  <el-button size="small" type="primary" plain @click="handleDownloadBackup(row.name)">
                    <el-icon><Download /></el-icon>下载
                  </el-button>
                  <el-button size="small" type="success" plain @click="handleRestoreBackup(row.name)">
                    <el-icon><Upload /></el-icon>恢复
                  </el-button>
                  <el-button size="small" type="danger" plain @click="handleDeleteBackup(row.name)">
                    <el-icon><Delete /></el-icon>删除
                  </el-button>
                </div>
              </template>
            </el-table-column>
          </el-table>
        </el-card>
      </el-tab-pane>

      <el-tab-pane label="安全" name="security">
        <el-tabs v-model="securityTab" @tab-change="handleSecurityTabChange">
          <el-tab-pane name="password-2fa">
            <template #label>
              <span class="sub-tab-label"><el-icon :size="14"><Lock /></el-icon>密码与2FA</span>
            </template>
            <el-row :gutter="16">
              <el-col :xs="24" :sm="24" :md="14">
                <el-card shadow="never">
                  <template #header>
                    <div class="card-header">
                      <span class="card-title"><el-icon><Lock /></el-icon> 修改密码</span>
                    </div>
                  </template>
                  <el-form label-position="top" style="max-width: 420px">
                    <el-form-item label="* 当前密码">
                      <el-input v-model="oldPassword" type="password" show-password placeholder="当前密码" />
                    </el-form-item>
                    <el-form-item label="* 新密码">
                      <el-input v-model="newPassword" type="password" show-password placeholder="新密码（至少 8 位）" />
                    </el-form-item>
                    <el-form-item label="* 确认密码">
                      <el-input v-model="confirmPassword" type="password" show-password placeholder="再次输入新密码" />
                    </el-form-item>
                    <el-form-item>
                      <el-button type="primary" @click="handleChangePassword">
                        <el-icon><CircleCheck /></el-icon>修改密码
                      </el-button>
                    </el-form-item>
                  </el-form>
                </el-card>
              </el-col>
              <el-col :xs="24" :sm="24" :md="10">
                <el-card shadow="never">
                  <template #header>
                    <div class="card-header">
                      <span class="card-title"><el-icon><Key /></el-icon> 双因素认证 (2FA)</span>
                      <el-tag :type="twoFAEnabled ? 'success' : 'info'" size="small">
                        {{ twoFAEnabled ? '已启用' : '未启用' }}
                      </el-tag>
                    </div>
                  </template>
                  <p class="twofa-desc">
                    双因素认证为您的账户提供额外的安全保护。启用后，登录时除了密码外，还需要输入认证器应用生成的验证码。
                  </p>
                  <el-button v-if="!twoFAEnabled" type="primary" @click="handleSetup2FA">
                    <el-icon><Key /></el-icon>启用双因素认证
                  </el-button>
                  <el-button v-else type="danger" @click="handleDisable2FA">禁用双因素认证</el-button>
                </el-card>
              </el-col>
            </el-row>
          </el-tab-pane>

          <el-tab-pane name="login-logs">
            <template #label>
              <span class="sub-tab-label"><el-icon :size="14"><Document /></el-icon>登录日志</span>
            </template>
            <el-card shadow="never">
              <template #header>
                <div class="card-header">
                  <span class="card-title"><el-icon><Document /></el-icon> 登录日志</span>
                  <div class="card-header-buttons">
                    <el-button @click="loadLoginLogs"><el-icon><Refresh /></el-icon>刷新</el-button>
                    <el-button @click="handleClearLoginLogs"><el-icon><Delete /></el-icon>清理旧日志</el-button>
                  </div>
                </div>
              </template>
              <el-table :data="loginLogs" v-loading="loginLogsLoading" stripe empty-text="暂无数据">
                <el-table-column prop="username" label="用户" width="100" />
                <el-table-column label="状态" width="80">
                  <template #default="{ row }">
                    <el-tag size="small" :type="row.status === 0 ? 'success' : 'danger'">
                      {{ row.status === 0 ? '成功' : '失败' }}
                    </el-tag>
                  </template>
                </el-table-column>
                <el-table-column prop="ip" label="IP地址" width="140" />
                <el-table-column prop="method" label="登录方式" width="100" />
                <el-table-column prop="message" label="原因" show-overflow-tooltip />
                <el-table-column prop="created_at" label="时间" width="170">
                  <template #default="{ row }">{{ new Date(row.created_at).toLocaleString() }}</template>
                </el-table-column>
              </el-table>
              <div class="pagination-container" v-if="loginLogsTotal > 15">
                <el-pagination
                  v-model:current-page="loginLogsPage"
                  :total="loginLogsTotal"
                  :page-size="15"
                  layout="prev, pager, next"
                  @current-change="loadLoginLogs"
                />
              </div>
            </el-card>
          </el-tab-pane>

          <el-tab-pane name="sessions">
            <template #label>
              <span class="sub-tab-label"><el-icon :size="14"><Monitor /></el-icon>会话管理</span>
            </template>
            <el-card shadow="never">
              <template #header>
                <div class="card-header">
                  <span class="card-title"><el-icon><Monitor /></el-icon> 活动会话</span>
                  <div class="card-header-buttons">
                    <el-button @click="loadSessions"><el-icon><Refresh /></el-icon>刷新</el-button>
                    <el-button type="danger" plain @click="handleRevokeAllSessions">撤销所有其他会话</el-button>
                  </div>
                </div>
              </template>
              <el-table :data="sessions" v-loading="sessionsLoading" stripe empty-text="暂无数据">
                <el-table-column prop="ip" label="IP地址" width="140" />
                <el-table-column prop="user_agent" label="用户代理" show-overflow-tooltip />
                <el-table-column label="最后活动" width="170">
                  <template #default="{ row }">{{ new Date(row.last_active || row.created_at).toLocaleString() }}</template>
                </el-table-column>
                <el-table-column label="操作" width="100" fixed="right">
                  <template #default="{ row }">
                    <el-button size="small" text type="danger" @click="handleRevokeSession(row.id)">撤销</el-button>
                  </template>
                </el-table-column>
              </el-table>
            </el-card>
          </el-tab-pane>

          <el-tab-pane name="ip-whitelist">
            <template #label>
              <span class="sub-tab-label"><el-icon :size="14"><Connection /></el-icon>IP白名单</span>
            </template>
            <el-card shadow="never">
              <template #header>
                <div class="card-header">
                  <span class="card-title"><el-icon><Connection /></el-icon> IP白名单</span>
                  <div class="card-header-buttons">
                    <el-button @click="loadIPWhitelist"><el-icon><Refresh /></el-icon>刷新</el-button>
                    <el-button type="primary" @click="showAddIPDialog = true">
                      <el-icon><Plus /></el-icon>添加IP
                    </el-button>
                  </div>
                </div>
              </template>
              <el-table :data="ipWhitelist" v-loading="ipWhitelistLoading" stripe empty-text="暂无数据">
                <el-table-column prop="ip" label="IP地址" min-width="200" />
                <el-table-column prop="remarks" label="描述" min-width="200" />
                <el-table-column label="状态" width="80">
                  <template #default>
                    <el-tag type="success" size="small">启用</el-tag>
                  </template>
                </el-table-column>
                <el-table-column prop="created_at" label="创建时间" width="170">
                  <template #default="{ row }">{{ new Date(row.created_at).toLocaleString() }}</template>
                </el-table-column>
                <el-table-column label="操作" width="100" fixed="right">
                  <template #default="{ row }">
                    <el-button size="small" text type="danger" @click="handleRemoveIP(row.id)">移除</el-button>
                  </template>
                </el-table-column>
              </el-table>
            </el-card>
          </el-tab-pane>
        </el-tabs>
      </el-tab-pane>
    </el-tabs>

    <el-dialog v-model="showBackupDialog" title="创建备份" width="400px">
      <el-form label-width="100px">
        <el-form-item label="备份密码">
          <el-input v-model="backupPassword" type="password" placeholder="可选，留空则不加密" show-password />
        </el-form-item>
        <el-alert type="info" :closable="false" show-icon>
          设置密码后备份将被加密，恢复时需要输入相同密码
        </el-alert>
      </el-form>
      <template #footer>
        <el-button @click="showBackupDialog = false">取消</el-button>
        <el-button type="primary" @click="confirmCreateBackup">创建</el-button>
      </template>
    </el-dialog>

    <el-dialog v-model="showRestoreDialog" title="恢复备份" width="400px">
      <el-form label-width="100px">
        <el-form-item label="备份文件">
          <el-input :model-value="restoreFilename" disabled />
        </el-form-item>
        <el-form-item label="备份密码" v-if="restoreFilename.endsWith('.enc')">
          <el-input v-model="restorePassword" type="password" placeholder="请输入备份密码" show-password />
        </el-form-item>
        <el-alert type="warning" :closable="false" show-icon>
          恢复将覆盖当前数据，请谨慎操作！
        </el-alert>
      </el-form>
      <template #footer>
        <el-button @click="showRestoreDialog = false">取消</el-button>
        <el-button type="danger" @click="confirmRestore">确认恢复</el-button>
      </template>
    </el-dialog>

    <el-dialog v-model="showSetup2FA" title="设置双因素认证" width="480px" :close-on-click-modal="false">
      <div class="setup-2fa">
        <div class="setup-2fa-step">
          <div class="step-title">步骤 1：扫描二维码</div>
          <div class="qr-code-wrapper">
            <img v-if="twoFAQrUrl" :src="twoFAQrUrl" alt="2FA QR Code" class="qr-code-img" />
          </div>
          <div class="step-hint">使用 Google Authenticator、Microsoft Authenticator 或其他 TOTP 认证器应用扫描此二维码</div>
        </div>
        <div class="setup-2fa-step">
          <div class="step-title">或手动输入密钥</div>
          <div class="secret-display">
            <code>{{ twoFASecret }}</code>
          </div>
        </div>
        <div class="setup-2fa-step">
          <div class="step-title">步骤 2：输入验证码</div>
          <el-input v-model="twoFACode" placeholder="请输入 6 位验证码" maxlength="6" size="large" style="width: 220px" @keyup.enter="handleVerify2FA" />
        </div>
      </div>
      <template #footer>
        <el-button @click="showSetup2FA = false">取消</el-button>
        <el-button type="primary" @click="handleVerify2FA">验证并启用</el-button>
      </template>
    </el-dialog>

    <el-dialog v-model="showAddIPDialog" title="添加 IP 白名单" width="400px">
      <el-form label-width="80px">
        <el-form-item label="IP 地址">
          <el-input v-model="newIP" placeholder="如: 192.168.1.100" />
        </el-form-item>
        <el-form-item label="备注">
          <el-input v-model="newIPRemarks" placeholder="备注说明 (可选)" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="showAddIPDialog = false">取消</el-button>
        <el-button type="primary" @click="handleAddIP">添加</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<style scoped lang="scss">
.settings-page {
  padding: 20px;
}

.page-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 16px;
}

.page-title {
  font-size: 20px;
  font-weight: 600;
  margin: 0;
}

.mt-card {
  margin-top: 16px;
}

.card-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
}

.card-title {
  font-weight: 600;
  font-size: 15px;
  display: inline-flex;
  align-items: center;
  gap: 6px;
}

.card-header-buttons {
  display: flex;
  gap: 8px;
}

.overview-card {
  :deep(.el-card__body) {
    padding: 0;
  }
}

.overview-center {
  display: flex;
  flex-direction: column;
  align-items: center;
  padding: 40px 20px;
}

.logo {
  width: 72px;
  height: 72px;
  border-radius: 18px;
  display: flex;
  align-items: center;
  justify-content: center;
  position: relative;
  margin-bottom: 16px;
  overflow: hidden;
}

.logo-img {
  width: 100%;
  height: 100%;
  object-fit: cover;
  border-radius: 18px;
}

.panel-name {
  font-size: 22px;
  font-weight: 700;
  margin: 0 0 4px;
}

.panel-desc {
  color: var(--el-text-color-secondary);
  font-size: 14px;
  margin: 0 0 28px;
}

.version-stats {
  display: flex;
  gap: 80px;
  margin-bottom: 28px;
}

.version-item {
  text-align: center;
}

.vs-label {
  display: block;
  font-size: 13px;
  color: var(--el-text-color-secondary);
  margin-bottom: 6px;
}

.vs-value {
  font-size: 22px;
  font-weight: 700;
}

.overview-buttons {
  display: flex;
  gap: 12px;
}

.overview-stats-row {
  display: grid;
  grid-template-columns: repeat(6, 1fr);
  text-align: center;
}

.os-item {
  padding: 16px 0;
}

.os-label {
  font-size: 13px;
  color: var(--el-text-color-secondary);
  margin-bottom: 10px;
}

.os-value {
  font-size: 26px;
  font-weight: 700;
}

.color-success {
  color: var(--el-color-success);
}

.color-warning {
  color: var(--el-color-warning);
}

.color-danger {
  color: var(--el-color-danger);
}

.system-info-grid {
  display: grid;
  grid-template-columns: repeat(3, 1fr);
  gap: 24px;
}

.si-item {
  padding: 4px 0;
}

.si-label {
  font-size: 13px;
  color: var(--el-text-color-secondary);
  margin-bottom: 6px;
}

.si-value {
  font-size: 14px;
  font-weight: 600;
}

.usage-success {
  color: var(--el-color-success);
}

.usage-warning {
  color: var(--el-color-warning);
}

.usage-danger {
  color: var(--el-color-danger);
}

.config-section {
  margin-bottom: 28px;

  &:last-child {
    margin-bottom: 0;
  }
}

.section-title {
  font-size: 15px;
  font-weight: 600;
  margin: 0 0 16px;
  padding-bottom: 10px;
  border-bottom: 1px solid var(--el-border-color-lighter);
}

.form-field {
  margin-bottom: 20px;
  max-width: 400px;

  label {
    display: block;
    font-size: 14px;
    color: var(--el-text-color-primary);
    margin-bottom: 8px;
  }
}

.form-hint {
  font-size: 12px;
  color: var(--el-text-color-secondary);
  margin-top: 4px;
  display: block;
}

.compound-input {
  display: flex;
  align-items: center;
  gap: 8px;

  span {
    font-size: 14px;
    white-space: nowrap;
  }
}

.switch-row {
  display: flex;
  gap: 40px;
  margin-bottom: 16px;
}

.switch-item {
  display: flex;
  align-items: center;
  gap: 12px;
}

.switch-label {
  font-size: 14px;
}

.sub-tab-label {
  display: inline-flex;
  align-items: center;
  gap: 4px;
}

.twofa-desc {
  color: var(--el-text-color-secondary);
  font-size: 14px;
  line-height: 1.6;
  margin: 0 0 20px;
}

.setup-2fa {
  .setup-2fa-step {
    margin-bottom: 20px;

    &:last-child {
      margin-bottom: 0;
    }
  }

  .step-title {
    font-weight: 600;
    font-size: 14px;
    margin-bottom: 10px;
    color: var(--el-text-color-primary);
  }

  .step-hint {
    font-size: 12px;
    color: var(--el-text-color-secondary);
    margin-top: 8px;
    text-align: center;
  }

  .qr-code-wrapper {
    text-align: center;
    padding: 16px 0;

    .qr-code-img {
      width: 200px;
      height: 200px;
      border-radius: 8px;
      border: 1px solid var(--el-border-color-light);
    }
  }

  .secret-display {
    padding: 12px;
    background: var(--el-fill-color-light);
    border-radius: 4px;
    text-align: center;

    code {
      font-size: 15px;
      font-weight: 600;
      letter-spacing: 2px;
      user-select: all;
    }
  }
}

.pagination-container {
  display: flex;
  justify-content: center;
  margin-top: 20px;
}

@media (max-width: 768px) {
  .overview-stats-row {
    grid-template-columns: repeat(3, 1fr);
  }

  .system-info-grid {
    grid-template-columns: repeat(2, 1fr);
  }

  .version-stats {
    gap: 40px;
  }

  .switch-row {
    flex-direction: column;
    gap: 12px;
  }
}
</style>
