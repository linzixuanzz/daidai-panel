<template>
  <div>
    <div class="page-header">
      <div>
        <h2>Open API 管理</h2>
        <span class="page-subtitle">创建和管理外部 API 调用应用密钥</span>
      </div>
      <el-button type="primary" @click="showCreateDialog">创建应用</el-button>
    </div>

    <div v-if="isMobile" class="dd-mobile-list">
      <div
        v-for="row in apps"
        :key="row.id"
        class="dd-mobile-card"
      >
        <div class="dd-mobile-card__header">
          <div class="dd-mobile-card__title-wrap">
            <span class="dd-mobile-card__title">{{ row.name }}</span>
            <div class="dd-mobile-card__subtitle">{{ row.app_key }}</div>
          </div>
          <el-switch
            :model-value="row.enabled"
            @change="(val: boolean) => toggleEnabled(row, val)"
            size="small"
          />
        </div>

        <div class="dd-mobile-card__body">
          <div class="dd-mobile-card__grid">
            <div class="dd-mobile-card__field dd-mobile-card__field--full">
              <span class="dd-mobile-card__label">App Secret</span>
              <div class="dd-mobile-card__value">
                <template v-if="revealedSecrets[row.id]">
                  <div class="key-display">
                    <code class="key-code secret-code">{{ revealedSecrets[row.id] }}</code>
                    <el-button class="copy-btn" size="small" @click="copyText(revealedSecrets[row.id] || '')">
                      <el-icon><DocumentCopy /></el-icon>
                    </el-button>
                    <el-button class="copy-btn" size="small" @click="delete revealedSecrets[row.id]">
                      <el-icon><Hide /></el-icon>
                    </el-button>
                  </div>
                </template>
                <template v-else>
                  <div class="key-display">
                    <span class="secret-mask">••••••••••••••••</span>
                    <el-button type="primary" link size="small" @click="viewSecret(row)">
                      <el-icon><View /></el-icon>查看
                    </el-button>
                  </div>
                </template>
              </div>
            </div>
            <div class="dd-mobile-card__field dd-mobile-card__field--full">
              <span class="dd-mobile-card__label">权限范围</span>
              <div class="dd-mobile-card__value">
                <el-tag
                  v-for="s in parseScopesTags(row.scopes)"
                  :key="s"
                  size="small"
                  style="margin: 2px 4px 2px 0"
                >{{ s }}</el-tag>
                <span v-if="!row.scopes" style="color: var(--el-text-color-secondary)">未授权任何范围</span>
              </div>
            </div>
            <div class="dd-mobile-card__field">
              <span class="dd-mobile-card__label">速率限制</span>
              <span class="dd-mobile-card__value">{{ formatRateLimit(row.rate_limit) }}</span>
            </div>
            <div class="dd-mobile-card__field">
              <span class="dd-mobile-card__label">今日调用</span>
              <span class="dd-mobile-card__value">{{ row.call_count }}</span>
            </div>
          </div>

          <div class="dd-mobile-card__actions open-api-card__actions">
            <el-button size="small" type="primary" plain @click="editApp(row)">编辑</el-button>
            <el-button size="small" type="warning" plain @click="resetSecret(row)">重置密钥</el-button>
            <el-button size="small" @click="showLogs(row)">日志</el-button>
            <el-button size="small" type="danger" plain @click="deleteApp(row)">删除</el-button>
          </div>
        </div>
      </div>

      <el-empty v-if="!loading && apps.length === 0" description="暂无应用" />
    </div>

    <el-table v-else :data="apps" v-loading="loading" border>
      <el-table-column prop="name" label="应用名称" min-width="120" />
      <el-table-column label="App Key" min-width="280">
        <template #default="{ row }">
          <div class="key-display">
            <code class="key-code">{{ row.app_key }}</code>
            <el-button class="copy-btn" size="small" @click="copyText(row.app_key)">
              <el-icon><DocumentCopy /></el-icon>
            </el-button>
          </div>
        </template>
      </el-table-column>
      <el-table-column label="App Secret" min-width="280">
        <template #default="{ row }">
          <div class="key-display">
            <template v-if="revealedSecrets[row.id]">
              <code class="key-code secret-code">{{ revealedSecrets[row.id] }}</code>
              <el-button class="copy-btn" size="small" @click="copyText(revealedSecrets[row.id] || '')">
                <el-icon><DocumentCopy /></el-icon>
              </el-button>
              <el-button class="copy-btn" size="small" @click="delete revealedSecrets[row.id]">
                <el-icon><Hide /></el-icon>
              </el-button>
            </template>
            <template v-else>
              <span class="secret-mask">••••••••••••••••</span>
              <el-button type="primary" link size="small" @click="viewSecret(row)">
                <el-icon><View /></el-icon>查看
              </el-button>
            </template>
          </div>
        </template>
      </el-table-column>
      <el-table-column label="权限范围" min-width="180">
        <template #default="{ row }">
          <el-tag
            v-for="s in parseScopesTags(row.scopes)"
            :key="s"
            size="small"
            style="margin: 2px 4px 2px 0"
          >{{ s }}</el-tag>
          <span v-if="!row.scopes" style="color: var(--el-text-color-secondary)">未授权任何范围</span>
        </template>
      </el-table-column>
      <el-table-column label="速率限制" width="120" align="center">
        <template #default="{ row }">
          {{ formatRateLimit(row.rate_limit) }}
        </template>
      </el-table-column>
      <el-table-column prop="call_count" label="今日调用" width="100" align="center" />
      <el-table-column label="状态" width="80" align="center">
        <template #default="{ row }">
          <el-switch
            :model-value="row.enabled"
            @change="(val: boolean) => toggleEnabled(row, val)"
            size="small"
          />
        </template>
      </el-table-column>
      <el-table-column label="操作" width="200" align="center">
        <template #default="{ row }">
          <el-button type="primary" link size="small" @click="editApp(row)">编辑</el-button>
          <el-button type="warning" link size="small" @click="resetSecret(row)">重置密钥</el-button>
          <el-button type="info" link size="small" @click="showLogs(row)">日志</el-button>
          <el-button type="danger" link size="small" @click="deleteApp(row)">删除</el-button>
        </template>
      </el-table-column>
    </el-table>

    <el-dialog v-model="dialogVisible" :title="editingApp ? '编辑应用' : '新建应用'" width="500px" :fullscreen="dialogFullscreen">
      <el-form :model="form" label-position="top">
        <el-form-item label="应用名称" required>
          <el-input v-model="form.name" placeholder="例如：外部调度系统" />
        </el-form-item>
        <el-form-item label="权限范围">
          <el-select
            v-model="form.scopesList"
            multiple
            filterable
            placeholder="请选择允许访问的资源范围"
            style="width: 100%"
          >
            <el-option v-for="s in scopeOptions" :key="s.value" :label="s.label" :value="s.value" />
          </el-select>
          <div style="margin-top: 6px; color: var(--el-text-color-secondary); font-size: 12px">
            默认拒绝。留空表示该应用创建成功，但没有任何接口访问权限。
          </div>
        </el-form-item>
        <el-form-item label="速率限制">
          <el-input-number v-model="form.rate_limit" :min="0" :max="10000" />
          <span style="margin-left: 8px; color: var(--el-text-color-secondary)">次/小时，填 0 表示无限制</span>
          <div style="margin-top: 6px; color: var(--el-text-color-secondary); font-size: 12px">
            调用次数按当天统计展示，每日会重新从 0 开始累计。
          </div>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="dialogVisible = false">取消</el-button>
        <el-button type="primary" @click="submitForm">确定</el-button>
      </template>
    </el-dialog>

    <el-dialog v-model="secretDialogVisible" title="应用密钥" width="560px" :fullscreen="dialogFullscreen" :close-on-click-modal="false">
      <el-alert type="warning" :closable="false" style="margin-bottom: 16px">
        请妥善保管密钥，关闭后需要验证密码才能再次查看 App Secret。
      </el-alert>
      <div class="secret-display-card">
        <div class="secret-row">
          <span class="secret-label">App Key</span>
          <div class="secret-value-box">
            <code class="secret-value-text">{{ secretData.app_key }}</code>
            <el-button class="copy-btn" size="small" @click="copyText(secretData.app_key)">
              <el-icon><DocumentCopy /></el-icon> 复制
            </el-button>
          </div>
        </div>
        <div class="secret-row">
          <span class="secret-label">App Secret</span>
          <div class="secret-value-box">
            <code class="secret-value-text">{{ secretData.app_secret }}</code>
            <el-button class="copy-btn" size="small" @click="copyText(secretData.app_secret)">
              <el-icon><DocumentCopy /></el-icon> 复制
            </el-button>
          </div>
        </div>
      </div>
    </el-dialog>

    <el-dialog v-model="logsDialogVisible" title="调用日志" width="700px" :fullscreen="dialogFullscreen">
      <el-table :data="callLogs" size="small" max-height="400">
        <el-table-column prop="endpoint" label="接口" min-width="200" show-overflow-tooltip />
        <el-table-column prop="method" label="方法" width="80" />
        <el-table-column prop="status" label="状态码" width="80" align="center" />
        <el-table-column prop="duration" label="耗时(ms)" width="90" align="center" />
        <el-table-column prop="ip" label="IP" width="140" />
        <el-table-column prop="created_at" label="时间" width="170">
          <template #default="{ row }">
            {{ new Date(row.created_at).toLocaleString('zh-CN') }}
          </template>
        </el-table-column>
      </el-table>
      <el-pagination
        v-if="logTotal > 20"
        style="margin-top: 12px; justify-content: flex-end"
        layout="total, prev, pager, next"
        :total="logTotal"
        :page-size="20"
        v-model:current-page="logPage"
        @current-change="loadLogs"
      />
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, onMounted } from 'vue'
import { openApiApi } from '@/api/open-api'
import { ElMessage, ElMessageBox } from 'element-plus'
import { useResponsive } from '@/composables/useResponsive'

