<script setup lang="ts">
import { ref, computed, onMounted, nextTick } from 'vue'
import { userApi } from '@/api/security'
import { useAuthStore } from '@/stores/auth'
import { ElMessage, ElMessageBox } from 'element-plus'
import { useResponsive } from '@/composables/useResponsive'
import { copyText } from '@/utils/clipboard'
import { toast } from '@/utils/toast'
import { scrollListToTop } from '@/utils/scrollToTop'
import DdBadge from '@/components/ui/DdBadge.vue'
import { formatDateTime } from '@/utils/datetime'
import { isValidNewPassword, NEW_PASSWORD_PLACEHOLDER, NEW_PASSWORD_RULE_MESSAGE } from '@/utils/password'

const authStore = useAuthStore()
const { isMobile, dialogFullscreen } = useResponsive()
const users = ref<any[]>([])
const loading = ref(false)
// 页面根：翻页回顶的锚点（scrollListToTop 从这里往上找移动端的 .layout-main、往里找桌面的 .table-card）
const pageRootRef = ref<HTMLElement | null>(null)

const keyword = ref('')
const page = ref(1)
const pageSize = ref(20)

const showCreateDialog = ref(false)
const showResetPwdDialog = ref(false)

const createForm = ref({ username: '', password: '', role: 'operator' })
const resetPwdForm = ref({ id: 0, username: '', password: '' })
// 新建用户的在途锁：请求期间锁住「创建」按钮，避免连点重复建号
const creating = ref(false)

const roleFilter = ref('')

// 只应用搜索词、不应用角色筛选的中间层。
//
// 拆出这一层是为了让「角色分段控件上的计数」和「切过去之后真正看到的条数」永远一致：
// 计数的基数必须排除角色筛选自身（否则选中「管理员」时其它三个角标全变 0），
// 但必须保留搜索词（搜「abc」时，各角色标签该告诉你「abc 在这个角色下有几个」，
// 拿全量去数就会出现「角标写 5、点进去只有 1 条」）。
const usersMatchingKeyword = computed(() => {
  const k = keyword.value.trim().toLowerCase()
  if (!k) return users.value
  return users.value.filter(u =>
    (u.username || '').toLowerCase().includes(k) ||
    (u.role || '').toLowerCase().includes(k)
  )
})

const filteredUsers = computed(() => {
  if (!roleFilter.value) return usersMatchingKeyword.value
  return usersMatchingKeyword.value.filter(u => u.role === roleFilter.value)
})

// 角色标签页的计数。
//
// 能这么算的前提：本页是【客户端筛选】——userApi.list() 一次性把全量用户拿回 users.value，
// roleFilter / keyword / 分页全在上面的 computed 里做，点标签只会走 handleSearch()（把页码归 1），
// 不会重新发请求。所以这里数出来的是真实总数，不是拿当前页冒充。
const roleCounts = computed(() => {
  const list = usersMatchingKeyword.value
  return {
    all: list.length,
    admin: list.filter(u => u.role === 'admin').length,
    operator: list.filter(u => u.role === 'operator').length,
    viewer: list.filter(u => u.role === 'viewer').length,
  }
})

const pagedUsers = computed(() => {
  const start = (page.value - 1) * pageSize.value
  return filteredUsers.value.slice(start, start + pageSize.value)
})

const total = computed(() => filteredUsers.value.length)

function handleSearch() { page.value = 1 }

// 翻页 / 改每页条数后回到列表顶部（issue #143 O3，双端都做）。
// 只挂在分页器的 current-change / size-change 上：它们只在用户翻页（或 total 变小把页码夹回）时触发，
// 搜索、切角色时直接改 page 不会触发，所以不会在筛选时把人拽回顶部。
// 本页是前端分页，数据早已在内存里，没有「请求回来」的时机可等；
// 等 nextTick 让新一页先渲染出来再滚，免得先回顶、后换内容，位置又被新内容的高度顶偏。
async function handlePageChange() {
  await nextTick()
  scrollListToTop(pageRootRef.value)
}

