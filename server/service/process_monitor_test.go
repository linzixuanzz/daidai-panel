package service

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"daidai-panel/config"
	"daidai-panel/database"
	"daidai-panel/model"
	"daidai-panel/testutil"
)

// 「后台进程」页（#176）的纯解析与判定，不依赖 /proc，Windows 开发机上也能跑。

// buildProcStatLine 按真实格式拼一行 /proc/<pid>/stat：comm 之后依次是第 3..24 个字段。
func buildProcStatLine(pid int, comm string, state string, ppid, pgrp int, startTicks uint64, rssPages int64) string {
	fields := make([]string, 0, 22)
	fields = append(fields, state, strconv.Itoa(ppid), strconv.Itoa(pgrp))
	// 第 6..21 个字段（session 到 itrealvalue）共 16 个，这里填 0
	for i := 6; i <= 21; i++ {
		fields = append(fields, "0")
	}
	fields = append(fields, strconv.FormatUint(startTicks, 10), "123456", strconv.FormatInt(rssPages, 10))
	// 后面还有一长串字段，补几个模拟真实行
	fields = append(fields, "18446744073709551615", "1", "1")
	return strconv.Itoa(pid) + " (" + comm + ") " + strings.Join(fields, " ") + "\n"
}

func TestParseProcStatReadsPgidStartTicksAndRSS(t *testing.T) {
	line := buildProcStatLine(4321, "sleep", "S", 1, 4321, 987654, 250)
	stat, err := parseProcStat(line)
	if err != nil {
		t.Fatalf("parse stat: %v", err)
	}
	if stat.State != 'S' || stat.PPID != 1 || stat.PGID != 4321 || stat.StartTicks != 987654 || stat.RSSPages != 250 {
		t.Fatalf("unexpected stat fields: %+v", stat)
	}
}

// 命令名里有空格和括号：必须以最后一个 ')' 为界，否则字段整体错位。
func TestParseProcStatHandlesCommWithSpacesAndParens(t *testing.T) {
	for _, comm := range []string{"my app", "weird) (name", "a (b) c)", ") )"} {
		line := buildProcStatLine(77, comm, "R", 10, 70, 5555, 3)
		stat, err := parseProcStat(line)
		if err != nil {
			t.Fatalf("comm %q: parse stat: %v", comm, err)
		}
		if stat.State != 'R' || stat.PPID != 10 || stat.PGID != 70 || stat.StartTicks != 5555 || stat.RSSPages != 3 {
			t.Fatalf("comm %q: unexpected stat fields: %+v", comm, stat)
		}
	}
}

func TestParseProcStatRejectsMalformedContent(t *testing.T) {
	cases := []string{
		// 没有右括号
		"12 sleep S 1 12",
		// 字段不足
		"12 (sleep) S 1 12 12 0",
		// pgrp 不是数字
		strings.Replace(buildProcStatLine(12, "x", "S", 1, 12, 9, 1), ") S 1 12 ", ") S 1 abc ", 1),
	}
	for _, content := range cases {
		if _, err := parseProcStat(content); err == nil {
			t.Fatalf("expected error for %q", content)
		}
	}
}

func TestProcEnvironValue(t *testing.T) {
	environ := []byte("PATH=/usr/bin\x00DAIDAI_TASK_ID=42\x00DAIDAI_TASK_ID_EXTRA=9\x00EMPTY=\x00")
	if value, ok := procEnvironValue(environ, "DAIDAI_TASK_ID"); !ok || value != "42" {
		t.Fatalf("expected DAIDAI_TASK_ID=42, got %q ok=%v", value, ok)
	}
	if value, ok := procEnvironValue(environ, "EMPTY"); !ok || value != "" {
		t.Fatalf("expected EMPTY to exist with empty value, got %q ok=%v", value, ok)
	}
	// 前缀相同的别的变量不能误认
	if _, ok := procEnvironValue([]byte("DAIDAI_TASK_ID_EXTRA=9\x00"), "DAIDAI_TASK_ID"); ok {
		t.Fatalf("DAIDAI_TASK_ID_EXTRA must not match DAIDAI_TASK_ID")
	}
	if _, ok := procEnvironValue(nil, "DAIDAI_TASK_ID"); ok {
		t.Fatalf("empty environ must not match")
	}
}

func TestFormatProcCmdline(t *testing.T) {
	if got := formatProcCmdline([]byte("sleep\x00600\x00")); got != "sleep 600" {
		t.Fatalf("expected %q, got %q", "sleep 600", got)
	}
	if got := formatProcCmdline(nil); got != "" {
		t.Fatalf("kernel thread cmdline should be empty, got %q", got)
	}
	long := strings.Repeat("参", processCmdlineMaxRunes+50)
	got := formatProcCmdline([]byte(long))
	if !strings.HasSuffix(got, "…") || len([]rune(got)) != processCmdlineMaxRunes+1 {
		t.Fatalf("expected truncation to %d runes plus ellipsis, got %d runes", processCmdlineMaxRunes, len([]rune(got)))
	}
}

