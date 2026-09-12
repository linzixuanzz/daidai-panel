package service

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"daidai-panel/config"
	"daidai-panel/database"
	"daidai-panel/model"
	"daidai-panel/testutil"
)

// 订阅同步「按脚本认任务、已有任务一律不动」（#125，取代订阅锁）的端到端用例：直接调 syncSubscriptionTasks 断言数据库。
// 字典、求键、删除判定的纯函数单测在 subscription_task_script_match_test.go。

func steEnv(t *testing.T) {
	t.Helper()
	testutil.SetupTestEnv(t)
	InitSchedulerV2()
	t.Cleanup(ShutdownSchedulerV2)
}

// steWriteScript 写一个带 cron 头与 new Env 名称的脚本；name 用正斜杠。
func steWriteScript(t *testing.T, saveDir, name, cron, envName string) string {
	t.Helper()
	full := filepath.Join(config.C.Data.ScriptsDir, saveDir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	body := "//cron: " + cron + "\nconst $ = new Env('" + envName + "');\n"
	if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return full
}

// steSubscription 铺脚本（name → cron，任务名 = 文件名去扩展名）并建订阅：自动添加开，自动删除按参数。
func steSubscription(t *testing.T, saveDir string, autoDelete bool, scripts map[string]string) *model.Subscription {
	t.Helper()
	for name, cron := range scripts {
		steWriteScript(t, saveDir, name, cron, strings.TrimSuffix(filepath.Base(name), filepath.Ext(name)))
	}
	delMode := model.SubTaskSyncDisabled
	if autoDelete {
		delMode = model.SubTaskSyncEnabled
	}
	sub := &model.Subscription{
		Name:            saveDir,
		Type:            model.SubTypeGitRepo,
		URL:             "https://github.com/u/" + saveDir + ".git",
		SaveDir:         saveDir,
		Enabled:         true,
		AutoAddTaskMode: model.SubTaskSyncEnabled,
		AutoDelTaskMode: delMode,
	}
	if err := database.DB.Create(sub).Error; err != nil {
		t.Fatal(err)
	}
	return sub
}

func steCmd(saveDir, name string) string {
	return "task " + filepath.Join(saveDir, filepath.FromSlash(name))
}

func steSync(sub *model.Subscription) []string {
	var logs []string
	syncSubscriptionTasks(sub, func(line string) { logs = append(logs, line) })
	return logs
}

func steManaged(sub *model.Subscription) []model.Task {
	var tasks []model.Task
	queryTasksByLabel(subscriptionTaskLabel(sub.ID)).Order("id").Find(&tasks)
	return tasks
}

func steReload(id uint) (model.Task, bool) {
	var task model.Task
	err := database.DB.First(&task, id).Error
	return task, err == nil
}

func steNewTask(t *testing.T, name, command, cron string, labels ...string) *model.Task {
	t.Helper()
	task := model.Task{Name: name, Command: command, CronExpression: cron, TaskType: model.TaskTypeCron, Status: model.TaskStatusEnabled}
	task.SetLabelsFromSlice(labels)
	if err := database.DB.Select("*").Create(&task).Error; err != nil {
		t.Fatal(err)
	}
	return &task
}

func steSetCommand(t *testing.T, task *model.Task, command string) {
	t.Helper()
	if err := database.DB.Model(&model.Task{}).Where("id = ?", task.ID).Update("command", command).Error; err != nil {
		t.Fatal(err)
	}
	task.Command = command
}

func steAddLog(t *testing.T, taskID uint) {
	t.Helper()
	if err := database.DB.Create(&model.TaskLog{TaskID: taskID, Content: "历史日志"}).Error; err != nil {
		t.Fatal(err)
	}
}

func steLogCount(taskID uint) int64 {
	var n int64
	database.DB.Model(&model.TaskLog{}).Where("task_id = ?", taskID).Count(&n)
	return n
}

func steCountLines(logs []string, keyword string) int {
	n := 0
	for _, line := range logs {
		if strings.Contains(line, keyword) {
			n++
		}
	}
	return n
}

func steDump(tasks []model.Task) string {
	var b strings.Builder
	for _, task := range tasks {
		b.WriteString("\n  #")
		b.WriteString(strings.TrimSpace(task.Name))
		b.WriteString(" | " + task.CronExpression + " | " + task.Command + " | " + task.Labels)
	}
	return b.String()
}

func findTaskByCommand(t *testing.T, command string) *model.Task {
	t.Helper()
	var task model.Task
	if err := database.DB.Where("command = ?", command).First(&task).Error; err != nil {
		t.Fatalf("task %q not found: %v", command, err)
	}
	return &task
}

func containsLogLine(logs []string, keyword string) bool {
	return steCountLines(logs, keyword) > 0
}

// steAssertUnchanged：任务还在，命令、名称、定时、状态、标签都与 want 一致。
func steAssertUnchanged(t *testing.T, want model.Task, logs []string) {
	t.Helper()
	got, ok := steReload(want.ID)
	if !ok {
		t.Fatalf("task %d (%s) was deleted\n%s", want.ID, want.Name, strings.Join(logs, "\n"))
	}
	if got.Command != want.Command || got.Name != want.Name || got.CronExpression != want.CronExpression ||
		got.Status != want.Status || got.Labels != want.Labels {
		t.Fatalf("task %d changed:\n want name=%q cron=%q cmd=%q labels=%q\n  got name=%q cron=%q cmd=%q labels=%q\n%s",
			want.ID, want.Name, want.CronExpression, want.Command, want.Labels,
			got.Name, got.CronExpression, got.Command, got.Labels, strings.Join(logs, "\n"))
	}
}

func steAssertNoAddDelete(t *testing.T, logs []string) {
	t.Helper()
	for _, keyword := range []string{"[自动添加任务]", "[自动删除任务]", "[自动更新任务]", "共自动更新"} {
		if containsLogLine(logs, keyword) {
			t.Fatalf("unexpected %s\n%s", keyword, strings.Join(logs, "\n"))
		}
	}
}

// steBlockTaskDeletes 让 tasks 表的 DELETE 一律失败，用来验证「删任务失败时历史日志不能先没了」。
func steBlockTaskDeletes(t *testing.T) {
	t.Helper()
	if err := database.DB.Exec(`CREATE TRIGGER ste_block_task_delete BEFORE DELETE ON tasks BEGIN SELECT RAISE(ABORT, 'blocked by test'); END;`).Error; err != nil {
		t.Fatalf("create trigger: %v", err)
	}
}

// #125 本体：用户改了命令（加参数、换写法、换 runner），自动删除开 / 关各同步两轮：
// 该脚本只有这 1 条任务，ID、命令、名称、定时不变，历史日志不变，不新建也不删除。
func TestSyncSubscriptionTasksEditedCommandKeepsSingleTask(t *testing.T) {
	rel := func(saveDir, name string) string { return filepath.Join(saveDir, name) }
	forms := []struct {
		name   string
		script string
		edit   func(saveDir string) string
	}{
		{"now", "biz.js", func(d string) string { return "task " + rel(d, "biz.js") + " now" }},
		{"desi", "biz.js", func(d string) string { return "task " + rel(d, "biz.js") + " desi JD_COOKIE" }},
		{"desi_range", "biz.js", func(d string) string { return "task " + rel(d, "biz.js") + " desi JD_COOKIE 1-3" }},
		{"conc", "biz.js", func(d string) string { return "task " + rel(d, "biz.js") + " conc JD_COOKIE" }},
		{"passthru", "biz.js", func(d string) string { return "task " + rel(d, "biz.js") + " -- --flag" }},
		{"timeout", "biz.js", func(d string) string { return "task -m 30m " + rel(d, "biz.js") + " now" }},
		{"log_flag", "biz.js", func(d string) string { return "task -l " + rel(d, "biz.js") }},
		{"dot_slash", "biz.js", func(d string) string { return "task ./" + d + "/biz.js now" }},
		{"quoted", "biz.js", func(d string) string { return `task "` + rel(d, "biz.js") + `" now` }},
		{"dbl_space", "biz.js", func(d string) string { return "task  " + rel(d, "biz.js") + "  now" }},
		{"trailing", "biz.js", func(d string) string { return "task " + rel(d, "biz.js") + " now   " }},
		{"desi_first", "biz.js", func(d string) string { return "desi " + rel(d, "biz.js") + " JD_COOKIE" }},
		{"node", "biz.js", func(d string) string { return "node " + rel(d, "biz.js") }},
		{"python3", "tool.py", func(d string) string { return "python3 " + rel(d, "tool.py") }},
		{"abs", "biz.js", func(d string) string {
			abs, err := filepath.Abs(filepath.Join(config.C.Data.ScriptsDir, d, "biz.js"))
			if err != nil {
				panic(err)
			}
			return "task " + abs + " now"
		}},
		{"forward_slash", "biz.js", func(d string) string { return "task " + d + "/biz.js desi JD_COOKIE" }},
	}
	for _, form := range forms {
		for _, autoDelete := range []bool{true, false} {
			name := form.name + "_del_off"
			if autoDelete {
				name = form.name + "_del_on"
			}
			t.Run(name, func(t *testing.T) {
				steEnv(t)
				saveDir := "ste_edit"
				sub := steSubscription(t, saveDir, autoDelete, map[string]string{"biz.js": "9 5 * * *", "tool.py": "8 4 * * *"})
				steSync(sub)
				other := "tool.py"
				if form.script == "tool.py" {
					other = "biz.js"
				}
				target := findTaskByCommand(t, steCmd(saveDir, form.script))
				steSetCommand(t, target, form.edit(saveDir))
				steAddLog(t, target.ID)
				want := *target

				for round := 1; round <= 2; round++ {
					logs := steSync(sub)
					if tasks := steManaged(sub); len(tasks) != 2 {
						t.Fatalf("round %d: want 2 tasks, got %s\n%s", round, steDump(tasks), strings.Join(logs, "\n"))
					}
					steAssertUnchanged(t, want, logs)
					findTaskByCommand(t, steCmd(saveDir, other))
					if n := steLogCount(target.ID); n != 1 {
						t.Fatalf("round %d: task logs changed, got %d", round, n)
					}
					steAssertNoAddDelete(t, logs)
					if !containsLogLine(logs, "[同步完成] 本次未对定时任务做任何变更") {
						t.Fatalf("round %d: expected no-op summary\n%s", round, strings.Join(logs, "\n"))
					}
				}
			})
		}
	}
}

// 上游改了脚本头的 cron 与名称：已有任务（原样命令的、改过命令的）都不变，不再有 [自动更新任务]。
func TestSyncSubscriptionTasksIgnoresUpstreamNameAndCronChanges(t *testing.T) {
	steEnv(t)
	saveDir := "ste_upstream"
	sub := steSubscription(t, saveDir, true, map[string]string{"a.js": "1 1 * * *", "b.js": "2 2 * * *"})
	steSync(sub)
	a := findTaskByCommand(t, steCmd(saveDir, "a.js"))
	b := findTaskByCommand(t, steCmd(saveDir, "b.js"))
	steSetCommand(t, b, b.Command+" now")
	wantA, wantB := *a, *b
	steWriteScript(t, saveDir, "a.js", "10 6 * * *", "上游新名字A")
	steWriteScript(t, saveDir, "b.js", "20 7 * * *", "上游新名字B")

	for round := 1; round <= 2; round++ {
		logs := steSync(sub)
		steAssertUnchanged(t, wantA, logs)
		steAssertUnchanged(t, wantB, logs)
		steAssertNoAddDelete(t, logs)
		if n := len(steManaged(sub)); n != 2 {
			t.Fatalf("round %d: want 2 tasks, got %d", round, n)
		}
	}
}

// E2：复制任务（副本带着订阅标签）再改副本的命令——两条都在、都不动，不新建。
func TestSyncSubscriptionTasksCopiedTaskEditedKeepsBoth(t *testing.T) {
	steEnv(t)
	saveDir := "ste_copy"
	sub := steSubscription(t, saveDir, true, map[string]string{"biz.js": "9 5 * * *"})
	steSync(sub)
	original := findTaskByCommand(t, steCmd(saveDir, "biz.js"))
	copied := steNewTask(t, original.Name+" - 副本", original.Command, original.CronExpression, original.GetLabels()...)
	steSetCommand(t, copied, copied.Command+" desi JD_COOKIE 2")
	steAddLog(t, original.ID)
	steAddLog(t, copied.ID)
	wantOriginal, wantCopy := *original, *copied

	for round := 1; round <= 2; round++ {
		logs := steSync(sub)
		steAssertUnchanged(t, wantOriginal, logs)
		steAssertUnchanged(t, wantCopy, logs)
		steAssertNoAddDelete(t, logs)
		if n := len(steManaged(sub)); n != 2 {
			t.Fatalf("round %d: want 2 tasks, got %d", round, n)
		}
		if steLogCount(original.ID) != 1 || steLogCount(copied.ID) != 1 {
			t.Fatalf("round %d: task logs changed", round)
		}
	}
}

// 同一脚本有多条改过命令的任务、没有规范命令那条：不新建，都不动。
func TestSyncSubscriptionTasksMultipleEditedTasksAreLeftAlone(t *testing.T) {
	steEnv(t)
	saveDir := "ste_multi"
	sub := steSubscription(t, saveDir, true, map[string]string{"biz.js": "9 5 * * *"})
	steSync(sub)
	first := findTaskByCommand(t, steCmd(saveDir, "biz.js"))
	steSetCommand(t, first, first.Command+" desi JD_COOKIE 1")
	second := steNewTask(t, "第二个账号", steCmd(saveDir, "biz.js")+" desi JD_COOKIE 2", "3 3 * * *", subscriptionTaskLabel(sub.ID))
	wantFirst, wantSecond := *first, *second
	steWriteScript(t, saveDir, "biz.js", "10 6 * * *", "上游新名字")

	for round := 1; round <= 2; round++ {
		logs := steSync(sub)
		steAssertUnchanged(t, wantFirst, logs)
		steAssertUnchanged(t, wantSecond, logs)
		steAssertNoAddDelete(t, logs)
		if n := len(steManaged(sub)); n != 2 {
			t.Fatalf("round %d: want 2 tasks, got %d", round, n)
		}
	}
}

// 把 a.js 的任务改成跑 b.js（b.js 已有规范任务）：改过的那条归 b.js、字段不变，a.js 新建一条。
func TestSyncSubscriptionTasksRepointedTaskKeepsFields(t *testing.T) {
	steEnv(t)
	saveDir := "ste_repoint"
	sub := steSubscription(t, saveDir, true, map[string]string{"a.js": "1 1 * * *", "b.js": "2 2 * * *"})
	steSync(sub)
	a := findTaskByCommand(t, steCmd(saveDir, "a.js"))
	b := findTaskByCommand(t, steCmd(saveDir, "b.js"))
	steSetCommand(t, a, steCmd(saveDir, "b.js")+" now")
	wantA, wantB := *a, *b

	logs := steSync(sub)
	steAssertUnchanged(t, wantA, logs)
	steAssertUnchanged(t, wantB, logs)
	recreated := findTaskByCommand(t, steCmd(saveDir, "a.js"))
	if recreated.ID == a.ID || recreated.Name != "a" || recreated.CronExpression != "1 1 * * *" {
		t.Fatalf("a.js should get a fresh canonical task, got %+v", recreated)
	}
	if n := len(steManaged(sub)); n != 3 {
		t.Fatalf("want 3 tasks, got %s", steDump(steManaged(sub)))
	}
	if steCountLines(logs, "[自动添加任务]") != 1 || containsLogLine(logs, "[自动删除任务]") {
		t.Fatalf("want exactly one add and no delete\n%s", strings.Join(logs, "\n"))
	}
	if logs := steSync(sub); !containsLogLine(logs, "[同步完成] 本次未对定时任务做任何变更") {
		t.Fatalf("second round should be a no-op\n%s", strings.Join(logs, "\n"))
	}
}

// 上游删了脚本：规范命令与改过参数的任务都按开关处理——开则连日志删，关则都保留。
func TestSyncSubscriptionTasksUpstreamRemovedScriptFollowsSwitch(t *testing.T) {
	for _, autoDelete := range []bool{true, false} {
		name := "del_off"
		if autoDelete {
			name = "del_on"
		}
		t.Run(name, func(t *testing.T) {
			steEnv(t)
			saveDir := "ste_removed"
			sub := steSubscription(t, saveDir, autoDelete, map[string]string{"keep.js": "1 1 * * *", "gone.js": "2 2 * * *"})
			steSync(sub)
			keep := findTaskByCommand(t, steCmd(saveDir, "keep.js"))
			canonical := findTaskByCommand(t, steCmd(saveDir, "gone.js"))
			edited := steNewTask(t, "gone 带参数", steCmd(saveDir, "gone.js")+" desi JD_COOKIE", "4 4 * * *", subscriptionTaskLabel(sub.ID))
			steAddLog(t, canonical.ID)
			steAddLog(t, edited.ID)
			if err := os.Remove(filepath.Join(config.C.Data.ScriptsDir, saveDir, "gone.js")); err != nil {
				t.Fatal(err)
			}

			logs := steSync(sub)
			steAssertUnchanged(t, *keep, logs)
			for _, task := range []*model.Task{canonical, edited} {
				_, exists := steReload(task.ID)
				if autoDelete && (exists || steLogCount(task.ID) != 0) {
					t.Fatalf("%s should be deleted with its logs\n%s", task.Name, strings.Join(logs, "\n"))
				}
				if !autoDelete && (!exists || steLogCount(task.ID) != 1) {
					t.Fatalf("%s should be kept with its logs\n%s", task.Name, strings.Join(logs, "\n"))
				}
			}
			if autoDelete && (steCountLines(logs, "[自动删除任务]") != 2 || !containsLogLine(logs, "[共自动删除 2 个失效任务]")) {
				t.Fatalf("want two delete lines\n%s", strings.Join(logs, "\n"))
			}
			if !autoDelete && containsLogLine(logs, "[自动删除任务]") {
				t.Fatalf("auto delete is off\n%s", strings.Join(logs, "\n"))
			}
		})
	}
}

// review-r2 R2-1：命令参数里带 URL / 盘符（含 : ? * | < > 这类在 Windows 上非法的文件名字符）的失效任务。
// 脚本在时按脚本键认出、保留；上游删掉脚本后按开关删除（含历史日志）。
// 改前：os.Stat 对最长前缀报 ERROR_INVALID_NAME 被当成「无法确认」，任务永远删不掉，还每次拉取误报「权限不足或存储异常」。
func TestSyncSubscriptionTasksUrlOrDriveArgFollowsSwitch(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("ERROR_INVALID_NAME 由路径里的 : ? * | < > 触发，是 Windows 专属；Linux 的超长段等价场景见 TestSyncSubscriptionTasksLongArgFollowsSwitch")
	}
	for _, arg := range []string{"https://cdn.example.com/lib/b.js", "C:/cfg/settings.js"} {
		for _, autoDelete := range []bool{true, false} {
			name := arg
			if autoDelete {
				name += "_del_on"
			} else {
				name += "_del_off"
			}
			t.Run(name, func(t *testing.T) {
				steEnv(t)
				saveDir := "ste_urlarg"
				sub := steSubscription(t, saveDir, autoDelete, map[string]string{"loader.js": "1 1 * * *", "keep.js": "2 2 * * *"})
				steSync(sub)
				loader := findTaskByCommand(t, steCmd(saveDir, "loader.js"))
				steSetCommand(t, loader, loader.Command+" "+arg)
				steAddLog(t, loader.ID)
				want := *loader

				// 脚本还在：按脚本键认出，保留，不新建重复任务、不删。
				logs := steSync(sub)
				steAssertUnchanged(t, want, logs)
				steAssertNoAddDelete(t, logs)
				if n := len(steManaged(sub)); n != 2 || steLogCount(loader.ID) != 1 {
					t.Fatalf("script present: want 2 kept tasks with the log, got %s\n%s", steDump(steManaged(sub)), strings.Join(logs, "\n"))
				}

				// 上游删掉脚本：按开关删（含日志）或保留。
				if err := os.Remove(filepath.Join(config.C.Data.ScriptsDir, saveDir, "loader.js")); err != nil {
					t.Fatal(err)
				}
				logs = steSync(sub)
				_, exists := steReload(loader.ID)
				if autoDelete {
					if exists || steLogCount(loader.ID) != 0 || !containsLogLine(logs, "[自动删除任务] loader") {
						t.Fatalf("del on: url-arg task should be deleted with its logs\n%s", strings.Join(logs, "\n"))
					}
				} else if !exists || steLogCount(loader.ID) != 1 ||
					containsLogLine(logs, "[自动删除任务]") || containsLogLine(logs, "可能是权限不足或存储异常") {
					t.Fatalf("del off: url-arg task should be kept, no delete and no misleading stat-error hint\n%s", strings.Join(logs, "\n"))
				}
			})
		}
	}
}