async function handlePageSizeChange() {
  // 改每页条数后从第 1 页看起：原页码在新页大小下对应的是另一批数据，停在中间没有意义
  page.value = 1
  await nextTick()
  scrollListToTop(pageRootRef.value)
}

async function loadUsers() {
  loading.value = true
  try {
    const res = await userApi.list()
    users.value = res.data || []
  } catch (err: any) {
    ElMessage.error(err?.response?.data?.error || '加载用户列表失败')
  } finally {
    loading.value = false
  }
}

onMounted(loadUsers)

function openCreate() {
  createForm.value = { username: '', password: '', role: 'operator' }
  showCreateDialog.value = true
}

// 新建用户、重置密码都是设置新密码，与后端同一规则
function validatePassword(pwd: string): string | null {
  if (!isValidNewPassword(pwd)) return NEW_PASSWORD_RULE_MESSAGE
  return null
}

async function handleCreate() {
  const username = createForm.value.username.trim()
  if (!username) {
    ElMessage.warning('用户名不能为空')
    return
  }
  if (!/^[\p{L}\p{N}_]{1,32}$/u.test(username)) {
    ElMessage.warning('用户名需 1-32 位，支持中文、字母、数字和下划线')
    return
  }
  const pwdErr = validatePassword(createForm.value.password)
  if (pwdErr) {
    ElMessage.warning(pwdErr)
    return
  }
  // 用户名与密码都校验通过后才置位，复位放 finally
  creating.value = true
  try {
    await userApi.create({ ...createForm.value, username })
    ElMessage.success('创建成功')
    showCreateDialog.value = false
    loadUsers()
  } catch (err: any) {
    ElMessage.error(err?.response?.data?.error || '创建失败')
  } finally {
    creating.value = false
  }
}

async function handleToggle(row: any) {
  try {
    const enabling = !row.enabled
    await ElMessageBox.confirm(
      enabling
        ? `确认启用用户 ${row.username} 吗？`
        : `确认禁用用户 ${row.username} 吗？禁用后该账号将无法继续登录。`,
      enabling ? '启用确认' : '禁用确认',
      { type: enabling ? 'info' : 'warning' }
    )
    await userApi.update(row.id, { enabled: !row.enabled })
    ElMessage.success(row.enabled ? '已禁用' : '已启用')
    loadUsers()
  } catch (err: any) {
    if (err === 'cancel' || err?.toString?.() === 'cancel') return
    ElMessage.error(err?.response?.data?.error || '操作失败')
  }
}

async function handleRoleChange(row: any, role: string) {
  const originalRole = row.role
  if (role === originalRole) return
  const roleName = getRoleName(role)
  try {
    const tips = originalRole === 'admin' && role !== 'admin'
      ? `确认将用户 ${row.username} 从「管理员」降级为「${roleName}」吗？此操作将立即剥夺该用户的管理员权限。`
      : role === 'admin'
        ? `确认将用户 ${row.username} 提升为「管理员」吗？管理员将拥有全部权限。`
        : `确认将用户 ${row.username} 的角色改为「${roleName}」吗？`
    await ElMessageBox.confirm(tips, '角色变更', { type: 'warning' })
    await userApi.update(row.id, { role })
    ElMessage.success('角色更新成功')
    loadUsers()
  } catch (err: any) {
    if (err === 'cancel' || err?.toString?.() === 'cancel') {
      // 回滚 UI
      loadUsers()
      return
    }
    ElMessage.error(err?.response?.data?.error || '更新失败')
    loadUsers()
  }
}

async function handleDelete(row: any) {
  try {
    await ElMessageBox.confirm(`确定要删除用户 ${row.username} 吗？`, '确认删除', { type: 'warning' })
    await userApi.delete(row.id)
    ElMessage.success('删除成功')
    loadUsers()
  } catch (err: any) {
    if (err === 'cancel' || err?.toString?.() === 'cancel') return
    ElMessage.error(err?.response?.data?.error || '删除失败')
  }
}

