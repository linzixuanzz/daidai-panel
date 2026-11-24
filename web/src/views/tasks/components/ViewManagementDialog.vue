<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Rank, View, Hide, Edit, Delete, Lock } from '@element-plus/icons-vue'
import { taskViewApi, type TaskView } from '@/api/taskView'
import { useResponsive } from '@/composables/useResponsive'
import { saveListPreferences, type ListPreferences } from '@/utils/listPreferences'

interface ManagedView extends TaskView {
  // Local working copy of `hidden` that may diverge from the saved value until
  // the user clicks 保存. The original `hidden` is kept on the TaskView shape.
  _hidden: boolean
}

const props = defineProps<{
  modelValue: boolean
  views: TaskView[]
  // 「全部」的当前显隐状态。它是标签栏里硬编码的内置项、库里没有对应行、没有 task_views 的 hidden 字段可写，
  // 所以由父组件（ViewManager）传进来当基准。点保存时由本弹窗经 utils/listPreferences.ts 的 saveListPreferences
  // 写进账户、等到服务端写入成功才回传给父组件应用（v3.3.1 起跟随账户；等写入结果是 #157 ③ 起）。
  allHidden: boolean
  // 顶栏分组页签（issue #130）的整体显隐，与「全部」同一套来回：分组来自任务 labels 里的 `分组:` 标签，
  // 同样没有 task_views 行，同样经 listPreferences 写进账户。
  groupsHidden: boolean
  // 当前有几个分组，只用来写说明文字
  groupCount: number
}>()

const emit = defineEmits<{
  'update:modelValue': [value: boolean]
  // 全部保存成功：两个内置开关的新值（已写进账户），父组件应用到顶栏并刷新视图列表
  saved: [allHidden: boolean, groupsHidden: boolean]
  // 视图的顺序与显隐已经写进服务端、两个内置开关却没写成：请父组件按服务端刷新视图列表（弹窗不关）
  'views-saved': []
  edit: [view: TaskView]
  delete: [viewId: number]
}>()

const { dialogFullscreen } = useResponsive()
const saving = ref(false)
const managed = ref<ManagedView[]>([])
// 「全部」显隐的本地工作副本，和各视图的 _hidden 一样，点保存前不落地
const allTabHidden = ref(props.allHidden)
// 顶栏分组页签显隐的本地工作副本，规则同上
const groupTabsHidden = ref(props.groupsHidden)
const listContainer = ref<HTMLElement | null>(null)
let sortableInstance: any = null
let sortableLoader: Promise<any> | null = null
// 打开时（以及视图列表被父组件按服务端刷新后）各视图的顺序与显隐，用来判断有没有没保存的改动（#157 ③）
let savedViewsSignature = ''

// 计数只统计自定义视图，刻意不把「全部」算进去：它不占 sort_order、不能删除、也不在下面的列表里，
// 算进去会让「共 N 个」与用户数得到的行数对不上。文案里直接写明「自定义视图」，避免口径歧义。
const hiddenCount = computed(() => managed.value.filter(v => v._hidden).length)
const visibleCount = computed(() => managed.value.length - hiddenCount.value)

function cloneToManaged(source: TaskView[]): ManagedView[] {
  return source.map(v => ({ ...v, _hidden: Boolean(v.hidden) }))
}

function viewsSignature(list: ManagedView[]) {
  return list.map(view => `${view.id}:${view._hidden ? 1 : 0}`).join(',')
}

// 有没有没保存的改动（#157 ③）：视图顺序变了、任一视图显隐变了、两个内置开关与打开时不同（基准就是父组件当前值）
function hasUnsavedChanges() {
  return viewsSignature(managed.value) !== savedViewsSignature
    || allTabHidden.value !== props.allHidden
    || groupTabsHidden.value !== props.groupsHidden
}

