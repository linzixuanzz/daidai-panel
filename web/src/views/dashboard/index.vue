<script setup lang="ts">
import {
  ref,
  computed,
  onMounted,
  onBeforeUnmount,
  onActivated,
  onDeactivated,
  watch,
  nextTick,
  defineAsyncComponent,
} from "vue";
import { useRouter } from "vue-router";
import { systemApi } from "@/api/system";
import { taskApi } from "@/api/task";
import { useAuthStore } from "@/stores/auth";
import { useAttentionStore } from "@/stores/attention";
import { useRunningStore } from "@/stores/running";
import { useUiStore } from "@/stores/ui";
import { ElMessage } from "element-plus";
import {
  Tickets,
  TrendCharts,
  CircleCheck,
  Loading,
  Refresh,
  ArrowRight,
  Cpu,
  View,
  Calendar,
  WarningFilled,
  CirclePlus,
  Operation,
  Brush,
  Grid,
  FullScreen,
  Rank,
  Close,
  Monitor,
  DataLine,
  Odometer,
} from "@element-plus/icons-vue";
import DdCountUp from "@/components/ui/DdCountUp.vue";
import AttentionList from "@/components/AttentionList.vue";
import TaskHealthPanel from "@/components/TaskHealthPanel.vue";
import UpcomingCard from "./components/UpcomingCard.vue";
import { useResponsive } from "@/composables/useResponsive";
import { canAdminister, canOperate } from "@/utils/roles";
import { formatDuration } from "@/utils/duration";
import { formatRelativeTime } from "@/utils/datetime";

const ExecutionTrendChart = defineAsyncComponent(
  () => import("./components/ExecutionTrendChart.vue"),
);
const router = useRouter();
const authStore = useAuthStore();
const attention = useAttentionStore();
const runningStore = useRunningStore();
const ui = useUiStore();
const { isMobile } = useResponsive();
const LOG_STATUS_SUCCESS = 0;
const LOG_STATUS_FAILED = 1;
const LOG_STATUS_RUNNING = 2;
const LOG_STATUS_ABORTED = 3;

const canOperateTasks = computed(() => canOperate(authStore.user?.role));
const canViewSystemDetails = computed(() =>
  canAdminister(authStore.user?.role),
);

// ============================================================================
// 卡片布局（design §5 F7）：顺序与显隐存本机 dd:dashboard:layout = {v:1, order:[], hidden:[]}
// ============================================================================
const LAYOUT_KEY = "dd:dashboard:layout";
// span 是 12 栏栅格里占几栏（原型的 s4/s5/s7/s8）；顺序就是默认顺序
const CARD_DEFS = [
  { id: "hero", title: "总览", span: 7 },
  { id: "kpis", title: "关键指标", span: 5 },
  { id: "trend", title: "执行趋势", span: 8 },
  { id: "attention", title: "需要关注", span: 4 },
  { id: "upcoming", title: "未来 24 小时", span: 8 },
  { id: "resource", title: "系统资源", span: 4 },
  { id: "feed", title: "实时执行流", span: 7 },
  { id: "health", title: "任务健康画像", span: 5 },
] as const;
type CardId = (typeof CARD_DEFS)[number]["id"];
const CARD_IDS = CARD_DEFS.map((c) => c.id) as CardId[];

/** 读本机布局：未知卡片 id 忽略、重复的去掉、新卡片追加到末尾；存档坏了或版本不对就用默认 */
function loadLayout(): { order: CardId[]; hidden: CardId[] } {
  let order: CardId[] = [];
  let hidden: CardId[] = [];
  try {
    const saved = JSON.parse(localStorage.getItem(LAYOUT_KEY) || "null");
    if (saved && saved.v === 1) {
      if (Array.isArray(saved.order)) {
        for (const id of saved.order) {
          if (CARD_IDS.includes(id) && !order.includes(id)) order.push(id);
        }
      }
      if (Array.isArray(saved.hidden)) {
        hidden = saved.hidden.filter((id: CardId) => CARD_IDS.includes(id));
      }
    }
  } catch {
    // 隐私模式读不了 / JSON 坏了：按默认布局
  }
  for (const id of CARD_IDS) {
    if (!order.includes(id)) order.push(id);
  }
  return { order, hidden };
}

const layout = ref(loadLayout());
const editing = ref(false);

function saveLayout() {
  try {
    localStorage.setItem(
      LAYOUT_KEY,
      JSON.stringify({ v: 1, order: layout.value.order, hidden: layout.value.hidden }),
    );
  } catch {
    // 写不进去只影响「刷新后还在」，不打断操作
  }
}

/**
 * 实际渲染的卡片与动态栅格跨度：
 * - 编辑态全部显示（隐藏的半透明，方便再打开），保持卡片原始跨度便于理解 12 栏位置；
 * - 常规态只渲染可见卡片，按 12 栏逐行装箱：若某行因隐藏邻卡只剩不足 12 栏的空洞，
 *   把剩余列宽回填给该行卡片（单卡直接拉满 12 栏，双卡按余量补齐），避免隐藏单卡留下明显空白。
 */
const renderedCards = computed(() => {
  const cards = layout.value.order
    .filter((id) => editing.value || !layout.value.hidden.includes(id))
    .map((id) => CARD_DEFS.find((c) => c.id === id)!);

  if (editing.value) {
    return cards.map((card) => ({ ...card, renderSpan: card.span as number }));
  }

  // 按顺序拆成若干不超过 12 栏的行，逐行把空余栏位平摊回当前行卡片
  const result: Array<(typeof CARD_DEFS)[number] & { renderSpan: number }> = [];
  let row: Array<(typeof CARD_DEFS)[number] & { renderSpan: number }> = [];
  let rowSum = 0;

  const flushRow = () => {
    if (row.length === 0) return;
    let remain = 12 - rowSum;
    if (remain > 0) {
      // 单卡直接拉满 12 栏；多卡时依次轮询补列，保持相对比例且不留空洞
      let i = 0;
      while (remain > 0) {
        row[i % row.length]!.renderSpan += 1;
        remain -= 1;
        i += 1;
      }
    }
    result.push(...row);
    row = [];
    rowSum = 0;
  };

  for (const card of cards) {
    if (rowSum + card.span > 12) {
      flushRow();
    }
    row.push({ ...card, renderSpan: card.span });
    rowSum += card.span;
  }
  flushRow();
  return result;
});

function toggleCardHidden(id: CardId, visible: boolean) {
  const hidden = layout.value.hidden.filter((h) => h !== id);
  if (!visible) hidden.push(id);
  layout.value = { ...layout.value, hidden };
  saveLayout();
}

function resetLayout() {
  layout.value = { order: [...CARD_IDS], hidden: [] };
  saveLayout();
}

const bentoRef = ref<HTMLElement | null>(null);
let sortableInstance: any = null;

async function startEditing() {
  editing.value = true;
  await nextTick();
  if (!bentoRef.value) return;
  try {
    const Sortable = (await import("sortablejs")).default;
    sortableInstance?.destroy();
    sortableInstance = Sortable.create(bentoRef.value, {
      animation: 180,
      handle: ".dash-drag-handle",
      draggable: ".dash-item",
      ghostClass: "dash-item--ghost",
      forceFallback: true,
      onEnd: (evt: any) => {
        // 编辑态渲染的是完整 order，下标与 order 一一对应
        const { oldIndex, newIndex } = evt;
        if (oldIndex == null || newIndex == null || oldIndex === newIndex) return;
        const order = [...layout.value.order];
        const [moved] = order.splice(oldIndex, 1);
        if (!moved) return;
        order.splice(newIndex, 0, moved);
        layout.value = { ...layout.value, order };
        saveLayout();
      },
    });
  } catch (err) {
    console.warn("failed to init sortable for dashboard layout", err);
  }
}

function stopEditing() {
  editing.value = false;
  sortableInstance?.destroy();
  sortableInstance = null;
}

// ============================================================================
// 数据
// ============================================================================
const trendRange = ref<7 | 30>(7);
type LogFilter = "all" | "success" | "failed" | "aborted" | "running";
const logFilter = ref<LogFilter>("all");
const logFilterOptions: Array<{ label: string; value: LogFilter }> = [
  { label: "全部", value: "all" },
  { label: "成功", value: "success" },
  { label: "失败", value: "failed" },
  { label: "终止", value: "aborted" },
  { label: "运行中", value: "running" },
];
const refreshTimestamp = ref(new Date());
const hasLoadedOnce = ref(false);
const skipInitialActivated = ref(true);

const dashboardData = ref<any>({});
const sysInfo = ref<any>({});
const recentLogs = computed<any[]>(() => dashboardData.value.recent_logs || []);
const dailyStats = computed<Array<{ date?: string; success?: number; failed?: number; aborted?: number }>>(
  () => dashboardData.value.daily_stats || [],
);

const greeting = computed(() => {
  const hour = new Date().getHours();
  if (hour < 6) return "夜深了";
  if (hour < 12) return "早上好";
  if (hour < 14) return "中午好";
  if (hour < 18) return "下午好";
  return "晚上好";
});

// hero 内的日期/星期文案：用 new Date() 直接推导，无需新增数据源
const heroDateLabel = computed(() => {
  void refreshTimestamp.value; // 刷新时一并更新展示
  const now = new Date();
  const weekdays = ["周日", "周一", "周二", "周三", "周四", "周五", "周六"];
  return `${now.getMonth() + 1}月${now.getDate()}日 · ${weekdays[now.getDay()]}`;
});

