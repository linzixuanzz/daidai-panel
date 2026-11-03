package main

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"daidai-panel/appboot"
	"daidai-panel/config"
	"daidai-panel/database"
	"daidai-panel/handler"
	"daidai-panel/model"
	"daidai-panel/pkg/crypto"
	"daidai-panel/pkg/validator"
	"daidai-panel/service"

	"gorm.io/gorm"
)

func run(args []string) int {
	rt := &cliRuntime{}

	if len(args) == 0 {
		printHelp()
		return 0
	}

	var err error
	switch args[0] {
	case "help", "-h", "--help":
		printHelp()
	case "version":
		err = runVersion()
	case "status":
		err = runStatus(rt)
	case "check":
		err = runCheck(rt)
	case "logs":
		err = runLogs(rt, args[1:])
	case "restart":
		err = runRestart(rt)
	case "update":
		err = runUpdate(rt)
	case "service":
		err = runService(rt, args[1:])
	case "python", "py":
		err = runRuntimePython(rt, args[1:])
	case "shell", "sh":
		err = runRuntimeShell(rt, args[1:])
	case "script":
		err = runScript(rt, args[1:])
	case "env":
		err = runEnv(rt, args[1:])
	case "clean-logs":
		err = runCleanLogs(rt, args[1:])
	case "backup":
		err = runBackup(rt, args[1:])
	case "task":
		err = runTask(rt, args[1:])
	case "sub":
		err = runSubscription(rt, args[1:])
	case "reset-login":
		err = runResetLogin(rt, args[1:])
	case "reset-password":
		err = runResetPassword(rt, args[1:])
	case "reset-username":
		err = runResetUsername(rt, args[1:])
	case "list-users":
		err = runListUsers(rt)
	case "disable-2fa":
		err = runDisable2FA(rt, args[1:])
	case "ip-whitelist", "ip-white", "whitelist":
		err = runIPWhitelist(rt, args[1:])
	case "mcp":
		err = runMCP(rt, args[1:])
	default:
		err = fmt.Errorf("未知命令: %s", args[0])
	}

	if err == nil {
		rt.printWarnings()
		return 0
	}

	fmt.Fprintf(os.Stderr, "错误: %s\n", err)
	rt.printWarnings()
	return 1
}

func runVersion() error {
	fmt.Printf("呆呆面板版本: %s\n", handler.Version)
	fmt.Println("命令工具: ddp")
	return nil
}

func runStatus(rt *cliRuntime) error {
	if err := rt.bootstrap(); err != nil {
		return err
	}

	resource := service.GetResourceInfo()
	pid, pidErr := readServerPID(rt.serverPIDFile())
	serverRunning := pidErr == nil && isProcessRunning(pid)
	healthURL := rt.backendBaseURL() + "/api/v1/health"
	backendReachable := checkHTTPReachable(healthURL)

	var taskCount int64
	var runningTasks int64
	var envCount int64
	var subCount int64
	database.DB.Model(&model.Task{}).Count(&taskCount)
	database.DB.Model(&model.Task{}).Where("status = ?", model.TaskStatusRunning).Count(&runningTasks)
	database.DB.Model(&model.EnvVar{}).Count(&envCount)
	database.DB.Model(&model.Subscription{}).Count(&subCount)

	backupList, _ := service.ListBackups()

	fmt.Printf("版本: %s\n", handler.Version)
	fmt.Printf("数据目录: %s\n", resource.DataDir)
	fmt.Printf("数据库: %s\n", rt.cfg.Database.Path)
	fmt.Printf("面板标题: %s\n", model.GetRegisteredConfig("panel_title"))
	if serverRunning {
		fmt.Printf("服务状态: 运行中 (PID %d)\n", pid)
	} else {
		fmt.Println("服务状态: 未检测到运行中的 daidai-server 进程")
	}
	fmt.Printf("后端探活: %s\n", boolLabel(backendReachable, "正常", "不可达"))
	fmt.Printf("访问端口: 前端 %d / 后端 %d\n", rt.panelPort(), rt.backendPort())
	fmt.Printf("任务: %d 个，总运行中 %d 个\n", taskCount, runningTasks)
	fmt.Printf("订阅: %d 个\n", subCount)
	fmt.Printf("环境变量: %d 个\n", envCount)
	fmt.Printf("脚本文件: %d 个\n", service.CountScriptFiles(rt.cfg.Data.ScriptsDir))
	fmt.Printf("备份文件: %d 个\n", len(backupList))
	fmt.Printf("资源占用: CPU %.2f%% / 内存 %.2f%% / 磁盘 %.2f%%\n", resource.CPUUsage, resource.MemoryUsage, resource.DiskUsage)

	return nil
}

