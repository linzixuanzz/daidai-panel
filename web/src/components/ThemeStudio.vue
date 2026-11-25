<script setup lang="ts">
/**
 * 主题工坊（v4.0 F3，design §1.3 ~ §1.6）：右侧抽屉，无 props，显隐读写 useUiStore().studioOpen。
 * 由 MainLayout 挂一次；入口在顶栏按钮、命令面板、个人设置页「打开主题工坊」、系统设置「面板外观」卡的提示。
 *
 * 读写全部走 useThemeStore（真正的优先级合并、写 <html> 都在 utils/panelAppearance.ts）：
 *   - 每一项都显示「本机 / 跟随全站」，本机设过的可单独恢复为跟随全站；
 *   - 切风格、主色、背景时把点击事件传进去当过渡起点，从点击处圆形扩散（View Transitions，非丰富档直接切）；
 *   - 管理员多一块：设为全站默认、页面背景图、自定义 CSS（实时预览，保存时服务端校验，400 显示中文原因）。
 */
import { computed, ref, watch } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Brush, Close, Delete, Download, MagicStick, RefreshLeft, Upload } from '@element-plus/icons-vue'
import { configApi } from '@/api/system'
import { useAuthStore } from '@/stores/auth'
import { useThemeStore } from '@/stores/theme'
import { useUiStore } from '@/stores/ui'
import { canAdminister } from '@/utils/roles'
import {
  THEME_PRESETS,
  type BackgroundEffect,
  type LocalAppearanceKey,
  type MotionPreference,
  type PanelDensity,
  type ThemePresetMeta,
} from '@/utils/panelAppearance'
import { compressLogBackgroundImage, logBackgroundImageMaxBytes } from '@/views/settings/useSettingsConfig'

const theme = useThemeStore()
const ui = useUiStore()
const authStore = useAuthStore()

// 管理员区（设为全站默认 / 背景图 / 自定义 CSS）只给管理员渲染；服务端 /configs 本身也只认管理员
const isAdmin = computed(() => canAdminister(authStore.user?.role))

// 主色候选（是预览数据，不是界面配色）。第一个「跟随」= 回到全站值，全站也没设就是主题自带的主色
const ACCENT_SWATCHES = ['#2563eb', '#7c3aed', '#db2777', '#ea580c', '#059669', '#0891b2', '#ca8a04', '#dc2626']

const backgroundOptions: Array<{ value: BackgroundEffect; label: string }> = [
  { value: 'theme', label: '跟随主题' },
  { value: 'plain', label: '纯色' },
  { value: 'aurora', label: '极光' },
  { value: 'grid', label: '网格' },
  { value: 'image', label: '图片' },
]

// 文案与服务端注册表 panel_density 的选项一致（紧凑 / 舒适 / 宽松）
const densityOptions: Array<{ value: PanelDensity; label: string }> = [
  { value: 'compact', label: '紧凑' },
  { value: 'cozy', label: '舒适' },
  { value: 'roomy', label: '宽松' },
]

const motionOptions: Array<{ value: MotionPreference; label: string }> = [
  { value: 'system', label: '跟随系统' },
  { value: 'rich', label: '丰富' },
  { value: 'subtle', label: '克制' },
  { value: 'off', label: '关闭' },
]

// 自定义 CSS 片段（原型同款四个）。选择器对应面板里的真实类名，追加到编辑框末尾并立即预览
const CSS_SNIPPETS: Array<{ name: string; css: string }> = [
  {
    name: '霓虹描边',
    css: '.el-card, .dd-glass { box-shadow: 0 0 0 1px var(--dd-accent), 0 0 28px -6px var(--dd-accent); }',
  },
  {
    name: '彩虹标题',
    css: '.dd-page-header__title { background: linear-gradient(90deg, #f43f5e, #f59e0b, #10b981, #3b82f6, #8b5cf6); -webkit-background-clip: text; background-clip: text; color: transparent; }',
  },
  {
    name: '超大数字',
    // 仪表板统计卡的大数字（G3 重构后的类名）
    css: '.dash-kpi__num { font-size: 40px; letter-spacing: -1.5px; }',
  },
  {
    name: '隐藏分组标题',
    css: '.dd-nav-group-title { display: none; }',
  },
]

// 与服务端 panel_custom_css 的长度上限一致（design §1.6），超了前端先拦，不必等 400
const CUSTOM_CSS_MAX_BYTES = 32768

// 预设卡片缩略图：用 THEME_PRESETS 里的预览色块画一个迷你面板（侧栏 + 顶条 + 两张卡），跟当前明暗走
function presetThumbStyle(item: ThemePresetMeta) {
  const swatch = item.swatch[theme.isDark ? 'dark' : 'light']
  return {
    '--pv-bg': swatch.bg,
    '--pv-s': swatch.surface,
    '--pv-a': swatch.accent,
    '--pv-a2': swatch.accent2,
    // 有极光块的主题在缩略图左上角铺一团主色光晕
    '--pv-glow': item.blobs ? '0.55' : '0',
  }
}

// 本机 / 跟随全站 两态的标签文字
function scopeLabel(key: LocalAppearanceKey) {
  return theme.isLocal(key) ? '本机' : '跟随全站'
}

