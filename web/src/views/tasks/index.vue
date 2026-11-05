<script setup lang="ts">
import { ref, onMounted, onBeforeUnmount, onActivated, computed, watch, nextTick } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { taskApi } from '@/api/task'
import { depsApi, type PythonRuntimeInfo } from '@/api/deps'
import { useAuthStore } from '@/stores/auth'
import { ElMessage, ElMessageBox } from 'element-plus'
// 全局注册的图标里没有 WarningFilled / InfoFilled，这里按仓库既有做法（settings/SystemHealthCard.vue 同款）局部引入
import { InfoFilled, WarningFilled } from '@element-plus/icons-vue'
// 移动端工具栏 / 批量栏的按钮走 el-button 的 :icon prop，要的是组件对象而不是全局注册名；
// PriceTag 全局也没注册（本期约定不改 main.ts）。与全局同名的几个在模板里解析到的是同一个组件，桌面端不受影响。
// Bell 是移动端批量栏「通知」按钮用的（issue #149），同样按这个办法局部引入。
import { Sort, Plus, CircleCheck, CircleClose, VideoPlay, VideoPause, PriceTag, Top, Delete, Close, Bell } from '@element-plus/icons-vue'
import TaskForm from './components/TaskForm.vue'
import LogViewer from './components/LogViewer.vue'
import TaskDetail from './components/TaskDetail.vue'
import LogFileBrowser from './components/LogFileBrowser.vue'
import ViewManager from './components/ViewManager.vue'
import TaskCronList from './components/TaskCronList.vue'
import { DEFAULT_CRON_DAILY_MIDNIGHT } from './components/CronInput.vue'
import BatchAddLabelDialog from './components/BatchAddLabelDialog.vue'
import BatchNotifyDialog from './components/BatchNotifyDialog.vue'
import TaskDeleteDialog from './components/TaskDeleteDialog.vue'
import DdSplitButton from '@/components/ui/DdSplitButton.vue'
import type { SplitButtonItem } from '@/components/ui/DdSplitButton.vue'
import DdMoreMenu from '@/components/ui/DdMoreMenu.vue'
import { getDisplayTaskLabels, classifyDisplayTaskLabels, isTaskSwitchOn } from './taskLabels'
import type { DisplayTaskLabelKind } from './taskLabels'
import { splitTaskCommandDisplay } from './taskCommand'
import { usePageActivity } from '@/composables/usePageActivity'
import { useResponsive } from '@/composables/useResponsive'
import { canOperate } from '@/utils/roles'
import { formatDuration } from '@/utils/duration'
import { scrollListToTop } from '@/utils/scrollToTop'
import {
  TASKS_PAGE_SIZE_OPTIONS,
  ensureListPreferencesLoaded,
  readListPreference,
  setListPreference,
} from '@/utils/listPreferences'
import type { TaskViewFilter, TaskViewSortRule } from '@/api/taskView'

const route = useRoute()
const router = useRouter()
const authStore = useAuthStore()
const { isMobile, width: viewportWidth } = useResponsive()
// 窄桌面：还没到移动端卡片布局，但表格已经装不下全部列。
// 1600 这个阈值来自实测：固定宽列合计 930px，加上三个弹性列的 min-width 230px 共 1160px，
// 再算上侧栏 218px 与 .layout-main 的左右 20px 内边距，窗口宽 <1600 时弹性列就开始被压到下限，
// 命令/标签/定时规则接连换行，行高从 ~50px 涨到 100~190px（1280×720 下只剩 3~4 行可见）。
// 典型受害者是 14 寸笔记本 1920×1080 + 150% 系统缩放 = 等效 1280×720。
// v3.3.8（issue #163）把「最后运行」「下次运行」两列（各 160）合成一列「运行时间」（184），宽桌面固定列少了 136px；
// 阈值没跟着重算，仍按 1600 切换——窄桌面要省的主要是「耗时」与名称标签，这条线本身没变。
const isNarrowDesktop = computed(() => !isMobile.value && viewportWidth.value < 1600)
const { isPageActive } = usePageActivity()
let statusTimer: ReturnType<typeof setInterval> | null = null
// 列表请求序号闸。loadTasks 在整页十几处被调用（切视图 / 分组、翻页、筛选、排序、增删改后重拉、keep-alive 回页……），
// 几次请求可能同时在飞，返回顺序没有保证；原来是「后到者赢」，快速连点标签时列表会显示旧筛选的结果、
// 高亮却停在新标签上。现在每次 loadTasks 自增序号，只有最新一次能写回 tasks / total；
// 3 秒状态轮询与运行后的回读不自增，只在发请求时记下序号，飞行期间一旦有新的 loadTasks 发出就丢弃自己的结果。
let loadTasksSeq = 0

// 每页条数的可选档位（v3.3.1 起跟随账户，读写都经 utils/listPreferences.ts，本页不再直接碰 localStorage）。
// 展开成可变数组：el-pagination 的 page-sizes 声明的是 number[]，只读数组直接传会被 vue-tsc 拒绝。
const taskPageSizeOptions = [...TASKS_PAGE_SIZE_OPTIONS]

// 任务名后面那一排标签的分项显隐偏好（工具栏「显示设置」下拉）。
// 四项默认全 true = 与改造前完全一致，老用户升级后第一眼一个标签都不会少。
const TASK_NAME_LABELS_STORAGE_KEY = 'dd:tasks:name_labels'

type TaskNameLabelPrefs = { subscription: boolean; group: boolean; custom: boolean; type: boolean }

const TASK_NAME_LABEL_KEYS = ['subscription', 'group', 'custom', 'type'] as const

function defaultTaskNameLabelPrefs(): TaskNameLabelPrefs {
  return { subscription: true, group: true, custom: true, type: true }
}

function readStoredTaskNameLabelPrefs(): TaskNameLabelPrefs {
  const prefs = defaultTaskNameLabelPrefs()
  if (typeof window === 'undefined') {
    return prefs
  }
  // 隐私模式 / 禁用站点存储时 localStorage 会直接抛错，不兜住会让整个 setup 挂掉、页面白掉。
  try {
    const raw = window.localStorage.getItem(TASK_NAME_LABELS_STORAGE_KEY)
    if (!raw) return prefs
    const parsed = JSON.parse(raw)
    if (!parsed || typeof parsed !== 'object') return prefs
    // 逐项校验、只认布尔值：手改坏的 JSON 或以后新增的键都各自回落到默认的 true，不会整份作废
    for (const key of TASK_NAME_LABEL_KEYS) {
      if (typeof parsed[key] === 'boolean') prefs[key] = parsed[key]
    }
  } catch {
    return defaultTaskNameLabelPrefs()
  }
  return prefs
}

function persistTaskNameLabelPrefs(value: TaskNameLabelPrefs) {
  if (typeof window === 'undefined') {
    return
  }
  try {
    window.localStorage.setItem(TASK_NAME_LABELS_STORAGE_KEY, JSON.stringify(value))
  } catch {
    // 写不进去只影响「下次进来还记不记得」，本次会话内的显示效果照常生效，不打扰用户
  }
}

const tasks = ref<any[]>([])
const total = ref(0)
const page = ref(1)
// 先用本机缓存同步起步（首屏不能等网络），服务端值由 onMounted 里的 applyAccountPageSize 异步补上。
// 显式标成 number：分页器的 v-model 回写的是 number，不是 10|20|50|100 的字面量联合。
const pageSize = ref<number>(readListPreference('tasks_page_size'))
const keyword = ref('')
const statusFilter = ref<string>('')
const loading = ref(false)
const selectedIds = ref<number[]>([])
const selectedIdSet = computed(() => new Set(selectedIds.value))
// 桌面表格实例：勾选状态存在 el-table 内部，「取消选择」只清 selectedIds 复选框不会回弹
const taskTableRef = ref<any>(null)
// 页面根：翻页回顶（scrollListToTop）的锚点
const pageRootRef = ref<HTMLElement | null>(null)
// 视图分组栏实例：移动端「新建视图 / 视图管理」两个入口收进了工具栏的「+」菜单，要经它 expose 的方法打开
const viewManagerRef = ref<InstanceType<typeof ViewManager> | null>(null)
const nameLabelPrefs = ref<TaskNameLabelPrefs>(readStoredTaskNameLabelPrefs())
const batchLabelVisible = ref(false)
// 批量设置通知弹窗（issue #149）。它和添加标签一样直接绑 selectedIds：弹窗是模态的，开着时改不了勾选
const batchNotifyVisible = ref(false)
// 删除确认弹窗（单删与批量共用，issue #124）。taskIds 存打开那一刻的快照，而不是直接绑 selectedIds：
// 弹窗里的预览和最终提交必须针对同一批任务，不能被中途的选中变化带偏。
const deleteDialogVisible = ref(false)
const deleteDialogMode = ref<'single' | 'batch'>('single')
const deleteDialogTaskIds = ref<number[]>([])
const deleteDialogTaskName = ref('')
// push_scope 由 GET /tasks/notification-channels 下发：'bound' 表示该渠道不参与广播，
// 任务不在表单里显式选中它，它就一条通知都收不到 —— 表单需要据此打标。
const notificationChannels = ref<{ id: number; name: string; type: string; enabled: boolean; push_scope?: string }[]>([])
const defaultPythonVersion = ref('3.12')
// 任务表单的 Python 版本候选：保留后端算好的 available/message，用于标注/禁用未安装版本
const pythonRuntimeOptions = ref<PythonRuntimeInfo[]>([])
const formVisible = ref(false)
// 提交在途锁：弹窗要等请求成功才关，请求期间不锁按钮连点就会建出多条重复任务
const formSubmitting = ref(false)
const editingTask = ref<any>(null)
const prefillData = ref<any>(null)
const logViewerVisible = ref(false)
const logViewerTaskId = ref<number | null>(null)
const logViewerTaskName = ref('')
const logViewerMode = ref<'live' | 'latest'>('live')
const detailVisible = ref(false)
const detailTask = ref<any>(null)
const logFilesVisible = ref(false)
const logFilesTaskId = ref<number | null>(null)
const logFilesTaskName = ref('')
const viewFilters = ref<TaskViewFilter[]>([])
const viewSortRules = ref<TaskViewSortRule[]>([])
// 工具栏快捷排序：null 表示走后端默认排序（置顶+状态分组），非空时优先于视图自带排序。
// 「定时规则 / 运行时间」两列的表头点击排序也写在这一条状态上，不另起一份，
// 否则表头箭头与工具栏按钮文案会各说各话。
const quickSort = ref<{ field: string; direction: 'asc' | 'desc' } | null>(null)
const canOperateTasks = computed(() => canOperate(authStore.user?.role))

// ---- 拖拽排序（整套照抄 web/src/views/envs/index.vue，差异点见各处注释）----
const isDragging = ref(false)
let sortableInstance: any = null
let sortableLoader: Promise<any> | null = null
let dragPointerY = 0
let dragAutoScrollFrame = 0
let sortableInitFrame = 0

// 🔴 拖拽期间必须停轮询：这一页有 3 秒一轮的状态轮询，它会改写 tasks.value 里的行 ——
// 即便已经改成了「就地合并字段」（见 mergeTaskListInPlace），行内容一变 Vue 照样重画，
// 正在被拖的那一行会断在半空。（envs 页没有轮询，照抄时最容易漏这条。）
//
// 与 dragSortDisabledReason 里那条「有任务在跑就不能拖」不矛盾，两者只是碰巧互斥：
// 轮询要 hasRunningTasks 为真才跑，拖拽要它为假才允许，所以正常路径上「拖拽中还在轮询」已经不会发生，
// !isDragging 退化成一道兜底闸。**不要因此把它删掉**：它兜的是「先有响应在飞、再开始拖」这类时序，
// 且以后只要放松了第 4 条禁用规则（比如改成把落点映射回 list_order 序），它立刻又变成必需的。
const canPollTaskStatus = computed(
  () => hasRunningTasks.value && isPageActive.value && selectedIds.value.length === 0 && !isDragging.value
)
const desktopTableHeight = computed(() => (isMobile.value ? undefined : '100%'))

function handleViewChange(filters: TaskViewFilter[], sortRules: TaskViewSortRule[]) {
  // 应用视图时清空快捷排序，避免与视图自带排序双重作用；视图自带排序优先
  quickSort.value = null
  viewFilters.value = filters
  viewSortRules.value = sortRules
  page.value = 1
  // 快捷排序被清掉了，表头箭头也要一起收回去，否则会出现「表头还指着运行时间、实际按视图规则排」
  syncTableSortIndicator()
  void loadTasks()
}

function getTaskTypeLabel(taskType: string | null | undefined) {
  if (taskType === 'manual') return '手动运行'
  if (taskType === 'startup') return '开机运行'
  return '常规定时'
}

function getCronExpressions(task: any) {
  if (Array.isArray(task?.cron_expressions) && task.cron_expressions.length > 0) {
    return task.cron_expressions
  }
  return String(task?.cron_expression || '')
    .split(/\r?\n/)
    .map((item: string) => item.trim())
    .filter(Boolean)
}

// schedule_hint 有两种严重程度：定时任务没注册进调度器是真故障，
// 「手动运行 / 开机运行不会按定时触发」只是在陈述任务类型，本来就该如此。
// 后者用警示色天天挂着，等于喊狼来了，真出故障时反而没人看。
function isScheduleFault(task: any) {
  return (task?.task_type || 'cron') === 'cron'
}

const hasRunningTasks = computed(() => tasks.value.some(t => t.status === 2))

// 可以点表头排序的两列：「定时规则」与「运行时间」（后者按下次运行排）。表头点击与下拉选项写的是同一条 quickSort 状态，
// 这份名单用来判断「当前排序该不该在表头上画箭头」。
// last_run_at 不在名单里：「运行时间」一列同时显示上次 / 下次，表头只能对应一个排序字段，选了 next_run_at。
const taskColumnSortFields = ['cron_expression', 'next_run_at']

// 快捷排序可选项：value 为 null 表示恢复默认排序，其余对应后端 sort_rules 的单条规则。
// 「最后运行」两项是按上次运行排序的唯一入口（issue #163 起合并成「运行时间」一列，表头点击按下次运行排）；
// 「下次运行」两项与表头点击等价，留着给移动端卡片（没有表头）用。
// 「定时规则」两项（issue #151）按每天最早一次执行的时刻排（后端 cron_expression 规则 v3.3.3 起的口径），
// 与「下次运行」不同：它不随「现在几点」轮转，周一 09:00 与每天 09:00 挨在一起，方便看哪个时段扎堆；
// 手动 / 开机运行等没有时刻的任务恒排本区最后，不随升降序翻转。移动端卡片没有表头，只能从这里选。
const quickSortOptions: { key: string; label: string; value: { field: string; direction: 'asc' | 'desc' } | null }[] = [
  { key: 'default', label: '默认排序', value: null },
  { key: 'name_asc', label: '名称 A→Z', value: { field: 'name', direction: 'asc' } },
  { key: 'name_desc', label: '名称 Z→A', value: { field: 'name', direction: 'desc' } },
  { key: 'created_desc', label: '创建时间（最新优先）', value: { field: 'created_at', direction: 'desc' } },
  { key: 'created_asc', label: '创建时间（最早优先）', value: { field: 'created_at', direction: 'asc' } },
  { key: 'last_run_desc', label: '最后运行（最近优先）', value: { field: 'last_run_at', direction: 'desc' } },
  { key: 'last_run_asc', label: '最后运行（最早优先）', value: { field: 'last_run_at', direction: 'asc' } },
  { key: 'next_run_asc', label: '下次运行（最快到来）', value: { field: 'next_run_at', direction: 'asc' } },
  { key: 'next_run_desc', label: '下次运行（最晚到来）', value: { field: 'next_run_at', direction: 'desc' } },
  { key: 'cron_time_asc', label: '定时规则（每天时刻 早→晚）', value: { field: 'cron_expression', direction: 'asc' } },
  { key: 'cron_time_desc', label: '定时规则（每天时刻 晚→早）', value: { field: 'cron_expression', direction: 'desc' } },
]

// 排序字段的中文名。原来这里是 `field === 'name' ? '名称' : '创建时间'` 的三目，
// 字段一多就会把未知字段错标成「创建时间」（视图自带排序也走这个按钮文案），改成查表 + 回落。
const quickSortFieldLabels: Record<string, string> = {
  name: '名称',
  command: '命令',
  cron_expression: '定时规则',
  status: '状态',
  labels: '标签',
  subscription: '订阅',
  created_at: '创建时间',
  last_run_at: '最后运行',
  next_run_at: '下次运行',
}

// 当前选中的快捷排序项 key，用于下拉菜单高亮；未选中（默认排序）时返回 'default'
const activeQuickSortKey = computed(() => {
  if (!quickSort.value) return 'default'
  const matched = quickSortOptions.find(
    opt => opt.value && opt.value.field === quickSort.value!.field && opt.value.direction === quickSort.value!.direction,
  )
  return matched ? matched.key : 'default'
})

// 排序按钮文案：默认显示「排序」，选中后体现当前排序，如「排序：创建时间 ↓」
const quickSortButtonText = computed(() => {
  if (!quickSort.value) return '排序'
  const arrow = quickSort.value.direction === 'asc' ? '↑' : '↓'
  const fieldText = quickSortFieldLabels[quickSort.value.field] || '自定义'
  return `排序：${fieldText} ${arrow}`
})

// 选择快捷排序项：重置到第一页并以现有加载逻辑刷新（与 handleSearch 同款）
function handleQuickSortSelect(value: { field: string; direction: 'asc' | 'desc' } | null) {
  quickSort.value = value
  page.value = 1
  syncTableSortIndicator()
  void loadTasks()
}

// el-table 的 sort() 会反过来抛一次 @sort-change。同步表头箭头时用这个闸拦住那一次回调，
// 否则「下拉选排序 → 同步箭头 → 回调再发一次请求」会白发一遍列表请求。
let syncingTableSort = false

