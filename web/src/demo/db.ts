import type {
  DemoDbState,
  DemoEnvVar,
  DemoSubscription,
  DemoTask,
  DemoTaskLog,
} from './types'
import {
  LOG_STATUS_ABORTED,
  LOG_STATUS_FAILED,
  LOG_STATUS_RUNNING,
  LOG_STATUS_SUCCESS,
  TASK_STATUS_DISABLED,
  TASK_STATUS_ENABLED,
  TASK_STATUS_QUEUED,
  TASK_STATUS_RUNNING,
} from './types'
import { createSeedState, logStatusOfKind } from './fixtures/business'
import { normalizeScriptPath, splitCommandTokens } from '@/utils/taskCommandScript'

/**
 * 在线演示 Demo 的可写数据层。
 *
 * 设计要点：
 *   1. **纯内存，不做任何持久化**。演示站对外承诺的就是「刷新页面即恢复初始数据」
 *      （横幅、README、issue #96 的回复都是这么写的），而 sessionStorage
 *      在刷新后是【保留】的 —— 只有关标签页才清空，用它就会和承诺对不上。
 *      模块级变量随页面文档一起销毁，刷新即回到初始 fixture，语义天然对齐。
 *      顺带省掉了 schema 版本号、隐私模式 / 配额兜底、每次 mutation 全量序列化
 *      （fixture 已是数千条日志的量级）这一整块只为持久化存在的代码。
 *   2. **登录态不在这里**：access_token / refresh_token 在 localStorage 里，
 *      由 auth store 管理。刷新后访客仍然是登录着的，只是数据回到初始状态。
 *   3. **所有汇总数字都从这里的事实现算**，不写死常量。
 *      仪表盘、执行统计、任务列表读的是同一批 tasks / logs，
 *      所以「总执行数 1367 但日志列表 0 条」这类自相矛盾在结构上就不可能出现。
 */

const DAY_MS = 24 * 60 * 60 * 1000

let state: DemoDbState | null = null

/** 取当前数据库。第一次调用时播种，之后一直复用同一个内存对象。 */
export function db(): DemoDbState {
  if (!state) state = createSeedState()
  return state
}

/**
 * 重置成初始 fixture（横幅上的「重置演示数据」按钮走这里）。
 *
 * 刷新页面也能达到同样效果；这个按钮提供的是「会话中途主动重置」的能力。
 * 注意 createSeedState() 每次都以调用时刻为基准重算时间，所以重置之后
 * 拿到的不是同一份旧数据，而是一台「刚刚还在干活」的面板。
 */
export function resetDb() {
  state = createSeedState()
}

/** 各表共用的自增游标，保证新建对象不会和播种数据的 id 撞车 */
export function nextId(table: string): number {
  const current = db()
  const next = (current.seq[table] ?? 0) + 1
  current.seq[table] = next
  return next
}

export function nowIso() {
  return new Date().toISOString()
}

// ---------------------------------------------------------------------------
// 通用工具
// ---------------------------------------------------------------------------

/** 取本地时区的当天零点，与后端按 now.Location() 划分自然日的口径一致 */
function startOfLocalDay(ms: number) {
  const date = new Date(ms)
  date.setHours(0, 0, 0, 0)
  return date.getTime()
}

/** `MM-DD`，与后端 DailyStat 的 `day.Format("01-02")` 一致（不是 10 位日期） */
function monthDayKey(ms: number) {
  const date = new Date(ms)
  return `${String(date.getMonth() + 1).padStart(2, '0')}-${String(date.getDate()).padStart(2, '0')}`
}

function parseIntOr(raw: string | undefined, fallback: number) {
  const value = Number.parseInt(raw ?? '', 10)
  return Number.isFinite(value) ? value : fallback
}

function isTruthyFlag(raw: string | undefined) {
  const value = (raw ?? '').trim().toLowerCase()
  return value === '1' || value === 'true' || value === 'yes'
}

export interface PaginatedBody<T> {
  data: T[]
  total: number
  page: number
  page_size: number
}

/**
 * 统一的分页信封，形状照抄 server/pkg/response/response.go 的 Paginated：
 * `{ data, total, page, page_size }`，**不要再包一层**——
 * request.ts:50 的响应拦截器返回的就是这里的对象本身。
 *
 * all=1 的语义与服务端一致：一次性返回全部，page 固定 1、page_size 等于返回条数。
 */
export function paginate<T>(rows: T[], params: Record<string, string>): PaginatedBody<T> {
  if (isTruthyFlag(params['all'])) {
    return { data: rows, total: rows.length, page: 1, page_size: rows.length }
  }

  const page = Math.max(1, parseIntOr(params['page'], 1))
  let pageSize = parseIntOr(params['page_size'], 20)
  if (pageSize < 1 || pageSize > 100) pageSize = 20

  const start = (page - 1) * pageSize
  return {
    data: rows.slice(start, start + pageSize),
    total: rows.length,
    page,
    page_size: pageSize,
  }
}

function includesIgnoreCase(haystack: string, needle: string) {
  return haystack.toLowerCase().includes(needle.toLowerCase())
}

// ---------------------------------------------------------------------------
// cron 下次执行时间
// ---------------------------------------------------------------------------

// 解析一个 cron 字段。支持通配符、带步长的通配符、区间（可带步长）、逗号枚举与纯数字。
// 解析不了返回 null —— 调用方据此放弃计算 next_run_at（列表页会显示 `-`），
// 而不是瞎给一个时间：演示环境里显示错的下次执行时间比不显示更糟。
function parseCronField(field: string, min: number, max: number): Set<number> | null {
  const values = new Set<number>()

  for (const part of field.split(',')) {
    const chunk = part.trim()
    if (!chunk) return null

    const [rangePart = '', stepPart] = chunk.split('/')
    const step = stepPart === undefined ? 1 : Number.parseInt(stepPart, 10)
    if (!Number.isFinite(step) || step < 1) return null

    let from = min
    let to = max
    if (rangePart !== '*') {
      const bounds = rangePart.split('-')
      const start = Number.parseInt(bounds[0] ?? '', 10)
      if (!Number.isFinite(start)) return null
      from = start
      to = bounds.length > 1 ? Number.parseInt(bounds[1] ?? '', 10) : start
      if (!Number.isFinite(to)) return null
    }

    if (from < min || to > max || from > to) return null
    for (let value = from; value <= to; value += step) values.add(value)
  }

  return values.size > 0 ? values : null
}

/**
 * 估算接下来 count 次执行时间。
 *
 * 覆盖标准五段与「带秒」的六段（服务端两种都收，见 server/pkg/cron/cron.go 的
 * buildFields；任务表单里的 cron 模板下发的正是六段）。秒这一段直接丢掉 ——
 * 演示环境不需要精确到秒，而保留它会让「每 10 秒」这类模板在下次执行时间上
 * 显示成同一分钟内的一串重复值。`@daily` 这类别名一律返回空数组。
 *
 * 逐日筛选（先判断这一天是否命中 dom/month/dow，再在命中的日子里找时刻），
 * 所以最坏情况也只是 366 次日期判断，不会退化成逐分钟扫描。
 */
export function nextRunTimes(cronExpression: string, from: number, count: number): string[] {
  const firstLine = cronExpression
    .split(/[\r\n]+/)
    .map((line) => line.trim())
    .find((line) => line.length > 0)
  if (!firstLine) return []

  let parts = firstLine.split(/\s+/)
  if (parts.length === 6) parts = parts.slice(1)
  if (parts.length !== 5) return []

  const minutes = parseCronField(parts[0] ?? '', 0, 59)
  const hours = parseCronField(parts[1] ?? '', 0, 23)
  const monthDays = parseCronField(parts[2] ?? '', 1, 31)
  const months = parseCronField(parts[3] ?? '', 1, 12)
  const weekDays = parseCronField(parts[4] ?? '', 0, 7)
  if (!minutes || !hours || !monthDays || !months || !weekDays) return []

  // cron 里 7 也表示周日
  if (weekDays.has(7)) weekDays.add(0)

  const domRestricted = (parts[2] ?? '*') !== '*'
  const dowRestricted = (parts[4] ?? '*') !== '*'

  const sortedHours = [...hours].sort((a, b) => a - b)
  const sortedMinutes = [...minutes].sort((a, b) => a - b)

  const result: string[] = []
  const cursor = new Date(from)
  cursor.setSeconds(0, 0)

  for (let dayOffset = 0; dayOffset < 366 && result.length < count; dayOffset += 1) {
    const day = new Date(cursor.getTime() + dayOffset * DAY_MS)
    day.setHours(0, 0, 0, 0)

    if (!months.has(day.getMonth() + 1)) continue

    // 标准 cron：dom 与 dow 同时受限时取【并集】，只有一个受限时以它为准
    const domHit = monthDays.has(day.getDate())
    const dowHit = weekDays.has(day.getDay())
    let dayHit = true
    if (domRestricted && dowRestricted) dayHit = domHit || dowHit
    else if (domRestricted) dayHit = domHit
    else if (dowRestricted) dayHit = dowHit
    if (!dayHit) continue

    for (const hour of sortedHours) {
      for (const minute of sortedMinutes) {
        const candidate = new Date(day)
        candidate.setHours(hour, minute, 0, 0)
        if (candidate.getTime() > from) {
          result.push(candidate.toISOString())
          if (result.length >= count) break
        }
      }
      if (result.length >= count) break
    }
  }

  return result
}

/** 下一次执行时间；算不出来返回 null（列表页会显示 `-`，好过给一个错的时间） */
export function estimateNextRun(cronExpression: string, from: number): string | null {
  return nextRunTimes(cronExpression, from, 1)[0] ?? null
}

// ---------------------------------------------------------------------------
// 任务
// ---------------------------------------------------------------------------

export function findTask(id: number): DemoTask | undefined {
  return db().tasks.find((task) => task.id === id)
}

function splitCronExpressions(raw: string): string[] {
  return raw
    .split(/[\r\n]+/)
    .map((line) => line.trim())
    .filter((line) => line.length > 0)
}

/**
 * 把原始标签翻译成展示标签，复刻 server/handler/task_query.go 的 buildPreparedTaskLabels：
 *   - `分组:xxx` 提到最前面；
 *   - `subscription:N` 换成订阅名（订阅已删除则显示「订阅任务」）。
 * 不翻译的话列表页会直接把 `subscription:1` 当成标签画出来。
 *
 * 返回值里的 subscription 对应后端的 subscriptionLabels（只含订阅名）：
 * display 是扁平数组，订阅名和自定义标签在里面长得一模一样，前端「按类别隐藏标签」
 * 只能靠这份名单把两者分开。演示站不跟着下发，就会出现「本地能分项隐藏、演示站不能」。
 */
