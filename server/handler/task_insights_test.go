package handler

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"daidai-panel/database"
	"daidai-panel/model"
	"daidai-panel/testutil"
)

func TestTaskUpcomingEndpoint(t *testing.T) {
	testutil.SetupTestEnv(t)
	engine := newInsightTestRouter()

	enabled := &model.Task{Name: "每小时", Command: "echo", CronExpression: "0 * * * *", TaskType: model.TaskTypeCron, Status: model.TaskStatusEnabled}
	disabled := &model.Task{Name: "禁用", Command: "echo", CronExpression: "* * * * *", TaskType: model.TaskTypeCron, Status: model.TaskStatusDisabled}
	for _, task := range []*model.Task{enabled, disabled} {
		if err := database.DB.Create(task).Error; err != nil {
			t.Fatal(err)
		}
	}
	// 禁用要在创建后单独写：GORM Create 会跳过零值字段，status=0 会落成列默认值
	database.DB.Model(&model.Task{}).Where("id = ?", disabled.ID).Update("status", model.TaskStatusDisabled)

	rec, payload := insightGet(t, engine, "/api/v1/tasks/upcoming?hours=6", "viewer")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d %s", rec.Code, rec.Body.String())
	}
	if payload["to"].(float64)-payload["from"].(float64) != 6*3600 {
		t.Fatalf("hours=6 时 to-from 应为 21600，实际 %#v", payload)
	}
	tasks := payload["tasks"].([]interface{})
	if len(tasks) != 1 || tasks[0].(map[string]interface{})["id"] != float64(enabled.ID) {
		t.Fatalf("只应包含启用的任务，实际 %#v", tasks)
	}
	// 正好在整点那一秒跑用例时，第 6 个整点落在区间右端（不含）上，只有 5 个点
	if got := len(tasks[0].(map[string]interface{})["times"].([]interface{})); got != 6 && got != 5 {
		t.Fatalf("6 小时内每小时整点应有 6 个点，实际 %d", got)
	}
	if len(payload["hourly"].([]interface{})) != 6 {
		t.Fatalf("hourly 长度应为 6")
	}
	if _, exists := payload["truncated"]; exists {
		t.Fatal("没截断时不应带 truncated")
	}

	// 边界：hours 越界夹到 1~48
	_, payload = insightGet(t, engine, "/api/v1/tasks/upcoming?hours=999", "viewer")
	if len(payload["hourly"].([]interface{})) != 48 {
		t.Fatalf("hours=999 应夹到 48")
	}
	_, payload = insightGet(t, engine, "/api/v1/tasks/upcoming?hours=0", "viewer")
	if len(payload["hourly"].([]interface{})) != 1 {
		t.Fatalf("hours=0 应夹到 1")
	}
}

