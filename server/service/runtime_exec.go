package service

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"daidai-panel/config"
	"daidai-panel/database"
	"daidai-panel/model"
)

type managedRuntimePaths struct {
	NodeBin          string
	NodeModules      string
	VenvBin          string
	VenvSitePackages string
	SanitizedPath    string

	searchDirs []string
}

var managedPythonVenvMu sync.Mutex

var warmManagedPythonVenvForVersionFunc = func(version string) {
	ensureManagedPythonVenvForVersion(version, false)
}

var cleanupBrokenManagedPythonVenvsFunc = cleanupBrokenManagedPythonVenvs

var windowsShellSearchDirs = []string{
	filepath.Join(os.Getenv("ProgramFiles"), "Git", "bin"),
	filepath.Join(os.Getenv("ProgramFiles"), "Git", "usr", "bin"),
	filepath.Join(os.Getenv("ProgramFiles(x86)"), "Git", "bin"),
	filepath.Join(os.Getenv("ProgramFiles(x86)"), "Git", "usr", "bin"),
}

var windowsPythonPreferredDirs = []string{
	filepath.Join(os.Getenv("LocalAppData"), "Programs", "Python", "Python314"),
	filepath.Join(os.Getenv("LocalAppData"), "Programs", "Python", "Python313"),
	filepath.Join(os.Getenv("LocalAppData"), "Programs", "Python", "Python312"),
	filepath.Join(os.Getenv("LocalAppData"), "Programs", "Python", "Python311"),
	filepath.Join(os.Getenv("LocalAppData"), "Programs", "Python", "Python310"),
}

// pythonEnvBootstrap 只负责四件事：
//  1. Windows 下先按启动环境初始化 Python 本地时区缓存
//  2. 从 env.json 注入任务环境变量到 os.environ
//  3. 把 PYTHONPATH 里声明的目录前置到 sys.path（工作目录、脚本目录、venv site-packages）
//  4. 以 runpy.run_path 的方式执行用户脚本
//
// 历史上这里还有"AST 扫 import + importlib.find_spec 判缺失 + 自动 pip install"
// 的预检链路（v2.0.7 引入），但 find_spec 在 Alpine venv 下对 pysmx 等包会漏判，
// 导致已装好的包反复被判定缺失、循环触发 pip install。真实缺失的包由
// Go 侧 task_executor.detectAndInstallDeps 兜底——它在脚本真实抛出
// ModuleNotFoundError 时介入，基于正则抓模块名后 pip install，并自动重跑脚本，
// 比预检更精准，且最多重试 5 次覆盖多依赖场景。
const pythonEnvBootstrap = `import json, os, runpy, sys, time
env_file, script_path, extra_path_raw = sys.argv[1:4]
script_args = sys.argv[4:]
if os.name == "nt":
    time.localtime()
with open(env_file, "r", encoding="utf-8") as fh:
    payload = json.load(fh)
for key, value in payload.items():
    if value is None:
        continue
    os.environ[str(key)] = str(value)
for entry in reversed([item for item in extra_path_raw.split(os.pathsep) if item]):
    if entry not in sys.path:
        sys.path.insert(0, entry)
sys.argv = [script_path] + script_args
runpy.run_path(script_path, run_name="__main__")
`

const pythonModuleEnvBootstrap = `import json, os, runpy, sys, time
env_file, module_name, extra_path_raw = sys.argv[1:4]
module_args = sys.argv[4:]
if os.name == "nt":
    time.localtime()
with open(env_file, "r", encoding="utf-8") as fh:
    payload = json.load(fh)
for key, value in payload.items():
    if value is None:
        continue
    os.environ[str(key)] = str(value)
for entry in reversed([item for item in extra_path_raw.split(os.pathsep) if item]):
    if entry not in sys.path:
        sys.path.insert(0, entry)
sys.argv = [module_name] + module_args
runpy.run_module(module_name, run_name="__main__", alter_sys=True)
`

// shellEnvBootstrap 里那段 __dd_dump_env 只为「任务前置钩子回传环境变量」服务，
// 且**只有 DAIDAI_HOOK_ENV_DUMP 非空时才装**，其余 bash 任务、订阅钩子、后置钩子
// 走的仍然是原来的两行逻辑（见 hookEnvDumpPathEnvKey 的注释）。
//
// 为什么必须用 trap EXIT、不能把 dump 代码追加在用户脚本尾部：
// 这里对用户脚本是 `. "$__dd_script"`（source，不是 exec），用户写一句 `exit 0`
// 会直接终止整个 bootstrap shell，任何追加在其后的代码永远不会执行 —— 而
// `exit 0` / `[ -z "$X" ] && exit 1` 恰恰是前置脚本里最常见的收尾写法。
// trap EXIT 装在 source 之前，正常结束、exit、绝大多数异常退出都能兜住
// （被 SIGKILL 强杀除外，那时 dump 文件不存在，Go 侧跳过合并）。
//
// dump 走 NUL 分隔的 "KEY=VALUE\0"，是为了原样承载含换行、含 '=' 的值。
// 三级降级链：env -0（不依赖 bash 特性，Alpine 容器里未必有真 bash 的 compgen）
// → bash 内建 compgen -e + ${!k} → 逐行 env（多行值会丢，纯兜底）。
// 三者都失败就留一个空文件，Go 侧跳过合并并写一行日志，不影响任务继续执行。
//
// 采集两次（.base 与最终）而不是只采一次：bootstrap 进程自身的环境里本来就有
// PATH / HOME / LANG / HTTP_PROXY 等一批不属于 envVars 的键，只采一次的话它们会被
// 当成「前置脚本新增的变量」合并进任务环境（代理地址还会被冻结成快照）。
// 以 source 用户脚本之前的快照为基线做差集，得到的才是用户脚本真正改动的那部分。
//
// 开关除了「变量非空」还要求同名 .ok 标记文件存在（由 newHookEnvCapture 落盘）。
// 这一道是防呆：用户完全可以在「环境变量」页手建一条同名变量，值随手填成
// /app/config.yaml，那样每个 bash 任务都会用 `>` 把它截断。多一个只有面板才会创建的
// 标记文件，误设的值就只是个空转。
const shellEnvBootstrap = `__dd_env_file=$1
__dd_script=$2
shift 2
export DAIDAI_RUNTIME_SHELL_ENV_FILE="$__dd_env_file"
if [ -f "$__dd_env_file" ]; then
  . "$__dd_env_file"
fi
if [ -n "$DAIDAI_HOOK_ENV_DUMP" ] && [ -f "${DAIDAI_HOOK_ENV_DUMP}.ok" ]; then
  __dd_dump_env() {
    if env -0 >"$1" 2>/dev/null; then
      return 0
    fi
    if { for __dd_key in $(compgen -e 2>/dev/null); do
           printf '%s=%s\0' "$__dd_key" "${!__dd_key}"
         done; } >"$1" 2>/dev/null && [ -s "$1" ]; then
      return 0
    fi
    env >"$1" 2>/dev/null || : >"$1" 2>/dev/null || true
    return 0
  }
  __dd_dump_env "${DAIDAI_HOOK_ENV_DUMP}.base"
  trap '__dd_dump_env "$DAIDAI_HOOK_ENV_DUMP"' EXIT
fi
. "$__dd_script" "$@"
`

// bash 任务的 env.sh 里，超过 MAX_ARG_STRLEN 的变量只赋值不 export，
// 否则 bash 自己往下 exec 任何命令都会直接 E2BIG。
// 预算上限再额外压一道，避免一堆中等大小的变量叠加撞上 ARG_MAX。
const shellEnvExportValueMaxBytes = linuxMaxArgStrLenBytes
const shellEnvExportBudgetBytes = 512 * 1024

const goEnvBootstrapSource = `package main

import (
	"encoding/json"
	"os"
)

func init() {
	envFile := os.Getenv("DAIDAI_RUNTIME_ENV_FILE")
	if envFile == "" {
		return
	}
	data, err := os.ReadFile(envFile)
	if err != nil {
		return
	}
	payload := map[string]string{}
	if err := json.Unmarshal(data, &payload); err != nil {
		return
	}
	for key, value := range payload {
		if key == "" {
			continue
		}
		_ = os.Setenv(key, value)
	}
}
`

// BuildManagedRuntimeEnvMap 丢弃脚本凭据的 jti，见
// BuildManagedRuntimeEnvMapForPythonVersion 上的警告。
func BuildManagedRuntimeEnvMap(workDir, scriptsDir string, defaultChannelID *uint, ttl time.Duration) (map[string]string, error) {
	return BuildManagedRuntimeEnvMapForPythonVersion(workDir, scriptsDir, defaultChannelID, ttl, "")
}

// BuildManagedRuntimeEnvMapForPythonVersion 丢弃脚本凭据的 jti。
//
// 警告：丢掉 jti 就等于放弃吊销能力——那枚 operator 凭据会一直有效到 ttl 到期，谁也收不回来。
// 目前**没有任何生产路径**该走这里，三条注入路径（任务执行、ddp python/shell、脚本调试运行）
// 全部改用 BuildManagedRuntimeEnvMapWithScriptToken 并在用完后 RevokeScriptToken。
// 保留这两个包装只是给不关心凭据回收的测试用；新增调用方前请先确认你真的不需要吊销。
func BuildManagedRuntimeEnvMapForPythonVersion(workDir, scriptsDir string, defaultChannelID *uint, ttl time.Duration, pythonVersion string) (map[string]string, error) {
	envMap, _, err := BuildManagedRuntimeEnvMapWithScriptToken(workDir, scriptsDir, defaultChannelID, ttl, pythonVersion, nil)
	return envMap, err
}

// RuntimeEnvScope 是任务级「可用环境变量」的范围（#168）。传 nil = 全部已启用变量（与改动前一致）。
// 非 nil 时只注入「已启用，且变量名在 Names 里、或分组与 Groups 有交集」的变量；两个列表都空 = 一个用户变量都不注入。
type RuntimeEnvScope struct {
	Groups []string
	Names  []string
	// InjectedNames 由 BuildManagedRuntimeEnvMapWithScriptToken 回填：按范围实际注入的用户变量名（按注入顺序、已去重），
	// 只给任务日志开头那一行用，不含值。
	InjectedNames []string
}

// TaskRuntimeEnvScope 取任务的环境变量范围：只有「指定」才返回非 nil，「全部」与老数据都返回 nil。
func TaskRuntimeEnvScope(task *model.Task) *RuntimeEnvScope {
	if task == nil || task.GetEnvScope() != model.TaskEnvScopeSelected {
		return nil
	}
	return &RuntimeEnvScope{Groups: task.GetEnvGroups(), Names: task.GetEnvNames()}
}

