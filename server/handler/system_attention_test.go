package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"daidai-panel/config"
	"daidai-panel/database"
	"daidai-panel/model"
	"daidai-panel/testutil"

	"github.com/gin-gonic/gin"
)

// 本文件与 task_insights_test.go 用内部测试包（package handler），是为了能替换 attentionDiskUsage：
// 真实磁盘使用率随机器而变，不替换的话 admin 那一档的条目数不可预期。

func newInsightTestRouter() *gin.Engine {
	engine := gin.New()
	api := engine.Group("/api/v1")
	NewSystemHandler().RegisterRoutes(api)
	NewTaskHandler().RegisterRoutes(api)
	return engine
}

func insightGet(t *testing.T, engine *gin.Engine, path, role string) (*httptest.ResponseRecorder, map[string]interface{}) {
	t.Helper()
	user := testutil.MustCreateUser(t, "insight-"+role+"-"+time.Now().Format("150405.000000000"), role)
	token := testutil.MustCreateAccessToken(t, user.Username, user.Role)
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	payload := map[string]interface{}{}
	_ = json.Unmarshal(rec.Body.Bytes(), &payload)
	return rec, payload
}

// seedFinishedLogs 给任务按「旧 → 新」写一串已结束日志，最新一条的时间最大。
func seedFinishedLogs(t *testing.T, taskID uint, statuses []int, logPath *string) []model.TaskLog {
	t.Helper()
	base := time.Now().Add(-time.Hour)
	logs := make([]model.TaskLog, 0, len(statuses))
	for i, status := range statuses {
		status := status
		duration := float64(i + 1)
		startedAt := base.Add(time.Duration(i) * time.Minute)
		endedAt := startedAt.Add(30 * time.Second)
		taskLog := model.TaskLog{TaskID: taskID, Status: &status, Duration: &duration, StartedAt: startedAt, EndedAt: &endedAt}
		if i == len(statuses)-1 {
			taskLog.LogPath = logPath
		}
		if err := database.DB.Create(&taskLog).Error; err != nil {
			t.Fatalf("create log: %v", err)
		}
		logs = append(logs, taskLog)
	}
	return logs
}

func attentionKeys(t *testing.T, payload map[string]interface{}) []string {
	t.Helper()
	items, ok := payload["items"].([]interface{})
	if !ok {
		t.Fatalf("expected items array, got %#v", payload)
	}
	keys := make([]string, 0, len(items))
	for _, raw := range items {
		item := raw.(map[string]interface{})
		keys = append(keys, item["key"].(string))
	}
	return keys
}

