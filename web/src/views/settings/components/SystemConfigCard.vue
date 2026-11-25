<script setup lang="ts">
import { computed } from 'vue'
import { ElMessage } from 'element-plus'
import { Document, MagicStick, Setting, Upload } from '@element-plus/icons-vue'
import type { SettingsConfigForm } from '../types'
import { resolveConfigOptions, type ParsedSystemConfigItem } from '../systemConfigSchema'
import { compressLogBackgroundImage, logBackgroundImageMaxBytes } from '../useSettingsConfig'
import { useThemeStore } from '@/stores/theme'
import { useUiStore } from '@/stores/ui'

const timezoneOptions = [
  { value: 'Asia/Shanghai', label: '中国时间 Asia/Shanghai' },
  { value: 'UTC', label: 'UTC 标准时间' },
  { value: 'Asia/Tokyo', label: '日本时间 Asia/Tokyo' },
  { value: 'Asia/Hong_Kong', label: '香港时间 Asia/Hong_Kong' },
  { value: 'Asia/Singapore', label: '新加坡时间 Asia/Singapore' },
  { value: 'America/New_York', label: '纽约时间 America/New_York' },
  { value: 'Europe/London', label: '伦敦时间 Europe/London' }
]

const props = defineProps<{
  configsLoading: boolean
  configsSaving: boolean
  form: SettingsConfigForm
  /** 全部注册项的服务端 schema，按 key 索引（useSettingsConfig 的 configSchema） */
  configSchema: Record<string, ParsedSystemConfigItem>
  onSave: () => void
  onIconUpload: (file: File) => boolean
  onLogBackgroundUpload: (file: File) => boolean
  onAppearancePreview: () => void
}>()

// 界面圆角 v3.2.9 从「其它配置项」兜底区挪进本卡：值走 form，
// 标题、说明、下拉选项仍取服务端 schema，不在 Web 另抄一份；schema 还没加载到时整项不渲染（卡片此时在 loading）
const shapeStyleItem = computed(() => props.configSchema.panel_shape_style)
// v4.0 的页面背景两项同样只认服务端 schema 的标题、说明、选项
const backgroundEffectItem = computed(() => props.configSchema.panel_background_effect)
const backgroundImageItem = computed(() => props.configSchema.panel_background_image)

const themeStore = useThemeStore()
const ui = useUiStore()

// 页面背景图上传：与日志背景图同一套格式、10MB 上限与自动压缩（compressLogBackgroundImage）。
// 只填进表单，点「保存配置」后才生效 —— 与圆角一样不做即时预览，预览要改主题引擎的全站值，没保存也会进首屏快照。
// 放在卡片里处理而不是经 props 回调：settings/index.vue 不归本组改，form 是父组件传进来的响应式对象，模板里本来就直接改它的字段
function handlePanelBackgroundUpload(file: File) {
  if (!file.type.startsWith('image/')) {
    ElMessage.warning('仅支持图片格式背景')
    return false
  }
  if (file.size > logBackgroundImageMaxBytes) {
    ElMessage.warning('背景图片不能超过 10MB')
    return false
  }
  void (async () => {
    try {
      const result = await compressLogBackgroundImage(file)
      props.form.panel_background_image = result.dataUrl
      ElMessage.success(result.compressed ? '背景图片已自动压缩，请保存配置后生效' : '背景图片已载入，请保存配置后生效')
    } catch {
      ElMessage.error('背景图片压缩失败，请换一张尺寸更小的图片')
    }
  })()
  return false
}
</script>

