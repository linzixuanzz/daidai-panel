/**
 * 从任务命令里解析出「脚本相对路径」（纯函数、零依赖、可单独复用）。
 *
 * 【口径的唯一真源是 `server/service/script_runner.go`】
 * 这里是它的前端镜像：`ParseCommandExecutionPlan` / `parseTaskCommandPlan` /
 * `splitCommandTokens` / `splitTaskShellAndScriptArgs` / `findTaskScriptTarget` /
 * `findScriptTarget` / `isValidTaskRemainder` / `isSupportedScriptExtension` /
 * `parseTaskTimeoutSeconds` / `resolveCommandScriptPath` 逐条对齐。
 * **改那边就要同步改这里**，两边没有任何自动校验把它们钉在一起。
 *
 * 【这层耦合会怎么静默失效】
 * - 后端新增一个顶层关键字（比如在 `task` / `desi` 之外再加一个 `xxx`），
 *   这里的首 token 分支认不出来，会直接判成「没有脚本路径」→ 静默漏判；
 * - 后端新增一个解释器（比如 `python3.13`）或一种可执行扩展名，同样漏判；
 * - 后端改 `-m` / `-l` / `--` 的处理，或改 `findTaskScriptTarget` 的贪婪语义，
 *   这里会解析出另一条路径 → 匹配结果与真实执行的脚本不一致；
 * - 后端改 `parseTaskTimeoutSeconds` 的超时格式（新增单位后缀、放开 `0` 或负数、加上下界），
 *   这里的 `isValidTaskTimeoutValue` 没跟着改 → `-m` 取值的合法性判定两边分叉。
 *   后端放宽、这里没放 → 漏判（安全）；后端**收紧**、这里没收 → 误判，会吞掉提示。
 * 漏判只会让调用方回落到「照旧问一次」，不会误伤；但**误判**会吞掉提示，所以宁可保守。
 * 换个说法：后端所有「会让整条命令报错、这条任务根本跑不起来」的校验，
 * 这里都必须一起镜像成「解析不出来」，否则就会把一条从没跑起来过的任务当成有效引用。
 *
 * 【前端做不到、只能认下的一件事】
 * `resolveCommandScriptPath` 会 `os.Stat` 校验脚本是否真实存在，前端没有这个能力。
 * 所以 `task a.py b.py` 这种命令，后端会回落到存在的那个候选，这里只会取最长的
 * `a.py b.py` → 与目标不相等 → 判不匹配。方向是安全的（多问一次）。
 * 同一个函数里**不依赖文件系统**的另外两条校验（危险字符黑名单、绝对路径穿越）已经镜像过来了，
 * 见 `isResolvableScriptCandidate`——它们同样会让整条命令跑不起来，不镜像就是误判。
 */

/** task / desi 能直接执行的 6 种扩展名，抄 `isSupportedScriptExtension` */
const SCRIPT_EXTENSIONS = ['.py', '.js', '.mjs', '.ts', '.sh', '.go']

/** 可以直接当首 token 的解释器，抄 `ParseCommandExecutionPlan` 的 case 分支（逐字，勿增删） */
const INTERPRETERS = ['python', 'python3', 'python3.10', 'python3.11', 'python3.12', 'node', 'ts-node', 'bash', 'go']

/** 其中的 Python 系解释器，抄 `IsPythonInterpreter`：只有它们支持 `-m <模块>` 形态 */
const PYTHON_INTERPRETERS = ['python', 'python3', 'python3.10', 'python3.11', 'python3.12']

/**
 * Go 的 `unicode.IsSpace` 按 Unicode White_Space 属性判定；
 * JS 的 `\s` 比它多一个 U+FEFF，这里排掉，保持两边切词结果一致。
 */
function isCommandSpace(ch: string): boolean {
  // U+FEFF 用码点判断，避免源码里出现一个看不见的字符
  return ch.codePointAt(0) !== 0xfeff && /\s/u.test(ch)
}

/**
 * 去掉首尾空白，抄 Go 的 `strings.TrimSpace`。
 * 空白判定复用 `isCommandSpace` 而不是 JS 自带的 `String.prototype.trim`：
 * 后者会把 U+FEFF 也当空白吃掉，比 `unicode.IsSpace` 宽松。
 */
function trimCommandSpace(value: string): string {
  // Array.from 按码点切分，和 Go 的 rune 迭代一致
  const chars = Array.from(value)
  let start = 0
  let end = chars.length
  while (start < end && isCommandSpace(chars[start]!)) start++
  while (end > start && isCommandSpace(chars[end - 1]!)) end--
  return chars.slice(start, end).join('')
}

