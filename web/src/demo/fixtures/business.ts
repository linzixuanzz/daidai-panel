import type {
  DemoApiCallLog,
  DemoBackupFile,
  DemoConfigItem,
  DemoDbState,
  DemoDependency,
  DemoEnvVar,
  DemoIPWhitelist,
  DemoLoginLog,
  DemoLogKind,
  DemoNotifyChannel,
  DemoOpenApp,
  DemoScriptFile,
  DemoSession,
  DemoSSHKey,
  DemoSubLog,
  DemoSubscription,
  DemoTask,
  DemoTaskLog,
  DemoTaskView,
  DemoUser,
} from '../types'
import {
  LOG_STATUS_ABORTED,
  LOG_STATUS_FAILED,
  LOG_STATUS_RUNNING,
  LOG_STATUS_SUCCESS,
  TASK_STATUS_DISABLED,
  TASK_STATUS_ENABLED,
  TASK_STATUS_RUNNING,
} from '../types'
import configsFixture from './configs.json'
import { DEMO_CONFIG_SCRIPT, DEMO_SCRIPT_DIRS, DEMO_SCRIPT_FILES } from './scripts'

/**
 * 在线演示 Demo 的业务剧本。
 *
 * 剧本设定：一台跑了一阵子的中性运维面板 —— 备份、清理、巡检、报表。
 * ⚠️ 刻意避开任何具体的薅羊毛平台名：公网 Demo 是项目门面。
 *
 * 【最重要的一条：数字必须自洽】
 * 仪表盘的每一个数字都不是这里写死的常量，而是从下面生成出来的 tasks / logs
 * 现算的（见 db.ts 的 buildDashboard / buildSystemStats）。
 * 上一轮踩过的坑就是「成功率恒 100%」—— 各处各写一份常量，互相对不上。
 * 所以这里只生成【事实】（14 个任务、约 4800 条执行日志），
 * 所有汇总数字一律由事实推导，新建任务 / 手动运行 / 删日志之后也不会自相矛盾。
 * 仪表盘卡片、趋势图当天那一格、执行统计占比条、执行日志列表这四处
 * 之所以能互相印证，靠的就是这条结构约束，改这个文件时不要破坏它。
 */

const DAY_MS = 24 * 60 * 60 * 1000
const HOUR_MS = 60 * 60 * 1000
const MINUTE_MS = 60 * 1000

/** 执行日志覆盖的天数。仪表盘趋势图只有 7 / 30 两档，铺满 30 天两档就都不会出现空白格。 */
const LOG_HISTORY_DAYS = 30

/**
 * 「今天」这一天的失败样本占比，以及触发这条整形规则的最小样本量。
 *
 * 为什么需要它：日志只铺到「此刻」为止，所以今天永远是一个小样本。
 * 各任务的 failRate 都在 2%~5% 量级，小样本下很容易一条失败都没抽中 ——
 * 于是仪表盘「成功率」（今日口径）显示 100.0%，而紧挨着的「执行统计」（近 7 天口径）
 * 却写着「失败 55 (4.9%)」。两个口径本来就不同，但并排放在一起像数据打架。
 *
 * 处理办法是**只改事实、不改汇总**：播种完之后把今天的失败条数校准到约 5%
 * （与全量 7 天的自然失败率同量级），四处汇总照旧全部现算，因此仍然互相印证。
 *
 * ⚠️ 残留边界：本地时间刚过零点的约 20 分钟内，今天累计执行数本来就只有个位数
 *    （这是「日志只铺到此刻」的算术结果，除非编造未来时间的日志，否则无解）。
 *    低于 TODAY_SHAPING_MIN_LOGS 就不整形 —— 8 条里挑 1 条失败已经是 87.5%，
 *    再往下就会出现 66% 这种吓人的数字，比 100% 更糟。
 *    那个时间窗内成功率可能仍是 100%，但此时「今日执行」也只有几条，
 *    读起来是「这一天刚开始」而不是数据矛盾。
 */
const TODAY_FAILURE_RATIO = 0.05
const TODAY_SHAPING_MIN_LOGS = 8

/**
 * 确定性 PRNG（mulberry32）。
 *
 * 不用 Math.random 的原因：失败率、时长这些必须可复现 ——
 * 否则「重置演示数据」前后同一天的成功率会跳变，看起来像数据在乱算。
 */
function mulberry32(seed: number) {
  let state = seed >>> 0
  return function next() {
    state = (state + 0x6d2b79f5) >>> 0
    let t = Math.imul(state ^ (state >>> 15), 1 | state)
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296
  }
}

function iso(ms: number) {
  return new Date(ms).toISOString()
}

/** 取本地时区的当天零点，与后端按 now.Location() 划分自然日的口径一致 */
function startOfLocalDay(ms: number) {
  const date = new Date(ms)
  date.setHours(0, 0, 0, 0)
  return date.getTime()
}

// ---------------------------------------------------------------------------
// 任务
// ---------------------------------------------------------------------------

interface TaskSeed {
  id: number
  name: string
  command: string
  taskType: string
  cron: string
  status: number
  labels: string[]
  pinned?: boolean
  timeout?: number
  channelId?: number | null
  notifyOnFailure?: boolean
  notifyOnSuccess?: boolean
  pythonVersion?: string
  createdDaysAgo: number
  /**
   * 每天在这些小时执行；空数组表示不参与历史日志生成（禁用/手动/开机任务）。
   *
   * ⚠️ runHours × runMinutes 必须与上面的 cron 字段等价。列表页的「下次执行时间」
   *    是由 cron 现算的（db.ts 的 estimateNextRun），两者对不上就会出现
   *    「历史日志每 10 分钟一条、下次执行却排在 6 小时后」这种自相矛盾。
   */
  runHours: number[]
  /** 命中小时内的分钟点，默认 [0] */
  runMinutes?: number[]
  /** 只在这些星期几执行（0=周日）。留空表示每天 */
  runWeekdays?: number[]
  /** 每 N 天执行一次（手动任务用），与 runWeekdays 互斥 */
  runEveryDays?: number
  durMin: number
  durMax: number
  failRate: number
  timeoutRate: number
  abortRate: number
}

// 与 cron 的 `0 <step 小时> * * *` 等价的小时集合
function everyHours(step: number): number[] {
  const hours: number[] = []
  for (let hour = 0; hour < 24; hour += step) hours.push(hour)
  return hours
}

// 与 cron 的 `<step 分钟> * * * *` 等价的分钟集合
function everyMinutes(step: number): number[] {
  const minutes: number[] = []
  for (let minute = 0; minute < 60; minute += step) minutes.push(minute)
  return minutes
}

