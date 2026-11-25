import { EditorView } from '@codemirror/view'
import { HighlightStyle, StreamLanguage, syntaxHighlighting } from '@codemirror/language'
import type { Extension } from '@codemirror/state'
import { tags } from '@lezer/highlight'
import { css } from '@codemirror/lang-css'
import { go } from '@codemirror/lang-go'
import { html } from '@codemirror/lang-html'
import { javascript } from '@codemirror/lang-javascript'
import { json } from '@codemirror/lang-json'
import { markdown } from '@codemirror/lang-markdown'
import { python } from '@codemirror/lang-python'
import { xml } from '@codemirror/lang-xml'
import { yaml } from '@codemirror/lang-yaml'
import { shell } from '@codemirror/legacy-modes/mode/shell'
import {
  getDefaultEditorBackgroundColor,
  getReadableTextColor,
  isDarkColor,
  isPanelDarkMode
} from './panelAppearance'

/**
 * CodeMirror 6 的语言支持与主题构建。
 *
 * 与被它取代的 utils/monaco.ts 最大的差别：**这里没有任何异步加载、资源探测或 CDN 兜底**。
 * CodeMirror 是普通 ES 模块，由 Vite 直接打进产物，import 完就能用；
 * 「本地资源残缺 → 回退 CDN → 超时 → 提示重试」那一整套故障模式在这个方案里不存在，
 * 所以调用点上的「加载中骨架 / 加载失败 + 重试按钮」也一并删掉了，不要再补回来。
 */

const EDITOR_BACKGROUND_VAR = '--dd-editor-bg-color'
const EDITOR_FOREGROUND_VAR = '--dd-editor-fg-color'

/** 等宽字体栈兜底值（当 --dd-font-mono 尚未解析或处于非浏览器环境时使用）。 */
const FALLBACK_EDITOR_FONT_FAMILY =
  'ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, "Liberation Mono", monospace'

/**
 * 读取全站等宽字体令牌 `--dd-font-mono`，保证 CodeMirror、Monaco 与 MonacoDiff 使用同一字体栈。
 */
export function resolveCodeEditorFontFamily(): string {
  return readRootCssVar('--dd-font-mono') || FALLBACK_EDITOR_FONT_FAMILY
}

/**
 * 语言名 → CodeMirror 语言扩展。
 *
 * 语言名的真源是 `views/scripts/useScriptWorkspaceBrowser.ts` 的 editorLanguage 映射
 * （按扩展名推导），配置文件页固定传 'shell'，版本对比默认 'plaintext'。
 * 这里把真源里出现过的每一个值都覆盖到，并额外认几个常见别名（js/ts/sh/yml/md），
 * 免得将来在真源里加一行别名却忘了同步这边 —— 那种漏配的表现是「静默丢掉高亮」，不报错。
 *
 * plaintext（以及任何认不出来的值）返回空数组：CodeMirror 里「没有语言扩展」就是纯文本，
 * 不需要像 Monaco 那样有一个叫 plaintext 的语言。
 */
export function resolveCodeEditorLanguage(language?: string): Extension {
  switch ((language || '').trim().toLowerCase()) {
    case 'javascript':
    case 'js':
      return javascript()
    case 'typescript':
    case 'ts':
      // typescript 不是独立包，走 lang-javascript 的 typescript 选项
      return javascript({ typescript: true })
    case 'python':
    case 'py':
      return python()
    case 'shell':
    case 'bash':
    case 'sh':
      // shell 没有官方 lezer 语法，用 legacy-modes 的流式模式兜住（高亮质量略弱于 lezer，但覆盖到位）
      return StreamLanguage.define(shell)
    case 'go':
      return go()
    case 'json':
      return json()
    case 'yaml':
    case 'yml':
      return yaml()
    case 'markdown':
    case 'md':
      return markdown()
    case 'html':
      return html()
    case 'css':
      return css()
    case 'xml':
      return xml()
    default:
      return []
  }
}

/* --------------------------------------------------------------------------
 * 缩进宽度检测（issue #116-1/3）。
 * ⚠️ 这一段**两个引擎共用**，不是 CodeMirror 专属：
 * CodeMirror 侧拿它去配 indentUnit + EditorState.tabSize，Monaco 侧拿它去配 tabSize。
 * 放在这里的理由与语言别名表、调色板一样 —— 一份判断，两处使用，拆开就会漂。
 * -------------------------------------------------------------------------- */

/**
 * 采样行数上限。
 * 打开一份几万行的日志或压缩产物时，全量扫会明显拖慢首次渲染；
 * 而缩进风格是文件级的习惯，看前 1000 行足够定性（Monaco 自己取的是 10000 行，
 * 但它是在 TextModel 构造时同步跑的，我们是在每次外部换文档时跑，取更保守的值）。
 */
const INDENT_SAMPLE_LINES = 1000

/** 允许的检测结果，与 utils/editorPreferences.ts 的 EditorIndentWidth 逐项对齐。 */
const INDENT_WIDTH_CANDIDATES = [2, 4, 6, 8] as const

/**
 * 按内容猜「一级缩进等于几列」。
 *
 * 思路对齐 Monaco 的 guessIndentation：逐行取行首空格列数，与**上一条非空行**比一次，
 * 正增量就是一次「进了一级」的证据，给这个增量投一票；票最多的那个宽度胜出。
 *
 * ⚠️ 为什么不直接用 Monaco 自带的 detectIndentation：
 *   1. 它只在 TextModel **构造那一瞬间**跑一次，`setValue()` 永远不会重猜 ——
 *      而脚本页是「先挂编辑器、再 await 拉内容」，构造时文档还是空串，
 *      猜出来的永远是兜底值（这正是 issue #116-1「F5 之后随机 2 或 4」的成因）。
 *   2. 我们有两个引擎，CodeMirror 侧根本没有这个东西。两边必须用**同一份**判断，
 *      否则同一个文件换个引擎缩进宽度就变了。
 * 所以 MonacoEditor.vue 里 detectIndentation 是显式关掉的，两边都吃这个函数的结果。
 *
 * 没有任何缩进证据（空文件、单行、全顶格）→ 回落 2；
 * 用 Tab 缩进的文件也回落 2 —— Tab 的显示宽度由 tabSize 决定，这里只关心「一级缩进几列」。
 */
