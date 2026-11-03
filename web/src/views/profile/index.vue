<script setup lang="ts">
import { computed, onActivated, onMounted, onUnmounted, ref, watch } from "vue";
import { ElMessage, ElMessageBox } from "element-plus";
import {
  CircleCheck,
  Key,
  Lock,
  RefreshRight,
  User,
  Clock,
  Avatar,
  Star,
  InfoFilled,
  Camera,
  Delete,
  MagicStick,
  Sunny,
  Document,
} from "@element-plus/icons-vue";
import { authApi } from "@/api/auth";
import {
  applyMotionPreference,
  readMotionPreference,
  type MotionPreference,
  type ThemeMode,
} from "@/utils/panelAppearance";
import { useThemeStore } from "@/stores/theme";
import {
  ensureListPreferencesLoaded,
  readListPreference,
  setListPreference,
} from "@/utils/listPreferences";
import { securityApi } from "@/api/security";
import {
  sponsorApi,
  type SponsorRecord,
  type SponsorSummary,
} from "@/api/sponsor";
import { useAuthStore } from "@/stores/auth";
import { createQrCodeDataUrl } from "@/utils/qrcode";
import { formatDateTime } from "@/utils/datetime";
import { useResponsive } from "@/composables/useResponsive";
import SponsorWall from "./components/SponsorWall.vue";
import SecurityPasskeyCard from "@/views/settings/components/SecurityPasskeyCard.vue";
import { useRouter } from "vue-router";
import { canAdminister } from "@/utils/roles";
import {
  isValidNewPassword,
  NEW_PASSWORD_PLACEHOLDER,
  NEW_PASSWORD_RULE_MESSAGE,
} from "@/utils/password";

const authStore = useAuthStore();
const router = useRouter();
const { dialogFullscreen } = useResponsive();
const activeTab = ref<"profile" | "security" | "sponsors">("profile");

const passwordForm = ref({
  oldPassword: "",
  newPassword: "",
  confirmPassword: "",
});
const passwordSaving = ref(false);

const twoFAEnabled = ref(false);
const twoFASecret = ref("");
const twoFAUri = ref("");
const twoFAQrUrl = ref("");
const twoFACode = ref("");
const showSetup2FA = ref(false);
const twoFALoading = ref(false);
const twoFADisabling = ref(false);

// 通行密钥（#175）：个人资料页只显示个数；null = 还没拉到（或拉取失败，比如老版本后端没有这个接口）
const passkeyCount = ref<number | null>(null);
const showPasskeyDialog = ref(false);

const sponsors = ref<SponsorRecord[]>([]);
const sponsorSummary = ref<SponsorSummary | null>(null);
const sponsorLoading = ref(false);
const sponsorRefreshing = ref(false);
const sponsorFetchedOnce = ref(false);
let sponsorRefreshTimer: ReturnType<typeof setInterval> | null = null;

const sponsorRefreshInterval = 24 * 60 * 60 * 1000;

const roleLabel = computed(() => {
  const role = authStore.user?.role;
  if (role === "admin") return "管理员";
  if (role === "operator") return "运维用户";
  return "只读用户";
});

const roleTagType = computed(() => {
  const role = authStore.user?.role;
  if (role === "admin") return "danger";
  if (role === "operator") return "warning";
  return "info";
});

// 薄封装转调 utils/datetime：注册时间/最近登录/赞助同步时间共用一套口径，
// 空值与无效值由 formatDateTime 统一兜底成 "-"。
function formatTime(value?: string | null) {
  return formatDateTime(value);
}

function buildEmptySponsorSummary(unavailable = false): SponsorSummary {
  return {
    sponsors: [],
    count: 0,
    total_amount: 0,
    updated_at: null,
    unavailable,
  };
}

function applySponsorSummary(summary: SponsorSummary) {
  const normalizedSponsors = Array.isArray(summary.sponsors)
    ? summary.sponsors
    : [];
  sponsors.value = normalizedSponsors;
  sponsorSummary.value = {
    ...buildEmptySponsorSummary(),
    ...summary,
    sponsors: normalizedSponsors,
    count: summary.count || normalizedSponsors.length,
  };
}

async function loadSponsors(options: { silent?: boolean } = {}) {
  const useSilentRefresh = options.silent === true && sponsorFetchedOnce.value;
  if (useSilentRefresh) {
    sponsorRefreshing.value = true;
  } else {
    sponsorLoading.value = true;
  }

  try {
    const res = (await sponsorApi.list()) as any;
    applySponsorSummary({
      ...buildEmptySponsorSummary(),
      ...(res.data || {}),
      sponsors: res.data?.sponsors || [],
    });
  } catch {
    if (!sponsorFetchedOnce.value) {
      applySponsorSummary(buildEmptySponsorSummary(true));
    } else {
      sponsorSummary.value = {
        ...(sponsorSummary.value || buildEmptySponsorSummary()),
        unavailable: true,
      };
    }
  } finally {
    sponsorFetchedOnce.value = true;
    sponsorLoading.value = false;
    sponsorRefreshing.value = false;
  }
}

function stopSponsorRefreshTimer() {
  if (sponsorRefreshTimer) {
    clearInterval(sponsorRefreshTimer);
    sponsorRefreshTimer = null;
  }
}

function startSponsorRefreshTimer() {
  stopSponsorRefreshTimer();
  sponsorRefreshTimer = setInterval(() => {
    void loadSponsors({ silent: true });
  }, sponsorRefreshInterval);
}

async function activateSponsorTab() {
  if (!sponsorFetchedOnce.value) {
    await loadSponsors();
  }
  startSponsorRefreshTimer();
}

async function handleManualSponsorRefresh() {
  await loadSponsors({ silent: sponsorFetchedOnce.value });
}

const sponsorTabHint = computed(() => {
  if (sponsorLoading.value && !sponsorFetchedOnce.value) {
    return "正在同步赞助名单";
  }
  if (sponsorRefreshing.value) {
    return "后台同步中";
  }
  if (sponsorSummary.value?.unavailable && sponsors.value.length > 0) {
    return "当前展示最近一次同步结果";
  }
  if (sponsorSummary.value?.unavailable) {
    return "服务暂不可用，页面会自动重试";
  }
  if (sponsorSummary.value?.updated_at) {
    return `更新于 ${formatTime(sponsorSummary.value.updated_at)}`;
  }
  return "本页每 24 小时静默同步一次";
});

const editingUsername = ref(false);
const newUsername = ref("");
const usernameSaving = ref(false);

function startEditUsername() {
  newUsername.value = authStore.user?.username || "";
  editingUsername.value = true;
}

async function saveUsername() {
  const name = newUsername.value.trim();
  if (!name) {
    ElMessage.warning("用户名不能为空");
    return;
  }
  if (!/^[\p{L}\p{N}_]{1,32}$/u.test(name)) {
    ElMessage.warning("用户名需 1-32 位，支持中文、字母、数字和下划线");
    return;
  }
  if (name === authStore.user?.username) {
    editingUsername.value = false;
    return;
  }
  usernameSaving.value = true;
  try {
    await authApi.changeUsername(name);
    ElMessage.success("用户名修改成功，请重新登录");
    editingUsername.value = false;
    authStore.logout();
  } catch (err: any) {
    ElMessage.error(err?.response?.data?.error || "修改用户名失败");
  } finally {
    usernameSaving.value = false;
  }
}

const usernameInitial = computed(() => {
  const name = authStore.user?.username || "";
  if (!name) return "用";
  return (name[0] ?? "用").toUpperCase();
});

const avatarCacheBuster = ref(Date.now());
const avatarLoadFailed = ref(false);
const hasAvatar = computed(() => Boolean(authStore.user?.avatar_url));
const avatarUrl = computed(() => {
  const url = authStore.user?.avatar_url || "";
  if (!url || avatarLoadFailed.value) return "";
  const separator = url.includes("?") ? "&" : "?";
  return `${url}${separator}t=${avatarCacheBuster.value}`;
});
const avatarUploading = ref(false);
const avatarInputRef = ref<HTMLInputElement | null>(null);

