package handler

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"daidai-panel/config"
	"daidai-panel/service"
)

// ============================================================================
// Magisk 模块版的面板在线升级
//
// 模块版的文件分布在三个互不相同的位置，这是理解本文件的前提：
//
//	1. 模块本体（宿主 Android 侧）：/data/adb/modules/daidai-panel/
//	   里面有 system/bin/daidai-server、system/bin/ddp、web/、module.prop。
//	2. 容器 rootfs：/data/daidai 或 /data/local/daidai。
//	3. 面板真正运行的文件（容器内）：/usr/local/bin/daidai-server、/app/web。
//
// Magisk/service.sh 每次开机都会把 (1) 拷贝进 (3)。所以在线升级必须【两处都写】：
// 只写 (3) 会在下次开机被旧版覆盖；只写 (1) 则本次不生效。
//
// 容器是 chroot 模式（ruri -p -N -S -A），但宿主的 /data/adb 在容器内可见可写
// —— 面板的 Android 运行时安装功能就是直接往 /data/adb/daidai-panel/bin 落
// Python/Node 的（见 handler/android_runtime.go）。写模块目录仍按 best-effort 处理：
// KernelSU 下 /data 可能以只读挂载，写不进去时不能中断升级，靠 service.sh 的
// 「模块内文件更新才覆盖」兜底。
//
// 升级范围严格限定为三样：daidai-server、ddp、web/。
// 容器 rootfs、apt 依赖、Python venv、config.yaml、ports.conf 一概不动。
// 在线升级覆盖不到模块外壳（service.sh / customize.sh / action.sh / rootfs 结构），
// 所以外壳带来的新能力只能靠重刷 zip 拿到 —— 见下面两个版本常量的分工。
// ============================================================================

const (
	// currentMagiskShellVersion 是【本仓库 Magisk/service.sh 当前 export 的】外壳版本号。
	// 每改一次 Magisk/*.sh 或 rootfs 结构就加一；magisk_assets_test.go 静态断言两者一致。
	// 它只表示「仓库里的外壳长什么样」，不参与任何放行判断。
	currentMagiskShellVersion = 5

	// requiredMagiskShellVersion 是【在线升级放行的最低外壳版本】。
	//
	// 只有当新面板【无法】在旧外壳上运行时才提这个数字 —— 一旦提了，所有还在跑旧外壳的
	// 用户都必须先手动重刷一次模块 zip 才能继续在面板内一键升级。
	// 反过来，外壳只是多了一项增量能力（例如 v2 的手动停止开关）时必须保持不动：
	// 新面板在旧外壳上照常运行，只是那项能力不可用，由前端按外壳版本 gating 并提示重刷。
	//
	// 所以这里【不是】必须等于 currentMagiskShellVersion，只要求 <= 它。
	requiredMagiskShellVersion = 1
	magiskShellVersionEnv      = "DAIDAI_MAGISK_SHELL_VERSION"

	// 容器内的固定运行路径。名字和路径都不能变：
	// Magisk/service.sh 与 action.sh 都靠 `/usr/local/bin/daidai-server` 这个 argv0
	// 判断面板是否在跑，改名会让开机时重复拉起第二个实例抢同一个端口。
	magiskPanelBinaryPath = "/usr/local/bin/daidai-server"
	magiskPanelCLIPath    = "/usr/local/bin/ddp"

	// 升级窗口哨兵。service.sh 的存活守护看到它就不插手，
	// 避免在「旧进程已退出、新进程还没起来」的空档里抢着拉起旧版本。
	magiskUpdatingSentinelName = ".updating"

	// magiskPersistDir 是模块的宿主侧持久目录，service.sh / customize.sh /
	// action.sh / uninstall.sh 四个脚本用的都是它。
	magiskPersistDir = "/data/adb/daidai-panel"

	// magiskStopFlagName 是跨重启的「手动停止」开关文件名。
	//
	// 存在即表示用户显式停了面板：service.sh 在同步完模块文件后直接早退，
	// 存活守护读到它也会自退，所以重启手机同样不会把面板拉起来。
	//
	// 放在 magiskPersistDir 下而不是容器数据目录里，是因为：
	//   1. 它不在 rootfs 内，刷模块 zip 时的 `rm -rf "$rootfs"` 碰不到；
	//   2. 容器内可读写（面板进程自己要写它）；
	//   3. 容器数据目录里的 .updating 每次开机被无条件删除，
	//      跨重启的状态放在同一个目录迟早被同类清理误伤。
	//
	// 这三个常量必须与 Magisk/*.sh 里的字面量逐字一致，magisk_assets_test.go 有静态断言。
	magiskStopFlagName    = "stopped"
	magiskStopFlagPath    = magiskPersistDir + "/" + magiskStopFlagName
	magiskWatchdogGenName = "watchdog.gen"

	// magiskStopSupportedShellVersion 是「手动停止面板」这项能力所需的最低外壳版本。
	//
	// 停止开关和守护自退都是 v2 外壳（v3.0.4）才有的东西：在 v1 外壳上写下开关根本
	// 没有任何代码会读它，面板会被存活守护在 60 秒内原样拉回来。
	// 所以必须在接口层就拦下并提示重刷 zip，而不是让用户以为自己停成功了。
	magiskStopSupportedShellVersion = 2
)

