# 组件规范

> 适用于 `views/*/*.vue`、`components/*.vue`、`layouts/*.vue`。

---

## 总原则

- 优先让组件**一眼能看懂**。
- 组件拆分以“职责边界明确”为前提，不以“文件越小越好”为目标。
- 如果一段逻辑只服务于当前组件，并不会复用，优先留在当前组件内。
- 复杂交互、边界分支、兼容逻辑建议补中文注释。

---

## 组件拆分边界

### 适合拆出去的情况

- 某块 UI 在多个页面/多个弹窗里复用。
- 当前页面太长，某个局部区域已经有独立职责。
- 某块区域本身就有清晰输入输出，比如表单、详情面板、日志查看器。

### 不适合拆出去的情况

- 只是为了“每个文件行数少一点”。
- 逻辑高度依赖父组件内部状态，拆出去反而传一堆 props 和 events。
- 只会出现一次、且本身并不复杂。

---

## 文件内部结构建议

Vue 单文件组件通常按下面顺序组织：

1. `template`
2. `script setup`
3. `style`

在 `script setup` 内部建议顺序：

1. import
2. 类型定义
3. props / emits
4. 响应式状态
5. 计算属性 / 侦听
6. 事件函数 / 业务函数
7. 生命周期

---

## props 和 emits

- props 要尽量语义清楚，别用模糊命名。
- 如果组件只是局部页面组件，也不要为了抽象强行设计很复杂的 props API。
- 对外事件名尽量直接体现动作，例如“保存”“关闭”“刷新”。
- 对业务对象较复杂的场景，优先传明确结构对象，不要传大量分散字段。

---

## 样式方式

- 当前项目以 `scss/css` 为主，样式应尽量贴近组件或页面目录。
- 页面专属样式优先就近放置。
- 多个设置卡片共用样式时，可以像现有项目一样提取共享 scss 文件，但前提是确实存在复用。
- 不要为了统一视觉把页面样式过度抽象成很难追踪的公共类名。

---

## 交互与可读性

- 表单、弹窗、抽屉、表格操作应保持用户路径清晰。
- 错误提示、确认弹窗、空状态文案要直接明确，不要写得太“技术化”。
- 当交互状态较多时，建议用清晰的状态变量名，不要混成一个难懂的大对象。

---

## 常见错误

- 一个页面拆出过多薄组件，导致阅读要来回跳文件。
- 把页面局部逻辑硬做成“通用组件”，结果 props/emits 变得复杂。
- 没有中文注释，导致状态切换或边界逻辑难理解。
- 同一类弹窗、卡片、表单在不同地方写出完全不同的交互风格。

---

## 移动端全屏弹窗：为什么 media 块里要单独写一遍 `.is-fullscreen`

> 记录日期 2026-08-08（基线 v3.0.1 发现），**v3.0.2 已修复**。
> 保留本节是为了防止有人「顺手清理冗余」把修复删掉。

**曾经的现象**：`≤768px` 下所有全屏弹窗高度是满的，左右各短 4vw，露出两条遮罩边。

**根因是 CSS 层叠，不是写错**：

```scss
// Element Plus 自己的实现（node_modules/element-plus/theme-chalk/el-dialog.css）
.el-dialog               { width: var(--el-dialog-width, 50%); }   // 无 !important
.el-dialog.is-fullscreen { --el-dialog-width: 100%; height: 100%; }  // 也无 !important，且不写 width

// global.scss，在 @media screen and (max-width: 768px) 里
.el-dialog {
  --el-dialog-width: 92vw !important;
  width: 92vw !important;      // ← 带 !important，直接盖掉上面那条 width
}
```

EP 是**通过改变量**实现全屏的，而这里是**直接写 width 且带 `!important`**。
带 `!important` 的声明无条件胜过不带的（与特异性、源码顺序都无关），
所以 `.is-fullscreen` 那条 `--el-dialog-width: 100%` 根本没机会参与计算。

**修法**：在**同一个 media 块内**补 `&.is-fullscreen`，`--el-dialog-width` 与 `width` **两条都要写**。
只补 `width` 渲染结果就已经对了，但变量会停在 92vw，留下「变量值与实际宽度不一致」的状态。

不能改成 `.el-dialog:not(.is-fullscreen)` 来规避：那样非全屏弹窗会回落到组件自己的
`width` prop（`600px` / `1100px` 这类写死值），在窄屏上溢出——92vw 这条兜底正是防它的。
也不能指望改 `global.scss:341-350` 那个非 media 的 `&.is-fullscreen` 块：它在 media 生效时
压不过 `92vw !important`；给它加 `!important` 又会让桌面端也被强制 100%。

`margin` / `max-height` / `border-radius` **不需要跟着改**，三者已各自就位
（`:348` 把 `--el-dialog-margin-top` 置 0 使 margin 解析成 `0 auto`；`:347` 的 `max-height: 100dvh`；
`:278` 全站圆角归零）。往 media 块里再塞 `margin: 0 !important` 只会掩盖 `:348` 的作用。

**影响面**：`:fullscreen=` 绑定 **46 处 / 26 个文件**，另有 **2 处静态 `fullscreen`**
（`ScriptExecutionDialogs.vue:47` 与 `:110`，桌面端也全屏），合计 **48 个全屏弹窗 / 27 个文件**。
改 `global.scss` 一处即可覆盖全部，**调用点一个都不用动**。

**⚠️ 下面两处曾被误记为「局部绕过宽度」，实际都不是**（2026-08-10 逐行核实推翻）：

- `ScriptExecutionDialogs.vue:316-322`——注释里的「用双 class 提高特异性」讲的是
  **覆盖全局进场动画**，块体只有 `animation` 与 `transform-origin`，**没有 width**。
  别去「清理」它：`:324-328` 的注释解释了为何刻意不加 `both/forwards`——残留 transform
  会成为编辑器 `position:fixed` 浮层（补全 / 搜索面板）的包含块，导致弹层错位。
  （v3.2.0 前这里写的是 Monaco 的补全浮层；换成 CodeMirror 6 后这条约束**依然成立**，
  只是浮层换成了 `.cm-tooltip` / `.cm-panels`。）
- `LogViewer.vue:1088-1094`——确实重声明了 `&.is-fullscreen`，但**五条全部没有 `!important`**，
  所以其中的 `width: 100%` 一直是死代码，压不过 global 那条。它真正生效的是
  `height` / `max-height` / `border-radius`（用来压住本组件自己的桌面端尺寸）。

全库 `is-fullscreen` 仅 5 处命中，已确认**没有第三、第四处绕过点**，不必再全站找一遍。

**为什么必须走「特异性 + !important」而不是靠后写覆盖**：`vite.config.ts` 用的是
`ElementPlusResolver({ importStyle: 'css' })`，EP 组件样式随各 `.vue` 按需导入注入，
相对 `global.scss` 的位置不确定，dev 与 build 还可能不同。任何依赖源码顺序的写法都不可靠。

## Scenario: 日志查看器中的 `\r` 单行覆盖刷新

### 1. Scope / Trigger
- Trigger: 修改任务日志查看器、执行日志详情、日志文件预览这类终端风格日志组件时必须看本节。
- 原因: 任务脚本常输出进度条，裸 `\r` 不是“换行”，而是“把光标移回当前行开头并覆盖原内容”。如果日志组件在每个流式分片到达时都强行追加新行，进度条就会刷屏。

### 2. Signatures
- 任务实时日志组件: `web/src/views/tasks/components/LogViewer.vue`
- 执行日志详情页: `web/src/views/logs/index.vue`
- 日志文件预览: `web/src/views/tasks/components/LogFileBrowser.vue`

### 3. Contracts
- 渲染规则必须区分三类边界:
  - `\n`: 真正落一新行
  - `\r\n`: 真正落一新行
  - 裸 `\r`: 清空当前行并等待后续字符覆盖
- 流式日志组件不能在“每个数据分片结束”时默认把当前 tail 强制 push 成一整行。
- 只有确认遇到真实换行，或者该分片本身没有覆盖语义时，才允许落行为历史行。

### 4. Validation & Error Matrix
- 纯文本日志 -> 展示行为与原来一致
- `下载中 10%\r下载中 20%\r下载中 30%\n` -> 最终只保留一条当前进度行
- 如果在 `requestAnimationFrame` flush、buffer flush 或 computed 渲染里把每个 chunk 直接 `join('\\n')` -> 进度条刷屏，属于错误实现

### 5. Good/Base/Bad Cases
- Good: 实时任务日志、执行日志详情、日志文件详情对同一份包含裸 `\r` 的内容展示结果一致
- Base: 没有 `\r` 的普通多行日志，仍按原有 `pre-wrap` 展示
- Bad: 任务页能单行刷新，但“执行日志”页和“日志文件”页又恢复成多行刷屏

