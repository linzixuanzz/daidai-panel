<script setup lang="ts">
import { ref, shallowRef, onMounted, onBeforeUnmount, watch } from "vue";
import {
  defineMonacoTheme,
  detectIndentWidth,
  resolveCodeEditorFontFamily,
  resolveMonacoLanguage,
} from "@/utils/codeEditor";
import { PANEL_APPEARANCE_CHANGE_EVENT } from "@/utils/panelAppearance";
import { MonacoLoadErrorOverlay } from "@/utils/monacoWarmup";
// ⚠️ 只能是 `import type`：MonacoApi 是纯类型，type 关键字保证这一行被 TS 完全擦掉、
// 不产生任何运行时导入。漏掉它就等于给本文件加了一条对 monacoEngine 的静态 import，
// monaco chunk 会被 Rollup 提升成入口的静态依赖 —— 构建全绿、页面能用，纯静默劣化。
// 真正的 Monaco 实例只在 onMounted 里通过 `await import('@/utils/monacoEngine')` 取。
import type { MonacoApi } from "@/utils/monacoEngine";

/**
 * 代码编辑器的 Monaco 实现（v3.2.2 起的**可选**第二引擎）。
 *
 * 定位：CodeMirror 6（CodeMirrorEditor.vue）仍是默认引擎与**移动端唯一**引擎。
 * Monaco 是自绘编辑器，触摸事件由它内部的手势层接管，
 * 「长按出系统菜单 / 双击出选择手柄 / 按住拖动光标」三条无论怎么配都做不到 ——
 * 这正是 v3.2.0 换引擎的全部理由，别在这里想办法「补回来」，补不了。
 * 它换来的是多光标、列选、代码折叠、JSON 语言服务那套重型能力，给桌面端用户按需选。
 *
 * 对外 props / emit / defineExpose 与 CodeMirrorEditor.vue **逐字相同**：
 * 两个实现是同一个契约的两份实现，由 CodeEditor.vue 按引擎偏好分发，
 * 调用点感知不到自己拿到的是哪一个。任何一边加 prop，另一边必须同步加，
 * 否则表现是「切个引擎某个开关就没反应了」，不报错。
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
    /** 自动换行。默认 'on'，与 CodeMirror 侧一致。 */
    wordWrap?: "on" | "off";
    /**
     * 右侧代码缩略图。默认 false。
     * 🔴 v3.2.4 起这一项**只有本引擎真的实现**：CodeMirror 侧那份自绘缩略图
     * （utils/codeMinimap.ts）已经删掉，那边的同名 prop 是刻意留空的 no-op。
     * 这是「两个引擎 props 逐字相同」这条契约唯一开的例外，理由写在 CodeMirrorEditor.vue
     * 那个 prop 的注释里。Monaco 的缩略图是它自己原生画的，白送，没有删的理由。
     */
    minimap?: boolean;
    /** 缩进参考线。默认 false（偏好本身的默认值是开，由页面侧决定） */
    indentGuides?: boolean;
    /**
     * 显示空白符三态（issue #116-4）。默认 'none'。
     * 取值与 Monaco 原生的 renderWhitespace 逐字相同，直接透传下去。
     */
    whitespace?: "none" | "selection" | "all";
    /**
     * 缩进宽度（issue #116-1/3）。默认 2，与改造前写死的 tabSize: 2 一致。
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

// 从 create() 的返回值反推实例类型，而不是 import Monaco 的 IStandaloneCodeEditor：
// 全仓只有 utils/monacoEngine.ts 可以出现 `from 'monaco-editor/...'`（见那个文件顶部）。
type MonacoStandaloneEditor = ReturnType<MonacoApi["editor"]["create"]>;
// 同理再从 getModel() 反推模型类型（去掉它的 null 分支），不去 import Monaco 的 ITextModel。
type MonacoTextModel = NonNullable<ReturnType<MonacoStandaloneEditor["getModel"]>>;

const editorRef = ref<HTMLElement>();
let editor: MonacoStandaloneEditor | null = null;
// 存下 monaco 命名空间：换语言（setModelLanguage）与换主题（defineTheme/setTheme）
// 都是挂在命名空间上的模块级函数，不是实例方法，拿不到实例就够。
let monacoApi: MonacoApi | null = null;
// onDidChangeModelContent 的订阅句柄。类型写成结构化最小形状，理由同上（不引 IDisposable）。
let contentSubscription: { dispose(): void } | null = null;
// model.onDidChangeOptions 的订阅句柄，即下面那个「缩进宽度自愈守卫」。类型同上。
let modelOptionsSubscription: { dispose(): void } | null = null;
// 组件是否已卸载。mountEditor 是 async 的，见它 create 前那道守卫。
let destroyed = false;
// monacoEngine 那一发 import 正在路上。连点「重试」时挡住第二发：两次 await 回来会各 create 一个实例，
// 先建的那个没人 dispose。
let loadingEngine = false;

/**
 * monacoEngine 动态 import 的失败原因；非 null 时在编辑区上盖一层失败态（#126）。
 * 原来这里没有 try/catch：chunk 一失败（升级后的旧页面、网络抖动、升级进行到一半）编辑区就永远空白，
 * 没有提示也没有出路。现在给「重试」与「改用 CodeMirror」两个出口，与分发层的失败态是同一个组件。
 * 用 shallowRef：Error 对象不需要做成深层响应式。
 */
