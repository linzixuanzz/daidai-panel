package handler_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"daidai-panel/config"
	"daidai-panel/database"
	"daidai-panel/handler"
	"daidai-panel/middleware"
	"daidai-panel/model"
	"daidai-panel/service"
	"daidai-panel/testutil"

	"github.com/gin-gonic/gin"
)

func newProtectedRouter() *gin.Engine {
	engine := gin.New()
	api := engine.Group("/api/v1")

	handler.NewAuthHandler().RegisterRoutes(api)
	handler.NewTaskHandler().RegisterRoutes(api)
	handler.NewScriptHandler().RegisterRoutes(api)
	handler.NewEnvHandler().RegisterRoutes(api)
	handler.NewLogHandler().RegisterRoutes(api)
	handler.NewNotificationHandler().RegisterRoutes(api)
	handler.NewSystemHandler().RegisterRoutes(api)
	handler.NewConfigHandler().RegisterRoutes(api)
	handler.NewSubscriptionHandler().RegisterRoutes(api)
	handler.NewSecurityHandler().RegisterRoutes(api)

	return engine
}

func performRequest(engine *gin.Engine, method, path string, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	for key, value := range headers {
		req.Header.Set(key, value)
	}

	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	return rec
}

func performJSONRequest(engine *gin.Engine, method, path string, body string, headers map[string]string, remoteIP string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	if remoteIP != "" {
		req.RemoteAddr = remoteIP + ":12345"
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}

	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	return rec
}

func decodeJSONMap(t *testing.T, rec *httptest.ResponseRecorder) map[string]interface{} {
	t.Helper()

	var payload map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	return payload
}

func TestOpenAPIPermissionMatrixRepresentativeRoutes(t *testing.T) {
	testutil.SetupTestEnv(t)

	engine := newProtectedRouter()

	cases := []struct {
		name   string
		token  string
		method string
		path   string
		want   int
	}{
		{
			name:   "tasks scope can access tasks list",
			token:  testutil.MustCreateAppToken(t, "tasks-app", "tasks"),
			method: http.MethodGet,
			path:   "/api/v1/tasks",
			want:   http.StatusOK,
		},
		{
			name:   "scripts scope can access scripts list",
			token:  testutil.MustCreateAppToken(t, "scripts-app", "scripts"),
			method: http.MethodGet,
			path:   "/api/v1/scripts",
			want:   http.StatusOK,
		},
		{
			name:   "envs scope can access env list",
			token:  testutil.MustCreateAppToken(t, "envs-app", "envs"),
			method: http.MethodGet,
			path:   "/api/v1/envs",
			want:   http.StatusOK,
		},
		{
			name:   "logs scope can access logs list",
			token:  testutil.MustCreateAppToken(t, "logs-app", "logs"),
			method: http.MethodGet,
			path:   "/api/v1/logs",
			want:   http.StatusOK,
		},
		{
			name:   "system scope can access system version",
			token:  testutil.MustCreateAppToken(t, "system-app", "system"),
			method: http.MethodGet,
			path:   "/api/v1/system/version",
			want:   http.StatusOK,
		},
		{
			name:   "empty scope is denied",
			token:  testutil.MustCreateAppToken(t, "empty-app", ""),
			method: http.MethodGet,
			path:   "/api/v1/system/version",
			want:   http.StatusForbidden,
		},
		{
			name:   "wrong scope is denied",
			token:  testutil.MustCreateAppToken(t, "tasks-only-app", "tasks"),
			method: http.MethodGet,
			path:   "/api/v1/system/version",
			want:   http.StatusForbidden,
		},
		{
			name:   "user only sse route rejects app token",
			token:  testutil.MustCreateAppToken(t, "logs-sse-app", "logs"),
			method: http.MethodGet,
			path:   "/api/v1/logs/1/stream",
			want:   http.StatusForbidden,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := performRequest(engine, tc.method, tc.path, map[string]string{
				"Authorization": "Bearer " + tc.token,
			})

			if rec.Code != tc.want {
				t.Fatalf("expected %d, got %d, body=%s", tc.want, rec.Code, rec.Body.String())
			}
		})
	}
}

func TestJWTAuthRejectsQueryTokenAndAcceptsAuthorizationHeader(t *testing.T) {
	testutil.SetupTestEnv(t)

	engine := newProtectedRouter()
	user := testutil.MustCreateUser(t, "viewer-user", "viewer")
	accessToken := testutil.MustCreateAccessToken(t, user.Username, user.Role)

	queryOnly := performRequest(engine, http.MethodGet, "/api/v1/system/version?token="+accessToken, nil)
	if queryOnly.Code != http.StatusUnauthorized {
		t.Fatalf("expected query token request to be rejected with 401, got %d", queryOnly.Code)
	}

	headerAuth := performRequest(engine, http.MethodGet, "/api/v1/system/version", map[string]string{
		"Authorization": "Bearer " + accessToken,
	})
	if headerAuth.Code != http.StatusOK {
		t.Fatalf("expected header auth request to succeed, got %d", headerAuth.Code)
	}
}