// taskEnvScopeLogMaxNames 是任务日志开头那一行最多列出的变量名个数，超出的只写个数。
const taskEnvScopeLogMaxNames = 20

// formatTaskEnvScopeLogLine 生成「指定」范围时写在任务日志开头的那一行（#168），只写变量名、不写值。
func formatTaskEnvScopeLogLine(names []string) string {
	if len(names) == 0 {
		return "[环境变量] 已按任务设置只注入 0 个（没有命中任何已启用的变量）\n"
	}
	if len(names) <= taskEnvScopeLogMaxNames {
		return fmt.Sprintf("[环境变量] 已按任务设置只注入 %d 个：%s\n", len(names), strings.Join(names, "、"))
	}
	return fmt.Sprintf("[环境变量] 已按任务设置只注入 %d 个：%s 等，另有 %d 个未列出\n",
		len(names), strings.Join(names[:taskEnvScopeLogMaxNames], "、"), len(names)-taskEnvScopeLogMaxNames)
}

// BuildManagedRuntimeEnvMapWithScriptToken 额外返回注入凭据的 jti / 到期时间，
// 让调用方能在任务结束时调 RevokeScriptToken 立刻作废它。
//
// envScope 只有任务执行会传（#168）；ddp python/shell、脚本调试运行、run-code 都传 nil，行为不变。
// 指定范围时只过滤用户变量：config.sh、青龙兼容变量、TZ、PATH、NO_PROXY、DAIDAI_* 照常注入；
// 注入的脚本令牌改签只能发通知的那种。
func BuildManagedRuntimeEnvMapWithScriptToken(workDir, scriptsDir string, defaultChannelID *uint, ttl time.Duration, pythonVersion string, envScope *RuntimeEnvScope) (map[string]string, *ScriptTokenInfo, error) {
	var envVarRecords []model.EnvVar
	// 按稳定顺序读取：置顶 > 组内位置 > 创建时间 > id；避免无 ORDER BY 导致同名变量的相对顺序抖动
	database.DB.Where("enabled = ?", true).
		Order("sort_order DESC, position ASC, created_at ASC, id ASC").
		Find(&envVarRecords)

	// 任务指定了范围：只留命中变量名或分组的记录，再走下面原有的按名合并（同名多条照样用 & 拼接）
	if envScope != nil {
		allowedNames := make(map[string]bool, len(envScope.Names))
		for _, name := range envScope.Names {
			allowedNames[name] = true
		}
		allowedGroups := make(map[string]bool, len(envScope.Groups))
		for _, group := range envScope.Groups {
			allowedGroups[group] = true
		}
		filtered := make([]model.EnvVar, 0, len(envVarRecords))
		for _, ev := range envVarRecords {
			matched := allowedNames[ev.Name]
			if !matched {
				// 一条变量可以属于多个分组（逗号拼接），任一分组命中就算
				for _, group := range model.SplitEnvGroups(ev.Group) {
					if allowedGroups[group] {
						matched = true
						break
					}
				}
			}
			if matched {
				filtered = append(filtered, ev)
			}
		}
		envVarRecords = filtered
	}

	// 先按 name 分组保持顺序，再用 joinTaskEnvValues 做带转义合并，
	// 解决值内含 '&' 时脚本按 '&' 切分会错位的问题（与 splitTaskEnvValues 对称）。
	grouped := make(map[string][]string)
	order := make([]string, 0, len(envVarRecords))
	for _, ev := range envVarRecords {
		if _, ok := grouped[ev.Name]; !ok {
			order = append(order, ev.Name)
		}
		grouped[ev.Name] = append(grouped[ev.Name], ev.Value)
	}
	if envScope != nil {
		envScope.InjectedNames = append([]string{}, order...)
	}

	envMap := make(map[string]string, len(grouped))
	for _, name := range order {
		envMap[name] = joinTaskEnvValues(grouped[name])
	}

	loadConfigShellVars(envMap)
	// 青龙兼容变量（#110）：只在 key 还不存在时才写。
	//
	// ⚠️ 这与紧接着下面的 TZ、以及 DAIDAI_* 那批「保留名」语义**完全相反**，不要顺手统一掉：
	// QL_DIR 等不是面板独占的名字，用户完全可能已经在环境变量页（或 config.sh 里）
	// 手建了一个 QL_DIR 指向他自己的青龙路径，脚本也早就照那个值跑通了。
	// 无条件覆盖等于静默改掉现有行为，而且用户在页面上看到自己设的值、实际却不生效，
	// 排查起来毫无线索。这批变量的定位是「用户没配就给个能用的默认值」。
	for key, value := range buildQingLongCompatEnv() {
		if _, exists := envMap[key]; !exists {
			envMap[key] = value
		}
	}
	// PLAYWRIGHT_BROWSERS_PATH（#142）与上面的青龙兼容变量同一语义：用户没配才补默认值。
	// 必须落在 envMap 里：任务、调试运行、run-code、ddp python/shell 的子进程都是白名单环境，
	// 只靠进程环境（entrypoint export / main.go 的 os.Setenv）根本传不进去。
	applyPlaywrightBrowsersPathDefault(envMap)
	// 面板时区是全局运行时配置，优先级高于普通环境变量，避免任务脚本继续继承 UTC。
	envMap["TZ"] = CurrentPanelTimezone()

	// 回环直连白名单（#111）：面板开代理后，Python 的 urllib 会把发往
	// 127.0.0.1:5701 的 notify 回调也交给代理，代理连不上回环就回 HTTP 502。
	//
	// 为什么落在 envMap 而不是进程环境：env.json / env.sh 是在子进程内**最后**落地的，
	// 只写进程环境的话，用户自己设的同名变量会把它整个盖回去。
	//
	// 语义是「在用户已有值上合并追加」而不是覆盖 —— 用户可能已经设了
	// NO_PROXY=corp.internal 之类的内网白名单，覆盖会把它洗掉。
	// 大小写两个键的已有值都要读进来：只读大写、大写为空才退小写的写法，
	// 会静默丢掉用户只设了小写 no_proxy 的那份值。
	// 合并结果最后同时写回大小写两个键，保证不同运行时读哪个都一致。
	noProxyPresent := make(map[string]struct{}, 8)
	noProxyItems := make([]string, 0, 8)
	for _, existing := range []string{envMap["NO_PROXY"], envMap["no_proxy"]} {
		for _, item := range strings.Split(existing, ",") {
			item = strings.TrimSpace(item)
			if item == "" {
				continue
			}
			// 幂等判定统一按小写比较：用户写成 ` 127.0.0.1 ` 或 `LocalHost`
			// 也算已存在，不再重复追加一遍。
			lower := strings.ToLower(item)
			if _, exists := noProxyPresent[lower]; exists {
				continue
			}
			noProxyPresent[lower] = struct{}{}
			noProxyItems = append(noProxyItems, item)
		}
	}
	// 指定了非回环监听地址时也补上面板自身地址（#162）；它可能是带大写的主机名，同样按小写判重
	for _, host := range loopbackNoProxyHostList() {
		lower := strings.ToLower(host)
		if _, exists := noProxyPresent[lower]; exists {
			continue
		}
		noProxyPresent[lower] = struct{}{}
		noProxyItems = append(noProxyItems, host)
	}
	mergedNoProxy := strings.Join(noProxyItems, ",")
	envMap["NO_PROXY"] = mergedNoProxy
	envMap["no_proxy"] = mergedNoProxy

	pythonVersion = NormalizePythonVersionOrDefault(pythonVersion)
	if !PythonVersionSupportedByCurrentRuntime(pythonVersion) {
		pythonVersion = DefaultPythonVersion()
	}
	envMap["DAIDAI_PYTHON_VERSION"] = pythonVersion

	runtimePaths := currentManagedRuntimePathsForPythonVersion(pythonVersion)
	if runtimePaths.NodeModules != "" {
		envMap["NODE_PATH"] = runtimePaths.NodeModules
	}
	if runtimePaths.SanitizedPath != "" {
		envMap["PATH"] = joinPathSegments(runtimePaths.VenvBin, runtimePaths.SanitizedPath, runtimePaths.NodeBin)
	}
	if pythonPath := buildManagedPythonPath(envMap["PYTHONPATH"], workDir, scriptsDir, runtimePaths.VenvSitePackages); pythonPath != "" {
		envMap["PYTHONPATH"] = pythonPath
	}
	AppendScriptHelperPaths(envMap, scriptsDir)
	var helperErr error
	var scriptToken *ScriptTokenInfo
	if helperEnv, tokenInfo, err := BuildNotifyHelperEnv(scriptsDir, workDir, config.C.Server.Port, defaultChannelID, ttl, envScope != nil); err == nil {
		for key, value := range helperEnv {
			envMap[key] = value
		}
		scriptToken = tokenInfo
	} else {
		helperErr = err
	}

	return envMap, scriptToken, helperErr
}

func buildManagedPythonPath(existingPythonPath, workDir, scriptsDir, venvSitePackages string) string {
	return joinPathSegments(workDir, scriptsDir, existingPythonPath, venvSitePackages)
}

func CreateManagedCommand(interpreter, scriptPath string, scriptArgs []string, workDir string, envVars map[string]string) (*exec.Cmd, func(), error) {
	pythonVersion := ResolvePythonVersionFromEnv(envVars)
	if versionFromInterpreter := ResolvePythonVersionFromInterpreter(interpreter); versionFromInterpreter != "" {
		pythonVersion = versionFromInterpreter
		if envVars != nil {
			envVars["DAIDAI_PYTHON_VERSION"] = pythonVersion
		}
	}
	runtimePaths := currentManagedRuntimePathsForPythonVersion(pythonVersion)

	switch {
	case IsPythonInterpreter(interpreter):
		return createManagedPythonCommand(scriptPath, scriptArgs, workDir, envVars, runtimePaths, pythonVersion)
	}

	switch interpreter {
	case "node":
		return createManagedNodeCommand(scriptPath, scriptArgs, workDir, envVars, runtimePaths)
	case "ts-node":
		return createManagedTSNodeCommand(scriptPath, scriptArgs, workDir, envVars, runtimePaths)
	default:
		return createStandardManagedCommand(interpreter, scriptPath, scriptArgs, workDir, envVars, runtimePaths)
	}
}

func currentManagedRuntimePaths() managedRuntimePaths {
	return currentManagedRuntimePathsForPythonVersion("")
}

