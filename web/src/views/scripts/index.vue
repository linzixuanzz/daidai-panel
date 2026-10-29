<script setup lang="ts">
import { ElMessageBox } from 'element-plus'
import { watch } from 'vue'
import { useRoute } from 'vue-router'
import ScriptExecutionDialogs from './components/ScriptExecutionDialogs.vue'
import ScriptManageDialogs from './components/ScriptManageDialogs.vue'
import ScriptsEditorPane from './components/ScriptsEditorPane.vue'
import ScriptsSidebar from './components/ScriptsSidebar.vue'
import { useScriptExecution } from './useScriptExecution'
import { useScriptWorkspace } from './useScriptWorkspace'

const workspace = useScriptWorkspace()
const execution = useScriptExecution({
  selectedFile: workspace.selectedFile,
  fileContent: workspace.fileContent
})

const {
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
  saving,
  treeLoading,
  isEditing,
  editorAutoFocusTicket,
  treeRevealTicket,
  showCreateFileDialog,
  showCreateDirDialog,
  showRenameDialog,
  showVersionDialog,
  showVersionDiffDialog,
  showUploadDialog,
  uploadDir,
  newFileName,
  newFileParent,
  newDirName,
  newDirParent,
  renameTarget,
  versions,
  versionsLoading,
  versionDiffLoading,
  versionDiffOriginalTitle,
  versionDiffModifiedTitle,
  versionDiffOriginalContent,
  versionDiffModifiedContent,
  formatting,
  // 新建文件 / 新建目录 / 上传的在途锁，透传给弹窗组件绑按钮 loading
  creatingFile,
  creatingDir,
  uploading,
  editorLanguage,
  hasChanges,
  allFolders,
  loadTree,
  handleNodeClick,
  handleSave,
  handleCreateFile,
  handleCreateDir,
  handleDelete,
  handleMoveToRoot,
  allowDrag,
  allowDrop,
  handleNodeDrop,
  handleRename,
  openRename,
  openUploadDialog,
  handleUploadFileChange,
  handleUploadSubmit,
  handleAddToTask,
  loadVersions,
  handleRollback,
  handleClearVersions,
  handleCompareVersion,
  handleFormat,
  handleDownload,
  handleMobileBack
} = workspace

const {
  showDebugDialog,
  debugCode,
  debugFileName,
  debugLogs,
  debugRunning,
  debugError,
  debugExitCode,
  debugCodeChanged,
  showCodeRunner,
  runnerCode,
  runnerLanguage,
  runnerLogs,
  runnerRunning,
  runnerExitCode,
  runnerError,
  handleDebugRun,
  handleDebugStart,
  handleDebugStop,
  openCodeRunner,
  handleRunCode,
  handleStopRunner
} = execution

// 带 ?file= 进来（任务页点脚本、删除任务弹窗里点脚本）时先关掉调试窗和代码运行器（issue #169）：
// 页面在 keep-alive 里，调试窗开着时离开，回来时它还原样盖在新打开的脚本上，看到的仍是刚才调试的脚本。
// 置 false 后 useScriptExecution 里现有的 watch 会顺带停止运行。
// 不用 immediate：首次挂载时两个弹窗本来就是关的；判空规则与 useScriptWorkspace 打开文件的 watch 保持一致。
const route = useRoute()
watch(
  () => route.query.file,
  (fileParam) => {
    if (typeof fileParam !== 'string' || !fileParam.trim()) {
      return
    }
    showDebugDialog.value = false
    showCodeRunner.value = false
  }
)

async function handleDebugSave() {
  if (!selectedFile.value || isBinary.value) {
    return
  }
  fileContent.value = debugCode.value
  isEditing.value = true
  await handleSave()
  debugCodeChanged.value = debugCode.value !== originalContent.value
}

function openCreateFileDialog() {
  showCreateFileDialog.value = true
}

function openCreateDirDialog() {
  showCreateDirDialog.value = true
}

function openSelectedFileRenameDialog() {
  openRename(selectedFile.value)
}

function handleDeleteSelectedFile() {
  return handleDelete(selectedFile.value)
}

async function handleCancelEdit() {
  if (hasChanges.value) {
    try {
      await ElMessageBox.confirm('当前有未保存的改动，确认放弃修改并退出编辑？', '退出编辑', {
        confirmButtonText: '放弃改动',
        cancelButtonText: '继续编辑',
        type: 'warning'
      })
    } catch {
      return
    }
    fileContent.value = originalContent.value
  }
  isEditing.value = false
}
</script>

