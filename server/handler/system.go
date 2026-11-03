package handler

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"daidai-panel/config"
	"daidai-panel/database"
	"daidai-panel/middleware"
	"daidai-panel/model"
	"daidai-panel/pkg/response"
	"daidai-panel/service"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

const (
	systemHealthLastCheckedAtKey = "system_health_last_checked_at"
	systemHealthLastResultKey    = "system_health_last_result_json"
)

type systemHealthCheckItem struct {
	Name    string `json:"name"`
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
}

type systemHealthSnapshot struct {
	Items         []systemHealthCheckItem `json:"items"`
	LastCheckedAt string                  `json:"last_checked_at,omitempty"`
}

var (
	// systemHealthCheckHTTPClient 默认为 nil，运行时按需用 service.NewHTTPClient 构建，
	// 这样能实时读取 system_configs.proxy_url，让 "网络代理" 配置变更立即对健康检查生效。
	// 单元测试可直接赋值非 nil 客户端进行 mock。
	systemHealthCheckHTTPClient *http.Client
	systemHealthCheckURL        = "https://www.baidu.com"
	// systemHealthGetResourceInfo 默认走真实资源采集，
	// 测试里可替换成固定值，避免健康检查回归用例受当前机器状态影响。
	systemHealthGetResourceInfo = service.GetResourceInfo
)

func resolveSystemHealthCheckClient() *http.Client {
	if systemHealthCheckHTTPClient != nil {
		return systemHealthCheckHTTPClient
	}
	return service.NewHTTPClient(3 * time.Second)
}

type SystemHandler struct{}

func NewSystemHandler() *SystemHandler {
	return &SystemHandler{}
}

// systemInfoResponse 在原有资源快照之上追加两项「部署形态」字段与面板版本号。
//
// 必须用匿名嵌入而不是 {"resource": info, ...}：ResourceInfo 的字段会被 encoding/json
// 平铺到同一层，老字段的位置和名字一个都不变。/system/info 是独立发版的 Flutter APP
// 也在读的接口，换成嵌套结构等于单方面改协议。
type systemInfoResponse struct {
	service.ResourceInfo
	// deployment_type 取值与 CheckUpdate 的 update_target.deployment_type 同一套：
	// docker / binary / magisk。
	DeploymentType string `json:"deployment_type"`
	// magisk_shell_version 是 Magisk/service.sh 注入的模块外壳版本；
	// 非模块版、或旧外壳（v3.0.3 之前根本没 export 过）都是 0。
	MagiskShellVersion int `json:"magisk_shell_version"`
	// version 是面板版本号（#139）：开放 API / MCP 的调用方拿一次 /system/info 就能
	// 判断对端能力，不必再单独请求 /system/version。
	Version string `json:"version"`
}

// Info 返回资源快照，外加两项「部署形态」字段与面板版本号。
//
// 为什么部署形态要挂在这里：前端判断「要不要显示模块版专属功能」原本只能从
// CheckUpdate 的 update_target.deployment_type 里拿，而那个接口必须联网拉 GitHub
// Release，前端还把它包在 v-if="updateInfo.has_update" 里 —— 没有新版本时前端
// 根本拿不到部署形态。外壳版本号更是完全没有 API 出口。
//
// 挂在 /system/info 而【不是】/system/panel-settings：后者注册在 JWTAuth 组之外，
// 是完全公开的接口，没必要把部署形态和外壳版本暴露给未登录访客。
func (h *SystemHandler) Info(c *gin.Context) {
	response.Success(c, gin.H{"data": systemInfoResponse{
		ResourceInfo:       systemHealthGetResourceInfo(),
		DeploymentType:     detectPanelDeploymentTypeHint(),
		MagiskShellVersion: resolveMagiskShellVersion(),
		Version:            Version,
	}})
}

// MachineCode 单独返回面板机器码，便于外部工具通过接口直接获取（无需解析完整系统信息）。
func (h *SystemHandler) MachineCode(c *gin.Context) {
	code := service.EnsureMachineCode()
	response.Success(c, gin.H{"data": gin.H{"machine_code": code}})
}

