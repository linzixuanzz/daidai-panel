<script setup lang="ts">
import { computed, ref, watch, onMounted } from 'vue'
import { taskViewApi, type TaskView, type TaskViewFilter, type TaskViewSortRule } from '@/api/taskView'
import { taskApi, type TaskGroupSummary } from '@/api/task'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Plus, Delete, Close, Edit, Setting, Folder } from '@element-plus/icons-vue'
import { useResponsive } from '@/composables/useResponsive'
import { usePageActivity } from '@/composables/usePageActivity'
import { ensureListPreferencesLoaded, readListPreference } from '@/utils/listPreferences'
import ViewManagementDialog from './ViewManagementDialog.vue'

const emit = defineEmits<{
  'view-change': [filters: TaskViewFilter[], sortRules: TaskViewSortRule[]]
}>()

const { dialogFullscreen, isMobile } = useResponsive()
const views = ref<TaskView[]>([])
// 当前高亮项由两个互斥的 ref 共同表达：选中视图时 activeViewId 是它的 id、activeGroupName 为 null；
// 选中分组标签时反过来。两者都为 null 有两层含义：一是「不带任何筛选」，二是「全部」标签处于高亮。
// 「全部」被隐藏后第二层含义失效 —— 此时仍可能短暂停在两者皆 null（比如刚删掉当前选中的视图），
// 标签栏不会有任何高亮项，但 emit 出去的 filters 同样是空数组、列表不带筛选，两者是一致的。
// 随后的 applyViewFallback() 会把它落到第一个可见视图。
const activeViewId = ref<number | null>(null)
// 分组标签（issue #130）没有库里的行、只能按名字认，所以单独一个 ref，
// 而不是往 number 型的 activeViewId 里硬塞字符串（那样每个比较点都要多一层类型判断）。
const activeGroupName = ref<string | null>(null)
const showDialog = ref(false)
// 提交在途锁：弹窗要等请求成功才关，期间不锁按钮连点就会建出多个同名视图。
// 写法与同目录 ViewManagementDialog 的 saving 保持一致。
const saving = ref(false)
const isEditMode = ref(false)
const editingViewId = ref<number | null>(null)
const showManagementDialog = ref(false)

const visibleViews = computed(() => views.value.filter(view => !view.hidden))

// 「全部」是模板里硬编码的内置筛选项，库里没有对应行，task_views 的 hidden 字段够不到它；
// 分组标签（issue #130）同理：它们来自任务 labels 里的 `分组:` 标签，不在 task_views 表里，
// 照「全部」的先例只做一个整体开关（不逐个分组隐藏）。
// 这两个开关是【个人偏好】，v3.3.1（issue #143 桌面端第 1 条）起跟随账户，读写都经 utils/listPreferences.ts
// （服务端为真源、本机缓存兜首屏；老的 localStorage 键由它接管并自动迁移，本文件不再直接碰 localStorage）。
// 默认都显示，老用户升级后第一眼观感不变。
// 注意作用域不同：各自定义视图自己的 hidden 仍存在全局的 task_views 表里（不分用户）。
const allTabHidden = ref(readListPreference('tasks_view_all_hidden'))
const groupTabsHidden = ref(readListPreference('tasks_view_groups_hidden'))

// 保底规则：一个可见视图都没有时，忽略隐藏设置强制把「全部」放回来。
// 否则标签栏只剩右侧两个图标按钮，用户既没有可点的筛选项、也退不回不带筛选的状态。
// 刻意不把分组标签算进「可见项」：分组标签同样是筛选，只剩它们时照样退不回「不带筛选」。
const showAllTab = computed(() => !allTabHidden.value || visibleViews.value.length === 0)

// 分组标签（issue #130）：App 里建的分组就是任务 labels 里的 `分组:<名>` 标签，
// 两端数据本来就是同一份，但网页顶栏原来只渲染 task_views，看不到它们。
// 清单来自 GET /tasks/groups（契约 C2，按名称升序）；点一下按 filters 的 group 字段精确筛选（契约 C3），
// 不会像「labels 等于 X」那样误中同名的自定义标签或订阅名。
const groups = ref<TaskGroupSummary[]>([])
const showGroupTabs = computed(() => !groupTabsHidden.value && groups.value.length > 0)
const allTabActive = computed(() => activeViewId.value === null && activeGroupName.value === null)

