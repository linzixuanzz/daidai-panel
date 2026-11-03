package handler

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"daidai-panel/database"
	"daidai-panel/middleware"
	"daidai-panel/model"
	"daidai-panel/pkg/netutil"
	"daidai-panel/pkg/response"
	"daidai-panel/service"

	"github.com/gin-gonic/gin"
)

type SecurityHandler struct{}

func NewSecurityHandler() *SecurityHandler {
	return &SecurityHandler{}
}

func (h *SecurityHandler) LoginLogs(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	username := c.Query("username")

	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}

	query := database.DB.Model(&model.LoginLog{})
	if username != "" {
		query = query.Where("username = ?", username)
	}

	var total int64
	query.Count(&total)

	var logs []model.LoginLog
	query.Order("created_at DESC").
		Offset((page - 1) * pageSize).Limit(pageSize).Find(&logs)

	data := make([]map[string]interface{}, len(logs))
	for i, l := range logs {
		item := l.ToDict()
		clientType := service.DetectSessionClientType("", "", l.UserAgent)
		item["client_type"] = clientType
		item["client_type_label"] = service.SessionClientLabel(clientType)
		item["client_name"] = service.ResolveStoredSessionClientName(clientType, l.ClientName, l.UserAgent)
		data[i] = item
	}

	response.Paginated(c, data, total, page, pageSize)
}

func (h *SecurityHandler) Sessions(c *gin.Context) {
	service.CleanExpiredSessions()

	// 列出「access 或 refresh 仍有效」的会话：登录超过 access 有效期、但还能续期的会话也得看得见，才能单独撤销它
	now := time.Now()
	var sessions []model.UserSession
	database.DB.Where("expires_at > ? OR (refresh_expires_at IS NOT NULL AND refresh_expires_at > ?)", now, now).
		Order("created_at DESC").Find(&sessions)

	currentSessionID := c.GetString("sid")
	data := make([]map[string]interface{}, len(sessions))
	for i, s := range sessions {
		item := s.ToDict()
		clientType := service.DetectSessionClientType(s.ClientType, "", s.UserAgent)
		item["client_type"] = clientType
		item["client_type_label"] = service.SessionClientLabel(clientType)
		item["client_name"] = service.ResolveStoredSessionClientName(clientType, s.ClientName, s.UserAgent)
		// current：这一行是不是发起请求的这个登录会话。按会话号认，续期过的令牌也认得出来
		item["current"] = s.JTI == currentSessionID
		data[i] = item
	}

	response.Success(c, gin.H{"data": data})
}

func (h *SecurityHandler) RevokeSession(c *gin.Context) {
	sessionID, _ := strconv.ParseUint(c.Param("id"), 10, 32)

	var session model.UserSession
	if err := database.DB.First(&session, sessionID).Error; err != nil {
		response.NotFound(c, "会话不存在")
		return
	}

	service.BlockSessionTokens(&session)
	database.DB.Delete(&session)
	response.Success(c, gin.H{"message": "会话已撤销"})
}

func (h *SecurityHandler) RevokeAllSessions(c *gin.Context) {
	userID, _ := strconv.ParseUint(c.Param("user_id"), 10, 32)

	count := service.RevokeAllUserSessions(uint(userID))
	response.Success(c, gin.H{"message": fmt.Sprintf("已撤销 %d 个会话", count)})
}

func (h *SecurityHandler) RevokeOtherSessions(c *gin.Context) {
	username, _ := c.Get("username")

	var user model.User
	if err := database.DB.Where("username = ?", username).First(&user).Error; err != nil {
		response.NotFound(c, "用户不存在")
		return
	}

	// 按会话号认「当前会话」：用续期得来的令牌发起时，它的 jti 不是会话行里记的那个，
	// 按 jti 排除会把自己的会话一起撤掉，当场踢下线
	count := service.RevokeOtherUserSessions(user.ID, c.GetString("sid"))
	response.Success(c, gin.H{"message": fmt.Sprintf("已撤销 %d 个其他会话", count)})
}