func (h *SystemHandler) Dashboard(c *gin.Context) {
	rangeDays := 7
	if r := c.Query("range"); r != "" {
		if n, err := strconv.Atoi(r); err == nil && n > 0 && n <= 90 {
			rangeDays = n
		}
	}

	type DailyStat struct {
		Date    string `json:"date"`
		Success int64  `json:"success"`
		Failed  int64  `json:"failed"`
		Aborted int64  `json:"aborted"`
	}

	now := time.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	yesterday := today.AddDate(0, 0, -1)

	var taskCount, enabledTasks, runningTasks int64
	var todayLogs, successLogs, failedLogs, abortedLogs int64
	var envCount, subCount, prevTaskCount int64
	var yesterdayLogs, yesterdaySuccess, yesterdayFailed, yesterdayAborted int64
	var recentLogs []model.TaskLog
	var dailyStats []DailyStat

	// #158：一次请求的全部读取放进同一个只读事务（同一个 WAL 快照）。删日志可能排进任意两条查询之间：
	// 落在「读计数表」和「按天现算」之间，被删的行两边都数不到；落在「现算」和「读计数表」之间又会算两次。
	// 只读事务不升级写锁，不会撞 517。事务里只能用 tx：连接池只有一条连接，再碰 database.DB 会自己等自己。
	// 下面的查询与改动前逐条相同（顺序、条件一字不改），#153 的口径锁与执行计划护栏照旧成立。
	_ = database.DB.Transaction(func(tx *gorm.DB) error {
		tx.Model(&model.Task{}).Count(&taskCount)
		tx.Model(&model.Task{}).Where("status = ?", model.TaskStatusEnabled).Count(&enabledTasks)
		tx.Model(&model.Task{}).Where("status = ?", model.TaskStatusRunning).Count(&runningTasks)

		tx.Model(&model.TaskLog{}).Where("created_at >= ?", today).Count(&todayLogs)
		tx.Model(&model.TaskLog{}).Where("created_at >= ? AND status = ?", today, model.LogStatusSuccess).Count(&successLogs)
		tx.Model(&model.TaskLog{}).Where("created_at >= ? AND status = ?", today, model.LogStatusFailed).Count(&failedLogs)
		tx.Model(&model.TaskLog{}).Where("created_at >= ? AND status = ?", today, model.LogStatusAborted).Count(&abortedLogs)

		tx.Model(&model.EnvVar{}).Count(&envCount)
		tx.Model(&model.Subscription{}).Count(&subCount)
		tx.Model(&model.Task{}).Where("created_at < ?", today).Count(&prevTaskCount)

		tx.Model(&model.TaskLog{}).Where("created_at >= ? AND created_at < ?", yesterday, today).Count(&yesterdayLogs)
		tx.Model(&model.TaskLog{}).Where("created_at >= ? AND created_at < ? AND status = ?", yesterday, today, model.LogStatusSuccess).Count(&yesterdaySuccess)
		tx.Model(&model.TaskLog{}).Where("created_at >= ? AND created_at < ? AND status = ?", yesterday, today, model.LogStatusFailed).Count(&yesterdayFailed)
		tx.Model(&model.TaskLog{}).Where("created_at >= ? AND created_at < ? AND status = ?", yesterday, today, model.LogStatusAborted).Count(&yesterdayAborted)

		tx.Preload("Task").Order("created_at DESC").Limit(10).Find(&recentLogs)

		// 已经删掉的日志按天留在 task_log_daily_stats（键 YYYY-MM-DD），今日 / 昨日 / 按天都把它加回来，
		// 趋势与卡片不再随日志清理变少。下界取 min(趋势第一天, 昨天)：range=1 时昨天不在趋势里，「昨日执行」卡片照样要加。
		first := today.AddDate(0, 0, -(rangeDays - 1))
		if yesterday.Before(first) {
			first = yesterday
		}
		var archivedRows []model.TaskLogDailyStat
		tx.Where("day >= ? AND day <= ?", first.Format("2006-01-02"), today.Format("2006-01-02")).Find(&archivedRows)
		archived := make(map[string]model.TaskLogDailyStat, len(archivedRows))
		for _, row := range archivedRows {
			archived[row.Day] = row
		}
		// 总数要连 Other（被删时 status 为 NULL 或还在运行中的行）一起加，与现算的 today_logs / yesterday_logs 口径一致。
		td := archived[today.Format("2006-01-02")]
		todayLogs += td.Success + td.Failed + td.Aborted + td.Other
		successLogs += td.Success
		failedLogs += td.Failed
		abortedLogs += td.Aborted
		yd := archived[yesterday.Format("2006-01-02")]
		yesterdayLogs += yd.Success + yd.Failed + yd.Aborted + yd.Other
		yesterdaySuccess += yd.Success
		yesterdayFailed += yd.Failed
		yesterdayAborted += yd.Aborted

		for i := rangeDays - 1; i >= 0; i-- {
			day := today.AddDate(0, 0, -i)
			nextDay := day.Add(24 * time.Hour)
			date := day.Format("01-02")

			var s, f, a int64
			tx.Model(&model.TaskLog{}).Where("created_at >= ? AND created_at < ? AND status = ?", day, nextDay, model.LogStatusSuccess).Count(&s)
			tx.Model(&model.TaskLog{}).Where("created_at >= ? AND created_at < ? AND status = ?", day, nextDay, model.LogStatusFailed).Count(&f)
			tx.Model(&model.TaskLog{}).Where("created_at >= ? AND created_at < ? AND status = ?", day, nextDay, model.LogStatusAborted).Count(&a)
			// 按天的三条折线只加对应的三列；Other 不进折线（与现算时 NULL / 运行中不进任何一桶一致）。
			gone := archived[day.Format("2006-01-02")]
			dailyStats = append(dailyStats, DailyStat{Date: date, Success: s + gone.Success, Failed: f + gone.Failed, Aborted: a + gone.Aborted})
		}
		return nil
	})

	recentData := make([]map[string]interface{}, len(recentLogs))
	for i, l := range recentLogs {
		recentData[i] = l.ToDict()
	}

	response.Success(c, gin.H{
		"data": gin.H{
			"task_count":        taskCount,
			"enabled_tasks":     enabledTasks,
			"running_tasks":     runningTasks,
			"today_logs":        todayLogs,
			"success_logs":      successLogs,
			"failed_logs":       failedLogs,
			"aborted_logs":      abortedLogs,
			"env_count":         envCount,
			"sub_count":         subCount,
			"prev_task_count":   prevTaskCount,
			"yesterday_logs":    yesterdayLogs,
			"yesterday_success": yesterdaySuccess,
			"yesterday_failed":  yesterdayFailed,
			"yesterday_aborted": yesterdayAborted,
			"recent_logs":       recentData,
			"daily_stats":       dailyStats,
			"range_days":        rangeDays,
		},
	})
}

