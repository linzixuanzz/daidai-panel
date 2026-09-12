<script setup lang="ts">
/**
 * 动效组件预览页（内部用，不进侧栏菜单）。
 *
 * 访问方式：手动输入 /dev/motion。
 * 它不在 MainLayout 的 workspaceItems / adminItems 里，也不在 router 的
 * routePreloaders 里，所以既不会出现在导航中，也不会被空闲预加载拉下来——
 * 不访问就不下载这个 chunk。
 *
 * 存在的意义：七类动效组件的状态散落在 15 个业务页里，逐个点过去既慢又容易漏。
 * 这里把它们的【全部状态和全部动效】排在一页，改完动效可以一次性看完，
 * 也方便用浏览器做实测截图。
 *
 * 每一块都必须是【可交互】的：只摆静态样子没有意义，动效要能被真的触发出来。
 */
import { ref, computed } from 'vue'
import { toast } from '@/utils/toast'
import { ElMessage, ElMessageBox } from 'element-plus'
import DdBadge from '@/components/ui/DdBadge.vue'
import DdSplitButton from '@/components/ui/DdSplitButton.vue'
import type { SplitButtonItem } from '@/components/ui/DdSplitButton.vue'
import DdMultiSelect from '@/components/ui/DdMultiSelect.vue'
import DdDateRangePicker from '@/components/ui/DdDateRangePicker.vue'

// ===== 1. 角标 =====
const badgeCount = ref(3)
const badgeBig = ref(120)

// ===== 4. Select =====
const selectValue = ref('')
const selectOptions = [
  { value: 'pending', label: '待处理' },
  { value: 'running', label: '进行中' },
  { value: 'done', label: '已完成' },
]

// ===== 5. Multi-select =====
const multiValue = ref<string[]>(['design'])
const multiOptions = [
  { value: 'design', label: '设计' },
  { value: 'writing', label: '写作' },
  { value: 'data', label: '数据' },
  { value: 'ops', label: '运维' },
  { value: 'qa', label: '测试' },
  { value: 'pm', label: '产品' },
]
const multiLimited = ref<string[]>([])

// ===== 6. Split Button =====
const taskRunning = ref(false)
/**
 * 动态主操作的范例：运行中时主体变「停止」，且菜单里不能再出现「停止」。
 * 这正是 SplitButtonItem 的 visible 字段要解决的问题——把互斥关系写在同一份数组里，
 * 而不是在调用点用 v-if 拆成两套按钮。
 */
const taskItems = computed<SplitButtonItem[]>(() => [
  { key: 'run', label: '立即运行', visible: taskRunning.value },
  { key: 'stop', label: '停止运行', visible: !taskRunning.value },
  { key: 'log', label: '查看日志' },
  { key: 'edit', label: '编辑任务' },
  { key: 'delete', label: '删除', danger: true, divided: true },
])

const staticItems: SplitButtonItem[] = [
  { key: 'schedule', label: '定时发布' },
  { key: 'draft', label: '保存草稿' },
  { key: 'discard', label: '丢弃更改', danger: true, divided: true },
]

// ===== 7. Date Picker =====
const dateRange = ref<[Date, Date] | null>(null)
const dateRangeInline = ref<[Date, Date] | null>(null)

function onSplitCommand(key: string) {
  if (key === 'run') taskRunning.value = true
  else if (key === 'stop') taskRunning.value = false
  ElMessage.info(`菜单项：${key}`)
}
</script>

