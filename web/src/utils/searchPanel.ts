import { EditorSelection } from '@codemirror/state'
import type { EditorState, StateEffect } from '@codemirror/state'
import { EditorView, runScopeHandlers } from '@codemirror/view'
import type { Panel, ViewUpdate } from '@codemirror/view'
import {
  SearchQuery,
  closeSearchPanel,
  findNext,
  findPrevious,
  getSearchQuery,
  replaceAll,
  replaceNext,
  setSearchQuery
} from '@codemirror/search'

/**
 * CodeMirror 编辑器的中文查找面板（issue #170），给 `search({ top: true, createPanel })` 用。
 *
 * 为什么不用自带面板：英文标签；输入框和按钮是基础主题里的 `.cm-textfield` / `.cm-button`，
 * 写死 70% 字号（14px 编辑器下约 10px，就是 issue 说的「字很小」）；没有匹配计数；
 * 「all」（选中全部匹配）在这里没意义 —— 本编辑器刻意没开 drawSelection，多选区画不出来。
 * 所以样式全部用自己的类名（.cm-dd-search*），不复用那两个类，也就不受 70% 字号影响。
 *
 * 守「自建 CodeMirror 扩展的铁律」：面板 DOM 交给 CodeMirror 的 panels 容器托管（挂在 .cm-editor 下、
 * 正文区外面），一个节点都不往 contentDOM 里放，也不用 widget，原生选区不受影响。
 *
 * 查询本身仍是 @codemirror/search 的 SearchQuery / setSearchQuery，
 * 匹配高亮、F3 / Ctrl+G、Esc 关闭这些都沿用官方实现，这里只换了面板这一层。
 */

/** 计数上限：大文件里匹配太多时数到这里就停，显示「1000+」，避免每次输入都扫完整篇。 */
const MAX_MATCH_COUNT = 1000

// 替换行展开状态：模块级变量，本次打开的页面里所有编辑器共用、关掉面板再开也记得；
// 刻意不存 localStorage，刷新后回到默认收起（与青龙 / Monaco 的习惯一致）。
let replaceExpanded = false