function buildDisplayLabels(labels: string[]): { display: string[]; subscription: string[] } {
  const current = db()
  const display: string[] = []
  const subscription: string[] = []
  // 展示标签的去重按【类别】分开，绝不共用一个集合（与后端 seenCustom / seenSubscriptions 对齐）：
  // 自定义标签可能和订阅名重名（自建标签「娱乐」+ 名为「娱乐」的订阅），共用一个集合会把两条合并成一条，
  // 前端「自定义标签 / 订阅标签」两个开关就同时作用在同一条上 ——
  // 表现为「关掉自定义藏不掉它、关掉订阅反而把它藏了」。
  // 分组名不参与这两个集合（它在 return 里单独前插），与订阅名重名时同样各留一条。
  const seenCustom = new Set<string>()
  const seenSubscription = new Set<string>()
  let groupName = ''

  const push = (label: string) => {
    const value = label.trim()
    if (!value || seenCustom.has(value)) return
    seenCustom.add(value)
    display.push(value)
  }

  for (const raw of labels) {
    const label = raw.trim()
    if (label.startsWith('分组:')) {
      const group = label.slice('分组:'.length).trim()
      if (group && !groupName) groupName = group
      continue
    }
    if (!label.startsWith('subscription:')) {
      push(label)
      continue
    }
    const subId = Number.parseInt(label.slice('subscription:'.length), 10)
    const matched = current.subscriptions.find((item) => item.id === subId)
    // 订阅源被删掉后查不到名字，退回字面量（与后端一致）
    const subName = (matched?.name ?? '').trim() || '订阅任务'
    // 两个数组用同一个订阅去重集合、同一次判定，保证 display 里的订阅项与 subscription 逐条对得上
    // （前端 classifyDisplayTaskLabels 按出现次数认领类别，多一条少一条都会错位）。
    if (seenSubscription.has(subName)) continue
    seenSubscription.add(subName)
    display.push(subName)
    subscription.push(subName)
  }

  return { display: groupName ? [groupName, ...display] : display, subscription }
}

/** 任务列表/详情下发体，字段照抄 server/model/task.go 的 ToDict() + task_query.go 的补充字段 */
export function toTaskDict(task: DemoTask): Record<string, unknown> {
  const current = db()
  const preparedLabels = buildDisplayLabels(task.labels)
  const item: Record<string, unknown> = {
    ...task,
    labels: [...task.labels],
    cron_expressions: splitCronExpressions(task.cron_expression),
    display_labels: preparedLabels.display,
    // 与后端 prepareTaskListItems 一致：订阅名再单独下发一份，前端靠它把订阅标签和自定义标签分开
    subscription_labels: preparedLabels.subscription,
  }

  if (task.notification_channel_id) {
    const channel = current.channels.find((item2) => item2.id === task.notification_channel_id)
    if (channel) {
      item['notification_channel_name'] = channel.name
      item['notification_channel_enabled'] = channel.enabled
    }
  }

  // 与服务端一致：只有「非禁用 + cron 类型 + 表达式非空」才给下次执行时间
  if (task.status !== TASK_STATUS_DISABLED && task.task_type === 'cron' && task.cron_expression) {
    const next = estimateNextRun(task.cron_expression, Date.now())
    if (next) item['next_run_at'] = next
  }

  return item
}

/**
 * 任务的状态分组，复刻 server/handler/task_query.go 的 taskSortGroup：
 * 启用/运行中/排队中一组、禁用一组、其余一组。
 * 默认排序和拖拽的「桶」判定都用它，两处必须是同一份口径。
 */
export function taskSortGroup(status: number): number {
  if (status === TASK_STATUS_DISABLED) return 1
  return status === TASK_STATUS_ENABLED || status === TASK_STATUS_RUNNING || status === 0.5 ? 0 : 2
}

/**
 * 默认排序的比较器，复刻服务端的 defaultTaskListLess。
 *
 * 抽成具名函数是因为它有两个调用方：默认排序本身，以及带 sort_rules 时「规则全平手」的回落——
 * 服务端那两处也是同一个函数（sortPreparedTaskListItems 末尾直接调 defaultTaskListLess）。
 * 两处各写一份的话，会漂成「按某一列排序时的平手顺序和不排序时不一样」。
 */
function compareTasksByDefault(left: DemoTask, right: DemoTask): number {
  if (left.is_pinned !== right.is_pinned) return left.is_pinned ? -1 : 1
  const groupDiff = taskSortGroup(left.status) - taskSortGroup(right.status)
  if (groupDiff !== 0) return groupDiff
  // 运行中的任务提到【本区】最前（issue #118），口径与服务端 applyDefaultTaskListOrdering 的
  // `CASE WHEN status = 2 THEN 0 ELSE 1 END` 以及 defaultTaskListLess 逐字对应：
  // 同样压在状态分区之下、list_order 之上，同样只认 2（运行中）不认 0.5（排队中）——
  // 排队是几秒钟就过去的瞬时态，一并提上来只会让同一次运行多跳一次。
  //
  // 🔴 这一层【不能】并进 taskSortGroup：那个函数还被 reorderTask 的 sameBucket 复用，
  //    一旦把运行中拆成独立的桶，拖拽落点会随任务自己起跑/跑完而漂移。排序要分区、拖拽不要分区。
  // 🔴 出于同样的理由，reorderTask 枚举整桶时也【不能】复用本函数（它会把运行中位置写进 list_order），
  //    那里另有一个 compareTasksForReorder，口径对齐服务端 task_sort.go。
  // 🔴 也【不能】加进 sortTasksByTimeField 的规则层：那里对应服务端的 sortPreparedTaskListItems，
  //    服务端没在那儿加这一层 —— 用户显式点了「名称 A→Z」，运行中不该越过他的规则插到前面。
  //    规则全部打平回落到本函数时它才生效，与服务端一致。
  const leftRunning = left.status === TASK_STATUS_RUNNING
  const rightRunning = right.status === TASK_STATUS_RUNNING
  if (leftRunning !== rightRunning) return leftRunning ? -1 : 1
  // list_order（拖拽顺序）插在 sort_order 之前，与服务端一致。
  // 存量数据全是 0 ⇒ 这一步全平手、顺序完全由 sort_order 决定，行为与加这个字段之前一致。
  if (left.list_order !== right.list_order) return left.list_order - right.list_order
  if (left.sort_order !== right.sort_order) return left.sort_order - right.sort_order
  if (left.created_at !== right.created_at) return right.created_at.localeCompare(left.created_at)
  return right.id - left.id
}

/** 默认排序：置顶优先 → 启用/运行 在前、禁用在后 → 运行中提到本区最前 → list_order → sort_order → 创建时间倒序 */
export function sortTasks(rows: DemoTask[]): DemoTask[] {
  return [...rows].sort(compareTasksByDefault)
}

/**
 * 「最后运行 / 下次运行」两列的排序，复刻服务端 sortPreparedTaskListItems 的内存排序分支。
 *
 * next_run_at 不是库里的列、是按 cron 现算的快照，所以只能在内存里排；这里直接复用 estimateNextRun，
 * 生效条件（非禁用 + cron 类型 + 表达式非空）与 toTaskDict 保持一致，
 * 否则会出现「列表里显示有下次运行、排序却把它当成空值沉底」。
 *
 * 🔴 分区语义：置顶与状态分组【压在排序规则之上】，规则只在同一分区内生效。
 *    少了这两层的表现是「点一下最后运行倒序，置顶任务被冲散、禁用任务混进启用任务中间」——
 *    而不带排序规则时它们规规矩矩地待在各自的区里，同一个列表两副面孔。
 *
 * 🔴 空值语义：从未运行 / 没有下次运行的任务【在各自分区内恒排最后，不随 asc/desc 翻转】。
 *    这段判定必须写在方向翻转【之前】——写在后面的话降序时一堆 `-` 会全冒到最前面。
 */
function sortTasksByTimeField(
  rows: DemoTask[],
  field: 'last_run_at' | 'next_run_at',
  direction: 'asc' | 'desc',
): DemoTask[] {
  const now = Date.now()
  // 先把值算出来存下：estimateNextRun 要遍历时间窗口，放进比较器里每次比较都算一遍会明显卡顿
  const values = new Map<number, number | null>()
  for (const task of rows) {
    if (field === 'last_run_at') {
      values.set(task.id, task.last_run_at ? new Date(task.last_run_at).getTime() : null)
      continue
    }
    if (task.status === TASK_STATUS_DISABLED || task.task_type !== 'cron' || !task.cron_expression) {
      values.set(task.id, null)
      continue
    }
    const next = estimateNextRun(task.cron_expression, now)
    values.set(task.id, next ? new Date(next).getTime() : null)
  }

  return [...rows].sort((left, right) => {
    // 两层分区先走，与服务端 sortPreparedTaskListItems 的口径一致：
    // 置顶是用户主动设置的展示优先级，状态分组决定「能跑的在上、禁用的在下」，
    // 这两件事都不该被一次列排序推翻，所以它们压在下面的时间比较之上。
    if (left.is_pinned !== right.is_pinned) return left.is_pinned ? -1 : 1
    const groupDiff = taskSortGroup(left.status) - taskSortGroup(right.status)
    if (groupDiff !== 0) return groupDiff

    const leftValue = values.get(left.id) ?? null
    const rightValue = values.get(right.id) ?? null
    // 两边都没有值 / 值完全相同 ⇒ 这条规则给不出顺序，回落默认排序（拖拽顺序 > 手工顺序 > 创建时间），
    // 与服务端「comparePreparedTaskByRule 返回 0 就走 defaultTaskListLess」逐字一致。
    if (leftValue === null && rightValue === null) return compareTasksByDefault(left, right)
    if (leftValue === null) return 1
    if (rightValue === null) return -1
    if (leftValue !== rightValue) return direction === 'desc' ? rightValue - leftValue : leftValue - rightValue
    return compareTasksByDefault(left, right)
  })
}

/**
 * 解析 sort_rules 里的第一条规则。
 *
 * Demo 只认「最后运行 / 下次运行」这两个字段（任务页的列排序与工具栏排序下拉），
 * 其余字段（视图那套 name / command / labels…）仍回落默认排序 —— 与服务端「未知 field 静默回落」的口径一致，
 * 不报错、不空列表。
 */
function parseTaskSortRule(raw: string): { field: 'last_run_at' | 'next_run_at'; direction: 'asc' | 'desc' } | null {
  const text = raw.trim()
  if (!text) return null
  try {
    const rules = JSON.parse(text)
    if (!Array.isArray(rules) || rules.length === 0) return null
    const first = rules[0] ?? {}
    const field = String(first.field ?? '')
    if (field === 'last_run_at' || field === 'next_run_at') {
      // 与服务端一致：direction 只认 desc，其它一律按 asc
      return { field, direction: String(first.direction ?? '') === 'desc' ? 'desc' : 'asc' }
    }
    return null
  } catch {
    return null
  }
}

/** 复刻服务端的 keyword / status / label 过滤（filters 那套高级筛选不在 Demo 范围内） */
export function filterTasks(params: Record<string, string>): DemoTask[] {
  let rows = db().tasks

  const keyword = (params['keyword'] ?? '').trim()
  if (keyword) {
    rows = rows.filter(
      (task) => includesIgnoreCase(task.name, keyword) || includesIgnoreCase(task.command, keyword),
    )
  }

  const statusRaw = (params['status'] ?? '').trim()
  if (statusRaw !== '') {
    const status = Number.parseFloat(statusRaw)
    if (Number.isFinite(status)) rows = rows.filter((task) => task.status === status)
  }

  const label = (params['label'] ?? '').trim()
  if (label) {
    rows = rows.filter((task) => task.labels.some((item) => includesIgnoreCase(item, label)))
  }

  const rule = parseTaskSortRule(params['sort_rules'] ?? '')
  if (rule) return sortTasksByTimeField(rows, rule.field, rule.direction)

  return sortTasks(rows)
}

