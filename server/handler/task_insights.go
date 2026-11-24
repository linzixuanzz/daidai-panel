package handler

import (
	"strconv"
	"strings"
	"time"

	"daidai-panel/database"
	"daidai-panel/model"
	"daidai-panel/pkg/response"
	"daidai-panel/service"

	"github.com/gin-gonic/gin"
)

// v4.0 仪表板与任务页的洞察类接口（design §4.2 ~ §4.5），全部 viewer 及以上可读。
// 这里只做参数解析与边界收敛，计算在 service/task_insights.go。
// 数值参数越界时一律夹到合法区间（而不是 400）：这些都是展示用的只读接口，夹一下比让图表整块报错更友好。

// clampQueryInt 读一个整数查询参数：缺省或解析失败用默认值，越界夹到 [minValue, maxValue]。
func clampQueryInt(c *gin.Context, name string, defaultValue, minValue, maxValue int) int {
	value, err := strconv.Atoi(strings.TrimSpace(c.Query(name)))
	if err != nil {
		value = defaultValue
	}
	if value < minValue {
		return minValue
	}
	if value > maxValue {
		return maxValue
	}
	return value
}

// Upcoming GET /tasks/upcoming?hours=24：启用中的定时任务在未来 hours 小时（1~48，默认 24）内的触发时刻。
func (h *TaskHandler) Upcoming(c *gin.Context) {
	hours := clampQueryInt(c, "hours", 24, 1, 48)

	var tasks []model.Task
	database.DB.Select("id", "name", "cron_expression", "task_type", "status").
		Where("status <> ?", model.TaskStatusDisabled).
		Order("id ASC").
		Find(&tasks)

	response.Success(c, service.BuildUpcomingRuns(tasks, time.Now(), hours))
}

// Health GET /tasks/:id/health?days=91：单个任务最近 days 天（7~180，默认 91）的健康画像。
func (h *TaskHandler) Health(c *gin.Context) {
	taskID, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil || taskID == 0 {
		response.BadRequest(c, "任务 ID 无效")
		return
	}
	var count int64
	database.DB.Model(&model.Task{}).Where("id = ?", taskID).Count(&count)
	if count == 0 {
		response.NotFound(c, "任务不存在")
		return
	}

	days := clampQueryInt(c, "days", 91, 7, 180)
	response.Success(c, service.BuildTaskHealth(uint(taskID), days, time.Now()))
}

// recentRunsMaxIDs / recentRunsMaxLimit：一次最多查多少个任务、每个任务最多多少条（design §4.4）。
// 任务列表每页最多 100 条，前端按当前页 id 取，正好够用；超了直接 400，不默默丢一部分。
const (
	recentRunsMaxIDs   = 100
	recentRunsMaxLimit = 30
)

// RecentRuns GET /tasks/recent-runs?ids=1,2,3&limit=20：多个任务各自最近 limit 次已结束的执行，旧 → 新。
func (h *TaskHandler) RecentRuns(c *gin.Context) {
	seen := make(map[uint]bool)
	ids := make([]uint, 0)
	for _, part := range strings.Split(c.Query("ids"), ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		id, err := strconv.ParseUint(part, 10, 32)
		if err != nil || id == 0 {
			response.BadRequest(c, "ids 格式无效，应为逗号分隔的任务 ID")
			return
		}
		// 重复的 id 只查一次
		if seen[uint(id)] {
			continue
		}
		seen[uint(id)] = true
		ids = append(ids, uint(id))
	}
	if len(ids) > recentRunsMaxIDs {
		response.BadRequest(c, "ids 一次最多 100 个")
		return
	}
	limit := clampQueryInt(c, "limit", 20, 1, recentRunsMaxLimit)

	runs := service.BuildRecentRuns(ids, limit)
	// JSON 对象的键只能是字符串，这里显式转一遍，和 design 的 {"runs": {"1": [...]}} 对齐
	data := make(map[string][]service.RecentRun, len(runs))
	for id, items := range runs {
		data[strconv.FormatUint(uint64(id), 10)] = items
	}
	response.Success(c, gin.H{"runs": data})
}

// Running GET /tasks/running：当前运行中的任务与各自正在写的日志 id，给网页的运行中浮窗用。
// 原有接口里没有能一次拿到「运行中任务 + 当前日志 id」的（列表接口要分页、live-logs 是单任务），所以新开一个。
func (h *TaskHandler) Running(c *gin.Context) {
	response.Success(c, gin.H{"items": service.ListRunningTasks()})
}