// 主色：本机设过且等于这个色块时高亮（统一小写比较，避免大小写 hex 漏判）
function isAccentActive(color: string) {
  return theme.isLocal('accent') && theme.accent.toLowerCase() === color
}

// 本机选了非预设主色时，给自定义取色器挂选中外环
const isCustomAccentActive = computed(
  () => theme.isLocal('accent') && Boolean(theme.accent) && !ACCENT_SWATCHES.includes(theme.accent.toLowerCase()),
)

function handleCustomAccent(event: Event) {
  const value = (event.target as HTMLInputElement).value
  if (/^#[0-9a-fA-F]{6}$/.test(value)) {
    theme.setAccent(value.toLowerCase())
  }
}

// 圆角滑杆没设过时显示「全站圆角风格」对应的卡片圆角：直角 0、圆角 10（与 panelAppearance 的 rounded 档一致）、跟随主题取主题默认
const radiusValue = computed(() => {
  if (theme.radius !== null) return theme.radius
  if (theme.site.shape === 'square') return 0
  if (theme.site.shape === 'rounded') return 10
  return theme.presetMeta.radius
})

// 有效模糊为 0 但本机和全站都没设：要么主题本身不透明，要么是低端设备降级，给一句说明
const blurHint = computed(() => {
  if (theme.isLocal('blur') || theme.site.blur !== null) return ''
  if (theme.presetMeta.blur === 0) return '这个风格默认是不透明卡片'
  if (theme.blur === 0) return '检测到低端设备，动效跟随系统时自动关闭模糊'
  return ''
})

function handleBackgroundClick(value: BackgroundEffect, event: MouseEvent) {
  // 选「图片」但全站还没有背景图：AppBackdrop 没东西可画，直接拦下并说明
  if (value === 'image' && !theme.backgroundImage) {
    ElMessage.warning(isAdmin.value ? '全站还没有背景图，请先在下方「页面背景图」上传' : '全站还没有背景图，需要管理员先上传')
    return
  }
  theme.setBackground(value, event)
}

function handleResetAll() {
  // 动效不在其中：它没有全站值，单独在动效一行恢复
  theme.resetLocal()
  ElMessage.success('已全部恢复为全站默认')
}

// ===================== 导出 / 导入 =====================

async function handleExport() {
  // 管理员导出时带上全站自定义 CSS，别人（管理员）导入时可以一并载入
  const json = theme.exportTheme({ includeCustomCss: isAdmin.value })
  try {
    await navigator.clipboard.writeText(json)
    ElMessage.success('主题 JSON 已复制到剪贴板，在主题工坊点「导入」粘贴即可')
    return
  } catch {
    // 非安全上下文（http + IP）没有剪贴板权限：退回下载文件
  }
  const url = URL.createObjectURL(new Blob([json], { type: 'application/json' }))
  const link = document.createElement('a')
  link.href = url
  link.download = `daidai-theme-${theme.preset}.json`
  link.click()
  URL.revokeObjectURL(url)
  ElMessage.success('浏览器不允许写剪贴板，已改为下载主题 JSON 文件')
}

async function handleImport() {
  let text = ''
  try {
    const { value } = (await ElMessageBox.prompt('把导出的主题 JSON 粘贴到下面', '导入主题', {
      inputType: 'textarea',
      inputPlaceholder: '{ "version": 1, "preset": "aurora", ... }',
      confirmButtonText: '导入',
      cancelButtonText: '取消',
      inputValidator: (value: string) => (value && value.trim() ? true : '请粘贴主题 JSON'),
    })) as { value: string }
    text = value.trim()
  } catch {
    return
  }

  const result = theme.importTheme(text, { allowCustomCss: isAdmin.value })
  if (!result.ok) {
    ElMessage.error(`导入失败：${result.error}`)
    return
  }
  if (result.customCss !== null) {
    // 管理员：自定义 CSS 只载入编辑框并预览，点「保存为全站」才真正生效
    cssDraft.value = result.customCss
    ElMessage.success('主题已导入；其中的自定义 CSS 已载入编辑框预览，点「保存为全站」后才对所有人生效')
    return
  }
  // 非管理员导入带 customCSS 的文件：store 已忽略该字段，这里说一声
  let hasCustomCss = false
  try {
    hasCustomCss = Boolean((JSON.parse(text) as Record<string, unknown>).customCSS)
  } catch {
    // 上面已经解析成功过，这里不会失败
  }
  ElMessage.success(hasCustomCss && !isAdmin.value ? '主题已导入；其中的自定义 CSS 只有管理员能用，已忽略' : '主题已导入')
}

// ===================== 管理员：设为全站默认 =====================

const siteSaving = ref(false)

