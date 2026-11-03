package service

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"daidai-panel/config"
	"daidai-panel/database"
	"daidai-panel/model"
	"daidai-panel/pkg/cron"

	"gorm.io/gorm"
)

type PullCallback func(line string)

func PullSubscription(sub *model.Subscription) (string, error) {
	return PullSubscriptionWithCallback(sub, nil)
}

func PullSubscriptionWithCallback(sub *model.Subscription, onOutput PullCallback) (string, error) {
	return PullSubscriptionWithContext(context.Background(), sub, onOutput)
}

func PullSubscriptionWithContext(ctx context.Context, sub *model.Subscription, onOutput PullCallback) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	startTime := time.Now()

	var sshKeyPath string
	if sub.SSHKeyID != nil {
		var sshKey model.SSHKey
		if err := database.DB.First(&sshKey, *sub.SSHKeyID).Error; err == nil {
			tmpFile, err := writeTempSSHKey(sshKey.PrivateKey)
			if err != nil {
				return "", fmt.Errorf("写入 SSH 密钥失败: %w", err)
			}
			defer os.Remove(tmpFile)
			sshKeyPath = tmpFile
		}
	}
	authCfg, err := buildGitAuthConfig(os.Environ(), sub.URL, sub, sshKeyPath)
	if err != nil {
		return "", err
	}
	defer authCfg.CleanupFunc()

	var fullLog strings.Builder
	emit := func(line string) {
		fullLog.WriteString(line)
		fullLog.WriteString("\n")
		if onOutput != nil {
			onOutput(line)
		}
	}

	emit(fmt.Sprintf("[开始拉取] %s (%s)", sub.Name, sub.Type))

	var output string
	var pullErr error

	// 落盘路径先过越界检查：保存目录 / 别名的存量脏数据（如 ../xxx）在这里就让整次拉取失败，
	// 拉取前指令、拉取、钩子、任务同步都不跑。拉取函数内部还会再查一次（拉取前指令可能改了目录）。
	pullErr = checkSubscriptionPullTarget(sub)

	// 拉取前指令跑在真正拉取之前，失败就不往下走了（详见 runSubscriptionPreScriptIfConfigured）。
	if pullErr == nil {
		pullErr = runSubscriptionPreScriptIfConfigured(sub, emit)
	}
	if pullErr == nil && ctx.Err() != nil {
		pullErr = fmt.Errorf("拉取已停止")
	}

	if pullErr == nil {
		switch sub.Type {
		case model.SubTypeSingleFile:
			output, pullErr = pullSingleFileWithCallback(ctx, sub, sshKeyPath, emit)
		default:
			output, pullErr = pullGitRepoWithCallback(ctx, sub, authCfg, emit)
		}
	}

	if pullErr == nil && ctx.Err() != nil {
		pullErr = fmt.Errorf("拉取已停止")
	}
	if pullErr == nil {
		pullErr = runSubscriptionHookIfConfigured(sub, emit)
	}
	if pullErr == nil && ctx.Err() != nil {
		pullErr = fmt.Errorf("拉取已停止")
	}
	if pullErr == nil {
		syncSubscriptionTasks(sub, emit)
	}

	duration := time.Since(startTime).Seconds()

	status := 0
	if pullErr != nil {
		status = 1
		emit(fmt.Sprintf("[错误] %s", pullErr.Error()))
	}

	emit(fmt.Sprintf("[完成] 耗时 %.2f 秒, 状态: %s", duration, map[int]string{0: "成功", 1: "失败"}[status]))

	subLog := model.SubLog{
		SubscriptionID: sub.ID,
		Status:         status,
		Content:        fullLog.String(),
		Duration:       duration,
	}
	database.DB.Create(&subLog)

	now := time.Now()
	database.DB.Model(sub).Updates(map[string]interface{}{
		"last_pull_at": &now,
		"status":       status,
	})

	return output, pullErr
}

// resolveSubscriptionForceOverwrite 解析这次拉取到底覆盖还是保留本地。
// 只读不写：v2.2.15 到 v3.0.9 之间这里是个「覆盖器」，每次拉取开头就把全局值盖进 sub.ForceOverwrite，
// 订阅自己存的值从此再没生效过；而且盖的那个 sub 稍后还会被 database.DB.Model(sub).Updates(...) 用到，
// 顺手也是个隐患。现在改成纯解析，sub 对象一个字段都不动。
//
// 三态优先级：订阅显式选了 force / preserve 就以订阅为准，inherit（含空串、脏值）才回落全局开关。
// 刻意不写成 `sub.X || isConfigEnabled(...)` 那种 OR —— OR 表达不了「全局开着、但这个订阅强制关」，
// 而这正是本功能的核心诉求。v3.2.6 起「自动添加定时任务 / 自动删除失效任务」也从 OR 换成了同一形状，
// 见下面的 resolveSubscriptionAutoAddTask / resolveSubscriptionAutoDelTask。
func resolveSubscriptionForceOverwrite(sub *model.Subscription) bool {
	if sub != nil {
		switch model.NormalizeSubscriptionOverwriteMode(sub.OverwriteMode) {
		case model.SubOverwriteForce:
			return true
		case model.SubOverwritePreserve:
			return false
		}
	}
	// inherit（含空串、脏值、sub 为 nil）才回落全局开关，顺带避免另外两档白读一次配置
	return isConfigEnabled("subscription_force_overwrite", true)
}

// resolveSubscriptionAutoAddTask / resolveSubscriptionAutoDelTask 解析这次同步到底建不建任务、删不删任务。
// 与上面的覆盖拉取解析器同形：纯解析，sub 对象一个字段都不写回
// （sub 稍后还会被 database.DB.Model(sub).Updates(...) 用到，就地改写既掩盖个体值也埋回写隐患）。
//
// v3.2.6 之前这两项是 `sub.AutoAddTask || isConfigEnabled("auto_add_cron", true)` 的 OR：
// 全局默认 true，订阅那一列几乎恒等于空转；更要命的是 OR 永远表达不出
// 「全局开着、但这一条订阅别建任务」——那正是用户提的需求。现在换成三态解析：
// 订阅显式选了 enabled / disabled 就以订阅为准，inherit（含空串、脏值、sub 为 nil）才回落全局默认。
func resolveSubscriptionAutoAddTask(sub *model.Subscription) bool {
	if sub != nil {
		switch model.NormalizeSubscriptionTaskSyncMode(sub.AutoAddTaskMode) {
		case model.SubTaskSyncEnabled:
			return true
		case model.SubTaskSyncDisabled:
			return false
		}
	}
	return isConfigEnabled("auto_add_cron", true)
}

func resolveSubscriptionAutoDelTask(sub *model.Subscription) bool {
	if sub != nil {
		switch model.NormalizeSubscriptionTaskSyncMode(sub.AutoDelTaskMode) {
		case model.SubTaskSyncEnabled:
			return true
		case model.SubTaskSyncDisabled:
			return false
		}
	}
	return isConfigEnabled("auto_del_cron", true)
}

// subscriptionTaskSyncStrategyLabel 把三态开关翻译成日志里的人话，
// 让「我明明在订阅里关了怎么还在建任务」这类问题一眼能自查（同拉取日志里的「策略：X」）。
func subscriptionTaskSyncStrategyLabel(mode string) string {
	switch model.NormalizeSubscriptionTaskSyncMode(mode) {
	case model.SubTaskSyncEnabled:
		return "强制开启"
	case model.SubTaskSyncDisabled:
		return "强制关闭"
	default:
		return "跟随全局设置"
	}
}

func runCmdWithCallback(ctx context.Context, cmd *exec.Cmd, emit PullCallback) (string, error) {
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		return "", err
	}
	cmd.Stderr = cmd.Stdout

	if err := cmd.Start(); err != nil {
		return "", err
	}

	var buf strings.Builder
	scanner := bufio.NewScanner(pipe)
	scanner.Buffer(make([]byte, 64*1024), 256*1024)
	for scanner.Scan() {
		line := scanner.Text()
		buf.WriteString(line)
		buf.WriteString("\n")
		emit(line)
	}
	if scanErr := scanner.Err(); scanErr != nil {
		if ctx != nil && ctx.Err() != nil {
			return buf.String(), fmt.Errorf("拉取已停止")
		}
		return buf.String(), scanErr
	}

	err = cmd.Wait()
	if ctx != nil && ctx.Err() != nil {
		return buf.String(), fmt.Errorf("拉取已停止")
	}
	if err != nil {
		// clone / fetch / checkout / reset / sparse-checkout 全部走这一个出口，
		// 所以错误识别也统一放这里：调用方拿到的只有 `exit status 128`，
		// 真正的原因在刚刚 emit 出去的那几行 fatal 里。这里紧跟着追加一条
		// 中文提示，让「原因」和「原始输出」在日志里挨着，原始输出一行不删。
		if hint := classifyGitFailure(buf.String(), err); hint != "" {
			emit(hint)
		}
	}
	return buf.String(), err
}

func gitHasWorkingTreeChanges(ctx context.Context, repoDir string, env []string) (bool, error) {
	cmd := exec.CommandContext(ctx, "git", "status", "--porcelain", "--untracked-files=all")
	cmd.Dir = repoDir
	cmd.Env = env

	output, err := cmd.Output()
	if err != nil {
		if ctx != nil && ctx.Err() != nil {
			return false, fmt.Errorf("拉取已停止")
		}
		// 这条命令用的是 cmd.Output()，stderr 不进日志流；直接抛 err 的话
		// 用户只能看到 `exit status 128`，所以把 stderr 原文一并带上。
		return false, wrapGitCommandError("检查本地改动", gitCommandStderr(err), err)
	}

	return strings.TrimSpace(string(output)) != "", nil
}

