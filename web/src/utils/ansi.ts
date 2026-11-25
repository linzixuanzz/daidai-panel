// 16 色与 256 色基础槽位输出 CSS 变量（var(--dd-ansi-*)），由 global.scss 根据日志底色明暗
// （html[data-dd-log-tone="light|dark"]）切换亮底加深版 / 暗底提亮版调色板，避免浅底黄白字近乎隐身。
const ANSI_COLORS: Record<number, string> = {
  30: 'var(--dd-ansi-30, #4d4d4d)', 31: 'var(--dd-ansi-31, #cd3131)', 32: 'var(--dd-ansi-32, #0dbc79)', 33: 'var(--dd-ansi-33, #e5e510)',
  34: 'var(--dd-ansi-34, #2472c8)', 35: 'var(--dd-ansi-35, #bc3fbc)', 36: 'var(--dd-ansi-36, #11a8cd)', 37: 'var(--dd-ansi-37, #e5e5e5)',
  90: 'var(--dd-ansi-90, #666666)', 91: 'var(--dd-ansi-91, #f14c4c)', 92: 'var(--dd-ansi-92, #23d18b)', 93: 'var(--dd-ansi-93, #f5f543)',
  94: 'var(--dd-ansi-94, #3b8eea)', 95: 'var(--dd-ansi-95, #d670d6)', 96: 'var(--dd-ansi-96, #29b8db)', 97: 'var(--dd-ansi-97, #ffffff)',
}

const ANSI_BG_COLORS: Record<number, string> = {
  40: 'var(--dd-ansi-bg-40, #4d4d4d)', 41: 'var(--dd-ansi-bg-41, #cd3131)', 42: 'var(--dd-ansi-bg-42, #0dbc79)', 43: 'var(--dd-ansi-bg-43, #e5e510)',
  44: 'var(--dd-ansi-bg-44, #2472c8)', 45: 'var(--dd-ansi-bg-45, #bc3fbc)', 46: 'var(--dd-ansi-bg-46, #11a8cd)', 47: 'var(--dd-ansi-bg-47, #e5e5e5)',
  100: 'var(--dd-ansi-bg-100, #666666)', 101: 'var(--dd-ansi-bg-101, #f14c4c)', 102: 'var(--dd-ansi-bg-102, #23d18b)', 103: 'var(--dd-ansi-bg-103, #f5f543)',
  104: 'var(--dd-ansi-bg-104, #3b8eea)', 105: 'var(--dd-ansi-bg-105, #d670d6)', 106: 'var(--dd-ansi-bg-106, #29b8db)', 107: 'var(--dd-ansi-bg-107, #ffffff)',
}

const ANSI_256_BASE_COLORS = [
  'var(--dd-ansi-30, #4d4d4d)', 'var(--dd-ansi-31, #cd3131)', 'var(--dd-ansi-32, #0dbc79)', 'var(--dd-ansi-33, #e5e510)',
  'var(--dd-ansi-34, #2472c8)', 'var(--dd-ansi-35, #bc3fbc)', 'var(--dd-ansi-36, #11a8cd)', 'var(--dd-ansi-37, #e5e5e5)',
  'var(--dd-ansi-90, #666666)', 'var(--dd-ansi-91, #f14c4c)', 'var(--dd-ansi-92, #23d18b)', 'var(--dd-ansi-93, #f5f543)',
  'var(--dd-ansi-94, #3b8eea)', 'var(--dd-ansi-95, #d670d6)', 'var(--dd-ansi-96, #29b8db)', 'var(--dd-ansi-97, #ffffff)',
] as const

