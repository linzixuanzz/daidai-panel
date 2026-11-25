<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { ChatDotSquare, Document, DocumentCopy, Plus, Refresh } from '@element-plus/icons-vue'
import { copyText } from '@/utils/clipboard'
import { openApiApi } from '@/api/open-api'
import { wecomTriggerApi, type WecomTrigger } from '@/api/wecom'
import { useResponsive } from '@/composables/useResponsive'
import type { WecomTriggerConfigFields } from '../useSettingsConfig'

// 形态照抄同目录的 McpConfigCard：两个开关的值由父级的 configForm 持有（跟着「保存配置」一起回写），
// 接入配置的增删改查则是本卡自己发请求，与配置项无关。
defineProps<{
  configsLoading: boolean
  configsSaving: boolean
  form: WecomTriggerConfigFields
  onSave: () => void
}>()

const { dialogFullscreen } = useResponsive()

// 企业微信后台给的 EncodingAESKey 固定 43 位。服务端也校验（validateWecomTriggerInput），
// 这里提前拦一道纯粹是体验问题：配错了要等企业微信那边验签失败才发现，排查成本高得多。
const ENCODING_AES_KEY_LENGTH = 43

const triggers = ref<WecomTrigger[]>([])
const listLoading = ref(false)
// 开放 API 应用列表：接入配置必须绑一个，回调时用它的凭据回放 /tasks 接口。
// 没有 tasks 权限的应用铸出来的令牌会在回放时被 403（表现为「指令发了但任务没跑」），
// 所以这里把每个应用的权限范围一起显示出来，让用户选之前就能看出来。
const apps = ref<any[]>([])
const appsLoaded = ref(false)

const dialogVisible = ref(false)
const submitting = ref(false)
// 正在编辑哪一条；null 表示新建
const editingTrigger = ref<WecomTrigger | null>(null)

const triggerForm = reactive({
  name: '',
  corp_id: '',
  agent_id: '',
  callback_token: '',
  encoding_aes_key: '',
  open_app_id: undefined as number | undefined,
  task_whitelist: '',
  enabled: true,
})

const dialogTitle = computed(() => (editingTrigger.value ? '编辑企业微信接入配置' : '新建企业微信接入配置'))

// 两项凭据读不回来（服务端 json:"-"，任何接口都不回显），编辑时留空即「不修改」。
const isEditing = computed(() => editingTrigger.value !== null)

const appOptions = computed(() =>
  apps.value.map((app) => ({
    value: Number(app.id),
    label: String(app.name || `应用 #${app.id}`),
    scopes: String(app.scopes || ''),
    enabled: Boolean(app.enabled),
  }))
)

function appLabel(appID: number) {
  const matched = appOptions.value.find((item) => item.value === Number(appID))
  if (matched) return matched.label
  // 应用被删掉之后这条接入配置就废了（回放时报「绑定的开放 API 应用不存在」），要显式说出来
  return appsLoaded.value ? `应用 #${appID}（已不存在）` : `应用 #${appID}`
}

// 回调地址由服务端拼好（callback_path），前端只补上当前面板的 origin：
// 面板同时挂着 /api 与 /api/v1 两套前缀，前端自己拼容易拼成企业微信后台配不通的那一套。
function callbackUrl(trigger: WecomTrigger) {
  const path = trigger.callback_path || `/api/v1/wecom/callback/${trigger.id}`
  return `${window.location.origin}${path}`
}

async function handleCopy(text: string, successMessage: string) {
  try {
    await copyText(text)
    ElMessage.success(successMessage)
  } catch {
    ElMessage.error('复制失败，请检查浏览器权限或站点访问方式')
  }
}

async function loadTriggers() {
  listLoading.value = true
  try {
    const res = await wecomTriggerApi.list()
    triggers.value = res.data || []
  } catch (err: any) {
    ElMessage.error(err?.response?.data?.error || '加载企业微信接入配置失败')
  } finally {
    listLoading.value = false
  }
}

async function loadApps() {
  try {
    const res = (await openApiApi.list()) as any
    apps.value = res.data || []
    appsLoaded.value = true
  } catch {
    // 取不到应用列表不算致命：下拉会是空的，但已有配置仍然能看能删，
    // 所以这里不弹错，只是保持 appsLoaded=false，让 appLabel 不要误报「已不存在」
    appsLoaded.value = false
  }
}