### 6. Tests Required
- 前端验证: `cd web && npm run build`
- 手工回归点:
  - 任务页 `LogViewer` 中查看运行中的进度条脚本
  - 执行日志页打开同一任务的日志详情
  - 日志文件弹窗查看对应落盘文件

### 7. Wrong vs Correct
#### Wrong
```ts
detailContent.value += sseBuffer.join('\n') + '\n'
```

```ts
if (commitBoundary) {
  pushLogLine()
}
```

#### Correct
```ts
detailContent.value = mergeTerminalText(detailContent.value, chunk)
```

```ts
if (commitBoundary && !endedWithLineBreak && !sawCarriageReturn) {
  pushLogLine()
}
```

## Scenario: 代码编辑器（CodeMirror 6 默认引擎 + Monaco 可选引擎）

> v3.2.0（issue #109-2）把编辑器从 Monaco 换成了 CodeMirror 6。
> **本节整体取代了原来的「Monaco 本地静态资源与加载探测」一节** ——
> 被删掉的是那一整套**加载管线**：「运行期 AMD 加载 + 本地 `/monaco/vs` 资源探测 + CDN 兜底 +
> 构建后整目录拷贝」，连同 `web/scripts/copy-monaco-assets.mjs`、`web/src/utils/monaco.ts`、
> `@monaco-editor/loader`、`vite-plugin-monaco-editor` 一起删除。
> 在旧提交、旧发布说明里看到那套契约，那是历史陈述，**不要照着往回加**。
>
> v3.2.2（issue #114）把 **Monaco 这个包**作为可切换的第二引擎加了回来，但走的是**另一条路**：
> `monaco-editor@0.56.0` 精确版本 + `monaco-editor/editor/editor.api` 裁剪 ESM 导入 +
> `await import()` 懒加载，产物由 Vite 打进 `dist/assets/`。
> 也就是说上面那条禁令**一个字都没作废**：仍然没有运行期加载器、没有 CDN 兜底、
> 没有构建后资源拷贝，`server/main.go` 的静态白名单一行没动（`assets` 本来就覆盖了）。
> **禁的是那套管线，不是 Monaco 本身。**
>
> v3.2.4（issue #116）动了三处：
> ① 偏好从「`CodeEditor.vue` 里的 6 个具名导出 + 纯 localStorage」搬到
> `web/src/utils/editorPreferences.ts`，并升级成「服务端 `/api/auth/preferences` 为真源 +
> localStorage 当首屏/离线缓存」，于是偏好**跨浏览器、跨设备跟着人走**；
> ② 空白符从二态布尔改成三态 `none/selection/all`，缩进宽度做成 `auto/2/4/6/8` 可配；
> ③ 缩略图**只在 Monaco 引擎下保留**，CodeMirror 侧的自研实现（`utils/codeMinimap.ts`，265 行）已删除。
> 下面所有带 v3.2.4 标注的条目都是这一版的产物，旧提交里的 6 个具名导出与 `showWhitespace`
> 是历史陈述，**不要照着往回加**。
>
> v3.2.4 的对抗性代码审查之后又补了四条契约，它们各自解决一类**只在真实使用中才暴露**的静默失效：
> `GET /api/auth/preferences` 新增 `stored`（升级用户的本地偏好不再被首屏默认值冲掉）、
> `CodeEditor.vue` 新增 `engine-resolved` emit（页面判断当前引擎的唯一入口）、
> Monaco `tabSize` 全进程共享带来的两条硬规则、状态条 `overflow: hidden` 的常驻标约束。
> 详见下方对应小节。

### 1. Scope / Trigger
- Trigger: 修改下面「2. Signatures」列出的任意一个文件，
  或它们那 5 个调用点（脚本管理页 / 配置文件页 / 代码运行器 / 调试弹窗 / 版本对比弹窗）时必须看本节。
- 为什么 CodeMirror 是默认引擎、而且 `auto` 在触摸设备上**永远不会**选 Monaco：
  Monaco 是**自绘编辑器** —— 文本渲染在 `.view-lines` 里、原生选择被关掉，
  选区与光标是它自己画的 DOM 层，触摸事件被内部手势层接管。
  于是移动端「长按出系统菜单 / 双击出选择手柄 / 按住拖动光标」三条**只要还用 Monaco 渲染就都不可能满足**，
  不是「少配了某个选项」。CodeMirror 6 的内容区是 `contenteditable`，选区就是原生 `Selection`。
- 这段话在 v3.2.0 是「为什么换引擎」的理由；v3.2.2 之后它换了个位置继续生效 ——
  它是 `resolveEditorEngine()` 里那两条**硬回落**（coarse pointer / 窄屏 → `codemirror`）的唯一依据。
  别看到「现在有两个引擎了」就把那两条改成可配。

### 2. Signatures

**组件（2 个分发层 + 4 个引擎实现）**
- 共用编辑器分发层: `web/src/components/CodeEditor.vue`（v3.2.4 起**只剩 `script setup`**，见下方偏好契约）
- 差异编辑器分发层: `web/src/components/CodeDiffEditor.vue`
- CodeMirror 实现: `web/src/components/CodeMirrorEditor.vue` / `CodeMirrorDiffEditor.vue`（`@codemirror/merge`）
- Monaco 实现: `web/src/components/MonacoEditor.vue` / `MonacoDiffEditor.vue`

**utils**
- 编辑器偏好（v3.2.4 新增，服务端同步）: `web/src/utils/editorPreferences.ts`
- 引擎偏好与 `auto` 解析: `web/src/utils/editorEngine.ts`（**刻意不参与服务端同步**，理由见契约）
- Monaco 的**唯一**导入边界: `web/src/utils/monacoEngine.ts`
- 语言映射 + 主题 + 缩进检测（两个引擎共用）: `web/src/utils/codeEditor.ts` ——
  `resolveCodeEditorLanguage()` / `buildCodeEditorTheme()` / `isCodeEditorDark()` /
  `resolveMonacoLanguage()` / `defineMonacoTheme()` / `detectIndentWidth()`
- CodeMirror 侧自建扩展: `web/src/utils/indentGuides.ts`（缩进参考线）、
  `web/src/utils/selectionWhitespace.ts`（v3.2.4 新增，仅选中时显示空白符）
- ⚠️ `web/src/utils/codeMinimap.ts`（自研 CodeMirror 缩略图，265 行）**v3.2.4 已删除**，
  缩略图现在只有 Monaco 侧有原生实现。在旧提交里看到它是历史陈述，不要重建。

**服务端（偏好落库）**
- `GET|PUT /api/auth/preferences`（`middleware.JWTAuth()`，不限角色），per-user 偏好表，
  **不复用**全局 `system_configs` —— 那张表是「面板级配置」，偏好是「这个人的」，混进去会让
  两个用户互相覆盖，并且非管理员没有写权限。

**依赖**
- `@codemirror/{state,view,language,commands,search,autocomplete,merge,legacy-modes}` + `@codemirror/lang-*` + `@lezer/highlight`
- `monaco-editor`：`package.json` 里是**精确版本 `0.56.0`、不带 `^`**（全表唯一一条没有 caret 的依赖）。
  `monacoEngine.ts` 里那一长串 contrib / language 深路径 import 走的是包内私有路径，
  它自己的注释就记了两处 0.56 专属事实：`exports` 映射换过写法
  （`monaco-editor/editor/editor.api` 才对，老教程里的 `monaco-editor/esm/vs/editor/editor.api`
  会被解析成 `esm/vs/esm/vs/...` 直接 404），以及 `languages/definitions/` 下没有 json 目录。
  想放开成 caret 的话，先把那份 import 清单逐条在新版本上验一遍再说。

**这几条否定式全都仍然成立**
- **没有**运行期加载器、**没有** CDN 兜底、**没有**构建后资源拷贝：CodeMirror 由 Vite 直接打包。
- Monaco 是同一句话的另一半：它走 **ESM + Vite code-split**，**不是** AMD。
  `from 'monaco-editor/...'` 全仓只允许出现在 `utils/monacoEngine.ts`，
  其余地方一律 `await import('@/utils/monacoEngine')`（要标类型只能 `import type { MonacoApi }`）。
  产物落 `dist/assets/monaco-*.js`，由 `vite.config.ts` 的 `manualChunks` 单独成块。

### 3. Contracts

#### 编辑器偏好（`utils/editorPreferences.ts`，v3.2.4 起的唯一真源）
> v3.2.3 及以前，这一坨是 `CodeEditor.vue` 顶部普通 `<script>` 块里的 6 个具名导出
> （`EditorWordWrap` / `readStoredEditorWordWrap` / `persistEditorWordWrap` /
> `EditorViewOption` / `readStoredEditorViewOption` / `persistEditorViewOption`），当时的契约是
> 「这 6 个导出必须留在 `CodeEditor.vue`」。**v3.2.4 已整体删除并搬到 utils**，那条旧契约作废：
> 偏好现在要发网络请求，而组件文件不是发请求的地方。

