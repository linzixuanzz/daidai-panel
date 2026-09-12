package service

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"syscall"

	"daidai-panel/database"
	"daidai-panel/model"
)

// subscriptionScriptIndex 是订阅同步「按脚本认任务」（#125）用的字典：键是候选脚本相对脚本目录的归一化路径。
//
// 判据刻意是「在候选集里」，不是「文件存在」：候选集就是「订阅目录里现存、且按当前白/黑名单/依赖规则
// 应当建任务的脚本」。任务命令指向集合外的文件（上游删了、被黑名单排除、改了 SaveDir）一律查不到，
// 自动删除分支于是按原来的规则处理。
//
// 也刻意不复用 #124 的 ResolveTaskScriptTarget：它每条任务都要访问文件系统（500 条任务在 Windows 上近 1 秒），
// 语义服务于「删不删文件」，那边以后再改会让这边静默变化。这里只复用执行器的切词与首词判定。
type subscriptionScriptIndex struct {
	scriptsAbs string
	// scriptsReal 是 EvalSymlinks(scriptsAbs)，建字典时求一次，失败退回 scriptsAbs。
	// 只给「落在脚本目录外的绝对路径」兜底用（Docker 青龙兼容层把 /ql/data/scripts 等软链到脚本目录）。
	scriptsReal string
	// commandByKey：键 → 候选命令。同一键对应多个候选（Windows 下仅大小写不同）时置空串，
	// 这类键只用于删除分支的「保留」，不参与新增分支的「认领」。
	commandByKey map[string]string
}

// subscriptionScriptRef 是一段路径文本归一后的结果。
type subscriptionScriptRef struct {
	key     string // 字典键：相对脚本目录、正斜杠；只在 Windows 上转小写（与执行器的大小写口径一致）
	display string // 同一路径保留原大小写，只用于日志
	full    string // 本机上的路径，只在删除分支判断「文件还在不在」时用
}

func newSubscriptionScriptIndex(scriptsDir string, candidates map[string]subscriptionTaskCandidate) subscriptionScriptIndex {
	idx := subscriptionScriptIndex{commandByKey: make(map[string]string, len(candidates))}
	if strings.TrimSpace(scriptsDir) == "" {
		return idx
	}
	abs, err := filepath.Abs(scriptsDir)
	if err != nil {
		abs = filepath.Clean(scriptsDir)
	}
	idx.scriptsAbs = abs
	idx.scriptsReal = abs
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		if resolvedAbs, err := filepath.Abs(resolved); err == nil {
			idx.scriptsReal = filepath.Clean(resolvedAbs)
		}
	}
	for command := range candidates {
		key, ok := idx.normalize(strings.TrimPrefix(command, "task "))
		if !ok {
			continue
		}
		if _, dup := idx.commandByKey[key]; dup {
			idx.commandByKey[key] = ""
			continue
		}
		idx.commandByKey[key] = command
	}
	return idx
}

// normalize 把一段路径文本归一成字典键：按执行器口径相对脚本目录求解（相对路径拼到脚本目录下，
// 绝对路径直接求相对路径），正斜杠，Clean；落在脚本目录外的丢弃；只在 Windows 上不分大小写。
// Linux 上反斜杠是合法文件名字符，不当分隔符（执行器同样不当）。
func (idx subscriptionScriptIndex) normalize(ref string) (string, bool) {
	resolved, ok := idx.resolveRef(ref)
	return resolved.key, ok
}

