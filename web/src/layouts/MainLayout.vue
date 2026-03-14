<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { useRouter, useRoute } from 'vue-router'
import { useAuthStore } from '@/stores/auth'
import { useThemeStore } from '@/stores/theme'
import { systemApi } from '@/api/system'

const router = useRouter()
const route = useRoute()
const authStore = useAuthStore()
const themeStore = useThemeStore()
const isCollapsed = ref(false)
const panelTitle = ref('呆呆面板')
const panelIcon = ref('')

const menuItems = computed(() => [
  { index: '/dashboard', title: '仪表板', icon: 'Odometer' },
  { index: '/tasks', title: '定时任务', icon: 'Timer' },
  { index: '/scripts', title: '脚本管理', icon: 'Document' },
  { index: '/envs', title: '环境变量', icon: 'Setting' },
  { index: '/subscriptions', title: '订阅管理', icon: 'Download' },
  { index: '/logs', title: '执行日志', icon: 'Tickets' },
  { index: '/notifications', title: '通知渠道', icon: 'Bell' },
  { index: '/deps', title: '依赖管理', icon: 'Box' },
  { index: '/open-api', title: 'Open API', icon: 'Key' },
  { index: '/users', title: '用户管理', icon: 'UserFilled' },
  { index: '/api-docs', title: '接口文档', icon: 'Connection' },
  { index: '/settings', title: '系统设置', icon: 'SetUp' },
])

const activeMenu = computed(() => route.path)

function handleMenuSelect(index: string) {
  router.push(index)
}

async function handleLogout() {
  await authStore.logout()
}

async function loadPanelSettings() {
  try {
    const res = await systemApi.panelSettings() as any
    if (res.data?.panel_title) panelTitle.value = res.data.panel_title
    if (res.data?.panel_icon) panelIcon.value = res.data.panel_icon
  } catch {}
}

onMounted(loadPanelSettings)
</script>

<template>
  <el-container class="layout-container">
    <el-aside :width="isCollapsed ? '64px' : '220px'" class="layout-aside">
      <div class="logo-area">
        <img :src="panelIcon || '/favicon.svg'" alt="logo" class="logo-img" />
        <span v-show="!isCollapsed" class="logo-text">{{ panelTitle }}</span>
      </div>
      <el-menu
        :default-active="activeMenu"
        :collapse="isCollapsed"
        :collapse-transition="false"
        background-color="transparent"
        @select="handleMenuSelect"
      >
        <el-menu-item v-for="item in menuItems" :key="item.index" :index="item.index">
          <el-icon><component :is="item.icon" /></el-icon>
          <template #title>{{ item.title }}</template>
        </el-menu-item>
      </el-menu>
    </el-aside>

    <el-container>
      <el-header class="layout-header">
        <div class="header-left">
          <el-button :icon="isCollapsed ? 'Expand' : 'Fold'" text @click="isCollapsed = !isCollapsed" />
        </div>
        <div class="header-right">
          <el-button :icon="themeStore.isDark ? 'Sunny' : 'Moon'" text circle @click="themeStore.toggleTheme" />
          <el-dropdown trigger="click">
            <span class="user-dropdown">
              <el-icon><User /></el-icon>
              <span>{{ authStore.user?.username || 'User' }}</span>
            </span>
            <template #dropdown>
              <el-dropdown-menu>
                <el-dropdown-item @click="router.push('/settings')">系统设置</el-dropdown-item>
                <el-dropdown-item divided @click="handleLogout">退出登录</el-dropdown-item>
              </el-dropdown-menu>
            </template>
          </el-dropdown>
        </div>
      </el-header>

      <el-main class="layout-main">
        <router-view v-slot="{ Component, route: viewRoute }">
          <transition name="page-fade" mode="out-in">
            <keep-alive :max="3">
              <component :is="Component" :key="viewRoute.path" />
            </keep-alive>
          </transition>
        </router-view>
      </el-main>
    </el-container>
  </el-container>
</template>

<style scoped lang="scss">
.layout-container {
  height: 100vh;
}

.layout-aside {
  border-right: 1px solid var(--el-border-color-light);
  transition: width 0.25s cubic-bezier(0.25, 0.46, 0.45, 0.94);
  overflow: hidden;
  display: flex;
  flex-direction: column;
  background: var(--el-bg-color);
  will-change: width;
}

.logo-area {
  height: 60px;
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 10px;
  border-bottom: 1px solid var(--el-border-color-light);
  flex-shrink: 0;
  padding: 0 16px;

  .logo-img {
    width: 32px;
    height: 32px;
    transition: transform 0.3s cubic-bezier(0.68, -0.55, 0.265, 1.55);
    will-change: transform;

    &:hover {
      transform: rotate(10deg) scale(1.1);
    }
  }

  .logo-text {
    font-size: 18px;
    font-weight: 700;
    white-space: nowrap;
    background: linear-gradient(135deg, #409EFF, #7B5CFA);
    -webkit-background-clip: text;
    -webkit-text-fill-color: transparent;
    background-clip: text;
  }
}

.layout-header {
  height: 60px;
  display: flex;
  align-items: center;
  justify-content: space-between;
  border-bottom: 1px solid var(--el-border-color-light);
  padding: 0 20px;
  background: var(--el-bg-color);
  backdrop-filter: blur(8px);
}

.header-right {
  display: flex;
  align-items: center;
  gap: 8px;
}

.user-dropdown {
  display: flex;
  align-items: center;
  gap: 6px;
  cursor: pointer;
  padding: 6px 12px;
  border-radius: 8px;
  transition: all 0.2s cubic-bezier(0.25, 0.46, 0.45, 0.94);
  font-weight: 500;

  &:hover {
    background: var(--el-fill-color-light);
    color: var(--el-color-primary);
    transform: translateY(-1px);
  }
}

.layout-main {
  background: var(--el-bg-color-page);
  overflow-y: auto;
  padding: 20px;
}

.page-fade-enter-active {
  animation: pageEnter 0.2s ease-out;
}

.page-fade-leave-active {
  animation: pageFadeOut 0.1s ease-in;
}

@keyframes pageEnter {
  from {
    opacity: 0;
  }
  to {
    opacity: 1;
  }
}

@keyframes pageFadeOut {
  from { opacity: 1; }
  to { opacity: 0; }
}
</style>
