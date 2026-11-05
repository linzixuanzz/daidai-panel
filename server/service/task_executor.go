package service

import (
	"bytes"
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
	// executingRuns 登记每个任务此刻处于「执行窗口」里的每一次执行（taskID -> 执行集合）。
	// 窗口在 OnTaskExecuting 把状态写成运行中之前打开、在 runTask 结算时关闭，覆盖「已写运行中、进程还没登记」、
	// 前置钩子、重试等待、依赖自动安装、后置钩子这一整段。原来 StopTask 只看 runningProcesses，
	// 落在进程登记之前的停止会返回 false、什么都不做，进程照常跑完被记成成功。
	// 停止请求记在每一次执行自己身上（executingRun.stop），只作用于停止那一刻正在执行的那几次：
	// 多实例下停止之后才开始的执行不受影响，同一批被停的执行各自结算成已终止。
	executingRuns map[uint]map[*executingRun]struct{}
	// preparedRuns 记录 OnTaskExecuting 已经打开、还没被 runTask 接手的执行窗口。
	// runTask 接手时沿用它（停止请求可能已经记在上面）；准备后没能进入 runTask（OnTaskFailed、缺日志兜底）时据它关窗。
	preparedRuns map[*ExecutionRequest]*executingRun
	// halted 在面板关停（StopAllRunningTasks）后置为 true，之后不再复位：关停后这个执行器不会再被复用。
	// 之后才开窗的执行（worker 恰好在关停前取到请求、正在准备）一开窗就带上 runStopHalt，
	// 前置钩子也不再启动。受 processLock 保护。
	halted      bool
	processLock sync.Mutex
	runWG       sync.WaitGroup
}

// runStopKind 是一次执行收到的停止请求种类，只升不降。
type runStopKind int

const (
	runStopNone runStopKind = iota
	// runStopHalt：面板关闭 / 重启时的整体中断（StopAllRunningTasks）。拦「继续启动新进程」，
	// 结算仍按失败（不记成手动停止），由 runTask 自己写「[面板正在关闭，任务已中断]」、跳过后置脚本、不发失败通知；
	// 关停截止前没能结算的执行才由 MarkActiveTasksInterrupted 兜底标成中断。
	runStopHalt
	// runStopManual：手动 / 批量 / 定时停止（StopTask）。拦新进程，且本次执行结算成已终止。
	runStopManual
)

// executingRun 是一次执行在执行窗口里的登记。stop、processes、hookProcesses、scriptToken、closed 受 processLock 保护；
// stopCh 创建后不再替换（因此可以不持锁读），第一次收到停止请求时关闭，重试等待靠它提前醒来。
type executingRun struct {
	taskID    uint
	stop      runStopKind
	stopCh    chan struct{}
	processes map[int]*os.Process
	// hookProcesses 是此刻正在跑的钩子进程（前置钩子，以及关停前就已经开始跑的后置钩子）。刻意不进 runningProcesses / processes：
	// 手动停止从来不杀钩子（行为保持不变），只有面板关停（StopAllRunningTasks）才按进程组杀掉它们——
	// 不杀的话关停要白等钩子跑完，二进制 / Magisk 部署下面板退出后钩子还会变成孤儿继续跑。
	hookProcesses map[int]*os.Process
	// scriptToken 是这次执行注入脚本的面板凭据。关停截止时还没结算的执行由 revokeUnsettledScriptTokens 吊销。
	scriptToken *ScriptTokenInfo
	closed      bool
}

// requestStopLocked 给这次执行记下停止请求：种类只升不降（关机之后又点手动停止会升级成手动，
// 反过来不会把手动停止降级成关机），第一次收到请求时关闭 stopCh 叫醒正在等重试间隔的那一轮。
// 调用方必须持有 processLock。
func (r *executingRun) requestStopLocked(kind runStopKind) {
	if r == nil || kind <= r.stop {
		return
	}
	r.stop = kind
	select {
	case <-r.stopCh:
		// 升级种类时 stopCh 已经关过，不能再关一次。
	default:
		close(r.stopCh)
	}
}

// stopNotice 是执行器因停止打进任务日志的提示行，前缀已登记到 panelMetaLinePrefixes。
func stopNotice(kind runStopKind, action string) string {
	if kind == runStopHalt {
		return "[面板正在关闭，" + action + "]\n"
	}
	return "[任务已被手动停止，" + action + "]\n"
}

