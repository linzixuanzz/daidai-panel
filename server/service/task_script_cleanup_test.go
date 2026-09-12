package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"daidai-panel/config"
	"daidai-panel/database"
	"daidai-panel/model"
	"daidai-panel/testutil"
)

// #124「删除任务时可选同时删除脚本」：判定（各 reason、优先级、warnings）与执行（收窄、复核、并发）。

func tscSetup(t *testing.T) TaskScriptCleanupEnv {
	t.Helper()
	testutil.SetupTestEnv(t)
	return TaskScriptCleanupEnv{ScriptsDir: config.C.Data.ScriptsDir}
}

func tscWriteFile(t *testing.T, rel, content string) string {
	t.Helper()
	full := filepath.Join(config.C.Data.ScriptsDir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir for %s: %v", rel, err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
	return full
}

func tscMkdir(t *testing.T, rel string) string {
	t.Helper()
	full := filepath.Join(config.C.Data.ScriptsDir, filepath.FromSlash(rel))
	if err := os.MkdirAll(full, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", rel, err)
	}
	return full
}

func tscCreateTask(t *testing.T, name, command string, mutate ...func(*model.Task)) *model.Task {
	t.Helper()
	task := &model.Task{
		Name:           name,
		Command:        command,
		CronExpression: "0 0 * * *",
		TaskType:       model.TaskTypeCron,
		Status:         model.TaskStatusEnabled,
	}
	for _, fn := range mutate {
		fn(task)
	}
	if err := database.DB.Select("*").Create(task).Error; err != nil {
		t.Fatalf("create task %s: %v", name, err)
	}
	return task
}

// tscCreateSubscription 用 Select("*") 落库，Enabled=false 这类零值才能真的写进去（否则会被 default:true 顶掉）。
func tscCreateSubscription(t *testing.T, sub model.Subscription) *model.Subscription {
	t.Helper()
	if sub.URL == "" {
		sub.URL = "https://example.com/" + sub.Name + ".git"
	}
	if err := database.DB.Select("*").Create(&sub).Error; err != nil {
		t.Fatalf("create subscription %s: %v", sub.Name, err)
	}
	return &sub
}

func tscDeleteTaskRows(t *testing.T, ids ...uint) {
	t.Helper()
	for _, id := range ids {
		if err := database.DB.Where("id = ?", id).Delete(&model.Task{}).Error; err != nil {
			t.Fatalf("delete task row %d: %v", id, err)
		}
	}
}

func tscItem(t *testing.T, items []TaskScriptItem, path string) TaskScriptItem {
	t.Helper()
	for _, item := range items {
		if item.Path == path {
			return item
		}
	}
	t.Fatalf("script item %q not found in %#v", path, items)
	return TaskScriptItem{}
}

func tscTaskInfo(t *testing.T, infos []TaskScriptTaskInfo, id uint) TaskScriptTaskInfo {
	t.Helper()
	for _, info := range infos {
		if info.ID == id {
			return info
		}
	}
	t.Fatalf("task info %d not found in %#v", id, infos)
	return TaskScriptTaskInfo{}
}

func tscAssertKept(t *testing.T, item TaskScriptItem, reason string) {
	t.Helper()
	if item.Deletable || item.Reason != reason {
		t.Fatalf("expected %s to be kept with reason %q, got deletable=%v reason=%q detail=%q", item.Path, reason, item.Deletable, item.Reason, item.Detail)
	}
}

func tscAssertDeletable(t *testing.T, item TaskScriptItem) {
	t.Helper()
	if !item.Deletable || item.Reason != "" || item.Detail != "" {
		t.Fatalf("expected %s to be deletable, got deletable=%v reason=%q detail=%q", item.Path, item.Deletable, item.Reason, item.Detail)
	}
}

func tscAssertExists(t *testing.T, full string) {
	t.Helper()
	if _, err := os.Lstat(full); err != nil {
		t.Fatalf("expected %s to still exist: %v", full, err)
	}
}

func tscAssertGone(t *testing.T, full string) {
	t.Helper()
	if _, err := os.Lstat(full); !os.IsNotExist(err) {
		t.Fatalf("expected %s to be removed, got err=%v", full, err)
	}
}

// ---------- 预览：合并与共用 ----------

// 同一个脚本只被本次选中的多个任务引用：合并成一项、task_ids 全部列出、可以删除。
func TestTaskScriptPreviewMergesRequestedTasks(t *testing.T) {
	env := tscSetup(t)
	tscWriteFile(t, "a.py", "x")
	first := tscCreateTask(t, "first", "task a.py")
	second := tscCreateTask(t, "second", "python3 ./a.py")

	preview := PreviewTaskScriptDeletion([]uint{first.ID, second.ID}, env)
	if !preview.Checked || len(preview.Scripts) != 1 {
		t.Fatalf("expected one merged script, got %#v", preview)
	}
	item := preview.Scripts[0]
	tscAssertDeletable(t, item)
	if item.Path != "a.py" || len(item.TaskIDs) != 2 || item.TaskIDs[0] != first.ID || item.TaskIDs[1] != second.ID {
		t.Fatalf("unexpected merged item: %#v", item)
	}
	for _, id := range []uint{first.ID, second.ID} {
		info := tscTaskInfo(t, preview.Tasks, id)
		if info.ScriptStatus != "resolved" || info.ScriptPath != "a.py" || info.Note != "" {
			t.Fatalf("unexpected task info: %#v", info)
		}
	}
}

// 范围外任务以各种写法引用同一文件都算共用；跑不起来 / 托管命令按文本匹配；子目录同名文件不算。
func TestTaskScriptPreviewSharedDetection(t *testing.T) {
	env := tscSetup(t)
	tscWriteFile(t, "a.py", "x")
	tscWriteFile(t, "sub/a.py", "x")
	base, err := resolveScriptsBase(env.ScriptsDir)
	if err != nil {
		t.Fatalf("resolve base: %v", err)
	}

	requested := tscCreateTask(t, "req", "task a.py")
	direct := map[uint]bool{}
	for i, command := range []string{"task ./a.py", "python3 a.py", "task " + filepath.ToSlash(filepath.Join(base.Real, "a.py"))} {
		direct[tscCreateTask(t, fmt.Sprintf("direct-%d", i), command).ID] = true
	}
	disabled := tscCreateTask(t, "disabled", "task a.py", func(task *model.Task) { task.Status = model.TaskStatusDisabled })
	direct[disabled.ID] = true
	if runtime.GOOS == "windows" {
		direct[tscCreateTask(t, "upper", "task A.PY").ID] = true
	}
	textMatch := map[uint]bool{
		tscCreateTask(t, "broken", "task a.py now extra").ID: true,
		tscCreateTask(t, "managed", "python3.13 a.py").ID:    true,
	}
	unrelated := tscCreateTask(t, "sub", "task sub/a.py")

	preview := PreviewTaskScriptDeletion([]uint{requested.ID}, env)
	item := tscItem(t, preview.Scripts, "a.py")
	tscAssertKept(t, item, TaskScriptReasonShared)
	if len(item.SharedBy) != len(direct)+len(textMatch) {
		t.Fatalf("expected %d shared refs, got %#v", len(direct)+len(textMatch), item.SharedBy)
	}
	for _, ref := range item.SharedBy {
		switch {
		case ref.ID == unrelated.ID:
			t.Fatalf("task sub/a.py must not be counted as sharing a.py")
		case direct[ref.ID] && ref.TextMatch:
			t.Fatalf("direct reference %d must have text_match=false", ref.ID)
		case textMatch[ref.ID] && !ref.TextMatch:
			t.Fatalf("text reference %d must have text_match=true", ref.ID)
		case !direct[ref.ID] && !textMatch[ref.ID]:
			t.Fatalf("unexpected shared ref %#v", ref)
		}
	}
	wantPrefix := fmt.Sprintf("仍被 %d 个其他任务使用：", len(item.SharedBy))
	if !strings.HasPrefix(item.Detail, wantPrefix) || !strings.HasSuffix(item.Detail, " 等，删除后它们会无法运行。") {
		t.Fatalf("unexpected shared detail: %q", item.Detail)
	}
}

func TestTaskScriptPreviewSharedDetailText(t *testing.T) {
	env := tscSetup(t)
	tscWriteFile(t, "a.py", "x")
	requested := tscCreateTask(t, "A", "task a.py")
	tscCreateTask(t, "A (副本)", "task a.py")

	// 共用方只有 1 个：代词用「它」。
	item := tscItem(t, PreviewTaskScriptDeletion([]uint{requested.ID}, env).Scripts, "a.py")
	if item.Detail != "仍被 1 个其他任务使用：A (副本)，删除后它会无法运行。" {
		t.Fatalf("unexpected detail: %q", item.Detail)
	}

	// 共用方有 2 个：代词用「它们」。
	tscCreateTask(t, "坏命令", "task a.py now extra")
	item = tscItem(t, PreviewTaskScriptDeletion([]uint{requested.ID}, env).Scripts, "a.py")
	if item.Detail != "仍被 2 个其他任务使用：A (副本)、坏命令（按命令文本匹配），删除后它们会无法运行。" {
		t.Fatalf("unexpected detail: %q", item.Detail)
	}
}

// 查「其他任务」禁止用 SQL 的 NOT IN：空集合或 nil 时 GORM 生成 NOT IN (NULL)，查出 0 行，
// 共用判定静默失效直接误删。行为与源码两层一起钉住。
func TestCollectOtherTasksNeverUsesNotIn(t *testing.T) {
	tscSetup(t)
	first := tscCreateTask(t, "first", "task a.py")
	tscCreateTask(t, "second", "task b.py")
	tscCreateTask(t, "disabled", "echo ok", func(task *model.Task) { task.Status = model.TaskStatusDisabled })

	for name, scope := range map[string]map[uint]bool{"nil": nil, "empty": {}} {
		others, remaining, err := loadScriptCleanupTasks(scope)
		if err != nil {
			t.Fatalf("%s scope: load tasks: %v", name, err)
		}
		if len(others) != 3 || len(remaining) != 0 {
			t.Fatalf("%s scope must keep the full task table as others (NOT IN (NULL) trap), got others=%d remaining=%d", name, len(others), len(remaining))
		}
	}

	others, remaining, err := loadScriptCleanupTasks(map[uint]bool{first.ID: true})
	if err != nil {
		t.Fatalf("load tasks: %v", err)
	}
	if len(others) != 2 || len(remaining) != 1 || remaining[0].ID != first.ID {
		t.Fatalf("expected the scoped task to be split out, got others=%d remaining=%#v", len(others), remaining)
	}

	// 源码层面再钉一次：这两个文件的字符串字面量里不许出现 NOT IN（注释里讲原因不算）。
	for _, file := range []string{"task_script_cleanup.go", "task_script_target.go"} {
		fset := token.NewFileSet()
		parsed, err := parser.ParseFile(fset, file, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", file, err)
		}
		ast.Inspect(parsed, func(node ast.Node) bool {
			if lit, ok := node.(*ast.BasicLit); ok && lit.Kind == token.STRING && strings.Contains(strings.ToUpper(lit.Value), "NOT IN") {
				t.Errorf("%s: string literal at %s must not use NOT IN: %s", file, fset.Position(lit.Pos()), lit.Value)
			}
			return true
		})
	}
}

// python -m 运行同一脚本的其他任务算共用（A3）：python3 -m a → a.py、python3 -m jd.sign → jd/sign.py。
func TestTaskScriptPreviewModuleSharedDetection(t *testing.T) {
	env := tscSetup(t)
	tscWriteFile(t, "a.py", "x")
	tscWriteFile(t, "jd/sign.py", "x")

	cases := []struct {
		script  string
		command string
	}{
		{"a.py", "python3 -m a"},
		{"jd/sign.py", "python3 -m jd.sign"},
	}
	for _, tc := range cases {
		t.Run(tc.script, func(t *testing.T) {
			env := env
			requested := tscCreateTask(t, "req-"+tc.script, "task "+tc.script)
			other := tscCreateTask(t, "mod-"+tc.script, tc.command)

			item := tscItem(t, PreviewTaskScriptDeletion([]uint{requested.ID}, env).Scripts, tc.script)
			tscAssertKept(t, item, TaskScriptReasonShared)
			if len(item.SharedBy) != 1 || item.SharedBy[0].ID != other.ID || !item.SharedBy[0].TextMatch {
				t.Fatalf("expected %s to be shared via python -m (text_match), got %#v", tc.script, item.SharedBy)
			}

			// 执行阶段也必须保留：模块任务还在正常运行，删了它引用的文件会坏掉。
			cleanup := CollectTaskScriptTargets([]uint{requested.ID}, env)
			tscDeleteTaskRows(t, requested.ID)
			if result := cleanup.Execute(nil); len(result.Deleted) != 0 {
				t.Fatalf("module-shared script must not be deleted, got %#v", result.Deleted)
			}
		})
	}
}

// 跑不起来的其他任务也按命令文本算共用（A4）。triage 点名的 python3 -u a.py、task a.py now b.py、
// node --flag a.js、bash -x a.sh 都解析成 not_found；python3 -u a.py ../x.py 因最后一个候选含 .. 解析成
// outside_scripts_dir（R2-6）。它们的文本都指向对应的脚本，一律算共用且 text_match=true。
func TestTaskScriptPreviewBrokenCommandSharedDetection(t *testing.T) {
	env := tscSetup(t)
	cases := []struct {
		script     string
		command    string
		wantStatus string
	}{
		{"a.py", "python3 -u a.py", taskScriptStatusNotFound},
		{"a.py", "task a.py now b.py", taskScriptStatusNotFound},
		{"a.py", "python3 -u a.py ../x.py", taskScriptStatusOutside},
		{"a.js", "node --flag a.js", taskScriptStatusNotFound},
		{"a.sh", "bash -x a.sh", taskScriptStatusNotFound},
	}

	var requestedIDs []uint
	wantRefs := map[string]map[uint]bool{}
	for _, tc := range cases {
		if wantRefs[tc.script] != nil {
			continue
		}
		wantRefs[tc.script] = map[uint]bool{}
		tscWriteFile(t, tc.script, "x")
		requestedIDs = append(requestedIDs, tscCreateTask(t, "req-"+tc.script, "task "+tc.script).ID)
	}
	otherIDs := make([]uint, len(cases))
	for i, tc := range cases {
		otherIDs[i] = tscCreateTask(t, tc.command, tc.command).ID
		wantRefs[tc.script][otherIDs[i]] = true
	}
	// 反例：文本指向别的文件，不算共用。
	tscWriteFile(t, "c.py", "x")
	tscCreateTask(t, "unrelated", "python3 -u c.py")

	// 先钉住这些其他任务各自的解析分类：outside 那条若退化成 not_found，就兜不住「outside 也按文本算共用」这条分支了。
	statuses := PreviewTaskScriptDeletion(otherIDs, env).Tasks
	for i, tc := range cases {
		if got := tscTaskInfo(t, statuses, otherIDs[i]).ScriptStatus; got != tc.wantStatus {
			t.Fatalf("%s: expected script_status %q, got %q", tc.command, tc.wantStatus, got)
		}
	}

	preview := PreviewTaskScriptDeletion(requestedIDs, env)
	for script, refs := range wantRefs {
		item := tscItem(t, preview.Scripts, script)
		tscAssertKept(t, item, TaskScriptReasonShared)
		if len(item.SharedBy) != len(refs) {
			t.Fatalf("%s: expected %d shared refs, got %#v", script, len(refs), item.SharedBy)
		}
		for _, ref := range item.SharedBy {
			if !refs[ref.ID] || !ref.TextMatch {
				t.Fatalf("%s: unexpected shared ref %#v", script, ref)
			}
		}
	}
}

// 脚本目录根下的全局钩子 task_before.sh / task_after.sh / extra.sh 一律保留（A2，managed_helper）；
// 子目录下的同名文件照常可删。
func TestTaskScriptPreviewGlobalHooksKept(t *testing.T) {
	env := tscSetup(t)
	roots := map[string]string{}
	for _, name := range []string{"task_before.sh", "task_after.sh", "extra.sh"} {
		roots[name] = tscWriteFile(t, name, "echo hook\n")
	}
	subHook := tscWriteFile(t, "lib/task_before.sh", "echo lib\n")

	var ids []uint
	idToName := map[uint]string{}
	for name := range roots {
		task := tscCreateTask(t, "root-"+name, "task "+name)
		ids = append(ids, task.ID)
		idToName[task.ID] = name
	}
	subTask := tscCreateTask(t, "sub-hook", "task lib/task_before.sh")
	ids = append(ids, subTask.ID)

	preview := PreviewTaskScriptDeletion(ids, env)
	for name := range roots {
		item := tscItem(t, preview.Scripts, name)
		tscAssertKept(t, item, TaskScriptReasonManagedHelper)
		want := "脚本目录根下的 " + name + " 是全局钩子，面板每次运行任务前或结束后都会执行它，删除后会静默失效，已保留。"
		if item.Detail != want {
			t.Fatalf("%s: unexpected detail %q", name, item.Detail)
		}
	}
	tscAssertDeletable(t, tscItem(t, preview.Scripts, "lib/task_before.sh"))

	// 执行阶段：根目录三个钩子必须保留、文件仍在；子目录的可删。
	cleanup := CollectTaskScriptTargets(ids, env)
	tscDeleteTaskRows(t, ids...)
	result := cleanup.Execute(nil)
	if len(result.Deleted) != 1 || result.Deleted[0].Path != "lib/task_before.sh" {
		t.Fatalf("only the sub-dir hook may be deleted, got %#v", result)
	}
	for _, full := range roots {
		tscAssertExists(t, full)
	}
	tscAssertGone(t, subHook)
}

// 读任务快照出错（如数据库已关闭）时：用单独的 note，不复用 unresolved 文案；没有任何脚本组（B1）。
func TestTaskScriptPreviewSnapshotError(t *testing.T) {
	env := tscSetup(t)
	tscWriteFile(t, "a.py", "x")
	task := tscCreateTask(t, "a", "task a.py")

	// 关掉底层连接，让快照查询报错。SetupTestEnv 的 cleanup 再关一次是安全的。
	sqlDB, err := database.DB.DB()
	if err != nil {
		t.Fatalf("get sql db: %v", err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatalf("close db: %v", err)
	}

	preview := PreviewTaskScriptDeletion([]uint{task.ID}, env)
	if len(preview.Scripts) != 0 {
		t.Fatalf("snapshot error must yield no deletable scripts, got %#v", preview.Scripts)
	}
	info := tscTaskInfo(t, preview.Tasks, task.ID)
	if info.Note == taskScriptNoteUnresolved {
		t.Fatalf("snapshot error must not reuse the unresolved note")
	}
	if info.Note != taskScriptNoteSnapshotError {
		t.Fatalf("unexpected snapshot-error note: %q", info.Note)
	}
}

// ---------- 预览：结构性保护 ----------

func TestTaskScriptPreviewStructuralProtections(t *testing.T) {
	env := tscSetup(t)
	dir := tscMkdir(t, "dir.py")
	hook := tscWriteFile(t, "sub/.git/hook.py", "x")
	nodeModule := tscWriteFile(t, "node_modules/x.js", "x")
	dirTask := tscCreateTask(t, "dir", "task dir.py")
	hookTask := tscCreateTask(t, "git-hook", "task sub/.git/hook.py")
	nodeTask := tscCreateTask(t, "node-module", "task node_modules/x.js")
	ids := []uint{dirTask.ID, hookTask.ID, nodeTask.ID}

	preview := PreviewTaskScriptDeletion(ids, env)
	dirItem := tscItem(t, preview.Scripts, "dir.py")
	tscAssertKept(t, dirItem, TaskScriptReasonNotRegularFile)
	if dirItem.Detail != "dir.py 不是普通文件（可能是一个文件夹），面板只删除普通文件。" {
		t.Fatalf("unexpected not_regular_file detail: %q", dirItem.Detail)
	}
	for _, path := range []string{"sub/.git/hook.py", "node_modules/x.js"} {
		item := tscItem(t, preview.Scripts, path)
		tscAssertKept(t, item, TaskScriptReasonHiddenPath)
		if item.Detail != "这个文件位于受保护的目录（如 .git、node_modules）里，不能在这里删除。" {
			t.Fatalf("unexpected hidden_path detail: %q", item.Detail)
		}
	}

	// 执行阶段同样一个都不删；尤其目录绝不能被 os.Remove 掉（Windows 上能删空目录）。
	cleanup := CollectTaskScriptTargets(ids, env)
	tscDeleteTaskRows(t, ids...)
	result := cleanup.Execute(nil)
	if len(result.Deleted) != 0 || len(result.Skipped) != 3 {
		t.Fatalf("expected nothing deleted, got %#v", result)
	}
	for _, full := range []string{dir, hook, nodeModule} {
		tscAssertExists(t, full)
	}
}

// 路径上有软链接一律保留（末级软链接、中间目录软链接）。os.Symlink 不可用时跳过，需在 WSL 上确认。
func TestTaskScriptPreviewSymlinkKept(t *testing.T) {
	env := tscSetup(t)
	scriptsDir := config.C.Data.ScriptsDir
	target := tscWriteFile(t, "a.py", "x")
	tscWriteFile(t, "realdir/x.py", "x")
	tstSymlinkOrSkip(t, target, filepath.Join(scriptsDir, "link.py"))
	tstSymlinkOrSkip(t, filepath.Join(scriptsDir, "realdir"), filepath.Join(scriptsDir, "linkdir"))
	linkTask := tscCreateTask(t, "link", "task link.py")
	dirTask := tscCreateTask(t, "linkdir", "task linkdir/x.py")
	ids := []uint{linkTask.ID, dirTask.ID}

	preview := PreviewTaskScriptDeletion(ids, env)
	for _, path := range []string{"a.py", "realdir/x.py"} {
		item := tscItem(t, preview.Scripts, path)
		tscAssertKept(t, item, TaskScriptReasonSymlink)
		if item.Detail != "脚本路径经过了软链接，面板无法确定该删链接还是它指向的文件，已保留；如需删除请到脚本管理处理。" {
			t.Fatalf("unexpected symlink detail: %q", item.Detail)
		}
	}

	cleanup := CollectTaskScriptTargets(ids, env)
	tscDeleteTaskRows(t, ids...)
	if result := cleanup.Execute(nil); len(result.Deleted) != 0 {
		t.Fatalf("symlinked scripts must never be deleted, got %#v", result.Deleted)
	}
	tscAssertExists(t, target)
	tscAssertExists(t, filepath.Join(scriptsDir, "link.py"))
	tscAssertExists(t, filepath.Join(scriptsDir, "realdir", "x.py"))
}

// 脚本目录本身挂在软链接下（Docker / Magisk）时不能误判成软链接，照常可删。
func TestTaskScriptCleanupSymlinkedScriptsDirStillDeletable(t *testing.T) {
	testutil.SetupTestEnv(t)
	root := filepath.Dir(config.C.Data.ScriptsDir)
	realScripts := filepath.Join(root, "real-scripts")
	if err := os.MkdirAll(realScripts, 0o755); err != nil {
		t.Fatalf("mkdir real scripts: %v", err)
	}
	realFile := filepath.Join(realScripts, "a.py")
	if err := os.WriteFile(realFile, []byte("x"), 0o644); err != nil {
		t.Fatalf("write script: %v", err)
	}
	linkScripts := filepath.Join(root, "link-scripts")
	tstSymlinkOrSkip(t, realScripts, linkScripts)
	env := TaskScriptCleanupEnv{ScriptsDir: linkScripts}
	task := tscCreateTask(t, "a", "task a.py")

	tscAssertDeletable(t, tscItem(t, PreviewTaskScriptDeletion([]uint{task.ID}, env).Scripts, "a.py"))

	cleanup := CollectTaskScriptTargets([]uint{task.ID}, env)
	tscDeleteTaskRows(t, task.ID)
	result := cleanup.Execute(nil)
	if len(result.Deleted) != 1 || result.Deleted[0].Path != "a.py" {
		t.Fatalf("expected a.py to be deleted through the symlinked scripts dir, got %#v", result)
	}
	tscAssertGone(t, realFile)
}

// R2-05 / R2-4：isReparsePointFn 可替换。注入「某段是 reparse 点」的假函数，在各平台（含 Linux CI）钉住两件事：
//   - 中间目录段是 reparse 点（Windows 目录联接那一类）→ Direct=false，预览给 symlink，执行不删；
//   - 末级文件本身带 reparse 属性（如 Windows Server 的 DEDUP 文件）不算，照常可删。
//
// 真实的 isReparsePoint 只在 Windows 上可能为真，不注入的话这条分支的调用点在 Linux 上永远测不到。
func TestTaskScriptReparsePointSegmentsViaInjectedCheck(t *testing.T) {
	env := tscSetup(t)
	middle := tscWriteFile(t, "fakejunction/x.py", "x")
	last := tscWriteFile(t, "dedup.py", "x")
	original := isReparsePointFn
	isReparsePointFn = func(info os.FileInfo) bool {
		return info.Name() == "fakejunction" || info.Name() == "dedup.py"
	}
	t.Cleanup(func() { isReparsePointFn = original })

	base, err := resolveScriptsBase(env.ScriptsDir)
	if err != nil {
		t.Fatalf("resolve base: %v", err)
	}
	if target := ResolveTaskScriptTarget("task fakejunction/x.py", base); target.Kind != taskScriptKindScript || target.RelPath != "fakejunction/x.py" || target.Direct || target.LinkCheckFailed {
		t.Fatalf("a reparse-point middle directory must make the target indirect, got %#v", target)
	}
	if target := ResolveTaskScriptTarget("task dedup.py", base); target.Kind != taskScriptKindScript || !target.Direct || target.LinkCheckFailed {
		t.Fatalf("a reparse attribute on the file itself must not count, got %#v", target)
	}

	middleTask := tscCreateTask(t, "middle", "task fakejunction/x.py")
	lastTask := tscCreateTask(t, "last", "task dedup.py")
	ids := []uint{middleTask.ID, lastTask.ID}
	preview := PreviewTaskScriptDeletion(ids, env)
	tscAssertKept(t, tscItem(t, preview.Scripts, "fakejunction/x.py"), TaskScriptReasonSymlink)
	tscAssertDeletable(t, tscItem(t, preview.Scripts, "dedup.py"))

	cleanup := CollectTaskScriptTargets(ids, env)
	tscDeleteTaskRows(t, ids...)
	result := cleanup.Execute(nil)
	if len(result.Deleted) != 1 || result.Deleted[0].Path != "dedup.py" {
		t.Fatalf("only dedup.py may be deleted, got %#v", result)
	}
	tscAssertExists(t, middle)
	tscAssertGone(t, last)
}

// R2-1：LinkCheckFailed 置位时文件按 check_failed 保留，前端只看到「检查脚本引用时出错」，原因只能到面板日志里查。
// addMember 必须留一行：任务 id、相对路径与错误原文，并且判 ERROR 级别；没置位时不写。
func TestTaskScriptLinkCheckFailureIsLogged(t *testing.T) {
	tscSetup(t)
	var buf bytes.Buffer
	originalWriter := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(originalWriter) })

	cleanup := &TaskScriptCleanup{}
	cleanup.addMember(42, TaskScriptTarget{
		Kind:            taskScriptKindScript,
		RealPath:        filepath.Join(config.C.Data.ScriptsDir, "jd", "a.py"),
		RelPath:         "jd/a.py",
		LinkCheckFailed: true,
		linkCheckErr:    errors.New("lstat /data/scripts/jd: permission denied"),
	})
	line := strings.TrimSpace(buf.String())
	for _, want := range []string{"[任务删除]", "[42]", "jd/a.py", "lstat /data/scripts/jd: permission denied"} {
		if !strings.Contains(line, want) {
			t.Fatalf("expected the log line to contain %q, got %q", want, line)
		}
	}
	if level := detectPanelLogLevel(line); level != PanelLogLevelError {
		t.Fatalf("a link check failure must be logged at error level, got %q for %q", level, line)
	}

	buf.Reset()
	cleanup.addMember(43, TaskScriptTarget{Kind: taskScriptKindScript, RealPath: filepath.Join(config.C.Data.ScriptsDir, "b.py"), RelPath: "b.py", Direct: true})
	if buf.Len() != 0 {
		t.Fatalf("no log line expected without a link check failure, got %q", buf.String())
	}
}

