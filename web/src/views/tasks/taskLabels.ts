const SUBSCRIPTION_LABEL_PREFIX = 'subscription:'
const SUBSCRIPTION_DISPLAY_LABEL = '订阅任务'
const TASK_GROUP_LABEL_PREFIX = '分组:'

function uniqueLabels(labels: string[]) {
  return Array.from(new Set(labels.filter(Boolean)))
}

export function isInternalTaskLabel(label: string) {
  return label.startsWith(SUBSCRIPTION_LABEL_PREFIX) || label.startsWith(TASK_GROUP_LABEL_PREFIX)
}

export function getTaskGroupName(labels: string[] = []) {
  for (const label of labels) {
    if (!label) continue
    if (label.startsWith(TASK_GROUP_LABEL_PREFIX)) {
      const group = label.slice(TASK_GROUP_LABEL_PREFIX.length).trim()
      if (group) return group
    }
  }
  return ''
}

export function toTaskGroupLabel(groupName: string) {
  const normalized = groupName.trim()
  return normalized ? `${TASK_GROUP_LABEL_PREFIX}${normalized}` : ''
}

export function getDisplayTaskLabels(labels: string[] = []) {
  const displayLabels: string[] = []
  let hasSubscriptionLabel = false
  const groupName = getTaskGroupName(labels)

  for (const label of labels) {
    if (!label) continue
    if (label.startsWith(SUBSCRIPTION_LABEL_PREFIX)) {
      hasSubscriptionLabel = true
      continue
    }
    if (label.startsWith(TASK_GROUP_LABEL_PREFIX)) {
      continue
    }
    displayLabels.push(label)
  }

  if (groupName) {
    displayLabels.unshift(groupName)
  }

  if (hasSubscriptionLabel) {
    displayLabels.push(SUBSCRIPTION_DISPLAY_LABEL)
  }

  return uniqueLabels(displayLabels)
}

export type DisplayTaskLabelKind = 'group' | 'subscription' | 'custom'

export interface DisplayTaskLabelEntry {
  /** 展示文案，与 display_labels 里那一项逐字相同 */
  label: string
  /** 该项属于哪一类，决定「显示设置」里哪个开关管它，以及渲染成哪种配色 */
  kind: DisplayTaskLabelKind
  /** v-for 的 :key。带下标，保证同名两条不会撞 key */
  key: string
}

/**
 * 把后端下发的 display_labels 标注成「分组 / 订阅 / 自定义」三类，供列表页的标签分项隐藏使用。
 *
 * 返回值与 display_labels 【下标一一对齐】（空字符串项会被跳过），而不是切成三个桶：
 * 桶的做法把「类别」寄生在「字符串值」上，同名即同类 —— 订阅名等于分组名时，
 * 订阅那条必然被分组桶吞掉、订阅桶恒空，两个开关就串台成一个（issue #109-3）。
 *
 * display_labels 是一个扁平字符串数组、自身不带任何标记，前端只能靠两条外部证据反推：
 *   1) 分组：原始 labels 里那一项仍带 `分组:` 前缀，用 getTaskGroupName 取出显示名再精确比对。
 *      后端会把它 unshift 到 display_labels[0]，但这里【不按下标 0 猜】——
 *      任务没有分组时第 0 项就是普通自定义标签，按位置判会误伤。
 *   2) 订阅：后端新增的只读字段 subscription_labels（含订阅源已删除时的字面量「订阅任务」）。
 *
 * 定类别靠【名额消费】而不是集合成员判定：分组名只有 1 个名额（后端只 unshift 一条），
 * 订阅名的名额 = 它在 subscription_labels 里的出现次数。名额用完后同名的后续项自动落到 custom。
 * 这样「分组 娱乐 + 订阅 娱乐 + 自定义 娱乐」三条同名标签会各归各类，三个开关互不干扰。
 *
 * ⚠️ 同名多条时，谁先出现谁先被认领，无法还原后端那一条究竟来自哪个来源 ——
 * 但同名意味着文案完全一样，用户看到的结果没有差别，每个开关仍然恰好管住一条。
 *
 * ⚠️ subscription_labels 是后加的字段，老后端 / 未同步的演示站不会下发。
 * 这里对「缺失或不是数组」一律降级成空名额：订阅标签会被归进 custom 一类，
 * 表现为「关掉订阅标签开关时订阅名没被藏掉」，但界面照常渲染、不报错——宁可少隐藏也不能崩。
 */
export function classifyDisplayTaskLabels(
  displayLabels: string[] = [],
  rawLabels: string[] = [],
  subscriptionLabels?: unknown,
): DisplayTaskLabelEntry[] {
  const groupName = getTaskGroupName(rawLabels)

  // 订阅名建「名额表」而不是 Set：Set 只能回答「这个字符串是不是订阅名」，
  // 回答不了「并排的两条同名标签里，哪一条才是订阅那条」。
  const subscriptionQuota = new Map<string, number>()
  if (Array.isArray(subscriptionLabels)) {
    for (const item of subscriptionLabels) {
      if (typeof item !== 'string' || !item) continue
      subscriptionQuota.set(item, (subscriptionQuota.get(item) ?? 0) + 1)
    }
  }

  // 分组名额恒为 1：后端 buildPreparedTaskLabels 只会 unshift 一条分组名。
  let groupQuota = groupName ? 1 : 0

  const entries: DisplayTaskLabelEntry[] = []
  displayLabels.forEach((label, index) => {
    if (!label) return

    let kind: DisplayTaskLabelKind = 'custom'
    if (groupQuota > 0 && label === groupName) {
      groupQuota -= 1
      kind = 'group'
    } else {
      const remaining = subscriptionQuota.get(label) ?? 0
      if (remaining > 0) {
        subscriptionQuota.set(label, remaining - 1)
        kind = 'subscription'
      }
    }

    // key 必须带下标：同名两条只用 label 当 :key，Vue 会报重复 key 警告，
    // 且可能在开关切换时复用错节点（藏掉的和留下的是同一个字符串）。
    entries.push({ label, kind, key: `${index}-${kind}-${label}` })
  })

  return entries
}

export function splitTaskLabels(labels: string[] = []) {
  const editableLabels: string[] = []
  const internalLabels: string[] = []
  const groupName = getTaskGroupName(labels)

  for (const label of labels) {
    if (!label) continue
    if (isInternalTaskLabel(label)) {
      internalLabels.push(label)
      continue
    }
    editableLabels.push(label)
  }

  return {
    editableLabels: uniqueLabels(editableLabels),
    internalLabels: uniqueLabels(internalLabels),
    groupName,
  }
}

export function mergeTaskLabels(editableLabels: string[] = [], internalLabels: string[] = [], groupName = '') {
  const merged = [...editableLabels, ...internalLabels.filter(label => !label.startsWith(TASK_GROUP_LABEL_PREFIX))]
  const groupLabel = toTaskGroupLabel(groupName)
  if (groupLabel) merged.push(groupLabel)
  return uniqueLabels(merged)
}