// review-r2 R2-1（Linux）：命令参数超长（Stat 报 ENAMETOOLONG）的失效任务，上游删掉脚本后按开关删除。
func TestSyncSubscriptionTasksLongArgFollowsSwitch(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("超长参数段在 Windows 上报 ERROR_INVALID_NAME / FILENAME_EXCED_RANGE，等价 URL / 盘符场景见 TestSyncSubscriptionTasksUrlOrDriveArgFollowsSwitch")
	}
	for _, autoDelete := range []bool{true, false} {
		name := "del_off"
		if autoDelete {
			name = "del_on"
		}
		t.Run(name, func(t *testing.T) {
			steEnv(t)
			saveDir := "ste_longarg"
			sub := steSubscription(t, saveDir, autoDelete, map[string]string{"loader.js": "1 1 * * *", "keep.js": "2 2 * * *"})
			steSync(sub)
			loader := findTaskByCommand(t, steCmd(saveDir, "loader.js"))
			steSetCommand(t, loader, loader.Command+" "+strings.Repeat("x", 300)+".js")
			steAddLog(t, loader.ID)
			want := *loader

			logs := steSync(sub)
			steAssertUnchanged(t, want, logs)
			steAssertNoAddDelete(t, logs)

			if err := os.Remove(filepath.Join(config.C.Data.ScriptsDir, saveDir, "loader.js")); err != nil {
				t.Fatal(err)
			}
			logs = steSync(sub)
			_, exists := steReload(loader.ID)
			if autoDelete {
				if exists || steLogCount(loader.ID) != 0 || !containsLogLine(logs, "[自动删除任务] loader") {
					t.Fatalf("del on: long-arg task should be deleted with its logs\n%s", strings.Join(logs, "\n"))
				}
			} else if !exists || steLogCount(loader.ID) != 1 || containsLogLine(logs, "[自动删除任务]") {
				t.Fatalf("del off: long-arg task should be kept\n%s", strings.Join(logs, "\n"))
			}
		})
	}
}

