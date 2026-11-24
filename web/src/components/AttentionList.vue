<script setup lang="ts">
/**
 * 「需要关注」条目列表（design §5 F2，契约见 wave1-handoff §8）。
 * 仪表板的「需要关注」卡片与顶栏铃铛弹出层共用；数据读 stores/attention.ts，本组件不自己轮询。
 *
 * props：
 *   compact  紧凑版（铃铛弹出层用）：行距更小、按钮只留一个主操作
 *   limit    最多显示几条，默认全部
 *
 * 一键处理：
 *   任务连续失败 → 重跑（operator+）/ 看日志
 *   订阅拉取失败 → 重新拉取（operator+）
 *   依赖安装失败 → 跳依赖管理并带上类型与名称（定位由依赖页读 query）
 *   新版本 / 磁盘 → 跳系统设置
 * 「重跑」「重新拉取」成功后条目折叠消失，并立即 refresh()；只是跳转的操作不算「已处理」，条目保留。
 */
import { computed, ref } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import {
  WarningFilled,
  Refresh,
  Box,
  Upload,
  FolderOpened,
  InfoFilled,
  CircleCheckFilled,
} from '@element-plus/icons-vue'
import type { AttentionItem } from '@/api/system'
import { taskApi } from '@/api/task'
import { subscriptionApi } from '@/api/subscription'
import { useAttentionStore } from '@/stores/attention'
import { useAuthStore } from '@/stores/auth'
import { useUiStore } from '@/stores/ui'
import { canOperate } from '@/utils/roles'
import { formatRelativeTime } from '@/utils/datetime'
import { burst } from '@/utils/motion'

const props = withDefaults(defineProps<{ compact?: boolean; limit?: number }>(), {
  compact: false,
  limit: 0,
})

const router = useRouter()
const store = useAttentionStore()
const authStore = useAuthStore()
const ui = useUiStore()

const canRun = computed(() => canOperate(authStore.user?.role))
const list = computed(() => (props.limit > 0 ? store.items.slice(0, props.limit) : store.items))
const hiddenCount = computed(() => Math.max(0, store.items.length - list.value.length))

/** 正在执行一键处理的条目（按钮转圈、防连点） */
const busyKey = ref('')
/** 处理成功、正在播放折叠动画的条目 */
const goneKeys = ref<string[]>([])

function iconOf(item: AttentionItem) {
  switch (item.type) {
    case 'task_failing':
      return WarningFilled
    case 'subscription_failed':
      return Refresh
    case 'dependency_failed':
      return Box
    case 'update_available':
      return Upload
    case 'disk_high':
      return FolderOpened
    default:
      return InfoFilled
  }
}

/** 图标色块的颜色：err / warn 用状态色，其余用主色（图标与浅底，不是文字，所以用实色令牌） */
function colorOf(item: AttentionItem) {
  if (item.level === 'err') return 'var(--dd-err)'
  if (item.level === 'warn') return 'var(--dd-warn)'
  return 'var(--dd-accent)'
}

function subText(item: AttentionItem) {
  const time = item.at ? formatRelativeTime(item.at, '') : ''
  if (item.detail && time) return `${time} · ${item.detail}`
  return item.detail || time
}

/** 跳转前先关掉铃铛弹出层（在仪表板卡片里调用时它本来就是关的，无副作用） */
function go(to: Parameters<typeof router.push>[0]) {
  ui.closeAttention()
  void router.push(to)
}

/** 处理成功：先折叠，动画播完再从 store 里压掉；同时立刻刷新一次 */
function finishHandled(item: AttentionItem) {
  goneKeys.value = [...goneKeys.value, item.key]
  window.setTimeout(() => {
    store.markHandled(item)
    goneKeys.value = goneKeys.value.filter((k) => k !== item.key)
  }, 520)
  void store.refresh()
}

async function rerunTask(item: AttentionItem, e: MouseEvent) {
  if (busyKey.value) return
  busyKey.value = item.key
  try {
    await taskApi.run(item.target.id)
    ElMessage.success('已开始重跑')
    burst(e.clientX, e.clientY)
    finishHandled(item)
  } catch {
    // 错误提示由 request 拦截器统一弹出
  } finally {
    busyKey.value = ''
  }
}

async function repullSubscription(item: AttentionItem, e: MouseEvent) {
  if (busyKey.value) return
  busyKey.value = item.key
  try {
    await subscriptionApi.pull(item.target.id)
    ElMessage.success('已开始重新拉取')
    burst(e.clientX, e.clientY)
    finishHandled(item)
  } catch {
    // 同上
  } finally {
    busyKey.value = ''
  }
}

function openDependency(item: AttentionItem) {
  // 服务端标题形如「依赖「requests」安装失败（python）」，从里面取出类型与名称交给依赖页定位
  const match = item.title.match(/「(.+)」.*（(nodejs|python|linux)）\s*$/)
  const query: Record<string, string> = { dep_id: String(item.target.id) }
  if (match) {
    query.keyword = match[1] || ''
    query.type = match[2] || ''
  }
  go({ path: '/deps', query })
}
</script>

