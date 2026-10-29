<script setup lang="ts">
import { ref, onMounted, onBeforeUnmount, watch } from "vue";
import { Compartment, EditorState } from "@codemirror/state";
import {
  EditorView,
  highlightActiveLine,
  highlightActiveLineGutter,
  highlightSpecialChars,
  highlightTrailingWhitespace,
  highlightWhitespace,
  keymap,
  lineNumbers,
} from "@codemirror/view";
import {
  defaultKeymap,
  history,
  historyKeymap,
  indentWithTab,
} from "@codemirror/commands";
import {
  bracketMatching,
  foldGutter,
  foldKeymap,
  indentOnInput,
  indentUnit,
} from "@codemirror/language";
import {
  highlightSelectionMatches,
  search,
  searchKeymap,
} from "@codemirror/search";
import {
  autocompletion,
  closeBrackets,
  closeBracketsKeymap,
  completionKeymap,
} from "@codemirror/autocomplete";
import {
  buildCodeEditorTheme,
  detectIndentWidth,
  resolveCodeEditorLanguage,
} from "@/utils/codeEditor";
import { indentGuides } from "@/utils/indentGuides";
import { selectionWhitespace } from "@/utils/selectionWhitespace";
import { createSearchPanel, searchPanelTheme } from "@/utils/searchPanel";
import { PANEL_APPEARANCE_CHANGE_EVENT } from "@/utils/panelAppearance";

/**
 * 代码编辑器的 CodeMirror 6 实现。
 *
 * 为什么不是 Monaco：Monaco 是自绘编辑器，文本画在 .view-lines 里、原生选择被关掉，
 * 触摸事件由它内部的手势层接管，所以「长按出系统菜单 / 双击出选择手柄 / 按住拖动光标」
 * 这三条在手机上**无论怎么配都做不到**。CodeMirror 的内容区是 contenteditable、
 * 选区就是原生 Selection，这三条天然可用。
 *
 * ⚠️ 正因如此，这里刻意**没有**启用 CodeMirror 的 drawSelection ——
 * 那个扩展会把原生选区藏起来改成自绘，等于把 Monaco 的毛病原样搬过来。
 * 后人要加多光标之类的功能时请先想清楚这一点。
 *
 * ⚠️ v3.2.2 起 Monaco 作为**可选的第二引擎**回来了（MonacoEditor.vue，由 CodeEditor.vue 分发），
 * 但上面这段理由一个字都没变：**本组件仍然是移动端唯一的选择**。
 * utils/editorEngine.ts 的 resolveEditorEngine 在 coarse pointer / 窄屏上是**硬回落**到这里的，
 * 别看到「现在有两个引擎了」就把那条回落改成可配 —— 那等于把上面三条能力当场收回去，
 * 而这正是 v3.2.0 换引擎的全部理由。
 */