// 文件还在盘上、但被新加的黑名单排除：判据是「在候选集里」不是「文件存在」，改过命令的也照删。
func TestSyncSubscriptionTasksBlacklistedScriptIsStillDeleted(t *testing.T) {
	steEnv(t)
	saveDir := "ste_black"
	sub := steSubscription(t, saveDir, true, map[string]string{"biz.js": "9 5 * * *", "old_task.js": "1 1 * * *"})
	steSync(sub)
	old := findTaskByCommand(t, steCmd(saveDir, "old_task.js"))
	steSetCommand(t, old, old.Command+" now")
	steAddLog(t, old.ID)
	sub.Blacklist = "old_task"

	logs := steSync(sub)
	if _, exists := steReload(old.ID); exists || steLogCount(old.ID) != 0 {
		t.Fatalf("blacklisted script task should be deleted with logs\n%s", strings.Join(logs, "\n"))
	}
	if n := len(steManaged(sub)); n != 1 {
		t.Fatalf("want 1 task, got %s", steDump(steManaged(sub)))
	}
}

// 改了 SaveDir：旧目录的文件还在盘上，但不在当前 SaveDir 里——旧任务（含改过命令的）照删，新目录照建。
func TestSyncSubscriptionTasksSaveDirChangeDeletesOldTasks(t *testing.T) {
	steEnv(t)
	sub := steSubscription(t, "ste_old_dir", true, map[string]string{"biz.js": "9 5 * * *"})
	steSync(sub)
	old := findTaskByCommand(t, steCmd("ste_old_dir", "biz.js"))
	steSetCommand(t, old, old.Command+" now")
	steWriteScript(t, "ste_new_dir", "biz.js", "9 5 * * *", "biz")
	sub.SaveDir = "ste_new_dir"

	logs := steSync(sub)
	if _, exists := steReload(old.ID); exists {
		t.Fatalf("old-dir task should be deleted\n%s", strings.Join(logs, "\n"))
	}
	findTaskByCommand(t, steCmd("ste_new_dir", "biz.js"))
	if n := len(steManaged(sub)); n != 1 {
		t.Fatalf("want 1 task, got %s", steDump(steManaged(sub)))
	}
}

