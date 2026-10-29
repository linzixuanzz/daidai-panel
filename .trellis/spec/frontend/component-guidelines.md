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
- 表单字段说明**不写常驻灰字段落**：易错字段在标签旁放 `DdFieldHelp` 气泡（一两句），不常用字段收进默认折叠的「高级设置」，
  完整规则放 README 与接口文档。约定见下方「Scenario: 表单字段说明」。

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
>
> 🔴 **现状（v3.3.2 / issue #144）**：global.scss 的 `≤768` 块已改成**无差别整屏**——
> `.el-dialog` 直接写 `width / max-width / height / max-height: 100%` + `margin: 0` + `border-radius: 0`（全部 `!important`），
> 不再有 `92vw`，也不再区分 `&.is-fullscreen` / `:not(.is-fullscreen)`（两支取值一样，分支没有意义）。
> **所以不要再往那个 media 块里补 `&.is-fullscreen`**。下面这段层叠分析仍然成立、也仍是本节的价值所在
> （它解释了为什么这类覆盖只能靠「特异性 + `!important`」，以及为什么组件侧的 `:fullscreen` 不能被全局规则替代），
> 移动端弹窗几何的现行契约见 `design-system.md` §5。

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

**⚠️ 页面侧只有下面这几处值得逐个交代**——前两处曾被误记为「局部绕过宽度」，实际都不是（2026-08-10 逐行核实推翻）；
第三处是 v3.3.2 新增的、**必须留着**的页面级重声明：

- `ScriptExecutionDialogs.vue:316-322`——注释里的「用双 class 提高特异性」讲的是
  **覆盖全局进场动画**，块体只有 `animation` 与 `transform-origin`，**没有 width**。
  别去「清理」它：`:324-328` 的注释解释了为何刻意不加 `both/forwards`——残留 transform
  会成为编辑器 `position:fixed` 浮层（补全 / 搜索面板）的包含块，导致弹层错位。
  （v3.2.0 前这里写的是 Monaco 的补全浮层；换成 CodeMirror 6 后这条约束**依然成立**，
  只是浮层换成了 `.cm-tooltip` / `.cm-panels`。）
- `LogViewer.vue:1088-1094`——确实重声明了 `&.is-fullscreen`，但**五条全部没有 `!important`**，
  所以其中的 `width: 100%` 一直是死代码，压不过 global 那条。它真正生效的是
  `height` / `max-height` / `border-radius`（用来压住本组件自己的桌面端尺寸）。
- 🔴 `logs/index.vue` 的 `:deep(.log-detail-dialog)` 里那条 `&.is-fullscreen`（v3.3.2 / issue #144 新增）——
  这条是**真·必要**，不是冗余：该弹窗自己写了 `height: clamp(680px, 85dvh, 920px)` 与 `max-height: calc(100dvh - 64px)`，
  必须在 `.is-fullscreen` 下把 `height` / `max-height` 一起显式压回 `100%`，否则移动端底部会空出一截。
  它靠 `:deep()` + `&` 编译出的 `[data-v-x] .log-detail-dialog.is-fullscreen` (0,3,0) 生效，刻意不写 `!important`
  （改 global.scss 那条 (0,2,0) 去压它，就得给全站所有全屏弹窗的高度加 `!important`）。「顺手清理冗余」时不要删。
  同理 `LogFileBrowser.vue` 的 `.task-log-files-dialog.is-fullscreen` 也不能删——它负责放开本组件写死的 810px，见下方
  「Scenario: 日志查看器中的 `\r` 单行覆盖刷新」里日志文件预览那条。

**凡是页面自己给 `.el-dialog` 写过 `height` / `max-height` 的，都要配一条 `&.is-fullscreen` 把它放开**，
否则移动端整屏规则会被页面自己的定值压住。全库 `is-fullscreen` 共 6 处命中（v3.3.2 起 `logs/index.vue` 与
`LogFileBrowser.vue` 各多一条，global.scss 移动端块里原来那条 `&.is-fullscreen` 分支则随之删除），
已确认**没有别的绕过点**，不必再全站找一遍。

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
  - 高度契约（v3.3.2 / issue #144）：它的 `el-dialog` 挂**全仓唯一**的非 scoped 类 `task-log-files-dialog`，
    整窗高度由该类下的定值 `810px` 推导（= 内容区 700 + 弹窗的框 109：global.scss 的弹窗 header 61 + body 上下 padding 48），
    内容区 `.log-files-browser` 只写 `height: 100%` 跟着吃满，移动端 `:fullscreen` 再由 `&.is-fullscreen` 把定值放开。
    🔴 **改 global.scss 的弹窗 header / body 内边距时要回来核对这个 810**；类名也不能改成 `log-files-dialog`
    （那个名字已被 `logs/index.vue` 那个 900px 宽的弹窗占着，撞名会把它一起拉成 810px 并截断）。

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
  v3.3.1 起同一个接口还承载列表页偏好（`list` 组，另一列），两组按组写入、互不碰对方的列，
  前端见下方「Scenario: 列表页偏好」。

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
  **有这个用户的记录 且 记录里的 editor 列能解析成 JSON 对象**才是 `true`；
  没记录 / 空串 / 脏 JSON 一律 `false`（后两种当成「没存过」，正好让前端把本机那份重新迁上去）。
  `stored` 只描述 editor 这一组，与 v3.3.1 加的 `list` 组无关。
  `PUT` 的响应：请求里带了 `editor` 组（哪怕是空对象 `{}`）时为 `true`；
  🔴 v3.3.1 起**不再恒为 `true`**：只带 `list` 组、或两组都不带的 PUT，按读库时的真实状态回。
  写死 `true` 的话，只改列表偏好的请求会让前端误以为 editor 存过，拿默认值把本机那份编辑器偏好冲掉。
  服务端按组写入的完整契约见 backend `quality-guidelines.md`「场景：用户界面偏好按组写入」。
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
- **≤768px 内容区字号提到 16px**：14px 输入区在 iOS 上聚焦会自动放大整页。
  `index.html` 的 viewport 不写 `maximum-scale`（安卓上会真的禁掉双指缩放）；v3.3.3 起 `main.ts` 只对 iOS 运行时追加
  `maximum-scale=1.0` 挡住全站的聚焦放大（iOS 10 起用户仍能双指缩放，见 `design-system.md` §5「iOS 聚焦输入框自动放大」）。
  这条 16px 照留：它不依赖 UA 判定，也更易读，别因为有了 `maximum-scale` 就删掉。
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

#### 查找面板（`utils/searchPanel.ts`，v3.4.1 issue #170）🔴
CodeMirror 自带面板换成了自己的中文面板。挂法固定两条一起挂：
`search({ top: true, createPanel: createSearchPanel })` + `searchPanelTheme`（`CodeMirrorEditor.vue`）。
- **面板对象必须自己带 `top: true`**：自定义面板不读 `search({ top })` 的配置，漏了面板跑到编辑器底部。
- **查找框必须带 `main-field="true"`**：`openSearchPanel`（Ctrl+F）靠它找输入框聚焦、带入选中文字；`mount()` 里 `select()`。
- **不要复用 `.cm-textfield` / `.cm-button`**：CodeMirror 基础主题把它们写成 70% 字号（14px 下约 10px，#170 说的「字很小」就是它）。
  一律用 `.cm-dd-search*` 自有类名；桌面 13px，≤768 的 16px 在 `CodeMirrorEditor.vue` 的媒体查询里用 `:deep()` 补（iOS 聚焦放大）。
- `update(u)` 三件事：别处发来的 `setSearchQuery`（`!value.eq(当前)`）同步回输入框与切换；`docChanged` / 查询变了才重扫匹配，
  `selectionSet` 只重算「第几个」；`startState.readOnly !== state.readOnly` 时显隐替换箭头与替换行。
- `keydown` 先走 `runScopeHandlers(view, e, 'search-panel')`（Esc、F3、Ctrl+G 都在这里），再处理 Enter / Shift+Enter；
  组字中（`isComposing` / `keyCode 229`）一律不处理，`input` 也等 `compositionend` 再提交。
- 匹配状态：查找框空 → 不显示；正则写错 → 「正则有误」；0 个 → 「无结果」；与主选区 from/to 完全相同的匹配是当前项 →
  「第 N 个，共 M 个」，否则「共 M 个」；计数到 1000 封顶显示「1000+」。
- 替换行默认收起，展开状态是模块级变量（本页记住、不存 localStorage）；去掉了自带面板的「all」（没开 `drawSelection`，多选区画不出来）。
- 查看态（没点「编辑」）正文是 `contenteditable=false` 且没有 tabindex，点不进焦点，Ctrl+F 归浏览器自己的查找——原本如此，测查找面板要先进编辑态。

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
  真源：`server/model/system_config_registry.go`（56 项）

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
- 系统配置：首个 `GET /configs` 失败或还没返回时点任一保存 -> `submitConfigs` 拦下、不发 `PUT /configs/batch`，
  提示「配置还没加载成功，请点刷新后再保存」/「配置还在加载，请稍后再保存」。
  去掉这道闸 = 「面板外观」保存把已存的 `rounded` 写回 `square`、「任务运行」保存把没显示的两项重置成默认值，不报错
- 系统配置：专属卡里给从兜底区挪来的项（依赖安装超时 / 检测脚本半路静默结束 / 界面圆角）或直接放进专属卡的项（v3.3.6 的任务结束后清理残留进程）写死标签、选项或 min/max ->
  服务端改了注册表后 Web 与 APP 显示分叉，无任何报错；一律读 `configSchema`

### 5. Good/Base/Bad Cases

- **Good**：加渠道字段只改 `notify_channel_registry.go`，Web 与 APP 自动跟上
- **Base**：schema 缺表达力（如条件 options）时，先扩 schema 再让两端消费
- **Bad**：在 `index.vue` 里加一张字段表 / 在 computed 里做 `widget -> type`
  映射保住旧模板 —— 都是把漂移源搬回来

### 6. Tests Required

`server/service/notifier_schema_binding_test.go` 用 `go/ast` 扫
`notifier.go` 的 `cfg["..."]`，与 registry **双向**断言相等。
已做双向突变验证：两个方向各改一处，测试都会红。**不要绕过它。**

系统配置侧没有前端自动化测试，改 `useSettingsConfig.ts` / 设置页卡片后浏览器实测：
- DevTools 拦掉 `GET /configs` 进「通用设置」：「面板外观」「其它配置项」「任务运行」「代理设置」的保存都只出「配置还没加载成功，请点刷新后再保存」，Network 里没有 `PUT /configs/batch`，页面圆角不变；放行后刷新、改一项再保存，请求体只有那一个键
- 「任务运行」：「依赖安装超时(分钟)」标题 / 说明 /「取值范围 5 - 720」/ 占位 20 都来自 schema；填 3 保存提示「「依赖安装超时(分钟)」需在 5-720 之间」且不发请求
- 「任务运行」：「任务结束后清理残留进程」开关排在「检测脚本半路静默结束」之后，标题与说明来自 schema、默认开；只拨它再保存，请求体只有 `cleanup_leftover_processes` 一个键
- 「其它配置项」只剩运行时日志输出、守护方式（只读）、systemd 服务名（只读）三项
- 演示站：清空「依赖安装超时」保存、切走再切回，输入框显示 20，改别的项能正常保存

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
> `useSettingsConfig.ts` 的 `configForm` 保留 50 个键的硬编码，因为它们绑着
> SVG 上传、取色器实时预览、图片压缩、镜像源弹窗、备份内容 CSV ↔ 复选框等定制控件
> （v3.2.9 又从兜底区挪进 3 项：依赖安装超时、检测脚本半路静默结束进「任务运行」卡，界面圆角进「面板外观」卡；
> v3.3.6 新增的任务结束后清理残留进程没进过兜底区，直接放进「任务运行」卡）；
> 其余项由 `settings/systemConfigSchema.ts` + `components/ExtraConfigCard.vue`
> 按服务端 schema 兜底渲染（挂在「通用设置」标签右栏，当前只剩「面板与运行时」组 3 项）。
> **谁进兜底区是拿 `configForm` 的键去减算出来的**，所以服务端加配置项时 Web 不用改，它会自己冒出来。
> 挪进专属卡的项**仍只认服务端 schema**：标签、说明、选项、min/max 读 `configSchema`，卡片里不抄值表。
>
> 三项只读见 `READ_ONLY_CONFIG_KEYS`（巡检写入的机器状态 + `ddp service install`
> 写入的安装事实）：不隐藏、不回写（上次检查更新时间在代理设置页只读展示，另两项在兜底区渲染成只读行）。
> 保存统一走 `submitConfigs`，**只提交改动过的键** —— 服务端 `BatchSet` 逐键写入、
> 中途 400 时前面的键已经落库，全量回写会造成半保存状态；**配置没成功加载过时直接拒绝提交**
> （空基准下差集会把整张卡连同没渲染的项一起按本地默认值写回）。细节见 `index.md` 第 2 节。