const ALL_HOURS = everyHours(1)

/**
 * 14 个任务，状态覆盖：成功 / 失败 / 运行中 / 已禁用 / 置顶。
 *
 * command 全部指向 fixtures/scripts.ts 里真实存在的文件 —— 演示时从任务点到脚本页，
 * 文件是打得开的，不会出现「任务指着一个不存在的脚本」。
 *
 * 【关于执行节律】访客可能在本地时间的任意时刻打开演示站，而日志只铺到「此刻」。
 * 如果所有任务都是「每天一两次」，凌晨打开时今天就只有两三条日志，
 * 仪表盘的今日口径会退化成一个无意义的小样本。所以这里刻意做了三件事：
 *   1. 巡检 / 采集 / 同步这几个任务按 20、30、60、120 分钟的真实节律跑
 *      （合计约 160 次/天），让「今天」的样本量随时间平滑累积，而不是等到白天才有数；
 *   2. 频次**摊在四个任务上**而不是堆在一个高频任务上 —— 仪表盘「最近执行」只显示 5 行，
 *      单个任务频次过高会让那 5 行变成同一个任务名刷屏，是另一种「一眼假」；
 *   3. 备份 / 归档 / 清理这几个夜间维护任务排在 00:00–00:05，
 *      模拟真实运维面板的「零点批处理窗口」，让一天的最开头就有一批记录。
 */
const TASK_SEEDS: TaskSeed[] = [
  {
    id: 1, name: '每日数据备份', command: 'bash ops/backup_data.sh', taskType: 'cron',
    cron: '0 0 * * *', status: TASK_STATUS_ENABLED, labels: ['备份'], pinned: true,
    timeout: 3600, channelId: 1, notifyOnFailure: true, createdDaysAgo: 96,
    runHours: [0], durMin: 92, durMax: 214, failRate: 0.02, timeoutRate: 0, abortRate: 0.01,
  },
  {
    id: 2, name: '清理临时文件', command: 'bash ops/clean_tmp.sh', taskType: 'cron',
    cron: '5 0 * * *', status: TASK_STATUS_ENABLED, labels: ['清理'],
    timeout: 600, channelId: null, notifyOnFailure: true, createdDaysAgo: 96,
    runHours: [0], runMinutes: [5], durMin: 1.4, durMax: 4.8, failRate: 0.01, timeoutRate: 0, abortRate: 0,
  },
  {
    id: 3, name: '同步配置文件', command: 'python3 ops/sync_config.py', taskType: 'cron',
    cron: '0 * * * *', status: TASK_STATUS_ENABLED, labels: ['配置'],
    timeout: 300, channelId: 1, notifyOnFailure: true, pythonVersion: '3.12', createdDaysAgo: 88,
    runHours: ALL_HOURS, durMin: 2.1, durMax: 9.6, failRate: 0.03, timeoutRate: 0.01, abortRate: 0.01,
  },
  {
    id: 4, name: '检查 SSL 证书', command: 'bash monitor/check_ssl.sh', taskType: 'cron',
    cron: '0 7 * * *', status: TASK_STATUS_ENABLED, labels: ['监控'],
    timeout: 300, channelId: 2, notifyOnFailure: true, createdDaysAgo: 74,
    runHours: [7], durMin: 0.9, durMax: 3.4, failRate: 0.05, timeoutRate: 0, abortRate: 0,
  },
  {
    id: 5, name: '更新 IP 数据库', command: 'node data/update_ipdb.mjs', taskType: 'cron',
    cron: '0 23 * * 0', status: TASK_STATUS_ENABLED, labels: ['数据'],
    timeout: 1800, channelId: null, notifyOnFailure: true, createdDaysAgo: 61,
    runHours: [23], runWeekdays: [0], durMin: 46, durMax: 132, failRate: 0.08, timeoutRate: 0.04, abortRate: 0,
  },
  {
    id: 6, name: '数据库优化', command: 'bash ops/db_optimize.sh', taskType: 'cron',
    cron: '0 4 * * 0', status: TASK_STATUS_DISABLED, labels: ['数据库'],
    timeout: 3600, channelId: 1, notifyOnFailure: true, createdDaysAgo: 55,
    runHours: [], durMin: 38, durMax: 92, failRate: 0, timeoutRate: 0, abortRate: 0,
  },
  {
    id: 7, name: '发送运营日报', command: 'python3 report/daily_report.py', taskType: 'cron',
    cron: '0 9 * * 1-5', status: TASK_STATUS_ENABLED, labels: ['通知'],
    timeout: 300, channelId: 1, notifyOnFailure: true, notifyOnSuccess: true,
    pythonVersion: '3.12', createdDaysAgo: 47,
    runHours: [9], runWeekdays: [1, 2, 3, 4, 5], durMin: 2.4, durMax: 6.8, failRate: 0.02, timeoutRate: 0, abortRate: 0,
  },
  {
    // 每 20 分钟一次的健康巡检：它和「监控指标采集」「同步配置文件」「对象存储同步」
    // 一起撑起「今天」的样本量，让访客无论几点打开，今日执行数都不会退化成个位数。
    id: 8, name: '系统健康检查', command: 'bash monitor/health_check.sh', taskType: 'cron',
    cron: '*/20 * * * *', status: TASK_STATUS_RUNNING, labels: ['监控'], pinned: true,
    timeout: 180, channelId: 2, notifyOnFailure: true, createdDaysAgo: 47,
    runHours: ALL_HOURS, runMinutes: everyMinutes(20),
    durMin: 0.6, durMax: 2.9, failRate: 0.05, timeoutRate: 0.005, abortRate: 0.004,
  },
  {
    // 手动任务 + 运行中：「运行中的任务 = 2」这张卡片需要两条运行中的记录，
    // 而手动任务在任意时刻处于运行中都说得通（有人刚点了「运行」）。
    // 反过来把两条都挂在高频巡检任务上，仪表盘「最近执行」的 5 行里就有 2 行是同一个名字。
    id: 9, name: '离线报表导出', command: 'python3 report/export_offline.py', taskType: 'manual',
    cron: '', status: TASK_STATUS_RUNNING, labels: ['报表'],
    timeout: 1800, channelId: null, pythonVersion: '3.11', createdDaysAgo: 33,
    runHours: [15], runEveryDays: 3, durMin: 24, durMax: 88, failRate: 0.06, timeoutRate: 0, abortRate: 0.12,
  },
  {
    id: 10, name: '日志归档压缩', command: 'bash ops/archive_logs.sh', taskType: 'cron',
    cron: '2 0 * * *', status: TASK_STATUS_ENABLED, labels: ['清理'],
    timeout: 1200, channelId: null, notifyOnFailure: true, createdDaysAgo: 33,
    runHours: [0], runMinutes: [2], durMin: 6.2, durMax: 27, failRate: 0.02, timeoutRate: 0, abortRate: 0,
  },
  {
    id: 11, name: '监控指标采集', command: 'python3 monitor/collect_metrics.py', taskType: 'cron',
    cron: '*/30 * * * *', status: TASK_STATUS_ENABLED, labels: ['监控'],
    timeout: 300, channelId: 2, notifyOnFailure: true, pythonVersion: '3.12', createdDaysAgo: 26,
    runHours: ALL_HOURS, runMinutes: everyMinutes(30),
    durMin: 1.1, durMax: 5.2, failRate: 0.04, timeoutRate: 0, abortRate: 0.004,
  },
  {
    id: 12, name: '证书到期巡检', command: 'python3 monitor/cert_expiry.py', taskType: 'cron',
    cron: '0 8 * * *', status: TASK_STATUS_ENABLED, labels: ['监控', 'subscription:1'],
    timeout: 600, channelId: 2, notifyOnFailure: true, pythonVersion: '3.12', createdDaysAgo: 19,
    runHours: [8], durMin: 12, durMax: 41, failRate: 0.16, timeoutRate: 0.03, abortRate: 0,
  },
  {
    id: 13, name: '对象存储同步', command: 'bash ops/sync_object_storage.sh', taskType: 'cron',
    cron: '15 */2 * * *', status: TASK_STATUS_ENABLED, labels: ['备份', 'subscription:1'],
    timeout: 2400, channelId: 1, notifyOnFailure: true, createdDaysAgo: 19,
    runHours: everyHours(2), runMinutes: [15],
    durMin: 18, durMax: 96, failRate: 0.05, timeoutRate: 0.02, abortRate: 0.02,
  },
  {
    id: 14, name: '缓存预热', command: 'node ops/warm_cache.mjs', taskType: 'startup',
    cron: '', status: TASK_STATUS_DISABLED, labels: ['缓存'],
    timeout: 600, channelId: null, createdDaysAgo: 0,
    runHours: [], durMin: 3.2, durMax: 11, failRate: 0, timeoutRate: 0, abortRate: 0,
  },
]

