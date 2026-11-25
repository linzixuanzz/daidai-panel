import { defineStore } from 'pinia'
import { ref } from 'vue'

/**
 * 全局浮层开关（design §3）：命令面板、主题工坊、运行中浮窗、「需要关注」弹出层、快捷键帮助。
 *
 * 这几个浮层由不同的人实现（CommandPalette / ThemeStudio / RunningDock / 铃铛弹出层 / ShortcutHelpDialog），
 * 触发入口又散在顶栏、命令面板、快捷键里。约定：各组件只读写这里的开关，互相之间不 import。
 * 组件自己用 v-model 绑这里的 ref 即可（例如 <ThemeStudio v-model="ui.studioOpen" />）。
 *
 * 互斥规则只有一条：命令面板与主题工坊同一时间只开一个（两者都是全屏遮罩级浮层，叠着开会抢焦点）。
 * 浮窗、弹出层、帮助不参与互斥，由各自组件决定。
 */
export const useUiStore = defineStore('ui', () => {
  const paletteOpen = ref(false)
  const studioOpen = ref(false)
  const dockOpen = ref(false)
  const attentionOpen = ref(false)
  const shortcutHelpOpen = ref(false)

  function openPalette() {
    studioOpen.value = false
    // 关闭铃铛弹出层并主动失焦收起可能打开的下拉菜单，避免 EP 2000+ 浮层反向压住 1991 的命令面板
    attentionOpen.value = false
    if (typeof document !== 'undefined' && document.activeElement instanceof HTMLElement) {
      document.activeElement.blur()
    }
    paletteOpen.value = true
  }

  function closePalette() {
    paletteOpen.value = false
  }

  function togglePalette() {
    if (paletteOpen.value) {
      closePalette()
    } else {
      openPalette()
    }
  }

  function openStudio() {
    paletteOpen.value = false
    attentionOpen.value = false
    studioOpen.value = true
  }

  function closeStudio() {
    studioOpen.value = false
  }

  function toggleStudio() {
    if (studioOpen.value) {
      closeStudio()
    } else {
      openStudio()
    }
  }

  function openDock() {
    dockOpen.value = true
  }

  function closeDock() {
    dockOpen.value = false
  }

  function toggleDock() {
    dockOpen.value = !dockOpen.value
  }

  function openAttention() {
    attentionOpen.value = true
  }

  function closeAttention() {
    attentionOpen.value = false
  }

  function toggleAttention() {
    attentionOpen.value = !attentionOpen.value
  }

  function openShortcutHelp() {
    shortcutHelpOpen.value = true
  }

  function closeShortcutHelp() {
    shortcutHelpOpen.value = false
  }

  function toggleShortcutHelp() {
    shortcutHelpOpen.value = !shortcutHelpOpen.value
  }

  /** Esc 的兜底：一次关掉所有浮层（快捷键体系里「Esc 关浮层」用） */
  function closeAll() {
    paletteOpen.value = false
    studioOpen.value = false
    dockOpen.value = false
    attentionOpen.value = false
    shortcutHelpOpen.value = false
  }

  return {
    paletteOpen,
    studioOpen,
    dockOpen,
    attentionOpen,
    shortcutHelpOpen,
    openPalette,
    closePalette,
    togglePalette,
    openStudio,
    closeStudio,
    toggleStudio,
    openDock,
    closeDock,
    toggleDock,
    openAttention,
    closeAttention,
    toggleAttention,
    openShortcutHelp,
    closeShortcutHelp,
    toggleShortcutHelp,
    closeAll,
  }
})