func TestTaskHealthEndpoint(t *testing.T) {
	testutil.SetupTestEnv(t)
	engine := newInsightTestRouter()

	task := &model.Task{Name: "健康", Command: "echo", CronExpression: "0 * * * *", TaskType: model.TaskTypeCron, Status: model.TaskStatusEnabled}
	if err := database.DB.Create(task).Error; err != nil {
		t.Fatal(err)
	}
	// 今天 3 次（1 次失败）+ 一条运行中（不计）
	now := time.Now()
	for i, status := range []int{model.LogStatusSuccess, model.LogStatusFailed, model.LogStatusSuccess, model.LogStatusRunning} {
		status := status
		duration := float64(i + 1)
		startedAt := now.Add(-time.Duration(4-i) * time.Second)
		taskLog := model.TaskLog{TaskID: task.ID, Status: &status, StartedAt: startedAt}
		if status != model.LogStatusRunning {
			taskLog.Duration = &duration
		}
		if err := database.DB.Create(&taskLog).Error; err != nil {
			t.Fatal(err)
		}
	}

	rec, payload := insightGet(t, engine, fmt.Sprintf("/api/v1/tasks/%d/health?days=7", task.ID), "viewer")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d %s", rec.Code, rec.Body.String())
	}
	days := payload["days"].([]interface{})
	if len(days) != 7 {
		t.Fatalf("days=7 应返回 7 行（没运行的天补 0），实际 %d", len(days))
	}
	last := days[6].(map[string]interface{})
	if last["date"] != now.Format("2006-01-02") || last["total"] != float64(3) || last["failed"] != float64(1) {
		t.Fatalf("今天的统计不对：%#v", last)
	}
	if first := days[0].(map[string]interface{}); first["total"] != float64(0) {
		t.Fatalf("6 天前没有运行，应补 0：%#v", first)
	}
	stats := payload["stats"].(map[string]interface{})
	if stats["runs"] != float64(3) || stats["success_rate"] != 66.7 || stats["p90_duration"] != float64(3) {
		t.Fatalf("stats 不对：%#v", stats)
	}
	if recent := payload["recent"].([]interface{}); len(recent) != 3 || recent[0].(map[string]interface{})["status"] != float64(model.LogStatusSuccess) {
		t.Fatalf("recent 应为 3 条且旧 → 新：%#v", recent)
	}

	// 边界：days 越界夹到 7~180；任务不存在 404
	_, payload = insightGet(t, engine, fmt.Sprintf("/api/v1/tasks/%d/health?days=1", task.ID), "viewer")
	if len(payload["days"].([]interface{})) != 7 {
		t.Fatal("days=1 应夹到 7")
	}
	_, payload = insightGet(t, engine, fmt.Sprintf("/api/v1/tasks/%d/health?days=999", task.ID), "viewer")
	if len(payload["days"].([]interface{})) != 180 {
		t.Fatal("days=999 应夹到 180")
	}
	if rec, _ := insightGet(t, engine, "/api/v1/tasks/999999/health", "viewer"); rec.Code != http.StatusNotFound {
		t.Fatalf("不存在的任务应 404，实际 %d", rec.Code)
	}
}

func TestTaskRecentRunsEndpoint(t *testing.T) {
	testutil.SetupTestEnv(t)
	engine := newInsightTestRouter()

	a := &model.Task{Name: "a", Command: "echo", CronExpression: "0 * * * *", TaskType: model.TaskTypeCron, Status: model.TaskStatusEnabled}
	b := &model.Task{Name: "b", Command: "echo", CronExpression: "0 * * * *", TaskType: model.TaskTypeCron, Status: model.TaskStatusEnabled}
	empty := &model.Task{Name: "empty", Command: "echo", CronExpression: "0 * * * *", TaskType: model.TaskTypeCron, Status: model.TaskStatusEnabled}
	for _, task := range []*model.Task{a, b, empty} {
		if err := database.DB.Create(task).Error; err != nil {
			t.Fatal(err)
		}
	}
	// a：5 条已结束 + 最新一条运行中；b：2 条
	seedFinishedLogs(t, a.ID, []int{model.LogStatusSuccess, model.LogStatusFailed, model.LogStatusSuccess, model.LogStatusSuccess, model.LogStatusFailed}, nil)
	running := model.LogStatusRunning
	database.DB.Create(&model.TaskLog{TaskID: a.ID, Status: &running, StartedAt: time.Now()})
	seedFinishedLogs(t, b.ID, []int{model.LogStatusFailed, model.LogStatusSuccess}, nil)

	rec, payload := insightGet(t, engine, fmt.Sprintf("/api/v1/tasks/recent-runs?ids=%d,%d,%d,%d&limit=3", a.ID, b.ID, empty.ID, a.ID), "viewer")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d %s", rec.Code, rec.Body.String())
	}
	runs := payload["runs"].(map[string]interface{})
	aRuns := runs[fmt.Sprint(a.ID)].([]interface{})
	// a 最近 3 次已结束（旧 → 新）：success, success, failed；运行中那条不算
	wantA := []int{model.LogStatusSuccess, model.LogStatusSuccess, model.LogStatusFailed}
	if len(aRuns) != 3 {
		t.Fatalf("a 应有 3 条，实际 %#v", aRuns)
	}
	for i, want := range wantA {
		if aRuns[i].(map[string]interface{})["status"] != float64(want) {
			t.Fatalf("a 的顺序 / 内容不对：%#v", aRuns)
		}
	}
	if got := aRuns[2].(map[string]interface{})["duration"]; got != float64(5) {
		t.Fatalf("a 最新一条耗时应为 5，实际 %v", got)
	}
	if bRuns := runs[fmt.Sprint(b.ID)].([]interface{}); len(bRuns) != 2 || bRuns[0].(map[string]interface{})["status"] != float64(model.LogStatusFailed) {
		t.Fatalf("b 应为 2 条且旧 → 新：%#v", bRuns)
	}
	if emptyRuns := runs[fmt.Sprint(empty.ID)].([]interface{}); len(emptyRuns) != 0 {
		t.Fatalf("没有日志的任务应为空数组：%#v", emptyRuns)
	}

	// 边界：ids 超过 100 个、格式错误都 400
	ids := make([]string, 0, 101)
	for i := 1; i <= 101; i++ {
		ids = append(ids, fmt.Sprint(i))
	}
	if rec, _ := insightGet(t, engine, "/api/v1/tasks/recent-runs?ids="+strings.Join(ids, ","), "viewer"); rec.Code != http.StatusBadRequest {
		t.Fatalf("101 个 id 应 400，实际 %d", rec.Code)
	}
	if rec, _ := insightGet(t, engine, "/api/v1/tasks/recent-runs?ids=1,abc", "viewer"); rec.Code != http.StatusBadRequest {
		t.Fatalf("格式错误应 400，实际 %d", rec.Code)
	}
}

