package handler

import (
	"fmt"
	"log"
	"strconv"
	"strings"

	"daidai-panel/database"
	"daidai-panel/model"
	panelcron "daidai-panel/pkg/cron"
	"daidai-panel/pkg/response"
	"daidai-panel/service"

	"github.com/gin-gonic/gin"
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

func (h *TaskHandler) Create(c *gin.Context) {
	var req struct {
		Name                   string   `json:"name" binding:"required"`
		Command                string   `json:"command" binding:"required"`
		PythonVersion          string   `json:"python_version"`
		CronExpression         string   `json:"cron_expression"`
		TaskType               string   `json:"task_type"`
		Timeout                *int     `json:"timeout"`
		SuccessExitCodes       *string  `json:"success_exit_codes"`
		RandomDelaySeconds     *int     `json:"random_delay_seconds"`
		MaxRetries             *int     `json:"max_retries"`
		RetryInterval          *int     `json:"retry_interval"`
		NotifyOnFailure        *bool    `json:"notify_on_failure"`
		NotifyOnSuccess        *bool    `json:"notify_on_success"`
		NotifyOnAbort          *bool    `json:"notify_on_abort"`
		NotificationChannelID  *uint    `json:"notification_channel_id"`
		Labels                 []string `json:"labels"`
		DependsOn              *uint    `json:"depends_on"`
		TaskBefore             *string  `json:"task_before"`
		TaskAfter              *string  `json:"task_after"`
		AllowMultipleInstances *bool    `json:"allow_multiple_instances"`
		StopSchedule           *string  `json:"stop_schedule"`
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
	}

	if req.Timeout != nil {
		task.Timeout = *req.Timeout
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
	if req.MaxRetries != nil {
		task.MaxRetries = *req.MaxRetries
	}
	if req.RetryInterval != nil {
		task.RetryInterval = *req.RetryInterval
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
		"data":    task.ToDict(),
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
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "请求参数错误")
		return
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
		"allow_multiple_instances": true, "stop_schedule": true,
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

	if len(updates) > 0 {
		database.DB.Model(&task).Updates(updates)
	}

	database.DB.First(&task, taskID)
	if scheduler := service.GetSchedulerV2(); scheduler != nil {
		scheduler.UpdateJob(&task)
	}

	response.Success(c, gin.H{
		"message": "task updated",
		"data":    task.ToDict(),
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
	database.DB.Where("task_id = ?", taskID).Delete(&model.TaskLog{})
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
	}
	// 原来这行没接 .Error：插入失败也照样返回 201 + 一个没有 id 的空壳，用户以为复制成功了。
	// 注意 tasks 表刻意不加名称唯一键（同名任务合法，这里本来就是产出「XX (副本)」），
	// 所以这里报的一定是真实的写库故障，按 500 返回。
	if err := database.DB.Select("*").Create(&newTask).Error; err != nil {
		response.InternalError(c, "复制任务失败")
		return
	}
	response.Created(c, gin.H{"message": "复制成功", "data": newTask.ToDict()})
}
