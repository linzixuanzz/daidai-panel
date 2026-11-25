<script setup lang="ts">
import {
  ref,
  onMounted,
  onBeforeUnmount,
  onActivated,
  computed,
  watch,
  nextTick,
} from "vue";
// 纯图标按钮走 el-button 的 :icon prop，要的是组件对象而不是全局注册名，所以这几枚在本页局部引入
import { Check, Close, Delete, Plus, Setting } from "@element-plus/icons-vue";
import { subscriptionApi } from "@/api/subscription";
import { sshKeyApi } from "@/api/notification";
import { configApi } from "@/api/system";
import { ElMessage, ElMessageBox } from "element-plus";
import {
  openAuthorizedEventStream,
  type EventStreamConnection,
} from "@/utils/sse";
import { useResponsive } from "@/composables/useResponsive";
import { useLogAutoFollow } from "@/composables/useLogAutoFollow";
import { useAuthStore } from "@/stores/auth";
import { useBadgesStore } from "@/stores/badges";
import { canAdminister } from "@/utils/roles";
import { ansiToHtml, normalizeAnsi } from "@/utils/ansi";
import { formatDuration } from "@/utils/duration";
import { formatDateTime } from "@/utils/datetime";
import DdSplitButton from "@/components/ui/DdSplitButton.vue";
import type { SplitButtonItem } from "@/components/ui/DdSplitButton.vue";
import DdMoreMenu from "@/components/ui/DdMoreMenu.vue";
import DdFieldHelp from "@/components/ui/DdFieldHelp.vue";
import { scrollListToTop } from "@/utils/scrollToTop";

type SubTypeFilter = "" | "git-repo" | "single-file" | "disabled";

const subList = ref<any[]>([]);
const loading = ref(false);
const total = ref(0);
const page = ref(1);
const pageSize = ref(20);
const keyword = ref("");
const selectedIds = ref<number[]>([]);
const selectedIdSet = computed(() => new Set(selectedIds.value));
const { isMobile, dialogFullscreen } = useResponsive();
const typeFilter = ref<SubTypeFilter>("");
// 页面根：翻页回顶（scrollListToTop）以它为锚点往上找真正在滚的容器
const pageRootRef = ref<HTMLElement | null>(null);

// 类型分段控件的四项。桌面工具栏与移动端第二行各渲染一份，共用这张表，免得两处文案走样。
const typeTabs: { value: SubTypeFilter; label: string }[] = [
  { value: "", label: "全部" },
  { value: "git-repo", label: "仓库" },
  { value: "single-file", label: "单文件" },
  { value: "disabled", label: "已禁用" },
];

const authStore = useAuthStore();
const badgesStore = useBadgesStore();
// 本页路由的 minRole 是 operator，而 GET /configs 是 JWTAuth() + RequireAdmin() 的管理员接口。
// 不按角色 gate 的话，每个 operator 每次进这个页面都会打一次注定 403 的请求
// （被 catch 静默吞掉、不弹错，但白白多一次调用）。
const isAdmin = computed(() => canAdminister(authStore.user?.role));

const filteredSubList = computed(() => {
  if (!typeFilter.value) return subList.value;
  if (typeFilter.value === "disabled")
    return subList.value.filter((s) => !s.enabled);
  return subList.value.filter((s) => s.type === typeFilter.value);
});

// 移动端批量栏「全选 / 取消全选」的判定，范围只到当前页可见的卡片
const allSelectedOnPage = computed(
  () =>
    filteredSubList.value.length > 0 &&
    filteredSubList.value.every((row) => selectedIdSet.value.has(row.id)),
);

// 跨断点时清空勾选：桌面 el-table 的内部选择并不认 selectedIds。
// 从移动端切到桌面时表格一行都没勾，工具栏却还挂着「批量删除」，删的是看不见的行；
// 从桌面切到移动端时 el-table 卸载不会回调 selection-change，旧勾选会让移动端一进来就处在批量态。
// 所以两个方向都清空。
watch(isMobile, () => {
  selectedIds.value = [];
});

// 表头只引用语义令牌，明暗两套都成立；取值与 global.scss 里 .el-table th 的规则同口径。
// 原先写死的 #f8fafc / #64748b 在暗色下全靠 html.dark 那条 !important 补丁盖掉，明色下字色还压着全局规则。
const subTableHeaderStyle = {
  background: "var(--el-fill-color-light)",
  color: "var(--el-text-color-regular)",
  fontWeight: 600,
  fontSize: "13px",
};

const showEditDialog = ref(false);
const showLogDialog = ref(false);
const showSettingsDialog = ref(false);
const isCreate = ref(true);
// 订阅保存的在途锁：从「镜像加速」确认框开始就锁住按钮，避免连点重复创建订阅
const editSaving = ref(false);
const qlCommand = ref("");

const settingsLoading = ref(false);
const settingsSaving = ref(false);
// 三个全局默认值的**只读展示值**，只供订阅编辑弹窗里那几句「（当前：X）」使用：
//   覆盖拉取     subscription_force_overwrite
//   自动建任务   auto_add_cron
//   自动删任务   auto_del_cron
// 刻意不复用 settingsForm.*：那些字段同时是「订阅设置」弹窗里 el-switch 的 v-model，
// 而该弹窗的「取消」只做 showSettingsDialog = false、不重置表单。
// 于是「拨动开关 → 取消 → 打开订阅编辑弹窗」会把用户已经撤销的值当成服务端现状展示出来，
// 用户据此选了 inherit，下次拉取的实际行为和提示相反（提示保留本地、实际 reset --hard）。
// 所以这里只在真正拿到/写入服务端值的时刻更新：页面加载、打开设置弹窗回包、设置保存成功、
// 打开订阅编辑弹窗时的静默刷新。settingsForm 从此只负责设置弹窗自己的编辑态。
const globalOverwriteDefault = ref(true);
const globalAutoAddDefault = ref(true);
const globalAutoDelDefault = ref(true);
// 上面那一组值到底读到没有。三个值来自同一次 /configs 回包、在同样的三个时刻一起更新，
// 所以共用一个标志位即可。没读到时订阅表单不显示「（当前：X）」，见 loadGlobalDefaults。
const globalDefaultsLoaded = ref(false);
const settingsForm = ref({
  github_mirror: "",
  auto_add_cron: true,
  auto_del_cron: true,
  subscription_force_overwrite: true,
  default_cron_rule: "",
  repo_file_extensions: "",
});

const GITHUB_MIRROR_STORAGE_KEY = "subscription.github_mirror";
const DEFAULT_GITHUB_MIRROR = "https://gh-proxy.com/";
const githubMirror = ref(
  localStorage.getItem(GITHUB_MIRROR_STORAGE_KEY) || DEFAULT_GITHUB_MIRROR,
);

function normalizeMirror(u: string): string {
  const t = u.trim();
  if (!t) return "";
  return t.endsWith("/") ? t : t + "/";
}

const editForm = ref({
  id: 0,
  name: "",
  type: "git-repo",
  url: "",
  branch: "",
  schedule: "",
  whitelist: "",
  blacklist: "",
  depend_on: "",
  pre_script: "",
  hook_script: "",
  // ⚠️ 刻意不放旧布尔字段 auto_add_task / auto_del_task：后端已废弃、只做只读输出，
  // 表单里带着它们的唯一后果是「原样回传」——用户把订阅改成「跟随全局设置」并保存时，
  // 顺带把 auto_add_task=true 也写回了库，下次重启被启动回填提回「强制开启」，
  // 用户的选择静默消失。真正生效的是下面的 auto_add_task_mode / auto_del_task_mode。
  //
  // 同步任务三态：inherit=跟随全局设置 / enabled=强制开启 / disabled=强制关闭。
  // 与 overwrite_mode 一样要多处同步，但落点比它多两处，一共六处：
  //   这里的初值、openCreate、openEdit 回填、handleSave 提交、表单控件、
  //   以及只读展示的全局默认值（globalAutoAddDefault / globalAutoDelDefault）。
  // 注意它和 overwrite_mode / full_checkout 有一点关键差别：那两个只对 git 仓库生效，
  // 而同步任务对单文件订阅一样跑，所以既不加 v-if，handleSave 里也不能跟着复位。
  // 类型写成联合而不是 string，与下面 auth_type 的既有写法一致：
  // 打错一个档位（比如 "enable"）能在编译期就被 SubscriptionPayload 挡住，不用等到线上存不住。
  auto_add_task_mode: "inherit" as "inherit" | "enabled" | "disabled",
  auto_del_task_mode: "inherit" as "inherit" | "enabled" | "disabled",
  save_dir: "",
  sub_path: "",
  auth_type: "" as "" | "ssh" | "token",
  ssh_key_id: null as number | null,
  auth_username: "",
  auth_token: "",
  has_auth_token: false,
  alias: "",
  // 覆盖拉取策略三态：inherit=跟随全局 / force=强制覆盖 / preserve=保留本地修改。
  // 这个字段要在四个地方同步：这里的初值、openCreate、openEdit 回填、handleSave 提交，
  // 漏掉任意一处的表现都是「设了但存不住」。
  overwrite_mode: "inherit",
  // 完整检出：true = 跳过 sparse-checkout 拉整个仓库，false = 按白名单/子目录/依赖规则稀疏检出。
  // 默认必须是 false —— 存量订阅升级后行为要完全不变，见后端同名字段 full_checkout。
  // 同样要在四处同步（这里的初值、openCreate、openEdit 回填、handleSave 提交）。
  full_checkout: false,
});

// 新建 / 编辑订阅弹窗里的「高级设置」是否展开（v3.2.9 精简弹窗）。默认折叠。
// 弹窗没有 destroy-on-close，openCreate / openEdit 里都要复位成 false，否则会沿用上一次关弹窗时的展开状态。
const showAdvanced = ref(false);

// 「高级设置」折叠行右侧的摘要。一键识别会往折叠区里写分支、拉取后钩子、自动建任务=强制开启，
// 编辑存量订阅时也常带着鉴权、钩子；没有这行摘要的话，这些值藏在折叠区里用户根本看不见。
// 只列「和新建时的默认值不一样」的项：字符串去掉首尾空白后非空、三态不是 inherit、完整检出为开。
// 保存目录不算：一键识别总会填它，算进去摘要就几乎永远不为空，失去提示意义。
// 只对 git 仓库显示的字段（分支 / 指定子目录 / 鉴权 / 完整检出 / 覆盖拉取）在单文件订阅下不列——
// 表单里看不到它们，列出来用户展开也找不到。顺序与高级区里的字段顺序一致。
const advancedSummary = computed(() => {
  const form = editForm.value;
  const isGit = form.type === "git-repo";
  const items: string[] = [];
  if (isGit && form.branch.trim()) items.push("分支");
  if (isGit && form.sub_path.trim()) items.push("指定子目录");
  if (form.alias.trim()) items.push("别名");
  if (isGit && form.auth_type) items.push("鉴权");
  if (isGit && form.full_checkout) items.push("完整检出");
  if (isGit && form.overwrite_mode !== "inherit") items.push("覆盖拉取");
  if (form.auto_add_task_mode !== "inherit") items.push("自动建任务");
  if (form.auto_del_task_mode !== "inherit") items.push("自动删任务");
  if (form.pre_script.trim()) items.push("拉取前指令");
  if (form.hook_script.trim()) items.push("拉取后钩子");
  if (items.length > 0) return `已设置：${items.join("、")}`;
  // 都是默认值时只概述折叠区里有什么，按类型给，别对单文件订阅提分支和鉴权
  return isGit
    ? "分支、目录、鉴权、任务同步、钩子"
    : "目录、别名、任务同步、钩子";
});

// 保存失败时，如果后端 400 点名的是折叠区里的字段，自动展开「高级设置」，让用户看得见该改哪一项。
// 前端自己只校验名称和 URL（都在基本区）。后端会点名折叠区字段的文案（server/handler/subscription.go）：
//   - 仓库鉴权：「已选择 SSH 鉴权，请指定 SSH 密钥」「已选择 Token 鉴权，请填写访问令牌」「无效的仓库鉴权方式」「无效的仓库访问令牌」；
//   - 创建判重：「相同地址、分支、子目录、保存目录和别名的订阅已存在」——能改来区分的字段除 URL 外全在折叠区。
// 白名单 / 黑名单 / 依赖规则的正则报错以字段名开头，并且会原样带出用户写的片段，
// 片段里可能恰好含下面的关键字，所以先按开头排除，免得正则写错反而展开了高级设置。
const ADVANCED_FIELD_ERROR_KEYWORDS = [
  "鉴权",
  "访问令牌",
  "SSH 密钥",
  "分支",
  "子目录",
  "保存目录",
  "别名",
];
function revealAdvancedForError(message: string) {
  if (!message || /^(白名单|黑名单|依赖规则)/.test(message)) return;
  if (
    ADVANCED_FIELD_ERROR_KEYWORDS.some((keyword) => message.includes(keyword))
  ) {
    showAdvanced.value = true;
  }
}

const sshKeys = ref<any[]>([]);
const showSSHKeyManageDialog = ref(false);
const showSSHKeyDialog = ref(false);
const isCreateSSHKey = ref(true);
const sshKeyForm = ref({ id: 0, name: "", private_key: "" });
const sshKeyLoading = ref(false);
// SSH 密钥保存的在途锁：与订阅保存各用一个独立 ref，避免互相牵连转圈
const sshKeySaving = ref(false);

const logList = ref<any[]>([]);
const logTotal = ref(0);
const logPage = ref(1);
const logSubId = ref(0);
const logLoading = ref(false);

const showLogDetail = ref(false);
const logDetailContent = ref("");
const logDetailContentHtml = computed(() =>
  ansiToHtml(normalizeAnsi(logDetailContent.value || "(无日志内容)")),
);

const showPullLog = ref(false);
const pullLogLines = ref<string[]>([]);
const pullLogLineHtmlList = computed(() =>
  pullLogLines.value.map((line) => ansiToHtml(normalizeAnsi(line))),
);
const pullRunning = ref(false);
const pullingSubId = ref<number | null>(null);
let pullEventSource: EventStreamConnection | null = null;
const pullLogRef = ref<HTMLElement>();
// 拉取日志的自动跟随（#133）：最新一行在可视区内就贴底跟随，用户上翻超过阈值即暂停、滚回底部恢复。
// 拉取日志弹窗不带 destroy-on-close、容器常驻，所以每次打开（beginPullSession / reattachPullStream）
// 都要 begin(true) 重置，否则会沿用上一次拉取结束时冻结下来的跟随态。
const pullFollow = useLogAutoFollow(pullLogRef);
let pullBuffer: string[] = [];
let pullFlushRaf = 0;

// 拉取结束后的「业务结果」。注意它和 pullRunning 是两回事：
// pullRunning 描述的是 SSE 连接/拉取是否还在进行，pullOutcome 描述的是跑完之后成没成。
// idle = 还没有可展示的终态；unknown = 判不出来（回退到改造前的「已完成」）。
type PullOutcome =
  | "idle"
  | "success"
  | "failed"
  | "aborted"
  | "disconnected"
  | "unknown";
const pullOutcome = ref<PullOutcome>("idle");

// 竞态守卫：每开始一次拉取会话就递增。异步查库回来时比对会话号，
// 对不上就整个丢弃 —— 覆盖「弹窗已关」「用户切到别的订阅」「又发起了一次拉取」三种情况。
let pullSessionSeq = 0;
let pullSession = 0;

// 本次拉取开始前，该订阅最新一条 sub_log 的 id（一条都没有时记 0）。
// null 表示基线没取到（查询失败），此时不做业务状态判定。
let pullBaselineLogId: number | null = null;

// 用户是否点过「停止」。后端把「拉取已停止」记成 status=1，跟真失败无法区分；
// 但前端知道是主动终止，所以本地打标记，done 之后直接显示「已终止」，不查库。
let pullStopRequested = false;

// footer 左侧状态指示：色标 tone + 文案。
// 恒返回对象而不是 `对象 | null`，是为了让模板里 v-if 和 :class 能挂在同一个元素上
// 而不依赖类型收窄 —— text 为空串即表示不渲染。
const pullStatusView = computed<{ tone: string; text: string }>(() => {
  if (pullRunning.value) return { tone: "running", text: "运行中" };
  switch (pullOutcome.value) {
    case "success":
      return { tone: "success", text: "成功" };
    case "failed":
      return { tone: "failed", text: "失败" };
    case "aborted":
      return { tone: "aborted", text: "已终止" };
    case "disconnected":
      return { tone: "disconnected", text: "连接中断" };
    default:
      // 判不出业务状态时严格回退到改造前的表现：有输出就「已完成」，没输出就什么都不显示。
      return {
        tone: "unknown",
        text: pullLogLines.value.length > 0 ? "已完成" : "",
      };
  }
});

async function loadData() {
  loading.value = true;
  try {
    const res = await subscriptionApi.list({
      keyword: keyword.value || undefined,
      type:
        typeFilter.value && typeFilter.value !== "disabled"
          ? typeFilter.value
          : undefined,
      enabled: typeFilter.value === "disabled" ? false : undefined,
      page: page.value,
      page_size: pageSize.value,
    });
    subList.value = res.data || [];
    total.value = res.total || 0;
    // 勾选裁剪到刷新后仍可见的行：移动卡片的勾选不经过 el-table，翻页、筛选、单删之后
    // 旧勾选会残留，批量删除就会作用到看不见的订阅。裁剪后没有剩余时批量栏自动退出。
    // 桌面无副作用：el-table 换了 data 数组本来就会清空选择并回调 selection-change([])。
    const visibleIds = new Set(filteredSubList.value.map((row) => row.id));
    selectedIds.value = selectedIds.value.filter((id) => visibleIds.has(id));
  } catch (err: any) {
    ElMessage.error(err?.response?.data?.error || "加载订阅列表失败");
  } finally {
    loading.value = false;
  }
}

