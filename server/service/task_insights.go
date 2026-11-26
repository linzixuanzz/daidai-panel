package service

import (
	"math"
	"sort"
	"time"

	"daidai-panel/database"
	"daidai-panel/model"
	panelcron "daidai-panel/pkg/cron"
)

// v4.0 仪表板与任务页的洞察类接口（design §4.2 ~ §4.5）的计算部分。参数解析与路由在 handler/task_insights.go。
//
// 【共同的性能约束】task_logs 的 status / duration 排在大字段 content 之后，读这两列要顺着 content 的溢出页走一遍
// （见 model.TaskLog 的注释）。所以这里能只靠 idx_task_logs_task_id_started_at（含 rowid）定位的，就先在索引上
// 圈出要的行，再回表取 status / duration，避免把某个任务的全部日志行逐行读一遍。

// ===== 未来 N 小时（GET /tasks/upcoming）=====

const (
	// UpcomingMaxPointsPerTask：单个任务最多给这么多个触发点（每分钟一次的任务 24 小时正好 1440 个）。
	UpcomingMaxPointsPerTask = 1440
	// UpcomingMaxTotalPoints：所有任务的触发点合计超过这个数就截断，并在响应里带 truncated:true。
	UpcomingMaxTotalPoints = 5000
)

type UpcomingTask struct {
	ID    uint    `json:"id"`
	Name  string  `json:"name"`
	Times []int64 `json:"times"`
}

type UpcomingResult struct {
	From  int64          `json:"from"`
	To    int64          `json:"to"`
	Tasks []UpcomingTask `json:"tasks"`
	// Hourly[i] 是 [from+i 小时, from+i+1 小时) 内的触发次数。
	// 刻意按截断前的全部触发点统计：柱状图要反映真实的忙闲，截断只影响泳道里的明细点。
	Hourly    []int `json:"hourly"`
	Truncated bool  `json:"truncated,omitempty"`
}

// BuildUpcomingRuns 计算启用中的定时任务在 [from, from+hours) 内的触发时刻（Unix 秒）。
// tasks 由调用方查好（只需要 id / name / cron_expression / task_type / status），这里不碰数据库，方便单测。
func BuildUpcomingRuns(tasks []model.Task, from time.Time, hours int) UpcomingResult {
	from = from.Truncate(time.Second)
	to := from.Add(time.Duration(hours) * time.Hour)
	result := UpcomingResult{
		From:   from.Unix(),
		To:     to.Unix(),
		Tasks:  make([]UpcomingTask, 0),
		Hourly: make([]int, hours),
	}

	total := 0
	for _, task := range tasks {
		// 只算启用中（含运行中、排队中）且按 cron 调度的任务；手动、开机任务没有「下次运行」
		if task.Status == model.TaskStatusDisabled || !task.UsesCronSchedule() || task.CronExpression == "" {
			continue
		}
		// 任务可以有多条 cron（换行分隔），按窗口求触发点：到 to 就停，合并、排序并截到 UpcomingMaxPointsPerTask 个。
		// 以前是先固定推 1440 个点再丢掉窗口外的，每天一次的任务要往后推约 4 年，任务一多接口就慢（v4.0.2）。
		candidates := panelcron.NextRunTimesForExpressionsBetween(task.CronExpression, from, to, UpcomingMaxPointsPerTask)
		times := make([]int64, 0, len(candidates))
		for _, t := range candidates {
			times = append(times, t.Unix())
			result.Hourly[int(t.Sub(from)/time.Hour)]++
		}
		if len(times) == 0 {
			continue
		}

		// 总数截断：放不下的部分丢掉，之后的任务也不再进泳道（但仍计入 hourly）
		if result.Truncated {
			continue
		}
		if total+len(times) > UpcomingMaxTotalPoints {
			times = times[:UpcomingMaxTotalPoints-total]
			result.Truncated = true
			if len(times) == 0 {
				continue
			}
		}
		total += len(times)
		result.Tasks = append(result.Tasks, UpcomingTask{ID: task.ID, Name: task.Name, Times: times})
	}
	return result
}

// ===== 健康画像（GET /tasks/:id/health）=====

// HealthP90SampleLimit：P90 只拿窗口内最近这么多条耗时来算，避免高频任务一次排序几十万个数。
const HealthP90SampleLimit = 2000

// HealthRecentLimit：recent 返回最近多少次执行。
const HealthRecentLimit = 20

type HealthDay struct {
	Date        string  `json:"date"`
	Total       int     `json:"total"`
	Failed      int     `json:"failed"`
	AvgDuration float64 `json:"avg_duration"`
}

type HealthStats struct {
	Runs        int     `json:"runs"`
	SuccessRate float64 `json:"success_rate"`
	AvgDuration float64 `json:"avg_duration"`
	P90Duration float64 `json:"p90_duration"`
}

