<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { ElMessage } from 'element-plus'
import CronInput, { DEFAULT_CRON_DAILY_MIDNIGHT, DEFAULT_CRON_EVERY_MINUTE } from './CronInput.vue'
import StopScheduleInput from './StopScheduleInput.vue'
import TaskLabelPicker from './TaskLabelPicker.vue'
import { mergeTaskLabels, normalizeTaskNameCounts, splitTaskLabels } from '../taskLabels'
import { useResponsive } from '@/composables/useResponsive'
import { taskApi, type TaskGroupSummary } from '@/api/task'
import { envApi, type EnvNameOption } from '@/api/env'
import type { PythonRuntimeInfo } from '@/api/deps'

const props = withDefaults(defineProps<{
  visible: boolean
  task?: any
  prefill?: any
  defaultPythonVersion?: string
  pythonRuntimes?: PythonRuntimeInfo[]
  notificationChannels?: { id: number; name: string; type: string; enabled: boolean; push_scope?: string }[]
  // 系统设置里「日志保留天数」的当前值，只用于「跟随全局」那一档的提示文案。
  // 父组件没传就不显示数字——宁可少说一句，也不能写死一个 7 骗用户（issue #144 / v3.3.2）。
  globalLogRetentionDays?: number
  // 提交在途标记：父组件创建/更新请求期间置位。
  // 弹窗要等请求成功才关，不锁按钮的话连点就会建出多条重复任务。
  submitting?: boolean
}>(), {
  submitting: false
})

const emit = defineEmits<{
  'update:visible': [value: boolean]
  'submit': [data: any]
}>()

const form = ref({
  name: '',
  command: '',
  python_version: '3.12',
  // 这只是 ref 的声明初值：弹窗每次打开都会走下面的 watch 把整个 form 重置一遍，
  // 所以它实际上看不到。取值口径见 CronInput.vue 顶部那两个导出常量。
  cron_expression: DEFAULT_CRON_DAILY_MIDNIGHT,
  task_type: 'cron',
  timeout: 0,
  success_exit_codes: '0',
  random_delay_seconds: null as number | null,
  max_retries: 0,
  retry_interval: 60,
  notify_on_failure: false,
  notify_on_success: false,
  notification_channel_id: null as number | null,
  labels: [] as string[],
  depends_on: null as number | null,
  task_before: '',
  task_after: '',
  allow_multiple_instances: false,
  // null = 跟随全局 log_retention_days，>0 = 这个任务单独保留这么多天
  log_retention_days: null as number | null,
  stop_schedule: '',
  notify_on_abort: false,
  group_name: '',
  // 可用环境变量（#168）：all = 注入全部已启用变量（默认）；selected = 只注入命中下面分组 / 变量名的
  env_scope: 'all' as 'all' | 'selected',
  env_groups: [] as string[],
  env_names: [] as string[],
})

const activeTab = ref('basic')
// 「环境变量」页签的候选（#168）：每次打开弹窗各拉一次，失败静默、不挡保存（留着上一次的清单）
const envGroupOptions = ref<string[]>([])
const envNameOptions = ref<EnvNameOption[]>([])
const internalLabels = ref<string[]>([])
// 标签选择器（#157）：输入框里没回车的字由它在保存前并进 form.labels（commitPending）。
// 输入状态放在它自己身上、随弹窗内容一起销毁；labelPickerKey 每次打开都换一个，兜住「关窗动画没走完又打开」时
// EP 不重建内容的那条缝 —— 否则上一个任务里没回车的字会留到下一个任务、保存时被并进去（#157 N3）。
const labelPickerRef = ref<InstanceType<typeof TaskLabelPicker> | null>(null)
const labelPickerKey = ref(0)
// 「任务分组」下方的已有分组（#157）。清单来自 GET /tasks/groups，每次打开弹窗拉一次（本组件常驻挂载，onMounted 只跑一回）。
// 失败、老面板 404 一律静默：成功过就留着上一次的清单，从没成功过就不显示这一排。
const groupOptions = ref<TaskGroupSummary[]>([])
const randomDelayMode = ref<'inherit' | 'disabled' | 'custom'>('inherit')
// 日志保留天数只有两态：跟随全局（提交 null）/ 自定义天数。
// 刻意不做「永久保留」第三态——那需要哨兵值，清理侧的分组 SQL 会再复杂一档（issue #144 / v3.3.2）。
const logRetentionMode = ref<'inherit' | 'custom'>('inherit')
const { dialogFullscreen } = useResponsive()
const allPythonVersions = ['3.10', '3.11', '3.12']

