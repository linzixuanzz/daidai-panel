<script setup lang="ts">
import { CopyDocument, InfoFilled } from '@element-plus/icons-vue'
import { ElMessage } from 'element-plus'
import { copyText } from '@/utils/clipboard'

defineProps<{
  systemInfo: any
  formatBytes: (bytes: number) => string
  getUsageClass: (percent: number) => string
}>()

async function handleCopyMachineCode(code: string) {
  if (!code) {
    ElMessage.warning('机器码尚未生成')
    return
  }
  try {
    await copyText(code)
    ElMessage.success('已复制机器码')
  } catch {
    ElMessage.error('复制失败，请手动选中复制')
  }
}
</script>

<template>
  <el-card shadow="never" class="mt-card dd-spot dd-rise" style="--dd-i: 2" v-if="systemInfo">
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
      <div class="si-item si-item-wide">
        <div class="si-label">机器码</div>
        <div class="si-value si-machine-code">
          <span class="machine-code-text">{{ systemInfo.machine_code || '-' }}</span>
          <el-tooltip content="复制机器码" placement="top">
            <el-button
              link
              :disabled="!systemInfo.machine_code"
              @click="handleCopyMachineCode(systemInfo.machine_code)"
            >
              <el-icon><CopyDocument /></el-icon>
            </el-button>
          </el-tooltip>
        </div>
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
</template>

<style scoped lang="scss">
@use './config-card-shared.scss' as *;

.mt-card {
  margin-top: 16px;
  // 扁平化：描边宽走主题令牌；卡片本体 → surface 档
  border-radius: var(--dd-radius-surface);
  border: var(--dd-border-width, 1px) solid var(--dd-border, var(--el-border-color-lighter));
}

.system-info-grid {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 20px;
}

.si-item {
  min-width: 0;
  padding: 8px 0;
}

.si-label {
  font-size: 13px;
  color: var(--el-text-color-secondary);
  margin-bottom: 6px;
}

.si-value {
  font-size: 14px;
  font-weight: 600;
  word-break: break-word;
  color: var(--el-text-color-primary);
}

.si-item-wide {
  grid-column: span 2;
}

.si-machine-code {
  display: flex;
  align-items: center;
  gap: 8px;
}

.machine-code-text {
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  letter-spacing: 0.5px;
  word-break: break-all;
}

.usage-success {
  color: var(--dd-ok-ink);
}

.usage-warning {
  color: var(--dd-warn-ink);
}

.usage-danger {
  color: var(--dd-err-ink);
}

// 双列概览下卡片宽度偏窄，≤1440px 提前切 2 列并收紧 gap，避免内存/磁盘读数被压折行
@media (max-width: 1440px) {
  .system-info-grid {
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: 14px 16px;
  }
}

@media (max-width: 768px) {
  .system-info-grid {
    grid-template-columns: 1fr;
    gap: 12px;
  }

  .si-item-wide {
    grid-column: span 1;
  }
}
</style>
