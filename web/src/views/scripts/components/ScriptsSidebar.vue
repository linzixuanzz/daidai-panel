<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import {
  DocumentAdd,
  Fold,
  FolderAdd,
  Refresh,
  Search,
  Sort,
  Upload,
  VideoPlay
} from '@element-plus/icons-vue'
import ScriptTreeNode from './ScriptTreeNode.vue'
import DdSplitButton from '@/components/ui/DdSplitButton.vue'
import type { SplitButtonItem } from '@/components/ui/DdSplitButton.vue'
import { shouldReduceMotion } from '@/utils/panelAppearance'
import type { TreeNode } from '../types'

/** el-tree 的 getNode() 返回 EP 内部的节点对象，定位只用到这几个字段 */
interface TreeStoreNode {
  level: number
  visible: boolean
  parent: TreeStoreNode | null
}

const props = defineProps<{
  isMobile: boolean
  mobileShowEditor: boolean
  /** 桌面端目录树是否收起。index.vue 传的是按布局修正过的值，≤1024 恒为 false */
  sidebarCollapsed?: boolean
  /** 编辑器当前打开的文件，与树节点 key 同一口径（openFile 已按后端规则规范化过） */
  currentFile: string
  /** openFile 每成功一次 +1（加载失败回滚时，仅当加载期间树重建过才 +1）。同一文件再次打开时 currentFile 不变，但同样要重新定位 */
  revealTicket: number
  treeLoading: boolean
  fileTree: TreeNode[]
  allowDrag: (draggingNode: any) => boolean
  allowDrop: (draggingNode: any, dropNode: any, type: string) => boolean
  onOpenCreateFile: () => void
  onOpenCreateDir: () => void
  onOpenUpload: () => void
  onOpenCodeRunner: () => void
  onRefresh: () => void | Promise<void>
  onNodeClick: (data: TreeNode) => void | Promise<void>
  onNodeDrop: (draggingNode: any, dropNode: any, dropType: string) => void | Promise<void>
  onOpenRename: (path: string) => void
  onDelete: (path: string, isDir: boolean) => void | Promise<void>
  onMoveToRoot?: (path: string, isDir: boolean) => void | Promise<void>
  /** 收起目录树。只在桌面宽屏可用，≤1024 由 mobileShowEditor 的互斥模式负责藏列表 */
  onToggleCollapse: () => void
}>()

const sidebarRef = ref<HTMLElement>()
const treeContainerRef = ref<HTMLElement>()
const treeRef = ref()
const searchKeyword = ref('')

/**
 * 「新建」Split Button 的菜单项。
 *
 * 主体是「新建文件」——侧边栏这三个入口里最高频的一个，而且点错了只是弹出一个
 * 创建表单，可以直接关掉，代价最小。「上传文件」会带来外部文件落盘，语义比前两个重，
 * 沿用原下拉里的 divided 与前两项分开。这三项都不是不可逆操作，因此没有 danger 项。
 */
const newActionItems: SplitButtonItem[] = [
  { key: 'dir', label: '新建目录', icon: FolderAdd },
  { key: 'upload', label: '上传文件', icon: Upload, divided: true }
]

function onNewAction(key: string) {
  if (key === 'dir') props.onOpenCreateDir()
  else if (key === 'upload') props.onOpenUpload()
}

// ===== 目录树排序（issue #160）=====
// 纯前端排序：目录始终在文件前，同层按所选规则；时间相同或缺失按名称兜底。
// 「创建时间」取 btime 与 mtime 中较早的那个（issue #169）：迁移拷贝（cp -a）、解压备份会把所有文件的 btime
// 刷成拷贝那一刻（全员相同、排序退回按名称），mtime 却保留原值；btime 比 mtime 晚就说明它记的不是真正的创建时间。
// 只有其一时取那一个，都没有当 0。只影响排序比较，接口返回的原始值不变。
type ScriptSortMode = 'name_asc' | 'name_desc' | 'btime_desc' | 'btime_asc' | 'mtime_desc'
const SCRIPTS_SORT_MODE_KEY = 'dd:scripts:sort_mode'
const sortOptions: { value: ScriptSortMode; label: string }[] = [
  { value: 'name_asc', label: '名称 A→Z' },
  { value: 'name_desc', label: '名称 Z→A' },
  { value: 'btime_desc', label: '创建时间 新→旧' },
  { value: 'btime_asc', label: '创建时间 旧→新' },
  { value: 'mtime_desc', label: '修改时间 新→旧' }
]
// 中文按拼音、数字按大小（2 在 10 前）、不分大小写
const nameCollator = new Intl.Collator('zh-CN', { numeric: true, sensitivity: 'base' })
// zh-CN 规则会把汉字整体排到英文前面；旧版（后端字节序）是数字、英文在前，所以先按首字符是不是汉字分组
const hanStartPattern = /^\p{Script=Han}/u

