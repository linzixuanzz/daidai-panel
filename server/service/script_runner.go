package service

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"daidai-panel/pkg/pathutil"
)

// ScriptResult 只回报退出码。#159 修复 D 删掉了原来的 Output / Truncated：
// 那份整段输出没有任何调用方读（执行器自己从 onOutput 收尾部），白白在堆里多存一份。
type ScriptResult struct {
	ReturnCode int
}

// 日志回调现在直接传原始输出片段。
// 这样可以把裸 \r 也保留下来，让前端有机会按终端语义做单行覆盖刷新。
type OnOutputFunc func(chunk string)

type OnProcessStartFunc func(process *os.Process)

type commandExecutionMode string

const (
	commandModeNormal commandExecutionMode = "normal"
	commandModeNow    commandExecutionMode = "now"
	commandModeConc   commandExecutionMode = "conc"
	commandModeDesi   commandExecutionMode = "desi"
)

type CommandExecutionPlan struct {
	Interpreter        string
	FullPath           string
	ManagedCommand     string
	PythonModule       string
	WorkDir            string
	ScriptArgs         []string
	TimeoutOverride    *int
	SkipRandomDelay    bool
	SuppressLiveOutput bool
	Mode               commandExecutionMode
	EnvName            string
	AccountSpec        string

	// ScriptToken 是命令里被认作脚本路径的那段原始文本（多个 token 按空格拼回），如 "./a.py"、"demo folder/my script.py"。
	// 执行链路不读它。它存在的原因：FullPath 已经被 ResolveWithinBase 用 EvalSymlinks 解析过，
	// 拿不到用户写的字面路径；而「删除任务时一并删除脚本」必须对字面路径做 Lstat 才能认出软链接
	// （见 task_script_target.go）。只在解析出脚本文件时赋值，托管命令与 python -m 为空串。
	ScriptToken string

	ShouldCleanupProcessGroup func() bool // #159 修复 A：主命令结束后要不要清理它的进程组；nil = 不清（只有任务执行器的 runTask 会挂上判定）
}

type taskAccountSelection struct {
	Index int
	Value string
}

func RunCommand(command, scriptsDir string, timeout int, envVars map[string]string, maxLogSize int, onOutput OnOutputFunc, onProcessStart ...OnProcessStartFunc) (*ScriptResult, *os.Process, error) {
	plan, err := ParseCommandExecutionPlan(command, scriptsDir)
	if err != nil {
		return nil, nil, err
	}
	return RunCommandWithPlan(plan, timeout, envVars, maxLogSize, onOutput, onProcessStart...)
}

func RunCommandWithPlan(plan *CommandExecutionPlan, timeout int, envVars map[string]string, maxLogSize int, onOutput OnOutputFunc, onProcessStart ...OnProcessStartFunc) (*ScriptResult, *os.Process, error) {
	if plan == nil {
		return nil, nil, fmt.Errorf("命令执行计划不能为空")
	}

	effectiveTimeout := timeout
	if plan.TimeoutOverride != nil && *plan.TimeoutOverride > 0 {
		effectiveTimeout = *plan.TimeoutOverride
	}

	if plan.Mode == commandModeConc {
		return runConcurrentCommand(plan, effectiveTimeout, envVars, maxLogSize, onOutput, onProcessStart...)
	}

	resolvedEnv, err := applyCommandEnvOverrides(plan, envVars)
	if err != nil {
		return nil, nil, err
	}

	// 放在 desi 收窄之后：desi 只把选中的账号写回变量，按这里的口径统计才是子进程真实要背的量。
	if onOutput != nil {
		// #187：desi 只把选中的账号交给脚本，脚本按自己收到的列表从 1 编号，用户会以为「跑的都是账号 1」，
		// 这里打一行说明。上面 applyCommandEnvOverrides 已经按同样的参数解析成功过，这里再解析一次不会出错。
		if plan.Mode == commandModeDesi {
			if selections, selectErr := resolveTaskAccountSelections(envVars, plan.EnvName, plan.AccountSpec); selectErr == nil {
				onOutput(taskAccountModeNotice(plan, len(splitTaskEnvValues(envVars[plan.EnvName])), selections) + "\n")
			}
		}
		emitRuntimeEnvLimitWarnings(resolvedEnv, plan.Interpreter, func(line string) {
			onOutput(line + "\n")
		})
	}

	return runSingleCommand(plan, effectiveTimeout, resolvedEnv, maxLogSize, onOutput, onProcessStart...)
}

var extInterpreterMap = map[string]string{
	".py":  "python3",
	".js":  "node",
	".mjs": "node",
	".ts":  "ts-node",
	".sh":  "bash",
	".go":  "go",
}

var desiInterpreterMap = map[string]string{
	".js":  "node",
	".mjs": "node",
	".py":  "python3",
	".ts":  "ts-node",
	".sh":  "bash",
	".go":  "go",
}

// commandRunnerKind 是执行器按命令首词选定的解析分支。
type commandRunnerKind int

const (
	commandRunnerUnsupported commandRunnerKind = iota
	commandRunnerTask
	commandRunnerDesi
	commandRunnerInterpreter
	commandRunnerManaged
)

// classifyCommandRunner 是「首词认不认、按哪条分支解析」的唯一判定。
// 订阅同步按脚本认任务（#125）也用它，不另写一份解释器列表：两边口径一旦分叉，
// 执行器能跑的命令在同步里就认不出来，又会新建重复任务。
func classifyCommandRunner(first string) commandRunnerKind {
	switch first {
	case "task":
		return commandRunnerTask
	case "desi":
		return commandRunnerDesi
	case "python", "python3", "python3.10", "python3.11", "python3.12", "node", "ts-node", "bash", "go":
		return commandRunnerInterpreter
	default:
		if isManagedExecutableName(first) {
			return commandRunnerManaged
		}
		return commandRunnerUnsupported
	}
}

func ParseCommandExecutionPlan(command, scriptsDir string) (*CommandExecutionPlan, error) {
	tokens, err := splitCommandTokens(command)
	if err != nil {
		return nil, err
	}
	if len(tokens) == 0 {
		return nil, fmt.Errorf("命令格式无效")
	}

	switch classifyCommandRunner(tokens[0]) {
	case commandRunnerTask:
		return parseTaskCommandPlan(tokens[1:], scriptsDir, "")
	case commandRunnerDesi:
		return parseTaskCommandPlan(tokens[1:], scriptsDir, commandModeDesi)
	case commandRunnerInterpreter:
		return parseInterpreterCommandPlan(tokens[0], tokens[1:], scriptsDir)
	case commandRunnerManaged:
		return parseManagedCommandPlan(tokens, scriptsDir)
	default:
		return nil, fmt.Errorf("不支持的解释器: %s", tokens[0])
	}
}