const apps = ref<any[]>([])
const loading = ref(false)
const dialogVisible = ref(false)
const secretDialogVisible = ref(false)
const logsDialogVisible = ref(false)
const editingApp = ref<any>(null)
const secretData = ref<any>({})
const callLogs = ref<any[]>([])
const logTotal = ref(0)
const logPage = ref(1)
const currentLogAppId = ref(0)
const revealedSecrets = reactive<Record<number, string>>({})
const { isMobile, dialogFullscreen } = useResponsive()

const form = ref({ name: '', scopesList: [] as string[], rate_limit: 0 })

const scopeOptions = [
  { label: '任务管理', value: 'tasks' },
  { label: '脚本管理', value: 'scripts' },
  { label: '环境变量', value: 'envs' },
  { label: '订阅管理', value: 'subscriptions' },
  { label: '日志查看', value: 'logs' },
  { label: '系统信息', value: 'system' },
]

const scopeLabelMap: Record<string, string> = Object.fromEntries(scopeOptions.map(s => [s.value, s.label]))

const parseScopesTags = (scopes: string): string[] => {
  if (!scopes) return []
  return scopes.split(',').map(s => s.trim()).filter(Boolean).map(s => scopeLabelMap[s] || s)
}

const scopesToString = (list: string[]): string => list.join(',')

