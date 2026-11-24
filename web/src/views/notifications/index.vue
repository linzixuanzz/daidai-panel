<script setup lang="ts">
import { ref, onMounted, computed, nextTick } from 'vue'
import { notificationApi, type NotifyChannelDefinition, type NotifyFieldDefinition, type NotifyPushScope } from '@/api/notification'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Bell, Plus, Refresh, Search } from '@element-plus/icons-vue'
import { useResponsive } from '@/composables/useResponsive'
import { extractError, isCancel } from '@/utils/error'
import { toast } from '@/utils/toast'
import { scrollListToTop } from '@/utils/scrollToTop'
import DdBadge from '@/components/ui/DdBadge.vue'
import { formatDateTime } from '@/utils/datetime'

const { isMobile, dialogFullscreen } = useResponsive()
// 页面根：翻页回顶的锚点（scrollListToTop 从这里往上找移动端的 .layout-main、往里找桌面的 .table-card）
const pageRootRef = ref<HTMLElement | null>(null)

const channels = ref<any[]>([])
const channelLoading = ref(false)
// 渠道类型 + 字段 schema 都来自 GET /notifications/types，Web 不再持有本地副本。
const channelTypes = ref<NotifyChannelDefinition[]>([])

const showChannelDialog = ref(false)
const isCreateChannel = ref(true)
// 渠道保存的在途锁：请求期间锁住底部按钮，避免连点重复创建渠道
const channelSaving = ref(false)

interface ChannelFormState {
  id: number
  name: string
  type: string
  config: string
  // 这里刻意用宽松的 string 而不是 'default' | 'bound'：
  // el-radio-group 的 v-model 回写类型是 string | number | boolean，标成字面量联合类型
  // 会让 vue-tsc 在 v-model 上报不兼容。收窄统一放在提交前的 normalizePushScope 里做。
  push_scope: string
}

const channelForm = ref<ChannelFormState>({ id: 0, name: '', type: 'webhook', config: '{}', push_scope: 'default' })

// 服务端把空串也当作「默认推送」，所以只认 'bound' 这一个值，其余一律归到默认。
function normalizePushScope(raw: unknown): NotifyPushScope {
  return raw === 'bound' ? 'bound' : 'default'
}

function isBoundChannel(row: any): boolean {
  return normalizePushScope(row?.push_scope) === 'bound'
}

// 一条「所有渠道都不参与广播」的顶部警示。
//
// 只列举仓库里真实存在的广播调用点：资源告警（service/resource_monitor.go）、
// 登录通知（handler/auth.go）、静默更新结果（handler/system_update_auto.go）。
// 不要往里加订阅同步 / 备份 / 依赖安装 —— 面板根本没有这几处通知调用，写上去是在承诺不存在的能力。
const hasEnabledDefaultChannel = computed(() =>
  channels.value.some(c => c.enabled && !isBoundChannel(c))
)
const showNoDefaultChannelAlert = computed(() =>
  !channelLoading.value && channels.value.length > 0 && !hasEnabledDefaultChannel.value
)

// --- Search / filter state ---
const searchKeyword = ref('')
const filterType = ref('')
const filterStatus = ref('')
const channelPage = ref(1)
const channelPageSize = ref(10)

// --- Filtered channels ---
// 拆成「状态筛选之前」和「最终结果」两层，是为了让状态下拉里的计数与切过去看到的条数一致：
// 计数的基数必须排除状态筛选自身（否则选中「启用」后「禁用」永远显示 0），
// 但必须保留搜索词与类型（否则搜完再看计数会和实际条数对不上）。
const channelsBeforeStatusFilter = computed(() => {
  let list = channels.value
  if (searchKeyword.value) {
    const kw = searchKeyword.value.toLowerCase()
    list = list.filter(c => c.name?.toLowerCase().includes(kw) || c.type?.toLowerCase().includes(kw))
  }
  if (filterType.value) {
    list = list.filter(c => c.type === filterType.value)
  }
  return list
})

const filteredChannels = computed(() => {
  const list = channelsBeforeStatusFilter.value
  if (filterStatus.value === 'enabled') return list.filter(c => c.enabled)
  if (filterStatus.value === 'disabled') return list.filter(c => !c.enabled)
  return list
})

// 状态下拉里的计数。
//
// 能这么算的前提：本页是【客户端筛选】——notificationApi.list() 一次性把全量渠道拿回
// channels.value，搜索/类型/状态/分页全在 computed 里做，改筛选只会走 handleChannelSearch()
// （把页码归 1），不会重新发请求。所以数出来的是真实总数，不是拿当前页冒充。
const statusCounts = computed(() => {
  const list = channelsBeforeStatusFilter.value
  const enabled = list.filter(c => c.enabled).length
  return { all: list.length, enabled, disabled: list.length - enabled }
})

const pagedChannels = computed(() => {
  const start = (channelPage.value - 1) * channelPageSize.value
  return filteredChannels.value.slice(start, start + channelPageSize.value)
})

const filteredTotal = computed(() => filteredChannels.value.length)

function handleChannelSearch() {
  channelPage.value = 1
}

// 翻页 / 改每页条数后回到列表顶部（issue #143 O3，双端都做）。
// 只挂在分页器的 current-change / size-change 上：它们只在用户翻页（或 total 变小把页码夹回）时触发，
// 搜索、改筛选时直接改 channelPage 不会触发，所以不会在筛选时把人拽回顶部。
// 本页是前端分页，数据早已在内存里，没有「请求回来」的时机可等；
// 等 nextTick 让新一页先渲染出来再滚，免得先回顶、后换内容，位置又被新内容的高度顶偏。
async function handleChannelPageChange() {
  await nextTick()
  scrollListToTop(pageRootRef.value)
}