func parseTaskCommandPlan(tokens []string, scriptsDir string, forcedMode commandExecutionMode) (*CommandExecutionPlan, error) {
	if len(tokens) == 0 {
		return nil, fmt.Errorf("命令格式无效，缺少脚本路径或依赖命令名")
	}

	plan := &CommandExecutionPlan{
		Mode: forcedMode,
	}

	idx := 0
	for idx < len(tokens) {
		switch tokens[idx] {
		case "-m":
			if idx+1 >= len(tokens) {
				return nil, fmt.Errorf("缺少 -m 对应的超时时间")
			}
			timeoutSeconds, err := parseTaskTimeoutSeconds(tokens[idx+1])
			if err != nil {
				return nil, err
			}
			plan.TimeoutOverride = &timeoutSeconds
			idx += 2
		case "-l":
			idx++
		default:
			goto optionsDone
		}
	}

optionsDone:
	remainingTokens := tokens[idx:]
	taskShellTokens, scriptArgs := splitTaskShellAndScriptArgs(remainingTokens)
	if len(taskShellTokens) == 0 {
		return nil, fmt.Errorf("命令格式无效，缺少脚本路径或依赖命令名")
	}

	fullPath, pathTokenCount, err := findTaskScriptTarget(taskShellTokens, scriptsDir, forcedMode)
	if err != nil {
		managedCommand, managedTokenCount, managedErr := findTaskManagedCommandTarget(taskShellTokens, forcedMode)
		if managedErr != nil {
			return nil, err
		}
		// Python / Node 依赖安装后会在托管 bin 目录暴露可执行命令，例如 dailycheckin。
		// 这类命令不是脚本文件，所以不能继续按 scriptsDir 里的 .py/.js 路径校验。
		plan.ManagedCommand = managedCommand
		plan.WorkDir = scriptsDir
		plan.ScriptArgs = scriptArgs
		remainder := taskShellTokens[managedTokenCount:]
		return applyTaskCommandRemainder(plan, remainder, forcedMode)
	}

	plan.FullPath = fullPath
	plan.ScriptToken = strings.Join(taskShellTokens[:pathTokenCount], " ")
	plan.WorkDir = filepath.Dir(fullPath)
	plan.ScriptArgs = scriptArgs
	remainder := taskShellTokens[pathTokenCount:]

	ext := strings.ToLower(filepath.Ext(fullPath))
	mapped, ok := extInterpreterMap[ext]
	if !ok {
		if forcedMode == commandModeDesi {
			return nil, fmt.Errorf("desi 命令不支持的文件扩展名: %s", ext)
		}
		return nil, fmt.Errorf("task 命令不支持的文件扩展名: %s", ext)
	}
	plan.Interpreter = mapped
	return applyTaskCommandRemainder(plan, remainder, forcedMode)
}

func applyTaskCommandRemainder(plan *CommandExecutionPlan, remainder []string, forcedMode commandExecutionMode) (*CommandExecutionPlan, error) {
	if forcedMode == commandModeDesi {
		if len(remainder) == 0 {
			return nil, fmt.Errorf("desi 命令缺少环境变量名称")
		}
		plan.Mode = commandModeDesi
		plan.SkipRandomDelay = true
		plan.EnvName = remainder[0]
		plan.AccountSpec = strings.Join(remainder[1:], " ")
		return plan, nil
	}

	if len(remainder) == 0 {
		plan.Mode = commandModeNormal
		return plan, nil
	}

	switch remainder[0] {
	case "now":
		if len(remainder) != 1 {
			return nil, fmt.Errorf("now 模式不支持额外参数")
		}
		plan.Mode = commandModeNow
		plan.SkipRandomDelay = true
	case "conc":
		if len(remainder) < 2 {
			return nil, fmt.Errorf("conc 模式缺少环境变量名称")
		}
		plan.Mode = commandModeConc
		plan.SkipRandomDelay = true
		plan.SuppressLiveOutput = true
		plan.EnvName = remainder[1]
		plan.AccountSpec = strings.Join(remainder[2:], " ")
	case "desi":
		if len(remainder) < 2 {
			return nil, fmt.Errorf("desi 命令缺少环境变量名称")
		}
		plan.Mode = commandModeDesi
		plan.SkipRandomDelay = true
		plan.EnvName = remainder[1]
		plan.AccountSpec = strings.Join(remainder[2:], " ")
	default:
		plan.ScriptArgs = append(remainder, plan.ScriptArgs...)
		plan.SkipRandomDelay = true
	}

	return plan, nil
}

func parseInterpreterCommandPlan(interpreter string, tokens []string, scriptsDir string) (*CommandExecutionPlan, error) {
	if len(tokens) == 0 {
		return nil, fmt.Errorf("命令格式无效，格式: <解释器> <脚本路径>")
	}

	if IsPythonInterpreter(interpreter) && tokens[0] == "-m" {
		if len(tokens) < 2 {
			return nil, fmt.Errorf("python -m 命令缺少模块名")
		}
		if !isSafePythonModuleName(tokens[1]) {
			return nil, fmt.Errorf("python 模块名无效: %s", tokens[1])
		}
		return &CommandExecutionPlan{
			Interpreter:  interpreter,
			PythonModule: tokens[1],
			WorkDir:      scriptsDir,
			ScriptArgs:   append([]string{}, tokens[2:]...),
			Mode:         commandModeNormal,
		}, nil
	}

	fullPath, pathTokenCount, err := findScriptTarget(tokens, scriptsDir)
	if err != nil {
		return nil, err
	}

	return &CommandExecutionPlan{
		Interpreter: interpreter,
		FullPath:    fullPath,
		ScriptToken: strings.Join(tokens[:pathTokenCount], " "),
		WorkDir:     filepath.Dir(fullPath),
		ScriptArgs:  append([]string{}, tokens[pathTokenCount:]...),
		Mode:        commandModeNormal,
	}, nil
}

func parseManagedCommandPlan(tokens []string, scriptsDir string) (*CommandExecutionPlan, error) {
	if len(tokens) == 0 || !isManagedExecutableName(tokens[0]) {
		return nil, fmt.Errorf("命令格式无效")
	}
	return &CommandExecutionPlan{
		ManagedCommand: tokens[0],
		WorkDir:        scriptsDir,
		ScriptArgs:     append([]string{}, tokens[1:]...),
		Mode:           commandModeNormal,
	}, nil
}