function openResetPassword(row: any) {
  resetPwdForm.value = { id: row.id, username: row.username, password: '' }
  showResetPwdDialog.value = true
}

async function handleResetPassword() {
  const pwdErr = validatePassword(resetPwdForm.value.password)
  if (pwdErr) {
    ElMessage.warning(pwdErr)
    return
  }
  // 弹窗一关，这个新密码在界面上就再也找不到了（下次打开弹窗会被清空，
  // 服务端也只存哈希、查不回来）。管理员重置完的下一步必然是把它发给本人，
  // 所以这里先把值抓进闭包，再在提示里挂一个「复制密码」出口，省掉「忘了记 → 再重置一次」。
  const newPassword = resetPwdForm.value.password
  const targetUser = resetPwdForm.value.username
  try {
    await userApi.resetPassword(resetPwdForm.value.id, newPassword)
    showResetPwdDialog.value = false
    toast.success(`已重置「${targetUser}」的密码`, {
      action: {
        text: '复制密码',
        handler: async () => {
          try {
            await copyText(newPassword)
            ElMessage.success('新密码已复制到剪贴板')
          } catch {
            ElMessage.error('复制失败，请手动记录新密码')
          }
        },
      },
    })
  } catch (err: any) {
    ElMessage.error(err?.response?.data?.error || '重置失败')
  }
}

function getRoleTag(role: string) {
  switch (role) {
    case 'admin': return 'danger'
    case 'operator': return ''
    case 'viewer': return 'info'
    default: return 'info'
  }
}

function getRoleName(role: string) {
  switch (role) {
    case 'admin': return '管理员'
    case 'operator': return '操作员'
    case 'viewer': return '观察者'
    default: return role
  }
}
</script>