async function loadSSHKeys() {
  sshKeyLoading.value = true;
  try {
    const res = await sshKeyApi.list();
    sshKeys.value = res.data || [];
  } catch {
    /* ignore */
  } finally {
    sshKeyLoading.value = false;
  }
}

// 订阅表单里「跟随全局设置」那几项要当场标出全局开关现在是什么值，所以进页面就先读一次；
// 打开「订阅设置」弹窗（handleOpenSettings）、保存设置（handleSaveSettings）、
// 打开订阅编辑弹窗（openEdit）时都会再刷新一遍。
//
// /configs 是 admin-only 接口，所以这里先按角色 gate：operator 直接返回、根本不发这个必然 403 的请求。
// 派生结论是「（当前：X）」这句话对 operator 永远不存在（globalDefaultsLoaded 保持 false）——
// 这是有意为之：拿不到服务端真值时宁可不写，也别把前端写死的默认值当成实际值展示误导用户。
async function loadGlobalDefaults() {
  if (!isAdmin.value) return;
  try {
    const res = await configApi.list();
    const cfgs = res.data || {};
    globalOverwriteDefault.value = readCfgBool(
      cfgs,
      "subscription_force_overwrite",
      true,
    );
    // 这两个默认值同样是 true（见后端配置注册表），与订阅三态的 inherit 档配套展示
    globalAutoAddDefault.value = readCfgBool(cfgs, "auto_add_cron", true);
    globalAutoDelDefault.value = readCfgBool(cfgs, "auto_del_cron", true);
    globalDefaultsLoaded.value = true;
  } catch {
    /* ignore：失败就沿用上一次读到的值，不弹错 */
  }
}

onMounted(() => {
  // 进页即把侧栏的「订阅管理」失败角标标记为已读——用户已经站在这一页上了，再红着没有意义
  badgesStore.ackSubsFailed();
  loadData();
  loadSSHKeys();
  loadGlobalDefaults();
});

onActivated(() => {
  // 必须和 onMounted 两处都写：MainLayout 的 keep-alive 是 :max="14"，
  // 第二次以后进本页只触发 onActivated、不再触发 onMounted，只写一处会只有首访生效。
  // 这里刻意只清角标、不重新拉列表，保持本页原有的「缓存页不自动刷新」行为不变。
  badgesStore.ackSubsFailed();
});

onBeforeUnmount(() => {
  closePullStream();
  if (pullFlushRaf) {
    cancelAnimationFrame(pullFlushRaf);
    pullFlushRaf = 0;
  }
});

function handleSearch() {
  page.value = 1;
  loadData();
}

// 翻页回顶（双端）：只挂在分页器的 current-change / size-change 上，数据回来之后再滚。
// 不能挂进 loadData：拉取结束、保存、删除之后都会调它，用户正往下看着会被一把拽回顶部。
async function handlePageChange() {
  await loadData();
  await nextTick();
  scrollListToTop(pageRootRef.value);
}

async function handlePageSizeChange() {
  page.value = 1;
  await loadData();
  await nextTick();
  scrollListToTop(pageRootRef.value);
}

function handleTypeFilter(value: SubTypeFilter) {
  if (typeFilter.value === value) {
    return;
  }
  typeFilter.value = value;
  page.value = 1;
  // 移动卡片的勾选不经过 el-table：分段一切，filteredSubList 就在旧数据上当场把不符的卡片筛掉了，
  // 所以勾选要在这里就裁，不能只指望 loadData 成功分支里那次裁剪——请求一失败，
  // 看不见的订阅会一直挂在勾选里，批量删除就会删到它们（约束 9）。
  // 桌面同样无副作用：表格 data 跟着换成新数组，el-table 会自行清空选择。
  const visibleIds = new Set(filteredSubList.value.map((row) => row.id));
  selectedIds.value = selectedIds.value.filter((id) => visibleIds.has(id));
  loadData();
}

function openCreate() {
  isCreate.value = true;
  qlCommand.value = "";
  editForm.value = {
    id: 0,
    name: "",
    type: "git-repo",
    url: "",
    branch: "",
    schedule: "",
    whitelist: "",
    blacklist: "",
    depend_on: "",
    pre_script: "",
    hook_script: "",
    // 新建订阅默认跟随全局设置，与 editForm 的初值保持一致
    auto_add_task_mode: "inherit",
    auto_del_task_mode: "inherit",
    save_dir: "",
    sub_path: "",
    auth_type: "",
    ssh_key_id: null,
    auth_username: "",
    auth_token: "",
    has_auth_token: false,
    alias: "",
    overwrite_mode: "inherit",
    full_checkout: false,
  };
  // 高级设置每次打开都从折叠开始（弹窗不销毁，不复位会沿用上次的展开状态）
  showAdvanced.value = false;
  showEditDialog.value = true;
}

function addGithubMirror(url: string): string {
  if (!url) return url;
  const mirror = normalizeMirror(githubMirror.value);
  if (!mirror) return url;
  const githubPattern = /^https?:\/\/github\.com\//;
  // 已经包含镜像（任何协议）就不再重复包裹
  const mirrorHost = mirror.replace(/^https?:\/\//, "").replace(/\/$/, "");
  if (mirrorHost && url.includes(mirrorHost)) return url;
  if (githubPattern.test(url)) {
    return url.replace(
      /^https?:\/\/github\.com\//,
      mirror + "https://github.com/",
    );
  }
  return url;
}

function readCfgBool(
  cfgs: Record<string, any>,
  key: string,
  fallback: boolean,
): boolean {
  const entry = cfgs[key];
  const raw = String(
    entry?.value ?? entry?.default_value ?? (fallback ? "true" : "false"),
  )
    .trim()
    .toLowerCase();
  if (["true", "1", "yes", "on"].includes(raw)) return true;
  if (["false", "0", "no", "off"].includes(raw)) return false;
  return fallback;
}

function readCfgStr(
  cfgs: Record<string, any>,
  key: string,
  fallback = "",
): string {
  const entry = cfgs[key];
  const raw = entry?.value ?? entry?.default_value ?? fallback;
  return raw === null || raw === undefined ? fallback : String(raw);
}

async function handleOpenSettings() {
  showSettingsDialog.value = true;
  settingsForm.value.github_mirror = githubMirror.value;
  settingsLoading.value = true;
  try {
    const res = await configApi.list();
    const cfgs = res.data || {};
    settingsForm.value.auto_add_cron = readCfgBool(cfgs, "auto_add_cron", true);
    settingsForm.value.auto_del_cron = readCfgBool(cfgs, "auto_del_cron", true);
    settingsForm.value.subscription_force_overwrite = readCfgBool(
      cfgs,
      "subscription_force_overwrite",
      true,
    );
    // 这一刻读到的是服务端最新值，同步给只读展示 ref；此后用户在这个弹窗里怎么拨开关、
    // 拨完是点保存还是点取消，都不会再影响订阅编辑弹窗里的「（当前：X）」。
    // 三个默认值要一起同步：只同步覆盖拉取的话，另外两句「（当前：X）」会停在旧值上。
    globalOverwriteDefault.value =
      settingsForm.value.subscription_force_overwrite;
    globalAutoAddDefault.value = settingsForm.value.auto_add_cron;
    globalAutoDelDefault.value = settingsForm.value.auto_del_cron;
    globalDefaultsLoaded.value = true;
    settingsForm.value.default_cron_rule = readCfgStr(
      cfgs,
      "default_cron_rule",
      "",
    );
    settingsForm.value.repo_file_extensions = readCfgStr(
      cfgs,
      "repo_file_extensions",
      "",
    );
  } catch (err: any) {
    ElMessage.error(err?.response?.data?.error || "加载订阅设置失败");
  } finally {
    settingsLoading.value = false;
  }
}

async function handleSaveSettings() {
  const mirrorRaw = (settingsForm.value.github_mirror || "").trim();
  if (mirrorRaw && !/^https?:\/\/.+/.test(mirrorRaw)) {
    ElMessage.warning("镜像地址需以 http:// 或 https:// 开头");
    return;
  }
  settingsSaving.value = true;
  try {
    await configApi.batchSet({
      auto_add_cron: settingsForm.value.auto_add_cron ? "true" : "false",
      auto_del_cron: settingsForm.value.auto_del_cron ? "true" : "false",
      subscription_force_overwrite: settingsForm.value
        .subscription_force_overwrite
        ? "true"
        : "false",
      default_cron_rule: settingsForm.value.default_cron_rule,
      repo_file_extensions: settingsForm.value.repo_file_extensions,
    });
    // 保存成功 ⇒ 编辑态的值已经落到服务端，只读展示值同步跟上（失败时不动，展示的仍是旧的服务端值）。
    globalOverwriteDefault.value =
      settingsForm.value.subscription_force_overwrite;
    globalAutoAddDefault.value = settingsForm.value.auto_add_cron;
    globalAutoDelDefault.value = settingsForm.value.auto_del_cron;
    globalDefaultsLoaded.value = true;
    const mirror = mirrorRaw || DEFAULT_GITHUB_MIRROR;
    githubMirror.value = normalizeMirror(mirror);
    localStorage.setItem(GITHUB_MIRROR_STORAGE_KEY, githubMirror.value);
    ElMessage.success("订阅设置已保存");
    showSettingsDialog.value = false;
  } catch (err: any) {
    ElMessage.error(err?.response?.data?.error || "保存失败");
  } finally {
    settingsSaving.value = false;
  }
}

function deriveSubscriptionSaveDir(url: string): string {
  const trimmed = url
    .trim()
    .replace(/\/+$/, "")
    .replace(/\.git$/i, "");
  if (!trimmed) return "";
  const parts = trimmed.split("/").filter(Boolean);
  if (parts.length >= 2) {
    const owner = parts[parts.length - 2];
    const repo = parts[parts.length - 1];
    if (owner && repo) {
      return `${owner}_${repo}`;
    }
  }
  return parts[parts.length - 1] || "";
}

function normalizeRecognizedHookScript(raw: string): string {
  return raw
    .replace(
      /(?:\$\{?QL_DIR\}?|%QL_DIR%)[/\\]data[/\\](?:repo|scripts)[/\\][^/\\"'\s;]+/g,
      "$SUB_DIR",
    )
    .trim();
}

function parseQLCommand() {
  const cmd = qlCommand.value.trim();
  if (!cmd) return;

  const lines = cmd
    .split(/\r?\n/)
    .map((line) => line.trim())
    .filter(Boolean);
  const qlLine = lines.find((line) => /^ql\s+(repo|raw)\b/.test(line)) || cmd;
  const hookScript = normalizeRecognizedHookScript(
    lines
      .filter((line) => line !== qlLine && !/^ql\s+(repo|raw)\b/.test(line))
      .join(" ; "),
  );

  const repoMatch = qlLine.match(
    /ql\s+repo\s+"?([^\s"]+)"?\s*"?([^"]*)"?\s*"?([^"]*)"?\s*"?([^"]*)"?\s*"?([^"]*)"?/,
  );
  if (repoMatch) {
    const [, url = "", whitelist, blacklist, dependOn, branch] = repoMatch;
    const repoName =
      url
        .replace(/\.git$/, "")
        .split("/")
        .pop() || "repo";
    editForm.value.type = "git-repo";
    editForm.value.url = addGithubMirror(url);
    editForm.value.name = repoName;
    editForm.value.save_dir = deriveSubscriptionSaveDir(url);
    editForm.value.whitelist = whitelist || "";
    editForm.value.blacklist = blacklist || "";
    editForm.value.branch = branch || "";
    editForm.value.depend_on = dependOn || "";
    // 刻意不设 full_checkout：青龙 ql repo 的位置参数（url / 白名单 / 黑名单 / 依赖 / 分支…）
    // 里没有「完整检出」的对位概念，硬猜一个值只会让识别结果和用户粘贴的命令不符。
    // 它保持 openCreate 给的 false（稀疏检出），需要整仓的用户自己去开那个开关。
    if (hookScript) editForm.value.hook_script = hookScript;
    // ql repo 的语义就是「拉下来并建任务」，所以这里强制开启而不是留给全局设置：
    // 用户全局关掉了自动建任务时，粘贴 ql 命令建出来的订阅仍应按命令本意建任务。
    // 只动「添加」，不动「删除」——ql repo 没有删任务的对位概念，保持 inherit。
    editForm.value.auto_add_task_mode = "enabled";
    ElMessage.success("已识别 ql repo 命令");
    qlCommand.value = "";
    return;
  }

  const rawMatch = qlLine.match(/ql\s+raw\s+"?([^\s"]+)"?/);
  if (rawMatch) {
    const url = rawMatch[1] || "";
    const fileName = url.split("/").pop() || "file";
    editForm.value.type = "single-file";
    editForm.value.url = addGithubMirror(url);
    editForm.value.name = fileName.replace(/\.[^/.]+$/, "");
    editForm.value.save_dir = deriveSubscriptionSaveDir(url) || "downloads";
    if (hookScript) editForm.value.hook_script = hookScript;
    // 同 ql repo：单文件订阅一样会建任务，这里同样强制开启「自动建任务」
    editForm.value.auto_add_task_mode = "enabled";
    ElMessage.success("已识别 ql raw 命令");
    qlCommand.value = "";
    return;
  }

  if (
    cmd.includes("github.com") ||
    cmd.includes(".git") ||
    cmd.startsWith("http")
  ) {
    editForm.value.url = addGithubMirror(cmd);
    const repoName =
      cmd
        .replace(/\.git$/, "")
        .split("/")
        .pop() || "";
    if (repoName) editForm.value.name = repoName;
    editForm.value.save_dir = deriveSubscriptionSaveDir(cmd);
    editForm.value.type =
      cmd.endsWith(".js") ||
      cmd.endsWith(".py") ||
      cmd.endsWith(".ts") ||
      cmd.endsWith(".sh")
        ? "single-file"
        : "git-repo";
    ElMessage.success("已识别链接");
    qlCommand.value = "";
    return;
  }

  ElMessage.warning("无法识别命令格式，支持 ql repo/raw 命令或直接粘贴链接");
}

function openEdit(row: any) {
  isCreate.value = false;
  editForm.value = {
    id: row.id,
    name: row.name,
    type: row.type,
    url: row.url,
    branch: row.branch || "",
    schedule: row.schedule || "",
    whitelist: row.whitelist || "",
    blacklist: row.blacklist || "",
    depend_on: row.depend_on || "",
    pre_script: row.pre_script || "",
    hook_script: row.hook_script || "",
    // 老库或老接口没有这两个字段时回落 inherit（跟随全局），与后端归一口径一致。
    // 刻意不回填、也不提交旧布尔字段 row.auto_add_task / row.auto_del_task：
    // 它们已废弃、后端不再读，抄进表单只会被原样回传写回库，
    // 让用户选的「跟随全局设置」在下次重启被启动回填提回「强制开启」。
    // 按它们推导三态同样不行——会把「跟随全局」的存量订阅静默钉死成强制档。
    auto_add_task_mode: row.auto_add_task_mode || "inherit",
    auto_del_task_mode: row.auto_del_task_mode || "inherit",
    save_dir: row.save_dir || "",
    sub_path: row.sub_path || "",
    auth_type: row.auth_type || "",
    ssh_key_id: row.ssh_key_id,
    auth_username: row.auth_username || "",
    auth_token: "",
    has_auth_token: !!row.has_auth_token,
    alias: row.alias || "",
    // 老库或老接口没有这个字段时回落 inherit（跟随全局），与后端归一口径一致。
    overwrite_mode: row.overwrite_mode || "inherit",
    // 同理：老库/老接口没有 full_checkout 时 row.full_checkout 是 undefined，
    // 用 !! 归一成 false（稀疏检出），保持存量订阅的既有行为。
    full_checkout: !!row.full_checkout,
  };
  // 同 openCreate：从折叠开始，已设置的高级项靠折叠行的 advancedSummary 提示
  showAdvanced.value = false;
  showEditDialog.value = true;
  // 「（当前：X）」展示的是全局开关：别的管理员在别处改过之后，本页那个值一旦读到就不会自己回落，
  // 会一直陈旧到用户手动打开一次「订阅设置」。这里顺手静默刷新一次
  //（非管理员在函数内部直接 return；失败就沿用旧值、不弹错），代价只有一次低频请求。
  void loadGlobalDefaults();
}

