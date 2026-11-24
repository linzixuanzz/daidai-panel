package service

import (
	"strings"
	"testing"
	"time"

	"daidai-panel/model"
)

func TestBuildUpcomingRunsCountsEnabledCronTasksAndHourly(t *testing.T) {
	from := time.Date(2026, 11, 24, 10, 0, 30, 0, time.Local)
	tasks := []model.Task{
		// 每小时整点：未来 3 小时 11:00、12:00、13:00 → 3 个点
		{ID: 1, Name: "hourly", CronExpression: "0 * * * *", TaskType: model.TaskTypeCron, Status: model.TaskStatusEnabled},
		// 多条 cron（换行分隔）：每天 10:30 与 11:15 → 2 个点
		{ID: 2, Name: "multi", CronExpression: "30 10 * * *\n15 11 * * *", TaskType: model.TaskTypeCron, Status: model.TaskStatusRunning},
		// 禁用、手动任务都不算
		{ID: 3, Name: "disabled", CronExpression: "* * * * *", TaskType: model.TaskTypeCron, Status: model.TaskStatusDisabled},
		{ID: 4, Name: "manual", CronExpression: "* * * * *", TaskType: model.TaskTypeManual, Status: model.TaskStatusEnabled},
	}

	result := BuildUpcomingRuns(tasks, from, 3)
	if result.From != from.Unix() || result.To != from.Add(3*time.Hour).Unix() {
		t.Fatalf("from/to 不对：%d %d", result.From, result.To)
	}
	if result.Truncated {
		t.Fatal("没超上限不应截断")
	}
	if len(result.Tasks) != 2 || result.Tasks[0].ID != 1 || result.Tasks[1].ID != 2 {
		t.Fatalf("应只含任务 1、2，实际 %#v", result.Tasks)
	}
	if len(result.Tasks[0].Times) != 3 || len(result.Tasks[1].Times) != 2 {
		t.Fatalf("触发点个数不对：%d / %d", len(result.Tasks[0].Times), len(result.Tasks[1].Times))
	}
	// 第 0 小时 [10:00:30, 11:00:30)：10:30、11:00 → 2；第 1 小时：11:15、12:00 → 2；第 2 小时：13:00 → 1
	want := []int{2, 2, 1}
	for i, count := range want {
		if result.Hourly[i] != count {
			t.Fatalf("hourly = %v，期望 %v", result.Hourly, want)
		}
	}
}

// 边界：单任务最多 1440 个点；总数超过 5000 截断并带 truncated，但 hourly 仍按全部触发点统计。
func TestBuildUpcomingRunsCapsPerTaskAndTruncatesTotal(t *testing.T) {
	from := time.Date(2026, 11, 24, 0, 0, 0, 0, time.Local)
	tasks := make([]model.Task, 0)
	for i := 1; i <= 4; i++ {
		// 每分钟一次，48 小时本有 2880 个点，单任务被截到 1440
		tasks = append(tasks, model.Task{ID: uint(i), Name: "busy", CronExpression: "* * * * *", TaskType: model.TaskTypeCron, Status: model.TaskStatusEnabled})
	}

	result := BuildUpcomingRuns(tasks, from, 48)
	if !result.Truncated {
		t.Fatal("4 × 1440 = 5760 > 5000，应截断")
	}
	total := 0
	for _, task := range result.Tasks {
		if len(task.Times) > UpcomingMaxPointsPerTask {
			t.Fatalf("单任务点数超过上限：%d", len(task.Times))
		}
		total += len(task.Times)
	}
	if total != UpcomingMaxTotalPoints {
		t.Fatalf("截断后总数应正好是 %d，实际 %d", UpcomingMaxTotalPoints, total)
	}
	hourlyTotal := 0
	for _, count := range result.Hourly {
		hourlyTotal += count
	}
	if hourlyTotal != 4*UpcomingMaxPointsPerTask {
		t.Fatalf("hourly 应按截断前全部点统计（%d），实际 %d", 4*UpcomingMaxPointsPerTask, hourlyTotal)
	}
}

func insightIntPtr(v int) *int           { return &v }
func insightFloatPtr(v float64) *float64 { return &v }

