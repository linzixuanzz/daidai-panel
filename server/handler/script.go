package handler

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"daidai-panel/config"
	"daidai-panel/database"
	"daidai-panel/middleware"
	"daidai-panel/model"
	"daidai-panel/pkg/response"

	"github.com/gin-gonic/gin"
)

var allowedExtensions = map[string]bool{
	".py": true, ".js": true, ".sh": true, ".ts": true, ".json": true,
	".yaml": true, ".yml": true, ".txt": true, ".md": true, ".conf": true,
	".ini": true, ".env": true, ".toml": true, ".xml": true, ".csv": true,
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".svg": true,
	".ico": true, ".bmp": true, ".webp": true, ".log": true, ".htm": true,
	".html": true, ".css": true, ".sql": true, ".bat": true, ".cmd": true, ".ps1": true,
	".so": true,
}

var binaryExtensions = map[string]bool{
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true,
	".ico": true, ".bmp": true, ".webp": true, ".so": true,
}

var filenamePattern = regexp.MustCompile(`^[\w\x{4e00}-\x{9fff}\-./]+$`)

const maxUploadSize = 10 * 1024 * 1024

type debugRun struct {
	Process  *os.Process
	Logs     []string
	Done     bool
	ExitCode *int
	Status   string
	mu       sync.Mutex
}

type ScriptHandler struct {
	debugRuns map[string]*debugRun
	mu        sync.Mutex
}

func NewScriptHandler() *ScriptHandler {
	return &ScriptHandler{
		debugRuns: make(map[string]*debugRun),
	}
}

func scriptsDir() string {
	return config.C.Data.ScriptsDir
}

func safePath(relPath string, mustExist bool) (string, error) {
	relPath = strings.TrimSpace(relPath)
	if relPath == "" {
		return "", fmt.Errorf("路径不能为空")
	}
	if !filenamePattern.MatchString(relPath) {
		return "", fmt.Errorf("路径包含非法字符")
	}
	if strings.Contains(relPath, "..") {
		return "", fmt.Errorf("不允许路径穿越")
	}

	full := filepath.Join(scriptsDir(), relPath)
	full, _ = filepath.Abs(full)
	absDir, _ := filepath.Abs(scriptsDir())
	if !strings.HasPrefix(full, absDir) {
		return "", fmt.Errorf("检测到路径穿越")
	}

	if mustExist {
		if _, err := os.Stat(full); os.IsNotExist(err) {
			return "", fmt.Errorf("文件不存在: %s", relPath)
		}
	}
	return full, nil
}

func relPath(absPath string) string {
	absDir, _ := filepath.Abs(scriptsDir())
	rel, _ := filepath.Rel(absDir, absPath)
	return filepath.ToSlash(rel)
}

func (h *ScriptHandler) List(c *gin.Context) {
	dir := scriptsDir()
	var files []map[string]interface{}

	filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(info.Name()))
		if !allowedExtensions[ext] && ext != "" {
			return nil
		}
		rel := relPath(path)
		files = append(files, map[string]interface{}{
			"path":  rel,
			"name":  info.Name(),
			"size":  info.Size(),
			"mtime": float64(info.ModTime().Unix()),
		})
		return nil
	})

	if files == nil {
		files = []map[string]interface{}{}
	}

	sort.Slice(files, func(i, j int) bool {
		return files[i]["path"].(string) < files[j]["path"].(string)
	})

	response.Success(c, gin.H{"data": files, "total": len(files)})
}

func (h *ScriptHandler) Tree(c *gin.Context) {
	tree := buildTree(scriptsDir(), "")
	response.Success(c, gin.H{"data": tree})
}