function readStoredSortMode(): ScriptSortMode {
  try {
    const stored = window.localStorage.getItem(SCRIPTS_SORT_MODE_KEY)
    return sortOptions.some(option => option.value === stored) ? (stored as ScriptSortMode) : 'name_asc'
  } catch {
    // 隐私模式 / 禁用存储时 getItem 会抛错，回到默认
    return 'name_asc'
  }
}

const sortMode = ref<ScriptSortMode>(readStoredSortMode())

function onSortCommand(command: string) {
  const mode = command as ScriptSortMode
  sortMode.value = mode
  try {
    window.localStorage.setItem(SCRIPTS_SORT_MODE_KEY, mode)
  } catch {
    // 写失败只是下次记不住，静默忽略
  }
}

function sortTreeNodes(nodes: TreeNode[], mode: ScriptSortMode): TreeNode[] {
  const copied = nodes.map(node => (node.isLeaf ? node : { ...node, children: sortTreeNodes(node.children || [], mode) }))
  return copied.sort((a, b) => {
    if (a.isLeaf !== b.isLeaf) return a.isLeaf ? 1 : -1
    let diff = 0
    if (mode === 'btime_desc' || mode === 'btime_asc') {
      // 两个都有取较早者；只有一个用那一个；都没有当 0
      const aCreated = a.btime != null && a.mtime != null ? Math.min(a.btime, a.mtime) : (a.btime ?? a.mtime ?? 0)
      const bCreated = b.btime != null && b.mtime != null ? Math.min(b.btime, b.mtime) : (b.btime ?? b.mtime ?? 0)
      diff = aCreated - bCreated
      if (mode === 'btime_desc') diff = -diff
    } else if (mode === 'mtime_desc') {
      diff = (b.mtime ?? 0) - (a.mtime ?? 0)
    }
    if (diff !== 0) return diff
    // 非汉字开头的组在前，组内再用 collator；Z→A 整体取反，时间排序的兜底也走这里
    const aHan = hanStartPattern.test(a.title)
    const bHan = hanStartPattern.test(b.title)
    const byName = aHan !== bHan ? (aHan ? 1 : -1) : nameCollator.compare(a.title, b.title)
    return mode === 'name_desc' ? -byName : byName
  })
}

// el-tree 吃排好序的副本；搜索过滤是在这份数据上隐藏节点，顺序自然一致
const sortedFileTree = computed(() => sortTreeNodes(props.fileTree, sortMode.value))

function filterNode(value: string, data: TreeNode) {
  if (!value) return true
  return (data.title || '').toLowerCase().includes(value.toLowerCase())
}

watch(searchKeyword, (val) => {
  treeRef.value?.filter(val)
})

// ===== 目录树定位（issue #136）=====
// 打开脚本时让左侧树跟上：展开祖先、高亮当前文件、滚到可见（≥769 在 .sidebar-tree 里滚，≤768 滚外层布局容器）。
// 下面的写法由 EP 2.13.5 el-tree 的几条行为决定（Node 实测过）：
// 1. setCurrentKey 找不到 key 时【不清】旧高亮 → 一律先 getNode 判断，找不到就显式 setCurrentKey(null)；
// 2. setCurrentKey(key, true) 会把祖先全部展开，包括用户刚手动收起的那一层 → 只有「打开文件 / 树刷新」传 true，
//    把高亮拉回当前文件时必须传 false，否则当前文件所在的目录永远收不起来；
// 3. 每次 setData（即每次 loadTree）都会丢掉展开与过滤状态 → 树刷新后补套一次搜索过滤再定位；
// 4. 点节点时 EP 先改高亮、再 emit node-click → 点目录、取消切换、加载失败后都要把高亮拉回当前文件。

/** 定位序号：每次定位 / 补滚动开始时占一个号，await 回来发现号被后来者占了就放弃 */
let revealSeq = 0

const COLLAPSE_MOVING_SELECTOR = '.el-collapse-transition-enter-active, .el-collapse-transition-leave-active'
/** EP 折叠动画 0.3s，加上 Vue 过渡收尾的一两帧；超过这个数不再等，按当下的位置滚 */
const TREE_SETTLE_MAX_MS = 400

/** 侧栏此刻看不见：≤1024 切到了编辑器（v-show 藏起），或桌面端收起成 0 宽 */
const sidebarHidden = computed(() => (props.isMobile && props.mobileShowEditor) || Boolean(props.sidebarCollapsed))

function isRevealStale(seq: number, path: string) {
  return seq !== revealSeq || path !== props.currentFile
}

