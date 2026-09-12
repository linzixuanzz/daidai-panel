package service

import (
	"fmt"
	"log"
	"math/rand"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"daidai-panel/config"
	"daidai-panel/database"
	"daidai-panel/model"

	"gorm.io/gorm"
)

// UntimedTaskScriptTokenTTL 是 task.Timeout == 0（DB 默认值，绝大多数任务）时
// 注入脚本的面板凭据有效期。这不是「预期存活时长」——正常路径下任务一结束就会被吊销——
// 而是吊销来不及执行时的最坏窗口。
//
// 导出是因为 ddp python / ddp shell 也要用同一个窗口：交互式会话同样没有可推导的运行时长，
// 与「未设超时的任务」是同一类场景。一个常量、一套说法，文档才讲得清。
const UntimedTaskScriptTokenTTL = 7 * 24 * time.Hour

// runCommandWithPlanFunc 是子进程执行入口的可替换钩子，仅供测试注入
// （与 runtime_exec.go 里的 warmManagedPythonVenvForVersionFunc 同款做法）。
// 生产路径永远是 RunCommandWithPlan；测试借它构造"执行中 / 执行崩溃"这类
// 无法靠真实子进程稳定复现的场景。
var runCommandWithPlanFunc = RunCommandWithPlan

type TaskExecutor struct {
	scriptsDir       string
	logDir           string
	runningProcesses map[uint]map[int]*os.Process
	processLock      sync.Mutex
	runWG            sync.WaitGroup
}

func NewTaskExecutor() *TaskExecutor {
	return &TaskExecutor{
		scriptsDir:       config.C.Data.ScriptsDir,
		logDir:           config.C.Data.LogDir,
		runningProcesses: make(map[uint]map[int]*os.Process),
	}
}

func (e *TaskExecutor) OnTaskScheduled(req *ExecutionRequest) {
	log.Printf("task %d scheduled: %s", req.TaskID, req.Task.Name)
}

// ResolveExecutionDelay 计算本次执行需要等待的随机延迟。
// 调度器会在占用并发槽位之前完成这段等待，因此这里只负责算时长、不负责 sleep。
func (e *TaskExecutor) ResolveExecutionDelay(req *ExecutionRequest) time.Duration {
	if e == nil || req == nil || req.Task == nil {
		return 0
	}
	// 随机延迟对定时与开机任务生效；手动执行立即运行，避免用户手点后还要等待。
	if !shouldApplyRandomDelayForTrigger(req.TriggerType) {
		return 0
	}

	plan := req.CommandPlan
	if plan == nil {
		parsed, err := ParseCommandExecutionPlan(req.Task.Command, e.scriptsDir)
		if err != nil {
			// 解析失败交给 OnTaskExecuting 统一报错，这里只表示“不需要延迟”。
			return 0
		}
		plan = parsed
	}

	randomDelay := resolveTaskRandomDelaySeconds(req.Task, plan)
	if randomDelay <= 0 {
		return 0
	}
	return time.Duration(rand.Intn(randomDelay)+1) * time.Second
}

// OnTaskExecuting 只做执行前准备：依赖检查、解析命令、建立日志记录。
// 真正的执行在 RunTask 中同步完成，调度器据此实现并发上限。
func (e *TaskExecutor) OnTaskExecuting(req *ExecutionRequest) error {
	task := req.Task

	if task.DependsOn != nil {
		var depTask model.Task
		if err := database.DB.First(&depTask, *task.DependsOn).Error; err == nil {
			if depTask.LastRunStatus == nil || *depTask.LastRunStatus != model.RunSuccess {
				return fmt.Errorf("依赖任务 '%s' 上次执行未成功", depTask.Name)
			}
		}
	}

	plan, err := ParseCommandExecutionPlan(task.Command, e.scriptsDir)
	if err != nil {
		return err
	}
	req.CommandPlan = plan

	// 随机延迟已经在 ResolveExecutionDelay + 调度器重新入队阶段完成，这里不再 sleep，避免双重延迟。

	now := time.Now()
	database.DB.Model(task).Updates(map[string]interface{}{
		"status":      model.TaskStatusRunning,
		"last_run_at": now,
	})

	logID := fmt.Sprintf("%d_%d", task.ID, now.UnixNano())
	var tinyLog *TinyLog
	if !plan.SuppressLiveOutput {
		tinyLog, err = GetTinyLogManager().Create(logID)
		if err != nil {
			return fmt.Errorf("failed to create log: %w", err)
		}
		req.LogID = logID
	}

	relLogPath := GetRelativeLogPathForTask(task)
	runningStatus := model.LogStatusRunning
	taskLog := &model.TaskLog{
		TaskID:    task.ID,
		Status:    &runningStatus,
		StartedAt: now,
		LogPath:   &relLogPath,
	}
	database.DB.Create(taskLog)

	req.TaskLogID = taskLog.ID
	req.taskLog = taskLog
	req.tinyLog = tinyLog

	return nil
}

func (e *TaskExecutor) OnTaskStarted(req *ExecutionRequest) {
	log.Printf("task %d started: %s", req.TaskID, req.Task.Name)
}

