package service

import (
	"log"
	"sync"
	"time"

	"daidai-panel/config"
	"daidai-panel/database"
	"daidai-panel/model"

	"gorm.io/gorm"
)

var (
	logCleanupOnce sync.Once
	logCleanupStop chan struct{}
)

// StartLogCleanupWorker 启动周期清理后台 worker：
// 启动后延迟一小段时间先清一次，之后每 6 小时清理一次。
// 清理「数据库 TaskLog 旧记录」与「磁盘旧 .log 文件」（按 log_retention_days 判定，无开关），
// 以及「已过期的 token 黑名单行」「已过期的登录失败计数」「90 天前的登录日志」。
func StartLogCleanupWorker() {
	logCleanupOnce.Do(func() {
		logCleanupStop = make(chan struct{})
		go logCleanupLoop()
		log.Println("log cleanup worker started (interval: 6h)")
	})
}

func StopLogCleanupWorker() {
	if logCleanupStop != nil {
		close(logCleanupStop)
	}
}

func logCleanupLoop() {
	ticker := time.NewTicker(6 * time.Hour)
	defer ticker.Stop()

	// 启动延迟，避免与启动迁移争抢
	time.Sleep(60 * time.Second)
	runPeriodicCleanup()

	for {
		select {
		case <-ticker.C:
			runPeriodicCleanup()
		case <-logCleanupStop:
			return
		}
	}
}

// runPeriodicCleanup 汇总挂在这个 6 小时 ticker 上的全部清理动作。
// 新增周期性清理请加在这里，不要再另起一个 goroutine + ticker。
func runPeriodicCleanup() {
	cleanupOldLogs()
	cleanupExpiredTokenBlocklist()
	cleanupLoginSecurityTables()
}

// cleanupLoginSecurityTables 清理登录相关的两张表：过期的失败计数行、90 天前的登录日志。
// 这两张表都由免登录的 /auth/login 写入，不清理就会被持续的登录尝试无限撑大。
func cleanupLoginSecurityTables() {
	if removed := CleanExpiredAttempts(); removed > 0 {
		log.Printf("login security cleanup: removed %d expired login attempts", removed)
	}
	if removed := CleanOldLoginLogs(); removed > 0 {
		log.Printf("login security cleanup: removed %d login logs older than %d days", removed, LoginLogRetentionDays)
	}
}

// cleanupExpiredTokenBlocklist 清掉已经过期、留着也不改变鉴权结果的 token 黑名单行。
func cleanupExpiredTokenBlocklist() {
	removed, err := CleanExpiredTokenBlocklist()
	if err != nil {
		log.Printf("token blocklist cleanup: delete expired rows failed: %v", err)
		return
	}
	if removed > 0 {
		log.Printf("token blocklist cleanup: removed %d expired rows", removed)
	}
}

// CleanLogsOlderThan 按保留天数一次性清掉过期日志：删 DB 行 + 删对应磁盘文件 + 清空目录，
// 返回（删掉的记录数, 删掉的文件数）。
//
// 【为什么要有这个函数】issue #144 之前三个入口各做各的：自动清理删行也删文件、
// 执行日志页「清理日志」只删行、定时任务页「清理日志」只删文件——同一句「清理日志」
// 在三个地方是三种结果，用户从哪个入口点下去完全不可预期。现在三处统一走这里（v3.3.2）。
//
// 🔴 WHERE 必须写成 (status IS NULL OR status <> ?)，绝不能只写 status <> ?：
// task_logs.status 是可空列（model.TaskLog.Status 是 *int），SQL 三值逻辑下
// NULL <> 2 求值为 NULL 而不是 TRUE，只写后者会把所有 status 为 NULL 的历史行整批漏掉、
// 永远清不干净——而且这种漏是静默的，接口照样返回成功。
//
// 排除 running 是为了不动正在跑的那次执行的日志行；磁盘侧还有 IsStreamOpen 兜第二道。
func CleanLogsOlderThan(days int) (int64, int) {
	if days < 1 {
		days = 1
	}

	logDir := ""
	if config.C != nil {
		logDir = config.C.Data.LogDir
	}

	deletedRecords, deletedFiles := cleanExpiredTaskLogRows("", nil, days, logDir)
	if logDir != "" {
		// 再扫一遍盘：上面按 log_path 只能删到「还有 DB 行」的那部分，
		// 「行早就没了、文件还在」的存量垃圾必须靠 ModTime 扫描才找得到。
		deletedFiles += CleanOldLogs(logDir, days)
	}

	return deletedRecords, deletedFiles
}