func runCheck(rt *cliRuntime) error {
	if err := rt.bootstrap(); err != nil {
		return err
	}

	type checkItem struct {
		label   string
		state   string
		detail  string
		isError bool
	}

	items := make([]checkItem, 0, 12)
	add := func(label, state, detail string, isError bool) {
		items = append(items, checkItem{label: label, state: state, detail: detail, isError: isError})
	}

	configPath := appboot.ResolveConfigPath()
	if _, err := os.Stat(configPath); err == nil {
		add("配置文件", "OK", configPath, false)
	} else {
		add("配置文件", "FAIL", err.Error(), true)
	}

	for _, dir := range []struct {
		label string
		path  string
	}{
		{"数据目录", rt.cfg.Data.Dir},
		{"脚本目录", rt.cfg.Data.ScriptsDir},
		{"日志目录", rt.cfg.Data.LogDir},
		{"备份目录", filepath.Join(rt.cfg.Data.Dir, "backups")},
	} {
		if info, err := os.Stat(dir.path); err == nil && info.IsDir() {
			add(dir.label, "OK", dir.path, false)
		} else if err != nil {
			add(dir.label, "FAIL", err.Error(), true)
		} else {
			add(dir.label, "FAIL", "不是目录", true)
		}
	}

	if _, err := os.Stat(rt.cfg.Database.Path); err == nil {
		add("数据库文件", "OK", rt.cfg.Database.Path, false)
	} else {
		add("数据库文件", "FAIL", err.Error(), true)
	}

	pid, pidErr := readServerPID(rt.serverPIDFile())
	serverRunning := pidErr == nil && isProcessRunning(pid)
	if serverRunning {
		add("服务进程", "OK", fmt.Sprintf("PID %d", pid), false)
	} else if pidErr != nil {
		add("服务进程", "WARN", "未找到 PID 文件，可能尚未启动或不是通过官方入口运行", false)
	} else {
		add("服务进程", "FAIL", fmt.Sprintf("PID %d 不存在", pid), true)
	}

	healthURL := rt.backendBaseURL() + "/api/v1/health"
	if checkHTTPReachable(healthURL) {
		add("后端探活", "OK", healthURL, false)
	} else {
		add("后端探活", "WARN", healthURL+" 不可达", false)
	}

	for _, binary := range []string{"python3", "node", "npm", "git"} {
		if _, err := execLookPath(binary); err == nil {
			add("运行时 "+binary, "OK", "已检测到", false)
		} else {
			add("运行时 "+binary, "FAIL", err.Error(), true)
		}
	}

	plan, planErr := handler.BuildPanelUpdatePlanInfo()
	if planErr == nil && plan.UpdateManager == "watchtower" {
		// 精简镜像由 Watchtower 托管更新，不应因为没有 Docker CLI 或 docker.sock 报出误导性警告。
		if plan.WatchtowerManualTriggerSupported {
			// 只看配置齐不齐会漏掉「配置没问题但 API 根本没在监听」的情况（issue #108：
			// Watchtower 低于 v1.20.0 时会静默忽略 WATCHTOWER_HTTP_API_ENDPOINTS，端口不会打开），
			// 所以这里再连一下确认。短超时、不触发更新，失败也只是这一条转红，不影响其余检查项。
			if reachable, detail := handler.CheckWatchtowerAPIReachable(3 * time.Second); reachable {
				add("更新托管", "OK", "Watchtower HTTP API 已配置且探活通过，ddp update 可立即触发检查", false)
			} else {
				add("更新托管", "FAIL", detail, true)
			}
		} else if plan.WatchtowerPeriodicPollsEnabled || plan.WatchtowerSchedule != "" {
			add("更新托管", "WARN", "Watchtower 定时检查已启用；如需使用 ddp update，请补充 HTTP API 地址和令牌", false)
		} else {
			add("更新托管", "WARN", "已声明使用 Watchtower；如需使用 ddp update，请配置 WATCHTOWER_HTTP_API_URL 与 WATCHTOWER_HTTP_API_TOKEN", false)
		}
	} else if planErr == nil && plan.DeploymentType == "magisk" {
		// 模块版跑在 Android 的 chroot 容器里，本来就不该有 docker.sock，
		// 这里报 Docker 相关警告只会误导手机用户。
		add("更新托管", "OK", "Magisk 模块版，可使用 ddp update 在线升级面板程序与前端", false)
	} else if _, err := os.Stat("/var/run/docker.sock"); err == nil {
		add("Docker Socket", "OK", "已挂载 /var/run/docker.sock，可使用 ddp update", false)
	} else {
		add("Docker Socket", "WARN", "未挂载；Docker 容器场景请使用 Watchtower，二进制部署仍会尝试后台更新", false)
	}

	if planErr == nil {
		if plan.DeploymentType == "magisk" {
			add("更新目标", "OK", fmt.Sprintf("模块版在线升级 %s（只更新面板程序与前端）", plan.AssetName), false)
		} else if plan.DeploymentType == "binary" {
			add("更新目标", "OK", fmt.Sprintf("二进制包 %s -> %s", plan.AssetName, plan.InstallDir), false)
		} else if plan.UpdateManager == "watchtower" {
			target := plan.PullImageName
			if target == "" {
				target = "当前容器镜像"
			}
			add("更新目标", "OK", "Watchtower -> "+target, false)
		} else {
			add("更新目标", "OK", fmt.Sprintf("%s -> %s", plan.ContainerName, plan.PullImageName), false)
		}
	} else {
		add("更新目标", "WARN", planErr.Error(), false)
	}

	failures := 0
	for _, item := range items {
		fmt.Printf("[%s] %s", item.state, item.label)
		if item.detail != "" {
			fmt.Printf(" - %s", item.detail)
		}
		fmt.Println()
		if item.isError {
			failures++
		}
	}

	if failures > 0 {
		return fmt.Errorf("检测到 %d 项异常", failures)
	}
	return nil
}