func TestParseProcBootTimeAndStartTime(t *testing.T) {
	content := "cpu  1 2 3 4\nintr 0\nctxt 100\nbtime 1700000000\nprocesses 5\n"
	bootTime, ok := parseProcBootTime(content)
	if !ok || bootTime != 1700000000 {
		t.Fatalf("expected btime 1700000000, got %d ok=%v", bootTime, ok)
	}
	if _, ok := parseProcBootTime("cpu 1 2 3\n"); ok {
		t.Fatalf("missing btime must report not ok")
	}

	// 12345 tick = 123.45 秒
	got := procStartTime(1700000000, 12345)
	want := time.Unix(1700000123, 450*int64(time.Millisecond))
	if !got.Equal(want) {
		t.Fatalf("expected %v, got %v", want, got)
	}
	// 开机好几年也不能溢出：10 年的 tick 数
	tenYears := uint64(10 * 365 * 24 * 3600 * procClockTicksPerSecond)
	if got := procStartTime(1700000000, tenYears); got.Unix() != 1700000000+int64(10*365*24*3600) {
		t.Fatalf("expected no overflow for long uptime, got %v", got)
	}
}

func TestDecideProcessKill(t *testing.T) {
	const selfPID, selfPGID = 500, 400

	var refused *ProcessKillRefusedError
	// 依次是：PID 1、面板自身、面板所在进程组的成员、面板进程组的组长
	for _, args := range [][2]int{{1, 1}, {selfPID, selfPGID}, {501, selfPGID}, {selfPGID, selfPGID}} {
		if _, err := decideProcessKill(args[0], args[1], selfPID, selfPGID); !errors.As(err, &refused) {
			t.Fatalf("pid=%d pgid=%d: expected refusal, got %v", args[0], args[1], err)
		}
	}

	// 组长（setsid 拉起的常驻进程）→ 结束整组
	killGroup, err := decideProcessKill(900, 900, selfPID, selfPGID)
	if err != nil || !killGroup {
		t.Fatalf("group leader: expected group kill, got killGroup=%v err=%v", killGroup, err)
	}
	// 不是组长（比如还在任务进程组里的子进程）→ 只结束它自己
	killGroup, err = decideProcessKill(901, 900, selfPID, selfPGID)
	if err != nil || killGroup {
		t.Fatalf("non-leader: expected single kill, got killGroup=%v err=%v", killGroup, err)
	}
}

// 状态判定：进程组正是任务当前登记的进程 → 随任务运行中；否则已脱离；任务名查不到（已删除）留空。
func TestFinishPanelProcessListFillsTaskNameAndState(t *testing.T) {
	testutil.SetupTestEnv(t)

	task := &model.Task{Name: "常驻服务", Command: "bash keep.sh", TaskType: model.TaskTypeManual}
	if err := database.DB.Create(task).Error; err != nil {
		t.Fatalf("create task: %v", err)
	}

	previous := globalExecutor
	executor := NewTaskExecutor()
	globalExecutor = executor
	t.Cleanup(func() { globalExecutor = previous })
	// 登记一个「正在运行的任务进程」，组号 3000（pid 即组号）
	executor.registerRunningProcess(nil, task.ID, &os.Process{Pid: 3000})

	taskID := task.ID
	deletedID := task.ID + 999
	// 依次是：不是任务进程、任务已删除、setsid 出去已脱离、还在任务进程组里
	list := finishPanelProcessList([]PanelProcess{
		{PID: 9000, PGID: 9000},
		{PID: 3100, PGID: 9100, TaskID: &deletedID},
		{PID: 3002, PGID: 3002, TaskID: &taskID},
		{PID: 3001, PGID: 3000, TaskID: &taskID},
	})

	if len(list) != 4 {
		t.Fatalf("expected 4 rows, got %d", len(list))
	}
	// 排序：任务进程在前（按任务 ID、PID），其余在后
	if list[0].PID != 3001 || list[1].PID != 3002 || list[2].PID != 3100 || list[3].PID != 9000 {
		t.Fatalf("unexpected order: %d %d %d %d", list[0].PID, list[1].PID, list[2].PID, list[3].PID)
	}
	if list[0].State != ProcessStateRunningWithTask || list[0].TaskName != "常驻服务" {
		t.Fatalf("row in task group: expected running_with_task + task name, got %+v", list[0])
	}
	if list[1].State != ProcessStateDetached || list[1].TaskName != "常驻服务" {
		t.Fatalf("setsid row: expected detached + task name, got %+v", list[1])
	}
	if list[2].State != ProcessStateDetached || list[2].TaskName != "" {
		t.Fatalf("deleted task row: expected detached + empty name, got %+v", list[2])
	}
	if list[3].State != ProcessStateOther || list[3].TaskID != nil {
		t.Fatalf("non-task row: expected other, got %+v", list[3])
	}
}