async function handleSetSiteDefault() {
  const next = theme.buildSiteDefaultConfigs()
  if (next.panel_background_effect === 'image' && !theme.backgroundImage) {
    ElMessage.warning('全站还没有背景图，不能把「图片」设为全站背景效果，请先上传背景图')
    return
  }
  // 只提交和全站当前值不同的键（与设置页 submitConfigs 同一规则：服务端逐键写，出错时范围最小）
  const current: Record<string, string> = {
    panel_theme_preset: theme.site.preset,
    panel_accent_color: theme.site.accent,
    panel_background_effect: theme.site.background,
    panel_glass_blur: theme.site.blur === null ? '' : String(theme.site.blur),
    panel_density: theme.site.density,
  }
  const changed: Record<string, string> = {}
  for (const [key, value] of Object.entries(next)) {
    if (current[key] !== value) changed[key] = value
  }

  try {
    await ElMessageBox.confirm(
      '把当前的风格、主色、背景效果、玻璃模糊、密度写成全站默认，所有没在本机单独设置过的浏览器都会跟着变。圆角滑杆与动效是本机偏好，不会写入全站。',
      '设为全站默认',
      { confirmButtonText: '设为全站默认', cancelButtonText: '取消', type: 'warning' },
    )
  } catch {
    return
  }

  // 写入全站后清掉本机这几项，让本机回到「跟随全站」（圆角滑杆保留：它没有对应的全站键）
  const clearLocal = () => {
    for (const key of ['preset', 'accent', 'background', 'blur', 'density'] as LocalAppearanceKey[]) {
      theme.resetLocal(key)
    }
  }

  if (Object.keys(changed).length === 0) {
    clearLocal()
    ElMessage.info('全站默认已经和当前外观一致')
    return
  }

  siteSaving.value = true
  try {
    await configApi.batchSet(changed)
    theme.applySiteSettings(changed)
    clearLocal()
    ElMessage.success(`已设为全站默认（更新 ${Object.keys(changed).length} 项）`)
  } catch (err: any) {
    ElMessage.error(err?.response?.data?.error || '保存全站默认失败')
  } finally {
    siteSaving.value = false
  }
}

// ===================== 管理员：页面背景图 =====================

const imageSaving = ref(false)

// 背景图直接保存为全站值（panel_background_image）。格式、10MB 上限与自动压缩沿用日志背景图那一套
function handleBackgroundUpload(file: File) {
  if (!file.type.startsWith('image/')) {
    ElMessage.warning('仅支持图片格式背景')
    return false
  }
  if (file.size > logBackgroundImageMaxBytes) {
    ElMessage.warning('背景图片不能超过 10MB')
    return false
  }
  void (async () => {
    imageSaving.value = true
    try {
      const { dataUrl } = await compressLogBackgroundImage(file)
      await configApi.batchSet({ panel_background_image: dataUrl })
      theme.applySiteSettings({ panel_background_image: dataUrl })
      // 管理员上传完多半就是想看效果：本机背景效果还不是图片时顺手切过去（只改本机，不动全站）
      if (theme.background !== 'image') {
        theme.setBackground('image')
      }
      ElMessage.success('背景图已保存为全站背景图；要让所有人默认用它，再点「设为全站默认」')
    } catch (err: any) {
      ElMessage.error(err?.response?.data?.error || '背景图保存失败，请换一张尺寸更小的图片')
    } finally {
      imageSaving.value = false
    }
  })()
  // 返回 false 阻止 el-upload 自己发请求
  return false
}

async function handleBackgroundRemove() {
  imageSaving.value = true
  try {
    await configApi.batchSet({ panel_background_image: '' })
    theme.applySiteSettings({ panel_background_image: '' })
    // 本机还停在「图片」就没东西可画了，回到跟随全站
    if (theme.isLocal('background') && theme.background === 'image') {
      theme.resetLocal('background')
    }
    ElMessage.success('已移除全站背景图')
  } catch (err: any) {
    ElMessage.error(err?.response?.data?.error || '移除背景图失败')
  } finally {
    imageSaving.value = false
  }
}

// ===================== 管理员：自定义 CSS =====================

// 编辑框草稿。打开工坊时从全站值初始化；和全站值不同时实时预览（previewCustomCss，不落库）
const cssDraft = ref('')
const cssSaving = ref(false)
const cssDirty = computed(() => cssDraft.value !== theme.site.customCss)

watch(cssDraft, (value) => {
  if (!ui.studioOpen || !isAdmin.value) return
  theme.previewCustomCss(value !== theme.site.customCss ? value : null)
})

function appendSnippet(css: string) {
  cssDraft.value = cssDraft.value.trim() ? `${cssDraft.value.trimEnd()}\n${css}` : css
}

function revertCss() {
  cssDraft.value = theme.site.customCss
  theme.previewCustomCss(null)
}

async function saveCss() {
  const css = cssDraft.value.trim()
  if (new TextEncoder().encode(css).length > CUSTOM_CSS_MAX_BYTES) {
    ElMessage.warning('自定义 CSS 超过 32KB，请精简后再保存')
    return
  }
  if (css === theme.site.customCss) {
    ElMessage.info('自定义 CSS 没有变化')
    return
  }
  cssSaving.value = true
  try {
    await configApi.batchSet({ panel_custom_css: css })
    theme.applySiteSettings({ panel_custom_css: css })
    theme.previewCustomCss(null)
    cssDraft.value = css
    ElMessage.success(css ? '自定义 CSS 已保存，对所有页面（含登录页）生效' : '已清空全站自定义 CSS')
  } catch (err: any) {
    // 服务端校验不通过时返回 400 + 中文原因（如「不允许 @import」），原样给管理员看；预览保持，方便接着改
    ElMessage.error(err?.response?.data?.error || '保存自定义 CSS 失败')
  } finally {
    cssSaving.value = false
  }
}

