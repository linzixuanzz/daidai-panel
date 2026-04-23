package handler

import (
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"daidai-panel/database"
	"daidai-panel/middleware"
	"daidai-panel/model"
	"daidai-panel/pkg/response"
	"daidai-panel/service"

	"github.com/gin-gonic/gin"
)

type subPullBroadcaster struct {
	mu   sync.RWMutex
	subs map[chan string]struct{}
	log  strings.Builder
}

var (
	subPullStreams   = make(map[uint]*subPullBroadcaster)
	subPullStreamsMu sync.RWMutex
)

func getOrCreateSubBroadcaster(id uint) *subPullBroadcaster {
	subPullStreamsMu.Lock()
	defer subPullStreamsMu.Unlock()
	if b, ok := subPullStreams[id]; ok {
		return b
	}
	b := &subPullBroadcaster{subs: make(map[chan string]struct{})}
	subPullStreams[id] = b
	return b
}

func removeSubBroadcaster(id uint) {
	subPullStreamsMu.Lock()
	defer subPullStreamsMu.Unlock()
	if b, ok := subPullStreams[id]; ok {
		b.mu.Lock()
		for ch := range b.subs {
			close(ch)
		}
		b.mu.Unlock()
		delete(subPullStreams, id)
	}
}

func (b *subPullBroadcaster) subscribe() chan string {
	ch := make(chan string, 64)
	b.mu.Lock()
	b.subs[ch] = struct{}{}
	b.mu.Unlock()
	return ch
}

func (b *subPullBroadcaster) unsubscribe(ch chan string) {
	b.mu.Lock()
	delete(b.subs, ch)
	b.mu.Unlock()
}

func (b *subPullBroadcaster) broadcast(line string) {
	b.mu.Lock()
	b.log.WriteString(line)
	b.log.WriteString("\n")
	b.mu.Unlock()

	b.mu.RLock()
	defer b.mu.RUnlock()
	for ch := range b.subs {
		select {
		case ch <- line:
		default:
		}
	}
}

func (b *subPullBroadcaster) done() {
	b.mu.RLock()
	defer b.mu.RUnlock()
	for ch := range b.subs {
		select {
		case ch <- "\x00DONE":
		default:
		}
	}
}

func (b *subPullBroadcaster) history() string {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.log.String()
}

type SubscriptionHandler struct{}

func NewSubscriptionHandler() *SubscriptionHandler {
	return &SubscriptionHandler{}
}

func (h *SubscriptionHandler) List(c *gin.Context) {
	keyword := c.Query("keyword")
	subType := c.Query("type")
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))

	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}

	query := database.DB.Model(&model.Subscription{})

	if keyword != "" {
		like := "%" + keyword + "%"
		query = query.Where("name LIKE ? OR url LIKE ?", like, like)
	}
	if subType != "" {
		query = query.Where("type = ?", subType)
	}

	var total int64
	query.Count(&total)

	var subs []model.Subscription
	query.Order("created_at DESC").
		Offset((page - 1) * pageSize).Limit(pageSize).Find(&subs)

	data := make([]map[string]interface{}, len(subs))
	for i, s := range subs {
		data[i] = s.ToDict()
	}

	response.Paginated(c, data, total, page, pageSize)
}

