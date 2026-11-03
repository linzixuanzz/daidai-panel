package handler

import (
	"log"
	"sort"
	"strings"

	"daidai-panel/database"
	"daidai-panel/middleware"
	"daidai-panel/model"
	"daidai-panel/pkg/response"
	"daidai-panel/service"

	"github.com/gin-gonic/gin"
)

type ConfigHandler struct{}

func NewConfigHandler() *ConfigHandler {
	return &ConfigHandler{}
}

func reloadRuntimeConfigKeys(keys ...string) {
	for _, key := range keys {
		switch key {
		case model.PanelTimezoneConfigKey:
			if err := service.ApplyRegisteredPanelTimezone(); err != nil {
				log.Printf("warn: apply panel timezone failed: %v", err)
			}
		case "trusted_proxy_cidrs":
			_ = middleware.ConfigureTrustedProxyCIDRs(model.GetRegisteredConfig(key))
		case "max_concurrent_tasks":
			// 并发数只在启动时读一次的话，用户改完必须重启面板才生效，
			// 而重启会中断所有正在运行的任务。这里让它立刻生效。
			service.ApplySchedulerWorkerCount()
		case "backup_schedule_enabled",
			"backup_schedule_frequency",
			"backup_schedule_time",
			"backup_schedule_weekday",
			"backup_schedule_monthday",
			"backup_schedule_name",
			"backup_schedule_password",
			"backup_schedule_selection":
			service.ReloadBackupScheduler()
		}
	}
}

func buildConfigResponseItem(cfg *model.SystemConfig, def *model.SystemConfigDefinition) gin.H {
	item := gin.H{
		"registered": false,
		"updated_at": nil,
	}

	if cfg != nil {
		item["value"] = cfg.Value
		item["description"] = cfg.Description
		item["updated_at"] = cfg.UpdatedAt
	} else {
		item["value"] = ""
		item["description"] = ""
	}

	if def != nil {
		item["registered"] = true
		item["default_value"] = def.DefaultValue
		item["value_type"] = def.ValueType
		item["group"] = def.Group
		item["description"] = def.Description
		// 以下几项是给「按 schema 动态渲染设置页」的客户端用的，纯新增字段：
		// label 当输入框标题（description 是长句说明，只能当 hint）；
		// group_label 是分组中文名；order 是注册顺序（本接口返回 map，本身没有顺序）；
		// secret 提示客户端用密码框渲染；min/max 供数字项做前端校验。
		// 老客户端读不到这些键也不受影响。
		item["label"] = def.Label
		item["group_label"] = def.GroupLabel
		item["order"] = def.Order
		// secret 目前只是渲染提示，服务端仍然明文回传 value。
		// 这是有意的：本接口是 JWTAuth + RequireAdmin，且 Web 的验证码/备份密码表单要靠回填的明文
		// 才能在保存整组配置时不把真实值覆盖掉。要改成服务端打码，必须同时定义
		// 「未修改」的写入哨兵值并同步改 Web/APP，不能只在这里单方面遮蔽。
		item["secret"] = def.Secret
		if def.Min != nil {
			item["min"] = *def.Min
		}
		if def.Max != nil {
			item["max"] = *def.Max
		}
		if cfg == nil || cfg.Value == "" {
			item["value"] = def.DefaultValue
		}
		if len(def.Options) > 0 {
			item["options"] = def.Options
		}
	}

	return item
}

func (h *ConfigHandler) List(c *gin.Context) {
	var configs []model.SystemConfig
	database.DB.Order("key ASC").Find(&configs)

	configMap := make(map[string]model.SystemConfig, len(configs))
	for _, cfg := range configs {
		configMap[cfg.Key] = cfg
	}

	data := make(map[string]interface{})
	for _, def := range model.SystemConfigDefinitions() {
		cfg, exists := configMap[def.Key]
		if exists {
			cfgCopy := cfg
			data[def.Key] = buildConfigResponseItem(&cfgCopy, &def)
			delete(configMap, def.Key)
			continue
		}
		defCopy := def
		data[def.Key] = buildConfigResponseItem(nil, &defCopy)
	}

	for key, cfg := range configMap {
		cfgCopy := cfg
		data[key] = buildConfigResponseItem(&cfgCopy, nil)
	}

	response.Success(c, gin.H{"data": data})
}