async function handleChannelPageSizeChange() {
  // 改每页条数后从第 1 页看起：原页码在新页大小下对应的是另一批数据，停在中间没有意义
  channelPage.value = 1
  await nextTick()
  scrollListToTop(pageRootRef.value)
}

function resetFilters() {
  searchKeyword.value = ''
  filterType.value = ''
  filterStatus.value = ''
  channelPage.value = 1
}

// --- Channel type color / avatar helpers ---
// hue 是主题令牌名（ok / info / warn / err / accent-2），头像用 --dd-<hue> 调浅底、--dd-<hue>-ink 做字色
const typeColorMap: Record<string, { hue: string; badge: string }> = {
  wecom: { hue: 'ok', badge: 'success' },
  wecom_app: { hue: 'ok', badge: 'success' },
  pushplus: { hue: 'info', badge: '' },
  feishu: { hue: 'info', badge: '' },
  dingtalk: { hue: 'warn', badge: 'warning' },
  email: { hue: 'err', badge: 'danger' },
  webhook: { hue: 'info', badge: 'warning' },
  custom: { hue: 'accent-2', badge: 'warning' },
  telegram: { hue: 'info', badge: '' },
  bark: { hue: 'warn', badge: 'warning' },
  serverchan: { hue: 'accent-2', badge: '' },
  gotify: { hue: 'ok', badge: 'success' },
  pushdeer: { hue: 'warn', badge: 'warning' },
  pushme: { hue: 'info', badge: '' },
  discord: { hue: 'info', badge: '' },
  slack: { hue: 'err', badge: 'danger' },
  ntfy: { hue: 'ok', badge: 'success' },
  wxpusher: { hue: 'ok', badge: 'success' },
}

function getChannelAvatar(type: string, name: string) {
  const initial = (name || type || '?').charAt(0).toUpperCase()
  const hue = typeColorMap[type]?.hue
  // 未知渠道类型走中性灰
  if (!hue) return { initial, bg: 'var(--dd-surface-3)', color: 'var(--dd-text-2)' }
  return { initial, bg: `color-mix(in srgb, var(--dd-${hue}) 12%, transparent)`, color: `var(--dd-${hue}-ink)` }
}

function getTypeBadgeType(type: string): string {
  return typeColorMap[type]?.badge || ''
}

// 渠道 config 的扁平键值视图，与 channelForm.config 的 JSON 文本互相同步。
const configData = ref<Record<string, string>>({})

// 当前渠道的字段 schema，来自服务端 GET /notifications/types。
//
// 这里以前是 225 行硬编码的 configFields（22 个 case / 90 个字段槽），与服务端
// notifier.go 实际读取的 config 键靠人手同步。现在唯一真源在
// server/model/notify_channel_registry.go，并由 Go 测试与 notifier.go 双向绑死，
// Web 只负责渲染。
//
// 刻意不做「服务端没下发 fields」的降级：Web 与服务端永远同版本发布，
// 不存在「新 Web + 老服务端」的组合，拿不到 schema 就是空表单。
const currentChannelFields = computed<NotifyFieldDefinition[]>(
  () => channelTypes.value.find(t => t.type === channelForm.value.type)?.fields ?? []
)

// 求 show_when 依赖字段的当前取值：用户没填过时按 schema 声明的 default 判定，
// 与服务端「留空走默认值」的行为保持一致（wecom / wecom_app 的 msg_type 留空即 text，
// 这也是改造前那两行 `(configData.value.msg_type || 'text')` 的等价写法）。
function resolveShowWhenValue(fields: NotifyFieldDefinition[], key: string): string {
  const filled = (configData.value[key] ?? '').toString().trim()
  if (filled) return filled
  return (fields.find(f => f.key === key)?.default ?? '').trim()
}

// show_when 语义固定为「单键等值命中」：依赖字段的当前值命中 values 之一才显示本字段。
// 服务端刻意不支持表达式，这里也不要扩展成联动 DSL。
const configFields = computed<NotifyFieldDefinition[]>(() => {
  const fields = currentChannelFields.value
  return fields.filter(field => {
    const condition = field.show_when
    if (!condition) return true
    return condition.values.includes(resolveShowWhenValue(fields, condition.key))
  })
})

function syncConfigToForm() {
  channelForm.value.config = JSON.stringify(configData.value)
}

function syncFormToConfig() {
  try {
    configData.value = JSON.parse(channelForm.value.config)
  } catch {
    configData.value = {}
  }
}

async function loadChannels() {
  channelLoading.value = true
  try {
    const res = await notificationApi.list()
    channels.value = res.data || []
  } catch (err: any) {
    ElMessage.error(err?.response?.data?.error || '加载通知渠道失败')
  } finally {
    channelLoading.value = false
  }
}

async function loadChannelTypes() {
  try {
    const res = await notificationApi.types()
    channelTypes.value = res.data || []
  } catch { /* ignore */ }
}

onMounted(() => {
  loadChannels()
  loadChannelTypes()
})

function applyChannelTypeDefaults() {
  // 钉钉新建渠道默认走 Markdown，与后端 msg_type 空值兜底保持一致
  if (channelForm.value.type === 'dingtalk') {
    configData.value.msg_type = 'markdown'
  }
}

function onChannelTypeChange() {
  configData.value = {}
  applyChannelTypeDefaults()
}