/**
 * 把 quickSort 同步到表头箭头。
 *
 * 只有「定时规则 / 运行时间」两列画得出箭头，其余排序（含默认排序、视图自带排序、按最后运行排）一律 clearSort()——
 * 不收回去的话表头会一直指着上一次点的那一列，和实际排序对不上。
 * clearSort 内部是 silent 提交，不会触发 @sort-change，可以直接调。
 * 这两列在窄桌面和常规桌面都渲染（v3.3.8 起不再有被 v-if 掉的可排序列），所以不用再判列是否存在。
 */
function syncTableSortIndicator() {
  const table = taskTableRef.value
  if (!table) return
  const field = quickSort.value?.field || ''
  if (taskColumnSortFields.includes(field)) {
    syncingTableSort = true
    table.sort?.(field, quickSort.value?.direction === 'desc' ? 'descending' : 'ascending')
    syncingTableSort = false
    return
  }
  table.clearSort?.()
}

// 点表头排序。第三次点会把 order 置空 = 取消该列排序，回落到后端默认排序（置顶 → 状态 → 手工顺序）。
function handleTableSortChange(payload: { prop?: string; order?: string | null }) {
  if (syncingTableSort) return
  const prop = payload?.prop || ''
  const order = payload?.order || ''
  if (!prop || !order) {
    quickSort.value = null
  } else {
    quickSort.value = { field: prop, direction: order === 'ascending' ? 'asc' : 'desc' }
  }
  page.value = 1
  void loadTasks()
}

/**
 * 拖拽是否可用。四种情况直接禁掉手柄（并用 tooltip 说明原因）：
 *   1) 正在按列 / 按快捷项排序 —— 展示顺序由排序字段决定，拖出来的位置刷新就弹回去；
 *   2) 当前视图自带排序规则 —— 同上；
 *   3) 列表不足两行 —— 没有可换的位置；
 *   4) 列表里有任务正在运行 —— 落点会算错，见下。
 * 观察者（没有操作权限）连拖拽列都不渲染，这里的判定只是兜底。
 *
 * 🔴 第 4 条（有任务在跑就不能拖）的由来：
 * 前端 onEnd 取的是**视觉上**紧挨落点后面那一行当 target_id，而服务端 handler/task_sort.go 是按
 * `list_order ASC, sort_order ASC, created_at DESC, id DESC` 重建兄弟序列、把 source 插到 target
 * 在**这条序列**里的下标前。改造前两边口径是同一个；v3.2.5 加了「运行中提到本区最前」之后，
 * 展示序变成了 list_order 序的一个**置换**，两边不再等价。
 * 举例（默认排序、同桶内一条在跑）：A(list_order=10, 空闲)、B(20, 运行中)、C(30, 空闲)，展示序是 [B, A, C]。
 * 把 A 拖到最上面（即拖到 B 上方）⇒ target=B ⇒ 服务端 filtered=[B(20), C(30)]、insertIndex=0 ⇒
 * 重编号得 A=10, B=20, C=30，**list_order 一个字没变** ⇒ 刷新后展示序仍是 [B, A, C]，用户等于白拖；
 * 而松手时是乐观更新，界面上 A 已经在最上面了，要到下一次真刷新才弹回去。把 C 拖到最上面则更糟：C 反而跑到最后一行。
 * 列表顶部正是最常见的落点，运行中的行现在又恒在顶部，所以命中率不低。
 *
 * ⚠️ 为什么选「禁用」而不是「把落点映射回 list_order 序」：运行中的位置本身是**临时**的，
 * 「把 A 拖到一个临时占位的行上方」这件事在语义上就是未定义的 —— 用户想要的是「排在 B 前面」
 * 还是「排在这个位置」，谁也说不准。与其猜，不如直接说清楚现在不能拖、什么时候能拖。
 * 注：状态页签「已启用 / 已禁用」走的是服务端 `status = ?` 精确匹配，运行中（status=2）会被筛掉，
 * 所以切过去就能拖，tooltip 里给的就是这条出路。
 */
const dragSortDisabledReason = computed(() => {
  if (!canOperateTasks.value) return '当前账号没有排序任务权限'
  if (quickSort.value) return '当前正在按列排序，先切回「默认排序」再拖拽'
  if (viewSortRules.value.length > 0) return '当前视图自带排序规则，切到不带排序的视图再拖拽'
  if (tasks.value.length < 2) return '至少两条任务才能拖拽排序'
  if (hasRunningTasks.value) return '运行中的任务被临时排到了最前，此时拖拽的落点会算错；等它跑完再拖，或先切到「已启用」/「已禁用」页签排'
  return ''
})
const sortableEnabled = computed(() => dragSortDisabledReason.value === '')
const dragSortHint = computed(() => dragSortDisabledReason.value || '按住拖动可调整任务顺序')

// 任务的排序桶：与后端 handler/task_query.go 的 taskSortGroup 同一口径 ——
// 启用/运行中/排队中算一组、禁用一组、其余一组。拖拽只允许在同一个桶内进行（置顶状态也要相同）。
function taskSortGroupOf(status: number) {
  if (status === 0) return 1
  if (status === 1 || status === 2 || status === 0.5) return 0
  return 2
}

function inSameTaskBucket(left: any, right: any) {
  return Boolean(left?.is_pinned) === Boolean(right?.is_pinned)
    && taskSortGroupOf(Number(left?.status)) === taskSortGroupOf(Number(right?.status))
}

function loadSortable() {
  if (!sortableLoader) {
    sortableLoader = import('sortablejs').then((mod) => mod.default)
  }
  return sortableLoader
}

function updateDragPointer(evt: any) {
  const pointerEvent = evt?.originalEvent || evt
  if (typeof pointerEvent?.clientY === 'number') {
    dragPointerY = pointerEvent.clientY
    return
  }

  const touch = pointerEvent?.touches?.[0] || pointerEvent?.changedTouches?.[0]
  if (touch && typeof touch.clientY === 'number') {
    dragPointerY = touch.clientY
  }
}

function startDragAutoScroll() {
  stopDragAutoScroll()

  const tick = () => {
    // 本页表格带 :height="100%"，EP 会把表体套进 el-scrollbar，真正滚动的是 .el-scrollbar__wrap
    // （envs 页表格没设高度、靠整页滚动，所以那边直接取 .el-table__body-wrapper）。
    // 取不到就退回 body-wrapper 本身：拿不到滚动容器时下面的 canScrollTable 判定自然为假，
    // 只剩整页滚动兜底，不会抛错。
    const bodyWrapper = (
      document.querySelector('.task-table .el-table__body-wrapper .el-scrollbar__wrap')
      || document.querySelector('.task-table .el-table__body-wrapper')
    ) as HTMLElement | null
    const tableThreshold = 72
    const viewportThreshold = 88
    const tableScrollStep = 22
    const pageScrollStep = 18

    if (bodyWrapper) {
      const rect = bodyWrapper.getBoundingClientRect()
      const canScrollTable = bodyWrapper.scrollHeight > bodyWrapper.clientHeight + 4
      if (canScrollTable) {
        if (dragPointerY < rect.top + tableThreshold && bodyWrapper.scrollTop > 0) {
          bodyWrapper.scrollTop -= tableScrollStep
        } else if (
          dragPointerY > rect.bottom - tableThreshold &&
          bodyWrapper.scrollTop + bodyWrapper.clientHeight < bodyWrapper.scrollHeight
        ) {
          bodyWrapper.scrollTop += tableScrollStep
        }
      }
    }

    if (dragPointerY < viewportThreshold) {
      window.scrollBy({ top: -pageScrollStep, behavior: 'auto' })
    } else if (dragPointerY > window.innerHeight - viewportThreshold) {
      window.scrollBy({ top: pageScrollStep, behavior: 'auto' })
    }

    dragAutoScrollFrame = window.requestAnimationFrame(tick)
  }

  dragAutoScrollFrame = window.requestAnimationFrame(tick)
}

function stopDragAutoScroll() {
  if (dragAutoScrollFrame) {
    window.cancelAnimationFrame(dragAutoScrollFrame)
    dragAutoScrollFrame = 0
  }
}

function clearQueuedSortableInit() {
  if (sortableInitFrame) {
    window.cancelAnimationFrame(sortableInitFrame)
    sortableInitFrame = 0
  }
}

function queueSortableInit() {
  if (typeof window === 'undefined') return
  if (!sortableEnabled.value) {
    if (sortableInstance) {
      sortableInstance.destroy()
      sortableInstance = null
    }
    return
  }
  clearQueuedSortableInit()
  sortableInitFrame = window.requestAnimationFrame(() => {
    sortableInitFrame = 0
    void initSortable()
  })
}

// 拖拽时把被拖行每个单元格宽度锁成固定 px，避免 forceFallback 克隆行脱离表格后列宽塌陷
function lockRowCellWidths(row: HTMLElement | null) {
  if (!row) return
  row.querySelectorAll<HTMLElement>('td').forEach((cell) => {
    const w = cell.offsetWidth
    cell.style.width = `${w}px`
    cell.style.minWidth = `${w}px`
    cell.style.maxWidth = `${w}px`
    cell.style.boxSizing = 'border-box'
  })
}

function unlockRowCellWidths(row: HTMLElement | null) {
  if (!row) return
  row.querySelectorAll<HTMLElement>('td').forEach((cell) => {
    cell.style.width = ''
    cell.style.minWidth = ''
    cell.style.maxWidth = ''
    cell.style.boxSizing = ''
  })
}

async function initSortable() {
  if (sortableInstance) {
    sortableInstance.destroy()
    sortableInstance = null
  }
  // 移动端渲染的是卡片列表、压根没有表格，本轮拖拽排序只做桌面表格
  if (isMobile.value || loading.value || !sortableEnabled.value) return
  const el = document.querySelector('.task-table .el-table__body-wrapper tbody')
  if (!el) return
  try {
    const Sortable = await loadSortable()
    sortableInstance = Sortable.create(el as HTMLElement, {
      animation: 220,
      easing: 'cubic-bezier(0.22, 1, 0.36, 1)',
      ghostClass: 'sortable-ghost',
      chosenClass: 'sortable-chosen',
      dragClass: 'sortable-drag',
      forceFallback: true,
      fallbackOnBody: true,
      scroll: true,
      bubbleScroll: true,
      scrollSensitivity: 100,
      scrollSpeed: 18,
      handle: '.task-drag-handle',
      delay: 0,
      touchStartThreshold: 4,
      onChoose: (evt: any) => {
        lockRowCellWidths(evt.item)
      },
      onUnchoose: (evt: any) => {
        unlockRowCellWidths(evt.item)
      },
      onStart: (evt: any) => {
        // 置位后 canPollTaskStatus 立刻变假，watch 会把 3 秒轮询停掉（理由见 canPollTaskStatus 的注释）
        isDragging.value = true
        lockRowCellWidths(evt.item)
        updateDragPointer(evt)
        startDragAutoScroll()
      },
      onMove: (evt: any) => {
        updateDragPointer(evt)
      },
      onEnd: async (evt: any) => {
        // 放在最前面复位：后面每一条早退分支都要恢复轮询，写在末尾会漏掉
        isDragging.value = false
        unlockRowCellWidths(evt.item)
        stopDragAutoScroll()
        updateDragPointer(evt)

        const { oldIndex, newIndex } = evt
        if (oldIndex === newIndex) return

        const sourceItem = tasks.value[oldIndex]
        if (!sourceItem) return

        // 先在本地把行挪到位（乐观更新），请求失败再 loadTasks 弹回原位。
        // 不这么做的话松手瞬间 Vue 会按旧数据把行画回原位置，等请求回来又跳一次。
        const movedItem = tasks.value.splice(oldIndex, 1)[0]
        tasks.value.splice(newIndex, 0, movedItem)

        // 桶判定：后端只允许「置顶状态相同 + 状态分组相同」的任务互拖，跨桶回 400。
        // 落点前后两侧都不是同桶 ⇒ 真的拖进别的区了，先拦下并弹回原位；
        // 后一条同桶 ⇒ 插到它前面；否则前一条同桶 ⇒ 插到它后面（position:'after'，服务端 task_sort.go 早已支持）。
        // 🔴 「只有前一条同桶」时不能再发空 target：服务端把缺省 target 当成「移到整桶末尾」，而整桶是跨页、
        // 不看筛选的 —— 分页或筛选下把任务拖到本页（或筛选结果）底部，它会越过所有没显示出来的同桶任务，
        // 被甩到最后一页之后。两种插法都以「看得见的邻居」为锚，对整桶也成立（与环境变量页 #131 同一写法）。
        const nextRow = tasks.value[newIndex + 1]
        const prevRow = tasks.value[newIndex - 1]
        const nextSameBucket = !!nextRow && inSameTaskBucket(sourceItem, nextRow)
        const prevSameBucket = !!prevRow && inSameTaskBucket(sourceItem, prevRow)
        if (!nextSameBucket && !prevSameBucket) {
          ElMessage.warning('置顶任务与普通任务、启用与禁用任务请分别排序，跨区移动请用置顶 / 启用按钮')
          void loadTasks()
          return
        }

        try {
          if (nextSameBucket) {
            await taskApi.sort(sourceItem.id, nextRow.id)
          } else {
            await taskApi.sort(sourceItem.id, prevRow.id, 'after')
          }
        } catch (err: any) {
          ElMessage.error(err?.response?.data?.error || err?.message || '排序失败')
          void loadTasks()
        }
      }
    })
  } catch {
    ElMessage.error('拖拽排序组件加载失败')
  }
}

// 拖拽可用性或端形态变了就重挂实例：从「禁用」翻成「可用」时表格里才刚长出手柄，
// 只靠 loadTasks 末尾那一次挂载会漏掉（比如清掉列排序但不刷新列表的场景）。
watch([sortableEnabled, isMobile], () => {
  void nextTick(() => queueSortableInit())
})

// 显示设置集中在这里写回：toggleNameLabelPref 每次都整体换一个新对象，所以不需要 deep
watch(nameLabelPrefs, (value) => {
  persistTaskNameLabelPrefs(value)
})

watch(canPollTaskStatus, () => {
  syncStatusPolling()
})

// 移动端没有状态分段（全部 / 运行中 / 已禁用 / 已启用）。在桌面选了某个状态再缩到移动端，
// 列表会带着一个看不见、也关不掉的筛选，所以进入移动端时清掉它并重拉。
watch(isMobile, (mobile) => {
  if (mobile && statusFilter.value !== '') {
    statusFilter.value = ''
    handleSearch()
  }
})

// 移动端勾选不经过 el-table，列表数据一换就要把 selectedIds 裁剪到新数据里仍在的 id。
// 桌面端 el-table 在 data 换成新数组时会自己整体清空勾选（再经 selection-change 清掉 selectedIds），移动端没人管：
// 翻页后旧页的 id 留在 selectedIds 里，批量栏不消失、批量操作会作用到看不见的任务，
// canPollTaskStatus 也因此一直为假，3 秒状态轮询被卡停。
// 挂在 tasks 的整体替换上而不是只写进 loadTasks：运行后的回读、轮询合并剔除行也会换数组，一处收口。
// 只裁不清：批量启用 / 置顶之后重拉，勾着的任务都还在，接着做下一个批量操作不用重选。
watch(tasks, (rows) => {
  if (!isMobile.value || selectedIds.value.length === 0) return
  const present = new Set<number>(rows.map(row => row?.id))
  const kept = selectedIds.value.filter(id => present.has(id))
  if (kept.length !== selectedIds.value.length) {
    selectedIds.value = kept
  }
})

function buildTaskListParams() {
  const params: Record<string, string | number> = {
    page: page.value,
    page_size: pageSize.value,
  }
  if (keyword.value) params.keyword = keyword.value
  if (statusFilter.value !== '') params.status = statusFilter.value
  if (viewFilters.value.length > 0) {
    params.filters = JSON.stringify(viewFilters.value)
  }
  // sort_rules 优先级：工具栏快捷排序 > 视图自带排序；默认排序（quickSort 为 null 且无视图排序）时不传，走后端默认置顶逻辑
  if (quickSort.value) {
    params.sort_rules = JSON.stringify([quickSort.value])
  } else if (viewSortRules.value.length > 0) {
    params.sort_rules = JSON.stringify(viewSortRules.value)
  }
  return params
}

/**
 * 把一次列表响应【按 id 就地合并】进当前 tasks，不动剩余行的顺序；
 * 这一页的成员关系真的变了就返回 false，由调用方整表替换。
 *
 * 🔴 为什么轮询不能重排（issue #118）：v3.2.5 起服务端默认排序把「运行中」提到本区最前，
 * 如果 3 秒一轮的轮询照旧整表替换，点一次运行这条任务就会当场蹿到列表最上面、跑完又跳回来，
 * 一次运行跳两次 —— hlt1995 的原话是「手动点击运行，脚本直接跑最上面去了」。
 * 所以这一页把「顺序」和「状态」拆开：状态 / 上次结果 / 耗时 / 最后运行时间等字段照常每 3 秒刷新，
 * 顺序只在用户主动刷新、切筛选、搜索、翻页、增删改任务时才按新规则重排（也就是他说的「刷新后生效」）。
 * ⚠️ 后人别「顺手统一成整表替换」，那等于把这条 issue 原样退回去。
 *
 * 三条规则，缺一条就会出人命：
 *
 * 1) **响应里出现了当前列表没有的 id ⇒ 直接返回 false，交给调用方整表替换**。
 *    出现新 id 说明这一页的成员关系真的变了（别处新建、或本页某行被挤到别的页去了），
 *    此时重排是正确行为 —— 硬留在原地只会让页面停在一份已经不存在的快照上。
 *
 * 2) **配不上的行剔除掉**（服务端这一页已经没有它了：跑完被状态页签筛掉、或被别处删了）。
 *    曾经写成「配不上就原样留着」，结果是「运行中」页签下（轮询带着 status=2 一起发）
 *    任务跑完后服务端不再返回它、前端却原样留着 ⇒ 它永远挂在「运行中」页签里，
 *    而且这条陈旧的 status=2 会自锁 hasRunningTasks ⇒ canPollTaskStatus 恒为真 ⇒ 3 秒轮询永远收不掉。
 *
 * 3) **只删不插、只删不排**：剩下的行保持原有相对顺序与对象引用。
 *    插入新行必须给它选一个位置 = 重排，而重排正是 issue #118 要消灭的抖动；
 *    新任务留到下一次真正的刷新再出现即可。
 *
 * ⚠️ 已知代价（结构性，不是这里能消掉的）：v3.2.5 起服务端把运行中的任务提到本区最前，
 *    所以**在第 2 页及以后点「运行」**，那一行会被提到第 1 页 ⇒ 第 2 页的响应里挤进来一个本页没有的 id
 *    ⇒ 命中规则 1 整表替换 ⇒ 用户会看到「刚点运行的那条从本页移走了」。
 *    这是「服务端全局置顶 + 分页」的结构性后果。相比之下，
 *    「跑完的任务永远卡在运行中页签、轮询永不停止、浏览器一直空转发请求」严重得多，所以选这一边。
 *    （第 1 页不受影响：置顶只在页内挪动，id 集合没变 ⇒ 就地合并 ⇒ 不跳。）
 */
