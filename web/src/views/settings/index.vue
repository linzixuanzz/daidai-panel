<script setup lang="ts">
import { computed, onActivated, onMounted, ref, watch } from 'vue'
import { useAuthStore } from '@/stores/auth'
import AlertConfigCard from './components/AlertConfigCard.vue'
import AuditLogsCard from './components/AuditLogsCard.vue'
import BackupManagementCard from './components/BackupManagementCard.vue'
import CaptchaConfigCard from './components/CaptchaConfigCard.vue'
import ExtraConfigCard from './components/ExtraConfigCard.vue'
import IPWhitelistCard from './components/IPWhitelistCard.vue'
import LoginLogsCard from './components/LoginLogsCard.vue'
import McpConfigCard from './components/McpConfigCard.vue'
import OverviewHeroCard from './components/OverviewHeroCard.vue'
import OverviewStatsCard from './components/OverviewStatsCard.vue'
import PanelLogCard from './components/PanelLogCard.vue'
import ProxyConfigCard from './components/ProxyConfigCard.vue'
import SecurityPassword2FACard from './components/SecurityPassword2FACard.vue'
import SecurityPasskeyCard from './components/SecurityPasskeyCard.vue'
import SessionManagementCard from './components/SessionManagementCard.vue'
import SystemConfigCard from './components/SystemConfigCard.vue'
import SystemHealthCard from './components/SystemHealthCard.vue'
import SystemInfoCard from './components/SystemInfoCard.vue'
import TaskExecutionCard from './components/TaskExecutionCard.vue'
import WecomTriggerCard from './components/WecomTriggerCard.vue'
import { useSettingsConfig } from './useSettingsConfig'
import { useSettingsOverview } from './useSettingsOverview'
import { usePanelLogViewer } from './usePanelLogViewer'
import { useSettingsSecurity } from './useSettingsSecurity'
import { Bell, Connection, Document, Key, Lock, Monitor, Refresh, Tickets } from '@element-plus/icons-vue'
import { useRoute } from 'vue-router'

const authStore = useAuthStore()
const route = useRoute()
// 通行密钥卡片（lazy 挂载，没点开过时是 null）：顶部「刷新」要能刷到它
const passkeyCardRef = ref<InstanceType<typeof SecurityPasskeyCard> | null>(null)
// 审计日志卡片同样 lazy 挂载、自己拉数据；再次切回这个子标签时靠它刷新
const auditLogsCardRef = ref<InstanceType<typeof AuditLogsCard> | null>(null)
const roleLevel: Record<string, number> = { viewer: 1, operator: 2, admin: 3 }
const isAdmin = computed(() => (roleLevel[authStore.user?.role || ''] || 0) >= (roleLevel.admin || 0))

const activeTab = ref('overview')

const overview = useSettingsOverview()
const config = useSettingsConfig()
// 只在「面板日志」子标签激活时自动刷新（#159 修复 E），停在别的子标签不再后台轮询
const panelLogViewer = usePanelLogViewer(computed(() => activeTab.value === 'panel-log'))
const security = useSettingsSecurity()

const {
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
} = overview

const {
  captchaFeatureImplemented,
  configsLoading,
  configsSaving,
  configForm,
  configSchema,
  autoUpdateLastCheckedAt,
  extraConfigGroups,
  extraConfigDraft,
  handleSaveExtraConfigs,
  loadSystemConfigs,
  handleSaveSystemConfig,
  handleSaveAlertConfig,
  handleIconUpload,
  handleLogBackgroundUpload,
  previewPanelAppearance,
  handleSaveTaskConfig,
  handleSaveProxy,
  handleSaveCaptcha,
  handleSaveSessionConfig,
  handleSaveMcpConfig,
  handleSaveWecomTriggerConfig,
  handleSaveBackupSchedule
} = config

const {
  loading: panelLogLoading,
  refreshing: panelLogRefreshing,
  lines: panelLogLines,
  keyword: panelLogKeyword,
  level: panelLogLevel,
  autoRefresh: panelLogAutoRefresh,
  logs: panelLogs,
  total: panelLogTotal,
  lastLoadedAt: panelLogLastLoadedAt,
  byteSizeLabel: panelLogByteSizeLabel,
  activePreset: panelLogActivePreset,
  loadPanelLogs,
  refreshNow: refreshPanelLogs,
  applyUpdatePreset: applyPanelLogUpdatePreset,
  applyErrorPreset: applyPanelLogErrorPreset,
  resetFilters: resetPanelLogFilters,
  copyLogs: copyPanelLogs,
  downloadLogs: downloadPanelLogs,
} = panelLogViewer