function openCreate() {
  editingTrigger.value = null
  Object.assign(triggerForm, {
    name: '',
    corp_id: '',
    agent_id: '',
    callback_token: '',
    encoding_aes_key: '',
    open_app_id: undefined,
    task_whitelist: '',
    enabled: true,
  })
  dialogVisible.value = true
}

function openEdit(trigger: WecomTrigger) {
  editingTrigger.value = trigger
  Object.assign(triggerForm, {
    name: trigger.name,
    corp_id: trigger.corp_id,
    agent_id: trigger.agent_id || '',
    // 两项凭据一律回填空串：服务端不回显，填不回来；留空提交时服务端会当「不修改」
    callback_token: '',
    encoding_aes_key: '',
    open_app_id: Number(trigger.open_app_id) || undefined,
    task_whitelist: trigger.task_whitelist || '',
    enabled: trigger.enabled,
  })
  dialogVisible.value = true
}

// 提交前的本地校验。与服务端同口径，只是把报错提前到点「确定」的那一刻。
function validateForm(): string {
  if (!triggerForm.name.trim()) return '请填写配置名称'
  if (!triggerForm.corp_id.trim()) return '请填写 CorpID'
  if (!triggerForm.open_app_id) return '请选择绑定的开放 API 应用'

  const token = triggerForm.callback_token.trim()
  const aesKey = triggerForm.encoding_aes_key.trim()
  if (!isEditing.value) {
    if (!token) return '请填写 Token'
    if (!aesKey) return '请填写 EncodingAESKey'
  }
  // 长度按码点数算，与服务端的 len([]rune(...)) 对齐（用 .length 会把代理对算成两个）
  if (aesKey && Array.from(aesKey).length !== ENCODING_AES_KEY_LENGTH) {
    return `EncodingAESKey 必须是企业微信后台给出的 ${ENCODING_AES_KEY_LENGTH} 位字符串`
  }
  return ''
}

async function submitForm() {
  const message = validateForm()
  if (message) {
    ElMessage.warning(message)
    return
  }

  const token = triggerForm.callback_token.trim()
  const aesKey = triggerForm.encoding_aes_key.trim()
  // 服务端更新接口是「按键更新」：没出现的键一概不动已有值。
  // 所以两项凭据留空时直接不传，而不是传空串——语义更清楚，也不依赖服务端那条兜底。
  const payload: Record<string, unknown> = {
    name: triggerForm.name.trim(),
    corp_id: triggerForm.corp_id.trim(),
    agent_id: triggerForm.agent_id.trim(),
    open_app_id: triggerForm.open_app_id,
    task_whitelist: triggerForm.task_whitelist.trim(),
    enabled: triggerForm.enabled,
  }
  if (token) payload['callback_token'] = token
  if (aesKey) payload['encoding_aes_key'] = aesKey

  submitting.value = true
  try {
    if (editingTrigger.value) {
      await wecomTriggerApi.update(editingTrigger.value.id, payload as any)
      ElMessage.success('更新成功')
    } else {
      await wecomTriggerApi.create(payload as any)
      ElMessage.success('创建成功，请把下方的回调 URL 填进企业微信后台')
    }
    dialogVisible.value = false
    await loadTriggers()
  } catch (err: any) {
    ElMessage.error(err?.response?.data?.error || '保存失败')
  } finally {
    submitting.value = false
  }
}

async function removeTrigger(trigger: WecomTrigger) {
  try {
    await ElMessageBox.confirm(
      `确认删除接入配置「${trigger.name}」？删除后企业微信后台配的那条回调 URL 会立刻失效，需要重新创建并重新配置。`,
      '删除确认',
      { type: 'warning' }
    )
  } catch {
    return
  }
  try {
    await wecomTriggerApi.delete(trigger.id)
    ElMessage.success('删除成功')
    await loadTriggers()
  } catch (err: any) {
    ElMessage.error(err?.response?.data?.error || '删除失败')
  }
}

// 列表里的启用开关：只改 enabled 一个键，其余字段不动（服务端按键更新）
async function toggleEnabled(trigger: WecomTrigger, value: boolean) {
  try {
    await wecomTriggerApi.update(trigger.id, { enabled: value })
    trigger.enabled = value
    ElMessage.success(value ? '已启用' : '已禁用')
  } catch (err: any) {
    ElMessage.error(err?.response?.data?.error || '操作失败')
  }
}

function refreshAll() {
  void loadTriggers()
  void loadApps()
}

onMounted(() => {
  // 本卡所在的标签页带 lazy，首次切过去才会挂载，不会在设置页一打开就发这两个请求
  refreshAll()
})
</script>

