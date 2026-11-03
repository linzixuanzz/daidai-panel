package handler

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"daidai-panel/database"
	"daidai-panel/middleware"
	"daidai-panel/model"
	"daidai-panel/pkg/crypto"
	"daidai-panel/pkg/response"
	"daidai-panel/service"

	"github.com/gin-gonic/gin"
)

// 通行密钥（Passkey / WebAuthn，issue #175）的接口。只做网页端，APP 照常用密码 + 两步验证码。
// 通行密钥是额外的登录方式：密码登录、两步验证的任何行为都不因它改变。

// 通行密钥接口的稳定机器可读标识，与登录接口的 code 同一个用途
const (
	PasskeyCodeUnsupportedOrigin = "passkey_unsupported_origin"
	PasskeyCodeSessionExpired    = "passkey_session_expired"
	PasskeyCodeInvalid           = "invalid_passkey"
)

// passkeyLoginMethod 是登录日志「登录方式」一列的取值（密码登录走列默认值「密码登录」）
const passkeyLoginMethod = "通行密钥"

const passkeyLoginFailedMessage = "通行密钥登录失败"

// passkeyBeginError 把 begin 阶段（登录 / 注册）的错误翻成响应
func passkeyBeginError(c *gin.Context, err error) {
	switch err {
	case service.ErrPasskeyUnsupportedOrigin:
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error(), "code": PasskeyCodeUnsupportedOrigin})
	case service.ErrPasskeyTooManySessions:
		response.TooManyRequests(c, err.Error())
	case service.ErrPasskeyLimitReached:
		response.BadRequest(c, err.Error())
	default:
		response.InternalError(c, "发起通行密钥验证失败")
	}
}

// PasskeyLoginBegin 免登录：发起一次通行密钥登录，返回 session_id 与 navigator.credentials.get 要的 publicKey 选项。
func (h *AuthHandler) PasskeyLoginBegin(c *gin.Context) {
	// 客户端 IP 只用来给挑战会话按 IP 限额：刷接口的人只能挤掉自己的会话，挤不掉别人的
	sessionID, options, err := service.BeginPasskeyLogin(c.GetHeader("Origin"), middleware.ResolveClientIP(c))
	if err != nil {
		passkeyBeginError(c, err)
		return
	}
	response.Success(c, gin.H{"data": gin.H{"session_id": sessionID, "options": options}})
}

// PasskeyLoginFinish 免登录：校验通行密钥并登录。
// 与密码登录一样先判 IP 白名单；不走验证码、不看登录锁定（锁定是防密码爆破的，别人试密码把账号锁住时本人还能用通行密钥进来）。
// 成功后与密码登录走同一段收尾（commitLoginSession + respondLoginSuccess），响应体逐字段相同。
func (h *AuthHandler) PasskeyLoginFinish(c *gin.Context) {
	var req struct {
		SessionID  string          `json:"session_id"`
		Credential json.RawMessage `json:"credential"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "请求参数错误")
		return
	}

	ip := middleware.ResolveClientIP(c)
	ua := c.GetHeader("User-Agent")
	clientInfo := service.DetectSessionClientInfo(
		c.GetHeader("X-Client-Type"),
		c.GetHeader("X-Client-App"),
		c.GetHeader("X-Client-Platform"),
		c.GetHeader("X-Device-Model"),
		c.GetHeader("X-Device-Name"),
		c.GetHeader("X-OS-Version"),
		ua,
	)
	clientType := clientInfo.Type
	clientName := service.SessionClientDisplayName(clientInfo)

	// 白名单判断与文案同密码登录；此时还不知道是谁，日志用户名留空
	if !service.IsIPWhitelisted(ip) {
		service.RecordLoginLogWithMethod(0, "", ip, clientName, ua, 1, "IP 不在白名单", passkeyLoginMethod)
		response.Forbidden(c, "当前 IP 不在登录白名单中")
		return
	}

	invalid := func(username string) {
		service.RecordLoginLogWithMethod(0, username, ip, clientName, ua, 1, passkeyLoginFailedMessage, passkeyLoginMethod)
		c.JSON(http.StatusUnauthorized, gin.H{"error": service.ErrPasskeyInvalid.Error(), "code": PasskeyCodeInvalid})
	}
	disabled := func(username string) {
		service.RecordLoginLogWithMethod(0, username, ip, clientName, ua, 1, passkeyLoginFailedMessage, passkeyLoginMethod)
		response.Forbidden(c, "账号已被禁用")
	}

	user, username, err := service.FinishPasskeyLogin(req.SessionID, req.Credential)
	switch err {
	case nil:
	case service.ErrPasskeySessionExpired:
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error(), "code": PasskeyCodeSessionExpired})
		return
	case service.ErrUserDisabled:
		disabled(username)
		return
	case service.ErrPasskeyInvalid:
		invalid(username)
		return
	default:
		service.RecordLoginLogWithMethod(0, username, ip, clientName, ua, 1, passkeyLoginFailedMessage, passkeyLoginMethod)
		response.InternalError(c, "登录失败")
		return
	}

	user, accessToken, refreshToken, accessInfo, refreshInfo, err := h.authService.IssueLoginTokens(user)
	if err != nil {
		service.RecordLoginLogWithMethod(0, username, ip, clientName, ua, 1, passkeyLoginFailedMessage, passkeyLoginMethod)
		response.InternalError(c, "登录失败")
		return
	}
	switch err := commitLoginSession(user, accessInfo, refreshInfo, clientType, clientName, ip, ua); err {
	case nil:
	case errLoginSessionNotCreated:
		response.InternalError(c, "登录失败")
		return
	case service.ErrUserDisabled:
		disabled(username)
		return
	default:
		// 校验完到会话落库之间账号被删、改名、改密或改角色：这次登录作废
		invalid(username)
		return
	}

	respondLoginSuccess(c, user, accessToken, refreshToken, user.Username, ip, clientName, ua, true)
}

// currentPasskeyUser 读当前登录用户；读不到回 404 并返回 nil
func currentPasskeyUser(c *gin.Context) *model.User {
	var user model.User
	if err := database.DB.Where("username = ?", c.GetString("username")).First(&user).Error; err != nil {
		response.NotFound(c, "用户不存在")
		return nil
	}
	return &user
}

func (h *SecurityHandler) ListPasskeys(c *gin.Context) {
	user := currentPasskeyUser(c)
	if user == nil {
		return
	}
	rows, err := service.ListPasskeys(user.ID)
	if err != nil {
		response.InternalError(c, "读取通行密钥失败")
		return
	}
	data := make([]map[string]interface{}, len(rows))
	for i := range rows {
		data[i] = rows[i].ToDict()
	}
	response.Success(c, gin.H{"data": data})
}

// PasskeyRegisterBegin 添加通行密钥前必须校验当前密码：防会话被劫持后偷偷加一个通行密钥长期驻留。
// 开了两步验证的账号还要再带上两步验证码：通行密钥能直接登录、跳过两步验证，
// 只凭密码就能加一枚的话，密码泄露就等于两步验证失效。顺序是先密码、再验证码。
func (h *SecurityHandler) PasskeyRegisterBegin(c *gin.Context) {
	var req struct {
		Password string `json:"password"`
		TOTPCode string `json:"totp_code"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "请求参数错误")
		return
	}

	user := currentPasskeyUser(c)
	if user == nil {
		return
	}
	if req.Password == "" || !crypto.CheckPassword(req.Password, user.Password) {
		response.BadRequest(c, "密码错误")
		return
	}
	// 没开两步验证的账号不看 totp_code，行为与原来一致；code 与登录接口的同名 code 一个含义
	if service.IsTwoFactorEnabled(user.ID) {
		totpCode := strings.TrimSpace(req.TOTPCode)
		if totpCode == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "请输入两步验证码", "code": LoginCodeTwoFactorRequired})
			return
		}
		if !service.ValidateUserTOTP(user.ID, totpCode) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "两步验证码错误", "code": LoginCodeInvalidTOTP})
			return
		}
	}

	sessionID, options, err := service.BeginPasskeyRegistration(user, c.GetHeader("Origin"))
	if err != nil {
		passkeyBeginError(c, err)
		return
	}
	response.Success(c, gin.H{"data": gin.H{"session_id": sessionID, "options": options}})
}