<template>
  <el-card shadow="never" v-loading="configsLoading">
    <template #header>
      <div class="card-header">
        <span class="card-title"><el-icon><Setting /></el-icon> 面板外观</span>
        <el-button type="primary" :loading="configsSaving" @click="onSave">
          <el-icon><Document /></el-icon>保存配置
        </el-button>
      </div>
    </template>

    <div class="config-section">
      <h4 class="section-title">面板设置</h4>
      <!-- v4.0：风格、主色、背景、玻璃模糊、圆角、密度、动效、自定义 CSS 都在主题工坊里（管理员可「设为全站默认」），这里只留一句入口 -->
      <button type="button" class="studio-hint" @click="ui.openStudio()">
        <el-icon><MagicStick /></el-icon>
        <span>更多外观（风格、主色、背景、玻璃模糊、圆角、密度、动效、自定义 CSS）在主题工坊，点这里打开</span>
      </button>
      <div class="form-field">
        <label>面板标题</label>
        <el-input v-model="form.panel_title" placeholder="呆呆面板" />
        <span class="form-hint">自定义面板的站点标题，留空使用默认值"呆呆面板"</span>
      </div>
      <div class="form-field">
        <label>面板时区</label>
        <el-select
          v-model="form.timezone"
          class="timezone-select"
          filterable
          allow-create
          default-first-option
          placeholder="Asia/Shanghai"
        >
          <el-option
            v-for="item in timezoneOptions"
            :key="item.value"
            :label="item.label"
            :value="item.value"
          />
        </el-select>
        <span class="form-hint">影响面板日志、任务日期判断和脚本运行时 TZ；Linux 二进制包建议保持 Asia/Shanghai</span>
      </div>
      <div class="form-field">
        <label>面板图标 (SVG)</label>
        <div class="icon-upload-row">
          <el-upload
            :show-file-list="false"
            :before-upload="onIconUpload"
            accept=".svg"
          >
            <el-button size="small"><el-icon><Upload /></el-icon>上传 SVG 图标</el-button>
          </el-upload>
          <div v-if="form.panel_icon" class="icon-preview">
            <img :src="form.panel_icon" alt="icon" class="icon-preview__image" />
            <el-button size="small" text type="danger" @click="form.panel_icon = ''">移除</el-button>
          </div>
        </div>
        <span class="form-hint">上传 SVG 格式图标自定义面板图标，留空使用默认图标</span>
      </div>
      <!--
        不做选中即预览：预览会顺手把圆角写进本机缓存，没保存也会带到下次首屏。
        取色等其它预览、切主题也不会带上这里没保存的选择（见 useSettingsConfig 的 appearanceSnapshot）；
        保存成功后由 useSettingsConfig 的 saveConfigKeys 重跑 applyPanelAppearance，当场生效
      -->
      <div v-if="shapeStyleItem" class="form-field">
        <label>{{ shapeStyleItem.label }}</label>
        <el-select v-model="form.panel_shape_style" class="shape-style-select">
          <el-option
            v-for="option in resolveConfigOptions(shapeStyleItem, form.panel_shape_style)"
            :key="option.value"
            :label="option.label"
            :value="option.value"
          />
        </el-select>
        <span v-if="shapeStyleItem.description" class="form-hint">{{ shapeStyleItem.description }}</span>
        <!-- 本机在主题工坊单独调过圆角滑杆时，会压过全站 panel_shape_style；提示并提供一键恢复 -->
        <div v-if="themeStore.isLocal('radius')" class="local-override-tip">
          <span>本机已在主题工坊单独覆盖，当前修改仅影响未覆盖设备</span>
          <el-button link type="primary" size="small" @click="themeStore.setRadius(null)">
            恢复跟随全站
          </el-button>
        </div>
      </div>
      <!-- 页面背景效果（v4.0）：与圆角一样保存后生效，选项取服务端 schema -->
      <div v-if="backgroundEffectItem" class="form-field">
        <label>{{ backgroundEffectItem.label }}</label>
        <el-select v-model="form.panel_background_effect" class="shape-style-select">
          <el-option
            v-for="option in resolveConfigOptions(backgroundEffectItem, form.panel_background_effect)"
            :key="option.value"
            :label="option.label"
            :value="option.value"
          />
        </el-select>
        <span v-if="backgroundEffectItem.description" class="form-hint">{{ backgroundEffectItem.description }}</span>
        <div v-if="themeStore.isLocal('background')" class="local-override-tip">
          <span>本机已在主题工坊单独覆盖，当前修改仅影响未覆盖设备</span>
          <el-button link type="primary" size="small" @click="themeStore.setBackground(null)">
            恢复跟随全站
          </el-button>
        </div>
      </div>
      <div v-if="backgroundImageItem" class="form-field">
        <label>{{ backgroundImageItem.label }}</label>
        <div class="log-bg-upload">
          <el-upload
            :show-file-list="false"
            :before-upload="handlePanelBackgroundUpload"
            accept="image/*"
          >
            <el-button size="small"><el-icon><Upload /></el-icon>上传背景图</el-button>
          </el-upload>
          <el-button
            v-if="form.panel_background_image"
            size="small"
            text
            type="danger"
            @click="form.panel_background_image = ''"
          >
            移除背景图
          </el-button>
        </div>
        <div
          v-if="form.panel_background_image"
          class="page-bg-preview"
          :style="{ backgroundImage: `url('${form.panel_background_image}')` }"
        ></div>
        <span class="form-hint">
          背景效果选「{{ backgroundEffectItem?.options.find((option) => option.value === 'image')?.label || '图片' }}」时铺满页面背景；
          支持常见图片格式，单张最大 10MB，较大的图片会自动压缩后保存
        </span>
      </div>
      <div class="form-field">
        <label>编辑器背景颜色</label>
        <div class="log-bg-controls">
          <el-color-picker v-model="form.editor_background_color" @change="onAppearancePreview" />
          <el-input v-model="form.editor_background_color" placeholder="留空跟随当前主题" @change="onAppearancePreview" />
        </div>
        <span class="form-hint">统一应用到脚本只读预览和在线编辑器，留空时浅色模式为白底深字，深色模式为深底浅字</span>
      </div>
      <div class="form-field">
        <label>日志背景颜色</label>
        <div class="log-bg-controls">
          <el-color-picker v-model="form.log_background_color" show-alpha @change="onAppearancePreview" />
          <el-input v-model="form.log_background_color" placeholder="留空跟随当前主题" @change="onAppearancePreview" />
        </div>
        <span class="form-hint">统一应用到任务日志和执行日志查看器，留空时浅色模式为浅底深字，深色模式为深底浅字</span>
      </div>
      <div class="form-field">
        <label>日志背景图片</label>
        <div class="log-bg-upload">
          <el-upload
            :show-file-list="false"
            :before-upload="onLogBackgroundUpload"
            accept="image/*"
          >
            <el-button size="small"><el-icon><Upload /></el-icon>上传背景图</el-button>
          </el-upload>
          <el-button
            v-if="form.log_background_image"
            size="small"
            text
            type="danger"
            @click="form.log_background_image = ''; onAppearancePreview()"
          >
            移除背景图
          </el-button>
        </div>
        <div
          class="log-bg-preview dd-log-surface"
          :style="{
            backgroundColor: form.log_background_color || undefined,
            backgroundImage: form.log_background_image
              ? `url('${form.log_background_image}'), radial-gradient(circle at top right, color-mix(in srgb, var(--dd-log-text-color) 10%, transparent), transparent 24%), linear-gradient(155deg, color-mix(in srgb, var(--dd-log-bg-color) 96%, white), color-mix(in srgb, var(--dd-log-bg-color) 88%, var(--dd-log-text-color) 8%))`
              : undefined
          }"
        >
          <div class="log-bg-preview__content">任务输出预览：日志背景将应用到所有日志查看器</div>
        </div>
        <span class="form-hint">支持常见图片格式，单张最大 10MB；较大的图片会自动压缩后保存</span>
      </div>
    </div>
  </el-card>