- **存储模型是两层，顺序不能反**：`localStorage` 是首屏 / 离线 / 隐私模式下的**同步缓存**
  （编辑器挂载那一刻必须立刻拿到值，不能等网络），服务端 `/api/auth/preferences` 是**真源**。
  `ensureEditorPreferencesLoaded()` 异步拉回来后写回本地缓存并派发一次变更事件做二次刷新。
  这套范式抄的是 `utils/panelAppearance.ts` 里 `panel_shape_style` 那条链路，**不要另发明一套**。
  ⚠️ 「拉回来就写回」**有两个前置条件**，别按无条件覆盖实现：服务端必须 `stored === true`，
  且本次会话里已被本地改过的键要跳过。见下方「首屏偏好同步的两条铁律」。
- **5 项偏好 + 默认值**（`EDITOR_PREFERENCES_DEFAULTS`）：
  `word_wrap: 'on'` / `minimap: false` / `indent_guides: true` /
  `whitespace: 'selection'` / `indent_width: 'auto'`。
  ⚠️ **默认值刻意各不相同**，判据是「打开它会不会打扰到不需要它的人」：参考线噪声极低所以默认开，
  缩略图要吃掉正文右侧一条所以默认关，空白符取 Monaco/VS Code 的原生默认「仅选中」，
  缩进宽度取「自动」（钉死 2 会把 4 空格缩进的 Python 画成每 2 列一条线）。别顺手统一成一个值。
- 🔴 **服务端 `server/handler/user_preference.go` 里有一份逐字相同的默认值，两边必须一起改。**
  对不上的表现是「从没存过偏好的用户，在服务端值拉回来的那一瞬间观感跳一下」——
  不报错、构建全绿，只有肉眼能看见。
- **`setEditorPreference()` 的三步顺序是契约**：写本地缓存 → 派发 `EDITOR_PREFERENCES_CHANGE_EVENT`
  （`'dd:editor-preferences-change'`）→ 后台 `PUT` 同步。派发**必须**在存储与网络的成败之外：
  隐私模式写不进存储、或服务端不可达时，本次切换仍然必须立刻生效。
  漏掉事件的表现与引擎偏好那条一模一样——「菜单里切了、已经开着的编辑器纹丝不动，要刷新才生效」，不报错。
- **`PUT` 只提交改动的那一个键**，服务端做字段级合并。全量回写会让两个标签页各改各的开关时互相覆盖。
- **同步失败刻意不打扰用户**：本机已经生效了，弹一句「同步失败」除了制造焦虑没有别的作用。
- **`ensureEditorPreferencesLoaded()` 是记忆化的，且失败后不重置 promise**：未登录 / 老服务端没这个接口
  这类失败是稳定的，反复重试只会在每次切页时白发一轮请求。它由编辑器页面在挂载时调，
  **刻意不放进 `main.ts` 的 bootstrap** —— 编辑器不在首屏，没必要给每次打开面板都加一个请求。
- 🔴 **退出登录必须调 `resetEditorPreferencesCache()`**（已挂在 `stores/auth.ts` 的 `clearAuth` 里）：
  否则换个人登进来会拿着上一个用户的「已经拉过」记忆不放，看到的是别人的偏好。
- **传输层里 `indent_width` 是字符串**（`"auto"` / `"2"` / `"4"` / `"6"` / `"8"`），
  而 TS 类型 `EditorIndentWidth` 是 `'auto' | 2 | 4 | 6 | 8`（数字）。
  跨边界时只走 `serialize()` 与 `parseIndentWidth()`，别在别处手写 `Number()` / `String()`。
- **老键兼容不能删**：localStorage 键沿用 v3.2.2 的
  `dd:editor:word_wrap` / `:minimap` / `:indent_guides` / `:whitespace`（`dd:editor:indent_width` 是新增）；
  `whitespace` 从二态变三态时，老值 `'on'`→`'all'`、`'off'`→`'none'`。
  删掉这段映射的表现是老用户升级后开关被静默重置一次。

#### 引擎偏好（`utils/editorEngine.ts`，**刻意不参与服务端同步**）
- `EditorEngine = 'auto' | 'codemirror' | 'monaco'`，解析后 `ResolvedEditorEngine = 'codemirror' | 'monaco'`；
  localStorage 键 `dd:editor:engine`，默认 `'auto'`，只认两个显式值、其余一律回落 `auto`。
- `persistEditorEngine()` 写完存储**必须**派发 `EDITOR_ENGINE_CHANGE_EVENT`（`'dd:editor-engine-change'`），
  且派发要放在 try/catch **之外** —— 隐私模式下写不进存储时，本次切换仍然必须生效。
  漏掉事件的表现是「菜单里切了、已经开着的那个编辑器纹丝不动，要刷新页面才生效」，不报错。
  两个分发层都靠监听它来重新解析引擎，且解析时**重新读一遍 localStorage**（存储才是唯一真源）。
- `resolveEditorEngine()` 里 **coarse pointer 与 ≤ `TABLET_BREAKPOINT`（1024）是硬回落 `codemirror`**，
  且**先判指针再判宽度**，顺序不能反：只看宽度会漏掉 1024~1366 的平板横屏与触屏笔电。
- `auto` **只在挂载时解析一次**，刻意不接 `useResponsive()` 的响应式宽度：
  响应式会让「桌面窗口从 1100 拖到 900」当场拆掉 Monaco 重建 CodeMirror，撤销历史 / 光标 / 滚动位置全丢。
- 阈值从 `useResponsive` 具名导入，**不要在这里重抄一个 `1024`**。
- 🔴 **`dd:editor:engine` 刻意留在纯 localStorage，不进 `/api/auth/preferences`。**
  理由不是「懒得加」：它带着 `auto` 的**设备自适应**语义（coarse pointer 与窄屏硬回落 `codemirror`）。
  把桌面上显式选的 `monaco` 同步到手机，等于把手机用户锁死在一个**用不了**
  「长按出系统菜单 / 双击出选择手柄 / 按住拖动光标」的引擎上；而他在手机上改回来，
  又会把桌面的选择顶掉，两头都难受。
  一句话记法：**引擎跟设备走，偏好跟人走。** 别看到「v3.2.4 偏好都能同步了」就顺手把引擎也塞进去。

#### props（普通编辑器 10 个，差异编辑器 8 个，分发层与两个实现逐字相同）
- 普通编辑器：`modelValue` / `language` / `readonly` / `minHeight` / `fillHeight` /
  `wordWrap`（默认 `'on'`）/ `minimap`（默认 `false`）/ `indentGuides`（默认 `false`）/
  `whitespace`（`'none' | 'selection' | 'all'`，默认 `'none'`）/
  `indentWidth`（`'auto' | number`，默认 `2`）。
- ⚠️ v3.2.4 的两处 prop 变更：**删掉 `showWhitespace: boolean`**（被三态的 `whitespace` 取代），
  **新增 `indentWidth`**。老名字在旧提交里还能看见，那是历史陈述。
- **prop 默认值一律等于「加这些功能之前的行为」**，所以不传它们的调用点（调试弹窗 / 代码运行器）
  一个像素都不会变：`whitespace` 默认 `'none'`（不是偏好里的 `'selection'`），
  `indentWidth` 默认 `2`（不是偏好里的 `'auto'`），`indentGuides` 默认 `false`（不是偏好里的 `true`）。
  🔴 **prop 默认值与偏好默认值刻意不相等，别去「统一」它们**：偏好默认值描述的是
  「脚本页/配置文件页这两个正经编辑场景该长什么样」，prop 默认值描述的是
  「没人管这个开关时不要改变现状」。偏好的值是页面侧读出来、当 prop 显式传下去的。
- ⚠️ **`indentWidth` 上「不传就等于不改变现状」只对 CodeMirror 成立**：Monaco 的 `tabSize`
  是全进程共享的，不传等于把**全局**值改成 `2`，连带改掉旁边那个编辑器。
  所以 prop 默认值照旧留 `2`（组件自己的兜底），但**会解析成 Monaco 的调用点必须显式传**，
  见下方「Monaco 的 `tabSize` 是全进程共享的配置」。
- 差异编辑器：`originalValue` / `modifiedValue` / `language` / `readonly` / `renderSideBySide` /
  `ignoreTrimWhitespace` / `hideUnchangedRegions` / `contextLineCount`。无 emit、无 `defineExpose`。