<template>
  <div class="motion-page dd-scroll-page">
    <header class="motion-header">
      <h1>动效组件预览</h1>
      <p>
        内部验收页，不进侧栏菜单。七类动效组件的全部状态与交互都在这里，
        每一块都可以直接点出来看。视觉基调遵循「全屏纯扁平直角」——
        没有圆角、没有阴影、没有装饰渐变，层次靠 1px 边框与底色反差，
        动效时长统一吃 <code>--dd-motion-*</code> 令牌，系统开启「减少动态效果」时自动降级。
      </p>
    </header>

    <!-- ============ 1. 角标 ============ -->
    <section class="motion-section">
      <h2><span class="motion-section__no">01</span> 角标提示 Badge</h2>
      <p class="motion-section__desc">
        数字变化时做翻牌：新值从上方落下、旧值向下退出。等宽数字，跳字时宽度不抖。
      </p>

      <div class="motion-demo">
        <div class="motion-row">
          <span class="motion-label">语义级别</span>
          <span class="motion-chip">危险 <DdBadge :value="3" level="danger" /></span>
          <span class="motion-chip">警告 <DdBadge :value="7" level="warning" /></span>
          <span class="motion-chip">进行中 <DdBadge :value="2" level="primary" /></span>
          <span class="motion-chip">中性计数 <DdBadge :value="18" level="info" /></span>
        </div>

        <div class="motion-row">
          <span class="motion-label">小圆点</span>
          <span class="motion-chip">有新动态 <DdBadge dot level="danger" /></span>
          <span class="motion-chip">进行中 <DdBadge dot level="primary" /></span>
        </div>

        <div class="motion-row">
          <span class="motion-label">数字翻牌</span>
          <el-button size="small" @click="badgeCount = Math.max(0, badgeCount - 1)">−1</el-button>
          <DdBadge :value="badgeCount" level="danger" title="待处理" />
          <el-button size="small" @click="badgeCount += 1">+1</el-button>
          <span class="motion-hint">减到 0 时整个角标会淡出消失</span>
        </div>

        <div class="motion-row">
          <span class="motion-label">超出上限</span>
          <DdBadge :value="badgeBig" :max="99" level="danger" />
          <el-button size="small" @click="badgeBig = badgeBig > 99 ? 8 : 120">
            在 8 / 120 之间切换
          </el-button>
          <span class="motion-hint">超过 max 显示 99+，避免把菜单项撑开</span>
        </div>

        <div class="motion-row">
          <span class="motion-label">值为 0</span>
          <span class="motion-chip">默认（不渲染）<DdBadge :value="0" /></span>
          <span class="motion-chip">show-zero <DdBadge :value="0" level="info" show-zero /></span>
          <span class="motion-hint">提醒型角标该消失；统计型「失败 0」是有信息量的</span>
        </div>
      </div>
    </section>

    <!-- ============ 2. 轻提示 ============ -->
    <section class="motion-section">
      <h2><span class="motion-section__no">02</span> 轻提示 Toast</h2>
      <p class="motion-section__desc">
        左缘 3px 语义色实心条替代「浅底 + 同色图标」的弱区分；
        停留时长按「这条信息要不要读完」分档，而不是一律 3 秒。
      </p>

      <div class="motion-demo">
        <div class="motion-row">
          <span class="motion-label">四种语义</span>
          <el-button size="small" type="success" @click="toast.success('任务已保存')">
            成功 2.5s
          </el-button>
          <el-button size="small" @click="toast.info('正在后台同步')">信息 3s</el-button>
          <el-button size="small" type="warning" @click="toast.warning('日志目录占用已超过 90% 磁盘空间，建议清理旧日志')">
            警告 4.5s
          </el-button>
          <el-button size="small" type="danger" @click="toast.error('执行失败：command not found: python3')">
            错误 6s
          </el-button>
        </div>

        <div class="motion-row">
          <span class="motion-label">长文本换行</span>
          <el-button
            size="small"
            @click="toast.error('执行失败：拉取远端仓库超时，请检查网络代理设置以及该仓库是否需要鉴权凭据')"
          >
            触发一条长报错
          </el-button>
          <span class="motion-hint">EP 默认 line-height:1 会让两行糊在一起，这里改成 1.5</span>
        </div>

        <div class="motion-row">
          <span class="motion-label">相同内容合并</span>
          <el-button size="small" @click="toast.error('批量操作中有一项失败', { grouping: true })">
            连点几次看右侧计数
          </el-button>
          <span class="motion-hint">批量操作循环报错时用，否则会刷出一屏一样的提示</span>
        </div>

        <div class="motion-row">
          <span class="motion-label">不自动关闭</span>
          <el-button size="small" @click="toast.warning('这条需要你手动关掉', { duration: 0 })">
            duration: 0
          </el-button>
          <span class="motion-hint">传 0 时会自动带上关闭按钮，否则会永久占住页面顶部</span>
        </div>
      </div>
    </section>

    <!-- ============ 3. 带操作的轻提示 ============ -->
    <section class="motion-section">
      <h2><span class="motion-section__no">03</span> 带操作的轻提示</h2>
      <p class="motion-section__desc">
        把「打断式弹窗」降级成「非阻塞提示 + 一个后续入口」。
        按钮颜色继承提示条的语义色，成功/失败各自成套；hover 反色，不做位移。
      </p>

      <div class="motion-demo">
        <div class="motion-row">
          <span class="motion-label">查看详情</span>
          <el-button
            size="small"
            @click="
              toast.error('测试发送失败', {
                action: {
                  text: '查看详情',
                  handler: () =>
                    ElMessageBox.alert('HTTP 401：Bot Token 无效或已被吊销', '测试发送失败', {
                      confirmButtonText: '知道了',
                      type: 'error',
                    }).catch(() => {}),
                },
              })
            "
          >
            通知渠道测试失败
          </el-button>
          <span class="motion-hint">替代原来一失败就弹 alert 挡住整个界面</span>
        </div>

        <div class="motion-row">
          <span class="motion-label">立即刷新</span>
          <el-button
            size="small"
            @click="
              toast.warning('重启超时，面板可能已经起来了', {
                action: { text: '立即刷新', handler: () => ElMessage.info('这里会 location.reload()') },
              })
            "
          >
            重启超时
          </el-button>
          <span class="motion-hint">替代「请手动刷新页面」——说了动作却让用户自己按 F5</span>
        </div>

        <div class="motion-row">
          <span class="motion-label">成功后的后续入口</span>
          <el-button
            size="small"
            @click="
              toast.withAction('已导出 12 个任务', {
                text: '查看',
                handler: () => ElMessage.success('跳转到任务列表'),
              })
            "
          >
            导出完成
          </el-button>
        </div>

        <div class="motion-row">
          <span class="motion-label">重试</span>
          <el-button
            size="small"
            @click="
              toast.error('加载依赖列表失败', {
                action: { text: '重试', handler: () => ElMessage.success('重新请求中…') },
              })
            "
          >
            加载失败
          </el-button>
        </div>
      </div>
    </section>

    <!-- ============ 4. Select ============ -->
    <section class="motion-section">
      <h2><span class="motion-section__no">04</span> Select 单选</h2>
      <p class="motion-section__desc">
        面板展开从 <code>scaleY</code> 卷帘改成淡入 + 轻微上移，与全站语汇一致；
        朝上弹出时位移自动反向。箭头翻转与选项 hover 都吃令牌，不再是 EP 默认的 all 0.3s。
      </p>

      <div class="motion-demo">
        <div class="motion-row">
          <span class="motion-label">基础</span>
          <el-select v-model="selectValue" placeholder="请选择状态" style="width: 220px">
            <el-option v-for="o in selectOptions" :key="o.value" :label="o.label" :value="o.value" />
          </el-select>
          <span class="motion-hint">当前值：{{ selectValue || '未设置' }}</span>
        </div>

        <div class="motion-row">
          <span class="motion-label">可筛选 + 可清空</span>
          <el-select
            v-model="selectValue"
            placeholder="输入关键字筛选"
            filterable
            clearable
            style="width: 220px"
          >
            <el-option v-for="o in selectOptions" :key="o.value" :label="o.label" :value="o.value" />
          </el-select>
        </div>

        <div class="motion-row">
          <span class="motion-label">禁用</span>
          <el-select model-value="pending" disabled style="width: 220px">
            <el-option label="待处理" value="pending" />
          </el-select>
        </div>
      </div>
    </section>

    <!-- ============ 5. Multi-select ============ -->
    <section class="motion-section">
      <h2><span class="motion-section__no">05</span> Multi-select 多选</h2>
      <p class="motion-section__desc">
        EP 自带标签化、单独删除和一键清空，缺的是「已选几项 / 还能选几项」——
        尤其在标签折叠成「+N」之后，屏幕上只剩一个数字时更无从判断。
      </p>

      <div class="motion-demo">
        <div class="motion-row motion-row--top">
          <span class="motion-label">标签化多选</span>
          <div style="width: 320px">
            <DdMultiSelect v-model="multiValue" :options="multiOptions" placeholder="选择技能" filterable />
          </div>
        </div>

        <div class="motion-row motion-row--top">
          <span class="motion-label">带上限</span>
          <div style="width: 320px">
            <DdMultiSelect v-model="multiLimited" :options="multiOptions" :max="3" placeholder="最多选 3 项" />
          </div>
          <span class="motion-hint">
            到上限后未选中的项【置灰】而不是静默忽略点击；已选中的仍可点，否则会被锁死
          </span>
        </div>

        <div class="motion-row motion-row--top">
          <span class="motion-label">折叠标签</span>
          <div style="width: 320px">
            <DdMultiSelect
              v-model="multiValue"
              :options="multiOptions"
              collapse-tags
              :max-collapse-tags="2"
              placeholder="选多了会折叠"
            />
          </div>
        </div>
      </div>
    </section>

    <!-- ============ 6. Split Button ============ -->
    <section class="motion-section">
      <h2><span class="motion-section__no">06</span> Split Button</h2>
      <p class="motion-section__desc">
        主体点了【直接执行】，右侧箭头才展开更多——主体不能执行操作的话，那只是个带标签的下拉。
        危险操作一律进菜单并标红加分隔线，绝不能放在「点了就执行」的主体上。
      </p>

      <div class="motion-demo">
        <div class="motion-row">
          <span class="motion-label">动态主操作</span>
          <DdSplitButton
            :label="taskRunning ? '停止' : '运行'"
            :type="taskRunning ? 'danger' : 'primary'"
            size="small"
            :items="taskItems"
            @click="taskRunning = !taskRunning"
            @command="onSplitCommand"
          />
          <span class="motion-hint">
            点主体切换运行状态——注意菜单里的「立即运行 / 停止运行」跟着互斥，不会出现矛盾项
          </span>
        </div>

        <div class="motion-row">
          <span class="motion-label">各语义色</span>
          <DdSplitButton label="发布" type="primary" :items="staticItems" @command="onSplitCommand" />
          <DdSplitButton label="保存" type="success" :items="staticItems" @command="onSplitCommand" />
          <DdSplitButton label="默认" type="default" :items="staticItems" @command="onSplitCommand" />
          <span class="motion-hint">中缝在实心底色上用 #ffffff80，浅底按钮上用边框灰</span>
        </div>

        <div class="motion-row">
          <span class="motion-label">禁用</span>
          <DdSplitButton label="发布" type="primary" disabled :items="staticItems" />
        </div>
      </div>
    </section>

    <!-- ============ 7. Date Picker ============ -->
    <section class="motion-section">
      <h2><span class="motion-section__no">07</span> Date Picker 日期范围</h2>
      <p class="motion-section__desc">
        面板里的日期格原本是 <code>border-radius: 50%</code> 的圆点、范围起止是半个胶囊——
        在一片直角界面里非常突兀。这里全部方化，范围条也去掉了两端各 5px 的缩进，连成一条实心带。
      </p>

      <div class="motion-demo">
        <div class="motion-row motion-row--top">
          <span class="motion-label">竖排（表单里）</span>
          <DdDateRangePicker v-model="dateRange" />
        </div>

        <div class="motion-row motion-row--top">
          <span class="motion-label">横排（工具栏里）</span>
          <DdDateRangePicker v-model="dateRangeInline" inline />
          <span class="motion-hint">竖排会把整条工具栏撑成两行高，列表页要用 inline</span>
        </div>

        <div class="motion-row motion-row--top">
          <span class="motion-label">限制可回溯天数</span>
          <DdDateRangePicker v-model="dateRange" inline :max-past-days="7" :shortcuts="[]" />
          <span class="motion-hint">
            日志有保留期，让用户选到 90 天前再发现没数据，等于把清理策略的后果扔给他去猜
          </span>
        </div>
      </div>
    </section>

    <footer class="motion-footer">
      系统「减少动态效果」开启时，以上全部动效会自动降级为 1ms
      —— 时长与延迟都吃 <code>--dd-motion-*</code> 令牌，不写死毫秒数。
    </footer>
  </div>
