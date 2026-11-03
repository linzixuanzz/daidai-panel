package service

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"daidai-panel/model"
)

var (
	autoInstallNodeModuleRe = regexp.MustCompile(`(?:Cannot find module|Error \[ERR_MODULE_NOT_FOUND\].*)\s*'([^']+)'`)
	autoInstallNodeHintRe   = regexp.MustCompile(`npm\s+install\s+([a-zA-Z@][a-zA-Z0-9_./@-]*)`)
	autoInstallPyModuleRe   = regexp.MustCompile(`(?:ModuleNotFoundError|ImportError):\s*No module named\s+'([^']+)'`)
	autoInstallPyHintRe     = regexp.MustCompile(`pip3?\s+install\s+["']?([a-zA-Z][a-zA-Z0-9_.@-]*(?:\[[a-zA-Z0-9_.,\s-]*\])?)`)
	autoInstallGoModuleRe   = regexp.MustCompile(`(?:no required module provides package|missing go\.sum entry for module providing package)\s+([^\s:;]+)`)

	thirdPartyExcludedModules = map[string]bool{
		"sendNotify":           true,
		"notify":               true,
		"CryptoJS":             true,
		"ql":                   true,
		"qlApi":                true,
		"jdCookie":             true,
		"JD_DmFruitShareCodes": true,
	}
)

type AutoInstallCandidate struct {
	Manager       string
	RequestedName string
	PackageName   string
	DisplayName   string
	WorkDir       string
	RecordType    string
	RecordName    string
	PythonVersion string
}

type AutoInstallResult struct {
	Success bool
	Log     string
	Error   string
}

func DetectAutoInstallCandidate(ext, output, workDir string) *AutoInstallCandidate {
	ext = strings.ToLower(strings.TrimSpace(ext))

	switch ext {
	case ".py":
		if matches := autoInstallPyModuleRe.FindStringSubmatch(output); len(matches) > 1 {
			requested := strings.Split(matches[1], ".")[0]
			if isPythonStdlib(requested) || thirdPartyExcludedModules[requested] {
				return nil
			}
			if isLocalPythonModule(requested, workDir) {
				return nil
			}
			packageName := ResolvePythonAutoInstallPackage(requested)
			return &AutoInstallCandidate{
				Manager:       "python",
				RequestedName: requested,
				PackageName:   packageName,
				DisplayName:   formatAutoInstallDisplayName(requested, packageName),
				WorkDir:       workDir,
				RecordType:    model.DepTypePython,
				RecordName:    packageName,
			}
		}
		// autoInstallPyHintRe 连 extras 一起捕获（issue #166）：httpx 缺 h2 时的提示是
		// `pip install httpx[http2]`，以前字符集不含 `[`，被截成 httpx，装完 h2 仍然缺。
		// 包名前允许一个引号，兼容 `pip install "httpx[http2]"` / `pip install 'httpx[http2]'` 这种写法。
		// 捕获到的 httpx[http2] 原样交给 pip（exec 直接传参、不经 shell），登记时也按这个写法改名，见
		// RecordAutoInstalledDepForPythonVersion。
		if matches := autoInstallPyHintRe.FindStringSubmatch(output); len(matches) > 1 {
			requested := strings.TrimSpace(matches[1])
			if requested == "" || isPythonStdlib(requested) || thirdPartyExcludedModules[requested] {
				return nil
			}
			return &AutoInstallCandidate{
				Manager:       "python",
				RequestedName: requested,
				PackageName:   requested,
				DisplayName:   requested,
				WorkDir:       workDir,
				RecordType:    model.DepTypePython,
				RecordName:    requested,
			}
		}
	case ".js", ".mjs", ".ts":
		if matches := autoInstallNodeModuleRe.FindStringSubmatch(output); len(matches) > 1 {
			requested := strings.TrimSpace(matches[1])
			if requested == "" || strings.HasPrefix(requested, ".") || strings.HasPrefix(requested, "/") || thirdPartyExcludedModules[requested] {
				return nil
			}
			return &AutoInstallCandidate{
				Manager:       "nodejs",
				RequestedName: requested,
				PackageName:   requested,
				DisplayName:   requested,
				WorkDir:       workDir,
				RecordType:    model.DepTypeNodeJS,
				RecordName:    requested,
			}
		}
		if matches := autoInstallNodeHintRe.FindStringSubmatch(output); len(matches) > 1 {
			requested := strings.TrimSpace(matches[1])
			if requested == "" || thirdPartyExcludedModules[requested] {
				return nil
			}
			return &AutoInstallCandidate{
				Manager:       "nodejs",
				RequestedName: requested,
				PackageName:   requested,
				DisplayName:   requested,
				WorkDir:       workDir,
				RecordType:    model.DepTypeNodeJS,
				RecordName:    requested,
			}
		}
	case ".go":
		moduleRoot := findNearestAncestorWithFile(workDir, "go.mod")
		if moduleRoot == "" {
			return nil
		}
		if matches := autoInstallGoModuleRe.FindStringSubmatch(output); len(matches) > 1 {
			moduleName := strings.TrimSpace(matches[1])
			if moduleName == "" {
				return nil
			}
			return &AutoInstallCandidate{
				Manager:       "go",
				RequestedName: moduleName,
				PackageName:   moduleName,
				DisplayName:   moduleName,
				WorkDir:       moduleRoot,
			}
		}
	}

	return nil
}

