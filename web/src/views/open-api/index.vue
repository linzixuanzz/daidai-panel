<template>
  <div ref="pageRootRef" class="open-api-page dd-scroll-page dd-page-hide-heading">
    <div class="page-header">
      <div>
        <h2 class="page-title-with-icon"><el-icon><Key /></el-icon><span>Open API 管理</span></h2>
        <p class="page-subtitle">
          创建和管理外部 API 调用应用密钥，控制接口访问权限和速率。
        </p>
      </div>
      <div class="header-actions">
        <el-button type="primary" @click="showCreateDialog">
          <el-icon><Plus /></el-icon> 创建令牌
        </el-button>
      </div>
    </div>

    <div class="toolbar">
      <div class="toolbar__left">
        <!-- 状态分段控件带计数：不用切过去也知道各状态下有几个应用。
             计数是「统计型」而非「提醒型」，所以开 show-zero ——「已禁用 0」是有信息量的
             （告诉你确实一个都没有），角标直接消失反而像是没加载出来。
             level 用 info（描边不实心）：中性计数，不该和真正需要处理的红色角标抢注意力。 -->
        <div class="status-tabs">
          <button
            :class="['status-tab', { active: statusFilter === '' }]"
            @click="statusFilter = ''"
          >
            <span>全部</span>
            <DdBadge
              :value="statusCounts.all"
              level="info"
              show-zero
              title="全部应用"
            />
          </button>
          <button
            :class="['status-tab', { active: statusFilter === 'enabled' }]"
            @click="statusFilter = 'enabled'"
          >
            <span>已启用</span>
            <DdBadge
              :value="statusCounts.enabled"
              level="info"
              show-zero
              title="已启用应用"
            />
          </button>
          <button
            :class="['status-tab', { active: statusFilter === 'disabled' }]"
            @click="statusFilter = 'disabled'"
          >
            <span>已禁用</span>
            <DdBadge
              :value="statusCounts.disabled"
              level="info"
              show-zero
              title="已禁用应用"
            />
          </button>
        </div>
        <el-input
          v-model="searchKeyword"
          placeholder="搜索应用名称或 Key"
          clearable
          class="toolbar__search"
          @keyup.enter="handleSearch"
          @clear="handleSearch"
        >
          <template #prefix
            ><el-icon><Search /></el-icon
          ></template>
        </el-input>
      </div>
      <div class="toolbar__right">
        <el-button type="primary" @click="showCreateDialog">
          <el-icon><Plus /></el-icon> 创建令牌
        </el-button>
        <!-- 作废是全局的、不可逐条撤销，所以不放进某个应用的行内菜单，而是单独摆在工具条上 -->
        <el-button
          type="danger"
          plain
          :loading="revokingTriggerLinks"
          title="作废全部企业微信触发链接（不影响 App Key / App Secret）"
          @click="revokeTriggerLinks"
        >
          作废触发链接
        </el-button>
        <el-button @click="loadApps" title="刷新列表">
          <el-icon><Refresh /></el-icon>
        </el-button>
      </div>
    </div>

    <div v-if="isMobile" class="dd-mobile-list">
      <div v-for="row in pagedApps" :key="row.id" class="dd-mobile-card">
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
                    <code class="key-code secret-code">{{
                      revealedSecrets[row.id]
                    }}</code>
                    <el-button
                      class="copy-btn"
                      size="small"
                      @click="copyText(revealedSecrets[row.id] || '')"
                    >
                      <el-icon><DocumentCopy /></el-icon>
                    </el-button>
                    <el-button
                      class="copy-btn"
                      size="small"
                      @click="hideSecret(row.id)"
                    >
                      <el-icon><Hide /></el-icon>
                    </el-button>
                  </div>
                </template>
                <template v-else>
                  <div class="key-display">
                    <span class="secret-mask">••••••••••••••••</span>
                    <el-button
                      type="primary"
                      link
                      size="small"
                      @click="viewSecret(row)"
                    >
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
                  >{{ s }}</el-tag
                >
                <span
                  v-if="!row.scopes"
                  style="color: var(--el-text-color-secondary)"
                  >未授权任何范围</span
                >
              </div>
            </div>
            <div class="dd-mobile-card__field">
              <span class="dd-mobile-card__label">速率限制</span>
              <span class="dd-mobile-card__value">{{
                formatRateLimit(row.rate_limit)
              }}</span>
            </div>
            <div class="dd-mobile-card__field">
              <span class="dd-mobile-card__label">今日调用</span>
              <span class="dd-mobile-card__value">{{ row.call_count }}</span>
            </div>
          </div>

          <div class="dd-mobile-card__actions open-api-card__actions">
            <el-button size="small" type="primary" plain @click="editApp(row)"
              >编辑</el-button
            >
            <el-button
              size="small"
              type="warning"
              plain
              @click="resetSecret(row)"
              >重置密钥</el-button
            >
            <el-button size="small" @click="showLogs(row)">日志</el-button>
            <!-- 企业微信菜单触发链接（issue #145）：移动端也要能生成，
                 因为配企业微信后台这件事本身经常是在手机上做的 -->
            <el-button size="small" @click="openTriggerLinkDialog(row)"
              >触发链接</el-button
            >
            <el-button size="small" type="danger" plain @click="deleteApp(row)"
              >删除</el-button
            >
          </div>
        </div>
      </div>

      <el-empty
        v-if="!loading && filteredApps.length === 0"
        description="暂无应用"
      />
    </div>

    <div v-else class="table-card">
      <el-table
        :data="pagedApps"
        v-loading="loading"
        style="width: 100%"
        :header-cell-style="{
          background: 'var(--el-fill-color-light)',
          color: 'var(--el-text-color-regular)',
          fontWeight: 600,
          fontSize: '13px',
        }"
      >
        <el-table-column prop="name" label="应用名称" min-width="180">
          <template #default="{ row }">
            <div class="app-name-cell">
              <span
                class="app-avatar"
                :style="{ background: getAvatarColor(row.name) }"
                >{{ getInitial(row.name) }}</span
              >
              <div class="app-name-info">
                <span class="app-name-text">{{ row.name }}</span>
                <span class="app-name-desc">{{
                  row.scopes
                    ? parseScopesTags(row.scopes).join("、")
                    : "暂无权限"
                }}</span>
              </div>
            </div>
          </template>
        </el-table-column>
        <el-table-column label="API Key" min-width="220">
          <template #default="{ row }">
            <div class="key-display">
              <code class="key-code">{{ maskKey(row.app_key) }}</code>
              <el-button
                class="copy-btn"
                size="small"
                @click="copyText(row.app_key)"
              >
                <el-icon><DocumentCopy /></el-icon>
              </el-button>
            </div>
          </template>
        </el-table-column>
        <el-table-column label="App Secret" min-width="240">
          <template #default="{ row }">
            <div v-if="revealedSecrets[row.id]" class="key-display">
              <code class="key-code secret-code">{{
                revealedSecrets[row.id]
              }}</code>
              <el-button
                class="copy-btn"
                size="small"
                @click="copyText(revealedSecrets[row.id] || '')"
              >
                <el-icon><DocumentCopy /></el-icon>
              </el-button>
              <el-button
                class="copy-btn"
                size="small"
                @click="hideSecret(row.id)"
              >
                <el-icon><Hide /></el-icon>
              </el-button>
            </div>
            <div v-else class="key-display">
              <span class="secret-mask">••••••••••••••••</span>
              <el-button
                type="primary"
                link
                size="small"
                @click="viewSecret(row)"
              >
                <el-icon><View /></el-icon>查看
              </el-button>
            </div>
          </template>
        </el-table-column>
        <el-table-column label="权限" min-width="200">
          <template #default="{ row }">
            <el-tag
              v-for="s in parseScopesTags(row.scopes)"
              :key="s"
              size="small"
              class="scope-tag"
              >{{ s }}</el-tag
            >
            <span
              v-if="!row.scopes"
              style="color: var(--el-text-color-secondary)"
              >未授权</span
            >
          </template>
        </el-table-column>
        <el-table-column
          prop="call_count"
          label="今日调用"
          width="90"
          align="center"
        />
        <el-table-column label="状态" width="80" align="center">
          <template #default="{ row }">
            <el-switch
              :model-value="row.enabled"
              @change="(val: boolean) => toggleEnabled(row, val)"
              size="small"
            />
          </template>
        </el-table-column>
        <el-table-column label="创建时间" width="170">
          <template #default="{ row }">
            <span class="time-text">{{
              row.created_at
                ? formatDateTime(row.created_at)
                : "-"
            }}</span>
          </template>
        </el-table-column>
        <!-- 原来是 4 个 link 按钮平铺、没有容器，间距全靠 EP 自带的
             `.el-button + .el-button { margin-left: 12px }`，占掉 180px 列宽，
             而且「删除」和「编辑」并排、字号一样大，误点的代价却完全不同。
             改成 Split Button：主体是最常用的「编辑」，其余收进菜单，
             删除标红并用分隔线隔开。列宽 180 → 130。 -->
        <!-- fixed="right"：这张表列多（应用名/Key/Secret/权限/调用数/状态/创建时间），
             1249px 视口下实测横向溢出 323px，不固定的话操作列整个被挤出可视区，
             用户得先横向滚动才点得到。收窄列宽只是把溢出从 373 减到 323，治不了根。 -->
        <el-table-column label="操作" width="130" align="center" fixed="right">
          <template #default="{ row }">
            <DdSplitButton
              label="编辑"
              type="primary"
              size="small"
              :items="appActionItems"
              @click="editApp(row)"
              @command="(key: string) => onAppAction(key, row)"
            />
          </template>
        </el-table-column>
      </el-table>
    </div>

    <div class="pagination-bar">
      <span class="pagination-total">共 {{ filteredApps.length }} 条</span>
      <el-pagination
        v-if="filteredApps.length > apiPageSize"
        v-model:current-page="apiPage"
        :total="filteredApps.length"
        :page-size="apiPageSize"
        layout="prev, pager, next"
        small
        @current-change="handleApiPageChange"
      />
    </div>

    <div class="info-cards">
      <div class="info-card">
        <div class="info-card__header">
          <div class="info-card__icon info-card__icon--blue">
            <el-icon :size="20"><Lock /></el-icon>
          </div>
          <h3 class="info-card__title">安全建议</h3>
        </div>
        <ul class="info-card__list">
          <li>定期轮换 API 密钥，建议每 90 天更换一次</li>
          <li>使用最小权限原则，仅授权必要的接口范围</li>
          <li>配置合理的速率限制，防止接口被滥用</li>
          <li>不要在客户端代码中暴露 App Secret</li>
        </ul>
      </div>
      <div class="info-card">
        <div class="info-card__header">
          <div class="info-card__icon info-card__icon--green">
            <el-icon :size="20"><Connection /></el-icon>
          </div>
          <h3 class="info-card__title">开发者文档</h3>
        </div>
        <ul class="info-card__list">
          <li>使用 App Key + App Secret 生成签名进行身份验证</li>
          <li>所有 API 请求需在 Header 中携带认证信息</li>
          <li>接口返回标准 JSON 格式，包含 code 和 data 字段</li>
          <li>超过速率限制将返回 429 状态码，请合理控制频率</li>
        </ul>
      </div>
    </div>

    <el-dialog
      v-model="dialogVisible"
      :title="editingApp ? '编辑应用' : '新建应用'"
      width="500px"
      :fullscreen="dialogFullscreen"
    >
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
            <el-option
              v-for="s in scopeOptions"
              :key="s.value"
              :label="s.label"
              :value="s.value"
            />
          </el-select>
          <div
            style="
              margin-top: 6px;
              color: var(--el-text-color-secondary);
              font-size: 12px;
            "
          >
            默认拒绝。留空表示该应用创建成功，但没有任何接口访问权限。
          </div>
          <!-- 上传 / 恢复 / 删除备份会覆盖或销毁数据，服务端只放行管理员账号本人，这里提前说清楚免得以为勾了就能用 -->
          <div
            v-if="form.scopesList.includes('backup')"
            style="
              margin-top: 4px;
              color: var(--el-text-color-secondary);
              font-size: 12px;
            "
          >
            「系统备份」权限可创建、列出、下载备份；上传、恢复、删除备份需管理员账号本人操作。
          </div>
        </el-form-item>
        <el-form-item label="速率限制">
          <el-input-number v-model="form.rate_limit" :min="0" :max="10000" />
          <span style="margin-left: 8px; color: var(--el-text-color-secondary)"
            >次/小时，填 0 表示无限制</span
          >
          <div
            style="
              margin-top: 6px;
              color: var(--el-text-color-secondary);
              font-size: 12px;
            "
          >
            调用次数按当天统计展示，每日会重新从 0 开始累计。
          </div>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="dialogVisible = false">取消</el-button>
        <el-button
          type="primary"
          :loading="submitting"
          :disabled="submitting"
          @click="submitForm"
          >确定</el-button
        >
      </template>
    </el-dialog>

    <el-dialog
      v-model="secretDialogVisible"
      title="应用密钥"
      width="560px"
      :fullscreen="dialogFullscreen"
      :close-on-click-modal="false"
    >
      <el-alert type="warning" :closable="false" style="margin-bottom: 16px">
        请妥善保管密钥，关闭后需要验证密码才能再次查看 App Secret。
      </el-alert>
      <div class="secret-display-card">
        <div class="secret-row">
          <span class="secret-label">App Key</span>
          <div class="secret-value-box">
            <code class="secret-value-text">{{ secretData.app_key }}</code>
            <el-button
              class="copy-btn"
              size="small"
              @click="copyText(secretData.app_key)"
            >
              <el-icon><DocumentCopy /></el-icon> 复制
            </el-button>
          </div>
        </div>
        <div class="secret-row">
          <span class="secret-label">App Secret</span>
          <div class="secret-value-box">
            <code class="secret-value-text">{{ secretData.app_secret }}</code>
            <el-button
              class="copy-btn"
              size="small"
              @click="copyText(secretData.app_secret)"
            >
              <el-icon><DocumentCopy /></el-icon> 复制
            </el-button>
          </div>
        </div>
      </div>
      <!-- 移动端全屏时右上角 × 被全局规则隐藏（F2），关闭入口放到右下角；
           本弹窗 close-on-click-modal=false，点遮罩也关不掉，这颗按钮是移动端唯一的出口。
           桌面不传 footer 插槽，EP 不渲染 footer，保持原样。 -->
      <template v-if="dialogFullscreen" #footer>
        <el-button @click="secretDialogVisible = false">关闭</el-button>
      </template>
    </el-dialog>

    <!-- 生成企业微信触发链接（issue #145 方案 B）。
         链接是「点一下就执行某个任务」的长期地址，配到企业微信自建应用的菜单里用。 -->
    <el-dialog
      v-model="triggerLinkDialogVisible"
      title="生成企业微信触发链接"
      width="600px"
      :fullscreen="dialogFullscreen"
    >
      <el-alert type="warning" :closable="false" show-icon style="margin-bottom: 16px">
        <template #title>这条链接长期有效，且打开时不需要登录</template>
        <div class="trigger-link-notice">
          等同于这一个任务的永久触发权：只应配置到企业微信应用菜单里，不要贴进聊天、工单或文档——
          它会留在企业微信服务器、内置浏览器历史与反向代理的访问日志里。需要止血时用工具条上的「作废触发链接」。
          <!-- v3.4.4（审计 M-1）起打开链接时会重新核对签发应用与任务的状态，所以这里明说这几种操作也能让链接失效 -->
          禁用 / 删除签发它的应用，或禁用任务，链接随即失效。
        </div>
      </el-alert>

      <el-form label-position="top">
        <el-form-item label="以哪个应用的名义签发">
          <el-input :model-value="triggerLinkApp?.name || ''" readonly />
          <div class="trigger-link-hint">
            只用于记录「这条链接是谁发的」，真正的执行权限来自这个应用勾选的权限范围，需要含「任务管理（tasks）」。
          </div>
        </el-form-item>

        <el-form-item label="要触发的任务" required>
          <el-select
            v-model="triggerLinkTaskId"
            filterable
            clearable
            placeholder="搜索并选择一个任务"
            style="width: 100%"
            :loading="triggerLinkTasksLoading"
          >
            <el-option
              v-for="task in triggerLinkTasks"
              :key="task.id"
              :label="`#${task.id} ${task.name}`"
              :value="task.id"
            />
          </el-select>
          <div class="trigger-link-hint">
            票据只绑这一个任务：改 URL 上的 task_id 一定验签失败，一张票挪不到别的任务上。
          </div>
        </el-form-item>
      </el-form>

      <div v-if="triggerLinkResult" class="secret-display-card">
        <div class="secret-row">
          <span class="secret-label"
            >触发链接（任务：{{ triggerLinkResult.task_name }}）</span
          >
          <div class="secret-value-box">
            <code class="secret-value-text">{{ triggerLinkResult.url }}</code>
            <el-button
              class="copy-btn"
              size="small"
              @click="copyText(triggerLinkResult.url)"
            >
              <el-icon><DocumentCopy /></el-icon> 复制
            </el-button>
          </div>
        </div>
      </div>

      <!-- issue #152：菜单链接由手机上的企业微信内置浏览器直接打开，不经企业微信服务器，
           所以真实要求只是「这台手机能打开这条地址」，公网可达只是手机不在家里网络时的条件。
           企业微信后台的菜单是否接受内网地址没实测过，这里不承诺内网可用，也不写「必须公网可达」。 -->
      <div class="trigger-link-hint trigger-link-hint--block">
        前置条件：<strong>企业微信内置浏览器要能打开这条地址</strong>（手机不在家里网络时需要公网可达），
        并在「系统设置 → 企业微信触发」里打开「启用企业微信触发」与「允许企业微信触发执行」两个开关，
        否则点开链接只会看到一句「未开启」。
      </div>

      <template #footer>
        <el-button @click="triggerLinkDialogVisible = false">关闭</el-button>
        <el-button
          type="primary"
          :loading="triggerLinkGenerating"
          :disabled="triggerLinkGenerating"
          @click="generateTriggerLink"
          >生成链接</el-button
        >
      </template>
    </el-dialog>

    <el-dialog
      v-model="logsDialogVisible"
      title="调用日志"
      width="700px"
      :fullscreen="dialogFullscreen"
    >
      <el-table :data="callLogs" size="small" max-height="400">
        <el-table-column
          prop="endpoint"
          label="接口"
          min-width="200"
          show-overflow-tooltip
        />
        <el-table-column prop="method" label="方法" width="80" />
        <el-table-column
          prop="status"
          label="状态码"
          width="80"
          align="center"
        />
        <el-table-column
          prop="duration"
          label="耗时(ms)"
          width="90"
          align="center"
        />
        <el-table-column prop="ip" label="IP" width="140" />
        <el-table-column prop="created_at" label="时间" width="170">
          <template #default="{ row }">
            {{ formatDateTime(row.created_at) }}
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
      <!-- 移动端全屏时右上角 × 被全局规则隐藏（F2），关闭入口放到右下角；桌面不渲染 footer。 -->
      <template v-if="dialogFullscreen" #footer>
        <el-button @click="logsDialogVisible = false">关闭</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, computed, onMounted, nextTick } from "vue";