func buildTree(baseDir, prefix string) []map[string]interface{} {
	dir := filepath.Join(baseDir, prefix)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return []map[string]interface{}{}
	}

	var dirs, files []map[string]interface{}

	sorted := make([]os.DirEntry, len(entries))
	copy(sorted, entries)
	sort.Slice(sorted, func(i, j int) bool {
		return strings.ToLower(sorted[i].Name()) < strings.ToLower(sorted[j].Name())
	})

	for _, entry := range sorted {
		name := entry.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}

		rel := name
		if prefix != "" {
			rel = prefix + "/" + name
		}

		if entry.IsDir() {
			children := buildTree(baseDir, rel)
			dirs = append(dirs, map[string]interface{}{
				"key":      rel,
				"title":    name,
				"isLeaf":   false,
				"type":     "directory",
				"children": children,
			})
		} else {
			info, _ := entry.Info()
			size := int64(0)
			mtime := float64(0)
			if info != nil {
				size = info.Size()
				mtime = float64(info.ModTime().Unix())
			}
			files = append(files, map[string]interface{}{
				"key":       rel,
				"title":     name,
				"isLeaf":    true,
				"type":      "file",
				"extension": strings.ToLower(filepath.Ext(name)),
				"size":      size,
				"mtime":     mtime,
			})
		}
	}

	result := make([]map[string]interface{}, 0, len(dirs)+len(files))
	result = append(result, dirs...)
	result = append(result, files...)
	return result
}

func (h *ScriptHandler) GetContent(c *gin.Context) {
	path := c.Query("path")
	full, err := safePath(path, true)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	ext := strings.ToLower(filepath.Ext(full))
	if binaryExtensions[ext] {
		data, err := os.ReadFile(full)
		if err != nil {
			response.InternalError(c, "读取文件失败")
			return
		}
		mimeType := mime.TypeByExtension(ext)
		if mimeType == "" {
			mimeType = "application/octet-stream"
		}
		response.Success(c, gin.H{
			"data": gin.H{
				"path":    path,
				"content": base64.StdEncoding.EncodeToString(data),
				"binary":  true,
				"mime":    mimeType,
			},
		})
		return
	}

	data, err := os.ReadFile(full)
	if err != nil {
		response.InternalError(c, "读取文件失败")
		return
	}

	response.Success(c, gin.H{
		"data": gin.H{
			"path":    path,
			"content": string(data),
			"binary":  false,
		},
	})
}

func (h *ScriptHandler) SaveContent(c *gin.Context) {
	var req struct {
		Path    string `json:"path" binding:"required"`
		Content string `json:"content"`
		Message string `json:"message"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "请求参数错误")
		return
	}

	ext := strings.ToLower(filepath.Ext(req.Path))
	if ext != "" && !allowedExtensions[ext] {
		response.BadRequest(c, "不支持的文件类型")
		return
	}

	if len(req.Content) > maxUploadSize {
		response.BadRequest(c, "内容过大（最大 10MB）")
		return
	}

	full, err := safePath(req.Path, false)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	os.MkdirAll(filepath.Dir(full), 0755)
	if err := os.WriteFile(full, []byte(req.Content), 0644); err != nil {
		response.InternalError(c, "写入文件失败")
		return
	}

	var maxVersion int
	database.DB.Model(&model.ScriptVersion{}).
		Where("script_path = ?", req.Path).
		Select("COALESCE(MAX(version), 0)").
		Scan(&maxVersion)

	newVersion := maxVersion + 1
	msg := req.Message
	if msg == "" {
		msg = fmt.Sprintf("v%d", newVersion)
	}

	sv := model.ScriptVersion{
		ScriptPath: req.Path,
		Content:    req.Content,
		Version:    newVersion,
		Message:    msg,
	}
	database.DB.Create(&sv)

	response.Success(c, gin.H{"message": "保存成功", "version": newVersion})
}

func (h *ScriptHandler) Upload(c *gin.Context) {
	file, header, err := c.Request.FormFile("file")
	if err != nil {
		response.BadRequest(c, "未选择文件")
		return
	}
	defer file.Close()

	if header.Size > maxUploadSize {
		response.BadRequest(c, "文件过大（最大 10MB）")
		return
	}

	filename := header.Filename
	ext := strings.ToLower(filepath.Ext(filename))
	if ext != "" && !allowedExtensions[ext] {
		response.BadRequest(c, "不支持的文件类型")
		return
	}

	full, err := safePath(filename, false)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	os.MkdirAll(filepath.Dir(full), 0755)
	dst, err := os.Create(full)
	if err != nil {
		response.InternalError(c, "创建文件失败")
		return
	}
	defer dst.Close()

	io.Copy(dst, file)

	response.Created(c, gin.H{"message": "上传成功", "path": filename})
}

func (h *ScriptHandler) Delete(c *gin.Context) {
	path := c.Query("path")
	fileType := c.DefaultQuery("type", "file")

	full, err := safePath(path, true)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	if fileType == "directory" {
		os.RemoveAll(full)
	} else {
		os.Remove(full)
	}

	response.Success(c, gin.H{"message": "删除成功"})
}

func (h *ScriptHandler) CreateDirectory(c *gin.Context) {
	var req struct {
		Path string `json:"path" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "请求参数错误")
		return
	}

	full, err := safePath(req.Path, false)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	if err := os.MkdirAll(full, 0755); err != nil {
		response.InternalError(c, "创建目录失败")
		return
	}

	response.Created(c, gin.H{"message": "创建成功"})
}