func TestRefreshOnlyAcceptsBearerRefreshToken(t *testing.T) {
	testutil.SetupTestEnv(t)

	engine := newProtectedRouter()
	user := testutil.MustCreateUser(t, "admin-user", "admin")
	refreshToken := testutil.MustCreateRefreshToken(t, user.Username, user.Role)
	accessToken := testutil.MustCreateAccessToken(t, user.Username, user.Role)

	valid := performRequest(engine, http.MethodPost, "/api/v1/auth/refresh", map[string]string{
		"Authorization": "Bearer " + refreshToken,
	})
	if valid.Code != http.StatusOK {
		t.Fatalf("expected refresh token request to succeed, got %d, body=%s", valid.Code, valid.Body.String())
	}

	payload := decodeJSONMap(t, valid)
	newToken, _ := payload["access_token"].(string)
	if newToken == "" {
		t.Fatalf("expected new access token in response, got %v", payload)
	}

	claims, err := middleware.ParseToken(newToken)
	if err != nil {
		t.Fatalf("parse refreshed access token: %v", err)
	}
	if claims.TokenType != "access" {
		t.Fatalf("expected refreshed token type access, got %s", claims.TokenType)
	}
	if claims.Username != user.Username {
		t.Fatalf("expected refreshed token username %s, got %s", user.Username, claims.Username)
	}

	accessAttempt := performRequest(engine, http.MethodPost, "/api/v1/auth/refresh", map[string]string{
		"Authorization": "Bearer " + accessToken,
	})
	if accessAttempt.Code != http.StatusUnauthorized {
		t.Fatalf("expected access token refresh to be rejected with 401, got %d", accessAttempt.Code)
	}

	rawAttempt := performRequest(engine, http.MethodPost, "/api/v1/auth/refresh", map[string]string{
		"Authorization": refreshToken,
	})
	if rawAttempt.Code != http.StatusUnauthorized {
		t.Fatalf("expected raw refresh token without Bearer to be rejected with 401, got %d", rawAttempt.Code)
	}
}

func TestCaptchaUpstreamFailurePolicyOnLogin(t *testing.T) {
	cases := []struct {
		name       string
		failMode   string
		wantStatus int
		wantError  string
		wantToken  bool
	}{
		{
			name:       "open mode allows login on upstream failure",
			failMode:   service.CaptchaFailModeOpen,
			wantStatus: http.StatusOK,
			wantToken:  true,
		},
		{
			name:       "strict mode blocks login on upstream failure",
			failMode:   service.CaptchaFailModeStrict,
			wantStatus: http.StatusServiceUnavailable,
			wantError:  "验证码服务暂时不可用，请稍后重试",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			testutil.SetupTestEnv(t)

			password := "Password123!"
			user := testutil.MustCreateLoginUser(t, "captcha-user", "admin", password)
			clientIP := "198.51.100.20"

			model.SetConfig("captcha_enabled", "true")
			model.SetConfig("captcha_id", "captcha-id")
			model.SetConfig("captcha_key", "secret-key")
			model.SetConfig("captcha_fail_mode", tc.failMode)

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Error(w, "upstream error", http.StatusBadGateway)
			}))
			defer server.Close()

			restore := service.SetGeeTestValidationForTesting(&http.Client{Timeout: time.Second}, server.URL)
			defer restore()

			engine := newProtectedRouter()
			body := `{"username":"` + user.Username + `","password":"` + password + `","captcha":{"lot_number":"lot-123","captcha_output":"captcha-output","pass_token":"pass-token","gen_time":"1711000000"}}`
			rec := performJSONRequest(engine, http.MethodPost, "/api/v1/auth/login", body, nil, clientIP)

			if rec.Code != tc.wantStatus {
				t.Fatalf("expected %d, got %d, body=%s", tc.wantStatus, rec.Code, rec.Body.String())
			}

			payload := decodeJSONMap(t, rec)
			if tc.wantToken {
				if accessToken, _ := payload["access_token"].(string); accessToken == "" {
					t.Fatalf("expected login success token, got %v", payload)
				}
				return
			}

			if got, _ := payload["error"].(string); got != tc.wantError {
				t.Fatalf("expected error %q, got %q", tc.wantError, got)
			}
			if got, _ := payload["captcha_service_unavailable"].(bool); !got {
				t.Fatalf("expected captcha_service_unavailable flag, got %v", payload)
			}
			if got, _ := payload["captcha_fail_mode"].(string); got != tc.failMode {
				t.Fatalf("expected captcha_fail_mode %q, got %q", tc.failMode, got)
			}
			if got, _ := payload["captcha_reason"].(string); got != "upstream_5xx" {
				t.Fatalf("expected captcha_reason upstream_5xx, got %q", got)
			}
		})
	}
}

