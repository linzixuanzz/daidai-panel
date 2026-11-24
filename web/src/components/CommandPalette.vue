<script setup lang="ts">
/**
 * 命令面板（F1，design §5）：Ctrl K 打开，跳页 / 执行操作 / 搜任务、脚本、变量、订阅 / 运行任务。
 *
 * - 显隐只读写 useUiStore().paletteOpen。全局 Ctrl K、Esc 兜底都在 MainLayout（G1），
 *   这里不挂全局 keydown，只在面板自己身上处理 ↑↓ / Tab / 回车 / Esc。
 * - 四类来源：页面（layouts/menu.ts，按角色过滤）、操作、远程搜索（输入后 200ms 防抖，各取前 8 条）、
 *   最近使用（输入为空时显示最近 5 条，存本机）。
 * - 匹配：原文 / 拼音首字母 / 子序列三档，见 utils/pinyinInitials.ts 的 matchPinyin。
 *   远程结果是服务端按 keyword 搜出来的（服务端不认拼音），本地再算一遍只为排序和高亮。
 */
import { computed, nextTick, ref, watch, type Component } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import {
  Brush,
  Clock,
  Document,
  Download,
  MagicStick,
  Monitor,
  Moon,
  Plus,
  Search,
  Setting,
  Sunny,
  Timer,
  VideoPlay,
  Odometer,
} from '@element-plus/icons-vue'
import { PANEL_MENU_GROUPS, hasMinRole } from '@/layouts/menu'
import { useUiStore } from '@/stores/ui'
import { useThemeStore } from '@/stores/theme'
import { useAuthStore } from '@/stores/auth'
import { taskApi } from '@/api/task'
import { scriptApi } from '@/api/script'
import { envApi } from '@/api/env'
import { subscriptionApi } from '@/api/subscription'
import { THEME_PRESETS, type MotionPreference, type ThemePreset } from '@/utils/panelAppearance'
import { canOperate } from '@/utils/roles'
import { extractError } from '@/utils/error'
import { matchPinyin } from '@/utils/pinyinInitials'

// 条目种类：决定执行方式和图标。page / action 的 target 是路径 / 操作 id，其余是远程对象的 id 或路径
type PaletteKind = 'page' | 'action' | 'task-run' | 'task-open' | 'script' | 'env' | 'sub'

interface PaletteItem {
  key: string // 全局唯一，最近使用按它去重
  kind: PaletteKind
  group: string // 分组名：页面 / 操作 / 任务 / 脚本 / 环境变量 / 订阅
  title: string
  hint?: string
  keywords?: string[]
  target: string | number
  name?: string // 任务名（运行成功提示、按名字定位任务用）
  icon?: Component
}

// 渲染用的一行：带分组标题（只在空输入时出现）和高亮切片
interface PaletteRow {
  rowKey: string
  item: PaletteItem
  header?: string
  parts: { text: string; hit: boolean }[]
}

const RECENT_KEY = 'dd:palette:recent'
const RECENT_LIMIT = 5
const REMOTE_LIMIT = 8

const ui = useUiStore()
const theme = useThemeStore()
const auth = useAuthStore()
const router = useRouter()

const query = ref('')
const sel = ref(0)
const searching = ref(false)
const remoteItems = ref<PaletteItem[]>([])
const recentItems = ref<PaletteItem[]>([])
const inputRef = ref<HTMLInputElement | null>(null)
const listRef = ref<HTMLElement | null>(null)

const isMac = typeof navigator !== 'undefined' && /Mac|iPhone|iPad/i.test(navigator.platform || navigator.userAgent)
const role = computed(() => auth.user?.role)
const operator = computed(() => canOperate(role.value))

// 远程搜索的序号闸：输入变了、面板关了，迟到的结果一律丢掉
let searchSeq = 0
let searchTimer: ReturnType<typeof setTimeout> | undefined
// 脚本接口不认 keyword、一次返回全部文件：每次打开面板只拉一次，之后在本地过滤
let scriptCache: Promise<any[]> | null = null
// 打开前的焦点，关掉后还回去
let lastFocused: HTMLElement | null = null

