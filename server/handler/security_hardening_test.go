package handler_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"daidai-panel/database"
	"daidai-panel/handler"
	"daidai-panel/model"
	"daidai-panel/pkg/validator"
	"daidai-panel/testutil"

	"github.com/gin-gonic/gin"
)

func newHardeningRouter() *gin.Engine {
	engine := gin.New()
	api := engine.Group("/api/v1")
	handler.NewAuthHandler().RegisterRoutes(api)
	handler.NewUserHandler().RegisterRoutes(api)
	handler.NewOpenAPIHandler().RegisterRoutes(api)
	return engine
}

// 设置新密码的入口都按新规则校验，文案把规则说全（APP 直接显示服务端文案）。
func TestSetPasswordEntriesRejectWeakPassword(t *testing.T) {
	testutil.SetupTestEnv(t)
	engine := newHardeningRouter()

	// 初始化：弱密码被拒，且不会建出用户
	rec := performJSONRequest(engine, http.MethodPost, "/api/v1/auth/init", `{"username":"admin","password":"123456"}`, nil, "198.51.100.1")
	if rec.Code != http.StatusBadRequest || decodeJSONMap(t, rec)["error"] != validator.NewPasswordRuleMessage {
		t.Fatalf("init with weak password: got %d %s", rec.Code, rec.Body.String())
	}
	var count int64
	database.DB.Model(&model.User{}).Count(&count)
	if count != 0 {
		t.Fatalf("expected no user created, got %d", count)
	}

	// 改密：旧密码对、新密码弱 → 400
	user := testutil.MustCreateLoginUser(t, "weak-pw-user", "admin", "OldPassword123!")
	token := testutil.MustCreateAccessToken(t, user.Username, user.Role)
	rec = performJSONRequest(engine, http.MethodPut, "/api/v1/auth/password",
		`{"old_password":"OldPassword123!","new_password":"abcdefgh"}`,
		map[string]string{"Authorization": "Bearer " + token}, "198.51.100.2")
	if rec.Code != http.StatusBadRequest || !strings.Contains(fmt.Sprint(decodeJSONMap(t, rec)["error"]), validator.NewPasswordRuleMessage) {
		t.Fatalf("change password with weak password: got %d %s", rec.Code, rec.Body.String())
	}

	// 建用户、重置密码：弱密码 → 400
	rec = performJSONRequest(engine, http.MethodPost, "/api/v1/users", `{"username":"newbie","password":"12345678","role":"viewer"}`,
		map[string]string{"Authorization": "Bearer " + token}, "198.51.100.3")
	if rec.Code != http.StatusBadRequest || decodeJSONMap(t, rec)["error"] != validator.NewPasswordRuleMessage {
		t.Fatalf("create user with weak password: got %d %s", rec.Code, rec.Body.String())
	}
	target := testutil.MustCreateUser(t, "reset-target", "viewer")
	rec = performJSONRequest(engine, http.MethodPut, fmt.Sprintf("/api/v1/users/%d/reset-password", target.ID), `{"password":"short1!"}`,
		map[string]string{"Authorization": "Bearer " + token}, "198.51.100.4")
	if rec.Code != http.StatusBadRequest || decodeJSONMap(t, rec)["error"] != validator.NewPasswordRuleMessage {
		t.Fatalf("reset password with weak password: got %d %s", rec.Code, rec.Body.String())
	}

	// 合规密码照常可用
	rec = performJSONRequest(engine, http.MethodPost, "/api/v1/users", `{"username":"newbie","password":"Newbie12345","role":"viewer"}`,
		map[string]string{"Authorization": "Bearer " + token}, "198.51.100.5")
	if rec.Code != http.StatusCreated {
		t.Fatalf("create user with valid password: got %d %s", rec.Code, rec.Body.String())
	}
}