watch(
  () => authStore.user?.avatar_url,
  () => {
    avatarLoadFailed.value = false;
  },
  { immediate: true },
);

function triggerAvatarUpload() {
  avatarInputRef.value?.click();
}

function handleAvatarLoadError() {
  avatarLoadFailed.value = true;
}

async function handleAvatarFileChange(e: Event) {
  const input = e.target as HTMLInputElement;
  const file = input.files?.[0];
  if (!file) return;
  input.value = "";

  const maxSize = 2 * 1024 * 1024;
  if (file.size > maxSize) {
    ElMessage.warning("头像文件不能超过 2MB");
    return;
  }

  const allowed = ["image/jpeg", "image/png", "image/gif", "image/webp"];
  if (!allowed.includes(file.type)) {
    ElMessage.warning("仅支持 JPG、PNG、GIF、WebP 格式");
    return;
  }

  avatarUploading.value = true;
  try {
    await authApi.uploadAvatar(file);
    ElMessage.success("头像上传成功");
    avatarLoadFailed.value = false;
    await authStore.fetchUser();
    avatarCacheBuster.value = Date.now();
  } catch (err: any) {
    ElMessage.error(err?.response?.data?.error || "头像上传失败");
  } finally {
    avatarUploading.value = false;
  }
}

async function handleDeleteAvatar() {
  try {
    await ElMessageBox.confirm("确定要删除当前头像吗？", "确认", {
      type: "warning",
    });
  } catch {
    return;
  }
  try {
    await authApi.deleteAvatar();
    ElMessage.success("头像已删除");
    avatarLoadFailed.value = false;
    await authStore.fetchUser();
  } catch (err: any) {
    ElMessage.error(err?.response?.data?.error || "删除头像失败");
  }
}

async function load2FAStatus() {
  try {
    const res = await securityApi.get2FAStatus();
    twoFAEnabled.value = res.data.enabled;
  } catch {
    ElMessage.error("加载 2FA 状态失败");
  }
}

async function handleChangePassword() {
  if (!passwordForm.value.oldPassword || !passwordForm.value.newPassword) {
    ElMessage.warning("请完整填写密码信息");
    return;
  }
  // 与后端同一规则（只管新密码，旧密码不校验格式）
  if (!isValidNewPassword(passwordForm.value.newPassword)) {
    ElMessage.warning(`新${NEW_PASSWORD_RULE_MESSAGE}`);
    return;
  }
  if (passwordForm.value.newPassword !== passwordForm.value.confirmPassword) {
    ElMessage.warning("两次输入的新密码不一致");
    return;
  }

  passwordSaving.value = true;
  try {
    const res = await authApi.changePassword(
      passwordForm.value.oldPassword,
      passwordForm.value.newPassword,
    );
    // 后端文案会说明通行密钥已一并清除，优先用它
    ElMessage.success(res?.message || "密码修改成功，即将重新登录");
    passwordForm.value = {
      oldPassword: "",
      newPassword: "",
      confirmPassword: "",
    };
    const LOGOUT_DELAY_MS = 1200;
    setTimeout(() => {
      authStore.logout();
    }, LOGOUT_DELAY_MS);
  } catch (err: any) {
    ElMessage.error(err?.response?.data?.error || "密码修改失败");
  } finally {
    passwordSaving.value = false;
  }
}

async function loadPasskeyCount() {
  try {
    const res = await securityApi.listPasskeys();
    passkeyCount.value = Array.isArray(res.data) ? res.data.length : 0;
  } catch {
    // 只是一行摘要，拉不到就显示「未知」，不打扰用户
    passkeyCount.value = null;
  }
}

// 「管理」：管理员跳到设置页「账号安全 → 通行密钥」；
// 其他角色进不了设置页（路由 minRole=admin），就地弹出同一张管理卡片
function openPasskeyManager() {
  if (canAdminister(authStore.user?.role)) {
    void router.push({
      path: "/admin/settings",
      query: { tab: "security", security_tab: "passkeys" },
    });
    return;
  }
  showPasskeyDialog.value = true;
}

async function handleSetup2FA() {
  twoFALoading.value = true;
  try {
    const res = await securityApi.setup2FA();
    twoFASecret.value = res.data.secret;
    twoFAUri.value = res.data.uri;
    twoFAQrUrl.value = await createQrCodeDataUrl(res.data.uri, 220);
    twoFACode.value = "";
    showSetup2FA.value = true;
  } catch (err: any) {
    ElMessage.error(err?.response?.data?.error || "初始化 2FA 失败");
  } finally {
    twoFALoading.value = false;
  }
}

async function handleVerify2FA() {
  if (!twoFACode.value.trim()) {
    ElMessage.warning("请输入验证码");
    return;
  }
  try {
    await securityApi.verify2FA(twoFACode.value.trim());
    ElMessage.success("2FA 已启用");
    twoFAEnabled.value = true;
    showSetup2FA.value = false;
  } catch (err: any) {
    ElMessage.error(err?.response?.data?.error || "验证码错误");
  }
}

async function handleDisable2FA() {
  let prompted: { value: string };
  try {
    prompted = (await ElMessageBox.prompt(
      "为了确认操作本人持有认证器，请输入当前的 6 位动态验证码后再禁用 2FA。",
      "禁用双因素认证",
      {
        inputPattern: /^\d{6}$/,
        inputErrorMessage: "请输入 6 位数字验证码",
        confirmButtonText: "确认禁用",
        cancelButtonText: "取消",
        type: "warning",
        inputPlaceholder: "6 位数字验证码",
        closeOnClickModal: false,
      },
    )) as { value: string };
  } catch {
    return;
  }

  twoFADisabling.value = true;
  try {
    await securityApi.disable2FA(prompted.value.trim());
    twoFAEnabled.value = false;
    ElMessage.success("2FA 已禁用");
  } catch (err: any) {
    ElMessage.error(err?.response?.data?.error || "禁用 2FA 失败");
  } finally {
    twoFADisabling.value = false;
  }
}

// ===== 界面动效（本机偏好，存 localStorage；三档的具体含义见 utils/panelAppearance.ts）=====
const motionOptions: Array<{ value: MotionPreference; label: string }> = [
  { value: "system", label: "跟随系统" },
  { value: "always", label: "始终开启" },
  { value: "reduce", label: "减少动效" },
];
const motionPreference = ref<MotionPreference>(readMotionPreference());
// 系统当前是否开着「减少动态效果」：只用来在说明里提示用户「为什么跟随系统时看不到动画」，
// 真正的降级完全由 global.scss 里的媒体查询决定，这里读不读都不影响效果。
const systemReducedMotion = ref(false);
let reducedMotionQuery: MediaQueryList | null = null;

function setMotionPreference(value: MotionPreference) {
  // applyMotionPreference 会写 localStorage 并立即改 <html> 上的 class，全站即时生效、无需刷新
  motionPreference.value = applyMotionPreference(value);
}

function syncSystemReducedMotion() {
  systemReducedMotion.value = Boolean(reducedMotionQuery?.matches);
}

// ===== 界面主题（v3.3.2 / issue #145，同为本机偏好，三档含义见 utils/panelAppearance.ts）=====
// 顶栏那颗图标按钮只在明/暗之间二态切换，第三档「跟随系统」只有这张卡能选回来。
const themeStore = useThemeStore();
const themeOptions: Array<{ value: ThemeMode; label: string }> = [
  { value: "light", label: "明亮" },
  { value: "dark", label: "暗夜" },
  { value: "system", label: "跟随系统" },
];
// 系统当前是不是深色：只用来在说明里告诉用户「跟随系统此刻会落到哪一档」，
// 真正生效的是 applyThemeMode 写在 <html> 上的 class，这里读不读都不影响效果。
const systemDarkTheme = ref(false);
let darkThemeQuery: MediaQueryList | null = null;

