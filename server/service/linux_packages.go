package service

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

type LinuxPackageManager struct {
	Name   string
	Binary string
}

const AptPackageListTTL = 6 * time.Hour

// aptLockTimeoutOption 让 apt-get 在 dpkg 锁被占用时最多等 300 秒，而不是立刻报错退出。
//
// ⚠️ 它只覆盖 dpkg 那把锁（install / remove 用的）。安装脚本里排在前面的 apt-get update 拿的是
// /var/lib/apt/lists/lock，这把锁不看这个选项、占用时立刻失败；而脚本用 ; 串联，update 失败后
// install 照跑，读着还是空的索引报 Unable to locate package。所以面板自己发起的 apt 操作之间
// 靠进程内的 LockLinuxPackageOperation 串行（网页安装 / 卸载与容器重建后的自动重装共用），
// 不指望这个选项。它留着是为了挡面板管不到的 apt（比如用户在系统终端里手动装包）。
// 这个选项从 apt 1.9.11 起支持（Debian 11+ / Ubuntu 20.04+）；更老的 apt 会忽略未知的 -o 配置项，不会报错。
const aptLockTimeoutOption = "DPkg::Lock::Timeout=300"

var linuxPackageOperationMu sync.Mutex

// LockLinuxPackageOperation 串行化面板发起的所有 Linux 系统包操作，返回解锁函数（写法同 LockNodePackageOperation）。
//
// 网页端安装 / 卸载 / 强制卸载，与容器重建后启动校验的后台重装（reinstallDependency）必须共用这一把：
// 重建后 apt 索引是空的，两边都会先跑 apt-get update，而 update 的 lists 锁不等待（见 aptLockTimeoutOption），
// 撞上就是一条 failed 记录。调用方要在构造命令之前就拿锁、一直持有到命令结束：
// BuildLinuxPackageCommand 构造时就会写镜像源、再判断索引要不要刷新（顺序见该函数里的顺序契约），
// 这两步同样不能与另一条 apt 交错。
// 不可重入：拿着它的代码路径里不能再调用会拿它的函数。
func LockLinuxPackageOperation() func() {
	linuxPackageOperationMu.Lock()
	return linuxPackageOperationMu.Unlock
}

var DetectLinuxPackageManagerLookPathFunc = exec.LookPath

func DetectLinuxPackageManager() (LinuxPackageManager, error) {
	return DetectLinuxPackageManagerWithLookPath(DetectLinuxPackageManagerLookPathFunc)
}

func DetectLinuxPackageManagerWithLookPath(lookPath func(string) (string, error)) (LinuxPackageManager, error) {
	candidates := []LinuxPackageManager{
		{Name: "apk", Binary: "apk"},
		{Name: "apt", Binary: "apt-get"},
		{Name: "dnf", Binary: "dnf"},
		{Name: "yum", Binary: "yum"},
		{Name: "microdnf", Binary: "microdnf"},
		{Name: "zypper", Binary: "zypper"},
	}

	for _, candidate := range candidates {
		if _, err := lookPath(candidate.Binary); err == nil {
			return candidate, nil
		}
	}

	return LinuxPackageManager{}, errors.New("未检测到可用的 Linux 包管理器（支持 apk/apt/dnf/yum/microdnf/zypper）")
}

// aptPackageListsDir 是 apt 索引落盘的位置，刷新判定与作废都指着它。
const aptPackageListsDir = "/var/lib/apt/lists"

func ShouldRefreshAptPackageLists() bool {
	return ShouldRefreshAptPackageListsFromDir(aptPackageListsDir, time.Now(), AptPackageListTTL)
}

// InvalidateAptPackageIndex 删掉 apt 索引文件，让下一次安装必然先跑一遍 apt-get update（issue #146）。
//
// 用在「用户在面板里手动换了 apt 源」之后：索引文件名是按仓库 URL 编码的，换完源还留着旧源那批索引，
// ShouldRefreshAptPackageLists 只看 mtime 不看来源，6 小时内会判定「索引还新」而跳过 update，
// 于是 apt 拿着旧源的索引找包，报 E: Unable to locate package（issue #146 里 libxdamage1 的成因）。
//
// 选「删文件」而不是在内存里记个脏标记，是因为它跨进程有效：二进制部署的面板重启后标记就丢了，
// 而索引已经被删掉这件事仍然成立。
//
// 跳过 lock / *.lock 与子目录，判据与 ShouldRefreshAptPackageListsFromDir 保持一致 ——
// 两边认的「索引文件」必须是同一组，否则会出现「删完了它还说索引是新的」。
func InvalidateAptPackageIndex() error {
	return invalidateAptPackageIndexInDir(aptPackageListsDir)
}