func (h *ScriptHandler) Rename(c *gin.Context) {
	var req struct {
		OldPath string `json:"old_path" binding:"required"`
		NewName string `json:"new_name" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "请求参数错误")
		return
	}

	if strings.ContainsAny(req.NewName, "/\\") {
		response.BadRequest(c, "新名称不能包含路径分隔符")
		return
	}

	full, err := safePath(req.OldPath, true)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	newFull := filepath.Join(filepath.Dir(full), req.NewName)
	if err := os.Rename(full, newFull); err != nil {
		response.InternalError(c, "重命名失败")
		return
	}

	response.Success(c, gin.H{"message": "重命名成功", "new_path": relPath(newFull)})
}

func (h *ScriptHandler) Move(c *gin.Context) {
	var req struct {
		SourcePath string `json:"source_path" binding:"required"`
		TargetDir  string `json:"target_dir"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "请求参数错误")
		return
	}

	srcFull, err := safePath(req.SourcePath, true)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	targetBase := scriptsDir()
	if req.TargetDir != "" {
		targetBase, err = safePath(req.TargetDir, true)
		if err != nil {
			response.BadRequest(c, "目标目录无效")
			return
		}
	}

	absTarget, _ := filepath.Abs(targetBase)
	absSrc, _ := filepath.Abs(srcFull)
	if strings.HasPrefix(absTarget, absSrc+string(filepath.Separator)) {
		response.BadRequest(c, "不能将目录移动到自身")
		return
	}

	destFull := filepath.Join(targetBase, filepath.Base(srcFull))
	if err := os.Rename(srcFull, destFull); err != nil {
		response.InternalError(c, "移动失败")
		return
	}

	response.Success(c, gin.H{"message": "移动成功", "new_path": relPath(destFull)})
}

