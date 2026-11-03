package handler

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"daidai-panel/config"
	"daidai-panel/service"
)

const (
	// maxRunLogLines 是单次运行在内存里保留的日志行数上限。
	// scanner.Buffer 只限制单行长度，不限制总量：实测 `yes` 跑 2 秒就能灌进 2500 万行、
	// 占用 1.3GB 堆内存，而命令行的默认超时是 30 分钟——容器会先被 OOM Killer 干掉，
	// 整个面板（含所有定时任务）一起没。
	maxRunLogLines = 200000
	// runLogTrimBatch 是超限时一次性丢弃的行数（约上限的 1/4）。
	// 成块丢而不是逐行淘汰：逐行淘汰意味着每来一行都要重排一次切片、每一轮轮询里
	// 头部锚点都在动，前端只能整份重灌；成块丢让「触顶后的截断」几万行才发生一次。
	// 代价是每次截断会一口气少掉 5 万行历史输出，所以必须把丢弃计数如实告诉前端
	// （见 snapshotWithOffset），否则它按数组长度猜下标就会静默跳过整整一块。
	runLogTrimBatch = maxRunLogLines / 4
	// maxDebugRuns / debugRunRetention 是「调试运行 / 运行代码」记录的上限与过期时长（#159 修复 C）。
	// 原来这张表没有条数上限也不过期，网页和 APP 又从不调 DELETE：实测 50 次调试 RSS 29→216MB，闲置加强制 GC 也不降。
	// 只在写入新记录时修剪（见 pruneDebugRunsLocked），不起后台协程。
	maxDebugRuns      = 20
	debugRunRetention = 30 * time.Minute
)

var scriptInterpreterMap = map[string][]string{
	".py":  {"python", "-u"},
	".js":  {"node"},
	".mjs": {"node"},
	".ts":  {"npx", "ts-node"},
	".sh":  {"bash"},
	".go":  {"go", "run"},
}

var scriptLanguageExtMap = map[string]string{
	"python":     ".py",
	"javascript": ".js",
	"node":       ".mjs",
	"mjs":        ".mjs",
	"typescript": ".ts",
	"shell":      ".sh",
	// bash 是 shell 的别名（#139）：开放 API / MCP 调用方更习惯写 bash，同样落成 .sh 交给 bash 执行。
	"bash": ".sh",
	"go":   ".go",
}

func newDebugRun() *debugRun {
	return &debugRun{
		Logs:   []string{},
		Status: "running",
	}
}

// newDebugRunID 生成调试运行 / 运行代码 / 系统命令行的 run_id：可读前缀 + 16 位十六进制随机串（crypto/rand）。
// 以前直接用毫秒时间戳（console 还带自增序号），run_id 可以按时间猜出来，
// 拿到另一个有权限的令牌就能去读别人正在跑的输出、停掉或清掉它。
func newDebugRunID(prefix string) string {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		// crypto/rand 读失败极少见；退回纳秒时间戳，至少不让这次运行起不来。
		return fmt.Sprintf("%s_%d", prefix, time.Now().UnixNano())
	}
	return prefix + "_" + hex.EncodeToString(buf)
}

func (h *ScriptHandler) storeRun(runID string, run *debugRun) {
	h.mu.Lock()
	// 写入新记录前先修剪（#159 修复 C）：用户调试完离开后，下次再调试时内存就回到基线
	h.pruneDebugRunsLocked()
	if _, exists := h.debugRuns[runID]; !exists {
		// run_id 现在是随机串，正常不会重复；万一撞上沿用原来的覆盖行为，
		// 这时 order 里已经有它，不再追加第二份
		h.order = append(h.order, runID)
	}
	h.debugRuns[runID] = run
	h.mu.Unlock()
}

