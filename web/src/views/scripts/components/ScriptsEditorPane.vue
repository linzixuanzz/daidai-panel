<script setup lang="ts">
import {
  computed,
  nextTick,
  onActivated,
  onBeforeUnmount,
  onMounted,
  ref,
  watch,
} from "vue";
import {
  ArrowLeft,
  ArrowRight,
  Check,
  Clock,
  Close,
  Delete,
  Document,
  Download,
  Edit,
  Expand,
  MagicStick,
  MoreFilled,
  Plus,
  Setting,
  Switch,
  VideoPlay,
} from "@element-plus/icons-vue";
import CodeEditor from "@/components/CodeEditor.vue";
import {
  EDITOR_INDENT_WIDTH_OPTIONS,
  EDITOR_PREFERENCES_CHANGE_EVENT,
  EDITOR_WHITESPACE_MODES,
  cycleEditorOption,
  editorIndentWidthLabel,
  editorWhitespaceLabel,
  ensureEditorPreferencesLoaded,
  readEditorPreferences,
  setEditorPreference,
} from "@/utils/editorPreferences";
import type { EditorPreferences } from "@/utils/editorPreferences";
import {
  EDITOR_ENGINE_CHANGE_EVENT,
  persistEditorEngine,
  readStoredEditorEngine,
  resolveEditorEngine,
} from "@/utils/editorEngine";
import type {
  EditorEngine,
  ResolvedEditorEngine,
} from "@/utils/editorEngine";
import { setUnsavedWork } from "@/utils/chunkReload";

const fileContent = defineModel<string>("fileContent", { required: true });
const isEditing = defineModel<boolean>("isEditing", { required: true });

const props = defineProps<{
  isMobile: boolean;
  mobileShowEditor: boolean;
  /** 目录树是否已收起（只可能在桌面宽屏为 true），用来决定要不要渲染展开把手 */
  sidebarCollapsed: boolean;
  onExpandSidebar: () => void;
  selectedFile: string;
  isBinary: boolean;
  hasChanges: boolean;
  saving: boolean;
  formatting: boolean;
  loading: boolean;
  editorLanguage: string;
  editorAutoFocusTicket?: number;
  onMobileBack: () => void;
  onDebugRun: () => void | Promise<void>;
  onOpenCreateFile: () => void | Promise<void>;
  onAddToTask: () => void;
  onSave: () => void | Promise<void>;
  onCancelEdit: () => void | Promise<void>;
  onFormat: () => void | Promise<void>;
  onLoadVersions: () => void | Promise<void>;
  onOpenRename: () => void;
  onDownload: () => void;
  onDelete: () => void | Promise<void>;
}>();

const fileName = computed(() => {
  if (!props.selectedFile) return "";
  return props.selectedFile.split("/").pop() || props.selectedFile;
});

const filePath = computed(() => {
  if (!props.selectedFile) return "";
  const parts = props.selectedFile.split("/");
  parts.pop();
  return parts;
});

const languageLabel = computed(() => {
  const lang = (props.editorLanguage || "").toLowerCase();
  if (!lang) return "";
  const map: Record<string, string> = {
    javascript: "JS",
    typescript: "TS",
    python: "PY",
    shell: "SH",
    bash: "SH",
    yaml: "YAML",
    json: "JSON",
    markdown: "MD",
    html: "HTML",
    css: "CSS",
    go: "GO",
    plaintext: "TXT",
  };
  return map[lang] || lang.toUpperCase().slice(0, 4);
});

const fileSizeLabel = computed(() => {
  if (props.isBinary) return "";
  if (typeof fileContent.value !== "string") return "";
  const bytes = new Blob([fileContent.value]).size;
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
});

const lineCountLabel = computed(() => {
  if (props.isBinary || !fileContent.value) return "";
  // 三种行尾都算一次换行：编辑器会把正文原样留着（CRLF、单 CR 都可能），
  // 只按 "\n" 切会把「只用单个 CR 换行」的文件数成 1 行。口径与配置文件页一致。
  const count = fileContent.value.split(/\r\n|\n|\r/).length;
  return `${count} 行`;
});

// 图片预览：MIME 按扩展名映射，不用后端的 mime 字段（Alpine 上 ico/bmp 可能给 octet-stream）
const IMAGE_MIME_MAP: Record<string, string> = {
  png: "image/png",
  jpg: "image/jpeg",
  jpeg: "image/jpeg",
  gif: "image/gif",
  webp: "image/webp",
  bmp: "image/bmp",
  ico: "image/x-icon",
};
const imageFailed = ref(false);
const imageSize = ref<{ width: number; height: number } | null>(null);
const imageViewerVisible = ref(false);

const imageSrc = computed(() => {
  if (!props.isBinary || !fileContent.value) return "";
  const ext = props.selectedFile.split(".").pop()?.toLowerCase() || "";
  const mime = IMAGE_MIME_MAP[ext];
  return mime ? `data:${mime};base64,${fileContent.value}` : "";
});

// 换了文件就清掉上一张图的失败标记和尺寸
watch(imageSrc, () => {
  imageFailed.value = false;
  imageSize.value = null;
  imageViewerVisible.value = false;
});