func currentManagedRuntimePathsForPythonVersion(pythonVersion string) managedRuntimePaths {
	pythonVersion = NormalizePythonVersionOrDefault(pythonVersion)
	dataDir := ""
	if config.C != nil {
		dataDir = config.C.Data.Dir
	}
	depsDir := filepath.Join(dataDir, "deps")
	venvDir := ManagedPythonVenvDir(pythonVersion)
	venvBin := resolveManagedVenvBin(venvDir)
	nodeBin := filepath.Join(depsDir, "nodejs", "node_modules", ".bin")
	sanitizedPath := sanitizeManagedPath(os.Getenv("PATH"), nodeBin, venvBin)

	return managedRuntimePaths{
		NodeBin:          nodeBin,
		NodeModules:      filepath.Join(depsDir, "nodejs", "node_modules"),
		VenvBin:          venvBin,
		VenvSitePackages: findVenvSitePackages(venvDir),
		SanitizedPath:    sanitizedPath,
		searchDirs:       splitPathDirs(sanitizedPath),
	}
}

func managedPythonVenvHealthy(venvDir string) bool {
	return managedPythonVenvHealthyForVersion(venvDir, "")
}

func managedPythonVenvHealthyForVersion(venvDir, pythonVersion string) bool {
	venvBin := resolveManagedVenvBin(venvDir)
	if info, err := os.Stat(venvBin); err != nil || !info.IsDir() {
		return false
	}
	pythonBin := resolveManagedPythonBinaryInVenv(venvDir)
	if pythonBin == "" {
		return false
	}
	if version := strings.TrimSpace(pythonVersion); version != "" && !managedPythonBinaryMatchesVersion(pythonBin, version) {
		return false
	}

	pipBin := resolveManagedPipBinaryInVenv(venvDir)
	if pipBin == "" {
		return false
	}

	cmd := exec.Command(pipBin, "--version")
	cmd.Env = appendPythonBootstrapEnv(SanitizePipEnv(os.Environ()))
	out, err := cmd.CombinedOutput()
	return err == nil && strings.Contains(strings.ToLower(string(out)), "pip")
}

func managedPythonBinaryMatchesVersion(pythonBin, pythonVersion string) bool {
	pythonVersion = NormalizePythonVersionOrDefault(pythonVersion)
	cmd := exec.Command(pythonBin, "-c", "import sys; print(f'{sys.version_info.major}.{sys.version_info.minor}')")
	cmd.Env = appendPythonBootstrapEnv(SanitizePipEnv(os.Environ()))
	out, err := cmd.CombinedOutput()
	return err == nil && strings.TrimSpace(string(out)) == pythonVersion
}

func resolveManagedPythonBinaryInVenv(venvDir string) string {
	venvBin := resolveManagedVenvBin(venvDir)
	for _, name := range []string{"python", "python3"} {
		if binary := findExecutableInDir(venvBin, name); binary != "" {
			return binary
		}
	}
	return ""
}

func resolveManagedPipBinaryInVenv(venvDir string) string {
	venvBin := resolveManagedVenvBin(venvDir)
	for _, name := range []string{"pip3", "pip"} {
		if binary := findExecutableInDir(venvBin, name); binary != "" {
			return binary
		}
	}
	return ""
}

func repairManagedPythonVenvPip(venvDir string) bool {
	pythonBin := resolveManagedPythonBinaryInVenv(venvDir)
	if pythonBin == "" {
		return false
	}

	cmd := exec.Command(pythonBin, "-m", "ensurepip", "--upgrade")
	cmd.Env = appendPythonBootstrapEnv(SanitizePipEnv(os.Environ()))
	out, err := cmd.CombinedOutput()
	if err != nil {
		log.Printf("warn: managed python venv pip repair failed at %s: %v: %s", venvDir, err, strings.TrimSpace(string(out)))
		return false
	}
	return managedPythonVenvHealthy(venvDir)
}

func quarantineManagedPythonVenv(venvDir string) {
	venvDir = strings.TrimSpace(venvDir)
	if venvDir == "" {
		return
	}
	if _, err := os.Stat(venvDir); err != nil {
		return
	}

	backup := venvDir + ".broken-" + time.Now().Format("20060102150405")
	if err := os.Rename(venvDir, backup); err == nil {
		log.Printf("managed python venv moved aside for rebuild: %s -> %s", venvDir, backup)
		return
	}
	if err := os.RemoveAll(venvDir); err != nil {
		log.Printf("warn: failed to remove broken managed python venv %s: %v", venvDir, err)
	}
}

func cleanupBrokenManagedPythonVenvs() {
	dataDir := ""
	if config.C != nil {
		dataDir = config.C.Data.Dir
	}
	if strings.TrimSpace(dataDir) == "" {
		return
	}

	pythonDir := filepath.Join(dataDir, "deps", "python")
	entries, err := os.ReadDir(pythonDir)
	if err != nil {
		return
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := strings.TrimSpace(entry.Name())
		if !strings.Contains(name, ".broken-") {
			continue
		}
		fullPath := filepath.Join(pythonDir, name)
		if err := os.RemoveAll(fullPath); err != nil {
			log.Printf("warn: failed to cleanup broken managed python venv backup %s: %v", fullPath, err)
			continue
		}
		log.Printf("cleanup broken managed python venv backup: %s", fullPath)
	}
}

func CleanupManagedPythonArtifactsOnStartup() {
	managedPythonVenvMu.Lock()
	defer managedPythonVenvMu.Unlock()

	migrateLegacyManagedPythonVenvLocked()
	cleanupUnsupportedManagedPythonVenvsLocked()
	cleanupBrokenManagedPythonVenvs()
}

func detectManagedPythonVenvVersion(venvDir string) string {
	pythonBin := resolveManagedPythonBinaryInVenv(venvDir)
	if pythonBin == "" {
		return ""
	}

	cmd := exec.Command(pythonBin, "-c", "import sys; print(f'{sys.version_info.major}.{sys.version_info.minor}')")
	cmd.Env = appendPythonBootstrapEnv(SanitizePipEnv(os.Environ()))
	out, err := cmd.CombinedOutput()
	if err != nil {
		return ""
	}

	version, err := NormalizePythonVersionStrict(strings.TrimSpace(string(out)))
	if err != nil {
		return ""
	}
	return version
}

type LegacyPythonVenvMigration struct {
	Version               string
	FoundRoot             bool
	MigratedRoot          bool
	DefaultVersionExisted bool
}

func managedPythonVersionDir(version string) string {
	dataDir := ""
	if config.C != nil {
		dataDir = config.C.Data.Dir
	}
	return filepath.Join(dataDir, "deps", "python", NormalizePythonVersionOrDefault(version))
}

func cleanupUnsupportedManagedPythonVenvsLocked() {
	allowed := make(map[string]bool)
	for _, version := range CurrentPythonRuntimeVersions() {
		allowed[version] = true
	}
	if len(allowed) >= len(allPythonRuntimeVersions) {
		return
	}

	// 只删除面板托管的 Python 小版本目录，不碰 nodejs、脚本、备份和未知目录。
	// 这样旧版三 Python 镜像升级到单版本镜像后，会自动释放 3.10 / 3.11 venv 空间，
	// 同时保留当前镜像版本（默认 latest 为 3.12）的依赖环境。
	for _, version := range allPythonRuntimeVersions {
		if allowed[version] {
			continue
		}
		versionDir := managedPythonVersionDir(version)
		if !directoryExists(versionDir) {
			continue
		}
		if err := os.RemoveAll(versionDir); err != nil {
			log.Printf("warn: failed to remove unsupported managed python %s venv %s: %v", version, versionDir, err)
			continue
		}
		log.Printf("unsupported managed python %s venv removed for current image policy: %s", version, versionDir)
	}
}

func directoryExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func flattenLegacyVersionedManagedPythonVenvsLocked() {
	for _, version := range allPythonRuntimeVersions {
		versionDir := managedPythonVersionDir(version)
		nestedVenvDir := filepath.Join(versionDir, "venv")
		if !directoryExists(nestedVenvDir) {
			continue
		}
		if directoryExists(resolveManagedVenvBin(versionDir)) {
			backup := nestedVenvDir + ".nested-" + time.Now().Format("20060102150405")
			if err := os.Rename(nestedVenvDir, backup); err == nil {
				log.Printf("legacy nested managed python %s venv moved aside because flat target already exists: %s -> %s", version, nestedVenvDir, backup)
			} else {
				log.Printf("warn: failed to move nested managed python %s venv aside %s: %v", version, nestedVenvDir, err)
			}
			continue
		}

		backupDir := versionDir + ".pre-2218-" + time.Now().Format("20060102150405")
		if err := os.Rename(versionDir, backupDir); err != nil {
			log.Printf("warn: failed to prepare legacy managed python %s venv flatten %s: %v", version, versionDir, err)
			continue
		}
		if err := os.Rename(filepath.Join(backupDir, "venv"), versionDir); err != nil {
			log.Printf("warn: failed to flatten legacy managed python %s venv %s -> %s: %v", version, filepath.Join(backupDir, "venv"), versionDir, err)
			_ = os.Rename(backupDir, versionDir)
			continue
		}
		_ = os.Remove(backupDir)
		log.Printf("legacy managed python %s venv flattened: %s -> %s", version, filepath.Join(backupDir, "venv"), versionDir)
	}
}

func migrateLegacyManagedPythonVenvLocked() LegacyPythonVenvMigration {
	flattenLegacyVersionedManagedPythonVenvsLocked()

	result := LegacyPythonVenvMigration{
		DefaultVersionExisted: directoryExists(ManagedPythonVenvDir(defaultPythonRuntimeVersion)),
	}

	legacyDir := legacyManagedPythonVenvDir()
	if strings.TrimSpace(legacyDir) == "" {
		return result
	}
	if info, err := os.Stat(legacyDir); err != nil || !info.IsDir() {
		return result
	}

	version := detectManagedPythonVenvVersion(legacyDir)
	if version == "" {
		version = defaultPythonRuntimeVersion
		log.Printf("warn: legacy managed python venv version detection failed at %s, assuming Python %s", legacyDir, version)
	}
	result.Version = version
	result.FoundRoot = true

	targetDir := ManagedPythonVenvDir(version)
	if filepath.Clean(targetDir) == filepath.Clean(legacyDir) {
		return result
	}
	if info, err := os.Stat(targetDir); err == nil && info.IsDir() {
		backup := legacyDir + ".migrated-" + time.Now().Format("20060102150405")
		if err := os.Rename(legacyDir, backup); err == nil {
			log.Printf("legacy managed python venv kept aside because target already exists: %s -> %s", legacyDir, backup)
		} else {
			log.Printf("warn: failed to move legacy managed python venv aside %s: %v", legacyDir, err)
		}
		return result
	}

	if err := os.MkdirAll(filepath.Dir(targetDir), 0o755); err != nil {
		log.Printf("warn: failed to create managed python version dir parent %s: %v", filepath.Dir(targetDir), err)
		return result
	}
	if err := os.Rename(legacyDir, targetDir); err != nil {
		log.Printf("warn: failed to migrate legacy managed python venv %s -> %s: %v", legacyDir, targetDir, err)
		return result
	}
	log.Printf("legacy managed python venv migrated: %s -> %s (Python %s)", legacyDir, targetDir, version)
	result.MigratedRoot = true
	return result
}

