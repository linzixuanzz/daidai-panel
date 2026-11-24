<script setup lang="ts">
import { ref, watch } from 'vue'
import { taskApi } from '@/api/task'
import { formatDateTime } from '@/utils/datetime'
import CronVisualDialog from './CronVisualDialog.vue'

type StopRuleState = {
  id: number
  expression: string
  parseResult: any | null
}

const props = defineProps<{
  modelValue: string
}>()

const emit = defineEmits<{
  'update:modelValue': [value: string]
}>()

const rules = ref<StopRuleState[]>([])
const showVisualDialog = ref(false)
const activeVisualRuleIndex = ref(0)
/** 传给可视化弹窗的初值，打开那一刻取一次快照，弹窗内部自己反解 */
const visualExpression = ref('')

let nextRuleId = 1

function createRule(expression = ''): StopRuleState {
  return {
    id: nextRuleId++,
    expression,
    parseResult: null
  }
}

function splitExpressions(value: string) {
  return value
    .split(/\r?\n/)
    .map(item => item.trim())
    .filter(Boolean)
}

function joinExpressions(items: string[]) {
  return items
    .map(item => item.trim())
    .filter(Boolean)
    .join('\n')
}

function syncRulesFromModel(value: string) {
  const expressions = splitExpressions(value)
  rules.value = expressions.length > 0
    ? expressions.map(expression => createRule(expression))
    : [createRule('')]

  rules.value.forEach((_, index) => {
    void parseRule(index)
  })
}

watch(
  () => props.modelValue,
  (value) => {
    const incoming = joinExpressions(splitExpressions(value))
    const current = joinExpressions(rules.value.map(rule => rule.expression))
    if (incoming === current && rules.value.length > 0) {
      return
    }
    syncRulesFromModel(value)
  },
  { immediate: true }
)

function emitRules() {
  emit('update:modelValue', joinExpressions(rules.value.map(rule => rule.expression)))
}

async function parseRule(index: number) {
  const rule = rules.value[index]
  if (!rule) {
    return
  }

  const expression = rule.expression.trim()
  if (!expression) {
    rule.parseResult = null
    return
  }

  try {
    rule.parseResult = await taskApi.cronParse(expression)
  } catch {
    rule.parseResult = null
  }
}

function handleRuleInput(index: number) {
  emitRules()
  void parseRule(index)
}

function addRule(afterIndex = rules.value.length - 1) {
  rules.value.splice(afterIndex + 1, 0, createRule(''))
  emitRules()
}

function removeRule(index: number) {
  if (rules.value.length <= 1) {
    const first = rules.value[0]
    if (first) {
      first.expression = ''
      first.parseResult = null
    }
    emitRules()
    return
  }
  rules.value.splice(index, 1)
  emitRules()
}

function openVisualDialog(index: number) {
  activeVisualRuleIndex.value = index
  visualExpression.value = rules.value[index]?.expression || ''
  showVisualDialog.value = true
}

/**
 * 应用可视化编辑的结果：写回表达式 -> 上报父组件 -> 立刻解析拿描述和下次停止时间。
 * 三步缺一不可，漏 emitRules 会「改了但没上报」，漏 parseRule 会「描述停在改之前」。
 */
function applyVisualExpression(expression: string) {
  const index = activeVisualRuleIndex.value
  const rule = rules.value[index]
  if (!rule) {
    return
  }
  rule.expression = expression
  emitRules()
  void parseRule(index)
}

function handleKeyDown(event: KeyboardEvent) {
  if (event.key === ' ') {
    event.stopPropagation()
  }
}
</script>