func runLogs(rt *cliRuntime, args []string) error {
	lines := 100
	keyword := ""
	level := ""

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--lines", "-n":
			if i+1 >= len(args) {
				return fmt.Errorf("--lines 需要参数")
			}
			parsed, err := strconv.Atoi(args[i+1])
			if err != nil || parsed <= 0 {
				return fmt.Errorf("无效的日志行数: %s", args[i+1])
			}
			lines = parsed
			i++
		case "--grep":
			if i+1 >= len(args) {
				return fmt.Errorf("--grep 需要参数")
			}
			keyword = args[i+1]
			i++
		case "--level":
			if i+1 >= len(args) {
				return fmt.Errorf("--level 需要参数")
			}
			level = strings.ToLower(strings.TrimSpace(args[i+1]))
			switch level {
			case "", "debug", "info", "warn", "error":
			default:
				return fmt.Errorf("无效的日志级别: %s", args[i+1])
			}
			i++
		default:
			return fmt.Errorf("未知参数: %s", args[i])
		}
	}

	logPath := rt.panelLogPath()
	linesData, err := readLinesFromFile(logPath, lines, keyword)
	if err != nil {
		return fmt.Errorf("读取面板日志失败: %w", err)
	}

	if len(linesData) == 0 {
		fmt.Println("暂无面板日志")
		return nil
	}

	for _, line := range linesData {
		if level != "" && !service.MatchPanelLogLevel(line, level) {
			continue
		}
		fmt.Println(line)
	}
	return nil
}

func runRestart(rt *cliRuntime) error {
	pid, err := readServerPID(rt.serverPIDFile())
	if err != nil {
		return fmt.Errorf("未找到服务 PID 文件，无法重启: %w", err)
	}
	if !isProcessRunning(pid) {
		return fmt.Errorf("PID %d 当前未运行", pid)
	}

	process, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	if err := process.Signal(syscall.SIGTERM); err != nil {
		return err
	}

	fmt.Printf("已向 daidai-server (PID %d) 发送重启信号\n", pid)
	fmt.Println("入口脚本会自动拉起新进程")
	return nil
}

func runUpdate(rt *cliRuntime) error {
	if err := rt.bootstrap(); err != nil {
		return err
	}

	status, err := handler.ExecutePanelUpdateForCLI()
	if err != nil {
		return err
	}

	fmt.Printf("更新状态: %s / %s\n", status.Status, status.Phase)
	if status.UpdateManager == "watchtower" {
		fmt.Println("更新方式: Watchtower 托管更新")
		if status.PullImageName != "" {
			fmt.Printf("目标镜像: %s\n", status.PullImageName)
		}
	} else if status.DeploymentType == "magisk" {
		fmt.Printf("更新方式: Magisk 模块版在线升级（只更新面板程序与前端，容器与已装依赖不动）\n")
		fmt.Printf("更新包: %s\n", status.AssetName)
	} else if status.DeploymentType == "binary" {
		fmt.Printf("更新方式: 二进制后台更新\n")
		fmt.Printf("更新包: %s\n", status.AssetName)
		fmt.Printf("安装目录: %s\n", status.InstallDir)
		fmt.Printf("目标程序: %s\n", status.BinaryName)
	} else {
		fmt.Printf("当前容器: %s\n", status.ContainerName)
		fmt.Printf("目标镜像: %s\n", status.PullImageName)
	}
	fmt.Printf("说明: %s\n", status.Message)
	return nil
}

