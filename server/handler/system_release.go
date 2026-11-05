package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"daidai-panel/model"
	"daidai-panel/service"
)

// 下面三个地址抽成变量只是为了单测能换成 httptest 地址，运行时不会改它们。
var (
	latestPanelReleaseAPI = "https://api.github.com/repos/linzixuanzz/daidai-panel/releases/latest"
	// githubAPIOrigin 是 GitHub Token 唯一允许发往的源（scheme + host 必须完全一致）。
	// 🔴 Token 绝不能带到 github.com 网页、Release 下载地址、加速代理或镜像：那些地址要么不需要它，
	// 要么是第三方（用户填的加速源），带过去就等于把 Token 交给了别人。
	githubAPIOrigin = "https://api.github.com"
	// latestPanelReleasePage 是 API 被限流时的兜底：网页的 /releases/latest 会 302 到 /releases/tag/<tag>，
	// 只看跳转地址就能拿到最新版本号。这条路拿不到附件 digest，所以只能用来「显示有新版本」，不能用来执行更新。
	latestPanelReleasePage = "https://github.com/linzixuanzz/daidai-panel/releases/latest"
)

// panelReleaseTagPageBase 拼回退时返回给前端的 release_url（固定指向 github.com 的 tag 页）。
const panelReleaseTagPageBase = "https://github.com/linzixuanzz/daidai-panel/releases/tag/"

// githubTokenConfigKey 是「系统设置 → 网络」里的 GitHub Token（Secret 配置）。
const githubTokenConfigKey = "github_token"

type panelReleaseInfo struct {
	TagName     string              `json:"tag_name"`
	Name        string              `json:"name"`
	Body        string              `json:"body"`
	HTMLURL     string              `json:"html_url"`
	PublishedAt string              `json:"published_at"`
	Assets      []panelReleaseAsset `json:"assets"`
}

type panelReleaseAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Size               int64  `json:"size"`
	// Digest 是 GitHub 为每个 Release 附件算好的校验值，形如 "sha256:<hex>"。
	// 下载更新包后用它核对内容，防止下载链路（含用户配置的加速代理）返回被篡改的程序。
	Digest string `json:"digest"`
}

func (r panelReleaseInfo) version() string {
	return strings.TrimPrefix(strings.TrimSpace(r.TagName), "v")
}

func (r panelReleaseInfo) findAsset(name string) (panelReleaseAsset, bool) {
	name = strings.TrimSpace(name)
	if name == "" {
		return panelReleaseAsset{}, false
	}

	for _, asset := range r.Assets {
		if strings.EqualFold(strings.TrimSpace(asset.Name), name) {
			return asset, true
		}
	}
	return panelReleaseAsset{}, false
}

// githubAPIError 是 GitHub API 返回非 200 时的错误（#177）。
// 调用方用 errors.As 取出它，按 RateLimited / ResetAt 决定是否通知、何时重试、要不要走网页回退。
type githubAPIError struct {
	StatusCode int
	// Message 是 GitHub JSON 里的 message，已截断到 200 字符以内；解析不出来时为空。
	Message string
	// RateLimited 表示这次是被限流（403 + 额度用完 / 二级限流，或 429）。
	RateLimited bool
	// ResetAt 是额度恢复时间：优先取 retry-after（秒），其次 x-ratelimit-reset（Unix 秒）；两个头都没有时为零值。
	ResetAt time.Time
	// TokenUsed 表示这次请求带了 GitHub Token：决定 401 的说法，以及限流时还要不要提示「去填 Token」。
	TokenUsed bool
}

func (e *githubAPIError) Error() string {
	if e.StatusCode == http.StatusUnauthorized && e.TokenUsed {
		return "GitHub Token 无效或已过期（401）"
	}
	if e.RateLimited {
		who := "匿名访问"
		if e.TokenUsed {
			who = "GitHub Token 的访问额度"
		}
		text := fmt.Sprintf("GitHub API 返回 %d：%s已达上限，", e.StatusCode, who)
		if clock := formatGitHubResetClock(e.ResetAt, time.Now()); clock != "" {
			text += "约 " + clock + " 恢复"
		} else {
			text += "请稍后再试"
		}
		if !e.TokenUsed {
			text += "。可在「系统设置 → 网络」填写 GitHub Token 提高额度"
		}
		return text
	}
	if e.Message != "" {
		return fmt.Sprintf("GitHub API 返回 %d：%s", e.StatusCode, e.Message)
	}
	return fmt.Sprintf("GitHub API 返回 %d", e.StatusCode)
}

