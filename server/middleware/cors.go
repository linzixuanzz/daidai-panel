package middleware

import (
	"log"
	"net"
	"net/url"
	"strings"
	"sync"
	"time"

	"daidai-panel/config"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
)

func matchesConfiguredOrigin(origin string, allowedOrigins []string) bool {
	normalizedOrigin := normalizeConfiguredOrigin(origin)
	for _, allowed := range allowedOrigins {
		if normalizeConfiguredOrigin(allowed) == normalizedOrigin {
			return true
		}
	}
	return false
}

func normalizeConfiguredOrigin(origin string) string {
	trimmed := strings.TrimSpace(origin)
	if trimmed == "" {
		return ""
	}

	parsed, err := url.Parse(trimmed)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return strings.ToLower(trimmed)
	}

	hostname := strings.ToLower(parsed.Hostname())
	if isLoopbackHost(hostname) {
		hostname = "loopback"
	}

	port := parsed.Port()
	if port != "" {
		hostname = net.JoinHostPort(hostname, port)
	}

	return strings.ToLower(parsed.Scheme) + "://" + hostname
}

func isLoopbackHost(hostname string) bool {
	switch hostname {
	case "localhost", "127.0.0.1", "::1":
		return true
	default:
		return false
	}
}

func extractHost(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}

	if strings.Contains(value, ",") {
		value = strings.TrimSpace(strings.Split(value, ",")[0])
	}

	if parsed, err := url.Parse(value); err == nil && parsed.Host != "" {
		return strings.ToLower(parsed.Host)
	}

	return strings.ToLower(value)
}

func splitHostAndPort(value string) (string, string) {
	host := extractHost(value)
	if host == "" {
		return "", ""
	}

	if h, p, err := net.SplitHostPort(host); err == nil {
		return strings.ToLower(strings.Trim(h, "[]")), normalizePort(p)
	}

	// 普通域名/IPv4 的 host:port 没有方括号，可以用最后一个冒号拆端口。
	// IPv6 不带端口时会有多个冒号，不能按这个分支处理。
	if strings.Count(host, ":") == 1 {
		h, p, _ := strings.Cut(host, ":")
		if h != "" && normalizePort(p) != "" {
			return strings.ToLower(strings.Trim(h, "[]")), normalizePort(p)
		}
	}

	return strings.ToLower(strings.Trim(host, "[]")), ""
}

func normalizePort(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if strings.Contains(value, ",") {
		value = strings.TrimSpace(strings.Split(value, ",")[0])
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return ""
		}
	}
	return value
}

func defaultPortForScheme(scheme string) string {
	switch strings.ToLower(strings.TrimSpace(scheme)) {
	case "http":
		return "80"
	case "https":
		return "443"
	default:
		return ""
	}
}

func originParts(origin string) (string, string, string) {
	parsed, err := url.Parse(strings.TrimSpace(origin))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", "", ""
	}
	return strings.ToLower(parsed.Scheme), strings.ToLower(parsed.Hostname()), normalizePort(parsed.Port())
}

func hostMatchesOrigin(originScheme, originHostname, originPort, candidate, forwardedPort string) bool {
	candidateHostname, candidatePort := splitHostAndPort(candidate)
	if originHostname == "" || candidateHostname == "" || candidateHostname != originHostname {
		return false
	}

	if originPort == "" {
		defaultPort := defaultPortForScheme(originScheme)
		return candidatePort == "" || candidatePort == defaultPort
	}

	if candidatePort == originPort {
		return true
	}
	if candidatePort == "" {
		if forwardedPort != "" {
			return forwardedPort == originPort
		}
		if originPort == defaultPortForScheme(originScheme) {
			return true
		}
		// NAS / Nginx Proxy Manager 等多层反代经常只把外部域名放进 Host / X-Forwarded-Host，
		// 但把用户访问用的公网端口丢掉。此时 Origin 是 https://域名:端口，后端只看到域名。
		// 域名一致时按“反代同源但端口丢失”放行；不同域名仍会被拒绝，避免退化成全网 CORS。
		return true
	}

	return false
}

func isSameOriginRequest(c *gin.Context, origin string) bool {
	originScheme, originHostname, originPort := originParts(origin)
	if originHostname == "" {
		return false
	}
	forwardedPort := normalizePort(c.GetHeader("X-Forwarded-Port"))

	candidates := []string{
		c.Request.Host,
		c.GetHeader("X-Forwarded-Host"),
		c.GetHeader("X-Original-Host"),
	}
	if forwarded := c.GetHeader("Forwarded"); forwarded != "" {
		candidates = append(candidates, parseForwardedHosts(forwarded)...)
	}

	for _, candidate := range candidates {
		if hostMatchesOrigin(originScheme, originHostname, originPort, candidate, forwardedPort) {
			return true
		}
	}

	return false
}

