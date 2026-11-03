package handler

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"daidai-panel/database"
	"daidai-panel/model"
	panelcron "daidai-panel/pkg/cron"
	"daidai-panel/pkg/response"
	"daidai-panel/service"

	"github.com/gin-gonic/gin"
)

func (h *TaskHandler) Export(c *gin.Context) {
	var tasks []model.Task
	database.DB.Find(&tasks)
	notificationChannels := loadTaskNotificationChannelMap(tasks)

	data := make([]map[string]interface{}, len(tasks))
	for i, task := range tasks {
		var notificationChannelName string
		if task.NotificationChannelID != nil {
			if channel, exists := notificationChannels[*task.NotificationChannelID]; exists {
				notificationChannelName = channel.Name
			}
		}
		data[i] = map[string]interface{}{
			"name":                      task.Name,
			"command":                   task.Command,
			"python_version":            task.PythonVersion,
			"cron_expression":           task.CronExpression,
			"task_type":                 task.GetTaskType(),
			"status":                    task.Status,
			"labels":                    task.GetLabels(),
			"timeout":                   task.Timeout,
			"success_exit_codes":        task.GetSuccessExitCodes(),
			"random_delay_seconds":      task.RandomDelaySeconds,
			"log_retention_days":        task.LogRetentionDays,
			"max_retries":               task.MaxRetries,
			"retry_interval":            task.RetryInterval,
			"notify_on_failure":         task.NotifyOnFailure,
			"notify_on_success":         task.NotifyOnSuccess,
			"notify_on_abort":           task.NotifyOnAbort,
			"notification_channel_id":   task.NotificationChannelID,
			"notification_channel_name": notificationChannelName,
			"depends_on":                task.DependsOn,
			"sort_order":                task.SortOrder,
			"task_before":               task.TaskBefore,
			"task_after":                task.TaskAfter,
			"allow_multiple_instances":  task.AllowMultipleInstances,
			// 可用环境变量（#168）。与 ToDict 同形：scope 规范成 all / selected，两个列表恒为数组。
			// 旧版面板导入时认不出这三个键，会静默忽略、按全部处理。
			"env_scope":  task.GetEnvScope(),
			"env_groups": task.GetEnvGroups(),
			"env_names":  task.GetEnvNames(),
		}
	}
	response.Success(c, gin.H{"data": data})
}

func normalizeImportedTaskStatus(value interface{}) (float64, error) {
	if value == nil {
		return model.TaskStatusDisabled, nil
	}

	var status float64
	switch typed := value.(type) {
	case float64:
		status = typed
	case int:
		status = float64(typed)
	case string:
		trimmed := strings.TrimSpace(typed)
		if trimmed == "" {
			return model.TaskStatusDisabled, nil
		}
		parsed, err := strconv.ParseFloat(trimmed, 64)
		if err != nil {
			return 0, fmt.Errorf("无效的任务状态")
		}
		status = parsed
	default:
		return 0, fmt.Errorf("无效的任务状态")
	}

	switch status {
	case model.TaskStatusDisabled:
		return model.TaskStatusDisabled, nil
	case model.TaskStatusQueued, model.TaskStatusEnabled, model.TaskStatusRunning:
		return model.TaskStatusEnabled, nil
	default:
		return 0, fmt.Errorf("无效的任务状态")
	}
}

