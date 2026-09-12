package service

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
)

// 本文件把一条任务命令解析成「它实际会运行的那个脚本文件」，供 #124「删除任务时可选同时删除脚本」判定用
// （判定与执行见 task_script_cleanup.go）。
//
// 为什么不用 extractTaskScriptPath（task_executor.go）：它只做文本处理、不查文件系统，而且只取最长候选。
// 勘察实验里它与真实执行有多类不一致：task a.py b.js 得到不存在的 "a.py b.js"；./a.py 不归一化；
// 目录外的绝对路径原样返回；python3 my script.py 解析为空；本来就跑不起来的命令（now extra、-m 5x、
// 危险字符）照样给出路径。拿它定位要删的文件会删错或漏删。
// 这里只用真实执行口径 ParseCommandExecutionPlan（task_executor.go 执行任务时就是它），
// 所以「解析出的脚本」一定是这条任务真正会跑的那个文件；解析失败的命令一律不产生可删的脚本。

const (
	taskScriptKindScript     = "script"
	taskScriptKindManaged    = "managed"
	taskScriptKindModule     = "module"
	taskScriptKindNotFound   = "not_found"
	taskScriptKindOutside    = "outside_scripts_dir"
	taskScriptKindUnresolved = "unresolved"

	// maxScriptTextWindowTokens 限制文本匹配时一个候选最多由几个 token 拼成（路径里可以有空格）。
	// 命令是用户输入，不设上限时超长命令会让窗口枚举的开销失控。
	maxScriptTextWindowTokens = 16
)

// scriptsBase 是脚本目录的两种绝对形式。Docker / Magisk 环境下脚本目录本身可能挂在软链接下：
// Abs 是配置里的路径，Real 是 EvalSymlinks 之后的真实路径。解析器给出的 FullPath 已经解析过软链接，
// 所以真实相对路径必须对 Real 求，否则会得到 ../real-scripts/a.py 这种路径。
type scriptsBase struct {
	Abs  string
	Real string
}

// resolveScriptsBase 算出脚本目录的 Abs 与 Real。
// 失败时仍尽量返回 Abs（让调用方还能给出相对路径做展示），但调用方必须把所有脚本记为 check_failed，一个都不删。
func resolveScriptsBase(scriptsDir string) (scriptsBase, error) {
	dir := strings.TrimSpace(scriptsDir)
	if dir == "" {
		return scriptsBase{}, fmt.Errorf("脚本目录未配置")
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return scriptsBase{}, err
	}
	abs = filepath.Clean(abs)
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		// Windows 上脚本目录的「上级」是 NTFS 目录联接时（用 mklink /J 把 data 或面板目录挪到别的盘），
		// Go 1.23 起联接的 Lstat 只报 ModeIrregular、不算目录，EvalSymlinks 走到这一段就报错；目录本身其实好好的，
		// 任务也照常能跑——执行侧 pathutil.resolveExistingPath 在 EvalSymlinks 出错时同样退回未解析的路径。
		// 这里与它同口径：目录能 Stat 到就退回 Real=Abs，只有 Stat 也失败才算错误（否则所有脚本恒判 check_failed）。
		// 脚本目录以下的联接不受影响，pathHasLinkSegment 仍会逐段拦住。见 R2-2。
		if info, statErr := os.Stat(abs); statErr == nil && info.IsDir() {
			return scriptsBase{Abs: abs, Real: abs}, nil
		}
		return scriptsBase{Abs: abs, Real: abs}, err
	}
	if realAbs, absErr := filepath.Abs(real); absErr == nil {
		real = realAbs
	}
	return scriptsBase{Abs: abs, Real: filepath.Clean(real)}, nil
}

