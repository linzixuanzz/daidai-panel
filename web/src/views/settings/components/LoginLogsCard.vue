<script setup lang="ts">
import { Delete, Document, Refresh } from '@element-plus/icons-vue'
import { useResponsive } from '@/composables/useResponsive'
import { formatDateTime } from '@/utils/datetime'

const loginLogsPage = defineModel<number>('loginLogsPage', { required: true })
const { isMobile } = useResponsive()

defineProps<{
  loginLogs: any[]
  loginLogsLoading: boolean
  loginLogsTotal: number
  onLoadLoginLogs: () => void | Promise<void>
  onClearLoginLogs: () => void | Promise<void>
}>()
</script>

<template>
  <el-card shadow="never">
    <template #header>
      <div class="card-header">
        <span class="card-title"><el-icon><Document /></el-icon> 登录日志</span>
        <div class="card-header-buttons">
          <el-button @click="onLoadLoginLogs"><el-icon><Refresh /></el-icon>刷新</el-button>
          <el-button @click="onClearLoginLogs"><el-icon><Delete /></el-icon>清理旧日志</el-button>
        </div>
      </div>
    </template>
    <div v-if="isMobile" v-loading="loginLogsLoading" class="dd-mobile-list">
      <div
        v-for="row in loginLogs"
        :key="row.id"
        class="dd-mobile-card"
      >
        <div class="dd-mobile-card__header">
          <div class="dd-mobile-card__title-wrap">
            <span class="dd-mobile-card__title">{{ row.username }}</span>
            <span class="dd-mobile-card__subtitle">{{ row.ip }}</span>
          </div>
          <el-tag size="small" :type="row.status === 0 ? 'success' : 'danger'">
            {{ row.status === 0 ? '成功' : '失败' }}
          </el-tag>
        </div>
        <div class="dd-mobile-card__body">
          <div class="dd-mobile-card__grid">
            <div class="dd-mobile-card__field dd-mobile-card__field--full">
              <span class="dd-mobile-card__label">客户端</span>
              <span class="dd-mobile-card__value">{{ row.client_name || row.client_type_label || '-' }}</span>
            </div>
            <div class="dd-mobile-card__field">
              <span class="dd-mobile-card__label">登录方式</span>
              <span class="dd-mobile-card__value">{{ row.method }}</span>
            </div>
            <div class="dd-mobile-card__field">
              <span class="dd-mobile-card__label">时间</span>
              <span class="dd-mobile-card__value">{{ formatDateTime(row.created_at) }}</span>
            </div>
            <div class="dd-mobile-card__field dd-mobile-card__field--full">
              <span class="dd-mobile-card__label">原因</span>
              <span class="dd-mobile-card__value">{{ row.message || '-' }}</span>
            </div>
          </div>
        </div>
      </div>
      <el-empty v-if="!loginLogsLoading && loginLogs.length === 0" description="暂无数据" />
    </div>

    <el-table v-else :data="loginLogs" v-loading="loginLogsLoading" stripe empty-text="暂无数据">
      <el-table-column prop="username" label="用户" width="100" />
      <el-table-column label="状态" width="80">
        <template #default="{ row }">
          <el-tag size="small" :type="row.status === 0 ? 'success' : 'danger'">
            {{ row.status === 0 ? '成功' : '失败' }}
          </el-tag>
        </template>
      </el-table-column>
      <el-table-column prop="ip" label="IP地址" width="140" />
      <el-table-column label="客户端" min-width="180" show-overflow-tooltip>
        <template #default="{ row }">{{ row.client_name || row.client_type_label || '-' }}</template>
      </el-table-column>
      <el-table-column prop="method" label="登录方式" width="100" />
      <el-table-column prop="message" label="原因" show-overflow-tooltip />
      <el-table-column prop="created_at" label="时间" width="170">
        <template #default="{ row }">{{ formatDateTime(row.created_at) }}</template>
      </el-table-column>
    </el-table>
    <div class="pagination-container" v-if="loginLogsTotal > 15">
      <el-pagination
        v-model:current-page="loginLogsPage"
        :total="loginLogsTotal"
        :page-size="15"
        layout="prev, pager, next"
        @current-change="onLoadLoginLogs"
      />
    </div>
  </el-card>
</template>

<style scoped lang="scss">
@use './config-card-shared.scss' as *;

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

.pagination-container {
  display: flex;
  justify-content: center;
  margin-top: 20px;
}

@media (max-width: 768px) {
  .card-header-buttons {
    width: 100%;
    flex-wrap: wrap;
  }
}
</style>