type HealthRun struct {
	Status    int       `json:"status"`
	Duration  *float64  `json:"duration"`
	StartedAt time.Time `json:"started_at"`
}

type HealthResult struct {
	Days   []HealthDay `json:"days"`
	Stats  HealthStats `json:"stats"`
	Recent []HealthRun `json:"recent"`
}

// healthLogRow 是健康画像查询的一行。
type healthLogRow struct {
	Status    *int
	Duration  *float64
	StartedAt time.Time
}

// BuildTaskHealth 统计某任务最近 days 天（按面板时区分天，含今天）的执行情况。
//
// 口径：
//   - 只统计已结束的执行（成功 / 失败 / 已终止），运行中的那条不计；
//   - total = 当天已结束次数，failed = 当天失败次数（超时也记为失败）；
//   - success_rate = 成功 /（成功 + 失败）× 100，已终止不拉低成功率，与 /system/stats 一致；
//   - 平均耗时只算有耗时记录的执行；没有任何执行的天补一行全 0。
//
// 这个接口按需调用（展开任务行时），不是轮询，所以接受「窗口内的日志行全部回表」的代价；
// 日志默认只保留 7 天，绝大多数实例窗口内的行数并不多。
func BuildTaskHealth(taskID uint, days int, now time.Time) HealthResult {
	now = now.In(time.Local)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)
	start := today.AddDate(0, 0, -(days - 1))

	var rows []healthLogRow
	database.DB.Model(&model.TaskLog{}).
		Select("status", "duration", "started_at").
		Where("task_id = ? AND started_at >= ? AND status IN ?", taskID, start,
			[]int{model.LogStatusSuccess, model.LogStatusFailed, model.LogStatusAborted}).
		Order("started_at DESC, id DESC").
		Scan(&rows)

	return summarizeTaskHealth(rows, start, days)
}

// summarizeTaskHealth 是 BuildTaskHealth 的纯计算部分。rows 必须按 started_at 倒序（新 → 旧）。
func summarizeTaskHealth(rows []healthLogRow, start time.Time, days int) HealthResult {
	result := HealthResult{
		Days:   make([]HealthDay, days),
		Recent: make([]HealthRun, 0),
	}
	dayIndex := make(map[string]int, days)
	dayDurationSum := make([]float64, days)
	dayDurationCount := make([]int, days)
	for i := 0; i < days; i++ {
		// 用 AddDate 而不是加 24 小时：有夏令时的时区里一天不一定是 24 小时
		date := start.AddDate(0, 0, i).Format("2006-01-02")
		result.Days[i] = HealthDay{Date: date}
		dayIndex[date] = i
	}

	var success, failed int
	var durationSum float64
	var durationCount int
	p90Samples := make([]float64, 0)
	for _, row := range rows {
		if row.Status == nil {
			continue
		}
		status := *row.Status
		result.Stats.Runs++
		switch status {
		case model.LogStatusSuccess:
			success++
		case model.LogStatusFailed:
			failed++
		}
		if row.Duration != nil {
			durationSum += *row.Duration
			durationCount++
			if len(p90Samples) < HealthP90SampleLimit {
				p90Samples = append(p90Samples, *row.Duration)
			}
		}
		// SQLite 取回的时间可能是 UTC，换成面板时区再取日期
		if index, ok := dayIndex[row.StartedAt.In(time.Local).Format("2006-01-02")]; ok {
			result.Days[index].Total++
			if status == model.LogStatusFailed {
				result.Days[index].Failed++
			}
			if row.Duration != nil {
				dayDurationSum[index] += *row.Duration
				dayDurationCount[index]++
			}
		}
		if len(result.Recent) < HealthRecentLimit {
			result.Recent = append(result.Recent, HealthRun{Status: status, Duration: row.Duration, StartedAt: row.StartedAt})
		}
	}

	for i := range result.Days {
		if dayDurationCount[i] > 0 {
			result.Days[i].AvgDuration = roundInsight(dayDurationSum[i] / float64(dayDurationCount[i]))
		}
	}
	if success+failed > 0 {
		result.Stats.SuccessRate = math.Round(float64(success)/float64(success+failed)*1000) / 10
	}
	if durationCount > 0 {
		result.Stats.AvgDuration = roundInsight(durationSum / float64(durationCount))
	}
	result.Stats.P90Duration = roundInsight(percentile90(p90Samples))

	// recent 要旧 → 新，查询是新 → 旧，这里翻过来
	for i, j := 0, len(result.Recent)-1; i < j; i, j = i+1, j-1 {
		result.Recent[i], result.Recent[j] = result.Recent[j], result.Recent[i]
	}
	return result
}

