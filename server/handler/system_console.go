package handler

import (
	"bytes"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	"daidai-panel/config"
	"daidai-panel/middleware"
	"daidai-panel/model"
	"daidai-panel/pkg/response"
	"daidai-panel/service"

	"github.com/gin-gonic/gin"
)

// 网页版系统命令行。
//
// 执行链路复用脚本调试那一套（startTrackedCommand / waitTrackedCommand / debugRun），
// 但刻意不复用 /scripts/run-code 那个入口，原因有三：
//  1. run-code 没有超时，命令挂死只能人工点停止；
//  2. /scripts 组是 operator + OpenAPIAccess("scripts")，等于 operator 和应用令牌就能跑任意 bash；
//  3. run-code 没有任何审计留痕。
//
// 这里把这三条补齐：admin-only、单条命令有超时上限、每次启动都写面板日志。

const (
	// defaultConsoleTimeout 是 console_timeout_minutes 读不出来时的兜底值，与注册默认值保持一致。
	defaultConsoleTimeout = 30 * time.Minute
	// maxConsoleCommandBytes 是单条命令的字节上限。按字节而不是按字符算：
	// 中文命令的字符数远小于字节数，字节长度才和最终写进临时脚本的体积对得上。
	maxConsoleCommandBytes = 8192
	// maxConsoleRuns 是内存里保留的运行记录条数上限。命令输出全在内存里，
	// 长期运行的面板不设上限会一直堆到 OOM。
	maxConsoleRuns = 50
	// consoleCommandLogLimit 是写进面板日志的命令长度上限（按字符）。
	consoleCommandLogLimit = 200
	// consoleWaitDelay 是「命令本体已退出，但输出管道还被别人握着」时额外等待的时长。
	//
	// 管理员敲 `nohup python3 bot.py &` 时，bash 毫秒级就以 0 退出，但孙进程继承了
	// stdout/stderr 那对 OS 管道，于是 cmd.Wait() 会一直等到孙进程结束 ——
	// 前端永远「运行中」、注册表槽位永远不释放、30 分钟后还会把用户特意 nohup 起的进程杀掉。
	// （反证：`nohup sleep 6 >/dev/null 2>&1 &` 因为重定向掉了管道，Wait 立刻返回。）
	//
	// WaitDelay 的语义是「进程本体退出后最多再等这么久收管道，到点强制关掉管道并让 Wait
	// 返回 exec.ErrWaitDelay」。刻意只给命令行这条路设，不动共享的 startTrackedCommand：
	// script_debug.go / script_run_code.go 也在用它，那两条路没有这个诉求。
	consoleWaitDelay = 2 * time.Second
)

// consoleBashUnavailableReason 是 bash 不在 PATH 时的统一说明。
// meta 用它禁用前端输入框，run 用它挡住请求，两处文案必须一致。
const consoleBashUnavailableReason = "当前系统的 PATH 中找不到 bash，网页版命令行不可用。" +
	"面板的 Docker 镜像与 Magisk 模块版都自带 bash；" +
	"如果你是在 Windows 上直接跑面板二进制，请改用 Docker 部署，或自行安装 bash 后重启面板"

type ConsoleHandler struct {
	mu   sync.Mutex
	runs map[string]*debugRun
	// order 记录 runs 的插入顺序，淘汰时从最老的一头开始找。
	// 不复用 ScriptHandler 的注册表：那边没有条数上限，语义也不同（脚本调试 vs 系统命令）。
	order []string
}

func NewConsoleHandler() *ConsoleHandler {
	return &ConsoleHandler{runs: make(map[string]*debugRun)}
}

// resolveConsoleTimeout 读取用户配置的命令行超时。
// 配置项注册在 model 层并带 1-720 分钟的区间校验，这里只对「数据库里存着历史越界值」
// 这一种情况再兜一次底，避免非法值把超时变成 0（等于命令一起步就被杀）。
func resolveConsoleTimeout() time.Duration {
	minutes := model.GetRegisteredConfigInt("console_timeout_minutes")
	if minutes < 1 || minutes > 720 {
		return defaultConsoleTimeout
	}
	return time.Duration(minutes) * time.Minute
}

// consoleIsRoot 判断面板进程是不是 root。
// Windows 没有 euid 概念（os.Geteuid() 恒为 -1），一律按非 root 报，避免前端显示成 root。
func consoleIsRoot() bool {
	if runtime.GOOS == "windows" {
		return false
	}
	return os.Geteuid() == 0
}

