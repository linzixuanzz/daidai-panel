package handler

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"daidai-panel/database"
	"daidai-panel/middleware"
	"daidai-panel/model"
	"daidai-panel/pkg/crypto"
	"daidai-panel/pkg/response"
	"daidai-panel/pkg/trigticket"
	"daidai-panel/service"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"

	"daidai-panel/config"
)

// openAPITokenFailureLimit / openAPITokenFailureWindow：同一个计数桶（同 IP 的某个 app_key，或同 IP 的不存在 app_key）
// 换令牌失败 10 次后，10 分钟内不再受理。
const (
	openAPITokenFailureLimit  = 10
	openAPITokenFailureWindow = 10 * time.Minute
)

type OpenAPIHandler struct {
	// tokenFailures 只数 POST /open-api/token 的失败（app_key 不存在、secret 不对）。
	// 挂在 handler 上而不是每次注册时现构造：/api 与 /api/v1 两个前缀共用同一个 handler，
	// 共用计数才能防住「换个前缀接着猜」。
	tokenFailures *middleware.FailureLimiter
}

func NewOpenAPIHandler() *OpenAPIHandler {
	return &OpenAPIHandler{
		tokenFailures: middleware.NewFailureLimiter(openAPITokenFailureLimit, openAPITokenFailureWindow),
	}
}

func generateRandomKey(length int) string {
	bytes := make([]byte, length)
	rand.Read(bytes)
	return hex.EncodeToString(bytes)
}

type openAppDailyCountRow struct {
	AppID uint
	Total int64
}

func startOfToday(now time.Time) time.Time {
	return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
}

func loadOpenAppDailyCounts(appIDs []uint) map[uint]int64 {
	counts := make(map[uint]int64, len(appIDs))
	if len(appIDs) == 0 {
		return counts
	}

	var rows []openAppDailyCountRow
	database.DB.
		Model(&model.ApiCallLog{}).
		Select("app_id, COUNT(*) AS total").
		Where("app_id IN ? AND created_at >= ?", appIDs, startOfToday(time.Now())).
		Group("app_id").
		Scan(&rows)

	for _, row := range rows {
		counts[row.AppID] = row.Total
	}
	return counts
}

func loadOpenAppDailyCount(appID uint) int64 {
	return loadOpenAppDailyCounts([]uint{appID})[appID]
}

func buildOpenAppResponse(app *model.OpenApp, dailyCount int64, includeSecret bool) map[string]interface{} {
	item := app.ToDict()
	item["call_count"] = dailyCount
	if includeSecret {
		item["app_secret"] = app.AppSecret
	}
	return item
}

func buildOpenAppListResponse(apps []model.OpenApp) []map[string]interface{} {
	appIDs := make([]uint, 0, len(apps))
	for _, app := range apps {
		appIDs = append(appIDs, app.ID)
	}

	dailyCounts := loadOpenAppDailyCounts(appIDs)
	data := make([]map[string]interface{}, len(apps))
	for i := range apps {
		data[i] = buildOpenAppResponse(&apps[i], dailyCounts[apps[i].ID], false)
	}
	return data
}

func (h *OpenAPIHandler) List(c *gin.Context) {
	var apps []model.OpenApp
	database.DB.Order("created_at DESC").Find(&apps)
	response.Success(c, gin.H{"data": buildOpenAppListResponse(apps)})
}

