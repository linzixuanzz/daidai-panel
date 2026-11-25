<script setup lang="ts">
import { Connection, Plus, Refresh } from '@element-plus/icons-vue'
import { useResponsive } from '@/composables/useResponsive'
import { formatDateTime } from '@/utils/datetime'

const showAddIPDialog = defineModel<boolean>('showAddIPDialog', { required: true })
const newIP = defineModel<string>('newIP', { required: true })
const newIPRemarks = defineModel<string>('newIPRemarks', { required: true })

withDefaults(defineProps<{
  ipWhitelist: any[]
  ipWhitelistLoading: boolean
  // 添加 IP 的在途锁由 useSettingsSecurity 持有，本卡片只负责把按钮锁住；默认 false
  ipAdding?: boolean
  onLoadIPWhitelist: () => void | Promise<void>
  onAddIP: () => void | Promise<void>
  onRemoveIP: (id: number) => void | Promise<void>
}>(), {
  ipAdding: false
})

const { isMobile, dialogFullscreen } = useResponsive()
</script>

<template>
  <el-card shadow="never">
    <template #header>
      <div class="card-header">
        <span class="card-title"><el-icon><Connection /></el-icon> IP白名单</span>
        <div class="card-header-buttons">
          <el-button @click="onLoadIPWhitelist"><el-icon><Refresh /></el-icon>刷新</el-button>
          <el-button type="primary" @click="showAddIPDialog = true">
            <el-icon><Plus /></el-icon>添加IP/网段
          </el-button>
        </div>
      </div>
    </template>
    <p class="card-tip">
      支持单个 IP、CIDR 网段，以及更易输入的 IPv4 通配格式，例如 <code>203.0.113.7</code>、<code>203.0.113.0/24</code>、<code>203.0.113.*</code>。
    </p>
    <div v-if="isMobile" v-loading="ipWhitelistLoading" class="dd-mobile-list">
      <div
        v-for="row in ipWhitelist"
        :key="row.id"
        class="dd-mobile-card"
      >
        <div class="dd-mobile-card__header">
          <div class="dd-mobile-card__title-wrap">
            <span class="dd-mobile-card__title">{{ row.ip }}</span>
            <span class="dd-mobile-card__subtitle">{{ row.remarks || '未填写描述' }}</span>
          </div>
          <el-tag type="success" size="small">启用</el-tag>
        </div>
        <div class="dd-mobile-card__body">
          <div class="dd-mobile-card__grid">
            <div class="dd-mobile-card__field">
              <span class="dd-mobile-card__label">创建时间</span>
              <span class="dd-mobile-card__value">{{ formatDateTime(row.created_at) }}</span>
            </div>
          </div>
          <div class="dd-mobile-card__actions ip-card__actions">
            <el-button size="small" type="danger" plain @click="onRemoveIP(row.id)">移除</el-button>
          </div>
        </div>
      </div>
      <el-empty v-if="!ipWhitelistLoading && ipWhitelist.length === 0" description="暂无数据" />
    </div>

    <el-table v-else :data="ipWhitelist" v-loading="ipWhitelistLoading" stripe empty-text="暂无数据">
      <el-table-column prop="ip" label="IP / 网段" min-width="220" />
      <el-table-column prop="remarks" label="描述" min-width="200" />
      <el-table-column label="状态" width="80">
        <template #default>
          <el-tag type="success" size="small">启用</el-tag>
        </template>
      </el-table-column>
      <el-table-column prop="created_at" label="创建时间" width="170">
        <template #default="{ row }">{{ formatDateTime(row.created_at) }}</template>
      </el-table-column>
      <el-table-column label="操作" width="100" fixed="right">
        <template #default="{ row }">
          <el-button size="small" text type="danger" @click="onRemoveIP(row.id)">移除</el-button>
        </template>
      </el-table-column>
    </el-table>
  </el-card>

  <el-dialog v-model="showAddIPDialog" title="添加 IP 白名单" width="460px" :fullscreen="dialogFullscreen">
    <el-form :label-width="dialogFullscreen ? 'auto' : '80px'" :label-position="dialogFullscreen ? 'top' : 'right'">
      <el-form-item label="IP / 网段">
        <el-input v-model="newIP" placeholder="如: 203.0.113.7 / 203.0.113.0/24 / 203.0.113.*" />
        <div class="field-hint">
          适合固定公网填单个 IP；动态公网但前缀稳定时，可填网段或 <code>*</code> 通配格式。
        </div>
      </el-form-item>
      <el-form-item label="备注">
        <el-input v-model="newIPRemarks" placeholder="备注说明 (可选)" />
      </el-form-item>
    </el-form>
    <template #footer>
      <el-button @click="showAddIPDialog = false">取消</el-button>
      <el-button type="primary" :loading="ipAdding" :disabled="ipAdding" @click="onAddIP">添加</el-button>
    </template>
  </el-dialog>
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

.card-tip {
  margin: 0 0 12px;
  font-size: 13px;
  line-height: 1.6;
  color: var(--el-text-color-secondary);
}

.field-hint {
  margin-top: 6px;
  font-size: 12px;
  line-height: 1.5;
  color: var(--el-text-color-secondary);
}

.card-tip code,
.field-hint code {
  font-family: var(--dd-font-mono);
  font-size: 0.92em;
  padding: 1px 5px;
  border-radius: var(--dd-radius-control);
  background: var(--el-fill-color-light);
  color: var(--el-text-color-primary);
}

.ip-card__actions > * {
  flex: 1 1 auto;
}

@media (max-width: 768px) {
  .card-header-buttons {
    width: 100%;
    flex-wrap: wrap;
  }
}
</style>