import { openApiApi } from "@/api/open-api";
import { taskApi } from "@/api/task";
import { ElMessage, ElMessageBox } from "element-plus";
import {
  Connection,
  DocumentCopy,
  Hide,
  Key,
  Lock,
  Plus,
  Refresh,
  Search,
  View,
} from "@element-plus/icons-vue";
import { useResponsive } from "@/composables/useResponsive";
import { formatDateTime } from "@/utils/datetime";
import { scrollListToTop } from "@/utils/scrollToTop";
import DdBadge from "@/components/ui/DdBadge.vue";
import DdSplitButton from "@/components/ui/DdSplitButton.vue";
import type { SplitButtonItem } from "@/components/ui/DdSplitButton.vue";

const apps = ref<any[]>([]);
const loading = ref(false);
const dialogVisible = ref(false);
const secretDialogVisible = ref(false);
const logsDialogVisible = ref(false);
const editingApp = ref<any>(null);
// 应用创建/更新的在途锁：连点会连开多个密钥弹窗，必须把整段（含弹密钥）锁在窗口内
const submitting = ref(false);
const secretData = ref<any>({});
const callLogs = ref<any[]>([]);
const logTotal = ref(0);
const logPage = ref(1);
const currentLogAppId = ref(0);
const revealedSecrets = reactive<Record<number, string>>({});