// RunTask 同步执行任务直到结束（含全部重试与重试等待），调用方（worker）在此期间一直占用并发槽位。
func (e *TaskExecutor) RunTask(req *ExecutionRequest) {
	if e == nil || req == nil {
		return
	}

	taskLog := req.taskLog
	tinyLog := req.tinyLog
	// 用完即清，避免同一请求对象被重新入队时复用上一次的日志记录。
	req.taskLog = nil
	req.tinyLog = nil

	if taskLog == nil {
		// 正常链路里 OnTaskExecuting 成功后一定有 taskLog，这里只是兜底。
		// 兜底也必须把已经建好的实时日志收口，否则 TinyLog 会永远留在管理器里泄漏。
		if tinyLog != nil {
			tinyLog.Close()
			GetTinyLogManager().Remove(tinyLog.LogID)
		}
		log.Printf("task %d: missing prepared task log, skip execution", req.TaskID)
		return
	}

	e.runWG.Add(1)
	defer e.runWG.Done()

	e.runTask(req, taskLog, tinyLog)
}

func (e *TaskExecutor) OnTaskCompleted(req *ExecutionRequest, result *ExecutionResult) {
	log.Printf("task %d completed: success=%v, duration=%.2fs",
		req.TaskID, result.Success, result.Duration)
}

func (e *TaskExecutor) OnTaskFailed(req *ExecutionRequest, err error) {
	log.Printf("task %d failed: %v", req.TaskID, err)

	task := req.Task
	if task == nil {
		return
	}

	now := time.Now()
	if req.TaskLogID == 0 {
		status := model.LogStatusFailed
		duration := 0.0
		content := fmt.Sprintf("=== 执行失败 [%s] ===\n%s\n", now.Format("2006-01-02 15:04:05"), err.Error())
		taskLog := &model.TaskLog{
			TaskID:    task.ID,
			Content:   content,
			Status:    &status,
			Duration:  &duration,
			StartedAt: now,
			EndedAt:   &now,
		}
		database.DB.Create(taskLog)
		req.TaskLogID = taskLog.ID
	}

	runStatus := model.RunFailed
	database.DB.Model(task).Updates(map[string]interface{}{
		"status":            ResolveTaskInactiveStatus(task),
		"last_run_at":       now,
		"last_run_status":   runStatus,
		"last_running_time": 0.0,
		"pid":               gorm.Expr("NULL"),
	})
}

func KillProcessGroup(p *os.Process) {
	if p == nil {
		return
	}
	killGroup(p)
	p.Kill()
}

func KillProcessByPid(pid int) {
	killGroupByPid(pid)
	p, err := os.FindProcess(pid)
	if err != nil {
		return
	}
	p.Kill()
}

func (e *TaskExecutor) StopTask(taskID uint) bool {
	e.processLock.Lock()
	defer e.processLock.Unlock()

	if processes, ok := e.runningProcesses[taskID]; ok {
		// 先打"手动停止"标记再 kill，保证完成块结算时标记已可见。
		markManualStop(taskID)
		for _, process := range processes {
			KillProcessGroup(process)
		}
		delete(e.runningProcesses, taskID)
		return true
	}
	return false
}

// HasRunningProcess 判断执行器进程表里是否还登记着这个任务的进程。
// 给「删除任务时一并删除脚本」兜底：库里 status 不一定是运行中（例如禁用了一个正在跑的任务，
// status 已经改写，但进程还在），这时删掉脚本会让它这次的重试找不到文件。
// 只读不改，接收者为 nil（测试或启动早期执行器还没建好）时返回 false。
func (e *TaskExecutor) HasRunningProcess(taskID uint) bool {
	if e == nil {
		return false
	}
	e.processLock.Lock()
	defer e.processLock.Unlock()
	return len(e.runningProcesses[taskID]) > 0
}

func (e *TaskExecutor) StopAllRunningTasks() int {
	if e == nil {
		return 0
	}

	e.processLock.Lock()
	processesByTask := e.runningProcesses
	e.runningProcesses = make(map[uint]map[int]*os.Process)
	e.processLock.Unlock()

	count := 0
	for _, processes := range processesByTask {
		for _, process := range processes {
			KillProcessGroup(process)
			count++
		}
	}
	return count
}

func (e *TaskExecutor) Wait(timeout time.Duration) bool {
	if e == nil {
		return true
	}

	done := make(chan struct{})
	go func() {
		e.runWG.Wait()
		close(done)
	}()

	if timeout <= 0 {
		<-done
		return true
	}

	select {
	case <-done:
		return true
	case <-time.After(timeout):
		return false
	}
}