// 「视图管理」入口出不出现：有自定义视图，或者有分组时都要给。
// 分组标签的显隐开关在管理弹窗里，一个自定义视图都没有的用户（很多人只用 App 建分组）也得能关掉它、关掉后也得能再打开。
// expose 给父组件：移动端这个入口挪进了工具栏的「+」菜单，两处用同一份判据。
const canManageViews = computed(() => views.value.length > 0 || groups.value.length > 0)

const filterFields = [
  { value: 'command', label: '命令' },
  { value: 'name', label: '名称' },
  { value: 'cron_expression', label: '定时规则' },
  { value: 'status', label: '状态' },
  { value: 'labels', label: '标签' },
  // 分组（issue #130，契约 C3）：只比任务的 `分组:` 标签，不会误中同名的自定义标签 / 订阅名。
  // App 会把视图的 filters 原样透传给 GET /tasks，所以这里建的「按分组」视图两端都能用。
  // ⚠️ 老后端不认这个字段，带它的视图在老后端上会筛成空列表。
  { value: 'group', label: '分组' },
  { value: 'subscription', label: '订阅' }
]

// 排序可选字段与筛选字段是同一份，含「分组」：服务端 task_query.go 的 comparePreparedTaskByRule
// 已有 case "group"（与 labels / subscription 同一套口径：不区分大小写，没有分组的按空串参与比较）。
// ⚠️ 老后端不认这个排序字段，会走 default 分支静默回落默认序（不报错），选了等于没排。
const sortFields = filterFields

const statusOptions = [
  { label: '已启用 / 空闲中', value: '1' },
  { label: '已禁用', value: '0' },
  { label: '运行中', value: '2' },
  { label: '排队中', value: '0.5' },
]

const filterOperators = [
  { value: 'contains', label: '包含' },
  { value: 'not_contains', label: '不包含' },
  { value: 'equals', label: '等于' },
  { value: 'not_equals', label: '不等于' }
]

const sortDirections = [
  { value: 'asc', label: '升序' },
  { value: 'desc', label: '降序' }
]

const editForm = ref({
  name: '',
  filters: [{ field: 'command', operator: 'contains', value: '' }] as TaskViewFilter[],
  sortRules: [] as TaskViewSortRule[]
})

async function loadViews() {
  try {
    views.value = await taskViewApi.list()
  } catch {
    views.value = []
  }
  applyViewFallback()
}

// 服务端契约是裸数组；老后端 404 会走 catch，演示站没注册这个端点时会兜底回 { data: [] } 这种对象，
// 这里一律收成「没有分组」。逐项再过一遍：名字必须是非空字符串，重名只留第一条
// （模板拿名字拼 :key，重复会让 Vue 报 Duplicate keys）。
function normalizeTaskGroups(raw: unknown): TaskGroupSummary[] {
  if (!Array.isArray(raw)) return []
  const seen = new Set<string>()
  const result: TaskGroupSummary[] = []
  for (const item of raw) {
    const name = typeof item?.name === 'string' ? item.name.trim() : ''
    if (!name || seen.has(name)) continue
    seen.add(name)
    const count = Number(item?.count)
    result.push({ name, count: Number.isFinite(count) ? count : 0 })
  }
  return result
}

// 在途去重：挂载、每次切换筛选、页面重新可见这几路可能撞在一起，同一时刻只发一个请求，
// 后来者直接复用在途那一次（它刚发出去，结果足够新）。
// 顺带挡住一条回环：本函数末尾的 applyViewFallback 可能 selectView → emitViewChange → 再调本函数，
// 那一刻在途标记还没清，拿到的是同一个 promise，不会再发第二个请求。
let groupsLoading: Promise<void> | null = null

