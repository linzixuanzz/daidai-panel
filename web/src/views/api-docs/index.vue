<script setup lang="ts">
import { ref, computed } from 'vue'
import { apiCategories, generateCodeExamples, type ApiEndpoint } from './apiData'

const defaultEndpoint = apiCategories[0]!.endpoints[0]!
const defaultCategory = apiCategories[0]!

const selectedId = ref(defaultEndpoint.id)
const searchText = ref('')
const codeTab = ref('Shell')
const copiedKey = ref('')
const mobileMenuOpen = ref(false)

const filteredCategories = computed(() => {
  if (!searchText.value.trim()) return apiCategories
  const kw = searchText.value.toLowerCase()
  return apiCategories
    .map(cat => ({
      ...cat,
      endpoints: cat.endpoints.filter(
        ep => ep.title.toLowerCase().includes(kw) ||
          ep.path.toLowerCase().includes(kw) ||
          ep.method.toLowerCase().includes(kw)
      ),
    }))
    .filter(cat => cat.endpoints.length > 0)
})

const currentEndpoint = computed<ApiEndpoint>(() => {
  for (const cat of apiCategories) {
    for (const ep of cat.endpoints) {
      if (ep.id === selectedId.value) return ep
    }
  }
  return defaultEndpoint
})

const currentCategory = computed(() => {
  for (const cat of apiCategories) {
    if (cat.endpoints.some(ep => ep.id === selectedId.value)) return cat
  }
  return defaultCategory
})

const codeExamples = computed<Record<string, string>>(() => generateCodeExamples(currentEndpoint.value))

const totalEndpoints = computed(() =>
  apiCategories.reduce((sum, cat) => sum + cat.endpoints.length, 0))

function handleSelect(id: string) {
  selectedId.value = id
  mobileMenuOpen.value = false
}

function handleCopy(text: string, key: string) {
  if (!text) return
  const doCopy = () => {
    copiedKey.value = key
    setTimeout(() => copiedKey.value = '', 1500)
  }
  if (navigator.clipboard && window.isSecureContext) {
    navigator.clipboard.writeText(text).then(doCopy)
  } else {
    const textarea = document.createElement('textarea')
    textarea.value = text
    textarea.style.position = 'fixed'
    textarea.style.opacity = '0'
    document.body.appendChild(textarea)
    textarea.select()
    document.execCommand('copy')
    document.body.removeChild(textarea)
    doCopy()
  }
}

function methodClass(method: string) {
  const m = method.toLowerCase()
  if (m === 'get') return 'method-get'
  if (m === 'post') return 'method-post'
  if (m === 'put') return 'method-put'
  if (m === 'delete') return 'method-delete'
  return ''
}


</script>

