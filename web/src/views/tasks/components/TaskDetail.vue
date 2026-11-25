<script setup lang="ts">
import { computed } from 'vue'
// 全局注册的图标里没有 WarningFilled，按仓库既有做法局部引入
import { WarningFilled } from '@element-plus/icons-vue'
import { getDisplayTaskLabels, isTaskSwitchOn } from '../taskLabels'
import { useResponsive } from '@/composables/useResponsive'
import { formatDuration } from '@/utils/duration'
import { formatDateTime } from '@/utils/datetime'
import TaskCronList from './TaskCronList.vue'
import TaskHealthPanel from '@/components/TaskHealthPanel.vue'

const props = defineProps<{
  visible: boolean
  task: any
}>()

const emit = defineEmits<{
  'update:visible': [value: boolean]
}>()
const { dialogFullscreen, isMobile } = useResponsive()

// 与列表页（index.vue 的 getStatusType）同一套配色（issue #133）：禁用灰、排队橙、运行中绿、空闲蓝。
// 排队 / 运行中先判运行态；其余两档（已启用 / 已禁用）按开关位判，口径同列表页的 isTaskSwitchOn。
const statusText = computed(() => {
  if (props.task?.status === 0.5) return '排队中'
  if (props.task?.status === 2) return '运行中'
  return isTaskSwitchOn(props.task) ? '已启用' : '已禁用'
})

const statusType = computed(() => {
  if (props.task?.status === 0.5) return 'warning'
  if (props.task?.status === 2) return 'success'
  return isTaskSwitchOn(props.task) ? 'primary' : 'info'
})