const MOTION_OPTIONS: { value: MotionPreference; label: string }[] = [
  { value: 'system', label: '跟随系统' },
  { value: 'rich', label: '丰富' },
  { value: 'subtle', label: '克制' },
  { value: 'off', label: '关闭' },
]

// 本地条目：页面 + 操作。依赖角色、明暗、当前风格、动效档位，切换后标题和「当前」标记跟着变
const localItems = computed<PaletteItem[]>(() => {
  const list: PaletteItem[] = []

  for (const group of PANEL_MENU_GROUPS) {
    for (const menu of group.items) {
      if (!hasMinRole(role.value, menu.minRole)) continue
      list.push({
        key: `page:${menu.key}`,
        kind: 'page',
        group: '页面',
        title: menu.title,
        hint: group.title,
        keywords: menu.keywords,
        // 主题工坊不是路由，执行时打开抽屉
        target: menu.action === 'theme-studio' ? 'theme-studio' : (menu.path ?? ''),
        icon: menu.icon,
      })
    }
  }

  if (operator.value) {
    list.push({ key: 'action:new-task', kind: 'action', group: '操作', title: '新建任务', keywords: ['new', 'task', 'create', '添加'], target: 'new-task', icon: Plus })
  }
  list.push({
    key: 'action:toggle-dark',
    kind: 'action',
    group: '操作',
    title: theme.isDark ? '切换到亮色' : '切换到暗色',
    keywords: ['dark', 'light', '明暗', '深色', '浅色', '夜间'],
    target: 'toggle-dark',
    icon: theme.isDark ? Sunny : Moon,
  })
  list.push({ key: 'action:studio', kind: 'action', group: '操作', title: '打开主题工坊', keywords: ['theme', 'studio', '外观', '样式', '风格'], target: 'studio', icon: Brush })
  list.push({ key: 'action:dock', kind: 'action', group: '操作', title: '打开运行中浮窗', keywords: ['running', 'dock', '运行中'], target: 'dock', icon: Monitor })
  for (const preset of THEME_PRESETS) {
    list.push({
      key: `action:preset:${preset.id}`,
      kind: 'action',
      group: '操作',
      title: `风格 · ${preset.name}`,
      hint: theme.preset === preset.id ? '当前' : preset.desc,
      keywords: [preset.id, 'theme', '主题', '切换风格'],
      target: `preset:${preset.id}`,
      icon: MagicStick,
    })
  }
  for (const option of MOTION_OPTIONS) {
    list.push({
      key: `action:motion:${option.value}`,
      kind: 'action',
      group: '操作',
      title: `动效 · ${option.label}`,
      hint: theme.motion === option.value ? '当前' : undefined,
      keywords: [option.value, 'motion', '动画', '动效档位'],
      target: `motion:${option.value}`,
      icon: Setting,
    })
  }
  return list
})