function mergeTaskListInPlace(incoming: any, incomingTotal: number): boolean {
  const rows: any[] = Array.isArray(incoming) ? incoming : []

  const rowById = new Map<number, any>()
  for (const row of rows) {
    rowById.set(Number(row?.id), row)
  }

  // 规则 1：出现当前列表没有的 id ⇒ 这一页真换内容了，让调用方整表替换（此时重排才是对的）
  const currentIds = new Set<number>(tasks.value.map(item => Number(item?.id)))
  for (const row of rows) {
    if (!currentIds.has(Number(row?.id))) return false
  }

  // 先整轮配对、再统一落字段。配不上的行不进 pairs = 被剔除（规则 2）
  const pairs: [any, any][] = []
  for (const current of tasks.value) {
    const source = rowById.get(Number(current?.id))
    if (!source) continue
    pairs.push([current, source])
  }

  for (const [current, source] of pairs) {
    // 服务端只在「有话要说」时才下发 next_run_at / schedule_hint / notification_channel_*
    // （见 handler/task_query.go 的 prepareTaskListItems），它们不是恒有字段。
    // 光靠 Object.assign 覆盖不掉「这次没下发」的键，上一轮的旧值会一直挂在行上
    // （比如任务被禁用后 next_run_at 该消失、警示图标该收回，却还留着），所以先删本次响应里没有的键。
    for (const key of Object.keys(current)) {
      if (!(key in source)) delete current[key]
    }
    // 再用服务端这条记录整体覆盖，而不是挑几个字段合：漏一个字段的表现是
    // 「那个字段在轮询里永远不刷新」，而且构建全绿、只有盯着看才发现，所以这里不做白名单。
    // 保留原对象引用 ⇒ 数组顺序、el-table 的行选中、详情弹窗持有的那份引用都不受影响。
    Object.assign(current, source)
  }

  // 规则 3：只删不排 —— 换掉数组引用只是为了让被剔除的行消失，
  // 剩下的仍是同一批对象、同一个相对顺序。长度没变说明一行都没掉，连数组都不用换。
  if (pairs.length !== tasks.value.length) {
    tasks.value = pairs.map(([current]) => current)
  }
  // total 必须跟着走：不更新的话「共 N 条数据」和分页器会长期停在旧值
  total.value = incomingTotal
  return true
}

function startStatusPolling() {
  stopStatusPolling()
  statusTimer = setInterval(async () => {
    if (!canPollTaskStatus.value) {
      stopStatusPolling()
      return
    }
    // 记下发请求这一刻的列表请求序号（见 loadTasksSeq）
    const seq = loadTasksSeq
    try {
      const res = await taskApi.list(buildTaskListParams())
      // await 之后必须再判一次：停轮询只清掉了定时器，挡不住**已经在飞**的这一次请求。
      // 拖拽开始时若上一轮 tick 的响应还在路上，回来后动 tasks.value
      // 会把正在拖的那一行重画掉，onEnd 再按下标取值就取到了对不上的数据。
      if (!canPollTaskStatus.value) return
      // 飞行期间 loadTasks 又发起了新的一次（切筛选、翻页……）：这份是按旧参数拿的，不能再写回列表
      if (seq !== loadTasksSeq) return
      // 优先就地刷字段（顺序纹丝不动），只有这一页的成员关系真变了才整表替换 ——
      // 两条路各自的理由与代价见 mergeTaskListInPlace 的注释。
      if (!mergeTaskListInPlace(res.data, res.total)) {
        tasks.value = res.data
        total.value = res.total
      }
      syncStatusPolling()
    } catch {}
  }, 3000)
}

function stopStatusPolling() {
  if (statusTimer) {
    clearInterval(statusTimer)
    statusTimer = null
  }
}

function syncStatusPolling() {
  if (canPollTaskStatus.value) {
    if (!statusTimer) {
      startStatusPolling()
    }
    return
  }
  stopStatusPolling()
}

async function loadTasks() {
  // 请求序号闸（说明见 loadTasksSeq）：记下本次序号，响应回来时已经不是最新一次就整个丢弃
  const seq = ++loadTasksSeq
  loading.value = true
  try {
    const res = await taskApi.list(buildTaskListParams())
    if (seq !== loadTasksSeq) return
    tasks.value = res.data
    total.value = res.total
    syncStatusPolling()
  } catch {
    // 过期请求的失败同样丢弃：更新的那次还在飞，这时报「加载失败」只会误导
    if (seq !== loadTasksSeq) return
    ElMessage.error('加载任务列表失败')
  } finally {
    // loading 仍是「列表请求在飞」的意思，只是改由最新一次收尾：旧请求先回来就把它置 false 的话，
    // 新请求还没回、表格已经撤掉加载态露出旧数据（initSortable 也会提前按旧行挂上拖拽实例）
    if (seq === loadTasksSeq) loading.value = false
  }

  // 行是整批换掉的，拖拽实例要跟着重挂（initSortable 里会先 destroy 旧的）
  await nextTick()
  queueSortableInit()
}

async function loadNotificationChannels() {
  try {
    const res = await taskApi.notificationChannels()
    notificationChannels.value = res.data || []
  } catch {
    notificationChannels.value = []
  }
}

async function loadDefaultPythonVersion() {
  try {
    const res = await depsApi.pythonRuntimes()
    // 保留后端已算好的 available/message，任务表单据此标注/禁用未安装版本并提示安装方式
    pythonRuntimeOptions.value = (res.data || [])
      .filter(item => ['3.10', '3.11', '3.12'].includes(item.version))
    if (['3.10', '3.11', '3.12'].includes(res.default_version)) {
      defaultPythonVersion.value = res.default_version
    }
  } catch {
    defaultPythonVersion.value = '3.12'
    pythonRuntimeOptions.value = []
  }
}

function clearTaskRouteQuery() {
  return router.replace({ path: '/tasks' })
}

async function handleRouteQueryAction() {
  if (route.query.create === '1') {
    if (!canOperateTasks.value) {
      ElMessage.warning('当前账号没有新建任务权限')
      await clearTaskRouteQuery()
      return
    }
    openCreate()
    await clearTaskRouteQuery()
    return
  }

  if (route.query.autoCreate === '1') {
    const name = route.query.name as string || ''
    const command = route.query.command as string || ''
    if (name && command) {
      if (!canOperateTasks.value) {
        ElMessage.warning('当前账号没有新建任务权限')
        await clearTaskRouteQuery()
        return
      }
      editingTask.value = null
      // 脚本页「添加到定时任务」的预填值，取值口径见 CronInput.vue 顶部那两个导出常量。
      // ⚠️ 它是「每天 00:00」，意味着任务建好后最长 24 小时都不会自己跑一次——
      // 这正是 issue #115 里「加了定时任务不执行、日志抽屉说没有日志记录」的真实处境，
      // 所以 handleFormSubmit 在创建成功后会把下次运行时间直接说出来（见那里的注释）。
      prefillData.value = { name, command, cron_expression: DEFAULT_CRON_DAILY_MIDNIGHT, task_type: 'cron' }
      formVisible.value = true
      await clearTaskRouteQuery()
    }
    return
  }

  // 「后台进程」页点任务名跳过来（#176）：按任务名搜索，把这条任务定位出来
  const locateKeyword = typeof route.query.keyword === 'string' ? route.query.keyword.trim() : ''
  if (locateKeyword) {
    keyword.value = locateKeyword
    page.value = 1
    await clearTaskRouteQuery()
    void loadTasks()
    return
  }

  const taskId = Number(route.query.task_id)
  if (taskId > 0 && route.query.action === 'run') {
    await clearTaskRouteQuery()
    if (!canOperateTasks.value) {
      ElMessage.warning('当前账号没有运行任务权限')
      return
    }
    const task = tasks.value.find(item => item.id === taskId) || { id: taskId, name: `任务#${taskId}`, status: 1 }
    await handleRun(task)
  }
}

let skipInitialActivated = true

/**
 * 每页条数跟随账户（issue #143 桌面端第 1 条）：把服务端存的值应用到本页。
 *
 * 刻意不阻塞首拉：首屏先按本机缓存拉一次，服务端值回来、且与当前不同时再回到第 1 页重拉。
 * 两次请求谁先回来都没关系，loadTasksSeq 保证最后停在最新一次上。
 * 只有换浏览器 / 换域名后第一次打开才会多这一次；之后缓存与服务端一致，这里是空操作。
 * 用户在 GET 回来之前已经亲手改过条数的话，listPreferences 会跳过这一键的下行写回，
 * 这里读到的就是他刚选的值，与当前相等，不会把他的选择弹回去。
 * onActivated 不用再调：ensure 是记忆化的，同一会话里只拉一次。
 */
function applyAccountPageSize() {
  return ensureListPreferencesLoaded().then(() => {
    const next = readListPreference('tasks_page_size')
    if (next === pageSize.value) return
    pageSize.value = next
    page.value = 1
    void loadTasks()
  })
}

onMounted(async () => {
  void applyAccountPageSize()
  await Promise.all([loadTasks(), loadNotificationChannels(), loadDefaultPythonVersion()])
  await handleRouteQueryAction()
})

onActivated(async () => {
  if (skipInitialActivated) {
    skipInitialActivated = false
    return
  }
  await Promise.all([loadTasks(), loadNotificationChannels(), loadDefaultPythonVersion()])
  await handleRouteQueryAction()
})

onBeforeUnmount(() => {
  stopStatusPolling()
  stopDragAutoScroll()
  clearQueuedSortableInit()
  if (sortableInstance) {
    sortableInstance.destroy()
    sortableInstance = null
  }
})

function handleSearch() {
  page.value = 1
  void loadTasks()
}

// 状态标签配色（issue #133）：禁用灰、排队橙、运行中绿、空闲蓝。
// 运行中的呼吸点（.pulse-dot）取 currentColor，换成 success 后自动跟着变绿；暗色由 EP 令牌自动适配。
// 任务详情弹窗（TaskDetail.vue）另有一份同色映射，改这里记得同步那边。
function getStatusType(status: number) {
  if (status === 0) return 'info'
  if (status === 0.5) return 'warning'
  if (status === 2) return 'success'
  return 'primary'
}

// 状态标签的附加 class：运行中挂呼吸点排版；空闲挂 dd-tag--idle（global.scss，契约 C6），
// 把 primary 浅色标签的边框加深一档到 primary-light-7（EP 默认 light-8），与 issue 里要的配色对上。
function getStatusTagClass(status: number) {
  if (status === 2) return 'tag-with-dot'
  if (getStatusType(status) === 'primary') return 'dd-tag--idle'
  return ''
}

function getStatusText(status: number) {
  if (status === 0) return '禁用中'
  if (status === 0.5) return '排队中'
  if (status === 2) return '运行中'
  return '空闲中'
}

