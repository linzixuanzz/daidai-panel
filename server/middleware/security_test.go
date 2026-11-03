package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// CSP 不再放行 eval 与 jsdelivr，但极验验证码要用的外域必须还在，否则登录验证码加载不出来。
func TestSecurityHeadersCSPDropsEvalAndCDN(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(SecurityHeaders())
	engine.GET("/", func(c *gin.Context) { c.Status(http.StatusNoContent) })

	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	csp := rec.Header().Get("Content-Security-Policy")

	for _, banned := range []string{"'unsafe-eval'", "jsdelivr"} {
		if strings.Contains(csp, banned) {
			t.Fatalf("CSP should not contain %s: %q", banned, csp)
		}
	}
	for _, directive := range strings.Split(csp, ";") {
		directive = strings.TrimSpace(directive)
		if strings.HasPrefix(directive, "script-src ") && !strings.Contains(directive, "https://*.geetest.com") {
			t.Fatalf("script-src must still allow GeeTest: %q", directive)
		}
	}
}