<template>
  <div class="scripts-page dd-fixed-page dd-page-hide-heading" :class="{ mobile: isMobile, compact: isCompactLayout }">
    <div class="page-header">
      <div>
        <h2>脚本管理</h2>
        <p class="page-subtitle">在线编辑、调试和管理您的自动化脚本文件</p>
      </div>
    </div>

    <div
      class="scripts-workspace"
      :class="{
        'mobile-show-editor': isCompactLayout && mobileShowEditor,
        'sidebar-collapsed': isSidebarCollapsed
      }"
    >
      <!-- current-file / reveal-ticket / sidebar-collapsed 三个给目录树定位用（issue #136）：
           侧栏据此展开祖先、高亮当前文件、滚到可见；侧栏看不见时只高亮，等它重新可见再滚 -->
      <ScriptsSidebar
        :is-mobile="isCompactLayout"
        :mobile-show-editor="mobileShowEditor"
        :sidebar-collapsed="isSidebarCollapsed"
        :current-file="selectedFile"
        :reveal-ticket="treeRevealTicket"
        :on-toggle-collapse="toggleSidebarCollapsed"
        :tree-loading="treeLoading"
        :file-tree="fileTree"
        :allow-drag="allowDrag"
        :allow-drop="allowDrop"
        :on-open-create-file="openCreateFileDialog"
        :on-open-create-dir="openCreateDirDialog"
        :on-open-upload="openUploadDialog"
        :on-open-code-runner="openCodeRunner"
        :on-refresh="loadTree"
        :on-node-click="handleNodeClick"
        :on-node-drop="handleNodeDrop"
        :on-open-rename="openRename"
        :on-delete="handleDelete"
        :on-move-to-root="handleMoveToRoot"
      />

      <ScriptsEditorPane
        v-model:file-content="fileContent"
        v-model:is-editing="isEditing"
        :is-mobile="isCompactLayout"
        :mobile-show-editor="mobileShowEditor"
        :sidebar-collapsed="isSidebarCollapsed"
        :on-expand-sidebar="toggleSidebarCollapsed"
        :selected-file="selectedFile"
        :is-binary="isBinary"
        :has-changes="hasChanges"
        :saving="saving"
        :formatting="formatting"
        :loading="loading"
        :editor-language="editorLanguage"
        :editor-auto-focus-ticket="editorAutoFocusTicket"
        :on-mobile-back="handleMobileBack"
        :on-debug-run="handleDebugRun"
        :on-open-create-file="openCreateFileDialog"
        :on-add-to-task="handleAddToTask"
        :on-save="handleSave"
        :on-cancel-edit="handleCancelEdit"
        :on-format="handleFormat"
        :on-load-versions="loadVersions"
        :on-open-rename="openSelectedFileRenameDialog"
        :on-download="handleDownload"
        :on-delete="handleDeleteSelectedFile"
      />
    </div>

    <ScriptManageDialogs
      v-model:show-create-file-dialog="showCreateFileDialog"
      v-model:show-create-dir-dialog="showCreateDirDialog"
      v-model:show-rename-dialog="showRenameDialog"
      v-model:show-version-dialog="showVersionDialog"
      v-model:show-version-diff-dialog="showVersionDiffDialog"
      v-model:show-upload-dialog="showUploadDialog"
      v-model:new-file-name="newFileName"
      v-model:new-file-parent="newFileParent"
      v-model:new-dir-name="newDirName"
      v-model:new-dir-parent="newDirParent"
      v-model:rename-target="renameTarget"
      v-model:upload-dir="uploadDir"
      v-model:version-diff-original-title="versionDiffOriginalTitle"
      v-model:version-diff-modified-title="versionDiffModifiedTitle"
      v-model:version-diff-original-content="versionDiffOriginalContent"
      v-model:version-diff-modified-content="versionDiffModifiedContent"
      :is-mobile="isMobile"
      :selected-file="selectedFile"
      :all-folders="allFolders"
      :editor-language="editorLanguage"
      :versions="versions"
      :versions-loading="versionsLoading"
      :version-diff-loading="versionDiffLoading"
      :creating-file="creatingFile"
      :creating-dir="creatingDir"
      :uploading="uploading"
      :on-create-file="handleCreateFile"
      :on-create-dir="handleCreateDir"
      :on-rename="handleRename"
      :on-compare-version="handleCompareVersion"
      :on-rollback="handleRollback"
      :on-clear-versions="handleClearVersions"
      :on-upload-file-change="handleUploadFileChange"
      :on-upload-submit="handleUploadSubmit"
    />

    <ScriptExecutionDialogs
      v-model:show-code-runner="showCodeRunner"
      v-model:runner-code="runnerCode"
      v-model:runner-language="runnerLanguage"
      v-model:show-debug-dialog="showDebugDialog"
      v-model:debug-code="debugCode"
      v-model:debug-code-changed="debugCodeChanged"
      :is-mobile="isMobile"
      :editor-language="editorLanguage"
      :debug-file-name="debugFileName"
      :debug-logs="debugLogs"
      :debug-running="debugRunning"
      :debug-error="debugError"
      :debug-exit-code="debugExitCode"
      :runner-logs="runnerLogs"
      :runner-running="runnerRunning"
      :runner-exit-code="runnerExitCode"
      :runner-error="runnerError"
      :debug-saving="saving"
      :on-debug-start="handleDebugStart"
      :on-debug-save="handleDebugSave"
      :on-debug-stop="handleDebugStop"
      :on-run-code="handleRunCode"
      :on-stop-runner="handleStopRunner"
    />
  </div>
