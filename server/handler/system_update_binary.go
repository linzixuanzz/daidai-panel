package handler

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"daidai-panel/config"
	"daidai-panel/model"
	"daidai-panel/service"
)

const (
	binaryUpdateProtectedConfig = "config.yaml"
)

func buildBinaryPanelUpdatePlan(release *panelReleaseInfo) (*panelUpdatePlan, error) {
	if isMagiskPanelRuntime() {
		return nil, fmt.Errorf("Magisk 模块版请通过 Magisk / KernelSU / APatch 管理器更新模块")
	}

	assetName, binaryName, err := resolveBinaryReleaseTarget(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return nil, err
	}

	if release == nil {
		release, err = fetchLatestPanelRelease()
		if err != nil {
			return nil, err
		}
	}

	asset, ok := release.findAsset(assetName)
	if !ok || strings.TrimSpace(asset.BrowserDownloadURL) == "" {
		return nil, fmt.Errorf("当前 Release 未提供适配本机平台的二进制包 %s", assetName)
	}

	executablePath, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("无法识别当前面板程序路径: %w", err)
	}

	installDir, err := filepath.Abs(filepath.Dir(executablePath))
	if err != nil {
		return nil, fmt.Errorf("无法识别当前安装目录: %w", err)
	}

	serverPIDFile := resolvePanelServerPIDFile()
	serverPID := readPIDFileIfExists(serverPIDFile)

	return &panelUpdatePlan{
		DeploymentType: panelUpdateDeploymentBinary,
		ReleaseVersion: strings.TrimSpace(release.version()),
		AssetName:      asset.Name,
		AssetURL:       asset.BrowserDownloadURL,
		AssetDigest:    asset.Digest,
		InstallDir:     installDir,
		BinaryName:     binaryName,
		ExecutablePath: executablePath,
		CurrentPID:     os.Getpid(),
		ServerPID:      serverPID,
		ServerPIDFile:  serverPIDFile,
	}, nil
}

func resolveBinaryReleaseTarget(goos, goarch string) (assetName, binaryName string, err error) {
	goos = strings.ToLower(strings.TrimSpace(goos))
	goarch = strings.ToLower(strings.TrimSpace(goarch))

	switch goos {
	case "windows":
		if goarch == "amd64" {
			return "daidai-windows-amd64.zip", "daidai-server.exe", nil
		}
	case "linux":
		switch goarch {
		case "amd64":
			return "daidai-linux-amd64.tar.gz", "daidai-linux-amd64", nil
		case "arm64":
			return "daidai-linux-arm64.tar.gz", "daidai-linux-arm64", nil
		case "386":
			return "daidai-linux-386.tar.gz", "daidai-linux-386", nil
		case "arm":
			return "daidai-linux-armv7.tar.gz", "daidai-linux-armv7", nil
		}
	}

	return "", "", fmt.Errorf("当前平台 %s/%s 暂未提供二进制后台更新包", goos, goarch)
}

func isMagiskPanelRuntime() bool {
	for _, marker := range []string{
		"/data/adb/daidai-panel/ports.conf",
		"/data/adb/modules/daidai-panel/module.prop",
		"/data/adb/modules_update/daidai-panel/module.prop",
	} {
		if _, err := os.Stat(marker); err == nil {
			return true
		}
	}
	return false
}

