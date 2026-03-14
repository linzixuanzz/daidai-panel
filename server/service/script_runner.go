package service

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

type ScriptResult struct {
	ReturnCode int
	Output     string
	Truncated  bool
}

type OnOutputFunc func(line string)

func RunCommand(command, scriptsDir string, timeout int, envVars map[string]string, maxLogSize int, onOutput OnOutputFunc) (*ScriptResult, *os.Process, error) {
	interpreter, scriptPath, err := validateCommand(command, scriptsDir)
	if err != nil {
		return nil, nil, err
	}

	cmd := buildCmd(interpreter, scriptPath, scriptsDir, envVars)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create stdout pipe: %w", err)
	}
	cmd.Stderr = cmd.Stdout

	if err := cmd.Start(); err != nil {
		return nil, nil, fmt.Errorf("failed to start process: %w", err)
	}

	process := cmd.Process

	var outputBuilder strings.Builder
	totalSize := 0
	truncated := false

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)

	done := make(chan struct{})
	go func() {
		for scanner.Scan() {
			line := scanner.Text()
			if totalSize < maxLogSize {
				outputBuilder.WriteString(line + "\n")
				totalSize += len(line) + 1
				if onOutput != nil {
					onOutput(line)
				}
			} else if !truncated {
				truncated = true
				msg := "\n[日志已截断，超过最大大小限制]"
				outputBuilder.WriteString(msg)
				if onOutput != nil {
					onOutput(msg)
				}
			}
		}
		close(done)
	}()

	timer := time.NewTimer(time.Duration(timeout) * time.Second)
	defer timer.Stop()

	waitCh := make(chan error, 1)
	go func() {
		waitCh <- cmd.Wait()
	}()

	var returnCode int
	select {
	case err := <-waitCh:
		<-done
		if err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				returnCode = exitErr.ExitCode()
			} else {
				returnCode = 1
			}
		}
	case <-timer.C:
		cmd.Process.Kill()
		<-done
		<-waitCh
		returnCode = -1
		msg := fmt.Sprintf("\n[任务超时，已在 %d 秒后终止]", timeout)
		outputBuilder.WriteString(msg)
		if onOutput != nil {
			onOutput(msg)
		}
	}

	return &ScriptResult{
		ReturnCode: returnCode,
		Output:     outputBuilder.String(),
		Truncated:  truncated,
	}, process, nil
}

var extInterpreterMap = map[string]string{
	".py": "python3",
	".js": "node",
	".ts": "ts-node",
	".sh": "bash",
}

var desiInterpreterMap = map[string]string{
	".js": "node",
	".py": "python3",
	".ts": "ts-node",
	".sh": "bash",
}

func validateCommand(command, scriptsDir string) (string, string, error) {
	parts := strings.Fields(command)
	if len(parts) < 2 {
		return "", "", fmt.Errorf("命令格式无效，格式: <解释器> <脚本路径>")
	}

	interpreter := parts[0]
	scriptPath := strings.Join(parts[1:], " ")

	if interpreter == "task" {
		ext := strings.ToLower(filepath.Ext(scriptPath))
		mapped, ok := extInterpreterMap[ext]
		if !ok {
			return "", "", fmt.Errorf("task 命令不支持的文件扩展名: %s", ext)
		}
		interpreter = mapped
	}

	if interpreter == "desi" {
		ext := strings.ToLower(filepath.Ext(scriptPath))
		mapped, ok := desiInterpreterMap[ext]
		if !ok {
			return "", "", fmt.Errorf("desi 命令不支持的文件扩展名: %s", ext)
		}
		interpreter = mapped
	}

	allowedInterpreters := map[string]bool{
		"python": true, "python3": true, "node": true, "ts-node": true, "bash": true,
	}
	if !allowedInterpreters[interpreter] {
		return "", "", fmt.Errorf("不支持的解释器: %s", interpreter)
	}

	dangerous := []string{"..", "~", "$", "`", ";", "|", "&", ">", "<"}
	for _, d := range dangerous {
		if strings.Contains(scriptPath, d) {
			return "", "", fmt.Errorf("脚本路径包含危险字符: %s", d)
		}
	}

	allowedExts := map[string]bool{
		".py": true, ".js": true, ".ts": true, ".sh": true,
	}
	ext := filepath.Ext(scriptPath)
	if !allowedExts[ext] {
		return "", "", fmt.Errorf("不支持的文件扩展名: %s", ext)
	}

	fullPath := filepath.Join(scriptsDir, scriptPath)
	fullPath, err := filepath.Abs(fullPath)
	if err != nil {
		return "", "", fmt.Errorf("无效路径: %w", err)
	}

	absScriptsDir, _ := filepath.Abs(scriptsDir)
	if !strings.HasPrefix(fullPath, absScriptsDir) {
		return "", "", fmt.Errorf("检测到路径遍历攻击")
	}

	if _, err := os.Stat(fullPath); os.IsNotExist(err) {
		return "", "", fmt.Errorf("脚本不存在: %s", scriptPath)
	}

	return interpreter, fullPath, nil
}

