package handler

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"daidai-panel/database"
	"daidai-panel/middleware"
	"daidai-panel/model"
	"daidai-panel/pkg/response"

	"github.com/gin-gonic/gin"
)

var envNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

type EnvHandler struct{}

func NewEnvHandler() *EnvHandler {
	return &EnvHandler{}
}

func (h *EnvHandler) List(c *gin.Context) {
	keyword := c.Query("keyword")
	group := c.Query("group")
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))

	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}

	query := database.DB.Model(&model.EnvVar{})

	if keyword != "" {
		like := "%" + keyword + "%"
		query = query.Where("name LIKE ? OR remarks LIKE ?", like, like)
	}
	if group != "" {
		query = query.Where("\"group\" = ?", group)
	}

	var total int64
	query.Count(&total)

	var envs []model.EnvVar
	query.Order("position ASC, created_at DESC").
		Offset((page - 1) * pageSize).Limit(pageSize).Find(&envs)

	data := make([]map[string]interface{}, len(envs))
	for i, e := range envs {
		data[i] = e.ToDict()
	}

	response.Paginated(c, data, total, page, pageSize)
}

func (h *EnvHandler) Create(c *gin.Context) {
	raw, err := io.ReadAll(c.Request.Body)
	if err != nil {
		response.BadRequest(c, "请求参数错误")
		return
	}

	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		response.BadRequest(c, "请求内容为空")
		return
	}

	type envItem struct {
		Name    string `json:"name"`
		Value   string `json:"value"`
		Remarks string `json:"remarks"`
		Group   string `json:"group"`
	}

	var items []envItem

	if raw[0] == '[' {
		if err := json.Unmarshal(raw, &items); err != nil {
			response.BadRequest(c, "请求参数错误")
			return
		}
	} else {
		var single envItem
		if err := json.Unmarshal(raw, &single); err != nil {
			response.BadRequest(c, "请求参数错误")
			return
		}
		items = []envItem{single}
	}

	if len(items) == 0 {
		response.BadRequest(c, "请求内容为空")
		return
	}

	created := []map[string]interface{}{}
	errors := []string{}

	for i, item := range items {
		if item.Name == "" {
			errors = append(errors, fmt.Sprintf("第 %d 项: 缺少名称", i+1))
			continue
		}
		if !envNamePattern.MatchString(item.Name) {
			errors = append(errors, fmt.Sprintf("第 %d 项: 变量名 '%s' 格式无效", i+1, item.Name))
			continue
		}

		var existing model.EnvVar
		found := false
		if item.Remarks != "" {
			if database.DB.Where("name = ? AND remarks = ?", item.Name, item.Remarks).First(&existing).Error == nil {
				found = true
			}
		}

		if found {
			updates := map[string]interface{}{"value": item.Value}
			if item.Group != "" {
				updates["group"] = item.Group
			}
			database.DB.Model(&existing).Updates(updates)
			database.DB.First(&existing, existing.ID)
			created = append(created, existing.ToDict())
			continue
		}

		env := model.EnvVar{
			Name:     item.Name,
			Value:    item.Value,
			Remarks:  item.Remarks,
			Group:    item.Group,
			Enabled:  true,
			Position: 10000.0,
		}

		if err := database.DB.Create(&env).Error; err != nil {
			errors = append(errors, fmt.Sprintf("item %d: %s", i+1, err.Error()))
			continue
		}
		created = append(created, env.ToDict())
	}

	if len(created) == 1 && len(errors) == 0 {
		response.Created(c, gin.H{"message": "创建成功", "data": created[0]})
		return
	}

	response.Created(c, gin.H{
		"message": fmt.Sprintf("成功创建 %d 个环境变量", len(created)),
		"data":    created,
		"errors":  errors,
	})
}