func (e *TaskExecutor) runTask(req *ExecutionRequest, taskLog *model.TaskLog, tinyLog *TinyLog) {
	task := req.Task
	plan := req.CommandPlan
	if plan == nil {
		parsedPlan, err := ParseCommandExecutionPlan(task.Command, e.scriptsDir)
		if err != nil {
			panic(err)
		}
		plan = parsedPlan
		req.CommandPlan = parsedPlan
	}
	startTime := time.Now()
	exitCode := 0
	success := false
	lastFailureOutput := ""
	lastSuccessOutput := ""
	taskWorkDir := e.scriptsDir
	if plan != nil && strings.TrimSpace(plan.FullPath) != "" {
		taskWorkDir = filepath.Dir(plan.FullPath)
	}

	maxLogSize := model.GetRegisteredConfigInt("max_log_content_size")

	timeout := task.Timeout
	if timeout < 0 {
		timeout = 0
	}
	envTTL := time.Duration(timeout)*time.Second + time.Hour
	if timeout == 0 {
		// 未设超时的任务没有可推导的运行时长，取一个固定兜底窗口。
		// 主控手段是任务结束时的吊销（见下方 defer），TTL 只在「面板被 kill -9 / 宿主断电，
		// 吊销压根没机会执行」时兜底。取值不能太短，否则长任务会跑到一半丢掉 API 权限。
		envTTL = UntimedTaskScriptTokenTTL
	}
	envVars, scriptToken, envErr := BuildManagedRuntimeEnvMapWithScriptToken(taskWorkDir, e.scriptsDir, task.NotificationChannelID, envTTL, task.PythonVersion)
	if envErr != nil {
		log.Printf("prepare task runtime env failed: %v", envErr)
	}

	defer func() {
		if r := recover(); r != nil {
			log.Printf("task %d panicked: %v", req.TaskID, r)
			if tinyLog != nil {
				fmt.Fprintf(tinyLog, "\n[任务异常崩溃: %v]\n", r)
			}
			exitCode = 1
			success = false
		}

		// 任务已经跑完（正常结束、失败、超时被杀、panic 都汇聚到这里），
		// 注入脚本的那枚 operator 凭据立刻作废，不让它在任务之外继续游荡。
		// 放在 recover 之后：先保证 panic 被接住，再做吊销。
		RevokeScriptToken(scriptToken)

		duration := time.Since(startTime).Seconds()

		compressed := ""
		if tinyLog != nil {
			compressed, _ = tinyLog.Close()
			GetTinyLogManager().Remove(tinyLog.LogID)
		}

		logStatus := model.LogStatusSuccess
		if !success {
			logStatus = model.LogStatusFailed
		}

		runStatus := model.RunSuccess
		if !success {
			runStatus = model.RunFailed
		}

		// 主动停止：统一结算为 Aborted，跳过成功/失败通知，必要时单独发送终止通知。
		// applyManualStopOverride 读即清标记，自然完成时返回原状态、manualAborted=false。
		runStatus, logStatus, manualAborted := applyManualStopOverride(req.TaskID, runStatus, logStatus)
		finalSuccess := runStatus == model.RunSuccess
		finalAborted := runStatus == model.RunAborted

		endedAt := time.Now()
		database.DB.Model(taskLog).Updates(map[string]interface{}{
			"status":   logStatus,
			"content":  compressed,
			"ended_at": endedAt,
			"duration": duration,
		})

		inactiveStatus := ResolveTaskInactiveStatus(task)
		if inactiveStatus == model.TaskStatusDisabled {
			// 「运行中被禁用、等这次跑完再生效」的意图到这里就落地了（status 已经写成禁用），
			// 标记用完即清：它只活在内存里，留着的话万一这个任务 id 被删除后复用，
			// 新任务会平白继承一个禁用意图。
			ClearPendingDisable(task.ID)
		}
		database.DB.Model(task).Updates(map[string]interface{}{
			"status":            inactiveStatus,
			"last_run_status":   runStatus,
			"last_running_time": duration,
			"pid":               gorm.Expr("NULL"),
		})

		e.processLock.Lock()
		delete(e.runningProcesses, req.TaskID)
		e.processLock.Unlock()

		result := &ExecutionResult{
			Success:  finalSuccess,
			ExitCode: exitCode,
			Duration: duration,
		}
		e.OnTaskCompleted(req, result)

		if finalAborted && manualAborted && task.NotifyOnAbort {
			title, content, context := buildTaskExecutionNotification(task, req.TaskLogID, model.RunAborted, exitCode, duration, endedAt, "")
			SendNotificationWithOptions(title, content, NotificationDispatchOptions{
				ChannelIDs: buildTaskNotificationChannelIDs(task.NotificationChannelID),
				Context:    context,
			})
		}
		if !finalAborted && finalSuccess && task.NotifyOnSuccess {
			title, content, context := buildTaskExecutionNotification(task, req.TaskLogID, model.RunSuccess, exitCode, duration, endedAt, lastSuccessOutput)
			SendNotificationWithOptions(title, content, NotificationDispatchOptions{
				ChannelIDs: buildTaskNotificationChannelIDs(task.NotificationChannelID),
				Context:    context,
			})
		}
		if !finalAborted && !finalSuccess && task.NotifyOnFailure {
			title, content, context := buildTaskExecutionNotification(task, req.TaskLogID, model.RunFailed, exitCode, duration, endedAt, lastFailureOutput)
			SendNotificationWithOptions(title, content, NotificationDispatchOptions{
				ChannelIDs: buildTaskNotificationChannelIDs(task.NotificationChannelID),
				Context:    context,
			})
		}
	}()

	logMgr := GetLogStreamManager()
	var fullLogPath string
	if taskLog.LogPath != nil {
		fullLogPath = filepath.Join(e.logDir, *taskLog.LogPath)
	}
	defer func() {
		if fullLogPath != "" {
			logMgr.CloseStream(fullLogPath)
		}
	}()

	var outputCollectorMu sync.Mutex
	onOutput := func(chunk string) {
		if tinyLog != nil {
			fmt.Fprint(tinyLog, chunk)
		}
		if fullLogPath != "" {
			logMgr.Write(fullLogPath, chunk)
		}
	}

	var outputCollector strings.Builder

	onOutputWithCollect := func(chunk string) {
		onOutput(chunk)
		outputCollectorMu.Lock()
		outputCollector.WriteString(chunk)
		outputCollectorMu.Unlock()
	}

	onOutput(fmt.Sprintf("=== 开始执行 [%s] ===\n", startTime.Format("2006-01-02 15:04:05")))

	// 前置脚本（任务专属 + 全局 task_before.sh）里 export 的环境变量会按执行顺序
	// 增量合并回 envVars，供后面的目标脚本、task_after.sh、extra.sh 和后置脚本共用。
	// 这是青龙 task_before 的语义；细节与保护名单见 task_hook_env.go。
	if task.TaskBefore != nil && *task.TaskBefore != "" {
		onOutput("[执行前置脚本]\n")
		captureHookEnvExports(envVars, onOutput, func(hookEnv map[string]string) {
			// 前置脚本的错误过去被直接丢弃，bash 找不到、临时文件写不进去、超时，
			// 用户在任务日志里只能看到「[执行前置脚本]」一行。这里把它说出来，
			// 但仍然保持「前置脚本失败不中断任务」的既有行为。
			if err := RunInlineScript(*task.TaskBefore, e.scriptsDir, hookEnv, 60, onOutput, plan.ScriptArgs...); err != nil {
				onOutput(fmt.Sprintf("[前置脚本执行失败: %s]\n", err.Error()))
			}
		})
	}

	captureHookEnvExports(envVars, onOutput, func(hookEnv map[string]string) {
		RunHookScript("task_before.sh", e.scriptsDir, hookEnv, onOutput, plan.ScriptArgs...)
	})

	retries := 0
	var lastExitCode int
	depInstallCount := 0
	maxDepInstalls := 5
	installedDeps := make(map[string]bool)

	for retries <= task.MaxRetries {
		if retries > 0 {
			onOutput(fmt.Sprintf("[第 %d 次重试，等待 %d 秒]\n", retries, task.RetryInterval))
			time.Sleep(time.Duration(task.RetryInterval) * time.Second)
		}

		outputCollector.Reset()
		onStart := func(process *os.Process) {
			e.registerRunningProcess(req.TaskID, process)
			pid := process.Pid
			database.DB.Model(task).Update("pid", pid)
		}
		effectiveTimeout := timeout
		if plan.TimeoutOverride != nil && *plan.TimeoutOverride > 0 {
			effectiveTimeout = *plan.TimeoutOverride
		}
		result, _, err := runCommandWithPlanFunc(plan, effectiveTimeout, envVars, maxLogSize, onOutputWithCollect, onStart)
		if err != nil {
			onOutput(fmt.Sprintf("[执行错误: %s]\n", err.Error()))
			if strings.Contains(err.Error(), "illegal instruction") || strings.Contains(err.Error(), "core dumped") {
				onOutput("[提示] 该错误通常是因为当前 CPU 不支持程序所需的指令集（如 AVX/SSE），常见于部分 VPS 或 ARM 设备。建议更换支持相关指令集的服务器。\n")
			}
			retries++
			lastExitCode = 1
			outputCollectorMu.Lock()
			lastFailureOutput = buildTaskFailureOutput(outputCollector.String(), err.Error())
			outputCollectorMu.Unlock()
			continue
		}

		lastExitCode = result.ReturnCode
		if task.IsSuccessExitCode(result.ReturnCode) {
			success = true
			lastFailureOutput = ""
			outputCollectorMu.Lock()
			lastSuccessOutput = outputCollector.String()
			outputCollectorMu.Unlock()
			break
		}
		outputCollectorMu.Lock()
		lastFailureOutput = outputCollector.String()
		outputCollectorMu.Unlock()

		if depInstallCount < maxDepInstalls && model.GetRegisteredConfigBool("auto_install_deps") {
			outputCollectorMu.Lock()
			collected := outputCollector.String()
			outputCollectorMu.Unlock()
			if e.detectAndInstallDeps(plan, collected, envVars, installedDeps, onOutput) {
				depInstallCount++
				onOutput(fmt.Sprintf("[依赖已安装 (%d/%d)，自动重试执行]\n", depInstallCount, maxDepInstalls))
				continue
			}
		}

		if hint := BuildModuleCompatibilityHint(lastFailureOutput); hint != "" {
			onOutput(hint)
			outputCollectorMu.Lock()
			lastFailureOutput = strings.TrimSpace(outputCollector.String())
			outputCollectorMu.Unlock()
		}

		retries++
	}

	exitCode = lastExitCode

	// 后置脚本不参与环境变量回传：它跑完任务就结束了，回写没有消费方。
	// 但同样要把执行错误说出来，理由与前置脚本一致。
	if task.TaskAfter != nil && *task.TaskAfter != "" {
		onOutput("[执行后置脚本]\n")
		if err := RunInlineScript(*task.TaskAfter, e.scriptsDir, envVars, 60, onOutput, plan.ScriptArgs...); err != nil {
			onOutput(fmt.Sprintf("[后置脚本执行失败: %s]\n", err.Error()))
		}
	}

	RunHookScript("task_after.sh", e.scriptsDir, envVars, onOutput, plan.ScriptArgs...)
	RunHookScript("extra.sh", e.scriptsDir, envVars, onOutput, plan.ScriptArgs...)

	endTime := time.Now()
	duration := endTime.Sub(startTime).Seconds()

	completionNote := ""
	if success && lastExitCode != 0 {
		completionNote = "（已按任务配置判定成功）"
	}
	onOutput(fmt.Sprintf("=== 执行结束 [%s] 耗时 %.2f 秒 退出码 %d%s ===\n",
		endTime.Format("2006-01-02 15:04:05"), duration, lastExitCode, completionNote))
}

