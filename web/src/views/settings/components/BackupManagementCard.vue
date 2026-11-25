<script setup lang="ts">
import { Clock, Connection, Delete, Document, Download, Files, Setting, Timer, Upload, View } from '@element-plus/icons-vue'
import { ref } from 'vue'
import type { BackupSelection } from '@/api/system'
import { useResponsive } from '@/composables/useResponsive'
import { formatDateTime } from '@/utils/datetime'
import RestoreProgressDialog from './RestoreProgressDialog.vue'

const showBackupDialog = defineModel<boolean>('showBackupDialog', { required: true })
const backupName = defineModel<string>('backupName', { required: true })
const backupPassword = defineModel<string>('backupPassword', { required: true })
const backupSelection = defineModel<BackupSelection>('backupSelection', { required: true })
const backupScheduleSelection = defineModel<BackupSelection>('backupScheduleSelection', { required: true })
const showRestoreDialog = defineModel<boolean>('showRestoreDialog', { required: true })
const restorePassword = defineModel<string>('restorePassword', { required: true })

withDefaults(defineProps<{
  settingsForm: {
    backup_schedule_enabled: boolean
    backup_schedule_frequency: 'daily' | 'weekly' | 'monthly' | string
    backup_schedule_time: string
    backup_schedule_weekday: string
    backup_schedule_monthday: number
    backup_schedule_name: string
    backup_schedule_password: string
  }
  configsSaving: boolean
  backups: Array<{ name: string; size: number; created_at: string }>
  backupsLoading: boolean
  restoreFilename: string
  restoreProgressVisible: boolean
  restoreProgressStatus: string
  restoreProgressStage: string
  restoreProgressMessage: string
  restoreProgressPercent: number
  restoreProgressSource: string
  restoreProgressSelection: Partial<BackupSelection>
  restoreProgressStartedAt?: string
  restoreRestartCountdown: number
  restoreProgressError: string
  uploadProgress: number
  uploadUploading: boolean
  // 创建备份的在途锁，来源与 uploadUploading 同为 useSettingsSecurity，两者互不影响；默认 false
  backupCreating?: boolean
  onCreateBackup: () => void | Promise<void>
  onUploadBackup: (event: Event) => void | Promise<void>
  onSaveSchedule: () => void | Promise<void>
  onConfirmCreateBackup: () => void | Promise<void>
  onDownloadBackup: (filename: string) => void | Promise<void>
  onRestoreBackup: (filename: string) => void | Promise<void>
  onConfirmRestore: () => void | Promise<void>
  onCloseRestoreProgress: () => void | Promise<void>
  onRestartRestoreNow: () => void | Promise<void>
  onDeleteBackup: (filename: string) => void | Promise<void>
}>(), {
  backupCreating: false
})

const backupFileInput = ref<HTMLInputElement | null>(null)
const { isMobile, dialogFullscreen } = useResponsive()

const backupSelectionOptions: Array<{ key: keyof BackupSelection; title: string; description: string; icon: any }> = [
  {
    key: 'configs',
    title: '配置项',
    description: '系统设置、Open API、通知渠道与安全配置',
    icon: Setting,
  },
  {
    key: 'tasks',
    title: '定时任务',
    description: '任务定义、标签、执行参数与依赖关系',
    icon: Timer,
  },
  {
    key: 'subscriptions',
    title: '订阅管理',
    description: '订阅配置与 SSH 密钥',
    icon: Connection,
  },
  {
    key: 'env_vars',
    title: '环境变量',
    description: '面板环境变量与分组信息',
    icon: Document,
  },
  {
    key: 'logs',
    title: '日志文件',
    description: '任务日志记录与面板运行日志',
    icon: Files,
  },
  {
    key: 'scripts',
    title: '脚本文件',
    description: '脚本目录内的源码与可执行文件',
    icon: Document,
  },
  {
    key: 'dependencies',
    title: '依赖记录',
    description: '已安装依赖，恢复时自动重装',
    icon: Files,
  },
  {
    key: 'task_views',
    title: '任务视图',
    description: '分组视图与自定义筛选排序',
    icon: View,
  },
]

