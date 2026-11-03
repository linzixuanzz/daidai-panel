package handler

import (
	"fmt"
	"strconv"

	"daidai-panel/database"
	"daidai-panel/middleware"
	"daidai-panel/model"
	"daidai-panel/pkg/crypto"
	"daidai-panel/pkg/response"
	"daidai-panel/pkg/validator"
	"daidai-panel/service"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type UserHandler struct{}

func NewUserHandler() *UserHandler {
	return &UserHandler{}
}

func (h *UserHandler) List(c *gin.Context) {
	var users []model.User
	database.DB.Order("created_at ASC").Find(&users)

	userIDs := make([]uint, 0, len(users))
	for _, user := range users {
		userIDs = append(userIDs, user.ID)
	}

	twoFactorEnabled := make(map[uint]bool, len(userIDs))
	if len(userIDs) > 0 {
		var records []model.TwoFactorAuth
		database.DB.
			Select("user_id").
			Where("user_id IN ? AND enabled = ?", userIDs, true).
			Find(&records)
		for _, record := range records {
			twoFactorEnabled[record.UserID] = true
		}
	}

	data := make([]map[string]interface{}, len(users))
	for i, u := range users {
		item := u.ToDict()
		item["two_factor_enabled"] = twoFactorEnabled[u.ID]
		data[i] = item
	}

	response.Success(c, gin.H{"data": data})
}

func (h *UserHandler) Create(c *gin.Context) {
	var req struct {
		Username string `json:"username" binding:"required"`
		Password string `json:"password" binding:"required"`
		Role     string `json:"role"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "请求参数错误")
		return
	}

	if !validator.ValidateUsername(req.Username) {
		response.BadRequest(c, "用户名需 1-32 位，支持中文、字母、数字和下划线")
		return
	}
	if !validator.ValidatePassword(req.Password) {
		response.BadRequest(c, "密码长度需 6-128 位")
		return
	}
	if req.Role == "" {
		req.Role = "operator"
	}
	if req.Role != "admin" && req.Role != "operator" && req.Role != "viewer" {
		response.BadRequest(c, "角色无效，可选 admin/operator/viewer")
		return
	}

	var existing model.User
	if database.DB.Where("username = ?", req.Username).First(&existing).Error == nil {
		response.BadRequest(c, "用户名已存在")
		return
	}

	hashed, err := crypto.HashPassword(req.Password)
	if err != nil {
		response.InternalError(c, "密码加密失败")
		return
	}

	user := model.User{
		Username: req.Username,
		Password: hashed,
		Role:     req.Role,
		Enabled:  true,
	}

	if err := database.DB.Create(&user).Error; err != nil {
		response.InternalError(c, "创建用户失败")
		return
	}

	response.Created(c, gin.H{"message": "创建成功", "data": user.ToDict()})
}

func (h *UserHandler) Update(c *gin.Context) {
	userID, _ := strconv.ParseUint(c.Param("id"), 10, 32)

	var user model.User
	if err := database.DB.First(&user, userID).Error; err != nil {
		response.NotFound(c, "用户不存在")
		return
	}

	var req struct {
		Role    string `json:"role"`
		Enabled *bool  `json:"enabled"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "请求参数错误")
		return
	}

	updates := make(map[string]interface{})
	if req.Role != "" {
		if req.Role != "admin" && req.Role != "operator" && req.Role != "viewer" {
			response.BadRequest(c, "角色无效")
			return
		}
		updates["role"] = req.Role
	}
	if req.Enabled != nil {
		updates["enabled"] = *req.Enabled
	}

	if len(updates) > 0 {
		database.DB.Model(&user).Updates(updates)
	}

	database.DB.First(&user, userID)
	if len(updates) > 0 {
		service.RevokeAllUserSessions(user.ID)
	}
	response.Success(c, gin.H{"message": "更新成功", "data": user.ToDict()})
}

func (h *UserHandler) Delete(c *gin.Context) {
	userID, _ := strconv.ParseUint(c.Param("id"), 10, 32)

	currentUser := c.GetString("username")
	var user model.User
	if err := database.DB.First(&user, userID).Error; err != nil {
		response.NotFound(c, "用户不存在")
		return
	}

	if user.Username == currentUser {
		response.BadRequest(c, "不能删除自己")
		return
	}

	// 删号要撤销他的全部登录会话：登录令牌每请求只查黑名单、不查用户表，
	// 不撤销的话，被删的人手里的令牌还能一直调业务接口到自然过期。
	// 🔴 顺序必须是先删用户行、再撤销，与改密、改名、改角色 / 禁用、重置密码、ddp 两条命令同一个口径：
	// 登录落会话后会按 id 重读一次用户（handler/auth.go 的 Login），读不到就撤掉刚建的会话、拒绝登录；
	// 重读若早于这里删行，它的会话行已经在库里，下面的撤销一定遍历得到。
	// 反过来先撤销后删行，一次登录可以落在撤销的 Find 之后、删行之前：重读时用户还在，token 照发；
	// 用户行随后被删，这行会话成了孤儿，再没有哪条改用户状态的路径会撤它，
	// 被删的管理员能拿着这枚 access 调管理员接口到自然过期。
	// 删不掉就回 500、不撤销：用户还在、状态没变，按同一口径不需要撤销。
	// 通行密钥行跟用户行在同一个事务里删：SQLite 删掉最大 id 的用户后新建用户可能复用这个 id，
	// 留着孤儿行，新用户就会继承被删用户的通行密钥。
	if err := database.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Delete(&user).Error; err != nil {
			return err
		}
		return tx.Where("user_id = ?", user.ID).Delete(&model.Passkey{}).Error
	}); err != nil {
		response.InternalError(c, "删除用户失败")
		return
	}
	service.RevokeAllUserSessions(user.ID)
	response.Success(c, gin.H{"message": "删除成功"})
}

func (h *UserHandler) ResetPassword(c *gin.Context) {
	userID, _ := strconv.ParseUint(c.Param("id"), 10, 32)

	var user model.User
	if err := database.DB.First(&user, userID).Error; err != nil {
		response.NotFound(c, "用户不存在")
		return
	}

	var req struct {
		Password string `json:"password" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "请求参数错误")
		return
	}

	if !validator.ValidatePassword(req.Password) {
		response.BadRequest(c, "密码长度需 6-128 位")
		return
	}

	hashed, err := crypto.HashPassword(req.Password)
	if err != nil {
		response.InternalError(c, "密码加密失败")
		return
	}

	// 新密码与清掉该用户的全部通行密钥放在同一事务里；落库之后再撤销会话（先落库、再撤销）
	removedPasskeys, err := service.SetPasswordAndClearPasskeys(user.ID, hashed)
	if err != nil {
		response.InternalError(c, "密码重置失败")
		return
	}
	service.RevokeAllUserSessions(user.ID)
	// 确实清掉了通行密钥才多说一句；没有通行密钥时文案与原来逐字相同
	message := "密码重置成功"
	if removedPasskeys > 0 {
		message = fmt.Sprintf("密码重置成功，该用户已添加的 %d 个通行密钥也已清除", removedPasskeys)
	}
	response.Success(c, gin.H{"message": message})
}

func (h *UserHandler) RegisterRoutes(r *gin.RouterGroup) {
	users := r.Group("/users", middleware.JWTAuth(), middleware.RequireAdmin())
	{
		users.GET("", h.List)
		users.POST("", h.Create)
		users.PUT("/:id", h.Update)
		users.DELETE("/:id", h.Delete)
		users.PUT("/:id/reset-password", h.ResetPassword)
	}
}