func MigrateLegacyManagedPythonVenv() string {
	return MigrateLegacyManagedPythonVenvInfo().Version
}

func MigrateLegacyManagedPythonVenvInfo() LegacyPythonVenvMigration {
	managedPythonVenvMu.Lock()
	defer managedPythonVenvMu.Unlock()
	return migrateLegacyManagedPythonVenvLocked()
}

func ensureManagedPythonVenv(syncCreate bool) bool {
	return ensureManagedPythonVenvForVersion("", syncCreate)
}

// ensureManagedPythonVenvForVersion 每条返回 true 的路径，结尾都是一次通过的 managedPythonVenvHealthyForVersion。
// 两个 ResolveManaged*BinaryForPythonVersion 与两个 createManagedPython* 靠这一点不再自己查第二遍（#158）：
// 以后要给这里加缓存、「刚查过就直接返回 true」之类的捷径，必须同时改那四处，否则会把坏掉的 venv 里的 pip / python 交出去。
func ensureManagedPythonVenvForVersion(pythonVersion string, syncCreate bool) bool {
	pythonVersion = NormalizePythonVersionOrDefault(pythonVersion)
	if !PythonVersionSupportedByCurrentRuntime(pythonVersion) {
		return false
	}
	dataDir := ""
	if config.C != nil {
		dataDir = config.C.Data.Dir
	}
	if strings.TrimSpace(dataDir) == "" {
		return false
	}

	venvDir := ManagedPythonVenvDir(pythonVersion)
	if managedPythonVenvHealthyForVersion(venvDir, pythonVersion) {
		return true
	}

	if !syncCreate {
		go ensureManagedPythonVenvForVersion(pythonVersion, true)
		return false
	}

	managedPythonVenvMu.Lock()
	defer managedPythonVenvMu.Unlock()

	migrateLegacyManagedPythonVenvLocked()

	if managedPythonVenvHealthyForVersion(venvDir, pythonVersion) {
		return true
	}

	if info, err := os.Stat(resolveManagedVenvBin(venvDir)); err == nil && info.IsDir() {
		if repairManagedPythonVenvPip(venvDir) && managedPythonVenvHealthyForVersion(venvDir, pythonVersion) {
			log.Printf("managed python venv pip repaired at %s", venvDir)
			return true
		}
		quarantineManagedPythonVenv(venvDir)
	}

	_ = os.MkdirAll(filepath.Dir(venvDir), 0o755)
	var lastErr error
	for _, candidate := range managedPythonBootstrapCommandsForVersion(pythonVersion) {
		if !managedBootstrapCommandMatchesVersion(candidate, pythonVersion) {
			continue
		}
		// v2.2.4 重构 bootstrap 命令表时漏了把 venvDir 拼到 args 末尾，
		// 导致执行的是 `python3 -m venv`（不带目标路径）必然失败。venv 永远建不出来，
		// ResolveManagedPipBinary 返回空，自动安装 fallback 到系统 pip3，
		// Alpine/Debian 上的 PEP 668 把"externally-managed-environment"砸到用户脸上。
		args := append(append([]string(nil), candidate.args...), venvDir)
		cmd := exec.Command(candidate.binary, args...)
		cmd.Env = appendPythonBootstrapEnv(SanitizePipEnv(os.Environ()))
		out, runErr := cmd.CombinedOutput()
		if runErr == nil {
			if managedPythonVenvHealthyForVersion(venvDir, pythonVersion) {
				log.Printf("managed python %s venv created at %s using %s", pythonVersion, venvDir, candidate.binary)
				return true
			}
			if repairManagedPythonVenvPip(venvDir) && managedPythonVenvHealthyForVersion(venvDir, pythonVersion) {
				log.Printf("managed python %s venv created and pip repaired at %s using %s", pythonVersion, venvDir, candidate.binary)
				return true
			}
			lastErr = fmt.Errorf("%s %v created venv but pip is unavailable", candidate.binary, args)
			quarantineManagedPythonVenv(venvDir)
			continue
		}
		lastErr = fmt.Errorf("%s %v failed: %v: %s", candidate.binary, args, runErr, strings.TrimSpace(string(out)))
		quarantineManagedPythonVenv(venvDir)
	}
	if lastErr != nil {
		log.Printf("warn: managed python %s venv create failed: %v (auto-install will fall back to system pip with --break-system-packages)", pythonVersion, lastErr)
	}
	return false
}

func EnsureManagedPythonVenv() bool {
	return EnsureManagedPythonVenvForVersion("")
}

func EnsureManagedPythonVenvForVersion(pythonVersion string) bool {
	return ensureManagedPythonVenvForVersion(pythonVersion, true)
}

func WarmManagedPythonVenv() {
	cleanupBrokenManagedPythonVenvsFunc()

	defaultVersion := DefaultPythonVersion()
	warmed := map[string]bool{}
	if strings.TrimSpace(defaultVersion) != "" {
		WarmManagedPythonVenvForVersion(defaultVersion)
		warmed[defaultVersion] = true
	}

	for _, version := range CurrentPythonRuntimeVersions() {
		if warmed[version] {
			continue
		}
		if directoryExists(ManagedPythonVenvDir(version)) {
			WarmManagedPythonVenvForVersion(version)
		}
	}
}

func WarmManagedPythonVenvForVersion(pythonVersion string) {
	warmManagedPythonVenvForVersionFunc(pythonVersion)
}

func resolveManagedVenvBin(venvDir string) string {
	venvDir = strings.TrimSpace(venvDir)
	if venvDir == "" {
		return ""
	}

	candidates := []string{
		filepath.Join(venvDir, "Scripts"),
		filepath.Join(venvDir, "bin"),
	}
	if runtime.GOOS != "windows" {
		candidates[0], candidates[1] = candidates[1], candidates[0]
	}

	for _, candidate := range candidates {
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate
		}
	}

	if runtime.GOOS == "windows" {
		return filepath.Join(venvDir, "Scripts")
	}
	return filepath.Join(venvDir, "bin")
}

func ResolveManagedPipBinary() string {
	return ResolveManagedPipBinaryForPythonVersion("")
}

func ResolveManagedPipBinaryForPythonVersion(pythonVersion string) string {
	pythonVersion = NormalizePythonVersionOrDefault(pythonVersion)
	if !PythonVersionSupportedByCurrentRuntime(pythonVersion) {
		return ""
	}
	// Ensure 返回 true 时刚做过一遍通过的健康检查（或刚建好 / 修好 venv，结尾同样查过一遍），这里不再自己查第二遍：
	// 每查一遍要起 2 个子进程（python -c 核对版本 + pip --version），慢 NAS 上一个就几秒（#158）。
	// 返回 false（venv 建不出来也修不好）时照旧返回空，由调用方走各自原有的回退（系统 pip、venv 里残留的 pip）。
	if !EnsureManagedPythonVenvForVersion(pythonVersion) {
		return ""
	}
	return resolveManagedPipBinaryInVenv(ManagedPythonVenvDir(pythonVersion))
}

// ResolveManagedPythonBinary 返回面板默认版本托管 venv 的 python 可执行文件路径，
// 供 ddp 等容器内命令在与任务执行一致的环境里跑脚本/开 shell。venv 缺失时会先按需创建。
func ResolveManagedPythonBinary() string {
	return ResolveManagedPythonBinaryForPythonVersion("")
}

func ResolveManagedPythonBinaryForPythonVersion(pythonVersion string) string {
	pythonVersion = NormalizePythonVersionOrDefault(pythonVersion)
	if !PythonVersionSupportedByCurrentRuntime(pythonVersion) {
		return ""
	}
	// 同 ResolveManagedPipBinaryForPythonVersion：Ensure 返回 true 就不再查第二遍（#158）。
	if !EnsureManagedPythonVenvForVersion(pythonVersion) {
		return ""
	}
	return resolveManagedPythonBinaryInVenv(ManagedPythonVenvDir(pythonVersion))
}

func createManagedPythonCommand(scriptPath string, scriptArgs []string, workDir string, envVars map[string]string, runtimePaths managedRuntimePaths, pythonVersion string) (*exec.Cmd, func(), error) {
	pythonVersion = NormalizePythonVersionOrDefault(pythonVersion)
	if !PythonVersionSupportedByCurrentRuntime(pythonVersion) {
		return nil, nil, fmt.Errorf("当前镜像不支持 Python %s，请切换到对应 Python 版本镜像或 all 镜像", pythonVersion)
	}
	venvReady := EnsureManagedPythonVenvForVersion(pythonVersion)
	runtimePaths = currentManagedRuntimePathsForPythonVersion(pythonVersion)
	pythonBin := ""
	if venvReady {
		// Ensure 返回 true 时，它的健康检查刚用 python -c 核对过 venv 里这个解释器（resolveManagedPythonBinaryInVenv 找到的同一个文件）的版本，
		// 直接用，不再进下面的候选循环多起一次 python -c（#158）。
		pythonBin = resolveManagedPythonBinaryInVenv(ManagedPythonVenvDir(pythonVersion))
	}
	if pythonBin == "" {
		// venv 不可用（建不出来、修不好）时照旧按原顺序找：托管 venv、Windows 常见安装目录、PATH，逐个核对版本。
		preferredDirs := append([]string{runtimePaths.VenvBin}, windowsPythonPreferredDirsForVersion(pythonVersion)...)
		for _, name := range []string{"python", "python3", "python" + pythonVersion} {
			candidate, err := resolveManagedBinary(name, preferredDirs, runtimePaths.searchDirs)
			if err == nil && managedPythonBinaryMatchesVersion(candidate, pythonVersion) {
				pythonBin = candidate
				break
			}
		}
	}
	if pythonBin == "" {
		return nil, nil, fmt.Errorf("Python %s 不可用，请先安装对应版本，或切换任务 Python 版本", pythonVersion)
	}

	tempDir, envFile, cleanup, err := writeManagedRuntimeEnvFile(envVars)
	if err != nil {
		return nil, nil, err
	}
	_ = tempDir

	args := []string{"-u", "-c", pythonEnvBootstrap, envFile, scriptPath, strings.TrimSpace(envVars["PYTHONPATH"])}
	args = append(args, scriptArgs...)

	cmd := exec.Command(pythonBin, args...)
	cmd.Dir = workDir
	cmd.Env = buildPythonBootstrapProcessEnv(envVars)
	setPgid(cmd)
	return cmd, cleanup, nil
}

