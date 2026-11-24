<template>
  <span class="dd-field-help">
    <span class="dd-field-help__label">{{ label }}</span>
    <!-- trigger 只用 click，不要 hover：
         - 触屏上 hover 靠模拟 mouseenter 打开，iOS Safari 上开关都不稳；
         - 同时写 ['hover','click'] 时鼠标移入打开、紧接着点击又 toggle 关掉，两者互相打架；
         - EP 的「点外面关闭」只在 trigger 不含 hover / focus 时才会真的关。
         trigger-keys 置空：键盘交给原生 <button> 的 Enter / Space 激活（合成的 click 同样 button === 0，
         EP 的 onClick 照样 toggle）。留着默认值的话 keydown 里 toggle 一次，个别浏览器在 Space 的 keyup
         上还会再补一次原生 click，按一下等于开了又关。
         触发元素必须是 <button type="button">：
         - el-form-item 里只有一个输入控件时，EP 把标签渲染成 <label for=输入框id>。
           点 label 里的普通 span 会把焦点转给输入框（手机上直接弹键盘）；
           按 HTML 规范，点 label 内的交互元素（button）不会触发这次转发。
         - el-form 渲染的是 <form>，按钮不写 type 默认是 submit，点一下就提交表单。
         （这段注释刻意不放进 #reference 插槽：那里只该有触发元素这一个节点，交给 EP 的 OnlyChild 去挂事件。）
         before-enter / before-leave 挂拆 Esc 监听，理由见脚本里 onEscapeKeydown 的注释。 -->
    <el-popover
      ref="popoverRef"
      trigger="click"
      :trigger-keys="triggerKeys"
      placement="top-start"
      :width="width"
      :popper-style="popperStyle"
      @before-enter="listenEscape"
      @before-leave="unlistenEscape"
    >
      <template #reference>
        <button
          type="button"
          class="dd-field-help__trigger"
          :aria-label="`${label}说明`"
        >
          <el-icon><QuestionFilled /></el-icon>
        </button>
      </template>
      <div class="dd-field-help__body"><slot /></div>
    </el-popover>
  </span>
</template>

<script setup lang="ts">
// 表单字段标签 + 「?」帮助气泡（v3.2.9，订阅弹窗精简时新增）。
// 用来替代控件下方常驻的灰字说明段落：正文走默认插槽，只放一两句「用途 + 最容易踩的坑」，
// 完整规则留给文档与日志，不要往气泡里塞长段落。用法：
//   <el-form-item><template #label><DdFieldHelp label="白名单">一两句说明</DdFieldHelp></template>…</el-form-item>

import { onBeforeUnmount, ref } from 'vue'
// 全局注册的图标里没有 QuestionFilled，这里按仓库既有做法（tasks/index.vue 同款）局部引入
import { QuestionFilled } from '@element-plus/icons-vue'

withDefaults(
  defineProps<{
    /** 标签文字；同时拼出按钮的读屏名称「<标签>说明」（按钮里只有图标，没有可读文字） */
    label: string
    /** 气泡宽度（数字按 px）。窄屏另有 maxWidth 兜底，不会顶出视口 */
    width?: number | string
  }>(),
  {
    width: 260,
  }
)

// 键盘开关交给原生 button，理由见模板里 el-popover 上方的注释
const triggerKeys: string[] = []

// EP 把它和 width 拼成 [{ width }, popperStyle]，maxWidth 比 width 小时生效：
// 375px 宽的手机上 260px 的气泡从标签处往右展开也不会伸出屏幕。
// 两个都提成常量，免得每次渲染都新建数组 / 对象、让 popover 的 props 白白 diff 一遍。
const popperStyle = { maxWidth: 'calc(100vw - 32px)' }

// Esc 先关气泡、不连带关掉所在的弹窗。
// EP 的 click 型 popover 自己不响应 Esc：内容层的焦点陷阱只在焦点进过气泡时才生效，而气泡里没有可聚焦元素。
// el-dialog 的 Esc 却挂在 document 上、与焦点无关，于是按 Esc 时弹窗关了、teleport 到 body 的气泡还开着，
// 飘在列表页上，直到下一次点击才消失；只想关气泡的用户也会把整个弹窗连同没保存的输入一起关掉。
// 所以只在气泡打开期间，在 document 的捕获阶段接住 Esc：关气泡并停止传播（弹窗的监听在冒泡阶段，收不到）。
// 输入法组字中的 Esc 是取消组字，不当成关气泡。
const popoverRef = ref<{ hide: () => void } | null>(null)

function onEscapeKeydown(event: KeyboardEvent) {
  if (event.key !== 'Escape' || event.isComposing) return
  event.stopPropagation()
  popoverRef.value?.hide()
}

// 同一个函数引用重复 add / remove 都是幂等的，打开途中又被关掉、关到一半又被打开都不会挂多份
function listenEscape() {
  document.addEventListener('keydown', onEscapeKeydown, true)
}

function unlistenEscape() {
  document.removeEventListener('keydown', onEscapeKeydown, true)
}

// 打开着的气泡随所在页面一起卸载时，before-leave 不会再来，这里兜底拆掉
onBeforeUnmount(unlistenEscape)
</script>

<style scoped lang="scss">
.dd-field-help {
  display: inline-flex;
  align-items: center;
}

/* 「?」按钮：只有一枚 14px 图标，底色与边框清零，颜色走次级文字令牌。
   点击热区用「内边距 + 等量负外边距」放大到 22px：右侧与上下的负外边距把多出来的尺寸抵掉，
   不改变标签的排版宽度（只多占左侧 4px，正好当作图标与文字的间距）。
   订阅弹窗桌面端标签宽 104px 就是按「5 个汉字 70px + 这里的 18px + 标签右内边距 12px」算的，改尺寸要回去同步。 */
.dd-field-help__trigger {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
  margin: -4px -4px -4px 0;
  padding: 4px;
  border: 0;
  // 只有键盘焦点框会露出形状；14px 的按钮 >12px，按规范吃 control 档
  border-radius: var(--dd-radius-control);
  background: transparent;
  color: var(--el-text-color-secondary);
  font-size: 14px;
  line-height: 1;
  cursor: pointer;
  transition: color var(--dd-motion-fast) var(--dd-ease-standard);

  // hover 只改颜色，不位移、不缩放（design-system 硬规则 2）
  &:hover {
    color: var(--dd-accent-ink);
  }

  &:focus-visible {
    outline: 2px solid var(--el-color-primary);
    outline-offset: 1px;
  }
}

.dd-field-help__body {
  font-size: 13px;
  line-height: 1.6;
  color: var(--el-text-color-regular);
  // 断行不用另写：EP 2.13 的 .el-popover.el-popper 自带 overflow-wrap: break-word，
  // 正则、路径这类不含空格的长串（^jd[^_]、scripts/daily）放不下时会自己断开。
  // 别照 global.scss 里暗色 tooltip（.el-popper.is-dark）那条加 word-break: break-all——
  // 那会把 sendNotify 这类普通英文词也从中间劈开；气泡是浅色 popover，不受那条影响。
}
</style>