func invalidateAptPackageIndexInDir(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		// 目录不存在等价于「索引本来就是空的」，ShouldRefreshAptPackageListsFromDir 会直接判要刷新。
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	var firstErr error
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if name == "lock" || strings.HasSuffix(name, ".lock") {
			continue
		}
		if err := os.Remove(filepath.Join(dir, name)); err != nil && !os.IsNotExist(err) && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func ShouldRefreshAptPackageListsFromDir(dir string, now time.Time, ttl time.Duration) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return true
	}

	var newest time.Time
	hasIndexFile := false
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if name == "lock" || strings.HasSuffix(name, ".lock") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		hasIndexFile = true
		if info.ModTime().After(newest) {
			newest = info.ModTime()
		}
	}

	if !hasIndexFile {
		return true
	}
	return now.Sub(newest) > ttl
}

func LinuxInstallCommandSpec(manager LinuxPackageManager, packageName string, refreshApt bool) (string, []string, error) {
	switch manager.Name {
	case "apk":
		return manager.Binary, []string{"add", "--no-cache", packageName}, nil
	case "apt":
		script := "export DEBIAN_FRONTEND=noninteractive; "
		if refreshApt {
			// -o 写在 update 后面是刻意的：apt-get 允许选项与子命令交错，这样脚本里仍保留
			// "apt-get update" 这个连续子串，日志和既有断言都不用跟着变。
			// Acquire::Retries 让索引下载失败时自动重试 3 次（issue #146）——
			// 面具部署靠 /etc/apt/apt.conf.d/99-daidai-android 已经有重试，Docker / 二进制部署没有。
			script += "echo '[APT] 软件包索引过期，正在刷新...'; apt-get update -o Acquire::Retries=3; "
		}
		script += "echo '[APT] 正在安装软件包...'; apt-get -o " + aptLockTimeoutOption +
			" install -y --no-install-recommends " + shellQuoteLinuxPackage(packageName)
		return "sh", []string{"-lc", script}, nil
	case "dnf", "yum", "microdnf":
		return manager.Binary, []string{"install", "-y", packageName}, nil
	case "zypper":
		return manager.Binary, []string{"--non-interactive", "install", packageName}, nil
	default:
		return "", nil, errors.New("不支持的 Linux 包管理器")
	}
}

func LinuxRemoveCommandSpec(manager LinuxPackageManager, packageName string, force bool) (string, []string, error) {
	switch manager.Name {
	case "apk":
		args := []string{"del"}
		if force {
			args = append(args, "--force-broken-world")
		}
		args = append(args, packageName)
		return manager.Binary, args, nil
	case "apt":
		args := []string{"-o", aptLockTimeoutOption, "remove", "-y"}
		if force {
			args = append(args, "--allow-remove-essential", "--purge")
		}
		args = append(args, packageName)
		return manager.Binary, args, nil
	case "dnf", "yum", "microdnf":
		return manager.Binary, []string{"remove", "-y", packageName}, nil
	case "zypper":
		return manager.Binary, []string{"--non-interactive", "remove", packageName}, nil
	default:
		return "", nil, errors.New("不支持的 Linux 包管理器")
	}
}