func TestCaptchaEnabledRequiresCaptchaOnFirstLogin(t *testing.T) {
	testutil.SetupTestEnv(t)

	password := "Password123!"
	user := testutil.MustCreateLoginUser(t, "captcha-first-user", "admin", password)
	model.SetConfig("captcha_enabled", "true")
	model.SetConfig("captcha_id", "captcha-id")
	model.SetConfig("captcha_key", "secret-key")

	engine := newProtectedRouter()
	body := `{"username":"` + user.Username + `","password":"` + password + `"}`
	rec := performJSONRequest(engine, http.MethodPost, "/api/v1/auth/login", body, nil, "198.51.100.30")

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected missing captcha to be rejected with 401, got %d, body=%s", rec.Code, rec.Body.String())
	}

	payload := decodeJSONMap(t, rec)
	if got, _ := payload["captcha_required"].(bool); !got {
		t.Fatalf("expected captcha_required flag, got %v", payload)
	}
	if got, _ := payload["captcha_id"].(string); got != "captcha-id" {
		t.Fatalf("expected captcha_id captcha-id, got %q", got)
	}
	if got, _ := payload["require_after_failures"].(float64); got != 0 {
		t.Fatalf("expected require_after_failures 0 for every-login captcha, got %v", got)
	}
	// 原有中文文案不许变：Web 与存量 APP 都在读它。
	if got, _ := payload["error"].(string); got != "请先完成人机验证" {
		t.Fatalf("expected legacy error text to be preserved, got %q", got)
	}
	// 「压根没提交验证码」这条分支也要带稳定的机器可读 code，
	// APP 才能把它和「密码错」「要两步验证码」区分开——三者都是 401。
	assertLoginFailureContext(t, payload, handler.LoginCodeCaptchaRequired)
}

func TestLoginRecordsForwardedPublicIPFromTrustedProxyHop(t *testing.T) {
	testutil.SetupTestEnv(t)

	password := "Password123!"
	user := testutil.MustCreateLoginUser(t, "forwarded-ip-user", "admin", password)
	engine := newProtectedRouter()

	// 默认可信代理已收窄到只信任本机，这条用例要模拟「私网里的反代」，
	// 必须显式把 192.168.0.0/16 配成可信，不能再依赖默认值；用完恢复默认，别影响后面的用例。
	if err := middleware.ConfigureTrustedProxyCIDRs("192.168.0.0/16"); err != nil {
		t.Fatalf("configure trusted proxies: %v", err)
	}
	t.Cleanup(func() { _ = middleware.ConfigureTrustedProxyCIDRs("") })

	rec := performJSONRequest(engine, http.MethodPost, "/api/v1/auth/login", `{"username":"`+user.Username+`","password":"`+password+`"}`, map[string]string{
		"X-Forwarded-For": "198.51.100.45",
	}, "192.168.1.2")

	if rec.Code != http.StatusOK {
		t.Fatalf("expected login success, got %d, body=%s", rec.Code, rec.Body.String())
	}

	var loginLog model.LoginLog
	if err := database.DB.Order("id DESC").First(&loginLog).Error; err != nil {
		t.Fatalf("query login log: %v", err)
	}
	if loginLog.IP != "198.51.100.45" {
		t.Fatalf("expected login log IP 198.51.100.45, got %q", loginLog.IP)
	}
}

