package middleware_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"daidai-panel/database"
	"daidai-panel/middleware"
	"daidai-panel/model"
	"daidai-panel/testutil"

	"github.com/gin-gonic/gin"
)

func TestAppTokenDefaultDenyWithoutOpenAPIAccess(t *testing.T) {
	testutil.SetupTestEnv(t)

	token := testutil.MustCreateAppToken(t, "deny-without-scope", "tasks")
	engine := gin.New()

	handlerReached := false
	engine.GET("/private", middleware.JWTAuth(), middleware.RequireRole("viewer"), func(c *gin.Context) {
		handlerReached = true
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	req := httptest.NewRequest(http.MethodGet, "/private", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", rec.Code)
	}
	if handlerReached {
		t.Fatal("handler should not run when app token is not explicitly authorized")
	}
}

func TestAppTokenScopeCanPassOperatorRoute(t *testing.T) {
	testutil.SetupTestEnv(t)

	token := testutil.MustCreateAppToken(t, "operator-scope", "tasks")
	engine := gin.New()

	handlerReached := false
	engine.POST("/operator", middleware.JWTAuth(), middleware.OpenAPIAccess("tasks"), middleware.RequireRole("operator"), func(c *gin.Context) {
		handlerReached = true
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	req := httptest.NewRequest(http.MethodPost, "/operator", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if !handlerReached {
		t.Fatal("handler should run when app token scope is explicitly authorized")
	}
}

func TestOpenAPIAccessLogsCallsWithoutIncrementingPersistentCallCount(t *testing.T) {
	testutil.SetupTestEnv(t)

	app := testutil.MustCreateOpenApp(t, "daily-count-app", "tasks")
	token := testutil.MustCreateAccessToken(t, "app:"+app.AppKey, "app:"+app.Scopes)
	engine := gin.New()

	engine.GET("/tasks", middleware.JWTAuth(), middleware.OpenAPIAccess("tasks"), func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	req := httptest.NewRequest(http.MethodGet, "/tasks", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	var updated model.OpenApp
	if err := database.DB.First(&updated, app.ID).Error; err != nil {
		t.Fatalf("reload app: %v", err)
	}
	if updated.CallCount != 0 {
		t.Fatalf("expected persistent call_count to stay 0, got %d", updated.CallCount)
	}

	var logCount int64
	if err := database.DB.Model(&model.ApiCallLog{}).Where("app_id = ?", app.ID).Count(&logCount).Error; err != nil {
		t.Fatalf("count api logs: %v", err)
	}
	if logCount != 1 {
		t.Fatalf("expected 1 api call log, got %d", logCount)
	}
}

// APP #30：应用超出 rate_limit 时回 429，文案要带上每小时上限和去哪改；
// 开头保持「应用调用频率超限」不变，按文案前缀匹配的老调用方不受影响。
func TestOpenAPIAccessRateLimitMessageShowsLimitAndWhereToChange(t *testing.T) {
	testutil.SetupTestEnv(t)

	app := testutil.MustCreateOpenApp(t, "rate-limit-app", "tasks")
	if err := database.DB.Model(app).Update("rate_limit", 1).Error; err != nil {
		t.Fatalf("set rate limit: %v", err)
	}
	token := testutil.MustCreateAccessToken(t, "app:"+app.AppKey, "app:"+app.Scopes)
	engine := gin.New()
	engine.GET("/tasks", middleware.JWTAuth(), middleware.OpenAPIAccess("tasks"), func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	call := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/tasks", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		engine.ServeHTTP(rec, req)
		return rec
	}

	if rec := call(); rec.Code != http.StatusOK {
		t.Fatalf("第一次调用应放行，实际 %d body=%s", rec.Code, rec.Body.String())
	}
	rec := call()
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("超过每小时 1 次后应回 429，实际 %d body=%s", rec.Code, rec.Body.String())
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v body=%s", err, rec.Body.String())
	}
	want := "应用调用频率超限（每小时最多 1 次，可在面板「开放 API」里调整速率限制）"
	if body["error"] != want {
		t.Fatalf("429 文案不对：\n got %q\nwant %q", body["error"], want)
	}
}