func (h *SystemHandler) Stats(c *gin.Context) {
	var taskCount, enabledTasks, disabledTasks, runningTasks int64
	database.DB.Model(&model.Task{}).Count(&taskCount)
	database.DB.Model(&model.Task{}).Where("status = ?", model.TaskStatusEnabled).Count(&enabledTasks)
	database.DB.Model(&model.Task{}).Where("status = ?", model.TaskStatusDisabled).Count(&disabledTasks)
	database.DB.Model(&model.Task{}).Where("status = ?", model.TaskStatusRunning).Count(&runningTasks)

	var totalLogs, successLogs, failedLogs, abortedLogs int64
	database.DB.Model(&model.TaskLog{}).Count(&totalLogs)
	database.DB.Model(&model.TaskLog{}).Where("status = ?", model.LogStatusSuccess).Count(&successLogs)
	database.DB.Model(&model.TaskLog{}).Where("status = ?", model.LogStatusFailed).Count(&failedLogs)
	database.DB.Model(&model.TaskLog{}).Where("status = ?", model.LogStatusAborted).Count(&abortedLogs)

	successRate := 0.0
	// 成功率只统计自然完成的成功 / 失败，Aborted 单独统计，不拉低成功率。
	finishedLogs := successLogs + failedLogs
	if finishedLogs > 0 {
		successRate = float64(successLogs) / float64(finishedLogs) * 100
	}

	scriptCount := service.CountScriptFiles(config.C.Data.ScriptsDir)

	response.Success(c, gin.H{
		"data": gin.H{
			"tasks": gin.H{
				"total":    taskCount,
				"enabled":  enabledTasks,
				"disabled": disabledTasks,
				"running":  runningTasks,
			},
			"logs": gin.H{
				"total":        totalLogs,
				"success":      successLogs,
				"failed":       failedLogs,
				"aborted":      abortedLogs,
				"success_rate": successRate,
			},
			"scripts": gin.H{
				"total": scriptCount,
			},
		},
	})
}

func (h *SystemHandler) Backup(c *gin.Context) {
	var req struct {
		Password  string                  `json:"password"`
		Name      string                  `json:"name"`
		Selection service.BackupSelection `json:"selection"`
	}
	c.ShouldBindJSON(&req)

	filePath, err := service.CreateBackup(service.BackupCreateOptions{
		Password:  req.Password,
		Name:      req.Name,
		Selection: req.Selection.NormalizeDefaults(),
	})
	if err != nil {
		response.InternalError(c, "备份失败: "+err.Error())
		return
	}
	response.Success(c, gin.H{"message": "备份成功", "data": gin.H{"path": filePath}})
}

func (h *SystemHandler) BackupList(c *gin.Context) {
	backups, err := service.ListBackups()
	if err != nil {
		response.InternalError(c, "获取备份列表失败")
		return
	}
	response.Success(c, gin.H{"data": backups})
}