// ===================== 抽屉开关 =====================

function handleOpen() {
  cssDraft.value = theme.site.customCss
}

function handleClose() {
  // 没保存的自定义 CSS 只是预览：关掉工坊就结束预览，回到全站值
  if (isAdmin.value && cssDirty.value) {
    theme.previewCustomCss(null)
    ElMessage.info('未保存的自定义 CSS 已丢弃')
  }
  cssDraft.value = theme.site.customCss
}
</script>

<template>
  <el-drawer
    v-model="ui.studioOpen"
    direction="rtl"
    :size="420"
    append-to-body
    :with-header="false"
    class="dd-studio"
    modal-class="dd-studio-mask"
    aria-label="主题工坊"
    @open="handleOpen"
    @close="handleClose"
  >
    <div class="studio">
      <header class="studio-head">
        <el-icon class="studio-head__icon" :size="20"><Brush /></el-icon>
        <h3>主题工坊</h3>
        <span class="studio-head__sub">实时预览</span>
        <button type="button" class="studio-icon-btn" aria-label="关闭主题工坊" @click="ui.closeStudio()">
          <el-icon><Close /></el-icon>
        </button>
      </header>

      <div class="studio-body">
        <!-- 风格（7 套预设） -->
        <section class="studio-sec">
          <div class="studio-sec__head">
            <span class="studio-sec__title">风格</span>
            <span class="studio-scope" :class="{ 'is-local': theme.isLocal('preset') }">{{ scopeLabel('preset') }}</span>
            <button v-if="theme.isLocal('preset')" type="button" class="studio-reset" @click="theme.setPreset(null, $event)">
              恢复跟随全站
            </button>
          </div>
          <div class="studio-presets">
            <button
              v-for="item in THEME_PRESETS"
              :key="item.id"
              type="button"
              class="studio-preset"
              :class="{ 'is-active': theme.preset === item.id }"
              :aria-pressed="theme.preset === item.id"
              @click="theme.setPreset(item.id, $event)"
            >
              <span class="studio-preset__thumb" :style="presetThumbStyle(item)" aria-hidden="true">
                <i class="pv-side"></i>
                <span class="pv-main">
                  <i class="pv-bar"></i>
                  <span class="pv-cards"><i class="pv-card pv-card--accent"></i><i class="pv-card"></i></span>
                </span>
              </span>
              <b>{{ item.name }}</b>
              <small>{{ item.desc }}</small>
            </button>
          </div>
          <p class="studio-hint">点一下，从点击处展开新风格；明暗仍按顶栏按钮 / 个人设置走。</p>
        </section>

        <!-- 主色 -->
        <section class="studio-sec">
          <div class="studio-sec__head">
            <span class="studio-sec__title">主色</span>
            <span class="studio-scope" :class="{ 'is-local': theme.isLocal('accent') }">{{ scopeLabel('accent') }}</span>
            <button v-if="theme.isLocal('accent')" type="button" class="studio-reset" @click="theme.setAccent(null, $event)">
              恢复跟随全站
            </button>
          </div>
          <div class="studio-swatches">
            <button
              type="button"
              class="studio-swatch studio-swatch--auto"
              :class="{ 'is-active': !theme.isLocal('accent') }"
              title="跟随（全站没设主色时用风格自带的主色）"
              aria-label="主色跟随全站"
              @click="theme.setAccent(null, $event)"
            >
              A
            </button>
            <button
              v-for="color in ACCENT_SWATCHES"
              :key="color"
              type="button"
              class="studio-swatch"
              :class="{ 'is-active': isAccentActive(color) }"
              :style="{ '--sw': color }"
              :aria-label="`主色 ${color}`"
              @click="theme.setAccent(color, $event)"
            ></button>
            <!-- 自定义取色：选完（change）才提交一次，拖动取色盘时不反复写存储 -->
            <label
              class="studio-swatch studio-swatch--pick"
              :class="{ 'is-active': isCustomAccentActive }"
              :style="isCustomAccentActive ? { '--sw': theme.accent } : undefined"
              title="自定义主色"
            >
              <input type="color" :value="theme.accent || '#7c3aed'" aria-label="自定义主色" @change="handleCustomAccent" />
            </label>
          </div>
        </section>

        <!-- 背景效果 -->
        <section class="studio-sec">
          <div class="studio-sec__head">
            <span class="studio-sec__title">背景效果</span>
            <span class="studio-scope" :class="{ 'is-local': theme.isLocal('background') }">{{ scopeLabel('background') }}</span>
            <button v-if="theme.isLocal('background')" type="button" class="studio-reset" @click="theme.setBackground(null, $event)">
              恢复跟随全站
            </button>
          </div>
          <div class="dd-seg-group studio-seg" role="radiogroup" aria-label="背景效果">
            <button
              v-for="option in backgroundOptions"
              :key="option.value"
              type="button"
              role="radio"
              class="dd-seg-btn"
              :class="{ 'is-active': theme.background === option.value, 'is-unavailable': option.value === 'image' && !theme.backgroundImage }"
              :aria-checked="theme.background === option.value"
              @click="handleBackgroundClick(option.value, $event)"
            >
              {{ option.label }}
            </button>
          </div>
        </section>

        <!-- 玻璃模糊 -->
        <section class="studio-sec">
          <div class="studio-sec__head">
            <span class="studio-sec__title">玻璃模糊</span>
            <span class="studio-scope" :class="{ 'is-local': theme.isLocal('blur') }">{{ scopeLabel('blur') }}</span>
            <button v-if="theme.isLocal('blur')" type="button" class="studio-reset" @click="theme.setBlur(null)">
              恢复跟随全站
            </button>
          </div>
          <div class="studio-range">
            <el-slider
              :model-value="theme.blur"
              :min="0"
              :max="40"
              :show-tooltip="false"
              aria-label="玻璃模糊"
              @update:model-value="(value: number | number[]) => theme.setBlur(Number(value))"
            />
            <output>{{ theme.blur }}px</output>
          </div>
          <p v-if="blurHint" class="studio-hint">{{ blurHint }}；拖动滑杆可以在本机强制打开。</p>
        </section>

        <!-- 圆角 -->
        <section class="studio-sec">
          <div class="studio-sec__head">
            <span class="studio-sec__title">圆角</span>
            <span class="studio-scope" :class="{ 'is-local': theme.isLocal('radius') }">{{ scopeLabel('radius') }}</span>
            <button v-if="theme.isLocal('radius')" type="button" class="studio-reset" @click="theme.setRadius(null)">
              恢复跟随全站
            </button>
          </div>
          <div class="studio-range">
            <el-slider
              :model-value="radiusValue"
              :min="0"
              :max="28"
              :show-tooltip="false"
              aria-label="圆角"
              @update:model-value="(value: number | number[]) => theme.setRadius(Number(value))"
            />
            <output>{{ radiusValue }}px</output>
          </div>
        </section>

        <!-- 密度 -->
        <section class="studio-sec">
          <div class="studio-sec__head">
            <span class="studio-sec__title">界面密度</span>
            <span class="studio-scope" :class="{ 'is-local': theme.isLocal('density') }">{{ scopeLabel('density') }}</span>
            <button v-if="theme.isLocal('density')" type="button" class="studio-reset" @click="theme.setDensity(null)">
              恢复跟随全站
            </button>
          </div>
          <div class="dd-seg-group studio-seg" role="radiogroup" aria-label="界面密度">
            <button
              v-for="option in densityOptions"
              :key="option.value"
              type="button"
              role="radio"
              class="dd-seg-btn"
              :class="{ 'is-active': theme.density === option.value }"
              :aria-checked="theme.density === option.value"
              @click="theme.setDensity(option.value)"
            >
              {{ option.label }}
            </button>
          </div>
        </section>

        <!-- 动效档位：本机偏好，没有全站值，所以「跟随」指的是跟随系统 -->
        <section class="studio-sec">
          <div class="studio-sec__head">
            <span class="studio-sec__title">动效强度</span>
            <span class="studio-scope" :class="{ 'is-local': theme.motion !== 'system' }">
              {{ theme.motion === 'system' ? '跟随系统' : '本机' }}
            </span>
            <button v-if="theme.motion !== 'system'" type="button" class="studio-reset" @click="theme.setMotion('system')">
              恢复跟随系统
            </button>
          </div>
          <div class="dd-seg-group studio-seg" role="radiogroup" aria-label="动效强度">
            <button
              v-for="option in motionOptions"
              :key="option.value"
              type="button"
              role="radio"
              class="dd-seg-btn"
              :class="{ 'is-active': theme.motion === option.value }"
              :aria-checked="theme.motion === option.value"
              @click="theme.setMotion(option.value)"
            >
              {{ option.label }}
            </button>
          </div>
          <p class="studio-hint">「克制」保留必要过渡，关掉背景流动与光斑；「关闭」不播任何动画。</p>
        </section>

        <!-- ===== 管理员区 ===== -->
        <template v-if="isAdmin">
          <section class="studio-sec studio-admin">
            <div class="studio-sec__head">
              <span class="studio-sec__title">全站默认</span>
              <span class="studio-scope is-admin">管理员</span>
            </div>
            <p class="studio-hint studio-hint--top">
              把上面的风格、主色、背景效果、玻璃模糊、密度写成全站默认；没在本机单独设置过的浏览器都会跟着变。
            </p>
            <el-button type="primary" :loading="siteSaving" @click="handleSetSiteDefault">
              <el-icon><MagicStick /></el-icon>
              <span>设为全站默认</span>
            </el-button>
          </section>

          <section class="studio-sec studio-admin">
            <div class="studio-sec__head">
              <span class="studio-sec__title">页面背景图</span>
              <span class="studio-scope is-admin">管理员</span>
            </div>
            <div
              v-if="theme.backgroundImage"
              class="studio-bg-preview"
              :style="{ backgroundImage: `url('${theme.backgroundImage}')` }"
            ></div>
            <div class="studio-row">
              <el-upload :show-file-list="false" :before-upload="handleBackgroundUpload" accept="image/*">
                <el-button size="small" :loading="imageSaving">
                  <el-icon><Upload /></el-icon>
                  <span>{{ theme.backgroundImage ? '更换背景图' : '上传背景图' }}</span>
                </el-button>
              </el-upload>
              <el-button v-if="theme.backgroundImage" size="small" text type="danger" :disabled="imageSaving" @click="handleBackgroundRemove">
                <el-icon><Delete /></el-icon>
                <span>移除</span>
              </el-button>
            </div>
            <p class="studio-hint">上传后立即保存为全站背景图（单张最大 10MB，较大会自动压缩）；背景效果选「图片」时显示。</p>
          </section>

          <section class="studio-sec studio-admin">
            <div class="studio-sec__head">
              <span class="studio-sec__title">自定义 CSS</span>
              <span class="studio-scope is-admin">管理员 · 全站</span>
              <span v-if="cssDirty" class="studio-scope is-local">预览中，未保存</span>
            </div>
            <el-input
              v-model="cssDraft"
              type="textarea"
              class="studio-css"
              :autosize="{ minRows: 6, maxRows: 16 }"
              spellcheck="false"
              placeholder="/* 在这里写 CSS，输入即实时预览，点「保存为全站」后对所有页面生效 */"
            />
            <div class="studio-snippets">
              <button
                v-for="snippet in CSS_SNIPPETS"
                :key="snippet.name"
                type="button"
                class="studio-chip"
                @click="appendSnippet(snippet.css)"
              >
                + {{ snippet.name }}
              </button>
            </div>
            <div class="studio-row">
              <el-button type="primary" size="small" :loading="cssSaving" @click="saveCss">保存为全站</el-button>
              <el-button size="small" :disabled="!cssDirty" @click="revertCss">撤销修改</el-button>
              <el-button size="small" text @click="cssDraft = ''">清空</el-button>
            </div>
            <p class="studio-hint">
              注入到所有页面（含登录页），未登录访客也能读到，别写敏感信息。不允许 @import、外链 url()、expression()、javascript:。
            </p>
            <p v-if="theme.safeMode" class="studio-hint studio-hint--warn">
              当前处于安全模式：自定义 CSS 没有注入，预览也看不到效果。
            </p>
          </section>
        </template>

        <p class="studio-hint studio-safe-tip">
          自定义样式把页面弄乱了？在地址后加 <code>?safe=1</code> 进入安全模式，会跳过自定义 CSS。
        </p>
      </div>

      <footer class="studio-foot">
        <el-button size="small" @click="handleExport">
          <el-icon><Download /></el-icon>
          <span>导出</span>
        </el-button>
        <el-button size="small" @click="handleImport">
          <el-icon><Upload /></el-icon>
          <span>导入</span>
        </el-button>
        <el-button size="small" title="清掉本机的全部外观设置（不含动效），回到全站默认" @click="handleResetAll">
          <el-icon><RefreshLeft /></el-icon>
          <span>恢复全站默认</span>
        </el-button>
        <span class="studio-foot__spacer"></span>
        <el-button size="small" type="primary" @click="ui.closeStudio()">完成</el-button>
      </footer>
    </div>
  </el-drawer>
