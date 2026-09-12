import request from './request'
import type { RawLogDownloadTicket } from '@/utils/rawLogDownload'

// ---- 删除任务时可选同时删除脚本（issue #124）----
// 字段名与取值逐一对应服务端的响应。契约以已跟踪的代码为准：server/service/task_script_cleanup.go
// 里的常量与 json tag，对外说明见 web/src/views/api-docs/apiData.ts（任务目录下的设计稿不进仓库，不要引用它）。
// 服务端保证数组一律是 [] 而不是 null，路径一律是相对脚本目录的正斜杠路径。
// 说明文案（detail / note / warnings）以服务端为唯一真源，前端原样展示、不另写 reason → 文案的映射；
// 所以下面的字面量联合只是给读代码的人看的清单，外加 string 兜底，服务端以后新增取值时照样能显示。
// 写成 `(string & {})` 而不是直接 `| string`：后者会把整个联合坍缩成 string，字面量提示就没了。

/** 预览里每个任务的脚本解析结果 */
export type TaskScriptStatus =
  | 'resolved' // 解析出了脚本，一定会出现在 scripts[] 里
  | 'no_script' // 托管命令或 python -m，没有脚本文件
  | 'not_found' // 命令里的脚本已经不存在
  | 'outside_scripts_dir' // 脚本不在脚本目录内
  | 'unresolved' // 命令格式有误，识别不出脚本；服务端读任务信息出错、无法确认时也是它（此时 found=false）
  | 'task_not_found' // 任务不存在

/** 脚本不能一起删的原因；空串表示可删。最后四个只会出现在执行结果里 */
export type TaskScriptDeletionReason =
  | 'check_failed'
  | 'hidden_path'
  | 'symlink'
  | 'not_regular_file'
  | 'managed_helper'
  | 'subscription_managed'
  | 'task_not_deleted'
  | 'shared'
  | 'referenced_in_hook'
  | 'task_running'
  | 'not_confirmed'
  | 'changed'
  | 'not_found'
  | 'remove_failed'

export interface TaskScriptRef {
  id: number
  name: string
  /** true = 这个任务不是直接运行该脚本（或命令当前跑不起来），但命令文本里指向了它 */
  text_match: boolean
}

export interface TaskDeletePreviewTask {
  id: number
  name: string
  found: boolean
  /** 运行中或排队中 */
  running: boolean
  script_status: TaskScriptStatus | (string & {})
  /** 只在 resolved / not_found 时有值 */
  script_path: string
  /** 服务端给的说明文案，原样展示 */
  note: string
}

export interface TaskScriptDeletionItem {
  path: string
  task_ids: number[]
  /** 当且仅当 reason 为空时为 true */
  deletable: boolean
  reason: '' | TaskScriptDeletionReason | (string & {})
  /** 服务端给的保留原因文案，原样展示 */
  detail: string
  /** 本次范围外、仍然引用这个文件的任务（含已禁用的） */
  shared_by: TaskScriptRef[]
  /** 只提示、不阻断删除 */
  warnings: string[]
}

export interface TaskDeletePreview {
  /** 恒为 true 的哨兵：演示站对没注册的端点会兜底回 { data: [] }，靠它区分「真的查过」与「没查成」 */
  checked: boolean
  tasks: TaskDeletePreviewTask[]
  scripts: TaskScriptDeletionItem[]
}

/** 带了删脚本开关的删除接口，在原响应上只追加这一个 scripts 字段 */
export interface TaskScriptDeletionResult {
  tasks: TaskDeletePreviewTask[]
  deleted: TaskScriptDeletionItem[]
  skipped: TaskScriptDeletionItem[]
}

/** 两个批量删除入口的可选开关（body 字段）。delete_scripts 必须是 JSON bool，传成字符串服务端会整单 400 */
export interface TaskBatchDeleteScriptOptions {
  delete_scripts?: boolean
  /** 原样回传预览里服务端给的路径；只能收窄删除范围，传 [] 表示一个都不删 */
  confirm_script_paths?: string[]
}

