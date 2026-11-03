package handler_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"daidai-panel/database"
	"daidai-panel/model"
	"daidai-panel/testutil"
)

// mustLoadTask 按 ID 从库里读回任务，用来比对「库变没变」。
func mustLoadTask(t *testing.T, id uint) model.Task {
	t.Helper()

	var task model.Task
	if err := database.DB.First(&task, id).Error; err != nil {
		t.Fatalf("load task %d: %v", id, err)
	}
	return task
}

// mustCreateTypedTask 直接在库里建一个指定类型的任务，cron_expression 原样落库。
// 手动 / 开机任务正常经接口保存时 cron_expression 是空串；这里给它们留一段旧值，
// 才看得出「传 null 之后服务端确实把它写成了空串」，而不是压根没写。
func mustCreateTypedTask(t *testing.T, name, taskType, cronExpression string) model.Task {
	t.Helper()

	task := model.Task{
		Name:           name,
		Command:        "echo typed",
		CronExpression: cronExpression,
		TaskType:       taskType,
		Status:         model.TaskStatusEnabled,
	}
	if err := database.DB.Create(&task).Error; err != nil {
		t.Fatalf("create %s task %q: %v", taskType, name, err)
	}
	return task
}

// #157：PUT /tasks/:id 的 labels 原样落库、原样读回（含分组与订阅这两类内部标签），传空数组清空。
// 后端不丢标签；「填了标签却没保存」的根因在网页没提交，不在这里。
func TestUpdateTaskLabelsRoundTrip(t *testing.T) {
	testutil.SetupTestEnv(t)

	engine := newProtectedRouter()
	user := testutil.MustCreateUser(t, "update-labels-operator", "operator")
	headers := map[string]string{"Authorization": "Bearer " + testutil.MustCreateAccessToken(t, user.Username, user.Role)}

	task := mustCreateLabeledTask(t, "标签往返", "已有", "subscription:3")
	path := fmt.Sprintf("/api/v1/tasks/%d", task.ID)

	// 网页表单真实发出的形状：labels 整体覆写，内部标签由前端原样带回。
	want := []string{"已有", "新标签", "subscription:3", "分组:京东"}
	rec := performJSONRequest(engine, http.MethodPut, path,
		`{"name":"标签往返","command":"echo group","labels":["已有","新标签","subscription:3","分组:京东"]}`, headers, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	stored := mustLoadTask(t, task.ID)
	if got := stored.GetLabels(); !reflect.DeepEqual(got, want) {
		t.Fatalf("expected stored labels %v, got %v", want, got)
	}
	data, ok := decodeJSONMap(t, rec)["data"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected task data object in response, got %s", rec.Body.String())
	}
	if got := data["labels"]; !reflect.DeepEqual(got, []interface{}{"已有", "新标签", "subscription:3", "分组:京东"}) {
		t.Fatalf("expected response labels %v, got %#v", want, got)
	}

	// 传空数组清空标签。
	rec = performJSONRequest(engine, http.MethodPut, path, `{"labels":[]}`, headers, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 when clearing labels, got %d: %s", rec.Code, rec.Body.String())
	}
	if cleared := mustLoadTask(t, task.ID); cleared.Labels != "" {
		t.Fatalf("expected labels to be cleared, got %q", cleared.Labels)
	}
}

// #157：写库失败时必须回 500「更新任务失败」，库里一个字段都不变。
// 以前这里不接 .Error，失败也回 200「task updated」，前端照样弹「任务更新成功」，刷新后什么都没变。
func TestUpdateTaskReturns500AndKeepsRowWhenWriteFails(t *testing.T) {
	testutil.SetupTestEnv(t)

	engine := newProtectedRouter()
	user := testutil.MustCreateUser(t, "update-fail-operator", "operator")
	headers := map[string]string{"Authorization": "Bearer " + testutil.MustCreateAccessToken(t, user.Username, user.Role)}

	task := mustCreateLabeledTask(t, "写库失败", "旧")
	before := mustLoadTask(t, task.ID)

	// 让 UPDATE tasks 必然失败，模拟磁盘满、只读挂载、锁冲突这类写库故障。
	if err := database.DB.Exec(`CREATE TRIGGER force_task_update_failure BEFORE UPDATE ON tasks BEGIN SELECT RAISE(ABORT, 'forced task update failure'); END;`).Error; err != nil {
		t.Fatalf("create trigger: %v", err)
	}

	rec := performJSONRequest(engine, http.MethodPut, fmt.Sprintf("/api/v1/tasks/%d", task.ID),
		`{"name":"改了名","labels":["旧","新"],"timeout":30}`, headers, "")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 when the update cannot be written, got %d: %s", rec.Code, rec.Body.String())
	}
	if payload := decodeJSONMap(t, rec); payload["error"] != "更新任务失败" {
		t.Fatalf("expected error 更新任务失败, got %#v", payload)
	}
	if after := mustLoadTask(t, task.ID); !reflect.DeepEqual(after, before) {
		t.Fatalf("expected the row to stay unchanged\n  before: %+v\n  after:  %+v", before, after)
	}
}