// pruneDebugRunsLocked 修剪调试运行注册表（#159 修复 C），调用方必须已持有 h.mu。照搬 ConsoleHandler.pruneConsoleRunsLocked，多一条过期规则：
//  1. 先把 order 扫一遍：已经不在 map 里的残留项摘掉；结束超过 debugRunRetention（30 分钟）的，从 map 和 order 里一起删掉；
//  2. 再从最老的一头淘汰已结束的记录，直到总数低于 maxDebugRuns（20 条，给即将写入的新记录留位）。
//
// 只淘汰已结束的：还在跑的记录一旦被删，用户正盯着的输出就断了，进程也会失去停止入口。
// 全都在跑时宁可暂时超过上限，也不动它们、更不死循环。
// 测试里直接构造的 Done=true 记录 finishedAt 是零值：不会按时间过期，只会按条数被淘汰。
func (h *ScriptHandler) pruneDebugRunsLocked() {
	now := time.Now()
	kept := h.order[:0]
	for _, runID := range h.order {
		run, exists := h.debugRuns[runID]
		if !exists {
			// order 里的残留项（记录已被 DELETE 清掉），顺手摘掉
			continue
		}
		if finishedAt := run.finishedAtTime(); !finishedAt.IsZero() && now.Sub(finishedAt) > debugRunRetention {
			delete(h.debugRuns, runID)
			continue
		}
		kept = append(kept, runID)
	}
	h.order = kept

	for len(h.debugRuns) >= maxDebugRuns {
		evicted := false
		for i, runID := range h.order {
			if !h.debugRuns[runID].isDone() {
				continue
			}
			delete(h.debugRuns, runID)
			h.order = append(h.order[:i], h.order[i+1:]...)
			evicted = true
			break
		}
		if !evicted {
			// 一条都淘汰不掉（全在跑），不再死循环
			return
		}
	}
}

func (h *ScriptHandler) loadRun(runID string) (*debugRun, bool) {
	h.mu.Lock()
	run, exists := h.debugRuns[runID]
	h.mu.Unlock()
	return run, exists
}

func (h *ScriptHandler) deleteRun(runID string) (*debugRun, bool) {
	h.mu.Lock()
	run, exists := h.debugRuns[runID]
	if exists {
		delete(h.debugRuns, runID)
		// 同时从插入顺序里摘掉（#159 修复 C，照搬 deleteConsoleRun）
		for i, id := range h.order {
			if id == runID {
				h.order = append(h.order[:i], h.order[i+1:]...)
				break
			}
		}
	}
	h.mu.Unlock()
	return run, exists
}

func (run *debugRun) setProcess(process *os.Process) {
	run.mu.Lock()
	run.Process = process
	run.mu.Unlock()
}

func (run *debugRun) appendLog(line string) {
	run.mu.Lock()
	defer run.mu.Unlock()

	run.Logs = append(run.Logs, line)
	run.trimLogsLocked()
}

// trimLogsLocked 在日志超过上限时，把最前面的一整块丢掉，并在头部补一行省略提示。
// 调用方必须已持有 run.mu。
//
// 不变式：Logs[i] 的全局行号恒等于 run.discardedLogs + i。
// 每次截断丢掉 drop 个槽位、又补回 1 个提示槽位，所以丢弃计数只加 drop-1，
// 这样 logLen() 返回的全局序号在截断前后完全连续。
func (run *debugRun) trimLogsLocked() {
	if len(run.Logs) <= maxRunLogLines {
		return
	}

	drop := runLogTrimBatch
	if drop >= len(run.Logs) {
		// 兜底：上限被调得极小时也不能把新写进来的那行一起丢掉。
		drop = len(run.Logs) - 1
	}
	if drop <= 0 {
		return
	}

	run.discardedLogs += drop - 1
	discarded := run.discardedLogs
	kept := run.Logs[drop:]
	trimmed := make([]string, 0, len(kept)+1)
	// discarded+1 才是「实际被省略的输出行数」：头部那行提示自己也占一个槽位。
	trimmed = append(trimmed, fmt.Sprintf("[前 %d 行输出已省略：单次运行最多保留 %d 行日志]", discarded+1, maxRunLogLines))
	trimmed = append(trimmed, kept...)
	run.Logs = trimmed
}

func (run *debugRun) logOutput() string {
	run.mu.Lock()
	defer run.mu.Unlock()
	return strings.Join(run.Logs, "\n")
}

// logOutputSince 返回全局行号 offset 及其之后的日志。
// offset 是 logLen() 给出的全局序号，不是切片下标——头部被成块丢弃后两者会错开。
func (run *debugRun) logOutputSince(offset int) string {
	run.mu.Lock()
	defer run.mu.Unlock()

	start := offset - run.discardedLogs
	if start < 0 {
		// 要找的位置已经被丢弃了，只能从现存最早一行开始。
		start = 0
	}
	if start >= len(run.Logs) {
		return ""
	}
	return strings.Join(run.Logs[start:], "\n")
}

// logLen 返回只增不减的全局行号水位，供调用方当作 logOutputSince 的锚点。
func (run *debugRun) logLen() int {
	run.mu.Lock()
	defer run.mu.Unlock()
	return run.discardedLogs + len(run.Logs)
}

func (run *debugRun) snapshot() ([]string, bool, *int, string) {
	logs, _, done, exitCode, status := run.snapshotWithOffset()
	return logs, done, exitCode, status
}

