<script setup lang="ts">
import { ref, onMounted, onBeforeUnmount, onDeactivated, nextTick, computed, watch, type CSSProperties } from 'vue'
import { envApi, type EnvNameOption, type EnvUpdatePayload } from '@/api/env'
import { ElMessage, ElMessageBox } from 'element-plus'
import { copyText } from '@/utils/clipboard'
import EnvBatchGroupDialog from './components/EnvBatchGroupDialog.vue'
import EnvBatchRenameDialog from './components/EnvBatchRenameDialog.vue'
import EnvEditDialog from './components/EnvEditDialog.vue'
import EnvImportDialog from './components/EnvImportDialog.vue'
import { useResponsive } from '@/composables/useResponsive'
import DdSplitButton from '@/components/ui/DdSplitButton.vue'
import DdMoreMenu from '@/components/ui/DdMoreMenu.vue'
import type { SplitButtonItem } from '@/components/ui/DdSplitButton.vue'
// ArrowDown 不在 main.ts 的全局图标表里，要单独引；模板里以 <Lock /> 这类标签写的图标（Lock / Unlock / View / Hide / Check …）走全局注册。
// 其余几个是给 el-button 的 :icon 传组件对象用的（移动端纯图标按钮与批量栏），显式局部引入，不依赖 main.ts 的全局表。
import { ArrowDown, CircleCheck, CircleClose, Close, Delete, Download, Edit, Folder, Plus, Refresh } from '@element-plus/icons-vue'
import { maskEnvValue, shouldMaskEnvValue, type EnvValueMaskMode } from '@/utils/envSecret'
import { scrollListToTop } from '@/utils/scrollToTop'
import {
  ENVS_PAGE_SIZE_OPTIONS,
  ensureListPreferencesLoaded,
  readListPreference,
  setListPreference,
  type ListPreferences
} from '@/utils/listPreferences'

const envTableDensityStorageKey = 'daidai-env-table-density'
// 值的遮蔽模式（#127），和上面的表格密度是同一类只存本机的偏好，键名沿用本页 daidai-env-* 前缀。
// （每页条数原来也在这一类，v3.3.1 起改为跟随账户，读写统一走 utils/listPreferences.ts。）
const envMaskModeStorageKey = 'daidai-env-mask-mode'
const envAllFetchBatchSize = 100
const { isMobile } = useResponsive()

type EnvPageSizeSelection = ListPreferences['envs_page_size']

type EnvFormModel = {
  id: number
  name: string
  value: string
  remarks: string
  group?: string
  groups: string[]
  // 排序值（#131，即接口里的 position）。编辑时带进弹窗；弹窗只在用户真改过时才把它放进 save 的数据里
  position?: number | null
  // 「重要」标记（APP #16）。弹窗规则：新建开着才带 true，编辑拨过开关才带
  important?: boolean
}

const envList = ref<any[]>([])
const loading = ref(true)
const total = ref(0)
const page = ref(1)
// setup 时先按本机缓存（同步）给出条数；服务端的值由 onMounted 里 ensureListPreferencesLoaded() 补上，再发首个列表请求
const initialPageSizeSelection = readListPreference('envs_page_size')
const pageSizeSelection = ref<EnvPageSizeSelection>(initialPageSizeSelection)
const pageSize = ref(initialPageSizeSelection === 'all' ? envAllFetchBatchSize : Number(initialPageSizeSelection))
const keyword = ref('')
const groupFilters = ref<string[]>([])
const groups = ref<string[]>([])
// 变量名筛选：与分组筛选并列的一等筛选维度（issue #118 建议 2）。
// 同名多条是刻意功能（多账号场景，见 server/model/env_var.go），所以「按变量名收敛」是高频诉求，
// 而搜索框那套四字段模糊 OR 根本限定不了字段。
const nameFilters = ref<string[]>([])
// 选项里的 count 是【全库同名条数】（服务端 /envs/names 的口径），不跟随当前筛选：
// 它与后端 PUT /envs/by-name 冲突时那句「存在 N 条名为 X 的环境变量」是同一个 N。
const nameOptions = ref<EnvNameOption[]>([])
const selectedIds = ref<number[]>([])
const selectedIdSet = computed(() => new Set(selectedIds.value))
const selectedCountInCurrentPage = computed(() =>
  envList.value.filter((item) => selectedIdSet.value.has(item.id)).length
)
const showAllEnvs = computed(() => pageSizeSelection.value === 'all')
const pinnedCountInCurrentPage = computed(() =>
  envList.value.filter((item) => isTopPinned(item)).length
)
const currentPageOffset = computed(() => (showAllEnvs.value ? 0 : (page.value - 1) * pageSize.value))
const statusFilter = ref<'' | 'enabled' | 'disabled'>('')
const showFooterBar = computed(() => total.value > 0 || selectedCountInCurrentPage.value > 0)
const showPager = computed(() => !showAllEnvs.value && total.value > pageSize.value)
const sortableEnabled = computed(() => envList.value.length >= 2)
// 选项由 listPreferences 的白名单生成（与服务端 list.envs_page_size 白名单逐项对应），不在页面里再抄一份：
// 多出一个服务端不认的档位，选了只在本机生效，换个浏览器就回到默认值
const pageSizeOptions: Array<{ label: string; value: EnvPageSizeSelection }> = ENVS_PAGE_SIZE_OPTIONS.map((value) => ({
  label: value === 'all' ? '全部' : `${value} / 页`,
  value
}))
const selectionScopeText = computed(() =>
  showAllEnvs.value ? '批量操作作用于当前已勾选的数据。' : '批量操作仅作用于当前页勾选的数据。'
)
const filteredEnvList = computed(() => {
  if (statusFilter.value === 'enabled') return envList.value.filter(item => item.enabled)
  if (statusFilter.value === 'disabled') return envList.value.filter(item => !item.enabled)
  return envList.value
})
// 移动端批量栏的「全选 / 取消全选」：只看当前渲染出来的卡片（= 当前页；「全部」档下就是已加载的全部行）。
// 手机上状态筛选不存在（进入移动端时已清空），filteredEnvList 就等于 envList，不会选中看不见的行。
const allSelectedInPage = computed(() =>
  filteredEnvList.value.length > 0 && filteredEnvList.value.every((item) => selectedIdSet.value.has(item.id))
)
// 手机上唯一还在的筛选是搜索框（状态 / 分组 / 变量名三个筛选在移动端不渲染，进入移动端时会被清空），
// 所以「筛出来是空的」只可能来自关键词
const mobileEmptyDescription = computed(() =>
  keyword.value.trim() ? '当前筛选条件下暂无环境变量' : '暂无环境变量'
)
const envTableClass = computed(() => ['env-table', 'env-table--' + tableDensity.value])
const envTableHeaderStyle = { background: 'var(--el-fill-color-light)', color: 'var(--el-text-color-regular)', fontWeight: 600, fontSize: '13px' }
const tableDensity = ref<'comfortable' | 'compact'>(
  typeof window !== 'undefined' && window.localStorage.getItem(envTableDensityStorageKey) === 'compact'
    ? 'compact'
    : 'comfortable'
)

// ===== 敏感值遮蔽（issue #127）=====
// 只管显示：接口、复制按钮、编辑弹窗、导出一律仍是明文，理由见 utils/envSecret.ts 文件头。
// 三态是页面级本地偏好，默认「自动」（只遮名字像凭据的变量），存取方式与表格密度相同（只存本机）。
// 切换入口只在桌面「值」列表头；手机上沿用本机存的那一档（与同一浏览器的桌面共用），见模板里移动端工具栏的注释。
const envMaskMode = ref<EnvValueMaskMode>(readEnvMaskMode())
// 用小眼睛临时揭示过的行 id。刻意不持久化：loadData（刷新 / 翻页 / 改筛选 / 保存后重拉）和切换模式时都会清空，
// 看完一眼就收回，不会因为「上次点开忘了关」一直明文挂在屏幕上。
// Set 放进 ref 后是响应式集合：模板里的 has() 会被追踪，add / delete / clear 都会触发重渲染，不用每次换新 Set。
const revealedEnvIds = ref(new Set<number>())
const envMaskModeOrder: EnvValueMaskMode[] = ['auto', 'all', 'none']
const envMaskModeMeta: Record<EnvValueMaskMode, { label: string; short: string; hint: string }> = {
  auto: { label: '自动', short: '自动遮蔽', hint: '按变量名识别凭据类变量' },
  all: { label: '全部遮蔽', short: '全部遮蔽', hint: '所有变量的值都遮住' },
  none: { label: '全部明文', short: '全部明文', hint: '不遮蔽，直接显示原值' }
}
// 模式切换按钮的 title 和 aria-label：说清「自动」认哪些名字，以及遮蔽管不到哪里
const envMaskModeTitle = computed(() => {
  const rule = envMaskMode.value === 'auto' ? '（变量名含 TOKEN、SECRET、PASSWORD、COOKIE、KEY 等时遮住）' : ''
  return `值的显示方式：${envMaskModeMeta[envMaskMode.value].short}${rule}。只影响本页显示，复制、编辑、导出仍是明文`
})

const showEditDialog = ref(false)
const editDialogMode = ref<'create' | 'edit'>('create')
const currentEditEnv = ref<EnvFormModel | null>(null)
// 提交在途锁：请求返回前弹窗一直开着，不锁按钮连点就会连发 POST 建出多条重复变量
const editSubmitting = ref(false)

const showImportDialog = ref(false)
const importSubmitting = ref(false)

const showExportDialog = ref(false)
const exportFormat = ref('shell')
const exportContent = ref('')
const exportScopeText = computed(() =>
  selectedIds.value.length > 0 ? `已选中的 ${selectedIds.value.length} 项环境变量` : '当前列表中的全部已启用环境变量'
)

const showBatchRenameDialog = ref(false)
const batchRenameSubmitting = ref(false)
const showBatchGroupDialog = ref(false)
const batchGroupSubmitting = ref(false)

const tableRef = ref()
// 页面根：翻页后回顶（scrollListToTop）的锚点
const pageRootRef = ref<HTMLElement | null>(null)
const desktopTableReady = ref(false)
const showDesktopLoadingPlaceholder = computed(
  () => !isMobile.value && (!desktopTableReady.value || (loading.value && envList.value.length === 0))
)
const showDesktopEmptyState = computed(
  () => !isMobile.value && desktopTableReady.value && !loading.value && envList.value.length === 0
)
let sortableInstance: any = null
let sortableLoader: Promise<any> | null = null
let dragPointerY = 0
let dragAutoScrollFrame = 0
let sortableInitFrame = 0
let desktopTableReadyFrame = 0
const groupBadgeStyleCache = new Map<string, CSSProperties>()

// 只改页面状态、不写偏好：挂载时按服务端值应用也走这里，那次若顺手写回，
// 会多发一次 PUT，还会把这一键记成「本机改过」、挡住后面的下行同步。写偏好只在 handlePageSizeChange 这个用户动作里做。
function applyEnvPageSizeSelection(value: EnvPageSizeSelection) {
  pageSizeSelection.value = value
  pageSize.value = value === 'all' ? envAllFetchBatchSize : Number(value)
}

function loadSortable() {
  if (!sortableLoader) {
    sortableLoader = import('sortablejs').then((mod) => mod.default)
  }
  return sortableLoader
}