const loadError = shallowRef<unknown>(null);

/**
 * 本实例**期望**的缩进宽度，也就是自愈守卫判断「model 被别人改坏了」的唯一基准。
 * 每一条会写 model tabSize 的路径都必须顺带更新它：
 * create 的 options 那一次在 onMounted 里显式对齐，其余全部走 applyIndentWidth。
 * 初值 2 与 props.indentWidth 的默认值一致（也就是加这个功能之前两个引擎写死的值）。
 */
let desiredIndentWidth = 2;

/**
 * 定义并应用主题。
 *
 * ⚠️ 必须整套重来，不能只在「明暗翻转了」时才跑：
 * Monaco 的 colors 表只吃字面 hex，塞不进 `var(--dd-editor-bg-color, ...)`
 * （塞了会被解析成 Color.red，一片正红），所以底色是被**烘进**主题数据里的。
 * 用户只改了编辑器底色、没切明暗时，CodeMirror 侧靠 var() 自动重绘、主题都不用动，
 * Monaco 侧却必须重新 defineTheme —— 少了这一步的表现是「设置里改了底色，
 * Monaco 编辑器纹丝不动」。详见 utils/codeEditor.ts 的 defineMonacoTheme 注释。
 *
 * defineTheme 同名覆盖后 Monaco 会自动刷新正在用该主题的实例；
 * 但明暗翻转时主题名会变（dd-editor-dark ⇄ dd-editor-light），所以 setTheme 那句不能省。
 */
function applyEditorTheme(monaco: MonacoApi) {
  const { themeName } = defineMonacoTheme(monaco);
  monaco.editor.setTheme(themeName);
  // 切主题或自定义 CSS 更新 --dd-font-mono 时同步刷新编辑器字体栈
  editor?.updateOptions({ fontFamily: resolveCodeEditorFontFamily() });
}

// 明暗切换、以及用户在系统设置里改编辑器底色，都会走 PANEL_APPEARANCE_CHANGE_EVENT
// （切主题那一路见 stores/theme.ts：toggle 完立刻 applyPanelAppearance()）。
function syncEditorTheme() {
  if (!monacoApi) return;
  applyEditorTheme(monacoApi);
}

/**
 * 解析出本次要用的缩进宽度。与 CodeMirrorEditor.vue 的同名函数逐字同构。
 * 'auto' 走内容检测；数字直接用，非法值兜回 2。
 */
function resolveIndentWidth(doc: string) {
  if (props.indentWidth === "auto") {
    return detectIndentWidth(doc);
  }
  return Number.isInteger(props.indentWidth) && props.indentWidth > 0
    ? props.indentWidth
    : 2;
}

