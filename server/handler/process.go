package handler

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"unicode/utf8"

	"daidai-panel/middleware"
	"daidai-panel/pkg/response"
	"daidai-panel/service"

	"github.com/gin-gonic/gin"
)

// ProcessHandler 是「后台进程」页（#176）的接口：查看并结束任务拉起、逃出进程组的常驻进程。
type ProcessHandler struct{}

func NewProcessHandler() *ProcessHandler {
	return &ProcessHandler{}
}

// auditProcessCmdlineMaxRunes：审计里只留命令行前 100 个字符。
const auditProcessCmdlineMaxRunes = 100

// List 列出进程。scope=task（默认）只列带任务标识的进程；scope=all 列全部（Magisk 模块版不开放）。
func (h *ProcessHandler) List(c *gin.Context) {
	scope := strings.TrimSpace(c.DefaultQuery("scope", "task"))
	if scope == "" {
		scope = "task"
	}
	if scope != "task" && scope != "all" {
		response.BadRequest(c, "scope 只能是 task 或 all")
		return
	}

	supported := service.ProcessMonitorSupported()
	// 前端据 all_supported 决定是否显示「显示全部进程」开关
	allSupported := supported && !service.IsMagiskModuleRuntime()
	if !supported {
		// 非 Linux（Windows 开发机等）：空列表 + supported=false，前端显示「仅 Linux 部署支持」
		response.Success(c, gin.H{"data": []service.PanelProcess{}, "supported": false, "all_supported": false, "scope": scope})
		return
	}

	list, err := service.ListPanelProcesses(scope == "all")
	if err != nil {
		if errors.Is(err, service.ErrProcessListAllUnavailable) {
			response.BadRequest(c, err.Error())
			return
		}
		log.Printf("list panel processes failed: %v", err)
		response.InternalError(c, "读取进程列表失败")
		return
	}
	response.Success(c, gin.H{"data": list, "supported": true, "all_supported": allSupported, "scope": scope})
}

// Kill 结束一个进程。body {pid, start_ticks}；start_ticks 来自列表，用来防 PID 复用后误杀。
func (h *ProcessHandler) Kill(c *gin.Context) {
	var req struct {
		PID        int    `json:"pid"`
		StartTicks uint64 `json:"start_ticks"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "请求参数错误")
		return
	}
	if req.PID <= 0 || req.StartTicks == 0 {
		response.BadRequest(c, "缺少 pid 或 start_ticks")
		return
	}

	proc, exited, err := service.KillPanelProcess(req.PID, req.StartTicks)
	if err != nil {
		var refused *service.ProcessKillRefusedError
		switch {
		case errors.Is(err, service.ErrProcessGone):
			response.Error(c, http.StatusConflict, err.Error())
		case errors.Is(err, service.ErrProcessMonitorUnsupported):
			response.BadRequest(c, err.Error())
		case errors.As(err, &refused):
			response.BadRequest(c, refused.Reason)
		default:
			log.Printf("kill panel process %d failed: %v", req.PID, err)
			response.InternalError(c, "结束进程失败")
		}
		return
	}

	// 信号已经发出去了就记审计，不论最后是否退出
	taskLabel := "无"
	if proc.TaskID != nil {
		if proc.TaskName != "" {
			taskLabel = fmt.Sprintf("%s（#%d）", proc.TaskName, *proc.TaskID)
		} else {
			taskLabel = fmt.Sprintf("已删除的任务 #%d", *proc.TaskID)
		}
	}
	cmdline := proc.Cmdline
	if utf8.RuneCountInString(cmdline) > auditProcessCmdlineMaxRunes {
		cmdline = string([]rune(cmdline)[:auditProcessCmdlineMaxRunes]) + "…"
	}
	recordAudit(c, service.AuditProcessKill, fmt.Sprintf("结束进程 PID %d（任务：%s）：%s", proc.PID, taskLabel, cmdline))

	message := "进程已结束"
	if !exited {
		message = "已发送结束信号，但进程仍未退出，请稍后刷新查看"
	}
	response.Success(c, gin.H{"message": message, "exited": exited})
}

func (h *ProcessHandler) RegisterRoutes(r *gin.RouterGroup) {
	// 能看到所有任务的命令行、还能结束进程：只给管理员账号本人，不对应用令牌 / 脚本令牌开放。
	processes := r.Group("/system/processes", middleware.JWTAuth(), middleware.RequireUserToken(), middleware.RequireAdmin())
	{
		processes.GET("", h.List)
		processes.POST("/kill", h.Kill)
	}
}