const props = withDefaults(
  defineProps<{
    modelValue: string;
    language?: string;
    readonly?: boolean;
    minHeight?: string | number;
    /**
     * 撑满父容器高度。
     * 开启后外层容器改为 `flex: 1 1 auto; height: 100%`，高度由父级 flex 链决定，
     * `min-height` 退化为「下限」（默认 0，父级没有确定高度时可由调用方另行给下限）。
     */
    fillHeight?: boolean;
    /**
     * 自动换行。默认 'on'，与改造前一致，
     * 不传该 prop 的调用点（调试弹窗 / 代码运行器）行为零变化。
     */
    wordWrap?: "on" | "off";
    /**
     * 右侧代码缩略图 —— 🔴 **本组件不支持，这个 prop 是刻意的 no-op**（v3.2.4 / issue #116-2）。
     *
     * 缩略图只剩 Monaco 一侧（它原生就画，白送）。CodeMirror 侧那份是自己写的
     * utils/codeMinimap.ts，为了不碰 contentDOM、不碰原生选区，几何、拖动、明暗全得自己维护，
     * 收益却只是「右边多一条 64px 的形状图」，本版整个删掉了。
     *
     * 那为什么 prop 声明还留着？两个候选做法：
     *   A. 分发层改成 v-bind 一个「只有 monaco 分支才含 minimap」的对象；
     *   B. 这里留一个空声明，把分发层透传下来的值吃掉。
     * 选 B，理由是两条失效模式的**响度**不一样：
     *   - 走 A 的话，分发层就不再是「一个个显式 :xxx 往下传」了，vue-tsc 逐个查类型的能力
     *     在这个 prop 上当场消失（spec 里「刻意不写 v-bind="props"」写的就是这件事），
     *     将来再加 Monaco 专属选项时漏进那个对象是**静默**的。
     *   - 走 B 的话，spec 那条「三层 props 逐字相同」仍然字面成立，后人不用先学一个例外；
     *     真有人把这个声明删了，表现是 CodeMirror 根节点上多一个 `minimap="false"` 脏属性
     *     （Vue 会把不认识的 prop 当 attr 落到根上），一眼能看见，不会静默丢功能。
     * 所以：**别把这个声明删掉**，也别在这里「顺手实现一下缩略图」。
     */
    minimap?: boolean;
    /**
     * 缩进参考线。默认 false。
     * ⚠️ 下面这几个 prop 的默认值一律等于「加这些功能之前的行为」，
     * 所以不传它们的调用点（调试弹窗 / 代码运行器）一个像素都不会变
     * （注意它**不等于**偏好本身的默认值：参考线偏好默认是开的，由页面侧读出来再传进来）。
     */
    indentGuides?: boolean;
    /**
     * 显示空白符三态（issue #116-4）。默认 'none'。
     *   - 'all'：一直画（空格灰点、Tab 箭头，另外给行尾空白标底色）
     *   - 'selection'：只画选中范围内的（见 utils/selectionWhitespace.ts）
     *   - 'none'：不画
     */
    whitespace?: "none" | "selection" | "all";
    /**
     * 缩进宽度（issue #116-1/3）。默认 2，与改造前写死的 indentUnit("  ") + tabSize(2) 一致。
     * 'auto' 表示按文档内容检测（detectIndentWidth），页面侧的偏好默认就是 'auto'。
     */
    indentWidth?: "auto" | number;
  }>(),
  {
    wordWrap: "on",
    minimap: false,
    indentGuides: false,
    whitespace: "none",
    indentWidth: 2,
  },
);

const emit = defineEmits<{
  "update:modelValue": [value: string];
}>();

const editorRef = ref<HTMLElement>();
let view: EditorView | null = null;

// 每一个运行期可变的配置各占一个 Compartment。
// CodeMirror 的 extensions 只在 EditorState.create() 那一刻读一次，
// 想在挂载之后改就必须走 Compartment.reconfigure —— 直接改 prop 是**完全没反应且不报错**的，
// 构建和类型检查都发现不了（design-system.md 里专门为这条写过一则 Don't）。
// 新增可配项时「create 的 extensions + Compartment + watch」三处必须一起补。
const languageCompartment = new Compartment();
const readonlyCompartment = new Compartment();
const wordWrapCompartment = new Compartment();
const themeCompartment = new Compartment();
const indentGuidesCompartment = new Compartment();
const whitespaceCompartment = new Compartment();
const indentWidthCompartment = new Compartment();

// 只读必须两条一起给：
// EditorState.readOnly 挡住事务写入，EditorView.editable 关掉 contenteditable。
// 只写前者的话光标仍能落进去、手机输入法照样弹出来，用户会以为能改，改不动又不知道为什么。
function buildReadonlyExtension(readonly: boolean) {
  return [EditorState.readOnly.of(readonly), EditorView.editable.of(!readonly)];
}

/**
 * 光标横向跟随（issue #114-5）。
 *
 * 关掉自动换行后，用鼠标点 / 手指点把光标放到一处时，CodeMirror **不会**横向滚过去。
 * 这不是 bug 而是它的刻意设计：只有事务带 scrollIntoView 标志才滚，
 * 而指针驱动的选区变更明确把这个标志设成 false（来源是 mousedown / touchstart
 * 打的 "select.pointer" 标记）。键盘移动 / 输入 / 粘贴 / 撤销 / Ctrl+F 走的都是带标志的路径，
 * 本来就会滚，所以这里**只**认 select.pointer，不去和它们抢滚动位置。
 *
 * ⚠️ 刻意**不**用 `view.dispatch({effects: EditorView.scrollIntoView(...)})`：
 * 那条路会进 scrollRectIntoView，它一路往上遍历祖先滚动容器、最后可能落到 win.scrollBy()
 * —— 也就是整个页面被带着跳，而且 Y 轴也会动。我们只想动编辑器自己的横向滚动，
 * 所以直接写 .cm-scroller 的 scrollLeft。
 * 顺带一个关键好处：不发事务 ⇒ 不触发 docView.updateSelection ⇒ 原生选区和手机上的
 * 选择手柄一根手指都碰不到，与本组件「保住原生选区」的取舍零冲突。
 */