async function handleSave() {
  if (!editForm.value.name.trim() || !editForm.value.url.trim()) {
    ElMessage.warning("名称和 URL 不能为空");
    return;
  }
  const mirror = normalizeMirror(githubMirror.value);
  const mirrorHost = mirror.replace(/^https?:\/\//, "").replace(/\/$/, "");
  const githubDirect =
    /^https?:\/\/github\.com\//.test(editForm.value.url) &&
    mirrorHost &&
    !editForm.value.url.includes(mirrorHost);
  // 置位放在镜像确认框之前：确认框本身也是 await，弹着的时候按钮同样必须是锁住的，
  // 否则用户可以在确认框外面继续连点「创建」，锁不住完整的在途窗口。
  editSaving.value = true;
  if (githubDirect) {
    try {
      await ElMessageBox.confirm(
        "检测到 GitHub 直连地址，是否自动添加镜像加速？\n加速地址: " + mirror,
        "镜像加速",
        {
          confirmButtonText: "添加加速",
          cancelButtonText: "保持原样",
          type: "info",
        },
      );
      editForm.value.url = addGithubMirror(editForm.value.url);
    } catch {
      /* keep original */
    }
  }
  try {
    const data = { ...editForm.value };
    if (data.type !== "git-repo") {
      data.auth_type = "";
      data.ssh_key_id = null;
      data.auth_username = "";
      data.auth_token = "";
      // 单文件订阅没有 git 工作区，覆盖策略对它无意义（后端也不会读），
      // 存成 inherit 免得用户先在 git 模式选了强制覆盖、改成单文件后还留着一个假设置。
      data.overwrite_mode = "inherit";
      // 完整检出同理：单文件订阅根本没有 clone / sparse-checkout 这一步，
      // 表单里也不显示这个开关，所以先在 git 模式打开、再改成单文件时要一并复位，
      // 免得库里留下一个永远不会生效、改回 git 仓库时却会突然生效的值。
      data.full_checkout = false;
      // ⚠️ auto_add_task_mode / auto_del_task_mode 绝不能跟着复位：
      // overwrite_mode 与 full_checkout 之所以要复位，是因为它们只在 git clone / sparse-checkout
      // 这一步生效、单文件订阅根本走不到；而「同步定时任务」对单文件订阅一样跑，
      // 表单里也照样显示这两组单选。跟着复位的表现是「用户明明在界面上选了强制关闭，
      // 保存后却被悄悄改回跟随全局」——设了但存不住，而且没有任何提示。
    } else if (data.auth_type === "ssh") {
      data.auth_username = "";
      data.auth_token = "";
    } else if (data.auth_type === "token") {
      data.ssh_key_id = null;
    } else {
      data.ssh_key_id = null;
      data.auth_username = "";
      data.auth_token = "";
    }
    delete (data as any).has_auth_token;
    if (isCreate.value) {
      await subscriptionApi.create(data);
      ElMessage.success("创建成功");
    } else {
      await subscriptionApi.update(data.id, data);
      ElMessage.success("更新成功");
    }
    showEditDialog.value = false;
    loadData();
  } catch (err: any) {
    const serverError = err?.response?.data?.error;
    // 后端点名的是折叠区字段（鉴权、判重里的分支 / 目录 / 别名）时先展开高级设置，再弹原文
    if (typeof serverError === "string") revealAdvancedForError(serverError);
    ElMessage.error(serverError || (isCreate.value ? "创建失败" : "更新失败"));
  } finally {
    editSaving.value = false;
  }
}

async function handleDelete(id: number) {
  try {
    await ElMessageBox.confirm("确定要删除该订阅吗？", "确认删除", {
      type: "warning",
    });
    await subscriptionApi.delete(id);
    ElMessage.success("删除成功");
    loadData();
  } catch {
    /* cancelled */
  }
}

async function handleToggle(row: any) {
  try {
    const enabling = !row.enabled;
    await ElMessageBox.confirm(
      enabling
        ? `确认启用订阅「${row.name}」吗？`
        : `确认禁用订阅「${row.name}」吗？禁用后将停止后续自动拉取。`,
      enabling ? "启用确认" : "禁用确认",
      { type: enabling ? "info" : "warning" },
    );
    if (row.enabled) {
      await subscriptionApi.disable(row.id);
    } else {
      await subscriptionApi.enable(row.id);
    }
    ElMessage.success(row.enabled ? "已禁用" : "已启用");
    loadData();
  } catch (err: any) {
    if (err === "cancel" || err?.toString?.() === "cancel") return;
    ElMessage.error(err?.response?.data?.error || "操作失败");
  }
}

// 禁用行整行弱化（#133，照环境变量页 getRowClassName 的禁用分支）。类名只用于样式，
// 弱化的写法与三条坑见 <style> 里 .sub-row-disabled 那段注释。
function getRowClassName({ row }: { row: any }) {
  return row.enabled ? "" : "sub-row-disabled";
}

/**
 * 操作列 Split Button 的菜单项。
 *
 * 主体是「拉取」：列表里最高频的操作，而且 handlePull 进去还有一道 ElMessageBox
 * 确认框兜底（正在拉取同一条时只是重连 SSE），点错了代价最小。
 * 删除不可撤销，只能待在菜单里并加 divided + danger——「点了就执行」的主体位置
 * 绝不能放这种一击致命的操作。
 *
 * 这几项都不随行状态变化：订阅的「启用/禁用」外置在本列 Split Button 右侧的独立按钮上
 *（#133 起与环境变量页同一形态，原来那一整列「启用」el-switch 已删），刻意不在菜单里再放一份——
 * 已外置的操作不进菜单，否则同一件事两个入口、还得按行状态做 visible 联动；
 * 「停止拉取」也只挂在拉取日志弹窗的 footer（handleStopPull），本来就不属于这一列。
 *
 * 移动卡片右上角的「···」（DdMoreMenu）直接复用这一份数组和 onSubAction，
 * 卡片末行已经外置了「拉取」「禁用 / 启用」，与桌面操作列同一套分工。
 * 改这里的文案或顺序会同时改到两端。
 */
const subActionItems: SplitButtonItem[] = [
  { key: "logs", label: "拉取日志" },
  { key: "edit", label: "编辑" },
  { key: "delete", label: "删除", danger: true, divided: true },
];

function onSubAction(key: string, row: any) {
  if (key === "logs") openLogs(row.id);
  else if (key === "edit") openEdit(row);
  else if (key === "delete") handleDelete(row.id);
}

async function fetchLatestSubLog(subId: number) {
  const res = await subscriptionApi.logs(subId, { page: 1, page_size: 1 });
  // 后端按 created_at DESC 排序，page_size=1 拿到的就是最新一条。
  return (res.data || [])[0] || null;
}

// 取「当前最新一条日志的 id」当基线。取不到（网络/权限）返回 null，后续就不判业务状态。
async function readPullLogBaseline(subId: number): Promise<number | null> {
  try {
    const latest = await fetchLatestSubLog(subId);
    const id = Number(latest?.id);
    // 一条日志都没有时基线记 0：之后任何新记录的自增 id 都会大于 0。
    return Number.isFinite(id) ? id : 0;
  } catch {
    return null;
  }
}

// 「全新一次拉取」的会话初始化：基线、停止标记、业务结果全部重来。
// 重连（关掉弹窗后再打开）不能走这里，见下面的 reattachPullStream。
function beginPullSession(subId: number, baseline: number | null) {
  pullSession = ++pullSessionSeq;
  pullBaselineLogId = baseline;
  pullStopRequested = false;
  pullOutcome.value = "idle";
  pullRunning.value = true;
  pullingSubId.value = subId;
  // 新一次拉取：日志从空开始增长，默认跟随。容器常驻，不重置的话会沿用上一次 end() 冻结的跟随态。
  pullFollow.begin(true);
  showPullLog.value = true;
}

// 拉取途中关掉弹窗（handlePullDialogClose 切断了 SSE，但 pullRunning 保持 true），
// 再次点同一条订阅的「拉取」时走这里：把 SSE 重新接回来，而不是只把弹窗显示出来。
// PullStream 对新订阅者会先补发 broadcaster.history() 的全部历史行再进实时循环，
// 所以重连能拿回完整日志；广播器已经销毁（拉取早就结束）时后端直接回 done/not_running，
// 状态也能收敛掉，不会再永远卡在「运行中」。
function reattachPullStream(subId: number) {
  // 必须清空：后端会把 history() 整体重发一遍，不清的话弹窗里已有的行 + 补发的历史
  // 会整体重复一遍。顺带把还没 flush 的缓冲和在途的 rAF 也一起清掉，否则那一帧
  // 会把清空前的旧行再补回来。
  pullLogLines.value = [];
  pullBuffer = [];
  if (pullFlushRaf) {
    cancelAnimationFrame(pullFlushRaf);
    pullFlushRaf = 0;
  }

  // 只 bump 会话号，不碰其余状态 —— 这正是不能复用 beginPullSession 的原因：
  //   pullBaselineLogId 重读会把本次拉取已经落库的那条记录也算进基线，
  //     导致 latestId > baseline 不成立、降级成 unknown，白白丢掉成功/失败判定；
  //   pullStopRequested 清掉的话，用户停止后关弹窗再打开会显示成「失败」而不是「已终止」。
  // 关弹窗时 handlePullDialogClose 已经 bump 过一次（作废在途的查库结果），
  // 这里再 bump 出一个「当前有效」的会话号，让本次重连的 resolvePullOutcome 能写进状态。
  pullSession = ++pullSessionSeq;

  // 重连 = 用户重新打开了弹窗：正文刚清空、后端马上整体补发 history()，按新会话跟随即可。
  // 不沿用关弹窗前的暂停态——那次暂停对应的阅读位置已随正文清空不存在了；
  // 关弹窗时 handlePullDialogClose 已 end() 冻结，这里必须 begin(true) 解冻，否则补发的日志不会贴底。
  pullFollow.begin(true);

  showPullLog.value = true;
  // connectPullStream 内部第一件事就是 closePullStream()，已有连接会先关再连，不会叠加。
  connectPullStream(subId);
}

// 拉取结束后去查最近一条 sub_log，拿 status 区分成功/失败。
async function resolvePullOutcome(subId: number, session: number) {
  if (pullBaselineLogId === null) {
    pullOutcome.value = "unknown";
    return;
  }
  const baseline = pullBaselineLogId;

  let latest: any = null;
  try {
    latest = await fetchLatestSubLog(subId);
  } catch {
    // 查库失败不弹错、也不让 UI 卡在运行中，直接回退成改造前的「已完成」。
    if (pullSession === session) pullOutcome.value = "unknown";
    return;
  }
  if (pullSession !== session) return;

  const latestId = Number(latest?.id);
  if (!latest || !Number.isFinite(latestId) || latestId <= baseline) {
    // 本次拉取压根没落下新记录。ExecuteSubscriptionPull 在「订阅不存在」和
    // 「该订阅正在拉取中」两条路径上直接 return，PullSubscriptionWithContext 又在
    // 「写 SSH 密钥失败」和「构建 git 鉴权配置失败」两处提前 return，
    // 这四条都走不到 database.DB.Create(&subLog)。此时查到的是上一次拉取的结果，
    // 拿来当本次状态就是误报，所以标成 unknown。
    pullOutcome.value = "unknown";
    return;
  }

  pullOutcome.value = Number(latest.status) === 0 ? "success" : "failed";
}

async function handlePull(row: any) {
  // 同一条订阅、且前端认为还在拉取中：这次点击的语义是「回到那次拉取」而不是「再拉一次」，
  // 所以不弹确认、不重取基线，直接重连 SSE。
  // 只显示弹窗是不够的：弹窗关掉时 SSE 已经被切断，没有任何事件能把 pullRunning 置回 false，
  // 状态会永远停在「运行中」。点别的订阅不命中这条守卫，仍走下面的确认 + 拉取流程。
  if (pullingSubId.value === row.id && pullRunning.value) {
    reattachPullStream(row.id);
    return;
  }

  try {
    await ElMessageBox.confirm(
      `确认按订阅设置拉取订阅「${row.name}」吗？`,
      "拉取确认",
      {
        type: "warning",
        confirmButtonText: "立即拉取",
        cancelButtonText: "取消",
      },
    );
  } catch {
    return;
  }

  // 基线必须在拉取真正开始「之前」取：sub_logs.id 是自增主键，本次拉取一旦落库，
  // 新记录的 id 必然大于基线，据此就能判断「这次到底有没有产生新记录」。
  const baseline = await readPullLogBaseline(row.id);

  try {
    await subscriptionApi.pull(row.id);
    pullLogLines.value = [];
    beginPullSession(row.id, baseline);
    connectPullStream(row.id);
  } catch (err: any) {
    if (err?.response?.data?.error?.includes("拉取中")) {
      // 已在拉取中：SubLog 是整个拉取跑完之后才 Create 的，
      // 所以此刻的最新记录仍然属于上一次，直接拿来当基线是准的。
      // 这里同样要清空 —— 后端会补发 history()，残留旧行会和补发的历史重复。
      pullLogLines.value = [];
      beginPullSession(row.id, baseline);
      connectPullStream(row.id);
      return;
    }
    ElMessage.error(err?.response?.data?.error || "拉取失败");
  }
}

async function handleStopPull() {
  if (!pullingSubId.value) {
    return;
  }

  try {
    await ElMessageBox.confirm("确认停止当前拉库任务吗？", "停止拉库", {
      type: "warning",
      confirmButtonText: "停止",
      cancelButtonText: "取消",
    });
    await subscriptionApi.stopPull(pullingSubId.value);
    // 后端把「拉取已停止」当成 pullErr，落库就是 status=1，跟真正的失败无法区分。
    // 这里打个本地标记，done 之后直接显示「已终止」，从而不必给 SubLog.Status
    // 加第三个取值、也不必跟着改订阅列表状态列和日志表格。
    pullStopRequested = true;
    ElMessage.success("已发送停止请求");
  } catch (err: any) {
    if (err === "cancel" || err?.toString?.() === "cancel") return;
    ElMessage.error(err?.response?.data?.error || "停止失败");
  }
}

function connectPullStream(id: number) {
  closePullStream();
  const base = import.meta.env.VITE_API_BASE || "/api/v1";
  const url = `${base}/subscriptions/${id}/pull-stream`;
  pullEventSource = openAuthorizedEventStream(url, {
    onMessage(data) {
      pullBuffer.push(data);
      if (!pullFlushRaf) {
        pullFlushRaf = requestAnimationFrame(() => {
          pullFlushRaf = 0;
          flushPullBuffer();
        });
      }
    },
    onEvent(event) {
      if (event.event !== "done") return;

      const session = pullSession;
      // 先把还挂在 rAF 里的最后一批冲进去（跟随中会贴底）；冻结跟随态的 end() 要等下面
      // 补完提示行之后再调——end() 之后 onContentChange 是空操作，理由见 flushPullBuffer。
      flushPullBuffer();
      pullRunning.value = false;
      pullingSubId.value = null;
      closePullStream();
      loadData();

      // done 只说明「这条 SSE 连接结束了」，是传输状态不是业务状态。
      // 真正区分靠 data（PullStream 只发这五种）：
      //   finished     收到 \x00DONE 哨兵，拉取确实跑完了
      //   not_running  广播器已不存在，拉取早就结束了
      //   closed       订阅 channel 被 close，只可能来自 removeSubBroadcaster
      //   timeout      5 分钟静默
      //   reconnect    面板关停时收流（v3.3.5 起），拉取可能还没收尾；与 timeout 一样只报「连接中断」、不查库
      //
      // 前三种都代表「拉取已经返回」，SubLog 也已经 Create 完
      // （service 里 Create 在 done() 之前），可以查库拿成功/失败：
      //   - closed 之所以不是 finished，是因为 done() 往 64 槽缓冲 channel
      //     非阻塞发哨兵，槽满就丢；而 removeSubBroadcaster 只在
      //     handler 那个拉取 goroutine 的 defer 里调用，能收到 closed
      //     就说明 ExecuteSubscriptionPull 早已 return。
      //   - timeout 是 5 分钟静默，拉取可能还在跑（大仓库 clone），
      //     此刻查库拿到的会是上一次的结果，所以只报「连接中断」。
      //
      // 查库本身有 pullBaselineLogId 主键基线守卫兜底：本次没落新记录就降级成
      // 「已完成」，不会把上一次的旧结果误报成本次结果。
      const reason = event.data.trim();

      // not_running 说明广播器已随拉取结束一起销毁，history 也跟着没了，
      // 这条流一行日志都补发不出来。空日志配一个孤零零的状态太突兀，补一行指路。
      // 放在 pullStopRequested 分支之前：用户点过停止后关掉弹窗再打开，同样是空日志。
      // 最常见的触发路径就是「拉取途中关掉弹窗，等跑完之后再打开」。
      // 上面已同步 flush 过，缓冲一定是空的，所以只看已渲染的行数。
      if (reason === "not_running" && pullLogLines.value.length === 0) {
        pullLogLines.value.push(
          "[提示] 本次拉取已结束，完整日志请在订阅列表的「日志」中查看",
        );
        pullFollow.onContentChange();
      }
      // 这条连接不会再有新输出：冻结跟随态，此后滚动不再改变它、也不再自动贴底。
      // 必须排在 flush 与补提示行之后，否则结尾那几行与提示行都不会贴底。
      pullFollow.end();

      if (pullStopRequested) {
        pullOutcome.value = "aborted";
        return;
      }
      if (
        reason === "finished" ||
        reason === "not_running" ||
        reason === "closed"
      ) {
        // 用闭包里的 id 而不是 pullingSubId：上面刚把它置空，且这条流本来就是为 id 开的。
        void resolvePullOutcome(id, session);
        return;
      }
      pullOutcome.value = "disconnected";
    },
    onError() {
      // 与 done 同理：先冲掉 rAF 里的最后一批（跟随中会贴底），再冻结跟随态
      flushPullBuffer();
      pullFollow.end();
      pullRunning.value = false;
      pullingSubId.value = null;
      closePullStream();
      // 断网 / 刷新 token 失败等：拉取多半还在后端跑着，同样不查库。
      pullOutcome.value = pullStopRequested ? "aborted" : "disconnected";
    },
  });
}

// 把还挂在 rAF 里、没来得及 flush 的拉取日志立刻冲进去，并按跟随态贴底（与 deps 安装日志同一写法）。
// done / onError 时必须先调它再 end()：最后几行常与 done 同一帧到达，留给 rAF 的话那次
// onContentChange 会落在 end() 之后被忽略，跟随中的用户就看不到结尾那几行
//（旧实现在 rAF 里无条件贴底，没有这个问题）。
function flushPullBuffer() {
  if (pullFlushRaf) {
    cancelAnimationFrame(pullFlushRaf);
    pullFlushRaf = 0;
  }
  if (pullBuffer.length === 0) return;
  pullLogLines.value.push(...pullBuffer);
  pullBuffer = [];
  // 跟随中贴到最新，暂停时什么都不做（由 useLogAutoFollow 判定）
  pullFollow.onContentChange();
}

function closePullStream() {
  if (pullEventSource) {
    pullEventSource.close();
    pullEventSource = null;
  }
}

function handlePullDialogClose() {
  // 弹窗关掉之后，在途的日志查询回来不能再写状态（否则会盖到下一次拉取上）。
  // 递增会话号即可让 resolvePullOutcome 的守卫把结果整个丢弃。
  pullSession = ++pullSessionSeq;
  closePullStream();
  // 流已切断、不会再有新输出：冻结跟随态（弹窗隐藏期间 ResizeObserver 回调也就不会再去贴底）。
  // 重新打开只有 beginPullSession / reattachPullStream 两条路，都会 begin(true) 重置。
  pullFollow.end();
  // 这里刻意不动 pullRunning / pullingSubId / pullBaselineLogId / pullStopRequested：
  // 后端拉取还在跑，这四个值是重新打开弹窗时 reattachPullStream 恢复现场的依据。
}

// 桌面表格实例：勾选态保存在 el-table 内部，「取消选择」与批量删除完成后需同步清空复选框
const subTableRef = ref<any>(null);

async function handleBatchDelete() {
  // 确认之前就把 ids 拷下来，确认框里的数量与真正发出去的请求用同一份：
  // 确认框开着的时候，在途的 loadData 回来可能已把 selectedIds 裁小甚至裁空，
  // 事后再读它就会删掉与用户确认的不一样的集合，裁空时还会发出 batchDelete([]) 却提示「批量删除成功」。
  const ids = [...selectedIds.value];
  if (ids.length === 0) return;
  try {
    await ElMessageBox.confirm(
      `确定要删除选中的 ${ids.length} 个订阅吗？`,
      "批量删除",
      { type: "warning" },
    );
    await subscriptionApi.batchDelete(ids);
    ElMessage.success("批量删除成功");
    clearSelection();
    loadData();
  } catch {
    /* cancelled */
  }
}

function handleSelectionChange(rows: any[]) {
  selectedIds.value = rows.map((r) => r.id);
}

// 批量栏「取消选择 / 取消」：同时清桌面 el-table 内部勾选态与移动端 selectedIds。
function clearSelection() {
  subTableRef.value?.clearSelection?.();
  selectedIds.value = [];
}

// 移动端批量栏「全选 / 取消全选」：只作用于当前页可见的卡片。
// 取消全选后选择为空，批量栏随之退回普通工具栏，与点「取消」效果一致。
function toggleSelectAllOnPage() {
  const pageIds = filteredSubList.value.map((row) => row.id as number);
  if (allSelectedOnPage.value) {
    const pageIdSet = new Set(pageIds);
    selectedIds.value = selectedIds.value.filter((id) => !pageIdSet.has(id));
  } else {
    selectedIds.value = [...new Set([...selectedIds.value, ...pageIds])];
  }
}

function isSelected(id: number) {
  return selectedIdSet.value.has(id);
}

function toggleSelected(id: number, checked: boolean | string | number) {
  const next = new Set(selectedIds.value);
  if (checked) {
    next.add(id);
  } else {
    next.delete(id);
  }
  selectedIds.value = [...next];
}

async function openLogs(subId: number) {
  logSubId.value = subId;
  logPage.value = 1;
  showLogDialog.value = true;
  await loadLogs();
}

async function loadLogs() {
  logLoading.value = true;
  try {
    const res = await subscriptionApi.logs(logSubId.value, {
      page: logPage.value,
      page_size: 10,
    });
    logList.value = res.data || [];
    logTotal.value = res.total || 0;
  } catch (err: any) {
    ElMessage.error(err?.response?.data?.error || "加载日志失败");
  } finally {
    logLoading.value = false;
  }
}

function getStatusTag(status: number) {
  return status === 0 ? "success" : "danger";
}

function getStatusText(status: number) {
  return status === 0 ? "正常" : "失败";
}

// 打开「SSH 密钥管理」弹窗并刷新列表。桌面工具栏的「SSH 密钥」按钮与移动端「设置」下拉里的同名项共用。
function openSSHKeyManage() {
  showSSHKeyManageDialog.value = true;
  loadSSHKeys();
}

// 移动端工具栏「设置」下拉：桌面那两颗按钮（SSH 密钥 / 订阅设置）在手机上收进同一个齿轮菜单，
// 第一行才放得下「搜索框 + 新建 + 设置」而不换行。
function onMobileSettingsCommand(command: string | number | object) {
  if (command === "settings") handleOpenSettings();
  else if (command === "ssh-keys") openSSHKeyManage();
}

function openCreateSSHKey() {
  isCreateSSHKey.value = true;
  sshKeyForm.value = { id: 0, name: "", private_key: "" };
  showSSHKeyDialog.value = true;
}

function openEditSSHKey(row: any) {
  isCreateSSHKey.value = false;
  sshKeyForm.value = { id: row.id, name: row.name, private_key: "" };
  showSSHKeyDialog.value = true;
}

async function handleSaveSSHKey() {
  if (!sshKeyForm.value.name.trim()) {
    ElMessage.warning("名称不能为空");
    return;
  }
  if (isCreateSSHKey.value && !sshKeyForm.value.private_key.trim()) {
    ElMessage.warning("私钥不能为空");
    return;
  }
  // 校验通过后才置位，复位放 finally
  sshKeySaving.value = true;
  try {
    const data: any = { name: sshKeyForm.value.name };
    if (sshKeyForm.value.private_key) {
      data.private_key = sshKeyForm.value.private_key;
    }
    if (isCreateSSHKey.value) {
      await sshKeyApi.create(data);
      ElMessage.success("创建成功");
    } else {
      await sshKeyApi.update(sshKeyForm.value.id, data);
      ElMessage.success("更新成功");
    }
    showSSHKeyDialog.value = false;
    loadSSHKeys();
  } catch (err: any) {
    // ssh_keys.name 现在是唯一索引，后端把冲突翻译成了面向用户的中文 400
    // （创建与改名两条路径都会返回「同名 SSH 密钥已存在」）。
    // 这里必须优先取后端文案，否则那条提示在 UI 上是死的，用户只会看到笼统的「创建失败」。
    ElMessage.error(
      err?.response?.data?.error ||
        (isCreateSSHKey.value ? "创建失败" : "更新失败"),
    );
  } finally {
    sshKeySaving.value = false;
  }
}

async function handleDeleteSSHKey(id: number) {
  try {
    await ElMessageBox.confirm("确定要删除该 SSH 密钥吗？", "确认删除", {
      type: "warning",
    });
    await sshKeyApi.delete(id);
    ElMessage.success("删除成功");
    loadSSHKeys();
  } catch {
    /* cancelled */
  }
}

function viewLogDetail(log: any) {
  logDetailContent.value = log.content || "(无日志内容)";
  showLogDetail.value = true;
}
</script>

<template>
  <div
    ref="pageRootRef"
    class="subscriptions-page dd-fixed-page dd-page-hide-heading"
  >
    <!--
      移动端工具栏（v3.3.1，issue #143）：与桌面拆成两支，桌面走下面的 v-else、结构原样保留。
      第一行：常态是「搜索框 + 新建订阅 + 设置」；勾选卡片后整行换成批量栏。
        移动端是普通文档流，两支直接 v-if 互换，不走桌面 §4.2 的 visibility 叠放；
        两支高度都是 32px、外边距相同，切换时下面的内容不跳。
      第二行：类型分段控件，与页面内容同宽（左右各留 12px 页面留白）、放不下时横向滑动。
        v3.3.2 起不再贴屏幕边缘：贴边时横滑到尽头会吃掉 iOS 的边缘返回手势，留白后才让得开。
        它是「已禁用」唯一的筛选入口，所以保留。
    -->
    <div v-if="isMobile" class="subscription-mobile-toolbar dd-rise" style="--dd-i: 0">
      <div v-if="selectedIds.length === 0" class="dd-mobile-toolbar">
        <el-input
          v-model="keyword"
          placeholder="搜索订阅名称或 URL"
          clearable
          @keyup.enter="handleSearch"
          @clear="handleSearch"
        >
          <template #prefix
            ><el-icon><Search /></el-icon
          ></template>
        </el-input>
        <el-button
          type="primary"
          class="dd-icon-only-btn"
          :icon="Plus"
          aria-label="新建订阅"
          title="新建订阅"
          @click="openCreate"
        />
        <!-- 桌面的「SSH 密钥」「订阅设置」两颗按钮在这里收进齿轮下拉，菜单观感与操作列 Split Button 一致 -->
        <el-dropdown
          trigger="click"
          placement="bottom-end"
          popper-class="dd-split-button__popper"
          @command="onMobileSettingsCommand"
        >
          <el-button
            class="dd-icon-only-btn"
            :icon="Setting"
            aria-label="设置"
            title="设置"
          />
          <template #dropdown>
            <el-dropdown-menu>
              <el-dropdown-item command="settings">订阅设置</el-dropdown-item>
              <el-dropdown-item command="ssh-keys">SSH 密钥</el-dropdown-item>
            </el-dropdown-menu>
          </template>
        </el-dropdown>
      </div>
      <!-- 批量栏：全选 / 取消全选 → 删除 → 取消（放最后）。不显示「已选 N 项」，数量在删除确认框里写明。
           「删除」用实心 danger：订阅没有批量启停接口，这一栏只有这一个不可逆操作。 -->
      <div v-else class="dd-scroll-row dd-mobile-batch-bar">
        <el-button :icon="Check" @click="toggleSelectAllOnPage">
          {{ allSelectedOnPage ? "取消全选" : "全选" }}
        </el-button>
        <el-button type="danger" :icon="Delete" @click="handleBatchDelete">
          删除
        </el-button>
        <el-button :icon="Close" @click="clearSelection">取消</el-button>
      </div>
      <div class="status-tabs dd-scroll-row dd-mobile-bleed">
        <button
          v-for="tab in typeTabs"
          :key="tab.value"
          :class="['status-tab', { active: typeFilter === tab.value }]"
          @click="handleTypeFilter(tab.value)"
        >
          {{ tab.label }}
        </button>
      </div>
    </div>

    <div v-else class="toolbar dd-rise" style="--dd-i: 0">
      <!-- 按 design-system §4.2：左槽 1×1 网格叠放筛选区与批量栏，两支常驻 DOM 靠 visibility 切换，
           勾选前后工具栏高度恒定，消除表格与右区按钮跳动。 -->
      <div class="toolbar__left">
        <div class="toolbar__filters" :class="{ 'is-swapped-out': selectedIds.length > 0 }">
          <div class="status-tabs">
            <button
              v-for="tab in typeTabs"
              :key="tab.value"
              :class="['status-tab', { active: typeFilter === tab.value }]"
              @click="handleTypeFilter(tab.value)"
            >
              {{ tab.label }}
            </button>
          </div>
          <el-input
            v-model="keyword"
            placeholder="搜索订阅名称或 URL"
            clearable
            class="toolbar__search"
            @keyup.enter="handleSearch"
            @clear="handleSearch"
          >
            <template #prefix
              ><el-icon><Search /></el-icon
            ></template>
          </el-input>
        </div>
        <div class="batch-actions" :class="{ 'is-swapped-out': selectedIds.length === 0 }">
          <span class="batch-actions__count">已选 {{ selectedIds.length }} 项</span>
          <el-button
            type="danger"
            :title="`批量删除已勾选的 ${selectedIds.length} 条订阅`"
            @click="handleBatchDelete"
          >
            批量删除
          </el-button>
          <el-button @click="clearSelection">取消选择</el-button>
        </div>
      </div>
      <div class="toolbar__right">
        <el-button @click="openSSHKeyManage" title="SSH 密钥管理">
          <el-icon><Key /></el-icon> SSH 密钥
        </el-button>
        <el-button @click="handleOpenSettings" title="订阅设置">
          <el-icon><Setting /></el-icon>
        </el-button>
        <el-button type="primary" @click="openCreate">
          <el-icon><Plus /></el-icon> 新建订阅
        </el-button>
      </div>
    </div>

    <div v-if="isMobile" class="dd-mobile-list dd-rise" style="--dd-i: 1">
      <div
        v-for="row in filteredSubList"
        :key="row.id"
        class="dd-mobile-card"
        :class="{ 'subscription-card--disabled': !row.enabled }"
      >
        <!--
          首行（v3.3.1，issue #143）：复选框 → 启用圆点 → 名称 → 类型标签 → 可选标签组 → 右上角「···」。
          URL 与分支不再上卡片（issue 的保留清单里没有它们），需要时从「···」→「编辑」里看。
          启用圆点（#133）与桌面名称格同一套东西，a11y 三件套见 .sub-status-dot 的注释；
          名称改成单行省略后首行固定一行高，圆点直接跟着 align-items:center 居中，不再需要钉在首行的 margin-top。
        -->
        <div class="dd-mobile-card__head">
          <el-checkbox
            :model-value="isSelected(row.id)"
            @change="toggleSelected(row.id, $event)"
          />
          <span
            class="sub-status-dot"
            :class="{ 'is-enabled': row.enabled }"
            role="img"
            :title="row.enabled ? '已启用' : '已禁用'"
            :aria-label="row.enabled ? '已启用' : '已禁用'"
          />
          <span
            class="dd-mobile-card__name subscription-card__name"
            :title="row.name"
            >{{ row.name }}</span
          >
          <!--
            标签用桌面那套缩写（Git / 文件、覆盖 / 保留），全称挂 title，窄屏上能多放一枚。
            类型标签单独放在标签组外、不参与收缩，保证每张卡至少看得到一枚标签；
            其余可选标签进 .subscription-card__tags，只吃名称和类型标签用剩的宽度，
            放不下的整枚隐藏、不换行、不做 +N，名称优先：原理见样式里 .subscription-card__tags 的注释。
            覆盖策略同桌面，只在「不是跟随全局」时才显示。
          -->
          <el-tag
            class="subscription-card__type-tag"
            size="small"
            :type="row.type === 'git-repo' ? 'primary' : 'warning'"
            :title="row.type === 'git-repo' ? 'Git 仓库' : '单文件'"
          >
            {{ row.type === "git-repo" ? "Git" : "文件" }}
          </el-tag>
          <div class="dd-mobile-card__head-tags subscription-card__tags">
            <el-tag
              v-if="row.overwrite_mode === 'force'"
              size="small"
              type="warning"
              title="强制覆盖：该订阅强制覆盖本地脚本文件，不跟随全局设置"
            >
              覆盖
            </el-tag>
            <el-tag
              v-else-if="row.overwrite_mode === 'preserve'"
              size="small"
              type="info"
              title="保留本地修改：该订阅拉取时保留本地脚本改动，不跟随全局设置"
            >
              保留
            </el-tag>
            <!--
              同步任务三态只标 disabled 这一档：全局默认是「开」，enabled 与绝大多数订阅的
              实际行为一致，标出来全是噪音；「这条订阅不建/不删任务」才是意料之外、
              值得在列表里一眼看到的状态。inherit 同理不标（写法照上面的 overwrite_mode）。
              桌面表格刻意不加这两个标签：名称列 min-width 136 扣掉名称前状态圆点那 16px 只剩 120，
              再挂标签会把订阅名挤到第二行，见下面表格里那段宽度测算。
            -->
            <el-tag
              v-if="row.auto_add_task_mode === 'disabled'"
              size="small"
              type="info"
              title="自动建任务：该订阅强制关闭，不跟随全局设置"
            >
              不建任务
            </el-tag>
            <el-tag
              v-if="row.auto_del_task_mode === 'disabled'"
              size="small"
              type="info"
              title="自动删任务：该订阅强制关闭，不跟随全局设置"
            >
              不删任务
            </el-tag>
          </div>
          <!-- 「···」：拉取日志 / 编辑 / 删除，与桌面操作列 Split Button 的菜单同一份数组 -->
          <DdMoreMenu
            :items="subActionItems"
            @command="(key: string) => onSubAction(key, row)"
          />
        </div>

        <!-- 字段区：标签与值横排。「启用」字段早在 #133 就改由名称前的圆点 + 末行按钮承载 -->
        <div class="dd-mobile-card__rows">
          <div class="dd-mobile-card__row">
            <span class="dd-mobile-card__row-label">定时拉取</span>
            <span
              class="dd-mobile-card__row-value"
              :class="{ 'dd-mono': row.schedule }"
              :title="row.schedule || undefined"
              >{{ row.schedule || "手动拉取" }}</span
            >
          </div>
          <div class="dd-mobile-card__row">
            <span class="dd-mobile-card__row-label">最后拉取</span>
            <!-- issue #144 / v3.3.2：补 dd-mono，与上一行「定时拉取」及定时任务卡的时间值统一成等宽。
                 不改用本页的 .time-text（12px，且它还服务 SSH 密钥弹窗）；dd-mono 只给 font-family，
                 字号仍继承 .dd-mobile-card__row 的 13px。空值时 formatDateTime 回落 '-'，等宽渲染无副作用。 -->
            <span class="dd-mobile-card__row-value dd-mono">{{
              formatDateTime(row.last_pull_at)
            }}</span>
          </div>
          <!-- issue #163：下次拉取时间由后端按调度器同一口径现算（next_pull_at），
               禁用、没设定时或规则解析不了时不下发，formatDateTime 回落 '-'。 -->
          <div class="dd-mobile-card__row">
            <span class="dd-mobile-card__row-label">下次拉取</span>
            <span class="dd-mobile-card__row-value dd-mono">{{
              formatDateTime(row.next_pull_at)
            }}</span>
          </div>
        </div>

        <!--
          末行：左侧「状态」（拉取结果），右侧「拉取」「禁用 / 启用」。与定时任务卡「上次结果在左、操作在右下角」同构。
          日志 / 编辑 / 删除收进首行的「···」：不可逆的删除不再和高频按钮并排，误点代价最大的那颗离拇指最远。
          「禁用 / 启用」的 type/plain 与桌面操作列那颗逐字一致，直接复用 handleToggle（确认框、报错、loadData 都在里面）。
          按钮用 default 尺寸（32px），与工具栏图标按钮等高。
        -->
        <div class="dd-mobile-card__footer">
          <div class="dd-mobile-card__footer-main subscription-card__status">
            <span class="dd-mobile-card__row-label">状态</span>
            <!-- 与桌面状态列同一套过渡，key 同样绑状态值 -->
            <Transition name="dd-status-switch" mode="out-in">
              <el-tag
                :key="row.status"
                size="small"
                :type="getStatusTag(row.status)"
                >{{ getStatusText(row.status) }}</el-tag
              >
            </Transition>
          </div>
          <div class="dd-mobile-card__footer-actions">
            <el-button type="success" @click="handlePull(row)">拉取</el-button>
            <el-button
              :type="row.enabled ? 'danger' : 'default'"
              :plain="row.enabled"
              @click="handleToggle(row)"
              >{{ row.enabled ? "禁用" : "启用" }}</el-button
            >
          </div>
        </div>
      </div>

      <el-empty
        v-if="!loading && filteredSubList.length === 0"
        description="暂无订阅"
      />
    </div>

    <div v-else class="table-card dd-spot dd-rise" style="--dd-i: 1">
      <el-table
        ref="subTableRef"
        :data="filteredSubList"
        v-loading="loading"
        @selection-change="handleSelectionChange"
        :row-class-name="getRowClassName"
        style="width: 100%"
        :header-cell-style="subTableHeaderStyle"
      >
        <el-table-column type="selection" width="40" />
        <!-- min-width 120 → 136（#133）：名称前多了一枚 8px 状态圆点，外加圆点与名称之间的一份 gap 8px，
             合计 +16px（与环境变量页名称列 188 → 204 那笔账同一口径）。不补回来的话订阅名的可见宽度会净减 16px，
             省略号提前出现、标签也更容易被挤到第二行。下面标签那段测算说的是扣掉圆点之后的 120，不受影响。 -->
        <el-table-column prop="name" label="名称" min-width="136">
          <template #default="{ row }">
            <div class="sub-name-cell">
              <!-- 圆点与名称必须包成一组（.sub-name-main）：外层 .sub-name-cell 是 flex-wrap，
                   两者若是平级 flex 项，长名称会整块掉到第二行、把圆点孤零零留在第一行。
                   圆点只表达订阅自身的启用开关（二态）；右边「状态」列是拉取结果（正常绿 / 失败红），两者含义不同，
                   同一行出现「红点 + 正常绿标」时靠圆点的 title / aria-label 区分，见 .sub-status-dot 的注释。
                   订阅级「自动建 / 删任务」三态与它正交，不揉进圆点（一枚点表达不了 2 个字段 × 3 档）。 -->
              <span class="sub-name-main">
                <span
                  class="sub-status-dot"
                  :class="{ 'is-enabled': row.enabled }"
                  role="img"
                  :title="row.enabled ? '已启用' : '已禁用'"
                  :aria-label="row.enabled ? '已启用' : '已禁用'"
                />
                <!-- 单行省略，全称靠 title 兜底（design-system §4.1：省略后必须挂 title） -->
                <span class="sub-name-text" :title="row.name">{{
                  row.name
                }}</span>
              </span>
              <el-tag
                size="small"
                :type="row.type === 'git-repo' ? 'primary' : 'warning'"
                round
              >
                {{ row.type === "git-repo" ? "Git" : "文件" }}
              </el-tag>
              <!--
                覆盖策略只在「不是跟随全局」时才挂标签：它是低频配置，
                绝大多数订阅都是 inherit，常驻一列会白占桌面表格本就紧张的宽度（操作列已 fixed）。

                文案在桌面端缩成「覆盖 / 保留」，与同格的「Git / 文件」一个风格（v3.3.1 起移动端卡片也用这套缩写，
                同样靠 title 看全称）：这一列 min-width 136 扣掉名称前状态圆点那 16px，留给「名称 + 标签」的只有 120，
                而「Git」+「强制覆盖」两个 small round 标签加 gap 粗算已经 124px、比这 120 本身还宽，订阅名一个字都放不下，
                force/preserve 那几行会靠 .sub-name-cell 的 flex-wrap 掉到第二行、行高比 inherit 行高一截。
                缩写后两个标签约 96px，常规订阅名能和标签同排。
                全称走 title 兜底（设计规范：省略后必须挂 title）。
                注意标签里不要塞 el-icon —— EP 会把 .el-tag 内的任意 .el-icon 当成关闭按钮，
                图标与文字会被拆成两行（见 design-system.md 的 Gotcha）。
              -->
              <el-tag
                v-if="row.overwrite_mode === 'force'"
                size="small"
                type="warning"
                round
                title="强制覆盖：该订阅强制覆盖本地脚本文件，不跟随全局设置"
              >
                覆盖
              </el-tag>
              <el-tag
                v-else-if="row.overwrite_mode === 'preserve'"
                size="small"
                type="info"
                round
                title="保留本地修改：该订阅拉取时保留本地脚本改动，不跟随全局设置"
              >
                保留
              </el-tag>
            </div>
          </template>
        </el-table-column>
        <el-table-column
          prop="url"
          label="URL"
          min-width="160"
          show-overflow-tooltip
        >
          <template #default="{ row }">
            <span class="url-text">{{ row.url }}</span>
          </template>
        </el-table-column>
        <el-table-column prop="branch" label="分支" width="80" />
        <el-table-column prop="schedule" label="定时拉取" width="110">
          <template #default="{ row }">
            <code v-if="row.schedule" class="cron-text">{{
              row.schedule
            }}</code>
            <span v-else class="text-muted">手动</span>
          </template>
        </el-table-column>
        <el-table-column label="状态" width="70" align="center">
          <template #default="{ row }">
            <!--
              这一列是全页流转最频繁的状态：拉取的 SSE 收到 done 后会直接 loadData()，
              正常 ⇄ 失败 就地翻牌。硬切的话用户正盯着日志弹窗，回到列表根本看不出哪一行变了。
              out-in 淡出淡入补一次「它刚刚变了」的提示。

              key 必须绑 row.status（状态值本身）——绑 row.id 的话行没换、key 不变，
              过渡永远不会触发，等于白写。
            -->
            <Transition name="dd-status-switch" mode="out-in">
              <el-tag
                :key="row.status"
                size="small"
                :type="getStatusTag(row.status)"
                round
                >{{ getStatusText(row.status) }}</el-tag
              >
            </Transition>
          </template>
        </el-table-column>
        <!-- 「拉取时间」（issue #163）：原「最后拉取」列加一行下次拉取，上下两行「上次 / 下次」，写法同定时任务页的「运行时间」。
             next_pull_at 由后端按调度器同一口径现算，禁用 / 没设定时 / 规则解析不了时不下发，显示 -。
             宽度：前缀 12px×2=24 + 间距 6 + `YYYY-MM-DD HH:mm:ss` 19 个等宽字符（JetBrains Mono 12px 约 7.2px/字，≈137）
             ≈ 167px，加 .cell 左右各 12px 内边距 = 191，取 192；两行都 nowrap 不折行。 -->
        <el-table-column label="拉取时间" width="192">
          <template #default="{ row }">
            <div class="pull-time-cell">
              <div class="pull-time-cell__line">
                <span class="pull-time-cell__label">上次</span>
                <span v-if="row.last_pull_at" class="time-text">{{
                  formatDateTime(row.last_pull_at)
                }}</span>
                <span v-else class="text-muted">-</span>
              </div>
              <div class="pull-time-cell__line">
                <span class="pull-time-cell__label">下次</span>
                <span v-if="row.next_pull_at" class="time-text">{{
                  formatDateTime(row.next_pull_at)
                }}</span>
                <span v-else class="text-muted">-</span>
              </div>
            </div>
          </template>
        </el-table-column>
        <!--
          原来是「拉取 / 日志 / 编辑 / 删除」四个 text 按钮平铺，吃掉 220px 列宽；
          而且「删除」和「拉取」同字号并排，误点的代价却天差地别。
          改成 Split Button：主体是最高频、且还有一道确认框兜底的「拉取」，
          其余收进菜单，删除标红并用分隔线隔开。

          #133 起这一格是两个元素（与环境变量页、定时任务页同一形态）：左边 Split Button（拉取 ▾），
          右边外置的「禁用 / 启用」按钮 —— 原来那一整列「启用」（el-switch，width 60）已删，这是唯一的切换入口。

          列宽 130 → 172（与环境变量页、定时任务页取同一个值）：
          EP 的 .el-table .cell 是 padding:0 12px + overflow:hidden，可用内容宽 = 172 - 24 = 148px。
          实测口径（size="small"，见 design-system §5「split-button 的 caret 是 32px」）：
            主体 = 中文字数 × 12（字宽）+ 22（EP small 的 padding 5px 11px）+ 2（边框）
            caret = 32，且不受 size 影响；el-button-group 组内相邻按钮还有 -1px 负边距
          「拉取」= 2×12+22+2 = 48 → 48+32-1 = 79px；「禁用」/「启用」同为 2 字 = 48px；
          中间 gap 4px（.action-btns 已把 EP 的 `.el-button + .el-button` 外边距清零，间距只由 gap 决定）。
          合计 79 + 4 + 48 = 131px，148 - 131 = 17px 余量。
          ⚠️ 本处旧注释按「caret 半边 24px」估算，是错的（会系统性偏小约 14px），别再照它把列宽收窄回去。
          按钮组一旦超出可用宽，.cell 会变成可滚动容器，点右侧按钮时整行会被滚偏且不复位。

          整表最小宽度账：删掉「启用」列 −60、操作列 +42、名称列 +16（状态圆点），
          各列最小宽度合计 920 → 918px，窄窗口的横向溢出不会比改动前更坏。
        -->
        <el-table-column label="操作" width="172" fixed="right" align="center">
          <template #default="{ row }">
            <div class="action-btns">
              <DdSplitButton
                label="拉取"
                type="success"
                size="small"
                :items="subActionItems"
                @click="handlePull(row)"
                @command="(key: string) => onSubAction(key, row)"
              />
              <!-- 「启用 / 禁用」外置成一级按钮，位置、type/plain 组合与环境变量页操作列逐字一致：
                   禁用 = danger + plain（白底红字红描边），启用 = default（EP 白底）。
                   直接复用 handleToggle —— 二次确认、错误提示、loadData 全在里面，不要再写一份。

                   ⚠️ design-system §4.2 的内容约定是「危险按钮不放最外侧」。这里照抄环境变量页（#109-4）
                   那次有意识的让步：handleToggle 带 ElMessageBox 二次确认，点错的代价是按一下 Esc；
                   真正不可逆的「删除」仍然待在 Split 菜单里（danger + divided），没有被提到外侧。 -->
              <el-button
                size="small"
                :type="row.enabled ? 'danger' : 'default'"
                :plain="row.enabled"
                @click="handleToggle(row)"
              >
                {{ row.enabled ? "禁用" : "启用" }}
              </el-button>
            </div>
          </template>
        </el-table-column>
      </el-table>
    </div>

    <div class="pagination-bar">
      <span class="pagination-total">共 {{ total }} 条数据</span>
      <el-pagination
        v-model:current-page="page"
        v-model:page-size="pageSize"
        :total="total"
        :page-sizes="[20, 50, 100]"
        :layout="isMobile ? 'prev, pager, next' : 'sizes, prev, pager, next'"
        @current-change="handlePageChange"
        @size-change="handlePageSizeChange"
      />
    </div>

    <el-dialog
      v-model="showEditDialog"
      :title="isCreate ? '新建订阅' : '编辑订阅'"
      width="800px"
      :fullscreen="dialogFullscreen"
    >
      <!--
        新建与编辑共用这一个弹窗，只有「一键识别」随 isCreate 出现（v3.2.9 精简）。
        结构：
          - 基本区：名称 / 类型 / URL / 定时拉取，加上与 ql repo 第 2~4 个参数一一对应的白名单 / 黑名单 / 依赖规则
            （粘贴命令识别后要一眼看得到；后端 400 点名这三项时也不用先去展开什么）；
          - 默认折叠的「高级设置」：分支、目录、鉴权、完整检出、覆盖拉取、自动建 / 删任务、钩子这些不常改的项。
            折叠行右侧的 advancedSummary 列出高级区里已经有值的项，免得一键识别 / 存量订阅的值藏着看不见。
        ⚠️ 说明文字的口径：弹窗里不放常驻说明段落。完整匹配规则见 README 订阅管理一节与接口文档（views/api-docs/apiData.ts），
        弹窗只放一两句气泡（DdFieldHelp，点标签旁的「?」弹出），拉取日志的 [提示] 负责当场解释。
        v3.2.9 前这里常驻约 1500 字说明，用户反馈「文字说明太多」才删掉的；以后字段需要解释，
        也只在标签旁加气泡、讲清用途与最容易踩的坑，别把「写全条件」的长段落加回表单里。
      -->
      <el-form
        class="subscription-form"
        :model="editForm"
        :label-width="dialogFullscreen ? 'auto' : '104px'"
        :label-position="dialogFullscreen ? 'top' : 'right'"
      >
        <el-form-item v-if="isCreate" class="form-item--full">
          <template #label>
            <DdFieldHelp label="一键识别">
              粘贴 ql repo / ql raw
              命令，自动填好名称、URL、白名单、黑名单、依赖规则、分支等，并把「自动建任务」设为强制开启。识别后可在「高级设置」里核对。
            </DdFieldHelp>
          </template>
          <div style="display: flex; gap: 8px; width: 100%">
            <el-input
              v-model="qlCommand"
              placeholder="粘贴 ql repo / ql raw 命令或仓库链接"
              clearable
              @keyup.enter="parseQLCommand"
            />
            <el-button type="primary" @click="parseQLCommand">识别</el-button>
          </div>
        </el-form-item>
        <el-form-item label="名称">
          <el-input v-model="editForm.name" placeholder="订阅名称" />
        </el-form-item>
        <el-form-item label="类型">
          <el-radio-group v-model="editForm.type">
            <el-radio value="git-repo">Git 仓库</el-radio>
            <el-radio value="single-file">单文件</el-radio>
          </el-radio-group>
        </el-form-item>
        <el-form-item label="URL" class="form-item--full">
          <el-input
            v-model="editForm.url"
            :placeholder="
              editForm.type === 'git-repo'
                ? '如 https://github.com/owner/repo.git'
                : '脚本文件的下载链接'
            "
          />
        </el-form-item>
        <!--
          白名单 / 黑名单 / 依赖规则：对应 ql repo 的第 2/3/4 个参数，一键识别后要一眼看得到，所以留在基本区。
          三个字段共用同一套匹配口径（#129，与后端 subscription_patterns.go 同源）：
            - 只在顶层的 , 或 | 处拆成片段，括号 / 方括号里的 , 与 | 属于正则本身、不拆；
            - 普通片段按「子串包含」匹配（行为与改动前逐字节一致），片段命中目录名时目录下全部文件一并命中；
            - 含 ^ $ ( ) [ ] { } ? \ 任一字符，或含 .* / .+ 的片段按正则（Go RE2，与青龙 grep -E 同口径）
              匹配仓库相对路径，不锚定；单独一个 + 不算触发字符，所以 jd_*.js、.github 这类写法仍按子串包含；
            - 「全部」类写法（* / ** / *.* / .* / all / 全部）与「含空格或中文 = 文字备注」的判定照旧先跑；
            - git 的 sparse-checkout 表达不了正则：依赖规则、（没填「指定子目录」时的）白名单出现正则片段会改为整仓检出，
              黑名单的正则片段只能保证不建任务、做不到不落盘。
          完整匹配规则见 README 订阅管理一节与接口文档，弹窗只放一两句气泡，拉取日志的 [提示] 负责当场解释
          （整仓检出、依赖规则被当作备注跳过、黑名单正则片段只挡建任务、零命中的常见原因，都在拉取日志里打）。
          别把上面这些条件写回表单的常驻说明——那正是 v3.2.9 前「说明太多」的来源。
          非法正则由后端在保存时回 400（点名字段、第几段与 RE2 报错并提示用 \ 转义），handleSave 原样展示后端文案，前端不重复校验。

          气泡口径对齐后端 isSubscriptionDependencyOnlyFile（白名单优先）：同时命中白名单的文件照常建任务，
          白名单留空时依赖规则不影响建任务——别把依赖规则气泡再压成「命中即不建任务」。
          白名单气泡也别写「命中即建任务」：建不建还要看「自动建任务」与脚本有没有声明 cron（#134）。

          桌面双列：白名单 | 黑名单、依赖规则 | 定时拉取。三个规则字段挨着放，手机单列时也保持 ql repo 的参数顺序。
        -->
        <el-form-item>
          <template #label>
            <DdFieldHelp label="白名单">
              命中的文件会拉取，并按「自动建任务」设置建任务，留空为全部。多个用 , 或 |
              分隔，按「包含」匹配；含 ^ $ ( ) [ ] { } ? \ 或 .* .+
              时按正则匹配，要按字面匹配请用 \ 转义。
            </DdFieldHelp>
          </template>
          <el-input
            v-model="editForm.whitelist"
            placeholder="如 jd_|jx_，留空为全部"
          />
        </el-form-item>
        <el-form-item>
          <template #label>
            <DdFieldHelp label="黑名单">
              命中的文件不拉取、不建任务（正则片段只保证不建任务）。写法同白名单。
            </DdFieldHelp>
          </template>
          <el-input v-model="editForm.blacklist" placeholder="如 backUp" />
        </el-form-item>
        <el-form-item>
          <template #label>
            <DdFieldHelp label="依赖规则">
              对应 ql repo 的第 4
              个参数：命中的文件拉取给脚本调用，没命中白名单的不建任务；白名单留空时不影响建任务。写法同白名单。
            </DdFieldHelp>
          </template>
          <el-input
            v-model="editForm.depend_on"
            placeholder="辅助库，如 sendNotify|utils"
          />
        </el-form-item>
        <el-form-item label="定时拉取">
          <el-input
            v-model="editForm.schedule"
            placeholder="如 0 */6 * * *，留空不自动拉取"
          />
        </el-form-item>

        <!--
          「高级设置」切换行。项目里没有 el-collapse 先例，这里用 link 按钮 + v-show 做轻量折叠；
          高级区用 v-show 而不是 v-if，折叠时字段照样挂着、v-model 与保存逻辑与展开时完全一样。
          showAdvanced 在 openCreate / openEdit 里复位成折叠；保存时后端点名折叠区字段会自动展开（revealAdvancedForError）。
          右侧摘要（advancedSummary）折叠与展开时都显示，展开收起时这一行的高度不跳。
        -->
        <div class="subscription-form__advanced-toggle">
          <el-button
            link
            type="primary"
            :aria-expanded="String(showAdvanced)"
            aria-controls="subscription-advanced-fields"
            @click="showAdvanced = !showAdvanced"
          >
            <template #icon>
              <ArrowRight
                class="subscription-form__advanced-arrow"
                :class="{ 'is-expanded': showAdvanced }"
              />
            </template>
            高级设置
          </el-button>
          <span class="subscription-form__advanced-summary">{{
            advancedSummary
          }}</span>
        </div>
        <div
          v-show="showAdvanced"
          id="subscription-advanced-fields"
          class="subscription-form__advanced"
        >
          <el-form-item v-if="editForm.type === 'git-repo'" label="分支">
            <el-input v-model="editForm.branch" placeholder="留空使用默认分支" />
          </el-form-item>
          <el-form-item v-if="editForm.type === 'git-repo'">
            <template #label>
              <DdFieldHelp label="指定子目录">
                只检出这些子目录（依赖规则命中的文件另算），也只给其中的脚本建任务。逗号分隔多个，按路径匹配，不支持正则。
              </DdFieldHelp>
            </template>
            <el-input
              v-model="editForm.sub_path"
              placeholder="如 scripts,utils，留空为全部"
            />
          </el-form-item>
          <!-- 保存目录与别名挨着放：Git 仓库的目录名依次取 保存目录 → 别名 → 仓库名，
               单文件订阅的目录取保存目录（空则 downloads）、文件名取别名（空则 URL 末段）。
               以前别名夹在「仓库鉴权」和「SSH 密钥」之间，把鉴权那一组拆开了。 -->
          <el-form-item>
            <template #label>
              <DdFieldHelp label="保存目录">
                scripts 下的子目录。留空时：Git 仓库用别名或仓库名，单文件用 downloads。
              </DdFieldHelp>
            </template>
            <el-input
              v-model="editForm.save_dir"
              placeholder="如 owner_repo，留空自动取名"
            />
          </el-form-item>
          <el-form-item>
            <template #label>
              <DdFieldHelp label="别名">
                单文件：保存的文件名。Git 仓库：保存目录留空时作为目录名。
              </DdFieldHelp>
            </template>
            <el-input v-model="editForm.alias" placeholder="可选" />
          </el-form-item>
          <el-form-item
            v-if="editForm.type === 'git-repo'"
            class="form-item--full"
          >
            <template #label>
              <DdFieldHelp label="仓库鉴权">
                私有仓库才需要，推荐用只读权限的 Token。
              </DdFieldHelp>
            </template>
            <el-radio-group v-model="editForm.auth_type">
              <el-radio value="">无鉴权</el-radio>
              <el-radio value="ssh">SSH 密钥</el-radio>
              <el-radio value="token">Access Token</el-radio>
            </el-radio-group>
          </el-form-item>
          <el-form-item
            v-if="editForm.type === 'git-repo' && editForm.auth_type === 'ssh'"
            label="SSH 密钥"
          >
            <el-select
              v-model="editForm.ssh_key_id"
              placeholder="选择 SSH 密钥"
              clearable
              style="width: 100%"
            >
              <el-option
                v-for="key in sshKeys"
                :key="key.id"
                :label="key.name"
                :value="key.id"
              />
            </el-select>
          </el-form-item>
          <el-form-item
            v-if="editForm.type === 'git-repo' && editForm.auth_type === 'token'"
          >
            <template #label>
              <DdFieldHelp label="鉴权用户名">
                GitHub 留空；Gitee 填用户名；GitLab 填 oauth2 或 private-token。
              </DdFieldHelp>
            </template>
            <el-input
              v-model="editForm.auth_username"
              placeholder="留空默认 x-access-token"
            />
          </el-form-item>
          <el-form-item
            v-if="editForm.type === 'git-repo' && editForm.auth_type === 'token'"
            label="Access Token"
          >
            <el-input
              v-model="editForm.auth_token"
              type="password"
              show-password
              :placeholder="
                editForm.has_auth_token
                  ? '已保存，留空不修改'
                  : '粘贴访问令牌（建议只读权限）'
              "
            />
          </el-form-item>
          <!--
            完整检出：开启后跳过 sparse-checkout、整仓拉取。只对 git 仓库出现，理由同下面的「覆盖拉取」：
            单文件订阅压根没有 clone / sparse-checkout 这一步，显示出来只会让人以为它有用。
            开不开什么时候没区别（子目录 / 白名单 / 黑名单都空时本来就整仓；正则片段已触发整仓时，
            差别只在黑名单普通片段命中的文件落不落盘）属于完整规则，见 README 订阅管理一节与接口文档；
            气泡只讲用途与代价，别把这些条件写回表单。
          -->
          <el-form-item v-if="editForm.type === 'git-repo'">
            <template #label>
              <DdFieldHelp label="完整检出">
                拉取整个仓库（可能很大），供脚本读取源码、配置等其它文件；不改变建任务的范围。
              </DdFieldHelp>
            </template>
            <el-switch
              v-model="editForm.full_checkout"
              inline-prompt
              active-text="开"
              inactive-text="关"
            />
          </el-form-item>
          <!--
            覆盖拉取策略（订阅级三态）。只对 git 仓库出现——单文件订阅没有工作区，
            后端在拉取分支里也压根不看这个值，显示出来只会让人以为它有用。
            用单选而不是开关：开关只有两态，表达不了「跟随全局」这个默认档。
          -->
          <el-form-item
            v-if="editForm.type === 'git-repo'"
            class="form-item--full"
          >
            <template #label>
              <DdFieldHelp label="覆盖拉取">
                只影响脚本文件：强制覆盖会丢弃本地改动，保留则先暂存再恢复。不改任务配置，首次拉取不适用。
              </DdFieldHelp>
            </template>
            <el-radio-group v-model="editForm.overwrite_mode">
              <!--
                这里必须读 globalOverwriteDefault（只读展示值）而不是
                settingsForm.subscription_force_overwrite：后者是「订阅设置」弹窗里 el-switch 的
                编辑态，而那个弹窗的「取消」不重置表单，读它会把用户已经撤销的值当成服务端现状展示。
              -->
              <el-radio value="inherit"
                >跟随全局设置<template v-if="globalDefaultsLoaded"
                  >（当前：{{
                    globalOverwriteDefault ? "强制覆盖" : "保留本地修改"
                  }}）</template
                ></el-radio
              >
              <el-radio value="force">强制覆盖</el-radio>
              <el-radio value="preserve">保留本地修改</el-radio>
            </el-radio-group>
          </el-form-item>
          <!--
            同步定时任务的两组订阅级三态（#119）。挨着「覆盖拉取」放，因为它们是同一类
            「这条订阅要不要跟随全局设置」的开关，用单选也是同一个理由：开关只有两态，
            表达不了「跟随全局」这个默认档。

            ⚠️ 刻意**不加** v-if="editForm.type === 'git-repo'"：上面的完整检出与覆盖拉取
            只在 clone / sparse-checkout 这一步生效，单文件订阅走不到；而拉取后同步定时任务
            这一步对单文件订阅一样跑。加了 v-if 的表现是「单文件订阅界面上看不到开关」，
            用户完全没有办法为它单独关掉自动建任务。handleSave 里也同理不能跟着复位。

            气泡里不写全局开关的当前值：operator 读不到 /configs，「（当前：X）」对他们本来就不显示。
            「自动建任务」那句「未声明 cron 用默认 Cron 规则、留空不建」对齐 #134 起的后端口径
            （订阅设置「默认 Cron 规则」留空时，未声明 cron 的脚本不建任务），改后端口径时这里要跟着改。
          -->
          <el-form-item class="form-item--full">
            <template #label>
              <DdFieldHelp label="自动建任务">
                拉取后为新脚本建定时任务；脚本未声明 cron 时用订阅设置里的「默认 Cron
                规则」，留空则不建；已有任务不受影响。
              </DdFieldHelp>
            </template>
            <el-radio-group v-model="editForm.auto_add_task_mode">
              <!-- 「（当前：X）」同样只读 globalAutoAddDefault，理由见上面覆盖拉取那段注释 -->
              <el-radio value="inherit"
                >跟随全局设置<template v-if="globalDefaultsLoaded"
                  >（当前：{{ globalAutoAddDefault ? "开" : "关" }}）</template
                ></el-radio
              >
              <el-radio value="enabled">强制开启</el-radio>
              <el-radio value="disabled">强制关闭</el-radio>
            </el-radio-group>
          </el-form-item>
          <el-form-item class="form-item--full">
            <template #label>
              <DdFieldHelp label="自动删任务">
                订阅源删掉脚本后，删除对应的定时任务。怕误删手动建的任务就选强制关闭。
              </DdFieldHelp>
            </template>
            <el-radio-group v-model="editForm.auto_del_task_mode">
              <el-radio value="inherit"
                >跟随全局设置<template v-if="globalDefaultsLoaded"
                  >（当前：{{ globalAutoDelDefault ? "开" : "关" }}）</template
                ></el-radio
              >
              <el-radio value="enabled">强制开启</el-radio>
              <el-radio value="disabled">强制关闭</el-radio>
            </el-radio-group>
          </el-form-item>
          <el-form-item class="form-item--full">
            <template #label>
              <DdFieldHelp label="拉取前指令">
                非 0 退出会中断本次拉取并记为失败。首次拉取时 $SUB_DIR 为脚本根目录。
              </DdFieldHelp>
            </template>
            <el-input
              v-model="editForm.pre_script"
              type="textarea"
              :rows="3"
              placeholder="拉取前执行的 Shell，可用 $SUB_DIR、$SCRIPTS_DIR 等变量"
            />
          </el-form-item>
          <el-form-item class="form-item--full">
            <template #label>
              <DdFieldHelp label="拉取后钩子">
                非 0 退出会让本次拉取记为失败，并跳过任务同步。
              </DdFieldHelp>
            </template>
            <el-input
              v-model="editForm.hook_script"
              type="textarea"
              :rows="4"
              placeholder="拉取成功后执行的 Shell，可用 $SUB_DIR、$SCRIPTS_DIR 等变量"
            />
          </el-form-item>
        </div>
      </el-form>
      <template #footer>
        <el-button @click="showEditDialog = false">取消</el-button>
        <el-button
          type="primary"
          :loading="editSaving"
          :disabled="editSaving"
          @click="handleSave"
          >{{ isCreate ? "创建" : "保存" }}</el-button
        >
      </template>
    </el-dialog>

    <el-dialog
      v-model="showLogDialog"
      title="拉取日志"
      width="700px"
      :fullscreen="dialogFullscreen"
      class="sub-log-list-dialog"
    >
      <!-- 移动端全屏改用紧凑卡片列表，消除 400px 限高留白与 fixed=right 横向挤压；桌面保留表格 -->
      <div v-if="isMobile" class="sub-log-mobile-list" v-loading="logLoading">
        <div
          v-for="row in logList"
          :key="row.id || row.created_at"
          class="sub-log-mobile-card"
        >
          <div class="sub-log-mobile-card__head">
            <el-tag
              size="small"
              :type="row.status === 0 ? 'success' : 'danger'"
            >
              {{ row.status === 0 ? "成功" : "失败" }}
            </el-tag>
            <span class="sub-log-mobile-card__meta">
              {{ formatDateTime(row.created_at) }} · {{ formatDuration(row.duration) }}
            </span>
            <el-button
              size="small"
              text
              type="primary"
              @click="viewLogDetail(row)"
            >
              查看
            </el-button>
          </div>
          <div class="sub-log-mobile-card__content">
            {{ row.content || "暂无日志摘要" }}
          </div>
        </div>
        <el-empty
          v-if="!logLoading && logList.length === 0"
          description="暂无拉取日志"
        />
      </div>
      <el-table v-else :data="logList" v-loading="logLoading" max-height="400px">
        <el-table-column label="状态" width="80">
          <template #default="{ row }">
            <el-tag
              size="small"
              :type="row.status === 0 ? 'success' : 'danger'"
            >
              {{ row.status === 0 ? "成功" : "失败" }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column
          prop="content"
          label="内容"
          class-name="log-content-cell"
        />
        <el-table-column prop="duration" label="耗时" width="100">
          <template #default="{ row }">{{ formatDuration(row.duration) }}</template>
        </el-table-column>
        <el-table-column prop="created_at" label="时间" width="170">
          <template #default="{ row }">{{
            formatDateTime(row.created_at)
          }}</template>
        </el-table-column>
        <el-table-column label="操作" width="80" fixed="right" align="center">
          <template #default="{ row }">
            <el-button
              size="small"
              text
              type="primary"
              @click="viewLogDetail(row)"
              >查看</el-button
            >
          </template>
        </el-table-column>
      </el-table>
      <div
        class="pagination-container"
        v-if="logTotal > 10"
        style="margin-top: 12px"
      >
        <el-pagination
          v-model:current-page="logPage"
          :total="logTotal"
          :page-size="10"
          layout="prev, pager, next"
          @current-change="loadLogs"
        />
      </div>
      <!-- 移动端全屏时关闭入口放右下角（F2，issue #143）：有 footer 的弹窗在 ≤768 会隐藏右上角 ×（global.scss）。
           只在 dialogFullscreen 时提供插槽，桌面 EP 拿不到 $slots.footer、不渲染 footer，零变化。 -->
      <template v-if="dialogFullscreen" #footer>
        <el-button @click="showLogDialog = false">关闭</el-button>
      </template>
    </el-dialog>

    <el-dialog
      v-model="showLogDetail"
      title="日志详情"
      width="900px"
      :fullscreen="dialogFullscreen"
      class="sub-log-detail-dialog"
    >
      <pre
        class="pull-log-content dd-log-surface"
        style="min-height: 100px"
        v-html="logDetailContentHtml"
      ></pre>
      <!-- 同上：移动端全屏时的底部「关闭」（F2） -->
      <template v-if="dialogFullscreen" #footer>
        <el-button @click="showLogDetail = false">关闭</el-button>
      </template>
    </el-dialog>

    <el-dialog
      v-model="showPullLog"
      title="拉取日志"
      width="900px"
      :fullscreen="dialogFullscreen"
      class="sub-log-detail-dialog"
      :close-on-click-modal="false"
      @close="handlePullDialogClose"
    >
      <div ref="pullLogRef" class="pull-log-content dd-log-surface">
        <div
          v-for="(line, i) in pullLogLineHtmlList"
          :key="i"
          class="pull-log-line"
          v-html="line"
        ></div>
        <div v-if="pullRunning" class="pull-log-line pull-running">
          <LoadingMotion
            variant="dots"
            size="sm"
            tone="warning"
            :stacked="false"
          />
          <span>拉取中...</span>
        </div>
        <el-empty
          v-if="!pullRunning && pullLogLines.length === 0"
          description="暂无输出"
          :image-size="60"
        />
      </div>
      <template #footer>
        <!--
          状态指示靠 `margin-right: auto` 推到最左，这依赖 .el-dialog__footer 是 flex 容器
          （已在 global.scss 的 .el-dialog 块里统一改为 flex；Element Plus 原生只有 text-align:right，
          在那种 inline 上下文里 auto 外边距不产生推挤，所以这里换回 el-tag 会重新贴到按钮上）。
        -->
        <span
          v-if="pullStatusView.text"
          class="pull-status"
          :class="`is-${pullStatusView.tone}`"
        >
          <span class="pull-status__mark" aria-hidden="true"></span
          >{{ pullStatusView.text }}
        </span>
        <el-button v-if="pullRunning" type="danger" @click="handleStopPull"
          >停止</el-button
        >
        <el-button @click="showPullLog = false">关闭</el-button>
      </template>
    </el-dialog>

    <el-dialog
      v-model="showSettingsDialog"
      title="订阅设置"
      width="560px"
      :fullscreen="dialogFullscreen"
    >
      <el-form
        v-loading="settingsLoading"
        :label-width="dialogFullscreen ? 'auto' : '140px'"
        :label-position="dialogFullscreen ? 'top' : 'right'"
      >
        <el-form-item label="GitHub 镜像地址">
          <el-input
            v-model="settingsForm.github_mirror"
            :placeholder="DEFAULT_GITHUB_MIRROR"
          />
          <div class="settings-hint">
            留空使用默认值 {{ DEFAULT_GITHUB_MIRROR }}，拉取 GitHub
            仓库时自动加速
          </div>
        </el-form-item>
        <el-form-item label="自动添加定时任务">
          <el-switch
            v-model="settingsForm.auto_add_cron"
            inline-prompt
            active-text="开"
            inactive-text="关"
          />
          <div class="settings-hint">
            拉取后按脚本内容为新脚本自动创建定时任务（已有任务的名称和定时不改），未声明
            cron 的脚本只在填了下方「默认 Cron 规则」时才建。<b>未单独设置的订阅使用此默认值</b>，单个订阅可在编辑订阅的高级设置里选「强制开启 / 强制关闭」
          </div>
        </el-form-item>
        <el-form-item label="自动删除失效任务">
          <el-switch
            v-model="settingsForm.auto_del_cron"
            inline-prompt
            active-text="开"
            inactive-text="关"
          />
          <div class="settings-hint">
            订阅源删除脚本后，自动删除对应定时任务。<b>未单独设置的订阅使用此默认值</b>，单个订阅可在编辑订阅的高级设置里选「强制开启 / 强制关闭」
          </div>
        </el-form-item>
        <el-form-item label="覆盖拉取（默认）">
          <el-switch
            v-model="settingsForm.subscription_force_overwrite"
            inline-prompt
            active-text="开"
            inactive-text="关"
          />
          <div class="settings-hint">
            只作用于脚本文件：开启后拉取前丢弃本地改动，关闭则先暂存再恢复。<b>不影响任务配置</b>——订阅拉取从不修改已有任务的名称和定时。<b>未单独设置的订阅使用此默认值</b>，单个订阅可在编辑订阅的高级设置里选「强制覆盖 / 保留本地修改」
          </div>
        </el-form-item>
        <!--
          #134 起的口径：留空（出厂默认）时，未声明 cron 的脚本不建任务；填了合法规则才按它建启用任务。
          placeholder 刻意不写具体 cron：以前写着「0 9 * * *」，看起来像是留空时的默认值，实际留空既不是 9 点、
          也不再是后端旧兜底的每天 0 点。
        -->
        <el-form-item label="默认 Cron 规则">
          <el-input
            v-model="settingsForm.default_cron_rule"
            placeholder="cron 表达式，留空不建任务"
          />
          <div class="settings-hint">
            脚本未声明 cron 时使用；留空则不为这类脚本建任务
          </div>
        </el-form-item>
        <el-form-item label="拉取文件后缀">
          <el-input
            v-model="settingsForm.repo_file_extensions"
            placeholder="py js mjs ts sh"
          />
          <div class="settings-hint">
            空格分隔，如 py js mjs ts sh。订阅同步时只有这些后缀的脚本会被识别成定时任务
          </div>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="showSettingsDialog = false">取消</el-button>
        <el-button
          type="primary"
          :loading="settingsSaving"
          @click="handleSaveSettings"
          >保存</el-button
        >
      </template>
    </el-dialog>

    <!-- SSH Key Management Dialog -->
    <el-dialog
      v-model="showSSHKeyManageDialog"
      title="SSH 密钥管理"
      width="600px"
      :fullscreen="dialogFullscreen"
    >
      <div
        style="margin-bottom: 12px; display: flex; justify-content: flex-end"
      >
        <el-button type="primary" size="small" @click="openCreateSSHKey">
          <el-icon><Plus /></el-icon> 新建密钥
        </el-button>
      </div>
      <el-table :data="sshKeys" v-loading="sshKeyLoading" style="width: 100%">
        <el-table-column prop="name" label="名称" min-width="180" />
        <el-table-column prop="created_at" label="创建时间" width="170">
          <template #default="{ row }">
            <span class="time-text">{{ formatDateTime(row.created_at) }}</span>
          </template>
        </el-table-column>
        <el-table-column label="操作" width="150" fixed="right" align="center">
          <template #default="{ row }">
            <div class="action-btns">
              <el-button
                size="small"
                text
                type="primary"
                @click="openEditSSHKey(row)"
                >编辑</el-button
              >
              <el-button
                size="small"
                text
                type="danger"
                @click="handleDeleteSSHKey(row.id)"
                >删除</el-button
              >
            </div>
          </template>
        </el-table-column>
      </el-table>
      <el-empty
        v-if="!sshKeyLoading && sshKeys.length === 0"
        description="暂无 SSH 密钥"
      />
      <!-- 同上：移动端全屏时的底部「关闭」（F2） -->
      <template v-if="dialogFullscreen" #footer>
        <el-button @click="showSSHKeyManageDialog = false">关闭</el-button>
      </template>
    </el-dialog>

    <!-- SSH Key Edit Dialog -->
    <el-dialog
      v-model="showSSHKeyDialog"
      :title="isCreateSSHKey ? '新建 SSH 密钥' : '编辑 SSH 密钥'"
      width="550px"
      :fullscreen="dialogFullscreen"
      append-to-body
    >
      <el-form
        :model="sshKeyForm"
        :label-width="dialogFullscreen ? 'auto' : '80px'"
        :label-position="dialogFullscreen ? 'top' : 'right'"
      >
        <el-form-item label="名称">
          <el-input v-model="sshKeyForm.name" placeholder="密钥名称" />
        </el-form-item>
        <el-form-item label="私钥">
          <el-input
            v-model="sshKeyForm.private_key"
            type="textarea"
            :rows="8"
            :placeholder="isCreateSSHKey ? '粘贴 SSH 私钥内容' : '留空不修改'"
            spellcheck="false"
            style="font-family: var(--dd-font-mono)"
          />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="showSSHKeyDialog = false">取消</el-button>
        <el-button
          type="primary"
          :loading="sshKeySaving"
          :disabled="sshKeySaving"
          @click="handleSaveSSHKey"
          >{{ isCreateSSHKey ? "创建" : "保存" }}</el-button
        >
      </template>
    </el-dialog>
  </div>
