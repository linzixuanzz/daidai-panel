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
  Document,
  Bell,
  Brush,
} from "@element-plus/icons-vue";
import { authApi } from "@/api/auth";
import {
  type MotionPreference,
  type ThemeMode,
} from "@/utils/panelAppearance";
import { useThemeStore } from "@/stores/theme";
import { useUiStore } from "@/stores/ui";
import {
  getDesktopNotifyEnabled,
  isDesktopNotifySupported,
  setDesktopNotifyEnabled,
} from "@/utils/desktopNotify";
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
  if (role === "operator") return "操作员";
  return "观察者";
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

// ===== 界面外观卡（v4.0）：明暗三档 + 动效四档 + 打开主题工坊 =====
// 风格、主色、背景、模糊、圆角、密度这些细项都挪进了主题工坊（components/ThemeStudio.vue），
// 这张卡只留最常用的两排；两排都是本机偏好（localStorage），读写全部走 useThemeStore。
const themeStore = useThemeStore();
const uiStore = useUiStore();

// 动效四档（旧值 always → 丰富、reduce → 关闭，读取时自动换算，见 utils/panelAppearance.ts）。
// 当前档位直接读 themeStore.motion：主题工坊里改了动效，这里同步变，不再各自存一份。
const motionOptions: Array<{ value: MotionPreference; label: string }> = [
  { value: "system", label: "跟随系统" },
  { value: "rich", label: "丰富" },
  { value: "subtle", label: "克制" },
  { value: "off", label: "关闭" },
];
// 系统当前是否开着「减少动态效果」：只用来在说明里提示用户「为什么跟随系统时看不到动画」，
// 真正的降级完全由 global.scss 里的媒体查询决定，这里读不读都不影响效果。
const systemReducedMotion = ref(false);
let reducedMotionQuery: MediaQueryList | null = null;

function syncSystemReducedMotion() {
  systemReducedMotion.value = Boolean(reducedMotionQuery?.matches);
}

// ===== 桌面通知（v4.0 F10，本机偏好）=====
// 开启时由 utils/desktopNotify.ts 请求浏览器授权，返回的是「实际」状态：用户在授权弹窗里点了拒绝，开关会弹回关闭。
const desktopNotifySupported = isDesktopNotifySupported();
const desktopNotifyEnabled = ref(desktopNotifySupported && getDesktopNotifyEnabled());
const desktopNotifyBusy = ref(false);
// 浏览器的授权状态（granted / denied / default），只用来在说明里回显真实情况
const notifyPermission = ref<string>(
  typeof Notification !== "undefined" ? Notification.permission : "unsupported",
);
// 不支持时说明原因：最常见的是用 http + IP 访问（非安全上下文，浏览器直接不给 Notification）
const desktopNotifyUnsupportedReason = computed(() => {
  if (desktopNotifySupported) return "";
  if (typeof window !== "undefined" && !window.isSecureContext) {
    return "桌面通知需要通过 HTTPS 或 localhost 访问面板，当前地址浏览器不允许发通知。";
  }
  return "当前浏览器不支持桌面通知。";
});
const notifyPermissionLabel = computed(() => {
  if (notifyPermission.value === "granted") return "已允许";
  if (notifyPermission.value === "denied") return "已拒绝";
  if (notifyPermission.value === "default") return "尚未询问";
  return "不可用";
});

async function toggleDesktopNotify(value: string | number | boolean) {
  const wanted = Boolean(value);
  desktopNotifyBusy.value = true;
  try {
    const actual = await setDesktopNotifyEnabled(wanted);
    desktopNotifyEnabled.value = actual;
    if (typeof Notification !== "undefined") {
      notifyPermission.value = Notification.permission;
    }
    // 想开没开成：多半是授权被拒，给出能照着做的提示
    if (wanted && !actual) {
      ElMessage.warning(
        notifyPermission.value === "denied"
          ? "浏览器拒绝了通知权限：请在地址栏左侧的网站设置里允许通知，再回来打开"
          : "没有拿到通知权限，桌面通知未开启",
      );
    }
  } catch {
    desktopNotifyEnabled.value = getDesktopNotifyEnabled();
    ElMessage.error("切换桌面通知失败");
  } finally {
    desktopNotifyBusy.value = false;
  }
}