/**
 * 拖拽重编号时桶内兄弟的排序，逐字对应服务端 task_sort.go 取兄弟列表的
 * `Order("list_order ASC, sort_order ASC, created_at DESC, id DESC")`。
 *
 * 🔴 这里刻意【不复用】sortTasks / compareTasksByDefault —— 它们含「运行中优先」（issue #118），
 *    而服务端 task_sort.go 这条查询**没有**这一层。复用它会把「此刻正在运行」这个临时状态
 *    永久写进 list_order：桶里正在跑的那条被重编号成 10，等它跑完回到已启用，
 *    它仍然钉在拖拽序第一位再也下不来 —— 真实面板不会这样
 *    （服务端的 TestTaskListRestoresRunningTaskPositionAfterItFinishes 钉的正是「跑完回原位」）。
 * 🔴 置顶与状态分组也不用比：调用方已按 sameBucket 过滤过，桶内这两项恒等
 *    （服务端同理，靠 `is_pinned = ?` 与 taskSortGroup 过滤，ORDER BY 里也没有它们）。
 */
function compareTasksForReorder(left: DemoTask, right: DemoTask): number {
  if (left.list_order !== right.list_order) return left.list_order - right.list_order
  if (left.sort_order !== right.sort_order) return left.sort_order - right.sort_order
  if (left.created_at !== right.created_at) return right.created_at.localeCompare(left.created_at)
  return right.id - left.id
}

/**
 * 拖拽排序：把 sourceId 插到 targetId 之前（position='after' 时插到之后）；targetId 为空表示移到本区末尾。
 * 复刻 server/handler/task_sort.go。
 *
 * ⚠️ 这里必须【真的改顺序】。只回一句 `{message:'排序更新成功'}` 而不动数据，
 *    页面重新加载列表后会把行弹回原位，看起来像拖拽功能坏了。
 * ⚠️ 改的是 list_order，不能碰 sort_order（那是开机任务的执行顺序）。
 */
export function reorderTask(
  sourceId: number,
  targetId?: number,
  position?: string,
): { ok: true } | { ok: false; error: string } {
  const current = db()
  const source = current.tasks.find((task) => task.id === sourceId)
  if (!source) return { ok: false, error: '任务不存在' }
  if (targetId !== undefined && targetId === sourceId) return { ok: true }

  const sameBucket = (task: DemoTask) =>
    task.is_pinned === source.is_pinned && taskSortGroup(task.status) === taskSortGroup(source.status)

  if (targetId !== undefined) {
    const target = current.tasks.find((task) => task.id === targetId)
    if (!target) return { ok: false, error: '目标任务不存在' }
    if (!sameBucket(target)) {
      return { ok: false, error: '置顶任务与普通任务、启用与禁用任务请分别排序，跨区移动请用置顶 / 启用按钮' }
    }
  }

  // 兄弟列表取【整桶】而不是当前页，所以跨页拖拽也有效。
  // 排序走 compareTasksForReorder（服务端口径，不含运行中优先），不是列表用的 sortTasks —— 原因见其函数注释。
  // filter 已经产出新数组，这里就地 sort 不会动到 state.tasks 本身。
  const bucket = current.tasks.filter(sameBucket).sort(compareTasksForReorder)
  const rest = bucket.filter((task) => task.id !== source.id)

  let insertIndex = rest.length
  if (targetId !== undefined) {
    const found = rest.findIndex((task) => task.id === targetId)
    if (found === -1) return { ok: false, error: '目标任务不存在' }
    // position 只认 after，其余一律按 before（与服务端同口径）
    insertIndex = position === 'after' ? found + 1 : found
  }

  const ordered = [...rest.slice(0, insertIndex), source, ...rest.slice(insertIndex)]
  // 整桶重编号，步长 10 与服务端 (idx+1)*10 一致；其它桶保持原值
  ordered.forEach((task, index) => {
    task.list_order = (index + 1) * 10
  })

  return { ok: true }
}

// ---------------------------------------------------------------------------
// 执行日志
// ---------------------------------------------------------------------------

/**
 * 生成日志正文。
 *
 * 正文不入库（几千条日志各存一份会把内存占用撑大一个数量级），按 kind 现算。
 * 每种结局的正文都要能自证：失败的正文里必须真的有报错行，
 * 否则演示时点开一条「失败」看到的是一片成功日志。
 */
export function buildLogContent(log: DemoTaskLog): string {
  // 假日志流跑完之后会把「刚刚滚过的那份正文」留在 log.content 上。
  // LogViewer 收到 done 之后会立刻回查 latest-log 并整体替换渲染内容
  // （LogViewer.vue:239-241），不认这份正文的话，访客会看到刚看完的日志
  // 在最后一刻被换成另一套措辞的版本。
  if (log.content) return log.content

  const task = findTask(log.task_id)
  const command = task?.command ?? '(任务已删除)'
  const stamp = (offsetSeconds: number) => {
    const at = new Date(new Date(log.started_at).getTime() + offsetSeconds * 1000)
    const pad = (value: number) => String(value).padStart(2, '0')
    return `${at.getFullYear()}-${pad(at.getMonth() + 1)}-${pad(at.getDate())} `
      + `${pad(at.getHours())}:${pad(at.getMinutes())}:${pad(at.getSeconds())}`
  }

  // 「已注入 N 项」跟着 envs 现算，不写死数字 —— 本文件开头第 3 条规矩就是「汇总数字一律从事实算」。
  // 写死过一次就会漂：v3.2.5 给 fixture 加了 3 条 ACCOUNT_TOKEN，原先写死的「19 项」当场和
  // 环境变量页的 22 条对不上。数的是【启用】的那些，与面板真实只注入启用变量的口径一致。
  const injectedEnvCount = db().envs.filter((env) => env.enabled).length

  const duration = log.duration ?? 0
  const lines: string[] = [
    `[${stamp(0)}] ## 开始执行 ${log.task_name}`,
    `[${stamp(0)}] ## 命令：${command}`,
    `[${stamp(0)}] ## 工作目录：/opt/daidai/scripts`,
    '',
  ]

  switch (log.kind) {
    case 'ok':
      lines.push(
        `[${stamp(1)}] 环境变量已注入（${injectedEnvCount} 项）`,
        `[${stamp(1)}] 开始处理...`,
        // 裸 \r 是终端进度条的覆盖语义，前端的日志渲染会按覆盖处理，不要换成 \n
        `处理进度: 25%\r处理进度: 60%\r处理进度: 88%\r处理进度: 100%`,
        `[${stamp(Math.max(1, duration - 1))}] 处理完成，无异常`,
        '',
        `[${stamp(duration)}] ## 执行结束，退出码 0，耗时 ${duration}s`,
      )
      break
    case 'fail': {
      lines.push(
        `[${stamp(1)}] 环境变量已注入（${injectedEnvCount} 项）`,
        `[${stamp(1)}] 开始处理...`,
      )
      // 重试相关的两行只在任务真的配了重试时才出现：
      // 一个 max_retries=0 的任务，正文里写「重试次数已用尽」当场就穿帮。
      // 同理不写死「2s 后重试」——很多任务整次执行也就一两秒。
      if ((task?.max_retries ?? 0) > 0) {
        lines.push(`[${stamp(Math.max(1, duration - 2))}] WARN  第 1 次尝试失败，准备重试`)
      }
      lines.push(`[${stamp(Math.max(1, duration - 1))}] ERROR 远端返回 502 Bad Gateway`)
      if ((task?.max_retries ?? 0) > 0) {
        lines.push(`[${stamp(Math.max(1, duration - 1))}] ERROR 重试次数已用尽，放弃本次执行`)
      }
      lines.push(
        '',
        `[${stamp(duration)}] ## 执行结束，退出码 1，耗时 ${duration}s`,
      )
      break
    }
    case 'timeout':
      lines.push(
        `[${stamp(1)}] 环境变量已注入（${injectedEnvCount} 项）`,
        `[${stamp(1)}] 开始处理...`,
        `处理进度: 12%\r处理进度: 34%\r处理进度: 41%`,
        `[${stamp(duration)}] ERROR 任务超过配置的超时时间 ${task?.timeout ?? duration}s，已被面板终止`,
        '',
        `[${stamp(duration)}] ## 执行结束，退出码 -1（超时），耗时 ${duration}s`,
      )
      break
    case 'abort':
      lines.push(
        `[${stamp(1)}] 环境变量已注入（${injectedEnvCount} 项）`,
        `[${stamp(1)}] 开始处理...`,
        `[${stamp(duration)}] 收到停止信号，正在清理临时文件`,
        '',
        `[${stamp(duration)}] ## 已被手动终止，耗时 ${duration}s`,
      )
      break
    case 'running':
      lines.push(
        `[${stamp(1)}] 环境变量已注入（${injectedEnvCount} 项）`,
        `[${stamp(1)}] 开始处理...`,
        `[${stamp(2)}] 正在执行中，实时日志请点「查看实时日志」`,
      )
      break
  }

  return lines.join('\n')
}

/** 执行日志下发体，字段照抄 server/model/task_log.go 的 ToDict() */
export function toLogDict(log: DemoTaskLog, withContent = false): Record<string, unknown> {
  const task = findTask(log.task_id)
  // 日志页只画展示标签，不需要订阅名单
  const labels = task ? buildDisplayLabels(task.labels).display : []
  const taskType = task?.task_type ?? 'cron'

  return {
    id: log.id,
    task_id: log.task_id,
    task_name: task?.name ?? log.task_name,
    task_type: taskType,
    labels,
    task: { task_type: taskType, labels },
    status: log.status,
    duration: log.duration,
    content: withContent ? buildLogContent(log) : '',
    log_path: null,
    started_at: log.started_at,
    ended_at: log.ended_at,
    // 后端这两条由同一次写入产生，值与 started_at / ended_at 一致
    created_at: log.started_at,
    updated_at: log.ended_at ?? log.started_at,
  }
}

export function filterLogs(params: Record<string, string>): DemoTaskLog[] {
  let rows = db().logs

  const taskIdRaw = (params['task_id'] ?? '').trim()
  if (taskIdRaw) {
    const taskId = Number.parseInt(taskIdRaw, 10)
    if (Number.isFinite(taskId)) rows = rows.filter((log) => log.task_id === taskId)
  }

  const statusRaw = (params['status'] ?? '').trim()
  if (statusRaw !== '') {
    const status = Number.parseInt(statusRaw, 10)
    if (Number.isFinite(status)) rows = rows.filter((log) => log.status === status)
  }

  const keyword = (params['keyword'] ?? '').trim()
  if (keyword) {
    rows = rows.filter((log) => {
      const task = findTask(log.task_id)
      return includesIgnoreCase(task?.name ?? log.task_name, keyword)
    })
  }

  // 执行时间范围，对齐 server/handler/log.go 的 start_time / end_time（RFC3339，闭区间）。
  // 服务端筛的是 created_at；Demo 这边 created_at 就等于 started_at（见 toLogDict 的注释），
  // 所以直接拿 started_at 比就行，语义一致。
  //
  // 演示站必须真的能筛：一个点了没反应的日期选择器比没有这个功能更糟——
  // 访客会以为面板本身有问题。
  const startTime = Date.parse((params['start_time'] ?? '').trim())
  if (Number.isFinite(startTime)) {
    rows = rows.filter((log) => new Date(log.started_at).getTime() >= startTime)
  }
  const endTime = Date.parse((params['end_time'] ?? '').trim())
  if (Number.isFinite(endTime)) {
    rows = rows.filter((log) => new Date(log.started_at).getTime() <= endTime)
  }

  // 服务端是 started_at DESC；state.logs 本身就按这个顺序存，这里只在过滤后保持它
  return rows
}