---

## Scenario: 表单字段说明——`DdFieldHelp` 气泡与「高级设置」折叠

> v3.2.9 精简新建 / 编辑订阅弹窗时定的口径：此前弹窗常驻 10 段约 1500 字说明，用户反馈「文字说明太多」。

### 1. Scope / Trigger

- 触发：给表单字段加说明文字、改 `web/src/components/ui/DdFieldHelp.vue`、调整新建 / 编辑类弹窗的字段分区时必须看本节。
  当前唯一调用方是 `web/src/views/subscriptions/index.vue` 的新建 / 编辑订阅弹窗（共 15 个气泡）。

### 2. Signatures

- `DdFieldHelp`：props `label: string`（必填，既是标签文字，也拼出按钮读屏名「<label>说明」）、
  `width?: number | string`（默认 `260`）；**默认插槽 = 气泡正文**；无 emit。
- 用法：`<el-form-item><template #label><DdFieldHelp label="白名单">一两句</DdFieldHelp></template>…</el-form-item>`
- 订阅弹窗：`showAdvanced`（ref，默认 `false`）、`advancedSummary`（computed 字符串）、
  `revealAdvancedForError(message: string)` + `ADVANCED_FIELD_ERROR_KEYWORDS`。

### 3. Contracts

**文案口径**
- 新建 / 编辑类弹窗**不再加常驻说明段落**。易错字段在标签旁放气泡，正文 1～2 句：用途 + 最容易踩的坑；
  placeholder 一行「例子 / 留空含义」。
- 完整规则写在 README（订阅管理一节）与接口文档 `views/api-docs/apiData.ts`，拉取日志的 `[提示]` 负责当场解释；
  别把「写全条件」的长段落加回表单，也别整段塞进气泡。
- 压成一两句时**限定语不能丢**，说法以后端代码为准：v3.2.9 复查抓到「依赖规则」气泡写成「命中即不建任务」，
  丢了「白名单留空时不影响建任务」，与 `isSubscriptionDependencyOnlyFile`（白名单优先）相反。
- 说明改成气泡后不再占表单宽度：**别再按「说明有多长」决定字段跨不跨两列**，只看控件本身放不放得进半列。

**`DdFieldHelp` 的交互（每条去掉都是静默失效）**
- 触发器是 14px 图标的 `<button type="button">`，放在 EP 渲染的 `<label for=控件id>` 里：
  按 HTML 规范，点 label 内的交互元素不会把点击转发给控件（不聚焦输入框、手机不弹键盘、不拨动开关）；
  不写 `type` 时在 `el-form` 渲染的 `<form>` 里默认是 submit。
- `el-popover trigger="click"`，**不用 hover，也不要 `['hover','click']`**：触屏 hover 靠模拟 mouseenter，开关不稳；
  两者并存时移入打开、点击又关掉；EP 的「点外面关闭」只在 trigger 不含 hover / focus 时才真的关。
- `:trigger-keys="[]"`：键盘交给原生 button 的 Enter / Space，留默认值时个别浏览器按一下 Space 开了又关。
- **Esc 只关气泡**：只在气泡打开期间（`@before-enter` 挂、`@before-leave` 拆、`onBeforeUnmount` 兜底）
  在 `document` 的**捕获阶段**监听 `keydown`，`key === 'Escape'` 且不在输入法组字中时 `stopPropagation()` + `popoverRef.hide()`。
  原因：click 型 popover 自己不响应 Esc（气泡里没有可聚焦元素，焦点陷阱不生效），
  而 `el-dialog` 的 Esc 挂在 document 冒泡阶段、与焦点无关。
- `QuestionFilled` 在组件里局部 import（全局注册的图标里没有）。视觉约定见 `design-system.md` §6.5。

**「高级设置」折叠**
- 基本区只留高频字段（订阅弹窗：一键识别〔仅新建〕、名称、类型、URL、白名单、黑名单、依赖规则、定时拉取），其余进默认折叠的高级区。
- 折叠用 link 按钮 + **`v-show`（不是 `v-if`）**：折叠时字段仍挂载，`v-model` 与保存逻辑和展开时完全一样；按钮带 `aria-expanded` / `aria-controls`。
- `showAdvanced` 在 `openCreate` 与 `openEdit` 里**都**复位成 `false`：弹窗没有 `destroy-on-close`，不复位会沿用上次的展开状态。
- 折叠行右侧**常驻**摘要 `advancedSummary`（折叠、展开都显示，行高不跳）：
  有值时「已设置：分支、鉴权、拉取后钩子…」，顺序同高级区字段顺序，只列与新建默认值不同的项
  （字符串 trim 后非空、三态不是 `inherit`、完整检出为开；**保存目录不算**——一键识别总会填它，算进去摘要就几乎永不为空）；
  只对 git 仓库显示的字段在单文件订阅下不列；都没设置时按类型概述可设项。
  一键识别写进折叠区的值（分支、拉取后钩子、自动建任务=强制开启）必须能从摘要里看到。
- 保存失败时若后端 400 点名折叠区字段，先 `showAdvanced = true` 再弹原文（`revealAdvancedForError`）：
  按关键字「鉴权 / 访问令牌 / SSH 密钥 / 分支 / 子目录 / 保存目录 / 别名」匹配，
  但**以「白名单 / 黑名单 / 依赖规则」开头的正则报错先排除**——它们原样带出用户写的片段，片段里可能恰好含「分支」「子目录」。
  前端自己只校验名称和 URL，两者都在基本区。

### 4. Validation & Error Matrix

- 「?」写成 label 里的 `<span>` / 裸 `el-icon` -> 点它焦点跳进输入框（手机弹键盘）、开关被拨动
- `<button>` 漏写 `type="button"` -> 点「?」直接提交表单
- `trigger` 含 hover -> 触屏点不开；桌面移入即开、点击反而关
- 删掉 Esc 捕获监听 -> 按 Esc 把整个弹窗连同没保存的输入一起关掉，teleport 到 body 的气泡飘在列表页上直到下次点击
- 高级区用 `v-if` / `showAdvanced` 没复位 -> 折叠时字段卸载 / 打开新建弹窗沿用上次的展开状态
- 摘要漏列某个字段或只在展开时显示 -> 一键识别写进去的值藏在折叠区里看不见
- `revealAdvancedForError` 不先排除正则报错 -> 白名单正则写错，高级区反而被展开

### 5. Good/Base/Bad Cases

- Good：新字段有坑要解释 -> 标签换成 `<DdFieldHelp label="…">一两句</DdFieldHelp>`，完整口径补进 README / `apiData.ts`
- Base：字段不需要解释 -> 普通 `label="…"`，不硬加气泡
- Bad：控件下方加 `form-hint` 长段落；把长段落整段塞进气泡；为了容纳说明把字段改成跨两列

### 6. Tests Required（仓库无前端测试，浏览器实测）

- 桌面 1280 与手机 375：点「?」开、点外面关、再点一次也关；焦点落在按钮上、不进输入框；点「完整检出」的「?」开关不被拨动；表单不提交
- 键盘 Tab 到「?」：Enter、Space 各按一次只切换一次（Chrome 与 Firefox）
- 气泡开着按 Esc：只关气泡、弹窗还在；再按 Esc 关弹窗，列表页上不残留气泡
- 新建与编辑每次打开高级区都是折叠；一键识别 `ql repo https://github.com/a/b.git "jd_" "" "utils" "main"` 加第二行 `cp a b` 后摘要为「已设置：分支、自动建任务、拉取后钩子」
- SSH 鉴权不选密钥、高级区折叠时保存 -> 自动展开并弹原文；白名单写 `(abc` 保存 -> 高级区保持折叠
- 明暗 × 直角 / 圆角四种组合下看一遍气泡；375px 下气泡不出界、正文不从词中间断开

### 7. Wrong vs Correct

#### Wrong
```vue
<el-form-item label="白名单">
  <el-input v-model="editForm.whitelist" />
  <div class="form-hint">只有命中白名单的文件才会建任务；多个用 , 或 | 分隔……（再写三行条件）</div>
</el-form-item>
```

```vue
<!-- span 在 <label for> 里：点它焦点跳进输入框；hover 在触屏上开关不稳 -->
<el-popover trigger="hover"><template #reference><span class="help">?</span></template>…</el-popover>
```

#### Correct
```vue
<el-form-item>
  <template #label>
    <DdFieldHelp label="白名单">命中的文件会拉取，并按「自动建任务」设置建任务，留空为全部。</DdFieldHelp>
  </template>
  <el-input v-model="editForm.whitelist" placeholder="如 jd_|jx_，留空为全部" />
</el-form-item>
```

---

## Scenario: 日志窗口的实时流状态机（`done` / `waitingForLog` / 换代）

> v3.2.8 把「打开排队中的任务只看到上一次运行的日志」「重连退避期间头部闪已完成」一并收口时定的口径。

### 1. Scope / Trigger

- 触发：修改 `web/src/views/tasks/components/LogViewer.vue` 的 `startStream` / `onEvent` / `onError` / `recoverStreamAfterError` / `pollPendingTaskUntilRunning` / `waitQueuedTaskThenPoll` / `handleVisibilityChange`，或改服务端 `done` 事件载荷时必须看本节。

### 2. Contracts

- `done` 事件有三种载荷，**语义完全不同**，不能合并处理：
  - `finished`：服务端 0 等待，任务早就结束、日志已落库 → 可以复用并行预取的 latest-log。
  - `finished-late`：服务端在短轮询里**真的等过** → 打开弹窗那一刻任务还在排队/启动，此刻按 `started_at` 取到的很可能是**上一次运行**的记录，必须作废预取并进等待链。
  - `reconnect`：服务端主动断流要求重连 → **退避期间不得置 `done`**，否则头部会闪「已完成」（「点运行后立刻打开日志」每次都会碰到）。只有熔断跳闸、真正按完成收口时才置 `done=true`。
- 进入等待链前，若正文还是空的（`!hasLogs`），必须**同步**把 `waitingForLog` 置真再 `await` 查状态：那一个 live-logs 往返里头部否则会是「已完成」。正文已有内容时不动（快任务推过历史，「已完成」是对的）。
- `headerState` / 底栏 / 空态三处必须自洽：`waitingForLog=true` 时分别是「等待中」/「等待日志中」/「等待日志输出…」，不得出现「无日志」这类与状态矛盾的字。
- **实时流出错后按状态分流**：运行中（2）才退避重连；排队中（0.5）且流里没有正文 → 与 `finished-late` 同路进等待链（直接从「排队中」那一步进，不要再绕一次 live-logs，那一个往返又会闪「已完成」）；其余按完成收口。
- 任何 `await` 之后都要复查 `disposed` / `visible` / `taskId` / **`streamGeneration`**：用户在一个往返内关掉再打开同一任务时，旧链会对新会话开流或写排队文案。`streamGeneration` 只在 `cleanup()` 里自增，正常等待链中途不会换代。
- `done=true` 且正文为空时，切回前台才允许补拉 latest-log；等待链进行中（`waitingForLog=true`）**必须不补拉**，否则会把上一次运行的日志渲染成本次结果。

### 3. Tests Required（浏览器实测，演示站的假流只发 `finished`，两条路径都到不了）

- 打开排队超过 1.5 秒的任务日志：头部「运行中」→「等待中」，全程不出现「已完成」；正文从「等待日志输出…」变「任务排队中…」。
- 就在那一个往返里切后台再回来：不得出现 latest-log 请求，正文不得闪出上一次运行的日志。
- 快任务（有输出）：仍显示「已完成」，不得出现「等待中」。
- 等待中关窗再开同一任务：只应新开 1 条 `/stream`，不得出现两条并行的每秒 live-logs。
- `done:reconnect` 退避期间：头部保持绿色「运行中」，底栏「实时采集中」。