// ---------- 预览：订阅 ----------

func TestTaskScriptPreviewGitSubscription(t *testing.T) {
	cases := []struct {
		name    string
		sub     model.Subscription
		makeGit bool
		want    string
		notWant string
	}{
		{
			name:    "force overwrite restores the file",
			sub:     model.Subscription{Name: "repo-force", Type: model.SubTypeGitRepo, SaveDir: "repo", OverwriteMode: model.SubOverwriteForce, AutoAddTaskMode: model.SubTaskSyncDisabled, Enabled: true},
			makeGit: true,
			want:    "下次拉取会把它还原。",
			notWant: "自动重新创建",
		},
		{
			name:    "auto add task mentions re-creation",
			sub:     model.Subscription{Name: "repo-readd", Type: model.SubTypeGitRepo, SaveDir: "repo", OverwriteMode: model.SubOverwriteForce, AutoAddTaskMode: model.SubTaskSyncEnabled, Enabled: true},
			makeGit: true,
			want:    "下次拉取会把它还原，并自动重新创建对应的任务。",
		},
		{
			name:    "preserve local changes warns about conflicts",
			sub:     model.Subscription{Name: "repo-preserve", Type: model.SubTypeGitRepo, SaveDir: "repo", OverwriteMode: model.SubOverwritePreserve, Enabled: true},
			makeGit: true,
			want:    "冲突",
		},
		{
			name:    "disabled subscription still protects its files",
			sub:     model.Subscription{Name: "repo-off", Type: model.SubTypeGitRepo, SaveDir: "repo", OverwriteMode: model.SubOverwriteForce, Enabled: false},
			makeGit: true,
			want:    "下次拉取会把它还原",
		},
		{
			name:    "existing non-git dir will be overwritten by the next pull",
			sub:     model.Subscription{Name: "repo-plain", Type: model.SubTypeGitRepo, SaveDir: "repo", OverwriteMode: model.SubOverwriteForce, Enabled: true},
			makeGit: false,
			want:    "下次拉取会把它还原",
		},
		{
			name:    "save dir falls back to alias",
			sub:     model.Subscription{Name: "repo-alias", Type: model.SubTypeGitRepo, Alias: "repo", OverwriteMode: model.SubOverwriteForce, Enabled: true},
			makeGit: true,
			want:    "下次拉取会把它还原",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := tscSetup(t)
			full := tscWriteFile(t, "repo/x.js", "x")
			if tc.makeGit {
				tscMkdir(t, "repo/.git")
			}
			tscCreateSubscription(t, tc.sub)
			task := tscCreateTask(t, "repo-x", "task repo/x.js")

			item := tscItem(t, PreviewTaskScriptDeletion([]uint{task.ID}, env).Scripts, "repo/x.js")
			tscAssertKept(t, item, TaskScriptReasonSubscriptionManaged)
			if !strings.Contains(item.Detail, "订阅「"+tc.sub.Name+"」") || !strings.Contains(item.Detail, tc.want) {
				t.Fatalf("unexpected detail: %q", item.Detail)
			}
			if tc.notWant != "" && strings.Contains(item.Detail, tc.notWant) {
				t.Fatalf("detail must not contain %q: %q", tc.notWant, item.Detail)
			}

			cleanup := CollectTaskScriptTargets([]uint{task.ID}, env)
			tscDeleteTaskRows(t, task.ID)
			if result := cleanup.Execute(nil); len(result.Deleted) != 0 {
				t.Fatalf("subscription files must not be deleted, got %#v", result.Deleted)
			}
			tscAssertExists(t, full)
		})
	}
}