// TaskScriptTarget 是一条任务命令解析出的脚本信息。
type TaskScriptTarget struct {
	// Kind：script | managed | module | not_found | outside_scripts_dir | unresolved。
	// 后三种是解析失败的分类，只影响提示文案：以后解析器的错误文案改了，最多退化成 unresolved，
	// 删不删的结论不受影响（解析失败的命令一律不产生可删的脚本）。
	Kind           string
	ManagedCommand string // Kind=managed：托管命令名，只用于提示
	PythonModule   string // Kind=module：模块名，只用于提示
	// HintPath 只在 Kind=not_found 时有值：相对脚本目录的展示路径。
	// 求出来以 .. 开头（目录外）或求不出来时留空——不回显绝对路径。
	HintPath string

	// 以下只在 Kind=script 时有值。
	RealPath   string // plan.FullPath：已解析软链接的绝对路径
	LiteralAbs string // 命令里写的字面路径的绝对形式，未解析软链接；Lstat 它才能认出软链接
	RelPath    string // RealPath 相对 Real 的正斜杠路径，也是对外展示与确认用的 path
	LiteralRel string // LiteralAbs 相对脚本目录的正斜杠路径
	Direct     bool   // LiteralRel 与 RelPath 相同（Windows 不分大小写），即路径上没有经过软链接
	// LinkCheckFailed 表示 pathHasLinkSegment 逐段 Lstat 时出错（某一段不可访问），无法判定路径是否经过链接。
	// 只有 Kind=script 且文本上 Direct 为真时才会逐段检查，所以也只在这种情况下可能为 true；
	// 命中一律按 check_failed 保留（排在 hidden_path 之后判定），Collect 时 addMember 会写一行面板日志。
	LinkCheckFailed bool
	// linkCheckErr 是 LinkCheckFailed 时的错误原文，只给 addMember 写面板日志用（原文带绝对路径，不回显给前端）。
	linkCheckErr error

	// TextCandidates 对所有非 script 的 Kind（managed / module / not_found / outside / unresolved）计算：
	// 命令文本或 python 模块名里「看起来指向脚本文件」的相对路径。
	// 它只用来扩大「共用」判定（宁可多保留），绝不参与删除。
	TextCandidates []string
}

// ResolveTaskScriptTarget 按真实执行口径解析一条任务命令。
func ResolveTaskScriptTarget(command string, base scriptsBase) TaskScriptTarget {
	plan, err := ParseCommandExecutionPlan(command, base.Abs)
	if err != nil {
		target := TaskScriptTarget{Kind: classifyTaskScriptParseError(err)}
		if target.Kind == taskScriptKindNotFound {
			target.HintPath = taskScriptHintPath(err, base)
		}
		// not_found / outside_scripts_dir / unresolved 都可能是「命令当前跑不起来，但用户以后修好命令
		// 还会用到它指向的文件」，一律计算文本候选参与共用判定；目录外的候选会被 normalizeScriptReference
		// 丢掉，不会扩大删除。这些候选只用于「其他任务是否也在用这个脚本」，绝不参与删哪个文件。
		target.TextCandidates = collectScriptTextCandidates(command, base)
		return target
	}

	if plan.ManagedCommand != "" {
		// 托管命令以 WorkDir=scriptsDir 运行并原样透传参数（python3.13 a.py、tsx a.ts），
		// 实际依赖参数里的脚本，所以同样要参与共用判定。
		return TaskScriptTarget{
			Kind:           taskScriptKindManaged,
			ManagedCommand: plan.ManagedCommand,
			TextCandidates: collectScriptTextCandidates(command, base),
		}
	}
	if plan.PythonModule != "" {
		// python3 -m a.b.c 走 runpy.run_module，实际会加载脚本目录里的 a/b/c.py 或 a/b/c/__main__.py，
		// 导入链上每一级包的 __init__.py 也会被执行。删掉这些文件会让「用 -m 运行同一脚本」的其他任务坏掉，
		// 所以把它们映射成文本候选参与共用判定（不参与删除）。
		return TaskScriptTarget{
			Kind:           taskScriptKindModule,
			PythonModule:   plan.PythonModule,
			TextCandidates: pythonModuleTextCandidates(plan.PythonModule, base),
		}
	}

	target := TaskScriptTarget{Kind: taskScriptKindScript, RealPath: filepath.Clean(plan.FullPath)}
	token := strings.TrimSpace(plan.ScriptToken)
	switch {
	case token == "":
		// 理论上不会发生（ScriptToken 与 FullPath 同时赋值）。拿不到字面路径就无法确认有没有经过软链接，
		// 留空让 Direct=false，判定会按「保留」处理。
	case filepath.IsAbs(token):
		target.LiteralAbs = filepath.Clean(token)
	default:
		target.LiteralAbs = filepath.Join(base.Abs, token)
	}
	target.RelPath = relSlash(base.Real, target.RealPath)
	if target.LiteralAbs != "" {
		target.LiteralRel = relSlash(base.Abs, target.LiteralAbs)
		if isOutsideRel(target.LiteralRel) {
			// 命令里写的是真实路径（脚本目录本身挂在软链接下时），换对 Real 求。
			target.LiteralRel = relSlash(base.Real, target.LiteralAbs)
		}
	}
	target.Direct = target.LiteralAbs != "" && !isOutsideRel(target.RelPath) && samePathText(target.LiteralRel, target.RelPath)
	// 只在文本上 Direct 为真时逐段检查。Direct 已为 false 的路径 decide 必然给 symlink，再查是多余的；而且其中
	// 「字面路径在脚本目录外、解析器却解析到了目录内」的写法（Docker 版青龙兼容层的 /ql/data/scripts 这类脚本目录
	// 别名写的绝对路径）求不出目录内的相对路径，pathHasLinkSegment 会报错，把准确的 symlink 退化成 check_failed。见 R2-1。
	//
	// Direct 为真时仍要查：Windows 目录联接（junction）从 Go 1.23 起不再报 os.ModeSymlink，EvalSymlinks 也不解析它，
	// 于是解析器给出的 FullPath 会「穿过」联接指向脚本目录外，而上面纯文本的 Direct 比对看不出来。
	// 逐段 Lstat 补这道闸：命中就令 Direct=false（decide 会给 symlink，一律保留）；Lstat 出错则标
	// LinkCheckFailed，走 check_failed。见 A1。
	if target.Direct {
		if hit, err := pathHasLinkSegment(base, target.LiteralAbs); err != nil {
			target.LinkCheckFailed = true
			target.linkCheckErr = err
		} else if hit {
			target.Direct = false
		}
	}
	return target
}

