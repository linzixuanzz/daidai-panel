package handler_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"daidai-panel/config"
	"daidai-panel/database"
	"daidai-panel/model"
	"daidai-panel/testutil"
)

// 本文件覆盖 #124「删除任务时可选同时删除脚本」。
//
// 前半部分是 golden 回归：三个删除入口（DELETE /tasks/:id、PUT /tasks/batch、DELETE /tasks/batch/delete）
// 在【不带开关】时，响应必须与改动前逐字节一致，脚本文件原样存在。这组用例先于实现写好，
// 在改动前的 HEAD 上就是绿的——Web 与 APP 的老调用方全靠这条兜底，任何一个字节变了都要在这里红。

// ---------- 夹具 ----------

func delScriptOperatorAuth(t *testing.T) map[string]string {
	t.Helper()
	user := testutil.MustCreateUser(t, "task-del-script-operator", "operator")
	token := testutil.MustCreateAccessToken(t, user.Username, user.Role)
	return map[string]string{"Authorization": "Bearer " + token}
}

// delScriptWriteFile 在脚本目录下写一个文件，rel 用正斜杠。
func delScriptWriteFile(t *testing.T, rel, content string) string {
	t.Helper()
	full := filepath.Join(config.C.Data.ScriptsDir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", rel, err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
	return full
}

func delScriptCreateTask(t *testing.T, name, command string) *model.Task {
	t.Helper()
	task := model.Task{
		Name:           name,
		Command:        command,
		CronExpression: "0 0 * * *",
		TaskType:       model.TaskTypeCron,
		Status:         model.TaskStatusEnabled,
	}
	if err := database.DB.Select("*").Create(&task).Error; err != nil {
		t.Fatalf("create task %s: %v", name, err)
	}
	return &task
}

func delScriptCreateTaskLog(t *testing.T, taskID uint) {
	t.Helper()
	if err := database.DB.Create(&model.TaskLog{TaskID: taskID, Content: "log"}).Error; err != nil {
		t.Fatalf("create task log: %v", err)
	}
}

func delScriptTaskPath(id uint) string {
	return fmt.Sprintf("/api/v1/tasks/%d", id)
}

func delScriptAssertFileExists(t *testing.T, full string) {
	t.Helper()
	info, err := os.Lstat(full)
	if err != nil {
		t.Fatalf("expected %s to still exist: %v", full, err)
	}
	if !info.Mode().IsRegular() && !info.IsDir() && info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("unexpected file mode for %s: %v", full, info.Mode())
	}
}

func delScriptAssertFileGone(t *testing.T, full string) {
	t.Helper()
	if _, err := os.Lstat(full); !os.IsNotExist(err) {
		t.Fatalf("expected %s to be removed, got err=%v", full, err)
	}
}

func delScriptTaskExists(t *testing.T, id uint) bool {
	t.Helper()
	var count int64
	if err := database.DB.Model(&model.Task{}).Where("id = ?", id).Count(&count).Error; err != nil {
		t.Fatalf("count task: %v", err)
	}
	return count > 0
}

func delScriptLogCount(t *testing.T, taskID uint) int64 {
	t.Helper()
	var count int64
	if err := database.DB.Model(&model.TaskLog{}).Where("task_id = ?", taskID).Count(&count).Error; err != nil {
		t.Fatalf("count task logs: %v", err)
	}
	return count
}

func delScriptAssertBody(t *testing.T, got, want string) {
	t.Helper()
	if got != want {
		t.Fatalf("response body must stay byte-identical\nwant: %s\n got: %s", want, got)
	}
}

// ---------- golden：不带开关时逐字节不变 ----------

func TestTaskDeleteGoldenWithoutScriptSwitch(t *testing.T) {
	testutil.SetupTestEnv(t)
	engine := newProtectedRouter()
	auth := delScriptOperatorAuth(t)

	// 不带参数，以及几种「开关没开」的写法：假值、非法值、空值，以及只带 confirm 不带开关。它们都必须走旧逻辑。
	suffixes := []string{"", "?delete_script=0", "?delete_script=abc", "?delete_script=", "?confirm_script_path=demo/golden_4.py"}
	for index, suffix := range suffixes {
		t.Run("suffix="+suffix, func(t *testing.T) {
			rel := fmt.Sprintf("demo/golden_%d.py", index)
			full := delScriptWriteFile(t, rel, "print('ok')\n")
			task := delScriptCreateTask(t, fmt.Sprintf("golden-%d", index), "task "+rel)
			delScriptCreateTaskLog(t, task.ID)

			rec := performJSONRequest(engine, http.MethodDelete, delScriptTaskPath(task.ID)+suffix, "", auth, "")
			if rec.Code != http.StatusOK {
				t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
			}
			delScriptAssertBody(t, rec.Body.String(), `{"message":"删除成功"}`)
			if delScriptTaskExists(t, task.ID) {
				t.Fatalf("expected task row to be deleted")
			}
			if n := delScriptLogCount(t, task.ID); n != 0 {
				t.Fatalf("expected task logs to be deleted, got %d", n)
			}
			delScriptAssertFileExists(t, full)
		})
	}
}

