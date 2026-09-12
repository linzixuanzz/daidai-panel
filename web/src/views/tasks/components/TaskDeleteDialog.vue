<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import {
  taskApi,
  type TaskDeletePreview,
  type TaskDeletePreviewTask,
  type TaskScriptDeletionItem,
  type TaskScriptDeletionResult,
} from '@/api/task'
import { useResponsive } from '@/composables/useResponsive'
import { toast } from '@/utils/toast'
import LoadingMotion from '@/components/LoadingMotion.vue'

/**
 * 删除任务的确认弹窗，单删与批量共用（issue #124）。
 *
 * 【为什么不用 ElMessageBox】这里要在确认框里放「同时删除脚本」的勾选框，内容还要随预览在
 * 加载中 / 可勾选 / 降级之间切换，按钮文案跟着勾选联动。往 ElMessageBox 里塞 VNode 全仓没有先例，
 * 所以照同目录 BatchAddLabelDialog.vue 的骨架写，与其它批量弹窗保持同一种交互。
 *
 * 【删哪些文件只听服务端的】前端判断不了文件存不存在、在不在脚本目录内，也看不到分页之外
 * 还有没有别的任务在用同一个脚本。所以打开时先调预览接口：可删 / 保留由服务端判定，
 * 保留原因（detail / note / warnings）原样展示，不在前端另写一份 reason → 文案的映射，
 * 服务端以后新增的 reason 这里不用改也能正常显示。提交时路径也原样回传，前端不拼、不改。
 *
 * 【拿不准就只删任务】预览失败（老后端 404、应用令牌缺 scripts 权限 403、网络错误）、
 * 响应形状不对（演示站对未注册端点兜底回 { data: [] }）、预览还没回来就点了删除，
 * 一律只删任务、不带删脚本开关。勾选框每次打开都默认不勾，也不记住上次的选择。
 */

type DeleteMode = 'single' | 'batch'
type PreviewState = 'loading' | 'ready' | 'unavailable'
// 提交时记下的一个确认项：要删的路径，加上预览里指向它的任务（服务端 scripts[].task_ids）
type ConfirmItem = { path: string; taskIds: number[] }

// 列表折叠阈值：可删的路径先列 5 条；批量时保留项超过 3 条就折叠
const DELETABLE_FOLD_LIMIT = 5
const KEPT_FOLD_LIMIT = 3

const props = defineProps<{
  visible: boolean
  mode: DeleteMode
  taskIds: number[]
  taskName?: string
}>()

const emit = defineEmits<{
  'update:visible': [value: boolean]
  success: [payload: { mode: DeleteMode; taskIds: number[] }]
}>()

const router = useRouter()
const { dialogFullscreen } = useResponsive()

const previewState = ref<PreviewState>('loading')
const preview = ref<TaskDeletePreview | null>(null)
const deleteScripts = ref(false)
const submitting = ref(false)
const showAllDeletable = ref(false)
const showAllKept = ref(false)
// 预览请求序号：每发一次预览、每次关闭弹窗、每次「预览没回来就提交」都 +1，
// 响应回来时序号对不上就整份丢弃。否则关了再开（或换一个任务打开）时，上一轮慢回来的预览
// 会把别的任务的脚本塞进这一轮的勾选框。它不参与渲染，用普通变量就够。
let previewSeq = 0

const isBatch = computed(() => props.mode === 'batch')
const dialogTitle = computed(() => (isBatch.value ? '批量删除任务' : '删除任务'))
const displayTaskName = computed(() => props.taskName || `#${props.taskIds[0] ?? ''}`)