func (h *EnvHandler) Update(c *gin.Context) {
	envID, _ := strconv.ParseUint(c.Param("id"), 10, 32)

	var env model.EnvVar
	if err := database.DB.First(&env, envID).Error; err != nil {
		response.NotFound(c, "环境变量不存在")
		return
	}

	var req map[string]interface{}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "请求参数错误")
		return
	}

	allowed := map[string]bool{"name": true, "value": true, "remarks": true, "group": true}
	updates := make(map[string]interface{})
	for k, v := range req {
		if allowed[k] {
			updates[k] = v
		}
	}

	if name, ok := updates["name"].(string); ok {
		if !envNamePattern.MatchString(name) {
			response.BadRequest(c, "变量名格式无效")
			return
		}
	}

	if len(updates) > 0 {
		database.DB.Model(&env).Updates(updates)
	}

	database.DB.First(&env, envID)
	response.Success(c, gin.H{"message": "更新成功", "data": env.ToDict()})
}

func (h *EnvHandler) Delete(c *gin.Context) {
	envID, _ := strconv.ParseUint(c.Param("id"), 10, 32)
	database.DB.Where("id = ?", envID).Delete(&model.EnvVar{})
	response.Success(c, gin.H{"message": "删除成功"})
}

func (h *EnvHandler) Enable(c *gin.Context) {
	envID, _ := strconv.ParseUint(c.Param("id"), 10, 32)
	var env model.EnvVar
	if err := database.DB.First(&env, envID).Error; err != nil {
		response.NotFound(c, "环境变量不存在")
		return
	}
	database.DB.Model(&env).Update("enabled", true)
	env.Enabled = true
	response.Success(c, gin.H{"message": "已启用", "data": env.ToDict()})
}

func (h *EnvHandler) Disable(c *gin.Context) {
	envID, _ := strconv.ParseUint(c.Param("id"), 10, 32)
	var env model.EnvVar
	if err := database.DB.First(&env, envID).Error; err != nil {
		response.NotFound(c, "环境变量不存在")
		return
	}
	database.DB.Model(&env).Update("enabled", false)
	env.Enabled = false
	response.Success(c, gin.H{"message": "已禁用", "data": env.ToDict()})
}

func (h *EnvHandler) BatchDelete(c *gin.Context) {
	var req struct {
		IDs []uint `json:"ids" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "请求参数错误")
		return
	}

	result := database.DB.Where("id IN ?", req.IDs).Delete(&model.EnvVar{})
	response.Success(c, gin.H{
		"message": fmt.Sprintf("已删除 %d 个环境变量", result.RowsAffected),
	})
}

func (h *EnvHandler) BatchEnable(c *gin.Context) {
	var req struct {
		IDs []uint `json:"ids" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "请求参数错误")
		return
	}

	result := database.DB.Model(&model.EnvVar{}).Where("id IN ?", req.IDs).Update("enabled", true)
	response.Success(c, gin.H{
		"message": fmt.Sprintf("已启用 %d 个环境变量", result.RowsAffected),
	})
}

func (h *EnvHandler) BatchDisable(c *gin.Context) {
	var req struct {
		IDs []uint `json:"ids" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "请求参数错误")
		return
	}

	result := database.DB.Model(&model.EnvVar{}).Where("id IN ?", req.IDs).Update("enabled", false)
	response.Success(c, gin.H{
		"message": fmt.Sprintf("已禁用 %d 个环境变量", result.RowsAffected),
	})
}

func (h *EnvHandler) Sort(c *gin.Context) {
	var req struct {
		SourceID uint  `json:"source_id" binding:"required"`
		TargetID *uint `json:"target_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "请求参数错误")
		return
	}

	var source model.EnvVar
	if err := database.DB.First(&source, req.SourceID).Error; err != nil {
		response.NotFound(c, "源环境变量不存在")
		return
	}

	if req.TargetID == nil {
		var maxPos float64
		database.DB.Model(&model.EnvVar{}).Select("COALESCE(MAX(position), 0)").Scan(&maxPos)
		database.DB.Model(&source).Update("position", maxPos+1000)
	} else {
		var target model.EnvVar
		if err := database.DB.First(&target, *req.TargetID).Error; err != nil {
			response.NotFound(c, "目标环境变量不存在")
			return
		}

		var prev model.EnvVar
		err := database.DB.Where("position < ? AND id != ?", target.Position, source.ID).
			Order("position DESC").First(&prev).Error

		var newPos float64
		if err != nil {
			newPos = target.Position / 2.0
		} else {
			newPos = (prev.Position + target.Position) / 2.0
		}

		database.DB.Model(&source).Update("position", newPos)
	}

	response.Success(c, gin.H{"message": "排序更新成功"})
}