const {
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
  // 创建备份的在途锁，透传给备份卡片绑「创建」按钮的 loading
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
  // 添加 IP 白名单的在途锁，透传给白名单卡片绑「添加」按钮的 loading
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
} = security

function handleRefresh() {
  handleTabChange(activeTab.value)
}

function serializeBackupScheduleSelection() {
  const order: Array<keyof typeof backupScheduleSelection.value> = [
    'configs',
    'tasks',
    'subscriptions',
    'env_vars',
    'logs',
    'scripts',
    'dependencies',
    'task_views'
  ]
  return order.filter((key) => backupScheduleSelection.value[key]).join(',')
}

function applyBackupScheduleSelection(raw: string) {
  const selected = new Set(
    raw
      .split(',')
      .map((item) => item.trim())
      .filter(Boolean)
  )
  backupScheduleSelection.value = {
    configs: selected.has('configs'),
    tasks: selected.has('tasks'),
    subscriptions: selected.has('subscriptions'),
    env_vars: selected.has('env_vars'),
    logs: selected.has('logs'),
    scripts: selected.has('scripts'),
    dependencies: selected.has('dependencies'),
    task_views: selected.has('task_views')
  }
}

function handleTabChange(tab: string) {
  if (tab === 'overview') {
    void loadVersion()
    void loadSystemStats()
    void loadSystemInfo()
  } else if (tab === 'config' || tab === 'task-exec' || tab === 'proxy' || tab === 'captcha' || tab === 'alert' || tab === 'mcp' || tab === 'wecom') {
    void loadSystemConfigs()
  } else if (tab === 'panel-log') {
    void loadPanelLogs()
  } else if (tab === 'backup') {
    void loadBackups()
    void loadSystemConfigs()
  } else if (tab === 'security') {
    void load2FAStatus()
    if (securityTab.value === 'sessions') void loadSystemConfigs()
    if (securityTab.value === 'passkeys') void passkeyCardRef.value?.reload()
    if (securityTab.value === 'audit-logs') void auditLogsCardRef.value?.reload()
  }
}

// 「会话管理」里的会话数上限也走 useSettingsConfig 的保存，没成功加载过配置时保存会被拦下，
// 所以进这个子标签也要拉一次配置（顺带让上限显示服务端的真实值，而不是本地默认的 1）
function handleSecuritySubTabChange(tab: string) {
  handleSecurityTabChange(tab)
  if (tab === 'sessions') void loadSystemConfigs()
  // 第一次点开时卡片还没挂载（ref 为 null），由它自己的 onMounted 加载
  if (tab === 'audit-logs') void auditLogsCardRef.value?.reload()
}

onMounted(() => {
  void loadVersion()
  void loadSystemStats()
  void loadSystemInfo()
  if (!isAdmin.value) {
    securityTab.value = 'password-2fa'
  }
})

// 从别处带 query 直达某个子页签（#175：个人资料页「通行密钥 · 管理」跳过来是
// ?tab=security&security_tab=passkeys）。本页在 MainLayout 的 keep-alive 里，第二次进来只触发 onActivated，
// 所以放在 onActivated（首次挂载时它也会在 onMounted 之后跑一次）。
// 程序改 v-model 不会触发 el-tabs 的 tab-change，加载要自己调
onActivated(() => {
  if (route.query.tab === 'security') {
    activeTab.value = 'security'
    const sub = route.query.security_tab
    const allowedSubTabs = isAdmin.value
      ? ['password-2fa', 'passkeys', 'login-logs', 'audit-logs', 'sessions', 'ip-whitelist']
      : ['password-2fa', 'passkeys']
    if (typeof sub === 'string' && allowedSubTabs.includes(sub)) {
      securityTab.value = sub
      handleSecuritySubTabChange(sub)
    }
    handleTabChange('security')
  }
})

watch(
  () => configForm.value.backup_schedule_selection,
  (value) => {
    applyBackupScheduleSelection(value || '')
  },
  { immediate: true }
)
</script>