func TestLoginReplacesOnlySameClientTypeSessions(t *testing.T) {
	testutil.SetupTestEnv(t)

	password := "Password123!"
	user := testutil.MustCreateLoginUser(t, "session-scope-user", "admin", password)
	engine := newProtectedRouter()

	login := func(remoteIP, userAgent string, headers map[string]string) string {
		reqHeaders := map[string]string{
			"User-Agent": userAgent,
		}
		for key, value := range headers {
			reqHeaders[key] = value
		}

		rec := performJSONRequest(
			engine,
			http.MethodPost,
			"/api/v1/auth/login",
			`{"username":"`+user.Username+`","password":"`+password+`"}`,
			reqHeaders,
			remoteIP,
		)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected login success for %s, got %d, body=%s", remoteIP, rec.Code, rec.Body.String())
		}

		payload := decodeJSONMap(t, rec)
		token, _ := payload["access_token"].(string)
		if token == "" {
			t.Fatalf("expected access token, got %v", payload)
		}
		return token
	}

	assertProtectedStatus := func(token string, want int, name string) {
		rec := performRequest(engine, http.MethodGet, "/api/v1/system/version", map[string]string{
			"Authorization": "Bearer " + token,
		})
		if rec.Code != want {
			t.Fatalf("%s: expected protected request status %d, got %d, body=%s", name, want, rec.Code, rec.Body.String())
		}
	}

	assertSessions := func(want map[string]string) {
		var sessions []model.UserSession
		if err := database.DB.Where("user_id = ?", user.ID).Order("id ASC").Find(&sessions).Error; err != nil {
			t.Fatalf("query sessions: %v", err)
		}
		if len(sessions) != len(want) {
			t.Fatalf("expected %d sessions, got %d", len(want), len(sessions))
		}

		got := make(map[string]string, len(sessions))
		for _, session := range sessions {
			clientType := service.DetectSessionClientType(session.ClientType, "", session.UserAgent)
			got[clientType] = session.IP
		}

		for clientType, ip := range want {
			if got[clientType] != ip {
				t.Fatalf("expected %s session ip %s, got %s", clientType, ip, got[clientType])
			}
		}
	}

	assertClientNameContains := func(clientType, expected string) {
		var sessions []model.UserSession
		if err := database.DB.Where("user_id = ?", user.ID).Order("id ASC").Find(&sessions).Error; err != nil {
			t.Fatalf("query sessions for client name: %v", err)
		}

		for _, session := range sessions {
			gotType := service.DetectSessionClientType(session.ClientType, "", session.UserAgent)
			if gotType != clientType {
				continue
			}
			if !strings.Contains(session.ClientName, expected) {
				t.Fatalf("expected %s client name to contain %q, got %q", clientType, expected, session.ClientName)
			}
			return
		}

		t.Fatalf("expected to find %s session", clientType)
	}

	webTokenA := login("198.51.100.10", "Mozilla/5.0 (Windows NT 10.0; Win64; x64)", map[string]string{
		"X-Client-Type": "web",
		"X-Client-App":  "daidai-panel-web",
	})
	assertSessions(map[string]string{
		service.SessionClientWeb: "198.51.100.10",
	})
	assertProtectedStatus(webTokenA, http.StatusOK, "web token A should be valid after first login")

	appTokenA := login("198.51.100.20", "Dart/3.11 (dart:io)", map[string]string{
		"X-Client-Type":     "app",
		"X-Client-App":      "daidai-panel-app",
		"X-Client-Platform": "android",
		"X-Device-Model":    "Xiaomi 15 Pro",
		"X-Device-Name":     "umi",
		"X-OS-Version":      "15",
	})
	assertSessions(map[string]string{
		service.SessionClientWeb: "198.51.100.10",
		service.SessionClientApp: "198.51.100.20",
	})
	assertClientNameContains(service.SessionClientApp, "Xiaomi 15 Pro")
	assertProtectedStatus(webTokenA, http.StatusOK, "web token A should remain valid after app login")
	assertProtectedStatus(appTokenA, http.StatusOK, "app token A should be valid after first app login")

	webTokenB := login("198.51.100.30", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7)", map[string]string{
		"X-Client-Type": "web",
		"X-Client-App":  "daidai-panel-web",
	})
	assertSessions(map[string]string{
		service.SessionClientWeb: "198.51.100.30",
		service.SessionClientApp: "198.51.100.20",
	})
	assertProtectedStatus(webTokenA, http.StatusUnauthorized, "web token A should be revoked by second web login")
	assertProtectedStatus(appTokenA, http.StatusOK, "app token A should remain valid after second web login")
	assertProtectedStatus(webTokenB, http.StatusOK, "web token B should be valid after second web login")

	appTokenB := login("198.51.100.40", "DaidaiPanelApp/1.0.2+3 (Android; Flutter)", map[string]string{
		"X-Client-Type":     "app",
		"X-Client-App":      "daidai-panel-app",
		"X-Client-Platform": "ios",
		"X-Device-Model":    "iPhone16,2",
		"X-Device-Name":     "iPhone",
		"X-OS-Version":      "18.3",
	})
	assertSessions(map[string]string{
		service.SessionClientWeb: "198.51.100.30",
		service.SessionClientApp: "198.51.100.40",
	})
	assertClientNameContains(service.SessionClientWeb, "macOS")
	assertClientNameContains(service.SessionClientApp, "iPhone16,2")
	assertProtectedStatus(webTokenB, http.StatusOK, "web token B should remain valid after second app login")
	assertProtectedStatus(appTokenA, http.StatusUnauthorized, "app token A should be revoked by second app login")
	assertProtectedStatus(appTokenB, http.StatusOK, "app token B should be valid after second app login")
}