func executeBinaryPanelUpdateWithOptions(plan *panelUpdatePlan, options panelUpdateExecutionOptions) {
	panelUpdater.setRunning("preparing", "正在准备二进制后台更新目录")

	workDir, err := createBinaryUpdateWorkDir(plan)
	if err != nil {
		failPanelBinaryUpdate(options, err)
		return
	}

	archivePath := filepath.Join(workDir, sanitizeUpdateFileName(plan.AssetName))
	panelUpdater.setRunning("downloading", fmt.Sprintf("正在下载二进制更新包 %s", plan.AssetName))
	if err := downloadBinaryUpdateAsset(resolveBinaryUpdateDownloadURL(plan.AssetURL), archivePath, plan.AssetDigest); err != nil {
		failPanelBinaryUpdate(options, err)
		return
	}

	extractDir := filepath.Join(workDir, "extract")
	panelUpdater.setRunning("extracting", "更新包已下载完成，正在安全解压并校验内容")
	if err := extractBinaryUpdateArchive(archivePath, extractDir); err != nil {
		failPanelBinaryUpdate(options, err)
		return
	}

	packageRoot, err := findBinaryUpdatePackageRoot(extractDir, plan.BinaryName)
	if err != nil {
		failPanelBinaryUpdate(options, err)
		return
	}

	panelUpdater.setRunning("scheduling", "更新包已准备完成，正在启动后台替换脚本")
	scriptPath, err := writeBinaryUpdateHelperScript(plan, packageRoot, workDir)
	if err != nil {
		failPanelBinaryUpdate(options, err)
		return
	}

	if err := startBinaryUpdateHelper(scriptPath); err != nil {
		failPanelBinaryUpdate(options, err)
		return
	}

	panelUpdater.setRestarting("后台更新脚本已启动，将保留 config.yaml 与数据目录并重启面板")
	go func() {
		time.Sleep(1500 * time.Millisecond)
		// 面板进程里走主程序的完整关停再以 0 退出；helper 脚本会一直等到本进程退出才替换文件，不受多等几十毫秒影响。
		// `ddp update` 走这里时 panelProcessExit 没被接线，仍是直接 os.Exit（CLI 自己退出），行为不变。
		panelProcessExit(0)
	}()
}

func failPanelBinaryUpdate(options panelUpdateExecutionOptions, err error) {
	panelUpdater.fail(err)
	if options.AutoUpdate {
		notifyAutoUpdateFailure(options.TargetVersion, err)
	}
}

func createBinaryUpdateWorkDir(plan *panelUpdatePlan) (string, error) {
	baseDir := filepath.Join(resolveBinaryUpdateBaseDir(), "updates")
	if err := os.MkdirAll(baseDir, 0o755); err != nil {
		return "", fmt.Errorf("创建更新目录失败: %w", err)
	}

	// 清掉上一次留下的工作目录。每次更新会在这里解压出一份完整的二进制 + 前端（约 50MB），
	// 之前从没人清理，在 Magisk 模块版这种装在手机 /data 上的部署里尤其明显：
	// 每升一次涨 50MB，还会一起被算进重刷模块时的数据备份。
	// panelUpdater.begin() 保证同一时刻只有一个更新任务，删掉的一定是已经跑完的。
	if entries, err := os.ReadDir(baseDir); err == nil {
		for _, entry := range entries {
			os.RemoveAll(filepath.Join(baseDir, entry.Name()))
		}
	}

	versionPart := sanitizeUpdateFileName(plan.ReleaseVersion)
	if versionPart == "" {
		versionPart = time.Now().Format("20060102150405")
	}
	dir := filepath.Join(baseDir, "panel-"+versionPart+"-"+strconv.FormatInt(time.Now().UnixNano(), 36))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("创建更新工作目录失败: %w", err)
	}
	return dir, nil
}

func resolveBinaryUpdateBaseDir() string {
	if config.C != nil && strings.TrimSpace(config.C.Data.Dir) != "" {
		return config.C.Data.Dir
	}
	return filepath.Join(os.TempDir(), "daidai-panel")
}

// parseReleaseAssetSHA256 从 GitHub 附件的 digest（"sha256:<hex>"）里取出十六进制校验值。
// 拿不到合法的 SHA-256 就不更新：更新包会直接替换面板程序，宁可让用户手动升级也不能装一个没核对过的包。
func parseReleaseAssetSHA256(digest string) (string, error) {
	digest = strings.ToLower(strings.TrimSpace(digest))
	if digest == "" {
		return "", fmt.Errorf("GitHub Release 没有提供更新包的 SHA-256 校验值，无法确认下载内容完整可信，已中止更新；请稍后重试或手动下载升级")
	}
	hexValue, ok := strings.CutPrefix(digest, "sha256:")
	if !ok {
		return "", fmt.Errorf("更新包校验值格式不受支持（%s），已中止更新；请手动下载升级", digest)
	}
	if len(hexValue) != sha256.Size*2 {
		return "", fmt.Errorf("更新包 SHA-256 校验值格式无效，已中止更新；请手动下载升级")
	}
	if _, err := hex.DecodeString(hexValue); err != nil {
		return "", fmt.Errorf("更新包 SHA-256 校验值格式无效，已中止更新；请手动下载升级")
	}
	return hexValue, nil
}