// 订阅根目录等于脚本目录时，只有真的是 git 仓库才算受管，免得一条配错的订阅把全部脚本锁死。
func TestTaskScriptPreviewGitSubscriptionAtScriptsRoot(t *testing.T) {
	env := tscSetup(t)
	tscWriteFile(t, "a.py", "x")
	tscCreateSubscription(t, model.Subscription{Name: "root-sub", Type: model.SubTypeGitRepo, SaveDir: ".", OverwriteMode: model.SubOverwriteForce, Enabled: true})
	task := tscCreateTask(t, "root-a", "task a.py")

	tscAssertDeletable(t, tscItem(t, PreviewTaskScriptDeletion([]uint{task.ID}, env).Scripts, "a.py"))

	tscMkdir(t, ".git")
	tscAssertKept(t, tscItem(t, PreviewTaskScriptDeletion([]uint{task.ID}, env).Scripts, "a.py"), TaskScriptReasonSubscriptionManaged)
}

// 单文件订阅只保护精确的下载目标；subscription:N 标签不能作为归属依据（可以伪造），以路径为准。
func TestTaskScriptPreviewSingleFileSubscriptionAndLabels(t *testing.T) {
	env := tscSetup(t)
	tscWriteFile(t, "downloads/x.js", "x")
	tscWriteFile(t, "downloads/other.js", "x")
	tscWriteFile(t, "ops/y.py", "x")
	tscCreateSubscription(t, model.Subscription{Name: "single", Type: model.SubTypeSingleFile, URL: "https://example.com/raw/x.js", OverwriteMode: model.SubOverwriteForce, AutoAddTaskMode: model.SubTaskSyncEnabled, Enabled: true})
	labeled := tscCreateSubscription(t, model.Subscription{Name: "ops-scripts", Type: model.SubTypeGitRepo, SaveDir: "subscriptions/ops-scripts", Enabled: true})
	downloaded := tscCreateTask(t, "x", "task downloads/x.js")
	sibling := tscCreateTask(t, "other", "task downloads/other.js")
	fakeOwned := tscCreateTask(t, "labeled", "task ops/y.py", func(task *model.Task) {
		task.Labels = fmt.Sprintf("subscription:%d", labeled.ID)
	})

	preview := PreviewTaskScriptDeletion([]uint{downloaded.ID, sibling.ID, fakeOwned.ID}, env)
	item := tscItem(t, preview.Scripts, "downloads/x.js")
	tscAssertKept(t, item, TaskScriptReasonSubscriptionManaged)
	if item.Detail != "这是订阅「single」下载的文件，每次拉取都会重新下载，并自动重新创建对应的任务。如果不再需要，请停用或删除这个订阅。" {
		t.Fatalf("unexpected single-file detail: %q", item.Detail)
	}
	tscAssertDeletable(t, tscItem(t, preview.Scripts, "downloads/other.js"))
	tscAssertDeletable(t, tscItem(t, preview.Scripts, "ops/y.py"))
}

