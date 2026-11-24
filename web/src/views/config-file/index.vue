<script setup lang="ts">
import { computed, onActivated, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { ElMessage } from 'element-plus'
import { Check, CopyDocument, Document, Refresh, Setting, Switch } from '@element-plus/icons-vue'
import { configScriptApi } from '@/api/system'
import CodeEditor from '@/components/CodeEditor.vue'
import { useResponsive } from '@/composables/useResponsive'
import { setUnsavedWork } from '@/utils/chunkReload'
import { copyText } from '@/utils/clipboard'
import {
  EDITOR_ENGINE_CHANGE_EVENT,
  persistEditorEngine,
  readStoredEditorEngine,
  resolveEditorEngine,
} from '@/utils/editorEngine'
import type { EditorEngine, ResolvedEditorEngine } from '@/utils/editorEngine'
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
} from '@/utils/editorPreferences'
import type { EditorPreferences } from '@/utils/editorPreferences'

const content = ref('')
const savedContent = ref('')
const configPath = ref('config.sh')
const loading = ref(false)
const saving = ref(false)
const copying = ref(false)

// 五项编辑器偏好（自动换行 / 缩略图 / 缩进参考线 / 空白符 / 缩进宽度）必须活在页面级 ref 里：
// 下面的 CodeEditor 挂在 v-if="!loading" 上，点一次「刷新」编辑器实例就整个重建，
// 状态若只活在编辑器内部会被一起丢掉。
// v3.2.4 起这份记忆不再只是 localStorage：utils/editorPreferences.ts 会把每次改动同步到
// /api/auth/preferences，换设备、换 IP 登进来还是这一份（issue #116）。
// 默认值全在那个模块里定，本页只管「读到什么用什么」，不复制一份默认值到这里。
const prefs = ref<EditorPreferences>(readEditorPreferences())

// 移动端工具栏（v3.3.1，issue #143 CF1）：Wrap / 刷新 / 复制三颗按钮只留图标，与齿轮、保存排成一行。
// 本页原来全靠 ≤768 媒体查询；这次要改的是「按钮文字渲不渲染」，CSS 做不到
// （把文字包成 span 再 display:none 会被 EP 的 `[class*=el-icon]+span{margin-left:6px}` 命中，图标偏心），
// 所以引入 isMobile，在模板里用 v-if 去掉文字节点。阈值与 ≤768 媒体查询同源（useResponsive 的 MOBILE_BREAKPOINT）。
const { isMobile } = useResponsive()

// Wrap 按钮的动作说明：桌面给悬停提示用，移动端只剩图标时再兼作读屏名称
const wordWrapActionLabel = computed(() => (prefs.value.word_wrap === 'on' ? '关闭自动换行' : '开启自动换行'))

function toggleWordWrap() {
  const next = prefs.value.word_wrap === 'on' ? 'off' : 'on'
  prefs.value.word_wrap = next
  setEditorPreference('word_wrap', next)
}

// 两个布尔开关按名字读写。空白符曾经也走这个通道（v3.2.2 时它是二态），
// 现在它是三档循环，语义变了，拆去下面单独一个函数。
function toggleViewOption(name: 'minimap' | 'indent_guides') {
  const next = !prefs.value[name]
  prefs.value[name] = next
  setEditorPreference(name, next)
}

// 缩进宽度与空白符是「点一下换下一档」的循环项，档位顺序与取下一档的算法都由
// utils/editorPreferences.ts 定，本页不复制一份（复制一份的表现是两页循环顺序不一致，而且构建全绿）。
function cycleIndentWidth() {
  const next = cycleEditorOption(EDITOR_INDENT_WIDTH_OPTIONS, prefs.value.indent_width)
  prefs.value.indent_width = next
  setEditorPreference('indent_width', next)
}

function cycleWhitespace() {
  const next = cycleEditorOption(EDITOR_WHITESPACE_MODES, prefs.value.whitespace)
  prefs.value.whitespace = next
  setEditorPreference('whitespace', next)
}

// 「在脚本页改了一项」「刚从服务端拉回来了一份」都靠这个事件同步到本页。
// 刻意从存储重读、而不是从事件里取值：偏好可能是在另一个组件里改的，存储才是唯一真源
// （范式对齐 CodeEditor.vue 里的 syncEngine）。
function syncPreferences() {
  prefs.value = readEditorPreferences()
}