// downloadBinaryUpdateAsset 下载更新包并核对 SHA-256（expectedDigest 来自 GitHub Release 附件的 digest）。
// 校验值缺失或不一致时删掉已下载的文件并返回错误，调用方据此中止更新。
func downloadBinaryUpdateAsset(assetURL, archivePath, expectedDigest string) (err error) {
	assetURL = strings.TrimSpace(assetURL)
	if assetURL == "" {
		return fmt.Errorf("更新包下载地址为空")
	}
	// 先确认有校验值再下载，省得白下几十 MB。
	expectedSHA256, err := parseReleaseAssetSHA256(expectedDigest)
	if err != nil {
		return err
	}

	client := service.NewHTTPClient(20 * time.Minute)
	req, err := http.NewRequest(http.MethodGet, assetURL, nil)
	if err != nil {
		return fmt.Errorf("构建更新包下载请求失败: %w", err)
	}
	req.Header.Set("User-Agent", "daidai-panel-updater")

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("下载二进制更新包失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("下载二进制更新包失败: HTTP %d", resp.StatusCode)
	}

	file, err := os.OpenFile(archivePath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("创建更新包文件失败: %w", err)
	}
	// 任何一步失败都不留下半截或未通过校验的更新包，避免后续流程误用。
	defer func() {
		if err != nil {
			_ = os.Remove(archivePath)
		}
	}()

	hasher := sha256.New()
	_, copyErr := io.Copy(io.MultiWriter(file, hasher), resp.Body)
	closeErr := file.Close()
	if copyErr != nil {
		return fmt.Errorf("保存二进制更新包失败: %w", copyErr)
	}
	if closeErr != nil {
		return fmt.Errorf("保存二进制更新包失败: %w", closeErr)
	}

	actualSHA256 := hex.EncodeToString(hasher.Sum(nil))
	if actualSHA256 != expectedSHA256 {
		return fmt.Errorf("更新包 SHA-256 校验失败（期望 %s，实际 %s），下载内容可能被篡改或加速代理返回了错误文件，已中止更新", expectedSHA256, actualSHA256)
	}
	return nil
}

func resolveBinaryUpdateDownloadURL(assetURL string) string {
	assetURL = strings.TrimSpace(assetURL)
	if assetURL == "" {
		return ""
	}

	proxyBase := strings.TrimSpace(model.GetRegisteredConfig("binary_update_proxy"))
	if proxyBase == "" {
		return assetURL
	}
	proxyBase = strings.TrimRight(proxyBase, "/") + "/"
	return proxyBase + assetURL
}

func extractBinaryUpdateArchive(archivePath, extractDir string) error {
	if err := os.MkdirAll(extractDir, 0o755); err != nil {
		return fmt.Errorf("创建解压目录失败: %w", err)
	}

	lower := strings.ToLower(archivePath)
	switch {
	case strings.HasSuffix(lower, ".zip"):
		return extractZipArchive(archivePath, extractDir)
	case strings.HasSuffix(lower, ".tar.gz"), strings.HasSuffix(lower, ".tgz"):
		return extractTarGzArchive(archivePath, extractDir)
	default:
		return fmt.Errorf("不支持的二进制更新包格式: %s", filepath.Base(archivePath))
	}
}

