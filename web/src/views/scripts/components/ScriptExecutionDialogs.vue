<script setup lang="ts">
import { Edit, RefreshRight, Select, Tickets, VideoPause, VideoPlay } from '@element-plus/icons-vue'
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { ansiToHtml, normalizeAnsi } from '@/utils/ansi'
import { useLogAutoFollow } from '@/composables/useLogAutoFollow'
import {
  EDITOR_PREFERENCES_CHANGE_EVENT,
  readEditorPreferences,
} from '@/utils/editorPreferences'
import type { EditorIndentWidth } from '@/utils/editorPreferences'
// 这里原来是 defineAsyncComponent 懒加载，但那层包装其实一直没带来收益：
// 旧的 MonacoEditor 靠 utils/monaco.ts 在运行期用 AMD loader 去拉 /monaco/vs 下的资源，
// 那几 MB 从来没被 Vite 打进任何 chunk，异步组件既挡不住它、也没什么可挡。
// 换成 CodeEditor 之后代码确实由 Vite 打包了，但同路由的 ScriptsEditorPane 已经静态引入了它，
// 这里再切一个异步块也分不出去，只会多一次模块往返，所以直接静态 import。
// （同目录 ScriptManageDialogs.vue 里的 CodeDiffEditor 仍保留 defineAsyncComponent，
//  那个是有理由的：@codemirror/merge 只有「版本对比」弹窗用得到，懒加载能把它挡在主 chunk 外。）
import CodeEditor from '@/components/CodeEditor.vue'

const showCodeRunner = defineModel<boolean>('showCodeRunner', { required: true })
const runnerCode = defineModel<string>('runnerCode', { required: true })
const runnerLanguage = defineModel<string>('runnerLanguage', { required: true })
const showDebugDialog = defineModel<boolean>('showDebugDialog', { required: true })
const debugCode = defineModel<string>('debugCode', { required: true })
const debugCodeChanged = defineModel<boolean>('debugCodeChanged', { required: true })

const props = defineProps<{
  isMobile: boolean
  editorLanguage: string
  debugFileName: string
  debugLogs: string[]
  debugRunning: boolean
  debugSaving: boolean
  debugError: string
  debugExitCode: number | null
  runnerLogs: string[]
  runnerRunning: boolean
  runnerExitCode: number | null
  runnerError: string
  onDebugStart: () => void | Promise<void>
  onDebugSave: () => void | Promise<void>
  onDebugStop: () => void | Promise<void>
  onRunCode: () => void | Promise<void>
  onStopRunner: () => void | Promise<void>
}>()

/**
 * 缩进宽度偏好。
 *
 * 🔴 这两个弹窗里的 <CodeEditor> **必须**显式传 :indent-width，不能吃 prop 默认值 2 ——
 * 这不是「顺手也配一下」，删掉它会把主编辑器一起带偏：
 * Monaco 的 tabSize 是**全进程共享**的配置，弹窗一开，全局 tabSize 就被拉回 2
 * 并广播给所有活着的 model，后面那个还开着的脚本编辑器（可能正记着 Tab 4）当场
 * 变成 2 列缩进、参考线跟着走位，而齿轮菜单和底部状态条仍然写着 Tab 4。
 * 组件侧另有一道自愈守卫兜底，但那只是兜底，用户会看见一次可见的闪变。
 *
 * 其余四项（wordWrap / minimap / indentGuides / whitespace）**刻意保持不传**：
 * 它们的 prop 默认值就等于「加这些功能之前的行为」，不传是这两个弹窗的既定取舍，
 * 别顺手一起补上（见 CodeEditor.vue 里那几个 prop 的注释）。
 *
 * 只持这一项、不把整套 prefs 搬进来：弹窗要的就是「别把全局 tabSize 拉歪」这一件事。
 */
const indentWidth = ref<EditorIndentWidth>(readEditorPreferences().indent_width)

