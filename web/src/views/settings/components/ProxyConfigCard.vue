<script setup lang="ts">
import { computed, ref } from 'vue'
import { Connection, Document, InfoFilled } from '@element-plus/icons-vue'
import { useResponsive } from '@/composables/useResponsive'
import { formatDateTime } from '@/utils/datetime'
import type { SettingsConfigForm } from '../types'

const props = defineProps<{
  configsLoading: boolean
  configsSaving: boolean
  form: SettingsConfigForm
  /** 服务端的 auto_update_last_checked_at 原值（ISO 时间或空串），只读展示、不回写 */
  autoUpdateLastCheckedAt: string
  onSave: () => void
}>()

// issue #144 / v3.3.2：下面三个说明弹窗在移动端必须整屏。
// 遮罩只有 24% 暗度，弹窗不铺满视口的话，sticky 顶栏会从遮罩后面透出来；
// 而它的层级又低于遮罩、点了没反应，观感就是「顶栏卡住了」。
const { dialogFullscreen } = useResponsive()

// 空值文案用「从未检查」而不是 "-"：说明是一次都没查过，而不是查了没记下来
const lastCheckedDisplay = computed(() => formatDateTime(props.autoUpdateLastCheckedAt, '从未检查'))

const dockerMirrorDialogVisible = ref(false)
const binaryProxyDialogVisible = ref(false)
const proxyHelpDialogVisible = ref(false)

const dockerMirrorOptions = [
  'https://docker.1ms.run',
  'https://docker.1panel.live',
  'https://docker.sparkcr.cn',
  'https://hub.rat.dev',
  'https://dockerproxy.net',
  'https://mirror.ccs.tencentyun.com'
]

const binaryProxyOptions = [
  'https://gh-proxy.org/',
  'https://v4.gh-proxy.org/',
  'http://gh.301.ee/',
  'https://ghproxy.homeboyc.cn/'
]
</script>

