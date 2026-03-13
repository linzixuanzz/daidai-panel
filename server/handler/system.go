package handler

import (
	"bufio"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
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
)

type SystemHandler struct{}

func NewSystemHandler() *SystemHandler {
	return &SystemHandler{}
}

func (h *SystemHandler) Info(c *gin.Context) {
	info := service.GetResourceInfo()
	response.Success(c, gin.H{"data": info})
}

func (h *SystemHandler) Dashboard(c *gin.Context) {
	var taskCount int64
	database.DB.Model(&model.Task{}).Count(&taskCount)

	var enabledTasks int64
	database.DB.Model(&model.Task{}).Where("status = ?", 1).Count(&enabledTasks)

	var runningTasks int64
	database.DB.Model(&model.Task{}).Where("status = ?", 2).Count(&runningTasks)

	var todayLogs int64
	today := time.Now().Truncate(24 * time.Hour)
	database.DB.Model(&model.TaskLog{}).Where("created_at >= ?", today).Count(&todayLogs)

	var successLogs int64
	database.DB.Model(&model.TaskLog{}).Where("created_at >= ? AND status = 0", today).Count(&successLogs)

	var failedLogs int64
	database.DB.Model(&model.TaskLog{}).Where("created_at >= ? AND status = 1", today).Count(&failedLogs)

	var envCount int64
	database.DB.Model(&model.EnvVar{}).Count(&envCount)

	var subCount int64
	database.DB.Model(&model.Subscription{}).Count(&subCount)

	var recentLogs []model.TaskLog
	database.DB.Preload("Task").Order("created_at DESC").Limit(10).Find(&recentLogs)

	recentData := make([]map[string]interface{}, len(recentLogs))
	for i, l := range recentLogs {
		recentData[i] = l.ToDict()
	}

	type DailyStat struct {
		Date    string `json:"date"`
		Success int64  `json:"success"`
		Failed  int64  `json:"failed"`
	}

	var dailyStats []DailyStat
	for i := 6; i >= 0; i-- {
		day := time.Now().AddDate(0, 0, -i).Truncate(24 * time.Hour)
		nextDay := day.Add(24 * time.Hour)
		date := day.Format("01-02")

		var s, f int64
		database.DB.Model(&model.TaskLog{}).Where("created_at >= ? AND created_at < ? AND status = 0", day, nextDay).Count(&s)
		database.DB.Model(&model.TaskLog{}).Where("created_at >= ? AND created_at < ? AND status = 1", day, nextDay).Count(&f)
		dailyStats = append(dailyStats, DailyStat{Date: date, Success: s, Failed: f})
	}

	response.Success(c, gin.H{
		"data": gin.H{
			"task_count":    taskCount,
			"enabled_tasks": enabledTasks,
			"running_tasks": runningTasks,
			"today_logs":    todayLogs,
			"success_logs":  successLogs,
			"failed_logs":   failedLogs,
			"env_count":     envCount,
			"sub_count":     subCount,
			"recent_logs":   recentData,
			"daily_stats":   dailyStats,
		},
	})
}

func (h *SystemHandler) Stats(c *gin.Context) {
	var taskCount, enabledTasks, disabledTasks, runningTasks int64
	database.DB.Model(&model.Task{}).Count(&taskCount)
	database.DB.Model(&model.Task{}).Where("status = ?", 1).Count(&enabledTasks)
	database.DB.Model(&model.Task{}).Where("status = ?", 0).Count(&disabledTasks)
	database.DB.Model(&model.Task{}).Where("status = ?", 2).Count(&runningTasks)

	var totalLogs, successLogs, failedLogs int64
	database.DB.Model(&model.TaskLog{}).Count(&totalLogs)
	database.DB.Model(&model.TaskLog{}).Where("status = 0").Count(&successLogs)
	database.DB.Model(&model.TaskLog{}).Where("status = 1").Count(&failedLogs)

	successRate := 0.0
	if totalLogs > 0 {
		successRate = float64(successLogs) / float64(totalLogs) * 100
	}

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
				"success_rate": successRate,
			},
		},
	})
}