export function createSearchPanel(view: EditorView): Panel {
  // 面板当前持有的查询；自己 commit 时先改它再 dispatch，update 里据此区分「别处发来的查询」
  let query = getSearchQuery(view.state)
  // 当前查询的匹配位置（最多 MAX_MATCH_COUNT 个）。只在文档或查询变了时重扫，
  // 光标移动只重算「当前第几个」，长文件里按方向键不会每次都整篇扫一遍
  let matches: { from: number; to: number }[] = []
  let capped = false

  // 按钮统一的建法：带 title + aria-label；mousedown 时 preventDefault，
  // 焦点留在输入框里，点完「下一个」还能接着按 Enter
  function makeButton(className: string, text: string, label: string) {
    const button = document.createElement('button')
    button.type = 'button'
    button.className = `cm-dd-search-btn ${className}`
    button.textContent = text
    button.title = label
    button.setAttribute('aria-label', label)
    button.addEventListener('mousedown', (e) => e.preventDefault())
    return button
  }

  // 输入框：form="" 让它不归属外层任何 <form>；移动端关掉自动大写 / 纠错 / 拼写检查，
  // 理由与编辑器正文那三条相同（输入法会静默改掉要查的内容）
  function makeInput(label: string) {
    const input = document.createElement('input')
    input.className = 'cm-dd-search-input'
    input.placeholder = label
    input.setAttribute('aria-label', label)
    input.setAttribute('form', '')
    input.setAttribute('autocomplete', 'off')
    input.setAttribute('autocapitalize', 'off')
    input.setAttribute('autocorrect', 'off')
    input.spellcheck = false
    return input
  }

  const expandButton = makeButton('cm-dd-search-expand', '▸', '展开替换')
  const searchField = makeInput('查找')
  // openSearchPanel / Ctrl+F 靠 [main-field] 找到这个输入框去聚焦、带入选中文字
  searchField.setAttribute('main-field', 'true')
  const status = document.createElement('span')
  status.className = 'cm-dd-search-status'
  status.setAttribute('aria-live', 'polite')
  const caseButton = makeButton('cm-dd-search-toggle', 'Aa', '区分大小写')
  const wordButton = makeButton('cm-dd-search-toggle cm-dd-search-word', 'ab', '全词匹配')
  const regexpButton = makeButton('cm-dd-search-toggle', '.*', '使用正则表达式')
  const prevButton = makeButton('', '↑', '上一个匹配（Shift+Enter）')
  const nextButton = makeButton('', '↓', '下一个匹配（Enter）')
  const closeButton = makeButton('cm-dd-search-close', '×', '关闭（Esc）')

  const replaceField = makeInput('替换')
  const replaceButton = makeButton('is-text', '替换', '替换')
  const replaceAllButton = makeButton('is-text', '全部替换', '全部替换')

  const searchRow = document.createElement('div')
  searchRow.className = 'cm-dd-search-row'
  searchRow.append(
    expandButton,
    searchField,
    status,
    caseButton,
    wordButton,
    regexpButton,
    prevButton,
    nextButton,
    closeButton
  )
  const replaceRow = document.createElement('div')
  replaceRow.className = 'cm-dd-search-row cm-dd-search-replace-row'
  replaceRow.append(replaceField, replaceButton, replaceAllButton)

  const dom = document.createElement('div')
  dom.className = 'cm-dd-search'
  dom.append(searchRow, replaceRow)

  const isPressed = (button: HTMLButtonElement) => button.getAttribute('aria-pressed') === 'true'

  // 把 query 同步回输入框和三个切换（构造时、以及别处发来 setSearchQuery 时）
  function syncFields() {
    searchField.value = query.search
    replaceField.value = query.replace
    caseButton.setAttribute('aria-pressed', String(query.caseSensitive))
    wordButton.setAttribute('aria-pressed', String(query.wholeWord))
    regexpButton.setAttribute('aria-pressed', String(query.regexp))
  }

  // 只读时箭头和替换行都不显示；可编辑时替换行跟着展开状态走。
  // 用 style.display 而不是 hidden 属性：类名上的 display: flex 会压过 [hidden] 的 UA 样式
  function syncReplaceVisibility(readOnly: boolean) {
    const label = replaceExpanded ? '收起替换' : '展开替换'
    expandButton.style.display = readOnly ? 'none' : ''
    expandButton.textContent = replaceExpanded ? '▾' : '▸'
    expandButton.title = label
    expandButton.setAttribute('aria-label', label)
    expandButton.setAttribute('aria-expanded', String(replaceExpanded))
    replaceRow.style.display = readOnly || !replaceExpanded ? 'none' : ''
  }

  function recount(state: EditorState) {
    matches = []
    capped = false
    // 查询无效（查找框空、正则写错）时不扫
    if (!query.valid) return
    const cursor = query.getCursor(state)
    for (let next = cursor.next(); !next.done; next = cursor.next()) {
      if (matches.length >= MAX_MATCH_COUNT) {
        capped = true
        break
      }
      matches.push({ from: next.value.from, to: next.value.to })
    }
  }

  function renderStatus(state: EditorState) {
    status.classList.remove('is-error')
    // 查找框为空：不显示任何状态
    if (!query.search) {
      status.textContent = ''
      status.style.display = 'none'
      return
    }
    status.style.display = ''
    // 非正则且查找框非空时 valid 恒为 true，所以这里只会是正则写错
    if (!query.valid && query.regexp) {
      status.textContent = '正则有误'
      status.classList.add('is-error')
      return
    }
    if (matches.length === 0) {
      status.textContent = '无结果'
      status.classList.add('is-error')
      return
    }
    const total = capped ? `${MAX_MATCH_COUNT}+` : String(matches.length)
    // 「当前项」= 起止位置与主选区完全相同的那个匹配；光标不在任何匹配上时只显示总数
    const { from, to } = state.selection.main
    const index = matches.findIndex((match) => match.from === from && match.to === to)
    status.textContent = index >= 0 ? `第 ${index + 1} 个，共 ${total} 个` : `共 ${total} 个`
  }

  // 输入或切换后生成新查询；locate = true（查找框内容变了）时边打字边定位到匹配，像 Monaco / 青龙那样
  function commit(locate: boolean) {
    const next = new SearchQuery({
      search: searchField.value,
      caseSensitive: isPressed(caseButton),
      regexp: isPressed(regexpButton),
      wholeWord: isPressed(wordButton),
      replace: replaceField.value
    })
    if (next.eq(query)) return
    query = next
    const effects: StateEffect<unknown>[] = [setSearchQuery.of(next)]
    if (locate && next.valid) {
      const state = view.state
      // 从主选区的 from 起往后找（继续打字时当前匹配原地变长，不会跳走）；后面没有就从文档开头绕回
      let found = next.getCursor(state, state.selection.main.from).next()
      if (found.done) found = next.getCursor(state).next()
      if (!found.done) {
        const range = EditorSelection.range(found.value.from, found.value.to)
        effects.push(EditorView.scrollIntoView(range, { y: 'center' }))
        view.dispatch({
          selection: EditorSelection.create([range]),
          effects,
          userEvent: 'select.search'
        })
        return
      }
    }
    view.dispatch({ effects })
  }

  // 输入法组字过程中（拼音还没上屏）不提交，等 compositionend 再提交；两者都触发时 eq 判等会挡掉重复的那次
  searchField.addEventListener('input', (e) => {
    if ((e as InputEvent).isComposing) return
    commit(true)
  })
  searchField.addEventListener('compositionend', () => commit(true))
  replaceField.addEventListener('input', (e) => {
    if ((e as InputEvent).isComposing) return
    commit(false)
  })
  replaceField.addEventListener('compositionend', () => commit(false))

  for (const toggle of [caseButton, wordButton, regexpButton]) {
    toggle.addEventListener('click', () => {
      toggle.setAttribute('aria-pressed', String(!isPressed(toggle)))
      commit(false)
    })
  }

  expandButton.addEventListener('click', () => {
    replaceExpanded = !replaceExpanded
    syncReplaceVisibility(view.state.readOnly)
  })
  prevButton.addEventListener('click', () => findPrevious(view))
  nextButton.addEventListener('click', () => findNext(view))
  closeButton.addEventListener('click', () => closeSearchPanel(view))
  replaceButton.addEventListener('click', () => replaceNext(view))
  replaceAllButton.addEventListener('click', () => replaceAll(view))

  dom.addEventListener('keydown', (e) => {
    // 组字中的 Enter / Esc 是给输入法的（确认候选词 / 取消组字），不能当成「下一个」「关闭」
    if (e.isComposing || e.keyCode === 229) return
    // 先交给 search-panel 作用域的快捷键：Esc 关闭、F3 / Ctrl+G 下一个、Shift 版上一个、Ctrl+F 等
    if (runScopeHandlers(view, e, 'search-panel')) {
      e.preventDefault()
      return
    }
    if (e.key === 'Enter' && e.target === searchField) {
      e.preventDefault()
      if (e.shiftKey) findPrevious(view)
      else findNext(view)
    } else if (e.key === 'Enter' && e.target === replaceField) {
      e.preventDefault()
      replaceNext(view)
    }
  })

  syncFields()
  syncReplaceVisibility(view.state.readOnly)
  recount(view.state)
  renderStatus(view.state)

  return {
    dom,
    // 必须自己写 top：自定义面板不会去读 search({ top }) 的配置，漏了面板会跑到编辑器底部
    top: true,
    mount() {
      searchField.select()
    },
    update(u: ViewUpdate) {
      // 别处发来的查询（例如再按一次 Ctrl+F 带入选中文字）：同步回输入框与切换按钮
      for (const tr of u.transactions) {
        for (const effect of tr.effects) {
          if (effect.is(setSearchQuery) && !effect.value.eq(query)) {
            query = effect.value
            syncFields()
          }
        }
      }
      const queryChanged = getSearchQuery(u.state) !== getSearchQuery(u.startState)
      if (u.docChanged || queryChanged) recount(u.state)
      if (u.docChanged || queryChanged || u.selectionSet) renderStatus(u.state)
      // 只读切换（脚本页进入 / 退出编辑）时箭头和替换行跟着显隐
      if (u.startState.readOnly !== u.state.readOnly) syncReplaceVisibility(u.state.readOnly)
    }
  }
}