// Python 版本下拉选项：带上后端算好的 available/message，
// 未安装版本仍展示但会被标注/禁用，避免任务默认或误选到本机不存在的解释器。
const pythonVersionOptions = computed(() => {
  const runtimes = (props.pythonRuntimes || [])
    .filter(item => allPythonVersions.includes(item.version))
    .map(item => ({ version: item.version, available: item.available, message: item.message }))
  // 后端未返回运行时信息时退回全量候选，且默认视为可用（无从判断可用性）
  return runtimes.length > 0
    ? runtimes
    : allPythonVersions.map(version => ({ version, available: true, message: '' }))
})

// 全局保留天数的展示文案：父组件没传（或传了非法值）时返回空串，模板里据此整段不显示数字。
const globalLogRetentionText = computed(() => {
  const days = props.globalLogRetentionDays
  return days && days > 0 ? String(days) : ''
})

// 当前选中版本若未安装，展示后端给的提示，让用户知道该版本不可用及如何处理
const selectedPythonRuntimeMessage = computed(() => {
  const current = pythonVersionOptions.value.find(item => item.version === form.value.python_version)
  return current && !current.available ? current.message : ''
})

function getDefaultPythonVersion() {
  // 默认值优先落在已安装（available）的版本上，不要默认到未安装版本；
  // 单版本 Docker 镜像也只会返回当前镜像支持的小版本。
  const options = pythonVersionOptions.value
  const preferred = props.defaultPythonVersion || ''
  const defaultOption = options.find(item => item.version === preferred)
  if (defaultOption?.available) return defaultOption.version
  const firstAvailable = options.find(item => item.available)
  if (firstAvailable) return firstAvailable.version
  // 全部未安装时退回后端默认/首个候选
  return defaultOption?.version || options[0]?.version || preferred || '3.12'
}

watch(() => props.visible, (val) => {
  if (val && props.task) {
    const { editableLabels, internalLabels: hiddenLabels, groupName } = splitTaskLabels(props.task.labels || [])
    internalLabels.value = hiddenLabels
    const taskRandomDelay = typeof props.task.random_delay_seconds === 'number'
      ? props.task.random_delay_seconds
      : props.task.random_delay_seconds == null
        ? null
        : Number(props.task.random_delay_seconds)
    if (taskRandomDelay == null) {
      randomDelayMode.value = 'inherit'
    } else if (taskRandomDelay <= 0) {
      randomDelayMode.value = 'disabled'
    } else {
      randomDelayMode.value = 'custom'
    }
    // 后端下发的是 *int：null / 缺字段都表示跟随全局，只有 >0 才是任务自己设的天数。
    const taskLogRetention = typeof props.task.log_retention_days === 'number'
      ? props.task.log_retention_days
      : props.task.log_retention_days == null
        ? null
        : Number(props.task.log_retention_days)
    const hasCustomLogRetention = taskLogRetention != null && taskLogRetention > 0
    logRetentionMode.value = hasCustomLogRetention ? 'custom' : 'inherit'
    form.value = {
      name: props.task.name || '',
      command: props.task.command || '',
      python_version: props.task.python_version || getDefaultPythonVersion(),
      cron_expression: props.task.cron_expression || DEFAULT_CRON_EVERY_MINUTE,
      task_type: props.task.task_type || 'cron',
      timeout: props.task.timeout ?? 0,
      success_exit_codes: props.task.success_exit_codes || '0',
      random_delay_seconds: taskRandomDelay,
      max_retries: props.task.max_retries ?? 0,
      retry_interval: props.task.retry_interval ?? 60,
      notify_on_failure: props.task.notify_on_failure ?? false,
      notify_on_success: props.task.notify_on_success ?? false,
      notification_channel_id: props.task.notification_channel_id ?? null,
      labels: editableLabels,
      group_name: groupName,
      depends_on: props.task.depends_on || null,
      task_before: props.task.task_before || '',
      task_after: props.task.task_after || '',
      allow_multiple_instances: props.task.allow_multiple_instances ?? false,
      log_retention_days: hasCustomLogRetention ? taskLogRetention : null,
      stop_schedule: props.task.stop_schedule || '',
      notify_on_abort: props.task.notify_on_abort ?? false,
      // 只有明确是 selected 才算「指定」；老面板没有这个字段时按「全部」回显
      env_scope: props.task.env_scope === 'selected' ? 'selected' : 'all',
      env_groups: Array.isArray(props.task.env_groups) ? [...props.task.env_groups] : [],
      env_names: Array.isArray(props.task.env_names) ? [...props.task.env_names] : [],
    }
  } else if (val) {
    const p = props.prefill
    internalLabels.value = []
    randomDelayMode.value = 'inherit'
    logRetentionMode.value = 'inherit'
    form.value = {
      name: p?.name || '', command: p?.command || '',
      python_version: p?.python_version || getDefaultPythonVersion(),
      // 手动新建（prefill 没带表达式）时预填「每分钟」；从脚本页跳过来的 prefill 带的是「每天 00:00」
      cron_expression: p?.cron_expression || DEFAULT_CRON_EVERY_MINUTE,
      task_type: p?.task_type || 'cron',
      timeout: 0, success_exit_codes: '0', random_delay_seconds: null, max_retries: 0, retry_interval: 60,
      notify_on_failure: false, notify_on_success: false, notification_channel_id: null, labels: [], depends_on: null,
      task_before: '', task_after: '', allow_multiple_instances: false, group_name: '', stop_schedule: '', notify_on_abort: false,
      log_retention_days: null,
      env_scope: 'all', env_groups: [], env_names: [],
    }
  }
  activeTab.value = 'basic'
  if (val) {
    labelPickerKey.value++
    void loadGroupOptions()
    void loadEnvOptions()
  }
})

