<script setup lang="ts">
import { Document, Setting } from '@element-plus/icons-vue'
import {
  formatConfigRangeHint,
  resolveConfigOptions,
  type ParsedSystemConfigGroup
} from '../systemConfigSchema'

// 兜底区：渲染服务端注册了、但本页没有专属表单的配置项。
// 控件形状全部来自 GET /api/configs 的 schema，这里不持有任何配置定义副本。
defineProps<{
  configsLoading: boolean
  configsSaving: boolean
  groups: ParsedSystemConfigGroup[]
  /** key -> 原始字符串草稿，由 useSettingsConfig 持有 */
  draft: Record<string, string>
  onSave: () => void
}>()
</script>

<template>
  <el-card shadow="never" v-loading="configsLoading">
    <template #header>
      <div class="card-header">
        <span class="card-title"><el-icon><Setting /></el-icon> 其它配置项</span>
        <el-button type="primary" :loading="configsSaving" @click="onSave">
          <el-icon><Document /></el-icon>保存配置
        </el-button>
      </div>
    </template>

    <el-alert
      title="下面这些配置项由面板下发，本页还没有为它们准备专属表单，因此按面板给的定义直接渲染。保存时只会提交你改动过的项。"
      type="info"
      :closable="false"
      style="margin-bottom: 16px"
    />

    <div v-for="group in groups" :key="group.group" class="config-section">
      <h4 class="section-title">{{ group.label }}</h4>

      <div v-for="item in group.items" :key="item.key" class="form-field">
        <label>{{ item.label }}</label>

        <!-- 只读项仍然把值显示出来：隐藏会让「为什么面板更新失败」无从排查 -->
        <el-input v-if="item.readOnly" :model-value="item.value" disabled />

        <!--
          开关直接读写字符串 "true"/"false"：服务端的 bool 值就是字符串
          （newBoolConfig 走 strconv.FormatBool），而 PUT /configs/batch 绑定的是
          map[string]string，混进 JSON 布尔会让整份请求绑定失败。
        -->
        <el-switch
          v-else-if="item.valueType === 'bool'"
          v-model="draft[item.key]"
          active-value="true"
          inactive-value="false"
          inline-prompt
          active-text="开"
          inactive-text="关"
        />

        <el-select
          v-else-if="item.valueType === 'enum'"
          v-model="draft[item.key]"
          class="extra-config-select"
        >
          <el-option
            v-for="option in resolveConfigOptions(item, draft[item.key])"
            :key="option.value"
            :label="option.label"
            :value="option.value"
          />
        </el-select>

        <el-input
          v-else
          v-model="draft[item.key]"
          :type="item.secret ? 'password' : 'text'"
          :show-password="item.secret"
          :placeholder="item.defaultValue || item.label"
        />

        <span v-if="item.description" class="form-hint">{{ item.description }}</span>
        <span v-if="formatConfigRangeHint(item)" class="form-hint">{{ formatConfigRangeHint(item) }}</span>
        <span v-if="item.readOnlyReason" class="form-hint form-hint--warning">
          只读：{{ item.readOnlyReason }}
        </span>
        <span v-else-if="item.riskNote" class="form-hint form-hint--warning">
          {{ item.riskNote }}
        </span>
      </div>
    </div>
  </el-card>
</template>

<style scoped lang="scss">
@use './config-card-shared.scss' as *;

.extra-config-select {
  width: 100%;
}

.form-hint--warning {
  color: var(--dd-warn-ink);
}
</style>
