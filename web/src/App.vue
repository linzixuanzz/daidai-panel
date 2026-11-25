<script setup lang="ts">
// 应用根：全屏背景层 + 安全模式横幅 + 路由出口。
// 这里第一次用到 theme store（比 MainLayout / 登录页更早），它的 watch(isDark, immediate) 会顺带重推一次编辑器 / 日志底色，
// 与 main.ts 首屏那几次同步预热的结果一致，不会闪。
import { watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import AppBackdrop from '@/components/AppBackdrop.vue'
import { useThemeStore } from '@/stores/theme'

const themeStore = useThemeStore()
const route = useRoute()
const router = useRouter()

// 安全模式开启时给 <html> 挂类与高度变量，让主壳层/登录页自然让位，绝不遮挡移动端顶栏与主题工坊入口
watch(
  () => themeStore.safeMode,
  (active) => {
    if (typeof document === 'undefined') return
    document.documentElement.classList.toggle('dd-safe-mode', active)
    if (active) {
      document.documentElement.style.setProperty('--dd-safe-banner-h', '40px')
    } else {
      document.documentElement.style.removeProperty('--dd-safe-banner-h')
    }
  },
  { immediate: true },
)

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
// 安全模式横幅：改为文档流顶部通栏（配合 --dd-safe-banner-h 扣减主壳层与登录页高度），
// 保证桌面与手机端都不压住顶栏按钮（尤其「主题工坊」自救入口）。
.dd-safe-banner {
  position: relative;
  z-index: 3000;
  min-height: 40px;
  display: flex;
  align-items: center;
  justify-content: center;
  flex-wrap: wrap;
  gap: 8px 12px;
  width: 100%;
  padding: 6px 14px;
  border-bottom: 1px solid var(--el-color-warning);
  background: var(--el-color-warning-light-9);
  color: var(--el-text-color-primary);
  font-size: 13px;
}

.dd-safe-banner__exit {
  flex-shrink: 0;
  padding: 3px 10px;
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