function buildTasks(now: number): DemoTask[] {
  return TASK_SEEDS.map((seed, index) => {
    const createdAt = now - seed.createdDaysAgo * DAY_MS - index * 37 * MINUTE_MS
    return {
      id: seed.id,
      name: seed.name,
      command: seed.command,
      python_version: seed.pythonVersion ?? '',
      cron_expression: seed.cron,
      task_type: seed.taskType,
      status: seed.status,
      labels: [...seed.labels],
      // last_run_at / last_run_status 在日志生成之后统一回填，保证与日志表一致
      last_run_at: null,
      last_run_status: null,
      timeout: seed.timeout ?? 0,
      success_exit_codes: '0',
      random_delay_seconds: null,
      max_retries: seed.failRate > 0.05 ? 2 : 0,
      retry_interval: seed.failRate > 0.05 ? 60 : 0,
      notify_on_failure: seed.notifyOnFailure ?? false,
      notify_on_success: seed.notifyOnSuccess ?? false,
      notify_on_abort: false,
      notification_channel_id: seed.channelId ?? null,
      depends_on: null,
      // 全部落 0，与真实库里「升级后存量行默认 0」的状态一致：
      // 默认排序里 list_order 排在 sort_order 之前，全平手时顺序完全由下面的 sort_order 决定，
      // 也就是加这个字段前后演示站的初始列表逐条不变。拖一次之后这一桶才会被重编号成 10/20/30…
      list_order: 0,
      sort_order: index,
      is_pinned: seed.pinned ?? false,
      pid: seed.status === TASK_STATUS_RUNNING ? 20000 + seed.id : null,
      log_path: null,
      last_running_time: null,
      task_before: null,
      task_after: null,
      allow_multiple_instances: false,
      stop_schedule: '',
      created_at: iso(createdAt),
      updated_at: iso(createdAt + 12 * HOUR_MS),
    }
  })
}

// ---------------------------------------------------------------------------
// 执行日志
// ---------------------------------------------------------------------------

function pickKind(roll: number, seed: TaskSeed): DemoLogKind {
  const abort = seed.abortRate
  const timeout = abort + seed.timeoutRate
  const fail = timeout + seed.failRate
  if (roll < abort) return 'abort'
  if (roll < timeout) return 'timeout'
  if (roll < fail) return 'fail'
  return 'ok'
}

export function logStatusOfKind(kind: DemoLogKind): number {
  switch (kind) {
    case 'ok':
      return LOG_STATUS_SUCCESS
    case 'abort':
      return LOG_STATUS_ABORTED
    case 'running':
      return LOG_STATUS_RUNNING
    default:
      // fail 与 timeout 在服务端是同一个状态：超时是面板杀进程，退出码同样非零
      return LOG_STATUS_FAILED
  }
}

type SeedLogRow = Omit<DemoTaskLog, 'id'>

/**
 * 从候选里等距挑 count 条（候选需按时间升序），让被改写的样本分散在一天里，
 * 而不是全挤在同一个小时 —— 执行日志列表翻两页看不到一条失败，也是一种「假」。
 */
function pickSpread(candidates: SeedLogRow[], count: number): SeedLogRow[] {
  if (count <= 0 || candidates.length === 0) return []
  if (count >= candidates.length) return [...candidates]

  const picked: SeedLogRow[] = []
  for (let i = 0; i < count; i += 1) {
    const index = Math.min(
      candidates.length - 1,
      Math.floor(((i + 0.5) * candidates.length) / count),
    )
    picked.push(candidates[index]!)
  }
  return picked
}

/**
 * 把「今天」的失败条数校准到 TODAY_FAILURE_RATIO 附近。
 *
 * 只改事实（某几条日志的 kind / status），不碰任何汇总口径：
 * 仪表盘卡片、趋势图当天那一格、执行统计、执行日志列表照旧全部从这批日志现算，
 * 所以四处仍然互相印证 —— 点开被改写的那条日志，正文也会按 fail 模板渲染出报错行。
 *
 * 超时（timeout）那几条不参与回改：它们的 duration 等于任务的超时阈值，
 * 改成成功会出现「成功，但耗时正好等于超时时间」这种一眼假的样本。
 */