func (h *SecurityHandler) IPWhitelist(c *gin.Context) {
	var whitelist []model.IPWhitelist
	database.DB.Order("created_at DESC").Find(&whitelist)

	data := make([]map[string]interface{}, len(whitelist))
	for i, w := range whitelist {
		data[i] = w.ToDict()
	}

	response.Success(c, gin.H{"data": data})
}

func (h *SecurityHandler) AddIPWhitelist(c *gin.Context) {
	var req struct {
		IP      string `json:"ip" binding:"required"`
		Remarks string `json:"remarks"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "请求参数错误")
		return
	}

	normalizedIP, err := netutil.NormalizeIPWhitelistEntry(req.IP)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	entry := model.IPWhitelist{
		IP:      normalizedIP,
		Remarks: strings.TrimSpace(req.Remarks),
	}

	if err := database.DB.Create(&entry).Error; err != nil {
		response.BadRequest(c, "该 IP 或网段已在白名单中")
		return
	}
	recordAudit(c, service.AuditIPWhitelistAdd, "添加白名单 "+entry.IP)

	response.Created(c, gin.H{"message": "添加成功", "data": entry.ToDict()})
}

func (h *SecurityHandler) RemoveIPWhitelist(c *gin.Context) {
	entryID, _ := strconv.ParseUint(c.Param("id"), 10, 32)
	// 先读出这一条，审计里要写删的是哪个 IP；读不到时删除照旧执行（响应保持原样），只是不记审计
	var entry model.IPWhitelist
	found := database.DB.First(&entry, entryID).Error == nil
	result := database.DB.Where("id = ?", entryID).Delete(&model.IPWhitelist{})
	if found && result.Error == nil && result.RowsAffected > 0 {
		recordAudit(c, service.AuditIPWhitelistRemove, "删除白名单 "+entry.IP)
	}
	response.Success(c, gin.H{"message": "删除成功"})
}

func (h *SecurityHandler) AuditLogs(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	action := c.Query("action")

	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}

	query := database.DB.Model(&model.SecurityAudit{})
	if action != "" {
		query = query.Where("action = ?", action)
	}

	var total int64
	query.Count(&total)

	var audits []model.SecurityAudit
	query.Order("created_at DESC").
		Offset((page - 1) * pageSize).Limit(pageSize).Find(&audits)

	data := make([]map[string]interface{}, len(audits))
	for i, a := range audits {
		data[i] = a.ToDict()
	}

	response.Paginated(c, data, total, page, pageSize)
}

func (h *SecurityHandler) LoginStats(c *gin.Context) {
	days, _ := strconv.Atoi(c.DefaultQuery("days", "7"))
	if days < 1 || days > 90 {
		days = 7
	}

	stats := service.GetLoginStats(days)
	response.Success(c, gin.H{"data": stats})
}

func (h *SecurityHandler) ClearLoginLogs(c *gin.Context) {
	result := database.DB.Where("1 = 1").Delete(&model.LoginLog{})
	if result.Error == nil {
		recordAudit(c, service.AuditLoginLogsClear, fmt.Sprintf("清空登录日志 %d 条", result.RowsAffected))
	}
	response.Success(c, gin.H{
		"message": fmt.Sprintf("已清除 %d 条登录日志", result.RowsAffected),
	})
}

func (h *SecurityHandler) Setup2FA(c *gin.Context) {
	username := c.GetString("username")
	var user model.User
	if err := database.DB.Where("username = ?", username).First(&user).Error; err != nil {
		response.NotFound(c, "用户不存在")
		return
	}

	secret, uri, err := service.SetupTwoFactor(user.ID)
	if err != nil {
		response.InternalError(c, "设置 2FA 失败")
		return
	}

	response.Success(c, gin.H{
		"data": gin.H{
			"secret": secret,
			"uri":    uri,
		},
	})
}

func (h *SecurityHandler) Verify2FA(c *gin.Context) {
	username := c.GetString("username")
	var user model.User
	if err := database.DB.Where("username = ?", username).First(&user).Error; err != nil {
		response.NotFound(c, "用户不存在")
		return
	}

	var req struct {
		Code string `json:"code" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "请求参数错误")
		return
	}

	if err := service.VerifyAndEnableTwoFactor(user.ID, req.Code); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	recordAudit(c, service.AuditTwoFactorEnable, "开启了两步验证")

	response.Success(c, gin.H{"message": "2FA 已启用"})
}