function clearTableSelection() {
  selectedIds.value = []
  tableRef.value?.clearSelection?.()
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

// 移动端批量栏的「全选 / 取消全选」。移动卡片的勾选不经过 el-table，直接改 selectedIds；
// 取消全选后勾选数归零，第一行会自动换回搜索栏。
function toggleSelectAllInPage() {
  if (allSelectedInPage.value) {
    clearTableSelection()
    return
  }
  selectedIds.value = filteredEnvList.value.map((item) => item.id)
}

function handleDensityChange(value: 'comfortable' | 'compact') {
  tableDensity.value = value
  if (typeof window !== 'undefined') {
    window.localStorage.setItem(envTableDensityStorageKey, value)
  }
}

function buildStringHue(value: string) {
  let hash = 0
  for (const char of value) {
    hash = ((hash << 5) - hash + char.charCodeAt(0)) | 0
  }
  return Math.abs(hash) % 360
}

function getGroupBadgeStyle(group: string): CSSProperties {
  const cacheKey = group || 'default'
  const cached = groupBadgeStyleCache.get(cacheKey)
  if (cached) {
    return cached
  }

  const style = {
    '--group-hue': String(buildStringHue(cacheKey))
  } as CSSProperties
  groupBadgeStyleCache.set(cacheKey, style)
  return style
}

function splitEnvGroups(value: string): string[] {
  return value
    .split(/[,，;；\n\r\t]/)
    .map(group => group.trim())
    .filter((group, index, list) => group !== '' && list.indexOf(group) === index)
}

function normalizeGroupList(value: unknown): string[] {
  if (Array.isArray(value)) {
    return splitEnvGroups(value.filter(item => typeof item === 'string').join(','))
  }
  if (typeof value === 'string') {
    return splitEnvGroups(value)
  }
  return []
}

function normalizeEnvRow(row: any) {
  const rowGroups = normalizeGroupList(row.groups?.length ? row.groups : row.group)
  return {
    ...row,
    group: rowGroups.join(','),
    groups: rowGroups
  }
}

function normalizeEnvRows(rows: any[]) {
  return rows.map(row => normalizeEnvRow(row))
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
    const bodyWrapper = (
      isMobile.value
        ? document.querySelector('.env-mobile-scroll')
        : document.querySelector('.env-table .el-table__body-wrapper')
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

function clearDesktopTableReadyQueue() {
  if (desktopTableReadyFrame) {
    window.cancelAnimationFrame(desktopTableReadyFrame)
    desktopTableReadyFrame = 0
  }
}

function queueDesktopTableReady() {
  if (typeof window === 'undefined' || isMobile.value || desktopTableReady.value) return
  clearDesktopTableReadyQueue()
  desktopTableReadyFrame = window.requestAnimationFrame(() => {
    desktopTableReadyFrame = 0
    desktopTableReady.value = true
  })
}

// 拖拽成功后本地行上的 position 已经过期（原因见 openEdit 的注释）；loadData 拿回新列表时复位
let envPositionsStale = false
let loadDataDepth = 0
// 列表请求序号闸（照任务页的 loadTasksSeq）。loadData 在本页十几处被调用（切状态 / 分组 / 变量名筛选、搜索、翻页、
// 增删改后重拉、拖拽失败回滚、首拉……），几次请求可能同时在飞，返回顺序没有保证；原来是「后到者赢」：
// 比如保存后的重拉还没回来就点了「已启用」，旧请求晚到会把另一种筛选的行和总数写回来，高亮却停在新标签上。
// 现在每次 loadData 自增序号，只有最新一次能写回 envList / total、报错、收起 loading、重挂拖拽。
let loadDataSeq = 0
async function loadData() {
  if (loadDataDepth >= 3) {
    // 防止空页重算递归堆叠，超过 3 层直接中止
    loadDataDepth = 0
    loading.value = false
    return
  }
  loadDataDepth += 1
  // 空页重算的递归调用也会在这里拿到自己的新序号，外层那次随之作废，收尾交给递归的这一次
  const seq = ++loadDataSeq
  loading.value = true
  selectedIds.value = []
  // 临时揭示只管当前这一屏：一刷新、翻页、改筛选、保存后重拉，就全部收回遮蔽（#127）
  revealedEnvIds.value.clear()
  try {
    const params = {
      keyword: keyword.value || undefined,
      // 变量名受 ^[A-Za-z_][A-Za-z0-9_]*$ 约束、不含逗号，所以这里可以放心用逗号拼多值。
      names: nameFilters.value.length > 0 ? nameFilters.value.join(',') : undefined,
      groups: groupFilters.value.length > 0 ? groupFilters.value.join(',') : undefined,
      enabled: statusFilter.value === 'enabled' ? true : statusFilter.value === 'disabled' ? false : undefined
    }

    if (showAllEnvs.value) {
      // 服务端通过 all=1 一次性返回（带 5000 条硬上限保护），避免循环分页造成的等待与滚动卡顿。
      const res = await envApi.list({ ...params, all: 1 })
      // 飞行期间又发出了新的一次：这份是按旧的筛选 / 条数拿的，整个丢弃（见 loadDataSeq）
      if (seq !== loadDataSeq) return
      envList.value = normalizeEnvRows(res.data || [])
      total.value = res.total || envList.value.length
    } else {
      const res = await envApi.list({
        ...params,
        page: page.value,
        page_size: pageSize.value
      })
      // 同上；要在空页重算之前判：过期的 total 不能拿来改 page
      if (seq !== loadDataSeq) return
      envList.value = normalizeEnvRows(res.data || [])
      total.value = res.total || 0

      if (envList.value.length === 0 && total.value > 0 && page.value > 1) {
        page.value = Math.max(1, Math.ceil(total.value / pageSize.value))
        await loadData()
        return
      }
    }
    // 列表刚从服务端整份拿回来，每行的 position 都是新的
    envPositionsStale = false
  } catch (err: any) {
    // 过期请求的失败同样丢弃：更新的那次还在飞，这时报「加载失败」只会误导
    if (seq !== loadDataSeq) return
    ElMessage.error(err?.response?.data?.error || '加载环境变量失败')
  } finally {
    // loading 改由最新一次收尾：旧请求先回来就置 false 的话，新请求还没回，骨架屏 / 加载态已经撤掉露出旧数据。
    // loadDataDepth 则无论新旧都要减回去，否则作废的请求会一直占着层数，攒够 3 层后正常的调用也会被当成递归拦掉
    if (seq === loadDataSeq) loading.value = false
    loadDataDepth = Math.max(0, loadDataDepth - 1)
  }

  await nextTick()
  // 等这一拍的工夫里可能又发出了新的一次：清勾选、重挂拖拽都交给它，这里再动只会拿旧的收尾去折腾新数据
  if (seq !== loadDataSeq) return
  queueDesktopTableReady()
  clearTableSelection()
  queueSortableInit()
}

async function loadGroups() {
  try {
    const res = await envApi.groups()
    groups.value = normalizeGroupList(res.data || [])
  } catch {
    // ignore
  }
}

// 变量名下拉的选项源。名字与条数只在「新增 / 改名 / 删除 / 导入」后才会变，
// 所以不跟着翻页、搜索、切筛选一起刷，只在这些真正改动数据的地方补一次。
async function loadNames() {
  try {
    const res = await envApi.names()
    nameOptions.value = Array.isArray(res.data) ? res.data : []
  } catch {
    // ignore
  }
}

// 「刷新」按钮（#174）：别处（APP、脚本、另一个标签页）改了变量后，不用整页刷新就能看到。
// 按当前筛选、当前页重拉列表，同时重拉分组与变量名选项；筛选条件和分页一概不动。
// 勾选与临时揭示由 loadData 自己清（与翻页、改筛选同一口径）。
// 请求中按钮转圈并拦住连点；loadData 自带序号闸，就算漏过一次也只会以最新那次为准。
const refreshing = ref(false)
async function handleRefresh() {
  if (refreshing.value) return
  refreshing.value = true
  try {
    // 三个函数各自吞掉了错误（列表失败会弹「加载环境变量失败」），这里不会被某一个卡住
    await Promise.all([loadData(), loadGroups(), loadNames()])
  } finally {
    refreshing.value = false
  }
}

onMounted(async () => {
  queueDesktopTableReady()
  void loadGroups()
  void loadNames()
  // 每页条数跟随账户（v3.3.1，issue #143 桌面端第 1 条）：首个列表请求等服务端的值回来再发。
  // loadData 有了序号闸（loadDataSeq），其实也能像任务页那样「先按本地值拉，不同再按服务端值重拉」，谁先回来都会停在最新一次上；
  // 仍选择先等，是因为两边不一致时（换浏览器 / 换域名后第一次打开）先拉的那次注定作废：白发一个列表请求
  // （本地若是「全部」档，这一趟就是整表），屏幕上还会先闪一版按本地条数排的行与翻页器，再整页换掉。
  // 等待期间状态标签、搜索框照样能点，由此发出的 loadData 不论回没回来，都会被下面这次更新的请求盖过
  // （还在飞的由序号闸丢弃），最终按服务端的条数 + 用户当时选的筛选落地，不会停在旧条数那一版上。
  // 代价只是首拉多等一个 GET 的往返：ensure 是记忆化的，同一会话里先去过任务页时这里零等待；
  // 拉失败（离线、老服务端）也会正常 resolve，照本机缓存加载。
  // loading 初始就是 true：只要用户不动，这段时间桌面一直是骨架屏，移动端的「暂无环境变量」空态也不会提前冒出来。
  await ensureListPreferencesLoaded()
  applyEnvPageSizeSelection(readListPreference('envs_page_size'))
  void loadData()
})

// 手机上不渲染状态分段、分组筛选、变量名筛选（见模板里移动端工具栏的注释）。桌面拖窄到 ≤768、平板旋转时，
// 已经生效的这几项会变成「看不见、也关不掉」却仍在过滤的状态，用户只会以为变量丢了 ⇒ 进入移动端时清空并重拉。
// 反方向（回到桌面）不用处理：三个控件又都看得见了。
function resetFiltersHiddenOnMobile() {
  if (!statusFilter.value && groupFilters.value.length === 0 && nameFilters.value.length === 0) return
  statusFilter.value = ''
  groupFilters.value = []
  nameFilters.value = []
  page.value = 1
  void loadData()
}

watch(isMobile, (mobile) => {
  // 两种布局切换时勾选一律作废：移动卡片的勾选不经过 el-table，切回桌面后表格里一格都没勾上，
  // 批量栏却还挂着「已选 N 项」，批量操作会落到看不出被选中的行上（反方向同理）。
  clearTableSelection()
  if (mobile) {
    resetFiltersHiddenOnMobile()
  }
  nextTick(() => {
    queueDesktopTableReady()
    queueSortableInit()
  })
})

onBeforeUnmount(() => {
  stopDragAutoScroll()
  clearQueuedSortableInit()
  clearDesktopTableReadyQueue()
  if (sortableInstance) {
    sortableInstance.destroy()
    sortableInstance = null
  }
})

// 本页在 MainLayout 的 keep-alive 里：切到别的菜单既不卸载也不重跑 loadData，revealedEnvIds 会原样留着。
// 临时揭示（#127）必须在离开时收回，否则点开的明文会一直挂着，隔几个小时切回来还是明文，和「只看一眼」对不上。
onDeactivated(() => {
  revealedEnvIds.value.clear()
})

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
  if (loading.value || !sortableEnabled.value) return
  const el = document.querySelector(
    isMobile.value
      ? '.env-mobile-list'
      : '.env-table .el-table__body-wrapper tbody'
  )
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
      ...(isMobile.value
        ? { handle: '.env-mobile-drag-handle', delay: 350, delayOnTouchOnly: true, touchStartThreshold: 8 }
        : { handle: '.env-drag-handle', delay: 0, touchStartThreshold: 4 }),
      onChoose: (evt: any) => {
        if (!isMobile.value) lockRowCellWidths(evt.item)
      },
      onUnchoose: (evt: any) => {
        if (!isMobile.value) unlockRowCellWidths(evt.item)
      },
      onStart: (evt: any) => {
        if (!isMobile.value) lockRowCellWidths(evt.item)
        updateDragPointer(evt)
        startDragAutoScroll()
      },
      onMove: (evt: any) => {
        updateDragPointer(evt)
      },
      onEnd: async (evt: any) => {
        if (!isMobile.value) unlockRowCellWidths(evt.item)
        stopDragAutoScroll()
        updateDragPointer(evt)

        const { oldIndex, newIndex } = evt
        if (oldIndex === newIndex) return

        const sourceItem = envList.value[oldIndex]
        if (!sourceItem) return

        const movedItem = envList.value.splice(oldIndex, 1)[0]
        envList.value.splice(newIndex, 0, movedItem)

        // 桶判定（#131）：后端只允许同一个置顶桶（sort_order 相同）里互拖，跨桶回 400。
        // 以前只看落点的后一条，有两种落点会出错：
        //   ① 置顶项拖到置顶区最后一格时，后一条必然是第一条普通项 ⇒ 永远被拦，置顶区里排不到末尾；
        //   ② 落点是本页最后一行时后一条不存在，发出去的 target 为空，服务端把它当成「移到整桶末尾」⇒
        //      分页或筛选时拖到可见列表底部，实际会越过所有没显示的项，被甩到最后一页之后。
        // 现在照任务页（tasks/index.vue 的 onEnd）同时看前后两条：
        //   后一条同桶 ⇒ 插到它前面；否则前一条同桶 ⇒ 插到它后面（position:'after'，契约 C4）；
        //   两边都不同桶，才是真的拖进了另一个区，拦下来并弹回原位。
        // 两种插法都以「看得见的邻居」为锚，分页、筛选时对整个桶也成立，所以不再发空 target。
        // 和任务页唯一的差别：任务页在「只有前一条同桶」时发空 target（本区末尾），这里改发 after，原因就是 ②。
        const nextItem = envList.value[newIndex + 1]
        const prevItem = envList.value[newIndex - 1]
        const nextSameBucket = !!nextItem && inSameEnvSortBucket(sourceItem, nextItem)
        const prevSameBucket = !!prevItem && inSameEnvSortBucket(sourceItem, prevItem)
        if (!nextSameBucket && !prevSameBucket) {
          ElMessage.warning('置顶区和普通区请分别排序，跨区移动请使用置顶按钮')
          void loadData()
          return
        }

        try {
          if (nextSameBucket) {
            await envApi.sort(sourceItem.id, nextItem.id)
          } else {
            await envApi.sort(sourceItem.id, prevItem.id, 'after')
          }
          // 服务端已把整个桶重编号，本地行上的 position 从这一刻起过期；打开编辑弹窗时会单独取新值
          envPositionsStale = true
        } catch (err: any) {
          ElMessage.error(err?.response?.data?.error || err?.message || '排序失败')
          void loadData()
        }
      }
    })
  } catch {
    ElMessage.error('拖拽排序组件加载失败')
  }
}

function handleSearch() {
  page.value = 1
  void loadData()
}

function handleGroupSelect() {
  page.value = 1
  void loadData()
}

function handleNameSelect() {
  page.value = 1
  void loadData()
}

// 名称单元格点一下 = 「只看这个变量名的全部条目」。
// 这是同名多条场景里最高频的诉求，做成【替换】而不是【追加】：用户点的是「只看这个」，
// 追加的话原来选中的其它名字还留在结果里，与点击时的心理预期相反。
// 已经就是这一个名字时直接返回，别白刷一次列表。
function handleFilterByName(name: string) {
  if (!name) return
  if (nameFilters.value.length === 1 && nameFilters.value[0] === name) return
  nameFilters.value = [name]
  page.value = 1
  void loadData()
}

// 翻页 / 改每页条数之后回到列表顶部（v3.3.1，issue #143 O3，桌面与移动端都做），等数据回来再滚。
// 只挂在分页器 current-change 与每页条数下拉的 change 上（= 用户翻页）；不放进 loadData：
// 保存、删除、启用禁用、拖拽失败回滚都会调它，用户正往下看着会被一把拽回顶部。
async function handlePageChange(newPage: number) {
  page.value = newPage
  await loadData()
  await nextTick()
  scrollListToTop(pageRootRef.value)
}

async function handlePageSizeChange(newSize: EnvPageSizeSelection) {
  applyEnvPageSizeSelection(newSize)
  // 每页条数只在这个用户动作里写偏好：先写本机缓存，再后台同步到账户（失败静默，本机照样生效）
  setListPreference('envs_page_size', newSize)
  page.value = 1
  await loadData()
  await nextTick()
  scrollListToTop(pageRootRef.value)
}

function handlePageSizeSelect(value: string) {
  void handlePageSizeChange(value as EnvPageSizeSelection)
}

function openCreate() {
  editDialogMode.value = 'create'
  currentEditEnv.value = { id: 0, name: '', value: '', remarks: '', group: '', groups: [], important: false }
  showEditDialog.value = true
}

function openDuplicate(row: any) {
  editDialogMode.value = 'create'
  currentEditEnv.value = {
    id: 0,
    name: row.name || '',
    value: '',
    remarks: row.remarks || '',
    group: row.group || '',
    groups: normalizeGroupList(row.groups?.length ? row.groups : row.group),
    // 复制同名变量多半是加一个账号，账号类变量不该跟着变成「重要」，所以固定关着
    important: false
  }
  showEditDialog.value = true
}

async function openEdit(row: any) {
  // 列表接口本来就下发 position（float）；老数据或演示站缺这个字段时传 null，弹窗里的排序值框就是空的
  let position: number | null = typeof row.position === 'number' ? row.position : null
  // 拖拽是乐观更新、成功后不重拉列表，可服务端已经把整个桶重编号成 1000、2000…，本地行上的 position 还是拖之前的旧数。
  // 弹窗「排序值」读的就是它：用户照着旧数去填，会排到意想不到的位置。所以拖过之后，单独取一次这一条的最新值。
  // 不在拖完后直接重拉整页或回填 position：el-table 对 data 是 deep watch，后台改行字段会触发整表 setData，
  // 碰上下一次拖拽进行中就会把正在拖的行重画掉（任务页为同一个原因在拖拽期间停轮询）。
  // 取不到就退回行上的值，不拦着打开弹窗（request 拦截器不弹全局错误提示，这里静默即可）。
  if (envPositionsStale) {
    try {
      const res = await envApi.get(row.id)
      if (typeof res?.data?.position === 'number') position = res.data.position
    } catch {
      // 退回行上的旧值
    }
  }
  editDialogMode.value = 'edit'
  currentEditEnv.value = {
    id: row.id,
    name: row.name || '',
    value: row.value || '',
    remarks: row.remarks || '',
    group: row.group || '',
    groups: normalizeGroupList(row.groups?.length ? row.groups : row.group),
    position,
    important: row.important === true
  }
  showEditDialog.value = true
}

async function handleSave(data: EnvFormModel | EnvFormModel[]) {
  // 校验都在子弹窗里做完了，走到这里必然要发请求，直接置位在途锁
  editSubmitting.value = true
  try {
    if (Array.isArray(data)) {
      await envApi.create(data as any)
      ElMessage.success(`批量创建 ${data.length} 个变量成功`)
    } else if (editDialogMode.value === 'create') {
      await envApi.create(data)
      ElMessage.success('创建成功')
    } else {
      const payload: EnvUpdatePayload = {
        name: data.name,
        value: data.value,
        remarks: data.remarks,
        group: data.group,
        groups: data.groups
      }
      // 排序值（契约 C5）：弹窗只在用户真改过时才把 position 放进 data，有就带、没有就不带
      if (typeof data.position === 'number') {
        payload.position = data.position
      }
      // 「重要」同理：弹窗只在用户拨过开关时才放进 data，没拨就不带，免得冲掉别处刚改的标记
      if (typeof data.important === 'boolean') {
        payload.important = data.important
      }
      await envApi.update(data.id, payload)
      ElMessage.success('更新成功')
    }
    showEditDialog.value = false
    void loadData()
    void loadGroups()
    // 新建会多出一条同名记录、编辑可能改掉变量名，两种都会让下拉里的名字或计数过期
    void loadNames()
  } catch {
    ElMessage.error(editDialogMode.value === 'create' ? '创建失败' : '更新失败')
  } finally {
    editSubmitting.value = false
  }
}