// errMagiskRuntimeNotDetected 是内部哨兵，表示「当前不是模块版」，
// 调用方据此继续往下走 Docker / 二进制链路，而不是把它当成升级失败。
var errMagiskRuntimeNotDetected = errors.New("当前不是 Magisk 模块版运行环境")

// magiskModuleDirCandidates 与 Magisk/service.sh 的模块目录探测顺序保持一致。
var magiskModuleDirCandidates = []string{
	"/data/adb/modules/daidai-panel",
	"/data/adb/magisk/modules/daidai-panel",
	"/sbin/.magisk/modules/daidai-panel",
}

// isMagiskPanelUpdateRuntime 判断当前进程是否跑在 Magisk 模块的容器里。
//
// 判据取合取而不是单看环境变量：误判成模块版会让普通 Linux 部署往
// /usr/local/bin 写文件。DAIDAI_MAGISK_MODULE 只由 service.sh 生成的容器
// 启动脚本 export（ddp 从面板终端起时同样继承得到）。
//
// 这里只校验「可执行文件在 /usr/local/bin 下」，不校验具体文件名：
// ddp CLI 装在 /usr/local/bin/ddp，写死 daidai-server 会让 `ddp check` / `ddp update`
// 在模块版上永远判定成「不是模块版」，CLI 里的 magisk 分支变成死代码。
func isMagiskPanelUpdateRuntime() bool {
	if runtime.GOOS != "linux" {
		return false
	}
	if !service.IsMagiskModuleRuntime() {
		return false
	}
	executablePath, err := os.Executable()
	if err != nil {
		return false
	}
	// Linux 上 os.Executable() 读的是 /proc/self/exe。二进制被替换（在线升级、手动 mv）之后，
	// 这个链接会变成 "/usr/local/bin/daidai-server (deleted)"。旧进程在退出前若还接到一次
	// 检查更新请求，就会因为路径对不上而被判成"不是模块版"，再退回去报 Docker 的错。
	executablePath = strings.TrimSuffix(strings.TrimSpace(executablePath), " (deleted)")
	executableDir := filepath.ToSlash(filepath.Dir(filepath.Clean(executablePath)))
	return executableDir == filepath.ToSlash(filepath.Dir(magiskPanelBinaryPath))
}

// findMagiskPanelServerPID 在 /proc 里找出正在运行的面板进程。
//
// 面板自己发起升级时这就是当前进程；`ddp update` 发起时当前进程是 CLI，
// 必须找到真正的面板 PID，否则 helper 会在面板还活着的时候替换二进制并再起一个实例。
// 容器走 chroot（ruri 不传 -u），没有 PID namespace，容器内外看到的是同一份 /proc。
func findMagiskPanelServerPID() int {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return 0
	}
	self := os.Getpid()
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid <= 0 {
			continue
		}
		raw, err := os.ReadFile(filepath.Join("/proc", entry.Name(), "cmdline"))
		if err != nil || len(raw) == 0 {
			continue
		}
		// cmdline 是 NUL 分隔的，第一段就是可执行文件路径。
		argv0 := string(raw)
		if idx := strings.IndexByte(argv0, 0); idx >= 0 {
			argv0 = argv0[:idx]
		}
		if strings.TrimSpace(argv0) != magiskPanelBinaryPath {
			continue
		}
		// 面板自己发起升级时优先返回自己，避免同名进程干扰。
		if pid == self {
			return pid
		}
		return pid
	}
	return 0
}