func (h *OpenAPIHandler) Create(c *gin.Context) {
	var req struct {
		Name      string `json:"name" binding:"required"`
		Scopes    string `json:"scopes"`
		RateLimit int    `json:"rate_limit"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "请求参数错误")
		return
	}

	if req.RateLimit < 0 {
		response.BadRequest(c, "速率限制不能小于 0")
		return
	}

	app := model.OpenApp{
		Name:      req.Name,
		AppKey:    generateRandomKey(16),
		AppSecret: generateRandomKey(32),
		Scopes:    req.Scopes,
		Enabled:   true,
		RateLimit: req.RateLimit,
	}

	// open_apps.name 是唯一索引，连点创建按钮的第二发会撞在这里。
	// 这个接口尤其怕连点：每成功一次前端就弹一次「密钥只展示这一次」，用户根本对不上是哪一个应用。
	if err := database.DB.Create(&app).Error; err != nil {
		response.BadRequest(c, "同名应用已存在")
		return
	}
	recordAudit(c, service.AuditOpenAppCreate, fmt.Sprintf("新建应用 %s（scope：%s）", app.Name, app.Scopes))

	response.Created(c, gin.H{"message": "创建成功", "data": buildOpenAppResponse(&app, 0, true)})
}

func (h *OpenAPIHandler) Update(c *gin.Context) {
	appID, _ := strconv.ParseUint(c.Param("id"), 10, 32)

	var app model.OpenApp
	if err := database.DB.First(&app, appID).Error; err != nil {
		response.NotFound(c, "应用不存在")
		return
	}

	var req map[string]interface{}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "请求参数错误")
		return
	}

	allowed := map[string]bool{"name": true, "scopes": true, "rate_limit": true}
	updates := make(map[string]interface{})
	for k, v := range req {
		if k == "rate_limit" {
			switch value := v.(type) {
			case float64:
				if value < 0 {
					response.BadRequest(c, "速率限制不能小于 0")
					return
				}
			case int:
				if value < 0 {
					response.BadRequest(c, "速率限制不能小于 0")
					return
				}
			}
		}
		if allowed[k] {
			updates[k] = v
		}
	}

	oldScopes := app.Scopes
	if len(updates) > 0 {
		// 改名撞上别的应用会在这里报错；不接 .Error 就会出现「提示更新成功、刷新又变回去」。
		if err := database.DB.Model(&app).Updates(updates).Error; err != nil {
			response.BadRequest(c, "同名应用已存在")
			return
		}
	}

	database.DB.First(&app, appID)
	// 只有权限范围真的变了才记：改名、改限流不算敏感操作
	if app.Scopes != oldScopes {
		recordAudit(c, service.AuditOpenAppScopeChange, fmt.Sprintf("应用 %s 的 scope：%s → %s", app.Name, oldScopes, app.Scopes))
	}
	response.Success(c, gin.H{"message": "更新成功", "data": buildOpenAppResponse(&app, loadOpenAppDailyCount(app.ID), false)})
}

func (h *OpenAPIHandler) Delete(c *gin.Context) {
	appID, _ := strconv.ParseUint(c.Param("id"), 10, 32)
	// 先读出名字给审计用；读不到时删除照旧执行（响应保持原样），只是不记审计
	var app model.OpenApp
	found := database.DB.First(&app, appID).Error == nil
	result := database.DB.Where("id = ?", appID).Delete(&model.OpenApp{})
	if found && result.Error == nil && result.RowsAffected > 0 {
		recordAudit(c, service.AuditOpenAppDelete, "删除应用 "+app.Name)
	}
	response.Success(c, gin.H{"message": "删除成功"})
}

func (h *OpenAPIHandler) Enable(c *gin.Context) {
	appID, _ := strconv.ParseUint(c.Param("id"), 10, 32)
	database.DB.Model(&model.OpenApp{}).Where("id = ?", appID).Update("enabled", true)
	response.Success(c, gin.H{"message": "已启用"})
}

func (h *OpenAPIHandler) Disable(c *gin.Context) {
	appID, _ := strconv.ParseUint(c.Param("id"), 10, 32)
	database.DB.Model(&model.OpenApp{}).Where("id = ?", appID).Update("enabled", false)
	response.Success(c, gin.H{"message": "已禁用"})
}

func (h *OpenAPIHandler) ResetSecret(c *gin.Context) {
	appID, _ := strconv.ParseUint(c.Param("id"), 10, 32)

	var app model.OpenApp
	if err := database.DB.First(&app, appID).Error; err != nil {
		response.NotFound(c, "应用不存在")
		return
	}

	newSecret := generateRandomKey(32)
	if database.DB.Model(&app).Update("app_secret", newSecret).Error == nil {
		recordAudit(c, service.AuditOpenAppResetSecret, "重置应用 "+app.Name+" 的密钥")
	}
	app.AppSecret = newSecret

	response.Success(c, gin.H{"message": "密钥已重置", "data": buildOpenAppResponse(&app, loadOpenAppDailyCount(app.ID), true)})
}

func (h *OpenAPIHandler) ViewSecret(c *gin.Context) {
	appID, _ := strconv.ParseUint(c.Param("id"), 10, 32)

	var req struct {
		Password string `json:"password" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "请输入密码")
		return
	}

	username := c.GetString("username")
	var user model.User
	if err := database.DB.Where("username = ?", username).First(&user).Error; err != nil {
		response.Unauthorized(c, "用户不存在")
		return
	}

	if !crypto.CheckPassword(req.Password, user.Password) {
		response.Unauthorized(c, "密码错误")
		return
	}

	var app model.OpenApp
	if err := database.DB.First(&app, appID).Error; err != nil {
		response.NotFound(c, "应用不存在")
		return
	}

	response.Success(c, gin.H{"data": gin.H{"app_secret": app.AppSecret}})
}

