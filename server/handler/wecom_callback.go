package handler

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"daidai-panel/database"
	"daidai-panel/middleware"
	"daidai-panel/model"
	"daidai-panel/pkg/response"
	"daidai-panel/pkg/wxcrypt"

	"github.com/gin-gonic/gin"
)

// WecomCallbackHandler 是「企业微信自建应用拉起面板任务」的回调入口（issue #145，v3.3.2）：
// GET /api/v1/wecom/callback/:id（企业微信后台保存配置时的 URL 验签）
// POST /api/v1/wecom/callback/:id（用户在应用里回复文字 / 点菜单时的消息投递）。
//
// 形态整套照抄 handler/mcp.go，不另发明一种鉴权：
//
//	总开关 wecom_trigger_enabled → handler 内验签（wxcrypt）→ engineDispatcher 回放 /api/v1 接口
//
// 这不是在开新口子：POST /api/v1/mcp 早就是同形态的公开路由，且同样能跑任务
// （mcptools 的 run_task 最终也是回放 PUT /tasks/:id/run）。安全基线的四件套一件不少：
// 默认关 + 每请求现读开关 + 凭据强校验 + 全局 CORS 兜底。
//
// Origin 校验不在这里重写：router.Setup 在所有路由之前挂了全局 CORS 中间件。
// 企业微信服务器发来的是 server-to-server 请求、不带 Origin，中间件直接放行；
// 反过来，浏览器发起的跨站请求带着 Origin，会在进入本 handler 之前就被回 403。
type WecomCallbackHandler struct {
	engine http.Handler
}

// NewWecomCallbackHandler 需要拿到 engine 本身：触发任务是在请求期回放到面板自己的接口上的。
func NewWecomCallbackHandler(engine http.Handler) *WecomCallbackHandler {
	return &WecomCallbackHandler{engine: engine}
}

func (h *WecomCallbackHandler) RegisterRoutes(r *gin.RouterGroup) {
	r.GET("/wecom/callback/:id", h.Verify)
	// 公网免鉴权入口：声明长度超过 1MB 直接 413，连 body 都不收。
	// Receive 里自己还有一道 64KB 的 LimitReader，这里是更外层、不读 body 就能拒绝的那一道。
	r.POST("/wecom/callback/:id", middleware.BodyLimit(wecomCallbackRequestBodyLimit), h.Receive)

	// 接入配置本身的增删改查。这一组是普通的管理接口（登录 + 管理员），和上面两条公开路由
	// 只是共用一个 /wecom 前缀，没有任何关系 —— 别看见 /wecom 就以为整段都是免鉴权的。
	// 静态段 triggers 与同层的 callback 共存，各自的 :id 在不同子树里，不冲突。
	triggers := r.Group("/wecom/triggers", middleware.JWTAuth(), middleware.RequireAdmin())
	{
		triggers.GET("", h.ListTriggers)
		triggers.POST("", h.CreateTrigger)
		triggers.PUT("/:id", h.UpdateTrigger)
		triggers.DELETE("/:id", h.DeleteTrigger)
	}
}

// ---------------------------------------------------------------------------
// 接入配置管理（管理员）
// ---------------------------------------------------------------------------

// ListTriggers 列出全部接入配置。Token 与 EncodingAESKey 不下发（model 上是 json:"-"，
// ToDict 也没带它们）—— 这两项泄漏任意一个都等于把公网入口交出去，只允许写入不允许读回。
func (h *WecomCallbackHandler) ListTriggers(c *gin.Context) {
	var triggers []model.WecomTrigger
	database.DB.Order("id ASC").Find(&triggers)

	data := make([]map[string]interface{}, 0, len(triggers))
	for i := range triggers {
		item := triggers[i].ToDict()
		// 回调地址由服务端拼好，免得管理员自己拼错前缀 —— 填错企业微信后台是配不通的。
		item["callback_path"] = wecomCallbackPath(c, triggers[i].ID)
		data = append(data, item)
	}
	response.Success(c, gin.H{"data": data})
}

