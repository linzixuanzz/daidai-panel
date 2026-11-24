<script lang="ts">
/**
 * 定时规则默认表达式的单一来源。
 *
 * 为什么放在这个组件文件里（而不是新起一个 utils 模块）：这两个值只服务于「定时规则」这一件事，
 * 而 CronInput 就是它的编辑器，任务表单与任务列表本来也都已经依赖本组件；
 * 再抽一层常量模块属于「为通用性多套一层」，本仓约定是不做（见 AGENTS.md 写代码的风格）。
 *
 * 两个值都写成 6 段（秒 分 时 日 月 周），与随机生成器、后端 21 条出厂预设同一形态。
 * ⚠️ 5 段补 6 段永远是在【开头补 0】，末尾补 `*` 会把「每分钟」变成「每秒」。
 *
 * 这次收敛之前，同一个面板里散着四处字面量（脚本入口、表单手动新建、切换定时类型的兜底、
 * 本组件新增规则行），改一处忘一处的风险一直在。现在全部指向下面这两个常量，
 * **每一处的取值都与改动前逐字节一致**，只是不再各写各的。
 */

/**
 * 「每天 00:00:00」，与 5 段的 `0 0 * * *` 语义相同。用在两处：
 * 1. 脚本页点「添加到定时任务」跳到任务页时的预填值；
 * 2. 本组件里新增一条规则行、或外部传进来是空值时的兜底。
 */
export const DEFAULT_CRON_DAILY_MIDNIGHT = '0 0 0 * * *'

/**
 * 「每分钟一次」。注意秒段固定为 0，`* * * * * *` 那种才是每秒。
 * 只用在任务表单里手动新建任务这一个入口。
 *
 * 与上面那个值不一样是【既有行为】：要不要把两个入口统一成同一个频率是产品决定，
 * 不该顺手夹在这次收敛里改掉。
 */
export const DEFAULT_CRON_EVERY_MINUTE = '0 * * * * *'
</script>

<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { taskApi } from '@/api/task'
import { useAuthStore } from '@/stores/auth'
import { useResponsive } from '@/composables/useResponsive'
import { formatDateTime } from '@/utils/datetime'
import RandomCronDialog from './RandomCronDialog.vue'
import CronVisualDialog from './CronVisualDialog.vue'

type CronRuleState = {
  id: number
  expression: string
  parseResult: any | null
}

/** 「我的常用」里的一条收藏 */
type CronFavorite = {
  name: string
  expression: string
}

const props = defineProps<{
  modelValue: string
}>()

const emit = defineEmits<{
  'update:modelValue': [value: string]
}>()

/**
 * 常用定时规则的收藏存在 localStorage。
 * 依据是「localStorage vs 服务端配置」的口径：这类纯本机的交互便利（分页大小、显示开关、
 * 常用收藏）不进服务端配置，换设备记不住是可接受的代价。
 */
const CRON_FAVORITES_STORAGE_KEY = 'dd:tasks:cron_favorites'
/** 收藏条数上限。它是置顶展示的，太多会把后端那 21 条出厂预设整个挤到看不见 */
const MAX_CRON_FAVORITES = 30
/** 输入解析的防抖时长。原来每敲一个字符就打一次 POST /tasks/cron/parse */
const PARSE_DEBOUNCE = 300

/**
 * 读取收藏。
 *
 * `u` 是归属校验：同一台机器可能多人共用面板（localStorage 按浏览器不按账号），
 * 名字对不上就当没有收藏，避免把别人的常用规则显示给当前用户。
 */
function readStoredCronFavorites(username: string): CronFavorite[] {
  if (typeof window === 'undefined') {
    return []
  }
  // 隐私模式 / 禁用站点存储时访问 localStorage 会直接抛错，不兜住会让整个 setup 挂掉、页面白掉
  try {
    const raw = window.localStorage.getItem(CRON_FAVORITES_STORAGE_KEY)
    if (!raw) return []
    const saved = JSON.parse(raw) as { u?: string; items?: unknown } | null
    if (!saved || typeof saved !== 'object' || saved.u !== username) return []
    if (!Array.isArray(saved.items)) return []

    const items: CronFavorite[] = []
    // 逐项校验：手改坏的 JSON、旧版本写的结构只丢掉坏的那一条，不让整份收藏作废
    for (const entry of saved.items) {
      const item = entry as Partial<CronFavorite> | null
      if (!item || typeof item !== 'object') continue
      const name = typeof item.name === 'string' ? item.name.trim() : ''
      const expression = typeof item.expression === 'string' ? item.expression.trim() : ''
      if (!name || !expression) continue
      items.push({ name, expression })
      if (items.length >= MAX_CRON_FAVORITES) break
    }
    return items
  } catch {
    return []
  }
}