// 与两个编辑器页面同一份真源：在齿轮菜单里切了缩进宽度、或者服务端那份偏好刚拉回来，
// 都靠这个事件同步过来。刻意从存储重读而不是从事件里取值（事件本身不带载荷），
// 范式对齐两个页面里的 syncPreferences。
function syncIndentWidth() {
  indentWidth.value = readEditorPreferences().indent_width
}

onMounted(() => {
  window.addEventListener(EDITOR_PREFERENCES_CHANGE_EVENT, syncIndentWidth)
})

onBeforeUnmount(() => {
  window.removeEventListener(EDITOR_PREFERENCES_CHANGE_EVENT, syncIndentWidth)
})

const debugLogsHtml = computed(() => ansiToHtml(normalizeAnsi(props.debugLogs.join('\n'))))
const runnerLogsHtml = computed(() => ansiToHtml(normalizeAnsi(props.runnerLogs.join('\n'))))

// 调试日志 / 运行输出的自动跟随（原来这两处完全不自动滚动，现补上「运行中跟随、上翻暂停」）。
// 日志由父组件按 500ms 轮询整体替换数组传下来，引用必变，watch 一定触发。
// 两个 ref 挂在 .panel-content.debug-log-content（overflow:auto 的那一层）上，不能挂在里面的 <pre>：
// <pre> 只是随内容撑高的 flex 子项、自己不滚动，对它读写 scrollTop、监听 scroll 全是空操作。
const debugLogRef = ref<HTMLElement>()
const runnerLogRef = ref<HTMLElement>()
const debugFollow = useLogAutoFollow(debugLogRef)
const runnerFollow = useLogAutoFollow(runnerLogRef)

// 调试运行点「停止」：useScriptExecution 的 stopDebugRun 先同步置 debugRunning=false，
// await 停止请求之后才补取最后几行 logs 赋回来。下降沿若照常 end()，这批补取的输出到达时
// 跟随已冻结、onContentChange 成了空操作，跟随中的用户看不到结尾。
// 所以用户主动停止期间，下降沿只贴底、不冻结；等 onDebugStop 整个结束（补取已赋值）再「先贴底、再冻结」，
// 与系统命令行 handleStop 里「先 appendLogs 再 end()」同一口径（见下面的 handleDebugStop）。
// 代码运行器的停止不补取日志，没有这个问题，仍由下降沿直接冻结。
let debugStopPending = false

// 运行结束那一拍，父组件的轮询通常【同时】换上最后一批输出（useScriptExecution 里先赋 logs 再置 running=false）。
// 这两个 watcher 是 pre，会先于下面 flush:'post' 的日志 watcher 执行，直接 end() 的话那批输出就不再贴底。
// 所以下降沿先按跟随态排一次贴底（在 nextTick 里执行，届时 DOM 已换上新输出），再冻结。
watch(
  () => props.debugRunning,
  (running) => {
    if (running) {
      debugFollow.begin(true)
    } else {
      debugFollow.onContentChange()
      // 用户主动停止时先不冻结，由 handleDebugStop 在补取完最后几行之后再冻结（理由见 debugStopPending）
      if (!debugStopPending) debugFollow.end()
    }
  },
)
watch(
  () => props.runnerRunning,
  (running) => {
    if (running) {
      runnerFollow.begin(true)
    } else {
      runnerFollow.onContentChange()
      runnerFollow.end()
    }
  },
)
// flush:'post' 保证 DOM 已按新数组 patch 完再贴底，否则会停在这一帧之前的高度上
watch(() => props.debugLogs, () => debugFollow.onContentChange(), { flush: 'post' })
watch(() => props.runnerLogs, () => runnerFollow.onContentChange(), { flush: 'post' })

async function handleDebugStop() {
  debugStopPending = true
  try {
    await props.onDebugStop()
  } finally {
    debugStopPending = false
    // 补取的最后几行此刻已赋值：跟随中先排一次贴底（nextTick 里执行，届时 DOM 已换上），再冻结。
    // 停止请求还在飞时用户可能已经点了「重新运行」：新一轮已经 begin(true)，不能把它冻结掉。
    if (!props.debugRunning) {
      debugFollow.onContentChange()
      debugFollow.end()
    }
  }
}

