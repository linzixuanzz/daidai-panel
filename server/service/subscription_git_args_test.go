package service

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"daidai-panel/model"
	"daidai-panel/testutil"
)

// 用户填的分支名放在 `--` 之后：以 - 开头的分支名不能被 git fetch 当成选项。
// 用 --dry-run 当探针：被当成选项时 fetch 会「成功」且什么都不改，整次拉取照样成功；
// 当成 refspec 时 git 直接报 invalid refspec，拉取失败。同时验证正常的 clone / fetch 带上 `--` 之后照常工作。
func TestPullGitRepoDoesNotTreatBranchAsGitOption(t *testing.T) {
	root := testutil.SetupTestEnv(t)
	remoteDir := filepath.Join(root, "remote.git")
	worktreeDir := filepath.Join(root, "worktree")

	runGit(t, root, "init", "--bare", remoteDir)
	runGit(t, root, "clone", remoteDir, worktreeDir)
	if err := os.WriteFile(filepath.Join(worktreeDir, "a.js"), []byte("console.log('a')"), 0o644); err != nil {
		t.Fatalf("write repo file: %v", err)
	}
	runGit(t, worktreeDir, "add", "a.js")
	runGit(t, worktreeDir, "-c", "user.name=Test User", "-c", "user.email=test@example.com", "commit", "-m", "init")
	runGit(t, worktreeDir, "push", "origin", "HEAD:main")

	sub := &model.Subscription{Name: "args", Type: model.SubTypeGitRepo, URL: remoteDir, Branch: "main", SaveDir: "args-repo"}
	authCfg, err := buildGitAuthConfig(os.Environ(), sub.URL, sub, "")
	if err != nil {
		t.Fatalf("build git auth config: %v", err)
	}
	defer authCfg.CleanupFunc()

	// 第一次走 clone，第二次走 fetch，都要成功。
	for i := 0; i < 2; i++ {
		if output, err := pullGitRepoWithCallback(context.Background(), sub, authCfg, func(string) {}); err != nil {
			t.Fatalf("pull #%d failed: %v\n%s", i+1, err, output)
		}
	}

	sub.Branch = "--dry-run"
	if output, err := pullGitRepoWithCallback(context.Background(), sub, authCfg, func(string) {}); err == nil {
		t.Fatalf("expected option-like branch to be rejected by git as a refspec, got success:\n%s", output)
	}
}