const readyPreview = computed(() => (previewState.value === 'ready' ? preview.value : null))
const previewTasks = computed(() => readyPreview.value?.tasks ?? [])
const previewScripts = computed(() => readyPreview.value?.scripts ?? [])
// 服务端保证 deletable 当且仅当 reason 为空；前端只认 deletable 这一个字段，不自己推断
const deletableItems = computed(() => previewScripts.value.filter(item => item.deletable))
const keptItems = computed(() => previewScripts.value.filter(item => !item.deletable))
const deletablePaths = computed(() => deletableItems.value.map(item => item.path))
// 单删只会解析出一个脚本
const firstDeletable = computed(() => deletableItems.value[0])
const firstDeletableWarnings = computed(() => firstDeletable.value?.warnings ?? [])
// 真正带上删脚本开关的条件：勾了，并且服务端确实判定有可删的脚本
const willDeleteScripts = computed(() => deleteScripts.value && deletablePaths.value.length > 0)

// 「任务已经不存在」只认 script_status=task_not_found，不能按 !found 数：服务端读任务信息出错时
// （数据库锁超时等）也会给 found=false，那些任务其实还在、接着会照常被删，报成「已经不存在，会被跳过」是误导。
const missingTaskCount = computed(
  () => previewTasks.value.filter(task => task.script_status === 'task_not_found').length
)
// 「没能检查」= 服务端读任务信息出错、无法确认的任务：found=false 却又不是 task_not_found（此时 script_status=unresolved）。
// 这些任务可能有能一起删的脚本，只是这次没查成，不能归进「没有可以一起删除的脚本文件」——那是没核实过的结论。
// 任务快照是一条 IN 查询，出错时整批都查不成，所以通常是 allChecksFailed，文案与预览失败同一句。
const checkFailedTaskCount = computed(
  () => previewTasks.value.filter(task => !task.found && task.script_status !== 'task_not_found').length
)
const allChecksFailed = computed(
  () => previewTasks.value.length > 0 && checkFailedTaskCount.value === previewTasks.value.length
)
const runningTaskCount = computed(() => previewTasks.value.filter(task => task.found && task.running).length)
// 「另有 N 个任务没有可以一起删除的脚本」只数命令里没解析出脚本的任务（依赖命令 / Python 模块 /
// 文件不存在 / 命令有误 / 目录外）；解析出了脚本但要保留的，保留列表里已经逐条写了原因，不重复计数。
const noScriptTaskCount = computed(
  () => previewTasks.value.filter(task => task.found && task.script_status !== 'resolved').length
)
// 单删且没有可删脚本时的说明，原样用服务端的 note。解析出了脚本但要保留时 note 为空，原因由保留列表给出。
const singleNote = computed(() => previewTasks.value[0]?.note ?? '')

// 按人数选代词：只有 1 个时写「它们」读着别扭（浏览器实测发现批量只选 1 个任务时主句是「它们的执行日志」）。
// 与服务端 shared 文案同一口径：1 个用「它」，2 个及以上用「它们」。
function pronounFor(count: number) {
  return count > 1 ? '它们' : '它'
}

const runningAlertText = computed(() =>
  isBatch.value
    ? `其中 ${runningTaskCount.value} 个任务正在运行或排队中，删除任务不会中断${pronounFor(runningTaskCount.value)}这次的执行。`
    : '这个任务正在运行或排队中，删除任务不会中断这次执行。'
)

const visibleDeletableItems = computed(() =>
  showAllDeletable.value ? deletableItems.value : deletableItems.value.slice(0, DELETABLE_FOLD_LIMIT)
)
const hiddenDeletableCount = computed(() => deletableItems.value.length - visibleDeletableItems.value.length)
// 单删最多一条保留项，不折叠；批量超过阈值才折叠
const visibleKeptItems = computed(() =>
  !isBatch.value || showAllKept.value ? keptItems.value : keptItems.value.slice(0, KEPT_FOLD_LIMIT)
)
const hiddenKeptCount = computed(() => keptItems.value.length - visibleKeptItems.value.length)

const confirmText = computed(() => {
  if (isBatch.value) {
    return willDeleteScripts.value
      ? `删除任务和 ${deletablePaths.value.length} 个脚本`
      : `删除 ${props.taskIds.length} 个任务`
  }
  return willDeleteScripts.value ? '删除任务和脚本' : '删除'
})