function shapeTodayFailures(rows: SeedLogRow[], now: number) {
  const todayStartIso = iso(startOfLocalDay(now))
  // started_at 都是同一套定宽 ISO 串，字典序即时间序，不用再解析成 Date
  const todays = rows
    .filter((row) => row.kind !== 'running' && row.started_at >= todayStartIso)
    .sort((left, right) => (left.started_at < right.started_at ? -1 : left.started_at > right.started_at ? 1 : 0))
  if (todays.length < TODAY_SHAPING_MIN_LOGS) return

  const target = Math.max(1, Math.round(todays.length * TODAY_FAILURE_RATIO))
  const failed = todays.filter((row) => row.status === LOG_STATUS_FAILED).length
  if (failed === target) return

  if (failed < target) {
    for (const picked of pickSpread(todays.filter((row) => row.kind === 'ok'), target - failed)) {
      picked.kind = 'fail'
      picked.status = LOG_STATUS_FAILED
    }
    return
  }

  for (const picked of pickSpread(todays.filter((row) => row.kind === 'fail'), failed - target)) {
    picked.kind = 'ok'
    picked.status = LOG_STATUS_SUCCESS
  }
}

/**
 * 按任务的执行节律铺出近 30 天的执行日志。
 *
 * 这批日志是仪表盘、执行统计、执行日志页三处数字的【唯一来源】，
 * 谁都不再各写一份常量。今天只铺到当前时刻为止，所以「今日执行」天然小于整天量；
 * 收尾处的 shapeTodayFailures 负责把这个小样本的失败占比拉回可信区间。
 */
function buildLogs(now: number, tasks: DemoTask[]): DemoTaskLog[] {
  const rows: SeedLogRow[] = []
  const todayStart = startOfLocalDay(now)
  const taskNameById = new Map(tasks.map((task) => [task.id, task.name] as const))

  for (const seed of TASK_SEEDS) {
    if (seed.runHours.length === 0) continue
    const taskName = taskNameById.get(seed.id) ?? seed.name
    const runMinutes = seed.runMinutes ?? [0]
    // 每个任务一条独立随机流，改一个任务的节律不会把别的任务的历史全部洗牌
    const random = mulberry32(seed.id * 7919 + 104729)

    for (let dayOffset = LOG_HISTORY_DAYS - 1; dayOffset >= 0; dayOffset -= 1) {
      const dayStart = todayStart - dayOffset * DAY_MS
      const weekday = new Date(dayStart).getDay()

      if (seed.runWeekdays && !seed.runWeekdays.includes(weekday)) continue
      if (seed.runEveryDays && dayOffset % seed.runEveryDays !== 0) continue

      for (const hour of seed.runHours) {
        for (const minute of runMinutes) {
          // 真实执行不会精确落在整点：加 0-40 秒的确定性抖动。
          // 上限刻意压得比较小 —— 零点批处理窗口那几条要在刚过零点时就已经落库，
          // 抖动太大会让「今日执行」在 00:00 之后空好几分钟。
          const jitter = Math.floor(random() * 40) * 1000
          const startedAt = dayStart + hour * HOUR_MS + minute * MINUTE_MS + jitter
          if (startedAt > now) continue

          const kind = pickKind(random(), seed)
          const duration = kind === 'timeout'
            ? seed.timeout ?? 600
            : Math.round((seed.durMin + random() * (seed.durMax - seed.durMin)) * 10) / 10

          rows.push({
            task_id: seed.id,
            task_name: taskName,
            status: logStatusOfKind(kind),
            duration,
            started_at: iso(startedAt),
            ended_at: iso(startedAt + duration * 1000),
            kind,
          })
        }
      }
    }
  }

  shapeTodayFailures(rows, now)

  // 运行中的两条：与 tasks 里 status=running 的两个任务一一对应。
  // 少了它们，「运行中的任务 = 2」这张卡片就和执行日志页的筛选结果对不上。
  //
  // 已跑时长按各自正常耗时的量级取（durMax 的八成），不写死秒数：
  // 一个历史上每次都只跑 1 秒的巡检任务，显示「已运行 3 分 34 秒」会和它自己的日志打架。
  for (const task of tasks) {
    if (task.status !== TASK_STATUS_RUNNING) continue
    const seed = TASK_SEEDS.find((item) => item.id === task.id)
    const startedAt = now - Math.max(1, Math.round((seed?.durMax ?? 60) * 0.8)) * 1000
    rows.push({
      task_id: task.id,
      task_name: task.name,
      status: LOG_STATUS_RUNNING,
      duration: null,
      started_at: iso(startedAt),
      ended_at: null,
      kind: 'running',
    })
  }

  // 按开始时间升序编号（最早的 id=1），再按接口口径倒序返回，与后端
  // `Order("task_logs.started_at DESC")` + 自增主键一致。
  // 定宽 ISO 串的字典序就是时间序，这里用裸比较而不是 localeCompare：
  // 后者走 Intl，几千条日志排一次要慢一个数量级，而且是在首屏路径上。
  rows.sort((left, right) => (left.started_at < right.started_at ? -1 : left.started_at > right.started_at ? 1 : 0))
  return rows
    .map((row, index) => ({ ...row, id: index + 1 }))
    .reverse()
}

/** 用刚生成的日志回填任务的 last_run_at / last_run_status / last_running_time */
function applyLastRunFromLogs(tasks: DemoTask[], logs: DemoTaskLog[]) {
  // logs 已按时间倒序，第一条命中的就是最近一次
  const seen = new Set<number>()
  for (const log of logs) {
    if (seen.has(log.task_id)) continue
    seen.add(log.task_id)
    const task = tasks.find((item) => item.id === log.task_id)
    if (!task) continue
    task.last_run_at = log.started_at
    task.last_run_status = log.status === LOG_STATUS_RUNNING ? null : log.status
    task.last_running_time = log.duration
  }
}

// ---------------------------------------------------------------------------
// 环境变量
// ---------------------------------------------------------------------------

interface EnvSeed {
  name: string
  value: string
  remarks: string
  group: string
  enabled?: boolean
  pinned?: boolean
}

/**
 * 22 条环境变量（去重后 20 个不同的变量名），覆盖 6 个分组 + 凭据类的脱敏样本。
 * 分组依次是：通用 4 条、通知 5 条、备份 3 条、多账号 3 条、存储 4 条、监控 3 条
 * （S3_ENDPOINT / S3_BUCKET 的 group 写成「存储,备份」，同时算进这两个分组，不额外新增分组）。
 *
 * 其中 ACCOUNT_TOKEN 刻意重复 3 条（22 与 20 的差就来自这里），
 * 用于演示 v3.2.5 的「按变量名筛选」—— 少了同名样本，变量名下拉里每一项都是 (1)，
 * 「同名多条 → 一键收敛」这个功能在演示站上完全看不出存在的理由。
 *
 * ⚠️ 增删条目时把上面这几个数字一起改掉：后来人判断「演示数据规模 / 分组覆盖」只有这一处依据。
 *
 * 凭据一律写成「前缀 + 星号」的形态：演示站是公开的，
 * 就算是编的字符串，也不要写成看起来像真 key 的样子。
 */
