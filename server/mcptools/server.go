// Package mcptools 是面板内置 MCP 服务（issue #128）的工具层。
//
// 设计要点（面向用户的说明见 docs/mcp.md）：
//   - 工具不碰数据库、也不调 service，一律经 Dispatcher 调面板自己的 /api/v1 接口。
//     HTTP 模式下 Dispatcher 在进程内回放请求（handler/mcp_dispatch.go），stdio 模式下
//     它是连本机面板的 HTTP 客户端（RemoteDispatcher）。这样开放 API 的 scope 校验、限流与
//     调用审计全部原样生效，MCP 没有多开任何一条绕过鉴权的路。
//   - 只读 / 写入分层在注册阶段完成：写入与执行类工具只在 allowMutations 为真时注册。
//     SDK 对没注册的工具一律回 unknown tool，所以 tools/list 看不到、tools/call 也调不到。
//   - 包名刻意不叫 mcp，避免和官方 SDK 的 mcp 包撞名。
package mcptools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Dispatcher 把一次工具调用落到面板的 /api/v1 接口上。
//
// path 以 / 开头、相对 /api/v1，并且已经转义（动态段由调用方 url.PathEscape）；
// body 非 nil 时按 JSON 发送。返回接口的 HTTP 状态码与原始响应体。
// 只有「请求没发出去 / 没拿到响应」才返回 err，4xx / 5xx 属于正常返回，由工具层翻译成错误。
type Dispatcher interface {
	Do(ctx context.Context, method, path string, query url.Values, body any) (status int, payload []byte, err error)
}

// ServerVersion 是 initialize 响应里 serverInfo.version 的值。
// 本包不能 import handler（handler 反过来 import 本包），所以由调用方在启动时写入 handler.Version。
var ServerVersion = "dev"

// MaxOutputBytes 是单次工具输出的上限，超出部分截掉并注明，避免一次查询撑爆 AI 的上下文。
const MaxOutputBytes = 64 * 1024

// contentBudget 是日志正文、脚本内容这类大字段单独截断时的预算。
// 比 MaxOutputBytes 小一截，给同一条输出里的其它字段留余量，这样截完仍是合法 JSON。
const contentBudget = 48 * 1024

// HTTP 无状态模式下每个请求都会新建一个 Server，共用一份 schema 缓存省掉重复反射。
var schemaCache = mcp.NewSchemaCache()

// readToolNames / writeToolNames 是两档工具的完整名单。
// 测试按它们逐个核对注册结果与注解，新增工具时必须同步，否则测试会红。
var (
	readToolNames = []string{
		"list_tasks", "get_task", "get_task_log", "list_logs", "get_log",
		"list_envs", "list_scripts", "read_script", "list_subscriptions",
		"get_system_info", "get_dashboard",
		// #139 对齐开放 API
		"get_script_tree", "list_script_versions", "export_envs", "list_backups",
		// #157 任务的已有标签（GET /tasks/labels，返回裸数组）
		"list_task_labels",
	}
	writeToolNames = []string{
		"run_task", "stop_task", "set_task_enabled", "batch_task_action",
		"create_env", "update_env", "delete_env",
		"save_script", "run_script", "pull_subscription",
		// #139 对齐开放 API
		"create_task", "update_task",
		"delete_script", "rename_script", "move_script", "copy_script", "batch_delete_scripts", "run_code", "rollback_script",
		"create_subscription", "update_subscription", "delete_subscription", "set_subscription_enabled",
		"batch_env_action", "import_envs",
		"send_notification", "create_backup", "delete_backup", "restore_backup",
		// #149 批量设置任务通知
		"batch_set_task_notify",
	}
)

// BuildServer 构造一个 MCP Server：只读工具总是注册，写入 / 执行工具只在 allowMutations 时注册。
func BuildServer(d Dispatcher, allowMutations bool) *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{
		Name:    "daidai-panel",
		Title:   "呆呆面板",
		Version: ServerVersion,
	}, &mcp.ServerOptions{
		Instructions: buildInstructions(allowMutations),
		SchemaCache:  schemaCache,
	})

	tools := &toolset{d: d}
	tools.registerReadTools(server)
	if allowMutations {
		tools.registerWriteTools(server)
	}
	return server
}

