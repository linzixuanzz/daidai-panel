<script setup lang="ts">
// 设置 → 账号安全 → 审计日志（只给管理员）。
// 组件自己加载、自己闭合（同 SecurityPasskeyCard），不往 useSettingsSecurity.ts 里塞。
import { onMounted, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { Refresh, Tickets } from '@element-plus/icons-vue'
import { securityApi } from '@/api/security'
import { useResponsive } from '@/composables/useResponsive'
import { formatDateTime } from '@/utils/datetime'

const { isMobile } = useResponsive()

// 后端落库的是稳定的英文标识（server/service/login_security.go 的 Audit* 常量），这里翻成中文。
// 后端新增标识时要同步加在这里；没收录的标识原样显示，不至于整行空白。
const ACTION_LABELS: Record<string, string> = {
  'password.change': '修改密码',
  'username.change': '修改用户名',
  '2fa.enable': '开启两步验证',
  '2fa.disable': '关闭两步验证',
  'passkey.add': '添加通行密钥',
  'passkey.delete': '删除通行密钥',
  'user.create': '新建用户',
  'user.update': '修改用户',
  'user.delete': '删除用户',
  'user.password_reset': '重置用户密码',
  'backup.create': '创建备份',
  'backup.upload': '上传备份',
  'backup.download': '下载备份',
  'backup.restore': '恢复备份',
  'backup.delete': '删除备份',
  'console.run': '执行系统命令',
  'config.update': '修改系统配置',
  'config.delete': '删除系统配置',
  'config_script.update': '修改 config.sh',
  'open_app.create': '新建开放 API 应用',
  'open_app.delete': '删除开放 API 应用',
  'open_app.reset_secret': '重置应用密钥',
  'open_app.scope_change': '修改应用权限',
  'ip_whitelist.add': '添加 IP 白名单',
  'ip_whitelist.remove': '删除 IP 白名单',
  'login_logs.clear': '清空登录日志',
  'ssh_key.create': '添加 SSH 密钥',
  'ssh_key.delete': '删除 SSH 密钥',
  'system.update': '更新面板',
  'system.restart': '重启面板',
  'system.stop': '停止面板',
  'process.kill': '结束后台进程'
}
const actionOptions = Object.entries(ACTION_LABELS).map(([value, label]) => ({ value, label }))

function actionLabel(action: string) {
  return ACTION_LABELS[action] || action
}

// 删除、清空、恢复这类会销毁或覆盖数据的操作标红，扫一眼就能看到
function actionTagType(action: string) {
  if (/\.(delete|remove|clear|restore|reset_secret|password_reset|disable|kill)$/.test(action) || action === 'console.run') return 'danger'
  if (action.startsWith('system.')) return 'warning'
  return 'info'
}

const PAGE_SIZE = 15
const logs = ref<any[]>([])
const loading = ref(false)
const total = ref(0)
const page = ref(1)
const actionFilter = ref('')

async function loadAuditLogs() {
  loading.value = true
  try {
    const res = await securityApi.auditLogs({
      page: page.value,
      page_size: PAGE_SIZE,
      // 不筛选时不带 action 这个参数
      action: actionFilter.value || undefined
    })
    logs.value = Array.isArray(res.data) ? res.data : []
    total.value = res.total || 0
  } catch (err: any) {
    ElMessage.error(err?.response?.data?.error || '加载审计日志失败')
  } finally {
    loading.value = false
  }
}

// 换筛选条件要回到第一页，否则停在第 5 页可能一条都看不到
function handleFilterChange() {
  page.value = 1
  void loadAuditLogs()
}

onMounted(() => {
  void loadAuditLogs()
})

defineExpose({ reload: loadAuditLogs })
</script>

<template>
  <el-card shadow="never">
    <template #header>
      <div class="card-header">
        <span class="card-title"><el-icon><Tickets /></el-icon> 审计日志</span>
        <div class="card-header-buttons">
          <el-select
            v-model="actionFilter"
            class="action-filter"
            placeholder="全部操作"
            clearable
            filterable
            @change="handleFilterChange"
          >
            <el-option v-for="item in actionOptions" :key="item.value" :label="item.label" :value="item.value" />
          </el-select>
          <el-button @click="loadAuditLogs"><el-icon><Refresh /></el-icon>刷新</el-button>
        </div>
      </div>
    </template>
    <div v-if="isMobile" class="dd-mobile-list" v-loading="loading">
      <div
        v-for="row in logs"
        :key="row.id"
        class="dd-mobile-card"
      >
        <div class="dd-mobile-card__header">
          <div class="dd-mobile-card__title-wrap">
            <span class="dd-mobile-card__title">{{ row.username || '-' }}</span>
            <span class="dd-mobile-card__subtitle">{{ row.ip || '-' }}</span>
          </div>
          <el-tag size="small" :type="actionTagType(row.action)">{{ actionLabel(row.action) }}</el-tag>
        </div>
        <div class="dd-mobile-card__body">
          <div class="dd-mobile-card__grid">
            <div class="dd-mobile-card__field dd-mobile-card__field--full">
              <span class="dd-mobile-card__label">详情</span>
              <span class="dd-mobile-card__value">{{ row.detail || '-' }}</span>
            </div>
            <div class="dd-mobile-card__field dd-mobile-card__field--full">
              <span class="dd-mobile-card__label">时间</span>
              <span class="dd-mobile-card__value">{{ formatDateTime(row.created_at) }}</span>
            </div>
          </div>
        </div>
      </div>
      <el-empty v-if="!loading && logs.length === 0" description="暂无数据" />
    </div>

    <el-table v-else :data="logs" v-loading="loading" stripe empty-text="暂无数据">
      <el-table-column prop="created_at" label="时间" width="170">
        <template #default="{ row }">{{ formatDateTime(row.created_at) }}</template>
      </el-table-column>
      <el-table-column label="操作者" min-width="140" show-overflow-tooltip>
        <template #default="{ row }">{{ row.username || '-' }}</template>
      </el-table-column>
      <el-table-column label="操作" width="150">
        <template #default="{ row }">
          <el-tag size="small" :type="actionTagType(row.action)">{{ actionLabel(row.action) }}</el-tag>
        </template>
      </el-table-column>
      <el-table-column prop="detail" label="详情" min-width="220" show-overflow-tooltip />
      <el-table-column prop="ip" label="IP地址" width="140" />
    </el-table>
    <div class="pagination-container" v-if="total > PAGE_SIZE">
      <el-pagination
        v-model:current-page="page"
        :total="total"
        :page-size="PAGE_SIZE"
        layout="prev, pager, next"
        @current-change="loadAuditLogs"
      />
    </div>
  </el-card>
</template>

<style scoped lang="scss">
@use './config-card-shared.scss' as *;

.card-header-buttons {
  padding: 2px;
  // 与登录日志卡片同一套按钮组灰底槽
  border-radius: var(--dd-radius-control);
  background: color-mix(in srgb, var(--el-fill-color-light) 84%, transparent);
  display: flex;
  gap: 8px;
}

.action-filter {
  width: 180px;
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

  .action-filter {
    flex: 1;
    min-width: 0;
  }
}
</style>