func runCleanLogs(rt *cliRuntime, args []string) error {
	if err := rt.bootstrap(); err != nil {
		return err
	}

	days := model.GetRegisteredConfigInt("log_retention_days")
	if len(args) > 1 {
		return fmt.Errorf("用法: ddp clean-logs [days]")
	}
	if len(args) == 1 {
		parsed, err := strconv.Atoi(args[0])
		if err != nil || parsed < 1 {
			return fmt.Errorf("无效的天数: %s", args[0])
		}
		days = parsed
	}

	count := service.CleanOldLogs(rt.cfg.Data.LogDir, days)
	fmt.Printf("已清理 %d 个日志文件，当前保留最近 %d 天\n", count, days)
	return nil
}

func runBackup(rt *cliRuntime, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("用法: ddp backup <create|list|restore|delete> ...")
	}

	switch args[0] {
	case "create":
		return runBackupCreate(rt, args[1:])
	case "list":
		return runBackupList(rt)
	case "restore":
		return runBackupRestore(rt, args[1:])
	case "delete":
		return runBackupDelete(rt, args[1:])
	default:
		return fmt.Errorf("未知 backup 子命令: %s", args[0])
	}
}

func runBackupCreate(rt *cliRuntime, args []string) error {
	if err := rt.bootstrap(); err != nil {
		return err
	}

	options := service.BackupCreateOptions{}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--name":
			if i+1 >= len(args) {
				return fmt.Errorf("--name 需要参数")
			}
			options.Name = args[i+1]
			i++
		case "--password":
			if i+1 >= len(args) {
				return fmt.Errorf("--password 需要参数")
			}
			options.Password = args[i+1]
			i++
		case "--only":
			if i+1 >= len(args) {
				return fmt.Errorf("--only 需要参数")
			}
			selection, err := parseBackupSelection(args[i+1])
			if err != nil {
				return err
			}
			options.Selection = selection
			i++
		default:
			return fmt.Errorf("未知参数: %s", args[i])
		}
	}

	path, err := service.CreateBackup(options)
	if err != nil {
		return err
	}

	fmt.Printf("备份已创建: %s\n", path)
	return nil
}

func runBackupList(rt *cliRuntime) error {
	if err := rt.bootstrap(); err != nil {
		return err
	}

	backups, err := service.ListBackups()
	if err != nil {
		return err
	}
	if len(backups) == 0 {
		fmt.Println("当前没有备份文件")
		return nil
	}

	sort.Slice(backups, func(i, j int) bool {
		left, _ := backups[i]["created_at"].(time.Time)
		right, _ := backups[j]["created_at"].(time.Time)
		return left.After(right)
	})

	for _, item := range backups {
		name, _ := item["name"].(string)
		size, _ := item["size"].(int64)
		createdAt, _ := item["created_at"].(time.Time)
		fmt.Printf("%s\t%s\t%s\n", createdAt.Format("2006-01-02 15:04:05"), formatBytes(size), name)
	}
	return nil
}

func runBackupRestore(rt *cliRuntime, args []string) error {
	if err := rt.bootstrap(); err != nil {
		return err
	}
	if len(args) == 0 {
		return fmt.Errorf("用法: ddp backup restore <filename> [--password xxx]")
	}

	filename := args[0]
	password := ""
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--password":
			if i+1 >= len(args) {
				return fmt.Errorf("--password 需要参数")
			}
			password = args[i+1]
			i++
		default:
			return fmt.Errorf("未知参数: %s", args[i])
		}
	}

	if err := service.RestoreBackup(filename, password); err != nil {
		return err
	}

	fmt.Println("备份恢复完成")

	if pid, err := readServerPID(rt.serverPIDFile()); err == nil && isProcessRunning(pid) {
		process, findErr := os.FindProcess(pid)
		if findErr == nil && process.Signal(syscall.SIGTERM) == nil {
			fmt.Printf("已自动重启 daidai-server (PID %d)，依赖恢复会在新进程启动后继续校验\n", pid)
			return nil
		}
	}

	fmt.Println("未检测到可自动重启的 daidai-server 进程，请手动重启面板")
	return nil
}

func runBackupDelete(rt *cliRuntime, args []string) error {
	if err := rt.bootstrap(); err != nil {
		return err
	}
	if len(args) != 1 {
		return fmt.Errorf("用法: ddp backup delete <filename>")
	}

	if err := service.DeleteBackup(args[0]); err != nil {
		return err
	}
	fmt.Printf("已删除备份: %s\n", args[0])
	return nil
}

func runTask(rt *cliRuntime, args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("用法: ddp task <list|logs|run|stop> ...")
	}

	switch args[0] {
	case "list":
		return runTaskList(rt, args[1:])
	case "logs":
		return runTaskLogs(rt, args[1:])
	case "run":
		return runTaskNow(rt, strings.Join(args[1:], " "))
	case "stop":
		return stopTaskNow(rt, strings.Join(args[1:], " "))
	default:
		return fmt.Errorf("未知 task 子命令: %s", args[0])
	}
}