// ---------------------------------------------------------------------------
// 企业微信菜单触发链接（issue #145，v3.3.2）
//
// 换票接口挂在应用下（POST /open-api/apps/:id/task-trigger-link），所以入口做成行内操作；
// 作废是代次 +1、一次作废全部，没有「只废这一条」，所以单独放在工具条上。
// ---------------------------------------------------------------------------
const triggerLinkDialogVisible = ref(false);
const triggerLinkApp = ref<any>(null);
const triggerLinkTasks = ref<any[]>([]);
const triggerLinkTasksLoading = ref(false);
const triggerLinkTaskId = ref<number | undefined>(undefined);
const triggerLinkGenerating = ref(false);
// 生成结果：{ url, task_name, notice }。换任务重新生成时先清掉，免得旧链接还挂在那儿被误复制
const triggerLinkResult = ref<{ url: string; task_name: string } | null>(null);
const revokingTriggerLinks = ref(false);
const { isMobile, dialogFullscreen } = useResponsive();
// 页面根：翻页回顶的锚点。本页是 dd-scroll-page，桌面由页面根自己滚，移动端由外层 .layout-main 滚，
// scrollListToTop 会沿锚点及其祖先链把两者都覆盖到。
const pageRootRef = ref<HTMLElement | null>(null);