func InstallAutoDependency(candidate *AutoInstallCandidate, envVars map[string]string) AutoInstallResult {
	if candidate == nil {
		return AutoInstallResult{Error: "未找到可自动安装的依赖"}
	}
	// 包名取自脚本的报错输出，脚本可以伪造；以 - 开头的一律不装（go get 不经过下面的命令构造函数，这里统一先挡）。
	if err := rejectOptionLikePackageName(candidate.PackageName); err != nil {
		return AutoInstallResult{Error: err.Error()}
	}

	baseEnv := buildEnvSlice(envVars)
	switch candidate.Manager {
	case "python":
		pipEnv := PipInstallEnv(baseEnv, CurrentPipMirror())
		pythonVersion := ResolvePythonVersionFromEnv(envVars)
		candidate.PythonVersion = pythonVersion
		cmd, err := NewPipInstallCommandForPythonVersion(pythonVersion, candidate.PackageName)
		if err != nil {
			return AutoInstallResult{Error: err.Error()}
		}
		cmd.Env = pipEnv
		out, err := cmd.CombinedOutput()

		// 二次兜底：即使 ResolvePipInstallCommand 用的是 venv pip，某些极端场景下
		// （venv 被外部破坏、用户改了 sys.prefix 等）仍可能撞到 PEP 668。检测到就加
		// --break-system-packages --user 重跑一次。
		if err != nil && IsExternallyManagedError(out) {
			retryFlags := []string{"--break-system-packages", "--user"}
			retry, buildErr := NewPipInstallCommandForPythonVersionWithFlags(pythonVersion, candidate.PackageName, dedupFlags(retryFlags))
			if buildErr != nil {
				return AutoInstallResult{Log: string(out), Error: buildErr.Error()}
			}
			retry.Env = pipEnv
			retryOut, retryErr := retry.CombinedOutput()
			combined := append([]byte{}, out...)
			combined = append(combined, []byte("\n[PEP 668 检测到 externally-managed-environment，自动加 --break-system-packages --user 重试]\n")...)
			combined = append(combined, retryOut...)
			return completeAutoInstall(candidate, combined, retryErr)
		}

		return completeAutoInstall(candidate, out, err)
	case "nodejs":
		unlock := LockNodePackageOperation()
		defer unlock()

		notice := NodeInstallCompatibilityNotice(candidate.PackageName)
		cmd, err := NewNpmInstallCommand(candidate.PackageName)
		if err != nil {
			return AutoInstallResult{Error: err.Error()}
		}
		cmd.Env = NpmInstallEnv(baseEnv, CurrentNpmMirror())
		out, err := cmd.CombinedOutput()
		if notice != "" {
			// 自动安装成功后也会把安装日志写入依赖记录，先把兼容映射决策一起存进去。
			out = append([]byte(notice+"\n"), out...)
		}
		return completeAutoInstall(candidate, out, err)
	case "go":
		cmd := exec.Command("go", "get", candidate.PackageName)
		cmd.Dir = candidate.WorkDir
		cmd.Env = baseEnv
		out, err := cmd.CombinedOutput()
		return completeAutoInstall(candidate, out, err)
	default:
		return AutoInstallResult{Error: fmt.Sprintf("不支持的自动安装类型: %s", candidate.Manager)}
	}
}

// IsExternallyManagedError 判断 pip 输出是否命中 PEP 668
// （Alpine/Debian 等系统从 Python 3.11+ 起默认拒绝在系统 site-packages 上执行 pip install）。
func IsExternallyManagedError(out []byte) bool {
	text := strings.ToLower(string(out))
	return strings.Contains(text, "externally-managed-environment") ||
		strings.Contains(text, "this environment is externally managed")
}