func createManagedPythonModuleCommand(interpreter string, moduleName string, moduleArgs []string, workDir string, envVars map[string]string) (*exec.Cmd, func(), error) {
	pythonVersion := ResolvePythonVersionFromEnv(envVars)
	if versionFromInterpreter := ResolvePythonVersionFromInterpreter(interpreter); versionFromInterpreter != "" {
		pythonVersion = versionFromInterpreter
		if envVars != nil {
			envVars["DAIDAI_PYTHON_VERSION"] = pythonVersion
		}
	}
	pythonVersion = NormalizePythonVersionOrDefault(pythonVersion)
	if !PythonVersionSupportedByCurrentRuntime(pythonVersion) {
		return nil, nil, fmt.Errorf("当前镜像不支持 Python %s，请切换到对应 Python 版本镜像或 all 镜像", pythonVersion)
	}

	venvReady := EnsureManagedPythonVenvForVersion(pythonVersion)
	runtimePaths := currentManagedRuntimePathsForPythonVersion(pythonVersion)
	pythonBin := ""
	if venvReady {
		// 同 createManagedPythonCommand：Ensure 已核对过版本，直接用 venv 里的解释器，不再多起一次 python -c（#158）。
		pythonBin = resolveManagedPythonBinaryInVenv(ManagedPythonVenvDir(pythonVersion))
	}
	if pythonBin == "" {
		// venv 不可用时照旧按原顺序找：托管 venv、Windows 常见安装目录、PATH，逐个核对版本。
		preferredDirs := append([]string{runtimePaths.VenvBin}, windowsPythonPreferredDirsForVersion(pythonVersion)...)
		for _, name := range []string{"python", "python3", "python" + pythonVersion} {
			candidate, err := resolveManagedBinary(name, preferredDirs, runtimePaths.searchDirs)
			if err == nil && managedPythonBinaryMatchesVersion(candidate, pythonVersion) {
				pythonBin = candidate
				break
			}
		}
	}
	if pythonBin == "" {
		return nil, nil, fmt.Errorf("Python %s 不可用，请先安装对应版本，或切换任务 Python 版本", pythonVersion)
	}

	tempDir, envFile, cleanup, err := writeManagedRuntimeEnvFile(envVars)
	if err != nil {
		return nil, nil, err
	}
	_ = tempDir

	args := []string{"-u", "-c", pythonModuleEnvBootstrap, envFile, moduleName, strings.TrimSpace(envVars["PYTHONPATH"])}
	args = append(args, cleanManagedProcessArgs(moduleArgs)...)

	cmd := exec.Command(pythonBin, args...)
	cmd.Dir = workDir
	cmd.Env = buildPythonBootstrapProcessEnv(envVars)
	setPgid(cmd)
	return cmd, cleanup, nil
}

func createManagedExecutableCommand(commandName string, commandArgs []string, workDir string, envVars map[string]string) (*exec.Cmd, func(), error) {
	pythonVersion := ResolvePythonVersionFromEnv(envVars)
	runtimePaths := currentManagedRuntimePathsForPythonVersion(pythonVersion)
	preferredDirs := []string{runtimePaths.VenvBin, runtimePaths.NodeBin}

	binary, err := resolveManagedBinary(commandName, preferredDirs, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("找不到托管依赖命令 %s，请先在依赖页面安装对应 Python/Node 依赖，或改用脚本文件命令", commandName)
	}

	cmd := exec.Command(binary, cleanManagedProcessArgs(commandArgs)...)
	cmd.Dir = workDir
	// 依赖命令不是面板脚本，无法走 Python/Node bootstrap，只能通过真实进程环境传入任务变量。
	// 这里仍然过滤危险变量和 NUL，避免 LD_PRELOAD 等变量污染托管运行时。
	cmd.Env = appendPythonBootstrapEnv(buildEnv(envVars))
	setPgid(cmd)
	return cmd, func() {}, nil
}

func createManagedNodeCommand(scriptPath string, scriptArgs []string, workDir string, envVars map[string]string, runtimePaths managedRuntimePaths) (*exec.Cmd, func(), error) {
	nodeBin, err := resolveManagedBinary("node", nil, runtimePaths.searchDirs)
	if err != nil {
		return nil, nil, err
	}

	_, envFile, cleanup, err := writeManagedRuntimeEnvFile(envVars)
	if err != nil {
		return nil, nil, err
	}
	nodeModulesCleanup := ensureManagedNodeModulesAccess(workDir, runtimePaths.NodeModules)

	preloadFile, preloadErr := writeNodePreloadScript(filepath.Dir(envFile), envFile, envVars, model.GetRegisteredConfigBool("detect_silent_exit"))
	if preloadErr != nil {
		cleanup()
		nodeModulesCleanup()
		return nil, nil, preloadErr
	}

	args := []string{"--require", preloadFile, scriptPath}
	args = append(args, scriptArgs...)

	cmd := exec.Command(nodeBin, args...)
	cmd.Dir = workDir
	cmd.Env = buildBootstrapProcessEnv(envVars)
	setPgid(cmd)
	return cmd, combineCleanup(cleanup, nodeModulesCleanup), nil
}

func createManagedTSNodeCommand(scriptPath string, scriptArgs []string, workDir string, envVars map[string]string, runtimePaths managedRuntimePaths) (*exec.Cmd, func(), error) {
	_, envFile, cleanup, err := writeManagedRuntimeEnvFile(envVars)
	if err != nil {
		return nil, nil, err
	}
	nodeModulesCleanup := ensureManagedNodeModulesAccess(workDir, runtimePaths.NodeModules)

	preloadFile, preloadErr := writeNodePreloadScript(filepath.Dir(envFile), envFile, envVars, model.GetRegisteredConfigBool("detect_silent_exit"))
	if preloadErr != nil {
		cleanup()
		nodeModulesCleanup()
		return nil, nil, preloadErr
	}

	tsNodeBin, tsErr := resolveManagedBinary("ts-node", []string{runtimePaths.NodeBin}, runtimePaths.searchDirs)
	if tsErr == nil {
		args := []string{"--require", preloadFile, scriptPath}
		args = append(args, scriptArgs...)
		cmd := exec.Command(tsNodeBin, args...)
		cmd.Dir = workDir
		cmd.Env = buildBootstrapProcessEnv(envVars)
		setPgid(cmd)
		return cmd, combineCleanup(cleanup, nodeModulesCleanup), nil
	}

	npxBin, err := resolveManagedBinary("npx", nil, runtimePaths.searchDirs)
	if err != nil {
		cleanup()
		nodeModulesCleanup()
		return nil, nil, err
	}

	args := []string{"ts-node", "--require", preloadFile, scriptPath}
	args = append(args, scriptArgs...)

	cmd := exec.Command(npxBin, args...)
	cmd.Dir = workDir
	cmd.Env = buildBootstrapProcessEnv(envVars)
	setPgid(cmd)
	return cmd, combineCleanup(cleanup, nodeModulesCleanup), nil
}

func createStandardManagedCommand(interpreter, scriptPath string, scriptArgs []string, workDir string, envVars map[string]string, runtimePaths managedRuntimePaths) (*exec.Cmd, func(), error) {
	switch interpreter {
	case "bash":
		return createManagedShellCommand(scriptPath, scriptArgs, workDir, envVars, runtimePaths)
	case "go":
		return createManagedGoCommand(scriptPath, scriptArgs, workDir, envVars, runtimePaths)
	}

	binary, err := resolveManagedBinary(interpreter, standardBinaryPreferredDirs(interpreter, runtimePaths), runtimePaths.searchDirs)
	if err != nil {
		return nil, nil, err
	}

	args := append([]string{scriptPath}, cleanManagedProcessArgs(scriptArgs)...)

	cmd := exec.Command(binary, args...)
	cmd.Dir = workDir
	cmd.Env = buildBootstrapProcessEnv(envVars)
	setPgid(cmd)
	return cmd, func() {}, nil
}

func createManagedShellCommand(scriptPath string, scriptArgs []string, workDir string, envVars map[string]string, runtimePaths managedRuntimePaths) (*exec.Cmd, func(), error) {
	if err := NormalizeShellScriptFile(scriptPath); err != nil {
		return nil, nil, fmt.Errorf("脚本换行规范化失败: %w", err)
	}

	binary, err := resolveManagedBinary("bash", standardBinaryPreferredDirs("bash", runtimePaths), runtimePaths.searchDirs)
	if err != nil {
		return nil, nil, err
	}

	_, envFile, cleanup, err := writeManagedRuntimeShellEnvFile(envVars)
	if err != nil {
		return nil, nil, err
	}

	args := []string{"-c", shellEnvBootstrap, scriptPath, envFile, scriptPath}
	args = append(args, cleanManagedProcessArgs(scriptArgs)...)

	cmd := exec.Command(binary, args...)
	cmd.Dir = workDir
	cmd.Env = buildBootstrapProcessEnv(envVars)
	setPgid(cmd)
	return cmd, cleanup, nil
}

func createManagedGoCommand(scriptPath string, scriptArgs []string, workDir string, envVars map[string]string, runtimePaths managedRuntimePaths) (*exec.Cmd, func(), error) {
	binary, err := resolveManagedBinary("go", standardBinaryPreferredDirs("go", runtimePaths), runtimePaths.searchDirs)
	if err != nil {
		return nil, nil, err
	}

	_, envFile, envCleanup, err := writeManagedRuntimeEnvFile(envVars)
	if err != nil {
		return nil, nil, err
	}

	wrapperPath := filepath.Join(filepath.Dir(scriptPath), fmt.Sprintf("000000_daidai_env_bootstrap_%d.go", time.Now().UnixNano()))
	if err := os.WriteFile(wrapperPath, []byte(goEnvBootstrapSource), 0o600); err != nil {
		envCleanup()
		return nil, nil, err
	}

	cleanup := func() {
		_ = os.Remove(wrapperPath)
		envCleanup()
	}

	args := []string{"run", wrapperPath, scriptPath}
	args = append(args, cleanManagedProcessArgs(scriptArgs)...)

	cmd := exec.Command(binary, args...)
	cmd.Dir = workDir
	cmd.Env = append(buildBootstrapProcessEnv(envVars), "DAIDAI_RUNTIME_ENV_FILE="+envFile)
	setPgid(cmd)
	return cmd, cleanup, nil
}

