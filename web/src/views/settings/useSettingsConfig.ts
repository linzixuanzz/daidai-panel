import { computed, ref } from 'vue'
import { configApi, type SystemConfigMap } from '@/api/system'
import { ElMessage } from 'element-plus'
import { applyPanelAppearance, type PanelAppearanceSettings } from '@/utils/panelAppearance'
import type { PanelShapeStyle } from '@/utils/panelSettings'
import type { SettingsConfigForm } from './types'
import {
  buildConfigDraftWrites,
  CONFIG_KEYS_RENDERED_ELSEWHERE,
  groupSystemConfigItems,
  parseConfigBool,
  parseSystemConfigItems,
  validateConfigDraft,
  type ParsedSystemConfigItem
} from './systemConfigSchema'

const logBackgroundImageMaxBytes = 10 * 1024 * 1024
const logBackgroundImageTargetDataUrlBytes = 1600 * 1024
const logBackgroundImageMaxDimensions = [1920, 1600, 1280, 1024, 900, 768, 640]
const logBackgroundImageQualities = [0.82, 0.72, 0.62, 0.52, 0.42, 0.34, 0.28]

function getTextBytes(value: string) {
  return new Blob([value]).size
}

function readFileAsDataURL(file: File): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader()
    reader.onload = () => resolve(String(reader.result || ''))
    reader.onerror = () => reject(reader.error || new Error('read failed'))
    reader.readAsDataURL(file)
  })
}

function loadImage(dataUrl: string): Promise<HTMLImageElement> {
  return new Promise((resolve, reject) => {
    const image = new Image()
    image.onload = () => resolve(image)
    image.onerror = () => reject(new Error('image load failed'))
    image.src = dataUrl
  })
}

function canvasToDataURL(canvas: HTMLCanvasElement, mimeType: string, quality: number) {
  const dataUrl = canvas.toDataURL(mimeType, quality)
  if (mimeType === 'image/webp' && !dataUrl.startsWith('data:image/webp')) {
    return canvas.toDataURL('image/jpeg', quality)
  }
  return dataUrl
}

async function compressLogBackgroundImage(file: File) {
  const originalDataUrl = await readFileAsDataURL(file)
  if (getTextBytes(originalDataUrl) <= logBackgroundImageTargetDataUrlBytes) {
    return { dataUrl: originalDataUrl, compressed: false }
  }

  const image = await loadImage(originalDataUrl)
  const sourceWidth = image.naturalWidth || image.width
  const sourceHeight = image.naturalHeight || image.height
  if (!sourceWidth || !sourceHeight) {
    throw new Error('invalid image')
  }

  for (const maxDimension of logBackgroundImageMaxDimensions) {
    const scale = Math.min(1, maxDimension / Math.max(sourceWidth, sourceHeight))
    const width = Math.max(1, Math.round(sourceWidth * scale))
    const height = Math.max(1, Math.round(sourceHeight * scale))
    const canvas = document.createElement('canvas')
    canvas.width = width
    canvas.height = height

    const context = canvas.getContext('2d', { alpha: true })
    if (!context) {
      throw new Error('canvas unsupported')
    }
    context.clearRect(0, 0, width, height)
    context.drawImage(image, 0, 0, width, height)

    for (const quality of logBackgroundImageQualities) {
      const dataUrl = canvasToDataURL(canvas, 'image/webp', quality)
      const size = getTextBytes(dataUrl)
      if (size <= logBackgroundImageTargetDataUrlBytes) {
        return { dataUrl, compressed: true }
      }
    }
  }

  throw new Error('compressed image too large')
}

/**
 * 「MCP 服务」卡片（components/McpConfigCard.vue）的两个开关，
 * 对应服务端注册项 mcp_enabled / mcp_allow_mutations，默认都关。
 * 并进 configForm 之后，这两个键按差集规则自动从兜底区（ExtraConfigCard）消失。
 */
export interface McpConfigFields {
  mcp_enabled: boolean
  mcp_allow_mutations: boolean
}

/**
 * 「企业微信触发」卡片（components/WecomTriggerCard.vue）的两个开关（issue #145，v3.3.2），
 * 对应服务端注册项 wecom_trigger_enabled / wecom_trigger_allow_run，默认都关。
 *
 * 与 MCP 那两项是同一套形态：总开关关着时回调路由直接 403（连验签都不做），
 * 总开关开着但不允许触发执行时，企业微信那边配回调 URL 能配通、指令却不会真的跑任务——
 * 用来分两步上线。并进 configForm 之后，这两个键按差集规则自动从兜底区（ExtraConfigCard）消失。
 */