func (h *TaskHandler) Import(c *gin.Context) {
	var req struct {
		Tasks []map[string]interface{} `json:"tasks" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "请求参数错误")
		return
	}

	imported := 0
	errors := make([]string, 0)

	for i, taskData := range req.Tasks {
		name, _ := taskData["name"].(string)
		command, _ := taskData["command"].(string)
		pythonVersionRaw, _ := taskData["python_version"].(string)
		cronExpr, _ := taskData["cron_expression"].(string)
		taskType := model.TaskTypeCron
		if rawTaskType, ok := taskData["task_type"].(string); ok && strings.TrimSpace(rawTaskType) != "" {
			taskType = model.NormalizeTaskType(rawTaskType)
		} else if strings.TrimSpace(cronExpr) == "" {
			taskType = model.TaskTypeManual
		}

		if name == "" || command == "" {
			errors = append(errors, fmt.Sprintf("任务 %d: 缺少必填字段", i+1))
			continue
		}
		if taskType == "" {
			errors = append(errors, fmt.Sprintf("任务 %d: 无效的任务类型", i+1))
			continue
		}
		if taskType == model.TaskTypeCron {
			result := panelcron.Parse(cronExpr)
			if !result.Valid {
				errors = append(errors, fmt.Sprintf("任务 %d: 无效的 cron 表达式", i+1))
				continue
			}
		} else {
			cronExpr = ""
		}
		pythonVersion, err := service.NormalizePythonVersionStrict(pythonVersionRaw)
		if err != nil {
			errors = append(errors, fmt.Sprintf("任务 %d: %s", i+1, err.Error()))
			continue
		}

		task := model.Task{
			Name:             name,
			Command:          command,
			PythonVersion:    pythonVersion,
			CronExpression:   cronExpr,
			TaskType:         taskType,
			Status:           model.TaskStatusDisabled,
			Timeout:          0,
			SuccessExitCodes: model.DefaultSuccessExitCodes,
			RetryInterval:    60,
			NotifyOnFailure:  true,
			EnvScope:         model.TaskEnvScopeAll,
		}

		// 可用环境变量（#168）：老导出文件没有这三个键 → 按默认「全部」；
		// 带了就与创建接口同一套校验，不合法的这一条记进 errors 并跳过，其余任务照常导入。
		_, hasEnvScope := taskData["env_scope"]
		_, hasEnvGroups := taskData["env_groups"]
		_, hasEnvNames := taskData["env_names"]
		if hasEnvScope || hasEnvGroups || hasEnvNames {
			scope := model.TaskEnvScopeAll
			if hasEnvScope {
				value, ok := taskData["env_scope"].(string)
				if !ok {
					errors = append(errors, fmt.Sprintf("任务 %d: env_scope 只能是 all 或 selected", i+1))
					continue
				}
				scope = value
			}
			envGroups, err := normalizeTaskEnvListValue(taskData["env_groups"], "env_groups")
			if err != nil {
				errors = append(errors, fmt.Sprintf("任务 %d: %s", i+1, err.Error()))
				continue
			}
			envNames, err := normalizeTaskEnvListValue(taskData["env_names"], "env_names")
			if err != nil {
				errors = append(errors, fmt.Sprintf("任务 %d: %s", i+1, err.Error()))
				continue
			}
			task.EnvScope, task.EnvGroups, task.EnvNames, err = resolveTaskEnvScope(scope, envGroups, envNames)
			if err != nil {
				errors = append(errors, fmt.Sprintf("任务 %d: %s", i+1, err.Error()))
				continue
			}
		}

		if statusValue, exists := taskData["status"]; exists {
			status, err := normalizeImportedTaskStatus(statusValue)
			if err != nil {
				errors = append(errors, fmt.Sprintf("任务 %d: %s", i+1, err.Error()))
				continue
			}
			task.Status = status
		}
		// 超时 / 重试三项与创建接口同一套上下限（normalizeTaskIntRangeValue）：越界的这一条记进 errors 并跳过，
		// 其余任务照常导入。非数字的值维持原样忽略（取默认值），与改动前一致。
		intRangeInvalid := false
		for _, key := range []string{"timeout", "max_retries", "retry_interval"} {
			value, ok := taskData[key].(float64)
			if !ok {
				continue
			}
			field := taskIntRangeFields[key]
			normalized, err := normalizeTaskIntRangeValue(value, field.label, field.max)
			if err != nil {
				errors = append(errors, fmt.Sprintf("任务 %d: %s", i+1, err.Error()))
				intRangeInvalid = true
				break
			}
			switch key {
			case "timeout":
				task.Timeout = normalized
			case "max_retries":
				task.MaxRetries = normalized
			case "retry_interval":
				task.RetryInterval = normalized
			}
		}
		if intRangeInvalid {
			continue
		}
		if value, exists := taskData["success_exit_codes"]; exists {
			raw := ""
			if value != nil {
				var ok bool
				raw, ok = value.(string)
				if !ok {
					errors = append(errors, fmt.Sprintf("任务 %d: 成功退出码格式无效", i+1))
					continue
				}
			}
			normalized, err := model.NormalizeSuccessExitCodes(raw)
			if err != nil {
				errors = append(errors, fmt.Sprintf("任务 %d: %s", i+1, err.Error()))
				continue
			}
			task.SuccessExitCodes = normalized
		}
		if value, exists := taskData["random_delay_seconds"]; exists {
			randomDelayValue, err := normalizeTaskRandomDelaySecondsValue(value)
			if err != nil {
				errors = append(errors, fmt.Sprintf("任务 %d: %s", i+1, err.Error()))
				continue
			}
			task.RandomDelaySeconds = randomDelayValue
		}
		// 任务级日志保留天数（issue #144 / v3.3.2）。导出里多带一个键对旧版本面板是安全的：
		// 导入侧就是这样按键逐个取值的，认不出的键会被静默忽略。
		if value, exists := taskData["log_retention_days"]; exists {
			logRetentionValue, err := normalizeTaskLogRetentionDaysValue(value)
			if err != nil {
				errors = append(errors, fmt.Sprintf("任务 %d: %s", i+1, err.Error()))
				continue
			}
			task.LogRetentionDays = logRetentionValue
		}
		if value, ok := taskData["notify_on_failure"].(bool); ok {
			task.NotifyOnFailure = value
		}
		if value, ok := taskData["notify_on_success"].(bool); ok {
			task.NotifyOnSuccess = value
		}
		if value, ok := taskData["notify_on_abort"].(bool); ok {
			task.NotifyOnAbort = value
		}
		if channelID, warningMessage, err := resolveImportedTaskNotificationChannel(taskData); err != nil {
			errors = append(errors, fmt.Sprintf("任务 %d: 解析通知渠道失败: %s", i+1, err.Error()))
		} else {
			task.NotificationChannelID = channelID
			if warningMessage != "" {
				errors = append(errors, fmt.Sprintf("任务 %d: %s", i+1, warningMessage))
			}
		}
		if labels, ok := taskData["labels"].([]interface{}); ok {
			values := make([]string, len(labels))
			for j, label := range labels {
				values[j] = fmt.Sprintf("%v", label)
			}
			task.SetLabelsFromSlice(values)
		}
		if value, ok := taskData["task_before"].(string); ok {
			task.TaskBefore = &value
		}
		if value, ok := taskData["task_after"].(string); ok {
			task.TaskAfter = &value
		}

		if err := database.DB.Select("*").Create(&task).Error; err != nil {
			errors = append(errors, fmt.Sprintf("task %d: %s", i+1, err.Error()))
			continue
		}
		if task.Status == model.TaskStatusEnabled {
			if scheduler := service.GetSchedulerV2(); scheduler != nil {
				if err := scheduler.AddJob(&task); err != nil {
					errors = append(errors, fmt.Sprintf("任务 %d: 添加调度失败: %s", i+1, err.Error()))
				}
			}
		}
		imported++
	}

	if imported == 0 && len(errors) > 0 {
		response.BadRequest(c, "没有成功导入任何任务")
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"message": fmt.Sprintf("成功导入 %d 个任务", imported),
		"errors":  errors,
	})
}