</template>

<style lang="scss">
// 抽屉外壳与遮罩：el-drawer 传送到 body，外壳节点不吃本组件的 scoped 属性，只能写全局样式（类名带 dd-studio 前缀避免撞车）。

// 桌面端浮在右侧、四周留 12px（原型同款），底色用浮层令牌（接近不透明，压在任何背景上都可读）
.el-overlay .el-drawer.dd-studio.rtl {
  top: 12px;
  bottom: 12px;
  right: 12px;
  height: auto;
  max-width: calc(100vw - 24px);
  border-radius: var(--dd-radius-surface);
  border: var(--dd-border-width) solid var(--dd-border);
  background: var(--dd-overlay);
  box-shadow: var(--dd-shadow-popper);
  overflow: hidden;
  // 滑入用柔和弹簧（时长仍走令牌，关闭档 / 减少动效时自动压到 1ms）
  transition: transform var(--dd-motion-overlay) var(--dd-spring-soft) !important;

  .el-drawer__body {
    padding: 0;
    display: flex;
    flex-direction: column;
    overflow: hidden;
  }
}

// 进出场多推 24px，免得右侧留着的 12px 间距在动画起点露出一条边
.el-drawer-fade-enter-from .el-drawer.dd-studio.rtl,
.el-drawer-fade-leave-to .el-drawer.dd-studio.rtl {
  transform: translate3d(calc(100% + 24px), 0, 0) !important;
}