func (h *OpenAPIHandler) Token(c *gin.Context) {
	var req struct {
		AppKey    string `json:"app_key" binding:"required"`
		AppSecret string `json:"app_secret" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "请求参数错误")
		return
	}

	// 只有失败才计数，凭据正确的脚本再频繁取令牌也不会被挡。
	// 计数分两个桶：本机脚本都从同一个回环 IP 来，只按 IP 计数的话，一个拿着旧 secret 的脚本会把同机所有正常脚本一起封住。
	//   - app_key 存在但 secret 错：按「IP + app_key」计数，只封这一个 key；
	//   - app_key 不存在：按 IP 计数，也只在「app_key 不存在」这条路径上检查，不影响同 IP 的其他已知 key。
	ip := middleware.ResolveClientIP(c)

	var app model.OpenApp
	if err := database.DB.Where("app_key = ?", req.AppKey).First(&app).Error; err != nil {
		unknownKeyBucket := "unknown-key|" + ip
		if h.tokenFailures != nil && h.tokenFailures.Blocked(unknownKeyBucket) {
			response.TooManyRequests(c, "凭证错误次数过多，请 10 分钟后再试")
			return
		}
		if h.tokenFailures != nil {
			h.tokenFailures.RecordFailure(unknownKeyBucket)
		}
		response.Unauthorized(c, "凭证无效")
		return
	}

	// 这个 key 在这个 IP 上已经失败太多次：封禁期间不再比对 secret，直接拒绝。
	// 不能改成「对了就放行」，否则封禁形同虚设，猜密钥的人照样能一直试下去。
	keyBucket := "app-key|" + ip + "|" + app.AppKey
	if h.tokenFailures != nil && h.tokenFailures.Blocked(keyBucket) {
		response.TooManyRequests(c, "凭证错误次数过多，请 10 分钟后再试")
		return
	}

	if !app.Enabled {
		response.Forbidden(c, "应用已被禁用")
		return
	}

	// 定长比较：逐字节比较会因为「前几位对上了就多比一会儿」泄漏耗时差，可被用来逐位猜密钥。
	if subtle.ConstantTimeCompare([]byte(app.AppSecret), []byte(req.AppSecret)) != 1 {
		if h.tokenFailures != nil {
			h.tokenFailures.RecordFailure(keyBucket)
		}
		response.Unauthorized(c, "凭证无效")
		return
	}

	claims := &middleware.Claims{
		Username:  fmt.Sprintf("app:%s", app.AppKey),
		Role:      fmt.Sprintf("app:%s", app.Scopes),
		TokenType: "access",
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(24 * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenStr, err := token.SignedString([]byte(config.C.JWT.Secret))
	if err != nil {
		response.InternalError(c, "生成令牌失败")
		return
	}

	response.Success(c, gin.H{
		"data": gin.H{
			"access_token": tokenStr,
			"token_type":   "Bearer",
			"expires_in":   86400,
		},
	})
}

func (h *OpenAPIHandler) CallLogs(c *gin.Context) {
	appID, _ := strconv.ParseUint(c.Param("id"), 10, 32)
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))

	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}

	query := database.DB.Model(&model.ApiCallLog{}).Where("app_id = ?", appID)

	var total int64
	query.Count(&total)

	var logs []model.ApiCallLog
	query.Order("created_at DESC").
		Offset((page - 1) * pageSize).Limit(pageSize).Find(&logs)

	data := make([]map[string]interface{}, len(logs))
	for i, l := range logs {
		data[i] = l.ToDict()
	}

	response.Paginated(c, data, total, page, pageSize)
}

// ---------------------------------------------------------------------------
// 企业微信菜单触发链接（issue #145 方案 B，v3.3.2）
//
// 形态与「日志原始文件下载」那两步完全一致，只是票据换成了 pkg/trigticket：
//  1. 管理员带 JWT 调 POST /open-api/apps/:id/task-trigger-link 换一张票，拿到完整 URL；
//  2. 企业微信应用菜单配这个 URL，用户点一下就是一次 GET /open-api/trigger?task_id=..&ticket=..。
//
// 第 2 条路由刻意没有任何中间件（和同层的 POST /open-api/token 一样）：企业微信内置浏览器
// 打开菜单链接时带不了 Authorization 头。它的防线是「两级总开关 + 票据验签 + 票据只绑一个任务」，
// 和 handler/mcp.go 的「路由公开 + handler 内强鉴权」是同一套口径。
// ---------------------------------------------------------------------------

// taskTriggerResource 是触发票据的资源标识。它参与签名但不随票据传输，
// 所以校验方必须自己用同一个 task_id 算出同一个串才可能验签通过 —— 一张票挪不到别的任务上。
func taskTriggerResource(taskID uint) string {
	return fmt.Sprintf("task-trigger:%d", taskID)
}

// currentTaskTriggerGeneration 读当前代次。这个键刻意没进配置注册表（见 model 侧的说明），
// 读不出来或是垃圾值时一律当 0，不要报错 —— 报错会让所有链接连带失效。
func currentTaskTriggerGeneration() int64 {
	raw := strings.TrimSpace(model.GetConfig(model.WecomTriggerLinkGenerationConfigKey, "0"))
	generation, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || generation < 0 {
		return 0
	}
	return generation
}

// IssueTaskTriggerLink 为某个任务签发一条长期有效的触发链接（管理员）。
func (h *OpenAPIHandler) IssueTaskTriggerLink(c *gin.Context) {
	appID, _ := strconv.ParseUint(c.Param("id"), 10, 32)

	var app model.OpenApp
	if err := database.DB.First(&app, appID).Error; err != nil {
		response.NotFound(c, "应用不存在")
		return
	}

	var req struct {
		TaskID uint `json:"task_id" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "请求参数错误")
		return
	}

	var task model.Task
	if err := database.DB.First(&task, req.TaskID).Error; err != nil {
		response.NotFound(c, "任务不存在")
		return
	}

	// ttl 传 0 = 不过期：菜单链接是配一次用一年的，带过期时间等于配完就失效。
	// 止血手段是代次（POST /open-api/task-trigger-links/revoke 一键 +1），不是过期时间。
	// subject 记成签发时用的应用，纯粹为了事后能查「这条链接是谁的名义发的」。
	ticket, _, err := trigticket.Issue(config.C.JWT.Secret, taskTriggerResource(task.ID), "app:"+app.AppKey, currentTaskTriggerGeneration(), 0)
	if err != nil {
		response.InternalError(c, "签发触发票据失败")
		return
	}

	query := url.Values{}
	query.Set("task_id", strconv.FormatUint(uint64(task.ID), 10))
	query.Set("ticket", ticket)

	// 从当前请求路径推导触发地址，这样 /api 与 /api/v1 两套前缀都能自动对上，不用硬编码。
	currentPath := c.Request.URL.EscapedPath()
	triggerPath := "/api/v1/open-api/trigger"
	if index := strings.LastIndex(currentPath, "/open-api/"); index >= 0 {
		triggerPath = currentPath[:index] + "/open-api/trigger"
	}
	relative := triggerPath + "?" + query.Encode()

	response.Success(c, gin.H{
		"data": gin.H{
			"url":       buildPanelAbsoluteURL(c, relative),
			"path":      relative,
			"task_id":   task.ID,
			"task_name": task.Name,
			"app_name":  app.Name,
			// 提醒前端把这句话显示出来：链接长期有效，等同于这一个任务的永久触发权。
			"notice": "该链接长期有效且无需登录，请只配置到企业微信应用菜单里；需要作废时调用「作废全部触发链接」",
		},
	})
}