const CARET_REVEAL_X_MARGIN = 32;
const CARET_REVEAL_MEASURE_KEY = "dd-caret-reveal-x";

function measureCaretReveal(view: EditorView) {
  const scroller = view.scrollDOM;
  // 没有横向溢出就什么都不用干（自动换行开着时永远走这条）
  if (scroller.scrollWidth <= scroller.clientWidth) return null;
  const coords = view.coordsAtPos(view.state.selection.main.head);
  if (!coords) return null;

  const box = scroller.getBoundingClientRect();
  // 行号栏是 position: sticky 盖在内容上面的，左边界必须按它的右沿算，
  // 否则光标会被滚到行号底下。CodeMirror 自己走 scrollIntoView 时靠 gutter 提供的
  // EditorView.scrollMargins 补这一段，我们自己滚就得自己补。
  const gutter = scroller.querySelector<HTMLElement>(".cm-gutters");
  const leftBound = box.left + (gutter ? gutter.getBoundingClientRect().width : 0);

  if (coords.left < leftBound + CARET_REVEAL_X_MARGIN) {
    return coords.left - (leftBound + CARET_REVEAL_X_MARGIN);
  }
  if (coords.right > box.right - CARET_REVEAL_X_MARGIN) {
    return coords.right - (box.right - CARET_REVEAL_X_MARGIN);
  }
  // 中间一大块是死区：点在视野中央不该产生任何滚动
  return null;
}

const revealCaretOnPointerSelect = EditorView.updateListener.of((update) => {
  if (!update.selectionSet) return;
  if (!update.transactions.some((tr) => tr.isUserEvent("select.pointer"))) return;

  // 走 requestMeasure 而不是在这里直接量：这是 CodeMirror 自己的「量-写」两段式，
  // 量的那一阶段拿坐标是安全的，key 还能把同一帧内的重复请求合并掉。
  update.view.requestMeasure({
    key: CARET_REVEAL_MEASURE_KEY,
    read: measureCaretReveal,
    write: (delta, view) => {
      if (delta) view.scrollDOM.scrollLeft += delta;
    },
  });
});

// 换行开着时内容区是 overflowWrap: anywhere + flexShrink: 1，根本不会横向溢出，
// 横向跟随那条扩展纯属白装 —— 所以挂在同一个 compartment 里，跟着开关自动生效/失效，
// 不用再多加一个 Compartment 和一个 watch。
function buildWordWrapExtension(wordWrap: "on" | "off") {
  return wordWrap === "on"
    ? EditorView.lineWrapping
    : [revealCaretOnPointerSelect];
}

function buildIndentGuidesExtension(enabled: boolean) {
  return enabled ? indentGuides() : [];
}

// 三档都是 Decoration.mark：只往真实空格外面套一层 <span class="cm-highlightSpace">，
// 灰点是这个 span 的 background-image，不是 ::before content、不是 widget、不替换任何字符。
// 所以复制粘贴拿到的还是真空格，原生 Selection 完全不受影响。
// 与已启用的 highlightSpecialChars() 也不冲突：后者的默认字符集是「U+0000~U+0008」与
// 「U+000A~U+001F」两段（中间正好跳过 U+0009 也就是 Tab），也不含 U+0020 空格 ——
// 两边各管各的，同一个字符不会被两套装饰同时套上。
// 'selection' 档必须自己写：官方的 highlightWhitespace() 无参、只有「一直显示」这一档，
// 没有 Monaco renderWhitespace: 'selection' 的对应物（见 utils/selectionWhitespace.ts）。
// 行尾空白底色只挂在 'all' 上：它标的是「这里有多余空白」，与选区无关，跟着选区闪反而干扰。
function buildWhitespaceExtension(mode: "none" | "selection" | "all") {
  if (mode === "all") {
    return [highlightWhitespace(), highlightTrailingWhitespace()];
  }
  if (mode === "selection") {
    return [selectionWhitespace()];
  }
  return [];
}