</template>

<style scoped lang="scss">
.motion-page {
  padding-bottom: 40px;
}

.motion-header {
  padding: 4px 0 20px;
  border-bottom: 1px solid var(--el-border-color-lighter);
  margin-bottom: 20px;

  h1 {
    margin: 0 0 8px;
    font-size: 20px;
    font-weight: 600;
    color: var(--el-text-color-primary);
  }

  p {
    margin: 0;
    max-width: 900px;
    font-size: 13px;
    line-height: 1.7;
    color: var(--el-text-color-secondary);
  }
}

.motion-section {
  padding: 20px;
  margin-bottom: 16px;
  border: 1px solid var(--el-border-color-lighter);
  // 分节卡片属容器类表面 → surface 档（与业务页的 .table-card / .dd-data-card 同档，
  // 这样这页作为动效/形状预览入口时，观感与业务页一致，不会自成一套）
  border-radius: var(--dd-radius-surface);
  background: var(--el-bg-color);
  // 入场：与全站 dd-*-rise-in 同一语汇（opacity + translateY），时长吃令牌
  animation: dd-motion-rise-in var(--dd-motion-page) var(--dd-ease-decelerate) both;

  h2 {
    display: flex;
    align-items: center;
    gap: 10px;
    margin: 0 0 6px;
    font-size: 15px;
    font-weight: 600;
    color: var(--el-text-color-primary);
  }
}