// 有没保存的改动时先确认，返回 true 表示可以关。
// 只挂在「用户要关窗」的几个入口上（×、Esc、取消、编辑）。保存成功后是直接 emit 关窗、不经过这里，确认框挡不住正常保存。
// 原来拨了开关点 × / 取消 / Esc 会静默丢弃，刷新后页签还在，用户以为「关了又出现」。
async function confirmDiscardChanges(): Promise<boolean> {
  if (!hasUnsavedChanges()) return true
  try {
    await ElMessageBox.confirm('放弃未保存的修改？', '提示', {
      type: 'warning',
      confirmButtonText: '放弃修改',
      cancelButtonText: '继续编辑'
    })
    return true
  } catch {
    return false
  }
}

function loadSortable() {
  if (!sortableLoader) {
    sortableLoader = import('sortablejs').then(mod => mod.default)
  }
  return sortableLoader
}

async function initSortable() {
  teardownSortable()
  if (!listContainer.value) return
  try {
    const Sortable = await loadSortable()
    sortableInstance = Sortable.create(listContainer.value, {
      animation: 150,
      handle: '.view-drag-handle',
      ghostClass: 'view-row-ghost',
      chosenClass: 'view-row-chosen',
      dragClass: 'view-row-dragging',
      forceFallback: true,
      onEnd: (evt: any) => {
        const { oldIndex, newIndex } = evt
        if (oldIndex == null || newIndex == null || oldIndex === newIndex) return
        const [moved] = managed.value.splice(oldIndex, 1)
        if (!moved) return
        managed.value.splice(newIndex, 0, moved)
      }
    })
  } catch (err) {
    console.warn('failed to init sortable for view manager', err)
  }
}

function teardownSortable() {
  if (sortableInstance) {
    sortableInstance.destroy()
    sortableInstance = null
  }
}

function toggleHidden(view: ManagedView) {
  view._hidden = !view._hidden
}

async function handleEdit(view: ManagedView) {
  // 编辑会关掉本弹窗，没保存的顺序 / 显隐同样会丢，先确认
  if (!(await confirmDiscardChanges())) return
  emit('edit', view as TaskView)
  emit('update:modelValue', false)
}

async function handleDelete(view: ManagedView) {
  try {
    await ElMessageBox.confirm(`确认删除视图「${view.name}」吗？`, '删除确认', { type: 'warning' })
    emit('delete', view.id)
  } catch {
    // cancelled
  }
}

async function handleSave() {
  saving.value = true
  // 视图那一半写成功了没有：后面的偏好写失败时，要据此让父组件按服务端刷新视图列表
  let viewsSaved = false
  try {
    // 「全部」与「顶栏分组页签」都不参与 sort_order 重编号、也不进 reorder 的提交列表 —— 库里根本没有这两行，
    // 它们的显隐走下面的账户偏好。所以一个自定义视图都没有时也仍然要走到下面。
    if (managed.value.length > 0) {
      // Dense re-numbering keeps sort_order contiguous and mirrors the
      // visible list order.
      const payload = managed.value.map((view, index) => ({
        id: view.id,
        sort_order: (index + 1) * 10,
        hidden: view._hidden
      }))
      await taskViewApi.reorder(payload)
      viewsSaved = true
    }
    // 两个内置开关跟随账户（PUT /auth/preferences）。只提交【真的变了】的键：服务端对这组偏好是稀疏存储，
    // 没改也写一次就等于替用户「占坑」存下默认值，会挡住他在别的域名 / IP 上存过、还没迁上来的值。
    // 两个都变了时合进同一个 patch，一次 PUT 带齐。
    // 🔴 要等写入结果（#157 ③）：原来先提示「已保存」并当场隐藏页签、PUT 发出去不管，PUT 一失败，
    //    刷新后服务端旧值把本机缓存冲回去、页签又出现。现在写失败时 saveListPreferences 已把本机缓存回滚，下面报错、弹窗不关。
    const patch: Partial<ListPreferences> = {}
    if (allTabHidden.value !== props.allHidden) {
      patch.tasks_view_all_hidden = allTabHidden.value
    }
    if (groupTabsHidden.value !== props.groupsHidden) {
      patch.tasks_view_groups_hidden = groupTabsHidden.value
    }
    if (Object.keys(patch).length > 0) {
      await saveListPreferences(patch)
    }
    ElMessage.success('视图设置已保存')
    emit('saved', allTabHidden.value, groupTabsHidden.value)
    emit('update:modelValue', false)
  } catch (err: any) {
    if (viewsSaved) {
      // 视图的顺序与显隐已经落库、只是开关没写成：让顶栏按服务端刷新视图，弹窗里改好的开关留着等用户重试。
      // 刷新回来的列表会经下面 props.views 的 watch 换掉工作副本与基准，那时只剩开关算「没保存」
      emit('views-saved')
    }
    // 有服务端文案就照旧显示；没有时（断网、超时、反代没回 JSON）不再透出 axios 的英文 err.message
    // （如「Network Error」「timeout of 30000ms exceeded」），统一给中文兜底
    ElMessage.error(err?.response?.data?.error || '保存失败，请检查网络后重试')
  } finally {
    saving.value = false
  }
}