function loadGroups(): Promise<void> {
  if (!groupsLoading) {
    groupsLoading = (async () => {
      try {
        groups.value = normalizeTaskGroups(await taskApi.groups())
      } catch {
        // 老后端没有这个接口（404）、应用令牌缺 tasks 权限（403）、网络抖动：一律静默，不弹错。
        // 从没成功过 → groups 仍是空数组，顶栏不出现分组标签；
        // 成功过 → 保留上一次的清单：一次抖动不该把用户正在用的分组标签闪没、把筛选回落掉。
        return
      }
      // 只在「当前停在某个分组上」时校正（applyViewFallback 的第 4 条），视图相关的回落只归 loadViews 管。
      // 不能在这里跑整套规则：删掉当前视图时 doDeleteView 先 selectView(null)（顺带发出本请求）再 await loadViews()，
      // 本请求若先回来，views 里还留着被删的那条 ——「全部」被隐藏、被删的又恰好排在第一个可见时，
      // 整套规则会把它重新选回来、按它的筛选再拉一次列表，等 loadViews 回来才又改选。
      if (activeGroupName.value !== null) {
        applyViewFallback()
      }
    })().finally(() => {
      groupsLoading = null
    })
  }
  return groupsLoading
}

// 视图列表、分组清单或各类显隐变化后校正当前高亮项，几处硬回退点
// （首次加载 / 管理弹窗保存 / 删除视图 / 分组清单刷新）都汇到这里，避免各写一份规则跑偏：
//   1. 当前选中的视图被删除或被隐藏了 —— 不能继续停在一个看不见的标签上；
//   2. 「全部」被隐藏而当前停在「全部」语义上（两个 ref 皆 null）—— 必须落到第一个可见视图，
//      否则标签栏一个高亮都没有；
//   3. 一个可见视图都没有时 showAllTab 会强制把「全部」放回来，此时停在「全部」反而是对的；
//   4. 当前选中的分组在 App 里被改名 / 解散了（清单里没了），或分组标签被整体隐藏了 —— 同第 1 条。
function applyViewFallback() {
  if (activeGroupName.value !== null) {
    const name = activeGroupName.value
    if (showGroupTabs.value && groups.value.some(group => group.name === name)) {
      return
    }
  } else if (activeViewId.value !== null) {
    const current = views.value.find(v => v.id === activeViewId.value)
    if (current && !current.hidden) {
      return
    }
  } else if (showAllTab.value) {
    return
  }
  // 走到这里说明当前高亮项已经不可见：优先回落「全部」，「全部」也被隐藏时落到第一个可见视图
  selectView(showAllTab.value ? null : (visibleViews.value[0]?.id ?? null))
}

function openManagementDialog() {
  showManagementDialog.value = true
}

async function handleManagementSaved(allHidden: boolean, groupsHidden: boolean) {
  // 「全部」与顶栏分组页签都不进 taskViewApi.reorder 的提交列表，它们的显隐是账户偏好。
  // #157 ③ 起由管理弹窗自己经 listPreferences.saveListPreferences 写进账户、【等服务端写入成功】才回传到这里
  // （只写真的变了的键，口径见弹窗的 handleSave），这里只负责应用到顶栏。
  // 原来是这里发 PUT 不管结果，弹窗却先提示了「已保存」：PUT 一失败，刷新后页签又出现。
  // 分组页签被隐藏时若正选中某个分组，下面 loadViews 末尾的 applyViewFallback 会把它回落掉。
  allTabHidden.value = allHidden
  groupTabsHidden.value = groupsHidden
  await loadViews()
}

// 所有 view-change 都从这里发，发完顺手刷新一次分组清单。
// 分组来自任务 labels：任务的增删改、导入、订阅拉取、App 端改分组都会让它变，而这些都发生在
// index.vue 或别的端上。本组件在「挂载 / 每次切换筛选 / 页面重新可见」三个时机自己拉（另外两处调用见文件末尾）；
// #157 起 index.vue 在本页任务创建 / 更新 / 复制 / 删除 / 批量加标签 / 导入成功后也会调 expose 出去的 loadGroups，
// 改了分组不用刷新页面，顶栏分组页签当场就对。
function emitViewChange(filters: TaskViewFilter[], sortRules: TaskViewSortRule[]) {
  emit('view-change', filters, sortRules)
  void loadGroups()
}

