package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"daidai-panel/config"

	"github.com/gin-gonic/gin"
)

func newCORSRouter(t *testing.T) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(CORS())
	engine.GET("/ping", func(c *gin.Context) { c.Status(http.StatusNoContent) })
	engine.POST("/login", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) })
	return engine
}

func TestCORSAllowsPrivateLANOrigin(t *testing.T) {
	prev := config.C
	t.Cleanup(func() { config.C = prev })
	config.C = &config.Config{CORS: config.CORSConfig{Origins: []string{"https://allowed.example.com"}}}

	engine := newCORSRouter(t)

	preflight := httptest.NewRequest(http.MethodOptions, "/login", nil)
	preflight.Host = "panel.internal:5700"
	preflight.Header.Set("Origin", "http://192.168.1.50:5700")
	preflight.Header.Set("Access-Control-Request-Method", http.MethodPost)
	preflight.Header.Set("Access-Control-Request-Headers", "Authorization,Content-Type")
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, preflight)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204 for LAN origin preflight, got %d", rec.Code)
	}
	if rec.Header().Get("Access-Control-Allow-Origin") != "http://192.168.1.50:5700" {
		t.Fatalf("expected origin echoed back, got %q", rec.Header().Get("Access-Control-Allow-Origin"))
	}
}

func TestCORSAllowsLoopbackIPOrigin(t *testing.T) {
	prev := config.C
	t.Cleanup(func() { config.C = prev })
	config.C = &config.Config{}

	engine := newCORSRouter(t)

	preflight := httptest.NewRequest(http.MethodOptions, "/ping", nil)
	preflight.Header.Set("Origin", "http://127.0.0.1:8080")
	preflight.Header.Set("Access-Control-Request-Method", http.MethodGet)
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, preflight)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204 for loopback IP preflight, got %d", rec.Code)
	}
}

func TestCORSRejectsForeignPublicOrigin(t *testing.T) {
	prev := config.C
	t.Cleanup(func() { config.C = prev })
	config.C = &config.Config{CORS: config.CORSConfig{Origins: []string{"https://panel.example.com"}}}

	engine := newCORSRouter(t)

	preflight := httptest.NewRequest(http.MethodOptions, "/ping", nil)
	preflight.Host = "panel.example.com"
	preflight.Header.Set("Origin", "https://evil.example.com")
	preflight.Header.Set("X-Forwarded-Host", "panel.example.com")
	preflight.Header.Set("Access-Control-Request-Method", http.MethodGet)
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, preflight)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for foreign public origin, got %d", rec.Code)
	}
	if rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("expected no allow-origin for foreign request, got %q", rec.Header().Get("Access-Control-Allow-Origin"))
	}
}

func TestCORSAcceptsForwardedHeaderHost(t *testing.T) {
	prev := config.C
	t.Cleanup(func() { config.C = prev })
	config.C = &config.Config{}

	engine := newCORSRouter(t)

	preflight := httptest.NewRequest(http.MethodOptions, "/ping", nil)
	preflight.Host = "backend.internal"
	preflight.Header.Set("Origin", "https://panel.example.com")
	preflight.Header.Set("Forwarded", `for=10.0.0.5;proto=https;host=panel.example.com`)
	preflight.Header.Set("Access-Control-Request-Method", http.MethodGet)
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, preflight)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204 with Forwarded host matching origin, got %d", rec.Code)
	}
	if rec.Header().Get("Access-Control-Allow-Origin") != "https://panel.example.com" {
		t.Fatalf("expected origin allowed via Forwarded header, got %q", rec.Header().Get("Access-Control-Allow-Origin"))
	}
}

func TestCORSAcceptsProxyOriginWhenForwardedHostDropsExternalPort(t *testing.T) {
	prev := config.C
	t.Cleanup(func() { config.C = prev })
	config.C = &config.Config{}

	engine := newCORSRouter(t)

	preflight := httptest.NewRequest(http.MethodOptions, "/login", nil)
	preflight.Host = "dd.221008.xyz"
	preflight.Header.Set("Origin", "https://dd.221008.xyz:5888")
	preflight.Header.Set("X-Forwarded-Host", "dd.221008.xyz")
	preflight.Header.Set("Access-Control-Request-Method", http.MethodPost)
	preflight.Header.Set("Access-Control-Request-Headers", "Authorization,Content-Type")
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, preflight)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204 when reverse proxy drops external port, got %d", rec.Code)
	}
	if rec.Header().Get("Access-Control-Allow-Origin") != "https://dd.221008.xyz:5888" {
		t.Fatalf("expected origin with external port to be echoed, got %q", rec.Header().Get("Access-Control-Allow-Origin"))
	}
}

