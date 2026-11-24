<script setup lang="ts">
import { Upload } from '@element-plus/icons-vue'
import { computed, defineAsyncComponent, ref, watch } from 'vue'
import type { ScriptVersionRecord } from '../types'
import { formatDateTime } from '@/utils/datetime'

// 差异对比只服务「版本历史 → 对比」这个低频弹窗，而它比普通编辑器多带一套 merge 依赖，
// 所以仍保持懒加载：不点对比就不下载这块代码。
const CodeDiffEditor = defineAsyncComponent(() => import('@/components/CodeDiffEditor.vue'))

const showCreateFileDialog = defineModel<boolean>('showCreateFileDialog', { required: true })
const showCreateDirDialog = defineModel<boolean>('showCreateDirDialog', { required: true })
const showRenameDialog = defineModel<boolean>('showRenameDialog', { required: true })
const showVersionDialog = defineModel<boolean>('showVersionDialog', { required: true })
const showVersionDiffDialog = defineModel<boolean>('showVersionDiffDialog', { required: true })
const showUploadDialog = defineModel<boolean>('showUploadDialog', { required: true })

const newFileName = defineModel<string>('newFileName', { required: true })
const newFileParent = defineModel<string>('newFileParent', { required: true })
const newDirName = defineModel<string>('newDirName', { required: true })
const newDirParent = defineModel<string>('newDirParent', { required: true })
const renameTarget = defineModel<string>('renameTarget', { required: true })
const uploadDir = defineModel<string>('uploadDir', { required: true })
const versionDiffOriginalTitle = defineModel<string>('versionDiffOriginalTitle', { required: true })
const versionDiffModifiedTitle = defineModel<string>('versionDiffModifiedTitle', { required: true })
const versionDiffOriginalContent = defineModel<string>('versionDiffOriginalContent', { required: true })
const versionDiffModifiedContent = defineModel<string>('versionDiffModifiedContent', { required: true })

const props = withDefaults(defineProps<{
  isMobile: boolean
  selectedFile: string
  allFolders: string[]
  editorLanguage: string
  versions: ScriptVersionRecord[]
  versionsLoading: boolean
  versionDiffLoading: boolean
  // 三个在途锁由父级（useScriptWorkspaceActions）持有，这里只负责把按钮锁住。
  // 默认 false：老调用点不传也不会把按钮锁死。
  creatingFile?: boolean
  creatingDir?: boolean
  uploading?: boolean
  onCreateFile: () => void | Promise<void>
  onCreateDir: () => void | Promise<void>
  onRename: () => void | Promise<void>
  onCompareVersion: (version: ScriptVersionRecord) => void | Promise<void>
  onRollback: (versionId: number) => void | Promise<void>
  onClearVersions: () => void | Promise<void>
  onUploadFileChange: (file: any, files: any[]) => void
  onUploadSubmit: () => void | Promise<void>
}>(), {
  creatingFile: false,
  creatingDir: false,
  uploading: false
})

// 移动/复制的目标目录下拉同样要排掉受控目录，
// 否则用户能在这里选中 .git 当目标（后端会直接拒绝，白跑一趟）。
const hiddenTargetFolderSegments = new Set(['node_modules', '__pycache__', '.git', '.svn', '.hg', '.bzr'])
const nestedFolders = computed(() => props.allFolders.filter(folder => folder && !folder.split('/').some(segment => hiddenTargetFolderSegments.has(segment.trim().toLowerCase()))))
const ignoreWhitespaceDiff = ref(false)

function normalizeWhitespaceForCompare(content: string) {
  return content.replace(/\s+/g, '')
}

const versionDiffExactSame = computed(() => versionDiffOriginalContent.value === versionDiffModifiedContent.value)
const versionDiffWhitespaceOnly = computed(() =>
  !versionDiffExactSame.value &&
  normalizeWhitespaceForCompare(versionDiffOriginalContent.value) === normalizeWhitespaceForCompare(versionDiffModifiedContent.value)
)
const versionDiffEffectivelySame = computed(() =>
  versionDiffExactSame.value || (ignoreWhitespaceDiff.value && versionDiffWhitespaceOnly.value)
)
const versionDiffSummaryTitle = computed(() => {
  if (versionDiffExactSame.value) {
    return '当前版本与历史版本一致'
  }
  if (ignoreWhitespaceDiff.value && versionDiffWhitespaceOnly.value) {
    return '当前版本与历史版本仅存在空白差异'
  }
  return ''
})
const versionDiffSummaryDescription = computed(() => {
  if (versionDiffExactSame.value) {
    return '这两个版本的代码内容完全相同，无需继续查看差异。'
  }
  if (ignoreWhitespaceDiff.value && versionDiffWhitespaceOnly.value) {
    return '已开启忽略空白差异，当前只检测到空格、缩进或换行变化。关闭开关后可查看完整差异。'
  }
  return ''
})