function triggerUploadBackup() {
  backupFileInput.value?.click()
}

function updateBackupSelection(key: keyof BackupSelection, value: boolean) {
  backupSelection.value = {
    ...backupSelection.value,
    [key]: value
  }
}

function formatSize(bytes: number): string {
  if (bytes < 1024) return bytes + ' B'
  if (bytes < 1024 * 1024) return (bytes / 1024).toFixed(1) + ' KB'
  if (bytes < 1024 * 1024 * 1024) return (bytes / (1024 * 1024)).toFixed(2) + ' MB'
  return (bytes / (1024 * 1024 * 1024)).toFixed(2) + ' GB'
}

function updateBackupScheduleSelection(key: keyof BackupSelection, value: boolean) {
  backupScheduleSelection.value = {
    ...backupScheduleSelection.value,
    [key]: value
  }
}
</script>

<template>
  <el-card shadow="never">
    <template #header>
      <div class="card-header">
        <span class="card-title"><el-icon><Clock /></el-icon> 数据备份与恢复</span>
        <div class="card-header-buttons">
          <el-button @click="triggerUploadBackup" :loading="uploadUploading">
            <el-icon v-if="!uploadUploading"><Download /></el-icon>{{ uploadUploading ? `导入中 ${uploadProgress}%` : '导入备份' }}
          </el-button>
          <el-button type="primary" @click="onCreateBackup">
            <el-icon><Upload /></el-icon>创建备份
          </el-button>
          <input ref="backupFileInput" type="file" accept=".json,.enc,.tgz,.tar.gz" style="display: none" @change="onUploadBackup" />
        </div>
      </div>
    </template>

    <el-progress
      v-if="uploadUploading"
      :percentage="uploadProgress"
      :stroke-width="6"
      :show-text="false"
      style="margin-bottom: 12px"
    />

    <div v-if="isMobile" v-loading="backupsLoading" class="dd-mobile-list">
      <div
        v-for="row in backups"
        :key="row.name"
        class="dd-mobile-card"
      >
        <div class="dd-mobile-card__header">
          <div class="dd-mobile-card__title-wrap">
            <span class="dd-mobile-card__title">{{ row.name }}</span>
            <span class="dd-mobile-card__subtitle">{{ formatDateTime(row.created_at) }}</span>
          </div>
        </div>
        <div class="dd-mobile-card__body">
          <div class="dd-mobile-card__grid">
            <div class="dd-mobile-card__field">
              <span class="dd-mobile-card__label">大小</span>
              <span class="dd-mobile-card__value">{{ formatSize(row.size) }}</span>
            </div>
          </div>
          <div class="dd-mobile-card__actions">
            <el-button size="small" type="primary" plain @click="onDownloadBackup(row.name)">下载</el-button>
            <el-button size="small" type="success" plain @click="onRestoreBackup(row.name)">恢复</el-button>
            <el-button size="small" type="danger" plain @click="onDeleteBackup(row.name)">删除</el-button>
          </div>
        </div>
      </div>
      <el-empty v-if="!backupsLoading && backups.length === 0" description="暂无备份" />
    </div>

    <el-table v-else :data="backups" v-loading="backupsLoading" empty-text="暂无备份">
      <el-table-column prop="name" label="文件名" min-width="200" />
      <el-table-column label="大小" width="120">
        <template #default="{ row }">{{ formatSize(row.size) }}</template>
      </el-table-column>
      <el-table-column prop="created_at" label="创建时间" width="170">
        <template #default="{ row }">{{ formatDateTime(row.created_at) }}</template>
      </el-table-column>
      <el-table-column label="操作" width="260" fixed="right" align="center">
        <template #default="{ row }">
          <div class="backup-actions">
            <el-button size="small" type="primary" plain @click="onDownloadBackup(row.name)">
              <el-icon><Download /></el-icon>下载
            </el-button>
            <el-button size="small" type="success" plain @click="onRestoreBackup(row.name)">
              <el-icon><Upload /></el-icon>恢复
            </el-button>
            <el-button size="small" type="danger" plain @click="onDeleteBackup(row.name)">
              <el-icon><Delete /></el-icon>删除
            </el-button>
          </div>
        </template>
      </el-table-column>
    </el-table>

    <el-alert type="info" :closable="false" show-icon style="margin-top: 16px" class="backup-format-alert">
      支持导入呆呆面板备份（<code>.tgz</code> / <code>.enc</code> / 旧版 <code>.json</code>）以及青龙面板导出的 <code>.tgz</code> 备份包
    </el-alert>

    <el-divider />

    <section class="schedule-section">
      <div class="schedule-header">
        <div>
          <h4>定时备份</h4>
          <p>按每天、每周或每月自动创建面板备份，便于长期保留和灾备恢复。</p>
        </div>
        <el-button type="primary" :loading="configsSaving" @click="onSaveSchedule">
          保存定时配置
        </el-button>
      </div>

      <div class="schedule-grid">
        <div class="schedule-field schedule-field--switch">
          <label>启用定时备份</label>
          <el-switch v-model="settingsForm.backup_schedule_enabled" inline-prompt active-text="开" inactive-text="关" />
        </div>
        <div class="schedule-field">
          <label>备份频率</label>
          <el-select v-model="settingsForm.backup_schedule_frequency" style="width: 100%">
            <el-option label="每天" value="daily" />
            <el-option label="每周" value="weekly" />
            <el-option label="每月" value="monthly" />
          </el-select>
        </div>
        <div class="schedule-field">
          <label>执行时间</label>
          <el-time-select
            v-model="settingsForm.backup_schedule_time"
            style="width: 100%"
            start="00:00"
            step="00:30"
            end="23:30"
            placeholder="选择执行时间"
          />
        </div>
        <div v-if="settingsForm.backup_schedule_frequency === 'weekly'" class="schedule-field">
          <label>每周执行日</label>
          <el-select v-model="settingsForm.backup_schedule_weekday" style="width: 100%">
            <el-option label="周日" value="0" />
            <el-option label="周一" value="1" />
            <el-option label="周二" value="2" />
            <el-option label="周三" value="3" />
            <el-option label="周四" value="4" />
            <el-option label="周五" value="5" />
            <el-option label="周六" value="6" />
          </el-select>
        </div>
        <div v-if="settingsForm.backup_schedule_frequency === 'monthly'" class="schedule-field">
          <label>每月执行日</label>
          <el-input-number v-model="settingsForm.backup_schedule_monthday" :min="1" :max="28" style="width: 100%" />
        </div>
        <div class="schedule-field">
          <label>文件名前缀</label>
          <el-input v-model="settingsForm.backup_schedule_name" placeholder="可选，例如：daily-auto-backup" />
        </div>
        <div class="schedule-field">
          <label>加密密码</label>
          <el-input v-model="settingsForm.backup_schedule_password" type="password" show-password placeholder="可选，留空则不加密" />
        </div>
      </div>

      <div class="schedule-selection">
        <label class="schedule-selection-title">定时备份内容</label>
        <div class="backup-selection-grid">
          <div
            v-for="option in backupSelectionOptions"
            :key="`schedule-${option.key}`"
            class="backup-selection-card"
            :class="{ 'is-active': backupScheduleSelection[option.key] }"
            @click="updateBackupScheduleSelection(option.key, !backupScheduleSelection[option.key])"
          >
            <div class="backup-selection-card-header">
              <el-icon class="backup-selection-icon" :size="16"><component :is="option.icon" /></el-icon>
              <el-checkbox
                :model-value="backupScheduleSelection[option.key]"
                @click.stop
                @update:model-value="updateBackupScheduleSelection(option.key, Boolean($event))"
              >
                {{ option.title }}
              </el-checkbox>
            </div>
            <span class="backup-selection-hint">{{ option.description }}</span>
          </div>
        </div>
      </div>
    </section>
  </el-card>

  <el-dialog v-model="showBackupDialog" title="创建备份" width="560px" :fullscreen="dialogFullscreen">
    <el-form :label-width="dialogFullscreen ? 'auto' : '100px'" :label-position="dialogFullscreen ? 'top' : 'right'">
      <el-form-item label="备份内容">
        <div class="backup-selection-grid">
          <div
            v-for="option in backupSelectionOptions"
            :key="option.key"
            class="backup-selection-card"
            :class="{ 'is-active': backupSelection[option.key] }"
            @click="updateBackupSelection(option.key, !backupSelection[option.key])"
          >
            <div class="backup-selection-card-header">
              <el-icon class="backup-selection-icon" :size="16"><component :is="option.icon" /></el-icon>
              <el-checkbox
                :model-value="backupSelection[option.key]"
                @click.stop
                @update:model-value="updateBackupSelection(option.key, Boolean($event))"
              >
                {{ option.title }}
              </el-checkbox>
            </div>
            <span class="backup-selection-hint">{{ option.description }}</span>
          </div>
        </div>
      </el-form-item>
      <el-form-item label="备份密码">
        <el-input v-model="backupPassword" type="password" placeholder="可选，留空则不加密" show-password />
      </el-form-item>
      <el-form-item label="备份文件名">
        <el-input v-model="backupName" placeholder="可选，例如：周五全量备份" />
      </el-form-item>
      <el-alert type="info" :closable="false" show-icon class="backup-format-alert">
        创建的备份默认导出为 <code>.tgz</code>，设置密码后会加密为 <code>.enc</code>；如果填写名称，系统会自动补全正确扩展名。
      </el-alert>
    </el-form>
    <template #footer>
      <el-button @click="showBackupDialog = false">取消</el-button>
      <el-button type="primary" :loading="backupCreating" :disabled="backupCreating" @click="onConfirmCreateBackup">创建</el-button>
    </template>
  </el-dialog>

  <el-dialog v-model="showRestoreDialog" title="恢复备份" width="400px" :fullscreen="dialogFullscreen">
    <el-form :label-width="dialogFullscreen ? 'auto' : '100px'" :label-position="dialogFullscreen ? 'top' : 'right'">
      <el-form-item label="备份文件">
        <el-input :model-value="restoreFilename" disabled />
      </el-form-item>
      <el-form-item v-if="restoreFilename.endsWith('.enc')" label="备份密码">
        <el-input v-model="restorePassword" type="password" placeholder="请输入备份密码" show-password />
      </el-form-item>
      <el-alert type="warning" :closable="false" show-icon>
        恢复将覆盖当前数据，请谨慎操作！
      </el-alert>
    </el-form>
    <template #footer>
      <el-button @click="showRestoreDialog = false">取消</el-button>
      <el-button type="danger" @click="onConfirmRestore">确认恢复</el-button>
    </template>
  </el-dialog>

  <RestoreProgressDialog
    :visible="restoreProgressVisible"
    :fullscreen="dialogFullscreen"
    :filename="restoreFilename"
    :status="restoreProgressStatus"
    :stage="restoreProgressStage"
    :message="restoreProgressMessage"
    :percent="restoreProgressPercent"
    :source="restoreProgressSource"
    :selection="restoreProgressSelection"
    :started-at="restoreProgressStartedAt"
    :countdown="restoreRestartCountdown"
    :error-message="restoreProgressError"
    :on-close="onCloseRestoreProgress"
    :on-restart-now="onRestartRestoreNow"
  />