// snapshotWithOffset 在 snapshot() 的基础上多返回一个「logs[0] 的全局行号」，
// 也就是已经被成块丢弃的行数（见 trimLogsLocked 的不变式）。
//
// 锚点必须和日志快照在同一把锁里取：分两次调用的话，两次之间恰好发生一次截断，
// 拿到的锚点就和 logs 对不上，按它算出来的下标会指到别的全局行上。
//
// 只有命令行的轮询接口需要这个锚点（前端按全局行号增量追加，光看数组长度会在
// 「本轮既截断、又新增更多行」时静默跳过中间几万行）；脚本调试页走的是全量替换，
// 所以 snapshot() 保持原签名不动，避免动它那几个调用点。
func (run *debugRun) snapshotWithOffset() ([]string, int, bool, *int, string) {
	run.mu.Lock()
	defer run.mu.Unlock()

	logs := make([]string, len(run.Logs))
	copy(logs, run.Logs)

	var exitCode *int
	if run.ExitCode != nil {
		value := *run.ExitCode
		exitCode = &value
	}

	return logs, run.discardedLogs, run.Done, exitCode, run.Status
}

func (run *debugRun) stop() {
	run.mu.Lock()
	defer run.mu.Unlock()

	if run.Process == nil || run.Done {
		return
	}

	// #159 修复 B：先给整组发 SIGTERM，5 秒后组里还有进程才 SIGKILL。
	// TerminateProcessGroup 不阻塞（补刀在单独的协程里），所以在 run.mu 里调用没问题，停止接口照样立即返回。
	service.TerminateProcessGroup(run.Process)
	run.Status = "stopped"
	exitCode := -1
	run.ExitCode = &exitCode
	run.Done = true
	// 与 Done 同一处、同一把锁里写，保持「Done 为真 ⇔ finishedAt 非零」（#159 修复 C 的过期判断靠它）
	run.finishedAt = time.Now()
	run.Logs = append(run.Logs, "[调试运行已停止]")
}

// killIfRunning 杀掉仍在运行的进程组，返回是否真的下过这一刀。
// 返回值是给超时结算用的：只要 kill 发出去了，就不许再拿子进程的退出码把超时标记抹掉。
func (run *debugRun) killIfRunning() bool {
	run.mu.Lock()
	defer run.mu.Unlock()

	if run.Process != nil && !run.Done {
		service.KillProcessGroup(run.Process)
		return true
	}
	return false
}

func (run *debugRun) isStopped() bool {
	run.mu.Lock()
	defer run.mu.Unlock()
	return run.Status == "stopped"
}

// isDone 只读一个布尔。注册表淘汰只关心「跑完没有」，
// 用 snapshot() 会顺手复制整份日志切片，纯属浪费。
func (run *debugRun) isDone() bool {
	run.mu.Lock()
	defer run.mu.Unlock()
	return run.Done
}

// finishedAtTime 在 run.mu 里读出结束时刻，还在跑时是零值。给注册表判断「结束超过 30 分钟」用（#159 修复 C）。
func (run *debugRun) finishedAtTime() time.Time {
	run.mu.Lock()
	defer run.mu.Unlock()
	return run.finishedAt
}

func (run *debugRun) finish(exitCode int, waitErr error, elapsed float64) {
	run.mu.Lock()
	defer run.mu.Unlock()

	if run.Status == "stopped" {
		return
	}

	run.ExitCode = &exitCode
	run.Done = true
	// 与 Done 同一处、同一把锁里写，保持「Done 为真 ⇔ finishedAt 非零」（#159 修复 C 的过期判断靠它）
	run.finishedAt = time.Now()
	if exitCode == 0 {
		run.Status = "success"
		run.Logs = append(run.Logs, fmt.Sprintf("[进程结束, 退出码: %d, 耗时: %.2f秒]", exitCode, elapsed))
		return
	}

	run.Status = "failed"
	errMsg := ""
	if waitErr != nil {
		errMsg = waitErr.Error()
	}
	if errMsg != "" {
		run.Logs = append(run.Logs, fmt.Sprintf("[进程异常退出, 退出码: %d, 错误: %s, 耗时: %.2f秒]", exitCode, errMsg, elapsed))
		return
	}
	run.Logs = append(run.Logs, fmt.Sprintf("[进程异常退出, 退出码: %d, 耗时: %.2f秒]", exitCode, elapsed))
}

