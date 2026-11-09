package handler_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"daidai-panel/database"
	"daidai-panel/handler"
	"daidai-panel/model"
	"daidai-panel/testutil"

	"github.com/gin-gonic/gin"
)

// 安全审计 M-1（v3.4.4）：触发链接要认「签发应用的存在 / 启用状态」与「任务是否禁用」，并且限流。
// 这组用例复用 wecom_callback_test.go 里的测试辅助（同一个 handler_test 包）。

// issueTaskTriggerPath 以 app 的名义为 task 签发一条触发链接，返回它的相对路径（/api/v1/...）。
func issueTaskTriggerPath(t *testing.T, engine *gin.Engine, appID, taskID uint) string {
	t.Helper()
	headers := map[string]string{"Authorization": "Bearer " + testutil.MustCreateAccessToken(t, "admin", "admin")}
	rec := doWecomJSON(t, engine, http.MethodPost, fmt.Sprintf("/api/v1/open-api/apps/%d/task-trigger-link", appID),
		map[string]any{"task_id": taskID}, headers)
	if rec.Code != http.StatusOK {
		t.Fatalf("签发链接期望 200，实际 %d：%s", rec.Code, rec.Body.String())
	}
	var issued struct {
		Data struct {
			Path   string `json:"path"`
			Notice string `json:"notice"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &issued); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	// 签发时就要告诉管理员：禁用应用 / 任务也能让链接失效，不只有「作废全部」一条路。
	if !strings.Contains(issued.Data.Notice, "禁用任务") {
		t.Fatalf("notice 应当提示禁用应用或任务即可让链接失效，实际: %s", issued.Data.Notice)
	}
	return issued.Data.Path
}

func TestTaskTriggerRejectedWhenTaskDisabled(t *testing.T) {
	engine := newWecomTestEngine(t)
	setWecomSwitches(t, true, true)
	app := testutil.MustCreateOpenApp(t, "wecom-link-app", "tasks")
	task := mustCreateWecomTask(t, "签到任务")
	path := issueTaskTriggerPath(t, engine, app.ID, task.ID)

	if err := database.DB.Model(&model.Task{}).Where("id = ?", task.ID).Update("status", model.TaskStatusDisabled).Error; err != nil {
		t.Fatalf("disable task: %v", err)
	}

	page := doWecomGET(t, engine, path, nil)
	if page.Code != http.StatusForbidden {
		t.Fatalf("任务禁用后期望 403，实际 %d：%s", page.Code, page.Body.String())
	}
	if !strings.Contains(page.Body.String(), "任务已禁用") {
		t.Fatalf("应当提示任务已禁用，实际: %s", page.Body.String())
	}
}

func TestTaskTriggerRejectedWhenAppDisabledOrDeleted(t *testing.T) {
	engine := newWecomTestEngine(t)
	setWecomSwitches(t, true, true)
	disabledApp := testutil.MustCreateOpenApp(t, "wecom-link-disabled", "tasks")
	deletedApp := testutil.MustCreateOpenApp(t, "wecom-link-deleted", "tasks")
	task := mustCreateWecomTask(t, "签到任务")
	disabledPath := issueTaskTriggerPath(t, engine, disabledApp.ID, task.ID)
	deletedPath := issueTaskTriggerPath(t, engine, deletedApp.ID, task.ID)

	if err := database.DB.Model(&model.OpenApp{}).Where("id = ?", disabledApp.ID).Update("enabled", false).Error; err != nil {
		t.Fatalf("disable app: %v", err)
	}
	if err := database.DB.Where("id = ?", deletedApp.ID).Delete(&model.OpenApp{}).Error; err != nil {
		t.Fatalf("delete app: %v", err)
	}

	// 两种情况都与「票据无效」同一口径：401 + 链接已失效，不暴露到底是哪一种。
	for name, path := range map[string]string{"应用已禁用": disabledPath, "应用已删除": deletedPath} {
		page := doWecomGET(t, engine, path, nil)
		if page.Code != http.StatusUnauthorized {
			t.Fatalf("%s时期望 401，实际 %d：%s", name, page.Code, page.Body.String())
		}
		if !strings.Contains(page.Body.String(), "链接已失效") {
			t.Fatalf("%s时应当回「链接已失效」，实际: %s", name, page.Body.String())
		}
	}
}

// TestTaskTriggerPerTaskRateLimitSharedAcrossPrefixes：同一个任务 10 秒内只受理 1 次，
// 并且 /api 与 /api/v1 共用这个桶，换个前缀刷不出第二次。
func TestTaskTriggerPerTaskRateLimitSharedAcrossPrefixes(t *testing.T) {
	engine := newWecomTestEngine(t)
	setWecomSwitches(t, true, true)
	app := testutil.MustCreateOpenApp(t, "wecom-link-app", "tasks")
	task := mustCreateWecomTask(t, "签到任务")
	path := issueTaskTriggerPath(t, engine, app.ID, task.ID)

	// 第一次受理（测试环境没装调度器，入队会失败，页面是「未能触发」，但仍是 200 且已占掉这一次额度）。
	page := doWecomGET(t, engine, path, nil)
	if page.Code != http.StatusOK {
		t.Fatalf("第一次期望 200，实际 %d：%s", page.Code, page.Body.String())
	}

	legacyPath := strings.Replace(path, "/api/v1/open-api/trigger", "/api/open-api/trigger", 1)
	if legacyPath == path {
		t.Fatalf("签发路径不是预期的 /api/v1 形态: %s", path)
	}
	page = doWecomGET(t, engine, legacyPath, nil)
	if page.Code != http.StatusTooManyRequests {
		t.Fatalf("10 秒内换前缀再触发期望 429，实际 %d：%s", page.Code, page.Body.String())
	}
	if !strings.Contains(page.Body.String(), "操作太频繁") {
		t.Fatalf("应当提示操作太频繁，实际: %s", page.Body.String())
	}
}

// TestTaskTriggerPerTaskRateLimitIgnoresInvalidTickets：没有有效票据的请求不占任务额度，
// 否则谁都能拿着 task_id 狂打，把正常用户的点击一直挡在外面。
func TestTaskTriggerPerTaskRateLimitIgnoresInvalidTickets(t *testing.T) {
	engine := newWecomTestEngine(t)
	setWecomSwitches(t, true, true)
	app := testutil.MustCreateOpenApp(t, "wecom-link-app", "tasks")
	task := mustCreateWecomTask(t, "签到任务")
	path := issueTaskTriggerPath(t, engine, app.ID, task.ID)

	bogus := fmt.Sprintf("/api/v1/open-api/trigger?task_id=%d&ticket=bogus", task.ID)
	if page := doWecomGET(t, engine, bogus, nil); page.Code != http.StatusUnauthorized {
		t.Fatalf("垃圾票期望 401，实际 %d：%s", page.Code, page.Body.String())
	}

	if page := doWecomGET(t, engine, path, nil); page.Code != http.StatusOK {
		t.Fatalf("有效链接期望 200，实际 %d：%s", page.Code, page.Body.String())
	}
}

// TestTaskTriggerJunkTicketsNeverRateLimited：不再按 IP 限流。反代后面所有人共用一个 IP，
// 拿垃圾票狂打的人不能把大家的链接都打成 429；垃圾票一律 401，有效链接照常受理。
func TestTaskTriggerJunkTicketsNeverRateLimited(t *testing.T) {
	engine := newWecomTestEngine(t)
	setWecomSwitches(t, true, true)
	app := testutil.MustCreateOpenApp(t, "wecom-link-app", "tasks")
	task := mustCreateWecomTask(t, "签到任务")
	path := issueTaskTriggerPath(t, engine, app.ID, task.ID)

	for i := 1; i <= 30; i++ {
		page := doWecomGET(t, engine, "/api/v1/open-api/trigger?task_id=1&ticket=bogus", nil)
		if page.Code != http.StatusUnauthorized {
			t.Fatalf("第 %d 次垃圾票期望 401，实际 %d：%s", i, page.Code, page.Body.String())
		}
	}
	if page := doWecomGET(t, engine, path, nil); page.Code != http.StatusOK {
		t.Fatalf("同一 IP 狂打垃圾票后，有效链接仍应 200，实际 %d：%s", page.Code, page.Body.String())
	}
}

// TestTaskTriggerDeniedTapDoesNotExtendCooldown：冷却内被拒的点击不能把冷却往后推，
// 否则隔几秒点一下就能让这个任务永远触发不了。用拨表代替真的等 10 秒。
func TestTaskTriggerDeniedTapDoesNotExtendCooldown(t *testing.T) {
	engine := newWecomTestEngine(t)
	setWecomSwitches(t, true, true)
	app := testutil.MustCreateOpenApp(t, "wecom-link-app", "tasks")
	task := mustCreateWecomTask(t, "签到任务")
	path := issueTaskTriggerPath(t, engine, app.ID, task.ID)

	base := time.Now()
	now := base
	handler.SetTaskTriggerClockForTest(t, func() time.Time { return now })

	if page := doWecomGET(t, engine, path, nil); page.Code != http.StatusOK {
		t.Fatalf("第一次期望 200，实际 %d：%s", page.Code, page.Body.String())
	}

	// 5 秒后再点：还在冷却里，429。
	now = base.Add(5 * time.Second)
	if page := doWecomGET(t, engine, path, nil); page.Code != http.StatusTooManyRequests {
		t.Fatalf("冷却内期望 429，实际 %d：%s", page.Code, page.Body.String())
	}

	// 距第一次受理 11 秒：冷却按第一次算已经过了。若被拒的那一下延长了冷却（要到 15 秒），这里会是 429。
	now = base.Add(11 * time.Second)
	if page := doWecomGET(t, engine, path, nil); page.Code != http.StatusOK {
		t.Fatalf("冷却按受理时间算，11 秒后期望 200，实际 %d：%s", page.Code, page.Body.String())
	}
}