</template>

<style scoped lang="scss">
.subscriptions-page {
  padding: 0;
}

.page-header {
  display: flex;
  justify-content: space-between;
  align-items: flex-start;
  margin-bottom: 18px;
  gap: 16px;

  h2 {
    margin: 0;
    font-size: 22px;
    font-weight: 700;
    color: var(--el-text-color-primary);
    line-height: 1.3;
  }
  .page-subtitle {
    font-size: 13px;
    color: var(--el-text-color-secondary);
    margin: 4px 0 0;
  }
  .header-actions {
    display: flex;
    gap: 10px;
    flex-shrink: 0;
  }
}

// 工具条：按 design-system §4.2，左槽 1×1 网格叠放筛选区与批量栏，两支常驻 DOM 靠 visibility 切换，
// 勾选前后工具栏高度恒定，消除表格跳动。
.toolbar {
  display: flex;
  justify-content: space-between;
  align-items: flex-start;
  margin: 14px 0;
  gap: 12px;
  flex-wrap: wrap;
  &__left {
    display: grid;
    grid-template-columns: minmax(0, 1fr);
    align-items: start;
    flex: 1;
    min-width: 0;
    min-height: 39px;
  }
  &__filters {
    display: flex;
    align-items: center;
    gap: 12px;
    flex-wrap: wrap;
    min-width: 0;
  }
  &__right {
    display: flex;
    align-items: center;
    gap: 10px;
    min-height: 39px;

    // 清零 EP 默认相邻按钮 12px 左外边距，间距统一由 gap 决定
    :deep(.el-button + .el-button) {
      margin-left: 0;
    }
  }
  &__search {
    width: 260px;
  }
}