func (h *SystemHandler) Restore(c *gin.Context) {
	var req struct {
		Filename string `json:"filename" binding:"required"`
		Password string `json:"password"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "请求参数错误")
		return
	}

	if err := service.RestoreBackup(req.Filename, req.Password); err != nil {
		response.InternalError(c, "恢复失败: "+err.Error())
		return
	}
	response.Success(c, gin.H{"message": "恢复成功"})
}

func (h *SystemHandler) RestoreProgress(c *gin.Context) {
	response.Success(c, gin.H{"data": service.CurrentRestoreProgress()})
}

func (h *SystemHandler) DeleteBackup(c *gin.Context) {
	filename := c.Query("filename")
	if filename == "" {
		response.BadRequest(c, "文件名不能为空")
		return
	}
	service.DeleteBackup(filename)
	response.Success(c, gin.H{"message": "删除成功"})
}

func (h *SystemHandler) UploadBackup(c *gin.Context) {
	file, err := c.FormFile("file")
	if err != nil {
		response.BadRequest(c, "请选择备份文件")
		return
	}

	if file.Size > 512*1024*1024 {
		response.BadRequest(c, "文件过大，最大 512MB")
		return
	}

	filename := filepath.Base(file.Filename)
	lowerName := strings.ToLower(filename)
	if !strings.HasSuffix(lowerName, ".json") &&
		!strings.HasSuffix(lowerName, ".enc") &&
		!strings.HasSuffix(lowerName, ".tgz") &&
		!strings.HasSuffix(lowerName, ".tar.gz") {
		response.BadRequest(c, "仅支持 .json、.enc、.tgz 或 .tar.gz 备份文件")
		return
	}

	src, err := file.Open()
	if err != nil {
		response.InternalError(c, "读取上传文件失败")
		return
	}
	defer src.Close()

	// 先落成临时文件、校验内容，通过才改名为正式备份；同名已存在时自动改名，响应里返回实际文件名。
	savedName, err := service.StoreUploadedBackup(src, filename)
	if err != nil {
		if errors.Is(err, service.ErrBackupUploadInvalid) {
			response.BadRequest(c, err.Error())
			return
		}
		response.InternalError(c, "保存文件失败")
		return
	}

	response.Success(c, gin.H{"message": "上传成功", "data": gin.H{"filename": savedName}})
}

func (h *SystemHandler) DownloadBackup(c *gin.Context) {
	filename := c.Param("filename")
	if filename == "" {
		filename = c.Query("filename")
	}
	if filename == "" {
		response.BadRequest(c, "文件名不能为空")
		return
	}

	baseName := filepath.Base(filename)
	if baseName != filename || strings.TrimSpace(baseName) == "." || strings.TrimSpace(baseName) == string(filepath.Separator) {
		response.BadRequest(c, "备份文件名无效")
		return
	}

	lowerName := strings.ToLower(baseName)
	if !strings.HasSuffix(lowerName, ".json") &&
		!strings.HasSuffix(lowerName, ".enc") &&
		!strings.HasSuffix(lowerName, ".tgz") &&
		!strings.HasSuffix(lowerName, ".tar.gz") {
		response.BadRequest(c, "仅支持下载 .json、.enc、.tgz 或 .tar.gz 备份文件")
		return
	}

	backupDir := filepath.Join(config.C.Data.Dir, "backups")
	filePath := filepath.Join(backupDir, baseName)
	info, err := os.Stat(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			response.NotFound(c, "备份文件不存在")
			return
		}
		response.InternalError(c, "读取备份文件失败")
		return
	}
	if info.IsDir() {
		response.BadRequest(c, "备份文件名无效")
		return
	}

	c.Header("Cache-Control", "no-store, no-cache, must-revalidate")
	c.Header("Pragma", "no-cache")
	c.Header("Expires", "0")
	c.Header("Content-Length", fmt.Sprintf("%d", info.Size()))
	c.FileAttachment(filePath, baseName)
}

func (h *SystemHandler) Version(c *gin.Context) {
	response.Success(c, gin.H{
		"data": gin.H{
			"version":     Version,
			"api_version": "v1",
			"framework":   "gin",
			"go_version":  service.GetResourceInfo().GoVersion,
		},
	})
}

func (h *SystemHandler) PublicVersion(c *gin.Context) {
	response.Success(c, gin.H{
		"version": Version,
		"data": gin.H{
			"version": Version,
		},
	})
}

func (h *SystemHandler) PanelSettings(c *gin.Context) {
	title := model.GetRegisteredConfig("panel_title")
	icon := model.GetRegisteredConfig("panel_icon")
	editorBackgroundColor := model.GetRegisteredConfig("editor_background_color")
	logBackgroundColor := model.GetRegisteredConfig("log_background_color")
	logBackgroundImage := model.GetRegisteredConfig("log_background_image")
	panelRuntimeMode := service.ResolvePanelRuntimeMode()
	panelServiceManager := model.GetRegisteredConfig("panel_service_manager")
	panelServiceName := model.GetRegisteredConfig("panel_service_name")
	// 界面圆角风格。这个接口在 JWTAuth 之外，是 Web 首屏唯一能拿到外观配置的地方
	// （/api/configs 要登录后才能读），不在这里下发的话前端 applyPanelAppearance()
	// 永远拿不到值，设置里的圆角开关就只能靠 localStorage 缓存、换台设备就失效。
	panelShapeStyle := model.GetRegisteredConfig("panel_shape_style")
	response.Success(c, gin.H{
		"data": gin.H{
			"panel_title":             title,
			"panel_icon":              icon,
			"editor_background_color": editorBackgroundColor,
			"log_background_color":    logBackgroundColor,
			"log_background_image":    logBackgroundImage,
			"panel_runtime_mode":      panelRuntimeMode,
			"panel_service_manager":   panelServiceManager,
			"panel_service_name":      panelServiceName,
			"panel_shape_style":       panelShapeStyle,
		},
	})
}

func (h *SystemHandler) CheckUpdate(c *gin.Context) {
	currentVersion := Version

	release, err := fetchLatestPanelRelease()
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}

	latestVersion := release.version()
	hasUpdate := compareVersions(currentVersion, latestVersion)
	autoUpdateSupported := true
	updateDisabledReason := ""
	updateTarget := gin.H{}
	watchtowerCfg := currentWatchtowerRuntimeConfig()

	if watchtowerCfg.Managed {
		imageName := strings.TrimSpace(os.Getenv("IMAGE_NAME"))
		pinnedImage := isPinnedPanelImageReference(imageName)
		autoUpdateSupported = watchtowerCfg.ManualTriggerSupported && !pinnedImage
		if pinnedImage {
			updateDisabledReason = "当前容器使用固定版本标签或 digest，Watchtower 不会把它切换到后续版本；请先把 DAIDAI_PANEL_IMAGE 改为同系列浮动标签"
		} else if !watchtowerCfg.ManualTriggerSupported {
			updateDisabledReason = "当前由 Watchtower 托管自动更新；面板可展示更新状态，但未配置 Watchtower HTTP API 手动触发能力"
		}
		updateTarget = buildWatchtowerUpdateTarget(watchtowerCfg)
	} else {
		plan, planErr := buildPanelUpdatePlanForRelease(release)
		if planErr != nil {
			autoUpdateSupported = false
			updateDisabledReason = planErr.Error()
			// 构建方案失败时也要把「当前是什么部署形态」告诉前端。
			// 否则前端只能看到一个空对象，就会退回到「请在宿主机执行 docker compose pull」
			// 这种兜底文案——对 Android 模块版和裸机二进制部署都是纯粹的误导。
			updateTarget = gin.H{"deployment_type": detectPanelDeploymentTypeHint()}
		} else {
			updateTarget = buildPanelUpdateTarget(plan)
		}
	}

	response.Success(c, gin.H{
		"data": gin.H{
			"current":                currentVersion,
			"latest":                 latestVersion,
			"has_update":             hasUpdate,
			"release_name":           release.Name,
			"release_url":            release.HTMLURL,
			"release_notes":          release.Body,
			"published_at":           release.PublishedAt,
			"auto_update_supported":  autoUpdateSupported,
			"update_disabled_reason": updateDisabledReason,
			"update_target":          updateTarget,
		},
	})
}

func (h *SystemHandler) UpdatePanel(c *gin.Context) {
	plan, err := buildPanelUpdatePlan()
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	if err := panelUpdater.begin(plan); err != nil {
		respondUpdateConflict(c, err.Error())
		return
	}

	if plan.UpdateManager == panelUpdateManagerWatchtower {
		// Watchtower 的 async API 会立即返回接管结果；同步完成这一步后，
		// 页面、自动更新和 ddp CLI 都能看到同一套 completed/failed 终态。
		executePanelUpdate(plan)
		snapshot := panelUpdater.snapshotCopy()
		if snapshot.Status == "failed" {
			response.BadRequest(c, snapshot.Error)
			return
		}
		response.Success(c, gin.H{
			"message": "已触发 Watchtower 检查更新",
			"data":    snapshot,
		})
		return
	}

	go executePanelUpdate(plan)

	response.Success(c, gin.H{
		"data": panelUpdater.snapshotCopy(),
	})
}

// panelProcessExit 是「面板自己发起的退出」（重启、停止、二进制 / Magisk 在线升级）的唯一出口。
// 默认直接 os.Exit：ddp 等其它入口不接线，行为不变。面板主程序启动时用 SetPanelExitRequester
// 把它换成「请求 main 走完整关停（终止并结算任务、关库）后再按 code 退出」。
// panelProcessExitDelay 抽成变量是为了让测试不必真等 2 秒（真跑 os.Exit 会把整个 test 二进制干掉）。
var (
	panelProcessExit      = func(code int) { os.Exit(code) }
	panelProcessExitDelay = 2 * time.Second
)

// SetPanelExitRequester 由面板主程序在启动时调用，必须早于 HTTP 服务与自动更新检查这些可能触发退出的协程，
// 之后再接会有数据竞争。fn 为空时保持原样。
func SetPanelExitRequester(fn func(code int)) {
	if fn != nil {
		panelProcessExit = fn
	}
}

// Restart 只退出进程，由外部（systemd / Docker restart policy / Magisk 存活守护）拉回来。
//
// ⚠️ 这里【绝对不能】顺手写 Magisk 的停止开关。restart 的语义是「重来一次」，
// 一旦写了开关，模块版的存活守护会自退、重启手机也不会再起来 ——
// 一次正常重启就变成了永久停机，而此时 Web 已经没了，用户在面板上没有任何自救手段。
// 「停止」是另一个接口（StopPanel），两者共用退出逻辑但绝不共用停止开关。
func (h *SystemHandler) Restart(c *gin.Context) {
	response.Success(c, gin.H{"message": "面板将在 2 秒后重启"})

	go func() {
		time.Sleep(panelProcessExitDelay)
		panelProcessExit(1)
	}()
}

// StopPanel 停止面板服务，且跨重启保持停止 —— 只对 Magisk 模块版开放。
//
// 与 Restart 的差别就是多写了一个停止开关：service.sh 在开机同步完模块文件后读到它会
// 直接早退，存活守护读到它也会自退，所以进程退出后不会有任何东西把面板拉回来。
//
// 只在模块版放行：其它部署形态（Docker / systemd / 裸机二进制）的进程管理器会立刻重启
// 面板，这个接口在那些环境里等价于 restart，只会让用户误以为自己停掉了服务。
func (h *SystemHandler) StopPanel(c *gin.Context) {
	if !service.IsMagiskModuleRuntime() {
		response.BadRequest(c, "停止面板服务只支持 Magisk 模块版；其它部署形态请在宿主机停止对应的容器或服务")
		return
	}
	if shellVersion := resolveMagiskShellVersion(); shellVersion < magiskStopSupportedShellVersion {
		response.BadRequest(c, fmt.Sprintf(
			"当前模块外壳版本为 %d，不支持手动停止（需要 %d 及以上）。在线升级只替换面板程序与前端，覆盖不到模块脚本；请到 GitHub Releases 下载对应 flavor 的模块 zip 重新刷入一次",
			shellVersion, magiskStopSupportedShellVersion,
		))
		return
	}
	if err := writeMagiskStopFlag(); err != nil {
		response.InternalError(c, "写入停止开关失败："+err.Error())
		return
	}

	response.Success(c, gin.H{"message": "面板将在 2 秒后停止；重启手机也不会自动启动，恢复请到模块管理器点动作按钮"})

	go func() {
		time.Sleep(panelProcessExitDelay)
		panelProcessExit(0)
	}()
}

func (h *SystemHandler) PanelLog(c *gin.Context) {
	linesStr := c.DefaultQuery("lines", "100")
	keyword := c.Query("keyword")
	level := strings.ToLower(strings.TrimSpace(c.Query("level")))

	lines, _ := strconv.Atoi(linesStr)
	if lines <= 0 || lines > 10000 {
		lines = 100
	}

	logFile := filepath.Join(config.C.Data.Dir, "panel.log")
	file, err := os.Open(logFile)
	if err != nil {
		response.Success(c, gin.H{"data": gin.H{"logs": []string{}}})
		return
	}
	defer file.Close()

	// #159 修复 E：照旧扫完整个文件（total 仍是全文件命中筛选的行数，网页显示「共 N 行」），
	// 但只用环形缓冲留最后 lines 行：内存只和 lines（最多 10000）有关，不再把整份 panel.log 读进内存。
	ring := make([]string, lines)
	total := 0
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if level != "" && !service.MatchPanelLogLevel(line, level) {
			continue
		}
		if keyword == "" || strings.Contains(line, keyword) {
			// 第 total 条命中行放进 total%lines 号格子，绕圈时正好覆盖掉最老的那一行。
			ring[total%lines] = line
			total++
		}
	}

	// 按文件顺序取出最后 min(total, lines) 行；一行都没命中时保持 nil（JSON 为 null），与改动前逐字节一致。
	var tail []string
	if total > 0 {
		n := total
		if n > lines {
			n = lines
		}
		tail = make([]string, 0, n)
		for i := total - n; i < total; i++ {
			tail = append(tail, ring[i%lines])
		}
	}

	response.Success(c, gin.H{
		"data": gin.H{
			"logs":  tail,
			"total": total,
			"level": level,
		},
	})
}

func runSystemHealthChecks() []systemHealthCheckItem {
	items := make([]systemHealthCheckItem, 0, 4)

	if err := database.DB.Exec("SELECT 1").Error; err != nil {
		items = append(items, systemHealthCheckItem{Name: "database", Status: "error", Message: err.Error()})
	} else {
		items = append(items, systemHealthCheckItem{Name: "database", Status: "ok"})
	}

	info := systemHealthGetResourceInfo()
	memThreshold := float64(model.GetRegisteredConfigInt("memory_warn"))
	if memThreshold <= 0 {
		memThreshold = 80
	}
	if info.MemoryTotal == 0 {
		items = append(items, systemHealthCheckItem{Name: "memory", Status: "warning", Message: "资源采集不可用"})
	} else if info.MemoryUsage > memThreshold {
		items = append(items, systemHealthCheckItem{Name: "memory", Status: "warning", Message: strconv.FormatFloat(info.MemoryUsage, 'f', 1, 64) + "%"})
	} else {
		items = append(items, systemHealthCheckItem{Name: "memory", Status: "ok", Message: strconv.FormatFloat(info.MemoryUsage, 'f', 1, 64) + "%"})
	}

	if sched := service.GetScheduler(); sched != nil {
		items = append(items, systemHealthCheckItem{Name: "scheduler", Status: "ok", Message: "运行中"})
	} else if schedV2 := service.GetSchedulerV2(); schedV2 != nil {
		items = append(items, systemHealthCheckItem{Name: "scheduler", Status: "ok", Message: "运行中"})
	} else {
		items = append(items, systemHealthCheckItem{Name: "scheduler", Status: "ok", Message: "空闲"})
	}

	if resp, err := resolveSystemHealthCheckClient().Get(systemHealthCheckURL); err != nil {
		items = append(items, systemHealthCheckItem{Name: "network", Status: "error", Message: "无法连接外部网络"})
	} else {
		resp.Body.Close()
		if resp.StatusCode >= http.StatusBadRequest {
			items = append(items, systemHealthCheckItem{Name: "network", Status: "error", Message: "网络检查返回状态异常"})
		} else {
			items = append(items, systemHealthCheckItem{Name: "network", Status: "ok"})
		}
	}

	return items
}

func loadSystemHealthSnapshot() systemHealthSnapshot {
	snapshot := systemHealthSnapshot{
		Items:         []systemHealthCheckItem{},
		LastCheckedAt: strings.TrimSpace(model.GetConfig(systemHealthLastCheckedAtKey, "")),
	}

	rawItems := strings.TrimSpace(model.GetConfig(systemHealthLastResultKey, ""))
	if rawItems == "" {
		return snapshot
	}

	if err := json.Unmarshal([]byte(rawItems), &snapshot.Items); err != nil {
		snapshot.Items = []systemHealthCheckItem{}
	}

	return snapshot
}

func saveSystemHealthSnapshot(items []systemHealthCheckItem, checkedAt time.Time) error {
	rawItems, err := json.Marshal(items)
	if err != nil {
		return err
	}

	if err := model.SetConfig(systemHealthLastResultKey, string(rawItems)); err != nil {
		return err
	}
	if err := model.SetConfig(systemHealthLastCheckedAtKey, checkedAt.Format(time.RFC3339)); err != nil {
		return err
	}
	return nil
}

func buildSystemHealthSnapshot(items []systemHealthCheckItem, checkedAt string) gin.H {
	return gin.H{
		"items":           items,
		"last_checked_at": checkedAt,
	}
}

func (h *SystemHandler) HealthCheck(c *gin.Context) {
	snapshot := loadSystemHealthSnapshot()
	response.Success(c, buildSystemHealthSnapshot(snapshot.Items, snapshot.LastCheckedAt))
}

func (h *SystemHandler) RunHealthCheck(c *gin.Context) {
	items := runSystemHealthChecks()
	checkedAt := time.Now()

	if err := saveSystemHealthSnapshot(items, checkedAt); err != nil {
		response.InternalError(c, "保存健康检查结果失败: "+err.Error())
		return
	}

	response.Success(c, buildSystemHealthSnapshot(items, checkedAt.Format(time.RFC3339)))
}

func (h *SystemHandler) GetConfigScript(c *gin.Context) {
	filePath := filepath.Join(config.C.Data.Dir, "config.sh")
	data, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			response.Success(c, gin.H{"content": "", "path": "config.sh"})
			return
		}
		response.InternalError(c, "读取配置文件失败")
		return
	}
	response.Success(c, gin.H{"content": string(data), "path": "config.sh"})
}

func (h *SystemHandler) SaveConfigScript(c *gin.Context) {
	var req struct {
		Content string `json:"content"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "请求参数错误")
		return
	}
	filePath := filepath.Join(config.C.Data.Dir, "config.sh")
	if err := os.WriteFile(filePath, []byte(req.Content), 0755); err != nil {
		response.InternalError(c, "保存配置文件失败")
		return
	}
	response.Success(c, gin.H{"message": "配置文件已保存"})
}