function syncSystemDarkTheme() {
  systemDarkTheme.value = Boolean(darkThemeQuery?.matches);
}

// ===== 日志查看（#147）：跟上面两张不同，这是【账户偏好】，走 /auth/preferences 的 list 组，
// 换浏览器、换设备、APP 里都生效。没设过时网页停在开头（与 v3.2.8 / #133 起一致）。=====
const logOpenOptions: Array<{ value: boolean; label: string }> = [
  { value: false, label: "停在开头（默认）" },
  { value: true, label: "定位到底部" },
];
const logOpenAtBottom = ref(readListPreference("log_open_at_bottom"));

// 点击就写，点的是当前已选中的那一档也写：点击是用户的明确意图，点哪一档就把哪一档写进账户。
// 账户没设过时网页按 false 显示「停在开头」，APP 却按它自己的默认值打开在底部；
// 这时点「停在开头」要是不写，APP 会继续停在底部，用户只能先切到底部再切回来。
// 「占坑」的顾虑只针对挂载 / watch 这类没人动过也自动写默认值的程序化写入，所以写入仍然只放在点击里。
// 🔴 写入只在点击里做，不要改成 watch(logOpenAtBottom)：挂载时按服务端值重读也会触发 watch，多发一次 PUT。
function setLogOpenAtBottom(value: boolean) {
  logOpenAtBottom.value = value;
  setListPreference("log_open_at_bottom", value);
}

// 两个 MediaQueryList 共用这一对生命周期钩子：本文件已经有两个 onMounted 了，不再往上加第三对
onMounted(() => {
  // 老 WebView 可能没有 matchMedia：拿不到就当系统没开，只影响那一行提示文字
  reducedMotionQuery = window.matchMedia?.("(prefers-reduced-motion: reduce)") ?? null;
  syncSystemReducedMotion();
  darkThemeQuery = window.matchMedia?.("(prefers-color-scheme: dark)") ?? null;
  syncSystemDarkTheme();
  // 页面开着时用户也可能去改系统设置，跟着刷新提示
  reducedMotionQuery?.addEventListener("change", syncSystemReducedMotion);
  darkThemeQuery?.addEventListener("change", syncSystemDarkTheme);
});

onUnmounted(() => {
  reducedMotionQuery?.removeEventListener("change", syncSystemReducedMotion);
  reducedMotionQuery = null;
  darkThemeQuery?.removeEventListener("change", syncSystemDarkTheme);
  darkThemeQuery = null;
});

onMounted(async () => {
  // 日志查看卡：setup 时读的是本机缓存，账户里的值拉回来后重读一遍。
  // 用户在 GET 回来之前就点了的话，这一键被记成本地改过、不会被旧值写回，重读拿到的就是他刚选的。
  void ensureListPreferencesLoaded().then(() => {
    logOpenAtBottom.value = readListPreference("log_open_at_bottom");
  });
  if (!authStore.user) {
    try {
      await authStore.fetchUser();
    } catch (err: any) {
      ElMessage.error(
        err?.response?.data?.error || "获取用户信息失败，请重新登录",
      );
      return;
    }
  }
  load2FAStatus();
});

// 通行密钥个数放在 onActivated 里拉：本页在 keep-alive 里，管理员去设置页增删完回来只触发 onActivated；
// 首次挂载时 onActivated 也会跑一次，不用在 onMounted 里重复
onActivated(() => {
  void loadPasskeyCount();
});

watch(activeTab, (tab) => {
  if (tab === "sponsors") {
    void activateSponsorTab();
    return;
  }
  stopSponsorRefreshTimer();
});

onUnmounted(() => {
  stopSponsorRefreshTimer();
});
</script>