// 熔断（E5）：本次一个候选都没有（检出为空）→ 一条不删、日志不动，打 [跳过自动删除]。
func TestSyncSubscriptionTasksEmptyCandidatesSkipsAutoDelete(t *testing.T) {
	steEnv(t)
	saveDir := "ste_empty"
	sub := steSubscription(t, saveDir, true, map[string]string{"biz.js": "9 5 * * *", "other.js": "1 1 * * *"})
	steSync(sub)
	tasks := steManaged(sub)
	for i := range tasks {
		steSetCommand(t, &tasks[i], tasks[i].Command+" now")
		steAddLog(t, tasks[i].ID)
	}
	for _, name := range []string{"biz.js", "other.js"} {
		if err := os.Remove(filepath.Join(config.C.Data.ScriptsDir, saveDir, name)); err != nil {
			t.Fatal(err)
		}
	}

	logs := steSync(sub)
	for _, task := range tasks {
		steAssertUnchanged(t, task, logs)
		if steLogCount(task.ID) != 1 {
			t.Fatalf("task logs of %s were deleted", task.Name)
		}
	}
	if !containsLogLine(logs, "[跳过自动删除]") || containsLogLine(logs, "[自动删除任务]") {
		t.Fatalf("expected the circuit breaker\n%s", strings.Join(logs, "\n"))
	}
}