func NewTaskExecutor() *TaskExecutor {
	return &TaskExecutor{
		scriptsDir:       config.C.Data.ScriptsDir,
		logDir:           config.C.Data.LogDir,
		runningProcesses: make(map[uint]map[int]*os.Process),
		executingRuns:    make(map[uint]map[*executingRun]struct{}),
		preparedRuns:     make(map[*ExecutionRequest]*executingRun),
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

	// 状态马上要写成运行中，用户从这一刻起就能点停止：先把执行窗口打开，
	// 停止落在「已写运行中、进程还没登记」这段里时才认得出这次执行。窗口由 runTask 结算时关闭；
	// 准备阶段失败（下面建日志失败、或调用方随后的 OnTaskFailed）则由 closePreparedRun 关掉。
	e.openPreparedRun(req)

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
			// 准备失败，这次执行不会再有 runTask 来结算：窗口必须在这里关掉。
			e.closePreparedRun(req)
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
		// 兜底也必须把已经建好的实时日志收口，否则 TinyLog 会永远留在管理器里泄漏；
		// 准备阶段开的执行窗口同理，不关的话这个任务会永远显示成「正在执行」。
		e.closePreparedRun(req)
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

	// 准备阶段打开过执行窗口（OnTaskExecuting 成功后失败、或调度阶段 panic）时必须关掉，
	// 这次执行已经在这里结算完了，不会再有 runTask 接手。
	e.closePreparedRun(req)

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
	inactiveStatus := ResolveTaskInactiveStatus(task)
	if inactiveStatus == model.TaskStatusDisabled {
		// 与 runTask 结算块同一口径：禁用意图到这里已经落地，标记用完即清。
		// 禁用中的任务被手动运行时 RunNow 会打这笔标记（issue #133），准备阶段就失败
		// （依赖任务上次未成功、脚本不存在……）同样是这次运行的最终结算，不清的话标记会一直挂在任务上。
		ClearPendingDisable(task.ID)
	}
	database.DB.Model(task).Updates(map[string]interface{}{
		"status":            inactiveStatus,
		"last_run_at":       now,
		"last_run_status":   runStatus,
		"last_running_time": 0.0,
		"pid":               gorm.Expr("NULL"),
	})
}

// KillProcessGroup 立即对 p 所在的整个进程组发 SIGKILL（再补一个 p.Kill() 兜住 Windows 与「组长自己换了进程组」）。
// 要先 SIGTERM、宽限期过后再 SIGKILL 的（超时、各类停止、关停、修复 A 的残留清理），用 TerminateProcessGroup。
func KillProcessGroup(p *os.Process) {
	if p == nil {
		return
	}
	killGroup(p)
	p.Kill()
}

// KillProcessByPid 是 KillProcessGroup 的按 PID 版本，同样立即 SIGKILL。
// 要先 SIGTERM、宽限期过后再 SIGKILL 的，用 TerminateProcessByPid。
func KillProcessByPid(pid int) {
	// pid <= 1 一律不动手，口径同 process_unix.go 的 signalGroupTerm / processGroupAlive：
	// pid 0 时 Kill(-0, …) 打的是面板自己的进程组；pid 1 时 Kill(-1, SIGKILL) 会打到面板有权限打的所有进程
	// （容器里以 root 跑时 nginx 连同全部任务一起被杀，面板网页打不开、要重启容器才恢复；Magisk / 裸机上是整台机器）。
	// pid 来自库里的任务记录，恢复备份、脏数据都可能让它变成 0 或 1，而调用方只挡了 <= 0
	// （TerminateProcessByPid 对 pid 1 发不出 TERM，正是转到这里立即 KILL）。
	if pid <= 1 {
		return
	}
	killGroupByPid(pid)
	p, err := os.FindProcess(pid)
	if err != nil {
		return
	}
	p.Kill()
}

// groupTermGrace 是「先 SIGTERM、再 SIGKILL」之间的宽限期（#159 修复 B）：超时、手动 / 批量 / 定时停止、
// 调试运行停止、修复 A 的残留清理都用它，给 trap 了 TERM 的脚本、收到 TERM 会自己关浏览器的 Puppeteer 留出收尾时间。
// 用 var 而不是 const，是为了测试里能调短（先例：handler/deps.go 的 dependencyOutputWaitDelay）。
var groupTermGrace = 5 * time.Second

// shutdownTermGrace 是面板关停时所有进程组共用的 TERM 宽限期（见 StopAllRunningTasks），
// 加上 SignalStop 的 1 秒与 ShutdownSchedulerV2 的 3 秒结算等待，整体压在 main.go 的 8 秒关停预算里。
// Magisk 部署下关停不用它：动作按钮只等 2 秒，关停时所有进程组立即整组 KILL（理由见 StopAllRunningTasks）。
var shutdownTermGrace = 2 * time.Second

// waitProcessGroupGone 每 50ms 查一次以 pid 为组号的进程组：组里的进程都退了返回 true，等满 limit 还有进程返回 false。
// limit <= 0 时只查一次（关停时共享的截止时间已经用完，后面几组只看一眼就决定要不要 KILL）。
func waitProcessGroupGone(pid int, limit time.Duration) bool {
	deadline := time.Now().Add(limit)
	for processGroupAlive(pid) {
		if !time.Now().Before(deadline) {
			return false
		}
		time.Sleep(50 * time.Millisecond)
	}
	return true
}

// TerminateProcessGroup 先给 p 所在的整个进程组发 SIGTERM，groupTermGrace（5 秒）后组里还有进程才整组 SIGKILL（#159 修复 B）。
// 不阻塞调用方：等待与补刀放在单独的协程里，HTTP 停止接口可以立即返回，修复 A 的残留清理也不会推迟结算。
// TERM 发不出去（组已经不在、或 Windows）时直接走 KillProcessGroup，和改动前一致。
//
// 宽限期满后调 KillProcessGroup 而不是只发 Kill(-pid, SIGKILL)：多一个 p.Kill() 兜住「组长自己换了进程组」；
// p 已经被 Wait 回收时 Go 只会返回 os: process already finished，不会误杀别的进程；
// 组里只要还有成员，内核就不会把这个组号分配出去，所以 5 秒后的 Kill(-pid, …) 只会落在原来那一组上。
// 代价：补刀协程随所在进程退出而消失，面板 / ddp 在宽限期内退出时，忽略 TERM 的进程会留下来（与改动前的残留一样）。
func TerminateProcessGroup(p *os.Process) {
	if p == nil {
		return
	}
	if !signalGroupTerm(p.Pid) {
		KillProcessGroup(p)
		return
	}
	// 在发起协程之前读宽限期：测试会临时调短它，协程里再读全局变量会和测试收尾时的还原互相踩
	grace := groupTermGrace
	go func() {
		if !waitProcessGroupGone(p.Pid, grace) {
			KillProcessGroup(p)
		}
	}()
}

// TerminateProcessByPid 是 TerminateProcessGroup 的按 PID 版本（#159 修复 B），
// 只给「执行器不认识这次执行、只能按库里记的 PID 兜底」的停止路径用（例如任务是 ddp task run 在另一个进程里跑起来的）。
// 同样不阻塞：TERM 发不出去就立即 KillProcessByPid，发出去了就在协程里等 groupTermGrace，组还在才补刀。
func TerminateProcessByPid(pid int) {
	if !signalGroupTerm(pid) {
		KillProcessByPid(pid)
		return
	}
	grace := groupTermGrace
	go func() {
		if !waitProcessGroupGone(pid, grace) {
			KillProcessByPid(pid)
		}
	}()
}

func (e *TaskExecutor) StopTask(taskID uint) bool {
	if e == nil {
		return false
	}

	e.processLock.Lock()
	runs := e.executingRuns[taskID]
	processes := e.runningProcesses[taskID]
	if len(runs) == 0 && len(processes) == 0 {
		e.processLock.Unlock()
		return false
	}

	// 先打"手动停止"标记再 kill，保证完成块结算时标记已可见。
	markManualStop(taskID)
	// 停止请求记在此刻正在执行的每一次执行上：onStart 一登记就杀掉刚起的进程、重试循环每一轮跳出、
	// 重试等待立刻醒来，本次执行结算成已终止。原来这里只看 runningProcesses：
	// 落在「已进入执行、进程还没登记」窗口里的停止直接返回 false、什么都不做，
	// 进程随后照常启动跑完、被记成成功 —— 修复的正是这段竞态。
	// 记在每一次执行自己身上而不是按任务 id 挂一笔，多实例下才不会误伤停止之后才开始的执行。
	victims := make([]*os.Process, 0, len(processes))
	for _, process := range processes {
		victims = append(victims, process)
	}
	for run := range runs {
		run.requestStopLocked(runStopManual)
		run.processes = make(map[int]*os.Process)
	}
	delete(e.runningProcesses, taskID)
	e.processLock.Unlock()

	// 杀进程放在锁外：发信号要走系统调用，拿着 processLock 发会把 onStart 的进程登记、
	// HasRunningProcess 这些短临界区一起堵住。标记与请求都已经在锁内落定，这里只剩收尾。
	// #159 修复 B：先给整组发 SIGTERM，让 trap 了 TERM 的脚本自己收尾，5 秒后组里还有进程才 SIGKILL。
	// TerminateProcessGroup 不阻塞（补刀在单独的协程里），停止接口照样立即返回。
	// 返回 true 即表示执行器已认领这次停止：调用方不要再按库里的 PID 补刀，否则紧跟的第二个信号会打断脚本的收尾。
	for _, process := range victims {
		TerminateProcessGroup(process)
	}
	return true
}

// newExecutingRunLocked 建一次执行窗口登记并挂到任务名下，同时回报「本任务此前没有别的执行在窗口里」。
// 调用方必须持有 processLock。
func (e *TaskExecutor) newExecutingRunLocked(taskID uint) (*executingRun, bool) {
	run := &executingRun{
		taskID:    taskID,
		stopCh:    make(chan struct{}),
		processes: make(map[int]*os.Process),
	}
	if e.halted {
		// 面板已经在关停：worker 恰好在关停前取到请求、此刻才开窗。StopAllRunningTasks 已经扫过一遍，
		// 不在这里补上的话，这次执行会照常起进程，面板退出后变成孤儿。
		run.requestStopLocked(runStopHalt)
	}
	if e.executingRuns == nil {
		e.executingRuns = make(map[uint]map[*executingRun]struct{})
	}
	first := len(e.executingRuns[taskID]) == 0
	if e.executingRuns[taskID] == nil {
		e.executingRuns[taskID] = make(map[*executingRun]struct{})
	}
	e.executingRuns[taskID][run] = struct{}{}
	return run, first
}

// beginExecuting 打开一次执行窗口：从这里起，即便进程还没登记，StopTask 也能认出这次执行。
func (e *TaskExecutor) beginExecuting(taskID uint) *executingRun {
	if e == nil {
		return nil
	}
	e.processLock.Lock()
	run, first := e.newExecutingRunLocked(taskID)
	e.processLock.Unlock()

	if first {
		// 本任务此刻没有别的执行在窗口里，那么还留着的手动停止标记只可能是上一次运行残留的
		// （例如网页停止一个由 `ddp task run` 在别的进程里跑起来的任务：执行器里没有这次执行，
		// handler 按库里的 PID 兜底时照样会 MarkManualStop）。不清掉的话，它会把这次刚开始的
		// 执行错判成已终止、还顺手吞掉成功通知。已经有执行在窗口里时不能清：那笔标记可能正是给它的。
		consumeManualStop(taskID)
	}
	return run
}

// endExecuting 关掉一次执行窗口，幂等（结算 defer 与 runTask 的兜底 defer 都会调）。
// 本任务最后一次执行窗口关掉时，把可能在结算之后才落下的残留手动停止标记读即清，避免串到下一次运行。
func (e *TaskExecutor) endExecuting(run *executingRun) {
	if e == nil || run == nil {
		return
	}

	taskID := run.taskID
	e.processLock.Lock()
	if run.closed {
		e.processLock.Unlock()
		return
	}
	run.closed = true
	if runs, ok := e.executingRuns[taskID]; ok {
		delete(runs, run)
		if len(runs) == 0 {
			delete(e.executingRuns, taskID)
		}
	}
	// 兜底摘干净这次执行登记过的进程：正常路径下结算块已经摘过，panic 等异常路径靠这里。
	for pid := range run.processes {
		e.removeProcessLocked(taskID, pid)
	}
	run.processes = make(map[int]*os.Process)
	drained := len(e.executingRuns[taskID]) == 0
	e.processLock.Unlock()

	if drained {
		// 正常路径下标记早已被 applyManualStopOverride 消费，这里命不中，纯属兜底。
		consumeManualStop(taskID)
	}
}

// openPreparedRun 在准备阶段（OnTaskExecuting 把状态写成运行中之前）打开执行窗口，
// 并记到 preparedRuns 上等 runTask 接手。用户从状态变成运行中那一刻起就能点停止，
// 而进程还要经过建日志、环境准备、前置钩子好几步才登记，这段同样必须认得出「正在执行」。
func (e *TaskExecutor) openPreparedRun(req *ExecutionRequest) *executingRun {
	if e == nil || req == nil {
		return nil
	}
	run := e.beginExecuting(req.TaskID)
	e.processLock.Lock()
	if e.preparedRuns == nil {
		e.preparedRuns = make(map[*ExecutionRequest]*executingRun)
	}
	e.preparedRuns[req] = run
	e.processLock.Unlock()
	return run
}

// takePreparedRun 取走准备阶段打开的执行窗口（读即删），交给 runTask 接手。
// 取不到说明这次执行没走准备阶段（测试直接调 runTask、或兜底路径），由调用方自己开窗。
func (e *TaskExecutor) takePreparedRun(req *ExecutionRequest) *executingRun {
	if e == nil || req == nil {
		return nil
	}
	e.processLock.Lock()
	defer e.processLock.Unlock()

	run, ok := e.preparedRuns[req]
	if !ok {
		return nil
	}
	delete(e.preparedRuns, req)
	return run
}

// closePreparedRun 关掉准备阶段打开、却没能进入 runTask 的执行窗口（OnTaskFailed、缺日志兜底）。
// 不关的话窗口只增不减：之后对这个空闲任务点停止会被当成「正在执行」返回 true，
// 停止请求还会挂到下一次运行上。没有窗口时是空操作。
func (e *TaskExecutor) closePreparedRun(req *ExecutionRequest) {
	e.endExecuting(e.takePreparedRun(req))
}

// runStopKind 只读地回答「这次执行此刻收到的停止请求是哪一种」，供重试循环守卫使用。
func (e *TaskExecutor) runStopKind(run *executingRun) runStopKind {
	if e == nil || run == nil {
		return runStopNone
	}
	e.processLock.Lock()
	defer e.processLock.Unlock()
	return run.stop
}

// canClaimTaskManualStopMark 判断这次结算能不能认领那笔按任务 id 的手动停止标记。
//
// 标记是任务级的、只有一笔，而外部停止路径（handler 的 PID 兜底、定时停止、旧调度器）只知道任务 id、
// 不知道该算到哪一次执行头上。多实例下「谁先结算谁读即清」会把一次停止记到另一个根本没被停的执行头上：
// 那次执行被判成已终止，成功通知也被吞掉。所以只有本任务此刻没有别的执行还在窗口里时才认领 ——
// 那种情况下标记只可能是给自己的。
func (e *TaskExecutor) canClaimTaskManualStopMark(run *executingRun) bool {
	if e == nil || run == nil {
		// 没有执行窗口信息（理论上不会发生），维持原来的口径。
		return true
	}
	e.processLock.Lock()
	defer e.processLock.Unlock()
	return len(e.executingRuns[run.taskID]) <= 1
}

// removeProcessLocked 从任务进程表里摘掉一个 pid，空了就把整条删掉。调用方必须持有 processLock。
func (e *TaskExecutor) removeProcessLocked(taskID uint, pid int) {
	procs, ok := e.runningProcesses[taskID]
	if !ok {
		return
	}
	delete(procs, pid)
	if len(procs) == 0 {
		delete(e.runningProcesses, taskID)
	}
}

// releaseRunProcesses 结算时把这次执行登记过的进程摘掉。
// 只摘自己这一次的 pid：多实例下同一任务还有别的执行在跑，整条 delete 会把别人的进程一起抹掉，
// 之后对那个任务点停止就找不到进程可杀了。
func (e *TaskExecutor) releaseRunProcesses(run *executingRun) {
	if e == nil || run == nil {
		return
	}
	e.processLock.Lock()
	defer e.processLock.Unlock()

	for pid := range run.processes {
		e.removeProcessLocked(run.taskID, pid)
	}
	run.processes = make(map[int]*os.Process)
}

// killRunningProcess 把某个已登记进程从进程表里摘掉并连进程组一起杀掉。
// 供 onStart 在「登记时发现已有停止请求」的竞态窗口里立即收尾。
func (e *TaskExecutor) killRunningProcess(run *executingRun, taskID uint, process *os.Process) {
	if process == nil {
		return
	}
	e.processLock.Lock()
	e.removeProcessLocked(taskID, process.Pid)
	if run != nil {
		delete(run.processes, process.Pid)
	}
	e.processLock.Unlock()
	KillProcessGroup(process)
}

// waitRetryInterval 等待重试间隔，期间收到停止请求就立刻醒来并返回 true。
// 原来这里是 time.Sleep：停止落在等待里得白等满整个间隔才生效，间隔设成几分钟时用户会以为停止没反应。
func waitRetryInterval(run *executingRun, seconds int) bool {
	if seconds <= 0 {
		return false
	}
	timer := time.NewTimer(time.Duration(seconds) * time.Second)
	defer timer.Stop()

	if run == nil {
		<-timer.C
		return false
	}
	select {
	case <-run.stopCh: // 创建后不再替换，可以不持锁读
		return true
	case <-timer.C:
		return false
	}
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
	e.halted = true
	processesByTask := e.runningProcesses
	e.runningProcesses = make(map[uint]map[int]*os.Process)
	// 关机同样要拦住执行窗口：只杀已登记进程的话，落在窗口里的执行随后照样启动新进程，
	// 被杀的那一轮还会按失败继续重试，面板退出后这些子进程（独立进程组）会变成孤儿继续跑。
	// 关机不是手动停止，用 runStopHalt：只拦「继续启动新进程」，结算口径仍是失败（runTask 自己写中断提示）。
	// 正在跑的钩子（前置钩子、关停前已经开始的后置钩子）一并按进程组杀掉（它们不在进程表里，手动停止不碰它们）。
	var hookVictims []*os.Process
	for _, runs := range e.executingRuns {
		for run := range runs {
			run.requestStopLocked(runStopHalt)
			run.processes = make(map[int]*os.Process)
			for _, process := range run.hookProcesses {
				hookVictims = append(hookVictims, process)
			}
			run.hookProcesses = nil
		}
	}
	e.processLock.Unlock()

	// #159 修复 B：所有进程组（任务进程，加上正在跑的钩子）先一起收到 SIGTERM（Magisk 部署例外，见下面的 termGrace），
	// 给 trap 了 TERM 的脚本、收到 TERM 会自己关浏览器的 Puppeteer 一个收尾的机会；
	// 然后共用一个 shutdownTermGrace（2 秒）的截止时间逐组等：先退出的不占后面的时间，到点还在的统一 SIGKILL。
	// 这里必须阻塞到 KILL 落地再返回：Docker 停容器时 PID 1 一退出内核会收走整个命名空间，
	// 但「重启面板」（退出码 1、容器不停）之后就没人再管这些进程组了，不等的话忽略 TERM 的进程会成为孤儿。
	// 第二次调用（ShutdownSchedulerV2 里会再走一遍 HaltSchedulerV2）时进程表已经空了，立刻返回。
	victims := make([]*os.Process, 0, len(hookVictims))
	for _, processes := range processesByTask {
		for _, process := range processes {
			victims = append(victims, process)
		}
	}
	victims = append(victims, hookVictims...)

	// TERM 宽限在这里按部署决定：Magisk 部署下不给宽限，所有进程组立即整组 SIGKILL（与 v3.3.5 一致）。
	// 模块管理器里的动作按钮「停止」是 kill -TERM 面板、sleep 2、面板还在就 kill -KILL（Magisk/action.sh），动作按钮只等 2 秒；
	// 走到这里之前 SignalStop 可能已经用掉 1 秒，再等 2 秒宽限就装不下：面板先被 KILL，忽略 TERM 的任务进程组没人补刀、成了孤儿。
	// action.sh 是模块外壳，在线升级改不到它（只有重刷 zip 才会换），只能面板这边让步。
	// Magisk 判断直接复用 playwrightMagiskRuntime（名字带 playwright 只因它最早给 Playwright 默认目录用）：
	// IsMagiskModuleRuntime 之外也认 service.sh 拉起面板时 export 的 DAIDAI_MAGISK_SHELL_VERSION，不另起一套口径。
	// 其它部署（Docker 默认等 10 秒、systemd 30 秒、二进制）照旧共享 shutdownTermGrace；
	// 手动 / 批量 / 定时停止、超时、调试运行停止不经过 action.sh，在 Magisk 上也照旧先 TERM、5 秒后 KILL。
	termGrace := shutdownTermGrace
	if playwrightMagiskRuntime() {
		termGrace = 0
	}

	var termed []*os.Process
	for _, process := range victims {
		if termGrace > 0 && signalGroupTerm(process.Pid) {
			termed = append(termed, process)
		} else {
			// 不给宽限（Magisk），或 TERM 发不出去（组已经不在、或 Windows）：立刻整组 KILL
			KillProcessGroup(process)
		}
	}
	deadline := time.Now().Add(termGrace)
	killed := 0
	for _, process := range termed {
		if !waitProcessGroupGone(process.Pid, time.Until(deadline)) {
			KillProcessGroup(process)
			killed++
		}
	}
	if killed > 0 {
		// 事后判断「关停为什么慢了 2 秒」用：只有真有进程组被强制结束时才打
		log.Printf("%d task process group(s) still running %s after SIGTERM, killed", killed, termGrace)
	}
	return len(victims)
}

// halting 回答「面板是否已经在关停」：之后不再启动新的前置钩子，也不再跑后置脚本。
func (e *TaskExecutor) halting() bool {
	if e == nil {
		return false
	}
	e.processLock.Lock()
	defer e.processLock.Unlock()
	return e.halted
}

// registerHookProcess 登记一个刚启动的钩子进程（前置 / 后置），供关停时按进程组杀掉。
// 面板已经在关停（钩子起来的那一刻正好撞上 StopAllRunningTasks）时不登记、直接杀掉，否则它会一直跑到超时。
func (e *TaskExecutor) registerHookProcess(run *executingRun, process *os.Process) {
	if e == nil || run == nil || process == nil {
		return
	}
	e.processLock.Lock()
	halted := e.halted
	if !halted {
		if run.hookProcesses == nil {
			run.hookProcesses = make(map[int]*os.Process)
		}
		run.hookProcesses[process.Pid] = process
	}
	e.processLock.Unlock()
	if halted {
		KillProcessGroup(process)
	}
}

// releaseHookProcess 在钩子结束后注销它，免得关停时去杀一个早已退出（pid 可能被复用）的进程组。
func (e *TaskExecutor) releaseHookProcess(run *executingRun, process *os.Process) {
	if e == nil || run == nil || process == nil {
		return
	}
	e.processLock.Lock()
	delete(run.hookProcesses, process.Pid)
	e.processLock.Unlock()
}

// attachRunScriptToken 把这次执行注入脚本的面板凭据记到执行窗口上，关停截止时据它吊销。
func (e *TaskExecutor) attachRunScriptToken(run *executingRun, token *ScriptTokenInfo) {
	if e == nil || run == nil || token == nil {
		return
	}
	e.processLock.Lock()
	run.scriptToken = token
	e.processLock.Unlock()
}

// revokeUnsettledScriptTokens 吊销此刻仍在执行窗口里（还没结算）的每一次执行注入脚本的面板凭据，返回吊销了几枚。
// 只给关停兜底用：ShutdownSchedulerV2 等到截止还有执行没结算时，结算里那句吊销已经等不到了——
// 典型是任务派生了逃出进程组、还攥着输出管道的孙进程，runTask 卡在读输出里。
// 重复吊销安全（blockToken 按 jti 去重），之后那次执行真的结算了再吊一次也无害。
func (e *TaskExecutor) revokeUnsettledScriptTokens() int {
	if e == nil {
		return 0
	}
	e.processLock.Lock()
	var tokens []*ScriptTokenInfo
	for _, runs := range e.executingRuns {
		for run := range runs {
			if run.scriptToken != nil {
				tokens = append(tokens, run.scriptToken)
			}
		}
	}
	e.processLock.Unlock()

	for _, token := range tokens {
		RevokeScriptToken(token)
	}
	return len(tokens)
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

	// 接手准备阶段打开的执行窗口（停止请求可能已经记在上面了）；直接调 runTask 的路径自己开一个。
	// 从这里起，即便进程还没登记，StopTask 也能认出「这次执行正在进行」。
	run := e.takePreparedRun(req)
	if run == nil {
		run = e.beginExecuting(req.TaskID)
	}
	// 关窗兜底：defer 后进先出，它在下面的结算 defer 之后才执行，
	// 于是 panic、结算块自己出错这些路径都不会把窗口留下（endExecuting 幂等，正常路径重复调用无害）。
	defer e.endExecuting(run)

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

	// #159 修复 A：主命令自己结束后，要不要清理它进程组里留在后台的进程，由这里挂到 plan 上的判定决定。
	// 开机任务按任务类型豁免，不论是开机自动触发还是手动点运行——这类任务常用来拉起常驻服务，
	// 而且每次开机只跑一次，不会累积。总开关关闭时也不清。每次执行读一次配置，保存后对之后开始的执行生效。
	// 判定推迟到主命令结束那一刻才做：这次执行已被手动停止或面板正在关停时，停止路径已经对整组发过 TERM、
	// 也安排了 KILL，这里再发一次会把第二个信号打进脚本的收尾逻辑，也不该写「清理残留进程」那一行。
	// 先清成 nil：plan 挂在请求上，万一被复用，不能沿用上一次执行留下的判定。
	plan.ShouldCleanupProcessGroup = nil
	if task.GetTaskType() != model.TaskTypeStartup && model.GetRegisteredConfigBool("cleanup_leftover_processes") {
		plan.ShouldCleanupProcessGroup = func() bool { return e.runStopKind(run) == runStopNone }
	}

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
	// 任务级「可用环境变量」（#168）：「全部」时是 nil，与改动前一致；「指定」时只注入命中的变量，脚本令牌只能发通知。
	envScope := TaskRuntimeEnvScope(task)
	envVars, scriptToken, envErr := BuildManagedRuntimeEnvMapWithScriptToken(taskWorkDir, e.scriptsDir, task.NotificationChannelID, envTTL, task.PythonVersion, envScope)
	if envErr != nil {
		log.Printf("prepare task runtime env failed: %v", envErr)
	}
	// 任务标识（#176）：「后台进程」页靠它认出进程属于哪个任务。放在构建完之后写，
	// 所以不受「可用环境变量」范围裁剪，用户建了同名变量也会被面板值覆盖；
	// DAIDAI_ 前缀在前置脚本回传的保护名单里，前置脚本 export 也改不掉它。
	if envVars == nil {
		envVars = make(map[string]string)
	}
	envVars[TaskIDEnvKey] = strconv.FormatUint(uint64(task.ID), 10)
	// 记到执行窗口上：面板关停时这次执行要是卡住、等不到下面结算里的吊销，ShutdownSchedulerV2 在截止时据它吊销。
	e.attachRunScriptToken(run, scriptToken)

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
		manualAborted := false
		if e.runStopKind(run) == runStopManual {
			// 本次执行自己收到过停止请求：不依赖那笔按任务 id 的手动停止标记（只有一笔，
			// 多实例下同一次停止命中的两次执行会抢，抢输的被记成普通失败），各自判成已终止；
			// 顺手把标记消费掉，免得串到下一次运行。
			// runStopHalt（面板关闭）刻意不走这里：关机按失败结算（日志里已写明「面板正在关闭，任务已中断」）。
			consumeManualStop(req.TaskID)
			runStatus, logStatus, manualAborted = model.RunAborted, model.LogStatusAborted, true
		} else if e.canClaimTaskManualStopMark(run) {
			// 没被执行器停过，才去认领那笔任务级标记（外部停止路径只知道任务 id）。
			// applyManualStopOverride 读即清，自然完成时返回原状态、manualAborted=false。
			runStatus, logStatus, manualAborted = applyManualStopOverride(req.TaskID, runStatus, logStatus)
		}
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

		// 只摘这次执行自己登记过的进程：整条 delete 会把同一任务另一个实例的进程一起抹掉。
		// 执行窗口本身由 runTask 开头那个兜底 defer 关闭（它在本结算 defer 之后执行）。
		e.releaseRunProcesses(run)

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
		// 被面板关停打断的这次执行不发失败通知（D15）：失败是面板自己造成的，原来被 SIGKILL 时也从不通知；
		// 而且通知是异步发的，进程马上就退出，发没发出去全凭运气，发出去的还写着「执行失败、退出码 -1」，会误导人。
		haltedByShutdown := !finalSuccess && e.runStopKind(run) == runStopHalt
		if !finalAborted && !finalSuccess && task.NotifyOnFailure && !haltedByShutdown {
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

	// #159 修复 D：只留输出的最后 outputTailLimit（1MiB）字节，不再整份累积（原来上限约 97.7MiB，
	// 常驻大输出的任务会在堆里多存一份）。失败 / 成功摘要、依赖自动识别、运行时失败提示都只看尾部，
	// 完整输出另有 TinyLog 与日志文件。
	var outputTail []byte

	onOutputWithCollect := func(chunk string) {
		onOutput(chunk)
		outputCollectorMu.Lock()
		outputTail = appendOutputTail(outputTail, chunk)
		outputCollectorMu.Unlock()
	}

	onOutput(fmt.Sprintf("=== 开始执行 [%s] ===\n", startTime.Format("2006-01-02 15:04:05")))
	// 指定了可用环境变量时在日志开头说明注入了哪些（#168）：只写变量名、不写值；最多列 20 个，超出的只写总数。
	if envScope != nil {
		onOutput(formatTaskEnvScopeLogLine(envScope.InjectedNames))
	}

	// 前置 / 后置钩子起来后登记到这次执行上：面板关停（StopAllRunningTasks）时按进程组杀掉，手动停止照旧不碰它们。
	// 钩子都是先后跑的，共用一个变量记住当前那个进程，跑完就注销。
	var hookProcess *os.Process
	onHookStart := func(process *os.Process) {
		hookProcess = process
		e.registerHookProcess(run, process)
	}
	releaseHook := func() {
		e.releaseHookProcess(run, hookProcess)
		hookProcess = nil
	}

	// 前置脚本（任务专属 + 全局 task_before.sh）里 export 的环境变量会按执行顺序
	// 增量合并回 envVars，供后面的目标脚本、task_after.sh、extra.sh 和后置脚本共用。
	// 这是青龙 task_before 的语义；细节与保护名单见 task_hook_env.go。
	// 面板开始关停之后不再启动新的前置钩子（halting），下面的重试循环会直接写「取消后续执行」。
	if task.TaskBefore != nil && *task.TaskBefore != "" && !e.halting() {
		onOutput("[执行前置脚本]\n")
		captureHookEnvExports(envVars, onOutput, func(hookEnv map[string]string) {
			// 前置脚本的错误过去被直接丢弃，bash 找不到、临时文件写不进去、超时，
			// 用户在任务日志里只能看到「[执行前置脚本]」一行。这里把它说出来，
			// 但仍然保持「前置脚本失败不中断任务」的既有行为。
			err := runInlineScript(*task.TaskBefore, e.scriptsDir, hookEnv, 60, onOutput, onHookStart, plan.ScriptArgs...)
			releaseHook()
			if err != nil {
				onOutput(fmt.Sprintf("[前置脚本执行失败: %s]\n", err.Error()))
			}
		})
	}

	if !e.halting() {
		captureHookEnvExports(envVars, onOutput, func(hookEnv map[string]string) {
			runHookScript("task_before.sh", e.scriptsDir, hookEnv, onOutput, onHookStart, plan.ScriptArgs...)
			releaseHook()
		})
	}

	retries := 0
	var lastExitCode int
	depInstallCount := 0
	maxDepInstalls := 5
	installedDeps := make(map[string]bool)

	for retries <= task.MaxRetries {
		// 停止请求可能落在前置钩子阶段、上一轮进程被杀之后、重试等待里，或依赖自动安装后的重试之前。
		// 每一轮开始先看一眼：已被停止就直接跳出，不再启动新进程。原来这里没有守卫——
		// 被杀的那一轮算失败，循环照常重试、再起一个新进程，停止等于没停。
		// 跳出后手动停止照常走后置脚本、关机则跳过后置脚本，再由结算统一判结果（手动停止判已终止，关机仍判失败）。
		if kind := e.runStopKind(run); kind != runStopNone {
			onOutput(stopNotice(kind, "取消后续执行"))
			break
		}

		if retries > 0 {
			onOutput(fmt.Sprintf("[第 %d 次重试，等待 %d 秒]\n", retries, task.RetryInterval))
			if waitRetryInterval(run, task.RetryInterval) {
				// 等待期间收到停止请求：立刻醒来跳出，不必白等满整个重试间隔。
				onOutput(stopNotice(e.runStopKind(run), "取消后续执行"))
				break
			}
		}

		// 不加锁：上一轮的进程已经结束、读协程已经排空，这时不会有并发写
		outputTail = nil
		onStart := func(process *os.Process) {
			stopKind := e.registerRunningProcess(run, req.TaskID, process)
			pid := process.Pid
			database.DB.Model(task).Update("pid", pid)
			if stopKind != runStopNone {
				// 停止请求早于进程登记（本次修复的竞态窗口）：一登记就连进程组一起杀掉，
				// 否则它会照常跑完、被结算成成功。结算口径由上面的停止种类决定。
				onOutput(stopNotice(stopKind, "终止刚启动的进程"))
				e.killRunningProcess(run, req.TaskID, process)
			}
		}
		effectiveTimeout := timeout
		if plan.TimeoutOverride != nil && *plan.TimeoutOverride > 0 {
			effectiveTimeout = *plan.TimeoutOverride
		}
		result, _, err := runCommandWithPlanFunc(plan, effectiveTimeout, envVars, maxLogSize, onOutputWithCollect, onStart)
		// 主命令一返回就读一次停止种类。#159 修复 B 起面板关停是先 SIGTERM：trap 了 TERM、收完尾以 0 退出的脚本，
		// 退出码会被判成成功，于是这次执行记成成功、不写「任务已中断」、还发成功通知，违反 runStopHalt 的契约（按失败结算、不发通知）。
		// 读到 runStopHalt 时这一轮不论退出码都按失败处理：不走下面成功那条 break，照常走失败分支，由循环开头的停止检查拦住重试；
		// 「任务已中断」和不发通知（haltedByShutdown）随之自然生效。重试、last_run_status、执行器写的「取消后续执行 / 任务已中断 /
		// 跳过后置脚本」都和改前「立即 KILL」时一样；不同的只是脚本自己退出、没有「被信号终止」那行，退出码照实记（改前是 -1）。
		// 读到的还是 runStopNone，说明主进程在关停之前就自己返回了，照旧按退出码判，成功就照常记成功。
		// 手动停止（runStopManual）不看这里：结算按这次执行收到的停止种类判成已终止，退出码是多少都一样。
		haltedThisRound := e.runStopKind(run) == runStopHalt
		if err != nil {
			onOutput(fmt.Sprintf("[执行错误: %s]\n", err.Error()))
			if strings.Contains(err.Error(), "illegal instruction") || strings.Contains(err.Error(), "core dumped") {
				onOutput("[提示] 该错误通常是因为当前 CPU 不支持程序所需的指令集（如 AVX/SSE），常见于部分 VPS 或 ARM 设备。建议更换支持相关指令集的服务器。\n")
			}
			retries++
			lastExitCode = 1
			outputCollectorMu.Lock()
			lastFailureOutput = buildTaskFailureOutput(string(outputTail), err.Error())
			outputCollectorMu.Unlock()
			continue
		}

		lastExitCode = result.ReturnCode
		if task.IsSuccessExitCode(result.ReturnCode) && !haltedThisRound {
			success = true
			lastFailureOutput = ""
			outputCollectorMu.Lock()
			lastSuccessOutput = string(outputTail)
			outputCollectorMu.Unlock()
			break
		}
		outputCollectorMu.Lock()
		lastFailureOutput = string(outputTail)
		outputCollectorMu.Unlock()

		if depInstallCount < maxDepInstalls && model.GetRegisteredConfigBool("auto_install_deps") {
			outputCollectorMu.Lock()
			collected := string(outputTail)
			outputCollectorMu.Unlock()
			if e.detectAndInstallDeps(plan, collected, envVars, installedDeps, onOutput) {
				depInstallCount++
				onOutput(fmt.Sprintf("[依赖已安装 (%d/%d)，自动重试执行]\n", depInstallCount, maxDepInstalls))
				continue
			}
		}

		// BuildRuntimeFailureHint = ESM 兼容提示 + Playwright 环境提示（#142）。
		// 补一个换行：提示本身不带换行，原来会和下一行「[第 N 次重试…]」或「=== 执行结束」粘在同一行。
		if hint := BuildRuntimeFailureHint(lastFailureOutput); hint != "" {
			onOutput(hint + "\n")
			outputCollectorMu.Lock()
			lastFailureOutput = strings.TrimSpace(string(outputTail))
			outputCollectorMu.Unlock()
		}

		retries++
	}

	exitCode = lastExitCode

	if e.halting() {
		// 面板正在关闭（D15）：不再启动任务后置脚本、task_after.sh、extra.sh。每个最长 60 秒，关停等不起
		// （Docker 宽限只有 10 秒，到点整组 SIGKILL，这次执行一样结算不了）。
		// 关停前就已经在跑的后置钩子走下面 else 分支，登记在 hookProcesses 里，由 StopAllRunningTasks 按进程组杀掉。
		// 写明「任务已中断」：结算就在这次 runTask 里完成，MarkActiveTasksInterrupted 不会再碰它，
		// 不写的话日志里只剩一行「被信号终止…常见原因：内存超限」，用户看不出是面板重启打断的。
		// 手动停止（runStopManual）的那次照旧结算成已终止，不写这一行。
		if !success && e.runStopKind(run) == runStopHalt {
			onOutput(stopNotice(runStopHalt, "任务已中断"))
		}
		onOutput(stopNotice(runStopHalt, "跳过后置脚本"))
	} else {
		// 后置脚本不参与环境变量回传：它跑完任务就结束了，回写没有消费方。
		// 但同样要把执行错误说出来，理由与前置脚本一致。
		// 三个后置钩子同样登记到这次执行上（onHookStart）：跑到一半面板开始关停时按进程组杀掉。
		// 不登记的话关停要白等它跑完，截止时这次执行只能被标成中断、输出丢失，二进制部署下钩子还会变成孤儿。
		// 关停之后才轮到的钩子一启动就会被 registerHookProcess 杀掉。
		if task.TaskAfter != nil && *task.TaskAfter != "" {
			onOutput("[执行后置脚本]\n")
			err := runInlineScript(*task.TaskAfter, e.scriptsDir, envVars, 60, onOutput, onHookStart, plan.ScriptArgs...)
			releaseHook()
			if err != nil {
				onOutput(fmt.Sprintf("[后置脚本执行失败: %s]\n", err.Error()))
			}
		}

		runHookScript("task_after.sh", e.scriptsDir, envVars, onOutput, onHookStart, plan.ScriptArgs...)
		releaseHook()
		runHookScript("extra.sh", e.scriptsDir, envVars, onOutput, onHookStart, plan.ScriptArgs...)
		releaseHook()
	}

	endTime := time.Now()
	duration := endTime.Sub(startTime).Seconds()

	completionNote := ""
	if success && lastExitCode != 0 {
		completionNote = "（已按任务配置判定成功）"
	}
	onOutput(fmt.Sprintf("=== 执行结束 [%s] 耗时 %.2f 秒 退出码 %d%s ===\n",
		endTime.Format("2006-01-02 15:04:05"), duration, lastExitCode, completionNote))
}

// registerRunningProcess 登记进程（任务进程表 + 这次执行自己的进程集合），
// 并在同一把锁内回报「这次执行此刻是否已经收到停止请求」。
// 回报非 runStopNone 说明停止早于进程登记（本次修复的竞态），调用方必须立刻把这个进程杀掉。
func (e *TaskExecutor) registerRunningProcess(run *executingRun, taskID uint, process *os.Process) runStopKind {
	if process == nil {
		return runStopNone
	}
	e.processLock.Lock()
	defer e.processLock.Unlock()

	if e.runningProcesses[taskID] == nil {
		e.runningProcesses[taskID] = make(map[int]*os.Process)
	}
	e.runningProcesses[taskID][process.Pid] = process
	if run == nil {
		return runStopNone
	}
	if run.processes == nil {
		run.processes = make(map[int]*os.Process)
	}
	run.processes[process.Pid] = process
	return run.stop
}

func buildTaskNotificationChannelIDs(channelID *uint) []uint {
	if channelID == nil || *channelID == 0 {
		return nil
	}
	return []uint{*channelID}
}

// outputTailLimit 是 runTask 为摘要、依赖识别、运行时失败提示保留的输出尾部上限（#159 修复 D）。
const outputTailLimit = 1 << 20

// appendOutputTail 把 chunk 接到 buf 后面，只保留最后 outputTailLimit 字节的输出（#159 修复 D）。
// 失败 / 成功摘要、依赖自动识别、运行时失败提示都只看尾部；完整输出另有 TinyLog 和日志文件。
// 超过上限时从下一行的开头算起，避免窗口的第一行是半截（也顺带避开被切开的多字节字符）。
// 底层数组最多约为上限的 2 倍：append 扩容时只拷贝窗口里的字节，旧数组随之被回收。
func appendOutputTail(buf []byte, chunk string) []byte {
	buf = append(buf, chunk...)
	if len(buf) <= outputTailLimit {
		return buf
	}
	buf = buf[len(buf)-outputTailLimit:]
	// 窗口里唯一的换行就在末尾时（一整行就超过 1MiB，例如压成一行的报错、jq -c 的输出），不再往后切，
	// 否则窗口会被切成空串，失败摘要和依赖识别什么都拿不到；这种情况保留这一行的后 1MiB
	if i := bytes.IndexByte(buf, '\n'); i >= 0 && i < len(buf)-1 {
		buf = buf[i+1:]
	}
	return buf
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
	// 自动安装 npm 包前打的兼容映射说明（NodeInstallCompatibilityNotice）。它走的是不收集输出的 onOutput，
	// 本来就进不了成功通知的摘录；登记只是守住「面板自己打进任务日志的行都要在册」这条契约（quality-guidelines.md）。
	"[Node.js 依赖]",
	"[重试启动失败:",
	"[任务异常崩溃:",
	// 执行器因停止 / 关机打进日志的提示行（stopNotice 产出的两种前缀）：
	// 「取消后续执行」「终止刚启动的进程」「任务已中断」「跳过后置脚本」都挂在这两个前缀下。
	"[任务已被手动停止，",
	"[面板正在关闭，",
	// 子进程被信号杀掉时的可诊断提示（#113 排查里补的）。被信号杀必然结算为失败、
	// 走的是不过滤的 failureExcerpt，所以登记它今天不改变任何行为；
	// 登记只是守住「面板输出行必须在册」这条契约，免得以后有人把它挪进成功摘录。
	"[脚本进程被信号终止：",
	// #159 修复 A：主命令结束后清理了留在进程组里的后台进程（leftoverProcessCleanupNotice）。
	// 成功的任务最容易出现这一行，不登记就会挤进成功通知那 30 行的日志摘录。
	"[已结束残留的后台进程：",
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

	if hint := BuildRuntimeFailureHint(output); hint != "" {
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
		// #159 修复 A 清理残留后台进程时打的那行提示（leftoverProcessCleanupNotice）经 onOutput 进了输出尾部，
		// 失败时它常常就是最后一行：不丢掉的话，Python 摘要认不出最后一行的异常而放弃，通用摘要的「上下文」也会取成这行提示。
		// 只丢这一种：别的面板行（例如「[脚本进程被信号终止：…]」）对失败诊断有用，失败摘要刻意不整体套 isPanelMetaLine。
		// 成功摘要本来就靠 isPanelMetaLine 滤掉它，在这里先丢不改变成功摘要的结果。
		if strings.HasPrefix(line, "[已结束残留的后台进程：") {
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

	// installedDeps 按包名记「本次执行里自动安装过一次的结果」（true = 装上了），同一个包只装一次。
	// 重试轮要按成败给出不同说法（#154）：原来装之前就记成 true，配了重试时会把「装失败」
	// 误报成「已安装但仍然报错」。「本次执行已装包数」由调用方只在返回 true（装成功）时累加，不看这张表。
	if installed, tried := installedDeps[candidate.PackageName]; tried {
		if installed {
			onOutput(fmt.Sprintf("[%s 已安装但仍然报错，可能是模块版本不兼容或内部依赖异常，请尝试手动安装指定版本]\n", candidate.DisplayName))
		} else {
			// 同一次执行里再装一遍结果也不会变（Alpine 上的 playwright 必然失败），只会白等一遍 pip / npm。
			// 复用已登记的「[安装失败:」前缀，不新增元信息前缀。
			onOutput(fmt.Sprintf("[安装失败: %s 本次执行已自动安装过一次并失败，不再重复安装，原因见上方日志]\n", candidate.DisplayName))
		}
		return false
	}

	// 这里打的每一行都要自带换行：onOutput 是原样写入的，原来不带换行时会粘成
	// 「…正在自动安装...][安装失败: …」（#154 的截图）和「…for playwright]=== 执行结束」。
	onOutput(fmt.Sprintf("[检测到缺失依赖: %s，正在自动安装...]\n", candidate.DisplayName))
	if candidate.Manager == "nodejs" {
		// NodeInstallCompatibilityNotice 的返回值本身不带换行。
		if notice := NodeInstallCompatibilityNotice(candidate.PackageName); notice != "" {
			onOutput(notice + "\n")
		}
	}
	result := InstallAutoDependency(candidate, envVars)
	if installedDeps != nil {
		installedDeps[candidate.PackageName] = result.Success
	}
	if !result.Success {
		failureReason := strings.TrimSpace(result.Error)
		if failureReason == "" {
			failureReason = "未知错误"
		}
		onOutput(fmt.Sprintf("[安装失败: %s]\n", failureReason))
		return false
	}

	onOutput(fmt.Sprintf("[安装成功: %s]\n", candidate.DisplayName))
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
		updates := map[string]interface{}{
			"status":         model.DepStatusInstalled,
			"log":            installLog,
			"python_version": pythonVersion,
		}
		// issue #166：已有 httpx 记录、这次自动装的是 httpx[http2] 时，名字要一起改成带 extras 的写法。
		// 只改状态不改名的话，重建容器后按旧名字 httpx 重装，h2 又丢了，脚本照样报错。
		// 旧名字的 extras 已经覆盖本次写法（比如旧的是 httpx[http2]、这次只是 httpx）就不动，免得把 extras 改丢。
		// 改名时用合并后的写法而不是整串换成本次写法：旧记录里的版本锁定（httpx==0.27.0）和已有 extras
		// （httpx[socks]）同样是重建容器重装时要用的，整串替换会把它们丢掉。这里只改登记名，
		// pip 这次实际执行的仍是自动安装候选的写法。
		if depType == model.DepTypePython && !PythonExtrasCovered(existing.Name, name) {
			updates["name"] = MergePythonSpecExtras(existing.Name, name)
		}
		database.DB.Model(&existing).Updates(updates)
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
