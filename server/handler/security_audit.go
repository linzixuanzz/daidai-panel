package handler

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"daidai-panel/database"
	"daidai-panel/middleware"
	"daidai-panel/model"
	"daidai-panel/service"

	"github.com/gin-gonic/gin"
)

// auditActor 从请求上下文里认出「是谁做的」：返回用户 ID（查不到为 nil）和审计里显示的操作者名。
//
// 应用令牌的 username 是 app:<app_key>，审计里只写这串随机 key 没人认得出是哪个应用，
// 所以换成「应用:名称（app_key）」；应用已被删掉时退回原样。
// 用户令牌按用户名查一次用户 ID：只在敏感操作成功后调用，不在每请求路径上。
func auditActor(c *gin.Context) (*uint, string) {
	username := c.GetString("username")
	if username == "" || database.DB == nil {
		return nil, username
	}
	if strings.HasPrefix(username, "app:") {
		appKey := strings.TrimPrefix(username, "app:")
		var app model.OpenApp
		if err := database.DB.Select("name").Where("app_key = ?", appKey).First(&app).Error; err == nil && app.Name != "" {
			return nil, fmt.Sprintf("应用:%s（%s）", app.Name, appKey)
		}
		return nil, username
	}
	var user model.User
	if err := database.DB.Select("id").Where("username = ?", username).First(&user).Error; err != nil {
		return nil, username
	}
	id := user.ID
	return &id, username
}

// recordAudit 在敏感操作成功之后写一条安全审计（操作者、IP 都从请求上下文里取）。
// 写入失败由 service.RecordSecurityAudit 吞掉，不影响主流程；失败的操作不要调用它。
func recordAudit(c *gin.Context, action, detail string) {
	userID, username := auditActor(c)
	service.RecordSecurityAudit(userID, username, action, detail, middleware.ResolveClientIP(c))
}

// auditConsoleCommandMaxRunes：审计里只留命令的前 200 个字符。
// 命令可能很长、还可能带着口令之类的参数，全文进审计表既占地方又扩大泄露面；前 200 字足够看出干了什么。
const auditConsoleCommandMaxRunes = 200

func truncateAuditCommand(command string) string {
	if utf8.RuneCountInString(command) <= auditConsoleCommandMaxRunes {
		return command
	}
	return string([]rune(command)[:auditConsoleCommandMaxRunes]) + "…"
}
