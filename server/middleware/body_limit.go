package middleware

import (
	"fmt"
	"net/http"

	"daidai-panel/pkg/response"

	"github.com/gin-gonic/gin"
)

// BodyLimit 给单条路由的请求体封顶，只挂在免登录的公开入口上（登录、初始化、换令牌、回调等）。
//
// 刻意不做全局默认上限：登录后的接口里脚本、备份、导入的体积差得很远，一刀切容易误伤。
// 公开入口则不同，任何人都能打，不封顶就等于给「发个超大 body 把内存打满」留了口子。
func BodyLimit(maxBytes int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		// 声明了长度且超限：不读 body，直接 413，省得白白收一遍数据。
		if c.Request.ContentLength > maxBytes {
			response.Error(c, http.StatusRequestEntityTooLarge, fmt.Sprintf("请求体过大，上限 %d KB", maxBytes/1024))
			c.Abort()
			return
		}
		// 没声明长度（分块传输）或长度如实：再用 MaxBytesReader 兜底，读到超限处就报错，
		// 后续的 JSON 解析会因此失败，按各接口原有的「请求参数错误」口径返回。
		if c.Request.Body != nil {
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBytes)
		}
		c.Next()
	}
}