func splitCommandTokens(command string) ([]string, error) {
	tokens := make([]string, 0)
	var current strings.Builder
	var quote rune
	escaped := false

	flush := func() {
		if current.Len() == 0 {
			return
		}
		tokens = append(tokens, current.String())
		current.Reset()
	}

	for _, r := range command {
		if escaped {
			if r == '\'' || r == '"' || r == '\\' || unicode.IsSpace(r) {
				current.WriteRune(r)
			} else {
				current.WriteRune('\\')
				current.WriteRune(r)
			}
			escaped = false
			continue
		}

		if r == '\\' && quote != '\'' {
			escaped = true
			continue
		}

		if quote != 0 {
			if r == quote {
				quote = 0
				continue
			}
			current.WriteRune(r)
			continue
		}

		if r == '\'' || r == '"' {
			quote = r
			continue
		}

		if unicode.IsSpace(r) {
			flush()
			continue
		}

		current.WriteRune(r)
	}

	if escaped {
		current.WriteRune('\\')
	}
	if quote != 0 {
		return nil, fmt.Errorf("命令引号未闭合")
	}
	flush()
	return tokens, nil
}

func splitTaskShellAndScriptArgs(tokens []string) ([]string, []string) {
	for idx, token := range tokens {
		if token == "--" {
			return tokens[:idx], tokens[idx+1:]
		}
	}
	return tokens, nil
}

func findTaskScriptTarget(tokens []string, scriptsDir string, forcedMode commandExecutionMode) (string, int, error) {
	var bestPath string
	bestCount := 0
	var lastResolveErr error

	for count := 1; count <= len(tokens); count++ {
		candidate := strings.Join(tokens[:count], " ")
		if !isSupportedScriptExtension(candidate) {
			continue
		}

		fullPath, err := resolveCommandScriptPath(candidate, scriptsDir)
		if err != nil {
			lastResolveErr = err
			continue
		}

		remainder := tokens[count:]
		if !isValidTaskRemainder(remainder, forcedMode) {
			continue
		}

		bestPath = fullPath
		bestCount = count
	}

	if bestCount == 0 {
		if lastResolveErr != nil {
			return "", 0, lastResolveErr
		}
		return "", 0, fmt.Errorf("脚本不存在或命令格式无效")
	}

	return bestPath, bestCount, nil
}

func findTaskManagedCommandTarget(tokens []string, forcedMode commandExecutionMode) (string, int, error) {
	if len(tokens) == 0 {
		return "", 0, fmt.Errorf("命令格式无效，缺少依赖命令名")
	}
	if filepath.Ext(tokens[0]) != "" || !isManagedExecutableName(tokens[0]) {
		return "", 0, fmt.Errorf("脚本不存在或命令格式无效")
	}
	if !isValidTaskRemainder(tokens[1:], forcedMode) {
		return "", 0, fmt.Errorf("脚本不存在或命令格式无效")
	}
	return tokens[0], 1, nil
}

func findScriptTarget(tokens []string, scriptsDir string) (string, int, error) {
	var bestPath string
	bestCount := 0
	var lastResolveErr error

	for count := 1; count <= len(tokens); count++ {
		candidate := strings.Join(tokens[:count], " ")
		if !isSupportedScriptExtension(candidate) {
			continue
		}

		fullPath, err := resolveCommandScriptPath(candidate, scriptsDir)
		if err != nil {
			lastResolveErr = err
			continue
		}

		bestPath = fullPath
		bestCount = count
	}

	if bestCount == 0 {
		if lastResolveErr != nil {
			return "", 0, lastResolveErr
		}
		return "", 0, fmt.Errorf("脚本不存在或命令格式无效")
	}

	return bestPath, bestCount, nil
}

func isValidTaskRemainder(tokens []string, forcedMode commandExecutionMode) bool {
	if forcedMode == commandModeDesi {
		return len(tokens) >= 1
	}
	if len(tokens) == 0 {
		return true
	}

	switch tokens[0] {
	case "now":
		return len(tokens) == 1
	case "conc", "desi":
		return len(tokens) >= 2
	default:
		return true
	}
}

func isSupportedScriptExtension(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".py", ".js", ".mjs", ".ts", ".sh", ".go":
		return true
	default:
		return false
	}
}

func isManagedExecutableName(name string) bool {
	name = strings.TrimSpace(name)
	if name == "" || strings.HasPrefix(name, "-") || name == "." || name == ".." {
		return false
	}
	for _, r := range name {
		if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '-' || r == '.' {
			continue
		}
		return false
	}
	return true
}

func isSafePythonModuleName(name string) bool {
	name = strings.TrimSpace(name)
	if name == "" || strings.HasPrefix(name, ".") || strings.HasSuffix(name, ".") || strings.Contains(name, "..") {
		return false
	}
	for _, r := range name {
		if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '.' {
			continue
		}
		return false
	}
	return true
}

func resolveCommandScriptPath(scriptPath, scriptsDir string) (string, error) {
	dangerous := []string{"..", "~", "$", "`", ";", "|", "&", ">", "<"}
	for _, d := range dangerous {
		if strings.Contains(scriptPath, d) {
			return "", fmt.Errorf("脚本路径包含危险字符: %s", d)
		}
	}

	fullPath, err := pathutil.ResolveWithinBase(scriptsDir, scriptPath, true)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(fullPath); os.IsNotExist(err) {
		return "", fmt.Errorf("脚本不存在: %s", scriptPath)
	}
	return fullPath, nil
}

func parseTaskTimeoutSeconds(raw string) (int, error) {
	value := strings.TrimSpace(strings.ToLower(raw))
	if value == "" {
		return 0, fmt.Errorf("超时时间不能为空")
	}

	multiplier := 1
	switch suffix := value[len(value)-1]; suffix {
	case 's':
		value = value[:len(value)-1]
	case 'm':
		value = value[:len(value)-1]
		multiplier = 60
	case 'h':
		value = value[:len(value)-1]
		multiplier = 3600
	case 'd':
		value = value[:len(value)-1]
		multiplier = 86400
	}

	seconds, err := strconv.Atoi(value)
	if err != nil || seconds <= 0 {
		return 0, fmt.Errorf("无效的超时时间: %s", raw)
	}

	return seconds * multiplier, nil
}

// leftoverProcessCleanupNotice 是 #159 修复 A 清理了残留后台进程时打进任务日志的那一行，前缀已登记到 panelMetaLinePrefixes。
// 前后各一个 \n，和「被信号终止」那行同形：开头的 \n 让 conc 模式的 [ENV#N] 前缀单独占一行、提示行本身从行首开始，
// isPanelMetaLine 才认得出；结尾的 \n 免得后面的「=== 执行结束」粘在这一行末尾。
// 写的是「已发送结束信号，5 秒内不退出会被强制结束」而不是「已结束」：补刀在单独的协程里，面板在这 5 秒内退出时补不上。
const leftoverProcessCleanupNotice = "\n[已结束残留的后台进程：主命令退出后，它的进程组里还有进程在运行（多为 nohup、& 放到后台的），" +
	"已发送结束信号，5 秒内不退出会被强制结束。需要常驻请用 setsid nohup 命令 >/dev/null 2>&1 & 启动，" +
	"或在「系统设置 → 任务运行」关闭「任务结束后清理残留进程」]\n"