---

## Scenario: 升级后的旧前端壳与「未保存内容」（issue #126）

### 1. Scope / Trigger

- 触发：修改 `web/src/utils/chunkReload.ts`、`web/src/router/index.ts` 的 `onError`、`main.ts` 的 `vite:preloadError`、`MainLayout.vue` 的版本自检时必须看本节。

### 2. Contracts

- 自动刷新一次的三个调用方共用 `reloadOnce()`：`vite:preloadError`（任何动态 import 失败）、`router.onError`（切页 chunk 失败，刷新后落到目标页）、版本自检（后端版本比前端包新）。
- **限次是硬约束**：60 秒内只自刷一次，时间戳记在 `sessionStorage`；读写不了时**宁可不刷**（记不住「刚刷过」就可能死循环）。
- **页面上有未保存内容时不自动刷新**，改为一条带「刷新」按钮的提示：自动刷新会被任何一次 chunk 失败触发（含菜单预加载、编辑器预热），用户根本没点过刷新，内容却会丢。
- **调用方不能自己猜 `reloadOnce` 为什么返回 false**。`wasLastReloadDeferred()` 只在「因未保存内容而暂缓」时为真，且在 `reloadOnce` 开头统一清零；路由据此决定要不要再弹红色「页面文件加载失败」。用 `hasUnsavedWork() && navigator.onLine !== false` 近似判断会在「有未保存内容 + 60 秒限次内」时一条提示都不弹（点了菜单没反应）。
- 提示的 8 秒去重窗口在用户手动关闭提示时复位，否则关掉之后 8 秒内再失败会静默无提示。
- 版本自检只认「后端更新」，不认「不一致」：反过来是部署本身的版本号没写对（自建镜像默认值、Magisk 本地打包默认值），按不一致比会每开一个标签页白刷一次。

### 3. Tests Required（浏览器实测）

- 在线 + 有未保存内容 + 限次外：只出一条黄色「面板已更新，保存后刷新页面即可」，不出红色。
- 在线 + 有未保存内容 + 限次内：出红色提示（不能一条都没有）。
- 关掉黄色提示后 8 秒内再失败：重新弹一条黄色。
- 离线 + 有未保存内容：只出红色。
- 无未保存内容 + 限次外：自动刷新并落到目标页。

---

## Scenario: 编辑器的行尾规范化（CodeMirror 6）

- CodeMirror 6 按 `/\r\n?|\n/` 切行、`doc.toString()` 一律用 `\n` 拼回。载入 CRLF 内容后，如果用「文档内容 !== `props.modelValue`」判断要不要 `emit`，**一打开 CRLF 文件就会被判成「未保存」**（进而触发离开确认、挡住自动刷新）。
- 口径：`updateListener` 判等时把 `props.modelValue` 的 `\r\n?` 规范成 `\n` 再比；`replaceDoc` 的判等**故意不规范化**——外部值是 CRLF 时一定整篇替换，切文件、放弃改动都不会被漏掉。
- Monaco 保留 CRLF，没有这个问题；两个引擎的「保存后文件行尾」本来就不同，不要为了统一去改保存行为。
- 凡是按行尾切分文本的地方一律用 `split(/\r\n|\n|\r/)`（脚本页行数、配置文件页行数），`split('\n')` 会让纯 CR 文件恒显示 1 行。

---

## Scenario: 脚本目录树定位（`ScriptsSidebar` reveal，issue #136）

> v3.2.9 定的口径。下面的约束来自 EP 2.13.5 `el-tree` 源码核对、Node 探针与浏览器实测（Vue 3.5.30），
> 标 🔴 的几条是实现后在浏览器里真的复现过、再修掉的。

### 1. Scope / Trigger

- 触发：修改 `web/src/views/scripts/components/ScriptsSidebar.vue` 的定位逻辑（`setHighlight` / `revealInTree` / 三条侦听 /
  `handleTreeNodeClick` / 滚动）、`useScriptWorkspaceBrowser.ts` 的 `openFile` / `treeRevealTicket` / `normalizeScriptPath`，
  或在别处写「让 el-tree 定位到某个节点」时必须看本节。

### 2. Signatures

- `ScriptsSidebar` 新 props：`currentFile: string`（与树节点 key 同口径）、`revealTicket: number`、
  `sidebarCollapsed?: boolean`（`scripts/index.vue` 传按布局修正过的值，≤1024 恒为 `false`）。
- `useScriptWorkspaceBrowser()` 新导出 `treeRevealTicket: Ref<number>`；`openFile(path)` 入口先 `normalizeScriptPath(path)`。
- 侧栏内部：`setHighlight(key, expandParents)`、`revealInTree(path)`、`scrollRowIntoView(path, smooth)`、`findScrollContainer(from)`。
- 平滑与否只问 `utils/panelAppearance.ts` 的 `shouldReduceMotion()`（v3.2.9 从登录页抽出来的公共函数）。

### 3. Contracts

**触发信号**
- `treeRevealTicket` 在 `openFile` 成功时 +1，**包括「同一文件再次进入」的早返回**。
  刻意不侦听 `selectedFile`：同一文件从任务页再点一次它不变，却要重新定位；
  而 `openFile` 是「先改 `selectedFile`、加载失败再回滚」，侦听它会先按打不开的路径展开一遍祖先。
- 加载失败回滚时，**只有加载期间树重建过**（`fileTree.value !== treeBeforeLoad`）才 +1：
  树没重建时补定位会把用户刚收起的目录重新展开、把正在浏览的滚动位置拽走（高亮已由下面的 `expandParents=false` 路径拉回）；
  树重建过时不补，原文件高亮了却藏在全收起的新树里。
  🔴 这个判断依赖 `loadTree` 每次**整体赋新数组**，改成原地更新会让它静默失效。
- `?file=` 的值可能是 `./jd/x.py`、`jd\x.py`：`openFile` 按后端 `normalizeScriptRelativePath`（`server/handler/script.go`）口径规范化——
  `\` 转 `/`、每段 trim、去空段与 `.` 段；以 `/` 开头或含 `..` 的原样交给后端拒绝。
  不规范化时 `selectedFile` 与树 key 对不上，定位、以及删除 / 重命名里「是不是当前文件」的比较一起静默判错。

**EP `el-tree` 的四条行为**
- `setCurrentKey(key)` 找不到 key 时**不清旧高亮** ⇒ `setHighlight` 一律先 `getNode(key)`，找不到就显式 `setCurrentKey(null)`。
- `setCurrentKey(key, true)` 会展开全部祖先，**包括用户刚手动收起的那一层** ⇒ 只有「打开文件 / 树刷新」传 `true`；
  把高亮拉回当前文件（点目录、点文件后的 `finally`、`currentFile` 变化）**必须传 `false`**，否则当前文件所在目录永远收不起来。
- 每次 `setData`（即每次 `loadTree`）都丢掉展开与过滤状态 ⇒ 树刷新后若搜索框有词，先补 `filter(keyword)` 再定位。
- EP 在 emit `node-click` 之前就已把高亮挪到点中的节点 ⇒ 点目录、「未保存修改」选取消、加载失败后都要把高亮拉回当前文件。

**🔴 `fileTree` 侦听里必须先 `await nextTick()` 再展开**
- 侦听已是 `flush: 'post'`，仍要先 `await nextTick()`。机制：`setData` 换了新节点，按 key 复用的 tree-node 组件看到
  `node.expanded` 由 true 变 false，登记一个 `nextTick(expanded=false)`，挂在本轮 flush 的 promise 上；
  Vue 3.5 的 `flushJobs` 在递归那一轮之前把 `currentFlushPromise` 置空，此时同步展开登记的 `nextTick(expanded=true)`
  挂在已就绪的 `resolvedPromise` 上**反而先跑** ⇒ `Node.expanded=true`、组件 `expanded=false`：
  目录看着收起、点它也展不开（EP 按组件的值判断，只会重复 expand），一直卡到下次树刷新。
  重命名当前文件、把它拖进已展开的目录时最容易撞上。升级 Vue 后要回归这一条。

**滚动**
- 只在**最近的可滚动祖先**里算位置 `scrollTo`（nearest 语义：完整可见不动，上沿出界对齐顶部，下沿出界对齐底部），
  **不用 `scrollIntoView`**：它会连带滚动所有能滚的祖先，缩进很深时还可能横向滚。
- 可滚动祖先由 `findScrollContainer` 从行往上找第一个 `overflow-y: auto|scroll` 且 `scrollHeight > clientHeight` 的元素，
  找到一个就停：≥769 是 `.sidebar-tree`；**≤768 是外层 `.layout-main`**（`.dd-fixed-page` 只在 ≥769 定高，
  手机上侧栏随内容撑高、`.sidebar-tree` 根本不滚）。可见区取容器与 `window.innerHeight` 的交集
  （不能假定外壳恰好等于视口。v3.3.3 之前演示站横幅没让出高度，`.layout-main` 底边落在窗口外 34px；
  `banner.ts` 的补丁加上 `!important` 后已修，交集照留，代码不用动）。
- `behavior: smooth && !shouldReduceMotion() ? 'smooth' : 'auto'`：`global.scss` 的减少动效只压得住 CSS `scroll-behavior`，
  管不到 JS 显式传的 `smooth`。侧栏「重新可见」那次固定瞬时滚动（卡片刚播完动画，再接平滑滚动显得拖沓）。
- 量位置前等布局落定：`nextTick` + 一个宏任务；再每 16ms 查树里还有没有 `.el-collapse-transition-enter-active / -leave-active`
  （上限 400ms）。不用 `transitionend`：EP 这条过渡声明了 max-height 与上下 padding 三个属性、实际只变 max-height，Vue 等不齐。
  侧栏从看不见变可见时，再等侧栏卡自身 `getAnimations()` 跑完（宽度过渡 / 入场关键帧，同样上限 400ms）。
- 不滚、只高亮的情况：侧栏看不见（≤1024 切到了编辑器、桌面端收起目录树），交给「重新可见」侦听补滚；
  `offsetParent === null`（v-show 藏起或 keep-alive 缓存、DOM 脱离文档）；
  **搜索过滤把目标或它的祖先藏掉时只高亮不滚动，也不替用户清搜索词**。
- 每次定位 / 补滚动占一个序号（`revealSeq`），任何 `await` 回来发现号被后来者占了、或 `currentFile` 已变，就放弃。
- 表头「定位当前文件」按钮 v3.2.9 实测放不下（300px 侧栏的工具条只剩约 10px 余量，加上后「脚本文件」折成两行、
  新建按钮的 caret 掉到第二行），已按「放不下就不加」撤掉，模板里留了注释；别直接加回去。

### 4. Validation & Error Matrix

- 直接 `setCurrentKey(key)` 不先 `getNode` -> 当前文件被删 / 改名后，旧高亮留在不相干的节点上
- 把高亮拉回当前文件时传 `expandParents=true` -> 当前文件所在目录一收起就被弹开
- `fileTree` 侦听去掉 `await nextTick()` -> 重命名 / 拖拽后目录「看着收起、点不开」，直到下次刷新，构建与 vue-tsc 全绿
- 用 `scrollIntoView` -> 整页 / 布局容器跟着跳
- 只滚 `.sidebar-tree` -> ≤768 永远滚不到，从编辑器返回列表时当前行停在窗口外
- 加载失败回滚时无条件 +1 ticket -> 点开一个磁盘上已删的文件失败，用户刚收起的目录被重新展开、滚动位置被拽走
- 回滚时从不 +1 -> 缓存页从任务页打开脚本已删的任务（恰逢 `loadTree`），原文件高亮了却藏在收起的目录里
- 写死 `behavior: 'smooth'` -> 「减少动效」档下仍在平滑滚动

### 5. Good/Base/Bad Cases

- Good：任务页点脚本名进入（首次与 keep-alive 缓存后两种）-> 祖先展开、高亮、滚到可见；同一文件再点一次也重新定位
- Base：搜索过滤中进入 -> 只高亮，不滚动、不清词；侧栏看不见时只高亮，重新可见后瞬时补滚
- Bad：侦听 `currentFile` 就展开 + 滚动 -> 按打不开的路径展开祖先；每次都清搜索词替用户「定位」

### 6. Tests Required（仓库无前端测试，浏览器实测；演示站只有一层目录，要先手动建多层目录与足够多的文件）

- 1920 宽：任务页点深层脚本名（首次进入、离开再回来两种）-> 各级祖先展开、高亮、行在可见区
- 打开 `a/b/c/x.py` 后重命名它（或拖进一个已展开的目录）-> `a/b/c` 显示为展开、行可见；点 `a/b/c` 能收起、再点能展开
- 点目录后高亮仍在当前文件；手动收起当前文件的父目录不会被强行展开
- 收起当前文件的目录、往下滚并展开别的目录，点一个磁盘上已删、树没刷新的文件 -> 报「脚本不存在」，收起的目录保持收起、滚动位置不变、高亮回到原文件
- 缓存页从任务页打开「脚本已删」的任务 -> 回到脚本页原文件仍展开、高亮、可见
- 375×812：打开深层文件 -> 返回列表，当前行在窗口内（滚的是 `.layout-main`）；900 宽滚的是 `.sidebar-tree`、页面本身不动
- 桌面端收起目录树 -> 从任务页打开深层文件 -> 展开目录树，宽度动画结束后瞬时滚到当前文件
- 个人设置「界面动效」切「始终开启」时平滑滚动；「减少动效」或「跟随系统」且系统开着减少动态效果时瞬时跳（量之前先确认本机的系统设置）

### 7. Wrong vs Correct

#### Wrong
```ts
watch(() => props.fileTree, () => {
  treeRef.value?.setCurrentKey(props.currentFile, true)   // 同一轮 flush 里展开：组件 expanded 与 Node.expanded 失步
  document.querySelector('.is-current')?.scrollIntoView({ behavior: 'smooth' })   // 连带滚整页，且无视减少动效
}, { flush: 'post' })
```

#### Correct
```ts
watch(() => props.fileTree, async () => {
  await nextTick()   // 先让本轮 flush 整个跑完
  if (searchKeyword.value) treeRef.value?.filter(searchKeyword.value)
  // getNode 判存在 -> setCurrentKey(path, true) -> 等动画落定 -> 在最近的可滚动祖先里 scrollTo
  void revealInTree(props.currentFile)
}, { flush: 'post' })
```

---

## Scenario: 列表页表头样式统一用令牌

- 8 个列表页（环境变量、依赖、日志、开放接口、通知、任务、用户、订阅）的 `:header-cell-style` 取值必须是 `var(--el-fill-color-light)` / `var(--el-text-color-regular)`，不得写死 `#f8fafc` / `#64748b`：写死值在暗色下要靠 `!important` 补丁盖回来，而内联样式只有 `!important` 压得住，漏一页就是一块浅色表头。
- `global.scss` 里那条 `html.dark` 表头补丁取的是同一对令牌，现在是兜底（防以后又有人写死浅色内联样式）。