function markDebugCodeChanged() {
  debugCodeChanged.value = true
}
</script>

<template>
  <el-dialog
    v-model="showCodeRunner"
    title="代码运行器"
    fullscreen
    class="script-execution-fullscreen-dialog"
    :close-on-click-modal="false"
    destroy-on-close
  >
    <div class="debug-container debug-dialog-container" :class="{ mobile: isMobile }">
      <div class="debug-code-panel">
        <div class="panel-header">
          <el-icon><Edit /></el-icon>
          <span>代码编辑</span>
          <el-select v-model="runnerLanguage" size="small" style="width: 130px; margin-left: auto">
            <el-option label="Python" value="python" />
            <el-option label="JavaScript" value="javascript" />
            <el-option label="TypeScript" value="typescript" />
            <el-option label="Shell" value="shell" />
            <el-option label="Go" value="go" />
          </el-select>
        </div>
        <div class="panel-content" style="padding: 0">
          <!-- :indent-width 必须传，理由见 <script> 里 indentWidth 的注释：
               Monaco 的 tabSize 是全进程共享的，不传就会把主编辑器的缩进一起拉回 2。 -->
          <CodeEditor
            v-if="showCodeRunner"
            v-model="runnerCode"
            :language="runnerLanguage === 'shell' ? 'shell' : runnerLanguage"
            :indent-width="indentWidth"
            min-height="0"
            style="height: 100%; min-height: 0"
          />
        </div>
      </div>
      <div class="debug-log-panel">
        <div class="panel-header">
          <el-icon><Tickets /></el-icon>
          <span>运行输出</span>
          <el-tag v-if="runnerRunning" type="warning" size="small" effect="plain">运行中</el-tag>
          <el-tag v-else-if="runnerError || runnerLogs.length > 0" :type="runnerExitCode === 0 ? 'success' : 'danger'" size="small" effect="plain">
            {{ runnerExitCode === 0 ? '成功' : '失败' }}
          </el-tag>
        </div>
        <div ref="runnerLogRef" class="panel-content debug-log-content dd-log-surface">
          <div v-if="runnerError" class="debug-error">
            <el-alert type="error" :title="runnerError === 'failed' ? `退出码: ${runnerExitCode}` : runnerError" :closable="false" show-icon />
          </div>
          <pre v-if="runnerLogs.length > 0" class="debug-logs" v-html="runnerLogsHtml"></pre>
          <el-empty v-if="!runnerLogs.length && !runnerError" description="点击运行按钮执行代码" :image-size="80" />
        </div>
      </div>
    </div>
    <template #footer>
      <el-button v-if="!runnerRunning && !runnerLogs.length && !runnerError" type="primary" @click="onRunCode">
        <el-icon><VideoPlay /></el-icon>运行
      </el-button>
      <el-button v-if="runnerRunning" type="danger" @click="onStopRunner">
        <el-icon><VideoPause /></el-icon>停止
      </el-button>
      <el-button v-if="!runnerRunning && (runnerLogs.length > 0 || runnerError)" type="primary" @click="onRunCode">
        <el-icon><RefreshRight /></el-icon>重新运行
      </el-button>
      <el-button @click="showCodeRunner = false">关闭</el-button>
    </template>
  </el-dialog>

  <el-dialog
    v-model="showDebugDialog"
    title="调试运行"
    fullscreen
    class="script-execution-fullscreen-dialog"
    :close-on-click-modal="false"
    destroy-on-close
  >
    <div class="debug-container debug-dialog-container" :class="{ mobile: isMobile }">
      <div class="debug-code-panel">
        <div class="panel-header">
          <el-icon><Edit /></el-icon>
          <span>{{ debugFileName }}</span>
          <el-tag v-if="debugCodeChanged" type="warning" size="small" effect="plain">已修改</el-tag>
        </div>
        <div class="panel-content" style="padding: 0">
          <!-- :indent-width 必须传，理由见 <script> 里 indentWidth 的注释：
               Monaco 的 tabSize 是全进程共享的，不传就会把主编辑器的缩进一起拉回 2。 -->
          <CodeEditor
            v-if="showDebugDialog"
            v-model="debugCode"
            :language="editorLanguage"
            :indent-width="indentWidth"
            min-height="0"
            style="height: 100%; min-height: 0"
            @update:modelValue="markDebugCodeChanged"
          />
        </div>
      </div>
      <div class="debug-log-panel">
        <div class="panel-header">
          <el-icon><Tickets /></el-icon>
          <span>调试日志</span>
          <el-tag v-if="debugRunning" type="warning" size="small" effect="plain">运行中</el-tag>
          <el-tag v-else-if="debugLogs.length > 0" type="success" size="small" effect="plain">已完成</el-tag>
        </div>
        <div ref="debugLogRef" class="panel-content debug-log-content dd-log-surface">
          <div v-if="debugError" class="debug-error">
            <el-alert type="error" :title="`退出码: ${debugExitCode}`" :closable="false" show-icon />
          </div>
          <pre v-if="debugLogs.length > 0" class="debug-logs" v-html="debugLogsHtml"></pre>
          <el-empty v-if="!debugLogs.length && !debugError" description="点击运行按钮开始调试" :image-size="80" />
        </div>
      </div>
    </div>
    <template #footer>
      <el-button v-if="!debugRunning && !debugLogs.length && !debugError" type="primary" @click="onDebugStart">
        <el-icon><VideoPlay /></el-icon>运行
      </el-button>
      <el-button :disabled="!debugCodeChanged || debugRunning || debugSaving" @click="onDebugSave">
        <el-icon><Select /></el-icon>{{ debugSaving ? '保存中' : '保存' }}
      </el-button>
      <el-button v-if="debugRunning" type="danger" @click="handleDebugStop">
        <el-icon><VideoPause /></el-icon>停止
      </el-button>
      <el-button v-if="!debugRunning && (debugLogs.length > 0 || debugError)" type="primary" @click="onDebugStart">
        <el-icon><RefreshRight /></el-icon>重新运行
      </el-button>
      <el-button @click="showDebugDialog = false">关闭</el-button>
    </template>
  </el-dialog>