<template>
  <div ref="pageRootRef" class="users-page dd-fixed-page dd-page-hide-heading">
    <div class="page-header">
      <div>
        <h2 class="page-title-with-icon"><el-icon><User /></el-icon><span>用户管理</span></h2>
        <p class="page-subtitle">管理系统内所有用户的权限和角色，控制用户对系统各功能的访问权限</p>
      </div>
      <div class="header-actions">
        <el-button type="primary" @click="openCreate">
          <el-icon><Plus /></el-icon> 新建用户
        </el-button>
      </div>
    </div>

    <div class="toolbar">
      <div class="toolbar__left">
        <!-- 角色分段控件带计数：不用切过去也知道每个角色下有几个人。
             计数是「统计型」而非「提醒型」，所以开 show-zero ——「观察者 0」是有信息量的
             （告诉你确实一个都没有），角标直接消失反而像是没加载出来。
             level 用 info（描边不实心）：它只是中性计数，不该和真正需要处理的红色角标抢注意力。 -->
        <div class="status-tabs">
          <button :class="['status-tab', { active: roleFilter === '' }]" @click="roleFilter = ''; handleSearch()">
            <span>全部</span>
            <DdBadge :value="roleCounts.all" level="info" show-zero title="全部用户" />
          </button>
          <button :class="['status-tab', { active: roleFilter === 'admin' }]" @click="roleFilter = 'admin'; handleSearch()">
            <span>管理员</span>
            <DdBadge :value="roleCounts.admin" level="info" show-zero title="管理员" />
          </button>
          <button :class="['status-tab', { active: roleFilter === 'operator' }]" @click="roleFilter = 'operator'; handleSearch()">
            <span>操作员</span>
            <DdBadge :value="roleCounts.operator" level="info" show-zero title="操作员" />
          </button>
          <button :class="['status-tab', { active: roleFilter === 'viewer' }]" @click="roleFilter = 'viewer'; handleSearch()">
            <span>观察者</span>
            <DdBadge :value="roleCounts.viewer" level="info" show-zero title="观察者" />
          </button>
        </div>
        <el-input v-model="keyword" placeholder="搜索用户名/角色" clearable class="toolbar__search" @input="handleSearch">
          <template #prefix><el-icon><Search /></el-icon></template>
        </el-input>
      </div>
      <div class="toolbar__right">
        <el-button type="primary" @click="openCreate">
          <el-icon><Plus /></el-icon> 新建用户
        </el-button>
      </div>
    </div>

    <div v-if="isMobile" class="dd-mobile-list">
      <div v-for="row in pagedUsers" :key="row.id" class="dd-mobile-card">
        <div class="dd-mobile-card__header">
          <div class="dd-mobile-card__title-wrap">
            <span class="dd-mobile-card__title">{{ row.username }}</span>
            <div class="dd-mobile-card__badges">
              <!-- 改完角色会重新拉列表、这枚标签跟着翻。硬切时用户不确定「是我改成功了，还是我看错了」，
                   out-in 让旧角色先淡出、新角色再淡入，把「生效了」这件事显式演出来。
                   key 必须绑【角色值】：绑 row.id 的话同一行永远是同一个 key，节点原地复用，过渡一次都不会触发。 -->
              <span class="role-tag-slot">
                <Transition name="dd-status-switch" mode="out-in">
                  <el-tag :key="row.role" size="small" :type="getRoleTag(row.role)">{{ getRoleName(row.role) }}</el-tag>
                </Transition>
              </span>
              <el-tag v-if="row.two_factor_enabled" size="small" type="success" effect="plain">2FA</el-tag>
            </div>
          </div>
          <el-switch :model-value="row.enabled" size="small" :disabled="row.username === authStore.user?.username" @change="handleToggle(row)" />
        </div>
        <div class="dd-mobile-card__body">
          <div class="dd-mobile-card__grid">
            <div class="dd-mobile-card__field">
              <span class="dd-mobile-card__label">角色</span>
              <div class="dd-mobile-card__value">
                <el-select :model-value="row.role" size="small" :disabled="row.username === authStore.user?.username" @change="(val: string) => handleRoleChange(row, val)">
                  <el-option value="admin" label="管理员" />
                  <el-option value="operator" label="操作员" />
                  <el-option value="viewer" label="观察者" />
                </el-select>
              </div>
            </div>
            <div class="dd-mobile-card__field">
              <span class="dd-mobile-card__label">最后登录</span>
              <span class="dd-mobile-card__value">{{ formatDateTime(row.last_login_at) }}</span>
            </div>
            <div class="dd-mobile-card__field">
              <span class="dd-mobile-card__label">创建时间</span>
              <span class="dd-mobile-card__value">{{ formatDateTime(row.created_at) }}</span>
            </div>
          </div>
          <div class="dd-mobile-card__actions user-card__actions">
            <el-button size="small" type="primary" plain @click="openResetPassword(row)">重置密码</el-button>
            <el-button size="small" type="danger" plain :disabled="row.username === authStore.user?.username" @click="handleDelete(row)">删除</el-button>
          </div>
        </div>
      </div>
      <el-empty v-if="!loading && pagedUsers.length === 0" description="暂无用户" />
    </div>

    <div v-else class="table-card">
      <el-table :data="pagedUsers" v-loading="loading" style="width: 100%" :header-cell-style="{ background: 'var(--el-fill-color-light)', color: 'var(--el-text-color-regular)', fontWeight: 600, fontSize: '13px' }">
        <el-table-column prop="id" label="ID" width="60" />
        <el-table-column prop="username" label="用户名" min-width="150">
          <template #default="{ row }">
            <div class="user-name-cell">
              <div class="user-avatar">{{ (row.username || '?')[0].toUpperCase() }}</div>
              <div class="user-name-info">
                <span class="user-name-text">{{ row.username }}</span>
                <!-- 同上：角色改完后由 loadUsers() 刷新，这里 out-in 一次淡出淡入。
                     只做 opacity，不做位移——表格行里任何位移都会连带整行抖。
                     外层 .role-tag-slot 撑住最小高度，out-in 中间那一帧标签被移除时不塌。 -->
                <span class="role-tag-slot">
                  <Transition name="dd-status-switch" mode="out-in">
                    <el-tag :key="row.role" size="small" :type="getRoleTag(row.role)" round>{{ getRoleName(row.role) }}</el-tag>
                  </Transition>
                </span>
              </div>
            </div>
          </template>
        </el-table-column>
        <el-table-column label="角色" width="140">
          <template #default="{ row }">
            <el-select :model-value="row.role" size="small" :disabled="row.username === authStore.user?.username" @change="(val: string) => handleRoleChange(row, val)">
              <el-option value="admin" label="管理员" />
              <el-option value="operator" label="操作员" />
              <el-option value="viewer" label="观察者" />
            </el-select>
          </template>
        </el-table-column>
        <el-table-column label="2FA" width="80" align="center">
          <template #default="{ row }">
            <el-tag v-if="row.two_factor_enabled" size="small" type="success" effect="plain" round>已启用</el-tag>
            <span v-else class="text-secondary">-</span>
          </template>
        </el-table-column>
        <el-table-column label="状态" width="80" align="center">
          <template #default="{ row }">
            <el-switch :model-value="row.enabled" size="small" :disabled="row.username === authStore.user?.username" @change="handleToggle(row)" />
          </template>
        </el-table-column>
        <el-table-column label="最后登录" width="170">
          <template #default="{ row }">
            <span v-if="row.last_login_at" class="time-text">{{ formatDateTime(row.last_login_at) }}</span>
            <span v-else class="text-secondary">-</span>
          </template>
        </el-table-column>
        <el-table-column label="创建时间" width="170">
          <template #default="{ row }">
            <span class="time-text">{{ formatDateTime(row.created_at) }}</span>
          </template>
        </el-table-column>
        <el-table-column label="操作" width="160" fixed="right" align="center">
          <template #default="{ row }">
            <div class="action-btns">
              <el-button size="small" text type="primary" @click="openResetPassword(row)">重置密码</el-button>
              <el-button size="small" text type="danger" :disabled="row.username === authStore.user?.username" @click="handleDelete(row)">删除</el-button>
            </div>
          </template>
        </el-table-column>
      </el-table>
    </div>

    <div class="pagination-bar">
      <span class="pagination-total">共 {{ total }} 条数据</span>
      <el-pagination
        v-model:current-page="page"
        v-model:page-size="pageSize"
        :total="total"
        :page-sizes="[10, 20, 50, 100]"
        layout="sizes, prev, pager, next"
        @current-change="handlePageChange"
        @size-change="handlePageSizeChange"
      />
    </div>

    <el-dialog v-model="showCreateDialog" title="新建用户" width="400px" :fullscreen="dialogFullscreen">
      <el-form :model="createForm" :label-width="dialogFullscreen ? 'auto' : '80px'" :label-position="dialogFullscreen ? 'top' : 'right'">
        <el-form-item label="用户名">
          <el-input v-model="createForm.username" placeholder="3-32 位字母/数字/下划线" />
        </el-form-item>
        <el-form-item label="密码">
          <el-input v-model="createForm.password" type="password" show-password :placeholder="NEW_PASSWORD_PLACEHOLDER" />
        </el-form-item>
        <el-form-item label="角色">
          <el-radio-group v-model="createForm.role">
            <el-radio value="admin">管理员</el-radio>
            <el-radio value="operator">操作员</el-radio>
            <el-radio value="viewer">观察者</el-radio>
          </el-radio-group>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="showCreateDialog = false">取消</el-button>
        <el-button type="primary" :loading="creating" :disabled="creating" @click="handleCreate">创建</el-button>
      </template>
    </el-dialog>

    <el-dialog v-model="showResetPwdDialog" title="重置密码" width="400px" :fullscreen="dialogFullscreen">
      <el-form :model="resetPwdForm" :label-width="dialogFullscreen ? 'auto' : '80px'" :label-position="dialogFullscreen ? 'top' : 'right'">
        <el-form-item label="用户">
          <el-input :model-value="resetPwdForm.username" disabled />
        </el-form-item>
        <el-form-item label="新密码">
          <el-input v-model="resetPwdForm.password" type="password" show-password :placeholder="NEW_PASSWORD_PLACEHOLDER" />
        </el-form-item>
      </el-form>
      <!-- 重置密码会撤销该用户所有登录并清掉他的通行密钥（后端行为），提前告诉管理员 -->
      <div class="reset-pwd-hint">重置后该用户需要重新登录，该用户的通行密钥会一并清除。</div>
      <template #footer>
        <el-button @click="showResetPwdDialog = false">取消</el-button>
        <el-button type="primary" @click="handleResetPassword">确定</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<style scoped lang="scss">
