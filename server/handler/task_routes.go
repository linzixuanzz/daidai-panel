package handler

import (
	"daidai-panel/middleware"

	"github.com/gin-gonic/gin"
)

func (h *TaskHandler) RegisterRoutes(r *gin.RouterGroup) {
	// 原始日志文件直传下载：浏览器原生下载带不了 Authorization 头，改用短期票据鉴权，
	// 因此不能放进下面带 JWTAuth 的 tasks 组。票据由 /log-files/:filename/raw-ticket 签发，
	// 那条接口的鉴权与其它日志文件接口完全一致。
	r.GET("/tasks/:id/log-files/:filename/raw", h.DownloadRawLogFile)
	// 日志文件夹打包下载，同样是票据鉴权。静态段 archive 与同层的 :filename 共存，
	// 和下面 /tasks/export、/tasks/views 与 /tasks/:id 是同一个形态。
	r.GET("/tasks/:id/log-files/archive", h.DownloadLogArchive)

	tasks := r.Group("/tasks", middleware.JWTAuth(), middleware.OpenAPIAccess("tasks"))
	{
		tasks.GET("", middleware.RequireRole("viewer"), h.List)
		tasks.GET("/notification-channels", middleware.RequireRole("viewer"), h.NotificationChannels)
		tasks.GET("/:id/latest-log", middleware.RequireRole("viewer"), h.LatestLog)
		tasks.GET("/:id/live-logs", middleware.RequireRole("viewer"), h.LiveLogs)
		tasks.GET("/:id/log-files", middleware.RequireRole("viewer"), h.LogFiles)
		tasks.GET("/:id/log-files/archive-ticket", middleware.RequireRole("viewer"), h.LogArchiveDownloadTicket)
		tasks.GET("/:id/log-files/:filename", middleware.RequireRole("viewer"), h.LogFileContent)
		tasks.GET("/:id/log-files/:filename/download", middleware.RequireRole("viewer"), h.DownloadLogFile)
		tasks.GET("/:id/log-files/:filename/raw-ticket", middleware.RequireRole("viewer"), h.RawLogFileDownloadTicket)
		tasks.GET("/:id/stats", middleware.RequireRole("viewer"), h.Stats)
		tasks.GET("/export", middleware.RequireRole("viewer"), h.Export)
		tasks.POST("/cron/parse", middleware.RequireRole("viewer"), h.CronParse)
		tasks.GET("/cron/templates", middleware.RequireRole("viewer"), h.CronTemplates)

		tasks.POST("", middleware.RequireRole("operator"), h.Create)
		tasks.PUT("/:id", middleware.RequireRole("operator"), h.Update)
		tasks.DELETE("/:id", middleware.RequireRole("operator"), h.Delete)
		tasks.PUT("/:id/run", middleware.RequireRole("operator"), h.Run)
		tasks.PUT("/:id/stop", middleware.RequireRole("operator"), h.Stop)
		tasks.PUT("/:id/enable", middleware.RequireRole("operator"), h.Enable)
		tasks.PUT("/:id/disable", middleware.RequireRole("operator"), h.Disable)
		tasks.PUT("/:id/pin", middleware.RequireRole("operator"), h.Pin)
		tasks.PUT("/:id/unpin", middleware.RequireRole("operator"), h.Unpin)
		// 列表拖拽排序。静态段 sort 与同层的 PUT /:id 共存，和 /tasks/batch 是同一个形态。
		// 复用组上已挂的 OpenAPIAccess("tasks")，不新开 scope。
		tasks.PUT("/sort", middleware.RequireRole("operator"), h.Sort)
		tasks.POST("/:id/copy", middleware.RequireRole("operator"), h.Copy)
		tasks.DELETE("/:id/log-files/:filename", middleware.RequireRole("operator"), h.DeleteLogFile)
		// 删除前预览：列出每个任务解析出的脚本、能否一起删、不能删的原因（#124）。
		// 静态段 delete-preview 与同层的 POST /:id/copy 共存，和 POST /import 是同一个形态。
		// 复用组上已挂的 OpenAPIAccess("tasks")；应用令牌另需 scripts scope，在 handler 里校验。
		tasks.POST("/delete-preview", middleware.RequireRole("operator"), h.DeletePreview)
		tasks.PUT("/batch", middleware.RequireRole("operator"), h.Batch)
		tasks.PUT("/batch/enable", middleware.RequireRole("operator"), h.BatchEnable)
		tasks.PUT("/batch/disable", middleware.RequireRole("operator"), h.BatchDisable)
		tasks.DELETE("/batch/delete", middleware.RequireRole("operator"), h.BatchDelete)
		tasks.POST("/batch/run", middleware.RequireRole("operator"), h.BatchRun)
		tasks.PUT("/batch/add-labels", middleware.RequireRole("operator"), h.BatchAddLabels)
		tasks.DELETE("/clean-logs", middleware.RequireRole("operator"), h.CleanLogs)
		tasks.POST("/import", middleware.RequireRole("operator"), h.Import)

		tasks.GET("/views", middleware.RequireRole("viewer"), h.ListViews)
		tasks.POST("/views", middleware.RequireRole("operator"), h.CreateView)
		tasks.PUT("/views/reorder", middleware.RequireRole("operator"), h.ReorderViews)
		tasks.PUT("/views/:viewId", middleware.RequireRole("operator"), h.UpdateView)
		tasks.DELETE("/views/:viewId", middleware.RequireRole("operator"), h.DeleteView)
	}
}