</template>

<style scoped lang="scss">
.debug-container {
  display: flex;
  gap: 16px;
  // 全屏调试时让代码区和日志区尽量吃满可用高度，不再像小弹窗一样压缩内容。
  height: 100%;
  min-height: 0;
  max-height: none;
  min-width: 0;
}

.debug-code-panel,
.debug-log-panel {
  flex: 1;
  display: flex;
  flex-direction: column;
  min-width: 0;
  min-height: 0;
  border: 1px solid var(--el-border-color-light);
  // 代码区 / 日志区都是独立成块、带边框的容器类表面 → surface 档；
  // 下面的 overflow: hidden 会把内部贴边铺满的 header / 日志区自动裁成同样的角
  border-radius: var(--dd-radius-surface);
  overflow: hidden;
  background: var(--el-bg-color);
}

/*
  整屏滑入后，内部两块面板错落淡入，营造层次感。
  只用 opacity，不用 transform：.debug-code-panel 内含代码编辑器，残留 transform
  会成为其 fixed 浮层的包含块导致错位；opacity 结束为 1 不产生层叠上下文，安全。
*/
.debug-code-panel {
  animation: dd-script-panel-fade var(--dd-motion-normal) var(--dd-ease-decelerate) both;
  animation-delay: 60ms;
}

.debug-log-panel {
  animation: dd-script-panel-fade var(--dd-motion-normal) var(--dd-ease-decelerate) both;
  animation-delay: 140ms;
}

@keyframes dd-script-panel-fade {
  from {
    opacity: 0;
  }
  to {
    opacity: 1;
  }
}