/**
 * 记一条「刚刚跑完」的执行日志，并同步任务的 last_run_*。
 *
 * 手动点「运行」走这里。写完之后仪表盘的今日执行、成功数、趋势图当天那一格
 * 会一起往上跳一格——因为它们本来就是从这张表算出来的。
 */
export function appendTaskRunLog(task: DemoTask, kind: DemoTaskLog['kind'], durationSeconds: number): DemoTaskLog {
  const current = db()
  const startedAt = Date.now() - Math.round(durationSeconds * 1000)
  const status = logStatusOfKind(kind)

  const log: DemoTaskLog = {
    id: nextId('log'),
    task_id: task.id,
    task_name: task.name,
    status,
    duration: kind === 'running' ? null : Math.round(durationSeconds * 10) / 10,
    started_at: new Date(startedAt).toISOString(),
    ended_at: kind === 'running' ? null : new Date(startedAt + durationSeconds * 1000).toISOString(),
    kind,
  }

  current.logs.unshift(log)
  task.last_run_at = log.started_at
  task.last_run_status = status === LOG_STATUS_RUNNING ? null : status
  task.last_running_time = log.duration
  task.updated_at = nowIso()
  return log
}

// ---------------------------------------------------------------------------
// 仪表盘 / 系统统计
// ---------------------------------------------------------------------------

interface DailyBucket {
  success: number
  failed: number
  aborted: number
  total: number
}

/** 把全部日志按本地自然日分桶，仪表盘的每日趋势与今日/昨日对比都读它 */
function bucketLogsByDay(): Map<string, DailyBucket> {
  const buckets = new Map<string, DailyBucket>()

  for (const log of db().logs) {
    const key = monthDayKey(new Date(log.started_at).getTime())
    let bucket = buckets.get(key)
    if (!bucket) {
      bucket = { success: 0, failed: 0, aborted: 0, total: 0 }
      buckets.set(key, bucket)
    }
    bucket.total += 1
    if (log.status === LOG_STATUS_SUCCESS) bucket.success += 1
    else if (log.status === LOG_STATUS_FAILED) bucket.failed += 1
    else if (log.status === LOG_STATUS_ABORTED) bucket.aborted += 1
  }

  return buckets
}

const EMPTY_BUCKET: DailyBucket = { success: 0, failed: 0, aborted: 0, total: 0 }

/**
 * GET /system/dashboard 的响应体（不含外层 `data`）。
 * 字段清单对齐 server/handler/system.go:185-205。
 *
 * ⚠️ 这里的每一个数字都是从 tasks / logs 现算的。
 *    历史上这一块出过「成功率恒 100%」的问题，根因就是各处各写一份常量。
 *    改这里时请继续保持「只算不写死」，尤其是 failed_logs / yesterday_* 这几个
 *    ——少下发一个，前端的对比卡片就会拿 0 去比，直接显示 +100%。
 */
export function buildDashboard(params: Record<string, string>): Record<string, unknown> {
  const current = db()
  const requested = parseIntOr(params['range'], 7)
  const rangeDays = requested > 0 && requested <= 90 ? requested : 7

  const buckets = bucketLogsByDay()
  const todayStart = startOfLocalDay(Date.now())

  const dailyStats: Array<{ date: string; success: number; failed: number; aborted: number }> = []
  for (let offset = rangeDays - 1; offset >= 0; offset -= 1) {
    const key = monthDayKey(todayStart - offset * DAY_MS)
    const bucket = buckets.get(key) ?? EMPTY_BUCKET
    dailyStats.push({ date: key, success: bucket.success, failed: bucket.failed, aborted: bucket.aborted })
  }

  const today = buckets.get(monthDayKey(todayStart)) ?? EMPTY_BUCKET
  const yesterday = buckets.get(monthDayKey(todayStart - DAY_MS)) ?? EMPTY_BUCKET

  return {
    task_count: current.tasks.length,
    enabled_tasks: current.tasks.filter((task) => task.status === TASK_STATUS_ENABLED).length,
    running_tasks: current.tasks.filter((task) => task.status === TASK_STATUS_RUNNING).length,
    // 「任务总数」卡片的增量 = task_count - prev_task_count，按「今天之前建的任务数」算
    prev_task_count: current.tasks.filter((task) => new Date(task.created_at).getTime() < todayStart).length,
    // 后端 today_logs 数的是当天全部日志行，含 status=running 那几条，
    // 所以它会比同页三段占比条的当日合计（只算已结束的三类）大一点，这是对的
    today_logs: today.total,
    success_logs: today.success,
    failed_logs: today.failed,
    aborted_logs: today.aborted,
    yesterday_logs: yesterday.total,
    yesterday_success: yesterday.success,
    yesterday_failed: yesterday.failed,
    yesterday_aborted: yesterday.aborted,
    env_count: current.envs.length,
    sub_count: current.subscriptions.length,
    daily_stats: dailyStats,
    recent_logs: current.logs.slice(0, 10).map((log) => toLogDict(log)),
    range_days: rangeDays,
  }
}

/**
 * GET /system/stats 的响应体（不含外层 `data`）。
 * 系统设置页「系统概况」那六个数字读的就是它——不铺就全是 0。
 * 字段与 server/handler/system.go:230-249 一致。
 */
export function buildSystemStats(): Record<string, unknown> {
  const current = db()
  const success = current.logs.filter((log) => log.status === LOG_STATUS_SUCCESS).length
  const failed = current.logs.filter((log) => log.status === LOG_STATUS_FAILED).length
  const aborted = current.logs.filter((log) => log.status === LOG_STATUS_ABORTED).length
  // 与后端一致：成功率只看自然结束的成功/失败，主动终止不拉低成功率
  const finished = success + failed

  return {
    tasks: {
      total: current.tasks.length,
      enabled: current.tasks.filter((task) => task.status === TASK_STATUS_ENABLED).length,
      disabled: current.tasks.filter((task) => task.status === TASK_STATUS_DISABLED).length,
      running: current.tasks.filter((task) => task.status === TASK_STATUS_RUNNING).length,
    },
    logs: {
      total: current.logs.length,
      success,
      failed,
      aborted,
      success_rate: finished > 0 ? (success / finished) * 100 : 0,
    },
    scripts: {
      total: current.scriptFiles.length,
    },
  }
}

/**
 * GET /system/badges 的响应体（不含外层 `data`）。
 * 字段清单对齐 server/handler/system_badges.go。
 *
 * 同样遵守本文件「只算不写死」的规矩：五个数字全部从 tasks / logs / subscriptions / deps
 * 现算。演示站的角标必须跟着 Demo 数据走 —— 访客点「运行任务」之后，侧栏的运行中角标
 * 要真的 +1，写死常量就演不出这个联动。
 *
 * 服务端会按角色裁剪（viewer 看不到订阅/依赖），Demo 的账号恒为管理员，所以这里全量返回。
 */
export function buildSystemBadges(): Record<string, number> {
  const current = db()
  const todayStart = startOfLocalDay(Date.now())

  return {
    tasks_running: current.tasks.filter((task) => task.status === TASK_STATUS_RUNNING).length,
    logs_failed_today: current.logs.filter(
      (log) => log.status === LOG_STATUS_FAILED && new Date(log.started_at).getTime() >= todayStart
    ).length,
    // 订阅 status：0 成功 / 1 失败，与 server/service/subscription.go 的写入一致
    subs_failed: current.subscriptions.filter((sub) => sub.status === 1).length,
    deps_failed: current.deps.filter((dep) => dep.status === 'failed').length,
    deps_installing: current.deps.filter((dep) =>
      dep.status === 'queued' || dep.status === 'installing' || dep.status === 'removing'
    ).length,
  }
}

// ---------------------------------------------------------------------------
// 环境变量
// ---------------------------------------------------------------------------

const ENV_NAME_PATTERN = /^[A-Za-z_][A-Za-z0-9_]*$/
const ENV_POSITION_STEP = 1000

export function isValidEnvName(name: string) {
  return ENV_NAME_PATTERN.test(name)
}

/** 逗号/分号/换行分隔的分组串 → 去重后的数组，复刻 server/model/env_var.go 的 SplitEnvGroups */
export function splitEnvGroups(value: string): string[] {
  const seen = new Set<string>()
  const groups: string[] = []
  for (const raw of value.split(/[,，;；\n\r\t]/)) {
    const group = raw.trim()
    if (!group || seen.has(group)) continue
    seen.add(group)
    groups.push(group)
  }
  return groups
}

export function joinEnvGroups(groups: string[]): string {
  return splitEnvGroups(groups.join(',')).join(',')
}

export function toEnvDict(env: DemoEnvVar): Record<string, unknown> {
  return { ...env, groups: splitEnvGroups(env.group) }
}

/** 列表顺序：sort_order DESC（置顶区在前）→ position ASC → created_at ASC → id ASC */
export function sortEnvs(rows: DemoEnvVar[]): DemoEnvVar[] {
  return [...rows].sort((left, right) => {
    if (left.sort_order !== right.sort_order) return right.sort_order - left.sort_order
    if (left.position !== right.position) return left.position - right.position
    if (left.created_at !== right.created_at) return left.created_at.localeCompare(right.created_at)
    return left.id - right.id
  })
}

export function filterEnvs(params: Record<string, string>): DemoEnvVar[] {
  let rows = db().envs

  const keyword = (params['keyword'] ?? '').trim()
  if (keyword) {
    rows = rows.filter(
      (env) => includesIgnoreCase(env.name, keyword)
        || includesIgnoreCase(env.remarks, keyword)
        || includesIgnoreCase(env.value, keyword)
        || includesIgnoreCase(env.group, keyword),
    )
  }

  const groupFilters = splitEnvGroups([params['groups'] ?? '', params['group'] ?? ''].join(','))
  if (groupFilters.length > 0) {
    rows = rows.filter((env) => {
      const groups = splitEnvGroups(env.group)
      return groupFilters.some((group) => groups.includes(group))
    })
  }

  // 变量名筛选是【精确】匹配（不是 keyword 那种模糊 LIKE），复刻服务端的 applyEnvNameFilters：
  // 多个变量名之间是 OR，与 keyword / groups / enabled 之间是 AND；传了库里不存在的名字就筛出空列表，
  // 绝不回落成「不筛」。names=JD_COOKIE 不会带出 JD_COOKIE_EXTRA，这正是它和搜索框的区别。
  //
  // 解析同样借 splitEnvGroups 的「切分 + 去空白 + 去重」，与服务端 parseEnvNameFilter 是同一套路：
  // 变量名受 ENV_NAME_PATTERN（^[A-Za-z_][A-Za-z0-9_]*$）约束、不可能含逗号，逗号分隔在这里是安全的。
  // 🔴 这个前提只对变量名成立，别把同一套解析扩散到 value / remarks 那种自由文本字段上。
  //
  // 服务端还认 `names=A&names=B` 这种重复参数；Demo 这边 ctx.params 是 Record<string, string>，
  // 重复键只留最后一个 —— 但前端（views/envs/index.vue）发的是逗号拼串，axios 传数组也会被
  // String() 成逗号串，两条路径都落在逗号分支上，实际行为与服务端一致。
  const nameFilters = splitEnvGroups(params['names'] ?? '')
  if (nameFilters.length > 0) {
    rows = rows.filter((env) => nameFilters.includes(env.name))
  }

  const enabledRaw = (params['enabled'] ?? '').trim()
  if (enabledRaw !== '') {
    const enabled = enabledRaw.toLowerCase() === 'true' || enabledRaw === '1'
    rows = rows.filter((env) => env.enabled === enabled)
  }

  return sortEnvs(rows)
}