func runSingleCommand(plan *CommandExecutionPlan, timeout int, envVars map[string]string, maxLogSize int, onOutput OnOutputFunc, onProcessStart ...OnProcessStartFunc) (*ScriptResult, *os.Process, error) {
	cmd, cleanup, err := buildCmd(plan, filepath.Dir(plan.FullPath), envVars)
	if err != nil {
		return nil, nil, err
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cleanup()
		return nil, nil, fmt.Errorf("failed to create stdout pipe: %w", err)
	}
	cmd.Stderr = cmd.Stdout

	if err := cmd.Start(); err != nil {
		cleanup()
		return nil, nil, fmt.Errorf("failed to start process: %w", err)
	}

	process := cmd.Process
	if len(onProcessStart) > 0 && onProcessStart[0] != nil {
		onProcessStart[0](process)
	}

	// totalSize / truncated 决定 onOutput 什么时候开始截断（#159 修复 D 删掉了旁边那份没人读的 outputBuilder）
	totalSize := 0
	truncated := false

	emitChunk := func(chunk string) {
		if truncated {
			return
		}
		if totalSize >= maxLogSize {
			truncated = true
			msg := "\n[日志已截断，超过最大大小限制]"
			if onOutput != nil {
				onOutput(msg)
			}
			return
		}
		totalSize += len(chunk)
		if onOutput != nil {
			onOutput(chunk)
		}
	}

	var timeoutDuration time.Duration
	if timeout > 0 {
		timeoutDuration = time.Duration(timeout) * time.Second
	}

	waitErr, timedOut, readErr := pumpAndWait(cmd, stdout, timeoutDuration, emitChunk)
	cleanup()

	returnCode := exitCodeFromWaitError(waitErr)
	if timedOut {
		// 超时被杀时进程的退出码没有意义（是信号导致的），沿用原来的约定值 -1
		returnCode = -1
		msg := fmt.Sprintf("\n[任务超时，已在 %d 秒后终止]", timeout)
		if onOutput != nil {
			onOutput(msg)
		}
	} else if signalName := describeTerminationSignal(waitErr); signalName != "" {
		// 【进程被信号杀掉时，给用户一个能看懂的解释】
		// 被信号终止的进程在 Go 里 ExitCode() 恒为 -1，日志里只会留下「退出码 -1」，
		// 用户完全不知道是内存超限被 OOM Killer 杀了、还是自己点了停止、还是被外部脚本 kill 了。
		// 这一行就是把这个静默现象变成可诊断的。
		//
		// 🔴 【必须先排除 timedOut，顺序不能反】
		// 超时分支自己会对整组先发 SIGTERM、宽限期过后还在再 SIGKILL（#159 修复 B），waitErr 里同样是「被信号终止」。
		// 不排除的话一次超时会同时打出「任务超时」和「被信号终止」两行互相矛盾的解释，
		// 而且超时分支已经把退出码硬置成 -1，再解释一遍毫无信息量。
		//
		// 写法照抄上面的超时分支（直接写 onOutput），
		// 所以 conc 模式下它和超时提示形状一致：会被 prefixedOutput 加上 [EnvName#N] 前缀，
		// 开头那个 \n 让前缀单独占一行 —— 这是既有行为，不在本次改动范围内。
		// Windows 上 describeTerminationSignal 恒返回空串，这个分支永远不会进。
		//
		// 结尾必须带换行：后面紧跟的往往是执行器自己的提示行（「[面板正在关闭，任务已中断]」）或「=== 执行结束」，
		// 不换行就粘在这一行行尾，isPanelMetaLine 按行首认不出那条提示，日志也难读。
		msg := fmt.Sprintf("\n[脚本进程被信号终止：%s（退出码 -1）。常见原因：内存超限被系统 OOM Killer 杀掉、面板或用户手动停止、外部 kill]\n", signalName)
		if onOutput != nil {
			onOutput(msg)
		}
	}
	if readErr != nil && readErr != io.EOF && totalSize < maxLogSize && !truncated {
		if !isBenignProcessPipeReadError(readErr) {
			emitChunk(fmt.Sprintf("[读取脚本输出失败] %s\n", readErr.Error()))
		}
	}

	// #159 修复 A：主命令自己结束（没有超时）以后，进程组里还活着的，只可能是它放到后台、又不占输出管道的子孙进程
	// （nohup … >/dev/null &、Popen(DEVNULL)、没调 quit 的浏览器）。不清理的话它们会被 PID 1 收养，一直活到重启容器，
	// 「重启面板」也释放不了。setsid 起的进程不在这个组里，不受影响。超时分支已经对整组 TERM→KILL 过，这里不再重复。
	// 要不要清由执行器决定（plan.ShouldCleanupProcessGroup）：开机任务、总开关关闭、这次执行已被手动停止或面板正在关停时，判定都返回 false；
	// 钩子（前置 / 后置脚本、task_before.sh 等）不经过这里，所以只覆盖主命令（conc 每个账号、desi 都走 runSingleCommand）。
	// 先用 processGroupAlive 看一眼组里还有没有进程，组本来就空时不写那行提示。
	// 用不阻塞的 TerminateProcessGroup：退出码、日志都已经是最终结果，不为残留进程推迟结算、多占并发槽位。
	// 写 onOutput 而不是 emitChunk：和上面「任务超时」「被信号终止」两行同一写法，日志被截断后这一行照样会出现。
	if !timedOut && plan.ShouldCleanupProcessGroup != nil && plan.ShouldCleanupProcessGroup() && processGroupAlive(process.Pid) {
		TerminateProcessGroup(process)
		if onOutput != nil {
			onOutput(leftoverProcessCleanupNotice)
		}
	}

	return &ScriptResult{
		ReturnCode: returnCode,
	}, process, nil
}