func (h *WecomCallbackHandler) CreateTrigger(c *gin.Context) {
	var req struct {
		Name           string `json:"name" binding:"required"`
		CorpID         string `json:"corp_id" binding:"required"`
		AgentID        string `json:"agent_id"`
		CallbackToken  string `json:"callback_token" binding:"required"`
		EncodingAESKey string `json:"encoding_aes_key" binding:"required"`
		OpenAppID      uint   `json:"open_app_id" binding:"required"`
		TaskWhitelist  string `json:"task_whitelist"`
		Enabled        *bool  `json:"enabled"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "请求参数错误")
		return
	}
	if message, ok := validateWecomTriggerInput(req.EncodingAESKey, req.OpenAppID); !ok {
		response.BadRequest(c, message)
		return
	}

	trigger := model.WecomTrigger{
		Name:           strings.TrimSpace(req.Name),
		CorpID:         strings.TrimSpace(req.CorpID),
		AgentID:        strings.TrimSpace(req.AgentID),
		CallbackToken:  strings.TrimSpace(req.CallbackToken),
		EncodingAESKey: strings.TrimSpace(req.EncodingAESKey),
		OpenAppID:      req.OpenAppID,
		TaskWhitelist:  strings.TrimSpace(req.TaskWhitelist),
		Enabled:        true,
	}
	if req.Enabled != nil {
		trigger.Enabled = *req.Enabled
	}
	if err := database.DB.Create(&trigger).Error; err != nil {
		response.BadRequest(c, "创建失败")
		return
	}

	item := trigger.ToDict()
	item["callback_path"] = wecomCallbackPath(c, trigger.ID)
	response.Created(c, gin.H{"message": "创建成功", "data": item})
}

// UpdateTrigger 按键更新：请求里没出现的键一概不动已有值（与 PUT /notifications/:id 同一口径，
// 独立发版的 APP 不带某个字段时不会把它清掉）。
//
// 两项凭据额外加一条：传了但是空串一律当「不改」。前端的密码框回显不出原值，
// 保存时带的就是空串，按字面写回去会把配置改坏且没有任何提示。
func (h *WecomCallbackHandler) UpdateTrigger(c *gin.Context) {
	id, _ := strconv.ParseUint(c.Param("id"), 10, 32)

	var trigger model.WecomTrigger
	if err := database.DB.First(&trigger, id).Error; err != nil {
		response.NotFound(c, "企业微信接入配置不存在")
		return
	}

	var req map[string]interface{}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "请求参数错误")
		return
	}

	updates := map[string]interface{}{}
	for _, key := range []string{"name", "corp_id", "agent_id", "callback_token", "encoding_aes_key", "task_whitelist"} {
		raw, exists := req[key]
		if !exists || raw == nil {
			continue
		}
		value, ok := raw.(string)
		if !ok {
			response.BadRequest(c, key+" 必须是字符串")
			return
		}
		value = strings.TrimSpace(value)
		if value == "" && (key == "callback_token" || key == "encoding_aes_key") {
			continue
		}
		updates[key] = value
	}
	if raw, exists := req["open_app_id"]; exists && raw != nil {
		// JSON 数字解出来是 float64，这里只接受它，别的类型直接 400。
		value, ok := raw.(float64)
		if !ok || value <= 0 {
			response.BadRequest(c, "open_app_id 必须是正整数")
			return
		}
		updates["open_app_id"] = uint(value)
	}
	if raw, exists := req["enabled"]; exists && raw != nil {
		value, ok := raw.(bool)
		if !ok {
			response.BadRequest(c, "enabled 必须是布尔值")
			return
		}
		updates["enabled"] = value
	}

	// 校验拿更新后的值来做：没传的字段沿用库里的现值。
	aesKey := trigger.EncodingAESKey
	if value, exists := updates["encoding_aes_key"]; exists {
		aesKey = value.(string)
	}
	appID := trigger.OpenAppID
	if value, exists := updates["open_app_id"]; exists {
		appID = value.(uint)
	}
	if message, ok := validateWecomTriggerInput(aesKey, appID); !ok {
		response.BadRequest(c, message)
		return
	}

	if len(updates) > 0 {
		if err := database.DB.Model(&trigger).Updates(updates).Error; err != nil {
			response.BadRequest(c, "更新失败")
			return
		}
	}

	database.DB.First(&trigger, id)
	item := trigger.ToDict()
	item["callback_path"] = wecomCallbackPath(c, trigger.ID)
	response.Success(c, gin.H{"message": "更新成功", "data": item})
}

func (h *WecomCallbackHandler) DeleteTrigger(c *gin.Context) {
	id, _ := strconv.ParseUint(c.Param("id"), 10, 32)
	database.DB.Where("id = ?", id).Delete(&model.WecomTrigger{})
	response.Success(c, gin.H{"message": "删除成功"})
}

// validateWecomTriggerInput 把「配错了要等企业微信那边验签失败才发现」的两件事提前到保存时报错。
func validateWecomTriggerInput(encodingAESKey string, openAppID uint) (string, bool) {
	// 企业微信后台给的 EncodingAESKey 固定 43 位，长度不对根本解不出密钥。
	if len([]rune(strings.TrimSpace(encodingAESKey))) != 43 {
		return "EncodingAESKey 必须是企业微信后台给出的 43 位字符串", false
	}
	var app model.OpenApp
	if database.DB.First(&app, openAppID).Error != nil {
		return "绑定的开放 API 应用不存在", false
	}
	// 没有 tasks 权限的应用铸出来的令牌会在 OpenAPIAccess 那一关被 403，
	// 表现是「指令发了但任务没跑、界面上什么都看不到」，所以在这里就拦掉。
	// 判定口径照抄 middleware 的 appScopeAllowed（逗号切分、去空格、认 `*`），
	// 那个函数没导出，两边分叉会出现「这里放行、回放时 403」的假通过。
	allowed := false
	for _, item := range strings.Split(app.Scopes, ",") {
		if item = strings.TrimSpace(item); item == "*" || item == "tasks" {
			allowed = true
			break
		}
	}
	if !allowed {
		return "绑定的开放 API 应用需要勾选「tasks」权限范围", false
	}
	return "", true
}

// wecomCallbackPath 按当前请求的前缀拼出回调路径，/api 与 /api/v1 两套都能自动对上。
func wecomCallbackPath(c *gin.Context, triggerID uint) string {
	path := c.Request.URL.EscapedPath()
	if index := strings.LastIndex(path, "/wecom/"); index >= 0 {
		return fmt.Sprintf("%s/wecom/callback/%d", path[:index], triggerID)
	}
	return fmt.Sprintf("/api/v1/wecom/callback/%d", triggerID)
}

const (
	// wecomDispatchTokenTTL 是回放接口时现铸的应用令牌有效期。这枚令牌从不下发给任何人，
	// 只在本次 HTTP 请求内部用一下；给 2 分钟纯粹是留点余量，入队本身是毫秒级的。
	wecomDispatchTokenTTL = 2 * time.Minute

	// wecomMaxCallbackBodySize 给请求体封顶。企业微信的消息包只有几 KB，
	// 而这是一条公网免鉴权路由，不限长就等于给「发个 1GB 的 body 把内存打满」留口子。
	wecomMaxCallbackBodySize = 64 << 10

	// wecomCallbackRequestBodyLimit 是路由层的请求体上限（middleware.BodyLimit）。
	wecomCallbackRequestBodyLimit = 1 << 20

	// wecomCallbackMaxSkewSeconds：回调 URL 上的 timestamp 与服务器时间最多差这么多秒（安全审计 M-2）。
	// 签名只覆盖 token+timestamp+nonce+encrypt，不卡时间的话，抓到的一份合法报文任何时候都能重放。
	wecomCallbackMaxSkewSeconds = 300

	// wecomReplayTTL 是去重记录的保留时长。要盖住整个时间窗（前后各 300 秒 = 10 分钟），
	// 否则一份报文过了去重期、却还在时间窗内，就又能重放一次。
	wecomReplayTTL = 10 * time.Minute

	// wecomReplayCacheLimit 是去重表的条目上限，防止内存无限增长。
	// 能写进这张表的都是验签通过的报文，正常企业微信流量远到不了这个量。
	wecomReplayCacheLimit = 4096
)

// 回调去重表：包级共享（/api 与 /api/v1 两套前缀、所有接入配置共用一份），键里带接入配置 ID 互不干扰。
// 只存在内存里，重启后清空；重启前后的重放仍有时间窗兜底。
var (
	wecomReplayMu   sync.Mutex
	wecomReplaySeen = map[string]time.Time{}
)

// wecomSeenRecently 判断 key 在 TTL 内是否已经出现过；没出现过就顺手记下并返回 false。
// 「查」和「记」必须在同一把锁里：分开做的话，两个并发的重放请求会同时判成「第一次」，一起跑任务。
func wecomSeenRecently(key string) bool {
	now := time.Now()
	wecomReplayMu.Lock()
	defer wecomReplayMu.Unlock()

	if seenAt, ok := wecomReplaySeen[key]; ok && now.Sub(seenAt) <= wecomReplayTTL {
		return true
	}

	// 不起后台清理协程（与 middleware.FailureLimiter 同一做法）：条目多起来时顺手扫掉过期的。
	if len(wecomReplaySeen) >= wecomReplayCacheLimit {
		for k, seenAt := range wecomReplaySeen {
			if now.Sub(seenAt) > wecomReplayTTL {
				delete(wecomReplaySeen, k)
			}
		}
		// 扫完还是满的（10 分钟内真有这么多条），只能挤掉最老的一条给新记录腾位置：
		// 宁可让最老那条失去去重保护，也不能让表无限长大。
		if len(wecomReplaySeen) >= wecomReplayCacheLimit {
			oldestKey := ""
			var oldestAt time.Time
			for k, seenAt := range wecomReplaySeen {
				if oldestKey == "" || seenAt.Before(oldestAt) {
					oldestKey, oldestAt = k, seenAt
				}
			}
			delete(wecomReplaySeen, oldestKey)
		}
	}

	wecomReplaySeen[key] = now
	return false
}

// wecomEncryptedEnvelope 是企业微信 POST 过来的外层信封。
// 除了 Encrypt 之外的字段都不可信（没参与签名），只用来写日志。
type wecomEncryptedEnvelope struct {
	XMLName    xml.Name `xml:"xml"`
	ToUserName string   `xml:"ToUserName"`
	AgentID    string   `xml:"AgentID"`
	Encrypt    string   `xml:"Encrypt"`
}

// wecomPlainMessage 是解密后的业务报文。文字消息读 Content，菜单点击读 EventKey。
// encoding/xml 会自动把 <![CDATA[...]]> 解出来，不用手工剥。
type wecomPlainMessage struct {
	XMLName      xml.Name `xml:"xml"`
	ToUserName   string   `xml:"ToUserName"`
	FromUserName string   `xml:"FromUserName"`
	MsgType      string   `xml:"MsgType"`
	Content      string   `xml:"Content"`
	Event        string   `xml:"Event"`
	EventKey     string   `xml:"EventKey"`
	AgentID      string   `xml:"AgentID"`
	// MsgId 只有普通消息才有（事件没有）。企业微信 5 秒没收到响应会换一个 nonce 把同一条消息重发，
	// 外层报文指纹因此对不上，只能靠它认出「这是同一条」。
	MsgID string `xml:"MsgId"`
}

// Verify 处理企业微信后台保存「接收消息服务器配置」时发来的那一次 GET 验签。
// 验过之后必须把解密出来的 echostr 原样回成纯文本 —— 包成 JSON 企业微信是不认的。
func (h *WecomCallbackHandler) Verify(c *gin.Context) {
	trigger, ok := h.loadTrigger(c)
	if !ok {
		return
	}

	echo, err := wxcrypt.VerifyURL(
		trigger.CallbackToken,
		trigger.EncodingAESKey,
		trigger.CorpID,
		c.Query("msg_signature"),
		c.Query("timestamp"),
		c.Query("nonce"),
		c.Query("echostr"),
	)
	if err != nil {
		// 失败原因只写日志不写响应体：对外多一种可区分的失败原因，就多一条给人探测的信息。
		log.Printf("[企业微信触发] 接入配置 %d(%s) 的 URL 验签失败: %v", trigger.ID, trigger.Name, err)
		response.Unauthorized(c, "企业微信回调验签失败")
		return
	}

	c.Data(http.StatusOK, "text/plain; charset=utf-8", []byte(echo))
}

// Receive 处理企业微信投递过来的消息。
//
// 回包口径：验签之前的失败按真实状态码回（401/403/404），这样管理员在企业微信后台配错时
// 能立刻看到；验签之后一律回 200 + 空串。后者是刻意的 —— 企业微信要求 5 秒内响应，
// 非 200 会被它当失败重试三次，而重试意味着同一条「运行 xx」指令被执行三遍。
// 指令的执行结果走现有的 wecom_app 通知渠道异步推回，不占这次响应。
// 唯一的例外是时间戳超出时间窗（v3.4.4，安全审计 M-2）：那是重放或服务器时间不准，回 401 让管理员看得见。
func (h *WecomCallbackHandler) Receive(c *gin.Context) {
	trigger, ok := h.loadTrigger(c)
	if !ok {
		return
	}

	body, err := io.ReadAll(io.LimitReader(c.Request.Body, wecomMaxCallbackBodySize))
	if err != nil {
		response.BadRequest(c, "读取回调内容失败")
		return
	}

	var envelope wecomEncryptedEnvelope
	if err := xml.Unmarshal(body, &envelope); err != nil || strings.TrimSpace(envelope.Encrypt) == "" {
		response.BadRequest(c, "回调内容不是合法的企业微信消息")
		return
	}

	if !wxcrypt.VerifySignature(
		trigger.CallbackToken,
		c.Query("timestamp"),
		c.Query("nonce"),
		envelope.Encrypt,
		c.Query("msg_signature"),
	) {
		log.Printf("[企业微信触发] 接入配置 %d(%s) 收到签名不匹配的消息，已丢弃", trigger.ID, trigger.Name)
		response.Unauthorized(c, "企业微信回调验签失败")
		return
	}

	// 防重放第一道：时间窗。timestamp 参与了签名，改不了，所以过期的报文只可能是旧报文被拿来重放。
	// 这里回 401 而不是 200：真是服务器时间不准时，管理员能在企业微信后台看到失败，而不是指令静默没跑。
	timestamp := strings.TrimSpace(c.Query("timestamp"))
	sentAt, parseErr := strconv.ParseInt(timestamp, 10, 64)
	if parseErr != nil {
		log.Printf("[企业微信触发] 接入配置 %d(%s) 收到的消息时间戳无法解析（%q），已丢弃", trigger.ID, trigger.Name, timestamp)
		response.Unauthorized(c, "企业微信回调已过期")
		return
	}
	if skew := time.Now().Unix() - sentAt; skew > wecomCallbackMaxSkewSeconds || skew < -wecomCallbackMaxSkewSeconds {
		log.Printf("[企业微信触发] 接入配置 %d(%s) 收到的消息时间戳与服务器时间相差 %d 秒（上限 %d 秒），按重放丢弃；若正常消息也被拒，请检查服务器时间是否准确",
			trigger.ID, trigger.Name, skew, wecomCallbackMaxSkewSeconds)
		response.Unauthorized(c, "企业微信回调已过期")
		return
	}

	plain, err := wxcrypt.Decrypt(trigger.EncodingAESKey, trigger.CorpID, envelope.Encrypt)
	if err != nil {
		// 这里最常见的是 ErrReceiveIDMismatch：消息能解开但不是发给本企业的。
		// 那正是「别人拿自己企业的凭据来敲」的样子，必须拒掉，日志里留一条便于排查。
		log.Printf("[企业微信触发] 接入配置 %d(%s) 的消息解密失败: %v", trigger.ID, trigger.Name, err)
		response.Unauthorized(c, "企业微信回调解密失败")
		return
	}

	// 防重放第二道：时间窗内按整份报文的指纹去重。只卡时间窗的话，窗口内仍能原样重放。
	// 放在解密之后才记：解不开的报文本来就回 401，不该占去重表，也不该在重放时变成 200。
	// 见过的报文回 200 空串：它可能是企业微信自己的重试，回非 200 只会招来更多重试。
	envelopeSum := sha256.Sum256([]byte(fmt.Sprintf("%d|%s|%s|%s", trigger.ID, timestamp, c.Query("nonce"), envelope.Encrypt)))
	if wecomSeenRecently("envelope|" + hex.EncodeToString(envelopeSum[:])) {
		log.Printf("[企业微信触发] 接入配置 %d(%s) 收到重复的回调报文，已忽略", trigger.ID, trigger.Name)
		replyWecomEmpty(c)
		return
	}

	var message wecomPlainMessage
	if err := xml.Unmarshal(plain, &message); err != nil {
		log.Printf("[企业微信触发] 接入配置 %d(%s) 的消息体解析失败: %v", trigger.ID, trigger.Name, err)
		replyWecomEmpty(c)
		return
	}

	// 同一条消息被企业微信换 nonce 重发时，外层指纹不同，靠 MsgId 认出来；事件消息没有 MsgId，跳过这一道。
	if msgID := strings.TrimSpace(message.MsgID); msgID != "" && wecomSeenRecently(fmt.Sprintf("msg|%d|%s", trigger.ID, msgID)) {
		log.Printf("[企业微信触发] 接入配置 %d(%s) 收到重复的消息（MsgId=%s），已忽略", trigger.ID, trigger.Name, msgID)
		replyWecomEmpty(c)
		return
	}

	h.handleCommand(c, trigger, &message)
	replyWecomEmpty(c)
}

// handleCommand 从消息里解出指令并触发执行。
// 它一律不写响应体：调用方随后统一回空串，这里只负责「做事 + 留日志」。
func (h *WecomCallbackHandler) handleCommand(c *gin.Context, trigger *model.WecomTrigger, message *wecomPlainMessage) {
	// 文字消息读 Content，菜单点击读 EventKey。其它消息类型（图片、位置、关注事件等）直接忽略。
	text := ""
	switch strings.ToLower(strings.TrimSpace(message.MsgType)) {
	case "text":
		text = message.Content
	case "event":
		text = message.EventKey
	default:
		return
	}

	taskID, taskName, ok := parseWecomRunCommand(text)
	if !ok {
		// 不认识的内容不回任何提示：企业微信应用里用户可能只是随便说了句话，
		// 每句都回一条「指令格式错误」很吵，而且会把面板的存在暴露给误触的人。
		return
	}

	if !model.GetRegisteredConfigBool(model.WecomTriggerAllowRunConfigKey) {
		log.Printf("[企业微信触发] 接入配置 %d(%s) 收到运行指令，但「允许企业微信触发执行」未开启，已忽略", trigger.ID, trigger.Name)
		return
	}
	if !trigger.AllowsTask(taskID, taskName) {
		log.Printf("[企业微信触发] 接入配置 %d(%s) 收到的目标不在任务白名单内，已忽略：%s", trigger.ID, trigger.Name, describeWecomTarget(taskID, taskName))
		return
	}

	status, body, err := h.dispatchRun(c, trigger, taskID, taskName)
	if err != nil {
		log.Printf("[企业微信触发] 接入配置 %d(%s) 回放运行接口失败（%s）: %v", trigger.ID, trigger.Name, describeWecomTarget(taskID, taskName), err)
		return
	}
	log.Printf("[企业微信触发] 接入配置 %d(%s) 由 %s 触发 %s，接口返回 %d: %s",
		trigger.ID, trigger.Name, message.FromUserName, describeWecomTarget(taskID, taskName), status, strings.TrimSpace(string(body)))
}

// dispatchRun 用接入配置绑定的开放 API 应用现铸一枚短期令牌，在进程内回放 /api/v1 的运行接口。
//
// 刻意不直调 service.GetSchedulerV2().RunNow()：直调会绕开 OpenAPIAccess 的 scope 校验、
// 调用限流与 ApiCallLog 审计 —— 那三样正是「公网入口能触发执行」这件事的可追溯性来源。
// 回放走的是和开放 API 客户端一模一样的路径（JWTAuth → OpenAPIAccess → RequireRole），
// 这条企业微信入口因此不比现有开放 API 多任何权限。
func (h *WecomCallbackHandler) dispatchRun(c *gin.Context, trigger *model.WecomTrigger, taskID uint, taskName string) (int, []byte, error) {
	var app model.OpenApp
	if trigger.OpenAppID == 0 {
		return 0, nil, fmt.Errorf("接入配置未绑定开放 API 应用")
	}
	if err := database.DB.First(&app, trigger.OpenAppID).Error; err != nil {
		return 0, nil, fmt.Errorf("绑定的开放 API 应用不存在")
	}
	if !app.Enabled {
		return 0, nil, fmt.Errorf("绑定的开放 API 应用已被禁用")
	}

	// 与 POST /open-api/token 签发的应用令牌同一形态（username=app:<key>，role=app:<scopes>），
	// 回放时照常经过 JWTAuth + OpenAPIAccess，scope、限流、审计都记在这个应用名下。
	token, err := middleware.GenerateTemporaryAccessToken("app:"+app.AppKey, "app:"+app.Scopes, wecomDispatchTokenTTL)
	if err != nil {
		return 0, nil, fmt.Errorf("生成临时令牌失败: %w", err)
	}

	dispatcher := newEngineDispatcher(h.engine, c.Request, "Bearer "+token)
	if taskID > 0 {
		return dispatcher.Do(c.Request.Context(), http.MethodPut, fmt.Sprintf("/tasks/%d/run", taskID), nil, nil)
	}
	return dispatcher.Do(c.Request.Context(), http.MethodPost, "/tasks/run-by-name", nil, map[string]string{"name": taskName})
}

// loadTrigger 走完「总开关 → 取接入配置 → 配置本身是否启用」这三关。
// 返回 false 时响应已经写好，调用方直接 return。
func (h *WecomCallbackHandler) loadTrigger(c *gin.Context) (*model.WecomTrigger, bool) {
	// 每次请求现读，管理员在设置页关掉之后立即生效，不需要重启（与 mcp.go 同口径）。
	if !model.GetRegisteredConfigBool(model.WecomTriggerEnabledConfigKey) {
		response.Forbidden(c, "企业微信触发未开启，请管理员在「系统设置 → 企业微信触发」中启用")
		return nil, false
	}

	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil || id == 0 {
		response.NotFound(c, "企业微信接入配置不存在")
		return nil, false
	}

	var trigger model.WecomTrigger
	if err := database.DB.First(&trigger, id).Error; err != nil {
		response.NotFound(c, "企业微信接入配置不存在")
		return nil, false
	}
	if !trigger.Enabled {
		response.Forbidden(c, "该企业微信接入配置已被禁用")
		return nil, false
	}
	// 凭据缺一不可：少了任何一项都没法验签，此时绝不能退化成「不验」。
	if strings.TrimSpace(trigger.CorpID) == "" ||
		strings.TrimSpace(trigger.CallbackToken) == "" ||
		strings.TrimSpace(trigger.EncodingAESKey) == "" {
		response.Forbidden(c, "该企业微信接入配置缺少 CorpID / Token / EncodingAESKey")
		return nil, false
	}
	return &trigger, true
}

// replyWecomEmpty 回一个 200 空串，表示「收到了，但不需要被动回复」。
// 这是企业微信认的写法，也是唯一能保证 5 秒内响应的写法。
func replyWecomEmpty(c *gin.Context) {
	c.Data(http.StatusOK, "text/plain; charset=utf-8", nil)
}

// parseWecomRunCommand 解析运行指令，支持这四种写法：
//
//	运行 签到任务 / 运行签到任务 / run 12 / run 签到任务
//
// 返回 (taskID, taskName, ok)：taskID > 0 表示按 ID 触发，否则按名称触发，两者只会有一个有值。
func parseWecomRunCommand(text string) (uint, string, bool) {
	// 手机输入法默认打出来的是全角空格，先归一成半角，否则「运行　签到」会连在一起当成任务名。
	normalized := strings.TrimSpace(strings.ReplaceAll(text, "　", " "))
	if normalized == "" {
		return 0, "", false
	}

	var rest string
	switch {
	case strings.HasPrefix(normalized, "运行"):
		// 中文指令允许不带空格：「运行签到任务」与「运行 签到任务」都认。
		rest = strings.TrimSpace(strings.TrimPrefix(normalized, "运行"))
	case len(normalized) >= 3 && strings.EqualFold(normalized[:3], "run") &&
		(len(normalized) == 3 || normalized[3] == ' '):
		// 英文指令必须用空格分隔，否则 runner / running 这类普通词会被误当成指令。
		rest = strings.TrimSpace(normalized[3:])
	default:
		return 0, "", false
	}
	if rest == "" {
		return 0, "", false
	}

	// 纯数字一律当任务 ID。任务名恰好是一串数字的情况极少，真遇到就让用户改用 ID 触发，
	// 反过来（把「run 12」当成找名字叫 12 的任务）踩中的概率大得多。
	if id, err := strconv.ParseUint(rest, 10, 32); err == nil && id > 0 {
		return uint(id), "", true
	}
	return 0, rest, true
}

func describeWecomTarget(taskID uint, taskName string) string {
	if taskID > 0 {
		return fmt.Sprintf("任务 #%d", taskID)
	}
	return fmt.Sprintf("任务「%s」", taskName)
}