export const taskApi = {
  list(params?: { keyword?: string; status?: number | string; label?: string; page?: number; page_size?: number; filters?: string; sort_rules?: string; all?: 0 | 1 }) {
    return request.get('/tasks', { params }) as Promise<{ data: any[]; total: number; page: number; page_size: number }>
  },

  // push_scope 见 @/api/notification 的 NotifyPushScope：
  // 'bound' 的渠道不参与广播，任务不显式选中它就一条通知都收不到，任务表单需要据此打标。
  notificationChannels() {
    return request.get('/tasks/notification-channels') as Promise<{ data: { id: number; name: string; type: string; enabled: boolean; push_scope?: string }[] }>
  },

  create(data: any) {
    return request.post('/tasks', data) as Promise<{ message: string; data: any }>
  },

  update(id: number, data: any) {
    return request.put(`/tasks/${id}`, data) as Promise<{ message: string; data: any }>
  },

  // 不传 opts（或 deleteScript 不为真）时 params 为 undefined，axios 不拼任何 query，
  // 请求与改动前逐字节一致：APP 与老调用方都依赖「缺省不删脚本」。
  // confirmScriptPath 原样回传预览里服务端给的路径，只能收窄、不能扩大——服务端只从任务命令里解析
  // 要删的文件，请求里不存在「删哪个文件」的参数。它为 undefined 时 axios 会省略这个键（= 没传、不收窄）。
  delete(id: number, opts?: { deleteScript?: boolean; confirmScriptPath?: string }) {
    const params = opts?.deleteScript
      ? { delete_script: 1, confirm_script_path: opts.confirmScriptPath }
      : undefined
    return request.delete(`/tasks/${id}`, { params }) as Promise<{ message: string; scripts?: TaskScriptDeletionResult }>
  },

  run(id: number) {
    return request.put(`/tasks/${id}/run`) as Promise<{ message: string }>
  },

  stop(id: number) {
    return request.put(`/tasks/${id}/stop`) as Promise<{ message: string }>
  },

  enable(id: number) {
    return request.put(`/tasks/${id}/enable`) as Promise<{ message: string; data: any }>
  },

  disable(id: number) {
    return request.put(`/tasks/${id}/disable`) as Promise<{ message: string; data: any }>
  },

  pin(id: number) {
    return request.put(`/tasks/${id}/pin`) as Promise<{ message: string }>
  },

  unpin(id: number) {
    return request.put(`/tasks/${id}/unpin`) as Promise<{ message: string }>
  },

  // 列表拖拽排序：把 sourceId 挪到 targetId 前面（position='after' 时挪到后面）；
  // targetId 留空 = 移到本区末尾。这里的「区」= 置顶状态相同且状态分组（启用/禁用）相同的一批任务，
  // 跨区拖后端直接回 400，前端在 onEnd 里已经先拦过一道。
  // ⚠️ 它改的是 list_order（列表展示顺序），不是 sort_order（开机任务的执行顺序），两者互不影响。
  sort(sourceId: number, targetId?: number, position?: 'before' | 'after') {
    return request.put('/tasks/sort', { source_id: sourceId, target_id: targetId, position }) as Promise<{ message: string }>
  },

  copy(id: number) {
    return request.post(`/tasks/${id}/copy`) as Promise<{ message: string; data: any }>
  },

  // 清除订阅锁：任务重新跟随订阅源的名称与定时，下次拉取会被订阅源的值覆盖回来
  restoreSubscriptionDefault(id: number) {
    return request.put(`/tasks/${id}/restore-subscription-default`) as Promise<{ message: string; data: any }>
  },

  // signal 是给「并行预取」用的（LogViewer 打开弹窗时会先发一份，见 issue #109-1）：
  // 一旦流里来了实时数据，那份预取就作废了 —— 但如果不 abort，axios 仍会把响应体下完
  // 并在主线程 JSON.parse。运行中的大日志任务这一份可能是几十 MB，正好压在
  // 「弱机 + 大日志」这个本来要优化的场景上。不传 signal 时行为与以前完全一致。
  latestLog(id: number, signal?: AbortSignal) {
    return request.get(`/tasks/${id}/latest-log`, { signal }) as Promise<any>
  },

  liveLogs(id: number) {
    return request.get(`/tasks/${id}/live-logs`) as Promise<{ logs: string[]; done: boolean; status: number }>
  },

  logFiles(id: number) {
    return request.get(`/tasks/${id}/log-files`) as Promise<any[]>
  },

  logFileContent(id: number, filename: string, path?: string) {
    return request.get(`/tasks/${id}/log-files/${encodeURIComponent(filename)}`, { params: path ? { path } : undefined }) as Promise<{ filename: string; content: string }>
  },

  deleteLogFile(id: number, filename: string, path?: string) {
    return request.delete(`/tasks/${id}/log-files/${encodeURIComponent(filename)}`, { params: path ? { path } : undefined }) as Promise<{ message: string }>
  },

  // 换取「下载原始日志文件」的短期票据。真正的文件由浏览器原生下载去拉，不走 axios。
  logFileRawDownloadTicket(id: number, filename: string, path?: string) {
    return request.get(`/tasks/${id}/log-files/${encodeURIComponent(filename)}/raw-ticket`, { params: path ? { path } : undefined }) as Promise<RawLogDownloadTicket>
  },

  // 换取「日志文件夹打包下载」的短期票据，zip 同样由浏览器原生下载去拉。
  // 不传 start/end = 打包该任务的全部日志文件；两端都是 RFC3339，
  // 用 utils/datetime.ts 的 toDateRangeParams 生成，按磁盘 ModTime（列表里的「时间」列）筛。
  // 返回体里的 size 是【未压缩总字节】——流式 zip 事先算不出压缩后大小。
  logArchiveDownloadTicket(id: number, params?: { start?: string; end?: string }) {
    return request.get(`/tasks/${id}/log-files/archive-ticket`, { params }) as Promise<RawLogDownloadTicket & { file_count: number }>
  },

  stats(id: number, days?: number) {
    return request.get(`/tasks/${id}/stats`, { params: { days } }) as Promise<any>
  },

  // extra 只在批量删除且勾了「同时删除脚本」时才传；不传时 body 仍是 { ids, action }，与改动前逐字节一致。
  // 服务端只在 action === 'delete' 时认这两个字段，其它 action 带了也会被忽略。
  // count 只计查到的任务。
  batch(ids: number[], action: string, extra?: TaskBatchDeleteScriptOptions) {
    return request.put('/tasks/batch', { ids, action, ...(extra ?? {}) }) as Promise<{ message: string; count: number; scripts?: TaskScriptDeletionResult }>
  },

  batchEnable(taskIds: number[]) {
    return request.put('/tasks/batch/enable', { task_ids: taskIds }) as Promise<{ message: string; success_count: number }>
  },

  batchDisable(taskIds: number[]) {
    return request.put('/tasks/batch/disable', { task_ids: taskIds }) as Promise<{ message: string; success_count: number }>
  },

  // Web 端目前没有调用点（APP 批量删除在用）。开关语义同 batch；不传 extra 时 body 仍是 { task_ids }。
  // 注意这个入口的 count 等于传入 id 的个数（含不存在的 id），是服务端的现有口径，没有改。
  batchDelete(taskIds: number[], extra?: TaskBatchDeleteScriptOptions) {
    return request.delete('/tasks/batch/delete', { data: { task_ids: taskIds, ...(extra ?? {}) } }) as Promise<{ message: string; count: number; scripts?: TaskScriptDeletionResult }>
  },

  // 删除前的只读预览：每个任务解析出的脚本、能不能一起删、不能删的原因（服务端文案）。
  // 老后端没有这个接口会 404，应用令牌缺 scripts 权限、观察者会 403——调用方都必须能降级成「只删任务」。
  deletePreview(taskIds: number[]) {
    return request.post('/tasks/delete-preview', { task_ids: taskIds }) as Promise<{ data: TaskDeletePreview }>
  },

  batchRun(taskIds: number[]) {
    return request.post('/tasks/batch/run', { task_ids: taskIds }) as Promise<{ message: string; count: number }>
  },

  batchAddLabels(taskIds: number[], labels: string[]) {
    return request.put('/tasks/batch/add-labels', { task_ids: taskIds, labels }) as Promise<{ message: string; success_count: number }>
  },

  cleanLogs(days?: number) {
    return request.delete('/tasks/clean-logs', { params: { days } }) as Promise<{ message: string }>
  },

  export() {
    return request.get('/tasks/export') as Promise<{ data: any[] }>
  },

  import(tasks: any[]) {
    return request.post('/tasks/import', { tasks }) as Promise<{ message: string; errors: string[] }>
  },

  cronParse(expression: string) {
    return request.post('/tasks/cron/parse', { expression }) as Promise<any>
  },

  cronTemplates() {
    return request.get('/tasks/cron/templates') as Promise<any[]>
  }
}