// ---------- 预览：托管 helper 与公共库提示 ----------

func TestTaskScriptPreviewManagedHelpersAndWarnings(t *testing.T) {
	env := tscSetup(t)
	tscWriteFile(t, "notify.py", "# "+managedNotifyHelperToken+"\nprint('x')\n")
	tscWriteFile(t, "sendNotify.js", "module.exports = {}\n")
	tscWriteFile(t, "lib/notify.py", "x")
	tscWriteFile(t, "jd/sign.js", "x")
	tscWriteFile(t, "utils.js", "x")
	var ids []uint
	for _, command := range []string{"task notify.py", "task sendNotify.js", "task lib/notify.py", "task jd/sign.js", "task utils.js"} {
		ids = append(ids, tscCreateTask(t, command, command).ID)
	}

	preview := PreviewTaskScriptDeletion(ids, env)
	managed := tscItem(t, preview.Scripts, "notify.py")
	tscAssertKept(t, managed, TaskScriptReasonManagedHelper)
	if managed.Detail != "notify.py 是面板内置的通知辅助脚本，其他脚本会引用它，删除后也会被自动重新生成。" {
		t.Fatalf("unexpected managed helper detail: %q", managed.Detail)
	}
	userHelper := tscItem(t, preview.Scripts, "sendNotify.js")
	tscAssertKept(t, userHelper, TaskScriptReasonManagedHelper)
	if userHelper.Detail != "脚本目录根下的 sendNotify.js 通常被其他脚本当作通知库引用，删除后面板会写入内置版本替换它，已保留。" {
		t.Fatalf("unexpected user helper detail: %q", userHelper.Detail)
	}

	cases := map[string]string{
		"lib/notify.py": "notify",
		"jd/sign.js":    "sign",
		"utils.js":      "utils",
	}
	for path, stem := range cases {
		item := tscItem(t, preview.Scripts, path)
		tscAssertDeletable(t, item)
		want := "文件名 " + stem + " 常见于公共库（如 sendNotify、utils、sign），可能被其他脚本引用，确认不再需要再删。"
		if len(item.Warnings) != 1 || item.Warnings[0] != want {
			t.Fatalf("%s: unexpected warnings %#v", path, item.Warnings)
		}
	}
}