.debug-dialog-container {
  height: 100%;
  min-height: 0;
  max-height: none;
}

.panel-header {
  padding: 12px 16px;
  background: var(--el-fill-color-light);
  border-bottom: 1px solid var(--el-border-color-light);
  display: flex;
  align-items: center;
  gap: 8px;
  font-weight: 600;
  font-size: 14px;
  flex-shrink: 0;
}

.panel-content {
  flex: 1;
  min-height: 0;
  overflow: auto;
  padding: 16px;
  display: flex;
  flex-direction: column;
}

.debug-log-content.dd-log-surface {
  // 脚本运行输出和调试日志也属于日志窗口，复用统一滚动条增强；
  // 外层面板已经有边框，这里去掉重复边框和阴影，避免视觉变厚。
  border: 0;
  // 保持 0，不吃令牌：它是 .debug-log-panel 里贴边铺满的正文区（紧接 .panel-header 之下），
  // 圆角交给外层面板的 surface 档 + overflow: hidden 去裁；
  // 这里再吃一次 global.scss 给 .dd-log-surface 的 surface 档，
  // 会在面板直角的下半段露出两个内缩的圆角缺口。
  border-radius: 0;
  box-shadow: none;
}

.debug-error {
  margin-bottom: 12px;
}

.debug-logs {
  font-family: var(--dd-font-mono);
  font-size: 13px;
  line-height: 1.6;
  margin: 0;
  white-space: pre-wrap;
  word-break: break-all;
  // 必须用 --dd-log-text-color 而不是面板主题文字色：日志区底色来自 log_background_color，
  // 与面板明暗主题是两套独立取值，--dd-log-text-color 才是 panelAppearance 按日志底色亮度
  // 推导出来的配对前景色。写 --el-text-color-primary 会出现「浅色面板的深灰字压在深色日志底上」
  // ——这正是脚本调试弹窗日志看不清的原因（全站只有这一处写错）。
  // 兜底值保留面板文字色，覆盖变量还没挂上（首屏 applyPanelAppearance 之前）的一瞬间。
  color: var(--dd-log-text-color, var(--el-text-color-primary));
  flex: 1;
}

.debug-container.mobile {
  flex-direction: column;
  height: 100%;
  min-height: 0;
  max-height: none;

  .debug-code-panel,
  .debug-log-panel {
    flex: 1 1 0;
    min-height: 180px;
    max-height: none;
  }

  .panel-content {
    padding: 8px;
  }
}
</style>

<style lang="scss">
/*
  Element Plus 的 el-dialog 会 teleport 到 body，scoped 样式很难稳定命中根节点。
  这里用唯一 class 只作用于脚本调试/代码运行器，让桌面端和移动端都使用最大化工作区。
*/
.script-execution-fullscreen-dialog {
  display: flex;
  flex-direction: column;

  .el-dialog__header {
    flex-shrink: 0;
    padding: 14px 18px;
    margin: 0;
    border-bottom: 1px solid var(--el-border-color-lighter);
  }

  .el-dialog__body {
    flex: 1 1 0;
    min-height: 0;
    padding: 14px 18px;
    overflow: hidden;
  }

  .el-dialog__footer {
    flex-shrink: 0;
    padding: 12px 18px;
    border-top: 1px solid var(--el-border-color-lighter);
  }
}