function openCreateChannel() {
  isCreateChannel.value = true
  // 新建默认给「默认推送」：与老版本行为一致，用户想隔离时再手动切成「绑定推送」。
  channelForm.value = { id: 0, name: '', type: 'webhook', config: '{}', push_scope: 'default' }
  configData.value = {}
  applyChannelTypeDefaults()
  showChannelDialog.value = true
}

function openEditChannel(row: any) {
  isCreateChannel.value = false
  channelForm.value = {
    id: row.id,
    name: row.name,
    type: row.type,
    config: row.config || '{}',
    push_scope: normalizePushScope(row.push_scope),
  }
  syncFormToConfig()
  showChannelDialog.value = true
}

// 配置中属于 JSON 结构的字段 key（需要 JSON.parse 校验）
const JSON_CONFIG_KEYS = new Set([
  'headers',
  'news_articles',
  'mpnews_articles',
  'template_card_payload',
])

// email 端口校验 key
const NUMERIC_CONFIG_KEYS = new Set(['smtp_port', 'port'])

function validateConfigFields(): string | null {
  // 只校验当前可见的字段：被 show_when 过滤掉的字段不会随表单提交语义生效。
  for (const field of configFields.value) {
    const key = field.key
    const val = (configData.value[key] ?? '').toString().trim()
    if (!val) continue
    if (JSON_CONFIG_KEYS.has(key)) {
      try {
        JSON.parse(val)
      } catch (e: any) {
        return `字段「${field.label || key}」不是合法 JSON：${e?.message || ''}`
      }
    }
    if (NUMERIC_CONFIG_KEYS.has(key)) {
      const n = Number(val)
      if (!Number.isInteger(n) || n <= 0 || n > 65535) {
        return `字段「${field.label || key}」应为 1-65535 的端口号`
      }
    }
  }
  return null
}

async function handleSaveChannel() {
  if (!channelForm.value.name.trim()) {
    ElMessage.warning('名称不能为空')
    return
  }
  const validationErr = validateConfigFields()
  if (validationErr) {
    ElMessage.warning(validationErr)
    return
  }
  syncConfigToForm()
  // 提交前把 push_scope 收窄成服务端认的两个值：服务端对非法取值会直接 400。
  const payload = {
    name: channelForm.value.name,
    type: channelForm.value.type,
    config: channelForm.value.config,
    push_scope: normalizePushScope(channelForm.value.push_scope),
  }
  // 校验全部通过后才置位，保证校验失败的早退分支不会把按钮锁死
  channelSaving.value = true
  try {
    if (isCreateChannel.value) {
      await notificationApi.create(payload)
      ElMessage.success('创建成功')
    } else {
      await notificationApi.update(channelForm.value.id, payload)
      ElMessage.success('更新成功')
    }
    showChannelDialog.value = false
    loadChannels()
  } catch (err: any) {
    ElMessage.error(err?.response?.data?.error || (isCreateChannel.value ? '创建失败' : '更新失败'))
  } finally {
    channelSaving.value = false
  }
}

async function handleDeleteChannel(id: number) {
  try {
    await ElMessageBox.confirm('确定要删除该通知渠道吗？', '确认删除', { type: 'warning' })
    await notificationApi.delete(id)
    ElMessage.success('删除成功')
    loadChannels()
  } catch (err) {
    // 这里原来是裸 `catch { /* cancelled */ }`，把两件完全不同的事混成了一件：
    // 用户在确认框点「取消」，和「点了确定但删除接口真的失败了」，走的是同一个分支。
    // 后果是删除失败时界面上一点反馈都没有，渠道还在列表里，用户只会以为自己没点到。
    // 必须先用 isCancel() 摘掉取消，剩下的才是真错误。
    if (isCancel(err)) return
    ElMessage.error(extractError(err, '删除失败'))
  }
}

async function handleToggleChannel(row: any) {
  try {
    const enabling = !row.enabled
    await ElMessageBox.confirm(
      enabling
        ? `确认启用通知渠道「${row.name}」吗？`
        : `确认禁用通知渠道「${row.name}」吗？禁用后将不再接收任务推送。`,
      enabling ? '启用确认' : '禁用确认',
      { type: enabling ? 'info' : 'warning' }
    )
    if (row.enabled) {
      await notificationApi.disable(row.id)
    } else {
      await notificationApi.enable(row.id)
    }
    ElMessage.success(row.enabled ? '已禁用' : '已启用')
    loadChannels()
  } catch (err) {
    // 手写的 `err === 'cancel'` 漏了 'close'（点右上角 × 关闭确认框时 EP reject 的是它），
    // 那种情况下会弹一条没有意义的「操作失败」。统一走 isCancel()。
    if (isCancel(err)) return
    ElMessage.error(extractError(err, '操作失败'))
  }
}

async function handleTestChannel(id: number) {
  try {
    const res: any = await notificationApi.test(id)
    const detail = res?.message || res?.data?.message
    ElMessage.success(detail ? `测试通知发送成功：${detail}` : '测试通知发送成功')
  } catch (e: any) {
    const data = e?.response?.data
    const mainError = data?.error || '测试发送失败'
    const detail = data?.detail || data?.message || e?.message

    if (detail && detail !== mainError) {
      // 这里原来直接弹 ElMessageBox.alert：一次「测试」失败就把整个界面挡住、
      // 必须点「知道了」才能继续。测试渠道往往要连着试好几个，很打断。
      // 改成带操作的轻提示——一眼看到失败原因，想看服务端返回的长详情再点开。
      toast.error(mainError, {
        action: {
          text: '查看详情',
          handler: () => {
            ElMessageBox.alert(String(detail), mainError, {
              confirmButtonText: '知道了',
              type: 'error',
            }).catch(() => {})
          },
        },
      })
    } else {
      ElMessage.error(mainError)
    }
  } finally {
    await loadChannels()
  }
}