function persistCronFavorites(username: string, items: CronFavorite[]) {
  if (typeof window === 'undefined') {
    return
  }
  try {
    window.localStorage.setItem(CRON_FAVORITES_STORAGE_KEY, JSON.stringify({ u: username, items }))
  } catch {
    // 写不进去只影响「下次进来还记不记得」，本次会话内照常生效，不打扰用户
  }
}

const authStore = useAuthStore()
const { dialogFullscreen } = useResponsive()

const templates = ref<any[]>([])
const rules = ref<CronRuleState[]>([])
const showAllTemplates = ref(false)
const activeTemplateRuleIndex = ref(0)
const showRandomDialog = ref(false)
const activeRandomRuleIndex = ref(0)
const showVisualDialog = ref(false)
const activeVisualRuleIndex = ref(0)
/** 传给可视化弹窗的初值。只在打开那一刻取一次快照，弹窗内部自己反解 */
const visualExpression = ref('')
const favoriteNameInput = ref('')

/** 拿不到用户名时用空串——它与任何存档里的名字都对不上，等价于「没有收藏」 */
function currentUsername() {
  return authStore.user?.username || ''
}

const favorites = ref<CronFavorite[]>(readStoredCronFavorites(currentUsername()))

let nextRuleId = 1

/**
 * 每条规则各自一把解析定时器，按 `rule.id` 存而不是数组下标。
 *
 * 两个原因，缺一不可：
 * 1. 随机生成会一次插入 N 条、删除又会让后面的下标整体前移，用下标做键会把定时器挂到别的规则上；
 * 2. 只用一把共享定时器的话，生成 N 条时后一条会把前一条的解析请求顶掉，
 *    用户只看得到最后一条的人话描述和下次执行时间。
 */
const parseTimers = new Map<number, ReturnType<typeof setTimeout>>()

function createRule(expression = ''): CronRuleState {
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
    : [createRule(DEFAULT_CRON_DAILY_MIDNIGHT)]

  // forEach 是在 rules.value（reactive 数组代理）上遍历的，回调拿到的每一项都已被 get 陷阱
  // 包成代理，所以这里写 parseResult 会正常触发重渲染，不需要再按下标取一次
  rules.value.forEach(rule => {
    void parseRule(rule)
  })
}

watch(
  () => props.modelValue,
  (value) => {
    const incoming = joinExpressions(splitExpressions(value))
    const current = joinExpressions(rules.value.map(rule => rule.expression))
    if (incoming === current) {
      return
    }
    syncRulesFromModel(value)
  },
  { immediate: true }
)

onBeforeUnmount(() => {
  parseTimers.forEach(timer => clearTimeout(timer))
  parseTimers.clear()
})

async function loadTemplates() {
  if (templates.value.length > 0) {
    return
  }
  try {
    const res = await taskApi.cronTemplates()
    templates.value = res || []
  } catch {
    templates.value = []
  }
}

void loadTemplates()

function emitRules() {
  emit('update:modelValue', joinExpressions(rules.value.map(rule => rule.expression)))
}