// consoleOperator 取当前登录用户名，用于审计留痕。取法与本目录其它 handler 一致。
func consoleOperator(c *gin.Context) string {
	if value, exists := c.Get("username"); exists {
		if username, ok := value.(string); ok && strings.TrimSpace(username) != "" {
			return username
		}
	}
	// 正常走不到：路由挂了 JWTAuth，没有用户名就进不来。
	return "unknown"
}

// truncateConsoleCommandForLog 把命令压成一行、必要时截断，供面板日志使用。
// 换行用 ⏎ 占位而不是直接删掉，否则多行命令拼成一行后看不出原来的结构。
func truncateConsoleCommandForLog(command string) string {
	single := strings.ReplaceAll(command, "\r\n", "\n")
	single = strings.ReplaceAll(single, "\r", "\n")
	single = strings.ReplaceAll(single, "\n", " ⏎ ")

	// 按 rune 截断，避免把一个中文字符切成半截字节。
	runes := []rune(single)
	if len(runes) <= consoleCommandLogLimit {
		return single
	}
	return string(runes[:consoleCommandLogLimit]) + fmt.Sprintf("...[已截断，原命令共 %d 字符]", len(runes))
}

// stopConsoleRun 是 debugRun.stop() 的命令行版本，返回是否真的停掉了一条在跑的命令。
// 不直接复用 stop() 是因为它追加的收尾日志是「[调试运行已停止]」——那是脚本调试的措辞，
// 出现在系统命令行里会让用户以为自己在调试脚本。除文案外行为完全一致。
func stopConsoleRun(run *debugRun) bool {
	run.mu.Lock()
	defer run.mu.Unlock()

	if run.Process == nil || run.Done {
		return false
	}

	service.KillProcessGroup(run.Process)
	run.Status = "stopped"
	exitCode := -1
	run.ExitCode = &exitCode
	run.Done = true
	// 顺手写上结束时刻，保持 debugRun「Done 为真 ⇔ finishedAt 非零」这条不变式（#159 修复 C）；
	// 命令行自己的注册表不读这个字段，行为零变化
	run.finishedAt = time.Now()
	run.Logs = append(run.Logs, "[命令已停止]")
	return true
}

// consoleRunOutcome 是一次命令行运行的结算结果。
type consoleRunOutcome struct {
	// ExitCode 是最终写进运行记录的退出码。
	ExitCode int
	// WaitErr 是最终写进运行记录的错误（ErrWaitDelay 不算错误，会被抹平成 nil）。
	WaitErr error
	// TimedOut 表示这次运行是被超时终止的。
	TimedOut bool
	// BackgroundHeld 表示命令本体已经退出，只是还有后台进程握着输出管道没放。
	BackgroundHeld bool
}

// resolveConsoleRunOutcome 把 cmd.Wait() 的返回值结算成最终状态。
//
// 抽成纯函数是为了能单测：WaitDelay 只在 Linux 上有真实意义，Windows 开发机上
// bash 多半解析到 WSL，转发器不会把 Windows 的管道句柄传给 Linux 侧进程，复现不出来。
//
// processExitCode 传 cmd.ProcessState.ExitCode()，即进程本体的真实退出码；
// killIssued 表示超时分支里是否真的下过 kill。
func resolveConsoleRunOutcome(waitErr error, processExitCode int, timedOut, killIssued bool) consoleRunOutcome {
	outcome := consoleRunOutcome{TimedOut: timedOut}

	if errors.Is(waitErr, exec.ErrWaitDelay) {
		// 命令本体已经正常退出，只是 WaitDelay 到点强制关了管道。这不是命令失败，
		// 真实退出码得从 ProcessState 上取 —— 否则 resolveExitCode 会把它当成 1 误报 failed。
		outcome.ExitCode = processExitCode
		outcome.WaitErr = nil
		outcome.BackgroundHeld = true
	} else {
		outcome.ExitCode = resolveExitCode(waitErr)
		outcome.WaitErr = waitErr
	}

	// 超时结算不能只看 bash 的退出码。`nohup sleep 20 &` 这类命令里 bash 本体早就 0 退出了，
	// 拿这个 0 去抹掉超时标记，会把「被整组杀掉的运行」记成 status=success、退出码 0、耗时 1800 秒。
	//
	// ⚠️ !killIssued 在当前接线下恒不成立：Run 里 timedOut=true 与 killIssued=run.killIfRunning()
	// 写在同一条分支里，而那一刻 run.Done 必然还是 false（只有 finish / stop / stopConsoleRun
	// 会置位，进程自然退出不会），killIfRunning 必然真的下刀并返回 true。
	// 也就是说这条豁免只覆盖「Wait 已经返回、却什么都没杀」这种亚毫秒抢跑窗口——
	// 它现在是一条防御性契约（将来有人把 kill 和超时标记拆开时才会真的生效），不是活路径。
	// 保留它的取舍：漏报一次超时，远比把被整组杀掉的运行写成 success 危害小。
	if outcome.TimedOut && !killIssued && outcome.ExitCode == 0 {
		outcome.TimedOut = false
	}
	// 超时终止一律记成失败：finish 是按退出码判定状态的，
	// 留着 0 会出现「日志写着超时被终止、状态却是 success」的自相矛盾。
	if outcome.TimedOut && outcome.ExitCode == 0 {
		outcome.ExitCode = -1
	}

	return outcome
}