// formatGitHubResetClock 把恢复时间格式化成面板时区的 HH:MM（面板时区由 service.ApplyRegisteredPanelTimezone 写进 time.Local）；
// 不是今天时带上日期（MM-DD HH:MM）。零值返回空串。
func formatGitHubResetClock(resetAt, now time.Time) string {
	if resetAt.IsZero() {
		return ""
	}
	local := resetAt.In(time.Local)
	nowLocal := now.In(time.Local)
	if local.Year() == nowLocal.Year() && local.YearDay() == nowLocal.YearDay() {
		return local.Format("15:04")
	}
	return local.Format("01-02 15:04")
}

// isGitHubAPIRequestURL 判断一个地址是不是发往 GitHub API 的：scheme 与 host（含端口）都要和 githubAPIOrigin 完全一致。
// 子域名、带端口的写法、http 明文、加速代理拼出来的「代理前缀 + api.github.com」地址都不算。
func isGitHubAPIRequestURL(target *url.URL) bool {
	if target == nil {
		return false
	}
	origin, err := url.Parse(githubAPIOrigin)
	if err != nil {
		return false
	}
	return strings.EqualFold(target.Scheme, origin.Scheme) && strings.EqualFold(target.Host, origin.Host)
}

// applyGitHubAPIToken 只在请求发往 GitHub API 时带上 Authorization，返回是否真的带了。
func applyGitHubAPIToken(req *http.Request) bool {
	token := strings.TrimSpace(model.GetRegisteredConfig(githubTokenConfigKey))
	if token == "" || !isGitHubAPIRequestURL(req.URL) {
		return false
	}
	req.Header.Set("Authorization", "Bearer "+token)
	return true
}

func fetchLatestPanelRelease() (*panelReleaseInfo, error) {
	client := service.NewHTTPClient(15 * time.Second)
	// 第二道保险：Go 跟随跳转时本来就不会把 Authorization 带去别的域名，
	// 这里再按 githubAPIOrigin 显式删一次，跳到哪都不会把 Token 带出 api.github.com。
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return errors.New("跳转次数过多")
		}
		if !isGitHubAPIRequestURL(req.URL) {
			req.Header.Del("Authorization")
		}
		return nil
	}

	req, err := http.NewRequest(http.MethodGet, latestPanelReleaseAPI, nil)
	if err != nil {
		return nil, fmt.Errorf("检查更新失败: %w", err)
	}
	tokenUsed := applyGitHubAPIToken(req)

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("检查更新失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, parseGitHubAPIError(resp, tokenUsed, time.Now())
	}

	var release panelReleaseInfo
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return nil, fmt.Errorf("解析 GitHub 响应失败")
	}

	return &release, nil
}