func runTaskNow(rt *cliRuntime, identifier string) error {
	if err := rt.bootstrap(); err != nil {
		return err
	}

	task, err := findTask(identifier)
	if err != nil {
		return err
	}
	if task.Status == model.TaskStatusRunning {
		return fmt.Errorf("任务当前正在运行中")
	}

	executor := service.NewTaskExecutor()
	scheduler := service.NewSchedulerV2(service.SchedulerConfig{
		WorkerCount:  1,
		QueueSize:    8,
		RateInterval: 50 * time.Millisecond,
	}, executor)
	scheduler.Start()
	defer scheduler.Stop()

	if err := scheduler.RunNow(task.ID); err != nil {
		return fmt.Errorf("任务启动失败: %w", err)
	}

	fmt.Printf("任务已启动: %s (#%d)\n", task.Name, task.ID)
	return waitTaskCompletion(task.ID)
}

func waitTaskCompletion(taskID uint) error {
	lastState := ""
	for {
		var task model.Task
		if err := database.DB.First(&task, taskID).Error; err != nil {
			return err
		}

		state := taskStatusText(task.Status)
		if state != lastState {
			fmt.Printf("当前状态: %s\n", state)
			lastState = state
		}

		var taskLog model.TaskLog
		err := database.DB.Where("task_id = ?", taskID).Order("started_at DESC").First(&taskLog).Error
		if err == nil && taskLog.Status != nil && *taskLog.Status != model.LogStatusRunning &&
			task.Status != model.TaskStatusQueued && task.Status != model.TaskStatusRunning {
			output := latestTaskLogOutput(taskLog)
			if output != "" {
				fmt.Println("------ 任务输出 ------")
				fmt.Println(strings.TrimRight(output, "\n"))
			}

			duration := "-"
			if taskLog.Duration != nil {
				duration = fmt.Sprintf("%.1fs", *taskLog.Duration)
			}

			if *taskLog.Status == model.LogStatusSuccess {
				fmt.Printf("任务执行成功，耗时 %s\n", duration)
				return nil
			}
			if *taskLog.Status == model.LogStatusAborted {
				return fmt.Errorf("任务已终止，耗时 %s", duration)
			}
			return fmt.Errorf("任务执行失败，耗时 %s", duration)
		}

		time.Sleep(500 * time.Millisecond)
	}
}

func stopTaskNow(rt *cliRuntime, identifier string) error {
	if err := rt.bootstrap(); err != nil {
		return err
	}

	task, err := findTask(identifier)
	if err != nil {
		return err
	}

	if task.Status == model.TaskStatusQueued && (task.PID == nil || *task.PID <= 0) {
		return fmt.Errorf("任务当前处于排队中，命令行停止仅支持已启动进程的任务")
	}
	if task.PID == nil || *task.PID <= 0 {
		return fmt.Errorf("任务当前没有可终止的进程 PID")
	}

	// 命令行停止也是用户主动终止，统一进入 Aborted，不再混入失败统计。
	service.MarkManualStop(task.ID)
	service.KillProcessByPid(*task.PID)

	inactiveStatus := service.ResolveTaskInactiveStatus(task)
	abortedRunStatus := model.RunAborted
	if err := database.DB.Model(task).Updates(map[string]interface{}{
		"status":          inactiveStatus,
		"last_run_status": abortedRunStatus,
		"pid":             gorm.Expr("NULL"),
		"log_path":        gorm.Expr("NULL"),
	}).Error; err != nil {
		return err
	}

	var runningLog model.TaskLog
	if err := database.DB.Where("task_id = ? AND status = ?", task.ID, model.LogStatusRunning).
		Order("started_at DESC").First(&runningLog).Error; err == nil {
		now := time.Now()
		abortedStatus := model.LogStatusAborted
		duration := now.Sub(runningLog.StartedAt).Seconds()
		if duration < 0 {
			duration = 0
		}
		_ = database.DB.Model(&runningLog).Updates(map[string]interface{}{
			"status":   &abortedStatus,
			"ended_at": now,
			"duration": duration,
		}).Error
		// CLI 停止也回写本次耗时，任务详情里能立刻看到终止前运行了多久。
		_ = database.DB.Model(task).Update("last_running_time", duration).Error
	}

	fmt.Printf("已停止任务: %s (#%d)\n", task.Name, task.ID)
	return nil
}

func runSubscription(rt *cliRuntime, args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("用法: ddp sub <list|logs|pull> ...")
	}

	switch args[0] {
	case "list":
		return runSubscriptionList(rt, args[1:])
	case "logs":
		return runSubscriptionLogs(rt, args[1:])
	case "pull":
		if len(args) < 2 {
			return fmt.Errorf("用法: ddp sub pull <订阅ID或名称>")
		}
		return pullSubscription(rt, strings.Join(args[1:], " "))
	default:
		return fmt.Errorf("未知 sub 子命令: %s", args[0])
	}
}