func standardBinaryPreferredDirs(interpreter string, runtimePaths managedRuntimePaths) []string {
	switch interpreter {
	case "bash":
		if runtime.GOOS == "windows" {
			return windowsShellSearchDirs
		}
		return nil
	case "go":
		return nil
	default:
		return nil
	}
}

type managedBootstrapCommand struct {
	binary            string
	versionArgsPrefix []string
	args              []string
}

func managedPythonBootstrapCommands() []managedBootstrapCommand {
	return managedPythonBootstrapCommandsForVersion("")
}

func managedPythonBootstrapCommandsForVersion(pythonVersion string) []managedBootstrapCommand {
	pythonVersion = NormalizePythonVersionOrDefault(pythonVersion)
	commands := []managedBootstrapCommand{
		{binary: "python" + pythonVersion, args: []string{"-m", "venv"}},
		{binary: "python3", args: []string{"-m", "venv"}, versionArgsPrefix: []string{}},
		{binary: "python", args: []string{"-m", "venv"}, versionArgsPrefix: []string{}},
	}
	if runtime.GOOS == "windows" {
		commands = append([]managedBootstrapCommand{
			{binary: "py", versionArgsPrefix: []string{"-" + pythonVersion}, args: []string{"-" + pythonVersion, "-m", "venv"}},
			{binary: "py", versionArgsPrefix: []string{"-3"}, args: []string{"-3", "-m", "venv"}},
		}, commands...)
	}
	return commands
}

func buildBootstrapProcessEnv(envVars map[string]string) []string {
	safeKeys := []string{"PATH", "HOME", "USER", "LANG", "LC_ALL", "TZ", "LD_LIBRARY_PATH"}
	if runtime.GOOS == "windows" {
		safeKeys = append(safeKeys, "SYSTEMROOT", "PATHEXT", "TEMP", "TMP", "APPDATA", "LOCALAPPDATA", "USERPROFILE")
	}

	env := make([]string, 0, len(safeKeys))
	for _, key := range safeKeys {
		value := os.Getenv(key)
		if key == "PATH" && strings.TrimSpace(envVars["PATH"]) != "" {
			value = envVars["PATH"]
		}
		if key == "TZ" && strings.TrimSpace(envVars["TZ"]) != "" {
			value = envVars["TZ"]
		}
		if value == "" {
			continue
		}
		env = append(env, key+"="+value)
	}

	// 任务标识（#176）：其它用户变量都走 env 文件、在子进程里才落地，/proc/<pid>/environ 里看不到；
	// 「后台进程」页正是靠 environ 认出进程属于哪个任务，所以这一个必须放进真实进程环境，子孙进程也会继承。
	// 只有任务执行会在 envVars 里放它，调试运行、ddp、run-code 没有这个键，行为不变。
	if taskID := strings.TrimSpace(envVars[TaskIDEnvKey]); taskID != "" {
		env = append(env, TaskIDEnvKey+"="+taskID)
	}

	return AppendProxyEnv(env)
}

func buildPythonBootstrapProcessEnv(envVars map[string]string) []string {
	env := buildBootstrapProcessEnv(envVars)
	if runtime.GOOS != "windows" {
		return appendPythonBootstrapEnv(env)
	}

	ianaTimezone := strings.TrimSpace(envVars["TZ"])
	if ianaTimezone == "" {
		ianaTimezone = strings.TrimSpace(os.Getenv("TZ"))
	}
	pythonTimezone, err := windowsPythonPOSIXTimezone(ianaTimezone, time.Now())
	if err != nil {
		return appendPythonBootstrapEnv(env)
	}

	// Windows Python 启动时由 CRT 解析 TZ，只接受三字母 POSIX 格式；
	// env.json 会在 bootstrap 内把脚本最终可见的 TZ 恢复为原始 IANA 名称。
	for index, entry := range env {
		if strings.HasPrefix(entry, "TZ=") {
			env[index] = "TZ=" + pythonTimezone
			break
		}
	}
	return appendPythonBootstrapEnv(env)
}

func windowsPythonPOSIXTimezone(ianaTimezone string, now time.Time) (string, error) {
	location, err := time.LoadLocation(strings.TrimSpace(ianaTimezone))
	if err != nil {
		return "", fmt.Errorf("invalid IANA timezone %q: %w", ianaTimezone, err)
	}

	zoneName, utcOffsetSeconds := now.In(location).Zone()
	abbreviation := make([]byte, 0, 3)
	for index := 0; index < len(zoneName) && len(abbreviation) < 3; index++ {
		letter := zoneName[index]
		if letter >= 'a' && letter <= 'z' {
			letter -= 'a' - 'A'
		}
		if letter >= 'A' && letter <= 'Z' {
			abbreviation = append(abbreviation, letter)
		}
	}
	if len(abbreviation) < 3 {
		abbreviation = []byte("DDT")
	}

	// POSIX 的偏移符号与 UTC 偏移相反：UTC+8 要写成 -8，UTC-4 要写成 4。
	posixOffsetSeconds := -utcOffsetSeconds
	sign := ""
	if posixOffsetSeconds < 0 {
		sign = "-"
		posixOffsetSeconds = -posixOffsetSeconds
	}
	hours := posixOffsetSeconds / 3600
	minutes := posixOffsetSeconds % 3600 / 60
	seconds := posixOffsetSeconds % 60
	offset := fmt.Sprintf("%s%d", sign, hours)
	if minutes != 0 || seconds != 0 {
		offset += fmt.Sprintf(":%02d", minutes)
	}
	if seconds != 0 {
		offset += fmt.Sprintf(":%02d", seconds)
	}
	return string(abbreviation) + offset, nil
}

func appendPythonBootstrapEnv(env []string) []string {
	hasUTF8 := false
	hasEncoding := false
	for _, entry := range env {
		if strings.HasPrefix(entry, "PYTHONUTF8=") {
			hasUTF8 = true
		}
		if strings.HasPrefix(entry, "PYTHONIOENCODING=") {
			hasEncoding = true
		}
	}
	if !hasUTF8 {
		env = append(env, "PYTHONUTF8=1")
	}
	if !hasEncoding {
		env = append(env, "PYTHONIOENCODING=utf-8")
	}
	return env
}

func writeManagedRuntimeEnvFile(envVars map[string]string) (string, string, func(), error) {
	tempDir, err := os.MkdirTemp("", "daidai-runtime-*")
	if err != nil {
		return "", "", nil, err
	}

	cleanup := func() {
		_ = os.RemoveAll(tempDir)
	}

	payload := make(map[string]string, len(envVars))
	for key, value := range envVars {
		if strings.ContainsRune(value, 0) {
			continue
		}
		payload[key] = value
	}

	data, err := json.Marshal(payload)
	if err != nil {
		cleanup()
		return "", "", nil, err
	}

	envFile := filepath.Join(tempDir, "env.json")
	if err := os.WriteFile(envFile, data, 0o600); err != nil {
		cleanup()
		return "", "", nil, err
	}

	return tempDir, envFile, cleanup, nil
}

func writeManagedRuntimeShellEnvFile(envVars map[string]string) (string, string, func(), error) {
	tempDir, err := os.MkdirTemp("", "daidai-runtime-*")
	if err != nil {
		return "", "", nil, err
	}

	cleanup := func() {
		_ = os.RemoveAll(tempDir)
	}

	var b strings.Builder
	b.WriteString("# daidai runtime environment\n")
	keys := make([]string, 0, len(envVars))
	for key := range envVars {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		value := envVars[key]
		if !isValidShellEnvName(key) || isDangerousShellEnvName(key) || strings.ContainsRune(value, 0) {
			continue
		}
		b.WriteString(key)
		b.WriteByte('=')
		b.WriteString(shellSingleQuote(value))
		b.WriteByte('\n')
	}

	for _, key := range planShellEnvExport(envVars).Exported {
		b.WriteString("export ")
		b.WriteString(key)
		b.WriteByte('\n')
	}

	envFile := filepath.Join(tempDir, "env.sh")
	if err := os.WriteFile(envFile, []byte(b.String()), 0o600); err != nil {
		cleanup()
		return "", "", nil, err
	}

	return tempDir, envFile, cleanup, nil
}

// shellEnvExportPlan 是 writeManagedRuntimeShellEnvFile 的导出决策结果。
// 单独抽出来是为了让诊断提示（buildRuntimeEnvLimitWarnings）能复用同一套规则，
// 不至于两边阈值各写一份然后慢慢漂移。
type shellEnvExportPlan struct {
	// Exported 是真正会写 `export KEY` 的变量名，按 key 升序。
	Exported []string
	// SkippedTooLong 是单条就超过 MAX_ARG_STRLEN、只赋值不导出的变量。
	SkippedTooLong []runtimeEnvEntrySize
	// SkippedBudget 是单条不超限、但累计超过导出预算被让位的变量。
	SkippedBudget []runtimeEnvEntrySize
}

func planShellEnvExport(envVars map[string]string) shellEnvExportPlan {
	keys := make([]string, 0, len(envVars))
	for key := range envVars {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	plan := shellEnvExportPlan{Exported: make([]string, 0, len(keys))}
	exportedBytes := 0
	for _, key := range keys {
		value := envVars[key]
		if !isValidShellEnvName(key) || isDangerousShellEnvName(key) || strings.ContainsRune(value, 0) {
			continue
		}
		entry := runtimeEnvEntrySize{Name: key, Bytes: runtimeEnvEntryBytes(key, value)}
		if entry.Bytes > shellEnvExportValueMaxBytes {
			plan.SkippedTooLong = append(plan.SkippedTooLong, entry)
			continue
		}
		if exportedBytes+entry.Bytes > shellEnvExportBudgetBytes {
			plan.SkippedBudget = append(plan.SkippedBudget, entry)
			continue
		}
		plan.Exported = append(plan.Exported, key)
		exportedBytes += entry.Bytes
	}
	return plan
}

// shellEnvExportSkippedByBudget 只返回「因为预算被挤掉」的变量，按体积降序，
// 供诊断提示列出来。超过单条上限的那批由 buildRuntimeEnvLimitWarnings 单独讲。
func shellEnvExportSkippedByBudget(envVars map[string]string) []runtimeEnvEntrySize {
	skipped := planShellEnvExport(envVars).SkippedBudget
	sortRuntimeEnvEntries(skipped)
	return skipped
}

func isValidShellEnvName(name string) bool {
	if name == "" {
		return false
	}
	for idx, r := range name {
		if idx == 0 {
			if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || r == '_' {
				continue
			}
			return false
		}
		if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' {
			continue
		}
		return false
	}
	return true
}