// E4：单文件订阅 SaveDir 为空时同步扫的是 <ScriptsDir>/<文件名>（口径错位另开 issue），候选恒为空；
// 熔断先堵住误删：托管任务与日志都在。
func TestSyncSubscriptionTasksSingleFileEmptySaveDirSkipsAutoDelete(t *testing.T) {
	steEnv(t)
	sub := &model.Subscription{
		Name: "single", Type: model.SubTypeSingleFile, URL: "https://example.com/raw/x.js", Enabled: true,
		AutoAddTaskMode: model.SubTaskSyncEnabled, AutoDelTaskMode: model.SubTaskSyncEnabled,
	}
	if err := database.DB.Create(sub).Error; err != nil {
		t.Fatal(err)
	}
	dest := singleFileSubscriptionDestPath(config.C.Data.ScriptsDir, sub)
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dest, []byte("//cron: 1 1 * * *\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	task := steNewTask(t, "x", "task "+filepath.Join("downloads", "x.js"), "1 1 * * *", subscriptionTaskLabel(sub.ID))
	steAddLog(t, task.ID)

	logs := steSync(sub)
	steAssertUnchanged(t, *task, logs)
	if steLogCount(task.ID) != 1 || !containsLogLine(logs, "[跳过自动删除]") {
		t.Fatalf("single-file task must survive\n%s", strings.Join(logs, "\n"))
	}
}

// E6d：SaveDir 是 NTFS 目录联接时，扫描只能靠根目录兜底读到平铺的文件，子目录整个漏掉。
// 子目录里的脚本还在、任务也能跑——规范命令与改过参数的都保留，并提示扫描没读到。
func TestSyncSubscriptionTasksJunctionSaveDirKeepsUnscannedTask(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("NTFS 目录联接只在 Windows 上有；读目录异常的同类场景由 TestSubscriptionStaleTaskJudgeKeepsUnscannedFile 覆盖")
	}
	steEnv(t)
	realRepo := filepath.Join(filepath.Dir(config.C.Data.ScriptsDir), "real_repo")
	for _, rel := range []string{"a.js", filepath.Join("sub", "b.js")} {
		full := filepath.Join(realRepo, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("//cron: 1 1 * * *\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	link := filepath.Join(config.C.Data.ScriptsDir, "ste_junction")
	// mklink /J 不需要管理员权限，出错就 Fatalf。
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", link, realRepo).CombinedOutput(); err != nil {
		t.Fatalf("mklink /J %q %q failed: %v (%s)", link, realRepo, err, out)
	}
	sub := steSubscription(t, "ste_junction", true, nil)
	logs := steSync(sub)
	findTaskByCommand(t, steCmd("ste_junction", "a.js"))
	var premise int64
	database.DB.Model(&model.Task{}).Where("command = ?", steCmd("ste_junction", "sub/b.js")).Count(&premise)
	if premise != 0 {
		t.Fatalf("premise broken: the scan walked into the junction, this test no longer covers a partial scan\n%s", strings.Join(logs, "\n"))
	}

	label := subscriptionTaskLabel(sub.ID)
	canonical := steNewTask(t, "b", steCmd("ste_junction", "sub/b.js"), "1 1 * * *", label)
	edited := steNewTask(t, "b 带参数", steCmd("ste_junction", "sub/b.js")+" desi JD_COOKIE", "2 2 * * *", label)
	steAddLog(t, canonical.ID)
	steAddLog(t, edited.ID)

	logs = steSync(sub)
	for _, task := range []*model.Task{canonical, edited} {
		steAssertUnchanged(t, *task, logs)
		if steLogCount(task.ID) != 1 {
			t.Fatalf("logs of %s were deleted", task.Name)
		}
	}
	if steCountLines(logs, "本次扫描没有读到") != 2 || !containsLogLine(logs, "ste_junction/sub/b.js") || containsLogLine(logs, "[自动删除任务]") {
		t.Fatalf("expected two keep hints and no delete\n%s", strings.Join(logs, "\n"))
	}
}

// review-r1 F2 的真实场景：Docker 按 PUID 降权运行、NFS root_squash 时，子目录可能暂时既不能列、也不能进入。
// 脚本还在盘上，用户改过命令的任务与历史日志都要保留：0311（能进不能列）走「扫描漏读」的保留；
// 0000（进不去，Stat 报 permission denied，证明不了文件不在）走「无法确认」的保留。权限恢复后还是这一条。
// 只在非 root 的 Linux 上有意义：root 绕过权限位，Windows 没有这套权限位。
func TestSyncSubscriptionTasksUnreadableSubdirKeepsTask(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("目录权限位只在 Unix 上有；Stat 报错的判定由 TestSubscriptionStaleTaskJudgeStatErrorIsNotAbsence 覆盖")
	}
	if os.Geteuid() == 0 {
		t.Skip("root 绕过目录权限位；在 WSL 里用 setpriv --reuid=65534 以非 root 身份跑")
	}
	for _, tc := range []struct {
		name string
		mode os.FileMode
		hint string
	}{
		{"mode_0311", 0o311, "本次扫描没有读到"},
		{"mode_0000", 0o000, "无法确认脚本 ste_perm/sub/b.js 是否还在（permission denied）"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			steEnv(t)
			saveDir := "ste_perm"
			sub := steSubscription(t, saveDir, true, map[string]string{"a.js": "1 1 * * *", "sub/b.js": "2 2 * * *"})
			steSync(sub)
			task := findTaskByCommand(t, steCmd(saveDir, "sub/b.js"))
			steSetCommand(t, task, task.Command+" now")
			steAddLog(t, task.ID)
			want := *task
			subDir := filepath.Join(config.C.Data.ScriptsDir, saveDir, "sub")
			if err := os.Chmod(subDir, tc.mode); err != nil {
				t.Fatal(err)
			}
			// 晚于 SetupTestEnv 注册，先于临时目录清理执行：否则 0000 的目录删不掉。
			t.Cleanup(func() { _ = os.Chmod(subDir, 0o755) })

			logs := steSync(sub)
			steAssertUnchanged(t, want, logs)
			if steLogCount(task.ID) != 1 || containsLogLine(logs, "[自动删除任务]") || !containsLogLine(logs, tc.hint) {
				t.Fatalf("task and its log must be kept with hint %q\n%s", tc.hint, strings.Join(logs, "\n"))
			}

			if err := os.Chmod(subDir, 0o755); err != nil {
				t.Fatal(err)
			}
			logs = steSync(sub)
			steAssertUnchanged(t, want, logs)
			steAssertNoAddDelete(t, logs)
			if steLogCount(task.ID) != 1 {
				t.Fatalf("task logs changed after permissions were restored\n%s", strings.Join(logs, "\n"))
			}
		})
	}
}

