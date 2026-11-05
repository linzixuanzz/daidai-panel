//go:build linux

package service

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const processMonitorSupported = true

// ListPanelProcesses 遍历 /proc 列出进程（#176）。
// includeAll=false：只列 environ 里带 DAIDAI_TASK_ID 的进程；
// includeAll=true：列出全部进程，排除内核线程（cmdline 为空）、面板自身与 PID 1。Magisk 模块版不开放。
// 读不到的 /proc/<pid>（进程刚退出、权限不够）直接跳过。
func ListPanelProcesses(includeAll bool) ([]PanelProcess, error) {
	if includeAll && IsMagiskModuleRuntime() {
		return nil, ErrProcessListAllUnavailable
	}
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, fmt.Errorf("读取 /proc 失败: %w", err)
	}

	bootTime := readProcBootTime()
	selfPID := os.Getpid()
	list := make([]PanelProcess, 0)
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid <= 1 || pid == selfPID {
			continue
		}
		proc, ok := readPanelProcess(pid, bootTime, !includeAll)
		if !ok {
			continue
		}
		// 内核线程没有 cmdline
		if proc.Cmdline == "" && proc.TaskID == nil {
			continue
		}
		list = append(list, proc)
	}
	return finishPanelProcessList(list), nil
}

// readProcBootTime 读系统启动时刻；读不到返回 0，调用方据此不填启动时间。
func readProcBootTime() int64 {
	raw, err := os.ReadFile("/proc/stat")
	if err != nil {
		return 0
	}
	bootTime, ok := parseProcBootTime(string(raw))
	if !ok {
		return 0
	}
	return bootTime
}

// readPanelProcess 读一个进程的 environ / stat / cmdline。requireTask=true 时不带任务标识的进程直接返回 false（先读 environ，省掉后面两次读）。
// 僵尸进程（state Z）也返回 false：它已经死了，只是还没被父进程回收，结束不了，列出来只会让人困惑。
func readPanelProcess(pid int, bootTime int64, requireTask bool) (PanelProcess, bool) {
	dir := "/proc/" + strconv.Itoa(pid)
	proc := PanelProcess{PID: pid}

	// environ 只有同一用户或 root 才读得到；读不到就当它不是任务进程
	if environ, err := os.ReadFile(dir + "/environ"); err == nil {
		if raw, ok := procEnvironValue(environ, TaskIDEnvKey); ok {
			if id, err := strconv.ParseUint(strings.TrimSpace(raw), 10, 64); err == nil && id > 0 {
				taskID := uint(id)
				proc.TaskID = &taskID
			}
		}
	}
	if requireTask && proc.TaskID == nil {
		return proc, false
	}

	statRaw, err := os.ReadFile(dir + "/stat")
	if err != nil {
		return proc, false
	}
	stat, err := parseProcStat(string(statRaw))
	if err != nil || stat.State == 'Z' {
		return proc, false
	}
	cmdline, _ := os.ReadFile(dir + "/cmdline")

	proc.PGID = stat.PGID
	proc.Cmdline = formatProcCmdline(cmdline)
	proc.StartTicks = stat.StartTicks
	proc.RSSBytes = stat.RSSPages * int64(os.Getpagesize())
	if bootTime > 0 {
		startedAt := procStartTime(bootTime, stat.StartTicks)
		proc.StartedAt = &startedAt
	}
	return proc, true
}

// KillPanelProcess 结束一个进程（#176），返回结束前读到的进程信息（给审计用）与「最终是否已退出」。
// 先核对 starttime，对不上就是 PID 已被复用，返回 ErrProcessGone；
// 组长且不是面板的组就整组结束，否则只结束这一个；先 SIGTERM，最多等 3 秒，还在就 SIGKILL 再等 1 秒。
func KillPanelProcess(pid int, startTicks uint64) (*PanelProcess, bool, error) {
	if pid <= 0 {
		return nil, false, ErrProcessGone
	}
	proc, ok := readPanelProcess(pid, readProcBootTime(), false)
	if !ok || proc.StartTicks != startTicks {
		return nil, false, ErrProcessGone
	}
	killGroup, err := decideProcessKill(pid, proc.PGID, os.Getpid(), syscall.Getpgrp())
	if err != nil {
		return &proc, false, err
	}
	proc = finishPanelProcessList([]PanelProcess{proc})[0]

	// 负数 pid = 整个进程组
	target := pid
	if killGroup {
		target = -pid
	}
	// 判断是否还活着：整组时看组里还有没有非僵尸进程；单个时看同一个进程（starttime 一致）是否还在且不是僵尸
	alive := func() bool {
		if killGroup {
			return procGroupHasLiveMember(pid)
		}
		current, ok := readPanelProcess(pid, 0, false)
		return ok && current.StartTicks == startTicks
	}
	waitGone := func(timeout time.Duration) bool {
		deadline := time.Now().Add(timeout)
		for {
			if !alive() {
				return true
			}
			if time.Now().After(deadline) {
				return false
			}
			time.Sleep(100 * time.Millisecond)
		}
	}

	if err := syscall.Kill(target, syscall.SIGTERM); err != nil {
		if errors.Is(err, syscall.ESRCH) {
			// 刚核对完就自己退出了
			return &proc, true, nil
		}
		if errors.Is(err, syscall.EPERM) {
			return &proc, false, &ProcessKillRefusedError{Reason: "没有权限结束该进程（它可能属于其他用户）"}
		}
		return &proc, false, fmt.Errorf("发送结束信号失败: %v", err)
	}
	if waitGone(3 * time.Second) {
		return &proc, true, nil
	}
	_ = syscall.Kill(target, syscall.SIGKILL)
	return &proc, waitGone(time.Second), nil
}

// procGroupHasLiveMember 扫一遍 /proc，看进程组 pgid 里还有没有活着（非僵尸）的进程。
// 不用 kill(-pgid, 0)：它把僵尸也算「还在」，而 setsid 出去的进程被 PID 1 收养后，
// PID 1 不一定及时回收，结束成功也会被误报成「没退出」。
func procGroupHasLiveMember(pgid int) bool {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return false
	}
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid <= 0 {
			continue
		}
		raw, err := os.ReadFile("/proc/" + entry.Name() + "/stat")
		if err != nil {
			continue
		}
		stat, err := parseProcStat(string(raw))
		if err != nil {
			continue
		}
		if stat.PGID == pgid && stat.State != 'Z' {
			return true
		}
	}
	return false
}