const ENV_SEEDS: EnvSeed[] = [
  { name: 'TZ', value: 'Asia/Shanghai', remarks: '容器与脚本统一时区', group: '通用', pinned: true },
  { name: 'LOG_LEVEL', value: 'info', remarks: '脚本日志级别：debug/info/warn/error', group: '通用', pinned: true },
  { name: 'REQUEST_TIMEOUT', value: '10', remarks: '脚本内 HTTP 请求超时（秒）', group: '通用' },
  { name: 'HTTP_PROXY', value: 'http://127.0.0.1:7890', remarks: '仅拉取外部数据源时使用', group: '通用', enabled: false },

  { name: 'NOTIFY_WEBHOOK_URL', value: 'https://hooks.example.com/services/T000000/B000000/****************', remarks: 'lib/notify.py 统一出口', group: '通知' },
  { name: 'SMTP_HOST', value: 'smtp.example.com', remarks: '运维告警邮件', group: '通知' },
  { name: 'SMTP_PORT', value: '465', remarks: 'SSL 端口', group: '通知' },
  { name: 'SMTP_USER', value: 'ops-bot@example.com', remarks: '发件账号', group: '通知' },
  { name: 'SMTP_PASSWORD', value: 'app-****************', remarks: '授权码，非登录密码', group: '通知' },

  { name: 'BACKUP_TARGET_DIR', value: '/opt/app/backups', remarks: '本地归档目录', group: '备份' },
  { name: 'BACKUP_RETENTION_DAYS', value: '14', remarks: '本地归档保留天数', group: '备份' },
  { name: 'BACKUP_ENCRYPT_KEY', value: 'bk_live_****************', remarks: '归档加密口令', group: '备份' },

  // 刻意造的三条**同名**变量（多账号场景）。面板允许同名，运行时会按 name 分组用 & 拼给脚本，
  // v3.2.5 的「按变量名筛选」就是为这种场景做的 —— 少了它们，演示站的变量名下拉全是 (1)，
  // 「同名多条 → 一键收敛」这个功能存在的理由在 demo 上完全看不出来。
  { name: 'ACCOUNT_TOKEN', value: 'acc_a_****************', remarks: '主账号', group: '多账号' },
  { name: 'ACCOUNT_TOKEN', value: 'acc_b_****************', remarks: '备用账号', group: '多账号' },
  { name: 'ACCOUNT_TOKEN', value: 'acc_c_****************', remarks: '测试账号', group: '多账号', enabled: false },

  { name: 'S3_ENDPOINT', value: 'https://s3.example.com', remarks: '对象存储接入点', group: '存储,备份' },
  { name: 'S3_BUCKET', value: 'ops-archive', remarks: '归档桶', group: '存储,备份' },
  { name: 'S3_ACCESS_KEY_ID', value: 'AKIA****************', remarks: '只授予该桶的写权限', group: '存储' },
  { name: 'S3_SECRET_ACCESS_KEY', value: '****************************************', remarks: '与 AK 成对使用', group: '存储' },

  { name: 'MONITOR_TARGETS', value: 'https://example.com/healthz,https://api.example.com/healthz', remarks: '健康检查目标，逗号分隔', group: '监控' },
  { name: 'MONITOR_ALERT_THRESHOLD', value: '85', remarks: '磁盘/内存告警阈值（%）', group: '监控' },
  { name: 'SSL_WATCH_DOMAINS', value: 'example.com,api.example.com,cdn.example.com', remarks: '证书巡检域名', group: '监控' },
]

function buildEnvs(now: number): DemoEnvVar[] {
  const pinned = ENV_SEEDS.filter((seed) => seed.pinned)
  const normal = ENV_SEEDS.filter((seed) => !seed.pinned)
  const ordered = [...pinned, ...normal]

  let pinnedPosition = 0
  let normalPosition = 0

  return ordered.map((seed, index) => {
    // position 只在同一个 sort_order 桶内比较，两个桶各自从 1000 起步、步长 1000，
    // 与服务端 envPositionStep 一致，拖拽重排时才不会算出奇怪的间隔
    const sortOrder = seed.pinned ? 1 : 0
    let position: number
    if (seed.pinned) {
      pinnedPosition += 1000
      position = pinnedPosition
    } else {
      normalPosition += 1000
      position = normalPosition
    }
    const createdAt = now - (60 - index) * DAY_MS - index * 41 * MINUTE_MS
    return {
      id: index + 1,
      name: seed.name,
      value: seed.value,
      remarks: seed.remarks,
      enabled: seed.enabled ?? true,
      position,
      sort_order: sortOrder,
      group: seed.group,
      created_at: iso(createdAt),
      updated_at: iso(createdAt + 3 * DAY_MS),
    }
  })
}

// ---------------------------------------------------------------------------
// 订阅 / SSH 密钥
// ---------------------------------------------------------------------------

