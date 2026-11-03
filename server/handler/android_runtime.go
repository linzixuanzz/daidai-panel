package handler

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"daidai-panel/middleware"
	"daidai-panel/pkg/response"

	"github.com/gin-gonic/gin"
)

// AndroidRuntimeHandler 提供在 Magisk 环境里一键安装 Python / Node.js 等脚本运行时的能力。
//
// 背景：Docker 镜像里 apk / apt 装好了 python、nodejs，但在 Android 上没有这些。模块默认
// 只打包面板本体，解释器需要用户另外提供。这个 handler 让用户可以：
//   - 在面板里看到当前 Android 运行时的 bin 目录有没有 python/node；
//   - 一键触发下载 + 解压（若用户已装 Termux 则优先使用 Termux 的 pkg）。
//
// 只在检测到当前是 Android 环境时才暴露给前端。
type AndroidRuntimeHandler struct{}

func NewAndroidRuntimeHandler() *AndroidRuntimeHandler { return &AndroidRuntimeHandler{} }

// bin 目录约定：Magisk service.sh 会把这里加入 PATH / LD_LIBRARY_PATH。
const defaultAndroidRuntimeBinDir = "/data/adb/daidai-panel/bin"

// androidRuntimePreset 定义了面板预置的运行时下载源。
type androidRuntimePreset struct {
	Name            string `json:"name"`             // python / node
	Label           string `json:"label"`            // 展示用
	Arch            string `json:"arch"`             // arm64 / amd64
	URL             string `json:"url"`              // 下载地址 (tar.gz)
	StripComponents int    `json:"strip_components"` // 解压时去掉的顶层目录层数
	CheckBin        string `json:"check_bin"`        // 解压后期望存在的可执行文件相对路径 (相对 bin 目录)
	SizeMB          int    `json:"size_mb"`          // 预估大小
	Note            string `json:"note"`             // 备注
}

// 预置下载源 —— 会跟随后续 Release 更新，用户也可以通过 `/install` 接口传入自定义 URL。
// 这里选择的是社区常用的静态/可移植构建：
//   - Python: python-build-standalone (indygreg) aarch64-unknown-linux-gnu / x86_64-unknown-linux-gnu
//   - Node.js: 官方 nodejs.org linux-arm64 / linux-x64 包
//
// 由于 Android 是 bionic libc，这些预构建并不总是能跑，因此同时保留 Termux 一键方案。
var androidRuntimePresets = []androidRuntimePreset{
	{
		Name:            "python",
		Label:           "Python 3.12 (python-build-standalone)",
		Arch:            "arm64",
		URL:             "https://github.com/indygreg/python-build-standalone/releases/download/20240415/cpython-3.12.3+20240415-aarch64-unknown-linux-gnu-install_only.tar.gz",
		StripComponents: 1,
		CheckBin:        "python/bin/python3",
		SizeMB:          28,
	},
	{
		Name:            "python",
		Label:           "Python 3.12 (python-build-standalone)",
		Arch:            "amd64",
		URL:             "https://github.com/indygreg/python-build-standalone/releases/download/20240415/cpython-3.12.3+20240415-x86_64-unknown-linux-gnu-install_only.tar.gz",
		StripComponents: 1,
		CheckBin:        "python/bin/python3",
		SizeMB:          30,
	},
	{
		Name:            "node",
		Label:           "Node.js v24 LTS (nodejs.org)",
		Arch:            "arm64",
		URL:             "https://nodejs.org/dist/v24.21.0/node-v24.21.0-linux-arm64.tar.gz",
		StripComponents: 1,
		CheckBin:        "node/bin/node",
		SizeMB:          55,
		Note:            "Android bionic libc 下可能需要 Termux 提供的动态库",
	},
	{
		Name:            "node",
		Label:           "Node.js v24 LTS (nodejs.org)",
		Arch:            "amd64",
		URL:             "https://nodejs.org/dist/v24.21.0/node-v24.21.0-linux-x64.tar.gz",
		StripComponents: 1,
		CheckBin:        "node/bin/node",
		SizeMB:          55,
	},
}