/**
 * 应用缩进宽度。**两处都要写**：
 * tabSize 既是编辑器选项、又是**模型**选项，而真正决定「\t 显示多宽 / 一级缩进插几个空格」的
 * 是挂在模型上的那份。只调 editor.updateOptions 的表现是「改了没反应」，不报错。
 * indentSize 跟着 tabSize 走（Monaco 允许两者不同，我们这个面板没有分开配的需求）。
 *
 * 🔴 但第一句 editor.updateOptions({ tabSize }) 同时是一个**全进程副作用**，别当冗余删掉。
 * monaco-editor 0.56 的 standaloneCodeEditor 在 create（standaloneCodeEditor.js:184）与
 * updateOptions（:218）里都会调 updateConfigurationService(...)，把 tabSize 写进**全局**那个
 * StandaloneConfigurationService（editor.tabSize 是 editorConfigurationSchema 里注册过的配置键）。
 * 全局配置一变，services/modelService.js:162 的 _updateModelOptions 会遍历**当前所有活着的
 * model**，在 _setModelOptionsForModel(:180-209) 里把新的 tabSize / indentSize 推给每一个。
 * 也就是说：本实例设一次缩进宽度，会顺手把同一页上**别的** Monaco 实例的 model 一起改掉
 * ——改造前 tabSize 处处写死 2、全局只有一个值，所以看不出来；indent_width 变成可配之后
 * 这条就浮出水面了。反向的受害路径与修法见 installModelOptionsGuard。
 *
 * ⚠️ 反过来「把 tabSize 从 create 的 options 里拿掉、只留 model.updateOptions」并不能修这个问题，
 * 别照那个方向改：建 model 时 ModelService 用的缓存键是 `language + undefined`、
 * _updateModelOptions 用的是 `language + uri`，首次必然对不上，于是 _setModelOptionsForModel
 * 在 oldOptions 为 undefined 时不会 early-return，会把**全局配置里的** tabSize 直接盖到新 model 上。
 * 现在这里全局与 model 两处一起写，恰好让那条路径无害（盖上来的正是我们刚写进去的值）。
 */
function applyIndentWidth(width: number) {
  desiredIndentWidth = width;
  editor?.updateOptions({ tabSize: width });
  editor?.getModel()?.updateOptions({ tabSize: width, indentSize: width });
}

/**
 * 给本实例的 model 装一个「缩进宽度自愈守卫」，返回订阅句柄（卸载时必须 dispose）。
 *
 * 要挡的就是 applyIndentWidth 注释里那条全进程副作用的**反向**：别的 Monaco 实例
 * create / updateOptions 时改了全局配置，ModelService 再把新的 tabSize 广播给所有活着的
 * model —— 包括我们这一个。两条真实触发路径：
 *   A. 脚本页开着一个 4 空格 Python 脚本（自动检测得 4，状态条显示 Tab 4）→ 点「调试运行」，
 *      ScriptExecutionDialogs.vue 里那个 <CodeEditor> 没传 indent-width、吃默认值 2 →
 *      它以 tabSize: 2 create → 全局值从 4 变 2 → 主编辑器那个**还活着的** model 被一起刷成 2。
 *      关掉弹窗也不会还原：正文按 2 列画参考线、按 Tab 只插 2 个空格，
 *      而齿轮菜单和状态条仍然写着 Tab 4。
 *   B. 不用弹窗也能中：脚本页与配置文件页都被 keep-alive，两个实例同时活着。
 *      配置文件（2 空格 shell）先打开 → 全局 2；切到脚本页打开 4 空格脚本 → 全局 4 →
 *      配置文件页那个 model 跟着变 4；切回配置文件页时 keep-alive 不重建、内容没变
 *      也就不走 replaceValue，于是 2 空格的文件按 4 列画参考线。
 * CodeMirror 侧没有这个毛病（Compartment 是实例级的），所以同一个操作在两个引擎下表现
 * 不一致，更难被认出来。
 *
 * 修法：一旦发现本实例 model 的缩进宽度不是自己想要的，就用 **model 级**的
 * model.updateOptions 顶回来 —— 它只改这一个 model、**不写全局配置**，
 * 所以两个实例之间不会互相推来推去（谁被改坏谁自己顶回去，各修各的）。
 *
 * ⚠️ 不会死循环，这一点别怀疑、更别为此把守卫删掉：顶回去之后值已经相等，
 * 事件即使再触发一次，也会在下面那道判等处直接 return。而且 TextModel.updateOptions 只在真的有字段
 * 变化时才 fire，写入相同的值连事件都不会发；它还是**先**更新 _options 再 fire 的，
 * 所以哪怕同步重入，重入的那一层读到的也已经是新值。
 *
 * ⚠️ 订阅的是**传进来的这一个 model**。本仓换文档走 editor.setValue()（见 replaceValue）、
 * 换语言走 setModelLanguage()，两条路都不换 model 对象，所以一次订阅够用；
 * 哪天真改成 editor.setModel(newModel)，这里必须跟着重新订阅（旧的先 dispose）。
 */