func TestTaskDeleteGoldenNotFound(t *testing.T) {
	testutil.SetupTestEnv(t)
	engine := newProtectedRouter()
	auth := delScriptOperatorAuth(t)

	for _, suffix := range []string{"", "?delete_script=1", "?delete_script=true&confirm_script_path=a.py"} {
		rec := performJSONRequest(engine, http.MethodDelete, "/api/v1/tasks/999999"+suffix, "", auth, "")
		if rec.Code != http.StatusNotFound {
			t.Fatalf("suffix %q: expected 404, got %d: %s", suffix, rec.Code, rec.Body.String())
		}
		delScriptAssertBody(t, rec.Body.String(), `{"error":"任务不存在"}`)
	}
}

func TestTaskBatchDeleteGoldenWithoutScriptSwitch(t *testing.T) {
	testutil.SetupTestEnv(t)
	engine := newProtectedRouter()
	auth := delScriptOperatorAuth(t)

	// 不带开关，以及几种「开关没开」的写法：显式 false、只带 confirm、confirm 为 null。它们都必须走旧逻辑，
	// 响应逐字节不变、脚本仍在。%d 处填任务 id。
	bodies := []string{
		`{"ids":[%d,999999],"action":"delete"}`,
		`{"ids":[%d,999999],"action":"delete","delete_scripts":false}`,
		`{"ids":[%d,999999],"action":"delete","confirm_script_paths":["demo/batch.py"]}`,
		`{"ids":[%d,999999],"action":"delete","confirm_script_paths":null}`,
	}
	for index, bodyFmt := range bodies {
		t.Run(fmt.Sprintf("body=%d", index), func(t *testing.T) {
			rel := fmt.Sprintf("demo/batch_%d.py", index)
			full := delScriptWriteFile(t, rel, "print('ok')\n")
			task := delScriptCreateTask(t, fmt.Sprintf("batch-golden-%d", index), "task "+rel)
			delScriptCreateTaskLog(t, task.ID)

			rec := performJSONRequest(engine, http.MethodPut, "/api/v1/tasks/batch", fmt.Sprintf(bodyFmt, task.ID), auth, "")
			if rec.Code != http.StatusOK {
				t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
			}
			// count 只计查到的任务（999999 不存在，不计入）。
			delScriptAssertBody(t, rec.Body.String(), `{"count":1,"message":"批量delete: 1 个任务"}`)
			if delScriptTaskExists(t, task.ID) {
				t.Fatalf("expected task row to be deleted")
			}
			if n := delScriptLogCount(t, task.ID); n != 0 {
				t.Fatalf("expected task logs to be deleted, got %d", n)
			}
			delScriptAssertFileExists(t, full)
		})
	}
}