func (h *EnvHandler) Groups(c *gin.Context) {
	var groups []string
	database.DB.Model(&model.EnvVar{}).
		Where("\"group\" != ''").
		Distinct("\"group\"").
		Pluck("\"group\"", &groups)

	response.Success(c, gin.H{"data": groups})
}

func (h *EnvHandler) Export(c *gin.Context) {
	var envs []model.EnvVar
	database.DB.Where("enabled = ?", true).Order("position ASC").Find(&envs)

	data := make(map[string]string)
	for _, e := range envs {
		data[e.Name] = e.Value
	}

	response.Success(c, gin.H{"data": data})
}

func (h *EnvHandler) ExportAll(c *gin.Context) {
	var envs []model.EnvVar
	database.DB.Order("position ASC").Find(&envs)

	data := make([]map[string]interface{}, len(envs))
	for i, e := range envs {
		data[i] = map[string]interface{}{
			"name":    e.Name,
			"value":   e.Value,
			"remarks": e.Remarks,
			"group":   e.Group,
			"enabled": e.Enabled,
		}
	}

	response.Success(c, gin.H{"data": data})
}

func (h *EnvHandler) ExportFiles(c *gin.Context) {
	var req struct {
		Format      string `json:"format"`
		EnabledOnly *bool  `json:"enabled_only"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		req.Format = "all"
	}
	if req.Format == "" {
		req.Format = "all"
	}

	query := database.DB.Model(&model.EnvVar{}).Order("position ASC")
	if req.EnabledOnly != nil && *req.EnabledOnly {
		query = query.Where("enabled = ?", true)
	}

	var envs []model.EnvVar
	query.Find(&envs)

	grouped := groupEnvs(envs)

	result := make(map[string]string)
	if req.Format == "shell" || req.Format == "all" {
		result["shell"] = exportShell(grouped)
	}
	if req.Format == "js" || req.Format == "all" {
		result["js"] = exportJS(grouped)
	}
	if req.Format == "python" || req.Format == "all" {
		result["python"] = exportPython(grouped)
	}

	response.Success(c, gin.H{"data": result})
}

func groupEnvs(envs []model.EnvVar) map[string]string {
	grouped := make(map[string][]string)
	for _, e := range envs {
		grouped[e.Name] = append(grouped[e.Name], e.Value)
	}
	result := make(map[string]string)
	for name, vals := range grouped {
		result[name] = strings.Join(vals, "&")
	}
	return result
}

func exportShell(envs map[string]string) string {
	var b strings.Builder
	b.WriteString("#!/bin/bash\n")
	b.WriteString("# 呆呆面板 - 环境变量\n\n")

	keys := sortedKeys(envs)
	for _, k := range keys {
		v := envs[k]
		escaped := strings.ReplaceAll(v, "'", "'\\''")
		b.WriteString(fmt.Sprintf("export %s='%s'\n", k, escaped))
	}
	return b.String()
}

func exportJS(envs map[string]string) string {
	var b strings.Builder
	b.WriteString("// 呆呆面板 - 环境变量\n\n")

	keys := sortedKeys(envs)
	for _, k := range keys {
		v := envs[k]
		escaped := strings.ReplaceAll(v, "\\", "\\\\")
		escaped = strings.ReplaceAll(escaped, "\"", "\\\"")
		escaped = strings.ReplaceAll(escaped, "\n", "\\n")
		b.WriteString(fmt.Sprintf("process.env.%s = \"%s\";\n", k, escaped))
	}
	return b.String()
}

func exportPython(envs map[string]string) string {
	var b strings.Builder
	b.WriteString("# -*- coding: utf-8 -*-\n")
	b.WriteString("# 呆呆面板 - 环境变量\n")
	b.WriteString("import os\n\n")

	keys := sortedKeys(envs)
	for _, k := range keys {
		v := envs[k]
		escaped := strings.ReplaceAll(v, "'", "\\'")
		escaped = strings.ReplaceAll(escaped, "\n", "\\n")
		b.WriteString(fmt.Sprintf("os.environ['%s'] = '%s'\n", k, escaped))
	}
	return b.String()
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func (h *EnvHandler) Import(c *gin.Context) {
	var req struct {
		Envs []map[string]interface{} `json:"envs" binding:"required"`
		Mode string                   `json:"mode"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "请求参数错误")
		return
	}

	if req.Mode == "" {
		req.Mode = "merge"
	}

	if req.Mode == "replace" {
		database.DB.Where("1 = 1").Delete(&model.EnvVar{})
	}

	imported := 0
	errors := []string{}

	for i, item := range req.Envs {
		name, _ := item["name"].(string)
		value, _ := item["value"].(string)
		if name == "" {
			errors = append(errors, fmt.Sprintf("第 %d 项: 缺少名称", i+1))
			continue
		}

		if !envNamePattern.MatchString(name) {
			errors = append(errors, fmt.Sprintf("第 %d 项: 名称 '%s' 格式无效", i+1, name))
			continue
		}

		remarks, _ := item["remarks"].(string)
		group, _ := item["group"].(string)

		enabled := true
		if statusVal, ok := item["status"].(float64); ok {
			enabled = statusVal == 0
		} else if enabledVal, ok := item["enabled"].(bool); ok {
			enabled = enabledVal
		}

		if req.Mode == "merge" {
			var existing model.EnvVar
			if database.DB.Where("name = ? AND value = ?", name, value).First(&existing).Error == nil {
				updates := map[string]interface{}{}
				if remarks != "" {
					updates["remarks"] = remarks
				}
				if group != "" {
					updates["group"] = group
				}
				updates["enabled"] = enabled
				database.DB.Model(&existing).Updates(updates)
				imported++
				continue
			}
		}

		env := model.EnvVar{
			Name:     name,
			Value:    value,
			Remarks:  remarks,
			Group:    group,
			Enabled:  enabled,
			Position: 10000.0,
		}
		if err := database.DB.Create(&env).Error; err != nil {
			errors = append(errors, fmt.Sprintf("item %d: %s", i+1, err.Error()))
			continue
		}
		imported++
	}

	if imported == 0 && len(errors) > 0 {
		response.BadRequest(c, "没有成功导入任何环境变量")
		return
	}

	c.JSON(201, gin.H{
		"message": fmt.Sprintf("成功导入 %d 个环境变量", imported),
		"errors":  errors,
	})
}

func (h *EnvHandler) RegisterRoutes(r *gin.RouterGroup) {
	envs := r.Group("/envs", middleware.JWTAuth())
	{
		envs.GET("", h.List)
		envs.POST("", h.Create)
		envs.PUT("/:id", h.Update)
		envs.DELETE("/:id", h.Delete)
		envs.PUT("/:id/enable", h.Enable)
		envs.PUT("/:id/disable", h.Disable)
		envs.DELETE("/batch", h.BatchDelete)
		envs.PUT("/batch/enable", h.BatchEnable)
		envs.PUT("/batch/disable", h.BatchDisable)
		envs.GET("/export", h.Export)
		envs.PUT("/sort", h.Sort)
		envs.GET("/groups", h.Groups)
		envs.GET("/export-all", h.ExportAll)
		envs.POST("/export-files", h.ExportFiles)
		envs.POST("/import", h.Import)
	}
}
