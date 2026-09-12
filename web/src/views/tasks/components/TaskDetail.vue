<script setup lang="ts">
import { computed } from 'vue'
// 全局注册的图标里没有 WarningFilled，按仓库既有做法局部引入
import { WarningFilled } from '@element-plus/icons-vue'
import { getDisplayTaskLabels } from '../taskLabels'
import { useResponsive } from '@/composables/useResponsive'
import { formatDuration } from '@/utils/duration'
import { formatDateTime } from '@/utils/datetime'
import TaskCronList from './TaskCronList.vue'

const props = defineProps<{
  visible: boolean
  task: any
}>()

const emit = defineEmits<{
  'update:visible': [value: boolean]
}>()
const { dialogFullscreen } = useResponsive()

const statusText = computed(() => {
  if (props.task?.status === 0) return '已禁用'
  if (props.task?.status === 0.5) return '排队中'
  if (props.task?.status === 2) return '运行中'
  return '已启用'
})

const statusType = computed(() => {
  if (props.task?.status === 0) return 'info'
  if (props.task?.status === 0.5) return 'warning'
  if (props.task?.status === 2) return 'warning'
  return 'success'
})

const displayLabels = computed(() => {
  if (Array.isArray(props.task?.display_labels) && props.task.display_labels.length > 0) {
    return props.task.display_labels
  }
  return getDisplayTaskLabels(props.task?.labels || [])
})

// 薄封装转调 utils/datetime，模板里的多处调用点不用改。
// 空值/无效值由 formatDateTime 统一兜底成 '-'，本地不再自己判空。
const formatTime = (t: string) => formatDateTime(t)

const taskTypeText = computed(() => {
  if (props.task?.task_type === 'manual') return '手动运行'
  if (props.task?.task_type === 'startup') return '开机运行'
  return '常规定时'
})

const cronExpressions = computed(() => {
  if (Array.isArray(props.task?.cron_expressions) && props.task.cron_expressions.length > 0) {
    return props.task.cron_expressions
  }
  return String(props.task?.cron_expression || '')
    .split(/\r?\n/)
    .map(item => item.trim())
    .filter(Boolean)
})

function handleClose() {
  emit('update:visible', false)
}
</script>

