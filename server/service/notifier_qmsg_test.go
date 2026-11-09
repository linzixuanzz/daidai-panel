package service

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"unicode/utf8"

	"daidai-panel/testutil"
)

// issue #179：Qmsg 改走 3.0 接口 /v3/send/{key}。
// 私聊不带任何收件人参数（发给 Key 绑定的 QQ），群发带 group=<群号>；旧的 qq 参数 3.0 已不认。

func TestQmsgSendURLIsV3(t *testing.T) {
	// 后面的用例会把地址指向 httptest，这一条单独锁住线上的真实地址：改错时只有它会红。
	if qmsgSendURL != "https://qmsg.zendee.cn/v3/send/" {
		t.Fatalf("Qmsg 发送地址 = %q，期望 3.0 的 https://qmsg.zendee.cn/v3/send/", qmsgSendURL)
	}
}

type qmsgCapturedRequest struct {
	method string
	path   string
	form   url.Values
}

// startQmsgServer 起一个假的 Qmsg 服务端：记下收到的请求，按给定状态码回给定的响应体。
func startQmsgServer(t *testing.T, status int, reply string) *[]qmsgCapturedRequest {
	t.Helper()
	testutil.SetupTestEnv(t)

	requests := &[]qmsgCapturedRequest{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Errorf("parse qmsg form: %v", err)
		}
		*requests = append(*requests, qmsgCapturedRequest{method: r.Method, path: r.URL.Path, form: r.PostForm})
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(reply))
	}))
	t.Cleanup(server.Close)

	old := qmsgSendURL
	qmsgSendURL = server.URL + "/v3/send/"
	t.Cleanup(func() { qmsgSendURL = old })
	return requests
}

const qmsgOKReply = `{"success":true,"message":"发送成功","code":0,"data":null}`

func TestSendQmsgPrivateSendsNoRecipientParams(t *testing.T) {
	requests := startQmsgServer(t, http.StatusOK, qmsgOKReply)

	// 私聊模式下即使旧配置里还留着 qq，也不能再传：3.0 固定发给 Key 绑定的 QQ。
	cfg := map[string]string{"key": "abc123", "mode": "send", "qq": "10001"}
	if err := sendQmsg(cfg, "标题", "正文"); err != nil {
		t.Fatalf("send qmsg: %v", err)
	}
	if len(*requests) != 1 {
		t.Fatalf("期望 1 次请求，实际 %d", len(*requests))
	}
	got := (*requests)[0]
	if got.method != http.MethodPost || got.path != "/v3/send/abc123" {
		t.Fatalf("请求 = %s %s，期望 POST /v3/send/abc123", got.method, got.path)
	}
	if got.form.Get("msg") != "标题\n正文" {
		t.Fatalf("msg = %q", got.form.Get("msg"))
	}
	if _, ok := got.form["group"]; ok {
		t.Fatalf("私聊不应带 group：%v", got.form)
	}
	if _, ok := got.form["qq"]; ok {
		t.Fatalf("3.0 不认 qq 参数，不应再传：%v", got.form)
	}
}

func TestSendQmsgGroupSendsGroupNumber(t *testing.T) {
	requests := startQmsgServer(t, http.StatusOK, qmsgOKReply)

	// 配置键名仍是 qq（兼容已存渠道），发出去要变成 group。
	cfg := map[string]string{"key": "abc123", "mode": "group", "qq": " 123456789 "}
	if err := sendQmsg(cfg, "标题", "正文"); err != nil {
		t.Fatalf("send qmsg: %v", err)
	}
	if len(*requests) != 1 {
		t.Fatalf("期望 1 次请求，实际 %d", len(*requests))
	}
	got := (*requests)[0]
	// 群发也走同一个 /v3/send 接口，不再是旧的 /group。
	if got.path != "/v3/send/abc123" {
		t.Fatalf("群发路径 = %q，期望 /v3/send/abc123", got.path)
	}
	if got.form.Get("group") != "123456789" {
		t.Fatalf("group = %q，期望 123456789", got.form.Get("group"))
	}
	if _, ok := got.form["qq"]; ok {
		t.Fatalf("3.0 不认 qq 参数，不应再传：%v", got.form)
	}
}

func TestSendQmsgGroupWithoutNumberFailsWithoutRequest(t *testing.T) {
	requests := startQmsgServer(t, http.StatusOK, qmsgOKReply)

	err := sendQmsg(map[string]string{"key": "abc123", "mode": "group", "qq": "  "}, "标题", "正文")
	if err == nil || !strings.Contains(err.Error(), "群发模式需要填写 QQ 群号") {
		t.Fatalf("群发未填群号应报错，实际: %v", err)
	}
	// 不填群号时 3.0 会退化成私聊（#179 的现象），所以必须在发请求之前就拦下。
	if len(*requests) != 0 {
		t.Fatalf("未填群号不应发出请求，实际 %d 次", len(*requests))
	}
}

func TestSendQmsgReportsFailureMessage(t *testing.T) {
	cases := []struct {
		name   string
		status int
		reply  string
		want   string
	}{
		// 3.0 接口的真实形态：HTTP 400 + JSON，原因在 message。
		{name: "v3 message on http 400", status: http.StatusBadRequest,
			reply: `{"success":false,"message":"key不存在","code":500,"data":null}`, want: "Qmsg 发送失败: key不存在"},
		// 没有 message 时回退 reason（旧接口的写法）。
		{name: "fallback to reason", status: http.StatusOK,
			reply: `{"success":false,"reason":"旧接口原因"}`, want: "Qmsg 发送失败: 旧接口原因"},
		// JSON 都解析不了才按 HTTP 状态报。
		{name: "non-json error page", status: http.StatusBadGateway,
			reply: `<html>bad gateway</html>`, want: "HTTP 502"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			startQmsgServer(t, tc.status, tc.reply)
			err := sendQmsg(map[string]string{"key": "abc123"}, "标题", "正文")
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("期望错误包含 %q，实际: %v", tc.want, err)
			}
		})
	}
}

func TestSendQmsgTruncatesTo1800Runes(t *testing.T) {
	requests := startQmsgServer(t, http.StatusOK, qmsgOKReply)

	// 全用中文：按字节截断会切出半个字，这里同时验证结果仍是合法 UTF-8。
	content := strings.Repeat("长", 2000)
	if err := sendQmsg(map[string]string{"key": "abc123"}, "标题", content); err != nil {
		t.Fatalf("send qmsg: %v", err)
	}
	if len(*requests) != 1 {
		t.Fatalf("期望 1 次请求，实际 %d", len(*requests))
	}
	msg := (*requests)[0].form.Get("msg")
	if n := utf8.RuneCountInString(msg); n != 1800 {
		t.Fatalf("msg 长度 = %d 字符，期望截断到 1800", n)
	}
	if !utf8.ValidString(msg) || !strings.HasPrefix(msg, "标题\n长") {
		t.Fatalf("截断后的 msg 不对：前缀 %q", string([]rune(msg)[:4]))
	}
}