func buildCmd(interpreter, fullPath, scriptsDir string, envVars map[string]string) *exec.Cmd {
	var cmd *exec.Cmd

	switch interpreter {
	case "python", "python3":
		cmd = exec.Command(interpreter, "-u", fullPath)
	case "ts-node":
		cmd = exec.Command("npx", "ts-node", fullPath)
	default:
		cmd = exec.Command(interpreter, fullPath)
	}

	cmd.Dir = scriptsDir
	cmd.Env = buildEnv(envVars)

	return cmd
}

func buildEnv(envVars map[string]string) []string {
	safeKeys := []string{"PATH", "HOME", "USER", "LANG", "LC_ALL", "TZ"}
	if runtime.GOOS == "windows" {
		safeKeys = append(safeKeys, "SYSTEMROOT", "PATHEXT", "TEMP", "TMP", "APPDATA", "LOCALAPPDATA", "USERPROFILE")
	}

	env := make([]string, 0)
	for _, key := range safeKeys {
		if val := os.Getenv(key); val != "" {
			env = append(env, key+"="+val)
		}
	}

	dangerousVars := map[string]bool{
		"LD_PRELOAD": true, "LD_LIBRARY_PATH": true, "DYLD_INSERT_LIBRARIES": true,
		"PYTHONPATH": true, "NODE_PATH": true,
	}

	for k, v := range envVars {
		if dangerousVars[k] {
			continue
		}
		if strings.ContainsRune(v, 0) {
			continue
		}
		env = append(env, k+"="+v)
	}

	return env
}

func RunInlineScript(content, scriptsDir string, envVars map[string]string, timeout int, onOutput OnOutputFunc) error {
	tmpFile := filepath.Join(scriptsDir, fmt.Sprintf(".hook_%d.sh", time.Now().UnixNano()))
	if err := os.WriteFile(tmpFile, []byte(content), 0755); err != nil {
		return err
	}
	defer os.Remove(tmpFile)

	cmd := exec.Command("bash", tmpFile)
	cmd.Dir = scriptsDir
	cmd.Env = buildEnv(envVars)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	cmd.Stderr = cmd.Stdout

	if err := cmd.Start(); err != nil {
		return err
	}

	scanner := bufio.NewScanner(stdout)
	go func() {
		for scanner.Scan() {
			if onOutput != nil {
				onOutput(scanner.Text())
			}
		}
	}()

	timer := time.NewTimer(time.Duration(timeout) * time.Second)
	defer timer.Stop()

	waitCh := make(chan error, 1)
	go func() {
		waitCh <- cmd.Wait()
	}()

	select {
	case err := <-waitCh:
		return err
	case <-timer.C:
		cmd.Process.Kill()
		<-waitCh
		return fmt.Errorf("钩子脚本超时，已超过 %d 秒", timeout)
	}
}

func RunHookScript(scriptName, scriptsDir string, envVars map[string]string, onOutput OnOutputFunc) {
	hookPath := filepath.Join(scriptsDir, scriptName)
	if _, err := os.Stat(hookPath); os.IsNotExist(err) {
		return
	}

	absPath, _ := filepath.Abs(hookPath)
	absDir, _ := filepath.Abs(scriptsDir)
	if !strings.HasPrefix(absPath, absDir) {
		return
	}

	cmd := exec.Command("bash", hookPath)
	cmd.Dir = scriptsDir
	cmd.Env = buildEnv(envVars)

	stdout, _ := cmd.StdoutPipe()
	cmd.Stderr = cmd.Stdout

	if err := cmd.Start(); err != nil {
		if onOutput != nil {
			onOutput(fmt.Sprintf("[hook %s failed to start: %s]", scriptName, err))
		}
		return
	}

	scanner := bufio.NewScanner(stdout)
	go func() {
		for scanner.Scan() {
			if onOutput != nil {
				onOutput(scanner.Text())
			}
		}
	}()

	timer := time.NewTimer(60 * time.Second)
	defer timer.Stop()

	waitCh := make(chan error, 1)
	go func() {
		waitCh <- cmd.Wait()
	}()

	select {
	case <-waitCh:
	case <-timer.C:
		cmd.Process.Kill()
		<-waitCh
	}
}
