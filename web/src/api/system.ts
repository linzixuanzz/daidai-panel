import request from './request'

export interface BackupSelection {
  configs: boolean
  tasks: boolean
  subscriptions: boolean
  env_vars: boolean
  logs: boolean
  scripts: boolean
  dependencies: boolean
  task_views: boolean
}

export interface RestoreProgressState {
  active: boolean
  status: 'idle' | 'running' | 'completed' | 'failed'
  filename?: string
  source?: string
  selection?: Partial<BackupSelection>
  stage?: string
  message?: string
  percent: number
  error?: string
  started_at?: string
  updated_at?: string
}

export interface PanelUpdateStatus {
  status?: 'idle' | 'running' | 'restarting' | 'completed' | 'failed'
  phase?: string
  message?: string
  error?: string
  started_at?: string
  updated_at?: string
  deployment_type?: 'docker' | 'binary' | 'magisk'
  container_name?: string
  image_name?: string
  pull_image_name?: string
  mirror_host?: string
  registry_url?: string
  release_version?: string
  asset_name?: string
  asset_url?: string
  install_dir?: string
  binary_name?: string
  update_manager?: 'panel' | 'watchtower'
  watchtower_response?: Record<string, any>
}

/**
 * GET /system/info 里与「部署形态」有关的两个字段（v3.0.4 新增）。
 *
 * 其余字段是资源快照，仍然平铺在同一层（服务端用匿名嵌入保证老字段位置不变），
 * 这里只声明前端真正要拿来做分支判断的两项。
 */
export interface SystemDeploymentInfo {
  /** 取值与 PanelUpdateStatus.deployment_type 同一套 */
  deployment_type?: 'docker' | 'binary' | 'magisk'
  /**
   * Magisk 模块外壳版本。非模块版、或 v3.0.3 之前的旧外壳都是 0。
   * 「停止面板服务」需要 >= 2（v3.0.4 的外壳），低于它只能重刷模块 ZIP。
   */
  magisk_shell_version?: number
}

/** 「停止面板服务」所需的最低模块外壳版本，与后端 magiskStopSupportedShellVersion 对齐 */
export const MAGISK_STOP_SUPPORTED_SHELL_VERSION = 2

export interface SystemHealthItem {
  name: string
  status: string
  message?: string
}

export interface SystemHealthSnapshot {
  items: SystemHealthItem[]
  last_checked_at?: string
}

export interface ConfigScriptPayload {
  content: string
  path: string
}

/**
 * 侧栏菜单角标的计数来源（GET /system/badges）。
 *
 * 服务端按角色裁剪：viewer 拿不到 subs_*，operator 拿不到 deps_*，
 * 无权项一律返回 0 而不是缺字段，所以这里全部声明为必填。
 *
 * 注意这里【没有】「有新版本」——检查更新每次都要打一发 GitHub 外网请求且无缓存，
 * 不能放进轮询接口。面板更新角标由 store 复用已发生的 checkUpdate 结果，见 stores/badges.ts。
 */
export interface SystemBadges {
  /** 正在运行的任务数 */
  tasks_running: number
  /** 今日失败的执行日志数 */
  logs_failed_today: number
  /** 最近一次拉取失败的订阅数 */
  subs_failed: number
  /** 安装失败的依赖数 */
  deps_failed: number
  /** 排队中 / 安装中 / 卸载中的依赖数合计 */
  deps_installing: number
}

// ---- v4.0「需要关注」（GET /system/attention，design §4.1，F2）----
// 响应体【没有】外层 data 包装：服务端直接返回 { items }（与 design §4 的 JSON 一致，badges 那种 {data} 是另一套写法）。
// 角色裁剪与 /system/badges 一致；已按 err → warn → info、同级按 at 倒序排好（v4.0.3 前最多 20 条）。
// v4.0.3（#186）起每条带 read（当前用户是否已读），排序不变；未读、已读各最多 20 条（合计最多 40 条），保持原顺序合并下发。
// 下面的字面量联合只是清单，外加 string 兜底：服务端以后新增类型时照样能显示。

export type AttentionType =
  | 'task_failing' // 启用中的任务最近 3 条已结束日志全部失败或超时
  | 'subscription_failed'
  | 'dependency_failed'
  | 'update_available' // 服务端【不会】下发（后端没有新版本缓存）：前端复用 checkUpdate 结果自行合并进列表（同 stores/badges.ts 的做法）
  | 'backup_failed' // 服务端目前【不会】下发（定时备份没有持久化的失败状态），保留类型供以后扩展
  | 'disk_high' // 数据盘使用率 ≥ 85%

export type AttentionLevel = 'err' | 'warn' | 'info'

export interface AttentionItem {
  /** 稳定键，例如 task_failing:12；用作 v-for key 与「已处理」动画 */
  key: string
  type: AttentionType | (string & {})
  level: AttentionLevel | (string & {})
  title: string
  /** 最近一次日志末尾第一条非空错误行等，≤120 字；可能为空串 */
  detail: string
  /** 一键处理的目标：kind 为 task / subscription / dependency / update / backup / disk 等，id 没有时为 0 */
  target: { kind: string; id: number }
  /** RFC3339 时间 */
  at: string
  /**
   * v4.0.3（#186）：当前用户是否已读。老面板没有这个字段，按未读处理。
   * 已读按「用户 + key + 版本」记在服务端，问题再次发生（at 变了）会自动回到未读。
   */
  read?: boolean
}

