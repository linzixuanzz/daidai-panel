package mcptools

import (
	"net/http"
	"reflect"
	"strings"
	"testing"
)

func TestSendNotificationForwardsTargetsAndSummarizes(t *testing.T) {
	fake := &fakeDispatcher{respond: okResponder(map[string]any{
		"message": "通知发送完成，成功 1 个渠道",
		"data": map[string]any{"sent_count": 1, "failed_count": 0, "channel_names": []any{"我的 Bark"}, "errors": []any{},
			"used_all": false, "requested_ids": []any{3}, "content_length": 5},
	})}
	session := connectTestClient(t, fake, true)

	result, text := callTool(t, session, "send_notification", map[string]any{
		"title": " 巡检 ", "content": "失败 2 个", "content_type": "markdown",
		"channel_ids": []int{3}, "channel_names": []string{"我的 Bark"}, "channel_types": []string{"bark"},
	})
	if result.IsError {
		t.Fatalf("send_notification 不应失败: %s", text)
	}
	call := fake.recorded()[0]
	want := map[string]any{
		"title": "巡检", "content": "失败 2 个", "content_type": "markdown",
		"channel_ids": []int64{3}, "channel_names": []string{"我的 Bark"}, "channel_types": []string{"bark"},
	}
	if call.method != http.MethodPost || call.path != "/notifications/send" || !reflect.DeepEqual(call.body, want) {
		t.Fatalf("应当以 POST /notifications/send 转发，实际 %s %s %#v", call.method, call.path, call.body)
	}
	out := decodeObject(t, text)
	if out["sent_count"] != float64(1) || out["used_all"] != false || out["requested_ids"] != nil || out["message"] == nil {
		t.Fatalf("应当整理出发送结果，实际 %s", text)
	}

	// 不点名渠道时请求体里不带任何渠道键：面板把这种请求当广播。
	if result, text := callTool(t, session, "send_notification", map[string]any{"title": "t", "content": "c"}); result.IsError {
		t.Fatalf("广播不应失败: %s", text)
	}
	if body := fake.recorded()[1].body; !reflect.DeepEqual(body, map[string]any{"title": "t", "content": "c"}) {
		t.Fatalf("广播的请求体不应带渠道键，实际 %#v", body)
	}

	for _, args := range []map[string]any{{"title": " ", "content": "c"}, {"title": "t", "content": "c", "channel_ids": []int{0}}} {
		if result, text := callTool(t, session, "send_notification", args); !result.IsError {
			t.Errorf("参数 %v 应当报错，实际 %s", args, text)
		}
	}
	if len(fake.recorded()) != 2 {
		t.Fatal("参数校验失败时不应调用面板接口")
	}
}

func TestListAndCreateBackup(t *testing.T) {
	backupPath := "/app/Dumb-Panel/backups/nightly.enc"
	fake := &fakeDispatcher{respond: func(call recordedCall) (int, any, error) {
		switch {
		case call.method == http.MethodGet && call.path == "/system/backups":
			return http.StatusOK, map[string]any{"data": []any{map[string]any{"name": "a.tar.gz", "size": 100, "created_at": "2026-09-18T10:00:00Z"}}}, nil
		case call.method == http.MethodPost && call.path == "/system/backup":
			return http.StatusOK, map[string]any{"message": "备份成功", "data": map[string]any{"path": backupPath}}, nil
		}
		return http.StatusTeapot, map[string]any{"error": "unexpected"}, nil
	}}
	session := connectTestClient(t, fake, true)

	result, text := callTool(t, session, "list_backups", nil)
	if out := decodeObject(t, text); result.IsError || out["total"] != float64(1) {
		t.Fatalf("list_backups 应当返回备份列表，实际 %s", text)
	}

	result, text = callTool(t, session, "create_backup", map[string]any{"name": " nightly ", "password": "p@ss", "include": []string{"tasks", "ENV_VARS"}})
	if result.IsError {
		t.Fatalf("create_backup 不应失败: %s", text)
	}
	call := fake.recorded()[1]
	want := map[string]any{"name": "nightly", "password": "p@ss", "selection": map[string]bool{"tasks": true, "env_vars": true}}
	if !reflect.DeepEqual(call.body, want) {
		t.Fatalf("include 应当换成面板的 selection，实际 %#v", call.body)
	}
	if out := decodeObject(t, text); out["filename"] != "nightly.enc" || out["path"] != backupPath {
		t.Fatalf("应当从绝对路径里取出文件名，实际 %s", text)
	}

	// 默认备份全部：请求体不带 selection，由面板补默认值。
	if result, text := callTool(t, session, "create_backup", nil); result.IsError {
		t.Fatalf("默认备份不应失败: %s", text)
	}
	if body := fake.recorded()[2].body; !reflect.DeepEqual(body, map[string]any{}) {
		t.Fatalf("什么都不传时请求体应当为空对象，实际 %#v", body)
	}

	backupPath = `C:\daidai\data\backups\win.tar.gz`
	if _, text := callTool(t, session, "create_backup", nil); decodeObject(t, text)["filename"] != "win.tar.gz" {
		t.Fatalf("Windows 路径也要能取出文件名，实际 %s", text)
	}

	before := len(fake.recorded())
	if result, _ := callTool(t, session, "create_backup", map[string]any{"include": []string{"users"}}); !result.IsError {
		t.Fatal("未知的备份项应当报错")
	}
	if len(fake.recorded()) != before {
		t.Fatal("参数校验失败时不应调用面板接口")
	}
}