export interface WecomTriggerConfigFields {
  wecom_trigger_enabled: boolean
  wecom_trigger_allow_run: boolean
}

export type SettingsConfigFormState = SettingsConfigForm & McpConfigFields & WecomTriggerConfigFields

export function useSettingsConfig() {
  // 此开关原为功能上线门控，当前已全量启用（保留常量以兼容消费方）
  const captchaFeatureImplemented = true
  const configsLoading = ref(false)
  const configsSaving = ref(false)
  // 是否至少成功加载过一次配置。没加载成功时 rawConfigs 是空的、表单里全是本地默认值，
  // 这时保存会把每个键都当成改动写回去，连卡片上因为缺 schema 没显示的项也会被默认值盖掉，所以 submitConfigs 直接拦下。
  // 加载成功过之后再刷新失败不清零：rawConfigs 和表单都还是上一次成功加载的那份，彼此对得上
  const configsLoaded = ref(false)

  const configForm = ref<SettingsConfigFormState>({
    max_concurrent_tasks: 5,
    log_retention_days: 7,
    max_log_content_size: 102400000,
    random_delay: '',
    random_delay_extensions: '',
    auto_install_deps: true,
    console_timeout_minutes: 30,
    dependency_install_timeout_minutes: 20,
    detect_silent_exit: true,
    cleanup_leftover_processes: true,
    auto_add_cron: true,
    auto_del_cron: true,
    default_cron_rule: '',
    repo_file_extensions: '',
    cpu_warn: 80,
    memory_warn: 80,
    disk_warn: 90,
    notify_on_resource_warn: false,
    notify_panel_label: '',
    notify_on_login: false,
    proxy_url: '',
    update_image_mirror: '',
    binary_update_proxy: '',
    github_token: '',
    auto_update_enabled: false,
    trusted_proxy_cidrs: '',
    captcha_enabled: false,
    captcha_id: '',
    captcha_key: '',
    captcha_fail_mode: 'open',
    panel_title: '',
    timezone: 'Asia/Shanghai',
    panel_icon: '',
    editor_background_color: '',
    log_background_color: '',
    log_background_image: '',
    panel_shape_style: 'square',
    backup_schedule_enabled: false,
    backup_schedule_frequency: 'daily',
    backup_schedule_time: '03:00',
    backup_schedule_weekday: '1',
    backup_schedule_monthday: 1,
    backup_schedule_name: '',
    backup_schedule_password: '',
    backup_schedule_selection: 'configs,tasks,subscriptions,env_vars,logs,scripts,dependencies,task_views',
    max_web_sessions: 1,
    max_app_sessions: 1,
    mcp_enabled: false,
    mcp_allow_mutations: false,
    wecom_trigger_enabled: false,
    wecom_trigger_allow_run: false
  })

  // 服务端下发的原始 schema + 当前值。两个用途：
  // 1) 兜底区（ExtraConfigCard）按 schema 渲染本页没有专属表单的配置项；
  // 2) 所有保存按钮拿它当基准，只回写真正改动过的键。
  const rawConfigs = ref<SystemConfigMap>({})

  // 兜底区的表单草稿：key -> 原始字符串。
  // 控件全部按字符串读写，因为服务端 PUT /configs/batch 绑定的是 map[string]string，
  // 连 bool 的值也是字符串 "true"/"false" 而不是 JSON 布尔。
  const extraConfigDraft = ref<Record<string, string>>({})

  // 兜底区要渲染的项 = 服务端注册项 − 本页硬编码表单已覆盖的键 − 已在别的页面渲染的键。
  //
  // 「已覆盖」直接取 configForm 的键而不是另写一张名单：
  // 往 configForm 里加键就自动从兜底区消失，往服务端注册表里加项而 Web 忘了加表单，
  // 就会自动出现在兜底区。这样两边都不需要人手同步。
  const extraConfigItems = computed(() => {
    const covered = new Set<string>([
      ...Object.keys(configForm.value),
      ...Object.keys(CONFIG_KEYS_RENDERED_ELSEWHERE)
    ])
    return parseSystemConfigItems(rawConfigs.value, covered)
  })

  const extraConfigGroups = computed(() => groupSystemConfigItems(extraConfigItems.value))

  // 全部注册项的 schema，按 key 索引。
  // 专属表单里有几项是从兜底区挪进来的（依赖安装超时、检测脚本半路静默结束、界面圆角），
  // 它们的标题、说明、下拉选项、取值范围照旧只认服务端下发的这一份，卡片从这里取，不在 Web 另抄一遍。
  const configSchema = computed(() => {
    const index: Record<string, ParsedSystemConfigItem> = {}
    for (const item of parseSystemConfigItems(rawConfigs.value, new Set())) {
      index[item.key] = item
    }
    return index
  })

  // 「代理设置」页静默更新开关下方只读展示的「上次检查更新时间」。
  // 它由巡检与手动检查更新写入，不是用户偏好，所以不进 configForm、也永不回写，直接读服务端下发的值；
  // 切到「代理设置」标签时 handleTabChange 会重跑 loadSystemConfigs，这里跟着刷新
  const autoUpdateLastCheckedAt = computed(() =>
    String(rawConfigs.value.auto_update_last_checked_at?.value ?? '').trim()
  )

  function readConfigString(cfgs: Record<string, any>, key: string, fallback = ''): string {
    const entry = cfgs[key]
    const raw = entry?.value ?? entry?.default_value ?? fallback
    if (raw === null || raw === undefined) return fallback
    return String(raw)
  }

  function readConfigNumber(cfgs: Record<string, any>, key: string, fallback: number): number {
    const raw = readConfigString(cfgs, key, String(fallback))
    // Number('') 是 0 而不是 NaN：空串不先拦下就读成 0，低于数字项的下限，整张卡保存时会被范围校验拦住
    if (!raw.trim()) return fallback
    const parsed = Number(raw)
    return Number.isFinite(parsed) ? parsed : fallback
  }

  function readConfigBool(cfgs: Record<string, any>, key: string, fallback: boolean): boolean {
    const raw = readConfigString(cfgs, key, fallback ? 'true' : 'false').trim().toLowerCase()
    if (['true', '1', 'yes', 'on'].includes(raw)) return true
    if (['false', '0', 'no', 'off'].includes(raw)) return false
    return fallback
  }

  // 本页交给 applyPanelAppearance 的外观设置，两点讲究：
  // 1) 界面圆角只取已保存的值（rawConfigs；保存成功后 submitConfigs 已把新值记进去），不取下拉里还没保存的选择。
  //    圆角刻意不做选中即预览，可取色、上传/移除背景图会即时预览，带上表单值的话没保存的圆角会被顺手应用并写进本机缓存；
  // 2) 传快照而不是 configForm 本身：applyPanelAppearance 会把它留给切主题时复用，
  //    留的若是响应式表单，在下拉里改了圆角还没保存，一切主题就生效了。
  // 取空串时 applyPanelShapeStyle 认不出、沿用上一次的圆角，不会被拍回直角
  function appearanceSnapshot(): PanelAppearanceSettings {
    return {
      ...configForm.value,
      panel_shape_style: readConfigString(rawConfigs.value, 'panel_shape_style', '') as PanelShapeStyle
    }
  }

  async function loadSystemConfigs() {
    configsLoading.value = true
    try {
      const res = await configApi.list()
      const cfgs: SystemConfigMap = res.data || {}
      rawConfigs.value = cfgs

      configForm.value = {
        max_concurrent_tasks: readConfigNumber(cfgs, 'max_concurrent_tasks', 5),
        log_retention_days: readConfigNumber(cfgs, 'log_retention_days', 7),
        max_log_content_size: readConfigNumber(cfgs, 'max_log_content_size', 102400000),
        random_delay: readConfigString(cfgs, 'random_delay', ''),
        random_delay_extensions: readConfigString(cfgs, 'random_delay_extensions', ''),
        auto_install_deps: readConfigBool(cfgs, 'auto_install_deps', true),
        console_timeout_minutes: readConfigNumber(cfgs, 'console_timeout_minutes', 30),
        dependency_install_timeout_minutes: readConfigNumber(cfgs, 'dependency_install_timeout_minutes', 20),
        detect_silent_exit: readConfigBool(cfgs, 'detect_silent_exit', true),
        // 兜底值与服务端注册默认值一致（开），读不到时不会把它误显示成关
        cleanup_leftover_processes: readConfigBool(cfgs, 'cleanup_leftover_processes', true),
        auto_add_cron: readConfigBool(cfgs, 'auto_add_cron', true),
        auto_del_cron: readConfigBool(cfgs, 'auto_del_cron', true),
        default_cron_rule: readConfigString(cfgs, 'default_cron_rule', ''),
        repo_file_extensions: readConfigString(cfgs, 'repo_file_extensions', ''),
        cpu_warn: readConfigNumber(cfgs, 'cpu_warn', 80),
        memory_warn: readConfigNumber(cfgs, 'memory_warn', 80),
        disk_warn: readConfigNumber(cfgs, 'disk_warn', 90),
        notify_on_resource_warn: readConfigBool(cfgs, 'notify_on_resource_warn', false),
        notify_panel_label: readConfigString(cfgs, 'notify_panel_label', ''),
        notify_on_login: readConfigBool(cfgs, 'notify_on_login', false),
        proxy_url: readConfigString(cfgs, 'proxy_url', ''),
        update_image_mirror: readConfigString(cfgs, 'update_image_mirror', ''),
        binary_update_proxy: readConfigString(cfgs, 'binary_update_proxy', ''),
        github_token: readConfigString(cfgs, 'github_token', ''),
        auto_update_enabled: readConfigBool(cfgs, 'auto_update_enabled', false),
        trusted_proxy_cidrs: readConfigString(cfgs, 'trusted_proxy_cidrs', ''),
        captcha_enabled: readConfigBool(cfgs, 'captcha_enabled', false),
        captcha_id: readConfigString(cfgs, 'captcha_id', ''),
        captcha_key: readConfigString(cfgs, 'captcha_key', ''),
        captcha_fail_mode: readConfigString(cfgs, 'captcha_fail_mode', 'open'),
        panel_title: readConfigString(cfgs, 'panel_title', ''),
        timezone: readConfigString(cfgs, 'timezone', 'Asia/Shanghai'),
        panel_icon: readConfigString(cfgs, 'panel_icon', ''),
        editor_background_color: readConfigString(cfgs, 'editor_background_color', ''),
        log_background_color: readConfigString(cfgs, 'log_background_color', ''),
        log_background_image: readConfigString(cfgs, 'log_background_image', ''),
        // 按字符串原样读：认不出的历史值要留在下拉里给用户看到（见 resolveConfigOptions），
        // 应用到页面时由 normalizePanelShape 忽略，不会被拍回直角
        panel_shape_style: readConfigString(cfgs, 'panel_shape_style', 'square') as PanelShapeStyle,
        backup_schedule_enabled: readConfigBool(cfgs, 'backup_schedule_enabled', false),
        backup_schedule_frequency: readConfigString(cfgs, 'backup_schedule_frequency', 'daily'),
        backup_schedule_time: readConfigString(cfgs, 'backup_schedule_time', '03:00'),
        backup_schedule_weekday: readConfigString(cfgs, 'backup_schedule_weekday', '1'),
        backup_schedule_monthday: readConfigNumber(cfgs, 'backup_schedule_monthday', 1),
        backup_schedule_name: readConfigString(cfgs, 'backup_schedule_name', ''),
        backup_schedule_password: readConfigString(cfgs, 'backup_schedule_password', ''),
        backup_schedule_selection: readConfigString(cfgs, 'backup_schedule_selection', 'configs,tasks,subscriptions,env_vars,logs,scripts,dependencies,task_views'),
        max_web_sessions: readConfigNumber(cfgs, 'max_web_sessions', 1),
        max_app_sessions: readConfigNumber(cfgs, 'max_app_sessions', 1),
        mcp_enabled: readConfigBool(cfgs, 'mcp_enabled', false),
        mcp_allow_mutations: readConfigBool(cfgs, 'mcp_allow_mutations', false),
        wecom_trigger_enabled: readConfigBool(cfgs, 'wecom_trigger_enabled', false),
        wecom_trigger_allow_run: readConfigBool(cfgs, 'wecom_trigger_allow_run', false)
      }

      // 兜底区草稿整体重建，以服务端当前值为准（丢弃上一次未保存的编辑）。
      // 与本页其它表单「重新加载即回到服务端状态」的行为一致。
      //
      // bool 收敛成 "true"/"false" 两个字面量：开关控件按这两个字符串绑值，
      // 服务端 strconv.FormatBool 出来的也正是这两个，历史脏值（"1"/"on"）在这里被拉平。
      const draft: Record<string, string> = {}
      for (const item of extraConfigItems.value) {
        draft[item.key] = item.valueType === 'bool'
          ? (parseConfigBool(item.value) ? 'true' : 'false')
          : item.value
      }
      extraConfigDraft.value = draft
      configsLoaded.value = true

      applyPanelAppearance(appearanceSnapshot())
    } catch (err: any) {
      ElMessage.error(err?.response?.data?.error || '加载配置失败')
    } finally {
      configsLoading.value = false
    }
  }

  // 本页所有保存按钮的共同出口：只回写「与服务端当前值不同」的键。
  //
  // 服务端 BatchSet 是逐键 SetConfig、中途失败直接 400 返回而前面的键已经落库，
  // 全量回写会把「这一组里有一项填错」放大成「一半保存了一半没保存」。
  // 只发改动项能把出错范围缩到用户真正动过的那几个键上。
  async function submitConfigs(next: Record<string, string>): Promise<boolean> {
    if (!configsLoaded.value) {
      ElMessage.warning(configsLoading.value ? '配置还在加载，请稍后再保存' : '配置还没加载成功，请点刷新后再保存')
      return false
    }

    const changed: Record<string, string> = {}
    for (const [key, value] of Object.entries(next)) {
      if (rawConfigs.value[key]?.value === value) continue
      changed[key] = value
    }

    if (Object.keys(changed).length === 0) {
      // 一个改动都没有时不发请求：服务端的 configs 是 binding:"required"，空对象会直接 400
      ElMessage.info('配置没有变化')
      return false
    }

    configsSaving.value = true
    try {
      await configApi.batchSet(changed)
      // 更新比较基准，避免同一份改动下次保存时被重复写一遍。
      // 服务端 normalize 可能把值改写（例如二进制加速源会补上尾部斜杠），这里只记我们发出去的值：
      // 真有差异时下次保存会再写一次，写入是幂等的，没有副作用。
      for (const [key, value] of Object.entries(changed)) {
        const entry = rawConfigs.value[key]
        if (entry) entry.value = value
        else rawConfigs.value[key] = { value }
      }
      ElMessage.success('配置已保存')
      return true
    } catch (err: any) {
      ElMessage.error(err?.response?.data?.error || '保存失败')
      // 保存失败后从后端重新拉取该组的真实值，避免陈旧本地状态被下一次保存再次带出
      void loadSystemConfigs()
      return false
    } finally {
      configsSaving.value = false
    }
  }

  async function saveConfigKeys(keys: string[]) {
    const next: Record<string, string> = {}
    for (const key of keys) {
      const val = (configForm.value as any)[key]
      next[key] = typeof val === 'boolean' ? (val ? 'true' : 'false') : String(val ?? '')
    }
    if (await submitConfigs(next)) {
      applyPanelAppearance(appearanceSnapshot())
    }
  }

  // 界面圆角随本卡保存：保存成功后 submitConfigs 已把新值记进 rawConfigs，
  // saveConfigKeys 重跑 applyPanelAppearance 时快照里就是刚保存的圆角，当场生效、不用刷新
  function handleSaveSystemConfig() {
    void saveConfigKeys([
      'panel_title', 'timezone', 'panel_icon', 'editor_background_color', 'log_background_color', 'log_background_image',
      'panel_shape_style'
    ])
  }

  function handleSaveAlertConfig() {
    void saveConfigKeys([
      'cpu_warn', 'memory_warn', 'disk_warn', 'notify_on_resource_warn', 'notify_panel_label', 'notify_on_login'
    ])
  }

  function handleIconUpload(file: File) {
    if (!file.name.endsWith('.svg')) {
      ElMessage.warning('仅支持 SVG 格式图标')
      return false
    }
    if (file.size > 100 * 1024) {
      ElMessage.warning('图标文件不能超过 100KB')
      return false
    }
    const reader = new FileReader()
    reader.onload = (e) => {
      configForm.value.panel_icon = e.target?.result as string
    }
    reader.readAsDataURL(file)
    return false
  }

  function handleLogBackgroundUpload(file: File) {
    if (!file.type.startsWith('image/')) {
      ElMessage.warning('仅支持图片格式背景')
      return false
    }
    if (file.size > logBackgroundImageMaxBytes) {
      ElMessage.warning('背景图片不能超过 10MB')
      return false
    }

    void (async () => {
      try {
        const result = await compressLogBackgroundImage(file)
        configForm.value.log_background_image = result.dataUrl
        applyPanelAppearance(appearanceSnapshot())
        if (result.compressed) {
          ElMessage.success('背景图片已自动压缩，请保存配置后生效')
        }
      } catch {
        ElMessage.error('背景图片压缩失败，请换一张尺寸更小的图片')
      }
    })()
    return false
  }

  function previewPanelAppearance() {
    applyPanelAppearance(appearanceSnapshot())
  }

  function handleSaveTaskConfig() {
    // 依赖安装超时是从兜底区挪过来的，兜底区保存前本来会按 schema 的 min/max 拦一道；
    // 挪了位置不能把这道校验丢掉 —— 越界时服务端 400 的原文带的是英文键名，用户看不懂
    const installTimeoutItem = configSchema.value.dependency_install_timeout_minutes
    if (installTimeoutItem) {
      // v-model.number 在输入框清空或填了非数字时会留下字符串，统一转成字符串交给 schema 校验
      const message = validateConfigDraft(installTimeoutItem, String(configForm.value.dependency_install_timeout_minutes))
      if (message) {
        ElMessage.warning(message)
        return
      }
    }
    void saveConfigKeys([
      'max_concurrent_tasks', 'log_retention_days',
      'max_log_content_size', 'random_delay', 'random_delay_extensions', 'auto_install_deps',
      'console_timeout_minutes', 'dependency_install_timeout_minutes', 'detect_silent_exit',
      'cleanup_leftover_processes'
    ])
  }

  function handleSaveProxy() {
    void saveConfigKeys(['proxy_url', 'update_image_mirror', 'binary_update_proxy', 'github_token', 'auto_update_enabled', 'trusted_proxy_cidrs'])
  }

  function handleSaveCaptcha() {
    void saveConfigKeys(['captcha_enabled', 'captcha_id', 'captcha_key', 'captcha_fail_mode'])
  }

  function handleSaveSessionConfig() {
    void saveConfigKeys(['max_web_sessions', 'max_app_sessions'])
  }

  function handleSaveMcpConfig() {
    void saveConfigKeys(['mcp_enabled', 'mcp_allow_mutations'])
  }

  function handleSaveWecomTriggerConfig() {
    void saveConfigKeys(['wecom_trigger_enabled', 'wecom_trigger_allow_run'])
  }

  function handleSaveBackupSchedule(selectionCSV?: string) {
    const normalizedSelection = selectionCSV?.trim() || configForm.value.backup_schedule_selection
    configForm.value.backup_schedule_selection = normalizedSelection
    void saveConfigKeys([
      'backup_schedule_enabled',
      'backup_schedule_frequency',
      'backup_schedule_time',
      'backup_schedule_weekday',
      'backup_schedule_monthday',
      'backup_schedule_name',
      'backup_schedule_password',
      'backup_schedule_selection'
    ])
  }

  // 兜底区保存：先做前端能确定的整数校验，再把草稿交给统一出口做差集回写。
  // 保存后不用补 applyPanelAppearance / applyPanelShapeStyle：界面圆角 v3.2.9 已挪进「面板外观」卡、
  // 随 saveConfigKeys 生效，兜底区里没有要当场应用到页面上的项
  async function handleSaveExtraConfigs() {
    for (const item of extraConfigItems.value) {
      const message = validateConfigDraft(item, extraConfigDraft.value[item.key] ?? '')
      if (message) {
        ElMessage.warning(message)
        return
      }
    }
    await submitConfigs(buildConfigDraftWrites(extraConfigItems.value, extraConfigDraft.value))
  }

  return {
    captchaFeatureImplemented,
    configsLoading,
    configsSaving,
    configForm,
    configSchema,
    autoUpdateLastCheckedAt,
    extraConfigGroups,
    extraConfigDraft,
    handleSaveExtraConfigs,
    loadSystemConfigs,
    handleSaveSystemConfig,
    handleSaveAlertConfig,
    handleIconUpload,
    handleLogBackgroundUpload,
    previewPanelAppearance,
    handleSaveTaskConfig,
    handleSaveProxy,
    handleSaveCaptcha,
    handleSaveSessionConfig,
    handleSaveMcpConfig,
    handleSaveWecomTriggerConfig,
    handleSaveBackupSchedule
  }
}