// Docker 青龙兼容层的别名绝对路径（/ql/data/scripts → 脚本目录）：认得出，不新建、不删除。
func TestSyncSubscriptionTasksAliasAbsolutePathIsRecognised(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Docker 青龙别名是 Linux 软链接场景，在 WSL 里跑交叉编译的测试二进制覆盖")
	}
	steEnv(t)
	saveDir := "ste_alias"
	sub := steSubscription(t, saveDir, true, map[string]string{"a.js": "1 1 * * *"})
	steSync(sub)
	alias := filepath.Join(filepath.Dir(config.C.Data.ScriptsDir), "qlalias")
	if err := os.Symlink(config.C.Data.ScriptsDir, alias); err != nil {
		t.Fatalf("symlink alias: %v", err)
	}
	task := findTaskByCommand(t, steCmd(saveDir, "a.js"))
	steSetCommand(t, task, "task "+filepath.Join(alias, saveDir, "a.js")+" desi JD_COOKIE")
	steAddLog(t, task.ID)
	want := *task

	for round := 1; round <= 2; round++ {
		logs := steSync(sub)
		steAssertUnchanged(t, want, logs)
		steAssertNoAddDelete(t, logs)
		if n := len(steManaged(sub)); n != 1 || steLogCount(task.ID) != 1 {
			t.Fatalf("round %d: want the single alias task with its log, got %s", round, steDump(steManaged(sub)))
		}
	}
}

// 大小写与分隔符跟随执行器：Windows 上大写路径、正斜杠都认得出；Linux 上大写认不出——
// 新建规范任务，旧任务按删除规则处理（文件不存在 → 删），与改动前一致。
func TestSyncSubscriptionTasksCaseAndSeparator(t *testing.T) {
	if runtime.GOOS == "windows" {
		edits := []struct {
			name string
			edit func(saveDir string) string
		}{
			{"upper", func(d string) string { return "task " + strings.ToUpper(filepath.Join(d, "biz.js")) + " now" }},
			{"forward_slash", func(d string) string { return "task " + d + "/biz.js desi JD_COOKIE" }},
			{"mixed", func(d string) string { return "task " + strings.ToUpper(d) + "/Biz.js now" }},
		}
		for _, tc := range edits {
			t.Run(tc.name, func(t *testing.T) {
				steEnv(t)
				saveDir := "ste_case"
				sub := steSubscription(t, saveDir, true, map[string]string{"biz.js": "9 5 * * *"})
				steSync(sub)
				task := findTaskByCommand(t, steCmd(saveDir, "biz.js"))
				steSetCommand(t, task, tc.edit(saveDir))
				steAddLog(t, task.ID)
				want := *task
				for round := 1; round <= 2; round++ {
					logs := steSync(sub)
					steAssertUnchanged(t, want, logs)
					steAssertNoAddDelete(t, logs)
					if n := len(steManaged(sub)); n != 1 {
						t.Fatalf("round %d: want 1 task, got %s", round, steDump(steManaged(sub)))
					}
				}
			})
		}
		return
	}

	steEnv(t)
	saveDir := "ste_case"
	sub := steSubscription(t, saveDir, true, map[string]string{"biz.js": "9 5 * * *"})
	steSync(sub)
	old := findTaskByCommand(t, steCmd(saveDir, "biz.js"))
	steSetCommand(t, old, "task "+strings.ToUpper(filepath.Join(saveDir, "biz.js"))+" now")
	steAddLog(t, old.ID)

	logs := steSync(sub)
	if _, exists := steReload(old.ID); exists || steLogCount(old.ID) != 0 {
		t.Fatalf("linux: an upper-case path does not run, the old task is deleted as before\n%s", strings.Join(logs, "\n"))
	}
	findTaskByCommand(t, steCmd(saveDir, "biz.js"))
	if n := len(steManaged(sub)); n != 1 || !containsLogLine(logs, "[自动添加任务]") || !containsLogLine(logs, "[自动删除任务]") {
		t.Fatalf("linux: want recreate + delete, got %s\n%s", steDump(steManaged(sub)), strings.Join(logs, "\n"))
	}
}

// 接管：订阅之前用户自建的、带参数的任务（没有标签）→ 只加标签，字段不变，不新建。
func TestSyncSubscriptionTasksAdoptsUnlabeledTaskWithArgs(t *testing.T) {
	steEnv(t)
	saveDir := "ste_adopt_args"
	sub := steSubscription(t, saveDir, true, map[string]string{"biz.js": "9 5 * * *"})
	own := steNewTask(t, "我的任务", steCmd(saveDir, "biz.js")+" desi JD_COOKIE", "45 21 * * *")
	want := *own

	logs := steSync(sub)
	got, _ := steReload(own.ID)
	if !hasLabel(got.GetLabels(), subscriptionTaskLabel(sub.ID)) {
		t.Fatalf("task should be adopted, labels=%q\n%s", got.Labels, strings.Join(logs, "\n"))
	}
	want.Labels = got.Labels
	steAssertUnchanged(t, want, logs)
	if n := len(steManaged(sub)); n != 1 || containsLogLine(logs, "[自动添加任务]") || !containsLogLine(logs, "[关联已有任务] 我的任务") {
		t.Fatalf("want adopt without create, got %s\n%s", steDump(steManaged(sub)), strings.Join(logs, "\n"))
	}
	if logs := steSync(sub); !containsLogLine(logs, "[同步完成] 本次未对定时任务做任何变更") {
		t.Fatalf("second round should be a no-op\n%s", strings.Join(logs, "\n"))
	}
}

// 接管：删订阅再重建，任务身上留着悬空的旧订阅标签——带参数的也要被接管（E9），旧标签原样保留。
func TestSyncSubscriptionTasksAdoptsTaskWithDanglingLabel(t *testing.T) {
	steEnv(t)
	saveDir := "ste_adopt_dangling"
	sub := steSubscription(t, saveDir, true, map[string]string{"biz.js": "9 5 * * *"})
	orphan := steNewTask(t, "旧订阅留下的", steCmd(saveDir, "biz.js")+" now", "10 10 * * *", "subscription:999")
	want := *orphan

	logs := steSync(sub)
	got, _ := steReload(orphan.ID)
	labels := got.GetLabels()
	if !hasLabel(labels, subscriptionTaskLabel(sub.ID)) || !hasLabel(labels, "subscription:999") {
		t.Fatalf("want both labels, got %q\n%s", got.Labels, strings.Join(logs, "\n"))
	}
	want.Labels = got.Labels
	steAssertUnchanged(t, want, logs)
	if n := len(steManaged(sub)); n != 1 || containsLogLine(logs, "[自动添加任务]") {
		t.Fatalf("want adopt without create, got %s\n%s", steDump(steManaged(sub)), strings.Join(logs, "\n"))
	}
}

