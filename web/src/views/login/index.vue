<script setup lang="ts">
import { ref, onMounted, computed, onUnmounted, watch } from "vue";
import { useRouter } from "vue-router";
import { useAuthStore } from "@/stores/auth";
import { useThemeStore } from "@/stores/theme";
import { authApi } from "@/api/auth";
import { ElMessage } from "element-plus";
import {
  Hide,
  Key,
  Lock,
  Monitor,
  Moon,
  Sunny,
  User,
  View,
} from "@element-plus/icons-vue";
import Characters, { type CharacterMood } from "./Characters.vue";
import {
  createGeeTestInstance,
  type GeeTestInstance,
  type GeeTestValidateResult,
} from "@/utils/geetest";
import { shouldReduceMotion } from "@/utils/panelAppearance";
import {
  isPasskeyCancelError,
  isPasskeySupported,
  resolvePasskeyErrorMessage,
} from "@/utils/passkey";

const router = useRouter();
const authStore = useAuthStore();
const themeStore = useThemeStore();

const isInit = ref(false);
const checkingInit = ref(true);
const loading = ref(false);
const mood = ref<CharacterMood>("idle");
const mousePos = ref({ x: 0, y: 0 });
const pwdVisible = ref(false);
const focusField = ref<"none" | "username" | "password">("none");
const containerRef = ref<HTMLDivElement>();

const panelVersion = ref("");
const require2FA = ref(false);
const show2FADialog = ref(false);
const totpError = ref("");
const totpVerifying = ref(false);
const captchaConfig = ref({
  enabled: false,
  captcha_id: "",
  configured: false,
  required: false,
  require_after_failures: 0,
  message: "",
});
const captchaVisible = ref(false);
const captchaPreparing = ref(false);
const captchaReady = ref(false);
const captchaVerified = ref(false);
const captchaResult = ref<GeeTestValidateResult | null>(null);
const captchaStatusText = ref("");
let captchaInstance: GeeTestInstance | null = null;
let captchaInitPromise: Promise<GeeTestInstance> | null = null;
let pendingSubmitAfterCaptcha = false;

// 通行密钥（#175）：与后端同一口径，只有 HTTPS 域名或 http://localhost 访问时可用（IP 访问一律不行），页面打开后不会变，取一次即可
const passkeySupported = isPasskeySupported();
const passkeyLoading = ref(false);

const lockCountdown = ref(0);
let lockTimer: ReturnType<typeof setInterval> | null = null;

function startLockCountdown(seconds: number) {
  if (lockTimer) clearInterval(lockTimer);
  lockCountdown.value = seconds;
  lockTimer = setInterval(() => {
    lockCountdown.value--;
    if (lockCountdown.value <= 0) {
      lockCountdown.value = 0;
      if (lockTimer) {
        clearInterval(lockTimer);
        lockTimer = null;
      }
    }
  }, 1000);
}

onUnmounted(() => {
  if (lockTimer) clearInterval(lockTimer);
  resetCaptchaProof();
});

// 在线演示 Demo 构建（npm run build:demo）才为真。
// VITE_DEMO 是编译期常量，发布版构建里恒为 ''，下面这些分支会被整段消除，
// 真实面板的登录页不会有任何预填与文案改动。
const isDemoBuild = import.meta.env.VITE_DEMO === "1";

// 演示站落地页就是这张真实的登录页，账号密码预先填好，
// 访客只需要点一下按钮即可进入面板（后端由浏览器内 mock 层顶替，密码不做校验）。
const form = ref({
  username: isDemoBuild ? "demo" : "",
  password: isDemoBuild ? "demo" : "",
  confirmPassword: "",
  totp_code: "",
});

watch(
  () => form.value.username,
  () => {
    captchaConfig.value.required = false;
    pendingSubmitAfterCaptcha = false;
    resetCaptchaProof();
  },
);