</template>

<style scoped lang="scss">
.scripts-page {
  --scripts-accent: #3b82f6;
  --scripts-surface: var(--el-bg-color);
  --scripts-surface-muted: color-mix(in srgb, var(--el-fill-color) 70%, transparent);
  --scripts-border-soft: var(--el-border-color-lighter);

  padding: 0;
  font-size: 14px;
  font-family: var(--dd-font-ui);
  min-height: 0;
}

/* ---- Page Header (design system) ---- */
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
}

/* ---- Workspace（两卡容器：透明 flex，卡片间留间隙）---- */
.scripts-workspace {
  /* 目录树卡宽度的【单一真源】。
     以前这个 300px 被两处同时钉死（这里的 flex-basis + ScriptsSidebar.vue 的 width/min-width），
     只改一处会被另一处压住、卡片根本收不起来，所以收敛成这一个变量，两边都来消费它。 */
  --scripts-sidebar-width: 300px;

  display: flex;
  flex: 1 1 auto;
  width: 100%;
  height: 0;
  min-width: 0;
  min-height: 0;
  /* 目录树卡与编辑器卡之间的间隙 */
  gap: 14px;
  /* 折叠时 gap 要一起收掉，否则编辑器卡左边会凭空留 14px 空档；
     只有两个不换行的子项，逐帧重算 gap 不会引起整页重排 */
  transition: gap var(--dd-motion-normal) var(--dd-ease-standard);
  /* 容器本身透明，边框下放到两张子卡 */
  background: transparent;
}

/* ---- 目录树卡（靠 1px 边框划分层次，圆角跟随 --dd-radius-surface）---- */
:deep(.scripts-sidebar) {
  flex: 0 0 var(--scripts-sidebar-width);
  min-height: 0;
  overflow: hidden; /* 裁切内部溢出内容，顺带把内部贴边元素裁成同样的角 */
  background: var(--el-bg-color);
  border: 1px solid var(--el-border-color-lighter);
  /* 整张目录树卡是容器类表面 → surface 档 */
  border-radius: var(--dd-radius-surface);
  /* 折叠动画只动宽度，【绝不能用 transform】：
     残留 transform 会让这张卡成为编辑器内部 position:fixed 浮层（补全 / 搜索面板 /
     悬浮提示）的包含块，弹层会飘位 —— 与文末入场动画那条「只能 backwards」是同一个坑。
     border-width 也要一起过渡：box-sizing 是 border-box，1px 边框压不掉，
     只收 flex-basis 的话完全折叠后会留下一条 2px 竖线。 */
  transition:
    flex-basis var(--dd-motion-normal) var(--dd-ease-standard),
    border-width var(--dd-motion-normal) var(--dd-ease-standard);
}

/* ---- 目录树折叠态（只在桌面宽屏出现，见 useScriptWorkspaceBrowser 的 isSidebarCollapsed）---- */
.scripts-workspace.sidebar-collapsed {
  --scripts-sidebar-width: 0px;
  gap: 0;

  :deep(.scripts-sidebar) {
    border-width: 0;
    /* 收到 0 宽后卡片仍留在 DOM 里（留着才有宽度过渡），
       这里禁掉命中，避免鼠标点到看不见的搜索框/按钮上 */
    pointer-events: none;
  }
}

/* ---- 编辑器卡（靠 1px 边框划分层次，圆角跟随 --dd-radius-surface）---- */
:deep(.scripts-editor) {
  min-height: 0;
  overflow: hidden; /* 裁切内部溢出内容，顺带把内部贴边的 hero / 状态条裁成同样的角 */
  background: var(--el-bg-color);
  border: 1px solid var(--el-border-color-lighter);
  /* 整张编辑器卡是容器类表面 → surface 档 */
  border-radius: var(--dd-radius-surface);
}

