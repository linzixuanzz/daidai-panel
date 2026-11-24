<script setup lang="ts">
import { InfoFilled } from '@element-plus/icons-vue'

defineProps<{
  systemStats: any
}>()
</script>

<template>
  <el-card shadow="never" class="stats-card dd-spot dd-rise" style="--dd-i: 1" v-if="systemStats">
    <template #header>
      <div class="card-header">
        <span class="card-title"><el-icon><InfoFilled /></el-icon> 系统概况</span>
      </div>
    </template>
    <div class="overview-stats-grid">
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
</template>

<style scoped lang="scss">
@use './config-card-shared.scss' as *;

.stats-card {
  // 扁平化：无阴影，层次只靠 1px 描边表达；卡片本体属容器类表面 → surface 档
  border-radius: var(--dd-radius-surface);
  border: 1px solid var(--el-border-color-lighter);
  height: 100%;
}

.overview-stats-grid {
  display: grid;
  grid-template-columns: repeat(3, 1fr);
  text-align: center;
  gap: 10px;
}

.os-item {
  padding: 18px 10px;
  // 卡片内的统计数字块（可 hover 的小块，格子间有 10px 间距、不贴边）→ control 档
  border-radius: var(--dd-radius-control);
  // 扁平化：hover 不再上浮，只换底色
  transition: background 0.18s;
  background: var(--el-fill-color-light);

  &:hover {
    background: color-mix(in srgb, var(--el-color-primary) 6%, var(--el-fill-color-light));
  }
}

.os-label {
  font-size: 13px;
  color: var(--el-text-color-secondary);
  margin-bottom: 8px;
  font-weight: 500;
}

.os-value {
  font-size: 24px;
  font-weight: 700;
  color: var(--el-text-color-primary);
  font-family: 'Inter', var(--dd-font-ui), sans-serif;
  font-variant-numeric: tabular-nums;
  -webkit-font-smoothing: antialiased;
  letter-spacing: -0.01em;
}

// 大号数字是文字，用墨色令牌（原来写死的绿 / 橙在浅色主题下对比度只有 2 左右）
.color-success {
  color: var(--dd-ok-ink);
}

.color-warning {
  color: var(--dd-warn-ink);
}

@media (max-width: 768px) {
  .overview-stats-grid {
    grid-template-columns: repeat(2, 1fr);
  }
}
</style>