// ===== 明暗（v3.3.2 / issue #145，本机偏好，三档含义见 utils/panelAppearance.ts）=====
// 顶栏那颗图标按钮只在明/暗之间二态切换，第三档「跟随系统」只有这张卡能选回来。
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
  // 用户可能去浏览器设置里改了通知授权再切回来：按真实状态重新回显
  if (typeof Notification !== "undefined") {
    notifyPermission.value = Notification.permission;
  }
  desktopNotifyEnabled.value = desktopNotifySupported && getDesktopNotifyEnabled();
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
    <header class="profile-hero dd-glass dd-rise" style="--dd-i: 0">
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
      <nav class="profile-sidebar dd-glass dd-rise" style="--dd-i: 1">
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
      <div class="profile-content dd-rise" style="--dd-i: 2">
        <!-- 三个 sidebar-tab 原本是整块硬替换，切换时旧面板凭空消失。
             这里包一层 out-in 过渡：旧面板先淡出，新面板再淡入上移。
             key 绑当前 tab 标识（不是索引），保证任意方向切换都会触发。
             mode="out-in" 也避免了新旧两块内容同时占位把页面撑高一倍。 -->
        <Transition name="dd-profile-panel" mode="out-in">
        <!-- ===== 基本资料 ===== -->
        <div v-if="activeTab === 'profile'" key="profile" class="profile-panel">
          <section class="profile-card dd-glass">
            <header class="profile-card-header">
              <span class="card-title">
                <el-icon :size="15"><User /></el-icon>
                <span>账户信息</span>
              </span>
            </header>
            <div class="info-grid">
              <div class="info-cell" :class="{ 'info-cell--editing': editingUsername }">
                <span class="info-label">用户名</span>
                <div
                  v-if="editingUsername"
                  class="username-edit-row"
                >
                  <el-input
                    v-model="newUsername"
                    size="small"
                    class="username-edit-input"
                    placeholder="输入新用户名"
                    @keyup.enter="saveUsername"
                  />
                  <div class="username-edit-actions">
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

          <!-- 界面外观（v4.0）：明暗三档 + 动效四档 + 打开主题工坊。都是本机偏好，不进服务端配置
               （理由见 utils/panelAppearance.ts）。顶栏那颗图标按钮点一下只在明/暗之间切，
               第三档「跟随系统」只有这里能选回来。风格、主色等细项在主题工坊里。 -->
          <section class="profile-card dd-glass">
            <header class="profile-card-header">
              <span class="card-title">
                <el-icon :size="15"><Brush /></el-icon>
                <span>界面外观</span>
              </span>
              <span class="pref-scope">仅对当前浏览器生效</span>
            </header>

            <div class="pref-row">
              <span class="pref-row-label">明暗</span>
              <div class="dd-seg-group" role="radiogroup" aria-label="明暗">
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
            </div>
            <p class="pref-desc pref-desc--muted">
              「跟随系统」会跟着操作系统的深色模式自动切换，当前系统：{{
                systemDarkTheme ? "深色模式" : "浅色模式"
              }}
            </p>

            <div class="pref-row">
              <span class="pref-row-label">动效</span>
              <div class="dd-seg-group" role="radiogroup" aria-label="界面动效">
                <button
                  v-for="option in motionOptions"
                  :key="option.value"
                  type="button"
                  role="radio"
                  class="dd-seg-btn"
                  :class="{ 'is-active': themeStore.motion === option.value }"
                  :aria-checked="themeStore.motion === option.value"
                  @click="themeStore.setMotion(option.value)"
                >
                  {{ option.label }}
                </button>
              </div>
            </div>
            <p class="pref-desc pref-desc--muted">
              「跟随系统」= 克制（系统开了减少动态效果时为关闭），「丰富」才有背景流动、光斑与整页切换动画，「关闭」无论系统怎么设都不播动画；当前系统：{{
                systemReducedMotion ? "已开启减少动态效果" : "未开启减少动态效果"
              }}
            </p>

            <!-- 流畅模式（v4.0.2，#182）：本机偏好，集中关掉毛玻璃、背景装饰、装饰动画 -->
            <div class="pref-row">
              <span class="pref-row-label">流畅</span>
              <el-switch
                :model-value="themeStore.lite"
                active-text="流畅模式"
                @update:model-value="(value: string | number | boolean) => themeStore.setLite(Boolean(value))"
              />
            </div>
            <p class="pref-desc pref-desc--muted">
              关掉毛玻璃、背景光斑与装饰动画，动效最多到「克制」；没开硬件加速的浏览器、远程桌面、老电脑上推荐打开。{{
                themeStore.litePreference === "auto"
                  ? "当前是检测到设备渲染较慢后自动开启的，手动关掉后不会再自动打开。"
                  : themeStore.litePreference === null
                    ? "从没设置过时会自动检测一次。"
                    : ""
              }}
            </p>

            <div class="pref-studio-row">
              <el-button type="primary" @click="uiStore.openStudio()">
                <el-icon><MagicStick /></el-icon>
                <span>打开主题工坊</span>
              </el-button>
              <span class="pref-desc pref-desc--inline">
                风格、主色、背景、玻璃模糊、圆角、密度都在主题工坊里，改动实时预览。
              </span>
            </div>
          </section>

          <!-- 桌面通知（v4.0 F10）：本机偏好。开启时请求浏览器授权，被拒时开关会弹回关闭并回显真实状态 -->
          <section class="profile-card dd-glass">
            <header class="profile-card-header">
              <span class="card-title">
                <el-icon :size="15"><Bell /></el-icon>
                <span>桌面通知</span>
              </span>
              <span class="pref-scope">仅对当前浏览器生效</span>
            </header>
            <div class="pref-row">
              <el-switch
                :model-value="desktopNotifyEnabled"
                :loading="desktopNotifyBusy"
                :disabled="!desktopNotifySupported"
                active-text="任务连续失败时弹出系统通知"
                @update:model-value="toggleDesktopNotify"
              />
            </div>
            <p v-if="!desktopNotifySupported" class="pref-desc pref-desc--warn">
              {{ desktopNotifyUnsupportedReason }}
            </p>
            <template v-else>
              <p class="pref-desc">
                面板开着（哪怕在后台标签页）时，新出现的失败任务会弹一条系统通知，点击直接打开对应日志；
                同时开着多个标签页也只弹一次。面板关掉后不会推送，需要时请配置通知渠道。
              </p>
              <p
                class="pref-desc pref-desc--muted"
                :class="{ 'pref-desc--warn': notifyPermission === 'denied' }"
              >
                浏览器授权：{{ notifyPermissionLabel }}<template v-if="notifyPermission === 'denied'">
                  ，请在地址栏左侧的网站设置里允许通知后再打开</template>
              </p>
            </template>
          </section>

          <!-- 日志查看（#147）：与上面两张不同，这是账户偏好（/auth/preferences 的 list 组），APP 读写的也是这一份。 -->
          <section class="profile-card dd-glass">
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

          <section class="profile-card dd-glass">
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
          <section class="profile-card dd-glass">
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
            class="profile-card profile-card--twofa dd-glass"
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
            <div class="sponsor-toolbar dd-glass">
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
      <SecurityPasskeyCard embedded @changed="passkeyCount = $event" />
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
  /* v4.0：全部改读主题令牌（styles/themes/）。
     hero / 侧栏 / 卡片 / 赞助工具条在模板上挂了 .dd-glass，底色由它给（半透明 surface + 模糊，
     生效模糊为 0 时 _switches.scss 会把 --dd-surface 换成实色），这里的样式不再写 background，
     否则 scoped 选择器的特异度会压掉 .dd-glass 的底色与「降低透明度」时的实色兜底。 */
  --profile-accent: var(--dd-accent);
  --profile-border: var(--dd-border);
  /* 内部行分隔线、边框与卡片外框解耦：素白（clean）亮色下 --dd-border 为 transparent，
     内部分隔改走 --dd-divider 避免隐身 */
  --profile-divider: var(--dd-divider, var(--dd-border-strong));
  --profile-surface: var(--dd-surface-solid);
  --profile-surface-muted: var(--dd-surface-2);

  display: flex;
  flex-direction: column;
  gap: 16px;
  font-family: var(--dd-font-ui);
}