<template>
  <div class="profile-page dd-scroll-page">
    <!-- ================= Hero ================= -->
    <header class="profile-hero">
      <div class="profile-hero-aura" aria-hidden="true"></div>

      <div class="profile-hero-main">
        <div class="profile-hero-left">
          <div class="profile-avatar-wrap">
            <div
              class="profile-avatar"
              @click="triggerAvatarUpload"
              :class="{ 'is-uploading': avatarUploading }"
            >
              <img
                v-if="avatarUrl"
                :src="avatarUrl"
                alt="用户头像"
                class="profile-avatar-img"
                @error="handleAvatarLoadError"
              />
              <span v-else class="profile-avatar-initial">{{
                usernameInitial
              }}</span>
              <span class="profile-avatar-ring"></span>
              <div class="profile-avatar-overlay">
                <el-icon :size="18"><Camera /></el-icon>
              </div>
              <input
                ref="avatarInputRef"
                type="file"
                accept="image/jpeg,image/png,image/gif,image/webp"
                class="profile-avatar-input"
                @change="handleAvatarFileChange"
              />
            </div>
            <div class="profile-avatar-camera" @click="triggerAvatarUpload">
              <el-icon :size="12"><Camera /></el-icon>
            </div>
            <el-button
              v-if="hasAvatar"
              class="avatar-delete-btn"
              :icon="Delete"
              size="small"
              @click.stop="handleDeleteAvatar"
              title="删除头像"
            />
          </div>

          <div class="profile-hero-body">
            <h1 class="profile-hero-name">
              {{ authStore.user?.username || "当前用户" }}
            </h1>
            <div class="profile-hero-meta-row">
              <span class="hero-chip hero-chip--green">
                <el-icon :size="13"><Avatar /></el-icon>
                <span>{{ roleLabel }}</span>
              </span>
              <span
                class="hero-chip hero-chip--2fa"
                :class="{ 'hero-chip--2fa-on': twoFAEnabled }"
              >
                <span
                  class="hero-chip-dot"
                  :class="{ 'hero-chip-dot--on': twoFAEnabled }"
                ></span>
                <span>2FA {{ twoFAEnabled ? "已启用" : "未启用" }}</span>
              </span>
            </div>
            <div class="profile-hero-login">
              <el-icon :size="13"><Clock /></el-icon>
              <span
                >最近登录: {{ formatTime(authStore.user?.last_login_at) }}</span
              >
            </div>
          </div>
        </div>

        <div class="profile-hero-badge" aria-hidden="true">
          <svg
            viewBox="0 0 80 96"
            fill="none"
            xmlns="http://www.w3.org/2000/svg"
            class="shield-icon"
          >
            <path
              d="M40 4L8 20V44C8 66 22 86 40 92C58 86 72 66 72 44V20L40 4Z"
              fill="currentColor"
              opacity="0.12"
              stroke="currentColor"
              stroke-width="2"
              stroke-opacity="0.2"
            />
            <path
              d="M34 48L38 52L50 40"
              stroke="currentColor"
              stroke-width="3"
              stroke-linecap="round"
              stroke-linejoin="round"
              opacity="0.3"
            />
          </svg>
        </div>
      </div>
    </header>

    <!-- ================= Body: sidebar tabs + content ================= -->
    <div class="profile-body">
      <!-- Left sidebar tabs -->
      <nav class="profile-sidebar">
        <button
          class="sidebar-tab"
          :class="{ active: activeTab === 'profile' }"
          @click="activeTab = 'profile'"
        >
          <el-icon :size="15"><User /></el-icon>
          <span>基本资料</span>
        </button>
        <button
          class="sidebar-tab"
          :class="{ active: activeTab === 'security' }"
          @click="activeTab = 'security'"
        >
          <el-icon :size="15"><Lock /></el-icon>
          <span>安全设置</span>
        </button>
        <button
          class="sidebar-tab"
          :class="{ active: activeTab === 'sponsors' }"
          @click="activeTab = 'sponsors'"
        >
          <el-icon :size="15"><Star /></el-icon>
          <span>赞助信息</span>
        </button>
      </nav>

      <!-- Right content area -->
      <div class="profile-content">
        <!-- 三个 sidebar-tab 原本是整块硬替换，切换时旧面板凭空消失。
             这里包一层 out-in 过渡：旧面板先淡出，新面板再淡入上移。
             key 绑当前 tab 标识（不是索引），保证任意方向切换都会触发。
             mode="out-in" 也避免了新旧两块内容同时占位把页面撑高一倍。 -->
        <Transition name="dd-profile-panel" mode="out-in">
        <!-- ===== 基本资料 ===== -->
        <div v-if="activeTab === 'profile'" key="profile" class="profile-panel">
          <section class="profile-card">
            <header class="profile-card-header">
              <span class="card-title">
                <el-icon :size="15"><User /></el-icon>
                <span>账户信息</span>
              </span>
            </header>
            <div class="info-grid">
              <div class="info-cell">
                <span class="info-label">用户名</span>
                <div
                  v-if="editingUsername"
                  style="display: flex; align-items: center; gap: 8px"
                >
                  <el-input
                    v-model="newUsername"
                    size="small"
                    style="width: 180px"
                    placeholder="输入新用户名"
                    @keyup.enter="saveUsername"
                  />
                  <el-button
                    size="small"
                    type="primary"
                    :loading="usernameSaving"
                    @click="saveUsername"
                    >保存</el-button
                  >
                  <el-button size="small" @click="editingUsername = false"
                    >取消</el-button
                  >
                </div>
                <span v-else class="info-value">
                  {{ authStore.user?.username || "-" }}
                  <el-button
                    text
                    size="small"
                    style="margin-left: 8px"
                    @click="startEditUsername"
                    >修改</el-button
                  >
                </span>
              </div>
              <div class="info-cell">
                <span class="info-label">角色</span>
                <span class="info-value">
                  <el-tag :type="roleTagType" size="small" effect="light">{{
                    roleLabel
                  }}</el-tag>
                </span>
              </div>
              <div class="info-cell">
                <span class="info-label">注册时间</span>
                <span class="info-value">{{
                  formatTime(authStore.user?.created_at)
                }}</span>
              </div>
              <div class="info-cell">
                <span class="info-label">最近登录</span>
                <span class="info-value">{{
                  formatTime(authStore.user?.last_login_at)
                }}</span>
              </div>
            </div>
          </section>

          <!-- 界面主题（v3.3.2，issue #145）：与界面动效同为纯本机偏好，不进服务端配置
               （理由见 utils/panelAppearance.ts）。顶栏那颗图标按钮点一下只在明/暗之间切，
               第三档「跟随系统」只有这里能选回来。 -->
          <section class="profile-card">
            <header class="profile-card-header">
              <span class="card-title">
                <el-icon :size="15"><Sunny /></el-icon>
                <span>界面主题</span>
              </span>
              <span class="pref-scope">仅对当前浏览器生效</span>
            </header>
            <div class="dd-seg-group" role="radiogroup" aria-label="界面主题">
              <button
                v-for="option in themeOptions"
                :key="option.value"
                type="button"
                role="radio"
                class="dd-seg-btn"
                :class="{ 'is-active': themeStore.mode === option.value }"
                :aria-checked="themeStore.mode === option.value"
                @click="themeStore.setThemeMode(option.value)"
              >
                {{ option.label }}
              </button>
            </div>
            <p class="pref-desc">
              选「跟随系统」后，面板会跟着操作系统的深色模式自动切换；
              点顶栏那颗图标按钮则会脱离跟随，落到固定的明亮或暗夜。
            </p>
            <p class="pref-desc pref-desc--muted">
              当前系统：{{ systemDarkTheme ? "深色模式" : "浅色模式" }}
            </p>
          </section>

          <!-- 界面动效：纯本机偏好，不进服务端配置（理由见 utils/panelAppearance.ts）。
               分段控件用全局的 .dd-seg-group / .dd-seg-btn，与全站「多选一」的控件同一套观感。 -->
          <section class="profile-card">
            <header class="profile-card-header">
              <span class="card-title">
                <el-icon :size="15"><MagicStick /></el-icon>
                <span>界面动效</span>
              </span>
              <span class="pref-scope">仅对当前浏览器生效</span>
            </header>
            <div class="dd-seg-group" role="radiogroup" aria-label="界面动效">
              <button
                v-for="option in motionOptions"
                :key="option.value"
                type="button"
                role="radio"
                class="dd-seg-btn"
                :class="{ 'is-active': motionPreference === option.value }"
                :aria-checked="motionPreference === option.value"
                @click="setMotionPreference(option.value)"
              >
                {{ option.label }}
              </button>
            </div>
            <p class="pref-desc">
              系统开了「减少动态效果」时浏览器默认不播动画，选「始终开启」可覆盖；
              选「减少动效」则无论系统怎么设，动画都压到几乎不可见。
            </p>
            <p class="pref-desc pref-desc--muted">
              当前系统：{{
                systemReducedMotion ? "已开启减少动态效果" : "未开启减少动态效果"
              }}
            </p>
          </section>

          <!-- 日志查看（#147）：与上面两张不同，这是账户偏好（/auth/preferences 的 list 组），APP 读写的也是这一份。 -->
          <section class="profile-card">
            <header class="profile-card-header">
              <span class="card-title">
                <el-icon :size="15"><Document /></el-icon>
                <span>日志查看</span>
              </span>
              <span class="pref-scope">跟随账户，换浏览器、换设备、APP 里都生效</span>
            </header>
            <div class="dd-seg-group" role="radiogroup" aria-label="打开已结束的日志时">
              <button
                v-for="option in logOpenOptions"
                :key="String(option.value)"
                type="button"
                role="radio"
                class="dd-seg-btn"
                :class="{ 'is-active': logOpenAtBottom === option.value }"
                :aria-checked="logOpenAtBottom === option.value"
                @click="setLogOpenAtBottom(option.value)"
              >
                {{ option.label }}
              </button>
            </div>
            <p class="pref-desc">
              打开已经运行结束的任务日志时停在哪里：选「定位到底部」会直接显示最后一行，
              对定时任务的「日志」「日志文件」和执行日志页的「查看」「日志文件」生效。
              运行中的日志始终自动跟随最新输出，不受这项影响。
              没设置过时，APP 仍按原来的习惯打开在底部；在这里选一次后，网页和 APP 就按同一个设置走。
            </p>
          </section>

          <section class="profile-card">
            <header class="profile-card-header">
              <span class="card-title">
                <el-icon :size="15"><InfoFilled /></el-icon>
                <span>安全建议</span>
              </span>
            </header>
            <ul class="tip-list">
              <li>
                <span class="tip-bullet">1</span>
                <span>密码建议至少 12 位，包含大小写、数字和特殊字符。</span>
              </li>
              <li>
                <span class="tip-bullet">2</span>
                <span>启用 2FA 后，即使密码泄露，账户仍有第二层保护。</span>
              </li>
              <li>
                <span class="tip-bullet">3</span>
                <span
                  >禁用 2FA 也需要动态验证码，防止会话被劫持后被人关掉
                  2FA。</span
                >
              </li>
              <li>
                <span class="tip-bullet">4</span>
                <span>修改密码后所有登录（包括当前这个）都会被撤销，需要重新登录；已添加的通行密钥也会一并清除。</span>
              </li>
            </ul>
          </section>
        </div>

        <!-- ===== 安全设置 ===== -->
        <div v-else-if="activeTab === 'security'" key="security" class="profile-panel">
          <section class="profile-card">
            <header class="profile-card-header">
              <span class="card-title">
                <el-icon :size="15"><Lock /></el-icon>
                <span>修改密码</span>
              </span>
            </header>
            <!-- 改密码会撤销所有登录并清掉通行密钥（后端行为），提前说清楚 -->
            <p class="security-form-hint">
              修改密码后需要重新登录，已添加的通行密钥也会一并清除。
            </p>
            <el-form label-position="top" class="security-form">
              <el-form-item label="当前密码">
                <el-input
                  v-model="passwordForm.oldPassword"
                  type="password"
                  show-password
                  placeholder="请输入当前密码"
                />
              </el-form-item>
              <el-form-item label="新密码">
                <el-input
                  v-model="passwordForm.newPassword"
                  type="password"
                  show-password
                  :placeholder="NEW_PASSWORD_PLACEHOLDER"
                />
              </el-form-item>
              <el-form-item label="确认新密码">
                <el-input
                  v-model="passwordForm.confirmPassword"
                  type="password"
                  show-password
                  placeholder="再次输入新密码"
                  @keyup.enter="handleChangePassword"
                />
              </el-form-item>
              <el-form-item>
                <el-button
                  type="primary"
                  :loading="passwordSaving"
                  class="primary-cta"
                  @click="handleChangePassword"
                >
                  <el-icon><Lock /></el-icon>
                  <span>更新密码</span>
                </el-button>
              </el-form-item>
            </el-form>
          </section>

          <section
            class="profile-card profile-card--twofa"
            :class="{ 'is-on': twoFAEnabled }"
          >
            <div class="twofa-halo" aria-hidden="true"></div>
            <header class="profile-card-header">
              <span class="card-title">
                <el-icon :size="15"><Key /></el-icon>
                <span>双因素认证</span>
              </span>
              <span
                class="twofa-status"
                :class="{ 'twofa-status--on': twoFAEnabled }"
              >
                <span class="twofa-status-dot"></span>
                <span>{{ twoFAEnabled ? "已启用" : "未启用" }}</span>
              </span>
            </header>

            <p class="twofa-desc">
              <template v-if="twoFAEnabled">
                你已经开启 2FA，登录时会要求输入认证器应用里的 6
                位动态码。禁用前需要再次输入当前动态码确认操作。
              </template>
              <template v-else>
                启用后，登录除了账号密码还需要输入认证器（Google / Microsoft
                Authenticator 等）生成的 6 位动态码。
              </template>
            </p>

            <div class="twofa-actions">
              <el-button
                v-if="!twoFAEnabled"
                type="primary"
                class="primary-cta"
                :loading="twoFALoading"
                @click="handleSetup2FA"
              >
                <el-icon><Key /></el-icon>
                <span>启用 2FA</span>
              </el-button>
              <el-button
                v-else
                class="danger-outline-btn"
                :loading="twoFADisabling"
                @click="handleDisable2FA"
              >
                <el-icon><Key /></el-icon>
                <span>禁用 2FA（需动态码）</span>
              </el-button>
            </div>

            <!-- 通行密钥（#175）：只放一行摘要 + 管理入口，具体管理在设置页「账号安全 → 通行密钥」 -->
            <div class="passkey-row">
              <el-icon :size="14"><Key /></el-icon>
              <span>
                通行密钥：{{
                  passkeyCount === null ? "未知" : `已添加 ${passkeyCount} 个`
                }}
              </span>
              <span class="passkey-row__sep" aria-hidden="true">·</span>
              <el-button link type="primary" @click="openPasskeyManager">
                管理
              </el-button>
            </div>
          </section>
        </div>

        <!-- ===== 赞助信息 ===== -->
        <div v-else key="sponsors" class="profile-panel">
          <section class="sponsor-panel">
            <div class="sponsor-toolbar">
              <div class="sponsor-toolbar-copy">
                <div class="sponsor-toolbar-title-row">
                  <h3>赞助名单</h3>
                  <span class="sponsor-toolbar-hint">{{ sponsorTabHint }}</span>
                </div>
                <p class="sponsor-toolbar-intro">
                  本项目长期以公益方式维护，感谢以下赞助人员对持续开发、服务开销与后续迭代的支持。
                </p>
              </div>
              <el-button
                class="sponsor-refresh-btn"
                :loading="sponsorLoading || sponsorRefreshing"
                @click="handleManualSponsorRefresh"
              >
                <el-icon><RefreshRight /></el-icon>
                <span>立即刷新</span>
              </el-button>
            </div>

            <SponsorWall
              :sponsors="sponsors"
              :summary="sponsorSummary"
              :loading="sponsorLoading && sponsors.length === 0"
            />
          </section>
        </div>
        </Transition>
      </div>
    </div>

    <!-- ================= 通行密钥管理（非管理员用） =================
         设置页整页只对管理员开放，operator / viewer 点「管理」进不去，
         所以给他们在这里弹出同一张卡片；destroy-on-close 让每次打开都重新拉一次列表 -->
    <el-dialog
      v-model="showPasskeyDialog"
      title="通行密钥"
      width="820px"
      :fullscreen="dialogFullscreen"
      append-to-body
      destroy-on-close
    >
      <SecurityPasskeyCard @changed="passkeyCount = $event" />
    </el-dialog>

    <!-- ================= Setup 2FA dialog ================= -->
    <el-dialog
      v-model="showSetup2FA"
      width="520px"
      :fullscreen="dialogFullscreen"
      :close-on-click-modal="false"
      class="setup-2fa-dialog"
    >
      <template #header>
        <div class="setup-dialog-header">
          <div class="setup-dialog-badge" aria-hidden="true">
            <el-icon :size="16"><Key /></el-icon>
          </div>
          <div>
            <div class="setup-dialog-title">设置双因素认证</div>
            <div class="setup-dialog-sub">
              扫码 / 抄密钥 / 输入验证码，三步开启
            </div>
          </div>
        </div>
      </template>

      <div class="setup-2fa">
        <div class="setup-step">
          <div class="step-head">
            <span class="step-num">1</span>
            <span class="step-title">扫描二维码</span>
          </div>
          <div class="qr-wrapper">
            <img
              v-if="twoFAQrUrl"
              :src="twoFAQrUrl"
              alt="2FA QR Code"
              class="qr-image"
            />
          </div>
          <div class="step-hint">
            推荐使用 Google Authenticator、Microsoft Authenticator 或
            1Password。
          </div>
        </div>

        <div class="setup-step">
          <div class="step-head">
            <span class="step-num">2</span>
            <span class="step-title">或手动输入密钥</span>
          </div>
          <div class="secret-box">
            <code>{{ twoFASecret }}</code>
          </div>
        </div>

        <div class="setup-step">
          <div class="step-head">
            <span class="step-num">3</span>
            <span class="step-title">输入 6 位验证码</span>
          </div>
          <el-input
            v-model="twoFACode"
            maxlength="6"
            placeholder="认证器上的 6 位数字"
            size="large"
            class="totp-input"
            @keyup.enter="handleVerify2FA"
          />
        </div>
      </div>

      <template #footer>
        <div class="setup-dialog-footer">
          <el-button @click="showSetup2FA = false">取消</el-button>
          <el-button type="primary" class="primary-cta" @click="handleVerify2FA"
            >验证并启用</el-button
          >
        </div>
      </template>
    </el-dialog>
  </div>