function selectView(viewId: number | null) {
  activeViewId.value = viewId
  activeGroupName.value = null
  if (!viewId) {
    emitViewChange([], [])
    return
  }
  const view = views.value.find(v => v.id === viewId)
  if (view) {
    try {
      const filters = JSON.parse(view.filters || '[]')
      const sortRules = JSON.parse(view.sort_rules || '[]')
      emitViewChange(filters, sortRules)
    } catch {
      emitViewChange([], [])
    }
  }
}

// 分组标签 = 一条临时的「group 等于 X」视图（契约 C3）。不带排序规则，所以列表的拖拽排序照常可用
// （index.vue 的 dragSortDisabledReason 只看 sort_rules），handleViewChange / buildTaskListParams 一行不用改。
function selectGroup(name: string) {
  activeViewId.value = null
  activeGroupName.value = name
  emitViewChange([{ field: 'group', operator: 'equals', value: name }], [])
}

function openCreateDialog() {
  isEditMode.value = false
  editingViewId.value = null
  editForm.value = {
    name: '',
    filters: [{ field: 'command', operator: 'contains', value: '' }],
    sortRules: []
  }
  showDialog.value = true
}

function openEditDialog(view: TaskView) {
  isEditMode.value = true
  editingViewId.value = view.id
  let filters: TaskViewFilter[] = []
  let sortRules: TaskViewSortRule[] = []
  try {
    filters = JSON.parse(view.filters || '[]')
  } catch { /* ignore */ }
  try {
    sortRules = JSON.parse(view.sort_rules || '[]')
  } catch { /* ignore */ }
  if (filters.length === 0) {
    filters = [{ field: 'command', operator: 'contains', value: '' }]
  }
  editForm.value = {
    name: view.name,
    filters,
    sortRules
  }
  showDialog.value = true
}

function addFilter() {
  editForm.value.filters.push({ field: 'command', operator: 'contains', value: '' })
}

function removeFilter(index: number) {
  editForm.value.filters.splice(index, 1)
}

function addSortRule() {
  editForm.value.sortRules.push({ field: 'name', direction: 'asc' })
}

function removeSortRule(index: number) {
  editForm.value.sortRules.splice(index, 1)
}

function isStatusField(filter: TaskViewFilter) {
  return filter.field === 'status'
}

async function handleSave() {
  if (!editForm.value.name.trim()) {
    ElMessage.warning('请输入视图名称')
    return
  }
  const validFilters = editForm.value.filters.filter(f => f.value.trim() !== '')
  if (validFilters.length === 0) {
    ElMessage.warning('请至少添加一个有效的筛选条件')
    return
  }
  // 两处校验早退都过了才置位，否则校验失败会把按钮永久锁死
  saving.value = true
  try {
    const payload = {
      name: editForm.value.name,
      filters: JSON.stringify(validFilters),
      sort_rules: JSON.stringify(editForm.value.sortRules)
    }
    if (isEditMode.value && editingViewId.value) {
      await taskViewApi.update(editingViewId.value, payload)
      ElMessage.success('视图更新成功')
      // If editing the active view, re-apply its filters
      if (activeViewId.value === editingViewId.value) {
        emitViewChange(validFilters, editForm.value.sortRules)
      }
    } else {
      await taskViewApi.create(payload)
      ElMessage.success('视图创建成功')
    }
    showDialog.value = false
    await loadViews()
  } catch (err: any) {
    // task_views.name 现在是唯一索引，后端把冲突翻译成了面向用户的中文 400
    // （新建与改名两条路径都会返回「同名任务视图已存在」）。
    // 这里必须优先取后端文案，否则那条提示在 UI 上是死的，用户只会看到笼统的「创建失败」。
    ElMessage.error(err?.response?.data?.error || (isEditMode.value ? '更新失败' : '创建失败'))
  } finally {
    saving.value = false
  }
}