- 🔴 **一边加 prop，另一边必须同步加**。分发层是逐个显式 `:xxx="props.xxx"` 往下传的
  （刻意不写 `v-bind="props"` —— 那样 vue-tsc 就逐个查不了类型），两边对不上不报错，
  只会在切到某个引擎时静默丢掉一个开关。
  ⚠️ **`minimap` 也在此列，它不是 props 上的例外**：三个文件都声明了它，分发层无条件往下传，
  只不过 CodeMirror 侧那份是刻意的 no-op。详见下面「`minimap`：功能上的唯一例外」那一节。
- emit 不适用「三处逐字相同」：`engine-resolved` 是**分发层独有**的，两个实现都不加，见后文。

#### `minimap`：**功能上**的唯一例外，但 props 仍然三处逐字相同 🔴
> ⚠️ 2026-09-07 更正：本节此前写的是「`CodeMirrorEditor.vue` 上没有这个 prop、分发层只在 monaco 分支传它」，
> 那是**被否决掉的方案 A**，与代码相反，也与上一节「分发层与两个实现逐字相同」自相矛盾。
> 实际选的是方案 B，结论如下（完整推演在 `CodeMirrorEditor.vue` 那个 prop 的注释里）。

- 先把两件事分开，混起来正是上面那条记反的成因：
  - **props 清单**：`minimap` 在 `CodeEditor.vue`（分发层）、`MonacoEditor.vue`、`CodeMirrorEditor.vue`
    三个文件里**都有**，分发层是**无条件** `:minimap="props.minimap"` 往下传的。
    上一节那条「分发层与两个实现逐字相同」对它**同样成立，没有例外**。
  - **功能**：`CodeMirrorEditor.vue` 上那个 `minimap?: boolean`（默认 `false`）是**刻意的 no-op**，
    声明它只是为了把分发层传下来的值吃掉。缩略图只剩 Monaco 一侧（它原生就画，白送）。
- 🔴 **别把 `CodeMirrorEditor.vue` 里那个 prop 声明删掉**，也别在那里「顺手实现一下缩略图」。
  删掉的表现是 CodeMirror 根节点上多一个 `minimap="false"` 脏属性
  （Vue 会把不认识的 prop 当 attr 落到根上），一眼能看见，不会静默丢功能。
- 🔴 **也别把分发层改成「只在 monaco 分支才 v-bind」**（那就是被否决的方案 A）。
  一旦分发层不再是「一个个显式 `:xxx` 往下传」，vue-tsc 对这个 prop 的逐项类型检查**当场消失**
  （上一节「刻意不写 `v-bind="props"`」讲的就是这件事），将来再加 Monaco 专属选项时漏进那个对象是**静默**的。
  选 B 的判据是两条失效模式的**响度**：B 坏了根节点上多个脏属性，肉眼可见；A 坏了什么都看不见。
- 为什么 CodeMirror 侧不做缩略图：v3.2.2 那版是自研的
  `utils/codeMinimap.ts`（265 行，自己算像素几何、自己挂覆盖物、自己处理触摸）。
  它是这个编辑器里**最脆**的一块——它守的那两条铁律（见下方「编辑器覆盖物」）一旦被后人
  「顺手优化」掉，代价是移动端原生选区被静默毁掉。为了一个「小地图」背这么大风险不划算，
  所以 v3.2.4 直接删掉 CodeMirror 侧的实现，缩略图退化成 **Monaco 专属能力**。
- 齿轮菜单里那一项**按当前解析出来的引擎条件显示**：CodeMirror 下不渲染这一项。
  🔴 别改成「渲染成 disabled」——用户会以为是坏了；也别改成「无条件显示」——
  在 CodeMirror 下点了没反应，正是本文档反复强调的那种静默失效。
  页面判断「当前实际是哪个引擎」只能听 `engine-resolved`，不能自己 computed，见下一条。
- 偏好里 `minimap` 这个键**仍然保留并照常同步**：用户在 Monaco 下开着它，切到 CodeMirror
  只是暂时看不见，切回去还在。别因为「CodeMirror 用不上」就把这个键从偏好里删掉。
- 这是本节唯一被批准的**功能性**例外。**再想开第二个，先在这里写清理由**，
  不然默认按「一边加，另一边必须同步加」执行。

#### `engine-resolved`：页面判断「当前实际是哪个引擎」的唯一入口 🔴（v3.2.4）
- `CodeEditor.vue` 新增 emit：`'engine-resolved': [engine: 'codemirror' | 'monaco']`。
  它是**分发层独有**的 —— `CodeMirrorEditor.vue` / `MonacoEditor.vue` 两个实现都**不加**这个 emit。
  上面那条「三处逐字相同」讲的是 props，不覆盖它：这是分发层自己的能力，不是要往下传的东西。
- 分发层**每次解析出引擎都要 emit 一次**：挂载时一次，
  监听 `EDITOR_ENGINE_CHANGE_EVENT` 重新解析时再一次。少一次就有一个状态对不上。
- 🔴 **页面必须用它判断当前引擎，不能自己写 computed 调 `resolveEditorEngine()`。**
  理由：`resolveEditorEngine('auto')` 读的 `matchMedia` 与 `innerWidth` **都不是响应式的**，
  页面那份 computed 只在它自己的依赖变了才重算；而编辑器会在 `v-if` 翻转（切文件 / 开关弹窗）时
  **按当下的窗口宽度重新解析一遍**。两者必然分叉。
  表现是「菜单里点了缩略图没反应、按钮还高亮着」——页面以为在 Monaco，实际渲染的是 CodeMirror。
  不报错，构建与 vue-tsc 全绿。

#### 🔴 Monaco 的 `tabSize` 是**全进程共享**的配置（v3.2.4）
- 事实依据：`standaloneCodeEditor.js` 的 `create()` 与 `updateOptions()` 都把编辑器选项写进
  **全局的** `StandaloneConfigurationService`，`modelService.js` 再把它广播给**所有活着的 model**。
  于是页面上第二个 Monaco 实例改一下缩进宽度，第一个实例的正文缩进会跟着一起变。
- 由此两条硬规则，缺一不可：
  1. 🔴 **每一个会解析成 Monaco 的 `<CodeEditor>` 调用点都必须显式传 `indent-width`**，不能吃 prop 默认值。
     吃默认值在这里**不等于「保持现状」**，而是**把全局值改成 2**，顺手把旁边那个编辑器一起改了。
     典型现场：脚本页开着（用户偏好 4）→ 点开调试弹窗（不传 → 全局被改成 2）→ 主编辑器缩进当场变 2。
     ⚠️ 这条与「prop 默认值一律等于加功能之前的行为」不冲突：那条讲的是**组件自己**的默认值该取什么，
     这条讲的是**调用点**不许省着不传。
  2. 🔴 **`MonacoEditor.vue` 里那道「model options 漂移就顶回来」的自愈守卫不许删。**
     它监听模型选项变化，发现 `tabSize` 不是我们算出来的那个值就再写回去，
     兜的是「别处的 Monaco 实例把全局值改掉了」这种够不着的来源。
     **它为什么不会死循环**：写回之前先比一次，只有「当前值 ≠ 目标值」才写；
     写回之后两者已经相等，下一次回调里那个判断不成立，链条自己停住。
- 这两条 CodeMirror 侧都不需要：它的 `indentUnit` / `EditorState.tabSize` 挂在**各自的 EditorState** 上，
  实例之间天然隔离。别看到「CodeMirror 不用这么写」就把 Monaco 侧的删掉。

#### 首屏偏好同步的两条铁律（v3.2.4）🔴
- `GET /api/auth/preferences` 的响应带一个 `stored`（布尔）：
  **有这个用户的记录 且 记录里的偏好能解析成 JSON 对象**才是 `true`；
  没记录 / 空串 / 脏 JSON 一律 `false`（后两种当成「没存过」，正好让前端把本机那份重新迁上去）。
  `PUT` 的响应恒为 `"stored": true`。
  `editor` 字段本身**不受它影响**：无论 `stored` 是什么都下发一整套可用的值，
  不认得 `stored` 的老客户端行为与加这个字段之前逐字一致。
- 🔴 **`stored !== true` 时必须走【上行迁移】，绝不许下行覆盖**：
  把 `readEditorPreferences()` 那 5 项一次性 `PUT` 上去，
  **一个本地键都不改，也不派发变更事件**（本地值本来就是当下生效的值，什么都没变，
  派发只会让已经开着的编辑器白重配一轮）。
  照老写法逐项写回的表现是**升级用户的本地偏好在首屏被服务端默认值静默冲掉** ——
  v3.2.3 及以前偏好只在 localStorage，升级到本版后第一次 GET 拿到的必然是那套默认值。
- 🔴 **`stored === true` 走下行写回时，必须跳过本次会话里已经被本地改过的键。**
  首屏 GET 是异步的，用户完全可能在它回来之前就点了某个开关（慢网下尤其容易撞上）。
  不跳过的表现是「刚点的开关自己弹回去了」，而且只在网络慢时复现，本机开发几乎撞不到。
