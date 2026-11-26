package handler_test

import (
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"daidai-panel/database"
	"daidai-panel/model"
	"daidai-panel/testutil"

	"gorm.io/gorm"
)

// TestTaskLogHotQueriesUseQueryIndexes 守住 issue #153 补的三个索引真的被热路径用上。
//
// 以后有人给这些查询加个条件、换个排序，或者对 created_at 包一层 date()，索引就会悄悄失效：
// 结果照样对、其它测试照样全绿，只有日志多的用户那边又回到「每条查询读一遍整库日志正文」。
// 所以这里对真实 handler 发出的每一条 task_logs 查询跑 EXPLAIN QUERY PLAN：
//   - 仪表板与角标的计数：只读 idx_task_logs_created_at_status（SEARCH … COVERING INDEX，不回表）；
//   - 仪表板最近 10 条：顺着 idx_task_logs_created_at_status 倒序取，不能出现 USE TEMP B-TREE；
//   - 仪表板读执行趋势计数表（#158）：按主键 day 取一段范围（SEARCH … USING INDEX sqlite_autoindex_task_log_daily_stats_1）；
//   - 日志列表首页：顺着 idx_task_logs_started_at_status 倒序取，不能出现 USE TEMP B-TREE；
//   - latest-log：只断言用上了 idx_task_logs_task_id_started_at。GORM 的 First() 会在 ORDER BY 后面追加主键 id，
//     started_at DESC 加 id ASC 方向不一致，任何普通索引都覆盖不了，所以允许出现
//     USE TEMP B-TREE FOR RIGHT PART / LAST TERM OF ORDER BY（只是给同一个任务的几行排序）。
//
// SQL 与绑定参数都从真实请求里原样截下来（同一个回调挂在 gorm:query 与 gorm:row 之后，两条查询回调链都截），
// 再用 database/sql 带同一组参数跑 EXPLAIN，time.Time 由驱动绑成带偏移的文本，和生产完全一致。
// 不要改成手写日期字符串：GORM 日志里的时间是去掉了偏移的。
func TestTaskLogHotQueriesUseQueryIndexes(t *testing.T) {
	testutil.SetupTestEnv(t)
	engine := newProtectedRouter()
	user := testutil.MustCreateUser(t, "task-log-plan", "admin")
	token := testutil.MustCreateAccessToken(t, user.Username, user.Role)

	// 造一个任务和几行日志，让 latest-log、recent_logs 的 Preload 都走正常路径。
	// 执行计划与数据量无关（库里没有 ANALYZE 统计，规划器按固定规则选），几行就够。
	task := &model.Task{
		Name:     "task log plan guard",
		Command:  "echo ok",
		TaskType: model.TaskTypeManual,
		Status:   model.TaskStatusEnabled,
	}
	if err := database.DB.Create(task).Error; err != nil {
		t.Fatalf("create task: %v", err)
	}
	now := time.Now()
	for i, status := range []int{model.LogStatusSuccess, model.LogStatusFailed, model.LogStatusAborted} {
		logRecord := &model.TaskLog{
			TaskID:    task.ID,
			Status:    &status,
			StartedAt: now.Add(-time.Duration(i+1) * time.Minute),
		}
		if err := database.DB.Create(logRecord).Error; err != nil {
			t.Fatalf("create task log: %v", err)
		}
	}

	type capturedQuery struct {
		sql  string
		vars []interface{}
	}
	var captured []capturedQuery
	captureTaskLogQuery := func(db *gorm.DB) {
		sql := db.Statement.SQL.String()
		// 执行趋势计数表（#158）的表名不含 task_logs 子串，要单独点名截下来，仪表板读它的那条也得有执行计划断言。
		if strings.Contains(sql, "task_logs") || strings.Contains(sql, "task_log_daily_stats") {
			// 复制一份参数：这条语句执行完，GORM 会清掉 Statement 上的 SQL 与 Vars。
			captured = append(captured, capturedQuery{sql: sql, vars: append([]interface{}(nil), db.Statement.Vars...)})
		}
	}
	if err := database.DB.Callback().Query().After("gorm:query").Register("test:capture_task_log_queries", captureTaskLogQuery); err != nil {
		t.Fatalf("register query capture callback: %v", err)
	}
	// Raw().Scan()、Scan()、Row()、Rows() 不走 Query 链，走的是 Row 回调链（gorm finisher_api.go 的 Row / Rows / Scan）。
	// 只挂 Query 链的话，以后用这些写法新增的 task_logs 查询这里看不见，守护会悄悄漏掉它们。
	if err := database.DB.Callback().Row().After("gorm:row").Register("test:capture_task_log_row_queries", captureTaskLogQuery); err != nil {
		t.Fatalf("register row capture callback: %v", err)
	}
	// 两个回调都挂在全局的 database.DB 上，用例结束就摘掉，不指望 SetupTestEnv 下一次换掉连接时顺带清掉。
	t.Cleanup(func() {
		_ = database.DB.Callback().Query().Remove("test:capture_task_log_queries")
		_ = database.DB.Callback().Row().Remove("test:capture_task_log_row_queries")
	})

	queriesOf := func(t *testing.T, path string) []capturedQuery {
		t.Helper()
		captured = nil
		rec := performJSONRequest(engine, http.MethodGet, path, `{}`, map[string]string{"Authorization": "Bearer " + token}, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: expected 200, got %d body=%s", path, rec.Code, rec.Body.String())
		}
		return captured
	}

	sqlDB, err := database.DB.DB()
	if err != nil {
		t.Fatalf("get sql.DB: %v", err)
	}
	planOf := func(t *testing.T, query capturedQuery) string {
		t.Helper()
		// 直接走 database/sql：GORM 执行查询时也是把这段 SQL 和这组 Vars 原样交给 ConnPool.QueryContext。
		rows, err := sqlDB.Query("EXPLAIN QUERY PLAN "+query.sql, query.vars...)
		if err != nil {
			t.Fatalf("explain %s: %v", query.sql, err)
		}
		defer rows.Close()
		var details []string
		for rows.Next() {
			var id, parent, notUsed int
			var detail string
			if err := rows.Scan(&id, &parent, &notUsed, &detail); err != nil {
				t.Fatalf("scan query plan of %s: %v", query.sql, err)
			}
			details = append(details, detail)
		}
		if err := rows.Err(); err != nil {
			t.Fatalf("read query plan of %s: %v", query.sql, err)
		}
		return strings.Join(details, " | ")
	}

	// 仪表板（7 天视图）：9 条计数（今日 1 + 昨日 1 + 按天 7；v4.0.2 起同一时间段的总数与三种状态合成一条 count(CASE …)，
	// 以前是今日 4 + 昨日 4 + 按天 7×3 = 29 条）、1 条最近 10 条，
	// 加 1 条读执行趋势计数表（#158，已删日志的按天计数，按主键 day 取一段范围）。
	// 条数也要对上，否则截获机制失灵、一条都没截到时这个用例会空跑通过。
	counts, recent, archives := 0, 0, 0
	for _, query := range queriesOf(t, "/api/v1/system/dashboard?range=7") {
		plan := planOf(t, query)
		switch {
		case strings.HasPrefix(query.sql, "SELECT count(*)"):
			counts++
			if !strings.Contains(plan, "SEARCH task_logs USING COVERING INDEX idx_task_logs_created_at_status") {
				t.Fatalf("仪表板计数应只读 idx_task_logs_created_at_status，实际计划：%s\nSQL：%s", plan, query.sql)
			}
		case strings.Contains(query.sql, "ORDER BY created_at DESC"):
			recent++
			if !strings.Contains(plan, "idx_task_logs_created_at_status") || strings.Contains(plan, "USE TEMP B-TREE") {
				t.Fatalf("仪表板最近 10 条应顺着 idx_task_logs_created_at_status 倒序取、不再排序，实际计划：%s\nSQL：%s", plan, query.sql)
			}
		case strings.Contains(query.sql, "task_log_daily_stats"):
			archives++
			if !strings.Contains(plan, "SEARCH task_log_daily_stats USING INDEX sqlite_autoindex_task_log_daily_stats_1") {
				t.Fatalf("仪表板读执行趋势计数表应按主键 day 范围取，实际计划：%s\nSQL：%s", plan, query.sql)
			}
		default:
			// 仪表板上新增或改写的 task_logs / 计数表查询，先看一眼执行计划，再在这里补上对应的断言。
			t.Fatalf("仪表板出现了没有执行计划断言的 task_logs / 计数表查询：%s\n实际计划：%s", query.sql, plan)
		}
	}
	if counts != 9 || recent != 1 || archives != 1 {
		t.Fatalf("仪表板 7 天视图应截到 9 条 task_logs 计数、1 条最近 10 条、1 条计数表查询，实际 %d 条、%d 条、%d 条", counts, recent, archives)
	}

	// 侧栏角标：每个标签页 30 秒轮询一次的「今日失败」。
	badgeQueries := queriesOf(t, "/api/v1/system/badges")
	if len(badgeQueries) != 1 {
		t.Fatalf("角标应只有 1 条 task_logs 查询，实际 %d 条", len(badgeQueries))
	}
	if plan := planOf(t, badgeQueries[0]); !strings.Contains(plan, "SEARCH task_logs USING COVERING INDEX idx_task_logs_created_at_status") {
		t.Fatalf("角标今日失败计数应只读 idx_task_logs_created_at_status，实际计划：%s\nSQL：%s", plan, badgeQueries[0].sql)
	}

	// 日志列表首页（页面上有运行中的任务时每 5 秒刷新一次）：分页那条按 started_at 倒序。
	// 同一个请求里的连表计数不在 #153 的范围内，这里不断言。
	pageQueries := 0
	for _, query := range queriesOf(t, "/api/v1/logs?page=1&page_size=20") {
		if !strings.Contains(query.sql, "ORDER BY task_logs.started_at DESC") {
			continue
		}
		pageQueries++
		if plan := planOf(t, query); !strings.Contains(plan, "idx_task_logs_started_at_status") || strings.Contains(plan, "USE TEMP B-TREE") {
			t.Fatalf("日志列表首页应顺着 idx_task_logs_started_at_status 倒序取、不再排序，实际计划：%s\nSQL：%s", plan, query.sql)
		}
	}
	if pageQueries != 1 {
		t.Fatalf("日志列表应截到 1 条按 started_at 倒序的分页查询，实际 %d 条", pageQueries)
	}

	// latest-log：打开日志弹窗时预取、等待链里每秒一次。
	latestQueries := queriesOf(t, "/api/v1/tasks/"+strconv.FormatUint(uint64(task.ID), 10)+"/latest-log")
	if len(latestQueries) != 1 {
		t.Fatalf("latest-log 应只有 1 条 task_logs 查询，实际 %d 条", len(latestQueries))
	}
	if plan := planOf(t, latestQueries[0]); !strings.Contains(plan, "idx_task_logs_task_id_started_at") {
		t.Fatalf("latest-log 应按 idx_task_logs_task_id_started_at 找该任务的日志，实际计划：%s\nSQL：%s", plan, latestQueries[0].sql)
	}
}