export function detectIndentWidth(content: string): 2 | 4 | 6 | 8 {
  // 键是增量、值是出现次数：votes.get(4) 就是「相邻两级差 4 列」出现过几次。
  // 用 Map 而不是定长数组，是因为本仓开了 noUncheckedIndexedAccess，
  // 数组下标读出来是 number | undefined，`votes[diff]++` 会直接类型报错。
  const votes = new Map<number, number>()
  const voteOf = (width: number) => votes.get(width) ?? 0
  let tabLines = 0
  let spaceLines = 0
  // 上一条「纯空格缩进的非空行」的缩进列数；-1 表示没有可比对象
  let prevIndent = -1
  let scanned = 0
  let pos = 0

  // 手写切行而不是 content.split('\n')：超长文件不必先造一个几万项的数组才发现只用前 1000 行
  while (pos < content.length && scanned < INDENT_SAMPLE_LINES) {
    let end = content.indexOf('\n', pos)
    if (end < 0) end = content.length
    const raw = content.slice(pos, end)
    pos = end + 1
    scanned++
    // CRLF 文件的行尾 \r 必须先摘掉，否则「只有 \r 的空行」会被下面当成一条顶格的代码行，
    // 把 prevIndent 拉回 0，紧跟着的那一行就会凭空贡献一个假的大增量
    const text = raw.endsWith('\r') ? raw.slice(0, -1) : raw

    let spaces = 0
    let hasTab = false
    let i = 0
    for (; i < text.length; i++) {
      const ch = text[i]
      if (ch === ' ') spaces++
      else if (ch === '\t') hasTab = true
      else break
    }

    // 空行与纯空白行不作为证据：它们的缩进是排版残留，不代表代码层级
    if (i >= text.length) continue

    if (hasTab) {
      tabLines++
      // 跨过一条 Tab 缩进行去比对空格列数没有意义，断掉这条链
      prevIndent = -1
      continue
    }

    spaceLines++
    if (prevIndent >= 0) {
      const diff = spaces - prevIndent
      // 只认正增量（进一级）。负增量是退出块，一次退几级是任意的，不是证据。
      if (diff > 0 && diff <= 8) votes.set(diff, voteOf(diff) + 1)
    }
    prevIndent = spaces
  }

  // 整份文件主要靠 Tab 缩进：没有「一级等于几列」这回事，回落 2
  if (tabLines > spaceLines) return 2

  let best: 2 | 4 | 6 | 8 = 2
  let bestScore = 0
  for (const width of INDENT_WIDTH_CANDIDATES) {
    // 严格大于：票数打平时靠遍历顺序让小的胜出
    if (voteOf(width) > bestScore) {
      bestScore = voteOf(width)
      best = width
    }
  }
  // 一条正增量证据都没有（空文件、单行、全顶格）→ 回落 2
  if (bestScore === 0) return 2

  // 抄自 Monaco 的一条经验规则：4 只有在明显压倒 2 时才算数。
  // 典型场景是 YAML / shell 这类 2 空格文件里嵌了几处深层结构，跳级跳出一堆 4，
  // 光比票数会把整份文件判成 4、于是每两级才画一条参考线。
  if (best === 4 && voteOf(2) > 0 && voteOf(2) * 3 >= voteOf(4) * 2) return 2

  return best
}

/**
 * 语法高亮配色。
 *
 * 取值刻意贴近 Monaco 内置的 vs / vs-dark：改造前 `defineMonacoTheme` 的 rules 是空数组 + inherit，
 * 也就是语法色用的就是 Monaco 默认那套。老用户升级后代码的颜色基本不变，
 * 不会以为「换了引擎之后高亮坏了」。
 */
const DARK_SYNTAX = {
  comment: '#6a9955',
  keyword: '#569cd6',
  controlKeyword: '#c586c0',
  string: '#ce9178',
  number: '#b5cea8',
  typeName: '#4ec9b0',
  functionName: '#dcdcaa',
  // variableName 保留只为记录 VS Code 的原值，**两个引擎都不再引用它**（issue #119，见
  // buildHighlightStyle / defineMonacoTheme 里那两段注释）。别看它没人用就顺手接回去。
  variableName: '#9cdcfe',
  constant: '#4fc1ff',
  operator: '#d4d4d4',
  tagName: '#569cd6',
  attributeName: '#9cdcfe',
  regexp: '#d16969',
  meta: '#9e9e9e',
  invalid: '#f14c4c'
}

const LIGHT_SYNTAX = {
  comment: '#008000',
  keyword: '#0000ff',
  controlKeyword: '#af00db',
  string: '#a31515',
  number: '#098658',
  typeName: '#267f99',
  functionName: '#795e26',
  variableName: '#001080',
  constant: '#0070c1',
  operator: '#000000',
  tagName: '#800000',
  attributeName: '#e50000',
  regexp: '#811f3f',
  meta: '#808080',
  invalid: '#cd3131'
}