<template>
  <div class="settings-page dd-scroll-page dd-page-hide-heading">
    <div class="settings-toolbar">
      <el-button @click="handleRefresh">
        <el-icon><Refresh /></el-icon> 刷新
      </el-button>
    </div>

    <el-tabs v-model="activeTab" @tab-change="handleTabChange">
      <el-tab-pane label="概览" name="overview">
        <div class="overview-grid">
          <OverviewHeroCard
            :is-admin="isAdmin"
            :current-version="currentVersion"
            :update-info="updateInfo"
            :update-status="updateStatus"
            :system-info="systemInfo"
            :checking-update="checkingUpdate"
            :updating-panel="updatingPanel"
            :stopping-panel="stoppingPanel"
            :release-notes-visible="releaseNotesVisible"
            :update-progress-visible="updateProgressVisible"
            :update-progress-status="updateProgressStatus"
            :update-progress-error="updateProgressError"
            :on-check-update="handleCheckUpdate"
            :on-start-update="handleUpdatePanel"
            :on-restart-panel="handleRestartPanel"
            :on-stop-panel="handleStopPanel"
            :on-open-release-notes="openReleaseNotes"
            :on-close-release-notes="closeReleaseNotes"
            :on-open-git-hub="openGitHub"
            :on-close-update-progress="closeUpdateProgress"
          />
        </div>

        <OverviewStatsCard :system-stats="systemStats" style="margin-bottom: 16px" />

        <div class="overview-info-grid">
          <SystemInfoCard
            :system-info="systemInfo"
            :format-bytes="formatBytes"
            :get-usage-class="getUsageClass"
          />
          <SystemHealthCard />
        </div>
      </el-tab-pane>

      <!-- name 仍是 config：handleTabChange 靠它加载配置，标签名改成「通用设置」不影响这里 -->
      <el-tab-pane v-if="isAdmin" label="通用设置" name="config">
        <div class="config-grid">
          <SystemConfigCard
            :configs-loading="configsLoading"
            :configs-saving="configsSaving"
            :form="configForm"
            :config-schema="configSchema"
            :on-save="handleSaveSystemConfig"
            :on-icon-upload="handleIconUpload"
            :on-log-background-upload="handleLogBackgroundUpload"
            :on-appearance-preview="previewPanelAppearance"
          />

          <!--
            兜底卡片：渲染面板注册了、但本页没有专属表单的配置项，按服务端 schema 分组展示；
            哪些项进兜底区由 useSettingsConfig 的差集算出，不在这里写死。没有这类配置项时整卡不渲染。
          -->
          <ExtraConfigCard
            v-if="extraConfigGroups.length"
            :configs-loading="configsLoading"
            :configs-saving="configsSaving"
            :groups="extraConfigGroups"
            :draft="extraConfigDraft"
            :on-save="handleSaveExtraConfigs"
          />
        </div>
      </el-tab-pane>

      <el-tab-pane v-if="isAdmin" label="任务运行" name="task-exec">
        <TaskExecutionCard
          :configs-loading="configsLoading"
          :configs-saving="configsSaving"
          :form="configForm"
          :config-schema="configSchema"
          :on-save="handleSaveTaskConfig"
        />
      </el-tab-pane>

      <el-tab-pane v-if="isAdmin" name="panel-log">
        <template #label>
          <span class="sub-tab-label"><el-icon :size="14"><Document /></el-icon>面板日志</span>
        </template>
        <PanelLogCard
          v-model:lines="panelLogLines"
          v-model:keyword="panelLogKeyword"
          v-model:level="panelLogLevel"
          v-model:auto-refresh="panelLogAutoRefresh"
          :loading="panelLogLoading"
          :refreshing="panelLogRefreshing"
          :logs="panelLogs"
          :total="panelLogTotal"
          :last-loaded-at="panelLogLastLoadedAt"
          :byte-size-label="panelLogByteSizeLabel"
          :active-preset="panelLogActivePreset"
          :on-refresh="refreshPanelLogs"
          :on-apply-update-preset="applyPanelLogUpdatePreset"
          :on-apply-error-preset="applyPanelLogErrorPreset"
          :on-reset-filters="resetPanelLogFilters"
          :on-copy="copyPanelLogs"
          :on-download="downloadPanelLogs"
        />
      </el-tab-pane>

      <el-tab-pane v-if="isAdmin" name="alert">
        <template #label>
          <span class="sub-tab-label"><el-icon :size="14"><Bell /></el-icon>告警通知</span>
        </template>
        <AlertConfigCard
          :configs-loading="configsLoading"
          :configs-saving="configsSaving"
          :form="configForm"
          :on-save="handleSaveAlertConfig"
        />
      </el-tab-pane>

      <el-tab-pane v-if="isAdmin" label="代理设置" name="proxy">
        <ProxyConfigCard
          :configs-loading="configsLoading"
          :configs-saving="configsSaving"
          :form="configForm"
          :auto-update-last-checked-at="autoUpdateLastCheckedAt"
          :on-save="handleSaveProxy"
        />
      </el-tab-pane>

      <el-tab-pane v-if="isAdmin" label="登录验证码" name="captcha">
        <CaptchaConfigCard
          :configs-saving="configsSaving"
          :form="configForm"
          :captcha-feature-implemented="captchaFeatureImplemented"
          :on-save="handleSaveCaptcha"
        />
      </el-tab-pane>

      <el-tab-pane v-if="isAdmin" label="MCP 服务" name="mcp">
        <McpConfigCard
          :configs-loading="configsLoading"
          :configs-saving="configsSaving"
          :form="configForm"
          :on-save="handleSaveMcpConfig"
        />
      </el-tab-pane>

      <!-- lazy：这张卡挂载时会自己去拉接入配置与开放 API 应用两份列表，
           不加 lazy 的话设置页一打开（哪怕从不切到这个标签）就会白发两个请求 -->
      <el-tab-pane v-if="isAdmin" label="企业微信触发" name="wecom" lazy>
        <WecomTriggerCard
          :configs-loading="configsLoading"
          :configs-saving="configsSaving"
          :form="configForm"
          :on-save="handleSaveWecomTriggerConfig"
        />
      </el-tab-pane>

      <el-tab-pane v-if="isAdmin" label="备份恢复" name="backup">
        <BackupManagementCard
          v-model:show-backup-dialog="showBackupDialog"
          v-model:backup-name="backupName"
          v-model:backup-password="backupPassword"
          v-model:backup-selection="backupSelection"
          v-model:backup-schedule-selection="backupScheduleSelection"
          v-model:show-restore-dialog="showRestoreDialog"
          v-model:restore-password="restorePassword"
          :settings-form="configForm"
          :configs-saving="configsSaving"
          :on-save-schedule="() => handleSaveBackupSchedule(serializeBackupScheduleSelection())"
          :backups="backups"
          :backups-loading="backupsLoading"
          :restore-filename="restoreFilename"
          :restore-progress-visible="restoreProgressVisible"
          :restore-progress-status="restoreProgressStatus"
          :restore-progress-stage="restoreProgressStage"
          :restore-progress-message="restoreProgressMessage"
          :restore-progress-percent="restoreProgressPercent"
          :restore-progress-source="restoreProgressSource"
          :restore-progress-selection="restoreProgressSelection"
          :restore-progress-started-at="restoreProgressStartedAt"
          :restore-restart-countdown="restoreRestartCountdown"
          :restore-progress-error="restoreProgressError"
          :upload-progress="uploadProgress"
          :upload-uploading="uploadUploading"
          :backup-creating="backupCreating"
          :on-create-backup="handleCreateBackup"
          :on-upload-backup="handleUploadBackup"
          :on-confirm-create-backup="confirmCreateBackup"
          :on-download-backup="handleDownloadBackup"
          :on-restore-backup="handleRestoreBackup"
          :on-confirm-restore="confirmRestore"
          :on-close-restore-progress="closeRestoreProgress"
          :on-restart-restore-now="restartRestoreNow"
          :on-delete-backup="handleDeleteBackup"
        />
      </el-tab-pane>

      <el-tab-pane label="账号安全" name="security">
        <el-tabs v-model="securityTab" @tab-change="handleSecuritySubTabChange">
          <el-tab-pane name="password-2fa">
            <template #label>
              <span class="sub-tab-label"><el-icon :size="14"><Lock /></el-icon>密码与2FA</span>
            </template>
            <SecurityPassword2FACard
              v-model:old-password="oldPassword"
              v-model:new-password="newPassword"
              v-model:confirm-password="confirmPassword"
              v-model:show-setup2-f-a="showSetup2FA"
              v-model:two-f-a-code="twoFACode"
              :two-f-a-enabled="twoFAEnabled"
              :two-f-a-secret="twoFASecret"
              :two-f-a-qr-url="twoFAQrUrl"
              :on-change-password="handleChangePassword"
              :on-setup2-f-a="handleSetup2FA"
              :on-disable2-f-a="handleDisable2FA"
              :on-verify2-f-a="handleVerify2FA"
            />
          </el-tab-pane>

          <!-- 通行密钥（#175）：任何角色都能管自己的。lazy：第一次点开才挂载，卡片挂载时自己拉列表，
               不在每次打开设置页时白发一个请求 -->
          <el-tab-pane name="passkeys" lazy>
            <template #label>
              <span class="sub-tab-label"><el-icon :size="14"><Key /></el-icon>通行密钥</span>
            </template>
            <SecurityPasskeyCard ref="passkeyCardRef" />
          </el-tab-pane>

          <el-tab-pane v-if="isAdmin" name="login-logs">
            <template #label>
              <span class="sub-tab-label"><el-icon :size="14"><Document /></el-icon>登录日志</span>
            </template>
            <LoginLogsCard
              v-model:login-logs-page="loginLogsPage"
              :login-logs="loginLogs"
              :login-logs-loading="loginLogsLoading"
              :login-logs-total="loginLogsTotal"
              :on-load-login-logs="loadLoginLogs"
              :on-clear-login-logs="handleClearLoginLogs"
            />
          </el-tab-pane>

          <!-- 审计日志：改密、备份恢复、命令行、配置修改这类敏感操作的留痕。lazy：点开才挂载并拉数据 -->
          <el-tab-pane v-if="isAdmin" name="audit-logs" lazy>
            <template #label>
              <span class="sub-tab-label"><el-icon :size="14"><Tickets /></el-icon>审计日志</span>
            </template>
            <AuditLogsCard ref="auditLogsCardRef" />
          </el-tab-pane>

          <el-tab-pane v-if="isAdmin" name="sessions">
            <template #label>
              <span class="sub-tab-label"><el-icon :size="14"><Monitor /></el-icon>会话管理</span>
            </template>
            <SessionManagementCard
              :sessions="sessions"
              :sessions-loading="sessionsLoading"
              :config-form="configForm"
              :config-saving="configsSaving"
              :on-load-sessions="loadSessions"
              :on-revoke-all-sessions="handleRevokeAllSessions"
              :on-revoke-session="handleRevokeSession"
              :on-save-session-config="handleSaveSessionConfig"
            />
          </el-tab-pane>

          <el-tab-pane v-if="isAdmin" name="ip-whitelist">
            <template #label>
              <span class="sub-tab-label"><el-icon :size="14"><Connection /></el-icon>IP白名单</span>
            </template>
            <IPWhitelistCard
              v-model:show-add-i-p-dialog="showAddIPDialog"
              v-model:new-i-p="newIP"
              v-model:new-i-p-remarks="newIPRemarks"
              :ip-whitelist="ipWhitelist"
              :ip-whitelist-loading="ipWhitelistLoading"
              :ip-adding="ipAdding"
              :on-load-i-p-whitelist="loadIPWhitelist"
              :on-add-i-p="handleAddIP"
              :on-remove-i-p="handleRemoveIP"
            />
          </el-tab-pane>
        </el-tabs>
      </el-tab-pane>
    </el-tabs>
  </div>
