<script setup lang="ts">
// 设置 → 账号安全 → 通行密钥（#175）。
// 组件自己加载、自己闭合：列表、添加、重命名、删除都在这里，不往 useSettingsSecurity.ts 里塞。
// 个人资料页（非管理员进不了设置页）也直接复用这张卡片。
import { onMounted, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Key, Plus, Refresh } from '@element-plus/icons-vue'
import { securityApi, type PasskeyItem } from '@/api/security'
import { useResponsive } from '@/composables/useResponsive'
import { formatDateTime } from '@/utils/datetime'
import {
  isPasskeyCancelError,
  isPasskeySupported,
  registrationCredentialToJSON,
  resolvePasskeyErrorMessage,
  toCreationOptions
} from '@/utils/passkey'

// 列表数量变化时通知外层（个人资料页要显示「已添加 N 个」）
const emit = defineEmits<{ (e: 'changed', count: number): void }>()

const { isMobile, dialogFullscreen } = useResponsive()

// 与后端同一口径：只有 HTTPS 域名或 http://localhost 访问时能用（IP 访问一律不行），页面打开后不会变，取一次即可
const supported = isPasskeySupported()
// 通行密钥绑定的是「添加时的完整访问地址（含端口）」，说明文字和「其他地址」标签都拿它比
const currentOrigin = typeof window !== 'undefined' ? window.location.origin : ''

const passkeys = ref<PasskeyItem[]>([])
const loading = ref(false)

const showAddDialog = ref(false)
const adding = ref(false)
const addForm = ref({ password: '', name: '', totpCode: '' })
// 当前账号开没开两步验证：开了就要在添加弹窗里多填一个验证码（后端要求）
const twoFAEnabled = ref(false)
const checking2FA = ref(false)

async function loadPasskeys() {
  loading.value = true
  try {
    const res = await securityApi.listPasskeys()
    passkeys.value = Array.isArray(res.data) ? res.data : []
    emit('changed', passkeys.value.length)
  } catch (err: any) {
    ElMessage.error(err?.response?.data?.error || '加载通行密钥失败')
  } finally {
    loading.value = false
  }
}