/**
 * 切词，抄 `splitCommandTokens`：单双引号 + 反斜杠转义，空白分隔，空 token 丢弃。
 * 引号未闭合时后端直接报错，这里返回 null 让调用方按「解析不出来」处理。
 *
 * 导出给演示站 `web/src/demo/db.ts` 复用（删除任务时判定脚本）：两处必须按同一套规则切词，
 * 否则带引号的命令两边分类不一致。脚本管理页也在用它，改行为前两边都要看。
 */
export function splitCommandTokens(command: string): string[] | null {
  const tokens: string[] = []
  let current = ''
  let quote = ''
  let escaped = false

  // for...of 按码点迭代，与 Go 的 `for _, r := range command` 一致（不会把代理对拆成两半）
  for (const ch of command) {
    if (escaped) {
      // 只有这四类字符会被反斜杠「吃掉」，其余原样保留反斜杠本身
      if (ch === '\'' || ch === '"' || ch === '\\' || isCommandSpace(ch)) {
        current += ch
      } else {
        current += '\\' + ch
      }
      escaped = false
      continue
    }

    // 单引号内的反斜杠不转义（和 shell 一致）
    if (ch === '\\' && quote !== '\'') {
      escaped = true
      continue
    }

    if (quote) {
      if (ch === quote) {
        quote = ''
        continue
      }
      current += ch
      continue
    }

    if (ch === '\'' || ch === '"') {
      quote = ch
      continue
    }

    if (isCommandSpace(ch)) {
      if (current) {
        tokens.push(current)
        current = ''
      }
      continue
    }

    current += ch
  }

  if (escaped) current += '\\'
  if (quote) return null
  if (current) tokens.push(current)
  return tokens
}

/**
 * 取扩展名，抄 Go 的 `filepath.Ext`：从末尾往前找，遇到路径分隔符就停。
 * 分隔符只认 `/`——面板的运行环境是 Linux/Android，Windows 上的 `\` 在那边是普通字符。
 */
function fileExtension(path: string): string {
  for (let i = path.length - 1; i >= 0; i--) {
    const ch = path[i]
    if (ch === '/') break
    if (ch === '.') return path.slice(i).toLowerCase()
  }
  return ''
}

/** 命令末尾的剩余 token 是否构成合法的 task 尾巴，抄 `isValidTaskRemainder` */
function isValidTaskRemainder(remainder: string[], forcedDesi: boolean): boolean {
  if (forcedDesi) return remainder.length >= 1
  if (remainder.length === 0) return true
  switch (remainder[0]) {
    case 'now':
      return remainder.length === 1
    case 'conc':
    case 'desi':
      return remainder.length >= 2
    default:
      return true
  }
}

/** `resolveCommandScriptPath` 的危险字符黑名单，逐字抄（`..` 是两个字符的子串，不是「任意两个点」） */
const DANGEROUS_PATH_FRAGMENTS = ['..', '~', '$', '`', ';', '|', '&', '>', '<']

/**
 * 这个候选路径后端能不能解析出来（只镜像「必然失败」的部分）。
 *
 * 后端 `resolveCommandScriptPath` 对每个候选依次做三件事，任何一条不过就跳过这个候选：
 * 1. 危险字符黑名单；
 * 2. `pathutil.ResolveWithinBase`——绝对路径不会被拼到 scriptsDir 下面，落在 scriptsDir
 *    外面直接报「检测到路径穿越」；
 * 3. `os.Stat` 校验文件真实存在——这条前端做不到，见文件头。
 * 这里镜像 1 和 2：它们只看命令文本，不需要文件系统，而且不镜像就会误判
 * （`task a$b.py` 后端根本跑不起来，这里却会解析出 `a$b.py` 去和刚上传的脚本比相等）。
 *
 * ⚠️ 必须是「跳过这个候选」而不是「整条命令失败」，和后端的 `continue` 对齐：
 * `task a.py $x.py` 后端会跳过带 `$` 的长候选、回落到 `a.py` 并真的执行它，这里也要一样。
 */