func TestCORSAcceptsProxyOriginWithForwardedPort(t *testing.T) {
	prev := config.C
	t.Cleanup(func() { config.C = prev })
	config.C = &config.Config{}

	engine := newCORSRouter(t)

	preflight := httptest.NewRequest(http.MethodOptions, "/login", nil)
	preflight.Host = "backend.internal:5701"
	preflight.Header.Set("Origin", "https://panel.example.com:5888")
	preflight.Header.Set("X-Forwarded-Host", "panel.example.com")
	preflight.Header.Set("X-Forwarded-Port", "5888")
	preflight.Header.Set("X-Forwarded-Proto", "https")
	preflight.Header.Set("Access-Control-Request-Method", http.MethodPost)
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, preflight)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204 with X-Forwarded-Port matching origin, got %d", rec.Code)
	}
	if rec.Header().Get("Access-Control-Allow-Origin") != "https://panel.example.com:5888" {
		t.Fatalf("expected forwarded-port origin to be echoed, got %q", rec.Header().Get("Access-Control-Allow-Origin"))
	}
}

func TestCORSRejectsProxyOriginWhenForwardedPortConflicts(t *testing.T) {
	prev := config.C
	t.Cleanup(func() { config.C = prev })
	config.C = &config.Config{}

	engine := newCORSRouter(t)

	preflight := httptest.NewRequest(http.MethodOptions, "/login", nil)
	preflight.Host = "backend.internal:5701"
	preflight.Header.Set("Origin", "https://panel.example.com:5888")
	preflight.Header.Set("X-Forwarded-Host", "panel.example.com")
	preflight.Header.Set("X-Forwarded-Port", "443")
	preflight.Header.Set("Access-Control-Request-Method", http.MethodPost)
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, preflight)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 when X-Forwarded-Port conflicts with Origin port, got %d", rec.Code)
	}
	if rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("expected conflicting forwarded-port origin to be rejected, got %q", rec.Header().Get("Access-Control-Allow-Origin"))
	}
}

func TestCORSRejectsDifferentProxyHostnameEvenWhenOriginHasPort(t *testing.T) {
	prev := config.C
	t.Cleanup(func() { config.C = prev })
	config.C = &config.Config{}

	engine := newCORSRouter(t)

	preflight := httptest.NewRequest(http.MethodOptions, "/login", nil)
	preflight.Host = "panel.example.com"
	preflight.Header.Set("Origin", "https://evil.example.com:5888")
	preflight.Header.Set("X-Forwarded-Host", "panel.example.com")
	preflight.Header.Set("Access-Control-Request-Method", http.MethodPost)
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, preflight)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for different forwarded hostname, got %d", rec.Code)
	}
	if rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("expected different host origin to be rejected, got %q", rec.Header().Get("Access-Control-Allow-Origin"))
	}
}

// Origin: null 来自沙箱 iframe / data: / file: 页面，不能当成可信来源放行。
func TestCORSRejectsNullOrigin(t *testing.T) {
	prev := config.C
	t.Cleanup(func() { config.C = prev })
	config.C = &config.Config{}

	engine := newCORSRouter(t)

	preflight := httptest.NewRequest(http.MethodOptions, "/login", nil)
	preflight.Host = "panel.example.com"
	preflight.Header.Set("Origin", "null")
	preflight.Header.Set("Access-Control-Request-Method", http.MethodPost)
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, preflight)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for null origin preflight, got %d", rec.Code)
	}
	if rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("expected no allow-origin for null origin, got %q", rec.Header().Get("Access-Control-Allow-Origin"))
	}

	req := httptest.NewRequest(http.MethodPost, "/login", nil)
	req.Host = "panel.example.com"
	req.Header.Set("Origin", "null")
	rec = httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for null origin request, got %d", rec.Code)
	}
}

// 不带 Origin 的请求（脚本、APP）照常放行。
func TestCORSAllowsMissingOrigin(t *testing.T) {
	prev := config.C
	t.Cleanup(func() { config.C = prev })
	config.C = &config.Config{}

	engine := newCORSRouter(t)

	req := httptest.NewRequest(http.MethodPost, "/login", nil)
	req.Host = "panel.example.com"
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 without origin, got %d", rec.Code)
	}
}

func TestParseForwardedHostsExtractsMultipleHosts(t *testing.T) {
	hosts := parseForwardedHosts(`for=192.0.2.60;proto=http;host=example.com, for=198.51.100.17;host="cdn.example.com"`)
	if len(hosts) != 2 {
		t.Fatalf("expected 2 hosts, got %d (%v)", len(hosts), hosts)
	}
	if !strings.EqualFold(hosts[0], "example.com") || !strings.EqualFold(hosts[1], "cdn.example.com") {
		t.Fatalf("unexpected hosts: %v", hosts)
	}
}

func TestIsPrivateOrLoopbackOriginDistinguishesPublicIPs(t *testing.T) {
	cases := []struct {
		origin string
		want   bool
	}{
		{"http://192.168.1.10:5700", true},
		{"http://10.0.0.5", true},
		{"http://172.16.5.5:8080", true},
		{"http://127.0.0.1:5700", true},
		{"http://[::1]:5700", true},
		{"http://8.8.8.8", false},
		{"https://panel.example.com", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := isPrivateOrLoopbackOrigin(tc.origin); got != tc.want {
			t.Errorf("isPrivateOrLoopbackOrigin(%q)=%v, want %v", tc.origin, got, tc.want)
		}
	}
}
