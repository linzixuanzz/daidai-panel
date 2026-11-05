package handler

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"daidai-panel/model"
	"daidai-panel/testutil"

	"github.com/gin-gonic/gin"
)

// pointGitHubAPIAt 把 GitHub API 地址与 Token 允许的源都换成 httptest 地址，用例结束后还原。
func pointGitHubAPIAt(t *testing.T, serverURL string) {
	t.Helper()
	oldAPI, oldOrigin := latestPanelReleaseAPI, githubAPIOrigin
	latestPanelReleaseAPI = serverURL + "/repos/linzixuanzz/daidai-panel/releases/latest"
	githubAPIOrigin = serverURL
	t.Cleanup(func() {
		latestPanelReleaseAPI, githubAPIOrigin = oldAPI, oldOrigin
	})
}

// pointReleasePageAt 把网页回退地址换成 httptest 地址，用例结束后还原。
func pointReleasePageAt(t *testing.T, serverURL string) {
	t.Helper()
	old := latestPanelReleasePage
	latestPanelReleasePage = serverURL + "/linzixuanzz/daidai-panel/releases/latest"
	t.Cleanup(func() { latestPanelReleasePage = old })
}

// #177：五种 GitHub API 响应的错误类型与文案。
func TestFetchLatestPanelReleaseClassifiesGitHubResponses(t *testing.T) {
	testutil.SetupTestEnv(t)

	resetUnix := time.Now().Add(30 * time.Minute).Unix()
	longMessage := strings.Repeat("很长的报错", 100)

	var mode atomic.Value
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch mode.Load().(string) {
		case "403-remaining-0":
			w.Header().Set("X-RateLimit-Remaining", "0")
			w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(resetUnix, 10))
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"message":"API rate limit exceeded for 203.0.113.9. (But here's the good news...)"}`))
		case "429-retry-after":
			w.Header().Set("Retry-After", "120")
			w.WriteHeader(http.StatusTooManyRequests)
		case "404-message":
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"Not Found","documentation_url":"https://docs.github.com"}`))
		case "404-long-message":
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"` + longMessage + `"}`))
		case "403-blocked":
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"message":"Repository access blocked"}`))
		case "401":
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"message":"Bad credentials"}`))
		default:
			_, _ = w.Write([]byte(`{"tag_name":"v3.9.9","name":"v3.9.9","body":"notes","html_url":"https://github.com/linzixuanzz/daidai-panel/releases/tag/v3.9.9"}`))
		}
	}))
	defer server.Close()
	pointGitHubAPIAt(t, server.URL)

	fetchAPIError := func(t *testing.T) *githubAPIError {
		t.Helper()
		release, err := fetchLatestPanelRelease()
		if err == nil {
			t.Fatalf("应当返回错误，实际拿到 %#v", release)
		}
		var apiErr *githubAPIError
		if !errors.As(err, &apiErr) {
			t.Fatalf("错误应当能 errors.As 成 *githubAPIError，实际 %T: %v", err, err)
		}
		return apiErr
	}

	t.Run("403 + remaining=0 是限流，恢复时间取 x-ratelimit-reset", func(t *testing.T) {
		mode.Store("403-remaining-0")
		apiErr := fetchAPIError(t)
		if !apiErr.RateLimited || apiErr.StatusCode != http.StatusForbidden {
			t.Fatalf("应识别为 403 限流，实际 %#v", apiErr)
		}
		if !apiErr.ResetAt.Equal(time.Unix(resetUnix, 0)) {
			t.Fatalf("恢复时间应取 x-ratelimit-reset，实际 %v", apiErr.ResetAt)
		}
		text := apiErr.Error()
		want := "GitHub API 返回 403：匿名访问已达上限，约 " + formatGitHubResetClock(apiErr.ResetAt, time.Now()) + " 恢复"
		if !strings.HasPrefix(text, want) || !strings.Contains(text, "填写 GitHub Token") {
			t.Fatalf("限流文案不对：%q", text)
		}
		// GitHub 的限流 message 里带着出口 IP，不应原样抛给用户
		if strings.Contains(text, "203.0.113.9") {
			t.Fatalf("限流文案不应带上 GitHub 的原始 message：%q", text)
		}
	})

	t.Run("429 + retry-after 是限流，恢复时间按秒数推算", func(t *testing.T) {
		mode.Store("429-retry-after")
		before := time.Now()
		apiErr := fetchAPIError(t)
		after := time.Now()
		if !apiErr.RateLimited || apiErr.StatusCode != http.StatusTooManyRequests {
			t.Fatalf("应识别为 429 限流，实际 %#v", apiErr)
		}
		if apiErr.ResetAt.Before(before.Add(120*time.Second)) || apiErr.ResetAt.After(after.Add(120*time.Second)) {
			t.Fatalf("恢复时间应为 now+120s，实际 %v", apiErr.ResetAt)
		}
		if !strings.HasPrefix(apiErr.Error(), "GitHub API 返回 429：匿名访问已达上限，约 ") {
			t.Fatalf("429 文案不对：%q", apiErr.Error())
		}
	})

	t.Run("404 带上 GitHub 的 message", func(t *testing.T) {
		mode.Store("404-message")
		apiErr := fetchAPIError(t)
		if apiErr.RateLimited {
			t.Fatal("404 不是限流")
		}
		if got := apiErr.Error(); got != "GitHub API 返回 404：Not Found" {
			t.Fatalf("404 文案不对：%q", got)
		}
	})

	t.Run("message 截断到 200 字符以内", func(t *testing.T) {
		mode.Store("404-long-message")
		apiErr := fetchAPIError(t)
		if n := len([]rune(apiErr.Message)); n == 0 || n > 200 {
			t.Fatalf("message 应截断到 200 字符以内，实际 %d 字符", n)
		}
	})

	t.Run("没有限流特征的 403 按普通错误报", func(t *testing.T) {
		mode.Store("403-blocked")
		apiErr := fetchAPIError(t)
		if apiErr.RateLimited {
			t.Fatalf("不带限流头的 403 不应识别为限流：%#v", apiErr)
		}
		if got := apiErr.Error(); got != "GitHub API 返回 403：Repository access blocked" {
			t.Fatalf("403 文案不对：%q", got)
		}
	})

	t.Run("401 且填了 Token：提示 Token 无效", func(t *testing.T) {
		if err := model.SetConfig(githubTokenConfigKey, "test-token"); err != nil {
			t.Fatalf("set github_token: %v", err)
		}
		t.Cleanup(func() { _ = model.SetConfig(githubTokenConfigKey, "") })
		mode.Store("401")
		apiErr := fetchAPIError(t)
		if !apiErr.TokenUsed || apiErr.RateLimited {
			t.Fatalf("401 应标记已带 Token、且不是限流：%#v", apiErr)
		}
		if got := apiErr.Error(); got != "GitHub Token 无效或已过期（401）" {
			t.Fatalf("401 文案不对：%q", got)
		}
	})

	t.Run("200 正常解析", func(t *testing.T) {
		mode.Store("200")
		release, err := fetchLatestPanelRelease()
		if err != nil {
			t.Fatalf("200 不应报错: %v", err)
		}
		if release.version() != "3.9.9" || release.Body != "notes" {
			t.Fatalf("解析结果不对：%#v", release)
		}
	})
}

func TestFormatGitHubResetClockAddsDateAcrossDays(t *testing.T) {
	now := time.Date(2026, 11, 5, 23, 30, 0, 0, time.Local)
	if got := formatGitHubResetClock(now.Add(4*time.Minute), now); got != "23:34" {
		t.Fatalf("同一天只给 HH:MM，实际 %q", got)
	}
	if got := formatGitHubResetClock(now.Add(50*time.Minute), now); got != "11-06 00:20" {
		t.Fatalf("跨天要带日期，实际 %q", got)
	}
	if got := formatGitHubResetClock(time.Time{}, now); got != "" {
		t.Fatalf("零值应返回空串，实际 %q", got)
	}
}

// #177：网页回退只从 /releases/latest 的跳转地址里取 tag。
func TestParsePanelReleaseTagFromRedirect(t *testing.T) {
	cases := []struct {
		name     string
		status   int
		location string
		wantTag  string
		wantErr  bool
	}{
		{"正常 tag（绝对地址）", http.StatusFound, "https://github.com/linzixuanzz/daidai-panel/releases/tag/v3.4.3", "v3.4.3", false},
		{"正常 tag（相对地址）", http.StatusFound, "/linzixuanzz/daidai-panel/releases/tag/v3.4.3", "v3.4.3", false},
		{"无 v 前缀", http.StatusFound, "https://github.com/linzixuanzz/daidai-panel/releases/tag/3.4.3", "3.4.3", false},
		{"非 tag 跳转（还没有 Release）", http.StatusFound, "https://github.com/linzixuanzz/daidai-panel/releases", "", true},
		{"tag 为空", http.StatusFound, "https://github.com/linzixuanzz/daidai-panel/releases/tag/", "", true},
		{"tag 里混进奇怪字符", http.StatusFound, "https://github.com/linzixuanzz/daidai-panel/releases/tag/v3.4.3%22%3E%3Cscript%3E", "", true},
		{"非 302（直接 200）", http.StatusOK, "", "", true},
		{"非 302（404）", http.StatusNotFound, "", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tag, err := parsePanelReleaseTagFromRedirect(tc.status, tc.location)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("应当报错，实际拿到 %q", tag)
				}
				return
			}
			if err != nil || tag != tc.wantTag {
				t.Fatalf("期望 %q，实际 %q / %v", tc.wantTag, tag, err)
			}
		})
	}
}

// #177：Token 只能出现在发往 api.github.com 的请求头里。
func TestGitHubTokenOnlySentToGitHubAPI(t *testing.T) {
	// 生产地址下的判定
	allowed := []string{
		"https://api.github.com/repos/linzixuanzz/daidai-panel/releases/latest",
	}
	denied := []string{
		"https://github.com/linzixuanzz/daidai-panel/releases/latest",
		"https://github.com/linzixuanzz/daidai-panel/releases/download/v3.4.3/daidai-linux-amd64.tar.gz",
		"https://objects.githubusercontent.com/github-production-release-asset/1",
		// 加速代理拼出来的地址：主机是代理，不是 api.github.com
		"https://gh-proxy.org/https://api.github.com/repos/linzixuanzz/daidai-panel/releases/latest",
		"http://api.github.com/repos/linzixuanzz/daidai-panel/releases/latest",
		"https://api.github.com.evil.example/repos",
		"https://api.github.com:8443/repos",
	}
	for _, raw := range append(append([]string{}, allowed...), denied...) {
		parsed, err := url.Parse(raw)
		if err != nil {
			t.Fatalf("parse %s: %v", raw, err)
		}
		want := raw == allowed[0]
		if got := isGitHubAPIRequestURL(parsed); got != want {
			t.Errorf("%s：期望 %v，实际 %v", raw, want, got)
		}
	}

	testutil.SetupTestEnv(t)
	if err := model.SetConfig(githubTokenConfigKey, "test-token"); err != nil {
		t.Fatalf("set github_token: %v", err)
	}

	// 「别处」：网页回退、跳转目标、Release 下载都落在这台服务器上，它收到的任何请求都不能带 Authorization
	payload := []byte("fake panel update package")
	sum := sha256.Sum256(payload)
	var mu sync.Mutex
	var leaked []string
	elsewhere := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if auth := r.Header.Get("Authorization"); auth != "" {
			mu.Lock()
			leaked = append(leaked, r.URL.Path+" => "+auth)
			mu.Unlock()
		}
		switch {
		case strings.HasSuffix(r.URL.Path, "/releases/latest"):
			http.Redirect(w, r, "/linzixuanzz/daidai-panel/releases/tag/v3.9.9", http.StatusFound)
		case r.URL.Path == "/asset":
			_, _ = w.Write(payload)
		default:
			_, _ = w.Write([]byte(`{"tag_name":"v3.9.8"}`))
		}
	}))
	defer elsewhere.Close()

	var apiAuth atomic.Value
	apiAuth.Store("")
	var redirectAway atomic.Bool
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apiAuth.Store(r.Header.Get("Authorization"))
		if redirectAway.Load() {
			http.Redirect(w, r, elsewhere.URL+"/moved", http.StatusFound)
			return
		}
		_, _ = w.Write([]byte(`{"tag_name":"v3.9.9"}`))
	}))
	defer api.Close()
	pointGitHubAPIAt(t, api.URL)
	pointReleasePageAt(t, elsewhere.URL)

	// 1. 发往 API 的请求带 Token
	if _, err := fetchLatestPanelRelease(); err != nil {
		t.Fatalf("fetch release: %v", err)
	}
	if got := apiAuth.Load().(string); got != "Bearer test-token" {
		t.Fatalf("发往 GitHub API 的请求应带 Token，实际 %q", got)
	}

	// 2. API 跳转到别的地址时不带过去
	redirectAway.Store(true)
	if _, err := fetchLatestPanelRelease(); err != nil {
		t.Fatalf("fetch release via redirect: %v", err)
	}

	// 3. 网页回退不带，且不跟随跳转（tag 页本身不会被请求）
	tag, err := fetchLatestPanelTagFromWeb()
	if err != nil || tag != "v3.9.9" {
		t.Fatalf("网页回退应取到 v3.9.9，实际 %q / %v", tag, err)
	}

	// 4. Release 下载（含加速代理拼出来的地址）不带
	if err := downloadBinaryUpdateAsset(elsewhere.URL+"/asset", filepath.Join(t.TempDir(), "pkg.tar.gz"), "sha256:"+hex.EncodeToString(sum[:])); err != nil {
		t.Fatalf("download asset: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(leaked) > 0 {
		t.Fatalf("Token 被带到了 api.github.com 以外的地址：%v", leaked)
	}
}

// #177：手动检查更新遇到限流时，用网页回退显示新版本，但二进制部署不能放行一键更新。
func TestCheckUpdateFallsBackToReleasePageWhenRateLimited(t *testing.T) {
	testutil.SetupTestEnv(t)

	resetUnix := time.Now().Add(20 * time.Minute).Unix()
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-RateLimit-Remaining", "0")
		w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(resetUnix, 10))
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"message":"API rate limit exceeded"}`))
	}))
	defer api.Close()
	var pageMode atomic.Value
	pageMode.Store("redirect")
	page := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if pageMode.Load().(string) == "redirect" {
			http.Redirect(w, r, "/linzixuanzz/daidai-panel/releases/tag/v99.0.0", http.StatusFound)
			return
		}
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer page.Close()
	pointGitHubAPIAt(t, api.URL)
	pointReleasePageAt(t, page.URL)

	callCheckUpdate := func() *httptest.ResponseRecorder {
		router := gin.New()
		router.GET("/system/check-update", (&SystemHandler{}).CheckUpdate)
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/system/check-update", nil))
		return recorder
	}
	decode := func(t *testing.T, recorder *httptest.ResponseRecorder) map[string]any {
		t.Helper()
		var body struct {
			Data map[string]any `json:"data"`
		}
		if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode check-update: %v / %s", err, recorder.Body.String())
		}
		return body.Data
	}

	t.Run("二进制部署：显示新版本但不放行一键更新", func(t *testing.T) {
		t.Setenv("PANEL_UPDATE_MANAGER", "")
		t.Setenv("WATCHTOWER_HTTP_API_URL", "")
		t.Setenv("WATCHTOWER_HTTP_API_TOKEN", "")
		recorder := callCheckUpdate()
		if recorder.Code != http.StatusOK {
			t.Fatalf("回退成功时应回 200，实际 %d: %s", recorder.Code, recorder.Body.String())
		}
		data := decode(t, recorder)
		if data["latest"] != "99.0.0" || data["has_update"] != true {
			t.Fatalf("应从跳转地址取到 99.0.0 且有更新，实际 %#v", data)
		}
		if data["release_url"] != panelReleaseTagPageBase+"v99.0.0" || data["release_notes"] != "" {
			t.Fatalf("release_url 应指向 tag 页、release_notes 为空，实际 %#v", data)
		}
		if data["rate_limited"] != true || data["rate_limited_reset_at"] != time.Unix(resetUnix, 0).Format(time.RFC3339) {
			t.Fatalf("应带上限流标记与恢复时间，实际 %#v", data)
		}
		if target, _ := data["update_target"].(map[string]any); target != nil && target["deployment_type"] == panelUpdateDeploymentDocker {
			t.Skip("本机能走旧 Docker Socket 更新链，那条链不需要 digest，不在本用例的断言范围")
		}
		reason, _ := data["update_disabled_reason"].(string)
		if data["auto_update_supported"] != false || !strings.Contains(reason, "限流") || !strings.Contains(reason, "校验值") {
			t.Fatalf("拿不到 digest 时不能放行一键更新，实际 %#v", data)
		}
	})

	t.Run("Watchtower 托管：按原逻辑判断能否更新", func(t *testing.T) {
		t.Setenv("PANEL_UPDATE_MANAGER", "watchtower")
		t.Setenv("WATCHTOWER_HTTP_API_URL", "http://watchtower:8080")
		t.Setenv("WATCHTOWER_HTTP_API_TOKEN", "demo-token")
		t.Setenv("IMAGE_NAME", "linzixuanzz/daidai-panel:debian-full")
		data := decode(t, callCheckUpdate())
		if data["auto_update_supported"] != true || data["update_disabled_reason"] != "" || data["latest"] != "99.0.0" {
			t.Fatalf("Watchtower 不需要 digest，应照常放行，实际 %#v", data)
		}
	})

	t.Run("回退也失败：报限流本身", func(t *testing.T) {
		pageMode.Store("fail")
		recorder := callCheckUpdate()
		if recorder.Code == http.StatusOK {
			t.Fatalf("回退失败时不应回 200：%s", recorder.Body.String())
		}
		if !strings.Contains(recorder.Body.String(), "匿名访问已达上限") {
			t.Fatalf("回退失败时应报限流错误，实际 %s", recorder.Body.String())
		}
	})
}