function isResolvableScriptCandidate(candidate: string): boolean {
  // 绝对路径：Go 的 `filepath.IsAbs` 在 Linux/Android 上就是判首字符是不是 `/`。
  // 落在 scriptsDir 里的绝对路径后端能跑，但归一化后也不会等于文件树里的相对路径，
  // 两种情况都判「解析不出来」，结果一致。
  if (candidate.startsWith('/')) return false
  return !DANGEROUS_PATH_FRAGMENTS.some((fragment) => candidate.includes(fragment))
}

/**
 * 贪婪取脚本路径，抄 `findTaskScriptTarget` / `findScriptTarget`：
 * 从 1 个 token 开始逐个加长，把前缀用空格 join 成候选，取**最长**的合法候选。
 * 所以 `task dir/my script.py now` 这种不带引号的空格文件名也能解析出来。
 *
 * `checkRemainder` 对应两个入口的差别：task/desi 走的 `findTaskScriptTarget` 会额外
 * 校验尾巴，纯解释器走的 `findScriptTarget` 不校验。
 */
function findScriptTokenPath(tokens: string[], forcedDesi: boolean, checkRemainder: boolean): string | null {
  let best: string | null = null
  for (let count = 1; count <= tokens.length; count++) {
    const candidate = tokens.slice(0, count).join(' ')
    if (!SCRIPT_EXTENSIONS.includes(fileExtension(candidate))) continue
    // 顺序跟后端一致：先扩展名，再 resolveCommandScriptPath，最后校验尾巴
    if (!isResolvableScriptCandidate(candidate)) continue
    if (checkRemainder && !isValidTaskRemainder(tokens.slice(count), forcedDesi)) continue
    // 后面更长的合法候选会覆盖前面的，等价于 Go 那边循环到底后留下 bestCount 最大的一个
    best = candidate
  }
  return best
}

/**
 * `-m` 后面那个超时取值合不合法，抄 `parseTaskTimeoutSeconds`。
 *
 * 后端规则逐条对齐：
 * 1. `strings.TrimSpace` + `strings.ToLower`——带引号的 token 真的可能含首尾空白（`-m " 30M "`）；
 * 2. 末位是 `s` / `m` / `h` / `d` 就当单位后缀削掉（分别 ×1 / ×60 / ×3600 / ×86400），
 *    其余字符原样留给下一步的数字解析去否掉；
 * 3. 剩下的部分交给 `strconv.Atoi`：只认「可选正负号 + 一串 ASCII 数字」，
 *    空串、内部空白、小数点、下划线、`5x` 这种尾巴一律报错；
 * 4. 还要求 `seconds > 0`，所以 `0` / `0m` / 负数全部非法；
 * 5. Atoi 超出 int64 会连值带 error 一起返回，后端同样判非法。
 * 后端**不设上界**（`999d` 也收），乘出来溢出它自己也不报错，所以这里不额外收紧。
 */
function isValidTaskTimeoutValue(raw: string): boolean {
  let value = trimCommandSpace(raw).toLowerCase()
  if (!value) return false

  // 后端按**字节**取末位：多字节字符的末字节 >= 0x80，永远匹配不上这四个 ASCII 字母，
  // 所以这里按 UTF-16 码元取末位是等价的（末位是代理对低位时同样匹配不上）
  const suffix = value[value.length - 1]
  if (suffix === 's' || suffix === 'm' || suffix === 'h' || suffix === 'd') {
    value = value.slice(0, -1)
  }

  // 写死 [0-9] 而不是 \d，贴住 Atoi 只认 ASCII 数字的语义
  if (!/^[+-]?[0-9]+$/.test(value)) return false
  // 负数落在后端的 `seconds <= 0` 分支
  if (value.startsWith('-')) return false

  // 去掉正号与前导零，`+030` / `030` 在 Atoi 里都是 30；全是 0 则得 0，同样落在 `seconds <= 0`
  const digits = value.replace(/^\+/, '').replace(/^0+/, '')
  if (!digits) return false

  // int64 上界：这个量级 Number 已经丢精度了，只能按十进制字符串比大小
  const int64Max = '9223372036854775807'
  if (digits.length > int64Max.length) return false
  if (digits.length === int64Max.length && digits > int64Max) return false

  return true
}