---

## Scenario: 列表页偏好（`utils/listPreferences.ts`，v3.3.1，issue #143）

### 1. Scope / Trigger

- 触发：改 `web/src/utils/listPreferences.ts`；改任务页、环境变量页的每页条数；改任务页视图栏「全部」「顶栏分组页签」的显隐
  （v3.3.5 前视图管理里这一行叫「分组标签」，与「显示设置」里管任务名旁分组小标签的开关同名，#157 起改名区分）、改 `ViewManagementDialog.vue` 的保存；
  或者要往服务端的 `list` 白名单里加键时，必须看本节。
- 背景：这 4 项原来分散在 `tasks/index.vue`、`envs/index.vue`、`ViewManager.vue` 里，各自直接读写 localStorage。
  localStorage 按 origin 隔离，issue 的报告者正是多域名 / 多 IP 在用，换个地址打开就全没了。v3.3.1 起跟随账户。

### 2. Signatures

```ts
export interface ListPreferences {
  tasks_page_size: 10 | 20 | 50 | 100
  envs_page_size: '20' | '50' | '100' | 'all'
  tasks_view_all_hidden: boolean
  tasks_view_groups_hidden: boolean
  log_open_at_bottom: boolean   // v3.3.3，#147：打开已结束的日志时是否定位到底部
}
export const TASKS_PAGE_SIZE_OPTIONS    // [10, 20, 50, 100]
export const ENVS_PAGE_SIZE_OPTIONS     // ['20', '50', '100', 'all']
export const LIST_PREFERENCES_DEFAULTS  // { tasks_page_size: 20, envs_page_size: '20', 两个 hidden: false, log_open_at_bottom: false }，Object.freeze
export const LIST_PREFERENCE_KEYS       // 五个键，供遍历
export function parseListPreferenceWire(key, raw)                  // 校验「接口上的值」，口径与服务端白名单一致（类型不对也算非法）
export function readListPreference(key): ListPreferences[K]        // 同步读本地缓存
export function setListPreferences(patch: Partial<ListPreferences>): void  // 一次改多项，只发一次 PUT；写本机后发出去不管
export function setListPreference(key, value): void                // 等于只带这一个键的 setListPreferences
export async function saveListPreferences(patch: Partial<ListPreferences>): Promise<void>  // v3.3.5：等 PUT 结果，失败回滚本机键并抛错
export function ensureListPreferencesLoaded(): Promise<void>       // 记忆化
export function resetListPreferencesCache(): void
```

- `api/auth.ts`：`getPreferences()` 的返回类型多了 `list?: Record<string, unknown>`；新增 `updateListPreferences(list)`，
  发 `PUT /auth/preferences`，body 只有 `{ list }`。editor 那条 `updatePreferences(editor)` 不动。
- 消费方：`tasks/index.vue`（`tasks_page_size`）、`tasks/components/ViewManager.vue`（读两个 hidden 并应用到顶栏）、
  `tasks/components/ViewManagementDialog.vue`（保存两个 hidden，走 `saveListPreferences`）、`envs/index.vue`（`envs_page_size`）；
  `log_open_at_bottom` 的消费方见下面「打开已结束的日志」；
  `stores/auth.ts` 的 `clearAuth` 调 `resetListPreferencesCache()`。
- 组名还叫 `list`，但 v3.3.3 起它装的是「列表页 / 日志查看等跟账户走的零散界面开关」。以后再有这类开关照样往里加键，
  **不要**按组名字面另开一组（同一类偏好会出现两处真源）。
- 服务端契约（按组写入、稀疏白名单、400 文案、写锁）：backend `quality-guidelines.md`「场景：用户界面偏好按组写入」。

### 3. Contracts

**存储模型与 `editorPreferences.ts` 同构**：localStorage 是首屏 / 离线 / 隐私模式下的同步缓存（页面 setup 时必须立刻拿到条数，不能等网络），
服务端 `list` 组是真源。不要另发明一套。与 editor 刻意不同的只有两点：

1. **稀疏存储、逐键迁移**，没有 editor 那种组级 `stored`：服务端有这个键就下行，没有这个键、本机老键里真有值才上行，精确到每一个键。
   组级 `stored` 在多 origin 下会出事：第一个被打开的 origin 哪怕从没改过设置，也会把默认值「占坑」写上去，其它 origin 的自定义值随后被全部冲掉。
2. **不派发变更事件**：列表页的消费方与个人设置页都在挂载时调 `ensure`，等它 resolve 之后自己重读一遍并应用；
   `log_open_at_bottom` 的消费方都是「打开日志那一刻」同步读缓存，同样用不着事件。

**本地缓存**
- **沿用老键和老格式**：`dd:tasks:page_size`（数字字符串）、`daidai-env-page-size`（`'20'|'50'|'100'|'all'`）、
  `dd:tasks:view_all_hidden` / `dd:tasks:view_groups_hidden`（`'1'` / `'0'`）。老值天然就是上行迁移的数据源；回退到 v3.3.0 时老版本照样读得懂。
- 读写都包 try/catch，外加一层内存覆盖（`memoryOverrides`）：隐私模式下 `setItem` 抛错时，本次改动仍然生效。
- `readListPreference(key)`：本地有合法值就用它，否则取 `LIST_PREFERENCES_DEFAULTS`。
  默认值只有前端这一份（服务端不存默认值），与改造前三个页面各自的回落值逐字相同。

**`ensureListPreferencesLoaded()`**
- 记忆化，失败也不把 promise 置回 null（未登录、离线这类失败是稳定的，同 editor）；刻意不放进 `main.ts` 的 bootstrap，不给其它页面加请求。
- 🔴 响应里的 `list` 不是对象（v3.3.0 及以前的老服务端）→ 直接 return，**一个请求都不上行**。
  老服务端的 PUT 不认识 `list`，会把整套 editor 默认值写进库、把 editor 的 `stored` 翻成 `true`，下次打开编辑器时本机的编辑器偏好被默认值冲掉。
- 服务端有合法值 → 写回本地缓存；但本次会话里用户亲手改过的键（`locallyChanged`）跳过：那条 GET 是在他改之前发出的，带回来的是旧值。
- 服务端没有这个键、本机老键里**真的有**合法值 → 放进迁移补丁。「真的有」指 `parseStored` 解析出了值，不是回落出来的默认值；
  开关存的 `'0'` 也算真的有（用户在视图管理里保存过「显示」）。
- 补丁非空才发，且只发**一次** PUT，不等它回来就 resolve（迁上去的就是本机当前值，本地什么都不用变）。
  本机一个老键都没有时，不发任何请求，避免给服务端「占坑」写默认值。

**写入**
- 🔴 **所有写入必须经 `setListPreference(s)` 或 `saveListPreferences`**。验收 grep：
  `dd:tasks:page_size|daidai-env-page-size|dd:tasks:view_(all|groups)_hidden|dd:log:open_at_bottom` 在 `web/src` 里只允许出现在 `listPreferences.ts`（v3.3.1 时零违例）。
  绕过它直接写 localStorage 的后果：改动只留在本机，下次加载被服务端的旧值静默改回去，不报错、构建全绿。
- 两种写法，按「失败要不要让用户知道」选：
  - `setListPreferences(patch)`：**写本机 + 发出去不管**，给即改即生效的开关用（每页条数、个人设置「日志查看」）。
    同步失败对它们只是「换个浏览器不记得」，不值得打断用户。
  - `saveListPreferences(patch)`（v3.3.5，#157 ③）：同一套校验与本机写入，但**等服务端写入结果**，给「点了保存、要提示已保存」的地方用（目前只有视图管理弹窗）。
- `setListPreferences(patch)` 的顺序：按白名单键遍历 → `parseWire` 逐键校验（非法或缺失的那一键单独丢弃，其余照发）→ 记脏 → 写本地缓存 →
  后台发**一次** `updateListPreferences(accepted)`；一个合法键都没有就不发；同步失败静默（本机已经生效了）。
  按白名单遍历而不是遍历 patch：白名单外的键天然被忽略；非法值要是放行，服务端会对整个请求回 400，同一批里合法的键也跟着丢。
- `saveListPreferences(patch)` 的顺序：前四步同上，并记下每个被写键**写之前的本机原值**（没存过记 null）与它原来在不在脏集合里 →
  `await updateListPreferences(accepted)`。成功才 resolve；失败时逐键回滚（原来没存过的删掉本机键与内存覆盖，读的时候回落默认值；存过的写回原值），
  原来不在脏集合里的键移出 `locallyChanged`（还在路上的那次 ensure 回来时要允许它把服务端值写回本机），再把错误**原样抛给调用方**，由它报错、不关弹窗。
  一个合法键都没有时直接 resolve、不发请求。
- 同一个动作要改好几项时合成一个 patch 交给上面两个函数之一，不要连调几次 `setListPreference`（请求数白白翻倍）。
  视图管理保存时 `ViewManagementDialog.handleSave` 就是把两个开关合进一个 patch。这不是为了防丢键：服务端的 `preferenceWriteMu`
  已经把「读 - 合并 - 写」串行化，改不同键的并发 PUT 都会落库；两个 PUT 改**同一个**键时，以服务端后处理的那个为准。
