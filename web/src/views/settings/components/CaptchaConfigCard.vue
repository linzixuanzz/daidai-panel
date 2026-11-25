<script setup lang="ts">
import { Document, Key } from '@element-plus/icons-vue'
import type { SettingsConfigForm } from '../types'

defineProps<{
  configsLoading?: boolean
  configsSaving: boolean
  form: SettingsConfigForm
  captchaFeatureImplemented: boolean
  onSave: () => void
}>()
</script>

<template>
  <el-card shadow="never" v-loading="configsLoading">
    <template #header>
      <div class="card-header">
        <span class="card-title"><el-icon><Key /></el-icon> 验证码设置</span>
        <div class="captcha-header-actions">
          <el-tag type="success" effect="plain">已接入</el-tag>
          <el-button type="primary" :loading="configsSaving" :disabled="!captchaFeatureImplemented" @click="onSave">
            <el-icon><Document /></el-icon>保存配置
          </el-button>
        </div>
      </div>
    </template>

    <el-alert
      title="开启后，每次登录都会先要求完成极验滑块验证，验证通过后才继续校验账号密码。"
      type="info"
      :closable="false"
      style="margin-bottom: 16px"
    />
    <div class="switch-row" style="margin-bottom: 4px">
      <div class="switch-item">
        <span class="switch-label">启用极验验证码</span>
        <el-switch v-model="form.captcha_enabled" inline-prompt active-text="开" inactive-text="关" :disabled="!captchaFeatureImplemented" />
      </div>
    </div>
    <span class="form-hint" style="display: block; margin-bottom: 20px">
      建议同时配置完整的 Captcha ID 和 Captcha Key；未配完整时即使开关开启也不会实际生效。
    </span>
    <div class="form-field">
      <label>上游异常策略</label>
      <el-radio-group v-model="form.captcha_fail_mode">
        <el-radio value="open">宽松放行（推荐）</el-radio>
        <el-radio value="strict">严格拦截</el-radio>
      </el-radio-group>
      <span class="form-hint">
        当登录验证码已启用且极验请求超时、上游 5xx、返回异常结构时：
        “宽松放行”会允许本次登录继续校验用户名密码，“严格拦截”会直接阻止登录并提示稍后重试。
      </span>
    </div>
    <div class="form-field">
      <label>Captcha ID</label>
      <el-input v-model="form.captcha_id" placeholder="请输入极验 Captcha ID" :disabled="!captchaFeatureImplemented" />
      <span class="form-hint">极验后台获取的 Captcha ID</span>
    </div>
    <div class="form-field">
      <label>Captcha Key</label>
      <el-input v-model="form.captcha_key" type="password" show-password placeholder="请输入极验 Captcha Key" :disabled="!captchaFeatureImplemented" />
      <span class="form-hint">极验后台获取的 Captcha Key（服务端密钥）</span>
    </div>
  </el-card>
</template>

<style scoped lang="scss">
@use './config-card-shared.scss' as *;

.captcha-header-actions {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;

  :deep(.el-button + .el-button) {
    margin-left: 0;
  }
}
</style>