type androidRuntimeStatus struct {
	Supported bool                   `json:"supported"`
	Arch      string                 `json:"arch"`
	BinDir    string                 `json:"bin_dir"`
	Termux    bool                   `json:"termux_detected"`
	Runtimes  []androidRuntimeItem   `json:"runtimes"`
	Presets   []androidRuntimePreset `json:"presets"`
}

type androidRuntimeItem struct {
	Name      string `json:"name"`
	Installed bool   `json:"installed"`
	Path      string `json:"path"`
	Version   string `json:"version"`
}

// androidSupported 判断当前进程是不是跑在 Android 上（面具版）。
// 判定方式：
//  1. 真实 Android 二进制（runtime.GOOS == "android"）
//  2. Magisk 模块显式注入的环境变量
//  3. 宿主侧可见的模块目录
func androidSupported() bool {
	if runtime.GOOS == "android" {
		return true
	}
	if strings.TrimSpace(os.Getenv("DAIDAI_MAGISK_MODULE")) != "" {
		return true
	}
	if _, err := os.Stat("/data/adb/modules/daidai-panel"); err == nil {
		return true
	}
	return false
}

func resolveAndroidRuntimeBinDir() string {
	if dir := strings.TrimSpace(os.Getenv("DAIDAI_ANDROID_RUNTIME_BIN_DIR")); dir != "" {
		return dir
	}
	return defaultAndroidRuntimeBinDir
}

func detectArch() string {
	switch runtime.GOARCH {
	case "arm64":
		return "arm64"
	case "amd64":
		return "amd64"
	}
	return runtime.GOARCH
}

func termuxDetected() bool {
	for _, p := range []string{
		"/data/data/com.termux/files/usr/bin",
		"/data/user/0/com.termux/files/usr/bin",
	} {
		if info, err := os.Stat(p); err == nil && info.IsDir() {
			return true
		}
	}
	return false
}

// androidRuntimeCandidates 按优先级列出 probeRuntime 要检查的可执行文件路径。
//
// 一键安装装到 androidBinDir 的排最前（它在 service.sh 的 PATH 里也排最前，装了就是它在生效），
// 其次是 Termux，最后是容器自带的解释器。容器自带的分在两处：
//   - Magisk Debian 版的 Node 是 nodejs.org 官方包，customize.sh 解压到 /usr/local，落在 /usr/local/bin/node；
//   - Alpine 版 apk 装的 nodejs、python 在 /usr/bin。
//
// /usr/local/bin 必须排在 /usr/bin 之前，与 service.sh 的 PATH 顺序一致：两处都有时，卡片显示的才是实际运行的那个。
// 漏掉 /usr/local/bin 的话，Debian 版容器里明明有 Node，卡片却显示「未安装」并引导用户一键安装，
// 装进 androidBinDir 的那份会从此盖住容器自带的 Node。
func androidRuntimeCandidates(androidBinDir, cmdName string) []string {
	candidates := []string{
		filepath.Join(androidBinDir, cmdName, "bin", cmdName),
		filepath.Join(androidBinDir, cmdName),
		filepath.Join("/data/data/com.termux/files/usr/bin", cmdName),
		filepath.Join("/usr/local/bin", cmdName),
		filepath.Join("/usr/bin", cmdName),
	}
	if cmdName == "python" {
		candidates = append([]string{
			filepath.Join(androidBinDir, "python", "bin", "python3"),
			filepath.Join(androidBinDir, "python3"),
		}, candidates...)
	}
	return candidates
}

// probeRuntime 依次检查 androidRuntimeCandidates 给出的路径，返回第一个存在且可执行的命令。
func probeRuntime(cmdName string) androidRuntimeItem {
	item := androidRuntimeItem{Name: cmdName}

	for _, c := range androidRuntimeCandidates(resolveAndroidRuntimeBinDir(), cmdName) {
		info, err := os.Stat(c)
		if err != nil || info.IsDir() {
			continue
		}
		if info.Mode()&0o111 == 0 {
			continue
		}
		item.Installed = true
		item.Path = c

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		out, err := exec.CommandContext(ctx, c, "--version").CombinedOutput()
		cancel()
		if err == nil {
			item.Version = strings.TrimSpace(string(out))
		}
		return item
	}
	return item
}