watch(showVersionDiffDialog, (visible) => {
  if (!visible) {
    ignoreWhitespaceDiff.value = false
  }
})
</script>

<template>
  <el-dialog v-model="showCreateFileDialog" title="新建文件" :width="isMobile ? '90%' : '480px'" :fullscreen="isMobile">
    <el-form :label-width="isMobile ? 'auto' : '80px'" :label-position="isMobile ? 'top' : 'right'">
      <el-form-item label="上级目录">
        <!-- 目录很多时允许直接输入关键词筛选，避免用户只能滚动查找。 -->
        <el-select
          v-model="newFileParent"
          placeholder="输入关键词搜索目录，留空为根目录"
          clearable
          filterable
          default-first-option
          no-match-text="没有匹配的目录"
          no-data-text="暂无可选目录"
          style="width: 100%"
        >
          <el-option label="根目录" value="" />
          <el-option v-for="folder in nestedFolders" :key="folder" :label="folder" :value="folder" />
        </el-select>
      </el-form-item>
      <el-form-item label="文件名">
        <el-input v-model="newFileName" placeholder="如: script.py / main.go / task.sh" @keyup.enter="onCreateFile" />
      </el-form-item>
    </el-form>
    <template #footer>
      <el-button @click="showCreateFileDialog = false">取消</el-button>
      <el-button type="primary" :loading="props.creatingFile" :disabled="props.creatingFile" @click="onCreateFile">创建</el-button>
    </template>
  </el-dialog>

  <el-dialog v-model="showCreateDirDialog" title="新建目录" :width="isMobile ? '90%' : '480px'" :fullscreen="isMobile">
    <el-form :label-width="isMobile ? 'auto' : '80px'" :label-position="isMobile ? 'top' : 'right'">
      <el-form-item label="上级目录">
        <!-- 目录很多时允许直接输入关键词筛选，避免用户只能滚动查找。 -->
        <el-select
          v-model="newDirParent"
          placeholder="输入关键词搜索目录，留空为根目录"
          clearable
          filterable
          default-first-option
          no-match-text="没有匹配的目录"
          no-data-text="暂无可选目录"
          style="width: 100%"
        >
          <el-option label="根目录" value="" />
          <el-option v-for="folder in nestedFolders" :key="folder" :label="folder" :value="folder" />
        </el-select>
      </el-form-item>
      <el-form-item label="目录名">
        <el-input v-model="newDirName" placeholder="如: utils" @keyup.enter="onCreateDir" />
      </el-form-item>
    </el-form>
    <template #footer>
      <el-button @click="showCreateDirDialog = false">取消</el-button>
      <el-button type="primary" :loading="props.creatingDir" :disabled="props.creatingDir" @click="onCreateDir">创建</el-button>
    </template>
  </el-dialog>

  <el-dialog v-model="showRenameDialog" title="重命名" :width="isMobile ? '90%' : '400px'" :fullscreen="isMobile">
    <el-input v-model="renameTarget" placeholder="新名称" @keyup.enter="onRename" />
    <template #footer>
      <el-button @click="showRenameDialog = false">取消</el-button>
      <el-button type="primary" @click="onRename">确定</el-button>
    </template>
  </el-dialog>

  <el-dialog
    v-model="showVersionDialog"
    title="版本历史"
    :width="isMobile ? '100%' : 'min(1180px, 94vw)'"
    :fullscreen="isMobile"
    :top="isMobile ? '0' : '5vh'"
  >
    <div class="version-history-toolbar">
      <div class="version-history-meta">
        <strong>{{ props.selectedFile || '当前脚本' }}</strong>
        <span>
          {{ props.versions.length > 0 ? `共 ${props.versions.length} 条版本记录` : '当前脚本还没有版本记录' }}
        </span>
      </div>
      <el-button
        type="danger"
        plain
        :disabled="props.versionsLoading || !props.selectedFile || props.versions.length === 0"
        @click="props.onClearVersions"
      >
        清空版本历史
      </el-button>
    </div>
    <!-- 移动端全屏时底部有「关闭」footer（约 57px），表格最大高度要把它让出来（300 = 无 footer 时的 240 + 60），
         否则全屏弹窗的 body 会和表格自己的滚动叠成两层。桌面没有这条 footer，320px 不变。 -->
    <el-table
      :data="versions"
      v-loading="versionsLoading"
      :max-height="isMobile ? 'calc(100dvh - 300px)' : 'calc(100dvh - 320px)'"
      table-layout="auto"
      class="version-history-table"
    >
      <el-table-column prop="version" label="版本" width="88" />
      <el-table-column prop="message" label="备注" min-width="260" show-overflow-tooltip />
      <el-table-column prop="content_length" label="大小" width="120">
        <template #default="{ row }">{{ (row.content_length / 1024).toFixed(1) }} KB</template>
      </el-table-column>
      <el-table-column prop="created_at" label="时间" width="188">
        <template #default="{ row }">{{ formatDateTime(row.created_at) }}</template>
      </el-table-column>
      <el-table-column label="操作" width="156" fixed="right">
        <template #default="{ row }">
          <el-button size="small" text type="primary" @click="onCompareVersion(row)">对比</el-button>
          <el-button size="small" text type="primary" @click="onRollback(row.id)">回滚</el-button>
        </template>
      </el-table-column>
      <template #empty>
        <el-empty description="当前脚本还没有版本记录" />
      </template>
    </el-table>
    <!-- 移动端全屏时右上角 × 被全局规则隐藏（F2），关闭入口放到右下角。
         条件与本弹窗的 :fullscreen 同用 isMobile（父级传入的就是 useResponsive 的 isMobile）；
         桌面不传 footer 插槽，EP 不渲染 footer，保持原样。 -->
    <template v-if="isMobile" #footer>
      <el-button @click="showVersionDialog = false">关闭</el-button>
    </template>
  </el-dialog>

  <el-dialog
    v-model="showVersionDiffDialog"
    title="版本差异对比"
    :width="isMobile ? '100%' : '92%'"
    :fullscreen="isMobile"
    :top="isMobile ? '0' : '4vh'"
    :close-on-click-modal="false"
    append-to-body
    destroy-on-close
  >
    <div
      class="version-diff-dialog"
      v-loading="props.versionDiffLoading"
      element-loading-text="正在加载历史版本内容..."
    >
      <div class="version-diff-header">
        <div class="version-diff-side">
          <span class="version-diff-caption">左侧：历史代码</span>
          <strong>{{ versionDiffOriginalTitle || '历史版本' }}</strong>
        </div>
        <div class="version-diff-side version-diff-side--current">
          <span class="version-diff-caption">右侧：当前代码</span>
          <strong>{{ versionDiffModifiedTitle || '当前代码' }}</strong>
        </div>
      </div>
      <div class="version-diff-toolbar">
        <div v-if="versionDiffSummaryTitle" class="version-diff-summary">
          <strong>{{ versionDiffSummaryTitle }}</strong>
          <span>{{ versionDiffSummaryDescription }}</span>
        </div>
        <div class="version-diff-switch">
          <span>忽略空白差异</span>
          <el-switch
            v-model="ignoreWhitespaceDiff"
            inline-prompt
            active-text="开"
            inactive-text="关"
          />
        </div>
      </div>
      <div v-if="!props.versionDiffLoading && versionDiffEffectivelySame" class="version-diff-empty">
        <el-empty :description="versionDiffSummaryTitle || '当前版本与历史版本一致'">
          <template #description>
            <div class="version-diff-empty-copy">
              <strong>{{ versionDiffSummaryTitle || '当前版本与历史版本一致' }}</strong>
              <span>{{ versionDiffSummaryDescription || '这两个版本没有可显示的差异。' }}</span>
            </div>
          </template>
        </el-empty>
      </div>
      <CodeDiffEditor
        v-else-if="!props.versionDiffLoading"
        :original-value="versionDiffOriginalContent"
        :modified-value="versionDiffModifiedContent"
        :language="props.editorLanguage"
        :render-side-by-side="!isMobile"
        :ignore-trim-whitespace="ignoreWhitespaceDiff"
        class="version-diff-editor"
      />
    </div>
    <!-- 同上：移动端的关闭入口。本弹窗 close-on-click-modal=false，点遮罩关不掉，这颗按钮是移动端唯一的出口。
         置 false 与点 × 走的是同一条 v-model，上面 watch(showVersionDiffDialog) 的复位逻辑照样执行。 -->
    <template v-if="isMobile" #footer>
      <el-button @click="showVersionDiffDialog = false">关闭</el-button>
    </template>
  </el-dialog>

  <el-dialog v-model="showUploadDialog" title="上传文件" :width="isMobile ? '90%' : '480px'" :fullscreen="isMobile" destroy-on-close>
    <el-form :label-width="isMobile ? 'auto' : '80px'" :label-position="isMobile ? 'top' : 'right'">
      <el-form-item label="目标目录">
        <!-- 上传目标目录也支持输入关键词筛选，和新建文件/目录保持一致。 -->
        <el-select
          v-model="uploadDir"
          placeholder="输入关键词搜索目录，留空为根目录"
          clearable
          filterable
          default-first-option
          no-match-text="没有匹配的目录"
          no-data-text="暂无可选目录"
          style="width: 100%"
        >
          <el-option label="根目录" value="" />
          <el-option v-for="folder in nestedFolders" :key="folder" :label="folder" :value="folder" />
        </el-select>
      </el-form-item>
      <el-form-item label="选择文件">
        <el-upload
          :auto-upload="false"
          :show-file-list="true"
          multiple
          :on-change="onUploadFileChange"
          :on-remove="onUploadFileChange"
          drag
        >
          <el-icon :size="40"><Upload /></el-icon>
          <div>拖拽文件到此处或点击选择</div>
          <div class="el-upload__tip">支持一次选择多个脚本文件，单个文件大小不超过 100MB。</div>
        </el-upload>
      </el-form-item>
    </el-form>
    <template #footer>
      <el-button @click="showUploadDialog = false">取消</el-button>
      <el-button type="primary" :loading="props.uploading" :disabled="props.uploading" @click="onUploadSubmit">上传</el-button>
    </template>
  </el-dialog>
