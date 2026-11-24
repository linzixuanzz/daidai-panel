import type { PanelShapeStyle } from '@/utils/panelSettings'

export type CaptchaFailMode = 'open' | 'strict'

export interface SettingsConfigForm {
  max_concurrent_tasks: number
  log_retention_days: number
  max_log_content_size: number
  random_delay: string
  random_delay_extensions: string
  auto_install_deps: boolean
  // 系统命令行里单条命令的超时分钟数（服务端注册项 console_timeout_minutes，默认 30，区间 1-720）
  console_timeout_minutes: number
  // 下面两项 v3.2.9 从兜底区挪进「任务运行」卡（服务端注册项同名）。
  // 标题、说明、取值范围由卡片读服务端 schema，这里只是表单状态
  dependency_install_timeout_minutes: number
  detect_silent_exit: boolean
  // v3.3.6（#159）新增，直接放进「任务运行」卡：定时 / 手动任务主命令结束后清理留在后台的进程（服务端注册项同名，默认开）。
  // 标题、说明同样由卡片读服务端 schema，这里只是表单状态
  cleanup_leftover_processes: boolean
  auto_add_cron: boolean
  auto_del_cron: boolean
  default_cron_rule: string
  repo_file_extensions: string
  cpu_warn: number
  memory_warn: number
  disk_warn: number
  notify_on_resource_warn: boolean
  notify_panel_label: string
  notify_on_login: boolean
  proxy_url: string
  update_image_mirror: string
  binary_update_proxy: string
  // #177：只用于检查更新时访问 GitHub API（服务端 Secret 配置，密码框渲染）
  github_token: string
  auto_update_enabled: boolean
  trusted_proxy_cidrs: string
  captcha_enabled: boolean
  captcha_id: string
  captcha_key: string
  captcha_fail_mode: CaptchaFailMode | string
  panel_title: string
  timezone: string
  panel_icon: string
  editor_background_color: string
  log_background_color: string
  log_background_image: string
  // 界面圆角，v3.2.9 从兜底区挪进「面板外观」卡。服务端下发的其实是字符串，这里按已知枚举收窄
  // （与 PanelSettingsPayload 同一口径），认不出的历史值由 panelAppearance.ts 的 normalizePanelShape 兜住
  panel_shape_style: PanelShapeStyle
  // v4.0 主题引擎的两个全站键（服务端注册项同名，category branding，仅网页端生效），放在「面板外观」卡里编辑。
  // 背景效果：theme / plain / aurora / grid / image（认不出的历史值按字符串原样留着，见 resolveConfigOptions）
  panel_background_effect: string
  // 背景图片 data URL，背景效果为 image 时使用；'' = 没有
  panel_background_image: string
  // 其余 5 个 panel_* 键（风格、主色、玻璃模糊、密度、自定义 CSS）在主题工坊里编辑，
  // 不进这张表单，由 systemConfigSchema.ts 的 CONFIG_KEYS_RENDERED_ELSEWHERE 从兜底区排除
  backup_schedule_enabled: boolean
  backup_schedule_frequency: 'daily' | 'weekly' | 'monthly' | string
  backup_schedule_time: string
  backup_schedule_weekday: string
  backup_schedule_monthday: number
  backup_schedule_name: string
  backup_schedule_password: string
  backup_schedule_selection: string
  max_web_sessions: number
  max_app_sessions: number
}