onMounted(async () => {
  try {
    const res = await authApi.checkInit();
    isInit.value = !res.need_init;
  } catch {
    isInit.value = true;
  } finally {
    checkingInit.value = false;
  }
  if (isInit.value) {
    await loadCaptchaConfig(form.value.username);
  }
  try {
    // 在线演示 Demo 分叉：这一发是【裸 fetch】，不经过 axios，所以 demo 的 mock
    // adapter 拦不到它，在静态站上会 404 然后被下面那个空 catch 悄悄吞掉
    // —— 表现就是登录页右下角永远没有版本号。
    //
    // 守卫必须保持「编译期常量 + 动态 import」这个形状：发布版里 VITE_DEMO 是 ''，
    // 整个分支连同 demo chunk 会被 rollup 剔除，真实面板走的还是原来那发 fetch。
    if (import.meta.env.VITE_DEMO === "1") {
      const { demoPublicVersion } = await import("@/demo/shortcuts");
      const version = demoPublicVersion();
      if (version) panelVersion.value = version;
    } else {
      const vRes = await fetch("/api/system/public-version");
      const vData = await vRes.json();
      if (vData.version) panelVersion.value = vData.version;
    }
  } catch {}
});

/**
 * 卡通形象跟随鼠标。
 *
 * 需要减少动效时直接不更新位移 —— 插画保持在中位不动。
 * CSS 那边的 transition 时长虽然会被令牌降到 1ms，但那只是让位移【瞬间完成】，
 * 插画仍然会跟着鼠标跳；对明确要求减少动效的用户来说，这比平滑跟随更难受。
 * 真正要关掉的是位移本身，只能在 JS 层判断。
 *
 * 「需要减少动效」要综合个人设置页的「界面动效」三档（C8），口径见 utils/panelAppearance.ts 的 shouldReduceMotion()。
 */
function handleMouseMove(e: MouseEvent) {
  if (!containerRef.value) return;
  if (shouldReduceMotion()) return;
  const rect = containerRef.value.getBoundingClientRect();
  const cx = rect.left + rect.width / 2;
  const cy = rect.top + rect.height / 2;
  const x = Math.max(-1, Math.min(1, (e.clientX - cx) / (rect.width / 2)));
  const y = Math.max(-1, Math.min(1, (e.clientY - cy) / (rect.height / 2)));
  mousePos.value = { x, y };
}

function resetCaptchaProof(keepVisible = false) {
  captchaVerified.value = false;
  captchaResult.value = null;
  if (captchaInstance) {
    captchaInstance.reset();
  }
  captchaReady.value = Boolean(captchaInstance);
  if (!keepVisible) {
    captchaVisible.value = false;
    captchaStatusText.value = "";
  }
}

async function loadCaptchaConfig(
  username = form.value.username,
  silent = true,
) {
  try {
    const res = await authApi.captchaConfig(username || undefined);
    captchaConfig.value = {
      enabled: res.enabled,
      captcha_id: res.captcha_id || "",
      configured: res.configured,
      required: res.required,
      require_after_failures: res.require_after_failures ?? 0,
      message: res.message || "",
    };

    if (!res.enabled) {
      pendingSubmitAfterCaptcha = false;
      resetCaptchaProof();
      return captchaConfig.value;
    }

    if (!captchaVerified.value) {
      captchaStatusText.value = res.required
        ? "请先完成滑块验证，验证成功后会自动登录"
        : res.message || "";
    }

    return captchaConfig.value;
  } catch (error) {
    if (!silent) {
      ElMessage.error("加载验证码配置失败");
    }
    return null;
  }
}

async function ensureCaptchaInstance() {
  if (captchaInstance) {
    return captchaInstance;
  }
  if (!captchaConfig.value.enabled || !captchaConfig.value.captcha_id) {
    throw new Error("验证码未启用或未配置完整");
  }

  if (!captchaInitPromise) {
    captchaPreparing.value = true;
    captchaInitPromise = createGeeTestInstance(
      {
        captchaId: captchaConfig.value.captcha_id,
        language: "zho",
        product: "bind",
      },
      {
        onReady() {
          captchaReady.value = true;
          captchaStatusText.value = "请拖动滑块完成拼图";
        },
        onSuccess(result) {
          captchaResult.value = result;
          captchaVerified.value = true;
          captchaVisible.value = false;
          captchaStatusText.value = "验证成功，正在登录";
          ElMessage.success("验证成功，正在登录");

          if (pendingSubmitAfterCaptcha) {
            pendingSubmitAfterCaptcha = false;
            void handleSubmit();
          }
        },
        onError(error) {
          captchaVerified.value = false;
          captchaResult.value = null;
          captchaVisible.value = false;
          pendingSubmitAfterCaptcha = false;
          captchaStatusText.value = error.message || "验证码异常，请重试";
          ElMessage.error(captchaStatusText.value);
        },
        onClose() {
          if (captchaVerified.value) return;
          captchaVisible.value = false;
          pendingSubmitAfterCaptcha = false;
          captchaStatusText.value = "验证已取消";
        },
      },
    )
      .then((instance) => {
        captchaInstance = instance;
        return instance;
      })
      .finally(() => {
        captchaPreparing.value = false;
        captchaInitPromise = null;
      });
  }

  return captchaInitPromise;
}