/** `task` / `desi` 之后的部分，抄 `parseTaskCommandPlan` 的选项循环 + `splitTaskShellAndScriptArgs` */
function parseTaskShellScriptPath(tokens: string[], forcedDesi: boolean): string | null {
  let idx = 0
  while (idx < tokens.length) {
    // -m 后面跟超时时间（`-m 30m`），两个 token 一起跳过。
    // ⚠️ 后端不只是跳过：它会用 parseTaskTimeoutSeconds 校验取值，非法就整条命令报错、
    //    这条任务从来就没跑起来过。所以这里必须一起判非法并返回 null，否则
    //    `task -m 5x qd.py` 会被解析出 `qd.py` → 上传 qd.py 时被当成「已有任务」→ 吞掉建任务提示。
    if (tokens[idx] === '-m') {
      const rawTimeout = tokens[idx + 1]
      // 抄「缺少 -m 对应的超时时间」（后端的 `idx+1 >= len(tokens)`）
      if (rawTimeout === undefined) return null
      // 抄「无效的超时时间」
      if (!isValidTaskTimeoutValue(rawTimeout)) return null
      idx += 2
      continue
    }
    // -l 是纯开关，跳过一个
    if (tokens[idx] === '-l') {
      idx += 1
      continue
    }
    break
  }

  let shellTokens = tokens.slice(idx)
  // `--` 之后是透传给脚本的参数，不参与脚本路径匹配
  const separatorIndex = shellTokens.indexOf('--')
  if (separatorIndex >= 0) shellTokens = shellTokens.slice(0, separatorIndex)
  if (shellTokens.length === 0) return null

  return findScriptTokenPath(shellTokens, forcedDesi, true)
}

/**
 * 从一条任务命令里解析出脚本相对路径；解析不出来（托管依赖命令、`python -m` 模块、
 * 命令格式非法等）返回 null。返回值未做归一化，比较请用 `taskCommandMatchesScript`。
 */
export function parseTaskCommandScriptPath(command: string): string | null {
  const tokens = splitCommandTokens(command)
  if (!tokens) return null

  const head = tokens[0]
  if (!head) return null

  if (head === 'task' || head === 'desi') {
    return parseTaskShellScriptPath(tokens.slice(1), head === 'desi')
  }

  if (INTERPRETERS.includes(head)) {
    const rest = tokens.slice(1)
    if (rest.length === 0) return null
    // `python -m <模块>` 跑的是模块不是脚本文件，后端在这里就分叉了，没有脚本路径
    if (PYTHON_INTERPRETERS.includes(head) && rest[0] === '-m') return null
    return findScriptTokenPath(rest, false, false)
  }

  // 其余首 token = 托管依赖命令（dailycheckin 之类），它不是脚本文件，见 `parseManagedCommandPlan`
  return null
}

/**
 * 归一化脚本路径，好让「命令里解析出来的」和「文件树里的」能整体相等比较：
 * `\` 换成 `/`、去掉前导 `./` 与 `/`。
 * ⚠️ **大小写敏感**——面板跑在 Linux/Android 上，`QD.py` 和 `qd.py` 是两个文件。
 * ⚠️ `\` → `/` 是刻意的：Linux 上 `\` 其实是合法文件名字符，所以 `task jd\qd.py`
 *    在后端指的是根目录下一个名叫 `jd\qd.py` 的文件，这里会把它当成 `jd/qd.py`。
 *    这种命令在 Linux 上本来就跑不起来（脚本不存在），按 Windows 习惯写的路径归一化过去更贴近用户本意。
 */
export function normalizeScriptPath(path: string): string {
  let normalized = path.trim().replace(/\\/g, '/')
  while (normalized.startsWith('./') || normalized.startsWith('/')) {
    normalized = normalized.startsWith('./') ? normalized.slice(2) : normalized.slice(1)
  }
  return normalized
}

/** 该扩展名能不能被 task 直接执行（`config.json` 之类不行，见 `extInterpreterMap`） */
export function isTaskRunnableScriptPath(path: string): boolean {
  return SCRIPT_EXTENSIONS.includes(fileExtension(normalizeScriptPath(path)))
}

/**
 * 这条任务命令跑的是不是这个脚本。
 *
 * ⚠️ 必须是解析出路径后**整体相等**比较，绝不能拿 basename 做 includes：
 * 根目录的 `qd.py` 和子目录的 `jd/qd.py` 是两个不同脚本，
 * 但 `'task jd/qd.py'.includes('qd.py')` 为 true，会把子目录那条任务错当成根目录脚本已有任务。
 */
export function taskCommandMatchesScript(command: string, scriptPath: string): boolean {
  const target = normalizeScriptPath(scriptPath)
  if (!target) return false
  const parsed = parseTaskCommandScriptPath(command)
  if (!parsed) return false
  return normalizeScriptPath(parsed) === target
}