func (e *TaskExecutor) registerRunningProcess(taskID uint, process *os.Process) {
	if process == nil {
		return
	}
	e.processLock.Lock()
	defer e.processLock.Unlock()

	if e.runningProcesses[taskID] == nil {
		e.runningProcesses[taskID] = make(map[int]*os.Process)
	}
	e.runningProcesses[taskID][process.Pid] = process
}

func buildTaskNotificationChannelIDs(channelID *uint) []uint {
	if channelID == nil || *channelID == 0 {
		return nil
	}
	return []uint{*channelID}
}

func buildTaskFailureOutput(output, errMessage string) string {
	output = strings.TrimSpace(output)
	errMessage = strings.TrimSpace(errMessage)
	if output == "" {
		return errMessage
	}
	if errMessage == "" {
		return output
	}
	return output + "\n[执行错误] " + errMessage
}

func buildTaskExecutionNotification(task *model.Task, taskLogID uint, runStatus int, exitCode int, duration float64, endedAt time.Time, logOutput string) (string, string, map[string]string) {
	endedAtText := endedAt.Format("2006-01-02 15:04:05.000")
	durationText := fmt.Sprintf("%.1f", duration)

	var failureExcerpt, successExcerpt string
	if runStatus == model.RunSuccess {
		successExcerpt = summarizeTaskSuccessOutput(logOutput)
	} else if runStatus == model.RunFailed {
		failureExcerpt = summarizeTaskFailureOutput(logOutput)
	}

	statusText := "失败"
	statusValue := "failure"
	title := "任务执行失败"
	summaryLine := fmt.Sprintf("定时任务「%s」执行失败", task.Name)
	metaLines := []string{
		"完成时间: " + endedAtText,
		"日志ID: " + strconv.FormatUint(uint64(taskLogID), 10),
		"退出码: " + strconv.Itoa(exitCode),
		"耗时: " + durationText + " 秒",
	}

	if runStatus == model.RunSuccess {
		statusText = "成功"
		statusValue = "success"
		title = "任务执行成功"
		summaryLine = fmt.Sprintf("定时任务「%s」执行成功", task.Name)
		metaLines = []string{
			"完成时间: " + endedAtText,
			"日志ID: " + strconv.FormatUint(uint64(taskLogID), 10),
			"耗时: " + durationText + " 秒",
		}
	}
	if runStatus == model.RunAborted {
		statusText = "已终止"
		statusValue = "aborted"
		title = "任务已终止"
		summaryLine = fmt.Sprintf("定时任务「%s」已被主动终止", task.Name)
		metaLines = []string{
			"终止时间: " + endedAtText,
			"日志ID: " + strconv.FormatUint(uint64(taskLogID), 10),
			"耗时: " + durationText + " 秒",
		}
	}

	content := summaryLine + "\n" + strings.Join(metaLines, "\n")
	if runStatus == model.RunSuccess && successExcerpt != "" {
		content += "\n\n执行日志:\n" + successExcerpt
	}
	if runStatus == model.RunFailed && failureExcerpt != "" {
		content += "\n\n失败原因:\n" + failureExcerpt
	}

	context := map[string]string{
		"task_name":      task.Name,
		"task_id":        strconv.FormatUint(uint64(task.ID), 10),
		"task_log_id":    strconv.FormatUint(uint64(taskLogID), 10),
		"exit_code":      strconv.Itoa(exitCode),
		"duration":       durationText,
		"ended_at":       endedAtText,
		"completed_at":   endedAtText,
		"status":         statusValue,
		"status_text":    statusText,
		"result_summary": summaryLine,
		"error_log":      failureExcerpt,
		"failure_log":    failureExcerpt,
		"reason":         failureExcerpt,
		"failure_reason": failureExcerpt,
		"log_excerpt":    successExcerpt,
		"success_log":    successExcerpt,
	}
	return title, content, context
}