- 🔴 **只在用户动作里 set，程序化回写不 set**。当前调用点：tasks 与 envs 的 `handlePageSizeChange`、个人设置「日志查看」卡（这三处 `setListPreference`）；
  视图管理弹窗的 `handleSave`（`saveListPreferences`，只在值真的变了时）。`ViewManager.handleManagementSaved` 只负责把弹窗回传的结果应用到顶栏，**不再写偏好**。
  不要挂进 `watch(pageSize)`：应用服务端值时 watch 也会触发，多发一次 PUT，还会把这一键误记成「本地改过」，挡住后面的下行同步。
  视图管理什么都没改就点保存时不发偏好请求：服务端是稀疏存储，写一次就等于替用户占坑。

**视图管理的保存要等写入结果（v3.3.5，#157 ③）**
- 为什么：原来 `ViewManager.handleManagementSaved` 用 `setListPreferences` 发 PUT 不管结果，弹窗却先提示了「视图设置已保存」并当场隐藏页签；
  PUT 一旦失败，界面是假成功，刷新后 `ensureListPreferencesLoaded` 拿服务端旧值把本机缓存冲回去，用户关掉的页签又出现了（「关了刷新又出现」）。
- `ViewManagementDialog.handleSave` 的顺序：有自定义视图时先 `taskViewApi.reorder(...)` → 两个开关里**真的变了**的键合成 patch → `await saveListPreferences(patch)`
  → 都成功才 `ElMessage.success('视图设置已保存')`、`emit('saved', allHidden, groupsHidden)` 并关窗；父组件 `ViewManager.handleManagementSaved` 应用到顶栏再 `loadViews()`。
- 失败：报错（有服务端文案用文案，否则「保存失败，请检查网络后重试」，不透出 axios 的英文 `err.message`）、**弹窗不关**、本机两个键已由 `saveListPreferences` 回滚。
  视图那一半（reorder）已经成功、只是偏好没写成时 `emit('views-saved')`，父组件 `@views-saved="loadViews"` 按服务端刷新视图列表，弹窗里改好的开关留着等用户重试。
- 有未保存改动（视图顺序、任一视图显隐、两个内置开关与打开时不同）时，×、Esc（`before-close`）、「取消」（手机全屏弹窗唯一的关窗入口）、「编辑」先确认「放弃未保存的修改？」；
  保存成功是直接 emit 关窗、不经过确认，确认框挡不住正常保存。
- 分页器给的是 number：写之前按白名单取一遍（`TASKS_PAGE_SIZE_OPTIONS.find(option => option === pageSize.value)`），不要用 `as` 断言。
  `:page-sizes` / 条数下拉的选项也由两个 OPTIONS 常量生成，不在页面里再抄一份。

**首拉时机：两页刻意不同**
- **tasks 不阻塞首拉**：挂载时先按本机缓存拉一次，同时 `ensureListPreferencesLoaded().then(...)`；服务端值与当前不同才回到第 1 页重拉（`applyAccountPageSize`）。
  两次请求谁先回来都没关系，`loadTasksSeq` 保证最后停在最新一次上。
- **envs 先 `await ensureListPreferencesLoaded()` 再发首个列表请求**。`loadData` 现在也有请求序号闸（`loadDataSeq`），理论上也能照任务页那样写；
  仍然选择先等，是因为两边不一致时（换浏览器、换域名后第一次打开）先拉的那次注定作废：白发一个列表请求（本地是「全部」档时就是整表），
  屏幕上还会先闪一版按本地条数排的行与翻页器，再整页换掉。代价是首拉多等一个 GET 的往返；ensure 是记忆化的，同一会话先去过任务页就零等待。
  等待期间用户点状态标签、搜索触发的 `loadData`，会被随后那次更新的请求盖过（还在飞的由序号闸丢弃）。
- ViewManager：挂载时 ensure 之后刷新两个 ref，再调 `applyViewFallback()`（服务端存的是「隐藏全部」时，要把高亮落到第一个可见视图）。
- `onActivated` 里不用再调 ensure：它是记忆化的，同一会话只拉一次。

**退出登录**
- 🔴 `clearAuth` 必须调 `resetListPreferencesCache()`（已挂上）：清掉「已经拉过」的记忆和本会话的脏键集合；
  不清 localStorage 里的值，它同时是本机的离线缓存，下次登录会被服务端值覆盖。
- 已知且接受的边界：共用一个浏览器时，上一个账号留在本机的老值，会在下一个账号的服务端没有该键时被迁上去。影响只是观感，PRD 接受。

**打开已结束的日志（v3.3.3，#147，键 `log_open_at_bottom`）**
- 默认 `false`：保持 v3.2.8（#133）起「打开已结束的日志停在顶部」。APP 在账户没设过时按它自己的现状（底部）走，两端默认不同是刻意的，别「对齐」。
- 本地缓存键 `dd:log:open_at_bottom`。🔴 **绝不能**复用老的 `dd:tasks:log_follow`：`LogViewer.vue` 每次 setup 都会删它。
- 生效点（运行中的日志一律自动跟随，不看这一项）：
  - 执行日志页「查看」已结束记录：`end()` → 正文写进去 → `detailFollow.revealFinished()`。`revealFinished()` 是 `useLogAutoFollow` 的一次性方法：
    开关开着就 `scheduleStickToBottom()` 一次，**不改 `live` / `following`、不进入跟随**。
  - 定时任务「日志」：不走 `begin(false)`（那一刻正文还空），而是 `fetchLatestLog` 正文分支的条件
    `scrollMode === 'bottom' || (!runStateDecided && 开关)`。它依赖一条隐式不变式：`runStateDecided` 为 false 时，走到这里之前正文一定是空的
    （实时数据都先经 `markRunning`；静态正文每个会话只写这一次，切回前台补拉只在正文为空时才发），所以不会把正在读的人拽走。
    以后在实时会话里绕过 `markRunning` 渲染正文、或给已渲染的静态正文加刷新，就会打破它。
  - 两处「日志文件」查看（`LogFileBrowser.vue` 的右侧 `<pre>`、执行日志页的日志文件弹窗）：内容写入后 `nextTick` 里按开关贴底。
    执行日志页那个弹窗的正文不自己滚，滚动的是它的父元素 `.el-dialog__body`。
  - 依赖、订阅的日志不接。
- 执行日志页、个人设置页挂载时都 `ensure`；任务页本来就 ensure，`LogViewer` / `LogFileBrowser` 同步读缓存即可。
- 个人设置「日志查看」卡**点击就写**：点的是已选中的那一档也 `setListPreference`；仍不挂 watch（理由同上面的「只在用户动作里 set」）。
  这与 ViewManager「什么都没改就不发」刻意不同：账户没设过时网页按 `false` 显示「停在开头」、APP 按它自己的默认值在底部，
  点已选中的「停在开头」要是不写，APP 会一直停在底部，用户只能先切到底部再切回来。
  点击是用户的明确意图，不算「占坑」；「占坑」只针对挂载 / watch / ensure 这类没人动过也写默认值的程序化写入。

**白名单要三处一起改**：`listPreferences.ts` 的类型与两个 OPTIONS、`server/handler/user_preference.go` 的
`listTasksPageSizeValues` / `listEnvsPageSizeValues` 与 `listPreferences` 结构体。演示站 mock（`demo/adapter.ts`）从 `listPreferences.ts` 导入，不手抄。
两边对不上的表现：多出来的档位 PUT 上去被 400、只在本机生效，换个浏览器就回到默认值。
加一个新键时的清单：前端类型 / DEFAULTS / KEYS / STORAGE_KEYS；后端结构体字段与 `empty()` / merge / decode 各一行（**漏了 `empty()`，只带新键的 PUT 会 200 静默 no-op**）；
`demo/adapter.ts` 的 `pickDemoListPreferences`（布尔键在那个循环里）；`api-docs/apiData.ts` 的 GET / PUT 条目（手写副本，没有测试守着）。

### 4. Validation & Error Matrix

- 老服务端（响应里没有 `list`）-> ensure 直接 return，不上行，本机缓存照用
- 服务端 `list` 为 `{}`、本机没有老键 -> 不发任何请求
- 服务端 `list` 为 `{}`、本机 `dd:tasks:page_size` 为 `'50'` -> 只发一次 `PUT {list:{tasks_page_size:50}}`
- 服务端有值、用户在 GET 回来之前改过同一个键 -> 跳过这一键的下行写回，用户刚选的值保留
- 本机老键是脏值（`'30'`、`'abc'`）-> 当作没存过：不迁移，读出来是默认值
- `setListPreference` 收到白名单外的值 -> 这一键丢弃，不写本地、不发请求
- 隐私模式 `setItem` 抛错 -> 走内存覆盖，本次会话生效，刷新后丢失
- 页面直接读写老键 -> 验收 grep 违例；运行时表现为下次加载被服务端值改回去，不报错
- 视图管理保存时 `PUT /auth/preferences` 失败 -> 报错、不提示「视图设置已保存」、弹窗不关；本机两个键回滚到保存前，刷新后仍是保存前的值
- 视图管理保存时 reorder 成功、偏好 PUT 失败 -> `views-saved` 让顶栏按服务端刷新视图列表，弹窗里改好的开关留着等重试

### 5. Good/Base/Bad Cases

- Good：只在用户动作里 set；一次动作改多项时合成一个 patch；首拉阻塞与否按页面有没有请求序号闸、以及「先拉那次是否注定作废」来定；
  点了「保存」要提示成功的地方用 `saveListPreferences`，等写入结果再提示
- Base：从没改过这几项的新用户 -> 服务端一行都不写，全部用前端默认值
- Bad：`watch(pageSize, v => setListPreference(...))`；ensure 里把回落出来的默认值也迁上去；页面自己 `localStorage.getItem('dd:tasks:page_size')`；
  弹窗里 `setListPreferences(patch)` 之后立刻提示「已保存」（PUT 失败时界面假成功，刷新被服务端旧值冲回）

### 6. Tests Required

- 前端没有自动化测试：`cd web && npm run build`（含 vue-tsc）+ 上面的验收 grep。
- 浏览器实测：
  - 换一个浏览器 profile 用同一账号登录：tasks 与 envs 的每页条数、视图栏两个隐藏开关都还在
  - 只改过列表偏好的账号打开编辑器页：本机的编辑器偏好不被默认值冲掉（`GET` 的 `stored` 仍为 `false`）
  - 本机有老键、服务端没有：首次加载后 Network 里只有一个 PUT，只带本机真有的键；本机没有老键：零个 PUT
  - 视图管理什么都不改点保存：零个 PUT；两个开关都改：一个 PUT 带两个键
  - 视图管理拨开关后用 CDP `Fetch.failRequest` 让 `PUT /auth/preferences` 失败：报错、弹窗不关、不提示「已保存」；刷新后页签状态与保存前一致
  - 视图管理拨了开关点 × / 取消 / Esc：先弹「放弃未保存的修改？」；什么都没改时直接关
  - DevTools 限速到 Slow 3G，进任务页后立刻改条数：GET 回来之后不会被弹回去
  - 退出登录、换一个账号：看到的是新账号自己的值
- 服务端用例：`cd server && go test ./handler -run Preferences -count=1`（清单见 backend `quality-guidelines.md`）。

### 7. Wrong vs Correct

#### Wrong

```ts
// 页面自己读写老键：改动只留在本机，下次加载被服务端旧值静默改回去
const pageSize = ref(Number(localStorage.getItem('dd:tasks:page_size')) || 20)
watch(pageSize, (v) => localStorage.setItem('dd:tasks:page_size', String(v)))
```

```ts
// 挂在 watch 上：应用服务端值时也会触发，多发一次 PUT，还把这一键误记成「本地改过」
watch(pageSize, (v) => setListPreference('tasks_page_size', v as 10 | 20 | 50 | 100))
```

```ts
// 视图管理保存：发出去不管就提示成功 —— PUT 失败时界面假成功，刷新后页签又出现（#157 ③）
setListPreferences(patch)
ElMessage.success('视图设置已保存')
emit('saved', allTabHidden.value, groupTabsHidden.value)
```

#### Correct

```ts
const pageSize = ref<number>(readListPreference('tasks_page_size'))

function handlePageSizeChange() {
  page.value = 1
  const size = TASKS_PAGE_SIZE_OPTIONS.find(option => option === pageSize.value)
  if (size !== undefined) setListPreference('tasks_page_size', size)  // 只在这个用户动作里写
  void reloadAndScrollToTop()
}
```

