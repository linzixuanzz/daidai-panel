package mcptools

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// 发送通知与系统备份（#139）。通知走 POST /notifications/send（权限 notifications），
// 备份走 /system/backup* 与 /system/restore（权限 backup；登录令牌要求管理员）。
// 其中删除与恢复只认管理员账号本人，应用凭据即使有 backup 权限也会被拒（见 backupAdminOnlyHint）。

func (t *toolset) registerNotifyBackupReadTools(s *mcp.Server) {
	addReadTool(s, "list_backups", "查询备份",
		"列出面板备份目录里的备份文件（name、size、created_at）。name 可传给 restore_backup / delete_backup。",
		t.listBackups)
}

func (t *toolset) registerNotifyBackupWriteTools(s *mcp.Server) {
	addWriteTool(s, "send_notification", "发送通知",
		"通过面板配置好的通知渠道发一条消息。可用 channel_ids / channel_names / channel_types 指定渠道；都不传时发给全部已启用、设为「默认推送」的渠道。消息会真实发到外部服务，发出后无法撤回。",
		writeHints{openWorld: true}, t.sendNotification)
	addWriteTool(s, "create_backup", "创建备份",
		"在面板备份目录生成一个备份文件，默认备份全部内容，可用 include 只备份其中几项；设了 password 会加密（.enc），恢复时需要同一密码。"+
			"name 与已有备份同名时会覆盖那个文件。数据多时耗时较长。",
		writeHints{destructive: true}, t.createBackup)
	addWriteTool(s, "delete_backup", "删除备份",
		"按文件名删除一个备份文件，不可恢复。"+backupAdminOnlyHint,
		writeHints{destructive: true, idempotent: true}, t.deleteBackup)
	addWriteTool(s, "restore_backup", "恢复备份",
		"⚠️ 用备份文件覆盖当前面板数据，不可撤销：备份里包含的每一项（任务与执行日志、订阅与 SSH 密钥、环境变量、配置〔含开放 API 应用、通知渠道、IP 白名单〕、依赖、任务视图、脚本目录）"+
			"都会先被清空，再写入备份里的内容，当前的这部分数据无法找回，务必先用 create_backup 备份一份。"+
			"恢复配置会替换开放 API 应用，当前 MCP 使用的凭据可能随之失效。网页端恢复后会重启面板，这个工具不会，恢复完成后请提醒管理员重启一次面板。大备份可能耗时较长。"+backupAdminOnlyHint,
		writeHints{destructive: true}, t.restoreBackup)
}

// 恢复与删除会覆盖或销毁数据，面板只允许管理员账号本人执行：用应用凭据（app_key / 应用令牌）连接时，
// 这两个工具会被面板以 403 拒绝，backup 权限只覆盖查询与创建备份。
const backupAdminOnlyHint = "仅限用管理员账号的登录令牌连接时使用；用应用凭据连接（即使勾了 backup 权限）调用会被拒绝。"

// ---- 通知 --------------------------------------------------------------------

type sendNotificationInput struct {
	Title        string   `json:"title" jsonschema:"通知标题"`
	Content      string   `json:"content" jsonschema:"通知正文"`
	ContentType  string   `json:"content_type,omitempty" jsonschema:"正文格式：text、markdown 或 html；不传则按各渠道自己的配置发送"`
	ChannelIDs   []int64  `json:"channel_ids,omitempty" jsonschema:"只发给这些 ID 的渠道"`
	ChannelNames []string `json:"channel_names,omitempty" jsonschema:"只发给这些名称的渠道（精确匹配）"`
	ChannelTypes []string `json:"channel_types,omitempty" jsonschema:"只发给这些类型的渠道，例如 email、bark、telegram、wecom、dingtalk、feishu、pushplus"`
}

func (t *toolset) sendNotification(ctx context.Context, in sendNotificationInput) (any, error) {
	title := strings.TrimSpace(in.Title)
	if title == "" || strings.TrimSpace(in.Content) == "" {
		return nil, errors.New("title 与 content 都不能为空")
	}
	body := map[string]any{"title": title, "content": in.Content}
	if contentType := strings.TrimSpace(in.ContentType); contentType != "" {
		body["content_type"] = contentType
	}
	if len(in.ChannelIDs) > 0 {
		for _, id := range in.ChannelIDs {
			// 面板会把 0 当成「点名了却没有有效渠道」回 400，这里提前给出同样的结论。
			if err := requireID("channel_ids 中的渠道 ID", id); err != nil {
				return nil, err
			}
		}
		body["channel_ids"] = in.ChannelIDs
	}
	if len(in.ChannelNames) > 0 {
		body["channel_names"] = in.ChannelNames
	}
	if len(in.ChannelTypes) > 0 {
		body["channel_types"] = in.ChannelTypes
	}
	result, err := t.call(ctx, http.MethodPost, "/notifications/send", nil, body)
	if err != nil {
		return nil, err
	}
	out := map[string]any{"message": result["message"]}
	for key, value := range pickFields(objectOf(result), "sent_count", "failed_count", "channel_names", "errors", "used_all") {
		out[key] = value
	}
	return out, nil
}

