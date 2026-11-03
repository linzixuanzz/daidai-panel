package middleware

import (
	"net/http/httptest"
	"testing"
)

func TestResolveClientIPFromRequestUsesForwardedIPFromTrustedLocalProxy(t *testing.T) {
	if err := ConfigureTrustedProxyCIDRs(""); err != nil {
		t.Fatalf("reset trusted proxies: %v", err)
	}

	req := httptest.NewRequest("GET", "http://example.com/ping", nil)
	req.RemoteAddr = "192.168.1.2:34567"
	req.Header.Set("X-Forwarded-For", "198.51.100.45, 10.0.0.2")

	if got := ResolveClientIPFromRequest(req); got != "198.51.100.45" {
		t.Fatalf("expected forwarded public IP, got %q", got)
	}
}

func TestResolveClientIPFromRequestIgnoresSpoofedForwardedIPFromPublicRemote(t *testing.T) {
	if err := ConfigureTrustedProxyCIDRs(""); err != nil {
		t.Fatalf("reset trusted proxies: %v", err)
	}

	req := httptest.NewRequest("GET", "http://example.com/ping", nil)
	req.RemoteAddr = "203.0.113.9:45678"
	req.Header.Set("X-Forwarded-For", "198.51.100.45")

	if got := ResolveClientIPFromRequest(req); got != "203.0.113.9" {
		t.Fatalf("expected direct public remote IP, got %q", got)
	}
}

func TestResolveClientIPFromRequestFallsBackToRealIPHeader(t *testing.T) {
	if err := ConfigureTrustedProxyCIDRs(""); err != nil {
		t.Fatalf("reset trusted proxies: %v", err)
	}

	req := httptest.NewRequest("GET", "http://example.com/ping", nil)
	req.RemoteAddr = "127.0.0.1:23456"
	req.Header.Set("X-Real-IP", "198.51.100.88")

	if got := ResolveClientIPFromRequest(req); got != "198.51.100.88" {
		t.Fatalf("expected X-Real-IP to be used, got %q", got)
	}
}

// CF-Connecting-IP / True-Client-IP 没有可信组件会写，客户端伪造它们不能改变解析结果。
func TestResolveClientIPFromRequestIgnoresSpoofedCDNHeaders(t *testing.T) {
	if err := ConfigureTrustedProxyCIDRs(""); err != nil {
		t.Fatalf("reset trusted proxies: %v", err)
	}

	for _, header := range []string{"CF-Connecting-IP", "True-Client-IP"} {
		// 经过本机反代（可信跳）且带 XFF：仍按 XFF 链解析，伪造头不生效。
		req := httptest.NewRequest("GET", "http://example.com/ping", nil)
		req.RemoteAddr = "127.0.0.1:23456"
		req.Header.Set(header, "1.2.3.4")
		req.Header.Set("X-Forwarded-For", "198.51.100.45")
		if got := ResolveClientIPFromRequest(req); got != "198.51.100.45" {
			t.Fatalf("%s: expected XFF client IP, got %q", header, got)
		}

		// 经过本机反代但没有 XFF：落回反代地址本身，而不是伪造值。
		req = httptest.NewRequest("GET", "http://example.com/ping", nil)
		req.RemoteAddr = "127.0.0.1:23456"
		req.Header.Set(header, "1.2.3.4")
		if got := ResolveClientIPFromRequest(req); got != "127.0.0.1" {
			t.Fatalf("%s: expected remote addr when only spoofed header present, got %q", header, got)
		}

		// 公网直连：伪造头同样不生效。
		req = httptest.NewRequest("GET", "http://example.com/ping", nil)
		req.RemoteAddr = "203.0.113.9:45678"
		req.Header.Set(header, "1.2.3.4")
		if got := ResolveClientIPFromRequest(req); got != "203.0.113.9" {
			t.Fatalf("%s: expected direct remote IP, got %q", header, got)
		}
	}
}

func TestResolveClientIPFromRequestUsesConfiguredPublicTrustedProxy(t *testing.T) {
	if err := ConfigureTrustedProxyCIDRs("203.0.113.0/24"); err != nil {
		t.Fatalf("configure trusted proxies: %v", err)
	}
	t.Cleanup(func() {
		_ = ConfigureTrustedProxyCIDRs("")
	})

	req := httptest.NewRequest("GET", "http://example.com/ping", nil)
	req.RemoteAddr = "203.0.113.9:34567"
	req.Header.Set("X-Forwarded-For", "198.51.100.45, 203.0.113.20")

	if got := ResolveClientIPFromRequest(req); got != "198.51.100.45" {
		t.Fatalf("expected forwarded client IP behind configured trusted proxy, got %q", got)
	}
}