// classifyTaskScriptParseError 把解析失败分成三类，只用于选提示文案。
// 按错误文案匹配是不得已（解析器只返回 fmt.Errorf）：文案以后改了最多退化成 unresolved，
// 不影响删不删；测试钉住了这几句文案，改了会变红。
func classifyTaskScriptParseError(err error) string {
	if errors.Is(err, fs.ErrNotExist) {
		return taskScriptKindNotFound
	}
	message := err.Error()
	if strings.Contains(message, "检测到路径穿越") || strings.Contains(message, "危险字符: ..") {
		return taskScriptKindOutside
	}
	return taskScriptKindUnresolved
}

// taskScriptHintPath 从「文件不存在」的错误里取出路径，转成相对脚本目录的展示路径。
// 目录外或求不出来时返回空串：error-handling 规范要求不回显绝对路径与底层错误原文。
func taskScriptHintPath(err error, base scriptsBase) string {
	var pathErr *fs.PathError
	if !errors.As(err, &pathErr) || strings.TrimSpace(pathErr.Path) == "" {
		return ""
	}
	target := filepath.Clean(pathErr.Path)
	if !filepath.IsAbs(target) {
		target = filepath.Join(base.Abs, target)
	}
	rel := relSlash(base.Abs, target)
	if isOutsideRel(rel) {
		// 命令里写的是真实路径（脚本目录本身挂在软链接下时），换对 Real 求——与 LiteralRel、
		// normalizeScriptReference 的回退口径一致。否则 script_path 为空，前端认不出「确认过的文件已经不存在」。见 R2-02。
		rel = relSlash(base.Real, target)
	}
	if isOutsideRel(rel) {
		return ""
	}
	return rel
}