async function handleDelete(viewId: number) {
  try {
    await ElMessageBox.confirm('确认删除该视图吗？', '删除确认', { type: 'warning' })
    await doDeleteView(viewId)
  } catch {}
}

async function doDeleteView(viewId: number) {
  try {
    await taskViewApi.delete(viewId)
    ElMessage.success('已删除')
    if (activeViewId.value === viewId) {
      // 先清掉高亮与筛选（被删的视图不该继续作用于列表），
      // 具体落到「全部」还是第一个可见视图，交给刷新后的 applyViewFallback() 统一判断，
      // 因为此刻 views 里还留着刚删掉的那一条，就地挑「第一个可见视图」可能挑到它自己。
      selectView(null)
    }
    await loadViews()
  } catch {}
}

onMounted(() => {
  void loadViews()
  void loadGroups()
  // 两个隐藏开关跟随账户：setup 时先按本机缓存显示，服务端值回来后重读一遍，再按新值校正一次高亮项
  // （比如服务端存的是「隐藏全部」，要落到第一个可见视图）。与任务页调用的是同一个记忆化的 ensure，只发一次请求。
  // 先后顺序无所谓：视图列表还没回来时 showAllTab 保底为真，applyViewFallback 是空操作，等 loadViews 回来会再校正一次。
  void ensureListPreferencesLoaded().then(() => {
    allTabHidden.value = readListPreference('tasks_view_all_hidden')
    groupTabsHidden.value = readListPreference('tasks_view_groups_hidden')
    applyViewFallback()
  })
})

// 分组清单的第三个刷新时机：页面重新可见 —— 切回这个浏览器标签页，
// 或从别的菜单页切回任务页（MainLayout 是 keep-alive，二次进页只触发 activated、不会重新挂载）。
// isPageActive 两种情况都覆盖；挂载那一刻它本来就是 true，watch 不会多发一次。
const { isPageActive } = usePageActivity()
watch(isPageActive, (active) => {
  if (active) void loadGroups()
})

// openCreateDialog / openManagementDialog / canManageViews：移动端「新建视图」「视图管理」两个入口
// 由任务页工具栏的「+」菜单承担（本组件在移动端不渲染右侧那两个按钮），经这里打开同一个弹窗。
// loadGroups：任务页在任务改动成功后调它刷新顶栏分组页签（#157，见 emitViewChange 上方的说明）。
defineExpose({ loadViews, loadGroups, openCreateDialog, openManagementDialog, canManageViews })
</script>

