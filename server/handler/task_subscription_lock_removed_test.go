package handler_test

import (
	"net/http"
	"strconv"
	"strings"
	"testing"

	"daidai-panel/database"
	"daidai-panel/model"
	"daidai-panel/testutil"
)

// #125 下线订阅锁：解锁接口已删除；改订阅任务的名称、定时只是普通更新，响应里也不再有 subscription_locked。
func TestSubscriptionLockAPIRemoved(t *testing.T) {
	testutil.SetupTestEnv(t)

	engine := newProtectedRouter()
	user := testutil.MustCreateUser(t, "sub-lock-removed-operator", "operator")
	token := testutil.MustCreateAccessToken(t, user.Username, user.Role)
	auth := map[string]string{"Authorization": "Bearer " + token}

	task := model.Task{
		Name:           "订阅任务",
		Command:        "task repo/a.js",
		CronExpression: "1 1 * * *",
		TaskType:       model.TaskTypeCron,
		Status:         model.TaskStatusEnabled,
	}
	task.SetLabelsFromSlice([]string{"subscription:1"})
	if err := database.DB.Select("*").Create(&task).Error; err != nil {
		t.Fatalf("create task: %v", err)
	}
	path := "/api/v1/tasks/" + strconv.FormatUint(uint64(task.ID), 10)

	resp := performJSONRequest(engine, http.MethodPut, path+"/restore-subscription-default", "", auth, "")
	if resp.Code != http.StatusNotFound {
		t.Fatalf("restore-subscription-default should be gone, got %d: %s", resp.Code, resp.Body.String())
	}

	resp = performJSONRequest(engine, http.MethodPut, path, `{"name":"改过的名字","cron_expression":"2 2 * * *"}`, auth, "")
	if resp.Code != http.StatusOK {
		t.Fatalf("update task: %d %s", resp.Code, resp.Body.String())
	}
	if strings.Contains(resp.Body.String(), "subscription_locked") {
		t.Fatalf("task output must not carry subscription_locked any more: %s", resp.Body.String())
	}
	var after model.Task
	if err := database.DB.First(&after, task.ID).Error; err != nil {
		t.Fatalf("reload task: %v", err)
	}
	if after.Name != "改过的名字" || after.CronExpression != "2 2 * * *" {
		t.Fatalf("update not applied: name=%q cron=%q", after.Name, after.CronExpression)
	}
}