// ResolvePipInstallCommand 选默认 Python 版本的 pip 二进制 + 自动决定 PEP 668 参数。
// 优先用托管 venv 里的 pip，其次使用匹配目标版本的 python -m pip 或 pipX.Y。
// 目标版本不可用时不回落到未带版本的 pip3，避免把依赖装进错误解释器。
func ResolvePipInstallCommand() (binary string, extraFlags []string, usingSystemPip bool) {
	return ResolvePipInstallCommandForPythonVersion("")
}

type pipCommandSpec struct {
	binary         string
	prefixArgs     []string
	extraFlags     []string
	usingSystemPip bool
}

func (spec pipCommandSpec) command(args []string) *exec.Cmd {
	fullArgs := append([]string{}, spec.prefixArgs...)
	fullArgs = append(fullArgs, args...)
	return exec.Command(spec.binary, fullArgs...)
}

func resolvePipCommandSpecForPythonVersion(pythonVersion string, includeSystemInstallFlags bool) (pipCommandSpec, error) {
	pythonVersion = NormalizePythonVersionOrDefault(pythonVersion)
	if !PythonVersionSupportedByCurrentRuntime(pythonVersion) {
		return pipCommandSpec{}, fmt.Errorf("当前镜像不支持 Python %s，请切换到对应 Python 版本镜像或 all 镜像", pythonVersion)
	}
	if binary := ResolveManagedPipBinaryForPythonVersion(pythonVersion); strings.TrimSpace(binary) != "" {
		return pipCommandSpec{binary: binary}, nil
	}

	systemFlags := []string(nil)
	if includeSystemInstallFlags {
		systemFlags = []string{"--break-system-packages", "--user"}
	}
	for _, candidate := range managedPythonBootstrapCommandsForVersion(pythonVersion) {
		if !managedBootstrapCommandMatchesVersion(candidate, pythonVersion) {
			continue
		}
		prefix := append([]string{}, candidate.versionArgsPrefix...)
		prefix = append(prefix, "-m", "pip")
		return pipCommandSpec{
			binary:         candidate.binary,
			prefixArgs:     prefix,
			extraFlags:     systemFlags,
			usingSystemPip: true,
		}, nil
	}

	runtimePip := "pip" + pythonVersion
	if pipBin, err := exec.LookPath(runtimePip); err == nil && pipBinaryMatchesVersion(pipBin, pythonVersion) {
		return pipCommandSpec{binary: runtimePip, extraFlags: systemFlags, usingSystemPip: true}, nil
	}
	return pipCommandSpec{}, fmt.Errorf("Python %s 不可用，请先安装 python%s 或切换依赖 Python 版本", pythonVersion, pythonVersion)
}

func pipBinaryMatchesVersion(binary, pythonVersion string) bool {
	cmd := exec.Command(binary, "--version")
	cmd.Env = appendPythonBootstrapEnv(SanitizePipEnv(os.Environ()))
	out, err := cmd.CombinedOutput()
	if err != nil {
		return false
	}
	text := strings.ToLower(strings.TrimSpace(string(out)))
	return strings.Contains(text, "python "+NormalizePythonVersionOrDefault(pythonVersion))
}

func ResolvePipInstallCommandForPythonVersion(pythonVersion string) (binary string, extraFlags []string, usingSystemPip bool) {
	spec, err := resolvePipCommandSpecForPythonVersion(pythonVersion, true)
	if err != nil {
		return "", nil, false
	}
	return spec.binary, append([]string{}, spec.extraFlags...), spec.usingSystemPip
}

func NewPipCommandForPythonVersion(pythonVersion string, args []string) (*exec.Cmd, error) {
	spec, err := resolvePipCommandSpecForPythonVersion(pythonVersion, false)
	if err != nil {
		return nil, err
	}
	return spec.command(args), nil
}

// rejectOptionLikePackageName：包名以 - 开头时会被 pip / npm / go / apk / apt 当成选项
// （如 --index-url、--registry 把安装源换成别人的）。自动安装的包名来自脚本运行时的报错输出，
// 脚本自己就能打印出这种「包名」，所以在构造安装命令的地方统一挡住，手动安装、重装、恢复后重装也都经过这里。
func rejectOptionLikePackageName(packageName string) error {
	if strings.HasPrefix(strings.TrimSpace(packageName), "-") {
		return fmt.Errorf("依赖名不能以 - 开头：%s", packageName)
	}
	return nil
}

