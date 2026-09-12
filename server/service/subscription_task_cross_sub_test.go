package service

import (
	"fmt"
	"strings"
	"testing"

	"daidai-panel/database"
	"daidai-panel/model"
)

// 跨订阅接管之后的自动删除（review-r1 F1）：两个订阅的扫描范围重叠（共用 SaveDir、单文件订阅的 SaveDir 设在别的仓库里）时，
// 订阅 A 会接管订阅 B 的任务（只加 A 的标签）。之后这条任务对 A 失效（A 加黑名单、挪走 SaveDir），对 B 却没有失效：
// A 只能摘掉自己的标签，不能连日志删掉 B 还在用的任务。复用 subscription_task_sync_existing_test.go 的 ste* 辅助函数。

func cssSubscription(t *testing.T, name, saveDir string) *model.Subscription {
	t.Helper()
	sub := &model.Subscription{
		Name: name, Type: model.SubTypeGitRepo, URL: "https://github.com/u/" + name + ".git",
		SaveDir: saveDir, Enabled: true,
		AutoAddTaskMode: model.SubTaskSyncEnabled, AutoDelTaskMode: model.SubTaskSyncEnabled,
	}
	if err := database.DB.Create(sub).Error; err != nil {
		t.Fatal(err)
	}
	return sub
}

// cssUserEdits 模拟用户改过 B 的任务：名称、定时（可选再给命令加参数），并留两条历史日志；返回改完后的库内快照。
func cssUserEdits(t *testing.T, task *model.Task, editCommand bool) model.Task {
	t.Helper()
	updates := map[string]interface{}{"name": "用户改过的 " + task.Name, "cron_expression": "30 7 * * *"}
	if editCommand {
		updates["command"] = task.Command + " desi JD_COOKIE 1"
	}
	if err := database.DB.Model(&model.Task{}).Where("id = ?", task.ID).Updates(updates).Error; err != nil {
		t.Fatal(err)
	}
	steAddLog(t, task.ID)
	steAddLog(t, task.ID)
	got, _ := steReload(task.ID)
	return got
}

// cssAssertLabeled：任务还在，且同时带着 subs 的全部订阅标签。
func cssAssertLabeled(t *testing.T, id uint, logs []string, subs ...*model.Subscription) {
	t.Helper()
	got, ok := steReload(id)
	if !ok {
		t.Fatalf("task %d was deleted\n%s", id, strings.Join(logs, "\n"))
	}
	for _, sub := range subs {
		if !hasLabel(got.GetLabels(), subscriptionTaskLabel(sub.ID)) {
			t.Fatalf("task %d (%s) should carry %s, labels=%q\n%s",
				id, got.Name, subscriptionTaskLabel(sub.ID), got.Labels, strings.Join(logs, "\n"))
		}
	}
}

// cssAssertOnlyOwner：任务行还在，名称、定时、命令、状态与 want 一致，历史日志条数不变，标签只剩 owner 的（只少了 A 的）。
func cssAssertOnlyOwner(t *testing.T, want model.Task, wantLogs int64, owner *model.Subscription, logs []string) {
	t.Helper()
	want.Labels = subscriptionTaskLabel(owner.ID)
	steAssertUnchanged(t, want, logs)
	if n := steLogCount(want.ID); n != wantLogs {
		t.Fatalf("task %d (%s): want %d logs, got %d\n%s", want.ID, want.Name, wantLogs, n, strings.Join(logs, "\n"))
	}
}