const searchKeyword = ref("");
const statusFilter = ref("");
const apiPage = ref(1);
const apiPageSize = 10;

const form = ref({ name: "", scopesList: [] as string[], rate_limit: 0 });

// 这七加一项必须与服务端 middleware.OpenAPIAccess("...") 实际强制的 8 个 scope 一致。
// 少一项的后果不是报错，而是「这个权限永远勾不上」——建出来的应用调该类接口一律 403，
// 且界面上看不出少了什么。改这里之前先跑一遍：
//   grep -rho 'OpenAPIAccess("[a-z]*")' server --include=*.go | sort -u
const scopeOptions = [
  { label: "任务管理", value: "tasks" },
  { label: "脚本管理", value: "scripts" },
  { label: "环境变量", value: "envs" },
  { label: "订阅管理", value: "subscriptions" },
  { label: "日志查看", value: "logs" },
  { label: "系统信息", value: "system" },
  { label: "通知渠道", value: "notifications" },
  { label: "系统备份", value: "backup" },
];

const scopeLabelMap: Record<string, string> = Object.fromEntries(
  scopeOptions.map((s) => [s.value, s.label]),
);

const parseScopesTags = (scopes: string): string[] => {
  if (!scopes) return [];
  return scopes
    .split(",")
    .map((s) => s.trim())
    .filter(Boolean)
    .map((s) => scopeLabelMap[s] || s);
};