func pullGitRepoWithCallback(ctx context.Context, sub *model.Subscription, authCfg gitAuthConfig, emit PullCallback) (string, error) {
	saveDir := subscriptionSaveDir(sub)

	// 目录公式与 subscriptionSaveDir 一致，另外挡住越界和「就是脚本根目录」两种情况：
	// 下面的 reset --hard / clean -fd 落在错的目录上会直接毁掉别处的文件。
	destDir, err := resolveGitSubscriptionDestDir(config.C.Data.ScriptsDir, sub)
	if err != nil {
		return "", err
	}
	env := authCfg.Env

	if IsGitRepo(destDir) {
		var fullOutput strings.Builder
		branchLabel := "默认分支"
		if strings.TrimSpace(sub.Branch) != "" {
			branchLabel = strings.TrimSpace(sub.Branch)
		}

		// 措辞必须中性：这条 emit 在覆盖策略判定之前、无条件执行，preserve 的订阅也会打出来。
		// 原文写死「覆盖更新本地文件」，于是保留本地修改的订阅日志会先说要覆盖、再说在保留，自相矛盾；
		// 而用户搜关键字或截图报障时最先命中的恰恰就是这一句，加了策略标签反而多一处矛盾。
		// 真正的策略由下面 forceOverwrite 分支各自 emit（都带「策略：X」）。
		emit(fmt.Sprintf("[检测到已有仓库] %s 已存在 Git 仓库，接下来会同步远端并按覆盖策略更新本地文件", saveDir))
		emit(fmt.Sprintf("[同步远端地址] 正在校正订阅地址 -> %s", authCfg.DisplayURL))
		output, err := syncGitRemoteWithCallback(ctx, destDir, authCfg.RemoteURL, env, emit)
		fullOutput.WriteString(output)
		if err != nil {
			return fullOutput.String(), err
		}

		// 分支名是用户填的，放在 `--` 之后：以 - 开头的值（如 --upload-pack=...）不会被 git 当成选项。
		fetchArgs := []string{"fetch", "--depth", "1", "--prune", "--", "origin"}
		if strings.TrimSpace(sub.Branch) != "" {
			fetchArgs = append(fetchArgs, strings.TrimSpace(sub.Branch))
		}
		emit(fmt.Sprintf("[拉取远端更新] 正在获取分支 %s 的最新提交", branchLabel))
		cmd := exec.CommandContext(ctx, "git", fetchArgs...)
		cmd.Dir = destDir
		cmd.Env = env
		output, err = runCmdWithCallback(ctx, cmd, emit)
		fullOutput.WriteString(output)
		if err != nil {
			return fullOutput.String(), err
		}

		if err := applySparseCheckout(ctx, destDir, sub, env, emit); err != nil {
			return fullOutput.String(), err
		}

		// 策略来源一并写进日志：用户在订阅里单独设过就显示「强制覆盖 / 保留本地修改」，
		// 没设过就显示「跟随全局设置」，方便自查「我明明关了怎么还在覆盖」这类问题。
		forceOverwrite := resolveSubscriptionForceOverwrite(sub)
		strategyLabel := "跟随全局设置"
		switch model.NormalizeSubscriptionOverwriteMode(sub.OverwriteMode) {
		case model.SubOverwriteForce:
			strategyLabel = "强制覆盖"
		case model.SubOverwritePreserve:
			strategyLabel = "保留本地修改"
		}
		if forceOverwrite {
			emit(fmt.Sprintf("[覆盖更新本地文件] 正在用远端最新提交覆盖当前订阅目录中的仓库内容（策略：%s）", strategyLabel))
			cmd = exec.CommandContext(ctx, "git", "reset", "--hard", "FETCH_HEAD")
			cmd.Dir = destDir
			cmd.Env = env
			output, err = runCmdWithCallback(ctx, cmd, emit)
			fullOutput.WriteString(output)
			if err != nil {
				return fullOutput.String(), err
			}
			emit("[已完成] 已覆盖更新所有仓库文件，本地新增的文件已保留")
		} else {
			emit(fmt.Sprintf("[保留本地修改] 正在合并远端更新（保留本地修改的文件，策略：%s）", strategyLabel))
			hasStash, err := gitHasWorkingTreeChanges(ctx, destDir, env)
			if err != nil {
				return fullOutput.String(), err
			}
			if hasStash {
				cmd = exec.CommandContext(ctx, "git", "stash", "push", "--include-untracked", "-m", "daidai-panel-subscription-update")
				cmd.Dir = destDir
				cmd.Env = env
				output, err = runCmdWithCallback(ctx, cmd, emit)
				fullOutput.WriteString(output)
				if err != nil {
					return fullOutput.String(), err
				}
			} else {
				emit("[保留本地修改] 未检测到本地改动，跳过暂存恢复")
			}

			cmd = exec.CommandContext(ctx, "git", "reset", "--hard", "FETCH_HEAD")
			cmd.Dir = destDir
			cmd.Env = env
			output, err = runCmdWithCallback(ctx, cmd, emit)
			fullOutput.WriteString(output)
			if err != nil {
				return fullOutput.String(), err
			}

			if hasStash {
				emit("[恢复本地修改] 正在恢复之前暂存的本地修改")
				cmd = exec.CommandContext(ctx, "git", "stash", "pop")
				cmd.Dir = destDir
				cmd.Env = env
				output, err = runCmdWithCallback(ctx, cmd, emit)
				fullOutput.WriteString(output)
				if err != nil {
					emit("[提示] 本地修改与远端更新存在冲突，请手动处理")
				}
			}
		}
		return fullOutput.String(), err
	}

	if destInfo, err := os.Stat(destDir); err == nil {
		if !destInfo.IsDir() {
			return "", fmt.Errorf("保存目录已被文件占用: %s", saveDir)
		}

		entries, readErr := os.ReadDir(destDir)
		if readErr != nil {
			return "", fmt.Errorf("读取保存目录失败: %w", readErr)
		}
		if len(entries) > 0 {
			var fullOutput strings.Builder
			branchLabel := "默认分支"
			if strings.TrimSpace(sub.Branch) != "" {
				branchLabel = strings.TrimSpace(sub.Branch)
			}

			emit(fmt.Sprintf("[检测到已存在脚本目录] %s 当前不是 Git 仓库，接下来会原地初始化仓库并覆盖本地文件", saveDir))
			emit("[git init] 正在初始化本地仓库")
			cmd := exec.CommandContext(ctx, "git", "init")
			cmd.Dir = destDir
			cmd.Env = env
			output, err := runCmdWithCallback(ctx, cmd, emit)
			fullOutput.WriteString(output)
			if err != nil {
				return fullOutput.String(), err
			}

			emit(fmt.Sprintf("[同步远端地址] 正在校正订阅地址 -> %s", authCfg.DisplayURL))
			output, err = syncGitRemoteWithCallback(ctx, destDir, authCfg.RemoteURL, env, emit)
			fullOutput.WriteString(output)
			if err != nil {
				return fullOutput.String(), err
			}

			// 同上：分支名放在 `--` 之后，防止被当成 git 选项。
			fetchArgs := []string{"fetch", "--depth", "1", "--prune", "--", "origin"}
			if strings.TrimSpace(sub.Branch) != "" {
				fetchArgs = append(fetchArgs, strings.TrimSpace(sub.Branch))
			}
			emit(fmt.Sprintf("[拉取远端更新] 正在获取分支 %s 的最新提交", branchLabel))
			cmd = exec.CommandContext(ctx, "git", fetchArgs...)
			cmd.Dir = destDir
			cmd.Env = env
			output, err = runCmdWithCallback(ctx, cmd, emit)
			if err != nil {
				fullOutput.WriteString(output)
				return fullOutput.String(), err
			}
			fullOutput.WriteString(output)
			if ctx.Err() != nil {
				return fullOutput.String(), fmt.Errorf("拉取已停止")
			}

			if err := applySparseCheckout(ctx, destDir, sub, env, emit); err != nil {
				return fullOutput.String(), err
			}

			emit("[覆盖更新本地文件] 正在用远端最新提交覆盖当前脚本目录内容")
			cmd = exec.CommandContext(ctx, "git", "reset", "--hard", "FETCH_HEAD")
			cmd.Dir = destDir
			cmd.Env = env
			output, err = runCmdWithCallback(ctx, cmd, emit)
			fullOutput.WriteString(output)
			if err != nil {
				return fullOutput.String(), err
			}

			emit("[清理多余文件] 正在移除原脚本目录中不属于远端仓库的旧文件")
			cmd = exec.CommandContext(ctx, "git", "clean", "-fd")
			cmd.Dir = destDir
			cmd.Env = env
			output, err = runCmdWithCallback(ctx, cmd, emit)
			fullOutput.WriteString(output)
			if err != nil {
				return fullOutput.String(), err
			}

			emit("[已完成] 已覆盖更新所有仓库文件，并清理原目录中的多余旧文件")
			return fullOutput.String(), nil
		}
	}

	emit(fmt.Sprintf("[git clone] %s -> %s", authCfg.DisplayURL, saveDir))
	os.MkdirAll(destDir, 0755)
	args := []string{"clone", "--depth", "1"}
	// 告警**不能**在这里丢掉：applySparseCheckout 是唯一 emit 它们的地方，
	// 而下面 len(sparsePatterns) > 0 不成立时（完整检出、或规则全空）那一整段会被跳过 ——
	// 于是首次 clone 这条路径上一条提示都打不出来，用户第一次看到「整仓文件全落盘、
	// 白名单像是失效了」时反而没有任何线索，第二次增量拉取才会有。
	sparsePatterns, sparseWarnings := buildSubscriptionSparseCheckoutPatterns(sub)
	if len(sparsePatterns) == 0 {
		for _, warning := range sparseWarnings {
			emit(warning)
		}
	}
	if len(sparsePatterns) > 0 {
		// 有指定子目录/白名单时，先不检出工作区，避免 clone 阶段把整个仓库文件落盘。
		// --filter=blob:none 对 GitHub 这类支持 partial clone 的远端能少下载无关 blob；
		// 不支持的远端会退化为普通浅克隆，但工作区仍只会检出匹配路径。
		args = append(args, "--filter=blob:none", "--no-checkout")
	}
	if sub.Branch != "" {
		args = append(args, "-b", sub.Branch)
	}
	// `--` 之后的都按位置参数处理：地址以 - 开头时不会被 git 当成选项（如 --upload-pack=...）。
	args = append(args, "--", authCfg.RemoteURL, destDir)
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = config.C.Data.ScriptsDir
	cmd.Env = env
	output, err := runCmdWithCallback(ctx, cmd, emit)
	if err != nil {
		return output, err
	}
	if len(sparsePatterns) > 0 {
		var fullOutput strings.Builder
		fullOutput.WriteString(output)
		if spErr := applySparseCheckout(ctx, destDir, sub, env, emit); spErr != nil {
			return fullOutput.String(), spErr
		}
		emit("[checkout] 正在按子目录/白名单规则检出订阅文件")
		cmd = exec.CommandContext(ctx, "git", "checkout", "HEAD")
		cmd.Dir = destDir
		cmd.Env = env
		output, err = runCmdWithCallback(ctx, cmd, emit)
		fullOutput.WriteString(output)
		return fullOutput.String(), err
	}
	return output, nil
}

// subscriptionSparseUnsafeChars 列出会让 sparse-checkout「静默少匹配」的 gitignore 元字符。
//
// `git sparse-checkout set --no-cone` 用的是 gitignore 语法：`?` 匹配任意单字符、
// `[...]` 是字符类、`\` 是转义符。路径里若含这些字符，git 会按通配语义解释而不是字面量，
// 结果往往是「一个文件都没检出、且完全不报错」——本类 bug 最难排查的形态。
//
// #129 之后它只管「指定子目录」：白名单 / 黑名单 / 依赖规则里含这些字符的片段一律按正则识别
// （它们都在正则触发字符里，见 subscriptionRegexTriggerChars），另走「整仓检出 + Go 侧按正则匹配」。
//
// `*` 刻意不在此列：它只会放宽匹配、不会导致漏检出，方向是安全的。
// `|` 也不在此列：它在 gitignore 里就是普通字符，拆分阶段已经被当作分隔符拆掉了。
const subscriptionSparseUnsafeChars = "?[]\\"

// subscriptionSparseUnsafeCharsHint 是给用户看的可读版本（日志里直接打元字符会糊成一团）。
const subscriptionSparseUnsafeCharsHint = "? [ ] \\"

// splitSubscriptionSparseTargets 把「指定子目录」拆成两组：
// 能安全下发给 sparse-checkout 的路径，和含 gitignore 元字符、下发后会静默失配的路径。
// 子目录是精确路径字段、不参与 #129 的正则识别，所以用的是改造前的拆分器（splitSubscriptionPlainPatterns）。
func splitSubscriptionSparseTargets(raw string) (safe []string, risky []string) {
	for _, p := range splitSubscriptionPlainPatterns(raw) {
		p = normalizeSubscriptionFilterTarget(p)
		if p == "" || isWildcardFilterPattern(p) {
			continue
		}
		if strings.ContainsAny(p, subscriptionSparseUnsafeChars) {
			risky = append(risky, p)
			continue
		}
		safe = append(safe, p)
	}
	return safe, risky
}

func formatSubscriptionPatternList(patterns []string) string {
	quoted := make([]string, 0, len(patterns))
	for _, p := range patterns {
		quoted = append(quoted, "`"+p+"`")
	}
	return strings.Join(quoted, " / ")
}

// formatSubscriptionUndeclaredCronFiles 给「没识别到 cron 声明、未建任务」的提示列文件名：最多 5 个，多了用「如 … 等」带过
// （总数已经写在提示开头）。
func formatSubscriptionUndeclaredCronFiles(files []string) string {
	const limit = 5
	if len(files) > limit {
		return "如 " + strings.Join(files[:limit], "、") + " 等"
	}
	return strings.Join(files, "、")
}

// formatSubscriptionFileList 把文件列表压成一行日志；超过 limit 只列前 limit 个并带上总数，
// 避免大仓库把整个拉取日志刷爆。
func formatSubscriptionFileList(files []string, limit int) string {
	if limit > 0 && len(files) > limit {
		return strings.Join(files[:limit], ", ") + fmt.Sprintf(" ...（共 %d 个）", len(files))
	}
	return strings.Join(files, ", ")
}

