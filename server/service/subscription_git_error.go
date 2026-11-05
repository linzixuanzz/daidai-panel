package service

import (
	"errors"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// 这个文件只解决一件事：别再让用户只看到 `exit status 128`。
//
// git 失败时真正有用的信息在它自己打印的 `fatal:` / `error:` 行里，但那些行是
// 英文、夹在一大片输出中间，而 Go 侧包装出来的 `exit status 128` 又毫无信息量。
// 用户拿到的日志因此变成「失败了，但不知道为什么、也不知道该干什么」。
//
// 这里按常见失败特征翻译出一条可操作的中文提示，由调用方 **追加** 在原始输出
// 之后——原始 `fatal:` 行一行都不删，它对深入排查仍然有价值。
//
// 匹配方式刻意选「整体小写化 + 子串包含」而不是正则：
//   - git / curl 这些提示语是稳定的英文字面量，会变的只有中间嵌的 URL、对象
//     哈希、ref 名、端口号、耗时数字；锚定固定片段最不容易随 git 版本漂移，
//     而正则反而要为 URL 里的 `.` `?` `/` 做一堆转义，写法更脆、收益为零。
//   - 大小写必须忽略：git 自己就不统一（`fatal:` 全小写、`Could not resolve
//     host` 首字母大写、ssh 侧的 `ERROR: Repository not found.` 全大写）。
//   - 一次 ToLower 后做若干次 Contains，成本可以忽略，且只在命令失败时才走。

// gitFailureSignals 是从 git 原始输出里识别到的失败特征。
//
// 多个特征可以同时成立：用户实际遇到的就是「镜像限流(429)」叠加
// 「partial clone 回远端补 blob 失败」——前者是根因，后者解释了这次为什么会
// 突然打出海量请求。所以这里不是互斥枚举，而是一组独立开关。
type gitFailureSignals struct {
	rateLimited  bool // 429 / Too Many Requests：镜像或远端限流
	promisor     bool // partial clone 回远端补 blob 失败（本身不是根因，是请求放大器）
	authFailed   bool // 鉴权失败 / 403 / 拿不到凭证
	repoNotFound bool // 仓库不存在或无权访问 / 404
	unreachable  bool // DNS 解析失败、连接超时、连接被拒
	refNotFound  bool // 分支 / ref 在远端不存在
	noGetrandom  bool // 宿主机内核太老，没有 getrandom 系统调用，git 建不了临时文件（#173）
}

// any 返回是否识别出了任何特征。
func (s gitFailureSignals) any() bool {
	return s.rateLimited || s.promisor || s.authFailed || s.repoNotFound || s.unreachable || s.refNotFound || s.noGetrandom
}

// detectGitFailureSignals 只做特征识别，不组装文案，方便单独测。
func detectGitFailureSignals(text string) gitFailureSignals {
	lower := strings.ToLower(text)
	has := func(needles ...string) bool {
		for _, needle := range needles {
			if strings.Contains(lower, needle) {
				return true
			}
		}
		return false
	}

	return gitFailureSignals{
		// `The requested URL returned error: 429` / `error: RPC failed; HTTP 429 ...`
		// GitHub 的二级限流会走 403 + `secondary rate limit`，所以一并收进来。
		rateLimited: has(
			"returned error: 429",
			"http 429",
			"429 too many requests",
			"too many requests",
			"rate limit exceeded",
			"secondary rate limit",
		),
		// `fatal: could not fetch <oid> from promisor remote`
		promisor: has("promisor remote"),
		// `fatal: Authentication failed for '...'`
		// `fatal: could not read Username for '...': terminal prompts disabled`
		// `git@github.com: Permission denied (publickey).`
		//
		// 刻意不匹配裸的 `permission denied`：那也可能是本地文件权限问题
		// （`error: unable to create file ...: Permission denied`），处置方式完全不同。
		authFailed: has(
			"authentication failed",
			"could not read username",
			"could not read password",
			"terminal prompts disabled",
			"invalid username or password",
			"returned error: 403",
			"http 403",
			"403 forbidden",
			"permission denied (publickey",
			"remote: permission to",
		),
		// `remote: Repository not found.` / `fatal: repository '...' not found`
		// ssh 侧还会打 `fatal: '...' does not appear to be a git repository`。
		// 注意不要匹配 `not a git repository`——那是「本地目录不是仓库」，另一回事。
		repoNotFound: has(
			"repository not found",
			"returned error: 404",
			"http 404",
			"404 not found",
			"does not appear to be a git repository",
		),
		// `Could not resolve host: github.com`
		// `Failed to connect to github.com port 443 after 21053 ms: Connection timed out`
		unreachable: has(
			"could not resolve host",
			"could not resolve hostname",
			"failed to connect to",
			"connection timed out",
			"connection refused",
			"connection reset by peer",
			"network is unreachable",
			"no route to host",
			"operation timed out",
		),
		// `fatal: couldn't find remote ref refs/heads/xxx`
		// `fatal: Remote branch xxx not found in upstream origin`
		refNotFound: has(
			"couldn't find remote ref",
			"could not find remote ref",
			"not found in upstream",
		),
		// `error: unable to get random bytes for temporary file: Function not implemented`
		// 是 git 调 getrandom 拿到 ENOSYS：群晖 DSM 6 这类老 NAS 的内核是 3.10，而 getrandom 3.17 才有。与网络、代理无关。
		noGetrandom: has("unable to get random bytes"),
	}
}

// 下面三个抽成变量只为单测能替换：内核版本文件、当前 CPU 架构、是否 Magisk 模块版。
var (
	kernelOSReleasePath        = "/proc/sys/kernel/osrelease"
	gitHintRuntimeArch         = runtime.GOARCH
	gitHintIsMagiskModuleCheck = IsMagiskModuleRuntime
)

// buildGitNoGetrandomHint 给「内核不支持 getrandom」出一行 [提示]（#173）。
//
// 2026-11-05 实测：alpine:3.23 的 git 2.52 动态依赖 getrandom，Debian bookworm 的 git 2.39 读 /dev/urandom，
// 所以 Alpine 镜像换成 Debian 版就能在老内核上拉库。Alpine 判断复用 playwright_runtime.go 的 DetectLinuxOSRelease。
// Debian 版镜像只有 amd64 / arm64，别的架构叫人换镜像没用，只能升级宿主系统（README 的镜像标签表）。
func buildGitNoGetrandomHint() string {
	kernel := ""
	if data, err := os.ReadFile(kernelOSReleasePath); err == nil {
		kernel = strings.TrimSpace(string(data))
		// 只是拼进提示的版本号，异常长的内容不往日志里塞
		if len(kernel) > 64 {
			kernel = ""
		}
	}
	// 有版本号时是「宿主机内核 3.10.105 不支持」，读不到时是「宿主机内核不支持」，中间不能多出空格
	host := "宿主机内核"
	if kernel != "" {
		host += " " + kernel + " "
	}
	head := "[提示] " + host + "不支持 getrandom（Linux 3.17 起才有），"

	if DetectLinuxOSRelease().ID != "alpine" {
		return head + "git 无法创建临时文件，请升级宿主系统。"
	}
	if gitHintIsMagiskModuleCheck() {
		// 面具模块没有镜像标签可换，对应的出路是改刷 Debian 版模块
		return head + "Alpine 版模块里的 git 依赖它，无法创建临时文件。请改刷 Debian 版面具模块（daidai-panel-magisk-debian-vX.Y.Z.zip），或升级系统。"
	}
	if gitHintRuntimeArch != "amd64" && gitHintRuntimeArch != "arm64" {
		return head + "Alpine 镜像里的 git 依赖它，无法创建临时文件；Debian 版镜像只有 amd64 / arm64，当前架构 " + gitHintRuntimeArch + " 只能升级宿主系统。"
	}
	// 标签写法与 README 一致：把 latest 换成 debian（latest → debian、latest-full → debian-full），写死 :debian 会让 full 用户丢掉工具链
	return head + "Alpine 镜像里的 git 依赖它，无法创建临时文件。请换用 debian 镜像（把镜像标签里的 latest 换成 debian，如 latest → debian、latest-full → debian-full），或升级宿主系统。"
}

// 各类失败的提示文案。每条都要说清三件事：什么错了、为什么会这样、下一步怎么办。
const (
	gitHintRateLimited = "远端以「请求过于频繁」拒绝了本次请求（HTTP 429 Too Many Requests）：当前使用的镜像/代理地址已经触发限流。请稍等几分钟再重试，或在订阅设置里更换其他镜像地址、改用原始仓库地址。"

	gitHintAuthFailed = "远端拒绝了身份验证：仓库需要凭证，或当前凭证无效/已过期。私有仓库请在订阅里配置 SSH 密钥，或改用带 Access Token 的 HTTPS 地址；公开仓库请先确认地址没有写错。"

	gitHintRepoNotFound = "远端找不到这个仓库（404）：地址不存在、仓库已改名或已删除，也可能是私有仓库而当前凭证无权访问。请核对订阅里的仓库地址；私有仓库需要配置 SSH 密钥或 Access Token。"

	gitHintUnreachable = "无法连接到远端：DNS 解析失败或连接超时/被拒绝，说明容器网络不通，或镜像地址本身已经失效。请检查容器的网络与 DNS 设置，确认能访问该域名，必要时更换镜像地址。"

	gitHintRefNotFound = "远端没有订阅配置的这个分支：常见于默认分支已经从 master 改成 main。请在订阅设置里修正分支名，或把分支留空以使用远端默认分支。"

	// promisor 单独命中：补 blob 这一步确实失败了，但没识别出更上层的根因。
	gitHintPromisorOnly = "本地仓库是 partial clone（克隆时带了 --filter=blob:none，只下载了当时用得到的文件），这次切换子目录/白名单规则时 git 需要逐个回远端把缺失文件补下来，而这一步失败了，仓库内容并不完整。可稍后重试；若反复失败，删除该订阅的本地目录后重新拉取——全新克隆一次性取回所需文件，请求数远少于逐个补 blob。"

	// promisor 叠加在已识别的根因之后：补充说明「这次请求量为什么突然这么大」。
	gitHintPromisorSuffix = "另外，本地仓库是 partial clone（克隆时带了 --filter=blob:none，只下载了当时用得到的文件），这次切换子目录/白名单规则时 git 必须逐个回远端补下缺失文件，请求量被成倍放大，因此格外容易撞上上面这个失败。若重试仍不行，删除该订阅的本地目录后重新拉取——全新克隆一次性取回所需文件，请求数远少于逐个补 blob。"
)

// classifyGitFailure 把 git 的原始报错翻译成一条可操作的中文提示。
//
// output 是命令的合并输出（stdout+stderr），err 是命令返回的错误。
// 识别不出来时返回 ""，调用方照常输出原有的通用错误，不做任何改变。
//
// 返回值自带 `[错误]` 前缀，与本包其它 emit 文案风格一致，调用方直接 emit 即可；
// 唯一例外是内核不支持 getrandom（#173），那条是解释环境原因的 `[提示]`，同样是单行。
func classifyGitFailure(output string, err error) string {
	if err == nil {
		return ""
	}

	text := output
	if errText := err.Error(); errText != "" {
		// 有些调用方传进来的是已经包了原始输出的 error，一并纳入匹配范围。
		text += "\n" + errText
	}

	signals := detectGitFailureSignals(text)
	if !signals.any() {
		return ""
	}

	// 内核不支持 getrandom 时 git 连临时文件都建不了，别的特征（如果有）都是它的连带结果，直接给这一条 [提示]。
	// 它跟在 git 自己的 `error: unable to get random bytes …` 行后面，说明原因与出路。
	if signals.noGetrandom {
		return buildGitNoGetrandomHint()
	}

	// 根因优先级：先给能直接解释「远端为什么拒绝」的结论。
	// rateLimited 排在 authFailed 之前，是因为 GitHub 的二级限流会以 403 返回并
	// 附带 `secondary rate limit`——此时限流才是根因，按鉴权失败提示会把人带偏。
	// promisor 不参与这个排序：它不是根因，只是把请求量放大的放大器，
	// 因此改为附在根因之后作为补充说明。
	var root string
	switch {
	case signals.rateLimited:
		root = gitHintRateLimited
	case signals.authFailed:
		root = gitHintAuthFailed
	case signals.repoNotFound:
		root = gitHintRepoNotFound
	case signals.unreachable:
		root = gitHintUnreachable
	case signals.refNotFound:
		root = gitHintRefNotFound
	}

	switch {
	case root != "" && signals.promisor:
		// 用户实际遇到的就是这一条：429 + promisor 合并成一条完整提示。
		return "[错误] " + root + gitHintPromisorSuffix
	case root != "":
		return "[错误] " + root
	case signals.promisor:
		return "[错误] " + gitHintPromisorOnly
	default:
		return ""
	}
}

// gitCommandStderr 取出 `cmd.Output()` 顺手捎在 *exec.ExitError 里的 stderr。
//
// exec.Cmd.Output() 只有在调用方自己没设置 Stderr 时才会捕获它（见其实现里的
// captureErr 分支），本包这几个调用方都没设置，所以这里一定拿得到。
func gitCommandStderr(err error) string {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return string(exitErr.Stderr)
	}
	return ""
}

// wrapGitCommandError 给 `cmd.Output()` 这类不走日志流的 git 调用兜底。
//
// 这些调用的 stderr 既不会 emit 给用户，也不会进 error，用户最终只能看到一句
// `exit status 128`。这里把 stderr 原文和可操作提示一起塞进 error，
// 让它至少和走 runCmdWithCallback 的路径一样可读。
func wrapGitCommandError(action, stderr string, err error) error {
	if err == nil {
		return nil
	}

	detail := strings.TrimSpace(stderr)
	message := action + "失败: " + err.Error()
	if detail != "" {
		message += "\n" + detail
	}
	if hint := classifyGitFailure(detail, err); hint != "" {
		message += "\n" + hint
	}
	return errors.New(message)
}