func (h *ConfigHandler) Get(c *gin.Context) {
	key := c.Param("key")

	var cfg model.SystemConfig
	cfgExists := database.DB.Where("`key` = ?", key).First(&cfg).Error == nil

	if def, exists := model.GetSystemConfigDefinition(key); exists {
		var cfgPtr *model.SystemConfig
		if cfgExists {
			cfgPtr = &cfg
		}
		item := buildConfigResponseItem(cfgPtr, &def)
		response.Success(c, gin.H{"data": gin.H{
			"key":    key,
			"value":  item["value"],
			"config": item,
		}})
		return
	}

	if !cfgExists {
		response.NotFound(c, "配置不存在")
		return
	}

	item := buildConfigResponseItem(&cfg, nil)
	response.Success(c, gin.H{"data": gin.H{
		"key":    key,
		"value":  item["value"],
		"config": item,
	}})
}

func (h *ConfigHandler) Set(c *gin.Context) {
	var req struct {
		Key         string `json:"key" binding:"required"`
		Value       string `json:"value"`
		Description string `json:"description"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "请求参数错误")
		return
	}

	before := snapshotConfigValues([]string{req.Key})
	if err := model.SetConfig(req.Key, req.Value); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	reloadRuntimeConfigKeys(req.Key)
	auditChangedConfigKeys(c, before)

	var cfg model.SystemConfig
	if err := database.DB.Where("`key` = ?", req.Key).First(&cfg).Error; err == nil && req.Description != "" {
		database.DB.Model(&cfg).Update("description", req.Description)
	}

	response.Success(c, gin.H{"message": "配置已更新"})
}

func (h *ConfigHandler) BatchSet(c *gin.Context) {
	var req struct {
		Configs map[string]string `json:"configs" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "请求参数错误")
		return
	}

	keys := make([]string, 0, len(req.Configs))
	for key := range req.Configs {
		keys = append(keys, key)
	}
	before := snapshotConfigValues(keys)
	for key, value := range req.Configs {
		if err := model.SetConfig(key, value); err != nil {
			// 中途失败时前面的键已经写进去了，照实把它们记进审计（只记键名）
			auditChangedConfigKeys(c, before)
			response.BadRequest(c, err.Error())
			return
		}
	}
	for key := range req.Configs {
		reloadRuntimeConfigKeys(key)
	}
	auditChangedConfigKeys(c, before)

	response.Success(c, gin.H{"message": "配置已更新"})
}

func (h *ConfigHandler) Delete(c *gin.Context) {
	key := c.Param("key")
	result := database.DB.Where("`key` = ?", key).Delete(&model.SystemConfig{})
	if result.Error == nil && result.RowsAffected > 0 {
		recordAudit(c, service.AuditConfigDelete, "删除配置项 "+key)
	}
	response.Success(c, gin.H{"message": "配置已删除"})
}

// snapshotConfigValues 记下这几个键改之前的「生效值」（库里没有或为空时取注册的默认值），
// 给 auditChangedConfigKeys 比对用。网页保存设置是整组提交，不比对的话每次都会把没动过的键也记成「改了」。
// 一次查完，不按键逐条查：数据库是单连接，整组保存时逐条查会多出几十条查询。
func snapshotConfigValues(keys []string) map[string]string {
	stored := make(map[string]string, len(keys))
	if len(keys) > 0 {
		var cfgs []model.SystemConfig
		database.DB.Where("`key` IN ?", keys).Find(&cfgs)
		for _, cfg := range cfgs {
			stored[cfg.Key] = cfg.Value
		}
	}
	values := make(map[string]string, len(keys))
	for _, key := range keys {
		value := stored[key]
		if value == "" {
			if def, ok := model.GetSystemConfigDefinition(key); ok {
				value = def.DefaultValue
			}
		}
		values[key] = value
	}
	return values
}

// auditChangedConfigKeys 把生效值确实变了的键写进审计。
// 🔴 只记键名、绝不记值：配置里有验证码密钥、备份密码、代理账号这类机密，审计日志是给人翻看的，不能变成泄露渠道。
func auditChangedConfigKeys(c *gin.Context, before map[string]string) {
	keys := make([]string, 0, len(before))
	for key := range before {
		keys = append(keys, key)
	}
	after := snapshotConfigValues(keys)
	changed := make([]string, 0, len(before))
	for key, old := range before {
		if after[key] != old {
			changed = append(changed, key)
		}
	}
	if len(changed) == 0 {
		return
	}
	sort.Strings(changed)
	recordAudit(c, service.AuditConfigUpdate, "修改配置项："+strings.Join(changed, ", "))
}

func (h *ConfigHandler) RegisterRoutes(r *gin.RouterGroup) {
	cfgs := r.Group("/configs", middleware.JWTAuth(), middleware.RequireAdmin())
	{
		cfgs.GET("", h.List)
		cfgs.GET("/:key", h.Get)
		cfgs.POST("", h.Set)
		cfgs.PUT("/batch", h.BatchSet)
		cfgs.DELETE("/:key", h.Delete)
	}
}