// resolveMagiskShellVersion 读取 service.sh 注入的外壳版本号。
// 读不到按 0 处理：v3.0.3 之前的 service.sh 根本没有 export 这个变量。
func resolveMagiskShellVersion() int {
	raw := strings.TrimSpace(os.Getenv(magiskShellVersionEnv))
	if raw == "" {
		return 0
	}
	version, err := strconv.Atoi(raw)
	if err != nil || version < 0 {
		return 0
	}
	return version
}

// magiskStopFlagPathForTest 只为测试可替换，生产恒等于 magiskStopFlagPath。
// 真机上 /data/adb 是 root 专属路径，测试里不可能也不应该去写它。
var magiskStopFlagPathForTest = magiskStopFlagPath

// writeMagiskStopFlag 写下跨重启的停止开关。
//
// 只有「停止面板服务」这一条路径可以调用它。
// 特别注意不要在 /system/restart 里复用：restart 只是退出进程、靠存活守护拉回来，
// 一旦顺手写了这个开关，一次正常重启就会变成永久停机，
// 而此时 Web 已经没了，用户在面板上再也没有自救手段。
func writeMagiskStopFlag() error {
	flagPath := magiskStopFlagPathForTest
	if err := os.MkdirAll(filepath.Dir(flagPath), 0o755); err != nil {
		return err
	}
	content := fmt.Sprintf("stopped by panel at %s\n", time.Now().Format("2006-01-02 15:04:05"))
	return os.WriteFile(flagPath, []byte(content), 0o644)
}

// resolveMagiskModuleDir 找到模块本体目录，找不到返回空串（不是错误）。
func resolveMagiskModuleDir() string {
	for _, dir := range magiskModuleDirCandidates {
		if info, err := os.Stat(filepath.Join(dir, "module.prop")); err == nil && !info.IsDir() {
			return dir
		}
	}
	return ""
}

func buildMagiskPanelUpdatePlan(release *panelReleaseInfo) (*panelUpdatePlan, error) {
	if !isMagiskPanelUpdateRuntime() {
		return nil, errMagiskRuntimeNotDetected
	}

	if shellVersion := resolveMagiskShellVersion(); shellVersion < requiredMagiskShellVersion {
		return nil, fmt.Errorf(
			"当前 Magisk 模块外壳版本过旧（检测到 %d，需要 %d），在线升级只更新面板程序与前端，覆盖不到模块脚本；请到 GitHub Releases 下载对应 flavor 的模块 zip 重新刷入一次，之后即可在面板内一键升级",
			shellVersion, requiredMagiskShellVersion,
		)
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
		return nil, fmt.Errorf("当前 Release 未提供适配本机平台的更新包 %s", assetName)
	}

	dataDir := strings.TrimSpace(resolveBinaryUpdateBaseDir())
	if dataDir == "" || dataDir == "/" {
		return nil, fmt.Errorf("无法识别面板数据目录，模块版在线升级已终止")
	}

	webDir := resolveMagiskWebDir()
	if webDir == "" || webDir == "/" {
		return nil, fmt.Errorf("无法识别前端静态目录，请确认 config.yaml 中的 server.web_dir")
	}

	return &panelUpdatePlan{
		DeploymentType: panelUpdateDeploymentMagisk,
		UpdateManager:  panelUpdateManagerPanel,
		ReleaseVersion: strings.TrimSpace(release.version()),
		AssetName:      asset.Name,
		AssetURL:       asset.BrowserDownloadURL,
		AssetDigest:    asset.Digest,
		BinaryName:     binaryName,
		ExecutablePath: magiskPanelBinaryPath,
		InstallDir:     filepath.Dir(magiskPanelBinaryPath),
		DataDir:        dataDir,
		WebDir:         webDir,
		ModuleDir:      resolveMagiskModuleDir(),
		CurrentPID:     os.Getpid(),
		// 面板自己发起时 ServerPID == CurrentPID；`ddp update` 发起时是真正的面板进程。
		ServerPID: findMagiskPanelServerPID(),
	}, nil
}