function setHighlight(key: string, expandParents: boolean) {
  const tree = treeRef.value
  if (!tree) return
  if (key && tree.getNode(key)) {
    tree.setCurrentKey(key, expandParents)
  } else {
    tree.setCurrentKey(null)
  }
}

/**
 * 等树这一轮更新真正落到 DOM 上。
 * 数 nextTick 数不准：tree-node 的 expanded ref 是在 flush 期间另登记的 nextTick 里才翻的，
 * 展开动画要到再下一轮 flush 才开始；EP 的 filter 也是一串长度不定的 await 链。
 * 垫一个宏任务，前面排着的微任务（含后续几轮 flush）就都跑完了。
 */
async function waitTreeFlushed() {
  await nextTick()
  await new Promise<void>(resolve => window.setTimeout(resolve, 0))
}

/**
 * 等树里正在跑的展开 / 收起动画结束。动画期间 .el-tree-node__children 的 max-height 还在涨，量出来的位置是截断的。
 * 不只等目标的祖先：loadTree 之后 EP 会带动画收起用户手动展开过的其它目录，它们在目标上方时同样会把目标往上推。
 * 判据用 Vue 过渡的 active class，不用 transitionend：EP 这条过渡声明了 max-height 和上下 padding 三个属性，
 * 实际只有 max-height 在变，Vue 等不齐三次 transitionend，要靠自己的兜底定时器收尾，
 * 清掉 max-height 的 afterEnter 也在那时才跑 —— class 摘掉的那一刻布局才算真正落定。
 */
function waitCollapseTransitions(container: HTMLElement) {
  return new Promise<void>((resolve) => {
    const startedAt = Date.now()
    const check = () => {
      if (!container.querySelector(COLLAPSE_MOVING_SELECTOR) || Date.now() - startedAt >= TREE_SETTLE_MAX_MS) {
        resolve()
        return
      }
      window.setTimeout(check, 16)
    }
    check()
  })
}

/**
 * 侧栏卡从看不见变为可见时，等卡片自身的动画跑完再量：
 * - 桌面端展开目录树：卡片宽度从 0 过渡回来，宽度接近 0 的那几帧表头被挤高、树容器偏矮；
 * - ≤1024 从编辑器返回列表：卡片重播入场关键帧（translateY）。在 .sidebar-tree 里滚时树和行一起平移、不受影响，
 *   但 ≤768 真正在滚的是外层的 .layout-main（见 findScrollContainer），它不跟着平移，不等会差出一段位移。
 */
async function waitSidebarAnimations() {
  const el = sidebarRef.value
  if (!el || typeof el.getAnimations !== 'function') return
  const running = el.getAnimations()
  if (running.length === 0) return
  await Promise.race([
    Promise.allSettled(running.map(animation => animation.finished)),
    new Promise<void>(resolve => window.setTimeout(resolve, TREE_SETTLE_MAX_MS))
  ])
}

/** 节点自身和所有祖先都没被搜索过滤藏掉 */
function isShownUnderFilter(node: TreeStoreNode) {
  let cursor: TreeStoreNode | null = node
  while (cursor && cursor.level > 0) {
    if (!cursor.visible) return false
    cursor = cursor.parent
  }
  return true
}

/**
 * 从目标行往上找第一个真正在纵向滚动的容器。
 * ≥769 时就是 .sidebar-tree（global.scss 的 .dd-fixed-page 把整页钉在视口高度里）；
 * ≤768 时 .dd-fixed-page 不再定高，侧栏随内容撑高、.sidebar-tree 根本不滚，滚的是外层的 .layout-main（浏览器实测）。
 * 只取第一个：树里已经能滚到位时不再去动外层。
 */
function findScrollContainer(from: HTMLElement) {
  for (let el = from.parentElement; el && el !== document.body; el = el.parentElement) {
    const { overflowY } = window.getComputedStyle(el)
    if ((overflowY === 'auto' || overflowY === 'scroll') && el.scrollHeight > el.clientHeight) return el
  }
  return null
}

/**
 * 纵向滚动，让目标行完整可见（nearest 语义：已经完整可见就不动，上沿出界对齐顶部，下沿出界对齐底部）。
 * 刻意不用 scrollIntoView：它会连带滚动所有能滚的祖先（整页、布局容器），
 * 缩进很深时还可能横向滚动带 overflow 的容器。
 */