// pumpAndWait 把子进程的输出读干净，然后才等待它退出。
//
// 🔴 【顺序就是这个函数存在的全部理由，改它之前先读完这段】
//
// os/exec 对 StdoutPipe 的约定是：
//
//	"Wait will close the pipe after seeing the command exit ... it is incorrect
//	 to call Wait before all reads from the pipe have completed."
//
// 也就是说 cmd.Wait() 看到进程退出后会【关闭管道】。如果此时读协程还没把管道里
// 剩下的数据读完，那部分输出会连同 fd 一起消失 —— 而且是静默消失：读协程拿到的
// 是 "file already closed"，恰好落在 isBenignProcessPipeReadError 的良性名单里，
// 连一行报错都不会留下。
//
// 这正是 issue #102「实时日志显示不全、最后几秒不显示」的成因，也解释了用户观察到的
// 两个现象：「有些脚本能显示全」（输出少、读得快，抢在 Wait 前读完了）、
// 「脚本末尾加 5 秒延时就能显示全」（延时把 Wait 推后，给了读协程时间）。
//
// 正确顺序只有一种：**先等读协程读到 EOF，再调 Wait**。
// 进程退出后写端关闭，读端必然拿到 EOF，所以这样不会卡死；
// 超时分支里先对整组 SIGTERM、宽限期过后还在再 SIGKILL（#159 修复 B）也是同理，进程都结束了就会触发 EOF。
//
// 返回值：cmd.Wait() 的原始错误（保留 *exec.ExitError 供调用方断言）、是否因超时被杀、
// 读取过程中的错误（EOF 视为正常结束，归一成 nil）。
// 调用方拿到返回值时进程已经结束，可以安全地做 cleanup。
func pumpAndWait(cmd *exec.Cmd, stdout io.Reader, timeout time.Duration, emit func(string)) (error, bool, error) {
	// 不用 Scanner 按行切，因为终端进度条常用裸 \r 覆盖当前行。
	// 这里按字节流切片，把 \n / \r / \r\n 都当成边界保留下来，
	// 让实时日志、历史日志和前端渲染都能还原真实终端语义。
	reader := bufio.NewReaderSize(stdout, 256*1024)

	drained := make(chan error, 1)
	go func() {
		drained <- pumpTerminalChunks(reader, emit)
	}()

	type outcome struct {
		readErr error
		waitErr error
	}
	outcomeCh := make(chan outcome, 1)
	go func() {
		// ⚠️ 这两行的先后顺序是本函数的核心契约，不要交换（理由见上方注释）。
		//
		// 实测数据（把这两行换回旧顺序、交叉编译成 Linux 版跑真面板）：
		// 一个输出 2000 行后立刻退出的 Python 脚本，落库的日志只剩 23 行 ——
		// 丢了 98.85%，而且末尾照样写着「执行结束 退出码 0」，
		// 用户完全看不出日志被截断过。
		// 单元测试 TestPumpAndWaitKeepsTailOutput 守着这一条，交换即红。
		readErr := <-drained
		waitErr := cmd.Wait()
		outcomeCh <- outcome{readErr: readErr, waitErr: waitErr}
	}()

	var timerC <-chan time.Time
	if timeout > 0 {
		timer := time.NewTimer(timeout)
		defer timer.Stop()
		timerC = timer.C
	}

	select {
	case oc := <-outcomeCh:
		return oc.waitErr, false, normalizePumpReadError(oc.readErr)
	case <-timerC:
		// #159 修复 B：先对整组发 SIGTERM，给 trap 了 TERM 的脚本最多 groupTermGrace（5 秒）收尾；
		// 组里的进程都退了就提前结束，到点还有进程（包括攥着管道、忽略 TERM 的后台进程）再整组 SIGKILL。
		// 「超时杀整组」的语义不变。Windows 没有 SIGTERM（signalGroupTerm 恒为 false），照旧立刻 KillProcessGroup。
		// 等的是「整组退空」而不是 outcome：outcome 到了只说明主进程和攥着输出管道的进程退了，
		// 只等它会放过不攥管道、又忽略 TERM 的后台进程，而超时之后修复 A 不再清理，它们就留下来了。
		// 任务主命令、任务钩子（60 秒）、订阅的拉取前指令 / 拉取后钩子（900 秒）都经这里，一视同仁。
		if !signalGroupTerm(cmd.Process.Pid) || !waitProcessGroupGone(cmd.Process.Pid, groupTermGrace) {
			KillProcessGroup(cmd.Process)
		}
		// 进程都结束 → 管道写端关闭 → 读协程读到 EOF → drained 有值 → Wait 返回。
		// 所以这里等 outcomeCh 不会永久阻塞。
		oc := <-outcomeCh
		return oc.waitErr, true, normalizePumpReadError(oc.readErr)
	}
}

// pumpTerminalChunks 按终端语义把输出切成块交给 emit：
// \n 落一新行、\r\n 整对作为一个边界、裸 \r 也是边界（进度条覆盖当前行）。
// 读到结尾时把残留的不完整块也发出去，返回终止读取的那个 error（正常结束是 io.EOF）。
func pumpTerminalChunks(reader *bufio.Reader, emit func(string)) error {
	var chunkBuf strings.Builder
	for {
		text, err := reader.ReadString('\n')
		for i := 0; i < len(text); i++ {
			chunkBuf.WriteByte(text[i])
			if text[i] == '\r' {
				// 兼容 Windows 风格 \r\n，整对一起作为一个边界发出。
				if i+1 < len(text) && text[i+1] == '\n' {
					chunkBuf.WriteByte(text[i+1])
					i++
				}
				emit(chunkBuf.String())
				chunkBuf.Reset()
				continue
			}
			if text[i] == '\n' {
				emit(chunkBuf.String())
				chunkBuf.Reset()
			}
		}
		if err != nil {
			// 收尾：没有换行结束的最后一段也要发出去，否则脚本用 print(end='')
			// 或者进度条结尾不带换行时，最后一块会被吞掉。
			if chunkBuf.Len() > 0 {
				emit(chunkBuf.String())
				chunkBuf.Reset()
			}
			return err
		}
	}
}

func exitCodeFromWaitError(err error) int {
	if err == nil {
		return 0
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		return exitErr.ExitCode()
	}
	return 1
}

// normalizePumpReadError 把「读到结尾」统一成 nil，只把真正的读失败往外传。
func normalizePumpReadError(err error) error {
	if err == io.EOF {
		return nil
	}
	return err
}