/* ================= Tab 面板切换过渡 =================
   首次入场交由全局 .dd-rise（backwards 填充）控制；
   Transition 未挂 appear，所以首屏不播这段，只有真正点 Tab 才会触发，不会与首屏入场撞车。
   进入用 decelerate（内容落位），离场用更短的 standard（尽快让位）。 */
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
  /* 资料卡被裁的根因：.profile-page 是固定高度滚动容器（.dd-scroll-page）里的纵向 flex，
     hero 默认 flex-shrink: 1 又带 overflow: hidden，页面内容一超高 hero 就被压扁，
     「最近登录」那一行被裁掉。不许它参与收缩，超出的高度交给外层滚动。 */
  flex-shrink: 0;
  padding: 30px 34px;
  /* 底色由模板上的 .dd-glass 给，层次靠描边。hero 是整页最外层的容器类表面 → surface 档 */
  border-radius: var(--dd-radius-surface);
  border: var(--dd-border-width) solid var(--profile-border);
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
  /* 头像占位与顶栏头像同一组令牌（G1 的 MainLayout 也读这两个） */
  color: var(--dd-btn-primary-fg);
  font-family: var(--dd-font-ui);
  font-size: 28px;
  font-weight: 700;
  background: var(--dd-btn-primary-bg);
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
  /* 遮罩取正文色半透明：亮色主题是深色遮罩，暗色主题是浅色遮罩；图标取卡底色保证反差 */
  background: color-mix(in srgb, var(--dd-text) 45%, transparent);
  /* hover 遮罩铺满头像本体，跟随头像走白名单圆形 */
  border-radius: 50%;
  opacity: 0;
  // 时长/缓动走令牌：写死毫秒会绕过 prefers-reduced-motion 的降级
  transition: opacity var(--dd-motion-fast) var(--dd-ease-standard);
  color: var(--dd-surface-solid);
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
  border: 2px solid var(--profile-divider);
  color: var(--dd-text-2);
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
  color: var(--dd-err-ink) !important;
  border: 1px solid var(--profile-divider) !important;
  background: var(--profile-surface) !important;

  &:hover {
    color: var(--dd-on-status) !important;
    background: var(--dd-err-solid) !important;
    border-color: var(--dd-err-solid) !important;
  }
}