function buildHighlightStyle(dark: boolean) {
  const color = dark ? DARK_SYNTAX : LIGHT_SYNTAX

  // 同一个词命中多条规则时，lezer 按「标签更具体者胜」决定，与书写顺序无关。
  // 下面 tags.function(tags.variableName) / tags.constant(tags.variableName) 是**标签构造器**
  // （给 variableName 套一层修饰后得到的更具体标签），不是在读调色板里的 variableName 字段 ——
  // 裸的 variableName 规则本身已经按 issue #119 删掉了（见下面那条 ⚠️），但这两条得留着：
  // 它们只命中函数名与常量，不会把正文染色。
  return HighlightStyle.define([
    // ⚠️ 注释刻意**不加** fontStyle: 'italic'（issue #116-6）：等宽字体的斜体多半是合成出来的
    // （字体家族里没有真正的 italic 字形，浏览器直接把正体切一刀），笔画会发虚、行内还会串位。
    // 下面 tags.emphasis 那条斜体是另一回事，别连坐删掉 —— 它是 Markdown 的 *强调*，
    // 源文本本来就要求斜体，属于内容语义，不是「注释用斜体」这种主题偏好。
    { tag: [tags.comment, tags.lineComment, tags.blockComment, tags.docComment], color: color.comment },
    { tag: [tags.keyword, tags.modifier, tags.self, tags.null, tags.atom, tags.bool], color: color.keyword },
    { tag: [tags.controlKeyword, tags.moduleKeyword], color: color.controlKeyword },
    { tag: [tags.string, tags.special(tags.string), tags.character], color: color.string },
    { tag: [tags.number, tags.integer, tags.float], color: color.number },
    { tag: [tags.typeName, tags.className, tags.namespace], color: color.typeName },
    { tag: [tags.function(tags.variableName), tags.function(tags.propertyName), tags.macroName], color: color.functionName },
    // ⚠️ 裸的 variableName / propertyName / labelName 刻意**不给**颜色（issue #119）：
    // 它们覆盖的是「代码正文里绝大多数的词」，给它上色等于把正文整片染成 #001080 深蓝。
    // 不给规则时它们继承 buildCodeEditorTheme() 里 `&` 的 color（--dd-editor-fg-color，
    // 浅色 #111827 / 深色 #e5e7eb），与 v3.1.1 的观感一致，也跟着用户自定义的编辑器前景色走。
    // Monaco 侧 defineMonacoTheme() 里同样不给 identifier 规则，两个引擎必须一起改。
    { tag: [tags.constant(tags.variableName), tags.standard(tags.variableName)], color: color.constant },
    { tag: [tags.operator, tags.punctuation, tags.separator, tags.bracket, tags.derefOperator], color: color.operator },
    { tag: [tags.tagName, tags.angleBracket], color: color.tagName },
    { tag: [tags.attributeName], color: color.attributeName },
    { tag: [tags.regexp, tags.escape], color: color.regexp },
    { tag: [tags.meta, tags.processingInstruction, tags.documentMeta], color: color.meta },
    { tag: [tags.heading], color: color.keyword, fontWeight: 'bold' },
    { tag: [tags.link, tags.url], color: color.constant, textDecoration: 'underline' },
    { tag: [tags.emphasis], fontStyle: 'italic' },
    { tag: [tags.strong], fontWeight: 'bold' },
    { tag: [tags.strikethrough], textDecoration: 'line-through' },
    { tag: [tags.invalid], color: color.invalid }
  ])
}

function readRootCssVar(name: string) {
  if (typeof window === 'undefined') {
    return ''
  }
  return window.getComputedStyle(document.documentElement).getPropertyValue(name).trim()
}

/**
 * 编辑器底色是不是深色。
 *
 * 主题、以及那些没法用 var() 表达的配色（比如版本对比标尺上的红/绿刻度）共用这一条判断：
 * 底色是**用户可配项**，明暗规则只能有一处，否则改了默认底色只同步到一边，
 * 表现是「浅色底上一条深色标尺」。
 */
export function isCodeEditorDark() {
  const background =
    readRootCssVar(EDITOR_BACKGROUND_VAR) || getDefaultEditorBackgroundColor(isPanelDarkMode())
  return isDarkColor(background)
}

/**
 * 按当前配色构建编辑器主题（外观 + 语法高亮）。
 *
 * 底色/前景色刻意写成 `var(--dd-editor-bg-color, <本次解析值>)` 而不是直接写死解析结果：
 * 这两条变量是**用户可配项**（系统设置里的 editor_background_color，由 utils/panelAppearance.ts
 * 写进 documentElement），写死会让用户自定义配色完全失效。
 * 写成 var() 之后，用户改色时连主题都不用重建，浏览器自己就重绘了。
 *
 * 但语法色 / 行号色 / 选区色只能在 JS 里按「底色是深是浅」二选一，没法用 var() 表达，
 * 所以调用方仍必须在 PANEL_APPEARANCE_CHANGE_EVENT（明暗切换也会走它，见 stores/theme.ts）
 * 触发时重新调本函数并通过 Compartment 换主题，否则浅色底上会留着深色主题的行号和选区。
 */