func (h *SecurityHandler) Disable2FA(c *gin.Context) {
	username := c.GetString("username")
	var user model.User
	if err := database.DB.Where("username = ?", username).First(&user).Error; err != nil {
		response.NotFound(c, "用户不存在")
		return
	}

	// Before turning 2FA off we must prove the user still controls the
	// authenticator — otherwise a hijacked session alone would be enough to
	// strip 2FA from the account.
	if !service.IsTwoFactorEnabled(user.ID) {
		response.Success(c, gin.H{"message": "2FA 未启用"})
		return
	}

	var req struct {
		Code string `json:"code"`
	}
	_ = c.ShouldBindJSON(&req)

	code := strings.TrimSpace(req.Code)
	if code == "" {
		response.BadRequest(c, "请输入当前的动态验证码")
		return
	}
	if !service.ValidateUserTOTP(user.ID, code) {
		response.BadRequest(c, "动态验证码错误")
		return
	}

	service.DisableTwoFactor(user.ID)
	recordAudit(c, service.AuditTwoFactorDisable, "关闭了两步验证")
	response.Success(c, gin.H{"message": "2FA 已禁用"})
}

func (h *SecurityHandler) Get2FAStatus(c *gin.Context) {
	username := c.GetString("username")
	var user model.User
	if err := database.DB.Where("username = ?", username).First(&user).Error; err != nil {
		response.NotFound(c, "用户不存在")
		return
	}

	enabled := service.IsTwoFactorEnabled(user.ID)
	response.Success(c, gin.H{"data": gin.H{"enabled": enabled}})
}

func (h *SecurityHandler) RegisterRoutes(r *gin.RouterGroup) {
	security := r.Group("/security", middleware.JWTAuth())
	{
		security.GET("/login-logs", middleware.RequireAdmin(), h.LoginLogs)
		security.DELETE("/login-logs", middleware.RequireAdmin(), h.ClearLoginLogs)
		security.GET("/sessions", middleware.RequireAdmin(), h.Sessions)
		security.DELETE("/sessions/others", middleware.RequireAdmin(), h.RevokeOtherSessions)
		security.DELETE("/sessions/:id", middleware.RequireAdmin(), h.RevokeSession)
		security.DELETE("/sessions/user/:user_id", middleware.RequireAdmin(), h.RevokeAllSessions)
		security.GET("/ip-whitelist", middleware.RequireAdmin(), h.IPWhitelist)
		security.POST("/ip-whitelist", middleware.RequireAdmin(), h.AddIPWhitelist)
		security.DELETE("/ip-whitelist/:id", middleware.RequireAdmin(), h.RemoveIPWhitelist)
		security.GET("/audit-logs", middleware.RequireAdmin(), h.AuditLogs)
		security.GET("/login-stats", middleware.RequireAdmin(), h.LoginStats)

		security.POST("/2fa/setup", h.Setup2FA)
		security.POST("/2fa/verify", h.Verify2FA)
		security.DELETE("/2fa", h.Disable2FA)
		security.GET("/2fa/status", h.Get2FAStatus)

		// 通行密钥（issue #175）：任何角色都能管自己的，只能看 / 改 / 删自己的行
		security.GET("/passkeys", h.ListPasskeys)
		security.POST("/passkeys/register/begin", h.PasskeyRegisterBegin)
		security.POST("/passkeys/register/finish", h.PasskeyRegisterFinish)
		security.PUT("/passkeys/:id", h.RenamePasskey)
		security.DELETE("/passkeys/:id", h.DeletePasskey)
	}
}