// isBenignProcessPipeReadError 判断一个读错误要不要写进用户可见的日志。
//
// ⚠️ 这份名单曾经掩盖过一个真 bug（issue #102）。
//
// 前两项 "file already closed" / "read |0:" 正是 cmd.Wait() 抢在读协程之前关闭
// StdoutPipe 时的报错。在 pumpAndWait 把顺序修正之前，它们每次都会命中这里被判成
// 良性、静默丢弃 —— 于是「日志少了最后几行」这个现象连一条线索都不留。
//
// 现在 pumpAndWait 保证「先排空、再 Wait」，正常路径读到的是 io.EOF，
// 超时路径是先 kill 再读到 EOF，**都不会**再产生这两个错误。
// 所以如果你在日志里又看到它们被这个函数吞掉，几乎可以断定是有人把那个顺序改回去了，
// 先去看 TestPumpAndWaitKeepsTailOutput 是不是红的。
//
// 名单继续保留是防御性的：进程被外部强杀等边缘情况下，用户看到一行
// 「[读取脚本输出失败] file already closed」没有任何帮助。
func isBenignProcessPipeReadError(err error) bool {
	if err == nil || err == io.EOF {
		return true
	}

	text := strings.ToLower(strings.TrimSpace(err.Error()))
	if text == "" {
		return false
	}

	benignMarkers := []string{
		"file already closed",
		"read |0:",
		"the pipe has been ended",
		"handle is invalid",
		"io: read/write on closed pipe",
	}
	for _, marker := range benignMarkers {
		if strings.Contains(text, marker) {
			return true
		}
	}

	return false
}

func runConcurrentCommand(plan *CommandExecutionPlan, timeout int, envVars map[string]string, maxLogSize int, onOutput OnOutputFunc, onProcessStart ...OnProcessStartFunc) (*ScriptResult, *os.Process, error) {
	selections, err := resolveTaskAccountSelections(envVars, plan.EnvName, plan.AccountSpec)
	if err != nil {
		return nil, nil, err
	}
	if len(selections) == 0 {
		return nil, nil, fmt.Errorf("未匹配到可执行的账号")
	}

	// totalSize / truncated 决定 onOutput 什么时候开始截断（#159 修复 D 删掉了旁边那份没人读的 outputBuilder）
	totalSize := 0
	truncated := false
	var outputMu sync.Mutex
	var firstProcess *os.Process
	var firstProcessMu sync.Mutex

	appendLine := func(line string) {
		outputMu.Lock()
		defer outputMu.Unlock()

		// 【为什么要先算一份 text】
		// 进来的东西有两种：
		//   a) conc 自己生成的行（开始执行 / 执行完成 / 执行错误 / 截断标记），末尾【没有】换行；
		//   b) prefixedOutput 转发的脚本原始输出片段，末尾本来就带 \n 或裸 \r。
		// 老写法是无条件给已删掉的 outputBuilder 补一个 "\n"、却完全不给 onOutput 补：
		// 实时流（onOutput）里 a 类行会和紧跟其后的脚本输出粘成一行。
		// 现在统一成「本来就以换行/裸 \r 收尾就不动，否则才补一个 \n」，
		// 既修掉粘行，也不会写重 \n；裸 \r 保持原样，进度条的覆盖刷新语义不受影响。
		text := line
		if !strings.HasSuffix(text, "\n") && !strings.HasSuffix(text, "\r") {
			text += "\n"
		}

		if totalSize < maxLogSize {
			totalSize += len(text)
			if onOutput != nil {
				onOutput(text)
			}
			return
		}

		if !truncated {
			truncated = true
			msg := "[日志已截断，超过最大大小限制]\n"
			if onOutput != nil {
				onOutput(msg)
			}
		}
	}

	// #187：起各账号进程之前先打一行说明（只打一次）。每个进程只拿到自己那一个账号，
	// 脚本日志里的「账号1」其实是该进程分到的账号，用户常误以为全跑成了账号 1。
	// 这行没有 [EnvName#N] 前缀，开头就是登记在 panelMetaLinePrefixes 里的「[按账号并发]」。
	appendLine(taskAccountModeNotice(plan, len(splitTaskEnvValues(envVars[plan.EnvName])), selections))

	// conc 会为每个账号各起一个进程，环境只差 EnvName 这一条，
	// 所以按第一个账号体检一次即可，避免同一段提示刷 N 遍。
	emitRuntimeEnvLimitWarnings(
		applyConcurrentAccountEnv(plan, envVars, selections[0]),
		plan.Interpreter,
		appendLine,
	)

	type concurrentResult struct {
		index      int
		returnCode int
		err        error
	}

	results := make(chan concurrentResult, len(selections))
	var waitGroup sync.WaitGroup

	for _, selection := range selections {
		selection := selection
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()

			selectionEnv := applyConcurrentAccountEnv(plan, envVars, selection)
			prefixedOutput := func(line string) {
				appendLine(fmt.Sprintf("[%s#%d] %s", plan.EnvName, selection.Index, line))
			}
			processCapture := func(process *os.Process) {
				firstProcessMu.Lock()
				if firstProcess == nil {
					firstProcess = process
				}
				firstProcessMu.Unlock()
				if len(onProcessStart) > 0 && onProcessStart[0] != nil {
					onProcessStart[0](process)
				}
			}

			appendLine(fmt.Sprintf("[%s#%d] 开始执行", plan.EnvName, selection.Index))
			result, _, runErr := runSingleCommand(plan, timeout, selectionEnv, maxLogSize, prefixedOutput, processCapture)
			if runErr != nil {
				appendLine(fmt.Sprintf("[%s#%d] 执行错误: %s", plan.EnvName, selection.Index, runErr.Error()))
				results <- concurrentResult{index: selection.Index, returnCode: 1, err: runErr}
				return
			}

			appendLine(fmt.Sprintf("[%s#%d] 执行完成，退出码 %d", plan.EnvName, selection.Index, result.ReturnCode))
			results <- concurrentResult{index: selection.Index, returnCode: result.ReturnCode}
		}()
	}

	waitGroup.Wait()
	close(results)

	overallCode := 0
	var overallErr error
	for result := range results {
		if result.err != nil && overallErr == nil {
			overallErr = result.err
		}
		if result.returnCode != 0 && overallCode == 0 {
			overallCode = result.returnCode
		}
	}

	return &ScriptResult{
		ReturnCode: overallCode,
	}, firstProcess, overallErr
}

// taskAccountNoticeMaxIndexes 是账号说明行里最多列出的序号个数，再多只写「等 N 个」，免得一行拖得太长。
const taskAccountNoticeMaxIndexes = 20