// 列表：空输入 = 最近使用 + 页面 + 操作（带分组标题）；有输入 = 本地命中 + 远程结果，按分数排成一列
const rows = computed<PaletteRow[]>(() => {
  const q = query.value.trim()
  const out: PaletteRow[] = []

  if (!q) {
    let lastGroup = ''
    const push = (item: PaletteItem, group: string, prefix: string) => {
      out.push({
        rowKey: `${prefix}${item.key}`,
        item,
        header: group !== lastGroup ? group : undefined,
        parts: [{ text: item.title, hit: false }],
      })
      lastGroup = group
    }
    // 最近使用里的页面 / 操作按 key 换成当前版本（标题、权限可能已经变了），找不到就不显示
    for (const recent of recentItems.value) {
      if (recent.kind === 'page' || recent.kind === 'action') {
        const live = localItems.value.find((item) => item.key === recent.key)
        if (live) push(live, '最近使用', 'recent:')
      } else if (recent.kind !== 'task-run' || operator.value) {
        push(recent, '最近使用', 'recent:')
      }
    }
    for (const item of localItems.value) push(item, item.group, '')
    return out
  }

  const scored: { item: PaletteItem; score: number; hits: number[] }[] = []
  for (const item of localItems.value) {
    const hit = matchPinyin(item.title, q)
    if (hit) {
      scored.push({ item, score: hit.score, hits: hit.hits })
      continue
    }
    // 标题没命中再看关键词：分数打五折、不高亮
    let best = 0
    for (const word of item.keywords ?? []) {
      const kwHit = matchPinyin(word, q)
      if (kwHit && kwHit.score > best) best = kwHit.score
    }
    if (best > 0) scored.push({ item, score: best * 0.5, hits: [] })
  }
  for (const item of remoteItems.value) {
    // 远程结果一律保留（服务端可能是按命令、备注命中的），本地能命中标题的排前面并高亮
    const hit = matchPinyin(item.title, q)
    scored.push({ item, score: hit ? hit.score : 10, hits: hit ? hit.hits : [] })
  }
  // sort 是稳定的：同分时保持「页面 → 操作 → 任务 → 脚本 → 变量 → 订阅」的原始顺序
  scored.sort((a, b) => b.score - a.score)

  for (const { item, hits } of scored) {
    const parts: PaletteRow['parts'] = []
    const hitSet = new Set(hits)
    Array.from(item.title).forEach((ch, i) => {
      const hit = hitSet.has(i)
      const last = parts[parts.length - 1]
      if (last && last.hit === hit) last.text += ch
      else parts.push({ text: ch, hit })
    })
    out.push({ rowKey: item.key, item, parts })
  }
  return out
})

// 列表变化时尽量让选中项停在同一条上（远程结果晚到会插进来），找不到才回到第一条
let selKey = ''
watch(rows, (list) => {
  const keep = list.findIndex((row) => row.rowKey === selKey)
  sel.value = keep >= 0 ? keep : 0
  selKey = list[sel.value]?.rowKey ?? ''
})
watch(sel, (index) => {
  selKey = rows.value[index]?.rowKey ?? ''
  void nextTick(() => {
    listRef.value?.querySelector('.is-sel')?.scrollIntoView({ block: 'nearest' })
  })
})

// 打开：清空输入、读最近使用、聚焦；关闭：作废未完成的搜索
watch(
  () => ui.paletteOpen,
  (open) => {
    clearTimeout(searchTimer)
    searchSeq++
    searching.value = false
    if (open) {
      lastFocused = document.activeElement instanceof HTMLElement ? document.activeElement : null
      query.value = ''
      remoteItems.value = []
      scriptCache = null
      selKey = ''
      sel.value = 0
      try {
        const raw = JSON.parse(localStorage.getItem(RECENT_KEY) || '[]')
        recentItems.value = Array.isArray(raw) ? raw.filter((item) => item && item.key && item.kind).slice(0, RECENT_LIMIT) : []
      } catch {
        recentItems.value = []
      }
      void nextTick(() => inputRef.value?.focus())
    }
  },
)