// EnsureLinuxPackageManagerPrivilege 在非 root 时提前拦下并说清楚原因。
//
// 背景（v3.0.7）：容器配了 PUID/PGID 之后面板以普通用户跑，而 apt-get / apk 这类
// 系统包管理器必须 root 才能写 /usr、/var/lib/dpkg 以及包管理器自己的锁。
// 原来这条路会一路跑到包管理器自己报一串英文 Permission denied / 锁失败，
// 用户很容易当成面板的 bug。Node.js / Python 依赖不受影响 —— 它们装在数据目录里，
// 那个目录 entrypoint 已经 chown 给运行用户了。
func EnsureLinuxPackageManagerPrivilege() error {
	if runtime.GOOS == "windows" {
		return nil
	}
	if os.Geteuid() == 0 {
		return nil
	}
	// 出路要按部署形态给，不能一律写 Docker：仓库自己也推荐二进制部署用
	// packaging/linux/daidai-panel.service 的 User=daidai 非 root 跑，
	// 给那类用户一段 docker exec 的指引等于没给。
	hint := "去掉降权配置改回 root 运行，或以 root 身份手动安装该软件包"
	if _, err := os.Stat("/.dockerenv"); err == nil {
		hint = "在宿主机执行 docker exec -u 0 <容器名> apk add <包名>" +
			"（Debian 版镜像用 apt-get install -y <包名>），" +
			"或去掉 compose 里的 PUID/PGID 让容器回到 root 运行"
	} else if strings.TrimSpace(os.Getenv("DAIDAI_MAGISK_MODULE")) != "" {
		hint = "进容器后以 root 手动安装：参考模块 README 的「进入容器」命令"
	} else {
		hint = "以 root 手动安装该软件包，或去掉 systemd 单元里的 User= / Group= 让面板回到 root 运行"
	}

	return fmt.Errorf("当前面板以非 root 用户（uid=%d）运行，无法安装或卸载 Linux 系统依赖 —— "+
		"apt-get / apk 需要 root 权限才能写 /usr 与包管理器的锁，这是降权运行的固有限制。"+
		"可选做法：%s。"+
		"注意 Node.js / Python 依赖不受此限制，降权下仍可在面板里正常安装", os.Geteuid(), hint)
}

func BuildLinuxPackageCommand(manager LinuxPackageManager, action, packageName string, force bool, distribution string, ensureMirror func(LinuxPackageManager, string) (bool, error)) (*exec.Cmd, error) {
	// 装和卸都要写系统目录，两条路都得先过这道闸。
	if err := EnsureLinuxPackageManagerPrivilege(); err != nil {
		return nil, err
	}

	switch action {
	case "install":
		// 以 - 开头的包名会被 apt-get / apk 当成选项（恢复备份后的重装也走这里），见 rejectOptionLikePackageName。
		if err := rejectOptionLikePackageName(packageName); err != nil {
			return nil, err
		}
		// 顺序契约：refreshApt 必须在 ensureMirror 之后求值（issue #146）。
		// v3.3.2 之前是先算 refreshApt 再换源，于是「换源」这个动作永远影响不到本次要不要 update ——
		// 索引还在 6 小时 TTL 内时，换完源直接拿旧源的索引装包，报 E: Unable to locate package。
		// 挪到后面并或上换源结果，才能做到「源一变就必刷索引」。
		mirrorChanged := false
		if ensureMirror != nil {
			changed, mirrorErr := ensureMirror(manager, distribution)
			if mirrorErr != nil {
				return nil, mirrorErr
			}
			mirrorChanged = changed
		}
		refreshApt := manager.Name == "apt" && (mirrorChanged || ShouldRefreshAptPackageLists())
		bin, args, err := LinuxInstallCommandSpec(manager, packageName, refreshApt)
		if err != nil {
			return nil, err
		}
		cmd := exec.Command(bin, args...)
		cmd.Env = AppendProxyEnv(append(os.Environ(), "TMPDIR=/tmp"))
		return cmd, nil
	case "remove":
		bin, args, err := LinuxRemoveCommandSpec(manager, packageName, force)
		if err != nil {
			return nil, err
		}
		cmd := exec.Command(bin, args...)
		cmd.Env = AppendProxyEnv(append(os.Environ(), "TMPDIR=/tmp", "DEBIAN_FRONTEND=noninteractive"))
		return cmd, nil
	default:
		return nil, errors.New("不支持的 Linux 依赖操作")
	}
}

func DetectLinuxDistribution() string {
	data, err := os.ReadFile("/etc/os-release")
	if err != nil {
		return ""
	}

	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "ID=") {
			continue
		}
		value := strings.TrimSpace(strings.TrimPrefix(line, "ID="))
		value = strings.Trim(value, `"'`)
		return strings.ToLower(value)
	}

	return ""
}

func shellQuoteLinuxPackage(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}