/**
 * 缩进宽度 → 两条扩展（issue #116-1/3）。
 *
 * 改造前这两条是**写死**在 EditorState.create 的 extensions 里的（indentUnit.of("  ") +
 * EditorState.tabSize.of(2)），现在挂进 Compartment 才能运行期改。
 * indentUnit 管「按 Tab / 回车缩进时插入多少空格」，tabSize 管「已有的 \t 显示多宽」，
 * 两条都要给同一个值，只给一条的表现是「按出来的缩进和看到的对不上」。
 */
function buildIndentExtension(width: number) {
  return [indentUnit.of(" ".repeat(width)), EditorState.tabSize.of(width)];
}

/**
 * 解析出本次要用的缩进宽度。
 * 'auto' 走内容检测；数字直接用。
 * 页面只会传 2/4/6/8，但 prop 类型是 number，这里兜一下非法值 ——
 * " ".repeat(-1) 会直接抛 RangeError，把整个编辑器创建过程炸掉。
 */
function resolveIndentWidth(doc: string) {
  if (props.indentWidth === "auto") {
    return detectIndentWidth(doc);
  }
  return Number.isInteger(props.indentWidth) && props.indentWidth > 0
    ? props.indentWidth
    : 2;
}

// 明暗切换、以及用户在系统设置里改编辑器底色，都会走 PANEL_APPEARANCE_CHANGE_EVENT
// （切主题那一路见 stores/theme.ts：toggle 完立刻 applyPanelAppearance()）。
// 底色本身是 CSS 变量、不用管；但行号色 / 选区色 / 语法配色是按明暗二选一的，必须重建主题。
function syncEditorTheme() {
  view?.dispatch({
    effects: themeCompartment.reconfigure(buildCodeEditorTheme()),
  });
}

/**
 * 行尾规范成 LF，规则与 CodeMirror 6 自己切行的规则一致（@codemirror/state 的 DefaultSplit 是 /\r\n?|\n/）。
 *
 * 本组件没配 EditorState.lineSeparator，所以 CRLF / 单 CR 的内容一灌进来就被按行切开，
 * doc.toString() 再一律用 \n 拼回 —— 从编辑器里读出来的永远是 LF。
 * 凡是拿「编辑器当前文本」和 props.modelValue 判等、再据此决定要不要 emit 的地方，
 * 都得先把 modelValue 过一遍这里，否则一打开 CRLF 文件就会被当成「用户改过」。
 * ⚠️ 别改成配 lineSeparator: "\r\n" 让 CM 原样保留 CRLF：那会连带改变编辑后保存出去的行尾
 * （现在是整份存成 LF），不是这里要动的东西。
 */
function normalizeLineBreaks(value: string) {
  return value.replace(/\r\n?/g, "\n");
}

function replaceDoc(value: string) {
  if (!view) return;
  // 这里刻意原样判等、不走 normalizeLineBreaks：外部值是 CRLF 时（切到 CRLF 文件、放弃改动回到 CRLF 原文）
  // 这条永远不相等，一定走下面的整篇替换，不会有哪次外部变化被判等挡掉；
  // 就算规范后恰好和当前文档一样，也只是多替换一次、显示不变。
  // 这次替换也不会回写给父组件：updateListener 是规范后判等的，相等就不 emit。
  if (view.state.doc.toString() === value) return;
  // 外部整体换文档（脚本页切文件、拉到内容后首次灌入、setValue）时，
  // 「自动」档必须按新内容重新检测一次缩进宽度 —— 这是 issue #116-1 的关键一环：
  // 编辑器是先挂载、后 await 拉内容的，create 那一刻文档还是空串，测不出任何缩进。
  // 用户自己打字触发的那次会在上面那条判等提前 return，走不到这里，这正是想要的：
  // 边打字边重测既没意义，又会在每次输入时白重配一遍 state。
  const effects =
    props.indentWidth === "auto"
      ? indentWidthCompartment.reconfigure(
          buildIndentExtension(detectIndentWidth(value)),
        )
      : undefined;
  view.dispatch({
    changes: { from: 0, to: view.state.doc.length, insert: value },
    effects,
  });
}

