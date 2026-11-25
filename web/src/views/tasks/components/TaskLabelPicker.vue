<script setup lang="ts">
/**
 * 任务标签输入（issue #157）：已选标签 + 输入框（兼搜索）+「添加」+ 下方「已有标签」候选。
 * 任务表单与「批量添加标签」弹窗共用。四条硬约定，去掉任何一条都是静默失效：
 *   1. 宿主点保存前必须先调 commitPending()：输入框里没按回车的字在这一步并进来。
 *      原来的标签框只在回车时变成标签，没回车的字被静默丢掉、界面照样提示成功（#157 ①）。
 *   2. 候选 chip、「添加」、「全部 N 个 / 收起」一律原生 <button type="button"> + @mousedown.prevent：
 *      漏写 type，它在 el-form 渲染的 <form> 里就是提交按钮，在输入框里按回车会触发原生提交、整页刷新；
 *      mousedown.prevent 让点它不抢输入框的焦点，手机上不会因此收起 / 弹出键盘。
 *      不用 el-check-tag / el-tag 当 chip：它们是 span，键盘聚焦不到。
 *   3. 回车用 @keydown.enter，并跳过输入法组字（isComposing / keyCode 229）；不用 @keyup.enter
 *      （Windows 中文输入法回车上屏英文时 keyup 报的是 Enter，会把刚上屏的字母直接变成标签）。
 *   4. 输入状态（query）放在本组件里：宿主弹窗是 destroy-on-close，关窗即销毁，
 *      不会把 A 任务里没回车的字带进下一个打开的任务（宿主另用 :key 兜住「关窗动画没走完又打开」那条缝）。
 * 刻意不做「失焦即并入」：输入框同时是搜索框，打了半截字去点别处就入库不对；只在回车、点「添加」、宿主保存三处并入。
 */