function getTypeName(type: string) {
  const found = channelTypes.value.find(t => t.type === type)
  return found?.name || type
}

// --- 「最近测试」结果的展示三件套 ---
// 从模板里的两串嵌套三元原样搬出来，判定分支逐字对齐（success / error|failed / 其余），
// 只为让下面那格能包进 Transition 之后还看得清。不要在这里加新分支：
// last_test_status 的取值由服务端写入，Web 只做渲染。
// 返回值刻意不标注成 string —— 让 TS 推成字面量联合，才能直接喂给 el-tag 的 type。

function getTestStatusTagType(status: unknown) {
  if (status === 'success') return 'success'
  if (status === 'error' || status === 'failed') return 'danger'
  return 'warning'
}

function getTestStatusText(status: unknown) {
  if (status === 'success') return '测试通过'
  if (status === 'error' || status === 'failed') return '测试未通过'
  return '未测试'
}

// 过渡 key 必须绑【状态值】而不是 row.id：绑 row.id 的话同一行永远是同一个 key，
// 节点被原地复用，测完之后过渡一次都不会触发。
// 没测过时固定给 'none'，这样「未测试 → 测试通过」这次首测也能被演出来。
function getTestStatusKey(row: any): string {
  if (!row?.last_test_at) return 'none'
  return String(row.last_test_status ?? 'unknown')
}

function parseChannelConfig(configText: string): Record<string, unknown> {
  try {
    const parsed = JSON.parse(configText || '{}')
    return parsed && typeof parsed === 'object' && !Array.isArray(parsed) ? parsed : {}
  } catch {
    return {}
  }
}

function extractDisplayHost(raw: unknown): string {
  const value = String(raw ?? '').trim()
  if (!value) return ''
  try {
    const parsed = new URL(value)
    return parsed.host || value
  } catch {
    return value.length > 28 ? `${value.slice(0, 28)}...` : value
  }
}

function splitConfigTargets(raw: unknown): string[] {
  return String(raw ?? '')
    .split(/[,\n;|\t ]+/)
    .map(item => item.trim())
    .filter(Boolean)
}

function countConfiguredConfigItems(config: Record<string, unknown>): number {
  return Object.values(config).filter((value) => {
    if (value === null || value === undefined) return false
    if (Array.isArray(value)) return value.length > 0
    return String(value).trim() !== ''
  }).length
}

function getChannelConfigSummary(row: any): string[] {
  const config = parseChannelConfig(row.config || '{}')
  const lines: string[] = []
  const configCount = countConfiguredConfigItems(config)

  switch (row.type) {
    case 'webhook':
    case 'custom':
      if (config.url) lines.push(`地址 ${extractDisplayHost(config.url)}`)
      break
    case 'dingtalk':
    case 'wecom':
    case 'feishu':
    case 'discord':
    case 'slack':
      if (config.webhook) lines.push(`地址 ${extractDisplayHost(config.webhook)}`)
      break
    case 'email':
      if (config.smtp_host) {
        const port = String(config.smtp_port ?? '').trim()
        lines.push(`SMTP ${String(config.smtp_host)}${port ? `:${port}` : ''}`)
      }
      {
        const sslMode = String(config.smtp_ssl ?? '').trim()
        const port = String(config.smtp_port ?? '').trim()
        if (sslMode === 'true' || ((!sslMode || sslMode === 'auto') && port === '465')) {
          lines.push('SSL 已启用')
        } else if (sslMode === 'false') {
          lines.push('SSL 已关闭')
        } else if (sslMode === 'auto') {
          lines.push('SSL 自动')
        }
      }
      if (splitConfigTargets(config.to).length > 0) lines.push(`收件人 ${splitConfigTargets(config.to).length} 个`)
      break
    case 'telegram':
      if (config.chat_id) lines.push(`Chat ${String(config.chat_id)}`)
      if (config.message_thread_id) lines.push(`Topic ${String(config.message_thread_id)}`)
      if (config.api_host) lines.push(`API ${extractDisplayHost(config.api_host)}`)
      break
    case 'gotify':
    case 'pushdeer':
    case 'pushme':
    case 'chanify':
    case 'ntfy':
      if (config.server) lines.push(`服务器 ${extractDisplayHost(config.server)}`)
      if (row.type === 'ntfy' && config.topic) lines.push(`主题 ${String(config.topic)}`)
      break
    case 'wxpusher': {
      const uidCount = splitConfigTargets(config.uids).length
      const topicCount = splitConfigTargets(config.topic_ids).length
      if (uidCount > 0) lines.push(`UID ${uidCount} 个`)
      if (topicCount > 0) lines.push(`Topic ${topicCount} 个`)
      break
    }
    case 'wecom_app':
      if (config.agent_id) lines.push(`Agent ${String(config.agent_id)}`)
      if (config.base_url) lines.push(`地址 ${extractDisplayHost(config.base_url)}`)
      break
  }

  if (lines.length === 0) {
    return configCount > 0 ? [`已配置 ${configCount} 项`] : ['未配置']
  }
  if (configCount > lines.length) {
    lines.push(`共 ${configCount} 项配置`)
  }
  return lines.slice(0, 2)
}

</script>

