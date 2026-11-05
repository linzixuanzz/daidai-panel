package service

import (
	"bytes"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"daidai-panel/database"
	"daidai-panel/model"
)

// 「后台进程」页（#176）的平台无关部分：类型、/proc 文本解析、结束前的判定。
// 真正读 /proc、发信号的在 process_monitor_linux.go，非 Linux 的桩在 process_monitor_other.go。
// 解析函数刻意不带构建标签：Windows 开发机上也能跑它们的单测。

// TaskIDEnvKey 是任务执行时注入进程环境的任务标识（#176）。
// 它进的是真实进程环境（buildBootstrapProcessEnv），/proc/<pid>/environ 读得到、子孙进程继承。
const TaskIDEnvKey = "DAIDAI_TASK_ID"

// 进程状态：随任务运行中 / 已脱离任务 / 不是任务拉起的（只在「显示全部进程」里出现）。
const (
	ProcessStateRunningWithTask = "running_with_task"
	ProcessStateDetached        = "detached"
	ProcessStateOther           = "other"
)

// procClockTicksPerSecond 是 /proc/<pid>/stat 里 starttime 的单位（CLK_TCK）。
// Linux 各架构用户态看到的都是 100，不去调 sysconf，省一个 cgo 依赖。
const procClockTicksPerSecond = 100

// processCmdlineMaxRunes 是列表里命令行的最大字符数，超出截断。
const processCmdlineMaxRunes = 500

var (
	// ErrProcessGone：pid 已经不在，或者 starttime 对不上（PID 被别的进程复用了）。handler 翻成 409。
	ErrProcessGone = errors.New("进程已退出或 PID 已被复用，请刷新")

	// ErrProcessMonitorUnsupported：非 Linux 部署没有 /proc。
	ErrProcessMonitorUnsupported = errors.New("仅 Linux 部署支持查看后台进程")

	// ErrProcessListAllUnavailable：Magisk 模块版不开放「全部进程」——面板以 root 跑在安卓上，会列出整个系统。
	ErrProcessListAllUnavailable = errors.New("Magisk 模块版不支持列出全部进程：面板以 root 运行在安卓系统上，会列出整个手机的进程")
)

// ProcessKillRefusedError 是「这个进程不允许从这里结束」（面板自身、PID 1、面板所在进程组）。handler 翻成 400。
type ProcessKillRefusedError struct {
	Reason string
}

func (e *ProcessKillRefusedError) Error() string {
	return e.Reason
}

// PanelProcess 是「后台进程」页的一行。
type PanelProcess struct {
	PID  int `json:"pid"`
	PGID int `json:"pgid"`

	// TaskID 为 nil 表示不是任务拉起的进程（只会出现在 scope=all 里）
	TaskID *uint `json:"task_id"`

	// TaskName 为空表示任务已删除（或不是任务进程）
	TaskName  string     `json:"task_name"`
	Cmdline   string     `json:"cmdline"`
	StartedAt *time.Time `json:"started_at"`
	RSSBytes  int64      `json:"rss_bytes"`
	State     string     `json:"state"`

	// StartTicks 是 /proc/<pid>/stat 的原始 starttime，结束进程时原样带回来，防止 PID 被复用后误杀
	StartTicks uint64 `json:"start_ticks"`
}

// ProcessMonitorSupported 报告当前平台能不能读 /proc。
func ProcessMonitorSupported() bool {
	return processMonitorSupported
}

// procStatFields 是 /proc/<pid>/stat 里用得到的几个字段。
type procStatFields struct {
	State      byte
	PPID       int
	PGID       int
	StartTicks uint64
	RSSPages   int64
}

// parseProcStat 解析 /proc/<pid>/stat。
// 第 2 个字段是括号包着的命令名，命令名里可以有空格、甚至括号（例如 "(my (app) x)"），
// 所以不能直接按空格切：以最后一个 ')' 为界，后面的部分才按空格切。
// 切出来的第 0 项是第 3 个字段 state，于是第 N 个字段在下标 N-3。
func parseProcStat(content string) (procStatFields, error) {
	var fields procStatFields
	end := strings.LastIndexByte(content, ')')
	if end < 0 {
		return fields, fmt.Errorf("stat 格式不对：找不到命令名的右括号")
	}
	parts := strings.Fields(content[end+1:])
	// 至少要到第 24 个字段 rss（下标 21）
	if len(parts) < 22 {
		return fields, fmt.Errorf("stat 字段不足：只有 %d 个", len(parts))
	}
	if len(parts[0]) > 0 {
		fields.State = parts[0][0]
	}
	var err error
	if fields.PPID, err = strconv.Atoi(parts[1]); err != nil {
		return fields, fmt.Errorf("解析 ppid 失败: %w", err)
	}
	if fields.PGID, err = strconv.Atoi(parts[2]); err != nil {
		return fields, fmt.Errorf("解析 pgrp 失败: %w", err)
	}
	if fields.StartTicks, err = strconv.ParseUint(parts[19], 10, 64); err != nil {
		return fields, fmt.Errorf("解析 starttime 失败: %w", err)
	}
	if fields.RSSPages, err = strconv.ParseInt(parts[21], 10, 64); err != nil {
		return fields, fmt.Errorf("解析 rss 失败: %w", err)
	}
	return fields, nil
}

// procEnvironValue 从 /proc/<pid>/environ（\0 分隔的 KEY=VALUE）里取一个变量。
func procEnvironValue(environ []byte, key string) (string, bool) {
	prefix := []byte(key + "=")
	for _, entry := range bytes.Split(environ, []byte{0}) {
		if bytes.HasPrefix(entry, prefix) {
			return string(entry[len(prefix):]), true
		}
	}
	return "", false
}