// EP 关窗动画结束后回传的 update:model-value（以及别的程序化开关）：原样转给父组件
function handleClose(visible: boolean) {
  emit('update:modelValue', visible)
}

// × 与 Esc 走 el-dialog 的 before-close（点遮罩本来就不关）：确认之后调 done() 才真的关，不调就留在弹窗里
async function handleBeforeClose(done: () => void) {
  if (await confirmDiscardChanges()) done()
}

// 底部「取消」：手机全屏弹窗没有 ×（有 footer 时全局样式把它藏了），这是手机上唯一的关窗入口，同样先确认
async function handleCancel() {
  if (await confirmDiscardChanges()) emit('update:modelValue', false)
}

watch(
  () => props.modelValue,
  async (open) => {
    if (open) {
      managed.value = cloneToManaged(props.views)
      savedViewsSignature = viewsSignature(managed.value)
      // 每次打开都从父组件当前值重新取，避免上次取消掉的改动残留在工作副本里
      allTabHidden.value = props.allHidden
      groupTabsHidden.value = props.groupsHidden
      await nextTick()
      await initSortable()
    } else {
      teardownSortable()
    }
  }
)

watch(
  () => props.views,
  (next) => {
    if (props.modelValue) {
      managed.value = cloneToManaged(next)
      // 列表被父组件按服务端刷新了（删掉了某个视图、保存时视图那一半已落库）：未保存改动的基准跟着换，
      // 免得把已经落库的状态当成「没保存」
      savedViewsSignature = viewsSignature(managed.value)
    }
  },
  { deep: true }
)
</script>