<template>
  <div ref="pageRootRef" class="notifications-page dd-fixed-page dd-page-hide-heading">
    <!-- Page Header -->
    <div class="page-header dd-rise" style="--dd-i: 0">
      <div>
        <h2 class="page-title-with-icon"><el-icon><Bell /></el-icon><span>通知渠道</span></h2>
        <p class="page-subtitle">配置任务执行结果的通知渠道</p>
      </div>
    </div>

        <!--
          没有任何「默认推送」渠道时的警示。
          只列举仓库里真实存在的三处广播调用点，不要扩写成「所有系统通知」之类的模糊说法。
        -->
        <!--
          这条警示是「凭空冒出」的：把最后一个默认推送渠道禁掉的那一刻它就出现，
          把整个工具条和表格往下顶。淡入淡出至少让这次出现有个过程，而不是硬闪。
          刻意只做 opacity，不做高度过渡——高度动画会把下面整张表反复重排。
        -->
        <Transition name="dd-alert-fade">
          <el-alert
            v-if="showNoDefaultChannelAlert"
            type="warning"
            show-icon
            :closable="false"
            class="no-default-alert"
            title="当前没有启用中的「默认推送」渠道"
          >
            <template #default>
              资源告警、登录通知、静默更新结果这三类系统通知将不会发送（面板不做兜底，也不会退回其它渠道）。
              如需接收，请把至少一个已启用的渠道改为「默认推送」。
            </template>
          </el-alert>
        </Transition>

        <!-- Toolbar -->
        <div class="toolbar dd-rise" style="--dd-i: 1">
          <div class="toolbar__left">
            <el-input
              v-model="searchKeyword"
              placeholder="搜索渠道名称或关键词..."
              clearable
              class="toolbar__search"
              @keyup.enter="handleChannelSearch"
              @clear="handleChannelSearch"
            >
              <template #prefix><el-icon><Search /></el-icon></template>
            </el-input>
            <el-select v-model="filterType" placeholder="类型" clearable class="toolbar__filter" @change="handleChannelSearch">
              <template #prefix>类型</template>
              <el-option label="全部" value="" />
              <el-option v-for="t in channelTypes" :key="t.type" :label="t.name" :value="t.type" />
            </el-select>
            <!--
              状态筛选带计数：本页没有 .status-tabs 分段控件，状态筛选就是这个下拉，
              所以计数挂在选项上——展开时就能看到「启用 5 / 禁用 2」，不必逐个切过去数。
              计数是「统计型」，开 show-zero：「禁用 0」是有信息量的，消失了反而像没加载出来。
              level=info（描边不实心）：中性计数，不该和需要处理的红色角标抢注意力。
              注意 label 属性要保留：选中后输入框里显示的是 label，默认插槽只管下拉项。
            -->
            <el-select v-model="filterStatus" placeholder="状态" clearable class="toolbar__filter" @change="handleChannelSearch">
              <template #prefix>状态</template>
              <el-option label="全部" value="">
                <span class="filter-option">
                  <span>全部</span>
                  <DdBadge :value="statusCounts.all" level="info" show-zero title="全部渠道" />
                </span>
              </el-option>
              <el-option label="启用" value="enabled">
                <span class="filter-option">
                  <span>启用</span>
                  <DdBadge :value="statusCounts.enabled" level="info" show-zero title="已启用渠道" />
                </span>
              </el-option>
              <el-option label="禁用" value="disabled">
                <span class="filter-option">
                  <span>禁用</span>
                  <DdBadge :value="statusCounts.disabled" level="info" show-zero title="已禁用渠道" />
                </span>
              </el-option>
            </el-select>
            <el-button @click="resetFilters">
              <el-icon><Refresh /></el-icon> 重置
            </el-button>
          </div>
          <div class="toolbar__right">
            <el-button type="primary" @click="openCreateChannel">
              <el-icon><Plus /></el-icon> 新建渠道
            </el-button>
          </div>
        </div>

        <!-- Mobile Cards -->
        <div v-if="isMobile" class="dd-mobile-list dd-rise" style="--dd-i: 2">
          <div
            v-for="row in pagedChannels"
            :key="row.id"
            class="dd-mobile-card"
          >
            <div class="dd-mobile-card__header">
              <div class="dd-mobile-card__title-wrap">
                <span class="dd-mobile-card__title">{{ row.name }}</span>
                <div class="dd-mobile-card__badges">
                  <el-tag size="small" :type="getTypeBadgeType(row.type)" effect="plain">{{ getTypeName(row.type) }}</el-tag>
                  <el-tag v-if="isBoundChannel(row)" size="small" type="warning" effect="plain">仅绑定</el-tag>
                </div>
              </div>
              <el-switch :model-value="row.enabled" size="small" @change="handleToggleChannel(row)" />
            </div>
            <div class="dd-mobile-card__body">
              <div class="dd-mobile-card__grid">
                <div class="dd-mobile-card__field dd-mobile-card__field--full">
                  <span class="dd-mobile-card__label">配置概览</span>
                  <div class="dd-mobile-card__value config-summary">
                    <span v-for="item in getChannelConfigSummary(row)" :key="item" class="config-summary__line">{{ item }}</span>
                  </div>
                </div>
                <div class="dd-mobile-card__field">
                  <span class="dd-mobile-card__label">创建时间</span>
                  <span class="dd-mobile-card__value">{{ formatDateTime(row.created_at) }}</span>
                </div>
              </div>
              <div class="dd-mobile-card__actions notification-card__actions">
                <el-button size="small" type="success" plain @click="handleTestChannel(row.id)">测试</el-button>
                <el-button size="small" type="primary" plain @click="openEditChannel(row)">编辑</el-button>
                <el-button size="small" type="danger" plain @click="handleDeleteChannel(row.id)">删除</el-button>
              </div>
            </div>
          </div>
          <el-empty v-if="!channelLoading && pagedChannels.length === 0" description="暂无通知渠道" />
        </div>

        <!-- Desktop Table -->
        <div v-else class="table-card dd-spot dd-rise" style="--dd-i: 2">
          <el-table
            :data="pagedChannels"
            v-loading="channelLoading"
            style="width: 100%"
            :header-cell-style="{ background: 'var(--el-fill-color-light)', color: 'var(--el-text-color-regular)', fontWeight: 600, fontSize: '13px' }"
          >
            <el-table-column prop="name" label="名称" min-width="180">
              <template #default="{ row }">
                <div class="channel-name-cell">
                  <div
                    class="channel-avatar"
                    :style="{ background: getChannelAvatar(row.type, row.name).bg, color: getChannelAvatar(row.type, row.name).color }"
                  >
                    {{ getChannelAvatar(row.type, row.name).initial }}
                  </div>
                  <div class="channel-name-info">
                    <span class="channel-name-text">{{ row.name }}</span>
                    <span class="channel-name-sub">{{ getTypeName(row.type) }}</span>
                  </div>
                </div>
              </template>
            </el-table-column>
            <el-table-column prop="type" label="类型" width="130">
              <template #default="{ row }">
                <el-tag size="small" :type="getTypeBadgeType(row.type)" effect="plain" round>{{ getTypeName(row.type) }}</el-tag>
              </template>
            </el-table-column>
            <el-table-column label="推送范围" width="120" align="center">
              <template #default="{ row }">
                <el-tooltip
                  :content="isBoundChannel(row)
                    ? '不参与广播，只有任务绑定它、或脚本显式指定它时才推送'
                    : '参与广播：资源告警、登录通知、静默更新结果，以及未绑定渠道的任务通知都会发到这里'"
                  placement="top"
                >
                  <el-tag size="small" :type="isBoundChannel(row) ? 'warning' : 'success'" effect="plain" round>
                    {{ isBoundChannel(row) ? '仅绑定' : '默认推送' }}
                  </el-tag>
                </el-tooltip>
              </template>
            </el-table-column>
            <el-table-column label="配置概览" min-width="180">
              <template #default="{ row }">
                <div class="config-summary">
                  <span v-for="item in getChannelConfigSummary(row)" :key="item" class="config-summary__line">{{ item }}</span>
                </div>
              </template>
            </el-table-column>
            <el-table-column label="启用" width="80" align="center">
              <template #default="{ row }">
                <el-switch :model-value="row.enabled" size="small" @change="handleToggleChannel(row)" />
              </template>
            </el-table-column>
            <el-table-column label="最近测试" width="160">
              <template #default="{ row }">
                <!-- 点「测试」后 handleTestChannel 会重拉列表，这一格从「未测试」翻成
                     「测试通过 / 测试未通过」——是本页最值得被看见的一次变化，硬切会被眼睛错过。
                     out-in 让旧结果先淡出、新结果再淡入，给出一次明确的交接。
                     key 走 getTestStatusKey(row)（状态值），绝不能绑 row.id。
                     只做 opacity：表格行里任何位移都会连带整行抖。
                     外层 .test-status-slot 撑住最小高度，out-in 中间那一帧内容被移除时行高不塌。 -->
                <span class="test-status-slot">
                  <Transition name="dd-status-switch" mode="out-in">
                    <div v-if="row.last_test_at" :key="getTestStatusKey(row)" class="test-status">
                      <el-tag
                        size="small"
                        :type="getTestStatusTagType(row.last_test_status)"
                        effect="plain"
                        round
                      >
                        {{ getTestStatusText(row.last_test_status) }}
                      </el-tag>
                      <span class="time-text">{{ formatDateTime(row.last_test_at) }}</span>
                    </div>
                    <span v-else key="none" class="text-muted">未测试</span>
                  </Transition>
                </span>
              </template>
            </el-table-column>
            <el-table-column prop="created_at" label="创建时间" width="170">
              <template #default="{ row }">
                <div class="time-cell">
                  <span class="time-text">{{ formatDateTime(row.created_at) }}</span>
                </div>
              </template>
            </el-table-column>
            <el-table-column label="操作" width="180" fixed="right" align="center">
              <template #default="{ row }">
                <div class="action-btns">
                  <el-button size="small" text type="success" @click="handleTestChannel(row.id)">测试</el-button>
                  <el-button size="small" text type="primary" @click="openEditChannel(row)">编辑</el-button>
                  <el-button size="small" text type="danger" @click="handleDeleteChannel(row.id)">删除</el-button>
                </div>
              </template>
            </el-table-column>
          </el-table>
        </div>

        <!-- Pagination -->
        <div class="pagination-bar dd-rise" style="--dd-i: 3">
          <span class="pagination-total">共 {{ filteredTotal }} 条</span>
          <el-pagination
            v-model:current-page="channelPage"
            v-model:page-size="channelPageSize"
            :total="filteredTotal"
            :page-sizes="[10, 20, 50]"
            layout="sizes, prev, pager, next"
            @current-change="handleChannelPageChange"
            @size-change="handleChannelPageSizeChange"
          />
        </div>

    <!-- Channel Dialog -->
    <el-dialog v-model="showChannelDialog" :title="isCreateChannel ? '新建通知渠道' : '编辑通知渠道'" width="600px" :fullscreen="dialogFullscreen">
      <el-form :model="channelForm" :label-width="dialogFullscreen ? 'auto' : '130px'" :label-position="dialogFullscreen ? 'top' : 'right'">
        <el-form-item label="名称">
          <el-input v-model="channelForm.name" placeholder="渠道名称" />
        </el-form-item>
        <el-form-item label="类型">
          <el-select v-model="channelForm.type" style="width: 100%" @change="onChannelTypeChange">
            <el-option v-for="t in channelTypes" :key="t.type" :label="t.name" :value="t.type" />
          </el-select>
        </el-form-item>
        <!--
          推送范围是渠道自身的属性（数据库一等列），不是渠道 config 里的字段，
          所以必须手写在这里，绝不能混进下面由服务端 schema 驱动的 configFields 循环。
        -->
        <el-form-item label="推送范围">
          <el-radio-group v-model="channelForm.push_scope">
            <el-radio-button value="default">默认推送</el-radio-button>
            <el-radio-button value="bound">绑定推送</el-radio-button>
          </el-radio-group>
          <div class="form-tip">
            <strong>默认推送</strong>：参与广播。资源告警、登录通知、静默更新结果，以及没有绑定渠道的任务通知都会发到这里。<br />
            <strong>绑定推送</strong>：不参与广播，只有任务在「通知渠道」里选中它、或脚本调用时显式指定它才会推送。
            想做到「一个脚本对应一个通知」就选这个。
          </div>
        </el-form-item>
        <el-divider content-position="left">配置</el-divider>
        <!--
          通用渲染器：字段来自服务端 schema，widget 只有 input / password / textarea / select 四种。
          最后的 v-else 是兜底分支，遇到不认识的 widget 一律按普通输入框渲染，绝不隐藏字段。
        -->
        <el-form-item v-for="field in configFields" :key="field.key" :label="field.label">
          <el-select
            v-if="field.widget === 'select'"
            v-model="configData[field.key]"
            :placeholder="field.placeholder || field.label"
            clearable
            style="width: 100%"
          >
            <el-option v-for="opt in field.options || []" :key="opt.value" :label="opt.label" :value="opt.value" />
          </el-select>
          <el-input
            v-else-if="field.widget === 'textarea'"
            v-model="configData[field.key]"
            type="textarea"
            :rows="3"
            :placeholder="field.placeholder || field.label"
          />
          <el-input
            v-else
            v-model="configData[field.key]"
            :type="field.widget === 'password' ? 'password' : 'text'"
            :show-password="field.widget === 'password'"
            :placeholder="field.placeholder || field.label"
          />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="showChannelDialog = false">取消</el-button>
        <el-button type="primary" :loading="channelSaving" :disabled="channelSaving" @click="handleSaveChannel">{{ isCreateChannel ? '创建' : '保存' }}</el-button>
      </template>
    </el-dialog>

  </div>