```ts
// 视图管理保存：等写入结果再提示；失败时 saveListPreferences 已回滚本机键，这里报错、弹窗不关
try {
  if (Object.keys(patch).length > 0) await saveListPreferences(patch)
  ElMessage.success('视图设置已保存')
  emit('saved', allTabHidden.value, groupTabsHidden.value)
  emit('update:modelValue', false)
} catch (err: any) {
  if (viewsSaved) emit('views-saved')
  ElMessage.error(err?.response?.data?.error || '保存失败，请检查网络后重试')
}
```

---

## Scenario: 移动端卡片的「···」菜单（`DdMoreMenu`）与菜单项的 `success`（v3.3.1，issue #143）

### 1. Scope / Trigger

- 触发：改 `web/src/components/ui/DdMoreMenu.vue`、`DdSplitButton.vue` 的 `SplitButtonItem`，
  或给移动端卡片加「···」菜单、给操作菜单加「启用」这类正向操作时，必须看本节。卡片整体结构见 `design-system.md` §4.4。

### 2. Signatures

- `DdMoreMenu`：
  - props：`items: SplitButtonItem[]`（类型从 `./DdSplitButton.vue` 导入）、`ariaLabel?: string`（默认「更多操作」）、`disabled?: boolean`（默认 `false`）；
  - emit：`command(key: string)`（EP 给的 key 统一 `String()`）；
  - 没有插槽，也没有 scoped 样式。
- `SplitButtonItem`：`key` / `label` / `icon?` / `disabled?` / `divided?` / `danger?` / **`success?`（v3.3.1 新增）** / `visible?`。

### 3. Contracts

- **渲染**：外层是 `el-dropdown`，`trigger="click"`、`placement="bottom-end"`、`popper-class="dd-split-button__popper"`，根元素带 `class="dd-mobile-card__more-wrap"`（`margin-left: auto`）。
  触发器是原生 `<button type="button" class="dd-mobile-card__more" :aria-label>`，里面放 `MoreFilled` 图标（组件内局部 import，不依赖 `main.ts` 的全局注册）：
  键盘可达，读屏能念出 `aria-label`；`type="button"` 防止放进表单时被当成提交按钮。
- 菜单项只渲染 `visible !== false` 的；`disabled` / `divided` 照传；`danger` 挂 `dd-split-button__item--danger`，`success` 挂 `dd-split-button__item--success`。
  **一项都不可见时整个不渲染**：空菜单点开只有一块空白浮层，看起来像坏了。
- **放在 `.dd-mobile-card__head` 的最后**，靠根元素的 `margin-left: auto` 贴到最右。
- **items 直接复用桌面操作列那份数组**，菜单项与配色两端才一致：
  - tasks：`taskActionItems(row, { alwaysShowDetail: true })` + `onTaskAction`。移动端卡片没有 Split Button 主体，观察者那一支的「详情」没地方承担，
    所以用 `alwaysShowDetail` 让它留在菜单里，否则观察者在手机上看不到详情；
  - subscriptions：`subActionItems` + `onSubAction`；envs：`buildEnvActionItems(row)` + `onEnvAction`；
  - deps：`depCardMenuItems(row)`，即 `depActionItems(row)` 去掉 `delete` 项（卡片末行已有「卸载」按钮），并把分隔线挂到 `force-delete` 上；
  - logs：`logCardMenuItems`（只有「日志文件」一项，所有角色可见）。
- **`success`**：标记可撤销的正向操作。目前只有任务的「启用」：`success: !switchOn`，与「禁用」的 `danger: switchOn` 对称。
  - **常态即绿字**（`--el-color-success`），hover / focus 再叠 `--el-color-success-light-9` 淡绿底，与 `danger` 的「常态红字 + 悬停淡红底」完全同构。
    v3.3.2 / issue #144 按用户反馈把 #143 D2 的「常态刻意不着色」翻了过来：原理由是怕它比同菜单的中性项抢眼一档，
    现在这一档**就是想要的效果**——照旧口径把常态色去掉，等于把本轮改动回滚。
  - 与 `danger` 互斥，同一项不要两个都设。
  - 样式在 global.scss 的 `.dd-split-button__popper .dd-split-button__item--success`：常态那条是 (0,2,0)，压得过 EP 的 `.el-dropdown-menu__item` (0,1,0)；
    hover / focus 两条是 (0,4,0)，高于 EP 的 `.el-dropdown-menu__item:not(.is-disabled):hover` (0,3,0)。两条都不需要 `!important`；
    取值只用令牌，暗色下 `--el-color-success` / `--el-color-success-light-9` 会由 EP 的 dark css-vars 换值（写死 `#67c23a` / `#f0f9eb` 在暗色下失真）。
  - `DdSplitButton` 与 `DdMoreMenu` 的模板都绑了这个 class。**页面自己手写 `el-dropdown` 渲染同一份 items 时，`--danger` / `--success` 两个 class 都要绑**
    （deps 移动端工具栏的下拉就是这样写的），漏绑的话那个菜单里「启用」既不是常态绿字、hover 也不变绿。
- 样式全部写在 global.scss（`.dd-mobile-card__more`、`.dd-split-button__popper`）：菜单浮层 teleport 到 body，scoped 命中不到。

### 4. Validation & Error Matrix

- DdMoreMenu 放在 `__head` 中间 -> 「···」不贴右，后面的元素被它的 `margin-left: auto` 推到最右
- 移动端另写一份菜单数组 -> 与桌面漂移，某一项只在一端有
- 同一项同时设 `danger` 和 `success` -> 两条规则打架（常态字色与 hover 底色都会撞）
- 手写 `el-dropdown` 只绑了 `--danger` -> 这个菜单里「启用」常态不是绿字、hover 也不变绿
- 把 `.dd-split-button__item--success` 的常态色去掉、只留 hover -> 与 `danger` 不再对称，用户看不出这是正向操作
  （v3.3.2 / issue #144 修的就是这个；#133「菜单项字色统一」只约束**中性项**，`danger` / `success` 是它明确的两个例外）

### 5. Good/Base/Bad Cases

- Good：`<DdMoreMenu :items="taskActionItems(row, { alwaysShowDetail: true })" @command="onTaskAction($event, row)" />` 放在首行最后
- Base：菜单里全是中性项 -> 不设 `danger` / `success`
- Bad：在卡片里手写一个 `<el-dropdown>` + 自定义触发器 + 一份新的菜单项

### 6. Tests Required

- `cd web && npm run build`。
- 浏览器实测：
  - 390 宽下五个列表页的「···」都贴在卡片右上角，菜单项与桌面操作列一致；观察者账号下任务卡片的菜单里有「详情」「日志文件」
  - 任务菜单（桌面 Split Button 与移动端「···」）里「启用」常态就是绿字、hover / 键盘聚焦时再加淡绿底；「禁用」仍是常态红字 + 悬停淡红底，两者对称。明暗两种主题都看

### 7. Wrong vs Correct

#### Wrong

```vue
<!-- 移动端另写一份菜单：与桌面漂移，也没有 success / danger 配色 -->
<el-dropdown @command="onMobileCommand">
  <span class="more">···</span>
  <template #dropdown>
    <el-dropdown-menu><el-dropdown-item command="enable">启用</el-dropdown-item></el-dropdown-menu>
  </template>
</el-dropdown>
```

#### Correct

```vue
<div class="dd-mobile-card__head">
  <el-checkbox … />
  <span class="dd-mobile-card__name" :title="row.name">{{ row.name }}</span>
  <el-tag size="small">…</el-tag>
  <DdMoreMenu :items="taskActionItems(row, { alwaysShowDetail: true })" @command="onTaskAction($event, row)" />
</div>
```

---

## Scenario: 翻页回到顶部（`utils/scrollToTop.ts`，v3.3.1，issue #143 O3）

### 1. Scope / Trigger

- 触发：给列表页的分页器接事件、改 `web/src/utils/scrollToTop.ts`、新建带分页的列表页时，必须看本节。桌面与移动端都生效。

### 2. Signatures

```ts
export function scrollListToTop(anchor?: Element | null, extraTargets?: Array<Element | null | undefined>): void
```

- 当前调用方：tasks、subscriptions、envs、logs、deps、users、notifications、open-api 八个列表页。

### 3. Contracts

- **为什么不能只滚一个固定容器**：真正在滚的元素随断点和页面类型变化。
  ≤768 是外层 `.layout-main`；≥769 的 `dd-scroll-page`（deps / open-api 等）是页面根自己；
  ≥769 的 `dd-fixed-page`（tasks / logs / envs / subscriptions）是页面根里的 `.table-card`，el-table 定了高度时是 `.el-table__body-wrapper .el-scrollbar__wrap`；
  桌面的 `.layout-main` / `.route-shell` 都是 `overflow: hidden`，`scrollTop` 恒为 0。
  所以函数把「可能在滚的」都收集起来，只处理 `scrollTop > 0` 的，其余不动：
  1. 锚点自身及整条祖先链，走到弹窗遮罩 `.el-overlay` 就停（锚点在弹窗里时，不去拽弹窗背后列表的阅读位置）；
  2. 锚点内部的 `.table-card` 与 `.el-table__body-wrapper .el-scrollbar__wrap`；
  3. `extraTargets`。
- `anchor` 传**页面根的 ref**。不传时取 `.route-shell > :not(.page-shell-leave-active)`：切页时新旧两页会短暂并存，不能取到正在离场的旧页。
- 锚点 `offsetParent === null`（页面被 keep-alive 失活、DOM 已脱离文档，或被隐藏）时直接返回：量出来全是 0，滚了也白滚；
  也挡住「数据回来时用户已经切走了」的情况，不去动别的页面的滚动位置。
- 一律 `behavior: 'auto'`（瞬时）：翻页是整块换内容，平滑滚动会和换数据叠在一起，画面发飘。
  刻意不用 `scrollIntoView`：它会连带滚动所有能滚的祖先，还可能横向滚动带 overflow 的容器（理由同 `ScriptsSidebar.vue` 的 `scrollRowIntoView`）。
- 🔴 **只挂在分页器的 `current-change` / `size-change` 上**，不能挂进 `loadXxx`，也不能挂进 `watch(page)`：
  自动刷新、轮询、增删改后的重拉都会调它们，用户正往下看着，会被一把拽回顶部。
  EP 分页器只在用户点页码、改每页条数、total 变小把页码夹回时才 emit 这两个事件，程序直接改 `v-model` 不会触发，所以挂在事件上正好等于「用户翻页」。
- **等数据回来、DOM 换完之后再调**：`await loadXxx()` → `await nextTick()` → `scrollListToTop(...)`，避免「先回顶、再换内容」跳两下。
  前端分页的页面（users / notifications / open-api / deps）没有请求可等，只 `await nextTick()`。
  `size-change` 先把 page 置 1。分页器原来只有 `v-model` 的页面，要补上这两个事件监听。
- 异步翻页期间页面被卸载时 ref 已经是 null：不要把 null 传进去，否则会回落到「当前活动页」，把用户已经切过去的别的页面滚回顶部。
  tasks 的写法是 `if (pageRootRef.value) scrollListToTop(pageRootRef.value)`；被 keep-alive 失活时 ref 还在，由 `offsetParent` 判断挡掉。

### 4. Validation & Error Matrix

- 挂进 `loadXxx` / `watch(page)` -> 3 秒轮询、5 秒自动刷新把用户拽回顶部
- 数据回来之前就调 -> 先回顶再换内容，位置被新内容的高度顶偏
- 用 `scrollIntoView` 或平滑滚动 -> 整页 / 布局容器跟着跳，或者画面发飘
- 页面卸载后把 null 传进去 -> 别的页面被滚回顶部

### 5. Good/Base/Bad Cases

- Good：`handlePageChange` / `handlePageSizeChange` 里 `await loadXxx(); await nextTick(); scrollListToTop(pageRootRef.value)`
- Base：前端分页页面只等 `nextTick`
- Bad：`watch(page, () => { loadXxx(); scrollListToTop() })`

### 6. Tests Required

- `cd web && npm run build`。
- 浏览器实测：八个页面在 1440 桌面与 390 移动端各翻一次页，真正在滚的容器 `scrollTop === 0`；
  tasks（3 秒轮询）与 logs（开启自动刷新）滚到中间后等一轮刷新，滚动位置不变；翻页请求在途时切到别的菜单页，别的页面的滚动位置不变。