// collectScriptTextCandidates 从命令文本里找出「看起来指向脚本文件」的相对路径。
//
// 不能复用 extractTaskScriptPath：它只取最长的一个候选。这里要的是「所有可能」——结果只用来扩大
// 共用判定（多认一个共用方只是多保留一个文件），所以宁多勿少：
//   - task / desi：跳过开头的 -m <值> 与 -l，再按 -- 切断（-- 之后是透传给脚本的参数）；
//   - 其余形态（解释器、托管命令、不认识的首词）整条参与：解释器名本身不会以脚本扩展名结尾，
//     而像 a.py、./a.sh 这种被当成托管命令或「不支持的解释器」的首词，恰恰可能就是用户想跑的脚本；
//   - 在范围内枚举所有连续 token 窗口（路径可以含空格），以受支持扩展名结尾的都收集，
//     这样 python3.13 -u a.py 这种参数不在最前面的写法也能认出来。
func collectScriptTextCandidates(command string, base scriptsBase) []string {
	tokens, err := splitCommandTokens(command)
	if err != nil {
		// 引号没闭合之类的命令整条切不开。它当前跑不起来，但用户修好后仍会用到它指向的文件，
		// 所以退回按空白切词、去掉引号，宁可多认几个共用方。
		tokens = strings.Fields(strings.NewReplacer(`"`, " ", `'`, " ").Replace(command))
	}
	if len(tokens) == 0 {
		return nil
	}

	scope := tokens
	if tokens[0] == "task" || tokens[0] == "desi" {
		scope = tokens[1:]
		for len(scope) > 0 {
			if scope[0] == "-l" {
				scope = scope[1:]
				continue
			}
			if scope[0] == "-m" && len(scope) >= 2 {
				scope = scope[2:]
				continue
			}
			break
		}
		scope, _ = splitTaskShellAndScriptArgs(scope)
	}

	seen := make(map[string]bool)
	candidates := make([]string, 0)
	for start := 0; start < len(scope); start++ {
		for end := start; end < len(scope) && end-start < maxScriptTextWindowTokens; end++ {
			joined := strings.Join(scope[start:end+1], " ")
			if !isSupportedScriptExtension(joined) {
				continue
			}
			normalized := normalizeScriptReference(joined, base)
			if normalized == "" || seen[normalized] {
				continue
			}
			seen[normalized] = true
			candidates = append(candidates, normalized)
		}
	}
	return candidates
}

// pythonModuleTextCandidates 把 python -m a.b.c 映射成它可能加载的脚本文件（相对脚本目录的正斜杠路径），
// 只用来扩大共用判定。runpy.run_module 会执行 a/b/c.py（模块）或 a/b/c/__main__.py（包），
// 导入过程中还会执行沿途每一级包的 __init__.py（含 a/b/c 自身若是包）。目录外或非法的候选会被丢掉。
func pythonModuleTextCandidates(module string, base scriptsBase) []string {
	module = strings.TrimSpace(module)
	if module == "" {
		return nil
	}
	m := strings.ReplaceAll(module, ".", "/")
	raw := []string{m + ".py", m + "/__main__.py", m + "/__init__.py"}
	// 各级父包的 __init__.py（不含模块自身）：导入 a.b.c 会依次执行 a、a.b 的 __init__.py。
	parts := strings.Split(m, "/")
	prefix := ""
	for i := 0; i < len(parts)-1; i++ {
		if prefix == "" {
			prefix = parts[i]
		} else {
			prefix = prefix + "/" + parts[i]
		}
		raw = append(raw, prefix+"/__init__.py")
	}

	seen := make(map[string]bool)
	candidates := make([]string, 0, len(raw))
	for _, ref := range raw {
		normalized := normalizeScriptReference(ref, base)
		if normalized == "" || seen[normalized] {
			continue
		}
		seen[normalized] = true
		candidates = append(candidates, normalized)
	}
	return candidates
}

// isReparsePointFn 是 pathHasLinkSegment 实际调用的 reparse 判定。做成包级变量是为了让测试注入「某段是 reparse 点」：
// 真实的 isReparsePoint 只在 Windows 上可能为真，而 CI 跑在 Linux 上，不替换就永远兜不住这条分支的调用点。见 R2-05。
var isReparsePointFn = isReparsePoint

