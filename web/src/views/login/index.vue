<script setup lang="ts">
import { ref, onMounted, computed, onUnmounted } from 'vue'
import { useRouter } from 'vue-router'
import { useAuthStore } from '@/stores/auth'
import { useThemeStore } from '@/stores/theme'
import { authApi } from '@/api/auth'
import { ElMessage } from 'element-plus'
import Characters, { type CharacterMood } from './Characters.vue'

const router = useRouter()
const authStore = useAuthStore()
const themeStore = useThemeStore()

const isInit = ref(false)
const checkingInit = ref(true)
const loading = ref(false)
const mood = ref<CharacterMood>('idle')
const mousePos = ref({ x: 0, y: 0 })
const pwdVisible = ref(false)
const focusField = ref<'none' | 'username' | 'password'>('none')
const containerRef = ref<HTMLDivElement>()

const panelVersion = ref('')

const lockCountdown = ref(0)
let lockTimer: ReturnType<typeof setInterval> | null = null

function startLockCountdown(seconds: number) {
  if (lockTimer) clearInterval(lockTimer)
  lockCountdown.value = seconds
  lockTimer = setInterval(() => {
    lockCountdown.value--
    if (lockCountdown.value <= 0) {
      lockCountdown.value = 0
      if (lockTimer) { clearInterval(lockTimer); lockTimer = null }
    }
  }, 1000)
}

onUnmounted(() => {
  if (lockTimer) clearInterval(lockTimer)
})

const form = ref({
  username: '',
  password: ''
})

onMounted(async () => {
  try {
    const res = await authApi.checkInit()
    isInit.value = !res.need_init
  } catch {
    isInit.value = true
  } finally {
    checkingInit.value = false
  }
  try {
    const vRes = await fetch('/api/system/public-version')
    const vData = await vRes.json()
    if (vData.version) panelVersion.value = vData.version
  } catch {}
})

function handleMouseMove(e: MouseEvent) {
  if (!containerRef.value) return
  const rect = containerRef.value.getBoundingClientRect()
  const cx = rect.left + rect.width / 2
  const cy = rect.top + rect.height / 2
  const x = Math.max(-1, Math.min(1, (e.clientX - cx) / (rect.width / 2)))
  const y = Math.max(-1, Math.min(1, (e.clientY - cy) / (rect.height / 2)))
  mousePos.value = { x, y }
}

function handleUsernameFocus() {
  focusField.value = 'username'
  mood.value = 'typing'
  pwdVisible.value = false
}

function handlePasswordFocus() {
  focusField.value = 'password'
  mood.value = pwdVisible.value ? 'peek' : 'password'
}

function handleBlur() {
  focusField.value = 'none'
  if (mood.value !== 'success' && mood.value !== 'error') {
    mood.value = 'idle'
  }
}

function togglePwdVisible() {
  pwdVisible.value = !pwdVisible.value
  if (focusField.value === 'password') {
    mood.value = pwdVisible.value ? 'peek' : 'password'
  }
}

async function handleSubmit() {
  if (!form.value.username || !form.value.password) {
    ElMessage.warning('请输入用户名和密码')
    return
  }

  loading.value = true
  try {
    if (!isInit.value) {
      await authApi.init(form.value.username, form.value.password)
      ElMessage.success('初始化成功')
      isInit.value = true
    }

    await authStore.login(form.value.username, form.value.password)
    mood.value = 'success'
    ElMessage.success('登录成功')
    setTimeout(() => {
      router.push('/')
    }, 600)
  } catch (err: any) {
    mood.value = 'error'
    const data = err?.response?.data
    if (data?.locked && data?.remaining_seconds > 0) {
      startLockCountdown(data.remaining_seconds)
    }
    const msg = data?.error || err?.message || '操作失败'
    ElMessage.error(msg)
    setTimeout(() => {
      mood.value = 'idle'
    }, 2000)
  } finally {
    loading.value = false
  }
}

const titleText = computed(() => isInit.value ? '欢迎回来!' : '初始化管理员')
const subtitleText = computed(() => isInit.value ? '请输入您的登录信息' : '首次使用，请设置管理员账号')
const btnText = computed(() => isInit.value ? '登 录' : '初始化并登录')
</script>