export function buildCodeEditorTheme(): Extension[] {
  const background =
    readRootCssVar(EDITOR_BACKGROUND_VAR) || getDefaultEditorBackgroundColor(isPanelDarkMode())
  const foreground = readRootCssVar(EDITOR_FOREGROUND_VAR) || getReadableTextColor(background)
  const dark = isDarkColor(background)

  const backgroundValue = `var(${EDITOR_BACKGROUND_VAR}, ${background})`
  const foregroundValue = `var(${EDITOR_FOREGROUND_VAR}, ${foreground})`
  const fontFamily = resolveCodeEditorFontFamily()
  // 选区/行高亮沿用改造前 defineMonacoTheme 里那几条颜色，保持观感一致
  const selectionColor = dark ? '#134e4acc' : '#bfdbfe'
  const activeLineColor = dark ? 'rgba(255, 255, 255, 0.05)' : 'rgba(15, 23, 42, 0.04)'
  const gutterColor = dark ? '#6b7280' : '#94a3b8'
  const caretColor = dark ? '#34d399' : '#2563eb'
  // 缩进参考线的颜色。刻意不从 --dd-editor-fg-color 派生：那是用户可配的任意颜色，
  // 想在它基础上叠透明度只能靠 color-mix()，旧一点的移动端浏览器不认、会静默变成没有线。
  // 改用「底色深就叠白、底色浅就叠黑」的低透明度覆盖 —— 和上面 activeLine / selectionMatch
  // 用的是同一套办法，对任意自定义底色都成立。
  const indentGuideColor = dark ? 'rgba(255, 255, 255, 0.14)' : 'rgba(15, 23, 42, 0.12)'
  // 空白符标记与行号同色：行号色本来就是按「底色是深是浅」算出来的，
  // 用户改底色 / 切明暗时跟着主题一起重建，不用额外接线。
  const whitespaceMarkColor = gutterColor

  // ⚠️ 下面每条后代选择器都刻意多写一个 `.cm-editor`（`&` 会被换成本主题的生成类名，
  // 于是变成 `.主题类.cm-editor .cm-xxx`，三个类）。
  // 理由：CodeMirror 的基础主题里有一批 `&light .cm-gutters` / `&dark .cm-activeLine` /
  // `&light .cm-panels` 这样的规则，展开后是「一个类 + 一个类」，和不加 `.cm-editor` 时
  // 我们写的规则**特异性完全相同**。同分时谁赢只取决于样式表注入顺序，
  // 而那取决于哪个编辑器先挂载 —— 赌输的表现是「深色编辑器配了一条浅灰行号栏」，
  // 而且只在某些进入顺序下复现。加一个类把这件事变确定。
  const theme = EditorView.theme(
    {
      // 刻意不在主题里写 height：普通编辑器要撑满外层容器、差异编辑器是左右两个编辑器并排，
      // 两边的高度策略不同，各自在组件的 scoped 样式里定，主题只管配色。
      '&': {
        color: foregroundValue,
        backgroundColor: backgroundValue,
        // 缩进参考线的颜色。写在这里（而不是 indentGuides.ts 的 baseTheme 里）是因为
        // 主题本来就会在 PANEL_APPEARANCE_CHANGE_EVENT 时整块重建：明暗切换、用户改编辑器底色
        // 都会自动跟着换色，参考线那边不用再挂一次监听。自定义属性会继承到 .cm-line。
        // ⚠️ 别为它另起一个 '&.cm-editor .cm-content' 键 —— 下面已经有同名键了，
        // JS 对象字面量里重复键是后者覆盖前者且不报错，会把字体/内边距/光标色一起吃掉。
        '--dd-indent-guide-color': indentGuideColor
      },
      // CodeMirror 基础主题会在聚焦时画一圈点状 outline，Monaco 时代没有这个东西，去掉。
      // 聚焦与否靠原生光标就能看出来，不影响可访问性。
      '&.cm-focused': {
        outline: 'none'
      },
      '&.cm-editor .cm-scroller': {
        fontFamily,
        lineHeight: '1.5',
        overflow: 'auto'
      },
      '&.cm-editor .cm-content': {
        fontFamily,
        padding: '8px 0',
        // 原生光标的颜色：编辑器刻意没启用 drawSelection（那会关掉原生选区，
        // 手机上的长按菜单与选择手柄就全没了），所以只能靠 caret-color 给光标上色。
        caretColor
      },
      '&.cm-editor .cm-gutters': {
        backgroundColor: backgroundValue,
        color: gutterColor,
        border: 'none'
      },
      '&.cm-editor .cm-activeLine': {
        backgroundColor: activeLineColor
      },
      '&.cm-editor .cm-activeLineGutter': {
        backgroundColor: activeLineColor,
        color: foregroundValue
      },
      // 原生选区的配色。没启用 drawSelection，所以 .cm-selectionBackground 这里用不上。
      '&.cm-editor .cm-content ::selection': {
        backgroundColor: selectionColor
      },
      '&.cm-editor .cm-line::selection': {
        backgroundColor: selectionColor
      },
      '&.cm-editor .cm-selectionMatch': {
        backgroundColor: dark ? 'rgba(52, 211, 153, 0.18)' : 'rgba(37, 99, 235, 0.12)'
      },
      '&.cm-editor .cm-matchingBracket, &.cm-editor .cm-nonmatchingBracket': {
        backgroundColor: dark ? 'rgba(148, 163, 184, 0.28)' : 'rgba(100, 116, 139, 0.2)',
        outline: `1px solid ${gutterColor}`
      },
      '&.cm-editor .cm-searchMatch': {
        backgroundColor: dark ? 'rgba(234, 179, 8, 0.28)' : 'rgba(234, 179, 8, 0.35)'
      },
      '&.cm-editor .cm-searchMatch.cm-searchMatch-selected': {
        backgroundColor: dark ? 'rgba(234, 179, 8, 0.5)' : 'rgba(234, 179, 8, 0.6)'
      },
      // 搜索面板（Ctrl+F）用面板自身的令牌配色，不要跟着编辑器底色走：
      // 用户可能把编辑器底色调成任意颜色，面板控件与边框仍要与当前面板主题保持一致、避免极性相反时串色。
      '&.cm-editor .cm-panels': {
        backgroundColor: 'var(--el-bg-color)',
        color: 'var(--el-text-color-primary)',
        borderTop: '1px solid var(--dd-divider, var(--el-border-color-lighter))',
        borderBottom: '1px solid var(--dd-divider, var(--el-border-color-lighter))'
      },
      '&.cm-editor .cm-textfield': {
        backgroundColor: 'var(--el-fill-color-blank)',
        color: 'var(--el-text-color-primary)',
        border: '1px solid var(--el-border-color)',
        borderRadius: 'var(--dd-radius-control)'
      },
      '&.cm-editor .cm-button': {
        backgroundColor: 'var(--el-fill-color-light)',
        backgroundImage: 'none',
        color: 'var(--el-text-color-primary)',
        border: '1px solid var(--el-border-color)',
        borderRadius: 'var(--dd-radius-control)'
      },
      '&.cm-editor .cm-specialChar': {
        color: dark ? '#f87171' : '#dc2626'
      },
      // ⚠️ 下面三条只覆盖 backgroundImage / backgroundColor，**绝不能写 background 简写**：
      // CodeMirror 基础主题把 backgroundSize(.4em) 与 backgroundPosition 写成了独立长写属性，
      // 用简写会把这两条一并重置，灰点会变成铺满整格的一大坨、且位置错位。
      '&.cm-editor .cm-highlightSpace': {
        backgroundImage: `radial-gradient(circle at 50% 55%, ${whitespaceMarkColor} 20%, transparent 0)`
      },
      '&.cm-editor .cm-highlightTab': {
        backgroundImage:
          `url('data:image/svg+xml,<svg xmlns="http://www.w3.org/2000/svg" width="200" height="20">` +
          `<path stroke="${encodeURIComponent(whitespaceMarkColor)}" stroke-width="1" fill="none" ` +
          `d="M1 10H196L190 5M190 15L196 10M197 4L197 16"/></svg>')`
      },
      // 基础主题这条写死成 #ff332255（一片红），在用户自选底色上会串色，必须自己给
      '&.cm-editor .cm-trailingSpace': {
        backgroundColor: dark ? 'rgba(248, 113, 113, 0.16)' : 'rgba(220, 38, 38, 0.12)'
      }
    },
    { dark }
  )

  // dark 标志除了给我们自己用，@codemirror/merge 的内置差异配色也靠它区分明暗两套，
  // 传错了会出现「浅色底上一片深色的删除块」。
  return [theme, syntaxHighlighting(buildHighlightStyle(dark))]
}

