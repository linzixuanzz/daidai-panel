<script setup lang="ts">
import { ref, computed, onMounted, watch } from 'vue'
import { Search } from '@element-plus/icons-vue'
import DdBadge from '@/components/ui/DdBadge.vue'
import type { ApiCategory, ApiEndpoint } from './apiData'

type ApiDocsModule = typeof import('./apiData')

const apiDataModule = ref<ApiDocsModule | null>(null)
const apiCategories = ref<ApiCategory[]>([])
const apiLoading = ref(true)
const selectedId = ref('')
const searchText = ref('')
const codeTab = ref('Shell')
const helperTab = ref('JavaScript')
const copiedKey = ref('')
const mobileMenuOpen = ref(false)

const filteredCategories = computed(() => {
  if (!searchText.value.trim()) return apiCategories.value
  const kw = searchText.value.toLowerCase()
  return apiCategories.value
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

const currentEndpoint = computed<ApiEndpoint | null>(() => {
  for (const cat of filteredCategories.value) {
    for (const ep of cat.endpoints) {
      if (ep.id === selectedId.value) return ep
    }
  }
  return filteredCategories.value[0]?.endpoints[0] || null
})

const currentCategory = computed(() => {
  for (const cat of filteredCategories.value) {
    if (cat.endpoints.some(ep => ep.id === selectedId.value)) return cat
  }
  return filteredCategories.value[0] || null
})

const codeExamples = computed<Record<string, string>>(() => {
  if (!apiDataModule.value || !currentEndpoint.value) return {}
  return apiDataModule.value.generateCodeExamples(currentEndpoint.value)
})
const apiBaseOrigin = computed(() => {
  if (apiDataModule.value) {
    return apiDataModule.value.getApiBaseOrigin()
  }
  if (typeof window !== 'undefined' && window.location?.origin) {
    return window.location.origin
  }
  return 'http://localhost:5701'
})

const totalEndpoints = computed(() =>
  apiCategories.value.reduce((sum, cat) => sum + cat.endpoints.length, 0))

onMounted(async () => {
  const module = await import('./apiData')
  apiDataModule.value = module
  apiCategories.value = module.apiCategories
  selectedId.value = module.apiCategories[0]?.endpoints[0]?.id || ''
  apiLoading.value = false
})

watch(filteredCategories, (categories) => {
  const firstVisibleId = categories[0]?.endpoints[0]?.id || ''
  if (!firstVisibleId) {
    selectedId.value = ''
    return
  }

  const selectionStillVisible = categories.some(cat =>
    cat.endpoints.some(ep => ep.id === selectedId.value)
  )
  if (!selectionStillVisible) {
    selectedId.value = firstVisibleId
  }
}, { immediate: true })

watch(currentEndpoint, endpoint => {
  const names = Object.keys(endpoint?.helperExamples || {})
  helperTab.value = names[0] || 'JavaScript'
}, { immediate: true })

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

// 鉴权提示条样式：jwt=Bearer 鉴权，ticket=只认 URL 票据（原始日志直传下载），其余按无需鉴权。
function authBannerClass(auth?: ApiEndpoint['auth']) {
  if (auth === 'jwt') return 'auth-jwt'
  if (auth === 'ticket') return 'auth-ticket'
  return 'auth-none'
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
  <div class="api-docs-page dd-fixed-page dd-page-hide-heading">
    <div class="page-header dd-rise" style="--dd-i: 0">
      <h3 class="page-title">
        开发接口文档
      </h3>
    </div>

    <!-- 移动端顶部栏：.page-header 会被 dd-page-hide-heading 隐藏，
         接口列表抽屉入口放在 page-header 之外，确保移动端依然可点开 -->
    <div class="mobile-docs-bar">
      <el-button class="mobile-menu-btn" @click="mobileMenuOpen = true">
        <el-icon><Menu /></el-icon>
        接口列表
      </el-button>
    </div>

    <el-drawer v-model="mobileMenuOpen" title="接口列表" direction="ltr" size="280px" :z-index="2000" class="api-docs-drawer">
      <div class="sider-search">
        <el-input v-model="searchText" placeholder="搜索接口..." clearable :prefix-icon="Search" />
        <div class="sider-count">共 {{ totalEndpoints }} 个接口</div>
      </div>
      <div class="sider-menu">
        <template v-for="cat in filteredCategories" :key="cat.key">
          <div class="menu-group-title">
            <span class="menu-group-label">{{ cat.label }}</span>
            <DdBadge
              :value="cat.endpoints.length"
              level="info"
              show-zero
              :title="`${cat.label} 接口数`"
            />
          </div>
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
      <div class="api-sider dd-rise" style="--dd-i: 1">
        <div class="sider-search">
          <el-input v-model="searchText" placeholder="搜索接口..." clearable :prefix-icon="Search" />
          <div class="sider-count">共 {{ totalEndpoints }} 个接口</div>
        </div>
        <div class="sider-menu">
          <template v-for="cat in filteredCategories" :key="cat.key">
            <div class="menu-group-title">
              <span class="menu-group-label">{{ cat.label }}</span>
              <DdBadge
                :value="cat.endpoints.length"
                level="info"
                show-zero
                :title="`${cat.label} 接口数`"
              />
            </div>
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

      <div class="api-content dd-rise" style="--dd-i: 2">
        <!-- 切换左侧接口时右侧整块内容是硬替换的，看不出「换了一篇」。
             这里只做纯淡入淡出：本页正文很长，一旦带位移，滚动容器里的长内容会跟着抖。
             key 绑 selectedId —— 绑分类或索引都不会在同分类内切换时触发。 -->
        <Transition name="dd-apidocs-switch" mode="out-in">
          <div
            v-if="!apiLoading && currentEndpoint && currentCategory"
            :key="selectedId"
            class="api-doc-body"
          >
          <div class="api-breadcrumb">{{ currentCategory.label }} / {{ currentEndpoint.title }}</div>
          <h2 class="api-endpoint-title">{{ currentEndpoint.title }}</h2>

          <div class="url-bar">
            <span class="method-badge" :class="methodClass(currentEndpoint.method)">{{ currentEndpoint.method }}</span>
            <span class="url-path">{{ apiBaseOrigin }}{{ currentEndpoint.path }}</span>
            <el-tooltip :content="copiedKey === 'url' ? '已复制' : '复制 URL'" placement="top">
              <el-button text size="small" @click="handleCopy(`${apiBaseOrigin}${currentEndpoint.path}`, 'url')">
                <el-icon><DocumentCopy /></el-icon>
              </el-button>
            </el-tooltip>
          </div>

          <p class="api-description">{{ currentEndpoint.description }}</p>

          <div class="auth-banner" :class="authBannerClass(currentEndpoint.auth)">
            <el-icon v-if="currentEndpoint.auth === 'jwt'"><Lock /></el-icon>
            <el-icon v-else-if="currentEndpoint.auth === 'ticket'"><Key /></el-icon>
            <el-icon v-else><Unlock /></el-icon>
            <span v-if="currentEndpoint.auth === 'jwt'">
              使用 JWT Token 鉴权，请在请求头中添加 <code>Authorization: Bearer &lt;TOKEN&gt;</code>
            </span>
            <span v-else-if="currentEndpoint.auth === 'ticket'">
              票据鉴权：本接口不走 JWT，只认 URL 上的 <code>?ticket=</code>（先调同路径的 <code>-ticket</code> 接口换票）。
              只带 <code>Authorization: Bearer &lt;TOKEN&gt;</code> 而不带票据同样会被拒绝
            </span>
            <span v-else>此接口无需鉴权即可访问</span>
          </div>

        <el-card
          v-if="currentEndpoint.pathParams?.length || currentEndpoint.queryParams?.length || currentEndpoint.bodyParams?.length"
          shadow="never" class="api-card dd-spot"
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
              <el-table-column prop="description" label="说明" min-width="180" />
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
              <el-table-column prop="description" label="说明" min-width="180" />
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
              <el-table-column prop="description" label="说明" min-width="180" />
              <el-table-column prop="example" label="示例" width="130">
                <template #default="{ row }"><span class="param-example">{{ row.example || '-' }}</span></template>
              </el-table-column>
            </el-table>
          </template>
        </el-card>

        <el-card shadow="never" class="api-card dd-spot">
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

        <el-card
          v-if="currentEndpoint.helperExamples && Object.keys(currentEndpoint.helperExamples).length"
          shadow="never"
          class="api-card dd-spot"
        >
          <template #header>
            <div class="section-title">脚本示例</div>
          </template>
          <el-tabs v-model="helperTab" class="code-tabs">
            <el-tab-pane
              v-for="lang in Object.keys(currentEndpoint.helperExamples)"
              :key="lang"
              :label="lang"
              :name="lang"
            >
              <div class="code-block-wrapper">
                <el-tooltip :content="copiedKey === `helper-${lang}` ? '已复制' : '复制代码'" placement="top">
                  <el-button
                    class="code-copy-btn"
                    size="small"
                    type="primary"
                    plain
                    @click="handleCopy(currentEndpoint.helperExamples?.[lang] || '', `helper-${lang}`)"
                  >
                    <el-icon><DocumentCopy /></el-icon>
                  </el-button>
                </el-tooltip>
                <pre class="code-block">{{ currentEndpoint.helperExamples?.[lang] }}</pre>
              </div>
            </el-tab-pane>
          </el-tabs>
        </el-card>

        <el-card v-if="currentEndpoint.responseExample" shadow="never" class="api-card dd-spot">
          <template #header>
            <div class="section-title">返回响应</div>
          </template>

          <div class="response-status">
            <span class="status-badge status-200">
              <span class="status-dot" />
              200 成功
            </span>
            <!-- 文件流类接口（如原始日志直传下载）返回的不是 JSON，按条目声明的类型展示 -->
            <span class="response-type">{{ currentEndpoint.responseContentType || 'application/json' }}</span>
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
                <el-table-column prop="description" label="说明" min-width="180" />
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
          <el-empty v-else description="正在加载接口文档..." />
        </Transition>
      </div>
    </div>
  </div>
</template>

<style scoped lang="scss">
.api-docs-page {
  min-height: 0;
}

.page-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 20px;

  .page-title {
    margin: 0;
    font-size: var(--dd-page-title-size, 22px);
    font-weight: var(--dd-page-title-weight, 700);
    letter-spacing: var(--dd-page-title-tracking, normal);
    display: flex;
    align-items: center;
    gap: 8px;
    color: var(--el-text-color-primary);
  }
}

.mobile-docs-bar {
  display: none;
  align-items: center;
  justify-content: flex-start;
  margin-bottom: 12px;
}

// 移动端接口列表抽屉：body 改纵向 flex 并关闭外层滚动，让顶部搜索框固定、仅下方列表滚动
:deep(.el-drawer.api-docs-drawer) {
  .el-drawer__body {
    padding: 0;
    display: flex;
    flex-direction: column;
    overflow: hidden;
  }
}

.api-layout {
  display: flex;
  gap: 16px;
  flex: 1 1 auto;
  min-height: 0;
}

.api-sider {
  width: 280px;
  flex-shrink: 0;
  background: var(--el-bg-color);
  // 描边宽走主题令牌 + 圆角刻度，使侧栏与右侧文档卡观感统一（不再用阴影浮起）
  border-radius: var(--dd-radius-surface);
  border: var(--dd-border-width, 1px) solid var(--dd-border, var(--el-border-color-lighter));
  overflow: hidden;
  height: 100%;
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
  min-height: 0;
}

// 分类计数从「分类名 (N)」的括号写法换成右对齐的 DdBadge（中性计数，描边不实心）。
// text-transform / letter-spacing 只留给标题文字：留在容器上会连角标数字一起加字距，
// 让等宽数字看起来偏散。
.menu-group-title {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  padding: 12px 20px 4px;
  font-weight: 600;
  font-size: 12px;
  color: var(--el-text-color-secondary);
}

.menu-group-label {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  text-transform: uppercase;
  letter-spacing: 0.5px;
}

.menu-item {
  display: flex;
  align-items: center;
  gap: 8px;
  // 左侧留出 3px 透明边框位，选中时染成品牌色指示条（原为 inset 阴影）
  padding: 6px 12px 6px 9px;
  border-left: 3px solid transparent;
  margin: 2px 8px;
  border-radius: var(--dd-radius-control);
  cursor: pointer;
  // 选中/hover 过渡走令牌（快 + 标准缓动），与全站侧边菜单观感一致
  transition:
    background-color var(--dd-motion-fast) var(--dd-ease-standard),
    color var(--dd-motion-fast) var(--dd-ease-standard),
    border-color var(--dd-motion-fast) var(--dd-ease-standard);

  &:hover:not(.active) {
    background: var(--el-fill-color-light);
  }

  // 选中态对齐全局菜单：品牌色浅底纯色 + 左侧品牌色指示条
  &.active {
    background: var(--el-color-primary-light-9);
    border-left-color: var(--el-color-primary);

    .menu-item-text {
      color: var(--dd-accent-ink);
      font-weight: 600;
    }
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
  border-radius: var(--dd-radius-control);
  font-size: 11px;
  font-weight: 700;
  letter-spacing: 0.5px;
  font-family: var(--dd-font-mono);
  color: var(--dd-on-status);
  line-height: 20px;
}

.method-badge-sm {
  min-width: 40px;
  padding: 0 5px;
  font-size: 10px;
  line-height: 18px;
  border-radius: var(--dd-radius-control);
}

// 方法色标改纯色（原为渐变）；底色读主题实心状态色，配 --dd-on-status 文字
// POST 没有现成的实心蓝：把 --dd-info 向正文色压 18%，亮色下变深、暗色下变亮，白字 / 深字都能过 4.5:1
.method-get { background: var(--dd-ok-solid); }
.method-post { background: color-mix(in srgb, var(--dd-info) 82%, var(--dd-text)); }
.method-put { background: var(--dd-warn-solid); }
.method-delete { background: var(--dd-err-solid); }

.api-content {
  flex: 1;
  background: var(--el-bg-color);
  // 描边宽走主题令牌 + 圆角刻度，不再用阴影浮起
  border-radius: var(--dd-radius-surface);
  border: var(--dd-border-width, 1px) solid var(--dd-border, var(--el-border-color-lighter));
  padding: 28px 32px;
  overflow: auto;
  min-width: 0;
}

.api-doc-body {
  min-width: 0;
}

.api-breadcrumb {
  font-size: 12px;
  color: var(--el-text-color-secondary);
  margin-bottom: 4px;
  letter-spacing: 0.3px;
}

.api-endpoint-title {
  font-size: var(--dd-page-title-size, 22px);
  font-weight: var(--dd-page-title-weight, 700);
  letter-spacing: var(--dd-page-title-tracking, normal);
  color: var(--el-text-color-primary);
  margin: 0 0 16px 0;
  line-height: 1.3;
}

.url-bar {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 12px 20px;
  // 素白（clean）主题下主卡已是白底，内部区块用 surface-2 + divider 区分层次
  background: var(--dd-surface-2, var(--el-fill-color-lighter));
  border: var(--dd-border-width, 1px) solid var(--dd-divider, var(--dd-border-strong));
  border-radius: var(--dd-radius-surface);
  margin-bottom: 24px;
  flex-wrap: wrap;
  // hover 只加深描边，不再叠阴影
  transition: border-color var(--dd-motion-normal) var(--dd-ease-standard);

  &:hover {
    border-color: var(--el-border-color);
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
  border-radius: var(--dd-radius-surface);
  margin-bottom: 20px;
  font-size: 13px;
  line-height: 1.7;

  // 票据鉴权那条说明会换行，图标不能被文字挤扁
  .el-icon {
    flex-shrink: 0;
  }

  code {
    font-size: 12px;
    // 用 color-mix 基于当前文字色生成半透明底，明暗双主题都自适应
    background: color-mix(in srgb, currentColor 10%, transparent);
    padding: 2px 6px;
    border-radius: var(--dd-radius-control);
  }

  // JWT / 无鉴权两种状态：用 Element Plus 语义色 + color-mix 生成浅底，明暗双主题自适应
  &.auth-jwt {
    background: color-mix(in srgb, var(--el-color-warning) 10%, var(--el-bg-color));
    border: 1px solid color-mix(in srgb, var(--el-color-warning) 32%, transparent);
    color: var(--dd-warn-ink);
  }

  // 票据鉴权：既不是常规 Bearer，也不是「无需鉴权」，用品牌色单列一档避免被误读成公开接口
  &.auth-ticket {
    background: color-mix(in srgb, var(--el-color-primary) 10%, var(--el-bg-color));
    border: 1px solid color-mix(in srgb, var(--el-color-primary) 32%, transparent);
    color: var(--dd-accent-ink);
  }

  &.auth-none {
    background: color-mix(in srgb, var(--el-color-success) 10%, var(--el-bg-color));
    border: 1px solid color-mix(in srgb, var(--el-color-success) 32%, transparent);
    color: var(--dd-ok-ink);
  }
}

// .api-card 嵌在 .api-content 内部，素白（clean）主题会把顶层 .el-card 边框设为 transparent，
// 这里提高选择器特异性并用 --dd-divider 保留嵌套子卡的可见边界
.api-docs-page .api-content .api-card.el-card {
  border-radius: var(--dd-radius-surface);
  border: var(--dd-border-width, 1px) solid var(--dd-divider, var(--dd-border-strong));
  margin-bottom: 20px;
  overflow: hidden;

  :deep(.el-card__header) {
    background: var(--dd-surface-2, var(--el-fill-color-light));
    border-bottom: var(--dd-border-width, 1px) solid var(--dd-divider, var(--dd-border-strong));
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
    // 3px 宽的品牌色指示条属于天然胶囊，吃 pill 档：直角模式是细方条，圆角模式两端收圆
    border-radius: var(--dd-radius-pill);
    background: var(--el-color-primary);
  }
}

.param-section-header {
  padding: 8px 16px;
  background: var(--dd-surface-2, var(--el-fill-color-lighter));
  border-bottom: var(--dd-border-width, 1px) solid var(--dd-divider, var(--dd-border-strong));
  font-weight: 600;
  font-size: 13px;
  color: var(--el-text-color-regular);
}

.param-name {
  font-size: 12px;
  background: var(--el-fill-color);
  padding: 2px 6px;
  border-radius: var(--dd-radius-control);
}

.param-example {
  font-family: var(--dd-font-mono);
  font-size: 12px;
  color: var(--el-text-color-secondary);
}

.code-tabs {
  padding: 0 16px 16px;

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
  max-width: 100%;
  border-radius: var(--dd-radius-surface);
  overflow: hidden;
  margin: 0;

  &:hover .code-copy-btn,
  &:focus-within .code-copy-btn {
    opacity: 1;
  }
}

.code-block {
  // 代码示例底色跟随主题：在不透明卡底上叠 6% 正文色，亮色是浅灰、暗色是略亮的深底
  background: color-mix(in srgb, var(--dd-text) 6%, var(--dd-surface-solid)) !important;
  color: var(--dd-text);
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
  // 时长/缓动走令牌：写死毫秒会绕过 prefers-reduced-motion 的降级
  transition: opacity var(--dd-motion-fast) var(--dd-ease-standard);

  &:focus-visible {
    opacity: 1;
  }
}

// 触屏设备无 hover 态，复制按钮常显避免无法发现
@media (hover: none) {
  .code-copy-btn {
    opacity: 1;
  }
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
  // 「200 OK」这类状态 chip 属于天然胶囊，吃 pill 档
  border-radius: var(--dd-radius-pill);
  font-size: 13px;
  font-weight: 600;

  .status-dot {
    width: 8px;
    height: 8px;
    // 圆形承载语义：状态指示圆点，圆形是通用可供性，方块会被误读成色块装饰。
    // 两种圆角模式下都保持圆形，不吃 --dd-radius-* 令牌。
    border-radius: 50%;
    background: var(--el-color-success);
    display: inline-block;
  }
}

// 200 成功标签：用 Element Plus success 语义色 + color-mix 浅底，明暗双主题自适应
.status-200 {
  background: color-mix(in srgb, var(--el-color-success) 12%, var(--el-bg-color));
  color: var(--dd-ok-ink);
  border: 1px solid color-mix(in srgb, var(--el-color-success) 32%, transparent);
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

  .mobile-docs-bar {
    display: flex;
  }

  .api-content {
    padding: 16px;
  }

  .api-endpoint-title {
    font-size: var(--dd-page-title-size-mobile, 18px);
  }

  .api-content :deep(.el-table) {
    display: block;
    overflow-x: auto;
  }

  .code-copy-btn {
    opacity: 1;
  }

  .response-section {
    flex-direction: column;
  }

  // 移动端解除桌面端 min-width: 300px，避免长 JSON 把 flex 项撑破导致 <pre class="code-block"> 无法横向滚动
  .response-fields,
  .response-example {
    min-width: 0;
    width: 100%;
    max-width: 100%;
  }

  .url-bar {
    padding: 10px 14px;
  }
}

// ===== 接口切换过渡 =====
// 只做 opacity，**不带任何位移**：本页正文长达数屏、外层 .api-content 自己是滚动容器，
// 位移会让滚动条与长代码块一起抖。out-in 保证不会出现新旧两篇文档同时占位、
// 把容器高度顶成两倍的情况。
// 首次入场交由全局 .dd-rise 负责，Transition 未挂 appear，不会与首屏入场叠加。
.dd-apidocs-switch-enter-active {
  transition: opacity var(--dd-motion-normal) var(--dd-ease-decelerate);
}

.dd-apidocs-switch-leave-active {
  transition: opacity var(--dd-motion-fast) var(--dd-ease-standard);
}

.dd-apidocs-switch-enter-from,
.dd-apidocs-switch-leave-to {
  opacity: 0;
}
</style>
