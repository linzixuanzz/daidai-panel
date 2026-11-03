package handler_test

import (
	"fmt"
	"net/http"
	"testing"

	"daidai-panel/database"
	"daidai-panel/model"
	"daidai-panel/testutil"
)

// 超时 / 最大重试次数 / 重试间隔：越界或非整数在创建时回 400，上限与网页任务表单一致。
func TestCreateTaskRejectsOutOfRangeNumericFields(t *testing.T) {
	testutil.SetupTestEnv(t)

	engine := newProtectedRouter()
	user := testutil.MustCreateUser(t, "task-bounds-operator", "operator")
	headers := map[string]string{"Authorization": "Bearer " + testutil.MustCreateAccessToken(t, user.Username, user.Role)}

	rejected := []string{
		`"timeout":-1`,
		`"timeout":604801`,
		`"max_retries":-1`,
		`"max_retries":11`,
		`"max_retries":1000000`,
		`"retry_interval":-5`,
		`"retry_interval":3601`,
	}
	for _, field := range rejected {
		body := fmt.Sprintf(`{"name":"越界","command":"echo hi","task_type":"manual",%s}`, field)
		rec := performJSONRequest(engine, http.MethodPost, "/api/v1/tasks", body, headers, "")
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 for %s, got %d: %s", field, rec.Code, rec.Body.String())
		}
	}
	var count int64
	database.DB.Model(&model.Task{}).Count(&count)
	if count != 0 {
		t.Fatalf("expected no task created, got %d", count)
	}

	body := `{"name":"边界","command":"echo hi","task_type":"manual","timeout":604800,"max_retries":10,"retry_interval":3600}`
	rec := performJSONRequest(engine, http.MethodPost, "/api/v1/tasks", body, headers, "")
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected boundary values accepted, got %d: %s", rec.Code, rec.Body.String())
	}
}

// 更新：改成越界值回 400 且什么都不写；库里已有的越界旧值原样回传（网页 / APP 保存整张表单）不拦。
func TestUpdateTaskValidatesChangedNumericFields(t *testing.T) {
	testutil.SetupTestEnv(t)

	engine := newProtectedRouter()
	user := testutil.MustCreateUser(t, "task-bounds-editor", "operator")
	headers := map[string]string{"Authorization": "Bearer " + testutil.MustCreateAccessToken(t, user.Username, user.Role)}

	task := model.Task{Name: "旧任务", Command: "echo old", TaskType: model.TaskTypeManual, Status: model.TaskStatusEnabled, MaxRetries: 99, RetryInterval: 60}
	if err := database.DB.Create(&task).Error; err != nil {
		t.Fatalf("create task: %v", err)
	}
	path := fmt.Sprintf("/api/v1/tasks/%d", task.ID)

	rec := performJSONRequest(engine, http.MethodPut, path, `{"name":"改名","max_retries":99,"retry_interval":60}`, headers, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected unchanged legacy value to pass, got %d: %s", rec.Code, rec.Body.String())
	}

	before := mustLoadTask(t, task.ID)
	for _, body := range []string{`{"max_retries":50,"name":"不该写"}`, `{"timeout":-1}`, `{"retry_interval":1.5}`, `{"timeout":null}`} {
		rec = performJSONRequest(engine, http.MethodPut, path, body, headers, "")
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 for %s, got %d: %s", body, rec.Code, rec.Body.String())
		}
	}
	if after := mustLoadTask(t, task.ID); after.Name != before.Name || after.MaxRetries != 99 || after.Timeout != before.Timeout || after.RetryInterval != 60 {
		t.Fatalf("expected rejected updates to write nothing, got %+v", after)
	}

	rec = performJSONRequest(engine, http.MethodPut, path, `{"max_retries":3,"timeout":7200,"retry_interval":30}`, headers, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected in-range update accepted, got %d: %s", rec.Code, rec.Body.String())
	}
	if after := mustLoadTask(t, task.ID); after.MaxRetries != 3 || after.Timeout != 7200 || after.RetryInterval != 30 {
		t.Fatalf("expected in-range values saved, got %+v", after)
	}
}