/* ==========================================================================
 * 以下是 Monaco 引擎侧的对应物。
 *
 * 放在同一个文件里、而不是新起一个 monacoTheme.ts：语言别名表与调色板是**两个引擎共用的
 * 那一份**，拆开就一定会出现「在这边补了个新别名、忘了同步那边」，
 * 而那种漏配的表现是「同一个文件换个引擎高亮就没了」，不报错。
 *
 * ⚠️ 这里刻意**没有** import 任何 monaco-editor 的东西（连 `import type` 都没有）。
 * 本文件被 CodeMirror 那条路径直接静态 import，一旦沾上 monaco 的模块说明符，
 * 就有把 monaco chunk 提升成首屏静态依赖的风险 —— 详见 utils/monacoEngine.ts 顶部。
 * ========================================================================== */

/**
 * 语言名 → Monaco 语言 id。
 *
 * 与上面的 resolveCodeEditorLanguage **逐条对齐**（别名 js / ts / py / sh / bash / yml / md
 * 一个不少）。两个引擎认的别名不一致时，表现是「同一个文件换个引擎高亮就没了」，不报错，
 * 所以两边只能一起改。
 *
 * 目标 id 全是恒等映射，跟 utils/monacoEngine.ts 里注册的那批语言一一对应。
 * 注意 shell 的 id 就叫 `shell`（不是 bash），这是 0.56 的
 * languages/definitions/shell/register.js 里写死的。
 *
 * 认不出来的值回落 'plaintext'：Monaco 与 CodeMirror 不同，它必须拿到一个**存在的**语言 id，
 * 给空字符串会让模型创建失败。
 */
export function resolveMonacoLanguage(language?: string): string {
  switch ((language || '').trim().toLowerCase()) {
    case 'javascript':
    case 'js':
      return 'javascript'
    case 'typescript':
    case 'ts':
      return 'typescript'
    case 'python':
    case 'py':
      return 'python'
    case 'shell':
    case 'bash':
    case 'sh':
      return 'shell'
    case 'go':
      return 'go'
    case 'json':
      return 'json'
    case 'yaml':
    case 'yml':
      return 'yaml'
    case 'markdown':
    case 'md':
      return 'markdown'
    case 'html':
      return 'html'
    case 'css':
      return 'css'
    case 'xml':
      return 'xml'
    default:
      return 'plaintext'
  }
}

/** defineMonacoTheme 的返回值：主题名给 setTheme 用，底色/前景色给外层容器用。 */
export interface MonacoThemeDescriptor {
  background: string
  foreground: string
  base: 'vs' | 'vs-dark'
  themeName: string
}

/**
 * 我们构造的主题数据。结构对齐 Monaco 的 IStandaloneThemeData，
 * 但**不** import 它的类型（理由见 defineMonacoTheme 的入参说明）。
 */
interface MonacoThemeData {
  base: 'vs' | 'vs-dark'
  inherit: boolean
  rules: Array<{ token: string; foreground?: string; fontStyle?: string }>
  colors: Record<string, string>
}

/**
 * defineMonacoTheme 的入参：只要求「有个能定义主题的 editor」。
 *
 * 刻意不写成 `import type { MonacoApi } from './monacoEngine'`：
 * 那行今天会被 TS 擦掉（verbatimModuleSyntax 下 `import type` 不产生运行时导入），
 * 但它离「有人顺手删掉 type 关键字」只差一个词，而本文件是 CodeMirror 路径的静态依赖 ——
 * 真变成值导入的那一刻，monaco chunk 就被提升进首屏，构建全绿、页面能用，纯静默劣化。
 * 用结构化最小类型把这条路彻底堵死，代价只是这一个 interface。
 */
interface MonacoThemeHost {
  editor: {
    defineTheme(themeName: string, themeData: MonacoThemeData): void
  }
}