func NewPipInstallCommandForPythonVersion(pythonVersion, packageName string) (*exec.Cmd, error) {
	if err := rejectOptionLikePackageName(packageName); err != nil {
		return nil, err
	}
	spec, err := resolvePipCommandSpecForPythonVersion(pythonVersion, true)
	if err != nil {
		return nil, err
	}
	return spec.command(BuildPipInstallArgs(spec.extraFlags, packageName)), nil
}

func NewPipInstallCommandForPythonVersionWithFlags(pythonVersion, packageName string, extraFlags []string) (*exec.Cmd, error) {
	if err := rejectOptionLikePackageName(packageName); err != nil {
		return nil, err
	}
	spec, err := resolvePipCommandSpecForPythonVersion(pythonVersion, true)
	if err != nil {
		return nil, err
	}
	return spec.command(BuildPipInstallArgs(extraFlags, packageName)), nil
}

func NewPipUninstallCommandForPythonVersion(pythonVersion, packageName string) (*exec.Cmd, error) {
	spec, err := resolvePipCommandSpecForPythonVersion(pythonVersion, true)
	if err != nil {
		return nil, err
	}
	return spec.command(BuildPipUninstallArgs(spec.extraFlags, packageName)), nil
}

// BuildPipInstallArgs 把 install 子命令、附加 flag、包名拼成完整的 args。
func BuildPipInstallArgs(extraFlags []string, packageName string) []string {
	args := []string{"install"}
	args = append(args, extraFlags...)
	args = append(args, packageName)
	return args
}

// BuildPipUninstallArgs 类似 BuildPipInstallArgs，但用于卸载场景。
// 注意：--user 在 uninstall 时无意义，--break-system-packages 仍需要传以绕过 PEP 668。
// 刻意不留「额外选项」参数：以前强制卸载经它塞进了 --no-deps，而 pip uninstall 根本没有这个选项，
// 整条命令在参数解析阶段就以退出码 2 退出、一个包都不卸（#150）。卸载要的选项只有上面这些。
func BuildPipUninstallArgs(extraFlags []string, packageName string) []string {
	args := []string{"uninstall", "-y"}
	for _, flag := range extraFlags {
		if flag == "--user" {
			continue
		}
		args = append(args, flag)
	}
	args = append(args, packageName)
	return args
}

func dedupFlags(flags []string) []string {
	seen := map[string]struct{}{}
	result := make([]string, 0, len(flags))
	for _, flag := range flags {
		if _, exists := seen[flag]; exists {
			continue
		}
		seen[flag] = struct{}{}
		result = append(result, flag)
	}
	return result
}

func completeAutoInstall(candidate *AutoInstallCandidate, out []byte, err error) AutoInstallResult {
	logText := string(out)
	if err != nil {
		return AutoInstallResult{
			Success: false,
			Log:     logText,
			Error:   strings.TrimSpace(logText),
		}
	}

	if candidate.RecordType != "" && candidate.RecordName != "" {
		RecordAutoInstalledDepForPythonVersion(candidate.RecordType, candidate.RecordName, logText, candidate.PythonVersion)
	}

	return AutoInstallResult{
		Success: true,
		Log:     logText,
	}
}

func formatAutoInstallDisplayName(requested, packageName string) string {
	requested = strings.TrimSpace(requested)
	packageName = strings.TrimSpace(packageName)
	if requested == "" {
		return packageName
	}
	if packageName == "" || strings.EqualFold(requested, packageName) {
		return requested
	}
	return requested + " -> " + packageName
}

