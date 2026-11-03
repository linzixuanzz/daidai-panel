package handler

import (
	"fmt"
	"log"
	"strconv"
	"strings"

	"daidai-panel/config"
	"daidai-panel/database"
	"daidai-panel/model"
	panelcron "daidai-panel/pkg/cron"
	"daidai-panel/pkg/response"
	"daidai-panel/service"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func normalizeTaskRandomDelaySecondsValue(value interface{}) (*int, error) {
	if value == nil {
		return nil, nil
	}

	switch typed := value.(type) {
	case float64:
		delay := int(typed)
		if float64(delay) != typed {
			return nil, fmt.Errorf("随机延迟最大秒数必须为整数")
		}
		if delay < 0 || delay > 86400 {
			return nil, fmt.Errorf("随机延迟最大秒数需在 0-86400 之间")
		}
		return &delay, nil
	case int:
		if typed < 0 || typed > 86400 {
			return nil, fmt.Errorf("随机延迟最大秒数需在 0-86400 之间")
		}
		delay := typed
		return &delay, nil
	default:
		return nil, fmt.Errorf("随机延迟最大秒数无效")
	}
}

// 超时 / 最大重试次数 / 重试间隔的上限，与网页任务表单（web/src/views/tasks/components/TaskForm.vue）
// 里 el-input-number 的 max 对齐：超时最多 7 天（604800 秒，0 = 不超时）、重试最多 10 次、重试间隔最多 1 小时。
// 以前服务端完全不管，开放 API 能写进负数或天文数字（例如重试一百万次、每次间隔几年）。
const (
	taskTimeoutSecondsMax       = 604800
	taskMaxRetriesMax           = 10
	taskRetryIntervalSecondsMax = 3600
)

// normalizeTaskIntRangeValue 校验超时 / 重试这类整数字段，口径照抄 normalizeTaskRandomDelaySecondsValue：
// 非整数、超出范围直接报错（拒绝而不是夹紧）。Create 传 int，Update 与导入走 JSON、数字一律是 float64。
// null 也拒绝：这几列是 NOT NULL 语义的整数，写进 NULL 之后整条任务读不出来。
func normalizeTaskIntRangeValue(value interface{}, label string, max int) (int, error) {
	var number int
	switch typed := value.(type) {
	case float64:
		number = int(typed)
		if float64(number) != typed {
			return 0, fmt.Errorf("%s必须为整数", label)
		}
	case int:
		number = typed
	default:
		return 0, fmt.Errorf("%s无效", label)
	}
	if number < 0 || number > max {
		return 0, fmt.Errorf("%s需在 0-%d 之间", label, max)
	}
	return number, nil
}

// taskIntRangeFields 是 Update / 导入共用的字段表：JSON 键 → 提示里的中文名与上限。
var taskIntRangeFields = map[string]struct {
	label string
	max   int
}{
	"timeout":        {label: "超时秒数", max: taskTimeoutSecondsMax},
	"max_retries":    {label: "最大重试次数", max: taskMaxRetriesMax},
	"retry_interval": {label: "重试间隔秒数", max: taskRetryIntervalSecondsMax},
}

// normalizeTaskLogRetentionDaysValue 把任务级日志保留天数统一成两态：nil = 跟随全局、>0 = 自定义天数。
// 结构照抄上面的 normalizeTaskRandomDelaySecondsValue：Create 解引用后传的是 int，
// Update 与导入走 JSON、数字一律是 float64，两条路共用这一个函数（issue #144 / v3.3.2）。
//
// 0 与负数刻意归成 nil 而不是报错：前端「跟随全局」那一档提交的是 null，但 el-input-number
// 被清空、旧客户端、导入的旧文件都可能送来 0，把它们当「不自定义」处理用户才存得上。
// 上下限与全局 log_retention_days（system_config_registry.go 里登记的 1-3650）对齐。
func normalizeTaskLogRetentionDaysValue(value interface{}) (*int, error) {
	if value == nil {
		return nil, nil
	}

	switch typed := value.(type) {
	case float64:
		days := int(typed)
		if float64(days) != typed {
			return nil, fmt.Errorf("日志保留天数必须为整数")
		}
		if days <= 0 {
			return nil, nil
		}
		if days > 3650 {
			return nil, fmt.Errorf("日志保留天数需在 1-3650 之间")
		}
		return &days, nil
	case int:
		if typed <= 0 {
			return nil, nil
		}
		if typed > 3650 {
			return nil, fmt.Errorf("日志保留天数需在 1-3650 之间")
		}
		days := typed
		return &days, nil
	default:
		return nil, fmt.Errorf("日志保留天数无效")
	}
}

// normalizeTaskEnvListValue 把 env_groups / env_names 的入参统一成去空、去重的字符串切片（#168）。
// 网页、APP 发字符串数组；开放 API 手写时也兼容逗号拼接的字符串；null 当空列表。
// Create 解出来的是 interface{}，Update、导入走 JSON map，三条路共用这一个函数。
func normalizeTaskEnvListValue(value interface{}, field string) ([]string, error) {
	raw := []string{}
	switch typed := value.(type) {
	case nil:
	case string:
		raw = append(raw, typed)
	case []interface{}:
		for _, item := range typed {
			text, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("%s 只能是字符串数组", field)
			}
			raw = append(raw, text)
		}
	default:
		return nil, fmt.Errorf("%s 只能是字符串数组", field)
	}
	// JoinEnvGroups 按逗号等分隔符拆开、去首尾空白、去空、去重，分组与变量名共用同一套规整
	return model.SplitEnvGroups(model.JoinEnvGroups(raw)), nil
}