<template>
  <div class="login-page" @mousemove="handleMouseMove">
    <div class="theme-toggle">
      <el-button
        :icon="themeStore.isDark ? 'Sunny' : 'Moon'"
        text
        circle
        size="large"
        class="theme-toggle-btn"
        @click="themeStore.toggleTheme"
      />
    </div>

    <div class="login-container" ref="containerRef">
      <div class="login-left">
        <div class="characters-wrap">
          <Characters :mouseX="mousePos.x" :mouseY="mousePos.y" :mood="mood" />
        </div>
      </div>

      <div class="login-right">
        <div v-if="checkingInit" class="login-loading">
          <span>正在加载...</span>
        </div>
        <template v-else>
          <div class="login-header">
            <div class="login-logo">
              <img src="/favicon.svg" alt="呆呆面板" width="48" height="48" />
            </div>
            <h2>{{ titleText }}</h2>
            <p>{{ subtitleText }}</p>
          </div>

          <el-form @submit.prevent="handleSubmit" class="login-form">
            <el-form-item>
              <el-input
                v-model="form.username"
                placeholder="用户名"
                prefix-icon="User"
                size="large"
                @focus="handleUsernameFocus"
                @blur="handleBlur"
              />
            </el-form-item>
            <el-form-item>
              <el-input
                v-model="form.password"
                :type="pwdVisible ? 'text' : 'password'"
                placeholder="密码"
                prefix-icon="Lock"
                size="large"
                @focus="handlePasswordFocus"
                @blur="handleBlur"
                @keyup.enter="handleSubmit"
              >
                <template #suffix>
                  <el-icon class="pwd-toggle" @click="togglePwdVisible">
                    <View v-if="pwdVisible" />
                    <Hide v-else />
                  </el-icon>
                </template>
              </el-input>
            </el-form-item>
            <el-form-item>
              <el-button
                type="primary"
                size="large"
                :loading="loading"
                :disabled="lockCountdown > 0"
                class="login-btn"
                @click="handleSubmit"
              >
                {{ lockCountdown > 0 ? `${Math.floor(lockCountdown / 60)}:${String(lockCountdown % 60).padStart(2, '0')} 后重试` : btnText }}
              </el-button>
            </el-form-item>
          </el-form>

          <div class="login-version">
            呆呆面板{{ panelVersion ? ` v${panelVersion}` : '' }}
          </div>
        </template>
      </div>
    </div>
  </div>
</template>

<style scoped lang="scss">
.login-page {
  min-height: 100vh;
  display: flex;
  align-items: center;
  justify-content: center;
  background: #eef1f5;
  padding: 24px;
  overflow: hidden;
  position: relative;
  transition: background 0.4s ease;
}

.theme-toggle {
  position: fixed;
  top: 24px;
  right: 24px;
  z-index: 10;

  .theme-toggle-btn {
    width: 44px;
    height: 44px;
    font-size: 20px;
    color: #666;
    background: rgba(255, 255, 255, 0.7);
    backdrop-filter: blur(8px);
    border: 1px solid rgba(0, 0, 0, 0.06);
    transition: all 0.3s;

    &:hover {
      background: rgba(255, 255, 255, 0.9);
      transform: rotate(20deg);
    }
  }
}

.login-container {
  display: flex;
  width: 900px;
  max-width: 100%;
  min-height: 540px;
  border-radius: 24px;
  overflow: hidden;
  box-shadow: 0 12px 40px rgba(0, 0, 0, 0.08), 0 4px 12px rgba(0, 0, 0, 0.04);
  animation: loginSlideUp 0.6s ease-out;
  transition: box-shadow 0.4s ease;
}

@keyframes loginSlideUp {
  from {
    opacity: 0;
    transform: translateY(30px) scale(0.97);
  }
  to {
    opacity: 1;
    transform: translateY(0) scale(1);
  }
}