func extractZipArchive(archivePath, extractDir string) error {
	reader, err := zip.OpenReader(archivePath)
	if err != nil {
		return fmt.Errorf("打开 zip 更新包失败: %w", err)
	}
	defer reader.Close()

	for _, file := range reader.File {
		targetPath, err := safeArchiveTargetPath(extractDir, file.Name)
		if err != nil {
			return err
		}

		mode := file.FileInfo().Mode()
		if mode&os.ModeSymlink != 0 {
			continue
		}

		if file.FileInfo().IsDir() {
			if err := os.MkdirAll(targetPath, 0o755); err != nil {
				return fmt.Errorf("创建目录失败: %w", err)
			}
			continue
		}

		if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
			return fmt.Errorf("创建目录失败: %w", err)
		}

		src, err := file.Open()
		if err != nil {
			return fmt.Errorf("读取 zip 文件失败: %w", err)
		}

		dst, err := os.OpenFile(targetPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode.Perm())
		if err != nil {
			src.Close()
			return fmt.Errorf("写入解压文件失败: %w", err)
		}
		_, copyErr := io.Copy(dst, src)
		closeErr := dst.Close()
		src.Close()
		if copyErr != nil {
			return fmt.Errorf("解压 zip 文件失败: %w", copyErr)
		}
		if closeErr != nil {
			return fmt.Errorf("关闭解压文件失败: %w", closeErr)
		}
	}
	return nil
}

func extractTarGzArchive(archivePath, extractDir string) error {
	file, err := os.Open(archivePath)
	if err != nil {
		return fmt.Errorf("打开 tar.gz 更新包失败: %w", err)
	}
	defer file.Close()

	gzipReader, err := gzip.NewReader(file)
	if err != nil {
		return fmt.Errorf("读取 gzip 更新包失败: %w", err)
	}
	defer gzipReader.Close()

	tarReader := tar.NewReader(gzipReader)
	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("读取 tar 更新包失败: %w", err)
		}

		targetPath, err := safeArchiveTargetPath(extractDir, header.Name)
		if err != nil {
			return err
		}

		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(targetPath, 0o755); err != nil {
				return fmt.Errorf("创建目录失败: %w", err)
			}
		case tar.TypeReg, tar.TypeRegA:
			if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
				return fmt.Errorf("创建目录失败: %w", err)
			}
			mode := os.FileMode(header.Mode).Perm()
			if mode == 0 {
				mode = 0o644
			}
			dst, err := os.OpenFile(targetPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
			if err != nil {
				return fmt.Errorf("写入解压文件失败: %w", err)
			}
			_, copyErr := io.Copy(dst, tarReader)
			closeErr := dst.Close()
			if copyErr != nil {
				return fmt.Errorf("解压 tar 文件失败: %w", copyErr)
			}
			if closeErr != nil {
				return fmt.Errorf("关闭解压文件失败: %w", closeErr)
			}
		default:
			continue
		}
	}
	return nil
}

func safeArchiveTargetPath(baseDir, name string) (string, error) {
	cleanName := filepath.Clean(filepath.FromSlash(strings.ReplaceAll(name, "\\", "/")))
	if cleanName == "." || strings.HasPrefix(cleanName, ".."+string(os.PathSeparator)) || filepath.IsAbs(cleanName) {
		return "", fmt.Errorf("更新包包含不安全路径: %s", name)
	}

	targetPath := filepath.Join(baseDir, cleanName)
	absBase, err := filepath.Abs(baseDir)
	if err != nil {
		return "", err
	}
	absTarget, err := filepath.Abs(targetPath)
	if err != nil {
		return "", err
	}
	if absTarget != absBase && !strings.HasPrefix(absTarget, absBase+string(os.PathSeparator)) {
		return "", fmt.Errorf("更新包包含越界路径: %s", name)
	}
	return targetPath, nil
}