### 7. Wrong vs Correct

#### Wrong

```ts
// 轮询、自动刷新也会调 loadLogs：用户正往下看着，被拽回顶部
async function loadLogs() {
  // ...请求...
  scrollListToTop()
}
```

#### Correct

```ts
async function handlePageChange() {
  await loadLogs()
  await nextTick()
  scrollListToTop(pageRootRef.value)
}
function handlePageSizeChange() {
  page.value = 1
  void handlePageChange()
}
```

---

## Scenario: 任务标签输入（`TaskLabelPicker`，v3.3.5，issue #157）

### 1. Scope / Trigger

- 触发：改 `web/src/views/tasks/components/TaskLabelPicker.vue`；改 `TaskForm.vue` 的标签 / 任务分组输入或 `BatchAddLabelDialog.vue`；
  或在别处新做「输入框 + 候选 chip」这类「填了就要保存」的输入时，必须看本节。
- 背景：#157 ①「填了标签点更新，提示已更新，刷新后标签没建上」。原标签框只在 `@keyup.enter` 时变成标签，`handleSubmit` 只提交 `form.labels`，
  没回车的字被静默丢掉、照样提示成功（v1.0.0 起就这样，后端不丢标签）；批量弹窗的 `el-select allow-create` 失焦就清字，粘贴后按回车也建不出标签。

### 2. Signatures

- `TaskLabelPicker.vue`：`v-model: string[]`，只含用户可编辑的普通标签——`分组:` / `subscription:` 两类内部标签由宿主经 `splitTaskLabels` / `mergeTaskLabels` 自己藏起来、提交时并回去。
  `defineExpose({ commitPending })`：`commitPending(): boolean` 把输入框里的文字并进 v-model，有被拦下的段时返回 `false`（原文留在框里、已提示）。
  emit 是同步的，返回时宿主 v-model 绑的那份数组已经是新值，可以直接拿去组装请求体。
- 候选：挂载时 `taskApi.labels()`（`GET /tasks/labels`，裸数组，服务端契约 L1 见 backend `index.md`）→ `normalizeTaskNameCounts`（`taskLabels.ts`）→
  再滤一遍 `isInternalTaskLabel` → 按 count 降序、同 count 按 name 排。
- 宿主写法：`<TaskLabelPicker :key="labelPickerKey" ref="labelPickerRef" v-model="form.labels" />`，放在 `destroy-on-close` 的弹窗内容里，弹窗每次打开 `labelPickerKey++`。

### 3. 硬约定（每条都是「去掉就静默失效」）

1. **候选 chip、「添加」、「全部 N 个 / 收起」一律原生 `<button type="button">` + `@mousedown.prevent`。**
   - 是什么：不用 `el-button`、`el-check-tag`、`el-tag` 当可点的 chip；原生 button 两个属性都不能漏。
   - 为什么：漏写 `type`，它在 el-form 渲染的 `<form>` 里就是提交按钮，在输入框里按回车会触发原生隐式提交、**整页刷新**；
     不 `prevent`，点它会抢走输入框焦点，手机上键盘随之收起 / 弹出，连续加几个标签每点一次收一次；`el-check-tag` / `el-tag` 是 span，键盘聚焦不到。
   - 例子：任务表单「任务分组」下方的「已有分组」chip（`TaskForm.vue` 的 `.group-chip`）同一套约定、同一套样式，改一边要同步另一边。
2. **回车用 `@keydown.enter`，`e.isComposing || e.keyCode === 229` 时直接 return，否则 `preventDefault()` 后提交。不用 `@keyup.enter`。**
   - 为什么：输入法组字中按回车只是上屏（Chrome 报 `isComposing`，Safari 先发 compositionend、再发 keyCode 229 的 keydown）；
     Windows 中文输入法回车上屏英文时 keyup 报的是 Enter，用 keyup 会把刚上屏的字母直接变成标签。`preventDefault()` 挡掉 `<form>` 的隐式提交。
3. **宿主保存时先 `commitPending()`，再做空值校验、组装请求体。**
   - `TaskForm.handleSubmit`：放在各项校验之后、`const data = { ...form.value }` 之前；返回 `false` 时 `activeTab = 'basic'` 并中止。
   - `BatchAddLabelDialog.handleConfirm`：**先** `commitPending()`（`false` 就停），**再**做「请输入至少一个标签」——顺序反了，「打字不回车点确定」仍报空。
4. **输入状态随弹窗销毁。**
   - 是什么：输入框的值（`query`）只放在 picker 自己身上；picker 放在 `destroy-on-close` 的弹窗内容里，宿主每次打开再换一次 `:key`。
   - 为什么：`TaskForm` 常驻挂载，`destroy-on-close` 只卸 DOM；关窗动画没走完就重开时 EP 不重建内容。少了 `:key`，A 任务里没回车的字会带进下一个打开的任务，保存时被并进去（#157 N3）。
5. **内部前缀只校验新输入，不碰已有标签。** 新加的段 trim 后以 `分组:` / `subscription:` 开头 → 拦下、原文留在输入框，
   提示「标签不能以「分组:」或「subscription:」开头，分组请填在「任务分组」里」（`grouping: true`，每次操作只提示一次），合法的段照常加入。
   已选区里的历史脏数据（如 ` subscription:1`）不校验，否则这个任务保存不了。
6. **点候选只替换最后一段，前面各段先提交。** `pickCandidate`：`query.split(/[,，]/)` 弹出最后一段（那是搜索词），`commitSegments([...前面各段, name])`。
   只「加 chip + 清空输入框」会把「a,b,监」点「监控」里的 a、b 静默丢掉（#157 N1）。
7. **与候选忽略大小写相等时用候选的写法**（候选已按 count 降序，取最常用的那个变体）；与已选忽略大小写相等算重复、不再加。
   输入框加 `autocapitalize="off" autocorrect="off" spellcheck="false" enterkeyhint="done"`：手机句首自动大写会造出第二个「Prod」；
   安卓 Chrome 不设 `enterkeyhint` 时，后面紧跟「任务分组」输入框，软键盘动作键是「下一项」、不发回车。
8. **`el-select allow-create` 不能做「填了就保存」的输入。** 失焦或收起时 EP 会清掉没回车的文字；没有候选时粘贴、单字后按回车也建不出项（悬停索引被 nextTick 复位）。
   环境变量分组（`EnvEditDialog` / `EnvBatchGroupDialog`）、面板时区（`SystemConfigCard`）仍是这种写法，属已知同类，进 backlog，别顺手改。

其它口径：

- 拆分按 `/[,，]/`（半角、全角逗号都拆），逐段 trim、去空、去重：存储按半角逗号拼接，标签里留着逗号保存后会被拆成两个。
- **刻意不做「失焦即并入」**：输入框同时是搜索框，打半截字去点别处就入库不对；只在回车、点「添加」、宿主保存三处并入。
  有未提交文字时，输入框下方提示「保存时会把「xx」作为新标签一起保存」，让「所见即所得」名副其实。
- 每次操作只 emit 一次（`commitSegments`）：v-model 的 props 要等下一次渲染才更新，同一拍里连 emit 两次，第二次读到的还是旧的已选。
- 候选加载失败、老面板 404、权限不足、演示站兜底体 `{data: []}`、不是数组 → 一律当「没有候选」，**静默**，输入框照常可用。
- 「任务分组」保持 `el-input`（所见即所得，不存在没回车就丢）；分组名含半角逗号时拦下，提示「分组名不能包含英文逗号」并切回「基本信息」。

### 4. Tests Required（仓库无前端单测，`npm run build` + 浏览器实测）

- 桌面 1280 与手机 390 各一遍：
  - 打字不回车直接点「创建 / 更新」→ 刷新后标签在，按该标签建的视图能筛出这个任务；
  - `a,b，c` 回车得到三个；「a,b,监」再点「监控」得到 a、b、监控；
  - 组字中按回车不提交；在输入框回车不触发整页刷新；手机上点候选 chip、「添加」不弹 / 不收键盘；
  - 打 `分组:xx` 点保存：被拦一次、原文留在框里、停在「高级设置」时自动切回「基本信息」；
  - A 任务里打字 → 取消 → 打开任务 C 或新建：输入框是空的；
  - 批量添加标签弹窗打字不回车点「确定」也能加上。
- 演示站：`npm run build:demo` 后任务表单能看到已有标签；跑完必须再 `npm run build` 还原 `web/dist`。

### 5. Wrong vs Correct

#### Wrong

```vue
<!-- 只在 keyup 回车时入列：没回车的字被静默丢掉；输入法回车上屏也被当成提交 -->
<el-input v-model="labelInput" @keyup.enter="addLabel" />
<!-- 漏 type：在输入框回车触发原生提交、整页刷新；不 prevent：手机上点一下收一次键盘 -->
<button class="chip" @click="pick(item.name)">{{ item.name }}</button>
<!-- 失焦就清掉没回车的字 -->
<el-select v-model="labels" multiple filterable allow-create default-first-option />
```

```ts
// 顺序反了：「打字不回车点确定」仍报「请输入至少一个标签」
if (labels.value.length === 0) return ElMessage.warning('请输入至少一个标签')
pickerRef.value?.commitPending()
```

#### Correct

```vue
<el-input
  v-model="query"
  enterkeyhint="done"
  autocapitalize="off"
  autocorrect="off"
  spellcheck="false"
  @keydown.enter="handleEnter"
/>
<button type="button" class="label-picker__chip" @mousedown.prevent @click="pickCandidate(item.name)">…</button>
```

```ts
function handleEnter(event: Event | KeyboardEvent) {
  const e = event as KeyboardEvent
  if (e.isComposing || e.keyCode === 229) return // 组字中按回车只是上屏
  e.preventDefault()                              // 挡掉 <form> 的隐式提交
  commitPending()
}

async function handleConfirm() {
  if (pickerRef.value && !pickerRef.value.commitPending()) return // 先并入没回车的字（有非法段就停）
  const cleaned = Array.from(new Set(labels.value.map(label => label.trim()).filter(Boolean)))
  if (cleaned.length === 0) return ElMessage.warning('请输入至少一个标签') // 再做空值校验
  // ...
}
```

---

## Scenario: 环境变量的「重要」标记与删除确认（v3.3.6，APP #16）

### 1. Scope / Trigger

- 触发：改 `web/src/views/envs/index.vue` 的 `handleDelete` / `handleBatchDelete` / `handleImport` / `handleToggleImportant` / `buildEnvActionItems`、名称列或移动卡片首行；
  改 `EnvEditDialog.vue` 的「重要变量」开关或 `handleSave`；改 `api/env.ts` 的 `EnvPayload` / `EnvUpdatePayload`；
  或者在别的列表页给删除加「专门确认 / 批量删除跳过」时，必须看本节。
- 背景：APP #16 ②「重要变量醒目显示、防误删」。PRD D1 定的是轻量档：**服务端不拦删除**（脚本、开放 API、MCP、ddp、老 APP 照样能删），
  「专门确认」「批量删除跳过」只在客户端做，网页这几道确认就是全部防线。所以界面文案不写「受保护」「无法删除」。
- 服务端契约（`ToDict` 永远带 `important`、`PUT /envs/:id` 的 `important` 是 `*bool`、merge 导入只升不降）见 backend `index.md` 的契约 E1；
  配色、与「值」列遮蔽锁的区分见 `design-system.md` §1.3。

### 2. Signatures

```ts
// api/env.ts
EnvPayload.important?: boolean        // 新建：开着才带 true，没开不带这个键
EnvUpdatePayload.important?: boolean  // 编辑：false 是合法修改（取消重要）；编辑弹窗只在拨过开关时带
```

- `views/envs/index.vue`：
  - `handleToggleImportant(row)`：`envApi.update(row.id, { important: !row.important })`，只发这一个键；
  - `buildEnvActionItems(row)` 在置顶两项之后加 `{ key: 'important', label: '标为重要', visible: !important }`、`{ key: 'cancel-important', label: '取消重要', visible: important }`，
    由 `onEnvAction` 分发；桌面操作列 ▾ 与移动卡片「···」（`DdMoreMenu`）用的是同一份数组；
  - `handleDelete(row)`（v3.3.6 起收整行，原来收 `id`；唯一调用点是 `onEnvAction`）、`handleBatchDelete()`、`handleImport(payload: { envs: any[]; mode: string })`；
  - `openCreate` 与 `openDuplicate` 给 `important: false`，`openEdit` 给 `row.important === true`。