async function loadGroupOptions() {
  try {
    groupOptions.value = normalizeTaskNameCounts(await taskApi.groups())
  } catch {
    // 静默：留着上一次的清单（见 groupOptions 的说明）
  }
}

// 「环境变量」页签的分组与变量名候选（#168）。两个接口各自失败各自静默，互不影响，也不挡保存：
// 已选中的值即便不在候选里，多选框也照样显示、照样提交。
async function loadEnvOptions() {
  try {
    const res = await envApi.groups()
    envGroupOptions.value = Array.isArray(res.data) ? res.data : []
  } catch {
    // 静默
  }
  try {
    const res = await envApi.names()
    envNameOptions.value = Array.isArray(res.data) ? res.data : []
  } catch {
    // 静默
  }
}

// 后端默认版本或运行时可用性变化时，若正在新建任务则重新计算默认 Python 版本
watch([() => props.defaultPythonVersion, () => props.pythonRuntimes], () => {
  if (props.visible && !props.task && !props.prefill?.python_version) {
    form.value.python_version = getDefaultPythonVersion()
  }
})

watch(() => form.value.task_type, (value) => {
  if (value === 'cron' && !form.value.cron_expression) {
    // 把「定时类型」切走再切回来（且中途把表达式清空了）时的兜底，取值与改动前一致。
    // 刻意保持「每天 00:00」而不是跟手动新建的「每分钟」统一：切回来是个顺手动作，
    // 用户很可能不会再看一眼定时规则就保存，兜底值给「每分钟」等于给他挖坑。
    form.value.cron_expression = DEFAULT_CRON_DAILY_MIDNIGHT
  }
})

watch(randomDelayMode, (mode) => {
  if (mode === 'inherit') {
    form.value.random_delay_seconds = null
    return
  }
  if (mode === 'disabled') {
    form.value.random_delay_seconds = 0
    return
  }
  if (form.value.random_delay_seconds == null || form.value.random_delay_seconds <= 0) {
    form.value.random_delay_seconds = 60
  }
})