.profile-avatar-initial {
  position: relative;
  z-index: 1;
  line-height: 1;
  letter-spacing: 0.5px;
}

.profile-avatar-ring {
  position: absolute;
  /* 与 72×72 头像盒体齐平：原 inset: -4px 会在 overflow: hidden 内部造成 4px 溢出且环本身被裁切 */
  inset: 0;
  pointer-events: none;
  /* 头像同心环，跟随头像走白名单圆形，否则方环套圆头像会露出四角 */
  border-radius: 50%;
  border: 2.5px solid var(--dd-accent-ring);
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
  color: var(--dd-text);
  font-family: var(--dd-font-head, inherit);
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
  /* 基础 chip 底色（实际 chip 均带 --green/--2fa 修饰覆盖此值） */
  background: var(--profile-surface-muted);
  border: 1px solid var(--profile-border);
  color: var(--dd-text-2);
}

/* 角色 chip（类名沿用旧的 --green）：角色不是状态，v4.0 起跟主色走；文字用墨色令牌保证对比度 */
.hero-chip--green {
  color: var(--dd-accent-ink);
  background: var(--dd-accent-soft);
  border-color: var(--dd-accent-ring);
}

.hero-chip--2fa {
  background: color-mix(in srgb, var(--dd-err) 8%, transparent);
  border-color: color-mix(in srgb, var(--dd-err) 22%, transparent);
  color: var(--dd-err-ink);
}

.hero-chip--2fa-on {
  background: color-mix(in srgb, var(--dd-ok) 10%, transparent);
  border-color: color-mix(in srgb, var(--dd-ok) 26%, transparent);
  color: var(--dd-ok-ink);
}

/* 2FA 状态点：去掉外圈光晕。
   白名单：形状承载语义 —— 6×6 的状态灯与 global.scss 的 .pulse-dot 同类，
   方化后与旁边文字糊成一小块色斑，认不出是状态灯。两种 shape 模式下都固定圆形。 */
.hero-chip-dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: var(--dd-err);
}

.hero-chip-dot--on {
  background: var(--dd-ok);
}

