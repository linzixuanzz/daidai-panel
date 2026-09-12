package handler

import (
	"log"
	"strconv"

	"daidai-panel/middleware"
	"daidai-panel/pkg/response"
	"daidai-panel/service"

	"github.com/gin-gonic/gin"
)

// 本文件是 #124「删除任务时可选同时删除脚本」的 handler 层：鉴权、参数与响应。
// 判定与删除全部在 service/task_script_cleanup.go。
//
// 三个删除入口（task_mutate.go 的 Delete、task_batch.go 的 Batch / BatchDelete）原有的删除语句一字不改，
// 只在删除前调 beginTaskScriptCleanup（拍快照）、删除后调 finishTaskScriptCleanup（判定并删文件）。
// 不带开关时这两个函数都不会被调用，响应与改动前逐字节一致（task_delete_scripts_test.go 的 golden 用例钉住）。
// 刻意不另写一套「连脚本一起删」的删除流程：两份删除代码迟早会漂移（例如以后给删除补上 StopTask，新路径就漏了）。

const (
	taskScriptScopeDeniedMessage  = "应用令牌缺少 scripts 权限，不能同时删除脚本"
	taskDeletePreviewEmptyMessage = "请选择要删除的任务"
)

type taskScriptCleanupCtx struct {
	cleanup *service.TaskScriptCleanup
	confirm *[]string
}

func taskScriptCleanupEnv() service.TaskScriptCleanupEnv {
	return service.TaskScriptCleanupEnv{
		ScriptsDir: scriptsDir(),
		IsTaskRunning: func(taskID uint) bool {
			executor := service.GetTaskExecutor()
			if executor == nil {
				return false
			}
			return executor.HasRunningProcess(taskID)
		},
	}
}

// parseDeleteScriptQuery 读 DELETE /tasks/:id 的可选开关（APP 的 dio.delete 不带 body，所以放在 query）。
// delete_script 只有 strconv.ParseBool 为真才算开启；假值、非法值、空值一律按「没开」处理、不报 400，
// 这样老调用方误传的参数也只会走旧逻辑。
// confirm_script_path 用 GetQueryArray 区分「没传」（nil，不收窄）和「传了空串」（一个都不删）。
func parseDeleteScriptQuery(c *gin.Context) (bool, *[]string) {
	raw, ok := c.GetQuery("delete_script")
	if !ok {
		return false, nil
	}
	if on, err := strconv.ParseBool(raw); err != nil || !on {
		return false, nil
	}
	if paths, ok := c.GetQueryArray("confirm_script_path"); ok {
		confirm := append([]string{}, paths...)
		return true, &confirm
	}
	return true, nil
}

// beginTaskScriptCleanup 必须在删任务之前调用。返回 false 时已经写好了 403 响应，调用方直接 return——
// 此时任务和脚本都还没动。
func beginTaskScriptCleanup(c *gin.Context, ids []uint, confirm *[]string) (*taskScriptCleanupCtx, bool) {
	// tasks 路由组对应用令牌只校验 tasks scope（RequireRole 对应用令牌不看角色）；删脚本动的是 scripts 的文件，
	// 必须另外要求 scripts scope，否则只有 tasks 权限的应用就凭空获得了删脚本的能力。
	if !middleware.AppTokenHasScope(c, "scripts") {
		response.Forbidden(c, taskScriptScopeDeniedMessage)
		return nil, false
	}
	return &taskScriptCleanupCtx{
		cleanup: service.CollectTaskScriptTargets(ids, taskScriptCleanupEnv()),
		confirm: confirm,
	}, true
}

// finishTaskScriptCleanup 在删任务之后调用：执行判定与删除，并给每个删掉的文件留一行面板日志。
// remove_failed 的底层错误只进日志（含「失败」二字，按 ERROR 级展示），响应里不带原文。
func finishTaskScriptCleanup(c *gin.Context, cl *taskScriptCleanupCtx) service.TaskScriptDeleteResult {
	result := cl.cleanup.Execute(cl.confirm)
	username := c.GetString("username")
	clientIP := middleware.ResolveClientIP(c)
	for _, item := range result.Deleted {
		log.Printf("[任务删除] %s(%s) 删除任务 %v 时一并删除了脚本 %s", username, clientIP, item.TaskIDs, item.Path)
	}
	for _, item := range result.Skipped {
		if item.Reason == service.TaskScriptReasonRemoveFailed {
			log.Printf("[任务删除] %s(%s) 删除任务 %v 时一并删除脚本 %s 失败，已保留: %s", username, clientIP, item.TaskIDs, item.Path, item.RemoveError)
		}
	}
	return result
}

// DeletePreview 删除前预览：给一批任务 id，返回每个任务解析出的脚本、能否一起删、不能删的原因。只读。
func (h *TaskHandler) DeletePreview(c *gin.Context) {
	var req struct {
		TaskIDs []uint `json:"task_ids" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "请求参数错误")
		return
	}
	// binding:"required" 会放行空数组 []，必须显式判断。service 里按首次出现的顺序去重，
	// 去重不会把非空变成空，所以这里判原长度即可。
	if len(req.TaskIDs) == 0 {
		response.BadRequest(c, taskDeletePreviewEmptyMessage)
		return
	}
	// 预览会列出脚本目录里的文件路径与引用关系，应用令牌同样需要 scripts scope。
	if !middleware.AppTokenHasScope(c, "scripts") {
		response.Forbidden(c, taskScriptScopeDeniedMessage)
		return
	}
	response.Success(c, gin.H{"data": service.PreviewTaskScriptDeletion(req.TaskIDs, taskScriptCleanupEnv())})
}