<template>
  <div class="api-docs-page">
    <div class="page-header">
      <h3 class="page-title">
        <el-icon><Connection /></el-icon>
        开发接口文档
      </h3>
      <el-button class="mobile-menu-btn" @click="mobileMenuOpen = true">
        <el-icon><Menu /></el-icon>
        接口列表
      </el-button>
    </div>

    <el-drawer v-model="mobileMenuOpen" title="接口列表" direction="ltr" size="280px" :z-index="2000">
      <div class="sider-search">
        <el-input v-model="searchText" placeholder="搜索接口..." clearable prefix-icon="Search" />
        <div class="sider-count">共 {{ totalEndpoints }} 个接口</div>
      </div>
      <div class="sider-menu">
        <template v-for="cat in filteredCategories" :key="cat.key">
          <div class="menu-group-title">{{ cat.label }} ({{ cat.endpoints.length }})</div>
          <div
            v-for="ep in cat.endpoints" :key="ep.id"
            class="menu-item" :class="{ active: selectedId === ep.id }"
            @click="handleSelect(ep.id)"
          >
            <span class="method-badge method-badge-sm" :class="methodClass(ep.method)">{{ ep.method }}</span>
            <span class="menu-item-text">{{ ep.title }}</span>
          </div>
        </template>
      </div>
    </el-drawer>

    <div class="api-layout">
      <div class="api-sider">
        <div class="sider-search">
          <el-input v-model="searchText" placeholder="搜索接口..." clearable prefix-icon="Search" />
          <div class="sider-count">共 {{ totalEndpoints }} 个接口</div>
        </div>
        <div class="sider-menu">
          <template v-for="cat in filteredCategories" :key="cat.key">
            <div class="menu-group-title">{{ cat.label }} ({{ cat.endpoints.length }})</div>
            <div
              v-for="ep in cat.endpoints" :key="ep.id"
              class="menu-item" :class="{ active: selectedId === ep.id }"
              @click="handleSelect(ep.id)"
            >
              <span class="method-badge method-badge-sm" :class="methodClass(ep.method)">{{ ep.method }}</span>
              <span class="menu-item-text">{{ ep.title }}</span>
            </div>
          </template>
        </div>
      </div>

      <div class="api-content">
          <div class="api-breadcrumb">{{ currentCategory.label }} / {{ currentEndpoint.title }}</div>
          <h2 class="api-endpoint-title">{{ currentEndpoint.title }}</h2>

          <div class="url-bar">
            <span class="method-badge" :class="methodClass(currentEndpoint.method)">{{ currentEndpoint.method }}</span>
            <span class="url-path">http://localhost:5701{{ currentEndpoint.path }}</span>
            <el-tooltip :content="copiedKey === 'url' ? '已复制' : '复制 URL'" placement="top">
              <el-button text size="small" @click="handleCopy(`http://localhost:5701${currentEndpoint.path}`, 'url')">
                <el-icon><DocumentCopy /></el-icon>
              </el-button>
            </el-tooltip>
          </div>

          <p class="api-description">{{ currentEndpoint.description }}</p>

          <div class="auth-banner" :class="currentEndpoint.auth === 'jwt' ? 'auth-jwt' : 'auth-none'">
            <el-icon v-if="currentEndpoint.auth === 'jwt'"><Lock /></el-icon>
            <el-icon v-else><Unlock /></el-icon>
            <span v-if="currentEndpoint.auth === 'jwt'">
              使用 JWT Token 鉴权，请在请求头中添加 <code>Authorization: Bearer &lt;TOKEN&gt;</code>
            </span>
            <span v-else>此接口无需鉴权即可访问</span>
          </div>

        <el-card
          v-if="currentEndpoint.pathParams?.length || currentEndpoint.queryParams?.length || currentEndpoint.bodyParams?.length"
          shadow="never" class="api-card"
        >
          <template #header>
            <div class="section-title">请求参数</div>
          </template>

          <template v-if="currentEndpoint.pathParams?.length">
            <div class="param-section-header">Path 参数</div>
            <el-table :data="currentEndpoint.pathParams" size="small" :show-header="true" :border="false">
              <el-table-column prop="name" label="参数名" width="150">
                <template #default="{ row }"><code class="param-name">{{ row.name }}</code></template>
              </el-table-column>
              <el-table-column prop="type" label="类型" width="90">
                <template #default="{ row }"><el-tag size="small" type="info" round>{{ row.type }}</el-tag></template>
              </el-table-column>
              <el-table-column prop="required" label="必填" width="70">
                <template #default="{ row }">
                  <el-tag v-if="row.required" size="small" type="danger" round>必填</el-tag>
                  <el-tag v-else size="small" round>可选</el-tag>
                </template>
              </el-table-column>
              <el-table-column prop="description" label="说明" />
              <el-table-column prop="example" label="示例" width="130">
                <template #default="{ row }"><span class="param-example">{{ row.example || '-' }}</span></template>
              </el-table-column>
            </el-table>
          </template>

          <template v-if="currentEndpoint.queryParams?.length">
            <div class="param-section-header">Query 参数</div>
            <el-table :data="currentEndpoint.queryParams" size="small" :show-header="true" :border="false">
              <el-table-column prop="name" label="参数名" width="150">
                <template #default="{ row }"><code class="param-name">{{ row.name }}</code></template>
              </el-table-column>
              <el-table-column prop="type" label="类型" width="90">
                <template #default="{ row }"><el-tag size="small" type="info" round>{{ row.type }}</el-tag></template>
              </el-table-column>
              <el-table-column prop="required" label="必填" width="70">
                <template #default="{ row }">
                  <el-tag v-if="row.required" size="small" type="danger" round>必填</el-tag>
                  <el-tag v-else size="small" round>可选</el-tag>
                </template>
              </el-table-column>
              <el-table-column prop="description" label="说明" />
              <el-table-column prop="example" label="示例" width="130">
                <template #default="{ row }"><span class="param-example">{{ row.example || '-' }}</span></template>
              </el-table-column>
            </el-table>
          </template>

          <template v-if="currentEndpoint.bodyParams?.length">
            <div class="param-section-header">Body 参数（JSON）</div>
            <el-table :data="currentEndpoint.bodyParams" size="small" :show-header="true" :border="false">
              <el-table-column prop="name" label="参数名" width="150">
                <template #default="{ row }"><code class="param-name">{{ row.name }}</code></template>
              </el-table-column>
              <el-table-column prop="type" label="类型" width="90">
                <template #default="{ row }"><el-tag size="small" type="info" round>{{ row.type }}</el-tag></template>
              </el-table-column>
              <el-table-column prop="required" label="必填" width="70">
                <template #default="{ row }">
                  <el-tag v-if="row.required" size="small" type="danger" round>必填</el-tag>
                  <el-tag v-else size="small" round>可选</el-tag>
                </template>
              </el-table-column>
              <el-table-column prop="description" label="说明" />
              <el-table-column prop="example" label="示例" width="130">
                <template #default="{ row }"><span class="param-example">{{ row.example || '-' }}</span></template>
              </el-table-column>
            </el-table>
          </template>
        </el-card>

        <el-card shadow="never" class="api-card">
          <template #header>
            <div class="section-title">请求示例代码</div>
          </template>
          <el-tabs v-model="codeTab" class="code-tabs">
            <el-tab-pane v-for="lang in Object.keys(codeExamples)" :key="lang" :label="lang" :name="lang">
              <div class="code-block-wrapper">
                <el-tooltip :content="copiedKey === `code-${lang}` ? '已复制' : '复制代码'" placement="top">
                  <el-button
                    class="code-copy-btn"
                    size="small"
                    type="primary"
                    plain
                    @click="handleCopy(codeExamples[lang] || '', `code-${lang}`)"
                  >
                    <el-icon><DocumentCopy /></el-icon>
                  </el-button>
                </el-tooltip>
                <pre class="code-block">{{ codeExamples[lang] }}</pre>
              </div>
            </el-tab-pane>
          </el-tabs>
        </el-card>

        <el-card v-if="currentEndpoint.responseExample" shadow="never" class="api-card">
          <template #header>
            <div class="section-title">返回响应</div>
          </template>

          <div class="response-status">
            <span class="status-badge status-200">
              <span class="status-dot" />
              200 成功
            </span>
            <span class="response-type">application/json</span>
          </div>

          <div class="response-section">
            <div v-if="currentEndpoint.responseFields?.length" class="response-fields">
              <div class="response-label">Body 字段</div>
              <el-table :data="currentEndpoint.responseFields" size="small" :border="false">
                <el-table-column prop="name" label="字段" width="140">
                  <template #default="{ row }"><code class="param-name">{{ row.name }}</code></template>
                </el-table-column>
                <el-table-column prop="type" label="类型" width="80">
                  <template #default="{ row }"><el-tag size="small" type="info" round>{{ row.type }}</el-tag></template>
                </el-table-column>
                <el-table-column prop="description" label="说明" />
              </el-table>
            </div>

            <div class="response-example">
              <div class="response-label">响应示例</div>
              <div class="code-block-wrapper">
                <el-tooltip :content="copiedKey === 'resp' ? '已复制' : '复制'" placement="top">
                  <el-button
                    class="code-copy-btn"
                    size="small"
                    type="primary"
                    plain
                    @click="handleCopy(currentEndpoint.responseExample || '', 'resp')"
                  >
                    <el-icon><DocumentCopy /></el-icon>
                  </el-button>
                </el-tooltip>
                <pre class="code-block" style="max-height: 360px">{{ currentEndpoint.responseExample }}</pre>
              </div>
            </div>
          </div>
        </el-card>
      </div>
    </div>
  </div>
