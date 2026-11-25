<script setup lang="ts">
import { Monitor, Refresh } from '@element-plus/icons-vue'
import { useResponsive } from '@/composables/useResponsive'
import { formatRelativeTime } from '@/utils/datetime'
import type { SettingsConfigForm } from '../types'

const { isMobile } = useResponsive()

defineProps<{
  sessions: any[]
  sessionsLoading: boolean
  configForm: SettingsConfigForm
  configSaving: boolean
  onLoadSessions: () => void | Promise<void>
  onRevokeAllSessions: () => void | Promise<void>
  onRevokeSession: (id: number) => void | Promise<void>
  onSaveSessionConfig: () => void | Promise<void>
}>()
</script>

<template>
  <el-card shadow="never">
    <template #header>
      <div class="card-header">
        <span class="card-title"><el-icon><Monitor /></el-icon> 活动会话</span>
        <div class="card-header-buttons">
          <el-button @click="onLoadSessions"><el-icon><Refresh /></el-icon>刷新</el-button>
          <el-button type="danger" plain @click="onRevokeAllSessions">撤销所有其他会话</el-button>
        </div>
      </div>
    </template>
    <div class="session-limit-section">
      <el-form :label-width="isMobile ? 'auto' : '120px'" :label-position="isMobile ? 'top' : 'right'">
        <el-form-item label="网页端会话上限">
          <el-input-number
            :model-value="configForm.max_web_sessions"
            @update:model-value="configForm.max_web_sessions = $event ?? 1"
            :min="1"
            :max="20"
            controls-position="right"
          />
        </el-form-item>
        <el-form-item label="APP 端会话上限">
          <el-input-number
            :model-value="configForm.max_app_sessions"
            @update:model-value="configForm.max_app_sessions = $event ?? 1"
            :min="1"
            :max="20"
            controls-position="right"
          />
        </el-form-item>
        <el-form-item>
          <el-button type="primary" :loading="configSaving" @click="onSaveSessionConfig">保存</el-button>
        </el-form-item>
      </el-form>
      <div class="session-limit-hint">
        设置同一用户可同时保持的最大会话数，超出限制时最早的会话将被自动踢下线。默认 1 表示每次登录会顶掉之前的会话。
      </div>
    </div>

    <el-divider />

    <div v-if="isMobile" v-loading="sessionsLoading" class="dd-mobile-list">
      <div
        v-for="row in sessions"
        :key="row.id"
        class="dd-mobile-card"
      >
        <div class="dd-mobile-card__header">
          <div class="dd-mobile-card__title-wrap">
            <span class="dd-mobile-card__title">{{ row.ip }}</span>
            <span class="dd-mobile-card__subtitle">
              {{ row.client_name || row.client_type_label || '网页端' }} · {{ formatRelativeTime(row.last_active || row.created_at) }}
            </span>
          </div>
        </div>
        <div class="dd-mobile-card__body">
          <div class="dd-mobile-card__grid">
            <div class="dd-mobile-card__field dd-mobile-card__field--full">
              <span class="dd-mobile-card__label">客户端</span>
              <span class="dd-mobile-card__value">{{ row.client_name || row.client_type_label || '-' }}</span>
            </div>
            <div class="dd-mobile-card__field dd-mobile-card__field--full">
              <span class="dd-mobile-card__label">用户代理</span>
              <span class="dd-mobile-card__value">{{ row.user_agent }}</span>
            </div>
          </div>
          <div class="dd-mobile-card__actions session-card__actions">
            <el-button size="small" type="danger" plain @click="onRevokeSession(row.id)">撤销</el-button>
          </div>
        </div>
      </div>
      <el-empty v-if="!sessionsLoading && sessions.length === 0" description="暂无数据" />
    </div>

    <el-table v-else :data="sessions" v-loading="sessionsLoading" stripe empty-text="暂无数据">
      <el-table-column prop="ip" label="IP地址" width="140" />
      <el-table-column prop="client_name" label="客户端" min-width="180" show-overflow-tooltip>
        <template #default="{ row }">{{ row.client_name || row.client_type_label || '-' }}</template>
      </el-table-column>
      <el-table-column prop="user_agent" label="用户代理" show-overflow-tooltip />
      <el-table-column label="最后活动" width="170">
        <template #default="{ row }">{{ formatRelativeTime(row.last_active || row.created_at) }}</template>
      </el-table-column>
      <el-table-column label="操作" width="100" fixed="right">
        <template #default="{ row }">
          <el-button size="small" text type="danger" @click="onRevokeSession(row.id)">撤销</el-button>
        </template>
      </el-table-column>
    </el-table>
  </el-card>
</template>

<style scoped lang="scss">
@use './config-card-shared.scss' as *;

.session-limit-section {
  margin-bottom: 4px;
}

.session-limit-control {
  display: flex;
  align-items: center;
  gap: 10px;
}

.session-limit-hint {
  font-size: 12px;
  color: var(--el-text-color-secondary);
  line-height: 1.6;
  margin-top: 4px;
}

.card-header-buttons {
  padding: 2px;
  // 按钮组的灰底槽 → control 档（与槽内按钮同档，圆角一致才不会露出内外错位的角）
  border-radius: var(--dd-radius-control);
  background: color-mix(in srgb, var(--el-fill-color-light) 84%, transparent);
  display: flex;
  gap: 8px;

  :deep(.el-button + .el-button) {
    margin-left: 0;
  }
}

.session-card__actions > * {
  flex: 1 1 auto;
}

@media (max-width: 768px) {
  .card-header-buttons {
    width: 100%;
    flex-wrap: wrap;
  }
}
</style>