// buildSubscriptionSparseCheckoutPatterns 返回下发给 git sparse-checkout 的规则，
// 以及需要打给用户看的告警（调用方负责 emit）。
//
// 白名单 / 黑名单 / 依赖规则里的正则片段（#129，见 subscription_patterns.go）表达不成 gitignore 规则，
// 统一按「档 1」处理：
//   - 包含侧出现正则片段（白名单含正则，或包含侧已有限制时依赖规则含正则）→ 放弃包含侧的 sparse 限制、
//     检出完整仓库，由 Go 侧匹配器决定哪些是依赖文件、哪些建任务（只剩黑名单时照旧用 `*` + 排除规则）；
//   - 黑名单的正则片段不下发排除规则，文件照常落盘，由 Go 侧按正则排除（不建任务）；
//   - 编译失败的正则片段逐条跳过并告警。
//
// 普通片段生成的规则与改造前逐字节一致（subscription_pattern_pin_test.go 钉着）。
func buildSubscriptionSparseCheckoutPatterns(sub *model.Subscription) (patterns []string, warnings []string) {
	if sub == nil {
		return nil, nil
	}

	whitelist := compileSubscriptionPatternSet(sub.Whitelist, subscriptionPatternWhitelist)
	blacklist := compileSubscriptionPatternSet(sub.Blacklist, subscriptionPatternBlacklist)
	depend := compileSubscriptionPatternSet(sub.DependOn, subscriptionPatternDepend)

	// 完整检出：用户明确要整个仓库（青龙生态里那些要读仓库 src/ 自行编译的脚本）。
	// 返回空规则之后有两处顺带生效，不用再各写一遍开关：
	//   1. applySparseCheckout 看到空规则会把已有的 sparse-checkout 关掉，
	//      所以「先开着 sparse 拉过、再打开这个开关」也能自动恢复成完整仓库；
	//   2. pullGitRepoWithCallback 首次 clone 那条 --filter=blob:none --no-checkout
	//      分支的条件是 len(sparsePatterns) > 0，这里返回空自然就跳过了。
	// 白名单/黑名单仍然照常参与「建不建定时任务」的筛选，只是不再限制落盘范围。
	//
	// 这里必须带一条告警回去：用户同时填了指定子目录/白名单/黑名单又打开完整检出时，
	// 看到的现象是整仓文件全落盘、那些规则像是失效了；一声不吭的话没人能把现象和开关对上。
	// 与本函数其它几条「退回完整检出」的分支保持同一套做法（见函数末尾调用方的注释：
	// 这类问题的杀伤力全在「静默」）。
	//
	// 但三项都留空时**不打这条提示**：那种订阅本来就是整仓检出（下面几条分支会自己说明），
	// 开不开这个开关结果完全一样。无条件打的话，用户会照着提示去设置里找一个
	// 自己从来没配过的白名单，或误以为曾经配过、被这个开关吞掉了。
	if sub.FullCheckout {
		// 非法正则照样要点名：完整检出只放开落盘范围，白 / 黑名单、依赖规则仍然决定建不建任务。
		// 没有非法片段时 invalid 为 nil，下面两个返回值与改造前完全一样。
		invalid := subscriptionInvalidPatternWarnings(&whitelist, &blacklist, &depend)
		if strings.TrimSpace(sub.SubPath) == "" &&
			strings.TrimSpace(sub.Whitelist) == "" &&
			strings.TrimSpace(sub.Blacklist) == "" {
			return nil, invalid
		}
		return nil, append([]string{
			"[提示] 已按订阅设置检出整个仓库：指定子目录/白名单/黑名单本次都不限制落盘范围，但仍然照常决定给哪些脚本建定时任务",
		}, invalid...)
	}

	seen := map[string]bool{}
	addPattern := func(pattern string) {
		pattern = strings.TrimSpace(filepath.ToSlash(pattern))
		pattern = strings.TrimPrefix(pattern, "./")
		pattern = strings.TrimPrefix(pattern, "/")
		if pattern == "" || seen[pattern] {
			return
		}
		seen[pattern] = true
		patterns = append(patterns, pattern)
	}

	// addFragmentPatterns 为一个「子串包含」片段成对下发两条 sparse 规则：
	//
	//	**/*x*      名字里含 x 的文件，以及名为 *x* 的目录条目本身
	//	**/*x*/**   名为 *x* 的目录下的全部内容（含多级子目录）
	//
	// 为什么必须成对：sparse-checkout 用的是 gitignore 语法，而 gitignore 里的 `*`
	// **不跨 `/`**。片段 `utils` 只包成 `**/*utils*` 时，它能命中 `sendNotify.js`
	// 这种「名字里含片段」的文件、也能命中 `utils` 这个目录条目本身，但命不中
	// `utils/date.js`——`*utils*` 跨不过那个 `/`。反过来只留 `**/*utils*/**` 又会丢掉
	// 根目录下的单文件。两条缺一不可。
	//
	// 排除侧（黑名单）同样成对下发，理由不是「对称好看」，是不成对会真的变得更糟：
	//   - git 的 sparse-checkout 是「最后匹配者胜出」，且父目录整体未命中时子项会继承
	//     未命中。`jd_dir/backUp/old.js` 现在正是靠这条继承被挡住的。
	//   - 一旦包含侧多了 `**/*jd_*/**` 这种直接命中子文件的规则，非递归的
	//     `!**/*backUp*`（只匹配到 backUp 目录条目本身）就压不住它了，本来挡得住的
	//     文件反而会落盘。
	//   - Go 侧 checkBlacklist 对完整相对路径做子串包含（正则片段按正则），本来就是递归语义；
	//     递归排除只是让 git 侧与它对齐，不会多挡任何「本来会被建成定时任务」的文件。
	addFragmentPatterns := func(fragment string, exclude bool) {
		prefix := ""
		if exclude {
			prefix = "!"
		}
		addPattern(prefix + "**/*" + fragment + "*")
		addPattern(prefix + "**/*" + fragment + "*/**")
	}

	subPaths, unsafeSubPaths := splitSubscriptionSparseTargets(sub.SubPath)
	plan := resolveSubscriptionIncludePlan(subPaths, unsafeSubPaths, &whitelist)

	// 包含侧（指定子目录 / 白名单）是「或」语义：只跳过其中一条表达不了的子模式，
	// 会让那条本该命中的文件静默检不出来，用户看到的还是「拉取成功但任务是空的」。
	// 所以子目录含 git 元字符、或白名单含正则片段时，都整体放弃包含侧的 sparse 限制、改为检出完整仓库，
	// 再交给 Go 侧的匹配器（subscriptionFilterMatcher）决定给哪些脚本建任务。
	// 宁可多落几个文件，也不要静默丢文件。
	switch plan {
	case subscriptionIncludeUnsafeSubPath:
		warnings = append(warnings, fmt.Sprintf(
			"[警告] 指定子目录 %s 含 git 通配特殊字符（%s），无法安全转成 sparse-checkout 规则；本次改为检出完整仓库，请改用不含这些字符的普通路径片段",
			formatSubscriptionPatternList(unsafeSubPaths), subscriptionSparseUnsafeCharsHint))
	case subscriptionIncludeSubPath:
		// 指定子目录优先级最高：它代表用户明确只想要仓库里的某几个目录/文件。
		//
		// 这里刻意**不**走 addFragmentPatterns：子目录填的是明确路径（`scripts/daily`），
		// 直接作为 gitignore 模式下发时会精确命中那个目录条目，git 的
		// clear_ce_flags_dir 再把「命中」向下继承给目录里的全部文件，语义已经完整。
		// 若也包成 `**/*scripts/daily*` 反而会把它从「精确路径」变成「子串包含」，
		// 与该字段既有的语义不符。
		for _, p := range subPaths {
			addPattern(p)
		}
	case subscriptionIncludeWhitelistRegex:
		warnings = append(warnings, fmt.Sprintf(
			"[提示] 白名单 %s 含正则片段，git sparse-checkout 表达不了正则；本次检出完整仓库，建任务时再由面板按白名单筛选（普通片段按子串包含，正则片段按正则匹配仓库相对路径）",
			formatSubscriptionPatternList(whitelist.regexFragments())))
	case subscriptionIncludeWhitelist:
		// 没有指定子目录时，才用白名单限制真实检出的文件范围。
		// 白名单的普通片段是「完整相对路径的子串包含匹配」（见 matchesSubscriptionWhitelist），
		// 所以片段命中目录名时，目录里的文件也算命中——成对下发递归规则才对得上。
		for _, p := range whitelist.sparseTargets() {
			addFragmentPatterns(p, false)
		}
	}
	// 编译失败的白名单片段逐条跳过（它们本来就一个文件都匹配不到）。子目录优先时白名单不参与检出，
	// 但仍然决定建不建任务，所以不论走哪条分支都要点名。
	if warning := whitelist.invalidWarning(); warning != "" {
		warnings = append(warnings, warning)
	}

	// 依赖规则并进「包含侧」：命中的文件会被检出落盘（主脚本 require 的辅助库），
	// 但 Go 侧的任务候选筛选仍然只认白名单，所以它们不会被建成定时任务，
	// 见 isSubscriptionDependencyOnlyFile。按指定子目录检出时还有子目录护栏：子目录外的依赖文件一律不建任务
	// （白名单留空时 isSubscriptionDependencyOnlyFile 一个都摘不掉，全靠护栏，见 newSubscriptionFilterMatcher）。
	//
	// 守卫 `plan.restrictsCheckout()`（改造前写作 `len(patterns) > 0`，两者等价）是这里最关键的一处，
	// 去掉会直接把订阅打空：
	//   - 上面的 switch 退回完整检出时 patterns 为空 → 本来就全量落盘，
	//     依赖规则天然满足；此时若追加依赖模式，sparse-checkout 反而会被激活成
	//     「只检出依赖文件」，白名单文件全丢。
	//   - 用户既没填子目录也没填白名单时 patterns 同样为空 → 本来就检出全部文件
	//     （白名单为空 = 全部文件都算命中白名单），追加依赖模式一样会把「全量」
	//     缩成「只有依赖」，主脚本反而没了。
	if plan.restrictsCheckout() {
		if depend.hasRegex() {
			// 档 1：依赖规则的正则片段表达不成 sparse 规则。只跳过它会「少落一个辅助文件」——
			// 这正是 #129 要修的问题（主脚本 require('./jdCookie') 一跑就报找不到模块），
			// 所以改为放弃包含侧的 sparse 限制、检出完整仓库，由 Go 侧按依赖规则认出依赖文件（不建任务）。
			// 建任务的集合不因此变大：子目录有护栏（newSubscriptionFilterMatcher 的 subPathScope），
			// 白名单照常过滤，兜底 #2 也按放宽前的范围判断（见 scanSubscriptionTaskCandidates）。
			patterns = nil
			seen = map[string]bool{}
			scopeNote := ""
			if plan == subscriptionIncludeSubPath {
				scopeNote = "；指定子目录本次不再限制落盘范围，但仍然只给子目录里的脚本建定时任务"
			}
			warnings = append(warnings, fmt.Sprintf(
				"[提示] 依赖规则 %s 含正则片段，git sparse-checkout 表达不了正则；本次检出完整仓库，由面板按依赖规则识别依赖文件（依赖文件照旧不建定时任务）%s",
				formatSubscriptionPatternList(depend.regexFragments()), scopeNote))
		} else {
			dependPatterns := depend.sparseTargets()
			// 依赖片段最典型的写法就是目录名（青龙那条真实指令里的 `utils`），
			// 主脚本 require('./utils/xxx') 要的是目录里的文件而不是目录条目本身，
			// 所以这里同样成对下发递归规则。
			for _, p := range dependPatterns {
				addFragmentPatterns(p, false)
			}
			if len(dependPatterns) > 0 {
				taskScope := "只有命中白名单的文件才建任务"
				if plan == subscriptionIncludeSubPath {
					taskScope = "只给指定子目录里命中白名单的脚本建任务"
				}
				warnings = append(warnings, fmt.Sprintf(
					"[依赖规则] %s 已并入检出范围：命中的文件会被拉取到脚本目录供主脚本调用，但不会建成定时任务（%s）",
					formatSubscriptionPatternList(dependPatterns), taskScope))
			}
		}
		if dependNotes := depend.notes(); len(dependNotes) > 0 {
			warnings = append(warnings, fmt.Sprintf(
				"[提示] 依赖规则中的 %s 含空格/中文或过长，已按文字备注跳过、未参与文件检出；依赖规则现在是功能性字段，请填写文件名片段（多个用 `,` 或 `|` 分隔；普通片段按「子串包含」匹配，含 %s 的片段按正则匹配）",
				formatSubscriptionPatternList(dependNotes), subscriptionRegexTriggerHint))
		}
	} else if strings.TrimSpace(sub.DependOn) != "" {
		warnings = append(warnings, "[提示] 未配置指定子目录/白名单（或包含侧已退回完整检出），本次检出完整仓库，依赖规则无需额外生效")
	}
	// 依赖规则里编译失败的片段逐条跳过：只会「少认一个依赖文件」，方向安全，但必须打出来——
	// 静默少一个 jdCookie.js，用户看到的是任务跑起来才报错。
	if warning := depend.invalidWarning(); warning != "" {
		warnings = append(warnings, warning)
	}

	// 黑名单是「排除」语义：正则片段表达不成排除规则，跳过它只会让对应文件多落一份盘，
	// Go 侧的 checkBlacklist 会按正则把它们挡在定时任务之外，方向是安全的，逐条跳过即可。
	// （改造前这里的承诺并不成立：`back[Uu]p` 在 Go 侧被当成字面量，一个文件都挡不住。）
	if regexBlacklist := blacklist.regexFragments(); len(regexBlacklist) > 0 {
		warnings = append(warnings, fmt.Sprintf(
			"[提示] 黑名单 %s 含正则片段，git sparse-checkout 表达不了正则，已跳过对应的排除规则；这些文件仍会落盘，但面板会按正则把它们排除在定时任务之外",
			formatSubscriptionPatternList(regexBlacklist)))
	}
	if warning := blacklist.invalidWarning(); warning != "" {
		warnings = append(warnings, warning)
	}

	blacklistTargets := blacklist.sparseTargets()
	if len(blacklistTargets) == 0 {
		// 包含侧被迫放弃、又没有可用排除规则时 patterns 为空，
		// 等价于「不做任何过滤」，直接返回空让调用方关掉 sparse-checkout。
		return patterns, warnings
	}

	// 只有黑名单（或包含侧被迫放弃）时先包含全部，再用 !pattern 排除，
	// 避免"黑名单目录"也落到 scripts 里。
	if len(patterns) == 0 {
		addPattern("*")
	}
	// 排除规则必须排在包含规则之后：sparse-checkout 是「最后匹配者胜出」，
	// 只有这样 `!**/*backUp*/**` 才能压住前面 `**/*jd_*/**` 对 backUp/jd_old.js 的命中。
	for _, p := range blacklistTargets {
		addFragmentPatterns(p, true)
	}

	return patterns, warnings
}