.batch-actions {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
  min-height: 39px;

  :deep(.el-button + .el-button) {
    margin-left: 0;
  }
}

.batch-actions__count {
  font-size: 13px;
  color: var(--el-text-color-secondary);
  white-space: nowrap;
  cursor: default;
}

.toolbar__filters,
.batch-actions {
  grid-area: 1 / 1;
  min-width: 0;
  transition: opacity var(--dd-motion-fast) var(--dd-ease-standard);
}

.is-swapped-out {
  opacity: 0;
  visibility: hidden;
}

// 状态分段控件：与定时任务页/执行日志页一致的分段容器 + 选中态白底品牌色 + 1px 边框
.status-tabs {
  display: inline-flex;
  background: var(--el-fill-color-light);
  // 分段控件的灰底槽属控件类表面 → control 档（与槽内的项同档，两者一致才不会露出内外错位的角）
  border-radius: var(--dd-radius-control);
  padding: 3px;
  gap: 2px;
}

.status-tab {
  padding: 6px 14px;
  // 分段项属控件类表面 → control 档
  border-radius: var(--dd-radius-control);
  // 未选中态用透明边框占位，选中态只换边框颜色，避免尺寸跳动
  border: 1px solid transparent;
  background: transparent;
  color: var(--el-text-color-secondary);
  font-size: 13px;
  font-weight: 500;
  cursor: pointer;
  transition:
    color var(--dd-motion-fast) var(--dd-ease-standard),
    background-color var(--dd-motion-fast) var(--dd-ease-standard),
    border-color var(--dd-motion-fast) var(--dd-ease-standard);
  white-space: nowrap;
  &:hover {
    color: var(--el-text-color-primary);
  }
  &.active {
    background: var(--el-bg-color);
    color: var(--dd-accent-ink);
    border-color: var(--el-border-color-lighter);
    font-weight: 600;
  }
}