.login-left {
  flex: 1;
  background: linear-gradient(135deg, #f5f5f5, #e8e8e8);
  display: flex;
  align-items: flex-end;
  justify-content: center;
  position: relative;
  overflow: hidden;
  padding: 40px 20px 0;
  cursor: default;
  transition: background 0.4s ease;
}

.characters-wrap {
  width: 100%;
  max-width: 360px;
  transition: transform 0.1s ease-out;
}

.login-right {
  flex: 1;
  background: #fff;
  display: flex;
  flex-direction: column;
  justify-content: center;
  padding: 48px 40px;
  transition: background 0.4s ease;
}

.login-loading {
  text-align: center;
  padding: 60px 0;
  color: #8c8c8c;
  font-size: 14px;
}

.login-header {
  text-align: center;
  margin-bottom: 32px;

  .login-logo {
    width: 48px;
    height: 48px;
    margin: 0 auto 20px;
    display: flex;
    align-items: center;
    justify-content: center;

    img {
      border-radius: 12px;
    }
  }

  h2 {
    font-size: 26px;
    font-weight: 700;
    color: #1f1f1f;
    margin: 0 0 6px;
    transition: color 0.4s;
  }

  p {
    font-size: 14px;
    color: #8c8c8c;
    margin: 0;
    transition: color 0.4s;
  }
}

.login-form {
  :deep(.el-form-item) {
    margin-bottom: 20px;
  }

  :deep(.el-input__wrapper) {
    border-radius: 10px;
    height: 46px;
    box-shadow: 0 0 0 1px #e0e0e0 inset;
    transition: all 0.3s;

    &:hover {
      box-shadow: 0 0 0 1px #7B5CFA inset;
    }

    &.is-focus {
      box-shadow: 0 0 0 1px #7B5CFA inset, 0 0 0 3px rgba(123, 92, 250, 0.15);
    }
  }
}

.pwd-toggle {
  cursor: pointer;
  color: #8c8c8c;
  transition: color 0.3s;

  &:hover {
    color: #7B5CFA;
  }
}

.login-btn {
  width: 100%;
  height: 46px;
  border-radius: 10px;
  font-weight: 600;
  font-size: 15px;
  background: #1f1f1f;
  border: none;
  transition: all 0.3s;

  &:hover {
    background: #333 !important;
    transform: translateY(-1px);
    box-shadow: 0 4px 12px rgba(0, 0, 0, 0.2);
  }

  &:active {
    transform: translateY(0);
  }
}

.login-version {
  text-align: center;
  margin-top: 16px;
  font-size: 12px;
  color: #bfbfbf;
  transition: color 0.4s;
}

@media (max-width: 768px) {
  .login-container {
    flex-direction: column;
    width: 100%;
    min-height: auto;
  }

  .login-left {
    min-height: 200px;
    padding: 30px 20px 0;
  }

  .characters-wrap {
    max-width: 240px;
  }

  .login-right {
    padding: 32px 24px;
  }
}
</style>

<style lang="scss">
html.dark {
  .login-page {
    background: #1a1a2e;
  }

  .theme-toggle .theme-toggle-btn {
    color: #c0c0c0;
    background: rgba(255, 255, 255, 0.08);
    border-color: rgba(255, 255, 255, 0.1);

    &:hover {
      background: rgba(255, 255, 255, 0.15);
      color: #ffd666;
    }
  }

  .login-container {
    box-shadow: 0 20px 60px rgba(0, 0, 0, 0.4);
  }

  .login-left {
    background: linear-gradient(135deg, #2a2a3e, #232338);
  }

  .login-right {
    background: #1e1e30;
  }

  .login-loading {
    color: #666;
  }

  .login-header {
    h2 {
      color: #e8e8ec;
    }
    p {
      color: #6b6b80;
    }
  }

  .login-form {
    .el-input__wrapper {
      background: #252540;
      box-shadow: 0 0 0 1px #3a3a55 inset;

      &:hover {
        box-shadow: 0 0 0 1px #7B5CFA inset;
      }

      &.is-focus {
        box-shadow: 0 0 0 1px #7B5CFA inset, 0 0 0 3px rgba(123, 92, 250, 0.2);
      }
    }

    .el-input__inner {
      color: #e0e0e8;

      &::placeholder {
        color: #555568;
      }
    }

    .el-input__prefix .el-icon,
    .el-input__suffix .el-icon {
      color: #555568;
    }
  }

  .pwd-toggle {
    color: #555568;

    &:hover {
      color: #9B8AFB;
    }
  }

  .login-btn {
    background: #7B5CFA;

    &:hover {
      background: #6B4CE6 !important;
      box-shadow: 0 4px 16px rgba(123, 92, 250, 0.35);
    }
  }

  .login-version {
    color: #4a4a60;
  }
}
</style>