.profile-hero-login {
  display: flex;
  align-items: center;
  gap: 5px;
  margin-top: 2px;
  font-size: 12.5px;
  color: var(--dd-text-2);
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
  /* 底色由模板上的 .dd-glass 给。描边与卡片外壳统一。侧边栏是容器类表面 → surface 档 */
  border: var(--dd-border-width) solid var(--profile-border);
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
  color: var(--dd-text-2);
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
    color: var(--dd-text);
  }

  /* 选中项与侧栏导航同一组排版级令牌（素白是灰色药丸，其余主题是主色淡底） */
  &.active {
    background: var(--dd-nav-active-bg);
    color: var(--dd-nav-active-fg);
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
  /* 底色由模板上的 .dd-glass 给；描边划分层次。卡片本体是容器类表面 → surface 档 */
  border: var(--dd-border-width) solid var(--profile-border);
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
  font-size: var(--dd-card-title-size, 15px);
  font-weight: var(--dd-card-title-weight, 700);
  color: var(--dd-text);

  > .el-icon {
    display: var(--dd-card-title-icon-display);
  }
}

/* 偏好卡片（界面主题 / 界面动效 / 日志查看）共用：标题右侧的作用范围说明 + 分段控件下方的说明文字。
   v3.3.2（issue #145）加主题卡时从 .motion-pref-* 改成通用命名，几张卡共用一份样式 */
.pref-scope {
  font-size: 12px;
  color: var(--dd-text-2);
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
  color: var(--dd-text-2);
}

.pref-desc--muted {
  margin-top: 4px;
  color: var(--dd-text-3);
}

/* 授权被拒 / 浏览器不支持：用警告墨色，提醒用户这不是正常状态 */
.pref-desc--warn {
  color: var(--dd-warn-ink);
}

/* 界面外观卡（v4.0）：一行一个「标签 + 分段控件」 */
.pref-row {
  display: flex;
  align-items: center;
  gap: 12px;
  flex-wrap: wrap;
}

/* 第二行（动效）与上一行的说明文字之间拉开距离 */
.pref-desc + .pref-row {
  margin-top: 16px;
}

.pref-row-label {
  flex-shrink: 0;
  min-width: 36px;
  font-size: 13px;
  font-weight: 600;
  color: var(--dd-text-2);
}

.pref-studio-row {
  display: flex;
  align-items: center;
  gap: 12px;
  flex-wrap: wrap;
  margin-top: 18px;
  padding-top: 16px;
  border-top: 1px solid var(--profile-divider);
}

.pref-desc--inline {
  margin: 0;
  flex: 1;
  min-width: 200px;
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
  border-bottom: 1px dashed var(--profile-divider);
  font-size: 13.5px;

  &:last-child {
    border-bottom: none;
    padding-bottom: 4px;
  }

  &:first-child {
    padding-top: 0;
  }
}

.username-edit-row {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  flex-wrap: wrap;
  gap: 8px;
  min-width: 0;

  :deep(.el-button + .el-button) {
    margin-left: 0;
  }
}

.username-edit-input {
  width: 180px;
  max-width: 100%;
}

.username-edit-actions {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  flex-shrink: 0;

  :deep(.el-button + .el-button) {
    margin-left: 0;
  }
}

.info-label {
  color: var(--dd-text-2);
  font-size: 13px;
  letter-spacing: 0.2px;
  flex-shrink: 0;
}

.info-value {
  font-weight: 600;
  color: var(--dd-text);
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
    border: 1px solid var(--profile-divider);
    font-size: 12.5px;
    line-height: 1.6;
    color: var(--dd-text-2);
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
  color: var(--dd-accent-ink);
  background: var(--dd-accent-soft);
}

/* Password */
.security-form-hint {
  margin: 0 0 14px;
  font-size: 13px;
  line-height: 1.7;
  color: var(--dd-text-2);
}

.security-form {
  :deep(.el-form-item) {
    margin-bottom: 16px;
  }

  :deep(.el-form-item__label) {
    font-size: 13px;
    font-weight: 600;
    color: var(--dd-text-2);
  }

  :deep(.el-input__wrapper) {
    /* 输入框属控件类表面 → control 档 */
    border-radius: var(--dd-radius-control);
  }
}

/* 主行动按钮：v4.0 起不再写死绿色，底色 / 文字色交给主按钮令牌（--dd-btn-primary-*，已接到 .el-button--primary），
   圆角交给 --dd-btn-radius（素白是胶囊），这里只管尺寸 */
.primary-cta {
  height: 38px;
  padding: 0 20px;
  font-weight: 600;
}