const HTML_ESCAPE_MAP: Record<string, string> = {
  '&': '&amp;',
  '<': '&lt;',
  '>': '&gt;',
  '"': '&quot;',
}
const HTML_ESCAPE_TEST = /[&<>"]/
const HTML_ESCAPE_PATTERN = /[&<>"]/g

// eslint-disable-next-line no-control-regex
const ANSI_OSC_PATTERN = /\x1b\][^\x07]*(?:\x07|\x1b\\)?/g
// eslint-disable-next-line no-control-regex
const ANSI_CONTROL_PATTERN = /\x1b\[[0-?]*[ -/]*[@-~]|\x1b[@-_]/g
// eslint-disable-next-line no-control-regex
const BARE_ANSI_PATTERN = /(^|[^\x1b])\[([0-9;]+)m/g

/**
 * 把 `& < > "` 转成 HTML 实体。
 * 除了日志渲染，`utils/markdown.ts` 也复用它 —— 那边所有来自原文的字符都必须先过这一道，
 * 所以这份实现是全站唯一的转义口径，不要再写第二份。
 */
export function escapeHtml(text: string): string {
  // 日志正文里 & < > " 都很少见，先用一次廉价探测短路，
  // 命中时也只做单趟替换，避免旧实现连续四次 replace 反复全串扫描 + 分配中间字符串。
  if (!HTML_ESCAPE_TEST.test(text)) return text
  return text.replace(HTML_ESCAPE_PATTERN, (char) => HTML_ESCAPE_MAP[char] ?? char)
}

function sanitizeLogSegment(text: string): string {
  // Drop unsupported ANSI cursor/control sequences so they do not leak into the UI.
  // 绝大多数日志行根本不含 ESC，先判断一次可以跳过两次全串正则替换。
  if (text.indexOf('\x1b') === -1) return text
  return text
    .replace(ANSI_OSC_PATTERN, '')
    .replace(ANSI_CONTROL_PATTERN, '')
}

function ansi256ToHex(code: number): string {
  if (code >= 0 && code <= 15) {
    return ANSI_256_BASE_COLORS[code] ?? ''
  }

  if (code >= 16 && code <= 231) {
    const value = code - 16
    const red = Math.floor(value / 36)
    const green = Math.floor((value % 36) / 6)
    const blue = value % 6
    const toChannel = (n: number) => n === 0 ? 0 : 55 + n * 40
    return rgbToHex(toChannel(red), toChannel(green), toChannel(blue))
  }

  if (code >= 232 && code <= 255) {
    const channel = 8 + (code - 232) * 10
    return rgbToHex(channel, channel, channel)
  }

  return ''
}

function rgbToHex(red: number, green: number, blue: number): string {
  const toHex = (value: number) => Math.max(0, Math.min(255, value)).toString(16).padStart(2, '0')
  return `#${toHex(red)}${toHex(green)}${toHex(blue)}`
}

/**
 * 一段文本渲染结束时的 ANSI 样式（SGR）状态。
 * 按行增量渲染时必须把它带到下一行，否则跨行未复位的颜色会在行边界丢失。
 */
export interface AnsiSgrState {
  fg: string
  bg: string
  bold: boolean
  dim: boolean
  italic: boolean
  underline: boolean
}

export interface AnsiSegmentResult {
  html: string
  state: AnsiSgrState
}

export const ANSI_INITIAL_SGR_STATE: AnsiSgrState = Object.freeze({
  fg: '',
  bg: '',
  bold: false,
  dim: false,
  italic: false,
  underline: false,
})

/**
 * 把一段文本渲染成 HTML，并返回渲染结束时的 ANSI 状态。
 *
 * 与 `ansiToHtml` 的区别是它支持「带入初始状态」：
 * 传入的 `startState` 非默认时会先补一个开标签，段落结束时所有标签都会闭合，
 * 因此每段产出都是结构完整、可以独立插入 DOM 的片段。
 * 这是按行增量渲染日志的基础。
 */
export function renderAnsiSegment(
  text: string,
  startState: AnsiSgrState = ANSI_INITIAL_SGR_STATE,
): AnsiSegmentResult {
  let fg = startState.fg
  let bg = startState.bg
  let bold = startState.bold
  let dim = startState.dim
  let italic = startState.italic
  let underline = startState.underline

  function buildSpan(): string {
    const styles: string[] = []
    if (fg) styles.push(`color:${fg}`)
    if (bg) styles.push(`background-color:${bg}`)
    if (bold) styles.push('font-weight:bold')
    if (dim) styles.push('opacity:0.7')
    if (italic) styles.push('font-style:italic')
    if (underline) styles.push('text-decoration:underline')
    if (styles.length === 0) return ''
    return `<span style="${styles.join(';')}">`
  }

  // 快路径：整段没有 SGR 序列（日志里绝大多数行都是这样），
  // 直接按纯文本处理，跳过正则循环，状态原样透传。
  if (text.indexOf('\x1b[') === -1) {
    const body = escapeHtml(sanitizeLogSegment(text))
    const span = buildSpan()
    return {
      html: span ? `${span}${body}</span>` : body,
      state: startState,
    }
  }

  // eslint-disable-next-line no-control-regex
  const ansiRegex = /\x1b\[([0-9;]*)m/g

  let result = ''
  let lastIndex = 0
  let openSpans = 0

  const initialSpan = buildSpan()
  if (initialSpan) {
    result += initialSpan
    openSpans++
  }

  let match: RegExpExecArray | null
  while ((match = ansiRegex.exec(text)) !== null) {
    const before = text.slice(lastIndex, match.index)
    if (before) result += escapeHtml(sanitizeLogSegment(before))
    lastIndex = match.index + match[0].length

    const codes = match[1]
      ? match[1].split(';').map(Number)
      : [0]

    for (let index = 0; index < codes.length; index++) {
      const code = codes[index] ?? 0
      if (code === 0) {
        fg = ''; bg = ''; bold = false; dim = false; italic = false; underline = false
      } else if (code === 1) {
        bold = true
      } else if (code === 2) {
        dim = true
      } else if (code === 3) {
        italic = true
      } else if (code === 4) {
        underline = true
      } else if (code === 22) {
        bold = false; dim = false
      } else if (code === 23) {
        italic = false
      } else if (code === 24) {
        underline = false
      } else if (code === 39) {
        fg = ''
      } else if (code === 49) {
        bg = ''
      } else if (ANSI_COLORS[code]) {
        fg = ANSI_COLORS[code]
      } else if (ANSI_BG_COLORS[code]) {
        bg = ANSI_BG_COLORS[code]
      } else if ((code === 38 || code === 48) && codes[index + 1] === 5) {
        const color = ansi256ToHex(codes[index + 2] ?? -1)
        if (color && code === 38) fg = color
        if (color && code === 48) bg = color
        index += 2
      } else if ((code === 38 || code === 48) && codes[index + 1] === 2) {
        const red = codes[index + 2]
        const green = codes[index + 3]
        const blue = codes[index + 4]
        if (typeof red === 'number' && typeof green === 'number' && typeof blue === 'number') {
          const color = rgbToHex(red, green, blue)
          if (code === 38) fg = color
          if (code === 48) bg = color
        }
        index += 4
      }
    }

    if (openSpans > 0) {
      result += '</span>'
      openSpans--
    }

    const span = buildSpan()
    if (span) {
      result += span
      openSpans++
    }
  }

  const remaining = text.slice(lastIndex)
  if (remaining) result += escapeHtml(sanitizeLogSegment(remaining))

  while (openSpans > 0) {
    result += '</span>'
    openSpans--
  }

  const unchanged = fg === startState.fg
    && bg === startState.bg
    && bold === startState.bold
    && dim === startState.dim
    && italic === startState.italic
    && underline === startState.underline

  return {
    html: result,
    // 状态没变就复用同一个对象，避免每行都产生一份新对象
    state: unchanged ? startState : { fg, bg, bold, dim, italic, underline },
  }
}

export function ansiToHtml(text: string): string {
  return renderAnsiSegment(text, ANSI_INITIAL_SGR_STATE).html
}

/**
 * Check if text contains ANSI escape sequences.
 * Also matches incomplete sequences like [34m that lost the ESC byte
 * during transport (common in SSE/WebSocket).
 */
export function containsAnsi(text: string): boolean {
  // eslint-disable-next-line no-control-regex
  return /\x1b\[/.test(text) || /\[([0-9;]+)m/.test(text)
}

/**
 * Normalize broken ANSI codes where the ESC (\x1b) byte was stripped,
 * leaving bare sequences like [34m.
 */
export function normalizeAnsi(text: string): string {
  // Replace bare [<digits>m patterns with proper ESC sequences
  // without touching already valid ESC-prefixed sequences.
  if (text.indexOf('[') === -1) return text
  return text.replace(BARE_ANSI_PATTERN, '$1\x1b[$2m')
}

function utf8ByteLength(text: string): number {
  let bytes = 0
  for (let i = 0; i < text.length; i++) {
    const code = text.charCodeAt(i)
    if (code < 0x80) {
      bytes += 1
    } else if (code < 0x800) {
      bytes += 2
    } else if (code >= 0xd800 && code <= 0xdbff) {
      // 代理对：一个 4 字节字符占两个 UTF-16 码元
      bytes += 4
      i++
    } else {
      bytes += 3
    }
  }
  return bytes
}

/* ============================================================================
 * 终端行缓冲：日志窗口的增量渲染核心
 * ----------------------------------------------------------------------------
 * 背景：日志窗口原来每次刷新都要对「整份日志」重跑
 *   join -> normalizeAnsi -> ansiToHtml -> v-html 整块替换，
 * SSE 流式追加时每次 flush 都要跑一遍，整体是 O(n²)，日志越长越卡。
 *
 * 这里改成两级缓存：
 *   1. 按行缓存：历史行一旦落定就不会再变，HTML 只解析一次；
 *   2. 按块缓存：每 TERMINAL_RENDER_CHUNK_SIZE 行拼成一个块，
 *      被后续内容「封口」的块字符串固定不变，模板里 v-html 的值不变，
 *      Vue 的 props diff 会直接跳过，浏览器不会重建这段 DOM。
 * 于是每次 flush 只需要重算「最后一个未写满的块」和「正在刷新的当前行」，
 * 开销与日志总长度无关。
 * ============================================================================ */

/** 每个渲染块包含的行数。块越小单次重算越便宜，块越大节点越少，100 是折中值。 */
export const TERMINAL_RENDER_CHUNK_SIZE = 100

export interface TerminalRenderChunk {
  /** 块序号，同时作为 v-for 的 key，保证窗口滚动时节点身份稳定 */
  key: number
  html: string
}

export interface TerminalLineBuffer {
  /** 完全没有内容（历史行和当前行都为空） */
  readonly isEmpty: boolean
  /** 已落定的历史行数 */
  readonly lineCount: number
  /** 状态栏展示用行数，等价于旧实现的 renderedText.split('\n').length */
  readonly displayLineCount: number
  /** 状态栏展示用字节数，等价于旧实现的 new Blob([renderedText]).size */
  readonly byteLength: number
  reset(): void
  /**
   * 追加一段终端输出。
   * `\n` / `\r\n` 落新行；裸 `\r` 只把光标移回行首，等后续字符覆盖当前行。
   * `commitBoundary` 为 true 时，若本批既没换行也没出现裸 `\r`，才把当前行落定。
   */
  append(chunk: string, commitBoundary?: boolean): void
  /** 取渲染窗口内的块；`windowChunks <= 0` 表示不封顶 */
  visibleChunks(windowChunks?: number): TerminalRenderChunk[]
  /** 渲染窗口之外被省略的行数 */
  omittedLineCount(windowChunks?: number): number
  /** 正在刷新的当前行 HTML（已带上与历史行之间的换行） */
  pendingLineHtml(): string
  /** 还原成纯文本，供复制/下载使用；按需调用，不进渲染路径 */
  toText(): string
}

export function createTerminalLineBuffer(): TerminalLineBuffer {
  let lines: string[] = []
  let lineHtml: string[] = []
  let sealedChunkHtml = new Map<number, string>()
  // 只需要最后一行结束时的 ANSI 状态：新行和「正在刷新的当前行」都从它接着往下渲染
  let lastLineState: AnsiSgrState = ANSI_INITIAL_SGR_STATE
  let committedBytes = 0
  let pending = ''
  let pendingCarriageReturn = false

  function commitLine(raw: string) {
    const rendered = renderAnsiSegment(normalizeAnsi(raw), lastLineState)
    // 行之间的 '\n' 也要算进字节数，才能和旧的 Blob(整份文本) 口径一致
    committedBytes += utf8ByteLength(raw) + (lines.length > 0 ? 1 : 0)
    lines.push(raw)
    lineHtml.push(rendered.html)
    lastLineState = rendered.state
  }

  function append(chunk: string, commitBoundary = false) {
    if (!chunk && !commitBoundary) return

    let tail = pending
    let carriageReturnPending = pendingCarriageReturn
    let endedWithLineBreak = false
    let sawCarriageReturn = false

    if (chunk.indexOf('\r') === -1) {
      // 快路径：本批没有裸 \r，可以直接按 \n 切分，
      // 省掉逐字符拼接（大块历史日志一次性灌入时差别很明显）。
      const parts = chunk.split('\n')
      for (let i = 0; i < parts.length; i++) {
        if (i > 0) {
          commitLine(tail)
          tail = ''
          carriageReturnPending = false
          endedWithLineBreak = true
        }
        const part = parts[i]!
        if (part !== '') {
          if (carriageReturnPending) {
            tail = ''
            carriageReturnPending = false
          }
          tail += part
          endedWithLineBreak = false
        }
      }
    } else {
      for (let i = 0; i < chunk.length; i++) {
        const char = chunk[i]
        if (char === '\r') {
          if (chunk[i + 1] === '\n') {
            commitLine(tail)
            tail = ''
            carriageReturnPending = false
            endedWithLineBreak = true
            sawCarriageReturn = false
            i++
            continue
          }
          // 裸 \r 表示终端希望“回到当前行开头”，不是换行。
          // 这里先标记等待下一批字符覆盖，避免 SSE 分片刚好停在 \r 后面时把进度条临时清空。
          carriageReturnPending = true
          endedWithLineBreak = false
          sawCarriageReturn = true
          continue
        }

        if (char === '\n') {
          commitLine(tail)
          tail = ''
          carriageReturnPending = false
          endedWithLineBreak = true
          sawCarriageReturn = false
          continue
        }

        if (carriageReturnPending) {
          // 收到裸 \r 后的第一段普通字符，才真正覆盖当前行。
          // 这样进度条在 Web 面板里会像终端一样留在同一行刷新。
          tail = ''
          carriageReturnPending = false
        }
        tail += char
        endedWithLineBreak = false
      }
    }

    // 只有真正确定这一批内容已经形成稳定的一行时才落行。
    // 如果这批内容里出现过裸 \r，说明它更像“正在刷新的终端行”，
    // 需要继续留在当前行，等待后续覆盖或最终的 \n 收尾。
    if (commitBoundary && !endedWithLineBreak && !sawCarriageReturn) {
      commitLine(tail)
      tail = ''
      carriageReturnPending = false
    }

    pending = tail
    pendingCarriageReturn = carriageReturnPending
  }

  function chunkCount(): number {
    return Math.ceil(lineHtml.length / TERMINAL_RENDER_CHUNK_SIZE)
  }

  function chunkHtml(index: number): string {
    const start = index * TERMINAL_RENDER_CHUNK_SIZE
    if (start >= lineHtml.length) return ''
    const end = Math.min(start + TERMINAL_RENDER_CHUNK_SIZE, lineHtml.length)
    // 一个块只有在「写满」且「后面还有内容」时才算封口：
    // 此时它固定以一个换行结尾，内容不会再变，可以永久缓存。
    const sealed = end - start === TERMINAL_RENDER_CHUNK_SIZE && end < lineHtml.length
    if (!sealed) {
      return lineHtml.slice(start, end).join('\n')
    }
    const cached = sealedChunkHtml.get(index)
    if (cached !== undefined) return cached
    const html = `${lineHtml.slice(start, end).join('\n')}\n`
    sealedChunkHtml.set(index, html)
    // 封口后块内单行 HTML 不会再被读取，释放掉，
    // 避免超长日志同时留着「按行」和「按块」两份 HTML
    for (let i = start; i < end; i++) {
      lineHtml[i] = ''
    }
    return html
  }

  function firstVisibleChunk(windowChunks: number): number {
    if (!windowChunks || windowChunks <= 0) return 0
    return Math.max(0, chunkCount() - windowChunks)
  }

  return {
    get isEmpty() {
      return lines.length === 0 && pending === ''
    },
    get lineCount() {
      return lines.length
    },
    get displayLineCount() {
      // 对齐旧实现：整份文本为空串时算 0 行
      if (committedBytes === 0 && pending === '') return 0
      return lines.length + (pending !== '' || lines.length === 0 ? 1 : 0)
    },
    get byteLength() {
      if (pending === '') return committedBytes
      return committedBytes + (lines.length > 0 ? 1 : 0) + utf8ByteLength(pending)
    },
    reset() {
      // 直接换新数组而不是清长度，让超大日志占用的内存能被回收
      lines = []
      lineHtml = []
      sealedChunkHtml = new Map()
      lastLineState = ANSI_INITIAL_SGR_STATE
      committedBytes = 0
      pending = ''
      pendingCarriageReturn = false
    },
    append,
    visibleChunks(windowChunks = 0) {
      const total = chunkCount()
      const start = firstVisibleChunk(windowChunks)
      const chunks: TerminalRenderChunk[] = []
      for (let i = start; i < total; i++) {
        chunks.push({ key: i, html: chunkHtml(i) })
      }
      return chunks
    },
    omittedLineCount(windowChunks = 0) {
      return firstVisibleChunk(windowChunks) * TERMINAL_RENDER_CHUNK_SIZE
    },
    pendingLineHtml() {
      if (pending === '') return ''
      const html = renderAnsiSegment(normalizeAnsi(pending), lastLineState).html
      // 历史行块之间不带前导换行，所以当前行要自己补上与上一行的分隔
      return lines.length > 0 ? `\n${html}` : html
    },
    toText() {
      if (lines.length === 0) return pending
      if (pending === '') return lines.join('\n')
      return `${lines.join('\n')}\n${pending}`
    },
  }
}