// RevokeTaskTriggerLinks 把代次 +1，让所有已经发出去的触发链接立刻失效（管理员）。
func (h *OpenAPIHandler) RevokeTaskTriggerLinks(c *gin.Context) {
	generation := currentTaskTriggerGeneration() + 1
	if err := model.SetConfig(model.WecomTriggerLinkGenerationConfigKey, strconv.FormatInt(generation, 10)); err != nil {
		response.InternalError(c, "作废触发链接失败")
		return
	}
	response.Success(c, gin.H{"message": "已作废全部企业微信触发链接", "data": gin.H{"generation": generation}})
}

// TaskTrigger 是菜单链接真正落地的那一下：验票 → 跑任务 → 回一段极简 HTML。
//
// 回 HTML 而不是 JSON：企业微信会在内置浏览器里打开这个链接，用户看到的是一整屏裸 JSON
// 还是一句「已触发：签到任务」，体验差别很大。
func (h *OpenAPIHandler) TaskTrigger(c *gin.Context) {
	if !model.GetRegisteredConfigBool(model.WecomTriggerEnabledConfigKey) {
		respondTaskTriggerPage(c, http.StatusForbidden, "未开启", "请管理员在「系统设置 → 企业微信触发」中启用后再试")
		return
	}

	taskID, err := strconv.ParseUint(strings.TrimSpace(c.Query("task_id")), 10, 32)
	if err != nil || taskID == 0 {
		respondTaskTriggerPage(c, http.StatusBadRequest, "链接无效", "链接里缺少任务参数，请让管理员重新生成")
		return
	}

	ticket := strings.TrimSpace(c.Query("ticket"))
	if ticket == "" {
		respondTaskTriggerPage(c, http.StatusUnauthorized, "链接无效", "链接里缺少触发票据，请让管理员重新生成")
		return
	}
	// 先验票再查库：不验票就查库会让未授权访问者靠 404 / 401 的差异探测某个任务存不存在。
	// 无效、过期、已作废三种情况对外一律同一句话，同样是为了不泄漏可区分的信息。
	if _, err := trigticket.Verify(config.C.JWT.Secret, ticket, taskTriggerResource(uint(taskID)), currentTaskTriggerGeneration()); err != nil {
		respondTaskTriggerPage(c, http.StatusUnauthorized, "链接已失效", "请让管理员重新生成触发链接")
		return
	}

	if !model.GetRegisteredConfigBool(model.WecomTriggerAllowRunConfigKey) {
		respondTaskTriggerPage(c, http.StatusForbidden, "未允许触发执行", "管理员已开启企业微信触发，但还没打开「允许企业微信触发执行」")
		return
	}

	task, err := runTaskByID(uint(taskID))
	if err != nil {
		name := ""
		if task != nil {
			name = task.Name
		}
		respondTaskTriggerPage(c, http.StatusOK, "未能触发", strings.TrimSpace(name+" "+err.Error()))
		return
	}
	respondTaskTriggerPage(c, http.StatusOK, "已触发", task.Name)
}