func applySparseCheckout(ctx context.Context, repoDir string, sub *model.Subscription, env []string, emit PullCallback) error {
	patterns, warnings := buildSubscriptionSparseCheckoutPatterns(sub)
	// 先把告警打出来：这类问题的杀伤力全在「静默」，哪怕后面走了兜底也要让用户看见。
	for _, warning := range warnings {
		emit(warning)
	}
	if len(patterns) == 0 {
		// 用户清空子目录/白名单后，要把之前的 sparse-checkout 关掉，
		// 否则旧过滤规则会一直残留，导致后续看起来"仓库文件丢了"。
		cmd := exec.CommandContext(ctx, "git", "config", "--bool", "core.sparseCheckout")
		cmd.Dir = repoDir
		cmd.Env = env
		output, err := cmd.Output()
		if err == nil && strings.TrimSpace(string(output)) == "true" {
			emit("[sparse-checkout] 当前未配置子目录/白名单，正在恢复完整仓库检出")
			cmd = exec.CommandContext(ctx, "git", "sparse-checkout", "disable")
			cmd.Dir = repoDir
			cmd.Env = env
			if _, runErr := runCmdWithCallback(ctx, cmd, emit); runErr != nil {
				return fmt.Errorf("关闭 sparse-checkout 失败: %w", runErr)
			}
		}
		return nil
	}

	emit(fmt.Sprintf("[sparse-checkout] 设置订阅路径过滤（共 %d 条）: %s", len(patterns), strings.Join(patterns, ", ")))

	cmd := exec.CommandContext(ctx, "git", "sparse-checkout", "init", "--no-cone")
	cmd.Dir = repoDir
	cmd.Env = env
	if _, err := runCmdWithCallback(ctx, cmd, emit); err != nil {
		return fmt.Errorf("sparse-checkout init 失败: %w", err)
	}

	args := append([]string{"sparse-checkout", "set", "--no-cone"}, patterns...)
	cmd = exec.CommandContext(ctx, "git", args...)
	cmd.Dir = repoDir
	cmd.Env = env
	if _, err := runCmdWithCallback(ctx, cmd, emit); err != nil {
		return fmt.Errorf("sparse-checkout set 失败: %w", err)
	}

	return nil
}

// singleFileSubscriptionTarget 返回单文件订阅的保存子目录与文件名：
// SaveDir 为空用 downloads，Alias 为空用 URL 末段。
func singleFileSubscriptionTarget(sub *model.Subscription) (saveDir, filename string) {
	saveDir = sub.SaveDir
	if saveDir == "" {
		saveDir = "downloads"
	}

	parts := strings.Split(sub.URL, "/")
	filename = parts[len(parts)-1]
	if sub.Alias != "" {
		filename = sub.Alias
	}
	return saveDir, filename
}

// singleFileSubscriptionDestPath 是单文件订阅下载目标路径的唯一公式。
// 拉取（pullSingleFileWithCallback）与「删除任务时一并删除脚本」判断「这个文件是不是某个订阅下载的」
// 都走这里；两边口径一旦分叉，被订阅每次重新下载的文件就会被当成普通脚本删掉。
func singleFileSubscriptionDestPath(scriptsDir string, sub *model.Subscription) string {
	saveDir, filename := singleFileSubscriptionTarget(sub)
	return filepath.Join(scriptsDir, saveDir, filename)
}

func pullSingleFileWithCallback(ctx context.Context, sub *model.Subscription, _ string, emit PullCallback) (string, error) {
	saveDir, filename := singleFileSubscriptionTarget(sub)
	// 目标路径公式不变（singleFileSubscriptionDestPath），只多一道越界检查：
	// 别名 / 保存目录带 ../ 的存量数据会把文件写到脚本目录外，这里直接让拉取失败。
	destPath, err := resolveSingleFileSubscriptionDestPath(config.C.Data.ScriptsDir, sub)
	if err != nil {
		return "", err
	}
	emit(fmt.Sprintf("[下载] %s -> %s/%s", sub.URL, saveDir, filename))
	output, err := DownloadFileWithContext(ctx, sub.URL, destPath)
	if output != "" {
		emit(output)
	}
	return output, err
}

func syncGitRemoteWithCallback(ctx context.Context, repoDir, remoteURL string, env []string, emit PullCallback) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "remote")
	cmd.Dir = repoDir
	cmd.Env = env

	remoteOutput, err := cmd.Output()
	if err != nil {
		if ctx != nil && ctx.Err() != nil {
			return "", fmt.Errorf("拉取已停止")
		}
		// 同 gitHasWorkingTreeChanges：cmd.Output() 的 stderr 不会 emit 出去，
		// 不带上原文的话用户只能看到一句退出码。
		return "", wrapGitCommandError("读取远端配置", gitCommandStderr(err), err)
	}

	args := []string{"remote", "add", "origin", remoteURL}
	for _, name := range strings.Fields(string(remoteOutput)) {
		if name == "origin" {
			args = []string{"remote", "set-url", "origin", remoteURL}
			break
		}
	}

	cmd = exec.CommandContext(ctx, "git", args...)
	cmd.Dir = repoDir
	cmd.Env = env
	return runCmdWithCallback(ctx, cmd, emit)
}

func writeTempSSHKey(privateKey string) (string, error) {
	tmpFile, err := os.CreateTemp("", "ssh_key_*")
	if err != nil {
		return "", err
	}
	defer tmpFile.Close()

	if _, err := tmpFile.WriteString(privateKey); err != nil {
		os.Remove(tmpFile.Name())
		return "", err
	}

	os.Chmod(tmpFile.Name(), 0600)
	return tmpFile.Name(), nil
}

var (
	// 兼容多种 cron 声明前缀：
	//   cron: 30 8 * * *
	//   # cron: 30 8 * * *
	//   #cron 8 9,10,11 * * *
	//   cron 0 12 * * *
	//   * cron 8 10 * * *           (JSDoc 块注释每行的 `*` 前缀)
	//   * cron: 12 8 * * *
	//   @cron: 30 8 * * *           (JSDoc `@cron` 标签)
	//   * @cron 0 0 * * *
	//   // cron: 0 0 * * *
	// 通过 `\b` 词界避免误匹配 `crontab` / `cron-utils` 等关键字。
	// 第 1 组是 cron 前面的注释标记与缩进（判断值带引号时能不能剥，见 extractSubscriptionCronExpressionFromLabel），第 2 组是值。
	cronLabelPrefixRe      = regexp.MustCompile(`(?im)^([\s#*@/]*)@?cron\b\s*[:：]?\s*(\S.*)$`)
	subscriptionTaskNameRe = regexp.MustCompile(`new\s+Env\s*\(\s*['"` + "`" + `]([^'"` + "`" + `]+)['"` + "`" + `]\s*\)`)
	// 注释头里的任务名声明，覆盖用户实际在用的几种写法：
	//   //name: 远程开机        // name: 雀巢会员      （js / mjs / ts）
	//   #name: X               ## name: DDNS IP更新   （sh / py）
	//   * name: X              @name: X               （JSDoc 块注释 / 标签）
	//   <!-- name: X -->                              （html）
	// 刻意**要求**行首带注释标记，不接受裸 `name: xxx`：JS/TS 里 `{ name: 'foo' }`
	// 这种对象字面量太常见，裸匹配会把它误判成任务名。cron 那边敢接受裸写法，
	// 是因为还有 cron.Parse 兜底校验，而 name 没有可校验的形态，只能靠注释标记收窄。
	subscriptionTaskNameLabelRe = regexp.MustCompile(`(?i)^\s*(?:<!--|//+|#+|\*+|--+|@)\s*@?name\s*[:：]\s*(\S.*)$`)
	// 青龙风格 `cron "EXPR" filename, tag:xxx` 单行声明，常见于 JS 顶部注释。
	// 例如：cron "6 6 6 6 *" jd_CheckCK.js, tag:京东CK检测by-ccwav
	cronDirectiveLineRe = regexp.MustCompile(`(?i)\bcron\s+["']([^"'\n\r]+)["']\s+([^\s,;]+)`)
)

type subscriptionTaskSyncOptions struct {
	autoAdd    bool
	autoDelete bool
	// defaultCron：订阅设置里的默认 Cron 规则，脚本没识别到 cron 声明时使用；空串 = 这类脚本不建任务（#134）。
	defaultCron string
	// ignoredDefaultCron：库里存着、但 cron.Parse 不认而被忽略的默认规则原值（直接改库、恢复旧备份才会有），只给拉取日志用。
	ignoredDefaultCron string
	allowedExts        map[string]bool
}

type subscriptionTaskCandidate struct {
	Name           string
	Command        string
	CronExpression string
	// DefaultRule：脚本没声明 cron，CronExpression 来自默认 Cron 规则。只用于日志分开计数与标明来源。
	DefaultRule bool
}

func subscriptionTaskLabel(subID uint) string {
	return fmt.Sprintf("subscription:%d", subID)
}

func hasLabel(labels []string, target string) bool {
	for _, item := range labels {
		if item == target {
			return true
		}
	}
	return false
}

// hasLabelFold：labels 里有没有与 target 忽略大小写相同的项。给订阅标签判定用——SQLite 的 LIKE 对 ASCII
// 不分大小写（queryTasksByLabel），usedByOtherSubscription / withoutLabel 与它对齐，否则用户手写成
// Subscription:2 这类大小写差异会误删别的订阅在用的任务，或者每次同步空转一次「[解除关联]」。
func hasLabelFold(labels []string, target string) bool {
	for _, item := range labels {
		if strings.EqualFold(item, target) {
			return true
		}
	}
	return false
}

func withLabel(labels []string, target string) []string {
	if hasLabel(labels, target) {
		return labels
	}
	return append(labels, target)
}

// withoutLabel 是 withLabel 的反操作：去掉所有与 target 忽略大小写相同（strings.EqualFold）的项，其余按原顺序保留。
// 忽略大小写是为了与 queryTasksByLabel 的 LIKE 口径一致：用户把订阅标签手写成 Subscription:2 时，这里也能把它摘掉，
// 不会写回原值造成每次同步空转。
func withoutLabel(labels []string, target string) []string {
	kept := make([]string, 0, len(labels))
	for _, item := range labels {
		if !strings.EqualFold(item, target) {
			kept = append(kept, item)
		}
	}
	return kept
}

func subscriptionSaveDir(sub *model.Subscription) string {
	saveDir := sub.SaveDir
	if saveDir == "" {
		saveDir = sub.Alias
		if saveDir == "" {
			parts := strings.Split(sub.URL, "/")
			saveDir = strings.TrimSuffix(parts[len(parts)-1], ".git")
		}
	}
	return saveDir
}

// isWildcardFilterPattern 判断"用户填的 pattern 是不是通配符"——
// 如 `*`、`**`、`*.*`、`.*`、`/`、`all`。这些显然是用户想"全部放行"的意图，
// 但旧逻辑用 strings.Contains 字面匹配 → 全部不匹配 → 全部文件被过滤掉。
// 现在视为"等价于不填"。
func isWildcardFilterPattern(p string) bool {
	p = strings.TrimSpace(p)
	if p == "" {
		return true
	}
	switch strings.ToLower(p) {
	case "*", "**", "*.*", ".*", "/", "all", "any", "全部":
		return true
	}
	return false
}

func normalizeSubscriptionFilterTarget(value string) string {
	value = strings.TrimSpace(filepath.ToSlash(value))
	value = strings.TrimPrefix(value, "./")
	value = strings.TrimPrefix(value, "/")
	return value
}

func subscriptionFilterContains(target string, pattern string) bool {
	target = normalizeSubscriptionFilterTarget(target)
	pattern = normalizeSubscriptionFilterTarget(pattern)
	if target == "" || pattern == "" {
		return false
	}
	return strings.Contains(target, pattern)
}

// splitSubscriptionFilterPatterns 把「白名单 / 黑名单 / 依赖规则」这三个过滤字段
// 拆成一组独立模式（去重、保持顺序）。分隔符同时接受 `,` 和 `|`。
//
// 为什么必须认 `|`：用户最主要的配置来源是青龙的 `ql repo` 命令，它的第 2/3/4 个
// 位置参数是 `grep -E` 模式，天然用 `|` 分隔：
//
//	ql repo https://github.com/6dylan6/jdpro.git "jd_|jx_|jddj_" "backUp" "..."
//
// 旧实现只按 `,` 拆 → 整串 `jd_|jx_|jddj_` 被当成一个模式，三条链路同时失效：
//  1. buildSubscriptionSparseCheckoutPatterns 生成 `**/*jd_|jx_|jddj_*`，
//     而 gitignore 语法里 `|` 只是普通字符不是「或」→ sparse-checkout 检出 0 个文件；
//  2. matchesSubscriptionWhitelist 做 strings.Contains(路径, "jd_|jx_|jddj_") → 恒 false；
//  3. checkBlacklist 同理。
//
// 表现就是「git 拉取成功、日志没有任何报错、但扫描 0 个候选文件、一个定时任务都没建」。
//
// #129 起拆分器只在「顶层」拆（有配对的括号里、方括号表达式里、被 `\` 转义的分隔符不拆，见
// splitSubscriptionPatternSegments），让 `(jd|jx)_`、`a{1,3}` 这类正则片段不再被拆坏；
// 不含 ( [ { \ 的输入与改造前逐字节一致。空段照旧丢弃：`jd_||jx_`、`|jd_|` 这类首尾/连续分隔符很常见，
// 而空模式会让 subscriptionFilterContains 恒 false，也会让 sparse-checkout 生成 `**/**` 这种含义跑偏的规则。
//
// 注意：正则只对「含正则触发字符」的片段生效（见 subscription_patterns.go），普通片段仍是子串包含。
// 贸然把整个字段当正则，会让现存配置里含 `.` `*` `+` 的普通子串（a.b、jd_*.js、c++）行为突变。
func splitSubscriptionFilterPatterns(raw string) []string {
	var patterns []string
	seen := make(map[string]bool)
	for _, pattern := range splitSubscriptionPatternSegments(raw) {
		if seen[pattern] {
			continue
		}
		seen[pattern] = true
		patterns = append(patterns, pattern)
	}
	return patterns
}