// resolveMagiskWebDir 与 main.go 的 setupStaticFrontend 保持同一口径：
// 优先 config 里显式配置的 web_dir，其次是可执行文件同级的 web 目录。
func resolveMagiskWebDir() string {
	if config.C != nil {
		if dir := strings.TrimSpace(config.C.Server.WebDir); dir != "" {
			return filepath.Clean(dir)
		}
	}
	return "/app/web"
}

func executeMagiskPanelUpdateWithOptions(plan *panelUpdatePlan, options panelUpdateExecutionOptions) {
	panelUpdater.setRunning("preparing", "正在准备模块版在线升级目录")

	workDir, err := createBinaryUpdateWorkDir(plan)
	if err != nil {
		failPanelBinaryUpdate(options, err)
		return
	}

	archivePath := filepath.Join(workDir, sanitizeUpdateFileName(plan.AssetName))
	panelUpdater.setRunning("downloading", fmt.Sprintf("正在下载更新包 %s", plan.AssetName))
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

	if _, err := os.Stat(filepath.Join(packageRoot, "web")); err != nil {
		failPanelBinaryUpdate(options, fmt.Errorf("更新包中缺少前端目录 web，模块版在线升级已终止"))
		return
	}

	// 模块目录里的文件都没在执行，直接在当前进程里写完即可，不必等到 helper 脚本。
	// 写失败只告警不中断：容器内 /data 可能是只读挂载，此时靠 service.sh 的
	// 「模块内文件更新才覆盖」逻辑保证本次升级在重启后不被回滚。
	panelUpdater.setRunning("syncing", "正在同步模块目录，确保重启后不回滚到旧版本")
	moduleSyncWarning := syncMagiskModuleDir(plan, packageRoot)

	panelUpdater.setRunning("scheduling", "更新包已准备完成，正在启动后台替换脚本")
	scriptPath, err := writeMagiskUpdateHelperScript(plan, packageRoot, workDir)
	if err != nil {
		failPanelBinaryUpdate(options, err)
		return
	}

	// 哨兵必须在 helper 启动之前写：helper 一旦开跑，旧进程随时会退出。
	writeMagiskUpdatingSentinel(plan.DataDir)

	if err := startBinaryUpdateHelper(scriptPath); err != nil {
		clearMagiskUpdatingSentinel(plan.DataDir)
		failPanelBinaryUpdate(options, err)
		return
	}

	message := "后台更新脚本已启动，将替换面板程序与前端并重启，容器与已装依赖不受影响"
	if moduleSyncWarning != "" {
		message += "。" + moduleSyncWarning
	}
	panelUpdater.setRestarting(message)

	// 只有「面板自己发起升级」时才自杀让位。`ddp update` 发起时当前进程是 CLI，
	// 退出面板的活由 helper 脚本里的 kill -TERM 完成，这里直接退会把 CLI 的输出截断。
	if plan.ServerPID == 0 || plan.ServerPID == plan.CurrentPID {
		go func() {
			time.Sleep(1500 * time.Millisecond)
			// 走主程序的完整关停（终止并结算任务、关库）再以 0 退出，不再直接 os.Exit。
			panelProcessExit(0)
		}()
	}
}

