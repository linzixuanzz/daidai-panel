package middleware

import (
	"net/http"
	"strings"
	"time"

	"daidai-panel/database"
	"daidai-panel/model"

	"github.com/gin-gonic/gin"
)

func isAppToken(username, role string) bool {
	return strings.HasPrefix(username, "app:") || strings.HasPrefix(role, "app:")
}

func appScopeAllowed(scopeList, required string) bool {
	required = strings.TrimSpace(required)
	if required == "" {
		return false
	}

	scopeList = strings.TrimSpace(scopeList)
	if scopeList == "" {
		return false
	}

	for _, item := range strings.Split(scopeList, ",") {
		item = strings.TrimSpace(item)
		if item == "*" || item == required {
			return true
		}
	}
	return false
}

func loadOpenAppByUsername(username string) (*model.OpenApp, error) {
	appKey := strings.TrimPrefix(username, "app:")
	var app model.OpenApp
	if err := database.DB.Where("app_key = ?", appKey).First(&app).Error; err != nil {
		return nil, err
	}
	return &app, nil
}

// AppTokenHasScope 判断当前请求是否具备某个「额外」scope，给 handler 内部按需校验用。
//
// 适用场景：路由组上的 OpenAPIAccess 只校验了一个 scope（例如 tasks），但某个可选开关会顺带动到
// 另一类资源（例如删除任务时一并删除脚本，动的是 scripts 的文件）。RequireRole 对应用令牌只看
// app_scope_authorized、不看角色，所以这类开关必须在 handler 里再调一次本函数，否则只有 tasks
// 权限的应用就凭空获得了删脚本的能力。仿写新开关时别漏了这一步。
//
// 口径：用户令牌恒为 true（用户的权限由 RequireRole 按角色控制）；应用令牌要求应用存在、已启用，
// 且 scopes 含 scope 或 *，与 OpenAPIAccess 用的是同一套判定。
func AppTokenHasScope(c *gin.Context, scope string) bool {
	username := c.GetString("username")
	if !isAppToken(username, c.GetString("role")) {
		return true
	}
	app, err := loadOpenAppByUsername(username)
	return err == nil && app.Enabled && appScopeAllowed(app.Scopes, scope)
}

func RequireUserToken() gin.HandlerFunc {
	return func(c *gin.Context) {
		username := c.GetString("username")
		role := c.GetString("role")
		if isAppToken(username, role) {
			c.JSON(http.StatusForbidden, gin.H{"error": "应用令牌无权访问此接口"})
			c.Abort()
			return
		}
		c.Next()
	}
}

func OpenAPIAccess(scope string) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Set("app_scope_authorized", false)

		username := c.GetString("username")
		role := c.GetString("role")
		if !isAppToken(username, role) {
			c.Next()
			return
		}

		app, err := loadOpenAppByUsername(username)
		if err != nil || !app.Enabled {
			c.JSON(http.StatusForbidden, gin.H{"error": "应用不存在或已被禁用"})
			c.Abort()
			return
		}

		if !appScopeAllowed(app.Scopes, scope) {
			c.JSON(http.StatusForbidden, gin.H{"error": "应用无权访问此资源"})
			c.Abort()
			return
		}

		c.Set("app_scope_authorized", true)

		if app.RateLimit > 0 {
			since := time.Now().Add(-time.Hour)
			var count int64
			database.DB.Model(&model.ApiCallLog{}).Where("app_id = ? AND created_at >= ?", app.ID, since).Count(&count)
			if count >= int64(app.RateLimit) {
				c.JSON(http.StatusTooManyRequests, gin.H{"error": "应用调用频率超限"})
				c.Abort()
				return
			}
		}

		start := time.Now()
		c.Next()

		endpoint := c.FullPath()
		if endpoint == "" {
			endpoint = c.Request.URL.Path
		}

		database.DB.Create(&model.ApiCallLog{
			AppID:    app.ID,
			AppName:  app.Name,
			Endpoint: endpoint,
			Method:   c.Request.Method,
			Status:   c.Writer.Status(),
			Duration: float64(time.Since(start).Milliseconds()),
			IP:       ResolveClientIP(c),
		})
	}
}