func scriptCommandParts(ext, target string) ([]string, error) {
	baseCmd, ok := scriptInterpreterMap[ext]
	if !ok {
		return nil, fmt.Errorf("不支持执行此文件类型")
	}

	if ext == ".sh" {
		if err := service.NormalizeShellScriptFile(target); err != nil {
			return nil, fmt.Errorf("脚本换行规范化失败: %w", err)
		}
	}

	cmdParts := append([]string{}, baseCmd...)
	cmdParts = append(cmdParts, target)
	return cmdParts, nil
}

func scriptRuntimeInterpreter(ext string) (string, error) {
	switch ext {
	case ".py":
		return "python3", nil
	case ".js", ".mjs":
		return "node", nil
	case ".ts":
		return "ts-node", nil
	case ".sh":
		return "bash", nil
	case ".go":
		return "go", nil
	default:
		return "", fmt.Errorf("不支持执行此文件类型")
	}
}

// scriptDebugEnvTTL 是脚本编辑器「调试运行 / 运行代码」注入凭据的兜底有效期。
// 主控手段是运行结束后的吊销（见 DebugRun / RunCode），这里只在面板被 kill -9 时兜底。
const scriptDebugEnvTTL = 2 * time.Hour

// buildScriptExecEnv 返回调试运行的环境，以及注入其中的那枚 operator 凭据。
// 调用方必须在运行结束（含启动失败、被手动停止）后调 service.RevokeScriptToken 吊销它，
// 否则这枚凭据会一直有效到 scriptDebugEnvTTL 到期。
func buildScriptExecEnv(workDir string) (map[string]string, *service.ScriptTokenInfo) {
	// 与原实现一致：构建部分失败时仍返回已拼好的 env（通知 helper 不可用而已，脚本照跑）。
	// 最后的 nil：调试运行与 run-code 不属于任何任务，注入全部已启用变量（#168 的任务级范围不适用）。
	envMap, scriptToken, _ := service.BuildManagedRuntimeEnvMapWithScriptToken(workDir, config.C.Data.ScriptsDir, nil, scriptDebugEnvTTL, "", nil)
	return envMap, scriptToken
}

func newScriptCommand(interpreter string, target string, scriptArgs []string, workDir string, envMap map[string]string) (*exec.Cmd, func(), error) {
	return service.CreateManagedCommand(interpreter, target, scriptArgs, workDir, envMap)
}

func startTrackedCommand(cmd *exec.Cmd, run *debugRun) (*io.PipeWriter, chan struct{}, error) {
	pipeReader, pipeWriter := io.Pipe()
	cmd.Stdout = pipeWriter
	cmd.Stderr = pipeWriter

	if err := cmd.Start(); err != nil {
		pipeWriter.Close()
		return nil, nil, err
	}

	run.setProcess(cmd.Process)
	scanDone := collectRunLogs(pipeReader, run)
	return pipeWriter, scanDone, nil
}

func collectRunLogs(reader io.Reader, run *debugRun) chan struct{} {
	done := make(chan struct{})

	go func() {
		scanner := bufio.NewScanner(reader)
		scanner.Buffer(make([]byte, 64*1024), 1024*1024)
		for scanner.Scan() {
			run.appendLog(scanner.Text())
		}
		// 单行超过 1MB（`base64 -w0 某个db`、`cat 压成一行的 min.js`、`jq -c .` 都很日常）时，
		// Scan 会直接返回 false 退出循环。此时若不把 reader 读空，exec 内部那个往 io.Pipe
		// 写侧灌数据的 io.Copy 会永久阻塞，cmd.Wait() 卡死在 awaitGoroutines ——
		// 阻塞点在父进程里，杀子进程、杀整个进程组都救不回来。
		// 不能改用 CloseWithError：这里拿到的是 io.Reader，没有那个方法；
		// 而且实测它会污染 cmd.Wait() 的返回值，把本来成功的命令记成 failed。
		// 排空不会反过来卡住：drain 一直读，exec 的 copy goroutine 得以结束，
		// Wait 返回后 waitTrackedCommand 才 pipeWriter.Close()，drain 随即拿到 EOF。
		if err := scanner.Err(); err != nil {
			run.appendLog("[输出行过长（超过 1MB），该行及其后续输出已被丢弃]")
			_, _ = io.Copy(io.Discard, reader)
		}
		close(done)
	}()

	return done
}

func waitTrackedCommand(cmd *exec.Cmd, pipeWriter *io.PipeWriter, scanDone chan struct{}) error {
	err := cmd.Wait()
	pipeWriter.Close()
	<-scanDone
	return err
}

func resolveExitCode(err error) int {
	if err == nil {
		return 0
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		return exitErr.ExitCode()
	}
	return 1
}