// splitSubscriptionPlainPatterns 是改造前的拆分器：在每一个 `,` `|` 处拆，不看括号与转义。
// 只给「指定子目录」用——它是精确路径字段、不参与正则识别，行为必须原样保留。
func splitSubscriptionPlainPatterns(raw string) []string {
	var patterns []string
	seen := make(map[string]bool)
	for _, pattern := range strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == '|'
	}) {
		pattern = strings.TrimSpace(pattern)
		if pattern == "" || seen[pattern] {
			continue
		}
		seen[pattern] = true
		patterns = append(patterns, pattern)
	}
	return patterns
}

func hasNonWildcardSubscriptionFilter(raw string) bool {
	set := compileSubscriptionPatternSet(raw, subscriptionPatternWhitelist)
	return set.hasNonWildcard()
}

// matchesSubscriptionWhitelist 是单文件判定（测试与零散调用用）；扫描整个目录时走
// subscriptionFilterMatcher，每次同步只编译一次。
func matchesSubscriptionWhitelist(sub *model.Subscription, filePath string) bool {
	set := compileSubscriptionPatternSet(sub.Whitelist, subscriptionPatternWhitelist)
	return set.whitelistMatches(filePath)
}

func matchesSubscriptionFilters(sub *model.Subscription, filePath string) bool {
	if !matchesSubscriptionWhitelist(sub, filePath) {
		return false
	}
	return checkBlacklist(sub, filePath)
}

// subscriptionDependencyPatternMaxLen 是「依赖规则」单个片段的长度上限（按 rune 计）。
// 超过就判定为文字备注，见 looksLikeSubscriptionDependencyNote。
const subscriptionDependencyPatternMaxLen = 64

// looksLikeSubscriptionDependencyNote 判断依赖规则里的某一段是「文字备注」还是「文件名片段」。
//
// 兼容性背景：depend_on 在本次改造之前是纯备注字段（前端 placeholder 明写
// 「仅备注，不参与文件检出」），全链路一次都没读过它。存量数据里很可能是
// 「依赖 xxx 库，迁移自青龙」这类说明文字。现在它变成功能性字段后，直接拿去做
// 子串匹配有两种结果：
//  1. 匹配不到任何文件（绝大多数）——无害，只是没有额外检出；
//  2. 恰好命中（比如整段就是 `utils`）——会多检出一批文件。
//
// 第 2 种的杀伤力本身有限：依赖命中的文件只是落盘，不会被建成定时任务
// （见 isSubscriptionDependencyOnlyFile）。但没必要白白多下载，所以这里对
// 「一眼就是人话」的片段做启发式跳过，并在日志里明确列出被跳过的内容。
//
// 判定为备注的条件（任意一条命中）：
//   - 含空白字符：文件名片段不带空格；青龙 `ql repo` 的第 4 个位置参数是 shell 引号里的
//     `grep -E` 模式，各段用 `|` 分隔，段内也不含空格
//   - 含非 ASCII 字符（中文说明、中文标点）：脚本仓库里的路径是 ASCII
//   - rune 长度超过 subscriptionDependencyPatternMaxLen：单条文件名片段不会这么长
//
// 误判的代价是「该依赖文件没被额外检出」，也就是退回改造前的行为，方向安全。
func looksLikeSubscriptionDependencyNote(pattern string) bool {
	if pattern == "" {
		return false
	}
	if utf8.RuneCountInString(pattern) > subscriptionDependencyPatternMaxLen {
		return true
	}
	for _, r := range pattern {
		if r > unicode.MaxASCII || unicode.IsSpace(r) {
			return true
		}
	}
	return false
}

// splitSubscriptionDependencyPatterns 把「依赖规则」字段拆成真正参与匹配的模式，
// 以及被判定为文字备注、直接跳过的片段（调用方负责把 notes 打到日志里）。
//
// 分隔符与白/黑名单完全一致（`,` 和 `|`），匹配语义也一致：普通片段是子串包含（返回规整后的文本），
// 含正则触发字符的片段按正则（返回原文）；编译失败的正则片段不参与匹配，不在返回值里。
//
// 通配符（`*` / `all` / `全部`）刻意跳过而不是「全部当依赖」：依赖命中的文件不建任务，
// 若把整个仓库都算成依赖，等于一个定时任务都建不出来。跳过后依赖规则视为未配置，
// 行为与改造前一致。
func splitSubscriptionDependencyPatterns(raw string) (patterns []string, notes []string) {
	set := compileSubscriptionPatternSet(raw, subscriptionPatternDepend)
	for _, f := range set.fragments {
		switch f.kind {
		case subscriptionFragmentSubstring, subscriptionFragmentRegex:
			patterns = append(patterns, f.text)
		case subscriptionFragmentNote:
			notes = append(notes, f.text)
		}
	}
	return patterns, notes
}

// matchesSubscriptionDependency 判断文件是否命中「依赖规则」。
func matchesSubscriptionDependency(sub *model.Subscription, filePath string) bool {
	if sub == nil {
		return false
	}
	set := compileSubscriptionPatternSet(sub.DependOn, subscriptionPatternDepend)
	return set.anyMatches(filePath)
}

// isSubscriptionDependencyOnlyFile 判断文件是不是「只因为依赖规则才落盘」的辅助库文件。
//
// 这类文件（sendNotify.js、utils/*.js 等）是主脚本 require/import 的依赖，
// 检出它们是为了让主脚本能跑起来，本身不是定时任务，所以不进任务候选。
//
// 白名单判定放在前面且优先级更高：
//   - 白名单为空（或全是通配符）时 matchesSubscriptionWhitelist 恒 true，
//     这里恒返回 false → 依赖规则完全不改变任何行为。这正是「白名单为空 =
//     全部文件都算命中白名单，此时 depend_on 不应改变任何结果」的落点。
//   - 同时命中白名单和依赖规则的文件按白名单算，照常建任务。
func isSubscriptionDependencyOnlyFile(sub *model.Subscription, filePath string) bool {
	if sub == nil {
		return false
	}
	if matchesSubscriptionWhitelist(sub, filePath) {
		return false
	}
	return matchesSubscriptionDependency(sub, filePath)
}

// checkBlacklist 与白名单共用同一套拆分与匹配（subscriptionPatternSet），不再自己写一份 strings.Split(",")。
// 之前那份重复实现是 `|` 分隔失效的三个现场之一：白名单改好了黑名单还是不认 `|`。
// 返回 true 表示没被黑名单排除。通配写法跳过（黑名单填 `*` 不等于「全部排除」）。
func checkBlacklist(sub *model.Subscription, filePath string) bool {
	set := compileSubscriptionPatternSet(sub.Blacklist, subscriptionPatternBlacklist)
	return !set.anyMatches(filePath)
}