// panelMetaLinePrefixes 登记所有「面板自己打进任务日志」的元信息行前缀。
//
// isPanelMetaLine 靠它把这些行从成功通知的日志摘录里滤掉。摘录只有 30 行 / 1500 字符，
// 漏登记一条，它就会顶掉用户真正想看的脚本输出 —— 所以新增任何面板输出行时，
// 必须同步登记到这里（契约见 quality-guidelines.md）。
var panelMetaLinePrefixes = []string{
	"[执行前置脚本]",
	"[执行后置脚本]",
	"[前置脚本执行失败:",
	"[后置脚本执行失败:",
	// 前置钩子的环境变量回传日志：已生效 / 已忽略受保护变量 / 未采集到回传数据 /
	// 采集准备失败 / 运行时关键变量被整体覆盖的「注意：」提示，全部共用这个前缀。
	"[前置脚本环境变量]",
	"[执行错误:",
	"[提示]",
	"[第 ",
	"[检测到缺失依赖:",
	"[安装成功:",
	"[安装失败:",
	"[依赖已安装 ",
	"[重试启动失败:",
	"[任务异常崩溃:",
	// 子进程被信号杀掉时的可诊断提示（#113 排查里补的）。被信号杀必然结算为失败、
	// 走的是不过滤的 failureExcerpt，所以登记它今天不改变任何行为；
	// 登记只是守住「面板输出行必须在册」这条契约，免得以后有人把它挪进成功摘录。
	"[脚本进程被信号终止：",
}

func isPanelMetaLine(line string) bool {
	for _, prefix := range panelMetaLinePrefixes {
		if strings.HasPrefix(line, prefix) {
			return true
		}
	}
	return false
}

func summarizeTaskSuccessOutput(output string) string {
	output = strings.TrimSpace(output)
	if output == "" {
		return ""
	}

	rawLines := normalizeTaskFailureLines(output)
	lines := make([]string, 0, len(rawLines))
	for _, line := range rawLines {
		if isPanelMetaLine(line) {
			continue
		}
		lines = append(lines, line)
	}
	if len(lines) == 0 {
		return ""
	}

	const (
		maxSuccessLines = 30
		maxSuccessRunes = 1500
	)
	tail := tailTaskFailureLines(lines, maxSuccessLines)
	return truncateTaskFailureSummary(strings.Join(tail, "\n"), maxSuccessRunes)
}