func TestAttentionTaskFailingThresholdAndRoleScoping(t *testing.T) {
	testutil.SetupTestEnv(t)
	previousDisk := attentionDiskUsage
	attentionDiskUsage = func() float64 { return 90 }
	t.Cleanup(func() { attentionDiskUsage = previousDisk })
	engine := newInsightTestRouter()

	failed := model.RunFailed
	mkTask := func(name string, status float64) *model.Task {
		task := &model.Task{Name: name, Command: "echo", CronExpression: "0 * * * *", TaskType: model.TaskTypeCron, Status: status, LastRunStatus: &failed}
		if err := database.DB.Create(task).Error; err != nil {
			t.Fatalf("create task: %v", err)
		}
		return task
	}

	// 连续失败 3 次：要出；最近一条日志写在文件里，detail 取文件末尾的错误行
	threeFailed := mkTask("更新 IP 数据库", model.TaskStatusEnabled)
	relPath := "task_attention/last.log"
	if err := os.MkdirAll(filepath.Join(config.C.Data.LogDir, "task_attention"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(config.C.Data.LogDir, relPath), []byte("开始\nError: 连接超时（30s）\n=== 执行结束 ===\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	seedFinishedLogs(t, threeFailed.ID, []int{model.LogStatusSuccess, model.LogStatusFailed, model.LogStatusFailed, model.LogStatusFailed}, &relPath)

	// 只连续失败 2 次：不出（阈值边界）
	twoFailed := mkTask("two failed", model.TaskStatusEnabled)
	seedFinishedLogs(t, twoFailed.ID, []int{model.LogStatusSuccess, model.LogStatusFailed, model.LogStatusFailed}, nil)

	// 3 次失败但被一次「已终止」打断：不出
	interrupted := mkTask("interrupted", model.TaskStatusEnabled)
	seedFinishedLogs(t, interrupted.ID, []int{model.LogStatusFailed, model.LogStatusFailed, model.LogStatusAborted, model.LogStatusFailed}, nil)

	// 禁用的任务连续失败也不出
	disabled := mkTask("disabled", model.TaskStatusDisabled)
	seedFinishedLogs(t, disabled.ID, []int{model.LogStatusFailed, model.LogStatusFailed, model.LogStatusFailed}, nil)

	// 订阅失败 + 拉取日志
	sub := &model.Subscription{Name: "示例仓库", Type: model.SubTypeGitRepo, URL: "https://example.com/r.git", Status: 1}
	if err := database.DB.Create(sub).Error; err != nil {
		t.Fatal(err)
	}
	database.DB.Create(&model.SubLog{SubscriptionID: sub.ID, Status: 1, Content: "fatal: unable to access repo\n"})
	okSub := &model.Subscription{Name: "正常仓库", Type: model.SubTypeGitRepo, URL: "https://example.com/ok.git", Status: 0}
	database.DB.Create(okSub)

	// 依赖失败
	dep := &model.Dependency{Type: model.DepTypePython, Name: "requests", Status: model.DepStatusFailed, Log: "ERROR: No matching distribution found for requests\n"}
	if err := database.DB.Create(dep).Error; err != nil {
		t.Fatal(err)
	}

	taskKey := "task_failing:" + itoa(threeFailed.ID)
	subKey := "subscription_failed:" + itoa(sub.ID)
	depKey := "dependency_failed:" + itoa(dep.ID)

	cases := []struct {
		role string
		want []string
	}{
		{"viewer", []string{taskKey}},
		{"operator", []string{taskKey, subKey}},
		{"admin", []string{taskKey}},
	}
	for _, tc := range cases {
		rec, payload := insightGet(t, engine, "/api/v1/system/attention", tc.role)
		if rec.Code != http.StatusOK {
			t.Fatalf("[%s] expected 200, got %d %s", tc.role, rec.Code, rec.Body.String())
		}
		keys := attentionKeys(t, payload)
		if tc.role == "admin" {
			// admin：任务(err) 排第一；其余三条 warn 都在
			if len(keys) != 4 || keys[0] != taskKey {
				t.Fatalf("[admin] 期望 4 条且任务在首位，实际 %v", keys)
			}
			for _, k := range []string{subKey, depKey, "disk_high"} {
				if !containsString(keys, k) {
					t.Fatalf("[admin] 缺少 %s：%v", k, keys)
				}
			}
			continue
		}
		if len(keys) != len(tc.want) {
			t.Fatalf("[%s] 期望 %v，实际 %v", tc.role, tc.want, keys)
		}
		for i := range keys {
			if keys[i] != tc.want[i] {
				t.Fatalf("[%s] 期望 %v，实际 %v", tc.role, tc.want, keys)
			}
		}
	}

	// 条目内容：标题、级别、目标、detail
	_, payload := insightGet(t, engine, "/api/v1/system/attention", "viewer")
	item := payload["items"].([]interface{})[0].(map[string]interface{})
	if item["type"] != "task_failing" || item["level"] != "err" {
		t.Fatalf("task_failing 条目类型 / 级别不对：%#v", item)
	}
	if item["title"] != "更新 IP 数据库 · 连续失败 3 次" {
		t.Fatalf("标题不对：%v", item["title"])
	}
	if item["detail"] != "最近一次：Error: 连接超时（30s）" {
		t.Fatalf("detail 不对：%v", item["detail"])
	}
	target := item["target"].(map[string]interface{})
	if target["kind"] != "task" || target["id"] != float64(threeFailed.ID) {
		t.Fatalf("target 不对：%#v", target)
	}
	if _, err := time.Parse(time.RFC3339, item["at"].(string)); err != nil {
		t.Fatalf("at 应为 RFC3339：%v", item["at"])
	}
}

// 边界：磁盘使用率低于 85% 不出；什么都没有时 items 是空数组而不是 null。
func TestAttentionEmptyListAndDiskBelowThreshold(t *testing.T) {
	testutil.SetupTestEnv(t)
	previousDisk := attentionDiskUsage
	attentionDiskUsage = func() float64 { return 84.9 }
	t.Cleanup(func() { attentionDiskUsage = previousDisk })
	engine := newInsightTestRouter()

	rec, _ := insightGet(t, engine, "/api/v1/system/attention", "admin")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	if string(raw["items"]) != "[]" {
		t.Fatalf("空清单应下发 []，实际 %s", raw["items"])
	}
}

func itoa(id uint) string {
	b, _ := json.Marshal(id)
	return string(b)
}

func containsString(list []string, target string) bool {
	for _, item := range list {
		if item == target {
			return true
		}
	}
	return false
}