/**
 * CSS 颜色 → Monaco 认的十六进制；认不出来回落 fallback。
 *
 * Monaco 的 colors 表只吃 `#RGB` / `#RGBA` / `#RRGGBB` / `#RRGGBBAA`
 * （base/common/color.js 的 parseHex），认不出来时 `Color.fromHex` 兜成 **Color.red** ——
 * 表现是那块区域变成一片正红，不报错。
 * 更糟的是 `editor.background` / `editor.foreground` 这两条会被 standaloneThemeService
 * 反手塞进一条 token 规则，那边的正则是严格的 `^#?[0-9a-f]{6}([0-9a-f]{2})?$`，
 * 三位简写会直接 **throw**（Illegal value for token color），把编辑器创建整个炸掉。
 *
 * 两类输入都必须先过这里：
 *   - 用户可配的编辑器底色/前景色：系统设置里那是自由文本，utils/panelAppearance.ts 的
 *     parseColor 明确支持 `#fff` 与 `rgb()/rgba()`，也就是说这两种写法是**合法输入**；
 *     而自定义属性的计算值不会被浏览器归一化，用户写什么这里读到什么。
 *   - 我们自己那几条带透明度的叠加色：CodeMirror 侧写成 rgba() 更好读，这里换成 8 位 hex。
 *
 * CodeMirror 侧不需要这一层：那边的值直接进 CSS，浏览器什么都认。
 *
 * fallback 只有用户可配的那两条需要显式传（传内置默认色）：宁可丢掉用户的自定义色，
 * 也不能让编辑器崩掉或变红。我们自己写死的调色板必然能解析，用默认的黑就够了。
 */
function toMonacoColor(value: string, fallback = '#000000'): string {
  return parseMonacoHex(value) ?? parseMonacoHex(fallback) ?? '#000000'
}

/** 将 CSS 颜色叠上指定透明度（0~1），转成 Monaco 可识别的 8 位十六进制。 */
function toMonacoAlphaColor(value: string, alpha: number, fallback: string): string {
  const baseHex = parseMonacoHex(value) ?? parseMonacoHex(fallback) ?? '#000000'
  const rgbHex = baseHex.slice(0, 7)
  const alphaByte = Math.max(0, Math.min(255, Math.round(alpha * 255)))
    .toString(16)
    .padStart(2, '0')
  return `${rgbHex}${alphaByte}`
}

function parseMonacoHex(value: string): string | null {
  const text = (value || '').trim().toLowerCase()

  const hexMatch = /^#([0-9a-f]{3,8})$/.exec(text)
  if (hexMatch) {
    const digits = hexMatch[1] ?? ''
    // #RGB / #RGBA：colors 表认，但上面说的那条 token 规则正则不认，统一展开成长写
    if (digits.length === 3 || digits.length === 4) {
      return `#${digits.replace(/./g, (ch) => ch + ch)}`
    }
    if (digits.length === 6 || digits.length === 8) {
      return `#${digits}`
    }
    // 5 位 / 7 位是打错了，交给 fallback
    return null
  }

  const rgbMatch = /^rgba?\(\s*(\d{1,3})\s*,\s*(\d{1,3})\s*,\s*(\d{1,3})\s*(?:,\s*([0-9.]+)\s*)?\)$/.exec(text)
  if (!rgbMatch) {
    return null
  }

  const toByte = (n: number) => Math.max(0, Math.min(255, n)).toString(16).padStart(2, '0')
  const channel = (raw: string | undefined) => toByte(Number.parseInt(raw ?? '', 10) || 0)
  const alphaRaw = rgbMatch[4]
  // 没写 alpha 就不补末两位：`rgb()` 本来就是全不透明，8 位写法与 6 位等价，短的更好读
  const alpha = alphaRaw === undefined ? '' : toByte(Math.round((Number.parseFloat(alphaRaw) || 0) * 255))
  return `#${channel(rgbMatch[1])}${channel(rgbMatch[2])}${channel(rgbMatch[3])}${alpha}`
}

/**
 * 按当前配色定义并注册 Monaco 主题，返回描述符。
 *
 * 与 buildCodeEditorTheme() 是同一件事的两个引擎版本，取值全部从那边抄过来（见下面每条注释），
 * 目标是**两个引擎看起来是同一个编辑器**，而不是「Monaco 保持它自己那套 vs / vs-dark」。
 * 这一点是**刻意偏离**改造前的 defineMonacoTheme —— 那时候 rules 是空数组、纯靠 inherit，
 * 语法色就是 Monaco 默认那套；现在既然两个引擎并存、还能随时互切，颜色不一致会非常刺眼。
 * inherit 仍然保留（true），用来兜住我们没覆盖到的 token，而 DARK_SYNTAX / LIGHT_SYNTAX
 * 两份调色板本来就是照着 vs / vs-dark 抄的，兜底值和我们写的值本来就同一路数。
 *
 * ⚠️ 与 CodeMirror 侧唯一的**实质**差别：Monaco 只吃字面 hex，
 * 塞 `var(--dd-editor-bg-color, ...)` 进去不是「静默忽略」，是被解析成 Color.red（一片正红）。
 * 于是「用户只改了编辑器底色、没切明暗」这种情况：
 *   - CodeMirror 侧靠 var() 自动重绘，主题都不用重建；
 *   - Monaco 侧**必须重新 defineTheme**（同名覆盖即可，Monaco 会刷新已挂载实例）。
 * 所以两个引擎的调用方都得监听 PANEL_APPEARANCE_CHANGE_EVENT 才算覆盖完整。
 *
 * 入参是结构化最小类型而不是 MonacoApi，理由见 MonacoThemeHost。
 */