func isDangerousShellEnvName(name string) bool {
	switch name {
	case "LD_PRELOAD", "LD_LIBRARY_PATH", "DYLD_INSERT_LIBRARIES":
		return true
	default:
		return false
	}
}

func shellSingleQuote(value string) string {
	if value == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

func cleanManagedProcessArgs(args []string) []string {
	cleaned := make([]string, 0, len(args))
	for _, arg := range args {
		if strings.ContainsRune(arg, 0) {
			continue
		}
		cleaned = append(cleaned, arg)
	}
	return cleaned
}

// silentExitProbeJS 检测「事件循环排空了，但还有 Promise 从未 settle」的半路静默死亡。
//
// 触发这种情况的典型写法：一个 Promise 只在 req 上挂了 error 监听，而连接是在响应
// 开始之后断的（Node 此时只在 res 上出声），于是它永远不 resolve 也不 reject。
// 等它的 await 就永久卡住，事件循环随后排空，node **以退出码 0 正常退出** ——
// 从面板外面看和「跑完了」完全一样，任务会被记成成功、不发失败通知。
//
// 判据是 beforeExit 时仍有未 settle 的 Promise。实测（Node 26）正常结束与「正常但慢」
// 两种对照都是 0 个，挂起场景稳定为非 0，信号是干净分离的。
//
// 只统计探针启用之后创建的 Promise：preload 自身、helper 的加载过程都在这之前，
// 不会被算进来。
const silentExitProbeJS = `
try {
  // ---- 三道闸，任一不满足就连 async_hooks 都不 require ----
  // 1) --require 会被 child_process.fork 继承（实测：子进程 execArgv 与父进程相同），
  //    不打戳的话每个子进程都会再武装一次，各自往同一条 stdout 刷重复告警。
  const armedAlready = process.env.DAIDAI_SILENT_EXIT_ARMED === '1';
  process.env.DAIDAI_SILENT_EXIT_ARMED = '1';
  // 2) 任务级开关：给某个噪音脚本单独配一条同名环境变量填 0 / off / false 即可关掉，
  //    不必为了它把全局配置也关掉、连累其余任务。
  const perTaskSwitch = String(process.env.DAIDAI_SILENT_EXIT_DETECT || '').toLowerCase();
  const disabledPerTask = perTaskSwitch === '0' || perTaskSwitch === 'off' || perTaskSwitch === 'false';
  // 3) worker 线程各自有独立事件循环，退出与主线程无关，判定没有意义。
  let isMainThread = true;
  try { isMainThread = require('worker_threads').isMainThread; } catch (_) {}

  if (!armedAlready && !disabledPerTask && isMainThread) {
    const asyncHooks = require('async_hooks');
    const pendingPromises = new Set();
    let probing = true;
    let declaredDone = false;

    // 脚本主流程末尾调一次 globalThis.daidaiDone() 即永久判定为「跑完了」。
    // 这是唯一能做到零误报的手段：探针分不清「被抛弃的 promise」和「被卡住的 promise」
    // ——两者在依赖图上完全同构——只有脚本自己知道工作做没做完。
    // configurable 必须为 true：否则脚本里出现同名变量赋值会抛 TypeError 打断用户脚本。
    try {
      Object.defineProperty(globalThis, 'daidaiDone', {
        value: function daidaiDone() { declaredDone = true; },
        writable: false, enumerable: false, configurable: true,
      });
    } catch (_) {}

    const probe = asyncHooks.createHook({
      init(asyncId, type) {
        if (probing && type === 'PROMISE') pendingPromises.add(asyncId);
      },
      promiseResolve(asyncId) { pendingPromises.delete(asyncId); },
      destroy(asyncId) { pendingPromises.delete(asyncId); },
    });
    probe.enable();

    process.on('beforeExit', () => {
      // beforeExit 可能触发多次（回调里又排入了异步工作），只在第一次判定。
      if (!probing) return;
      probing = false;
      probe.disable();
      if (declaredDone) return;
      const stuck = pendingPromises.size;
      if (stuck <= 0) return;
      process.stdout.write(
        '\n[任务疑似半路结束] 脚本没跑完就退出了：还有 ' + stuck +
        ' 个异步操作永远不会完成，常见于请求在响应中途断开、而代码只监听了请求对象的错误。\n' +
        '[任务疑似半路结束] 已判定为失败。若该脚本本来就会留下未完成的异步操作（属于误报），' +
        '有三种关法：脚本末尾加一行 globalThis.daidaiDone?.()；' +
        '给该任务加环境变量 DAIDAI_SILENT_EXIT_DETECT=0；' +
        '或在「系统设置 - 任务运行」里关闭「检测脚本半路静默结束」。\n'
      );
      // 不覆盖脚本自己设过的退出码：它已经表达了失败，保留它更有信息量。
      if (!process.exitCode) process.exitCode = 75;
    });
  }
} catch (err) {
  // 探针本身出任何问题都安静跳过，绝不能因为它让任务跑不起来。
}
`

func writeNodePreloadScript(tempDir, envFile string, envVars map[string]string, detectSilentExit bool) (string, error) {
	helperPath := filepath.ToSlash(strings.TrimSpace(envVars["DAIDAI_SEND_NOTIFY_JS"]))
	nodePathList := strings.Split(strings.TrimSpace(envVars["NODE_PATH"]), string(os.PathListSeparator))
	filteredNodePaths := make([]string, 0, len(nodePathList))
	for _, item := range nodePathList {
		item = strings.TrimSpace(item)
		if item != "" {
			filteredNodePaths = append(filteredNodePaths, filepath.ToSlash(item))
		}
	}

	helperJSON, err := json.Marshal(helperPath)
	if err != nil {
		return "", err
	}
	nodePathsJSON, err := json.Marshal(filteredNodePaths)
	if err != nil {
		return "", err
	}

	// 探针放在 preload 末尾注入：它只统计自己启用之后创建的 Promise，
	// 放前面会把 helper 加载过程中的异步操作一起算进去，形成误报。
	probeJS := ""
	if detectSilentExit {
		probeJS = silentExitProbeJS
	}

	script := fmt.Sprintf(`const fs = require('fs');
const path = require('path');
const Module = require('module');
const envPayload = JSON.parse(fs.readFileSync(%q, 'utf8'));
for (const [key, value] of Object.entries(envPayload)) {
  if (value === undefined || value === null) {
    continue;
  }
  process.env[key] = String(value);
}
// daidaiFlush：给脚本一个「等 stdout 真正写出去」的手段（#113-3）。
// 用户在汇总输出之后写一行 await globalThis.daidaiFlush?.() 即可，
// 老面板上没有这个函数时可选链会安静跳过，脚本不用改回去。
// writable 必须是 true：脚本里出现同名变量赋值时，严格模式下给只读属性赋值会抛
// TypeError 打断用户脚本（daidaiDone 那条是历史写法，不要照抄）。
try {
  Object.defineProperty(globalThis, 'daidaiFlush', {
    value: function daidaiFlush() {
      return new Promise(function (resolve) { process.stdout.write('', resolve); });
    },
    writable: true, enumerable: false, configurable: true,
  });
} catch (_) {}
// 把 stdout 切成阻塞写：管道在 macOS 上是异步的，进程一旦自然退出，
// 排在队列里还没落地的尾部输出就没了。Windows/Linux 本来就是同步写，这里是无害兜底。
// _handle 在重定向到文件等场景下可能没有 setBlocking，全部用短路判断保护。
try {
  if (process.stdout && process.stdout._handle && typeof process.stdout._handle.setBlocking === 'function') {
    process.stdout._handle.setBlocking(true);
  }
} catch (_) {}
const originalJSONStringify = JSON.stringify;
JSON.stringify = function(value, replacer, space) {
  if (value === process.env) {
    const envCopy = {};
    for (const [key, envValue] of Object.entries(process.env)) {
      // 兼容少数青龙脚本：它们用 JSON.stringify(process.env).indexOf("GITHUB")
      // 粗暴判断 CI 环境并 process.exit(0)。这里只隐藏 stringify 结果，不删除真实变量。
      if (String(key).includes('GITHUB') || String(envValue).includes('GITHUB')) {
        continue;
      }
      envCopy[key] = envValue;
    }
    return originalJSONStringify.call(JSON, envCopy, replacer, space);
  }
  return originalJSONStringify.call(JSON, value, replacer, space);
};
const extraNodePaths = %s;
const mergedNodePaths = [];
for (const value of [...extraNodePaths, ...(process.env.NODE_PATH ? process.env.NODE_PATH.split(path.delimiter) : [])]) {
  if (!value) {
    continue;
  }
  if (!mergedNodePaths.includes(value)) {
    mergedNodePaths.push(value);
  }
}
if (mergedNodePaths.length > 0) {
  process.env.NODE_PATH = mergedNodePaths.join(path.delimiter);
  Module._initPaths();
}
const _origResolve = Module._resolveFilename;
function _resolveExportsEntry(exp) {
  if (typeof exp === 'string') return exp;
  if (exp && typeof exp === 'object') {
    return exp.require || exp.default || exp.node || exp.import || '';
  }
  return '';
}
Module._resolveFilename = function(request, parent, isMain, options) {
  try {
    return _origResolve.call(this, request, parent, isMain, options);
  } catch (err) {
    if (err.code === 'ERR_PACKAGE_PATH_NOT_EXPORTED') {
      const parts = request.split('/');
      const pkgName = parts[0].startsWith('@') ? parts.slice(0, 2).join('/') : parts[0];
      const subPath = parts.slice(pkgName.startsWith('@') ? 2 : 1).join('/');
      for (const np of (process.env.NODE_PATH || '').split(path.delimiter)) {
        if (!np) continue;
        try {
          const pkgDir = path.join(np, pkgName);
          const pkgJson = JSON.parse(fs.readFileSync(path.join(pkgDir, 'package.json'), 'utf8'));
          let target = '';
          if (subPath) {
            const exportKey = './' + subPath;
            if (pkgJson.exports && pkgJson.exports[exportKey]) {
              target = _resolveExportsEntry(pkgJson.exports[exportKey]);
            }
            if (!target) target = subPath;
          } else {
            if (pkgJson.exports && pkgJson.exports['.']) {
              target = _resolveExportsEntry(pkgJson.exports['.']);
            }
            if (!target) target = pkgJson.main || '';
            if (!target) target = 'index.js';
          }
          const candidates = [
            path.join(pkgDir, target),
            path.join(pkgDir, target + '.js'),
            path.join(pkgDir, target, 'index.js')
          ];
          for (const c of candidates) {
            if (fs.existsSync(c)) return c;
          }
        } catch (_) {}
      }
    }
    throw err;
  }
};
const helperPath = %s;
if (helperPath) {
  require(helperPath);
}
%s`, filepath.ToSlash(envFile), string(nodePathsJSON), string(helperJSON), probeJS)

	preloadFile := filepath.Join(tempDir, "node-preload.js")
	if err := os.WriteFile(preloadFile, []byte(script), 0o600); err != nil {
		return "", err
	}

	return preloadFile, nil
}

func resolveManagedBinary(name string, preferredDirs []string, fallbackDirs []string) (string, error) {
	if strings.ContainsRune(name, os.PathSeparator) || strings.Contains(name, "/") {
		if isExecutableFile(name) {
			return name, nil
		}
		return "", fmt.Errorf("找不到可执行文件: %s", name)
	}

	searchDirs := make([]string, 0, len(preferredDirs)+len(fallbackDirs))
	seen := make(map[string]struct{}, len(preferredDirs)+len(fallbackDirs))
	for _, dir := range append(preferredDirs, fallbackDirs...) {
		dir = strings.TrimSpace(dir)
		if dir == "" {
			continue
		}
		if _, exists := seen[dir]; exists {
			continue
		}
		seen[dir] = struct{}{}
		searchDirs = append(searchDirs, dir)
	}

	for _, dir := range searchDirs {
		if binary := findExecutableInDir(dir, name); binary != "" {
			return binary, nil
		}
	}

	return "", fmt.Errorf("找不到可执行文件: %s", name)
}

func findExecutableInDir(dir, name string) string {
	if dir == "" {
		return ""
	}

	candidates := []string{name}
	if runtime.GOOS == "windows" && filepath.Ext(name) == "" {
		pathext := os.Getenv("PATHEXT")
		if pathext == "" {
			pathext = ".COM;.EXE;.BAT;.CMD"
		}
		for _, ext := range strings.Split(pathext, ";") {
			ext = strings.TrimSpace(ext)
			if ext == "" {
				continue
			}
			candidates = append(candidates, name+strings.ToLower(ext))
			candidates = append(candidates, name+strings.ToUpper(ext))
		}
	}

	for _, candidate := range candidates {
		fullPath := filepath.Join(dir, candidate)
		if isExecutableFile(fullPath) {
			return fullPath
		}
	}

	return ""
}

func isExecutableFile(path string) bool {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false
	}
	if runtime.GOOS == "windows" {
		return true
	}
	return info.Mode()&0o111 != 0
}

