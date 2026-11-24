<script setup lang="ts">
import { ref, watch } from 'vue'
import { ElMessage } from 'element-plus'
import { taskApi } from '@/api/task'
import { useResponsive } from '@/composables/useResponsive'
import TaskLabelPicker from './TaskLabelPicker.vue'

const props = defineProps<{
  visible: boolean
  taskIds: number[]
}>()

const emit = defineEmits<{
  'update:visible': [value: boolean]
  success: []
}>()

const { dialogFullscreen } = useResponsive()
const labels = ref<string[]>([])
const submitting = ref(false)
// 与任务表单共用的标签选择器（#157）。原来的 el-select allow-create 失焦就清掉没回车的字，
// 粘贴后按回车也建不出标签（没有候选时 EP 的悬停项被复位）。
// pickerKey 每次打开都换一个：弹窗本身 destroy-on-close，关窗动画没走完又打开时 EP 不重建内容，靠它清掉上一次没回车的字
const pickerRef = ref<InstanceType<typeof TaskLabelPicker> | null>(null)
const pickerKey = ref(0)

watch(
  () => props.visible,
  (visible) => {
    if (visible) {
      labels.value = []
      pickerKey.value++
    }
  }
)

function close() {
  emit('update:visible', false)
}

async function handleConfirm() {
  // 先把输入框里没回车的字并进 labels（#157 ①），再做下面的空值校验 —— 顺序反了，「打字不回车点确定」仍会报「请输入至少一个标签」。
  // 有以「分组:」「subscription:」开头的段时它已提示过、原文留在输入框里，这里直接停
  if (pickerRef.value && !pickerRef.value.commitPending()) {
    return
  }
  const cleaned = Array.from(
    new Set(labels.value.map(label => label.trim()).filter(label => label !== ''))
  )
  if (cleaned.length === 0) {
    ElMessage.warning('请输入至少一个标签')
    return
  }
  if (props.taskIds.length === 0) {
    ElMessage.warning('请先选择任务')
    return
  }
  submitting.value = true
  try {
    const res = await taskApi.batchAddLabels(props.taskIds, cleaned)
    ElMessage.success(res?.message || `成功为 ${res?.success_count ?? props.taskIds.length} 个任务添加标签`)
    emit('success')
    close()
  } catch (err: any) {
    ElMessage.error(err?.response?.data?.error || '批量添加标签失败')
  } finally {
    submitting.value = false
  }
}
</script>

<template>
  <el-dialog
    :model-value="visible"
    title="批量添加标签"
    width="460px"
    :fullscreen="dialogFullscreen"
    :close-on-click-modal="false"
    destroy-on-close
    @update:model-value="emit('update:visible', $event)"
  >
    <div class="batch-add-label">
      <p class="batch-add-label__tip">
        将为选中的 {{ taskIds.length }} 个任务追加以下标签（保留原有标签，自动去重）。
      </p>
      <TaskLabelPicker :key="pickerKey" ref="pickerRef" v-model="labels" />
    </div>
    <template #footer>
      <el-button @click="close">取消</el-button>
      <el-button type="primary" :loading="submitting" @click="handleConfirm">确定</el-button>
    </template>
  </el-dialog>
</template>

<style scoped>
.batch-add-label__tip {
  margin: 0 0 12px;
  font-size: 13px;
  line-height: 1.6;
  color: var(--dd-text-2);
}
</style>