// percentile90 用最近秩法（nearest-rank）算 P90：排序后取第 ceil(0.9n) 个。没有样本时返回 0。
func percentile90(samples []float64) float64 {
	if len(samples) == 0 {
		return 0
	}
	sorted := append([]float64(nil), samples...)
	sort.Float64s(sorted)
	rank := int(math.Ceil(0.9*float64(len(sorted)))) - 1
	if rank < 0 {
		rank = 0
	}
	return sorted[rank]
}

// roundInsight 把秒数保留两位小数，免得前端拿到 1.4000000000000001 这种值。
func roundInsight(value float64) float64 {
	return math.Round(value*100) / 100
}

// ===== 最近 N 次（GET /tasks/recent-runs）=====

type RecentRun struct {
	Status   int      `json:"status"`
	Duration *float64 `json:"duration"`
}

type recentRunRow struct {
	TaskID   uint
	Status   *int
	Duration *float64
}

// BuildRecentRuns 一次查出多个任务各自最近 limit 次已结束的执行，每个任务按旧 → 新排列。
//
// 只用一条 SQL（窗口函数，SQLite 3.25+）：
//   - 内层只读 id / task_id / started_at，全部在 idx_task_logs_task_id_started_at 上（索引里带 rowid），
//     不碰日志行本身，按任务分区、新 → 旧编号；
//   - 外层只对编号 ≤ limit+1 的那几行回表取 status / duration。
//
// 多取 1 行是给「当前正在运行」的那条留位置：运行中的日志不算一次「已结束的执行」，在 Go 里丢掉，
// 丢完再截到 limit。同一任务同时有多个运行中实例（允许多实例）时，个别任务可能少于 limit 条，可以接受。
// 没有日志的任务也会出现在结果里，值为空数组，前端不用区分「没查」和「没跑过」。
func BuildRecentRuns(taskIDs []uint, limit int) map[uint][]RecentRun {
	result := make(map[uint][]RecentRun, len(taskIDs))
	for _, id := range taskIDs {
		result[id] = make([]RecentRun, 0)
	}
	if len(taskIDs) == 0 || limit <= 0 {
		return result
	}

	var rows []recentRunRow
	database.DB.Raw(`
SELECT r.task_id AS task_id, l.status AS status, l.duration AS duration
FROM (
	SELECT id, task_id, started_at,
		ROW_NUMBER() OVER (PARTITION BY task_id ORDER BY started_at DESC, id DESC) AS rn
	FROM task_logs
	WHERE task_id IN ?
) AS r
JOIN task_logs AS l ON l.id = r.id
WHERE r.rn <= ?
ORDER BY r.task_id ASC, r.started_at ASC, r.id ASC`, taskIDs, limit+1).Scan(&rows)

	for _, row := range rows {
		// 运行中（或状态缺失）的不是「已结束的执行」，跳过
		if row.Status == nil || *row.Status == model.LogStatusRunning {
			continue
		}
		result[row.TaskID] = append(result[row.TaskID], RecentRun{Status: *row.Status, Duration: row.Duration})
	}
	for id, runs := range result {
		// 行是旧 → 新，超出 limit 时保留最新的 limit 条
		if len(runs) > limit {
			result[id] = runs[len(runs)-limit:]
		}
	}
	return result
}

// ===== 运行中（GET /tasks/running）=====

type RunningTaskItem struct {
	TaskID    uint       `json:"task_id"`
	Name      string     `json:"name"`
	LogID     *uint      `json:"log_id"`
	StartedAt *time.Time `json:"started_at"`
}

// ListRunningTasks 列出当前运行中的任务以及各自正在写的那条日志。
//
// 每个任务只给一条：取最新的运行中日志（允许多实例的任务同时跑多份时，只给最新那份，前端浮窗按任务展示）。
// 运行中的日志几乎总是这个任务最新的一条，所以按 idx_task_logs_task_id_started_at 倒序只看前几条，
// 不按 status 扫全部日志。一条都找不到（极短的竞态窗口：任务已标记运行、日志行还没建好）时
// log_id 为 null、started_at 回落到任务的 last_run_at。
func ListRunningTasks() []RunningTaskItem {
	var tasks []model.Task
	database.DB.Select("id", "name", "last_run_at").
		Where("status = ?", model.TaskStatusRunning).
		Order("id ASC").
		Find(&tasks)

	items := make([]RunningTaskItem, 0, len(tasks))
	for _, task := range tasks {
		item := RunningTaskItem{TaskID: task.ID, Name: task.Name, StartedAt: task.LastRunAt}
		var logs []model.TaskLog
		database.DB.Select("id", "status", "started_at").
			Where("task_id = ?", task.ID).
			Order("started_at DESC, id DESC").
			Limit(3).
			Find(&logs)
		for _, taskLog := range logs {
			if taskLog.Status != nil && *taskLog.Status == model.LogStatusRunning {
				logID := taskLog.ID
				startedAt := taskLog.StartedAt
				item.LogID = &logID
				item.StartedAt = &startedAt
				break
			}
		}
		items = append(items, item)
	}
	return items
}