// 边界：没有运行的天补 0 行；P90 用最近秩法；已终止不拉低成功率；recent 旧 → 新。
func TestSummarizeTaskHealthFillsEmptyDaysAndComputesP90(t *testing.T) {
	start := time.Date(2026, 11, 18, 0, 0, 0, 0, time.Local) // 7 天：11-18 ~ 11-24
	day := func(d, hour int) time.Time { return time.Date(2026, 11, d, hour, 0, 0, 0, time.Local) }

	// 新 → 旧
	rows := []healthLogRow{
		{Status: insightIntPtr(model.LogStatusSuccess), Duration: insightFloatPtr(10), StartedAt: day(24, 9)},
		{Status: insightIntPtr(model.LogStatusFailed), Duration: insightFloatPtr(9), StartedAt: day(24, 8)},
		{Status: insightIntPtr(model.LogStatusAborted), Duration: insightFloatPtr(8), StartedAt: day(22, 8)},
		{Status: insightIntPtr(model.LogStatusSuccess), Duration: insightFloatPtr(7), StartedAt: day(22, 7)},
		{Status: insightIntPtr(model.LogStatusSuccess), Duration: insightFloatPtr(6), StartedAt: day(20, 7)},
		{Status: insightIntPtr(model.LogStatusSuccess), Duration: insightFloatPtr(5), StartedAt: day(20, 6)},
		{Status: insightIntPtr(model.LogStatusSuccess), Duration: insightFloatPtr(4), StartedAt: day(18, 6)},
		{Status: insightIntPtr(model.LogStatusSuccess), Duration: insightFloatPtr(3), StartedAt: day(18, 5)},
		{Status: insightIntPtr(model.LogStatusSuccess), Duration: insightFloatPtr(2), StartedAt: day(18, 4)},
		{Status: insightIntPtr(model.LogStatusSuccess), Duration: nil, StartedAt: day(18, 3)},
	}

	result := summarizeTaskHealth(rows, start, 7)
	if len(result.Days) != 7 || result.Days[0].Date != "2026-11-18" || result.Days[6].Date != "2026-11-24" {
		t.Fatalf("days 应覆盖 11-18 ~ 11-24 共 7 天，实际 %#v", result.Days)
	}
	if result.Days[1].Total != 0 || result.Days[1].Failed != 0 || result.Days[1].AvgDuration != 0 {
		t.Fatalf("没有运行的 11-19 应补 0 行，实际 %#v", result.Days[1])
	}
	if result.Days[6].Total != 2 || result.Days[6].Failed != 1 || result.Days[6].AvgDuration != 9.5 {
		t.Fatalf("11-24 统计不对：%#v", result.Days[6])
	}
	// 11-18 有 4 次，平均耗时只算有耗时的 3 次：(4+3+2)/3 = 3
	if result.Days[0].Total != 4 || result.Days[0].AvgDuration != 3 {
		t.Fatalf("11-18 统计不对：%#v", result.Days[0])
	}

	if result.Stats.Runs != 10 {
		t.Fatalf("runs 应为 10，实际 %d", result.Stats.Runs)
	}
	// 成功 8、失败 1，已终止不计：8/9 = 88.9%
	if result.Stats.SuccessRate != 88.9 {
		t.Fatalf("success_rate 应为 88.9，实际 %v", result.Stats.SuccessRate)
	}
	// 9 个耗时 2..10：ceil(0.9×9)=9 → 第 9 个 = 10
	if result.Stats.P90Duration != 10 {
		t.Fatalf("p90 应为 10，实际 %v", result.Stats.P90Duration)
	}
	if result.Stats.AvgDuration != 6 {
		t.Fatalf("avg_duration 应为 6，实际 %v", result.Stats.AvgDuration)
	}
	if len(result.Recent) != 10 || !result.Recent[0].StartedAt.Equal(day(18, 3)) || !result.Recent[9].StartedAt.Equal(day(24, 9)) {
		t.Fatalf("recent 应为旧 → 新，实际首尾 %v / %v", result.Recent[0].StartedAt, result.Recent[len(result.Recent)-1].StartedAt)
	}
}

func TestPercentile90(t *testing.T) {
	if percentile90(nil) != 0 {
		t.Fatal("没有样本应返回 0")
	}
	samples := make([]float64, 0, 100)
	for i := 100; i >= 1; i-- {
		samples = append(samples, float64(i))
	}
	if got := percentile90(samples); got != 90 {
		t.Fatalf("1..100 的 P90 应为 90，实际 %v", got)
	}
	if samples[0] != 100 {
		t.Fatal("不应原地改动调用方的切片")
	}
}

func TestPickAttentionDetail(t *testing.T) {
	log := strings.Join([]string{
		"=== 开始执行 [2026-11-24 20:00:00] ===",
		"正在请求接口",
		"\x1b[31mError: 连接超时（30s）\x1b[0m",
		"重试结束",
		"",
		"=== 执行结束 [2026-11-24 20:00:31] 耗时 31.00 秒 退出码 1 ===",
		"",
	}, "\n")
	if got := PickAttentionDetail(log); got != "Error: 连接超时（30s）" {
		t.Fatalf("应取末尾第一条错误行并去掉颜色码，实际 %q", got)
	}
	if got := PickAttentionDetail("第一行\n最后一行\n"); got != "最后一行" {
		t.Fatalf("没有错误特征时应退回最后一条非空行，实际 %q", got)
	}
	long := "error: " + strings.Repeat("长", 200)
	if got := PickAttentionDetail(long); len([]rune(got)) != AttentionDetailMaxRunes {
		t.Fatalf("应截到 %d 字，实际 %d", AttentionDetailMaxRunes, len([]rune(got)))
	}
	if PickAttentionDetail("  \n ") != "" {
		t.Fatal("空日志应返回空串")
	}
}