</template>

<style scoped lang="scss">
.version-history-toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  margin-bottom: 14px;
  flex-wrap: wrap;
}

.version-history-meta {
  display: flex;
  flex-direction: column;
  gap: 4px;
  min-width: 0;

  strong {
    font-size: 14px;
    font-weight: 700;
    color: var(--el-text-color-primary);
    line-height: 1.5;
    word-break: break-all;
  }

  span {
    font-size: 12px;
    color: var(--el-text-color-secondary);
    line-height: 1.6;
  }
}

.version-history-table {
  :deep(.el-empty) {
    padding: 32px 0;
  }
}

.version-diff-dialog {
  display: flex;
  flex-direction: column;
  height: calc(92vh - 80px);
}

.version-diff-header {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 12px;
  margin-bottom: 12px;
}

.version-diff-side {
  display: flex;
  flex-direction: column;
  gap: 4px;
  padding: 10px 12px;
  border: 1px solid var(--el-border-color-light);
  // 版本对比两侧的信息块是弹窗内独立成块的容器类表面 → surface 档
  border-radius: var(--dd-radius-surface);
  background: var(--el-fill-color-light);
}

.version-diff-side--current {
  text-align: right;
}

.version-diff-caption {
  font-size: 12px;
  color: var(--el-text-color-secondary);
}