</template>

<style scoped lang="scss">
.api-docs-page {
  animation: fadeIn 0.3s ease-out;
}

.page-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 20px;

  .page-title {
    margin: 0;
    font-size: 20px;
    font-weight: 600;
    display: flex;
    align-items: center;
    gap: 8px;
    color: var(--el-text-color-primary);
  }

  .mobile-menu-btn {
    display: none;
  }
}

.api-layout {
  display: flex;
  gap: 16px;
  min-height: calc(100vh - 160px);
}

.api-sider {
  width: 280px;
  flex-shrink: 0;
  background: var(--el-bg-color);
  border-radius: 10px;
  border: 1px solid var(--el-border-color-lighter);
  overflow: hidden;
  position: sticky;
  top: 0;
  max-height: calc(100vh - 160px);
  display: flex;
  flex-direction: column;
}

.sider-search {
  padding: 16px 14px 8px;
  flex-shrink: 0;

  .sider-count {
    padding: 8px 2px 0;
    font-size: 12px;
    color: var(--el-text-color-secondary);
  }
}

.sider-menu {
  overflow-y: auto;
  padding-bottom: 12px;
  flex: 1;
}

.menu-group-title {
  padding: 12px 20px 4px;
  font-weight: 600;
  font-size: 12px;
  text-transform: uppercase;
  letter-spacing: 0.5px;
  color: var(--el-text-color-secondary);
}