// respondTaskTriggerPage 回一段不依赖任何外部资源的极简 HTML。
// 故意不引面板前端：企业微信内置浏览器里加载整个 SPA 只为了显示一行字太慢，
// 而且这条路由是免鉴权的，不该把前端资源也挂上去。
func respondTaskTriggerPage(c *gin.Context, status int, title, detail string) {
	// 这一页随请求变化且带触发结果，任何一层缓存都不该留它。
	c.Header("Cache-Control", "no-store, no-cache, must-revalidate")
	c.Header("X-Content-Type-Options", "nosniff")
	// title / detail 里会带任务名（用户可控），必须转义，否则任务名里写一段 script 就注入了。
	page := "<!DOCTYPE html><html lang=\"zh-CN\"><head><meta charset=\"utf-8\">" +
		"<meta name=\"viewport\" content=\"width=device-width,initial-scale=1\">" +
		"<title>" + html.EscapeString(title) + "</title></head>" +
		"<body style=\"margin:0;display:flex;align-items:center;justify-content:center;min-height:100vh;" +
		"font-family:-apple-system,BlinkMacSystemFont,'PingFang SC','Microsoft YaHei',sans-serif;background:#f5f7fa;color:#303133\">" +
		"<div style=\"text-align:center;padding:24px\">" +
		"<div style=\"font-size:20px;font-weight:600;margin-bottom:8px\">" + html.EscapeString(title) + "</div>" +
		"<div style=\"font-size:14px;color:#909399;word-break:break-all\">" + html.EscapeString(detail) + "</div>" +
		"</div></body></html>"
	c.Data(status, "text/html; charset=utf-8", []byte(page))
}

