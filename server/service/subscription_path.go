package service

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"

	"daidai-panel/config"
	"daidai-panel/model"
	"daidai-panel/pkg/pathutil"
)

// 订阅「保存目录 / 别名」的路径校验。
//
// 这两个字段会直接拼进脚本目录下的落盘路径（git 订阅的仓库目录、单文件订阅的下载目标），
// 以前不做任何校验：save_dir 填 `../../xxx` 就能把仓库克隆、文件下载到脚本目录之外，
// 再配合 reset --hard / clean -fd 或覆盖下载，能改写面板数据目录甚至系统文件。
// 所以分两道闸：保存时（handler Create / Update）按下面的规则拒绝；
// 拉取时再用 pathutil.ResolveWithinBase 兜一次底，挡住存量脏数据、URL 末段推导出来的目录和符号链接。

// subscriptionPathEscapeHint 是拉取时越界的统一提示，用户在拉取日志里看到的就是这一句。
const subscriptionPathEscapeHint = "保存目录超出脚本目录，请修改订阅"

// subscriptionDriveLetterRe 匹配 Windows 盘符开头（C: / c:）。
var subscriptionDriveLetterRe = regexp.MustCompile(`^[A-Za-z]:`)

// subscriptionPathHasControlChar：控制字符（换行、NUL 等）出现在路径里只会制造歧义，一律拒绝。
func subscriptionPathHasControlChar(value string) bool {
	for _, r := range value {
		if unicode.IsControl(r) {
			return true
		}
	}
	return false
}

// subscriptionPathSegmentIsDotDot：路径段是不是「上一级」。
// 除了标准的 `..`，还把 `...`、`.. ` 这类全由点和空格组成且含 `..` 的段算进来——
// Windows 会剥掉路径段末尾的点和空格，这类写法在 Windows 上同样可能指向上级目录。
func subscriptionPathSegmentIsDotDot(segment string) bool {
	return strings.Contains(segment, "..") && strings.Trim(segment, ". ") == ""
}

// validateSubscriptionRelativeDir 是「当目录用」的统一规则：保存目录、git 订阅的别名都走这里。
// 先把 `\` 统一成 `/` 再判断，允许多级子目录（libs/utils）和中文目录。
func validateSubscriptionRelativeDir(label, value string) error {
	if subscriptionPathHasControlChar(value) {
		return fmt.Errorf("%s不能包含控制字符", label)
	}
	normalized := strings.ReplaceAll(value, "\\", "/")
	if strings.HasPrefix(normalized, "/") {
		return fmt.Errorf("%s必须是脚本目录下的相对路径，不能以 / 或 \\ 开头", label)
	}
	if subscriptionDriveLetterRe.MatchString(normalized) {
		return fmt.Errorf("%s必须是脚本目录下的相对路径，不能以盘符开头", label)
	}
	for _, segment := range strings.Split(normalized, "/") {
		if subscriptionPathSegmentIsDotDot(segment) {
			return fmt.Errorf("%s不能包含 .. 路径段", label)
		}
	}
	return nil
}

// ValidateSubscriptionSaveDir 校验保存目录。空串合法（拉取时按默认规则推导）。
func ValidateSubscriptionSaveDir(saveDir string) error {
	if saveDir == "" {
		return nil
	}
	return validateSubscriptionRelativeDir("保存目录", saveDir)
}

// ValidateSubscriptionAlias 校验别名。别名在两种订阅里含义不同：
// 单文件订阅里它是下载后的**文件名**，不能带路径分隔符、不能是 . 或 ..；
// git 订阅里它是仓库目录名（保存目录为空时生效），按保存目录的规则校验。
func ValidateSubscriptionAlias(subType, alias string) error {
	if alias == "" {
		return nil
	}
	if subType != model.SubTypeSingleFile {
		return validateSubscriptionRelativeDir("别名", alias)
	}
	if subscriptionPathHasControlChar(alias) {
		return fmt.Errorf("别名不能包含控制字符")
	}
	if strings.ContainsAny(alias, "/\\") {
		return fmt.Errorf("单文件订阅的别名是文件名，不能包含 / 或 \\")
	}
	// 全由点和空格组成的名字（. / .. / ...）解析出来是目录本身或上级目录，不是文件。
	if strings.Trim(alias, ". ") == "" {
		return fmt.Errorf("单文件订阅的别名不能是 . 或 ..")
	}
	return nil
}