.users-page { padding: 0; }

.reset-pwd-hint {
  margin-top: 4px;
  font-size: 12px;
  line-height: 1.6;
  color: var(--el-text-color-secondary);
}

.page-header {
  display: flex;
  justify-content: space-between;
  align-items: flex-start;
  margin-bottom: 18px;
  gap: 16px;

  h2 { margin: 0; font-size: 22px; font-weight: 700; color: var(--el-text-color-primary); line-height: 1.3; }
  .page-subtitle { font-size: 13px; color: var(--el-text-color-secondary); margin: 6px 0 0; line-height: 1.6; max-width: 720px; }
  .page-title-with-icon { display: inline-flex; align-items: center; gap: 8px; }
  .page-title-with-icon :deep(.el-icon) { color: var(--el-color-primary); }
  .header-actions { display: flex; gap: 10px; flex-shrink: 0; }
}

// 工具条：与定时任务页/订阅管理页对齐——上下统一间距、左右两区一行排布、gap 一致
.toolbar {
  display: flex; justify-content: space-between; align-items: center; margin: 14px 0; gap: 12px; flex-wrap: wrap;
  &__left { display: flex; align-items: center; gap: 12px; flex-wrap: wrap; flex: 1; min-width: 0; }
  &__right { display: flex; align-items: center; gap: 10px; }
  &__search { width: 260px; }
}