// 表格卡：无阴影，仅用 1px 边框与页面底色区分（dd-fixed-page 下的 flex + 内部滚动由全局规则接管）
.table-card {
  background: var(--el-bg-color);
  // 表格容器属容器类表面 → surface 档；overflow:hidden 让内部贴边的表头/行自动被圆角裁角
  border-radius: var(--dd-radius-surface);
  border: 1px solid var(--el-border-color-lighter);
  overflow: hidden;
}

.sub-name-cell {
  display: flex;
  align-items: center;
  gap: 8px;
  // 覆盖策略标签之后，这一格最多可能同时出现「Git」+「覆盖」两个标签（都是桌面缩写，全称挂 title）。
  // 缩写后两个标签约 96px，常规订阅名能和标签同排；这里仍保留换行兜底，
  // 遇到超长订阅名时让标签整块掉到第二行，而不是把订阅名挤成一列一个字。
  flex-wrap: wrap;
}
// 圆点 + 名称这一组（#133）。flex: 0 1 auto 而不是 1 1 auto：不抢剩余空间，
// 常规长度的名称后面标签仍紧贴着名字（与改动前逐像素一致）；名称超长时这一组的假想宽度
//（max-content）一行放不下 ⇒ 被外层 flex-wrap 单独放在第一行、标签掉到第二行，
// 再按 flex-shrink 收到行宽，靠 min-width:0 让里面的 .sub-name-text 出省略号。
// 外层 .sub-name-cell 保持 wrap 不动（design-system §4.1：名称行别改 nowrap，否则标签会把名字挤成 0 宽）。
.sub-name-main {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
  flex: 0 1 auto;
}
.sub-name-text {
  min-width: 0;
  font-weight: 500;
  color: var(--el-text-color-primary);
  // 单行省略写在基态，不分宽窄桌面（design-system §5「长文本列的单行省略只写在 .is-compact 里」那条坑）。
  // EP 的 .el-table .cell 是 white-space:normal + overflow-wrap:break-word，nowrap 会把它压住；
  // word-break: normal 是防御性的：项目里长文本常顺手写 break-all，它会让 text-overflow 失效（§4.1）。
  // 全称由模板上的 :title 兜底。
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  word-break: normal;
}