<template>
  <div class="attn-list" :class="{ 'is-compact': compact }">
    <div v-if="list.length === 0" class="attn-empty">
      <el-icon :size="compact ? 22 : 28" class="attn-empty__icon"><CircleCheckFilled /></el-icon>
      <span>{{ store.loaded ? '一切正常，没有需要处理的事' : '正在检查…' }}</span>
    </div>

    <div
      v-for="item in list"
      :key="item.key"
      class="attn-item"
      :class="{ 'is-gone': goneKeys.includes(item.key) }"
    >
      <div class="attn-item__clip">
        <div class="attn-row">
          <span class="attn-row__icon" :style="{ '--attn-c': colorOf(item) }">
            <el-icon :size="compact ? 15 : 17"><component :is="iconOf(item)" /></el-icon>
          </span>
          <span class="attn-row__text">
            <b :title="item.title">{{ item.title }}</b>
            <small v-if="subText(item)" :title="item.detail">{{ subText(item) }}</small>
          </span>
          <span class="attn-row__actions">
            <!-- 任务连续失败：operator+ 才有重跑；看日志所有角色都有 -->
            <template v-if="item.type === 'task_failing'">
              <el-button
                v-if="canRun"
                size="small"
                :loading="busyKey === item.key"
                @click="rerunTask(item, $event)"
              >重跑</el-button>
              <el-button
                v-if="!compact || !canRun"
                size="small"
                text
                @click="go({ path: '/logs', query: { task_id: String(item.target.id) } })"
              >日志</el-button>
            </template>
            <template v-else-if="item.type === 'subscription_failed'">
              <el-button
                v-if="canRun"
                size="small"
                :loading="busyKey === item.key"
                @click="repullSubscription(item, $event)"
              >重新拉取</el-button>
            </template>
            <el-button
              v-else-if="item.type === 'dependency_failed'"
              size="small"
              @click="openDependency(item)"
            >查看</el-button>
            <el-button
              v-else-if="item.type === 'update_available'"
              size="small"
              @click="go('/admin/settings')"
            >去更新</el-button>
            <el-button
              v-else-if="item.type === 'disk_high'"
              size="small"
              @click="go('/admin/settings')"
            >查看</el-button>
          </span>
        </div>
      </div>
    </div>

    <div v-if="hiddenCount > 0" class="attn-more">还有 {{ hiddenCount }} 项</div>
  </div>
</template>

<style scoped lang="scss">
.attn-list {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

// 折叠动画：grid 行从 1fr 收到 0fr，内容被 __clip 裁掉；同时淡出、把列表间距一起收回
.attn-item {
  display: grid;
  grid-template-rows: 1fr;
  transition:
    grid-template-rows 0.5s var(--dd-ease-emphasized),
    opacity 0.4s var(--dd-ease-standard),
    margin 0.5s var(--dd-ease-emphasized);

  &.is-gone {
    grid-template-rows: 0fr;
    opacity: 0;
    margin-top: -8px;
    pointer-events: none;
  }
}

.attn-item__clip {
  overflow: hidden;
  min-height: 0;
}

.attn-row {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 10px 12px;
  // 列表里的内嵌块：低卡片一档 → surface 档
  border-radius: var(--dd-radius-surface);
  background: var(--dd-surface-2);
  border: var(--dd-border-width, 1px) solid var(--dd-border);
}

.attn-row__icon {
  width: 34px;
  height: 34px;
  flex: none;
  display: grid;
  place-items: center;
  border-radius: var(--dd-radius-control);
  color: var(--attn-c);
  background: color-mix(in srgb, var(--attn-c) 15%, transparent);
}

.attn-row__text {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  line-height: 1.35;

  b {
    font-size: 13.5px;
    font-weight: 600;
    color: var(--dd-text);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }

  small {
    font-size: 12px;
    // 压在条目浅底（surface-2）上，text-3 在素白暗色只有 4.22:1，改用 text-2
    color: var(--dd-text-2);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
}

.attn-row__actions {
  flex: none;
  display: inline-flex;
  align-items: center;
  gap: 4px;

  // EP 按钮相邻时自带 margin-left，这里统一用 gap
  :deep(.el-button + .el-button) {
    margin-left: 0;
  }
}

.attn-empty {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 8px;
  padding: 24px 8px;
  font-size: 13px;
  color: var(--dd-text-3);
}

.attn-empty__icon {
  color: var(--dd-ok);
}

.attn-more {
  font-size: 12px;
  color: var(--dd-text-3);
  text-align: center;
  padding-top: 2px;
}

// 紧凑版：铃铛弹出层里空间小，行距与图标都收一档
.is-compact {
  gap: 6px;

  .attn-row {
    gap: 10px;
    padding: 8px 10px;
  }

  .attn-row__icon {
    width: 28px;
    height: 28px;
  }

  .attn-row__text b {
    font-size: 13px;
  }

  .attn-empty {
    padding: 16px 8px;
  }
}
</style>