func summarizeTaskFailureOutput(output string) string {
	output = strings.TrimSpace(output)
	if output == "" {
		return ""
	}

	if hint := BuildModuleCompatibilityHint(output); hint != "" {
		return truncateTaskFailureSummary(hint, 320)
	}

	lines := normalizeTaskFailureLines(output)
	if len(lines) == 0 {
		return ""
	}

	if summary := summarizePythonFailureOutput(lines); summary != "" {
		return summary
	}

	if summary := summarizeNodeFailureOutput(lines); summary != "" {
		return summary
	}

	if summary := summarizeGenericFailureOutput(lines); summary != "" {
		return summary
	}

	return truncateTaskFailureSummary(strings.Join(tailTaskFailureLines(lines, 4), "\n"), 420)
}

func BuildModuleCompatibilityHint(output string) string {
	lower := strings.ToLower(strings.TrimSpace(output))
	if lower == "" {
		return ""
	}

	if strings.Contains(lower, "err_require_esm") &&
		strings.Contains(lower, "require() of es module") {
		packageName := extractRequireESMPackageName(output)
		if packageName == "" {
			return "[提示] 当前依赖是 ESM 模块，但脚本使用了 CommonJS require() 方式加载。请改用 import() / ESM 写法，或安装兼容 require() 的旧版本依赖。"
		}

		installSpec := ResolveNodeInstallPackageSpec(packageName)
		if installSpec != packageName {
			return fmt.Sprintf("[提示] 当前依赖 %s 是 ESM 模块，但脚本使用了 CommonJS require() 方式加载。建议在依赖页重装兼容版本：%s；或改用 import() / ESM 写法。", packageName, installSpec)
		}
		return fmt.Sprintf("[提示] 当前依赖 %s 是 ESM 模块，但脚本使用了 CommonJS require() 方式加载。该包未在兼容映射中，请手动指定兼容 require() 的旧版本，或改用 import() / ESM 写法。", packageName)
	}

	return ""
}