// resolveRef 是 normalize 的完整形态。整个求键过程只有一处会访问文件系统：绝对路径对脚本目录求出来落在
// 目录外时，解析它所在目录的软链接后再对 scriptsReal 求一次。只解析目录、不解析文件本身——
// 仓库里的文件软链接（link.js → real.js）要保持自己的键，与相对路径写法 task repo/link.js 同口径，
// 否则会被 real.js 的候选认走。
func (idx subscriptionScriptIndex) resolveRef(ref string) (subscriptionScriptRef, bool) {
	ref = strings.TrimSpace(ref)
	if ref == "" || idx.scriptsAbs == "" {
		return subscriptionScriptRef{}, false
	}
	native := filepath.FromSlash(ref)
	if !filepath.IsAbs(native) {
		return subscriptionScriptRefWithin(idx.scriptsAbs, filepath.Join(idx.scriptsAbs, native))
	}
	native = filepath.Clean(native)
	if resolved, ok := subscriptionScriptRefWithin(idx.scriptsAbs, native); ok {
		return resolved, true
	}
	dir, err := filepath.EvalSymlinks(filepath.Dir(native))
	if err != nil {
		return subscriptionScriptRef{}, false
	}
	resolved, ok := subscriptionScriptRefWithin(idx.scriptsReal, filepath.Join(dir, filepath.Base(native)))
	if !ok {
		return subscriptionScriptRef{}, false
	}
	resolved.full = native
	return resolved, true
}

// subscriptionScriptRefWithin 纯文本求 full 相对 base 的键；结果是 .、.. 或以 ../ 开头的（base 本身或目录外）返回 false。
func subscriptionScriptRefWithin(base, full string) (subscriptionScriptRef, bool) {
	full = filepath.Clean(full)
	rel, err := filepath.Rel(base, full)
	if err != nil {
		return subscriptionScriptRef{}, false
	}
	display := filepath.ToSlash(rel)
	if display == "." || display == ".." || strings.HasPrefix(display, "../") {
		return subscriptionScriptRef{}, false
	}
	return subscriptionScriptRef{key: subscriptionScriptKeyCase(display), display: display, full: full}, true
}

func subscriptionScriptKeyCase(p string) string {
	if runtime.GOOS == "windows" {
		return strings.ToLower(p)
	}
	return p
}

// candidateKey 返回候选命令自己的键；键有歧义（多个候选归一到同一键）时返回 false，不参与认领。
func (idx subscriptionScriptIndex) candidateKey(command string) (string, bool) {
	key, ok := idx.normalize(strings.TrimPrefix(command, "task "))
	if !ok {
		return "", false
	}
	if owner, hit := idx.commandByKey[key]; !hit || owner != command {
		return "", false
	}
	return key, true
}

// scriptKeyOf 取任务命令实际运行的脚本在字典里的键。每条任务至多一个键，命中字典才算。
// 首词沿用执行器 ParseCommandExecutionPlan 的判定（classifyCommandRunner）：
//   - task / desi：跳过开头的 -l、-m <值>，按 -- 切断，取字典里命中的最长前缀（执行器 findTaskScriptTarget 也取最长前缀）；
//   - 解释器 / 托管命令：从第 2 个 token 起跳过 - 开头的 token，取第一个在字典里命中的最长窗口；
//     python -m <模块> 跑的是模块、不是脚本，没有键（与 parseInterpreterCommandPlan 同口径）；
//   - 其他首词一律没有键。
//
// 不校验命令能否执行（now 后面多带参数、危险字符等）——这里只回答「它指的是哪个脚本」。
func (idx subscriptionScriptIndex) scriptKeyOf(command string) (string, bool) {
	tokens, err := splitCommandTokens(strings.TrimSpace(command))
	if err != nil || len(tokens) < 2 {
		return "", false
	}
	switch kind := classifyCommandRunner(tokens[0]); kind {
	case commandRunnerTask, commandRunnerDesi:
		return idx.longestKnownPrefix(taskCommandPathTokens(tokens[1:]))
	case commandRunnerInterpreter, commandRunnerManaged:
		args := tokens[1:]
		if kind == commandRunnerInterpreter && IsPythonInterpreter(tokens[0]) && args[0] == "-m" {
			return "", false
		}
		for start := range args {
			if strings.HasPrefix(args[start], "-") {
				continue
			}
			if key, ok := idx.longestKnownPrefix(args[start:]); ok {
				return key, true
			}
		}
		return "", false
	default:
		return "", false
	}
}