</template>

<style scoped lang="scss">
@use './config-card-shared.scss' as *;

.card-header-buttons,
:deep(.el-table) .backup-actions {
  padding: 2px;
  // 按钮组的灰底槽 → control 档（与槽内按钮同档，圆角一致才不会露出内外错位的角）
  border-radius: var(--dd-radius-control);
  background: color-mix(in srgb, var(--el-fill-color-light) 84%, transparent);
  display: flex;
  gap: 8px;
  flex-wrap: wrap;
}

.card-header-buttons :deep(.el-button + .el-button),
:deep(.el-table) .backup-actions .el-button + .el-button {
  margin-left: 0;
}

:deep(.el-table) .backup-actions {
  justify-content: center;
  flex-wrap: nowrap;
}

.backup-format-alert :deep(code) {
  font-family: var(--dd-font-mono);
  font-size: 0.92em;
  padding: 1px 5px;
  border-radius: var(--dd-radius-control);
  background: var(--el-fill-color-light);
  color: var(--el-text-color-primary);
}

.backup-selection-grid {
  display: grid;
  gap: 10px;
  grid-template-columns: repeat(2, minmax(0, 1fr));
}

.backup-selection-card {
  display: flex;
  flex-direction: column;
  gap: 4px;
  padding: 12px 14px;
  border: var(--dd-border-width, 1px) solid var(--dd-divider, var(--el-border-color-light));
  // 备份内容的可选列表卡片属容器类表面 → surface 档
  border-radius: var(--dd-radius-surface);
  background: var(--el-fill-color-extra-light);
  transition: border-color 0.2s ease, background 0.2s ease;
  cursor: pointer;

  &:hover {
    border-color: color-mix(in srgb, var(--dd-accent) 35%, transparent);
    background: var(--el-fill-color-light);
  }

  &.is-active {
    border-color: color-mix(in srgb, var(--dd-accent) 48%, transparent);
    background: color-mix(in srgb, var(--el-color-primary) 6%, var(--el-bg-color));
  }

  :deep(.el-checkbox) {
    align-items: center;
    line-height: 1.4;
  }

  :deep(.el-checkbox__label) {
    font-weight: 600;
    font-size: 13px;
    color: var(--el-text-color-primary);
    padding-left: 6px;
  }
}

