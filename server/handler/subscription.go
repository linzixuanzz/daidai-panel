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
	panelcron "daidai-panel/pkg/cron"
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

func normalizeSubscriptionAuthInput(authType string, sshKeyID *uint, authToken string) (string, *uint, string, error) {
	normalizedType := model.NormalizeSubscriptionAuthType(authType)
	trimmedToken := strings.TrimSpace(authToken)

	switch normalizedType {
	case "":
		return "", nil, "", nil
	case model.SubAuthTypeSSH:
		if sshKeyID == nil || *sshKeyID == 0 {
			return "", nil, "", fmt.Errorf("已选择 SSH 鉴权，请指定 SSH 密钥")
		}
		return normalizedType, sshKeyID, "", nil
	case model.SubAuthTypeToken:
		if trimmedToken == "" {
			return "", nil, "", fmt.Errorf("已选择 Token 鉴权，请填写访问令牌")
		}
		return normalizedType, nil, trimmedToken, nil
	default:
		return "", nil, "", fmt.Errorf("无效的仓库鉴权方式")
	}
}

func (h *SubscriptionHandler) List(c *gin.Context) {
	keyword := c.Query("keyword")
	subType := c.Query("type")
	enabledRaw := c.Query("enabled")
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
	if enabledRaw != "" {
		enabled, err := strconv.ParseBool(enabledRaw)
		if err == nil {
			query = query.Where("enabled = ?", enabled)
		}
	}

	var total int64
	query.Count(&total)

	var subs []model.Subscription
	query.Order("created_at DESC").
		Offset((page - 1) * pageSize).Limit(pageSize).Find(&subs)

	data := make([]map[string]interface{}, len(subs))
	for i, s := range subs {
		data[i] = s.ToDict()
		// 下次拉取时间（issue #163）：只在「启用 + 定时规则非空 + 能解析」时下发，其余情况不带这个键。
		// 口径与订阅调度器 AddOrUpdateJob 完全一致：整串交给 ParseSchedule（5 段 / 6 段都认），
		// 不按行拆分——调度器本身就不拆，多行规则在那里注册失败、根本不会触发，这里也就不该显示时间。
		if s.Enabled && strings.TrimSpace(s.Schedule) != "" {
			if nextTimes := panelcron.NextRunTimes(s.Schedule, 1); len(nextTimes) > 0 {
				data[i]["next_pull_at"] = nextTimes[0]
			}
		}
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
		PreScript      string `json:"pre_script"`
		HookScript     string `json:"hook_script"`
		AutoAddTask    bool   `json:"auto_add_task"`
		AutoDelTask    bool   `json:"auto_del_task"`
		// 自动添加定时任务 / 自动删除失效任务的三态开关，前端只发这两个；
		// 上面那两个布尔字段继续接收只为老客户端不报错。它们不会被原样落库 ——
		// 只发布尔而没发三态时，由 model.ResolveSubscriptionTaskSyncModeInput 当场翻译成 mode，
		// 源列恒写 false（同 force_overwrite 与 overwrite_mode 的并存做法）。
		AutoAddTaskMode string `json:"auto_add_task_mode"`
		AutoDelTaskMode string `json:"auto_del_task_mode"`
		SaveDir        string `json:"save_dir"`
		SubPath        string `json:"sub_path"`
		SSHKeyID       *uint  `json:"ssh_key_id"`
		AuthType       string `json:"auth_type"`
		AuthUsername    string `json:"auth_username"`
		AuthToken      string `json:"auth_token"`
		Alias          string `json:"alias"`
		ForceOverwrite *bool  `json:"force_overwrite"`
		// 覆盖拉取策略三态，前端只发这个；不传或传脏值都会被 Normalize 归到 inherit（跟随全局）。
		OverwriteMode  string `json:"overwrite_mode"`
		// 完整检出：开启后放弃 sparse-checkout，整仓拉取。不传就是 false（走原来的 sparse）。
		FullCheckout   bool   `json:"full_checkout"`
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
	// 白名单 / 黑名单 / 依赖规则里含正则触发字符的片段按正则解析（#129），编译不过就在保存时拦下，
	// 文案点名字段、第几段和 RE2 的报错。拉取时另有兜底（逐条跳过并告警），管的是升级前的存量和青龙备份导入。
	if err := service.ValidateSubscriptionFilterFields(req.Whitelist, req.Blacklist, req.DependOn); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	// 保存目录 / 别名会直接拼进脚本目录下的落盘路径，带 ../ 或绝对路径就能写到脚本目录外，落库前拦下。
	if err := service.ValidateSubscriptionTarget(req.Type, req.URL, req.SaveDir, req.Alias); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	authType, sshKeyID, authToken, err := normalizeSubscriptionAuthInput(req.AuthType, req.SSHKeyID, req.AuthToken)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	// 订阅只做「先查后插」，刻意不给 subscriptions.url 加 DB 唯一索引：
	// 同一个仓库以不同分支 / 不同子目录订阅两次是合法用法，加硬约束会让这些历史数据在升级时被动改名。
	//
	// 判重口径必须覆盖**所有决定这条订阅落到哪个目录**的字段，也就是
	// service.subscriptionSaveDir 的整条取值链：save_dir → alias → URL 末段。
	// 只卡「地址 + 分支 + 子目录」会误伤青龙生态里很常见的一种用法 ——
	// 同一个脚本仓库订阅两次，一条 whitelist=jd_*.js 存进 jd、另一条 whitelist=utils/*.js 存进 libs，
	// 两条的 url/branch/sub_path 逐字相同，升级后第二条会被 400 拒掉且没有任何绕过入口。
	// 单文件订阅（type=file，branch 与 sub_path 恒为空）撞上的概率更高，save_dir 是它唯一的区分维度。
	//
	// 连点创建按钮时这五个字段本来就逐字相同，所以挡重复的能力不受影响。
	// 注意这只是尽力而为的一道闸 —— 两个请求同时进来时仍可能双双查不到再双双插入，
	// 前端的按钮在途锁才是主力，这里兜的是「超时后用户手动再点一次」那一半。
	var duplicateSubCount int64
	database.DB.Model(&model.Subscription{}).
		Where("url = ? AND COALESCE(branch, '') = ? AND COALESCE(sub_path, '') = ? AND COALESCE(save_dir, '') = ? AND COALESCE(alias, '') = ?",
			req.URL, req.Branch, req.SubPath, req.SaveDir, req.Alias).
		Count(&duplicateSubCount)
	if duplicateSubCount > 0 {
		response.BadRequest(c, "相同地址、分支、子目录、保存目录和别名的订阅已存在")
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
		PreScript:      req.PreScript,
		HookScript:     req.HookScript,
		// 刻意不写 AutoAddTask / AutoDelTask：旧布尔列在这里恒落 false（Go 零值 + 列默认值都是 false）。
		// 老客户端只发布尔 true、不发三态时，语义「开就是开、不看全局」由下面的 Resolve 翻译成
		// mode='enabled'，信息一点不丢；而源列一旦能被写成 1，启动回填就会在下次重启把用户
		// 显式选的 inherit 静默提回 enabled（回填的幂等性全靠「源列恒 0」）。
		AutoAddTaskMode: model.ResolveSubscriptionTaskSyncModeInput(req.AutoAddTaskMode, req.AutoAddTask),
		AutoDelTaskMode: model.ResolveSubscriptionTaskSyncModeInput(req.AutoDelTaskMode, req.AutoDelTask),
		Enabled:        true,
		SaveDir:        req.SaveDir,
		SubPath:        req.SubPath,
		SSHKeyID:       sshKeyID,
		AuthType:       authType,
		AuthUsername:    req.AuthUsername,
		AuthToken:      authToken,
		Alias:          req.Alias,
		ForceOverwrite: req.ForceOverwrite,
		OverwriteMode:  model.NormalizeSubscriptionOverwriteMode(req.OverwriteMode),
		FullCheckout:   req.FullCheckout,
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

// validateChangedSubscriptionFilterFields 只校验这次真的改了的过滤字段（值与库里逐字不同才校验）。
// 库里的存量值（升级前保存的、青龙备份导入的）可能本来就不是合法正则，而 App 每次保存都会把
// whitelist / blacklist / depend_on 原样回传；全量校验会让用户连改个名字都被 400 挡住。
// 这类存量值由拉取时的兜底处理：编译不过的片段逐条跳过并告警。
// 非字符串的值（null、数字）不在这里拦，维持 map 更新原有的写库行为。
func validateChangedSubscriptionFilterFields(updates map[string]interface{}, sub *model.Subscription) error {
	current := map[string]string{
		service.SubscriptionFilterFieldWhitelist: sub.Whitelist,
		service.SubscriptionFilterFieldBlacklist: sub.Blacklist,
		service.SubscriptionFilterFieldDependOn:  sub.DependOn,
	}
	for _, key := range []string{
		service.SubscriptionFilterFieldWhitelist,
		service.SubscriptionFilterFieldBlacklist,
		service.SubscriptionFilterFieldDependOn,
	} {
		value, exists := updates[key]
		if !exists {
			continue
		}
		text, ok := value.(string)
		if !ok || text == current[key] {
			continue
		}
		if err := service.ValidateSubscriptionFilterField(key, text); err != nil {
			return err
		}
	}
	return nil
}

// validateChangedSubscriptionTargetFields 校验这次改动后的保存目录 / 别名，口径同 validateChangedSubscriptionFilterFields：
// 只校验真的改了的字段。App 每次保存都会把 save_dir / alias 原样回传，库里的存量值（升级前保存的、青龙备份导入的）
// 若不合规，全量校验会让用户连改个名字都被 400 挡住；存量越界值由拉取时的兜底拦下并在日志里提示修改。
// - 保存目录：改了才校验；
// - 别名：改了，或订阅类型改了（别名在单文件订阅里是文件名、在 git 订阅里是目录名，规则不同）才校验；
// - git 订阅的最终目录（不能是脚本根目录等）：类型、地址、保存目录、别名任一改了才校验。
// 非字符串的值（null、数字）不在这里拦，维持 map 更新原有的写库行为。
func validateChangedSubscriptionTargetFields(updates map[string]interface{}, sub *model.Subscription) error {
	subType, url, saveDir, alias := sub.Type, sub.URL, sub.SaveDir, sub.Alias
	typeChanged, urlChanged, saveDirChanged, aliasChanged := false, false, false, false
	if text, ok := updates["type"].(string); ok && text != sub.Type {
		subType, typeChanged = text, true
	}
	if text, ok := updates["url"].(string); ok && text != sub.URL {
		url, urlChanged = text, true
	}
	if text, ok := updates["save_dir"].(string); ok && text != sub.SaveDir {
		saveDir, saveDirChanged = text, true
	}
	if text, ok := updates["alias"].(string); ok && text != sub.Alias {
		alias, aliasChanged = text, true
	}

	if saveDirChanged {
		if err := service.ValidateSubscriptionSaveDir(saveDir); err != nil {
			return err
		}
	}
	if aliasChanged || typeChanged {
		if err := service.ValidateSubscriptionAlias(subType, alias); err != nil {
			return err
		}
	}
	if typeChanged || urlChanged || saveDirChanged || aliasChanged {
		if err := service.ValidateGitSubscriptionTargetDir(subType, url, saveDir, alias); err != nil {
			return err
		}
	}
	return nil
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
		"depend_on": true, "pre_script": true, "hook_script": true,
		"save_dir": true, "sub_path": true, "ssh_key_id": true, "auth_type": true, "auth_username": true, "auth_token": true, "alias": true, "force_overwrite": true,
		"overwrite_mode": true,
		// 自动添加定时任务 / 自动删除失效任务的三态开关。
		// 旧布尔键 auto_add_task / auto_del_task 刻意**不在**白名单里：map 更新会真写那一列，
		// 而只要库里出现 legacy=1，启动回填就会把用户显式选的 inherit 提回 enabled
		//（老客户端本来也只读这两个字段，ToDict 继续下发就够它们不炸了）。
		"auto_add_task_mode": true, "auto_del_task_mode": true,
		// 完整检出开关。Update 走 map 更新，false 也会被写库（map 更新不会跳过零值），
		// 所以用户在表单里关掉它能正常落库。
		"full_checkout": true,
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

	if err := validateChangedSubscriptionFilterFields(updates, &sub); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	if err := validateChangedSubscriptionTargetFields(updates, &sub); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	// 覆盖拉取策略写库前先归一。Update 收的是 map[string]interface{}，
	// 值可能是任意 JSON 类型（前端误传 null / 数字，或直接调接口塞脏字符串），
	// 一律归到三个合法值之一，非字符串按 inherit（跟随全局）处理，绝不让脏值落库。
	if value, exists := updates["overwrite_mode"]; exists {
		text, _ := value.(string)
		updates["overwrite_mode"] = model.NormalizeSubscriptionOverwriteMode(text)
	}

	// 自动添加定时任务 / 自动删除失效任务的三态开关同理：非字符串或脏值一律归 inherit
	// （跟随全局默认），方向安全——最坏结果只是回到「和升级前一样」的行为。
	if value, exists := updates["auto_add_task_mode"]; exists {
		text, _ := value.(string)
		updates["auto_add_task_mode"] = model.NormalizeSubscriptionTaskSyncMode(text)
	}
	if value, exists := updates["auto_del_task_mode"]; exists {
		text, _ := value.(string)
		updates["auto_del_task_mode"] = model.NormalizeSubscriptionTaskSyncMode(text)
	}

	// 完整检出开关同理：JSON 里可能是 null / 数字 / 字符串，直接 map 更新会把脏值塞进
	// bool 列。非布尔一律归 false —— 也就是保持既有的 sparse 检出行为，方向安全
	// （误开成完整检出会让整仓文件落盘，误关只是回到默认行为）。
	if value, exists := updates["full_checkout"]; exists {
		flag, _ := value.(bool)
		updates["full_checkout"] = flag
	}

	if _, hasAuthType := updates["auth_type"]; hasAuthType || updates["ssh_key_id"] != nil || updates["auth_token"] != nil {
		var rawSSHKeyID *uint
		if value, exists := updates["ssh_key_id"]; exists {
			switch typed := value.(type) {
			case nil:
				rawSSHKeyID = nil
			case float64:
				if typed > 0 {
					id := uint(typed)
					rawSSHKeyID = &id
				}
			}
		} else {
			rawSSHKeyID = sub.SSHKeyID
		}

		authType := sub.EffectiveAuthType()
		if value, exists := updates["auth_type"]; exists {
			text, ok := value.(string)
			if !ok {
				response.BadRequest(c, "无效的仓库鉴权方式")
				return
			}
			authType = text
		}

		authToken := sub.AuthToken
		if value, exists := updates["auth_token"]; exists {
			text, ok := value.(string)
			if !ok {
				response.BadRequest(c, "无效的仓库访问令牌")
				return
			}
			if strings.TrimSpace(text) != "" || sub.EffectiveAuthType() != model.SubAuthTypeToken {
				authToken = text
			}
		}

		normalizedType, normalizedSSHKeyID, normalizedToken, err := normalizeSubscriptionAuthInput(authType, rawSSHKeyID, authToken)
		if err != nil {
			response.BadRequest(c, err.Error())
			return
		}
		updates["auth_type"] = normalizedType
		updates["ssh_key_id"] = normalizedSSHKeyID
		updates["auth_token"] = normalizedToken
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
			// 面板关停时请求 ctx 会被取消（main 里的 BaseContext）：先发 done:reconnect 再收流，与实时日志同口径。
			// 网页订阅页把 finished / not_running / closed 以外的值都当「连接中断」，不会一直停在拉取中。
			// 不能发 timeout：APP 的订阅页（v1.0.2 起）把 reconnect 以外的 done 都当成完成，会显示绿色「拉取完成」；
			// reconnect 让各版本 APP 重连一次，面板没起来就显示红色「连接已断开」，与面板被强杀时一样。
			// 客户端自己断开时这次写入只是写失败。
			fmt.Fprintf(c.Writer, "event: done\ndata: reconnect\n\n")
			c.Writer.Flush()
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
	// min=1：binding:"required" 只挡缺字段 / null，挡不住 "ids": []。空列表会生成 IN (NULL)、一条都不删，
	// 却照样回「已删除 0 个订阅」的成功，前端据此提示「批量删除成功」—— 明确回 400 才不会假装删过了。
	var req struct {
		IDs []uint `json:"ids" binding:"required,min=1"`
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