// pruneConsoleRunsLocked 把注册表压回上限。调用方必须已持有 h.mu。
// 只淘汰「已经结束」的记录：还在跑的记录一旦被删，用户正盯着的输出就断了，
// 进程也会失去停止入口。全都在跑时宁可暂时超限，也不动它们。
func (h *ConsoleHandler) pruneConsoleRunsLocked() {
	for len(h.runs) >= maxConsoleRuns {
		evicted := false
		for i, runID := range h.order {
			run, exists := h.runs[runID]
			if !exists {
				// order 里的残留项（记录已被 DELETE 清掉），顺手摘掉。
				h.order = append(h.order[:i], h.order[i+1:]...)
				evicted = true
				break
			}
			if !run.isDone() {
				continue
			}
			delete(h.runs, runID)
			h.order = append(h.order[:i], h.order[i+1:]...)
			evicted = true
			break
		}
		if !evicted {
			// 一条都淘汰不掉（全在跑），不再死循环。
			return
		}
	}
}

func (h *ConsoleHandler) storeConsoleRun(run *debugRun) string {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.pruneConsoleRunsLocked()

	// 随机 run_id（见 newDebugRunID）：以前是「毫秒时间戳 + 自增序号」，可以按时间猜出来。
	runID := newDebugRunID("console")
	h.runs[runID] = run
	h.order = append(h.order, runID)
	return runID
}

func (h *ConsoleHandler) loadConsoleRun(runID string) (*debugRun, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	run, exists := h.runs[runID]
	return run, exists
}

func (h *ConsoleHandler) deleteConsoleRun(runID string) (*debugRun, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()

	run, exists := h.runs[runID]
	if !exists {
		return nil, false
	}
	delete(h.runs, runID)
	for i, id := range h.order {
		if id == runID {
			h.order = append(h.order[:i], h.order[i+1:]...)
			break
		}
	}
	return run, true
}

// Meta 返回命令行的环境元信息，供前端渲染表头与前置提示。
func (h *ConsoleHandler) Meta(c *gin.Context) {
	available := true
	unavailableReason := ""
	if _, err := exec.LookPath("bash"); err != nil {
		available = false
		unavailableReason = consoleBashUnavailableReason
	}

	packageManager := ""
	if manager, err := detectLinuxPackageManager(); err == nil {
		packageManager = manager.Name
	}

	response.Success(c, gin.H{
		"available":          available,
		"unavailable_reason": unavailableReason,
		"shell":              "bash",
		"work_dir":           consoleWorkDir(),
		"is_root":            consoleIsRoot(),
		"package_manager":    packageManager,
		"distribution":       detectLinuxDistribution(),
		"timeout_minutes":    int(resolveConsoleTimeout() / time.Minute),
	})
}

// consoleWorkDir 是命令的执行目录：固定用面板的脚本目录，
// 和用户在脚本管理里看到的路径一致，`ls` 出来的东西才对得上。
func consoleWorkDir() string {
	return config.C.Data.ScriptsDir
}

