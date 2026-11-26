import { computed, ref, toRaw, watch } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { scriptApi } from '@/api/script'
import { useResponsive } from '@/composables/useResponsive'
import type { TreeNode } from './types'

type ScriptBrowserState = {
  selectedFile: string
  fileContent: string
  originalContent: string
  isBinary: boolean
  isEditing: boolean
  mobileShowEditor: boolean
  editorAutoFocusTicket: number
}

const SCRIPTS_SIDEBAR_COLLAPSED_KEY = 'dd:scripts:sidebar_collapsed'

function readStoredSidebarCollapsed() {
  if (typeof window === 'undefined') {
    return false
  }
  try {
    // 只认 '1'，其余（没写过、脏值、读不到）一律当作展开 —— 默认值 = 改造前的现状
    return window.localStorage.getItem(SCRIPTS_SIDEBAR_COLLAPSED_KEY) === '1'
  } catch {
    // 隐私模式 / 禁用存储时 getItem 会抛错，不能让它把 setup 整块炸掉
    return false
  }
}

function persistSidebarCollapsed(value: boolean) {
  if (typeof window === 'undefined') {
    return
  }
  try {
    window.localStorage.setItem(SCRIPTS_SIDEBAR_COLLAPSED_KEY, value ? '1' : '0')
  } catch {
    // 写失败只是这次记不住，不影响本次折叠效果，静默忽略
  }
}

/**
 * 按后端 normalizeScriptRelativePath（server/handler/script.go）的口径规范化脚本路径：
 * `\` 转 `/`、每段 trim、去掉空段和 `.` 段。
 *
 * 为什么要做：任务页 `?file=` 传来的是任务命令里的原始 token，可能是 `./jd/x.py`、`jd\x.py`。
 * 后端照样能打开，但 selectedFile 和目录树的 key（`jd/x.py`）对不上，树定位会静默失败，
 * 删除 / 重命名里的「是不是当前文件」比较也会跟着判错。
 *
 * 只做「后端也会做」的那几步：以 `/` 开头的绝对路径、`..` 段后端会直接拒绝，
 * 这里原样交过去让它报错，不在前端把它「修」成另一个相对路径。
 */
function normalizeScriptPath(raw: string) {
  const slashed = raw.trim().replace(/\\/g, '/')
  if (!slashed || slashed.startsWith('/')) {
    return slashed
  }
  return slashed
    .split('/')
    .map(segment => segment.trim())
    .filter(segment => segment !== '' && segment !== '.')
    .join('/')
}