// 两个订阅共用 SaveDir：A 接管了 B 的任务（x 改过名称和定时、有历史日志；可选再改过命令，此时是按脚本键接管的）。
// A 加黑名单排除 x.js，或把 SaveDir 挪走：B 的任务行、名称、定时、命令、日志都在，只少了 A 的标签，打 [解除关联]；
// 之后 A、B 再同步都不再有变更，B 不会按上游默认值重建。
func TestSyncSubscriptionTasksCrossSubStaleTaskOnlyDetaches(t *testing.T) {
	for _, trigger := range []string{"blacklist", "save_dir"} {
		for _, editCommand := range []bool{false, true} {
			name := trigger
			if editCommand {
				name += "_edited_command"
			}
			t.Run(name, func(t *testing.T) {
				steEnv(t)
				steWriteScript(t, "shared", "x.js", "1 1 * * *", "x")
				steWriteScript(t, "shared", "y.js", "2 2 * * *", "y")
				b := cssSubscription(t, "b", "shared")
				steSync(b)
				x := cssUserEdits(t, findTaskByCommand(t, steCmd("shared", "x.js")), editCommand)
				y := *findTaskByCommand(t, steCmd("shared", "y.js"))

				a := cssSubscription(t, "a", "shared")
				logs := steSync(a)
				cssAssertLabeled(t, x.ID, logs, a, b)
				cssAssertLabeled(t, y.ID, logs, a, b)

				// 让 shared/x.js 对 A 失效；挪走 SaveDir 时 y.js 也一并失效。
				stale := []model.Task{x}
				if trigger == "blacklist" {
					a.Blacklist = "x.js"
				} else {
					steWriteScript(t, "a_own", "z.js", "3 3 * * *", "z")
					a.SaveDir = "a_own"
					stale = append(stale, y)
				}
				logs = steSync(a)
				cssAssertOnlyOwner(t, x, 2, b, logs)
				if trigger == "blacklist" {
					cssAssertLabeled(t, y.ID, logs, a, b)
				} else {
					cssAssertOnlyOwner(t, y, 0, b, logs)
				}
				for _, task := range stale {
					if !containsLogLine(logs, "[解除关联] "+task.Name) {
						t.Fatalf("missing detach line for %s\n%s", task.Name, strings.Join(logs, "\n"))
					}
				}
				if containsLogLine(logs, "[自动删除任务]") || steCountLines(logs, "[解除关联]") != len(stale) ||
					!containsLogLine(logs, fmt.Sprintf("[共解除关联 %d 个任务]", len(stale))) {
					t.Fatalf("want %d detach lines and no delete\n%s", len(stale), strings.Join(logs, "\n"))
				}

				for _, s := range []*model.Subscription{a, b} {
					logs = steSync(s)
					if !containsLogLine(logs, "[同步完成] 本次未对定时任务做任何变更") {
						t.Fatalf("%s: the next round should be a no-op\n%s", s.Name, strings.Join(logs, "\n"))
					}
				}
				cssAssertOnlyOwner(t, x, 2, b, logs)
			})
		}
	}
}

// 另一条触发：单文件订阅把 SaveDir 设成 B 的仓库目录（方便脚本 require 同目录的 jdCookie / sendNotify），
// 扫描覆盖整个目录，接管了 B 的全部任务；之后用户把单文件订阅的 SaveDir 挪走。
// B 的任务只解除关联；单文件订阅自己的旧任务（只带它自己的标签）照常删，新目录照建。
func TestSyncSubscriptionTasksSingleFileMovedOutOfOtherRepoDetaches(t *testing.T) {
	steEnv(t)
	steWriteScript(t, "repo", "x.js", "1 1 * * *", "x")
	steWriteScript(t, "repo", "y.js", "2 2 * * *", "y")
	b := cssSubscription(t, "b-repo", "repo")
	steSync(b)
	x := cssUserEdits(t, findTaskByCommand(t, steCmd("repo", "x.js")), false)
	y := *findTaskByCommand(t, steCmd("repo", "y.js"))

	a := &model.Subscription{
		Name: "a-single", Type: model.SubTypeSingleFile, URL: "https://example.com/raw/extra.js", SaveDir: "repo", Enabled: true,
		AutoAddTaskMode: model.SubTaskSyncEnabled, AutoDelTaskMode: model.SubTaskSyncEnabled,
	}
	if err := database.DB.Create(a).Error; err != nil {
		t.Fatal(err)
	}
	steWriteScript(t, "repo", "extra.js", "3 3 * * *", "extra") // 单文件订阅下载到 repo/extra.js
	logs := steSync(a)
	cssAssertLabeled(t, x.ID, logs, a, b)
	cssAssertLabeled(t, y.ID, logs, a, b)
	own := findTaskByCommand(t, steCmd("repo", "extra.js"))

	steWriteScript(t, "extras", "extra.js", "3 3 * * *", "extra")
	a.SaveDir = "extras"
	logs = steSync(a)
	cssAssertOnlyOwner(t, x, 2, b, logs)
	cssAssertOnlyOwner(t, y, 0, b, logs)
	if _, exists := steReload(own.ID); exists {
		t.Fatalf("the single-file subscription's own old task should be deleted\n%s", strings.Join(logs, "\n"))
	}
	findTaskByCommand(t, steCmd("extras", "extra.js"))
	if steCountLines(logs, "[解除关联]") != 2 || !containsLogLine(logs, "[共解除关联 2 个任务]") ||
		steCountLines(logs, "[自动删除任务]") != 1 || !containsLogLine(logs, "[自动删除任务] extra") {
		t.Fatalf("want two detach lines and only the own task deleted\n%s", strings.Join(logs, "\n"))
	}

	// B 再同步：x、y 还是原来那两条，不按上游默认值重建。
	logs = steSync(b)
	cssAssertOnlyOwner(t, x, 2, b, logs)
	cssAssertOnlyOwner(t, y, 0, b, logs)
	for _, command := range []string{steCmd("repo", "x.js"), steCmd("repo", "y.js")} {
		var n int64
		database.DB.Model(&model.Task{}).Where("command = ?", command).Count(&n)
		if n != 1 {
			t.Fatalf("%s: want exactly 1 task, got %d\n%s", command, n, strings.Join(logs, "\n"))
		}
	}
}