- 这两条与「`PUT` 只提交改动的那一个键」是配套的三件套，别只实现其中一两条。

#### 编辑器状态条：`overflow: hidden`，加常驻标之前先算清楚谁被挤掉 🔴
- `ScriptsEditorPane.vue` 的 `.editor-statusbar` 里，`.status-group` 带 `overflow: hidden`：
  装不下**不换行、也不滚动，而是从右边缘直接裁掉**。
- 而最右侧的「编辑中 / 只读」是这条状态条上最要紧的一项——它回答「我现在改得动吗」。
  🔴 **它必须永远可见**。任何往这一组里新加的**常驻**标都会先挤它。
- 所以加常驻项之前必须先想清楚窄窗口下谁被裁掉：要么给新项加窗口宽度条件，
  要么让它非常驻（只在真有话说的时候才出现）。
- 失效模式是纯视觉的：不报错、构建全绿，只有把窗口拖窄才看得见。

#### 根 class 与 CSS 变量（🔴 两个引擎必须逐字相同）
- 普通编辑器：根 class 固定 `code-editor-wrapper`（+ `--fill`），最小高度变量固定 `--code-editor-min-height`，
  内层容器 `code-editor-container`。
- 差异编辑器：根 class 固定 `code-diff-wrapper`，内层 `code-diff-container`。
- **不要**因为「这是 Monaco 的文件」就改回 `monaco-editor-wrapper` / `--monaco-editor-min-height` ——
  被删掉那版 Monaco 用的正是这套名字，照搬回来是**静默失效**：
  `config-file/index.vue` 那条 `.editor-card :deep(.code-editor-wrapper) { min-height: 560px }`
  给窄屏兜的下限会失灵，`ScriptsEditorPane.vue` 的 `.code-editor { flex: 1; height: 100%; min-height: 0 }`
  也是穿透到这个根节点上的。表现是编辑器高度塌陷，而构建与 vue-tsc 全绿。
- 唯一允许两边不同的是差异编辑器的内层宽度：CodeMirror 侧 `.code-diff-container` 是
  `calc(100% - 10px)`（给自绘的差异总览标尺留槽），Monaco 侧必须是 `100%`（它的标尺画在编辑器内部）。
  照抄过去会多出一条 10px 死带。

#### 分发层的模板形状（🔴 `CodeEditor.vue` 与 `CodeDiffEditor.vue` 都适用）
- 必须是**单个 `<component :is>` 根节点**：不套 wrapper `<div>`、**不在根上放注释**、**不写 `<style>`**。
- 套 wrapper 的后果：三个调用点靠 `$attrs` 穿透给编辑器根节点加样式 ——
  `ScriptsEditorPane` 传 `class="code-editor"`，调试弹窗与代码运行器传内联
  `style="height: 100%; min-height: 0"`，版本对比弹窗传 `class="version-diff-editor"`
  （`flex: 1 1 0; min-height: 0`，只对弹窗 flex 容器的**直接子元素**成立）。
  套一层之后这些 class / style 落在中间那层 div 上，真正的编辑器根节点一点样式都拿不到，
  表现是高度塌陷或把弹窗顶穿，而构建与类型检查全绿。
- 在根上放注释的后果：dev 构建会保留注释节点、根退化成 Fragment，
  透传要靠 Vue 的 dev 专用 Fragment 透传路径兜 —— 平白让 **dev 与 prod 走两条不同的路径**。
- 写 `<style scoped>` 没有意义：scoped 样式只能命中子组件的根节点，
  `.code-editor-container` 与那几条 `:deep(.cm-*)` 都命中不到，它们必须跟着各自的实现走。

#### 每次输入即时 emit
- **每次输入即时 `emit('update:modelValue')`**：CodeMirror 侧在 `EditorView.updateListener` 里判 `docChanged`，
  Monaco 侧在 `onDidChangeModelContent` 里。等失焦才 emit 会让「未保存」角标、保存按钮 disabled、
  调试弹窗的脏标记全部滞后。
- 两侧都要先与 `props.modelValue` 比一次再 emit，否则外部同步进来的值会和父组件转成回环。

#### 运行期改配：两个引擎各有各的唯一姿势 🔴
- **CodeMirror 侧：可重配的选项一律用 `Compartment`。** 当前 7 个：
  `language` / `readonly` / `wordWrap` / `indentGuides` / `whitespace` / `indentWidth` / 主题。
  新增可配项时「create 的 `extensions` + `Compartment.of` + `watch` 里 `reconfigure`」**三处必须一起补**。
  （光标横向跟随刻意挂在 `wordWrap` 那个 compartment 里 —— 换行开着时它本来就不会横向溢出，
  不必再多一个 compartment 和一个 watch。）
- **`indentWidth` 那个 compartment 一次要吐两条**：`indentUnit.of(' '.repeat(n))` 与
  `EditorState.tabSize.of(n)`，v3.2.4 前这两个是写死的常量。
  🔴 **两条必须一起换**：只换 `indentUnit` 的话，回车自动缩进对了，但正文里的 `\t`
  仍按老宽度渲染；只换 `tabSize` 则反过来。而 `indentGuides` 的参考线是拿
  `getIndentUnit()` 和 `state.tabSize` **两个值**算列位的（见下方自建扩展那条），
  漏掉任一条都会让参考线与实际缩进错位。
- **`indentWidth === 'auto'` 由我们自己算**：`utils/codeEditor.ts` 的
  `detectIndentWidth(content): 2 | 4 | 6 | 8` 扫一遍内容取最常见的缩进步长。
  🔴 **刻意不依赖 Monaco 的 `detectIndentation`**，理由见下面那条踩坑——
  它的时机不可控，而我们要的是两个引擎在同一份内容上**得到同一个数**。
- **Monaco 侧：运行期改配一律 `editor.updateOptions({...})`。**
  新增可配项时「create 的 `options` + `updateOptions` + `watch`」**三处必须一起补**。
- **换语言是 Monaco 侧的例外**：必须 `monaco.editor.setModelLanguage(model, id)`。
  语言挂在 **model** 上，`updateOptions` 里的 `language` 只对 `create` 生效。
- 两边的失效模式**完全相同且同样安静**：点了开关没反应、不报错、`npm run build` 与 `vue-tsc` 全绿。
  两个版本的 Wrong/Correct 见下面第 7 节。
- Monaco 侧另有三个「必须显式给、不能吃默认值」的选项：
  `renderWhitespace` 直接吃 `props.whitespace`（三态与 Monaco 的取值恰好同名，
  但**关掉时必须显式给 `'none'`** —— 它的默认值就是 `'selection'`，不给等于没关干净）；
  `renderLineHighlight` 要给 `'all'`（默认 `'line'` 不涂行号栏那一格，而 CodeMirror 侧同时开着
  `highlightActiveLine()` 与 `highlightActiveLineGutter()`，不给就是换个引擎当前行少半截底色）；
  **`detectIndentation` 必须显式写死 `false`**（默认是 `true`，会自己按内容猜缩进），理由见下条。

#### 🔴 踩坑：Monaco 的 `detectIndentation`（issue #116 报的真 bug，F5 随机跳）
> 现象：同一个脚本，什么都不改，F5 几次缩进宽度在 2 和 4 之间随机跳。

- Monaco 的 `detectIndentation` **默认是 `true`**，而它只在 **TextModel 构造那一瞬间**
  按当时的内容猜一次 `tabSize` / `insertSpaces`；**`setValue()` 永远不会重猜**。
- 脚本页的加载顺序是「**先挂编辑器（此时内容还是空串或占位）、再 `await` 拉内容回来 `setValue()`」。
  于是两个异步谁先到就决定了它猜出 2 还是 4：内容先到就按真实内容猜（多半对），
  编辑器先挂上就按空内容猜（吃 Monaco 的兜底值）。**这就是 F5 随机跳的全部成因**，
  跟用户偏好、跟文件本身都没关系。
- 还有第二条更隐蔽的路径：Monaco 的 `ModelService` 缓存键与 `updateOptions` 的入参不完全一致，
  于是**任何一次带 `wordWrap` / `renderWhitespace` 的 `updateOptions()` 都可能触发一次重猜**，
  把我们刚设好的 `tabSize` 顶掉。表现是「切了下自动换行，缩进宽度自己变了」。
- **修法只有一条：`create()` 的 options 里显式 `detectIndentation: false`**，
  缩进宽度完全由我们自己的 `detectIndentWidth()` 算完再 `updateOptions({ tabSize, indentSize })` 下发。