export function nextEnvPosition(sortOrder: number): number {
  const siblings = db().envs.filter((env) => env.sort_order === sortOrder)
  const max = siblings.reduce((acc, env) => (env.position > acc ? env.position : acc), 0)
  return max + ENV_POSITION_STEP
}

/**
 * 拖拽排序：把 sourceId 插到 targetId 之前；targetId 为空表示移到末尾。
 * 复刻 server/handler/env.go 的 reorderEnvWithinSortBucket。
 *
 * ⚠️ 这里必须【真的改顺序】。只回一句 `{message:'排序更新成功'}` 而不动数据，
 *    页面重新加载列表后会把行弹回原位，看起来像拖拽功能坏了。
 */
export function reorderEnv(sourceId: number, targetId?: number): { ok: true } | { ok: false; error: string } {
  const current = db()
  const source = current.envs.find((env) => env.id === sourceId)
  if (!source) return { ok: false, error: '源环境变量不存在' }
  if (targetId !== undefined && targetId === sourceId) return { ok: true }

  if (targetId !== undefined) {
    const target = current.envs.find((env) => env.id === targetId)
    if (!target) return { ok: false, error: '目标环境变量不存在' }
    if (target.sort_order !== source.sort_order) {
      return { ok: false, error: '置顶项和普通项请分别排序，需要跨区移动时请使用置顶按钮' }
    }
  }

  const bucket = sortEnvs(current.envs.filter((env) => env.sort_order === source.sort_order))
  const rest = bucket.filter((env) => env.id !== source.id)

  let insertIndex = rest.length
  if (targetId !== undefined) {
    const found = rest.findIndex((env) => env.id === targetId)
    if (found === -1) return { ok: false, error: '目标环境变量不存在' }
    insertIndex = found
  }

  const ordered = [...rest.slice(0, insertIndex), source, ...rest.slice(insertIndex)]
  ordered.forEach((env, index) => {
    env.sort_order = source.sort_order
    env.position = (index + 1) * ENV_POSITION_STEP
  })

  return { ok: true }
}

// ---------------------------------------------------------------------------
// 脚本
// ---------------------------------------------------------------------------

export interface ScriptTreeNode {
  key: string
  title: string
  isLeaf: boolean
  type: string
  children?: ScriptTreeNode[]
  extension?: string
  size?: number
  mtime?: number
}

function utf8Size(text: string): number {
  return new TextEncoder().encode(text).length
}

function extensionOf(path: string): string {
  const name = path.slice(path.lastIndexOf('/') + 1)
  const dot = name.lastIndexOf('.')
  return dot > 0 ? name.slice(dot).toLowerCase() : ''
}

/** 所有目录（显式建的 + 从文件路径推出来的父目录） */
function allScriptDirs(): Set<string> {
  const current = db()
  const dirs = new Set<string>(current.scriptDirs)
  for (const file of current.scriptFiles) {
    const segments = file.path.split('/')
    segments.pop()
    let prefix = ''
    for (const segment of segments) {
      prefix = prefix ? `${prefix}/${segment}` : segment
      dirs.add(prefix)
    }
  }
  return dirs
}

/** 目录在前、文件在后，各自按名字排序，形状照抄 server/handler/script_file_ops.go 的 buildTree */
export function buildScriptTree(prefix = ''): ScriptTreeNode[] {
  const current = db()
  const dirs = allScriptDirs()
  const depth = prefix ? prefix.split('/').length : 0

  const childDirs: ScriptTreeNode[] = []
  for (const dir of dirs) {
    if (prefix ? !dir.startsWith(`${prefix}/`) : dir.includes('/')) continue
    if (dir.split('/').length !== depth + 1) continue
    childDirs.push({
      key: dir,
      title: dir.slice(dir.lastIndexOf('/') + 1),
      isLeaf: false,
      type: 'directory',
      children: buildScriptTree(dir),
    })
  }

  const childFiles: ScriptTreeNode[] = []
  for (const file of current.scriptFiles) {
    if (prefix ? !file.path.startsWith(`${prefix}/`) : file.path.includes('/')) continue
    if (file.path.split('/').length !== depth + 1) continue
    childFiles.push({
      key: file.path,
      title: file.path.slice(file.path.lastIndexOf('/') + 1),
      isLeaf: true,
      type: 'file',
      extension: extensionOf(file.path),
      size: utf8Size(file.content),
      mtime: file.mtime,
    })
  }

  const byTitle = (left: ScriptTreeNode, right: ScriptTreeNode) =>
    left.title.toLowerCase().localeCompare(right.title.toLowerCase())

  return [...childDirs.sort(byTitle), ...childFiles.sort(byTitle)]
}

/** GET /scripts 的扁平文件列表，字段照抄 server/handler/script_file_ops.go 的 List */
export function buildScriptList(keyword: string) {
  const rows = db().scriptFiles
    .filter((file) => !keyword || includesIgnoreCase(file.path, keyword))
    .map((file) => ({
      path: file.path,
      name: file.path.slice(file.path.lastIndexOf('/') + 1),
      size: utf8Size(file.content),
      mtime: file.mtime,
    }))
  return rows.sort((left, right) => left.path.localeCompare(right.path))
}

export function findScriptFile(path: string) {
  const normalized = path.replace(/^\/+/, '')
  return db().scriptFiles.find((file) => file.path === normalized)
}

/** 保存脚本内容；文件不存在则新建（「新建脚本」走的就是这条路径） */
export function saveScriptContent(path: string, content: string) {
  const normalized = path.replace(/^\/+/, '')
  const existing = findScriptFile(normalized)
  if (existing) {
    existing.content = content
    existing.mtime = Math.floor(Date.now() / 1000)
    return existing
  }

  const created = { path: normalized, content, mtime: Math.floor(Date.now() / 1000) }
  db().scriptFiles.push(created)
  return created
}

// ---------------------------------------------------------------------------
// 删除任务时一并删除脚本（issue #124）
// ---------------------------------------------------------------------------
//
// 【判定口径与文案抄 server/service/task_script_cleanup.go】
// 契约以已跟踪的代码为准：服务端 task_script_cleanup.go 的常量（reason / script_status 取值与各句文案）、
// 结构体的 json tag 与 decide 的判定顺序，以及对外文档 web/src/views/api-docs/apiData.ts。
// 返回的数据形状与服务端一致：预览带 checked: true 哨兵，所有数组恒为 []、不会是 null，
// 路径一律是相对脚本目录的正斜杠路径。服务端改文案、加 reason 时这里要跟着改，两边没有任何自动校验。
//
// 解析抄服务端 ParseCommandExecutionPlan（见 resolveDemoTaskScriptTarget）：切词复用 @/utils/taskCommandScript
// 导出的 splitCommandTokens，逐个候选查文件在不在时用 findScriptFile 代替服务端的 os.Stat。
// ⚠️ 不要换成 views/tasks/taskCommand.ts：它漏了 .mjs，种子任务 5、14（node xxx.mjs）会被判成解析不出脚本。
//
// 类型就地声明、不从 @/api/task 引入：前端组在并行给那边加同名类型，两边互相依赖的话任何一边单独都编不过。
// 字段名各自对齐契约即可。
//
// 演示站【不模拟】的判定（与真实后端的差异，如实记在这里，别当成 bug 去「修」成半套）：
//   - symlink / not_regular_file / hidden_path：演示站的脚本树里没有软链接、没有「名字像脚本的目录」，
//     也没有 .git、node_modules 这类受保护目录；
//   - managed_helper：脚本目录根下的 notify.py / sendNotify.js，以及全局钩子 task_before.sh / task_after.sh / extra.sh，
//     都不做特判（种子里的 lib/notify.py 不在根目录，真实后端也不会因为这一条拦它；种子脚本树的根下也没有这三个钩子）；
//   - referenced_in_hook：不扫其他任务的前置/后置命令，也不扫订阅的拉取前/后命令；
//   - check_failed / changed / remove_failed：没有真实文件系统，不会出错，也没有「确认之后文件被替换」的时间窗；
//     同理也不会「读任务快照出错」，服务端那种 found=false 却不是 task_not_found 的项，这里不会出现；
//   - outside_scripts_dir：演示站没有脚本目录的绝对路径，候选含 .. 或是绝对路径时一律算解析失败，这类任务落在 unresolved。
//     服务端这里会分情况：
//       - 含 .. 一律报 outside_scripts_dir，与文件在不在无关（危险字符检查在查文件之前）；
//       - 绝对路径会真的去查，而且先查存不存在、再判出没出界：不存在时报 not_found，落在脚本目录内的
//         带相对路径（script_path），在脚本目录外的不带路径；
//       - 存在但在脚本目录外，报 outside_scripts_dir；
//       - 存在且直接落在脚本目录内，判 resolved、可以一并删除；
//       - 经 /ql 兼容层这类别名落回脚本目录内的，也能解析成功，但按 symlink 保留、不会被删。
//     演示站对以上一律判 unresolved，所以「绝对路径指向脚本目录内的真实文件」这一类，服务端会提供删除，演示站不会。
//     差异的方向是演示站提供得更少，不会把服务端要保留的文件报成可删，所以不去模拟；
//   - 路径写法：候选按 normalizeScriptPath 归一（\ 当成 /、去掉开头的 ./ 与 /），不做 Clean，也严格区分大小写；
//     服务端会 Clean（a//b.py、a/./b.py 都能找到 a/b.py），在 Linux 上把 \ 当普通字符，Windows 版还不分大小写。
//     这几类写法两边可能一边找得到、一边找不到；
//   - text_match=true 的共用：服务端会对依赖命令、写坏的命令、python -m 模块映射到的文件（m.py、m/__main__.py、
//     沿途各级包的 __init__.py）做文本匹配；这里「其他任务」只认按同一套解析（resolveDemoTaskScriptTarget）
//     解析到同一路径的任务，所以 shared_by[].text_match 恒为 false；
//   - 运行状态只看任务的 status（运行中 / 排队中），没有执行器的进程表；
//   - git 订阅的根目录等于脚本目录（save_dir 配成 . 之类）时，服务端要求那里真是 git 仓库才保护，
//     演示站没有 .git 的概念，这种订阅一律不当成订阅目录。
// task_not_deleted 按服务端口径实现了，但演示站删任务不会失败，实际不会出现。

/** tasks[].script_status 的取值（抄 task_script_cleanup.go 的 taskScriptStatus* 常量） */
export type DemoTaskScriptStatus =
  | 'resolved'
  | 'no_script'
  | 'not_found'
  | 'outside_scripts_dir'
  | 'unresolved'
  | 'task_not_found'