// 遮罩只轻轻压一层：工坊是「实时预览」，要让人看得清背后的页面怎么变
// !important 不能省：global.scss 的 .el-overlay 底色带了 !important，不加的话这条永远不生效、遮罩还是那层 24% 深色
.el-overlay.dd-studio-mask {
  background-color: color-mix(in srgb, var(--dd-bg) 22%, transparent) !important;
}

@media (max-width: 768px) {
  // 手机上全局规则已把抽屉拉满宽度，这里去掉四周留白与圆角；
  // 贴紧屏幕右边缘时改用减速曲线，避免 --dd-spring-soft 过冲在右侧露出背景缝隙
  .el-overlay .el-drawer.dd-studio.rtl {
    top: 0;
    bottom: 0;
    right: 0;
    max-width: none;
    border-radius: 0;
    border: none;
    transition: transform var(--dd-motion-overlay) var(--dd-ease-decelerate) !important;
  }
}
</style>

<style scoped lang="scss">
.studio {
  display: flex;
  flex-direction: column;
  height: 100%;
  min-height: 0;
  color: var(--dd-text);
  font-family: var(--dd-font-ui);
}

.studio-head {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 18px 20px;
  border-bottom: 1px solid var(--dd-divider, var(--dd-border-strong));

  h3 {
    flex: 1;
    margin: 0;
    font-family: var(--dd-font-head, inherit);
    font-size: 17px;
    font-weight: 700;
    color: var(--dd-text);
  }
}