// 订阅启用状态灯（#133）：名称前的 8×8 圆点，绿 = 已启用 / 红 = 已禁用，桌面名称格与移动卡标题同用，
// 与环境变量页 .env-status-dot 同一套东西。刻意在本页 scoped 复制一份、不抽公共组件：
// 两处样式都很短，而 global.scss 与公共组件目录本轮是别组并行改动的热点文件。
//
// 🔴 固定 50%：design-system §1「状态圆点」白名单（收尾时在那张表里登记 .sub-status-dot），不是漏改。
//    8×8 的盒子吃 control(6px) 也会被圆角等比收缩夹回 4px = 正圆，写令牌只是绕远路。
// 取色只用 --el-color-success / --el-color-danger 语义令牌，暗色自动适配，不要写死十六进制。
// 红绿是最典型的色觉障碍撞色对，而且同一行右边还有「状态」列的拉取结果（正常绿 / 失败红），
// 只靠颜色分不清「哪个红是禁用、哪个红是失败」—— 模板上 title（悬停）与 role="img" + aria-label（读屏）
// 必须同时挂；role 不能省：光有 aria-label 的裸 <span> 不是可访问对象，读屏一般不会念出来。
.sub-status-dot {
  flex-shrink: 0;
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: var(--el-color-danger);
}
.sub-status-dot.is-enabled {
  background: var(--el-color-success);
}
.url-text {
  font-family: var(--dd-font-mono);
  font-size: 13px;
  color: var(--el-text-color-secondary);
}
.cron-text {
  font-family: var(--dd-font-mono);
  font-size: 13px;
  color: var(--el-text-color-secondary);
}
.time-text {
  font-family: var(--dd-font-mono);
  font-size: 12px;
  color: var(--el-text-color-regular);
}
.text-muted {
  color: var(--el-text-color-placeholder);
}
// 「拉取时间」格：上下两行「上次 / 下次」，前缀小号灰字
.pull-time-cell {
  display: flex;
  flex-direction: column;
  gap: 2px;
}
.pull-time-cell__line {
  display: flex;
  align-items: center;
  gap: 4px;
  line-height: 18px;
  white-space: nowrap;
}
.pull-time-cell__label {
  margin-right: 2px;
  font-size: 12px;
  color: var(--el-text-color-secondary);
}
// 操作列：与定时任务页/执行日志页一致的轻量行内按钮组
.action-btns {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 4px;

  // EP 自带 `.el-button + .el-button { margin-left: 12px }` 会叠加在上面的 flex gap 上，
  // 四个按钮凭空多吃 36px、撑破「操作」列的可用内容宽，两端的按钮被 .cell 的 overflow:hidden 裁掉。
  // 间距统一交给 gap（与 tasks / deps 两页一致）。
  :deep(.el-button + .el-button) {
    margin-left: 0;
  }

  // 原来这里还有一条 `:deep(.el-button) { padding: 4px 8px }` 的收窄覆写，已删（#133）：
  // 操作列宽已按 172 与 EP small 档默认内边距 5px 11px 重算（见模板里操作列的注释），
  // 环境变量页、定时任务页也早已去掉同一条；留着它按钮会比那两页窄一截，宽度账也对不上。
  // 同样用 .action-btns 的 SSH 密钥弹窗（两颗 small text 按钮、列宽 150）按默认内边距算是
  // 48 + 4 + 48 = 100 ≤ 150 − 24 = 126，不受影响。
}

// 分页条：与定时任务页/执行日志页一致的间距收敛
.pagination-bar {
  margin-top: 14px;
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 0 4px;
}
.pagination-total {
  font-size: 13px;
  color: var(--el-text-color-secondary);
}

