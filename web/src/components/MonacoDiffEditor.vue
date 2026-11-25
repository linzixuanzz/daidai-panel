<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref, shallowRef, watch } from "vue";
import {
  defineMonacoTheme,
  resolveCodeEditorFontFamily,
  resolveMonacoLanguage,
} from "@/utils/codeEditor";
import { PANEL_APPEARANCE_CHANGE_EVENT } from "@/utils/panelAppearance";
import { MonacoLoadErrorOverlay } from "@/utils/monacoWarmup";
// ⚠️ 只能是 `import type`：它会被 TS 完全擦掉、不产生运行时导入。
// 漏掉 type 关键字就等于给本文件加了一条对 monacoEngine 的静态 import，
// monaco chunk 当场被提升进首屏——构建全绿、页面能用，纯静默劣化。
// 真正的取用只有下面 buildEditor() 里那一句 `await import('@/utils/monacoEngine')`。
import type { MonacoApi } from "@/utils/monacoEngine";

/**
 * 版本对比编辑器的 **Monaco 实现**。
 *
 * 它不直接被页面使用：调用点用的是 CodeDiffEditor.vue，那是个按引擎偏好选实现的分发层，
 * 本组件与 CodeMirrorDiffEditor.vue 是它的两个可选后端。
 *
 * ⚠️ 对外 props（名字、类型、默认值）必须与 CodeMirrorDiffEditor.vue **逐字一致**：
 * 分发层是逐个显式往下传的，两边对不上不会报错，只会在切到某个引擎时静默丢掉一个开关。
 *
 * ⚠️ 本组件**永远不会**被发到触摸设备上（utils/editorEngine.ts 里粗指针是硬回落 CodeMirror），
 * 所以这里不做任何移动端适配：没有 iOS 放大规避、没有统一视图的手势考量。
 */
const props = withDefaults(
  defineProps<{
    originalValue: string;
    modifiedValue: string;
    language?: string;
    readonly?: boolean;
    renderSideBySide?: boolean;
    ignoreTrimWhitespace?: boolean;
    hideUnchangedRegions?: boolean;
    contextLineCount?: number;
  }>(),
  {
    language: "plaintext",
    readonly: true,
    renderSideBySide: true,
    ignoreTrimWhitespace: false,
    hideUnchangedRegions: false,
    contextLineCount: 3,
  },
);

// 从 API 类型上取实例类型，避免 import 具体的类型名（那些名字散在 monaco 的内部路径里，
// 而本文件只允许出现 monacoEngine 这一个说明符），同时也不用退化成 any。
type MonacoDiffEditorInstance = ReturnType<MonacoApi["editor"]["createDiffEditor"]>;
type MonacoTextModel = ReturnType<MonacoApi["editor"]["createModel"]>;

const containerRef = ref<HTMLElement>();
/**
 * 外层容器的底色。
 *
 * CSS 里已经给了 `var(--dd-editor-bg-color, ...)` 兜底，但那条兜底值是写死的深色，
 * 而「用户没自定义过底色」时真正的默认色要按面板明暗算（见 defineMonacoTheme）。
 * Monaco 是 `await import()` 之后才挂上来的，中间这段空窗期容器是裸露的，
 * 不对齐的话浅色面板下会先闪一块深色。拿到主题描述符后用它的 background 覆盖掉。
 */
const wrapperBackground = ref("");

/**
 * monacoEngine 动态 import 的失败原因；非 null 时在对比区上盖一层失败态（#126），
 * 给「重试」与「改用 CodeMirror」两个出口，与分发层的失败态是同一个组件。
 * 原来这里没有 try/catch，chunk 一失败对比区就永远空白。
 */
const loadError = shallowRef<unknown>(null);