const scopesToString = (list: string[]): string => list.join(",");

const stringToScopes = (str: string): string[] => {
  if (!str) return [];
  return str
    .split(",")
    .map((s) => s.trim())
    .filter(Boolean);
};

const formatRateLimit = (value: number | null | undefined) => {
  return Number(value || 0) > 0 ? `${value} / 小时` : "无限制";
};

const avatarColors = [
  "#409eff",
  "#67c23a",
  "#e6a23c",
  "#f56c6c",
  "#909399",
  "#06b6d4",
  "#14b8a6",
  "#10b981",
];

const getAvatarColor = (name: string): string => {
  if (!name) return avatarColors[0]!;
  let hash = 0;
  for (let i = 0; i < name.length; i++)
    hash = name.charCodeAt(i) + ((hash << 5) - hash);
  return avatarColors[Math.abs(hash) % avatarColors.length]!;
};

const getInitial = (name: string): string => {
  if (!name) return "?";
  return name.charAt(0).toUpperCase();
};

const maskKey = (key: string): string => {
  if (!key) return "";
  if (key.length <= 8) return key;
  return key.substring(0, 3) + "***" + key.substring(key.length - 5);
};

// 时间格式化已收编到 utils/datetime.ts（全站曾有 8 种写法，同一个「创建时间」在不同
// 页面长得不一样）。这里保留一个同名薄封装是为了不动模板里的 4 处调用点。

/**
 * 操作列 Split Button 的菜单项。
 *
 * 主体是「编辑」——最常用、且点错了也只是打开一个弹窗，代价最小。
 * 删除必须留在菜单里并加 divided + danger：它是不可撤销操作，
 * 绝不能出现在「点了就执行」的主体位置上。
 */
const appActionItems: SplitButtonItem[] = [
  { key: "reset", label: "重置密钥" },
  { key: "logs", label: "调用日志" },
  { key: "trigger-link", label: "生成企业微信触发链接" },
  { key: "delete", label: "删除", danger: true, divided: true },
];

function onAppAction(key: string, row: any) {
  if (key === "reset") resetSecret(row);
  else if (key === "logs") showLogs(row);
  else if (key === "trigger-link") openTriggerLinkDialog(row);
  else if (key === "delete") deleteApp(row);
}

// 只应用搜索词、不应用状态筛选的中间层。
//
// 拆出这一层是为了让「状态分段控件上的计数」和「切过去之后真正看到的条数」永远一致：
// 计数的基数必须排除状态筛选自身（否则选中「已启用」时「已禁用」永远显示 0），
// 但必须保留搜索词（搜完再看计数会和实际条数对不上）。
// 两个筛选是交集关系，先筛哪个结果都一样，这次调换顺序不改变 filteredApps 的输出。
const appsMatchingKeyword = computed(() => {
  const kw = searchKeyword.value.trim().toLowerCase();
  if (!kw) return apps.value;
  return apps.value.filter(
    (a) =>
      (a.name || "").toLowerCase().includes(kw) ||
      (a.app_key || "").toLowerCase().includes(kw),
  );
});

const filteredApps = computed(() => {
  const list = appsMatchingKeyword.value;
  if (statusFilter.value === "enabled") return list.filter((a) => a.enabled);
  if (statusFilter.value === "disabled") return list.filter((a) => !a.enabled);
  return list;
});

// 状态标签页的计数。
//
// 能这么算的前提：本页是【客户端筛选】——openApiApi.list() 一次性把全量应用拿回 apps.value，
// 状态/搜索/分页全在 computed 里做，点标签只改 statusFilter，不会重新发请求。
// 所以这里数出来的是真实总数，不是拿当前页冒充。
const statusCounts = computed(() => {
  const list = appsMatchingKeyword.value;
  const enabled = list.filter((a) => a.enabled).length;
  return { all: list.length, enabled, disabled: list.length - enabled };
});

const pagedApps = computed(() => {
  const start = (apiPage.value - 1) * apiPageSize;
  return filteredApps.value.slice(start, start + apiPageSize);
});

const handleSearch = () => {
  apiPage.value = 1;
};

// 翻页后回到列表顶部（issue #143 O3，双端都做）。本页分页器固定每页 10 条，没有 size-change。
// 只挂在分页器的 current-change 上：它只在用户翻页（或 total 变小把页码夹回）时触发，
// handleSearch 直接改 apiPage 不会触发，所以不会在搜索时把人拽回顶部。
// 本页是前端分页，数据早已在内存里，没有「请求回来」的时机可等；
// 等 nextTick 让新一页先渲染出来再滚，免得先回顶、后换内容，位置又被新内容的高度顶偏。
const handleApiPageChange = async () => {
  await nextTick();
  scrollListToTop(pageRootRef.value);
};

const copyText = async (text: string) => {
  if (!text) return;
  try {
    await navigator.clipboard.writeText(text);
    ElMessage.success("已复制");
  } catch {
    const textarea = document.createElement("textarea");
    textarea.value = text;
    textarea.style.position = "fixed";
    textarea.style.opacity = "0";
    document.body.appendChild(textarea);
    textarea.select();
    document.execCommand("copy");
    document.body.removeChild(textarea);
    ElMessage.success("已复制");
  }
};

