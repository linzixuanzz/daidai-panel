package middleware

import (
	"github.com/gin-gonic/gin"
)

// contentSecurityPolicy 是面板页面的 CSP（二进制 / Magisk 部署时由 Go 直接托管前端，这个头会落到页面上）。
//
//   - 不再允许 'unsafe-eval' 和 cdn.jsdelivr.net：前端（含 Monaco 编辑器）已全部本地打包，
//     没有任何地方从 CDN 加载资源，也不用 eval / new Function；留着只会放大 XSS 的危害。
//   - 极验验证码要从外域加载：gt4.js 在 static.geetest.com，加载 / 校验走 gcaptcha4.geetest.com 的 JSONP，
//     图片和样式也在 static.geetest.com；geevisit.com 是极验的备用域名。只有这两组域名单独放行。
const contentSecurityPolicy = "default-src 'self'; " +
	"script-src 'self' 'unsafe-inline' https://*.geetest.com https://*.geevisit.com; " +
	"style-src 'self' 'unsafe-inline' https://*.geetest.com https://*.geevisit.com; " +
	"img-src 'self' data: blob: https://*.geetest.com https://*.geevisit.com; " +
	"font-src 'self' data:; " +
	"connect-src 'self' ws: wss: https://*.geetest.com https://*.geevisit.com; " +
	"worker-src 'self' blob:"

func SecurityHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("X-Frame-Options", "DENY")
		c.Header("X-XSS-Protection", "1; mode=block")
		c.Header("Referrer-Policy", "strict-origin-when-cross-origin")
		c.Header("X-Robots-Tag", "noindex, nofollow, noarchive, nosnippet")
		c.Header("Content-Security-Policy", contentSecurityPolicy)
		c.Next()
	}
}