/** 本次范围外、仍在引用这个脚本的任务（抄 TaskScriptRef） */
export interface DemoTaskScriptRef {
  id: number
  name: string
  text_match: boolean
}

/** 每个请求任务一项，按请求顺序（抄 TaskScriptTaskInfo） */
export interface DemoTaskScriptTaskInfo {
  id: number
  name: string
  found: boolean
  running: boolean
  script_status: DemoTaskScriptStatus
  script_path: string
  note: string
}

/** 按真实文件合并后的一项（抄 TaskScriptItem）；deletable 当且仅当 reason 为空串 */
export interface DemoTaskScriptItem {
  path: string
  task_ids: number[]
  deletable: boolean
  reason: string
  detail: string
  shared_by: DemoTaskScriptRef[]
  warnings: string[]
}

/** POST /tasks/delete-preview 响应里的 data */
export interface DemoTaskDeletePreview {
  /** 哨兵：未铺设端点的兜底体里没有它，前端靠它区分「真的查过」和「没查成」 */
  checked: true
  tasks: DemoTaskScriptTaskInfo[]
  scripts: DemoTaskScriptItem[]
}

/** 三个删除入口带开关时，在原响应上追加的 scripts 字段 */
export interface DemoTaskScriptDeleteResult {
  tasks: DemoTaskScriptTaskInfo[]
  deleted: DemoTaskScriptItem[]
  skipped: DemoTaskScriptItem[]
}

/** 删任务之前拍的快照，只取 id、name、command、status（抄 CollectTaskScriptTargets） */
export interface DemoTaskSnapshot {
  id: number
  name: string
  command: string
  status: number
}

/** 一条任务命令解析出来的脚本目标 */
export interface DemoTaskScriptTarget {
  status: DemoTaskScriptStatus
  /** resolved / not_found 时是归一化后的相对路径，其余为空串 */
  path: string
  note: string
}

/**
 * planDemoTaskScriptDeletion 的产物。
 * 预览端点只把 preview 交出去；删除端点在删任务之前拿到它，删完再交给 executeDemoTaskScriptDeletion。
 */
export interface DemoTaskScriptPlan {
  /** 去重后的请求 id，保持首次出现的顺序 */
  ids: number[]
  snapshots: DemoTaskSnapshot[]
  targets: Map<number, DemoTaskScriptTarget>
  preview: DemoTaskDeletePreview
}

/**
 * 抄 ParseCommandExecutionPlan 的解释器 case 分支（逐字，勿增删），第二行是其中的 Python 系（IsPythonInterpreter）。
 * @/utils/taskCommandScript 里有同一份，但那边只导出了切词函数 splitCommandTokens：脚本管理页也依赖那个文件，
 * 导出面保持最小，所以这里和下面几个小工具就地抄一份。服务端改名单时几处要一起改。
 */
const DEMO_COMMAND_INTERPRETERS = ['python', 'python3', 'python3.10', 'python3.11', 'python3.12', 'node', 'ts-node', 'bash', 'go']
const DEMO_PYTHON_INTERPRETERS = ['python', 'python3', 'python3.10', 'python3.11', 'python3.12']

/** 抄 isSupportedScriptExtension：候选路径以这 6 种扩展名结尾才会去查文件 */
const DEMO_SCRIPT_EXTENSIONS = ['.py', '.js', '.mjs', '.ts', '.sh', '.go']

/** 抄 resolveCommandScriptPath 的危险字符黑名单（逐字；.. 是两个字符的子串，不是「任意两个点」） */
const DEMO_DANGEROUS_PATH_FRAGMENTS = ['..', '~', '$', '`', ';', '|', '&', '>', '<']

const NOTE_UNRESOLVED = '没能从命令里识别出脚本文件（命令格式可能有误），只删除任务。'
const NOTE_TASK_NOT_FOUND = '任务不存在（可能已被删除）。'

/** 运行中与排队中都算「还在跑」（server/model/task.go）：排队请求带的是任务副本，删掉任务行之后照样会执行 */
function isTaskActiveStatus(status: number): boolean {
  return status === TASK_STATUS_RUNNING || status === TASK_STATUS_QUEUED
}

/**
 * 抄 Go 的 unicode.IsSpace（strings.TrimSpace 按它判）。JS 的 \s 与它差两个字符：多一个 U+FEFF、
 * 少一个 U+0085（NEL），这里补齐。用码点判断，避免源码里出现看不见的字符。
 */
function isGoSpace(ch: string): boolean {
  const code = ch.codePointAt(0)
  if (code === 0x85) return true
  return code !== 0xfeff && /\s/u.test(ch)
}

/**
 * 抄 Go 的 strings.TrimSpace（Array.from 按码点切分，与 Go 的 rune 迭代一致）。
 * token 来自 splitCommandTokens，引号里的首尾空白会原样留在 token 里（task " dailycheckin"），
 * 而服务端下面几处校验都是先 TrimSpace 再判，这里要一样。
 */
function trimGoSpace(value: string): string {
  const chars = Array.from(value)
  let start = 0
  let end = chars.length
  while (start < end && isGoSpace(chars[start]!)) start++
  while (end > start && isGoSpace(chars[end - 1]!)) end--
  return chars.slice(start, end).join('')
}

/**
 * 抄 script_runner.go 的 isManagedExecutableName：首词长这样才会被当成托管依赖命令，否则整条命令报错。
 * 先 TrimSpace 再判；note 里服务端用的仍是原 token（带着引号里的空白），调用方照原样传即可。
 */
function isManagedExecutableName(name: string): boolean {
  const trimmed = trimGoSpace(name)
  if (!trimmed || trimmed.startsWith('-') || trimmed === '.' || trimmed === '..') return false
  return /^[A-Za-z0-9_.-]+$/.test(trimmed)
}

/** 抄 script_runner.go 的 isSafePythonModuleName（同样先 TrimSpace，note 用原 token）：模块名不合法时服务端整条命令报错 */
function isSafePythonModuleName(name: string): boolean {
  const trimmed = trimGoSpace(name)
  if (!trimmed || trimmed.startsWith('.') || trimmed.endsWith('.') || trimmed.includes('..')) return false
  return /^[A-Za-z0-9_.]+$/.test(trimmed)
}

/** no_script（托管依赖命令）的 note，抄 taskScriptNoteManagedFmt；task/desi 与直接写命令名两条路径共用 */
function managedCommandNote(name: string): string {
  return `命令不是直接运行脚本文件（由 ${name} 执行），不会一并删除任何文件。`
}

/**
 * `-m` 后面那个超时取值合不合法，照抄 @/utils/taskCommandScript 的 isValidTaskTimeoutValue
 * （那边没导出，见 DEMO_COMMAND_INTERPRETERS 的注释）。两份都是 script_runner.go 的 parseTaskTimeoutSeconds 的镜像，
 * 服务端改超时格式时三处要一起改。
 * 去首尾空白要贴住 Go 的 strings.TrimSpace（trimGoSpace），不能用 JS 的 trim：token 来自 splitCommandTokens，
 * 带引号的取值（-m " 30m "）会把空白原样带进来。
 */
function isValidTaskTimeoutValue(raw: string): boolean {
  let value = trimGoSpace(raw).toLowerCase()
  if (!value) return false

  // 末位是 s / m / h / d 就当单位后缀削掉，其余字符原样留给下一步的数字解析去否掉
  const suffix = value[value.length - 1]
  if (suffix === 's' || suffix === 'm' || suffix === 'h' || suffix === 'd') {
    value = value.slice(0, -1)
  }

  // strconv.Atoi 只认「可选正负号 + 一串 ASCII 数字」，5x、小数点、空串一律报错
  if (!/^[+-]?[0-9]+$/.test(value)) return false
  // 负数落在服务端的 seconds <= 0 分支
  if (value.startsWith('-')) return false

  // 去掉正号与前导零；全是 0 则得 0，同样落在 seconds <= 0
  const digits = value.replace(/^\+/, '').replace(/^0+/, '')
  if (!digits) return false

  // Atoi 超出 int64 会报错。这个量级 Number 已经丢精度，只能按十进制字符串比大小
  const int64Max = '9223372036854775807'
  if (digits.length > int64Max.length) return false
  if (digits.length === int64Max.length && digits > int64Max) return false

  return true
}

/** 抄 script_runner.go 的 isValidTaskRemainder：命令名后面的尾巴合不合法（desi 至少要跟一个环境变量名） */
function isValidTaskRemainder(remainder: string[], forcedDesi: boolean): boolean {
  if (forcedDesi) return remainder.length >= 1
  if (remainder.length === 0) return true
  switch (remainder[0]) {
    case 'now':
      return remainder.length === 1
    case 'conc':
    case 'desi':
      return remainder.length >= 2
    default:
      return true
  }
}

/** 抄 Go 的 filepath.Ext（Linux 口径：只认 / 为分隔符）再转小写，对应 isSupportedScriptExtension 里的 ToLower */
function demoFileExtension(path: string): string {
  for (let i = path.length - 1; i >= 0; i--) {
    const ch = path[i]
    if (ch === '/') break
    if (ch === '.') return path.slice(i).toLowerCase()
  }
  return ''
}

/**
 * 候选能不能过 resolveCommandScriptPath 里只看文本的两道检查：危险字符黑名单，以及 ResolveWithinBase 的绝对路径判断
 * （它先 TrimSpace，这里也先去首尾空白）。演示站没有脚本目录的绝对路径，绝对路径一律算不过（见区块开头 outside_scripts_dir 一条）。
 * 没过的候选服务端同样记成 lastResolveErr，只是错误不是「文件不存在」，最后不会落到 not_found。
 */
function isDemoCandidateTextSafe(candidate: string): boolean {
  if (DEMO_DANGEROUS_PATH_FRAGMENTS.some((fragment) => candidate.includes(fragment))) return false
  return !trimGoSpace(candidate).startsWith('/')
}

/** searchDemoScriptCandidates 的结果 */
interface DemoScriptSearch {
  /** 文件存在（task / desi 还要求尾巴合法）的最长候选，归一化后的相对路径；一个都没有时为空串 */
  found: string
  /**
   * 最后一个（也就是最长的）没能解析的带扩展名候选，对应服务端的 lastResolveErr：
   * missing = 文件不存在，path 是它归一化后的相对路径（对应 HintPath）；rejected = 没过 isDemoCandidateTextSafe。
   * 一个都没有时为 null，对应服务端的「脚本不存在或命令格式无效」。
   */
  lastFailure: { kind: 'missing'; path: string } | { kind: 'rejected' } | null
}

/**
 * 从 1 个 token 开始逐个加长拼成候选，逐个查文件在不在（findScriptFile 代替 os.Stat），
 * 抄 findTaskScriptTarget（remainder 非 null：task / desi 要校验尾巴）与 findScriptTarget（null：解释器不校验）。
 * 顺序与服务端一致：先看扩展名，再过 resolveCommandScriptPath（危险字符 → 绝对路径 → 文件是否存在），最后才校验尾巴；
 * 尾巴不合法只是跳过这个候选，不改 lastFailure（服务端同样不记错误）。
 * 所以 task a.py b.py（只有 a.py 存在）会退回较短的 a.py，而不是拿最长的「a.py b.py」去判不存在。
 */
