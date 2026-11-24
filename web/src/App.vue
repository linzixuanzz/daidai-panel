<script setup lang="ts">
// 应用根：全屏背景层 + 安全模式横幅 + 路由出口。
// 这里第一次用到 theme store（比 MainLayout / 登录页更早），它的 watch(isDark, immediate) 会顺带重推一次编辑器 / 日志底色，
// 与 main.ts 首屏那几次同步预热的结果一致，不会闪。
import { useRoute, useRouter } from 'vue-router'
import AppBackdrop from '@/components/AppBackdrop.vue'
import { useThemeStore } from '@/stores/theme'

const themeStore = useThemeStore()
const route = useRoute()
const router = useRouter()

// 退出安全模式（design §1.6）：先恢复注入自定义 CSS，再把地址上的 ?safe=1 去掉，免得刷新又进安全模式
function exitSafeMode() {
  themeStore.exitSafeMode()
  if (route.query.safe !== undefined) {
    const query = { ...route.query }
    delete query.safe
    void router.replace({ query })
  }
}
</script>

<template>
  <AppBackdrop />
  <div v-if="themeStore.safeMode" class="dd-safe-banner" role="status">
    <span>安全模式：已跳过自定义 CSS</span>
    <button type="button" class="dd-safe-banner__exit" @click="exitSafeMode">退出安全模式</button>
  </div>
  <router-view />
</template>

<style scoped lang="scss">
// 安全模式横幅：浮在页面顶部正中，不占布局（登录页与主布局都不用为它让位）。
// z-index 高过 EP 弹窗（2000 起）：安全模式本来就是给「自定义 CSS 把页面弄坏了」的人用的，横幅必须一直看得见。
.dd-safe-banner {
  position: fixed;
  top: 10px;
  left: 50%;
  z-index: 3000;
  display: flex;
  align-items: center;
  gap: 12px;
  max-width: calc(100vw - 24px);
  padding: 8px 10px 8px 14px;
  transform: translateX(-50%);
  border: 1px solid var(--el-color-warning);
  border-radius: var(--dd-radius-control);
  background: var(--el-color-warning-light-9);
  color: var(--el-text-color-primary);
  font-size: 13px;
  box-shadow: var(--el-box-shadow-light);
}

.dd-safe-banner__exit {
  flex-shrink: 0;
  padding: 4px 10px;
  border: 1px solid var(--el-color-warning);
  border-radius: var(--dd-radius-control);
  background: transparent;
  color: var(--el-text-color-primary);
  font: inherit;
  cursor: pointer;
  transition: background-color var(--dd-motion-fast) var(--dd-ease-standard);

  &:hover {
    background: var(--el-color-warning-light-7);
  }
}
</style>