// pathHasLinkSegment 从脚本目录起、沿 literalAbs 逐段 Lstat，判断中间某段目录或末级是否是软链接 /
// Windows 目录联接（junction）/ 其它 reparse 点。命中返回 true，某段 Lstat 失败返回 error（调用方按保留处理）。
//
// 之所以逐段 Lstat：Go 1.23 起 Windows 联接不再报 os.ModeSymlink，EvalSymlinks 也不解析它（winsymlink=1 默认），
// 单看末级 mode 或 EvalSymlinks 都识别不出「路径穿过联接指向目录外」。
//   - 每一段都看 os.ModeSymlink|os.ModeIrregular：默认 winsymlink=1 下联接报 ModeIrregular，靠这一条就拦得住。
//   - 只对中间目录段再查 FILE_ATTRIBUTE_REPARSE_POINT（isReparsePointFn，见带 build tag 的文件）：兜的是
//     GODEBUG=winsymlink=0 回退旧语义时不报任何特殊 mode 的非名称代理重解析目录。末级（文件本身）不查：
//     Windows Server 的重复数据删除（Data Deduplication）会随时把普通文件转成 IO_REPARSE_TAG_DEDUP 重解析点，
//     Go 刻意把它当普通文件；末级再查会把这类脚本误判成「经过软链接」、永远删不掉。目录不会被去重。见 R2-4。
//
// 只检查脚本目录以下的分段：脚本目录本身挂在软链接下（Docker / Magisk）是合法的，不计。
func pathHasLinkSegment(base scriptsBase, literalAbs string) (bool, error) {
	if literalAbs == "" {
		return false, nil
	}
	start := base.Abs
	rel := relSlash(base.Abs, literalAbs)
	if isOutsideRel(rel) {
		// literalAbs 用的是真实路径（脚本目录本身挂在软链接下时），改从 Real 求相对路径。
		start = base.Real
		rel = relSlash(base.Real, literalAbs)
	}
	if isOutsideRel(rel) {
		// 求不出脚本目录内的相对路径：无法安全逐段检查，交调用方按保留处理。
		return false, fmt.Errorf("路径不在脚本目录内: %s", literalAbs)
	}

	current := start
	segments := strings.Split(rel, "/")
	for idx, segment := range segments {
		if segment == "" || segment == "." {
			continue
		}
		current = filepath.Join(current, segment)
		info, err := os.Lstat(current)
		if err != nil {
			return false, err
		}
		if info.Mode()&(os.ModeSymlink|os.ModeIrregular) != 0 {
			return true, nil
		}
		// 末级是文件本身，不查 reparse 属性（DEDUP 文件，见函数注释）。rel 由 filepath.Rel 求出，已 Clean，
		// 最后一个元素就是末级。
		if idx < len(segments)-1 && isReparsePointFn(info) {
			return true, nil
		}
	}
	return false, nil
}

// normalizeScriptReference 把命令文本里的一段路径归一化成相对脚本目录的正斜杠路径，归一化不了返回空串。
// 绝对路径先转成相对 Abs 或 Real 的路径（目录外的丢弃）；再把 \ 换成 /，反复去掉开头的 ./ 与 /，
// 最后 Clean；以 .. 开头的丢弃。
func normalizeScriptReference(ref string, base scriptsBase) string {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return ""
	}
	if native := filepath.FromSlash(ref); filepath.IsAbs(native) {
		cleaned := filepath.Clean(native)
		rel := relSlash(base.Abs, cleaned)
		if isOutsideRel(rel) {
			rel = relSlash(base.Real, cleaned)
		}
		if isOutsideRel(rel) {
			return ""
		}
		ref = rel
	}

	ref = strings.ReplaceAll(ref, `\`, "/")
	for {
		if strings.HasPrefix(ref, "./") {
			ref = ref[2:]
			continue
		}
		if strings.HasPrefix(ref, "/") {
			ref = ref[1:]
			continue
		}
		break
	}
	if ref == "" {
		return ""
	}
	ref = path.Clean(ref)
	if isOutsideRel(ref) {
		return ""
	}
	return ref
}

// relSlash 返回 target 相对 base 的正斜杠路径；求不出来时返回空串。
func relSlash(base, target string) string {
	if base == "" || target == "" {
		return ""
	}
	rel, err := filepath.Rel(base, target)
	if err != nil {
		return ""
	}
	return filepath.ToSlash(rel)
}

// isOutsideRel 判断一个相对路径是否不能代表脚本目录里的文件：空串、目录本身（.）或以 .. 开头。
func isOutsideRel(rel string) bool {
	return rel == "" || rel == "." || rel == ".." || strings.HasPrefix(rel, "../")
}

// samePathText 比较两个路径文本。Windows 文件系统不区分大小写（A.PY 与 a.py 是同一个文件），
// 其余平台严格相等。
func samePathText(a, b string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}