const stringToScopes = (str: string): string[] => {
  if (!str) return []
  return str.split(',').map(s => s.trim()).filter(Boolean)
}

const formatRateLimit = (value: number | null | undefined) => {
  return Number(value || 0) > 0 ? `${value} / 小时` : '无限制'
}

const copyText = async (text: string) => {
  if (!text) return
  try {
    await navigator.clipboard.writeText(text)
    ElMessage.success('已复制')
  } catch {
    const textarea = document.createElement('textarea')
    textarea.value = text
    textarea.style.position = 'fixed'
    textarea.style.opacity = '0'
    document.body.appendChild(textarea)
    textarea.select()
    document.execCommand('copy')
    document.body.removeChild(textarea)
    ElMessage.success('已复制')
  }
}

const loadApps = async () => {
  loading.value = true
  try {
    const res = await openApiApi.list()
    apps.value = (res as any).data || []
  } catch (err: any) {
    ElMessage.error(err?.response?.data?.error || '加载应用列表失败')
  } finally {
    loading.value = false
  }
}

const showCreateDialog = () => {
  editingApp.value = null
  form.value = { name: '', scopesList: [], rate_limit: 0 }
  dialogVisible.value = true
}

const editApp = (app: any) => {
  editingApp.value = app
  form.value = { name: app.name, scopesList: stringToScopes(app.scopes || ''), rate_limit: Number(app.rate_limit || 0) }
  dialogVisible.value = true
}

const submitForm = async () => {
  if (!form.value.name) {
    ElMessage.warning('请输入应用名称')
    return
  }
  const payload = {
    name: form.value.name,
    scopes: scopesToString(form.value.scopesList),
    rate_limit: form.value.rate_limit,
  }
  try {
    if (editingApp.value) {
      await openApiApi.update(editingApp.value.id, payload)
      ElMessage.success('更新成功')
    } else {
      const res = await openApiApi.create(payload)
      secretData.value = (res as any).data || {}
      secretDialogVisible.value = true
      ElMessage.success('创建成功')
    }
    dialogVisible.value = false
    loadApps()
  } catch (err: any) {
    ElMessage.error(err?.response?.data?.error || '操作失败')
  }
}

const viewSecret = async (app: any) => {
  try {
    const { value: password } = await ElMessageBox.prompt('请输入登录密码以查看 App Secret', '身份验证', {
      inputType: 'password',
      inputPlaceholder: '请输入密码',
      confirmButtonText: '确认',
      cancelButtonText: '取消',
    })
    if (!password) return
    const res = await openApiApi.viewSecret(app.id, password) as any
    revealedSecrets[app.id] = res.data?.app_secret || ''
  } catch (e: any) {
    if (e === 'cancel' || e?.toString() === 'cancel') return
    ElMessage.error(e?.response?.data?.error || '验证失败')
  }
}