func TestDeleteBackupChecksExistenceFirst(t *testing.T) {
	fake := &fakeDispatcher{respond: func(call recordedCall) (int, any, error) {
		switch {
		case call.method == http.MethodGet && call.path == "/system/backups":
			return http.StatusOK, map[string]any{"data": []any{map[string]any{"name": "a.tar.gz"}}}, nil
		case call.method == http.MethodDelete && call.path == "/system/backup":
			return http.StatusOK, map[string]any{"message": "删除成功"}, nil
		}
		return http.StatusTeapot, map[string]any{"error": "unexpected"}, nil
	}}
	session := connectTestClient(t, fake, true)

	result, text := callTool(t, session, "delete_backup", map[string]any{"filename": "b.tar.gz"})
	if !result.IsError || !strings.Contains(text, "不存在") {
		t.Fatalf("删除不存在的备份应当报错（面板对不存在的文件也回成功），实际 %s", text)
	}
	for _, call := range fake.recorded() {
		if call.method == http.MethodDelete {
			t.Fatal("备份不存在时不应发出 DELETE")
		}
	}

	result, text = callTool(t, session, "delete_backup", map[string]any{"filename": "a.tar.gz"})
	if result.IsError {
		t.Fatalf("删除存在的备份不应失败: %s", text)
	}
	calls := fake.recorded()
	last := calls[len(calls)-1]
	if last.method != http.MethodDelete || last.query.Get("filename") != "a.tar.gz" {
		t.Fatalf("应当以 DELETE /system/backup?filename= 删除，实际 %s %s %v", last.method, last.path, last.query)
	}
}

func TestRestoreBackupForwardsAndWarns(t *testing.T) {
	fail := false
	fake := &fakeDispatcher{respond: func(recordedCall) (int, any, error) {
		if fail {
			return http.StatusInternalServerError, map[string]any{"error": "恢复失败: 加密备份需要密码"}, nil
		}
		return http.StatusOK, map[string]any{"message": "恢复成功"}, nil
	}}
	session := connectTestClient(t, fake, true)

	result, text := callTool(t, session, "restore_backup", map[string]any{"filename": "a.enc", "password": "p"})
	if result.IsError {
		t.Fatalf("restore_backup 不应失败: %s", text)
	}
	call := fake.recorded()[0]
	if call.method != http.MethodPost || call.path != "/system/restore" || !reflect.DeepEqual(call.body, map[string]any{"filename": "a.enc", "password": "p"}) {
		t.Fatalf("应当以 POST /system/restore 转发，实际 %s %s %#v", call.method, call.path, call.body)
	}
	if hint, _ := decodeObject(t, text)["hint"].(string); !strings.Contains(hint, "重启") {
		t.Fatalf("恢复成功后应当提醒重启面板，实际 %s", text)
	}

	fail = true
	if result, text := callTool(t, session, "restore_backup", map[string]any{"filename": "a.enc"}); !result.IsError || !strings.Contains(text, "加密备份需要密码") {
		t.Fatalf("面板的恢复错误应当原样带回，实际 %s", text)
	}

	// 高风险工具的描述必须写明后果，客户端据此向用户确认。
	tools := listedTools(t, session)
	description := tools["restore_backup"].Description
	for _, keyword := range []string{"清空", "无法找回", "create_backup", "凭据"} {
		if !strings.Contains(description, keyword) {
			t.Errorf("restore_backup 的描述应当写明后果（缺少 %q）: %s", keyword, description)
		}
	}
}

// 面板只允许管理员账号本人恢复、删除备份，应用凭据调用会 403；工具描述要提前说清，免得 AI 白试。
func TestDestructiveBackupToolsMentionAdminOnly(t *testing.T) {
	session := connectTestClient(t, &fakeDispatcher{respond: okResponder(map[string]any{})}, true)
	tools := listedTools(t, session)
	for _, name := range []string{"restore_backup", "delete_backup"} {
		if !strings.Contains(tools[name].Description, "管理员账号") {
			t.Errorf("%s 的描述应当说明只有管理员账号能用: %s", name, tools[name].Description)
		}
	}
}