func findBinaryUpdatePackageRoot(extractDir, binaryName string) (string, error) {
	candidates := []string{extractDir}
	entries, err := os.ReadDir(extractDir)
	if err != nil {
		return "", fmt.Errorf("读取解压目录失败: %w", err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			candidates = append(candidates, filepath.Join(extractDir, entry.Name()))
		}
	}

	for _, candidate := range candidates {
		if info, err := os.Stat(filepath.Join(candidate, binaryName)); err == nil && !info.IsDir() {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("更新包中未找到目标程序 %s", binaryName)
}

func writeBinaryUpdateHelperScript(plan *panelUpdatePlan, sourceRoot, workDir string) (string, error) {
	if runtime.GOOS == "windows" {
		return writeWindowsBinaryUpdateHelperScript(plan, sourceRoot, workDir)
	}
	return writeUnixBinaryUpdateHelperScript(plan, sourceRoot, workDir)
}

func writeUnixBinaryUpdateHelperScript(plan *panelUpdatePlan, sourceRoot, workDir string) (string, error) {
	scriptPath := filepath.Join(workDir, "apply-update.sh")
	logPath := filepath.Join(workDir, "binary-update.log")
	serviceManager := service.ResolvePanelServiceManager()
	serviceName := service.ResolvePanelServiceName()
	systemdStopBlock := ""
	systemdStartBlock := ""
	if serviceManager == service.PanelServiceManagerSystemd && serviceName != "" {
		systemdStopBlock = fmt.Sprintf(`
if command -v systemctl >/dev/null 2>&1; then
  log "stop systemd service: %s"
  systemctl stop %s >/dev/null 2>&1 || true
fi
`, serviceName, shellQuote(serviceName))
		systemdStartBlock = fmt.Sprintf(`
if command -v systemctl >/dev/null 2>&1; then
  log "start systemd service: %s"
  systemctl start %s >/dev/null 2>&1 || true
  exit 0
fi
`, serviceName, shellQuote(serviceName))
	}
	body := fmt.Sprintf(`#!/bin/sh
set -eu
PID=%d
SERVER_PID=%d
SRC=%s
DEST=%s
BINARY=%s
LOG=%s

log() {
  printf '%%s %%s\n' "$(date '+%%Y-%%m-%%d %%H:%%M:%%S')" "$1" >> "$LOG"
}

if [ -z "$DEST" ] || [ "$DEST" = "/" ]; then
  log "refuse to update unsafe install dir: $DEST"
  exit 1
fi
%s

if [ "$SERVER_PID" -gt 0 ] && [ "$SERVER_PID" != "$PID" ]; then
  kill -TERM "$SERVER_PID" 2>/dev/null || true
fi

while kill -0 "$PID" 2>/dev/null; do
  sleep 1
done

if [ "$SERVER_PID" -gt 0 ] && [ "$SERVER_PID" != "$PID" ]; then
  i=0
  while kill -0 "$SERVER_PID" 2>/dev/null && [ "$i" -lt 15 ]; do
    i=$((i + 1))
    sleep 1
  done
  if kill -0 "$SERVER_PID" 2>/dev/null; then
    kill -KILL "$SERVER_PID" 2>/dev/null || true
  fi
fi

log "copy update files from $SRC to $DEST"
for item in "$SRC"/* "$SRC"/.[!.]* "$SRC"/..?*; do
  [ -e "$item" ] || continue
  name=$(basename "$item")
  case "$name" in
    %s|Dumb-Panel|data|logs|backups|.env)
      log "skip protected item: $name"
      continue
      ;;
  esac
  if [ "$name" = "web" ] && [ -e "$DEST/web" ]; then
    rm -rf "$DEST/web"
  fi
  cp -R "$item" "$DEST/"
done

chmod +x "$DEST/$BINARY" 2>/dev/null || true
cd "$DEST"
if [ ! -x "./$BINARY" ]; then
  log "target binary is not executable: $DEST/$BINARY"
  exit 1
fi
%s

log "start new panel process: $BINARY"
nohup "./$BINARY" >/dev/null 2>&1 &
`, plan.CurrentPID, plan.ServerPID, shellQuote(sourceRoot), shellQuote(plan.InstallDir), shellQuote(plan.BinaryName), shellQuote(logPath), systemdStopBlock, binaryUpdateProtectedConfig, systemdStartBlock)

	if err := os.WriteFile(scriptPath, []byte(body), 0o755); err != nil {
		return "", fmt.Errorf("写入更新脚本失败: %w", err)
	}
	return scriptPath, nil
}

func writeWindowsBinaryUpdateHelperScript(plan *panelUpdatePlan, sourceRoot, workDir string) (string, error) {
	scriptPath := filepath.Join(workDir, "apply-update.ps1")
	logPath := filepath.Join(workDir, "binary-update.log")
	body := fmt.Sprintf(`$ErrorActionPreference = 'Stop'
$PidToWait = %d
$ServerPid = %d
$Source = %s
$Target = %s
$Binary = %s
$Log = %s

function Write-UpdateLog([string]$Text) {
  $Line = "$(Get-Date -Format 'yyyy-MM-dd HH:mm:ss') $Text"
  Add-Content -LiteralPath $Log -Value $Line -Encoding UTF8
}

try {
  if ([string]::IsNullOrWhiteSpace($Target) -or $Target -eq '\') {
    throw "unsafe install dir: $Target"
  }

  if ($ServerPid -gt 0 -and $ServerPid -ne $PidToWait) {
    Stop-Process -Id $ServerPid -ErrorAction SilentlyContinue
  }

  while (Get-Process -Id $PidToWait -ErrorAction SilentlyContinue) {
    Start-Sleep -Milliseconds 500
  }

  if ($ServerPid -gt 0 -and $ServerPid -ne $PidToWait) {
    $deadline = (Get-Date).AddSeconds(15)
    while ((Get-Date) -lt $deadline -and (Get-Process -Id $ServerPid -ErrorAction SilentlyContinue)) {
      Start-Sleep -Milliseconds 500
    }
    Stop-Process -Id $ServerPid -Force -ErrorAction SilentlyContinue
  }

  Write-UpdateLog "copy update files from $Source to $Target"
  $Protected = @('%s', 'Dumb-Panel', 'data', 'logs', 'backups', '.env')
  Get-ChildItem -LiteralPath $Source -Force | ForEach-Object {
    if ($Protected -contains $_.Name) {
      Write-UpdateLog "skip protected item: $($_.Name)"
    } else {
      $Destination = Join-Path $Target $_.Name
      if ($_.PSIsContainer) {
        if ($_.Name -eq 'web' -and (Test-Path -LiteralPath $Destination)) {
          Remove-Item -LiteralPath $Destination -Recurse -Force
        }
        Copy-Item -LiteralPath $_.FullName -Destination $Target -Recurse -Force
      } else {
        Copy-Item -LiteralPath $_.FullName -Destination $Destination -Force
      }
    }
  }

  $StartBat = Join-Path $Target 'start.bat'
  $BinaryPath = Join-Path $Target $Binary
  if (Test-Path -LiteralPath $StartBat) {
    Write-UpdateLog "start panel with start.bat"
    Start-Process -FilePath $StartBat -WorkingDirectory $Target
  } elseif (Test-Path -LiteralPath $BinaryPath) {
    Write-UpdateLog "start panel with $Binary"
    Start-Process -FilePath $BinaryPath -WorkingDirectory $Target
  } else {
    throw "target binary not found: $BinaryPath"
  }
} catch {
  Write-UpdateLog "update failed: $($_.Exception.Message)"
  exit 1
}
`, plan.CurrentPID, plan.ServerPID, powerShellQuote(sourceRoot), powerShellQuote(plan.InstallDir), powerShellQuote(plan.BinaryName), powerShellQuote(logPath), binaryUpdateProtectedConfig)

	if err := os.WriteFile(scriptPath, []byte(body), 0o600); err != nil {
		return "", fmt.Errorf("写入更新脚本失败: %w", err)
	}
	return scriptPath, nil
}

func startBinaryUpdateHelper(scriptPath string) error {
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.Command("powershell.exe", "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", scriptPath)
	} else {
		cmd = exec.Command("/bin/sh", scriptPath)
	}
	cmd.Dir = filepath.Dir(scriptPath)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("启动后台更新脚本失败: %w", err)
	}
	return nil
}

func sanitizeUpdateFileName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	replacer := strings.NewReplacer(
		"\\", "_",
		"/", "_",
		":", "_",
		"*", "_",
		"?", "_",
		"\"", "_",
		"<", "_",
		">", "_",
		"|", "_",
	)
	return replacer.Replace(name)
}

func resolvePanelServerPIDFile() string {
	if config.C == nil || strings.TrimSpace(config.C.Data.Dir) == "" {
		return ""
	}
	return filepath.Join(config.C.Data.Dir, "run", "daidai-server.pid")
}

func readPIDFileIfExists(path string) int {
	if strings.TrimSpace(path) == "" {
		return 0
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil || pid <= 0 {
		return 0
	}
	return pid
}

func powerShellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}
