package handler

import (
	"fmt"
	"strings"
	"time"

	"daidai-panel/middleware"
	"daidai-panel/pkg/response"
	"daidai-panel/service"

	"github.com/gin-gonic/gin"
)

type AuthHandler struct {
	authService  *service.AuthService
	loginLimiter gin.HandlerFunc
}

func NewAuthHandler() *AuthHandler {
	return &AuthHandler{
		authService:  service.NewAuthService(),
		loginLimiter: middleware.RateLimit(5, time.Minute),
	}
}

func (h *AuthHandler) CheckInit(c *gin.Context) {
	response.Success(c, gin.H{"need_init": h.authService.NeedInit()})
}

func (h *AuthHandler) Init(c *gin.Context) {
	var req struct {
		Username string `json:"username" binding:"required"`
		Password string `json:"password" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "请求参数错误")
		return
	}

	user, err := h.authService.InitAdmin(req.Username, req.Password)
	if err != nil {
		switch err {
		case service.ErrInvalidUsername:
			response.BadRequest(c, "用户名需 3-32 位，仅支持字母数字下划线")
		case service.ErrPasswordTooShort:
			response.BadRequest(c, "密码长度需 6-128 位")
		default:
			response.BadRequest(c, err.Error())
		}
		return
	}

	response.Success(c, gin.H{
		"message": "初始化成功",
		"user":    user.ToDict(),
	})
}

func (h *AuthHandler) Login(c *gin.Context) {
	var req struct {
		Username string `json:"username" binding:"required"`
		Password string `json:"password" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "请求参数错误")
		return
	}

	ip := c.ClientIP()
	ua := c.GetHeader("User-Agent")

	locked, remaining := service.CheckLoginLock(ip, req.Username)
	if locked {
		service.RecordLoginLog(0, req.Username, ip, ua, 1, "账号已锁定")
		remainSec := int(remaining.Seconds())
		c.JSON(429, gin.H{
			"error":             fmt.Sprintf("账号已锁定，请 %.0f 分钟后重试", remaining.Minutes()),
			"locked":            true,
			"remaining_seconds": remainSec,
		})
		return
	}

	user, accessToken, refreshToken, tokenInfo, err := h.authService.Login(req.Username, req.Password)
	if err != nil {
		service.RecordFailedLogin(ip, req.Username)

		switch err {
		case service.ErrUserNotFound, service.ErrInvalidPassword:
			service.RecordLoginLog(0, req.Username, ip, ua, 1, "登录失败")
			response.Unauthorized(c, "用户名或密码错误")
		case service.ErrUserDisabled:
			service.RecordLoginLog(0, req.Username, ip, ua, 1, "登录失败")
			response.Forbidden(c, "账号已被禁用")
		default:
			service.RecordLoginLog(0, req.Username, ip, ua, 1, "登录失败")
			response.InternalError(c, "登录失败")
		}
		return
	}

	service.ClearLoginAttempts(ip, req.Username)
	service.RecordLoginLog(user.ID, user.Username, ip, ua, 0, "登录成功")
	service.CreateSession(user.ID, user.Username, tokenInfo.JTI, ip, ua, tokenInfo.ExpiresAt)

	response.Success(c, gin.H{
		"message":       "登录成功",
		"access_token":  accessToken,
		"refresh_token": refreshToken,
		"user":          user.ToDict(),
	})
}

func (h *AuthHandler) Logout(c *gin.Context) {
	jti, _ := c.Get("jti")
	h.authService.Logout(jti.(string), nil)
	service.RevokeSession(jti.(string))
	response.Success(c, gin.H{"message": "已退出登录"})
}

func (h *AuthHandler) Refresh(c *gin.Context) {
	authHeader := c.GetHeader("Authorization")
	tokenStr := strings.TrimPrefix(authHeader, "Bearer ")

	newToken, err := h.authService.RefreshToken(tokenStr)
	if err != nil {
		response.Unauthorized(c, "令牌无效或已过期")
		return
	}

	response.Success(c, gin.H{"access_token": newToken})
}

func (h *AuthHandler) GetUser(c *gin.Context) {
	username, _ := c.Get("username")
	user, err := h.authService.GetUser(username.(string))
	if err != nil {
		response.NotFound(c, "用户不存在")
		return
	}
	response.Success(c, gin.H{"user": user.ToDict()})
}

func (h *AuthHandler) ChangePassword(c *gin.Context) {
	var req struct {
		OldPassword string `json:"old_password" binding:"required"`
		NewPassword string `json:"new_password" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "请求参数错误")
		return
	}

	username, _ := c.Get("username")
	if err := h.authService.ChangePassword(username.(string), req.OldPassword, req.NewPassword); err != nil {
		switch err {
		case service.ErrInvalidPassword:
			response.BadRequest(c, "当前密码不正确")
		case service.ErrPasswordTooShort:
			response.BadRequest(c, "新密码长度需 6-128 位")
		default:
			response.InternalError(c, "修改密码失败")
		}
		return
	}

	user, _ := h.authService.GetUser(username.(string))
	if user != nil {
		service.RevokeAllUserSessions(user.ID)
	}

	response.Success(c, gin.H{"message": "密码修改成功，请重新登录"})
}

func (h *AuthHandler) CaptchaConfig(c *gin.Context) {
	response.Success(c, gin.H{
		"enabled":    false,
		"captcha_id": "",
	})
}

func (h *AuthHandler) RegisterRoutes(r *gin.RouterGroup) {
	auth := r.Group("/auth")
	{
		auth.GET("/check-init", h.CheckInit)
		auth.POST("/init", h.Init)
		auth.POST("/login", h.loginLimiter, h.Login)
		auth.POST("/logout", middleware.JWTAuth(), h.Logout)
		auth.POST("/refresh", h.Refresh)
		auth.GET("/user", middleware.JWTAuth(), h.GetUser)
		auth.PUT("/password", middleware.JWTAuth(), h.ChangePassword)
		auth.GET("/captcha-config", h.CaptchaConfig)
	}
}