// longestKnownPrefix 从最长到最短试 tokens 的前缀（按空格拼回，路径可含空格），返回第一个带受支持扩展名、
// 归一后在字典里的键。
func (idx subscriptionScriptIndex) longestKnownPrefix(tokens []string) (string, bool) {
	for count := len(tokens); count >= 1; count-- {
		ref := strings.Join(tokens[:count], " ")
		if !isSupportedScriptExtension(ref) {
			continue
		}
		key, ok := idx.normalize(ref)
		if !ok {
			continue
		}
		if _, hit := idx.commandByKey[key]; hit {
			return key, true
		}
	}
	return "", false
}

// taskCommandPathTokens 按执行器 parseTaskCommandPlan 的口径剥掉 task / desi 后面的选项：
// 跳过开头的 -l、-m <值>，再按 -- 切断；剩下的 token 前缀才可能是脚本路径。
func taskCommandPathTokens(args []string) []string {
	i := 0
	for i < len(args) {
		if args[i] == "-l" {
			i++
			continue
		}
		if args[i] == "-m" && i+1 < len(args) {
			i += 2
			continue
		}
		break
	}
	shell, _ := splitTaskShellAndScriptArgs(args[i:])
	return shell
}

// taskCommandScriptRefs 给自动删除分支用：task / desi 命令引用的脚本路径，不要求在字典里。
// 从最长到最短列出带受支持扩展名、归一后仍在脚本目录内的前缀；由调用方挑第一个真实存在的
// （执行器 findTaskScriptTarget 跑的就是「最长的、能解析到的」那个前缀）。
func (idx subscriptionScriptIndex) taskCommandScriptRefs(command string) []subscriptionScriptRef {
	tokens, err := splitCommandTokens(strings.TrimSpace(command))
	if err != nil || len(tokens) < 2 {
		return nil
	}
	if kind := classifyCommandRunner(tokens[0]); kind != commandRunnerTask && kind != commandRunnerDesi {
		return nil
	}
	pathTokens := taskCommandPathTokens(tokens[1:])
	var refs []subscriptionScriptRef
	for count := len(pathTokens); count >= 1; count-- {
		ref := strings.Join(pathTokens[:count], " ")
		if !isSupportedScriptExtension(ref) {
			continue
		}
		if resolved, ok := idx.resolveRef(ref); ok {
			refs = append(refs, resolved)
		}
	}
	return refs
}

// saveDirKeyPrefix 返回订阅目录的键前缀（带末尾 /）。订阅目录就是脚本目录本身时返回 ("", true)；
// 落在脚本目录外时返回 false，此时没有任何路径算「在订阅目录里」。
// saveDir 按 syncSubscriptionTasks 的口径拼到脚本目录下（filepath.Join），纯文本。
func (idx subscriptionScriptIndex) saveDirKeyPrefix(saveDir string) (string, bool) {
	if idx.scriptsAbs == "" {
		return "", false
	}
	rel, err := filepath.Rel(idx.scriptsAbs, filepath.Join(idx.scriptsAbs, saveDir))
	if err != nil {
		return "", false
	}
	display := filepath.ToSlash(rel)
	if display == "." {
		return "", true
	}
	if display == ".." || strings.HasPrefix(display, "../") {
		return "", false
	}
	return subscriptionScriptKeyCase(display) + "/", true
}

// subscriptionStaleTaskVerdict 是自动删除分支对一条托管任务的判定结果。
type subscriptionStaleTaskVerdict int

