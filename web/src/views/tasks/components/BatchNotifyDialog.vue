<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { ElMessage } from 'element-plus'
import { taskApi } from '@/api/task'
import { useResponsive } from '@/composables/useResponsive'

/**
 * 批量设置任务的通知开关（issue #149：「一键设置全部脚本失败了通知，现在要一个一个设置」）。
 * 骨架照抄同目录 BatchAddLabelDialog.vue，移动端整屏同样靠 :fullscreen="dialogFullscreen"。
 *
 * 【为什么要有「全部任务」这一档】网页任务列表的勾选只作用于当前页（每页最多 100 条），
 * 任务一多就得一页页勾。选「全部任务」时只发 all=true，由服务端改全部任务，不受当前筛选、视图影响。
 *
 * 【三档而不是开关】每一项都有「不修改」：只有不是「不修改」的项才会放进请求体，
 * 服务端只改传了的开关，所以「只开失败通知、不碰成功 / 终止通知」这类操作不会误伤。
 * 通知渠道不在这里改（弹窗里有一行说明），各任务原来绑定的渠道照旧生效。
 */

type Scope = 'selected' | 'all'
type NotifyChoice = 'on' | 'off' | 'keep'

const props = defineProps<{
  visible: boolean
  taskIds: number[]
}>()

const emit = defineEmits<{
  'update:visible': [value: boolean]
  success: []
}>()

const { dialogFullscreen } = useResponsive()
const scope = ref<Scope>('all')
const onFailure = ref<NotifyChoice>('on')
const onSuccess = ref<NotifyChoice>('keep')
const onAbort = ref<NotifyChoice>('keep')
const submitting = ref(false)

// 三项全是「不修改」时这次提交什么都不会改，直接把确定置灰，不发请求
const hasChange = computed(() => [onFailure.value, onSuccess.value, onAbort.value].some(choice => choice !== 'keep'))

// 每次打开都从默认值开始：失败时通知默认「开启」（issue #149 的原始诉求），另两项默认「不修改」。
// 有勾选时默认只改选中的；没勾选时「选中的」那一档不可选，只能改全部任务。
watch(
  () => props.visible,
  (visible) => {
    if (visible) {
      scope.value = props.taskIds.length > 0 ? 'selected' : 'all'
      onFailure.value = 'on'
      onSuccess.value = 'keep'
      onAbort.value = 'keep'
    }
  }
)

function close() {
  emit('update:visible', false)
}

async function handleConfirm() {
  const payload: Parameters<typeof taskApi.batchSetNotify>[0] = scope.value === 'all'
    ? { all: true }
    : { task_ids: [...props.taskIds] }
  // 「不修改」的项不放进请求体：服务端用指针区分「没传」和「传了 false」，没传的开关保持各任务原值
  if (onFailure.value !== 'keep') payload.notify_on_failure = onFailure.value === 'on'
  if (onSuccess.value !== 'keep') payload.notify_on_success = onSuccess.value === 'on'
  if (onAbort.value !== 'keep') payload.notify_on_abort = onAbort.value === 'on'
  submitting.value = true
  try {
    const res = await taskApi.batchSetNotify(payload)
    ElMessage.success(res?.message || `已更新 ${res?.success_count ?? 0} 个任务的通知设置`)
    emit('success')
    close()
  } catch (err: any) {
    ElMessage.error(err?.response?.data?.error || '批量设置通知失败')
  } finally {
    submitting.value = false
  }
}
</script>

<template>
  <el-dialog
    :model-value="visible"
    title="批量设置通知"
    width="520px"
    :fullscreen="dialogFullscreen"
    :close-on-click-modal="false"
    destroy-on-close
    @update:model-value="emit('update:visible', $event)"
  >
    <el-form :label-width="dialogFullscreen ? 'auto' : '100px'" :label-position="dialogFullscreen ? 'top' : 'right'">
      <el-form-item label="作用范围">
        <el-radio-group v-model="scope" class="batch-notify__scope">
          <el-radio value="selected" :disabled="taskIds.length === 0">选中的 {{ taskIds.length }} 个任务</el-radio>
          <el-radio value="all">全部任务（不受当前筛选影响）</el-radio>
        </el-radio-group>
      </el-form-item>
      <el-form-item label="失败时通知">
        <el-radio-group v-model="onFailure">
          <el-radio-button value="on">开启</el-radio-button>
          <el-radio-button value="off">关闭</el-radio-button>
          <el-radio-button value="keep">不修改</el-radio-button>
        </el-radio-group>
      </el-form-item>
      <el-form-item label="成功时通知">
        <el-radio-group v-model="onSuccess">
          <el-radio-button value="on">开启</el-radio-button>
          <el-radio-button value="off">关闭</el-radio-button>
          <el-radio-button value="keep">不修改</el-radio-button>
        </el-radio-group>
      </el-form-item>
      <el-form-item label="终止时通知">
        <el-radio-group v-model="onAbort">
          <el-radio-button value="on">开启</el-radio-button>
          <el-radio-button value="off">关闭</el-radio-button>
          <el-radio-button value="keep">不修改</el-radio-button>
        </el-radio-group>
      </el-form-item>
    </el-form>
    <!-- 静态提示，不去请求渠道列表：这里只改开关，渠道选择与开关互相独立 -->
    <p class="batch-notify__tip">不修改任务绑定的通知渠道；没绑渠道的任务会发到默认推送渠道。</p>
    <template #footer>
      <el-button @click="close">取消</el-button>
      <el-button type="primary" :loading="submitting" :disabled="!hasChange" @click="handleConfirm">确定</el-button>
    </template>
  </el-dialog>
</template>

<style scoped>
/* 两个范围选项文案都偏长，并排在 520px 弹窗里会被挤到换行、参差不齐，干脆一项一行 */
.batch-notify__scope {
  flex-direction: column;
  align-items: flex-start;
}

.batch-notify__tip {
  margin: 0;
  font-size: 13px;
  line-height: 1.6;
  color: var(--dd-text-2);
}
</style>