/* Two-factor card：纯色底，状态差异只体现在描边颜色上 */
.profile-card--twofa {
  border-color: color-mix(in srgb, var(--dd-err) 26%, var(--profile-border));

  &.is-on {
    border-color: color-mix(in srgb, var(--dd-ok) 30%, var(--profile-border));
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
  background: color-mix(in srgb, var(--dd-err) 8%, transparent);
  color: var(--dd-err-ink);

  &--on {
    background: color-mix(in srgb, var(--dd-ok) 10%, transparent);
    color: var(--dd-ok-ink);
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
  color: var(--dd-text-2);
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
  border-top: 1px solid var(--profile-divider);
  font-size: 13px;
  color: var(--dd-text-2);
}

.passkey-row__sep {
  color: var(--dd-text-3);
}

.danger-outline-btn {
  /* 按钮圆角与全站按钮同一个令牌（素白是胶囊） */
  border-radius: var(--dd-btn-radius, var(--dd-radius-control));
  height: 38px;
  padding: 0 18px;
  font-weight: 600;
  color: var(--dd-err-ink);
  background: transparent;
  border: 1px solid color-mix(in srgb, var(--dd-err) 40%, transparent);

  &:hover {
    color: var(--dd-on-status);
    background: var(--dd-err-solid);
    border-color: var(--dd-err-solid);
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
  /* 底色由模板上的 .dd-glass 给，琥珀（警告色）描边点题。工具条是容器类表面 → surface 档 */
  border-radius: var(--dd-radius-surface);
  border: var(--dd-border-width) solid color-mix(in srgb, var(--dd-warn) 24%, var(--profile-border));
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
    color: var(--dd-warn-ink);
  }
}

.sponsor-toolbar-hint {
  font-size: 11.5px;
  color: var(--dd-warn-ink);
}

.sponsor-toolbar-intro {
  margin: 6px 0 0;
  font-size: 12.5px;
  line-height: 1.6;
  color: var(--dd-text-2);
  max-width: 680px;
}

.sponsor-refresh-btn {
  /* 按钮属控件类表面 → control 档 */
  border-radius: var(--dd-radius-control);
}

/* ================= Setup dialog ================= */
:deep(.el-dialog.setup-2fa-dialog) {
  /* 弹窗外壳是容器类表面 → surface 档 */
  border-radius: var(--dd-radius-surface);
  overflow: hidden;

  .el-dialog__header {
    padding: 18px 22px 14px;
    margin: 0;
    border-bottom: 1px solid var(--dd-divider, var(--dd-border-strong));
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
  /* 走主按钮高对比成对令牌，确保 14 套配色全部 ≥ 4.5:1 */
  color: var(--dd-btn-primary-fg);
  background: var(--dd-btn-primary-bg);
}

.setup-dialog-title {
  font-size: 15px;
  font-weight: 700;
  color: var(--dd-text);
}

.setup-dialog-sub {
  font-size: 12px;
  color: var(--dd-text-2);
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
  /* 走主按钮高对比成对令牌，避免亮主色压白字对比度不足 */
  color: var(--dd-btn-primary-fg);
  background: var(--dd-btn-primary-bg);
}

.step-title {
  font-size: 13.5px;
  font-weight: 600;
  color: var(--dd-text);
}

.step-hint {
  font-size: 12px;
  color: var(--dd-text-2);
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
  /* 唯一保留的写死颜色：二维码必须是白底深码，扫码软件才认得出（暗色主题下也一样），不跟主题走 */
  background: #fff;
  border: 1px solid var(--dd-divider, var(--dd-border-strong));
}

.secret-box {
  padding: 14px 16px;
  /* 密钥展示区是弹窗内的独立区块 → surface 档 */
  border-radius: var(--dd-radius-surface);
  background: var(--profile-surface-muted);
  border: 1px dashed var(--dd-divider, var(--dd-border-strong));
  text-align: center;

  code {
    font-family: var(--dd-font-mono);
    font-size: 14.5px;
    font-weight: 700;
    letter-spacing: 0.18em;
    user-select: all;
    color: var(--dd-text);
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

  :deep(.el-button + .el-button) {
    margin-left: 0;
  }
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

  /* 移动端行内编辑用户名改为纵向排布，输入框占满一行，防止按钮被 .profile-card 裁切 */
  .info-cell--editing {
    flex-direction: column;
    align-items: stretch;
    gap: 8px;
  }

  .username-edit-row {
    flex-direction: column;
    align-items: stretch;
    width: 100%;
  }

  .username-edit-input {
    width: 100%;
  }

  .username-edit-actions {
    justify-content: flex-end;
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
</style>