// #177：静默更新遇到限流不通知、推迟到额度恢复；成功后才写上次检查时间；连续限流满 72 小时提醒一次。
func TestRunPanelAutoUpdateCheckHandlesRateLimit(t *testing.T) {
	testutil.SetupTestEnv(t)
	if err := model.SetConfig("auto_update_enabled", "true"); err != nil {
		t.Fatalf("enable auto update: %v", err)
	}

	var notifications []string
	oldNotify := sendAutoUpdateNotification
	sendAutoUpdateNotification = func(title, content string) {
		notifications = append(notifications, title+"|"+content)
	}
	t.Cleanup(func() { sendAutoUpdateNotification = oldNotify })

	resetAt := time.Now().Add(40 * time.Minute).Truncate(time.Second)
	var mode atomic.Value
	mode.Store("rate-limited")
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		switch mode.Load().(string) {
		case "rate-limited":
			w.Header().Set("X-RateLimit-Remaining", "0")
			w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(resetAt.Unix(), 10))
			w.WriteHeader(http.StatusForbidden)
		case "server-error":
			w.WriteHeader(http.StatusInternalServerError)
		default:
			// 比当前版本旧，巡检拿到后不会真的去执行更新
			_, _ = w.Write([]byte(`{"tag_name":"v0.0.1"}`))
		}
	}))
	defer server.Close()
	pointGitHubAPIAt(t, server.URL)

	// 1. 限流：不通知、不写上次检查时间、记下恢复时间与连续限流起点
	runPanelAutoUpdateCheck()
	if len(notifications) != 0 {
		t.Fatalf("限流不应发通知，实际 %v", notifications)
	}
	if got := model.GetConfig(autoUpdateLastCheckedAtKey, ""); got != "" {
		t.Fatalf("限流时不应写上次检查时间，实际 %q", got)
	}
	if got := model.GetConfig(autoUpdateNextCheckAtKey, ""); got != resetAt.Format(time.RFC3339) {
		t.Fatalf("下次检查应推迟到额度恢复时间 %s，实际 %q", resetAt.Format(time.RFC3339), got)
	}
	if model.GetConfig(autoUpdateRateLimitedSinceKey, "") == "" {
		t.Fatal("应记下连续限流的起点")
	}

	// 2. 恢复时间之前的巡检直接跳过，不再请求 GitHub
	runPanelAutoUpdateCheck()
	if hits.Load() != 1 {
		t.Fatalf("恢复时间之前不应再请求 GitHub，实际请求了 %d 次", hits.Load())
	}

	// 3. 连续限流满 72 小时：提醒一次，之后不再重复
	_ = model.SetConfig(autoUpdateNextCheckAtKey, "")
	_ = model.SetConfig(autoUpdateRateLimitedSinceKey, time.Now().Add(-73*time.Hour).Format(time.RFC3339))
	runPanelAutoUpdateCheck()
	if len(notifications) != 1 || !strings.Contains(notifications[0], "72 小时") || !strings.Contains(notifications[0], "GitHub Token") {
		t.Fatalf("连续限流满 72 小时应提醒一次并提示填 Token，实际 %v", notifications)
	}
	_ = model.SetConfig(autoUpdateNextCheckAtKey, "")
	runPanelAutoUpdateCheck()
	if len(notifications) != 1 {
		t.Fatalf("72 小时提醒只发一次，实际 %v", notifications)
	}

	// 未满 72 小时不提醒
	_ = model.SetConfig(autoUpdateNextCheckAtKey, "")
	_ = model.SetConfig(autoUpdateRateLimitNotifiedKey, "")
	_ = model.SetConfig(autoUpdateRateLimitedSinceKey, time.Now().Add(-71*time.Hour).Format(time.RFC3339))
	runPanelAutoUpdateCheck()
	if len(notifications) != 1 {
		t.Fatalf("未满 72 小时不应提醒，实际 %v", notifications)
	}

	// 4. 成功拿到 release：写上次检查时间，限流状态清零
	mode.Store("ok")
	_ = model.SetConfig(autoUpdateNextCheckAtKey, "")
	runPanelAutoUpdateCheck()
	if model.GetConfig(autoUpdateLastCheckedAtKey, "") == "" {
		t.Fatal("成功检查后应写上次检查时间")
	}
	for _, key := range []string{autoUpdateNextCheckAtKey, autoUpdateRateLimitedSinceKey, autoUpdateRateLimitNotifiedKey} {
		if got := model.GetConfig(key, ""); got != "" {
			t.Fatalf("成功检查后 %s 应清零，实际 %q", key, got)
		}
	}
	if len(notifications) != 1 {
		t.Fatalf("成功检查不应发通知，实际 %v", notifications)
	}
}