// ---------- 预览：运行状态 ----------

func TestTaskScriptPreviewRunningStates(t *testing.T) {
	testutil.SetupTestEnv(t)
	tscWriteFile(t, "running.py", "x")
	tscWriteFile(t, "queued.py", "x")
	tscWriteFile(t, "process.py", "x")
	tscWriteFile(t, "idle.py", "x")
	running := tscCreateTask(t, "running", "task running.py", func(task *model.Task) { task.Status = model.TaskStatusRunning })
	queued := tscCreateTask(t, "queued", "task queued.py", func(task *model.Task) { task.Status = model.TaskStatusQueued })
	process := tscCreateTask(t, "process", "task process.py", func(task *model.Task) { task.Status = model.TaskStatusDisabled })
	idle := tscCreateTask(t, "idle", "task idle.py")
	env := TaskScriptCleanupEnv{
		ScriptsDir:    config.C.Data.ScriptsDir,
		IsTaskRunning: func(id uint) bool { return id == process.ID },
	}

	preview := PreviewTaskScriptDeletion([]uint{running.ID, queued.ID, process.ID, idle.ID}, env)
	for path, task := range map[string]*model.Task{"running.py": running, "queued.py": queued, "process.py": process} {
		item := tscItem(t, preview.Scripts, path)
		tscAssertKept(t, item, TaskScriptReasonTaskRunning)
		want := "任务「" + task.Name + "」正在运行或排队中，脚本先保留；等运行结束后可以到脚本管理里删除。"
		if item.Detail != want {
			t.Fatalf("%s: unexpected detail %q", path, item.Detail)
		}
		if !tscTaskInfo(t, preview.Tasks, task.ID).Running {
			t.Fatalf("%s: expected tasks[].running=true", path)
		}
	}
	tscAssertDeletable(t, tscItem(t, preview.Scripts, "idle.py"))
	if tscTaskInfo(t, preview.Tasks, idle.ID).Running {
		t.Fatalf("idle task must not be reported as running")
	}
}

func TestTaskExecutorHasRunningProcess(t *testing.T) {
	var nilExecutor *TaskExecutor
	if nilExecutor.HasRunningProcess(1) {
		t.Fatalf("nil executor must report no running process")
	}

	testutil.SetupTestEnv(t)
	executor := NewTaskExecutor()
	if executor.HasRunningProcess(7) {
		t.Fatalf("fresh executor must report no running process")
	}

	self, err := os.FindProcess(os.Getpid())
	if err != nil {
		t.Fatalf("find current process: %v", err)
	}
	// 只登记、不停止：这里登记的是测试进程自己，绝不能对它调 StopTask。
	executor.processLock.Lock()
	executor.runningProcesses[7] = map[int]*os.Process{self.Pid: self}
	executor.runningProcesses[8] = map[int]*os.Process{}
	executor.processLock.Unlock()

	if !executor.HasRunningProcess(7) {
		t.Fatalf("expected task 7 to have a running process")
	}
	if executor.HasRunningProcess(8) {
		t.Fatalf("an empty process table entry must not count as running")
	}
}

// ---------- 预览：钩子 ----------

