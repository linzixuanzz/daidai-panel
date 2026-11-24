// 设置页的「服务端 schema -> 可渲染表单项」解析，纯数据变换、不依赖 Vue。
//
// ── 这个文件解决什么问题 ────────────────────────────────────────────────
// GET /api/configs 早就下发了完整的渲染 schema（真源
// server/model/system_config_registry.go，47 项），但设置页的表单形状一直是
// useSettingsConfig.ts 里硬编码的 41 个键。差集实测导致
// panel_runtime_mode / panel_service_manager / panel_service_name 三项
// 在 Web 端完全没有 UI —— APP 能改的东西，Web 上改不了。
//
// ── 这个文件刻意不做什么 ────────────────────────────────────────────────
// 它不是「把设置页整体换成 schema 驱动」。设置页里的配置项大量绑着定制控件：
// SVG 上传预览、取色器实时预览、日志背景图前端压缩、镜像源选择弹窗、
// 备份内容的 CSV <-> 复选框映射、按备份频率联动显示的星期/日期。
// 通用渲染器做不出这些，硬换等于重画一遍设置页。
//
// 所以这里只做兜底：把 schema 里有、而本页没有专属表单的配置项渲染出来，
// 让「服务端加了一项配置、Web 忘了加表单」不再等于「Web 上永远改不了」。
// 谁被兜底不靠另写一张表维护，而是拿 configForm 的键去减，见 useSettingsConfig.ts。

import type { SystemConfigItemResponse, SystemConfigMap, SystemConfigOption } from '@/api/system'

/**
 * 渲染用的值类型。
 *
 * 服务端将来加了第五种类型时，这里降级成 'string' 用普通输入框渲染，
 * **绝不隐藏字段** —— 隐藏等于用户在 Web 上永远改不了它，正是本次要消灭的问题。
 */
export type ConfigValueType = 'string' | 'int' | 'bool' | 'enum'

export interface ParsedSystemConfigItem {
  key: string
  /** 服务端当前值（服务端已在空值时替换成 default_value，所以这就是面板实际会用的值） */
  value: string
  defaultValue: string
  valueType: ConfigValueType
  group: string
  groupLabel: string
  /** 输入框标题 */
  label: string
  /** 完整说明，渲染在输入框下方 */
  description: string
  order: number
  /** 用密码框渲染 */
  secret: boolean
  min?: number
  max?: number
  options: SystemConfigOption[]
  /** 见 READ_ONLY_CONFIG_KEYS */
  readOnly: boolean
  readOnlyReason?: string
  /** 见 CONFIG_RISK_NOTES */
  riskNote?: string
}

export interface ParsedSystemConfigGroup {
  group: string
  label: string
  items: ParsedSystemConfigItem[]
}

/**
 * 已经在 Web 的**别的地方**有编辑/展示入口的配置项，兜底区不再重复渲染一份。
 *
 * 值写的是那个入口所在的文件，改那些页面时顺手核对这里。
 * 这张表是「本页之外的 Web 布局事实」，不是配置定义的副本 ——
 * 配置有哪些项、长什么样，仍然只有服务端说了算。
 */
export const CONFIG_KEYS_RENDERED_ELSEWHERE: Record<string, string> = {
  // 依赖管理页的 Python 版本下拉 +「设为默认」按钮（经 depsApi 写入同一个键）
  python_default_version: 'web/src/views/deps/index.vue',
  // 订阅页的「订阅设置」弹窗
  subscription_force_overwrite: 'web/src/views/subscriptions/index.vue',
  // 「代理设置」页静默更新开关下方的「上次检查更新时间」只读展示；
  // 值由静默更新巡检与概览页手动检查更新写入，不是用户偏好
  auto_update_last_checked_at: 'web/src/views/settings/components/ProxyConfigCard.vue',
  // v4.0 主题工坊（管理员区）：风格 / 主色 / 玻璃模糊 / 密度经「设为全站默认」写入，自定义 CSS 有专门的编辑器与片段。
  // 兜底区只能渲染成单行输入框（自定义 CSS 最长 32KB），所以不在这里重复一份
  panel_theme_preset: 'web/src/components/ThemeStudio.vue',
  panel_accent_color: 'web/src/components/ThemeStudio.vue',
  panel_glass_blur: 'web/src/components/ThemeStudio.vue',
  panel_density: 'web/src/components/ThemeStudio.vue',
  panel_custom_css: 'web/src/components/ThemeStudio.vue'
}