// 老用户的弱密码照样能登录：登录绝不套用新规则。
func TestLoginStillAcceptsLegacyWeakPassword(t *testing.T) {
	testutil.SetupTestEnv(t)
	engine := newHardeningRouter()
	user := testutil.MustCreateLoginUser(t, "legacy-user", "admin", "123456")

	rec := performJSONRequest(engine, http.MethodPost, "/api/v1/auth/login", `{"username":"`+user.Username+`","password":"123456"}`, nil, "198.51.100.20")
	if rec.Code != http.StatusOK {
		t.Fatalf("legacy weak password login: got %d %s", rec.Code, rec.Body.String())
	}
}

// 并发首次初始化只能建出一个管理员。
func TestConcurrentInitCreatesSingleAdmin(t *testing.T) {
	testutil.SetupTestEnv(t)
	engine := newHardeningRouter()

	const workers = 8
	var wg sync.WaitGroup
	codes := make([]int, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			// 每个请求换一个来源 IP，避开 /auth/init 的限流，只考验互斥锁
			rec := performJSONRequest(engine, http.MethodPost, "/api/v1/auth/init",
				fmt.Sprintf(`{"username":"admin%d","password":"Admin12345"}`, i), nil, fmt.Sprintf("198.51.100.%d", 100+i))
			codes[i] = rec.Code
		}(i)
	}
	wg.Wait()

	success := 0
	for _, code := range codes {
		if code == http.StatusOK {
			success++
		}
	}
	var count int64
	database.DB.Model(&model.User{}).Count(&count)
	if success != 1 || count != 1 {
		t.Fatalf("expected exactly one admin, got %d successes and %d users (codes=%v)", success, count, codes)
	}
}

// /open-api/token 只数失败，且按桶分开计数：
//   - secret 错按「IP + app_key」计数，满 10 次只封这个 key（封住期间正确 secret 也拒），同 IP 的其他 key、别的 IP 不受影响；
//   - app_key 不存在按 IP 计数，满 10 次只封「不存在的 key」这条路径，同 IP 的已知 key 照常签发；
//   - 成功不计数，两类失败也不会互相累加。
func TestOpenAPITokenLimitsOnlyFailures(t *testing.T) {
	testutil.SetupTestEnv(t)
	engine := newHardeningRouter()
	appA := testutil.MustCreateOpenApp(t, "limit-app-a", "tasks")
	appB := testutil.MustCreateOpenApp(t, "limit-app-b", "tasks")
	goodA := fmt.Sprintf(`{"app_key":%q,"app_secret":%q}`, appA.AppKey, appA.AppSecret)
	goodB := fmt.Sprintf(`{"app_key":%q,"app_secret":%q}`, appB.AppKey, appB.AppSecret)
	wrongA := fmt.Sprintf(`{"app_key":%q,"app_secret":"wrong"}`, appA.AppKey)
	unknown := `{"app_key":"no-such-app","app_secret":"wrong"}`
	post := func(body, ip string) int {
		return performJSONRequest(engine, http.MethodPost, "/api/v1/open-api/token", body, nil, ip).Code
	}

	// 成功请求再多也不会被挡
	for i := 0; i < 30; i++ {
		if code := post(goodA, "127.0.0.1"); code != http.StatusOK {
			t.Fatalf("success #%d: got %d", i+1, code)
		}
	}

	// 两类失败分开计数：各 9 次加起来超过 10，但哪个桶都没满，正确凭据照常签发
	for i := 0; i < 9; i++ {
		if code := post(wrongA, "203.0.113.49"); code != http.StatusUnauthorized {
			t.Fatalf("mixed wrong secret #%d: got %d", i+1, code)
		}
		if code := post(unknown, "203.0.113.49"); code != http.StatusUnauthorized {
			t.Fatalf("mixed unknown key #%d: got %d", i+1, code)
		}
	}
	if code := post(goodA, "203.0.113.49"); code != http.StatusOK {
		t.Fatalf("buckets should not add up, got %d", code)
	}

	// key A 的 secret 连错 10 次都回 401
	for i := 0; i < 10; i++ {
		if code := post(wrongA, "203.0.113.50"); code != http.StatusUnauthorized {
			t.Fatalf("wrong secret #%d: got %d", i+1, code)
		}
	}
	// 第 11 次起 key A 在这个 IP 上被封，正确 secret 也不受理
	if code := post(goodA, "203.0.113.50"); code != http.StatusTooManyRequests {
		t.Fatalf("expected key A blocked after 10 failures, got %d", code)
	}
	// 同 IP 的 key B 照常签发
	if code := post(goodB, "203.0.113.50"); code != http.StatusOK {
		t.Fatalf("key B on same ip should not be blocked, got %d", code)
	}
	// 同 IP 的不存在 key 仍按自己的桶走：还没失败过，回 401 而不是 429
	if code := post(unknown, "203.0.113.50"); code != http.StatusUnauthorized {
		t.Fatalf("unknown key bucket should be independent, got %d", code)
	}
	// 别的 IP 用 key A 不受影响
	if code := post(goodA, "203.0.113.51"); code != http.StatusOK {
		t.Fatalf("key A on other ip should not be blocked, got %d", code)
	}

	// 不存在的 app_key 连错 10 次后，这条路径被封
	for i := 0; i < 10; i++ {
		if code := post(unknown, "203.0.113.52"); code != http.StatusUnauthorized {
			t.Fatalf("unknown key #%d: got %d", i+1, code)
		}
	}
	if code := post(unknown, "203.0.113.52"); code != http.StatusTooManyRequests {
		t.Fatalf("expected unknown key path blocked after 10 failures, got %d", code)
	}
	// 同 IP 的已知 key 照常签发
	if code := post(goodA, "203.0.113.52"); code != http.StatusOK {
		t.Fatalf("known key A should not be blocked by unknown-key bucket, got %d", code)
	}
	if code := post(goodB, "203.0.113.52"); code != http.StatusOK {
		t.Fatalf("known key B should not be blocked by unknown-key bucket, got %d", code)
	}
}

