<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { ElMessage } from 'element-plus'
import { useResponsive } from '@/composables/useResponsive'

type EnvFormModel = {
  id: number
  name: string
  value: string
  remarks: string
  group?: string
  groups: string[]
  // 排序值（#131，即接口里的 position）。父组件打开编辑时带进来；emit 出去时只有用户真改过才会带
  position?: number | null
  // 「重要」标记（APP #16）。父组件打开时带进来；emit 出去：新建只在打开时带 true，编辑只在拨过开关时带
  important?: boolean
}

const props = withDefaults(defineProps<{
  modelValue: boolean
  mode: 'create' | 'edit'
  initialData?: EnvFormModel | null
  groups?: string[]
  // 提交在途标记：父组件发起创建/更新请求期间置位。
  // 请求返回前弹窗一直开着，不锁住按钮的话连点就会连发 POST，造成重复创建。
  submitting?: boolean
}>(), {
  initialData: null,
  groups: () => [],
  submitting: false
})

const emit = defineEmits<{
  'update:modelValue': [value: boolean]
  save: [value: EnvFormModel | EnvFormModel[]]
}>()

// 与后端 envNamePattern 对齐：字母/数字/下划线，且不能以数字开头
const ENV_NAME_PATTERN = /^[A-Za-z_][A-Za-z0-9_]*$/

function splitEnvGroups(value: string): string[] {
  return value
    .split(/[,，;；\n\r\t]/)
    .map(group => group.trim())
    .filter((group, index, list) => group !== '' && list.indexOf(group) === index)
}

function normalizeGroupList(groups: string[]): string[] {
  return splitEnvGroups(groups.join(','))
}

function createEmptyForm(): EnvFormModel {
  return { id: 0, name: '', value: '', remarks: '', group: '', groups: [], important: false }
}

const form = ref<EnvFormModel>(createEmptyForm())
const splitMode = ref(false)
// 打开弹窗那一刻的排序值，用来判断用户到底改没改（契约 C5：只有改过才随请求发送）
const initialPosition = ref<number | null>(null)
// 打开弹窗那一刻的「重要」标记，同上：没拨过开关就不发，免得冲掉 APP 或另一个标签页刚改的标记
const initialImportant = ref(false)
const { dialogFullscreen } = useResponsive()

const isCreate = computed(() => props.mode === 'create')
const dialogTitle = computed(() => isCreate.value ? '新建环境变量' : '编辑环境变量')
const submitText = computed(() => isCreate.value ? '创建' : '保存')

// 已输入但不符合规则时，提示文字变红
const nameInvalid = computed(() => {
  const name = form.value.name.trim()
  return name !== '' && !ENV_NAME_PATTERN.test(name)
})

function syncForm() {
  const initial = props.initialData ?? createEmptyForm()
  const initialGroups = initial.groups?.length
    ? normalizeGroupList(initial.groups)
    : splitEnvGroups(initial.group || '')
  // 只认有限数字；缺字段、null、脏值都当作「没有原值」，输入框留空
  const initialPositionValue =
    typeof initial.position === 'number' && Number.isFinite(initial.position) ? initial.position : null
  initialPosition.value = initialPositionValue
  // 只认布尔 true；老数据、缺字段一律当作「不是重要变量」
  initialImportant.value = initial.important === true
  form.value = {
    ...createEmptyForm(),
    ...initial,
    group: initialGroups.join(','),
    groups: initialGroups,
    position: initialPositionValue,
    important: initialImportant.value
  }
  splitMode.value = false
}

function closeDialog() {
  emit('update:modelValue', false)
}