func (h *ConsoleHandler) Run(c *gin.Context) {
	var req struct {
		Command string `json:"command"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "请求参数错误")
		return
	}

	command := strings.TrimSpace(req.Command)
	if command == "" {
		response.BadRequest(c, "命令不能为空")
		return
	}
	if len(command) > maxConsoleCommandBytes {
		response.BadRequest(c, fmt.Sprintf(
			"命令过长（%d 字节，上限 %d 字节），请把长脚本保存到脚本管理里再执行",
			len(command), maxConsoleCommandBytes))
		return
	}
	if _, err := exec.LookPath("bash"); err != nil {
		response.BadRequest(c, consoleBashUnavailableReason)
		return
	}

	workDir := consoleWorkDir()
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		response.InternalError(c, "命令执行目录不可用")
		return
	}

	run := newDebugRun()
	runID := h.storeConsoleRun(run)

	// 前端终端里粘进来的命令常带 CRLF，直接交给 bash 会报 $'\r': command not found。
	content := service.NormalizeShellLineEndings([]byte(command + "\n"))

	// ⚠️ 命令走 stdin 喂给 `bash -s`，**刻意不落临时脚本文件**。
	// 落文件那条路要把一个宿主机路径当参数传给 bash，而 bash 未必与面板看到同一套文件系统：
	// Windows 上 `bash` 常常解析到 WSL 的 /bin/bash，它看不到 `C:/Users/...`（要 /mnt/c/...），
	// 解析到 Git Bash 时反斜杠又会被当成转义吃掉 —— 实测两种都报 No such file or directory。
	// 走 stdin 则完全不涉及路径转换，Linux / Alpine / Magisk / Windows 行为一致，
	// 顺带省掉临时文件的创建、清理与并发命名。
	// 副作用是命令自身的 stdin 被脚本占用：交互式命令（未加 -y 的 apt 之类）会读到 EOF 直接退出，
	// 而不是挂在那里等输入 —— 对一个非交互终端来说这正是想要的行为。
	cmd := exec.Command("bash", "-s")
	cmd.Stdin = bytes.NewReader(content)
	cmd.Dir = workDir
	// 刻意不走 buildScriptExecEnv / CreateManagedCommand：那条路会往子进程里注入一枚
	// operator 脚本凭据（给脚本回调面板 API 用）。命令行没有这个需求，
	// 不该把一枚长期有效的凭据摊在用户随手敲命令的终端环境里。
	// 这里只在系统环境上叠加面板配置的代理变量，与依赖安装保持一致。
	cmd.Env = service.AppendProxyEnv(os.Environ())
	// 超时要杀的是整个进程组：命令里常见管道、后台任务、nohup，只杀父进程杀不干净。
	service.SetPgid(cmd)
	// 必须在 Start 之前设置，否则不生效。语义见 consoleWaitDelay 的注释。
	cmd.WaitDelay = consoleWaitDelay

	pipeWriter, scanDone, err := startTrackedCommand(cmd, run)
	if err != nil {
		h.deleteConsoleRun(runID)
		response.InternalError(c, fmt.Sprintf("启动失败: %s", err))
		return
	}

	// 阈值只在启动时读一次并存下来，保证下面日志里写的数字就是本次实际生效的数字；
	// 中途用户改配置不影响已经跑起来的命令。
	timeout := resolveConsoleTimeout()

	// 审计留痕：谁、什么时候、执行了什么命令。刻意只写面板日志，不写 task_logs
	// ——那张表只装真正执行过的定时任务记录，往里塞会顶掉「最近一次日志」并污染耗时统计。
	operator := consoleOperator(c)
	log.Printf("[系统命令行] 用户 %s 执行命令（run_id=%s, 超时=%d 分钟）: %s",
		operator, runID, int(timeout/time.Minute), truncateConsoleCommandForLog(command))
	// 安全审计（设置页可查）：命令已经起来了才记，只留前 200 字符
	recordAudit(c, service.AuditConsoleRun, truncateAuditCommand(command))

	startTime := time.Now()

	go func() {
		waitCh := make(chan error, 1)
		go func() {
			waitCh <- waitTrackedCommand(cmd, pipeWriter, scanDone)
		}()

		timer := time.NewTimer(timeout)
		defer timer.Stop()

		var (
			waitErr    error
			timedOut   bool
			killIssued bool
		)
		select {
		case waitErr = <-waitCh:
		case <-timer.C:
			// 与「进程恰好在这一瞬间正常退出」抢跑：select 在两个 case 同时就绪时是随机挑的，
			// 所以定时器这一路必须再确认一次进程是不是已经自己退了，已经退了就按正常结束算。
			select {
			case waitErr = <-waitCh:
			default:
				timedOut = true
				killIssued = run.killIfRunning()
				waitErr = <-waitCh
			}
		}

		elapsed := time.Since(startTime).Seconds()
		processExitCode := -1
		if cmd.ProcessState != nil {
			processExitCode = cmd.ProcessState.ExitCode()
		}
		outcome := resolveConsoleRunOutcome(waitErr, processExitCode, timedOut, killIssued)

		// 手动停止已经把状态写成 stopped 并追加过收尾日志，这里不要再覆盖。
		if run.isStopped() {
			return
		}

		if outcome.BackgroundHeld {
			// 必须让用户知道：命令本身结束了，但被它拉起来的后台进程还活着，
			// 那些进程之后的输出面板收不到了。顺带给出正确姿势——WaitDelay 到点会关掉管道，
			// 后台进程下次往 stdout 写就会吃到 EPIPE，重定向掉才能真正长期驻留。
			// 推荐 setsid（#159）：脱离进程组才不会被「任务结束后清理残留进程」或停止 / 超时时的整组终止带走。
			run.appendLog("[命令已结束；仍有后台进程持有输出管道，后续输出不再收集。" +
				"若要让它长期驻留，请自行重定向输出并脱离进程组，例如 setsid nohup ... >/dev/null 2>&1 &]")
		}
		if outcome.TimedOut {
			run.appendLog(fmt.Sprintf("[命令执行超时：已超过 %d 分钟上限，整个进程组已被终止]", int(timeout/time.Minute)))
			// 审计留痕：面板替用户杀掉了一整个进程组，得留下是谁的哪一次运行。
			log.Printf("[系统命令行] 用户 %s 的命令执行超时被终止（run_id=%s, 超时=%d 分钟）",
				operator, runID, int(timeout/time.Minute))
		}
		// resolveConsoleRunOutcome 已经保证超时一定带着非 0 退出码进来，
		// finish 会把状态记成 failed，不会出现「日志写着超时、状态却是 success」。
		run.finish(outcome.ExitCode, outcome.WaitErr, elapsed)
	}()

	response.Created(c, gin.H{"message": "命令已启动", "run_id": runID})
}

// Logs 与 ScriptHandler.DebugLogs 同形，前端可以直接复用同一套轮询逻辑，
// 只多一个 discarded 字段。
//
// discarded 是 logs[0] 的全局行号（已被成块丢弃的行数，见 trimLogsLocked）。
// 命令行前端是【增量追加】渲染的：不给这个锚点，它只能拿数组长度猜下标，
// 而「两次轮询之间既发生了截断、又新增了更多行」时 logs.length 反而会比已渲染行数大，
// 切片下标在整体左移过的新数组里指向的是完全不同的全局行 —— 实测每命中一次
// 就静默吞掉近 5 万行，稳态下还会表现成「输出卡死不动」。
// 脚本调试页走的是全量替换，不需要这个字段，所以 DebugLogs 保持原样。
func (h *ConsoleHandler) Logs(c *gin.Context) {
	runID := c.Param("run_id")

	run, exists := h.loadConsoleRun(runID)
	if !exists {
		response.NotFound(c, "运行记录不存在")
		return
	}

	logs, discarded, done, exitCode, status := run.snapshotWithOffset()
	response.Success(c, gin.H{
		"data": gin.H{
			"logs":      logs,
			"discarded": discarded,
			"done":      done,
			"exit_code": exitCode,
			"status":    status,
		},
	})
}

func (h *ConsoleHandler) Stop(c *gin.Context) {
	runID := c.Param("run_id")

	run, exists := h.loadConsoleRun(runID)
	if !exists {
		response.NotFound(c, "运行记录不存在")
		return
	}

	// 审计留痕：只在真的停掉了一条在跑的命令时记一条，重复点停止不刷屏。
	if stopConsoleRun(run) {
		log.Printf("[系统命令行] 用户 %s 手动停止命令（run_id=%s）", consoleOperator(c), runID)
	}
	response.Success(c, gin.H{"message": "已停止"})
}

// Clear 清除运行记录。记录里的进程如果还在跑，一并杀掉，
// 否则记录没了、进程还占着资源，用户再也找不到入口停它。
func (h *ConsoleHandler) Clear(c *gin.Context) {
	runID := c.Param("run_id")

	run, exists := h.deleteConsoleRun(runID)
	if exists {
		run.killIfRunning()
	}

	response.Success(c, gin.H{"message": "已清除"})
}

func (h *ConsoleHandler) RegisterRoutes(r *gin.RouterGroup) {
	// 网页版系统命令行：等价于在宿主机 / 容器里开一个 shell。
	// 刻意不挂 OpenAPIAccess —— 这是「跑任意系统命令」的能力，只允许登录管理员本人使用，
	// 应用令牌一律进不来（RequireAdmin 也会因为应用令牌的角色是 app:* 而直接拒掉）。
	console := r.Group("/system/console", middleware.JWTAuth(), middleware.RequireAdmin())
	{
		console.GET("/meta", h.Meta)
		console.POST("/run", h.Run)
		console.GET("/run/:run_id/logs", h.Logs)
		console.PUT("/run/:run_id/stop", h.Stop)
		console.DELETE("/run/:run_id", h.Clear)
	}
}