func (h *SystemHandler) Backup(c *gin.Context) {
	var req struct {
		Password string `json:"password"`
	}
	c.ShouldBindJSON(&req)

	filePath, err := service.CreateBackup(req.Password)
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

func (h *SystemHandler) DeleteBackup(c *gin.Context) {
	filename := c.Query("filename")
	if filename == "" {
		response.BadRequest(c, "文件名不能为空")
		return
	}
	service.DeleteBackup(filename)
	response.Success(c, gin.H{"message": "删除成功"})
}

func (h *SystemHandler) DownloadBackup(c *gin.Context) {
	filename := c.Param("filename")
	if filename == "" {
		response.BadRequest(c, "文件名不能为空")
		return
	}

	backupDir := filepath.Join(config.C.Data.Dir, "backups")
	filePath := filepath.Join(backupDir, filepath.Base(filename))

	c.FileAttachment(filePath, filename)
}

func (h *SystemHandler) Version(c *gin.Context) {
	response.Success(c, gin.H{
		"data": gin.H{
			"version":     "0.2.0",
			"api_version": "v1",
			"framework":   "gin",
			"go_version":  service.GetResourceInfo().GoVersion,
		},
	})
}

func (h *SystemHandler) PublicVersion(c *gin.Context) {
	response.Success(c, gin.H{
		"data": gin.H{
			"version": Version,
		},
	})
}

func (h *SystemHandler) CheckUpdate(c *gin.Context) {
	currentVersion := Version

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get("https://api.github.com/repos/linzixuanzz/daidai-panel/releases/latest")
	if err != nil {
		response.InternalError(c, "检查更新失败: "+err.Error())
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		response.InternalError(c, "GitHub API 返回异常状态")
		return
	}

	var release struct {
		TagName     string `json:"tag_name"`
		Name        string `json:"name"`
		Body        string `json:"body"`
		HTMLURL     string `json:"html_url"`
		PublishedAt string `json:"published_at"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		response.InternalError(c, "解析 GitHub 响应失败")
		return
	}

	latestVersion := strings.TrimPrefix(release.TagName, "v")
	hasUpdate := latestVersion != currentVersion

	response.Success(c, gin.H{
		"data": gin.H{
			"current":       currentVersion,
			"latest":        latestVersion,
			"has_update":    hasUpdate,
			"release_url":   release.HTMLURL,
			"release_notes": release.Body,
			"published_at":  release.PublishedAt,
		},
	})
}

func (h *SystemHandler) UpdatePanel(c *gin.Context) {
	containerName := os.Getenv("CONTAINER_NAME")
	if containerName == "" {
		containerName = "daidai-panel"
	}

	imageName := os.Getenv("IMAGE_NAME")
	if imageName == "" {
		imageName = "linzixuanzz/daidai-panel:latest"
	}

	go func() {
		time.Sleep(1 * time.Second)

		pullCmd := exec.Command("docker", "pull", imageName)
		if err := pullCmd.Run(); err != nil {
			return
		}

		time.Sleep(1 * time.Second)

		restartCmd := exec.Command("docker", "restart", containerName)
		restartCmd.Run()
	}()

	response.Success(c, gin.H{
		"data": gin.H{
			"message": "更新任务已启动，正在拉取最新镜像",
		},
	})
}

func (h *SystemHandler) PanelLog(c *gin.Context) {
	linesStr := c.DefaultQuery("lines", "100")
	keyword := c.Query("keyword")

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

	var allLines []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if keyword == "" || strings.Contains(line, keyword) {
			allLines = append(allLines, line)
		}
	}

	start := len(allLines) - lines
	if start < 0 {
		start = 0
	}

	response.Success(c, gin.H{
		"data": gin.H{
			"logs":  allLines[start:],
			"total": len(allLines),
		},
	})
}

func (h *SystemHandler) RegisterRoutes(r *gin.RouterGroup) {
	r.GET("/system/public-version", h.PublicVersion)

	sys := r.Group("/system", middleware.JWTAuth())
	{
		sys.GET("/info", h.Info)
		sys.GET("/dashboard", h.Dashboard)
		sys.GET("/stats", h.Stats)
		sys.GET("/version", h.Version)
		sys.GET("/check-update", h.CheckUpdate)
		sys.POST("/update", middleware.RequireAdmin(), h.UpdatePanel)
		sys.GET("/panel-log", h.PanelLog)
		sys.POST("/backup", middleware.RequireAdmin(), h.Backup)
		sys.GET("/backups", middleware.RequireAdmin(), h.BackupList)
		sys.GET("/backup/download/:filename", middleware.RequireAdmin(), h.DownloadBackup)
		sys.POST("/restore", middleware.RequireAdmin(), h.Restore)
		sys.DELETE("/backup", middleware.RequireAdmin(), h.DeleteBackup)
	}
}