- 🔴 别用「在 `setValue()` 之后补一次 `model.detectIndentation()`」来绕：那等于把不确定性
  从「构造时机」搬到「每次 setValue 时机」，而且 CodeMirror 侧没有对应物，两个引擎会算出不同的数。
- 这个坑**构建、类型检查、CI 全绿**，只有连按几次 F5 才看得见。改动 Monaco 侧 options 时不要顺手删掉它。

#### 主题
- **主题必须吃 `--dd-editor-bg-color` / `--dd-editor-fg-color`**，并监听 `PANEL_APPEARANCE_CHANGE_EVENT` 重绘 ——
  编辑器底色是设置页的用户可配项（`editor_background_color`），写死颜色等于把这个功能废掉。
- **Monaco 侧不能只在「明暗翻转了」时才换主题**：它的 `colors` 表只吃字面 hex、塞不进 `var()`
  （塞了会被解析成正红），底色是被**烘进**主题数据里的。用户只改了底色没切明暗时，
  CodeMirror 侧靠 `var()` 自动重绘、主题都不用动，Monaco 侧必须重新 `defineTheme`。
  这一层差异统一收在 `codeEditor.ts` 的 `defineMonacoTheme()` 里，两个 Monaco 组件都调它。
- 明暗二选一的颜色（语法色 / 行号色 / 选区色 / 差异标尺的红绿）只能在 JS 里按
  `isCodeEditorDark()` 判一次 —— 底色是用户可配项，明暗规则只能有一处真源，
  否则改了默认底色只同步到一边，表现是「浅色底上一条深色标尺」。
- **注释不再是斜体**（v3.2.4）：`codeEditor.ts` 里注释相关的两处 `fontStyle: 'italic'` 已删除。
  等宽字体的斜体多半是**合成倾斜**（没有真的 italic 字形），在中文注释与小字号下糊得厉害。
  🔴 **`tags.emphasis` 那条斜体要留着，别连坐删掉**：它是 Markdown 的 `*强调*`，
  是文档内容本身的语义（原文就要求倾斜），跟「注释用斜体」完全是两回事。
  想验证的话看一个 `.md` 文件里的 `*斜体*` 还倾不倾斜。

#### 只在 CodeMirror 侧成立的两条（Monaco 侧写了是死代码）
- **移动端三件套不能少**：`spellcheck="false"` / `autocapitalize="off"` / `autocorrect="off"`。
  手机输入法默认句首大写 + 自动纠错，会**静默改坏脚本内容**。
- **≤768px 内容区字号提到 16px**：`index.html` 的 viewport 没有 `maximum-scale`（也不该加），
  14px 输入区在 iOS 上聚焦会自动放大整页。
- 这两条不必往 Monaco 侧补：它是自绘编辑器，正文字号来自 JS 的 `fontSize`、不吃容器 CSS `font-size`；
  而且它**永远不会被发到触摸设备上**（`auto` 硬回落，显式选 `monaco` 的用户是自己做的选择）。

#### 自建 CodeMirror 扩展的铁律（缩进参考线 / 仅选中空白符 / 任何覆盖物）
> 当前自建扩展有两个：`indentGuides.ts`（缩进参考线）、`selectionWhitespace.ts`（v3.2.4 新增，
> 仅选中时显示空白符）。第三个 `codeMinimap.ts` 已在 v3.2.4 删除，但**下面「编辑器覆盖物」那两条
> 铁律是随它一起写下来、却不随它一起作废的**——它们讲的是这个编辑器的底层约束，不是缩略图的私事。
> 所有这些条款守的是同一件事：**本编辑器刻意没有启用 `drawSelection()`，内容区靠的是原生 `Selection`。**
> 这也是「为什么不装现成的社区包」的答案。

**通用条款 A：任何要挂在编辑器上的覆盖物（不限缩略图）🔴**
- **DOM 必须挂在 `view.dom`（`.cm-editor`）上，绝不能碰 `view.contentDOM`。**
  CodeMirror 的 `DOMObserver` **只**监听 `contentDOM`，且 `childList + subtree` 全开。
  往里插一个节点再删掉（社区包 `@replit/codemirror-minimap` 每帧插一个 `div.cm-line`
  量字体就是这么干的）会被读成一次真实 DOM 变更：
  `readMutation → markDirty → DOMChange → applyDOMChange` → 发现文本其实没变 →
  强制 `view.update([])` → **把原生选区整个重写一遍**，手机长按菜单与选择手柄当场作废。
- **`touch-action: none` 只许加在覆盖物自己身上**，漏到 `.cm-content` / `.cm-scroller`
  会把触摸滚动和「按住拖动光标」一起废掉。
- 这两条的失效模式都是**只在真机触摸下能看见**：桌面鼠标一切正常，构建、`vue-tsc`、CI 全绿。
  以后要加行内标记、批注气泡、诊断浮层、滚动条缩略指示之类的东西，一律照这两条办。
- 顺带一条同源约束：版本对比的差异总览标尺是 `.code-diff-container` 的**兄弟节点**，
  刻意做成编辑器 DOM 之外的覆盖物而不是 `ViewPlugin` —— 插件想接点击就得挂
  `EditorView.domEventHandlers`，那正好是这个编辑器绝不能碰的东西。别把它「优化」成插件。
- 几何一律走**像素**（`contentHeight` / `lineBlockAtHeight`）不走行数 ——
  走行数在自动换行与折叠下会对不上。

**通用条款 B：往正文里加视觉标记，只能用装饰、不能建节点 🔴**
- **缩进参考线必须用 `Decoration.line` + CSS 背景，不能用 `Decoration.widget`。**
  widget 会往 `.cm-line` 里塞真实 DOM 节点（还会顺带插一对 `.cm-widgetBuffer` 零宽 span），
  那些节点会落进原生选区范围，影响长按选择的落点，也可能被系统「拷贝」抄进剪贴板。
  `Decoration.line` 只往**已经存在**的 `.cm-line` 上加 class 和 style，一个新节点都不建，
  线是 `repeating-linear-gradient` 画的 —— 背景不是内容，进不了选区、也拷不走。
  写样式时只能用 `background-*` **长写法**：`background:` 简写会把 `background-color` 一并重置掉，
  当前行高亮（`.cm-activeLine` 只设了 `background-color`）会消失，且只在光标所在那一行复现。
- **`selectionWhitespace.ts` 必须用 `Decoration.mark`，同样不能用 `Decoration.widget`。**
  空格与制表符的可见符号是给 `.cm-selectionWs` 之类的 class 挂 `background-image` /
  伪元素画出来的，字符本身一个都不动。用 widget 把「·」「→」当真实字符插进去，
  等于**往用户的选区和剪贴板里塞了不存在的字符**——他 Ctrl+C 出去粘贴，脚本就坏了，
  而且这种坏法只有粘贴到别处才发现。

**`selectionWhitespace.ts` 专属：必须在 `update.selectionSet` 时重建 🔴**
- 它的 `update(u)` 判据里**必须包含 `u.selectionSet`**（连同 `docChanged` / `viewportChanged`）。
  漏掉的表现：选中一段，空白符不显示；再动一下光标或改个字才显示——像随机失灵。
- ⚠️ **这与 `indentGuides.ts` 刻意「光标移动不重算」的取向正好相反，两者都对，别去统一。**
  判据是「装饰内容依不依赖选区」：
  - 参考线只跟**文本缩进**有关，光标在哪跟它一点关系没有。它的判据只看
    `docChanged || viewportChanged || getIndentUnit() 或 state.tabSize 变了`。
    把 `selectionSet` 加进去，等于每按一次方向键就重扫一遍视口内所有行去算列位——
    长文件里方向键会发涩，而且什么都不会变。
  - 「仅选中空白符」的装饰**范围本身就是选区**，选区一变装饰必然过期，不重建就是错的。
- `indentGuides.ts` 的 update 判据 v3.2.4 起要**同时看 `getIndentUnit()` 与 `state.tabSize`**
  （缩进宽度可配之后这两个都会变）。只看其中一个的表现是「切了缩进宽度，参考线还停在老列位上」——
  正文缩进已经变了、线没跟上，看起来像画歪了。

### 4. Validation & Error Matrix
- CodeMirror 侧改了 prop 但没配 Compartment / watch -> 点了开关没反应、按钮还高亮，**构建与 vue-tsc 全绿**
- Monaco 侧只改了 `create()` 的 options、没补 `updateOptions` 的 watch -> **同样是点了开关没反应**，
  同样不报错、同样全绿；换语言若走 `updateOptions({ language })` 而不是 `setModelLanguage()`，
  表现是「切了文件高亮还停在上一个文件的语言上」