func (h *SubscriptionHandler) Create(c *gin.Context) {
	var req struct {
		Name           string `json:"name" binding:"required"`
		Type           string `json:"type"`
		URL            string `json:"url" binding:"required"`
		Branch         string `json:"branch"`
		Schedule       string `json:"schedule"`
		Whitelist      string `json:"whitelist"`
		Blacklist      string `json:"blacklist"`
		DependOn       string `json:"depend_on"`
		HookScript     string `json:"hook_script"`
		AutoAddTask    bool   `json:"auto_add_task"`
		AutoDelTask    bool   `json:"auto_del_task"`
		SaveDir        string `json:"save_dir"`
		SubPath        string `json:"sub_path"`
		SSHKeyID       *uint  `json:"ssh_key_id"`
		Alias          string `json:"alias"`
		ForceOverwrite *bool  `json:"force_overwrite"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "请求参数错误")
		return
	}

	if req.Type == "" {
		req.Type = model.SubTypeGitRepo
	}
	if !service.ValidateSubscriptionSchedule(req.Schedule) {
		response.BadRequest(c, "无效的订阅定时规则")
		return
	}

	sub := model.Subscription{
		Name:           req.Name,
		Type:           req.Type,
		URL:            req.URL,
		Branch:         req.Branch,
		Schedule:       req.Schedule,
		Whitelist:      req.Whitelist,
		Blacklist:      req.Blacklist,
		DependOn:       req.DependOn,
		HookScript:     req.HookScript,
		AutoAddTask:    req.AutoAddTask,
		AutoDelTask:    req.AutoDelTask,
		Enabled:        true,
		SaveDir:        req.SaveDir,
		SubPath:        req.SubPath,
		SSHKeyID:       req.SSHKeyID,
		Alias:          req.Alias,
		ForceOverwrite: req.ForceOverwrite,
	}

	if err := database.DB.Create(&sub).Error; err != nil {
		response.InternalError(c, "创建订阅失败")
		return
	}

	if err := service.GetSubscriptionScheduler().AddOrUpdateJob(&sub); err != nil {
		response.InternalError(c, "创建订阅成功，但定时调度注册失败")
		return
	}

	response.Created(c, gin.H{"message": "创建成功", "data": sub.ToDict()})
}

func (h *SubscriptionHandler) Update(c *gin.Context) {
	subID, _ := strconv.ParseUint(c.Param("id"), 10, 32)

	var sub model.Subscription
	if err := database.DB.First(&sub, subID).Error; err != nil {
		response.NotFound(c, "订阅不存在")
		return
	}

	var req map[string]interface{}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "请求参数错误")
		return
	}

	allowed := map[string]bool{
		"name": true, "type": true, "url": true, "branch": true,
		"schedule": true, "whitelist": true, "blacklist": true,
		"depend_on": true, "hook_script": true, "auto_add_task": true, "auto_del_task": true,
		"save_dir": true, "sub_path": true, "ssh_key_id": true, "alias": true, "force_overwrite": true,
	}
	updates := make(map[string]interface{})
	for k, v := range req {
		if allowed[k] {
			updates[k] = v
		}
	}

	if schedule, ok := updates["schedule"].(string); ok {
		if !service.ValidateSubscriptionSchedule(schedule) {
			response.BadRequest(c, "无效的订阅定时规则")
			return
		}
	}

	if len(updates) > 0 {
		database.DB.Model(&sub).Updates(updates)
	}

	database.DB.First(&sub, subID)
	if err := service.GetSubscriptionScheduler().AddOrUpdateJob(&sub); err != nil {
		response.InternalError(c, "更新成功，但定时调度注册失败")
		return
	}
	response.Success(c, gin.H{"message": "更新成功", "data": sub.ToDict()})
}

func (h *SubscriptionHandler) Delete(c *gin.Context) {
	subID, _ := strconv.ParseUint(c.Param("id"), 10, 32)
	service.GetSubscriptionScheduler().RemoveJob(uint(subID))
	database.DB.Where("id = ?", subID).Delete(&model.Subscription{})
	database.DB.Where("subscription_id = ?", subID).Delete(&model.SubLog{})
	response.Success(c, gin.H{"message": "删除成功"})
}

func (h *SubscriptionHandler) Enable(c *gin.Context) {
	subID, _ := strconv.ParseUint(c.Param("id"), 10, 32)
	var sub model.Subscription
	if err := database.DB.First(&sub, subID).Error; err != nil {
		response.NotFound(c, "订阅不存在")
		return
	}
	database.DB.Model(&sub).Update("enabled", true)
	sub.Enabled = true
	if err := service.GetSubscriptionScheduler().AddOrUpdateJob(&sub); err != nil {
		response.InternalError(c, "启用成功，但定时调度注册失败")
		return
	}
	response.Success(c, gin.H{"message": "已启用", "data": sub.ToDict()})
}

func (h *SubscriptionHandler) Disable(c *gin.Context) {
	subID, _ := strconv.ParseUint(c.Param("id"), 10, 32)
	var sub model.Subscription
	if err := database.DB.First(&sub, subID).Error; err != nil {
		response.NotFound(c, "订阅不存在")
		return
	}
	database.DB.Model(&sub).Update("enabled", false)
	sub.Enabled = false
	service.GetSubscriptionScheduler().RemoveJob(sub.ID)
	response.Success(c, gin.H{"message": "已禁用", "data": sub.ToDict()})
}

func (h *SubscriptionHandler) Pull(c *gin.Context) {
	subID, _ := strconv.ParseUint(c.Param("id"), 10, 32)
	var sub model.Subscription
	if err := database.DB.First(&sub, subID).Error; err != nil {
		response.NotFound(c, "订阅不存在")
		return
	}

	if overrideStr := c.Query("force_overwrite"); overrideStr != "" {
		override := overrideStr == "true" || overrideStr == "1"
		sub.ForceOverwrite = &override
	}

	if service.IsSubscriptionPullRunning(uint(subID)) {
		response.BadRequest(c, "该订阅正在拉取中")
		return
	}

	broadcaster := getOrCreateSubBroadcaster(uint(subID))

	go func() {
		defer removeSubBroadcaster(uint(subID))
		service.ExecuteSubscriptionPull(&sub, func(line string) {
			broadcaster.broadcast(line)
		})
		broadcaster.done()
	}()

	response.Success(c, gin.H{"message": "拉取任务已启动"})
}

func (h *SubscriptionHandler) StopPull(c *gin.Context) {
	subID, _ := strconv.ParseUint(c.Param("id"), 10, 32)
	if !service.IsSubscriptionPullRunning(uint(subID)) {
		response.BadRequest(c, "当前没有进行中的拉取任务")
		return
	}

	subPullStreamsMu.RLock()
	broadcaster, exists := subPullStreams[uint(subID)]
	subPullStreamsMu.RUnlock()
	if exists {
		broadcaster.broadcast("[停止请求] 正在终止当前拉取任务...")
	}

	if !service.StopSubscriptionPull(uint(subID)) {
		response.BadRequest(c, "拉取任务停止失败")
		return
	}

	response.Success(c, gin.H{"message": "已发送停止请求"})
}

func (h *SubscriptionHandler) PullStream(c *gin.Context) {
	subID, _ := strconv.ParseUint(c.Param("id"), 10, 32)

	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")

	subPullStreamsMu.RLock()
	broadcaster, exists := subPullStreams[uint(subID)]
	subPullStreamsMu.RUnlock()

	if !exists {
		fmt.Fprintf(c.Writer, "event: done\ndata: not_running\n\n")
		c.Writer.Flush()
		return
	}

	history := broadcaster.history()
	if history != "" {
		for _, line := range strings.Split(strings.TrimRight(history, "\n"), "\n") {
			if line != "" {
				fmt.Fprintf(c.Writer, "data: %s\n\n", line)
			}
		}
		c.Writer.Flush()
	}

	sub := broadcaster.subscribe()
	defer broadcaster.unsubscribe(sub)

	ctx := c.Request.Context()
	for {
		select {
		case line, ok := <-sub:
			if !ok {
				fmt.Fprintf(c.Writer, "event: done\ndata: closed\n\n")
				c.Writer.Flush()
				return
			}
			if line == "\x00DONE" {
				fmt.Fprintf(c.Writer, "event: done\ndata: finished\n\n")
				c.Writer.Flush()
				return
			}
			fmt.Fprintf(c.Writer, "data: %s\n\n", line)
			c.Writer.Flush()
		case <-ctx.Done():
			return
		case <-time.After(5 * time.Minute):
			fmt.Fprintf(c.Writer, "event: done\ndata: timeout\n\n")
			c.Writer.Flush()
			return
		}
	}
}

func (h *SubscriptionHandler) Logs(c *gin.Context) {
	subID, _ := strconv.ParseUint(c.Param("id"), 10, 32)
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))

	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}

	query := database.DB.Model(&model.SubLog{}).Where("subscription_id = ?", subID)

	var total int64
	query.Count(&total)

	var logs []model.SubLog
	query.Order("created_at DESC").
		Offset((page - 1) * pageSize).Limit(pageSize).Find(&logs)

	data := make([]map[string]interface{}, len(logs))
	for i, l := range logs {
		data[i] = l.ToDict()
	}

	response.Paginated(c, data, total, page, pageSize)
}

func (h *SubscriptionHandler) BatchDelete(c *gin.Context) {
	var req struct {
		IDs []uint `json:"ids" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "请求参数错误")
		return
	}

	result := database.DB.Where("id IN ?", req.IDs).Delete(&model.Subscription{})
	database.DB.Where("subscription_id IN ?", req.IDs).Delete(&model.SubLog{})
	for _, id := range req.IDs {
		service.GetSubscriptionScheduler().RemoveJob(id)
	}

	response.Success(c, gin.H{
		"message": fmt.Sprintf("已删除 %d 个订阅", result.RowsAffected),
	})
}

func (h *SubscriptionHandler) RegisterRoutes(r *gin.RouterGroup) {
	subs := r.Group("/subscriptions", middleware.JWTAuth(), middleware.OpenAPIAccess("subscriptions"), middleware.RequireRole("operator"))
	{
		subs.GET("", h.List)
		subs.POST("", h.Create)
		subs.PUT("/:id", h.Update)
		subs.DELETE("/:id", h.Delete)
		subs.PUT("/:id/enable", h.Enable)
		subs.PUT("/:id/disable", h.Disable)
		subs.PUT("/:id/pull", h.Pull)
		subs.PUT("/:id/pull/stop", h.StopPull)
		subs.GET("/:id/pull-stream", h.PullStream)
		subs.GET("/:id/logs", h.Logs)
		subs.DELETE("/batch", h.BatchDelete)
	}
}