</template>

<style scoped lang="scss">
.profile-page {
  --profile-accent: #3b82f6;
  --profile-border: var(--el-border-color-lighter);
  --profile-surface: var(--el-bg-color);
  --profile-surface-muted: var(--el-fill-color-light);

  display: flex;
  flex-direction: column;
  gap: 16px;
  font-family: var(--dd-font-ui);
}

/* ================= 入场动画 =================
   克制的淡入上移：hero 与卡片轻微错落进入。
   时长用 --dd-motion-page、缓动用 --dd-ease-decelerate，
   prefers-reduced-motion 时令牌被压到 1ms 自动降级。 */
@keyframes dd-profile-rise-in {
  from {
    opacity: 0;
    transform: translateY(10px);
  }
  to {
    opacity: 1;
    transform: translateY(0);
  }
}

/* 只对三个卡片级容器做，各自只播一次：
   hero / 侧栏 / 内容区都是挂载后不再重建的稳定容器。
   ——不再逐张卡片挂 rise-in：Tab 切换已经由下面的 .dd-profile-panel 过渡接管，
   两者都是「opacity + translateY」，同时跑会叠加位移、且透明度相乘导致内容发灰。 */
.profile-hero,
.profile-sidebar,
.profile-content {
  animation: dd-profile-rise-in var(--dd-motion-page) var(--dd-ease-decelerate)
    both;
}