func findVenvSitePackages(venvDir string) string {
	venvDir = strings.TrimSpace(venvDir)
	if venvDir == "" {
		return ""
	}

	windowsSitePackages := filepath.Join(venvDir, "Lib", "site-packages")
	if info, err := os.Stat(windowsSitePackages); err == nil && info.IsDir() {
		return windowsSitePackages
	}

	venvLib := filepath.Join(venvDir, "lib")
	entries, err := os.ReadDir(venvLib)
	if err != nil {
		return ""
	}
	for _, entry := range entries {
		if entry.IsDir() && strings.HasPrefix(entry.Name(), "python") {
			return filepath.Join(venvLib, entry.Name(), "site-packages")
		}
	}
	return ""
}

func ensureManagedNodeModulesAccess(workDir, nodeModules string) func() {
	workDir = strings.TrimSpace(workDir)
	nodeModules = strings.TrimSpace(nodeModules)
	if workDir == "" || nodeModules == "" {
		return func() {}
	}

	if info, err := os.Stat(nodeModules); err != nil || !info.IsDir() {
		return func() {}
	}

	linkPath := filepath.Join(workDir, "node_modules")
	if _, err := os.Lstat(linkPath); err == nil || !os.IsNotExist(err) {
		return func() {}
	}

	if err := createManagedDirectoryLink(nodeModules, linkPath); err != nil {
		return func() {}
	}

	return func() {
		_ = os.Remove(linkPath)
	}
}

func createManagedDirectoryLink(target, link string) error {
	if runtime.GOOS == "windows" {
		cmd := exec.Command("cmd", "/C", "mklink", "/J", link, target)
		if output, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("create node_modules junction: %w: %s", err, strings.TrimSpace(string(output)))
		}
		return nil
	}

	return os.Symlink(target, link)
}

func combineCleanup(cleanups ...func()) func() {
	return func() {
		for _, cleanup := range cleanups {
			if cleanup != nil {
				cleanup()
			}
		}
	}
}

func sanitizeManagedPath(currentPath, nodeBin, venvBin string) string {
	cleanNodeBin := filepath.Clean(strings.TrimSpace(nodeBin))
	cleanVenvBin := filepath.Clean(strings.TrimSpace(venvBin))

	segments := make([]string, 0)
	seen := make(map[string]struct{})
	for _, item := range splitPathDirs(currentPath) {
		cleanItem := filepath.Clean(strings.TrimSpace(item))
		if cleanItem == "" || cleanItem == "." {
			continue
		}
		if cleanItem == cleanNodeBin || cleanItem == cleanVenvBin {
			continue
		}
		if _, exists := seen[cleanItem]; exists {
			continue
		}
		seen[cleanItem] = struct{}{}
		segments = append(segments, cleanItem)
	}

	return strings.Join(segments, string(os.PathListSeparator))
}

func splitPathDirs(raw string) []string {
	parts := strings.Split(raw, string(os.PathListSeparator))
	result := make([]string, 0, len(parts))
	for _, item := range parts {
		item = strings.TrimSpace(item)
		if item != "" {
			result = append(result, item)
		}
	}
	return result
}

func joinPathSegments(parts ...string) string {
	joined := make([]string, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))
	for _, part := range parts {
		for _, item := range splitPathDirs(part) {
			cleanItem := filepath.Clean(strings.TrimSpace(item))
			if cleanItem == "" || cleanItem == "." {
				continue
			}
			if _, exists := seen[cleanItem]; exists {
				continue
			}
			seen[cleanItem] = struct{}{}
			joined = append(joined, cleanItem)
		}
	}
	return strings.Join(joined, string(os.PathListSeparator))
}

func loadConfigShellVars(envMap map[string]string) {
	dataDir := ""
	if config.C != nil {
		dataDir = config.C.Data.Dir
	}
	if dataDir == "" {
		return
	}

	configPath := filepath.Join(dataDir, "config.sh")
	data, err := os.ReadFile(configPath)
	if err != nil {
		return
	}

	// config.sh 是用户可编辑文件，这里只解析变量赋值，不能通过 source 执行其中的 Shell 命令。
	content := strings.ReplaceAll(string(data), "\r\n", "\n")
	content = strings.ReplaceAll(content, "\r", "\n")
	configValues := make(map[string]string)
	pendingKey := ""
	pendingQuote := byte(0)
	var pendingValue strings.Builder

	findClosingQuote := func(value string, quote byte) int {
		for i := 0; i < len(value); i++ {
			if value[i] != quote {
				continue
			}
			if quote == '"' {
				backslashes := 0
				for j := i - 1; j >= 0 && value[j] == '\\'; j-- {
					backslashes++
				}
				if backslashes%2 == 1 {
					continue
				}
			}
			return i
		}
		return -1
	}

	for _, rawLine := range strings.Split(content, "\n") {
		trimmedLine := strings.TrimSpace(rawLine)
		if pendingKey != "" {
			// 未闭合值后如果遇到新的 export 赋值，放弃损坏项并从新变量继续解析。
			newExport := strings.HasPrefix(trimmedLine, "export ")
			if newExport {
				candidate := strings.TrimSpace(strings.TrimPrefix(trimmedLine, "export "))
				idx := strings.IndexByte(candidate, '=')
				newExport = idx > 0 && isValidShellEnvName(strings.TrimSpace(candidate[:idx]))
			}
			if newExport {
				pendingKey = ""
				pendingQuote = 0
				pendingValue.Reset()
			} else if idx := findClosingQuote(rawLine, pendingQuote); idx >= 0 {
				pendingValue.WriteByte('\n')
				pendingValue.WriteString(rawLine[:idx])
				configValues[pendingKey] = pendingValue.String()
				pendingKey = ""
				pendingQuote = 0
				pendingValue.Reset()
				continue
			} else {
				pendingValue.WriteByte('\n')
				pendingValue.WriteString(rawLine)
				continue
			}
		}

		if trimmedLine == "" || strings.HasPrefix(trimmedLine, "#") {
			continue
		}

		line := strings.TrimLeft(rawLine, " \t")
		if strings.HasPrefix(line, "export ") {
			line = strings.TrimLeft(strings.TrimPrefix(line, "export "), " \t")
		}
		idx := strings.IndexByte(line, '=')
		if idx <= 0 {
			continue
		}
		key := strings.TrimSpace(line[:idx])
		if key == "" {
			continue
		}

		value := strings.TrimLeft(line[idx+1:], " \t")
		if value == "" {
			configValues[key] = ""
			continue
		}
		if value[0] != '\'' && value[0] != '"' {
			configValues[key] = strings.Trim(strings.TrimSpace(value), "\"'")
			continue
		}

		quote := value[0]
		if closeAt := findClosingQuote(value[1:], quote); closeAt >= 0 {
			configValues[key] = value[1 : closeAt+1]
			continue
		}

		// 引号在本行未闭合时保留真实换行，直到后续行出现同类型闭合引号。
		pendingKey = key
		pendingQuote = quote
		pendingValue.Reset()
		pendingValue.WriteString(value[1:])
	}

	// 数据库环境变量来自面板页面，同名时优先级高于 config.sh。
	for key, value := range configValues {
		if _, exists := envMap[key]; exists {
			continue
		}
		envMap[key] = value
	}
}
