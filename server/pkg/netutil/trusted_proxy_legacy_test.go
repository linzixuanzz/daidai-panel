package netutil

import (
	"net"
	"strings"
	"testing"
)

// 审计 H-2：默认可信代理只能是本机地址，私网段一条都不能混进来。
func TestDefaultTrustedProxyCIDRsOnlyTrustLoopback(t *testing.T) {
	got := DefaultTrustedProxyCIDRs()
	want := []string{"127.0.0.1/32", "::1/128"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("expected default trusted proxies %q, got %q", want, got)
	}

	for _, cidr := range got {
		_, network, err := net.ParseCIDR(cidr)
		if err != nil {
			t.Fatalf("parse default %q: %v", cidr, err)
		}
		for _, private := range []string{"10.0.0.1", "172.17.0.1", "192.168.1.50"} {
			if network.Contains(net.ParseIP(private)) {
				t.Fatalf("default %q must not trust private address %s", cidr, private)
			}
		}
	}
}

// 返回的是副本，调用方改了不能污染包级默认值（注册表和迁移都依赖它）。
func TestTrustedProxyDefaultListsReturnCopies(t *testing.T) {
	DefaultTrustedProxyCIDRs()[0] = "0.0.0.0/0"
	LegacyDefaultTrustedProxyCIDRs()[0] = "0.0.0.0/0"

	if DefaultTrustedProxyCIDRs()[0] != "127.0.0.1/32" {
		t.Fatal("DefaultTrustedProxyCIDRs must return a copy")
	}
	if LegacyDefaultTrustedProxyCIDRs()[0] != "127.0.0.1/32" {
		t.Fatal("LegacyDefaultTrustedProxyCIDRs must return a copy")
	}
}

func TestIsLegacyDefaultTrustedProxyCIDRs(t *testing.T) {
	legacy := strings.Join(LegacyDefaultTrustedProxyCIDRs(), "\n")

	matches := map[string]string{
		"stored canonical": legacy,
		"reordered":        "192.168.0.0/16\n10.0.0.0/8\n::1/128\n172.16.0.0/12\n127.0.0.1/32",
		"comma separated":  "127.0.0.1/32, ::1/128, 10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16",
		"bare loopback":    "127.0.0.1\n::1\n10.0.0.0/8\n172.16.0.0/12\n192.168.0.0/16",
		"duplicated entry": legacy + "\n10.0.0.0/8",
		"crlf and spaces":  "  127.0.0.1/32\r\n::1/128\r\n10.0.0.0/8\r\n172.16.0.0/12\r\n192.168.0.0/16  ",
	}
	for name, value := range matches {
		if !IsLegacyDefaultTrustedProxyCIDRs(value) {
			t.Fatalf("%s: expected %q to be treated as legacy default", name, value)
		}
	}

	notMatches := map[string]string{
		"empty":         "",
		"blank":         " \n\t",
		"new default":   strings.Join(DefaultTrustedProxyCIDRs(), "\n"),
		"extra entry":   legacy + "\n203.0.113.0/24",
		"missing entry": "127.0.0.1/32\n::1/128\n10.0.0.0/8\n172.16.0.0/12",
		"swapped entry": "127.0.0.1/32\n::1/128\n10.0.0.0/8\n172.16.0.0/12\n192.168.1.0/24",
		"invalid value": legacy + "\nnot-an-ip",
		"only private":  "10.0.0.0/8\n172.16.0.0/12\n192.168.0.0/16",
		"docker bridge": "127.0.0.1/32\n::1/128\n172.17.0.1/32",
	}
	for name, value := range notMatches {
		if IsLegacyDefaultTrustedProxyCIDRs(value) {
			t.Fatalf("%s: %q must not be treated as legacy default", name, value)
		}
	}
}