func syncSubscriptionTasks(sub *model.Subscription, emit PullCallback) {
	options := getSubscriptionTaskSyncOptions(sub)
	addStrategy := subscriptionTaskSyncStrategyLabel(sub.AutoAddTaskMode)
	delStrategy := subscriptionTaskSyncStrategyLabel(sub.AutoDelTaskMode)
	if !options.autoAdd && !options.autoDelete {
		// 旧文案写的是「订阅与系统设置中均未启用 auto_add_cron / auto_del_cron」，
		// 那是 OR 语义下唯一可能的原因；三态之后「全局开着、订阅自己强制关」也会走到这里，
		// 所以必须把两项各自的策略来源打出来，否则用户会去翻全局设置而永远找不到原因。
		emit(fmt.Sprintf("[跳过自动同步任务] 自动添加定时任务=关闭（策略：%s），自动删除失效任务=关闭（策略：%s）",
			addStrategy, delStrategy))
		return
	}

	// 两项各打一条策略来源：订阅单独设过就是「强制开启 / 强制关闭」，没设过是「跟随全局设置」。
	// 只关掉其中一项时不会走上面那条跳过日志，不打这两行的话用户看不出「为什么一个都没建」。
	if options.autoAdd {
		emit(fmt.Sprintf("[自动添加定时任务] 已启用（策略：%s）", addStrategy))
	} else {
		emit(fmt.Sprintf("[自动添加定时任务] 未启用（策略：%s），本次不会新建任务", addStrategy))
	}
	if options.autoDelete {
		emit(fmt.Sprintf("[自动删除失效任务] 已启用（策略：%s）", delStrategy))
	} else {
		emit(fmt.Sprintf("[自动删除失效任务] 未启用（策略：%s），本次不会删除任务", delStrategy))
	}

	saveDir := subscriptionSaveDir(sub)
	scriptsDir := filepath.Join(config.C.Data.ScriptsDir, saveDir)
	scan := scanSubscriptionTaskCandidates(sub, options)
	candidates, dependencyFiles := scan.candidates, scan.dependencyOnly
	label := subscriptionTaskLabel(sub.ID)

	// 可观测兜底：v2.2.8 之前任何空候选 / DB 创建失败都被静默吞掉，用户只看到
	// "[完成]" 就以为同步成功了。这里把每一步都打日志出来。
	// cron 的来源分开数（#134）：以前把默认规则建的也算进「含 cron 的脚本」，看不出哪些是脚本自己声明的。
	declaredCount, defaultRuleCount := 0, 0
	for _, candidate := range candidates {
		if candidate.DefaultRule {
			defaultRuleCount++
		} else {
			declaredCount++
		}
	}
	scannedFileCount := countSubscriptionScriptFiles(scriptsDir, options.allowedExts, sub)
	emit(fmt.Sprintf("[扫描脚本] 目录 %s 共扫描 %d 个候选文件（按白/黑名单过滤后），识别出 %d 个声明 cron 的脚本",
		scriptsDir, scannedFileCount, declaredCount))
	if defaultRuleCount > 0 {
		emit(fmt.Sprintf("[默认 Cron 规则] %d 个脚本没有识别到 cron 声明，使用订阅设置里的默认规则 %s", defaultRuleCount, options.defaultCron))
	}
	// 默认规则只在新建任务时用得上，自动添加关着就不提。
	if options.autoAdd && options.ignoredDefaultCron != "" {
		emit(fmt.Sprintf("[警告] 订阅设置里的默认 Cron 规则「%s」无效，已忽略", options.ignoredDefaultCron))
	}
	// 依赖规则是「拉下来但不建任务」，不打出来的话用户会以为这些文件被漏掉了。
	if len(dependencyFiles) > 0 {
		emit(fmt.Sprintf("[依赖文件] 依赖规则命中 %d 个文件，已拉取到脚本目录供主脚本调用，不会建成定时任务: %s",
			len(dependencyFiles), formatSubscriptionFileList(dependencyFiles, 10)))
	}
	// 扫到 0 个文件是「静默失败」最典型的落点：拉取全绿、日志无错、任务列表空。
	// 把最可能的三个原因直接摊开，别让用户去猜。
	if scannedFileCount == 0 {
		emit("[提示] 没有扫描到任何候选脚本，常见原因：1) 指定子目录/白名单/黑名单把文件全过滤掉了（多个片段用 `,` 或 `|` 分隔；普通片段按「子串包含」匹配，含 " + subscriptionRegexTriggerHint + " 的片段按正则匹配仓库相对路径）；2) 上一步 sparse-checkout 规则没命中任何文件；3) 系统设置 repo_file_extensions 不含该脚本扩展名")
		// 只有依赖文件落了盘、主脚本一个没扫到，说明白名单和依赖规则填反了。
		// 这是「依赖规则改成功能性」之后最容易出现的新型误配，单独点名。
		if len(dependencyFiles) > 0 {
			emit("[提示] 本次只有依赖规则命中了文件、白名单一个都没命中：依赖规则只负责把辅助库文件拉下来，不会建任务。请确认主脚本的文件名片段是否填在「白名单」里")
		}
	}

	var managedTasks []model.Task
	if err := queryTasksByLabel(label).Find(&managedTasks).Error; err != nil {
		emit(fmt.Sprintf("[警告] 读取本订阅的任务失败，本次跳过任务同步: %v", err))
		return
	}

	// 按脚本认任务（#125）：本订阅已有任务在跑某个候选脚本——命令原样，或改过参数、写法
	// （task x.js now、desi …、-m 30m、./x.js、node x.js …）——就算这个脚本「已经有任务」：
	// 不新建，也不改它的名称、定时、命令、状态、日志；同一脚本有多条任务也都不动。
	// 想恢复订阅默认就删掉任务重新拉取。以前按命令原文匹配，命令一加参数就对不上：
	// 新建一条重复任务，开着自动删除时还会把改过命令的那条连同历史日志删掉。
	scriptIndex := newSubscriptionScriptIndex(config.C.Data.ScriptsDir, candidates)
	undeclared := newSubscriptionUndeclaredScripts(scriptIndex, config.C.Data.ScriptsDir, scan.undeclaredCron)
	managedCommands := make(map[string]bool, len(managedTasks))
	managedKeys := make(map[string]bool, len(managedTasks))
	managedUndeclaredKeys := make(map[string]bool)
	for i := range managedTasks {
		managedCommands[strings.TrimSpace(managedTasks[i].Command)] = true
		if key, ok := scriptIndex.scriptKeyOf(managedTasks[i].Command); ok {
			managedKeys[key] = true
		}
		if key, ok := scriptIndex.scriptKeyIn(managedTasks[i].Command, undeclared.hasKey); ok {
			managedUndeclaredKeys[key] = true
		}
	}
	// undeclaredWithTask：没识别到 cron 声明、本次不建任务的脚本里已经有任务的（按精确命令记）。先记本订阅自己的
	// （升级前按每天 0 点兜底建的、之前接管的），新增分支接管到的再补上。有任务的不再接管同脚本的其他任务，也不进「未建」提示。
	undeclaredWithTask := make(map[string]bool, len(undeclared.items))
	for _, item := range undeclared.items {
		if managedCommands[item.command] || (item.key != "" && managedUndeclaredKeys[item.key]) {
			undeclaredWithTask[item.command] = true
		}
	}

	created := 0
	deleted := 0
	adopted := 0
	detached := 0
	failed := 0

	// adoptExisting 接管：只加上本订阅的标签，名称、定时、命令一概不动；有几条接管几条。
	adoptExisting := func(matches []*model.Task) {
		for _, existing := range matches {
			existing.SetLabelsFromSlice(withLabel(existing.GetLabels(), label))
			if err := database.DB.Model(existing).Update("labels", existing.Labels).Error; err != nil {
				failed++
				emit(fmt.Sprintf("[关联已有任务失败] %s: %v", existing.Name, err))
			} else {
				adopted++
				emit(fmt.Sprintf("[关联已有任务] %s", existing.Name))
			}
		}
	}

	adoptPoolFailed := false
	if options.autoAdd {
		var pending []string
		for _, command := range sortedSubscriptionCandidateCommands(candidates) {
			if managedCommands[command] {
				continue
			}
			// 键有歧义的候选（Windows 下仅大小写不同的两个文件）不按脚本认领，只认命令原文。
			if key, ok := scriptIndex.candidateKey(command); ok && managedKeys[key] {
				continue
			}
			pending = append(pending, command)
		}
		var unownedUndeclared []subscriptionUndeclaredScript
		for _, item := range undeclared.items {
			if !undeclaredWithTask[item.command] {
				unownedUndeclared = append(unownedUndeclared, item)
			}
		}

		var adoptPool *subscriptionAdoptPool
		if len(pending) > 0 || len(unownedUndeclared) > 0 {
			var err error
			if adoptPool, err = loadSubscriptionAdoptPool(scriptIndex, managedTasks, undeclared); err != nil {
				failed++
				emit(fmt.Sprintf("[关联已有任务失败] 读取任务列表出错，本次不新建任务，以免与已有任务重复: %v", err))
				pending, unownedUndeclared = nil, nil
				adoptPoolFailed = true
			}
		}

		for _, command := range pending {
			candidate := candidates[command]

			// 接管：不归本订阅管、但命令与候选完全相同或跑的是同一个脚本的任务（用户自建的、删订阅再重建后
			// 带着悬空旧标签的、别的订阅的），只加标签。
			key, keyOK := scriptIndex.candidateKey(command)
			if matches := adoptPool.take(command, key, keyOK); len(matches) > 0 {
				adoptExisting(matches)
				continue
			}

			task := model.Task{
				Name:            candidate.Name,
				Command:         candidate.Command,
				CronExpression:  candidate.CronExpression,
				TaskType:        model.TaskTypeCron,
				Status:          model.TaskStatusEnabled,
				Timeout:         0,
				NotifyOnFailure: true,
			}
			task.SetLabelsFromSlice([]string{label})
			if err := database.DB.Select("*").Create(&task).Error; err != nil {
				failed++
				emit(fmt.Sprintf("[自动添加任务失败] %s (cron: %s) command=%s err=%v",
					candidate.Name, candidate.CronExpression, candidate.Command, err))
			} else {
				// 与 handler/task_mutate.go 的 Create 同口径：注册调度失败要留痕，
				// 否则订阅日志里显示「自动添加任务」成功，任务却从此不会被 cron 触发。
				// 文案里的「失败」会被 panel_log.go 的 detectPanelLogLevel 判成 ERROR 级。
				// 调度器为 nil 时不注册（与下面 deleteSubscriptionTaskIfUnchanged 摘任务同一写法）：面板关停时
				// ShutdownSchedulerV2 已经把它置空，还在跑的拉取照样会走到这里（原来直接对 nil 调 AddJob，
				// 手动拉取的协程没有 recover，整个面板崩溃退出）；ddp 进程里调度器本来就没起。
				// 任务行已经落库，面板下次启动时 InitSchedulerV2 会按库注册它。
				if scheduler := GetSchedulerV2(); scheduler != nil {
					if err := scheduler.AddJob(&task); err != nil {
						log.Printf("任务 %d 注册调度失败（它不会自动触发）: %v", task.ID, err)
					}
				}
				created++
				if candidate.DefaultRule {
					emit(fmt.Sprintf("[自动添加任务] %s (cron: %s，默认规则)", candidate.Name, candidate.CronExpression))
				} else {
					emit(fmt.Sprintf("[自动添加任务] %s (cron: %s)", candidate.Name, candidate.CronExpression))
				}
			}
		}

		// 没识别到 cron 声明、本订阅还没有任务的脚本：不建任务，但已有的同脚本任务（青龙导入的、手建的、删订阅再重建后
		// 带着悬空旧标签的）照旧只加标签接管，与 v3.2.8 它们还是兜底候选时一致——不接管的话它们不归订阅管，
		// 上游删了脚本也不会被自动删除（Wave 2 复查）。认法与候选相同：精确命令或脚本键（键有歧义时只认精确命令）。
		for _, item := range unownedUndeclared {
			if matches := adoptPool.take(item.command, item.key, item.claimable); len(matches) > 0 {
				adoptExisting(matches)
				undeclaredWithTask[item.command] = true
			}
		}
	}

	// 没识别到 cron 声明、默认规则又没有可用值的脚本不建任务（#134）：说清有几个、是哪些、想建怎么办。放在接管之后算：
	// 已经有任务的（本订阅的、刚接管的，不论原来带什么标签）不算「未建」，不列——叫人给已经有任务的脚本再建一条只会建出重复任务。
	// 自动添加关着时本来就不建，不提示；读任务列表失败时不知道哪些脚本有任务，也不提示。
	// 不说「未声明 cron」：缩进 + 引号这类写法的声明认不出，脚本作者明明写了。
	if options.autoAdd && !adoptPoolFailed {
		var unbuilt []string
		for _, item := range undeclared.items {
			if undeclaredWithTask[item.command] {
				continue
			}
			rel, err := filepath.Rel(scriptsDir, item.path)
			if err != nil {
				rel = filepath.Base(item.path)
			}
			unbuilt = append(unbuilt, filepath.ToSlash(rel))
		}
		if len(unbuilt) > 0 {
			// 库里存着非法规则时（上面已有 [警告]），叫人「修正」而不是「填写」：设置里看着是填了的。
			action := "填写"
			if options.ignoredDefaultCron != "" {
				action = "修正"
			}
			emit(fmt.Sprintf("[提示] %d 个脚本没有识别到 cron 声明，未建定时任务（%s）。需要的话在订阅设置%s「默认 Cron 规则」，或手动为脚本建任务",
				len(unbuilt), formatSubscriptionUndeclaredCronFiles(unbuilt), action))
		}
	}

	if options.autoDelete {
		if len(candidates) == 0 && len(scan.undeclaredCron) == 0 {
			// 熔断：一个候选都没有，多半是检出为空、子目录/白名单配错，或单文件订阅扫错了目录，
			// 而不是上游真把脚本全删了。这时照「不在候选里就删」会把整个订阅的任务连同历史日志清空。
			// 没识别到 cron 声明、没建任务的脚本也算扫到了受管脚本（#134）：改动前它们按兜底 cron 都是候选、不触发熔断，
			// 这里保持一致，否则整个仓库都没写 cron 头的订阅从此再也删不掉上游已删脚本的任务。
			if len(managedTasks) > 0 {
				emit("[跳过自动删除] 本次没有识别到任何候选脚本，为防误删，未删除任何任务")
			}
		} else if otherLabels, err := otherLiveSubscriptionLabels(sub.ID); err != nil {
			failed++
			emit(fmt.Sprintf("[跳过自动删除] 读取订阅列表失败，无法确认任务是否还被其他订阅使用，为防误删，未删除任何任务: %v", err))
		} else {
			// 删除必须有正面证据（判定见 subscriptionStaleTaskJudge.judge）。范围不变：只看 task 开头的命令，
			// node / python3 这类解释器命令从不自动删除。没识别到 cron 声明、没建任务的脚本对应的已有任务一律保留（#134）。
			judge := newSubscriptionStaleTaskJudge(scriptIndex, candidates, saveDir,
				subscriptionScannedFileKeys(scriptIndex, config.C.Data.ScriptsDir, scan.seenFiles), undeclared)
			for i := range managedTasks {
				task := &managedTasks[i]
				if !strings.HasPrefix(strings.TrimSpace(task.Command), "task ") {
					continue
				}
				verdict, scriptPath, statErr := judge.judge(task.Command)
				switch verdict {
				case staleTaskKeep:
					continue
				case staleTaskKeepUnscanned:
					emit(fmt.Sprintf("[保留任务] %s：脚本 %s 还在订阅目录里，但本次扫描没有读到，未删除（可能是目录联接或存储读目录异常）",
						task.Name, scriptPath))
					continue
				case staleTaskKeepStatError:
					emit(fmt.Sprintf("[保留任务] %s：无法确认脚本 %s 是否还在（%s），未删除（可能是权限不足或存储异常）",
						task.Name, scriptPath, statErr))
					continue
				}
				// 跨订阅接管之后（两个订阅共用 SaveDir、单文件订阅的 SaveDir 设在别的仓库里），这条任务可能还带着
				// 别的订阅的标签。那个订阅还在，任务对它就没有失效：只摘掉本订阅的标签，不删行、不动日志，
				// 删不删交给那个订阅自己的同步。已删订阅留下的悬空旧标签（E9）不算，照常删。
				if usedByOtherSubscription(task, otherLabels) {
					changed, err := detachSubscriptionTaskIfUnchanged(task, label)
					if err != nil {
						failed++
						emit(fmt.Sprintf("[解除关联失败] %s: %v", task.Name, err))
						continue
					}
					if !changed {
						// changed=false 有两种：快照过期（同步期间任务被改 / 删，条件不命中）→ 保留并提示；
						// 本订阅标签本就不在任务上（detach 无可摘除）→ 静默，绝不能每次拉取都刷屏。
						if hasLabelFold(task.GetLabels(), label) {
							emit(fmt.Sprintf("[保留任务] %s：同步期间任务被修改或已删除，本次未解除关联", task.Name))
						}
						continue
					}
					detached++
					emit(fmt.Sprintf("[解除关联] %s：仍被其他订阅使用，只移除本订阅的标签，未删除", task.Name))
					continue
				}
				removed, err := deleteSubscriptionTaskIfUnchanged(task)
				if err != nil {
					failed++
					emit(fmt.Sprintf("[自动删除任务失败] %s: %v", task.Name, err))
					continue
				}
				if !removed {
					emit(fmt.Sprintf("[保留任务] %s：同步期间任务被修改或已删除，本次未删除", task.Name))
					continue
				}
				deleted++
				emit(fmt.Sprintf("[自动删除任务] %s", task.Name))
			}
		}
	}

	if created > 0 {
		emit(fmt.Sprintf("[共自动添加 %d 个定时任务]", created))
	}
	if adopted > 0 {
		emit(fmt.Sprintf("[共关联 %d 个已有任务]", adopted))
	}
	if deleted > 0 {
		emit(fmt.Sprintf("[共自动删除 %d 个失效任务]", deleted))
	}
	if detached > 0 {
		emit(fmt.Sprintf("[共解除关联 %d 个任务]", detached))
	}
	if failed > 0 {
		emit(fmt.Sprintf("[警告] 共 %d 个任务操作失败，详见上方日志", failed))
	}
	if created == 0 && adopted == 0 && deleted == 0 && detached == 0 && failed == 0 {
		emit("[同步完成] 本次未对定时任务做任何变更")
	}
}