import { computed, onMounted, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { taskApi, type TaskLabelSummary } from '@/api/task'
import { isInternalTaskLabel, normalizeTaskNameCounts } from '../taskLabels'

const props = defineProps<{
  // 只含用户可编辑的普通标签；`分组:` / `subscription:` 两类内部标签由宿主自己藏起来，不传进来
  modelValue: string[]
}>()

const emit = defineEmits<{
  'update:modelValue': [value: string[]]
}>()

// 半角、全角逗号都当分隔符：存储按半角逗号拼接，标签里留着逗号保存后会被拆成两个，不如输入时就拆开（所见即所得）
const SEPARATOR = /[,，]/
// 输入框为空时只露出最常用的前 12 个，其余收在「全部 N 个」里
const COLLAPSED_LIMIT = 12
const INTERNAL_PREFIX_WARNING = '标签不能以「分组:」或「subscription:」开头，分组请填在「任务分组」里'

const query = ref('')
const candidates = ref<TaskLabelSummary[]>([])
const expanded = ref(false)

// 已选标签的比较键：trim + 忽略大小写。已选「Prod」时，「prod」既不出现在候选里，也不会再加一个
const selectedKeys = computed(() => new Set(props.modelValue.map(label => label.trim().toLowerCase())))

// 候选里排除已选的：同一个标签在屏幕上只出现一次，在已选区 × 掉之后自动回到候选
const availableCandidates = computed(() =>
  candidates.value.filter(item => !selectedKeys.value.has(item.name.toLowerCase()))
)

// 搜索词 = 最后一个逗号之后的片段：「a,b,监」搜的是「监」
const keyword = computed(() => {
  const segments = query.value.split(SEPARATOR)
  return (segments[segments.length - 1] ?? '').trim().toLowerCase()
})

// 有搜索词：在全部候选里做不区分大小写的包含匹配，不受 12 个的限制；没有搜索词：按收起 / 展开截取
const visibleCandidates = computed(() => {
  if (keyword.value) {
    return availableCandidates.value.filter(item => item.name.toLowerCase().includes(keyword.value))
  }
  return expanded.value ? availableCandidates.value : availableCandidates.value.slice(0, COLLAPSED_LIMIT)
})

// 一个候选都没有（接口失败、老面板、真的没有）时整块不显示；候选都已选上、又没在搜时也不显示
const showCandidates = computed(() =>
  candidates.value.length > 0 && (keyword.value !== '' || availableCandidates.value.length > 0)
)

// 保存时会并进去的新标签（已去掉重复与带内部前缀的段），驱动输入框下方那行提示
const pendingLabels = computed(() => planLabels(query.value.split(SEPARATOR)).added)

/**
 * 把若干段文字按统一规则排成「要新加的」与「被拦下的」：
 *   - 逐段 trim、去空、去重；与某个候选忽略大小写相等时沿用候选的写法（手机输入法句首自动大写也不会造出第二个「Prod」）；
 *   - 与已选（含本批前面刚排进来的）忽略大小写相等算重复，不再加；
 *   - trim 后以 `分组:` / `subscription:` 开头的拦下，trim 后的原文放进 rejected，由调用方留在输入框里等用户改。
 * 只校验新加的段，不碰已选标签：历史脏数据（比如 " subscription:1"）不能让任务保存不了。
 */
function planLabels(segments: string[]) {
  const seen = new Set(selectedKeys.value)
  const added: string[] = []
  const rejected: string[] = []
  for (const segment of segments) {
    const value = segment.trim()
    if (!value) continue
    if (isInternalTaskLabel(value)) {
      rejected.push(value)
      continue
    }
    const key = value.toLowerCase()
    if (seen.has(key)) continue
    seen.add(key)
    // candidates 已按 count 降序排好：大小写变体不止一个时，沿用最常用的那个写法
    const candidate = candidates.value.find(item => item.name.toLowerCase() === key)
    added.push(candidate ? candidate.name : value)
  }
  return { added, rejected }
}

/**
 * 把一批段落提交进已选标签：合法的并进去，被拦下的原文留在输入框里并提示一次。返回 false 表示有被拦下的段。
 * 每次操作只调一次（一次 emit）：v-model 的 props 要等下一次渲染才更新，同一拍里连调两次，第二次读到的还是旧的已选。
 */
function commitSegments(segments: string[]): boolean {
  const { added, rejected } = planLabels(segments)
  if (added.length > 0) {
    emit('update:modelValue', [...props.modelValue, ...added])
  }
  query.value = rejected.join(',')
  if (rejected.length > 0) {
    // 只在这里提示：回车、点「添加」、点候选、宿主保存都是各自一次操作，各提示一次；没有失焦并入，不会叠两条
    ElMessage({ type: 'warning', message: INTERNAL_PREFIX_WARNING, grouping: true })
    return false
  }
  return true
}

/**
 * 把输入框里的全部文字并进已选标签（回车、点「添加」走这里；宿主保存前也必须调它）。
 * 有被拦下的段时返回 false（原文留在框里、已提示），宿主据此中止保存；其余情况返回 true。
 * emit 是同步的：返回时宿主 v-model 绑的那份数组已经是新值，可以直接拿去组装请求体。
 */
function commitPending(): boolean {
  return commitSegments(query.value.split(SEPARATOR))
}

function handleEnter(event: Event | KeyboardEvent) {
  const e = event as KeyboardEvent
  // 输入法组字中按回车只是上屏，不能当成「添加」：Chrome 报 isComposing，Safari 先发 compositionend、再发 keyCode 229 的 keydown
  if (e.isComposing || e.keyCode === 229) return
  // 挡掉浏览器在 <form> 里按回车的隐式提交
  e.preventDefault()
  commitPending()
}

// 点候选 chip：最后一个逗号之前的各段按回车的规则先提交，再用这个 chip 替换掉最后那段（那段是搜索词）。
// 不能只加 chip 再清空输入框：「a,b,监」点「监控」会把 a、b 一起静默丢掉。被拦下的非法段照样留在输入框里。
function pickCandidate(name: string) {
  const segments = query.value.split(SEPARATOR)
  segments.pop()
  commitSegments([...segments, name])
}

function removeLabel(label: string) {
  emit('update:modelValue', props.modelValue.filter(item => item !== label))
}

// 候选在挂载时拉一次（宿主弹窗 destroy-on-close，每次打开都会重新挂载、重新拉）。
// 失败、老面板 404、权限不足、演示站兜底体、不是数组：一律当作「没有候选」，静默，输入框照常可用。
// 规整之后再兜底滤一遍内部前缀（服务端已经滤过），按 count 降序（常用的在前）、同 count 按 name 排。
onMounted(async () => {
  try {
    const list = normalizeTaskNameCounts(await taskApi.labels()).filter(item => !isInternalTaskLabel(item.name))
    list.sort((left, right) => right.count - left.count || (left.name < right.name ? -1 : left.name > right.name ? 1 : 0))
    candidates.value = list
  } catch {
    // 静默：见上
  }
})

defineExpose({ commitPending })
</script>

<template>
  <div class="label-picker">
    <!-- 已选标签：长标签单行省略，title 给全名。
         disable-transitions：根节点就是 span，title 直接落在它上面；也省掉 EP 那段不吃动效令牌的缩放动画 -->
    <div v-if="modelValue.length > 0" class="label-picker__selected">
      <el-tag
        v-for="label in modelValue"
        :key="label"
        closable
        disable-transitions
        :title="label"
        @close="removeLabel(label)"
      >{{ label }}</el-tag>
    </div>

    <!-- enterkeyhint="done"：安卓 Chrome 上没设它时，后面紧跟「任务分组」输入框，软键盘的动作键是「下一项」，
         按下去只挪焦点、不发回车。关自动大写 / 自动更正：免得手机上造出「Prod」「prod」两个标签。
         占位文案刻意只留 11 个字：手机上输入框右边还挤着「添加」，390 宽时文字区只剩约 230px（14px 字号放 16 个字），
         原来 17 个字的「输入新标签，回车添加，逗号分隔多个」会被截掉；11 个字到 320 宽也放得下，加字前先在手机宽下量一遍。
         「输入新标签」由 aria-label 和宿主上下文（表单项「标签」、批量弹窗的提示语）兜着 -->
    <div class="label-picker__input-row">
      <el-input
        v-model="query"
        placeholder="回车添加，逗号分隔多个"
        aria-label="输入新标签"
        enterkeyhint="done"
        autocapitalize="off"
        autocorrect="off"
        spellcheck="false"
        @keydown.enter="handleEnter"
      />
      <button
        type="button"
        class="label-picker__add"
        :disabled="!query.trim()"
        @mousedown.prevent
        @click="commitPending"
      >
        添加
      </button>
    </div>

    <!-- 所见即所得：搜索时打的字直接点保存也会被存成标签，这里先说清楚 -->
    <div v-if="pendingLabels.length > 0" class="label-picker__pending">
      保存时会把{{ pendingLabels.map(label => `「${label}」`).join('') }}作为新标签一起保存
    </div>

    <div v-if="showCandidates" class="label-picker__candidates">
      <div class="label-picker__candidates-head">
        <span>已有标签（不含分组、订阅）</span>
        <button
          v-if="!keyword && availableCandidates.length > COLLAPSED_LIMIT"
          type="button"
          class="label-picker__toggle"
          :aria-expanded="expanded"
          @mousedown.prevent
          @click="expanded = !expanded"
        >
          {{ expanded ? '收起' : `全部 ${availableCandidates.length} 个` }}
        </button>
      </div>
      <!-- 展开全部、或搜出来的结果很多时整块限高、内部滚动，不把弹窗撑得很长 -->
      <div
        v-if="visibleCandidates.length > 0"
        class="label-picker__chips"
        :class="{ 'is-scroll': expanded || keyword !== '' }"
        role="group"
        aria-label="已有标签"
      >
        <button
          v-for="item in visibleCandidates"
          :key="item.name"
          type="button"
          class="label-picker__chip"
          :title="`${item.name}（${item.count} 个任务）`"
          @mousedown.prevent
          @click="pickCandidate(item.name)"
        >
          <span class="label-picker__chip-text">{{ item.name }}</span>
        </button>
      </div>
      <div v-else class="label-picker__empty">没有匹配的已有标签</div>
    </div>
  </div>
</template>

<style scoped lang="scss">
.label-picker {
  display: flex;
  flex-direction: column;
  gap: 8px;
  width: 100%;
  min-width: 0;
  // el-form-item 的内容区是 32px 行高，提示文字沿用它会被撑得很松
  line-height: 1.5;
}

.label-picker__selected {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;

  // 已选标签单行省略：el-tag 是 inline-flex，内容区要 min-width:0 才缩得下来，关闭键不跟着缩。
  // .el-tag__content / .el-tag__close 由 EP 渲染、不带 data-v，只能经 :deep() 够到
  :deep(.el-tag) {
    max-width: 16em;
  }

  :deep(.el-tag__content) {
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  :deep(.el-tag__close) {
    flex-shrink: 0;
  }
}

.label-picker__input-row {
  display: flex;
  align-items: stretch;
  gap: 8px;

  :deep(.el-input) {
    flex: 1;
    min-width: 0;
  }
}

// 「添加」：原生 button（硬约定见文件头），观感照 el-button 默认档的令牌，高度跟输入框一起拉齐
.label-picker__add {
  flex-shrink: 0;
  padding: 0 14px;
  border: 1px solid var(--dd-border-strong);
  // 与全局 .el-button 统一走按钮排版令牌（默认 control 档，素白 clean 主题为胶囊）
  border-radius: var(--dd-btn-radius);
  background: var(--el-fill-color-blank);
  color: var(--el-text-color-regular);
  font-family: inherit;
  font-size: 14px;
  cursor: pointer;
  transition:
    color var(--dd-motion-fast) var(--dd-ease-standard),
    background-color var(--dd-motion-fast) var(--dd-ease-standard),
    border-color var(--dd-motion-fast) var(--dd-ease-standard);

  &:hover:not(:disabled) {
    color: var(--dd-accent-ink);
    border-color: color-mix(in srgb, var(--dd-accent) 30%, var(--dd-surface-solid));
    background: color-mix(in srgb, var(--dd-accent) 10%, var(--dd-surface-solid));
  }

  &:focus-visible {
    outline: 2px solid var(--dd-accent-ring);
    outline-offset: 1px;
  }

  &:disabled {
    border-color: var(--el-border-color-light);
    color: var(--dd-text-3);
    cursor: not-allowed;
  }
}

.label-picker__pending,
.label-picker__empty {
  font-size: 12px;
  color: var(--dd-text-2);
  // 很长的英文标签不能把手机全屏弹窗撑出横向滚动
  overflow-wrap: anywhere;
}

.label-picker__candidates-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  margin-bottom: 6px;
  font-size: 12px;
  color: var(--dd-text-2);
}

// 「全部 N 个 / 收起」：文字链观感，只有 focus-visible 的焦点环露出形状
.label-picker__toggle {
  flex-shrink: 0;
  padding: 0 2px;
  border: none;
  border-radius: var(--dd-radius-control);
  background: transparent;
  color: var(--dd-accent-ink);
  font-family: inherit;
  font-size: 12px;
  cursor: pointer;
  transition: color var(--dd-motion-fast) var(--dd-ease-standard);

  &:hover {
    color: color-mix(in srgb, var(--dd-accent-ink) 75%, var(--dd-text));
  }

  &:focus-visible {
    outline: 2px solid var(--dd-accent-ring);
    outline-offset: 1px;
  }
}

.label-picker__chips {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;

  &.is-scroll {
    max-height: min(40vh, 240px);
    overflow-y: auto;
  }
}

// 候选 chip。⚠️ TaskForm.vue 的「已有分组」chip（.group-chip）是同一套样式，改这里要同步那边
.label-picker__chip {
  display: inline-flex;
  align-items: center;
  max-width: 16em;
  height: 28px;
  padding: 0 10px;
  border: 1px solid var(--dd-border-strong);
  // 标签类小表面 → control 档
  border-radius: var(--dd-radius-control);
  background: var(--el-fill-color-blank);
  color: var(--el-text-color-regular);
  font-family: inherit;
  font-size: 12px;
  cursor: pointer;
  transition:
    color var(--dd-motion-fast) var(--dd-ease-standard),
    background-color var(--dd-motion-fast) var(--dd-ease-standard),
    border-color var(--dd-motion-fast) var(--dd-ease-standard);

  &:hover {
    color: var(--dd-accent-ink);
    border-color: color-mix(in srgb, var(--dd-accent) 50%, var(--dd-surface-solid));
    background: color-mix(in srgb, var(--dd-accent) 10%, var(--dd-surface-solid));
  }

  // 负 offset 把焦点环画在 chip 内侧：限高滚动时外扩的环会被容器裁掉
  &:focus-visible {
    outline: 2px solid var(--dd-accent-ring);
    outline-offset: -1px;
  }
}

.label-picker__chip-text {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

@media (max-width: 768px) {
  // 触屏点击区抬到 32px
  .label-picker__chip {
    height: 32px;
  }
}
</style>