<template>
  <div class="view-manager">
    <!-- 槽内只放筛选项（全部 + 各视图 + 分组标签），动作按钮（新建 / 视图管理）放槽外：
         「选哪一个」与「做什么」语义分开，顺带消掉了原来「全部」24px、视图 32px 的高度不一致。
         移动端（v3.3.1，issue #143；留白口径 v3.3.2 / issue #144 修正）：槽独占一行、单行横滑，
         左右停在页面 12px 留白线上（#143 当初是贴满屏幕边缘，会吃掉系统侧滑返回手势，已改回留白）。
         两个全局共享类只在移动端挂上，桌面 DOM 与换行行为不变；
         槽外两个动作按钮不渲染，入口挪进了任务页工具栏的「+」菜单。 -->
    <div class="view-tabs">
      <div class="view-seg" :class="{ 'dd-scroll-row': isMobile, 'dd-mobile-bleed': isMobile }">
        <button
          v-if="showAllTab"
          :class="['view-tab', { active: allTabActive }]"
          @click="selectView(null)"
        >
          全部
        </button>
        <button
          v-for="view in visibleViews"
          :key="view.id"
          :class="['view-tab', { active: activeViewId === view.id }]"
          @click="selectView(view.id)"
        >
          {{ view.name }}
        </button>
        <!-- 分组标签（issue #130）排在自定义视图之后、放在同一个灰底槽里：三类标签同一时刻只能选一个，
             同槽正好表达「单选」（另起一个槽会被读成可以和视图叠加的第二个维度）。
             与自己建的视图靠「竖线分隔 + 文件夹图标」区分。竖线画在第一个分组标签自己的伪元素上
             （view-tab--group-first），不再是槽里单独的一个子项：槽会换行，单独的竖线可能孤零零落在上一行末尾。
             前面恒有东西可分隔：没有可见视图时 showAllTab 会强制显示「全部」。 -->
        <template v-if="showGroupTabs">
          <button
            v-for="(group, index) in groups"
            :key="`group:${group.name}`"
            type="button"
            :class="['view-tab', 'view-tab--group', { 'view-tab--group-first': index === 0, active: activeGroupName === group.name }]"
            :title="`分组：${group.name}（${group.count} 个任务）`"
            @click="selectGroup(group.name)"
          >
            <el-icon class="view-tab__icon"><Folder /></el-icon>
            <span class="view-tab__label">{{ group.name }}</span>
          </button>
        </template>
      </div>
      <div v-if="!isMobile" class="view-tabs__actions">
        <el-tooltip content="新建视图" placement="top">
          <el-button @click="openCreateDialog">
            <el-icon><Plus /></el-icon>
          </el-button>
        </el-tooltip>
        <!-- 有分组时也要给入口，判据见 canManageViews -->
        <el-tooltip v-if="canManageViews" content="视图管理" placement="top">
          <el-button @click="openManagementDialog">
            <el-icon><Setting /></el-icon>
          </el-button>
        </el-tooltip>
      </div>
    </div>

    <ViewManagementDialog
      v-model="showManagementDialog"
      :views="views"
      :all-hidden="allTabHidden"
      :groups-hidden="groupTabsHidden"
      :group-count="groups.length"
      @saved="handleManagementSaved"
      @views-saved="loadViews"
      @edit="openEditDialog"
      @delete="doDeleteView"
    />

    <el-dialog
      v-model="showDialog"
      :title="isEditMode ? '编辑视图' : '创建视图'"
      width="600px"
      :fullscreen="dialogFullscreen"
      :lock-scroll="false"
    >
      <el-form :label-width="dialogFullscreen ? 'auto' : '90px'" :label-position="dialogFullscreen ? 'top' : 'right'">
        <el-form-item label="视图名称" required>
          <el-input v-model="editForm.name" placeholder="请输入视图名称" />
        </el-form-item>

        <el-form-item label="筛选条件" required>
          <div class="filter-list">
            <div v-for="(filter, index) in editForm.filters" :key="index" class="filter-row">
              <el-select v-model="filter.field" style="width: 120px" size="small" @change="filter.value = ''">
                <el-option v-for="f in filterFields" :key="f.value" :label="f.label" :value="f.value" />
              </el-select>
              <el-select v-model="filter.operator" style="width: 100px" size="small">
                <el-option v-for="op in filterOperators" :key="op.value" :label="op.label" :value="op.value" />
              </el-select>
              <el-select
                v-if="isStatusField(filter)"
                v-model="filter.value"
                placeholder="选择状态"
                size="small"
                style="flex: 1"
              >
                <el-option v-for="opt in statusOptions" :key="opt.value" :label="opt.label" :value="opt.value" />
              </el-select>
              <el-input v-else v-model="filter.value" placeholder="请输入内容" size="small" style="flex: 1" />
              <el-button v-if="editForm.filters.length > 1" :icon="Delete" size="small" @click="removeFilter(index)" />
            </div>
            <el-button size="small" type="primary" link @click="addFilter">+ 新增筛选条件</el-button>
          </div>
        </el-form-item>

        <el-form-item label="排序方式">
          <div class="filter-list">
            <div v-for="(rule, index) in editForm.sortRules" :key="index" class="filter-row">
              <el-select v-model="rule.field" style="width: 120px" size="small">
                <el-option v-for="f in sortFields" :key="f.value" :label="f.label" :value="f.value" />
              </el-select>
              <el-select v-model="rule.direction" style="width: 100px" size="small">
                <el-option v-for="d in sortDirections" :key="d.value" :label="d.label" :value="d.value" />
              </el-select>
              <el-button :icon="Delete" size="small" @click="removeSortRule(index)" />
            </div>
            <el-button size="small" type="primary" link @click="addSortRule">+ 新增排序方式</el-button>
          </div>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="showDialog = false">取消</el-button>
        <el-button type="primary" :loading="saving" :disabled="saving" @click="handleSave">{{ isEditMode ? '保存' : '创建' }}</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<style scoped lang="scss">
