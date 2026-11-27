package mcptools

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"reflect"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type recordedCall struct {
	method string
	path   string
	query  url.Values
	body   any
}

// fakeDispatcher 记录每次接口调用，并按 respond 给出响应；payload 为 string / []byte 时原样返回，否则按 JSON 编码。
type fakeDispatcher struct {
	mu      sync.Mutex
	calls   []recordedCall
	respond func(call recordedCall) (int, any, error)
}

func (f *fakeDispatcher) Do(_ context.Context, method, path string, query url.Values, body any) (int, []byte, error) {
	call := recordedCall{method: method, path: path, query: query, body: body}
	f.mu.Lock()
	f.calls = append(f.calls, call)
	f.mu.Unlock()

	if f.respond == nil {
		return http.StatusNotFound, []byte(`{"error":"未配置响应"}`), nil
	}
	status, payload, err := f.respond(call)
	if err != nil {
		return 0, nil, err
	}
	switch value := payload.(type) {
	case string:
		return status, []byte(value), nil
	case []byte:
		return status, value, nil
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return 0, nil, err
	}
	return status, data, nil
}

func (f *fakeDispatcher) recorded() []recordedCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]recordedCall(nil), f.calls...)
}

func connectTestClient(t *testing.T, d Dispatcher, allowMutations bool) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := BuildServer(d, allowMutations).Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatalf("server connect: %v", err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "mcptools-test", Version: "1.0.0"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() {
		_ = session.Close()
		_ = serverSession.Wait()
	})
	return session
}

func callTool(t *testing.T, session *mcp.ClientSession, name string, args map[string]any) (*mcp.CallToolResult, string) {
	t.Helper()
	if args == nil {
		args = map[string]any{}
	}
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("call %s: %v", name, err)
	}
	return result, resultText(result)
}