function buildSubscriptions(now: number): DemoSubscription[] {
  const base = {
    whitelist: '',
    blacklist: '',
    depend_on: '',
    pre_script: '',
    hook_script: '',
    status: 0,
    sub_path: '',
    ssh_key_id: null,
    auth_type: '',
    auth_username: '',
    has_auth_token: false,
    alias: '',
    force_overwrite: true,
    // 覆盖拉取策略默认跟随全局；下面第 2 条订阅单独设成 preserve，用来展示列表里的策略标签
    overwrite_mode: 'inherit',
    // 完整检出默认关闭：三条种子订阅都保持稀疏检出，与真实面板的默认值一致
    // （演示站开着它没有任何可见差异，反而会让编辑弹窗里的开关默认亮起，误导用户以为这是默认档）
    full_checkout: false,
    // 同步任务三态默认跟随全局；下面第 1 条设成「强制关闭删除」用来展示列表标签，
    // 第 3 条两项都强制开启，第 2 条不覆盖、留作 inherit 那一档的展示行。
    auto_add_task_mode: 'inherit',
    auto_del_task_mode: 'inherit',
  }

  return [
    {
      ...base,
      id: 1,
      name: '运维脚本仓库',
      type: 'git-repo',
      url: 'https://github.com/example/ops-scripts.git',
      branch: 'main',
      schedule: '0 4 * * *',
      whitelist: 'monitor/,ops/',
      blacklist: 'tests/',
      auto_add_task: true,
      auto_del_task: false,
      // 旧布尔值的等价三态：建任务强制开、删任务强制关（移动端卡片会挂「不删任务」标签）
      auto_add_task_mode: 'enabled',
      auto_del_task_mode: 'disabled',
      enabled: true,
      last_pull_at: iso(now - 9 * HOUR_MS),
      save_dir: 'subscriptions/ops-scripts',
      hook_script: 'python3 -m pip install -r requirements.txt --quiet',
      created_at: iso(now - 19 * DAY_MS),
      updated_at: iso(now - 9 * HOUR_MS),
    },
    {
      ...base,
      id: 2,
      name: '监控脚本集',
      type: 'git-repo',
      url: 'https://gitee.com/example/monitor-kit.git',
      branch: 'release',
      schedule: '0 5 * * 1',
      auto_add_task: false,
      auto_del_task: false,
      // 刻意不覆盖 auto_add_task_mode / auto_del_task_mode：沿用 base 的 inherit，
      // 让演示站三条订阅正好凑齐 inherit / enabled / disabled 三档形态。
      enabled: false,
      last_pull_at: iso(now - 6 * DAY_MS),
      save_dir: 'subscriptions/monitor-kit',
      auth_type: 'ssh',
      ssh_key_id: 1,
      overwrite_mode: 'preserve',
      created_at: iso(now - 14 * DAY_MS),
      updated_at: iso(now - 6 * DAY_MS),
    },
    {
      ...base,
      id: 3,
      name: '日报模板（单文件）',
      type: 'single-file',
      url: 'https://raw.githubusercontent.com/example/ops-scripts/main/report/daily_report.py',
      branch: '',
      schedule: '30 6 * * *',
      auto_add_task: true,
      auto_del_task: true,
      // 旧布尔值的等价三态：两项都强制开启。单文件订阅同样跑同步任务，
      // 所以这两个字段对 single-file 一样有意义（不像 overwrite_mode / full_checkout 只对 git 生效）。
      auto_add_task_mode: 'enabled',
      auto_del_task_mode: 'enabled',
      enabled: true,
      last_pull_at: iso(now - 31 * HOUR_MS),
      save_dir: 'report',
      alias: 'daily_report.py',
      created_at: iso(now - 8 * DAY_MS),
      updated_at: iso(now - 31 * HOUR_MS),
    },
  ]
}

function buildSubLogs(now: number, subscriptions: DemoSubscription[]): DemoSubLog[] {
  const rows: DemoSubLog[] = []
  let id = 0

  const script: Array<{ subId: number; hoursAgo: number; status: number; duration: number; content: string }> = [
    { subId: 1, hoursAgo: 9, status: 0, duration: 4.2, content: '拉取成功，更新 3 个文件，新增任务 0 个' },
    { subId: 1, hoursAgo: 33, status: 0, duration: 3.8, content: '拉取成功，更新 1 个文件' },
    { subId: 1, hoursAgo: 57, status: 1, duration: 30.4, content: 'git fetch 超时：连接远端仓库失败' },
    { subId: 1, hoursAgo: 81, status: 0, duration: 4.6, content: '拉取成功，无变更' },
    { subId: 2, hoursAgo: 144, status: 0, duration: 6.1, content: '拉取成功，更新 5 个文件，新增任务 2 个' },
    { subId: 2, hoursAgo: 312, status: 0, duration: 5.7, content: '拉取成功，无变更' },
    { subId: 3, hoursAgo: 31, status: 0, duration: 1.2, content: '单文件更新完成：report/daily_report.py' },
    { subId: 3, hoursAgo: 55, status: 0, duration: 1.1, content: '内容未变化，跳过写入' },
  ]

  for (const item of script) {
    const subscription = subscriptions.find((sub) => sub.id === item.subId)
    id += 1
    rows.push({
      id,
      subscription_id: item.subId,
      subscription_name: subscription?.name ?? '',
      status: item.status,
      content: item.content,
      duration: item.duration,
      created_at: iso(now - item.hoursAgo * HOUR_MS),
    })
  }

  return rows.sort((left, right) => right.created_at.localeCompare(left.created_at))
}

function buildSSHKeys(now: number): DemoSSHKey[] {
  return [
    { id: 1, name: '内网 Git 只读密钥', created_at: iso(now - 44 * DAY_MS), updated_at: iso(now - 44 * DAY_MS) },
    { id: 2, name: '备份服务器部署密钥', created_at: iso(now - 21 * DAY_MS), updated_at: iso(now - 12 * DAY_MS) },
  ]
}

// ---------------------------------------------------------------------------
// 通知渠道 / Open API / 用户
// ---------------------------------------------------------------------------

function buildChannels(now: number): DemoNotifyChannel[] {
  return [
    {
      id: 1,
      name: '运维告警邮件',
      type: 'email',
      config: JSON.stringify({
        smtp_host: 'smtp.example.com',
        smtp_port: '465',
        smtp_ssl: 'auto',
        smtp_user: 'ops-bot@example.com',
        smtp_pass: 'app-****************',
        to: 'oncall@example.com,ops@example.com',
        from: 'ops-bot@example.com',
      }),
      push_scope: 'default',
      enabled: true,
      today_send_count: 3,
      last_test_at: iso(now - 5 * DAY_MS),
      last_test_status: 'success',
      created_at: iso(now - 90 * DAY_MS),
      updated_at: iso(now - 5 * DAY_MS),
    },
    {
      id: 2,
      name: '值班机器人',
      type: 'webhook',
      config: JSON.stringify({ url: 'https://hooks.example.com/services/T000000/B000000/****************' }),
      push_scope: 'default',
      enabled: true,
      today_send_count: 7,
      last_test_at: iso(now - 2 * DAY_MS),
      last_test_status: 'success',
      created_at: iso(now - 62 * DAY_MS),
      updated_at: iso(now - 2 * DAY_MS),
    },
    {
      // push_scope=bound 的渠道不参与广播，只有被任务显式绑定才推送。
      // 留一条 bound 是为了让任务表单里的「仅绑定」标注有东西可标。
      id: 3,
      name: '备份结果专用推送',
      type: 'bark',
      config: JSON.stringify({
        key: '****************',
        server: 'https://api.day.app',
        group: '运维',
        level: 'timeSensitive',
      }),
      push_scope: 'bound',
      enabled: true,
      today_send_count: 1,
      last_test_at: iso(now - 11 * DAY_MS),
      last_test_status: 'success',
      created_at: iso(now - 28 * DAY_MS),
      updated_at: iso(now - 11 * DAY_MS),
    },
    {
      id: 4,
      name: '（停用）旧告警群',
      type: 'telegram',
      config: JSON.stringify({
        token: '0000000000:AA****************',
        chat_id: '-1000000000000',
        api_host: 'https://api.telegram.org',
      }),
      push_scope: 'default',
      enabled: false,
      today_send_count: 0,
      last_test_at: iso(now - 40 * DAY_MS),
      last_test_status: 'failed',
      created_at: iso(now - 71 * DAY_MS),
      updated_at: iso(now - 40 * DAY_MS),
    },
  ]
}