var (
	nodeRequireCallModuleRe  = regexp.MustCompile(`require\(['"]([^'"]+)['"]\)`)
	nodeModulesPathPackageRe = regexp.MustCompile(`node_modules[/\\]((?:@[^/\\\s:]+[/\\][^/\\\s:]+)|[^/\\\s:]+)`)
	pythonTraceFrameRe       = regexp.MustCompile(`^File "([^"]+)", line (\d+), in (.+)$`)
	pythonExceptionLineRe    = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.]*(?:Error|Exception|Warning|Exit|Interrupt|Failure)(?::.*)?$`)
	nodeStackFrameRe         = regexp.MustCompile(`^(?:at\s+.+?\s+\()?(.+?):(\d+)(?::(\d+))?\)?$`)
	caretIndicatorLineRe     = regexp.MustCompile(`^[\^~\s]+$`)
	genericErrorLineRe       = regexp.MustCompile(`(?i)(error|exception|panic|failed|failure|timeout|denied|refused|invalid|fatal|失败|错误|异常|超时|拒绝)`)
)

func extractRequireESMPackageName(output string) string {
	for _, matches := range nodeModulesPathPackageRe.FindAllStringSubmatch(output, -1) {
		if len(matches) < 2 {
			continue
		}
		// 这里处理的是从 Node 报错文本里捕获出来的路径，不是宿主机文件系统路径，
		// 因此不能用 filepath.ToSlash（它按宿主机分隔符工作，在 Linux 上是空操作）。
		packageName := normalizeNodeRequireSpecifier(strings.ReplaceAll(matches[1], "\\", "/"))
		if packageName != "" {
			return packageName
		}
	}

	for _, matches := range nodeRequireCallModuleRe.FindAllStringSubmatch(output, -1) {
		if len(matches) < 2 {
			continue
		}
		packageName := normalizeNodeRequireSpecifier(matches[1])
		if packageName != "" {
			return packageName
		}
	}
	return ""
}

func normalizeNodeRequireSpecifier(spec string) string {
	// 入参可能来自 Node 报错文本中的 Windows 风格路径，规范化必须与宿主机平台无关。
	spec = strings.TrimSpace(strings.ReplaceAll(spec, "\\", "/"))
	if spec == "" ||
		strings.HasPrefix(spec, ".") ||
		strings.HasPrefix(spec, "/") ||
		strings.HasPrefix(spec, "node:") ||
		strings.HasPrefix(spec, "data:") ||
		strings.HasPrefix(spec, "file:") ||
		strings.Contains(spec, ":/") {
		return ""
	}

	parts := strings.Split(spec, "/")
	if len(parts) == 0 || strings.TrimSpace(parts[0]) == "" || strings.HasPrefix(parts[0], ".") {
		return ""
	}
	if strings.HasPrefix(parts[0], "@") {
		if len(parts) < 2 || strings.TrimSpace(parts[1]) == "" {
			return ""
		}
		return parts[0] + "/" + parts[1]
	}
	return NormalizeNodeDependencyPackageName(parts[0])
}

type taskFailureFrame struct {
	Path     string
	Line     string
	Function string
}

func normalizeTaskFailureLines(output string) []string {
	rawLines := strings.Split(output, "\n")
	lines := make([]string, 0, len(rawLines))
	for _, line := range rawLines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "=== 开始执行") || strings.HasPrefix(line, "=== 执行结束") {
			continue
		}
		if line == "Traceback (most recent call last):" {
			continue
		}
		if caretIndicatorLineRe.MatchString(line) {
			continue
		}
		lines = append(lines, line)
	}
	return lines
}

func summarizePythonFailureOutput(lines []string) string {
	if len(lines) == 0 {
		return ""
	}

	exceptionLine := strings.TrimSpace(lines[len(lines)-1])
	if !pythonExceptionLineRe.MatchString(exceptionLine) {
		return ""
	}

	frames := extractPythonFailureFrames(lines)
	location := formatTaskFailureLocation(selectPreferredFailureFrame(frames))
	if location == "" {
		return truncateTaskFailureSummary(exceptionLine, 240)
	}

	return truncateTaskFailureSummary(exceptionLine+"\n定位: "+location, 320)
}

func extractPythonFailureFrames(lines []string) []taskFailureFrame {
	frames := make([]taskFailureFrame, 0, len(lines))
	for _, line := range lines {
		matches := pythonTraceFrameRe.FindStringSubmatch(line)
		if len(matches) != 4 {
			continue
		}
		frames = append(frames, taskFailureFrame{
			Path:     matches[1],
			Line:     matches[2],
			Function: strings.TrimSpace(matches[3]),
		})
	}
	return frames
}

func summarizeNodeFailureOutput(lines []string) string {
	if len(lines) == 0 {
		return ""
	}

	errorLine := findLastTaskFailureErrorLine(lines)
	if errorLine == "" {
		return ""
	}

	var frames []taskFailureFrame
	for _, line := range lines {
		matches := nodeStackFrameRe.FindStringSubmatch(line)
		if len(matches) < 3 {
			continue
		}
		frames = append(frames, taskFailureFrame{
			Path: matches[1],
			Line: matches[2],
		})
	}
	if len(frames) == 0 {
		return ""
	}

	location := formatTaskFailureLocation(selectPreferredFailureFrame(frames))
	if location == "" {
		return truncateTaskFailureSummary(errorLine, 240)
	}

	return truncateTaskFailureSummary(errorLine+"\n定位: "+location, 320)
}

func summarizeGenericFailureOutput(lines []string) string {
	if len(lines) == 0 {
		return ""
	}

	errorLine := findLastTaskFailureErrorLine(lines)
	if errorLine == "" {
		return ""
	}

	contextLine := findLastTaskFailureContextLine(lines, errorLine)
	if contextLine == "" {
		return truncateTaskFailureSummary(errorLine, 260)
	}

	return truncateTaskFailureSummary(errorLine+"\n上下文: "+contextLine, 360)
}

func findLastTaskFailureErrorLine(lines []string) string {
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		if genericErrorLineRe.MatchString(line) {
			return line
		}
	}
	return ""
}

func findLastTaskFailureContextLine(lines []string, errorLine string) string {
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" || line == errorLine {
			continue
		}
		if pythonTraceFrameRe.MatchString(line) || nodeStackFrameRe.MatchString(line) {
			frame := taskFailureFrameFromLine(line)
			location := formatTaskFailureLocation(&frame)
			if location != "" {
				return location
			}
		}
		if genericErrorLineRe.MatchString(line) {
			continue
		}
		return line
	}
	return ""
}

func taskFailureFrameFromLine(line string) taskFailureFrame {
	if matches := pythonTraceFrameRe.FindStringSubmatch(line); len(matches) == 4 {
		return taskFailureFrame{
			Path:     matches[1],
			Line:     matches[2],
			Function: strings.TrimSpace(matches[3]),
		}
	}
	if matches := nodeStackFrameRe.FindStringSubmatch(line); len(matches) >= 3 {
		return taskFailureFrame{
			Path: matches[1],
			Line: matches[2],
		}
	}
	return taskFailureFrame{}
}

func selectPreferredFailureFrame(frames []taskFailureFrame) *taskFailureFrame {
	if len(frames) == 0 {
		return nil
	}

	for i := len(frames) - 1; i >= 0; i-- {
		frame := frames[i]
		if !isRuntimeFailureFrame(frame.Path) {
			return &frames[i]
		}
	}
	return &frames[len(frames)-1]
}

func isRuntimeFailureFrame(path string) bool {
	lower := strings.ToLower(strings.ReplaceAll(path, "\\", "/"))
	runtimeMarkers := []string{
		"/python",
		"/asyncio/",
		"site-packages/",
		"/node_modules/",
		"node:internal",
		"<anonymous>",
	}
	for _, marker := range runtimeMarkers {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func formatTaskFailureLocation(frame *taskFailureFrame) string {
	if frame == nil || strings.TrimSpace(frame.Path) == "" {
		return ""
	}

	location := shortenTaskFailurePath(frame.Path)
	if strings.TrimSpace(frame.Line) != "" {
		location += ":" + strings.TrimSpace(frame.Line)
	}

	functionName := strings.TrimSpace(frame.Function)
	if functionName != "" && functionName != "<module>" {
		location += " (" + functionName + ")"
	}
	return location
}

func shortenTaskFailurePath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}

	normalized := strings.ReplaceAll(path, "\\", "/")
	parts := strings.FieldsFunc(normalized, func(r rune) bool {
		return r == '/'
	})
	if len(parts) == 0 {
		return normalized
	}
	if len(parts) >= 2 {
		return strings.Join(parts[len(parts)-2:], "/")
	}
	return parts[0]
}

func tailTaskFailureLines(lines []string, count int) []string {
	if len(lines) <= count {
		return append([]string{}, lines...)
	}
	return append([]string{}, lines[len(lines)-count:]...)
}

func truncateTaskFailureSummary(summary string, maxRunes int) string {
	summary = strings.TrimSpace(summary)
	if summary == "" {
		return ""
	}

	runes := []rune(summary)
	if len(runes) <= maxRunes {
		return summary
	}
	if maxRunes <= 1 {
		return string(runes[:maxRunes])
	}
	return string(runes[:maxRunes-1]) + "…"
}

func (e *TaskExecutor) detectAndInstallDeps(plan *CommandExecutionPlan, output string, envVars map[string]string, installedDeps map[string]bool, onOutput OnOutputFunc) bool {
	if plan == nil || plan.FullPath == "" {
		return false
	}

	ext := strings.ToLower(filepath.Ext(plan.FullPath))
	workDir := filepath.Dir(plan.FullPath)
	candidate := DetectAutoInstallCandidate(ext, output, workDir)
	if candidate == nil {
		return false
	}

	if installedDeps != nil && installedDeps[candidate.PackageName] {
		onOutput(fmt.Sprintf("[%s 已安装但仍然报错，可能是模块版本不兼容或内部依赖异常，请尝试手动安装指定版本]", candidate.DisplayName))
		return false
	}

	onOutput(fmt.Sprintf("[检测到缺失依赖: %s，正在自动安装...]", candidate.DisplayName))
	if candidate.Manager == "nodejs" {
		if notice := NodeInstallCompatibilityNotice(candidate.PackageName); notice != "" {
			onOutput(notice)
		}
	}
	result := InstallAutoDependency(candidate, envVars)
	if installedDeps != nil {
		installedDeps[candidate.PackageName] = true
	}
	if !result.Success {
		failureReason := strings.TrimSpace(result.Error)
		if failureReason == "" {
			failureReason = "未知错误"
		}
		onOutput(fmt.Sprintf("[安装失败: %s]", failureReason))
		return false
	}

	onOutput(fmt.Sprintf("[安装成功: %s]", candidate.DisplayName))
	return true
}

func RecordAutoInstalledDep(depType, name, installLog string) {
	RecordAutoInstalledDepForPythonVersion(depType, name, installLog, "")
}

func RecordAutoInstalledDepForPythonVersion(depType, name, installLog, pythonVersion string) {
	if depType == model.DepTypePython {
		pythonVersion = NormalizeDependencyPythonVersion(pythonVersion)
	} else {
		pythonVersion = ""
	}
	var existing model.Dependency
	found := false
	if depType == model.DepTypePython {
		// 按 PEP 503 归一化键查重，避免 requests / Requests 落成两条。
		existing, found = FindExistingPythonDependency(name, pythonVersion)
	} else {
		found = database.DB.Where("type = ? AND name = ?", depType, name).First(&existing).Error == nil
	}
	if found {
		database.DB.Model(&existing).Updates(map[string]interface{}{
			"status":         model.DepStatusInstalled,
			"log":            installLog,
			"python_version": pythonVersion,
		})
		return
	}
	dep := model.Dependency{
		Type:          depType,
		Name:          name,
		PythonVersion: pythonVersion,
		Status:        model.DepStatusInstalled,
		Log:           installLog,
	}
	database.DB.Create(&dep)
}

func buildEnvSlice(envVars map[string]string) []string {
	env := os.Environ()
	for k, v := range envVars {
		env = append(env, k+"="+v)
	}
	return env
}

func parseTaskExtensions(raw string) map[string]bool {
	exts := make(map[string]bool)
	for _, token := range strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ';' || r == '\n' || r == '\r' || r == '\t' || r == ' '
	}) {
		token = strings.TrimSpace(strings.ToLower(token))
		token = strings.TrimPrefix(token, "*")
		if token == "" {
			continue
		}
		if !strings.HasPrefix(token, ".") {
			token = "." + token
		}
		exts[token] = true
	}
	return exts
}

func shouldApplyRandomDelay(command string, allowedExts map[string]bool) bool {
	if len(allowedExts) == 0 {
		return true
	}

	scriptPath := extractTaskScriptPath(command)
	if scriptPath == "" {
		return false
	}

	ext := strings.ToLower(filepath.Ext(scriptPath))
	return allowedExts[ext]
}

func extractTaskScriptPath(command string) string {
	tokens, err := splitCommandTokens(command)
	if err != nil || len(tokens) < 2 {
		return ""
	}

	switch tokens[0] {
	case "task":
		remaining := tokens[1:]
		for len(remaining) > 0 {
			switch remaining[0] {
			case "-m":
				if len(remaining) < 2 {
					return ""
				}
				remaining = remaining[2:]
			case "-l":
				remaining = remaining[1:]
			default:
				taskShellTokens, _ := splitTaskShellAndScriptArgs(remaining)
				for count := len(taskShellTokens); count >= 1; count-- {
					candidate := strings.Join(taskShellTokens[:count], " ")
					if isSupportedScriptExtension(candidate) {
						return candidate
					}
				}
				return ""
			}
		}
		return ""
	case "desi", "python", "python3", "python3.10", "python3.11", "python3.12", "node", "ts-node", "bash", "go":
		for count := len(tokens) - 1; count >= 2; count-- {
			candidate := strings.Join(tokens[1:count], " ")
			if isSupportedScriptExtension(candidate) {
				return candidate
			}
		}
		if isSupportedScriptExtension(tokens[1]) {
			return tokens[1]
		}
		return ""
	default:
		return ""
	}
}