const (
	// staleTaskKeep：命令与候选完全相同，或按脚本认得出（键在字典里）——保留，不打日志。
	staleTaskKeep subscriptionStaleTaskVerdict = iota
	// staleTaskKeepUnscanned：命令引用的脚本在当前订阅目录里、文件还在，但本次扫描没读到
	// （目录联接、NAS / Magisk 读目录异常）——保留并提示。
	staleTaskKeepUnscanned
	// staleTaskKeepStatError：命令引用的脚本在当前订阅目录里，Stat 报的错误既不是「不存在」类、也不是名字类
	// （权限类 EACCES / EPERM / ERROR_ACCESS_DENIED，IO / 网络类 EIO / ESTALE / EHOSTDOWN、Windows 网络盘的 ERROR_NETNAME_DELETED 等，
	// 以及没见过的错误码：PUID 降权运行、NFS root_squash、SMB / CIFS 断连、存储异常、文件被独占打开），也没有更短的前缀真实存在——
	// 证明不了文件不在，保留并提示。判定见 subscriptionScriptNotAScript。
	staleTaskKeepStatError
	// staleTaskDelete：文件已不存在、被白/黑名单或依赖/辅助脚本规则排除、在当前订阅目录外，
	// 或提取不出脚本路径——按开关删除。
	staleTaskDelete
)

// subscriptionStaleTaskJudge 汇总自动删除分支需要的全部输入，拆出来是为了能直接构造「扫描漏读」的场景做单测。
type subscriptionStaleTaskJudge struct {
	index      subscriptionScriptIndex
	candidates map[string]subscriptionTaskCandidate
	saveDirKey string // saveDirKeyPrefix 的结果
	saveDirOK  bool
	seen       map[string]bool // 本次扫描读到的全部文件的键（任意扩展名）
}

func newSubscriptionStaleTaskJudge(index subscriptionScriptIndex, candidates map[string]subscriptionTaskCandidate, saveDir string, seen map[string]bool) subscriptionStaleTaskJudge {
	prefix, ok := index.saveDirKeyPrefix(saveDir)
	return subscriptionStaleTaskJudge{index: index, candidates: candidates, saveDirKey: prefix, saveDirOK: ok, seen: seen}
}

// subscriptionScriptStat 是删除判定用的 os.Stat。做成包级变量只为单测：Windows 上造不出 EACCES 这类错误。
var subscriptionScriptStat = os.Stat

// judge 对一条 task 开头的托管任务给出判定。script 是脚本的相对路径，statErr 是 Stat 报错的简短原文，都只给日志用：
// staleTaskKeepUnscanned 时有 script，staleTaskKeepStatError 时两个都有。
//
// 删除必须有正面证据：只有「认不出」且「文件不在 / 被扫描看到却被规则排除 / 在订阅目录外」才删。
//   - 只有「不存在」类与名字类 Stat 错误能证明「这个前缀不是脚本」（subscriptionScriptNotAScript），与文件不存在同路 continue：
//     ErrNotExist / ENOTDIR，以及 ENAMETOOLONG、ELOOP、EINVAL 和 Windows 的 ERROR_INVALID_NAME 等（命令参数里带 URL / 盘符 /
//     ? * | < > / 超长段 / 软链接环）——这些前缀根本不可能对应一个文件，与执行器「解析不了就试更短前缀」同口径。
//   - 其余一切 Stat 错误（权限类、IO / 网络类、没见过的错误码）都可能盖住一个真实文件，让删除退成「保留」：
//     ref 在当前订阅目录内的先记下、继续试更短的前缀；找到真实存在的就按它判（与执行器选的前缀一致：执行器
//     resolveCommandScriptPath 走 ResolveWithinBase(mustExist=true)，对任何 Stat 错误都报错、退到更短的前缀）；
//     一个真实存在的都没找到，才判 staleTaskKeepStatError，保留并提示。为什么不列白名单见 subscriptionScriptNotAScript。
//   - 提取不出脚本路径的命令（托管可执行命令如 task dailycheckin、引号未闭合）没有可查的文件，也按失效处理，与改动前一致。
func (j subscriptionStaleTaskJudge) judge(command string) (verdict subscriptionStaleTaskVerdict, script, statErr string) {
	command = strings.TrimSpace(command)
	if _, ok := j.candidates[command]; ok {
		return staleTaskKeep, "", ""
	}
	if _, ok := j.index.scriptKeyOf(command); ok {
		return staleTaskKeep, "", ""
	}
	var unsurePath string
	var unsureErr error
	for _, ref := range j.index.taskCommandScriptRefs(command) {
		info, err := subscriptionScriptStat(ref.full)
		if err != nil {
			if unsureErr == nil && !subscriptionScriptNotAScript(err) && j.inSaveDir(ref.key) {
				unsurePath, unsureErr = ref.display, err
			}
			continue
		}
		if info.IsDir() {
			continue
		}
		if j.inSaveDir(ref.key) && !j.seen[ref.key] {
			return staleTaskKeepUnscanned, ref.display, ""
		}
		return staleTaskDelete, "", ""
	}
	if unsureErr != nil {
		return staleTaskKeepStatError, unsurePath, subscriptionStatErrorText(unsureErr)
	}
	return staleTaskDelete, "", ""
}