func TestTaskScriptPreviewHookReferences(t *testing.T) {
	t.Run("task before hook mentions the file by name", func(t *testing.T) {
		env := tscSetup(t)
		tscWriteFile(t, "jd/sign.js", "x")
		before := "cd jd && node sign.js"
		tscCreateTask(t, "hooker", "echo ok", func(task *model.Task) { task.TaskBefore = &before })
		requested := tscCreateTask(t, "sign", "task jd/sign.js")

		item := tscItem(t, PreviewTaskScriptDeletion([]uint{requested.ID}, env).Scripts, "jd/sign.js")
		tscAssertKept(t, item, TaskScriptReasonReferencedInHook)
		if item.Detail != "任务「hooker」的前置/后置命令里提到了 sign.js，面板无法确认它是否依赖这个文件。" {
			t.Fatalf("unexpected detail: %q", item.Detail)
		}
	})

	// A5：省略扩展名的 node 引用（cd jd && node sign → 实际跑 sign.js）也要认出来。
	t.Run("task before hook mentions a node script without extension", func(t *testing.T) {
		env := tscSetup(t)
		tscWriteFile(t, "jd/sign.js", "x")
		before := "cd jd && node sign"
		tscCreateTask(t, "hooker", "echo ok", func(task *model.Task) { task.TaskBefore = &before })
		requested := tscCreateTask(t, "sign", "task jd/sign.js")

		item := tscItem(t, PreviewTaskScriptDeletion([]uint{requested.ID}, env).Scripts, "jd/sign.js")
		tscAssertKept(t, item, TaskScriptReasonReferencedInHook)
	})

	// A5 反例：不是 node 风格（echo sign）不应因省略扩展名而误判为引用。
	t.Run("plain text token without a node prefix does not match", func(t *testing.T) {
		env := tscSetup(t)
		tscWriteFile(t, "jd/sign.js", "x")
		before := "echo sign"
		tscCreateTask(t, "echoer", "echo ok", func(task *model.Task) { task.TaskBefore = &before })
		requested := tscCreateTask(t, "sign", "task jd/sign.js")

		tscAssertDeletable(t, tscItem(t, PreviewTaskScriptDeletion([]uint{requested.ID}, env).Scripts, "jd/sign.js"))
	})

	t.Run("subscription hook script mentions the file", func(t *testing.T) {
		env := tscSetup(t)
		tscWriteFile(t, "a.py", "x")
		tscCreateSubscription(t, model.Subscription{Name: "hook-sub", Type: model.SubTypeGitRepo, SaveDir: "somewhere", HookScript: "python3 a.py", Enabled: true})
		requested := tscCreateTask(t, "a", "task a.py")

		item := tscItem(t, PreviewTaskScriptDeletion([]uint{requested.ID}, env).Scripts, "a.py")
		tscAssertKept(t, item, TaskScriptReasonReferencedInHook)
		if item.Detail != "订阅「hook-sub」的拉取前/后命令里提到了 a.py，面板无法确认它是否依赖这个文件。" {
			t.Fatalf("unexpected detail: %q", item.Detail)
		}
	})

	t.Run("a different file name does not match", func(t *testing.T) {
		env := tscSetup(t)
		tscWriteFile(t, "a.py", "x")
		after := "python3 data.py"
		tscCreateTask(t, "data-hooker", "echo ok", func(task *model.Task) { task.TaskAfter = &after })
		requested := tscCreateTask(t, "a", "task a.py")

		tscAssertDeletable(t, tscItem(t, PreviewTaskScriptDeletion([]uint{requested.ID}, env).Scripts, "a.py"))
	})

	// A5 的 ./ 分支：cd jd && node -e "require('./sign')" 只能靠「以 ./ 开头」认出 sign.js（R2-6）。
	t.Run("task before hook requires a relative node module", func(t *testing.T) {
		env := tscSetup(t)
		tscWriteFile(t, "jd/sign.js", "x")
		before := `cd jd && node -e "require('./sign')"`
		tscCreateTask(t, "requirer", "echo ok", func(task *model.Task) { task.TaskBefore = &before })
		requested := tscCreateTask(t, "sign", "task jd/sign.js")

		item := tscItem(t, PreviewTaskScriptDeletion([]uint{requested.ID}, env).Scripts, "jd/sign.js")
		tscAssertKept(t, item, TaskScriptReasonReferencedInHook)
	})

	// R2-3：其他任务的前置 / 后置命令、订阅钩子里用 python -m 运行同一脚本，也算提到了这个文件；
	// python3 -u -m a 这种 -m 前面还有选项的写法也要认出。
	pythonModuleHooks := []struct {
		name       string
		script     string
		before     string
		after      string
		subHook    string
		wantDetail string
	}{
		{
			name:       "task before hook",
			script:     "jd/sign.py",
			before:     "cd /tmp && python3 -m jd.sign",
			wantDetail: "任务「hooker」的前置/后置命令里提到了 sign.py，面板无法确认它是否依赖这个文件。",
		},
		{
			name:       "task after hook with an option before -m",
			script:     "a.py",
			after:      "python3 -u -m a",
			wantDetail: "任务「hooker」的前置/后置命令里提到了 a.py，面板无法确认它是否依赖这个文件。",
		},
		{
			name:       "subscription hook",
			script:     "b.py",
			subHook:    "python3 -m b",
			wantDetail: "订阅「mod-sub」的拉取前/后命令里提到了 b.py，面板无法确认它是否依赖这个文件。",
		},
		// R3-BE-1：先 cd 进子目录再 -m，模块名是相对子目录写的（jd/sign.py 写成 -m sign），
		// 和从脚本目录根算起的候选整路径对不上，要按后缀认出来。
		{
			name:       "task before hook that cds into a subdirectory first",
			script:     "jd/sign.py",
			before:     "cd jd && python3 -m sign",
			wantDetail: "任务「hooker」的前置/后置命令里提到了 sign.py，面板无法确认它是否依赖这个文件。",
		},
		{
			name:       "subscription hook that cds into a subdirectory first",
			script:     "jd/sign.py",
			subHook:    "cd jd && python3 -m sign",
			wantDetail: "订阅「mod-sub」的拉取前/后命令里提到了 sign.py，面板无法确认它是否依赖这个文件。",
		},
		// 点号模块同理：实际执行 jd/pkg/mod.py，导入途中还会执行父包的 jd/pkg/__init__.py，两个都得保住。
		{
			name:       "task before hook that cds into a subdirectory and runs a dotted module",
			script:     "jd/pkg/mod.py",
			before:     "cd jd && python3 -m pkg.mod",
			wantDetail: "任务「hooker」的前置/后置命令里提到了 mod.py，面板无法确认它是否依赖这个文件。",
		},
		{
			name:       "parent package init of a dotted module run after cd",
			script:     "jd/pkg/__init__.py",
			before:     "cd jd && python3 -m pkg.mod",
			wantDetail: "任务「hooker」的前置/后置命令里提到了 __init__.py，面板无法确认它是否依赖这个文件。",
		},
	}
	for _, tc := range pythonModuleHooks {
		t.Run("python -m in "+tc.name, func(t *testing.T) {
			env := tscSetup(t)
			full := tscWriteFile(t, tc.script, "x")
			if tc.before != "" || tc.after != "" {
				before, after := tc.before, tc.after
				tscCreateTask(t, "hooker", "echo ok", func(task *model.Task) {
					if before != "" {
						task.TaskBefore = &before
					}
					if after != "" {
						task.TaskAfter = &after
					}
				})
			}
			if tc.subHook != "" {
				tscCreateSubscription(t, model.Subscription{Name: "mod-sub", Type: model.SubTypeGitRepo, SaveDir: "somewhere", HookScript: tc.subHook, Enabled: true})
			}
			requested := tscCreateTask(t, "req", "task "+tc.script)

			item := tscItem(t, PreviewTaskScriptDeletion([]uint{requested.ID}, env).Scripts, tc.script)
			tscAssertKept(t, item, TaskScriptReasonReferencedInHook)
			if item.Detail != tc.wantDetail {
				t.Fatalf("unexpected detail: %q", item.Detail)
			}

			// 执行阶段同样保留：删了它，钩子下次运行会报 ModuleNotFoundError。
			cleanup := CollectTaskScriptTargets([]uint{requested.ID}, env)
			tscDeleteTaskRows(t, requested.ID)
			if result := cleanup.Execute(nil); len(result.Deleted) != 0 {
				t.Fatalf("a script run by a hook via python -m must not be deleted, got %#v", result.Deleted)
			}
			tscAssertExists(t, full)
		})
	}

	// R2-3 反例：-m 前面不是 python 解释器（git commit -m a、task -m 5 x.py），不能把 a、5 当成模块而过度保留。
	t.Run("-m after a non-python command is not a module reference", func(t *testing.T) {
		env := tscSetup(t)
		tscWriteFile(t, "a.py", "x")
		tscWriteFile(t, "5.py", "x")
		gitHook := "git commit -m a"
		taskHook := "task -m 5 x.py"
		tscCreateTask(t, "git-hooker", "echo ok", func(task *model.Task) { task.TaskAfter = &gitHook })
		tscCreateTask(t, "task-hooker", "echo ok", func(task *model.Task) { task.TaskBefore = &taskHook })
		first := tscCreateTask(t, "a", "task a.py")
		second := tscCreateTask(t, "5", "task 5.py")

		preview := PreviewTaskScriptDeletion([]uint{first.ID, second.ID}, env)
		tscAssertDeletable(t, tscItem(t, preview.Scripts, "a.py"))
		tscAssertDeletable(t, tscItem(t, preview.Scripts, "5.py"))
	})

	// R3-BE-1 反例：后缀比较带着模块的目录层级，python3 -m json.tool 只会保住 */json/tool.py；
	// 只比文件名的话，任意一个同名的 tool.py 都会被它保住、永远删不掉。
	t.Run("python -m json.tool does not keep an unrelated tool.py", func(t *testing.T) {
		env := tscSetup(t)
		full := tscWriteFile(t, "other/tool.py", "x")
		before := "python3 -m json.tool"
		tscCreateTask(t, "json-hooker", "echo ok", func(task *model.Task) { task.TaskBefore = &before })
		requested := tscCreateTask(t, "tool", "task other/tool.py")

		tscAssertDeletable(t, tscItem(t, PreviewTaskScriptDeletion([]uint{requested.ID}, env).Scripts, "other/tool.py"))

		cleanup := CollectTaskScriptTargets([]uint{requested.ID}, env)
		tscDeleteTaskRows(t, requested.ID)
		if result := cleanup.Execute(nil); len(result.Deleted) != 1 || result.Deleted[0].Path != "other/tool.py" {
			t.Fatalf("python3 -m json.tool does not load other/tool.py, it should be deleted, got %#v", result.Deleted)
		}
		tscAssertGone(t, full)
	})
}

// 同一个文件既在订阅目录里又被共用：reason 取优先级更高的 subscription_managed，shared_by 仍然填写。
func TestTaskScriptPreviewReasonPriority(t *testing.T) {
	env := tscSetup(t)
	tscWriteFile(t, "repo/x.js", "x")
	tscMkdir(t, "repo/.git")
	tscCreateSubscription(t, model.Subscription{Name: "repo", Type: model.SubTypeGitRepo, SaveDir: "repo", OverwriteMode: model.SubOverwriteForce, Enabled: true})
	requested := tscCreateTask(t, "req", "task repo/x.js")
	other := tscCreateTask(t, "other", "task repo/x.js")

	item := tscItem(t, PreviewTaskScriptDeletion([]uint{requested.ID}, env).Scripts, "repo/x.js")
	tscAssertKept(t, item, TaskScriptReasonSubscriptionManaged)
	if len(item.SharedBy) != 1 || item.SharedBy[0].ID != other.ID || item.SharedBy[0].Name != "other" || item.SharedBy[0].TextMatch {
		t.Fatalf("shared_by must be filled regardless of the reason, got %#v", item.SharedBy)
	}
}