<template>
  <el-card shadow="never" v-loading="configsLoading">
    <template #header>
      <div class="card-header">
        <span class="card-title"><el-icon><ChatDotSquare /></el-icon> 企业微信触发</span>
        <el-button type="primary" :loading="configsSaving" @click="onSave">
          <el-icon><Document /></el-icon>保存配置
        </el-button>
      </div>
    </template>

    <el-alert
      title="开启后，企业微信自建应用里发一句「运行 签到任务」，或点一下菜单链接，就能拉起面板任务。执行走绑定的开放 API 应用，权限、频率限制与调用日志都和 Open API 一致。"
      type="info"
      :closable="false"
      style="margin-bottom: 16px"
    />

    <!-- 前置条件必须写在最显眼的位置：前两条缺一件，发指令就拉不起任务，
         而失败现象（企业微信后台点保存报「回调地址不可用」）根本看不出缺的是哪一件。
         三条按网络方向分开写（issue #152）：第一条是企业微信服务器 → 面板（入站回调），
         第三条是面板 → 企业微信（出站推送结果）。以前混在一起讲，没有公网 IP 的用户会以为
         配个 wxchat 这类微信代理就能在内网收到回调；其实代理只管出站，只能解决第三条的可信 IP。
         「只回调 80/443」有社区用非标端口配通的反例，没实测前只写「建议」。 -->
    <el-alert type="warning" :closable="false" style="margin-bottom: 16px">
      <template #title>使用前提</template>
      <ul class="prereq-list">
        <li><strong>发指令拉起（回调式）要求企业微信服务器能访问到面板的回调 URL</strong>，建议使用 80 / 443 端口。家里没有公网 IP 时需要内网穿透或 VPS 反代；wxchat 这类微信代理只转发面板发出去的请求，<strong>不能</strong>填作回调地址。</li>
        <li>需要在企业微信后台「自建应用 → 接收消息服务器配置」里填下方的回调 URL，以及同一条配置里的 Token 与 EncodingAESKey。</li>
        <li>执行结果通过「企业微信应用」通知渠道推送，要求面板的出口 IP 在<strong>企业可信 IP</strong> 里，否则应用发消息会被拒。出口是动态 IP 时，可以在该渠道的「反代基础地址 / 微信代理」填 wxchat 地址，并把 VPS 的 IP 加进可信 IP；也可以填「代理地址」。</li>
      </ul>
    </el-alert>

    <div class="config-section">
      <h4 class="section-title">服务开关</h4>
      <div class="form-field">
        <div class="switch-item">
          <span class="switch-label">启用企业微信触发</span>
          <el-switch v-model="form.wecom_trigger_enabled" inline-prompt active-text="开" inactive-text="关" />
        </div>
        <span class="form-hint">
          关闭时回调路由直接拒绝（连验签都不做），菜单链接也打不开。保存后立即生效，不需要重启面板。
        </span>
      </div>
      <div class="form-field">
        <div class="switch-item">
          <span class="switch-label">允许企业微信触发执行</span>
          <el-switch
            v-model="form.wecom_trigger_allow_run"
            :disabled="!form.wecom_trigger_enabled"
            inline-prompt
            active-text="开"
            inactive-text="关"
          />
        </div>
        <span v-if="form.wecom_trigger_enabled" class="form-hint">
          关掉这一项时只验签、不执行：可以先在企业微信后台把回调 URL 配通，确认没问题再打开执行。
        </span>
        <span v-else class="form-hint">需要先启用企业微信触发，这一项才会生效。</span>
      </div>
    </div>

    <div class="config-section">
      <div class="section-title section-title--with-actions">
        <span>接入配置</span>
        <div class="section-actions">
          <el-button size="small" @click="refreshAll">
            <el-icon><Refresh /></el-icon>刷新
          </el-button>
          <el-button size="small" type="primary" @click="openCreate">
            <el-icon><Plus /></el-icon>新建接入配置
          </el-button>
        </div>
      </div>

      <div v-loading="listLoading" class="trigger-list">
        <el-empty
          v-if="!listLoading && triggers.length === 0"
          description="还没有接入配置。新建一条之后，把它的回调 URL 填进企业微信后台即可。"
          :image-size="80"
        />

        <div v-for="item in triggers" :key="item.id" class="trigger-item">
          <div class="trigger-item__header">
            <div class="trigger-item__title-wrap">
              <span class="trigger-item__name">{{ item.name }}</span>
              <el-tag size="small" :type="item.enabled ? 'success' : 'info'">
                {{ item.enabled ? '已启用' : '已禁用' }}
              </el-tag>
            </div>
            <div class="trigger-item__actions">
              <el-switch
                :model-value="item.enabled"
                size="small"
                @change="(val: any) => toggleEnabled(item, Boolean(val))"
              />
              <el-button size="small" type="primary" plain @click="openEdit(item)">编辑</el-button>
              <el-button size="small" type="danger" plain @click="removeTrigger(item)">删除</el-button>
            </div>
          </div>

          <div class="trigger-item__meta">
            <span><em>CorpID</em>{{ item.corp_id }}</span>
            <span><em>AgentID</em>{{ item.agent_id || '未填' }}</span>
            <span><em>绑定应用</em>{{ appLabel(item.open_app_id) }}</span>
            <span><em>任务白名单</em>{{ item.task_whitelist || '不限制' }}</span>
          </div>

          <!-- 回调 URL 是整个功能的命门：拿不到它就没法在企业微信后台配，功能等于用不了，
               所以放在卡片正面、可一键复制，而不是塞进编辑弹窗里。 -->
          <div class="trigger-item__callback">
            <label>回调 URL</label>
            <el-input :model-value="callbackUrl(item)" readonly class="callback-input">
              <template #append>
                <el-button
                  :icon="DocumentCopy"
                  aria-label="复制回调 URL"
                  @click="handleCopy(callbackUrl(item), '回调 URL 已复制')"
                />
              </template>
            </el-input>
            <span class="form-hint">
              填进企业微信后台「接收消息服务器配置」的 URL 一栏。地址按你当前访问面板的方式生成，
              经反向代理或内网穿透时请换成企业微信那边能访问到的公网地址。
            </span>
          </div>
        </div>
      </div>
    </div>

    <el-alert
      type="warning"
      show-icon
      :closable="false"
      title="Token 与 EncodingAESKey 是这条公网入口的全部身份凭据，泄漏任意一个都等于把触发权交出去。面板不回显这两项（只能写入、读不回来），忘了就在企业微信后台重新生成并回来改。"
    />

    <el-dialog v-model="dialogVisible" :title="dialogTitle" width="560px" :fullscreen="dialogFullscreen">
      <el-form :model="triggerForm" label-position="top">
        <el-form-item label="配置名称" required>
          <el-input v-model="triggerForm.name" placeholder="例如：运维群自建应用" />
        </el-form-item>

        <el-form-item label="CorpID" required>
          <el-input v-model="triggerForm.corp_id" placeholder="企业微信后台「我的企业」里的企业 ID" />
          <div class="field-hint">解密后会拿它校验消息归属，对不上一律丢弃——这是这条公网回调唯一的身份锚点。</div>
        </el-form-item>

        <el-form-item label="AgentID">
          <el-input v-model="triggerForm.agent_id" placeholder="自建应用的 AgentID，可留空" />
        </el-form-item>

        <el-form-item label="Token" :required="!isEditing">
          <el-input
            v-model="triggerForm.callback_token"
            type="password"
            show-password
            :placeholder="isEditing ? '留空表示不修改' : '企业微信后台「接收消息服务器配置」里的 Token'"
          />
        </el-form-item>

        <el-form-item label="EncodingAESKey" :required="!isEditing">
          <el-input
            v-model="triggerForm.encoding_aes_key"
            type="password"
            show-password
            :placeholder="isEditing ? '留空表示不修改' : `企业微信后台给出的 ${ENCODING_AES_KEY_LENGTH} 位字符串`"
          />
          <div class="field-hint">
            固定 {{ ENCODING_AES_KEY_LENGTH }} 位，长度不对根本解不出密钥。
            <span v-if="triggerForm.encoding_aes_key">当前已填 {{ Array.from(triggerForm.encoding_aes_key.trim()).length }} 位。</span>
          </div>
        </el-form-item>

        <el-form-item label="绑定的开放 API 应用" required>
          <el-select v-model="triggerForm.open_app_id" placeholder="选择一个应用" style="width: 100%">
            <el-option v-for="app in appOptions" :key="app.value" :label="app.label" :value="app.value">
              <div class="option-row">
                <span class="option-label">{{ app.label }}</span>
                <span class="option-scopes">{{ app.scopes || '未授权任何范围' }}{{ app.enabled ? '' : ' · 已禁用' }}</span>
              </div>
            </el-option>
          </el-select>
          <div class="field-hint">
            收到指令后用这个应用现铸一枚短期令牌回放任务接口，所以它<strong>必须勾选「任务管理（tasks）」权限</strong>，
            否则指令发了也不会执行。应用在「Open API」页面创建。
          </div>
        </el-form-item>

        <el-form-item label="任务白名单">
          <el-input
            v-model="triggerForm.task_whitelist"
            type="textarea"
            :rows="3"
            placeholder="留空表示不限制。每项可写任务 ID 或任务名，用逗号或换行分隔，例如：12,签到任务"
          />
          <div class="field-hint">
            指令用哪种写法就按哪种匹配：「run 12」拿 ID 比，「运行 签到任务」拿名字比，两种都想放行就两条都写上。
          </div>
        </el-form-item>

        <el-form-item label="启用">
          <el-switch v-model="triggerForm.enabled" inline-prompt active-text="开" inactive-text="关" />
        </el-form-item>
      </el-form>

      <template #footer>
        <el-button @click="dialogVisible = false">取消</el-button>
        <el-button type="primary" :loading="submitting" :disabled="submitting" @click="submitForm">确定</el-button>
      </template>
    </el-dialog>
  </el-card>