- 改了根 class / CSS 变量名没同步页面 `:deep()` -> 编辑器高度塌陷或移动端 `min-height` 失效，同样全绿
- 分发层套了 wrapper div 或在根上放了注释 -> `$attrs` 落到中间层，高度塌陷 / dev 与 prod 行为不一致
- 主题写死颜色 -> 设置页自定义底色失效、暗色串色；Monaco 侧只在明暗翻转时换主题 -> 改底色纹丝不动
- `monaco-editor` 的静态 import 出现在 `monacoEngine.ts` 以外的地方（含漏写 `import type` 的 `type`）
  -> Rollup 把 monaco chunk 提升成入口的静态依赖，`index.html` 里冒出 monaco 的
  `modulepreload` / `stylesheet`，**没切引擎的用户白下约 1 MiB**。构建成功、页面能用，纯静默劣化
- `dist/monaco` **目录**又出现 -> 说明有人把 `copy-monaco-assets.mjs` 那套老管线加回来了
  （注意与 `dist/assets/monaco-*.js` 区分，后者是本版的正常产物）
- 前端默认值与 `server/handler/user_preference.go` 的默认值对不上 -> 老用户无感，
  但**从没存过偏好的新用户**在服务端值拉回来的一瞬间观感跳一次，**构建与 vue-tsc 全绿**
- `setEditorPreference()` 里漏了派发事件、或把派发放进了 `try` / `.then` 里 ->
  隐私模式或离线时「切了没反应，刷新才生效」，不报错
- `PUT /api/auth/preferences` 改成全量回写 -> 两个标签页各改各的开关会互相覆盖，
  表现是「我刚开的参考线自己关了」，无任何报错
- `stored !== true` 时仍然逐项下行写回（或干脆不看 `stored`）-> **从 v3.2.3 升上来的用户，
  本机调好的偏好在首屏被服务端默认值静默冲掉**，一次性、不可逆、不报错
- 首屏 GET 回来时不跳过「本次会话已被本地改过的键」-> 慢网下「刚点的开关自己弹回去」，
  本机开发几乎撞不到，只有限速才复现
- 页面自己 computed 调 `resolveEditorEngine()` 判断当前引擎、不听 `engine-resolved` ->
  页面认定的引擎与编辑器实际渲染的分叉，表现是「点了缩略图没反应、按钮还高亮」，全绿
- 会解析成 Monaco 的 `<CodeEditor>` 调用点没传 `indent-width` -> 它把**全局** `tabSize` 改成 2，
  旁边那个正经编辑器的缩进跟着变（开一次调试弹窗，主编辑器缩进就变了）
- 删掉 `MonacoEditor.vue` 的 model options 自愈守卫 -> 同上，且没有兜底能把它顶回来
- 往 `.status-group` 里加常驻标 -> 窄窗口下最右侧的「编辑中 / 只读」被静默裁掉，
  纯视觉失效、构建全绿
- `clearAuth` 里漏调 `resetEditorPreferencesCache()` -> 换个账号登进来看到的是上一个人的偏好，
  刷新一次才恢复正常
- Monaco 侧没写 `detectIndentation: false` -> **F5 几次缩进宽度在 2/4 之间随机跳**；
  或者切一下自动换行，缩进宽度自己变了。构建、类型检查、CI 全绿
- `indentWidth` 那个 compartment 只换了 `indentUnit` 没换 `EditorState.tabSize`（或反过来）->
  回车缩进与 `\t` 渲染宽度不一致，且参考线与正文错位
- `selectionWhitespace.ts` 的 update 判据漏了 `selectionSet` -> 选中一段空白符不显示，
  再动一下光标才显示，像随机失灵
- 往 `view.contentDOM` 里插节点，或把 `touch-action: none` 加到 `.cm-content` / `.cm-scroller` ->
  **原生选区被重写 / 触摸滚动被废**，桌面鼠标完全正常，只有真机触摸能看见
- 顺手把 `tags.emphasis` 的斜体一起删掉 -> Markdown 的 `*强调*` 不再倾斜，没人会立刻发现

### 5. Good/Base/Bad Cases
- Good: 新增可配项时两个引擎一起补 —— CodeMirror 侧「create 的 extensions + Compartment + watch」，
  Monaco 侧「create 的 options + updateOptions + watch」，各自三处齐全
- Good: 新增 prop 时分发层、两个实现三个文件同步改，且分发层仍然逐个显式往下传
- Good: 新增一项偏好时，`utils/editorPreferences.ts` 的类型 / 默认值 / 序列化 / 拉取回写
  与 `server/handler/user_preference.go` 的默认值 + 校验**一起改**
- Base: 至少保证四个调用点默认值等于改动前行为（调试弹窗与代码运行器不传 `wordWrap` / `minimap` /
  `indentGuides` / `whitespace`，吃 `'on'` / `false` / `false` / `'none'`）。
  🔴 **`indentWidth` 是这条的例外，它必须每个调用点都显式传**：Monaco 的 `tabSize` 全进程共享，
  「不传」在那边不是「不改变现状」而是「把全局值改成 2」。别把它归回上面那一串里
- Bad: 只改 `EditorState.create({extensions})`（或只改 `monaco.editor.create()` 的 options），然后以为改完了
- Bad: 只给一个引擎加了开关，另一个引擎不动 —— 用户切一下引擎，那个开关就没反应了
- Bad: 把 prop 默认值「统一」成偏好默认值（比如给 `whitespace` 的 prop 也写 `'selection'`）——
  调试弹窗与代码运行器会跟着变样，而它们根本没打算改
- Bad: 看到「偏好都能同步了」顺手把 `dd:editor:engine` 也塞进 `/api/auth/preferences`
- Bad: 把 CodeMirror 侧自绘的差异标尺移植到 Monaco 侧 —— Monaco 的 `renderOverviewRuler` 原生就画这条，
  两条会打架

### 6. Tests Required
- 前端验证: `cd web && npm run build`（含 `vue-tsc -b`）
- 构建后检查（`.github/workflows/checks.yml` 里「断言 Monaco 已打包且未进首屏」那一步已经有这对正反断言，
  位置在 `build:demo` 之前，因为那一步会先清空 `dist`）:
  - `web/dist` 下**不应**出现 `monaco` **目录** —— 出现即说明老管线被加回来了
  - `dist/assets/monaco-*.js` **必须存在** —— 不存在说明 Monaco 压根没被打包，
    此时下面那条反向断言变成恒真的空转门禁
  - `dist/index.html` 里**不得**出现指向 `/assets/monaco-` 的 `modulepreload` 或 `stylesheet` ——
    出现即懒加载失效，最常见成因是 `vite.config.ts` 里把 `vite/preload-helper` 钉到 `app-core` 的
    那条 `manualChunks` 规则被删了
- 手工回归点（仓库没有前端测试，这一段是唯一保护）。
  🔴 **除标注了引擎的条目外，下面每一条都要在 CodeMirror 与 Monaco 两个引擎下各跑一遍**:
  - 四处编辑器 ×（只读 / 编辑）×（Wrap on / off）
  - 缩进参考线开关一次真的生效
  - **空白符三态各验一遍**（关闭 / 仅选中 / 始终）：「仅选中」下**先选中一段再看**，
    并且**选中后按 Ctrl+C 粘贴到别处**确认没混进「·」「→」这类不存在的字符
  - **缩进宽度五档各验一遍**（自动 / 2 / 4 / 6 / 8）：正文缩进、`\t` 渲染宽度、
    回车后的自动缩进、缩进参考线的列位**四者要对齐**
  - **同一个脚本连按 10 次 F5，缩进宽度必须每次一样**（这条是 issue #116 的直接复现路径，
    成因是 Monaco 的 `detectIndentation`，别只测一次就过）
  - 保存后「未保存」角标消失；退出编辑态后确实改不动
  - 明暗主题切换、设置页自定义编辑器底色即时生效
  - **注释不是斜体**；同时打开一个 `.md` 文件确认 `*强调*` **仍然是斜体**（两者别连坐）
  - 版本对比弹窗：并排 / 统一两种模式，「忽略空白差异」开关；滚动条侧的差异标记能点、能跳
  - 引擎切换本身：齿轮菜单里切一下，**当前已经开着的编辑器**就换掉（不用刷新）；刷新后仍然记得
  - **仅 Monaco 引擎下**：缩略图开关一次真的生效。
    🔴 v3.2.4 起 CodeMirror 侧没有缩略图，齿轮菜单里那一项在 CodeMirror 下**不渲染** ——
    要顺带确认「切到 CodeMirror 后菜单里确实看不到这一项」，而不是看到了点不动。
    别把它写成无条件验收项，后人在 CodeMirror 下找不到开关会误判成回归。
  - **仅 CodeMirror 引擎下**：移动端长按出系统菜单、双击出选择手柄、按住能拖动光标。
    🔴 这三条**在 Monaco 下是不可能满足的**，那正是 `auto` 在触摸设备上硬回落 CodeMirror 的理由。
    别把它们写成无条件验收项 —— 后人在 Monaco 下测一遍会误判成回归。
  - **仅 Monaco 引擎下：开一次调试弹窗，主编辑器的缩进宽度不许变。**
    脚本页把缩进宽度调成 4（或任意非 2 的值）→ 点开调试弹窗 → 关掉 → 回看主编辑器正文缩进仍是 4。
    这条查的是「每个 Monaco 调用点都显式传了 `indent-width`」加那道自愈守卫，
    成因是 Monaco 的 `tabSize` 全进程共享。CodeMirror 下这条恒过，测了没意义
  - **窄窗口下状态条读数完整**：把窗口拖到最窄（或 ≤768px）→ 状态条最右侧的
    「编辑中 / 只读」必须仍然看得见，不能被别的常驻标从右边缘挤没