watch(
  () => props.visible,
  (visible) => {
    if (visible) {
      void loadPreview()
    } else {
      // 关闭时作废还没回来的预览
      previewSeq++
    }
  }
)

function close() {
  emit('update:visible', false)
}

// 只认「服务端真的查过」的形状：checked 哨兵为 true，且 tasks / scripts 都是数组。
// 演示站对没注册的端点兜底回 200 + { data: [], total: 0, ... }，没有 checked；
// 不在这里拦下，它会被静默显示成「没有可以一起删除的脚本」。
function isCheckedPreview(data: unknown): data is TaskDeletePreview {
  if (!data || typeof data !== 'object') return false
  const candidate = data as Partial<TaskDeletePreview>
  return candidate.checked === true && Array.isArray(candidate.tasks) && Array.isArray(candidate.scripts)
}

async function loadPreview() {
  const seq = ++previewSeq
  // 每次打开都从头来：勾选框默认不勾、不记住上次的选择（刻意不写 localStorage），折叠也收起
  deleteScripts.value = false
  showAllDeletable.value = false
  showAllKept.value = false
  preview.value = null
  previewState.value = 'loading'
  try {
    const res = await taskApi.deletePreview([...props.taskIds])
    if (seq !== previewSeq || !props.visible) return
    const data: unknown = res?.data
    if (isCheckedPreview(data)) {
      preview.value = data
      previewState.value = 'ready'
    } else {
      previewState.value = 'unavailable'
    }
  } catch {
    // 预览失败不弹错误提示：弹窗里如实写「没能检查脚本文件」，删除照常可用、只删任务
    if (seq !== previewSeq || !props.visible) return
    previewState.value = 'unavailable'
  }
}

async function handleConfirm() {
  // 关掉之后不再受理：关闭动画期间确认按钮还在、也不再是 loading，连点的第二下会对着刚删掉的任务
  // 再发一次删除请求，平白多弹一条「已经不存在」。
  if (submitting.value || !props.visible) return
  const ids = [...props.taskIds]
  const firstId = ids[0]
  if (firstId === undefined) {
    toast.warning('请先选择任务')
    return
  }
  const mode = props.mode
  const withScripts = willDeleteScripts.value
  // 确认项连同来源任务在提交前拷一份：结果要对照用户这次确认的内容来报，靠任务 id 认「文件已经不存在」
  // （见 isAlreadyGone），不能等响应回来再去读随时可能变化的预览。
  const confirmItems: ConfirmItem[] = withScripts
    ? deletableItems.value.map(item => ({ path: item.path, taskIds: [...item.task_ids] }))
    : []
  // 路径原样回传服务端给的字符串，前端不拼、不改；服务端只删「重新判定可删 且 在这张清单里」的文件
  const confirmPaths = confirmItems.map(item => item.path)
  // 预览还没回来就点了删除：这次只删任务，并作废那份预览，免得它回来后又把勾选框渲染出来。
  // 顺手切到「没能检查」，否则删除失败、弹窗留着时加载图标会一直转下去。
  if (previewState.value === 'loading') {
    previewSeq++
    previewState.value = 'unavailable'
  }
  submitting.value = true
  try {
    if (mode === 'single') {
      const res = await taskApi.delete(
        firstId,
        withScripts ? { deleteScript: true, confirmScriptPath: confirmPaths[0] } : undefined
      )
      notifyResult(mode, withScripts, confirmItems, res?.scripts, ids.length)
    } else {
      const res = await taskApi.batch(
        ids,
        'delete',
        withScripts ? { delete_scripts: true, confirm_script_paths: confirmPaths } : undefined
      )
      notifyResult(mode, withScripts, confirmItems, res?.scripts, res?.count ?? ids.length)
    }
    emit('success', { mode, taskIds: ids })
    close()
  } catch (err: any) {
    // 单删遇到 404：任务已经在别处被删掉了（另一个标签页、APP、订阅同步），当前列表还没刷新。
    // 重试只会一直 404，弹窗留着就是一个点了必然失败的按钮；按「要删的已经没了」收尾：
    // emit success 让父组件刷新列表、摘掉选中项，再关弹窗。批量删不存在的 id 服务端回 200，走不到这里。
    if (mode === 'single' && err?.response?.status === 404) {
      toast.info('这个任务已经不存在，列表已刷新')
      emit('success', { mode, taskIds: ids })
      close()
      return
    }
    // 弹窗不关，用户可以改主意（比如取消勾选）后重试；403 等情况原样显示服务端文案
    toast.error(err?.response?.data?.error || '删除失败')
  } finally {
    submitting.value = false
  }
}