async function triggerCaptcha() {
  captchaVisible.value = true;
  try {
    if (!captchaConfig.value.enabled || !captchaConfig.value.captcha_id) {
      const latest = await loadCaptchaConfig(form.value.username, false);
      if (!latest?.enabled) {
        throw new Error(latest?.message || "验证码未启用");
      }
    }

    if (captchaVerified.value) {
      resetCaptchaProof(true);
    }

    captchaStatusText.value = "请拖动滑块完成拼图";
    const instance = await ensureCaptchaInstance();
    instance.show();
  } catch (error) {
    captchaVisible.value = false;
    throw error;
  }
}

function handleUsernameFocus() {
  focusField.value = "username";
  mood.value = "typing";
  pwdVisible.value = false;
}

function handleUsernameBlur() {
  handleBlur();
  void loadCaptchaConfig(form.value.username);
}

function handlePasswordFocus() {
  focusField.value = "password";
  mood.value = pwdVisible.value ? "peek" : "password";
}

function handleBlur() {
  focusField.value = "none";
  if (mood.value !== "success" && mood.value !== "error") {
    mood.value = "idle";
  }
}

function togglePwdVisible() {
  pwdVisible.value = !pwdVisible.value;
  if (focusField.value === "password") {
    mood.value = pwdVisible.value ? "peek" : "password";
  }
}

function isGatewayLikeErrorStatus(status?: number) {
  return status === 502 || status === 503 || status === 504;
}

function resolveLoginErrorMessage(err: any) {
  const status = err?.response?.status;
  const data = err?.response?.data;

  if (typeof data?.error === "string" && data.error.trim()) {
    return data.error.trim();
  }

  if (isGatewayLikeErrorStatus(status)) {
    return "登录服务暂时不可用或面板正在重启，请稍后重试";
  }

  if (typeof data === "string" && data.trim()) {
    return data.trim();
  }

  return err?.message || "操作失败";
}

function handleTOTPDialogClose() {
  show2FADialog.value = false;
  require2FA.value = false;
  form.value.totp_code = "";
  totpError.value = "";
}

async function handleTOTPSubmit() {
  if (form.value.totp_code.length !== 6) {
    totpError.value = "请输入 6 位数字";
    return;
  }
  totpVerifying.value = true;
  try {
    await handleSubmit();
  } finally {
    totpVerifying.value = false;
  }
}

// 登录成功的收尾：密码登录与通行密钥登录（#175）共用，保证提示、mood、跳转完全一致。
// 两步验证弹窗里「改用通行密钥验证」成功时，也靠这里把弹窗关掉、清掉没用上的验证码。
function handleLoginSuccess() {
  require2FA.value = false;
  show2FADialog.value = false;
  totpError.value = "";
  form.value.totp_code = "";
  captchaConfig.value.required = false;
  pendingSubmitAfterCaptcha = false;
  resetCaptchaProof();
  mood.value = "success";
  ElMessage.success("登录成功");
  setTimeout(() => {
    router.push("/");
  }, 600);
}

// 通行密钥登录（#175）：登录页按钮与两步验证弹窗里的「改用通行密钥验证」都走这里。
// 不走滑块验证、不看登录锁定（后端同样不看）：被别人试密码锁住时，本人仍能用通行密钥进来。
async function handlePasskeyLogin() {
  if (!passkeySupported || passkeyLoading.value) return;
  passkeyLoading.value = true;
  try {
    await authStore.loginWithPasskey();
    handleLoginSuccess();
  } catch (err: any) {
    // 用户在系统弹窗里取消 / 超时：只提示一句，不弹红、不切哭脸
    if (isPasskeyCancelError(err)) {
      ElMessage.info("已取消");
      return;
    }
    mood.value = "error";
    // 接口报错（白名单 403、禁用 403、校验失败 401、会话过期 400 …）按后端 error 文案提示，与密码登录同一套取法；
    // 浏览器自己抛的异常没有 response，交给 passkey 工具翻成中文
    ElMessage.error(
      err?.response
        ? resolveLoginErrorMessage(err)
        : resolvePasskeyErrorMessage(err, "通行密钥登录失败，请重试或改用密码登录"),
    );
    setTimeout(() => {
      mood.value = "idle";
    }, 2000);
  } finally {
    passkeyLoading.value = false;
  }
}