// 角色分段控件：与定时任务页/订阅管理页一致的分段容器；选中态靠底色+品牌色文字区分，不再用阴影浮起
.status-tabs {
  // 分段控件的灰底槽属控件类表面 → control 档（与槽内的项同档，两者一致才不会露出内外错位的角）
  display: inline-flex; background: var(--el-fill-color-light); border-radius: var(--dd-radius-control); padding: 3px; gap: 2px;
}

.status-tab {
  // inline-flex + gap：标签文字与计数角标并排，间距交给 gap，不用 margin 拼。
  // 角标高 18px，与 13px 文字的行盒高度基本齐平，加上去不会把分段控件顶高。
  display: inline-flex; align-items: center; gap: 6px;
  // 分段项属控件类表面 → control 档
  padding: 6px 14px; border-radius: var(--dd-radius-control); border: none; background: transparent;
  color: var(--el-text-color-secondary); font-size: 13px; font-weight: 500; cursor: pointer;
  transition:
    color var(--dd-motion-fast) var(--dd-ease-standard),
    background-color var(--dd-motion-fast) var(--dd-ease-standard);
  white-space: nowrap;
  &:hover { color: var(--el-text-color-primary); }
  &.active { background: var(--el-bg-color); color: var(--el-color-primary); font-weight: 600; }
}

// 表格卡：1px 边框划分层次，不再用阴影浮起（dd-fixed-page 下的 flex + 内部滚动由全局规则接管）
// 表格容器属容器类表面 → surface 档；overflow:hidden 让内部贴边的表头/行自动被圆角裁角
.table-card {
  background: var(--el-bg-color); border-radius: var(--dd-radius-surface);
  border: 1px solid var(--el-border-color-lighter); overflow: hidden;
}