:deep(.editor-hero) {
  border-bottom: 1px solid var(--el-border-color-lighter);
}

:deep(.editor-statusbar) {
  border-top: 1px solid var(--el-border-color-lighter);
}

/* ---- Mobile layout ---- */
.scripts-page.mobile {
  height: 100%;
  min-height: 0;
  overflow: hidden;

  .page-header {
    flex-direction: column;
    gap: 10px;
    margin-bottom: 14px;

    h2 {
      font-size: 18px;
    }
  }

  /* 移动端：两卡纵向堆叠，各占满宽度 */
  .scripts-workspace {
    flex-direction: column;
    flex: 1 1 auto;
    width: 100%;
    height: 100%;
    min-height: 0;
    gap: 12px;

    :deep(.scripts-sidebar) {
      width: 100%;
      min-width: unset;
      flex: 1 1 auto;
      min-height: 0;
      overflow: hidden; /* 目录滚动由内部 .sidebar-tree 负责 */
    }

    :deep(.scripts-editor) {
      width: 100%;
      flex: 1 1 auto;
      min-height: 0;
    }
  }

  /* 切到「显示编辑器」时隐藏目录卡、铺满编辑器卡 */
  .scripts-workspace.mobile-show-editor {
    :deep(.scripts-sidebar) {
      display: none !important;
    }

    :deep(.scripts-editor) {
      display: flex !important;
      height: 100%;
      min-height: 0;
    }
  }
}

/* ---- Compact desktop/tablet layout ---- */
.scripts-page.compact:not(.mobile) {
  .page-header {
    flex-direction: column;
    gap: 10px;
    margin-bottom: 14px;

    h2 {
      font-size: 18px;
    }
  }

  /* compact：同样纵向堆叠两卡，保留卡片圆角与间隙 */
  .scripts-workspace {
    flex-direction: column;
    min-height: 0;
    height: 0;
    gap: 12px;

    :deep(.scripts-sidebar) {
      width: 100%;
      min-width: 0;
      flex: 1 1 auto;
      min-height: 0;
      overflow: hidden;
    }

    :deep(.scripts-editor) {
      width: 100%;
      min-width: 0;
      flex: 1 1 auto;
      min-height: 0;
    }
  }

  .scripts-workspace.mobile-show-editor {
    :deep(.scripts-editor) {
      height: 100%;
    }
  }
}

// ===== 入场动画 =====
// 与全站 dd-*-rise-in 同一语汇：只对两张卡片级容器（目录树卡 / 编辑器卡）做淡入上移，
// ≤60ms 轻微错落；时长与缓动全部走令牌，prefers-reduced-motion 下令牌降为 1ms、
// delay 被全局补丁归零，等效关闭。目录树里的文件节点属于列表行，绝不逐行 stagger。
//
// 只动 opacity + translateY，不碰 height / flex：
// .scripts-workspace 的 `flex + min-height: 0 + height: 0` 内嵌滚动链完全不受影响。
//
// fill-mode 用 backwards 而不是列表页惯用的 both：
// 编辑器卡里装着代码编辑器，both 会把 `transform: translateY(0)` 永久留在 .scripts-editor 上，
// 让它成为编辑器内部 position:fixed 浮层（搜索面板 / 补全 / 悬浮提示）的包含块 → 弹层错位，
// 与 ScriptExecutionDialogs.vue 里那条「不加 both」的注释是同一个坑。
// backwards 只在 delay 期间铺 from 帧，动画结束后回到自然样式（opacity 1、无 transform），
// 末态与 to 帧完全一致，视觉上没有区别。
@keyframes dd-scripts-rise-in {
  from {
    opacity: 0;
    transform: translateY(12px);
  }
  to {
    opacity: 1;
    transform: translateY(0);
  }
}

:deep(.scripts-sidebar),
:deep(.scripts-editor) {
  animation: dd-scripts-rise-in var(--dd-motion-page) var(--dd-ease-decelerate)
    backwards;
}

// 轻微错落：目录树卡先入，编辑器卡略晚。
// 移动端两卡是 display 切换（sidebar / editor 互斥），切换时动画会重放一次，
// 正好充当「文件列表 ↔ 编辑器」的页面级进场，属于预期行为。
:deep(.scripts-editor) {
  animation-delay: 60ms;
}
</style>
