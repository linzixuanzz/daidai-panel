package service

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"daidai-panel/config"
	"daidai-panel/model"
)

func GitClone(url, branch, destDir string, sshKeyPath string) (string, error) {
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return "", fmt.Errorf("创建目录失败: %w", err)
	}

	args := []string{"clone", "--depth", "1"}
	if branch != "" {
		args = append(args, "-b", branch)
	}
	// `--` 之后按位置参数处理：地址以 - 开头时不会被 git 当成选项（如 --upload-pack=...）。
	args = append(args, "--", url, destDir)

	cmd := exec.Command("git", args...)
	cmd.Dir = config.C.Data.ScriptsDir

	authCfg, err := buildGitAuthConfig(os.Environ(), url, &model.Subscription{
		URL:      url,
		SSHKeyID: nil,
	}, sshKeyPath)
	if err != nil {
		return "", err
	}
	defer authCfg.CleanupFunc()
	cmd.Env = authCfg.Env

	output, err := cmd.CombinedOutput()
	return string(output), err
}

func GitPull(repoDir string, sshKeyPath string) (string, error) {
	cmd := exec.Command("git", "pull")
	cmd.Dir = repoDir

	authCfg, err := buildGitAuthConfig(os.Environ(), "", &model.Subscription{}, sshKeyPath)
	if err != nil {
		return "", err
	}
	defer authCfg.CleanupFunc()
	cmd.Env = authCfg.Env

	output, err := cmd.CombinedOutput()
	return string(output), err
}

func GitReset(repoDir string) (string, error) {
	cmd := exec.Command("git", "reset", "--hard")
	cmd.Dir = repoDir

	output, err := cmd.CombinedOutput()
	if err != nil {
		return string(output), err
	}

	cmd2 := exec.Command("git", "clean", "-fd")
	cmd2.Dir = repoDir
	output2, err2 := cmd2.CombinedOutput()
	return string(output) + "\n" + string(output2), err2
}

func IsGitRepo(dir string) bool {
	gitDir := filepath.Join(dir, ".git")
	info, err := os.Stat(gitDir)
	return err == nil && info.IsDir()
}

func DownloadFile(url, destPath string) (string, error) {
	return DownloadFileWithContext(context.Background(), url, destPath)
}

func DownloadFileWithContext(ctx context.Context, url, destPath string) (string, error) {
	dir := filepath.Dir(destPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("创建目录失败: %w", err)
	}

	// 订阅地址是用户填的，放在 `--` 之后：以 - 开头的地址不会被 curl / wget 当成选项（如 -o 改写输出位置）。
	args := []string{"-fsSL", "-o", destPath, "--", url}
	cmd := exec.CommandContext(ctx, "curl", args...)
	cmd.Env = AppendProxyEnv(os.Environ())
	output, err := cmd.CombinedOutput()
	if err != nil {
		args = []string{"-q", "-O", destPath, "--", url}
		cmd = exec.CommandContext(ctx, "wget", args...)
		cmd.Env = AppendProxyEnv(os.Environ())
		output, err = cmd.CombinedOutput()
	}
	return string(output), err
}

func ListRepoFiles(repoDir string) ([]string, error) {
	var files []string
	err := filepath.Walk(repoDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			if info.Name() == ".git" || info.Name() == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		rel, _ := filepath.Rel(repoDir, path)
		rel = strings.ReplaceAll(rel, "\\", "/")
		files = append(files, rel)
		return nil
	})
	return files, err
}