function scrollRowIntoView(path: string, smooth: boolean) {
  const container = treeContainerRef.value
  // offsetParent 为 null：侧栏被 v-show 藏起，或页面被 keep-alive 缓存、DOM 已脱离文档。量出来全是 0，滚了也白滚
  if (!container || container.offsetParent === null) return
  const node = treeRef.value?.getNode(path) as TreeStoreNode | null | undefined
  // 搜索过滤把它或它的某个祖先藏掉了：只高亮不滚动，也不替用户清搜索词
  if (!node || !isShownUnderFilter(node)) return
  const row = container.querySelector<HTMLElement>(`[data-key="${CSS.escape(path)}"] > .el-tree-node__content`)
  if (!row) return
  const rowRect = row.getBoundingClientRect()
  // 祖先还收着（比如动画途中用户又点收了），行是 display:none
  if (rowRect.height === 0) return
  const scroller = findScrollContainer(row)
  if (!scroller) return
  const scrollerTop = scroller.getBoundingClientRect().top + scroller.clientTop
  // 可见区取容器与窗口的交集：容器自己也可能被窗口裁掉一截，不能假定外壳恰好等于视口。
  // （v3.3.3 之前演示站横幅没把高度让出来，.layout-main 底边落在窗口外 34px；banner.ts 已修，这里的交集照留）
  const viewTop = Math.max(scrollerTop, 0)
  const viewBottom = Math.min(scrollerTop + scroller.clientHeight, window.innerHeight)
  let delta = 0
  if (rowRect.top < viewTop) {
    delta = rowRect.top - viewTop
  } else if (rowRect.bottom > viewBottom) {
    delta = rowRect.bottom - viewBottom
  }
  if (delta === 0) return
  scroller.scrollTo({
    top: scroller.scrollTop + delta,
    // global.scss 的减少动效只压得住 CSS 的 scroll-behavior，管不到 JS 显式传的 smooth，要在这里判断
    behavior: smooth && !shouldReduceMotion() ? 'smooth' : 'auto'
  })
}

async function scrollAfterTreeSettled(seq: number, path: string, smooth: boolean) {
  await waitTreeFlushed()
  const container = treeContainerRef.value
  if (!container || isRevealStale(seq, path)) return
  await waitCollapseTransitions(container)
  // 等待期间侧栏又被藏起（手机上打开了编辑器 / 桌面端点了收起）：交给下面「重新可见」那条侦听
  if (isRevealStale(seq, path) || sidebarHidden.value) return
  scrollRowIntoView(path, smooth)
}

/**
 * 在树里定位 path：展开祖先、高亮、滚到可见。
 * 不动搜索框：目标被过滤藏掉时只高亮不滚动（见 scrollRowIntoView），不替用户清搜索词。
 */
async function revealInTree(path: string) {
  const seq = ++revealSeq
  // 找不到节点（文件已删 / 不在脚本目录 / 树还没加载完）：清掉高亮就走，等下次 fileTree 变化再试
  if (!path || !treeRef.value?.getNode(path)) {
    setHighlight('', false)
    return
  }
  setHighlight(path, true)
  // 侧栏看不见时滚了也白滚，留给「重新可见」那条侦听补做
  if (sidebarHidden.value) return
  await scrollAfterTreeSettled(seq, path, true)
}

/**
 * 包一层 node-click（EP 在 emit 之前就已经把高亮挪到了点中的节点上）：
 * - 点目录：只是展开 / 收起，高亮拉回当前文件；传 false，不然刚被点收起的目录会被重新展开；
 * - 点文件：等打开流程走完再拉回 —— 「未保存修改」选了取消、或加载失败时编辑器还是原来的文件，
 *   高亮不能停在刚点的那个上。打开成功时 currentFile 已经是它，这一步等于原地不动。
 */
async function handleTreeNodeClick(data: TreeNode) {
  if (!data.isLeaf) {
    setHighlight(props.currentFile, false)
    return
  }
  try {
    await props.onNodeClick(data)
  } finally {
    setHighlight(props.currentFile, false)
  }
}

// 打开文件成功（含同一文件再次打开），或加载失败回滚到原文件、且加载期间树重建过（原文件可能被收起藏住）
watch(() => props.revealTicket, () => {
  void revealInTree(props.currentFile)
})