onMounted(() => {
  window.addEventListener(PANEL_APPEARANCE_CHANGE_EVENT, syncEditorTheme);
  if (!editorRef.value) return;

  view = new EditorView({
    parent: editorRef.value,
    state: EditorState.create({
      doc: props.modelValue,
      extensions: [
        lineNumbers(),
        highlightActiveLineGutter(),
        highlightActiveLine(),
        highlightSpecialChars(),
        history(),
        indentOnInput(),
        bracketMatching(),
        // 下面这三条是 Monaco 时代默认就开着、换引擎时被顺手丢掉的编辑能力。
        // 它们都不碰选区绘制，与本组件「保住原生选区」的取舍毫无冲突，所以补回来：
        //   - foldGutter：行号右侧的折叠箭头。它自带 codeFolding()（见 @codemirror/language
        //     的 foldGutter 实现），不用再单独引折叠状态字段；放在 lineNumbers() 之后
        //     才会渲染在行号右侧，顺序不能随便挪。
        //   - closeBrackets：输入 ( [ { " ' 时自动补上右半边。
        //   - autocompletion：补全弹窗。补全项来自各语言扩展注册的 language data
        //     （js/ts/python/json/html/css 有，shell 走的是 legacy-modes 流式模式、没有源，
        //     表现就是不弹框，属于正常）。
        foldGutter(),
        closeBrackets(),
        autocompletion(),
        // 查找面板换成自己的中文版（issue #170，见 utils/searchPanel.ts）：
        // 正常字号、匹配计数「第 N 个，共 M 个」、去掉多选区画不出来的「all」。
        // 面板样式在 searchPanelTheme 里，两条要一起挂。
        search({ top: true, createPanel: createSearchPanel }),
        searchPanelTheme,
        highlightSelectionMatches(),
        keymap.of([
          // closeBracketsKeymap 只有一条 Backspace（在 `()` 正中间时把整对一起删掉），
          // 必须排在 defaultKeymap 前面，否则 Backspace 会先被 defaultKeymap 接走、这条永远不生效。
          // 它在非成对场景返回 false 会继续往下派发，所以不会挤掉 defaultKeymap 的删除行为。
          ...closeBracketsKeymap,
          ...defaultKeymap,
          ...historyKeymap,
          ...searchKeymap,
          // 折叠快捷键（Ctrl-Shift-[ / ]、Ctrl-Alt-[ / ]），与上面几套没有键位冲突
          ...foldKeymap,
          // completionKeymap 排在最后是安全的：autocompletion() 内部已经用 Prec.highest
          // 装了同一份绑定（默认 defaultKeymap: true），补全弹窗开着时 Enter / ↑↓ 一定先走它，
          // 不会被 defaultKeymap 的换行吃掉。这里再显式列一次是与官方 basicSetup 对齐，
          // 也兜住「日后给 autocompletion 传 defaultKeymap: false」时快捷键整套消失的情况。
          ...completionKeymap,
          indentWithTab,
        ]),
        // 移动端必补的三条：手机输入法默认句首大写 + 自动纠错 + 拼写检查，
        // 会**静默改坏脚本内容**（把 const 改成 Const、把变量名纠成英文单词），
        // 而且用户根本不会意识到是输入法干的。
        EditorView.contentAttributes.of({
          spellcheck: "false",
          autocapitalize: "off",
          autocorrect: "off",
        }),
        EditorView.updateListener.of((update) => {
          if (!update.docChanged) return;
          const value = update.state.doc.toString();
          // 和外部 modelValue 相同，说明这次变更是下面那个 watch 同步进来的，
          // 再 emit 一次就和父组件转成回环了。
          // modelValue 必须先规范行尾再比（见 normalizeLineBreaks）：CRLF 文件灌进来后 value 是 LF，
          // 原样比永远不等，会把 LF 回写给父组件 —— 脚本页的「未保存」圆点、离开确认、
          // 挡掉升级后的自动刷新就全误报了。用户真改了内容时规范后仍不等，照常 emit。
          if (value === normalizeLineBreaks(props.modelValue)) return;
          // 每次输入即时 emit，不能等失焦：
          // 「未保存」角标、保存按钮 disabled、调试弹窗的 markDebugCodeChanged 全靠它。
          emit("update:modelValue", value);
        }),
        languageCompartment.of(resolveCodeEditorLanguage(props.language)),
        readonlyCompartment.of(buildReadonlyExtension(props.readonly || false)),
        wordWrapCompartment.of(buildWordWrapExtension(props.wordWrap)),
        indentGuidesCompartment.of(buildIndentGuidesExtension(props.indentGuides)),
        whitespaceCompartment.of(buildWhitespaceExtension(props.whitespace)),
        indentWidthCompartment.of(
          buildIndentExtension(resolveIndentWidth(props.modelValue)),
        ),
        themeCompartment.of(buildCodeEditorTheme()),
      ],
    }),
  });
});