func (h *AndroidRuntimeHandler) Status(c *gin.Context) {
	androidBinDir := resolveAndroidRuntimeBinDir()
	if !androidSupported() {
		response.Success(c, androidRuntimeStatus{
			Supported: false,
			Arch:      detectArch(),
			BinDir:    androidBinDir,
		})
		return
	}

	arch := detectArch()
	runtimes := []androidRuntimeItem{
		probeRuntime("python"),
		probeRuntime("node"),
	}

	// 过滤出和当前 arch 匹配的预置
	var presets []androidRuntimePreset
	for _, p := range androidRuntimePresets {
		if p.Arch == arch {
			presets = append(presets, p)
		}
	}

	response.Success(c, androidRuntimeStatus{
		Supported: true,
		Arch:      arch,
		BinDir:    androidBinDir,
		Termux:    termuxDetected(),
		Runtimes:  runtimes,
		Presets:   presets,
	})
}

type androidInstallRequest struct {
	Name            string `json:"name" binding:"required"` // python / node
	URL             string `json:"url"`                     // 可选：自定义下载源
	StripComponents int    `json:"strip_components"`        // 解压层数
}

// Install 以 SSE 形式流式返回下载/解压进度。
func (h *AndroidRuntimeHandler) Install(c *gin.Context) {
	androidBinDir := resolveAndroidRuntimeBinDir()
	if !androidSupported() {
		response.Error(c, http.StatusForbidden, "仅 Android 面具版支持该操作")
		return
	}

	var req androidInstallRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, http.StatusBadRequest, "参数错误: "+err.Error())
		return
	}

	if req.Name != "python" && req.Name != "node" {
		response.Error(c, http.StatusBadRequest, "name 只能是 python 或 node")
		return
	}

	// 如果没传 URL，从预置里挑匹配当前 arch 的
	if strings.TrimSpace(req.URL) == "" {
		arch := detectArch()
		for _, p := range androidRuntimePresets {
			if p.Name == req.Name && p.Arch == arch {
				req.URL = p.URL
				if req.StripComponents == 0 {
					req.StripComponents = p.StripComponents
				}
				break
			}
		}
	}
	if req.URL == "" {
		response.Error(c, http.StatusBadRequest, "当前架构没有预置下载源，请手动填写 url")
		return
	}

	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.WriteHeader(http.StatusOK)
	flusher, _ := c.Writer.(http.Flusher)
	emit := func(msg string) {
		fmt.Fprintf(c.Writer, "data: %s\n\n", strings.ReplaceAll(msg, "\n", "\\n"))
		if flusher != nil {
			flusher.Flush()
		}
	}

	emit(fmt.Sprintf("下载目标: %s", req.URL))
	emit(fmt.Sprintf("解压到: %s/%s", androidBinDir, req.Name))

	if err := os.MkdirAll(androidBinDir, 0o755); err != nil {
		emit("❌ 无法创建目标目录: " + err.Error())
		return
	}

	targetDir := filepath.Join(androidBinDir, req.Name)
	// 清理旧目录
	if _, err := os.Stat(targetDir); err == nil {
		emit("清理旧版本: " + targetDir)
		if err := os.RemoveAll(targetDir); err != nil {
			emit("❌ 清理失败: " + err.Error())
			return
		}
	}
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		emit("❌ 创建目标目录失败: " + err.Error())
		return
	}
	// 目标目录本身可能经过软链（如 /data/adb 下的挂载），后面所有「是否越界」都按解析后的真实路径比较。
	realTargetDir, err := filepath.EvalSymlinks(targetDir)
	if err != nil {
		emit("❌ 解析目标目录失败: " + err.Error())
		return
	}

	client := &http.Client{Timeout: 30 * time.Minute}
	resp, err := client.Get(req.URL)
	if err != nil {
		emit("❌ 下载失败: " + err.Error())
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		emit(fmt.Sprintf("❌ HTTP %d", resp.StatusCode))
		return
	}

	emit(fmt.Sprintf("连接成功，Content-Length=%d", resp.ContentLength))

	gzr, err := gzip.NewReader(resp.Body)
	if err != nil {
		emit("❌ 解压 gzip 失败: " + err.Error())
		return
	}
	defer gzr.Close()

	tr := tar.NewReader(gzr)
	var extractedFiles int64
	lastReport := time.Now()

	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			emit("❌ 解析 tar 失败: " + err.Error())
			return
		}

		// 去掉前 N 层目录
		name := hdr.Name
		if req.StripComponents > 0 {
			parts := strings.SplitN(name, "/", req.StripComponents+1)
			if len(parts) <= req.StripComponents {
				continue
			}
			name = parts[len(parts)-1]
		}
		if name == "" {
			continue
		}

		outPath := filepath.Join(targetDir, name)
		// 防止 tar slip
		if !strings.HasPrefix(outPath, targetDir+string(os.PathSeparator)) && outPath != targetDir {
			emit("⚠ 跳过越界路径: " + hdr.Name)
			continue
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			// 路径上已有的软链（包括 outPath 自己）解析后必须仍在目标目录内，否则 MkdirAll 会顺着软链建到外面去。
			if !androidRealPathWithin(realTargetDir, outPath) {
				emit("⚠ 跳过经软链指向目标目录外的路径: " + hdr.Name)
				continue
			}
			if err := os.MkdirAll(outPath, os.FileMode(hdr.Mode)&0o777|0o755); err != nil {
				emit("❌ 创建目录失败: " + err.Error())
				return
			}
		case tar.TypeReg, tar.TypeRegA:
			// 先确认父目录解析软链后仍在目标目录内：恶意包可以先放一个指向外部的软链，再借它把文件写出去。
			if !androidRealPathWithin(realTargetDir, filepath.Dir(outPath)) {
				emit("⚠ 跳过经软链指向目标目录外的文件: " + hdr.Name)
				continue
			}
			if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
				emit("❌ 创建父目录失败: " + err.Error())
				return
			}
			// 同名位置已有软链时先删掉它再写（与 tar 的默认行为一致），不顺着软链写到它指向的文件里。
			if info, err := os.Lstat(outPath); err == nil && info.Mode()&os.ModeSymlink != 0 {
				_ = os.Remove(outPath)
			}
			f, err := os.OpenFile(outPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(hdr.Mode)&0o777)
			if err != nil {
				emit("❌ 写入文件失败: " + err.Error())
				return
			}
			if _, err := io.Copy(f, tr); err != nil {
				f.Close()
				emit("❌ 复制内容失败: " + err.Error())
				return
			}
			f.Close()
		case tar.TypeSymlink:
			// 软链只允许指向目标目录内部：官方包里的包内相对链接（如 bin/npm -> ../lib/node_modules/npm/bin/npm-cli.js）照常创建，
			// 绝对路径或解析后跑到目标目录外的一律跳过，否则后续条目能顺着它写到系统任意位置。
			linkDir := filepath.Dir(outPath)
			if !androidRealPathWithin(realTargetDir, linkDir) {
				emit("⚠ 跳过经软链指向目标目录外的软链: " + hdr.Name)
				continue
			}
			if err := os.MkdirAll(linkDir, 0o755); err != nil {
				emit("❌ 创建父目录失败: " + err.Error())
				return
			}
			realLinkDir, err := filepath.EvalSymlinks(linkDir)
			if err != nil || !androidSymlinkTargetWithin(realTargetDir, realLinkDir, hdr.Linkname) {
				emit("⚠ 跳过指向目标目录外的软链: " + hdr.Name + " -> " + hdr.Linkname)
				continue
			}
			_ = os.Remove(outPath)
			if err := os.Symlink(hdr.Linkname, outPath); err != nil {
				emit("⚠ 创建软链失败(" + outPath + "): " + err.Error())
			}
		}

		extractedFiles++
		if time.Since(lastReport) > 500*time.Millisecond {
			emit(fmt.Sprintf("解压中... 已处理 %d 个条目", extractedFiles))
			lastReport = time.Now()
		}
	}

	emit(fmt.Sprintf("✅ 解压完成，共 %d 个条目", extractedFiles))

	// 尝试验证
	probe := probeRuntime(req.Name)
	if probe.Installed {
		emit("✅ 检测到可执行: " + probe.Path)
		if probe.Version != "" {
			emit("版本: " + probe.Version)
		}
	} else {
		emit("⚠ 解压成功但未检测到可执行文件，可能架构不兼容，请查看 " + targetDir)
	}

	emit("完成。请在「任务」页面选择 Python/Node 运行时测试脚本执行。")
}