// 引擎偏好（auto / codemirror / monaco）：同样是「两页共享」，
// 也同样必须活在页面级 ref —— 编辑器挂在 v-if="!loading" 上，点一次刷新就整个重建。
// 但它**不是**视图选项那一档，菜单里用 divided 分开：
// 上面四项是「同一个编辑器怎么显示」，改一下只是重配一个 Compartment；
// 引擎是「换一个编辑器」，切一次要把实例整块拆掉重建，撤销历史、光标、滚动位置全丢
// （正文本身不丢，它活在本页的 content 里，重建后会重新灌进去）。
const editorEngine = ref(readStoredEditorEngine())

// 只做展示：告诉用户「自动」这一档在这台设备上落到哪个引擎，
// 否则选了 auto 的人完全看不出自己在用什么。
// 刻意不跟随窗口尺寸变化（resolveEditorEngine 内部读的是 window.innerWidth 快照）：
// auto 本来就只在编辑器挂载时解析一次，标签跟着拖窗口跳反而与实际用的引擎对不上。
const autoEngineLabel = computed(() =>
  resolveEditorEngine('auto') === 'monaco' ? 'Monaco' : 'CodeMirror',
)

// 解析后的实际引擎。本页原来只有上面那个 autoEngineLabel（只服务于「自动」那一档的括号说明），
// 没有「现在实际跑的是哪个」的读数 —— 脚本页有（它底部状态条要用），本页没有状态条，两页天生不对称。
// v3.2.4 需要它来决定齿轮菜单里「代码缩略图」渲不渲染：那一项只有 Monaco 认
// （CodeMirror 侧的缩略图这一版被删掉了），CodeMirror 下留着它就是一个点了没反应的开关。
//
// 🔴 这里**不能**写成 computed(() => resolveEditorEngine(editorEngine.value))。
// resolveEditorEngine 在 auto 档下读的是 matchMedia('(pointer: coarse)') 和 window.innerWidth，
// 两者都不是响应式依赖，那个 computed 只会在 editorEngine 变时重算一次；
// 而真正决定「现在跑的是哪个引擎」的 CodeEditor 是**每次挂载时按当下窗口宽度重新解析**的，
// 本页的编辑器又挂在 v-if="!loading" 上 —— 点一次「刷新」就整块重建。
// 于是「宽窗口打开页面（菜单里有代码缩略图）→ 把窗口贴靠成半屏 → 点刷新」之后，
// 编辑器已经重建成 CodeMirror，菜单里「代码缩略图」还在，点它开关翻 ON、正文毫无变化。
// 所以真源改成 CodeEditor 每次解析完 emit 上来的 engine-resolved（挂载时 + 引擎偏好变更后各一次）。
const resolvedEngine = ref<ResolvedEditorEngine>(
  // 初值只是兜底：编辑器还没挂载（loading 期间）时，齿轮菜单也要能渲染。
  // 编辑器一挂上来，第一次 engine-resolved 就会把它覆盖成真值。
  resolveEditorEngine(readStoredEditorEngine()),
)

function onEngineResolved(engine: ResolvedEditorEngine) {
  resolvedEngine.value = engine
}

function selectEditorEngine(next: EditorEngine) {
  if (editorEngine.value === next) return
  editorEngine.value = next
  // 顺手把 resolvedEngine 也推一次：编辑器没挂载（loading 期间）时不会有 engine-resolved 回来，
  // 少了这一行菜单里的「代码缩略图」会停在旧引擎上。编辑器挂着的话，下一拍 CodeEditor 会用
  // 同一份存储值 + 同一个窗口再解析一遍并 emit，结论必然相同，两者不会打架。
  // ⚠️ 只在这里补，不要在 onActivated 里也补一次：那时窗口宽度可能已经和编辑器挂载时不同，
  // 重解析出来的值会和实际跑着的实例对不上，正是上面注释里那个 bug 的翻版。
  resolvedEngine.value = resolveEditorEngine(next)
  persistEditorEngine(next) // 内部会派发 EDITOR_ENGINE_CHANGE_EVENT，已挂载的编辑器跟着换
}

// 本页被 keep-alive 缓存，第二次进来只触发 onActivated 不触发 onMounted。
// 下面 onMounted 里挂的变更事件监听在缓存期间是不摘的（keep-alive 只 deactivate 不 unmount），
// 所以「切走时在脚本页改了偏好」其实已经同步过了；这里仍然同步补读一次，
// 兜的是「事件没派发」那条路径 —— 比如另一个标签页改的偏好、或者存储被外部清掉。
onActivated(() => {
  prefs.value = readEditorPreferences()
  // 引擎偏好没有走服务端同步，全靠这一行两页共享。漏掉它的表现是
  // 「在脚本页切了引擎、切回本页菜单里还是旧的」——编辑器实例其实已经被
  // EDITOR_ENGINE_CHANGE_EVENT 换掉了，只有菜单勾选在骗人，比单纯不生效更难查。
  editorEngine.value = readStoredEditorEngine()
  void ensureEditorPreferencesLoaded()
})