// buildPanelAbsoluteURL 按当前请求推导面板对外的绝对地址。
// 面板没有「站点根地址」这项配置，只能从请求本身推：反代场景优先认 X-Forwarded-Proto。
func buildPanelAbsoluteURL(c *gin.Context, relative string) string {
	scheme := "http"
	if c.Request.TLS != nil {
		scheme = "https"
	}
	if forwarded := strings.TrimSpace(c.GetHeader("X-Forwarded-Proto")); forwarded != "" {
		// 多级反代会拼成 "https, http"，取第一段（最靠近客户端的那一跳）。
		if first := strings.TrimSpace(strings.Split(forwarded, ",")[0]); first != "" {
			scheme = first
		}
	}
	host := strings.TrimSpace(c.Request.Host)
	if host == "" {
		// Host 都没有时只能回相对路径，让调用方自己拼；总好过拼出一个 "http:///xxx"。
		return relative
	}
	return scheme + "://" + host + relative
}

func (h *OpenAPIHandler) RegisterRoutes(r *gin.RouterGroup) {
	openapi := r.Group("/open-api")
	{
		// 公开入口给请求体封顶（64KB 远超 app_key + app_secret 的体积）；失败计数在 handler 里做。
		openapi.POST("/token", middleware.BodyLimit(publicEntryBodyLimit), h.Token)
		// 企业微信菜单触发（issue #145）。与上面的 /token 一样直接挂在无中间件的 openapi 组上：
		// 菜单链接带不了 Authorization 头，鉴权靠 handler 内的票据校验 + 两级总开关。
		openapi.GET("/trigger", h.TaskTrigger)

		mgmt := openapi.Group("", middleware.JWTAuth(), middleware.RequireAdmin())
		{
			mgmt.GET("/apps", h.List)
			mgmt.POST("/apps", h.Create)
			mgmt.PUT("/apps/:id", h.Update)
			mgmt.DELETE("/apps/:id", h.Delete)
			mgmt.PUT("/apps/:id/enable", h.Enable)
			mgmt.PUT("/apps/:id/disable", h.Disable)
			mgmt.PUT("/apps/:id/reset-secret", h.ResetSecret)
			mgmt.POST("/apps/:id/view-secret", h.ViewSecret)
			mgmt.GET("/apps/:id/logs", h.CallLogs)
			mgmt.POST("/apps/:id/task-trigger-link", h.IssueTaskTriggerLink)
			// 一键作废：代次 +1，所有已发出去的链接立刻验不过。
			// 静态段 task-trigger-links 与同层的 /apps/:id 共存，形态同 openapi 组里的 /token。
			mgmt.POST("/task-trigger-links/revoke", h.RevokeTaskTriggerLinks)
		}
	}
}