func pullSubscription(rt *cliRuntime, identifier string) error {
	if err := rt.bootstrap(); err != nil {
		return err
	}

	sub, err := findSubscription(identifier)
	if err != nil {
		return err
	}

	fmt.Printf("开始拉取订阅: %s (#%d)\n", sub.Name, sub.ID)
	_, err = service.ExecuteSubscriptionPull(sub, func(line string) {
		if strings.TrimSpace(line) != "" {
			fmt.Println(line)
		}
	})
	if err != nil {
		return err
	}

	fmt.Printf("订阅拉取完成: %s (#%d)\n", sub.Name, sub.ID)
	return nil
}

func runResetLogin(rt *cliRuntime, args []string) error {
	if err := rt.bootstrap(); err != nil {
		return err
	}

	all := false
	username := ""
	ip := ""

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--all":
			all = true
		case "--ip":
			if i+1 >= len(args) {
				return fmt.Errorf("--ip 需要参数")
			}
			ip = strings.TrimSpace(args[i+1])
			i++
		default:
			if username != "" {
				return fmt.Errorf("只允许提供一个用户名")
			}
			username = strings.TrimSpace(args[i])
		}
	}

	query := database.DB.Model(&model.LoginAttempt{})
	switch {
	case username != "" && ip != "":
		query = query.Where("username = ? AND ip = ?", username, ip)
	case username != "":
		query = query.Where("username = ?", username)
	case ip != "":
		query = query.Where("ip = ?", ip)
	default:
		// 未提供任何过滤，视为全量清空
		all = true
	}

	if all {
		// GORM 要求显式 WHERE 才允许全表删除
		query = query.Where("1 = 1")
	}

	result := query.Delete(&model.LoginAttempt{})
	fmt.Printf("已清除 %d 条登录失败记录\n", result.RowsAffected)
	return result.Error
}

func runDisable2FA(rt *cliRuntime, args []string) error {
	if err := rt.bootstrap(); err != nil {
		return err
	}

	if len(args) == 0 {
		return fmt.Errorf("用法: ddp disable-2fa <用户名> 或 ddp disable-2fa --all")
	}

	if len(args) == 1 && args[0] == "--all" {
		result := database.DB.Where("1 = 1").Delete(&model.TwoFactorAuth{})
		if result.Error != nil {
			return result.Error
		}
		fmt.Printf("已禁用 %d 条 2FA 记录\n", result.RowsAffected)
		return nil
	}

	if len(args) != 1 {
		return fmt.Errorf("用法: ddp disable-2fa <用户名> 或 ddp disable-2fa --all")
	}

	var user model.User
	if err := database.DB.Where("username = ?", strings.TrimSpace(args[0])).First(&user).Error; err != nil {
		return fmt.Errorf("用户不存在: %s", args[0])
	}

	service.DisableTwoFactor(user.ID)
	fmt.Printf("已禁用用户 %s 的 2FA\n", user.Username)
	return nil
}

// runResetPassword 重置指定用户的密码。
//
// 用法:
//
//	ddp reset-password <用户名> <新密码>
//	ddp reset-password --user <用户名> --password <新密码>
//	未指定用户名时，若系统中只有一个用户会自动选中该用户。
func runResetPassword(rt *cliRuntime, args []string) error {
	if err := rt.bootstrap(); err != nil {
		return err
	}

	var username, newPassword string
	positional := make([]string, 0, 2)
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--user", "-u":
			if i+1 >= len(args) {
				return fmt.Errorf("--user 需要参数")
			}
			username = strings.TrimSpace(args[i+1])
			i++
		case "--password", "-p":
			if i+1 >= len(args) {
				return fmt.Errorf("--password 需要参数")
			}
			newPassword = args[i+1]
			i++
		default:
			positional = append(positional, args[i])
		}
	}

	if username == "" && len(positional) > 0 {
		username = strings.TrimSpace(positional[0])
		positional = positional[1:]
	}
	if newPassword == "" && len(positional) > 0 {
		newPassword = positional[0]
		positional = positional[1:]
	}
	if len(positional) > 0 {
		return fmt.Errorf("多余的参数: %v", positional)
	}

	if newPassword == "" {
		return fmt.Errorf("用法: ddp reset-password <用户名> <新密码>")
	}
	if !validator.ValidateNewPassword(newPassword) {
		return fmt.Errorf("新%s", validator.NewPasswordRuleMessage)
	}

	var user model.User
	if username == "" {
		var users []model.User
		if err := database.DB.Find(&users).Error; err != nil {
			return err
		}
		if len(users) == 0 {
			return fmt.Errorf("数据库中没有用户，请先通过面板初始化")
		}
		if len(users) > 1 {
			names := make([]string, 0, len(users))
			for _, u := range users {
				names = append(names, u.Username)
			}
			return fmt.Errorf("系统中存在多个用户 (%s)，请显式指定用户名", strings.Join(names, ", "))
		}
		user = users[0]
	} else {
		if err := database.DB.Where("username = ?", username).First(&user).Error; err != nil {
			return fmt.Errorf("用户不存在: %s", username)
		}
	}

	hash, err := crypto.HashPassword(newPassword)
	if err != nil {
		return fmt.Errorf("密码哈希失败: %w", err)
	}

	// 与网页改密一致：新密码与清掉该用户的全部通行密钥在同一事务里落库
	removedPasskeys, err := service.SetPasswordAndClearPasskeys(user.ID, hash)
	if err != nil {
		return fmt.Errorf("更新密码失败: %w", err)
	}

	// 同步清除登录失败记录，避免还在锁定中
	_ = database.DB.Where("username = ?", user.Username).Delete(&model.LoginAttempt{}).Error

	// 与网页改密一致：撤销该用户的全部登录会话（含续期出的令牌）。命令行是独立进程，
	// 撤销写在库里，运行中的面板下一个请求就能看到，不用重启。
	revoked := service.RevokeAllUserSessions(user.ID)

	fmt.Printf("已重置用户 %s 的密码，已撤销 %d 个登录会话\n", user.Username, revoked)
	// 确实清掉了通行密钥才多打一行，没有时输出与原来一致
	if removedPasskeys > 0 {
		fmt.Printf("已清除该用户添加的 %d 个通行密钥\n", removedPasskeys)
	}
	return nil
}