// 提示一律按服务端返回写，不按勾选状态写：
// - 没勾：只报任务删了；
// - 勾了但响应里没有 scripts：后端是旧版、根本不认这个开关，脚本多半还在，要明说；
// - 勾了：只对「用户确认要删、结果却没删掉」的脚本弹 warning；预览里就说明会保留的，不再重复提醒。
//   「文件已经不存在」不算没删掉：用户要的结果就是这个文件没了，只在成功提示里顺带说一句。
//   不能弹 warning 挂「去处理」——文件都不在了，脚本页必然打不开它，点了只会再弹一条错误。
function notifyResult(
  mode: DeleteMode,
  withScripts: boolean,
  confirmItems: ConfirmItem[],
  scripts: TaskScriptDeletionResult | undefined,
  count: number
) {
  if (!withScripts) {
    toast.success(mode === 'batch' ? `已删除 ${count} 个任务` : '任务已删除')
    return
  }
  if (!scripts) {
    toast.warning('任务已删除，但服务端没有返回脚本处理结果，脚本文件可能没有删除，请到脚本管理确认。')
    return
  }
  const deleted = scripts.deleted ?? []
  const skipped = scripts.skipped ?? []
  const resultTasks = scripts.tasks ?? []
  const deletedPaths = new Set(deleted.map(item => item.path))
  const gonePaths: string[] = []
  // 没删掉的 = 用户确认过、既不在 deleted 里也不是已经不存在的路径。大多能在 skipped 里找到服务端给的原因；
  // 找不到的也要算上（例如确认之后任务命令被改成了别的脚本，原来那个文件压根不在结果里），
  // 否则会把一个没删的文件报成「已删除」。
  const notDeleted: { path: string; detail: string }[] = []
  for (const item of confirmItems) {
    if (deletedPaths.has(item.path)) continue
    if (isAlreadyGone(item, skipped, resultTasks)) {
      gonePaths.push(item.path)
    } else {
      notDeleted.push(skipped.find(entry => entry.path === item.path) ?? { path: item.path, detail: '' })
    }
  }
  const first = notDeleted[0]
  if (!first) {
    toast.success(achievedMessage(mode, count, deleted, gonePaths))
    return
  }
  const targetPath = first.path
  const reasonText = first.detail ? `${targetPath}：${first.detail}` : targetPath
  const message = notDeleted.length > 1
    ? `任务已删除；${notDeleted.length} 个脚本文件没有删除，如 ${reasonText}`
    : `任务已删除；1 个脚本文件没有删除：${reasonText}`
  // 带操作的提示默认停留 8 秒；脚本页支持 ?file= 直接打开这个文件
  toast.warning(message, {
    action: {
      text: '去处理',
      handler: () => router.push({ path: '/scripts', query: { file: targetPath } }),
    },
  })
}