export interface SystemAttention {
  items: AttentionItem[]
}

export const systemApi = {
  info: () => request.get('/system/info'),
  machineCode: () => request.get('/system/machine-code'),
  dashboard: (range?: number) => request.get('/system/dashboard', { params: range ? { range } : undefined }),
  stats: () => request.get('/system/stats'),
  badges: () => request.get('/system/badges') as Promise<{ data: SystemBadges }>,
  /** v4.0 F2：需要关注的条目（viewer+）。响应没有 data 包装，见 SystemAttention 上方说明 */
  attention: () => request.get('/system/attention') as Promise<SystemAttention>,
  /**
   * v4.0.3（#186）：把「需要关注」的条目标记为已读。回传客户端看到的 key / at / level，
   * 服务端只记版本与当前一致的条目（其余保持未读）；返回 { marked } = 实际写入条数
   */
  attentionRead: (payload: { items: Array<Pick<AttentionItem, 'key' | 'at' | 'level'>> }) =>
    request.post('/system/attention/read', payload) as Promise<{ marked: number }>,
  version: () => request.get('/system/version'),
  publicVersion: () => request.get('/system/public-version'),
  panelSettings: () => request.get('/system/panel-settings'),
  checkUpdate: () => request.get('/system/check-update'),
  updateStatus: () => request.get('/system/update-status'),
  updatePanel: () => request.post('/system/update'),
  restart: () => request.post('/system/restart'),
  /**
   * 停止面板服务 —— 只有 Magisk 模块版可用，且要求外壳版本 >= 2。
   *
   * 与 restart 的区别：停止之后没有任何东西会把面板拉回来（重启手机也不会），
   * 所以【不要】复用 waitForRestart 那套轮询，它永远等不到。
   */
  stopPanel: () => request.post('/system/stop'),
  panelLog: (params?: { lines?: number; keyword?: string; level?: 'debug' | 'info' | 'warn' | 'error' | '' }) =>
    request.get('/system/panel-log', { params }),
  backup: (password?: string, selection?: Partial<BackupSelection>, name?: string) => request.post('/system/backup', { password, selection, name }),
  backupList: () => request.get('/system/backups'),
  downloadBackup: (filename: string) =>
    request.get('/system/backup/download', {
      params: { filename },
      responseType: 'blob',
    }) as Promise<Blob>,
  restoreProgress: () => request.get('/system/restore/progress'),
  restore: (filename: string, password?: string) =>
    request.post('/system/restore', { filename, password }, { timeout: 0 }),
  deleteBackup: (filename: string) =>
    request.delete('/system/backup', { params: { filename } }),
  healthStatus: () => request.get('/system/health-check') as Promise<SystemHealthSnapshot>,
  healthCheck: () => request.post('/system/health-check') as Promise<SystemHealthSnapshot>,
  uploadBackup: (file: File, onProgress?: (percent: number) => void) => {
    const formData = new FormData()
    formData.append('file', file)
    return request.post('/system/backup/upload', formData, {
      headers: { 'Content-Type': 'multipart/form-data' },
      timeout: 0,
      onUploadProgress: onProgress
        ? (e: any) => { if (e.total) onProgress(Math.round((e.loaded * 100) / e.total)) }
        : undefined,
    })
  },
}

export const configScriptApi = {
  get: () => request.get('/system/config-script') as Promise<ConfigScriptPayload>,
  save: (content: string) => request.put('/system/config-script', { content }) as Promise<{ message: string }>,
}

// ============================================================================
// 网页版系统命令行
//
// 一组 admin-only 接口（应用令牌进不来），形态与脚本调试的
// POST /scripts/run-code + GET /scripts/run/:id/logs 那一套完全一致：
// 提交一次拿 run_id，然后轮询取输出，中途可停止、结束后可清除。
//
// 它【不是】交互式 PTY：每次 run 都是一个独立的一次性 shell 进程，
// cd / export 出来的状态不会带到下一条命令，要连起来只能写成
// `cd /x && ls` 这种单条复合命令。这条约束必须在 UI 上说清楚，
// 否则用户会按终端的直觉一条条敲然后发现「目录没切过去」。
// ============================================================================

export interface SystemConsoleMeta {
  /** false 表示当前环境跑不了命令行（例如 Windows 裸机没有 bash），输入区要整体禁用 */
  available: boolean
  /** available=false 时的原因，原样展示给用户，不要自己编文案 */
  unavailable_reason?: string
  /** 实际使用的 shell（服务端当前固定是 bash） */
  shell?: string
  /** 命令的工作目录（面板脚本目录） */
  work_dir?: string
  /** 面板进程是不是 root。决定 apk add / apt-get install 能不能装得上 */
  is_root?: boolean
  /** 探测到的系统包管理器：apk / apt / dnf / yum / microdnf / zypper，探测不到是空串 */
  package_manager?: string
  distribution?: string
  /** 单条命令的超时分钟数，由系统配置 console_timeout_minutes 决定 */
  timeout_minutes?: number
}