/**
 * 查找面板样式。配色只用 --el-* 令牌，不跟编辑器底色走（用户可把底色调成任意颜色，面板仍要可读），
 * 写法参照 utils/codeEditor.ts 里 .cm-panels 那几条；桌面 13px，≤768px 的 16px 在 CodeMirrorEditor.vue 里。
 */
export const searchPanelTheme = EditorView.baseTheme({
  '.cm-dd-search': {
    display: 'flex',
    flexDirection: 'column',
    gap: '6px',
    padding: '6px 8px',
    fontFamily: 'var(--dd-font-ui)',
    fontSize: '13px',
    lineHeight: '1.4'
  },
  '.cm-dd-search-row': {
    display: 'flex',
    // 窄屏下按钮折到下一行，不撑出横向滚动
    flexWrap: 'wrap',
    alignItems: 'center',
    gap: '4px',
    minWidth: '0'
  },
  // 替换框与查找框左对齐：让出箭头按钮（26px）+ 间距（4px）
  '.cm-dd-search-replace-row': {
    paddingLeft: '30px'
  },
  '.cm-dd-search-input': {
    flex: '1 1 160px',
    minWidth: '0',
    maxWidth: '320px',
    height: '26px',
    boxSizing: 'border-box',
    margin: '0',
    padding: '0 8px',
    font: 'inherit',
    fontSize: '13px',
    color: 'var(--el-text-color-primary)',
    backgroundColor: 'var(--el-fill-color-blank)',
    border: '1px solid var(--el-border-color)',
    borderRadius: 'var(--dd-radius-control)',
    outline: 'none'
  },
  '.cm-dd-search-input:focus': {
    borderColor: 'var(--el-color-primary)'
  },
  '.cm-dd-search-input::placeholder': {
    color: 'var(--el-text-color-placeholder)'
  },
  '.cm-dd-search-status': {
    padding: '0 6px',
    whiteSpace: 'nowrap',
    color: 'var(--el-text-color-secondary)',
    fontVariantNumeric: 'tabular-nums'
  },
  '.cm-dd-search-status.is-error': {
    color: 'var(--el-color-danger)'
  },
  '.cm-dd-search-btn': {
    display: 'inline-flex',
    alignItems: 'center',
    justifyContent: 'center',
    flex: '0 0 auto',
    minWidth: '26px',
    height: '26px',
    boxSizing: 'border-box',
    margin: '0',
    padding: '0 6px',
    font: 'inherit',
    fontSize: '13px',
    lineHeight: '1',
    whiteSpace: 'nowrap',
    color: 'var(--el-text-color-regular)',
    backgroundColor: 'transparent',
    backgroundImage: 'none',
    border: '1px solid transparent',
    borderRadius: 'var(--dd-radius-control)',
    cursor: 'pointer'
  },
  '.cm-dd-search-btn:hover': {
    color: 'var(--el-text-color-primary)',
    backgroundColor: 'var(--el-fill-color-light)'
  },
  '.cm-dd-search-btn:focus-visible': {
    outline: '2px solid var(--el-color-primary)',
    outlineOffset: '-2px'
  },
  // 切换按钮的激活态
  '.cm-dd-search-btn[aria-pressed="true"]': {
    color: 'var(--el-color-primary)',
    backgroundColor: 'var(--el-color-primary-light-9)',
    borderColor: 'var(--el-color-primary-light-5)'
  },
  '.cm-dd-search-word': {
    textDecoration: 'underline',
    textUnderlineOffset: '2px'
  },
  // 「替换」「全部替换」两个文字按钮带边框，和图标按钮区分开
  '.cm-dd-search-btn.is-text': {
    padding: '0 10px',
    color: 'var(--el-text-color-primary)',
    backgroundColor: 'var(--el-fill-color-light)',
    borderColor: 'var(--el-border-color)'
  },
  '.cm-dd-search-btn.is-text:hover': {
    borderColor: 'var(--el-color-primary)'
  },
  // 关闭按钮靠右；换行后也停在所在行的最右边
  '.cm-dd-search-close': {
    marginLeft: 'auto',
    fontSize: '16px'
  }
})