watch(
  () => props.modelValue,
  (newValue) => {
    replaceDoc(newValue);
  },
);

// 脚本页切换文件会换语言，必须重配，否则一直停在第一次打开那个文件的高亮上
watch(
  () => props.language,
  (newLanguage) => {
    view?.dispatch({
      effects: languageCompartment.reconfigure(
        resolveCodeEditorLanguage(newLanguage),
      ),
    });
  },
);

// 漏了这条就是「只读态还能改」：改完污染 hasChanges，退出编辑时弹出幽灵「有未保存改动」
watch(
  () => props.readonly,
  (newReadonly) => {
    view?.dispatch({
      effects: readonlyCompartment.reconfigure(
        buildReadonlyExtension(newReadonly || false),
      ),
    });
  },
);

// 漏了这条就是「点了自动换行按钮没反应」，且不报错、构建与类型检查都发现不了
watch(
  () => props.wordWrap,
  (newWordWrap) => {
    view?.dispatch({
      effects: wordWrapCompartment.reconfigure(
        buildWordWrapExtension(newWordWrap),
      ),
    });
  },
);

// 下面几条同理：新增可配项必须「create 的 extensions + Compartment.of + watch 里 reconfigure」
// 三处齐全，缺一条就是静默失效（点了开关没反应、不报错、构建与 vue-tsc 全绿）
watch(
  () => props.indentGuides,
  (enabled) => {
    view?.dispatch({
      effects: indentGuidesCompartment.reconfigure(
        buildIndentGuidesExtension(enabled),
      ),
    });
  },
);

watch(
  () => props.whitespace,
  (mode) => {
    view?.dispatch({
      effects: whitespaceCompartment.reconfigure(buildWhitespaceExtension(mode)),
    });
  },
);

// 缩进宽度有**三个**重算时机，这是其中之二：① 建 state 时（见上面 create 的 extensions）、
// ② 用户在菜单里改了这一项（本 watch）。③ 是外部整体换文档，在 replaceDoc 里。
// 注意这里读的是 view 里当下的文档而不是 props.modelValue：两者在同一帧里可能还没同步，
// 而「自动」档要按编辑器**真正显示着的**内容来测。
watch(
  () => props.indentWidth,
  () => {
    if (!view) return;
    view.dispatch({
      effects: indentWidthCompartment.reconfigure(
        buildIndentExtension(resolveIndentWidth(view.state.doc.toString())),
      ),
    });
  },
);

onBeforeUnmount(() => {
  window.removeEventListener(PANEL_APPEARANCE_CHANGE_EVENT, syncEditorTheme);
  view?.destroy();
  view = null;
});

defineExpose({
  focus: () => view?.focus(),
  getValue: () => view?.state.doc.toString() || "",
  setValue: (value: string) => replaceDoc(value),
  // 全仓没有调用点：格式化走后端 scriptApi.format，前端不做本地格式化。
  // 保留同名方法只是为了不改动对外契约，故意留空。
  format: () => {},
});

function resolveMinHeight(value: string | number | undefined) {
  if (typeof value === "number") {
    return `${value}px`;
  }
  if (typeof value === "string" && value.trim()) {
    return value;
  }
  // 撑满模式下高度由父级决定，默认不设下限；普通模式仍需要一个固定高度兜底
  return props.fillHeight ? "0px" : "400px";
}
</script>

<template>
  <div
    class="code-editor-wrapper"
    :class="{ 'code-editor-wrapper--fill': props.fillHeight }"
    :style="{ '--code-editor-min-height': resolveMinHeight(props.minHeight) }"
  >
    <div ref="editorRef" class="code-editor-container"></div>
  </div>