func TestCORSAllowsConfiguredAndSameOriginAuthorizationRequests(t *testing.T) {
	testutil.SetupTestEnv(t)

	engine := gin.New()
	engine.Use(middleware.CORS())
	engine.GET("/ping", func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})

	configured := httptest.NewRequest(http.MethodOptions, "/ping", nil)
	configured.Header.Set("Origin", "https://allowed.example.com")
	configured.Header.Set("Access-Control-Request-Method", http.MethodGet)
	configured.Header.Set("Access-Control-Request-Headers", "Authorization")
	configuredRec := httptest.NewRecorder()
	engine.ServeHTTP(configuredRec, configured)

	if configuredRec.Code != http.StatusNoContent {
		t.Fatalf("expected configured origin preflight status 204, got %d", configuredRec.Code)
	}
	if configuredRec.Header().Get("Access-Control-Allow-Origin") != "https://allowed.example.com" {
		t.Fatalf("expected configured origin to be echoed, got %q", configuredRec.Header().Get("Access-Control-Allow-Origin"))
	}
	if !strings.Contains(configuredRec.Header().Get("Access-Control-Allow-Headers"), "Authorization") {
		t.Fatalf("expected Authorization in allow headers, got %q", configuredRec.Header().Get("Access-Control-Allow-Headers"))
	}

	config.C.CORS.Origins = []string{"http://localhost:5173"}
	loopbackEngine := gin.New()
	loopbackEngine.Use(middleware.CORS())
	loopbackEngine.GET("/ping", func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})

	loopbackAlias := httptest.NewRequest(http.MethodOptions, "/ping", nil)
	loopbackAlias.Header.Set("Origin", "http://127.0.0.1:5173")
	loopbackAlias.Header.Set("Access-Control-Request-Method", http.MethodGet)
	loopbackAlias.Header.Set("Access-Control-Request-Headers", "Authorization")
	loopbackAliasRec := httptest.NewRecorder()
	loopbackEngine.ServeHTTP(loopbackAliasRec, loopbackAlias)

	if loopbackAliasRec.Code != http.StatusNoContent {
		t.Fatalf("expected loopback alias preflight status 204, got %d", loopbackAliasRec.Code)
	}
	if loopbackAliasRec.Header().Get("Access-Control-Allow-Origin") != "http://127.0.0.1:5173" {
		t.Fatalf("expected loopback alias origin to be echoed, got %q", loopbackAliasRec.Header().Get("Access-Control-Allow-Origin"))
	}

	sameOrigin := httptest.NewRequest(http.MethodOptions, "/ping", nil)
	sameOrigin.Host = "internal:5701"
	sameOrigin.Header.Set("Origin", "https://panel.example.com")
	sameOrigin.Header.Set("X-Forwarded-Host", "panel.example.com")
	sameOrigin.Header.Set("Access-Control-Request-Method", http.MethodGet)
	sameOrigin.Header.Set("Access-Control-Request-Headers", "Authorization")
	sameOriginRec := httptest.NewRecorder()
	engine.ServeHTTP(sameOriginRec, sameOrigin)

	if sameOriginRec.Code != http.StatusNoContent {
		t.Fatalf("expected proxied same-origin preflight status 204, got %d", sameOriginRec.Code)
	}
	if sameOriginRec.Header().Get("Access-Control-Allow-Origin") != "https://panel.example.com" {
		t.Fatalf("expected proxied same-origin to be allowed, got %q", sameOriginRec.Header().Get("Access-Control-Allow-Origin"))
	}

	foreign := httptest.NewRequest(http.MethodOptions, "/ping", nil)
	foreign.Host = "panel.example.com"
	foreign.Header.Set("Origin", "https://evil.example.com")
	foreign.Header.Set("X-Forwarded-Host", "panel.example.com")
	foreign.Header.Set("Access-Control-Request-Method", http.MethodGet)
	foreign.Header.Set("Access-Control-Request-Headers", "Authorization")
	foreignRec := httptest.NewRecorder()
	engine.ServeHTTP(foreignRec, foreign)

	if foreignRec.Code != http.StatusForbidden {
		t.Fatalf("expected foreign origin preflight status 403, got %d", foreignRec.Code)
	}
	if foreignRec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("expected foreign origin to be rejected, got %q", foreignRec.Header().Get("Access-Control-Allow-Origin"))
	}
}

// mustEnableTwoFactor 给指定用户开启两步验证，返回其 TOTP 密钥。
func mustEnableTwoFactor(t *testing.T, userID uint) string {
	t.Helper()

	secret, _, err := service.SetupTwoFactor(userID)
	if err != nil {
		t.Fatalf("setup 2fa: %v", err)
	}
	if err := database.DB.Model(&model.TwoFactorAuth{}).
		Where("user_id = ?", userID).
		Update("enabled", true).Error; err != nil {
		t.Fatalf("enable 2fa: %v", err)
	}

	return secret
}

// mustInvalidTOTPCode 造一个当前一定校验不过的验证码。
// ValidateTOTP 同时接受 -1/0/+1 三个时间窗口，所以不能随手写死一个 6 位数字，
// 必须逐个试到确认不命中为止。
func mustInvalidTOTPCode(t *testing.T, secret string) string {
	t.Helper()

	for i := 0; i < 32; i++ {
		candidate := fmt.Sprintf("%06d", 100000+i)
		if !service.ValidateTOTP(secret, candidate) {
			return candidate
		}
	}

	t.Fatalf("failed to build an invalid totp code")
	return ""
}

func countLoginLogs(t *testing.T, username string, status int) int64 {
	t.Helper()

	var count int64
	if err := database.DB.Model(&model.LoginLog{}).
		Where("username = ? AND status = ?", username, status).
		Count(&count).Error; err != nil {
		t.Fatalf("count login logs: %v", err)
	}

	return count
}

// assertLoginFailureContext 断言登录失败响应里既有稳定的机器可读 code，
// 也带齐了客户端下一次请求需要的人机验证上下文。
func assertLoginFailureContext(t *testing.T, payload map[string]interface{}, wantCode string) {
	t.Helper()

	if got, _ := payload["code"].(string); got != wantCode {
		t.Fatalf("expected code %q, got %q, body=%v", wantCode, got, payload)
	}
	for _, key := range []string{"captcha_required", "captcha_id", "captcha_threshold", "require_after_failures"} {
		if _, ok := payload[key]; !ok {
			t.Fatalf("expected response to carry %q, got %v", key, payload)
		}
	}
}