// 输入防抖 200ms 后远程搜索。四路并发，全部结束后一次性替换结果（不会一条条往里跳）
watch(query, (value) => {
  sel.value = 0
  selKey = ''
  clearTimeout(searchTimer)
  searchSeq++
  const keyword = value.trim()
  if (!keyword) {
    remoteItems.value = []
    searching.value = false
    return
  }
  searching.value = true
  const seq = searchSeq
  searchTimer = setTimeout(async () => {
    const jobs: Promise<PaletteItem[]>[] = []

    // 任务：viewer 也能搜，只是看不到「运行」项
    jobs.push(
      taskApi.list({ keyword, page: 1, page_size: REMOTE_LIMIT }).then((res) => {
        const items: PaletteItem[] = []
        for (const task of (res.data || []).slice(0, REMOTE_LIMIT)) {
          const hint = task.cron_expression || undefined
          if (operator.value) {
            items.push({ key: `task-run:${task.id}`, kind: 'task-run', group: '任务', title: `运行 · ${task.name}`, hint, target: task.id, name: task.name })
          }
          items.push({ key: `task-open:${task.id}`, kind: 'task-open', group: '任务', title: `打开 · ${task.name}`, hint, target: task.id, name: task.name })
        }
        return items
      }),
    )

    // 脚本 / 变量 / 订阅三个接口都要求 operator，viewer 不发请求（发了也是 403）
    if (operator.value) {
      if (!scriptCache) {
        scriptCache = scriptApi.list().then((res) => (Array.isArray(res.data) ? res.data : []))
        // 拉失败时下次输入重新拉，不要把失败缓存住
        scriptCache.catch(() => {
          scriptCache = null
        })
      }
      jobs.push(
        scriptCache.then((files) => {
          const matched: { file: any; score: number }[] = []
          for (const file of files) {
            const hit = matchPinyin(String(file.path || ''), keyword)
            if (hit) matched.push({ file, score: hit.score })
          }
          matched.sort((a, b) => b.score - a.score)
          return matched.slice(0, REMOTE_LIMIT).map(({ file }) => ({
            key: `script:${file.path}`,
            kind: 'script' as const,
            group: '脚本',
            title: String(file.name || file.path),
            hint: String(file.path),
            target: String(file.path),
          }))
        }),
      )
      jobs.push(
        envApi.list({ keyword, page: 1, page_size: REMOTE_LIMIT }).then((res) =>
          (res.data || []).slice(0, REMOTE_LIMIT).map((env: any) => ({
            key: `env:${env.id}`,
            kind: 'env' as const,
            group: '环境变量',
            title: String(env.name),
            // 只显示备注，变量值可能是密钥，不往面板上放
            hint: env.remarks || undefined,
            target: env.id,
          })),
        ),
      )
      jobs.push(
        subscriptionApi.list({ keyword, page: 1, page_size: REMOTE_LIMIT }).then((res) =>
          (res.data || []).slice(0, REMOTE_LIMIT).map((sub: any) => ({
            key: `sub:${sub.id}`,
            kind: 'sub' as const,
            group: '订阅',
            title: String(sub.name),
            hint: sub.url || undefined,
            target: sub.id,
          })),
        ),
      )
    }

    // 某一路失败（老后端、权限、网络）只少那一类结果，不弹错
    const results = await Promise.allSettled(jobs)
    if (seq !== searchSeq || !ui.paletteOpen) return
    remoteItems.value = results.flatMap((result) => (result.status === 'fulfilled' ? result.value : []))
    searching.value = false
  }, 200)
})

const KIND_ICONS: Record<PaletteKind, Component> = {
  page: Odometer,
  action: Setting,
  'task-run': VideoPlay,
  'task-open': Timer,
  script: Document,
  env: Setting,
  sub: Download,
}

function iconOf(item: PaletteItem) {
  return item.icon ?? KIND_ICONS[item.kind]
}