/**
 * 注册了、但**不该让用户在面板里改**的键：渲染成只读行，值照常显示。
 *
 * 渲染成只读而不是直接隐藏：隐藏会让「为什么我的更新失败」无从排查。
 */
export const READ_ONLY_CONFIG_KEYS: Record<string, string> = {
  auto_update_last_checked_at: '由静默更新巡检自动写入，不是用户偏好。',
  panel_service_manager:
    '由服务器上执行 `ddp service install` 时写入，是安装事实而不是偏好。改错会让面板更新后拉不起来，而正因为拉不起来，也就没法再从面板改回去。',
  panel_service_name:
    '同上：systemd 服务名由安装命令写入。要改请在服务器上重新执行 `ddp service install`。'
}

/**
 * 可改、但改错代价明显高于其它项的键，渲染时附一行提醒。
 *
 * 只影响提示文案，不影响是否渲染、不影响取值、不影响回写。
 * 键从服务端注册表消失时，这里的条目自然查不到，不会有残留影响。
 */
export const CONFIG_RISK_NOTES: Record<string, string> = {
  panel_runtime_mode: '改成「仅写文件」后 docker logs 将看不到面板日志，请改用「面板日志」标签页查看。'
}

/** 没有 order 时的哨兵值，保证有 order 的项永远排在前面 */
const NO_ORDER = Number.MAX_SAFE_INTEGER

function parseValueType(raw: unknown): ConfigValueType {
  switch (String(raw ?? '').trim().toLowerCase()) {
    case 'int':
      return 'int'
    case 'bool':
      return 'bool'
    case 'enum':
      return 'enum'
    default:
      return 'string'
  }
}

function parseOptions(raw: SystemConfigOption[] | undefined): SystemConfigOption[] {
  if (!Array.isArray(raw)) return []
  return raw.map((option) => {
    const value = String(option?.value ?? '')
    const label = String(option?.label ?? '').trim()
    // label 缺失时退回显示 value，至少让下拉项可辨认
    return { value, label: label || value }
  })
}

function parseItem(key: string, raw: SystemConfigItemResponse): ParsedSystemConfigItem {
  const group = String(raw.group ?? '').trim()
  const options = parseOptions(raw.options)
  let valueType = parseValueType(raw.value_type)
  // enum 却没给 options（理论上不会发生，但发生了不能渲染出一个点不开的下拉框）
  if (valueType === 'enum' && options.length === 0) {
    valueType = 'string'
  }

  const readOnlyReason = READ_ONLY_CONFIG_KEYS[key]
  return {
    key,
    value: String(raw.value ?? ''),
    defaultValue: String(raw.default_value ?? ''),
    valueType,
    group,
    groupLabel: String(raw.group_label ?? '').trim() || group || '其它',
    label: String(raw.label ?? '').trim() || key,
    description: String(raw.description ?? '').trim(),
    order: typeof raw.order === 'number' ? raw.order : NO_ORDER,
    secret: raw.secret === true,
    min: typeof raw.min === 'number' ? raw.min : undefined,
    max: typeof raw.max === 'number' ? raw.max : undefined,
    options,
    readOnly: readOnlyReason !== undefined,
    readOnlyReason,
    riskNote: CONFIG_RISK_NOTES[key]
  }
}

/**
 * 解析 GET /api/configs 的 data，排除掉 excludedKeys，按 order 排序。
 *
 * 只收 registered === true 的项：其余是面板运行时自己写的临时状态
 * （auto_update_pending_version 之类），没有 schema，渲染出来没有意义，
 * 而且**渲染了就有被回写覆盖的风险**。
 */
export function parseSystemConfigItems(
  configs: SystemConfigMap,
  excludedKeys: Set<string>
): ParsedSystemConfigItem[] {
  const items: ParsedSystemConfigItem[] = []
  for (const [key, raw] of Object.entries(configs)) {
    if (!raw || raw.registered !== true) continue
    if (excludedKeys.has(key)) continue
    items.push(parseItem(key, raw))
  }
  // order 相同（都缺 order）时按 key 兜底，保证顺序稳定不抖动
  return items.sort((a, b) => (a.order - b.order) || a.key.localeCompare(b.key))
}

/** 按 group 分桶，组内保持已排好的顺序，组间按组内最小 order 排 */
export function groupSystemConfigItems(items: ParsedSystemConfigItem[]): ParsedSystemConfigGroup[] {
  const buckets = new Map<string, ParsedSystemConfigItem[]>()
  for (const item of items) {
    const bucket = buckets.get(item.group)
    if (bucket) bucket.push(item)
    else buckets.set(item.group, [item])
  }

  const groups: ParsedSystemConfigGroup[] = []
  for (const [group, groupItems] of buckets) {
    const first = groupItems[0]
    if (!first) continue
    groups.push({ group, label: first.groupLabel, items: groupItems })
  }

  return groups.sort((a, b) => {
    const rankA = Math.min(...a.items.map((item) => item.order))
    const rankB = Math.min(...b.items.map((item) => item.order))
    return (rankA - rankB) || a.group.localeCompare(b.group)
  })
}

