/**
 * 在线演示 Demo 的内存数据模型。
 *
 * 字段名一律照抄服务端的 model / handler，**不要照抄 web/mock-server.mjs** ——
 * 那份 55 行的开发用 mock 早就和后端漂移了（它的 daily_stats 多一个 total、
 * 少一个 aborted、日期还是 10 位的）。真源在这些文件里：
 *   任务      server/model/task.go 的 ToDict()
 *   执行日志  server/model/task_log.go 的 ToDict()
 *   环境变量  server/model/env_var.go 的 ToDict()
 *   订阅      server/model/subscription.go 的 ToDict()
 *   通知渠道  server/model/notification.go 的 ToDict()
 *   Open API  server/model/open_app.go 的 ToDict()
 *   用户      server/model/user.go 的 ToDict()
 *   安全相关  server/model/security.go
 *
 * ⚠️ 本目录下的所有代码只会进入 `npm run build:demo` 的产物，见 demo/index.ts 的说明。
 */

/** server/model/task.go:11-19 —— 状态与类型常量 */
export const TASK_STATUS_DISABLED = 0
export const TASK_STATUS_QUEUED = 0.5
export const TASK_STATUS_ENABLED = 1
export const TASK_STATUS_RUNNING = 2

/** server/model/task_log.go:7-12 */
export const LOG_STATUS_SUCCESS = 0
export const LOG_STATUS_FAILED = 1
export const LOG_STATUS_RUNNING = 2
export const LOG_STATUS_ABORTED = 3

export interface DemoTask {
  id: number
  name: string
  command: string
  python_version: string
  cron_expression: string
  task_type: string
  status: number
  /** 存的是数组；服务端库里是逗号串，但 ToDict() 下发的就是数组 */
  labels: string[]
  last_run_at: string | null
  last_run_status: number | null
  timeout: number
  success_exit_codes: string
  random_delay_seconds: number | null
  max_retries: number
  retry_interval: number
  notify_on_failure: boolean
  notify_on_success: boolean
  notify_on_abort: boolean
  notification_channel_id: number | null
  depends_on: number | null
  /**
   * 列表拖拽顺序，越小越靠前（server/model/task.go 的 ListOrder）。
   *
   * 与 sort_order 是两码事，别混用：sort_order 是【开机运行任务的执行顺序】契约，
   * 拿它做列表拖拽会静默改写用户的开机编排。默认排序里 list_order 排在 sort_order 之前，
   * 存量数据全是 0 ⇒ 整体顺序与加这个字段之前逐条一致。
   */
  list_order: number
  sort_order: number
  is_pinned: boolean
  pid: number | null
  log_path: string | null
  last_running_time: number | null
  task_before: string | null
  task_after: string | null
  allow_multiple_instances: boolean
  stop_schedule: string
  created_at: string
  updated_at: string
}

/**
 * 执行日志的结果类型。
 *
 * 它不是后端字段，是 Demo 自己的一层：status 只有四个值，区分不出
 * 「脚本自己报错」和「跑超时被面板杀掉」，而日志详情要按这两种情况给不同正文。
 * timeout 与 fail 最终都映射到 status=1（失败），与后端一致。
 */
export type DemoLogKind = 'ok' | 'fail' | 'timeout' | 'abort' | 'running'

/**
 * 存储态的执行日志行（比接口下发的字段少）。
 *
 * content / log_path / created_at / updated_at 都在响应时现算：
 * 正文按 kind 生成（几千条日志各存一份正文会把内存占用撑大一个数量级），
 * created_at 与 started_at 同值（后端两者由同一次写入产生）。
 */
export interface DemoTaskLog {
  id: number
  task_id: number
  /** 任务被删掉之后日志仍要显示任务名，所以这里存一份快照 */
  task_name: string
  status: number
  duration: number | null
  started_at: string
  ended_at: string | null
  kind: DemoLogKind
  /**
   * 正文覆盖值，只有「被假日志流滚过的那一次执行」才有（见 demo/taskRuns.ts）。
   * 其余日志一律留空，正文由 buildLogContent() 按 kind 现算 —— 几千条日志各存一份
   * 正文会把内存占用撑大一个数量级。
   */
  content?: string
}

export interface DemoEnvVar {
  id: number
  name: string
  value: string
  remarks: string
  enabled: boolean
  /** 组内顺序，越小越靠前（server/handler/env.go 的 position ASC） */
  position: number
  /** 0=普通区，1=置顶区（envNormalSortOrder / envPinnedSortOrder） */
  sort_order: number
  /** 逗号分隔的分组串，接口下发时会顺带拆成 groups 数组 */
  group: string
  created_at: string
  updated_at: string
}