function installModelOptionsGuard(model: MonacoTextModel) {
  return model.onDidChangeOptions(() => {
    // 卸载顺序上先摘订阅再 dispose model，正常走不到这里；留一道守卫免得将来顺序被人调换后
    // 在一个已 dispose 的 model 上调 getOptions() 抛错。
    if (model.isDisposed()) return;
    const options = model.getOptions();
    if (
      options.tabSize === desiredIndentWidth &&
      options.indentSize === desiredIndentWidth
    ) {
      return;
    }
    model.updateOptions({
      tabSize: desiredIndentWidth,
      indentSize: desiredIndentWidth,
    });
  });
}

// 写回文档前先比一次：setValue 会触发 onDidChangeModelContent，
// 不比就和父组件转成回环（CodeMirror 侧的 replaceDoc 是同一个思路）。
function replaceValue(value: string) {
  if (!editor) return;
  if (editor.getValue() === value) return;
  editor.setValue(value);
  // 与 CodeMirror 侧 replaceDoc 同一条规则：外部整体换文档时，「自动」档按新内容重测一次。
  // 这一条是 issue #116-1 的另一半 —— 页面是「先挂编辑器、再 await 拉内容」的，
  // create 那一刻文档还是空串，不在这里补测的话「自动」永远停在兜底的 2。
  // 用户自己打字触发的那次会在上面那条判等提前 return，走不到这里。
  if (props.indentWidth === "auto") {
    applyIndentWidth(detectIndentWidth(value));
  }
}

onMounted(() => {
  // 监听器在 await 之前就挂上，卸载时统一摘掉；即使 Monaco 最终没加载成也不会漏挂/漏摘。
  window.addEventListener(PANEL_APPEARANCE_CHANGE_EVENT, syncEditorTheme);
  void mountEditor();
});

/** 失败态里的「重试」：重新走一遍动态 import 与 create。 */
function retryMountEditor() {
  void mountEditor();
}