// formatProcCmdline 把 /proc/<pid>/cmdline 的 \0 分隔换成空格，超长截断。内核线程的 cmdline 是空的，返回空串。
func formatProcCmdline(raw []byte) string {
	text := strings.TrimSpace(string(bytes.ReplaceAll(raw, []byte{0}, []byte{' '})))
	if utf8.RuneCountInString(text) <= processCmdlineMaxRunes {
		return text
	}
	return string([]rune(text)[:processCmdlineMaxRunes]) + "…"
}

// parseProcBootTime 从 /proc/stat 里取 "btime <Unix 秒>"（系统启动时刻）。
func parseProcBootTime(content string) (int64, bool) {
	for _, line := range strings.Split(content, "\n") {
		if !strings.HasPrefix(line, "btime ") {
			continue
		}
		value, err := strconv.ParseInt(strings.TrimSpace(strings.TrimPrefix(line, "btime ")), 10, 64)
		if err != nil {
			return 0, false
		}
		return value, true
	}
	return 0, false
}

// procStartTime 由启动时刻 btime 与 starttime（开机后的 tick 数）算进程启动时间。
// 拆成整秒 + 余数再加：直接 time.Duration(ticks) * time.Second 在开机两三年后会溢出 int64。
func procStartTime(bootTime int64, startTicks uint64) time.Time {
	seconds := int64(startTicks / procClockTicksPerSecond)
	nanos := int64(startTicks%procClockTicksPerSecond) * int64(time.Second/procClockTicksPerSecond)
	return time.Unix(bootTime+seconds, nanos)
}

// decideProcessKill 判断能不能结束、结束整组还是只结束这一个。
// 拒绝：PID 1、面板自身、和面板同一个进程组的进程（结束它们可能把面板、nginx、容器入口脚本一起带走）。
// 进程是自己进程组的组长（pgid == pid）时结束整组：setsid 拉起的常驻进程就是这种，它的子进程都在组里；
// 否则只结束这一个进程，不去碰它所在的组（那个组可能还属于正在跑的任务或别的程序）。
func decideProcessKill(pid, pgid, selfPID, selfPGID int) (bool, error) {
	if pid <= 1 {
		return false, &ProcessKillRefusedError{Reason: "不能结束 PID 1（容器或系统的初始进程）"}
	}
	if pid == selfPID {
		return false, &ProcessKillRefusedError{Reason: "不能结束面板自身"}
	}
	if pgid == selfPGID {
		return false, &ProcessKillRefusedError{Reason: "该进程与面板在同一个进程组，不能从这里结束"}
	}
	return pgid == pid, nil
}

// registeredProcessGroupsByTask 返回执行器此刻登记着的进程（任务主进程 + 正在跑的前置 / 后置钩子），按任务分组。
// 这些进程启动时都 setpgid 过，pid 就是组号。用来判断一个带任务标识的进程是「随任务运行中」还是「已脱离任务」。
func (e *TaskExecutor) registeredProcessGroupsByTask() map[uint]map[int]bool {
	result := make(map[uint]map[int]bool)
	if e == nil {
		return result
	}
	e.processLock.Lock()
	defer e.processLock.Unlock()
	add := func(taskID uint, pid int) {
		if result[taskID] == nil {
			result[taskID] = make(map[int]bool)
		}
		result[taskID][pid] = true
	}
	for taskID, processes := range e.runningProcesses {
		for pid := range processes {
			add(taskID, pid)
		}
	}
	for taskID, runs := range e.executingRuns {
		for run := range runs {
			for pid := range run.hookProcesses {
				add(taskID, pid)
			}
		}
	}
	return result
}

// finishPanelProcessList 给读出来的进程补上任务名与状态，再排序（任务进程按任务、PID，其余按 PID）。
func finishPanelProcessList(list []PanelProcess) []PanelProcess {
	taskIDs := make([]uint, 0)
	seen := make(map[uint]bool)
	for _, proc := range list {
		if proc.TaskID != nil && !seen[*proc.TaskID] {
			seen[*proc.TaskID] = true
			taskIDs = append(taskIDs, *proc.TaskID)
		}
	}

	// 任务名一次查完；查不到的（任务已删除）留空，前端显示「已删除的任务 #ID」
	taskNames := make(map[uint]string, len(taskIDs))
	if len(taskIDs) > 0 && database.DB != nil {
		var tasks []model.Task
		database.DB.Select("id", "name").Where("id IN ?", taskIDs).Find(&tasks)
		for _, task := range tasks {
			taskNames[task.ID] = task.Name
		}
	}

	groups := GetTaskExecutor().registeredProcessGroupsByTask()
	for i := range list {
		if list[i].TaskID == nil {
			list[i].State = ProcessStateOther
			continue
		}
		taskID := *list[i].TaskID
		list[i].TaskName = taskNames[taskID]
		// 所在进程组正是这个任务当前登记的进程 → 随任务运行中；否则已经脱离任务（setsid 出去的，或任务已结束留下的）
		if groups[taskID][list[i].PGID] {
			list[i].State = ProcessStateRunningWithTask
		} else {
			list[i].State = ProcessStateDetached
		}
	}

	sort.SliceStable(list, func(a, b int) bool {
		left, right := list[a], list[b]
		// 任务进程排前面
		if (left.TaskID == nil) != (right.TaskID == nil) {
			return left.TaskID != nil
		}
		if left.TaskID != nil && *left.TaskID != *right.TaskID {
			return *left.TaskID < *right.TaskID
		}
		return left.PID < right.PID
	})
	return list
}