</template>

<style scoped lang="scss">
.settings-page { padding: 0; }

.settings-toolbar {
  display: flex;
  justify-content: flex-end;
  align-items: center;
  margin-bottom: 12px;
  padding: 4px;
  // 按钮组的灰底槽 → control 档（与槽内按钮同档，圆角一致才不会露出内外错位的角）
  border-radius: var(--dd-radius-control);
  background: color-mix(in srgb, var(--el-fill-color-light) 84%, transparent);
}

.page-header {
  display: flex;
  justify-content: space-between;
  align-items: flex-start;
  margin-bottom: 18px;
  gap: 16px;

  h2 { margin: 0; font-size: 22px; font-weight: 700; color: var(--el-text-color-primary); line-height: 1.3; }
  .page-subtitle { font-size: 13px; color: var(--el-text-color-secondary); margin: 6px 0 0; line-height: 1.6; display: block; max-width: 720px; }
}

.page-title {
  font-size: 22px;
  font-weight: 700;
  margin: 0;
  color: var(--el-text-color-primary);
  line-height: 1.3;
}

.sub-tab-label {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  font-weight: 500;
}

// 「通用设置」页：左「面板外观」、右「其它配置项」两栏 1:1，间距与概览信息网格一致（16px）。
// 两卡高度不同，顶部对齐、不拉伸，短卡下方不拖一截空白卡身（同 config-file 页 .side-panel）。
// 固定两栏、不按兜底卡在不在切单栏：兜底卡要等配置加载完才出现，动态切栏会让左卡宽度跳一下
.config-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 16px;
  align-items: start;
}