export function defineMonacoTheme(monaco: MonacoThemeHost): MonacoThemeDescriptor {
  // 明暗判定复用 isCodeEditorDark()：底色是用户可配项，这条规则只能有一处（见那个函数的说明）
  const dark = isCodeEditorDark()
  const rawBackground =
    readRootCssVar(EDITOR_BACKGROUND_VAR) || getDefaultEditorBackgroundColor(isPanelDarkMode())
  const rawForeground = readRootCssVar(EDITOR_FOREGROUND_VAR) || getReadableTextColor(rawBackground)

  // 下面这几条的字面值与 buildCodeEditorTheme() 里的同名局部变量**逐字相同**，改一边就要改另一边，
  // 否则表现是「换个引擎缩进参考线/选区就变色了」。带 rgba() 的那几条由 toMonacoColor 转成 8 位 hex。
  const selectionColor = dark ? '#134e4acc' : '#bfdbfe'
  const activeLineColor = dark ? 'rgba(255, 255, 255, 0.05)' : 'rgba(15, 23, 42, 0.04)'
  const gutterColor = dark ? '#6b7280' : '#94a3b8'
  const caretColor = dark ? '#34d399' : '#2563eb'
  const indentGuideColor = dark ? 'rgba(255, 255, 255, 0.14)' : 'rgba(15, 23, 42, 0.12)'
  // 对应 CodeMirror 侧的 .cm-selectionMatch
  const selectionMatchColor = dark ? 'rgba(52, 211, 153, 0.18)' : 'rgba(37, 99, 235, 0.12)'
  // 对应 .cm-searchMatch（非当前项）与 .cm-searchMatch-selected（当前项）
  const searchMatchColor = dark ? 'rgba(234, 179, 8, 0.28)' : 'rgba(234, 179, 8, 0.35)'
  const searchMatchSelectedColor = dark ? 'rgba(234, 179, 8, 0.5)' : 'rgba(234, 179, 8, 0.6)'
  // 空白符标记与行号同色，与 buildCodeEditorTheme() 里的 whitespaceMarkColor 同源
  const whitespaceMarkColor = gutterColor

  const background = toMonacoColor(rawBackground, getDefaultEditorBackgroundColor(dark))
  const foreground = toMonacoColor(rawForeground, getReadableTextColor(rawBackground))

  // 查找框/浮层控件与 Diff 着色从当前面板主题令牌派生，使 Monaco 融入全部 7 套主题（明暗双态）
  const panelDark = isPanelDarkMode()
  const widgetBg = toMonacoColor(
    readRootCssVar('--dd-overlay') || readRootCssVar('--dd-surface-solid'),
    panelDark ? '#1e293b' : '#ffffff'
  )
  const widgetFg = toMonacoColor(
    readRootCssVar('--dd-text'),
    panelDark ? '#e5e7eb' : '#111827'
  )
  const widgetMutedFg = toMonacoColor(
    readRootCssVar('--dd-text-3') || readRootCssVar('--dd-text-2'),
    panelDark ? '#94a3b8' : '#64748b'
  )
  const widgetBorder = toMonacoColor(
    readRootCssVar('--dd-divider') || readRootCssVar('--dd-border-strong'),
    panelDark ? '#334155' : '#e2e8f0'
  )
  const inputBg = toMonacoColor(
    readRootCssVar('--dd-surface-solid'),
    panelDark ? '#0f172a' : '#ffffff'
  )
  const inputBorder = toMonacoColor(
    readRootCssVar('--dd-border-strong') || readRootCssVar('--dd-divider'),
    panelDark ? '#475569' : '#cbd5e1'
  )
  const accentRaw = readRootCssVar('--dd-accent') || (panelDark ? '#60a5fa' : '#2563eb')
  const focusBorder = toMonacoColor(accentRaw, panelDark ? '#60a5fa' : '#2563eb')
  const okRaw = readRootCssVar('--dd-ok') || (dark ? '#34d399' : '#16a34a')
  const errRaw = readRootCssVar('--dd-err') || (dark ? '#f87171' : '#dc2626')

  const color = dark ? DARK_SYNTAX : LIGHT_SYNTAX
  // token 规则里的 foreground 按 Monaco 惯例写成不带 `#` 的 6 位裸 hex。
  // 截到 6 位是防御性的：调色板目前全是 6 位，将来谁加一条带透明度的进来，
  // 8 位虽然那条正则也认，但 token 颜色本来就不支持透明度，截掉更符合预期。
  const hex = (value: string) => toMonacoColor(value).slice(1, 7)

  const themeName = dark ? 'dd-editor-dark' : 'dd-editor-light'
  const base: 'vs' | 'vs-dark' = dark ? 'vs-dark' : 'vs'

  const themeData: MonacoThemeData = {
    base,
    // 兜住我们没列到的 token（Monarch 各语言会吐出一大堆细分 token 名）
    inherit: true,
    // 与 buildHighlightStyle() 的那张表一一对应。左边是 Monaco/Monarch 的 token 名，
    // 右边是两个引擎共用的调色板字段。
    // 注：调色板里的 functionName 在这边用不上 —— Monarch 语法不区分「函数名」这个 token，
    // 函数名会落到 identifier 上，硬给它编一个 token 名只会写出一条永不命中的规则。
    rules: [
      // 与 CodeMirror 侧那条注释规则一样，**不给** fontStyle（issue #116-6）。
      // 这里是整个 fontStyle 键都不写，不能写成 fontStyle: 'normal'：Monaco 的解析器只认
      // italic / bold / underline / strikethrough 四个词，'normal' 是靠「认不出就当没设」容错生效的。
      // inherit: true 也不会把斜体带回来 —— 0.56 的内置 vs / vs-dark 里 comment 与根规则都没有 fontStyle。
      { token: 'comment', foreground: hex(color.comment) },
      { token: 'keyword', foreground: hex(color.keyword) },
      { token: 'keyword.control', foreground: hex(color.controlKeyword) },
      { token: 'string', foreground: hex(color.string) },
      { token: 'number', foreground: hex(color.number) },
      { token: 'type', foreground: hex(color.typeName) },
      { token: 'type.identifier', foreground: hex(color.typeName) },
      // ⚠️ identifier 刻意**不给**规则（issue #119）：Monarch 里 JS/TS/Python 的 tokenizer 都是
      // `"@default": "identifier"`，也就是所有非关键字的词——变量、函数名、属性、模块名——全吐 identifier。
      // 给它上色等于把代码正文整片染成深蓝 #001080（v3.2.2 起就是这样）。不给规则时它落到
      // defaultForeground，也就是下面 colors 里的 editor.foreground（浅色 #111827 / 深色 #e5e7eb），
      // 与 v3.1.1（那时 rules 是空数组）逐字一致。CodeMirror 侧 buildHighlightStyle() 同样不给，
      // 两个引擎必须一起改，否则切一下引擎正文就变色。
      { token: 'constant', foreground: hex(color.constant) },
      { token: 'operator', foreground: hex(color.operator) },
      { token: 'delimiter', foreground: hex(color.operator) },
      { token: 'tag', foreground: hex(color.tagName) },
      { token: 'attribute.name', foreground: hex(color.attributeName) },
      { token: 'regexp', foreground: hex(color.regexp) },
      { token: 'metatag', foreground: hex(color.meta) },
      { token: 'annotation', foreground: hex(color.meta) },
      { token: 'invalid', foreground: hex(color.invalid) }
    ],
    colors: {
      'editor.background': background,
      'editor.foreground': foreground,
      'editorLineNumber.foreground': toMonacoColor(gutterColor),
      // 当前行的行号。对应 CodeMirror 侧 buildCodeEditorTheme() 里的
      // `'&.cm-editor .cm-activeLineGutter': { color: foregroundValue }` —— 那边用的是**正文色**，
      // 所以这里直接给 foreground（它已经过 toMonacoColor，是可用的字面 hex）。
      // ⚠️ 不给这条不是「跟着 editorLineNumber.foreground 走」，而是回落到 base 主题的默认值：
      // vs 是 #0B216F（深蓝）、vs-dark 是 #c6c6c6，在我们这套浅灰行号里会突兀地跳出来。
      'editorLineNumber.activeForeground': foreground,
      'editorCursor.foreground': toMonacoColor(caretColor),
      'editor.selectionBackground': toMonacoColor(selectionColor),
      // CodeMirror 侧只有一档选区色（用的是原生 ::selection，本来就不区分聚焦与否），
      // 这里两条给同一个值，免得「点到别处去选区就变了个颜色」成为两个引擎的又一处差异。
      'editor.inactiveSelectionBackground': toMonacoColor(selectionColor),
      'editor.lineHighlightBackground': toMonacoColor(activeLineColor),
      'editorIndentGuide.background1': toMonacoColor(indentGuideColor),
      'editorWhitespace.foreground': toMonacoColor(whitespaceMarkColor),
      'editor.selectionHighlightBackground': toMonacoColor(selectionMatchColor),
      // ⚠️ 这两条的语义与 CodeMirror 侧**是反的**，别照着名字对：
      // Monaco 的 findMatch 指【当前那一个】匹配（对应 .cm-searchMatch-selected），
      // findMatchHighlight 才是其余匹配（对应 .cm-searchMatch）。接反了不报错，只是深浅颠倒。
      'editor.findMatchBackground': toMonacoColor(searchMatchSelectedColor),
      'editor.findMatchHighlightBackground': toMonacoColor(searchMatchColor),
      // 查找替换浮层 / 悬浮提示 / 自动补全浮层：跟随面板当前主题的表面与分隔线令牌
      'editorWidget.background': widgetBg,
      'editorWidget.foreground': widgetFg,
      'editorWidget.border': widgetBorder,
      'editorHoverWidget.background': widgetBg,
      'editorHoverWidget.foreground': widgetFg,
      'editorHoverWidget.border': widgetBorder,
      'editorSuggestWidget.background': widgetBg,
      'editorSuggestWidget.foreground': widgetFg,
      'editorSuggestWidget.border': widgetBorder,
      'editorSuggestWidget.selectedBackground': toMonacoAlphaColor(accentRaw, panelDark ? 0.24 : 0.14, focusBorder),
      'input.background': inputBg,
      'input.foreground': widgetFg,
      'input.border': inputBorder,
      'input.placeholderForeground': widgetMutedFg,
      'inputOption.activeBorder': focusBorder,
      'inputOption.activeBackground': toMonacoAlphaColor(accentRaw, panelDark ? 0.24 : 0.14, focusBorder),
      'focusBorder': focusBorder,
      // Diff 对比增删高亮：按当前主题 --dd-ok / --dd-err 与编辑器明暗派生柔和半透底色
      'diffEditor.insertedTextBackground': toMonacoAlphaColor(okRaw, dark ? 0.22 : 0.18, '#22c55e'),
      'diffEditor.removedTextBackground': toMonacoAlphaColor(errRaw, dark ? 0.22 : 0.18, '#ef4444'),
      'diffEditor.insertedLineBackground': toMonacoAlphaColor(okRaw, dark ? 0.14 : 0.11, '#22c55e'),
      'diffEditor.removedLineBackground': toMonacoAlphaColor(errRaw, dark ? 0.14 : 0.11, '#ef4444'),
      'diffEditor.border': widgetBorder,
      'diffEditorGutter.insertedLineBackground': toMonacoAlphaColor(okRaw, dark ? 0.28 : 0.22, '#22c55e'),
      'diffEditorGutter.removedLineBackground': toMonacoAlphaColor(errRaw, dark ? 0.28 : 0.22, '#ef4444')
    }
  }

  // 同名重复定义是合法的：Monaco 会替换旧数据，并在该主题正是当前主题时自动刷新已挂载的实例
  // （standaloneThemeService.defineTheme 末尾那句 setTheme）。
  // 所以「用户只改了编辑器底色」时调用方重调本函数就够了。
  // 但**明暗切换时主题名会变**（dd-editor-dark ⇄ dd-editor-light），那种情况下调用方还必须
  // 拿返回的 themeName 再调一次 monaco.editor.setTheme()，否则新主题定义了却没被用上。
  monaco.editor.defineTheme(themeName, themeData)

  return { background, foreground, base, themeName }
}
