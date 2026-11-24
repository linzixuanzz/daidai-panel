<script setup lang="ts">
import { computed } from 'vue'
import { Delete, Edit, FolderRemove, MoreFilled } from '@element-plus/icons-vue'
import type { TreeNode } from '../types'

const props = defineProps<{
  data: TreeNode
  onOpenRename: (path: string) => void
  onDelete: (path: string, isDir: boolean) => void | Promise<void>
  onMoveToRoot?: (path: string, isDir: boolean) => void | Promise<void>
}>()

const isInSubDir = computed(() => props.data.key.includes('/'))

const dotColor = computed(() => {
  // 文件夹点用中性灰令牌；下面各扩展名的颜色是语言识别色（同 GitHub 语言色），刻意保持固定
  if (!props.data.isLeaf) return 'var(--dd-text-3)'
  const ext = props.data.title.split('.').pop()?.toLowerCase()
  switch (ext) {
    case 'js':
      return '#facc15'
    case 'ts':
      return '#38bdf8'
    case 'py':
      return '#22c55e'
    case 'sh':
      return '#4ade80'
    case 'json':
      return '#fb923c'
    case 'yaml':
    case 'yml':
      return '#f87171'
    case 'md':
      return '#818cf8'
    case 'html':
      return '#fb7185'
    case 'css':
      return '#60a5fa'
    case 'go':
      return '#06b6d4'
    default:
      return 'var(--el-text-color-placeholder)'
  }
})

const extLabel = computed(() => {
  if (!props.data.isLeaf) return ''
  const parts = props.data.title.split('.')
  if (parts.length < 2) return ''
  return (parts.pop() || '').toUpperCase()
})
</script>

<template>
  <div class="tree-node" :class="{ 'is-folder': !data.isLeaf, 'is-leaf': data.isLeaf }">
    <span class="tree-node-dot" :style="{ background: dotColor }" aria-hidden="true"></span>
    <span class="tree-node-label">{{ data.title }}</span>
    <span v-if="extLabel" class="tree-node-ext">{{ extLabel }}</span>
    <div class="tree-node-actions" @click.stop>
      <el-dropdown trigger="click" size="small">
        <button class="more-btn" aria-label="更多操作">
          <el-icon :size="16"><MoreFilled /></el-icon>
        </button>
        <template #dropdown>
          <el-dropdown-menu>
            <el-dropdown-item @click="onOpenRename(data.key)">
              <el-icon><Edit /></el-icon>重命名
            </el-dropdown-item>
            <el-dropdown-item v-if="isInSubDir && onMoveToRoot" @click="onMoveToRoot(data.key, !data.isLeaf)">
              <el-icon><FolderRemove /></el-icon>移动到根目录
            </el-dropdown-item>
            <el-dropdown-item divided @click="onDelete(data.key, !data.isLeaf)">
              <el-icon><Delete /></el-icon><span style="color: var(--dd-err-ink)">删除</span>
            </el-dropdown-item>
          </el-dropdown-menu>
        </template>
      </el-dropdown>
    </div>
  </div>
</template>

<style scoped lang="scss">
.tree-node {
  display: flex;
  // 这一层是真正接 :hover 底色的可点小块（见文件末尾的 .tree-node:hover）→ control 档，
  // 与外层 .el-tree-node__content（ScriptsSidebar.vue 里同样是 control）保持一致，
  // 免得两层底色的圆角错位、在拐角处露出一线深色。
  border-radius: var(--dd-radius-control);
  transition: background-color 0.16s ease;
  align-items: center;
  gap: 10px;
  flex: 1;
  width: 100%;
  min-width: 0;
  padding: 0 2px;
  font-family: var(--dd-font-ui);
  overflow: hidden;
}

/* 文件类型色标：8×8 的小色块，按扩展名取色。
   它【不属于】§4「状态圆点」白名单——那批是 .pulse-dot 这类「运行中 / 有新消息」的状态灯，
   靠圆形才认得出是指示灯；这个只是类型配色标记，与运行状态无关。

   🔴 但也【不能吃 control 档】（v3.1.0 新立的「≤12px 小色标」判据）：
   8×8 的盒子上 control 的 6px 会触发 CSS 圆角等比收缩（6+6=12 > 8 ⇒ 全部夹到 4px），
   结果就是一个正圆，跟状态灯彻底混淆——「不进白名单以便区分形状」的意图在圆角模式下
   自动失效。写死一个小于半边长的 2px，才能在两种 shape 模式下都稳住方形轮廓。 */
.tree-node-dot {
  width: 8px;
  height: 8px;
  border-radius: 2px;
  flex-shrink: 0;
}

.tree-node.is-folder .tree-node-dot {
  // 目录版只放大 1px 并压低透明度，圆角与上面同值（9px 边长同理夹不住 control 档）
  border-radius: 2px;
  width: 9px;
  height: 9px;
  opacity: 0.75;
}

.tree-node-label {
  flex: 1;
  transition: color 0.15s ease;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: 13.5px;
  color: var(--el-text-color-primary);
  letter-spacing: 0.1px;
}

.tree-node.is-folder .tree-node-label {
  font-weight: 500;
}

.tree-node-ext {
  font-size: 9.5px;
  font-weight: 700;
  font-family: var(--dd-font-mono);
  padding: 2px 6px;
  // 扩展名是静态标签，与 el-tag 同归控件类 → control 档
  border-radius: var(--dd-radius-control);
  background: color-mix(in srgb, var(--el-fill-color) 85%, transparent);
  color: var(--el-text-color-secondary);
  flex-shrink: 0;
  letter-spacing: 0.4px;
  line-height: 1.3;
  opacity: 0;
  transition: opacity 0.15s ease;
}

.tree-node-actions {
  opacity: 0;
  transition: opacity 0.15s ease;
  flex-shrink: 0;

  .more-btn {
    cursor: pointer;
    width: 24px;
    height: 24px;
    padding: 0;
    border: none;
    background: transparent;
    // 24×24 的「更多操作」图标按钮属控件类表面 → control 档
    border-radius: var(--dd-radius-control);
    color: var(--el-text-color-secondary);
    display: inline-flex;
    align-items: center;
    justify-content: center;
    transition: background 0.15s, color 0.15s;

    &:hover {
      background: var(--el-fill-color);
      color: var(--dd-accent-ink);
    }

    &:focus-visible {
      outline: 2px solid color-mix(in srgb, var(--el-color-primary) 50%, transparent);
      outline-offset: 1px;
    }
  }
}

.tree-node:hover {
  background: color-mix(in srgb, var(--el-fill-color-light) 86%, transparent);
  .tree-node-label { color: var(--dd-accent-ink); }

  .tree-node-ext,
  .tree-node-actions {
    opacity: 1;
  }
}
</style>