.studio-head__icon {
  color: var(--dd-accent);
}

.studio-head__sub {
  font-size: 12px;
  color: var(--dd-text-3);
}

.studio-icon-btn {
  display: inline-grid;
  place-items: center;
  width: 30px;
  height: 30px;
  border: none;
  border-radius: var(--dd-radius-control);
  background: transparent;
  color: var(--dd-text-2);
  cursor: pointer;
  transition: background-color var(--dd-motion-fast) var(--dd-ease-standard);

  &:hover {
    background: var(--dd-surface-3);
    color: var(--dd-text);
  }
}

.studio-body {
  flex: 1;
  min-height: 0;
  overflow: auto;
  overscroll-behavior: contain;
  padding: 16px 20px 20px;
  display: flex;
  flex-direction: column;
  gap: 22px;
}

.studio-sec__head {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 6px 8px;
  margin-bottom: 10px;
}

.studio-sec__title {
  flex: 1;
  min-width: max-content;
  font-size: 12.5px;
  font-weight: 600;
  letter-spacing: 1px;
  color: var(--dd-text-2);
}

// 「本机 / 跟随全站」小标签：跟随全站是中性灰，本机用主色墨色点出来
.studio-scope {
  flex-shrink: 0;
  padding: 1px 8px;
  border-radius: var(--dd-radius-pill);
  font-size: 11.5px;
  line-height: 18px;
  color: var(--dd-text-3);
  background: var(--dd-surface-2);

  &.is-local {
    color: var(--dd-accent-ink);
    background: var(--dd-accent-soft);
  }

  &.is-admin {
    color: var(--dd-warn-ink);
    background: color-mix(in srgb, var(--dd-warn) 12%, transparent);
  }
}

.studio-reset {
  flex-shrink: 0;
  padding: 0;
  border: none;
  background: none;
  font: inherit;
  font-size: 12px;
  color: var(--dd-accent-ink);
  cursor: pointer;

  &:hover {
    text-decoration: underline;
  }
}

.studio-hint {
  margin: 8px 0 0;
  font-size: 12px;
  line-height: 1.6;
  color: var(--dd-text-3);

  code {
    font-family: var(--dd-font-mono);
    color: var(--dd-text-2);
  }
}

.studio-hint--top {
  margin: 0 0 10px;
}

.studio-hint--warn {
  color: var(--dd-warn-ink);
}

.studio-safe-tip {
  margin-top: -6px;
}

// ===== 预设卡片 =====
.studio-presets {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 10px;
}

.studio-preset {
  display: block;
  padding: 8px;
  border-radius: var(--dd-radius-surface);
  border: 1.5px solid var(--dd-divider, var(--dd-border-strong));
  background: var(--dd-surface-2);
  color: var(--dd-text);
  font: inherit;
  text-align: left;
  cursor: pointer;
  transition:
    transform var(--dd-motion-normal) var(--dd-spring),
    border-color var(--dd-motion-fast) var(--dd-ease-standard),
    box-shadow var(--dd-motion-fast) var(--dd-ease-standard);

  &:hover {
    transform: translateY(-3px);
  }

  &.is-active {
    border-color: var(--dd-accent);
    box-shadow: 0 0 0 4px var(--dd-accent-soft);
  }

  b {
    display: block;
    margin-top: 8px;
    font-size: 13px;
  }

  small {
    display: block;
    font-size: 11.5px;
    line-height: 1.4;
    color: var(--dd-text-3);
  }
}

// 缩略图：颜色全部来自 THEME_PRESETS 的 swatch（数据），不读当前主题令牌，否则七张卡长得一样
.studio-preset__thumb {
  position: relative;
  display: grid;
  grid-template-columns: 22% 1fr;
  gap: 5px;
  height: 74px;
  padding: 6px;
  overflow: hidden;
  border-radius: var(--dd-radius-control);
  background: var(--pv-bg);

  &::before {
    content: '';
    position: absolute;
    left: -10%;
    top: -40%;
    width: 70%;
    height: 120%;
    background: radial-gradient(closest-side, var(--pv-a), transparent);
    opacity: var(--pv-glow);
  }

  i {
    position: relative;
    display: block;
    border-radius: 4px;
    background: var(--pv-s);
  }
}

.pv-main {
  position: relative;
  display: grid;
  grid-template-rows: 12px 1fr;
  gap: 5px;
}

.studio-preset__thumb .pv-bar {
  width: 55%;
  background: linear-gradient(90deg, var(--pv-a), var(--pv-a2));
}