/** 一次命令执行的输出快照。与脚本调试的轮询接口同形，外面还包一层 data */
export interface SystemConsoleRunLogs {
  logs: string[]
  /**
   * logs[0] 的全局行号，也就是服务端已经成块丢弃的行数（单次运行最多留 20 万行，
   * 触顶后一次丢掉最前面的 1/4）。
   *
   * 增量追加渲染必须按它算下标：只看 logs.length 的话，「本轮既截断、又新增了更多行」
   * 时长度反而变大，切片下标在整体左移过的新数组里指向的是完全不同的全局行。
   * 可选是为了兜住老服务端不下发的情况，读不到按 0 处理即可（没截断过时它本来就是 0）。
   */
  discarded?: number
  done: boolean
  exit_code?: number
  status?: string
}

export const systemConsoleApi = {
  meta: () => request.get('/system/console/meta') as Promise<SystemConsoleMeta>,
  run: (command: string) =>
    request.post('/system/console/run', { command }) as Promise<{ message?: string; run_id: string }>,
  logs: (runId: string) =>
    request.get(`/system/console/run/${runId}/logs`) as Promise<{ data: SystemConsoleRunLogs }>,
  stop: (runId: string) => request.put(`/system/console/run/${runId}/stop`) as Promise<{ message: string }>,
  clear: (runId: string) => request.delete(`/system/console/run/${runId}`) as Promise<{ message: string }>,
}

// ============================================================================
// 系统配置 schema
//
// 下面这几个类型与服务端 server/handler/config.go 的 buildConfigResponseItem
// 一一对应，是 GET /api/configs 的真实响应形状；字段真源在
// server/model/system_config_registry.go（47 项）。
//
// 这份 schema 由服务端持有唯一真源，Web 只负责渲染。
// 不做版本兼容降级：Web 与服务端永远同版本发布 —— release 产物里 web/ 目录和
// 二进制来自同一次构建，Docker 镜像同理，面板自更新脚本也是整个 web/ 目录替换。
// 不存在「新 Web + 老服务端」的组合。
// ============================================================================

/** 服务端 SystemConfigValueType 的四个取值 */
export type SystemConfigValueType = 'string' | 'int' | 'bool' | 'enum'

export interface SystemConfigOption {
  value: string
  label: string
}

export interface SystemConfigItemResponse {
  /** 当前值。服务端在库里没记录或记录为空串时已经替换成 default_value */
  value?: string
  /** 长句说明（个别项有三行），只能当 hint，不能当输入框标题 */
  description?: string
  updated_at?: string | null
  /** false 表示这是面板运行时自己写的临时状态，没有 schema，不要渲染也不要回写 */
  registered?: boolean
  default_value?: string
  value_type?: SystemConfigValueType
  group?: string
  /** group 这个英文 slug 对应的中文分组名 */
  group_label?: string
  /** 输入框标题用的短词 */
  label?: string
  /** 注册顺序。本接口返回的是 map 本身没有顺序，客户端要按声明顺序渲染只能靠它 */
  order?: number
  /** 凭据类配置，渲染时应当用密码框（服务端仍然明文下发） */
  secret?: boolean
  /** 只有 int 类型有值，供前端做范围校验（服务端仍会独立校验一次） */
  min?: number
  max?: number
  options?: SystemConfigOption[]
}

export type SystemConfigMap = Record<string, SystemConfigItemResponse>

export const configApi = {
  list: () => request.get('/configs') as Promise<{ data: SystemConfigMap }>,
  get: (key: string) => request.get(`/configs/${key}`),
  set: (data: { key: string; value: string; description?: string }) => request.post('/configs', data),
  batchSet: (configs: Record<string, string>) => request.put('/configs/batch', { configs }),
  delete: (key: string) => request.delete(`/configs/${key}`),
}

export const platformTokenApi = {
  platforms: () => request.get('/platform-tokens/platforms'),
  createPlatform: (data: { name: string; label?: string; icon?: string }) =>
    request.post('/platform-tokens/platforms', data),
  deletePlatform: (id: number) => request.delete(`/platform-tokens/platforms/${id}`),
  list: (platformId?: number) =>
    request.get('/platform-tokens', { params: platformId ? { platform_id: platformId } : {} }),
  create: (data: { platform_id: number; name: string; token: string; remarks?: string }) =>
    request.post('/platform-tokens', data),
  update: (id: number, data: { name?: string; token?: string; remarks?: string }) =>
    request.put(`/platform-tokens/${id}`, data),
  delete: (id: number) => request.delete(`/platform-tokens/${id}`),
  enable: (id: number) => request.put(`/platform-tokens/${id}/enable`),
  disable: (id: number) => request.put(`/platform-tokens/${id}/disable`),
}
