<script setup lang="ts">
import { ElMessage } from 'element-plus'
import { Connection, Document, DocumentCopy } from '@element-plus/icons-vue'
import { copyText } from '@/utils/clipboard'
import type { McpConfigFields } from '../useSettingsConfig'

defineProps<{
  configsLoading: boolean
  configsSaving: boolean
  form: McpConfigFields
  onSave: () => void
}>()

// 完整用法（建应用与勾选权限、stdio 方式、工具清单）只在 docs/mcp.md 维护一份，卡片只放最常用的 HTTP 写法并链过去。
const MCP_DOC_URL = 'https://github.com/linzixuanzz/daidai-panel/blob/main/docs/mcp.md'

// 面板接口与页面同源（请求基址是 /api，接口文档页也按 window.location.origin 拼地址），
// 所以连接地址就是当前页面的 origin + /api/v1/mcp。
const endpointUrl = `${window.location.origin}/api/v1/mcp`

// 与 docs/mcp.md「HTTP 方式」的配置示例同一形状（HTTP + Basic），只把地址换成当前面板的。
const clientConfigExample = JSON.stringify(
  {
    mcpServers: {
      'daidai-panel': {
        url: endpointUrl,
        headers: {
          Authorization: 'Basic <base64(app_key:app_secret)>'
        }
      }
    }
  },
  null,
  2
)

async function handleCopy(text: string, successMessage: string) {
  try {
    await copyText(text)
    ElMessage.success(successMessage)
  } catch {
    ElMessage.error('复制失败，请检查浏览器权限或站点访问方式')
  }
}
</script>

<template>
  <el-card shadow="never" v-loading="configsLoading">
    <template #header>
      <div class="card-header">
        <span class="card-title"><el-icon><Connection /></el-icon> MCP 服务</span>
        <el-button type="primary" :loading="configsSaving" @click="onSave">
          <el-icon><Document /></el-icon>保存配置
        </el-button>
      </div>
    </template>

    <el-alert
      title="开启后，Claude、Cursor 等支持 MCP 的 AI 客户端可以连上面板查任务、看日志。连接时使用「Open API」页面创建的应用凭据，能访问哪些模块由应用勾选的权限决定。"
      type="info"
      :closable="false"
      style="margin-bottom: 16px"
    />

    <div class="config-section">
      <h4 class="section-title">服务开关</h4>
      <div class="form-field">
        <div class="switch-item">
          <span class="switch-label">启用 MCP 服务</span>
          <el-switch v-model="form.mcp_enabled" inline-prompt active-text="开" inactive-text="关" />
        </div>
        <span class="form-hint">关闭时所有连接都会被拒绝。保存后立即生效，不需要重启面板。</span>
      </div>
      <div class="form-field">
        <div class="switch-item">
          <span class="switch-label">允许写入与执行工具</span>
          <el-switch
            v-model="form.mcp_allow_mutations"
            :disabled="!form.mcp_enabled"
            inline-prompt
            active-text="开"
            inactive-text="关"
          />
        </div>
        <span v-if="form.mcp_enabled" class="form-hint">
          关闭时 AI 只能查询；打开后还能运行和停止任务、修改环境变量、保存和运行脚本、拉取订阅。
        </span>
        <span v-else class="form-hint">需要先启用 MCP 服务，这一项才会生效。</span>
      </div>
    </div>

    <div class="config-section">
      <h4 class="section-title">连接方式</h4>
      <div class="form-field form-field--wide">
        <label>连接地址</label>
        <el-input :model-value="endpointUrl" readonly class="mcp-endpoint-input">
          <template #append>
            <el-button
              :icon="DocumentCopy"
              aria-label="复制连接地址"
              @click="handleCopy(endpointUrl, '连接地址已复制')"
            />
          </template>
        </el-input>
        <span class="form-hint">
          按你当前访问面板的地址生成。AI 客户端要填它能访问到的地址，经反向代理或内网穿透访问时请换成对应地址。
        </span>
      </div>
      <div class="form-field form-field--wide">
        <div class="field-label-row">
          <label>客户端配置示例</label>
          <el-button text type="primary" size="small" @click="handleCopy(clientConfigExample, '配置示例已复制')">
            <el-icon><DocumentCopy /></el-icon>复制
          </el-button>
        </div>
        <pre class="mcp-config-example">{{ clientConfigExample }}</pre>
        <span class="form-hint">
          把 &lt;base64(app_key:app_secret)&gt; 换成应用凭据「app_key:app_secret」的 Base64 编码。
          生成 Base64 的命令、只支持命令行方式（stdio）的客户端怎么连、全部工具清单，见
          <a :href="MCP_DOC_URL" target="_blank" rel="noopener noreferrer">MCP 使用说明</a>。
        </span>
      </div>
    </div>

    <el-alert
      type="warning"
      show-icon
      :closable="false"
      title="打开写入后，AI 能运行任务、改写环境变量；应用若勾选了 scripts 权限，就等于能在面板上执行任意代码，请只授权给完全信任的客户端。"
    />
  </el-card>
</template>

<style scoped lang="scss">
@use './config-card-shared.scss' as *;

// 地址和配置示例比普通输入框长，放宽到一行能完整显示常见地址
.form-field--wide {
  max-width: 640px;
}

.mcp-endpoint-input :deep(.el-input__inner) {
  font-family: var(--dd-font-mono);
}

.field-label-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  margin-bottom: 8px;

  label {
    margin-bottom: 0;
  }
}

.mcp-config-example {
  margin: 0;
  padding: 12px 14px;
  overflow-x: auto;
  border: 1px solid var(--el-border-color-lighter);
  // 独立的代码块（四周留白、不贴边）→ surface 档
  border-radius: var(--dd-radius-surface);
  background: var(--el-fill-color-lighter);
  color: var(--el-text-color-primary);
  font-family: var(--dd-font-mono);
  font-size: 12px;
  line-height: 1.6;
  white-space: pre;
}

.form-hint a {
  color: var(--dd-accent-ink);
  text-decoration: none;

  &:hover {
    text-decoration: underline;
  }
}
</style>