.view-manager {
  margin-bottom: 12px;
}

.view-tabs {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  align-items: center;
}

// 灰底槽：数值逐条对齐 tasks/index.vue 的 .status-tabs，让上下两排分段控件观感一致
.view-seg {
  display: inline-flex;
  background: var(--el-fill-color-light);
  // 灰底槽取 control 档，与全站通用类 .dd-seg-group、tasks/index.vue 的 .status-tabs 同档。
  // 槽 padding 3px ⇒ 内侧曲率 = 槽圆角 - 3px；取 control(6px) 时内侧 3px 小于项的 6px，
  // 选中项的角稳稳落在槽内侧曲线之内；取 surface(10px) 时内侧 7px 反而大于 6px，会露出错位。
  border-radius: var(--dd-radius-control);
  padding: 3px;
  gap: 2px;
  // 视图数量由用户决定、可能很多，桌面保留原来的换行而不是照抄 status-tabs 的单行排布。
  // 取舍：换行会让灰底槽变成两行、把下方工具栏推低；改成横向滚动虽然能锁死高度，
  // 但滚动条会遮住选中项、也没有溢出提示，权衡后选「宁可换行也别把视图藏起来」。
  // 移动端（v3.3.1，issue #143）反过来改成单行横滑，见文件末尾的移动端媒体查询。
  flex-wrap: wrap;
  // inline-flex 的基准宽是 max-content，窄屏下不加这条会顶破容器让页面横向滚
  max-width: 100%;
}

.view-tab {
  padding: 6px 14px;
  // 槽内的分段项 → control 档
  border-radius: var(--dd-radius-control);
  // 透明描边占位，选中时只换 border-color，避免出现 1px 的尺寸跳动
  border: 1px solid transparent;
  background: transparent;
  color: var(--dd-text-2);
  font-size: 13px;
  font-weight: 500;
  cursor: pointer;
  // 时长/缓动只能取令牌：写死毫秒会绕过 prefers-reduced-motion 下把令牌压到 1ms 的降级
  transition:
    color var(--dd-motion-fast) var(--dd-ease-standard),
    background-color var(--dd-motion-fast) var(--dd-ease-standard),
    border-color var(--dd-motion-fast) var(--dd-ease-standard);
  white-space: nowrap;

  &:hover {
    color: var(--dd-text);
  }

  // 从 el-button 换成原生 button 后丢了 EP 的焦点环，键盘走查会看不出焦点落在哪一项。
  // offset 取负值让焦点环画在按钮内侧：槽内 gap 只有 2px，正向外扩会压到相邻项上。
  &:focus-visible {
    outline: 2px solid var(--dd-accent-ring);
    outline-offset: -1px;
    // 焦点环沿 border-radius 描边，要跟分段项本身同档，否则圆角模式下环是方的、项是圆的
    border-radius: var(--dd-radius-control);
  }

  &.active {
    background: var(--dd-surface-solid);
    color: var(--dd-accent-ink);
    border-color: var(--dd-border);
    font-weight: 600;
  }
}

// 自定义视图与分组标签之间的竖向分隔线（issue #130）。
// 画在第一个分组标签自己的 ::before 上，不再是槽里一个独立的 flex 子项：槽是 flex-wrap 的，
// 视图、分组都多而窗口又窄时，独立的竖线会单独留在上一行末尾，成了一根孤零零的线；
// 挂在标签自身上，换行时它始终紧贴第一个分组标签左侧、跟着一起走。
// 间距照原来的独立元素复刻：gap 2 + 外边距 4 + 线 1 + 外边距 4 + gap 2 = 13px ——
// 这里用左外边距 11px（加上槽的 gap 2 正好 13px）让出位置，线落在正中、两侧各留 6px。
// 只是一条 1px 的线，不是表面，不涉及圆角；高 16px 约为项高 33px 的一半，居中不顶槽边。
.view-tab--group-first {
  position: relative;
  margin-left: 11px;

  &::before {
    content: '';
    position: absolute;
    // 绝对定位以内边距盒为参照，它比边框盒往里缩了 1px（.view-tab 的透明描边占位）：
    // -8px 让线的左缘落在边框盒左缘外 7px，线右侧与标签之间正好 6px
    left: -8px;
    top: calc(50% - 8px);
    width: 1px;
    height: 16px;
    background: var(--dd-border-strong);
    pointer-events: none;
  }
}