// 引擎偏好不只在本页齿轮菜单里改：编辑器加载失败时的「改用 CodeMirror」（utils/monacoWarmup.ts）、
// 脚本页的同一个菜单，都会走 persistEditorEngine 派发 EDITOR_ENGINE_CHANGE_EVENT。
// 编辑器靠 engine-resolved 跟得上，菜单里的「当前」只能靠这个监听跟：
// 不监听就要等下次进页面（onActivated）才更新，期间勾着的是已经不在用的那个引擎。
// 从存储重读而不是沿用本地值：存储写不进去时编辑器实际按 auto 解析，菜单也该显示 auto。
function syncEditorEngine() {
  editorEngine.value = readStoredEditorEngine()
}

const hasChanged = computed(() => content.value !== savedContent.value)
const lineCount = computed(() => content.value === '' ? 0 : content.value.split(/\r\n|\n|\r/).length)
const byteSizeLabel = computed(() => {
  const bytes = new Blob([content.value]).size
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`
  return `${(bytes / 1024 / 1024).toFixed(1)} MB`
})

// 有未保存修改时登记到 utils/chunkReload.ts：面板升级后的自动刷新改为提示「保存后刷新」，
// 关标签页 / 按 F5 也会先弹离开确认（没改动时不拦）。
// 直接复用标题行「有未保存修改 / 已保存」那枚标签的 hasChanged，不另算一套：
// 保存成功、点「刷新」重新读取文件、读取失败都会让它回落。
// 被 keep-alive 缓存、切到别的页面时不撤：没保存的内容仍在内存里，整页刷新一样会丢，只在卸载时撤。
const UNSAVED_WORK_KEY = 'config-file'

watch(
  hasChanged,
  (dirty) => {
    setUnsavedWork(UNSAVED_WORK_KEY, dirty)
  },
  { immediate: true },
)

onMounted(() => {
  void loadConfigScript()
  window.addEventListener(EDITOR_PREFERENCES_CHANGE_EVENT, syncPreferences)
  window.addEventListener(EDITOR_ENGINE_CHANGE_EVENT, syncEditorEngine)
  // 服务端那份偏好在这里拉一次（函数自己记忆化，重复调用不会重复发请求），
  // 拉回来之后它会派发变更事件，由上面的监听把新值刷进来。
  // 刻意不放进 main.ts 的启动流程：编辑器不在首屏，没必要给每一次打开面板都多加一个请求。
  void ensureEditorPreferencesLoaded()
})

onBeforeUnmount(() => {
  window.removeEventListener(EDITOR_PREFERENCES_CHANGE_EVENT, syncPreferences)
  window.removeEventListener(EDITOR_ENGINE_CHANGE_EVENT, syncEditorEngine)
  setUnsavedWork(UNSAVED_WORK_KEY, false)
})

async function loadConfigScript(showSuccess = false) {
  loading.value = true
  try {
    const res = await configScriptApi.get()
    content.value = res.content ?? ''
    savedContent.value = res.content ?? ''
    configPath.value = res.path || 'config.sh'
    if (showSuccess) {
      ElMessage.success('配置文件已刷新')
    }
  } catch {
    content.value = ''
    savedContent.value = ''
    ElMessage.error('加载配置文件失败')
  } finally {
    loading.value = false
  }
}

async function saveConfigScript() {
  saving.value = true
  try {
    await configScriptApi.save(content.value)
    savedContent.value = content.value
    ElMessage.success('配置文件已保存')
  } catch {
    ElMessage.error('保存配置文件失败')
  } finally {
    saving.value = false
  }
}

async function copyConfigScript() {
  copying.value = true
  try {
    await copyText(content.value)
    ElMessage.success('配置文件内容已复制')
  } catch {
    ElMessage.error('复制失败，请检查浏览器权限或站点访问方式')
  } finally {
    copying.value = false
  }
}
</script>

<template>
  <div class="config-file-page dd-scroll-page dd-page-hide-heading">
    <div class="page-header">
      <div>
        <h2 class="page-title-with-icon">
          <el-icon><Document /></el-icon>
          <span>配置文件</span>
        </h2>
        <p class="page-subtitle">
          集中维护 <code>config.sh</code>，脚本运行前会自动加载这里的共享配置。
        </p>
      </div>
    </div>

    <div class="config-layout">
      <el-card class="editor-card dd-rise" style="--dd-i: 0" shadow="never" v-loading="loading">
        <template #header>
          <div class="editor-card__header">
            <div class="editor-card__intro">
              <div class="editor-card__title-row">
                <span class="editor-card__title">{{ configPath }}</span>
                <!--
                  保存状态是本页唯一的状态叙事，原来放在 .page-header 里，
                  而 .dd-page-hide-heading 把整个页头 display:none 了 —— 实际永远看不见。
                  这里挪到工具栏标题行；out-in 淡切的 key 必须绑「状态值」，
                  绑其它稳定值（如文件路径）永远不会触发过渡。
                -->
                <Transition name="dd-status-switch" mode="out-in">
                  <el-tag
                    :key="hasChanged ? 'changed' : 'saved'"
                    class="editor-card__status"
                    :type="hasChanged ? 'warning' : 'success'"
                    effect="plain"
                    size="small"
                  >
                    <el-icon v-if="!hasChanged"><Check /></el-icon>
                    {{ hasChanged ? '有未保存修改' : '已保存' }}
                  </el-tag>
                </Transition>
              </div>
              <div class="editor-card__desc">按 Shell 语法编辑，每行一个变量或注释。</div>
            </div>
            <div class="editor-card__actions">
              <!-- 状态类按钮排在动作类按钮之前。配色沿用本仓工具栏切换按钮的既有写法
                   （tasks 页快捷排序：开启时 primary + plain），不另造一套语汇；
                   与「保存」的实心 primary 靠 plain 区分，不会抢主操作的注意力。
                   移动端（issue #143 CF1）Wrap / 刷新 / 复制三颗只留图标：图标仍放默认插槽，文字包进
                   <template v-if="!isMobile">，桌面仍是原来的裸文本节点、间距不变；移动端挂 dd-icon-only-btn 成 32×32。
                   aria-label 只在移动端挂：桌面按钮有可见文字，读屏直接念文字，再挂一个与可见文字不同的
                   aria-label（如 Wrap 按钮的「开启自动换行」）会违反「可见标签须包含在可访问名称里」。 -->
              <el-tooltip :content="wordWrapActionLabel" placement="bottom">
                <el-button
                  :class="{ 'dd-icon-only-btn': isMobile }"
                  :type="prefs.word_wrap === 'on' ? 'primary' : 'default'"
                  :plain="prefs.word_wrap === 'on'"
                  :aria-label="isMobile ? wordWrapActionLabel : undefined"
                  @click="toggleWordWrap"
                >
                  <el-icon><Switch /></el-icon>
                  <template v-if="!isMobile">Wrap</template>
                </el-button>
              </el-tooltip>
              <!-- 四个视图选项（缩略图 / 缩进参考线 / 缩进宽度 / 空白符）收进齿轮下拉，与脚本页同一套交互。
                   本页工具栏在桌面端虽然能换行（.editor-card__actions 带 flex-wrap，不像脚本页会被裁掉），
                   但四个选项并排就是四个按钮，窄屏必然把这一行折成好几行（移动端这一行更是固定单行，见 ≤768 媒体查询）；
                   而且两页的这组选项是同一份记忆，入口长得不一样只会让人以为是两套东西。
                   :hide-on-click="false"：这几项经常连着切（缩进宽度和空白符还要连点好几下
                   才能转到想要的档），点一下就收菜单会逼用户反复重开。 -->
              <el-dropdown trigger="click" placement="bottom-end" :hide-on-click="false">
                <el-button :class="{ 'dd-icon-only-btn': isMobile }" aria-label="编辑器选项">
                  <el-icon><Setting /></el-icon>
                </el-button>
                <template #dropdown>
                  <el-dropdown-menu>
                    <!-- 「代码缩略图」只在解析后的引擎是 Monaco 时渲染。
                         v3.2.4 删掉了 CodeMirror 侧的缩略图实现（它硬占正文右侧 64px，
                         手机 360px 宽下就是 18% 的正文没了；而桌面 auto 档本来就走 Monaco），
                         所以在 CodeMirror 下这一项已经没有任何东西可开。
                         宁可整项不渲染也不留一个「点了没反应、按钮还高亮、构建全绿」的开关。 -->
                    <el-dropdown-item v-if="resolvedEngine === 'monaco'" @click="toggleViewOption('minimap')">
                      <span class="opt-row">
                        <span>代码缩略图</span>
                        <span class="opt-state" :class="{ 'is-on': prefs.minimap }">{{ prefs.minimap ? 'ON' : 'OFF' }}</span>
                      </span>
                    </el-dropdown-item>
                    <el-dropdown-item @click="toggleViewOption('indent_guides')">
                      <span class="opt-row">
                        <span>缩进参考线</span>
                        <span class="opt-state" :class="{ 'is-on': prefs.indent_guides }">{{ prefs.indent_guides ? 'ON' : 'OFF' }}</span>
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
                        <span class="opt-label">缩进宽度<span class="opt-hint">点击切换</span></span>
                        <span class="opt-state is-on">{{ editorIndentWidthLabel(prefs.indent_width) }}</span>
                      </span>
                    </el-dropdown-item>
                    <el-dropdown-item @click="cycleWhitespace">
                      <span class="opt-row">
                        <span class="opt-label">显示空白符<span class="opt-hint">点击切换</span></span>
                        <span class="opt-state" :class="{ 'is-on': prefs.whitespace !== 'none' }">{{ editorWhitespaceLabel(prefs.whitespace) }}</span>
                      </span>
                    </el-dropdown-item>

                    <!-- 引擎切换：用 divided 单独起一组，不和上面四项混排。
                         上面四项是「同一个编辑器怎么显示」，切一下只是重配一个 Compartment，代价为零；
                         引擎是「换一个编辑器」，切一次整块重建实例，撤销历史 / 光标 / 滚动位置全丢
                         （正文本身不丢，它活在本页的 content 里）。两者不是同一档，
                         并排放会让人以为引擎也是个随手可以来回拨的显示开关。

                         做成三项单选而不是一个「Monaco ON/OFF」开关：auto 既是默认值也是推荐值，
                         二值开关表达不了「跟着设备走」这个第三态，而这第三态正是
                         「触摸设备永远不给 Monaco」这条硬约束的落点 —— Monaco 是自绘编辑器，
                         长按系统菜单 / 选择手柄 / 拖动光标三条做不到，这也是当初换引擎的全部理由。

                         状态标写「当前」而不是沿用 ON/OFF：ON/OFF 是布尔读数，
                         放进三选一里会让人以为三个引擎能各自开关、甚至同时开着。
                         未选中的项不出标，三项里只有一枚标，「哪个在生效」一眼可读。

                         没有为这组新加按钮或另开一个「编辑器设置」入口：本页工具栏虽然能换行，
                         但脚本页的 .hero-actions 装不下就会被静默裁掉，两页入口必须长得一样；
                         何况同一页出现两个编辑器设置入口本身就是坏设计。 -->
                    <el-dropdown-item divided @click="selectEditorEngine('auto')">
                      <span class="opt-row">
                        <span>引擎：自动（{{ autoEngineLabel }}）</span>
                        <span v-if="editorEngine === 'auto'" class="opt-state is-on">当前</span>
                      </span>
                    </el-dropdown-item>
                    <el-dropdown-item @click="selectEditorEngine('codemirror')">
                      <span class="opt-row">
                        <span>引擎：CodeMirror</span>
                        <span v-if="editorEngine === 'codemirror'" class="opt-state is-on">当前</span>
                      </span>
                    </el-dropdown-item>
                    <el-dropdown-item @click="selectEditorEngine('monaco')">
                      <span class="opt-row">
                        <span>引擎：Monaco</span>
                        <span v-if="editorEngine === 'monaco'" class="opt-state is-on">当前</span>
                      </span>
                    </el-dropdown-item>
                  </el-dropdown-menu>
                </template>
              </el-dropdown>
              <el-button
                :class="{ 'dd-icon-only-btn': isMobile }"
                :loading="loading"
                :aria-label="isMobile ? '刷新' : undefined"
                :title="isMobile ? '刷新' : undefined"
                @click="loadConfigScript(true)"
              >
                <el-icon><Refresh /></el-icon>
                <template v-if="!isMobile">刷新</template>
              </el-button>
              <el-button
                :class="{ 'dd-icon-only-btn': isMobile }"
                :loading="copying"
                :aria-label="isMobile ? '复制' : undefined"
                :title="isMobile ? '复制' : undefined"
                @click="copyConfigScript"
              >
                <el-icon><CopyDocument /></el-icon>
                <template v-if="!isMobile">复制</template>
              </el-button>
              <el-button
                class="editor-card__save"
                type="primary"
                :loading="saving"
                :disabled="loading || !hasChanged"
                @click="saveConfigScript"
              >
                保存
              </el-button>
            </div>
          </div>
        </template>

        <!-- 等接口返回后再挂载编辑器，避免先闪一下空内容。 -->
        <!-- fill-height：桌面双栏下由 .config-layout → .editor-card → .el-card__body 的 flex 链撑满剩余高度；
             窄屏/移动端父级没有确定高度，改由本页 media query 给 560px 下限，行为与改造前一致。 -->
        <CodeEditor
          v-if="!loading"
          v-model="content"
          language="shell"
          :fill-height="true"
          :word-wrap="prefs.word_wrap"
          :minimap="prefs.minimap"
          :indent-guides="prefs.indent_guides"
          :whitespace="prefs.whitespace"
          :indent-width="prefs.indent_width"
          @engine-resolved="onEngineResolved"
        />
        <div v-else class="editor-placeholder">
          正在读取配置文件...
        </div>
      </el-card>

      <aside class="side-panel">
        <el-card class="info-card dd-spot dd-rise" style="--dd-i: 1" shadow="never">
          <template #header>
            <span>文件说明</span>
          </template>
          <div class="info-list">
            <div class="info-row">
              <span>文件名</span>
              <code>{{ configPath }}</code>
            </div>
            <div class="info-row">
              <span>当前行数</span>
              <strong>{{ lineCount }}</strong>
            </div>
            <div class="info-row">
              <span>内容大小</span>
              <strong>{{ byteSizeLabel }}</strong>
            </div>
          </div>
        </el-card>

        <el-card class="tips-card dd-spot dd-rise" style="--dd-i: 2" shadow="never">
          <template #header>
            <span>写法提示</span>
          </template>
          <ul class="tips-list">
            <li><code>KEY=VALUE</code>：写入普通变量。</li>
            <li><code>export KEY="VALUE"</code>：写入并导出变量。</li>
            <li><code>#</code> 开头表示注释，可记录用途。</li>
            <li>环境变量页面里的同名变量优先级更高。</li>
          </ul>
        </el-card>
      </aside>
    </div>
  </div>
</template>

<style scoped lang="scss">
.config-file-page {
  padding: 0;
}

.page-header {
  display: flex;
  justify-content: space-between;
  align-items: flex-start;
  gap: 16px;
  margin-bottom: 18px;

  .page-title-with-icon {
    display: flex;
    align-items: center;
    gap: 8px;
    margin: 0;
    font-size: 22px;
    font-weight: 700;
    color: var(--el-text-color-primary);
    line-height: 1.3;
  }

  .page-subtitle {
    margin: 6px 0 0;
    font-size: 13px;
    color: var(--el-text-color-secondary);
  }
}

.config-layout {
  display: grid;
  grid-template-columns: minmax(0, 1fr) 300px;
  gap: 16px;
  align-items: start;
}

// 卡片：层次交给 1px 边框，不再用阴影浮起
.editor-card,
.info-card,
.tips-card {
  // 三张都是卡片级表面 → 「卡片」角色令牌：桌面就是 surface 档（与改动前一致），
  // ≤768 的 rounded 面板为 20px，与其它页的移动端卡片同一刻度（v3.3.1，issue #143）。
  // 这里写死 surface 的话会顶掉 global.scss 给 .el-card 派的角色令牌，手机上只有本页卡片圆角对不上。
  // （.editor-card 还带 overflow: hidden，内部贴边的 header 与编辑器会被裁成同样的角）
  border-radius: var(--dd-radius-card);
  border: 1px solid var(--el-border-color-lighter);
}

.editor-card {
  overflow: hidden;

  :deep(.el-card__header) {
    padding: 16px 18px;
  }

  :deep(.el-card__body) {
    padding: 0;
  }
}

.editor-card__header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 12px;
  flex-wrap: wrap;
}

.editor-card__intro {
  min-width: 0;
}

.editor-card__title-row {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}

// 卡片标题字号 / 字重读排版级令牌（素白等主题会覆盖），不按主题名分支
.editor-card__title {
  font-size: var(--dd-card-title-size);
  font-weight: var(--dd-card-title-weight);
  color: var(--el-text-color-primary);
}

// 标签不参与压缩：窄屏由 .editor-card__title-row 的 flex-wrap 把它整体换行，
// 而不是把「有未保存修改」挤成半截
.editor-card__status {
  flex-shrink: 0;
}

// 「有未保存修改 ↔ 已保存」是本页唯一的状态叙事，切换时做一次极短的 out-in 淡切，
// 避免文案硬跳导致用户看不出状态变过。
// 只动 opacity：标签宽度在两个状态下不同，位移/缩放会让整行标题跟着晃。
.dd-status-switch-enter-active,
.dd-status-switch-leave-active {
  transition: opacity var(--dd-motion-fast) var(--dd-ease-standard);
}

.dd-status-switch-enter-from,
.dd-status-switch-leave-to {
  opacity: 0;
}

.editor-card__desc {
  margin-top: 4px;
  font-size: 12px;
  color: var(--el-text-color-secondary);
}

.editor-card__actions {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}

// 齿轮下拉里的视图选项：左侧标题、右侧一枚状态标。
// 下拉菜单项没有 EP 现成的选中态可用，只画一个对勾的话「没对勾」既可能是关、
// 也可能是没渲染上，读不出确定状态，所以所有状态都出字。
// 这几条与脚本页 ScriptsEditorPane.vue 里的同名规则是同一副长相：
// 两边都是 scoped 样式、隔着组件边界够不到对方，只能各写一份，要改就两边一起改。
.opt-row {
  display: flex;
  flex: 1;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  width: 100%;
}

// 循环档位那两项（缩进宽度 / 显示空白符）的标题：正文后面跟一句常驻的「点击切换」。
// 两项都是「点一下换下一档」，而右侧那枚标只写当前档位、看不出还能点，
// 没有这句提示的话很容易被当成一个坏掉的只读读数。
// 写成同一行的次要文字而不是第二行副标题：菜单项是固定行高的单行控件，
// 撑成两行会让这两项在四项里高出一截，反而像是别的什么东西。
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
  // 菜单项是方角时里面嵌一颗胶囊会看出错位。它也不是状态灯（那类才走 pill 档），
  // 只是把开关状态写成字的静态读数。
  border-radius: var(--dd-radius-control);
  font-family: var(--dd-font-mono);
  font-size: 10px;
  font-weight: 600;
  letter-spacing: 0.4px;
  // 关闭态用 regular 而不是更淡的 placeholder：这枚标是开关状态的唯一读数，
  // 10px 的小字再配 placeholder（明色下约 2.5:1）就没法读了，等于白做一个状态标。
  color: var(--el-text-color-regular);
  background: var(--el-fill-color);
  transition: background-color var(--dd-motion-fast) var(--dd-ease-standard),
    color var(--dd-motion-fast) var(--dd-ease-standard);

  &.is-on {
    color: var(--dd-accent-ink);
    background: color-mix(in srgb, var(--el-color-primary) 12%, transparent);
  }
}

.editor-placeholder {
  min-height: 560px;
  display: flex;
  align-items: center;
  justify-content: center;
  color: var(--el-text-color-secondary);
  background: var(--el-fill-color-lighter);
  font-size: 14px;
}

.side-panel {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.info-card,
.tips-card {
  :deep(.el-card__header) {
    padding: 14px 16px;
    font-size: var(--dd-card-title-size);
    font-weight: var(--dd-card-title-weight);
  }

  :deep(.el-card__body) {
    padding: 16px;
  }
}

.info-list {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.info-row {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 12px;
  font-size: 13px;
  color: var(--el-text-color-secondary);

  code,
  strong {
    color: var(--el-text-color-primary);
    font-weight: 600;
  }
}

.tips-list {
  margin: 0;
  padding-left: 18px;
  display: flex;
  flex-direction: column;
  gap: 10px;
  color: var(--el-text-color-regular);
  font-size: 13px;
  line-height: 1.6;
}

code {
  padding: 1px 5px;
  // 行内代码底是文字里的小色块（不是块级代码面板）→ 归控件类 control 档
  border-radius: var(--dd-radius-control);
  background: var(--el-fill-color-lighter);
  color: var(--el-text-color-primary);
  font-family: var(--dd-font-mono);
  font-size: 12px;
}

// ===== 桌面双栏：整页固定高度，编辑器吃掉剩余高度 =====
// 只在双栏断点以上启用。1080px 以下是单栏（说明栏在编辑器下方），
// 固定高度反而会让编辑器和说明栏抢同一份垂直空间，保持原来的滚动布局更合适。
@media screen and (min-width: 1081px) {
  .config-file-page {
    height: 100%;
    min-height: 0;
    display: flex;
    flex-direction: column;
    // 覆盖 .dd-scroll-page 的 overflow: auto，滚动下沉到 .side-panel 内部
    overflow: hidden;
  }

  .page-header {
    flex-shrink: 0;
  }

  .config-layout {
    flex: 1 1 0;
    min-height: 0;
    // 单行 auto 轨道 + align-content 默认 stretch，行高会拉满容器
    align-items: stretch;
  }

  .editor-card {
    min-height: 0;
    display: flex;
    flex-direction: column;

    :deep(.el-card__header) {
      flex-shrink: 0;
    }

    :deep(.el-card__body) {
      flex: 1 1 auto;
      min-height: 0;
      display: flex;
      flex-direction: column;
      overflow: hidden;
    }
  }

  .editor-placeholder {
    flex: 1 1 auto;
    min-height: 0;
  }

  // 说明栏内容很短，保持自然高度顶部对齐，不跟着拉成长条
  .side-panel {
    align-self: start;
    max-height: 100%;
    overflow: auto;
  }
}

@media (max-width: 1080px) {
  .config-layout {
    grid-template-columns: 1fr;
  }

  .side-panel {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }

  // 单栏/移动端父级高度不确定，fill-height 拿不到可撑满的高度，
  // 这里给回改造前的 560px 下限（选择器带 .editor-card，特异性高于组件内的默认规则）
  .editor-card :deep(.code-editor-wrapper) {
    min-height: 560px;
  }
}

@media (max-width: 768px) {
  .page-header {
    flex-direction: column;
  }

  .editor-card__header {
    align-items: stretch;
  }

  // 工具栏这一排五个控件在移动端排成**一行、不折行**（v3.3.1，issue #143 CF1）：
  // 「保存」用 order:-1 挪到行首并吃掉剩余宽度（它是主操作，点击面积给最大），
  // 后面依次是 Wrap / 齿轮 / 刷新 / 复制四颗 32×32 纯图标按钮
  // （dd-icon-only-btn，文字在模板里按 isMobile 去掉；齿轮的 flex item 是外面那层 div.el-dropdown，
  // 它按里面 32px 的按钮取宽，不会被压缩）。
  //
  // 宽度核算（按最窄的 320px 视口）：可用宽 ≈ 320 − .layout-main 左右各 12 − 卡片边框各 1 − 卡片头左右各 18 = 258px；
  // 四颗图标按钮 4×32 加四个 8px 间距共 160px，「保存」还剩约 98px，高于它约 58px 的最小内容宽，一行放得下。
  // 四个视图选项之所以全塞进齿轮的下拉、而不是并排加四个按钮，也是为了这一行放得下。
  //
  // order 只改视觉顺序，Tab 焦点顺序仍是 Wrap → 齿轮 → 刷新 → 复制 → 保存。
  // 刷新 / 复制带 :loading，加载中 EP 会在图标前再插一个转圈图标，这颗 32px 按钮会短暂变成两个图标，
  // 刷新期间整张卡本来就有 v-loading 遮罩、复制又很快，接受这一瞬。
  .editor-card__actions {
    width: 100%;
    flex-wrap: nowrap;

    // 间距只由 gap 决定：EP 的 `.el-button + .el-button { margin-left: 12px }` 按 DOM 相邻命中「复制」「保存」，
    // 「保存」被 order 挪到行首后仍带着这 12px，就和卡片左缘对不齐了
    .el-button + .el-button {
      margin-left: 0;
    }
  }

  .editor-card__save {
    order: -1;
    flex: 1 1 auto;
  }

  .side-panel {
    grid-template-columns: 1fr;
  }
}

// ===== 入场动画 =====
// 与全站 dd-*-rise-in 同一语汇：只对卡片级容器（编辑器卡 / 说明卡 / 提示卡）做淡入上移，
// ≤60ms 轻微错落；时长与缓动全部走令牌，prefers-reduced-motion 下令牌降为 1ms、
// delay 被全局补丁归零，等效关闭。
//
// fill-mode 用 backwards 而不是列表页惯用的 both：
// 编辑器卡里装着代码编辑器，both 会把 `transform: translateY(0)` 永久留在 .editor-card 上，
// 让它成为编辑器内部 position: fixed 浮层（搜索面板 / 补全 / 悬浮提示）的包含块 → 弹层错位，
// 与 ScriptExecutionDialogs.vue 里那条「不加 both」的注释是同一个坑。
// backwards 只在 delay 期间铺 from 帧，动画结束后回到自然样式（opacity 1、无 transform），
// 末态与 to 帧完全一致，视觉上没有区别。
// 只动 opacity + translateY、不碰 height：容器高度全程稳定，编辑器的首次布局不受影响。
// v4.0 起改用全局 .dd-rise（同样是 backwards 填充，满足上面的约束）：模板里三张卡挂类，
// 内联 --dd-i 控制错落（编辑器卡 0、说明卡 1、提示卡 2）。
</style>