// parseForwardedHosts 从 RFC 7239 `Forwarded` header 中解析所有 host= 字段。
// 例如：Forwarded: for=192.0.2.60;proto=http;host=example.com, for=198.51.100.17
func parseForwardedHosts(value string) []string {
	var hosts []string
	for _, segment := range strings.Split(value, ",") {
		for _, pair := range strings.Split(segment, ";") {
			pair = strings.TrimSpace(pair)
			if len(pair) < 5 {
				continue
			}
			if !strings.EqualFold(pair[:5], "host=") {
				continue
			}
			host := strings.Trim(pair[5:], `"`)
			if host != "" {
				hosts = append(hosts, host)
			}
		}
	}
	return hosts
}

// isPrivateOrLoopbackOrigin 判断 Origin 的 host 是否为 IP 且在私有/局域网/Loopback 网段。
// 命中后视为可信来源（典型场景：飞牛 OS / 群晖 / 家用 NAS 等通过 LAN IP 访问），跳过严格 CORS 检查。
// 域名 origin 不会命中本函数，仍需走 allowedOrigins 或同源校验。
func isPrivateOrLoopbackOrigin(origin string) bool {
	host := extractHost(origin)
	if host == "" {
		return false
	}
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	return isPrivateOrLocalIP(ip)
}

var (
	corsRejectLogOnce sync.Map
	corsRejectLogTTL  = 5 * time.Minute
)

func logCORSRejection(c *gin.Context, origin string) {
	key := origin + "|" + c.Request.Host
	now := time.Now()
	if last, ok := corsRejectLogOnce.Load(key); ok {
		if when, ok := last.(time.Time); ok && now.Sub(when) < corsRejectLogTTL {
			return
		}
	}
	corsRejectLogOnce.Store(key, now)

	log.Printf(
		"[CORS] 拒绝跨域请求 origin=%q host=%q X-Forwarded-Host=%q X-Forwarded-Port=%q X-Forwarded-Proto=%q Forwarded=%q method=%s path=%s — 如需放行请在 config.yaml 的 cors.origins 中加入该 origin",
		origin,
		c.Request.Host,
		c.GetHeader("X-Forwarded-Host"),
		c.GetHeader("X-Forwarded-Port"),
		c.GetHeader("X-Forwarded-Proto"),
		c.GetHeader("Forwarded"),
		c.Request.Method,
		c.Request.URL.Path,
	)
}

func CORS() gin.HandlerFunc {
	allowedOrigins := []string{
		"http://localhost:5173",
		"http://localhost:5700",
	}
	if config.C != nil && len(config.C.CORS.Origins) > 0 {
		allowedOrigins = config.C.CORS.Origins
	}

	return cors.New(cors.Config{
		AllowOriginWithContextFunc: func(c *gin.Context, origin string) bool {
			// 空 Origin 放行：脚本、APP 等非浏览器客户端本来就不带 Origin。
			// 不再放行 "null"：沙箱 iframe、data:/file: 页面发出的请求都是这个值，
			// 放行它等于让任意页面借沙箱绕过来源校验、带着凭据跨域读接口。
			if origin == "" {
				return true
			}
			if matchesConfiguredOrigin(origin, allowedOrigins) {
				return true
			}
			if isSameOriginRequest(c, origin) {
				return true
			}
			if isPrivateOrLoopbackOrigin(origin) {
				return true
			}
			logCORSRejection(c, origin)
			return false
		},
		AllowMethods: []string{"GET", "POST", "PUT", "DELETE", "OPTIONS", "PATCH"},
		// MCP-Protocol-Version / Mcp-Session-Id 给浏览器里的 MCP 客户端（例如网页版 Inspector，issue #128）用：
		// 它们每个请求都带协议版本头，预检不放行的话浏览器直接拦下，放行名单里的来源也连不上。
		// 这里只多放行两个请求头，Origin 的放行口径（上面的 AllowOriginWithContextFunc）不变。
		// ExposeHeaders 不用加 Mcp-Session-Id：面板的 MCP 跑无状态模式，SDK 既不读也不回写这个头。
		AllowHeaders:     []string{"Origin", "Content-Type", "Authorization", "Accept", "X-Requested-With", "MCP-Protocol-Version", "Mcp-Session-Id"},
		ExposeHeaders:    []string{"Content-Length", "Content-Disposition"},
		AllowCredentials: true,
		MaxAge:           12 * time.Hour,
	})
}