// 回读失败同样回 500，并且整体回滚、库不变：写进去之后任务却读不回来（这里用触发器在 UPDATE 之后把这一行删掉来模拟），
// 不能拿内存里更新前的旧值去刷新调度、再当成「task updated」回给前端，也不能留下「回了失败、其实写进去了」的半截结果。
func TestUpdateTaskReturns500WhenReloadFails(t *testing.T) {
	testutil.SetupTestEnv(t)

	engine := newProtectedRouter()
	user := testutil.MustCreateUser(t, "update-reload-operator", "operator")
	headers := map[string]string{"Authorization": "Bearer " + testutil.MustCreateAccessToken(t, user.Username, user.Role)}

	task := mustCreateLabeledTask(t, "回读失败", "旧")
	before := mustLoadTask(t, task.ID)
	if err := database.DB.Exec(`CREATE TRIGGER drop_task_after_update AFTER UPDATE ON tasks BEGIN DELETE FROM tasks WHERE id = NEW.id; END;`).Error; err != nil {
		t.Fatalf("create trigger: %v", err)
	}

	rec := performJSONRequest(engine, http.MethodPut, fmt.Sprintf("/api/v1/tasks/%d", task.ID), `{"labels":["新"]}`, headers, "")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 when the task cannot be read back, got %d: %s", rec.Code, rec.Body.String())
	}
	if payload := decodeJSONMap(t, rec); payload["error"] != "更新任务失败" {
		t.Fatalf("expected error 更新任务失败, got %#v", payload)
	}
	if after := mustLoadTask(t, task.ID); !reflect.DeepEqual(after, before) {
		t.Fatalf("expected the update to be rolled back\n  before: %+v\n  after:  %+v", before, after)
	}
}

// 真实会碰到的回读失败：开放 API 往整数列里传了 "abc"，SQLite 照收，读回来时才报错。
// 这一行要是单独提交了，任务列表就整页 500、这条任务也改不回来；必须回 500 且整体回滚，列表照常能读。
// 原来用的是 timeout，v3.4.2 起 timeout / max_retries / retry_interval 在写库前就校验（非整数回 400），
// 走不到回读这一步了，所以换成同样是整数列、仍原样写库的 sort_order 来守住回滚机制本身。
func TestUpdateTaskRollsBackValueThatCannotBeReadBack(t *testing.T) {
	testutil.SetupTestEnv(t)

	engine := newProtectedRouter()
	user := testutil.MustCreateUser(t, "update-unreadable-operator", "operator")
	headers := map[string]string{"Authorization": "Bearer " + testutil.MustCreateAccessToken(t, user.Username, user.Role)}

	task := mustCreateLabeledTask(t, "读不回来", "旧")
	before := mustLoadTask(t, task.ID)

	rec := performJSONRequest(engine, http.MethodPut, fmt.Sprintf("/api/v1/tasks/%d", task.ID), `{"sort_order":"abc","labels":["新"]}`, headers, "")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 when the written value cannot be read back, got %d: %s", rec.Code, rec.Body.String())
	}
	if after := mustLoadTask(t, task.ID); !reflect.DeepEqual(after, before) {
		t.Fatalf("expected the update to be rolled back\n  before: %+v\n  after:  %+v", before, after)
	}
	if list := performRequest(engine, http.MethodGet, "/api/v1/tasks", headers); list.Code != http.StatusOK {
		t.Fatalf("expected the task list to stay readable, got %d: %s", list.Code, list.Body.String())
	}
}