// deleteSubscriptionTaskIfUnchanged 按同步开始时的快照条件删除一条失效任务：
// 只有 id 与命令原文都还和快照一致才删（同步期间用户改了命令、或别处已经删了，都不动），
// 任务行删到了，历史日志才跟着删——两步在同一个事务里，要么都删、要么都不删；提交之后再从调度器摘掉。
// 返回 (false, nil) 表示条件不满足、什么都没删。
//
// 事务里的顺序是「先删日志、再按条件删任务、条件不满足就回滚」：task_logs 对 tasks 有外键
// （PRAGMA foreign_keys 开着），任务行在、日志还在时删任务会报 FOREIGN KEY constraint failed。
// 删日志经 DeleteTaskLogs，先并入执行趋势的按天计数（#158）；它必须是事务第一条语句，这里本来就是。
// 事务里只能用 tx：库连接池只有 1 个连接，事务里再用 database.DB 会把自己锁死。
func deleteSubscriptionTaskIfUnchanged(task *model.Task) (bool, error) {
	removed, changed := false, false
	err := database.DB.Transaction(func(tx *gorm.DB) error {
		if _, _, err := DeleteTaskLogs(tx, "task_id = ?", task.ID); err != nil {
			return fmt.Errorf("删除历史日志失败: %w", err)
		}
		result := tx.Where("id = ? AND command = ?", task.ID, task.Command).Delete(&model.Task{})
		if result.Error != nil {
			return fmt.Errorf("删除任务失败: %w", result.Error)
		}
		if result.RowsAffected != 1 {
			// 返回非 nil 让事务回滚，刚删的日志与刚并入的计数原样恢复。
			changed = true
			return fmt.Errorf("任务在同步期间被修改或已删除")
		}
		removed = true
		return nil
	})
	if changed {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if removed {
		if scheduler := GetSchedulerV2(); scheduler != nil {
			scheduler.RemoveJob(task.ID)
		}
	}
	return removed, nil
}

// otherLiveSubscriptionLabels 返回除 selfID 外、仍然存在的订阅的标签（subscriptionTaskLabel 的形态），每次同步查一次。
// 删订阅不动任务，任务身上会留下已删订阅的悬空旧标签（E9）——它们不在这里面，不挡删除。
func otherLiveSubscriptionLabels(selfID uint) ([]string, error) {
	var ids []uint
	if err := database.DB.Model(&model.Subscription{}).Order("id").Pluck("id", &ids).Error; err != nil {
		return nil, err
	}
	labels := make([]string, 0, len(ids))
	for _, id := range ids {
		if id != selfID {
			labels = append(labels, subscriptionTaskLabel(id))
		}
	}
	return labels, nil
}

// usedByOtherSubscription：任务标签里有没有 otherLabels 中的任一个。与 queryTasksByLabel 的 LIKE 同口径：
// GetLabels 按逗号切、整项比较，但忽略大小写（hasLabelFold）——否则用户手写成 Subscription:2 时，B 的 LIKE 认它、
// A 的精确比较不认，A 会把 B 还在用的任务连日志删掉。
func usedByOtherSubscription(task *model.Task, otherLabels []string) bool {
	labels := task.GetLabels()
	for _, other := range otherLabels {
		if hasLabelFold(labels, other) {
			return true
		}
	}
	return false
}

// detachSubscriptionTaskIfUnchanged 把 label 从任务标签里摘掉（忽略大小写，见 withoutLabel），不删任务、不动日志与调度——
// 给「判为失效、但还被别的仍存在的订阅使用」的任务。快照条件比删除多一个 labels：同步期间用户改了命令或标签
// （含别的订阅刚摘掉自己的标签）、或任务已被删，都不动。返回 (false, nil) 有两种：新旧标签相同（没什么可摘、直接返回）、
// 或快照条件不命中（同步期间被改 / 删）——调用方两种都不计 detached、不打 [解除关联]，靠 hasLabelFold 区分要不要提示。
func detachSubscriptionTaskIfUnchanged(task *model.Task, label string) (bool, error) {
	updated := *task
	updated.SetLabelsFromSlice(withoutLabel(task.GetLabels(), label))
	if updated.Labels == task.Labels {
		// 标签本就不带 label（大小写规整后也没有）：没什么可摘的，直接当没改。调用方据此静默处理，避免空转刷屏。
		return false, nil
	}
	result := database.DB.Model(&model.Task{}).
		Where("id = ? AND command = ? AND labels = ?", task.ID, task.Command, task.Labels).
		Update("labels", updated.Labels)
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected == 1, nil
}

// countSubscriptionScriptFiles 统计 scriptsDir 下符合扩展名 + 白/黑名单过滤的文件数。
// 仅用于日志可观测：让用户知道"扫到了 X 个候选文件、识别出 Y 个 cron"——
// 当 X>0 而 Y=0 时能立刻看出是 cron 解析问题而不是路径问题。
func countSubscriptionScriptFiles(scriptsDir string, allowedExts map[string]bool, sub *model.Subscription) int {
	if _, err := os.Stat(scriptsDir); err != nil {
		return 0
	}
	matcher := newSubscriptionFilterMatcher(sub)
	count := 0
	filepath.Walk(scriptsDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			switch strings.ToLower(info.Name()) {
			case ".git", "node_modules", "__pycache__":
				return filepath.SkipDir
			}
			return nil
		}
		relPath := subscriptionRelativeScriptPath(scriptsDir, path, info)
		if matcher.shouldManage(relPath, allowedExts) {
			count++
		}
		return nil
	})
	return count
}

func getSubscriptionTaskSyncOptions(sub *model.Subscription) subscriptionTaskSyncOptions {
	// defaultCron 只保存用户在订阅设置里填的合法规则；留空（出厂默认）就是空，未声明 cron 的脚本不建任务（#134）。
	// v2.2.10 ～ v3.2.8 在留空时硬兜底成每天 0 点、建成启用任务，而且关不掉：config.py、mysend.py 这类库文件、
	// 拉取后钩子也被建成任务半夜跑，删掉了下次拉取又回来。现在要给这类脚本建任务，就在订阅设置里填默认规则。
	defaultCron, ignoredDefaultCron := subscriptionDefaultCronRule()

	return subscriptionTaskSyncOptions{
		// 三态解析，不再是 `sub.AutoAddTask || 全局`：见 resolveSubscriptionAutoAddTask 的注释。
		autoAdd:            resolveSubscriptionAutoAddTask(sub),
		autoDelete:         resolveSubscriptionAutoDelTask(sub),
		defaultCron:        defaultCron,
		ignoredDefaultCron: ignoredDefaultCron,
		allowedExts:        getSubscriptionAllowedExtensions(model.GetRegisteredConfig("repo_file_extensions")),
	}
}

// subscriptionDefaultCronRule 读订阅设置里的默认 Cron 规则：合法就返回它，留空返回空串。
// 库里的值 cron.Parse 不认时（normalizeDefaultCronRule 拒写非法值，只有直接改库 / 恢复旧备份能绕过去）当作没配，
// 原值放进 ignored——设置页里看着是填了的，拉取日志要说清它被忽略了。
func subscriptionDefaultCronRule() (rule, ignored string) {
	value := strings.TrimSpace(model.GetRegisteredConfig("default_cron_rule"))
	if value != "" && !cron.Parse(value).Valid {
		return "", value
	}
	return value, ""
}

func isConfigEnabled(key string, defaultValue bool) bool {
	if _, exists := model.GetSystemConfigDefinition(key); exists {
		return model.GetRegisteredConfigBool(key)
	}
	return model.GetConfigBool(key, defaultValue)
}

func getSubscriptionAllowedExtensions(raw string) map[string]bool {
	exts := make(map[string]bool)
	for _, token := range strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ';' || r == '\n' || r == '\r' || r == '\t' || r == ' '
	}) {
		token = strings.TrimSpace(strings.ToLower(token))
		token = strings.TrimPrefix(token, "*")
		if token == "" {
			continue
		}
		if !strings.HasPrefix(token, ".") {
			token = "." + token
		}
		exts[token] = true
	}
	if len(exts) > 0 {
		return exts
	}

	return map[string]bool{
		".js":  true,
		".mjs": true,
		".ts":  true,
		".py":  true,
		".sh":  true,
	}
}

// subscriptionHelperScriptNames 列出"通知辅助脚本"——这些脚本本身不是定时任务，
// 而是被业务脚本 require/import 调用的工具。订阅同步时不应该为它们建定时任务，
// 即使没有 cron 头并且系统配置了 default_cron_rule 兜底也不建。
// 名字按"去掉扩展名后的 basename，小写"匹配。
var subscriptionHelperScriptNames = map[string]bool{
	"sendnotify":  true, // QLScriptPublic / jdpro 风格的通知 helper（多种大小写拼写都收）
	"sendnofity":  true, // 实际仓库里 sendNofity.js 这种笔误也常见
	"notify":      true, // 青龙原版 notify.py
	"sendnotify_": true, // sendNotify_.js 这种带下划线后缀的变体
	"jdcookie":    true,
	"ql":          true,
	"qlapi":       true,
	"utils":       true,
	"util":        true,
	"common":      true,
	"helper":      true,
	"sign":        true, // 通用签名 helper
	"magic":       true, // jd_magic 类
	"jsencrypt":   true,
	"cryptojs":    true,
}

// isSubscriptionHelperScript 判断"该脚本是不是被业务脚本调用的辅助脚本"。
// 注意：只在脚本本身没有 cron 头注释时才用——脚本明确写了 cron 表达式
// 视为用户主动声明"这是定时任务"，必须建。
func isSubscriptionHelperScript(filename string) bool {
	base := strings.ToLower(strings.TrimSuffix(filename, filepath.Ext(filename)))
	return subscriptionHelperScriptNames[base]
}

func subscriptionRelativeScriptPath(root, path string, info os.FileInfo) string {
	if rel, err := filepath.Rel(root, path); err == nil && rel != "" && rel != "." {
		return rel
	}
	if info != nil {
		return info.Name()
	}
	return filepath.Base(path)
}

// shouldManageSubscriptionFile 是单文件判定：扩展名、子目录护栏、白 / 黑名单都过了才建任务。
// 扫描整个目录时走 subscriptionFilterMatcher（每次同步只编译一次），这里是给测试与零散调用的包装。
//
// 子目录护栏是「完整检出」开关的配套：`sub_path` 在整个 service 里只有 sparse-checkout 那一处引用，
// 它对建任务范围的约束一直靠「不在子目录里的文件根本不落盘」间接实现；打开完整检出、依赖规则的正则片段
// 把检出放宽成整仓（#129）、或依赖规则的普通片段并进检出范围之后，子目录外也有文件落盘，这条间接约束随之失效，
// 要在 Go 侧补回来。这几种情况的语义都是「多落一些文件到磁盘上给脚本自己读」，不是「多建一堆任务」。
// 细节见 newSubscriptionFilterMatcher。
func shouldManageSubscriptionFile(sub *model.Subscription, filePath string, allowedExts map[string]bool) bool {
	return newSubscriptionFilterMatcher(sub).shouldManage(filePath, allowedExts)
}

// collectSubscriptionTaskCandidates 返回任务候选，以及「仅因依赖规则落盘、刻意不建任务」
// 的文件相对路径（调用方负责打日志——这个项目的历史教训就是静默失配最难查）。
// 签名保持不变（有测试直接调它）；同步还需要「本次读到的全部文件」，走 scanSubscriptionTaskCandidates。
func collectSubscriptionTaskCandidates(sub *model.Subscription, options subscriptionTaskSyncOptions) (map[string]subscriptionTaskCandidate, []string) {
	scan := scanSubscriptionTaskCandidates(sub, options)
	return scan.candidates, scan.dependencyOnly
}

// subscriptionCandidateScan 是一次扫描的完整结果。
type subscriptionCandidateScan struct {
	candidates     map[string]subscriptionTaskCandidate
	dependencyOnly []string
	// seenFiles：本次扫描读到的全部文件（任意扩展名，未经任何规则过滤），路径与候选命令同根。
	// 自动删除分支靠它区分「文件在、被规则排除」（照删）与「文件在、扫描没读到」（保留并提示）。
	seenFiles []string
	// undeclaredCron：过了扩展名、子目录、白/黑名单与依赖规则，不是辅助脚本，但没声明 cron、默认规则又留空，
	// 本次不建任务的文件（路径与 seenFiles 同根）。它们仍是订阅在管的脚本：自动删除分支据此保留已有任务，
	// 拉取日志据此提示「未建任务」（#134）。
	undeclaredCron []string
}