.user-name-cell { display: flex; align-items: center; gap: 12px; }
.user-avatar {
  // 用户头像：形状承载语义（圆形=头像/身份标识），两种圆角模式下都固定正圆，不吃 --dd-radius-* 令牌
  width: 36px; height: 36px; border-radius: 50%;
  // 底色保持品牌纯色（原为渐变，扁平化时已去掉，此处不恢复）
  background: var(--el-color-primary);
  color: #fff; display: flex; align-items: center; justify-content: center;
  font-weight: 600; font-size: 14px; flex-shrink: 0;
}
.user-name-info { display: flex; align-items: center; gap: 8px; }
.user-name-text { font-weight: 500; color: var(--el-text-color-primary); }

// 角色标签的占位槽：out-in 过渡中间有一帧「旧的已移除、新的还没进来」，
// 没有这个最小高度的话，移动端卡片里那一行会瞬间塌一次高度。
// 24px = el-tag small 的高度。
.role-tag-slot {
  display: inline-flex;
  align-items: center;
  min-height: 24px;
}

// 角色标签切换：只做透明度，禁止位移/缩放——表格行与移动卡里任何位移都会带动整行。
// 时长与缓动走令牌，prefers-reduced-motion 时令牌自动降为 1ms 即等效关闭。
.dd-status-switch-enter-active,
.dd-status-switch-leave-active {
  transition: opacity var(--dd-motion-fast) var(--dd-ease-standard);
}

.dd-status-switch-enter-from,
.dd-status-switch-leave-to {
  opacity: 0;
}
.text-secondary { color: var(--el-text-color-secondary); }
.time-text { font-family: var(--dd-font-mono); font-size: 12px; color: var(--el-text-color-regular); }
.action-btns {
  display: flex; align-items: center; justify-content: center; gap: 2px;

  // EP 自带 `.el-button + .el-button { margin-left: 12px }` 会叠加在上面的 gap 上，
  // 实际间距变成 14px 而不是设计的 2px。间距统一交给 gap
  // （与 tasks / deps / subscriptions 三页一致）。
  :deep(.el-button + .el-button) { margin-left: 0; }
}
.user-card__actions > * { flex: 1 1 calc(50% - 4px); }

// 分页条：与定时任务页/订阅管理页一致的间距收敛
.pagination-bar {
  margin-top: 14px; display: flex; justify-content: space-between; align-items: center; padding: 0 4px;
}
.pagination-total { font-size: 13px; color: var(--el-text-color-secondary); }

:deep(.el-table) {
  // 边框统一走令牌，明暗自动适配（原写死浅灰会在暗色串色）
  --el-table-border-color: var(--el-border-color-lighter);
  .el-table__header-wrapper th { border-bottom: 1px solid var(--el-border-color-light); }
  .el-table__row td { border-bottom: 1px solid var(--el-border-color-lighter); }
  .el-table__cell { padding: 12px 0; }
}

@media (max-width: 768px) {
  .page-header { flex-direction: column; gap: 10px; margin-bottom: 14px; h2 { font-size: 18px; } }
  .toolbar { flex-direction: column; align-items: stretch; gap: 10px;
    &__left { flex-direction: column; gap: 10px; }
    &__right { justify-content: flex-end; }
    &__search { width: 100% !important; }
  }
  .status-tabs { width: 100%; overflow-x: auto; }
}

// ===== 入场动画 =====
// 与定时任务页/订阅管理页统一：只对卡片级容器（工具条 / 表格卡 / 移动列表）做克制的淡入上移 + 轻微错落；
// 不给表格每一行或每张移动卡做 stagger。时长走令牌，prefers-reduced-motion 时令牌自动降为 1ms 即等效关闭。
@keyframes dd-users-rise-in {
  from {
    opacity: 0;
    transform: translateY(12px);
  }
  to {
    opacity: 1;
    transform: translateY(0);
  }
}

.toolbar,
.table-card,
.dd-mobile-list {
  animation: dd-users-rise-in var(--dd-motion-page) var(--dd-ease-decelerate) both;
}

// 轻微错落：工具条先入，表格卡/移动列表略晚
.table-card,
.dd-mobile-list {
  animation-delay: 60ms;
}
</style>