// resolveTaskEnvScope 校验并算出要落库的三列（#168）：env_scope 只收 all / selected；
// 变量名必须符合 envNamePattern，不符合时点名是哪个；最终是 all 时两列清空，不留下看不见的旧选择。
func resolveTaskEnvScope(scope string, groups, names []string) (string, string, string, error) {
	for _, name := range names {
		if !envNamePattern.MatchString(name) {
			return "", "", "", fmt.Errorf("变量名 '%s' 格式无效：只能包含字母、数字和下划线，且不能以数字开头", name)
		}
	}
	switch strings.ToLower(strings.TrimSpace(scope)) {
	case model.TaskEnvScopeAll:
		return model.TaskEnvScopeAll, "", "", nil
	case model.TaskEnvScopeSelected:
		return model.TaskEnvScopeSelected, model.JoinEnvGroups(groups), strings.Join(names, ","), nil
	default:
		return "", "", "", fmt.Errorf("env_scope 只能是 all 或 selected")
	}
}

func (h *TaskHandler) Create(c *gin.Context) {
	var req struct {
		Name                   string      `json:"name" binding:"required"`
		Command                string      `json:"command" binding:"required"`
		PythonVersion          string      `json:"python_version"`
		CronExpression         string      `json:"cron_expression"`
		TaskType               string      `json:"task_type"`
		Timeout                *int        `json:"timeout"`
		SuccessExitCodes       *string     `json:"success_exit_codes"`
		RandomDelaySeconds     *int        `json:"random_delay_seconds"`
		LogRetentionDays       *int        `json:"log_retention_days"`
		MaxRetries             *int        `json:"max_retries"`
		RetryInterval          *int        `json:"retry_interval"`
		NotifyOnFailure        *bool       `json:"notify_on_failure"`
		NotifyOnSuccess        *bool       `json:"notify_on_success"`
		NotifyOnAbort          *bool       `json:"notify_on_abort"`
		NotificationChannelID  *uint       `json:"notification_channel_id"`
		Labels                 []string    `json:"labels"`
		DependsOn              *uint       `json:"depends_on"`
		TaskBefore             *string     `json:"task_before"`
		TaskAfter              *string     `json:"task_after"`
		AllowMultipleInstances *bool       `json:"allow_multiple_instances"`
		StopSchedule           *string     `json:"stop_schedule"`
		EnvScope               *string     `json:"env_scope"`
		EnvGroups              interface{} `json:"env_groups"` // 字符串数组，也兼容逗号字符串，见 normalizeTaskEnvListValue
		EnvNames               interface{} `json:"env_names"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "请求参数错误")
		return
	}

	taskType := model.NormalizeTaskType(req.TaskType)
	if taskType == "" {
		response.BadRequest(c, "无效的任务类型")
		return
	}
	if taskType == model.TaskTypeCron {
		req.CronExpression = panelcron.NormalizeExpressions(req.CronExpression)
		if err := panelcron.ValidateExpressions(req.CronExpression); err != nil {
			response.BadRequest(c, err.Error())
			return
		}
	} else {
		req.CronExpression = ""
	}
	pythonVersion, err := service.NormalizePythonVersionStrict(req.PythonVersion)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	if !service.PythonVersionSupportedByCurrentRuntime(pythonVersion) {
		response.BadRequest(c, fmt.Sprintf("当前镜像不支持 Python %s，请切换到对应 Python 版本镜像或 all 镜像", pythonVersion))
		return
	}

	task := model.Task{
		Name:             req.Name,
		Command:          req.Command,
		PythonVersion:    pythonVersion,
		CronExpression:   req.CronExpression,
		TaskType:         taskType,
		Status:           model.TaskStatusEnabled,
		Timeout:          0,
		SuccessExitCodes: model.DefaultSuccessExitCodes,
		RetryInterval:    60,
		NotifyOnFailure:  false,
		EnvScope:         model.TaskEnvScopeAll,
	}

	// 任务级「可用环境变量」（#168）：三项都不带时（旧网页缓存、旧版 APP、开放 API）按默认「全部」。
	// 只带了分组 / 变量名而没带 env_scope 时也按 all 算，两列随之清空——不能凭列表猜用户想「指定」。
	if req.EnvScope != nil || req.EnvGroups != nil || req.EnvNames != nil {
		scope := model.TaskEnvScopeAll
		if req.EnvScope != nil {
			scope = *req.EnvScope
		}
		envGroups, err := normalizeTaskEnvListValue(req.EnvGroups, "env_groups")
		if err != nil {
			response.BadRequest(c, err.Error())
			return
		}
		envNames, err := normalizeTaskEnvListValue(req.EnvNames, "env_names")
		if err != nil {
			response.BadRequest(c, err.Error())
			return
		}
		task.EnvScope, task.EnvGroups, task.EnvNames, err = resolveTaskEnvScope(scope, envGroups, envNames)
		if err != nil {
			response.BadRequest(c, err.Error())
			return
		}
	}

	if req.Timeout != nil {
		timeout, err := normalizeTaskIntRangeValue(*req.Timeout, "超时秒数", taskTimeoutSecondsMax)
		if err != nil {
			response.BadRequest(c, err.Error())
			return
		}
		task.Timeout = timeout
	}
	if req.SuccessExitCodes != nil {
		normalized, err := model.NormalizeSuccessExitCodes(*req.SuccessExitCodes)
		if err != nil {
			response.BadRequest(c, err.Error())
			return
		}
		task.SuccessExitCodes = normalized
	}
	if req.RandomDelaySeconds != nil {
		randomDelayValue, err := normalizeTaskRandomDelaySecondsValue(*req.RandomDelaySeconds)
		if err != nil {
			response.BadRequest(c, err.Error())
			return
		}
		task.RandomDelaySeconds = randomDelayValue
	}
	if req.LogRetentionDays != nil {
		logRetentionValue, err := normalizeTaskLogRetentionDaysValue(*req.LogRetentionDays)
		if err != nil {
			response.BadRequest(c, err.Error())
			return
		}
		task.LogRetentionDays = logRetentionValue
	}
	if req.MaxRetries != nil {
		maxRetries, err := normalizeTaskIntRangeValue(*req.MaxRetries, "最大重试次数", taskMaxRetriesMax)
		if err != nil {
			response.BadRequest(c, err.Error())
			return
		}
		task.MaxRetries = maxRetries
	}
	if req.RetryInterval != nil {
		retryInterval, err := normalizeTaskIntRangeValue(*req.RetryInterval, "重试间隔秒数", taskRetryIntervalSecondsMax)
		if err != nil {
			response.BadRequest(c, err.Error())
			return
		}
		task.RetryInterval = retryInterval
	}
	if req.NotifyOnFailure != nil {
		task.NotifyOnFailure = *req.NotifyOnFailure
	}
	if req.NotifyOnSuccess != nil {
		task.NotifyOnSuccess = *req.NotifyOnSuccess
	}
	if req.NotifyOnAbort != nil {
		task.NotifyOnAbort = *req.NotifyOnAbort
	}
	if req.NotificationChannelID != nil {
		if *req.NotificationChannelID == 0 {
			task.NotificationChannelID = nil
		} else if err := validateTaskNotificationChannelID(req.NotificationChannelID); err != nil {
			response.BadRequest(c, err.Error())
			return
		} else {
			task.NotificationChannelID = req.NotificationChannelID
		}
	}
	if req.Labels != nil {
		task.SetLabelsFromSlice(req.Labels)
	}
	if req.DependsOn != nil {
		task.DependsOn = req.DependsOn
	}
	if req.TaskBefore != nil {
		task.TaskBefore = req.TaskBefore
	}
	if req.TaskAfter != nil {
		task.TaskAfter = req.TaskAfter
	}
	if req.AllowMultipleInstances != nil {
		task.AllowMultipleInstances = *req.AllowMultipleInstances
	}
	if req.StopSchedule != nil {
		task.StopSchedule = *req.StopSchedule
	}
	if err := database.DB.Select("*").Create(&task).Error; err != nil {
		response.InternalError(c, "创建任务失败")
		return
	}

	// 注册调度失败必须留痕：此前这里把 error 整个丢掉，然后无条件回「创建成功」，
	// 用户看到的是一个「已启用」的任务，实际上 cron 里一个触发条目都没有，永远不会自动跑。
	// 今天这条 error 出口恰好不可达（上面的 panelcron.ValidateExpressions 与 AddJob 里的
	// panelcron.ParseSchedule 共用同一个 parser，判定必然一致），但这纯靠两处代码同源维持，
	// 没有任何测试钉住，一旦哪天分叉就会重新变成静默失败。
	// 刻意不改成返回 500：任务已经落库，报错回滚对用户更难解释；用户侧的可见性由任务详情的
	// schedule_hint 承担，这里只负责让运维在面板日志页看得见。
	// 文案里的「失败」会被 service/panel_log.go 的 detectPanelLogLevel 判成 ERROR 级。
	if scheduler := service.GetSchedulerV2(); scheduler != nil {
		if err := scheduler.AddJob(&task); err != nil {
			log.Printf("任务 %d 注册调度失败（它不会自动触发）: %v", task.ID, err)
		}
	}

	response.Created(c, gin.H{
		"message": "创建成功",
		"data":    taskDictWithEnabledSwitch(&task),
	})
}

func (h *TaskHandler) Update(c *gin.Context) {
	taskID, _ := strconv.ParseUint(c.Param("id"), 10, 32)

	var task model.Task
	if err := database.DB.First(&task, taskID).Error; err != nil {
		response.NotFound(c, "任务不存在")
		return
	}

	var req map[string]interface{}
	// 请求体是字面量 null 时 JSON 解码不报错，只是把 req 留成 nil map：以前往下走到给 req 赋值的地方
	// （例如手动 / 开机任务那句 req["cron_expression"] = ""）就 panic，被 gin.Recovery 兜成一个空的 500。
	// 与其它参数错误同口径回 400，什么都不写。
	if err := c.ShouldBindJSON(&req); err != nil || req == nil {
		response.BadRequest(c, "请求参数错误")
		return
	}

	// name / command 在库里都是 NOT NULL：显式传 null 会让下面那条 Updates 整条失败，
	// 同一个请求里的 labels 等其它字段也跟着一起没改，以前这个错误被吞掉、照样回 200「task updated」（#157）。
	// 网页、APP、MCP 都不会这样发，只有开放 API 的调用方可能传 null，所以在写库之前直接回 400，什么都不写。
	// 按固定顺序检查，两个字段同时是 null 时提示稳定落在第一个上。
	// cron_expression 同样是 NOT NULL，但它要看更新后的任务类型，放到下面解析完 task_type 之后再判。
	for _, key := range []string{"name", "command"} {
		if value, exists := req[key]; exists && value == nil {
			response.BadRequest(c, key+" 不能为空")
			return
		}
	}

	if labels, ok := req["labels"].([]interface{}); ok {
		values := make([]string, len(labels))
		for i, label := range labels {
			values[i] = fmt.Sprintf("%v", label)
		}
		req["labels"] = strings.Join(values, ",")
	}

	resolvedTaskType := task.GetTaskType()
	if rawTaskType, exists := req["task_type"]; exists {
		value, ok := rawTaskType.(string)
		if !ok {
			response.BadRequest(c, "无效的任务类型")
			return
		}
		resolvedTaskType = model.NormalizeTaskType(value)
		if resolvedTaskType == "" {
			response.BadRequest(c, "无效的任务类型")
			return
		}
		req["task_type"] = resolvedTaskType
	}

	if resolvedTaskType == model.TaskTypeCron {
		// 只有这次更新之后是常规定时任务（本来就是，或同一个请求里改成 cron）时，cron_expression 显式传 null 才回 400，
		// 理由同上面的 name / command。
		// 手动 / 开机任务（含同一个请求里把 task_type 改成 manual / startup）走下面的 else：
		// 服务端一律把 cron_expression 写成空串，传 null 就是清空，保持改动前回 200 的行为。
		if value, exists := req["cron_expression"]; exists && value == nil {
			response.BadRequest(c, "cron_expression 不能为空")
			return
		}
		cronExpr := task.CronExpression
		if value, ok := req["cron_expression"].(string); ok {
			cronExpr = panelcron.NormalizeExpressions(value)
			req["cron_expression"] = cronExpr
		}
		if err := panelcron.ValidateExpressions(cronExpr); err != nil {
			response.BadRequest(c, err.Error())
			return
		}
	} else {
		req["cron_expression"] = ""
	}
	if rawPythonVersion, exists := req["python_version"]; exists {
		value, ok := rawPythonVersion.(string)
		if !ok {
			response.BadRequest(c, "无效的 Python 版本")
			return
		}
		pythonVersion, err := service.NormalizePythonVersionStrict(value)
		if err != nil {
			response.BadRequest(c, err.Error())
			return
		}
		if !service.PythonVersionSupportedByCurrentRuntime(pythonVersion) {
			response.BadRequest(c, fmt.Sprintf("当前镜像不支持 Python %s，请切换到对应 Python 版本镜像或 all 镜像", pythonVersion))
			return
		}
		req["python_version"] = pythonVersion
	}
	if rawSuccessExitCodes, exists := req["success_exit_codes"]; exists {
		value := ""
		if rawSuccessExitCodes != nil {
			var ok bool
			value, ok = rawSuccessExitCodes.(string)
			if !ok {
				response.BadRequest(c, "成功退出码格式无效")
				return
			}
		}
		normalized, err := model.NormalizeSuccessExitCodes(value)
		if err != nil {
			response.BadRequest(c, err.Error())
			return
		}
		req["success_exit_codes"] = normalized
	}

	allowedFields := map[string]bool{
		"name": true, "command": true, "python_version": true, "cron_expression": true,
		"task_type": true,
		"timeout":   true, "success_exit_codes": true, "random_delay_seconds": true, "max_retries": true, "retry_interval": true,
		"notify_on_failure": true, "notify_on_success": true, "notify_on_abort": true, "notification_channel_id": true, "labels": true, "depends_on": true,
		"sort_order": true, "task_before": true, "task_after": true,
		"allow_multiple_instances": true, "stop_schedule": true, "log_retention_days": true,
	}

	updates := make(map[string]interface{})
	for key, value := range req {
		if key == "random_delay_seconds" {
			randomDelayValue, err := normalizeTaskRandomDelaySecondsValue(value)
			if err != nil {
				response.BadRequest(c, err.Error())
				return
			}
			updates[key] = randomDelayValue
			continue
		}
		if key == "log_retention_days" {
			logRetentionValue, err := normalizeTaskLogRetentionDaysValue(value)
			if err != nil {
				response.BadRequest(c, err.Error())
				return
			}
			// 这里刻意把 nil 也写进 updates：map 形式的 Updates 不会跳过 nil，
			// 所以「从自定义天数改回跟随全局」全靠这一句把列写回 NULL（issue #144 / v3.3.2）。
			updates[key] = logRetentionValue
			continue
		}
		if field, ok := taskIntRangeFields[key]; ok {
			// 与库里现值相同就原样放过：网页和 APP 保存时会把整张表单回传，
			// 升级前经开放 API / 导入写进去的越界旧值不该让用户连改个名字都被 400 挡住。
			current := map[string]int{"timeout": task.Timeout, "max_retries": task.MaxRetries, "retry_interval": task.RetryInterval}[key]
			if number, isNumber := value.(float64); isNumber && number == float64(current) {
				updates[key] = current
				continue
			}
			normalized, err := normalizeTaskIntRangeValue(value, field.label, field.max)
			if err != nil {
				response.BadRequest(c, err.Error())
				return
			}
			updates[key] = normalized
			continue
		}
		if key == "notification_channel_id" {
			channelID, err := normalizeTaskNotificationChannelIDValue(value)
			if err != nil {
				response.BadRequest(c, err.Error())
				return
			}
			if channelID == nil {
				updates[key] = nil
			} else {
				updates[key] = *channelID
			}
			continue
		}
		if allowedFields[key] {
			updates[key] = value
		}
	}

	// 任务级「可用环境变量」（#168）。这三项刻意不进上面的 allowedFields 白名单原样写：
	// Update 是局部更新，三项任一出现时，按「库里旧值 + 本次请求」算出最终值再三项一起写（改回 all 时两列一并清空）；
	// 一项都不带（旧网页缓存、旧版 APP、开放 API）时三列都不碰，保持原值。
	_, hasEnvScope := req["env_scope"]
	_, hasEnvGroups := req["env_groups"]
	_, hasEnvNames := req["env_names"]
	if hasEnvScope || hasEnvGroups || hasEnvNames {
		scope := task.GetEnvScope()
		if hasEnvScope {
			value, ok := req["env_scope"].(string)
			if !ok {
				response.BadRequest(c, "env_scope 只能是 all 或 selected")
				return
			}
			scope = value
		}
		envGroups := task.GetEnvGroups()
		if hasEnvGroups {
			normalized, err := normalizeTaskEnvListValue(req["env_groups"], "env_groups")
			if err != nil {
				response.BadRequest(c, err.Error())
				return
			}
			envGroups = normalized
		}
		envNames := task.GetEnvNames()
		if hasEnvNames {
			normalized, err := normalizeTaskEnvListValue(req["env_names"], "env_names")
			if err != nil {
				response.BadRequest(c, err.Error())
				return
			}
			envNames = normalized
		}
		finalScope, finalGroups, finalNames, err := resolveTaskEnvScope(scope, envGroups, envNames)
		if err != nil {
			response.BadRequest(c, err.Error())
			return
		}
		updates["env_scope"] = finalScope
		updates["env_groups"] = finalGroups
		updates["env_names"] = finalNames
	}

	// 写库与回读放在同一个事务里，任何一步失败都回 500「更新任务失败」、库里保持原样，调度也不去动（#157）：
	//   - 写库失败：以前这里不接 .Error，没写进去也照样回 200「task updated」，前端弹「任务更新成功」、
	//     刷新后什么都没变，和「填了标签却没保存」的症状一模一样，很难排查；
	//   - 回读失败：写进去的值读不回来时（例如开放 API 往整数列 sort_order 里传了 "abc"，SQLite 照收、读的时候才报错；
	//     timeout / max_retries / retry_interval 已在上面校验，到不了这里），
	//     单独提交就会留下一行读不出来的坏数据，任务列表整页 500、这条任务连改都改不回来，所以要整体回滚。
	// 底层报错不透给前端；GORM 的日志器（database.Init）会把失败的 SQL 与原因打到进程标准输出。
	// 事务里只能用 tx：连接池只有一条连接（SetMaxOpenConns(1)），在这里再用 database.DB 会把自己卡死。
	if err := database.DB.Transaction(func(tx *gorm.DB) error {
		if len(updates) > 0 {
			if err := tx.Model(&task).Updates(updates).Error; err != nil {
				return err
			}
		}
		return tx.First(&task, taskID).Error
	}); err != nil {
		response.InternalError(c, "更新任务失败")
		return
	}
	if scheduler := service.GetSchedulerV2(); scheduler != nil {
		scheduler.UpdateJob(&task)
	}

	response.Success(c, gin.H{
		"message": "task updated",
		"data":    taskDictWithEnabledSwitch(&task),
	})
}

func (h *TaskHandler) Delete(c *gin.Context) {
	taskID, _ := strconv.ParseUint(c.Param("id"), 10, 32)

	var task model.Task
	if err := database.DB.First(&task, taskID).Error; err != nil {
		response.NotFound(c, "任务不存在")
		return
	}

	// 可选开关 delete_script（#124）：不带时整段不进入，下面的删除语句与响应和改动前逐字节一致。
	// 带了则先校验应用令牌的 scripts scope 并拍快照（此时任务还没删），删完任务再判定、删脚本。
	var scriptCleanup *taskScriptCleanupCtx
	if on, confirm := parseDeleteScriptQuery(c); on {
		var ok bool
		if scriptCleanup, ok = beginTaskScriptCleanup(c, []uint{uint(taskID)}, confirm); !ok {
			return
		}
	}

	if scheduler := service.GetSchedulerV2(); scheduler != nil {
		scheduler.RemoveJob(uint(taskID))
	}
	// 任务的历史日志先经 DeleteTaskLogs 并入执行趋势的按天计数再删（#158），仪表板不跟着变少。
	// 出错照旧不拦流程（与改动前一致）；路径不用取回，下面整目录收走。
	_ = database.DB.Transaction(func(tx *gorm.DB) error {
		_, _, err := service.DeleteTaskLogs(tx, "task_id = ?", taskID)
		return err
	})
	// 任务都删了，logs/task_<ID>_* 留着只会变成永远没人认领的垃圾目录（issue #144 / v3.3.2）。
	// 必须在删行之后调：日志行还在的时候清理目录，会让「记录还在、文件没了」更难排查。
	service.RemoveTaskLogDirs(uint(taskID), config.C.Data.LogDir)
	database.DB.Delete(&task)

	resp := gin.H{"message": "删除成功"}
	if scriptCleanup != nil {
		resp["scripts"] = finishTaskScriptCleanup(c, scriptCleanup)
	}
	response.Success(c, resp)
}

func (h *TaskHandler) Pin(c *gin.Context) {
	taskID, _ := strconv.ParseUint(c.Param("id"), 10, 32)
	database.DB.Model(&model.Task{}).Where("id = ?", taskID).Update("is_pinned", true)
	response.Success(c, gin.H{"message": "已置顶"})
}

func (h *TaskHandler) Unpin(c *gin.Context) {
	taskID, _ := strconv.ParseUint(c.Param("id"), 10, 32)
	database.DB.Model(&model.Task{}).Where("id = ?", taskID).Update("is_pinned", false)
	response.Success(c, gin.H{"message": "已取消置顶"})
}

func (h *TaskHandler) Copy(c *gin.Context) {
	taskID, _ := strconv.ParseUint(c.Param("id"), 10, 32)

	var task model.Task
	if err := database.DB.First(&task, taskID).Error; err != nil {
		response.NotFound(c, "任务不存在")
		return
	}

	newTask := model.Task{
		Name:                   task.Name + " (副本)",
		Command:                task.Command,
		PythonVersion:          task.PythonVersion,
		CronExpression:         task.CronExpression,
		TaskType:               task.GetTaskType(),
		Status:                 model.TaskStatusDisabled,
		Labels:                 task.Labels,
		Timeout:                task.Timeout,
		SuccessExitCodes:       task.GetSuccessExitCodes(),
		RandomDelaySeconds:     task.RandomDelaySeconds,
		LogRetentionDays:       task.LogRetentionDays,
		MaxRetries:             task.MaxRetries,
		RetryInterval:          task.RetryInterval,
		NotifyOnFailure:        task.NotifyOnFailure,
		NotifyOnSuccess:        task.NotifyOnSuccess,
		NotifyOnAbort:          task.NotifyOnAbort,
		NotificationChannelID:  task.NotificationChannelID,
		DependsOn:              task.DependsOn,
		TaskBefore:             task.TaskBefore,
		TaskAfter:              task.TaskAfter,
		AllowMultipleInstances: task.AllowMultipleInstances,
		StopSchedule:           task.StopSchedule,
		// 可用环境变量（#168）跟着复制：副本是同一个脚本，限定范围不能在复制时悄悄放宽成「全部」
		EnvScope:  task.GetEnvScope(),
		EnvGroups: task.EnvGroups,
		EnvNames:  task.EnvNames,
	}
	// 原来这行没接 .Error：插入失败也照样返回 201 + 一个没有 id 的空壳，用户以为复制成功了。
	// 注意 tasks 表刻意不加名称唯一键（同名任务合法，这里本来就是产出「XX (副本)」），
	// 所以这里报的一定是真实的写库故障，按 500 返回。
	if err := database.DB.Select("*").Create(&newTask).Error; err != nil {
		response.InternalError(c, "复制任务失败")
		return
	}
	response.Created(c, gin.H{"message": "复制成功", "data": taskDictWithEnabledSwitch(&newTask)})
}