// 已删订阅留下的悬空旧标签（删订阅不动任务，E9）不算「还被别的订阅使用」：本订阅的失效任务照常连日志删除。
func TestSyncSubscriptionTasksDanglingLabelDoesNotBlockDelete(t *testing.T) {
	steEnv(t)
	steWriteScript(t, "shared", "x.js", "1 1 * * *", "x")
	steWriteScript(t, "shared", "y.js", "2 2 * * *", "y")
	b := cssSubscription(t, "b", "shared")
	steSync(b)
	x := findTaskByCommand(t, steCmd("shared", "x.js"))
	steAddLog(t, x.ID)
	a := cssSubscription(t, "a", "shared")
	logs := steSync(a)
	cssAssertLabeled(t, x.ID, logs, a, b)
	if err := database.DB.Delete(&model.Subscription{}, b.ID).Error; err != nil {
		t.Fatal(err)
	}
	// 另一条只带着「从来没存在过的订阅」的旧标签、指向已删脚本的任务，同样照删。
	orphan := steNewTask(t, "orphan", steCmd("shared", "gone.js"), "0 3 * * *", "subscription:999", subscriptionTaskLabel(a.ID))
	steAddLog(t, orphan.ID)

	a.Blacklist = "x.js"
	logs = steSync(a)
	for _, task := range []*model.Task{x, orphan} {
		if _, exists := steReload(task.ID); exists || steLogCount(task.ID) != 0 {
			t.Fatalf("%s should be deleted with its logs\n%s", task.Name, strings.Join(logs, "\n"))
		}
		if !containsLogLine(logs, "[自动删除任务] "+task.Name) {
			t.Fatalf("missing delete line for %s\n%s", task.Name, strings.Join(logs, "\n"))
		}
	}
	if containsLogLine(logs, "[解除关联]") {
		t.Fatalf("dangling labels must not block deletion\n%s", strings.Join(logs, "\n"))
	}
}

// 条件解除关联：快照之后标签或命令被改过 → 标签不动、任务不删、日志不动；快照与库一致 → 只摘掉给定的那个标签。
func TestDetachSubscriptionTaskIfUnchangedSkipsChangedTask(t *testing.T) {
	steEnv(t)
	task := steNewTask(t, "shared", "task repo/x.js", "0 3 * * *", "subscription:1", "subscription:2")
	steAddLog(t, task.ID)

	for _, change := range []struct{ column, value string }{
		{"labels", "subscription:1,subscription:2,我的标签"},
		{"command", "task repo/x.js now"},
	} {
		snapshot, _ := steReload(task.ID)
		if err := database.DB.Model(&model.Task{}).Where("id = ?", task.ID).Update(change.column, change.value).Error; err != nil {
			t.Fatal(err)
		}
		changed, _ := steReload(task.ID)
		detached, err := detachSubscriptionTaskIfUnchanged(&snapshot, "subscription:2")
		if err != nil || detached {
			t.Fatalf("%s changed: a stale snapshot must not detach, got detached=%v err=%v", change.column, detached, err)
		}
		steAssertUnchanged(t, changed, nil)
		if steLogCount(task.ID) != 1 {
			t.Fatalf("%s changed: task logs must survive", change.column)
		}
	}

	fresh, _ := steReload(task.ID)
	detached, err := detachSubscriptionTaskIfUnchanged(&fresh, "subscription:2")
	if err != nil || !detached {
		t.Fatalf("up-to-date snapshot should detach, got detached=%v err=%v", detached, err)
	}
	want := fresh
	want.Labels = "subscription:1,我的标签"
	steAssertUnchanged(t, want, nil)
	if steLogCount(task.ID) != 1 {
		t.Fatal("detaching must not touch task logs")
	}
}

// 解除关联那一步出错：计入失败并打日志；任务、标签、日志都不动，也不退回去删除。
func TestSyncSubscriptionTasksReportsFailedDetach(t *testing.T) {
	steEnv(t)
	steWriteScript(t, "shared", "x.js", "1 1 * * *", "x")
	steWriteScript(t, "shared", "y.js", "2 2 * * *", "y")
	b := cssSubscription(t, "b", "shared")
	steSync(b)
	x := findTaskByCommand(t, steCmd("shared", "x.js"))
	steAddLog(t, x.ID)
	a := cssSubscription(t, "a", "shared")
	steSync(a)
	adopted, _ := steReload(x.ID)
	if err := database.DB.Exec(`CREATE TRIGGER css_block_label_update BEFORE UPDATE OF labels ON tasks BEGIN SELECT RAISE(ABORT, 'blocked by test'); END;`).Error; err != nil {
		t.Fatalf("create trigger: %v", err)
	}

	a.Blacklist = "x.js"
	logs := steSync(a)
	steAssertUnchanged(t, adopted, logs)
	if steLogCount(x.ID) != 1 {
		t.Fatalf("task logs must survive\n%s", strings.Join(logs, "\n"))
	}
	if !containsLogLine(logs, "[解除关联失败] x") || !containsLogLine(logs, "[警告] 共 1 个任务操作失败") ||
		containsLogLine(logs, "[自动删除任务]") || containsLogLine(logs, "[解除关联] x") {
		t.Fatalf("want a failure line and no delete\n%s", strings.Join(logs, "\n"))
	}
}