// 任务执行的环境里一定有 DAIDAI_TASK_ID（#176），用户同名变量被面板值覆盖。
func TestRunTaskInjectsTaskIDEnv(t *testing.T) {
	testutil.SetupTestEnv(t)

	userVar := model.EnvVar{Name: TaskIDEnvKey, Value: "user-value", Enabled: true}
	if err := database.DB.Create(&userVar).Error; err != nil {
		t.Fatalf("create user env: %v", err)
	}

	var envVars map[string]string
	stubRunCommandWithPlan(t, func(taskEnv map[string]string) (*ScriptResult, error) {
		envVars = copyScriptEnv(taskEnv)
		return &ScriptResult{ReturnCode: 0}, nil
	})

	runTaskForScriptTokenTest(t, 0)

	var task model.Task
	if err := database.DB.Where("name = ?", "script-token-probe").First(&task).Error; err != nil {
		t.Fatalf("load task: %v", err)
	}
	if got := envVars[TaskIDEnvKey]; got != strconv.FormatUint(uint64(task.ID), 10) {
		t.Fatalf("expected %s=%d, got %q", TaskIDEnvKey, task.ID, got)
	}
}

// 指定了「可用环境变量」范围（#168）时也照样注入任务标识。
func TestRunTaskInjectsTaskIDEnvWithSelectedEnvScope(t *testing.T) {
	testutil.SetupTestEnv(t)

	var envVars map[string]string
	stubRunCommandWithPlan(t, func(taskEnv map[string]string) (*ScriptResult, error) {
		envVars = copyScriptEnv(taskEnv)
		return &ScriptResult{ReturnCode: 0}, nil
	})

	task := &model.Task{
		Name:      "scoped-task-id-probe",
		Command:   "node probe.js",
		TaskType:  model.TaskTypeManual,
		Status:    model.TaskStatusRunning,
		EnvScope:  model.TaskEnvScopeSelected,
		EnvNames:  "NOT_EXISTING_VAR",
		EnvGroups: "",
	}
	if err := database.DB.Create(task).Error; err != nil {
		t.Fatalf("create task: %v", err)
	}
	runningStatus := model.LogStatusRunning
	taskLog := &model.TaskLog{TaskID: task.ID, Status: &runningStatus, StartedAt: time.Now()}
	if err := database.DB.Create(taskLog).Error; err != nil {
		t.Fatalf("create task log: %v", err)
	}
	plan := &CommandExecutionPlan{Interpreter: "node", FullPath: filepath.Join(config.C.Data.ScriptsDir, "probe.js"), Mode: commandModeNormal}
	req := &ExecutionRequest{TaskID: task.ID, Task: task, TaskLogID: taskLog.ID, CommandPlan: plan}
	NewTaskExecutor().runTask(req, taskLog, nil)

	if got := envVars[TaskIDEnvKey]; got != strconv.FormatUint(uint64(task.ID), 10) {
		t.Fatalf("expected %s=%d under selected env scope, got %q", TaskIDEnvKey, task.ID, got)
	}
}

// 任务标识必须进真实进程环境（/proc/<pid>/environ 才读得到），其它用户变量照旧不进。
func TestBuildBootstrapProcessEnvCarriesTaskID(t *testing.T) {
	testutil.SetupTestEnv(t)

	env := buildBootstrapProcessEnv(map[string]string{TaskIDEnvKey: "7", "JD_COOKIE": "secret"})
	if got := testProcessEnvValue(env, TaskIDEnvKey); got != "7" {
		t.Fatalf("expected %s=7 in process env, got %v", TaskIDEnvKey, env)
	}
	if got := testProcessEnvValue(env, "JD_COOKIE"); got != "" {
		t.Fatalf("user env must stay out of the process env, got %v", env)
	}

	// 非任务执行（调试运行、ddp）没有这个键，进程环境里也不能凭空出现
	env = buildBootstrapProcessEnv(map[string]string{})
	for _, entry := range env {
		if strings.HasPrefix(entry, TaskIDEnvKey+"=") {
			t.Fatalf("unexpected %s without task, got %v", TaskIDEnvKey, env)
		}
	}
}