<template>
  <el-dialog
    :model-value="modelValue"
    title="视图管理"
    width="520px"
    :fullscreen="dialogFullscreen"
    :lock-scroll="false"
    :close-on-click-modal="false"
    :before-close="handleBeforeClose"
    @update:model-value="handleClose"
  >
    <div class="view-manager-hint">
      拖动左侧把手调整顺序，右侧开关控制是否在标签栏展示；「全部」与「顶栏分组页签」是内置项，只能改显隐。
      <!-- 计数刻意只报自定义视图的口径，并在文案里写明，避免与下方列表行数对不上 -->
      <span class="view-manager-counts">自定义视图 {{ managed.length }} 个 · 显示 {{ visibleCount }} · 隐藏 {{ hiddenCount }}</span>
    </div>

    <!-- 「全部」是标签栏里硬编码的内置筛选项，库里没有对应行：
         不可拖拽（不占 sort_order）、不可编辑（没有筛选条件）、不可删除，只给一个显示开关。
         放在 listContainer 之外，否则 sortable 会把它算进拖拽索引，和 managed 的下标对不上。 -->
    <div
      class="view-manager-row is-builtin"
      :class="{ 'is-hidden': allTabHidden }"
    >
      <span class="view-drag-handle is-locked" title="内置项，不参与排序">
        <el-icon><Lock /></el-icon>
      </span>
      <span class="view-row-name">
        全部
        <span class="view-row-note">内置项，不带任何筛选条件</span>
        <!-- 自定义视图全隐藏时标签栏会保底把「全部」放回来，这里提前讲清楚，
             免得用户保存后以为开关没生效 -->
        <span v-if="allTabHidden && visibleCount === 0" class="view-row-note is-warning">
          自定义视图全部隐藏时，标签栏仍会保底显示「全部」
        </span>
      </span>
      <div class="view-row-actions">
        <el-tooltip :content="allTabHidden ? '在标签栏隐藏' : '在标签栏显示'" placement="top">
          <el-switch
            :model-value="!allTabHidden"
            inline-prompt
            :active-icon="View"
            :inactive-icon="Hide"
            @update:model-value="allTabHidden = !allTabHidden"
          />
        </el-tooltip>
      </div>
    </div>

    <!-- 「顶栏分组页签」同样是内置项（issue #130）：来自任务 labels 里的 `分组:` 标签（App 里建的分组），
         库里没有对应的视图行，顺序固定按名称、也不能单独编辑删除，所以只给一个整体显示开关。
         同样放在 listContainer 之外，理由同上。
         #157 起改名：原来也叫「分组标签」，与工具栏「显示设置」里管任务名旁分组小标签的那一项同名、管的却是两样东西，
         用户关错了开关会以为「关了刷新又出现」。显示设置那一项保持原名不动。 -->
    <div
      class="view-manager-row is-builtin"
      :class="{ 'is-hidden': groupTabsHidden }"
    >
      <span class="view-drag-handle is-locked" title="内置项，不参与排序">
        <el-icon><Lock /></el-icon>
      </span>
      <span class="view-row-name">
        顶栏分组页签
        <span class="view-row-note">
          {{ groupCount > 0
            ? `当前 ${groupCount} 个分组，排在自定义视图之后，按名称排列`
            : '暂无分组；任务设置了分组（App 里建的分组也算）后会自动出现' }}
        </span>
      </span>
      <div class="view-row-actions">
        <el-tooltip :content="groupTabsHidden ? '在标签栏隐藏' : '在标签栏显示'" placement="top">
          <el-switch
            :model-value="!groupTabsHidden"
            inline-prompt
            :active-icon="View"
            :inactive-icon="Hide"
            @update:model-value="groupTabsHidden = !groupTabsHidden"
          />
        </el-tooltip>
      </div>
    </div>

    <div v-if="managed.length === 0" class="view-manager-empty">
      还没有任何自定义视图，先从标签栏右侧的「+」创建一个试试。
    </div>
    <div v-else ref="listContainer" class="view-manager-list">
      <div
        v-for="view in managed"
        :key="view.id"
        class="view-manager-row"
        :class="{ 'is-hidden': view._hidden }"
        :data-id="view.id"
      >
        <span class="view-drag-handle" :title="'拖动排序'">
          <el-icon><Rank /></el-icon>
        </span>
        <span class="view-row-name">{{ view.name }}</span>
        <div class="view-row-actions">
          <el-tooltip content="编辑" placement="top">
            <el-button size="small" type="primary" plain @click="handleEdit(view)">
              <el-icon><Edit /></el-icon>
            </el-button>
          </el-tooltip>
          <el-tooltip content="删除" placement="top">
            <el-button size="small" type="danger" plain @click="handleDelete(view)">
              <el-icon><Delete /></el-icon>
            </el-button>
          </el-tooltip>
          <el-tooltip :content="view._hidden ? '在标签栏隐藏' : '在标签栏显示'" placement="top">
            <el-switch
              :model-value="!view._hidden"
              inline-prompt
              :active-icon="View"
              :inactive-icon="Hide"
              @update:model-value="toggleHidden(view)"
            />
          </el-tooltip>
        </div>
      </div>
    </div>

    <template #footer>
      <el-button @click="handleCancel">取消</el-button>
      <!-- 不再按 managed.length 禁用：一个自定义视图都没有时，「全部」与「顶栏分组页签」的显隐开关仍然要能保存 -->
      <el-button
        type="primary"
        :loading="saving"
        @click="handleSave"
      >
        保存
      </el-button>
    </template>
  </el-dialog>