func resultText(result *mcp.CallToolResult) string {
	parts := make([]string, 0, len(result.Content))
	for _, content := range result.Content {
		if text, ok := content.(*mcp.TextContent); ok {
			parts = append(parts, text.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func decodeObject(t *testing.T, text string) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("工具输出不是合法 JSON 对象: %v\n%s", err, text)
	}
	return out
}

func listedTools(t *testing.T, session *mcp.ClientSession) map[string]*mcp.Tool {
	t.Helper()
	result, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	tools := make(map[string]*mcp.Tool, len(result.Tools))
	for _, tool := range result.Tools {
		tools[tool.Name] = tool
	}
	return tools
}

func sortedNames(tools map[string]*mcp.Tool) []string {
	names := make([]string, 0, len(tools))
	for name := range tools {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func sortedCopy(lists ...[]string) []string {
	var out []string
	for _, list := range lists {
		out = append(out, list...)
	}
	sort.Strings(out)
	return out
}

func okResponder(payload any) func(recordedCall) (int, any, error) {
	return func(recordedCall) (int, any, error) { return http.StatusOK, payload, nil }
}

// ---- 分层 --------------------------------------------------------------------

func TestReadOnlyServerListsExactlyTheReadTools(t *testing.T) {
	session := connectTestClient(t, &fakeDispatcher{}, false)
	tools := listedTools(t, session)

	if got, want := sortedNames(tools), sortedCopy(readToolNames); !reflect.DeepEqual(got, want) {
		t.Fatalf("关闭写入时工具清单应当只有只读工具\n  实际: %v\n  期望: %v", got, want)
	}
	for name, tool := range tools {
		if tool.Annotations == nil || !tool.Annotations.ReadOnlyHint {
			t.Errorf("只读工具 %s 必须标注 readOnlyHint", name)
		}
		if strings.TrimSpace(tool.Description) == "" {
			t.Errorf("工具 %s 缺少描述", name)
		}
	}
}

func TestMutationServerAddsWriteToolsWithAnnotations(t *testing.T) {
	session := connectTestClient(t, &fakeDispatcher{}, true)
	tools := listedTools(t, session)

	if got, want := sortedNames(tools), sortedCopy(readToolNames, writeToolNames); !reflect.DeepEqual(got, want) {
		t.Fatalf("开启写入后工具清单不对\n  实际: %v\n  期望: %v", got, want)
	}
	for _, name := range writeToolNames {
		annotations := tools[name].Annotations
		if annotations == nil || annotations.ReadOnlyHint {
			t.Errorf("写入工具 %s 不能标注 readOnlyHint", name)
			continue
		}
		if annotations.DestructiveHint == nil {
			t.Errorf("写入工具 %s 必须显式给出 destructiveHint（缺省会被客户端当成破坏性操作）", name)
		}
	}
	for _, name := range []string{
		"delete_env", "batch_task_action",
		// #139：删除、覆盖、恢复、批量删除类
		"update_task", "delete_script", "rename_script", "move_script", "copy_script", "batch_delete_scripts",
		"run_code", "rollback_script", "update_subscription", "delete_subscription", "batch_env_action",
		"import_envs", "create_backup", "delete_backup", "restore_backup",
		// #149：覆盖已有的通知开关设置
		"batch_set_task_notify",
	} {
		if hint := tools[name].Annotations.DestructiveHint; hint == nil || !*hint {
			t.Errorf("删除类工具 %s 必须标注 destructiveHint: true", name)
		}
	}
	for _, name := range []string{"run_task", "create_task", "create_subscription", "set_subscription_enabled", "send_notification"} {
		if hint := tools[name].Annotations.DestructiveHint; hint == nil || *hint {
			t.Errorf("%s 不会删除或覆盖已有数据，destructiveHint 应为 false", name)
		}
	}
}

func TestWriteToolsCannotBeCalledWhenMutationsDisabled(t *testing.T) {
	fake := &fakeDispatcher{respond: okResponder(map[string]any{"message": "ok"})}
	session := connectTestClient(t, fake, false)

	for _, name := range writeToolNames {
		result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: map[string]any{"id": 1}})
		if err == nil && (result == nil || !result.IsError) {
			t.Errorf("关闭写入时直接调用 %s 必须失败", name)
		}
	}
	if calls := fake.recorded(); len(calls) != 0 {
		t.Fatalf("写入工具被拦截前不应触达面板接口，实际调用了 %d 次: %+v", len(calls), calls)
	}
}

// ---- 任务 --------------------------------------------------------------------

func TestListTasksBuildsQueryAndSlimsItems(t *testing.T) {
	fake := &fakeDispatcher{respond: okResponder(map[string]any{
		"data": []any{
			// 禁用任务被手动运行：status 是运行中，但启用开关仍是关（#133 的场景）。
			map[string]any{"id": 1, "name": "禁用中手动运行", "status": 2, "enabled": false, "labels": []any{"分组:京东", "每日"}, "pid": 123, "log_path": "x.log", "last_run_status": nil},
			// 老面板没有 enabled 字段时按 status != 0 回退。
			map[string]any{"id": 2, "name": "老面板禁用", "status": 0, "labels": []any{}, "last_run_status": 1},
			map[string]any{"id": 3, "name": "老面板空闲", "status": 1, "labels": []any{" 分组: 其它 "}, "last_run_status": 0},
		},
		"total": 3, "page": 2, "page_size": 100,
	})}
	session := connectTestClient(t, fake, false)

	result, text := callTool(t, session, "list_tasks", map[string]any{
		"keyword": "jd", "status": "running", "group": "京东", "label": "每日", "page": 2, "page_size": 500,
	})
	if result.IsError {
		t.Fatalf("list_tasks 不应失败: %s", text)
	}

	calls := fake.recorded()
	if len(calls) != 1 || calls[0].method != http.MethodGet || calls[0].path != "/tasks" {
		t.Fatalf("应当只调用一次 GET /tasks，实际 %+v", calls)
	}
	query := calls[0].query
	for key, want := range map[string]string{"keyword": "jd", "status": "2", "label": "每日", "page": "2", "page_size": "100"} {
		if got := query.Get(key); got != want {
			t.Errorf("query %s = %q，期望 %q", key, got, want)
		}
	}
	var filters []map[string]string
	if err := json.Unmarshal([]byte(query.Get("filters")), &filters); err != nil || len(filters) != 1 ||
		filters[0]["field"] != "group" || filters[0]["operator"] != "equals" || filters[0]["value"] != "京东" {
		t.Fatalf("分组应当走 filters 的 group equals，实际 %q", query.Get("filters"))
	}

	out := decodeObject(t, text)
	items, _ := out["items"].([]any)
	if len(items) != 3 {
		t.Fatalf("应当返回 3 条任务，实际 %v", out["items"])
	}
	first := items[0].(map[string]any)
	if first["enabled"] != false || first["status_text"] != "运行中" || first["group"] != "京东" || first["last_run_status_text"] != "未运行" {
		t.Errorf("第一条任务的派生字段不对: %v", first)
	}
	if _, leaked := first["pid"]; leaked {
		t.Errorf("精简输出不应包含 pid 这类内部字段: %v", first)
	}
	second := items[1].(map[string]any)
	if second["enabled"] != false || second["status_text"] != "禁用" || second["last_run_status_text"] != "失败" {
		t.Errorf("第二条任务的派生字段不对: %v", second)
	}
	third := items[2].(map[string]any)
	if third["enabled"] != true || third["group"] != "其它" || third["last_run_status_text"] != "成功" {
		t.Errorf("第三条任务的派生字段不对: %v", third)
	}
	if out["total"] != float64(3) {
		t.Errorf("total 应原样透传，实际 %v", out["total"])
	}
}

func TestListTasksRejectsUnknownStatus(t *testing.T) {
	fake := &fakeDispatcher{}
	session := connectTestClient(t, fake, false)
	result, text := callTool(t, session, "list_tasks", map[string]any{"status": "sleeping"})
	if !result.IsError || !strings.Contains(text, "status 只能是") {
		t.Fatalf("未知状态应当报错，实际 isError=%v: %s", result.IsError, text)
	}
	if len(fake.recorded()) != 0 {
		t.Fatal("参数校验失败时不应调用面板接口")
	}
}

func TestGetTaskPicksItemFromFullList(t *testing.T) {
	fake := &fakeDispatcher{respond: okResponder(map[string]any{
		"data": []any{
			map[string]any{"id": 5, "name": "别的任务", "status": 1},
			map[string]any{"id": 7, "name": "目标任务", "status": 1, "command": "task demo.js"},
		},
	})}
	session := connectTestClient(t, fake, false)

	result, text := callTool(t, session, "get_task", map[string]any{"id": 7})
	if result.IsError {
		t.Fatalf("get_task 不应失败: %s", text)
	}
	out := decodeObject(t, text)
	if out["name"] != "目标任务" || out["command"] != "task demo.js" || out["enabled"] != true {
		t.Fatalf("get_task 返回的不是目标任务: %v", out)
	}
	if calls := fake.recorded(); calls[0].query.Get("all") != "1" {
		t.Fatalf("get_task 应当用 all=1 取全量，实际 %v", calls[0].query)
	}

	result, text = callTool(t, session, "get_task", map[string]any{"id": 99})
	if !result.IsError || !strings.Contains(text, "任务 99 不存在") {
		t.Fatalf("不存在的任务应当报错，实际 isError=%v: %s", result.IsError, text)
	}
}

func TestGetTaskLogKeepsTailWithinBudget(t *testing.T) {
	content := "START-MARKER\n" + strings.Repeat("x", 100*1024) + "\nEND-ERROR"
	fake := &fakeDispatcher{respond: okResponder(map[string]any{"id": 11, "task_id": 3, "status": 1, "content": content})}
	session := connectTestClient(t, fake, false)

	result, text := callTool(t, session, "get_task_log", map[string]any{"id": 3})
	if result.IsError {
		t.Fatalf("get_task_log 不应失败: %s", text)
	}
	if path := fake.recorded()[0].path; path != "/tasks/3/latest-log" {
		t.Fatalf("应当调用 /tasks/3/latest-log，实际 %s", path)
	}
	out := decodeObject(t, text)
	logText, _ := out["content"].(string)
	if !strings.Contains(logText, "END-ERROR") || strings.Contains(logText, "START-MARKER") {
		t.Fatal("长日志应当保留末尾、丢掉开头")
	}
	if _, ok := out["content_truncated"]; !ok || out["status_text"] != "失败" {
		t.Fatalf("应当注明截断并给出状态文字: %v", out["content_truncated"])
	}
	if len(text) > MaxOutputBytes {
		t.Fatalf("输出超过上限: %d", len(text))
	}
}

// list_task_labels（#157）：GET /tasks/labels 返回裸数组，通用的 call 解不了，工具要单独解码；
// 非 2xx 照常翻成带原因的工具错误；2xx 却不是数组时如实报「无法解析」，不能当成「没有标签」。
func TestListTaskLabelsDecodesBareArray(t *testing.T) {
	cases := []struct {
		name      string
		status    int
		payload   string
		wantOut   map[string]any
		wantError string
	}{
		{
			name:    "labels",
			status:  http.StatusOK,
			payload: `[{"name":"Prod","count":1},{"name":"京东","count":3}]`,
			wantOut: map[string]any{"total": float64(2), "labels": []any{
				map[string]any{"name": "Prod", "count": float64(1)},
				map[string]any{"name": "京东", "count": float64(3)},
			}},
		},
		{
			name:    "no labels",
			status:  http.StatusOK,
			payload: `[]`,
			wantOut: map[string]any{"total": float64(0), "labels": []any{}},
		},
		{
			name:      "missing scope",
			status:    http.StatusForbidden,
			payload:   `{"error":"应用无权访问此资源"}`,
			wantError: "应用无权访问此资源",
		},
		{
			name:      "not an array",
			status:    http.StatusOK,
			payload:   `{"data":[],"total":0}`,
			wantError: "无法解析",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeDispatcher{respond: func(recordedCall) (int, any, error) { return tc.status, tc.payload, nil }}
			session := connectTestClient(t, fake, false)

			result, text := callTool(t, session, "list_task_labels", nil)
			calls := fake.recorded()
			if len(calls) != 1 || calls[0].method != http.MethodGet || calls[0].path != "/tasks/labels" || len(calls[0].query) != 0 || calls[0].body != nil {
				t.Fatalf("应当只调用一次不带参数的 GET /tasks/labels，实际 %+v", calls)
			}
			if tc.wantError != "" {
				if !result.IsError || !strings.Contains(text, tc.wantError) {
					t.Fatalf("应当报错并带上「%s」，实际 isError=%v: %s", tc.wantError, result.IsError, text)
				}
				return
			}
			if result.IsError {
				t.Fatalf("list_task_labels 不应失败: %s", text)
			}
			if out := decodeObject(t, text); !reflect.DeepEqual(out, tc.wantOut) {
				t.Fatalf("应当原样给出标签数组与总数，期望 %v，实际 %s", tc.wantOut, text)
			}
		})
	}
}

// ---- 环境变量 ----------------------------------------------------------------

func TestListEnvsMasksSensitiveValues(t *testing.T) {
	fake := &fakeDispatcher{respond: okResponder(map[string]any{
		"data": []any{
			map[string]any{"id": 1, "name": "JD_COOKIE", "value": "pt_key=abcdef;pt_pin=zz;", "enabled": true},
			map[string]any{"id": 2, "name": "BARK_KEY", "value": "short", "enabled": true},
			map[string]any{"id": 3, "name": "SEARCH_KEYWORD", "value": "京东 超市", "enabled": true},
		},
		"total": 3,
	})}
	session := connectTestClient(t, fake, false)

	result, text := callTool(t, session, "list_envs", nil)
	if result.IsError {
		t.Fatalf("list_envs 不应失败: %s", text)
	}
	if strings.Contains(text, "abcdef") {
		t.Fatalf("敏感变量的明文不应出现在输出里: %s", text)
	}
	items := decodeObject(t, text)["items"].([]any)
	want := []struct {
		value  string
		masked bool
	}{
		{"pt_******zz;", true},
		{"********", true},
		{"京东 超市", false},
	}
	for i, expect := range want {
		item := items[i].(map[string]any)
		if item["value"] != expect.value {
			t.Errorf("第 %d 条 value = %v，期望 %q", i+1, item["value"], expect.value)
		}
		if masked, _ := item["value_masked"].(bool); masked != expect.masked {
			t.Errorf("第 %d 条 value_masked = %v，期望 %v", i+1, item["value_masked"], expect.masked)
		}
	}
}

// 关键词搜索不能变成探测凭据的工具：敏感变量的值里含不含关键词，工具输出必须逐字节相同。
// 否则 AI 拿 pt_key=a、pt_key=ab……一段段试，就能从「有没有这行 / total 是几 / 隐藏了几条」里拼出凭据。
func TestListEnvsKeywordSearchDoesNotRevealSensitiveValues(t *testing.T) {
	run := func(cookieValue string) (string, []recordedCall) {
		fake := &fakeDispatcher{respond: okResponder(map[string]any{
			"data": []any{
				map[string]any{"id": 1, "name": "JD_COOKIE", "value": cookieValue, "remarks": ""},
				map[string]any{"id": 2, "name": "API_TOKEN", "value": "tok-1234567890", "remarks": "abc 备用"},
				map[string]any{"id": 3, "name": "PLAIN", "value": "xxABCxx"},
				map[string]any{"id": 4, "name": "OTHER", "value": "nothing"},
			},
			"total": 4, "page": 1, "page_size": 4,
		})}
		session := connectTestClient(t, fake, false)
		result, text := callTool(t, session, "list_envs", map[string]any{"keyword": "abc", "group": "g1"})
		if result.IsError {
			t.Fatalf("list_envs 不应失败: %s", text)
		}
		return text, fake.recorded()
	}

	withHit, calls := run("pt_key=abc123456;")
	withoutHit, _ := run("pt_key=zzz999999;")
	if withHit != withoutHit {
		t.Fatalf("敏感变量的值含不含关键词，输出必须完全一致\n  含: %s\n  不含: %s", withHit, withoutHit)
	}

	query := calls[0].query
	if query.Has("keyword") || query.Get("all") != "1" || query.Get("group") != "g1" {
		t.Fatalf("关键词不能交给面板（面板会按值匹配），应当带着分组 all=1 取全量在本地筛，实际 %v", query)
	}

	out := decodeObject(t, withHit)
	items, _ := out["items"].([]any)
	if len(items) != 2 || out["total"] != float64(2) {
		t.Fatalf("应当只命中按备注匹配的敏感变量与按值匹配的普通变量，实际 total=%v items=%v", out["total"], items)
	}
	first, second := items[0].(map[string]any), items[1].(map[string]any)
	if first["name"] != "API_TOKEN" || first["value"] != "tok******890" {
		t.Errorf("按备注命中的敏感变量应当保留并遮蔽: %v", first)
	}
	if second["name"] != "PLAIN" || second["value"] != "xxABCxx" {
		t.Errorf("普通变量按值命中（不区分大小写）应当照常返回: %v", second)
	}
	if _, leaked := out["hidden"]; leaked {
		t.Errorf("不能报告「因值命中而隐藏」的条数，那本身就是探测信号: %v", out)
	}
}

func TestListEnvsKeywordSearchPaginatesLocally(t *testing.T) {
	fake := &fakeDispatcher{respond: okResponder(map[string]any{
		"data": []any{
			map[string]any{"id": 1, "name": "PLAIN_1", "value": "a"},
			map[string]any{"id": 2, "name": "PLAIN_2", "value": "b"},
			map[string]any{"id": 3, "name": "PLAIN_3", "value": "c"},
			map[string]any{"id": 4, "name": "UNRELATED", "value": "d"},
		},
		"total": 4,
	})}
	session := connectTestClient(t, fake, false)

	result, text := callTool(t, session, "list_envs", map[string]any{"keyword": "plain", "page": 2, "page_size": 2})
	if result.IsError {
		t.Fatalf("list_envs 不应失败: %s", text)
	}
	out := decodeObject(t, text)
	items, _ := out["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["name"] != "PLAIN_3" || out["total"] != float64(3) ||
		out["page"] != float64(2) || out["page_size"] != float64(2) {
		t.Fatalf("第 2 页（每页 2 条）应当只剩 PLAIN_3，total=3，实际 %s", text)
	}

	// 页码越界（含乘法溢出级别的大页码）返回空页而不是报错或崩溃；每页上限与面板一致为 100。
	result, text = callTool(t, session, "list_envs", map[string]any{"keyword": "plain", "page": int64(1) << 60, "page_size": 500})
	if result.IsError {
		t.Fatalf("越界页码不应失败: %s", text)
	}
	out = decodeObject(t, text)
	if items, _ := out["items"].([]any); len(items) != 0 || out["page_size"] != float64(100) || out["total"] != float64(3) {
		t.Fatalf("越界页码应当返回空页、page_size 收到 100，实际 %s", text)
	}
}

func TestUpdateEnvSendsOnlyProvidedFieldsAndMasksResult(t *testing.T) {
	fake := &fakeDispatcher{respond: okResponder(map[string]any{
		"message": "更新成功",
		"data":    map[string]any{"id": 3, "name": "BARK_KEY", "value": "abcdefghijkl", "enabled": true},
	})}
	session := connectTestClient(t, fake, true)

	result, text := callTool(t, session, "update_env", map[string]any{"id": 3, "value": "abcdefghijkl"})
	if result.IsError {
		t.Fatalf("update_env 不应失败: %s", text)
	}
	call := fake.recorded()[0]
	if call.method != http.MethodPut || call.path != "/envs/3" {
		t.Fatalf("应当调用 PUT /envs/3，实际 %s %s", call.method, call.path)
	}
	if body, _ := call.body.(map[string]any); !reflect.DeepEqual(body, map[string]any{"value": "abcdefghijkl"}) {
		t.Fatalf("只应发送传入的字段，实际 %#v", call.body)
	}
	env := decodeObject(t, text)["env"].(map[string]any)
	if env["value"] != "abc******jkl" || env["value_masked"] != true {
		t.Fatalf("返回值里的敏感变量也要遮蔽: %v", env)
	}

	result, text = callTool(t, session, "update_env", map[string]any{"id": 3})
	if !result.IsError || !strings.Contains(text, "没有要修改的字段") {
		t.Fatalf("什么都不改时应当报错，实际 %s", text)
	}
}

func TestDeleteEnvChecksExistenceFirst(t *testing.T) {
	fake := &fakeDispatcher{respond: func(call recordedCall) (int, any, error) {
		switch {
		case call.method == http.MethodGet && call.path == "/envs/9":
			return http.StatusNotFound, map[string]any{"error": "环境变量不存在"}, nil
		case call.method == http.MethodGet && call.path == "/envs/4":
			return http.StatusOK, map[string]any{"data": map[string]any{"id": 4, "name": "FOO"}}, nil
		case call.method == http.MethodDelete && call.path == "/envs/4":
			return http.StatusOK, map[string]any{"message": "删除成功"}, nil
		}
		return http.StatusTeapot, map[string]any{"error": "unexpected"}, nil
	}}
	session := connectTestClient(t, fake, true)

	result, text := callTool(t, session, "delete_env", map[string]any{"id": 9})
	if !result.IsError || !strings.Contains(text, "环境变量不存在") {
		t.Fatalf("删除不存在的变量应当报错，实际 %s", text)
	}
	for _, call := range fake.recorded() {
		if call.method == http.MethodDelete {
			t.Fatal("变量不存在时不应发出 DELETE")
		}
	}

	result, text = callTool(t, session, "delete_env", map[string]any{"id": 4})
	if result.IsError || decodeObject(t, text)["name"] != "FOO" {
		t.Fatalf("删除存在的变量应当成功并带上名称，实际 %s", text)
	}
}

func TestCreateEnvReportsPerItemErrors(t *testing.T) {
	fake := &fakeDispatcher{respond: okResponder(map[string]any{
		"message": "新增 0 条", "errors": []any{"第 1 项: 变量名 'a-b' 格式无效"}, "created": 0, "data": []any{},
	})}
	session := connectTestClient(t, fake, true)

	result, text := callTool(t, session, "create_env", map[string]any{"name": "a-b", "value": "1"})
	if !result.IsError || !strings.Contains(text, "格式无效") {
		t.Fatalf("接口 200 + errors 应当翻译成工具错误，实际 isError=%v: %s", result.IsError, text)
	}
}

// ---- 批量与脚本 --------------------------------------------------------------

func TestBatchTaskActionValidatesAndForwards(t *testing.T) {
	fake := &fakeDispatcher{respond: okResponder(map[string]any{"message": "批量delete: 2 个任务", "count": 2})}
	session := connectTestClient(t, fake, true)

	for _, args := range []map[string]any{
		{"ids": []int{1, 2}, "action": "explode"},
		{"ids": []int{}, "action": "run"},
		{"ids": []int{0}, "action": "run"},
	} {
		if result, text := callTool(t, session, "batch_task_action", args); !result.IsError {
			t.Errorf("非法参数 %v 应当报错，实际 %s", args, text)
		}
	}
	if len(fake.recorded()) != 0 {
		t.Fatal("参数校验失败时不应调用面板接口")
	}

	result, text := callTool(t, session, "batch_task_action", map[string]any{"ids": []int{1, 2}, "action": "DELETE"})
	if result.IsError {
		t.Fatalf("合法的批量操作不应失败: %s", text)
	}
	call := fake.recorded()[0]
	body, _ := call.body.(map[string]any)
	if call.method != http.MethodPut || call.path != "/tasks/batch" || body["action"] != "delete" || !reflect.DeepEqual(body["ids"], []int64{1, 2}) {
		t.Fatalf("应当以 PUT /tasks/batch 转发（动作小写），实际 %s %s %#v", call.method, call.path, call.body)
	}
}

// batch_set_task_notify（#149）：参数不合法时不触达面板；ids 转成接口的 task_ids，
// 只发送传了的开关（false 也要原样发出），all 为 true 时不带 task_ids；面板回 404 时工具报错。
func TestBatchSetTaskNotifyValidatesAndForwards(t *testing.T) {
	fake := &fakeDispatcher{respond: func(call recordedCall) (int, any, error) {
		body, _ := call.body.(map[string]any)
		if ids, _ := body["task_ids"].([]int64); len(ids) == 1 && ids[0] == 404 {
			return http.StatusNotFound, map[string]any{"error": "没有找到要修改的任务"}, nil
		}
		return http.StatusOK, map[string]any{"message": "已更新 2 个任务的通知设置", "success_count": 2}, nil
	}}
	session := connectTestClient(t, fake, true)

	for _, args := range []map[string]any{
		{"ids": []int{1, 2}},                                 // 一个开关都没传
		{"all": true},                                        // all 也要至少传一个开关
		{"notify_on_failure": true},                          // 没有 ids 也没有 all
		{"ids": []int{}, "notify_on_failure": true},          // ids 为空
		{"ids": []int{0}, "notify_on_failure": true},         // 非法 ID
		{"ids": make([]int, 101), "notify_on_failure": true}, // 超过 100 个
	} {
		if result, text := callTool(t, session, "batch_set_task_notify", args); !result.IsError {
			t.Errorf("非法参数 %v 应当报错，实际 %s", args, text)
		}
	}
	if len(fake.recorded()) != 0 {
		t.Fatal("参数校验失败时不应调用面板接口")
	}

	result, text := callTool(t, session, "batch_set_task_notify", map[string]any{
		"ids": []int{1, 2}, "notify_on_failure": true, "notify_on_abort": false,
	})
	if result.IsError {
		t.Fatalf("合法的批量设置不应失败: %s", text)
	}
	call := fake.recorded()[0]
	want := map[string]any{"task_ids": []int64{1, 2}, "notify_on_failure": true, "notify_on_abort": false}
	if call.method != http.MethodPut || call.path != "/tasks/batch/notify" || !reflect.DeepEqual(call.body, want) {
		t.Fatalf("应当以 PUT /tasks/batch/notify 转发、只带传了的开关，实际 %s %s %#v", call.method, call.path, call.body)
	}
	if out := decodeObject(t, text); out["message"] != "已更新 2 个任务的通知设置" || out["success_count"] != float64(2) {
		t.Fatalf("应当返回面板的 message 与 success_count，实际 %v", out)
	}

	// all 为 true 时忽略 ids，请求体里不带 task_ids。
	if result, text := callTool(t, session, "batch_set_task_notify", map[string]any{
		"all": true, "ids": []int{9}, "notify_on_success": true,
	}); result.IsError {
		t.Fatalf("all 为 true 的批量设置不应失败: %s", text)
	}
	if got := fake.recorded()[1].body; !reflect.DeepEqual(got, map[string]any{"all": true, "notify_on_success": true}) {
		t.Fatalf("all 为 true 时请求体应当只有 all 与开关，实际 %#v", got)
	}

	// 一个都没命中时面板回 404，工具必须报错，不能把失败当成功转述。
	result, text = callTool(t, session, "batch_set_task_notify", map[string]any{"ids": []int{404}, "notify_on_failure": true})
	if !result.IsError || !strings.Contains(text, "没有找到要修改的任务") {
		t.Fatalf("面板回 404 时工具应当报错并带上原因，实际 isError=%v: %s", result.IsError, text)
	}
}

func useFastScriptPolling(t *testing.T) {
	t.Helper()
	initial, maximum := scriptPollInitialDelay, scriptPollMaxDelay
	scriptPollInitialDelay, scriptPollMaxDelay = time.Millisecond, 20*time.Millisecond
	t.Cleanup(func() { scriptPollInitialDelay, scriptPollMaxDelay = initial, maximum })
}

func TestRunScriptPollsUntilDoneThenClearsRun(t *testing.T) {
	useFastScriptPolling(t)
	var polls atomic.Int32
	fake := &fakeDispatcher{respond: func(call recordedCall) (int, any, error) {
		switch {
		case call.method == http.MethodPost && call.path == "/scripts/run":
			if body, _ := call.body.(map[string]any); body["path"] != "demo/a b.py" {
				return http.StatusBadRequest, map[string]any{"error": "bad path"}, nil
			}
			return http.StatusCreated, map[string]any{"message": "脚本已启动", "run_id": "171_a b.py"}, nil
		case call.method == http.MethodGet && call.path == "/scripts/run/171_a%20b.py/logs":
			if polls.Add(1) < 3 {
				return http.StatusOK, map[string]any{"data": map[string]any{"logs": []any{"hello"}, "done": false, "status": "running"}}, nil
			}
			return http.StatusOK, map[string]any{"data": map[string]any{"logs": []any{"hello", "world"}, "done": true, "exit_code": 0, "status": "success"}}, nil
		case call.method == http.MethodDelete && call.path == "/scripts/run/171_a%20b.py":
			return http.StatusOK, map[string]any{"message": "已清除"}, nil
		}
		return http.StatusTeapot, map[string]any{"error": "unexpected " + call.method + " " + call.path}, nil
	}}
	session := connectTestClient(t, fake, true)

	result, text := callTool(t, session, "run_script", map[string]any{"path": "demo/a b.py"})
	if result.IsError {
		t.Fatalf("run_script 不应失败: %s", text)
	}
	out := decodeObject(t, text)
	if out["done"] != true || out["output"] != "hello\nworld" || out["run_id"] != "171_a b.py" || out["exit_code"] != float64(0) {
		t.Fatalf("运行结束后的输出不对: %v", out)
	}
	calls := fake.recorded()
	last := calls[len(calls)-1]
	if last.method != http.MethodDelete {
		t.Fatalf("运行结束后应当清掉面板内存里的运行记录，最后一次调用是 %s %s", last.method, last.path)
	}
}

func TestRunScriptReturnsRunIDWhileStillRunning(t *testing.T) {
	useFastScriptPolling(t)
	fake := &fakeDispatcher{respond: func(call recordedCall) (int, any, error) {
		if call.method == http.MethodPost {
			return http.StatusCreated, map[string]any{"run_id": "r1"}, nil
		}
		return http.StatusOK, map[string]any{"data": map[string]any{"logs": []any{"tick"}, "done": false, "status": "running"}}, nil
	}}
	session := connectTestClient(t, fake, true)

	result, text := callTool(t, session, "run_script", map[string]any{"path": "loop.py", "wait_seconds": 1})
	if result.IsError {
		t.Fatalf("run_script 不应失败: %s", text)
	}
	out := decodeObject(t, text)
	if out["done"] != false || out["run_id"] != "r1" || out["output"] != "tick" || out["hint"] == nil {
		t.Fatalf("超时仍在运行时应当返回 run_id 与已有输出: %v", out)
	}
	for _, call := range fake.recorded() {
		if call.method == http.MethodDelete {
			t.Fatal("还在运行时不能清掉运行记录，否则 run_id 就续不上了")
		}
	}
}

func TestRunScriptStopByRunID(t *testing.T) {
	useFastScriptPolling(t)
	fake := &fakeDispatcher{respond: func(call recordedCall) (int, any, error) {
		switch {
		case call.method == http.MethodPut && call.path == "/scripts/run/r2/stop":
			return http.StatusOK, map[string]any{"message": "已停止"}, nil
		case call.method == http.MethodGet:
			return http.StatusOK, map[string]any{"data": map[string]any{"logs": []any{}, "done": true, "status": "stopped"}}, nil
		case call.method == http.MethodDelete:
			return http.StatusOK, map[string]any{"message": "已清除"}, nil
		}
		return http.StatusTeapot, map[string]any{"error": "unexpected"}, nil
	}}
	session := connectTestClient(t, fake, true)

	result, text := callTool(t, session, "run_script", map[string]any{"run_id": "r2", "stop": true})
	if result.IsError || decodeObject(t, text)["done"] != true {
		t.Fatalf("按 run_id 停止应当成功并返回结束状态: %s", text)
	}
	if first := fake.recorded()[0]; first.method != http.MethodPut || first.path != "/scripts/run/r2/stop" {
		t.Fatalf("第一步应当是停止请求，实际 %s %s", first.method, first.path)
	}
}

func TestRunScriptArgumentValidation(t *testing.T) {
	fake := &fakeDispatcher{}
	session := connectTestClient(t, fake, true)
	for _, args := range []map[string]any{
		{},
		{"path": "a.py", "run_id": "x"},
		{"path": "a.py", "stop": true},
	} {
		if result, text := callTool(t, session, "run_script", args); !result.IsError {
			t.Errorf("参数 %v 应当报错，实际 %s", args, text)
		}
	}
	if len(fake.recorded()) != 0 {
		t.Fatal("参数校验失败时不应调用面板接口")
	}
}

// ---- 错误与截断 --------------------------------------------------------------

func TestPanelErrorsBecomeToolErrors(t *testing.T) {
	fake := &fakeDispatcher{respond: func(recordedCall) (int, any, error) {
		return http.StatusForbidden, map[string]any{"error": "应用无权访问此资源"}, nil
	}}
	session := connectTestClient(t, fake, false)

	result, text := callTool(t, session, "list_envs", nil)
	if !result.IsError || !strings.Contains(text, "HTTP 403") || !strings.Contains(text, "应用无权访问此资源") {
		t.Fatalf("面板 403 应当变成带原因的工具错误，实际 isError=%v: %s", result.IsError, text)
	}
}

func TestDispatcherFailureBecomesToolError(t *testing.T) {
	fake := &fakeDispatcher{respond: func(recordedCall) (int, any, error) {
		return 0, nil, errors.New("无法连接面板 http://127.0.0.1:5701")
	}}
	session := connectTestClient(t, fake, false)

	result, text := callTool(t, session, "get_system_info", nil)
	if !result.IsError || !strings.Contains(text, "无法连接面板") {
		t.Fatalf("连不上面板应当返回清楚的工具错误，实际 isError=%v: %s", result.IsError, text)
	}
}

func TestOutputIsCappedAt64KB(t *testing.T) {
	fake := &fakeDispatcher{respond: okResponder(map[string]any{"data": map[string]any{"blob": strings.Repeat("数", 40000)}})}
	session := connectTestClient(t, fake, false)

	result, text := callTool(t, session, "get_dashboard", nil)
	if result.IsError {
		t.Fatalf("get_dashboard 不应失败: %s", text)
	}
	if len(text) > MaxOutputBytes {
		t.Fatalf("输出 %d 字节，超过上限 %d", len(text), MaxOutputBytes)
	}
	if !strings.Contains(text, "已截断") {
		t.Fatal("超长输出应当注明已截断")
	}
}

// APP #30：v4.0.3 起面板的 429 文案自己带了「开放 API」的去处，MCP 不再重复追加提示；
// 老面板只回「应用调用频率超限」时仍然补上提示。
func TestAPIErrorRateLimitHintNotDuplicated(t *testing.T) {
	newPanel := apiError(http.StatusTooManyRequests, []byte(`{"error":"应用调用频率超限（每小时最多 5 次，可在面板「开放 API」里调整速率限制）"}`)).Error()
	if strings.Count(newPanel, "开放 API") != 1 {
		t.Fatalf("新面板的 429 文案已含「开放 API」，不应再追加提示：%s", newPanel)
	}
	if !strings.Contains(newPanel, "每小时最多 5 次") {
		t.Fatalf("应原样带上面板给的上限：%s", newPanel)
	}

	oldPanel := apiError(http.StatusTooManyRequests, []byte(`{"error":"应用调用频率超限"}`)).Error()
	if !strings.Contains(oldPanel, "（应用调用频率超限，可在「开放 API」调整速率限制）") {
		t.Fatalf("老面板的 429 仍应补上提示：%s", oldPanel)
	}
}