const todayLogs = computed(() => Number(dashboardData.value.today_logs) || 0);
const successLogs = computed(() => Number(dashboardData.value.success_logs) || 0);
const failedLogs = computed(() => Number(dashboardData.value.failed_logs) || 0);
const abortedLogs = computed(() => Number(dashboardData.value.aborted_logs) || 0);
const taskCount = computed(() => Number(dashboardData.value.task_count) || 0);
const prevTaskCount = computed(() => Number(dashboardData.value.prev_task_count) || 0);
const runningTasks = computed(() => Number(dashboardData.value.running_tasks) || 0);
const yesterdayLogs = computed(() => Number(dashboardData.value.yesterday_logs) || 0);
const yesterdaySuccess = computed(() => Number(dashboardData.value.yesterday_success) || 0);
const yesterdayFailed = computed(() => Number(dashboardData.value.yesterday_failed) || 0);
const yesterdayAborted = computed(() => Number(dashboardData.value.yesterday_aborted) || 0);
const todayFinishedLogs = computed(() => successLogs.value + failedLogs.value);
const yesterdayFinishedLogs = computed(() => yesterdaySuccess.value + yesterdayFailed.value);

const todaySuccessRate = computed(() => {
  if (!todayFinishedLogs.value) return 0;
  return Math.round((successLogs.value / todayFinishedLogs.value) * 1000) / 10;
});

const yesterdaySuccessRate = computed(() => {
  if (!yesterdayFinishedLogs.value) return 0;
  return Math.round((yesterdaySuccess.value / yesterdayFinishedLogs.value) * 1000) / 10;
});

const taskCountDelta = computed(() => taskCount.value - prevTaskCount.value);
const todayLogsDelta = computed(() => todayLogs.value - yesterdayLogs.value);
const successRateDelta = computed(
  () => Math.round((todaySuccessRate.value - yesterdaySuccessRate.value) * 10) / 10,
);

/** 「运行中」的迷你折线没有历史接口，用本页每次刷新时的采样（最多 12 个点，只在内存里） */
const runningHistory = ref<number[]>([]);
function sampleRunning(n: number) {
  runningHistory.value = [...runningHistory.value, n].slice(-12);
}

/**
 * 迷你折线路径（viewBox 200×40）。
 * partialLast = 最后一个点是「今天」：今天还没过完，最后一段单独返回成虚线（dash），避免画成断崖。
 */
function sparkPaths(values: number[], partialLast: boolean) {
  const W = 200;
  const H = 40;
  const list = values.length >= 2 ? values : [values[0] ?? 0, values[0] ?? 0];
  const max = Math.max(...list);
  const min = Math.min(...list);
  const pts = list.map((v, i) => [
    (i * W) / (list.length - 1),
    H - 4 - ((v - min) / (max - min || 1)) * (H - 12),
  ]);
  const toPath = (ps: number[][]) =>
    "M" + ps.map((p) => `${(p[0] ?? 0).toFixed(1)},${(p[1] ?? 0).toFixed(1)}`).join(" L");
  // 至少要 3 个点，实线部分才有 2 个点可画
  const split = partialLast && list.length >= 3;
  return {
    line: toPath(split ? pts.slice(0, -1) : pts),
    dash: split ? toPath(pts.slice(-2)) : "",
    area: `${toPath(pts)} L${W},${H} L0,${H} Z`,
  };
}

const statCards = computed(() => {
  const days = dailyStats.value.slice(-10);
  const dayTotals = days.map((d) => (d.success || 0) + (d.failed || 0) + (d.aborted || 0));
  // 某天一次都没结束时没有成功率，沿用前一天的值，免得折线掉到 0
  let lastRate = 100;
  const dayRates = days.map((d) => {
    const finished = (d.success || 0) + (d.failed || 0);
    if (finished > 0) lastRate = ((d.success || 0) / finished) * 100;
    return lastRate;
  });
  return [
    {
      key: "total",
      label: "任务总数",
      value: taskCount.value,
      delta: taskCountDelta.value as number | null,
      deltaSuffix: "",
      icon: Tickets,
      color: "var(--dd-accent)",
      link: "/tasks",
      spark: sparkPaths([prevTaskCount.value, taskCount.value], false),
      decimals: 0,
      suffix: "",
    },
    {
      key: "running",
      label: "运行中",
      value: runningTasks.value,
      delta: null as number | null,
      deltaSuffix: "",
      icon: Loading,
      color: "var(--dd-accent-2)",
      link: "/tasks",
      spark: sparkPaths(runningHistory.value, false),
      decimals: 0,
      suffix: "",
    },
    {
      key: "today",
      label: "今日执行",
      value: todayLogs.value,
      delta: todayLogsDelta.value as number | null,
      deltaSuffix: "",
      icon: TrendCharts,
      color: "var(--dd-warn)",
      link: "/logs",
      spark: sparkPaths(dayTotals, true),
      decimals: 0,
      suffix: "",
    },
    {
      key: "success-rate",
      label: "成功率",
      value: todaySuccessRate.value,
      delta: successRateDelta.value as number | null,
      deltaSuffix: "%",
      icon: CircleCheck,
      color: "var(--dd-ok)",
      link: "/logs",
      spark: sparkPaths(dayRates, true),
      decimals: 1,
      suffix: "%",
    },
  ];
});

// ---- 健康分（design §5）：round(近 7 天成功率 × 100) − 8 × err 条数 − 3 × warn 条数，夹在 0~100 ----
const sevenDayRate = computed(() => {
  const days = dailyStats.value.slice(-7);
  const ok = days.reduce((s, d) => s + (d.success || 0), 0);
  const fail = days.reduce((s, d) => s + (d.failed || 0), 0);
  // 7 天里一次都没结束：没有失败就不扣分
  return ok + fail > 0 ? ok / (ok + fail) : 1;
});
const attentionErrCount = computed(() => attention.items.filter((i) => i.level === "err").length);
const attentionWarnCount = computed(() => attention.items.filter((i) => i.level === "warn").length);
const failingTaskCount = computed(
  () => attention.items.filter((i) => i.type === "task_failing").length,
);
const healthScore = computed(() => {
  const raw =
    Math.round(sevenDayRate.value * 100) -
    8 * attentionErrCount.value -
    3 * attentionWarnCount.value;
  return Math.max(0, Math.min(100, raw));
});
// 环形进度：挂载后下一帧才把 offset 放到目标值，rich 档能看到从 0 画到目标的过渡
const ringReady = ref(false);
const ringOffset = computed(() => (ringReady.value ? 100 - healthScore.value : 100));

// ---- 执行趋势卡底部的小结（原「执行统计」卡收进来）----
const trendSummary = computed(() => {
  const success = dailyStats.value.reduce((s, d) => s + (d.success || 0), 0);
  const failed = dailyStats.value.reduce((s, d) => s + (d.failed || 0), 0);
  const aborted = dailyStats.value.reduce((s, d) => s + (d.aborted || 0), 0);
  const total = success + failed + aborted;
  const pct = (n: number) => (total ? Math.round((n / total) * 1000) / 10 : 0);
  return {
    total,
    success,
    failed,
    aborted,
    successPct: pct(success),
    failedPct: pct(failed),
    abortedPct: pct(aborted),
  };
});

// 平均执行时长（秒）。没有可用样本时返回 null，交给 formatDuration 显示 "-"，
// 避免把「没数据」渲染成 0，让人误以为任务是瞬间跑完的。
// 这里刻意不做预先四舍五入：提前把 59.96 修成 60.0 会让格式化结果跳成 "1m"，
// 精度统一交给 formatDuration 处理。
const avgDuration = computed<number | null>(() => {
  const valid = recentLogs.value.filter((l: any) => l.duration != null);
  if (!valid.length) return null;
  const sum = valid.reduce((s: number, l: any) => s + (l.duration || 0), 0);
  return sum / valid.length;
});

// ---- 系统资源（环形）----
const resourceRings = computed(() => {
  const s = sysInfo.value;
  const items = [
    { key: "cpu", label: `CPU · ${s.num_cpu || "-"} 核`, percent: Number(s.cpu_usage) || 0, decimals: 0 },
    { key: "memory", label: `内存 · ${formatBytes(s.memory_total)}`, percent: Number(s.memory_usage) || 0, decimals: 0 },
    { key: "disk", label: `磁盘 · ${formatBytes(s.disk_total)}`, percent: Number(s.disk_usage) || 0, decimals: 1 },
  ];
  return items.map((r) => ({
    ...r,
    // 环的颜色：>90 失败色、>75 警告色、其余主色（图形用实色令牌）
    color: r.percent > 90 ? "var(--dd-err)" : r.percent > 75 ? "var(--dd-warn)" : "var(--dd-accent)",
    offset: 100 - Math.min(100, Math.max(0, r.percent)),
  }));
});

function formatBytes(bytes: number) {
  if (!bytes) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB"];
  let i = 0;
  let val = bytes;
  while (val >= 1024 && i < units.length - 1) {
    val /= 1024;
    i++;
  }
  return val.toFixed(1) + " " + units[i];
}

const lastUpdatedTick = ref(0);
const updatedHint = computed(() => {
  void lastUpdatedTick.value; // 每 30 秒重算一次「x 分钟前」
  const diff = (Date.now() - refreshTimestamp.value.getTime()) / 1000;
  if (diff < 60) return "刚刚";
  if (diff < 3600) return Math.floor(diff / 60) + " 分钟前";
  return Math.floor(diff / 3600) + " 小时前";
});

// ---- 实时执行流 ----
function isRunningLog(status: number | null | undefined) {
  return status === LOG_STATUS_RUNNING || status === null || status === undefined;
}

const filteredLogs = computed(() => {
  const list = recentLogs.value;
  const pick = (fn: (s: number | null | undefined) => boolean) =>
    list.filter((l: any) => fn(l.status)).slice(0, 7);
  if (logFilter.value === "running") return pick(isRunningLog);
  if (logFilter.value === "success") return pick((s) => s === LOG_STATUS_SUCCESS);
  if (logFilter.value === "failed") return pick((s) => s === LOG_STATUS_FAILED);
  if (logFilter.value === "aborted") return pick((s) => s === LOG_STATUS_ABORTED);
  return list.slice(0, 7);
});

