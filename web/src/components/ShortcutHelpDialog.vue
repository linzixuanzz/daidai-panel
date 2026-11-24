<script setup lang="ts">
/**
 * 快捷键帮助（F9）。无 props，显隐读写 useUiStore().shortcutHelpOpen；按 `?` 或命令面板打开。
 * 快捷键本身只在 MainLayout 的全局 keydown 里处理，这里只负责列出来。
 * 「g 后接字母」的映射取自 layouts/menu.ts 的 GOTO_SHORTCUTS，与实际行为同源；
 * 看不到的页面（角色不够）不列，免得按了没反应。
 */
import { computed } from 'vue'
import { useUiStore } from '@/stores/ui'
import { useAuthStore } from '@/stores/auth'
import { GOTO_SHORTCUTS, findMenuItem, hasMinRole } from '@/layouts/menu'

const ui = useUiStore()
const authStore = useAuthStore()

// Mac 上显示 ⌘，其余显示 Ctrl（与顶栏搜索胶囊同一口径）
const isMac = typeof navigator !== 'undefined' && /Mac|iPhone|iPad|iPod/i.test(navigator.platform || navigator.userAgent)
const modKey = isMac ? '⌘' : 'Ctrl'

const gotoRows = computed(() =>
  GOTO_SHORTCUTS
    .map((row) => ({ key: row.key, item: findMenuItem(row.itemKey) }))
    .filter((row) => row.item && hasMinRole(authStore.user?.role, row.item.minRole))
    .map((row) => ({ keys: ['g', row.key], label: `跳到「${row.item!.title}」` })),
)

const generalRows = computed(() => [
  { keys: [modKey, 'K'], label: '打开 / 关闭命令面板（代码编辑器里交给编辑器）' },
  { keys: ['?'], label: '打开本帮助' },
  { keys: ['Esc'], label: '关闭命令面板、主题工坊、运行中浮窗等浮层' },
])

const paletteRows = [
  { keys: ['↑', '↓'], label: '在结果间移动' },
  { keys: ['Enter'], label: '执行选中项' },
  { keys: ['Esc'], label: '关闭命令面板' },
]
</script>

<template>
  <el-dialog
    v-model="ui.shortcutHelpOpen"
    title="快捷键"
    width="min(520px, calc(100vw - 32px))"
    append-to-body
    class="dd-shortcut-dialog"
  >
    <div class="shortcut-body">
      <section class="shortcut-group">
        <h3>通用</h3>
        <div v-for="row in generalRows" :key="row.label" class="shortcut-row">
          <span class="shortcut-label">{{ row.label }}</span>
          <span class="shortcut-keys">
            <kbd v-for="k in row.keys" :key="k">{{ k }}</kbd>
          </span>
        </div>
      </section>

      <section v-if="gotoRows.length" class="shortcut-group">
        <h3>跳转（先按 g，再按字母）</h3>
        <div v-for="row in gotoRows" :key="row.label" class="shortcut-row">
          <span class="shortcut-label">{{ row.label }}</span>
          <span class="shortcut-keys">
            <kbd>{{ row.keys[0] }}</kbd>
            <span class="shortcut-then">然后</span>
            <kbd>{{ row.keys[1] }}</kbd>
          </span>
        </div>
      </section>

      <section class="shortcut-group">
        <h3>命令面板内</h3>
        <div v-for="row in paletteRows" :key="row.label" class="shortcut-row">
          <span class="shortcut-label">{{ row.label }}</span>
          <span class="shortcut-keys">
            <kbd v-for="k in row.keys" :key="k">{{ k }}</kbd>
          </span>
        </div>
      </section>

      <p class="shortcut-note">
        光标在输入框、可编辑区或代码编辑器里时，g、? 这类单键快捷键不会触发，放心打字。
      </p>
    </div>
  </el-dialog>
</template>

<style scoped lang="scss">
.shortcut-body {
  display: flex;
  flex-direction: column;
  gap: 18px;
}

.shortcut-group h3 {
  margin: 0 0 8px;
  font-size: 12px;
  font-weight: 600;
  letter-spacing: 0.06em;
  color: var(--dd-text-3);
}

.shortcut-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 7px 0;
  border-bottom: 1px solid var(--dd-surface-3);

  &:last-child {
    border-bottom: none;
  }
}

.shortcut-label {
  min-width: 0;
  font-size: 13px;
  color: var(--dd-text-2);
}

.shortcut-keys {
  flex-shrink: 0;
  display: inline-flex;
  align-items: center;
  gap: 4px;
}

.shortcut-then {
  font-size: 11px;
  color: var(--dd-text-3);
}

kbd {
  min-width: 24px;
  padding: 2px 7px;
  border-radius: var(--dd-radius-control);
  border: 1px solid var(--dd-border-strong);
  background: var(--dd-surface-2);
  color: var(--dd-text);
  font-family: var(--dd-font-mono);
  font-size: 12px;
  line-height: 1.5;
  text-align: center;
}

.shortcut-note {
  margin: 0;
  font-size: 12px;
  line-height: 1.6;
  color: var(--dd-text-3);
}
</style>