/**
 * 按服务端 parseBoolString（server/model/system_config_registry.go）的语义读字符串布尔值：
 * 1/true/yes/on 为真，其余（含无法识别的值）为假 —— 服务端对无法识别的值也是取 false，
 * 这里跟着取，保证 Web 显示的就是面板实际会做的事。
 */
export function parseConfigBool(raw: string | undefined): boolean {
  return ['1', 'true', 'yes', 'on'].includes(String(raw ?? '').trim().toLowerCase())
}

/**
 * 下拉要渲染的选项。
 *
 * 当前值不在服务端给的 options 里（服务端改了枚举、库里有历史脏值）时**必须**把它补进去，
 * 否则用户点开下拉看不到自己当前的值，随手选一个就再也回不去了。
 */
export function resolveConfigOptions(
  item: ParsedSystemConfigItem,
  currentValue: string | undefined
): SystemConfigOption[] {
  const value = String(currentValue ?? '')
  if (!value || item.options.some((option) => option.value === value)) {
    return item.options
  }
  return [{ value, label: `${value}（面板未声明）` }, ...item.options]
}

/**
 * 整数项的取值区间提示，服务端给了 min/max 才有，其余返回空串。
 *
 * 兜底区与「任务运行」卡里从兜底区挪过去的整数项共用，保证两处说法一致。
 */
export function formatConfigRangeHint(item: ParsedSystemConfigItem): string {
  if (item.valueType !== 'int') return ''
  if (item.min !== undefined && item.max !== undefined) return `取值范围 ${item.min} - ${item.max}`
  if (item.min !== undefined) return `不能小于 ${item.min}`
  if (item.max !== undefined) return `不能大于 ${item.max}`
  return ''
}

/**
 * 单项前端校验，返回 null 表示通过，否则是给用户看的中文原因。
 *
 * 只拦「服务端一定会拒、而且拒绝理由用户看不懂」的两件事：整数类型和取值范围。
 * 其余（代理地址格式、cron 合法性、时区名……）交给服务端 normalize，
 * 400 的原文比 Web 自己编一套规则准确，也不会随服务端升级而过期。
 */
export function validateConfigDraft(item: ParsedSystemConfigItem, draft: string): string | null {
  if (item.readOnly || item.valueType !== 'int') return null

  const text = String(draft ?? '').trim()
  // 服务端的 int normalize 在空串时返回 default_value，是合法的「恢复默认」
  if (!text) return null

  if (!/^-?\d+$/.test(text)) {
    return `「${item.label}」需要填整数`
  }

  const parsed = Number(text)
  if (item.min !== undefined && item.max !== undefined) {
    if (parsed < item.min || parsed > item.max) {
      return `「${item.label}」需在 ${item.min}-${item.max} 之间`
    }
    return null
  }
  if (item.min !== undefined && parsed < item.min) {
    return `「${item.label}」不能小于 ${item.min}`
  }
  if (item.max !== undefined && parsed > item.max) {
    return `「${item.label}」不能大于 ${item.max}`
  }
  return null
}

/**
 * 表单草稿 -> 待回写的键值对（全是字符串）。
 *
 * 两条硬规则：
 * 1. 只读项永不回写（见 READ_ONLY_CONFIG_KEYS）。
 * 2. bool 一律收敛成 "true"/"false" 两个**字符串**：服务端 BatchSet 绑定的是
 *    map[string]string，混进 JSON 布尔会让整份 ShouldBindJSON 失败。
 *
 * 「只回写改动过的键」不在这里做，统一由 useSettingsConfig 的 submitConfigs
 * 拿服务端当前值做差集，保证本页所有保存按钮走同一条规则。
 */
export function buildConfigDraftWrites(
  items: ParsedSystemConfigItem[],
  draft: Record<string, string>
): Record<string, string> {
  const writes: Record<string, string> = {}
  for (const item of items) {
    if (item.readOnly) continue
    const raw = draft[item.key]
    if (raw === undefined) continue
    writes[item.key] = item.valueType === 'bool'
      ? (parseConfigBool(raw) ? 'true' : 'false')
      : String(raw).trim()
  }
  return writes
}