// androidPathWithin 判断 p 是否等于 root 或位于 root 之下（纯字符串比较，调用方负责先解析软链）。
func androidPathWithin(root, p string) bool {
	rel, err := filepath.Rel(root, p)
	if err != nil || filepath.IsAbs(rel) {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}

// androidSymlinkTargetWithin 判断软链目标是否落在 realRoot 内。realLinkDir 是软链所在目录解析软链后的真实路径。
//
// 规则：
//   - 绝对路径一律不行（解压出来的运行时可能被挪位置，包内链接本来就该是相对的）；
//   - 「..」只允许出现在开头。操作系统解析软链时是逐段走的，遇到中途的 a/.. 会先进 a（a 可能本身是软链）再回上一级，
//     而 filepath.Join 会把 a/.. 直接消掉，两者可能对不上；只许开头的「..」时，从已解析的真实目录往上走与字符串计算一致；
//   - 按上面算出的落点必须在 realRoot 内。
func androidSymlinkTargetWithin(realRoot, realLinkDir, linkname string) bool {
	if strings.TrimSpace(linkname) == "" {
		return false
	}
	if strings.HasPrefix(linkname, "/") || strings.HasPrefix(linkname, `\`) || filepath.IsAbs(linkname) || filepath.VolumeName(linkname) != "" {
		return false
	}
	seenName := false
	for _, segment := range strings.Split(strings.ReplaceAll(linkname, `\`, "/"), "/") {
		switch segment {
		case "", ".":
			continue
		case "..":
			if seenName {
				return false
			}
		default:
			seenName = true
		}
	}
	return androidPathWithin(realRoot, filepath.Join(realLinkDir, filepath.FromSlash(linkname)))
}

// androidRealPathWithin 找到 p 最深的已存在祖先（含 p 自己），解析其中的软链后判断是否仍在 realRoot 内。
// 尚不存在的那几级会被 MkdirAll 建成普通目录，不会是软链，所以只看已存在的部分就够了。
func androidRealPathWithin(realRoot, p string) bool {
	current := p
	for {
		if _, err := os.Lstat(current); err == nil {
			resolved, err := filepath.EvalSymlinks(current)
			if err != nil {
				// 悬空软链之类解析不了的，当作不安全处理。
				return false
			}
			return androidPathWithin(realRoot, resolved)
		}
		parent := filepath.Dir(current)
		if parent == current {
			return false
		}
		current = parent
	}
}

type androidUninstallRequest struct {
	Name string `json:"name" binding:"required"`
}

func (h *AndroidRuntimeHandler) Uninstall(c *gin.Context) {
	androidBinDir := resolveAndroidRuntimeBinDir()
	if !androidSupported() {
		response.Error(c, http.StatusForbidden, "仅 Android 面具版支持该操作")
		return
	}
	var req androidUninstallRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, http.StatusBadRequest, "参数错误: "+err.Error())
		return
	}
	if req.Name != "python" && req.Name != "node" {
		response.Error(c, http.StatusBadRequest, "name 只能是 python 或 node")
		return
	}
	target := filepath.Join(androidBinDir, req.Name)
	if err := os.RemoveAll(target); err != nil {
		response.Error(c, http.StatusInternalServerError, "删除失败: "+err.Error())
		return
	}
	response.Success(c, gin.H{"removed": target})
}

// 互斥锁，避免同时触发多次下载
var androidInstallMu sync.Mutex

func (h *AndroidRuntimeHandler) InstallGuarded(c *gin.Context) {
	if !androidInstallMu.TryLock() {
		response.Error(c, http.StatusConflict, "已有安装任务在进行中，请等待完成")
		return
	}
	defer androidInstallMu.Unlock()
	h.Install(c)
}

func (h *AndroidRuntimeHandler) RegisterRoutes(r *gin.RouterGroup) {
	grp := r.Group("/android-runtime", middleware.JWTAuth(), middleware.RequireAdmin())
	grp.GET("/status", h.Status)
	grp.POST("/install", h.InstallGuarded)
	grp.POST("/uninstall", h.Uninstall)
}