- `EnvEditDialog.vue`：`EnvFormModel.important?: boolean`、`initialImportant = ref(false)`；「重要变量」`el-switch` 在「分组」之后、「排序值」之前，新建与编辑都显示。
- `EnvImportDialog.vue`：不变，只解析 JSON、`emit('import', { envs, mode })`，不发请求。

### 3. Contracts

**显示**
- 标签写法：`<span v-if="row.important" class="important-chip" title="重要变量：删除时会再确认一次"><el-icon><Lock /></el-icon>重要</span>`。
- 桌面名称列：放在「置顶」标签**之前**，两枚一起靠在最右缘。靠的是 `.env-name-filter-btn` 的 `margin-right: auto`，不是 `.env-name` 撑满（它是 `flex: 0 1 auto`）。
- 移动卡片：放在变量名**之后**、`.env-card__tags` 标签组**之外**，`flex-shrink: 0` 常驻；标签组的 `v-if` 不改。
  标签组是 `flex: 1 1 0`，变量名一长整组就被挤成 0 宽裁掉；置顶还有左缘橙条兜底，「重要」没有别的视觉通道，宁可让变量名早一点出省略号。

**设置入口**
- 行菜单「标为重要 / 取消重要」：可撤销，不弹确认；成功提示「已标为重要 / 已取消重要」后 `loadData()`，失败弹 `err?.response?.data?.error || '操作失败'`。
- 编辑弹窗 `EnvEditDialog.handleSave` 的发送规则：

  | 场景 | 请求体里的 `important` |
  |---|---|
  | 新建单条 | 开着才带 `true`，关着不带这个键 |
  | 新建 + 按行拆分 | 开着时拆出的**每一项**都带 `true`，关着都不带 |
  | 编辑 | 只在 `form.important !== initialImportant` 时带当前值；父组件 `handleSave` 的编辑分支在 `typeof data.important === 'boolean'` 时才放进 `EnvUpdatePayload` |

  理由同排序值（契约 C5）：弹窗打开时读到的旧值不能冲掉 APP 或另一个标签页刚改的标记；服务端对不带这个键的整体回写不动标记。
- 「复制同名变量」固定 `important: false`：复制同名变量多半是加一个账号，账号类变量不该跟着变成重要。

**删除**
- 🔴 **确认与请求分两段 `try`**：第一段只包确认框，catch 就是点了取消，直接 `return`；第二段包请求，catch 弹服务端原话
  （`err?.response?.data?.error || '删除失败'`，批量是 `'批量删除失败'`）。v3.3.5 及以前一个 catch 同时吞掉「点了取消」和「删除失败」，删失败时界面上什么都不显示。
- 单删 `handleDelete(row)`：重要变量弹专门确认，正文点名
  `「${row.name}」已标记为重要变量，删除后无法恢复。确定要删除吗？`，标题「删除重要变量」，`type: 'warning'`，按钮「仍要删除」；
  普通变量沿用「确定要删除该环境变量吗？」/「确认删除」。
- 批量删除 `handleBatchDelete()`（桌面批量条与手机批量栏共用）：
  1. `selectedRows = envList.value.filter(row => selectedIdSet.value.has(row.id))`，与「已选 N 项」（`selectedCountInCurrentPage`）同一口径。
     不能直接用 `selectedIds`：它可能含已不在列表里的 id，也看不出哪些行是重要变量。
  2. 拆成重要 K 条与可删 N−K 条。全是重要变量时 `ElMessage.warning('选中的都是重要变量，没有可删除的。确需删除请在行菜单里逐个删除，或先取消「重要」标记')`，**不发请求、不清勾选**。
  3. K > 0：确认框正文「选中的 N 个环境变量里有 K 个是重要变量，将跳过它们，只删除其余 N−K 个。」，按钮「删除 N−K 个」；K = 0 沿用原文案「确定要删除选中的 N 个环境变量吗？」。
  4. 只发可删项的 id；成功提示是 `res.message`，K > 0 时再接「，已跳过 K 个重要变量」。

**替换导入**
- 二次确认写在父组件 `handleImport`：它是本页唯一发导入请求、能调 `envApi` 的地方；本页弹窗一律「弹窗只 emit、父组件调接口」。
- `mode === 'replace'` 的顺序：
  1. 先置 `importSubmitting`（在数数之前，挡住数数与确认期间的连点）；
  2. `envApi.list({ all: 1 })`，**不带任何筛选**，数 `important === true` 的条数。列表是分页的、还可能带着筛选，当前页数不全；
     `all=1` 有 5000 条硬上限，超大库可能少数，只影响文案里的数字。数数失败就不点名，确认照弹；
  3. 标题「替换导入」、按钮「清空并导入」，正文「替换会先删除面板上的全部环境变量，再写入这 N 条。[面板上 K 个重要变量也会被清空，]这份内容里没有的变量将无法找回。」
     （K = 0 时没有方括号里那半句；与 APP 同文案）；
  4. 点取消：什么都不发，导入弹窗保持打开（`finally` 解开在途锁）。
- merge 导入不弹这个确认框（merge 不删变量，服务端对 `important` 只升不降）。
- 导入失败弹服务端原话 `err?.response?.data?.error || '导入失败'`（如「请求体过大（最大 1MB）」），不再一律「导入失败」。

**名称列 `min-width` 账本：188 → 204 → 230 → 296**（账记在 `index.vue` 名称列上方的注释里）
- `.env-name-wrap` 是 `flex` + `gap: 8px`，每多一个子元素，净增 = 元素自身宽度 + 一份 gap 8px：

  | 变化 | 新增元素 | 净增 |
  |---|---|---|
  | 188 → 204 | 名称**前**的 8px 状态圆点 | 8 + 8 = 16px |
  | 204 → 230 | 名称**后**的「只看这个变量名」按钮（图标 14 + padding 2×2 = 18px） | 18 + 8 = 26px |
  | 230 → 296（v3.3.6） | 名称后的「重要」标签（约 58px） | 58 + 8 = 66px |

- **置顶标签不计入，「重要」计入**。置顶标签从没进过这本账；「重要」是浏览器实测后加的：1280×900、侧栏展开，230 时既置顶又重要的行变量名只剩约 29px、可见 2 个字符，
  加宽 66px 后约 11 个，正好是 v3.3.5 时只置顶的行能看到的量。变量名长时两枚都有的行照样会出省略号，`title` 挂全名兜底。
- 改 `.important-chip` / `.pinned-chip` 的 padding、字号、图标，或改 `.env-name-filter-btn` 的 padding，要回来重算这本账，并在 1280 宽、侧栏展开时实测。

### 4. Validation & Error Matrix

- 单删 / 批量删除 / 导入的请求失败（DevTools 拦截）-> 弹服务端原话，或「删除失败 / 批量删除失败 / 导入失败」，不能静默
- 单删重要变量时点「取消」-> 不发 `DELETE /envs/:id`
- 批量删除勾 2 个重要 + 1 个普通 -> 确认框写「有 2 个是重要变量，将跳过它们」、按钮「删除 1 个」，请求体只有那 1 个 id，成功提示带「，已跳过 2 个重要变量」
- 批量删除只勾了重要变量 -> warning，不发请求，勾选保留
- 编辑弹窗只改备注就保存 -> 请求体没有 `important` 键；先在另一个标签页标为重要、再在本页保存，标记仍在
- 替换导入：`all=1` 数数失败 -> 确认照弹、不点名；K = 0 -> 没有「面板上 K 个重要变量也会被清空」那半句；点取消 -> 不发 `POST /envs/import`，导入弹窗不关
- merge 导入 -> 不弹「替换导入」确认
- 行菜单里「标为重要」「取消重要」同时出现 -> `visible` 写错（两者按 `row.important === true` 互斥）
- 把「重要」标签挪进移动卡片的 `.env-card__tags` -> 变量名一长，整组连同「重要」被裁掉

### 5. Good/Base/Bad Cases

- Good：服务端不拦的保护，确认框点名对象和数量；跳过了几个，在确认框和成功提示里都写明；确认与请求分两段 `try`
- Base：存量数据升级后全是 `false` -> 列表、单删、批量删除的观感与 v3.3.5 一致，只是行菜单多了「标为重要」、替换导入多了一道确认
- Bad：确认框写「受保护 / 无法删除」（行菜单里照样能删）；在 `EnvImportDialog` 里调 `envApi`；编辑弹窗每次保存都带上开关的值；批量删除拿 `selectedIds` 原数组发请求

### 6. Tests Required

- `cd web && npm run build`（含 vue-tsc）；演示站 `npm run build:demo` 之后必须再跑一次 `npm run build` 还原 `web/dist`。
- 服务端：`cd server && go test ./handler -run "TestEnv|TestCreate|TestUpdate|TestImport|TestUpsertByName|TestMCPEnvToolsCarryImportant|TestMCPListEnvs" -count=1`。
  其中 `TestEnvDeleteIgnoresImportantFlag` 钉住「服务端不拦删除」（网页确认是唯一防线的前提），
  `TestEnvImportantFlagCreateUpdateAndList` 钉住「不带 `important` 的整体 PUT 不动标记」（编辑弹窗只发拨过的开关靠的就是它）。
- 浏览器实测（仓库无前端单测）：
  - 1280 与 1920 宽 × 明暗 × 直角 / 圆角：只重要、只置顶、两者都有三种行，标签靠在最右缘、「重要」在前；1280×900 侧栏展开时两者都有的行可见约 11 个字符，悬停能看到全名
  - 行菜单（桌面 ▾ 与手机「···」）里「标为重要」「取消重要」互斥出现，点了以后标签随之出现或消失
  - 编辑弹窗：只改备注保存时 DevTools 里请求体没有 `important`；拨开关保存生效；新建时开着开关、按行拆分时开着开关，生成的每一行都是重要变量；复制同名变量时开关是关的
  - 单删、批量删除（桌面与手机批量栏）、替换导入按上面的矩阵各走一遍；用 CDP `Fetch.failRequest` 让删除失败，界面弹出错误
  - 375 宽手机卡片：变量名超长时「重要」仍紧跟在名字后面
  - 演示站：`TZ` 同时显示「重要」「置顶」，`NOTIFY_WEBHOOK_URL`、`BACKUP_ENCRYPT_KEY` 显示「重要」；设置与取消可用；导出的 JSON 带 `important`

### 7. Wrong vs Correct

#### Wrong

```ts
// 一个 catch 同时吞掉「点了取消」和「删除失败」：删失败时界面上什么都不显示
async function handleDelete(id: number) {
  try {
    await ElMessageBox.confirm('确定要删除该环境变量吗？', '确认删除', { type: 'warning' })
    await envApi.delete(id)
    ElMessage.success('删除成功')
  } catch {
    // cancelled
  }
}

// 拿 selectedIds 原数组发请求：可能含已不在列表里的 id，重要变量也被一起删掉
await envApi.batchDelete(selectedIds.value)
```

```ts
// 编辑弹窗每次都带开关的值：弹窗打开时的旧值会冲掉 APP / 另一个标签页刚改的标记
payload.important = form.value.important
```

#### Correct

```ts
async function handleDelete(row: any) {
  try {
    if (row.important) {
      await ElMessageBox.confirm(`「${row.name}」已标记为重要变量，删除后无法恢复。确定要删除吗？`, '删除重要变量', {
        type: 'warning',
        confirmButtonText: '仍要删除'
      })
    } else {
      await ElMessageBox.confirm('确定要删除该环境变量吗？', '确认删除', { type: 'warning' })
    }
  } catch {
    return // 点了取消
  }
  try {
    await envApi.delete(row.id)
    ElMessage.success('删除成功')
    void loadData()
  } catch (err: any) {
    ElMessage.error(err?.response?.data?.error || '删除失败')
  }
}
```

```ts
// 新建只在开着时带 true；编辑只在拨过开关时带当前值
if (isCreate.value ? form.value.important : form.value.important !== initialImportant.value) {
  payload.important = form.value.important === true
}
```