func TestLoginTwoFactorChallengeThenSuccess(t *testing.T) {
	testutil.SetupTestEnv(t)

	password := "Password123!"
	user := testutil.MustCreateLoginUser(t, "totp-user", "admin", password)
	secret := mustEnableTwoFactor(t, user.ID)

	engine := newProtectedRouter()
	clientIP := "198.51.100.60"

	// 第一步：不带 totp_code，服务端应回 2FA 挑战而不是普通的登录失败。
	challenge := performJSONRequest(engine, http.MethodPost, "/api/v1/auth/login",
		`{"username":"`+user.Username+`","password":"`+password+`"}`, nil, clientIP)
	if challenge.Code != http.StatusUnauthorized {
		t.Fatalf("expected 2fa challenge status 401, got %d, body=%s", challenge.Code, challenge.Body.String())
	}

	payload := decodeJSONMap(t, challenge)
	if got, _ := payload["two_factor_required"].(bool); !got {
		t.Fatalf("expected two_factor_required flag, got %v", payload)
	}
	// 原有中文文案不许变：Web 与存量 APP 都在读它。
	if got, _ := payload["error"].(string); got != "请输入两步验证码" {
		t.Fatalf("expected legacy error text to be preserved, got %q", got)
	}
	assertLoginFailureContext(t, payload, handler.LoginCodeTwoFactorRequired)

	// 「要码但还没给码」是中间态，不该被记成登录失败。
	if got := countLoginLogs(t, user.Username, 1); got != 0 {
		t.Fatalf("expected 2fa challenge not to write a failed login log, got %d", got)
	}

	// 第二步：带上正确的 totp_code，应当直接登录成功。
	success := performJSONRequest(engine, http.MethodPost, "/api/v1/auth/login",
		`{"username":"`+user.Username+`","password":"`+password+`","totp_code":"`+service.GenerateCurrentTOTPForTest(secret)+`"}`,
		nil, clientIP)
	if success.Code != http.StatusOK {
		t.Fatalf("expected login with valid totp to succeed, got %d, body=%s", success.Code, success.Body.String())
	}
	if token, _ := decodeJSONMap(t, success)["access_token"].(string); token == "" {
		t.Fatalf("expected access token after 2fa login, got %s", success.Body.String())
	}
}

func TestLoginInvalidTOTPIsStillRecordedAsFailure(t *testing.T) {
	testutil.SetupTestEnv(t)

	password := "Password123!"
	user := testutil.MustCreateLoginUser(t, "totp-wrong-user", "admin", password)
	secret := mustEnableTwoFactor(t, user.ID)

	engine := newProtectedRouter()
	rec := performJSONRequest(engine, http.MethodPost, "/api/v1/auth/login",
		`{"username":"`+user.Username+`","password":"`+password+`","totp_code":"`+mustInvalidTOTPCode(t, secret)+`"}`,
		nil, "198.51.100.61")

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected invalid totp status 401, got %d, body=%s", rec.Code, rec.Body.String())
	}

	payload := decodeJSONMap(t, rec)
	if got, _ := payload["two_factor_required"].(bool); !got {
		t.Fatalf("expected two_factor_required flag on invalid totp, got %v", payload)
	}
	if got, _ := payload["error"].(string); got != "两步验证码错误" {
		t.Fatalf("expected legacy error text to be preserved, got %q", got)
	}
	assertLoginFailureContext(t, payload, handler.LoginCodeInvalidTOTP)

	// 「给了码但错了」是真失败，登录日志照记。
	if got := countLoginLogs(t, user.Username, 1); got != 1 {
		t.Fatalf("expected invalid totp to write exactly one failed login log, got %d", got)
	}
}

func TestLoginTwoFactorChallengeCarriesEnabledCaptchaContext(t *testing.T) {
	testutil.SetupTestEnv(t)

	password := "Password123!"
	user := testutil.MustCreateLoginUser(t, "totp-captcha-user", "admin", password)
	mustEnableTwoFactor(t, user.ID)

	model.SetConfig("captcha_enabled", "true")
	model.SetConfig("captcha_id", "captcha-id")
	model.SetConfig("captcha_key", "secret-key")

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"result":"success"}`))
	}))
	defer upstream.Close()

	restore := service.SetGeeTestValidationForTesting(&http.Client{Timeout: time.Second}, upstream.URL)
	defer restore()

	engine := newProtectedRouter()
	body := `{"username":"` + user.Username + `","password":"` + password +
		`","captcha":{"lot_number":"lot-123","captcha_output":"captcha-output","pass_token":"pass-token","gen_time":"1711000000"}}`
	rec := performJSONRequest(engine, http.MethodPost, "/api/v1/auth/login", body, nil, "198.51.100.62")

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 2fa challenge status 401, got %d, body=%s", rec.Code, rec.Body.String())
	}

	payload := decodeJSONMap(t, rec)
	if got, _ := payload["two_factor_required"].(bool); !got {
		t.Fatalf("expected two_factor_required flag, got %v", payload)
	}
	// 验证码是「每次登录都要过」，客户端拿到 2FA 挑战后必须知道第二次 POST 还要重做人机验证。
	if got, _ := payload["captcha_required"].(bool); !got {
		t.Fatalf("expected captcha_required true when captcha is enabled, got %v", payload)
	}
	if got, _ := payload["captcha_id"].(string); got != "captcha-id" {
		t.Fatalf("expected captcha_id captcha-id, got %q", got)
	}
}

