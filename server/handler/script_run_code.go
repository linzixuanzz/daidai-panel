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

func (h *ScriptHandler) RunCode(c *gin.Context) {
	var req struct {
		Code     string `json:"code" binding:"required"`
		Language string `json:"language" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "请求参数错误")
		return
	}

	ext, ok := scriptLanguageExtMap[req.Language]
	if !ok {
		response.BadRequest(c, "不支持的语言类型")
		return
	}

	tmpDir := filepath.Join(os.TempDir(), "daidai-debug")
	os.MkdirAll(tmpDir, 0755)

	tmpFile := filepath.Join(tmpDir, fmt.Sprintf("code_%d%s", time.Now().UnixMilli(), ext))
	if err := os.WriteFile(tmpFile, []byte(req.Code), 0644); err != nil {
		response.InternalError(c, "创建临时文件失败")
		return
	}

	interpreter, err := scriptRuntimeInterpreter(ext)
	if err != nil {
		os.Remove(tmpFile)
		response.BadRequest(c, err.Error())
		return
	}
	envMap, scriptToken := buildScriptExecEnv(tmpDir)
	cmd, cleanup, err := newScriptCommand(interpreter, tmpFile, nil, tmpDir, envMap)
	if err != nil {
		service.RevokeScriptToken(scriptToken)
		os.Remove(tmpFile)
		response.InternalError(c, fmt.Sprintf("启动失败: %s", err))
		return
	}

	run := newDebugRun()
	pipeWriter, scanDone, err := startTrackedCommand(cmd, run)
	if err != nil {
		cleanup()
		service.RevokeScriptToken(scriptToken)
		os.Remove(tmpFile)
		response.InternalError(c, fmt.Sprintf("启动失败: %s", err))
		return
	}

	// 随机 run_id（见 newDebugRunID），前缀 code 保留给人看。
	runID := newDebugRunID("code")
	h.storeRun(runID, run)

	startTime := time.Now()

	go func() {
		// 运行结束（含被手动停止时的提前 return）都要作废注入的凭据。
		defer service.RevokeScriptToken(scriptToken)

		waitErr := waitTrackedCommand(cmd, pipeWriter, scanDone)
		cleanup()
		elapsed := time.Since(startTime).Seconds()
		exitCode := resolveExitCode(waitErr)

		if run.isStopped() {
			os.Remove(tmpFile)
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
					os.Remove(tmpFile)
					return
				}
				candidate := detectAutoInstallCandidate(ext, run.logOutputSince(logOffset), tmpDir)
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
					os.Remove(tmpFile)
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
				retryCmd, retryCleanup, retryPrepareErr := newScriptCommand(interpreter, tmpFile, nil, tmpDir, envMap)
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

		os.Remove(tmpFile)
		run.finish(exitCode, waitErr, elapsed)
	}()

	response.Created(c, gin.H{"message": "代码已启动", "run_id": runID})
}

func detectAutoInstallCandidate(ext, output, workDir string) *service.AutoInstallCandidate {
	return service.DetectAutoInstallCandidate(ext, output, workDir)
}

func installDepForDebug(candidate *service.AutoInstallCandidate, envMap map[string]string) service.AutoInstallResult {
	return service.InstallAutoDependency(candidate, envMap)
}