const toggleEnabled = async (app: any, val: boolean) => {
  try {
    await ElMessageBox.confirm(
      val
        ? `确认启用应用「${app.name}」吗？`
        : `确认禁用应用「${app.name}」吗？禁用后该 App Key / App Secret 将立即失效。`,
      val ? '启用确认' : '禁用确认',
      { type: val ? 'info' : 'warning' }
    )
    if (val) {
      await openApiApi.enable(app.id)
    } else {
      await openApiApi.disable(app.id)
    }
    app.enabled = val
    ElMessage.success(val ? '已启用' : '已禁用')
  } catch (err: any) {
    if (err === 'cancel' || err?.toString?.() === 'cancel') return
    ElMessage.error(err?.response?.data?.error || '操作失败')
  }
}

const resetSecret = async (app: any) => {
  try {
    await ElMessageBox.confirm('确认重置密钥？旧密钥将立即失效。', '警告', { type: 'warning' })
  } catch { return }
  try {
    const res = await openApiApi.resetSecret(app.id)
    secretData.value = (res as any).data || {}
    secretDialogVisible.value = true
    delete revealedSecrets[app.id]
    ElMessage.success('密钥已重置')
  } catch (err: any) {
    ElMessage.error(err?.response?.data?.error || '重置失败')
  }
}

const deleteApp = async (app: any) => {
  try {
    await ElMessageBox.confirm(`确认删除应用 "${app.name}"？`, '提示', { type: 'warning' })
  } catch { return }
  try {
    await openApiApi.delete(app.id)
    ElMessage.success('删除成功')
    loadApps()
  } catch (err: any) {
    ElMessage.error(err?.response?.data?.error || '删除失败')
  }
}

const showLogs = (app: any) => {
  currentLogAppId.value = app.id
  logPage.value = 1
  logsDialogVisible.value = true
  loadLogs()
}

const loadLogs = async () => {
  try {
    const res = await openApiApi.callLogs(currentLogAppId.value, {
      page: logPage.value,
      page_size: 20,
    })
    callLogs.value = (res as any).data || []
    logTotal.value = (res as any).total || 0
  } catch (err: any) {
    ElMessage.error(err?.response?.data?.error || '加载调用日志失败')
  }
}

onMounted(loadApps)
</script>

<style scoped lang="scss">
.page-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 16px;

  h2 { margin: 0; font-size: 20px; font-weight: 700; color: var(--el-text-color-primary); }

  .page-subtitle {
    font-size: 13px;
    color: var(--el-text-color-secondary);
    display: block;
    margin-top: 2px;
  }
}

.key-display {
  display: flex;
  align-items: center;
  gap: 6px;
}

.key-code {
  font-size: 12px;
  word-break: break-all;
  background: var(--el-fill-color-light);
  padding: 4px 8px;
  border-radius: 4px;
  border: 1px solid var(--el-border-color-lighter);
  font-family: var(--dd-font-mono);
  flex: 1;
  min-width: 0;
}

.secret-code {
  background: var(--el-color-warning-light-9);
  border-color: var(--el-color-warning-light-5);
}

.secret-mask {
  color: var(--el-text-color-placeholder);
  letter-spacing: 2px;
}

.copy-btn {
  flex-shrink: 0;
  border: 1px solid var(--el-border-color);
  background: var(--el-fill-color-blank);
  &:hover {
    color: var(--el-color-primary);
    border-color: var(--el-color-primary-light-5);
    background: var(--el-color-primary-light-9);
  }
}

.secret-display-card {
  background: var(--el-fill-color-light);
  border-radius: 8px;
  padding: 20px;
  border: 1px solid var(--el-border-color-lighter);
}

.secret-row {
  &:not(:last-child) {
    margin-bottom: 16px;
    padding-bottom: 16px;
    border-bottom: 1px dashed var(--el-border-color-lighter);
  }
}

.secret-label {
  display: block;
  font-size: 13px;
  font-weight: 600;
  color: var(--el-text-color-secondary);
  margin-bottom: 8px;
}

.secret-value-box {
  display: flex;
  align-items: center;
  gap: 8px;
  background: var(--el-fill-color-blank);
  border: 1px solid var(--el-border-color);
  border-radius: 6px;
  padding: 10px 12px;
}

.secret-value-text {
  flex: 1;
  font-size: 13px;
  font-family: var(--dd-font-mono);
  word-break: break-all;
  line-height: 1.5;
}

.open-api-card__actions > * {
  flex: 1 1 calc(50% - 4px);
}

@media (max-width: 768px) {
  .page-header {
    flex-direction: column;
    align-items: stretch;
    gap: 10px;
  }
}
</style>