func (h *ScriptHandler) Copy(c *gin.Context) {
	var req struct {
		SourcePath string `json:"source_path" binding:"required"`
		TargetDir  string `json:"target_dir"`
		NewName    string `json:"new_name"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "请求参数错误")
		return
	}

	srcFull, err := safePath(req.SourcePath, true)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	targetBase := scriptsDir()
	if req.TargetDir != "" {
		targetBase, _ = safePath(req.TargetDir, false)
	}

	name := filepath.Base(srcFull)
	if req.NewName != "" {
		name = req.NewName
	}

	destFull := filepath.Join(targetBase, name)
	os.MkdirAll(targetBase, 0755)

	info, _ := os.Stat(srcFull)
	if info != nil && info.IsDir() {
		if err := copyDir(srcFull, destFull); err != nil {
			response.InternalError(c, "复制目录失败")
			return
		}
	} else {
		if err := copyFile(srcFull, destFull); err != nil {
			response.InternalError(c, "复制文件失败")
			return
		}
	}

	response.Created(c, gin.H{"message": "复制成功", "new_path": relPath(destFull)})
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	os.MkdirAll(filepath.Dir(dst), 0755)
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, in)
	return err
}

func copyDir(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, path)
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0755)
		}
		return copyFile(path, target)
	})
}

func (h *ScriptHandler) BatchDelete(c *gin.Context) {
	var req struct {
		Paths []struct {
			Path string `json:"path"`
			Type string `json:"type"`
		} `json:"paths" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "请求参数错误")
		return
	}

	successCount := 0
	failedCount := 0
	failedItems := []string{}

	for _, item := range req.Paths {
		full, err := safePath(item.Path, true)
		if err != nil {
			failedCount++
			failedItems = append(failedItems, item.Path)
			continue
		}
		if item.Type == "directory" {
			err = os.RemoveAll(full)
		} else {
			err = os.Remove(full)
		}
		if err != nil {
			failedCount++
			failedItems = append(failedItems, item.Path)
		} else {
			successCount++
		}
	}

	response.Success(c, gin.H{
		"message":       fmt.Sprintf("删除完成: 成功 %d, 失败 %d", successCount, failedCount),
		"success_count": successCount,
		"failed_count":  failedCount,
		"failed_items":  failedItems,
	})
}

func (h *ScriptHandler) ListVersions(c *gin.Context) {
	path := c.Query("path")
	if path == "" {
		response.BadRequest(c, "路径不能为空")
		return
	}

	var versions []model.ScriptVersion
	database.DB.Where("script_path = ?", path).
		Order("version DESC").Limit(50).Find(&versions)

	data := make([]map[string]interface{}, len(versions))
	for i, v := range versions {
		data[i] = v.ToDict()
	}

	response.Success(c, gin.H{"data": data})
}

func (h *ScriptHandler) GetVersion(c *gin.Context) {
	id, _ := strconv.ParseUint(c.Param("id"), 10, 32)

	var version model.ScriptVersion
	if err := database.DB.First(&version, id).Error; err != nil {
		response.NotFound(c, "版本不存在")
		return
	}

	response.Success(c, gin.H{"data": version.ToDictWithContent()})
}

func (h *ScriptHandler) Rollback(c *gin.Context) {
	id, _ := strconv.ParseUint(c.Param("id"), 10, 32)

	var version model.ScriptVersion
	if err := database.DB.First(&version, id).Error; err != nil {
		response.NotFound(c, "版本不存在")
		return
	}

	full, err := safePath(version.ScriptPath, false)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	os.MkdirAll(filepath.Dir(full), 0755)
	if err := os.WriteFile(full, []byte(version.Content), 0644); err != nil {
		response.InternalError(c, "写入文件失败")
		return
	}

	var maxVersion int
	database.DB.Model(&model.ScriptVersion{}).
		Where("script_path = ?", version.ScriptPath).
		Select("COALESCE(MAX(version), 0)").
		Scan(&maxVersion)

	newVer := maxVersion + 1
	sv := model.ScriptVersion{
		ScriptPath: version.ScriptPath,
		Content:    version.Content,
		Version:    newVer,
		Message:    fmt.Sprintf("回滚到 v%d", version.Version),
	}
	database.DB.Create(&sv)

	response.Success(c, gin.H{
		"message": fmt.Sprintf("已回滚到 v%d", version.Version),
		"version": newVer,
	})
}