// syncMagiskModuleDir 把新版文件写回模块本体目录，返回非空字符串表示有需要提示用户的告警。
func syncMagiskModuleDir(plan *panelUpdatePlan, packageRoot string) string {
	moduleDir := strings.TrimSpace(plan.ModuleDir)
	if moduleDir == "" {
		return "未找到 Magisk 模块目录，本次升级只在容器内生效；若重启后回退到旧版本，请重新刷入模块 zip"
	}

	binDir := filepath.Join(moduleDir, "system", "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return fmt.Sprintf("模块目录不可写（%v），本次升级只在容器内生效", err)
	}

	if err := copyFilePreservingMode(filepath.Join(packageRoot, plan.BinaryName), filepath.Join(binDir, "daidai-server"), 0o755); err != nil {
		return fmt.Sprintf("写入模块目录的面板程序失败（%v），本次升级只在容器内生效", err)
	}

	// ddp 是可选组件，旧模块里不一定有，缺了不算失败。
	if _, err := os.Stat(filepath.Join(packageRoot, "ddp")); err == nil {
		_ = copyFilePreservingMode(filepath.Join(packageRoot, "ddp"), filepath.Join(binDir, "ddp"), 0o755)
	}

	if err := replaceDirAtomically(filepath.Join(packageRoot, "web"), filepath.Join(moduleDir, "web")); err != nil {
		return fmt.Sprintf("写入模块目录的前端资源失败（%v），本次升级只在容器内生效", err)
	}

	// module.prop 的版本号要同步，否则 Magisk 管理器卡片会一直显示旧版本，
	// 而且下次真正需要刷模块时用户无从判断自己在哪一版。
	if err := rewriteMagiskModuleProp(filepath.Join(moduleDir, "module.prop"), plan.ReleaseVersion); err != nil {
		return fmt.Sprintf("更新 module.prop 版本号失败（%v），面板已升级但管理器仍显示旧版本号", err)
	}

	return ""
}

var (
	magiskModulePropVersionPattern     = regexp.MustCompile(`(?m)^version=.*$`)
	magiskModulePropVersionCodePattern = regexp.MustCompile(`(?m)^versionCode=.*$`)
)