// subscriptionScriptNotAScript 报告这个 Stat 错误能否证明「这个前缀不是脚本」。只有两类能证明，删除判定才 continue 试更短的
// 前缀、最后按删除规则处理：
//   - 「不存在」类 subscriptionScriptMissing：ErrNotExist / ENOTDIR；
//   - 名字类 subscriptionScriptBadName：路径文本本身不可能对应一个文件（命令参数里带 URL、盘符、? * | < >、超长段、软链接环）。
//
// 其余一切 Stat 错误都可能盖住一个真实存在的文件，落在当前订阅目录内就记为「无法确认」、保留并提示（staleTaskKeepStatError）。
//
// 判定刻意反过来写——列封闭的「证明不是脚本」，而不是列「可能盖住真实文件」的白名单（verify-r3 R3-1）：IO / 网络类错误开放、
// 平台相关、列不全，漏一个就是连日志删掉一个还在的任务。fix-r2 的白名单就栽在这里：Go 为 Windows 定义的
// syscall.EIO / ESTALE / ETIMEDOUT / ENOTCONN 是自造值（APPLICATION_ERROR 起，见 syscall/zerrors_windows.go），os.Stat 永远
// 不会返回；网络盘、SMB 断连时真实返回的是 ERROR_UNEXP_NET_ERR(59)、ERROR_NETNAME_DELETED(64)、ERROR_SEM_TIMEOUT(121)、
// ERROR_IO_DEVICE(1117) 这类 Windows 错误码，于是白名单在 Windows 上几乎是空的；Linux 上 CIFS 断连报的 EHOSTDOWN、ECONNRESET 等
// 也不在单里。名字类错误反而是一小撮、能列全。
func subscriptionScriptNotAScript(err error) bool {
	return subscriptionScriptMissing(err) || subscriptionScriptBadName(err)
}

// subscriptionScriptBadName：名字类 Stat 错误。通用的 ENAMETOOLONG（段或整条路径超长）、ELOOP（软链接环）、EINVAL（名字不合法，
// 如路径含 NUL 字节），加上平台特有的 subscriptionScriptBadNamePlatform（Windows 的 ERROR_INVALID_NAME 等，build tag 分文件）。
// Windows 上 ENAMETOOLONG / ELOOP 是自造值、不会命中，真实错误码靠平台文件补上；EINVAL 在 Windows 上只在路径含 NUL 字节时
// 由 UTF16FromString 返回。
func subscriptionScriptBadName(err error) bool {
	if errors.Is(err, syscall.ENAMETOOLONG) || errors.Is(err, syscall.ELOOP) || errors.Is(err, syscall.EINVAL) {
		return true
	}
	return subscriptionScriptBadNamePlatform(err)
}