// 接管：同一脚本有多条未托管任务（原样命令一条、带参数一条）→ 都接管，每条一行 [关联已有任务]。
func TestSyncSubscriptionTasksAdoptsEveryMatchingTask(t *testing.T) {
	steEnv(t)
	saveDir := "ste_adopt_many"
	sub := steSubscription(t, saveDir, true, map[string]string{"biz.js": "9 5 * * *"})
	exact := steNewTask(t, "原样", steCmd(saveDir, "biz.js"), "45 21 * * *")
	withArgs := steNewTask(t, "带参数", steCmd(saveDir, "biz.js")+" desi JD_COOKIE", "46 21 * * *")

	logs := steSync(sub)
	for _, task := range []*model.Task{exact, withArgs} {
		got, _ := steReload(task.ID)
		if !hasLabel(got.GetLabels(), subscriptionTaskLabel(sub.ID)) {
			t.Fatalf("%s should be adopted\n%s", task.Name, strings.Join(logs, "\n"))
		}
		want := *task
		want.Labels = got.Labels
		steAssertUnchanged(t, want, logs)
	}
	if steCountLines(logs, "[关联已有任务]") != 2 || !containsLogLine(logs, "[共关联 2 个已有任务]") || containsLogLine(logs, "[自动添加任务]") {
		t.Fatalf("want two adopt lines and no create\n%s", strings.Join(logs, "\n"))
	}
}

// 本订阅已有同脚本的托管任务（哪怕改过命令）时，不再接管用户自建的同脚本任务，也不新建。
func TestSyncSubscriptionTasksManagedTaskTakesPrecedenceOverAdopt(t *testing.T) {
	steEnv(t)
	saveDir := "ste_adopt_precedence"
	sub := steSubscription(t, saveDir, true, map[string]string{"biz.js": "9 5 * * *"})
	steSync(sub)
	managed := findTaskByCommand(t, steCmd(saveDir, "biz.js"))
	steSetCommand(t, managed, managed.Command+" now")
	own := steNewTask(t, "我自己建的", steCmd(saveDir, "biz.js"), "45 21 * * *")
	wantManaged, wantOwn := *managed, *own

	logs := steSync(sub)
	steAssertUnchanged(t, wantManaged, logs)
	steAssertUnchanged(t, wantOwn, logs)
	steAssertNoAddDelete(t, logs)
	if containsLogLine(logs, "[关联已有任务]") {
		t.Fatalf("own task must not be adopted\n%s", strings.Join(logs, "\n"))
	}
}

// node X 这类解释器命令：算已存在（见 EditedCommandKeepsSingleTask 的 node 子用例），
// 脚本从订阅里消失时也不在自动删除范围里（只看 task 开头的命令）。
func TestSyncSubscriptionTasksInterpreterCommandIsNeverAutoDeleted(t *testing.T) {
	steEnv(t)
	saveDir := "ste_node_gone"
	sub := steSubscription(t, saveDir, true, map[string]string{"keep.js": "1 1 * * *", "gone.js": "2 2 * * *"})
	steSync(sub)
	task := findTaskByCommand(t, steCmd(saveDir, "gone.js"))
	steSetCommand(t, task, "node "+filepath.Join(saveDir, "gone.js"))
	steAddLog(t, task.ID)
	if err := os.Remove(filepath.Join(config.C.Data.ScriptsDir, saveDir, "gone.js")); err != nil {
		t.Fatal(err)
	}

	logs := steSync(sub)
	steAssertUnchanged(t, *task, logs)
	if steLogCount(task.ID) != 1 || containsLogLine(logs, "[自动删除任务]") {
		t.Fatalf("interpreter commands are out of auto-delete scope\n%s", strings.Join(logs, "\n"))
	}
}

// 条件删除：快照之后命令被改过 → 不删、日志还在；快照与库一致 → 连日志删掉。
func TestDeleteSubscriptionTaskIfUnchangedSkipsEditedTask(t *testing.T) {
	steEnv(t)
	task := steNewTask(t, "stale", "task repo/gone.js", "0 3 * * *", "subscription:1")
	steAddLog(t, task.ID)
	snapshot := *task
	steSetCommand(t, task, "task repo/gone.js now")

	removed, err := deleteSubscriptionTaskIfUnchanged(&snapshot)
	if err != nil || removed {
		t.Fatalf("stale snapshot must not delete, got removed=%v err=%v", removed, err)
	}
	if _, exists := steReload(task.ID); !exists || steLogCount(task.ID) != 1 {
		t.Fatal("task and its logs must survive a stale snapshot")
	}

	fresh, _ := steReload(task.ID)
	removed, err = deleteSubscriptionTaskIfUnchanged(&fresh)
	if err != nil || !removed {
		t.Fatalf("up-to-date snapshot should delete, got removed=%v err=%v", removed, err)
	}
	if _, exists := steReload(task.ID); exists || steLogCount(task.ID) != 0 {
		t.Fatal("task and its logs should be gone")
	}
}

// 删任务那一步失败：历史日志不能先没了（原来的顺序是先删日志、且不查错误，E7）。
func TestDeleteSubscriptionTaskIfUnchangedKeepsLogsWhenTaskDeleteFails(t *testing.T) {
	steEnv(t)
	task := steNewTask(t, "blocked", "task repo/gone.js", "0 3 * * *", "subscription:1")
	steAddLog(t, task.ID)
	steBlockTaskDeletes(t)

	removed, err := deleteSubscriptionTaskIfUnchanged(task)
	if err == nil || removed {
		t.Fatalf("want an error, got removed=%v err=%v", removed, err)
	}
	if _, exists := steReload(task.ID); !exists || steLogCount(task.ID) != 1 {
		t.Fatal("task and its logs must both survive a failed delete")
	}
}

// 同步里删任务失败：计入失败并打日志，任务与日志都在。
func TestSyncSubscriptionTasksReportsFailedAutoDelete(t *testing.T) {
	steEnv(t)
	saveDir := "ste_delete_fail"
	sub := steSubscription(t, saveDir, true, map[string]string{"keep.js": "1 1 * * *"})
	steSync(sub)
	orphan := steNewTask(t, "orphan", steCmd(saveDir, "gone.js"), "0 3 * * *", subscriptionTaskLabel(sub.ID))
	steAddLog(t, orphan.ID)
	steBlockTaskDeletes(t)

	logs := steSync(sub)
	if _, exists := steReload(orphan.ID); !exists || steLogCount(orphan.ID) != 1 {
		t.Fatalf("orphan and its logs must survive\n%s", strings.Join(logs, "\n"))
	}
	if !containsLogLine(logs, "[自动删除任务失败] orphan") || !containsLogLine(logs, "[警告] 共 1 个任务操作失败") ||
		containsLogLine(logs, "[自动删除任务] orphan") {
		t.Fatalf("want a failure line\n%s", strings.Join(logs, "\n"))
	}
}