func buildInstructions(allowMutations bool) string {
	base := "这是呆呆面板（定时任务、脚本、环境变量管理面板）的内置 MCP 服务。" +
		"所有工具都经面板的开放接口执行，能访问哪些模块由当前凭据的权限决定，没有权限时工具会返回错误。" +
		"环境变量里名称像凭据（TOKEN、COOKIE、PASSWORD、KEY 等）的值会被遮蔽。"
	if allowMutations {
		return base + "当前允许写入与执行类工具（运行任务、修改环境变量、保存与运行脚本等），执行前请先向用户确认；删除、覆盖、恢复备份这类操作不可撤销。"
	}
	return base + "当前只开放查询类工具；写入与执行类工具需要管理员在面板「系统设置 → MCP 服务」中开启。"
}

type toolset struct {
	d Dispatcher
}

// writeHints 是写入类工具的注解。注解只是给客户端的提示，真正的拦截在注册阶段（见 BuildServer）。
type writeHints struct {
	destructive bool
	idempotent  bool
	openWorld   bool
}

func addReadTool[In any](s *mcp.Server, name, title, description string, h func(context.Context, In) (any, error)) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        name,
		Title:       title,
		Description: description,
		Annotations: &mcp.ToolAnnotations{
			Title:          title,
			ReadOnlyHint:   true,
			IdempotentHint: true,
			OpenWorldHint:  boolPtr(false),
		},
	}, adapt(h))
}

func addWriteTool[In any](s *mcp.Server, name, title, description string, hints writeHints, h func(context.Context, In) (any, error)) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        name,
		Title:       title,
		Description: description,
		Annotations: &mcp.ToolAnnotations{
			Title:           title,
			ReadOnlyHint:    false,
			DestructiveHint: boolPtr(hints.destructive),
			IdempotentHint:  hints.idempotent,
			OpenWorldHint:   boolPtr(hints.openWorld),
		},
	}, adapt(h))
}

// adapt 把「返回任意值或错误」的工具函数接到 SDK 上：成功时输出统一的 JSON 文本（超长截断），
// 失败时交给 SDK 包成 isError 的工具结果，AI 能看到原因并自行调整参数。
func adapt[In any](h func(context.Context, In) (any, error)) mcp.ToolHandlerFor[In, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, input In) (*mcp.CallToolResult, any, error) {
		output, err := h(ctx, input)
		if err != nil {
			return nil, nil, err
		}
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: renderOutput(output)}},
		}, nil, nil
	}
}

// call 发起一次接口调用并把 2xx 响应解成 map；非 2xx 翻译成带面板原始提示的错误。
// 返回裸数组的接口（例如 GET /tasks/labels）解不成 map，不能走这里，见 listTaskLabels。
func (t *toolset) call(ctx context.Context, method, path string, query url.Values, body any) (map[string]any, error) {
	status, payload, err := t.d.Do(ctx, method, path, query, body)
	if err != nil {
		return nil, err
	}
	if status < 200 || status > 299 {
		return nil, apiError(status, payload)
	}

	result := map[string]any{}
	if len(bytes.TrimSpace(payload)) == 0 {
		return result, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	// 保留数字原样（ID、状态 0.5 之类），重新序列化时不会变成科学计数法。
	decoder.UseNumber()
	if err := decoder.Decode(&result); err != nil {
		return nil, fmt.Errorf("面板接口返回了无法解析的内容（HTTP %d）", status)
	}
	return result, nil
}

func apiError(status int, payload []byte) error {
	message := ""
	var body map[string]any
	if json.Unmarshal(payload, &body) == nil {
		if text, ok := body["error"].(string); ok {
			message = text
		} else if text, ok := body["message"].(string); ok {
			message = text
		}
	}
	if message == "" {
		message = strings.TrimSpace(string(payload))
		if text, cut := headText(message, 300); cut {
			message = text + "…"
		}
	}
	if message == "" {
		message = http.StatusText(status)
	}

	hint := ""
	switch status {
	case http.StatusUnauthorized:
		hint = "（凭据无效或已过期）"
	case http.StatusForbidden:
		hint = "（当前凭据无权执行此操作：应用令牌请在「开放 API」给应用勾选对应模块的权限范围，登录账号请检查角色）"
	case http.StatusTooManyRequests:
		// v4.0.3 起面板的 429 文案自己就带了上限和「开放 API」的去处（APP #30），再追加一遍就重复了；
		// 老面板只回「应用调用频率超限」，仍然补上这句提示。
		if !strings.Contains(message, "开放 API") {
			hint = "（应用调用频率超限，可在「开放 API」调整速率限制）"
		}
	}
	return fmt.Errorf("面板接口返回 HTTP %d：%s%s", status, message, hint)
}

func renderOutput(value any) string {
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	// 不转义 < > &：输出是给人和 AI 读的，转成 < 只会增加噪音。
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return fmt.Sprintf("输出序列化失败：%v", err)
	}
	return truncateOutput(strings.TrimRight(buf.String(), "\n"))
}