<template>
  <div class="stop-schedule-input">
    <div
      v-for="(rule, index) in rules"
      :key="rule.id"
      class="stop-rule-block"
    >
      <div class="stop-rule-row">
        <el-input
          v-model="rule.expression"
          placeholder="cron 表达式，留空不自动停止（如 0 12 * * *）"
          clearable
          @keydown="handleKeyDown"
          @input="handleRuleInput(index)"
        />
        <div class="stop-rule-actions">
          <el-button
            v-if="index === 0"
            class="stop-add-btn"
            size="small"
            @click="addRule(index)"
          >
            增加停止规则
          </el-button>
          <el-button
            v-else
            text
            size="small"
            class="stop-remove-btn"
            @click="removeRule(index)"
          >
            删除
          </el-button>
        </div>
      </div>
      <!--
        信息区改成常驻：「可视化编辑」入口和解析结果同处一行（与 CronInput 的 .cron-meta 同构）。
        原来整块挂 v-if="rule.parseResult"，规则为空时整行不渲染 —— 而「还没填表达式」恰恰是最需要
        可视化入口的时候。
      -->
      <div class="stop-rule-info">
        <div class="stop-meta">
          <template v-if="rule.parseResult?.is_valid">
            <div class="valid-badge">
              <el-icon class="badge-icon"><CircleCheck /></el-icon>
              <span class="badge-text">{{ rule.parseResult.description }}</span>
            </div>
            <div v-if="rule.parseResult.next_run_times?.length" class="next-times">
              <el-icon class="time-icon"><Clock /></el-icon>
              <span class="label">下次停止</span>
              <span class="time-value">{{ formatDateTime(rule.parseResult.next_run_times[0]) }}</span>
            </div>
          </template>
          <div v-else-if="rule.parseResult" class="error-badge">
            <el-icon class="badge-icon"><CircleClose /></el-icon>
            <span class="badge-text">{{ rule.parseResult.error }}</span>
          </div>
          <el-button text size="small" @click="openVisualDialog(index)">可视化编辑</el-button>
        </div>
      </div>
    </div>
    <div class="stop-schedule-hint">
      到达设定时间后自动停止正在运行的任务，适合需要在特定时段运行的长驻任务。
    </div>

    <CronVisualDialog
      v-model:visible="showVisualDialog"
      :expression="visualExpression"
      @apply="applyVisualExpression"
    />
  </div>
</template>

<style scoped lang="scss">
.stop-schedule-input {
  width: 100%;
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.stop-rule-block {
  padding: 0;
}

.stop-rule-row {
  display: flex;
  gap: 10px;
  align-items: stretch;
}

.stop-rule-actions {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-shrink: 0;
}

.stop-add-btn {
  flex-shrink: 0;
}

.stop-remove-btn {
  padding-left: 0;
  padding-right: 0;
}

.stop-rule-info {
  margin-top: 6px;
}

.stop-meta {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
}

.stop-schedule-hint {
  font-size: 11px;
  color: var(--dd-text-2);
  line-height: 1.5;
}

.valid-badge {
  display: inline-flex;
  width: fit-content;
  align-items: center;
  gap: 4px;
  padding: 4px 10px;
  // 实心状态底读 *-solid（亮色主题压深过一档），文字配 --dd-on-status
  background: var(--dd-ok-solid);
  // 与 .error-badge 是同一槽位互斥的两态，统一走 control 档，避免有效/错误之间形状跳变
  border-radius: var(--dd-radius-control);
  color: var(--dd-on-status);
  font-weight: 500;

  .badge-icon {
    font-size: 14px;
  }

  .badge-text {
    font-size: 12px;
  }
}

.error-badge {
  display: inline-flex;
  width: fit-content;
  align-items: center;
  gap: 4px;
  padding: 4px 10px;
  background: var(--dd-err-solid);
  // 校验错误提示块 → control 档（与 CronInput 的同名元素保持一致）
  border-radius: var(--dd-radius-control);
  color: var(--dd-on-status);
  font-weight: 500;

  .badge-icon {
    font-size: 14px;
  }

  .badge-text {
    font-size: 12px;
  }
}

.next-times {
  display: flex;
  align-items: center;
  gap: 5px;
  padding: 4px 10px;
  background: color-mix(in srgb, var(--dd-warn) 10%, var(--dd-surface-solid));
  // 「下次停止」信息小块 → control 档
  border-radius: var(--dd-radius-control);
  color: var(--dd-warn-ink);
  font-weight: 500;
  border: 1px solid color-mix(in srgb, var(--dd-warn) 30%, var(--dd-surface-solid));

  .time-icon {
    font-size: 13px;
  }

  .label {
    font-size: 11px;
  }

  .time-value {
    font-family: var(--dd-font-mono);
    font-size: 11px;
  }
}

@media (max-width: 768px) {
  .stop-rule-row {
    flex-direction: column;
  }

  .stop-rule-actions {
    width: 100%;
    justify-content: flex-end;
  }

  .stop-add-btn {
    width: 100%;
  }
}
</style>