// 禁用任务被手动运行时，上面那枚标签只能显示运行态（排队中 / 运行中），看不出它其实仍是禁用的。
// 详情弹窗地方够，补一枚灰标签把开关位说出来：跑完仍保持禁用、不会被定时触发。
// 老后端不下发 enabled 时 isTaskSwitchOn 对运行中恒为真，这枚标签不出现，与改动前一致。
const runningWhileDisabled = computed(() => {
  const status = props.task?.status
  return (status === 0.5 || status === 2) && !isTaskSwitchOn(props.task)
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

// 可用环境变量（#168）：「全部」或「指定（分组：a、b；变量：X、Y）」。老面板不下发 env_scope 时按全部显示
const envScopeText = computed(() => {
  if (props.task?.env_scope !== 'selected') return '全部'
  const groups: string[] = Array.isArray(props.task?.env_groups) ? props.task.env_groups : []
  const names: string[] = Array.isArray(props.task?.env_names) ? props.task.env_names : []
  const parts: string[] = []
  if (groups.length > 0) parts.push(`分组：${groups.join('、')}`)
  if (names.length > 0) parts.push(`变量：${names.join('、')}`)
  // 两个列表都空 = 一个用户变量都不注入，要直说
  return parts.length > 0 ? `指定（${parts.join('；')}）` : '指定（未选任何分组或变量，不注入用户变量）'
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
          <el-icon v-if="task.is_pinned" color="var(--dd-warn)"><Star /></el-icon>
          <span>{{ task.name }}</span>
        </div>
      </el-descriptions-item>
      <el-descriptions-item label="任务ID">{{ task.id }}</el-descriptions-item>
      <el-descriptions-item label="状态">
        <div class="status-cell">
          <!-- 空闲的 primary 浅色标签挂 dd-tag--idle（global.scss，契约 C6），与列表页同一个修饰类 -->
          <el-tag :type="statusType" size="small" :class="{ 'dd-tag--idle': statusType === 'primary' }">{{ statusText }}</el-tag>
          <el-tag
            v-if="runningWhileDisabled"
            type="info"
            size="small"
            title="任务仍是禁用状态：本次运行结束后保持禁用，不会被定时触发"
          >
            已禁用
          </el-tag>
        </div>
      </el-descriptions-item>
      <el-descriptions-item label="定时类型">
        {{ taskTypeText }}
      </el-descriptions-item>
      <el-descriptions-item label="定时规则">
        <TaskCronList
          v-if="task.task_type === 'cron'"
          :expressions="cronExpressions"
        />
        <span v-else style="color: var(--dd-text-3)">不使用 Cron</span>
      </el-descriptions-item>
      <el-descriptions-item label="执行命令" :span="2">
        <code style="word-break: break-all">{{ task.command }}</code>
      </el-descriptions-item>
      <el-descriptions-item label="随机延迟">
        <span v-if="task.random_delay_seconds == null" style="color: var(--dd-text-2)">继承系统设置</span>
        <span v-else-if="task.random_delay_seconds === 0">不随机延迟</span>
        <span v-else>最多 {{ task.random_delay_seconds }} 秒</span>
      </el-descriptions-item>
      <el-descriptions-item label="标签" :span="2">
        <!--
          key 必须带下标：display_labels 允许出现同名的两条（例如「自定义标签名 = 订阅名」，
          后端按类别分别下发），只用标签文字当 key 会触发 Vue 的 Duplicate keys 警告。
          这里只改 key，本弹窗「显示完整标签、刻意不跟随列表页显示设置」的语义保持不变。
        -->
        <div v-if="displayLabels.length > 0" class="detail-labels">
          <el-tag
            v-for="(label, index) in displayLabels"
            :key="`${index}-${label}`"
            size="small"
            effect="plain"
          >
            {{ label }}
          </el-tag>
        </div>
        <span v-else style="color: var(--dd-text-3)">无</span>
      </el-descriptions-item>
      <el-descriptions-item label="上次运行状态">
        <el-tag v-if="task.last_run_status === null" type="info" size="small">未运行</el-tag>
        <el-tag v-else-if="task.last_run_status === 0" type="success" size="small">成功</el-tag>
        <!-- 已终止：比「未运行」更深的灰（issue #133），修饰类与列表页同一个（契约 C6） -->
        <el-tag v-else-if="task.last_run_status === 2" type="info" class="dd-tag--aborted" size="small">已终止</el-tag>
        <el-tag v-else type="danger" size="small">失败</el-tag>
      </el-descriptions-item>
      <el-descriptions-item label="上次运行耗时">
        <span v-if="task.last_running_time != null">{{ formatDuration(task.last_running_time) }}</span>
        <span v-else style="color: var(--dd-text-3)">-</span>
      </el-descriptions-item>
      <el-descriptions-item label="上次运行时间" :span="2">
        {{ formatTime(task.last_run_at) }}
      </el-descriptions-item>
      <el-descriptions-item label="通知渠道" :span="2">
        <span v-if="task.notification_channel_name">
          {{ task.notification_channel_name }}
          <span v-if="task.notification_channel_enabled === false" style="color: var(--dd-err-ink)">（已禁用）</span>
        </span>
        <!-- 未绑定渠道 = 走广播。广播只命中「默认推送」渠道，「绑定推送」渠道收不到。 -->
        <span v-else style="color: var(--dd-text-3)">全部默认推送渠道</span>
      </el-descriptions-item>
      <el-descriptions-item label="环境变量" :span="2">
        <span style="word-break: break-all">{{ envScopeText }}</span>
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
        <span v-else style="color: var(--dd-text-3)">无</span>
      </el-descriptions-item>
    </el-descriptions>
    <!-- 健康画像（v4.0 F5）：桌面端在任务表的展开行里看，移动端没有展开行，放在详情弹窗底部。
         v-if 带上 visible：关掉弹窗就卸载，不在后台留着取数；换任务时组件按 taskId 自己重新取。 -->
    <section v-if="visible && isMobile && task?.id" class="task-detail-health">
      <h4 class="task-detail-health__title">健康画像</h4>
      <TaskHealthPanel :task-id="task.id" />
    </section>
    <!-- 移动端全屏时关闭入口放右下角（v3.3.1，issue #143 F2）：右上角 × 离拇指最远，iOS 上还曾被顶栏整块盖住。
         有 footer 后 global.scss 会在 ≤768 隐藏 EP 自带的 ×。桌面不渲染 footer，保持原样。
         关闭走 handleClose 通知父组件；v-model 置 false 时 EP 仍会触发 @close，再回传一次 false 无害。 -->
    <template v-if="dialogFullscreen" #footer>
      <el-button @click="handleClose">关闭</el-button>
    </template>
  </el-dialog>
</template>

<style scoped>
.task-detail-health {
  margin-top: 16px;
}

.task-detail-health__title {
  margin: 0 0 10px;
  font-size: var(--dd-card-title-size, 14px);
  font-weight: var(--dd-card-title-weight, 600);
  color: var(--dd-text);
}

/* 标签容器：flex 换行 + 统一 gap，避免多枚标签折行时行间黏连 */
.detail-labels {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
}

/* 状态标签 + 可能出现的「已禁用」开关位标签并排一行（本块是纯 CSS，注释不能用 //） */
.status-cell {
  display: inline-flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 6px;
}

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
  color: var(--dd-warn-ink);
  font-size: 12px;
  line-height: 1.6;
}

.skip-reason {
  display: flex;
  align-items: flex-start;
  gap: 6px;
  color: var(--dd-warn-ink);
  line-height: 1.6;
}
</style>