function buildOpenApps(now: number): DemoOpenApp[] {
  return [
    {
      id: 1,
      name: '监控看板',
      app_key: 'ak_demo_dashboard0001',
      app_secret: 'sk_demo_****************************',
      scopes: 'tasks,logs,system',
      enabled: true,
      rate_limit: 120,
      call_count: 268,
      created_at: iso(now - 52 * DAY_MS),
      updated_at: iso(now - 6 * DAY_MS),
    },
    {
      id: 2,
      name: 'CI 触发器',
      app_key: 'ak_demo_citrigger00002',
      app_secret: 'sk_demo_****************************',
      scopes: 'tasks,scripts',
      enabled: true,
      rate_limit: 30,
      call_count: 41,
      created_at: iso(now - 24 * DAY_MS),
      updated_at: iso(now - 24 * DAY_MS),
    },
    {
      id: 3,
      name: '（停用）旧同步脚本',
      app_key: 'ak_demo_legacysync003',
      app_secret: 'sk_demo_****************************',
      scopes: 'envs',
      enabled: false,
      rate_limit: 0,
      call_count: 0,
      created_at: iso(now - 76 * DAY_MS),
      updated_at: iso(now - 30 * DAY_MS),
    },
  ]
}

function buildApiCallLogs(now: number, apps: DemoOpenApp[]): DemoApiCallLog[] {
  const rows: DemoApiCallLog[] = []
  const endpoints: Array<[string, string, number]> = [
    ['/api/v1/system/dashboard', 'GET', 200],
    ['/api/v1/tasks', 'GET', 200],
    ['/api/v1/logs', 'GET', 200],
    ['/api/v1/tasks/9/run', 'PUT', 200],
    ['/api/v1/tasks', 'GET', 429],
    ['/api/v1/scripts/tree', 'GET', 200],
  ]

  let id = 0
  for (const app of apps) {
    if (!app.enabled) continue
    const random = mulberry32(app.id * 6151 + 13)
    const count = app.id === 1 ? 24 : 9
    for (let i = 0; i < count; i += 1) {
      const endpoint = endpoints[Math.floor(random() * endpoints.length)] ?? endpoints[0]!
      id += 1
      rows.push({
        id,
        app_id: app.id,
        app_name: app.name,
        endpoint: endpoint[0],
        method: endpoint[1],
        status: endpoint[2],
        duration: Math.round((4 + random() * 180) * 10) / 10,
        ip: `10.0.${app.id}.${12 + Math.floor(random() * 40)}`,
        created_at: iso(now - Math.floor(random() * 20 * HOUR_MS)),
      })
    }
  }

  return rows.sort((left, right) => right.created_at.localeCompare(left.created_at))
}

function buildUsers(now: number): DemoUser[] {
  // avatar_url 必须全部留空：MainLayout 的三处头像 <img> 没有 @error 兜底
  return [
    {
      id: 1, username: 'demo', role: 'admin', enabled: true, avatar_url: '',
      last_login_at: iso(now - 3 * MINUTE_MS),
      created_at: iso(now - 96 * DAY_MS), updated_at: iso(now - 3 * MINUTE_MS),
    },
    {
      id: 2, username: 'ops', role: 'operator', enabled: true, avatar_url: '',
      last_login_at: iso(now - 2 * DAY_MS),
      created_at: iso(now - 61 * DAY_MS), updated_at: iso(now - 2 * DAY_MS),
    },
    {
      id: 3, username: 'viewer', role: 'viewer', enabled: false, avatar_url: '',
      last_login_at: iso(now - 27 * DAY_MS),
      created_at: iso(now - 40 * DAY_MS), updated_at: iso(now - 27 * DAY_MS),
    },
  ]
}

// ---------------------------------------------------------------------------
// 任务视图 / 依赖 / 安全 / 备份
// ---------------------------------------------------------------------------

function buildTaskViews(now: number): DemoTaskView[] {
  return [
    {
      id: 1,
      name: '监控巡检',
      filters: JSON.stringify([{ field: 'labels', operator: 'contains', value: '监控' }]),
      sort_rules: JSON.stringify([{ field: 'name', direction: 'asc' }]),
      hidden: false,
      sort_order: 0,
      created_at: iso(now - 30 * DAY_MS),
      updated_at: iso(now - 30 * DAY_MS),
    },
    {
      id: 2,
      name: '备份与清理',
      filters: JSON.stringify([{ field: 'labels', operator: 'contains', value: '备份' }]),
      sort_rules: '[]',
      hidden: false,
      sort_order: 1,
      created_at: iso(now - 22 * DAY_MS),
      updated_at: iso(now - 22 * DAY_MS),
    },
    {
      id: 3,
      name: '订阅任务',
      filters: JSON.stringify([{ field: 'subscription', operator: 'contains', value: '运维脚本仓库' }]),
      sort_rules: '[]',
      hidden: false,
      sort_order: 2,
      created_at: iso(now - 17 * DAY_MS),
      updated_at: iso(now - 17 * DAY_MS),
    },
  ]
}

function buildDeps(now: number): DemoDependency[] {
  const seeds: Array<[string, string, string, string, number]> = [
    ['python', 'requests', '3.12', 'installed', 90],
    ['python', 'urllib3', '3.12', 'installed', 90],
    ['python', 'pyyaml', '3.12', 'installed', 74],
    ['python', 'python-dateutil', '3.12', 'installed', 61],
    ['python', 'croniter', '3.12', 'installed', 47],
    ['python', 'boto3', '3.12', 'installed', 19],
    ['python', 'rich', '3.11', 'installed', 33],
    ['python', 'pandas', '3.11', 'failed', 12],
    ['nodejs', 'axios', '', 'installed', 88],
    ['nodejs', 'dayjs', '', 'installed', 88],
    ['nodejs', 'node-fetch', '', 'installed', 55],
    ['nodejs', 'cheerio', '', 'installed', 26],
    ['linux', 'curl', '', 'installed', 96],
    ['linux', 'openssl', '', 'installed', 96],
    ['linux', 'sqlite3', '', 'installed', 74],
    ['linux', 'awscli', '', 'installed', 19],
  ]

  return seeds.map((seed, index) => {
    const createdAt = now - seed[4] * DAY_MS - index * 17 * MINUTE_MS
    return {
      id: index + 1,
      type: seed[0],
      name: seed[1],
      python_version: seed[2],
      status: seed[3],
      created_at: iso(createdAt),
      updated_at: iso(createdAt + 90 * 1000),
    }
  })
}