/** 新出现、或状态刚变（运行中 → 成功）的日志行：从顶部滑入并闪一下底色 */
const freshLogIds = ref<number[]>([]);
const seenLogStatus = new Map<number, unknown>();
let feedBaseline = false;
let freshTimer: number | null = null;
watch(recentLogs, (list) => {
  const fresh: number[] = [];
  for (const log of list) {
    if (feedBaseline && seenLogStatus.get(log.id) !== log.status) fresh.push(log.id);
    seenLogStatus.set(log.id, log.status);
  }
  // 首次加载只建基线，不播动画
  feedBaseline = true;
  if (!fresh.length) return;
  freshLogIds.value = fresh;
  if (freshTimer) clearTimeout(freshTimer);
  freshTimer = window.setTimeout(() => {
    freshLogIds.value = [];
    freshTimer = null;
  }, 2200);
});

function statusKind(status: number | null | undefined) {
  if (isRunningLog(status)) return "running";
  if (status === LOG_STATUS_SUCCESS) return "success";
  if (status === LOG_STATUS_ABORTED) return "aborted";
  return "failed";
}

function statusText(status: number | null | undefined) {
  if (isRunningLog(status)) return "运行中";
  if (status === LOG_STATUS_SUCCESS) return "成功";
  if (status === LOG_STATUS_ABORTED) return "已终止";
  return "失败";
}

function normalizeLabels(labels: unknown): string[] {
  if (Array.isArray(labels)) return labels.map(String).filter(Boolean);
  if (typeof labels === "string") {
    return labels
      .split(",")
      .map((item) => item.trim())
      .filter(Boolean);
  }
  return [];
}

function taskTypeOf(log: any) {
  return log?.task_type || log?.task?.task_type;
}

function envLabel(log: any) {
  const labels = normalizeLabels(log?.labels ?? log?.task?.labels);
  if (labels.length > 0) return labels[0] || "default";
  return taskTypeOf(log) === "manual" ? "manual" : "cron";
}

/** 环境标记的色系：类名对应样式里的墨色 + 浅底（不再写死 hex） */
function envTone(env: string) {
  switch (env) {
    case "prod":
    case "production":
      return "is-ok";
    case "staging":
    case "manual":
      return "is-warn";
    case "local":
      return "is-muted";
    default:
      return "is-accent";
  }
}

function viewLog(log: any) {
  router.push({ path: "/logs", query: { task_id: log.task_id } });
}

function rerunLog(log: any) {
  router.push({ path: "/tasks", query: { task_id: log.task_id, action: "run" } });
}

// ---- 任务健康卡：默认取「最近最忙」的任务，用户选过的存本机 ----
const HEALTH_TASK_KEY = "dd:dashboard:health-task";
type TaskOption = { id: number; name: string };
const healthTask = ref<TaskOption | null>(null);
const healthOptions = ref<TaskOption[]>([]);
const healthSearching = ref(false);
let defaultOptions: TaskOption[] = [];

function readSavedHealthTask(): TaskOption | null {
  try {
    const saved = JSON.parse(localStorage.getItem(HEALTH_TASK_KEY) || "null");
    if (saved && Number(saved.id) > 0) return { id: Number(saved.id), name: String(saved.name || `任务#${saved.id}`) };
  } catch {
    // 读不了就按默认挑
  }
  return null;
}

/**
 * 默认任务：没有「近 7 天每个任务的运行次数」这种接口，这里用两个现成数据近似：
 * 1. 未来 24 小时调度点最多的任务（cron 越密，过去 7 天跑得越多）；
 * 2. 没有定时任务时，取最近执行记录里出现最多的任务。
 */
async function pickDefaultHealthTask() {
  try {
    const res = await taskApi.upcoming(24);
    const sorted = [...(res.tasks || [])].sort((a, b) => b.times.length - a.times.length);
    defaultOptions = sorted.slice(0, 20).map((t) => ({ id: t.id, name: t.name }));
    healthOptions.value = defaultOptions;
    if (!healthTask.value && sorted[0]) {
      healthTask.value = { id: sorted[0].id, name: sorted[0].name };
      return;
    }
  } catch {
    // 落到下面的兜底
  }
  if (healthTask.value) return;
  const counts = new Map<number, { n: number; name: string }>();
  for (const log of recentLogs.value) {
    if (!log.task_id) continue;
    const cur = counts.get(log.task_id) || { n: 0, name: log.task_name || `任务#${log.task_id}` };
    cur.n++;
    counts.set(log.task_id, cur);
  }
  const best = [...counts.entries()].sort((a, b) => b[1].n - a[1].n)[0];
  if (best) healthTask.value = { id: best[0], name: best[1].name };
}

async function searchHealthTasks(keyword: string) {
  if (!keyword.trim()) {
    healthOptions.value = defaultOptions;
    return;
  }
  healthSearching.value = true;
  try {
    const res = await taskApi.list({ keyword: keyword.trim(), page: 1, page_size: 20 });
    healthOptions.value = (res.data || []).map((t: any) => ({ id: Number(t.id), name: String(t.name) }));
  } catch {
    healthOptions.value = [];
  } finally {
    healthSearching.value = false;
  }
}

/** 下拉选项里必须包含当前选中的任务，否则 el-select 只能显示数字 id */
const healthSelectOptions = computed(() => {
  const list = [...healthOptions.value];
  if (healthTask.value && !list.some((o) => o.id === healthTask.value!.id)) list.unshift(healthTask.value);
  return list;
});

function selectHealthTask(id: number) {
  const opt = healthSelectOptions.value.find((o) => o.id === id);
  if (!opt) return;
  healthTask.value = opt;
  try {
    localStorage.setItem(HEALTH_TASK_KEY, JSON.stringify(opt));
  } catch {
    // 存不进去下次就按默认挑
  }
}

// ---- 加载 ----
const upcomingRef = ref<InstanceType<typeof UpcomingCard> | null>(null);
// 同 setTrendHost：v-for 里的组件 ref 用函数形式拿
function setUpcomingRef(el: unknown) {
  upcomingRef.value = (el as InstanceType<typeof UpcomingCard> | null) || null;
}

const loadDashboard = async () => {
  try {
    const res = (await systemApi.dashboard(trendRange.value)) as any;
    dashboardData.value = res.data || {};
    refreshTimestamp.value = new Date();
    sampleRunning(Number(dashboardData.value.running_tasks) || 0);
  } catch {
    ElMessage.error("加载仪表盘数据失败");
  }
};

const loadSysInfo = async () => {
  try {
    const res = (await systemApi.info()) as any;
    sysInfo.value = res.data || {};
  } catch {
    ElMessage.error("加载系统信息失败");
  }
};

watch(trendRange, () => {
  loadDashboard();
});

function loadDashboardPage() {
  loadDashboard();
  loadSysInfo();
  void attention.refresh();
}

// 运行中的任务集合变了（有任务开始或结束）→ 重新拉一次仪表板，执行流与计数随之更新。
// running store 由外壳轮询（5s / 30s），本页不再自己额外轮询 /system/dashboard。
// 侦听的是「运行中 task_id 拼成的字符串」，只有集合真的变了才会触发。
let runningReloadTimer: number | null = null;
watch(
  (): string => (runningStore.items || []).map((i: { task_id: number }) => i.task_id).join(","),
  () => {
    if (!hasLoadedOnce.value) return;
    if (runningReloadTimer) clearTimeout(runningReloadTimer);
    runningReloadTimer = window.setTimeout(() => {
      runningReloadTimer = null;
      void loadDashboard();
    }, 800);
  },
);

// ---- 执行趋势图懒加载（滚到视口附近才初始化 echarts）----
const showTrendChart = ref(false);
const trendChartHostRef = ref<HTMLElement | null>(null);
let trendChartObserver: IntersectionObserver | null = null;
let trendChartTimer: number | null = null;

function activateTrendChart() {
  if (showTrendChart.value || trendChartTimer) return;
  trendChartTimer = window.setTimeout(() => {
    showTrendChart.value = true;
    trendChartTimer = null;
  }, 120);
}

function stopObservingTrendChart() {
  trendChartObserver?.disconnect();
  trendChartObserver = null;
}

function scheduleTrendChartRender() {
  if (showTrendChart.value || !trendChartHostRef.value) return;
  if (typeof IntersectionObserver === "undefined") {
    activateTrendChart();
    return;
  }
  stopObservingTrendChart();
  trendChartObserver = new IntersectionObserver(
    (entries) => {
      if (!entries.some((e) => e.isIntersecting)) return;
      stopObservingTrendChart();
      activateTrendChart();
    },
    { rootMargin: "160px 0px" },
  );
  trendChartObserver.observe(trendChartHostRef.value);
}

// 卡片在 v-for 里，字符串 ref 会被收成数组，这里用函数 ref 直接拿单个元素
function setTrendHost(el: unknown) {
  trendChartHostRef.value = (el as HTMLElement | null) || null;
}

// 趋势卡可能被隐藏 / 在编辑态里重新显示，宿主节点换了要重新观察
watch(trendChartHostRef, () => scheduleTrendChartRender());

// ============================================================================
// 大屏模式（design §5 F8）
// - <html> 挂 dd-screen-mode：本文件末尾的全局样式隐藏侧栏、顶栏与所有操作按钮，字号 ×1.25；
// - Fullscreen API 可用时进全屏；退出全屏（含浏览器自己处理的 Esc）即退出大屏；
// - 每 30 秒刷新全部数据；navigator.wakeLock 可用时防止息屏。
// ============================================================================
const screenMode = ref(false);
const clockText = ref("");
const clockDate = ref("");
let clockTimer: number | null = null;
let screenRefreshTimer: number | null = null;
let wakeLock: any = null;

function tickClock() {
  const now = new Date();
  const pad = (n: number) => String(n).padStart(2, "0");
  clockText.value = `${pad(now.getHours())}:${pad(now.getMinutes())}:${pad(now.getSeconds())}`;
  clockDate.value = heroDateLabel.value;
}

async function requestWakeLock() {
  try {
    const nav = navigator as any;
    if (screenMode.value && nav.wakeLock && document.visibilityState === "visible") {
      wakeLock = await nav.wakeLock.request("screen");
    }
  } catch {
    // 省电模式 / 权限策略会拒绝，拒绝了就算了
  }
}