var pythonStdlibModules = map[string]bool{
	"__future__": true, "_thread": true, "_winapi": true, "abc": true, "aifc": true,
	"argparse": true, "array": true, "ast": true, "asynchat": true, "asyncio": true,
	"asyncore": true, "atexit": true, "audioop": true, "base64": true, "bdb": true,
	"binascii": true, "binhex": true, "bisect": true, "builtins": true, "bz2": true,
	"calendar": true, "cgi": true, "cgitb": true, "chunk": true, "cmath": true,
	"cmd": true, "code": true, "codecs": true, "codeop": true, "collections": true,
	"colorsys": true, "compileall": true, "concurrent": true, "configparser": true,
	"contextlib": true, "contextvars": true, "copy": true, "copyreg": true, "cProfile": true,
	"crypt": true, "csv": true, "ctypes": true, "curses": true, "dataclasses": true,
	"datetime": true, "dbm": true, "decimal": true, "difflib": true, "dis": true,
	"distutils": true, "doctest": true, "email": true, "encodings": true,
	"enum": true, "errno": true, "faulthandler": true, "fcntl": true, "filecmp": true,
	"fileinput": true, "fnmatch": true, "fractions": true, "ftplib": true,
	"functools": true, "gc": true, "getopt": true, "getpass": true, "gettext": true,
	"glob": true, "graphlib": true, "grp": true, "gzip": true, "hashlib": true,
	"heapq": true, "hmac": true, "html": true, "http": true, "idlelib": true,
	"imaplib": true, "imghdr": true, "imp": true, "importlib": true, "inspect": true,
	"io": true, "ipaddress": true, "itertools": true, "json": true, "keyword": true,
	"lib2to3": true, "linecache": true, "locale": true, "logging": true, "lzma": true,
	"mailbox": true, "mailcap": true, "marshal": true, "math": true, "mimetypes": true,
	"mmap": true, "modulefinder": true, "msvcrt": true, "multiprocessing": true,
	"netrc": true, "nis": true, "nntplib": true, "nt": true, "numbers": true,
	"operator": true, "optparse": true, "os": true, "ossaudiodev": true,
	"pathlib": true, "pdb": true, "pickle": true, "pickletools": true, "pipes": true,
	"pkgutil": true, "platform": true, "plistlib": true, "poplib": true, "posix": true,
	"posixpath": true, "pprint": true, "profile": true, "pstats": true, "pty": true,
	"pwd": true, "py_compile": true, "pyclbr": true, "pydoc": true, "queue": true,
	"quopri": true, "random": true, "re": true, "readline": true, "reprlib": true,
	"resource": true, "rlcompleter": true, "runpy": true, "sched": true, "secrets": true,
	"select": true, "selectors": true, "shelve": true, "shlex": true, "shutil": true,
	"signal": true, "site": true, "smtpd": true, "smtplib": true, "sndhdr": true,
	"socket": true, "socketserver": true, "spwd": true, "sqlite3": true, "sre_compile": true,
	"sre_constants": true, "sre_parse": true, "ssl": true, "stat": true, "statistics": true,
	"string": true, "stringprep": true, "struct": true, "subprocess": true, "sunau": true,
	"symtable": true, "sys": true, "sysconfig": true, "syslog": true, "tabnanny": true,
	"tarfile": true, "telnetlib": true, "tempfile": true, "termios": true, "test": true,
	"textwrap": true, "threading": true, "time": true, "timeit": true, "tkinter": true,
	"token": true, "tokenize": true, "tomllib": true, "trace": true, "traceback": true,
	"tracemalloc": true, "tty": true, "turtle": true, "turtledemo": true, "types": true,
	"typing": true, "unicodedata": true, "unittest": true, "urllib": true, "uu": true,
	"uuid": true, "venv": true, "warnings": true, "wave": true, "weakref": true,
	"webbrowser": true, "winreg": true, "winsound": true, "wsgiref": true,
	"xdrlib": true, "xml": true, "xmlrpc": true, "zipapp": true, "zipfile": true,
	"zipimport": true, "zlib": true, "zoneinfo": true,
	"backports": true, "pkg_resources": true, "setuptools": true, "pip": true,
	"_io": true, "_signal": true, "_abc": true, "_codecs": true, "_collections": true,
	"_functools": true, "_operator": true, "_sre": true, "_stat": true, "_string": true,
	"_weakref": true,
}

func isLocalPythonModule(name, workDir string) bool {
	if workDir == "" || name == "" {
		return false
	}
	for _, suffix := range []string{".py", ".so", ".pyd", ".pyc"} {
		if _, err := os.Stat(filepath.Join(workDir, name+suffix)); err == nil {
			return true
		}
	}
	if info, err := os.Stat(filepath.Join(workDir, name)); err == nil && info.IsDir() {
		return true
	}
	matches, _ := filepath.Glob(filepath.Join(workDir, name+".*.so"))
	if len(matches) > 0 {
		return true
	}
	matches, _ = filepath.Glob(filepath.Join(workDir, name+".*.pyd"))
	if len(matches) > 0 {
		return true
	}
	return false
}

func isPythonStdlib(name string) bool {
	name = strings.TrimSpace(name)
	if name == "" {
		return false
	}
	if strings.HasPrefix(name, "_") {
		return true
	}
	return pythonStdlibModules[name]
}

func findNearestAncestorWithFile(startDir, targetFile string) string {
	current := strings.TrimSpace(startDir)
	if current == "" {
		return ""
	}

	for {
		candidate := filepath.Join(current, targetFile)
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return current
		}

		parent := filepath.Dir(current)
		if parent == current {
			return ""
		}
		current = parent
	}
}