async function execute(item: PaletteItem | undefined) {
  if (!item) return

  // 记最近使用：去掉图标（组件不能进 JSON），同 key 挪到最前
  const { icon: _icon, ...plain } = item
  const recent = [plain, ...recentItems.value.filter((old) => old.key !== item.key)].slice(0, RECENT_LIMIT)
  recentItems.value = recent
  try {
    localStorage.setItem(RECENT_KEY, JSON.stringify(recent))
  } catch {
    // 隐私模式 / 存储满：最近使用只是锦上添花，静默跳过
  }

  ui.closePalette()

  switch (item.kind) {
    case 'page':
      if (item.target === 'theme-studio') ui.openStudio()
      else void router.push(String(item.target))
      break
    case 'action': {
      const target = String(item.target)
      if (target === 'new-task') {
        // 沿用仪表板「新建任务」快捷入口的做法：任务页读到 ?create=1 会打开新建弹窗并清掉 query
        void router.push({ path: '/tasks', query: { create: '1' } })
      } else if (target === 'toggle-dark') {
        theme.toggleTheme()
      } else if (target === 'studio') {
        ui.openStudio()
      } else if (target === 'dock') {
        ui.openDock()
      } else if (target.startsWith('preset:')) {
        theme.setPreset(target.slice('preset:'.length) as ThemePreset)
      } else if (target.startsWith('motion:')) {
        theme.setMotion(target.slice('motion:'.length) as MotionPreference)
      }
      break
    }
    case 'task-run':
      // 面板里的「运行 · X」本身就是明确的动作，不再二次确认（与原型一致）
      try {
        await taskApi.run(Number(item.target))
        ElMessage.success(`「${item.name}」已开始运行`)
      } catch (err) {
        ElMessage.error(extractError(err, '运行失败'))
      }
      break
    case 'task-open':
      // 任务页读到 ?keyword= 会按名字搜索并定位这条任务（#176 的入口）
      void router.push({ path: '/tasks', query: { keyword: item.name || '' } })
      break
    case 'script':
      void router.push({ path: '/scripts', query: { file: String(item.target) } })
      break
    case 'env':
      void router.push('/envs')
      break
    case 'sub':
      void router.push('/subscriptions')
      break
  }
}

function move(step: number) {
  const total = rows.value.length
  if (!total) return
  sel.value = (sel.value + step + total) % total
}

function onKeydown(event: KeyboardEvent) {
  // 输入法组字时的回车 / 方向键是给输入法的，不能抢
  if (event.isComposing || event.keyCode === 229) return
  if (event.key === 'ArrowDown') {
    event.preventDefault()
    move(1)
  } else if (event.key === 'ArrowUp') {
    event.preventDefault()
    move(-1)
  } else if (event.key === 'Tab') {
    // Tab 当作上下移动，同时把焦点关在输入框里
    event.preventDefault()
    move(event.shiftKey ? -1 : 1)
  } else if (event.key === 'Enter') {
    event.preventDefault()
    void execute(rows.value[sel.value]?.item)
  } else if (event.key === 'Escape') {
    // 只关面板：拦住冒泡，免得 MainLayout 的 Esc 兜底（closeAll）把运行中浮窗也一起关掉
    event.preventDefault()
    event.stopPropagation()
    ui.closePalette()
  }
}

// 关闭动画结束后把焦点还回去；如果焦点已经被别的浮层（例如主题工坊抽屉）拿走就不抢
function restoreFocus() {
  const active = document.activeElement
  if ((!active || active === document.body) && lastFocused?.isConnected) lastFocused.focus()
  lastFocused = null
}
</script>