// cleanExpiredTaskLogRows 删掉一批过期的 task_logs 行，并连带删掉它们的磁盘日志文件，
// 返回（删掉的记录数, 删掉的文件数）。删行经 DeleteTaskLogs，先并入执行趋势的按天计数（#158）；
// 删行失败时整批回滚、返回 (0, 0)，也不按 log_path 删文件。
//
// extraCond / extraArg 是额外的任务范围限定（"task_id = ?" 或 "task_id NOT IN ?"），
// 传空串表示不限定、整张表一起算。按任务分组清理（issue #144 / v3.3.2）之后有三个调用点
// 共用这段逻辑，上面那条 WHERE 的写法太容易写错，只留一份。
//
// 🔴 WHERE 必须写成 (status IS NULL OR status <> ?)，绝不能只写 status <> ?：
// task_logs.status 是可空列（model.TaskLog.Status 是 *int），SQL 三值逻辑下
// NULL <> 2 求值为 NULL 而不是 TRUE，只写后者会把所有 status 为 NULL 的历史行整批漏掉、
// 永远清不干净——而且这种漏是静默的，接口照样返回成功。
//
// 排除 running 是为了不动正在跑的那次执行的日志行；磁盘侧还有 IsStreamOpen 兜第二道。
func cleanExpiredTaskLogRows(extraCond string, extraArg interface{}, days int, logDir string) (int64, int) {
	if database.DB == nil {
		return 0, 0
	}
	cutoff := time.Now().AddDate(0, 0, -days)

	// 归档（并入执行趋势的按天计数，#158）→ 取 log_path → 删行，全在 DeleteTaskLogs 里、同一个事务内用同一条 WHERE，
	// 所以条件只拼这一份。extraCond 有 "task_id = ?" 与 "task_id NOT IN ?" 两种，后者的参数是切片，GORM 会展开成 (?,?,…)。
	where := "started_at < ? AND (status IS NULL OR status <> ?)"
	args := []interface{}{cutoff, model.LogStatusRunning}
	if extraCond != "" {
		where += " AND " + extraCond
		args = append(args, extraArg)
	}

	var paths []string
	var deletedRecords int64
	if err := database.DB.Transaction(func(tx *gorm.DB) error {
		var err error
		deletedRecords, paths, err = DeleteTaskLogs(tx, where, args...)
		return err
	}); err != nil {
		// 事务已整体回滚：行和计数都没动。这一轮也不按 log_path 删文件，免得删出「行还在、文件没了」。
		log.Printf("log cleanup: delete TaskLog records failed: %v", err)
		return 0, 0
	}

	// 事务提交之后再按路径删磁盘文件。
	return deletedRecords, DeleteLogFilesForRecords(paths, logDir)
}

// CleanLogsByRetentionPolicy 按「任务自己设了天数就用它、没设的跟随全局」分组清理过期日志，
// 返回（删掉的记录数, 删掉的文件数）。issue #144：每分钟跑一次的任务几天就能堆出上万个日志文件，
// 全局一刀切的天数对它来说太长（v3.3.2）。
//
// 🔴 刻意只给自动清理 worker 用，手动清理的两个入口仍走 CleanLogsOlderThan：
// 手动清理是用户明确指定「保留最近 N 天」的一次性动作，把任务级天数掺进去会删掉用户刚说要留的日志
// （某任务设了 1 天，用户点清理 30 天，结果它 1 天前的日志全没了）——那是越权删除，不是功能。
func CleanLogsByRetentionPolicy(globalDays int) (int64, int) {
	if globalDays < 1 {
		globalDays = 1
	}

	logDir := ""
	if config.C != nil {
		logDir = config.C.Data.LogDir
	}

	// 覆盖表只装真正设了任务级天数的任务；> 0 顺带挡掉手工改库塞进来的 0 和负数。
	var overrides []struct {
		ID               uint
		LogRetentionDays int
	}
	if database.DB != nil {
		database.DB.Model(&model.Task{}).
			Select("id", "log_retention_days").
			Where("log_retention_days IS NOT NULL AND log_retention_days > 0").
			Find(&overrides)
	}

	var totalRecords int64
	var totalFiles int
	scanDays := globalDays
	overrideIDs := make([]uint, 0, len(overrides))
	for _, item := range overrides {
		overrideIDs = append(overrideIDs, item.ID)
		if item.LogRetentionDays > scanDays {
			scanDays = item.LogRetentionDays
		}
		records, files := cleanExpiredTaskLogRows("task_id = ?", item.ID, item.LogRetentionDays, logDir)
		totalRecords += records
		totalFiles += files
	}

	// 剩下的行按全局天数清：没设任务级天数的任务，以及任务已经被删掉、只剩日志行的孤儿行。
	// 🔴 分组依据只认 task_logs.task_id，刻意不从目录名解析 task_<ID>：恢复备份时任务会重新分配
	// ID（backup_runtime.go 的 restoreTasks 把 item.ID 置 0），而 log_path 与磁盘目录是原样拷回的，
	// 按目录名取天数会把 A 任务的设置套到 B 任务头上。
	restCond := ""
	var restArg interface{}
	if len(overrideIDs) > 0 {
		restCond = "task_id NOT IN ?"
		restArg = overrideIDs
	}
	records, files := cleanExpiredTaskLogRows(restCond, restArg, globalDays, logDir)
	totalRecords += records
	totalFiles += files

	if logDir != "" {
		// 再扫一遍盘兜「DB 行早就没了、文件还在」的存量垃圾，这类文件只有 ModTime 扫描才找得到。
		//
		// 🔴 扫盘天数取 max(全局, 所有任务级天数)，不是全局天数：扫盘只看文件时间、认不出这个文件
		// 属于哪个任务，若某个任务把保留天数调得比全局长，用全局天数扫会出现「行还在、文件没了」，
		// 用户点开日志看到的是一片空白。宁可让孤儿垃圾多躺几天，也不能删掉还有人要的文件。
		//
		// 代价：一旦有任务把天数设得比全局长，孤儿垃圾也要等到那个更长的天数才会被扫走。
		// 本 issue 的主诉求（给高频任务调短）不受影响——那种情况下 max 就等于全局天数，扫盘行为不变。
		totalFiles += CleanOldLogs(logDir, scanDays)
	}

	return totalRecords, totalFiles
}

// cleanupOldLogs 按 log_retention_days 清理过期日志（DB 记录 + 磁盘文件），
// 任务自己设了 log_retention_days 的按它各自的天数算。
func cleanupOldLogs() {
	days := model.GetRegisteredConfigInt("log_retention_days")
	if days < 1 {
		days = 1
	}

	deletedRecords, deletedFiles := CleanLogsByRetentionPolicy(days)
	log.Printf("log cleanup: removed %d TaskLog records and %d log files (retention: %d days)", deletedRecords, deletedFiles, days)
}