// taskAccountModeNotice 生成 conc / desi 模式打进任务日志的那行说明（#187），其它模式返回空串。
// total 是变量里的账号总数，selections 是本次选中的账号。两种前缀都已登记到 panelMetaLinePrefixes。
func taskAccountModeNotice(plan *CommandExecutionPlan, total int, selections []taskAccountSelection) string {
	// 序号超过 20 个只列前 20 个，后面写「等 N 个」；不超过就是「1、2、3 个」
	shown := selections
	if len(shown) > taskAccountNoticeMaxIndexes {
		shown = shown[:taskAccountNoticeMaxIndexes]
	}
	indexes := make([]string, 0, len(shown))
	for _, selection := range shown {
		indexes = append(indexes, strconv.Itoa(selection.Index))
	}
	indexText := strings.Join(indexes, "、") + " 个"
	if len(selections) > taskAccountNoticeMaxIndexes {
		indexText = fmt.Sprintf("%s 等 %d 个", strings.Join(indexes, "、"), len(selections))
	}

	switch plan.Mode {
	case commandModeConc:
		return fmt.Sprintf("[按账号并发] %s 共 %d 个账号（只算已启用的，按环境变量页顺序），本次运行第 %s，每个账号单独一个进程。每个进程只拿到自己那一个账号，脚本日志里的「账号1」指的是该进程分到的账号，以行首 [%s#序号] 为准",
			plan.EnvName, total, indexText, plan.EnvName)
	case commandModeDesi:
		return fmt.Sprintf("[指定账号] 只把 %s 共 %d 个账号（只算已启用的，按环境变量页顺序）中的第 %s交给脚本，脚本自己的编号会从 1 开始",
			plan.EnvName, total, indexText)
	}
	return ""
}

func resolveTaskAccountSelections(envVars map[string]string, envName, accountSpec string) ([]taskAccountSelection, error) {
	envName = strings.TrimSpace(envName)
	if envName == "" {
		return nil, fmt.Errorf("缺少环境变量名称")
	}

	rawValue, exists := envVars[envName]
	if !exists || strings.TrimSpace(rawValue) == "" {
		return nil, fmt.Errorf("环境变量 %s 不存在或为空", envName)
	}

	values := splitTaskEnvValues(rawValue)
	indices, err := parseTaskAccountSpec(accountSpec, len(values))
	if err != nil {
		return nil, err
	}

	selections := make([]taskAccountSelection, 0, len(indices))
	for _, index := range indices {
		selections = append(selections, taskAccountSelection{
			Index: index,
			Value: values[index-1],
		})
	}
	return selections, nil
}

func parseTaskAccountSpec(spec string, total int) ([]int, error) {
	if total <= 0 {
		return nil, fmt.Errorf("环境变量账号数量为空")
	}

	spec = strings.TrimSpace(spec)
	if spec == "" {
		spec = "1-max"
	}

	tokens := strings.FieldsFunc(spec, func(r rune) bool {
		return unicode.IsSpace(r) || r == ','
	})

	seen := make(map[int]bool)
	indices := make([]int, 0)

	for _, token := range tokens {
		if token == "" {
			continue
		}
		expanded, err := expandTaskAccountToken(token, total)
		if err != nil {
			return nil, err
		}
		for _, index := range expanded {
			if !seen[index] {
				seen[index] = true
				indices = append(indices, index)
			}
		}
	}

	if len(indices) == 0 {
		return nil, fmt.Errorf("未匹配到有效的账号序号")
	}

	return indices, nil
}

func expandTaskAccountToken(token string, total int) ([]int, error) {
	token = strings.TrimSpace(strings.ToLower(token))
	for _, sep := range []string{"-", "~", "_"} {
		if strings.Contains(token, sep) {
			parts := strings.SplitN(token, sep, 2)
			start, err := parseTaskAccountEndpoint(parts[0], total)
			if err != nil {
				return nil, err
			}
			end, err := parseTaskAccountEndpoint(parts[1], total)
			if err != nil {
				return nil, err
			}
			step := 1
			if start > end {
				step = -1
			}
			result := make([]int, 0, absInt(start-end)+1)
			for current := start; ; current += step {
				result = append(result, current)
				if current == end {
					break
				}
			}
			return result, nil
		}
	}

	value, err := parseTaskAccountEndpoint(token, total)
	if err != nil {
		return nil, err
	}
	return []int{value}, nil
}

func parseTaskAccountEndpoint(raw string, total int) (int, error) {
	raw = strings.TrimSpace(strings.ToLower(raw))
	switch raw {
	case "", "max":
		return total, nil
	}

	value, err := strconv.Atoi(raw)
	if err != nil || value < 1 || value > total {
		return 0, fmt.Errorf("无效的账号序号: %s", raw)
	}
	return value, nil
}

func applyCommandEnvOverrides(plan *CommandExecutionPlan, envVars map[string]string) (map[string]string, error) {
	cloned := cloneEnvMap(envVars)
	if plan == nil || plan.Mode != commandModeDesi {
		return cloned, nil
	}

	selections, err := resolveTaskAccountSelections(cloned, plan.EnvName, plan.AccountSpec)
	if err != nil {
		return nil, err
	}

	selectedValues := make([]string, 0, len(selections))
	selectedIndexes := make([]string, 0, len(selections))
	for _, selection := range selections {
		selectedValues = append(selectedValues, selection.Value)
		selectedIndexes = append(selectedIndexes, strconv.Itoa(selection.Index))
	}

	cloned[plan.EnvName] = joinTaskEnvValues(selectedValues)
	cloned["envParam"] = plan.EnvName
	cloned["numParam"] = strings.Join(selectedIndexes, " ")
	cloned["TASK_EXEC_MODE"] = string(plan.Mode)
	cloned["TASK_ENV_NAME"] = plan.EnvName
	cloned["TASK_ACCOUNT_SPEC"] = strings.Join(selectedIndexes, " ")
	return cloned, nil
}

func applyConcurrentAccountEnv(plan *CommandExecutionPlan, envVars map[string]string, selection taskAccountSelection) map[string]string {
	cloned := cloneEnvMap(envVars)
	cloned[plan.EnvName] = selection.Value
	cloned["envParam"] = plan.EnvName
	cloned["numParam"] = strconv.Itoa(selection.Index)
	cloned["TASK_EXEC_MODE"] = string(plan.Mode)
	cloned["TASK_ENV_NAME"] = plan.EnvName
	cloned["TASK_ACCOUNT_SPEC"] = strconv.Itoa(selection.Index)
	cloned["TASK_ACCOUNT_NUMBER"] = strconv.Itoa(selection.Index)
	return cloned
}

func cloneEnvMap(envVars map[string]string) map[string]string {
	cloned := make(map[string]string, len(envVars))
	for key, value := range envVars {
		cloned[key] = value
	}
	return cloned
}

func absInt(value int) int {
	if value < 0 {
		return -value
	}
	return value
}