watch(logRetentionMode, (mode) => {
  // 切回「跟随全局」必须显式写 null：提交走的是 { ...form.value } 全量对象，
  // 不置空就会把上次填的天数一起发出去，用户以为改回跟随全局了、其实没有。
  // 反过来切到「自定义」也要给个初值，否则 el-input-number 空着时提交出来是 undefined，
  // JSON.stringify 会把整个键丢掉，后端压根收不到这次修改（issue #144 / v3.3.2）。
  if (mode === 'inherit') {
    form.value.log_retention_days = null
    return
  }
  if (form.value.log_retention_days == null || form.value.log_retention_days <= 0) {
    // 没拿到全局值时退回 7，与后端 log_retention_days 的注册默认值一致；
    // 这只是表单的起手值，用户改不改都不影响「跟随全局」的语义。
    form.value.log_retention_days = props.globalLogRetentionDays && props.globalLogRetentionDays > 0
      ? props.globalLogRetentionDays
      : 7
  }
})

function handleSubmit() {
  // 上一发还在路上时直接忽略，避免重复提交
  if (props.submitting) {
    return
  }
  if (!form.value.name || !form.value.command) {
    ElMessage.warning('请填写任务名称和执行命令')
    return
  }
  if (randomDelayMode.value === 'custom') {
    if (form.value.random_delay_seconds == null || form.value.random_delay_seconds <= 0) {
      ElMessage.warning('请输入大于 0 的随机延迟秒数')
      return
    }
  }
  if (logRetentionMode.value === 'custom') {
    if (form.value.log_retention_days == null || form.value.log_retention_days <= 0) {
      ElMessage.warning('请输入 1-3650 之间的日志保留天数')
      return
    }
  }
  const successExitCodes = form.value.success_exit_codes
    .trim()
    .split(/[,，\s]+/)
    .filter(Boolean)
  if (successExitCodes.length === 0 || successExitCodes.some(code => !/^\d+$/.test(code) || Number(code) > 255)) {
    ElMessage.warning('成功退出码只能填写 0-255 的整数，多个值请用逗号分隔')
    return
  }
  // 分组名不能带英文逗号（#157）：存储按英文逗号拼接，「京东,日常」读回来会变成分组「京东」加一个普通标签「日常」。
  // 用户可能停在「高级设置」页签，切回「基本信息」让他看得见这一项
  if (form.value.group_name.includes(',')) {
    activeTab.value = 'basic'
    ElMessage.warning('分组名不能包含英文逗号')
    return
  }
  // 标签输入框里没按回车的字在这里并进 form.labels（#157 ①：原来会被静默丢掉、照样提示成功）。
  // 必须在上面各项校验之后、组装请求体之前：emit 是同步的，返回时 form.labels 已经是新值。
  // 有以「分组:」「subscription:」开头的段时它已提示过、原文留在输入框里：切回「基本信息」，中止提交
  if (labelPickerRef.value && !labelPickerRef.value.commitPending()) {
    activeTab.value = 'basic'
    return
  }
  const data = { ...form.value }
  // 提交前统一为英文逗号并去重，后端仍会再次校验，避免异常数据写入数据库。
  data.success_exit_codes = [...new Set(successExitCodes.map(code => String(Number(code))))].join(',')
  if (data.task_type === 'cron') {
    if (!data.cron_expression) return
  } else {
    data.cron_expression = ''
  }
  data.labels = mergeTaskLabels(form.value.labels, internalLabels.value, form.value.group_name)
  // 「全部」时不带切换前残留的选择（后端也会清空），请求体与所见一致
  if (data.env_scope !== 'selected') {
    data.env_groups = []
    data.env_names = []
  }
  if (!data.task_before) data.task_before = ''
  if (!data.task_after) data.task_after = ''
  emit('submit', data)
}
</script>