func (h *ScriptHandler) DebugRun(c *gin.Context) {
	var req struct {
		Path string `json:"path" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "请求参数错误")
		return
	}

	full, err := safePath(req.Path, true)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	ext := strings.ToLower(filepath.Ext(full))
	interpreterMap := map[string][]string{
		".py": {"python", "-u"},
		".js": {"node"},
		".ts": {"npx", "ts-node"},
		".sh": {"bash"},
	}
	cmdParts, ok := interpreterMap[ext]
	if !ok {
		response.BadRequest(c, "不支持执行此文件类型")
		return
	}

	cmdParts = append(cmdParts, full)

	var envVars []model.EnvVar
	database.DB.Where("enabled = ?", true).Find(&envVars)

	env := []string{}
	for _, k := range []string{"PATH", "HOME", "USER", "LANG", "SYSTEMROOT", "PATHEXT", "TEMP", "TMP"} {
		if v := os.Getenv(k); v != "" {
			env = append(env, k+"="+v)
		}
	}
	envMap := make(map[string]string)
	for _, e := range envVars {
		if existing, ok := envMap[e.Name]; ok {
			envMap[e.Name] = existing + "&" + e.Value
		} else {
			envMap[e.Name] = e.Value
		}
	}
	for k, v := range envMap {
		env = append(env, k+"="+v)
	}

	workDir := filepath.Dir(full)

	cmd := exec.Command(cmdParts[0], cmdParts[1:]...)
	cmd.Dir = workDir
	cmd.Env = env

	pipeReader, pipeWriter := io.Pipe()
	cmd.Stdout = pipeWriter
	cmd.Stderr = pipeWriter

	if err := cmd.Start(); err != nil {
		pipeWriter.Close()
		response.InternalError(c, fmt.Sprintf("启动失败: %s", err))
		return
	}

	runID := fmt.Sprintf("%d_%s", time.Now().UnixMilli(), filepath.Base(req.Path))

	run := &debugRun{
		Process: cmd.Process,
		Logs:    []string{},
		Status:  "running",
	}

	h.mu.Lock()
	h.debugRuns[runID] = run
	h.mu.Unlock()

	startTime := time.Now()

	scanDone := make(chan struct{})

	go func() {
		scanner := bufio.NewScanner(pipeReader)
		scanner.Buffer(make([]byte, 64*1024), 1024*1024)
		for scanner.Scan() {
			line := scanner.Text()
			run.mu.Lock()
			run.Logs = append(run.Logs, line)
			run.mu.Unlock()
		}
		close(scanDone)
	}()

	go func() {
		err := cmd.Wait()
		pipeWriter.Close()
		<-scanDone
		elapsed := time.Since(startTime).Seconds()

		run.mu.Lock()
		exitCode := 0
		if err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				exitCode = exitErr.ExitCode()
			} else {
				exitCode = 1
			}
		}
		run.ExitCode = &exitCode
		run.Done = true
		if exitCode == 0 {
			run.Status = "success"
			run.Logs = append(run.Logs, fmt.Sprintf("[进程结束, 退出码: %d, 耗时: %.2f秒]", exitCode, elapsed))
		} else {
			run.Status = "failed"
			errMsg := ""
			if err != nil {
				errMsg = err.Error()
			}
			if errMsg != "" {
				run.Logs = append(run.Logs, fmt.Sprintf("[进程异常退出, 退出码: %d, 错误: %s, 耗时: %.2f秒]", exitCode, errMsg, elapsed))
			} else {
				run.Logs = append(run.Logs, fmt.Sprintf("[进程异常退出, 退出码: %d, 耗时: %.2f秒]", exitCode, elapsed))
			}
		}
		run.mu.Unlock()
	}()

	response.Created(c, gin.H{"message": "脚本已启动", "run_id": runID})
}

func (h *ScriptHandler) DebugLogs(c *gin.Context) {
	runID := c.Param("run_id")

	h.mu.Lock()
	run, exists := h.debugRuns[runID]
	h.mu.Unlock()

	if !exists {
		response.NotFound(c, "运行记录不存在")
		return
	}

	run.mu.Lock()
	logs := make([]string, len(run.Logs))
	copy(logs, run.Logs)
	done := run.Done
	exitCode := run.ExitCode
	status := run.Status
	run.mu.Unlock()

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

	h.mu.Lock()
	run, exists := h.debugRuns[runID]
	h.mu.Unlock()

	if !exists {
		response.NotFound(c, "运行记录不存在")
		return
	}

	run.mu.Lock()
	if run.Process != nil && !run.Done {
		run.Process.Kill()
		run.Status = "stopped"
		exitCode := -1
		run.ExitCode = &exitCode
		run.Done = true
	}
	run.mu.Unlock()

	response.Success(c, gin.H{"message": "已停止"})
}

func (h *ScriptHandler) DebugClear(c *gin.Context) {
	runID := c.Param("run_id")

	h.mu.Lock()
	run, exists := h.debugRuns[runID]
	if exists {
		run.mu.Lock()
		if run.Process != nil && !run.Done {
			run.Process.Kill()
		}
		run.mu.Unlock()
		delete(h.debugRuns, runID)
	}
	h.mu.Unlock()

	response.Success(c, gin.H{"message": "已清除"})
}

func (h *ScriptHandler) Format(c *gin.Context) {
	var req struct {
		Content   string `json:"content" binding:"required"`
		Language  string `json:"language" binding:"required"`
		Formatter string `json:"formatter"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "请求参数错误")
		return
	}

	var formatted string
	var usedFormatter string

	switch req.Language {
	case "python":
		formatted, usedFormatter = formatPython(req.Content)
	case "shell":
		formatted, usedFormatter = formatShell(req.Content)
	case "json":
		formatted, usedFormatter = formatJSON(req.Content)
	default:
		response.BadRequest(c, "不支持的语言")
		return
	}

	if formatted == "" {
		formatted = req.Content
	}

	response.Success(c, gin.H{
		"data": gin.H{
			"content":   formatted,
			"language":  req.Language,
			"formatter": usedFormatter,
		},
	})
}