func buildCmd(plan *CommandExecutionPlan, workDir string, envVars map[string]string) (*exec.Cmd, func(), error) {
	if strings.TrimSpace(plan.WorkDir) != "" {
		workDir = plan.WorkDir
	}
	if strings.TrimSpace(workDir) == "" {
		workDir = "."
	}
	if plan.ManagedCommand != "" {
		return createManagedExecutableCommand(plan.ManagedCommand, plan.ScriptArgs, workDir, envVars)
	}
	if plan.PythonModule != "" {
		return createManagedPythonModuleCommand(plan.Interpreter, plan.PythonModule, plan.ScriptArgs, workDir, envVars)
	}

	helperBaseDir := strings.TrimSpace(envVars["DAIDAI_SCRIPTS_DIR"])
	if helperBaseDir != "" {
		_ = EnsureBuiltinNotifyHelpers(helperBaseDir)
		_ = cleanupManagedHelperCopies(helperBaseDir, filepath.Dir(plan.FullPath))
	}
	if plan.Interpreter == "bash" {
		_ = NormalizeShellScriptFile(plan.FullPath)
	}

	return CreateManagedCommand(plan.Interpreter, plan.FullPath, plan.ScriptArgs, workDir, envVars)
}

func buildEnv(envVars map[string]string) []string {
	safeKeys := []string{"PATH", "HOME", "USER", "LANG", "LC_ALL", "TZ"}
	if runtime.GOOS == "windows" {
		safeKeys = append(safeKeys, "SYSTEMROOT", "PATHEXT", "TEMP", "TMP", "APPDATA", "LOCALAPPDATA", "USERPROFILE")
	}

	env := make([]string, 0)
	for _, key := range safeKeys {
		if val := os.Getenv(key); val != "" {
			env = append(env, key+"="+val)
		}
	}

	dangerousVars := map[string]bool{
		"LD_PRELOAD": true, "LD_LIBRARY_PATH": true, "DYLD_INSERT_LIBRARIES": true,
	}

	for k, v := range envVars {
		if dangerousVars[k] {
			continue
		}
		if strings.ContainsRune(v, 0) {
			continue
		}
		env = append(env, k+"="+v)
	}

	// 实时读取 system_configs.proxy_url，把 HTTP_PROXY / HTTPS_PROXY 等
	// 注入 bash / go 等标准命令的执行环境。Python / Node 走 buildBootstrapProcessEnv 已包含此逻辑。
	return AppendProxyEnv(env)
}

func RunInlineScript(content, scriptsDir string, envVars map[string]string, timeout int, onOutput OnOutputFunc, scriptArgs ...string) error {
	return runInlineScript(content, scriptsDir, envVars, timeout, onOutput, nil, scriptArgs...)
}

// runInlineScript 是 RunInlineScript 的本体。onStart 非空时，钩子进程一启动就回调它：
// 任务执行器借此登记前置 / 后置钩子，面板关停时按进程组杀掉（原来钩子不在任何登记里，关停要白等它跑完）。
func runInlineScript(content, scriptsDir string, envVars map[string]string, timeout int, onOutput OnOutputFunc, onStart OnProcessStartFunc, scriptArgs ...string) error {
	tmpFile := filepath.Join(scriptsDir, fmt.Sprintf(".hook_%d.sh", time.Now().UnixNano()))
	if err := os.WriteFile(tmpFile, NormalizeShellLineEndings([]byte(content)), 0755); err != nil {
		return err
	}
	defer os.Remove(tmpFile)

	cmd, cleanup, err := CreateManagedCommand("bash", tmpFile, scriptArgs, scriptsDir, envVars)
	if err != nil {
		return err
	}
	defer cleanup()

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	cmd.Stderr = cmd.Stdout

	if err := cmd.Start(); err != nil {
		return err
	}
	if onStart != nil {
		onStart(cmd.Process)
	}

	// 走统一的 pumpAndWait：原来这里是「读协程完全不同步 + 直接 cmd.Wait()」，
	// 与 runSingleCommand 是同一个丢尾巴的 bug（见 pumpAndWait 的注释），
	// 而且更严重 —— 连等读协程结束的 channel 都没有。
	emit := func(chunk string) {
		if onOutput != nil {
			onOutput(chunk)
		}
	}
	waitErr, timedOut, _ := pumpAndWait(cmd, stdout, time.Duration(timeout)*time.Second, emit)
	if timedOut {
		return fmt.Errorf("钩子脚本超时，已超过 %d 秒", timeout)
	}
	// 原样返回 cmd.Wait() 的错误（退出码非 0 时是 *exec.ExitError），
	// 调用方对钩子失败的判断逻辑不变。
	return waitErr
}

func RunHookScript(scriptName, scriptsDir string, envVars map[string]string, onOutput OnOutputFunc, scriptArgs ...string) {
	runHookScript(scriptName, scriptsDir, envVars, onOutput, nil, scriptArgs...)
}

// runHookScript 是 RunHookScript 的本体，onStart 的用途同 runInlineScript。
func runHookScript(scriptName, scriptsDir string, envVars map[string]string, onOutput OnOutputFunc, onStart OnProcessStartFunc, scriptArgs ...string) {
	hookPath, err := pathutil.ResolveWithinBase(scriptsDir, scriptName, true)
	if os.IsNotExist(err) {
		return
	}
	if err != nil {
		return
	}
	if err := NormalizeShellScriptFile(hookPath); err != nil {
		if onOutput != nil {
			onOutput(fmt.Sprintf("[hook %s line ending normalize failed: %s]", scriptName, err))
		}
		return
	}

	cmd, cleanup, err := CreateManagedCommand("bash", hookPath, scriptArgs, scriptsDir, envVars)
	if err != nil {
		if onOutput != nil {
			onOutput(fmt.Sprintf("[hook %s failed to prepare: %s]", scriptName, err))
		}
		return
	}
	defer cleanup()

	stdout, _ := cmd.StdoutPipe()
	cmd.Stderr = cmd.Stdout

	if err := cmd.Start(); err != nil {
		if onOutput != nil {
			onOutput(fmt.Sprintf("[hook %s failed to start: %s]", scriptName, err))
		}
		return
	}
	if onStart != nil {
		onStart(cmd.Process)
	}

	// 与上面两处同因：原来读协程不同步、直接 cmd.Wait()，钩子脚本的尾部输出会被吞掉。
	// 统一走 pumpAndWait（见它的注释）。这个函数本身不返回错误，钩子失败只体现在日志里，
	// 所以两个错误值都丢弃，但输出必须是完整的。
	emit := func(chunk string) {
		if onOutput != nil {
			onOutput(chunk)
		}
	}
	pumpAndWait(cmd, stdout, 60*time.Second, emit)
}

func cleanProcessArgs(args []string) []string {
	cleaned := make([]string, 0, len(args))
	for _, arg := range args {
		if strings.ContainsRune(arg, 0) {
			continue
		}
		cleaned = append(cleaned, arg)
	}
	return cleaned
}