<template>
  <el-dialog
    :model-value="visible"
    title="任务详情"
    width="700px"
    :fullscreen="dialogFullscreen"
    :lock-scroll="false"
    @close="handleClose"
  >
    <el-descriptions v-if="task" :column="dialogFullscreen ? 1 : 2" border>
      <el-descriptions-item label="任务名称" :span="2">
        <div style="display: flex; align-items: center; gap: 8px">
          <el-icon v-if="task.is_pinned" color="var(--el-color-warning)"><Star /></el-icon>
          <span>{{ task.name }}</span>
        </div>
      </el-descriptions-item>
      <el-descriptions-item label="任务ID">{{ task.id }}</el-descriptions-item>
      <el-descriptions-item label="状态">
        <el-tag :type="statusType" size="small">{{ statusText }}</el-tag>
      </el-descriptions-item>
      <el-descriptions-item label="定时类型">
        {{ taskTypeText }}
      </el-descriptions-item>
      <el-descriptions-item label="定时规则">
        <TaskCronList
          v-if="task.task_type === 'cron'"
          :expressions="cronExpressions"
        />
        <span v-else style="color: var(--el-text-color-placeholder)">不使用 Cron</span>
      </el-descriptions-item>
      <el-descriptions-item label="执行命令" :span="2">
        <code style="word-break: break-all">{{ task.command }}</code>
      </el-descriptions-item>
      <el-descriptions-item label="随机延迟">
        <span v-if="task.random_delay_seconds == null" style="color: var(--el-text-color-secondary)">继承系统设置</span>
        <span v-else-if="task.random_delay_seconds === 0">不随机延迟</span>
        <span v-else>最多 {{ task.random_delay_seconds }} 秒</span>
      </el-descriptions-item>
      <el-descriptions-item label="标签" :span="2">
        <!--
          key 必须带下标：display_labels 允许出现同名的两条（例如「自定义标签名 = 订阅名」，
          后端按类别分别下发），只用标签文字当 key 会触发 Vue 的 Duplicate keys 警告。
          这里只改 key，本弹窗「显示完整标签、刻意不跟随列表页显示设置」的语义保持不变。
        -->
        <el-tag
          v-for="(label, index) in displayLabels"
          :key="`${index}-${label}`"
          size="small"
          effect="plain"
          style="margin-right: 6px"
        >
          {{ label }}
        </el-tag>
        <span v-if="displayLabels.length === 0" style="color: var(--el-text-color-placeholder)">无</span>
      </el-descriptions-item>
      <el-descriptions-item label="上次运行状态">
        <el-tag v-if="task.last_run_status === null" type="info" size="small">未运行</el-tag>
        <el-tag v-else-if="task.last_run_status === 0" type="success" size="small">成功</el-tag>
        <el-tag v-else-if="task.last_run_status === 2" type="warning" size="small">已终止</el-tag>
        <el-tag v-else type="danger" size="small">失败</el-tag>
      </el-descriptions-item>
      <el-descriptions-item label="上次运行耗时">
        <span v-if="task.last_running_time != null">{{ formatDuration(task.last_running_time) }}</span>
        <span v-else style="color: var(--el-text-color-placeholder)">-</span>
      </el-descriptions-item>
      <el-descriptions-item label="上次运行时间" :span="2">
        {{ formatTime(task.last_run_at) }}
      </el-descriptions-item>
      <el-descriptions-item label="通知渠道" :span="2">
        <span v-if="task.notification_channel_name">
          {{ task.notification_channel_name }}
          <span v-if="task.notification_channel_enabled === false" style="color: var(--el-color-danger)">（已禁用）</span>
        </span>
        <!-- 未绑定渠道 = 走广播。广播只命中「默认推送」渠道，「绑定推送」渠道收不到。 -->
        <span v-else style="color: var(--el-text-color-placeholder)">全部默认推送渠道</span>
      </el-descriptions-item>
      <el-descriptions-item label="下次运行时间" :span="2">
        <div class="next-run-row">
          <span>{{ formatTime(task.next_run_at) }}</span>
          <!-- schedule_hint 由后端下发，正常时为空串。它专治「任务显示已启用、下次运行也有时间，
               实际却永远不会被调度器触发」这种静默状态（issue #115）。
               列表那边地方只够放一个带 tooltip 的图标，详情弹窗地方够，直接把整句话摊开写。 -->
          <span v-if="task.schedule_hint" class="schedule-hint">
            <el-icon><WarningFilled /></el-icon>
            {{ task.schedule_hint }}
          </span>
        </div>
      </el-descriptions-item>
      <!-- 「到点了但这次没执行」的原因。它记在任务行上、不建日志记录（跳过意味着压根没执行，
           建成日志会混进「已终止」和耗时统计，还会顶掉「最近一次日志」），所以只能在这里展示。 -->
      <el-descriptions-item v-if="task.last_skip_reason" label="最近一次被跳过" :span="2">
        <div class="skip-reason">
          <el-icon><WarningFilled /></el-icon>
          <span>{{ formatTime(task.last_skip_at) }}　{{ task.last_skip_reason }}</span>
        </div>
      </el-descriptions-item>
      <el-descriptions-item label="创建时间">{{ formatTime(task.created_at) }}</el-descriptions-item>
      <el-descriptions-item label="更新时间">{{ formatTime(task.updated_at) }}</el-descriptions-item>
      <el-descriptions-item label="备注" :span="2">
        <span v-if="task.remark">{{ task.remark }}</span>
        <span v-else style="color: var(--el-text-color-placeholder)">无</span>
      </el-descriptions-item>
    </el-descriptions>
  </el-dialog>
</template>

<style scoped>
.next-run-row {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}

.schedule-hint {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  color: var(--el-color-warning);
  font-size: 12px;
  line-height: 1.6;
}

.skip-reason {
  display: flex;
  align-items: flex-start;
  gap: 6px;
  color: var(--el-color-warning);
  line-height: 1.6;
}
</style>