// ---------- 预览：任务状态与 note ----------

func TestTaskScriptPreviewTaskStatusesAndNotes(t *testing.T) {
	env := tscSetup(t)
	tscWriteFile(t, "ok.py", "x")
	// a.py 存在，才能让 `task a.py now extra` 走到「脚本存在但命令格式非法」这条真正的 unresolved 分支；
	// 否则解析器先报「文件不存在」，会被归类成 not_found。
	tscWriteFile(t, "a.py", "x")
	base, err := resolveScriptsBase(env.ScriptsDir)
	if err != nil {
		t.Fatalf("resolve base: %v", err)
	}
	outsideMissing := filepath.ToSlash(filepath.Join(filepath.Dir(base.Real), "outside", "nope.py"))

	ok := tscCreateTask(t, "ok", "task ok.py")
	managed := tscCreateTask(t, "managed", "dailycheckin --help")
	module := tscCreateTask(t, "module", "python3 -m http.server")
	missing := tscCreateTask(t, "missing", "task ops/old.sh")
	missingOutside := tscCreateTask(t, "missing-outside", "task "+outsideMissing)
	outside := tscCreateTask(t, "outside", "task ../x.py")
	broken := tscCreateTask(t, "broken", "task a.py now extra")
	const ghost = uint(999999)

	ids := []uint{ok.ID, managed.ID, module.ID, missing.ID, missingOutside.ID, outside.ID, broken.ID, ghost, ok.ID}
	preview := PreviewTaskScriptDeletion(ids, env)
	if !preview.Checked {
		t.Fatalf("checked must always be true")
	}
	if len(preview.Tasks) != len(ids)-1 {
		t.Fatalf("expected duplicate ids to be merged, got %d tasks", len(preview.Tasks))
	}
	for i, id := range ids[:len(ids)-1] {
		if preview.Tasks[i].ID != id {
			t.Fatalf("tasks must follow request order, got %#v", preview.Tasks)
		}
	}

	want := map[uint]TaskScriptTaskInfo{
		ok.ID:             {ID: ok.ID, Name: "ok", Found: true, ScriptStatus: "resolved", ScriptPath: "ok.py"},
		managed.ID:        {ID: managed.ID, Name: "managed", Found: true, ScriptStatus: "no_script", Note: "命令不是直接运行脚本文件（由 dailycheckin 执行），不会一并删除任何文件。"},
		module.ID:         {ID: module.ID, Name: "module", Found: true, ScriptStatus: "no_script", Note: "命令运行的是 Python 模块 http.server，没有对应的脚本文件。"},
		missing.ID:        {ID: missing.ID, Name: "missing", Found: true, ScriptStatus: "not_found", ScriptPath: "ops/old.sh", Note: "命令里的脚本 ops/old.sh 已经不存在，只删除任务。"},
		missingOutside.ID: {ID: missingOutside.ID, Name: "missing-outside", Found: true, ScriptStatus: "not_found", Note: "命令里的脚本已经不存在，只删除任务。"},
		outside.ID:        {ID: outside.ID, Name: "outside", Found: true, ScriptStatus: "outside_scripts_dir", Note: "命令里的脚本不在脚本目录内，面板不会删除脚本目录以外的文件。"},
		broken.ID:         {ID: broken.ID, Name: "broken", Found: true, ScriptStatus: "unresolved", Note: "没能从命令里识别出脚本文件（命令格式可能有误），只删除任务。"},
		ghost:             {ID: ghost, ScriptStatus: "task_not_found", Note: "任务不存在（可能已被删除）。"},
	}
	for id, expected := range want {
		if got := tscTaskInfo(t, preview.Tasks, id); got != expected {
			t.Fatalf("task %d: want %#v, got %#v", id, expected, got)
		}
	}
	if len(preview.Scripts) != 1 || preview.Scripts[0].Path != "ok.py" {
		t.Fatalf("only the resolved script may appear in scripts[], got %#v", preview.Scripts)
	}
}

// ---------- 执行 ----------

func TestTaskScriptExecuteDeletesOnlyTheFile(t *testing.T) {
	env := tscSetup(t)
	full := tscWriteFile(t, "demo/a.py", "x")
	task := tscCreateTask(t, "a", "task demo/a.py")

	cleanup := CollectTaskScriptTargets([]uint{task.ID}, env)
	tscDeleteTaskRows(t, task.ID)
	result := cleanup.Execute(nil)
	if len(result.Deleted) != 1 || result.Deleted[0].Path != "demo/a.py" || len(result.Skipped) != 0 {
		t.Fatalf("unexpected result: %#v", result)
	}
	if len(result.Tasks) != 1 || result.Tasks[0].ID != task.ID {
		t.Fatalf("expected the requested task in result.tasks, got %#v", result.Tasks)
	}
	tscAssertGone(t, full)
	// 父目录绝不能被删。
	tscAssertExists(t, filepath.Dir(full))
}

func TestTaskScriptExecuteConfirmNarrowing(t *testing.T) {
	cases := []struct {
		name        string
		confirm     *[]string
		wantDeleted bool
	}{
		{name: "not passed deletes everything deletable", confirm: nil, wantDeleted: true},
		{name: "exact match deletes", confirm: &[]string{"demo/a.py"}, wantDeleted: true},
		{name: "different path keeps the file", confirm: &[]string{"demo/other.py"}},
		{name: "case differs keeps the file", confirm: &[]string{"DEMO/A.PY"}},
		{name: "empty list deletes nothing", confirm: &[]string{}},
		{name: "empty string deletes nothing", confirm: &[]string{""}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := tscSetup(t)
			full := tscWriteFile(t, "demo/a.py", "x")
			task := tscCreateTask(t, "a", "task demo/a.py")
			cleanup := CollectTaskScriptTargets([]uint{task.ID}, env)
			tscDeleteTaskRows(t, task.ID)

			result := cleanup.Execute(tc.confirm)
			if tc.wantDeleted {
				if len(result.Deleted) != 1 {
					t.Fatalf("expected the file to be deleted, got %#v", result)
				}
				tscAssertGone(t, full)
				return
			}
			if len(result.Deleted) != 0 || len(result.Skipped) != 1 {
				t.Fatalf("expected the file to be kept, got %#v", result)
			}
			tscAssertKept(t, result.Skipped[0], TaskScriptReasonNotConfirmed)
			if result.Skipped[0].Detail != "这个脚本不在你确认删除的列表里（可能是确认之后任务命令被改过），已保留。" {
				t.Fatalf("unexpected detail: %q", result.Skipped[0].Detail)
			}
			tscAssertExists(t, full)
		})
	}
}

// Collect 之后文件被删、被替换成同名目录或另一个新文件：执行阶段必须察觉并保留。
func TestTaskScriptExecuteDetectsChangesAfterCollect(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(t *testing.T, full string)
		reason string
	}{
		{
			name: "removed",
			mutate: func(t *testing.T, full string) {
				if err := os.Remove(full); err != nil {
					t.Fatalf("remove: %v", err)
				}
			},
			reason: TaskScriptReasonNotFound,
		},
		{
			name: "replaced by a directory",
			mutate: func(t *testing.T, full string) {
				if err := os.Remove(full); err != nil {
					t.Fatalf("remove: %v", err)
				}
				if err := os.Mkdir(full, 0o755); err != nil {
					t.Fatalf("mkdir: %v", err)
				}
			},
			reason: TaskScriptReasonChanged,
		},
		{
			// 先写新文件再 rename 覆盖：新 inode 在旧文件还在时就分配好了，不会复用旧 inode 号。
			name: "replaced by a new file",
			mutate: func(t *testing.T, full string) {
				if err := os.WriteFile(full+".new", []byte("new"), 0o644); err != nil {
					t.Fatalf("write new: %v", err)
				}
				if err := os.Rename(full+".new", full); err != nil {
					t.Fatalf("rename: %v", err)
				}
			},
			reason: TaskScriptReasonChanged,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := tscSetup(t)
			full := tscWriteFile(t, "demo/a.py", "x")
			task := tscCreateTask(t, "a", "task demo/a.py")
			cleanup := CollectTaskScriptTargets([]uint{task.ID}, env)
			tscDeleteTaskRows(t, task.ID)
			tc.mutate(t, full)

			result := cleanup.Execute(nil)
			if len(result.Deleted) != 0 || len(result.Skipped) != 1 {
				t.Fatalf("expected nothing deleted, got %#v", result)
			}
			tscAssertKept(t, result.Skipped[0], tc.reason)
			if tc.reason == TaskScriptReasonChanged {
				tscAssertExists(t, full)
			}
		})
	}
}

