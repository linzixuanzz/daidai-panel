import request from './request'

/** 「后台进程」页（#176）的一行，字段与后端 service.PanelProcess 一致 */
export interface PanelProcess {
  pid: number
  pgid: number
  /** 不是任务拉起的进程（只出现在「显示全部进程」里）为 null */
  task_id: number | null
  /** 任务已删除时为空串 */
  task_name: string
  cmdline: string
  started_at: string | null
  rss_bytes: number
  /** running_with_task：随任务运行中；detached：已脱离任务；other：不是任务拉起的 */
  state: 'running_with_task' | 'detached' | 'other'
  /** 结束进程时原样带回，后端用它核对 PID 没被复用 */
  start_ticks: number
}

export interface PanelProcessListResponse {
  data: PanelProcess[]
  /** 非 Linux 部署为 false，页面显示「仅 Linux 部署支持」 */
  supported: boolean
  /** Magisk 模块版为 false，页面隐藏「显示全部进程」开关 */
  all_supported: boolean
  scope: 'task' | 'all'
}

export const processApi = {
  list(scope: 'task' | 'all' = 'task') {
    return request.get('/system/processes', { params: { scope } }) as Promise<PanelProcessListResponse>
  },
  kill(pid: number, startTicks: number) {
    return request.post('/system/processes/kill', { pid, start_ticks: startTicks }) as Promise<{ message: string; exited: boolean }>
  },
}