async function handleSubmit() {
  if (!form.value.username || !form.value.password) {
    ElMessage.warning("请输入用户名和密码");
    return;
  }
  if (!isInit.value) {
    if (!form.value.confirmPassword) {
      ElMessage.warning("请再次输入密码");
      return;
    }
    if (form.value.password !== form.value.confirmPassword) {
      ElMessage.error("两次输入的密码不一致");
      return;
    }
  }
  if (require2FA.value && !form.value.totp_code) {
    ElMessage.warning("请输入两步验证码");
    return;
  }

  loading.value = true;
  const submittedCaptcha = captchaResult.value
    ? { ...captchaResult.value }
    : null;
  try {
    if (!isInit.value) {
      await authApi.init(form.value.username, form.value.password);
      ElMessage.success("初始化成功");
      isInit.value = true;
      form.value.confirmPassword = "";
      await loadCaptchaConfig(form.value.username);
    }

    const latestCaptchaConfig = await loadCaptchaConfig(form.value.username);
    if (latestCaptchaConfig?.enabled && !captchaVerified.value) {
      captchaVisible.value = true;
      captchaStatusText.value = "请先完成滑块验证，验证成功后会自动登录";
      pendingSubmitAfterCaptcha = true;
      await triggerCaptcha();
      return;
    }

    await authStore.login(
      form.value.username,
      form.value.password,
      form.value.totp_code,
      submittedCaptcha,
    );
    handleLoginSuccess();
  } catch (err: any) {
    mood.value = "error";
    const data = err?.response?.data;
    const status = err?.response?.status;
    if (data?.two_factor_required) {
      require2FA.value = true;
      show2FADialog.value = true;
      resetCaptchaProof(false);
      pendingSubmitAfterCaptcha = false;
      captchaConfig.value.required = Boolean(captchaConfig.value.enabled);
      if (form.value.totp_code) {
        totpError.value = data?.error || "两步验证码错误";
        form.value.totp_code = "";
      } else {
        totpError.value = "";
      }
    }
    if (data?.locked && data?.remaining_seconds > 0) {
      startLockCountdown(data.remaining_seconds);
    }
    if (data?.captcha_id) {
      captchaConfig.value.captcha_id = data.captcha_id;
    }
    if (typeof data?.require_after_failures === "number") {
      captchaConfig.value.require_after_failures = data.require_after_failures;
    }
    if (data?.captcha_required) {
      captchaConfig.value.enabled = true;
      captchaConfig.value.required = true;
      captchaVisible.value = true;
      pendingSubmitAfterCaptcha = !data?.captcha_service_unavailable;
      if (data?.captcha_service_unavailable) {
        resetCaptchaProof(true);
        captchaStatusText.value =
          data?.error || "验证码服务暂时不可用，请稍后重试";
      } else {
        captchaStatusText.value = data?.captcha_invalid
          ? "验证码已失效，请重新完成滑块验证"
          : "请先完成滑块验证，验证成功后会自动登录";
        resetCaptchaProof(true);
        void triggerCaptcha().catch((err: any) => {
          captchaStatusText.value =
            err?.message || "验证码加载失败，请检查网络后刷新重试";
          ElMessage.error(captchaStatusText.value);
        });
      }
    } else if (submittedCaptcha) {
      resetCaptchaProof(false);
      pendingSubmitAfterCaptcha = false;
    } else if (!data || isGatewayLikeErrorStatus(status)) {
      pendingSubmitAfterCaptcha = false;
    }
    const msg = resolveLoginErrorMessage(err);
    ElMessage.error(msg);
    setTimeout(() => {
      mood.value = "idle";
    }, 2000);
  } finally {
    loading.value = false;
  }
}