function handleSave() {
  // 上一发还在路上时直接忽略：按钮虽然已经 loading，但回车等入口仍可能再次触发
  if (props.submitting) {
    return
  }

  const name = form.value.name.trim()
  const remarks = form.value.remarks.trim()
  const groups = normalizeGroupList(form.value.groups)
  const group = groups.join(',')

  if (!name) {
    ElMessage.warning('变量名不能为空')
    return
  }

  if (!ENV_NAME_PATTERN.test(name)) {
    ElMessage.warning('变量名只能包含字母、数字、下划线，且不能以数字开头')
    return
  }

  if (isCreate.value && splitMode.value) {
    const lines = form.value.value.split('\n').filter(line => line.trim() !== '')
    if (lines.length === 0) {
      ElMessage.warning('请输入至少一行变量值')
      return
    }
    const items: EnvFormModel[] = lines.map(line => ({
      id: 0,
      name,
      value: line.trim(),
      remarks,
      group,
      groups,
      // 开了「重要」才带，拆出来的每一行都标；没开就不带这个键
      ...(form.value.important ? { important: true } : {})
    }))
    emit('save', items)
  } else {
    const payload: EnvFormModel = {
      id: form.value.id,
      name,
      value: form.value.value,
      remarks,
      group,
      groups
    }
    // 「重要」（APP #16）：新建只在开着时带 true；编辑只在拨过开关时带当前值（与下面的排序值同一个道理）
    if (isCreate.value ? form.value.important : form.value.important !== initialImportant.value) {
      payload.important = form.value.important === true
    }
    // 排序值（#131，契约 C5）只在编辑模式、并且用户真的改过时才带上。
    // 没改就不带：原值可能是青龙原样导入的大数、置顶留下的负数或小数，原样写回一遍没有意义，
    // 还会让一次「只改了备注」的保存也去碰 position。
    // 清空输入框时 el-input-number 给的是 null，不是数字 ⇒ 当作没改，不会把排序值清掉。
    if (!isCreate.value) {
      const nextPosition = form.value.position
      if (typeof nextPosition === 'number' && nextPosition !== initialPosition.value) {
        // 兜底：EP 2.13 会把 Infinity（敲 1e400 这种写法）夹到默认上限 MAX_SAFE_INTEGER 再回写，正常走不到这里；
        // 留着是防 EP 以后改了默认边界、Infinity 漏过来——服务端对 NaN / Infinity 回 400（契约 C5）
        if (!Number.isFinite(nextPosition)) {
          ElMessage.warning('排序值必须是有限的数字')
          return
        }
        payload.position = nextPosition
      }
    }
    emit('save', payload)
  }
}

watch(
  () => [props.modelValue, props.initialData, props.mode],
  ([visible]) => {
    if (visible) {
      syncForm()
    }
  },
  { immediate: true }
)
</script>

<template>
  <el-dialog
    :model-value="modelValue"
    :title="dialogTitle"
    width="760px"
    top="8vh"
    class="env-edit-dialog"
    :fullscreen="dialogFullscreen"
    :close-on-click-modal="false"
    destroy-on-close
    @update:model-value="emit('update:modelValue', $event)"
  >
    <el-form
      class="env-edit-dialog__form"
      :model="form"
      :label-width="dialogFullscreen ? 'auto' : '84px'"
      :label-position="dialogFullscreen ? 'top' : 'right'"
    >
      <el-form-item label="变量名">
        <el-input v-model="form.name" placeholder="变量名 (如: API_KEY)" />
        <div class="env-edit-dialog__name-hint" :class="{ 'is-error': nameInvalid }">
          只能输入字母、数字、下划线，且不能以数字开头
        </div>
      </el-form-item>
      <el-form-item v-if="isCreate" label="按行拆分">
        <div style="display: flex; align-items: center; gap: 8px; width: 100%">
          <el-switch v-model="splitMode" />
          <span style="font-size: 12px; color: var(--el-text-color-secondary)">
            {{ splitMode ? '每行创建一个变量' : '所有行作为一个变量值' }}
          </span>
        </div>
      </el-form-item>
      <el-form-item class="env-edit-dialog__value-item" label="值">
        <el-input
          v-model="form.value"
          type="textarea"
          :rows="isCreate ? 10 : 12"
          :placeholder="splitMode ? '每行一个值' : '变量值'"
        />
      </el-form-item>
      <el-form-item label="备注">
        <el-input v-model="form.remarks" placeholder="备注说明" />
      </el-form-item>
      <el-form-item label="分组">
        <el-select
          v-model="form.groups"
          multiple
          filterable
          allow-create
          default-first-option
          collapse-tags
          collapse-tags-tooltip
          clearable
          placeholder="可选择多个分组，也可直接输入新分组"
          style="width: 100%"
        >
          <el-option v-for="group in groups" :key="group" :label="group" :value="group" />
        </el-select>
      </el-form-item>
      <!-- 「重要」标记（APP #16）：新建与编辑都能设。只是防误删的提醒，服务端不拦删除，所以文案不写「受保护」 -->
      <el-form-item label="重要变量">
        <div style="display: flex; align-items: center; gap: 8px; width: 100%">
          <el-switch v-model="form.important" />
          <span style="font-size: 12px; color: var(--el-text-color-secondary)">
            列表里显示紫色「重要」标记，删除时多确认一次，批量删除会跳过它
          </span>
        </div>
      </el-form-item>
      <!-- 排序值（#131）只在编辑模式出现：新建和导入永远追加到普通区末尾，这里不让填。
           它就是列表接口里本来就有的 position，没有新增字段；拖拽改的也是它，两边天然一致。
           代价是拖拽会把整个区重新编号成 1000、2000…（手填的 1、2、3 会变成 1000、2000、3000，相对顺序不变），提示里写明了。
           🔴 不要再收窄 min / max：EP 拿到越界的 v-model 会在挂载那一刻把它夹回边界并回写（input-number 的 verifyValue），
              存量里有青龙原样导入的大数（青龙上限 9e15）、置顶留下的负数，边界一收窄，用户什么都没动也会被当成「改过了」发出去。
              不写时 EP 2.13 的默认边界是 ±Number.MAX_SAFE_INTEGER（约 ±9.007e15），不是 ±Infinity，正好把这些存量都装得下。
              也不加 precision：它只把显示四舍五入、不改 v-model，显示的数和实际存的数会对不上。
           controls 关掉：这里要的是直接敲一个数，±按钮的步长怎么取都别扭（1 太小，1000 又容易和相邻项撞值）。 -->
      <el-form-item v-if="!isCreate" label="排序值">
        <el-input-number
          v-model="form.position"
          class="env-edit-dialog__position-input"
          :controls="false"
          placeholder="越小越靠前"
        />
        <div class="env-edit-dialog__field-hint">
          越小越靠前，置顶区与普通区各排各的；同名变量（多账号）拼给脚本时也按这个顺序。拖拽排序后会重新编号为 1000、2000…
        </div>
      </el-form-item>
    </el-form>
    <template #footer>
      <el-button @click="closeDialog">取消</el-button>
      <el-button type="primary" :loading="submitting" :disabled="submitting" @click="handleSave">{{ submitText }}</el-button>
    </template>
  </el-dialog>