.backup-selection-card-header {
  display: flex;
  align-items: center;
  gap: 6px;
}

.backup-selection-icon {
  color: var(--el-text-color-secondary);
  flex-shrink: 0;
}

.backup-selection-card.is-active .backup-selection-icon {
  color: var(--el-color-primary);
}

.backup-selection-hint {
  display: block;
  font-size: 11px;
  color: var(--el-text-color-secondary);
  line-height: 1.5;
  margin-left: 0;
  white-space: normal;
}

.schedule-section {
  display: flex;
  flex-direction: column;
  gap: 18px;
}

.schedule-header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 16px;
  flex-wrap: wrap;

  h4 {
    margin: 0 0 6px;
    font-size: 16px;
    font-weight: 700;
    color: var(--el-text-color-primary);
  }

  p {
    margin: 0;
    font-size: 13px;
    line-height: 1.7;
    color: var(--el-text-color-secondary);
  }
}

.schedule-grid {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 16px 18px;
}

.schedule-field {
  display: flex;
  flex-direction: column;
  gap: 8px;

  label {
    font-size: 13px;
    font-weight: 600;
    color: var(--el-text-color-primary);
  }
}

.schedule-field--switch {
  justify-content: flex-start;
}

.schedule-selection {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.schedule-selection-title {
  font-size: 13px;
  font-weight: 700;
  color: var(--el-text-color-primary);
}

@media (max-width: 768px) {
  .card-header-buttons {
  padding: 2px;
  // 同上：按钮组的灰底槽 → control 档
  border-radius: var(--dd-radius-control);
  background: color-mix(in srgb, var(--el-fill-color-light) 84%, transparent);
    width: 100%;
  }

  .backup-selection-grid {
    grid-template-columns: 1fr;
  }

  .schedule-grid {
    grid-template-columns: 1fr;
  }
}
</style>