function refreshEverything() {
  loadDashboardPage();
  void upcomingRef.value?.refresh();
}

async function enterScreenMode() {
  if (screenMode.value) return;
  stopEditing();
  screenMode.value = true;
  document.documentElement.classList.add("dd-screen-mode");
  tickClock();
  clockTimer = window.setInterval(tickClock, 1000);
  screenRefreshTimer = window.setInterval(() => {
    if (document.visibilityState === "visible") refreshEverything();
  }, 30_000);
  void requestWakeLock();
  try {
    // iOS Safari 等不支持时直接跳过：仍然是大屏布局，只是不全屏
    if (document.documentElement.requestFullscreen && !document.fullscreenElement) {
      await document.documentElement.requestFullscreen();
    }
  } catch {
    // 用户拒绝 / 不是由点击触发：保持大屏布局，按右上角的 × 或 Esc 退出
  }
}

function exitScreenMode() {
  if (!screenMode.value) return;
  screenMode.value = false;
  document.documentElement.classList.remove("dd-screen-mode");
  if (clockTimer) clearInterval(clockTimer);
  clockTimer = null;
  if (screenRefreshTimer) clearInterval(screenRefreshTimer);
  screenRefreshTimer = null;
  try {
    wakeLock?.release?.();
  } catch {
    // 已经被系统释放过
  }
  wakeLock = null;
  if (document.fullscreenElement) {
    void document.exitFullscreen?.().catch(() => {});
  }
}

/** 退出全屏（浏览器自己的 Esc、F11、系统手势）= 退出大屏 */
function handleFullscreenChange() {
  if (!document.fullscreenElement && screenMode.value) exitScreenMode();
}

/**
 * 只在「大屏模式且没进全屏」时生效的 Esc（Fullscreen API 不可用或被拒绝的兜底）。
 * 全局快捷键归外壳统一处理；这里只在大屏期间临时挂、退出即摘，且只认 Esc。
 */
function handleScreenEsc(e: KeyboardEvent) {
  if (e.key !== "Escape" || !screenMode.value || document.fullscreenElement) return;
  // 有浮层开着时这次 Esc 只用来关浮层（命令面板 / 主题工坊 / 快捷键帮助 / EP 弹窗），不顺手退大屏。
  // 挂在捕获阶段，赶在 MainLayout 的 Esc 兜底（冒泡阶段 closeAll）与命令面板自己的 Esc 之前读到这些开关。
  // 运行中浮窗不算：大屏里它被藏起来了（RunningDock 的 html.dd-screen-mode 规则），用户看不见。
  if (
    ui.paletteOpen ||
    ui.studioOpen ||
    ui.shortcutHelpOpen ||
    ui.attentionOpen ||
    document.querySelector('.el-overlay:not([style*="display: none"])')
  ) {
    return;
  }
  exitScreenMode();
}
watch(screenMode, (on) => {
  if (on) window.addEventListener("keydown", handleScreenEsc, true);
  else window.removeEventListener("keydown", handleScreenEsc, true);
});

/** 息屏锁在页面隐藏时会被系统释放，回到前台重新申请 */
function handleVisibilityForWakeLock() {
  if (document.visibilityState === "visible" && screenMode.value) void requestWakeLock();
}

let lastUpdatedTimer: number | null = null;

onMounted(() => {
  loadDashboardPage();
  hasLoadedOnce.value = true;
  scheduleTrendChartRender();
  healthTask.value = readSavedHealthTask();
  void pickDefaultHealthTask();
  lastUpdatedTimer = window.setInterval(() => {
    lastUpdatedTick.value++;
  }, 30 * 1000);
  requestAnimationFrame(() => {
    ringReady.value = true;
  });
  document.addEventListener("fullscreenchange", handleFullscreenChange);
  document.addEventListener("visibilitychange", handleVisibilityForWakeLock);
});

onActivated(() => {
  if (skipInitialActivated.value) {
    skipInitialActivated.value = false;
  } else if (hasLoadedOnce.value) {
    loadDashboardPage();
  }
  scheduleTrendChartRender();
});

// keep-alive 切走时退出大屏与编辑态，布局复原（外壳的侧栏顶栏不能一直藏着）
onDeactivated(() => {
  exitScreenMode();
  stopEditing();
});

onBeforeUnmount(() => {
  exitScreenMode();
  stopEditing();
  stopObservingTrendChart();
  if (trendChartTimer) clearTimeout(trendChartTimer);
  if (lastUpdatedTimer) clearInterval(lastUpdatedTimer);
  if (runningReloadTimer) clearTimeout(runningReloadTimer);
  if (freshTimer) clearTimeout(freshTimer);
  window.removeEventListener("keydown", handleScreenEsc, true);
  document.removeEventListener("fullscreenchange", handleFullscreenChange);
  document.removeEventListener("visibilitychange", handleVisibilityForWakeLock);
});
</script>