</template>

<style scoped lang="scss">
@use './config-card-shared.scss' as *;

.prereq-list {
  margin: 6px 0 0;
  padding-left: 18px;
  font-size: 12px;
  line-height: 1.8;

  li {
    word-break: break-word;
  }
}

// 「接入配置」这一节的标题右侧要放刷新/新建两颗按钮，共享样式里的 .section-title 是块级标题，
// 这里只叠加一层 flex 布局，不改共享样式本身（其它卡片还在用）
.section-title--with-actions {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
}

.section-actions {
  display: flex;
  gap: 8px;
  flex-shrink: 0;

  :deep(.el-button + .el-button) {
    margin-left: 0;
  }
}

.trigger-list {
  // 列表为空时给点高度，免得 loading 遮罩贴成一条线
  min-height: 60px;
}

.trigger-item {
  padding: 14px 16px;
  margin-bottom: 12px;
  border: var(--dd-border-width, 1px) solid var(--dd-divider, var(--dd-border-strong));
  // 独立的信息块（四周留白、不贴边）→ surface 档
  border-radius: var(--dd-radius-surface);
  background: var(--dd-surface-2, var(--el-fill-color-blank));

  &:last-child {
    margin-bottom: 0;
  }

  &__header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 12px;
    flex-wrap: wrap;
  }

  &__title-wrap {
    display: inline-flex;
    align-items: center;
    gap: 8px;
    min-width: 0;
  }

  &__name {
    font-size: 14px;
    font-weight: 600;
    color: var(--el-text-color-primary);
    word-break: break-all;
  }

  &__actions {
    display: inline-flex;
    align-items: center;
    gap: 8px;
    flex-shrink: 0;

    :deep(.el-button + .el-button) {
      margin-left: 0;
    }
  }

  &__meta {
    display: flex;
    flex-wrap: wrap;
    gap: 6px 20px;
    margin-top: 10px;
    font-size: 12px;
    color: var(--el-text-color-regular);

    span {
      display: inline-flex;
      align-items: baseline;
      gap: 6px;
      min-width: 0;
      word-break: break-all;
    }

    em {
      font-style: normal;
      color: var(--el-text-color-secondary);
    }
  }

  &__callback {
    margin-top: 12px;

    label {
      display: block;
      font-size: 13px;
      color: var(--el-text-color-primary);
      margin-bottom: 6px;
    }
  }
}

.callback-input :deep(.el-input__inner) {
  font-family: var(--dd-font-mono);
}

// 弹窗里的逐项说明：el-form-item 自带 label，这里补的是 label 下方的解释文字
.field-hint {
  margin-top: 6px;
  font-size: 12px;
  line-height: 1.6;
  color: var(--el-text-color-secondary);
}

// 下拉选项左右分栏，替代 float: right 避免超长名称或权限范围重叠错行
.option-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  width: 100%;
  height: 100%;
  min-width: 0;
}

.option-label {
  flex: 1 1 auto;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

// 下拉里应用名右侧的权限范围，灰一号色，不和应用名抢视线
.option-scopes {
  flex: 0 1 auto;
  min-width: 0;
  max-width: 55%;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  color: var(--el-text-color-secondary);
  font-size: 12px;
}

@media (max-width: 768px) {
  .section-title--with-actions {
    flex-direction: column;
    align-items: flex-start;
  }

  .trigger-item__header {
    align-items: flex-start;
  }
}
</style>