// runResetUsername 重命名指定用户。
//
// 用法:
//
//	ddp reset-username <旧用户名> <新用户名>
//	ddp reset-username --new <新用户名>   (系统仅有一个用户时)
func runResetUsername(rt *cliRuntime, args []string) error {
	if err := rt.bootstrap(); err != nil {
		return err
	}

	var oldName, newName string
	positional := make([]string, 0, 2)
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--old":
			if i+1 >= len(args) {
				return fmt.Errorf("--old 需要参数")
			}
			oldName = strings.TrimSpace(args[i+1])
			i++
		case "--new":
			if i+1 >= len(args) {
				return fmt.Errorf("--new 需要参数")
			}
			newName = strings.TrimSpace(args[i+1])
			i++
		default:
			positional = append(positional, args[i])
		}
	}

	if oldName == "" && len(positional) > 0 {
		oldName = strings.TrimSpace(positional[0])
		positional = positional[1:]
	}
	if newName == "" && len(positional) > 0 {
		newName = strings.TrimSpace(positional[0])
		positional = positional[1:]
	}
	if len(positional) > 0 {
		return fmt.Errorf("多余的参数: %v", positional)
	}

	if newName == "" {
		return fmt.Errorf("用法: ddp reset-username <旧用户名> <新用户名>")
	}
	if !validator.ValidateUsername(newName) {
		return fmt.Errorf("新用户名不合法：需 3-32 位字母/数字/下划线")
	}

	var user model.User
	if oldName == "" {
		var users []model.User
		if err := database.DB.Find(&users).Error; err != nil {
			return err
		}
		if len(users) == 0 {
			return fmt.Errorf("数据库中没有用户")
		}
		if len(users) > 1 {
			names := make([]string, 0, len(users))
			for _, u := range users {
				names = append(names, u.Username)
			}
			return fmt.Errorf("系统中存在多个用户 (%s)，请显式指定旧用户名", strings.Join(names, ", "))
		}
		user = users[0]
	} else {
		if err := database.DB.Where("username = ?", oldName).First(&user).Error; err != nil {
			return fmt.Errorf("用户不存在: %s", oldName)
		}
	}

	if user.Username == newName {
		fmt.Printf("用户名未变更（仍为 %s）\n", newName)
		return nil
	}

	var existing model.User
	if err := database.DB.Where("username = ?", newName).First(&existing).Error; err == nil {
		return fmt.Errorf("新用户名 %s 已被占用", newName)
	}

	oldUsername := user.Username
	if err := database.DB.Model(&user).Update("username", newName).Error; err != nil {
		return fmt.Errorf("更新用户名失败: %w", err)
	}

	// 同步清除历史登录失败记录
	_ = database.DB.Where("username = ?", oldUsername).Delete(&model.LoginAttempt{}).Error

	// 与网页改用户名一致：撤销该用户的全部登录会话，旧用户名签出的令牌一律作废
	revoked := service.RevokeAllUserSessions(user.ID)

	fmt.Printf("已将用户 %s 重命名为 %s，已撤销 %d 个登录会话\n", oldUsername, newName, revoked)
	return nil
}