// /auth/refresh 加了限流：同一 IP 一分钟内超过 30 次回 429。
func TestRefreshIsRateLimited(t *testing.T) {
	testutil.SetupTestEnv(t)
	engine := newHardeningRouter()

	var last int
	for i := 0; i < 31; i++ {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/refresh", nil)
		req.RemoteAddr = "203.0.113.60:12345"
		req.Header.Set("Authorization", "Bearer not-a-token")
		rec := httptest.NewRecorder()
		engine.ServeHTTP(rec, req)
		last = rec.Code
		if i < 30 && rec.Code != http.StatusUnauthorized {
			t.Fatalf("refresh #%d: expected 401 for invalid token, got %d", i+1, rec.Code)
		}
	}
	if last != http.StatusTooManyRequests {
		t.Fatalf("expected 31st refresh to be rate limited, got %d", last)
	}
}

// 公开入口的请求体上限：声明长度超限直接 413；没声明长度的流式 body 由 MaxBytesReader 兜底。
func TestPublicEntriesRejectOversizedBody(t *testing.T) {
	testutil.SetupTestEnv(t)
	engine := newHardeningRouter()

	big := `{"username":"x","password":"` + strings.Repeat("a", 70<<10) + `"}`
	for _, path := range []string{"/api/v1/auth/login", "/api/v1/auth/init", "/api/v1/open-api/token", "/api/v1/auth/refresh"} {
		rec := performJSONRequest(engine, http.MethodPost, path, big, nil, "203.0.113.70")
		if rec.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("%s: expected 413 for oversized body, got %d %s", path, rec.Code, rec.Body.String())
		}
	}

	// 分块传输（ContentLength = -1）：读到上限处报错，按接口原有口径回 400，不会把整个 body 读进内存
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(big))
	req.ContentLength = -1
	req.RemoteAddr = "203.0.113.71:12345"
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("chunked oversized body: expected 400, got %d %s", rec.Code, rec.Body.String())
	}
}