// ---- 备份 --------------------------------------------------------------------

type createBackupInput struct {
	Name     string   `json:"name,omitempty" jsonschema:"备份文件名（不含扩展名），留空按时间自动生成"`
	Password string   `json:"password,omitempty" jsonschema:"加密密码；设置后备份加密保存，恢复时需要同一密码"`
	Include  []string `json:"include,omitempty" jsonschema:"只备份这几项，默认全部：configs（配置）、tasks（任务）、subscriptions（订阅）、env_vars（环境变量）、logs（执行日志）、scripts（脚本）、dependencies（依赖）、task_views（任务视图）"`
}

type backupFileInput struct {
	Filename string `json:"filename" jsonschema:"备份文件名，可从 list_backups 获得，例如 backup_20260918_120000.tar.gz"`
}

type restoreBackupInput struct {
	Filename string `json:"filename" jsonschema:"备份文件名，可从 list_backups 获得"`
	Password string `json:"password,omitempty" jsonschema:"加密备份（.enc）的密码"`
}

// backupSelectionKeys 与 service.BackupSelection 的 json 字段一一对应。
var backupSelectionKeys = map[string]bool{
	"configs": true, "tasks": true, "subscriptions": true, "env_vars": true,
	"logs": true, "scripts": true, "dependencies": true, "task_views": true,
}

func (t *toolset) listBackups(ctx context.Context, _ emptyInput) (any, error) {
	result, err := t.call(ctx, http.MethodGet, "/system/backups", nil, nil)
	if err != nil {
		return nil, err
	}
	backups := itemsOf(result)
	return map[string]any{"total": len(backups), "backups": backups}, nil
}

func (t *toolset) createBackup(ctx context.Context, in createBackupInput) (any, error) {
	body := map[string]any{}
	if name := strings.TrimSpace(in.Name); name != "" {
		body["name"] = name
	}
	if in.Password != "" {
		body["password"] = in.Password
	}
	if len(in.Include) > 0 {
		selection := map[string]bool{}
		for _, raw := range in.Include {
			key := strings.ToLower(strings.TrimSpace(raw))
			if !backupSelectionKeys[key] {
				return nil, fmt.Errorf("include 只能包含 configs、tasks、subscriptions、env_vars、logs、scripts、dependencies、task_views，收到 %q", raw)
			}
			selection[key] = true
		}
		body["selection"] = selection
	}
	result, err := t.call(ctx, http.MethodPost, "/system/backup", nil, body)
	if err != nil {
		return nil, err
	}
	// 接口给的是服务器上的绝对路径，restore_backup / delete_backup 要的是文件名。
	fullPath := stringValue(objectOf(result)["path"])
	return map[string]any{
		"message":  result["message"],
		"filename": path.Base(strings.ReplaceAll(fullPath, "\\", "/")),
		"path":     fullPath,
	}, nil
}

func (t *toolset) deleteBackup(ctx context.Context, in backupFileInput) (any, error) {
	filename := strings.TrimSpace(in.Filename)
	if filename == "" {
		return nil, errors.New("filename 不能为空")
	}
	// 删除接口对不存在的文件也回成功；先查一次，传错文件名时能拿到明确的「不存在」。
	listed, err := t.call(ctx, http.MethodGet, "/system/backups", nil, nil)
	if err != nil {
		return nil, err
	}
	found := false
	for _, item := range itemsOf(listed) {
		if stringValue(item["name"]) == filename {
			found = true
			break
		}
	}
	if !found {
		return nil, fmt.Errorf("备份文件不存在：%s（可用 list_backups 查看）", filename)
	}
	result, err := t.call(ctx, http.MethodDelete, "/system/backup", url.Values{"filename": {filename}}, nil)
	if err != nil {
		return nil, err
	}
	return map[string]any{"message": result["message"], "filename": filename}, nil
}

func (t *toolset) restoreBackup(ctx context.Context, in restoreBackupInput) (any, error) {
	filename := strings.TrimSpace(in.Filename)
	if filename == "" {
		return nil, errors.New("filename 不能为空")
	}
	body := map[string]any{"filename": filename}
	if in.Password != "" {
		body["password"] = in.Password
	}
	result, err := t.call(ctx, http.MethodPost, "/system/restore", nil, body)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"message":  result["message"],
		"filename": filename,
		"hint":     "数据已恢复。网页端恢复后会自动重启面板，这里不会：请提醒管理员重启一次面板，让所有配置完整生效",
	}, nil
}