function buildLoginLogs(now: number): DemoLoginLog[] {
  const seeds: Array<[string, number, number, string, string]> = [
    ['demo', 3 * MINUTE_MS, 0, '198.51.100.24', '登录成功'],
    ['demo', 21 * HOUR_MS, 0, '198.51.100.24', '登录成功'],
    ['ops', 2 * DAY_MS, 0, '203.0.113.71', '登录成功'],
    ['ops', 2 * DAY_MS + 4 * MINUTE_MS, 1, '203.0.113.71', '密码错误'],
    ['demo', 4 * DAY_MS, 0, '198.51.100.24', '登录成功'],
    ['unknown', 6 * DAY_MS, 1, '192.0.2.155', '用户不存在'],
    ['viewer', 27 * DAY_MS, 0, '203.0.113.18', '登录成功'],
  ]

  return seeds.map((seed, index) => ({
    id: seeds.length - index,
    user_id: seed[0] === 'demo' ? 1 : seed[0] === 'ops' ? 2 : 3,
    username: seed[0],
    ip: seed[3],
    client_name: index === 2 ? '呆呆面板 APP' : 'Chrome · Windows',
    user_agent: 'Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36',
    method: '密码登录',
    status: seed[2],
    message: seed[4],
    created_at: iso(now - seed[1]),
  }))
}

function buildSessions(now: number): DemoSession[] {
  return [
    {
      id: 1, user_id: 1, username: 'demo', client_type: 'web', client_name: 'Chrome · Windows',
      ip: '198.51.100.24', user_agent: 'Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36',
      expires_at: iso(now + 6 * DAY_MS), created_at: iso(now - 3 * MINUTE_MS),
    },
    {
      id: 2, user_id: 2, username: 'ops', client_type: 'app', client_name: '呆呆面板 APP',
      ip: '203.0.113.71', user_agent: 'Dart/3.5 (dart:io)',
      expires_at: iso(now + 4 * DAY_MS), created_at: iso(now - 2 * DAY_MS),
    },
  ]
}

function buildIPWhitelist(now: number): DemoIPWhitelist[] {
  return [
    { id: 1, ip: '198.51.100.0/24', remarks: '办公网出口', created_at: iso(now - 58 * DAY_MS) },
    { id: 2, ip: '203.0.113.71', remarks: '值班同事家庭宽带', created_at: iso(now - 12 * DAY_MS) },
  ]
}

function buildBackups(now: number): DemoBackupFile[] {
  return [
    { name: 'daidai-backup-auto-20260819-0300.tgz', size: 8_412_672, created_at: iso(now - 29 * HOUR_MS) },
    { name: 'daidai-backup-auto-20260818-0300.tgz', size: 8_355_840, created_at: iso(now - 53 * HOUR_MS) },
    { name: 'daidai-backup-升级前手动备份.tgz', size: 7_982_080, created_at: iso(now - 9 * DAY_MS) },
  ]
}

// ---------------------------------------------------------------------------
// 脚本
// ---------------------------------------------------------------------------

function buildScriptFiles(now: number): DemoScriptFile[] {
  return DEMO_SCRIPT_FILES.map((seed) => ({
    path: seed.path,
    content: seed.content,
    mtime: Math.floor((now - seed.daysAgo * DAY_MS) / 1000),
  }))
}

// ---------------------------------------------------------------------------
// 组装
// ---------------------------------------------------------------------------

/**
 * 从 Go registry 导出的 configs.json 取出「全新安装态」的配置 map。
 *
 * ⚠️ configs.json 是【生成产物】（server/cmd/gen-demo-fixtures），不要手改。
 *    这里只做一次深拷贝：import 进来的 JSON 是模块级单例，
 *    设置页保存后会就地改值，不拷贝的话「重置演示数据」也恢复不回去。
 */
function buildConfigs(): Record<string, DemoConfigItem> {
  const raw = configsFixture as unknown as { data?: Record<string, DemoConfigItem> }
  return structuredClone(raw.data ?? {})
}

/**
 * 生成一份全新的演示数据。
 *
 * 每次调用都以「现在」为基准重算所有时间，所以无论访客什么时候打开演示站，
 * 看到的都是一台刚刚还在干活的面板，而不是停在某个固定日期的死数据。
 */
export function createSeedState(now: number = Date.now()): DemoDbState {
  const tasks = buildTasks(now)
  const logs = buildLogs(now, tasks)
  applyLastRunFromLogs(tasks, logs)

  const subscriptions = buildSubscriptions(now)
  const openApps = buildOpenApps(now)
  const envs = buildEnvs(now)
  const channels = buildChannels(now)
  const users = buildUsers(now)
  const taskViews = buildTaskViews(now)
  const deps = buildDeps(now)
  const scriptFiles = buildScriptFiles(now)
  const sshKeys = buildSSHKeys(now)
  const subLogs = buildSubLogs(now, subscriptions)
  const apiCallLogs = buildApiCallLogs(now, openApps)
  const loginLogs = buildLoginLogs(now)
  const sessions = buildSessions(now)
  const ipWhitelist = buildIPWhitelist(now)

  return {
    tasks,
    taskViews,
    logs,
    envs,
    subscriptions,
    subLogs,
    sshKeys,
    channels,
    openApps,
    apiCallLogs,
    users,
    deps,
    loginLogs,
    sessions,
    ipWhitelist,
    backups: buildBackups(now),
    scriptDirs: [...DEMO_SCRIPT_DIRS],
    scriptFiles,
    configScript: DEMO_CONFIG_SCRIPT,
    configs: buildConfigs(),
    seq: {
      task: maxId(tasks),
      taskView: maxId(taskViews),
      log: maxId(logs),
      env: maxId(envs),
      subscription: maxId(subscriptions),
      subLog: maxId(subLogs),
      sshKey: maxId(sshKeys),
      channel: maxId(channels),
      openApp: maxId(openApps),
      apiCallLog: maxId(apiCallLogs),
      user: maxId(users),
      dep: maxId(deps),
      loginLog: maxId(loginLogs),
      session: maxId(sessions),
      ipWhitelist: maxId(ipWhitelist),
    },
  }
}

function maxId(rows: Array<{ id: number }>) {
  return rows.reduce((max, row) => (row.id > max ? row.id : max), 0)
}