export interface DemoSubscription {
  id: number
  name: string
  type: string
  url: string
  branch: string
  schedule: string
  whitelist: string
  blacklist: string
  depend_on: string
  pre_script: string
  hook_script: string
  // auto_add_task / auto_del_task 是 v3.2.5 之前的旧布尔字段，保留只做兼容，
  // 真正参与判定的是下面两个三态字段。
  auto_add_task: boolean
  auto_del_task: boolean
  // 订阅级「自动添加定时任务」三态：inherit=跟随全局 auto_add_cron / enabled=强制开启 / disabled=强制关闭。
  // 写成可选是为了对齐「缺省即 inherit」的后端口径；演示站自己已经不会产出缺省态了 ——
  // fixture 三条订阅都显式给了值，adapter.ts 的 POST / PUT /subscriptions 也会按
  // enabled / disabled 白名单归一后写进来（空值、脏值一律落 inherit）。
  // 留着 `?` 只是让页面读到 undefined 时仍按 inherit 处理，不用另外兜底。
  auto_add_task_mode?: string
  // 订阅级「自动删除失效任务」三态，取值同上，inherit 时回落到全局 auto_del_cron。
  auto_del_task_mode?: string
  enabled: boolean
  status: number
  last_pull_at: string | null
  sub_path: string
  save_dir: string
  ssh_key_id: number | null
  auth_type: string
  auth_username: string
  has_auth_token: boolean
  alias: string
  force_overwrite: boolean
  // 覆盖拉取策略：inherit=跟随全局 / force=强制覆盖 / preserve=保留本地修改。
  // force_overwrite 是 v2.2.15 之前的旧字段，只做只读兼容，真正生效的是这一个。
  overwrite_mode: string
  // 完整检出：true=放弃 sparse-checkout 拉整个仓库 / false=按子目录、白名单、依赖规则稀疏检出。
  // 只对 git 仓库订阅有意义，默认 false（与后端 model/subscription.go 的 FullCheckout 一致）。
  full_checkout: boolean
  created_at: string
  updated_at: string
}

export interface DemoSubLog {
  id: number
  subscription_id: number
  subscription_name: string
  status: number
  content: string
  duration: number
  created_at: string
}

export interface DemoSSHKey {
  id: number
  name: string
  created_at: string
  updated_at: string
}

export interface DemoNotifyChannel {
  id: number
  name: string
  type: string
  /** 服务端下发的就是 JSON 字符串，不是对象（notifications/index.vue:228 直接 JSON.parse） */
  config: string
  push_scope: string
  enabled: boolean
  today_send_count: number
  last_test_at: string | null
  last_test_status: string
  created_at: string
  updated_at: string
}

export interface DemoOpenApp {
  id: number
  name: string
  app_key: string
  app_secret: string
  scopes: string
  enabled: boolean
  rate_limit: number
  /** 列表接口下发的 call_count 实际是「今日调用次数」（handler/open_api.go:69） */
  call_count: number
  created_at: string
  updated_at: string
}

export interface DemoApiCallLog {
  id: number
  app_id: number
  app_name: string
  endpoint: string
  method: string
  status: number
  duration: number
  ip: string
  created_at: string
}

export interface DemoUser {
  id: number
  username: string
  role: string
  enabled: boolean
  /** 必须恒为空串：MainLayout 的三处头像 <img> 没有 @error 兜底，指向取不到的地址就是破图 */
  avatar_url: string
  last_login_at: string | null
  created_at: string
  updated_at: string
}

export interface DemoTaskView {
  id: number
  name: string
  filters: string
  sort_rules: string
  hidden: boolean
  sort_order: number
  created_at: string
  updated_at: string
}

export interface DemoDependency {
  id: number
  type: string
  name: string
  python_version: string
  status: string
  created_at: string
  updated_at: string
}

export interface DemoLoginLog {
  id: number
  user_id: number
  username: string
  ip: string
  client_name: string
  user_agent: string
  method: string
  status: number
  message: string
  created_at: string
}

export interface DemoSession {
  id: number
  user_id: number
  username: string
  client_type: string
  client_name: string
  ip: string
  user_agent: string
  expires_at: string
  created_at: string
}

export interface DemoIPWhitelist {
  id: number
  ip: string
  remarks: string
  created_at: string
}

export interface DemoBackupFile {
  name: string
  size: number
  created_at: string
}

/** 脚本树里的一个节点：目录只有 path，文件带 content */
export interface DemoScriptFile {
  path: string
  content: string
  mtime: number
}

/** GET /configs 的单项，形状见 server/handler/config.go 的 buildConfigResponseItem */
export interface DemoConfigItem {
  value?: string
  description?: string
  updated_at?: string | null
  registered?: boolean
  default_value?: string
  value_type?: string
  group?: string
  group_label?: string
  label?: string
  order?: number
  secret?: boolean
  min?: number
  max?: number
  options?: Array<{ value: string; label: string }>
}

/**
 * Demo 的全部可写状态。
 *
 * 纯内存，不落任何存储（见 db.ts）：刷新页面 = 回到初始 fixture，
 * 与横幅/README/issue 里「刷新即恢复初始数据」的承诺是同一件事。
 */
export interface DemoDbState {
  tasks: DemoTask[]
  taskViews: DemoTaskView[]
  logs: DemoTaskLog[]
  envs: DemoEnvVar[]
  subscriptions: DemoSubscription[]
  subLogs: DemoSubLog[]
  sshKeys: DemoSSHKey[]
  channels: DemoNotifyChannel[]
  openApps: DemoOpenApp[]
  apiCallLogs: DemoApiCallLog[]
  users: DemoUser[]
  deps: DemoDependency[]
  loginLogs: DemoLoginLog[]
  sessions: DemoSession[]
  ipWhitelist: DemoIPWhitelist[]
  backups: DemoBackupFile[]
  /** 脚本目录（只存目录本身，文件的父目录会自动补出来） */
  scriptDirs: string[]
  scriptFiles: DemoScriptFile[]
  /** 面板配置文件（GET/PUT /system/config-script） */
  configScript: string
  /** 系统配置：键 -> 配置项，初值来自 Go registry 导出的 configs.json */
  configs: Record<string, DemoConfigItem>
  /** 各表的自增游标，保证新建对象的 id 不与快照里的冲突 */
  seq: Record<string, number>
}