// 概览主区：只剩「产品与版本」一张卡，单列水平居中并限宽，宽屏上不拉成通栏；
// 入场淡入上移走切页动效令牌（reduced-motion 由 global.scss 统一降级）
.overview-grid {
  display: grid;
  animation: dd-settings-rise-in var(--dd-motion-page) var(--dd-ease-decelerate) both;
  grid-template-columns: minmax(0, 720px);
  justify-content: center;
  margin-bottom: 16px;
}

:deep(.el-tabs) {
  .el-tabs__header {
    margin-bottom: 20px;
    // 整排标签栏右移，让首个标签「概览」左侧留出呼吸空间；
    // nav-wrap（含标签、active-bar 下划线、::after 轨道线）整体右移、对齐不变
    padding-left: 16px;
  }

  // 所有标签下方的 1px 细线作为下划线指示条的轨道
  .el-tabs__nav-wrap::after {
    height: 1px;
    background-color: color-mix(in srgb, var(--el-border-color-lighter) 82%, transparent);
  }

  .el-tabs__item {
    font-size: 14px;
    // hover / 选中过渡统一走快速档动效令牌，与全站一致
    transition:
      color var(--dd-motion-fast) var(--dd-ease-standard);

    &:hover {
      // 未选中项 hover 时用品牌色提示
      color: var(--el-color-primary);
    }

    &.is-active {
      font-weight: 600;
      // 选中项仅用品牌色文字，配合底部下划线指示条
      color: var(--el-color-primary);
    }
  }

  // 下划线指示条：品牌色，跟随选中项滑动，无填充背景不会被裁切
  .el-tabs__active-bar {
    height: 3px;
    // 3px 高的下划线指示条属天然胶囊（与进度条同类）→ pill 档
    border-radius: var(--dd-radius-pill);
    background-color: var(--el-color-primary);
  }
}