// Collect 之后文件被同尺寸内容替换、并用 os.Chtimes 还原 mtime：仍要判为 changed，新文件保留（A6）。
// Linux/Android 靠 ctime，Windows 靠 Collect 时固化的文件身份。跨平台，不跳过。
func TestTaskScriptExecuteSameSizeReplacementChanged(t *testing.T) {
	env := tscSetup(t)
	full := tscWriteFile(t, "demo/a.py", "x")
	original, err := os.Stat(full)
	if err != nil {
		t.Fatalf("stat original: %v", err)
	}
	task := tscCreateTask(t, "a", "task demo/a.py")
	cleanup := CollectTaskScriptTargets([]uint{task.ID}, env)
	tscDeleteTaskRows(t, task.ID)

	// 同尺寸替换（"x" -> "y"，都 1 字节），再把 mtime 还原成原值。
	if err := os.Remove(full); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if err := os.WriteFile(full, []byte("y"), 0o644); err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	if err := os.Chtimes(full, original.ModTime(), original.ModTime()); err != nil {
		t.Fatalf("restore mtime: %v", err)
	}

	result := cleanup.Execute(nil)
	if len(result.Deleted) != 0 || len(result.Skipped) != 1 {
		t.Fatalf("expected nothing deleted, got %#v", result)
	}
	tscAssertKept(t, result.Skipped[0], TaskScriptReasonChanged)
	tscAssertExists(t, full)
	if content, err := os.ReadFile(full); err != nil || string(content) != "y" {
		t.Fatalf("the replacing file must stay intact, got %q err=%v", content, err)
	}
}

// Collect 之后文件被换成指向外部的软链接：记 changed，外部文件完好。os.Symlink 不可用时跳过，需在 WSL 上确认。
func TestTaskScriptExecuteSymlinkReplacementChanged(t *testing.T) {
	env := tscSetup(t)
	full := tscWriteFile(t, "demo/a.py", "x")
	outside := filepath.Join(filepath.Dir(config.C.Data.ScriptsDir), "outside.py")
	if err := os.WriteFile(outside, []byte("keep"), 0o644); err != nil {
		t.Fatalf("write outside: %v", err)
	}
	task := tscCreateTask(t, "a", "task demo/a.py")
	cleanup := CollectTaskScriptTargets([]uint{task.ID}, env)
	tscDeleteTaskRows(t, task.ID)
	if err := os.Remove(full); err != nil {
		t.Fatalf("remove: %v", err)
	}
	tstSymlinkOrSkip(t, outside, full)

	result := cleanup.Execute(nil)
	if len(result.Deleted) != 0 || len(result.Skipped) != 1 {
		t.Fatalf("expected nothing deleted, got %#v", result)
	}
	// B4：执行阶段先做 Lstat 身份比对再做 groupConsistent，所以「换成指向外部的软链接」在各平台都报 changed
	// （字面路径从普通文件变成了软链接，Mode 类型不同）。文件不删、外部文件不动。
	tscAssertKept(t, result.Skipped[0], TaskScriptReasonChanged)
	content, err := os.ReadFile(outside)
	if err != nil || string(content) != "keep" {
		t.Fatalf("outside file must stay intact, got %q err=%v", content, err)
	}
	if info, err := os.Lstat(full); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("the replacing symlink must stay, got info=%v err=%v", info, err)
	}
}

// 执行阶段基于删除后库里剩下的任务重新判定：Collect 之后新建的引用、没删掉的任务行都要拦住。
func TestTaskScriptExecuteRechecksReferencesAfterCollect(t *testing.T) {
	t.Run("new reference created after collect", func(t *testing.T) {
		env := tscSetup(t)
		full := tscWriteFile(t, "demo/a.py", "x")
		task := tscCreateTask(t, "req", "task demo/a.py")
		cleanup := CollectTaskScriptTargets([]uint{task.ID}, env)
		tscDeleteTaskRows(t, task.ID)
		late := tscCreateTask(t, "late", "task demo/a.py")

		result := cleanup.Execute(nil)
		if len(result.Skipped) != 1 {
			t.Fatalf("expected the file to be kept, got %#v", result)
		}
		tscAssertKept(t, result.Skipped[0], TaskScriptReasonShared)
		if len(result.Skipped[0].SharedBy) != 1 || result.Skipped[0].SharedBy[0].ID != late.ID {
			t.Fatalf("unexpected shared_by: %#v", result.Skipped[0].SharedBy)
		}
		tscAssertExists(t, full)
	})

	t.Run("task row was not deleted", func(t *testing.T) {
		env := tscSetup(t)
		full := tscWriteFile(t, "demo/a.py", "x")
		task := tscCreateTask(t, "req", "task demo/a.py")
		cleanup := CollectTaskScriptTargets([]uint{task.ID}, env)

		result := cleanup.Execute(nil)
		if len(result.Skipped) != 1 {
			t.Fatalf("expected the file to be kept, got %#v", result)
		}
		tscAssertKept(t, result.Skipped[0], TaskScriptReasonTaskNotDeleted)
		if result.Skipped[0].Detail != "任务「req」没有删除成功，脚本先保留。" {
			t.Fatalf("unexpected detail: %q", result.Skipped[0].Detail)
		}
		tscAssertExists(t, full)
	})
}

// 两个 goroutine 并发执行：执行阶段整体串行，只有一次 os.Remove 成功，另一次得到 not_found，不会 panic。
func TestTaskScriptExecuteConcurrentOnlyOneRemoves(t *testing.T) {
	env := tscSetup(t)
	full := tscWriteFile(t, "demo/a.py", "x")
	first := tscCreateTask(t, "a", "task demo/a.py")
	second := tscCreateTask(t, "b", "task demo/a.py")
	ids := []uint{first.ID, second.ID}
	cleanups := []*TaskScriptCleanup{CollectTaskScriptTargets(ids, env), CollectTaskScriptTargets(ids, env)}
	tscDeleteTaskRows(t, ids...)

	results := make([]TaskScriptDeleteResult, len(cleanups))
	var wg sync.WaitGroup
	for i, cleanup := range cleanups {
		wg.Add(1)
		go func(i int, cleanup *TaskScriptCleanup) {
			defer wg.Done()
			results[i] = cleanup.Execute(nil)
		}(i, cleanup)
	}
	wg.Wait()

	deleted, notFound := 0, 0
	for _, result := range results {
		deleted += len(result.Deleted)
		for _, item := range result.Skipped {
			if item.Reason == TaskScriptReasonNotFound {
				notFound++
			}
		}
	}
	if deleted != 1 || notFound != 1 {
		t.Fatalf("expected exactly one delete and one not_found, got deleted=%d not_found=%d results=%#v", deleted, notFound, results)
	}
	tscAssertGone(t, full)
}

// ---------- G：单文件订阅下载路径只有一个公式 ----------

// singleFileSubscriptionDestPath 必须与 pullSingleFileWithCallback 实际下载的目标一致。
// 用已取消的 ctx 让下载命令不真正启动，只观察它写出的日志行与建好的目录。
func TestSingleFileSubscriptionDestPathMatchesPull(t *testing.T) {
	testutil.SetupTestEnv(t)
	scriptsDir := config.C.Data.ScriptsDir
	cases := []struct {
		sub         model.Subscription
		wantSaveDir string
		wantFile    string
	}{
		{sub: model.Subscription{URL: "https://example.com/raw/x.js"}, wantSaveDir: "downloads", wantFile: "x.js"},
		{sub: model.Subscription{URL: "https://example.com/raw/x.js", SaveDir: "report", Alias: "daily_report.py"}, wantSaveDir: "report", wantFile: "daily_report.py"},
	}
	for _, tc := range cases {
		sub := tc.sub
		want := filepath.Join(scriptsDir, tc.wantSaveDir, tc.wantFile)
		if got := singleFileSubscriptionDestPath(scriptsDir, &sub); got != want {
			t.Fatalf("unexpected dest path: want %q got %q", want, got)
		}

		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		var emitted []string
		if _, err := pullSingleFileWithCallback(ctx, &sub, "", func(line string) { emitted = append(emitted, line) }); err == nil {
			t.Fatalf("expected the cancelled download to fail")
		}
		wantLine := fmt.Sprintf("[下载] %s -> %s/%s", sub.URL, tc.wantSaveDir, tc.wantFile)
		if len(emitted) == 0 || emitted[0] != wantLine {
			t.Fatalf("unexpected pull log: want %q got %#v", wantLine, emitted)
		}
		// DownloadFileWithContext 会先 MkdirAll 目标文件的父目录，目录建在哪里就说明它下载到哪里。
		if info, err := os.Stat(filepath.Dir(want)); err != nil || !info.IsDir() {
			t.Fatalf("expected pull to prepare %s, got info=%v err=%v", filepath.Dir(want), info, err)
		}
	}
}