const loadApps = async () => {
  loading.value = true;
  try {
    const res = await openApiApi.list();
    apps.value = (res as any).data || [];
  } catch (err: any) {
    ElMessage.error(err?.response?.data?.error || "加载应用列表失败");
  } finally {
    loading.value = false;
  }
};

const showCreateDialog = () => {
  editingApp.value = null;
  form.value = { name: "", scopesList: [], rate_limit: 0 };
  dialogVisible.value = true;
};

const editApp = (app: any) => {
  editingApp.value = app;
  form.value = {
    name: app.name,
    scopesList: stringToScopes(app.scopes || ""),
    rate_limit: Number(app.rate_limit || 0),
  };
  dialogVisible.value = true;
};

const submitForm = async () => {
  if (!form.value.name) {
    ElMessage.warning("请输入应用名称");
    return;
  }
  const payload = {
    name: form.value.name,
    scopes: scopesToString(form.value.scopesList),
    rate_limit: form.value.rate_limit,
  };
  // 名称校验通过后才置位，复位放 finally
  submitting.value = true;
  try {
    if (editingApp.value) {
      await openApiApi.update(editingApp.value.id, payload);
      ElMessage.success("更新成功");
    } else {
      const res = await openApiApi.create(payload);
      secretData.value = (res as any).data || {};
      secretDialogVisible.value = true;
      ElMessage.success("创建成功");
    }
    dialogVisible.value = false;
    loadApps();
  } catch (err: any) {
    ElMessage.error(err?.response?.data?.error || "操作失败");
  } finally {
    submitting.value = false;
  }
};

const viewSecret = async (app: any) => {
  try {
    const { value: password } = await ElMessageBox.prompt(
      "请输入登录密码以查看 App Secret",
      "身份验证",
      {
        inputType: "password",
        inputPlaceholder: "请输入密码",
        confirmButtonText: "确认",
        cancelButtonText: "取消",
      },
    );
    if (!password) return;
    const res = (await openApiApi.viewSecret(app.id, password)) as any;
    revealedSecrets[app.id] = res.data?.app_secret || "";
  } catch (e: any) {
    if (e === "cancel" || e?.toString() === "cancel") return;
    ElMessage.error(e?.response?.data?.error || "验证失败");
  }
};

const hideSecret = (id: number) => {
  delete revealedSecrets[id];
};

const toggleEnabled = async (app: any, val: boolean) => {
  try {
    await ElMessageBox.confirm(
      val
        ? `确认启用应用「${app.name}」吗？`
        : `确认禁用应用「${app.name}」吗？禁用后该 App Key / App Secret 将立即失效。`,
      val ? "启用确认" : "禁用确认",
      { type: val ? "info" : "warning" },
    );
    if (val) {
      await openApiApi.enable(app.id);
    } else {
      await openApiApi.disable(app.id);
    }
    app.enabled = val;
    ElMessage.success(val ? "已启用" : "已禁用");
  } catch (err: any) {
    if (err === "cancel" || err?.toString?.() === "cancel") return;
    ElMessage.error(err?.response?.data?.error || "操作失败");
  }
};

const resetSecret = async (app: any) => {
  try {
    await ElMessageBox.confirm("确认重置密钥？旧密钥将立即失效。", "警告", {
      type: "warning",
    });
  } catch {
    return;
  }
  try {
    const res = await openApiApi.resetSecret(app.id);
    secretData.value = (res as any).data || {};
    secretDialogVisible.value = true;
    delete revealedSecrets[app.id];
    ElMessage.success("密钥已重置");
  } catch (err: any) {
    ElMessage.error(err?.response?.data?.error || "重置失败");
  }
};

const deleteApp = async (app: any) => {
  try {
    await ElMessageBox.confirm(`确认删除应用 "${app.name}"？`, "提示", {
      type: "warning",
    });
  } catch {
    return;
  }
  try {
    await openApiApi.delete(app.id);
    ElMessage.success("删除成功");
    loadApps();
  } catch (err: any) {
    ElMessage.error(err?.response?.data?.error || "删除失败");
  }
};

const openTriggerLinkDialog = (app: any) => {
  triggerLinkApp.value = app;
  triggerLinkTaskId.value = undefined;
  triggerLinkResult.value = null;
  triggerLinkDialogVisible.value = true;
  // 任务列表只在第一次打开时拉一次：一次性取全量（all=1），下拉自带前端搜索，不用每次输入都发请求
  if (triggerLinkTasks.value.length === 0) void loadTriggerLinkTasks();
};

const loadTriggerLinkTasks = async () => {
  triggerLinkTasksLoading.value = true;
  try {
    const res = await taskApi.list({ all: 1 });
    triggerLinkTasks.value = res.data || [];
  } catch (err: any) {
    ElMessage.error(err?.response?.data?.error || "加载任务列表失败");
  } finally {
    triggerLinkTasksLoading.value = false;
  }
};

const generateTriggerLink = async () => {
  if (!triggerLinkApp.value) return;
  if (!triggerLinkTaskId.value) {
    ElMessage.warning("请先选择要触发的任务");
    return;
  }
  triggerLinkGenerating.value = true;
  try {
    const res = await openApiApi.issueTaskTriggerLink(
      triggerLinkApp.value.id,
      triggerLinkTaskId.value,
    );
    const data = res.data;
    triggerLinkResult.value = { url: data.url, task_name: data.task_name };
    ElMessage.success("链接已生成，请复制后填进企业微信应用菜单");
  } catch (err: any) {
    // 生成失败时把旧结果清掉：留着会让人以为这就是新任务的链接
    triggerLinkResult.value = null;
    ElMessage.error(err?.response?.data?.error || "生成触发链接失败");
  } finally {
    triggerLinkGenerating.value = false;
  }
};

