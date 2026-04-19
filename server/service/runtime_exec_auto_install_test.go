package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"daidai-panel/testutil"
)

// TestBuildManagedRuntimeEnvMapDoesNotWritePythonPreCheckEnv 守卫：
// Python 预检自动安装链路已移除，不应再向任务环境写入这些已废弃的 env 键。
// 若将来有人把预检加回来，这个测试会立刻失败，提醒同时把 pysmx 漏判问题重新评估。
func TestBuildManagedRuntimeEnvMapDoesNotWritePythonPreCheckEnv(t *testing.T) {
	root := testutil.SetupTestEnv(t)

	envMap, err := BuildManagedRuntimeEnvMap(root, root, nil, time.Hour)
	if err != nil {
		t.Fatalf("build managed runtime env map: %v", err)
	}

	for _, key := range []string{"DD_AUTO_INSTALL_DEPS", "DD_PY_AUTO_INSTALL_ALIASES"} {
		if got, exists := envMap[key]; exists {
			t.Fatalf("expected %s to be absent, got %q", key, got)
		}
	}
}

func TestBuildManagedPythonPathPrioritizesWorkDirAndScriptsDir(t *testing.T) {
	got := buildManagedPythonPath(
		filepath.Clean("/custom/pythonpath"),
		filepath.Clean("/work/scripts/subdir"),
		filepath.Clean("/work/scripts"),
		filepath.Clean("/deps/python/venv/lib/python3.11/site-packages"),
	)

	parts := strings.Split(got, string(os.PathListSeparator))
	want := []string{
		filepath.Clean("/work/scripts/subdir"),
		filepath.Clean("/work/scripts"),
		filepath.Clean("/custom/pythonpath"),
		filepath.Clean("/deps/python/venv/lib/python3.11/site-packages"),
	}

	if len(parts) != len(want) {
		t.Fatalf("unexpected python path parts: got=%v want=%v", parts, want)
	}
	for idx, expected := range want {
		if parts[idx] != expected {
			t.Fatalf("python path order mismatch at %d: got=%q want=%q (all=%v)", idx, parts[idx], expected, parts)
		}
	}
}

func TestFindVenvSitePackagesSupportsWindowsLayout(t *testing.T) {
	venvDir := filepath.Join(t.TempDir(), "venv")
	sitePackages := filepath.Join(venvDir, "Lib", "site-packages")
	if err := os.MkdirAll(sitePackages, 0o755); err != nil {
		t.Fatalf("mkdir site-packages: %v", err)
	}

	if got := findVenvSitePackages(venvDir); got != sitePackages {
		t.Fatalf("expected windows site-packages path %q, got %q", sitePackages, got)
	}
}

func TestResolveManagedVenvBinUsesExistingScriptsDir(t *testing.T) {
	venvDir := filepath.Join(t.TempDir(), "venv")
	scriptsDir := filepath.Join(venvDir, "Scripts")
	if err := os.MkdirAll(scriptsDir, 0o755); err != nil {
		t.Fatalf("mkdir scripts dir: %v", err)
	}

	if got := resolveManagedVenvBin(venvDir); got != scriptsDir {
		t.Fatalf("expected Scripts dir %q, got %q", scriptsDir, got)
	}
}

// TestPythonBootstrapHasNoPreCheckAutoInstall 守卫：
// Python bootstrap 必须保持"纯跑脚本"语义，不做任何基于 importlib.find_spec 或
// AST 扫 import 的预检自动安装。历史上这套预检曾导致 pysmx 等已装好的包被反复
// 判定缺失并循环触发 pip install（v2.0.7 两次尝试修 find_spec 均未根治）。
// 真实缺失的依赖由 Go 侧 task_executor.detectAndInstallDeps 兜底处理，
// 它在脚本真实抛出 ModuleNotFoundError 时再 pip install + 自动重跑，更精准。
func TestPythonBootstrapHasNoPreCheckAutoInstall(t *testing.T) {
	forbidden := []struct {
		name string
		text string
	}{
		{"AST import scan", "_dd_scan_imports"},
		{"find_spec pre-check", "find_spec"},
		{"importlib.metadata fallback", "packages_distributions"},
		{"disk scan fallback", "_dd_module_available_on_disk"},
		{"pip install subprocess", "_dd_install_package"},
		{"auto install switch", "DD_AUTO_INSTALL_DEPS"},
		{"alias env", "DD_PY_AUTO_INSTALL_ALIASES"},
		{"missing dep banner", "检测到缺失依赖"},
	}
	for _, m := range forbidden {
		if strings.Contains(pythonEnvBootstrap, m.text) {
			t.Fatalf("pythonEnvBootstrap must not contain %s marker %q (预检链路已移除，改由 Go 侧后置兜底)", m.name, m.text)
		}
	}
}