async function parseRule(rule: CronRuleState) {
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

/** 打字时走防抖，避免每敲一个字符就打一次解析请求 */
function scheduleParseRule(rule: CronRuleState) {
  const pending = parseTimers.get(rule.id)
  if (pending) {
    clearTimeout(pending)
  }
  parseTimers.set(rule.id, setTimeout(() => {
    parseTimers.delete(rule.id)
    void parseRule(rule)
  }, PARSE_DEBOUNCE))
}

function handleRuleInput(rule: CronRuleState) {
  emitRules()
  scheduleParseRule(rule)
}

function addRule(afterIndex = rules.value.length - 1) {
  // 钳制插入下标：splice 允许越界（自动落到首/末尾），越界时按 afterIndex + 1 取回来的会是 undefined
  const insertIndex = Math.min(Math.max(afterIndex + 1, 0), rules.value.length)
  rules.value.splice(insertIndex, 0, createRule(DEFAULT_CRON_DAILY_MIDNIGHT))
  emitRules()
  // 必须从 rules.value 把这条取回来再解析：createRule 返回的是裸对象，splice 存进去时
  // 并不会给它套上 reactive 代理（只有读 rules.value[i] 时才由 get 陷阱包一层）。
  // 直接拿裸对象去 parseRule，`rule.parseResult = ...` 会绕过 set 陷阱 —— 值确实写进去了，
  // 但不触发重渲染，新增那条的描述徽标和「下次执行」永远不出现，看着像后端没返回。
  const inserted = rules.value[insertIndex]
  if (inserted) {
    void parseRule(inserted)
  }
}

function removeRule(index: number) {
  if (rules.value.length <= 1) {
    return
  }
  const [removed] = rules.value.splice(index, 1)
  // 连同它挂着的防抖定时器一起清掉，否则 300ms 后还会为一条已经不存在的规则发请求
  if (removed) {
    const pending = parseTimers.get(removed.id)
    if (pending) {
      clearTimeout(pending)
      parseTimers.delete(removed.id)
    }
  }
  emitRules()
}

function openTemplateDialog(index: number) {
  activeTemplateRuleIndex.value = index
  favoriteNameInput.value = ''
  showAllTemplates.value = true
}

function selectTemplate(expression: string) {
  const rule = rules.value[activeTemplateRuleIndex.value]
  if (!rule) {
    return
  }
  rule.expression = expression
  emitRules()
  void parseRule(rule)
  showAllTemplates.value = false
}

function openRandomDialog(index: number) {
  activeRandomRuleIndex.value = index
  showRandomDialog.value = true
}

/**
 * 应用随机生成的一组表达式。
 *
 * 第一条写回当前这条规则，其余依次插在它后面：这样用户在第 N 条点「随机时间」
 * 也只会影响自己这一条及新插入的几条，不会覆盖别人的规则。
 */
function applyRandomExpressions(expressions: string[]) {
  if (expressions.length === 0) {
    return
  }
  const index = activeRandomRuleIndex.value
  const target = rules.value[index]
  if (!target) {
    return
  }

  target.expression = expressions[0]!
  const extras = expressions.slice(1)
  if (extras.length > 0) {
    rules.value.splice(index + 1, 0, ...extras.map(expression => createRule(expression)))
  }
  emitRules()
  // 这是用户的明确动作，立刻解析给出描述和下次执行时间，不走 300ms 防抖。
  // 每条规则各自一个请求、各自写回自己的 parseResult，互不覆盖。
  //
  // target 是从 rules.value 取的，本身就是 reactive 代理；但新插入的这几条必须同样
  // 按下标从 rules.value 取回代理，不能直接用 createRule 的返回值：裸对象上的
  // `rule.parseResult = ...` 绕过 set 陷阱、不触发重渲染。否则会变成 target 触发、
  // extras 不触发的竞态——只要 target 的响应先到，那次渲染时 extras 还是 null，
  // 之后 extras 落地也不再触发，第 2..N 条就永远空着。
  void parseRule(target)
  for (let offset = 0; offset < extras.length; offset++) {
    const inserted = rules.value[index + 1 + offset]
    if (inserted) {
      void parseRule(inserted)
    }
  }
}

function openVisualDialog(index: number) {
  activeVisualRuleIndex.value = index
  // 现场取当前表达式，不在 CronRuleState 上挂可视化状态：
  // 规则行的唯一事实来源始终是那串表达式，用户手改过再打开也必须看到手改后的状态
  visualExpression.value = rules.value[index]?.expression || ''
  showVisualDialog.value = true
}

/**
 * 应用可视化编辑的结果。
 *
 * 三步与 selectTemplate 完全一致，缺一不可：写回表达式 -> emitRules() 上报父组件 ->
 * 立刻解析拿后端的人话描述和下次执行时间。漏 emitRules 会「改了但没上报」，
 * 漏 parseRule 会「描述停在改之前」。
 * 这里必须从 rules.value 按下标取回代理对象（本文件另两处已踩过这个坑）：
 * 裸对象上的 `rule.parseResult = ...` 绕过 set 陷阱，值写进去了但不触发重渲染。
 */
function applyVisualExpression(expression: string) {
  const rule = rules.value[activeVisualRuleIndex.value]
  if (!rule) {
    return
  }
  rule.expression = expression
  emitRules()
  void parseRule(rule)
}

function isFavorited(expression: string) {
  return favorites.value.some(item => item.expression === expression)
}

function saveFavorites() {
  persistCronFavorites(currentUsername(), favorites.value)
}

/** 模板卡片右上角的星标：已收藏就取消，未收藏就加进「我的常用」 */
function toggleFavorite(name: string, expression: string) {
  const index = favorites.value.findIndex(item => item.expression === expression)
  if (index >= 0) {
    favorites.value.splice(index, 1)
    saveFavorites()
    return
  }
  if (favorites.value.length >= MAX_CRON_FAVORITES) {
    ElMessage.warning(`最多只能收藏 ${MAX_CRON_FAVORITES} 条常用规则`)
    return
  }
  favorites.value.push({ name: name || expression, expression })
  saveFavorites()
}

function removeFavorite(index: number) {
  favorites.value.splice(index, 1)
  saveFavorites()
}

async function renameFavorite(index: number) {
  const item = favorites.value[index]
  if (!item) {
    return
  }
  try {
    const res = await ElMessageBox.prompt('给这条常用规则起个名字', '重命名', {
      inputValue: item.name,
      confirmButtonText: '保存',
      cancelButtonText: '取消',
      inputValidator: (value: string) => (value || '').trim() ? true : '名称不能为空'
    })
    item.name = String(res.value || '').trim()
    saveFavorites()
  } catch {
    // 用户点了取消或按了 ESC，什么都不做
  }
}

/** 弹窗底部那一行：把当前正在编辑的表达式存成常用 */
function saveCurrentAsFavorite() {
  const rule = rules.value[activeTemplateRuleIndex.value]
  const expression = (rule?.expression || '').trim()
  if (!expression) {
    ElMessage.warning('当前规则还是空的，先填一个表达式再收藏')
    return
  }
  if (isFavorited(expression)) {
    ElMessage.info('这条表达式已经在「我的常用」里了')
    return
  }
  if (favorites.value.length >= MAX_CRON_FAVORITES) {
    ElMessage.warning(`最多只能收藏 ${MAX_CRON_FAVORITES} 条常用规则`)
    return
  }
  // 不填名字就用表达式本身当名字，省得为了收藏还得先想个名称
  favorites.value.push({ name: favoriteNameInput.value.trim() || expression, expression })
  favoriteNameInput.value = ''
  saveFavorites()
  ElMessage.success('已存为常用')
}

function handleKeyDown(event: KeyboardEvent) {
  if (event.key === ' ') {
    event.stopPropagation()
  }
}

const groupedTemplates = computed(() => {
  const groups: Record<string, any[]> = {}
  for (const template of templates.value) {
    if (!groups[template.category]) {
      groups[template.category] = []
    }
    groups[template.category]!.push(template)
  }
  return groups
})
</script>

<template>
  <div class="cron-input">
    <!--
      每条规则一个块：输入框 + 自己的解析结果（人话描述 / 下次执行 / 错误）+ 自己的入口按钮。
      信息区以前只渲染 rules[0]，随机生成出 3 条后用户只看得到第 1 条的描述，
      第 2..N 条既没有校验反馈也没有「常用规则」入口。
    -->
    <div
      v-for="(rule, index) in rules"
      :key="rule.id"
      class="cron-rule-block"
    >
      <div class="cron-rule-row">
        <el-input
          v-model="rule.expression"
          placeholder="cron 表达式 (秒 分 时 日 月 周, 如: 0 */5 * * * *)"
          clearable
          @keydown="handleKeyDown"
          @input="handleRuleInput(rule)"
        />
        <div class="cron-rule-actions">
          <el-button v-if="index === 0" class="cron-add-btn" size="small" @click="addRule()">
            增加定时规则
          </el-button>
          <el-button
            v-else
            text
            size="small"
            class="cron-remove-btn"
            @click="removeRule(index)"
          >
            删除
          </el-button>
        </div>
      </div>

      <!--
        段位含义只在第一条下面标一次。
        必须标出来：随机生成和出厂预设给的都是 6 段，而 5 段同样合法 —— 少写/多写一段会让
        每一段的含义整体平移，表达式照样能保存、照样能调度，但实际触发时间完全不是用户以为的那个。
      -->
      <div v-if="index === 0" class="cron-field-hint">
        6 段依次是 <code>秒 分 时 日 月 周</code>，5 段则是 <code>分 时 日 月 周</code>。
        少写一段会让所有段的含义整体前移：6 段的 <code>0 5 * * * *</code> 是「每小时第 5 分钟」，
        5 段的 <code>0 5 * * *</code> 却是「每天 05:00」。不支持 <code>@daily</code>、<code>@every 1h</code> 这类简写。
      </div>

      <div class="cron-info">
        <div class="cron-meta">
          <template v-if="rule.parseResult?.is_valid">
            <div class="valid-badge">
              <el-icon class="badge-icon"><CircleCheck /></el-icon>
              <span class="badge-text">{{ rule.parseResult.description }}</span>
            </div>
            <div v-if="rule.parseResult.next_run_times?.length" class="next-times">
              <el-icon class="time-icon"><Clock /></el-icon>
              <span class="label">下次执行</span>
              <span class="time-value">{{ formatDateTime(rule.parseResult.next_run_times[0]) }}</span>
            </div>
          </template>
          <div v-else-if="rule.parseResult" class="error-badge">
            <el-icon class="badge-icon"><CircleClose /></el-icon>
            <span class="badge-text">{{ rule.parseResult.error }}</span>
          </div>
          <el-button text size="small" @click="openTemplateDialog(index)">常用规则</el-button>
          <el-button text size="small" @click="openRandomDialog(index)">随机时间</el-button>
          <el-button text size="small" @click="openVisualDialog(index)">可视化编辑</el-button>
        </div>
      </div>
    </div>

    <el-dialog
      v-model="showAllTemplates"
      title="选择定时规则"
      width="700px"
      :fullscreen="dialogFullscreen"
      :lock-scroll="false"
      :close-on-click-modal="false"
    >
      <div class="cron-templates-dialog">
        <!-- 「我的常用」置顶：出厂那 21 条是通用预设，用户自己收藏的才是他的高频项 -->
        <div v-if="favorites.length > 0" class="template-group">
          <div class="group-header">
            <div class="group-title">我的常用</div>
            <div class="group-count">{{ favorites.length }} 个规则</div>
          </div>
          <div class="group-items">
            <div
              v-for="(item, favoriteIndex) in favorites"
              :key="`favorite-${favoriteIndex}-${item.expression}`"
              class="template-item"
              :class="{ active: rules[activeTemplateRuleIndex]?.expression === item.expression }"
              @click="selectTemplate(item.expression)"
            >
              <div class="item-header">
                <span class="item-name">{{ item.name }}</span>
                <div class="item-tools">
                  <!-- @click.stop：工具图标不应该顺带把这条规则选中 -->
                  <el-icon class="tool-icon" title="重命名" @click.stop="renameFavorite(favoriteIndex)">
                    <Edit />
                  </el-icon>
                  <el-icon class="tool-icon is-danger" title="从常用中删除" @click.stop="removeFavorite(favoriteIndex)">
                    <Delete />
                  </el-icon>
                </div>
              </div>
              <div class="item-expr">{{ item.expression }}</div>
            </div>
          </div>
        </div>

        <div v-for="(items, category) in groupedTemplates" :key="category" class="template-group">
          <div class="group-header">
            <div class="group-title">{{ category }}</div>
            <div class="group-count">{{ items.length }} 个规则</div>
          </div>
          <div class="group-items">
            <div
              v-for="template in items"
              :key="`${category}-${template.expression}`"
              class="template-item"
              :class="{ active: rules[activeTemplateRuleIndex]?.expression === template.expression }"
              @click="selectTemplate(template.expression)"
            >
              <div class="item-header">
                <span class="item-name">{{ template.name }}</span>
                <div class="item-tools">
                  <el-icon
                    class="tool-icon"
                    :class="{ 'is-starred': isFavorited(template.expression) }"
                    :title="isFavorited(template.expression) ? '从常用中移除' : '收藏到「我的常用」'"
                    @click.stop="toggleFavorite(template.name, template.expression)"
                  >
                    <Star />
                  </el-icon>
                  <el-icon
                    v-if="rules[activeTemplateRuleIndex]?.expression === template.expression"
                    class="check-icon"
                  >
                    <Check />
                  </el-icon>
                </div>
              </div>
              <div class="item-expr">{{ template.expression }}</div>
            </div>
          </div>
        </div>
      </div>

      <template #footer>
        <div class="template-footer">
          <el-input
            v-model="favoriteNameInput"
            size="small"
            maxlength="20"
            placeholder="名称（可留空，默认用表达式）"
          />
          <el-button size="small" @click="saveCurrentAsFavorite">把当前表达式存为常用</el-button>
          <!-- 移动端全屏时补一个「关闭」（v3.3.1，issue #143 F2）：这个弹窗 close-on-click-modal=false，
               而有 footer 的弹窗在 ≤768 下 EP 自带的右上角 × 会被 global.scss 隐藏，不补就只能靠选一条规则才能退出。
               放进现有的 .template-footer 而不另起一栏；尺寸跟同一栏的输入框、按钮一致用 small。桌面不渲染。 -->
          <el-button
            v-if="dialogFullscreen"
            size="small"
            class="template-footer__close"
            @click="showAllTemplates = false"
          >关闭</el-button>
        </div>
      </template>
    </el-dialog>

    <RandomCronDialog v-model:visible="showRandomDialog" @apply="applyRandomExpressions" />

    <CronVisualDialog
      v-model:visible="showVisualDialog"
      :expression="visualExpression"
      @apply="applyVisualExpression"
    />
  </div>
</template>

<style scoped lang="scss">
.cron-input {
  width: 100%;
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.cron-rule-block {
  padding: 0;
}

.cron-rule-row {
  display: flex;
  gap: 10px;
  align-items: stretch;
}

.cron-rule-actions {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-shrink: 0;
}

.cron-add-btn {
  flex-shrink: 0;
}

.cron-remove-btn {
  padding-left: 0;
  padding-right: 0;
}

.cron-field-hint {
  margin-top: 6px;
  font-size: 12px;
  color: var(--dd-text-2);
  line-height: 1.6;

  code {
    padding: 0 4px;
    background: var(--el-fill-color-light);
    font-family: var(--dd-font-mono);
    font-size: 11px;
  }
}

.cron-info {
  margin-top: 10px;
  display: flex;
  flex-direction: column;
  font-size: 13px;
}

.cron-meta {
  display: flex;
  align-items: center;
  justify-content: flex-start;
  gap: 10px;
  flex-wrap: wrap;
}

.valid-badge {
  display: inline-flex;
  width: fit-content;
  align-self: flex-start;
  align-items: center;
  gap: 4px;
  padding: 4px 10px;
  // 实心状态底读 *-solid（亮色主题压深过一档），文字配 --dd-on-status
  background: var(--dd-ok-solid);
  // 与下面的 .error-badge 是同一槽位互斥的两态，统一走 control 档：
  // error 那条会折成多行，胶囊档在多行块上会变成一个大椭圆，两态之间形状还会跳。
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
  // 段数不合法那条错误是几十个汉字的长句，不限宽会一路撑出表单（TaskForm 弹窗去掉标签后
  // 只剩 500px 左右可用）。限到 100% 让它在容器内折行，行高放宽保证多行时读得动。
  max-width: 100%;
  align-self: flex-start;
  // 折成多行后图标不该跟着整块垂直居中，锚在第一行行首才对得上文字
  align-items: flex-start;
  gap: 4px;
  padding: 5px 10px;
  background: var(--dd-err-solid);
  // 会折行的多行提示块 → control 档（不用 pill：多行时胶囊两端会撑成半圆）
  border-radius: var(--dd-radius-control);
  color: var(--dd-on-status);
  font-weight: 500;
  line-height: 1.6;

  .badge-icon {
    font-size: 14px;
    // 图标 14px、文字 12px×1.6≈19px 行高，往下压 2px 才和第一行文字视觉齐平
    margin-top: 2px;
    flex-shrink: 0;
  }

  .badge-text {
    font-size: 12px;
    line-height: 1.6;
  }
}

.next-times {
  display: flex;
  align-items: center;
  gap: 5px;
  padding: 4px 10px;
  background: color-mix(in srgb, var(--dd-accent) 10%, var(--dd-surface-solid));
  // 「下次执行」信息小块 → control 档，与旁边的 .valid-badge 同档
  border-radius: var(--dd-radius-control);
  color: var(--dd-accent-ink);
  font-weight: 500;
  border: 1px solid color-mix(in srgb, var(--dd-accent) 30%, var(--dd-surface-solid));

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

.cron-templates-dialog {
  max-height: 65vh;
  overflow-y: auto;
  padding: 4px;
}

.template-group {
  margin-bottom: 24px;

  &:last-child {
    margin-bottom: 0;
  }
}

.group-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 12px;
  padding: 10px 12px;
  background: var(--el-fill-color-light);
  // 模板弹窗里的分组标题条，是一块独立的容器类表面 → surface 档，与下面的 .template-item 同档
  border-radius: var(--dd-radius-surface);
  border-left: 4px solid var(--dd-accent);
}

.group-title {
  font-size: 14px;
  font-weight: 600;
  color: var(--dd-text);
}

.group-count {
  font-size: 12px;
  color: var(--dd-text-2);
  background: var(--dd-surface-solid);
  padding: 2px 8px;
  // 分组里的模板条数，是计数角标 → pill 档
  border-radius: var(--dd-radius-pill);
}

.group-items {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(210px, 1fr));
  gap: 12px;
}

.template-item {
  padding: 12px;
  border: 1px solid var(--el-border-color-light);
  // 模板卡片 → surface 档
  border-radius: var(--dd-radius-surface);
  background: var(--dd-surface-solid);
  cursor: pointer;
  transition: border-color 0.2s ease, background-color 0.2s ease;

  &:hover {
    border-color: color-mix(in srgb, var(--dd-accent) 50%, var(--dd-surface-solid));
  }

  &.active {
    border-color: var(--dd-accent);
    background: color-mix(in srgb, var(--dd-accent) 10%, var(--dd-surface-solid));
  }
}

.item-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  margin-bottom: 8px;
}