// 验证码「提交了但没过」与「压根没提交」是两条不同分支，都得回同一个 code。
// 这条覆盖的是校验失败分支（captcha_invalid）。
func TestLoginInvalidCaptchaCarriesMachineReadableCode(t *testing.T) {
	testutil.SetupTestEnv(t)

	password := "Password123!"
	user := testutil.MustCreateLoginUser(t, "captcha-invalid-user", "admin", password)

	model.SetConfig("captcha_enabled", "true")
	model.SetConfig("captcha_id", "captcha-id")
	model.SetConfig("captcha_key", "secret-key")

	// 上游返回 200 + result=fail：这是「用户没过人机验证」，不是上游故障，
	// 因此不能走 503 的 captcha_service_unavailable 那条分支。
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"result":"fail","reason":"invalid_lot_number"}`))
	}))
	defer upstream.Close()

	restore := service.SetGeeTestValidationForTesting(&http.Client{Timeout: time.Second}, upstream.URL)
	defer restore()

	engine := newProtectedRouter()
	body := `{"username":"` + user.Username + `","password":"` + password +
		`","captcha":{"lot_number":"lot-123","captcha_output":"captcha-output","pass_token":"pass-token","gen_time":"1711000000"}}`
	rec := performJSONRequest(engine, http.MethodPost, "/api/v1/auth/login", body, nil, "198.51.100.81")

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected invalid captcha status 401, got %d, body=%s", rec.Code, rec.Body.String())
	}

	payload := decodeJSONMap(t, rec)
	// captcha_invalid 是这条分支独有的标志，用它把自己和「没提交验证码」那条区分开，
	// 避免两条分支互相顶替、只覆盖到其中一条。
	if got, _ := payload["captcha_invalid"].(bool); !got {
		t.Fatalf("expected captcha_invalid flag, got %v", payload)
	}
	if got, _ := payload["captcha_reason"].(string); got != "invalid_lot_number" {
		t.Fatalf("expected captcha_reason invalid_lot_number, got %q", got)
	}
	// 原有中文文案不许变：Web 与存量 APP 都在读它。
	if got, _ := payload["error"].(string); got != "验证码校验失败，请重新完成人机验证" {
		t.Fatalf("expected legacy error text to be preserved, got %q", got)
	}
	assertLoginFailureContext(t, payload, handler.LoginCodeCaptchaRequired)
}

// 账号锁定回的是 429，且响应体结构与其他登录失败不同（没有 captcha_* 上下文），
// 所以这里不能复用 assertLoginFailureContext，直接断 code。
func TestLoginLockedAccountCarriesMachineReadableCode(t *testing.T) {
	testutil.SetupTestEnv(t)

	password := "Password123!"
	user := testutil.MustCreateLoginUser(t, "locked-account-user", "admin", password)
	clientIP := "198.51.100.82"

	// 直接落一条已锁定的 LoginAttempt，而不是真的连发 5 次错误密码：
	// 登录路由挂着 5 次/分钟的限流中间件，真跑满 5 次之后第 6 个请求会被限流器
	// 以同样的 429 挡在 handler 之前，锁定分支根本执行不到——断言就变成了
	// 对限流响应的断言，看着是绿的，实际什么都没测到。
	now := time.Now()
	attempt := model.LoginAttempt{
		IP:        clientIP,
		Username:  user.Username,
		Count:     service.MaxLoginAttempts,
		LockedAt:  &now,
		ExpiresAt: now.Add(service.LockDuration),
	}
	if err := database.DB.Create(&attempt).Error; err != nil {
		t.Fatalf("seed locked login attempt: %v", err)
	}

	engine := newProtectedRouter()
	// 故意用「正确」的密码：锁定检查排在校验密码之前，密码对不对都该被拦下。
	rec := performJSONRequest(engine, http.MethodPost, "/api/v1/auth/login",
		`{"username":"`+user.Username+`","password":"`+password+`"}`, nil, clientIP)

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("expected locked account status 429, got %d, body=%s", rec.Code, rec.Body.String())
	}

	payload := decodeJSONMap(t, rec)
	if got, _ := payload["code"].(string); got != handler.LoginCodeAccountLocked {
		t.Fatalf("expected code %q, got %q, body=%v", handler.LoginCodeAccountLocked, got, payload)
	}
	// locked / remaining_seconds 是 APP 做倒计时用的，和 code 一起构成锁定响应的契约。
	if got, _ := payload["locked"].(bool); !got {
		t.Fatalf("expected locked flag, got %v", payload)
	}
	if got, ok := payload["remaining_seconds"].(float64); !ok || got <= 0 {
		t.Fatalf("expected positive remaining_seconds, got %v", payload["remaining_seconds"])
	}
	// 原有中文文案不许变：Web 与存量 APP 都在读它。
	if got, _ := payload["error"].(string); !strings.HasPrefix(got, "账号已锁定") {
		t.Fatalf("expected legacy locked error text, got %q", got)
	}
}