// runListUsers 列出所有用户，方便忘记用户名时查询。
func runListUsers(rt *cliRuntime) error {
	if err := rt.bootstrap(); err != nil {
		return err
	}

	var users []model.User
	if err := database.DB.Order("id ASC").Find(&users).Error; err != nil {
		return err
	}
	if len(users) == 0 {
		fmt.Println("数据库中没有用户")
		return nil
	}

	fmt.Printf("%-4s  %-24s  %-10s  %s\n", "ID", "用户名", "角色", "最后登录")
	for _, u := range users {
		last := "-"
		if u.LastLoginAt != nil {
			last = u.LastLoginAt.Format("2006-01-02 15:04:05")
		}
		fmt.Printf("%-4d  %-24s  %-10s  %s\n", u.ID, u.Username, u.Role, last)
	}
	return nil
}

func checkHTTPReachable(url string) bool {
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode >= 200 && resp.StatusCode < 300
}

func boolLabel(value bool, yes, no string) string {
	if value {
		return yes
	}
	return no
}

func execLookPath(file string) (string, error) {
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if dir == "" {
			continue
		}
		candidate := filepath.Join(dir, file)
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("%s 不在 PATH 中", file)
}

func parseBackupSelection(raw string) (service.BackupSelection, error) {
	var selection service.BackupSelection
	parts := strings.FieldsFunc(strings.ToLower(raw), func(r rune) bool {
		return r == ',' || r == ' ' || r == ';'
	})
	for _, part := range parts {
		part = strings.TrimSpace(part)
		switch part {
		case "":
		case "configs", "config":
			selection.Configs = true
		case "tasks", "task":
			selection.Tasks = true
		case "subscriptions", "subscription", "subs", "sub":
			selection.Subscriptions = true
		case "envs", "env", "env_vars":
			selection.EnvVars = true
		case "logs", "log":
			selection.Logs = true
		case "scripts", "script":
			selection.Scripts = true
		case "dependencies", "dependency", "deps", "dep":
			selection.Dependencies = true
		default:
			return service.BackupSelection{}, fmt.Errorf("未知备份项: %s", part)
		}
	}

	if !selection.Any() {
		return service.BackupSelection{}, fmt.Errorf("--only 至少需要一个备份项")
	}
	return selection, nil
}

func findTask(identifier string) (*model.Task, error) {
	identifier = strings.TrimSpace(identifier)
	if identifier == "" {
		return nil, fmt.Errorf("任务标识不能为空")
	}

	if id, err := strconv.ParseUint(identifier, 10, 32); err == nil {
		var task model.Task
		if err := database.DB.First(&task, id).Error; err != nil {
			return nil, fmt.Errorf("任务不存在: %s", identifier)
		}
		return &task, nil
	}

	var tasks []model.Task
	if err := database.DB.Where("name = ?", identifier).Order("id ASC").Find(&tasks).Error; err != nil {
		return nil, err
	}
	switch len(tasks) {
	case 0:
		return nil, fmt.Errorf("任务不存在: %s", identifier)
	case 1:
		return &tasks[0], nil
	default:
		return nil, fmt.Errorf("存在多个同名任务，请改用任务 ID")
	}
}

func findSubscription(identifier string) (*model.Subscription, error) {
	identifier = strings.TrimSpace(identifier)
	if identifier == "" {
		return nil, fmt.Errorf("订阅标识不能为空")
	}

	if id, err := strconv.ParseUint(identifier, 10, 32); err == nil {
		var sub model.Subscription
		if err := database.DB.First(&sub, id).Error; err != nil {
			return nil, fmt.Errorf("订阅不存在: %s", identifier)
		}
		return &sub, nil
	}

	var subs []model.Subscription
	if err := database.DB.Where("name = ?", identifier).Order("id ASC").Find(&subs).Error; err != nil {
		return nil, err
	}
	switch len(subs) {
	case 0:
		return nil, fmt.Errorf("订阅不存在: %s", identifier)
	case 1:
		return &subs[0], nil
	default:
		return nil, fmt.Errorf("存在多个同名订阅，请改用订阅 ID")
	}
}

func latestTaskLogOutput(taskLog model.TaskLog) string {
	if taskLog.Content != "" {
		if content, err := service.DecompressFromBase64(taskLog.Content); err == nil {
			return truncateText(content, 20000)
		}
	}
	if taskLog.LogPath != nil && strings.TrimSpace(*taskLog.LogPath) != "" && config.C != nil {
		if content, err := service.ReadLogFile(*taskLog.LogPath, config.C.Data.LogDir); err == nil {
			return truncateText(content, 20000)
		}
	}
	return ""
}

func taskStatusText(status float64) string {
	switch status {
	case model.TaskStatusDisabled:
		return "禁用中"
	case model.TaskStatusQueued:
		return "排队中"
	case model.TaskStatusRunning:
		return "运行中"
	default:
		return "空闲中"
	}
}