<template>
  <el-dialog
    :model-value="visible"
    :title="task ? '编辑任务' : '新建任务'"
    width="640px"
    :fullscreen="dialogFullscreen"
    :lock-scroll="false"
    destroy-on-close
    @update:model-value="emit('update:visible', $event)"
  >
    <el-tabs v-model="activeTab">
      <el-tab-pane label="基本信息" name="basic">
        <el-form :model="form" :label-width="dialogFullscreen ? 'auto' : '100px'" :label-position="dialogFullscreen ? 'top' : 'right'">
          <el-form-item label="任务名称" required>
            <el-input v-model="form.name" placeholder="任务名称" />
          </el-form-item>
          <el-form-item label="执行命令" required>
            <div class="advanced-field-block">
              <el-input v-model="form.command" placeholder="如: script.py 或 python3 script.py" />
              <div class="advanced-field-hint">
                支持 task 脚本名 格式,自动根据扩展名选择解释器 (.py/.js/.ts/.sh)
              </div>
            </div>
          </el-form-item>
          <el-form-item label="Python版本">
            <div class="advanced-field-block">
              <el-select v-model="form.python_version" style="width: 100%">
                <el-option
                  v-for="option in pythonVersionOptions"
                  :key="option.version"
                  :label="option.available ? `Python ${option.version}` : `Python ${option.version}（未安装）`"
                  :value="option.version"
                  :disabled="!option.available"
                />
              </el-select>
              <div v-if="selectedPythonRuntimeMessage" class="advanced-field-hint advanced-field-hint--warn">
                {{ selectedPythonRuntimeMessage }}
              </div>
            </div>
          </el-form-item>
          <el-form-item label="定时类型" required>
            <el-select v-model="form.task_type" style="width: 100%">
              <el-option label="常规定时" value="cron" />
              <el-option label="手动运行" value="manual" />
              <el-option label="开机运行" value="startup" />
            </el-select>
          </el-form-item>
          <el-form-item v-if="form.task_type === 'cron'" label="定时规则" required>
            <CronInput v-model="form.cron_expression" />
          </el-form-item>
          <el-form-item v-if="form.task_type === 'cron'" label="定时停止">
            <StopScheduleInput v-model="form.stop_schedule" />
          </el-form-item>
          <el-form-item v-else label="执行说明">
            <div style="font-size: 12px; color: var(--dd-text-2); line-height: 1.7">
              <template v-if="form.task_type === 'manual'">
                不自动调度，仅在你手动点击“运行”或批量运行时执行。
              </template>
              <template v-else>
                每天首次启动面板时自动执行一次；当天再次重启不会重复自动执行，手动点击“运行”不受限制。
              </template>
            </div>
          </el-form-item>
          <el-form-item label="标签">
            <!-- 弹窗是 destroy-on-close，选择器（连同输入框里没回车的字）关窗即销毁；:key 见 labelPickerKey 的说明 -->
            <TaskLabelPicker :key="labelPickerKey" ref="labelPickerRef" v-model="form.labels" />
          </el-form-item>
          <el-form-item label="任务分组">
            <div class="group-field">
              <el-input v-model="form.group_name" placeholder="例如 京东 / 日常 / 中国联通" />
              <!-- 已有分组（#157）：点一下填进上面的输入框；输入框本身所见即所得，不存在「没回车就丢」。
                   原生 button 的两条约定同 TaskLabelPicker：type="button"（防输入框里回车触发原生提交、整页刷新）
                   + mousedown.prevent（点它不抢输入框焦点，手机上不收 / 不弹键盘） -->
              <div v-if="groupOptions.length > 0" class="group-options">
                <div class="group-options__title">已有分组</div>
                <div class="group-options__chips" role="group" aria-label="已有分组">
                  <button
                    v-for="group in groupOptions"
                    :key="group.name"
                    type="button"
                    class="group-chip"
                    :aria-pressed="form.group_name.trim() === group.name"
                    :title="`${group.name}（${group.count} 个任务）`"
                    @mousedown.prevent
                    @click="form.group_name = group.name"
                  >
                    <span class="group-chip__text">{{ group.name }}</span>
                  </button>
                </div>
              </div>
            </div>
          </el-form-item>
        </el-form>
      </el-tab-pane>

      <el-tab-pane label="高级设置" name="advanced">
        <el-form :model="form" :label-width="dialogFullscreen ? 'auto' : '120px'" :label-position="dialogFullscreen ? 'top' : 'right'">
          <el-form-item label="超时(秒)">
            <div class="advanced-field-block">
              <el-input-number v-model="form.timeout" :min="0" :max="604800" />
              <div v-if="form.timeout === 0" class="advanced-field-hint advanced-field-hint--warn">
                设置为 0 表示永不超时，任务将持续运行直到手动停止或定时停止。
              </div>
            </div>
          </el-form-item>
          <el-form-item label="成功退出码">
            <div class="advanced-field-block">
              <el-input v-model="form.success_exit_codes" maxlength="128" placeholder="0" />
              <div class="advanced-field-hint">
                普通任务保持 0。只有脚本业务成功但固定返回 1 时才填写 0,1；超时、启动错误和主动停止不会因此变成成功。
              </div>
            </div>
          </el-form-item>
          <el-form-item label="随机延迟">
            <div class="advanced-field-block">
              <el-radio-group v-model="randomDelayMode">
                <el-radio value="inherit">继承系统设置</el-radio>
                <el-radio value="disabled">不随机延迟</el-radio>
                <el-radio value="custom">任务单独设置</el-radio>
              </el-radio-group>
              <div v-if="randomDelayMode === 'custom'" class="advanced-inline-input">
                <el-input-number v-model="form.random_delay_seconds" :min="1" :max="86400" />
                <span>秒</span>
              </div>
              <div class="advanced-field-hint">
                仅当前任务生效。未单独设置时继续沿用系统设置里的全局随机延迟；设置为“不随机延迟”可明确跳过全局规则。
              </div>
            </div>
          </el-form-item>
          <el-form-item label="最大重试次数">
            <el-input-number v-model="form.max_retries" :min="0" :max="10" />
          </el-form-item>
          <el-form-item label="重试间隔(秒)">
            <el-input-number v-model="form.retry_interval" :min="0" :max="3600" />
          </el-form-item>
          <el-form-item label="依赖任务ID">
            <el-input-number v-model="form.depends_on" :min="0" controls-position="right" placeholder="可选" />
          </el-form-item>
          <el-form-item label="失败时通知">
            <el-switch v-model="form.notify_on_failure" />
          </el-form-item>
          <el-form-item label="成功时通知">
            <div class="advanced-field-block">
              <el-switch v-model="form.notify_on_success" />
              <div class="advanced-field-hint">任务执行成功后发送通知</div>
            </div>
          </el-form-item>
          <el-form-item label="通知渠道">
            <div class="advanced-field-block">
              <el-select
                v-model="form.notification_channel_id"
                clearable
                filterable
                placeholder="留空则发送到全部默认推送渠道"
                style="width: 100%"
              >
                <el-option
                  v-for="channel in (props.notificationChannels || [])"
                  :key="channel.id"
                  :label="`${channel.name} (${channel.type}${channel.enabled ? '' : '，已禁用'}${channel.push_scope === 'bound' ? '，仅绑定' : ''})`"
                  :value="channel.id"
                />
              </el-select>
              <div class="advanced-field-hint">
                可绑定单个通知渠道；留空时按广播发送，也就是只发到「默认推送」渠道。
                设为「绑定推送」的渠道不参与广播，必须在这里选中才会收到本任务的通知。
              </div>
            </div>
          </el-form-item>
          <el-form-item label="终止时通知">
            <div class="advanced-field-block">
              <el-switch v-model="form.notify_on_abort" />
              <div class="advanced-field-hint">
                任务被手动停止或定时停止后发送终止通知；终止状态会单独统计，不再算成功或失败。
              </div>
            </div>
          </el-form-item>
          <el-form-item label="允许多实例">
            <el-switch v-model="form.allow_multiple_instances" />
          </el-form-item>
          <el-form-item label="日志保留天数">
            <div class="advanced-field-block">
              <el-radio-group v-model="logRetentionMode">
                <el-radio value="inherit">
                  跟随全局<template v-if="globalLogRetentionText">（当前 {{ globalLogRetentionText }} 天）</template>
                </el-radio>
                <el-radio value="custom">任务单独设置</el-radio>
              </el-radio-group>
              <div v-if="logRetentionMode === 'custom'" class="advanced-inline-input">
                <el-input-number
                  v-model="form.log_retention_days"
                  :min="1"
                  :max="3650"
                  :placeholder="globalLogRetentionText || '天数'"
                />
                <span>天</span>
              </div>
              <div class="advanced-field-hint">
                只影响这个任务的日志清理。每分钟执行一次的任务建议单独调短，否则几天就能堆出上万个日志文件；
                清理会连磁盘上的 .log 文件一起删，删掉找不回来。
              </div>
            </div>
          </el-form-item>
        </el-form>
      </el-tab-pane>

      <!-- 任务级「可用环境变量」（#168）。说明文字要如实：只是减少暴露，不是沙箱 -->
      <el-tab-pane label="环境变量" name="env">
        <el-form :model="form" :label-width="dialogFullscreen ? 'auto' : '100px'" :label-position="dialogFullscreen ? 'top' : 'right'">
          <el-form-item label="可用变量">
            <el-radio-group v-model="form.env_scope">
              <el-radio value="all">全部（默认）</el-radio>
              <el-radio value="selected">指定</el-radio>
            </el-radio-group>
          </el-form-item>
          <template v-if="form.env_scope === 'selected'">
            <el-form-item label="分组">
              <el-select
                v-model="form.env_groups"
                multiple
                filterable
                collapse-tags
                collapse-tags-tooltip
                clearable
                placeholder="选择要注入的变量分组"
                style="width: 100%"
              >
                <el-option v-for="g in envGroupOptions" :key="g" :label="g" :value="g" />
              </el-select>
            </el-form-item>
            <el-form-item label="变量名">
              <!-- 标签沿用变量页的「JD_COOKIE (3)」写法，括号里是全库同名条数 -->
              <el-select
                v-model="form.env_names"
                multiple
                filterable
                collapse-tags
                collapse-tags-tooltip
                clearable
                placeholder="选择要注入的变量名"
                style="width: 100%"
              >
                <el-option
                  v-for="option in envNameOptions"
                  :key="option.name"
                  :label="`${option.name} (${option.count})`"
                  :value="option.name"
                />
              </el-select>
            </el-form-item>
          </template>
          <div class="hooks-help">
            只注入命中任一分组或变量名的已启用变量；config.sh 和面板内置变量照常注入。指定后，脚本里的 DAIDAI_TOKEN 只能用来发通知。
          </div>
          <div class="hooks-help env-scope-warning">
            这不是沙箱：脚本仍能用 ddp 命令或直接读取面板数据文件拿到全部变量。来路不明的脚本不要运行。
          </div>
        </el-form>
      </el-tab-pane>

      <el-tab-pane label="前后置脚本" name="hooks">
        <el-form :model="form" :label-width="dialogFullscreen ? 'auto' : '100px'" :label-position="dialogFullscreen ? 'top' : 'right'">
          <div class="hooks-help">
            当前任务专属的 shell 脚本。前置脚本会在目标脚本执行前运行，后置脚本会在目标脚本结束后运行；如果任务命令传入了参数，这里也可以通过 $1、$2 等读取同一份参数。
          </div>
          <div class="hooks-help">
            <strong>前置脚本里 export 的环境变量，会对随后的目标脚本生效。</strong>
            全局 <code>task_before.sh</code> 同样参与，按执行顺序依次合并；合并结果还会一并交给
            <code>task_after.sh</code>、<code>extra.sh</code> 和后置脚本。后置脚本自身的 export 不回传。
            三条限制请务必留意：
            <ol class="hooks-help-list">
              <li>
                只支持<strong>新增和覆盖</strong>，<strong>unset 不会传导</strong>。想让某个变量变空请写
                <code>export VAR=</code>，不要用 <code>unset VAR</code>。
              </li>
              <li>
                <code>TZ</code> 和所有 <code>DAIDAI_</code> 开头的变量属于面板运行时契约（面板时区、通知渠道绑定、脚本令牌等），
                在前置脚本里改了不会生效，任务日志里会写明被忽略了哪些。
              </li>
              <li>
                <strong>任务命令用了 desi / conc，就别在前置脚本里改同名变量。</strong>
                账号收窄发生在前置脚本<strong>之后</strong>，撞车时 desi / conc 会赢，
                你会遇到「明明 export 了单个值，结果还是全跑」。只想跑第 N 个账号，正规写法是
                <code>task 脚本.py desi 变量名 3</code>（序号从 1 开始，也支持 <code>2 3</code>、<code>1,3</code>、<code>2-5</code>）。
              </li>
            </ol>
          </div>
          <el-form-item label="前置脚本">
            <el-input
              v-model="form.task_before"
              type="textarea"
              :rows="4"
              placeholder="任务执行前运行，例如设置代理、准备环境变量、创建临时目录；这里 export 的变量会传给目标脚本；可用 $1、$2 读取任务参数"
            />
          </el-form-item>
          <el-form-item label="后置脚本">
            <el-input
              v-model="form.task_after"
              type="textarea"
              :rows="4"
              placeholder="任务执行后运行，例如清理临时文件、输出收尾日志、恢复环境；这里 export 的变量不会回传；可用 $1、$2 读取任务参数"
            />
          </el-form-item>
        </el-form>
      </el-tab-pane>
    </el-tabs>

    <template #footer>
      <el-button @click="emit('update:visible', false)">取消</el-button>
      <el-button type="primary" :loading="submitting" :disabled="submitting" @click="handleSubmit">{{ task ? '更新' : '创建' }}</el-button>
    </template>
  </el-dialog>