function formatTime(time: string | null) {
  if (!time) return '-'
  const d = new Date(time)
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`
}

function navigateToScript(path: string) {
  router.push({ path: '/scripts', query: { file: path } })
}

// 翻页（O3，桌面与移动端都做）：数据回来、DOM 换完之后再回到顶部，避免「先滚到顶、再换内容」跳两下。
// 🔴 回顶只挂在分页器的两个事件上，不能挪进 loadTasks：轮询、增删改后的重拉都会调它，用户正往下看着会被拽回顶部。
async function reloadAndScrollToTop() {
  await loadTasks()
  await nextTick()
  // 请求期间页面被卸载时 ref 已置空：不能把 null 传进去，那样 scrollListToTop 会回落到「当前活动页」，
  // 把用户已经切过去的别的页面滚回顶部。被 keep-alive 失活时 ref 还在，由 scrollListToTop 自己判 offsetParent 挡掉。
  if (pageRootRef.value) scrollListToTop(pageRootRef.value)
}

function handlePageChange() {
  void reloadAndScrollToTop()
}

function handlePageSizeChange() {
  page.value = 1
  // 只在这个用户动作里写偏好（同步到账户）；程序化应用服务端值时不经过这里，不会多发一次 PUT。
  // 按白名单取一遍而不是断言类型：分页器给的是 number，白名单外的值（理论上不会出现）直接不存。
  const size = TASKS_PAGE_SIZE_OPTIONS.find(option => option === pageSize.value)
  if (size !== undefined) {
    setListPreference('tasks_page_size', size)
  }
  void reloadAndScrollToTop()
}

// 「已终止」（issue #133）：从橙改成比「未运行」更深的灰 —— type 仍是 info，
// 再挂 dd-tag--aborted（global.scss，契约 C6，令牌派生、暗色自动变深）。
// 不照抄 issue 里给的十六进制：写死的灰在暗色下会变成一块亮斑（design-system 硬规则 1）。
function getRunStatusType(status: number | null) {
  if (status === null) return 'info'
  if (status === 0) return 'success'
  if (status === 2) return 'info'
  return 'danger'
}

function getRunStatusClass(status: number | null) {
  return status === 2 ? 'dd-tag--aborted' : ''
}

function getRunStatusText(status: number | null) {
  if (status === null) return '未运行'
  if (status === 0) return '成功'
  if (status === 2) return '已终止'
  return '失败'
}

function displayTaskLabels(task: any) {
  if (Array.isArray(task?.display_labels) && task.display_labels.length > 0) {
    return task.display_labels
  }
  return getDisplayTaskLabels(task?.labels || [])
}

// 「显示设置」下拉的四个可勾选项。
const nameLabelOptions: { key: keyof TaskNameLabelPrefs; label: string }[] = [
  { key: 'subscription', label: '订阅标签' },
  { key: 'group', label: '分组标签' },
  { key: 'custom', label: '自定义标签' },
  { key: 'type', label: '类型标签' },
]

// 点一下只翻转一项；整体换一个新对象，所以上面的 watch 不用开 deep。
// 菜单靠 hide-on-click=false 保持展开，方便连着调好几项。
function toggleNameLabelPref(key: keyof TaskNameLabelPrefs) {
  const next = { ...nameLabelPrefs.value }
  next[key] = !next[key]
  nameLabelPrefs.value = next
}

// 三类标签的悬浮说明。订阅名与分组名重名时会并排出现两个一模一样的文字，
// 光看文案用户无法判断「显示设置」里该关哪个开关，配色 + title 一起把类别说清楚。
const taskLabelKindTitles: Record<DisplayTaskLabelKind, string> = {
  group: '分组标签',
  subscription: '订阅标签',
  custom: '自定义标签',
}

// 按「显示设置」过滤后的名称标签，返回带类别的条目（不是纯字符串）。
// 用 filter 而不是把分类结果拼回去，是为了保住后端下发的原始顺序
// （分组在最前、订阅名在最后），否则关掉一类再打开，剩下标签的先后会莫名其妙地变。
//
// ⚠️ 这里【不能】再按「三类全开就短路返回原数组」来省一次分类：
// 模板要靠 entry.kind 上色、靠 entry.key 做 :key，全开时同样需要这份分类结果。
// 判别也必须按下标走 entry.kind，不能再按字符串成员判定 —— 同名标签会串台（issue #109-3）。
function visibleTaskLabels(task: any) {
  const entries = classifyDisplayTaskLabels(displayTaskLabels(task), task?.labels || [], task?.subscription_labels)
  const prefs = nameLabelPrefs.value
  if (prefs.subscription && prefs.group && prefs.custom) return entries
  return entries.filter(entry => prefs[entry.kind])
}

function ensureCanOperate(message = '当前账号没有操作任务权限') {
  if (canOperateTasks.value) return true
  ElMessage.warning(message)
  return false
}

function openCreate() {
  if (!ensureCanOperate('当前账号没有新建任务权限')) return
  editingTask.value = null
  prefillData.value = null
  formVisible.value = true
}

function openEdit(task: any) {
  if (!ensureCanOperate('当前账号没有编辑任务权限')) return
  editingTask.value = task
  formVisible.value = true
}

function openDetail(task: any) {
  detailTask.value = task
  detailVisible.value = true
}

function openLogViewer(task: any) {
  logViewerTaskId.value = task.id
  logViewerTaskName.value = task.name
  logViewerMode.value = 'live'
  logViewerVisible.value = true
}

function openLogFiles(task: any) {
  logFilesTaskId.value = task.id
  logFilesTaskName.value = task.name
  logFilesVisible.value = true
}

async function handleFormSubmit(data: any) {
  if (!ensureCanOperate()) return
  // 权限这道早退分支过了才置位，否则没权限的用户会把按钮永久转圈
  formSubmitting.value = true
  // 新建成功后还要弹一次「下次运行时间 + 立即运行一次」，那一步要等用户点。
  // 它不能压在 try 里：finally 会被 await 挡住，创建按钮会一直转圈。
  // 所以这里只把创建结果接出来，出了 try/finally 再提示。
  let createdTask: any = null
  try {
    if (editingTask.value) {
      await taskApi.update(editingTask.value.id, data)
      ElMessage.success('任务更新成功')
    } else {
      const res = await taskApi.create(data)
      ElMessage.success('任务创建成功')
      // create 返回体的 data 是完整 task（含 id / task_type / cron_expression / next_run_at）。
      // 没拿到 id 就没法「立即运行一次」，那就退回只提示创建成功，与改造前一致。
      if (res?.data?.id) {
        createdTask = res.data
      }
    }
    formVisible.value = false
    loadTasks()
    // 分组可能改了：顺手刷新顶栏分组页签，不用刷新页面（#157；下面复制 / 删除 / 批量加标签 / 导入同理）
    void viewManagerRef.value?.loadGroups()
  } catch (err: any) {
    ElMessage.error(err?.response?.data?.error || '操作失败')
  } finally {
    formSubmitting.value = false
  }

  if (createdTask) {
    await promptRunAfterCreate(createdTask)
  }
}

/**
 * 算出新建任务的「下次运行时间」文案，算不出来返回空串。
 *
 * 走的是 POST /tasks/cron/parse，也就是 CronInput 里「下次执行」预览用的同一个接口，
 * 多条规则各解析一次取最早的那条。刻意不为此引入任何前端 cron 解析库。
 *
 * 开头那个 next_run_at 分支是为「以后 create 接口也下发它」留的：
 * 现在 create 返回的是 model.Task.ToDict()，里面没有这个字段（它只在任务列表接口里按 cron 现算），
 * 所以今天一定走下面的解析分支。绝大多数任务只有一条定时规则，也就是多发一个请求。
 */
async function resolveNextRunText(task: any): Promise<string> {
  if (task?.next_run_at) {
    return formatTime(task.next_run_at)
  }

  let earliest = ''
  for (const expression of getCronExpressions(task)) {
    try {
      const parsed = await taskApi.cronParse(expression)
      const next = parsed?.next_run_times?.[0]
      if (!next) continue
      if (!earliest || new Date(next).getTime() < new Date(earliest).getTime()) {
        earliest = next
      }
    } catch {
      // 单条解析失败不影响其它规则；全都失败就返回空串，由调用方换一套说法
    }
  }
  return earliest ? formatTime(earliest) : ''
}

/**
 * 新建任务成功后，把「下次什么时候跑」直接说出来，并给一个「立即运行一次」的入口。
 *
 * issue #115 的真实处境：用户在脚本页看完调试运行的完整输出，转手点「添加到定时任务」，
 * 预填的是每天 00:00，于是接下来最长 24 小时什么都不会发生、日志抽屉如实显示
 * 「该任务还没有日志记录」，用户就以为面板坏了。只弹一句「任务创建成功」是不够的。
 */
async function promptRunAfterCreate(task: any) {
  const taskType = task?.task_type || 'cron'
  let message = ''
  if (taskType === 'manual') {
    // 手动运行 / 开机运行本来就不按 cron 触发，算不出「下次」，直接把这件事说清楚
    message = '任务已创建。「手动运行」的任务不会自动触发，只能手动点运行。要现在先跑一次吗？'
  } else if (taskType === 'startup') {
    message = '任务已创建。「开机运行」的任务只在面板每天首次启动时自动执行一次，不会按定时触发。要现在先跑一次吗？'
  } else {
    const nextRunText = await resolveNextRunText(task)
    message = nextRunText
      ? `任务已创建，下次运行时间：${nextRunText}。要现在先跑一次吗？`
      : '任务已创建，但暂时算不出下次运行时间。要现在先跑一次吗？'
  }

  try {
    await ElMessageBox.confirm(message, '任务已创建', {
      confirmButtonText: '立即运行一次',
      cancelButtonText: '知道了',
      type: 'info'
    })
  } catch {
    // 点了「知道了」或按了 ESC：只关掉弹窗，任务已经建好了
    return
  }
  // 这里已经确认过一次，不要再走 handleRun 的二次确认，否则用户要连点两个「确认运行」
  await runTaskNow(task)
}

/**
 * 真正发起运行。二次确认由各调用点自己做：
 * 列表里的运行按钮走 handleRun（带确认），新建后的「立即运行一次」已经确认过，直接调这里。
 */
async function runTaskNow(task: any) {
  try {
    await taskApi.run(task.id)
    ElMessage.success('任务已启动，正在打开实时日志')
    task.status = 2
    openLogViewer(task)
    syncStatusPolling()
    // 🔴 这一次回读【优先只合并字段、不重排】（原来是 loadTasks() 的整表替换）。
    // 服务端默认排序会把运行中的任务提到本区最前，当场整表替换的话，点一下运行这条任务
    // 就直接蹿到列表顶（issue #118 hlt1995 反对的现象），3 秒后的轮询再把它放回去 = 一次运行跳两次。
    // 置顶效果留到用户下次主动刷新 / 切筛选时才体现，理由与轮询同源，见 mergeTaskListInPlace。
    // 只有这一页的成员关系真变了（典型是在第 2 页点运行、那一行被提到第 1 页）才整表替换，
    // 这条代价与取舍同样写在 mergeTaskListInPlace 的注释里。
    // 记下发请求这一刻的列表请求序号（见 loadTasksSeq）
    const seq = loadTasksSeq
    try {
      const res = await taskApi.list(buildTaskListParams())
      // 飞行期间 loadTasks 又发起了新的一次：这份结果已过期，交给那一次写回（它收尾时也会 syncStatusPolling）
      if (seq !== loadTasksSeq) return
      if (!mergeTaskListInPlace(res.data, res.total)) {
        tasks.value = res.data
        total.value = res.total
      }
      // 与原来 loadTasks() 末尾那次同款：万一服务端已经跑完（秒级任务），要据此把轮询收掉
      syncStatusPolling()
    } catch {
      // 回读失败不影响「已启动」这个结论本身，列表保持原样，3 秒后的状态轮询会再刷一次
    }
  } catch (err: any) {
    ElMessage.error(err?.response?.data?.error || '启动失败')
  }
}

async function handleRun(task: any) {
  if (!ensureCanOperate('当前账号没有运行任务权限')) return
  try {
    await ElMessageBox.confirm(`确认运行定时任务「${task.name}」吗？`, '运行确认', { type: 'info' })
  } catch {
    // 取消 / ESC
    return
  }
  await runTaskNow(task)
}

async function handleStop(task: any) {
  if (!ensureCanOperate('当前账号没有停止任务权限')) return
  try {
    await ElMessageBox.confirm(`确认停止定时任务「${task.name}」吗？`, '停止确认', { type: 'warning' })
    await taskApi.stop(task.id)
    ElMessage.success('任务已停止')
    // 乐观赋值按开关位还原（issue #133）：原来无条件写 1，被手动运行的禁用任务停下后会闪一下「空闲中」，
    // 等 loadTasks 回来才变回「禁用中」。服务端停止后同样按开关位结算，这里与它一致。
    task.status = isTaskSwitchOn(task) ? 1 : 0
    loadTasks()
  } catch (err: any) {
    if (err === 'cancel' || err?.toString() === 'cancel') return
    ElMessage.error(err?.response?.data?.error || '停止失败')
  }
}

async function handleToggle(task: any) {
  if (!ensureCanOperate()) return
  try {
    // 按开关位分支而不是 status === 0（issue #133）：被手动运行的禁用任务 status 是 0.5 / 2，
    // 原来会走进下面的「禁用」分支，服务端只打一个待禁用标记、回一句「当前执行结束后生效」——
    // 用户既没法在它运行期间把它启用，还被文案误导。运行中禁用的那段文案仍按 status === 2 判。
    if (!isTaskSwitchOn(task)) {
      await ElMessageBox.confirm(`确认启用定时任务「${task.name}」吗？`, '启用确认', { type: 'info' })
      const res = await taskApi.enable(task.id)
      ElMessage.success(res.message || '已启用')
    } else {
      const confirmMessage = task.status === 2
        ? `确认禁用定时任务「${task.name}」吗？当前执行不会被中断，禁用会在本次运行结束后生效。`
        : `确认禁用定时任务「${task.name}」吗？`
      await ElMessageBox.confirm(confirmMessage, '禁用确认', { type: 'warning' })
      const res = await taskApi.disable(task.id)
      ElMessage.success(res.message || (task.status === 2 ? '已设置为禁用，当前执行结束后生效' : '已禁用'))
    }
    loadTasks()
  } catch (err: any) {
    if (err === 'cancel' || err?.toString?.() === 'cancel') return
    ElMessage.error(err?.response?.data?.error || '操作失败')
  }
}

// 删除的确认、请求与成败提示都在 TaskDeleteDialog 里：它会先向服务端预览这个任务用到的脚本，
// 让用户选择是否一并删除（默认不勾）。这里只保留权限闸并打开弹窗，删完的收尾见 handleDeleteSuccess。
// 桌面行内菜单与移动端卡片的「···」菜单都经 onTaskAction 走到这里。
function handleDelete(task: any) {
  if (!ensureCanOperate('当前账号没有删除任务权限')) return
  deleteDialogMode.value = 'single'
  deleteDialogTaskIds.value = [task.id]
  deleteDialogTaskName.value = task.name ?? ''
  deleteDialogVisible.value = true
}

// 复制的入口是下拉菜单项（DdSplitButton / el-dropdown-item），没有 loading 位可绑
// （给 DdSplitButton 加 loading prop 会波及 10+ 调用点，本轮明确不做），所以在函数内自己闸一道。
//
// 记的是「正在复制哪个任务的 id」而不是一个整页布尔：整页布尔是一把闸，
// 会把「对任务 A 点复制、请求还没回来时又对任务 B 点复制」也一并吞掉 ——
// 这两次复制的是不同任务，本来就不属于要防的「重复创建」。
let copyingTaskId: number | null = null

async function handleCopy(task: any) {
  if (!ensureCanOperate('当前账号没有复制任务权限')) return
  const taskId = task?.id ?? null
  // 被拦下时给一句轻提示：下拉项没有 loading 态可看，静默 return 会让用户
  // 分不清「被防重拦住了」还是「菜单点歪了没生效」。
  // 这里额外判 !== null，避免 task.id 缺失时 null === null 把第一次点击也误拦。
  if (copyingTaskId !== null && copyingTaskId === taskId) {
    ElMessage.info('复制中，请稍候')
    return
  }
  copyingTaskId = taskId
  try {
    await taskApi.copy(task.id)
    ElMessage.success('任务已复制')
    loadTasks()
    void viewManagerRef.value?.loadGroups()
  } catch (err: any) {
    ElMessage.error(err?.response?.data?.error || '复制失败')
  } finally {
    // 只在「闸上记的还是自己」时才复位：并发复制 A、B 时若无条件置 null，
    // 先返回的 A 会把还在途的 B 的闸提前放开。不会卡死 —— 后返回的那一发一定与闸相等。
    if (copyingTaskId === taskId) {
      copyingTaskId = null
    }
  }
}

// 置顶行挂 task-row-pinned，样式见 <style> 里的「Pinned Row」（与环境变量页同一套）。
// 类名由 el-table 每次渲染按行数据重算，轮询就地合并、拖拽重排之后都跟着数据走，不会挂错行。
function getRowClassName({ row }: { row: any }) {
  return row?.is_pinned ? 'task-row-pinned' : ''
}

async function handlePin(task: any) {
  if (!ensureCanOperate('当前账号没有置顶任务权限')) return
  try {
    if (task.is_pinned) {
      await taskApi.unpin(task.id)
    } else {
      await taskApi.pin(task.id)
    }
    loadTasks()
  } catch { /* ignore */ }
}

/**
 * 桌面表格「操作」列 Split Button 的菜单项。
 *
 * 按行生成而不是写成无参 computed：它要读 row.status / row.is_pinned，每行的结果都不一样。
 * 在模板里调用等于跑在渲染 effect 内，row 的响应式字段（handleRun 里会把 status 改成 2）
 * 与 canOperateTasks 都会被正常追踪。
 *
 * 铁规则：谁上了一级，谁就不在菜单里出现（同一个操作绝不在同一个按钮上出现两次）。
 * - 「运行 / 停止」互斥且已由主体承担：空闲时主体是「运行」，运行中主体整个换成「停止」，
 *   所以这两项在菜单里【一个都不挂】——既不会出现「停止 ▾」里还有「停止」，
 *   也不会出现空闲状态下点得到「停止」。
 * - 「实时日志」已经外置成操作列里独立的「日志」按钮（打开实时日志是这一页最高频的动作，
 *   原来要先点 ▾ 再选第 2 项），所以它整项从菜单里摘掉了。
 *
 * visible 兜的是权限：观察者（canOperateTasks=false）运行/启用/编辑/复制/置顶/删除全部无权。
 * 「实时日志」外置后观察者那一支的 Split Button 没有主体可用了，改由「详情」当主体，
 * 于是「详情」的 visible 也得跟着 op：有权限时它留在菜单里，观察者那一支由主体承担、
 * 菜单只剩「日志文件」。两支都是「2 字主体 + caret + 外置日志按钮」，宽度完全一致。
 *
 * 「删除」不可撤销，只能待在菜单里，并且 danger + divided，绝不上主体。
 *
 * 第一项「启用 / 禁用」按开关位（isTaskSwitchOn）而不是 status === 0 判（issue #133）：
 * 被手动运行的禁用任务 status 是 0.5 / 2，原来这里会显示成「禁用」。
 * 「禁用」项标红（danger）是 issue #133 要的配色，与删除同一套红字 + 悬停淡红底；
 * 但它可撤销、又是第一项，所以【不加】divided —— 分隔线只留给不可撤销的删除。
 * 「启用」项挂 success（issue #143 D2 引入，v3.3.2 / issue #144 按用户反馈翻案）：
 * 与「禁用」的红色完全对称 —— 常态就是绿字，hover / focus 再叠淡绿底。
 * （#143 当初刻意只在 hover 才上色，怕它比同菜单的中性项抢眼一档；现在用户明确要常驻绿，
 *   这是有意识的翻案，别照旧注释又把常态色去掉。常态绿字写在 global.scss 的
 *   .dd-split-button__item--success 上，这里只管挂 success 字段。）
 * 桌面 Split Button 与移动端卡片「···」用的是同一份数组，两边自动一致。
 *
 * alwaysShowDetail：移动端卡片没有 Split Button 的主体，观察者那一支「详情」没地方承担，
 * 只能留在「···」菜单里，否则观察者在手机上就看不到详情了（桌面仍按上面的规则由主体承担）。
 */
function taskActionItems(row: any, options: { alwaysShowDetail?: boolean } = {}): SplitButtonItem[] {
  const op = canOperateTasks.value
  const switchOn = isTaskSwitchOn(row)
  return [
    { key: 'toggle', label: switchOn ? '禁用' : '启用', danger: switchOn, success: !switchOn, visible: op },
    { key: 'edit', label: '编辑', visible: op },
    { key: 'detail', label: '详情', visible: op || options.alwaysShowDetail === true },
    { key: 'logFiles', label: '日志文件' },
    { key: 'copy', label: '复制', visible: op },
    { key: 'pin', label: row.is_pinned ? '取消置顶' : '置顶', visible: op },
    { key: 'delete', label: '删除', danger: true, divided: true, visible: op },
  ]
}

// 只是把原来每个按钮的 @click 原样接过来，逻辑不动。
// liveLog 分支已随「实时日志」外置成一级按钮而删除，菜单不再派发这个 key。
function onTaskAction(key: string, row: any) {
  if (key === 'toggle') handleToggle(row)
  else if (key === 'edit') openEdit(row)
  else if (key === 'detail') openDetail(row)
  else if (key === 'logFiles') openLogFiles(row)
  else if (key === 'copy') handleCopy(row)
  else if (key === 'pin') handlePin(row)
  else if (key === 'delete') handleDelete(row)
}

function handleSelectionChange(rows: any[]) {
  selectedIds.value = rows.map(r => r.id)
}

// 退出多选态。批量区占住左槽后状态分段和搜索框都被藏起来了，这是唯一的出口，必须有。
// 桌面表格的勾选状态存在 el-table 内部：只清 selectedIds 的话表头/行里的复选框仍是勾上的，
// 所以要调它自己的 clearSelection（会反过来触发 selection-change 把数组也清掉）。
// 移动端不渲染表格，taskTableRef 是 null，可选链兜住；那边的复选框直接绑 isSelected(id)，清数组就够。
function clearSelection() {
  taskTableRef.value?.clearSelection?.()
  selectedIds.value = []
}

function isSelected(id: number) {
  return selectedIdSet.value.has(id)
}

function toggleSelected(id: number, checked: boolean | string | number) {
  const next = new Set(selectedIds.value)
  if (checked) {
    next.add(id)
  } else {
    next.delete(id)
  }
  selectedIds.value = [...next]
}

// ---- 移动端工具栏（v3.3.1，issue #143）----

// 移动端批量态：有勾选时第一行整行换成批量栏。权限一并判：观察者没有复选框，永远进不来。
const mobileBatchActive = computed(() => canOperateTasks.value && selectedIds.value.length > 0)

// 「全选」只作用于当前页：批量接口按 id 执行，拿不到全部任务的 id；桌面表头复选框也只选当前页，口径一致。
// 移动端没有表格，taskTableRef 是 null，toggleAllSelection 用不了，所以直接改 selectedIds。
const allPageSelected = computed(
  () => tasks.value.length > 0 && tasks.value.every(task => selectedIdSet.value.has(task.id))
)

function toggleSelectAllOnPage() {
  selectedIds.value = allPageSelected.value ? [] : tasks.value.map(task => task.id)
}

/**
 * 移动端工具栏「+」菜单。第一行只放得下搜索框、排序和这一个按钮，所以把这些入口都收进来，移动端不丢功能：
 *   - 新建任务 / 新建视图 / 视图管理：后两项原来在视图分组栏右侧，移动端那两个按钮不再渲染（见 ViewManager.vue）；
 *   - 导入 / 导出 / 批量设置通知 / 清理日志：桌面「⋯」菜单里的四项。
 *     「批量设置通知」（issue #149）放这里是给「没勾选、要改全部任务」用的；勾选后批量栏里另有「通知」按钮。
 * 「新建视图」按 canOperateTasks 收权限：后端 POST /tasks/views 要求 operator，观察者点了只会 403。
 * 分隔线挂在第二组的第一个可见项上，第一组一项都没有时不画（画在菜单顶端像一条多余的横线）。
 * 「导出任务」对所有角色可见，所以这份菜单实际不会为空；为空时整个「+」不渲染只是兜底。
 */
const mobileAddMenuItems = computed<SplitButtonItem[]>(() => {
  const op = canOperateTasks.value
  const createGroup: SplitButtonItem[] = [
    { key: 'createTask', label: '新建任务', visible: op },
    { key: 'createView', label: '新建视图', visible: op },
    { key: 'manageViews', label: '视图管理', visible: Boolean(viewManagerRef.value?.canManageViews) },
  ].filter(item => item.visible)
  const moreGroup: SplitButtonItem[] = [
    { key: 'import', label: '导入任务', visible: op },
    { key: 'export', label: '导出任务', visible: true },
    { key: 'batchNotify', label: '批量设置通知', visible: op },
    { key: 'cleanLogs', label: '清理日志', visible: op },
  ].filter(item => item.visible)
  return [
    ...createGroup,
    ...moreGroup.map((item, index) => ({ ...item, divided: index === 0 && createGroup.length > 0 })),
  ]
})

function onMobileAddCommand(command: string | number | object) {
  if (command === 'createTask') openCreate()
  else if (command === 'createView') viewManagerRef.value?.openCreateDialog()
  else if (command === 'manageViews') viewManagerRef.value?.openManagementDialog()
  else if (command === 'import') triggerImport()
  else if (command === 'export') void handleExport()
  else if (command === 'batchNotify') openBatchNotify()
  else if (command === 'cleanLogs') void handleCleanLogs()
}

async function handleBatchAction(action: string) {
  if (!ensureCanOperate()) return
  if (selectedIds.value.length === 0) {
    ElMessage.warning('请先选择任务')
    return
  }
  // 批量删除改走 TaskDeleteDialog（可选同时删除脚本），不再用下面的通用确认框
  if (action === 'delete') {
    deleteDialogMode.value = 'batch'
    deleteDialogTaskIds.value = [...selectedIds.value]
    deleteDialogTaskName.value = ''
    deleteDialogVisible.value = true
    return
  }
  const confirmMap: Record<string, { title: string; msg: string; type: 'warning' | 'info' }> = {
    run: { title: '批量运行', msg: `确定运行选中的 ${selectedIds.value.length} 个任务？`, type: 'info' },
    enable: { title: '批量启用', msg: `确定启用选中的 ${selectedIds.value.length} 个任务？`, type: 'info' },
    disable: { title: '批量禁用', msg: `确定禁用选中的 ${selectedIds.value.length} 个任务？`, type: 'warning' },
    stop: { title: '批量停止', msg: `确定停止选中的 ${selectedIds.value.length} 个任务？`, type: 'warning' },
  }
  const confirm = confirmMap[action]
  // 确认框必须放在 try 里：点「取消」或关掉确认框时 ElMessageBox 会 reject（'cancel' / 'close'），
  // 原来写在 try 外面，这个 reject 会逃出 handler，被 Vue 当成事件处理器的未捕获错误打到控制台。
  try {
    if (confirm) {
      await ElMessageBox.confirm(confirm.msg, confirm.title, { type: confirm.type })
    }
    await taskApi.batch(selectedIds.value, action)
    ElMessage.success('操作成功')
    loadTasks()
  } catch (err: any) {
    if (err === 'cancel' || err === 'close' || err?.toString?.() === 'cancel') return
    ElMessage.error(err?.response?.data?.error || '操作失败')
  }
}

// 删除成功（单删或批量）后的收尾：
// - 批量：清掉选中态。原来批量删除后不清 selectedIds，移动端批量栏会带着已删除的 id 一直不退出；
// - 单删：被删的这一行若正好被勾着，也从选中项里摘掉，否则批量条会带着一个已不存在的 id；
// - 详情弹窗开着的正是被删的任务时关掉它，免得对着一条已删除的记录继续操作。
function handleDeleteSuccess(payload: { mode: 'single' | 'batch'; taskIds: number[] }) {
  loadTasks()
  void viewManagerRef.value?.loadGroups()
  if (payload.mode === 'batch') {
    clearSelection()
  } else {
    for (const id of payload.taskIds) {
      if (isSelected(id)) toggleSelected(id, false)
    }
  }
  if (detailTask.value && payload.taskIds.includes(detailTask.value.id)) {
    detailVisible.value = false
  }
}

function openBatchAddLabel() {
  if (!ensureCanOperate()) return
  if (selectedIds.value.length === 0) {
    ElMessage.warning('请先选择任务')
    return
  }
  batchLabelVisible.value = true
}

function handleBatchLabelSuccess() {
  selectedIds.value = []
  loadTasks()
  void viewManagerRef.value?.loadGroups()
}

// 批量设置通知（issue #149）。不要求先勾选：没勾选时弹窗只能选「全部任务」，
// 这正是 issue 原话「一键设置全部脚本」的用法（网页勾选只作用于当前页，凑不齐全部任务的 id）。
function openBatchNotify() {
  if (!ensureCanOperate()) return
  batchNotifyVisible.value = true
}

// 桌面勾选存在 el-table 内部，要走 clearSelection 才能把复选框一起清掉（只清数组复选框不会回弹）
function handleBatchNotifySuccess() {
  clearSelection()
  loadTasks()
}

async function handleBatchPin() {
  if (!ensureCanOperate('当前账号没有置顶任务权限')) return
  if (selectedIds.value.length === 0) {
    ElMessage.warning('请先选择任务')
    return
  }
  // 并发发送，单条失败不阻塞其他任务
  const results = await Promise.allSettled(selectedIds.value.map(id => taskApi.pin(id)))
  const failed = results.filter(r => r.status === 'rejected')
  if (failed.length === 0) {
    ElMessage.success(`批量置顶成功（${results.length} 个）`)
  } else if (failed.length === results.length) {
    const first = failed[0] as PromiseRejectedResult
    ElMessage.error((first.reason as any)?.response?.data?.error || '批量置顶全部失败')
  } else {
    ElMessage.warning(`已置顶 ${results.length - failed.length} 个，${failed.length} 个失败`)
  }
  loadTasks()
}

async function handleCleanLogs() {
  if (!ensureCanOperate('当前账号没有清理日志权限')) return
  let daysStr: string
  try {
    // 默认值取 7，与系统设置里「日志保留天数」的注册默认值（system_config_registry.go）以及
    // 执行日志页那颗清理按钮对齐。这里原本写死 30，和另一处的 7 对不上，同一件事两个默认值。
    // 文案同样要写明「连文件一起删」（issue #144 / v3.3.2）：这颗按钮原先只删磁盘文件不删记录，
    // 本轮起两边统一成「记录与文件一起清」，不说清楚会让人以为只是从列表里移除。
    const { value } = await ElMessageBox.prompt(
      '请输入保留天数（将清理该天数之前的日志记录与日志文件）',
      '日志清理',
      {
        confirmButtonText: '确定',
        cancelButtonText: '取消',
        inputPattern: /^[1-9]\d*$/,
        inputErrorMessage: '请输入正整数',
        inputValue: '7',
        type: 'warning',
      },
    )
    daysStr = value
  } catch {
    return
  }
  try {
    // 文案必须透传后端返回（v3.3.2，issue #144 桌面端第 10 条）：清理现在连日志文件一起删，
    // 后端会回「已清理 N 条日志记录、M 个日志文件」，写死成「日志清理成功」等于把战果吞了。
    // 写法与 views/logs/index.vue 的 handleClean 保持一致。
    const res = await taskApi.cleanLogs(Number(daysStr))
    ElMessage.success(res.message)
  } catch (err: any) {
    ElMessage.error(err?.response?.data?.error || '日志清理失败')
  }
}

async function handleExport() {
  try {
    const res = await taskApi.export()
    const blob = new Blob([JSON.stringify(res.data, null, 2)], { type: 'application/json' })
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = `tasks_export_${new Date().toISOString().slice(0, 10)}.json`
    a.click()
    URL.revokeObjectURL(url)
  } catch (err: any) {
    ElMessage.error(err?.response?.data?.error || '导出失败')
  }
}

const importFileRef = ref<HTMLInputElement>()

function triggerImport() {
  if (!ensureCanOperate('当前账号没有导入任务权限')) return
  importFileRef.value?.click()
}

async function handleImport(event: Event) {
  if (!ensureCanOperate('当前账号没有导入任务权限')) return
  const file = (event.target as HTMLInputElement).files?.[0]
  if (!file) return
  try {
    const text = await file.text()
    let data: any
    try {
      data = JSON.parse(text)
    } catch (e: any) {
      ElMessage.error(`JSON 解析失败：${e?.message || '文件格式错误'}`)
      return
    }
    const tasksData = Array.isArray(data) ? data : data.data || data.tasks
    if (!Array.isArray(tasksData)) {
      ElMessage.error('导入数据结构无效：期望数组或 {data: [...]} / {tasks: [...]}')
      return
    }
    const invalid = tasksData.find(t => !t || typeof t !== 'object' || !t.name)
    if (invalid) {
      ElMessage.error('导入数据中存在缺少 name 字段的任务')
      return
    }
    const res = await taskApi.import(tasksData)
    ElMessage.success(res.message)
    if (res.errors?.length) {
      ElMessage.warning(`${res.errors.length} 个导入错误`)
    }
    loadTasks()
    void viewManagerRef.value?.loadGroups()
  } catch (err: any) {
    ElMessage.error(err?.response?.data?.error || '导入失败')
  }
  (event.target as HTMLInputElement).value = ''
}
</script>

<template>
  <div ref="pageRootRef" class="tasks-page dd-fixed-page dd-page-hide-heading">
    <!-- 移动端工具栏（v3.3.1，issue #143）：与桌面 .toolbar 是两套独立 DOM，桌面那套只多了 v-if="!isMobile"，内容与样式不动
         （§4.2 左槽叠放范式只属于桌面，移动端不渲染它）。
         第一行（非批量态）：搜索框 + 排序 + 「+」菜单；有勾选时整行换成批量栏（两者外边距与高度一致，切换时下面不跳）。
         刻意放在 ViewManager【前面】、桌面 .toolbar 放在它后面，用两个互斥的 v-if 而不是 v-if / v-else 相邻：
         移动端要求视图分组栏排在工具栏下面，这样 DOM 顺序就是视觉顺序（Tab 顺序、读屏顺序跟着一致），
         不用给页面根改 flex 再靠 order 调位置；ViewManager 仍是同一个实例，不会因为换端被重建、丢掉当前选中的视图。
         导入用的隐藏 file input 两套各放一份、共用同一个 ref：同一时刻只渲染其中一套，桌面那份的位置保持原样。 -->
    <div v-if="isMobile" class="task-mobile-toolbar">
      <!-- 批量栏：全选 / 取消全选 → 批量按钮（图标 + 短文字）→ 取消（最后）。不显示「已选 N 项」，横向放不下时左右滑动。
           三个红按钮的相对位置与桌面批量区一致（任意两个不相邻，实心删除不放最外侧），理由见桌面那一支的注释。
           「通知」（issue #149）只在移动端批量栏里有：这一排能横向滑，多一个按钮不影响布局；
           桌面批量区没有余量了，那边的入口放在右侧「⋯」菜单里。它插在「标签」「置顶」之间，红按钮仍两两不相邻。 -->
      <div v-if="mobileBatchActive" class="dd-scroll-row dd-mobile-batch-bar">
        <el-button @click="toggleSelectAllOnPage">{{ allPageSelected ? '取消全选' : '全选' }}</el-button>
        <el-button :icon="CircleCheck" @click="handleBatchAction('enable')">启用</el-button>
        <el-button type="danger" plain :icon="CircleClose" @click="handleBatchAction('disable')">禁用</el-button>
        <el-button :icon="VideoPlay" @click="handleBatchAction('run')">运行</el-button>
        <el-button type="danger" plain :icon="VideoPause" @click="handleBatchAction('stop')">停止</el-button>
        <el-button :icon="PriceTag" @click="openBatchAddLabel">标签</el-button>
        <el-button :icon="Bell" @click="openBatchNotify">通知</el-button>
        <el-button :icon="Top" @click="handleBatchPin">置顶</el-button>
        <el-button type="danger" :icon="Delete" @click="handleBatchAction('delete')">删除</el-button>
        <el-button :icon="Close" @click="clearSelection">取消</el-button>
      </div>
      <div v-else class="dd-mobile-toolbar">
        <el-input v-model="keyword" placeholder="搜索任务名称/命令" clearable @keyup.enter="handleSearch" @clear="handleSearch">
          <template #prefix><el-icon><Search /></el-icon></template>
        </el-input>
        <!-- 排序只留图标：激活时仍是 primary plain 高亮，看得出「正在按某列排序」；具体排的是哪一列交给 title / 读屏 -->
        <el-dropdown trigger="click" placement="bottom-end">
          <el-button
            class="dd-icon-only-btn"
            :type="quickSort ? 'primary' : 'default'"
            :plain="!!quickSort"
            :icon="Sort"
            :aria-label="quickSortButtonText"
            :title="quickSortButtonText"
          />
          <template #dropdown>
            <el-dropdown-menu>
              <el-dropdown-item
                v-for="option in quickSortOptions"
                :key="option.key"
                @click="handleQuickSortSelect(option.value)"
              >
                <!-- 与桌面排序下拉同一套「选中打勾」写法（下拉菜单 teleport 到 body，scoped 命中不到，故用内联色） -->
                <el-icon
                  v-if="activeQuickSortKey === option.key"
                  style="margin-right: 6px; color: var(--el-color-primary);"
                ><Check /></el-icon>
                <span v-else style="display: inline-block; width: 20px;"></span>
                <span :style="activeQuickSortKey === option.key ? 'color: var(--el-color-primary); font-weight: 600;' : ''">
                  {{ option.label }}
                </span>
              </el-dropdown-item>
            </el-dropdown-menu>
          </template>
        </el-dropdown>
        <!-- 挂 dd-split-button__popper：菜单字色、分隔线与全站操作菜单一致 -->
        <el-dropdown
          v-if="mobileAddMenuItems.length > 0"
          trigger="click"
          placement="bottom-end"
          popper-class="dd-split-button__popper"
          @command="onMobileAddCommand"
        >
          <el-button
            type="primary"
            class="dd-icon-only-btn"
            :icon="Plus"
            aria-label="新建与更多操作"
            title="新建与更多操作"
          />
          <template #dropdown>
            <el-dropdown-menu>
              <el-dropdown-item
                v-for="item in mobileAddMenuItems"
                :key="item.key"
                :command="item.key"
                :divided="item.divided"
              >
                {{ item.label }}
              </el-dropdown-item>
            </el-dropdown-menu>
          </template>
        </el-dropdown>
      </div>
      <input ref="importFileRef" type="file" accept=".json" style="display:none" @change="handleImport" />
    </div>

    <ViewManager ref="viewManagerRef" @view-change="handleViewChange" />

    <div v-if="!isMobile" class="toolbar">
      <!-- 左槽是一个【恒在】的容器（flex:1 + min-width:0 + 锁定 min-height），
           内部的「筛选区」与「批量区」两支【对有操作权限的账号都常驻 DOM】，叠放在同一个 1×1 网格格子里，
           只用 visibility 切换显示（观察者没有选择列、勾不动，批量区对他直接 v-if 掉，
           左槽高度恒等于筛选区高度，同样是常数）。这么排的两个理由：
             1) 左槽的 flex 属性恒定 ⇒ .toolbar 的 space-between 不会因为左边突然没了而把右区整个甩过去；
             2) 左槽高度恒等于 max(筛选区高度, 批量区高度)，与当前显示哪一支无关 ⇒ 勾选/取消永不改变工具栏高度。
           第 2 条是本页的关键：批量区是 8 个按钮 + 计数且带 flex-wrap，约 1050~1230px 这一带它会换成两行，
           而筛选区（4 个 tab + 260px 搜索框，约 552px）始终只有一行。以前只留一支在 DOM 里的话，
           勾选那一刻左槽会凭空变高一行，dd-fixed-page 下 .table-card 是 flex:1 1 0，
           工具栏高多少表格就矮多少 ⇒ 列表向下跳一下（logs 页方向相反、是变矮）。
           换行点又由内容宽决定，而内容宽随侧栏展开/收起漂 156px（220px vs 64px），媒体查询锁不住，
           只能让两支同时参与撑高。
           visibility: hidden 自带「不可点、不进 Tab 序、不进无障碍树」，不需要再加 inert / aria-hidden。
           勾选期间轮询本来就停了（canPollTaskStatus 带 selectedIds.length === 0），列表是冻结的，
           这段时间搜不了、改不了筛选是可以接受的，退出口由批量区最右的「取消选择」承担。 -->
      <div class="toolbar__left">
        <!-- 判定与批量区严格互补：批量区只在【有权限】时渲染、且【有选中】时可见，
             所以筛选区只有在「有权限且有选中」这一种情况下才让位，两支恒有且仅有一支可见。
             canOperateTasks 这一项保留着不是冗余：它让「谁隐藏」只依赖显式权限判定，
             而不是靠「viewer 没有选择列所以 selectedIds 永远是空」这条间接推理。 -->
        <div
          class="toolbar__filters"
          :class="{ 'is-swapped-out': canOperateTasks && selectedIds.length > 0 }"
        >
          <div class="status-tabs">
            <button :class="['status-tab', { active: statusFilter === '' }]" @click="statusFilter = ''; handleSearch()">全部任务</button>
            <button :class="['status-tab', { active: statusFilter === '2' }]" @click="statusFilter = '2'; handleSearch()">运行中</button>
            <button :class="['status-tab', { active: statusFilter === '0' }]" @click="statusFilter = '0'; handleSearch()">已禁用</button>
            <button :class="['status-tab', { active: statusFilter === '1' }]" @click="statusFilter = '1'; handleSearch()">已启用</button>
          </div>
          <el-input v-model="keyword" placeholder="搜索任务名称/命令" clearable class="toolbar__search" @keyup.enter="handleSearch" @clear="handleSearch">
            <template #prefix><el-icon><Search /></el-icon></template>
          </el-input>
        </div>
        <!-- 权限走 v-if、选中态才走 is-swapped-out：两者不能混在同一个 class 判定里。
             观察者（canOperateTasks=false）连选择列都没有（见表格的 type="selection" 上的 v-if），
             这一支对他永远不可能显示；而 is-swapped-out 只是 visibility: hidden、照常参与撑高，
             写成 class 就等于让 viewer 白白顶着这 8 个按钮的高度——它们在约 1050~1230px 这一带还会换成两行，
             工具栏因此恒定高出约 40px，dd-fixed-page 下 .table-card 是 flex:1 1 0，表格就被永久压矮一行。
             拆开后对 viewer 这一支根本不渲染，左槽高度恒等于筛选区高度，同样是常数，高度不变式不受影响。 -->
        <div
          v-if="canOperateTasks"
          class="batch-actions"
          :class="{ 'is-narrow': isNarrowDesktop, 'is-swapped-out': selectedIds.length === 0 }"
        >
          <span class="batch-actions__count">已选 {{ selectedIds.length }} 项</span>
          <!-- 三个红按钮（issue #133 起「批量停止」也改成 danger plain，与「批量禁用」同色同形）：
               「批量禁用」与「批量停止」之间隔一个「批量运行」，「批量停止」与实心 danger 的「批量删除」之间
               隔「添加标签」「批量置顶」两个 —— 任意两个红按钮都不相邻，实心的删除与空心的两个也拉开了距离。
               两个空心红按钮文案不同、又都有二次确认兜底，窄桌面短文案（禁用 / 停止）下也不至于点错后果不可逆。
               删除也不放最右边缘（那一侧最容易被甩动鼠标顺手点到，代价还不可逆），
               由无害的「取消选择」殿后；logs / envs 两页同序。 -->
          <!-- 窄桌面（<1600px，含 14 寸笔记本 1920×1080 缩放 150% 后的 1280 等效视口）用短文案：
               长文案下这一排放不下，会换行把工具栏从 39px 顶成两行。
               短文案 + .is-narrow 的 6px gap 后实测内容宽约 695px，左槽 712.1px。
               「添加标签」不缩：只剩「标签」两个字看不出是添加还是筛选。
               这一支只在桌面渲染；移动端另有 .dd-mobile-batch-bar（带图标，图标兜住了「标签」的歧义），见上方。 -->
          <el-button @click="handleBatchAction('enable')">{{ isNarrowDesktop ? '启用' : '批量启用' }}</el-button>
          <el-button type="danger" plain @click="handleBatchAction('disable')">{{ isNarrowDesktop ? '禁用' : '批量禁用' }}</el-button>
          <el-button @click="handleBatchAction('run')">{{ isNarrowDesktop ? '运行' : '批量运行' }}</el-button>
          <el-button type="danger" plain @click="handleBatchAction('stop')">{{ isNarrowDesktop ? '停止' : '批量停止' }}</el-button>
          <el-button @click="openBatchAddLabel">添加标签</el-button>
          <el-button @click="handleBatchPin">{{ isNarrowDesktop ? '置顶' : '批量置顶' }}</el-button>
          <el-button type="danger" @click="handleBatchAction('delete')">{{ isNarrowDesktop ? '删除' : '批量删除' }}</el-button>
          <el-button @click="clearSelection">{{ isNarrowDesktop ? '取消' : '取消选择' }}</el-button>
        </div>
      </div>
      <div class="toolbar__right">
        <el-dropdown trigger="click" class="sort-dropdown">
          <el-button :type="quickSort ? 'primary' : 'default'" :plain="!!quickSort">
            <el-icon><Sort /></el-icon>
            <span class="sort-dropdown__text">{{ quickSortButtonText }}</span>
          </el-button>
          <template #dropdown>
            <el-dropdown-menu>
              <el-dropdown-item
                v-for="option in quickSortOptions"
                :key="option.key"
                @click="handleQuickSortSelect(option.value)"
              >
                <!-- 选中项用左侧 Check 图标 + 品牌色高亮（下拉菜单 teleport 到 body，故用内联色保证生效） -->
                <el-icon
                  v-if="activeQuickSortKey === option.key"
                  style="margin-right: 6px; color: var(--el-color-primary);"
                ><Check /></el-icon>
                <span v-else style="display: inline-block; width: 20px;"></span>
                <span :style="activeQuickSortKey === option.key ? 'color: var(--el-color-primary); font-weight: 600;' : ''">
                  {{ option.label }}
                </span>
              </el-dropdown-item>
            </el-dropdown-menu>
          </template>
        </el-dropdown>
        <!-- 显示设置：任务名后面那排标签的分项开关。
             它是【偏好】不是【动作】，所以单独一个勾选式下拉，不塞进右边的「⋯」——那里全是导出/导入/清理，
             把勾选项混进一串动作项里语义不齐。
             el-tooltip 包在 el-dropdown【外面】：el-dropdown 的默认插槽走 ElOnlyChild，
             它会把 forwardRef 指令挂到插槽里的第一个子节点上，而 el-tooltip 是多根组件、接不住指令，
             触发器引用会丢。包在外层则 tooltip 的 ElOnlyChild 拿到的是 el-dropdown 的单根 div，正常。
             hide-on-click=false：连着调好几项时菜单不要一点就关。 -->
        <el-tooltip content="显示设置" placement="top">
          <el-dropdown trigger="click" :hide-on-click="false">
            <el-button>
              <el-icon><View /></el-icon>
            </el-button>
            <template #dropdown>
              <el-dropdown-menu>
                <el-dropdown-item
                  v-for="option in nameLabelOptions"
                  :key="option.key"
                  @click="toggleNameLabelPref(option.key)"
                >
                  <!-- 与上面排序下拉同一套「选中打勾」写法：下拉菜单 teleport 到 body，scoped 样式命中不到，故用内联色 -->
                  <el-icon
                    v-if="nameLabelPrefs[option.key]"
                    style="margin-right: 6px; color: var(--el-color-primary);"
                  ><Check /></el-icon>
                  <span v-else style="display: inline-block; width: 20px;"></span>
                  <span :style="nameLabelPrefs[option.key] ? 'color: var(--el-color-primary); font-weight: 600;' : ''">
                    {{ option.label }}
                  </span>
                </el-dropdown-item>
              </el-dropdown-menu>
            </template>
          </el-dropdown>
        </el-tooltip>
        <!-- 「批量设置通知」（issue #149）放在这里而不进左侧批量区：批量区对有权限的账号常驻 DOM 参与撑高，
             1280 宽下只剩约 17px 余量，再加一个按钮会让工具栏常年多出一行、表格被压矮一行。
             放在这里还有一个好处：右区在勾选前后都可见，没勾选时也能直接改「全部任务」。 -->
        <el-dropdown trigger="click">
          <el-button><el-icon><More /></el-icon></el-button>
          <template #dropdown>
            <el-dropdown-menu>
              <el-dropdown-item @click="handleExport">导出任务</el-dropdown-item>
              <el-dropdown-item v-if="canOperateTasks" @click="triggerImport">导入任务</el-dropdown-item>
              <el-dropdown-item v-if="canOperateTasks" @click="openBatchNotify">批量设置通知</el-dropdown-item>
              <el-dropdown-item v-if="canOperateTasks" divided @click="handleCleanLogs">清理日志</el-dropdown-item>
            </el-dropdown-menu>
          </template>
        </el-dropdown>
        <input ref="importFileRef" type="file" accept=".json" style="display:none" @change="handleImport" />
        <el-button v-if="canOperateTasks" type="primary" @click="openCreate">
          <el-icon><Plus /></el-icon> 新建任务
        </el-button>
      </div>
    </div>

    <div v-if="isMobile" class="dd-mobile-list">
      <div
        v-for="row in tasks"
        :key="row.id"
        class="dd-mobile-card task-card"
        :class="{ 'task-card--pinned': row.is_pinned }"
      >
        <!-- 卡片（v3.3.1，issue #143 C1）：首行「复选框 · 任务名 · 状态 · ···」，中间两行横排的时间字段，
             末行左侧上次结果、右侧「运行 / 停止」「日志」。类型 / 分组 / 订阅 / 自定义标签、命令、定时规则、耗时
             在卡片上不再展示（「详情」里都有），其余操作（启用 / 禁用、编辑、详情、日志文件、复制、置顶、删除）收进「···」。 -->
        <div class="dd-mobile-card__head">
          <el-checkbox v-if="canOperateTasks" :model-value="isSelected(row.id)" @change="toggleSelected(row.id, $event)" />
          <!-- 名称单行省略，title 里带着全称；点击行为与桌面一致：打开日志文件 -->
          <button
            type="button"
            class="task-name-link dd-mobile-card__name"
            :title="`查看 ${row.name} 的日志文件`"
            @click.stop="openLogFiles(row)"
          >
            {{ row.name }}
          </button>
          <!-- 状态紧跟名称。与桌面表格同一套 out-in 过渡：轮询把状态换掉时先淡出旧值再淡入新值（key 绑状态值的理由见桌面那一列）。 -->
          <Transition name="dd-status-switch" mode="out-in">
            <el-tag :key="row.status" :type="getStatusType(row.status)" size="small" :class="getStatusTagClass(row.status)">
              <span v-if="row.status === 2" class="pulse-dot"></span>
              {{ getStatusText(row.status) }}
            </el-tag>
          </Transition>
          <!-- 与桌面操作列 Split Button 共用 taskActionItems / onTaskAction，菜单项与配色两端一致 -->
          <DdMoreMenu
            :items="taskActionItems(row, { alwaysShowDetail: true })"
            @command="onTaskAction($event, row)"
          />
        </div>

        <div class="dd-mobile-card__rows">
          <div class="dd-mobile-card__row">
            <span class="dd-mobile-card__row-label">最后运行</span>
            <span class="dd-mobile-card__row-value time-text">{{ row.last_run_at ? formatTime(row.last_run_at) : '-' }}</span>
          </div>
          <div class="dd-mobile-card__row">
            <span class="dd-mobile-card__row-label">下次运行</span>
            <span class="dd-mobile-card__row-value next-run-cell">
              <span class="time-text">{{ row.next_run_at ? formatTime(row.next_run_at) : '-' }}</span>
              <!-- 与桌面表格同一套图标与分级，说明见那边的注释 -->
              <el-tooltip v-if="row.schedule_hint" :content="row.schedule_hint" placement="top">
                <el-icon :class="['schedule-hint-icon', isScheduleFault(row) ? 'schedule-hint-icon--fault' : 'schedule-hint-icon--info']">
                  <WarningFilled v-if="isScheduleFault(row)" />
                  <InfoFilled v-else />
                </el-icon>
              </el-tooltip>
            </span>
          </div>
        </div>

        <div class="dd-mobile-card__footer">
          <div class="dd-mobile-card__footer-main task-card__result">
            <span class="dd-mobile-card__row-label">上次结果</span>
            <div class="last-run-result">
              <Transition name="dd-status-switch" mode="out-in">
                <el-tag :key="String(row.last_run_status)" :type="getRunStatusType(row.last_run_status)" :class="getRunStatusClass(row.last_run_status)" size="small">
                  {{ getRunStatusText(row.last_run_status) }}
                </el-tag>
              </Transition>
            </div>
          </div>
          <div class="dd-mobile-card__footer-actions">
            <!-- 「停止」与桌面 Split Button 的主体同色：issue #133 起从 warning 改成 danger（与「批量删除」一致） -->
            <el-button v-if="canOperateTasks && row.status !== 2" type="primary" @click="handleRun(row)">运行</el-button>
            <el-button v-else-if="canOperateTasks" type="danger" @click="handleStop(row)">停止</el-button>
            <el-button @click="openLogViewer(row)">日志</el-button>
          </div>
        </div>
      </div>

      <el-empty v-if="!loading && tasks.length === 0" description="暂无任务" />
    </div>

    <div v-else class="table-card" :class="{ 'is-compact': isNarrowDesktop }">
      <el-table
        ref="taskTableRef"
        v-loading="loading"
        :data="tasks"
        :height="desktopTableHeight"
        class="task-table"
        row-key="id"
        @selection-change="handleSelectionChange"
        @sort-change="handleTableSortChange"
        style="width: 100%"
        :header-cell-style="{ background: 'var(--el-fill-color-light)', color: 'var(--el-text-color-regular)', fontWeight: 600, fontSize: '13px' }"
        :row-style="{ cursor: 'pointer' }"
        :row-class-name="getRowClassName"
      >
        <el-table-column v-if="canOperateTasks" type="selection" width="40" />
        <!-- 拖拽列：40px，与环境变量页同宽同形（那边是 .env-drag-col）。
             只对有操作权限的账号渲染 —— 观察者拖不动，也不该白吃这 40px 列宽
             （窄桌面三个弹性列已经在抢像素了）。
             手柄按 sortableEnabled 置灰：按列排序 / 视图自带排序 / 只有一行 / 有任务正在运行时，
             拖出来的位置要么刷新就被排序规则覆盖回去，要么落点根本算错（理由见 dragSortDisabledReason），
             所以直接禁掉并用 tooltip 说明原因与出路。 -->
        <el-table-column v-if="canOperateTasks" width="40" align="center" class-name="task-drag-col">
          <template #default>
            <el-tooltip :content="dragSortHint" placement="top">
              <el-icon class="task-drag-handle" :class="{ 'is-disabled': !sortableEnabled }"><Rank /></el-icon>
            </el-tooltip>
          </template>
        </el-table-column>
        <!-- 三个弹性列按 min-width 的比例瓜分剩余宽度（EP 的分配规则）。
             窄桌面改成 90 : 70 : 95：cron 表达式长度固定且短，全显出来价值最高；
             命令是长路径，任何窄宽度下都得省略，让它少分一点最划算。 -->
        <el-table-column label="任务名称" :min-width="isNarrowDesktop ? 90 : 80">
          <template #default="{ row }">
            <div class="task-name-cell">
              <div class="task-name-info">
                <div class="task-name-inline">
                  <button
                    type="button"
                    class="task-name-text task-name-link"
                    :title="`查看 ${row.name} 的日志文件`"
                    @click.stop="openLogFiles(row)"
                  >
                    {{ row.name }}
                  </button>
                  <!-- 窄桌面隐藏任务类型标签：它和右边的「定时规则」列完全冗余
                       （cron 类型显示表达式、其余类型显示的就是这个标签的文案），
                       但在被压窄的名称列里它会独占一行，是行高翻倍的直接原因。
                       与工具栏「显示设置」是【与】关系：手动关掉就一定不显示；手动开着时，
                       窄桌面仍按上面这条自动隐藏。取舍理由是自动隐藏解决的是「行高翻倍」这种硬故障，
                       不该被一个默认开着的偏好覆盖掉。 -->
                  <el-tag v-if="!isNarrowDesktop && nameLabelPrefs.type" size="small" effect="plain" class="task-label task-label--type">
                    {{ getTaskTypeLabel(row.task_type) }}
                  </el-tag>
                  <!-- 分组 / 订阅 / 自定义三类标签按工具栏「显示设置」分项过滤。
                       任务详情弹窗（TaskDetail.vue）刻意不跟随，那里必须能看到完整标签。
                       :key 用 entry.key（带下标）而不是标签文字：订阅名与分组名重名时会并排两条同名标签，
                       用文字当 key 会触发 Vue 重复 key 警告。 -->
                  <el-tag
                    v-for="entry in visibleTaskLabels(row)"
                    :key="entry.key"
                    size="small"
                    effect="plain"
                    class="task-label"
                    :class="`task-label--${entry.kind}`"
                    :title="taskLabelKindTitles[entry.kind]"
                  >
                    {{ entry.label }}
                  </el-tag>
                </div>
              </div>
            </div>
          </template>
        </el-table-column>
        <el-table-column label="命令 / 脚本" :min-width="isNarrowDesktop ? 70 : 80">
          <template #default="{ row }">
            <!-- 两档桌面都单行省略：窄桌面这一列被压到 100px 出头、宽桌面也只有 176px，
                 不省略窄屏要换 5 行、宽屏也会换成两行把行高翻倍；title 挂全文兜底 -->
            <code class="command-text" :title="row.command">
              <template v-if="splitTaskCommandDisplay(row.command).script">
                <span>{{ splitTaskCommandDisplay(row.command).before }}</span>
                <span class="script-link" @click.stop="navigateToScript(splitTaskCommandDisplay(row.command).script!)">{{ splitTaskCommandDisplay(row.command).script }}</span>
                <span>{{ splitTaskCommandDisplay(row.command).after }}</span>
              </template>
              <template v-else>{{ row.command }}</template>
            </code>
          </template>
        </el-table-column>
        <!-- 表头可点排序（issue #151）：写法同下方「运行时间」，sortable="custom" 只出箭头、交给后端排。
             按每天最早一次执行的时刻比较，不是按表达式字符串；口径见 quickSortOptions 那两项的注释。
             窄桌面加了排序箭头后，这一格在 101px 以下会折成两行，但按 90:70:95 的比例那时「命令 / 脚本」列
             （表头要 89px、下限 70）早已折行，整行表头高度不变，所以不需要为它加 nowrap 或调宽；调宽反而会把命令列挤到折行。 -->
        <el-table-column
          prop="cron_expression"
          label="定时规则"
          :min-width="isNarrowDesktop ? 95 : 70"
          sortable="custom"
        >
          <template #default="{ row }">
            <template v-if="row.task_type === 'cron'">
              <TaskCronList
                :expressions="getCronExpressions(row)"
                compact
              />
            </template>
            <span v-else class="text-muted">{{ getTaskTypeLabel(row.task_type) }}</span>
          </template>
        </el-table-column>
        <!-- 窄桌面把「状态」「上次结果」收到刚好装下标签的宽度，省出的 36px 还给
             名称/命令/定时规则三个弹性列，让 cron 表达式尽量不被省略号截断 -->
        <el-table-column label="状态" :width="isNarrowDesktop ? 90 : 110" align="center">
          <template #default="{ row }">
            <!-- 3 秒一轮的状态轮询会让这枚 tag 在 禁用中/排队中/运行中/空闲中 之间来回跳，
                 硬切时用户只看到文字忽然变了、不确定是自己看花眼还是真的变了。
                 out-in 让旧状态先淡出、新状态再淡入，变化本身有了可见的过程。
                 key 必须绑【状态值】：绑 row.id 的话同一行永远是同一个 key，节点被原地复用，
                 过渡一次都不会触发。 -->
            <Transition name="dd-status-switch" mode="out-in">
              <el-tag :key="row.status" :type="getStatusType(row.status)" size="small" :class="getStatusTagClass(row.status)">
                <span v-if="row.status === 2" class="pulse-dot"></span>
                {{ getStatusText(row.status) }}
              </el-tag>
            </Transition>
          </template>
        </el-table-column>
        <!-- 「运行时间」（issue #163）：原来的「最后运行」「下次运行」两列合成一列，单元格上下两行「上次 / 下次」。
             原先窄桌面（<1600px）为省宽把「最后运行」整列 v-if 掉了，报告人在窄屏上就看不到上次运行；
             合并后一列 184px 两档桌面都显示。宽度这么算：一行最长是「下次」(12px×2=24) + 间距 6 +
             `MM-DD HH:mm:ss` 14 个等宽字符（JetBrains Mono 13px 约 7.8px/字，≈109）+ 间距 4 + schedule_hint 图标 14 ≈ 157px，
             再加 .cell 左右各 12px 内边距 = 181px，取 184 留一点字体回落的余量；两行都 nowrap，宁可贴边也不折行。
             比原来窄桌面单列「下次运行」的 160 多 24px，宽桌面则比两列合计 320 省出 136px。
             sortable="custom" = 只出箭头、排序交给后端；prop 用 next_run_at（后端 sort_rules 的 field 名），
             @sort-change 把它写进 quickSort（不另起一份排序状态）。按最后运行排序走工具栏排序下拉（quickSortOptions 的 last_run_* 两项）。 -->
        <el-table-column
          prop="next_run_at"
          label="运行时间"
          width="184"
          align="center"
          sortable="custom"
        >
          <template #default="{ row }">
            <div class="run-time-cell">
              <div class="run-time-cell__line">
                <span class="run-time-cell__label">上次</span>
                <span v-if="row.last_run_at" class="time-text">{{ formatTime(row.last_run_at) }}</span>
                <span v-else class="text-muted">-</span>
              </div>
              <div class="run-time-cell__line">
                <span class="run-time-cell__label">下次</span>
                <span v-if="row.next_run_at" class="time-text">{{ formatTime(row.next_run_at) }}</span>
                <span v-else class="text-muted">-</span>
                <!-- schedule_hint 由后端下发，正常时是空串。它专治 issue #115 那种「看着一切正常、实际不会触发」：
                     任务显示已启用、下次运行也有时间，但调度器里根本没有它（或者它压根不是 cron 类型）。
                     不做成 el-tag 是因为一行只剩十几像素，塞不下一枚标签；图标 + tooltip 是这里唯一装得下的形态。
                     它说的是「下次会不会触发」，所以跟在「下次」这一行。
                     分两种严重程度：定时任务没注册上是真故障（橙色警示），
                     手动 / 开机运行只是在陈述任务类型（灰色信息），后者用警示色就是天天喊狼来了。 -->
                <el-tooltip v-if="row.schedule_hint" :content="row.schedule_hint" placement="top">
                  <el-icon :class="['schedule-hint-icon', isScheduleFault(row) ? 'schedule-hint-icon--fault' : 'schedule-hint-icon--info']">
                    <WarningFilled v-if="isScheduleFault(row)" />
                    <InfoFilled v-else />
                  </el-icon>
                </el-tooltip>
              </div>
            </div>
          </template>
        </el-table-column>
        <el-table-column label="上次结果" :width="isNarrowDesktop ? 84 : 100" align="center">
          <template #default="{ row }">
            <div class="last-run-result">
              <!-- 同一轮轮询里，任务跑完的那一刻这里会从「未运行」翻成「成功 / 失败 / 已终止」，
                   是全表最值得被看见的一次变化。key 走 String()：last_run_status 为 null（未运行）时
                   直接绑 null 会被 Vue 当成「没有 key」，和有 key 的节点比较时行为不稳定。 -->
              <Transition name="dd-status-switch" mode="out-in">
                <el-tag :key="String(row.last_run_status)" :type="getRunStatusType(row.last_run_status)" :class="getRunStatusClass(row.last_run_status)" size="small">
                  {{ getRunStatusText(row.last_run_status) }}
                </el-tag>
              </Transition>
            </div>
          </template>
        </el-table-column>
        <!-- 窄桌面隐藏「耗时」：信息没丢，任务详情弹窗里有。 -->
        <el-table-column v-if="!isNarrowDesktop" label="耗时" width="90" align="center">
          <template #default="{ row }">
            <span v-if="row.last_running_time != null" class="time-text">{{ formatDuration(row.last_running_time) }}</span>
            <span v-else class="text-muted">-</span>
          </template>
        </el-table-column>
        <!--
          列宽 172：EP 的 .el-table .cell 是 padding:0 12px + overflow:hidden，可用内容宽 = 列宽 - 24 = 148px。
          这一格现在是两个元素：左边 Split Button（运行/停止 ▾），右边外置的「日志」普通按钮。
          实测口径（size="small"，浏览器量出来的，不是估的）：
            主体 = 中文字数 × 12（字宽）+ 22（EP small 的 padding 5px 11px）+ 2（边框）
            caret = 32，且【不受 size 影响】：EP 2.13 的 el-dropdown 根节点只挂 `el-dropdown` + `is-disabled`，
              不带 size 修饰类，`.el-dropdown--small .el-dropdown__caret-button{width:24px}` 根本匹配不上
            el-button-group 组内相邻按钮还有 -1px 的负边距
          有权限那一支：Split「运行 / 停止」= 2×12+22+2 = 48 → 48+32-1 = 79px（实测 78.99，吻合）；
          外置「日志」= 2×12+22+2 = 48px；中间 gap 4px（.action-btns 已把 EP 的 .el-button + .el-button
          外边距清零，间距只由 gap 决定，不会两份叠加）。合计 79 + 4 + 48 = 131px，148 - 131 = 17px 余量。
          观察者那一支主体换成「详情」，同样 2 字 → 79px，两支宽度完全一致，
          所以窄桌面（<1600px）与常规桌面仍然共用这一个宽度。
          按钮组一旦超出可用宽，.cell 就会变成可滚动容器，点右边的按钮时整行会被滚偏，详见下方 .action-btns 注释。
        -->
        <el-table-column label="操作" width="172" fixed="right" align="center">
          <template #default="{ row }">
            <div class="action-btns">
              <!-- 主体是「运行」，任务运行中（status===2）整个换成「停止」：两者互斥，谁上了主体谁就不在
                   菜单里出现，不会有「停止 ▾」点开还挂着「停止」这种事。
                   主体敢放运行/停止，是因为 handleRun / handleStop 各自都带 ElMessageBox 二次确认，
                   点错的代价是按一下 Esc；删除不可撤销，所以它只能待在菜单里（danger + divided）。

                   触发器贴在表格最右侧，DdSplitButton 默认 placement="bottom-end" 让菜单右对齐触发器。
                   EP 在 dropdown.vue 里把 fallback-placements 写死成 ['bottom','top']（2.13.5 仍是如此，
                   props 覆盖不掉），靠近视口底部放不下时仍会退回居中的 top —— 这属于 EP 限制；
                   但按钮组已从 228px 收到 80px，就算退回居中，菜单也只在表格内挪一点，不会被顶到窗口边。

                   「停止」主体从 warning 改成 danger（issue #133，与「批量删除」同色）。改的只是颜色：
                   主体仍是「点错代价最小」的那个操作（停止有二次确认、可以再点运行），DdSplitButton 的约定不受影响；
                   实心 danger 的 caret 中缝白线 global.scss 早已覆盖。 -->
              <DdSplitButton
                v-if="canOperateTasks"
                :label="row.status === 2 ? '停止' : '运行'"
                :type="row.status === 2 ? 'danger' : 'primary'"
                size="small"
                :items="taskActionItems(row)"
                @click="row.status === 2 ? handleStop(row) : handleRun(row)"
                @command="(key: string) => onTaskAction(key, row)"
              />
              <!-- 观察者：运行/停止/启用/编辑/复制/置顶/删除全部无权（handler 里 ensureCanOperate 会直接拦下），
                   主体只能给只读操作。原来这一支的主体是「实时日志」，它外置成右边的一级按钮后主体空了，
                   改成「详情」——DdSplitButton 的契约要求主体点了就能直接执行，「详情」满足（openDetail 就是它的全部）。
                   菜单里因此只剩「日志文件」，由 taskActionItems 的 visible 联动。
                   顺带两支形态对称：主体都是 2 个字 = 79px，加外置日志按钮总宽一致，172 的列宽两支通用。 -->
              <DdSplitButton
                v-else
                label="详情"
                type="default"
                size="small"
                :items="taskActionItems(row)"
                @click="openDetail(row)"
                @command="(key: string) => onTaskAction(key, row)"
              />
              <!-- 「日志」外置成一级：打开实时日志是这一页最高频的动作，原来埋在 ▾ 菜单第 2 项，要点两下。
                   外置后它就从 taskActionItems 里摘掉了（谁上了一级谁就不在菜单里）。
                   文案取 2 字「日志」而不是「实时日志」：4 字要多吃 24px，
                   在已经压到 min-width 下限的窄桌面上这 24px 只能从 cron/命令列身上扣。 -->
              <el-button size="small" @click="openLogViewer(row)">日志</el-button>
            </div>
          </template>
        </el-table-column>
      </el-table>
    </div>

    <div class="pagination-bar">
      <span class="pagination-total">共 {{ total }} 条数据</span>
      <el-pagination
        v-model:current-page="page"
        v-model:page-size="pageSize"
        :total="total"
        :page-sizes="taskPageSizeOptions"
        layout="sizes, prev, pager, next"
        @current-change="handlePageChange"
        @size-change="handlePageSizeChange"
      />
    </div>

    <TaskForm
      v-model:visible="formVisible"
      :task="editingTask"
      :prefill="prefillData"
      :default-python-version="defaultPythonVersion"
      :python-runtimes="pythonRuntimeOptions"
      :notification-channels="notificationChannels"
      :submitting="formSubmitting"
      @submit="handleFormSubmit"
    />

    <LogViewer
      v-model:visible="logViewerVisible"
      :task-id="logViewerTaskId"
      :task-name="logViewerTaskName"
      :mode="logViewerMode"
    />

    <TaskDetail
      v-model:visible="detailVisible"
      :task="detailTask"
    />

    <LogFileBrowser
      v-model:visible="logFilesVisible"
      :task-id="logFilesTaskId"
      :task-name="logFilesTaskName"
    />

    <BatchAddLabelDialog
      v-model:visible="batchLabelVisible"
      :task-ids="selectedIds"
      @success="handleBatchLabelSuccess"
    />

    <BatchNotifyDialog
      v-model:visible="batchNotifyVisible"
      :task-ids="selectedIds"
      @success="handleBatchNotifySuccess"
    />

    <TaskDeleteDialog
      v-model:visible="deleteDialogVisible"
      :mode="deleteDialogMode"
      :task-ids="deleteDialogTaskIds"
      :task-name="deleteDialogTaskName"
      @success="handleDeleteSuccess"
    />
  </div>
</template>

<style scoped lang="scss">
.tasks-page {
  padding: 0;
  font-size: 14px;
  min-width: 0;
}

.page-header {
  display: flex;
  justify-content: space-between;
  align-items: flex-start;
  margin-bottom: 18px;
  gap: 16px;

  h2 {
    margin: 0;
    font-size: 22px;
    font-weight: 700;
    color: var(--el-text-color-primary);
    line-height: 1.3;
  }

  .page-subtitle {
    font-size: 13px;
    color: var(--el-text-color-secondary);
    margin: 4px 0 0;
  }

  .header-actions {
    display: flex;
    gap: 10px;
    flex-shrink: 0;
  }
}

// 工具条：与上方 ViewManager 留出协调间距，左右两区一行排布、gap 统一
.toolbar {
  display: flex;
  justify-content: space-between;
  // 刻意【不】用 align-items: center（与 logs 页统一）：批量区在约 1050~1230px 这一带会换成两行，
  // 左槽变高后 center 会把只有 32px 的右区整个垂直居中到左槽的中线上，
  // 右侧按钮的中心比左区第一行低约 20px，看起来就是两边对不齐。
  // 改成 flex-start + 右区自己撑到 39px（= status-tabs 高度）并内部居中，两者都对齐到左区【第一行】的中心线。
  align-items: flex-start;
  margin: 14px 0;
  gap: 12px;
  flex-wrap: wrap;

  // 左槽：1×1 网格，筛选区与批量区【叠放在同一个格子里】，两支都常驻 DOM。
  // 这样左槽高度恒等于 max(两支高度)，勾选/取消勾选永远不改变工具栏高度。
  // align-items 必须是 start 不能是 center：批量区换成两行时，
  // center 会把只有一行的筛选区垂直居中到两行的中线上，切过去时整排筛选控件往下掉一截。
  // min-height 锁 39px（= .status-tabs 的实测高度）：批量按钮只有 32px，兜底避免左槽塌 7px。
  &__left {
    display: grid;
    grid-template-columns: minmax(0, 1fr);
    align-items: start;
    flex: 1;
    min-width: 0;
    min-height: 39px;
  }

  // 筛选区：原来这套横排（状态分段 + 搜索框）是直接挂在 .toolbar__left 上的，
  // 现在左槽要叠放两块内容，单独包一层来承接原先的 gap / flex-wrap / 可压缩。
  &__filters {
    display: flex;
    align-items: center;
    gap: 12px;
    flex-wrap: wrap;
    min-width: 0;
  }

  &__right {
    display: flex;
    align-items: center;
    gap: 10px;
    // 与左区第一行对齐的另一半：撑到同样的 39px，内部 center 让 32px 的按钮落在同一条中线上
    min-height: 39px;
  }

  &__search {
    width: 260px;
  }
}

// 状态分段控件：对齐全站统一的 .dd-seg-group / .dd-seg-btn 观感（灰底槽 + 选中态白底品牌色 + 1px 描边）
.status-tabs {
  display: inline-flex;
  background: var(--el-fill-color-light);
  // 灰底槽取 control 档，与全站通用类 .dd-seg-group 以及槽内的 .status-tab 同档。
  // 为什么不是 surface：槽 padding 3px，内侧曲率 = 槽圆角 - 3px。
  // 槽取 control(6px) 时内侧曲率 3px < 项的 6px，选中项的白底角始终落在槽内侧曲线之内；
  // 槽取 surface(10px) 时内侧曲率 7px > 6px，选中项的角会略微越过内侧曲线露出错位，方向是反的。
  border-radius: var(--dd-radius-control);
  padding: 3px;
  gap: 2px;
}

.status-tab {
  padding: 6px 14px;
  // 槽内的分段项 → control 档，与灰底槽同档（理由见上面 .status-tabs 的注释）
  border-radius: var(--dd-radius-control);
  // 透明描边占位，选中时只换 border-color，避免出现 1px 的尺寸跳动
  border: 1px solid transparent;
  background: transparent;
  color: var(--el-text-color-secondary);
  font-size: 13px;
  font-weight: 500;
  cursor: pointer;
  transition:
    color var(--dd-motion-fast) var(--dd-ease-standard),
    background-color var(--dd-motion-fast) var(--dd-ease-standard),
    border-color var(--dd-motion-fast) var(--dd-ease-standard);
  white-space: nowrap;

  &:hover {
    color: var(--el-text-color-primary);
  }

  &.active {
    background: var(--el-bg-color);
    color: var(--el-color-primary);
    border-color: var(--el-border-color-lighter);
    font-weight: 600;
  }
}

.batch-actions {
  display: flex;
  // 「已选 N 项」是一段纯文字，默认的 stretch 会让它按整个行高拉满、与 32px 的按钮基线对不齐
  align-items: center;
  gap: 8px;
  // 极窄的桌面视口下这一排装不下时换行，不要顶破左槽横向溢出（移动端不渲染这一支，另有横滑的 .dd-mobile-batch-bar）
  flex-wrap: wrap;
  min-width: 0;
  // 与 .toolbar__right 站同一条基线：右区也是 min-height:39px + 内部 center，中心线在 19.5px。
  // 批量条本身只有 32px，而左槽是 align-items: start（批量区换两行时不能把筛选区压到中线去），
  // 不补这个下限它就贴在网格行顶端、中心线只有 16px，勾选后整排批量按钮会比右侧按钮高 3.5px。
  // 39px 本来就是左槽的 min-height，补上不会改变左槽高度，高度不变式照旧成立。
  min-height: 39px;

  // 窄桌面再收 2px 间距。短文案之后 1280px 下实测内容宽 711.4px、左槽 712.1px，只剩 0.7px 余量，
  // 窗口再窄 1px 就换行；这一排是 9 个子项 8 个 gap，8px→6px 省出 16px，余量回到约 16.7px。
  // 判定源与短文案共用 isNarrowDesktop：文案长短是 JS 算的，媒体查询没法跟着它走。
  // 注意对有操作权限的账号它【常驻 DOM】（见模板注释），所以哪怕没勾选，这一排也在按当前文案长度参与撑高左槽 ——
  // 这正是勾选前后工具栏等高的原因，不是多余开销；观察者那边整支被 v-if 掉，不会白撑这份高度。
  &.is-narrow {
    gap: 6px;
  }
}

.batch-actions__count {
  font-size: 13px;
  color: var(--el-text-color-secondary);
  white-space: nowrap;
  cursor: default;
}

// 左槽的两支叠放在同一个网格格子里：谁都不脱离文档流，所以两支都在为左槽撑高，
// 左槽高度 = max(两支高度)，切换时高度恒定不变，表格不会被工具栏推着重排。
// 切换只改 opacity，不做高度/宽度过渡 —— .toolbar 与批量区本身都是 flex-wrap 容器，
// 尺寸每帧变一次就要重新算一遍换行，整页跟着重排。
.toolbar__filters,
.batch-actions {
  grid-area: 1 / 1;
  min-width: 0;
  transition: opacity var(--dd-motion-fast) var(--dd-ease-standard);
}

// 当前不该显示的那一支：visibility: hidden 已经同时挡掉鼠标、Tab 焦点和读屏，
// 不要再叠 inert / aria-hidden / pointer-events；也【不能】改成 display: none，
// 那样它就不再撑高左槽，高度不变式立刻失效（桌面端会退回勾选时表格跳动）。
// 注意过渡是【单向】的：上面的 transition 只列了 opacity，visibility 不在其中、切换那一帧立即生效，
// 所以退场的那一支是硬切、只有进场的那一支有淡入。这是刻意的——两支叠在同一个格子里，
// 真做交叉淡出会有一段两排按钮互相透视的重影，别为了「对称」把 visibility 加进 transition。
.is-swapped-out {
  opacity: 0;
  visibility: hidden;
}

// 快捷排序触发按钮：图标与文案留出间距，文案过长时不撑破工具栏
.sort-dropdown__text {
  margin-left: 4px;
  white-space: nowrap;
}

:deep(.tag-with-dot) {
  display: inline-flex !important;
  align-items: center;
  gap: 5px;
}

// 状态 tag 的 out-in 切换：只用 opacity。
// 表格行里禁止位移——tag 一动，同一格的基线跟着抖，视觉上整行都在晃。
// 这里的选择器要落到 el-tag 的根元素上：子组件根节点会继承父作用域的 data-v，scoped 下能命中。
.dd-status-switch-enter-active,
.dd-status-switch-leave-active {
  transition: opacity var(--dd-motion-fast) var(--dd-ease-standard);
}

.dd-status-switch-enter-from,
.dd-status-switch-leave-to {
  opacity: 0;
}

// 表格卡：无阴影，仅靠 1px 边框与页面底色区分（dd-fixed-page 下的 flex:1 + 内部滚动由全局规则接管）
.table-card {
  background: var(--el-bg-color);
  // 表格容器 → surface 档
  border-radius: var(--dd-radius-surface);
  border: 1px solid var(--el-border-color-lighter);
  overflow: hidden;
}

.task-name-cell {
  display: flex;
  align-items: center;
  gap: 8px;
}

.task-name-info {
  display: flex;
  flex-direction: column;
  gap: 4px;
  min-width: 0;
}

.task-name-text {
  font-weight: 500;
  color: var(--el-text-color-primary);
  min-width: 0;
}

.task-name-link {
  max-width: 100%;
  padding: 0;
  border: none;
  background: transparent;
  color: var(--el-color-primary);
  font: inherit;
  line-height: inherit;
  text-align: left;
  cursor: pointer;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  // 时长/缓动只能取令牌：写死毫秒会绕过 prefers-reduced-motion 下把令牌压到 1ms 的降级
  transition:
    color var(--dd-motion-fast) var(--dd-ease-standard),
    text-decoration-color var(--dd-motion-fast) var(--dd-ease-standard);

  &:hover {
    color: var(--el-color-primary-dark-2);
    text-decoration: underline;
    text-decoration-thickness: 1px;
    text-underline-offset: 3px;
  }

  &:focus-visible {
    outline: 2px solid color-mix(in srgb, var(--el-color-primary) 45%, transparent);
    outline-offset: 2px;
    // 焦点环沿 border-radius 描边，跟着控件档走
    border-radius: var(--dd-radius-control);
  }
}

.task-name-inline {
  display: flex;
  align-items: center;
  gap: 6px;
  flex-wrap: wrap;
}

.task-label {
  font-size: 11px !important;
  // 任务名后面的类型标签（el-tag）→ control 档，与全局 --el-tag-border-radius 同档
  border-radius: var(--dd-radius-control);

  &--type {
    background: rgba(64, 158, 255, 0.08);
    color: var(--el-color-primary);
    border-color: transparent;
  }

  // 分组 / 订阅 两类各给一套语义色。订阅名与分组名重名时会并排出现两个一模一样的文字，
  // 不上色的话用户只会当成「多了个重复标签」，也判断不出「显示设置」里该关哪个开关。
  // 配色全部吃 EP 语义色令牌 + color-mix，暗色主题下自动跟着变，不写死色值（design-system 硬规则 1）。
  &--group {
    background: color-mix(in srgb, var(--el-color-success) 12%, transparent);
    color: var(--el-color-success);
    border-color: transparent;
  }

  &--subscription {
    background: color-mix(in srgb, var(--el-color-warning) 12%, transparent);
    color: var(--el-color-warning);
    border-color: transparent;
  }

  // `--custom` 刻意不写规则：自定义标签是绝大多数任务的常态，保持 el-tag plain 的中性外观，
  // 整列才不会花成一片。类名照样挂上去，一是给「哪一条是自定义」留可查的 DOM 证据，
  // 二是以后要给它单独上色时有现成的挂点 —— 不是漏写。
}

.command-text {
  font-family: var(--dd-font-mono);
  font-size: 13px;
  color: var(--el-text-color-secondary);
  // 两档桌面统一单行省略。操作列加宽到 172px 后宽桌面的命令列只剩 176px，
  // 长命令一换行就把行高从 23px 顶到 46px，正好把隐藏标签省下的高度又吃回去。
  // word-break 必须是 normal：按字符断行会让 text-overflow 失效。
  display: block;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  word-break: normal;

  .script-link {
    color: var(--el-color-primary);
    cursor: pointer;
    &:hover { text-decoration: underline; }
  }
}

.time-text {
  font-family: var(--dd-font-mono);
  font-size: 13px;
  color: var(--el-text-color-regular);
}

.text-muted {
  color: var(--el-text-color-placeholder);
}

// 移动端卡片的「下次运行」值：时间 + 可能存在的 schedule_hint 警示图标并排一行，靠左排。
// 桌面表格 v3.3.8 起改用下面的 .run-time-cell，这个类只剩卡片在用。
.next-run-cell {
  display: flex;
  align-items: center;
  justify-content: flex-start;
  gap: 4px;
}

// 桌面「运行时间」格（issue #163）：上下两行「上次 / 下次」。
// 列是 align="center"，整块用 inline-flex 跟着居中；块内两行左对齐，「上次」「下次」两个前缀才能对成一条竖线。
.run-time-cell {
  display: inline-flex;
  flex-direction: column;
  align-items: flex-start;
  gap: 2px;
  vertical-align: middle;
}

.run-time-cell__line {
  display: flex;
  align-items: center;
  gap: 4px;
  line-height: 18px;
  // 宽度按一行最长内容算好了（见模板注释），不允许折行
  white-space: nowrap;
}

// 前缀用小号灰字，和时间本身拉开层级
.run-time-cell__label {
  margin-right: 2px;
  font-size: 12px;
  color: var(--el-text-color-secondary);
}

.schedule-hint-icon {
  font-size: 14px;
  // 它只是提示、点了没有动作，光标用 help 而不是 pointer，免得看着像能点开什么
  cursor: help;
}

// 定时任务没注册上 = 真故障，用警示色
.schedule-hint-icon--fault {
  color: var(--el-color-warning);
}

// 手动 / 开机运行只是在陈述任务类型，本来就不该自动跑，用中性色，别喊狼来了
.schedule-hint-icon--info {
  color: var(--el-text-color-placeholder);
}

.last-run-result {
  display: inline-flex;
  flex-direction: column;
  align-items: center;
  gap: 4px;
  // out-in 有一段「旧的已走、新的未来」的空档，这一格会瞬间没有内容。
  // 撑住 el-tag small 的高度，空档期这一格不塌成 0。
  // 桌面表格的行高由定时规则/操作列撑着、移动端卡片末行由右侧 32px 的按钮撑着，这里只是顺带保险。
  min-height: 20px;
}

.action-btns {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 4px;

  // EP 自带 `.el-button + .el-button { margin-left: 12px }`，它会叠加在上面的 flex gap 上：
  // 连续四个按钮凭空多吃 36px，按钮组总宽超过「操作」列的可用内容宽（列宽 - .cell 的 24px 内边距），
  // 于是 .cell（EP 给的 overflow:hidden）变成可滚动容器 —— 点最右边的 ⋯ 时浏览器会把获得焦点的
  // 按钮滚进可视区，整行按钮左移、最左边的「运行」被裁掉且不会自动复位。
  // 间距统一交给 gap，这里把这份外边距清零；顺带修掉「前四个按钮间距 16px、编辑与 ⋯ 之间只有 4px」的不一致。
  //
  // 这条规则一度是空转的：五个按钮 + ⋯ 收成一个 DdSplitButton 后，格子里只剩一个元素，
  // 而 EP 的 el-button-group 本来就把组内相邻按钮的 margin-left 归零。
  // 现在「日志」外置成了第二个按钮，它【真的生效了】——Split Button 与「日志」之间的间距
  // 只由上面的 gap:4px 决定，不会再叠上 EP 的 12px（叠上就正好把 131px 顶到 143px，
  // 逼近 148px 的可用宽，余量被吃光）。所以这条不能删。
  //
  // 原来这里还有一条 `:deep(.el-button) { padding: 4px 8px }`，是当年硬挤五个文字按钮才加的。
  // 现在这一格只有 Split Button + 「日志」两个元素，列宽也按 172 重算过（见上方操作列注释），
  // 压内边距只会让它们显得局促，已经去掉，改回 EP small 档默认的 5px 11px ——
  // 与 Open API 页的同款按钮保持一致，列宽估算也按这个默认值算。
  :deep(.el-button + .el-button) {
    margin-left: 0;
  }
}

// 分页条：与表格卡视觉衔接，间距/字号收敛
.pagination-bar {
  margin-top: 14px;
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 0 4px;
}

.pagination-total {
  font-size: 13px;
  color: var(--el-text-color-secondary);
}

@media screen and (min-width: 769px) {
  .tasks-page {
    height: 100%;
    min-height: 0;
    display: flex;
    flex-direction: column;
    overflow: hidden;
  }

  .tasks-page > * {
    flex-shrink: 0;
    min-width: 0;
  }

  .table-card {
    flex: 1 1 0;
    height: 0;
    min-height: 0;
    display: flex;
    flex-direction: column;
    overflow: hidden;
  }

  :deep(.table-card .el-table) {
    flex: 1 1 auto;
    min-height: 0;
  }

  .pagination-bar {
    flex-shrink: 0;
  }
}

:deep(.el-table) {
  // 边框统一走令牌，明暗自动适配（原写死浅灰会在暗色串色）
  --el-table-border-color: var(--el-border-color-lighter);

  .el-table__header-wrapper th {
    border-bottom: 1px solid var(--el-border-color-light);
  }

  .el-table__row td {
    border-bottom: 1px solid var(--el-border-color-lighter);
  }

  .el-table__cell {
    padding: 12px 0;
  }

  .el-table-column--selection .cell {
    display: flex;
    align-items: center;
    justify-content: center;
  }
}

/* ---- Pinned Row ---- */
// 置顶行：整行淡橙底 + 首格左缘 4px 橙色竖条，与环境变量页 .env-row-pinned 同一套写法（推导见那边注释）。
// 🔴 选择器里的 `.el-table` 别删：操作列是 fixed="right"，EP 给固定列补了
//    `.el-table__body-wrapper tr td.el-table-fixed-column--right { background: inherit }`（0,2,2），
//    不带 `.el-table` 只有 (0,2,1)，最右一格会退回普通底色；带上是 (0,3,1)，压得过。
//    它仍输给 EP 的行 hover（0,3,2），悬停照常显示 hover 底色，别加 !important 去压。
:deep(.el-table .task-row-pinned > td) {
  background: color-mix(in srgb, #ffd66b 12%, var(--el-table-tr-bg-color));
}

:deep(.task-row-pinned > td:first-child) {
  box-shadow: inset 4px 0 0 #f5a623;
}

// ===== 窄桌面紧凑模式（视口 <1600px，由 isNarrowDesktop 挂 .is-compact）=====
// 目标是把行高从 94~186px 压回 40px 上下。三件事一起做才有效，缺一件都会被最高的那格顶回去：
//   1) 定时规则单行省略（不省略就是 3 行）。命令列的省略已提升到 .command-text 基态、两档桌面共用，
//      所以这里不再重复声明；
//   2) 名称行不再换行（配合模板里隐藏任务类型标签，多数行只剩一行文字）；
//   3) 收紧单元格上下内边距。
// 列的隐藏在模板里做（v-if="!isNarrowDesktop"），不在这里用 display:none —— el-table 的
// 列宽是 JS 算出来写进 <colgroup> 的，CSS 藏列只会留下一段空白，宽度并不会还给其它列。
.table-card.is-compact {
  :deep(.el-table__cell) {
    padding: 6px 0;
  }

  // 名称行保持 wrap：一旦改成 nowrap，标签（flex-shrink:0）会把任务名挤成 0 宽，
  // 出现「只剩两个标签、名字整个不见了」。让标签换到第二行，名字始终独占第一行。
  .task-name-link {
    flex: 1 1 100%;
  }

  .task-label {
    flex-shrink: 0;
  }

  :deep(.task-cron-list__code) {
    // 内边距从 4px 8px 收到 3px 6px：正好让 `0 8,15,21 * * *` 这类 15 字符的表达式
    // 在窄桌面的列宽里完整显示，而不是差几个像素被省略号截掉
    padding: 3px 6px;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
    word-break: normal;
  }
}

// 置顶移动卡：与环境变量页 .env-card--pinned 同一套，只改边框色与左缘竖条、不改底色。
// global.scss 的 `.dd-mobile-card` 是 (0,1,0)，这条 scoped 之后是 (0,2,0)，压得过。
.task-card--pinned {
  border-color: rgba(245, 166, 35, 0.28);
  box-shadow: inset 4px 0 0 #f5a623;
}

// 移动卡片的任务名：同挂 .task-name-link（蓝字、点击打开日志文件）与全局 .dd-mobile-card__name（16px 单行省略）。
// .task-name-link 的 `font: inherit` 在 scoped 下是 (0,2,0)，会把全局那条 (0,1,0) 的 16px / 600 冲回页面的 14px，
// 这里用两个类叠加（scoped 后 (0,3,0)）把字号字重补回来 —— global.scss 那条的注释里约定了由任务页自己保证。
.task-name-link.dd-mobile-card__name {
  font-size: 16px;
  font-weight: 600;
  line-height: 1.4;
}

// 末行左侧「上次结果 + 状态标签」：间距取与上方 .dd-mobile-card__row 相同的 10px（全局 footer-main 是 8px），
// 标签的左缘才能和「最后运行 / 下次运行」两行的值对齐成一条竖线（三个标签字样都是 4 个字、同一个 min-width）。
.task-card__result {
  gap: 10px;
}

@media screen and (max-width: 768px) {
  .page-header {
    flex-direction: column;
    align-items: flex-start;
    gap: 10px;
    margin-bottom: 14px;

    h2 { font-size: 18px; }

    .header-actions {
      width: 100%;
      flex-wrap: wrap;
    }
  }
}

// ===== 入场动画 =====
// 只对卡片级容器（工具条 / 表格卡 / 移动列表）做克制的淡入上移 + 轻微错落；
// 不给表格每一行做 stagger（行数多会卡）。时长走令牌，prefers-reduced-motion 时令牌自动降为 1ms 即等效关闭。
@keyframes dd-tasks-rise-in {
  from {
    opacity: 0;
    transform: translateY(12px);
  }
  to {
    opacity: 1;
    transform: translateY(0);
  }
}

.toolbar,
.task-mobile-toolbar,
.table-card,
.dd-mobile-list {
  animation: dd-tasks-rise-in var(--dd-motion-page) var(--dd-ease-decelerate) both;
}

// 轻微错落：工具条先入，表格卡/移动列表略晚
.table-card,
.dd-mobile-list {
  animation-delay: 60ms;
}

// 桌面拖拽手柄（与环境变量页 .env-drag-handle 同款）
.task-drag-handle {
  cursor: grab;
  color: var(--el-text-color-placeholder);
  font-size: 16px;
  transition: color var(--dd-motion-fast) var(--dd-ease-standard);

  &:hover {
    color: var(--el-text-color-secondary);
  }

  &:active {
    cursor: grabbing;
  }

  // 禁用态只改观感、不改 DOM：手柄要留在原地才能挂 tooltip 说明为什么拖不了。
  // 真正的「拖不动」由 queueSortableInit 直接销毁 sortable 实例保证，不是靠这几行 CSS。
  &.is-disabled {
    cursor: not-allowed;
    opacity: 0.35;

    &:hover {
      color: var(--el-text-color-placeholder);
    }
  }
}

:deep(.task-drag-col) {
  padding: 0 !important;
}
</style>

<style lang="scss">
/* 这些类由 sortablejs 在运行时动态加到 el-table 行 / body 上的拖拽克隆上，
   不在本组件 scoped 作用域内，必须用全局样式才能命中。

   ⚠️ 与 web/src/views/envs/index.vue 末尾那一段【内容完全相同】，属于有意重复：
   组件样式是随组件模块按需注入的，只进任务页不进环境变量页时那一份根本不会被加载，
   不自带一份的话拖起来的行会是一片透明、看不出落点。选择器与声明逐字一致，两份同时在也不会互相打架。 */

/* 被拖起的克隆行：不做放大与投影，改为实底 + 主色描边标出正在拖动的行。
   用 outline 而不是 border：el-table 是 border-collapse: separate，tr 上的 border 不会绘制。 */
.sortable-drag {
  background: var(--el-bg-color);
  opacity: 1 !important;
  border-radius: 0;
  cursor: grabbing;
  outline: 1px solid var(--el-color-primary);
  outline-offset: -1px;
}

/* 落点占位：高亮"插槽"，清楚指示会落到哪 */
.sortable-ghost {
  background: var(--el-color-primary-light-9) !important;
  border-radius: 0;
}
.sortable-ghost > td {
  opacity: 0.25;
}

/* 被选中的原行 */
.sortable-chosen {
  cursor: grabbing;
}
</style>