</template>

<style scoped lang="scss">
.notifications-page {
  padding: 0;
}

.page-header {
  display: flex;
  justify-content: space-between;
  align-items: flex-start;
  margin-bottom: 18px;
  gap: 16px;

  .page-title-with-icon { display: inline-flex; align-items: center; gap: 8px; }
  .page-title-with-icon :deep(.el-icon) { color: var(--el-color-primary); }

  h2 { margin: 0; font-size: 22px; font-weight: 700; color: var(--el-text-color-primary); line-height: 1.3; }
  .page-subtitle { font-size: 13px; color: var(--el-text-color-secondary); margin: 6px 0 0; line-height: 1.6; max-width: 720px; }
}

// 「没有默认推送渠道」警示条：跟在页头之后，不占满页面高度
.no-default-alert {
  margin-bottom: 4px;
}

// 弹窗里推送范围单选的说明文字。
// el-form-item__content 是 flex + wrap，这里必须占满整行，否则会挤到单选按钮右边。
.form-tip {
  flex: 0 0 100%;
  width: 100%;
  margin-top: 6px;
  font-size: 12px;
  line-height: 1.7;
  color: var(--el-text-color-secondary);
}

// 工具条：与定时任务页/订阅管理页统一（margin:14px 0、左区 gap 12、右区 gap 10）
.toolbar {
  display: flex; justify-content: space-between; align-items: center; margin: 14px 0; gap: 12px; flex-wrap: wrap;
  &__left { display: flex; align-items: center; gap: 12px; flex-wrap: wrap; flex: 1; min-width: 0; }
  &__right { display: flex; align-items: center; gap: 10px; flex-shrink: 0; }
  &__search { width: 260px; }
  &__filter { width: 130px; }
}