// parseGitHubAPIError 把非 200 响应翻译成 githubAPIError：带状态码、GitHub 的 message，并识别限流。
func parseGitHubAPIError(resp *http.Response, tokenUsed bool, now time.Time) *githubAPIError {
	apiErr := &githubAPIError{StatusCode: resp.StatusCode, TokenUsed: tokenUsed}

	// 错误响应体很小，读 64KB 封顶，防止异常响应把内存吃满
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	var payload struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(body, &payload) == nil {
		// 压成单行，再按字符（不是字节）截断到 200 以内，避免把中文截成乱码
		message := strings.Join(strings.Fields(payload.Message), " ")
		if runes := []rune(message); len(runes) > 200 {
			message = string(runes[:199]) + "…"
		}
		apiErr.Message = message
	}

	remaining := strings.TrimSpace(resp.Header.Get("X-RateLimit-Remaining"))
	retryAfter := strings.TrimSpace(resp.Header.Get("Retry-After"))
	// 429 一定是限流；403 只有在额度为 0、带 retry-after（二级限流）或 message 写明 rate limit 时才算，
	// 其余 403（例如仓库被封、Token 权限不够）照普通错误报。
	if resp.StatusCode == http.StatusTooManyRequests ||
		(resp.StatusCode == http.StatusForbidden &&
			(remaining == "0" || retryAfter != "" || strings.Contains(strings.ToLower(apiErr.Message), "rate limit"))) {
		apiErr.RateLimited = true
		if seconds, err := strconv.Atoi(retryAfter); err == nil && seconds >= 0 {
			apiErr.ResetAt = now.Add(time.Duration(seconds) * time.Second)
		} else if unix, err := strconv.ParseInt(strings.TrimSpace(resp.Header.Get("X-RateLimit-Reset")), 10, 64); err == nil && unix > 0 {
			apiErr.ResetAt = time.Unix(unix, 0)
		}
	}
	return apiErr
}

// fetchLatestPanelTagFromWeb 是 API 被限流时的兜底：请求网页的 /releases/latest、不跟随跳转，从 Location 里取 tag。
// 🔴 这里绝不带 GitHub Token（不是 api.github.com）。拿到的只有版本号，没有附件 digest，不能用来执行更新。
func fetchLatestPanelTagFromWeb() (string, error) {
	client := service.NewHTTPClient(10 * time.Second)
	client.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	resp, err := client.Get(latestPanelReleasePage)
	if err != nil {
		return "", fmt.Errorf("访问 GitHub Release 页面失败: %w", err)
	}
	defer resp.Body.Close()
	return parsePanelReleaseTagFromRedirect(resp.StatusCode, resp.Header.Get("Location"))
}

// parsePanelReleaseTagFromRedirect 从 /releases/latest 的跳转里取出 tag（原样保留 v 前缀，调用方自己去掉）。
func parsePanelReleaseTagFromRedirect(statusCode int, location string) (string, error) {
	if statusCode < 300 || statusCode >= 400 {
		return "", fmt.Errorf("GitHub Release 页面返回 %d，没有跳转到具体版本", statusCode)
	}
	parsed, err := url.Parse(strings.TrimSpace(location))
	if err != nil {
		return "", fmt.Errorf("GitHub Release 页面的跳转地址无法解析")
	}
	const marker = "/releases/tag/"
	index := strings.Index(parsed.Path, marker)
	if index < 0 {
		// 仓库还没有任何 Release 时会跳回 /releases 列表页，这里没有版本号
		return "", fmt.Errorf("GitHub Release 页面没有跳转到具体版本")
	}
	tag := parsed.Path[index+len(marker):]
	if slash := strings.Index(tag, "/"); slash >= 0 {
		tag = tag[:slash]
	}
	tag = strings.TrimSpace(tag)
	if strings.TrimPrefix(tag, "v") == "" {
		return "", fmt.Errorf("GitHub Release 页面的跳转地址里没有版本号")
	}
	// 只收版本号该有的字符：tag 会拼进 release_url 返回给前端，不能让奇怪的跳转地址混进别的东西
	for _, r := range tag {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r == '.' || r == '-' || r == '_' || r == '+') {
			return "", fmt.Errorf("GitHub Release 页面的跳转地址里版本号格式不对")
		}
	}
	return tag, nil
}

// describePanelUpdateRateLimited 是网页回退时 update_disabled_reason 的文案（二进制 / Magisk 部署）。
func describePanelUpdateRateLimited(apiErr *githubAPIError) string {
	text := "GitHub API 被限流，拿不到更新包校验值，"
	if clock := formatGitHubResetClock(apiErr.ResetAt, time.Now()); clock != "" {
		text += "约 " + clock + " 恢复后可一键更新"
	} else {
		text += "恢复后可一键更新"
	}
	if !apiErr.TokenUsed {
		text += "；也可在系统设置 → 网络填写 GitHub Token"
	}
	return text
}
