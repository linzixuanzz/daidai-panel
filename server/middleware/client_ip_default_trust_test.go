package middleware

import (
	"net/http/httptest"
	"testing"
)

// 审计 H-2：默认配置下私网段不再是可信代理。来自 192.168.x / 172.17.x 的请求自己带 X-Forwarded-For
// 也不能改写客户端 IP，否则同网段的人（或经 Docker 网桥进来的请求）能随手绕过 IP 白名单和登录限流。
func TestResolveClientIPDefaultIgnoresForwardedHeadersFromPrivateRemote(t *testing.T) {
	if err := ConfigureTrustedProxyCIDRs(""); err != nil {
		t.Fatalf("reset trusted proxies: %v", err)
	}

	cases := []struct {
		remote string
		want   string
	}{
		{remote: "192.168.1.50:34567", want: "192.168.1.50"},
		{remote: "10.0.0.8:34567", want: "10.0.0.8"},
		{remote: "172.17.0.1:34567", want: "172.17.0.1"},
	}
	for _, tc := range cases {
		// 审计 PoC 里的两种写法：单个伪造值、伪造值 + 右侧私网段。
		for _, xff := range []string{"203.0.113.99", "203.0.113.99, 192.168.1.50"} {
			req := httptest.NewRequest("GET", "http://example.com/ping", nil)
			req.RemoteAddr = tc.remote
			req.Header.Set("X-Forwarded-For", xff)
			req.Header.Set("X-Real-IP", "203.0.113.98")
			if got := ResolveClientIPFromRequest(req); got != tc.want {
				t.Fatalf("remote %s xff %q: expected remote IP %q, got %q", tc.remote, xff, tc.want, got)
			}
		}
	}
}

// 默认配置下本机反代（Docker 容器内 nginx 就是 127.0.0.1）仍然可信：从右往左取第一个非可信地址，
// 客户端自己塞在最左边的伪造值不会被采信。
func TestResolveClientIPDefaultUsesRightmostUntrustedHopFromLoopbackProxy(t *testing.T) {
	if err := ConfigureTrustedProxyCIDRs(""); err != nil {
		t.Fatalf("reset trusted proxies: %v", err)
	}

	cases := []struct {
		name   string
		remote string
		xff    string
		want   string
	}{
		{name: "single client", remote: "127.0.0.1:23456", xff: "198.51.100.45", want: "198.51.100.45"},
		// 客户端伪造了 203.0.113.99，容器内 nginx 追加的是它看到的真实对端 198.51.100.45。
		{name: "spoofed left", remote: "127.0.0.1:23456", xff: "203.0.113.99, 198.51.100.45", want: "198.51.100.45"},
		// 宿主机 nginx 经 Docker 网桥转进来：私网网桥地址不再默认可信，取到的是网桥地址，
		// 这正是设置页提示「把 172.17.0.1 填进可信代理」的原因。
		{name: "docker bridge hop", remote: "127.0.0.1:23456", xff: "198.51.100.45, 172.17.0.1", want: "172.17.0.1"},
		// 本机地址本身仍被跳过。
		{name: "loopback hop", remote: "127.0.0.1:23456", xff: "198.51.100.45, 127.0.0.1", want: "198.51.100.45"},
		{name: "ipv6 loopback", remote: "[::1]:23456", xff: "203.0.113.99, 198.51.100.46", want: "198.51.100.46"},
	}
	for _, tc := range cases {
		req := httptest.NewRequest("GET", "http://example.com/ping", nil)
		req.RemoteAddr = tc.remote
		req.Header.Set("X-Forwarded-For", tc.xff)
		if got := ResolveClientIPFromRequest(req); got != tc.want {
			t.Fatalf("%s: expected %q, got %q", tc.name, tc.want, got)
		}
	}
}

// 用户按设置页提示把 Docker 网桥地址填进可信代理后，宿主机 nginx 后面的真实访客 IP 能被取出来。
func TestResolveClientIPUsesForwardedIPAfterConfiguringDockerBridge(t *testing.T) {
	if err := ConfigureTrustedProxyCIDRs("127.0.0.1/32\n::1/128\n172.17.0.1"); err != nil {
		t.Fatalf("configure trusted proxies: %v", err)
	}
	t.Cleanup(func() {
		_ = ConfigureTrustedProxyCIDRs("")
	})

	req := httptest.NewRequest("GET", "http://example.com/ping", nil)
	req.RemoteAddr = "127.0.0.1:23456"
	req.Header.Set("X-Forwarded-For", "203.0.113.99, 198.51.100.45, 172.17.0.1")
	if got := ResolveClientIPFromRequest(req); got != "198.51.100.45" {
		t.Fatalf("expected real client behind docker bridge, got %q", got)
	}
}