func TestTaskRunningEndpoint(t *testing.T) {
	testutil.SetupTestEnv(t)
	engine := newInsightTestRouter()

	runningTask := &model.Task{Name: "系统健康检查", Command: "echo", CronExpression: "0 * * * *", TaskType: model.TaskTypeCron, Status: model.TaskStatusRunning}
	idleTask := &model.Task{Name: "idle", Command: "echo", CronExpression: "0 * * * *", TaskType: model.TaskTypeCron, Status: model.TaskStatusEnabled}
	orphan := &model.Task{Name: "刚标记运行", Command: "echo", CronExpression: "0 * * * *", TaskType: model.TaskTypeCron, Status: model.TaskStatusRunning}
	for _, task := range []*model.Task{runningTask, idleTask, orphan} {
		if err := database.DB.Create(task).Error; err != nil {
			t.Fatal(err)
		}
	}
	seedFinishedLogs(t, runningTask.ID, []int{model.LogStatusSuccess}, nil)
	running := model.LogStatusRunning
	current := model.TaskLog{TaskID: runningTask.ID, Status: &running, StartedAt: time.Now()}
	database.DB.Create(&current)

	rec, payload := insightGet(t, engine, "/api/v1/tasks/running", "viewer")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d %s", rec.Code, rec.Body.String())
	}
	items := payload["items"].([]interface{})
	if len(items) != 2 {
		t.Fatalf("应只列出 2 个运行中任务，实际 %#v", items)
	}
	first := items[0].(map[string]interface{})
	if first["task_id"] != float64(runningTask.ID) || first["name"] != "系统健康检查" || first["log_id"] != float64(current.ID) || first["started_at"] == nil {
		t.Fatalf("运行中条目不对：%#v", first)
	}
	// 边界：任务已标记运行、日志行还没建好时 log_id 为 null
	if second := items[1].(map[string]interface{}); second["task_id"] != float64(orphan.ID) || second["log_id"] != nil {
		t.Fatalf("找不到运行中日志时 log_id 应为 null：%#v", second)
	}
}

// 公开接口 panel-settings 要下发全部 v4.0 新键（design §1.3）。
func TestPanelSettingsExposesAppearanceKeys(t *testing.T) {
	testutil.SetupTestEnv(t)
	engine := newInsightTestRouter()
	if err := model.SetConfig("panel_custom_css", ".a{color:red}"); err != nil {
		t.Fatal(err)
	}

	rec, payload := insightGet(t, engine, "/api/v1/system/panel-settings", "viewer")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	data := payload["data"].(map[string]interface{})
	want := map[string]string{
		"panel_theme_preset":      "aurora",
		"panel_accent_color":      "",
		"panel_background_effect": "theme",
		"panel_background_image":  "",
		"panel_glass_blur":        "",
		"panel_density":           "cozy",
		"panel_custom_css":        ".a{color:red}",
		"panel_shape_style":       "theme",
	}
	for key, value := range want {
		got, exists := data[key]
		if !exists || got != value {
			t.Errorf("panel-settings 的 %s 应为 %q，实际 %#v（存在=%v）", key, value, got, exists)
		}
	}
}