function isTopPinned(row: any) {
  return Number(row.sort_order || 0) > 0
}

// 拖拽排序的「桶」= sort_order 相同的一批（0 普通 / 1 置顶），和服务端 reorderEnvWithinSortBucket 的跨桶校验同一口径
function inSameEnvSortBucket(a: any, b: any) {
  return Number(a?.sort_order || 0) === Number(b?.sort_order || 0)
}

// 置顶与禁用是两个正交状态，返回空格分隔的多类名。
// Element Plus 把 row-class-name 的返回值整串作为**一项** push 进 Vue 的 class 数组，
// Vue 再把数组各项拼成最终的 class 字符串（只做拼接、不拆空格，也不需要拆：
// 空格分隔的多类名写进 class 属性本来就天然生效），
// 所以「又置顶又禁用」的行两个类都会挂上（谁的底色赢由样式表里的书写顺序决定，见 .env-row-disabled）。
function getRowClassName({ row }: { row: any }) {
  const classes: string[] = []
  if (isTopPinned(row)) classes.push('env-row-pinned')
  if (!row.enabled) classes.push('env-row-disabled')
  return classes.join(' ')
}

async function handleToggleTop(row: any) {
  try {
    if (isTopPinned(row)) {
      await envApi.cancelTop(row.id)
      ElMessage.success('已取消置顶')
    } else {
      await envApi.moveToTop(row.id)
      ElMessage.success('已置顶')
    }
    void loadData()
  } catch {
    ElMessage.error('操作失败')
  }
}

// 标为重要 / 取消重要（APP #16）：可撤销，不弹确认。写法照 handleToggleTop；只发 important 这一个键
async function handleToggleImportant(row: any) {
  try {
    await envApi.update(row.id, { important: !row.important })
    ElMessage.success(row.important ? '已取消重要' : '已标为重要')
    void loadData()
  } catch (err: any) {
    ElMessage.error(err?.response?.data?.error || '操作失败')
  }
}

async function handleDelete(row: any) {
  // 确认与请求分成两段 try：以前一个 catch 把「点了取消」和「删除失败」一起吞掉，删失败时界面上什么都不显示
  try {
    if (row.important) {
      // 重要变量换一个专门的确认框（APP #16），点名变量。服务端不拦删除，这一道确认就是全部的防线
      await ElMessageBox.confirm(`「${row.name}」已标记为重要变量，删除后无法恢复。确定要删除吗？`, '删除重要变量', {
        type: 'warning',
        confirmButtonText: '仍要删除'
      })
    } else {
      await ElMessageBox.confirm('确定要删除该环境变量吗？', '确认删除', { type: 'warning' })
    }
  } catch {
    // 点了取消
    return
  }
  try {
    await envApi.delete(row.id)
    ElMessage.success('删除成功')
    void loadData()
    void loadNames()
  } catch (err: any) {
    ElMessage.error(err?.response?.data?.error || '删除失败')
  }
}

async function handleToggle(row: any) {
  try {
    const enabling = !row.enabled
    await ElMessageBox.confirm(
      enabling
        ? `确认启用环境变量 ${row.name} 吗？`
        : `确认禁用环境变量 ${row.name} 吗？禁用后脚本将无法读取该变量。`,
      enabling ? '启用确认' : '禁用确认',
      { type: enabling ? 'info' : 'warning' }
    )
    if (row.enabled) {
      await envApi.disable(row.id)
    } else {
      await envApi.enable(row.id)
    }
    ElMessage.success(row.enabled ? '已禁用' : '已启用')
    void loadData()
  } catch (err: any) {
    if (err === 'cancel' || err?.toString?.() === 'cancel') return
    ElMessage.error('操作失败')
  }
}

/**
 * 行操作菜单项（按行生成）：桌面操作列 Split Button 的 ▾ 菜单与移动卡片右上角「···」（DdMoreMenu）共用这一份，两端同源。
 *
 * 桌面 Split Button 的主体是「编辑」——最常用，且点错了只是打开一个弹窗，代价最小；
 * 移动卡片上「编辑」是末行的一级按钮，同样不进菜单。
 * 「删除」不可撤销，必须留在菜单里并加 divided + danger，绝不能占主体位置。
 * 「置顶 / 取消置顶」是同一个 handleToggleTop 的两面，用 visible 按 row 状态互斥，
 * 避免出现「已置顶的行菜单里还挂着置顶」。「标为重要 / 取消重要」（handleToggleImportant）同理。
 *
 * 🔴 这里【不要】再补「启用 / 禁用」：它在两端都已经外置成一级按钮（谁上了一级谁就不在菜单里），
 * 补进来就会变成「点了外面的禁用，展开菜单里还挂着一个禁用」。
 */
function buildEnvActionItems(row: any): SplitButtonItem[] {
  const pinned = isTopPinned(row)
  const important = row.important === true
  return [
    { key: 'duplicate', label: '复制同名变量' },
    { key: 'top', label: '置顶', visible: !pinned },
    { key: 'cancel-top', label: '取消置顶', visible: pinned },
    { key: 'important', label: '标为重要', visible: !important },
    { key: 'cancel-important', label: '取消重要', visible: important },
    { key: 'delete', label: '删除', danger: true, divided: true }
  ]
}

function onEnvAction(key: string, row: any) {
  if (key === 'duplicate') openDuplicate(row)
  else if (key === 'top' || key === 'cancel-top') void handleToggleTop(row)
  else if (key === 'important' || key === 'cancel-important') void handleToggleImportant(row)
  else if (key === 'delete') void handleDelete(row)
}

async function handleBatchDelete() {
  // 只看当前列表里真实存在的勾选行（与「已选 N 项」同一口径），这样才知道哪些是重要变量
  const selectedRows = envList.value.filter((row) => selectedIdSet.value.has(row.id))
  if (selectedRows.length === 0) return
  // 批量删除默认跳过重要变量（APP #16）：服务端不拦，所以在这里剔除，只发普通变量的 id
  const skipped = selectedRows.filter((row) => row.important === true).length
  const deletable = selectedRows.filter((row) => row.important !== true)
  if (deletable.length === 0) {
    // 全是重要变量：不发请求、不清勾选。轻量档下行菜单的单删照样能删重要变量（多一道确认），文案照此写
    ElMessage.warning('选中的都是重要变量，没有可删除的。确需删除请在行菜单里逐个删除，或先取消「重要」标记')
    return
  }
  // 确认与请求分成两段 try（同 handleDelete）：删除失败不再被当成「点了取消」吞掉
  try {
    await ElMessageBox.confirm(
      skipped > 0
        ? `选中的 ${selectedRows.length} 个环境变量里有 ${skipped} 个是重要变量，将跳过它们，只删除其余 ${deletable.length} 个。`
        : `确定要删除选中的 ${selectedRows.length} 个环境变量吗？`,
      '批量删除',
      { type: 'warning', ...(skipped > 0 ? { confirmButtonText: `删除 ${deletable.length} 个` } : {}) }
    )
  } catch {
    // 点了取消
    return
  }
  try {
    const res = await envApi.batchDelete(deletable.map((row) => row.id))
    ElMessage.success(`${res.message || '批量删除成功'}${skipped > 0 ? `，已跳过 ${skipped} 个重要变量` : ''}`)
    clearTableSelection()
    void loadData()
    void loadNames()
  } catch (err: any) {
    ElMessage.error(err?.response?.data?.error || '批量删除失败')
  }
}

async function handleBatchGroup() {
  if (selectedIds.value.length === 0) return
  showBatchGroupDialog.value = true
}

function handleBatchRename() {
  if (selectedIds.value.length === 0) return
  showBatchRenameDialog.value = true
}

async function confirmBatchRename(payload: { name: string }) {
  // 在途锁：弹窗要等请求成功才关，期间连点会重复提交
  batchRenameSubmitting.value = true
  try {
    const res = await envApi.batchRename(selectedIds.value, payload.name)
    ElMessage.success(res.message || '批量改名成功')
    showBatchRenameDialog.value = false
    clearTableSelection()
    void loadData()
    void loadNames()
  } catch (err: any) {
    ElMessage.error(err?.response?.data?.error || err?.message || '批量改名失败')
  } finally {
    batchRenameSubmitting.value = false
  }
}

async function confirmBatchGroup(groups: string[]) {
  // 在途锁：同上，弹窗在请求期间仍可点
  batchGroupSubmitting.value = true
  try {
    await envApi.batchSetGroup(selectedIds.value, normalizeGroupList(groups))
    ElMessage.success('批量分组成功')
    showBatchGroupDialog.value = false
    clearTableSelection()
    void loadData()
    void loadGroups()
  } catch (err: any) {
    ElMessage.error(err?.response?.data?.error || err?.message || '批量分组失败')
  } finally {
    batchGroupSubmitting.value = false
  }
}

async function handleBatchEnable() {
  if (selectedIds.value.length === 0) return
  try {
    await ElMessageBox.confirm(`确认启用选中的 ${selectedIds.value.length} 个环境变量吗？`, '批量启用', { type: 'info' })
    await envApi.batchEnable(selectedIds.value)
    ElMessage.success('批量启用成功')
    void loadData()
  } catch (err: any) {
    if (err === 'cancel' || err?.toString?.() === 'cancel') return
    ElMessage.error('批量启用失败')
  }
}

async function handleBatchDisable() {
  if (selectedIds.value.length === 0) return
  try {
    await ElMessageBox.confirm(`确认禁用选中的 ${selectedIds.value.length} 个环境变量吗？`, '批量禁用', { type: 'warning' })
    await envApi.batchDisable(selectedIds.value)
    ElMessage.success('批量禁用成功')
    void loadData()
  } catch (err: any) {
    if (err === 'cancel' || err?.toString?.() === 'cancel') return
    ElMessage.error('批量禁用失败')
  }
}

function handleSelectionChange(rows: any[]) {
  selectedIds.value = rows.map(r => r.id)
}

async function handleImport(payload: { envs: any[]; mode: string }) {
  // 在途锁：JSON 体积大时导入耗时长，不锁按钮连点会把同一份数据导多遍；也挡住下面数数、确认期间的连点
  importSubmitting.value = true
  try {
    if (payload.mode === 'replace') {
      // 替换会先清空面板上的全部变量（含重要变量），以前点「导入」直接清空、没有二次确认（APP 早就有）。
      // 列表是分页的、还可能带着筛选，当前页数不全，所以现拉一次不带筛选的全量来数重要变量（all=1 上限 5000 条）；
      // 数不出来就不点名，确认照弹
      let importantCount = 0
      try {
        const res = await envApi.list({ all: 1 })
        importantCount = (res.data || []).filter((row: any) => row.important === true).length
      } catch {
        // 不点名
      }
      try {
        await ElMessageBox.confirm(
          `替换会先删除面板上的全部环境变量，再写入这 ${payload.envs.length} 条。` +
            (importantCount > 0 ? `面板上 ${importantCount} 个重要变量也会被清空，` : '') +
            '这份内容里没有的变量将无法找回。',
          '替换导入',
          { type: 'warning', confirmButtonText: '清空并导入' }
        )
      } catch {
        // 点了取消：什么都不发，导入弹窗保持打开（finally 会解开在途锁）
        return
      }
    }
    const res = await envApi.import(payload.envs, payload.mode)
    ElMessage.success(res.message)
    showImportDialog.value = false
    void loadData()
    void loadGroups()
    void loadNames()
  } catch (err: any) {
    // 弹服务端原话（如「请求体过大（最大 1MB）」），以前一律只说「导入失败」
    ElMessage.error(err?.response?.data?.error || '导入失败')
  } finally {
    importSubmitting.value = false
  }
}

async function handleExportAll() {
  try {
    const exportIds = selectedIds.value.length > 0 ? [...selectedIds.value] : undefined
    const res = await envApi.exportAll(exportIds)
    const json = JSON.stringify(res.data, null, 2)
    const blob = new Blob([json], { type: 'application/json' })
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = exportIds?.length ? `env_vars_selected_${exportIds.length}.json` : 'env_vars.json'
    a.click()
    URL.revokeObjectURL(url)
  } catch {
    ElMessage.error('导出失败')
  }
}

async function handleExportFiles() {
  showExportDialog.value = true
  try {
    const exportIds = selectedIds.value.length > 0 ? [...selectedIds.value] : undefined
    const enabledOnly = exportIds == null
    const res = await envApi.exportFiles(exportFormat.value, enabledOnly, exportIds)
    exportContent.value = res.data[exportFormat.value] || ''
  } catch {
    ElMessage.error('导出失败')
  }
}

/**
 * 工具条「导出」Split Button 的菜单项。
 *
 * 主体走 JSON（handleExportAll）：它是与「导入」配对的往返格式，原下拉里也排在第一位，
 * 且不依赖 exportFormat 这个 ref，点错的代价只是多下载一个文件。
 * Shell / JS / Python 三种只是打开预览弹窗，收进菜单；「导入」是反方向的操作，
 * 沿用原来的 divided 分隔线与其余项隔开。
 */
const exportActionItems: SplitButtonItem[] = [
  { key: 'shell', label: '导出 Shell' },
  { key: 'js', label: '导出 JS' },
  { key: 'python', label: '导出 Python' },
  { key: 'import', label: '导入', divided: true }
]

function onExportAction(key: string) {
  if (key === 'import') {
    showImportDialog.value = true
    return
  }
  exportFormat.value = key
  void handleExportFiles()
}

async function refreshExport() {
  try {
    const exportIds = selectedIds.value.length > 0 ? [...selectedIds.value] : undefined
    const enabledOnly = exportIds == null
    const res = await envApi.exportFiles(exportFormat.value, enabledOnly, exportIds)
    exportContent.value = res.data[exportFormat.value] || ''
  } catch {
    // ignore
  }
}

async function copyExport() {
  try {
    await copyText(exportContent.value)
    ElMessage.success('已复制到剪贴板')
  } catch {
    ElMessage.error('复制失败，请检查浏览器权限或站点访问方式')
  }
}

async function copyEnvValue(value: string) {
  try {
    await copyText(value)
    ElMessage.success('已复制到剪贴板')
  } catch {
    ElMessage.error('复制失败，请检查浏览器权限或站点访问方式')
  }
}

// 存储里读出来的值、下拉菜单的 command 都是外部输入，统一过这一道再用
function isEnvValueMaskMode(value: unknown): value is EnvValueMaskMode {
  return value === 'auto' || value === 'all' || value === 'none'
}

function readEnvMaskMode(): EnvValueMaskMode {
  if (typeof window === 'undefined') return 'auto'
  try {
    const raw = window.localStorage.getItem(envMaskModeStorageKey)
    if (isEnvValueMaskMode(raw)) return raw
  } catch {
    // 隐私模式或禁用站点数据时读存储会抛错：按默认「自动」走，宁可多遮
  }
  return 'auto'
}

function persistEnvMaskMode(mode: EnvValueMaskMode) {
  if (typeof window === 'undefined') return
  try {
    window.localStorage.setItem(envMaskModeStorageKey, mode)
  } catch {
    // 写不进存储只是下次打开回到默认，这次切换照样生效
  }
}

function handleEnvMaskModeCommand(command: string | number | object) {
  if (!isEnvValueMaskMode(command)) return
  if (envMaskMode.value === command) return
  envMaskMode.value = command
  persistEnvMaskMode(command)
  // 换模式时把临时揭示的行一起收回：不然在「全部遮蔽」下点开的几行，切走再切回来还开着，和刚选的模式对不上
  revealedEnvIds.value.clear()
}