</template>

<style scoped>
:deep(.env-edit-dialog) {
  max-width: calc(100vw - 48px);
}

:deep(.env-edit-dialog .el-dialog__header) {
  padding: 20px 24px 14px;
  margin-right: 0;
  border-bottom: 1px solid var(--el-border-color-lighter);
}

:deep(.env-edit-dialog .el-dialog__body) {
  max-height: calc(78vh - 128px);
  padding: 0 24px;
  overflow-y: auto;
}

:deep(.env-edit-dialog .el-dialog__footer) {
  padding: 14px 24px 18px;
  border-top: 1px solid var(--el-border-color-lighter);
}

.env-edit-dialog__form {
  padding: 18px 0 20px;
}

.env-edit-dialog__form :deep(.el-form-item__label) {
  white-space: nowrap;
  word-break: keep-all;
}

/* 排序值输入框：数字不长，不必铺满整行；EP 默认的 150px 又略窄，青龙导入的十几位大数放不下。手机端铺满，见下方媒体查询 */
.env-edit-dialog__position-input {
  width: 220px;
}

.env-edit-dialog__name-hint,
.env-edit-dialog__field-hint {
  width: 100%;
  margin-top: 4px;
  font-size: 12px;
  line-height: 1.5;
  color: var(--el-text-color-secondary);
}

.env-edit-dialog__name-hint.is-error {
  color: var(--dd-err-ink);
}

.env-edit-dialog__value-item :deep(.el-form-item__label) {
  align-self: flex-start;
}

.env-edit-dialog__value-item :deep(.el-textarea__inner) {
  min-height: 280px;
  max-height: 42vh;
  overflow: auto;
  resize: vertical;
  line-height: 1.6;
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, "Liberation Mono", monospace;
}

@media (max-width: 768px) {
  :deep(.env-edit-dialog) {
    max-width: 100vw;
  }

  :deep(.env-edit-dialog .el-dialog__header) {
    padding: 16px 18px 12px;
  }

  :deep(.env-edit-dialog .el-dialog__body) {
    max-height: none;
    padding: 0 18px;
  }

  :deep(.env-edit-dialog .el-dialog__footer) {
    padding: 12px 18px 16px;
  }

  .env-edit-dialog__form {
    padding: 14px 0 18px;
  }

  .env-edit-dialog__value-item :deep(.el-textarea__inner) {
    min-height: 45vh;
    max-height: none;
  }

  .env-edit-dialog__position-input {
    width: 100%;
  }
}
</style>