let monacoApi: MonacoApi | null = null;
let diffEditor: MonacoDiffEditorInstance | null = null;
let originalModel: MonacoTextModel | null = null;
let modifiedModel: MonacoTextModel | null = null;
/**
 * 构建轮次令牌。
 * 加载 Monaco 是异步的，等 import 回来时组件可能已经卸载、或者 props 又变了触发了新一轮构建。
 * destroyEditor() 每次都推进它，回来的那一轮对不上号就自行丢弃，不去碰已经不属于自己的容器。
 */
let buildToken = 0;

/**
 * 应用主题。
 *
 * defineTheme 用同名覆盖，Monaco 会自动刷新正在用这个主题的实例（所以「只改了编辑器底色」
 * 这种情况到这一步就够了）；但明暗切换时主题名会变（dd-editor-dark ⇄ dd-editor-light），
 * 那种情况必须再 setTheme 一次，否则新主题定义了却没被用上。两句都写才算覆盖完整。
 *
 * setTheme 是**全局**的：页面上同时开着的其它 Monaco 实例会跟着换，这正是我们要的效果。
 */
function applyTheme() {
  if (!monacoApi) return;
  const { themeName, background } = defineMonacoTheme(monacoApi);
  monacoApi.editor.setTheme(themeName);
  wrapperBackground.value = background;
  // 切换主题后同步刷新等宽字体栈，与 MonacoEditor 保持一致
  diffEditor?.updateOptions({ fontFamily: resolveCodeEditorFontFamily() });
}

function destroyEditor() {
  buildToken += 1;
  // 先拆编辑器再拆 model：反过来的话编辑器还挂着已经 dispose 的 model，
  // 它内部的监听器会在拆解过程中读到失效对象。
  diffEditor?.dispose();
  diffEditor = null;
  // model 是我们自己 createModel 出来的，Monaco 不会替我们回收；
  // 漏掉这两句的表现是每对比一次版本就永久泄漏两份文本（外加 JSON 语言服务那边的一份 worker 副本）。
  originalModel?.dispose();
  originalModel = null;
  modifiedModel?.dispose();
  modifiedModel = null;
}

/**
 * 整体重建。
 *
 * 取舍与 CodeMirrorDiffEditor.vue 一致：本组件是只读的一次性对比视图，
 * 每次 prop 变化基本都意味着换了一对要比的内容（选了另一个历史版本、切了并排/内联布局），
 * 整体重建比对着 updateOptions / setModel 维护一堆增量更新简单，代价只有滚动位置。
 */