// 概览统计卡：夹在两个网格之间，给同款入场动画保持节奏一致（轻微延迟错落）
:deep(.stats-card) {
  animation: dd-settings-rise-in var(--dd-motion-page) var(--dd-ease-decelerate) 30ms both;
}

// 概览信息网格：入场动画与主网格一致，轻微延迟形成克制的错落
.overview-info-grid {
  display: grid;
  animation: dd-settings-rise-in var(--dd-motion-page) var(--dd-ease-decelerate) 60ms both;
  grid-template-columns: 1fr 1fr;
  gap: 16px;

  :deep(.mt-card) {
    margin-top: 0;
  }
}

// 设置页入场动画：小幅淡入上移，避免大量子元素逐个 stagger
@keyframes dd-settings-rise-in {
  from {
    opacity: 0;
    transform: translateY(8px);
  }

  to {
    opacity: 1;
    transform: translateY(0);
  }
}

// 「通用设置」两栏在 ≤960px 收成单栏，断点沿用仪表盘页已有的 960 档
@media (max-width: 960px) {
  .config-grid { grid-template-columns: minmax(0, 1fr); }
}

@media (max-width: 768px) {
  // 手机端「产品与版本」卡占满整行
  .overview-grid { grid-template-columns: minmax(0, 1fr); }
  .overview-info-grid { grid-template-columns: 1fr; }
  .page-header {
    flex-direction: column;
    align-items: stretch;
    gap: 10px;
    margin-bottom: 14px;

    h2 { font-size: 18px; }
  }

  :deep(.el-tabs__nav-wrap) {
    overflow-x: auto;
  }

  :deep(.el-tabs__nav-scroll) {
    min-width: max-content;
  }

  :deep(.el-tabs__item) {
    white-space: nowrap;
  }
}
</style>


<style lang="scss">
html.dark {
  .settings-page .settings-toolbar {
    background: color-mix(in srgb, var(--el-fill-color-light) 72%, black);
  }
}
</style>