<template>
  <Teleport to="body">
    <Transition name="dd-cmdk-scrim">
      <div v-if="ui.paletteOpen" class="dd-cmdk-scrim" @click="ui.closePalette()" />
    </Transition>
    <Transition name="dd-cmdk" @after-leave="restoreFocus">
      <div v-if="ui.paletteOpen" class="dd-cmdk" role="dialog" aria-modal="true" aria-label="命令面板" @keydown="onKeydown">
        <div class="dd-cmdk__input">
          <el-icon class="dd-cmdk__search-icon"><Search /></el-icon>
          <input
            ref="inputRef"
            v-model="query"
            type="text"
            role="combobox"
            aria-expanded="true"
            aria-controls="dd-cmdk-list"
            aria-autocomplete="list"
            :aria-activedescendant="rows.length ? `dd-cmdk-opt-${sel}` : undefined"
            aria-label="命令搜索"
            autocomplete="off"
            spellcheck="false"
            placeholder="跳转页面、运行任务、切换风格…  试试输入「dsrw」"
          >
          <kbd class="dd-cmdk__kbd">Esc</kbd>
        </div>

        <ul id="dd-cmdk-list" ref="listRef" class="dd-cmdk__list" role="listbox" aria-label="命令">
          <template v-for="(row, index) in rows" :key="row.rowKey">
            <li v-if="row.header" class="dd-cmdk__group" role="presentation">{{ row.header }}</li>
            <li
              :id="`dd-cmdk-opt-${index}`"
              role="option"
              :aria-selected="index === sel"
              class="dd-cmdk__item"
              :class="{ 'is-sel': index === sel }"
              @pointermove="sel = index"
              @mousedown.prevent
              @click="execute(row.item)"
            >
              <el-icon class="dd-cmdk__icon"><component :is="iconOf(row.item)" /></el-icon>
              <span class="dd-cmdk__title">
                <template v-for="(part, partIndex) in row.parts" :key="partIndex">
                  <mark v-if="part.hit">{{ part.text }}</mark>
                  <template v-else>{{ part.text }}</template>
                </template>
              </span>
              <small v-if="row.item.hint" class="dd-cmdk__hint">{{ row.item.hint }}</small>
              <small v-if="query.trim()" class="dd-cmdk__tag">{{ row.item.group }}</small>
            </li>
          </template>
          <li v-if="!rows.length && !searching" class="dd-cmdk__empty" role="presentation">
            没有找到「{{ query.trim() }}」相关的内容
          </li>
        </ul>

        <div class="dd-cmdk__foot">
          <span><kbd class="dd-cmdk__kbd">↑</kbd> <kbd class="dd-cmdk__kbd">↓</kbd> 选择</span>
          <span><kbd class="dd-cmdk__kbd">↵</kbd> 执行</span>
          <span><kbd class="dd-cmdk__kbd">Esc</kbd> 关闭</span>
          <span class="dd-cmdk__spacer" />
          <span v-if="searching" class="dd-cmdk__searching">
            <el-icon class="dd-cmdk__clock"><Clock /></el-icon>正在搜索…
          </span>
          <span v-else>支持拼音首字母 · {{ isMac ? '⌘' : 'Ctrl' }} K 开关</span>
        </div>
      </div>
    </Transition>
  </Teleport>
</template>

<style scoped>
/* 层级：高于外壳与页面，低于 EP 弹窗体系（运行后的 ElMessage 要压在上面） */
.dd-cmdk-scrim {
  position: fixed;
  inset: 0;
  z-index: 1990;
  background: color-mix(in oklab, var(--dd-bg) 40%, transparent);
  -webkit-backdrop-filter: blur(6px);
  backdrop-filter: blur(6px);
}

.dd-cmdk {
  position: fixed;
  z-index: 1991;
  left: 50%;
  top: 14vh;
  width: min(660px, calc(100vw - 32px));
  transform: translateX(-50%);
  display: flex;
  flex-direction: column;
  overflow: hidden;
  color: var(--dd-text);
  background: var(--dd-overlay);
  border: var(--dd-border-width, 1px) solid var(--dd-border);
  border-radius: var(--dd-radius-surface);
  box-shadow: var(--dd-shadow-popper);
  -webkit-backdrop-filter: blur(20px) saturate(1.4);
  backdrop-filter: blur(20px) saturate(1.4);
  font-family: var(--dd-font-ui, inherit);
}

.dd-cmdk__input {
  display: flex;
  align-items: center;
  gap: 12px;
  height: 60px;
  padding: 0 18px;
  border-bottom: var(--dd-border-width, 1px) solid var(--dd-border);
}

.dd-cmdk__search-icon {
  font-size: 20px;
  color: var(--dd-text-3);
}

.dd-cmdk__input input {
  flex: 1;
  min-width: 0;
  border: 0;
  outline: 0;
  background: transparent;
  color: var(--dd-text);
  font: inherit;
  font-size: 17px;
}

.dd-cmdk__input input::placeholder {
  color: var(--dd-text-3);
}

.dd-cmdk__list {
  max-height: min(420px, 52vh);
  margin: 0;
  padding: 8px;
  overflow: auto;
  list-style: none;
  overscroll-behavior: contain;
}

.dd-cmdk__group {
  padding: 10px 12px 6px;
  font-size: 11.5px;
  font-weight: 600;
  letter-spacing: 1px;
  color: var(--dd-text-3);
}