</template>

<style scoped lang="scss">
.view-manager-empty {
  // 上方现在恒定有一行「全部」固定项，留出间距免得两块贴在一起
  padding: 20px 0 4px;
  text-align: center;
  color: var(--dd-text-2);
}

.view-manager-hint {
  font-size: 13px;
  color: var(--dd-text-2);
  margin-bottom: 12px;
  display: flex;
  justify-content: space-between;
  gap: 12px;
  flex-wrap: wrap;
}

.view-manager-counts {
  color: var(--dd-text-3);
}

.view-manager-list {
  display: flex;
  flex-direction: column;
  gap: 6px;
  // 「全部」固定项排在列表之外、不跟着一起滚，这里补一段间距接上
  margin-top: 6px;
  max-height: 420px;
  overflow-y: auto;
  padding-right: 4px;
}

.view-manager-row {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 8px 10px;
  background: var(--el-fill-color-lighter);
  border: 1px solid transparent;
  // 视图列表里的一行卡片（行间有 6px 间隔，不是贴边内嵌）→ surface 档
  border-radius: var(--dd-radius-surface);
  transition: border-color 0.15s, background 0.15s, opacity 0.15s;

  &:hover {
    border-color: var(--dd-border-strong);
  }

  &.is-hidden {
    opacity: 0.55;
  }

  // 「全部」固定项：换成卡片底 + 常亮 1px 描边与下方可拖拽的视图行区分开（不靠阴影/圆角）。
  // hover 也不点亮描边 —— 它没有拖拽/编辑/删除，点亮反而会误导成「这行能拖」。
  &.is-builtin,
  &.is-builtin:hover {
    background: var(--dd-surface-solid);
    border-color: var(--dd-border);
  }

  // 两个内置项（「全部」「顶栏分组页签」）上下相邻，间距与下方视图列表的行间距同为 6px
  &.is-builtin + &.is-builtin {
    margin-top: 6px;
  }
}

.view-drag-handle {
  cursor: grab;
  color: var(--dd-text-3);
  display: inline-flex;
  align-items: center;
  padding: 2px;

  &:active {
    cursor: grabbing;
  }

  // 内置项的把手位只用来占位对齐，光标保持默认，免得用户以为它也能拖
  &.is-locked,
  &.is-locked:active {
    cursor: default;
  }
}

.view-row-name {
  flex: 1;
  font-size: 14px;
  font-weight: 500;
  color: var(--dd-text);
  word-break: break-word;
}

// 内置项的次级说明：字号与字重都降一档与名称拉开层级。
// 这里靠 display:block 自己换行，而不是把 .view-row-name 改成 column 弹性容器 ——
// 那会改掉所有自定义视图行的布局模型，为一行说明文字不值当。
.view-row-note {
  display: block;
  margin-top: 2px;
  font-size: 12px;
  font-weight: 400;
  line-height: 1.4;
  color: var(--dd-text-2);

  &.is-warning {
    color: var(--dd-warn-ink);
  }
}

.view-row-actions {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-shrink: 0;
}

.view-row-ghost {
  opacity: 0.4;
  background: color-mix(in srgb, var(--dd-accent) 10%, var(--dd-surface-solid));
}

.view-row-chosen {
  background: var(--el-fill-color);
}

/* 拖拽中不再靠阴影浮起，改用描边标出当前拖动的行 */
.view-row-dragging {
  border-color: var(--dd-border-strong);
}
</style>