export function useScriptWorkspaceBrowser() {
  const { isMobile, isTablet } = useResponsive()
  const isCompactLayout = computed(() => isTablet.value)
  const mobileShowEditor = ref(false)

  // 用户的原始选择：不受布局影响，窗口缩到 ≤1024 再拉回宽屏时还能回到折叠前的状态
  const sidebarCollapsed = ref(readStoredSidebarCollapsed())
  // 真正生效的折叠态。≤1024 一律当成展开：
  // 那个断点下已经有 mobileShowEditor 的「文件列表 ↔ 编辑器」互斥模式（靠 v-show + display:none），
  // 两套逻辑同时开会打架 —— 折叠把宽度收到 0，compact 又把它拉回全宽，结果是谁都不对。
  const isSidebarCollapsed = computed(() => !isCompactLayout.value && sidebarCollapsed.value)

  function toggleSidebarCollapsed() {
    sidebarCollapsed.value = !sidebarCollapsed.value
    persistSidebarCollapsed(sidebarCollapsed.value)
  }

  const fileTree = ref<TreeNode[]>([])
  const selectedFile = ref('')
  const fileContent = ref('')
  const originalContent = ref('')
  const isBinary = ref(false)
  const loading = ref(false)
  const treeLoading = ref(false)
  const isEditing = ref(false)
  const editorAutoFocusTicket = ref(0)
  // 目录树定位的触发信号：openFile 每成功一次 +1（加载失败回滚时，仅当加载期间树重建过才 +1），侧栏侦听它去展开祖先、高亮、滚动。
  // 不直接侦听 selectedFile：同一文件从任务页再次进入时它不变，却同样要重新定位；
  // 而 openFile 是「先改 selectedFile、加载失败再回滚」，侦听它会先按失败的路径展开一遍祖先。
  const treeRevealTicket = ref(0)

  const editorLanguage = computed(() => {
    if (!selectedFile.value) return 'javascript'
    const ext = selectedFile.value.split('.').pop()?.toLowerCase()
    const langMap: Record<string, string> = {
      js: 'javascript',
      mjs: 'javascript',
      ts: 'typescript',
      py: 'python',
      sh: 'shell',
      go: 'go',
      json: 'json',
      yaml: 'yaml',
      yml: 'yaml',
      md: 'markdown',
      html: 'html',
      css: 'css',
      xml: 'xml'
    }
    return langMap[ext || ''] || 'plaintext'
  })

  const hasChanges = computed(() => fileContent.value !== originalContent.value)

  function extractScriptErrorMessage(err: any, fallback: string) {
    const message = String(err?.response?.data?.error || err?.message || '').trim()
    if (!message) {
      return fallback
    }

    if (message.includes('当前路径是目录')) {
      return '当前选中的是目录，不是可编辑脚本文件'
    }
    if (message.includes('文件不存在')) {
      return '脚本不存在，可能已被删除、重命名或移动'
    }
    if (message.includes('不允许路径穿越') || message.includes('检测到路径穿越') || message.includes('路径包含非法字符')) {
      return '脚本路径无效，请刷新文件树后重试'
    }

    return message
  }

  // 与后端 ShouldHideScriptTreeEntryName 保持一致的纵深防御名单。
  // .git 等版本控制目录里存着订阅注入的访问令牌，后端已经堵死，这里只是二次过滤。
  const hiddenScriptTreeSegments = new Set([
    'node_modules',
    '__pycache__',
    '.git',
    '.svn',
    '.hg',
    '.bzr'
  ])

  function shouldSkipFolder(path: string) {
    return path
      .split('/')
      .map(segment => segment.trim().toLowerCase())
      .some(segment => hiddenScriptTreeSegments.has(segment))
  }

  function normalizeTreeNodes(nodes: TreeNode[]): TreeNode[] {
    return nodes
      .filter(node => !shouldSkipFolder(node.key))
      .map((node) => {
        if (node.isLeaf) {
          return node
        }
        return {
          ...node,
          children: normalizeTreeNodes(node.children || [])
        }
      })
  }

  // 懒加载目录展开后，子项是绕开响应式写回 fileTree 的（见 loadLazyDirectory），
  // 依赖整棵树的派生数据（目录选择）靠这个版本号跟着重算
  const lazyTreeVersion = ref(0)

  const allFolders = computed(() => {
    void lazyTreeVersion.value
    const folders: string[] = ['']
    const collectFolders = (nodes: TreeNode[], prefix = '') => {
      for (const node of nodes) {
        if (!node.isLeaf) {
          const path = prefix ? `${prefix}/${node.title}` : node.title
          folders.push(path)
          if (node.children) {
            collectFolders(node.children, path)
          }
        }
      }
    }
    collectFolders(fileTree.value)
    return folders
  })

  watch(isCompactLayout, (compact) => {
    if (!compact) {
      mobileShowEditor.value = false
    }
  })

  function snapshotState(): ScriptBrowserState {
    return {
      selectedFile: selectedFile.value,
      fileContent: fileContent.value,
      originalContent: originalContent.value,
      isBinary: isBinary.value,
      isEditing: isEditing.value,
      mobileShowEditor: mobileShowEditor.value,
      editorAutoFocusTicket: editorAutoFocusTicket.value
    }
  }

  function restoreState(state: ScriptBrowserState) {
    selectedFile.value = state.selectedFile
    fileContent.value = state.fileContent
    originalContent.value = state.originalContent
    isBinary.value = state.isBinary
    isEditing.value = state.isEditing
    mobileShowEditor.value = state.mobileShowEditor
    editorAutoFocusTicket.value = state.editorAutoFocusTicket
  }

  async function loadTree() {
    treeLoading.value = true
    try {
      const res = await scriptApi.tree()
      fileTree.value = normalizeTreeNodes(res.data || [])
    } catch (err: any) {
      ElMessage.error(err?.response?.data?.error || err?.message || '加载文件树失败')
    } finally {
      treeLoading.value = false
    }
  }

  /**
   * 展开懒加载目录（#183）：GET /scripts/tree?lazy=1&path= 取这个目录的子项，返回给侧栏挂到 el-tree 上。
   *
   * 子项写回 fileTree 时刻意走 toRaw、不触发响应式：fileTree 一变，侧栏的排序副本就整棵重建，
   * el-tree 跟着 setData，用户已经展开的目录会全部收起（见 ScriptsSidebar 里 sortedFileTree 那条侦听的注释）。
   * 写回只是为了之后切换排序 / keep-alive 重建时数据还在；当下的显示由侧栏用 updateKeyChildren 直接更新。
   * 取失败返回 null，lazy 标记保留，下次展开再试。
   */
  async function loadLazyDirectory(key: string): Promise<TreeNode[] | null> {
    try {
      const res = await scriptApi.tree(key)
      const children = normalizeTreeNodes(res.data || [])
      const findNode = (nodes: TreeNode[]): TreeNode | null => {
        for (const node of nodes) {
          if (node.key === key) return node
          if (!node.isLeaf && node.children && key.startsWith(`${node.key}/`)) {
            const found = findNode(node.children)
            if (found) return found
          }
        }
        return null
      }
      const source = findNode(toRaw(fileTree.value))
      if (source) {
        source.children = children
        source.lazy = false
      }
      lazyTreeVersion.value += 1
      return children
    } catch (err: any) {
      ElMessage.error(extractScriptErrorMessage(err, '加载目录失败'))
      return null
    }
  }

  async function loadFileContent(path: string, options: { silent?: boolean } = {}) {
    loading.value = true
    try {
      const res = await scriptApi.getContent(path)
      isBinary.value = res.data.is_binary ?? res.data.binary ?? false
      fileContent.value = res.data.content
      originalContent.value = res.data.content
      return true
    } catch (err: any) {
      if (!options.silent) {
        ElMessage.error(extractScriptErrorMessage(err, '加载文件内容失败'))
      }
      return false
    } finally {
      loading.value = false
    }
  }

  async function confirmOpenFile(path: string, skipUnsavedCheck = false) {
    if (skipUnsavedCheck || !hasChanges.value || path === selectedFile.value) {
      return true
    }

    try {
      await ElMessageBox.confirm('当前文件有未保存的修改，是否放弃？', '提示', {
        confirmButtonText: '放弃',
        cancelButtonText: '取消',
        type: 'warning'
      })
      return true
    } catch {
      return false
    }
  }

  async function openFile(path: string, options: { skipUnsavedCheck?: boolean } = {}) {
    const normalizedPath = normalizeScriptPath(path)
    if (!normalizedPath) {
      return false
    }

    if (normalizedPath === selectedFile.value) {
      mobileShowEditor.value = true
      // 同一文件再次进入（比如从任务页又点了一次脚本名）也算打开成功，照样重新定位
      treeRevealTicket.value += 1
      return true
    }

    const canProceed = await confirmOpenFile(normalizedPath, options.skipUnsavedCheck ?? false)
    if (!canProceed) {
      return false
    }

    const previousState = snapshotState()
    // 记下加载前的树（loadTree 每次都整体换新数组，引用变了 = 加载期间树重建过），失败回滚时用来判断要不要补定位
    const treeBeforeLoad = fileTree.value
    selectedFile.value = normalizedPath
    isEditing.value = false
    const loaded = await loadFileContent(normalizedPath)
    if (!loaded) {
      restoreState(previousState)
      // 只有加载期间树重建过，才给「原来那个文件」补一次定位（这时 selectedFile 已经回滚，不会按打不开的路径展开祖先）：
      // 加载期间 selectedFile 暂时指着打不开的路径，恰好这时树刷新了（缓存态从任务页进来会 loadTree），
      // 刷新后的定位找不到它、只清了高亮，新树又是全收起的，回滚后原文件虽然高亮了，却藏在收起的目录里看不见。
      // 树没重建时不能补：定位会展开祖先并滚过去，用户刚收起的目录会被重新展开，正在浏览的位置也被拽走。
      // 这时侧栏的 currentFile 侦听和节点点击包装已经用 expandParents=false 把高亮拉回原文件，不展开、不滚动。
      if (previousState.selectedFile && fileTree.value !== treeBeforeLoad) {
        treeRevealTicket.value += 1
      }
      return false
    }

    mobileShowEditor.value = true
    treeRevealTicket.value += 1
    return true
  }

  function triggerEditorAutoFocus() {
    editorAutoFocusTicket.value += 1
  }

  async function handleNodeClick(data: TreeNode) {
    // 懒加载目录的占位节点不是文件
    if (!data.isLeaf || data.placeholder) return
    await openFile(data.key)
  }

  function allowDrag(draggingNode: any) {
    return draggingNode.data.isLeaf && !draggingNode.data.placeholder
  }

  function allowDrop(draggingNode: any, dropNode: any, type: string) {
    if (type === 'inner') {
      return !dropNode.data.isLeaf
    }
    if (type === 'before' || type === 'after') {
      return dropNode.level === 1
    }
    return false
  }

  async function handleNodeDrop(draggingNode: any, dropNode: any, dropType: string) {
    const sourcePath = draggingNode.data.key
    const targetDir = dropType === 'inner' ? dropNode.data.key : ''
    try {
      await scriptApi.move(sourcePath, targetDir)
      ElMessage.success('移动成功')
      if (selectedFile.value === sourcePath) {
        const fileName = sourcePath.split('/').pop() || sourcePath
        selectedFile.value = targetDir ? `${targetDir}/${fileName}` : fileName
      }
      await loadTree()
    } catch {
      ElMessage.error('移动失败')
      await loadTree()
    }
  }

  function handleMobileBack() {
    mobileShowEditor.value = false
  }

  return {
    isMobile,
    isCompactLayout,
    mobileShowEditor,
    isSidebarCollapsed,
    toggleSidebarCollapsed,
    fileTree,
    selectedFile,
    fileContent,
    originalContent,
    isBinary,
    loading,
    treeLoading,
    isEditing,
    editorAutoFocusTicket,
    treeRevealTicket,
    editorLanguage,
    hasChanges,
    allFolders,
    extractScriptErrorMessage,
    loadTree,
    loadLazyDirectory,
    loadFileContent,
    openFile,
    triggerEditorAutoFocus,
    handleNodeClick,
    allowDrag,
    allowDrop,
    handleNodeDrop,
    handleMobileBack
  }
}