- 跨设备偏好回归点（v3.2.4 新增，**这一组只需各跑一遍，不必两个引擎都跑**）:
  - 在 A 浏览器改几项偏好 → **换一个浏览器 / 换一台设备**用同一个账号登录 → 偏好还在
  - **退出登录，换另一个账号登进来 → 看到的是他自己的那份偏好**，不是上一个人的
    （漏调 `resetEditorPreferencesCache()` 时这条会红）
  - 两个标签页同时开着，在其中一个改一项 → 另一个刷新后跟上，且**先前改的其它项没被顶掉**
    （这条查的是「PUT 只提交改动的键」）
  - 断网 / 隐私模式下改一项 → **本次切换仍然立刻生效**、不报错、不弹「同步失败」
  - **升级路径（`stored === false`）**：用一个从没存过偏好的账号（或先把它那行记录清掉），
    在本机把几项偏好调成非默认值 → 打开编辑器页 → **本机那几项必须还在**（不许被默认值冲掉），
    且随后 `PUT` 已经把它们迁上去（再刷新一次仍然在，换个浏览器登同一账号也能看到）
  - **慢网下点开关**：DevTools 把网络限速到 Slow 3G → 进编辑器页后**立刻**点一个开关 →
    首屏 GET 回来之后，那个开关**不许弹回去**（这条查的是「跳过本次会话已改过的键」）

### 7. Wrong vs Correct

两个引擎是同一条铁律的两半，下面两组例子是双胞胎：**只在创建那一刻读一次的配置，挂载后改它完全没反应且不报错。**

#### Wrong
```ts
// CodeMirror：只在创建那一刻读一次 —— 挂载后再切 prop 完全没反应，也不报错
EditorState.create({ extensions: [props.wordWrap === 'on' ? EditorView.lineWrapping : []] })
```

```ts
// Monaco：同一个毛病。create 的 options 也只读一次，
// 而且 language 只对 create 生效 —— 后面再 updateOptions 传 language 是彻底的空操作。
monaco.editor.create(el, { minimap: { enabled: props.minimap }, language: resolveMonacoLanguage(props.language) })
```

#### Correct
```ts
// CodeMirror：Compartment + watch，两处一起写
const wrapCompartment = new Compartment()
// create 时：wrapCompartment.of(...)
watch(() => props.wordWrap, (value) => {
  view?.dispatch({ effects: wrapCompartment.reconfigure(value === 'on' ? EditorView.lineWrapping : []) })
})
```

```ts
// Monaco：普通选项走 updateOptions；语言挂在 model 上，只能走 setModelLanguage
watch(() => props.minimap, (enabled) => {
  editor?.updateOptions({ minimap: { enabled } })
})
watch(() => props.language, (next) => {
  const model = editor?.getModel()
  if (!model || !monacoApi) return
  monacoApi.editor.setModelLanguage(model, resolveMonacoLanguage(next))
})
```

缩进宽度（v3.2.4）另有一组，坏法不是「没反应」而是**随机**：

#### Wrong
```ts
// Monaco：不写 detectIndentation，等于默认 true —— 它在 TextModel 构造那一刻按当时的内容
// （脚本页此时往往还是空的）猜一次，setValue() 之后永不重猜，于是 F5 随机跳 2/4
monaco.editor.create(el, { tabSize: resolveIndentWidth(props.indentWidth, props.modelValue) })
```

```ts
// CodeMirror：只换了 indentUnit，tabSize 还是写死的 —— 回车缩进对了，
// 但正文里的 \t 与缩进参考线的列位还停在老宽度上
indentCompartment.reconfigure(indentUnit.of(' '.repeat(width)))
```

#### Correct
```ts
// Monaco：先关掉它自己的猜测，宽度完全由我们算
monaco.editor.create(el, { detectIndentation: false, tabSize: width, indentSize: width })
```

```ts
// CodeMirror：一个 compartment 吐两条，缺一不可
indentCompartment.reconfigure([indentUnit.of(' '.repeat(width)), EditorState.tabSize.of(width)])
```

---

## Scenario: 通知渠道表单 / 系统配置表单的字段定义

### 1. Scope / Trigger

改动涉及「某个渠道有哪些配置字段」或「系统设置页显示哪些配置项」时触发。
这两类知识的**真源在服务端**，Web 不得再持有副本。

### 2. Signatures

- `GET /api/notifications/types` -> `[{type, name, fields: NotifyFieldDefinition[]}]`
  真源：`server/model/notify_channel_registry.go`（22 渠道 / 93 字段槽 / 56 唯一键）
- `GET /api/configs` -> `{data: {key: {...}}}`
  真源：`server/model/system_config_registry.go`（47 项）

### 3. Contracts

`NotifyFieldDefinition`：`key` / `label` / `widget` / `placeholder` /
`required` / `default` / `options[{value,label}]` / `show_when{key,values}`

- `widget` **只有四种**：`input` / `password` / `textarea` / `select`
- `show_when` **只支持单键等值命中**，不支持 OR / 表达式
- 渲染器读的是 `field.widget`，**不是 `field.type`**

`SystemConfigDefinition` 额外下发 `label` / `group_label` / `order` /
`secret` / `min` / `max`。注意 `order` 的第一项是 0，
服务端**绝不能给它加 `omitempty`**。

### 4. Validation & Error Matrix

- 通知渠道 config 的值必须**全是字符串** -> 非字符串的可逆类型服务端会转换，
  对象/数组返回 400 并点名键
- `notifier.go` 读了但 registry 没声明 -> `go test ./service` 红
- registry 声明了但 `notifier.go` 不读 -> 同上，**反方向也红**

### 5. Good/Base/Bad Cases

- **Good**：加渠道字段只改 `notify_channel_registry.go`，Web 与 APP 自动跟上
- **Base**：schema 缺表达力（如条件 options）时，先扩 schema 再让两端消费
- **Bad**：在 `index.vue` 里加一张字段表 / 在 computed 里做 `widget -> type`
  映射保住旧模板 —— 都是把漂移源搬回来

### 6. Tests Required

`server/service/notifier_schema_binding_test.go` 用 `go/ast` 扫
`notifier.go` 的 `cfg["..."]`，与 registry **双向**断言相等。
已做双向突变验证：两个方向各改一处，测试都会红。**不要绕过它。**

### 7. Wrong vs Correct

#### Wrong
```ts
// web/src/views/notifications/index.vue —— 曾经的 225 行
const configFields = computed(() => {
  switch (form.type) {
    case 'telegram': return [{ key: 'token', label: 'Bot Token', type: 'input' }, ...]
```

#### Correct
```ts
const currentChannelFields = computed(() =>
  channelTypes.value.find(t => t.type === form.type)?.fields ?? [])
```

> 历史：这份知识曾在 `notifier.go` / `index.vue` / APP / `apiData.ts`
> 四处各存一份，并且已经漂移过 —— `apiData.ts` 的 wecom_app 消息类型漏了
> `mpnews`，而另外两处都有。

> **系统配置侧走的是「专属表单 + schema 兜底」两层**：
> `useSettingsConfig.ts` 的 `configForm` 保留 41 个键的硬编码，因为它们绑着
> SVG 上传、取色器实时预览、图片压缩、镜像源弹窗、备份内容 CSV ↔ 复选框等定制控件；
> 其余项由 `settings/systemConfigSchema.ts` + `components/ExtraConfigCard.vue`
> 按服务端 schema 兜底渲染。**谁进兜底区是拿 `configForm` 的键去减算出来的**，
> 所以服务端加配置项时 Web 不用改，它会自己冒出来。
>
> 三项只读见 `READ_ONLY_CONFIG_KEYS`（巡检写入的机器状态 + `ddp service install`
> 写入的安装事实）：渲染成只读行，不隐藏、不回写。
> 保存统一走 `submitConfigs`，**只提交改动过的键** —— 服务端 `BatchSet` 逐键写入、
> 中途 400 时前面的键已经落库，全量回写会造成半保存状态。