const revokeTriggerLinks = async () => {
  try {
    await ElMessageBox.confirm(
      "确认作废全部企业微信触发链接？作废是全局的、不可逐条撤销：所有已经发出去的链接会立刻失效，企业微信菜单里配的那些也一样，需要重新生成并重新配置。",
      "作废确认",
      { type: "warning", confirmButtonText: "确认作废", cancelButtonText: "取消" },
    );
  } catch {
    return;
  }
  revokingTriggerLinks.value = true;
  try {
    await openApiApi.revokeTaskTriggerLinks();
    // 之前生成的那条也已经失效了，别继续摆在弹窗里给人复制
    triggerLinkResult.value = null;
    ElMessage.success("已作废全部企业微信触发链接");
  } catch (err: any) {
    ElMessage.error(err?.response?.data?.error || "作废失败");
  } finally {
    revokingTriggerLinks.value = false;
  }
};

const showLogs = (app: any) => {
  currentLogAppId.value = app.id;
  logPage.value = 1;
  logsDialogVisible.value = true;
  loadLogs();
};

const loadLogs = async () => {
  try {
    const res = await openApiApi.callLogs(currentLogAppId.value, {
      page: logPage.value,
      page_size: 20,
    });
    callLogs.value = (res as any).data || [];
    logTotal.value = (res as any).total || 0;
  } catch (err: any) {
    ElMessage.error(err?.response?.data?.error || "加载调用日志失败");
  }
};

onMounted(loadApps);
</script>

<style scoped lang="scss">
.open-api-page {
  padding: 0;
  overflow-x: hidden;
}

.page-header {
  display: flex;
  justify-content: space-between;
  align-items: flex-start;
  margin-bottom: 18px;
  gap: 14px;

  .page-title-with-icon {
    display: inline-flex;
    align-items: center;
    gap: 8px;
  }

  .page-title-with-icon :deep(.el-icon) {
    color: var(--el-color-primary);
  }

  h2 {
    margin: 0;
    font-size: 22px;
    font-weight: 700;
    color: var(--el-text-color-primary);
    line-height: 1.3;
  }
  .page-subtitle {
    font-size: 13px;
    color: var(--el-text-color-secondary);
    margin: 6px 0 0;
    line-height: 1.6;
    max-width: 720px;
  }
  .header-actions {
    display: flex;
    gap: 10px;
    flex-shrink: 0;
  }
}

// 工具条：与定时任务页/订阅页对齐——上下统一间距、左右两区一行排布、gap 一致
.toolbar {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin: 14px 0;
  gap: 12px;
  flex-wrap: wrap;
  &__left {
    display: flex;
    align-items: center;
    gap: 12px;
    flex-wrap: wrap;
    flex: 1;
    min-width: 0;
  }
  &__right {
    display: flex;
    align-items: center;
    gap: 10px;
  }
  &__search {
    width: 260px;
  }
}

// 状态分段控件：与定时任务页/订阅页一致的分段容器；选中态靠底色+品牌色文字区分，不再用阴影浮起
.status-tabs {
  display: inline-flex;
  background: var(--el-fill-color-light);
  // 分段控件的灰底槽属控件类表面 → control 档（与槽内的项同档，两者一致才不会露出内外错位的角）
  border-radius: var(--dd-radius-control);
  padding: 3px;
  gap: 2px;
}

.status-tab {
  // inline-flex + gap：标签文字与计数角标并排，间距交给 gap，不用 margin 拼。
  // 角标高 18px，与 13px 文字的行盒高度基本齐平，加上去不会把分段控件顶高。
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: 6px 14px;
  // 分段项属控件类表面 → control 档
  border-radius: var(--dd-radius-control);
  border: none;
  background: transparent;
  color: var(--el-text-color-secondary);
  font-size: 13px;
  font-weight: 500;
  cursor: pointer;
  transition:
    color var(--dd-motion-fast) var(--dd-ease-standard),
    background-color var(--dd-motion-fast) var(--dd-ease-standard);
  white-space: nowrap;
  &:hover {
    color: var(--el-text-color-primary);
  }
  &.active {
    background: var(--el-bg-color);
    color: var(--el-color-primary);
    font-weight: 600;
  }
}

// 表格卡：1px 边框划分层次，不再用阴影浮起（本页为滚动页，无需 fixed 高度链）
.table-card {
  background: var(--el-bg-color);
  // 表格容器属容器类表面 → surface 档；overflow:hidden 让内部贴边的表头/行自动被圆角裁角
  border-radius: var(--dd-radius-surface);
  border: 1px solid var(--el-border-color-lighter);
  overflow: hidden;
}

.app-name-cell {
  display: flex;
  align-items: center;
  gap: 10px;
}

// 应用头像：形状承载语义（圆形=头像/身份标识），两种圆角模式下都固定正圆，不吃 --dd-radius-* 令牌
.app-avatar {
  width: 34px;
  height: 34px;
  border-radius: 50%;
  display: flex;
  align-items: center;
  justify-content: center;
  color: #fff;
  font-weight: 700;
  font-size: 14px;
  flex-shrink: 0;
}

.app-name-info {
  display: flex;
  flex-direction: column;
  gap: 2px;
  min-width: 0;
}