/* 轻微错落：侧栏稍晚于 hero，内容区再略晚（增量均 ≤60ms） */
.profile-sidebar {
  animation-delay: 50ms;
}

.profile-content {
  animation-delay: 90ms;
}

/* ================= Tab 面板切换过渡 =================
   Transition 没有写 appear，所以首屏不播这段，只有真正点 Tab 才会触发，
   与上面那次入场动画不会撞车。
   进入用 decelerate（内容落位），离场用更短的 standard（尽快让位），
   方向统一为「向下」，与全站 dd-*-rise-in 的语汇一致。 */
.dd-profile-panel-enter-active {
  transition:
    opacity var(--dd-motion-normal) var(--dd-ease-decelerate),
    transform var(--dd-motion-normal) var(--dd-ease-decelerate);
}

.dd-profile-panel-leave-active {
  transition:
    opacity var(--dd-motion-fast) var(--dd-ease-standard),
    transform var(--dd-motion-fast) var(--dd-ease-standard);
}

.dd-profile-panel-enter-from,
.dd-profile-panel-leave-to {
  opacity: 0;
  transform: translateY(8px);
}

/* ================= Hero ================= */
.profile-hero {
  position: relative;
  overflow: hidden;
  padding: 30px 34px;
  /* 纯色底：去掉紫蓝渐变与阴影，层次仅靠 1px 描边。
     注意：暗色下 hero 背景由 global.scss 的 `.profile-hero` 覆盖接管为纯色，与此处一致。
     hero 是整页最外层的容器类表面 → surface 档 */
  border-radius: var(--dd-radius-surface);
  background: var(--profile-surface);
  border: 1px solid var(--profile-border);
}

/* 原右下角紫色光晕已移除，容器保留占位以免模板结构变动 */
.profile-hero-aura {
  display: none;
}

.profile-hero-main {
  position: relative;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 24px;
}

.profile-hero-left {
  display: flex;
  align-items: center;
  gap: 20px;
  min-width: 0;
  flex: 1;
}

.profile-avatar-wrap {
  position: relative;
  flex-shrink: 0;
}

/* 头像：纯色底，hover 只显示遮罩，不再缩放。
   白名单：形状承载语义 —— 这里渲染的是用户上传的头像（与顶栏 .user-avatar 是同一张图），
   头像按圆形构图，方切后人脸/图标会被裁到角上；且同一张头像在顶栏是圆、在这里是方会很割裂。
   两种 shape 模式下都固定圆形，不吃 --dd-radius-* 刻度。 */
.profile-avatar {
  position: relative;
  width: 72px;
  height: 72px;
  border-radius: 50%;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  color: #fff;
  font-family: var(--dd-font-ui);
  font-size: 28px;
  font-weight: 700;
  background: var(--el-color-primary);
  flex-shrink: 0;
  cursor: pointer;
  overflow: hidden;

  &:hover {
    .profile-avatar-overlay {
      opacity: 1;
    }
  }

  &.is-uploading {
    opacity: 0.6;
    pointer-events: none;
  }
}

.profile-avatar-img {
  position: absolute;
  inset: 0;
  width: 100%;
  height: 100%;
  object-fit: cover;
  /* 铺满头像本体，跟随头像走白名单圆形，否则圆形底的四角会露出图片直角 */
  border-radius: 50%;
  z-index: 1;
}

.profile-avatar-overlay {
  position: absolute;
  inset: 0;
  z-index: 2;
  display: flex;
  align-items: center;
  justify-content: center;
  background: rgba(0, 0, 0, 0.4);
  /* hover 遮罩铺满头像本体，跟随头像走白名单圆形 */
  border-radius: 50%;
  opacity: 0;
  // 时长/缓动走令牌：写死毫秒会绕过 prefers-reduced-motion 的降级
  transition: opacity var(--dd-motion-fast) var(--dd-ease-standard);
  color: #fff;
}

.profile-avatar-input {
  display: none;
}

.profile-avatar-camera {
  position: absolute;
  top: -2px;
  right: -2px;
  z-index: 3;
  width: 22px;
  height: 22px;
  /* 22×22 的相机角标是可点的小图标底 → control 档 */
  border-radius: var(--dd-radius-control);
  display: flex;
  align-items: center;
  justify-content: center;
  background: var(--profile-surface);
  /* 去阴影后靠 2px 描边与头像本体分隔 */
  border: 2px solid var(--profile-border);
  color: var(--el-text-color-secondary);
  cursor: pointer;
  transition:
    color var(--dd-motion-fast) var(--dd-ease-standard),
    border-color var(--dd-motion-fast) var(--dd-ease-standard),
    background-color var(--dd-motion-fast) var(--dd-ease-standard);

  &:hover {
    color: var(--profile-accent);
    border-color: var(--profile-accent);
  }
}

/* 删除头像按钮：图标按钮（原 circle），去阴影，靠 1px 描边与头像分隔 */
.avatar-delete-btn {
  position: absolute;
  bottom: -2px;
  right: -2px;
  z-index: 3;
  width: 22px;
  height: 22px;
  min-height: 0;
  padding: 0 !important;
  /* 图标按钮 → control 档；这里的 !important 是为了压住 el-button 自己的圆角，
     不是为了压 --dd-radius-*，令牌值仍随外观开关切换 */
  border-radius: var(--dd-radius-control) !important;
  color: var(--el-color-danger) !important;
  border: 1px solid var(--profile-border) !important;
  background: var(--profile-surface) !important;

  &:hover {
    color: #fff !important;
    background: var(--el-color-danger) !important;
    border-color: var(--el-color-danger) !important;
  }
}

.profile-avatar-initial {
  position: relative;
  z-index: 1;
  letter-spacing: 0.5px;
}

.profile-avatar-ring {
  position: absolute;
  inset: -4px;
  /* 头像外圈环，与头像同心，必须跟随头像走白名单圆形，否则方环套圆头像会露出四角 */
  border-radius: 50%;
  border: 2.5px solid rgba(34, 197, 94, 0.25);
  z-index: 0;
}

.profile-hero-body {
  display: flex;
  flex-direction: column;
  gap: 6px;
  min-width: 0;
  flex: 1;
}

.profile-hero-name {
  margin: 0;
  font-size: 24px;
  font-weight: 700;
  letter-spacing: 0.2px;
  color: var(--el-text-color-primary);
  line-height: 1.2;
}

.profile-hero-meta-row {
  display: flex;
  gap: 8px;
  flex-wrap: wrap;
  margin-top: 4px;
}

.hero-chip {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  height: 24px;
  padding: 0 10px;
  /* 角色 / 2FA 状态 chip，与 global.scss 的 .dd-status-chip 同类 → pill 档 */
  border-radius: var(--dd-radius-pill);
  font-size: 12px;
  font-weight: 500;
  letter-spacing: 0.2px;
  /* 基础 chip 底色改用填充令牌，暗色不串色（实际 chip 均带 --green/--2fa 修饰覆盖此值） */
  background: var(--el-fill-color-light);
  border: 1px solid var(--profile-border);
  color: var(--el-text-color-regular);
}

.hero-chip--green {
  color: #16a34a;
  background: rgba(34, 197, 94, 0.08);
  border-color: rgba(34, 197, 94, 0.22);
}

.hero-chip--2fa {
  background: rgba(245, 108, 108, 0.06);
  border-color: rgba(245, 108, 108, 0.18);
  color: var(--el-color-danger);
}

