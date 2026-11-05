package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// #173 用户现场：群晖 DSM 6（内核 3.10）上 Alpine 镜像里的 git 拉库报的原文。
const gitOutputNoGetrandom = "Cloning into '/app/Dumb-Panel/scripts/someone_repo'...\n" +
	"error: unable to get random bytes for temporary file: Function not implemented\n" +
	"fatal: unable to write loose object file"

// stubGitGetrandomEnv 替换内核版本文件、os-release、CPU 架构与 Magisk 判断，用例结束后还原。
// kernel 为空表示内核版本文件读不到。
func stubGitGetrandomEnv(t *testing.T, kernel, osID, arch string, magisk bool) {
	t.Helper()
	dir := t.TempDir()

	oldKernel, oldOSRelease, oldArch, oldMagisk := kernelOSReleasePath, linuxOSReleaseFiles, gitHintRuntimeArch, gitHintIsMagiskModuleCheck
	t.Cleanup(func() {
		kernelOSReleasePath, linuxOSReleaseFiles, gitHintRuntimeArch, gitHintIsMagiskModuleCheck = oldKernel, oldOSRelease, oldArch, oldMagisk
	})

	kernelOSReleasePath = filepath.Join(dir, "missing-osrelease")
	if kernel != "" {
		kernelOSReleasePath = filepath.Join(dir, "osrelease")
		if err := os.WriteFile(kernelOSReleasePath, []byte(kernel+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	osRelease := filepath.Join(dir, "os-release")
	if err := os.WriteFile(osRelease, []byte("ID="+osID+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	linuxOSReleaseFiles = []string{osRelease}
	gitHintRuntimeArch = arch
	gitHintIsMagiskModuleCheck = func() bool { return magisk }
}

func TestClassifyGitFailureExplainsMissingGetrandom(t *testing.T) {
	cases := []struct {
		name    string
		kernel  string
		osID    string
		arch    string
		magisk  bool
		want    []string
		notWant []string
	}{
		{
			name:    "Alpine 镜像：叫人换 debian 镜像",
			kernel:  "3.10.105",
			osID:    "alpine",
			arch:    "amd64",
			want:    []string{"[提示] 宿主机内核 3.10.105 不支持 getrandom（Linux 3.17 起才有）", "Alpine 镜像里的 git 依赖它", "debian", "latest-full → debian-full", "或升级宿主系统"},
			notWant: []string{"面具模块"},
		},
		{
			name:    "不是 Alpine：只能升级宿主系统",
			kernel:  "3.10.105",
			osID:    "debian",
			arch:    "amd64",
			want:    []string{"[提示] 宿主机内核 3.10.105 不支持 getrandom（Linux 3.17 起才有），git 无法创建临时文件，请升级宿主系统。"},
			notWant: []string{"Alpine", "debian 镜像"},
		},
		{
			name:    "读不到内核版本：不写版本号",
			kernel:  "",
			osID:    "alpine",
			arch:    "arm64",
			want:    []string{"[提示] 宿主机内核不支持 getrandom", "debian"},
			notWant: []string{"宿主机内核  "},
		},
		{
			name:    "Alpine 但架构没有 Debian 版镜像：不叫人换镜像",
			kernel:  "3.10.108",
			osID:    "alpine",
			arch:    "arm",
			want:    []string{"3.10.108", "只有 amd64 / arm64", "当前架构 arm", "升级宿主系统"},
			notWant: []string{"latest → debian"},
		},
		{
			name:    "Magisk 模块版：改刷 Debian 版模块",
			kernel:  "3.10.84",
			osID:    "alpine",
			arch:    "arm64",
			magisk:  true,
			want:    []string{"3.10.84", "Debian 版面具模块"},
			notWant: []string{"镜像标签"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stubGitGetrandomEnv(t, tc.kernel, tc.osID, tc.arch, tc.magisk)

			got := classifyGitFailure(gitOutputNoGetrandom, errExitStatus128)
			if !strings.HasPrefix(got, "[提示] ") {
				t.Fatalf("应输出一行 [提示]，实际 %q", got)
			}
			if strings.Contains(got, "\n") {
				t.Fatalf("提示必须是单行（emit 按行输出）, got %q", got)
			}
			for _, keyword := range tc.want {
				if !strings.Contains(got, keyword) {
					t.Fatalf("提示缺少 %q\ngot: %s", keyword, got)
				}
			}
			for _, keyword := range tc.notWant {
				if strings.Contains(got, keyword) {
					t.Fatalf("提示不该出现 %q\ngot: %s", keyword, got)
				}
			}
		})
	}
}

// 大小写不同也要认出来；cmd.Output() 那条路（wrapGitCommandError）同样要带上提示。
func TestGetrandomHintMatchesCaseInsensitivelyAndInWrappedErrors(t *testing.T) {
	stubGitGetrandomEnv(t, "3.10.105", "alpine", "amd64", false)

	if !detectGitFailureSignals("ERROR: Unable To Get Random Bytes for temporary file").noGetrandom {
		t.Fatal("大小写不同也应识别出 getrandom 缺失")
	}

	err := wrapGitCommandError("读取远端配置", gitOutputNoGetrandom, errExitStatus128)
	if err == nil || !strings.Contains(err.Error(), "unable to get random bytes") || !strings.Contains(err.Error(), "[提示] 宿主机内核 3.10.105") {
		t.Fatalf("应保留原文并追加 getrandom 提示，实际 %v", err)
	}
}