// ---- 原 subscription_task_lock_test.go 的 5 条用例，按 #125 新契约改写（订阅锁已下线）----

// 原 TestSyncSubscriptionTasksKeepsManualCronWhenLocked：用户手改 cron（原先靠加锁）→ 同步后仍是用户的值。
// 现在不需要锁：同步从不改已有任务，也不再有「保留手动定时」这类日志。
func TestSyncSubscriptionTasksKeepsManualCron(t *testing.T) {
	steEnv(t)
	saveDir := "locked_cron_repo"
	sub := steSubscription(t, saveDir, true, map[string]string{"biz.js": "9 5 * * *"})
	steSync(sub)
	command := steCmd(saveDir, "biz.js")
	task := findTaskByCommand(t, command)
	if task.CronExpression != "9 5 * * *" {
		t.Fatalf("首次同步应使用订阅源 cron，got %q", task.CronExpression)
	}
	if err := database.DB.Model(task).Update("cron_expression", "30 7 * * *").Error; err != nil {
		t.Fatal(err)
	}

	logs := steSync(sub)
	if after := findTaskByCommand(t, command); after.CronExpression != "30 7 * * *" {
		t.Fatalf("订阅同步覆盖了用户手改的 cron：got %q", after.CronExpression)
	}
	if containsLogLine(logs, "保留手动定时") || containsLogLine(logs, "[自动更新任务]") {
		t.Fatalf("no lock or update lines expected\n%s", strings.Join(logs, "\n"))
	}
}

// 原 TestSyncSubscriptionTasksKeepsManualNameWhenLocked：手改任务名 → 同步后仍是用户的值。
func TestSyncSubscriptionTasksKeepsManualName(t *testing.T) {
	steEnv(t)
	saveDir := "locked_name_repo"
	sub := steSubscription(t, saveDir, true, map[string]string{"biz.js": "9 5 * * *"})
	steSync(sub)
	command := steCmd(saveDir, "biz.js")
	task := findTaskByCommand(t, command)
	if err := database.DB.Model(task).Update("name", "我自己改的名字").Error; err != nil {
		t.Fatal(err)
	}

	steSync(sub)
	if after := findTaskByCommand(t, command); after.Name != "我自己改的名字" {
		t.Fatalf("订阅同步覆盖了用户手改的名称：got %q", after.Name)
	}
}

// 原 TestSyncSubscriptionTasksKeepsLockedTaskMissingFromCandidates：原断言「加了锁的失效任务连日志保留」。
// 锁下线后没有这条例外：订阅源里已经没有的脚本，任务按自动删除开关连日志删掉（发布说明写明）。
func TestSyncSubscriptionTasksDeletesStaleTaskEvenIfManuallyEdited(t *testing.T) {
	steEnv(t)
	saveDir := "locked_delete_repo"
	sub := steSubscription(t, saveDir, true, map[string]string{"biz.js": "9 5 * * *"})
	orphan := steNewTask(t, "上游已删除的脚本", steCmd(saveDir, "gone.js"), "0 3 * * *", subscriptionTaskLabel(sub.ID))
	steAddLog(t, orphan.ID)

	logs := steSync(sub)
	if _, exists := steReload(orphan.ID); exists || steLogCount(orphan.ID) != 0 {
		t.Fatalf("失效任务应连日志被自动删除\n%s", strings.Join(logs, "\n"))
	}
	if !containsLogLine(logs, "[自动删除任务] 上游已删除的脚本") || containsLogLine(logs, "已加锁") {
		t.Fatalf("unexpected logs\n%s", strings.Join(logs, "\n"))
	}
}

// 原 TestSyncSubscriptionTasksUnlockedBehaviourUnchanged：
// ① 原断言「用户改了 cron 但没加锁 → 订阅源的值覆盖回来（9 5 * * *）」，现改为「保持用户的 30 7 * * *」；
// ② 不在候选集里的任务连历史日志一起删——不变。
func TestSyncSubscriptionTasksKeepsEditedCronAndDeletesOrphan(t *testing.T) {
	steEnv(t)
	saveDir := "unlocked_repo"
	sub := steSubscription(t, saveDir, true, map[string]string{"biz.js": "9 5 * * *"})
	steSync(sub)
	command := steCmd(saveDir, "biz.js")
	task := findTaskByCommand(t, command)
	if err := database.DB.Model(task).Update("cron_expression", "30 7 * * *").Error; err != nil {
		t.Fatal(err)
	}
	orphan := steNewTask(t, "上游已删除的脚本", steCmd(saveDir, "gone.js"), "0 3 * * *", subscriptionTaskLabel(sub.ID))
	steAddLog(t, orphan.ID)

	steSync(sub)
	if after := findTaskByCommand(t, command); after.CronExpression != "30 7 * * *" {
		t.Fatalf("已有任务不应再跟随订阅源 cron：got %q", after.CronExpression)
	}
	if _, exists := steReload(orphan.ID); exists || steLogCount(orphan.ID) != 0 {
		t.Fatal("失效任务应连日志一起删除")
	}
}

// 原 TestSyncSubscriptionTasksAdoptLocksExistingTask：原断言「接管时直接加锁」，现改为「只加订阅标签，
// 名称与定时不变」；再同步一次也不被覆盖。
func TestSyncSubscriptionTasksAdoptKeepsExistingTaskFields(t *testing.T) {
	steEnv(t)
	saveDir := "adopt_repo"
	sub := steSubscription(t, saveDir, true, map[string]string{"biz.js": "9 5 * * *"})
	command := steCmd(saveDir, "biz.js")
	own := steNewTask(t, "我自己建的", command, "45 21 * * *")

	logs := steSync(sub)
	adopted := findTaskByCommand(t, command)
	if adopted.ID != own.ID || !hasLabel(adopted.GetLabels(), subscriptionTaskLabel(sub.ID)) {
		t.Fatalf("用户自建任务应被接管（加订阅标签）\n%s", strings.Join(logs, "\n"))
	}
	if !containsLogLine(logs, "[关联已有任务] 我自己建的") {
		t.Fatalf("missing adopt line\n%s", strings.Join(logs, "\n"))
	}

	steSync(sub)
	if after := findTaskByCommand(t, command); after.CronExpression != "45 21 * * *" || after.Name != "我自己建的" {
		t.Fatalf("接管后的任务被覆盖了：name=%q cron=%q", after.Name, after.CronExpression)
	}
	if n := len(steManaged(sub)); n != 1 {
		t.Fatalf("want 1 task, got %s", steDump(steManaged(sub)))
	}
}
