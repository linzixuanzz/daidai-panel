package service

import (
	"strings"
	"testing"
)

// 安装命令构造函数统一拒绝以 - 开头的包名：自动安装的包名来自脚本报错输出，可以被脚本伪造成选项。
func TestInstallCommandsRejectOptionLikePackageNames(t *testing.T) {
	for _, name := range []string{"--registry=https://evil.example.com", " -g", "-r"} {
		if _, err := NewNpmInstallCommand(name); err == nil {
			t.Fatalf("expected npm install to reject %q", name)
		}
		if _, err := NewPipInstallCommandForPythonVersion("", name); err == nil {
			t.Fatalf("expected pip install to reject %q", name)
		}
		if _, err := NewPipInstallCommandForPythonVersionWithFlags("", name, nil); err == nil {
			t.Fatalf("expected pip install with flags to reject %q", name)
		}
		result := InstallAutoDependency(&AutoInstallCandidate{Manager: "go", PackageName: name}, nil)
		if !strings.Contains(result.Error, "不能以 - 开头") {
			t.Fatalf("expected auto install to reject %q, got %#v", name, result)
		}
	}
}