// rewriteMagiskModuleProp 只改 version / versionCode 两行，其余行原样保留。
// 不整体重写是因为 module.prop 里还有 id/author/description/updateJson，
// 其中 updateJson 在 debian flavor 里与 alpine 不同，重写会把它抹平。
func rewriteMagiskModuleProp(path, releaseVersion string) error {
	version := normalizeMagiskModuleVersion(releaseVersion)
	if version == "" {
		return nil
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	text := string(raw)
	if !magiskModulePropVersionPattern.MatchString(text) {
		return fmt.Errorf("module.prop 中未找到 version 行")
	}
	text = magiskModulePropVersionPattern.ReplaceAllLiteralString(text, "version="+version)

	if code, ok := magiskModuleVersionCode(version); ok && magiskModulePropVersionCodePattern.MatchString(text) {
		text = magiskModulePropVersionCodePattern.ReplaceAllLiteralString(text, "versionCode="+strconv.Itoa(code))
	}

	return os.WriteFile(path, []byte(text), 0o644)
}

// normalizeMagiskModuleVersion 统一成 module.prop 里使用的 vX.Y.Z 形式。
func normalizeMagiskModuleVersion(releaseVersion string) string {
	version := strings.TrimSpace(releaseVersion)
	if version == "" {
		return ""
	}
	if !strings.HasPrefix(version, "v") {
		version = "v" + version
	}
	return version
}

// magiskModuleVersionCode 与 scripts/release-preflight.ps1 的算法保持一致：
// major*10000 + minor*100 + patch。
func magiskModuleVersionCode(version string) (int, bool) {
	parts := strings.Split(strings.TrimPrefix(strings.TrimSpace(version), "v"), ".")
	if len(parts) != 3 {
		return 0, false
	}
	numbers := make([]int, 0, 3)
	for _, part := range parts {
		value, err := strconv.Atoi(part)
		if err != nil || value < 0 {
			return 0, false
		}
		numbers = append(numbers, value)
	}
	return numbers[0]*10000 + numbers[1]*100 + numbers[2], true
}

func copyFilePreservingMode(source, target string, mode os.FileMode) error {
	data, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	// 先写临时文件再 rename：目标文件可能正在被执行，直接覆盖会得到 ETXTBSY。
	tempPath := target + ".new"
	if err := os.WriteFile(tempPath, data, mode); err != nil {
		return err
	}
	if err := os.Chmod(tempPath, mode); err != nil {
		os.Remove(tempPath)
		return err
	}
	if err := os.Rename(tempPath, target); err != nil {
		os.Remove(tempPath)
		return err
	}
	return nil
}

// replaceDirAtomically 用「写新目录 -> 换名 -> 删旧目录」替换整个目录，
// 避免直接 cp 叠加导致 Vite 的 hash 文件名无限堆积。
func replaceDirAtomically(source, target string) error {
	stagingDir := target + ".new"
	backupDir := target + ".old"

	os.RemoveAll(stagingDir)
	if err := copyDirRecursive(source, stagingDir); err != nil {
		os.RemoveAll(stagingDir)
		return err
	}

	os.RemoveAll(backupDir)
	if _, err := os.Stat(target); err == nil {
		if err := os.Rename(target, backupDir); err != nil {
			os.RemoveAll(stagingDir)
			return err
		}
	}

	if err := os.Rename(stagingDir, target); err != nil {
		// 换名失败要把旧目录放回去，不能留下一个没有前端的面板。
		os.Rename(backupDir, target)
		os.RemoveAll(stagingDir)
		return err
	}

	os.RemoveAll(backupDir)
	return nil
}

func copyDirRecursive(source, target string) error {
	entries, err := os.ReadDir(source)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(target, 0o755); err != nil {
		return err
	}

	for _, entry := range entries {
		sourcePath := filepath.Join(source, entry.Name())
		targetPath := filepath.Join(target, entry.Name())

		if entry.IsDir() {
			if err := copyDirRecursive(sourcePath, targetPath); err != nil {
				return err
			}
			continue
		}
		// 符号链接不跟随：更新包里不应该有，出现了也不该带进模块目录。
		if entry.Type()&os.ModeSymlink != 0 {
			continue
		}

		info, err := entry.Info()
		if err != nil {
			return err
		}
		data, err := os.ReadFile(sourcePath)
		if err != nil {
			return err
		}
		if err := os.WriteFile(targetPath, data, info.Mode().Perm()); err != nil {
			return err
		}
	}
	return nil
}

func magiskUpdatingSentinelPath(dataDir string) string {
	dataDir = strings.TrimSpace(dataDir)
	if dataDir == "" {
		return ""
	}
	return filepath.Join(dataDir, magiskUpdatingSentinelName)
}

func writeMagiskUpdatingSentinel(dataDir string) {
	if path := magiskUpdatingSentinelPath(dataDir); path != "" {
		os.WriteFile(path, []byte(strconv.Itoa(os.Getpid())), 0o644)
	}
}

func clearMagiskUpdatingSentinel(dataDir string) {
	if path := magiskUpdatingSentinelPath(dataDir); path != "" {
		os.Remove(path)
	}
}

// writeMagiskUpdateHelperScript 生成容器内执行的替换脚本。
//
// 三条必须遵守的约束，改这段前先读：
//  1. 二进制必须用 rename 覆盖，不能 cp 直写 —— 目标文件正在被执行，会 ETXTBSY。
//  2. 新进程必须仍是 /usr/local/bin/daidai-server，否则 service.sh 的 pgrep 去重失效。
//  3. 启动前必须 cd 到数据目录，否则 appboot.ResolveConfigPath 找不到 config.yaml，
//     main.go 会直接 log.Fatalf，而模块版没有守护进程会把它拉起来。
func writeMagiskUpdateHelperScript(plan *panelUpdatePlan, sourceRoot, workDir string) (string, error) {
	scriptPath := filepath.Join(workDir, "apply-magisk-update.sh")
	logPath := filepath.Join(workDir, "magisk-update.log")

	body := fmt.Sprintf(`#!/bin/sh
set -u
PID=%d
SERVER_PID=%d
SRC=%s
BINARY=%s
TARGET_BIN=%s
TARGET_CLI=%s
WEB_DIR=%s
DATA_DIR=%s
SENTINEL=%s
LOG=%s

log() {
  printf '%%s %%s\n' "$(date '+%%Y-%%m-%%d %%H:%%M:%%S')" "$1" >> "$LOG" 2>/dev/null
}

fail() {
  log "$1"
  rm -f "$SENTINEL" 2>/dev/null
  exit 1
}

# ddp update 发起时，调用方（PID）是 CLI 而不是面板本身，面板不会自己退出，
# 必须显式请它退出，否则会在面板还活着的时候替换二进制并再起一个实例。
if [ "$SERVER_PID" -gt 0 ] && [ "$SERVER_PID" != "$PID" ]; then
  log "请求面板进程 $SERVER_PID 退出"
  kill -TERM "$SERVER_PID" 2>/dev/null || true
fi

log "等待调用进程 $PID 退出"
i=0
while kill -0 "$PID" 2>/dev/null; do
  i=$((i + 1))
  if [ "$i" -gt 60 ]; then
    kill -KILL "$PID" 2>/dev/null || true
    break
  fi
  sleep 1
done

if [ "$SERVER_PID" -gt 0 ] && [ "$SERVER_PID" != "$PID" ]; then
  i=0
  while kill -0 "$SERVER_PID" 2>/dev/null && [ "$i" -lt 30 ]; do
    i=$((i + 1))
    sleep 1
  done
  if kill -0 "$SERVER_PID" 2>/dev/null; then
    log "面板进程 $SERVER_PID 未在 30 秒内退出，强制结束"
    kill -KILL "$SERVER_PID" 2>/dev/null || true
    sleep 1
  fi
fi

# 运行中的可执行文件不能直接覆盖（ETXTBSY），必须先写临时文件再 rename。
log "替换面板程序"
cp -f "$SRC/$BINARY" "$TARGET_BIN.new" || fail "复制面板程序失败"
chmod 755 "$TARGET_BIN.new" 2>/dev/null
mv -f "$TARGET_BIN.new" "$TARGET_BIN" || fail "替换面板程序失败"

if [ -f "$SRC/ddp" ]; then
  cp -f "$SRC/ddp" "$TARGET_CLI.new" && chmod 755 "$TARGET_CLI.new" 2>/dev/null && mv -f "$TARGET_CLI.new" "$TARGET_CLI"
fi

# 前端整目录原子切换：直接往旧目录里 cp 会让带 hash 的产物无限堆积。
log "替换前端资源"
rm -rf "$WEB_DIR.new"
mkdir -p "$WEB_DIR.new" || fail "创建前端临时目录失败"
cp -R "$SRC/web/." "$WEB_DIR.new/" || fail "复制前端资源失败"
rm -rf "$WEB_DIR.old"
if [ -d "$WEB_DIR" ]; then
  mv "$WEB_DIR" "$WEB_DIR.old" || fail "备份旧前端目录失败"
fi
if ! mv "$WEB_DIR.new" "$WEB_DIR"; then
  mv "$WEB_DIR.old" "$WEB_DIR" 2>/dev/null
  fail "切换前端目录失败，已回滚"
fi
rm -rf "$WEB_DIR.old"

rm -f "$SENTINEL" 2>/dev/null

# 必须 cd 到数据目录再启动：config.yaml 在这里，换个工作目录会直接启动失败。
log "启动新面板进程"
cd "$DATA_DIR" || fail "进入数据目录失败"
nohup "$TARGET_BIN" >> "$DATA_DIR/daidai.log" 2>&1 &
log "新面板进程已拉起 PID=$!"
`,
		plan.CurrentPID,
		plan.ServerPID,
		shellQuote(sourceRoot),
		shellQuote(plan.BinaryName),
		shellQuote(magiskPanelBinaryPath),
		shellQuote(magiskPanelCLIPath),
		shellQuote(plan.WebDir),
		shellQuote(plan.DataDir),
		shellQuote(magiskUpdatingSentinelPath(plan.DataDir)),
		shellQuote(logPath),
	)

	if err := os.WriteFile(scriptPath, []byte(body), 0o755); err != nil {
		return "", fmt.Errorf("写入更新脚本失败: %w", err)
	}
	return scriptPath, nil
}