// 确认过、但不在 deleted 里的路径，是不是「文件已经不存在」。按顺序判断：
// 1. skipped 里有它：以服务端 Execute 的判定为准，只有 reason=not_found 才算（服务端判定之后、真正删之前文件没了，
//    或同一任务的两个删除请求并发，后到的一方）。reason 不是 not_found 时，说明服务端执行时文件还在，或状态未知
//    （check_failed 这类），按「没有删除」报，并带上服务端给的原因，不再往下看 tasks：否则批量里另一个同来源任务的命令
//    恰好在弹窗期间被改成以这条路径开头、却跑不起来的写法时，第 2 条会把「文件还在、因仍被别的任务使用而保留」
//    报成「已经不存在」，服务端给的原因和「去处理」都被盖掉。
//    skipped 的 path 与确认路径同一个来源（服务端的脚本组路径），按原文比。
// 2. skipped 里没有它，而预览里指向它的任务这次在 tasks 里是 script_status=not_found，且 script_path 对得上：
//    弹窗开着时文件就在别处被删了，服务端重新解析已经找不到它，脚本组根本没建。这种更常见，只看 skipped 会漏。
//
// 第 2 条的 script_path 不能按原文比。它取自命令里的写法（服务端 taskScriptHintPath），确认路径取自磁盘上的
// 真实文件名，两者有两种正常的差异：
//   - 大小写：Windows 上命令写 task jd_sign.py，磁盘上是 JD_Sign.py；
//   - 后面带着参数：解析器会把「脚本 + 后面带脚本扩展名的参数」也当成候选路径去试，全都找不到时报最长的那个，
//     task main.py extra.py 报 main.py extra.py，python3 run.py --config conf.py 报 run.py --config conf.py。
// 所以忽略大小写，并允许 script_path 以「确认路径 + 空格」开头；空格不能省，否则 a.py.bak.py 也会被当成 a.py。
//
// 也不能只按任务 id 认：确认之后任务命令被改成指向另一个不存在的文件时，这个任务同样是 not_found，可原来确认的
// 文件其实还在，得照常报「没有删除」并给「去处理」。放宽比较后，只有 skipped 里没有这条路径（这次没有任何任务再解析到它，
// 前端无从得知文件还在不在）、命令又恰好被改成只差大小写、或以同一路径开头却跑不起来的写法时才会认错，
// 得正好发生在弹窗开着的这几秒里，可以接受。
function isAlreadyGone(item: ConfirmItem, skipped: TaskScriptDeletionItem[], tasks: TaskDeletePreviewTask[]) {
  const kept = skipped.find(entry => entry.path === item.path)
  if (kept) return kept.reason === 'not_found'
  const confirmed = item.path.toLowerCase()
  return tasks.some(task => {
    if (task.script_status !== 'not_found' || !item.taskIds.includes(task.id)) return false
    const hint = (task.script_path ?? '').toLowerCase()
    return hint === confirmed || hint.startsWith(`${confirmed} `)
  })
}

// 确认过的脚本全部删掉、或已经不存在时的成功提示。已经不存在的顺带说一句：
// 1 个写路径，多个只写个数，免得一长串路径把提示条撑满。
function achievedMessage(
  mode: DeleteMode,
  count: number,
  deleted: TaskScriptDeletionItem[],
  gonePaths: string[]
) {
  const goneText = gonePaths.length === 1 ? `${gonePaths[0]} 已经不存在` : `${gonePaths.length} 个脚本文件已经不存在`
  if (mode === 'single') {
    // 单删只有一个确认路径：要么删掉了，要么已经不在了
    return deleted.length === 0 && gonePaths.length > 0
      ? `任务已删除；${goneText}`
      : `已删除任务和脚本 ${deleted[0]?.path ?? ''}`
  }
  const head = deleted.length > 0 || gonePaths.length === 0
    ? `已删除 ${count} 个任务和 ${deleted.length} 个脚本文件`
    : `已删除 ${count} 个任务`
  return gonePaths.length > 0 ? `${head}；${goneText}` : head
}
</script>