func formatPython(content string) (string, string) {
	if _, err := exec.LookPath("black"); err == nil {
		cmd := exec.Command("black", "--line-length", "88", "--quiet", "-")
		cmd.Stdin = strings.NewReader(content)
		out, err := cmd.Output()
		if err == nil {
			return string(out), "black"
		}
	}
	if _, err := exec.LookPath("autopep8"); err == nil {
		cmd := exec.Command("autopep8", "--max-line-length", "88", "-a", "-")
		cmd.Stdin = strings.NewReader(content)
		out, err := cmd.Output()
		if err == nil {
			return string(out), "autopep8"
		}
	}
	return content, "none"
}

func formatShell(content string) (string, string) {
	if _, err := exec.LookPath("shfmt"); err == nil {
		cmd := exec.Command("shfmt", "-i", "2", "-bn", "-ci", "-sr")
		cmd.Stdin = strings.NewReader(content)
		out, err := cmd.Output()
		if err == nil {
			return string(out), "shfmt"
		}
	}
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, " \t")
	}
	return strings.Join(lines, "\n"), "basic"
}

func formatJSON(content string) (string, string) {
	var obj interface{}
	if err := json.Unmarshal([]byte(content), &obj); err != nil {
		return content, "none"
	}
	out, err := json.MarshalIndent(obj, "", "  ")
	if err != nil {
		return content, "none"
	}
	return string(out), "json"
}

func (h *ScriptHandler) RegisterRoutes(r *gin.RouterGroup) {
	scripts := r.Group("/scripts", middleware.JWTAuth())
	{
		scripts.GET("", h.List)
		scripts.GET("/tree", h.Tree)
		scripts.GET("/content", h.GetContent)
		scripts.PUT("/content", h.SaveContent)
		scripts.POST("/upload", h.Upload)
		scripts.DELETE("", h.Delete)
		scripts.POST("/directory", h.CreateDirectory)
		scripts.PUT("/rename", h.Rename)
		scripts.PUT("/move", h.Move)
		scripts.POST("/copy", h.Copy)
		scripts.DELETE("/batch", h.BatchDelete)
		scripts.GET("/versions", h.ListVersions)
		scripts.GET("/versions/:id", h.GetVersion)
		scripts.PUT("/versions/:id/rollback", h.Rollback)
		scripts.POST("/run", h.DebugRun)
		scripts.GET("/run/:run_id/logs", h.DebugLogs)
		scripts.PUT("/run/:run_id/stop", h.DebugStop)
		scripts.DELETE("/run/:run_id", h.DebugClear)
		scripts.POST("/format", h.Format)
	}
}