// review-r2 R2-2：别的订阅的标签只差大小写（用户手写成 Subscription:<id>）。queryTasksByLabel 的 LIKE 不分大小写认它，
// usedByOtherSubscription 也必须忽略大小写，否则 A 会把 B 还在用的任务连日志删掉。只解除关联、不删。
func TestSyncSubscriptionTasksCaseVariantOtherLabelOnlyDetaches(t *testing.T) {
	steEnv(t)
	steWriteScript(t, "shared", "x.js", "1 1 * * *", "x")
	steWriteScript(t, "shared", "y.js", "2 2 * * *", "y") // 让候选非空、不触发熔断
	a := cssSubscription(t, "a", "shared")
	b := cssSubscription(t, "b", "shared")
	labelA := subscriptionTaskLabel(a.ID)
	labelBVariant := fmt.Sprintf("Subscription:%d", b.ID) // B 的标签大小写变体
	task := steNewTask(t, "x", steCmd("shared", "x.js")+" now", "0 3 * * *", labelA, labelBVariant)
	steAddLog(t, task.ID)

	a.Blacklist = "x.js" // 让 shared/x.js 对 A 失效
	logs := steSync(a)

	got, ok := steReload(task.ID)
	if !ok || steLogCount(task.ID) != 1 {
		t.Fatalf("task and its log must survive (B still uses it)\n%s", strings.Join(logs, "\n"))
	}
	if hasLabel(got.GetLabels(), labelA) {
		t.Fatalf("A's label should be removed, labels=%q\n%s", got.Labels, strings.Join(logs, "\n"))
	}
	if !hasLabel(got.GetLabels(), labelBVariant) {
		t.Fatalf("B's case-variant label must survive, labels=%q\n%s", got.Labels, strings.Join(logs, "\n"))
	}
	if containsLogLine(logs, "[自动删除任务]") || !containsLogLine(logs, "[解除关联] x") {
		t.Fatalf("want detach and no delete\n%s", strings.Join(logs, "\n"))
	}
}

// review-r2 R2-2：本订阅自己的标签只差大小写。withoutLabel 必须忽略大小写才能真正摘掉它——否则 UPDATE 写回原值，
// 每次同步都空转一次「[解除关联]」。这里断言：一次摘掉本订阅的大小写变体标签，下一轮不再空转。
func TestSyncSubscriptionTasksCaseVariantOwnLabelDetachesWithoutSpin(t *testing.T) {
	steEnv(t)
	steWriteScript(t, "shared", "x.js", "1 1 * * *", "x")
	steWriteScript(t, "shared", "y.js", "2 2 * * *", "y")
	a := cssSubscription(t, "a", "shared")
	b := cssSubscription(t, "b", "shared")
	labelAVariant := fmt.Sprintf("Subscription:%d", a.ID) // A 自己的标签大小写变体
	labelB := subscriptionTaskLabel(b.ID)
	task := steNewTask(t, "x", steCmd("shared", "x.js")+" now", "0 3 * * *", labelAVariant, labelB)
	steAddLog(t, task.ID)

	a.Blacklist = "x.js"
	logs := steSync(a)

	got, ok := steReload(task.ID)
	if !ok || steLogCount(task.ID) != 1 {
		t.Fatalf("task and its log must survive\n%s", strings.Join(logs, "\n"))
	}
	if hasLabelFold(got.GetLabels(), labelAVariant) {
		t.Fatalf("A's case-variant label must be removed, labels=%q\n%s", got.Labels, strings.Join(logs, "\n"))
	}
	if !hasLabel(got.GetLabels(), labelB) {
		t.Fatalf("B's label must survive, labels=%q\n%s", got.Labels, strings.Join(logs, "\n"))
	}
	if steCountLines(logs, "[解除关联]") != 1 {
		t.Fatalf("want exactly one detach line\n%s", strings.Join(logs, "\n"))
	}

	// 再同步：任务已不带 A 的任何大小写标签，A 不再托管它，不空转。
	logs = steSync(a)
	if containsLogLine(logs, "[解除关联]") || containsLogLine(logs, "[自动删除任务]") {
		t.Fatalf("second sync must not spin on the detached task\n%s", strings.Join(logs, "\n"))
	}
}