<template>
  <el-dialog
    :model-value="visible"
    :title="dialogTitle"
    width="520px"
    :fullscreen="dialogFullscreen"
    :close-on-click-modal="false"
    :close-on-press-escape="!submitting"
    :show-close="!submitting"
    destroy-on-close
    @update:model-value="emit('update:visible', $event)"
  >
    <div class="task-delete">
      <!-- ① 主句。原来的确认框没提执行日志，但后端删任务时确实会连日志一起删 -->
      <p class="task-delete__lead">
        <template v-if="isBatch">
          确定删除选中的 {{ taskIds.length }} 个任务吗？{{ pronounFor(taskIds.length) }}的执行日志会一起删除，删除后无法恢复。
        </template>
        <template v-else>
          确定删除任务「{{ displayTaskName }}」吗？它的执行日志会一起删除，删除后无法恢复。
        </template>
      </p>
      <p v-if="isBatch && missingTaskCount > 0" class="task-delete__muted">
        其中 {{ missingTaskCount }} 个任务已经不存在，会被跳过。
      </p>
      <!-- 只有一部分任务没能检查时才单独说；整批都没能检查时，脚本区直接写「没能检查脚本文件」，这里不重复 -->
      <p v-if="isBatch && checkFailedTaskCount > 0 && !allChecksFailed" class="task-delete__muted">
        其中 {{ checkFailedTaskCount }} 个任务没能检查脚本文件，只删除任务。
      </p>

      <!-- ② 删除任务不会停掉正在跑的进程（与改动前一致），先说清楚，免得用户以为删了就停了 -->
      <el-alert
        v-if="runningTaskCount > 0"
        type="info"
        :closable="false"
        show-icon
        class="task-delete__alert"
        :title="runningAlertText"
      />

      <!-- ③ 脚本文件：按预览的三种状态渲染，勾选框只在服务端判定有可删脚本时出现 -->
      <div class="task-delete__scripts">
        <LoadingMotion
          v-if="previewState === 'loading'"
          variant="spinner"
          size="sm"
          tone="neutral"
          :stacked="false"
          label="正在检查这些任务用到的脚本文件…"
        />

        <p v-else-if="previewState === 'unavailable'" class="task-delete__muted">
          没能检查脚本文件，这次只删除任务，脚本文件不会动。
        </p>

        <template v-else>
          <template v-if="deletableItems.length > 0">
            <el-checkbox v-model="deleteScripts" class="task-delete__check" :disabled="submitting">
              <template v-if="isBatch">同时删除 {{ deletableItems.length }} 个脚本文件</template>
              <template v-else>
                同时删除脚本文件 <code class="task-delete__path">{{ firstDeletable?.path }}</code>
              </template>
            </el-checkbox>

            <div class="task-delete__indent">
              <template v-if="isBatch">
                <ul class="task-delete__list">
                  <li v-for="item in visibleDeletableItems" :key="item.path">
                    <code class="task-delete__path">{{ item.path }}</code>
                    <p v-for="(warning, index) in item.warnings" :key="index" class="task-delete__warning">
                      {{ warning }}
                    </p>
                  </li>
                </ul>
                <el-button
                  v-if="hiddenDeletableCount > 0"
                  link
                  type="primary"
                  size="small"
                  @click="showAllDeletable = true"
                >
                  还有 {{ hiddenDeletableCount }} 个，展开
                </el-button>
              </template>
              <template v-else>
                <p v-for="(warning, index) in firstDeletableWarnings" :key="index" class="task-delete__warning">
                  {{ warning }}
                </p>
              </template>
              <p v-if="deleteScripts" class="task-delete__danger">
                脚本文件会从脚本目录里永久删除，面板里无法撤销。只删除列出的文件，{{ pronounFor(deletableItems.length) }}引用的其它文件不受影响。
              </p>
            </div>

            <p v-if="isBatch && noScriptTaskCount > 0" class="task-delete__muted">
              另有 {{ noScriptTaskCount }} 个任务没有可以一起删除的脚本文件（依赖命令、Python 模块、文件不存在或命令有误）。
            </p>
          </template>

          <template v-else>
            <!-- 整批都没能检查（服务端读任务信息出错）：沿用预览失败那一句。不能说「没有可以一起删除的脚本文件」，
                 这些任务可能有能删的脚本，只是这次没查成 -->
            <p v-if="isBatch && allChecksFailed" class="task-delete__muted">
              没能检查脚本文件，这次只删除任务，脚本文件不会动。
            </p>
            <!-- 有任务没能检查时上面已经单独说了，这里只替其余的任务下结论 -->
            <p v-else-if="isBatch" class="task-delete__muted">
              {{ checkFailedTaskCount > 0 ? '其余任务' : '选中的任务' }}没有可以一起删除的脚本文件，只删除任务。
            </p>
            <p v-else-if="singleNote" class="task-delete__muted">{{ singleNote }}</p>
            <p v-else-if="keptItems.length === 0" class="task-delete__muted">
              这个任务没有可以一起删除的脚本文件，只删除任务。
            </p>
          </template>

          <!-- 保留项：detail 是服务端文案，原样展示；v1 不提供强制删除 -->
          <div v-if="keptItems.length > 0" class="task-delete__kept">
            <p class="task-delete__subtitle">以下脚本文件会保留：</p>
            <ul class="task-delete__list">
              <li v-for="item in visibleKeptItems" :key="item.path">
                <code class="task-delete__path">{{ item.path }}</code><template v-if="item.detail">：{{ item.detail }}</template>
              </li>
            </ul>
            <el-button v-if="hiddenKeptCount > 0" link type="primary" size="small" @click="showAllKept = true">
              查看全部 {{ keptItems.length }} 个
            </el-button>
          </div>
        </template>
      </div>
    </div>

    <template #footer>
      <el-button :disabled="submitting" @click="close">取消</el-button>
      <el-button type="danger" :loading="submitting" @click="handleConfirm">{{ confirmText }}</el-button>
    </template>
  </el-dialog>