<template>
  <el-card shadow="never" v-loading="configsLoading">
    <template #header>
      <div class="card-header">
        <span class="card-title"><el-icon><Connection /></el-icon> 网络代理</span>
        <el-button type="primary" :loading="configsSaving" @click="onSave">
          <el-icon><Document /></el-icon>保存配置
        </el-button>
      </div>
    </template>

    <div class="form-field">
      <div class="field-label-row">
        <label>代理地址</label>
        <el-button
          class="field-help-button"
          text
          type="primary"
          size="small"
          @click="proxyHelpDialogVisible = true"
        >
          <el-icon><InfoFilled /></el-icon>
          说明
        </el-button>
      </div>
      <el-input v-model="form.proxy_url" placeholder="http://127.0.0.1:7890" />
      <span class="form-hint">服务器出站访问外网困难时填写；支持 HTTP/SOCKS5，如 http://127.0.0.1:7890</span>
    </div>

    <div class="form-field">
      <label>系统更新镜像源</label>
      <div class="mirror-row">
        <el-input v-model="form.update_image_mirror" placeholder="https://docker.example.com" />
        <el-button @click="dockerMirrorDialogVisible = true">
          配置
        </el-button>
        <el-button
          v-if="form.update_image_mirror"
          text
          type="danger"
          @click="form.update_image_mirror = ''"
        >
          恢复直连
        </el-button>
      </div>
      <span class="form-hint">
        仅用于旧 Docker Socket 一键更新。Watchtower 部署请在 .env 中设置 DAIDAI_PANEL_IMAGE，让容器镜像与 IMAGE_NAME 同步使用镜像加速或自建仓库。
      </span>
    </div>

    <div class="form-field">
      <label>二进制更新加速源</label>
      <div class="mirror-row">
        <el-input v-model="form.binary_update_proxy" placeholder="https://gh-proxy.example.com/" />
        <el-button @click="binaryProxyDialogVisible = true">
          配置
        </el-button>
        <el-button
          v-if="form.binary_update_proxy"
          text
          type="danger"
          @click="form.binary_update_proxy = ''"
        >
          恢复直连
        </el-button>
      </div>
      <span class="form-hint">
        二进制部署更新使用，用于加速 GitHub Release 更新包下载；留空则直连 GitHub 下载。
      </span>
    </div>

    <!-- #177：匿名访问 GitHub API 按出口 IP 每小时只有 60 次，共用代理时常被别人用光，检查更新就会报限流。
         服务端只在请求 api.github.com 时带上它，不会带到加速源与 Release 下载地址。
         与其它 Secret 配置（captcha_key、备份密码）一样用密码框 + show-password；
         autocomplete=new-password 防止浏览器把登录密码自动填进来 -->
    <div class="form-field">
      <label>GitHub Token</label>
      <el-input
        v-model="form.github_token"
        type="password"
        show-password
        autocomplete="new-password"
        placeholder="可选，留空则匿名访问"
      />
      <span class="form-hint">
        只用于检查更新，提高 GitHub API 的限流额度（匿名每小时 60 次，共用代理出口时容易被用光）；填一个不勾任何权限的 fine-grained token 即可。
      </span>
    </div>

    <div class="switch-row">
      <div class="switch-item">
        <span class="switch-label">静默更新</span>
        <el-switch v-model="form.auto_update_enabled" inline-prompt active-text="开" inactive-text="关" />
      </div>
    </div>
    <span class="form-hint">开启后每 24 小时自动检查一次新版本；若检测到更新，将按当前镜像渠道自动尝试更新并通过通知渠道反馈结果。</span>
    <!-- 巡检与概览页「检查系统更新」都会写这个时间，巡检按它判断是否满 24 小时；排查「为什么没自动更新」时有用 -->
    <span class="form-hint last-checked-hint">上次检查更新时间：{{ lastCheckedDisplay }}</span>

    <div class="form-field">
      <label>可信代理 CIDR</label>
      <el-input
        v-model="form.trusted_proxy_cidrs"
        type="textarea"
        :rows="5"
        placeholder="127.0.0.1/32&#10;::1/128&#10;172.17.0.1"
      />
      <!-- v3.4.4（审计 H-2）起默认只信任本机：私网段默认可信时，经 Docker 网桥 / 内网反代进来的请求
           能随手伪造 X-Forwarded-For，绕过 IP 白名单和登录限流。代价是反代后面的用户要自己把反代地址填进来，
           这里把「填什么、不填会怎样、填错进不去怎么办」一次讲清楚。 -->
      <span class="form-hint">
        支持 IP、CIDR、逗号或换行分隔。留空恢复默认（只信任本机）。
        面板在宿主机 nginx、宝塔、Lucky 等反代后面时，把反代访问面板时的来源地址填进来（Docker 默认网桥一般是 172.17.0.1，不确定就填 172.16.0.0/12），否则登录日志、限流看到的都是反代地址。
        经 Cloudflare 等 CDN 访问时，把 CDN 的回源 IP 段也填进来，面板会从 X-Forwarded-For 里取出真实访客 IP（不再读取 CF-Connecting-IP 这类可被伪造的请求头）。
        开了 IP 白名单后进不去面板，在服务器上执行 ddp ip-whitelist clear 清空白名单（Docker 部署前面加 docker exec -it daidai-panel）。
      </span>
    </div>

    <el-dialog v-model="proxyHelpDialogVisible" title="代理地址说明" width="560px" :fullscreen="dialogFullscreen">
      <div class="proxy-help">
        <p>
          这里配置的是面板服务器的出站代理。填写后，面板后台访问外部网络时会优先经过这个代理，例如拉取订阅仓库、下载脚本、安装 Python / Node / 系统依赖、健康检查以及部分通知请求。
        </p>
        <p>
          如果服务器本身访问 GitHub、npm、PyPI、订阅源或外部接口正常，可以留空；如果服务器在国内网络环境下经常连接超时、下载失败、依赖安装失败，或者你需要让面板通过指定代理访问外网，就填写这里。
        </p>
        <div class="proxy-help-section">
          <div class="proxy-help-title">填写示例</div>
          <code>http://127.0.0.1:7890</code>
          <code>http://user:pass@127.0.0.1:7890</code>
          <code>socks5://127.0.0.1:1080</code>
        </div>
        <p class="proxy-help-note">
          这里填写的是“面板服务器能访问到的代理地址”。如果面板运行在 Docker 容器内，127.0.0.1 指的是容器内部，不是宿主机；宿主机代理通常需要填写宿主机在容器内可访问的地址。
        </p>
        <!-- issue #111：开代理后 Python 的 notify.py 把发往面板自身的回调请求也交给了代理，代理回 502。
             修复方式是给脚本注入的 NO_PROXY / no_proxy 里合并追加回环地址，这里把这条行为讲清楚，
             免得用户以为“填了代理连本机接口也要走代理”。 -->
        <p class="proxy-help-note">
          面板会自动为 localhost / 127.0.0.1 / ::1 放行直连，脚本回调面板自身不经过代理。这三条是面板强制注入的：你在“环境变量”页设置的 NO_PROXY 会与它们合并（你的值不会被覆盖，但也无法把这三条移除）；确实要让某个本机服务走代理，需要在脚本内部自行指定 proxies。
        </p>
      </div>
      <template #footer>
        <el-button type="primary" @click="proxyHelpDialogVisible = false">知道了</el-button>
      </template>
    </el-dialog>

    <el-dialog v-model="dockerMirrorDialogVisible" title="系统更新镜像源" width="520px" :fullscreen="dialogFullscreen">
      <div class="mirror-source-tip">
        可到
        <a href="https://status.anye.xyz/" target="_blank" rel="noopener noreferrer">
          容器镜像监控
        </a>
        查看更多 Docker Hub 镜像加速源状态，选择可用地址后手动填入上方输入框。
      </div>
      <div class="mirror-option-list">
        <button
          v-for="url in dockerMirrorOptions"
          :key="url"
          type="button"
          class="mirror-option"
          :class="{ active: form.update_image_mirror === url }"
          @click="form.update_image_mirror = url; dockerMirrorDialogVisible = false"
        >
          <span>{{ url }}</span>
        </button>
      </div>
      <template #footer>
        <el-button @click="dockerMirrorDialogVisible = false">关闭</el-button>
      </template>
    </el-dialog>

    <el-dialog v-model="binaryProxyDialogVisible" title="二进制更新加速源" width="520px" :fullscreen="dialogFullscreen">
      <div class="mirror-option-list">
        <button
          v-for="url in binaryProxyOptions"
          :key="url"
          type="button"
          class="mirror-option"
          :class="{ active: form.binary_update_proxy === url }"
          @click="form.binary_update_proxy = url; binaryProxyDialogVisible = false"
        >
          <span>{{ url }}</span>
        </button>
      </div>
      <template #footer>
        <el-button @click="binaryProxyDialogVisible = false">关闭</el-button>
      </template>
    </el-dialog>
  </el-card>