// #157：name / command / cron_expression 在库里是 NOT NULL。显式传 null 以前会让整条 UPDATE 失败却回 200；
// 现在写库之前就回 400 并点名是哪个字段，同一个请求里的其它字段（labels、timeout）也一个都不写。
// 这里的任务是常规定时任务，所以 cron_expression 也回 400；手动 / 开机任务传 null 的口径见下一个用例。
func TestUpdateTaskRejectsNullRequiredFieldsWithoutWriting(t *testing.T) {
	testutil.SetupTestEnv(t)

	engine := newProtectedRouter()
	user := testutil.MustCreateUser(t, "update-null-operator", "operator")
	headers := map[string]string{"Authorization": "Bearer " + testutil.MustCreateAccessToken(t, user.Username, user.Role)}

	task := mustCreateLabeledTask(t, "null 字段", "旧")
	before := mustLoadTask(t, task.ID)
	path := fmt.Sprintf("/api/v1/tasks/%d", task.ID)

	for _, key := range []string{"name", "command", "cron_expression"} {
		t.Run(key, func(t *testing.T) {
			body := fmt.Sprintf(`{%q:null,"labels":["新"],"timeout":30}`, key)
			rec := performJSONRequest(engine, http.MethodPut, path, body, headers, "")
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("expected 400 for %s: null, got %d: %s", key, rec.Code, rec.Body.String())
			}
			if payload := decodeJSONMap(t, rec); payload["error"] != key+" 不能为空" {
				t.Fatalf("expected error %q, got %#v", key+" 不能为空", payload)
			}
			if after := mustLoadTask(t, task.ID); !reflect.DeepEqual(after, before) {
				t.Fatalf("expected nothing to be written\n  before: %+v\n  after:  %+v", before, after)
			}
		})
	}
}