func scanSubscriptionTaskCandidates(sub *model.Subscription, options subscriptionTaskSyncOptions) subscriptionCandidateScan {
	candidates := make(map[string]subscriptionTaskCandidate)
	saveDir := subscriptionSaveDir(sub)
	scriptsDir := filepath.Join(config.C.Data.ScriptsDir, saveDir)

	if _, err := os.Stat(scriptsDir); err != nil {
		return subscriptionCandidateScan{candidates: candidates}
	}

	// 收集"所有受支持扩展名的文件"。用 walk + 兜底的 ReadDir，确保:
	// 1) 子目录里的脚本能扫到（用 walk）
	// 2) 即使 walk 在某些挂载卷（NAS / Android Magisk 容器）下 readdir 异常返回 0，
	//    至少根目录平铺扫一遍兜底
	type fileEntry struct {
		path    string
		relPath string
		info    os.FileInfo
	}
	var allFiles []fileEntry
	var seenFiles []string
	seen := map[string]bool{}

	addEntry := func(path string, info os.FileInfo) {
		if info == nil || info.IsDir() {
			return
		}
		ext := strings.ToLower(filepath.Ext(info.Name()))
		if !options.allowedExts[ext] {
			return
		}
		if seen[path] {
			return
		}
		seen[path] = true
		allFiles = append(allFiles, fileEntry{
			path:    path,
			relPath: subscriptionRelativeScriptPath(scriptsDir, path, info),
			info:    info,
		})
	}

	filepath.Walk(scriptsDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			switch strings.ToLower(info.Name()) {
			case ".git", "node_modules", "__pycache__":
				return filepath.SkipDir
			}
			return nil
		}
		seenFiles = append(seenFiles, path)
		addEntry(path, info)
		return nil
	})

	// 兜底：walk 一个文件都没拿到，平铺扫根目录（不递归）
	if len(allFiles) == 0 {
		entries, _ := os.ReadDir(scriptsDir)
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			fullPath := filepath.Join(scriptsDir, entry.Name())
			info, err := entry.Info()
			if err != nil {
				if stat, statErr := os.Stat(fullPath); statErr == nil {
					info = stat
				} else {
					continue
				}
			}
			seenFiles = append(seenFiles, fullPath)
			addEntry(fullPath, info)
		}
	}

	// 依赖规则命中、白名单没命中的文件，是「为了让主脚本能跑起来才检出」的辅助库，
	// 在这里就摘出去，位置必须在下面的兜底 #2 之前，原因有两个：
	//  1) 它们本来就不该建任务；
	//  2) 兜底 #2 会把 Whitelist/Blacklist 整个清空，若依赖文件还留在 allFiles 里，
	//     兜底一触发就会把 sendNotify.js / utils/*.js 这些库文件全建成定时任务 ——
	//     正好是「依赖规则改成功能性」最需要避免的副作用。摘掉之后 allFiles 的内容
	//     与改造前（依赖文件压根不落盘）等价，兜底 #2 的判定结果也就完全不变。
	// 三个过滤字段每次同步只编译一次（正则片段要编译，不能对每个文件重来一遍）。
	matcher := newSubscriptionFilterMatcher(sub)

	var dependencyOnly []string
	if strings.TrimSpace(sub.DependOn) != "" {
		kept := allFiles[:0]
		for _, f := range allFiles {
			if matcher.isDependencyOnly(f.relPath) {
				dependencyOnly = append(dependencyOnly, filepath.ToSlash(f.relPath))
				continue
			}
			kept = append(kept, f)
		}
		allFiles = kept
	}

	// 兜底 #2：白/黑名单填错了导致全部被过滤 → 自动忽略过滤规则。
	// 依赖规则的正则片段把检出放宽成整仓时，只按放宽前本来就会落盘的范围判断，见 countsTowardWhitelistFallback；
	// 白名单自己的正则片段让本次检出整仓时不兜底，见 allowsWhitelistFallback。
	effective := matcher
	if (sub.Whitelist != "" || sub.Blacklist != "") && len(allFiles) > 0 {
		matchedCount := 0
		for _, f := range allFiles {
			if matcher.countsTowardWhitelistFallback(f.relPath) && matcher.matchesFilters(f.relPath) {
				matchedCount++
			}
		}
		if matchedCount == 0 && matcher.whitelist.hasNonWildcard() && matcher.allowsWhitelistFallback() {
			effective = matcher.withoutWhiteBlacklist()
		}
	}

	var undeclaredCron []string
	for _, f := range allFiles {
		path := f.path
		info := f.info

		if !effective.shouldManage(f.relPath, options.allowedExts) {
			continue
		}

		// 先尝试从脚本头部识别 cron。脚本明确写了 cron 就完全按它来。
		cronExpr := resolveSubscriptionScriptCron(path)
		defaultRule := false
		if cronExpr == "" {
			// 脚本头没 cron 注释：
			//   1) 已知是通知/工具辅助脚本（sendNotify.js / notify.py 等）→ 不建任务，也不算「未建任务」
			//   2) 订阅设置填了默认 Cron 规则 → 按规则建
			//   3) 默认规则留空 → 不建（#134），记进 undeclaredCron
			if isSubscriptionHelperScript(info.Name()) {
				continue
			}
			if options.defaultCron == "" {
				undeclaredCron = append(undeclaredCron, path)
				continue
			}
			cronExpr, defaultRule = options.defaultCron, true
		}

		relPath, err := filepath.Rel(config.C.Data.ScriptsDir, path)
		if err != nil {
			continue
		}
		command := "task " + relPath
		taskName := resolveSubscriptionTaskName(path, strings.TrimSuffix(info.Name(), filepath.Ext(info.Name())))
		candidates[command] = subscriptionTaskCandidate{
			Name:           taskName,
			Command:        command,
			CronExpression: cronExpr,
			DefaultRule:    defaultRule,
		}
	}

	return subscriptionCandidateScan{candidates: candidates, dependencyOnly: dependencyOnly, seenFiles: seenFiles, undeclaredCron: undeclaredCron}
}

func queryTasksByLabel(label string) *gorm.DB {
	return database.DB.Where(
		"labels = ? OR labels LIKE ? OR labels LIKE ? OR labels LIKE ?",
		label,
		label+",%",
		"%,"+label,
		"%,"+label+",%",
	)
}

// subscriptionScriptHeadLines 是订阅同步读脚本头部（cron 声明、任务名）的行数上限，两处共用。
// 以前 cron 只看前 50 行、任务名看 120 行：声明写在 51～120 行的脚本名字认得出、cron 却认不出（#134）。
const subscriptionScriptHeadLines = 120

// subscriptionScriptHeadLineMax 是单行参与匹配的最大字节数，与 bufio.Scanner 原来能处理的最长行一致。
const subscriptionScriptHeadLineMax = bufio.MaxScanTokenSize

// eachSubscriptionScriptHeadLine 逐行读脚本的前 subscriptionScriptHeadLines 行，fn 返回 false 时提前结束；文件打不开就什么都不做。
//
// 不用 bufio.Scanner：它遇到超过 64KB 的行（压缩、混淆过的单行代码很常见）报 ErrTooLong 后静默停止，
// 后面的 cron 声明一行都读不到（#134）。这里超长的行只取前 subscriptionScriptHeadLineMax 参与匹配，
// 余下的丢掉，照常往下读、照常计一行。行尾的 \n、\r\n 与 Scanner 一样去掉。
// 首行开头的 UTF-8 BOM 也去掉：Windows 编辑器保存的脚本常带 BOM，正则里的 \s 不匹配 U+FEFF，第 1 行的声明会认不出。
//
// 读缓冲用默认大小、长行才另攒：每个文件都开一块 64KB 缓冲的话，一次同步要读几千个文件，
// 分配量是 Scanner 的十来倍（实测 3000 次读 194MB 对 18MB），NAS / Magisk 这类小内存机器上 GC 明显变频繁。
func eachSubscriptionScriptHeadLine(path string, fn func(line string) bool) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()

	reader := bufio.NewReader(f)
	for lineNo := 1; lineNo <= subscriptionScriptHeadLines; lineNo++ {
		chunk, err := reader.ReadSlice('\n')
		line := string(chunk) // ReadSlice 的切片下次读取就会被覆盖，先拷出来
		if err == bufio.ErrBufferFull {
			// 比读缓冲长的行：接着读到行尾，最多攒到 subscriptionScriptHeadLineMax，其余丢掉。
			buf := []byte(line)
			for err == bufio.ErrBufferFull {
				chunk, err = reader.ReadSlice('\n')
				if room := subscriptionScriptHeadLineMax - len(buf); room > 0 {
					buf = append(buf, chunk[:min(len(chunk), room)]...)
				}
			}
			line = string(buf)
		}
		if line == "" && err != nil {
			return
		}
		line = strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
		if lineNo == 1 {
			line = strings.TrimPrefix(line, "\uFEFF")
		}
		if !fn(line) || err != nil {
			return
		}
	}
}

func resolveCronForSubscriptionTask(path string, defaultCron string) string {
	if expr := resolveSubscriptionScriptCron(path); expr != "" {
		return expr
	}
	return strings.TrimSpace(defaultCron)
}

// resolveSubscriptionScriptCron 读脚本头部，返回脚本自己声明的 cron，认不出返回空串。
func resolveSubscriptionScriptCron(path string) string {
	scriptBase := strings.ToLower(filepath.Base(path))
	expr := ""
	eachSubscriptionScriptHeadLine(path, func(line string) bool {
		expr = extractSubscriptionCronExpression(line, scriptBase)
		return expr == ""
	})
	return expr
}

// resolveSubscriptionTaskName 从脚本头部推断任务名，优先级：
//
//	new Env('名称')  >  注释头 `// name: 名称`  >  fallback（去掉扩展名的文件名）
//
// new Env 必须排在前面：它是青龙沿用至今的写法，存量任务名都是由它决定的。
// 订阅同步从 #125 起不再改已有任务的名称，但用户删掉任务重新拉取时，新建出来的名称仍要和原来一致；
// 让后加的注释头抢先，重建出来的任务就会换个名字。
// 所以这里两种声明都扫完再决定，而不是「哪个先在文件里出现就用哪个」。
func resolveSubscriptionTaskName(path, fallback string) string {
	fallback = strings.TrimSpace(fallback)

	var envName, labelName string
	eachSubscriptionScriptHeadLine(path, func(line string) bool {
		if envName == "" {
			if matches := subscriptionTaskNameRe.FindStringSubmatch(line); len(matches) > 1 {
				envName = strings.TrimSpace(matches[1])
			}
		}
		if labelName == "" {
			labelName = extractSubscriptionTaskNameFromLabel(line)
		}
		return envName == "" || labelName == ""
	})

	if envName != "" {
		return envName
	}
	if labelName != "" {
		return labelName
	}
	return fallback
}

// extractSubscriptionTaskNameFromLabel 解析单行注释头里的任务名。
// 除了正则本身，还要收尾处理块注释/HTML 注释的结束标记和包裹引号，
// 否则 `<!-- name: X -->` 会把 `-->` 也算进名字里。
func extractSubscriptionTaskNameFromLabel(line string) string {
	matches := subscriptionTaskNameLabelRe.FindStringSubmatch(line)
	if len(matches) < 2 {
		return ""
	}

	name := strings.TrimSpace(matches[1])
	name = strings.TrimSpace(strings.TrimSuffix(name, "-->"))
	name = strings.TrimSpace(strings.TrimSuffix(name, "*/"))
	name = strings.TrimSpace(strings.Trim(name, "\"'`"))
	if name == "" {
		return ""
	}

	// 任务名列是 VARCHAR(128)，注释里写超长句子时截断，避免建出一条离谱的任务名。
	if runes := []rune(name); len(runes) > 128 {
		name = strings.TrimSpace(string(runes[:128]))
	}
	return name
}

func extractSubscriptionCronExpression(line, scriptBase string) string {
	if expr := extractSubscriptionCronExpressionFromLabel(line); expr != "" {
		return expr
	}

	if matches := cronDirectiveLineRe.FindStringSubmatch(line); len(matches) > 2 && scriptBase != "" {
		expr := strings.TrimSpace(matches[1])
		fileToken := normalizeSubscriptionCronScriptToken(matches[2])
		if fileToken != "" &&
			strings.EqualFold(filepath.Base(fileToken), scriptBase) &&
			cron.Parse(expr).Valid {
			return expr
		}
	}

	return extractSubscriptionCronExpressionFromFilenameLine(line, scriptBase)
}

// extractSubscriptionCronExpressionFromLabel 处理“cron”标签开头的行，
// 兼容 `cron:`、`cron`（无冒号）、JSDoc `* cron`、`@cron:` 等多种写法。
// 当行尾跟随文件名提示（例如 `cron 8 10 * * *  qtx.js`）时，只截取前 5 或 6 个字段做 cron。
func extractSubscriptionCronExpressionFromLabel(line string) string {
	matches := cronLabelPrefixRe.FindStringSubmatch(line)
	if len(matches) < 3 {
		return ""
	}
	rest := strings.TrimSpace(matches[2])
	if rest == "" {
		return ""
	}

	if cron.Parse(rest).Valid {
		return rest
	}

	// `cron: "0 8 * * *"` / `cron: '0 8 * * *'`：青龙用 grep + xargs 取值，引号被 xargs 去掉，能认；面板以前认不出（#134）。
	// 只剥「成对包住整个值」的引号，不放宽别的写法；而且只在 cron 前面带注释标记（# // * @）或顶格（Python docstring 常见）时剥——
	// 缩进的裸 `  cron: '0 0 * * *'` 是 JS / TS 对象字面量的属性，不能认成声明（末尾带逗号的本来就认不出，没逗号的最后一个属性靠这条挡）。
	if prefix := matches[1]; prefix == "" || strings.ContainsAny(prefix, "#*@/") {
		if n := len(rest); n >= 2 && (rest[0] == '"' || rest[0] == '\'') && rest[n-1] == rest[0] {
			if unquoted := strings.TrimSpace(rest[1 : n-1]); unquoted != "" && cron.Parse(unquoted).Valid {
				return unquoted
			}
		}
	}

	fields := strings.Fields(rest)
	for _, cnt := range []int{6, 5} {
		if len(fields) < cnt {
			continue
		}
		expr := strings.Join(fields[:cnt], " ")
		if cron.Parse(expr).Valid {
			return expr
		}
	}
	return ""
}

func extractSubscriptionCronExpressionFromFilenameLine(line, scriptBase string) string {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || scriptBase == "" {
		return ""
	}

	cleaned := strings.TrimSpace(strings.Trim(trimmed, `"'`))
	fields := strings.Fields(cleaned)
	if len(fields) < 6 {
		return ""
	}

	for _, cronFieldCount := range []int{6, 5} {
		if len(fields) <= cronFieldCount {
			continue
		}

		expr := strings.Join(fields[:cronFieldCount], " ")
		if !cron.Parse(expr).Valid {
			continue
		}

		fileToken := normalizeSubscriptionCronScriptToken(fields[cronFieldCount])
		if fileToken == "" {
			continue
		}

		if strings.EqualFold(filepath.Base(fileToken), scriptBase) {
			return expr
		}
	}

	return ""
}

func normalizeSubscriptionCronScriptToken(token string) string {
	token = strings.TrimSpace(token)
	token = strings.Trim(token, `"'`)
	token = strings.TrimRight(token, ",;:)")
	token = strings.TrimLeft(token, "(")
	return strings.TrimSpace(token)
}
