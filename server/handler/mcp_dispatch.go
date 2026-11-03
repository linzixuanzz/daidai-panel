package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
)

// mcpClientIPHeaders 与 middleware/client_ip.go 的 forwardedIPHeaders 保持一致。
// 回放请求带上原请求的这几个头和 RemoteAddr，调用审计与限流里记下的就是 MCP 客户端的真实 IP，
// 而且只在原请求确实来自可信代理时才会被采信（判定逻辑在 ResolveClientIP 里，这里不重复）。
var mcpClientIPHeaders = []string{
	"X-Forwarded-For",
	"X-Real-IP",
	"X-Original-Forwarded-For",
}

// engineDispatcher 把 MCP 工具调用在进程内回放到面板自己的 /api/v1 接口（engine.ServeHTTP）。
//
// 不经过网络、也不另开任何入口：回放的请求带着调用方的令牌，照常经过
// JWTAuth → OpenAPIAccess（scope、限流、审计）→ RequireRole，和客户端直接调开放 API 完全一样。
type engineDispatcher struct {
	engine        http.Handler
	authorization string
	remoteAddr    string
	host          string
	clientIP      http.Header
}

func newEngineDispatcher(engine http.Handler, origin *http.Request, authorization string) *engineDispatcher {
	clientIP := http.Header{}
	for _, name := range mcpClientIPHeaders {
		if values := origin.Header.Values(name); len(values) > 0 {
			clientIP[http.CanonicalHeaderKey(name)] = append([]string(nil), values...)
		}
	}
	return &engineDispatcher{
		engine:        engine,
		authorization: authorization,
		remoteAddr:    origin.RemoteAddr,
		host:          origin.Host,
		clientIP:      clientIP,
	}
}

func (d *engineDispatcher) Do(ctx context.Context, method, path string, query url.Values, body any) (int, []byte, error) {
	var reader io.Reader = http.NoBody
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return 0, nil, fmt.Errorf("序列化请求体失败：%w", err)
		}
		reader = bytes.NewReader(data)
	}

	target := "http://daidai-panel.internal/api/v1" + path
	if len(query) > 0 {
		target += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, target, reader)
	if err != nil {
		return 0, nil, fmt.Errorf("构造内部请求失败：%w", err)
	}
	if d.host != "" {
		req.Host = d.host
	}
	req.RemoteAddr = d.remoteAddr
	for name, values := range d.clientIP {
		req.Header[name] = append([]string(nil), values...)
	}
	// 不带原请求的 Origin：回放是面板自己发起的同源调用，不该再过一遍 CORS。
	req.Header.Set("Authorization", d.authorization)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	recorder := httptest.NewRecorder()
	d.engine.ServeHTTP(recorder, req)
	return recorder.Code, recorder.Body.Bytes(), nil
}