.app-name-text {
  font-weight: 500;
  color: var(--el-text-color-primary);
  font-size: 14px;
}
.app-name-desc {
  font-size: 12px;
  color: var(--el-text-color-placeholder);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.scope-tag {
  margin: 2px 4px 2px 0;
}

.time-text {
  font-family: var(--dd-font-mono);
  font-size: 12px;
  color: var(--el-text-color-regular);
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
  // 表格内的 key 展示块是行内小型交互块（可复制），归控件类 → control 档
  border-radius: var(--dd-radius-control);
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

:deep(.el-table) {
  // 边框统一走令牌，明暗自动适配（原写死浅灰会在暗色串色）
  --el-table-border-color: var(--el-border-color-lighter);
  .el-table__header-wrapper th {
    border-bottom: 1px solid var(--el-border-color-light);
  }
  .el-table__row td {
    border-bottom: 1px solid var(--el-border-color-lighter);
  }
  .el-table__cell {
    padding: 12px 0;
  }
}

// 分页条：与定时任务页/订阅页一致的间距收敛
.pagination-bar {
  margin-top: 14px;
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 0 4px;
}
.pagination-total {
  font-size: 13px;
  color: var(--el-text-color-secondary);
}

.info-cards {
  display: grid;
  grid-template-columns: repeat(2, 1fr);
  gap: 16px;
  margin-top: 24px;
}

.info-card {
  // 信息卡：1px 边框，色面走令牌明暗自动适配；hover 只加深描边，不上浮
  background: var(--el-bg-color);
  transition: border-color var(--dd-motion-fast) var(--dd-ease-standard);
  // 信息卡属容器类表面 → surface 档
  border-radius: var(--dd-radius-surface);
  padding: 24px;
  border: 1px solid var(--el-border-color-lighter);

  &:hover {
    border-color: color-mix(
      in srgb,
      var(--el-color-primary) 18%,
      var(--el-border-color-lighter)
    );
  }

  &__header {
    display: flex;
    align-items: center;
    gap: 12px;
    margin-bottom: 16px;
  }

  &__icon {
    width: 40px;
    height: 40px;
    // 40×40 图标色底属控件类表面 → control 档（不做成正圆，避免与头像混淆）
    border-radius: var(--dd-radius-control);
    display: flex;
    align-items: center;
    justify-content: center;
    flex-shrink: 0;
    &--blue {
      background: color-mix(in srgb, var(--el-color-primary) 10%, transparent);
      color: var(--el-color-primary);
    }
    &--green {
      background: color-mix(in srgb, var(--el-color-success) 10%, transparent);
      color: var(--el-color-success);
    }
  }

  &__title {
    margin: 0;
    font-size: 15px;
    font-weight: 600;
    color: var(--el-text-color-primary);
  }

  &__list {
    margin: 0;
    padding: 0 0 0 18px;
    list-style: disc;
    li {
      font-size: 13px;
      color: var(--el-text-color-secondary);
      line-height: 2;
    }
  }
}

.secret-display-card {
  background: var(--el-fill-color-light);
  // 弹窗内的密钥展示区块属容器类表面 → surface 档
  border-radius: var(--dd-radius-surface);
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
  // 只读取值框（外观等同 input，且卡片有 20px 内边距不贴边）→ control 档
  border-radius: var(--dd-radius-control);
  padding: 10px 12px;
}

.secret-value-text {
  flex: 1;
  font-size: 13px;
  font-family: var(--dd-font-mono);
  word-break: break-all;
  line-height: 1.5;
}

// 触发链接弹窗里的说明文字：警告条正文 + 各表单项下的补充说明
.trigger-link-notice {
  font-size: 12px;
  line-height: 1.7;
}

.trigger-link-hint {
  margin-top: 6px;
  font-size: 12px;
  line-height: 1.6;
  color: var(--el-text-color-secondary);

  // 独立成段的前置条件说明（不跟在某个表单项下面），上方留出与卡片的间距
  &--block {
    margin-top: 16px;
  }
}

.open-api-card__actions > * {
  flex: 1 1 calc(50% - 4px);
}

@media screen and (max-width: 1200px) {
  .info-cards {
    grid-template-columns: 1fr;
  }
}

@media screen and (max-height: 720px) and (min-width: 769px) {
  .toolbar {
    margin-bottom: 10px;
  }

  .pagination-bar {
    margin-top: 12px;
  }

  .info-cards {
    margin-top: 16px;
  }

  .info-card {
    padding: 16px 18px;

    &__header {
      margin-bottom: 10px;
    }

    &__list li {
      line-height: 1.7;
    }
  }
}

@media (max-width: 768px) {
  .page-header {
    flex-direction: column;
    gap: 10px;
    margin-bottom: 14px;
    h2 {
      font-size: 18px;
    }
  }
  .toolbar {
    flex-direction: column;
    align-items: stretch;
    gap: 10px;
    &__left {
      flex-direction: column;
      gap: 10px;
    }
    &__search {
      width: 100% !important;
    }
    &__right {
      justify-content: flex-end;
    }
  }
  .status-tabs {
    width: 100%;
    overflow-x: auto;
  }
  .info-cards {
    grid-template-columns: 1fr;
  }
}

// ===== 入场动画 =====
// 与定时任务页/订阅页统一：只对卡片级容器（工具条 / 表格卡 / 移动列表）做克制的淡入上移 + 轻微错落；
// 不给表格每一行或每张移动卡做 stagger。时长走令牌，prefers-reduced-motion 时令牌自动降为 1ms 即等效关闭。
@keyframes dd-openapi-rise-in {
  from {
    opacity: 0;
    transform: translateY(12px);
  }
  to {
    opacity: 1;
    transform: translateY(0);
  }
}

// .info-cards 是本页唯一还没入场的卡片级容器（安全建议 / 开发者文档两张说明卡）。
// 它跟表格卡是同一层级的内容块，漏掉它会让页面下半截显得是「另一张页面拼上来的」。
.toolbar,
.table-card,
.dd-mobile-list,
.info-cards {
  animation: dd-openapi-rise-in var(--dd-motion-page) var(--dd-ease-decelerate)
    both;
}

// 轻微错落：工具条先入，表格卡/移动列表/说明卡略晚。
// 只分两档、间隔 60ms —— 不给表格每一行做 stagger（行多会卡，且是设计系统硬约束）。
.table-card,
.dd-mobile-list,
.info-cards {
  animation-delay: 60ms;
}
</style>