// subscriptionScriptMissing：这个 Stat 错误能不能证明「文件不在」。ENOTDIR（路径中间某一段是普通文件）必须单列：
// Go 在 Unix 上只把 fs.ErrNotExist 映射到 ENOENT。
func subscriptionScriptMissing(err error) bool {
	return errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR)
}

// subscriptionStatErrorText 取 Stat 报错的简短原文：*PathError 只留底层错误（permission denied 这类），路径由调用方另行打出。
func subscriptionStatErrorText(err error) string {
	var pathErr *fs.PathError
	if errors.As(err, &pathErr) && pathErr.Err != nil {
		return pathErr.Err.Error()
	}
	return err.Error()
}

func (j subscriptionStaleTaskJudge) inSaveDir(key string) bool {
	if !j.saveDirOK {
		return false
	}
	return j.saveDirKey == "" || strings.HasPrefix(key, j.saveDirKey)
}

// subscriptionScannedFileKeys 把扫描读到的文件（scriptsRoot 下的路径，与候选命令同一个根）转成字典键集合。
func subscriptionScannedFileKeys(index subscriptionScriptIndex, scriptsRoot string, files []string) map[string]bool {
	keys := make(map[string]bool, len(files))
	for _, file := range files {
		rel, err := filepath.Rel(scriptsRoot, file)
		if err != nil {
			continue
		}
		if key, ok := index.normalize(rel); ok {
			keys[key] = true
		}
	}
	return keys
}

// sortedSubscriptionCandidateCommands 固定候选的处理顺序：map 遍历是随机的，日志顺序不该每次拉取都不一样。
func sortedSubscriptionCandidateCommands(candidates map[string]subscriptionTaskCandidate) []string {
	commands := make([]string, 0, len(candidates))
	for command := range candidates {
		commands = append(commands, command)
	}
	sort.Strings(commands)
	return commands
}

// subscriptionAdoptPool 是「接管」分支的候选池：库里所有不归本订阅管的任务，按命令原文与脚本键各建一张索引。
// 全表加载后在 Go 里过滤，不写 NOT IN（空集合时 GORM 生成 NOT IN (NULL)，一行都查不出来）。
type subscriptionAdoptPool struct {
	byCommand map[string][]*model.Task
	byKey     map[string][]*model.Task
	taken     map[uint]bool
}

func loadSubscriptionAdoptPool(index subscriptionScriptIndex, managed []model.Task) (*subscriptionAdoptPool, error) {
	var all []model.Task
	if err := database.DB.Order("id").Find(&all).Error; err != nil {
		return nil, err
	}
	managedIDs := make(map[uint]bool, len(managed))
	for i := range managed {
		managedIDs[managed[i].ID] = true
	}
	pool := &subscriptionAdoptPool{
		byCommand: make(map[string][]*model.Task),
		byKey:     make(map[string][]*model.Task),
		taken:     make(map[uint]bool),
	}
	for i := range all {
		task := &all[i]
		if managedIDs[task.ID] {
			continue
		}
		command := strings.TrimSpace(task.Command)
		pool.byCommand[command] = append(pool.byCommand[command], task)
		if key, ok := index.scriptKeyOf(task.Command); ok {
			pool.byKey[key] = append(pool.byKey[key], task)
		}
	}
	return pool, nil
}

// take 取出命令原文与候选相同、或脚本键相同的任务（按 id 升序、去重）；取出过的不会再被别的候选取到。
func (p *subscriptionAdoptPool) take(command, key string, keyOK bool) []*model.Task {
	if p == nil {
		return nil
	}
	var matches []*model.Task
	pick := func(tasks []*model.Task) {
		for _, task := range tasks {
			if !p.taken[task.ID] {
				p.taken[task.ID] = true
				matches = append(matches, task)
			}
		}
	}
	pick(p.byCommand[command])
	if keyOK {
		pick(p.byKey[key])
	}
	sort.Slice(matches, func(i, j int) bool { return matches[i].ID < matches[j].ID })
	return matches
}