const imageInfoLabel = computed(() => {
  // content 是 base64，按长度换算回原始字节数
  const b64 = fileContent.value || "";
  const padding = b64.endsWith("==") ? 2 : b64.endsWith("=") ? 1 : 0;
  const bytes = Math.floor((b64.length * 3) / 4) - padding;
  let sizeText = `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
  if (bytes < 1024) sizeText = `${bytes} B`;
  else if (bytes < 1024 * 1024) sizeText = `${(bytes / 1024).toFixed(1)} KB`;
  if (!imageSize.value) return sizeText;
  return `${imageSize.value.width}×${imageSize.value.height} · ${sizeText}`;
});

function onImageLoad(event: Event) {
  const img = event.target as HTMLImageElement;
  imageSize.value = { width: img.naturalWidth, height: img.naturalHeight };
}

function startEdit() {
  isEditing.value = true;
}

// 五项编辑器偏好（自动换行 / 缩略图 / 缩进参考线 / 空白符 / 缩进宽度）统一收在一个 ref 里，
// 不写成五组逐字雷同的 ref + toggle。
// v3.2.4 起它们不再只是 localStorage：utils/editorPreferences.ts 会把每次改动同步到
// /api/auth/preferences，换设备、换 IP 登进来还是这一份（issue #116）。
// 默认值全在那个模块里定，页面只管「读到什么用什么」，两页才不会各写一份默认值然后慢慢漂移。
const prefs = ref<EditorPreferences>(readEditorPreferences());

function toggleWordWrap() {
  const next = prefs.value.word_wrap === "on" ? "off" : "on";
  prefs.value.word_wrap = next;
  setEditorPreference("word_wrap", next);
}

// 两个布尔开关按名字读写。空白符曾经也在这里（v3.2.2 时它是二态），
// 现在它是三档循环，语义变了，所以拆去下面单独一个函数，不再挤在这个布尔通道里。
function toggleViewOption(name: "minimap" | "indent_guides") {
  const next = !prefs.value[name];
  prefs.value[name] = next;
  setEditorPreference(name, next);
}

// 缩进宽度与空白符是「点一下换下一档」的循环项，档位顺序与取下一档的算法都由
// utils/editorPreferences.ts 定，页面不复制一份（复制一份的表现是两页循环顺序不一致，而且构建全绿）。
function cycleIndentWidth() {
  const next = cycleEditorOption(
    EDITOR_INDENT_WIDTH_OPTIONS,
    prefs.value.indent_width,
  );
  prefs.value.indent_width = next;
  setEditorPreference("indent_width", next);
}

function cycleWhitespace() {
  const next = cycleEditorOption(
    EDITOR_WHITESPACE_MODES,
    prefs.value.whitespace,
  );
  prefs.value.whitespace = next;
  setEditorPreference("whitespace", next);
}

// 「在配置文件页改了一项」「刚从服务端拉回来了一份」都靠这个事件同步到本页。
// 刻意从存储重读、而不是从事件里取值：偏好可能是在另一个组件里改的，存储才是唯一真源
// （范式对齐 CodeEditor.vue 里的 syncEngine）。
function syncPreferences() {
  prefs.value = readEditorPreferences();
}

// 引擎偏好（auto / codemirror / monaco）：和上面几项一样是「两页共享」，
// 但它**不是**同一档设置，所以在菜单里被 divided 分成了独立一组 ——
// 上面四项是「同一个编辑器怎么显示」，改一下只是重配一个 Compartment；
// 而且它**刻意不同步到服务端**（理由见 utils/editorPreferences.ts 文件头：
// 引擎跟设备走，偏好跟人走），所以它仍然只读写 localStorage。
// 引擎是「换一个编辑器」，切一次要把实例整块拆掉重建，撤销历史、光标、滚动位置全丢
// （正文本身不丢，它活在这里的 fileContent 里，重建后会重新灌进去）。
const editorEngine = ref(readStoredEditorEngine());

// 只做展示：告诉用户「自动」这一档在这台设备上落到哪个引擎，
// 否则选了 auto 的人完全看不出自己在用什么。
// 刻意不响应窗口尺寸变化（resolveEditorEngine 内部读的是 window.innerWidth 快照）：
// auto 本来就只在编辑器挂载时解析一次，标签跟着拖窗口跳来跳去反而与实际用的引擎对不上。
const autoEngineLabel = computed(() =>
  resolveEditorEngine("auto") === "monaco" ? "Monaco" : "CodeMirror",
);

// 解析后的实际引擎。两处要用：底部状态条的引擎读数，以及齿轮菜单里「代码缩略图」渲不渲染
// —— 那一项只有 Monaco 认（CodeMirror 侧的缩略图在 v3.2.4 被删掉了），
// 在 CodeMirror 下留着它就是一个点了没反应的开关。
//
// 🔴 这里**不能**写成 computed(() => resolveEditorEngine(editorEngine.value))。
// resolveEditorEngine 在 auto 档下读的是 matchMedia('(pointer: coarse)') 和 window.innerWidth，
// 两者都不是响应式依赖，那个 computed 只会在 editorEngine 变时重算一次；
// 而真正决定「现在跑的是哪个引擎」的 CodeEditor 是**每次挂载时按当下窗口宽度重新解析**的，
// 本页的编辑器又挂在 v-if 上（isBinary 那条分支一进一出就整块重建）。
// 于是「宽窗口打开页面（菜单里有代码缩略图）→ 把窗口贴靠成半屏 → 切一个二进制文件再切回来」
// 之后编辑器已经是 CodeMirror，菜单里「代码缩略图」还在，点它开关翻 ON、正文毫无变化。
// 所以真源改成 CodeEditor 每次解析完 emit 上来的 engine-resolved（挂载时 + 引擎偏好变更后各一次）。
const resolvedEngine = ref<ResolvedEditorEngine>(
  // 初值只是兜底：编辑器还没挂载（二进制文件 / 还没选文件）时，齿轮菜单也要能渲染。
  // 编辑器一挂上来，第一次 engine-resolved 就会把它覆盖成真值。
  resolveEditorEngine(readStoredEditorEngine()),
);

function onEngineResolved(engine: ResolvedEditorEngine) {
  resolvedEngine.value = engine;
}

// 状态条读的是**解析后**的引擎而不是偏好值：用户关心的是「现在跑的是哪个」，
// 选了 auto 的人看到「auto」等于什么都没告诉他。
const activeEngineLabel = computed(() =>
  resolvedEngine.value === "monaco" ? "Monaco" : "CM",
);

function selectEditorEngine(next: EditorEngine) {
  if (editorEngine.value === next) return;
  editorEngine.value = next;
  // 顺手把 resolvedEngine 也推一次：编辑器没挂载（二进制文件）时不会有 engine-resolved 回来，
  // 少了这一行菜单里的「代码缩略图」会停在旧引擎上。编辑器挂着的话，下一拍 CodeEditor 会用
  // 同一份存储值 + 同一个窗口再解析一遍并 emit，结论必然相同，两者不会打架。
  // ⚠️ 只在这里补，不要在 onActivated 里也补一次：那时窗口宽度可能已经和编辑器挂载时不同，
  // 重解析出来的值会和实际跑着的实例对不上，正是上面注释里那个 bug 的翻版。
  resolvedEngine.value = resolveEditorEngine(next);
  persistEditorEngine(next); // 内部会派发 EDITOR_ENGINE_CHANGE_EVENT，已挂载的编辑器跟着换
}

// 引擎偏好不只在本页齿轮菜单里改：编辑器加载失败时的「改用 CodeMirror」（utils/monacoWarmup.ts）、
// 配置文件页的同一个菜单，都会走 persistEditorEngine 派发 EDITOR_ENGINE_CHANGE_EVENT。
// 编辑器和状态条靠 engine-resolved 跟得上，菜单里的「当前」只能靠这个监听跟：
// 不监听就要等下次进页面（onActivated）才更新，期间勾着的是已经不在用的那个引擎。
// 从存储重读而不是沿用本地值：存储写不进去时编辑器实际按 auto 解析，菜单也该显示 auto。
function syncEditorEngine() {
  editorEngine.value = readStoredEditorEngine();
}

// 有未保存改动时登记到 utils/chunkReload.ts：面板升级后的自动刷新改为提示「保存后刷新」，
// 关标签页 / 按 F5 也会先弹离开确认（没改动时不拦）。
// 条件与 hero 上的「未保存」圆点相同（圆点在 v-else 分支里，隐含 selectedFile 非空），不另算一套：
// hasChanges 由父组件按「正文 ≠ 已保存正文」算，保存、放弃改动、切换文件、删除当前文件都会让它回落。
// 被 keep-alive 缓存、切到别的页面时不撤：没保存的内容仍在内存里，整页刷新一样会丢，只在卸载时撤。
const UNSAVED_WORK_KEY = "scripts-editor";

watch(
  () => Boolean(props.selectedFile) && props.hasChanges,
  (dirty) => {
    setUnsavedWork(UNSAVED_WORK_KEY, dirty);
  },
  { immediate: true },
);

onMounted(() => {
  window.addEventListener(EDITOR_PREFERENCES_CHANGE_EVENT, syncPreferences);
  window.addEventListener(EDITOR_ENGINE_CHANGE_EVENT, syncEditorEngine);
  // 服务端那份偏好在这里拉一次（函数自己记忆化，重复调用不会重复发请求），
  // 拉回来之后它会派发变更事件，由上面的监听把新值刷进来。
  // 刻意不放进 main.ts 的启动流程：编辑器不在首屏，没必要给每一次打开面板都多加一个请求。
  void ensureEditorPreferencesLoaded();
});

onBeforeUnmount(() => {
  window.removeEventListener(EDITOR_PREFERENCES_CHANGE_EVENT, syncPreferences);
  window.removeEventListener(EDITOR_ENGINE_CHANGE_EVENT, syncEditorEngine);
  setUnsavedWork(UNSAVED_WORK_KEY, false);
});

// 脚本页被 keep-alive 缓存，第二次进来只触发 onActivated 不触发 onMounted。
// 上面那个变更事件监听在缓存期间是不摘的（keep-alive 只 deactivate 不 unmount），
// 所以「切走时别的页面改了偏好」其实已经同步过了；这里仍然同步补读一次，
// 兜的是「事件没派发」那条路径 —— 比如另一个标签页改的偏好、或者存储被外部清掉。
onActivated(() => {
  prefs.value = readEditorPreferences();
  // 引擎偏好没有走服务端同步，全靠这一行两页共享。漏掉它的表现是
  // 「在配置文件页切了引擎、切回脚本页菜单里还是旧的」——编辑器实例其实已经被
  // EDITOR_ENGINE_CHANGE_EVENT 换掉了，只有菜单勾选和状态条读数在骗人，比单纯不生效更难查。
  editorEngine.value = readStoredEditorEngine();
  void ensureEditorPreferencesLoaded();
});

// CodeEditor 由 Vite 直接打包、同步 import，没有运行期加载链路，
// 所以这里不再需要「空状态时提前预热」那一段（也不可能再有加载失败）。
const codeEditorRef = ref<{ focus?: () => void } | null>(null);

watch(
  () =>
    [
      isEditing.value,
      props.editorAutoFocusTicket,
      props.loading,
      props.isBinary,
      props.selectedFile,
    ] as const,
  ([editing, focusTicket, loading, binary, file]) => {
    if (!editing || loading || binary || !file || !focusTicket) return;
    void nextTick(() => {
      codeEditorRef.value?.focus?.();
    });
  },
);
</script>

<template>
  <section
    class="scripts-editor"
    :class="{ mobile: isMobile }"
    v-show="!isMobile || mobileShowEditor"
  >
    <!-- Empty state -->
    <div v-if="!selectedFile" class="editor-empty animate-fade-in-up">
      <div class="empty-card">
        <div class="empty-badge">
          <el-icon :size="20"><Plus /></el-icon>
        </div>
        <h2 class="empty-title">新建一个脚本开始使用</h2>
        <p class="empty-subtitle">
          从左侧选择已有脚本，或直接创建一个新文件并立即开始编辑、调试和添加任务。
        </p>
        <div class="empty-actions">
          <el-button
            class="create-cta"
            type="primary"
            size="large"
            @click="onOpenCreateFile"
          >
            <el-icon><Plus /></el-icon>新建脚本
          </el-button>
          <!-- 空状态里没有 hero，那个展开把手也就不存在。
               目录树收起 + 还没选文件时，这里如果不补一个入口就是没有退路的死状态
               （移动端的返回键只在 isMobile 才渲染，桌面用不上）。 -->
          <el-button
            v-if="!isMobile && sidebarCollapsed"
            size="large"
            @click="onExpandSidebar"
          >
            <el-icon><Expand /></el-icon>展开文件树
          </el-button>
        </div>
      </div>
    </div>

    <template v-else>
      <!-- Hero header -->
      <header class="editor-hero animate-fade-in-up">
        <div class="hero-file">
          <!-- 展开把手：只在「桌面 + 已折叠」时渲染。
               折叠后目录树宽度是 0，桌面端必须有个看得见的出口，否则用户回不去。 -->
          <el-tooltip
            v-if="!isMobile && sidebarCollapsed"
            content="展开文件树"
            placement="bottom"
          >
            <button
              class="sidebar-expand-btn"
              aria-label="展开文件树"
              @click="onExpandSidebar"
            >
              <el-icon :size="15"><Expand /></el-icon>
            </button>
          </el-tooltip>
          <el-button
            v-if="isMobile"
            class="mobile-back"
            text
            @click="onMobileBack"
            aria-label="返回文件列表"
          >
            <el-icon :size="18"><ArrowLeft /></el-icon>
          </el-button>
          <div class="file-icon" aria-hidden="true">
            <el-icon :size="18"><Document /></el-icon>
          </div>
          <div class="file-meta">
            <nav
              v-if="filePath.length > 0 && !isMobile"
              class="breadcrumb"
              aria-label="路径"
            >
              <template v-for="(seg, idx) in filePath" :key="idx">
                <span class="breadcrumb-seg">{{ seg }}</span>
                <el-icon
                  v-if="idx < filePath.length - 1"
                  class="breadcrumb-sep"
                  :size="10"
                >
                  <ArrowRight />
                </el-icon>
              </template>
            </nav>
            <div class="file-title-row">
              <h1 class="file-title" :title="selectedFile">{{ fileName }}</h1>
              <!-- 二进制（含图片）不显示语言标签，类型由旁边的「二进制 / 图片」标签说明 -->
              <span v-if="languageLabel && !isBinary" class="file-pill file-pill--lang">{{
                languageLabel
              }}</span>
              <span v-if="isBinary" class="file-pill file-pill--binary"
                >{{ imageSrc && !imageFailed ? "图片" : "二进制" }}</span
              >
              <span
                v-else-if="fileSizeLabel && !isMobile"
                class="file-pill file-pill--muted"
                >{{ fileSizeLabel }}</span
              >
              <span
                v-if="hasChanges"
                class="unsaved-pulse"
                role="status"
                aria-label="文件有未保存的改动"
              >
                <span class="unsaved-dot"></span>
                <span class="unsaved-label">未保存</span>
              </span>
            </div>
          </div>
        </div>

        <!-- issue #144 / v3.3.2：窄屏这排按钮全部收成纯图标，写法照 config-file/index.vue 的既有范式。
             不能只删 `:size` 绑定 —— EP default 档的横向 padding 是 15px，纯图标按钮实宽约 48px，
             编辑态这排最多 7 颗（保存/退出编辑/调试/添加任务/Wrap/齿轮/更多），48px 档要 384px，
             而 360px 屏这里可用宽只有约 312px，必折行；挂共享类 .dd-icon-only-btn 才是真正的 32×32，
             7×32 + 6×8 = 272px 放得下，且 16px 圆角配 32px 高刚好是正圆。
             aria-label / title 只给「桌面有可见文字、窄屏才退化成图标」的那几颗按上移动端条件：
             桌面读屏直接念可见文字，再挂一个不同的 aria-label 会违反「可见标签须包含在可访问名称里」；
             齿轮与「更多」本来就永远是纯图标，它们的 aria-label 无条件常驻。
             注意本组件收到的 isMobile 其实是 ≤1024 的 isCompactLayout（见 index.vue 的 :is-mobile），
             所以 769~1024 一档同样收成 32×32 图标、但圆角仍是桌面档，属已知且可接受的降级。 -->
        <div class="hero-actions">
          <el-button
            v-if="!isEditing"
            :class="['action-btn', { 'dd-icon-only-btn': isMobile }]"
            :disabled="isBinary"
            :aria-label="isMobile ? '编辑' : undefined"
            :title="isMobile ? '编辑' : undefined"
            @click="startEdit"
          >
            <el-icon><Edit /></el-icon><span v-if="!isMobile">编辑</span>
          </el-button>
          <template v-else>
            <el-button
              :class="['action-btn', 'action-btn--primary', { 'dd-icon-only-btn': isMobile }]"
              type="primary"
              :loading="saving"
              :disabled="!hasChanges || isBinary"
              :aria-label="isMobile ? '保存' : undefined"
              :title="isMobile ? '保存' : undefined"
              @click="onSave"
            >
              <el-icon><Check /></el-icon><span v-if="!isMobile">保存</span>
            </el-button>
            <el-button
              :class="['action-btn', 'action-btn--cancel', { 'dd-icon-only-btn': isMobile }]"
              :disabled="saving"
              :aria-label="isMobile ? '退出编辑' : undefined"
              :title="isMobile ? '退出编辑' : undefined"
              @click="onCancelEdit"
            >
              <el-icon><Close /></el-icon><span v-if="!isMobile">退出编辑</span>
            </el-button>
          </template>

          <el-button
            :class="['action-btn', 'action-btn--run', { 'dd-icon-only-btn': isMobile }]"
            :disabled="isBinary"
            :aria-label="isMobile ? '调试' : undefined"
            :title="isMobile ? '调试' : undefined"
            @click="onDebugRun"
          >
            <el-icon><VideoPlay /></el-icon><span v-if="!isMobile">调试</span>
          </el-button>

          <el-button
            :class="['action-btn', 'action-btn--task', { 'dd-icon-only-btn': isMobile }]"
            :disabled="isBinary"
            :aria-label="isMobile ? '添加任务' : undefined"
            :title="isMobile ? '添加任务' : undefined"
            @click="onAddToTask"
          >
            <el-icon><Plus /></el-icon><span v-if="!isMobile">添加任务</span>
          </el-button>

          <!-- 状态类按钮，排在动作类的「更多」之前。
               配色沿用本仓工具栏切换按钮的既有写法（开启时 primary + plain），
               与左边实心 primary 的「保存」靠 plain 区分，不抢主操作的注意力。
               窄屏收成纯图标，与同排按钮同一套 `<span v-if="!isMobile">` 收缩写法；
               状态另在底部状态条镜像成 `Wrap ON/OFF`，图标态下也看得出开关。 -->
          <el-tooltip
            :content="prefs.word_wrap === 'on' ? '关闭自动换行' : '开启自动换行'"
            placement="bottom"
          >
            <el-button
              :class="['action-btn', { 'dd-icon-only-btn': isMobile }]"
              :type="prefs.word_wrap === 'on' ? 'primary' : 'default'"
              :plain="prefs.word_wrap === 'on'"
              @click="toggleWordWrap"
            >
              <el-icon><Switch /></el-icon><span v-if="!isMobile" class="wrap-btn-label">Wrap</span>
            </el-button>
          </el-tooltip>

          <!-- 四个视图选项（缩略图 / 缩进参考线 / 缩进宽度 / 空白符）收在齿轮下拉里，不并排加按钮。
               .hero-actions 是 flex-shrink: 0 且祖先 .scripts-editor 带 overflow: hidden，
               桌面端装不下不会换行、也不会出滚动条，而是从右边缘被静默裁掉；
               1025~1280 这一档本来就已经在收 Wrap 的文字腾宽度（见下面那条 media query），
               再并排塞 4 个按钮必然裁掉最右侧的「更多」。齿轮本身是纯图标，只多占一个按钮的宽。
               :hide-on-click="false" 是必须的：这几项经常连着切（缩进宽度和空白符还要连点好几下
               才能转到想要的档），点一下就收菜单会逼用户反复重开。 -->
          <el-dropdown
            trigger="click"
            placement="bottom-end"
            :hide-on-click="false"
          >
            <el-button
              :class="['action-btn', { 'dd-icon-only-btn': isMobile }]"
              aria-label="编辑器选项"
            >
              <el-icon><Setting /></el-icon>
            </el-button>
            <template #dropdown>
              <el-dropdown-menu>
                <!-- 「代码缩略图」只在解析后的引擎是 Monaco 时渲染。
                     v3.2.4 删掉了 CodeMirror 侧的缩略图实现（它硬占正文右侧 64px，
                     手机 360px 宽下就是 18% 的正文没了；而桌面 auto 档本来就走 Monaco），
                     所以在 CodeMirror 下这一项已经没有任何东西可开。
                     宁可整项不渲染也不留一个「点了没反应、按钮还高亮、构建全绿」的开关。 -->
                <el-dropdown-item
                  v-if="resolvedEngine === 'monaco'"
                  @click="toggleViewOption('minimap')"
                >
                  <span class="opt-row">
                    <span>代码缩略图</span>
                    <span class="opt-state" :class="{ 'is-on': prefs.minimap }">{{
                      prefs.minimap ? "ON" : "OFF"
                    }}</span>
                  </span>
                </el-dropdown-item>
                <el-dropdown-item @click="toggleViewOption('indent_guides')">
                  <span class="opt-row">
                    <span>缩进参考线</span>
                    <span
                      class="opt-state"
                      :class="{ 'is-on': prefs.indent_guides }"
                      >{{ prefs.indent_guides ? "ON" : "OFF" }}</span
                    >
                  </span>
                </el-dropdown-item>

                <!-- 下面两项不是开关而是「点一下换下一档」的循环项，
                     光看一枚状态标读不出「还能点」，所以标题后面跟一句常驻的「点击切换」。
                     不用 el-tooltip：这是下拉浮层里的菜单项，再套一层 popper 只会在
                     悬停与层级上添乱，而这句提示本来就该常驻可见。

                     状态标的配色规则在这四项里是统一的一条：**这一项此刻有没有在改变正文的样子**。
                     缩略图 / 参考线关着是灰的；空白符「关闭」也是灰的；
                     而缩进宽度没有「关」这一档（自动也是一种生效的档位），所以恒为主色。 -->
                <el-dropdown-item @click="cycleIndentWidth">
                  <span class="opt-row">
                    <span class="opt-label"
                      >缩进宽度<span class="opt-hint">点击切换</span></span
                    >
                    <span class="opt-state is-on">{{
                      editorIndentWidthLabel(prefs.indent_width)
                    }}</span>
                  </span>
                </el-dropdown-item>
                <el-dropdown-item @click="cycleWhitespace">
                  <span class="opt-row">
                    <span class="opt-label"
                      >显示空白符<span class="opt-hint">点击切换</span></span
                    >
                    <span
                      class="opt-state"
                      :class="{ 'is-on': prefs.whitespace !== 'none' }"
                      >{{ editorWhitespaceLabel(prefs.whitespace) }}</span
                    >
                  </span>
                </el-dropdown-item>

                <!-- 引擎切换：用 divided 单独起一组，不和上面四项混排。
                     上面四项是「同一个编辑器怎么显示」，切一下只是重配一个 Compartment，代价为零；
                     引擎是「换一个编辑器」，切一次整块重建实例，撤销历史 / 光标 / 滚动位置全丢
                     （正文本身不丢，它活在本组件的 fileContent 里）。两者不是同一档，
                     并排放会让人以为引擎也是个随手可以来回拨的显示开关。

                     做成三项单选而不是一个「Monaco ON/OFF」开关：auto 既是默认值也是推荐值，
                     二值开关表达不了「跟着设备走」这个第三态，而这第三态正是
                     「触摸设备永远不给 Monaco」这条硬约束的落点 —— Monaco 是自绘编辑器，
                     长按系统菜单 / 选择手柄 / 拖动光标三条做不到，这也是当初换引擎的全部理由。

                     状态标写「当前」而不是沿用 ON/OFF：ON/OFF 是布尔读数，
                     放进三选一里会让人以为三个引擎能各自开关、甚至同时开着。
                     未选中的项不出标（而不是出一枚灰的「否」），三项里只有一枚标，
                     「哪个在生效」一眼可读，也不必再解释另外两枚是什么意思。

                     没有为这组新加按钮或新开一个「编辑器设置」入口：.hero-actions 是
                     flex-shrink: 0 且祖先 .scripts-editor 带 overflow: hidden，1025~1280 那一档
                     已经在收 Wrap 的文字腾宽度，再加按钮会被静默从右边缘裁掉；
                     何况同一页出现两个编辑器设置入口本身就是坏设计。 -->
                <el-dropdown-item divided @click="selectEditorEngine('auto')">
                  <span class="opt-row">
                    <span>引擎：自动（{{ autoEngineLabel }}）</span>
                    <span v-if="editorEngine === 'auto'" class="opt-state is-on"
                      >当前</span
                    >
                  </span>
                </el-dropdown-item>
                <el-dropdown-item @click="selectEditorEngine('codemirror')">
                  <span class="opt-row">
                    <span>引擎：CodeMirror</span>
                    <span
                      v-if="editorEngine === 'codemirror'"
                      class="opt-state is-on"
                      >当前</span
                    >
                  </span>
                </el-dropdown-item>
                <el-dropdown-item @click="selectEditorEngine('monaco')">
                  <span class="opt-row">
                    <span>引擎：Monaco</span>
                    <span
                      v-if="editorEngine === 'monaco'"
                      class="opt-state is-on"
                      >当前</span
                    >
                  </span>
                </el-dropdown-item>
              </el-dropdown-menu>
            </template>
          </el-dropdown>

          <el-dropdown trigger="click" placement="bottom-end">
            <el-button
              :class="['action-btn', { 'dd-icon-only-btn': isMobile }]"
              aria-label="更多操作"
            >
              <el-icon><MoreFilled /></el-icon>
            </el-button>
            <template #dropdown>
              <el-dropdown-menu>
                <el-dropdown-item
                  v-if="isEditing"
                  @click="onFormat"
                  :disabled="isBinary"
                >
                  <el-icon><MagicStick /></el-icon>格式化
                </el-dropdown-item>
                <el-dropdown-item @click="onLoadVersions" :disabled="isBinary">
                  <el-icon><Clock /></el-icon>版本历史
                </el-dropdown-item>
                <el-dropdown-item @click="onOpenRename">
                  <el-icon><Edit /></el-icon>重命名
                </el-dropdown-item>
                <el-dropdown-item @click="onDownload">
                  <el-icon><Download /></el-icon>下载
                </el-dropdown-item>
                <el-dropdown-item divided @click="onDelete">
                  <el-icon><Delete /></el-icon>
                  <span style="color: var(--dd-err-ink)">删除</span>
                </el-dropdown-item>
              </el-dropdown-menu>
            </template>
          </el-dropdown>
        </div>
      </header>

      <!-- Editor body -->
      <div class="editor-body animate-fade-in-up delay-50" v-loading="loading">
        <!-- 图片文件直接预览；解码失败回落到下面的二进制卡片 -->
        <div v-if="isBinary && imageSrc && !imageFailed" class="image-preview">
          <div class="image-preview-stage">
            <img
              :src="imageSrc"
              :alt="fileName"
              class="image-preview-img"
              @load="onImageLoad"
              @error="imageFailed = true"
              @click="imageViewerVisible = true"
            />
          </div>
          <div class="image-preview-meta">{{ imageInfoLabel }}</div>
          <el-image-viewer
            v-if="imageViewerVisible"
            :url-list="[imageSrc]"
            teleported
            hide-on-click-modal
            @close="imageViewerVisible = false"
          />
        </div>
        <div v-else-if="isBinary" class="binary-card">
          <div class="binary-card-title">二进制文件</div>
          <p class="binary-card-text">
            该文件为二进制格式，无法在线编辑。可通过右上角「更多 →
            下载」取回文件。
          </p>
        </div>
        <CodeEditor
          ref="codeEditorRef"
          v-else
          v-model="fileContent"
          :language="editorLanguage"
          :readonly="!isEditing"
          :word-wrap="prefs.word_wrap"
          :minimap="prefs.minimap"
          :indent-guides="prefs.indent_guides"
          :whitespace="prefs.whitespace"
          :indent-width="prefs.indent_width"
          class="code-editor"
          @engine-resolved="onEngineResolved"
        />
      </div>

      <!-- Status strip -->
      <footer v-if="!isBinary && selectedFile" class="editor-statusbar animate-fade-in-up delay-100">
        <div class="status-group">
          <span v-if="languageLabel" class="status-item status-item--lang">{{
            languageLabel
          }}</span>
          <span v-if="lineCountLabel" class="status-item">{{
            lineCountLabel
          }}</span>
          <span v-if="fileSizeLabel" class="status-item">{{
            fileSizeLabel
          }}</span>
        </div>
        <!-- ⚠️ 这一组是 overflow: hidden + white-space: nowrap：装不下不会换行、也不会出滚动条，
             而是从**右边缘**静默裁掉。往这里加标之前先想清楚谁会被挤掉。
             v3.2.4 就栽在这上面：往这组尾部加了 Tab / Space 两枚常驻标（空白符默认值同时
             从「关」改成「仅选中」，Space 也跟着常驻），内容宽度从约 229px 涨到约 331px，
             把当时排在最右的「编辑中 / 只读」整个挤出了可视区 —— 414px 下裁掉约 61px，
             那一项一个字都不剩，而它是这条状态条上唯一告诉用户「现在能不能改」的读数。
             现在那一项已经提到 footer 直属项上（见本组下方），这组里只剩「挤掉了也能从别处
             读到」的镜像读数，被裁的代价才是可接受的。 -->
        <div class="status-group status-group--right">
          <span class="status-item">UTF-8</span>
          <span class="status-item">LF</span>
          <!-- 当前引擎读数。和下面那几个视图开关不同，它是**常驻**的：
               引擎藏在齿轮下拉的第二组里，菜单一收起就完全看不出正在用哪个，
               而两个引擎的手感差别（多光标、折叠、触摸选区）远大于「空白符开没开」，
               「切了却看不出切没切」是最糟的一种反馈缺失，所以不做「非默认才显示」的省略。

               位置取舍：排在 UTF-8 / LF 之后、Wrap 与开关镜像之前。
               .status-group 带 overflow: hidden，装不下是从右边缘直接裁掉、不换行也不滚动，
               所以越靠前越不容易被裁 —— 这里按语义就近排（UTF-8 / LF / 引擎都是
               「这份文件正被什么处理」的静态事实，后面几个才是开关镜像），
               并把默认引擎的标签压到最短的 CM：
               默认档是 CodeMirror，常态下只多两个字符加一个间隙，
               只有主动切到 Monaco 的桌面用户才会多付那 4 个字符的宽度。 -->
          <span class="status-item">{{ activeEngineLabel }}</span>
          <!-- 镜像 hero 上那个 Wrap 按钮的状态：窄屏按钮收成纯图标后，这里是唯一能读出开关的地方 -->
          <span class="status-item">Wrap {{ prefs.word_wrap === "on" ? "ON" : "OFF" }}</span>
          <!-- 四个视图选项藏在齿轮下拉里，菜单一收起就看不出档位，所以这里镜像一份。
               排序按「菜单外还有没有第二处读数」来定，不按菜单里的顺序抄：
               Tab / Space 是循环档位（自动·2·4·6·8 / 仅选中·始终·关闭），排在前面；
               Map / Guide 是布尔，开着才渲染，排在后面。

               四枚**全部**带 status-item--optional，在 ≤1280px 一起收掉（见样式里那条 media query）。
               v3.2.4 时 Tab / Space 是不收的，理由写着「菜单外没有第二处读数」——
               但那一版正是因此把「编辑中 / 只读」挤没了：宁可少两枚在齿轮菜单里
               一眼就能读到的镜像标，也不能让唯一读不到第二处的那一项被裁。

               Space 沿用「关闭态不渲染」的老写法（whitespace === 'none' 时整段不出），
               Map 还多一个条件：CodeMirror 下这一项根本不存在，开着也不该在状态条上出现。 -->
          <span class="status-item status-item--optional"
            >Tab {{ editorIndentWidthLabel(prefs.indent_width) }}</span
          >
          <span
            v-if="prefs.whitespace !== 'none'"
            class="status-item status-item--optional"
            >Space {{ editorWhitespaceLabel(prefs.whitespace) }}</span
          >
          <span
            v-if="resolvedEngine === 'monaco' && prefs.minimap"
            class="status-item status-item--optional"
            >Map</span
          >
          <span
            v-if="prefs.indent_guides"
            class="status-item status-item--optional"
            >Guide</span
          >
        </div>
        <!-- 「现在能不能改」是这条状态条上唯一没有第二处读数的一项：hero 上那排按钮写的是
             「编辑 / 保存」这类动作名，读不出当前处在哪一态。所以它**不进**上面那个会被裁切的组，
             而是 footer 的直属项 + flex-shrink: 0 —— 任何视口宽度下都完整可见，
             宽度不够时挤掉的永远是它左边那些镜像读数。 -->
        <span
          class="status-item status-item--mode"
          :class="{ 'status-item--accent': isEditing }"
        >
          {{ isEditing ? "编辑中" : "只读" }}
        </span>
      </footer>
    </template>
  </section>
</template>

<style scoped lang="scss">
.scripts-editor {
  flex: 1;
  min-width: 0;
  min-height: 0;
  display: flex;
  flex-direction: column;
  /* 卡片表面用令牌，明暗自动适配（卡片边框由 index.vue 负责） */
  background: var(--el-bg-color);
  animation: dd-editor-shell-in 360ms var(--dd-ease-emphasized) both;
  font-family: var(--dd-font-ui);
  overflow: hidden;
}

/* ---------------- Empty state ---------------- */
.editor-empty {
  flex: 1;
  min-height: 0;
  overflow: auto;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 40px 24px;
}

.empty-card {
  position: relative;
  max-width: 480px;
  width: 100%;
  padding: 36px 32px 32px;
  text-align: center;
  // 空态提示卡是独立成块的容器类表面 → surface 档
  border-radius: var(--dd-radius-surface);
  background: var(--el-fill-color-light);
  border: 1px solid var(--el-border-color-lighter);
  overflow: hidden;
}

.empty-badge {
  width: 44px;
  height: 44px;
  margin: 0 auto 14px;
  // 44×44 的图标色底属控件类表面 → control 档（与 .dd-stat-card__icon 同档，不做正圆免得像头像）
  border-radius: var(--dd-radius-control);
  display: inline-flex;
  align-items: center;
  justify-content: center;
  color: var(--dd-on-accent);
  background: var(--dd-accent);
}

.empty-title {
  font-size: 18px;
  font-weight: 600;
  margin: 0 0 6px;
  letter-spacing: 0.2px;
  color: var(--el-text-color-primary);
}

.empty-subtitle {
  font-size: 13px;
  line-height: 1.55;
  margin: 0 0 20px;
  color: var(--el-text-color-secondary);
}

.empty-actions {
  display: inline-flex;
  gap: 10px;
  flex-wrap: wrap;
  justify-content: center;
}

/* 主 CTA 使用全局主按钮纯色样式，这里只约束宽度 */
.create-cta {
  min-width: 160px;
}

/* ---------------- Hero header ---------------- */
.editor-hero {
  padding: 14px 22px;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 14px;
  /* 卡片内顶部分区线（明暗令牌自适配） */
  border-bottom: 1px solid var(--el-border-color-lighter);
  background: var(--el-bg-color);
  flex-shrink: 0;
  position: relative;
  min-width: 0;
}

.hero-file {
  display: flex;
  align-items: center;
  gap: 12px;
  min-width: 0;
  flex: 1;
}

/* 展开把手：与 ScriptsSidebar.vue 里那个收起按钮（.icon-btn）同一副长相——
   30×30、透明底、1px 描边，hover 只改颜色不做位移；圆角统一吃 --dd-radius-button。
   两处相隔一个组件边界、scoped 样式互相够不到，只能各写一份；
   数值要改的话两边一起改。 */
.sidebar-expand-btn {
  width: 30px;
  height: 30px;
  padding: 0;
  flex-shrink: 0;
  border: 1px solid var(--el-border-color-lighter);
  background: transparent;
  // issue #144 / v3.3.2：改吃「按钮」角色令牌 —— 手机上 rounded 面板放大到 16px，与同屏其它按钮拉齐；
  // 桌面与 square 面板下该令牌恒等于 control 档，零变化。孪生体 ScriptsSidebar 的 .icon-btn 同档
  border-radius: var(--dd-radius-button);
  color: var(--el-text-color-secondary);
  cursor: pointer;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  transition: color var(--dd-motion-fast) var(--dd-ease-standard),
    background-color var(--dd-motion-fast) var(--dd-ease-standard),
    border-color var(--dd-motion-fast) var(--dd-ease-standard);

  &:hover {
    color: var(--dd-accent-ink);
    border-color: color-mix(
      in srgb,
      var(--el-color-primary) 40%,
      var(--el-border-color-lighter)
    );
    background: color-mix(in srgb, var(--el-color-primary) 6%, transparent);
  }

  &:focus-visible {
    outline: 2px solid
      color-mix(in srgb, var(--el-color-primary) 50%, transparent);
    outline-offset: 1px;
  }
}

.file-icon {
  width: 36px;
  height: 36px;
  // 36×36 的文件类型图标色底属控件类表面 → control 档
  border-radius: var(--dd-radius-control);
  display: inline-flex;
  align-items: center;
  justify-content: center;
  color: var(--dd-accent);
  background: color-mix(in srgb, var(--dd-accent) 10%, transparent);
  flex-shrink: 0;
}

.file-meta {
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.breadcrumb {
  display: flex;
  align-items: center;
  gap: 4px;
  font-size: 11.5px;
  color: var(--el-text-color-placeholder);
  line-height: 1.2;
  overflow: hidden;
  white-space: nowrap;
  text-overflow: ellipsis;

  .breadcrumb-seg {
    font-family: var(--dd-font-mono);
    letter-spacing: 0.2px;
  }

  .breadcrumb-sep {
    opacity: 0.5;
    flex-shrink: 0;
  }
}

.file-title-row {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}

.file-title {
  font-size: 16px;
  font-weight: 600;
  letter-spacing: 0.1px;
  color: var(--el-text-color-primary);
  margin: 0;
  max-width: 100%;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.file-pill {
  display: inline-flex;
  transition: background-color 0.16s ease, color 0.16s ease;
  align-items: center;
  height: 20px;
  padding: 0 8px;
  // 类名虽叫 pill，实际是「语言 / 只读 / 二进制」这类静态标签，不是状态灯：
  // 与 el-tag、.dd-env-chip 同归控件类 → control 档，
  // 好和旁边真正的状态 chip（.unsaved-pulse，pill 档）拉开主次。
  border-radius: var(--dd-radius-control);
  font-size: 10.5px;
  font-weight: 600;
  letter-spacing: 0.4px;
  font-family: var(--dd-font-mono);
  line-height: 1;
}

.file-pill--lang {
  background: color-mix(in srgb, var(--dd-accent) 10%, transparent);
  color: var(--dd-accent-ink);
}

.file-pill--muted {
  background: var(--el-fill-color-light);
  color: var(--el-text-color-secondary);
}

.file-pill--binary {
  background: color-mix(in srgb, var(--dd-warn) 12%, transparent);
  color: var(--dd-warn-ink);
}

.unsaved-pulse {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  height: 20px;
  padding: 0 9px 0 6px;
  // 「未保存」是状态 chip，天然胶囊 → pill 档（与 .dd-status-chip 同档）
  border-radius: var(--dd-radius-pill);
  font-size: 11px;
  color: var(--dd-warn-ink);
  background: color-mix(in srgb, var(--dd-warn) 10%, transparent);

  /* 未保存标记：呼吸点 + 明暗渐变（不做缩放形变） */
  .unsaved-dot {
    width: 6px;
    height: 6px;
    // 白名单：形状承载语义 —— 6×6 的呼吸点是「有未保存改动」的状态灯，
    // 方化后和旁边的文字糊成一小块色斑，认不出是状态指示。
    // 两种 shape 模式下都固定圆形，不吃 --dd-radius-* 刻度（同 global.scss 的 .pulse-dot）。
    border-radius: 50%;
    background: var(--dd-warn);
    animation: unsaved-pulse 1.6s ease-in-out infinite;
  }

  .unsaved-label {
    font-weight: 600;
    letter-spacing: 0.3px;
  }
}

@keyframes unsaved-pulse {
  0%,
  100% {
    opacity: 1;
  }
  50% {
    opacity: 0.4;
  }
}

// 页面自带的「减少动效」规则统一走这个包装（C8 动效偏好，本文件末尾编辑器入场那段也用它）：
// - 跟随系统：媒体查询里带 :root:not(.dd-motion-force)，个人设置选「始终开启」时不生效；
// - 个人设置选「减少动效」：html.dd-motion-off 下不看系统同样生效。
// 与 global.scss 末尾「减少动效」段同一口径；前缀包在 :where() 里，不给选择器额外加特异性。
@mixin dd-page-reduced-motion {
  @media (prefers-reduced-motion: reduce) {
    :where(:root:not(.dd-motion-force)) {
      @content;
    }
  }

  :where(html.dd-motion-off) {
    @content;
  }
}

@include dd-page-reduced-motion {
  // 必须带上 .unsaved-pulse：呼吸点的 animation 写在 .unsaved-pulse 里的嵌套规则上（特异性高一级），
  // 原来的裸 .unsaved-dot 压不过它、一直是死规则（只是 global.scss 的通配段把时长压成 1ms 兜住了，看不出来）。
  .unsaved-pulse .unsaved-dot {
    animation: none;
  }
}

// issue #144 / v3.3.2：这里原来是一块灰底圆角槽（padding + border-radius + color-mix 底色），
// 已收成纯排布容器，与侧栏 ScriptsSidebar 的 .sidebar-toolbar-actions 同一口径
.hero-actions {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  flex-shrink: 0;

  // EP 自带 `.el-button + .el-button { margin-left: 12px }` 会叠加在 gap 上，而齿轮和「更多」外层是
  // div.el-dropdown、选择器不命中，同排间距就会一宽一窄（相邻 el-button 20px、下拉前只有 8px）。
  // 清掉之后间距只由 gap 决定，与全仓另外 15 处工具栏写法一致
  .el-button + .el-button {
    margin-left: 0;
  }
}

.action-btn {
  // issue #144 / v3.3.2：改吃「按钮」角色令牌 —— 桌面恒等于 control 档，
  // 手机上 rounded 面板是 16px，配 .dd-icon-only-btn 的 32px 高正好是正圆
  border-radius: var(--dd-radius-button);
  font-weight: 500;
  transition: background-color 0.18s ease, border-color 0.18s ease, color 0.18s ease;
}

/* 1025~1280 这一档：还没进 compact（按钮仍带文字），但编辑器卡被 300px 目录树挤到只剩 ~450px。
   .hero-actions 是 flex-shrink: 0，装不下不会换行，而是直接被卡片的 overflow: hidden 从右边裁掉。
   Wrap 是这排里唯一「不看文字也知道状态」的按钮（底部状态条镜像了 Wrap ON/OFF，还有 tooltip），
   所以这一档优先收掉它的文字，把宽度让给动作类按钮。目录树收起后编辑器多出 314px，更不会紧张。
   齿轮（编辑器选项）没参与这条规则：它本来就是纯图标、没有文字可收，
   三个视图开关全塞在它的下拉里，正是为了在这一档只多占一个按钮的宽度。 */
@media screen and (min-width: 1025px) and (max-width: 1280px) {
  .wrap-btn-label {
    display: none;
  }
}

/* 齿轮下拉里的视图选项：左侧标题、右侧一枚状态标。
   下拉菜单项没有 EP 现成的选中态可用，只画一个对勾的话「没对勾」既可能是关、
   也可能是没渲染上，读不出确定状态，所以所有状态都出字。
   配置文件页（views/config-file/index.vue）有一份长相相同的规则：两边都是 scoped 样式、
   隔着组件边界互相够不到，只能各写一份，数值要改就两边一起改。 */
.opt-row {
  display: flex;
  flex: 1;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  width: 100%;
}

/* 循环档位那两项（缩进宽度 / 显示空白符）的标题：正文后面跟一句常驻的「点击切换」。
   两项都是「点一下换下一档」，而右侧那枚标只写当前档位、看不出还能点，
   没有这句提示的话很容易被当成一个坏掉的只读读数。
   写成同一行的次要文字而不是第二行副标题：菜单项是固定行高的单行控件，
   撑成两行会让这两项在四项里高出一截，反而像是别的什么东西。 */
.opt-label {
  display: inline-flex;
  align-items: baseline;
  gap: 6px;
  min-width: 0;
}

.opt-hint {
  flex-shrink: 0;
  font-size: 10px;
  letter-spacing: 0.2px;
  color: var(--el-text-color-placeholder);
}

.opt-state {
  flex-shrink: 0;
  padding: 0 6px;
  height: 16px;
  line-height: 16px;
  // 跟着它所在的下拉菜单项走 control 档（令牌表里「下拉/浮层菜单项」就是这一档）：
  // 菜单项是方角时里面嵌一颗胶囊会看出错位。它也不是状态灯（那类走 pill 档），
  // 只是把开关状态写成字的静态读数，和同页 .file-pill 的取舍一致。
  border-radius: var(--dd-radius-control);
  font-family: var(--dd-font-mono);
  font-size: 10px;
  font-weight: 600;
  letter-spacing: 0.4px;
  // 关闭态用 regular 而不是更淡的 placeholder：这枚标是开关状态的唯一读数，
  // 10px 的小字再配 placeholder（明色下约 2.5:1）就没法读了，等于白做一个状态标。
  // 与开启态的主色 + 主色淡底相比，它已经靠「无彩色 + 灰底」退到次要位置，不需要再靠压低对比度区分。
  color: var(--el-text-color-regular);
  background: var(--el-fill-color);
  transition: background-color var(--dd-motion-fast) var(--dd-ease-standard),
    color var(--dd-motion-fast) var(--dd-ease-standard);

  &.is-on {
    color: var(--dd-accent-ink);
    background: color-mix(in srgb, var(--el-color-primary) 12%, transparent);
  }
}

.action-btn--cancel {
  color: var(--el-text-color-regular);

  &:hover:not(.is-disabled) {
    color: var(--dd-err-ink);
    border-color: color-mix(
      in srgb,
      var(--el-color-danger) 40%,
      var(--el-border-color)
    );
    background: color-mix(in srgb, var(--el-color-danger) 6%, transparent);
  }
}

.action-btn--run {
  // 浅色成功底 + 墨色字（实色 --dd-ok 只给描边）
  --el-button-bg-color: color-mix(in srgb, var(--dd-ok) 12%, transparent);
  --el-button-border-color: color-mix(in srgb, var(--dd-ok) 35%, transparent);
  --el-button-hover-bg-color: color-mix(in srgb, var(--dd-ok) 20%, transparent);
  --el-button-hover-border-color: var(--dd-ok);
  --el-button-hover-text-color: var(--dd-ok-ink);
  --el-button-text-color: var(--dd-ok-ink);
}

.action-btn--task {
  --el-button-bg-color: color-mix(in srgb, var(--dd-accent) 10%, transparent);
  --el-button-border-color: color-mix(in srgb, var(--dd-accent) 28%, transparent);
  --el-button-hover-bg-color: color-mix(in srgb, var(--dd-accent) 16%, transparent);
  --el-button-hover-border-color: color-mix(in srgb, var(--dd-accent) 55%, transparent);
  --el-button-hover-text-color: var(--dd-accent-ink);
  --el-button-text-color: var(--dd-accent-ink);

  &.is-disabled {
    opacity: 0.72;
  }
}

/* ---------------- Editor body ---------------- */
.editor-body {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
  overflow: hidden;

  .code-editor {
    flex: 1;
    height: 100%;
    min-height: 0;
  }
}

.binary-card {
  margin: 24px;
  padding: 24px 28px;
  border: 1px dashed var(--el-border-color);
  // 二进制文件提示块是带外边距、独立成块的容器类表面 → surface 档
  border-radius: var(--dd-radius-surface);
  background: var(--el-fill-color-light);

  .binary-card-title {
    font-size: 14px;
    font-weight: 600;
    font-family: var(--dd-font-mono);
    letter-spacing: 0.4px;
    color: var(--el-text-color-primary);
    margin-bottom: 6px;
  }

  .binary-card-text {
    font-size: 13px;
    color: var(--el-text-color-secondary);
    margin: 0;
    line-height: 1.55;
  }
}

.image-preview {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
  gap: 10px;
  padding: 24px;

  .image-preview-stage {
    flex: 1;
    min-height: 0;
    position: relative;
  }

  // 绝对定位 + max 尺寸：等比缩小适应区域，小图保持原尺寸不放大
  .image-preview-img {
    position: absolute;
    inset: 0;
    margin: auto;
    max-width: 100%;
    max-height: 100%;
    cursor: zoom-in;
    border: 1px solid var(--el-border-color-lighter);
    // 透明图垫棋盘格底
    background-color: var(--el-bg-color);
    background-image:
      linear-gradient(45deg, var(--el-fill-color-dark) 25%, transparent 25%),
      linear-gradient(-45deg, var(--el-fill-color-dark) 25%, transparent 25%),
      linear-gradient(45deg, transparent 75%, var(--el-fill-color-dark) 75%),
      linear-gradient(-45deg, transparent 75%, var(--el-fill-color-dark) 75%);
    background-size: 16px 16px;
    background-position: 0 0, 0 8px, 8px -8px, -8px 0;
  }

  .image-preview-meta {
    flex-shrink: 0;
    text-align: center;
    font-size: 12px;
    font-family: var(--dd-font-mono);
    color: var(--el-text-color-secondary);
  }
}

/* ---------------- Status bar ---------------- */
.editor-statusbar {
  flex-shrink: 0;
  position: relative;
  display: flex;
  align-items: center;
  justify-content: space-between;
  /* 与组内 .status-item 之间的 14px 同值：「编辑中 / 只读」提到 footer 直属项之后，
     这条 gap 就是它与左边最后一枚标之间的实际间隙，留 8px 会比组内窄一截、看着像贴上去的。 */
  gap: 14px;
  padding: 6px 18px;
  /* 卡片内底部分区线 + 次级底色（明暗令牌自适配） */
  border-top: 1px solid var(--el-border-color-lighter);
  font-family: var(--dd-font-mono);
  font-size: 11px;
  color: var(--el-text-color-placeholder);
  background: var(--el-fill-color-light);
}

/* ⚠️ overflow: hidden + white-space: nowrap = 装不下就从右边缘静默裁掉。
   往组里加标之前先想清楚谁会被挤掉（原委见模板里那段注释）。 */
.status-group {
  display: inline-flex;
  align-items: center;
  gap: 14px;
  min-width: 0;
  overflow: hidden;
  white-space: nowrap;
}

/* 右组吃掉两组之间的剩余空间，并把自己的内容顶到右边缘。
   footer 上那条 justify-content: space-between 只摆得平两个子项；
   现在多了「编辑中 / 只读」这个直属项，光靠 space-between 会把右组推到正中间。
   让右组 flex-grow 之后 footer 已经没有自由空间可分，space-between 退化成无操作，
   三项就是「左组贴左 → 右组内容贴右 → 模式标收尾」。 */
.status-group--right {
  flex: 1 1 auto;
  justify-content: flex-end;
}

.status-item {
  letter-spacing: 0.4px;
  transition: color 0.16s ease, opacity 0.16s ease;
}

/* 「编辑中 / 只读」：唯一不参与裁切的读数，理由见模板里的注释。
   它是 footer 直属项，flex-shrink: 0 之后左边两组会先被压缩，它永远完整。
   nowrap 要在这里再写一遍：原来它是靠 .status-group 那条继承下来的，
   提出来之后没人给它了，而「编辑中」是三个能各自断行的中日韩字符。 */
.status-item--mode {
  flex-shrink: 0;
  white-space: nowrap;
}

/* 窄档收掉四枚镜像读数（Tab / Space / Map / Guide），取舍与上面 .wrap-btn-label 那条同源：
   .status-group 带 overflow: hidden，装不下不是换行也不是滚动，而是从右边缘静默裁掉。
   1280px 以下这一档 —— 桌面被 300px 目录树挤到约 450px、再往下整块换成移动布局 ——
   右组能拿到的宽度只有 200px 上下，而 v3.2.4 那一版的常驻内容需要约 331px。

   v3.2.4 只收 Map / Guide，理由是「Tab / Space 是循环档位、菜单外没有第二处读数」，
   但这两枚加起来才 54px 左右（Map 在 CodeMirror 下压根不渲染，手机恒为 CodeMirror），
   抵不掉 Tab / Space 新增的约 102px，于是溢出继续从右边缘吃进去，
   吃掉的正是当时排在最右的「编辑中 / 只读」。
   现在的取舍反过来：这四枚在齿轮菜单里都带一枚常驻状态标、点开就能读到，
   而「能不能改」在菜单里读不到，所以宁可四枚一起收。

   Tab / Space 刻意**不**改成「非默认档才渲染」：那样保留的恰好是用户改过的档位
   （如「Space 始终」约 61px、「Tab 4」约 33px），而 360px 这一档同样塞不下它们；
   而且档位是靠在菜单里连点循环切的，切一下状态条上的标就忽隐忽现，读起来更乱。

   写成一条 max-width 而不是只针对 1025~1280 那一档：否则 769~1024 会「更窄反而显示更多」。 */
@media screen and (max-width: 1280px) {
  .status-item--optional {
    display: none;
  }
}

.status-item--lang {
  color: var(--dd-accent-ink);
  font-weight: 600;
}

.status-item--accent {
  color: var(--dd-ok-ink);
  font-weight: 600;
}

/* ---------------- Mobile ---------------- */
.mobile-back {
  padding: 4px;
  margin-right: -2px;
}

.scripts-editor.mobile {
  .editor-hero {
    padding: 10px 12px;
    gap: 8px;
    align-items: flex-start;
    flex-wrap: wrap;

    .hero-file {
      gap: 8px;
      width: 100%;
      min-width: 0;
    }

    .file-icon {
      width: 30px;
      height: 30px;
      // 移动端只缩尺寸，圆角仍与桌面端同档
      border-radius: var(--dd-radius-control);
    }

    .file-title {
      font-size: 15px;
    }

    .hero-actions {
      width: 100%;
      justify-content: flex-end;
      // issue #144 / v3.3.2：刻意不再单独压 gap —— 清掉 EP 叠加的 12px margin 后同排间距已经均匀，
      // 直接继承 .hero-actions 的 8px，与全仓移动端 8px 刻度对齐
      flex-wrap: wrap;
    }
  }

  .editor-body {
    .code-editor {
      min-height: 0;
    }
  }

  .editor-statusbar {
    padding: 5px 12px;
    font-size: 10.5px;
    gap: 10px;
  }

  /* 360px 是最紧的一档：收掉四枚镜像读数之后右组仍有 UTF-8 / LF / 引擎 / Wrap 四项，
     按 10.5px 字号估算约 134px，而右组能拿到的只有 136px 上下 —— 擦着边过。
     14px 的组内间距在这个字号下本来就偏松，收到 10px 能再让出约 12px，
     把「擦边」变成有余量，同时避免左组的「12.3 KB」被连带压掉半截。 */
  .status-group {
    gap: 10px;
  }

  .empty-card {
    padding: 28px 20px;
  }
}


@keyframes dd-editor-shell-in {
  from {
    opacity: 0;
    transform: translate3d(0, 12px, 0);
  }
  to {
    opacity: 1;
    transform: translate3d(0, 0, 0);
  }
}

// 走上面 dd-page-reduced-motion 的包装（C8 动效偏好）
@include dd-page-reduced-motion {
  .scripts-editor {
    animation: none;
  }
}

</style>