:deep(.el-table) {
  // 表线颜色与桌面单元格行高统一交由全局 global.scss + _switches.scss 密度令牌接管；
  // 这里仅保留拉取日志「内容」列的单行截断规则。
  .log-content-cell .cell {
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
}

// ===== 移动端工具栏与卡片（v3.3.1，issue #143） =====
// 行、按钮、卡片各部件的几何都在 global.scss 的共享类里（.dd-mobile-toolbar / .dd-mobile-card__head 等），
// 这里只补本页独有的几处。

// 移动端工具栏外层：第一行离顶栏的 12px 由移动端 .layout-main 的上内边距给（MainLayout.vue），
// 第一行与第二行之间的 10px 由 .dd-mobile-toolbar / .dd-mobile-batch-bar 的下外边距给（两者上外边距都是 0）；
// 这里只给第二行（类型分段控件）与下面卡片列表之间留距离。
.subscription-mobile-toolbar {
  margin-bottom: 12px;
}

// 卡片名称字色。字号、省略由 .dd-mobile-card__name 给，它不管颜色；禁用卡在下面降一档。
.subscription-card__name {
  color: var(--el-text-color-primary);
}

// 末行「状态」（issue #144 / v3.3.2）：gap 取与 .dd-mobile-card__row 相同的 10px（全局 footer-main 是 8px），
// 标签左缘才能和上面「定时拉取 / 最后拉取」两行的值对齐成一条竖线。同 tasks 的 .task-card__result。
.subscription-card__status {
  gap: 10px;
}

// 类型标签（Git / 文件）：放在可选标签组外、不收缩，保证每张卡至少露一枚标签。
// 它只有两三个字宽，名称被挤到省略时它也照样紧跟在名称后面。
.subscription-card__type-tag {
  flex-shrink: 0;
}

// 首行可选标签组（覆盖 / 保留 / 不建任务 / 不删任务）：放不下的整枚隐藏、不换行、不做 +N，名称优先。
// 纯 CSS，四条缺一不可：
// 1) flex:1 1 0：基准宽是 0，只吃名称与类型标签用剩的宽度。收缩按「收缩系数 × 基准宽」分摊，
//    基准宽 0 就一像素都不分担，空间不够时全落在名称（0 1 auto）上，名称不会为标签提前省略。
//    别改回「flex-shrink 给个大数」：那只是按比例让位，名称仍会分到零点几像素的收缩，一样出省略号；
//    也别给 min-width:auto，多行 flex 容器的最小宽是最宽那枚标签，它折到被裁的第二行时首行会空出一截、名称白白让位。
//    min-width:0 与 overflow:hidden 由 G0 类 .dd-mobile-card__head-tags 给，这里不再写。
// 2) flex-wrap:wrap + 固定 20px 高：放不下的标签整枚折到下一行，被 overflow:hidden 裁掉，不会露出半枚。
//    20px 是 el-tag size="small" 的高度，改了标签尺寸要连下面 ::before 的高一起同步。
// 3) ::before 零宽占位：它永远占住第一行，第一枚真标签就不再享有「一行至少放一个」的待遇，
//    放不下时同样整枚折走；没有它的话，第一枚会硬留在首行、被横着裁成半截。
//    高度必须给满 20px：首行若只有它且是 0 高，第二行只往下错一个 6px 行距，折下去的标签会露出大半枚。
// 4) margin-left:-8px 抵掉 .dd-mobile-card__head 的 8px gap：可选标签一枚都放不下或根本没有时，
//    这个空容器不多占一道间距、不再从名称那里抢 8px；放得下时，::before 后面那道 6px 标签间距
//    正好成为类型标签与第一枚可选标签的间距，与标签之间的 6px 一致。
//    「···」靠 DdMoreMenu 自带的 margin-left:auto 贴右，本容器会长满剩余宽度，不影响它的位置。
.subscription-card__tags {
  flex: 1 1 0;
  flex-wrap: wrap;
  height: 20px;
  margin-left: -8px;

  &::before {
    content: "";
    width: 0;
    height: 20px;
  }
}

/* ---- 禁用行 / 禁用卡弱化（#133，照环境变量页 .env-row-disabled / .env-card--disabled） ---- */
// 弱化靠「浅底 + 名称降一档语义令牌」，不是变淡（整行 opacity 在明色下「看得出调暗」与「读得清」
// 不能同时成立，对比度实算见 envs/index.vue 的 Disabled Row 注释）：
// 名称 --el-text-color-primary → --el-text-color-regular，仍过 WCAG AA。
// 圆点、操作列的「启用」按钮、类型 / 覆盖标签与状态列一律不动：前两者是启用态的载体，
// 后两者与启用态正交（而且「状态」列本身就是拉取结果，弱化它会误导成「拉取出了问题」）。
// 🔴 绝不要给 tr / td 写 opacity：操作列是 fixed="right"，EP 的固定列是 sticky + z-index，
//    opacity < 1 会造出新的层叠上下文、打乱固定列层级。
// 🔴 选择器里的 .el-table 不能删：要靠它压过 EP 固定列的
//    `.el-table__body-wrapper tr td.el-table-fixed-column--right { background: inherit }`(0,2,2)，
//    否则禁用行最右边的操作格会退回普通底色，整行浅底缺一块。
// 🔴 不要用 --el-fill-color-light：那是 EP 的行 hover 底色，禁用行会看起来像被永久悬停。
//    悬停禁用行时 EP 的 hover 底会盖过这条，是期望行为，别去 !important 强压。
:deep(.el-table .sub-row-disabled > td) {
  background: var(--el-fill-color-lighter);
}

// 桌面名称与移动卡名称共用同一档降级
:deep(.sub-row-disabled) .sub-name-text,
.subscription-card--disabled .subscription-card__name {
  color: var(--el-text-color-regular);
}

// 禁用移动卡：global.scss 的 `.dd-mobile-card { background }` 是 (0,1,0)（暗色下没有另写），
// 这条 scoped 之后是 (0,2,0)，压得过。
.subscription-card--disabled {
  background: var(--el-fill-color-lighter);
}

.pull-log-content {
  font-family: var(--dd-font-mono, monospace);
  font-size: 13px;
  line-height: 1.6;
  padding: 12px 16px;
  margin: 0;
  // 拉取日志面板属容器类表面 → surface 档（弹窗 body 有内边距，不贴边，不会露角）
  border-radius: var(--dd-radius-surface);
  max-height: 560px;
  overflow-y: auto;
  white-space: pre-wrap;
  word-break: break-all;
}

// 移动端拉取日志列表与日志详情全屏弹窗：改为 flex 撑满全屏，取消 400px / 560px 限高截断
:deep(.sub-log-list-dialog.is-fullscreen),
:deep(.sub-log-detail-dialog.is-fullscreen) {
  .el-dialog__body {
    display: flex;
    flex-direction: column;
    overflow: hidden;
  }

  .pull-log-content {
    flex: 1 1 0;
    min-height: 0;
    max-height: none;
  }
}

.sub-log-mobile-list {
  flex: 1 1 0;
  min-height: 0;
  overflow-y: auto;
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.sub-log-mobile-card {
  padding: 12px;
  border-radius: var(--dd-radius-card);
  border: 1px solid var(--dd-divider, var(--el-border-color-lighter));
  background: var(--el-fill-color-lighter);
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.sub-log-mobile-card__head {
  display: flex;
  align-items: center;
  gap: 8px;
}

.sub-log-mobile-card__meta {
  flex: 1;
  min-width: 0;
  font-family: var(--dd-font-mono);
  font-size: 12px;
  color: var(--el-text-color-secondary);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.sub-log-mobile-card__content {
  font-family: var(--dd-font-mono);
  font-size: 12px;
  line-height: 1.5;
  color: var(--el-text-color-regular);
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
  overflow: hidden;
  word-break: break-all;
}

.pull-log-line {
  white-space: pre-wrap;
  word-break: break-all;
}
.pull-running {
  color: var(--dd-warn-ink);
  display: flex;
  align-items: center;
  gap: 8px;
}

// 拉取日志弹窗底部的状态指示。
// 用「圆点状态灯 + 次级文字」替代原来的 el-tag：颜色只落在 8px 色标上，
// 文字保持 --el-text-color-secondary，不跟右侧的「停止 / 关闭」抢视觉重量。
// 无边框底色块、无阴影、无渐变；色标本身固定正圆（理由见下方 .pull-status__mark）。
.pull-status {
  // footer 已是 flex 容器，这条才真正把状态推到最左侧
  margin-right: auto;
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-size: 12.5px;
  line-height: 1.5;
  color: var(--el-text-color-secondary);
  user-select: none;
}

// 色标默认就是 placeholder 灰，所以「连接中断」(is-disconnected) 和
// 判不出状态时的「已完成」(is-unknown) 不需要单独写规则，直接吃这个默认值。
.pull-status__mark {
  width: 8px;
  height: 8px;
  flex: 0 0 auto;
  // 形状承载语义：这是一枚 8×8 的拉取状态灯（下面按 is-running/is-aborted/is-success/is-failed 换色），
  // 与全站其它同尺寸状态灯（.pulse-dot / .status-dot / .dd-badge--dot / .menu-collapsed-dot 等）一样
  // 固定正圆、不吃 --dd-radius-* 令牌 —— 两种 shape 模式下都必须是圆点，方块会读成色块而不是状态灯。
  // 之前整条规则一个 border-radius 都没写，圆角化时的白名单扫描扫不到它，是漏网的一处。
  border-radius: 50%;
  background: var(--el-text-color-placeholder);
}

// 三档色标只引用 EP 语义色令牌（与名称前的 .sub-status-dot、上方 .pull-running 同源），
// 暗色由 EP 的 dark css-vars 接管，不写十六进制（design-system 硬规则 1）。
.pull-status.is-running .pull-status__mark,
.pull-status.is-aborted .pull-status__mark {
  background: var(--el-color-warning);
}

.pull-status.is-success .pull-status__mark {
  background: var(--el-color-success);
}

.pull-status.is-failed .pull-status__mark {
  background: var(--el-color-danger);
}

.settings-hint {
  color: var(--el-text-color-secondary);
  font-size: 12px;
  margin-top: 4px;
  line-height: 1.4;
}

// 状态标签切换（桌面状态列 + 移动卡片同用）：
// 只动 opacity。这枚标签坐在表格单元格里，任何位移都会让整行看起来在晃；
// 位移还会在 out-in 的两段之间产生方向不一致的漂移，比硬切更难读。
// 时长/缓动走令牌，prefers-reduced-motion 下自动降为 1ms 即等效关闭。
.dd-status-switch-enter-active,
.dd-status-switch-leave-active {
  transition: opacity var(--dd-motion-fast) var(--dd-ease-standard);
}

.dd-status-switch-enter-from,
.dd-status-switch-leave-to {
  opacity: 0;
}

// ===== 新建 / 编辑订阅弹窗：「高级设置」切换行（桌面与手机共用） =====
// 上边一条 1px 分隔线把基本区与高级区隔开（层次只靠边框表达，不加底色块、不加阴影）。
// 按钮是 EP 的 link 按钮：font-size 14px、line-height 1、上下 padding 2px + 1px 透明边框 ≈ 20px 高，
// 摘要的 line-height 取同一个 20px 并顶对齐：摘要只有一行时与按钮文字齐平，
// 窄屏折成多行时第一行仍与按钮对齐，而不是让按钮垂直居中到几行字的中间。
.subscription-form__advanced-toggle {
  display: flex;
  align-items: flex-start;
  flex-wrap: wrap;
  column-gap: 12px;
  row-gap: 2px;
  margin-bottom: 18px;
  padding-top: 12px;
  border-top: 1px solid var(--el-border-color-lighter);
}

.subscription-form__advanced-summary {
  flex: 1 1 200px;
  min-width: 0;
  font-size: 12px;
  line-height: 20px;
  color: var(--el-text-color-secondary);
}

// 展开 / 折叠的箭头：ArrowRight 转 90° 成朝下。这是状态切换的指示，不是 hover 形变，
// 不违反「hover/active 不做 transform」那条规则；时长走令牌，减少动效档下自动压成 1ms。
.subscription-form__advanced-arrow {
  transition: transform var(--dd-motion-fast) var(--dd-ease-standard);

  &.is-expanded {
    transform: rotate(90deg);
  }
}

// ===== 新建 / 编辑订阅弹窗：桌面端双列 =====
// 只在 ≥769px 生效；≤768px 不套任何 grid，表单退回默认块级流（天然单列），
// 且此时 dialogFullscreen 为 true（useResponsive 的断点同为 768），label 走 top 布局，
// .form-item--full 的 grid-column 在块级流下不生效，对移动端零副作用。
//
// 用 Grid 而不是 el-row/el-col：表单里「分支 / 指定子目录 / 仓库鉴权 / SSH 密钥 /
// 鉴权用户名 / Access Token / 完整检出 / 覆盖拉取」都是条件字段，固定栅格在字段隐藏时会留下死格，
// 而 Grid 的自动流会让后面的字段自动补位。
//
// 高级区（.subscription-form__advanced）是外层网格里占满整行的一项，内部再按同一套两列网格排；
// 它的 el-form-item 仍是 .subscription-form 的后代，下面那几条 :deep 规则照样命中，不用再写一遍。
// v-show 折叠时写的是内联 display:none，压得过这里的 display:grid；展开时内联样式清掉，网格恢复。
@media (min-width: 769px) {
  .subscription-form,
  .subscription-form__advanced {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    column-gap: 20px;
    // 行高不拉伸：同一行里矮的那个（如单选组）不跟着另一边的多行控件一起变高，
    // 两列的 label 才能对齐在同一条基线上
    align-items: start;
  }

  .subscription-form__advanced-toggle,
  .subscription-form__advanced {
    grid-column: 1 / -1;
    min-width: 0;
  }

  .subscription-form {
    // 行间距沿用 el-form-item 自带的 margin-bottom，不再叠 row-gap，避免双倍间距
    :deep(.el-form-item) {
      min-width: 0;
      margin-bottom: 18px;
    }

    // 跨满两列的字段。判定口径只看控件本身放不放得进半列（每列输入框宽 246px，见下方宽度账）：
    // 一键识别的输入框 + 按钮、URL、仓库鉴权 / 覆盖拉取 / 自动建任务 / 自动删任务 这几组单选、两个钩子 textarea。
    // v3.2.9 删掉常驻说明后，白名单 / 黑名单 / 依赖规则 / 鉴权用户名 / Access Token / 完整检出 都回到了半列——
    // 以前它们跨列只是为了容纳说明文字。说明改成标签旁的「?」气泡，不再占表单宽度，
    // 所以以后也别再按「说明有多长」来决定跨不跨列。
    :deep(.form-item--full) {
      grid-column: 1 / -1;
    }

    // 800px 弹窗的可用宽度：800 − .el-dialog 自带 16px×2 − .el-dialog__body 24px×2 = 720px，
    // 两列减去 20px 列间距后每列 350px，减 104px 标签宽后输入框还有 246px。
    // 104px 标签宽 = 5 个中文字 70px + 「?」按钮 18px（14px 图标 + 4px 间距，见 DdFieldHelp 的热区写法）
    // + 12px 右内边距 = 100px，留 4px 余量，「指定子目录 / 鉴权用户名 / 自动建任务 / 拉取前指令」这类
    // 5 字带气泡的标签不折行；「Access Token」估算约 88px + 12px = 100px，同样放得下（88px 标签宽时它会折行）。
    // 以后标签更长、或 DdFieldHelp 的按钮尺寸变了，要回来重算这笔账。
    // 另外 EP 给 .el-form-item__label 写死了 height:32px / line-height:32px，一旦折行第二行会溢出压到下一行，
    // 所以下面仍然放开标签高度兜底。
    //
    // 放开高度必须同时写下面三条，缺一不可：
    // 1) height:auto + min-height:32px —— 折行时由标签内容自然撑高，不再溢出。
    // 2) align-self:flex-start —— 【关键，删掉就会复发】.el-form-item 是 display:flex 且
    //    没有声明 align-items，因此 flex 子元素默认 align-self:stretch。EP 原本那个显式的
    //    height:32px 恰好压住了 stretch（stretch 只在 cross-size 为 auto 时才生效）；
    //    一旦改成 height:auto，stretch 立即恢复，label 盒子会被拉伸到整个表单项的高度
    //    （例如「拉取前指令 / 拉取后钩子」的多行 textarea），第 3 条的 align-items:center 就会把标签文字
    //    居中到这个大盒子的正中，标签明显下沉，两列并排时同一行左右两个标签还会错开。锚在顶部后，
    //    label 盒子高度 = max(内容高, 32px)，才能对齐输入框/radio 那一行；
    //    多行 textarea 的标签对齐 textarea 顶行而不是垂直居中。
    // 3) align-items:center —— label 自身是 inline-flex，且 EP 给它设了 align-items:flex-start，
    //    而这里把 line-height 从 32px 收成 1.4（≈19.6px），不居中的话单行标签会贴着盒子顶端。
    //
    // 仅限左右布局：全屏（dialogFullscreen）时 label-position 切成 top，EP 会给
    // .el-form-item--label-top 设 display:block，label 不再是 flex 子元素，
    // min-height:32px 反而会在标签与控件之间垫出多余空隙，故用 :not() 排除。
    // 常态下本媒体查询（≥769px）与全屏（≤768px）互斥，但 useResponsive 有 document.hidden
    // 守卫会让 width 滞后，后台放大窗口再切回来的瞬间两者可能同时成立，这里做兜底。
    :deep(.el-form-item:not(.el-form-item--label-top) .el-form-item__label) {
      align-self: flex-start;
      height: auto;
      min-height: 32px;
      line-height: 1.4;
      align-items: center;
    }
  }
}

// ≤768 下桌面工具栏（.toolbar）不再渲染，移动端走上面的 .subscription-mobile-toolbar，
// 原来那组把 .toolbar 竖排成三行的规则已随拆分删除。
@media (max-width: 768px) {
  .page-header {
    flex-direction: column;
    gap: 10px;
    margin-bottom: 14px;
    h2 {
      font-size: 18px;
    }
  }

  // 移动端分页栏纵向堆叠居中，防止总条数与页码并排溢出
  .pagination-bar {
    flex-direction: column;
    gap: 10px;
    align-items: center;
  }

  :deep(.sub-log-list-dialog.is-fullscreen) .pagination-container {
    flex-shrink: 0;
    display: flex;
    justify-content: center;
  }

  // issue #144 / v3.3.2：横栏改回 12px 留白后，槽（.dd-mobile-bleed）在移动端吃 --dd-radius-button（16px），
  // 槽内的分段项必须跟着抬上去，否则槽圆了、项还是 control 档 6px，四角的灰边几乎看不见。
  // 减 3px 是槽自己的 padding，让内外两圈圆角同心；square 模式下 calc(0px - 3px) 会被 CSS 夹到 0，不会破直角。
  // 选择器必须带 .dd-mobile-bleed 限定：桌面工具栏里那组 .status-tabs 没挂它，不能被一起抬。
  .status-tabs.dd-mobile-bleed .status-tab {
    border-radius: calc(var(--dd-radius-button) - 3px);
  }
}

// ===== 入场动画 =====
// v4.0 起改用全局 .dd-rise（styles/motion.scss，受动效档位控制）：模板里给工具条 / 表格卡 / 移动列表挂类，
// 内联 --dd-i 控制错落。不给表格每一行或每张移动卡做 stagger。
</style>