/** 取 Monaco 并建出编辑器实例。挂载时调一次；monacoEngine 加载失败后由失败态的「重试」再调。 */
async function mountEditor() {
  // 已经建好了，或上一发 import 还在路上（连点「重试」）：都不再来一次
  if (!editorRef.value || editor || loadingEngine) return;
  loadingEngine = true;
  loadError.value = null;

  // 全仓唯一的 Monaco 取用姿势：动态 import。不切到 Monaco 的用户一个字节都不下载。
  // 经分发层 CodeEditor.vue 挂进来时，它的 loader 已经把 monacoEngine 一起拉好了，这一发当场 resolve；
  // 这里的 try/catch 是兜底，接住仍然失败的边角情况（比如不经分发层直接挂本组件），盖一层失败态。
  let monaco: MonacoApi;
  try {
    const engineModule = await import("@/utils/monacoEngine");
    // 取成员也必须在 try 里：chunk 失败而 main.ts 已经安排了自动刷新时，Vite 的预加载助手被 preventDefault，
    // 这一发 import 不 reject 而是 resolve 成 undefined，读成员抛的 TypeError 同样要落进失败态。
    monaco = engineModule.monaco;
  } catch (error) {
    loadingEngine = false;
    // 已卸载就不必显示了（切文件、关弹窗、引擎切回 CodeMirror 都会走到这里）
    if (!destroyed) {
      loadError.value = error;
      console.warn("Monaco 编辑器加载失败", error);
    }
    return;
  }
  loadingEngine = false;

  // ⚠️ await 期间组件完全可能已经被卸载了（脚本页切文件、调试弹窗被关、引擎又切回 CodeMirror），
  // 那时 onBeforeUnmount 早就跑完了。少了这道守卫就会往一个已经从文档里摘掉的 DOM 节点上
  // create 出一个**永远不会被 dispose** 的实例：它的 ResizeObserver、全局事件监听、模型
  // 全都留在内存里，而页面上什么都看不到。
  if (destroyed || !editorRef.value) return;

  monacoApi = monaco;
  // 先定主题再 create：setTheme 是全局的、不需要实例，这样新实例第一帧就是对的颜色，
  // 不会先闪一下 Monaco 自带的 vs / vs-dark。
  applyEditorTheme(monaco);

  // create 的 options 里那个 tabSize 是唯一不走 applyIndentWidth 的写入路径，
  // 所以在这里显式对齐一次自愈守卫的基准值。少了这一句，守卫会拿着初值 2
  // 去「纠正」一个本来就正确的 4 —— 那就从修 bug 变成造 bug 了。
  desiredIndentWidth = resolveIndentWidth(props.modelValue);

  editor = monaco.editor.create(editorRef.value, {
    value: props.modelValue,
    language: resolveMonacoLanguage(props.language),
    readOnly: props.readonly || false,
    wordWrap: props.wordWrap,
    minimap: { enabled: props.minimap },
    guides: { indentation: props.indentGuides },
    // ⚠️ 必须显式给：renderWhitespace 的默认值是 'selection'，不写就等于永远停在中间那档。
    // v3.2.4 起这一项是三态（none / selection / all），取值与 Monaco 原生逐字相同，直接透传；
    // CodeMirror 侧没有 'selection' 这一档，是自己写的（utils/selectionWhitespace.ts）。
    renderWhitespace: props.whitespace,
    // ⚠️ 也必须显式给：renderLineHighlight 的默认值是 'line'，只涂内容区、**不涂行号栏那一格**。
    // 而 CodeMirror 侧同时开了 highlightActiveLine() 与 highlightActiveLineGutter()，
    // 主题里 .cm-activeLine 与 .cm-activeLineGutter 两处铺的是同一个底色。
    // 不给 'all' 的话，同一份脚本换个引擎当前行就少了行号栏那半截底色，观感对不上。
    // （写死的常量、不跟任何开关走，所以下面那批 watch 里没有它的份。）
    renderLineHighlight: "all",
    // 让 Monaco 自己装 ResizeObserver 盯容器尺寸：三个调用点的高度都由外层 flex 链决定
    // （撑满卡片 / 撑满弹窗），不给这条就得自己在每次布局变化时调 layout()。
    automaticLayout: true,
    /*
     * 🔴 detectIndentation 必须显式关掉，这是 issue #116-1「F5 之后缩进随机 2 或 4」的**根治点**，
     * 别当成一个「没用的 false」顺手删掉 —— 删了当场复发，而且是概率性的，很难查。
     *
     * 成因有两条，给了这个 false 之后两条同时消失：
     *   1. 它默认是 true，只在 **TextModel 构造那一瞬间**按内容猜一次 tabSize
     *      （`setValue()` 永远不会重猜）。而页面是「先挂编辑器、再 await 拉脚本内容」的：
     *      两个异步谁先到就决定了它猜的是空串（→ 兜底 4，再被下面的 tabSize 盖成 2）
     *      还是真实的 4 空格脚本（→ 4）。同一份文件刷两次能得到两个结果。
     *   2. Monaco 的 ModelService 有个缓存键不一致的坑：**任何**一次带 wordWrap /
     *      renderWhitespace 之类选项的 updateOptions()，都会顺带触发一次
     *      model.detectIndentation() 重猜 —— 也就是「点一下自动换行，缩进宽度自己变了」。
     *
     * 关掉之后缩进宽度只有一个来源：下面这个 tabSize（'auto' 档由 detectIndentWidth 自己算，
     * 两个引擎共用同一份判断）。
     */
    detectIndentation: false,
    // 缩进宽度。与 CodeMirror 侧的 indentUnit + EditorState.tabSize 吃的是同一个解析结果。
    // 用上面刚算好的那份，不在这里重算一次：两处必须是同一个值（见 desiredIndentWidth 的注释）。
    tabSize: desiredIndentWidth,
    insertSpaces: true,
    fontSize: 14,
    fontFamily: resolveCodeEditorFontFamily(),
    // 不允许滚到最后一行以下的空白区：编辑器本来就嵌在有限高度的卡片里，
    // 多出一屏空白只会让人以为内容没加载完
    scrollBeyondLastLine: false,
  });

  // 装上缩进宽度自愈守卫。必须在 create 之后拿 model：standalone editor 是自己建 model 的，
  // create 之前根本没有这个对象。理由与死循环疑虑见 installModelOptionsGuard 的注释。
  const createdModel = editor.getModel();
  if (createdModel) {
    modelOptionsSubscription = installModelOptionsGuard(createdModel);
  }

  contentSubscription = editor.onDidChangeModelContent(() => {
    const value = editor?.getValue() ?? "";
    // 和外部 modelValue 相同，说明这次变更是 replaceValue 同步进来的，
    // 再 emit 一次就和父组件转成回环了。
    if (value === props.modelValue) return;
    // 每次输入即时 emit，不能等失焦：
    // 「未保存」角标、保存按钮 disabled、调试弹窗的 markDebugCodeChanged 全靠它。
    emit("update:modelValue", value);
  });
}

