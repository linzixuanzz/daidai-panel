package service

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"daidai-panel/config"
	"daidai-panel/database"
	"daidai-panel/model"
	"daidai-panel/testutil"
)

// 保存目录：越界写法一律拒绝，正常的多级子目录、中文目录照常可用。
func TestValidateSubscriptionSaveDirRejectsEscapes(t *testing.T) {
	rejected := []string{
		"../x",
		"..\\x",
		"a/../../x",
		"libs/..",
		"..",
		"...",
		"/etc",
		"\\etc",
		"\\\\server\\share",
		"C:/Windows",
		"c:\\x",
		"D:",
		"a\nb",
		"a\x00b",
		"a\tb",
	}
	for _, value := range rejected {
		if err := ValidateSubscriptionSaveDir(value); err == nil {
			t.Fatalf("expected save_dir %q to be rejected", value)
		}
	}

	accepted := []string{"", "jd", "libs/utils", "libs\\utils", "中文目录", "订阅/京东", "a/./b", "my.repo", "v1..2"}
	for _, value := range accepted {
		if err := ValidateSubscriptionSaveDir(value); err != nil {
			t.Fatalf("expected save_dir %q to be accepted, got %v", value, err)
		}
	}
}

// 别名：单文件订阅里是文件名，带路径分隔符或是 . / .. 都拒绝；git 订阅里按目录规则。
func TestValidateSubscriptionAliasByType(t *testing.T) {
	for _, value := range []string{"a/b.js", "a\\b.js", "../evil.js", ".", "..", "x\r\n.js"} {
		if err := ValidateSubscriptionAlias(model.SubTypeSingleFile, value); err == nil {
			t.Fatalf("expected single-file alias %q to be rejected", value)
		}
	}
	for _, value := range []string{"", "daily_report.py", "中文脚本.js", ".env.js"} {
		if err := ValidateSubscriptionAlias(model.SubTypeSingleFile, value); err != nil {
			t.Fatalf("expected single-file alias %q to be accepted, got %v", value, err)
		}
	}

	for _, value := range []string{"../repo", "/abs", "C:\\repo"} {
		if err := ValidateSubscriptionAlias(model.SubTypeGitRepo, value); err == nil {
			t.Fatalf("expected git alias %q to be rejected", value)
		}
	}
	for _, value := range []string{"repo", "group/repo", "仓库"} {
		if err := ValidateSubscriptionAlias(model.SubTypeGitRepo, value); err != nil {
			t.Fatalf("expected git alias %q to be accepted, got %v", value, err)
		}
	}
}

// git 订阅的最终目录不能是脚本根目录：URL 以 / 结尾、保存目录填 . 都会推导成根目录。
func TestValidateGitSubscriptionTargetDirRejectsScriptsRoot(t *testing.T) {
	cases := []struct {
		url, saveDir, alias string
	}{
		{url: "https://github.com/owner/repo/"},
		{url: "https://github.com/owner/repo.git", saveDir: "."},
		{url: "https://github.com/owner/repo.git", saveDir: "a/.."},
		{url: "https://github.com/owner/repo.git", alias: "./"},
		{url: "https://example.com/.."},
	}
	for _, tc := range cases {
		if err := ValidateSubscriptionTarget(model.SubTypeGitRepo, tc.url, tc.saveDir, tc.alias); err == nil {
			t.Fatalf("expected git target %#v to be rejected", tc)
		}
	}

	if err := ValidateSubscriptionTarget(model.SubTypeGitRepo, "https://github.com/owner/repo.git", "", ""); err != nil {
		t.Fatalf("expected repo-name derived dir to be accepted, got %v", err)
	}
	// 单文件订阅可以直接存到脚本根目录。
	if err := ValidateSubscriptionTarget(model.SubTypeSingleFile, "https://example.com/raw/x.js", ".", ""); err != nil {
		t.Fatalf("expected single-file subscription in scripts root to be accepted, got %v", err)
	}
}

// 存量脏数据（绕过 handler 直接在库里的越界保存目录）：拉取失败、日志写清原因、脚本目录外什么都没写出来。
func TestPullSubscriptionRejectsEscapingGitSaveDir(t *testing.T) {
	testutil.SetupTestEnv(t)
	scriptsDir := config.C.Data.ScriptsDir

	sub := &model.Subscription{
		Name:    "escape-git",
		Type:    model.SubTypeGitRepo,
		URL:     "https://example.invalid/owner/repo.git",
		SaveDir: "../escaped-repo",
		Enabled: true,
	}
	if err := database.DB.Create(sub).Error; err != nil {
		t.Fatalf("create subscription: %v", err)
	}

	var logs []string
	_, err := PullSubscriptionWithContext(context.Background(), sub, func(line string) { logs = append(logs, line) })
	if err == nil || !strings.Contains(err.Error(), subscriptionPathEscapeHint) {
		t.Fatalf("expected escape error, got %v", err)
	}
	if !strings.Contains(strings.Join(logs, "\n"), subscriptionPathEscapeHint) {
		t.Fatalf("expected pull log to mention escape hint, got %#v", logs)
	}
	if _, statErr := os.Stat(filepath.Join(scriptsDir, "..", "escaped-repo")); !os.IsNotExist(statErr) {
		t.Fatalf("expected nothing written outside scripts dir, got err=%v", statErr)
	}
}