function searchDemoScriptCandidates(tokens: string[], remainder: { forcedDesi: boolean } | null): DemoScriptSearch {
  const search: DemoScriptSearch = { found: '', lastFailure: null }
  for (let count = 1; count <= tokens.length; count++) {
    const candidate = tokens.slice(0, count).join(' ')
    if (!DEMO_SCRIPT_EXTENSIONS.includes(demoFileExtension(candidate))) continue
    const path = normalizeScriptPath(candidate)
    if (!isDemoCandidateTextSafe(candidate) || !path) {
      search.lastFailure = { kind: 'rejected' }
      continue
    }
    if (!findScriptFile(path)) {
      search.lastFailure = { kind: 'missing', path }
      continue
    }
    if (remainder && !isValidTaskRemainder(tokens.slice(count), remainder.forcedDesi)) continue
    // 更长的合法候选会覆盖前面的，等价于服务端循环到底后留下 bestCount 最大的那个
    search.found = path
  }
  return search
}

/**
 * 一个脚本都没找到时的分类，抄 classifyTaskScriptParseError + taskScriptHintPath。
 * 最后失败的候选是「文件不存在」才判 not_found，路径就是那个候选（这里不会是空串，用不到服务端不带路径的那句 note）；
 * 否则（危险字符、绝对路径，或者压根没有带扩展名的候选）判 unresolved——前两种服务端有时会分出
 * outside_scripts_dir，演示站不模拟，见区块开头。
 */
function demoSearchFailureTarget(lastFailure: DemoScriptSearch['lastFailure']): DemoTaskScriptTarget {
  if (lastFailure && lastFailure.kind === 'missing') {
    return { status: 'not_found', path: lastFailure.path, note: `命令里的脚本 ${lastFailure.path} 已经不存在，只删除任务。` }
  }
  return { status: 'unresolved', path: '', note: NOTE_UNRESOLVED }
}

/**
 * task / desi 之后的部分，抄 parseTaskCommandPlan：开头的 -m <超时> / -l → 按第一个 -- 切掉透传参数 →
 * 逐个候选找脚本 → 一个都找不到时退一步，把第一个词当成依赖安装后暴露的托管命令（findTaskManagedCommandTarget）。
 * task dailycheckin a.py 这种是能正常运行的命令，得判 no_script，不能说成「脚本已经不存在」或「命令格式可能有误」。
 * 下面每一步不合法，服务端都会让整条命令报错（任务根本跑不起来），这里就一起判 unresolved。
 */
function resolveDemoTaskShellTarget(tokens: string[], forcedDesi: boolean): DemoTaskScriptTarget {
  const unresolved: DemoTaskScriptTarget = { status: 'unresolved', path: '', note: NOTE_UNRESOLVED }

  // 开头的选项：-m 带一个超时取值，不只是跳过，还要按 parseTaskTimeoutSeconds 校验
  // （task -m 5x dailycheckin 服务端直接报「无效的超时时间」）；-l 是纯开关
  let idx = 0
  while (idx < tokens.length) {
    if (tokens[idx] === '-m') {
      const rawTimeout = tokens[idx + 1]
      if (rawTimeout === undefined || !isValidTaskTimeoutValue(rawTimeout)) return unresolved
      idx += 2
      continue
    }
    if (tokens[idx] === '-l') {
      idx += 1
      continue
    }
    break
  }

  // 第一个 -- 之后是透传给脚本的参数（splitTaskShellAndScriptArgs），不参与判定
  let rest = tokens.slice(idx)
  const separatorIndex = rest.indexOf('--')
  if (separatorIndex >= 0) rest = rest.slice(0, separatorIndex)
  if (rest.length === 0) return unresolved

  const search = searchDemoScriptCandidates(rest, { forcedDesi })
  if (search.found) return { status: 'resolved', path: search.found, note: '' }

  // 一个脚本都没找到，退一步按托管命令认。服务端不把带扩展名的词当托管命令（filepath.Ext 非空）；
  // isManagedExecutableName 不放行路径分隔符，点只可能出现在最后一段里，所以「含点」就等价于 Ext 非空：
  // task python3.13、task daily.checkin 都走不到 no_script
  const name = rest[0] ?? ''
  if (!name.includes('.') && isManagedExecutableName(name) && isValidTaskRemainder(rest.slice(1), forcedDesi)) {
    return { status: 'no_script', path: '', note: managedCommandNote(name) }
  }

  // 托管命令也不成立时，服务端返回的是找脚本那一步的错误（lastResolveErr）
  return demoSearchFailureTarget(search.lastFailure)
}

/**
 * 把一条任务命令解析成脚本目标（抄 ParseCommandExecutionPlan + ResolveTaskScriptTarget 的分类与 tasks[].note 文案）。
 * 找脚本时和服务端一样逐个候选查文件在不在，而不是只看命令文本取最长的一个：否则 task a.py b.py（只有 a.py 存在）
 * 会被说成「a.py b.py 已经不存在」，task dailycheckin a.py 也会被当成缺了脚本，而服务端前者删得了 a.py、后者判 no_script。
 */
function resolveDemoTaskScriptTarget(command: string): DemoTaskScriptTarget {
  const unresolved: DemoTaskScriptTarget = { status: 'unresolved', path: '', note: NOTE_UNRESOLVED }

  // 与服务端同一套切词（引号、反斜杠转义）；引号没闭合时服务端整条命令报错
  const tokens = splitCommandTokens(command)
  if (!tokens) return unresolved
  const head = tokens[0] ?? ''

  if (head === 'task' || head === 'desi') {
    return resolveDemoTaskShellTarget(tokens.slice(1), head === 'desi')
  }

  if (DEMO_COMMAND_INTERPRETERS.includes(head)) {
    const rest = tokens.slice(1)
    // python -m <模块> 跑的是模块不是脚本文件，parseInterpreterCommandPlan 在这里就分叉了
    if (DEMO_PYTHON_INTERPRETERS.includes(head) && rest[0] === '-m') {
      const moduleName = rest[1] ?? ''
      if (isSafePythonModuleName(moduleName)) {
        return { status: 'no_script', path: '', note: `命令运行的是 Python 模块 ${moduleName}，没有对应的脚本文件。` }
      }
      return unresolved
    }
    // 解释器命令抄 findScriptTarget：不校验尾巴，找不到也不退回托管命令
    const search = searchDemoScriptCandidates(rest, null)
    if (search.found) return { status: 'resolved', path: search.found, note: '' }
    return demoSearchFailureTarget(search.lastFailure)
  }

  // 其余首词：长得像命令名就是托管依赖命令（parseManagedCommandPlan），否则服务端报「不支持的解释器」
  if (isManagedExecutableName(head)) {
    return { status: 'no_script', path: '', note: managedCommandNote(head) }
  }
  return unresolved
}

/** 抄 Go 的 strings.Split(url, "/") 再取最后一段 */
function lastUrlSegment(url: string): string {
  const parts = url.split('/')
  return parts[parts.length - 1] ?? ''
}

/** 把订阅算出来的目录 / 文件路径归一成脚本树的形状（服务端的 filepath.Join 会顺手 Clean 掉重复斜杠） */
function normalizeSubscriptionPath(raw: string): string {
  return normalizeScriptPath(raw).replace(/\/{2,}/g, '/').replace(/\/+$/, '')
}

/**
 * 这个脚本归不归某个订阅管（抄 classify 的 subscription_managed 判定）。
 * 订阅已停用也算：重新启用后的下一次拉取照样会把文件还原。
 * 不看任务上的 subscription:N 标签：创建任务时标签原样写入、可以伪造，只以路径为准
 * （种子任务 12、13 带 subscription:1 标签，但路径不在 subscriptions/ops-scripts 下，照样可删）。
 */
function findManagingSubscription(path: string): DemoSubscription | undefined {
  return db().subscriptions.find((sub) => {
    if (sub.type === 'single-file') {
      // 公式抄 subscription.go 的 pullSingleFileWithCallback：(SaveDir 或 downloads)/(Alias 或 URL 末段)。
      // 只保护这一个下载目标，同目录下的其他文件照常判定。
      const dest = normalizeSubscriptionPath(`${sub.save_dir || 'downloads'}/${sub.alias || lastUrlSegment(sub.url)}`)
      return dest === path
    }
    // git 订阅：根目录公式抄 subscription.go 的 subscriptionSaveDir（SaveDir → Alias → URL 末段去掉 .git）
    const root = normalizeSubscriptionPath(sub.save_dir || sub.alias || lastUrlSegment(sub.url).replace(/\.git$/, ''))
    // 根目录等于脚本目录或在它上层：服务端只在那里真是 git 仓库时才保护，演示站没有 .git，一律不算（见区块开头）
    if (!root || root === '.' || root === '..' || root.startsWith('../')) return false
    return path.startsWith(`${root}/`)
  })
}

/** 抄 server/model/system_config_registry.go 的 parseBoolString：认不出的值返回 undefined */
function parseConfigBool(raw: string | undefined): boolean | undefined {
  switch ((raw ?? '').trim().toLowerCase()) {
    case '1':
    case 'true':
    case 'yes':
    case 'on':
      return true
    case '0':
    case 'false':
    case 'no':
    case 'off':
      return false
    default:
      return undefined
  }
}

/** 抄 GetRegisteredConfigBool：库里没有值、或值认不出时回落注册表默认值 */
function demoConfigBool(key: string, fallback: boolean): boolean {
  const item = db().configs[key]
  const defaultValue = parseConfigBool(item?.default_value) ?? fallback
  if (!item?.value) return defaultValue
  return parseConfigBool(item.value) ?? defaultValue
}

/** 抄 resolveSubscriptionForceOverwrite：订阅显式选了 force / preserve 就以订阅为准，inherit（含空串、脏值）回落全局 */
function resolveDemoSubscriptionForceOverwrite(sub: DemoSubscription): boolean {
  const mode = (sub.overwrite_mode ?? '').trim().toLowerCase()
  if (mode === 'force') return true
  if (mode === 'preserve') return false
  return demoConfigBool('subscription_force_overwrite', true)
}

/** 抄 resolveSubscriptionAutoAddTask：订阅显式选了 enabled / disabled 就以订阅为准，inherit 回落全局 auto_add_cron */
function resolveDemoSubscriptionAutoAddTask(sub: DemoSubscription): boolean {
  const mode = (sub.auto_add_task_mode ?? '').trim().toLowerCase()
  if (mode === 'enabled') return true
  if (mode === 'disabled') return false
  return demoConfigBool('auto_add_cron', true)
}

/** subscription_managed 的三种文案（逐字抄服务端）；「自动添加任务」生效时 {readd} 换成后半句 */
function subscriptionManagedDetail(sub: DemoSubscription): string {
  const readd = resolveDemoSubscriptionAutoAddTask(sub) ? '，并自动重新创建对应的任务' : ''
  if (sub.type === 'single-file') {
    return `这是订阅「${sub.name}」下载的文件，每次拉取都会重新下载${readd}。如果不再需要，请停用或删除这个订阅。`
  }
  if (resolveDemoSubscriptionForceOverwrite(sub)) {
    return `位于订阅「${sub.name}」的仓库目录里，下次拉取会把它还原${readd}。如果不再需要，请在订阅设置里用白名单或黑名单把它排除。`
  }
  return `位于订阅「${sub.name}」的仓库目录里；这个订阅设为「保留本地修改」，删除会被当成本地改动，上游之后再改这个文件时拉取会冲突并持续失败。如果不再需要，请用白名单或黑名单排除它。`
}