.item-name {
  font-size: 13px;
  font-weight: 600;
  color: var(--dd-text);
}

.item-tools {
  display: flex;
  align-items: center;
  gap: 6px;
  flex-shrink: 0;
}

.tool-icon {
  color: var(--dd-text-3);
  cursor: pointer;
  transition: color var(--dd-motion-fast) var(--dd-ease-standard);

  &:hover {
    color: var(--dd-accent-ink);
  }

  &.is-starred {
    color: var(--dd-warn-ink);
  }

  &.is-danger:hover {
    color: var(--dd-err-ink);
  }
}

.check-icon {
  color: var(--dd-accent-ink);
}

.item-expr {
  font-family: var(--dd-font-mono);
  font-size: 12px;
  color: var(--dd-text-2);
  word-break: break-all;
}

.template-footer {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: 10px;
  flex-wrap: wrap;

  // 锚点选自己模板里的 div 再 :deep 进去，避免「给 el-input 挂 class 但 scoped 命中不到」这类静默失效
  :deep(.el-input) {
    width: 220px;
  }
}

@media (max-width: 768px) {
  .cron-rule-row {
    flex-direction: column;
  }

  .cron-rule-actions {
    width: 100%;
    justify-content: flex-end;
  }

  .cron-add-btn {
    width: 100%;
  }

  .group-items {
    grid-template-columns: 1fr;
  }

  .template-footer {
    justify-content: stretch;

    :deep(.el-input) {
      width: 100%;
    }

    // 输入框独占第一行后，「存为常用」与「关闭」落在第二行：margin-left:auto 把「关闭」推到右下角，
    // 与其它弹窗的关闭按钮同一个位置。两个类叠加（scoped 后 (0,3,0)）是为了压过 EP 的
    // `.el-button + .el-button { margin-left: 12px }`（(0,2,0)，懒注入顺序不定），否则 auto 会被它顶掉。
    .template-footer__close {
      margin-left: auto;
    }
  }
}
</style>
