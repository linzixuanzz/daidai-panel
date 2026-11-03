package handler

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"daidai-panel/model"
	"daidai-panel/pkg/response"
	"daidai-panel/service"

	"github.com/gin-gonic/gin"
)

func (h *ScriptHandler) DebugRun(c *gin.Context) {
	var req struct {
		Path     string `json:"path"`
		Content  string `json:"content"`
		Language string `json:"language"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "请求参数错误")
		return
	}

	req.Path = strings.TrimSpace(req.Path)
	req.Language = strings.TrimSpace(req.Language)
	hasInlineContent := strings.TrimSpace(req.Content) != "" && req.Language != ""

	if req.Path == "" && !hasInlineContent {
		response.BadRequest(c, "缺少脚本路径或调试内容")
		return
	}

	var (
		full      string
		ext       string
		workDir   string
		cleanupFn = func() {}
	)

	if hasInlineContent {
		ext = strings.ToLower(scriptLanguageExtMap[strings.ToLower(strings.TrimSpace(req.Language))])
		if ext == "" {
			response.BadRequest(c, "不支持的语言类型")
			return
		}

		inlinePath, inlineWorkDir, inlineCleanup, prepareErr := prepareInlineDebugFile(req.Path, ext)
		if prepareErr != nil {
			response.InternalError(c, fmt.Sprintf("创建调试文件失败: %s", prepareErr))
			return
		}
		full = inlinePath
		workDir = inlineWorkDir
		cleanupFn = inlineCleanup
		content := req.Content
		if ext == ".sh" {
			content = string(service.NormalizeShellLineEndings([]byte(content)))
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			cleanupFn()
			response.InternalError(c, "创建调试文件失败")
			return
		}
	} else {
		resolvedPath, err := safePath(req.Path, true)
		if err != nil {
			response.BadRequest(c, err.Error())
			return
		}
		full = resolvedPath
		ext = strings.ToLower(filepath.Ext(full))
		workDir = filepath.Dir(full)
	}

	if ext == ".sh" {
		if err := service.NormalizeShellScriptFile(full); err != nil {
			cleanupFn()
			response.InternalError(c, fmt.Sprintf("脚本换行规范化失败: %s", err))
			return
		}
	}
	interpreter, err := scriptRuntimeInterpreter(ext)
	if err != nil {
		cleanupFn()
		response.BadRequest(c, err.Error())
		return
	}

	envMap, scriptToken := buildScriptExecEnv(workDir)
	cmd, cleanup, err := newScriptCommand(interpreter, full, nil, workDir, envMap)
	if err != nil {
		service.RevokeScriptToken(scriptToken)
		cleanupFn()
		response.InternalError(c, fmt.Sprintf("启动失败: %s", err))
		return
	}

	run := newDebugRun()
	pipeWriter, scanDone, err := startTrackedCommand(cmd, run)
	if err != nil {
		cleanup()
		service.RevokeScriptToken(scriptToken)
		cleanupFn()
		response.InternalError(c, fmt.Sprintf("启动失败: %s", err))
		return
	}

	runName := filepath.Base(full)
	if req.Path != "" {
		runName = filepath.Base(req.Path)
	}
	// 随机串在前、脚本名在后：脚本名只是方便人看，不可预测靠前面的随机串。
	runID := newDebugRunID("debug") + "_" + runName
	h.storeRun(runID, run)

	startTime := time.Now()

	go func() {
		// 调试运行结束（正常退出、依赖重试跑完、被手动停止提前 return）都要作废注入的凭据，
		// 不能只靠 scriptDebugEnvTTL 兜底。
		defer service.RevokeScriptToken(scriptToken)

		waitErr := waitTrackedCommand(cmd, pipeWriter, scanDone)
		cleanup()
		cleanupFn()
		elapsed := time.Since(startTime).Seconds()
		exitCode := resolveExitCode(waitErr)

		if run.isStopped() {
			return
		}

		if exitCode != 0 && model.GetRegisteredConfigBool("auto_install_deps") {
			installed := map[string]bool{}
			const maxRetries = 5
			// logOffset 是 logLen() 给出的全局行号，不是 Logs 的切片下标：
			// 输出超过上限时头部会被成块丢弃，两者会错开，logOutputSince 按全局行号解释它。
			logOffset := 0
			for i := 0; i < maxRetries && exitCode != 0; i++ {
				if run.isStopped() {
					return
				}
				candidate := detectAutoInstallCandidate(ext, run.logOutputSince(logOffset), workDir)
				if candidate == nil {
					break
				}
				if installed[candidate.PackageName] {
					run.appendLog(fmt.Sprintf("[%s 已安装但仍然报错，可能是模块版本不兼容或内部依赖异常，请尝试手动安装指定版本]", candidate.DisplayName))
					break
				}
				run.appendLog(fmt.Sprintf("[检测到缺失依赖: %s，正在自动安装...]", candidate.DisplayName))
				if candidate.Manager == "nodejs" {
					if notice := service.NodeInstallCompatibilityNotice(candidate.PackageName); notice != "" {
						run.appendLog(notice)
					}
				}
				installResult := installDepForDebug(candidate, envMap)
				installed[candidate.PackageName] = true
				if run.isStopped() {
					return
				}
				if !installResult.Success {
					failureReason := strings.TrimSpace(installResult.Error)
					if failureReason == "" {
						failureReason = candidate.DisplayName
					}
					run.appendLog(fmt.Sprintf("[安装失败: %s]", failureReason))
					break
				}
				run.appendLog(fmt.Sprintf("[安装成功: %s，自动重试执行]", candidate.DisplayName))
				logOffset = run.logLen()
				retryCmd, retryCleanup, retryPrepareErr := newScriptCommand(interpreter, full, nil, workDir, envMap)
				if retryPrepareErr != nil {
					run.appendLog(fmt.Sprintf("[重试启动失败: %s]", retryPrepareErr))
					break
				}
				retryPipeWriter, retryScanDone, startErr := startTrackedCommand(retryCmd, run)
				if startErr != nil {
					retryCleanup()
					run.appendLog(fmt.Sprintf("[重试启动失败: %s]", startErr))
					break
				}
				waitErr = waitTrackedCommand(retryCmd, retryPipeWriter, retryScanDone)
				retryCleanup()
				elapsed = time.Since(startTime).Seconds()
				exitCode = resolveExitCode(waitErr)
			}
		}

		if hint := service.BuildRuntimeFailureHint(run.logOutput()); hint != "" {
			run.appendLog(hint)
		}

		run.finish(exitCode, waitErr, elapsed)
	}()

	response.Created(c, gin.H{"message": "脚本已启动", "run_id": runID})
}

func prepareInlineDebugFile(requestPath, ext string) (full string, workDir string, cleanupFn func(), err error) {
	cleanupFn = func() {}
	fileName := fmt.Sprintf("debug_%d%s", time.Now().UnixMilli(), ext)
	workDir = filepath.Join(os.TempDir(), "daidai-debug")

	if trimmedPath := strings.TrimSpace(requestPath); trimmedPath != "" {
		resolvedPath, resolveErr := safePath(trimmedPath, true)
		if resolveErr != nil {
			return "", "", cleanupFn, resolveErr
		}
		workDir = filepath.Dir(resolvedPath)
		baseName := strings.TrimSuffix(filepath.Base(resolvedPath), filepath.Ext(resolvedPath))
		if baseName == "" {
			baseName = "debug"
		}
		fileName = fmt.Sprintf(".%s.daidai-debug-%d%s", baseName, time.Now().UnixMilli(), ext)
	}

	if err := os.MkdirAll(workDir, 0o755); err != nil {
		return "", "", cleanupFn, err
	}

	full = filepath.Join(workDir, fileName)
	cleanupFn = func() {
		_ = os.Remove(full)
	}
	return full, workDir, cleanupFn, nil
}

func (h *ScriptHandler) DebugLogs(c *gin.Context) {
	runID := c.Param("run_id")

	run, exists := h.loadRun(runID)
	if !exists {
		response.NotFound(c, "运行记录不存在")
		return
	}

	logs, done, exitCode, status := run.snapshot()
	response.Success(c, gin.H{
		"data": gin.H{
			"logs":      logs,
			"done":      done,
			"exit_code": exitCode,
			"status":    status,
		},
	})
}

func (h *ScriptHandler) DebugStop(c *gin.Context) {
	runID := c.Param("run_id")

	run, exists := h.loadRun(runID)
	if !exists {
		response.NotFound(c, "运行记录不存在")
		return
	}

	run.stop()
	response.Success(c, gin.H{"message": "已停止"})
}

func (h *ScriptHandler) DebugClear(c *gin.Context) {
	runID := c.Param("run_id")

	run, exists := h.deleteRun(runID)
	if exists {
		run.killIfRunning()
	}

	response.Success(c, gin.H{"message": "已清除"})
}