const titleText = computed(() => (isInit.value ? "欢迎回来!" : "初始化管理员"));
const subtitleText = computed(() =>
  isInit.value ? "请输入您的登录信息" : "首次使用，请设置管理员账号",
);
const btnText = computed(() => {
  if (!isInit.value) return "初始化并登录";
  // 演示站把主按钮文案换成「进入演示环境」，让访客一眼看懂这里不需要真的注册账号
  return isDemoBuild ? "进入演示环境" : "登 录";
});
// 主题三档（v3.3.2，issue #145）：与顶栏那颗按钮同一套口径，跟随系统时显示显示器图标。
// Monitor 必须在本文件【局部 import】：themeIcon 是返回组件对象的 computed，
// main.ts 的全局注册只对模板里直接写 <Monitor /> 生效，漏了这行登录页会直接报 Monitor is not defined。
const themeIcon = computed(() =>
  themeStore.mode === "system" ? Monitor : themeStore.isDark ? Sunny : Moon,
);
const themeTitle = computed(() => {
  if (themeStore.mode === "system") return "跟随系统 · 点击切换明暗";
  return themeStore.isDark ? "暗夜 · 点击切换明暗" : "明亮 · 点击切换明暗";
});
</script>

<template>
  <div class="login-page" @mousemove="handleMouseMove">
    <div class="theme-toggle">
      <!-- 只改 :icon / :title，别动这里的 DOM 结构：暗色样式挂在下方【非 scoped】style 块的
           `html.dark .theme-toggle .theme-toggle-btn` 上，换成原生 button 会让那段选择器失配 -->
      <el-button
        :icon="themeIcon"
        text
        size="large"
        class="theme-toggle-btn"
        :title="themeTitle"
        :aria-label="themeTitle"
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
          <LoadingMotion
            variant="infinity"
            size="lg"
            label="正在加载..."
            tone="neutral"
          />
        </div>
        <template v-else>
          <div class="login-header">
            <div class="login-logo">
              <img src="/favicon-512.webp" alt="呆呆面板" width="48" height="48" />
            </div>
            <h2>{{ titleText }}</h2>
            <p>{{ subtitleText }}</p>
          </div>

          <el-form @submit.prevent="handleSubmit" class="login-form">
            <el-form-item>
              <el-input
                v-model="form.username"
                placeholder="用户名"
                :prefix-icon="User"
                size="large"
                @focus="handleUsernameFocus"
                @blur="handleUsernameBlur"
              />
            </el-form-item>
            <el-form-item>
              <el-input
                v-model="form.password"
                :type="pwdVisible ? 'text' : 'password'"
                placeholder="密码"
                :prefix-icon="Lock"
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
            <el-form-item v-if="!isInit">
              <el-input
                v-model="form.confirmPassword"
                :type="pwdVisible ? 'text' : 'password'"
                placeholder="确认密码"
                :prefix-icon="Key"
                size="large"
                @focus="handlePasswordFocus"
                @blur="handleBlur"
                @keyup.enter="handleSubmit"
              />
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
                {{
                  lockCountdown > 0
                    ? `${Math.floor(lockCountdown / 60)}:${String(lockCountdown % 60).padStart(2, "0")} 后重试`
                    : btnText
                }}
              </el-button>
            </el-form-item>
            <!-- 通行密钥登录（#175）：初始化管理员时还没有账号，不显示。
                 不支持（IP 访问、或 localhost 以外的 http 访问）时置灰并说明原因，而不是直接藏掉：让用户知道换成 HTTPS 域名就能用。
                 不跟着登录锁定倒计时禁用：锁定是防密码爆破的，通行密钥不受影响。 -->
            <el-form-item v-if="isInit" class="passkey-form-item">
              <el-button
                size="large"
                class="passkey-btn"
                :icon="Key"
                :loading="passkeyLoading"
                :disabled="!passkeySupported || loading"
                @click="handlePasskeyLogin"
              >
                使用通行密钥登录
              </el-button>
              <div v-if="!passkeySupported" class="passkey-hint">
                通行密钥需要通过 HTTPS 域名（或 http://localhost）访问面板
              </div>
            </el-form-item>
          </el-form>

          <!-- 演示站说明：先讲清数据只存在访客自己的浏览器里，避免误以为在操作真实实例 -->
          <p v-if="isDemoBuild" class="login-demo-tip">
            演示数据仅存于你的浏览器，刷新页面即可恢复初始状态
          </p>

          <div class="login-version">
            呆呆面板{{ panelVersion ? ` v${panelVersion}` : "" }}
          </div>
        </template>
      </div>
    </div>

    <!-- 2FA modal: only path to satisfy two_factor_required; no UI bypass. -->
    <el-dialog
      v-model="show2FADialog"
      width="420px"
      align-center
      :close-on-click-modal="false"
      :show-close="false"
      class="totp-dialog"
      @close="handleTOTPDialogClose"
    >
      <template #header>
        <div class="totp-dialog-header">
          <div class="totp-dialog-badge" aria-hidden="true">
            <el-icon :size="18"><Lock /></el-icon>
          </div>
          <div>
            <div class="totp-dialog-title">两步验证</div>
            <div class="totp-dialog-sub">
              请输入认证器 App 上的 6 位动态验证码
            </div>
          </div>
        </div>
      </template>

      <div class="totp-dialog-body">
        <el-input
          v-model="form.totp_code"
          maxlength="6"
          placeholder="6 位数字验证码"
          size="large"
          class="totp-field"
          :autofocus="true"
          @input="totpError = ''"
          @keyup.enter="handleTOTPSubmit"
        />
        <div class="totp-hint" :class="{ 'totp-hint--error': !!totpError }">
          <template v-if="totpError">{{ totpError }}</template>
          <template v-else
            >丢失认证器？请联系管理员在用户管理里重置 2FA。</template
          >
        </div>
        <!-- 已添加通行密钥的用户可以不输验证码，直接走通行密钥登录（#175）；不支持时整个入口不显示 -->
        <div v-if="passkeySupported" class="totp-passkey-switch">
          <el-button
            link
            type="primary"
            :icon="Key"
            :loading="passkeyLoading"
            @click="handlePasskeyLogin"
          >
            改用通行密钥验证
          </el-button>
        </div>
      </div>

      <template #footer>
        <div class="totp-dialog-footer">
          <el-button @click="handleTOTPDialogClose">取消</el-button>
          <el-button
            type="primary"
            :loading="totpVerifying || loading"
            :disabled="form.totp_code.length !== 6"
            class="totp-submit-btn"
            @click="handleTOTPSubmit"
          >
            验证并登录
          </el-button>
        </div>
      </template>
    </el-dialog>
  </div>