// truncateOutput 是最后一道闸：保证单次输出不超过 MaxOutputBytes。
// 截断会破坏 JSON 结构，所以大字段应当先用 headText / tailText 在字段内截断，这里只兜底。
func truncateOutput(text string) string {
	if len(text) <= MaxOutputBytes {
		return text
	}
	cut := MaxOutputBytes - 512
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	return text[:cut] + fmt.Sprintf("\n…[输出超过 64KB 已截断，原始 %d 字节。请缩小范围，例如减小 page_size、加 keyword 或按 ID 查询]", len(text))
}

// headText 保留开头不超过 limit 字节（按 UTF-8 字符边界切）。
func headText(text string, limit int) (string, bool) {
	if len(text) <= limit {
		return text, false
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	return text[:cut], true
}

// tailText 保留末尾不超过 limit 字节（按 UTF-8 字符边界切）。日志的报错通常在最后，所以日志用它。
func tailText(text string, limit int) (string, bool) {
	if len(text) <= limit {
		return text, false
	}
	start := len(text) - limit
	for start < len(text) && !utf8.RuneStart(text[start]) {
		start++
	}
	return text[start:], true
}

func boolPtr(value bool) *bool {
	return &value
}

// numberValue 取 JSON 数字。call 开了 UseNumber，数字是 json.Number；其余分支给测试与兜底用。
func numberValue(value any) (float64, bool) {
	switch number := value.(type) {
	case json.Number:
		parsed, err := number.Float64()
		return parsed, err == nil
	case float64:
		return number, true
	case int:
		return float64(number), true
	case int64:
		return float64(number), true
	default:
		return 0, false
	}
}

func stringValue(value any) string {
	text, _ := value.(string)
	return text
}

// itemsOf 取列表接口 {"data": [...]} 里的对象数组。
func itemsOf(result map[string]any) []map[string]any {
	raw, _ := result["data"].([]any)
	items := make([]map[string]any, 0, len(raw))
	for _, entry := range raw {
		if item, ok := entry.(map[string]any); ok {
			items = append(items, item)
		}
	}
	return items
}

// objectOf 取 {"data": {...}} 里的对象；接口没包 data 时返回整个响应。
func objectOf(result map[string]any) map[string]any {
	if data, ok := result["data"].(map[string]any); ok {
		return data
	}
	return result
}

// pickFields 只保留存在的键，给 AI 的输出尽量精简。
func pickFields(item map[string]any, keys ...string) map[string]any {
	out := make(map[string]any, len(keys))
	for _, key := range keys {
		if value, ok := item[key]; ok {
			out[key] = value
		}
	}
	return out
}

func setPageQuery(query url.Values, page, pageSize int) {
	if page > 0 {
		query.Set("page", strconv.Itoa(page))
	}
	if pageSize > 0 {
		if pageSize > 100 {
			pageSize = 100
		}
		query.Set("page_size", strconv.Itoa(pageSize))
	}
}

func paginated(result map[string]any, items []map[string]any) map[string]any {
	out := map[string]any{"items": items}
	for _, key := range []string{"total", "page", "page_size"} {
		if value, ok := result[key]; ok {
			out[key] = value
		}
	}
	return out
}

func requireID(name string, id int64) error {
	if id <= 0 {
		return fmt.Errorf("%s 必须是正整数", name)
	}
	return nil
}