// 表格卡：1px 边框划分层次，不再用阴影浮起（dd-fixed-page 下的 flex + 内部滚动由全局规则接管）
.table-card {
  background: var(--el-bg-color);
  // 表格容器属容器类表面 → surface 档；overflow:hidden 让内部贴边的表头/行自动被圆角裁角
  border-radius: var(--dd-radius-surface);
  border: 1px solid var(--el-border-color-lighter);
  overflow: hidden;
}

.channel-name-cell {
  display: flex;
  align-items: center;
  gap: 10px;
}

// 渠道头像：底色/文字色保留按类型的品牌识别色（来自模板内联 style），
// 这里只统一通用尺寸与轻边框，边框走令牌以适配明暗。
// 形状承载语义（圆形=头像/身份标识），两种圆角模式下都固定正圆，不吃 --dd-radius-* 令牌。
.channel-avatar {
  width: 36px;
  height: 36px;
  border-radius: 50%;
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: 15px;
  font-weight: 700;
  flex-shrink: 0;
  letter-spacing: 0;
  border: 1px solid var(--el-border-color-lighter);
}

.channel-name-info {
  display: flex;
  flex-direction: column;
  gap: 1px;
  min-width: 0;
}

.channel-name-text {
  font-weight: 500;
  color: var(--el-text-color-primary);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.channel-name-sub {
  font-size: 12px;
  color: var(--el-text-color-placeholder);
}

.config-summary {
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.config-summary__line {
  font-size: 12px;
  color: var(--el-text-color-secondary);
  line-height: 1.5;
}

.test-status {
  display: flex;
  flex-direction: column;
  gap: 2px;
}

// 「最近测试」这一格的占位槽。
//
// out-in 中间有一帧「旧的已移除、新的还没进来」，没有最小高度的话整行会在测试完成的
// 那一刻塌一次再弹回来 —— 恰好发生在我们想让用户看清的那个瞬间，得不偿失。
// 42px = el-tag small(24px) + gap(2px) + 日期行(约 16px)，即「已测过」那一档的高度。
// 代价是「未测试」的行也被撑到同一高度（比原来高约 6px）；换来的是全表行高统一、
// 且测试完成时零跳动，这笔买卖划算。
// 用 block 级 flex 而不是 inline-flex：单元格里的行内盒会带基线余白，块级则严丝合缝。
.test-status-slot {
  display: flex;
  align-items: center;
  min-height: 42px;
}

.time-cell {
  display: flex;
  flex-direction: column;
  gap: 1px;
}

.time-text {
  font-family: var(--dd-font-mono, monospace);
  font-size: 12px;
  color: var(--el-text-color-regular);
}

.creator-text {
  font-size: 12px;
  color: var(--el-text-color-placeholder);
}

.text-muted { color: var(--el-text-color-placeholder); }

.action-btns {
  display: flex; align-items: center; justify-content: center; gap: 2px;

  // EP 自带 `.el-button + .el-button { margin-left: 12px }` 会叠加在上面的 gap 上，
  // 实际间距变成 14px 而不是设计的 2px。间距统一交给 gap
  // （与 tasks / deps / subscriptions 三页一致）。
  :deep(.el-button + .el-button) { margin-left: 0; }
}

// 下拉选项里「文字 + 计数角标」的排布。
// 这段样式虽然写在 scoped 里，但命中的是被 teleport 到 body 的下拉面板 ——
// 能命中是因为 .filter-option 这个 span 是本组件模板里写的、带 data-v 标记，
// scoped 编译出来的是普通属性选择器，与元素挂在 DOM 哪个位置无关。
// 用 inline-flex 而不是 block：EP 的 .el-select-dropdown__item 靠自身 line-height 撑高，
// 换成块级子元素会让选项高度塌到角标那 18px。
.filter-option {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  vertical-align: middle;
}

// 状态标签切换：只做透明度，禁止位移/缩放——表格行里任何位移都会连带整行。
// 时长与缓动走令牌，prefers-reduced-motion 时令牌自动降为 1ms 即等效关闭。
.dd-status-switch-enter-active,
.dd-status-switch-leave-active {
  transition: opacity var(--dd-motion-fast) var(--dd-ease-standard);
}

.dd-status-switch-enter-from,
.dd-status-switch-leave-to {
  opacity: 0;
}

// 顶部警示条的进出场：同样只做透明度，不碰高度。
.dd-alert-fade-enter-active,
.dd-alert-fade-leave-active {
  transition: opacity var(--dd-motion-normal) var(--dd-ease-standard);
}

.dd-alert-fade-enter-from,
.dd-alert-fade-leave-to {
  opacity: 0;
}

// 分页条：与定时任务页/订阅管理页一致的间距收敛
.pagination-bar {
  margin-top: 14px; display: flex; justify-content: space-between; align-items: center; padding: 0 4px;
}

.pagination-total {
  font-size: 13px; color: var(--el-text-color-secondary);
}

.notification-card__actions > * {
  flex: 1 1 calc(50% - 4px);
}

:deep(.el-table) {
  // 边框统一走令牌，明暗自动适配（原写死浅灰会在暗色串色）
  --el-table-border-color: var(--el-border-color-lighter);
  .el-table__header-wrapper th { border-bottom: 1px solid var(--el-border-color-light); }
  .el-table__row td { border-bottom: 1px solid var(--el-border-color-lighter); }
  .el-table__cell { padding: 12px 0; }
}

@media (max-width: 768px) {
  .page-header { flex-direction: column; gap: 10px; margin-bottom: 14px; h2 { font-size: 18px; } }
  .toolbar {
    flex-direction: column; align-items: stretch; gap: 10px;
    &__left { flex-direction: column; gap: 10px; }
    &__search { width: 100% !important; }
    &__filter { width: 100% !important; }
    &__right { justify-content: flex-end; }
  }
}

// ===== 入场动画 =====
// 与定时任务页/订阅管理页统一：只对卡片级容器（工具条 / 表格卡 / 移动列表）做克制的淡入上移 + 轻微错落；
// 不给表格每一行或每张移动卡做 stagger。时长走令牌，prefers-reduced-motion 时令牌自动降为 1ms 即等效关闭。
@keyframes dd-notify-rise-in {
  from {
    opacity: 0;
    transform: translateY(12px);
  }
  to {
    opacity: 1;
    transform: translateY(0);
  }
}

.toolbar,
.table-card,
.dd-mobile-list {
  animation: dd-notify-rise-in var(--dd-motion-page) var(--dd-ease-decelerate) both;
}

// 轻微错落：工具条先入，表格卡/移动列表略晚
.table-card,
.dd-mobile-list {
  animation-delay: 60ms;
}
</style>