.dd-cmdk__item {
  position: relative;
  display: flex;
  align-items: center;
  gap: 12px;
  height: 44px;
  padding: 0 12px;
  border-radius: var(--dd-radius-control);
  color: var(--dd-text-2);
  cursor: pointer;
  user-select: none;
}

.dd-cmdk__item.is-sel {
  background: var(--dd-accent-soft);
}

/* 选中项左侧的主色竖条 */
.dd-cmdk__item.is-sel::before {
  content: '';
  position: absolute;
  left: 0;
  top: 10px;
  bottom: 10px;
  width: 3px;
  border-radius: 3px;
  background: var(--dd-accent);
}

.dd-cmdk__icon {
  flex-shrink: 0;
  font-size: 17px;
  color: var(--dd-text-3);
}

.dd-cmdk__item.is-sel .dd-cmdk__icon {
  color: var(--dd-accent);
}

.dd-cmdk__title {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  white-space: nowrap;
  text-overflow: ellipsis;
  color: var(--dd-text);
}

/* 匹配字符：主色当文字色一律用墨色令牌 */
.dd-cmdk__title mark {
  background: transparent;
  color: var(--dd-accent-ink);
  font-weight: 700;
}

.dd-cmdk__hint,
.dd-cmdk__tag {
  flex-shrink: 0;
  max-width: 40%;
  overflow: hidden;
  white-space: nowrap;
  text-overflow: ellipsis;
  font-size: 12px;
  color: var(--dd-text-3);
}

.dd-cmdk__tag {
  max-width: none;
}

.dd-cmdk__empty {
  padding: 36px;
  text-align: center;
  color: var(--dd-text-3);
}

.dd-cmdk__foot {
  display: flex;
  align-items: center;
  gap: 16px;
  padding: 10px 16px;
  border-top: var(--dd-border-width, 1px) solid var(--dd-border);
  font-size: 12px;
  color: var(--dd-text-3);
}

.dd-cmdk__spacer {
  flex: 1;
}

.dd-cmdk__searching {
  display: inline-flex;
  align-items: center;
  gap: 4px;
}

.dd-cmdk__kbd {
  display: inline-block;
  min-width: 20px;
  padding: 1px 6px;
  border: 1px solid var(--dd-border-strong);
  border-radius: 6px;
  background: var(--dd-surface-2);
  color: var(--dd-text-2);
  font-family: var(--dd-font-mono, ui-monospace, monospace);
  font-size: 11px;
  line-height: 1.5;
  text-align: center;
}

@media (max-width: 640px) {
  .dd-cmdk {
    top: 8vh;
  }

  /* 手机上的键位提示没意义，只留右侧说明 */
  .dd-cmdk__foot > span:not(.dd-cmdk__spacer):not(:last-child) {
    display: none;
  }
}

/* ---------- 进出场：rich 档缩放 + 上移 + 模糊；subtle 档只淡入淡出；off 档由全局 1ms 压缩处理 ---------- */
.dd-cmdk-enter-active {
  transition: transform 0.5s var(--dd-spring-soft), opacity 0.25s ease, filter 0.35s ease;
}

.dd-cmdk-leave-active {
  transition: transform 0.2s var(--dd-ease-io), opacity 0.18s ease, filter 0.2s ease;
}

.dd-cmdk-enter-from,
.dd-cmdk-leave-to {
  opacity: 0;
  transform: translateX(-50%) translateY(-8px) scale(0.94);
  filter: blur(8px);
}

html[data-dd-motion='subtle'] .dd-cmdk-enter-from,
html[data-dd-motion='subtle'] .dd-cmdk-leave-to {
  transform: translateX(-50%);
  filter: none;
}

.dd-cmdk-scrim-enter-active,
.dd-cmdk-scrim-leave-active {
  transition: opacity 0.25s ease;
}

.dd-cmdk-scrim-enter-from,
.dd-cmdk-scrim-leave-to {
  opacity: 0;
}
</style>