watch(
  () => props.modelValue,
  (newValue) => {
    replaceValue(newValue);
  },
);

/**
 * ⚠️ 下面每一条 watch 都是必需的，理由与 CodeMirror 侧「必须用 Compartment」是同一条铁律的另一半：
 * Monaco 的 options 也只在 create() 那一刻读一次，挂载之后再点开关是**完全没反应且不报错**的，
 * 构建与 vue-tsc 全绿。运行期改配一律走 editor.updateOptions()，换语言走 setModelLanguage()。
 * 新增可配项时「create 的 options + updateOptions + watch」三处必须一起补。
 */

// 脚本页切换文件会换语言，必须重设，否则一直停在第一次打开那个文件的高亮上。
// 语言挂在**模型**上而不是编辑器上，所以不能走 updateOptions（那里的 language 只对 create 生效）。
watch(
  () => props.language,
  (newLanguage) => {
    const model = editor?.getModel();
    if (!model || !monacoApi) return;
    monacoApi.editor.setModelLanguage(model, resolveMonacoLanguage(newLanguage));
  },
);

// 漏了这条就是「只读态还能改」：改完污染 hasChanges，退出编辑时弹出幽灵「有未保存改动」
watch(
  () => props.readonly,
  (newReadonly) => {
    editor?.updateOptions({ readOnly: newReadonly || false });
  },
);

watch(
  () => props.wordWrap,
  (newWordWrap) => {
    editor?.updateOptions({ wordWrap: newWordWrap });
  },
);

watch(
  () => props.minimap,
  (enabled) => {
    editor?.updateOptions({ minimap: { enabled } });
  },
);

watch(
  () => props.indentGuides,
  (enabled) => {
    editor?.updateOptions({ guides: { indentation: enabled } });
  },
);

// 三态直接透传，Monaco 原生就认这三个值
watch(
  () => props.whitespace,
  (mode) => {
    editor?.updateOptions({ renderWhitespace: mode });
  },
);

// 缩进宽度。读的是编辑器里当下的文档而不是 props.modelValue：两者在同一帧里可能还没同步，
// 而「自动」档要按编辑器**真正显示着的**内容来测。
// （另外两个重算时机：create 的 options 里一次、外部整体换文档时在 replaceValue 里一次。）
watch(
  () => props.indentWidth,
  () => {
    if (!editor) return;
    applyIndentWidth(resolveIndentWidth(editor.getValue()));
  },
);