</template>

<style scoped lang="scss">
/* ================= 2FA dialog ================= */
:deep(.totp-dialog) {
  .el-dialog {
    border-radius: var(--dd-radius-surface);
    overflow: hidden;
  }

  .el-dialog__header {
    padding: 20px 22px 14px;
    margin: 0;
    border-bottom: 1px solid var(--el-border-color-lighter);
  }

  .el-dialog__body {
    padding: 18px 22px;
  }

  .el-dialog__footer {
    padding: 12px 22px 18px;
  }
}

.totp-dialog-header {
  display: flex;
  align-items: center;
  gap: 12px;
}

.totp-dialog-badge {
  width: 38px;
  height: 38px;
  border-radius: var(--dd-radius-control);
  display: inline-flex;
  align-items: center;
  justify-content: center;
  color: #fff;
  /* 纯色底，去掉渐变与辉光 */
  background: #1677ff;
  flex-shrink: 0;
}

.totp-dialog-title {
  font-size: 16px;
  font-weight: 700;
  color: var(--el-text-color-primary);
}

.totp-dialog-sub {
  font-size: 12px;
  color: var(--el-text-color-secondary);
  margin-top: 2px;
}

.totp-dialog-body {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.totp-field {
  :deep(.el-input__wrapper) {
    border-radius: var(--dd-radius-control);
    padding: 6px 14px;
  }

  :deep(.el-input__inner) {
    font-family: var(--dd-font-mono);
    font-size: 22px;
    letter-spacing: 0.6em;
    text-align: center;
    font-weight: 700;
    padding-left: 0.3em;
  }
}

.totp-hint {
  font-size: 12px;
  color: var(--el-text-color-secondary);
  min-height: 18px;
  line-height: 1.4;
  transition: color var(--dd-motion-fast) var(--dd-ease-standard);

  &--error {
    color: var(--el-color-danger);
    font-weight: 600;
  }
}

.totp-dialog-footer {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
}

/* 提交按钮：纯色底，去掉渐变与辉光，hover 只加深底色；圆角吃控件档令牌 */
.totp-submit-btn {
  border-radius: var(--dd-radius-control);
  padding: 0 18px;
  background: #1677ff;
  border: none;

  &:hover,
  &:focus {
    background: #1565d8;
    border: none;
  }

  &:disabled {
    background: var(--el-color-info-light-5);
    opacity: 0.6;
  }
}

.login-page {
  min-height: 100vh;
  display: flex;
  align-items: center;
  justify-content: center;
  background: #eef1f5;
  padding: 24px;
  overflow: hidden;
  position: relative;
  transition: background var(--dd-motion-slow) var(--dd-ease-standard);
}

.theme-toggle {
  position: fixed;
  top: 24px;
  right: 24px;
  z-index: 10;

  /* 主题切换：普通图标按钮（原 circle），hover 只换底色不上浮；圆角吃控件档令牌 */
  .theme-toggle-btn {
    width: 44px;
    height: 44px;
    padding: 0;
    border-radius: var(--dd-radius-control);
    font-size: 20px;
    color: #666;
    background: rgba(255, 255, 255, 0.7);
    backdrop-filter: blur(8px);
    border: 1px solid rgba(0, 0, 0, 0.06);
    transition:
      color 0.3s,
      background-color 0.3s,
      border-color 0.3s;

    &:hover {
      background: rgba(255, 255, 255, 0.9);
    }
  }
}

/* 登录卡：1px 边框划分层次，不再用投影浮起；圆角吃表面档令牌 */
.login-container {
  display: flex;
  width: 940px;
  max-width: 100%;
  min-height: 560px;
  border-radius: var(--dd-radius-surface);
  overflow: hidden;
  border: 1px solid var(--el-border-color-lighter);
  // 时长/缓动改吃令牌：原来写死 0.6s ease-out，是全站少数几个绕过
  // prefers-reduced-motion 降级的动效之一（那段补丁压不到写死的值）。
  animation: loginSlideUp var(--dd-motion-slow) var(--dd-ease-decelerate);
}

@keyframes loginSlideUp {
  from {
    opacity: 0;
    transform: translateY(30px);
  }
  to {
    opacity: 1;
    transform: translateY(0);
  }
}

/* 插画区底色：纯色（原为上下渐变）；插画本身在 Characters.vue，不受影响 */
.login-left {
  flex: 1;
  background: #f7fafc;
  display: flex;
  align-items: flex-end;
  justify-content: center;
  position: relative;
  overflow: hidden;
  padding: 32px 24px 0;
  cursor: default;
  transition: background var(--dd-motion-slow) var(--dd-ease-standard);
}

.characters-wrap {
  width: 100%;
  max-width: 360px;
  // 卡通形象跟随鼠标的位移。这是【插画元素】，不属于被禁的 hover 形变
  // （design-system 的圆形白名单里也单独放行了登录页插画）。
  // 时长改吃令牌：原来写死 0.1s 会绕过 prefers-reduced-motion 降级，
  // 是全站少数几个「用户明确要求减少动效、它却照动不误」的地方。
  // 令牌降到 1ms 后跟随变成瞬时，配合下面 JS 层的 matchMedia 守卫（不再更新位移），
  // 减少动效模式下这个插画就是静止的。
  transition: transform var(--dd-motion-fast) var(--dd-ease-decelerate);
}

.login-right {
  flex: 1;
  background: #fff;
  display: flex;
  flex-direction: column;
  justify-content: center;
  padding: 52px 44px;
  transition: background var(--dd-motion-slow) var(--dd-ease-standard);
}

.login-loading {
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 60px 0;
  color: #8c8c8c;
  font-size: 14px;
}

// 「检查初始化状态」的 loading 结束后，表单是【硬替换】出现的（v-if / v-else 切换），
// 这是访客看到面板的第一眼，却是全站唯一一处连淡入都没有的主内容切换。
// 这里给出现的两块内容各补一次入场：只用 opacity + translateY，走令牌，
// header 先、form 后错开 60ms（与全站列表页 rise-in 的错落口径一致）。
//
// 不用 <Transition mode="out-in"> 是因为 v-else 那一支是 <template>（多个根节点），
// Transition 要单根；为它包一层 div 会插进 .login-right 的 flex 链，得不偿失。
@keyframes dd-login-content-in {
  from {
    opacity: 0;
    transform: translateY(10px);
  }
  to {
    opacity: 1;
    transform: translateY(0);
  }
}

.login-header {
  text-align: center;
  margin-bottom: 36px;
  animation: dd-login-content-in var(--dd-motion-normal) var(--dd-ease-decelerate) both;

  .login-logo {
    width: 48px;
    height: 48px;
    margin: 0 auto 18px;
    display: flex;
    align-items: center;
    justify-content: center;

    img {
      border-radius: var(--dd-radius-control);
    }
  }

  h2 {
    font-size: 28px;
    font-weight: 700;
    color: #1f1f1f;
    margin: 0 0 8px;
    transition: color var(--dd-motion-slow) var(--dd-ease-standard);
  }

  p {
    font-size: 14px;
    color: #8c8c8c;
    margin: 0;
    transition: color var(--dd-motion-slow) var(--dd-ease-standard);
  }
}

.login-form {
  // 比 header 晚 60ms 出现，形成轻微错落（与全站列表页 rise-in 同一口径）。
  // 60ms 是范式允许的错落延迟上限，reduced-motion 下 global.scss 的补丁会把
  // animation-delay 一并归零，不会出现「先隐身 60ms 再瞬现」的闪烁。
  animation: dd-login-content-in var(--dd-motion-normal) var(--dd-ease-decelerate) both;
  animation-delay: 60ms;

  :deep(.el-form-item) {
    margin-bottom: 18px;
  }

  /* inset 1px 是 Element Plus 输入框的边框实现（功能性，非装饰阴影），保留；
     只去掉聚焦时外扩的 3px 辉光。圆角吃控件档令牌 */
  :deep(.el-input__wrapper) {
    border-radius: var(--dd-radius-control);
    height: 46px;
    box-shadow: 0 0 0 1px #e0e0e0 inset;
    transition: box-shadow var(--dd-motion-normal) var(--dd-ease-standard);

    &:hover {
      box-shadow: 0 0 0 1px #1890ff inset;
    }

    &.is-focus {
      box-shadow: 0 0 0 1px #1890ff inset;
    }
  }
}

.pwd-toggle {
  cursor: pointer;
  color: #8c8c8c;
  transition: color var(--dd-motion-normal) var(--dd-ease-standard);

  &:hover {
    color: #1890ff;
  }
}

/* 登录按钮：纯色底，hover 只加深底色，不上浮不加投影；圆角吃控件档令牌 */
.login-btn {
  width: 100%;
  height: 46px;
  border-radius: var(--dd-radius-control);
  font-weight: 600;
  font-size: 15px;
  background: #1f1f1f;
  border: none;
  transition:
    color 0.3s,
    background-color 0.3s,
    border-color 0.3s;

  &:hover {
    background: #333 !important;
  }
}

/* 通行密钥登录（#175）：次要按钮，白底描边与主按钮的深色实底区分开；尺寸、圆角与主按钮一致 */
.login-form :deep(.passkey-form-item) {
  margin-top: -6px;

  .el-form-item__content {
    flex-direction: column;
    align-items: stretch;
  }
}

.passkey-btn {
  width: 100%;
  height: 46px;
  border-radius: var(--dd-radius-control);
  font-weight: 600;
  font-size: 15px;
}

.passkey-hint {
  margin-top: 6px;
  font-size: 12px;
  line-height: 1.5;
  text-align: center;
  color: var(--el-text-color-secondary);
}

.totp-passkey-switch {
  display: flex;
  justify-content: center;
  padding-top: 2px;
}

/* 演示站说明文案：与版本号同一档次的辅助信息，直角纯文本，不加任何装饰 */
.login-demo-tip {
  text-align: center;
  margin: 12px 0 0;
  font-size: 12px;
  line-height: 1.6;
  color: var(--el-text-color-secondary);
}

.login-version {
  text-align: center;
  margin-top: 16px;
  font-size: 12px;
  color: #bfbfbf;
  transition: color var(--dd-motion-slow) var(--dd-ease-standard);
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
    background: #101828;
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
    border-color: var(--el-border-color-lighter);
  }

  .login-left {
    background: #17212f;
  }

  .login-right {
    background: #111827;
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
    // 同亮色：inset 1px 是输入框边框实现，保留；聚焦不再外扩辉光
    .el-input__wrapper {
      background: #252540;
      box-shadow: 0 0 0 1px #3a3a55 inset;

      &:hover {
        box-shadow: 0 0 0 1px #1890ff inset;
      }

      &.is-focus {
        box-shadow: 0 0 0 1px #1890ff inset;
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
      color: #36cfc9;
    }
  }

  .login-btn {
    background: #1890ff;

    &:hover {
      background: #1677ff !important;
    }
  }

  .login-version {
    color: #4a4a60;
  }
}
</style>