// 树数据刷新：进入页面、keep-alive 重新激活、刷新按钮、新建 / 重命名 / 移动 / 删除之后，以及切换排序。
// flush: 'post'：等 EP 的 setData 跑完、nodesMap 换成新节点再定位。
// 页面被 keep-alive 缓存期间，路由侦听照样会打开文件，但那时 DOM 不在文档里、滚不动；
// 重新激活时 onActivated → loadTree 会走到这里把滚动补上。
watch(
  sortedFileTree,
  async () => {
    // 🔴 必须先让这一轮 flush 整个跑完，再去展开节点（浏览器实测过，去掉这一行会复现）：
    // setData 换了新节点，按 key 复用的 tree-node 组件看到 node.expanded 由 true 变 false，
    // 登记了一个 nextTick(expanded=false)，它挂在本轮 flush 的 promise 上；
    // 若这里同步展开，组件那边登记的 nextTick(expanded=true) 挂在已经就绪的 resolvedPromise 上，反而先跑，
    // 结果 Node.expanded=true、组件 expanded=false：目录看着是收起的，而且点它也展不开（EP 按组件的值判断，只会重复 expand）。
    // 重命名 / 拖拽当前文件后它所在的目录最容易中招。await 本轮的 nextTick 之后再展开，先后顺序就对了。
    await nextTick()
    // setData 把节点全部重置成 visible，搜索框里的词却还在：补套一次过滤，免得「框里有词、树没过滤」
    if (searchKeyword.value) treeRef.value?.filter(searchKeyword.value)
    void revealInTree(props.currentFile)
  },
  { flush: 'post' }
)

// 当前文件变了（删除后清空、重命名 / 移动改了路径、加载失败回滚）：高亮先跟上，不展开、不滚动。
// 展开和滚动只在 revealTicket 变化或「树刷新」时做（上面两条），免得按一个打不开的路径去展开祖先。
watch(() => props.currentFile, (file) => {
  setHighlight(file, false)
})

// 侧栏从看不见变为可见（手机上从编辑器返回列表 / 桌面端展开目录树）：只补一次滚动，不再动高亮与展开。
// 瞬时滚动：卡片刚播完入场 / 展开动画，再接一段平滑滚动显得拖沓。
watch(sidebarHidden, async (hidden, wasHidden) => {
  const path = props.currentFile
  if (hidden || !wasHidden || !path) return
  const seq = ++revealSeq
  await nextTick()
  await waitSidebarAnimations()
  if (isRevealStale(seq, path)) return
  await scrollAfterTreeSettled(seq, path, false)
})
</script>

<template>
  <aside ref="sidebarRef" class="scripts-sidebar" :class="{ mobile: isMobile }" v-show="!isMobile || !mobileShowEditor">
    <header class="sidebar-top">
      <div class="sidebar-search">
        <el-input
          v-model="searchKeyword"
          placeholder="搜索文件或目录"
          clearable
          :prefix-icon="Search"
          class="sidebar-search-input"
        />
        <!-- 排序放在搜索框同一行：下面的工具条已经没有余量（见 issue #136 的注释） -->
        <el-dropdown trigger="click" @command="onSortCommand">
          <button class="icon-btn" aria-label="排序" title="排序">
            <el-icon :size="15"><Sort /></el-icon>
          </button>
          <template #dropdown>
            <el-dropdown-menu>
              <el-dropdown-item
                v-for="option in sortOptions"
                :key="option.value"
                :command="option.value"
                :style="sortMode === option.value ? { color: 'var(--el-color-primary)', fontWeight: 600 } : undefined"
              >
                {{ option.label }}
              </el-dropdown-item>
            </el-dropdown-menu>
          </template>
        </el-dropdown>
      </div>

      <div class="sidebar-toolbar">
        <div class="sidebar-toolbar-label">
          <span class="label-main">脚本文件</span>
        </div>
        <div class="sidebar-toolbar-actions">
          <!-- 原来是「新建 ▾」：主体点了只是展开菜单，最常用的「新建文件」还要再点一次，
               chevron 也是手写的。改成真正的 Split Button——主体直接新建文件，
               另外两项收进 caret 菜单。 -->
          <DdSplitButton
            class="new-split-button"
            label="新建文件"
            :icon="DocumentAdd"
            type="primary"
            size="small"
            :items="newActionItems"
            @click="onOpenCreateFile"
            @command="onNewAction"
          />

          <!-- issue #136 曾在这里加过「定位当前文件」图标按钮，实测放不下：桌面端 300px 侧栏的工具条
               原本只剩约 10px 余量，再加 30px 按钮 + 6px 间距，「脚本文件」被挤成两行、新建按钮的 caret 掉到第二行。
               按设计「放不下就不加」撤掉；打开文件、树刷新、侧栏重新可见时都会自动定位。 -->
          <el-tooltip content="刷新" placement="bottom">
            <button class="icon-btn" aria-label="刷新" @click="onRefresh">
              <el-icon :size="15"><Refresh /></el-icon>
            </button>
          </el-tooltip>

          <!-- 收起目录树。只在桌面宽屏渲染：≤1024（isMobile 这个 prop 传的是 isCompactLayout）
               已经有「文件列表 ↔ 编辑器」互斥模式，再给一个收起按钮只会让两套逻辑打架。
               v-if 挂在 tooltip 上而不是 button 上，否则隐藏时 tooltip 会剩一个空触发器。 -->
          <el-tooltip v-if="!isMobile" content="收起文件树" placement="bottom">
            <button class="icon-btn" aria-label="收起文件树" @click="onToggleCollapse">
              <el-icon :size="15"><Fold /></el-icon>
            </button>
          </el-tooltip>
        </div>
      </div>
    </header>

    <div ref="treeContainerRef" class="sidebar-tree" v-loading="treeLoading">
      <el-tree
        ref="treeRef"
        :data="sortedFileTree"
        node-key="key"
        :props="{ children: 'children', label: 'title' }"
        :highlight-current="true"
        :expand-on-click-node="true"
        :filter-node-method="filterNode"
        draggable
        :allow-drag="allowDrag"
        :allow-drop="allowDrop"
        empty-text="暂无脚本文件"
        @node-drop="onNodeDrop"
        @node-click="handleTreeNodeClick"
      >
        <template #default="{ data }">
          <ScriptTreeNode :data="data" :on-open-rename="onOpenRename" :on-delete="onDelete" :on-move-to-root="onMoveToRoot" />
        </template>
      </el-tree>
    </div>

    <footer class="sidebar-footer">
      <button class="runner-card" @click="onOpenCodeRunner" aria-label="打开代码运行器">
        <div class="runner-card-icon">
          <el-icon :size="16"><VideoPlay /></el-icon>
        </div>
        <div class="runner-card-body">
          <span class="runner-card-title">代码运行器</span>
          <span class="runner-card-desc">粘贴片段即刻执行</span>
        </div>
      </button>
    </footer>
  </aside>