</template>

<style scoped lang="scss">
@use './config-card-shared.scss' as *;

.log-bg-controls {
  display: flex;
  align-items: center;
  gap: 12px;
}

.icon-upload-row {
  display: flex;
  align-items: center;
  gap: 12px;
  flex-wrap: wrap;
}

.icon-preview {
  display: inline-flex;
  align-items: center;
  gap: 8px;
}

.icon-preview__image {
  width: 32px;
  height: 32px;
}

.timezone-select {
  width: 100%;
  max-width: 360px;
}

// 与兜底区的下拉同宽（ExtraConfigCard 的 .extra-config-select），挪过来看起来不变
.shape-style-select {
  width: 100%;
}

.studio-hint {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  margin: 0 0 18px;
  padding: 0;
  border: none;
  background: none;
  font: inherit;
  font-size: 13px;
  // 主色当文字用墨色令牌，保证各主题下对比度
  color: var(--dd-accent-ink);
  cursor: pointer;
  text-align: left;

  &:hover {
    text-decoration: underline;
  }
}

.local-override-tip {
  display: flex;
  align-items: center;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: 6px 10px;
  margin-top: 8px;
  padding: 6px 10px;
  border-radius: var(--dd-radius-control);
  background: color-mix(in srgb, var(--dd-warn) 10%, var(--dd-surface-solid));
  border: var(--dd-border-width, 1px) solid color-mix(in srgb, var(--dd-warn) 28%, transparent);
  font-size: 12px;
  line-height: 1.5;
  color: var(--dd-warn-ink);
}

.page-bg-preview {
  height: 96px;
  max-width: 400px;
  margin-bottom: 4px;
  border-radius: var(--dd-radius-surface);
  border: var(--dd-border-width, 1px) solid var(--dd-divider, var(--dd-border-strong));
  background-color: var(--dd-surface-2);
  background-size: cover;
  background-position: center;
}

.log-bg-upload {
  display: flex;
  align-items: center;
  gap: 12px;
  margin-bottom: 12px;

  :deep(.el-button + .el-button) {
    margin-left: 0;
  }
}

.log-bg-preview {
  padding: 18px;
  min-height: 92px;
  overflow: hidden;
}

.log-bg-preview__content {
  font-family: var(--dd-font-mono);
  font-size: 13px;
  line-height: 1.7;
  white-space: pre-wrap;
}

@media (max-width: 768px) {
  .log-bg-controls {
    flex-direction: column;
    align-items: stretch;
  }

  .log-bg-upload {
    flex-wrap: wrap;
  }
}
</style>
