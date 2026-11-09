package netutil

import (
	"fmt"
	"net"
	"strings"
	"unicode"
)

// defaultTrustedProxyCIDRs 自 v3.4.4 起只信任本机（审计 H-2）。
// Docker 部署里 Go 后端前面是容器内 nginx（127.0.0.1），本机地址足够让它的 X-Forwarded-For 生效；
// 私网段不再默认信任：TCP 对端落在私网段时（Docker 网桥、内网反代、同网段攻击者），
// 对方可以随手伪造 X-Forwarded-For，一次性绕过 IP 白名单、登录限流和登录日志。
// 面板确实在宿主机 nginx / 内网反代后面的用户，要在设置页把反代的来源地址显式填进来。
var defaultTrustedProxyCIDRs = []string{
	"127.0.0.1/32",
	"::1/128",
}

// legacyDefaultTrustedProxyCIDRs 是 v3.4.3 及之前的默认值（本机 + 全部私网段）。
// 只给升级迁移用：库里存的正好是这份旧默认 ⇒ 用户从没改过，InitDefaultConfigs 会抬到新默认。
var legacyDefaultTrustedProxyCIDRs = []string{
	"127.0.0.1/32",
	"::1/128",
	"10.0.0.0/8",
	"172.16.0.0/12",
	"192.168.0.0/16",
}

func DefaultTrustedProxyCIDRs() []string {
	result := make([]string, len(defaultTrustedProxyCIDRs))
	copy(result, defaultTrustedProxyCIDRs)
	return result
}

func LegacyDefaultTrustedProxyCIDRs() []string {
	result := make([]string, len(legacyDefaultTrustedProxyCIDRs))
	copy(result, legacyDefaultTrustedProxyCIDRs)
	return result
}

// IsLegacyDefaultTrustedProxyCIDRs 判断一份已存的配置值是不是「旧默认」。
//
// 按集合比较而不是按字符串比较：库里的值写入时已规范化成「一行一个 CIDR」，正常就是旧默认原样的顺序；
// 但同一组网段换了顺序、写成逗号分隔、或者把 127.0.0.1 写成不带 /32 的形式，信任范围完全一样，
// 一样算「没改过」，一并迁移。多一条或少一条都不算（那是用户自己定的范围，不能动）。
// 空串不算旧默认：空串在读取时本来就回落到注册表默认值（现在是新默认），不需要迁移。
func IsLegacyDefaultTrustedProxyCIDRs(value string) bool {
	if len(splitTrustedProxyCIDRs(value)) == 0 {
		return false
	}
	cidrs, err := ParseTrustedProxyCIDRs(value)
	if err != nil || len(cidrs) != len(legacyDefaultTrustedProxyCIDRs) {
		return false
	}
	// ParseTrustedProxyCIDRs 已去重，长度相等 + 每一项都在旧默认里 ⇒ 两个集合相等。
	legacy := make(map[string]struct{}, len(legacyDefaultTrustedProxyCIDRs))
	for _, cidr := range legacyDefaultTrustedProxyCIDRs {
		legacy[cidr] = struct{}{}
	}
	for _, cidr := range cidrs {
		if _, ok := legacy[cidr]; !ok {
			return false
		}
	}
	return true
}

func NormalizeTrustedProxyCIDRs(value string) (string, error) {
	cidrs, err := ParseTrustedProxyCIDRs(value)
	if err != nil {
		return "", err
	}
	return strings.Join(cidrs, "\n"), nil
}

func ParseTrustedProxyCIDRs(value string) ([]string, error) {
	parts := splitTrustedProxyCIDRs(value)
	if len(parts) == 0 {
		return DefaultTrustedProxyCIDRs(), nil
	}

	result := make([]string, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))
	for _, part := range parts {
		canonical, err := normalizeTrustedProxyToken(part)
		if err != nil {
			return nil, fmt.Errorf("可信代理 CIDR/IP %q 无效", part)
		}
		if _, exists := seen[canonical]; exists {
			continue
		}
		seen[canonical] = struct{}{}
		result = append(result, canonical)
	}

	return result, nil
}

func ParseTrustedProxyNetworks(value string) ([]*net.IPNet, error) {
	cidrs, err := ParseTrustedProxyCIDRs(value)
	if err != nil {
		return nil, err
	}

	result := make([]*net.IPNet, 0, len(cidrs))
	for _, cidr := range cidrs {
		_, network, err := net.ParseCIDR(cidr)
		if err != nil {
			return nil, err
		}
		result = append(result, network)
	}

	return result, nil
}

func splitTrustedProxyCIDRs(value string) []string {
	return strings.FieldsFunc(value, func(r rune) bool {
		return unicode.IsSpace(r) || r == ',' || r == ';'
	})
}

func normalizeTrustedProxyToken(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("empty trusted proxy")
	}

	if strings.Contains(value, "/") {
		_, network, err := net.ParseCIDR(value)
		if err != nil {
			return "", err
		}
		return network.String(), nil
	}

	ip := net.ParseIP(value)
	if ip == nil {
		return "", fmt.Errorf("invalid IP")
	}

	if v4 := ip.To4(); v4 != nil {
		return v4.String() + "/32", nil
	}
	return ip.String() + "/128", nil
}