.hero-chip--2fa-on {
  background: rgba(34, 197, 94, 0.08);
  border-color: rgba(34, 197, 94, 0.22);
  color: #16a34a;
}

/* 2FA 状态点：去掉外圈光晕。
   白名单：形状承载语义 —— 6×6 的状态灯与 global.scss 的 .pulse-dot 同类，
   方化后与旁边文字糊成一小块色斑，认不出是状态灯。两种 shape 模式下都固定圆形。 */
.hero-chip-dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: var(--el-color-danger);
}

.hero-chip-dot--on {
  background: var(--profile-accent);
}

.profile-hero-login {
  display: flex;
  align-items: center;
  gap: 5px;
  margin-top: 2px;
  font-size: 12.5px;
  color: var(--el-text-color-secondary);
}

/* ================= Hero Badge (shield) ================= */
.profile-hero-badge {
  flex-shrink: 0;
  color: var(--profile-accent);
}

.shield-icon {
  width: 72px;
  height: 86px;
}

/* ================= Body layout ================= */
.profile-body {
  display: flex;
  gap: 20px;
  min-height: 0;
}

/* ================= Sidebar tabs ================= */
.profile-sidebar {
  flex-shrink: 0;
  width: 160px;
  display: flex;
  flex-direction: column;
  gap: 4px;
  background: var(--profile-surface);
  /* 1px 边框，与卡片外壳风格统一（不再用阴影浮起）。侧边栏是容器类表面 → surface 档 */
  border: 1px solid var(--profile-border);
  border-radius: var(--dd-radius-surface);
  padding: 12px;
  align-self: flex-start;
}

.sidebar-tab {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 10px 14px;
  /* 侧边栏分段项属控件类表面 → control 档（父级 .profile-sidebar 有 12px 内边距，不贴边，可以吃圆角） */
  border-radius: var(--dd-radius-control);
  border: none;
  background: transparent;
  color: var(--el-text-color-regular);
  font-size: 13.5px;
  font-weight: 500;
  cursor: pointer;
  // 原为 `all 0.2s`：写死毫秒会绕过 prefers-reduced-motion，
  // `all` 还会把 font-weight 这类不该动的属性也带进来。按设计系统只过渡颜色三件套。
  transition:
    background-color var(--dd-motion-fast) var(--dd-ease-standard),
    color var(--dd-motion-fast) var(--dd-ease-standard),
    border-color var(--dd-motion-fast) var(--dd-ease-standard);
  white-space: nowrap;
  text-align: left;

  &:hover {
    background: var(--profile-surface-muted);
    color: var(--el-text-color-primary);
  }

  &.active {
    background: rgba(34, 197, 94, 0.08);
    color: #16a34a;
    font-weight: 600;
  }
}

/* ================= Content area ================= */
.profile-content {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 18px;
}

/* Transition 需要单根节点，所以每个 Tab 的内容包了一层。
   卡片之间的间距从 .profile-content 移到这里（out-in 下 .profile-content 永远只有一个子节点）。 */
.profile-panel {
  display: flex;
  flex-direction: column;
  gap: 18px;
  min-width: 0;
}

/* ================= Cards ================= */
.profile-card {
  position: relative;
  background: var(--profile-surface);
  /* 1px 边框划分层次，不再用阴影浮起。卡片本体是容器类表面 → surface 档 */
  border: 1px solid var(--profile-border);
  border-radius: var(--dd-radius-surface);
  padding: 20px 24px;
  overflow: hidden;
}

.profile-card-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  margin-bottom: 16px;
}

.card-title {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  font-size: 15px;
  font-weight: 700;
  color: var(--el-text-color-primary);
}

/* 偏好卡片（界面主题 / 界面动效 / 日志查看）共用：标题右侧的作用范围说明 + 分段控件下方的说明文字。
   v3.3.2（issue #145）加主题卡时从 .motion-pref-* 改成通用命名，几张卡共用一份样式 */
.pref-scope {
  font-size: 12px;
  color: var(--el-text-color-secondary);
  /* 日志查看卡（#147）的范围说明较长，窄屏会折成两行：靠右对齐，别让第二行缩回左边贴着标题 */
  text-align: right;
}

/* 标题不参与收缩：中文可在任意字间断行，范围说明一长，标题会被挤成「日志查 / 看」两行 */
.profile-card-header .card-title {
  flex-shrink: 0;
}

/* 320px 宽时三个分段项刚好放得下（约 256px / 可用约 260px），留一手换行兜底，不许撑破卡片 */
.profile-card .dd-seg-group {
  max-width: 100%;
  flex-wrap: wrap;
}

.pref-desc {
  margin: 12px 0 0;
  font-size: 12.5px;
  line-height: 1.7;
  color: var(--el-text-color-regular);
}

.pref-desc--muted {
  margin-top: 4px;
  color: var(--el-text-color-secondary);
}

/* Info grid */
.info-grid {
  display: flex;
  flex-direction: column;
  gap: 0;
}

.info-cell {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 12px;
  padding: 11px 0;
  border-bottom: 1px dashed var(--profile-border);
  font-size: 13.5px;

  &:last-child {
    border-bottom: none;
    padding-bottom: 4px;
  }

  &:first-child {
    padding-top: 0;
  }
}

.info-label {
  color: var(--el-text-color-secondary);
  font-size: 13px;
  letter-spacing: 0.2px;
}

.info-value {
  font-weight: 600;
  color: var(--el-text-color-primary);
  text-align: right;
  word-break: break-all;
  font-size: 13px;
}

/* Tips */
.tip-list {
  list-style: none;
  padding: 0;
  margin: 0;
  display: flex;
  flex-direction: column;
  gap: 10px;

  li {
    display: flex;
    gap: 10px;
    padding: 10px 14px;
    /* 每条提示是卡片内独立的内容块（带底色 + 描边），归容器类表面 → surface 档 */
    border-radius: var(--dd-radius-surface);
    background: var(--profile-surface-muted);
    border: 1px solid var(--profile-border);
    font-size: 12.5px;
    line-height: 1.6;
    color: var(--el-text-color-regular);
  }
}

.tip-bullet {
  flex-shrink: 0;
  width: 20px;
  height: 20px;
  /* 20×20 的序号色底属控件类表面 → control 档（不做正圆，避免与状态灯/头像混淆） */
  border-radius: var(--dd-radius-control);
  display: inline-flex;
  align-items: center;
  justify-content: center;
  font-size: 11px;
  font-weight: 700;
  font-family: var(--dd-font-mono);
  color: var(--profile-accent);
  background: rgba(34, 197, 94, 0.12);
}

/* Password */
.security-form-hint {
  margin: 0 0 14px;
  font-size: 13px;
  line-height: 1.7;
  color: var(--el-text-color-secondary);
}

.security-form {
  :deep(.el-form-item) {
    margin-bottom: 16px;
  }

  :deep(.el-form-item__label) {
    font-size: 13px;
    font-weight: 600;
    color: var(--el-text-color-secondary);
  }

  :deep(.el-input__wrapper) {
    /* 输入框属控件类表面 → control 档 */
    border-radius: var(--dd-radius-control);
  }
}

/* 主行动按钮：纯绿色底，去掉渐变与辉光，hover 只加深底色 */
.primary-cta {
  /* 按钮属控件类表面 → control 档 */
  border-radius: var(--dd-radius-control);
  height: 38px;
  padding: 0 20px;
  font-weight: 600;
  background: #22c55e;
  border: none;

  &:hover,
  &:focus {
    background: #16a34a;
    border: none;
  }
}

/* Two-factor card：纯色底，状态差异只体现在描边颜色上 */
.profile-card--twofa {
  border-color: rgba(245, 108, 108, 0.18);
  background: var(--profile-surface);

  &.is-on {
    border-color: rgba(34, 197, 94, 0.22);
    background: var(--profile-surface);
  }
}