/**
 * shared 的文案：名称最多列 3 个；text_match 的任务名后加「（按命令文本匹配）」（演示站恒为 false，见区块开头）。
 * 代词按共用方总人数选，与服务端同一条规则：只有 1 个时写「它」，2 个及以上写「它们」
 * （只有一个共用方时写「它们」读着像是漏了人）。只在有共用方时才调用，不会出现 0。
 */
function sharedDetail(sharedBy: DemoTaskScriptRef[]): string {
  const names = sharedBy.slice(0, 3).map((ref) => (ref.text_match ? `${ref.name}（按命令文本匹配）` : ref.name))
  const more = sharedBy.length > 3 ? ' 等' : ''
  const pronoun = sharedBy.length === 1 ? '它' : '它们'
  return `仍被 ${sharedBy.length} 个其他任务使用：${names.join('、')}${more}，删除后${pronoun}会无法运行。`
}

/**
 * 抄 subscription.go 的 subscriptionHelperScriptNames：按「去掉扩展名后的 basename，小写」匹配。
 * 命中只给 warning、不阻断删除 —— 名单里有 sign，阻断的话最常见的签到脚本永远删不掉。
 */
const DEMO_HELPER_SCRIPT_NAMES = new Set([
  'sendnotify',
  'sendnofity',
  'notify',
  'sendnotify_',
  'jdcookie',
  'ql',
  'qlapi',
  'utils',
  'util',
  'common',
  'helper',
  'sign',
  'magic',
  'jsencrypt',
  'cryptojs',
])

function helperScriptWarnings(path: string): string[] {
  const filename = path.slice(path.lastIndexOf('/') + 1)
  // Go 的 filepath.Ext 从最后一个点算扩展名，TrimSuffix 之后剩下的就是这里的 stem
  const dot = filename.lastIndexOf('.')
  const stem = dot >= 0 ? filename.slice(0, dot) : filename
  if (!DEMO_HELPER_SCRIPT_NAMES.has(stem.toLowerCase())) return []
  return [`文件名 ${stem} 常见于公共库（如 sendNotify、utils、sign），可能被其他脚本引用，确认不再需要再删。`]
}

/**
 * scripts[] 按 path 升序。服务端是 Go 的字符串比较（按字节），ASCII 路径下与这里按码元比较一致；
 * 不用 localeCompare，它会按语言规则重排，与服务端顺序对不上。
 */
function comparePath(left: DemoTaskScriptItem, right: DemoTaskScriptItem): number {
  if (left.path === right.path) return 0
  return left.path < right.path ? -1 : 1
}

/**
 * 预览与执行共用的判定（抄 classify）。
 *
 * afterDelete=false 是预览：请求里的任务还在库里，按「其他任务」口径把它们剔除；
 * afterDelete=true 是执行：请求里的任务按理已经删掉，还留在库里的就是「没删成功」（task_not_deleted）。
 * 两个阶段都基于调用时库里的任务重新算，所以预览之后有人复制了任务、改了命令，执行时会重新拦住。
 */
function classifyDemoTaskScripts(
  plan: Pick<DemoTaskScriptPlan, 'ids' | 'snapshots' | 'targets'>,
  afterDelete: boolean,
): DemoTaskScriptItem[] {
  const requested = new Set(plan.ids)

  // 按路径合并：演示站没有软链接、没有大小写不敏感的文件系统，归一化后的相对路径就是「同一个文件」
  // （服务端用 os.SameFile 判定）。task_ids 保持请求顺序。
  const groups = new Map<string, DemoTaskSnapshot[]>()
  for (const snapshot of plan.snapshots) {
    const target = plan.targets.get(snapshot.id)
    if (!target || target.status !== 'resolved') continue
    const members = groups.get(target.path)
    if (members) members.push(snapshot)
    else groups.set(target.path, [snapshot])
  }

  const current = db()
  // 其他任务 = 全部任务剔除本次请求的 id，已禁用的也算（用户随时可能重新启用它）。
  // 服务端强调「禁止 NOT IN、全表加载后在 Go 里过滤」，这里是内存过滤，天然没有那个坑。
  // 按 id 升序，对齐服务端全表加载的顺序，shared_by 的先后才一致。
  // 每个任务都按请求任务同一套解析（resolveDemoTaskScriptTarget，逐个候选查存在）算出它实际运行的脚本，再按路径比相等。
  // ⚠️ 不能改回 taskCommandMatchesScript：它不查文件在不在、只取最长的候选。其他任务写成 task a.py b.py
  //    （只有 a.py 存在）时它认不出这个任务在用 a.py，演示站就会把服务端判 shared 的文件报成可删。
  const scriptPathOf = (command: string) => {
    const target = resolveDemoTaskScriptTarget(command)
    return target.status === 'resolved' ? target.path : ''
  }
  const others = current.tasks
    .filter((task) => !requested.has(task.id))
    .sort((left, right) => left.id - right.id)
    .map((task) => ({ task, path: scriptPathOf(task.command) }))
  const leftovers = afterDelete
    ? current.tasks.filter((task) => requested.has(task.id)).map((task) => ({ task, path: scriptPathOf(task.command) }))
    : []

  const items: DemoTaskScriptItem[] = []
  for (const [path, members] of groups) {
    const sharedBy: DemoTaskScriptRef[] = others
      .filter((other) => other.path === path)
      .map((other) => ({ id: other.task.id, name: other.task.name, text_match: false }))

    const item: DemoTaskScriptItem = {
      path,
      task_ids: members.map((snapshot) => snapshot.id),
      deletable: false,
      reason: '',
      detail: '',
      // 只要有共用方就填，不论最终 reason 是什么
      shared_by: sharedBy,
      warnings: helperScriptWarnings(path),
    }

    // 按服务端 decide 的顺序，第一个命中的就是最终 reason（永久性原因在前、暂时性原因在后）。
    // 前 5 条（check_failed / hidden_path / symlink / not_regular_file / managed_helper）演示站不模拟。
    const subscription = findManagingSubscription(path)
    const leftover = leftovers.find((entry) => entry.path === path)?.task
    const running = members.find((snapshot) => isTaskActiveStatus(snapshot.status))
    if (subscription) {
      item.reason = 'subscription_managed'
      item.detail = subscriptionManagedDetail(subscription)
    } else if (leftover) {
      item.reason = 'task_not_deleted'
      item.detail = `任务「${leftover.name}」没有删除成功，脚本先保留。`
    } else if (sharedBy.length > 0) {
      item.reason = 'shared'
      item.detail = sharedDetail(sharedBy)
    } else if (running) {
      item.reason = 'task_running'
      item.detail = `任务「${running.name}」正在运行或排队中，脚本先保留；等运行结束后可以到脚本管理里删除。`
    }
    item.deletable = item.reason === ''
    items.push(item)
  }

  return items.sort(comparePath)
}

/**
 * 删掉一个脚本文件，但保留它的各级父目录。
 * 服务端只 os.Remove 这一个普通文件、绝不删父目录；而演示站的目录大多是从文件路径推出来的
 * （allScriptDirs），文件删光了目录会跟着从树里消失，所以把父目录显式登记进 scriptDirs。
 * 要登记每一级：buildScriptTree 从根往下找，中间哪一级不在集合里，下面的空目录就再也点不到。
 */
function removeDemoScriptFile(path: string) {
  const current = db()
  current.scriptFiles = current.scriptFiles.filter((file) => file.path !== path)
  const segments = path.split('/')
  segments.pop()
  let prefix = ''
  for (const segment of segments) {
    prefix = prefix ? `${prefix}/${segment}` : segment
    if (!current.scriptDirs.includes(prefix)) current.scriptDirs.push(prefix)
  }
}

/**
 * 拍快照 + 算预览（抄 PreviewTaskScriptDeletion / CollectTaskScriptTargets）。
 *
 * 预览端点直接交出 plan.preview；三个删除端点必须在【删任务之前】调用它 ——
 * 任务删掉之后就拿不到它的命令了（服务端是 Collect → 原删除语句 → Execute 同一顺序）。
 */
export function planDemoTaskScriptDeletion(ids: number[]): DemoTaskScriptPlan {
  // 按首次出现的顺序去重，与服务端一致
  const uniqueIds: number[] = []
  for (const id of ids) {
    if (!uniqueIds.includes(id)) uniqueIds.push(id)
  }

  const snapshots: DemoTaskSnapshot[] = []
  const targets = new Map<number, DemoTaskScriptTarget>()
  const tasks: DemoTaskScriptTaskInfo[] = []
  for (const id of uniqueIds) {
    const task = findTask(id)
    if (!task) {
      tasks.push({
        id,
        name: '',
        found: false,
        running: false,
        script_status: 'task_not_found',
        script_path: '',
        note: NOTE_TASK_NOT_FOUND,
      })
      continue
    }

    snapshots.push({ id: task.id, name: task.name, command: task.command, status: task.status })
    const target = resolveDemoTaskScriptTarget(task.command)
    targets.set(task.id, target)
    tasks.push({
      id: task.id,
      name: task.name,
      found: true,
      running: isTaskActiveStatus(task.status),
      script_status: target.status,
      script_path: target.path,
      note: target.note,
    })
  }

  const plan = { ids: uniqueIds, snapshots, targets }
  return { ...plan, preview: { checked: true, tasks, scripts: classifyDemoTaskScripts(plan, false) } }
}

/**
 * 删完任务之后真正删脚本（抄 (*TaskScriptCleanup).Execute）。
 *
 * confirm 只能收窄、不能扩大：
 *   - undefined / null = 调用方没传（APP、OpenAPI 不先调预览）：服务端判定为可删的全部删除；
 *   - 数组 = 只删与其中某一项完全相等（大小写敏感）的路径，其余可删项标 not_confirmed 保留；空数组 = 一个都不删。
 * 执行阶段额外会出现的原因里，演示站只实现 not_confirmed 与 not_found（changed / remove_failed 见区块开头）。
 */
export function executeDemoTaskScriptDeletion(
  plan: DemoTaskScriptPlan,
  confirm?: string[] | null,
): DemoTaskScriptDeleteResult {
  const confirmed = confirm === undefined || confirm === null ? null : new Set(confirm)
  const deleted: DemoTaskScriptItem[] = []
  const skipped: DemoTaskScriptItem[] = []

  for (const item of classifyDemoTaskScripts(plan, true)) {
    if (!item.deletable) {
      skipped.push(item)
      continue
    }
    if (confirmed && !confirmed.has(item.path)) {
      skipped.push({
        ...item,
        deletable: false,
        reason: 'not_confirmed',
        detail: '这个脚本不在你确认删除的列表里（可能是确认之后任务命令被改过），已保留。',
      })
      continue
    }
    if (!findScriptFile(item.path)) {
      skipped.push({ ...item, deletable: false, reason: 'not_found', detail: '文件已经不存在，无需删除。' })
      continue
    }
    removeDemoScriptFile(item.path)
    deleted.push(item)
  }

  // tasks 用删除前拍的快照：任务行已经删掉，再查库就查不到了
  return { tasks: plan.preview.tasks.map((info) => ({ ...info })), deleted, skipped }
}