</template>

<style scoped lang="scss">
@use './config-card-shared.scss' as *;

.mirror-row {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
}

// 静默更新这一组（开关 + 说明 + 上次检查时间）不在 .form-field 里，自己补上与下一项之间的间距
.last-checked-hint {
  margin-bottom: 20px;
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

.field-help-button {
  flex-shrink: 0;
  padding: 0 2px;
  font-size: 12px;

  .el-icon {
    margin-right: 3px;
  }
}

.proxy-help {
  display: grid;
  gap: 12px;
  color: var(--el-text-color-regular);
  font-size: 14px;
  line-height: 1.75;

  p {
    margin: 0;
  }
}

.proxy-help-section {
  display: grid;
  gap: 8px;
  padding: 12px;
  border: 1px solid var(--el-border-color-lighter);
  // 帮助说明区块属容器类表面 → surface 档
  border-radius: var(--dd-radius-surface);
  background: var(--el-fill-color-lighter);

  code {
    display: block;
    padding: 7px 9px;
    // 区块内的代码块（四周有 12px 留白、不贴边）→ surface 档
    border-radius: var(--dd-radius-surface);
    background: var(--el-bg-color);
    color: var(--el-text-color-primary);
    font-family: var(--dd-font-mono);
    font-size: 12px;
    word-break: break-all;
  }
}

.proxy-help-title {
  color: var(--el-text-color-primary);
  font-weight: 600;
}

.proxy-help-note {
  color: var(--el-text-color-secondary);
}

.mirror-option-list {
  display: grid;
  gap: 8px;
}

.mirror-source-tip {
  margin-bottom: 12px;
  padding: 10px 12px;
  border: 1px solid var(--el-border-color-lighter);
  // 镜像来源提示块属容器类表面 → surface 档
  border-radius: var(--dd-radius-surface);
  background: var(--el-fill-color-lighter);
  color: var(--el-text-color-secondary);
  font-size: 13px;
  line-height: 1.6;

  a {
    color: var(--el-color-primary);
    font-weight: 600;
    text-decoration: none;

    &:hover {
      text-decoration: underline;
    }
  }
}

.mirror-option {
  width: 100%;
  min-height: 40px;
  padding: 9px 12px;
  border: 1px solid var(--el-border-color);
  // 可点击的镜像选项是控件类表面 → control 档
  border-radius: var(--dd-radius-control);
  background: var(--el-bg-color);
  color: var(--el-text-color-primary);
  cursor: pointer;
  text-align: left;
  font-family: var(--dd-font-mono);
  font-size: 13px;
  line-height: 1.35;
  transition: border-color 0.16s, background 0.16s, color 0.16s;

  &:hover,
  &.active {
    border-color: var(--el-color-primary);
    background: color-mix(in srgb, var(--el-color-primary) 8%, var(--el-bg-color));
    color: var(--el-color-primary);
  }
}

@media (max-width: 768px) {
  .mirror-row {
    align-items: stretch;
  }
}
</style>