func (h *SecurityHandler) PasskeyRegisterFinish(c *gin.Context) {
	var req struct {
		SessionID  string          `json:"session_id"`
		Name       string          `json:"name"`
		Credential json.RawMessage `json:"credential"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "请求参数错误")
		return
	}

	user := currentPasskeyUser(c)
	if user == nil {
		return
	}

	row, err := service.FinishPasskeyRegistration(user.ID, req.SessionID, req.Name, req.Credential)
	switch err {
	case nil:
	case service.ErrPasskeySessionExpired:
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error(), "code": PasskeyCodeSessionExpired})
		return
	case service.ErrUserNotFound:
		response.NotFound(c, "用户不存在")
		return
	case service.ErrPasskeyRegisterFailed, service.ErrPasskeyDuplicate, service.ErrPasskeyLimitReached:
		response.BadRequest(c, err.Error())
		return
	default:
		response.InternalError(c, "添加通行密钥失败")
		return
	}
	recordAudit(c, service.AuditPasskeyAdd, "添加通行密钥「"+row.Name+"」")
	response.Success(c, gin.H{"message": "通行密钥已添加", "data": row.ToDict()})
}

func (h *SecurityHandler) RenamePasskey(c *gin.Context) {
	var req struct {
		Name string `json:"name"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "请求参数错误")
		return
	}
	user := currentPasskeyUser(c)
	if user == nil {
		return
	}
	passkeyID, _ := strconv.ParseUint(c.Param("id"), 10, 32)
	switch err := service.RenamePasskey(user.ID, uint(passkeyID), req.Name); err {
	case nil:
		response.Success(c, gin.H{"message": "已重命名"})
	case service.ErrPasskeyNotFound:
		// 不是自己的与不存在同样回 404，不透露别人的通行密钥是否存在
		response.NotFound(c, err.Error())
	default:
		response.InternalError(c, "重命名失败")
	}
}

func (h *SecurityHandler) DeletePasskey(c *gin.Context) {
	user := currentPasskeyUser(c)
	if user == nil {
		return
	}
	passkeyID, _ := strconv.ParseUint(c.Param("id"), 10, 32)
	// 删之前读出名字给审计用；读不到（不存在 / 不是自己的）时下面的删除会回 404，也不会记审计
	var target model.Passkey
	database.DB.Select("name").Where("id = ? AND user_id = ?", passkeyID, user.ID).First(&target)
	switch err := service.DeletePasskey(user.ID, uint(passkeyID)); err {
	case nil:
		recordAudit(c, service.AuditPasskeyDelete, "删除通行密钥「"+target.Name+"」")
		response.Success(c, gin.H{"message": "已删除"})
	case service.ErrPasskeyNotFound:
		response.NotFound(c, err.Error())
	default:
		response.InternalError(c, "删除失败")
	}
}
