//go:build linux

package service

import (
	"errors"
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"testing"
	"time"

	"daidai-panel/testutil"
)

// 「后台进程」页（#176）在真实 /proc 上的行为：列出带任务标识的进程、结束时的 409 / 拒绝 / 整组结束。

// startProbeSleep 起一个 sleep 进程；newGroup=true 时让它自成进程组（模拟 setsid 出去的常驻进程）。
func startProbeSleep(t *testing.T, newGroup bool, extraEnv ...string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command("sleep", "30")
	cmd.Env = append(os.Environ(), extraEnv...)
	if newGroup {
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	}
	if err := cmd.Start(); err != nil {
		t.Skipf("sleep not available: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})
	return cmd
}

func readProbeStartTicks(t *testing.T, pid int) uint64 {
	t.Helper()
	raw, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		t.Fatalf("read stat of %d: %v", pid, err)
	}
	stat, err := parseProcStat(string(raw))
	if err != nil {
		t.Fatalf("parse stat of %d: %v", pid, err)
	}
	return stat.StartTicks
}

func TestListPanelProcessesFindsTaskProcess(t *testing.T) {
	testutil.SetupTestEnv(t)

	cmd := startProbeSleep(t, true, TaskIDEnvKey+"=987654")
	list, err := ListPanelProcesses(false)
	if err != nil {
		t.Fatalf("list processes: %v", err)
	}
	for _, proc := range list {
		if proc.PID != cmd.Process.Pid {
			continue
		}
		if proc.TaskID == nil || *proc.TaskID != 987654 {
			t.Fatalf("expected task id 987654, got %+v", proc)
		}
		if proc.PGID != cmd.Process.Pid || proc.State != ProcessStateDetached || proc.Cmdline != "sleep 30" {
			t.Fatalf("unexpected row: %+v", proc)
		}
		if proc.StartTicks == 0 || proc.StartedAt == nil || proc.RSSBytes <= 0 {
			t.Fatalf("expected start ticks / started_at / rss, got %+v", proc)
		}
		if time.Since(*proc.StartedAt) > time.Minute || time.Until(*proc.StartedAt) > time.Minute {
			t.Fatalf("started_at should be about now, got %v", *proc.StartedAt)
		}
		return
	}
	t.Fatalf("task process %d not listed in %+v", cmd.Process.Pid, list)
}

func TestListPanelProcessesSkipsProcessWithoutTaskID(t *testing.T) {
	testutil.SetupTestEnv(t)

	cmd := startProbeSleep(t, true)
	list, err := ListPanelProcesses(false)
	if err != nil {
		t.Fatalf("list processes: %v", err)
	}
	for _, proc := range list {
		if proc.PID == cmd.Process.Pid {
			t.Fatalf("process without %s must not be listed in task scope: %+v", TaskIDEnvKey, proc)
		}
	}
}

func TestKillPanelProcessRejectsStaleStartTicks(t *testing.T) {
	testutil.SetupTestEnv(t)

	cmd := startProbeSleep(t, true)
	ticks := readProbeStartTicks(t, cmd.Process.Pid)
	if _, _, err := KillPanelProcess(cmd.Process.Pid, ticks+1); !errors.Is(err, ErrProcessGone) {
		t.Fatalf("expected ErrProcessGone for mismatched start ticks, got %v", err)
	}
	// 进程必须还活着
	if err := syscall.Kill(cmd.Process.Pid, 0); err != nil {
		t.Fatalf("process must survive a rejected kill: %v", err)
	}
}

func TestKillPanelProcessRefusesSelfAndPanelGroup(t *testing.T) {
	testutil.SetupTestEnv(t)

	var refused *ProcessKillRefusedError
	selfTicks := readProbeStartTicks(t, os.Getpid())
	if _, _, err := KillPanelProcess(os.Getpid(), selfTicks); !errors.As(err, &refused) {
		t.Fatalf("expected refusal for the panel itself, got %v", err)
	}

	// 没有 setpgid 的子进程和面板（这里是测试进程）同组，不能从这里结束
	cmd := startProbeSleep(t, false)
	ticks := readProbeStartTicks(t, cmd.Process.Pid)
	if _, _, err := KillPanelProcess(cmd.Process.Pid, ticks); !errors.As(err, &refused) {
		t.Fatalf("expected refusal for a process in the panel's group, got %v", err)
	}
}

func TestKillPanelProcessEndsGroupLeader(t *testing.T) {
	testutil.SetupTestEnv(t)

	cmd := startProbeSleep(t, true, TaskIDEnvKey+"=5")
	pid := cmd.Process.Pid
	ticks := readProbeStartTicks(t, pid)

	proc, exited, err := KillPanelProcess(pid, ticks)
	if err != nil {
		t.Fatalf("kill: %v", err)
	}
	if !exited {
		t.Fatalf("expected the process to exit after SIGTERM")
	}
	if proc == nil || proc.PID != pid || proc.TaskID == nil || *proc.TaskID != 5 || proc.Cmdline != "sleep 30" {
		t.Fatalf("expected process info for audit, got %+v", proc)
	}
	if procGroupHasLiveMember(pid) {
		t.Fatalf("group %d should be gone", pid)
	}
}