// 按 UA 给新通行密钥猜一个好认的默认名，如「Windows · Chrome」「iPhone · Safari」，用户可以改。
// 顺序有讲究：Edge / Opera 的 UA 里也带 Chrome，iOS 上的 Chrome / Firefox 也带 Safari，要先判前者。
function guessPasskeyName() {
  const ua = typeof navigator !== 'undefined' ? navigator.userAgent : ''
  let device = '设备'
  if (/iPhone/i.test(ua)) device = 'iPhone'
  else if (/iPad/i.test(ua)) device = 'iPad'
  else if (/Android/i.test(ua)) device = 'Android'
  else if (/Windows/i.test(ua)) device = 'Windows'
  else if (/Macintosh|Mac OS X/i.test(ua)) device = 'Mac'
  else if (/CrOS/i.test(ua)) device = 'ChromeOS'
  else if (/Linux/i.test(ua)) device = 'Linux'

  let browser = '浏览器'
  if (/Edg(e|A|iOS)?\//i.test(ua)) browser = 'Edge'
  else if (/OPR\/|Opera/i.test(ua)) browser = 'Opera'
  else if (/Firefox|FxiOS/i.test(ua)) browser = 'Firefox'
  else if (/Chrome|CriOS|Chromium/i.test(ua)) browser = 'Chrome'
  else if (/Safari/i.test(ua)) browser = 'Safari'

  return `${device} · ${browser}`
}

async function openAddDialog() {
  addForm.value = { password: '', name: guessPasskeyName(), totpCode: '' }
  twoFAEnabled.value = false
  showAddDialog.value = true
  // 每次打开都重新查一次两步验证状态（可能刚在别处开 / 关过）
  checking2FA.value = true
  try {
    const res = await securityApi.get2FAStatus()
    twoFAEnabled.value = !!res.data?.enabled
  } catch {
    // 查失败就先不显示验证码框；真开着的话，提交时后端会回「请输入两步验证码」，下面据此再把框显示出来
  } finally {
    checking2FA.value = false
  }
}

// 添加：校验当前密码（开了两步验证再加验证码）拿挑战 → 浏览器弹系统验证创建通行密钥 → 交回后端保存 → 刷新列表
async function confirmAdd() {
  if (adding.value || checking2FA.value) return
  if (!addForm.value.password) {
    ElMessage.warning('请输入当前密码')
    return
  }
  const totpCode = addForm.value.totpCode.trim()
  if (twoFAEnabled.value && !/^\d{6}$/.test(totpCode)) {
    ElMessage.warning('请输入 6 位两步验证码')
    return
  }
  adding.value = true
  try {
    const begin = await securityApi.passkeyRegisterBegin(
      addForm.value.password,
      twoFAEnabled.value ? totpCode : undefined
    )
    const credential = await navigator.credentials.create({
      publicKey: toCreationOptions(begin.data?.options)
    })
    if (!credential) {
      throw new Error('没有拿到通行密钥')
    }
    const res = await securityApi.passkeyRegisterFinish(
      begin.data.session_id,
      addForm.value.name.trim(),
      registrationCredentialToJSON(credential as PublicKeyCredential)
    )
    ElMessage.success(res.message || '通行密钥已添加')
    showAddDialog.value = false
    // 密码和验证码不在内存里多留
    addForm.value.password = ''
    addForm.value.totpCode = ''
    void loadPasskeys()
  } catch (err: any) {
    // 系统弹窗里取消 / 超时：只提示一句，弹窗留着方便再试
    if (isPasskeyCancelError(err)) {
      ElMessage.info('已取消')
      return
    }
    // 打开时没查到「已开启两步验证」（状态刚变或查询失败），后端说要验证码：把验证码框补出来
    if (err?.response?.data?.code === 'two_factor_required') {
      twoFAEnabled.value = true
    }
    ElMessage.error(resolvePasskeyErrorMessage(err, '添加通行密钥失败'))
  } finally {
    adding.value = false
  }
}

async function handleRename(row: PasskeyItem) {
  let name = ''
  try {
    const result = await ElMessageBox.prompt('给这个通行密钥起个好认的名字', '重命名通行密钥', {
      inputValue: row.name,
      confirmButtonText: '保存',
      cancelButtonText: '取消',
      // 后端按字符截到 64 个，这里提前拦住，免得保存后名字被悄悄截短
      inputValidator: (value: string) => {
        const trimmed = String(value || '').trim()
        if (!trimmed) return '名称不能为空'
        if ([...trimmed].length > 64) return '名称最多 64 个字'
        return true
      }
    })
    name = String(result.value || '').trim()
  } catch {
    return // 取消
  }
  try {
    const res = await securityApi.renamePasskey(row.id, name)
    ElMessage.success(res.message || '已重命名')
    void loadPasskeys()
  } catch (err: any) {
    ElMessage.error(err?.response?.data?.error || '重命名失败')
  }
}

async function handleDelete(row: PasskeyItem) {
  try {
    await ElMessageBox.confirm(
      `确定删除通行密钥「${row.name}」吗？删除后就不能再用它登录面板，设备或密码管理器里保存的那一份需要你自己去系统里删掉。`,
      '删除通行密钥',
      { type: 'warning', confirmButtonText: '删除', cancelButtonText: '取消' }
    )
  } catch {
    return // 取消
  }
  try {
    const res = await securityApi.deletePasskey(row.id)
    ElMessage.success(res.message || '已删除')
    void loadPasskeys()
  } catch (err: any) {
    ElMessage.error(err?.response?.data?.error || '删除失败')
  }
}

// 后端给的 origin 原样显示，只去掉可能的末尾斜杠
function displayOrigin(row: PasskeyItem) {
  return String(row.origin || '').replace(/\/+$/, '')
}

// 添加时的访问地址与当前不同（域名或端口变了）：这个通行密钥在这里登录用不上
function isOtherOrigin(row: PasskeyItem) {
  const origin = displayOrigin(row)
  return !!origin && origin !== currentOrigin
}

function formatLastUsed(row: PasskeyItem) {
  return row.last_used_at ? formatDateTime(row.last_used_at) : '从未使用'
}

onMounted(() => {
  void loadPasskeys()
})

// 设置页顶部「刷新」按钮要能刷到这张卡片
defineExpose({ reload: loadPasskeys })
</script>

<template>
  <el-card shadow="never">
    <template #header>
      <div class="card-header">
        <span class="card-title"><el-icon><Key /></el-icon> 通行密钥</span>
        <div class="card-header-buttons">
          <el-button :loading="loading" @click="loadPasskeys"><el-icon><Refresh /></el-icon>刷新</el-button>
          <el-button type="primary" :disabled="!supported" @click="openAddDialog">
            <el-icon><Plus /></el-icon>添加通行密钥
          </el-button>
        </div>
      </div>
    </template>

    <el-alert
      v-if="!supported"
      type="warning"
      show-icon
      :closable="false"
      class="passkey-alert"
      title="当前访问地址不能使用通行密钥：只支持 HTTPS 域名或 http://localhost，用 IP 访问不行。请改用 HTTPS 域名访问面板后再添加。"
    />

    <div class="card-tip">
      <p>通行密钥用指纹、面容或设备 PIN 登录，不用再输两步验证码；密码登录照常可用。</p>
      <p>
        每个通行密钥绑定添加时的访问地址（当前是 <code class="passkey-host">{{ currentOrigin || '-' }}</code>），
        换地址或端口访问需要重新添加。
      </p>
      <p>修改密码后需要重新登录，已添加的通行密钥也会一并清除。</p>
      <p>APP 端暂不支持通行密钥，仍用密码（和两步验证码）登录。</p>
    </div>

    <div v-if="isMobile" class="dd-mobile-list">
      <div v-for="row in passkeys" :key="row.id" class="dd-mobile-card">
        <div class="dd-mobile-card__header">
          <div class="dd-mobile-card__title-wrap">
            <span class="dd-mobile-card__title">{{ row.name }}</span>
            <span class="dd-mobile-card__subtitle">{{ displayOrigin(row) || row.rp_id }}</span>
          </div>
          <el-tag v-if="isOtherOrigin(row)" type="info" size="small">其他地址</el-tag>
        </div>
        <div class="dd-mobile-card__body">
          <div class="dd-mobile-card__grid">
            <div class="dd-mobile-card__field">
              <span class="dd-mobile-card__label">添加时间</span>
              <span class="dd-mobile-card__value">{{ formatDateTime(row.created_at) }}</span>
            </div>
            <div class="dd-mobile-card__field">
              <span class="dd-mobile-card__label">最近使用</span>
              <span class="dd-mobile-card__value">{{ formatLastUsed(row) }}</span>
            </div>
          </div>
          <div class="dd-mobile-card__actions passkey-card__actions">
            <el-button size="small" @click="handleRename(row)">重命名</el-button>
            <el-button size="small" type="danger" plain @click="handleDelete(row)">删除</el-button>
          </div>
        </div>
      </div>
      <el-empty v-if="!loading && passkeys.length === 0" description="还没有添加通行密钥" />
    </div>

    <el-table v-else :data="passkeys" v-loading="loading" stripe empty-text="还没有添加通行密钥">
      <el-table-column prop="name" label="名称" min-width="180" show-overflow-tooltip />
      <el-table-column label="访问地址" min-width="220">
        <template #default="{ row }">
          <span class="passkey-domain">
            <span class="passkey-domain__text">{{ displayOrigin(row) || row.rp_id }}</span>
            <el-tag v-if="isOtherOrigin(row)" type="info" size="small">其他地址</el-tag>
          </span>
        </template>
      </el-table-column>
      <el-table-column label="添加时间" width="170">
        <template #default="{ row }">{{ formatDateTime(row.created_at) }}</template>
      </el-table-column>
      <el-table-column label="最近使用" width="170">
        <template #default="{ row }">{{ formatLastUsed(row) }}</template>
      </el-table-column>
      <el-table-column label="操作" width="140" fixed="right">
        <template #default="{ row }">
          <el-button size="small" text @click="handleRename(row)">重命名</el-button>
          <el-button size="small" text type="danger" @click="handleDelete(row)">删除</el-button>
        </template>
      </el-table-column>
    </el-table>
  </el-card>

  <el-dialog
    v-model="showAddDialog"
    title="添加通行密钥"
    width="460px"
    :fullscreen="dialogFullscreen"
    :close-on-click-modal="false"
    append-to-body
  >
    <el-form
      :label-width="dialogFullscreen ? 'auto' : '96px'"
      :label-position="dialogFullscreen ? 'top' : 'right'"
      @submit.prevent="confirmAdd"
    >
      <el-form-item label="当前密码" required>
        <el-input
          v-model="addForm.password"
          type="password"
          show-password
          autocomplete="current-password"
          placeholder="为确认是你本人操作，请输入当前密码"
          @keyup.enter="confirmAdd"
        />
      </el-form-item>
      <!-- 开了两步验证时后端还要验证码，防只拿到密码的人偷偷加通行密钥 -->
      <el-form-item v-if="twoFAEnabled" label="两步验证码" required>
        <el-input
          v-model="addForm.totpCode"
          maxlength="6"
          inputmode="numeric"
          autocomplete="one-time-code"
          placeholder="认证器里的 6 位数字"
          @keyup.enter="confirmAdd"
        />
      </el-form-item>
      <el-form-item label="名称">
        <el-input v-model="addForm.name" maxlength="64" show-word-limit placeholder="如：Windows · Chrome" />
        <div class="field-hint">下一步浏览器会弹出系统验证（指纹、面容或设备 PIN），按提示完成即可。</div>
      </el-form-item>
    </el-form>
    <template #footer>
      <el-button @click="showAddDialog = false">取消</el-button>
      <el-button type="primary" :loading="adding || checking2FA" :disabled="adding || checking2FA" @click="confirmAdd">下一步</el-button>
    </template>
  </el-dialog>
</template>

<style scoped lang="scss">
@use './config-card-shared.scss' as *;

.card-header-buttons {
  padding: 2px;
  // 按钮组的灰底槽 → control 档（与槽内按钮同档，圆角一致才不会露出内外错位的角）
  border-radius: var(--dd-radius-control);
  background: color-mix(in srgb, var(--el-fill-color-light) 84%, transparent);
  display: flex;
  gap: 8px;
}

.passkey-alert {
  margin-bottom: 12px;
}

.card-tip {
  margin: 0 0 12px;
  font-size: 13px;
  line-height: 1.6;
  color: var(--el-text-color-secondary);

  p {
    margin: 0 0 2px;
  }
}

.passkey-host {
  font-family: var(--dd-font-mono);
  color: var(--el-text-color-primary);
  word-break: break-all;
}

.passkey-domain {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  min-width: 0;
  max-width: 100%;

  &__text {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
}

.field-hint {
  margin-top: 6px;
  font-size: 12px;
  line-height: 1.5;
  color: var(--el-text-color-secondary);
}

.passkey-card__actions > * {
  flex: 1 1 auto;
}

@media (max-width: 768px) {
  .card-header-buttons {
    width: 100%;
    flex-wrap: wrap;
  }
}
</style>