func TestPullSubscriptionRejectsEscapingSingleFileAlias(t *testing.T) {
	testutil.SetupTestEnv(t)
	scriptsDir := config.C.Data.ScriptsDir

	sub := &model.Subscription{
		Name:    "escape-file",
		Type:    model.SubTypeSingleFile,
		URL:     "https://example.invalid/raw/x.js",
		SaveDir: "downloads",
		Alias:   "../../evil.js",
		Enabled: true,
	}
	if err := database.DB.Create(sub).Error; err != nil {
		t.Fatalf("create subscription: %v", err)
	}

	_, err := PullSubscriptionWithContext(context.Background(), sub, func(string) {})
	if err == nil || !strings.Contains(err.Error(), subscriptionPathEscapeHint) {
		t.Fatalf("expected escape error, got %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(scriptsDir, "..", "evil.js")); !os.IsNotExist(statErr) {
		t.Fatalf("expected no file written outside scripts dir, got err=%v", statErr)
	}
	// 越界检查发生在下载之前：连 downloads 目录都不该被建出来。
	if _, statErr := os.Stat(filepath.Join(scriptsDir, "downloads")); !os.IsNotExist(statErr) {
		t.Fatalf("expected download dir not prepared, got err=%v", statErr)
	}
}

// URL 以 / 结尾、保存目录和别名都空：推导出来就是脚本根目录，拉取必须拒绝，不能在根目录 git init。
func TestPullGitRepoRejectsScriptsRootTarget(t *testing.T) {
	testutil.SetupTestEnv(t)
	scriptsDir := config.C.Data.ScriptsDir
	if err := os.WriteFile(filepath.Join(scriptsDir, "user.js"), []byte("console.log('mine')"), 0o644); err != nil {
		t.Fatalf("write user script: %v", err)
	}

	sub := &model.Subscription{Name: "root", Type: model.SubTypeGitRepo, URL: "https://example.invalid/owner/repo/"}
	authCfg, err := buildGitAuthConfig(os.Environ(), sub.URL, sub, "")
	if err != nil {
		t.Fatalf("build git auth config: %v", err)
	}
	defer authCfg.CleanupFunc()

	if _, err := pullGitRepoWithCallback(context.Background(), sub, authCfg, func(string) {}); err == nil {
		t.Fatalf("expected pull into scripts root to be rejected")
	}
	if IsGitRepo(scriptsDir) {
		t.Fatalf("expected scripts root not to become a git repo")
	}
	if _, err := os.Stat(filepath.Join(scriptsDir, "user.js")); err != nil {
		t.Fatalf("expected user script kept, got %v", err)
	}
}

// 保存目录是指向脚本目录外的符号链接时同样算越界。
func TestPullGitRepoRejectsSymlinkEscape(t *testing.T) {
	root := testutil.SetupTestEnv(t)
	scriptsDir := config.C.Data.ScriptsDir
	outside := filepath.Join(root, "outside")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatalf("create outside dir: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(scriptsDir, "linked")); err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("symlink not available on this Windows host: %v", err)
		}
		t.Fatalf("create symlink: %v", err)
	}

	sub := &model.Subscription{Name: "linked", Type: model.SubTypeGitRepo, URL: "https://example.invalid/owner/repo.git", SaveDir: "linked"}
	if _, err := resolveGitSubscriptionDestDir(scriptsDir, sub); err == nil || !strings.Contains(err.Error(), subscriptionPathEscapeHint) {
		t.Fatalf("expected symlink escape to be rejected, got %v", err)
	}
}

// 正常子目录、中文目录在拉取时照常解析，路径公式与原来一致。
func TestResolveSubscriptionDestAcceptsNormalDirs(t *testing.T) {
	testutil.SetupTestEnv(t)
	scriptsDir := config.C.Data.ScriptsDir

	for _, saveDir := range []string{"libs/utils", "中文目录"} {
		sub := &model.Subscription{Type: model.SubTypeGitRepo, URL: "https://example.invalid/owner/repo.git", SaveDir: saveDir}
		got, err := resolveGitSubscriptionDestDir(scriptsDir, sub)
		if err != nil {
			t.Fatalf("expected %q accepted, got %v", saveDir, err)
		}
		want, _ := filepath.Abs(filepath.Join(scriptsDir, saveDir))
		if got != want {
			t.Fatalf("unexpected dest dir: want %q got %q", want, got)
		}
	}

	single := &model.Subscription{Type: model.SubTypeSingleFile, URL: "https://example.invalid/raw/x.js", SaveDir: "报表", Alias: "日报.py"}
	got, err := resolveSingleFileSubscriptionDestPath(scriptsDir, single)
	if err != nil {
		t.Fatalf("expected single-file dest accepted, got %v", err)
	}
	if want := singleFileSubscriptionDestPath(scriptsDir, single); got != want {
		t.Fatalf("unexpected single-file dest: want %q got %q", want, got)
	}
}