</template>

<style scoped>
/* ⚠️ 根 class 与 --code-editor-min-height 这个变量名都是**对外契约**，
   不能因为文件从 CodeEditor.vue 拆出来了就顺手改成 code-mirror-*：
   config-file/index.vue 有一条 `:deep(.code-editor-wrapper) { min-height: 560px }`
   靠它给窄屏兜下限，改名是构建与类型检查都发现不了的静默失效。
   MonacoEditor.vue 出于同样的理由用的也是这一套类名。 */
.code-editor-wrapper {
  width: 100%;
  min-height: var(--code-editor-min-height, 400px);
  height: var(--code-editor-min-height, 400px);
  position: relative;
  /* 编辑器基准字号写在这里而不是主题里：下面那条移动端媒体查询只要改这一处，
     行号、正文、滚动条度量会一起跟着变，不会出现字大了行号还是小的错位。 */
  font-size: 14px;
}

/* 双类选择器：靠特异性压过上面的固定高度，不依赖样式表书写顺序 */
.code-editor-wrapper.code-editor-wrapper--fill {
  flex: 1 1 auto;
  height: 100%;
}

.code-editor-container {
  width: 100%;
  height: 100%;
  /* 普通卡片没有固定父级高度时，内层容器必须继承最小高度，否则会被算成 0 高度，
     只剩一条横线且无法输入。 */
  min-height: inherit;
  overflow: hidden;
  background: var(--dd-editor-bg-color, #111827);
  /* 固定 0、不吃 --dd-radius-*：这是一整块代码面，四角由外层卡片自己裁，
     这里再加圆角只会和卡片的圆角叠出双层圆角。改造前也是 0，保持不变。 */
  border-radius: 0;
}

.code-editor-container :deep(.cm-editor) {
  height: 100%;
}

/* 触摸滚动惯性：手机上编辑长脚本时没有它会明显发涩 */
.code-editor-container :deep(.cm-scroller) {
  -webkit-overflow-scrolling: touch;
}

/* 折叠箭头：字色继承 .cm-gutters（主题里按明暗算好的行号色），
   这里只压一点不透明度，免得比行号本身还抢眼。故意不写具体颜色 —— 编辑器底色是用户可配项，
   写死的颜色在深色底或自定义底色上会串色。 */
.code-editor-container :deep(.cm-foldGutter span) {
  opacity: 0.65;
}

.code-editor-container :deep(.cm-foldGutter span:hover) {
  opacity: 1;
}

/* 折叠后的「…」占位块。必须自己覆盖：CodeMirror 基础主题把它写死成浅色
   （#eee 底 + #ddd 边 + #888 字，见 @codemirror/language 的 baseTheme），
   深色编辑器上会糊出一块亮斑。改成透明底 + currentColor 描边后，
   颜色完全跟着 --dd-editor-fg-color 走，明暗与用户自定义底色都不会串。 */
.code-editor-container :deep(.cm-foldPlaceholder) {
  background: transparent;
  border: 1px solid currentColor;
  color: inherit;
  opacity: 0.7;
}

/* iOS Safari 在可编辑区字号小于 16px 时，聚焦会自动把整页放大。
   index.html 的 viewport 刻意不写 maximum-scale（安卓上它会真的禁掉双指缩放，不可接受）；
   iOS 由 main.ts 在运行时追加 maximum-scale=1.0 挡住自动放大（iOS 10 起不影响双指缩放）。
   这里把移动端编辑器字号提到 16px 照留，作为不依赖 UA 判定的兜底：UA 认不出的设备也不会被放大。 */
@media (max-width: 768px) {
  .code-editor-wrapper {
    font-size: 16px;
  }

  /* 查找面板（utils/searchPanel.ts）的输入框同理提到 16px：桌面端是 13px，iOS 上一聚焦就会放大整页。
     窄屏下输入框不设上限、占满一行，按钮折到下一行（行容器本身是 flex-wrap），不会撑出横向滚动。 */
  .code-editor-wrapper :deep(.cm-dd-search-input) {
    max-width: none;
    height: 32px;
    font-size: 16px;
  }
}
</style>