.pv-cards {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 5px;
}

.studio-preset__thumb .pv-card--accent {
  width: 60%;
  background: linear-gradient(160deg, var(--pv-a), var(--pv-a2));
}

// ===== 主色色块 =====
.studio-swatches {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px;
}

.studio-swatch {
  position: relative;
  display: inline-grid;
  place-items: center;
  width: 30px;
  height: 30px;
  padding: 0;
  border: none;
  border-radius: 50%;
  background: var(--sw);
  box-shadow: inset 0 0 0 2px color-mix(in srgb, var(--dd-surface-solid) 30%, transparent);
  cursor: pointer;
  transition: transform var(--dd-motion-normal) var(--dd-spring);

  &:hover {
    transform: scale(1.12);
  }

  &.is-active {
    box-shadow:
      0 0 0 2px var(--dd-surface-solid),
      0 0 0 4px var(--sw, var(--dd-accent));
  }
}

// 「跟随」色块：用当前生效的主色与副色画一圈渐变
.studio-swatch--auto {
  background: conic-gradient(var(--dd-accent), var(--dd-accent-2), var(--dd-accent));
  color: var(--dd-on-accent);
  font-size: 11px;
  font-weight: 700;
}

.studio-swatch--pick {
  overflow: hidden;
  background: conic-gradient(from 90deg, #f43f5e, #f59e0b, #10b981, #0ea5e9, #8b5cf6, #f43f5e);

  input {
    position: absolute;
    inset: 0;
    width: 100%;
    height: 100%;
    opacity: 0;
    cursor: pointer;
  }
}

// ===== 分段控件 / 滑杆 =====
.studio-seg {
  max-width: 100%;
  // 保持单行不折行、窄屏横向平滑滑动，避免素白（clean）胶囊分段折成两行变巨型椭圆
  flex-wrap: nowrap;
  overflow-x: auto;
  overflow-y: hidden;
  overscroll-behavior-x: auto;
  scrollbar-width: none;

  &::-webkit-scrollbar {
    display: none;
  }

  // 未上传背景图时的「图片」项：不用 opacity 二次降对比（素白亮色会跌至 2.31:1），
  // 改用次要文字色（全 14 主题 ≥ 5.8:1）配虚线边框区分待配置态
  .dd-seg-btn.is-unavailable {
    color: var(--dd-text-2);
    border-style: dashed;
    border-color: var(--dd-border-strong);
  }
}

.studio-range {
  display: flex;
  align-items: center;
  gap: 14px;
  padding-left: 6px;

  .el-slider {
    flex: 1;
  }

  output {
    width: 44px;
    text-align: right;
    font-family: var(--dd-font-mono);
    font-size: 12px;
    color: var(--dd-text-2);
  }
}

// ===== 管理员区 =====
.studio-admin {
  padding-top: 18px;
  border-top: 1px dashed var(--dd-border-strong);
}

.studio-row {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px;
  margin-top: 10px;

  // EP 按钮相邻时自带 12px 左外边距，这里统一由 gap 控制
  :deep(.el-button + .el-button) {
    margin-left: 0;
  }
}

.studio-bg-preview {
  height: 96px;
  border-radius: var(--dd-radius-control);
  border: 1px solid var(--dd-divider, var(--dd-border-strong));
  background-color: var(--dd-surface-2);
  background-size: cover;
  background-position: center;
}

.studio-css :deep(.el-textarea__inner) {
  font-family: var(--dd-font-mono);
  font-size: 12px;
  line-height: 1.6;
}

.studio-snippets {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  margin-top: 8px;
}

.studio-chip {
  padding: 3px 10px;
  border-radius: var(--dd-radius-pill);
  border: 1px solid var(--dd-divider, var(--dd-border-strong));
  background: var(--dd-surface-2);
  color: var(--dd-text-2);
  font: inherit;
  font-size: 12px;
  cursor: pointer;
  transition:
    border-color var(--dd-motion-fast) var(--dd-ease-standard),
    color var(--dd-motion-fast) var(--dd-ease-standard);

  &:hover {
    border-color: var(--dd-accent);
    color: var(--dd-accent-ink);
  }
}

// ===== 底栏 =====
.studio-foot {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px;
  padding: 14px 20px;
  border-top: 1px solid var(--dd-divider, var(--dd-border-strong));

  // EP 按钮相邻时自带 12px 左外边距，这里统一用 gap
  :deep(.el-button + .el-button) {
    margin-left: 0;
  }
}

.studio-foot__spacer {
  flex: 1;
}

@media (max-width: 768px) {
  .studio-head {
    padding: 14px 16px;
  }

  .studio-body {
    padding: 14px 16px 16px;
  }

  // 移动端底栏收紧间距并补底部安全区，避免窄屏折行或被底部手势条遮挡
  .studio-foot {
    gap: 6px;
    padding: 12px 16px calc(12px + env(safe-area-inset-bottom));
  }
}

@media (max-width: 420px) {
  .studio-foot {
    gap: 4px;
    padding: 10px 12px calc(12px + env(safe-area-inset-bottom));

    :deep(.el-button--small) {
      padding: 5px 8px;
    }
  }
}
</style>