</template>

<style scoped>
.task-delete__lead {
  margin: 0;
  font-size: 14px;
  line-height: 1.7;
  color: var(--el-text-color-primary);
}

.task-delete__muted {
  margin: 8px 0 0;
  font-size: 13px;
  line-height: 1.6;
  color: var(--el-text-color-secondary);
}

.task-delete__alert {
  margin-top: 12px;
}

.task-delete__scripts {
  margin-top: 14px;
  padding-top: 12px;
  border-top: 1px solid var(--el-border-color-lighter);
}

/* 区块里的第一行紧贴分隔线下方，不再叠加各自的上边距 */
.task-delete__scripts > :first-child {
  margin-top: 0;
}

/* EP 的 el-checkbox 默认 height: 32px、white-space: nowrap、margin-right: 30px，
   长路径会被撑出弹窗；这里放开换行，并让勾选框与第一行文字顶端对齐 */
.task-delete__check {
  display: flex;
  align-items: flex-start;
  height: auto;
  margin-right: 0;
  white-space: normal;
}

.task-delete__check :deep(.el-checkbox__input) {
  margin-top: 4px;
}

.task-delete__check :deep(.el-checkbox__label) {
  line-height: 1.6;
  white-space: normal;
  word-break: break-all;
}

/* 与勾选框的文字左对齐：14px 的框 + 8px 的 label 内边距 */
.task-delete__indent {
  padding-left: 22px;
}

.task-delete__list {
  margin: 6px 0 0;
  padding: 0;
  list-style: none;
}

.task-delete__list li {
  margin-top: 4px;
  font-size: 13px;
  line-height: 1.6;
  color: var(--el-text-color-regular);
  word-break: break-all;
}

.task-delete__path {
  padding: 1px 4px;
  font-size: 12px;
  color: var(--el-text-color-primary);
  background: var(--el-fill-color-light);
  border-radius: var(--dd-radius-control);
  word-break: break-all;
}

.task-delete__warning {
  margin: 2px 0 0;
  font-size: 12px;
  line-height: 1.5;
  color: var(--el-color-warning);
}

.task-delete__danger {
  margin: 8px 0 0;
  font-size: 12px;
  line-height: 1.5;
  color: var(--el-color-danger);
}

.task-delete__kept {
  margin-top: 12px;
}

.task-delete__subtitle {
  margin: 0;
  font-size: 13px;
  line-height: 1.6;
  color: var(--el-text-color-secondary);
}
</style>