</template>

<style scoped lang="scss">
.scripts-sidebar {
  /* 宽度的单一真源在 index.vue 的 .scripts-workspace（--scripts-sidebar-width），折叠时那边置 0px。
     这里必须消费同一个变量：min-width 是 flex-basis 的下限，还写死 300px 的话
     index.vue 那边把 flex-basis 收到 0 也没用，卡片会被这一行顶住收不起来。
     min-width 同样要过渡 —— 若它瞬间跳回 300px，展开动画会被立刻夹到终点，看着像没有动画。 */
  width: var(--scripts-sidebar-width, 300px);
  min-width: var(--scripts-sidebar-width, 300px);
  transition:
    width var(--dd-motion-normal) var(--dd-ease-standard),
    min-width var(--dd-motion-normal) var(--dd-ease-standard);
  height: 100%;
  min-height: 0;
  display: flex;
  flex-direction: column;
  gap: 0;
  padding: 0;
  /* 卡片表面/边框由 index.vue 的 :deep(.scripts-sidebar) 统一负责，
     这里不再画 border-right 分隔线（两卡独立后无需分隔） */
  background: var(--el-bg-color);
  box-sizing: border-box;
  font-family: var(--dd-font-ui);
  overflow: hidden;
}

.sidebar-top {
  display: flex;
  border-bottom: 1px solid color-mix(in srgb, var(--el-border-color-lighter) 80%, transparent);
  padding-bottom: 10px;
  position: relative;
  flex-direction: column;
  gap: 10px;
  flex-shrink: 0;
  padding: 16px 14px 0;
}

.sidebar-search {
  display: flex;
  align-items: center;
  gap: 6px;
}

.sidebar-search-input {
  flex: 1;
  min-width: 0;

  :deep(.el-input__wrapper) {
    // issue #144 / v3.3.2：搜索框跟同排按钮同吃「按钮」角色令牌 —— 手机上 rounded 面板放大到 16px，
    // 配 EP default 输入框的 32px 高正好是胶囊形，与其它五页移动端工具栏搜索框（global.scss 的
    // .dd-mobile-toolbar 一节）口径一致；桌面与 square 面板下该令牌恒等于 control 档，零变化。
    // 这里刻意不改挂 .dd-mobile-toolbar 去蹭那条共享规则：该类自带 display:flex / gap 与对 > .el-input
    // 的 flex:1，会和 .sidebar-top 的 flex-direction: column 打架（侧栏是搜索框、工具条各占一行的结构）
    border-radius: var(--dd-radius-button);
    padding: 4px 12px;
    box-shadow: 0 0 0 1px var(--el-border-color-lighter) inset;
    transition: box-shadow 0.2s, background 0.2s;
    background: var(--el-fill-color-light);
  }

  :deep(.el-input__wrapper.is-focus) {
    box-shadow: 0 0 0 2px color-mix(in srgb, var(--el-color-primary) 45%, transparent) inset;
    background: var(--el-bg-color);
  }

  :deep(.el-input__inner) {
    font-size: 13px;
    font-family: var(--dd-font-ui);
  }
}

.sidebar-toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  padding: 4px 0 0;
}