/*
  打开时整屏工作区从底部滑入升起，比默认缩放更有"工作台升起"的高级感。

  ⚠️ 必须挂在 .el-overlay-dialog 上，不能挂在 .el-dialog 上。
     原来写的是 `.el-dialog.script-execution-fullscreen-dialog`，那条只在【元素首次创建】
     时跑一次：.el-dialog 不是 <transition> 的作用元素，而 EP 的 el-dialog 默认
     destroy-on-close=false，第二次打开同一个弹窗 DOM 被复用，animation 不会重放
     ——表现为「第一次打开有升起动效，之后就没有了」。浏览器实测确认过
     （第 2、3 次打开时 .el-dialog.getAnimations() 返回空数组）。
     .el-overlay-dialog 在 transition 的作用域里，每次 enter 都会重放。

  用 :has() 从容器层反选「里面装的是脚本工作区弹窗」的那一个。
  特异性 (0,3,0) 高于 global.scss 通用规则的 (0,2,0)，两条都带 !important 时按特异性判胜负。
  本 style 块是非 scoped 的（见文件顶部说明），选择器不带 data-v，能命中 teleport 出去的节点。
*/
.dialog-fade-enter-active .el-overlay-dialog:has(.script-execution-fullscreen-dialog) {
  animation: dd-script-sheet-up var(--dd-motion-page) var(--dd-ease-decelerate) !important;
  transform-origin: center bottom;
}

/*
  调试窗 / 代码运行器在桌面端铺满整个视口（issue #171，也是 #169 第 2 条的成因）。

  ① 桌面端 .route-shell 带 `contain: layout`（web/src/layouts/MainLayout.vue），它会成为后代
     position: fixed 元素的包含块。这两个弹窗是原地渲染的，「全屏」遮罩于是只盖住内容区，
     侧栏、顶栏还能点——调试窗开着就能走去别的页面，回来时它还盖在新打开的脚本上。
     这里只在弹窗开着时撤掉 containment：两个弹窗都带 destroy-on-close，关掉（离场动画结束）后
     .el-dialog 节点会被移除，:has 不再命中，containment 自动恢复，其它时候布局不受影响。
  ② 为什么不用 append-to-body：脚本页在 keep-alive 里，Vue 的 Teleport 在 keep-alive 停用时
     不会把已传送到 body 的内容收回，用浏览器后退离开时弹窗会浮在别的页面上，只能在停用时强制关掉、
     丢掉调试状态。原地渲染 + 这条规则能保住「离开再回来，调试窗和正在跑的调试都还在」。

  选择器必须带 `.layout-main >`：MainLayout 里那条是 scoped 的 `.route-shell[data-v-x]`，特异性 (0,2,0)，
  不加就同分、谁赢要看样式注入顺序；加上后是 (0,3,0)（:has 取参数的特异性）。
  移动端（≤768）.route-shell 本来就是 contain: none，这条规则在那里不起额外作用。
  规则必须写在这个非 scoped 块里：scoped 编译可能给 :has() 里的选择器也加上 data-v，就匹配不到弹窗了。
*/
.layout-main > .route-shell:has(.script-execution-fullscreen-dialog) {
  contain: none;
}

/*
  同一个包含块问题的另一处来源：调试窗开着时离开再回来，MainLayout 的切页进场动画
  （.page-shell-enter-active，带 transform 动画和 will-change: transform，约 280ms）会让页面根
  暂时成为 fixed 后代的包含块。实测弹窗先缩在内容区里，约 320ms 后才铺满，能看到一下跳动。
  弹窗开着时直接跳过这次进场动画：animation: none 时 Vue Transition 读到时长为 0，进场立刻结束。
  选择器带 `.route-shell >`：MainLayout 那条是 scoped 的 `.page-shell-enter-active[data-v-x]`（0,2,0），
  加上后是 (0,3,0)。不动 z-index / position，进场类只会多留一两帧。
*/
.route-shell > .page-shell-enter-active:has(.script-execution-fullscreen-dialog) {
  animation: none;
  will-change: auto;
}

/*
  不加 both / forwards：动画收尾后 transform 自然清空。
  否则残留 transform 会让 .el-overlay-dialog 成为编辑器内部 position:fixed 浮层
  （搜索面板/补全/悬浮提示）的包含块，导致弹层错位。to 为单位变换，视觉无跳变。
*/
@keyframes dd-script-sheet-up {
  from {
    opacity: 0;
    transform: translateY(40px) scale(0.985);
  }
  to {
    opacity: 1;
    transform: translateY(0) scale(1);
  }
}
</style>