.menu-item {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 6px 12px;
  margin: 2px 8px;
  border-radius: 6px;
  cursor: pointer;
  transition: all 0.2s ease;

  &:hover {
    background: var(--el-color-primary-light-9);
  }

  &.active {
    background: var(--el-color-primary-light-9);
    box-shadow: inset 3px 0 0 var(--el-color-primary);
  }

  .menu-item-text {
    font-size: 13px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    color: var(--el-text-color-primary);
  }
}

.method-badge {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  min-width: 52px;
  padding: 1px 8px;
  border-radius: 4px;
  font-size: 11px;
  font-weight: 700;
  letter-spacing: 0.5px;
  font-family: var(--dd-font-mono);
  color: #fff;
  line-height: 20px;
}

.method-badge-sm {
  min-width: 40px;
  padding: 0 5px;
  font-size: 10px;
  line-height: 18px;
  border-radius: 3px;
}

.method-get { background: linear-gradient(135deg, #52c41a, #73d13d); }
.method-post { background: linear-gradient(135deg, #1677ff, #4096ff); }
.method-put { background: linear-gradient(135deg, #fa8c16, #ffa940); }
.method-delete { background: linear-gradient(135deg, #ff4d4f, #ff7875); }

.api-content {
  flex: 1;
  background: var(--el-bg-color);
  border-radius: 10px;
  border: 1px solid var(--el-border-color-lighter);
  padding: 28px 32px;
  overflow: auto;
  min-width: 0;
}

.api-breadcrumb {
  font-size: 12px;
  color: var(--el-text-color-secondary);
  margin-bottom: 4px;
  letter-spacing: 0.3px;
}

.api-endpoint-title {
  font-size: 22px;
  font-weight: 700;
  color: var(--el-text-color-primary);
  margin: 0 0 16px 0;
  line-height: 1.3;
}

.url-bar {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 12px 20px;
  background: var(--el-fill-color-lighter);
  border: 1px solid var(--el-border-color-lighter);
  border-radius: 10px;
  margin-bottom: 24px;
  flex-wrap: wrap;
  transition: border-color 0.3s ease, box-shadow 0.3s ease;

  &:hover {
    border-color: var(--el-border-color);
    box-shadow: 0 2px 8px rgba(0, 0, 0, 0.04);
  }
}

.url-path {
  font-family: var(--dd-font-mono);
  font-size: 14px;
  color: var(--el-text-color-primary);
  word-break: break-all;
}

.api-description {
  font-size: 14px;
  color: var(--el-text-color-regular);
  line-height: 1.8;
  margin-bottom: 20px;
}

.auth-banner {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 10px 16px;
  border-radius: 8px;
  margin-bottom: 20px;
  font-size: 13px;

  code {
    font-size: 12px;
    background: rgba(0, 0, 0, 0.06);
    padding: 2px 6px;
    border-radius: 4px;
  }

  &.auth-jwt {
    background: linear-gradient(135deg, #fffbe6, #fff7e6);
    border: 1px solid #ffe58f;
    color: #ad6800;
  }

  &.auth-none {
    background: linear-gradient(135deg, #f6ffed, #fcffe6);
    border: 1px solid #b7eb8f;
    color: #389e0d;
  }
}

.api-card {
  border-radius: 10px;
  margin-bottom: 20px;
  overflow: hidden;

  :deep(.el-card__header) {
    background: var(--el-fill-color-lighter);
    border-bottom: 1px solid var(--el-border-color-lighter);
    padding: 12px 20px;
  }

  :deep(.el-card__body) {
    padding: 0;
  }
}

.section-title {
  position: relative;
  font-size: 15px;
  font-weight: 600;
  color: var(--el-text-color-primary);
  padding-left: 12px;
  margin: 0;

  &::before {
    content: '';
    position: absolute;
    left: 0;
    top: 3px;
    bottom: 3px;
    width: 3px;
    border-radius: 2px;
    background: linear-gradient(180deg, var(--el-color-primary), var(--el-color-primary-light-3));
  }
}

.param-section-header {
  padding: 8px 16px;
  background: var(--el-fill-color-lighter);
  border-bottom: 1px solid var(--el-border-color-lighter);
  font-weight: 600;
  font-size: 13px;
  color: var(--el-text-color-regular);
}

.param-name {
  font-size: 12px;
  background: var(--el-fill-color);
  padding: 2px 6px;
  border-radius: 4px;
}

.param-example {
  font-family: var(--dd-font-mono);
  font-size: 12px;
  color: var(--el-text-color-secondary);
}

.code-tabs {
  padding: 0 16px;

  :deep(.el-tabs__header) {
    margin-bottom: 0;
  }

  :deep(.el-tabs__item) {
    font-size: 12px;
    padding: 0 12px;
  }
}

.code-block-wrapper {
  position: relative;
  border-radius: 8px;
  overflow: hidden;
  margin: 0;

  &:hover .code-copy-btn {
    opacity: 1;
  }
}

.code-block {
  background: #1a1a2e !important;
  color: #e0e0e0;
  padding: 20px;
  margin: 0;
  font-family: var(--dd-font-mono);
  font-size: 13px;
  line-height: 1.7;
  overflow: auto;
  max-height: 340px;
  white-space: pre;
  tab-size: 2;
}

.code-copy-btn {
  position: absolute;
  top: 10px;
  right: 10px;
  z-index: 2;
  opacity: 0;
  transition: opacity 0.2s ease;
}

.response-status {
  padding: 16px 20px 0;
  display: flex;
  align-items: center;
  gap: 12px;
}

.status-badge {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: 4px 12px;
  border-radius: 20px;
  font-size: 13px;
  font-weight: 600;

  .status-dot {
    width: 8px;
    height: 8px;
    border-radius: 50%;
    background: #52c41a;
    display: inline-block;
  }
}

.status-200 {
  background: #f6ffed;
  color: #52c41a;
  border: 1px solid #b7eb8f;
}

.response-type {
  font-size: 13px;
  color: var(--el-text-color-secondary);
}

.response-section {
  display: flex;
  gap: 20px;
  flex-wrap: wrap;
  padding: 16px 20px;
}

.response-fields {
  flex: 1;
  min-width: 300px;
}

.response-example {
  flex: 1;
  min-width: 300px;
}

.response-label {
  font-size: 13px;
  font-weight: 600;
  color: var(--el-text-color-regular);
  margin-bottom: 8px;
}

@media (max-width: 768px) {
  .api-sider {
    display: none;
  }

  .page-header .mobile-menu-btn {
    display: inline-flex;
  }

  .api-content {
    padding: 16px;
  }

  .response-section {
    flex-direction: column;
  }

  .url-bar {
    padding: 10px 14px;
  }
}

@keyframes fadeIn {
  from { opacity: 0; transform: translateY(12px); }
  to { opacity: 1; transform: translateY(0); }
}
</style>