</template>

<style scoped lang="scss">
.group-field {
  display: flex;
  flex-direction: column;
  gap: 8px;
  width: 100%;
  min-width: 0;
  // el-form-item 的内容区是 32px 行高，标题文字沿用它会被撑得很松
  line-height: 1.5;
}

.group-options__title {
  margin-bottom: 6px;
  font-size: 12px;
  color: var(--dd-text-2);
}

.group-options__chips {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  // 分组通常只有几个；真有很多时限高、内部滚动，不把弹窗撑得很长
  max-height: min(40vh, 240px);
  overflow-y: auto;
}

// 「已有分组」chip：与 TaskLabelPicker.vue 的候选 chip（.label-picker__chip）同一套样式，改一边要同步另一边。
// 多出来的只有当前值对应那一个的高亮（aria-pressed）
.group-chip {
  display: inline-flex;
  align-items: center;
  max-width: 16em;
  height: 28px;
  padding: 0 10px;
  border: 1px solid var(--dd-border-strong);
  // 标签类小表面 → control 档
  border-radius: var(--dd-radius-control);
  background: var(--el-fill-color-blank);
  color: var(--el-text-color-regular);
  font-family: inherit;
  font-size: 12px;
  cursor: pointer;
  transition:
    color var(--dd-motion-fast) var(--dd-ease-standard),
    background-color var(--dd-motion-fast) var(--dd-ease-standard),
    border-color var(--dd-motion-fast) var(--dd-ease-standard);

  &:hover {
    color: var(--dd-accent-ink);
    border-color: color-mix(in srgb, var(--dd-accent) 50%, var(--dd-surface-solid));
    background: color-mix(in srgb, var(--dd-accent) 10%, var(--dd-surface-solid));
  }

  &[aria-pressed='true'] {
    color: var(--dd-accent-ink);
    border-color: var(--dd-accent);
    background: color-mix(in srgb, var(--dd-accent) 10%, var(--dd-surface-solid));
  }

  // 负 offset 把焦点环画在 chip 内侧：限高滚动时外扩的环会被容器裁掉
  &:focus-visible {
    outline: 2px solid var(--dd-accent-ring);
    outline-offset: -1px;
  }
}