.version-diff-editor {
  flex: 1 1 0;
  min-height: 0;
  overflow: hidden;
}

.version-diff-toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  margin-bottom: 12px;
  flex-wrap: wrap;
}

.version-diff-summary {
  display: flex;
  flex-direction: column;
  gap: 4px;
  min-width: 0;

  strong {
    font-size: 13px;
    font-weight: 700;
    color: var(--el-text-color-primary);
  }

  span {
    font-size: 12px;
    color: var(--el-text-color-secondary);
    line-height: 1.6;
  }
}

.version-diff-switch {
  display: inline-flex;
  align-items: center;
  gap: 10px;
  padding: 8px 12px;
  // 这是包着 el-switch 与说明文字的一小条控件底槽 → control 档
  border-radius: var(--dd-radius-control);
  background: var(--el-fill-color-light);
  color: var(--el-text-color-secondary);
  font-size: 12px;
  font-weight: 600;
}

.version-diff-empty {
  flex: 1;
  min-height: 62vh;
  display: flex;
  align-items: center;
  justify-content: center;
  border: 1px dashed var(--el-border-color);
  // 空态占位区是整块虚线容器（占满 62vh）→ surface 档
  border-radius: var(--dd-radius-surface);
  background: color-mix(in srgb, var(--el-fill-color-light) 72%, var(--dd-surface-solid));
}

.version-diff-empty-copy {
  display: flex;
  flex-direction: column;
  gap: 6px;

  strong {
    font-size: 15px;
    font-weight: 700;
    color: var(--el-text-color-primary);
  }

  span {
    font-size: 13px;
    line-height: 1.7;
    color: var(--el-text-color-secondary);
  }
}

@media (max-width: 768px) {
  .version-history-toolbar {
    align-items: stretch;
  }

  .version-diff-header {
    grid-template-columns: 1fr;
  }

  .version-diff-side--current {
    text-align: left;
  }

  .version-diff-editor {
    min-height: calc(100dvh - 220px);
  }

  .version-diff-toolbar {
    align-items: stretch;
  }

  .version-diff-switch {
    justify-content: space-between;
  }

  .version-diff-empty {
    min-height: calc(100dvh - 220px);
  }
}
</style>