async function buildEditor() {
  destroyEditor();
  const token = buildToken;

  loadError.value = null;
  // 与 MonacoEditor.vue 同一套兜底：经分发层挂进来时 monacoEngine 已被它的 loader 拉好，这一发当场 resolve；
  // 仍然失败时盖一层失败态，而不是让对比区永远空白。取成员也放在 try 里，理由见 MonacoEditor.vue 的 mountEditor。
  let monaco: MonacoApi;
  try {
    const engineModule = await import("@/utils/monacoEngine");
    monaco = engineModule.monaco;
  } catch (error) {
    // 只有仍是最新一轮构建时才显示：已卸载、或 props 又变了触发了新一轮时，这一轮的结果没人要了
    if (token === buildToken) {
      loadError.value = error;
      console.warn("Monaco 对比编辑器加载失败", error);
    }
    return;
  }
  // 卸载守卫：上面 await 期间可能已经卸载或又发起了新一轮构建
  const container = containerRef.value;
  if (token !== buildToken || !container) return;

  monacoApi = monaco;
  const { themeName, background } = defineMonacoTheme(monaco);
  wrapperBackground.value = background;

  const language = resolveMonacoLanguage(props.language);
  originalModel = monaco.editor.createModel(props.originalValue, language);
  modifiedModel = monaco.editor.createModel(props.modifiedValue, language);

  diffEditor = monaco.editor.createDiffEditor(container, {
    // 左侧是历史版本，任何时候都不可编辑；readOnly 只管右侧（当前版本）
    originalEditable: false,
    readOnly: props.readonly,
    // false 就是内联（统一）视图，对应 CodeMirror 侧的 unifiedMergeView
    renderSideBySide: props.renderSideBySide,
    ignoreTrimWhitespace: props.ignoreTrimWhitespace,
    // 对应 CodeMirror 侧的 collapseUnchanged：把没改动的大段折起来，上下各留几行上下文
    hideUnchangedRegions: {
      enabled: props.hideUnchangedRegions,
      contextLineCount: props.contextLineCount,
    },
    // 滚动条上的红绿差异标记（issue #114-4 那条诉求）。默认就是 true，
    // 显式写出来是为了让「这条诉求在 Monaco 侧是原生能力」一眼可见 ——
    // 也提醒后人别把 CodeMirror 侧那把自绘标尺移植过来，两条会打架。
    renderOverviewRuler: true,
    automaticLayout: true,
    // 与 CodeMirror 侧的 EditorView.lineWrapping 对齐。
    // 差异编辑器的 diffWordWrap 默认是 'inherit'，会跟着这条走，不用另给。
    wordWrap: "on",
    // 与 MonacoEditor.vue 补齐一致的字号与等宽字体栈
    fontSize: 14,
    fontFamily: resolveCodeEditorFontFamily(),
    // ⚠️ 不要在这里给 minimap: { enabled: true }。
    // Monaco 的 _adjustOptionsForSubEditor 会在两侧子编辑器上无条件把
    // clonedOptions.minimap.enabled 设成 false，传了也会被静默覆盖。
    // 「编辑器选项」里那个缩略图开关只对普通编辑器生效，对比视图本来就没有缩略图。
  });
  monaco.editor.setTheme(themeName);
  diffEditor.setModel({ original: originalModel, modified: modifiedModel });
}

/** 失败态里的「重试」：整体重建一遍（buildEditor 会重新 import，并用轮次令牌挡住并发的那一轮）。 */
function retryBuild() {
  void buildEditor();
}

onMounted(() => {
  // 明暗切换与「自定义编辑器底色」都走这个事件（见 stores/theme.ts 与 utils/panelAppearance.ts）。
  // 这里只换主题、不重建实例：Monaco 支持在线换主题，没必要像 CodeMirror 侧那样整体重建。
  window.addEventListener(PANEL_APPEARANCE_CHANGE_EVENT, applyTheme);
  void buildEditor();
});

watch(
  [
    () => props.originalValue,
    () => props.modifiedValue,
    () => props.language,
    () => props.readonly,
    () => props.renderSideBySide,
    () => props.ignoreTrimWhitespace,
    () => props.hideUnchangedRegions,
    () => props.contextLineCount,
  ],
  () => {
    void buildEditor();
  },
);

onBeforeUnmount(() => {
  window.removeEventListener(PANEL_APPEARANCE_CHANGE_EVENT, applyTheme);
  destroyEditor();
});
</script>

<template>
  <div
    class="code-diff-wrapper"
    :style="wrapperBackground ? { background: wrapperBackground } : undefined"
  >
    <div ref="containerRef" class="code-diff-container"></div>
    <!-- monacoEngine 没加载成时盖一层失败态（重试 / 改用 CodeMirror），与分发层的失败态是同一个组件 -->
    <MonacoLoadErrorOverlay
      v-if="loadError !== null"
      :error="loadError"
      :retry="retryBuild"
    />
  </div>
</template>

<style scoped>
.code-diff-wrapper {
  width: 100%;
  height: 100%;
  min-height: 420px;
  position: relative;
  overflow: hidden;
  background: var(--dd-editor-bg-color, #111827);
}

/* ⚠️ 必须是 100%，不是 CodeMirror 侧那个 calc(100% - 10px)：
   那 10px 是给它自绘的差异总览标尺留的槽，而 Monaco 的标尺（renderOverviewRuler）
   画在编辑器内部。照抄过来的表现是右边多一条 10px 的死带。 */
.code-diff-container {
  width: 100%;
  height: 100%;
}
</style>