.sidebar-toolbar-label {
  display: flex;
  align-items: baseline;
  gap: 6px;

  .label-main {
    font-size: 12px;
    font-weight: 600;
    letter-spacing: 0.5px;
    text-transform: uppercase;
    color: var(--el-text-color-secondary);
  }
}

// issue #144 / v3.3.2：这里原来是一块灰底圆角槽（padding + border-radius + color-mix 底色），
// 实测槽底色把刷新按钮衬得像被额外「框」了一层，已收成纯排布容器（槽没底色后圆角只是死声明，一并删）。
// 暗色那份底色覆盖在文件末尾的 html.dark 块里，删这里就必须一起删，否则是「浅色干净、暗色还留着」的半主题 bug。
// 编辑器侧的同构槽 ScriptsEditorPane 的 .hero-actions 同口径处理
.sidebar-toolbar-actions {
  display: flex;
  align-items: center;
  gap: 6px;
}

/* class 落到 DdSplitButton 的根节点（EP 的 div.el-dropdown）上。
   EP small 档按钮高 24px，会比右边 30px 的 .icon-btn 矮一截，工具条看着参差不齐，
   所以这里只把两半按钮的高度和字号拉回原先「新建」按钮的 30px / 12.5px。
   圆角与 caret 中缝的可见度由 global.scss 的 Split Button 一节统一负责，
   chevron 也由组件自带，不需要再手写。 */
.new-split-button {
  :deep(.el-button) {
    // 这里刻意【不写】border-radius：split-button 内部是 el-button-group，
    // 只有最外两个角该跟着 --dd-radius-control 变圆、中缝两侧必须保持直角，
    // 这套 first-child/last-child 规则由 EP 自己按 --el-border-radius-base 算好了。
    // 在这里补一句 border-radius 会把两半四个角一起改掉，中缝直接糊成一整条。
    height: 30px;
    font-size: 12.5px;
    font-weight: 500;
  }
}

.icon-btn {
  width: 30px;
  height: 30px;
  padding: 0;
  border: 1px solid var(--el-border-color-lighter);
  background: transparent;
  // issue #144 / v3.3.2：30×30 图标按钮改吃「按钮」角色令牌 —— 手机上 rounded 面板放大到 16px，
  // 与同排早就是 16px 的 DdSplitButton 外角拉齐（EP 的 button-group 只归零首尾项的内侧两角，
  // 外侧四角一直走 .el-button 的 shorthand，移动端那条 `html .el-button` 压得过）；
  // 桌面与 square 面板下该令牌恒等于 control 档，零变化。
  // 孪生体 ScriptsEditorPane 的 .sidebar-expand-btn 必须同档，改这里就要一起改
  border-radius: var(--dd-radius-button);
  color: var(--el-text-color-secondary);
  cursor: pointer;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  transition: color 0.15s, background 0.15s, border-color 0.15s;

  &:hover {
    color: var(--el-color-primary);
    border-color: color-mix(in srgb, var(--el-color-primary) 40%, var(--el-border-color-lighter));
    background: color-mix(in srgb, var(--el-color-primary) 6%, transparent);
  }

  &:focus-visible {
    outline: 2px solid color-mix(in srgb, var(--el-color-primary) 50%, transparent);
    outline-offset: 1px;
  }
}