func (h *SystemHandler) RegisterRoutes(r *gin.RouterGroup) {
	r.GET("/system/public-version", h.PublicVersion)
	r.GET("/system/panel-settings", h.PanelSettings)

	sys := r.Group("/system", middleware.JWTAuth())
	{
		sys.GET("/info", middleware.OpenAPIAccess("system"), middleware.RequireRole("viewer"), h.Info)
		sys.GET("/machine-code", middleware.OpenAPIAccess("system"), middleware.RequireRole("viewer"), h.MachineCode)
		sys.GET("/dashboard", middleware.OpenAPIAccess("system"), middleware.RequireRole("viewer"), h.Dashboard)
		sys.GET("/stats", middleware.OpenAPIAccess("system"), middleware.RequireRole("viewer"), h.Stats)
		// 侧栏角标计数：只做 COUNT、按角色裁剪、不打外网，专为高频轮询设计（见 system_badges.go）
		sys.GET("/badges", middleware.OpenAPIAccess("system"), middleware.RequireRole("viewer"), h.Badges)
		sys.GET("/version", middleware.OpenAPIAccess("system"), middleware.RequireRole("viewer"), h.Version)
		sys.GET("/check-update", middleware.OpenAPIAccess("system"), middleware.RequireRole("viewer"), h.CheckUpdate)
		sys.GET("/health-check", middleware.RequireRole("viewer"), h.HealthCheck)
		sys.POST("/health-check", middleware.RequireRole("viewer"), h.RunHealthCheck)
		sys.GET("/update-status", middleware.RequireAdmin(), h.UpdateStatus)
		sys.POST("/update", middleware.RequireAdmin(), h.UpdatePanel)
		sys.POST("/restart", middleware.RequireAdmin(), h.Restart)
		// 停止面板服务：仅 Magisk 模块版可用，handler 内部再做一次运行态与外壳版本门控。
		// 不挂 OpenAPIAccess —— 这是「让面板彻底消失」的操作，只允许登录管理员本人执行。
		sys.POST("/stop", middleware.RequireAdmin(), h.StopPanel)
		sys.GET("/panel-log", middleware.RequireUserToken(), middleware.RequireAdmin(), h.PanelLog)
		// 备份 scope 的应用令牌只放行「创建 / 列出 / 下载 / 看恢复进度」这类不改变面板现状的操作。
		// 上传、恢复、删除会覆盖或销毁数据（上传 + 恢复 = 用任意内容替换整个面板），只允许管理员账号本人：
		// RequireAdmin 要求 role 恰好是 admin，应用令牌的 role 是 app:...，直接 403。
		sys.POST("/backup", middleware.OpenAPIAccess("backup"), middleware.RequireRole("admin"), h.Backup)
		sys.POST("/backup/upload", middleware.RequireAdmin(), h.UploadBackup)
		sys.GET("/backups", middleware.OpenAPIAccess("backup"), middleware.RequireRole("admin"), h.BackupList)
		sys.GET("/backup/download", middleware.OpenAPIAccess("backup"), middleware.RequireRole("admin"), h.DownloadBackup)
		sys.GET("/backup/download/:filename", middleware.OpenAPIAccess("backup"), middleware.RequireRole("admin"), h.DownloadBackup)
		sys.GET("/restore/progress", middleware.OpenAPIAccess("backup"), middleware.RequireRole("admin"), h.RestoreProgress)
		sys.POST("/restore", middleware.RequireAdmin(), h.Restore)
		sys.DELETE("/backup", middleware.RequireAdmin(), h.DeleteBackup)
		// config.sh 里的变量会注入每个任务的运行环境（能改 PATH 之类），同样只给管理员账号本人。
		sys.GET("/config-script", middleware.RequireAdmin(), h.GetConfigScript)
		sys.PUT("/config-script", middleware.RequireAdmin(), h.SaveConfigScript)
	}
}