.group-chip__text {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.advanced-field-block {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: 6px;
  width: 100%;
  // 阻断 el-form-item__content 的 32px 行高继承，避免下方提示文案被拉出稀疏行距
  line-height: 1.6;
}

.advanced-inline-input {
  display: flex;
  align-items: center;
  gap: 8px;
}

.advanced-field-hint {
  font-size: 12px;
  color: var(--dd-text-2);
  line-height: 1.6;

  &--warn {
    color: var(--dd-warn-ink);
  }
}

.hooks-help {
  margin: 0 0 14px;
  padding: 10px 12px;
  border: 1px solid var(--dd-border);
  // 弹窗内的说明区块 → surface 档
  border-radius: var(--dd-radius-surface);
  background: var(--el-fill-color-lighter);
  color: var(--el-text-color-regular);
  font-size: 13px;
  line-height: 1.7;
}

.hooks-help code {
  padding: 0 4px;
  background: var(--el-fill-color-dark);
  font-family: var(--dd-font-mono);
  font-size: 12px;
}

.hooks-help-list {
  margin: 6px 0 0;
  padding-left: 20px;
}

// 「这不是沙箱」那条警示（#168）：沿用说明区块的版式，换成警告色，免得被当成普通说明扫过去
.env-scope-warning {
  border-color: color-mix(in srgb, var(--dd-warn) 50%, var(--dd-surface-solid));
  background: color-mix(in srgb, var(--dd-warn) 10%, var(--dd-surface-solid));
  color: var(--dd-warn-ink);
}

@media (max-width: 768px) {
  // 触屏点击区抬到 32px（与 TaskLabelPicker 的候选 chip 同一条）
  .group-chip {
    height: 32px;
  }

  .advanced-inline-input {
    flex-wrap: wrap;
  }
}
</style>