onBeforeUnmount(() => {
  // 先立旗再做别的：onMounted 里的动态 import 可能还挂在 await 上，
  // 这面旗子是它 create 之前那道守卫的唯一依据。
  destroyed = true;
  window.removeEventListener(PANEL_APPEARANCE_CHANGE_EVENT, syncEditorTheme);
  contentSubscription?.dispose();
  contentSubscription = null;
  // 缩进宽度自愈守卫也必须在这里摘掉，而且要摘在下面 dispose model **之前**：
  // 它的回调闭包持着那个 model，漏掉就是每开一个文件泄漏一份订阅（表现同样是「用久了才吃内存」）。
  modelOptionsSubscription?.dispose();
  modelOptionsSubscription = null;
  // 模型必须在 dispose 编辑器**之前**取出来：dispose 之后 getModel() 返回 null，就没得回收了。
  const model = editor?.getModel() ?? null;
  editor?.dispose();
  // standalone editor 对「自己创建的」那个模型会在 detach 时顺手 dispose（内部的 _ownsModel），
  // 所以这里先问一句 isDisposed()，否则是一次重复 dispose。
  // 仍然显式写这一段，是因为「谁拥有模型」属于 Monaco 的内部实现细节：
  // 哪天它不再代管，漏掉的表现是每开一个文件泄漏一份全文加一整棵撤销树，用久了才吃内存。
  if (model && !model.isDisposed()) {
    model.dispose();
  }
  editor = null;
  monacoApi = null;
});

defineExpose({
  focus: () => editor?.focus(),
  getValue: () => editor?.getValue() ?? "",
  setValue: (value: string) => replaceValue(value),
  // 全仓没有调用点：格式化走后端 scriptApi.format，前端不做本地格式化。
  // ⚠️ 刻意与 CodeMirror 侧一样留空，**不要**改成
  // `editor.getAction('editor.action.formatDocument')?.run()` —— 那会让两个引擎在一个
  // 谁都不调的方法上行为不一致，而且只有 JSON 这类带语言服务的语言才真能格式化，
  // 其余语言静默什么都不做，将来谁真去调它反而更难查。
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
    <!-- monacoEngine 没加载成时盖一层失败态（重试 / 改用 CodeMirror），与分发层的失败态是同一个组件 -->
    <MonacoLoadErrorOverlay
      v-if="loadError !== null"
      :error="loadError"
      :retry="retryMountEditor"
    />
  </div>
</template>

<style scoped>
/* 🔴 根 class 与 --code-editor-min-height 这个变量名必须与 CodeMirrorEditor.vue **逐字相同**，
   不能叫 monaco-editor-wrapper / --monaco-editor-min-height：
   config-file/index.vue 有一条 `:deep(.code-editor-wrapper) { min-height: 560px }` 给窄屏兜下限，
   ScriptsEditorPane.vue 那条 `.code-editor { flex: 1 }` 也是穿透到这个根节点上的。
   换个名字之后两页的高度约束会一起失灵、编辑器高度塌陷，而构建与类型检查全绿。 */
.code-editor-wrapper {
  width: 100%;
  min-height: var(--code-editor-min-height, 400px);
  height: var(--code-editor-min-height, 400px);
  position: relative;
}

/* 双类选择器：靠特异性压过上面的固定高度，不依赖样式表书写顺序 */
.code-editor-wrapper.code-editor-wrapper--fill {
  flex: 1 1 auto;
  height: 100%;
}

/* 这里刻意**没有** CodeMirror 侧那条 `font-size: 14px` 与 ≤768px 提到 16px 的媒体查询：
   Monaco 是自绘编辑器，正文字号来自 JS 侧的 fontSize 选项，不吃容器的 CSS font-size，
   写了也只是死代码。iOS 那条「输入区小于 16px 会自动放大整页」的规避也就无从谈起 ——
   真正的答案是 Monaco 本来就不该出现在手机上（utils/editorEngine.ts 里 auto 在 coarse pointer
   与窄屏上硬回落 CodeMirror）；显式把引擎选成 monaco 的用户是自己做的选择。 */

.code-editor-container {
  width: 100%;
  height: 100%;
  /* 普通卡片没有固定父级高度时，内层容器必须继承最小高度，否则会被算成 0 高度，
     只剩一条横线且无法输入。 */
  min-height: inherit;
  overflow: hidden;
  /* 与 CodeMirror 侧同一条底色变量。Monaco 自己也会按主题涂底，
     但主题是在动态 import 落地之后才应用的，这条负责那之前那几帧不露出页面底色。 */
  background: var(--dd-editor-bg-color, #111827);
  /* 固定 0、不吃 --dd-radius-*：这是一整块代码面，四角由外层卡片自己裁，
     这里再加圆角只会和卡片的圆角叠出双层圆角。与 CodeMirror 侧保持一致。 */
  border-radius: 0;
}
</style>
