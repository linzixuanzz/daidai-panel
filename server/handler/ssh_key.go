package handler

import (
	"strconv"

	"daidai-panel/database"
	"daidai-panel/middleware"
	"daidai-panel/model"
	"daidai-panel/pkg/response"
	"daidai-panel/service"

	"github.com/gin-gonic/gin"
)

type SSHKeyHandler struct{}

func NewSSHKeyHandler() *SSHKeyHandler {
	return &SSHKeyHandler{}
}

func (h *SSHKeyHandler) List(c *gin.Context) {
	var keys []model.SSHKey
	database.DB.Order("created_at DESC").Find(&keys)

	data := make([]map[string]interface{}, len(keys))
	for i, k := range keys {
		data[i] = k.ToDict()
	}

	response.Success(c, gin.H{"data": data})
}

func (h *SSHKeyHandler) Create(c *gin.Context) {
	var req struct {
		Name       string `json:"name" binding:"required"`
		PrivateKey string `json:"private_key" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "请求参数错误")
		return
	}

	key := model.SSHKey{
		Name:       req.Name,
		PrivateKey: req.PrivateKey,
	}

	// ssh_keys.name 是唯一索引，连点创建按钮的第二发会撞在这里，翻译成友好 400。
	if err := database.DB.Create(&key).Error; err != nil {
		response.BadRequest(c, "同名 SSH 密钥已存在")
		return
	}
	recordAudit(c, service.AuditSSHKeyCreate, "添加 SSH 密钥 "+key.Name)

	response.Created(c, gin.H{"message": "创建成功", "data": key.ToDict()})
}

func (h *SSHKeyHandler) Update(c *gin.Context) {
	keyID, _ := strconv.ParseUint(c.Param("id"), 10, 32)

	var key model.SSHKey
	if err := database.DB.First(&key, keyID).Error; err != nil {
		response.NotFound(c, "SSH 密钥不存在")
		return
	}

	var req struct {
		Name       string `json:"name"`
		PrivateKey string `json:"private_key"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "请求参数错误")
		return
	}

	updates := make(map[string]interface{})
	if req.Name != "" {
		updates["name"] = req.Name
	}
	if req.PrivateKey != "" {
		updates["private_key"] = req.PrivateKey
	}

	if len(updates) > 0 {
		// 改名撞上别的密钥会在这里报错；不接 .Error 就会出现「提示更新成功、刷新又变回去」。
		if err := database.DB.Model(&key).Updates(updates).Error; err != nil {
			response.BadRequest(c, "同名 SSH 密钥已存在")
			return
		}
	}

	database.DB.First(&key, keyID)
	response.Success(c, gin.H{"message": "更新成功", "data": key.ToDict()})
}

func (h *SSHKeyHandler) Delete(c *gin.Context) {
	keyID, _ := strconv.ParseUint(c.Param("id"), 10, 32)
	// 先读出名字给审计用；读不到时删除照旧执行（响应保持原样），只是不记审计
	var key model.SSHKey
	found := database.DB.Select("id", "name").First(&key, keyID).Error == nil
	result := database.DB.Where("id = ?", keyID).Delete(&model.SSHKey{})
	if found && result.Error == nil && result.RowsAffected > 0 {
		recordAudit(c, service.AuditSSHKeyDelete, "删除 SSH 密钥 "+key.Name)
	}
	response.Success(c, gin.H{"message": "删除成功"})
}

func (h *SSHKeyHandler) Detail(c *gin.Context) {
	keyID, _ := strconv.ParseUint(c.Param("id"), 10, 32)

	var key model.SSHKey
	if err := database.DB.First(&key, keyID).Error; err != nil {
		response.NotFound(c, "SSH 密钥不存在")
		return
	}

	response.Success(c, gin.H{"data": key.ToDictWithKey()})
}

func (h *SSHKeyHandler) RegisterRoutes(r *gin.RouterGroup) {
	keys := r.Group("/ssh-keys", middleware.JWTAuth(), middleware.RequireUserToken(), middleware.RequireAdmin())
	{
		keys.GET("", h.List)
		keys.POST("", h.Create)
		keys.PUT("/:id", h.Update)
		keys.DELETE("/:id", h.Delete)
		keys.GET("/:id", h.Detail)
	}
}
