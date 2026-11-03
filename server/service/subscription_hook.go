package service

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"daidai-panel/config"
	"daidai-panel/model"
	"daidai-panel/pkg/pathutil"
)

const subscriptionHookTimeoutSeconds = 900

// runSubscriptionPreScriptIfConfigured 执行「拉取前指令」，在 git 拉取之前跑。
// 返回错误会让整次拉取中断并记为失败 —— 与「拉取后钩子」一致。
// 这么定是因为前置指令的典型用途是「准备环境 / 挂载目录 / 换源 / 生成凭据」，
// 它没跑成功还继续拉，拉下来的东西多半也是错的，静默继续只会更难查。
func runSubscriptionPreScriptIfConfigured(sub *model.Subscription, emit PullCallback) error {
	return runSubscriptionInlineScript(sub, sub.PreScript, "拉取前指令", "pre", emit)
}

func runSubscriptionHookIfConfigured(sub *model.Subscription, emit PullCallback) error {
	return runSubscriptionInlineScript(sub, sub.HookScript, "订阅钩子", "hook", emit)
}

// runSubscriptionInlineScript 是前置指令与拉取后钩子共用的执行体。
// label 用于日志里的中文提示，logPrefix 用于每行输出的前缀，便于在一次拉取日志里区分两段。
func runSubscriptionInlineScript(sub *model.Subscription, rawScript, label, logPrefix string, emit PullCallback) error {
	script := normalizeSubscriptionScriptPaths(sub, rawScript)
	if script == "" {
		return nil
	}

	// 首次拉取时订阅目录还不存在（前置指令尤其容易撞上），退回脚本根目录，
	// 保证指令总有一个确定的 cwd，而不是继承服务进程的当前目录。
	workDir := subscriptionWorkingDir(sub)
	if _, err := os.Stat(workDir); err != nil {
		workDir = config.C.Data.ScriptsDir
	}

	emit("[执行" + label + "]")
	err := RunInlineScript(script, workDir, buildSubscriptionHookEnv(sub, workDir), subscriptionHookTimeoutSeconds, func(line string) {
		emit("[" + logPrefix + "] " + line)
	})
	if err != nil {
		return fmt.Errorf("执行%s失败: %w", label, err)
	}

	emit("[" + label + "完成]")
	return nil
}

func buildSubscriptionHookEnv(sub *model.Subscription, workDir string) map[string]string {
	dataDir := strings.TrimSpace(config.C.Data.Dir)

	env := map[string]string{
		"SUB_ID":            strconv.FormatUint(uint64(sub.ID), 10),
		"SUB_NAME":          sub.Name,
		"SUB_TYPE":          sub.Type,
		"SUB_URL":           sub.URL,
		"SUB_BRANCH":        sub.Branch,
		"SUB_SAVE_DIR":      subscriptionSaveDir(sub),
		"SUB_DIR":           workDir,
		"SUB_WORK_DIR":      workDir,
		"SCRIPTS_DIR":       config.C.Data.ScriptsDir,
		"PANEL_DATA_DIR":    dataDir,
		"PANEL_SCRIPTS_DIR": config.C.Data.ScriptsDir,
	}

	// 青龙那批变量（QL_DIR / QL_BRANCH / QL_REPO_PATH / dir_repo / …）与任务执行链路
	// 共用同一个来源（见 qinglong_compat.go），保证「钩子里能用的」和「任务里能用的」是同一套。
	//
	// 这一整块原来只有一个 QL_DIR，且是一段「dataDir 末段叫 data 就取父目录」的启发式：
	// Docker 下（dataDir=/app/Dumb-Panel）算出 /app，钩子里的 $QL_DIR/data/repo 永远是死路径。
	// 只补 QL_DIR 还不够——docs/script-api.md §9 把这批变量整表下发给用户，
	// 用户照着在拉取后钩子里写 `find "$dir_repo" ...`，dir_repo 为空会展开成 `find ""`，
	// 钩子非零退出、整次订阅拉取被判失败。
	//
	// 用「不覆盖已有键」的写法：上面那些 SUB_*/SCRIPTS_DIR 是钩子的专属契约，优先级更高。
	for key, value := range buildQingLongCompatEnv() {
		if _, exists := env[key]; !exists {
			env[key] = value
		}
	}
	// 钩子里同样可能跑 Playwright（例如拉完仓库顺手装浏览器、做一次登录），浏览器目录要与任务看到的一致。
	// 钩子环境不含环境变量页的值，所以这里取 ResolvePlaywrightBrowsersPath（它已按任务的优先级查过环境变量页），
	// 而不是只补默认值。取到空串就不写：任务里拿到空串时 Playwright 同样按「没设」处理，两边一致。
	if _, exists := env[PlaywrightBrowsersPathEnv]; !exists {
		if value := ResolvePlaywrightBrowsersPath(); value != "" {
			env[PlaywrightBrowsersPathEnv] = value
		}
	}
	return env
}