<template>
  <div class="dashboard-page dd-scroll-page" :class="{ 'is-screen': screenMode, 'is-editing': editing }">
    <!-- ============ 工具条：编辑布局 / 大屏；大屏模式下隐藏 ============ -->
    <div class="dash-toolbar dash-hide-in-screen">
      <template v-if="!editing">
        <span class="dash-toolbar__hint">最近更新：{{ updatedHint }}</span>
        <button class="dash-tool" type="button" title="刷新" @click="refreshEverything">
          <el-icon :size="14"><Refresh /></el-icon><span>刷新</span>
        </button>
        <button class="dash-tool" type="button" @click="startEditing">
          <el-icon :size="14"><Grid /></el-icon><span>编辑布局</span>
        </button>
        <button v-if="!isMobile" class="dash-tool" type="button" @click="enterScreenMode">
          <el-icon :size="14"><FullScreen /></el-icon><span>大屏模式</span>
        </button>
      </template>
      <template v-else>
        <span class="dash-toolbar__hint">拖动卡片左上角的把手调整顺序，开关控制显示；只保存在本机</span>
        <button class="dash-tool" type="button" @click="resetLayout">恢复默认</button>
        <el-button type="primary" size="small" @click="stopEditing">完成</el-button>
      </template>
    </div>

    <!-- ============ 大屏时钟（右上角）============ -->
    <div v-if="screenMode" class="dash-clock">
      <span class="dash-clock__time">{{ clockText }}</span>
      <span class="dash-clock__date">{{ clockDate }}</span>
      <button class="dash-clock__exit" type="button" title="退出大屏（Esc）" @click="exitScreenMode">
        <el-icon :size="14"><Close /></el-icon>
      </button>
    </div>

    <!-- ============ Bento 栅格 ============ -->
    <section ref="bentoRef" class="dash-bento">
      <div
        v-for="(card, index) in renderedCards"
        :key="card.id"
        class="dash-item dd-rise"
        :class="[`dash-span-${card.renderSpan}`, `dash-item--${card.id}`, { 'is-hidden': layout.hidden.includes(card.id) }]"
        :style="{ '--dd-i': index, '--dash-span': card.renderSpan }"
      >
        <!-- 编辑态浮条：把手 + 名称 + 显隐开关 -->
        <div v-if="editing" class="dash-edit-bar">
          <span class="dash-drag-handle" title="拖动调整顺序"><el-icon :size="14"><Rank /></el-icon></span>
          <span class="dash-edit-bar__title">{{ card.title }}</span>
          <el-switch
            size="small"
            :model-value="!layout.hidden.includes(card.id)"
            @update:model-value="(v: string | number | boolean) => toggleCardHidden(card.id, Boolean(v))"
          />
        </div>

        <!-- ===== 总览 Hero ===== -->
        <article v-if="card.id === 'hero'" class="dash-card dash-hero dd-spot">
          <div class="dash-hero__glow" aria-hidden="true"></div>
          <div class="dash-hero__main">
            <span class="dash-eyebrow">
              <el-icon :size="13"><Calendar /></el-icon>{{ heroDateLabel }}
            </span>
            <h1 class="dash-hero__title">
              {{ greeting }}，{{ authStore.user?.username || "User" }}
              <span class="dash-hero__wave">👋</span>
            </h1>
            <p class="dash-hero__desc">
              今天已执行 <b>{{ todayLogs.toLocaleString() }}</b> 次，
              <b class="ink-accent">{{ runningTasks }}</b> 个任务正在运行，
              <template v-if="attention.count > 0">
                有 <b class="ink-warn">{{ attention.count }}</b> 件事等你看一眼。
              </template>
              <template v-else>没有需要处理的事。</template>
            </p>
            <div class="dash-hero__actions dash-hide-in-screen">
              <el-button
                v-if="canOperateTasks"
                v-magnetic
                type="primary"
                @click="router.push({ path: '/tasks', query: { create: '1' } })"
              >
                <el-icon><CirclePlus /></el-icon><span>新建任务</span>
              </el-button>
              <el-button @click="ui.openPalette()">
                <el-icon><Operation /></el-icon><span>快速操作</span>
              </el-button>
              <el-button text @click="ui.openStudio()">
                <el-icon><Brush /></el-icon><span>换个风格</span>
              </el-button>
            </div>
          </div>
          <div class="dash-health">
            <div class="dash-health__ring">
              <svg viewBox="0 0 120 120" aria-hidden="true">
                <defs>
                  <linearGradient id="dash-health-grad" x1="0" y1="0" x2="1" y2="1">
                    <stop offset="0" style="stop-color: var(--dd-accent)" />
                    <stop offset="1" style="stop-color: var(--dd-accent-2)" />
                  </linearGradient>
                </defs>
                <circle class="ring-track" cx="60" cy="60" r="50" />
                <circle
                  class="ring-value"
                  cx="60"
                  cy="60"
                  r="50"
                  pathLength="100"
                  :style="{ strokeDashoffset: ringOffset }"
                />
              </svg>
              <div class="dash-health__num">
                <b><DdCountUp :value="healthScore" /></b>
                <small>运行健康分</small>
              </div>
            </div>
            <ul class="dash-health__chips">
              <li>近 7 天成功率 {{ Math.round(sevenDayRate * 1000) / 10 }}%</li>
              <li :class="{ 'is-err': failedLogs > 0 }">今日失败 {{ failedLogs }}</li>
              <li :class="{ 'is-err': failingTaskCount > 0 }">
                {{ failingTaskCount > 0 ? `${failingTaskCount} 个连续失败` : "无连续失败" }}
              </li>
            </ul>
          </div>
        </article>

        <!-- ===== 4 个 KPI ===== -->
        <div v-else-if="card.id === 'kpis'" class="dash-kpis">
          <article
            v-for="kpi in statCards"
            :key="kpi.key"
            class="dash-card dash-kpi dd-spot dd-glass"
            :style="{ '--kc': kpi.color }"
            @click="router.push(kpi.link)"
          >
            <div class="dash-kpi__label">
              <el-icon :size="14" class="dash-kpi__icon" :class="{ 'is-spinning': kpi.key === 'running' && kpi.value > 0 }">
                <component :is="kpi.icon" />
              </el-icon>
              <span>{{ kpi.label }}</span>
            </div>
            <div class="dash-kpi__row">
              <span class="dash-kpi__num">
                <DdCountUp :value="kpi.value" :decimals="kpi.decimals" :suffix="kpi.suffix" />
              </span>
              <span v-if="kpi.delta === null" class="dash-delta is-live">实时</span>
              <span v-else-if="kpi.delta === 0" class="dash-delta is-flat">持平</span>
              <span v-else class="dash-delta" :class="kpi.delta > 0 ? 'is-up' : 'is-down'">
                {{ kpi.delta > 0 ? "+" : "" }}{{ kpi.delta }}{{ kpi.deltaSuffix }}
              </span>
            </div>
            <svg class="dash-kpi__spark" viewBox="0 0 200 40" preserveAspectRatio="none" aria-hidden="true">
              <path class="spark-area" :d="kpi.spark.area" />
              <path class="spark-line" :d="kpi.spark.line" />
              <path v-if="kpi.spark.dash" class="spark-line is-partial" :d="kpi.spark.dash" />
            </svg>
          </article>
        </div>

        <!-- ===== 执行趋势 ===== -->
        <article v-else-if="card.id === 'trend'" class="dash-card dd-spot dd-glass">
          <header class="dash-card__head">
            <h3 class="dash-card__title"><el-icon class="dash-card__icon"><DataLine /></el-icon>执行趋势</h3>
            <span class="dash-card__sub">今天还没过完，最后一段画成虚线</span>
            <span class="dash-card__spacer"></span>
            <div class="dd-seg-group dash-seg dash-hide-in-screen">
              <button
                class="dd-seg-btn"
                :class="{ 'is-active': trendRange === 7 }"
                type="button"
                @click="trendRange = 7"
              >近 7 天</button>
              <button
                class="dd-seg-btn"
                :class="{ 'is-active': trendRange === 30 }"
                type="button"
                @click="trendRange = 30"
              >近 30 天</button>
            </div>
          </header>
          <div :ref="setTrendHost" class="trend-chart-shell">
            <ExecutionTrendChart
              v-if="showTrendChart"
              :stats="dailyStats"
              :font-scale="screenMode ? 1.25 : 1"
            />
            <div v-else class="trend-chart-placeholder">
              <div class="placeholder-bar"></div>
              <div class="placeholder-bar placeholder-bar--short"></div>
            </div>
          </div>
          <!-- 原「执行统计」卡收成趋势卡底部的一行小结 -->
          <div class="trend-summary">
            <div class="trend-summary__bar">
              <span class="is-success" :style="{ width: trendSummary.successPct + '%' }"></span>
              <span class="is-failed" :style="{ width: trendSummary.failedPct + '%' }"></span>
              <span class="is-aborted" :style="{ width: trendSummary.abortedPct + '%' }"></span>
            </div>
            <div class="trend-summary__legend">
              <span>近 {{ trendRange }} 天共 <b>{{ trendSummary.total.toLocaleString() }}</b> 次</span>
              <span><i class="is-success"></i>成功 {{ trendSummary.success.toLocaleString() }}（{{ trendSummary.successPct }}%）</span>
              <span><i class="is-failed"></i>失败 {{ trendSummary.failed.toLocaleString() }}（{{ trendSummary.failedPct }}%）</span>
              <span><i class="is-aborted"></i>终止 {{ trendSummary.aborted.toLocaleString() }}</span>
              <span>平均耗时 <b>{{ formatDuration(avgDuration) }}</b></span>
            </div>
          </div>
        </article>

        <!-- ===== 需要关注 ===== -->
        <article v-else-if="card.id === 'attention'" class="dash-card dd-spot dd-glass">
          <header class="dash-card__head">
            <h3 class="dash-card__title"><el-icon class="dash-card__icon"><WarningFilled /></el-icon>需要关注</h3>
            <span class="dash-card__sub">{{ attention.count ? `${attention.count} 项` : "全部处理完了" }}</span>
          </header>
          <!-- 限制列表区最大高度并内部滚动，防止多条关注把同行「执行趋势」卡拉长留出大块空白 -->
          <div class="dash-attention-body">
            <AttentionList :limit="screenMode ? 6 : 0" />
          </div>
        </article>

        <!-- ===== 未来 24 小时 ===== -->
        <article v-else-if="card.id === 'upcoming'" class="dash-card dd-spot dd-glass">
          <header class="dash-card__head">
            <h3 class="dash-card__title"><el-icon class="dash-card__icon"><Calendar /></el-icon>未来 24 小时</h3>
            <span class="dash-card__sub">按 cron 推算 · 悬停看那一刻要跑哪些任务</span>
          </header>
          <UpcomingCard :ref="setUpcomingRef" />
        </article>

        <!-- ===== 系统资源（环形）===== -->
        <article v-else-if="card.id === 'resource'" class="dash-card dd-spot dd-glass">
          <header class="dash-card__head">
            <h3 class="dash-card__title"><el-icon class="dash-card__icon"><Cpu /></el-icon>系统资源</h3>
            <span class="dash-card__spacer"></span>
            <button
              v-if="canViewSystemDetails"
              class="dash-link dash-hide-in-screen"
              type="button"
              @click="router.push('/admin/settings')"
            >
              详情 <el-icon :size="11"><ArrowRight /></el-icon>
            </button>
          </header>
          <div class="res-rings">
            <div v-for="r in resourceRings" :key="r.key" class="res-ring">
              <div class="res-ring__box">
                <svg viewBox="0 0 80 80" aria-hidden="true">
                  <circle class="ring-track" cx="40" cy="40" r="32" />
                  <circle
                    class="ring-value"
                    cx="40"
                    cy="40"
                    r="32"
                    pathLength="100"
                    :style="{ strokeDashoffset: r.offset, stroke: r.color }"
                  />
                </svg>
                <b>{{ r.percent.toFixed(r.decimals) }}%</b>
              </div>
              <small>{{ r.label }}</small>
            </div>
          </div>
          <div class="res-meta">
            <div>
              <span>面板已运行</span>
              <b>{{ sysInfo.uptime || "-" }}</b>
            </div>
            <div>
              <span>网络 ↓ / ↑</span>
              <!-- 下行/上行拆成独立短项允许换行，1440px 四栏窄卡下不再截断上行速率 -->
              <b class="res-meta__speeds">
                <span>↓ {{ formatBytes(sysInfo.net_rx_speed) }}/s</span>
                <span>↑ {{ formatBytes(sysInfo.net_tx_speed) }}/s</span>
              </b>
            </div>
          </div>
        </article>

        <!-- ===== 实时执行流 ===== -->
        <article v-else-if="card.id === 'feed'" class="dash-card dd-spot dd-glass">
          <header class="dash-card__head">
            <h3 class="dash-card__title"><el-icon class="dash-card__icon"><Monitor /></el-icon>实时执行流</h3>
            <span class="dash-live" :class="{ 'is-on': runningTasks > 0 }">
              <span class="dash-live__dot"></span>{{ runningTasks > 0 ? `${runningTasks} 个运行中` : "空闲" }}
            </span>
            <span class="dash-card__spacer"></span>
            <div class="dd-seg-group dash-seg dash-seg--mini dash-hide-in-screen">
              <button
                v-for="opt in logFilterOptions"
                :key="opt.value"
                class="dd-seg-btn"
                :class="{ 'is-active': logFilter === opt.value }"
                type="button"
                @click="logFilter = opt.value"
              >{{ opt.label }}</button>
            </div>
          </header>
          <div v-if="filteredLogs.length === 0" class="dash-empty">暂无记录</div>
          <ul v-else class="feed-list">
            <!-- 这里刻意不给每一行做交错入场（列表随轮询整表刷新，逐行动画会规律性闪烁），
                 只给新出现 / 状态刚变的行播一次滑入 -->
            <li
              v-for="log in filteredLogs"
              :key="log.id"
              class="feed-item"
              :class="{ 'is-fresh': freshLogIds.includes(log.id) }"
            >
              <div class="feed-item__clip">
                <div class="feed-row">
                  <span class="feed-row__dot" :class="`is-${statusKind(log.status)}`"></span>
                  <b class="feed-row__name" :title="log.task_name">{{ log.task_name || "未命名任务" }}</b>
                  <span v-if="!isMobile" class="env-chip" :class="envTone(envLabel(log))">{{ envLabel(log) }}</span>
                  <span class="status-chip" :class="`is-${statusKind(log.status)}`">{{ statusText(log.status) }}</span>
                  <span class="feed-row__dur">{{ formatDuration(log.duration) }}</span>
                  <time class="feed-row__time">{{ formatRelativeTime(log.created_at) }}</time>
                  <span class="feed-row__actions dash-hide-in-screen">
                    <button class="icon-btn" type="button" title="查看日志" @click="viewLog(log)">
                      <el-icon :size="14"><View /></el-icon>
                    </button>
                    <button
                      v-if="canOperateTasks"
                      class="icon-btn"
                      type="button"
                      title="重新运行"
                      @click="rerunLog(log)"
                    >
                      <el-icon :size="14"><Refresh /></el-icon>
                    </button>
                  </span>
                </div>
              </div>
            </li>
          </ul>
          <button class="dash-link dash-link--foot dash-hide-in-screen" type="button" @click="router.push('/logs')">
            查看全部日志 <el-icon :size="11"><ArrowRight /></el-icon>
          </button>
        </article>

        <!-- ===== 任务健康画像 ===== -->
        <article v-else-if="card.id === 'health'" class="dash-card dd-spot dd-glass">
          <header class="dash-card__head">
            <h3 class="dash-card__title"><el-icon class="dash-card__icon"><Odometer /></el-icon>任务健康画像</h3>
            <span v-if="screenMode && healthTask" class="dash-card__sub">{{ healthTask.name }}</span>
            <span class="dash-card__spacer"></span>
            <el-select
              class="dash-health-select dash-hide-in-screen"
              size="small"
              filterable
              remote
              :remote-method="searchHealthTasks"
              :loading="healthSearching"
              :model-value="healthTask?.id"
              placeholder="选择任务"
              @update:model-value="selectHealthTask"
            >
              <el-option v-for="opt in healthSelectOptions" :key="opt.id" :label="opt.name" :value="opt.id" />
            </el-select>
          </header>
          <TaskHealthPanel v-if="healthTask" :task-id="healthTask.id" compact />
          <div v-else class="dash-empty">还没有可以画像的任务</div>
        </article>
      </div>
    </section>
  </div>