// 分组标签：前置文件夹图标 + 名称。尺寸、配色、选中态全部沿用 .view-tab，与上一排分段控件逐项对齐；
// inline-flex + center 下图标（13px）比文字行矮，项高不变，仍是 33px。
.view-tab--group {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  // 分组名是 App 里的自由输入，可能很长；单行省略，全名与任务数挂在按钮的 title 上
  max-width: 16em;
}

.view-tab__icon {
  flex-shrink: 0;
  font-size: 13px;
}

.view-tab__label {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
}

// 动作按钮在槽外，高度对齐灰底槽（3 + 33 + 3 = 39px），
// 不再出现原来「图标按钮 24px vs 分段控件 39px」那种半截高的落差。
.view-tabs__actions {
  display: flex;
  align-items: center;
  gap: 6px;

  // 锚点是自己模板里的元素，压 el-button 用 :deep()；
  // 同时清掉 EP 自带的 .el-button + .el-button{margin-left:12px}，间距只由 gap 决定
  :deep(.el-button) {
    height: 39px;
    width: 39px;
    padding: 0;
    margin-left: 0;
  }
}

.filter-list {
  width: 100%;
}

.filter-row {
  display: flex;
  gap: 8px;
  align-items: center;
  margin-bottom: 8px;
}

@media (max-width: 768px) {
  .filter-row {
    flex-wrap: wrap;
  }

  // 移动端视图分组栏（issue #143 T7/T8；留白与圆角口径 v3.3.2 / issue #144 修正）：
  // 灰底槽独占一行、单行左右滑动、隐藏滚动条，左右停在页面 12px 留白线上并吃「按钮」档圆角。
  // 横滑与这条留白由模板里只在移动端挂上的 .dd-scroll-row / .dd-mobile-bleed 两个全局类负责
  // （滚动、隐藏滚动条、子项不收缩、宽度拉满一行、槽本身的圆角）。
  // 这里只补被本文件 .view-seg 那条（scoped 后 (0,2,0)，压过全局单类）盖掉的两项：
  // 块级 flex（inline-flex 会按内容收宽、滚不起来）与不换行。gap 仍用本文件的 2px，与桌面分段控件一致。
  // 为什么手机上改横滑：窄屏上换行会把灰底槽堆成好几行，工具栏与列表被一路推到屏幕下半截；
  // 手机上左右滑动是熟悉的手势，槽的右缘被截断本身就在提示「还有更多」。
  .view-seg.dd-scroll-row {
    display: flex;
    flex-wrap: nowrap;
  }

  // 槽圆了、项还是桌面的 control 档（6px）的话，四角那 3px 灰边会被白色选中项几乎戳没
  // （槽 16 / 项 6 时，项的左上圆弧顶点到裁剪圆心只有 15.9px，离 16px 的边只剩 0.1px）。
  // 减 3 正好是槽的 padding，项的圆弧与槽外圆弧同心，四周灰边恒为 3px。
  // 必须限定 .dd-scroll-row：桌面那份 .view-tab 仍走本文件上面的 control 档，不受影响。
  // square 模式下 calc(0px - 3px) 会被 CSS 的取值范围夹到 0，不会出负圆角 ——
  // 这是实测结论，global.scss 的 .el-button--small{calc(var(--el-border-radius-base) - 1px)} 同款写法已验证过。
  .view-seg.dd-scroll-row .view-tab {
    border-radius: calc(var(--dd-radius-button) - 3px);
  }
}
</style>