// action 不是 delete 时，删脚本开关必须被忽略：响应不多 scripts 字段，脚本与任务都不动。
func TestTaskBatchNonDeleteActionIgnoresScriptSwitch(t *testing.T) {
	testutil.SetupTestEnv(t)
	engine := newProtectedRouter()
	auth := delScriptOperatorAuth(t)

	full := delScriptWriteFile(t, "demo/disable.py", "print('ok')\n")
	task := delScriptCreateTask(t, "batch-disable", "task demo/disable.py")

	body := fmt.Sprintf(`{"ids":[%d],"action":"disable","delete_scripts":true,"confirm_script_paths":["demo/disable.py"]}`, task.ID)
	rec := performJSONRequest(engine, http.MethodPut, "/api/v1/tasks/batch", body, auth, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	delScriptAssertBody(t, rec.Body.String(), `{"count":1,"message":"批量disable: 1 个任务"}`)
	if !delScriptTaskExists(t, task.ID) {
		t.Fatalf("disable must not delete the task")
	}
	delScriptAssertFileExists(t, full)
}

func TestTaskBatchDeleteEndpointGoldenWithoutScriptSwitch(t *testing.T) {
	testutil.SetupTestEnv(t)
	engine := newProtectedRouter()
	auth := delScriptOperatorAuth(t)

	// 不带开关，以及几种「开关没开」的写法：显式 false、只带 confirm、confirm 为 null。它们都必须走旧逻辑。
	bodies := []string{
		`{"task_ids":[%d,999999]}`,
		`{"task_ids":[%d,999999],"delete_scripts":false}`,
		`{"task_ids":[%d,999999],"confirm_script_paths":["demo/app_batch.py"]}`,
		`{"task_ids":[%d,999999],"confirm_script_paths":null}`,
	}
	for index, bodyFmt := range bodies {
		t.Run(fmt.Sprintf("body=%d", index), func(t *testing.T) {
			rel := fmt.Sprintf("demo/app_batch_%d.py", index)
			full := delScriptWriteFile(t, rel, "print('ok')\n")
			task := delScriptCreateTask(t, fmt.Sprintf("app-batch-golden-%d", index), "task "+rel)
			delScriptCreateTaskLog(t, task.ID)

			rec := performJSONRequest(engine, http.MethodDelete, "/api/v1/tasks/batch/delete", fmt.Sprintf(bodyFmt, task.ID), auth, "")
			if rec.Code != http.StatusOK {
				t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
			}
			// count 等于 len(task_ids)，连不存在的 id 也计入——这是现有怪癖，保留不改。
			delScriptAssertBody(t, rec.Body.String(), `{"count":2,"message":"已删除 2 个任务"}`)
			if delScriptTaskExists(t, task.ID) {
				t.Fatalf("expected task row to be deleted")
			}
			if n := delScriptLogCount(t, task.ID); n != 0 {
				t.Fatalf("expected task logs to be deleted, got %d", n)
			}
			delScriptAssertFileExists(t, full)
		})
	}
}

// 只有 tasks 权限的应用令牌，不带开关时三个入口的行为必须与现在完全一致（不能因为新增的 scripts 校验被误拦）。
func TestTaskDeleteGoldenAppTokenWithoutScriptSwitch(t *testing.T) {
	testutil.SetupTestEnv(t)
	engine := newProtectedRouter()
	token := testutil.MustCreateAppToken(t, "tasks-only-golden", "tasks")
	auth := map[string]string{"Authorization": "Bearer " + token}

	fullA := delScriptWriteFile(t, "demo/app_a.py", "print('a')\n")
	fullB := delScriptWriteFile(t, "demo/app_b.py", "print('b')\n")
	fullC := delScriptWriteFile(t, "demo/app_c.py", "print('c')\n")
	taskA := delScriptCreateTask(t, "app-a", "task demo/app_a.py")
	taskB := delScriptCreateTask(t, "app-b", "task demo/app_b.py")
	taskC := delScriptCreateTask(t, "app-c", "task demo/app_c.py")

	rec := performJSONRequest(engine, http.MethodDelete, delScriptTaskPath(taskA.ID), "", auth, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("single delete: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	delScriptAssertBody(t, rec.Body.String(), `{"message":"删除成功"}`)

	rec = performJSONRequest(engine, http.MethodPut, "/api/v1/tasks/batch", fmt.Sprintf(`{"ids":[%d],"action":"delete"}`, taskB.ID), auth, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("put batch: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	delScriptAssertBody(t, rec.Body.String(), `{"count":1,"message":"批量delete: 1 个任务"}`)

	rec = performJSONRequest(engine, http.MethodDelete, "/api/v1/tasks/batch/delete", fmt.Sprintf(`{"task_ids":[%d]}`, taskC.ID), auth, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("delete batch: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	delScriptAssertBody(t, rec.Body.String(), `{"count":1,"message":"已删除 1 个任务"}`)

	for _, id := range []uint{taskA.ID, taskB.ID, taskC.ID} {
		if delScriptTaskExists(t, id) {
			t.Fatalf("expected task %d to be deleted", id)
		}
	}
	for _, full := range []string{fullA, fullB, fullC} {
		delScriptAssertFileExists(t, full)
	}
}

// ---------- 带开关：响应结构 ----------
//
// 按 api_contract 独立定义，不复用 service 的类型：service 那边的 JSON tag 一旦写错，这里解不出值就会变红。

type delScriptRef struct {
	ID        uint   `json:"id"`
	Name      string `json:"name"`
	TextMatch bool   `json:"text_match"`
}

type delScriptItem struct {
	Path      string         `json:"path"`
	TaskIDs   []uint         `json:"task_ids"`
	Deletable bool           `json:"deletable"`
	Reason    string         `json:"reason"`
	Detail    string         `json:"detail"`
	SharedBy  []delScriptRef `json:"shared_by"`
	Warnings  []string       `json:"warnings"`
}

type delScriptTaskInfo struct {
	ID           uint   `json:"id"`
	Name         string `json:"name"`
	Found        bool   `json:"found"`
	Running      bool   `json:"running"`
	ScriptStatus string `json:"script_status"`
	ScriptPath   string `json:"script_path"`
	Note         string `json:"note"`
}

type delScriptResult struct {
	Tasks   []delScriptTaskInfo `json:"tasks"`
	Deleted []delScriptItem     `json:"deleted"`
	Skipped []delScriptItem     `json:"skipped"`
}

type delScriptDeleteResponse struct {
	Message string           `json:"message"`
	Count   *int             `json:"count"`
	Scripts *delScriptResult `json:"scripts"`
}

type delScriptPreviewResponse struct {
	Data struct {
		Checked bool                `json:"checked"`
		Tasks   []delScriptTaskInfo `json:"tasks"`
		Scripts []delScriptItem     `json:"scripts"`
	} `json:"data"`
}

func delScriptDecode[T any](t *testing.T, body []byte) T {
	t.Helper()
	var value T
	if err := json.Unmarshal(body, &value); err != nil {
		t.Fatalf("decode response %s: %v", body, err)
	}
	return value
}

func delScriptFind(items []delScriptItem, path string) (delScriptItem, bool) {
	for _, item := range items {
		if item.Path == path {
			return item, true
		}
	}
	return delScriptItem{}, false
}

func delScriptIDsJSON(t *testing.T, ids ...uint) string {
	t.Helper()
	raw, err := json.Marshal(ids)
	if err != nil {
		t.Fatalf("marshal ids: %v", err)
	}
	return string(raw)
}

// ---------- 带开关：删除与保留 ----------

func TestTaskDeleteWithScriptSwitchDeletesScript(t *testing.T) {
	testutil.SetupTestEnv(t)
	engine := newProtectedRouter()
	auth := delScriptOperatorAuth(t)

	for _, suffix := range []string{"?delete_script=1", "?delete_script=true&confirm_script_path=demo/b.py"} {
		rel := "demo/a.py"
		if strings.Contains(suffix, "confirm") {
			rel = "demo/b.py"
		}
		full := delScriptWriteFile(t, rel, "print('a')\n")
		task := delScriptCreateTask(t, "single-"+rel, "task "+rel)
		delScriptCreateTaskLog(t, task.ID)

		rec := performJSONRequest(engine, http.MethodDelete, delScriptTaskPath(task.ID)+suffix, "", auth, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: expected 200, got %d: %s", suffix, rec.Code, rec.Body.String())
		}
		// B3：所有嵌套数组恒为 []，响应体不含 null（APP / OpenAPI 调用方不一定有 ?? [] 兜底）。
		for _, want := range []string{`"shared_by":[]`, `"warnings":[]`, `"skipped":[]`} {
			if !strings.Contains(rec.Body.String(), want) {
				t.Fatalf("%s: response must contain %s, got %s", suffix, want, rec.Body.String())
			}
		}
		if strings.Contains(rec.Body.String(), "null") {
			t.Fatalf("%s: arrays must serialize as [], not null: %s", suffix, rec.Body.String())
		}
		resp := delScriptDecode[delScriptDeleteResponse](t, rec.Body.Bytes())
		if resp.Message != "删除成功" || resp.Count != nil {
			t.Fatalf("%s: original fields must stay unchanged, got %s", suffix, rec.Body.String())
		}
		if resp.Scripts == nil || len(resp.Scripts.Deleted) != 1 || len(resp.Scripts.Skipped) != 0 {
			t.Fatalf("%s: expected one deleted script, got %s", suffix, rec.Body.String())
		}
		deleted := resp.Scripts.Deleted[0]
		if deleted.Path != rel || len(deleted.TaskIDs) != 1 || deleted.TaskIDs[0] != task.ID || !deleted.Deletable {
			t.Fatalf("%s: unexpected deleted item %#v", suffix, deleted)
		}
		if len(resp.Scripts.Tasks) != 1 || resp.Scripts.Tasks[0].ScriptStatus != "resolved" || resp.Scripts.Tasks[0].ScriptPath != rel {
			t.Fatalf("%s: unexpected scripts.tasks %#v", suffix, resp.Scripts.Tasks)
		}
		delScriptAssertFileGone(t, full)
		delScriptAssertFileExists(t, filepath.Dir(full))
		if delScriptTaskExists(t, task.ID) || delScriptLogCount(t, task.ID) != 0 {
			t.Fatalf("%s: task row and logs must be deleted as before", suffix)
		}
	}
}

// 两个批量入口：选中的两个任务共用同一脚本 → 合并删除；选中一个、另一个没选中 → shared 保留。
func TestTaskBatchDeleteScriptsMergesAndKeepsShared(t *testing.T) {
	cases := []struct {
		name        string
		method      string
		path        string
		body        func(ids string) string
		wantMessage string
	}{
		{
			name:        "put batch",
			method:      http.MethodPut,
			path:        "/api/v1/tasks/batch",
			body:        func(ids string) string { return `{"ids":` + ids + `,"action":"delete","delete_scripts":true}` },
			wantMessage: "批量delete: 3 个任务",
		},
		{
			name:        "delete batch",
			method:      http.MethodDelete,
			path:        "/api/v1/tasks/batch/delete",
			body:        func(ids string) string { return `{"task_ids":` + ids + `,"delete_scripts":true}` },
			wantMessage: "已删除 3 个任务",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			testutil.SetupTestEnv(t)
			engine := newProtectedRouter()
			auth := delScriptOperatorAuth(t)

			mergedFile := delScriptWriteFile(t, "s.py", "s")
			sharedFile := delScriptWriteFile(t, "t.py", "t")
			a := delScriptCreateTask(t, "A", "task s.py")
			b := delScriptCreateTask(t, "B", "python3 ./s.py")
			c := delScriptCreateTask(t, "C", "task t.py")
			d := delScriptCreateTask(t, "D", "task t.py")

			rec := performJSONRequest(engine, tc.method, tc.path, tc.body(delScriptIDsJSON(t, a.ID, b.ID, c.ID)), auth, "")
			if rec.Code != http.StatusOK {
				t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
			}
			resp := delScriptDecode[delScriptDeleteResponse](t, rec.Body.Bytes())
			if resp.Message != tc.wantMessage || resp.Count == nil || *resp.Count != 3 || resp.Scripts == nil {
				t.Fatalf("message and count must keep their old semantics, got %s", rec.Body.String())
			}
			if len(resp.Scripts.Deleted) != 1 || resp.Scripts.Deleted[0].Path != "s.py" || len(resp.Scripts.Deleted[0].TaskIDs) != 2 {
				t.Fatalf("expected s.py to be deleted once for both tasks, got %#v", resp.Scripts.Deleted)
			}
			kept, ok := delScriptFind(resp.Scripts.Skipped, "t.py")
			if !ok || kept.Reason != "shared" || kept.Deletable || len(kept.SharedBy) != 1 || kept.SharedBy[0].ID != d.ID || kept.SharedBy[0].Name != "D" {
				t.Fatalf("expected t.py to be kept as shared by D, got %#v", resp.Scripts.Skipped)
			}
			delScriptAssertFileGone(t, mergedFile)
			delScriptAssertFileExists(t, sharedFile)
			if !delScriptTaskExists(t, d.ID) {
				t.Fatalf("unselected task must survive")
			}
		})
	}
}

func TestTaskDeleteScriptsConfirmNarrowing(t *testing.T) {
	testutil.SetupTestEnv(t)
	engine := newProtectedRouter()
	auth := delScriptOperatorAuth(t)

	t.Run("mismatched confirm keeps the file", func(t *testing.T) {
		full := delScriptWriteFile(t, "demo/c.py", "c")
		task := delScriptCreateTask(t, "C", "task demo/c.py")
		rec := performJSONRequest(engine, http.MethodDelete, delScriptTaskPath(task.ID)+"?delete_script=1&confirm_script_path=demo/other.py", "", auth, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
		}
		resp := delScriptDecode[delScriptDeleteResponse](t, rec.Body.Bytes())
		if len(resp.Scripts.Deleted) != 0 || len(resp.Scripts.Skipped) != 1 || resp.Scripts.Skipped[0].Reason != "not_confirmed" {
			t.Fatalf("expected not_confirmed, got %s", rec.Body.String())
		}
		delScriptAssertFileExists(t, full)
		if delScriptTaskExists(t, task.ID) {
			t.Fatalf("the task itself must still be deleted")
		}
	})

	t.Run("empty confirm deletes nothing", func(t *testing.T) {
		full := delScriptWriteFile(t, "demo/d.py", "d")
		task := delScriptCreateTask(t, "D", "task demo/d.py")
		rec := performJSONRequest(engine, http.MethodDelete, delScriptTaskPath(task.ID)+"?delete_script=true&confirm_script_path=", "", auth, "")
		resp := delScriptDecode[delScriptDeleteResponse](t, rec.Body.Bytes())
		if rec.Code != http.StatusOK || len(resp.Scripts.Deleted) != 0 || len(resp.Scripts.Skipped) != 1 || resp.Scripts.Skipped[0].Reason != "not_confirmed" {
			t.Fatalf("expected nothing deleted, got %d %s", rec.Code, rec.Body.String())
		}
		delScriptAssertFileExists(t, full)
	})

	t.Run("extra path fields in the body are ignored", func(t *testing.T) {
		other := delScriptWriteFile(t, "other.py", "keep")
		full := delScriptWriteFile(t, "demo/f.py", "f")
		task := delScriptCreateTask(t, "F", "task demo/f.py")
		body := fmt.Sprintf(`{"ids":[%d],"action":"delete","delete_scripts":true,"path":"other.py","paths":["other.py"]}`, task.ID)
		rec := performJSONRequest(engine, http.MethodPut, "/api/v1/tasks/batch", body, auth, "")
		resp := delScriptDecode[delScriptDeleteResponse](t, rec.Body.Bytes())
		if rec.Code != http.StatusOK || len(resp.Scripts.Deleted) != 1 || resp.Scripts.Deleted[0].Path != "demo/f.py" {
			t.Fatalf("expected only the parsed script to be deleted, got %d %s", rec.Code, rec.Body.String())
		}
		delScriptAssertFileGone(t, full)
		delScriptAssertFileExists(t, other)
	})

	t.Run("batch confirm list narrows", func(t *testing.T) {
		keepFile := delScriptWriteFile(t, "demo/h.py", "h")
		dropFile := delScriptWriteFile(t, "demo/g.py", "g")
		g := delScriptCreateTask(t, "G", "task demo/g.py")
		h := delScriptCreateTask(t, "H", "task demo/h.py")
		body := fmt.Sprintf(`{"task_ids":%s,"delete_scripts":true,"confirm_script_paths":["demo/g.py"]}`, delScriptIDsJSON(t, g.ID, h.ID))
		rec := performJSONRequest(engine, http.MethodDelete, "/api/v1/tasks/batch/delete", body, auth, "")
		resp := delScriptDecode[delScriptDeleteResponse](t, rec.Body.Bytes())
		if rec.Code != http.StatusOK || len(resp.Scripts.Deleted) != 1 || resp.Scripts.Deleted[0].Path != "demo/g.py" {
			t.Fatalf("expected only demo/g.py deleted, got %d %s", rec.Code, rec.Body.String())
		}
		if kept, ok := delScriptFind(resp.Scripts.Skipped, "demo/h.py"); !ok || kept.Reason != "not_confirmed" {
			t.Fatalf("expected demo/h.py not_confirmed, got %#v", resp.Scripts.Skipped)
		}
		delScriptAssertFileGone(t, dropFile)
		delScriptAssertFileExists(t, keepFile)
	})
}

// B3：嵌套数组 shared_by / warnings / skipped / deleted 恒为 []，响应体不含 null。
// 用「有一个可删、无共用方、无 warning 的脚本」的预览，和一次空 confirm 的批量删除，分别覆盖这几个数组。
func TestTaskDeleteScriptsNestedArraysNeverNull(t *testing.T) {
	testutil.SetupTestEnv(t)
	engine := newProtectedRouter()
	auth := delScriptOperatorAuth(t)

	// 预览：demo/a.py 不在公共库名单里，可删、无共用方、无 warning，正好命中 shared_by:[] 与 warnings:[]。
	delScriptWriteFile(t, "demo/a.py", "x")
	previewTask := delScriptCreateTask(t, "preview-a", "task demo/a.py")
	rec := performJSONRequest(engine, http.MethodPost, "/api/v1/tasks/delete-preview", fmt.Sprintf(`{"task_ids":[%d]}`, previewTask.ID), auth, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("preview: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	for _, want := range []string{`"shared_by":[]`, `"warnings":[]`} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Fatalf("preview must contain %s, got %s", want, rec.Body.String())
		}
	}
	if strings.Contains(rec.Body.String(), "null") {
		t.Fatalf("preview arrays must serialize as [], not null: %s", rec.Body.String())
	}

	// 批量删除但 confirm 为空列表：一个都不删，deleted 恒为 []。
	delScriptWriteFile(t, "demo/g.py", "g")
	g := delScriptCreateTask(t, "empty-confirm", "task demo/g.py")
	body := fmt.Sprintf(`{"task_ids":[%d],"delete_scripts":true,"confirm_script_paths":[]}`, g.ID)
	rec = performJSONRequest(engine, http.MethodDelete, "/api/v1/tasks/batch/delete", body, auth, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("batch: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"deleted":[]`) {
		t.Fatalf("batch response must contain \"deleted\":[], got %s", rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "null") {
		t.Fatalf("batch arrays must serialize as [], not null: %s", rec.Body.String())
	}
}

// 应用令牌带开关或调预览：scopes 必须含 scripts 或 *，否则 403 且任务、脚本都不动。viewer 调预览 403。
func TestTaskDeleteScriptsAppScopeMatrix(t *testing.T) {
	testutil.SetupTestEnv(t)
	engine := newProtectedRouter()

	tasksOnly := map[string]string{"Authorization": "Bearer " + testutil.MustCreateAppToken(t, "del-tasks-only", "tasks")}
	full := delScriptWriteFile(t, "demo/scope.py", "x")
	task := delScriptCreateTask(t, "scope", "task demo/scope.py")
	denied := `{"error":"应用令牌缺少 scripts 权限，不能同时删除脚本"}`
	requests := []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodDelete, delScriptTaskPath(task.ID) + "?delete_script=1", ""},
		{http.MethodPut, "/api/v1/tasks/batch", fmt.Sprintf(`{"ids":[%d],"action":"delete","delete_scripts":true}`, task.ID)},
		{http.MethodDelete, "/api/v1/tasks/batch/delete", fmt.Sprintf(`{"task_ids":[%d],"delete_scripts":true}`, task.ID)},
		{http.MethodPost, "/api/v1/tasks/delete-preview", fmt.Sprintf(`{"task_ids":[%d]}`, task.ID)},
	}
	for _, req := range requests {
		rec := performJSONRequest(engine, req.method, req.path, req.body, tasksOnly, "")
		if rec.Code != http.StatusForbidden {
			t.Fatalf("%s %s: expected 403, got %d: %s", req.method, req.path, rec.Code, rec.Body.String())
		}
		delScriptAssertBody(t, rec.Body.String(), denied)
		if !delScriptTaskExists(t, task.ID) {
			t.Fatalf("%s %s: task must not be deleted when the scope check fails", req.method, req.path)
		}
		delScriptAssertFileExists(t, full)
	}

	viewer := testutil.MustCreateUser(t, "del-script-viewer", "viewer")
	viewerAuth := map[string]string{"Authorization": "Bearer " + testutil.MustCreateAccessToken(t, viewer.Username, viewer.Role)}
	rec := performJSONRequest(engine, http.MethodPost, "/api/v1/tasks/delete-preview", fmt.Sprintf(`{"task_ids":[%d]}`, task.ID), viewerAuth, "")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("viewer preview: expected 403, got %d: %s", rec.Code, rec.Body.String())
	}
	delScriptAssertBody(t, rec.Body.String(), `{"error":"权限不足"}`)

	for index, scopes := range []string{"tasks,scripts", "*"} {
		auth := map[string]string{"Authorization": "Bearer " + testutil.MustCreateAppToken(t, fmt.Sprintf("del-scripts-%d", index), scopes)}
		rel := fmt.Sprintf("demo/allowed_%d.py", index)
		allowedFile := delScriptWriteFile(t, rel, "x")
		allowedTask := delScriptCreateTask(t, "allowed-"+rel, "task "+rel)

		rec := performJSONRequest(engine, http.MethodPost, "/api/v1/tasks/delete-preview", fmt.Sprintf(`{"task_ids":[%d]}`, allowedTask.ID), auth, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("scopes %q preview: expected 200, got %d: %s", scopes, rec.Code, rec.Body.String())
		}
		rec = performJSONRequest(engine, http.MethodDelete, delScriptTaskPath(allowedTask.ID)+"?delete_script=1", "", auth, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("scopes %q delete: expected 200, got %d: %s", scopes, rec.Code, rec.Body.String())
		}
		resp := delScriptDecode[delScriptDeleteResponse](t, rec.Body.Bytes())
		if resp.Scripts == nil || len(resp.Scripts.Deleted) != 1 || resp.Scripts.Deleted[0].Path != rel {
			t.Fatalf("scopes %q: expected the script to be deleted, got %s", scopes, rec.Body.String())
		}
		delScriptAssertFileGone(t, allowedFile)
	}
}

func TestTaskDeletePreviewValidation(t *testing.T) {
	testutil.SetupTestEnv(t)
	engine := newProtectedRouter()
	auth := delScriptOperatorAuth(t)

	for _, body := range []string{`{bad`, `{}`, `{"task_ids":null}`} {
		rec := performJSONRequest(engine, http.MethodPost, "/api/v1/tasks/delete-preview", body, auth, "")
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: expected 400, got %d: %s", body, rec.Code, rec.Body.String())
		}
		delScriptAssertBody(t, rec.Body.String(), `{"error":"请求参数错误"}`)
	}

	// binding:"required" 会放行 []，必须显式判断。
	rec := performJSONRequest(engine, http.MethodPost, "/api/v1/tasks/delete-preview", `{"task_ids":[]}`, auth, "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("empty ids: expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
	delScriptAssertBody(t, rec.Body.String(), `{"error":"请选择要删除的任务"}`)

	managed := delScriptCreateTask(t, "managed", "dailycheckin --help")
	rec = performJSONRequest(engine, http.MethodPost, "/api/v1/tasks/delete-preview", fmt.Sprintf(`{"task_ids":[999999,%d,%d]}`, managed.ID, managed.ID), auth, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	// 所有数组都是 [] 而不是 null。
	if strings.Contains(rec.Body.String(), "null") || !strings.Contains(rec.Body.String(), `"scripts":[]`) {
		t.Fatalf("arrays must serialize as [], got %s", rec.Body.String())
	}
	preview := delScriptDecode[delScriptPreviewResponse](t, rec.Body.Bytes())
	if !preview.Data.Checked || len(preview.Data.Tasks) != 2 || len(preview.Data.Scripts) != 0 {
		t.Fatalf("unexpected preview: %s", rec.Body.String())
	}
	ghost, managedInfo := preview.Data.Tasks[0], preview.Data.Tasks[1]
	if ghost.ID != 999999 || ghost.Found || ghost.ScriptStatus != "task_not_found" || ghost.Note != "任务不存在（可能已被删除）。" {
		t.Fatalf("unexpected ghost task info: %#v", ghost)
	}
	if managedInfo.ID != managed.ID || !managedInfo.Found || managedInfo.ScriptStatus != "no_script" {
		t.Fatalf("unexpected managed task info: %#v", managedInfo)
	}
	if !delScriptTaskExists(t, managed.ID) {
		t.Fatalf("preview must not delete anything")
	}
}

// 预览与执行一致：可删项路径集合相同、保留项 reason 相同；每个存在的请求任务在 scripts.tasks 里恰好出现一次。
func TestTaskDeletePreviewMatchesExecution(t *testing.T) {
	testutil.SetupTestEnv(t)
	engine := newProtectedRouter()
	auth := delScriptOperatorAuth(t)

	okFile := delScriptWriteFile(t, "ok.py", "x")
	sharedFile := delScriptWriteFile(t, "sh.py", "x")
	subFile := delScriptWriteFile(t, "repo/x.js", "x")
	if err := os.MkdirAll(filepath.Join(config.C.Data.ScriptsDir, "repo", ".git"), 0o755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
	dirPath := filepath.Join(config.C.Data.ScriptsDir, "dir.py")
	if err := os.MkdirAll(dirPath, 0o755); err != nil {
		t.Fatalf("mkdir dir.py: %v", err)
	}
	hiddenFile := delScriptWriteFile(t, "node_modules/x.js", "x")
	if err := database.DB.Create(&model.Subscription{Name: "repo-sub", Type: model.SubTypeGitRepo, URL: "https://example.com/repo.git", SaveDir: "repo", Enabled: true}).Error; err != nil {
		t.Fatalf("create subscription: %v", err)
	}

	requested := []uint{
		delScriptCreateTask(t, "ok", "task ok.py").ID,
		delScriptCreateTask(t, "shared-req", "task sh.py").ID,
		delScriptCreateTask(t, "missing", "task ops/old.sh").ID,
		delScriptCreateTask(t, "managed", "dailycheckin --help").ID,
		delScriptCreateTask(t, "sub", "task repo/x.js").ID,
		delScriptCreateTask(t, "dir", "task dir.py").ID,
		delScriptCreateTask(t, "hidden", "task node_modules/x.js").ID,
	}
	delScriptCreateTask(t, "shared-other", "task sh.py")
	ids := delScriptIDsJSON(t, requested...)

	rec := performJSONRequest(engine, http.MethodPost, "/api/v1/tasks/delete-preview", `{"task_ids":`+ids+`}`, auth, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("preview: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	preview := delScriptDecode[delScriptPreviewResponse](t, rec.Body.Bytes())
	previewDeletable := map[string]bool{}
	previewKept := map[string]string{}
	for _, item := range preview.Data.Scripts {
		if item.Deletable {
			previewDeletable[item.Path] = true
		} else {
			previewKept[item.Path] = item.Reason
		}
	}
	wantKept := map[string]string{"sh.py": "shared", "repo/x.js": "subscription_managed", "dir.py": "not_regular_file", "node_modules/x.js": "hidden_path"}
	if len(previewDeletable) != 1 || !previewDeletable["ok.py"] || fmt.Sprint(previewKept) != fmt.Sprint(wantKept) {
		t.Fatalf("unexpected preview fixture result: deletable=%v kept=%v", previewDeletable, previewKept)
	}

	rec = performJSONRequest(engine, http.MethodDelete, "/api/v1/tasks/batch/delete", `{"task_ids":`+ids+`,"delete_scripts":true}`, auth, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("execute: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	resp := delScriptDecode[delScriptDeleteResponse](t, rec.Body.Bytes())
	executedDeleted := map[string]bool{}
	for _, item := range resp.Scripts.Deleted {
		executedDeleted[item.Path] = true
	}
	executedKept := map[string]string{}
	for _, item := range resp.Scripts.Skipped {
		executedKept[item.Path] = item.Reason
	}
	if fmt.Sprint(executedDeleted) != fmt.Sprint(previewDeletable) || fmt.Sprint(executedKept) != fmt.Sprint(previewKept) {
		t.Fatalf("preview and execution disagree:\npreview deletable=%v kept=%v\nexecute deleted=%v kept=%v", previewDeletable, previewKept, executedDeleted, executedKept)
	}

	seen := map[uint]int{}
	for _, info := range resp.Scripts.Tasks {
		seen[info.ID]++
	}
	for _, id := range requested {
		if seen[id] != 1 {
			t.Fatalf("task %d must appear exactly once in scripts.tasks, got %d (%#v)", id, seen[id], resp.Scripts.Tasks)
		}
	}

	delScriptAssertFileGone(t, okFile)
	for _, full := range []string{sharedFile, subFile, dirPath, hiddenFile} {
		delScriptAssertFileExists(t, full)
	}
}

// delete_scripts 必须是 JSON bool：传成字符串整个请求绑定失败，返回 400，任务也不会被删。
func TestTaskBatchDeleteScriptsRejectsNonBoolSwitch(t *testing.T) {
	testutil.SetupTestEnv(t)
	engine := newProtectedRouter()
	auth := delScriptOperatorAuth(t)

	full := delScriptWriteFile(t, "demo/str.py", "x")
	task := delScriptCreateTask(t, "str", "task demo/str.py")
	requests := []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodPut, "/api/v1/tasks/batch", fmt.Sprintf(`{"ids":[%d],"action":"delete","delete_scripts":"true"}`, task.ID)},
		{http.MethodDelete, "/api/v1/tasks/batch/delete", fmt.Sprintf(`{"task_ids":[%d],"delete_scripts":"true"}`, task.ID)},
	}
	for _, req := range requests {
		rec := performJSONRequest(engine, req.method, req.path, req.body, auth, "")
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s %s: expected 400, got %d: %s", req.method, req.path, rec.Code, rec.Body.String())
		}
		delScriptAssertBody(t, rec.Body.String(), `{"error":"请求参数错误"}`)
		if !delScriptTaskExists(t, task.ID) {
			t.Fatalf("%s %s: task must not be deleted on a bind failure", req.method, req.path)
		}
		delScriptAssertFileExists(t, full)
	}
}