// ValidateGitSubscriptionTargetDir 校验 git 订阅最终会落到的目录（save_dir → alias → URL 末段）。
// 两件事：保存目录和别名都没填时，URL 末段推导出来的目录同样要过相对目录规则；
// 最终目录不能是脚本目录本身——URL 以 / 结尾时末段为空就会推导成脚本根目录，
// 之后的 git init + reset --hard + clean -fd 会把用户的其他脚本全部清掉。
// 单文件订阅直接放行（它的落盘目录默认是 downloads，可以是脚本根目录）。
func ValidateGitSubscriptionTargetDir(subType, url, saveDir, alias string) error {
	if subType == model.SubTypeSingleFile {
		return nil
	}
	dir := subscriptionSaveDir(&model.Subscription{URL: url, SaveDir: saveDir, Alias: alias})
	if saveDir == "" && alias == "" {
		if err := validateSubscriptionRelativeDir("由仓库地址推导出的保存目录", dir); err != nil {
			return fmt.Errorf("%s，请填写保存目录或别名", err.Error())
		}
	}
	cleaned := filepath.ToSlash(filepath.Clean(strings.ReplaceAll(dir, "\\", "/")))
	if cleaned == "." || cleaned == "/" {
		return fmt.Errorf("Git 订阅不能直接保存到脚本根目录，请填写保存目录或别名")
	}
	return nil
}

// ValidateSubscriptionTarget 是新建订阅时的完整校验：保存目录、别名、git 订阅的最终目录。
func ValidateSubscriptionTarget(subType, url, saveDir, alias string) error {
	if err := ValidateSubscriptionSaveDir(saveDir); err != nil {
		return err
	}
	if err := ValidateSubscriptionAlias(subType, alias); err != nil {
		return err
	}
	return ValidateGitSubscriptionTargetDir(subType, url, saveDir, alias)
}

// resolveGitSubscriptionDestDir 解析 git 订阅的仓库目录（拉取时的兜底）。
// 路径公式不变（ScriptsDir/<subscriptionSaveDir>），只是多了两道检查：
// 解析符号链接后必须仍在脚本目录内；且不能就是脚本目录本身。
// 先转绝对路径再交给 ResolveWithinBase：脚本目录配成相对路径时，相对的 destDir 会被它再拼一次基础目录，
// 把 `..` 吃掉，越界就查不出来了。
func resolveGitSubscriptionDestDir(scriptsDir string, sub *model.Subscription) (string, error) {
	saveDir := subscriptionSaveDir(sub)
	destDir := filepath.Join(scriptsDir, saveDir)
	if absDestDir, err := filepath.Abs(destDir); err == nil {
		destDir = absDestDir
	}
	if _, err := pathutil.ResolveWithinBase(scriptsDir, destDir, false); err != nil {
		return "", fmt.Errorf("%s（当前保存目录：%s）", subscriptionPathEscapeHint, saveDir)
	}
	// destDir 已确认在脚本目录内，此时「脚本目录在 destDir 之内」只可能是两者相同。
	if pathutil.IsWithinBase(destDir, scriptsDir) {
		return "", fmt.Errorf("Git 订阅的保存目录不能是脚本根目录（拉取会清理目录里不属于仓库的文件），请在订阅里填写保存目录或别名")
	}
	return destDir, nil
}

// resolveSingleFileSubscriptionDestPath 解析单文件订阅的下载目标（拉取时的兜底）。
// 路径公式仍是 singleFileSubscriptionDestPath，这里只负责挡越界：
// 别名、保存目录的存量脏数据，或下载目标本身是指向脚本目录外的符号链接，都会在这里失败。
func resolveSingleFileSubscriptionDestPath(scriptsDir string, sub *model.Subscription) (string, error) {
	saveDir, filename := singleFileSubscriptionTarget(sub)
	if strings.Trim(filename, ". ") == "" {
		return "", fmt.Errorf("无法从订阅地址确定下载文件名，请在订阅里填写别名")
	}
	destPath := singleFileSubscriptionDestPath(scriptsDir, sub)
	absDestPath := destPath
	if abs, err := filepath.Abs(destPath); err == nil {
		absDestPath = abs
	}
	if _, err := pathutil.ResolveWithinBase(scriptsDir, absDestPath, false); err != nil {
		return "", fmt.Errorf("%s（当前保存目录：%s，文件名：%s）", subscriptionPathEscapeHint, saveDir, filename)
	}
	// 文件名里带了 `x/..` 这类写法时，下载目标会解析成脚本目录本身。
	if pathutil.IsWithinBase(absDestPath, scriptsDir) {
		return "", fmt.Errorf("%s（当前保存目录：%s，文件名：%s）", subscriptionPathEscapeHint, saveDir, filename)
	}
	return destPath, nil
}

// checkSubscriptionPullTarget 在拉取一开始（拉取前指令之前）检查落盘路径，
// 越界就让整次拉取失败，拉取前指令、拉取、钩子和任务同步都不会跑。
func checkSubscriptionPullTarget(sub *model.Subscription) error {
	scriptsDir := config.C.Data.ScriptsDir
	if sub.Type == model.SubTypeSingleFile {
		_, err := resolveSingleFileSubscriptionDestPath(scriptsDir, sub)
		return err
	}
	_, err := resolveGitSubscriptionDestDir(scriptsDir, sub)
	return err
}