func normalizeSubscriptionHookScript(sub *model.Subscription) string {
	return normalizeSubscriptionScriptPaths(sub, sub.HookScript)
}

// normalizeSubscriptionScriptPaths 把用户从青龙抄来的绝对路径改写成 $SUB_DIR，
// 前置指令与拉取后钩子共用（用户往往两处都直接粘贴青龙那套命令）。
func normalizeSubscriptionScriptPaths(sub *model.Subscription, rawScript string) string {
	hookScript := strings.TrimSpace(rawScript)
	if hookScript == "" {
		return ""
	}

	repoKey := deriveSubscriptionRepoKey(sub.URL)
	if repoKey == "" {
		return hookScript
	}

	replacements := []string{
		"$QL_DIR/data/repo/" + repoKey,
		"${QL_DIR}/data/repo/" + repoKey,
		"$QL_DIR/data/scripts/" + repoKey,
		"${QL_DIR}/data/scripts/" + repoKey,
		"%QL_DIR%\\data\\repo\\" + repoKey,
		"%QL_DIR%\\data\\scripts\\" + repoKey,
	}

	normalized := hookScript
	for _, from := range replacements {
		normalized = strings.ReplaceAll(normalized, from, "$SUB_DIR")
	}
	return normalized
}

func deriveSubscriptionRepoKey(rawURL string) string {
	trimmed := strings.TrimSpace(rawURL)
	trimmed = strings.TrimSuffix(trimmed, "/")
	if trimmed == "" {
		return ""
	}

	trimmed = strings.TrimSuffix(trimmed, ".git")
	parts := strings.Split(trimmed, "/")
	if len(parts) >= 2 {
		owner := strings.TrimSpace(parts[len(parts)-2])
		repo := strings.TrimSpace(parts[len(parts)-1])
		if owner != "" && repo != "" {
			return owner + "_" + repo
		}
	}

	if len(parts) > 0 {
		return strings.TrimSpace(parts[len(parts)-1])
	}
	return ""
}

func subscriptionWorkingDir(sub *model.Subscription) string {
	saveDir := subscriptionSaveDir(sub)
	if sub.Type == model.SubTypeSingleFile && saveDir == "" {
		saveDir = "downloads"
	}
	if saveDir == "" {
		return config.C.Data.ScriptsDir
	}
	workDir := filepath.Join(config.C.Data.ScriptsDir, saveDir)
	// 保存目录越界（存量脏数据）时不把 cwd 放到脚本目录外，退回脚本根目录。
	// 正常拉取流程在 checkSubscriptionPullTarget 就已经失败，这里只是同一套口径的兜底。
	if absWorkDir, err := filepath.Abs(workDir); err != nil || !pathutil.IsWithinBase(config.C.Data.ScriptsDir, absWorkDir) {
		return config.C.Data.ScriptsDir
	}
	return workDir
}