.motion-section__no {
  font-size: 12px;
  font-weight: 700;
  color: var(--el-color-primary);
  font-variant-numeric: tabular-nums;
}

.motion-section__desc {
  margin: 0 0 16px;
  max-width: 900px;
  font-size: 13px;
  line-height: 1.7;
  color: var(--el-text-color-secondary);

  code {
    padding: 1px 4px;
    // 行内 code 是文字级小块，归控件类 → control 档（与 el-tag 同档）
    border-radius: var(--dd-radius-control);
    background: var(--el-fill-color-light);
    font-family: var(--dd-font-mono);
    font-size: 12px;
  }
}

.motion-demo {
  display: flex;
  flex-direction: column;
  gap: 14px;
}

.motion-row {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 10px;
}

// 内含多行控件（多选、日期范围）时顶端对齐，否则标签会被推到中间
.motion-row--top {
  align-items: flex-start;
}

.motion-label {
  flex: 0 0 auto;
  width: 110px;
  font-size: 12px;
  color: var(--el-text-color-secondary);
}

.motion-chip {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: 4px 10px;
  border: 1px solid var(--el-border-color-lighter);
  // 这是包着「文字 + DdBadge」的描边小块，属控件类表面 → control 档；
  // 不吃 pill：它不是状态灯，内部还嵌了角标，做成胶囊会和角标本身的圆度打架
  border-radius: var(--dd-radius-control);
  font-size: 13px;
  color: var(--el-text-color-regular);
}

.motion-hint {
  font-size: 12px;
  line-height: 1.6;
  color: var(--el-text-color-placeholder);
}

.motion-footer {
  padding: 16px 0 0;
  border-top: 1px solid var(--el-border-color-lighter);
  font-size: 12px;
  color: var(--el-text-color-placeholder);

  code {
    font-family: var(--dd-font-mono);
  }
}

@keyframes dd-motion-rise-in {
  from {
    opacity: 0;
    transform: translateY(12px);
  }
  to {
    opacity: 1;
    transform: translateY(0);
  }
}

@media screen and (max-width: 768px) {
  .motion-label {
    width: 100%;
  }
}
</style>