// #177：非限流的失败照旧通知，但同一文案 24 小时内只通知一次，且不把下一次检查推到 24 小时后。
func TestRunPanelAutoUpdateCheckDeduplicatesFailureNotifications(t *testing.T) {
	testutil.SetupTestEnv(t)
	if err := model.SetConfig("auto_update_enabled", "true"); err != nil {
		t.Fatalf("enable auto update: %v", err)
	}

	var notifications []string
	oldNotify := sendAutoUpdateNotification
	sendAutoUpdateNotification = func(title, content string) {
		notifications = append(notifications, title+"|"+content)
	}
	t.Cleanup(func() { sendAutoUpdateNotification = oldNotify })

	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	pointGitHubAPIAt(t, server.URL)

	runPanelAutoUpdateCheck()
	if len(notifications) != 1 || !strings.HasPrefix(notifications[0], "静默更新失败|") || !strings.Contains(notifications[0], "GitHub API 返回 500") {
		t.Fatalf("非限流失败应通知一次并带上状态码，实际 %v", notifications)
	}
	if got := model.GetConfig(autoUpdateLastCheckedAtKey, ""); got != "" {
		t.Fatalf("失败时不应写上次检查时间，实际 %q", got)
	}

	// 下一次巡检照常重试，但同一文案不重复通知
	runPanelAutoUpdateCheck()
	if hits.Load() != 2 {
		t.Fatalf("失败后下一次巡检应照常重试，实际请求 %d 次", hits.Load())
	}
	if len(notifications) != 1 {
		t.Fatalf("同一错误 24 小时内只通知一次，实际 %v", notifications)
	}

	// 超过 24 小时再通知
	_ = model.SetConfig(autoUpdateLastFailureAtKey, time.Now().Add(-25*time.Hour).Format(time.RFC3339))
	runPanelAutoUpdateCheck()
	if len(notifications) != 2 {
		t.Fatalf("超过 24 小时应再通知一次，实际 %v", notifications)
	}
}