.sidebar-tree {
  flex: 1 1 auto;
  min-width: 0;
  min-height: 0;
  overflow: auto;
  padding: 8px 10px 10px;

  :deep(.el-tree) {
    background: transparent;
    color: inherit;
    min-width: 0;
  }

  :deep(.el-tree-node),
  :deep(.el-tree-node__children) {
    min-width: 0;
  }

  :deep(.el-tree-node__content) {
    height: 34px;
    min-width: 0;
    padding-left: 4px;
    // 目录树的行是可点小块（hover / 选中都会上底色）→ control 档
    border-radius: var(--dd-radius-control);
    transition: background 0.15s;
    font-size: 13px;
    overflow: hidden;
  }

  :deep(.el-tree-node__content:hover) {
    background: var(--el-fill-color-light);
  }

  :deep(.el-tree-node.is-current > .el-tree-node__content) {
    background: color-mix(in srgb, var(--el-color-primary) 10%, transparent);
    position: relative;

    &::before {
      content: '';
      position: absolute;
      left: 0;
      top: 6px;
      bottom: 6px;
      width: 2.5px;
      // 装饰性细指示条 → pill 档，与 api-docs 的 .api-card 竖条、SponsorWall 的 .title-dot
      // 归为同一类，全站这一类统一走 pill。
      //
      // ⚠️ 原注释写的「圆角化后会缩成一个小圆点、彻底看不出是条」是错的：
      //    CSS 的圆角等比收缩只会把半径夹到 min(边长)/2，2.5px 宽的条最多得到 1.25px 半径，
      //    结果是一条【圆头细条】而不是圆点——条的长度（22px）一点没变。
      //    细条天然就是胶囊形，pill 与 control 在这个宽度下渲染结果相同，写 pill 只是让归类显式。
      border-radius: var(--dd-radius-pill);
      background: var(--el-color-primary);
    }
  }

  :deep(.el-tree-node.is-drop-inner > .el-tree-node__content) {
    background: color-mix(in srgb, var(--el-color-primary) 12%, transparent);
    outline: 2px dashed var(--el-color-primary);
    outline-offset: -2px;
  }

  :deep(.el-tree__drop-indicator) {
    height: 2px;
    background: var(--el-color-primary);
    // 🔴 保持 0，不吃令牌 —— 它与上面那条选中标识条【看着像同类，实则不是】：
    // 那条是「当前在哪一项」的装饰性标识（走 pill），这条是「松手会插到哪两行之间」的
    // 落点指示线，语义上等同于文本光标。方头两端才能把落点的横向范围指得干脆，
    // 端头一旦收圆，线的两头会先淡出再消失，看起来像没对齐任何一行。
    //
    // ⚠️ 原注释说「吃圆角会把两端磨成尖角」是错的（圆角只会变圆不会变尖）。
    //    实际吃 control 的话，2px 高会把半径夹到 1px——差别很小，但方向是错的，
    //    所以这里是按语义显式保持 0，不是因为「反正看不出来」。
    border-radius: 0;
  }

  :deep(.el-tree-node.is-dragging > .el-tree-node__content) {
    opacity: 0.4;
  }

  :deep(.el-tree-node__expand-icon) {
    color: var(--el-text-color-placeholder);
    font-size: 12px;
  }
}

.sidebar-footer {
  flex-shrink: 0;
  padding: 10px 14px 14px;
  border-top: 1px solid var(--el-border-color-lighter);
}

.runner-card {
  width: 100%;
  position: relative;
  overflow: hidden;
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 10px 12px;
  border: 1px solid var(--el-border-color-lighter);
  // 运行器入口是侧栏底部独立成块的卡片（带边框、有自己的留白）→ surface 档
  border-radius: var(--dd-radius-surface);
  background: var(--el-fill-color-light);
  color: inherit;
  text-align: left;
  cursor: pointer;
  font-family: inherit;
  transition: background 0.15s, border-color 0.15s;

  &:hover {
    background: color-mix(in srgb, var(--scripts-accent, #22c55e) 8%, var(--el-fill-color-light));
    border-color: color-mix(in srgb, var(--scripts-accent, #22c55e) 40%, var(--el-border-color-lighter));
  }

  &:focus-visible {
    outline: 2px solid color-mix(in srgb, var(--scripts-accent, #22c55e) 70%, transparent);
    outline-offset: 2px;
  }
}

.runner-card-icon {
  width: 30px;
  height: 30px;
  // 30×30 的图标色底属控件类表面 → control 档
  border-radius: var(--dd-radius-control);
  display: inline-flex;
  align-items: center;
  justify-content: center;
  color: var(--scripts-accent, #22c55e);
  background: color-mix(in srgb, var(--scripts-accent, #22c55e) 12%, transparent);
  flex-shrink: 0;
}

.runner-card-body {
  display: flex;
  flex-direction: column;
  gap: 2px;
  min-width: 0;
}

.runner-card-title {
  font-size: 13px;
  font-weight: 600;
  color: var(--el-text-color-primary);
  line-height: 1.2;
}

.runner-card-desc {
  font-size: 11.5px;
  color: var(--el-text-color-secondary);
  line-height: 1.2;
  letter-spacing: 0.1px;
}

.scripts-sidebar.mobile {
  width: 100%;
  min-width: 0;
  min-height: 0;

  .sidebar-toolbar {
    padding: 0;
  }

  :deep(.tree-node) {
    .tree-node-actions,
    .tree-node-ext {
      opacity: 1;
    }
  }
}
</style>


<style lang="scss">
html.dark {
  /* 暗色卡面/边框由 --el-bg-color 与 index.vue 的卡片样式自动适配，
     此处仅保留内部分区线与执行器卡片底色的暗色覆盖。
     issue #144 / v3.3.2：工具条按钮组 .sidebar-toolbar-actions 的槽底色已在浅色侧删掉，
     这里的暗色覆盖同步移除，别再往回加 */
  .scripts-sidebar .sidebar-top,
  .scripts-sidebar .sidebar-footer {
    border-color: rgba(255,255,255,0.08);
  }

  .scripts-sidebar .runner-card {
    background: color-mix(in srgb, var(--el-bg-color-overlay) 92%, black);
  }
}
</style>
