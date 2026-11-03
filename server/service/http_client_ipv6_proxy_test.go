package service

import (
	"net/http"
	"net/url"
	"testing"
	"time"
)

// go.mod 里的 godebug urlstrictcolons=0 钉在这里：Go 1.26 起 url.Parse 默认拒绝不带方括号的 IPv6:端口，
// 老配置里这样写的代理地址会解析失败，NewHTTPClientWithProxy 就静默不设代理、直连出去。
// 去掉 go.mod 那一行，这条测试会失败。
func TestLegacyUnbracketedIPv6ProxyStillWorks(t *testing.T) {
	const legacyProxy = "http://240e:3b0::1:7890"

	parsed, err := url.Parse(legacyProxy)
	if err != nil {
		t.Fatalf("url.Parse(%q) must keep accepting unbracketed IPv6:port (godebug urlstrictcolons=0): %v", legacyProxy, err)
	}
	if parsed.Host != "240e:3b0::1:7890" {
		t.Fatalf("unexpected host %q", parsed.Host)
	}

	client := NewHTTPClientWithProxy(time.Second, legacyProxy)
	transport, ok := client.Transport.(*http.Transport)
	if !ok || transport.Proxy == nil {
		t.Fatalf("proxy must be set for %q", legacyProxy)
	}
	req, _ := http.NewRequest(http.MethodGet, "https://example.com/", nil)
	proxyURL, err := transport.Proxy(req)
	if err != nil || proxyURL == nil || proxyURL.Host != "240e:3b0::1:7890" {
		t.Fatalf("expected requests to go through %q, got %v (%v)", legacyProxy, proxyURL, err)
	}
}