// 按当前模式，这一行本来要不要遮（不看临时揭示）。空值没什么可遮的：显示「-」，也不给小眼睛
function isEnvValueMaskable(row: any) {
  return Boolean(row?.value) && shouldMaskEnvValue(row.name, envMaskMode.value)
}

function isEnvValueRevealed(row: any) {
  return revealedEnvIds.value.has(row.id)
}

function toggleEnvValueReveal(row: any) {
  if (revealedEnvIds.value.has(row.id)) {
    revealedEnvIds.value.delete(row.id)
  } else {
    revealedEnvIds.value.add(row.id)
  }
}

// 值单元格要显示的文本。遮着的时候 title 也必须用它：title 挂原值的话，鼠标一停上去就把遮住的内容露出来了
function envValueDisplay(row: any) {
  if (!row?.value) return '-'
  return isEnvValueMaskable(row) && !isEnvValueRevealed(row) ? maskEnvValue(row.value) : row.value
}

function formatDateTime(t: string | null) {
  if (!t) return '-'
  const d = new Date(t)
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}  ${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`
}

function handleStatusFilter(value: '' | 'enabled' | 'disabled') {
  if (statusFilter.value === value) {
    return
  }
  statusFilter.value = value
  page.value = 1
  void loadData()
}
</script>

<template>
  <div ref="pageRootRef" class="envs-page dd-fixed-page dd-page-hide-heading">
    <!-- 移动端工具栏（v3.3.1，issue #143）：与桌面工具栏分成 v-if / v-else 两支，桌面那支 DOM 原样不动，
         design-system §4.2 的左槽叠放不变式只在桌面那支里成立，这边不受它约束。
         第一行非批量态 = 搜索框 + 导出 + 新建变量（后两个是 32×32 纯图标按钮）；勾选后整行换成批量栏。
         手机上刻意不渲染（用 v-if 不用 CSS 藏，el-select 的下拉浮层就不会挂载）：
           - 状态分段、分组筛选、变量名筛选：进入移动端时由 watch(isMobile) 清空重拉，免得留下看不见也关不掉的筛选；
           - 导出的下拉菜单（Shell / JS / Python 预览与导入）：手机上「导出」直接下载 JSON；
           - 遮蔽模式切换：沿用本机存的那一档（与同一浏览器的桌面端共用，默认「自动」），每行的小眼睛照样能临时揭示。
         外面包一层 .env-mobile-toolbar 只为挂入场动画：两支来回切换时外层不重建，动画不会在每次勾选时重放。 -->
    <div v-if="isMobile" class="env-mobile-toolbar">
      <!-- 批量栏：全选 → 批量按钮（图标 + 短文字）→ 取消放最后；不显示「已选 N 项」，勾了几张卡一眼就能数。
           横向滑动、隐藏滚动条（dd-scroll-row），7 颗按钮在窄屏上放不下时左右划。
           按钮形态沿用桌面批量区：禁用 = danger plain、删除 = 实心 danger，两者之间隔着改名 / 分组两个中性按钮。 -->
      <div v-if="selectedCountInCurrentPage > 0" class="dd-scroll-row dd-mobile-batch-bar">
        <el-button @click="toggleSelectAllInPage">{{ allSelectedInPage ? '取消全选' : '全选' }}</el-button>
        <el-button :icon="CircleCheck" @click="handleBatchEnable">启用</el-button>
        <el-button type="danger" plain :icon="CircleClose" @click="handleBatchDisable">禁用</el-button>
        <el-button :icon="Edit" @click="handleBatchRename">改名</el-button>
        <el-button :icon="Folder" @click="handleBatchGroup">分组</el-button>
        <el-button type="danger" :icon="Delete" @click="handleBatchDelete">删除</el-button>
        <el-button :icon="Close" @click="clearTableSelection">取消</el-button>
      </div>
      <div v-else class="dd-mobile-toolbar">
        <el-input
          v-model="keyword"
          placeholder="搜索变量名、值、备注或分组"
          clearable
          @keyup.enter="handleSearch"
          @clear="handleSearch"
        >
          <template #prefix><el-icon><Search /></el-icon></template>
        </el-input>
        <!-- 刷新（#174）：与导出、新建同款 32×32 纯图标按钮，排在最前，按「查看 → 导出 → 新建」的轻重顺序 -->
        <el-button
          class="dd-icon-only-btn"
          :icon="Refresh"
          :loading="refreshing"
          aria-label="刷新"
          title="刷新"
          @click="handleRefresh"
        />
        <el-button
          class="dd-icon-only-btn"
          :icon="Download"
          aria-label="导出 JSON"
          title="导出 JSON"
          @click="handleExportAll"
        />
        <el-button
          type="primary"
          class="dd-icon-only-btn"
          :icon="Plus"
          aria-label="新建变量"
          title="新建变量"
          @click="openCreate"
        />
      </div>
    </div>
    <div v-else class="toolbar">
      <!-- 左槽是恒在的容器（flex:1 + 固定高度下限），内部的「筛选区」与「批量区」两支【都常驻 DOM】，
           叠放在同一个 1×1 网格格子里，只用 visibility 切换显示：勾一下就让批量按钮就地显形，
           筛选控件同一帧立即隐藏（transition 只列了 opacity，visibility 是即时生效的，
           所以只有【进场】那一支有淡入，退场是硬切）。这是刻意的：两支叠在同一个格子里，
           真做交叉淡出会有一段两排控件互相透视的重影，别为了「对称」把 visibility 加进 transition。
           左槽自身的 flex 尺寸全程不变，右区不会被 space-between 甩位；
           更关键的是左槽高度恒等于 max(筛选区高度, 批量区高度)，与当前显示哪一支无关 ⇒ 勾选/取消永不改变工具栏高度。
           本页两支的换行阈值不一样，所以窄窗口下【两个方向都可能出现】——
           筛选区原来是「3 个 tab + 260px 搜索框 + 220px 分组筛选」≈ 724px 定宽；
           v3.2.5 加入「变量名筛选」后按定宽算会涨到 ~966px，1280 宽 + 侧栏展开时必然换行，
           所以三个输入控件改成【可伸缩】：flex-basis 取收缩下限（160/150/150）、max-width 取理想宽（260/220/220），
           换行阈值 = 230(tabs) + 3×12(gap) + 160 + 150 + 150 ≈ 726px，与加控件之前【几乎不变】；
           宽屏有余量时再长回 260/220/220，看起来和以前一样。详见样式里 &__search / &__group-filter / &__name-filter 的注释。
           批量区则是 6 个按钮 + 计数且带 flex-wrap——
           以前只留一支在 DOM 里的话，勾选那一刻左槽忽高忽矮，dd-fixed-page 下 .table-card 是 flex:1 1 0，
           工具栏差多少表格就反向补多少 ⇒ 列表跳一下。换行点又由内容宽决定，而内容宽随侧栏展开/收起漂 156px
           （220px vs 64px），媒体查询锁不住，只能让两支同时参与撑高。
           visibility: hidden 自带「不可点、不进 Tab 序、不进无障碍树」，不需要再加 inert / aria-hidden。
           切到批量区的条件只按「当前列表里真实存在的行」算（selectedCountInCurrentPage 拿
           selectedIdSet 过 envList），不是 selectedIds.length 这个可能含已失效 id 的原始数组。 -->
      <div class="toolbar__left">
        <div class="toolbar__filters" :class="{ 'is-swapped-out': selectedCountInCurrentPage > 0 }">
          <div class="status-tabs">
            <button :class="['status-tab', { active: statusFilter === '' }]" @click="handleStatusFilter('')">全部变量</button>
            <button :class="['status-tab', { active: statusFilter === 'enabled' }]" @click="handleStatusFilter('enabled')">已启用</button>
            <button :class="['status-tab', { active: statusFilter === 'disabled' }]" @click="handleStatusFilter('disabled')">已禁用</button>
          </div>
          <el-input
            v-model="keyword"
            placeholder="搜索变量名、值、备注或分组"
            clearable
            class="toolbar__search"
            @keyup.enter="handleSearch"
            @clear="handleSearch"
          >
            <template #prefix><el-icon><Search /></el-icon></template>
          </el-input>
          <!-- 分组同样要能打字搜索（#167）：分组一多，只能一路滚着找。 -->
          <el-select
            v-model="groupFilters"
            placeholder="分组筛选"
            multiple
            filterable
            collapse-tags
            collapse-tags-tooltip
            clearable
            class="toolbar__group-filter"
            @change="handleGroupSelect"
          >
            <el-option v-for="g in groups" :key="g" :label="g" :value="g" />
          </el-select>
          <!-- 🔴 filterable 是必须的，与上面分组筛选一样：变量名可能上百个（同名多条是刻意功能，每个账号一条），
               分组多了也是同样的问题，不能打字过滤就只能一路滚。
               标签渲染成「JD_COOKIE (3)」，括号里是全库同名条数，与后端 by-name 冲突文案里的 N 同源。 -->
          <el-select
            v-model="nameFilters"
            placeholder="变量名筛选"
            multiple
            filterable
            collapse-tags
            collapse-tags-tooltip
            clearable
            class="toolbar__name-filter"
            @change="handleNameSelect"
          >
            <el-option
              v-for="option in nameOptions"
              :key="option.name"
              :label="`${option.name} (${option.count})`"
              :value="option.name"
            />
          </el-select>
        </div>
        <div class="batch-actions" :class="{ 'is-swapped-out': !(selectedCountInCurrentPage > 0) }">
          <!-- selectionScopeText 已经按分页模式区分过「全部 / 仅当前页」的措辞，挂成 title：
               动手前就该知道这几个按钮到底作用在哪些数据上。 -->
          <span class="batch-actions__count" :title="selectionScopeText">
            已选 {{ selectedCountInCurrentPage }} 项
          </span>
          <el-button @click="handleBatchEnable">批量启用</el-button>
          <!-- 禁用会让变量在脚本里直接取不到值，属于破坏性变更，用 danger plain 与中性按钮区分；
               又刻意排在「批量删除」前面隔着改名/分组两个中性按钮，避免两个红按钮挨在一起被误点。 -->
          <el-button type="danger" plain @click="handleBatchDisable">批量禁用</el-button>
          <el-button @click="handleBatchRename">批量改名</el-button>
          <el-button @click="handleBatchGroup">批量分组</el-button>
          <!-- 删除是不可逆的，用实心 danger 压过 plain 的「批量禁用」，也与 tasks / logs 两页的
               「批量删除」保持同一形态：三页里最醒目的永远是删除。 -->
          <el-button type="danger" @click="handleBatchDelete">批量删除</el-button>
          <!-- 批量区显形期间搜索框/分组筛选虽然还在 DOM 里，但已被 visibility 挡住不能点，
               这里是退出多选态的唯一出口，不能省 -->
          <el-button @click="clearTableSelection">取消选择</el-button>
        </div>
      </div>
      <div class="toolbar__right">
        <!-- 刷新（#174）：桌面也用 32×32 纯图标按钮（title / aria-label 写明「刷新」），不用带字的按钮。
             右区每宽一点左槽就窄一点：带字按钮约 +84px，1280 宽 + 展开侧栏时左槽会掉到换行阈值 ~726px 以下，
             筛选区被挤成两行（账见下方 .toolbar__search 的注释）；纯图标只占 32 + 10(gap) = 42px，左槽仍有约 763px。 -->
        <el-button
          class="dd-icon-only-btn"
          :icon="Refresh"
          :loading="refreshing"
          aria-label="刷新"
          title="刷新"
          @click="handleRefresh"
        />
        <!-- 原来是一个「…」更多下拉：主体只能展开菜单，最常用的导出永远要点两次，
             按钮上也看不出这里能干什么。改成真正的 Split Button：
             主体「导出」直接下载 JSON（默认/往返格式），其余格式与导入留在菜单里。 -->
        <DdSplitButton
          label="导出"
          type="default"
          size="default"
          :items="exportActionItems"
          @click="handleExportAll"
          @command="onExportAction"
        />
        <el-button type="primary" @click="openCreate">
          <el-icon><Plus /></el-icon> 新建变量
        </el-button>
      </div>
    </div>

    <div v-if="isMobile" class="env-mobile-scroll">
      <div class="dd-mobile-list env-mobile-list">
        <div
          v-for="row in filteredEnvList"
          :key="row.id"
          class="dd-mobile-card env-card"
          :class="{
            'env-card--pinned': isTopPinned(row),
            'env-card--disabled': !row.enabled
          }"
        >
          <!-- 首行：拖拽手柄 → 复选框 → 状态灯 → 变量名 →「重要」标签 → 分组 / 置顶小标签 → 右上角「···」。
               手机上不再有「只看这个变量名」的放大镜：变量名筛选在移动端整体不提供（见移动端工具栏的注释），
               留着它，点一下就会造出一个手机上看不见、也关不掉的变量名筛选。桌面表格那颗照旧。 -->
          <div class="dd-mobile-card__head">
            <button
              v-if="sortableEnabled"
              class="env-mobile-drag-handle"
              type="button"
              aria-label="长按拖动排序"
              @click.stop
            >
              <el-icon :size="16"><Rank /></el-icon>
            </button>
            <el-checkbox :model-value="isSelected(row.id)" @change="toggleSelected(row.id, $event)" />
            <!-- 状态圆点：卡片里原来的「状态」字段（switch + 文字）已删掉，
                 启用/禁用改由名称前这枚 8×8 圆点 + 末行的启用/禁用按钮承载，
                 与桌面表格完全同一套东西。title / aria-label 的必要性见下方 .env-status-dot 的注释。 -->
            <span
              class="env-status-dot"
              :class="{ 'is-enabled': row.enabled }"
              role="img"
              :title="row.enabled ? '已启用' : '已禁用'"
              :aria-label="row.enabled ? '已启用' : '已禁用'"
            />
            <!-- 名称是 <span> 而不是按钮：长按 <span> 才会弹出系统的文本选择/复制浮层
                 （卡片的长按拖拽只绑在 .env-mobile-drag-handle 上，不抢这个手势）。
                 同挂 .env-name 是为了沿用等宽字体、主题色与禁用卡的降档规则；字号由卡片作用域里的覆盖交还给 16px。
                 单行省略，:title 挂全名。 -->
            <span class="env-name dd-mobile-card__name" :title="row.name">{{ row.name }}</span>
            <!-- 「重要」标签（APP #16）刻意放在下面的标签组【外面】、紧跟变量名：标签组 flex-basis 是 0，
                 变量名一长它就被挤成 0 宽、整组裁掉；置顶还有左缘橙条兜底，「重要」没有别的视觉通道，
                 所以它自己 flex-shrink:0 常驻，宁可让变量名早一点出省略号。 -->
            <span v-if="row.important" class="important-chip" title="重要变量：删除时会再确认一次">
              <el-icon><Lock /></el-icon>
              重要
            </span>
            <!-- 没有分组时什么都不显示（不再写灰字「未分组」）；置顶除了这枚标签，卡片左缘还有橙色色条。 -->
            <div
              v-if="row.groups.length > 0 || isTopPinned(row)"
              class="dd-mobile-card__head-tags env-card__tags"
            >
              <span
                v-for="group in row.groups"
                :key="group"
                class="group-pill"
                :style="getGroupBadgeStyle(group)"
                :title="group"
              >
                <span class="group-dot" />
                <span class="group-pill__text">{{ group }}</span>
              </span>
              <span v-if="isTopPinned(row)" class="pinned-chip">
                <el-icon><Top /></el-icon>
                置顶
              </span>
            </div>
            <!-- 「···」菜单与桌面操作列的 ▾ 同一份 buildEnvActionItems / onEnvAction：复制同名变量、置顶 / 取消置顶、标为重要 / 取消重要、删除 -->
            <DdMoreMenu
              :items="buildEnvActionItems(row)"
              :aria-label="`${row.name} 的更多操作`"
              @command="(key: string) => onEnvAction(key, row)"
            />
          </div>

          <!-- 字段区：标签与值横排，先「备注」再「值」。「更新时间」在卡片上不再显示（桌面表格仍有这一列）。 -->
          <div class="dd-mobile-card__rows">
            <div class="dd-mobile-card__row">
              <span class="dd-mobile-card__row-label">备注</span>
              <span class="dd-mobile-card__row-value env-remarks-text" :title="row.remarks || ''">{{ row.remarks || '-' }}</span>
            </div>
            <div class="dd-mobile-card__row env-card__value-row">
              <span class="dd-mobile-card__row-label">值</span>
              <!-- 🔴 遮蔽（#127）：文本只能输出 envValueDisplay(row)；title / aria-label / data-* 一律不许出现 row.value，
                   否则遮着的明文会从属性里露出来（读屏、长按预览、DOM 检查都能看到）。这一格因此不挂 title。
                   最多显示两行、超出省略；被小眼睛临时揭示的明文也只露两行。 -->
              <span class="dd-mobile-card__row-value dd-mobile-card__row-value--clamp2 env-value-text">{{ envValueDisplay(row) }}</span>
              <span v-if="row.value" class="env-card__value-actions">
                <!-- 小眼睛：和桌面值列同一套判定，只有当前模式会遮这一行时才出现，是手机上唯一的临时揭示入口；
                     与旁边的复制按钮同样是裸 link 按钮、不套 tooltip（触屏没有 hover），所以另挂 aria-label -->
                <el-button
                  v-if="isEnvValueMaskable(row)"
                  class="env-reveal-btn"
                  size="small"
                  link
                  :aria-label="isEnvValueRevealed(row) ? `重新遮住 ${row.name} 的值` : `临时查看 ${row.name} 的明文`"
                  @click.stop="toggleEnvValueReveal(row)"
                >
                  <el-icon :size="14"><Hide v-if="isEnvValueRevealed(row)" /><View v-else /></el-icon>
                </el-button>
                <!-- 复制仍然复制明文（#127 的刻意设计：遮蔽防的是肩窥和截图，不是防自己用）；无障碍名称只写变量名 -->
                <el-button
                  class="env-copy-btn"
                  size="small"
                  link
                  :aria-label="`复制 ${row.name} 的值`"
                  @click.stop="copyEnvValue(row.value)"
                >
                  <el-icon :size="14"><CopyDocument /></el-icon>
                </el-button>
              </span>
            </div>
          </div>

          <!-- 末行：右下角只留「编辑」与「启用 / 禁用」两个一级按钮（default 尺寸），其余操作在右上角「···」里。
               「启用 / 禁用」与桌面操作列同一个按钮、同一套 type/plain 组合、同一个 handleToggle：
               启用态显示「禁用」= danger plain，禁用态显示「启用」= 不写 type（EP default 白底）。
               它落在最外侧是与桌面操作列相同的有意让步：handleToggle 带二次确认，真正不可逆的「删除」仍在菜单里。 -->
          <div class="dd-mobile-card__footer">
            <div class="dd-mobile-card__footer-actions">
              <el-button type="primary" @click="openEdit(row)">编辑</el-button>
              <el-button
                :type="row.enabled ? 'danger' : 'default'"
                :plain="row.enabled"
                @click="handleToggle(row)"
              >
                {{ row.enabled ? '禁用' : '启用' }}
              </el-button>
            </div>
          </div>
        </div>

        <el-empty v-if="!loading && filteredEnvList.length === 0" :description="mobileEmptyDescription" />
      </div>
    </div>

    <div v-else-if="showDesktopLoadingPlaceholder" class="env-desktop-state env-desktop-state--loading" aria-hidden="true">
      <div class="env-skeleton env-skeleton--title" />
      <div class="env-skeleton env-skeleton--toolbar" />
      <div v-for="n in 6" :key="`env-skeleton-${n}`" class="env-skeleton env-skeleton--row" />
    </div>

    <div v-else-if="showDesktopEmptyState" class="env-desktop-state">
      <el-empty description="暂无环境变量">
        <template #description>
          <div class="env-empty-copy">
            <strong>暂无环境变量</strong>
            <span>可以直接新建变量，或导入已有的 JSON 配置。</span>
          </div>
        </template>
        <div class="env-empty-actions">
          <el-button type="primary" @click="openCreate">新建环境变量</el-button>
          <el-button @click="showImportDialog = true">导入 JSON</el-button>
        </div>
      </el-empty>
    </div>

    <div v-else class="table-card">
      <el-table
        ref="tableRef"
        :data="filteredEnvList"
        v-loading="loading"
        @selection-change="handleSelectionChange"
        :row-class-name="getRowClassName"
        :class="envTableClass"
        :header-cell-style="envTableHeaderStyle"
        row-key="id"
        style="width: 100%"
      >
        <el-table-column type="selection" width="44" />
        <!-- min-width 188 → 204 → 230 → 296，三笔账都记在这里。
             .env-name-wrap 是 flex + gap:8px，多插一个子元素就多出一份 gap，
             所以每一笔净增都是「新元素自身宽度」+「新增的那一份 gap 8px」，不是只有元素自身：
               188 → 204：名称【前】多了一枚 8px 状态圆点 → 8 + 8 = 16px；
               204 → 230：名称【后】多了一颗「只看这个变量名」图标按钮 →
                          按钮自身 18px（图标 14 + .env-name-filter-btn 的 padding 2×2）+ 8 = 26px；
               230 → 296：v3.3.6 名称【后】多了「重要」标签（约 58px）→ 58 + 8 = 66px。
                          浏览器实测 1280×900、侧栏展开：230 时既置顶又重要的行变量名只剩约 29px、可见 2 个字符，
                          加宽 66px 后与只置顶的行看到的字符数相当（约 11 个）。
             不补回来的话名称的可见宽度会净减同样多，长变量名的省略号会提前出现。 -->
        <el-table-column prop="name" label="名称" min-width="296">
          <template #default="{ row }">
            <div class="env-name-wrap">
              <!-- 状态圆点：独立的「状态」列已删除，启用/禁用状态改由这枚圆点表达
                   （筛选仍由工具栏的 .status-tabs 承担，切换由操作列的按钮执行）。
                   title / aria-label 的必要性见下方 .env-status-dot 的注释。 -->
              <span
                class="env-status-dot"
                :class="{ 'is-enabled': row.enabled }"
                role="img"
                :title="row.enabled ? '已启用' : '已禁用'"
                :aria-label="row.enabled ? '已启用' : '已禁用'"
              />
              <!-- 🔴 名称文本必须是 <span>，别再改回 <button>：这一列没有常驻复制按钮，
                   鼠标划选就是复制变量名的唯一路径（想不划选就只剩「打开编辑弹窗从 input 里复制」），
                   而 <button> 这类表单控件浏览器不从它的 mousedown 起始文本选区，
                   双击（选词的自然手势）还会连触发两次点击 —— 第一次设筛选、第二次被
                   handleFilterByName 的等值 guard 挡掉，用户得到的是「列表被筛了」而不是「选中了一个词」。
                   :title 挂全名：名称超长时这一格只剩省略号，不挂就没有任何办法看到完整变量名
                   （「值」「备注」两列本来也是这么挂的）。 -->
              <span class="env-name" :title="row.name">{{ row.name }}</span>
              <!-- 「点一下 = 只看这个变量名的全部条目」这个入口没有取消，只是从名称文本上外移到这颗独立图标按钮：
                   入口还在、还是点一下，但它落在名称文本【之外】，划选名称不会再顺带把列表筛掉。
                   用 el-button（渲染出来的就是 <button>）而不是给 span 挂 @click —— 可点的东西必须能 Tab 聚焦、
                   能回车触发、能被读屏念成按钮；图标按钮没有可见文字，所以另挂 aria-label 作为无障碍名称
                   （el-tooltip 只负责鼠标悬停时的可见提示，构不成元素的无障碍名称）。
                   尺寸 / 配色 / 过渡与「值」列的 .env-copy-btn 逐条对齐，同一张表里的行内图标按钮保持一款。
                   @click.stop 是防御性的：本行没有行级点击，但拖拽克隆/未来的行点击都可能冒泡上来。 -->
              <el-tooltip content="只看这个变量名" placement="top">
                <el-button
                  class="env-name-filter-btn"
                  size="small"
                  link
                  :aria-label="`只看变量名 ${row.name} 的全部条目`"
                  @click.stop="handleFilterByName(row.name)"
                >
                  <el-icon :size="14"><Search /></el-icon>
                </el-button>
              </el-tooltip>
              <!-- 「重要」（APP #16）排在「置顶」前面，两枚一起靠在最右缘（靠的是上面按钮的 margin-right:auto）。
                   置顶标签不计入本列 min-width；「重要」从 v3.3.6 起计入（见上方列注释第三笔账），两枚都有的行照样可能收缩出省略号、title 挂全名。 -->
              <span v-if="row.important" class="important-chip" title="重要变量：删除时会再确认一次">
                <el-icon><Lock /></el-icon>
                重要
              </span>
              <span v-if="isTopPinned(row)" class="pinned-chip">
                <el-icon><Top /></el-icon>
                置顶
              </span>
            </div>
          </template>
        </el-table-column>
        <!-- 值列（#127 起可遮蔽）。加了小眼睛之后把像素账重算了一遍，结论是 min-width 280 不用动：
             可用内容宽 = 280 − 24（EP .cell 左右 padding 各 12）= 256px；.env-value-cell 是 flex + gap:4px，
             行内图标按钮一颗 18px（图标 14 + padding 2×2，与 .env-copy-btn / .env-name-filter-btn 同款），每多一颗再多一份 gap 4px。
               · 明文行（「全部明文」下的所有行、「自动」下名字不像凭据的行）：不渲染小眼睛，文本 + 复制，与改动前逐像素相同；
               · 遮着的行：遮罩定长，最长 12 个等宽字符（前 3 + 6 个 * + 后 3），13px 等宽字约 7.8px/字 ≈ 94px，
                 + 眼睛 18 + 复制 18 + 2 × 4 = 138px，离 256 还空着 118px；
               · 用小眼睛临时揭示的那一行：明文 + 眼睛 + 复制，文本可见宽比改动前少 18 + 4 = 22px。
             只有第三种会变窄，而它是用户点出来的临时态（刷新 / 翻页 / 切模式就收回），title 也挂着全文。
             为它把列加宽 22px 的话，宽屏下 EP 会按四个弹性列的 min-width 比例重新分剩余宽度，名称 / 备注 / 分组三列都跟着变，不值得。
             表头多了模式切换：cell padding 24 +「值」13 + gap 8 + 触发按钮
             （锁 14 + EP 给图标后文字的 6 + 4 字 × 12 + 箭头前 2 + 箭头 12 + padding 2×2 = 86）≈ 131px，也装得下。 -->
        <el-table-column prop="value" label="值" min-width="280">
          <template #header>
            <div class="env-value-header">
              <span>值</span>
              <!-- 桌面端的三态切换就放在它管的这一列表头上：选的是「这一列怎么显示」，放这里一眼就对得上。
                   也不去挤工具栏右区：右区再宽一点，1280 宽 + 展开侧栏时左边的筛选区就要换成两行（账见 .toolbar__search 的注释）。
                   这是全站唯一的切换入口：手机端（v3.3.1 起）不提供，沿用本机存的那一档，理由见移动端工具栏的注释。 -->
              <el-dropdown trigger="click" placement="bottom-start" @command="handleEnvMaskModeCommand">
                <el-button
                  class="env-mask-mode-trigger"
                  :class="{ 'is-unmasked': envMaskMode === 'none' }"
                  size="small"
                  link
                  :title="envMaskModeTitle"
                  :aria-label="envMaskModeTitle"
                >
                  <el-icon :size="14"><Unlock v-if="envMaskMode === 'none'" /><Lock v-else /></el-icon>
                  <span>{{ envMaskModeMeta[envMaskMode].short }}</span>
                  <el-icon :size="12" class="env-mask-mode-trigger__caret"><ArrowDown /></el-icon>
                </el-button>
                <template #dropdown>
                  <el-dropdown-menu>
                    <el-dropdown-item v-for="mode in envMaskModeOrder" :key="mode" :command="mode">
                      <span class="env-mask-option" :class="{ 'is-active': envMaskMode === mode }">
                        <el-icon class="env-mask-option__check"><Check /></el-icon>
                        <span class="env-mask-option__text">
                          <span class="env-mask-option__label">{{ envMaskModeMeta[mode].label }}</span>
                          <span class="env-mask-option__hint">{{ envMaskModeMeta[mode].hint }}</span>
                        </span>
                      </span>
                    </el-dropdown-item>
                  </el-dropdown-menu>
                </template>
              </el-dropdown>
            </div>
          </template>
          <template #default="{ row }">
            <div class="env-value-cell">
              <!-- title 与正文同源：遮着时悬停也只看到遮罩，否则鼠标一停就露馅 -->
              <span class="env-value-text" :title="row.value ? envValueDisplay(row) : ''">{{ envValueDisplay(row) }}</span>
              <!-- 小眼睛只在「当前模式会遮这一行」时出现：明文行放一颗「查看明文」没有意义，还白占 22px。
                   图标表示点下去会做什么（与开放 API 页的密钥揭示同一套约定）：遮着显示 View，揭示后显示 Hide。 -->
              <el-tooltip v-if="isEnvValueMaskable(row)" :content="isEnvValueRevealed(row) ? '重新遮住' : '临时查看明文'" placement="top">
                <el-button
                  class="env-reveal-btn"
                  size="small"
                  link
                  :aria-label="isEnvValueRevealed(row) ? `重新遮住 ${row.name} 的值` : `临时查看 ${row.name} 的明文`"
                  @click.stop="toggleEnvValueReveal(row)"
                >
                  <el-icon :size="14"><Hide v-if="isEnvValueRevealed(row)" /><View v-else /></el-icon>
                </el-button>
              </el-tooltip>
              <!-- 复制仍然复制明文（与开放 API 页一致）：遮蔽防的是肩窥和截图，不是防自己用 -->
              <el-tooltip v-if="row.value" content="复制" placement="top">
                <el-button class="env-copy-btn" size="small" link @click.stop="copyEnvValue(row.value)">
                  <el-icon :size="14"><CopyDocument /></el-icon>
                </el-button>
              </el-tooltip>
            </div>
          </template>
        </el-table-column>
        <el-table-column prop="remarks" label="备注" min-width="180">
          <template #default="{ row }">
            <span class="env-remarks-text" :title="row.remarks || ''">{{ row.remarks || '-' }}</span>
          </template>
        </el-table-column>
        <el-table-column prop="group" label="分组" min-width="180" align="center">
          <template #default="{ row }">
            <div v-if="row.groups.length > 0" class="group-pill-list group-pill-list--table">
              <span
                v-for="group in row.groups"
                :key="group"
                class="group-pill"
                :style="getGroupBadgeStyle(group)"
              >
                <span class="group-dot" />
                <span class="group-pill__text">{{ group }}</span>
              </span>
            </div>
            <span v-else class="env-empty-text">未分组</span>
          </template>
        </el-table-column>
        <el-table-column width="40" align="center" class-name="env-drag-col">
          <template #default>
            <el-icon class="env-drag-handle"><Rank /></el-icon>
          </template>
        </el-table-column>
        <!-- 原来这里是一整列「状态」（92px：switch + 启用/禁用文字），已删除：
             状态由名称列前的圆点表达、切换由操作列的「启用 / 禁用」按钮执行，一处状态一处开关。
             筛选口径不变，仍由工具栏的 .status-tabs（全部 / 已启用 / 已禁用）承担。 -->
        <el-table-column label="更新时间" width="168" align="center">
          <template #default="{ row }">
            <span class="time-text">{{ formatDateTime(row.updated_at) }}</span>
          </template>
        </el-table-column>
        <!-- 原来是 4 个纯图标按钮，每个各套一层 el-tooltip：图标本身不自解释，
             而且「删除」和「编辑」同尺寸并排、误点代价完全不同。
             改成 Split Button：主体是最常用且可逆的「编辑」，其余收进菜单，
             删除标红并用分隔线隔开，置顶/取消置顶靠 visible 互斥。

             列宽 172（v3.2.0 从 110 重算，与定时任务页取同一个值）：
             EP 的 .el-table .cell 是 padding:0 12px + overflow:hidden，可用内容宽 = 172 - 24 = 148px。
             这一格现在是两个元素：左边 Split Button（编辑 ▾），右边外置的「禁用 / 启用」普通按钮。
             实测口径（size="small"，浏览器量出来的，不是估的）：
               主体 = 中文字数 × 12（字宽）+ 22（EP small 的 padding 5px 11px）+ 2（边框）
               caret = 32，且【不受 size 影响】：EP 的 el-dropdown 根节点只挂 `el-dropdown` + `is-disabled`，
                 不带 size 修饰类，`.el-dropdown--small .el-dropdown__caret-button{width:24px}` 根本匹配不上。
                 ⚠️ 本文件旧注释按那条规则算成 24px，是错的（会系统性偏小约 14px），此处一并订正 ——
                 别再照 24px 把列宽收窄回去。
               el-button-group 组内相邻按钮还有 -1px 的负边距
             「编辑」= 2×12+22+2 = 48 → 48+32-1 = 79px；「禁用」/「启用」同为 2 字 = 48px；
             中间 gap 4px（.action-btns 已把 EP 的 `.el-button + .el-button` 外边距清零，间距只由 gap 决定，
             不会两份叠加）。合计 79 + 4 + 48 = 131px，148 - 131 = 17px 余量。
             按钮组一旦超出可用宽，.cell 就会变成可滚动容器，点右边的按钮时整行会被滚偏且不复位。

             fixed="right"：与定时任务页一致，窄窗口横向滚动时操作列常驻可见。 -->
        <el-table-column label="操作" width="172" fixed="right" align="center">
          <template #default="{ row }">
            <div class="action-btns">
              <DdSplitButton
                label="编辑"
                type="primary"
                size="small"
                :items="buildEnvActionItems(row)"
                @click="openEdit(row)"
                @command="(key: string) => onEnvAction(key, row)"
              />
              <!-- 「启用 / 禁用」外置成一级按钮：状态列删掉后这是唯一的切换入口，埋进 ▾ 菜单要点两下。
                   两个状态互斥（谁上了一级谁就不在菜单里），buildEnvActionItems 里因此没有同名项。
                   type/plain 组合与工具栏的「批量启用 / 批量禁用」逐字一致：
                   禁用 = danger + plain（白底红字红描边），启用 = default（EP 白底）。
                   直接复用 handleToggle —— 二次确认、错误提示、loadData 全在里面，不要再写一份。

                   ⚠️ design-system §4.2 的内容约定是「危险按钮不放最外侧」（甩到边缘的光标最容易落在最外那个）。
                   这里是用户在 #109-4 里明确要求的位置，属于一次有意识的让步，理由：
                   handleToggle 带 ElMessageBox 二次确认，点错的代价是按一下 Esc；
                   真正不可逆的「删除」仍然待在 Split 菜单里（danger + divided），没有被提到外侧。 -->
              <el-button
                size="small"
                :type="row.enabled ? 'danger' : 'default'"
                :plain="row.enabled"
                @click="handleToggle(row)"
              >
                {{ row.enabled ? '禁用' : '启用' }}
              </el-button>
            </div>
          </template>
        </el-table-column>
      </el-table>
    </div>

    <!-- showFooterBar = 有数据，或者虽然列表空了但还留着勾选。
         一条数据都没有（且没勾选）时这条底栏只剩「共 0 条数据」和一个作用不到任何数据的
         每页条数选择器，正下方还压着空态自己的指路文案，纯属噪声——直接不渲染。
         .dd-fixed-page 下 .table-card 是 flex:1 1 0，少一条底栏只是把高度让给表格，不会塌。 -->
    <div v-if="showFooterBar" class="pagination-bar">
      <span class="pagination-total">共 {{ total }} 条数据</span>
      <div class="pagination-actions">
        <div class="page-size-picker">
          <span class="page-size-picker__label">每页</span>
          <el-select
            :model-value="pageSizeSelection"
            size="small"
            style="width: 112px"
            @change="handlePageSizeSelect"
          >
            <el-option
              v-for="option in pageSizeOptions"
              :key="option.value"
              :label="option.label"
              :value="option.value"
            />
          </el-select>
        </div>
        <el-pagination
          v-if="showPager"
          v-model:current-page="page"
          :page-size="pageSize"
          :total="total"
          layout="prev, pager, next"
          @current-change="handlePageChange"
        />
      </div>
    </div>

    <el-dialog v-model="showExportDialog" title="导出环境变量" width="600px" :fullscreen="isMobile">
      <div class="export-format-switch">
        <el-radio-group v-model="exportFormat" @change="refreshExport">
          <el-radio-button value="shell">Shell</el-radio-button>
          <el-radio-button value="js">JavaScript</el-radio-button>
          <el-radio-button value="python">Python</el-radio-button>
        </el-radio-group>
        <el-button size="small" @click="copyExport">
          <el-icon><CopyDocument /></el-icon>复制
        </el-button>
      </div>
      <!-- 导出预览刻意保持明文（主动导出要的就是原值，#127），不受页面遮蔽模式影响，所以在这里提醒一句 -->
      <el-alert
        type="info"
        :closable="false"
        show-icon
        style="margin-bottom: 12px"
        :title="`导出范围：${exportScopeText}`"
        description="内容是明文，包含凭据，不受页面遮蔽设置影响。复制、截图或分享前请留意。"
      />
      <pre class="export-preview">{{ exportContent }}</pre>
      <!-- 移动端全屏时右上角的 × 由全局规则隐藏（有 footer 的弹窗一律如此），关闭入口放到右下角拇指够得着的地方。
           只在全屏（= isMobile，与上面 :fullscreen 同一个条件）时提供 footer 插槽，桌面弹窗结构不变。 -->
      <template v-if="isMobile" #footer>
        <el-button @click="showExportDialog = false">关闭</el-button>
      </template>
    </el-dialog>

    <EnvEditDialog
      v-model="showEditDialog"
      :mode="editDialogMode"
      :initial-data="currentEditEnv"
      :groups="groups"
      :submitting="editSubmitting"
      @save="handleSave"
    />

    <EnvImportDialog
      v-model="showImportDialog"
      :submitting="importSubmitting"
      @import="handleImport"
    />

    <EnvBatchRenameDialog
      v-model="showBatchRenameDialog"
      :submitting="batchRenameSubmitting"
      @confirm="confirmBatchRename"
    />

    <EnvBatchGroupDialog
      v-model="showBatchGroupDialog"
      :groups="groups"
      :submitting="batchGroupSubmitting"
      @confirm="confirmBatchGroup"
    />
  </div>
</template>

<style scoped lang="scss">
.envs-page {
  padding: 0;
}

/* ---- Page Header ---- */
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

/* ---- Toolbar ---- */
// 工具条：与定时任务页/执行日志页/订阅页对齐——上下统一间距、左右两区一行排布、gap 一致；
// 分组筛选（特有元素）沿用与搜索框相同的 12px 行内间距，宽度保持业务所需的 220px。
.toolbar {
  display: flex;
  justify-content: space-between;
  // 刻意【不】用 align-items: center（与 logs 页统一）：左槽任一支换成两行时，
  // center 会把只有 32px 的右区整个垂直居中到左槽的中线上，
  // 右侧按钮的中心比左区第一行低约 20px，看起来就是两边对不齐。
  // 改成 flex-start + 右区自己撑到 39px（= status-tabs 高度）并内部居中，两者都对齐到左区【第一行】的中心线。
  align-items: flex-start;
  margin: 14px 0;
  gap: 12px;
  flex-wrap: wrap;

  // 左槽：1×1 网格，筛选区与批量区【叠放在同一个格子里】，两支都常驻 DOM。
  // 这样左槽高度恒等于 max(两支高度)，勾选/取消勾选永远不改变工具栏高度。
  // align-items 必须是 start 不能是 center：任一支换成两行时，
  // center 会把只有一行的另一支垂直居中到两行的中线上，切过去时整排控件上下错位。
  // min-height 取 status-tabs 的实测高度 39px——批量按钮只有 32px，没有这个下限的话
  // 两支都是单行时整条工具栏会比筛选态矮 7px。
  &__left {
    display: grid;
    grid-template-columns: minmax(0, 1fr);
    align-items: start;
    flex: 1;
    min-width: 0;
    min-height: 39px;
  }

  // 筛选区：原来直接挂在 __left 上的那套横排（状态分段 + 搜索框 + 分组筛选），
  // 现在下沉一层与批量区叠放，间距/换行规则原样搬过来。
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

  // 三个输入控件：定宽 → 可伸缩。
  //
  // v3.2.5 加了「变量名筛选」，按原来的定宽（260 + 220 + 新增 220）筛选区自然宽会从 ~724px 涨到 ~966px，
  // 而 1280 宽 + 侧栏展开时左槽只有约 805px（1280 − 221 侧栏 − 40 页面内边距 − ~202 右区 − 12 gap），
  // 必然换成两行、白吃掉表格 ~50px 高度。
  //
  // 解法是让 flex-basis 取【收缩下限】、max-width 取【理想宽】：
  // 换行是按 flex-basis（hypothetical main size）算的，所以阈值只由下限决定 ——
  //   230(tabs) + 3×12(gap) + 160 + 150 + 150 ≈ 726px，和加控件之前的 ~724px 基本持平，换行点没有被推高；
  // 而有余量时 flex-grow 会把它们长回 260 / 220 / 220，宽屏观感与以前一致。
  // grow 权重 2:1:1 是刻意的：余量优先补给搜索框，它是最常用、最吃宽度的那个。
  //
  // 🔴 不要改回 width：写死宽度换行阈值就重新变成 966px，1280 + 展开侧栏必然两行。
  // 🔴 也不要只给搜索框设 flex 而让另外两个保持定宽：换行阈值取的是三者 basis 之和，漏一个就白做。
  &__search {
    flex: 2 1 160px;
    max-width: 260px;
  }

  &__group-filter {
    flex: 1 1 150px;
    max-width: 220px;
  }

  &__name-filter {
    flex: 1 1 150px;
    max-width: 220px;
  }
}

// 状态分段控件：与定时任务页/执行日志页/订阅页一致的灰底槽 + 选中态白底品牌色 + 1px 边框
.status-tabs {
  display: inline-flex;
  background: var(--el-fill-color-light);
  // 分段控件的灰底槽 → control 档。
  // 全站 13 处分段槽（含 global.scss 的通用类 .dd-seg-group）统一走 control：
  // 槽只比槽内的项高 6px（padding 3px），槽外圈取 surface(10px)、内层项取 control(6px)
  // 的话，选中项白底的四角会从灰底槽里探出来，露出一圈错位的角。同档才严丝合缝。
  border-radius: var(--dd-radius-control);
  padding: 3px;
  gap: 2px;
}

.status-tab {
  padding: 6px 14px;
  // 槽内的分段项 → control 档
  border-radius: var(--dd-radius-control);
  // 未选中态用透明边框占位，选中态只换边框颜色，避免尺寸跳动
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

// 批量操作条：搬进左槽后可用宽度由左槽给（右边还站着导出/新建），窄窗口下这六个按钮排不下时
// 必须允许换行，否则会横向溢出被裁掉；换行后的两行高度也会被左槽如实吃进去（见下面的叠放规则），
// 所以勾选前后工具栏依然等高。
// 这一支只在桌面渲染：移动端另有一套 .dd-mobile-batch-bar（见模板里的移动端工具栏），不吃这里的任何规则。
.batch-actions {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
  // 与 .toolbar__right 站同一条基线：右区也是 min-height:39px + 内部 center，中心线在 19.5px。
  // 批量条本身只有 32px，而左槽是 align-items: start（任一支换两行时不能把另一支压到中线去），
  // 不补这个下限它就贴在网格行顶端、中心线只有 16px，勾选后整排批量按钮会比右侧按钮高 3.5px。
  // 39px 本来就是左槽的 min-height，补上不会改变左槽高度，高度不变式照旧成立。
  min-height: 39px;
}

// 勾选数：纯文字级提示，用次级色，不跟旁边那排实体按钮抢视觉重量
.batch-actions__count {
  font-size: 13px;
  color: var(--el-text-color-secondary);
  white-space: nowrap;
  cursor: default;
}

// 左槽的两支叠放在同一个网格格子里：谁都不脱离文档流，所以两支都在为左槽撑高，
// 左槽高度 = max(两支高度)，切换时高度恒定不变，表格不会被工具栏推着重排。
// 切换只做 opacity：两个分支宽高差得远，一旦做尺寸过渡，左槽每帧变尺寸就会推着右侧的导出/新建来回移动，
// 而两支本身都是 flex-wrap 容器、每帧还要重算换行，整行会持续重排；opacity 不参与布局计算。
// 时长走令牌，prefers-reduced-motion 下自动降为 1ms 即等效关闭。
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
// 真做交叉淡出会有一段两排控件互相透视的重影，别为了「对称」把 visibility 加进 transition。
.is-swapped-out {
  opacity: 0;
  visibility: hidden;
}

// 本页原来有一份 .dd-status-switch-* 过渡（服务于状态列/移动卡片里「启用 ⇄ 禁用」那段文字的淡入淡出），
// 随状态列与移动端 switch 一起删除。其余用到同名过渡的页面（tasks / logs / deps / users /
// subscriptions / notifications / config-file）各自在自己的 scoped 样式里定义了一份，不受影响。

/* ---- Table Card ---- */
// 表格卡：无阴影，仅用 1px 边框与页面底色区分（dd-fixed-page 下的 flex + 内部滚动由全局规则接管）
.table-card {
  background: var(--el-bg-color);
  // 表格容器 → surface 档
  border-radius: var(--dd-radius-surface);
  border: 1px solid var(--el-border-color-lighter);
  overflow: hidden;
}

/* ---- Pagination Bar ---- */
// 分页条：与定时任务页/执行日志页/订阅页一致的间距收敛（margin-top 14px）
.pagination-bar {
  margin-top: 14px;
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 12px;
  padding: 0 4px;
}

.pagination-total {
  font-size: 13px;
  color: var(--el-text-color-secondary);
}

.pagination-actions {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: 12px;
  flex-wrap: wrap;
}

.page-size-picker {
  display: inline-flex;
  align-items: center;
  gap: 8px;
}

.page-size-picker__label {
  font-size: 13px;
  color: var(--el-text-color-secondary);
  white-space: nowrap;
}

/* ---- Table internals ---- */
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
}

:deep(.env-table--compact .el-table__cell) {
  padding-top: 8px;
  padding-bottom: 8px;
}

:deep(.env-table--compact .env-value-text),
:deep(.env-table--compact .env-remarks-text),
:deep(.env-table--compact .env-name) {
  font-size: 12px;
}

:deep(.env-table--compact .time-text) {
  font-size: 11px;
}

:deep(.env-table--compact .pinned-chip),
:deep(.env-table--compact .important-chip),
:deep(.env-table--compact .group-pill) {
  padding-top: 2px;
  padding-bottom: 2px;
  font-size: 11px;
}

/* ---- Mobile ---- */
.env-mobile-scroll {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

// 桌面空态/骨架容器（特有元素）：纯色底 + 1px 边框，明暗自动适配；保留自定义空态结构
.env-desktop-state {
  display: flex;
  align-items: center;
  justify-content: center;
  min-height: 360px;
  padding: 28px 24px;
  border: 1px solid var(--el-border-color-lighter);
  // 它站在表格卡的位置上（空态/加载态占位）→ 与 .table-card 同走 surface 档
  border-radius: var(--dd-radius-surface);
  background: var(--el-bg-color);
}

.env-desktop-state--loading {
  display: grid;
  align-content: start;
  justify-items: stretch;
  gap: 16px;
}

// 骨架屏（特有元素）：底色走 --el-fill-color 系令牌，微光用半透明表面色，明暗皆不串色
.env-skeleton {
  position: relative;
  overflow: hidden;
  // 骨架条是标题/工具栏/表格行的占位，尺寸都是控件量级 → control 档
  border-radius: var(--dd-radius-control);
  background: var(--el-fill-color);
}

.env-skeleton::after {
  content: '';
  position: absolute;
  inset: 0;
  transform: translateX(-100%);
  background: linear-gradient(
    90deg,
    transparent 0%,
    color-mix(in srgb, var(--el-bg-color) 70%, transparent) 48%,
    transparent 100%
  );
  animation: env-skeleton-shimmer 1.35s ease-in-out infinite;
}

.env-skeleton--title {
  width: min(320px, 32%);
  height: 22px;
}

.env-skeleton--toolbar {
  width: min(460px, 48%);
  height: 40px;
  margin-bottom: 8px;
}

.env-skeleton--row {
  width: 100%;
  height: 58px;
}

.env-empty-copy {
  display: inline-flex;
  flex-direction: column;
  gap: 6px;
  color: var(--el-text-color-secondary);
}

.env-empty-copy strong {
  font-size: 16px;
  color: var(--el-text-color-primary);
}

.env-empty-copy span {
  font-size: 13px;
}

.env-empty-actions {
  display: inline-flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
}

/* ---- Mobile Card ---- */
// 卡片骨架（首行 / 字段区 / 末行）全部用 global.scss 的 .dd-mobile-card__head / __rows / __footer 系共享类，
// 下面只写本页特有的几处覆盖，且一律带 .env-card 前缀：.env-name / .env-value-text / .group-pill 这些类与桌面表格共用，
// 不带前缀直接改基态会波及桌面（例如值列从单行省略变成两行、行高翻倍）。

// 名称：.env-name 是 scoped 单类 (0,2,0)，它的 13px 会压过全局 .dd-mobile-card__name (0,1,0) 的 16px，
// 这里在卡片里把字号交还回去；字重 / 行高 / 单行省略仍由 __name 给，等宽字体与主题色沿用 .env-name。
.env-card .env-name {
  font-size: 16px;
}

// 名称右侧的分组 / 置顶小标签组（与 .dd-mobile-card__head-tags 同挂，这里只改两处）：
//   - 名称优先：flex-basis 取 0，只分名称用剩的宽度。全局那条是 0 1 auto，会和名称按各自内容宽等比例一起缩，
//     分组一多，变量名就被砍得只剩几个字；变量名才是认卡片的依据，分组是次要信息。
//   - 放不下的整枚隐藏：flex-wrap + 定高 20px + overflow:hidden，挤不下的标签整枚换到第二行、被高度裁掉，
//     不会出现半截标签，也不会把首行撑高；只剩第一枚都放不下时，靠 .group-pill 基态的 max-width:100% 收成带省略号的短标签。
//   不做「+N」计数：它本身也要占宽，而且点开看全部分组有编辑弹窗。
.env-card__tags {
  flex: 1 1 0;
  flex-wrap: wrap;
  align-content: flex-start;
  height: 20px;
}

// 分组标签在卡片里收成 el-tag size="small" 的尺寸（20px 高、左右 7px、字重 500），与其它页卡片首行的状态标签同一规格。
// 只在卡片作用域里改：.group-pill 基态还服务于桌面表格的「分组」列与紧凑模式。
.env-card .group-pill {
  flex-shrink: 0;
  height: 20px;
  padding: 0 7px;
  gap: 4px;
  line-height: 1;
  font-weight: 500;
}

// 色标跟着缩到 6px，圆角仍是 .group-dot 基态固定的 2px（≤12px 分类色标不吃令牌的判据，理由见 .group-dot）
.env-card .group-dot {
  width: 6px;
  height: 6px;
}

// 置顶 / 重要标签缩到与分组标签同一尺寸；配色与字重不动（置顶金、重要紫）
.env-card .pinned-chip,
.env-card .important-chip {
  height: 20px;
  padding: 0 7px;
  line-height: 1;
}

// 「值」一行：最多两行，所以整行改成顶端对齐（全局 __row 是居中，两行值会把标签和小眼睛挤到两行中间）；
// 行高统一成 20px，标签、值的第一行、右侧 20px 高的按钮组三者落在同一条中线上。
.env-card__value-row {
  align-items: flex-start;
  line-height: 20px;
}

// 值文本同挂 .env-value-text（等宽字体、禁用卡降档）与全局 --clamp2（两行截断）。
// 但 .env-value-text 基态是给桌面值列的单行省略（display:block + nowrap），scoped (0,2,0) 压过全局修饰类 (0,1,0)，
// 不在卡片里把 display / white-space 交还回去，两行截断就不生效、仍是一行。
.env-card .env-value-text {
  display: -webkit-box;
  white-space: normal;
}

// 值后面的小眼睛 + 复制：一组紧挨的 18px 行内图标按钮，组内间距只由 gap 决定
.env-card__value-actions {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  height: 20px;
  flex-shrink: 0;

  // 清掉 EP 的 .el-button + .el-button { margin-left: 12px }，否则与 gap 叠加
  > .el-button + .el-button {
    margin-left: 0;
  }
}

// 移动端拖拽手柄（特有元素）：色板已走令牌，过渡统一到 motion/ease 令牌
.env-mobile-drag-handle {
  flex-shrink: 0;
  width: 30px;
  height: 30px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  border: 1px solid var(--el-border-color-lighter);
  background: var(--el-fill-color-light);
  // 30×30 的图标按钮，只在移动卡片里渲染 → 「按钮」角色令牌：rounded 面板在手机上与同屏的其它按钮一样放大到 16px，
  // square 面板仍是直角（v3.3.1 前吃 control 档，手机上会和旁边已经变圆的按钮对不上）
  border-radius: var(--dd-radius-button);
  color: var(--el-text-color-secondary);
  cursor: grab;
  touch-action: none;
  transition:
    background-color var(--dd-motion-fast) var(--dd-ease-standard),
    color var(--dd-motion-fast) var(--dd-ease-standard),
    border-color var(--dd-motion-fast) var(--dd-ease-standard);
  -webkit-user-select: none;
  user-select: none;
  padding: 0;
}

.env-mobile-drag-handle:active {
  cursor: grabbing;
  background: var(--el-color-primary-light-9);
  color: var(--el-color-primary);
  border-color: var(--el-color-primary-light-7);
}

// 禁用移动卡：与桌面禁用行同一套弱化（浅底 + 文本降一档），完整理由见下方
// 「---- Disabled Row ----」那段注释。
// global.scss 的 `.dd-mobile-card { background: var(--el-bg-color) }` 是 (0,1,0)，
// 这条 scoped 之后是 (0,2,0)，压得过。刻意排在 .env-card--pinned 之前：
// 那条目前只改 border-color / box-shadow、不改 background，当前虽不冲突，
// 但保持与桌面一致的「置顶写在后面、置顶赢」顺序，以后给置顶加底色时不用再回来调。
.env-card--disabled {
  background: var(--el-fill-color-lighter);
}

// 置顶移动卡（特有元素）：只保留置顶语义的橙色左缘标识，不再叠加环境光阴影
.env-card--pinned {
  border-color: rgba(245, 166, 35, 0.28);
  box-shadow: inset 4px 0 0 #f5a623;
}

/* ---- Cell Styles ---- */
// 表格操作列：类名与定时任务页/依赖管理页统一叫 .action-btns（原来叫 .action-group，
// 是本页独有的一个名字，改名只是为了三页一致，行为不变）——居中排布 + 4px 间隙。
.action-btns {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 4px;

  // EP 自带 `.el-button + .el-button { margin-left: 12px }` 会叠加在上面的 flex gap 上。
  // 撑破列宽后 .cell 的 overflow:hidden 变成可滚动容器，点右侧按钮会触发 scrollIntoView
  // 让整行左移且不复位——这是本列最容易踩的坑，所以间距统一交给 gap
  //（与 tasks / deps / subscriptions 三页一致）。
  //
  // Split Button 内部就是相邻的两个 .el-button（主体 + caret），这条选择器照样命中；
  // EP 的 el-button-group 自己也置 0，两处一致，任何一处改动都不会让 caret 被推开。
  // 现在格子里还多了外置的「禁用 / 启用」，它与 Split Button 之间的间距也只由 gap:4px 决定。
  :deep(.el-button + .el-button) {
    margin-left: 0;
  }

  // 原来这里还有一条 `:deep(.el-button) { padding: 4px 8px }` 的收窄覆写，是当年为了把
  // Split Button 塞进 110px 列宽才加的。列宽已按 172 重算（见上方操作列注释），
  // 定时任务页也在 v3.0.x 去掉了同一条，这里跟着删掉、改回 EP small 档默认的 5px 11px ——
  // 列宽估算用的正是这个默认值，把它改回收窄档会让上面那笔账对不上。
}

.env-name-wrap {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
}

// 名称本体是一段【纯文本】（<span>），不是按钮也不是链接：可划选、可复制是硬要求，
// 详见模板里那条 🔴 注释。所以这里既没有 cursor:pointer 也没有 hover 下划线 ——
// 名称本身不可点，给它挂可点反馈只会骗人（真正可点的是旁边的 .env-name-filter-btn）。
// 保留 --el-color-primary 是它一直以来的取色（等宽字 + 主题色 = 标识符强调，不是链接语义）。
//
// flex 从 `1`（= 1 1 0%）改成 `0 1 auto`：名称后面新增了 .env-name-filter-btn，
// 名称若继续【撑满】整格，那颗按钮会被顶到名称列的最右缘、离它要操作的名字十几个字远，
// 还紧贴着「值」列的文字，看着像是下一列的东西。改成不 grow 之后按钮紧跟在名字后面，
// 与「值」列「文本 + 复制按钮」的排法一致。
// 🔴 min-width:0 才是省略号能生效的真正前提（允许 flex 项收缩到 min-content 以下），别删；
//    flex-shrink 保持默认的 1，所以名字变长时照样收缩出省略号。
// 🔴 原来靠 flex:1 把「重要」「置顶」标签顶到右缘的效果没有丢，改由 .env-name-filter-btn 的
//    margin-right:auto 承担（见下条规则），两处要一起看。
.env-name {
  min-width: 0;
  flex: 0 1 auto;
  font-family: var(--dd-font-mono);
  font-size: 13px;
  color: var(--el-color-primary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

// 「只看这个变量名」按钮：只在桌面表格的名称列里有（v3.3.1 起移动卡片不再放这颗按钮，
// 手机上不提供变量名筛选，理由见模板里移动卡片首行的注释）。
// 除 margin-right 外，其余声明与「值」列的 .env-copy-btn 逐字相同 —— 同一张表里的行内图标按钮
// 只有一款外观；padding 2px 也正是名称列 min-width 那笔账里按 18px 计的来源（图标 14 + 2×2），
// 改动它要回去同步改列宽。
// 没有 hover/opacity 门控：与 .env-copy-btn 同一条不变量（触屏没有 hover，且透明元素照样进 Tab 焦点序）。
// 焦点环不用自己写：EP 的 el-button 自带 :focus-visible 样式，不像原来那个裸 <button> 需要手动补。
.env-name-filter-btn {
  flex-shrink: 0;
  // 接手 .env-name 原来那份 flex:1 的活：本格的剩余空间全归这条 auto 边距，
  // 于是按钮贴着名字、后面的「重要」「置顶」标签仍然靠在最右缘（与改动前逐像素一致）。
  // 两枚标签都没有的行看不出区别（空白本来就在右边），别当成冗余删掉。
  margin-right: auto;
  color: var(--el-text-color-secondary);
  padding: 2px;
  // 只留颜色过渡；时长走令牌而不是写死毫秒数，理由同 .env-copy-btn（写死的绕不过 reduced-motion 降级）
  transition: color var(--dd-motion-fast) var(--dd-ease-standard);
}

// 置顶 / 重要标签：纯色底 + 1px 边框（原胶囊渐变与内描边已去掉），两枚同尺寸同形状，只差配色
.pinned-chip,
.important-chip {
  flex-shrink: 0;
  display: inline-flex;
  align-items: center;
  gap: 4px;
  padding: 2px 8px;
  // 走 control 而不是 pill：它和分组标签 .group-pill 是同一格里的一组同级小标签
  // （紧凑模式那条规则也是把两者一起收的），做成胶囊会跟旁边的分组标签形状打架。
  border-radius: var(--dd-radius-control);
  font-size: 12px;
  font-weight: 700;
}

.pinned-chip {
  color: #8a4b00;
  background: #ffd66b;
  border: 1px solid rgba(196, 118, 0, 0.28);
}

// 重要标签（APP #16）：紫色是语义识别色（同置顶金色，design-system §1 硬规则 1 允许写死），
// 本页别处没用过紫色。和置顶一样明暗两态不变（一枚浅色贴纸）：字 #6d28d9 压浅紫底约 6:1，过 AA。
// 颜色之外还有锁图标 +「重要」两个字，不只靠颜色传达状态。
.important-chip {
  color: #6d28d9;
  background: #ede9fe;
  border: 1px solid rgba(139, 92, 246, 0.35);
}

.env-value-text,
.env-remarks-text {
  display: block;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.env-value-cell {
  display: flex;
  align-items: center;
  gap: 4px;
  min-width: 0;
}

.env-value-text {
  font-family: var(--dd-font-mono);
  color: var(--el-text-color-primary);
}

// 值列的复制按钮：常驻显示（v3.2.0 / #109-4）。
// 原来是 `opacity: 0` + 一条 `:deep(.el-table__row:hover) .env-copy-btn { opacity: 1 }`，
// 即整页唯一一个「鼠标移上去才出现」的控件，两条已一并删除：
//   1) 触摸屏没有 hover，平板/触屏笔记本上这个按钮根本摸不出来；
//   2) opacity:0 的元素照常进 Tab 焦点序，键盘用户会聚焦到一个看不见的按钮上；
//   3) 移动端卡片里的同款复制按钮本来就是常驻的，两端不一致。
// 顺带说明：本页操作栏（.action-btns）一直就是常驻的，没有任何 hover/opacity 门控 ——
// 那是要守住的不变量，别顺手给它加 hover 显隐。
// 值列的小眼睛（.env-reveal-btn，#127）与复制按钮同一款：尺寸 18px（图标 14 + padding 2×2）也是值列 min-width
// 那笔像素账的来源，改这里要回去同步。它同样常驻、不做 hover 显隐，理由同上面 1)~3)。
// v3.3.1 起移动卡片「值」一行里的两颗也挂这两个类，两端同一款外观（卡片里 20px 的按钮组高度按 18px 算）。
.env-copy-btn,
.env-reveal-btn {
  flex-shrink: 0;
  color: var(--el-text-color-secondary);
  padding: 2px;
  // 只留颜色过渡（EP link 按钮 hover 换色）。时长走令牌而不是写死毫秒数：
  // 写死的会绕过 prefers-reduced-motion 降级，令牌在降级媒体查询里会被压到 1ms。
  transition: color var(--dd-motion-fast) var(--dd-ease-standard);
}

.env-remarks-text {
  color: var(--el-text-color-regular);
}

// 值列表头：「值」+ 三态切换（#127）排成一行
.env-value-header {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  max-width: 100%;
}

// 表头里的模式切换：link 按钮，字重压回 500，不和加粗的列名抢眼。
// 「全部明文」是三档里最不设防的一档，用警示色标出来，扫一眼表头就知道现在什么都没遮。
.env-mask-mode-trigger {
  font-weight: 500;
  color: var(--el-text-color-secondary);
  padding: 2px;
  transition: color var(--dd-motion-fast) var(--dd-ease-standard);

  // 类名刻意不叫 is-plain：那是 EP 给 plain 按钮的状态类，.el-button.is-plain 会改掉 hover / focus 时的边框与底色
  &.is-unmasked {
    color: var(--el-color-warning);
  }
}

// EP 只给「图标后面紧跟的文字」加 6px 左边距，文字后面的箭头要自己隔开一点
.env-mask-mode-trigger__caret {
  margin-left: 2px;
}

// 三态菜单项：勾 + 名称 + 一行说明。菜单虽然 teleport 到了 body，但这些 span 是写在本组件模板里的，
// 仍然带本组件的 scoped 标记，所以样式可以留在 scoped 块里（只有 EP 自己渲染的 li 命中不到）。
.env-mask-option {
  display: flex;
  align-items: flex-start;
  gap: 8px;
  padding: 2px 0;
}

.env-mask-option__check {
  flex-shrink: 0;
  // 图标 14px，和第一行 20px 行高垂直居中
  margin-top: 3px;
  color: var(--el-color-primary);
  // 不是当前项也要占位，三项的文字才能左对齐
  visibility: hidden;
}

.env-mask-option.is-active .env-mask-option__check {
  visibility: visible;
}

.env-mask-option__text {
  display: flex;
  flex-direction: column;
  min-width: 0;
}

.env-mask-option__label {
  font-size: 13px;
  line-height: 20px;
  color: var(--el-text-color-primary);
}

.env-mask-option.is-active .env-mask-option__label {
  color: var(--el-color-primary);
  font-weight: 600;
}

.env-mask-option__hint {
  font-size: 12px;
  line-height: 18px;
  color: var(--el-text-color-secondary);
}

.env-empty-text {
  font-size: 12px;
  color: var(--el-text-color-placeholder);
}

// 分组标签：纯色底；原来的内描边改成真实 1px 边框，保证浅底在白色表格上仍有分隔
.group-pill {
  --group-hue: 210;
  display: inline-flex;
  align-items: center;
  gap: 6px;
  max-width: 100%;
  padding: 4px 10px;
  // 名字里虽然叫 pill，但它是分组标签（tag 一类）→ control 档，和 .pinned-chip 保持同档
  border-radius: var(--dd-radius-control);
  font-size: 12px;
  font-weight: 600;
  color: hsl(var(--group-hue) 55% 32%);
  background: hsl(var(--group-hue) 85% 96%);
  border: 1px solid hsl(var(--group-hue) 72% 78% / 0.7);
}

.group-pill-list {
  display: flex;
  flex-wrap: wrap;
  justify-content: flex-end;
  gap: 6px;
  min-width: 0;
}

.group-pill-list--table {
  justify-content: center;
}

.group-pill__text {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

// 分组色标
.group-dot {
  width: 7px;
  height: 7px;
  // 🔴 固定 2px，既不进圆形白名单、也不吃 control 档（v3.1.0 新立的「≤12px 小色标」判据）。
  //
  // 它的颜色由 --group-hue（按分组名派生）决定，**与运行状态无关**，表达的是「分类」
  // 而不是「这个东西现在是什么状态」—— 语义上就不该和 .pulse-dot / .env-status 那类
  // 状态灯长得一样。原来写 50% 是照着 .pulse-dot 抄的，理由并不成立。
  //
  // 但也不能改成吃 control：7×7 的盒子上，6px 半径会触发 CSS 的圆角等比收缩
  // （一条边上两个半径之和 12px > 边长 7px ⇒ 全部夹到 3.5px），结果还是一个正圆，
  // 等于什么都没改。只有写死一个小于半边长的值才稳得住方形轮廓。
  border-radius: 2px;
  background: hsl(var(--group-hue) 72% 48%);
  flex-shrink: 0;
}

// 环境变量状态灯：名称前的 8×8 圆点，绿 = 已启用 / 红 = 已禁用（桌面表格与移动卡片同用）。
// 它接替了原来那一整列「状态」（el-switch + 启用/禁用文字），随之删掉的还有
// .env-status-cell / .env-status-inline / .env-status-text 三个类和本页那份
// .dd-status-switch-* 过渡（切换动效跟着 switch 一起没了，其余页面各有自己的一份，不受影响）。
//
// 🔴 固定 50%：这是 design-system §1 的「状态圆点」白名单，不是漏改，圆角扫描别把它当漏网点改掉。
//    两个理由：
//    a) 圆点 = 运行状态是全行业约定；而同一格里 7×7 的 .group-dot 反过来刻意写死 2px 方角来表达
//       「分类」（见它自己那段注释）。两者一圆一方，正是靠形状把「分类色标」与「状态灯」分开的，
//       任何一边改形状都会让它俩撞脸。
//    b) 就算改成吃 control(6px) 也是白改：8×8 的盒子上，一条边两个半径之和 12px > 边长 8px，
//       会触发 CSS 圆角等比收缩、全部夹到 4px = 还是正圆，绕远路得到同一结果还多一次误改机会。
//
// 取色用 --el-color-success / --el-color-danger 两个语义令牌，暗色主题自动适配；
// 不要写死 #10b981 / #ef4444 之类，写死的在暗色下不跟随、会串色。
// 红绿是最典型的色觉障碍撞色对，只靠颜色传达状态对红绿色盲用户等于没有状态，
// 所以模板上必须同时挂 title（鼠标悬停）与 role="img" + aria-label（读屏）——
// role 不能省：光有 aria-label 的裸 <span> 不是可访问对象，读屏一般不会念出来。
.env-status-dot {
  flex-shrink: 0;
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: var(--el-color-danger);
}

.env-status-dot.is-enabled {
  background: var(--el-color-success);
}

.time-text {
  font-family: var(--dd-font-mono);
  font-size: 12px;
  color: var(--el-text-color-regular);
}

// 桌面拖拽手柄（特有元素）：色板已走令牌，过渡统一到 motion/ease 令牌
.env-drag-handle {
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
}

:deep(.env-drag-col) {
  padding: 0 !important;
}

/* ---- Disabled Row ---- */
// 禁用行的整行弱化（issue #109 追评：「禁用的变量文字淡一点，更突出已禁用状态，
// 但不确定会不会影响可读性」）。提出者的担心是对的，所以这里**不写整行 opacity**，
// 下面是实算的数据：
//
// 明色下表格行底是 --el-fill-color-blank(#fff)，按 WCAG 相对亮度算出的基线对比度是
//   .env-name        --el-color-primary(#409eff)      2.78:1  ← 本来就不到 AA 的 4.5:1
//   .env-value-text  --el-text-color-primary(#303133) 13.02:1
//   .env-remarks-text / .time-text
//                    --el-text-color-regular(#606266)  6.11:1
// 整行叠 opacity 之后（前景与白底混合再算）：
//   α=0.75 → primary 5.88 / regular 3.49
//   α=0.85 → primary 8.08 / regular 4.33   ← 备注仍然不到 4.5，已经破线
//   α=0.90 → primary 9.51 / regular 4.84   ← 到这一档人眼已经看不出「淡了」
// 也就是「看得出被调暗」和「读得清」在明色下不能同时成立。暗色余量很大，
// 但不能为暗色牺牲明色。
//
// 改用【浅底 + 文本降一档语义令牌】：弱化感来自「彩色 → 中性灰」的色相变化，不是变淡。
//   名称 --el-color-primary(2.78:1) → --el-text-color-regular(在 #fafafa 上 5.85:1)，对比度反而升了
//   值   --el-text-color-primary(12.47:1) → --el-text-color-regular(5.85:1)，明显降一档且仍过 AA
//   备注 / 更新时间 已经是 regular = 触底，不再往下压（降到 secondary 只有 2.95:1）
// 红点与操作列的「启用」按钮保持原样，它们仍是主要的状态载体；
// 分组标签 / 复制按钮 / 拖拽手柄一律不动 —— 前者与启用状态正交，
// 后两者在禁用行上仍然可点，调暗一个能点的控件会误导用户以为它失效了。
//
// 🔴 绝不要改成给 tr / td 写 opacity：操作列是 fixed="right"，EP 的固定列是
//    `position: sticky !important` + z-index，`opacity < 1` 会造出新的层叠上下文、打乱固定列层级。
// 🔴 选择器里的 `.el-table` 与下面置顶行那条同理，是为了压过 EP 的
//    `td.el-table-fixed-column--right { background: inherit }`(0,2,2)，别删。
// 🔴 本块必须排在 `.env-row-pinned` **之前**：两者特异性同为 (0,3,1)，靠书写顺序让置顶赢
//    —— 既置顶又禁用的行仍显示橙色底（置顶是用户主动标记，语义优先级更高）。
// 🔴 不要用 --el-fill-color-light：那正是 EP 的行 hover 底色，用了会让禁用行看起来像被永久悬停。
// 悬停禁用行时 EP 的 hover 底(0,3,2；暗色下还带 !important)会盖过这条 —— 与置顶行一致，
// 是期望行为，别去 !important 强压，强压会让用户失去「鼠标停在哪一行」的反馈。
:deep(.el-table .env-row-disabled > td) {
  background: var(--el-fill-color-lighter);
}

// 桌面与移动共用同一条文本降档规则（两端本来就共用 .env-name / .env-value-text 两个类）
:deep(.env-row-disabled) .env-name,
:deep(.env-row-disabled) .env-value-text,
.env-card--disabled .env-name,
.env-card--disabled .env-value-text {
  color: var(--el-text-color-regular);
}

/* ---- Pinned Row ---- */
// 置顶行：整行纯色淡橙底（原来的横向渐变已去掉），左缘 4px 橙色标识保留。
//
// 🔴 选择器里的 `.el-table` 不是可有可无的层级修饰，是为了压过 EP 的固定列规则，别顺手删掉：
// 操作列加 fixed="right" 之后，EP 会给这一格补上
// `.el-table__body-wrapper tr td.el-table-fixed-column--right { background: inherit }`（特异性 0,2,2），
// 而本规则原来是 `:deep(.env-row-pinned > td)` = `[data-v-x] .env-row-pinned > td`（0,2,1），
// 类数相同、元素数少一个 ⇒ 输给 EP，置顶行最右边那一格会退回普通行底色，整行淡橙底缺一块。
// 加上 `.el-table` 后是 (0,3,1)，类数 3 > 2 直接赢下 EP 那条。
// 同时它仍然【输给】EP 的行 hover（`.el-table__body tr.hover-row > td.el-table__cell` = 0,3,2），
// 所以置顶行悬停仍会正常变成 hover 底色，与改动前一致。
:deep(.el-table .env-row-pinned > td) {
  background: color-mix(in srgb, #ffd66b 12%, var(--el-table-tr-bg-color));
}

:deep(.env-row-pinned > td:first-child) {
  box-shadow: inset 4px 0 0 #f5a623;
}

/* ---- Export Dialog ---- */
.export-format-switch {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 12px;
}

.export-preview {
  background: var(--el-bg-color-page);
  // 导出预览是弹窗里的一整块代码面板 → surface 档
  border-radius: var(--dd-radius-surface);
  padding: 16px;
  font-family: var(--dd-font-mono);
  font-size: 13px;
  line-height: 1.6;
  max-height: 400px;
  overflow-y: auto;
  margin: 0;
  white-space: pre-wrap;
  word-break: break-all;
}

@keyframes env-skeleton-shimmer {
  100% {
    transform: translateX(100%);
  }
}

/* ---- Responsive: 768px (Mobile) ---- */
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

  // 这里不再有 .toolbar / .status-tabs / 批量区的移动端规则（v3.3.1）：桌面工具栏整支是 v-else，≤768 下根本不渲染，
  // 手机上的工具栏换成模板里 .env-mobile-toolbar 那一支，样式全部来自 global.scss 的 .dd-mobile-toolbar / .dd-mobile-batch-bar。
  // useResponsive 的 isMobile（innerWidth ≤ 768）与这条媒体查询口径一致，都按含滚动条的视口宽判定。

  .pagination-bar {
    flex-direction: column;
    align-items: stretch;
  }

  .pagination-actions {
    justify-content: space-between;
  }

  .page-size-picker {
    width: 100%;
    justify-content: space-between;
  }
}

// ===== 入场动画 =====
// 与定时任务页/执行日志页/订阅页统一：只对卡片级容器（工具条 / 表格卡 / 移动列表）做克制的淡入上移 + 轻微错落；
// 不给表格每一行或每张移动卡做 stagger；骨架屏/桌面空态（.env-desktop-state）不参与入场，避免与其自身加载/空态冲突。
// 时长走令牌，prefers-reduced-motion 时令牌自动降为 1ms 即等效关闭。
@keyframes dd-envs-rise-in {
  from {
    opacity: 0;
    transform: translateY(12px);
  }
  to {
    opacity: 1;
    transform: translateY(0);
  }
}

// 移动端工具栏挂在外层 .env-mobile-toolbar 上：批量栏与搜索栏在它里面 v-if 切换，外层不重建，动画只在进页时跑一次
.toolbar,
.env-mobile-toolbar,
.table-card,
.dd-mobile-list {
  animation: dd-envs-rise-in var(--dd-motion-page) var(--dd-ease-decelerate) both;
}

// 轻微错落：工具条先入，表格卡/移动列表略晚
.table-card,
.dd-mobile-list {
  animation-delay: 60ms;
}
</style>

<style lang="scss">
/* 这些类由 sortablejs 在运行时动态加到 el-table 行 / body 上的拖拽克隆上，
   不在本组件 scoped 作用域内，必须用全局样式才能命中。 */

/* 被拖起的克隆行：不做放大与投影，改为实底 + 主色描边标出正在拖动的行。
   用 outline 而不是 border：el-table 是 border-collapse: separate，tr 上的 border 不会绘制。 */
.sortable-drag {
  background: var(--el-bg-color);
  opacity: 1 !important;
  // 保持 0，不吃令牌：这是铺满表格宽度的一整行 <tr>，贴着 table-card 的内边；
  // 给它加圆角只会在行两端与表格边框之间露出缺口（且 border-collapse:separate 下 tr 圆角几乎不可见）。
  border-radius: 0;
  cursor: grabbing;
  outline: 1px solid var(--el-color-primary);
  outline-offset: -1px;
}

/* 落点占位：高亮"插槽"，清楚指示会落到哪（主色浅底本身已足够区分，不再叠内描边） */
.sortable-ghost {
  background: var(--el-color-primary-light-9) !important;
  // 保持 0，不吃令牌：与 .sortable-drag 同理，它是贴边铺满的落点占位行
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