// cron_expression 传 null 只在「这次更新之后是常规定时任务」时回 400。
// 手动 / 开机任务的 cron_expression 服务端一律写成空串，传 null 就是清空：保持改动前回 200、其它字段照常写入的行为，
// 同一个请求里把 task_type 改成 manual / startup 的也一样；反过来把手动任务改成 cron 的同时传 null，要回 400。
func TestUpdateTaskNullCronExpressionFollowsResultingTaskType(t *testing.T) {
	testutil.SetupTestEnv(t)

	engine := newProtectedRouter()
	user := testutil.MustCreateUser(t, "update-null-cron-operator", "operator")
	headers := map[string]string{"Authorization": "Bearer " + testutil.MustCreateAccessToken(t, user.Username, user.Role)}

	cases := []struct {
		name     string
		taskType string // 更新前的任务类型
		body     string
		// wantType 为空表示应当回 400、库里一个字段都不变；否则应当回 200，任务变成这个类型、cron_expression 是空串
		wantType string
	}{
		{name: "手动任务", taskType: model.TaskTypeManual, body: `{"cron_expression":null,"labels":["新"]}`, wantType: model.TaskTypeManual},
		{name: "开机任务", taskType: model.TaskTypeStartup, body: `{"cron_expression":null,"labels":["新"]}`, wantType: model.TaskTypeStartup},
		{name: "cron 任务同一请求改成手动", taskType: model.TaskTypeCron, body: `{"task_type":"manual","cron_expression":null,"labels":["新"]}`, wantType: model.TaskTypeManual},
		{name: "cron 任务同一请求改成开机", taskType: model.TaskTypeCron, body: `{"task_type":"startup","cron_expression":null,"labels":["新"]}`, wantType: model.TaskTypeStartup},
		{name: "cron 任务", taskType: model.TaskTypeCron, body: `{"cron_expression":null,"labels":["新"]}`},
		{name: "手动任务同一请求改成 cron", taskType: model.TaskTypeManual, body: `{"task_type":"cron","cron_expression":null,"labels":["新"]}`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			task := mustCreateTypedTask(t, "null cron "+tc.name, tc.taskType, "0 0 * * *")
			before := mustLoadTask(t, task.ID)

			rec := performJSONRequest(engine, http.MethodPut, fmt.Sprintf("/api/v1/tasks/%d", task.ID), tc.body, headers, "")
			after := mustLoadTask(t, task.ID)

			if tc.wantType == "" {
				if rec.Code != http.StatusBadRequest {
					t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
				}
				if payload := decodeJSONMap(t, rec); payload["error"] != "cron_expression 不能为空" {
					t.Fatalf("expected error cron_expression 不能为空, got %#v", payload)
				}
				if !reflect.DeepEqual(after, before) {
					t.Fatalf("expected nothing to be written\n  before: %+v\n  after:  %+v", before, after)
				}
				return
			}

			if rec.Code != http.StatusOK {
				t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
			}
			if after.CronExpression != "" {
				t.Fatalf("expected cron_expression to be stored as an empty string, got %q", after.CronExpression)
			}
			if got := after.GetTaskType(); got != tc.wantType {
				t.Fatalf("expected task_type %q, got %q", tc.wantType, got)
			}
			// 同一个请求里的其它字段照常写入，说明这条更新没有被整条拦下
			if got := after.GetLabels(); !reflect.DeepEqual(got, []string{"新"}) {
				t.Fatalf("expected labels [新], got %v", got)
			}
			data, ok := decodeJSONMap(t, rec)["data"].(map[string]interface{})
			if !ok || data["cron_expression"] != "" {
				t.Fatalf("expected response cron_expression to be an empty string, got %s", rec.Body.String())
			}
		})
	}
}

// 请求体是字面量 null：JSON 解码不报错，只是把请求留成 nil map。以前手动 / 开机任务会在给 nil map 赋值处 panic
// （线上被 gin.Recovery 兜成一个空的 500），cron 任务则当成一次空更新回 200。现在一律回 400「请求参数错误」，什么都不写。
// 测试路由没挂 gin.Recovery，panic 会直接冒到测试里，这里用 recover 换成一条看得懂的失败。
func TestUpdateTaskRejectsLiteralNullBody(t *testing.T) {
	testutil.SetupTestEnv(t)

	engine := newProtectedRouter()
	user := testutil.MustCreateUser(t, "update-null-body-operator", "operator")
	headers := map[string]string{"Authorization": "Bearer " + testutil.MustCreateAccessToken(t, user.Username, user.Role)}

	for _, taskType := range []string{model.TaskTypeManual, model.TaskTypeStartup, model.TaskTypeCron} {
		t.Run(taskType, func(t *testing.T) {
			task := mustCreateTypedTask(t, "null body "+taskType, taskType, "0 0 * * *")
			before := mustLoadTask(t, task.ID)
			path := fmt.Sprintf("/api/v1/tasks/%d", task.ID)

			rec := func() *httptest.ResponseRecorder {
				defer func() {
					if r := recover(); r != nil {
						t.Fatalf("PUT %s with a literal null body panicked: %v", path, r)
					}
				}()
				return performJSONRequest(engine, http.MethodPut, path, `null`, headers, "")
			}()

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("expected 400 for a literal null body, got %d: %s", rec.Code, rec.Body.String())
			}
			if payload := decodeJSONMap(t, rec); payload["error"] != "请求参数错误" {
				t.Fatalf("expected error 请求参数错误, got %#v", payload)
			}
			if after := mustLoadTask(t, task.ID); !reflect.DeepEqual(after, before) {
				t.Fatalf("expected nothing to be written\n  before: %+v\n  after:  %+v", before, after)
			}
		})
	}
}