</template>

<style scoped lang="scss">
// 字号统一走 fs()：大屏模式把 --dash-scale 调成 1.25，全页文字一起放大（design §5 F8）
@function fs($px) {
  @return calc(#{$px} * var(--dash-scale, 1));
}

.dashboard-page {
  --dash-scale: 1;
  display: flex;
  flex-direction: column;
  gap: var(--dd-gap);
  // 预留外围缓冲留白，防止 daidai 硬投影、hover 上移与编辑态虚线框被滚动容器裁切或引发横滚条
  padding: 4px 6px 8px 4px;
  color: var(--dd-text);

  &.is-screen {
    --dash-scale: 1.25;
    // 大屏模式下为右上角 fixed 时钟预留顶部避让空间，绝不遮挡首行右侧 KPI 卡片头
    padding-top: 56px;
  }
}

// ============ 工具条 ============
.dash-toolbar {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  flex-wrap: wrap;
  gap: 8px;
  margin-bottom: calc(var(--dd-gap) * -0.4);
}

.dash-toolbar__hint {
  margin-right: auto;
  font-size: fs(12px);
  color: var(--dd-text-3);
}

.dash-tool {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: 5px 12px;
  border-radius: var(--dd-btn-radius);
  border: var(--dd-border-width, 1px) solid var(--dd-border);
  background: var(--dd-surface);
  color: var(--dd-text-2);
  font-size: fs(12.5px);
  font-weight: 500;
  cursor: pointer;
  transition:
    color var(--dd-motion-fast) var(--dd-ease-standard),
    border-color var(--dd-motion-fast) var(--dd-ease-standard);

  &:hover {
    color: var(--dd-accent-ink);
    border-color: var(--dd-accent-ring);
  }
}

// ============ Bento 栅格（原型 12 栏）============
.dash-bento {
  display: grid;
  grid-template-columns: repeat(12, minmax(0, 1fr));
  gap: var(--dd-gap);
  // 隐藏 / 重排后用后面的卡片补位，不留空洞
  grid-auto-flow: row dense;
}

.dash-item {
  position: relative;
  min-width: 0;
  display: flex;
  flex-direction: column;
  grid-column: span var(--dash-span, 12);

  > .dash-card {
    flex: 1;
  }
}

.dash-span-4 { grid-column: span 4; }
.dash-span-5 { grid-column: span 5; }
.dash-span-6 { grid-column: span 6; }
.dash-span-7 { grid-column: span 7; }
.dash-span-8 { grid-column: span 8; }
.dash-span-9 { grid-column: span 9; }
.dash-span-10 { grid-column: span 10; }
.dash-span-11 { grid-column: span 11; }
.dash-span-12 { grid-column: span 12; }

// ============ 卡片 ============
.dash-card {
  position: relative;
  min-width: 0;
  padding: var(--dd-pad);
  background: var(--dd-surface);
  border: var(--dd-border-width, 1px) solid var(--dd-border);
  border-radius: var(--dd-radius-card);
  box-shadow: var(--dd-shadow-card);
  transition:
    box-shadow var(--dd-motion-normal) var(--dd-ease-standard),
    transform var(--dd-motion-normal) var(--dd-spring, var(--dd-ease-standard));

  &:hover {
    box-shadow: var(--dd-shadow-card-hover);
  }
}

.dash-card__head {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 8px 10px;
  min-height: 30px;
  margin-bottom: 14px;
  min-width: 0;
}

.dash-card__title {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  margin: 0;
  font-family: var(--dd-font-head);
  font-size: calc(var(--dd-card-title-size) * var(--dash-scale, 1));
  font-weight: var(--dd-card-title-weight);
  color: var(--dd-text);
  white-space: nowrap;
}

.dash-card__icon {
  display: var(--dd-card-title-icon-display);
  color: var(--dd-accent);
}

.dash-card__sub {
  min-width: 0;
  font-size: fs(12px);
  color: var(--dd-text-3);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.dash-card__spacer {
  flex: 1;
}

.dash-link {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  border: none;
  background: transparent;
  cursor: pointer;
  color: var(--dd-accent-ink);
  font-size: fs(12px);
  padding: 4px 6px;
  border-radius: var(--dd-radius-control);
  transition: background-color var(--dd-motion-fast) var(--dd-ease-standard);

  &:hover {
    background: var(--dd-accent-soft);
  }

  &--foot {
    align-self: flex-start;
    margin-top: 8px;
  }
}

.dash-empty {
  padding: 28px 8px;
  text-align: center;
  font-size: fs(13px);
  color: var(--dd-text-3);
}

.dash-seg {
  flex: none;
  max-width: 100%;
  overflow-x: auto;
  scrollbar-width: none;

  &::-webkit-scrollbar {
    display: none;
  }

  .dd-seg-btn {
    padding: 4px 10px;
    font-size: fs(12px);
  }

  // 颜色、底槽、滑块全部沿用全局 .dd-seg-group / .dd-seg-btn（读 --dd-seg-* 令牌），这里只收小尺寸

  &--mini .dd-seg-btn {
    padding: 3px 8px;
  }
}

// ============ Hero ============
.dash-hero {
  display: flex;
  align-items: center;
  gap: 24px;
  min-height: 236px;
  overflow: hidden;
  background: var(--dd-hero-bg);
}

// 背后一团主色光晕，rich 档缓慢呼吸；克制 / 关闭档静止（见文件末尾的档位规则）
.dash-hero__glow {
  position: absolute;
  inset: -40% 30% -40% -20%;
  // v4.0.2（#182）：去掉原来的 filter: blur(30px)。径向渐变本身就是柔边，再模糊一次没有可见差别，
  // 却让总览卡每次重绘（数字滚动、悬停）都要重新做一遍模糊
  background: radial-gradient(closest-side, color-mix(in srgb, var(--dd-accent) 30%, transparent), transparent);
  opacity: 0.6;
  pointer-events: none;
  animation: dash-breathe 7s var(--dd-ease-io, ease-in-out) infinite alternate;
}

@keyframes dash-breathe {
  to {
    transform: translate(12%, 6%) scale(1.15);
  }
}

.dash-hero__main {
  position: relative;
  flex: 1;
  min-width: 0;
}

.dash-eyebrow {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: 4px 10px;
  border-radius: var(--dd-radius-pill);
  // 高不透明实底混色：盖住 Hero 左上角紫色渐变光晕，确保素白 clean 等全部主题对比度 ≥ 4.5:1
  background: color-mix(in srgb, var(--dd-surface-solid) 84%, var(--dd-accent));
  border: var(--dd-border-width, 1px) solid var(--dd-accent-ring);
  color: var(--dd-accent-ink);
  font-size: fs(12px);
  font-weight: 600;
  letter-spacing: 0.4px;
}

.dash-hero__title {
  margin: 14px 0 10px;
  font-family: var(--dd-font-head);
  font-size: calc(clamp(24px, 2.4vw, 36px) * var(--dash-scale, 1));
  font-weight: 700;
  line-height: 1.15;
  letter-spacing: -0.3px;
  color: var(--dd-text);
  text-shadow: var(--dd-text-glow);
  overflow-wrap: anywhere;
}

.dash-hero__wave {
  display: inline-block;
  transform-origin: 70% 80%;
}

// 挥手只在 rich 档播两次
html[data-dd-motion="rich"] .dash-hero__wave {
  animation: dash-wave 2.4s ease-in-out 1s 2;
}

@keyframes dash-wave {
  0%, 60%, 100% { transform: rotate(0); }
  10%, 30% { transform: rotate(14deg); }
  20% { transform: rotate(-8deg); }
  40% { transform: rotate(-4deg); }
  50% { transform: rotate(10deg); }
}

.dash-hero__desc {
  margin: 0;
  max-width: 520px;
  font-size: fs(14px);
  line-height: 1.6;
  color: var(--dd-text-2);

  b {
    font-variant-numeric: tabular-nums;
    color: var(--dd-text);
  }

  .ink-accent {
    color: var(--dd-accent-ink);
  }

  .ink-warn {
    color: var(--dd-warn-ink);
  }
}

.dash-hero__actions {
  display: flex;
  flex-wrap: wrap;
  gap: 10px;
  margin-top: 20px;

  :deep(.el-button + .el-button) {
    margin-left: 0;
  }

  .el-icon {
    margin-right: 6px;
  }
}

// 健康分圆环
.dash-health {
  position: relative;
  flex: none;
  width: 200px;
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 10px;
}

.dash-health__ring {
  position: relative;
  width: 148px;
  height: 148px;

  svg {
    width: 100%;
    height: 100%;
    transform: rotate(-90deg);
  }

  circle {
    fill: none;
    stroke-width: 11;
    stroke-linecap: round;
  }

  .ring-track {
    stroke: var(--dd-surface-3);
  }

  .ring-value {
    stroke: url(#dash-health-grad);
    stroke-dasharray: 100;
    transition: stroke-dashoffset var(--dd-motion-slow) var(--dd-ease-emphasized);
    filter: drop-shadow(0 0 6px var(--dd-accent-ring));
  }
}

.dash-health__num {
  position: absolute;
  inset: 0;
  display: grid;
  place-content: center;
  text-align: center;

  b {
    font-size: fs(40px);
    line-height: 1;
    font-weight: 700;
    letter-spacing: -1px;
    color: var(--dd-text);
    text-shadow: var(--dd-text-glow);
  }

  small {
    margin-top: 4px;
    font-size: fs(12px);
    color: var(--dd-text-3);
  }
}

.dash-health__chips {
  margin: 0;
  padding: 0;
  list-style: none;
  display: flex;
  flex-wrap: wrap;
  justify-content: center;
  gap: 6px;

  li {
    padding: 2px 8px;
    border-radius: var(--dd-radius-pill);
    background: var(--dd-surface-3);
    color: var(--dd-text-2);
    font-size: fs(12px);
    white-space: nowrap;

    &.is-err {
      background: color-mix(in srgb, var(--dd-err) 12%, transparent);
      color: var(--dd-err-ink);
    }
  }
}

// ============ KPI ============
.dash-kpis {
  flex: 1;
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: var(--dd-gap);
}

// 隐藏总览卡后 KPI 卡片独享整行 12 栏时，桌面端改 4 列一排铺开，视觉更均衡
.dash-item--kpis.dash-span-12 .dash-kpis {
  grid-template-columns: repeat(4, minmax(0, 1fr));
}

.dash-kpi {
  display: flex;
  flex-direction: column;
  min-height: 110px;
  padding: calc(var(--dd-pad) - 4px) calc(var(--dd-pad) - 2px) 0;
  overflow: hidden;
  cursor: pointer;
}

html[data-dd-motion="rich"] .dash-kpi:hover {
  transform: translateY(-3px);
}

.dash-kpi__label {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: fs(13px);
  color: var(--dd-text-2);
}

.dash-kpi__icon {
  color: var(--kc);

  // 有任务运行时图标转圈：纯装饰的常驻动画，v4.0.2 起只在显式丰富档转（见文件末尾「动效档位」）
}

@keyframes dash-spin {
  to {
    transform: rotate(360deg);
  }
}

.dash-kpi__row {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 4px 8px;
  margin-top: 6px;
  min-width: 0;
}

.dash-kpi__num {
  // 随视口平滑缩放，配合上方 flex-wrap 彻底避免 KPI 涨跌徽章在窄卡里被裁切
  font-size: calc(clamp(22px, 2vw, 30px) * var(--dash-scale, 1));
  line-height: 1.15;
  font-weight: 700;
  letter-spacing: -0.5px;
  color: var(--dd-text);
  text-shadow: var(--dd-text-glow);
  white-space: nowrap;
}

// 涨跌标签：文字用墨色、底用同色浅底（§7 遗留：原来直接用状态色当文字，对比度不够）
.dash-delta {
  flex: none;
  display: inline-flex;
  align-items: center;
  padding: 1px 7px;
  border-radius: var(--dd-radius-pill);
  font-size: fs(12px);
  font-weight: 600;
  white-space: nowrap;

  &.is-up {
    color: var(--dd-ok-ink);
    background: color-mix(in srgb, var(--dd-ok) 12%, transparent);
  }

  &.is-down {
    color: var(--dd-err-ink);
    background: color-mix(in srgb, var(--dd-err) 12%, transparent);
  }

  &.is-flat {
    color: var(--dd-text-2);
    background: var(--dd-surface-3);
  }

  &.is-live {
    color: var(--dd-accent-2-ink);
    background: color-mix(in srgb, var(--dd-accent-2) 12%, transparent);
  }
}

.dash-kpi__spark {
  display: block;
  width: calc(100% + 2 * (var(--dd-pad) - 2px));
  height: 40px;
  margin: auto calc((var(--dd-pad) - 2px) * -1) 0;

  .spark-line {
    fill: none;
    stroke: var(--kc);
    stroke-width: 2;
    vector-effect: non-scaling-stroke;

    &.is-partial {
      stroke-dasharray: 4 4;
      opacity: 0.8;
    }
  }

  .spark-area {
    fill: var(--kc);
    opacity: 0.16;
  }
}

// ============ 执行趋势 ============
.trend-chart-shell {
  min-height: 280px;
}

.trend-chart-placeholder {
  height: 280px;
  border-radius: var(--dd-radius-surface);
  padding: 18px;
  background: var(--dd-surface-2);
  display: flex;
  flex-direction: column;
  justify-content: flex-end;
  gap: 14px;
}

.placeholder-bar {
  height: 8px;
  border-radius: var(--dd-radius-control);
  background: color-mix(in srgb, var(--dd-accent) 15%, transparent);
  animation: dash-placeholder-pulse 1.6s ease-in-out infinite;
}

.placeholder-bar--short {
  width: 65%;
  animation-delay: 0.12s;
}

@keyframes dash-placeholder-pulse {
  0%, 100% { opacity: 0.5; }
  50% { opacity: 1; }
}

.trend-summary {
  display: flex;
  flex-direction: column;
  gap: 8px;
  margin-top: 10px;
  padding-top: 12px;
  border-top: 1px dashed var(--dd-divider, var(--dd-border));
}

.trend-summary__bar {
  display: flex;
  height: 6px;
  border-radius: var(--dd-radius-pill);
  background: var(--dd-surface-3);
  overflow: hidden;

  span {
    height: 100%;
    transition: width var(--dd-motion-normal) var(--dd-ease-standard);
  }
}

.trend-summary__legend {
  display: flex;
  flex-wrap: wrap;
  gap: 6px 16px;
  font-size: fs(12px);
  color: var(--dd-text-2);

  b {
    color: var(--dd-text);
    font-variant-numeric: tabular-nums;
  }

  i {
    display: inline-block;
    width: 8px;
    height: 8px;
    margin-right: 5px;
    // 图例色块固定 2px：≤12px 的小色标不吃圆角令牌，否则圆角模式下会变成和状态灯一样的正圆
    border-radius: 2px;
    vertical-align: 0;
  }
}

// 占比条与图例色块共用的状态色（图形用实色令牌）
.trend-summary .is-success { background: var(--dd-chart-ok); }
.trend-summary .is-failed { background: var(--dd-err); }
.trend-summary .is-aborted { background: var(--dd-warn); }

// ============ 需要关注 ============
// 封顶 360px 并内部滚动，防止关注项过多时把同行左侧「执行趋势」卡撑出巨幅空白
.dash-attention-body {
  max-height: 360px;
  overflow-y: auto;
  min-height: 0;
}

// ============ 系统资源 ============
.res-rings {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 8px;
  text-align: center;
}

.res-ring__box {
  position: relative;
  width: 100%;
  max-width: 96px;
  margin: 0 auto;
  aspect-ratio: 1;

  svg {
    width: 100%;
    height: 100%;
    transform: rotate(-90deg);
  }

  circle {
    fill: none;
    stroke-width: 8;
    stroke-linecap: round;
  }

  .ring-track {
    stroke: var(--dd-surface-3);
  }

  .ring-value {
    stroke-dasharray: 100;
    transition:
      stroke-dashoffset var(--dd-motion-slow) var(--dd-ease-emphasized),
      stroke var(--dd-motion-normal) var(--dd-ease-standard);
  }

  b {
    position: absolute;
    inset: 0;
    display: grid;
    place-content: center;
    // 用 clamp 收敛环内数字字号，避免 100.0% 在 4 栏窄卡圆环内碰边
    font-size: calc(clamp(12px, 1vw, 15px) * var(--dash-scale, 1));
    line-height: 1;
    letter-spacing: -0.3px;
    font-weight: 700;
    color: var(--dd-text);
    font-variant-numeric: tabular-nums;
  }
}

.res-ring small {
  display: block;
  margin-top: 4px;
  font-size: fs(12px);
  color: var(--dd-text-3);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.res-meta {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 8px;
  margin-top: 16px;

  > div {
    min-width: 0;
    padding: 10px 12px;
    border-radius: var(--dd-radius-surface);
    background: var(--dd-surface-2);
    border: var(--dd-border-width, 1px) solid var(--dd-border);
    font-size: fs(12px);
    color: var(--dd-text-3);
  }

  b {
    display: block;
    margin-top: 2px;
    font-size: fs(14px);
    color: var(--dd-text);
    font-variant-numeric: tabular-nums;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }

  // 网速项允许在窄卡下自然折成两行展示 ↓ / ↑，不再在 1440px 下截断上行速率
  .res-meta__speeds {
    display: flex;
    flex-wrap: wrap;
    gap: 2px 8px;
    white-space: normal;
    overflow: visible;
    text-overflow: clip;
    line-height: 1.35;

    span {
      white-space: nowrap;
    }
  }
}

// ============ 实时执行流 ============
.dash-live {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  flex: none;
  font-size: fs(12px);
  font-weight: 600;
  color: var(--dd-text-3);

  &.is-on {
    color: var(--dd-ok-ink);
  }
}

// 白名单：形状承载语义的状态灯，两种圆角模式下都固定圆形
.dash-live__dot {
  width: 7px;
  height: 7px;
  border-radius: 50%;
  background: var(--dd-text-3);

  // 「实时」圆点的闪烁同样只在丰富档（文件末尾「动效档位」），其余档位是常亮的绿点
  .is-on & {
    background: var(--dd-ok);
  }
}

@keyframes dash-pulse {
  0%, 100% { opacity: 1; }
  50% { opacity: 0.35; }
}

.feed-list {
  margin: 0;
  padding: 0;
  list-style: none;
  display: flex;
  flex-direction: column;
}

.feed-item {
  display: grid;
  grid-template-rows: 1fr;

  &.is-fresh {
    animation: dash-feed-in 0.7s var(--dd-ease-emphasized) both;

    .feed-row {
      background: linear-gradient(90deg, var(--dd-accent-soft), transparent 70%);
    }
  }
}

@keyframes dash-feed-in {
  from {
    grid-template-rows: 0fr;
    opacity: 0;
  }
  to {
    grid-template-rows: 1fr;
    opacity: 1;
  }
}

.feed-item__clip {
  overflow: hidden;
  min-height: 0;
}

.feed-row {
  display: flex;
  align-items: center;
  gap: 10px;
  min-height: 44px;
  padding: 0 4px;
  border-bottom: 1px dashed var(--dd-divider, var(--dd-border));
  transition: background-color var(--dd-motion-normal) var(--dd-ease-standard);

  .feed-item:last-child & {
    border-bottom: 0;
  }
}

// 状态灯：形状承载语义，固定圆形
.feed-row__dot {
  flex: none;
  width: 8px;
  height: 8px;
  border-radius: 50%;

  &.is-success { background: var(--dd-ok); }
  &.is-failed { background: var(--dd-err); }
  &.is-aborted { background: var(--dd-warn); }
  &.is-running { background: var(--dd-accent); }
}

.feed-row__name {
  flex: 1;
  min-width: 0;
  font-size: fs(13.5px);
  font-weight: 550;
  color: var(--dd-text);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.feed-row__dur {
  flex: none;
  min-width: 46px;
  text-align: right;
  font-family: var(--dd-font-mono);
  font-size: fs(12px);
  color: var(--dd-text-2);
  white-space: nowrap;
}

.feed-row__time {
  flex: none;
  width: 72px;
  text-align: right;
  font-size: fs(12px);
  color: var(--dd-text-3);
  white-space: nowrap;
}

.feed-row__actions {
  flex: none;
  display: inline-flex;
  gap: 2px;
}

// 执行状态 chip：不折行（修「运行中」在窄列里折成两行），文字用墨色
.status-chip {
  flex: none;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  min-width: 44px;
  padding: 1px 9px;
  border-radius: var(--dd-radius-pill);
  font-size: fs(11.5px);
  font-weight: 600;
  line-height: 1.5;
  white-space: nowrap;

  &.is-success {
    color: var(--dd-ok-ink);
    background: color-mix(in srgb, var(--dd-ok) 12%, transparent);
  }
  &.is-failed {
    color: var(--dd-err-ink);
    background: color-mix(in srgb, var(--dd-err) 12%, transparent);
  }
  &.is-aborted {
    color: var(--dd-warn-ink);
    background: color-mix(in srgb, var(--dd-warn) 12%, transparent);
  }
  &.is-running {
    color: var(--dd-accent-ink);
    background: color-mix(in srgb, var(--dd-accent) 12%, transparent);
  }
}

// 环境标记：标签而不是状态灯 → control 档；文字用墨色（§7 遗留）
.env-chip {
  flex: none;
  max-width: 96px;
  padding: 1px 7px;
  border-radius: var(--dd-radius-control);
  font-family: var(--dd-font-mono);
  font-size: fs(11px);
  font-weight: 500;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;

  &.is-ok {
    color: var(--dd-ok-ink);
    background: color-mix(in srgb, var(--dd-ok) 10%, transparent);
  }
  &.is-warn {
    color: var(--dd-warn-ink);
    background: color-mix(in srgb, var(--dd-warn) 10%, transparent);
  }
  &.is-accent {
    color: var(--dd-accent-ink);
    background: color-mix(in srgb, var(--dd-accent) 10%, transparent);
  }
  &.is-muted {
    color: var(--dd-text-2);
    background: var(--dd-surface-3);
  }
}

.icon-btn {
  width: 26px;
  height: 26px;
  border-radius: var(--dd-radius-control);
  border: none;
  background: transparent;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  cursor: pointer;
  color: var(--dd-text-3);
  transition:
    background-color var(--dd-motion-fast) var(--dd-ease-standard),
    color var(--dd-motion-fast) var(--dd-ease-standard);

  &:hover {
    background: var(--dd-surface-3);
    color: var(--dd-accent-ink);
  }
}

// ============ 任务健康 ============
.dash-health-select {
  width: 180px;
  flex: none;
}

// ============ 编辑布局 ============
// 改为文档流顶部条（不再 absolute 覆盖卡片顶部），彻底避免编辑态压挡卡片标题与 KPI 大数字
.dash-edit-bar {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 8px;
  padding: 6px 10px;
  border-radius: var(--dd-radius-control);
  background: var(--dd-overlay);
  border: var(--dd-border-width, 1px) solid var(--dd-accent-ring);
  box-shadow: var(--dd-shadow-popper);
  font-size: fs(12.5px);
  color: var(--dd-text);
}

.dash-edit-bar__title {
  flex: 1;
  min-width: 0;
  font-weight: 600;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.dash-drag-handle {
  display: inline-flex;
  padding: 2px;
  color: var(--dd-text-2);
  cursor: grab;
  touch-action: none;
}

.is-editing .dash-item {
  outline: 1.5px dashed var(--dd-accent-ring);
  outline-offset: 3px;
  border-radius: var(--dd-radius-card);

  // 编辑态卡片内容不响应点击，避免拖动时误触跳转
  > .dash-card,
  > .dash-kpis {
    pointer-events: none;
  }
}

.dash-item.is-hidden {
  opacity: 0.45;
}

.dash-item--ghost {
  opacity: 0.35;
}

// ============ 大屏时钟 ============
.dash-clock {
  position: fixed;
  top: 14px;
  right: 22px;
  z-index: 20;
  display: flex;
  align-items: baseline;
  gap: 10px;
  padding: 6px 12px;
  border-radius: var(--dd-radius-pill);
  background: var(--dd-overlay);
  border: var(--dd-border-width, 1px) solid var(--dd-border);
  box-shadow: var(--dd-shadow-popper);
}

.dash-clock__time {
  font-family: var(--dd-font-mono);
  font-size: 22px;
  font-weight: 700;
  color: var(--dd-text);
  font-variant-numeric: tabular-nums;
}

.dash-clock__date {
  font-size: 13px;
  color: var(--dd-text-3);
}

// 退出按钮平时几乎看不见，悬停时出现（触屏设备没有 Esc，给一个退出口）
.dash-clock__exit {
  align-self: center;
  display: inline-flex;
  padding: 3px;
  border: none;
  border-radius: 50%;
  background: transparent;
  color: var(--dd-text-3);
  cursor: pointer;
  opacity: 0.25;
  transition: opacity var(--dd-motion-fast) var(--dd-ease-standard);

  .dash-clock:hover & {
    opacity: 1;
  }
}

// 大屏模式：卡片里的操作按钮全部隐藏
.is-screen .dash-hide-in-screen {
  display: none !important;
}

// 大屏模式：共享的「需要关注」列表跟着放大（组件本身按 1 倍字号写）
.is-screen :deep(.attn-row__text b) {
  font-size: fs(13.5px);
}

.is-screen :deep(.attn-row__text small) {
  font-size: fs(12px);
}

// ============ 动效档位 ============
// 光晕呼吸只在 rich 档；克制 / 关闭档静止（关闭档的全局规则本来也会去掉，这里给克制档补上）
html:not([data-dd-motion="rich"]) .dash-hero__glow {
  animation: none;
}

// v4.0.2（#182）：常驻的装饰动画只在显式丰富档（rich 只能手动选）：KPI 运行中图标转圈、「实时」圆点闪烁。
// 骨架屏的 dash-placeholder-pulse 是加载反馈，不在此列
html[data-dd-motion="rich"] .dash-kpi__icon.is-spinning {
  animation: dash-spin 2.4s linear infinite;
}

html[data-dd-motion="rich"] .is-on .dash-live__dot {
  animation: dash-pulse 1.6s ease-in-out infinite;
}

// ============ 响应式 ============
// 12 栏降级断点由 1100px 提至 1280px：侧栏展开时 1101~1280px 窗口内容区不足 1040px，
// 提前切单列可彻底避免 Hero / KPI / 资源 / 执行流被挤成窄卡
@media (max-width: 1280px) {
  .dash-bento .dash-item {
    grid-column: span 12;
  }

  .dash-kpis {
    grid-template-columns: repeat(4, minmax(0, 1fr));
  }
}

@media (max-width: 900px) {
  .dash-hero {
    flex-direction: column;
    align-items: stretch;
  }

  .dash-health {
    width: auto;
    flex-direction: row;
    justify-content: center;
    flex-wrap: wrap;
  }

  .dash-kpis,
  .dash-item--kpis.dash-span-12 .dash-kpis {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}

@media (max-width: 768px) {
  .dash-toolbar__hint {
    display: none;
  }

  .dash-card__sub {
    display: none;
  }

  .dash-health-select {
    width: 100%;
  }

  .feed-row__dur {
    display: none;
  }

  // 移动端释放时间列固定 72px 宽度，把空间让给任务名
  .feed-row__time {
    width: auto;
  }
}
</style>

<!-- 大屏模式的外壳部分（非 scoped）：侧栏、顶栏属于外壳，这里只用 <html> 上的类把它们藏起来，不改 MainLayout -->
<style lang="scss">
html.dd-screen-mode {
  .layout-aside,
  .layout-header,
  .layout-mobile-drawer {
    display: none !important;
  }

  .layout-main {
    padding: 20px 24px;
  }
}
</style>