/* 原右下角圆形光晕已移除，容器保留占位以免模板结构变动 */
.twofa-halo {
  display: none;
}

.twofa-status {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  height: 24px;
  padding: 0 10px;
  /* 2FA 开关状态 chip，与 .hero-chip / .dd-status-chip 同类 → pill 档 */
  border-radius: var(--dd-radius-pill);
  font-size: 11.5px;
  font-weight: 700;
  font-family: var(--dd-font-mono);
  letter-spacing: 0.5px;
  background: rgba(245, 108, 108, 0.08);
  color: var(--el-color-danger);

  &--on {
    background: rgba(34, 197, 94, 0.1);
    color: #16a34a;
  }
}

/* 白名单：形状承载语义 —— 6×6 状态灯，与 .hero-chip-dot / .pulse-dot 同类，
   方化后认不出是状态灯。两种 shape 模式下都固定圆形。 */
.twofa-status-dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: currentColor;
}

.twofa-desc {
  margin: 0 0 16px;
  font-size: 13px;
  line-height: 1.7;
  color: var(--el-text-color-regular);
}

.twofa-actions {
  display: flex;
  gap: 8px;
  flex-wrap: wrap;
}

/* 通行密钥摘要行（#175）：2FA 操作按钮下方一行辅助信息，用细分隔线与上面隔开 */
.passkey-row {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 6px;
  margin-top: 16px;
  padding-top: 12px;
  border-top: 1px solid var(--el-border-color-lighter);
  font-size: 13px;
  color: var(--el-text-color-regular);
}

.passkey-row__sep {
  color: var(--el-text-color-placeholder);
}

.danger-outline-btn {
  /* 按钮属控件类表面 → control 档 */
  border-radius: var(--dd-radius-control);
  height: 38px;
  padding: 0 18px;
  font-weight: 600;
  color: var(--el-color-danger);
  background: transparent;
  border: 1px solid rgba(245, 108, 108, 0.4);

  &:hover {
    color: #fff;
    background: var(--el-color-danger);
    border-color: var(--el-color-danger);
  }
}

/* ================= Sponsor panel ================= */
.sponsor-panel {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.sponsor-toolbar {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 14px;
  flex-wrap: wrap;
  padding: 18px 22px;
  /* 纯色底：去掉琥珀渐变与阴影，只保留琥珀描边点题。工具条是容器类表面 → surface 档 */
  border-radius: var(--dd-radius-surface);
  border: 1px solid rgba(245, 158, 11, 0.16);
  background: var(--profile-surface);
}

.sponsor-toolbar-copy {
  min-width: 0;
  flex: 1;
}

.sponsor-toolbar-title-row {
  display: flex;
  align-items: baseline;
  gap: 12px;
  flex-wrap: wrap;

  h3 {
    margin: 0;
    font-size: 17px;
    font-weight: 700;
    color: #b45309;
  }
}

.sponsor-toolbar-hint {
  font-size: 11.5px;
  color: #a16207;
}

.sponsor-toolbar-intro {
  margin: 6px 0 0;
  font-size: 12.5px;
  line-height: 1.6;
  color: #78350f;
  max-width: 680px;
}

.sponsor-refresh-btn {
  /* 按钮属控件类表面 → control 档 */
  border-radius: var(--dd-radius-control);
}

/* ================= Setup dialog ================= */
:deep(.setup-2fa-dialog) {
  .el-dialog {
    /* 弹窗外壳是容器类表面 → surface 档 */
    border-radius: var(--dd-radius-surface);
    overflow: hidden;
  }

  .el-dialog__header {
    padding: 18px 22px 14px;
    margin: 0;
    border-bottom: 1px solid var(--profile-border);
  }

  .el-dialog__body {
    padding: 20px 24px;
  }

  .el-dialog__footer {
    padding: 12px 22px 18px;
  }
}

.setup-dialog-header {
  display: flex;
  align-items: center;
  gap: 12px;
}

.setup-dialog-badge {
  width: 36px;
  height: 36px;
  /* 36×36 的图标色底属控件类表面 → control 档 */
  border-radius: var(--dd-radius-control);
  display: inline-flex;
  align-items: center;
  justify-content: center;
  color: #fff;
  background: var(--el-color-primary);
}

.setup-dialog-title {
  font-size: 15px;
  font-weight: 700;
  color: var(--el-text-color-primary);
}

.setup-dialog-sub {
  font-size: 12px;
  color: var(--el-text-color-secondary);
  margin-top: 2px;
}

.setup-2fa {
  display: flex;
  flex-direction: column;
  gap: 20px;
}

.setup-step {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.step-head {
  display: flex;
  align-items: center;
  gap: 8px;
}

.step-num {
  width: 22px;
  height: 22px;
  /* 22×22 的步骤序号色底属控件类表面 → control 档 */
  border-radius: var(--dd-radius-control);
  display: inline-flex;
  align-items: center;
  justify-content: center;
  font-size: 12px;
  font-weight: 700;
  font-family: var(--dd-font-mono);
  color: #fff;
  background: #16a34a;
}

.step-title {
  font-size: 13.5px;
  font-weight: 600;
  color: var(--el-text-color-primary);
}

.step-hint {
  font-size: 12px;
  color: var(--el-text-color-secondary);
  line-height: 1.6;
}

.qr-wrapper {
  display: flex;
  justify-content: center;
  padding: 8px 0;
}

.qr-image {
  width: 220px;
  height: 220px;
  padding: 10px;
  /* 二维码图片容器属容器类表面 → surface 档 */
  border-radius: var(--dd-radius-surface);
  background: #fff;
  border: 1px solid var(--profile-border);
}

.secret-box {
  padding: 14px 16px;
  /* 密钥展示区是弹窗内的独立区块 → surface 档 */
  border-radius: var(--dd-radius-surface);
  background: var(--profile-surface-muted);
  border: 1px dashed var(--profile-border);
  text-align: center;

  code {
    font-family: var(--dd-font-mono);
    font-size: 14.5px;
    font-weight: 700;
    letter-spacing: 0.18em;
    user-select: all;
    color: var(--el-text-color-primary);
    word-break: break-all;
  }
}

.totp-input {
  :deep(.el-input__wrapper) {
    /* 输入框属控件类表面 → control 档 */
    border-radius: var(--dd-radius-control);
  }

  :deep(.el-input__inner) {
    font-family: var(--dd-font-mono);
    font-size: 18px;
    letter-spacing: 0.5em;
    text-align: center;
    font-weight: 600;
  }
}

.setup-dialog-footer {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
}

/* ================= Mobile ================= */
@media (max-width: 768px) {
  .profile-body {
    flex-direction: column;
  }

  .profile-sidebar {
    width: 100%;
    flex-direction: row;
    overflow-x: auto;
    padding: 8px;
    gap: 4px;
  }

  .sidebar-tab {
    padding: 8px 14px;
    font-size: 13px;
  }
}

@media (max-width: 600px) {
  .profile-hero {
    padding: 20px;
  }

  .profile-hero-left {
    flex-direction: column;
    align-items: flex-start;
    gap: 12px;
  }

  .profile-hero-name {
    font-size: 20px;
  }

  .profile-hero-meta-row {
    gap: 6px;
  }

  .hero-chip {
    font-size: 11.5px;
  }

  .profile-card {
    padding: 16px 18px;
  }

  .shield-icon {
    width: 48px;
    height: 58px;
  }
}

/* ================= 暗色修补 =================
   赞助卡的琥珀文字（深棕）在暗色底上对比过低，仅在暗色下提亮，
   不影响明色下的品牌色观感 */
html.dark {
  .sponsor-toolbar-title-row h3 {
    color: #fbbf24;
  }

  .sponsor-toolbar-hint {
    color: #d6a85f;
  }

  .sponsor-toolbar-intro {
    color: var(--el-text-color-regular);
  }
}
</style>
