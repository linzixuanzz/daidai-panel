<script setup lang="ts">
import { CircleCheck, Key, Lock } from '@element-plus/icons-vue'
import { useResponsive } from '@/composables/useResponsive'

const oldPassword = defineModel<string>('oldPassword', { required: true })
const newPassword = defineModel<string>('newPassword', { required: true })
const confirmPassword = defineModel<string>('confirmPassword', { required: true })
const showSetup2FA = defineModel<boolean>('showSetup2FA', { required: true })
const twoFACode = defineModel<string>('twoFACode', { required: true })

defineProps<{
  twoFAEnabled: boolean
  twoFASecret: string
  twoFAQrUrl: string
  onChangePassword: () => void | Promise<void>
  onSetup2FA: () => void | Promise<void>
  onDisable2FA: () => void | Promise<void>
  onVerify2FA: () => void | Promise<void>
}>()

const { dialogFullscreen } = useResponsive()
</script>

<template>
  <el-row :gutter="16">
    <el-col :xs="24" :sm="24" :md="14">
      <el-card shadow="never">
        <template #header>
          <div class="card-header">
            <span class="card-title"><el-icon><Lock /></el-icon> 修改密码</span>
          </div>
        </template>
        <!-- 改密码会撤销所有登录并清掉通行密钥（后端行为），提前说清楚 -->
        <p class="password-hint">修改密码后需要重新登录，已添加的通行密钥也会一并清除。</p>
        <el-form label-position="top" style="max-width: 420px">
          <form @submit.prevent="onChangePassword">
          <el-form-item label="* 当前密码">
            <el-input v-model="oldPassword" type="password" show-password placeholder="当前密码" aria-label="当前密码" />
          </el-form-item>
          <el-form-item label="* 新密码">
            <el-input v-model="newPassword" type="password" show-password placeholder="新密码（至少 6 位）" aria-label="新密码" />
          </el-form-item>
          <el-form-item label="* 确认密码">
            <el-input v-model="confirmPassword" type="password" show-password placeholder="再次输入新密码" aria-label="确认新密码" />
          </el-form-item>
          <el-form-item>
            <el-button type="primary" native-type="submit">
              <el-icon><CircleCheck /></el-icon>修改密码
            </el-button>
          </el-form-item>
          </form>
        </el-form>
      </el-card>
    </el-col>
    <el-col :xs="24" :sm="24" :md="10">
      <el-card shadow="never">
        <template #header>
          <div class="card-header">
            <span class="card-title"><el-icon><Key /></el-icon> 双因素认证 (2FA)</span>
            <el-tag :type="twoFAEnabled ? 'success' : 'info'" size="small">
              {{ twoFAEnabled ? '已启用' : '未启用' }}
            </el-tag>
          </div>
        </template>
        <p class="twofa-desc">
          双因素认证为您的账户提供额外的安全保护。启用后，登录时除了密码外，还需要输入认证器应用生成的验证码。
        </p>
        <el-button v-if="!twoFAEnabled" type="primary" @click="onSetup2FA">
          <el-icon><Key /></el-icon>启用双因素认证
        </el-button>
        <el-button v-else type="danger" @click="onDisable2FA">禁用双因素认证</el-button>
      </el-card>
    </el-col>
  </el-row>

  <el-dialog v-model="showSetup2FA" title="设置双因素认证" width="480px" :fullscreen="dialogFullscreen" :close-on-click-modal="false">
    <div class="setup-2fa">
      <div class="setup-2fa-step">
        <div class="step-title">步骤 1：扫描二维码</div>
        <div class="qr-code-wrapper">
          <img v-if="twoFAQrUrl" :src="twoFAQrUrl" alt="2FA QR Code" class="qr-code-img" />
        </div>
        <div class="step-hint">使用 Google Authenticator、Microsoft Authenticator 或其他 TOTP 认证器应用扫描此二维码</div>
      </div>
      <div class="setup-2fa-step">
        <div class="step-title">或手动输入密钥</div>
        <div class="secret-display">
          <code>{{ twoFASecret }}</code>
        </div>
      </div>
      <div class="setup-2fa-step">
        <div class="step-title">步骤 2：输入验证码</div>
        <form class="twofa-verify-form" @submit.prevent="onVerify2FA">
          <el-input v-model="twoFACode" placeholder="请输入 6 位验证码" maxlength="6" size="large" style="width: 220px" aria-label="双因素验证码" @keyup.enter="onVerify2FA" />
        </form>
      </div>
    </div>
    <template #footer>
      <el-button @click="showSetup2FA = false">取消</el-button>
      <el-button type="primary" @click="onVerify2FA">验证并启用</el-button>
    </template>
  </el-dialog>
</template>

<style scoped lang="scss">
@use './config-card-shared.scss' as *;

.twofa-desc {
  color: var(--el-text-color-secondary);
  font-size: 14px;
  line-height: 1.6;
  margin: 0 0 20px;
}

.password-hint {
  color: var(--el-text-color-secondary);
  font-size: 13px;
  line-height: 1.6;
  margin: 0 0 12px;
}

form {
  margin: 0;
}

.twofa-verify-form {
  display: inline-block;
}

.setup-2fa {
  .setup-2fa-step {
    margin-bottom: 20px;

    &:last-child {
      margin-bottom: 0;
    }
  }

  .step-title {
    font-weight: 600;
    font-size: 14px;
    margin-bottom: 10px;
    color: var(--el-text-color-primary);
  }

  .step-hint {
    font-size: 12px;
    color: var(--el-text-color-secondary);
    margin-top: 8px;
    text-align: center;
  }

  .qr-code-wrapper {
    text-align: center;
    padding: 16px 0;

    .qr-code-img {
      width: 200px;
      height: 200px;
      // 二维码是带描边的图片容器 → surface 档
      border-radius: var(--dd-radius-surface);
      border: 1px solid var(--el-border-color-light);
    }
  }

  .secret-display {
    padding: 12px;
    background: var(--el-fill-color-light);
    // 密钥展示块是弹窗内的独立区块（不贴边）→ surface 档
    border-radius: var(--dd-radius-surface);
    text-align: center;

    code {
      font-size: 15px;
      font-weight: 600;
      letter-spacing: 2px;
      user-select: all;
    }
  }
}
</style>