func TestLoginInvalidCredentialsCarriesMachineReadableCode(t *testing.T) {
	testutil.SetupTestEnv(t)

	password := "Password123!"
	user := testutil.MustCreateLoginUser(t, "wrong-password-user", "admin", password)
	clientIP := "198.51.100.83"

	engine := newProtectedRouter()
	rec := performJSONRequest(engine, http.MethodPost, "/api/v1/auth/login",
		`{"username":"`+user.Username+`","password":"WrongPassword456!"}`, nil, clientIP)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected wrong password status 401, got %d, body=%s", rec.Code, rec.Body.String())
	}

	payload := decodeJSONMap(t, rec)
	// 原有中文文案不许变：Web 与存量 APP 都在读它。
	if got, _ := payload["error"].(string); got != "用户名或密码错误" {
		t.Fatalf("expected legacy error text to be preserved, got %q", got)
	}
	if got, _ := payload["failed_attempts"].(float64); got != 1 {
		t.Fatalf("expected failed_attempts 1, got %v", payload["failed_attempts"])
	}
	assertLoginFailureContext(t, payload, handler.LoginCodeInvalidCredentials)

	// 用户不存在必须和密码错误共用同一个 code：
	// code 一旦分开，就等于给爆破方提供了「这个用户名存在」的机器可读枚举接口。
	unknown := performJSONRequest(engine, http.MethodPost, "/api/v1/auth/login",
		`{"username":"no-such-user","password":"WhateverPassword789!"}`, nil, clientIP)
	if unknown.Code != http.StatusUnauthorized {
		t.Fatalf("expected unknown user status 401, got %d, body=%s", unknown.Code, unknown.Body.String())
	}

	unknownPayload := decodeJSONMap(t, unknown)
	if got, _ := unknownPayload["error"].(string); got != "用户名或密码错误" {
		t.Fatalf("expected unknown user to reuse the same error text, got %q", got)
	}
	assertLoginFailureContext(t, unknownPayload, handler.LoginCodeInvalidCredentials)
}

func TestLoginRateLimitIsIndependentPerRoutePrefix(t *testing.T) {
	testutil.SetupTestEnv(t)

	// router.go 会把同一个 AuthHandler 分别注册到 /api/v1 与 /api，
	// 限流器如果挂在 handler 字段上，两个前缀就共用同一个按 IP 计数的桶。
	engine := gin.New()
	authHandler := handler.NewAuthHandler()
	authHandler.RegisterRoutes(engine.Group("/api/v1"))
	authHandler.RegisterRoutes(engine.Group("/api"))

	clientIP := "198.51.100.70"
	// 故意发不合法 body：限流中间件排在 handler 之前，照样计数，
	// 又不会碰 LoginAttempt 的锁定逻辑，避免和「账号已锁定」的 429 混淆。
	body := `{}`

	for i := 1; i <= 5; i++ {
		rec := performJSONRequest(engine, http.MethodPost, "/api/v1/auth/login", body, nil, clientIP)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected /api/v1 login attempt %d to reach handler with 400, got %d, body=%s", i, rec.Code, rec.Body.String())
		}
	}

	limited := performJSONRequest(engine, http.MethodPost, "/api/v1/auth/login", body, nil, clientIP)
	if limited.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 6th /api/v1 login to be rate limited with 429, got %d, body=%s", limited.Code, limited.Body.String())
	}

	legacy := performJSONRequest(engine, http.MethodPost, "/api/auth/login", body, nil, clientIP)
	if legacy.Code != http.StatusBadRequest {
		t.Fatalf("expected /api login bucket to be independent (want 400), got %d, body=%s", legacy.Code, legacy.Body.String())
	}
}

func TestSSEStreamRequiresAuthorizationHeader(t *testing.T) {
	testutil.SetupTestEnv(t)

	engine := newProtectedRouter()
	user := testutil.MustCreateUser(t, "sse-user", "viewer")
	accessToken := testutil.MustCreateAccessToken(t, user.Username, user.Role)

	okRec := performRequest(engine, http.MethodGet, "/api/v1/logs/1/stream", map[string]string{
		"Authorization": "Bearer " + accessToken,
	})
	if okRec.Code != http.StatusOK {
		t.Fatalf("expected SSE request with header auth to succeed, got %d, body=%s", okRec.Code, okRec.Body.String())
	}
	if !strings.HasPrefix(okRec.Header().Get("Content-Type"), "text/event-stream") {
		t.Fatalf("expected text/event-stream content type, got %q", okRec.Header().Get("Content-Type"))
	}
	if !strings.Contains(okRec.Body.String(), "event: done") {
		t.Fatalf("expected stream to send done event, got %q", okRec.Body.String())
	}

	queryOnlyRec := performRequest(engine, http.MethodGet, "/api/v1/logs/1/stream?token="+accessToken, nil)
	if queryOnlyRec.Code != http.StatusUnauthorized {
		t.Fatalf("expected query token SSE request to be rejected with 401, got %d", queryOnlyRec.Code)
	}
}
