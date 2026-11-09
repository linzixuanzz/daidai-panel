package service

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/smtp"
	"reflect"
	"strings"
	"sync"
	"testing"

	"daidai-panel/model"
	"daidai-panel/testutil"
)

// 这个文件守的是 issue #135 的头号兼容约束：调用方不传 content_type 时，各渠道发出去的报文与加这个能力之前逐字节一致。
//
// legacyPayloadGoldenJSON 不是手写的期望值，而是在改动之前（v3.2.8 的 notifier.go）用同一套用例真跑 sendToChannel
// 抓下来的请求（方法、路径、关键请求头、请求体）。所以这里比的是「改动前的真实报文」，不是「新代码和自己的包装函数比」。
// 以后若有意改变某个渠道的默认报文，要同时改这里的 golden，并在提交说明里写清楚为什么老行为可以变。
//
// 覆盖不到的渠道：igot / pushover 的接口地址写死在代码里、无法指到 httptest，
// 它们的发送函数这次也没有改动；qmsg 在 v3.4.4 换成了 3.0 接口（报文本来就和老版本不同，不再适合比 golden），
// 地址改成了包级变量 qmsgSendURL，由 notifier_qmsg_test.go 单独守；
// pushplus 的地址改成了变量，在 notifier_content_format_test.go 里单独守；
// serverchan 两代接口（Turbo 版、Server酱³）的地址后来也改成了包级变量，请求路径与请求体（title / desp）
// 由 notifier_serverchan_test.go 守。

type legacyCapturedRequest struct {
	Method  string            `json:"method"`
	URI     string            `json:"uri"`
	Headers map[string]string `json:"headers,omitempty"`
	Body    string            `json:"body"`
}

type legacyPayloadCase struct {
	name        string
	channelType string
	config      func(serverURL string) map[string]string
}

const (
	legacyPayloadTitle   = "任务<通知>"
	legacyPayloadContent = "第一行 <b>加粗</b> & \"引号\"\n第二行\t尾"
)

var legacyPayloadContext = map[string]string{"task_name": "签到"}

var legacyPayloadCases = []legacyPayloadCase{
	{name: "webhook", channelType: "webhook", config: func(u string) map[string]string { return map[string]string{"url": u + "/hook"} }},
	{name: "email", channelType: "email", config: func(u string) map[string]string {
		return map[string]string{"smtp_host": "smtp.example.com", "smtp_port": "587", "smtp_user": "a@example.com", "smtp_pass": "x", "to": "b@example.com"}
	}},
	{name: "telegram", channelType: "telegram", config: func(u string) map[string]string {
		return map[string]string{"token": "123:abc", "chat_id": "42", "api_host": u}
	}},
	{name: "dingtalk_default", channelType: "dingtalk", config: func(u string) map[string]string { return map[string]string{"webhook": u + "/ding"} }},
	{name: "dingtalk_text", channelType: "dingtalk", config: func(u string) map[string]string {
		return map[string]string{"webhook": u + "/ding", "msg_type": "text"}
	}},
	{name: "wecom_text", channelType: "wecom", config: func(u string) map[string]string {
		return map[string]string{"webhook": u + "/wecom", "mentioned_list": "a"}
	}},
	{name: "wecom_markdown", channelType: "wecom", config: func(u string) map[string]string {
		return map[string]string{"webhook": u + "/wecom", "msg_type": "markdown", "content_template": "**{{title}}** {{task_name}}\n{{content}}"}
	}},
	{name: "wecom_markdown_v2", channelType: "wecom", config: func(u string) map[string]string {
		return map[string]string{"webhook": u + "/wecom", "msg_type": "markdown_v2"}
	}},
	{name: "wecom_app_text", channelType: "wecom_app", config: func(u string) map[string]string {
		return map[string]string{"corp_id": "ww", "secret": "sec", "agent_id": "1000001", "base_url": u}
	}},
	{name: "wecom_app_markdown", channelType: "wecom_app", config: func(u string) map[string]string {
		return map[string]string{"corp_id": "ww", "secret": "sec", "agent_id": "1000001", "base_url": u, "msg_type": "markdown"}
	}},
	{name: "bark", channelType: "bark", config: func(u string) map[string]string { return map[string]string{"key": "k", "server": u} }},
	{name: "feishu", channelType: "feishu", config: func(u string) map[string]string { return map[string]string{"webhook": u + "/feishu"} }},
	{name: "gotify", channelType: "gotify", config: func(u string) map[string]string { return map[string]string{"server": u, "token": "gt"} }},
	{name: "pushdeer", channelType: "pushdeer", config: func(u string) map[string]string { return map[string]string{"key": "k", "server": u} }},
	{name: "pushme", channelType: "pushme", config: func(u string) map[string]string {
		return map[string]string{"key": "k", "server": u + "/pushme", "message_type": "markdown"}
	}},
	{name: "chanify", channelType: "chanify", config: func(u string) map[string]string { return map[string]string{"token": "ct", "server": u} }},
	{name: "discord", channelType: "discord", config: func(u string) map[string]string { return map[string]string{"webhook": u + "/discord"} }},
	{name: "slack", channelType: "slack", config: func(u string) map[string]string { return map[string]string{"webhook": u + "/slack"} }},
	{name: "ntfy", channelType: "ntfy", config: func(u string) map[string]string {
		return map[string]string{"topic": "tp", "server": u, "priority": "4", "token": "nt"}
	}},
	{name: "wxpusher_1", channelType: "wxpusher", config: func(u string) map[string]string {
		return map[string]string{"app_token": "AT", "uids": "U1", "server": u + "/wx"}
	}},
	{name: "wxpusher_2", channelType: "wxpusher", config: func(u string) map[string]string {
		return map[string]string{"app_token": "AT", "uids": "U1", "server": u + "/wx", "content_type": "2"}
	}},
	{name: "wxpusher_3", channelType: "wxpusher", config: func(u string) map[string]string {
		return map[string]string{"app_token": "AT", "uids": "U1", "server": u + "/wx", "content_type": "3"}
	}},
	{name: "custom", channelType: "custom", config: func(u string) map[string]string { return map[string]string{"url": u + "/custom"} }},
}

// Markdown / X-Markdown 是 ntfy 的 markdown 开关，改动前从来不发；抓下来是为了让「不许悄悄打开它」也能被断言。
var legacyCapturedHeaderKeys = []string{"Content-Type", "Title", "Priority", "Authorization", "X-Gotify-Key", "Markdown", "X-Markdown"}

// legacyPayloadConfig 按用例名取渠道配置，给格式映射的用例复用同一套 httptest 配置。
func legacyPayloadConfig(t *testing.T, name string) func(serverURL string) map[string]string {
	t.Helper()
	for _, tc := range legacyPayloadCases {
		if tc.name == name {
			return tc.config
		}
	}
	t.Fatalf("没有名为 %s 的渠道用例", name)
	return nil
}

// captureNotifyRequests 起一个记录请求的 httptest 源站并替换 SMTP 发送函数，
// 用 send 发一次，返回抓到的全部请求（邮件记成 Method=SMTP、Body=整封报文）。
// 响应体 {"success":true,"access_token":"tok"} 能同时通过各渠道的业务码校验，也能给企业微信应用发 token。
func captureNotifyRequests(t *testing.T, config func(serverURL string) map[string]string, channelType string, send func(ch model.NotifyChannel) error) []legacyCapturedRequest {
	t.Helper()

	var (
		mu       sync.Mutex
		captured []legacyCapturedRequest
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		headers := map[string]string{}
		for _, key := range legacyCapturedHeaderKeys {
			if v := r.Header.Get(key); v != "" {
				headers[key] = v
			}
		}
		mu.Lock()
		captured = append(captured, legacyCapturedRequest{Method: r.Method, URI: r.RequestURI, Headers: headers, Body: string(body)})
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"access_token":"tok"}`))
	}))
	defer server.Close()

	oldPlain := smtpSendMail
	oldTLS := smtpSendMailWithImplicitTLS
	defer func() {
		smtpSendMail = oldPlain
		smtpSendMailWithImplicitTLS = oldTLS
	}()
	smtpSendMail = func(addr string, auth smtp.Auth, from string, to []string, msg []byte) error {
		mu.Lock()
		captured = append(captured, legacyCapturedRequest{Method: "SMTP", URI: addr, Body: string(msg)})
		mu.Unlock()
		return nil
	}
	smtpSendMailWithImplicitTLS = func(addr, host string, auth smtp.Auth, from string, to []string, msg []byte) error {
		t.Errorf("unexpected implicit TLS send to %s", addr)
		return nil
	}

	raw, err := json.Marshal(config(server.URL))
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	ch := model.NotifyChannel{Type: channelType, Config: string(raw)}
	if err := send(ch); err != nil {
		t.Fatalf("send via %s: %v", channelType, err)
	}

	mu.Lock()
	defer mu.Unlock()
	// httptest 的端口每次不同，统一换成占位，golden 才能逐字节比。
	for i := range captured {
		captured[i].Body = strings.ReplaceAll(captured[i].Body, server.URL, "http://HOST")
	}
	return captured
}

func TestSendToChannelEmptyContentTypeKeepsLegacyPayloads(t *testing.T) {
	testutil.SetupTestEnv(t)

	golden := map[string][]legacyCapturedRequest{}
	if err := json.Unmarshal([]byte(legacyPayloadGoldenJSON), &golden); err != nil {
		t.Fatalf("parse golden: %v", err)
	}
	if len(golden) != len(legacyPayloadCases) {
		t.Fatalf("golden 有 %d 条、用例有 %d 条，两边必须一一对应", len(golden), len(legacyPayloadCases))
	}

	for _, tc := range legacyPayloadCases {
		tc := tc
		want, ok := golden[tc.name]
		if !ok {
			t.Fatalf("golden 里没有用例 %s", tc.name)
		}
		t.Run(tc.name, func(t *testing.T) {
			got := captureNotifyRequests(t, tc.config, tc.channelType, func(ch model.NotifyChannel) error {
				return sendToChannel(ch, legacyPayloadTitle, legacyPayloadContent, legacyPayloadContext, "")
			})
			assertLegacyRequestsEqual(t, got, want)
		})
	}

	// 邮件的 text / markdown 也必须是原来的 text/plain 报文：只有 html 才换成 text/html。
	for _, contentType := range []string{NotifyContentText, NotifyContentMarkdown} {
		contentType := contentType
		t.Run("email_"+contentType, func(t *testing.T) {
			got := captureNotifyRequests(t, legacyPayloadConfig(t, "email"), "email", func(ch model.NotifyChannel) error {
				return sendToChannel(ch, legacyPayloadTitle, legacyPayloadContent, legacyPayloadContext, contentType)
			})
			assertLegacyRequestsEqual(t, got, golden["email"])
		})
	}
}

func assertLegacyRequestsEqual(t *testing.T, got, want []legacyCapturedRequest) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("请求数不一致：got %d want %d\ngot=%#v", len(got), len(want), got)
	}
	for i := range want {
		// golden 里没有请求头时 JSON 省略了这个键，反序列化出来是 nil；与空 map 视为相同。
		sameHeaders := len(got[i].Headers) == 0 && len(want[i].Headers) == 0 || reflect.DeepEqual(got[i].Headers, want[i].Headers)
		if got[i].Method != want[i].Method || got[i].URI != want[i].URI || got[i].Body != want[i].Body || !sameHeaders {
			t.Fatalf("第 %d 个请求与改动前的报文不一致：\ngot  %#v\nwant %#v", i, got[i], want[i])
		}
	}
}

// legacyPayloadGoldenJSON：改动前（v3.2.8）用上面的用例真跑抓到的请求，见文件头注释。
const legacyPayloadGoldenJSON = `{
  "bark": [
    {
      "method": "POST",
      "uri": "/k",
      "headers": {
        "Content-Type": "application/json"
      },
      "body": "{\"body\":\"第一行 \\u003cb\\u003e加粗\\u003c/b\\u003e \\u0026 \\\"引号\\\"\\n第二行\\t尾\",\"title\":\"任务\\u003c通知\\u003e\"}"
    }
  ],
  "chanify": [
    {
      "method": "POST",
      "uri": "/v1/sender/ct",
      "headers": {
        "Content-Type": "application/json"
      },
      "body": "{\"text\":\"第一行 \\u003cb\\u003e加粗\\u003c/b\\u003e \\u0026 \\\"引号\\\"\\n第二行\\t尾\",\"title\":\"任务\\u003c通知\\u003e\"}"
    }
  ],
  "custom": [
    {
      "method": "POST",
      "uri": "/custom",
      "headers": {
        "Content-Type": "application/json"
      },
      "body": "{\"title\":\"任务\u003c通知\u003e\",\"content\":\"第一行 \u003cb\u003e加粗\u003c/b\u003e \u0026 \"引号\"\n第二行\t尾\"}"
    }
  ],
  "dingtalk_default": [
    {
      "method": "POST",
      "uri": "/ding",
      "headers": {
        "Content-Type": "application/json"
      },
      "body": "{\"markdown\":{\"text\":\"### 任务\\u003c通知\\u003e  \\n第一行 \\u003cb\\u003e加粗\\u003c/b\\u003e \\u0026 \\\"引号\\\"  \\n第二行\\t尾\",\"title\":\"任务\\u003c通知\\u003e\"},\"msgtype\":\"markdown\"}"
    }
  ],
  "dingtalk_text": [
    {
      "method": "POST",
      "uri": "/ding",
      "headers": {
        "Content-Type": "application/json"
      },
      "body": "{\"msgtype\":\"text\",\"text\":{\"content\":\"任务\\u003c通知\\u003e\\n第一行 \\u003cb\\u003e加粗\\u003c/b\\u003e \\u0026 \\\"引号\\\"\\n第二行\\t尾\"}}"
    }
  ],
  "discord": [
    {
      "method": "POST",
      "uri": "/discord",
      "headers": {
        "Content-Type": "application/json"
      },
      "body": "{\"embeds\":[{\"color\":3447003,\"description\":\"第一行 \\u003cb\\u003e加粗\\u003c/b\\u003e \\u0026 \\\"引号\\\"\\n第二行\\t尾\",\"title\":\"任务\\u003c通知\\u003e\"}]}"
    }
  ],
  "email": [
    {
      "method": "SMTP",
      "uri": "smtp.example.com:587",
      "body": "From: a@example.com\r\nTo: b@example.com\r\nSubject: 任务\u003c通知\u003e\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n第一行 \u003cb\u003e加粗\u003c/b\u003e \u0026 \"引号\"\n第二行\t尾"
    }
  ],
  "feishu": [
    {
      "method": "POST",
      "uri": "/feishu",
      "headers": {
        "Content-Type": "application/json"
      },
      "body": "{\"content\":{\"text\":\"任务\\u003c通知\\u003e\\n第一行 \\u003cb\\u003e加粗\\u003c/b\\u003e \\u0026 \\\"引号\\\"\\n第二行\\t尾\"},\"msg_type\":\"text\"}"
    }
  ],
  "gotify": [
    {
      "method": "POST",
      "uri": "/message",
      "headers": {
        "Content-Type": "application/json",
        "X-Gotify-Key": "gt"
      },
      "body": "{\"message\":\"第一行 \\u003cb\\u003e加粗\\u003c/b\\u003e \\u0026 \\\"引号\\\"\\n第二行\\t尾\",\"priority\":5,\"title\":\"任务\\u003c通知\\u003e\"}"
    }
  ],
  "ntfy": [
    {
      "method": "POST",
      "uri": "/tp",
      "headers": {
        "Authorization": "Bearer nt",
        "Priority": "4",
        "Title": "任务\u003c通知\u003e"
      },
      "body": "第一行 \u003cb\u003e加粗\u003c/b\u003e \u0026 \"引号\"\n第二行\t尾"
    }
  ],
  "pushdeer": [
    {
      "method": "POST",
      "uri": "/message/push",
      "headers": {
        "Content-Type": "application/json"
      },
      "body": "{\"desp\":\"第一行 \\u003cb\\u003e加粗\\u003c/b\\u003e \\u0026 \\\"引号\\\"\\n第二行\\t尾\",\"pushkey\":\"k\",\"text\":\"任务\\u003c通知\\u003e\"}"
    }
  ],
  "pushme": [
    {
      "method": "POST",
      "uri": "/pushme",
      "headers": {
        "Content-Type": "application/x-www-form-urlencoded"
      },
      "body": "content=%E7%AC%AC%E4%B8%80%E8%A1%8C+%3Cb%3E%E5%8A%A0%E7%B2%97%3C%2Fb%3E+%26+%22%E5%BC%95%E5%8F%B7%22%0A%E7%AC%AC%E4%BA%8C%E8%A1%8C%09%E5%B0%BE\u0026push_key=k\u0026title=%E4%BB%BB%E5%8A%A1%3C%E9%80%9A%E7%9F%A5%3E\u0026type=markdown"
    }
  ],
  "slack": [
    {
      "method": "POST",
      "uri": "/slack",
      "headers": {
        "Content-Type": "application/json"
      },
      "body": "{\"text\":\"*任务\\u003c通知\\u003e*\\n\\n第一行 \\u003cb\\u003e加粗\\u003c/b\\u003e \\u0026 \\\"引号\\\"\\n第二行\\t尾\"}"
    }
  ],
  "telegram": [
    {
      "method": "POST",
      "uri": "/bot123:abc/sendMessage",
      "headers": {
        "Content-Type": "application/json"
      },
      "body": "{\"chat_id\":\"42\",\"text\":\"任务\\u003c通知\\u003e\\n第一行 \\u003cb\\u003e加粗\\u003c/b\\u003e \\u0026 \\\"引号\\\"\\n第二行\\t尾\"}"
    }
  ],
  "webhook": [
    {
      "method": "POST",
      "uri": "/hook",
      "headers": {
        "Content-Type": "application/json"
      },
      "body": "{\"content\":\"第一行 \\u003cb\\u003e加粗\\u003c/b\\u003e \\u0026 \\\"引号\\\"\\n第二行\\t尾\",\"title\":\"任务\\u003c通知\\u003e\"}"
    }
  ],
  "wecom_app_markdown": [
    {
      "method": "GET",
      "uri": "/cgi-bin/gettoken?corpid=ww\u0026corpsecret=sec",
      "body": ""
    },
    {
      "method": "POST",
      "uri": "/cgi-bin/message/send?access_token=tok",
      "headers": {
        "Content-Type": "application/json"
      },
      "body": "{\"agentid\":1000001,\"duplicate_check_interval\":1800,\"enable_duplicate_check\":0,\"markdown\":{\"content\":\"**任务\\u003c通知\\u003e**\\n第一行 \\u003cb\\u003e加粗\\u003c/b\\u003e \\u0026 \\\"引号\\\"\\n第二行\\t尾\"},\"msgtype\":\"markdown\",\"toparty\":\"\",\"totag\":\"\",\"touser\":\"@all\"}"
    }
  ],
  "wecom_app_text": [
    {
      "method": "GET",
      "uri": "/cgi-bin/gettoken?corpid=ww\u0026corpsecret=sec",
      "body": ""
    },
    {
      "method": "POST",
      "uri": "/cgi-bin/message/send?access_token=tok",
      "headers": {
        "Content-Type": "application/json"
      },
      "body": "{\"agentid\":1000001,\"duplicate_check_interval\":1800,\"enable_duplicate_check\":0,\"enable_id_trans\":0,\"msgtype\":\"text\",\"safe\":0,\"text\":{\"content\":\"任务\\u003c通知\\u003e\\n第一行 \\u003cb\\u003e加粗\\u003c/b\\u003e \\u0026 \\\"引号\\\"\\n第二行\\t尾\"},\"toparty\":\"\",\"totag\":\"\",\"touser\":\"@all\"}"
    }
  ],
  "wecom_markdown": [
    {
      "method": "POST",
      "uri": "/wecom",
      "headers": {
        "Content-Type": "application/json"
      },
      "body": "{\"markdown\":{\"content\":\"**任务\\u003c通知\\u003e** 签到\\n第一行 \\u003cb\\u003e加粗\\u003c/b\\u003e \\u0026 \\\"引号\\\"\\n第二行\\t尾\"},\"msgtype\":\"markdown\"}"
    }
  ],
  "wecom_markdown_v2": [
    {
      "method": "POST",
      "uri": "/wecom",
      "headers": {
        "Content-Type": "application/json"
      },
      "body": "{\"markdown_v2\":{\"content\":\"**任务\\u003c通知\\u003e**\\n第一行 \\u003cb\\u003e加粗\\u003c/b\\u003e \\u0026 \\\"引号\\\"\\n第二行\\t尾\"},\"msgtype\":\"markdown_v2\"}"
    }
  ],
  "wecom_text": [
    {
      "method": "POST",
      "uri": "/wecom",
      "headers": {
        "Content-Type": "application/json"
      },
      "body": "{\"msgtype\":\"text\",\"text\":{\"content\":\"任务\\u003c通知\\u003e\\n第一行 \\u003cb\\u003e加粗\\u003c/b\\u003e \\u0026 \\\"引号\\\"\\n第二行\\t尾\",\"mentioned_list\":[\"a\"]}}"
    }
  ],
  "wxpusher_1": [
    {
      "method": "POST",
      "uri": "/wx",
      "headers": {
        "Content-Type": "application/json"
      },
      "body": "{\"appToken\":\"AT\",\"content\":\"任务\\u003c通知\\u003e\\n第一行 \\u003cb\\u003e加粗\\u003c/b\\u003e \\u0026 \\\"引号\\\"\\n第二行\\t尾\",\"contentType\":1,\"summary\":\"任务\\u003c通知\\u003e\",\"uids\":[\"U1\"]}"
    }
  ],
  "wxpusher_2": [
    {
      "method": "POST",
      "uri": "/wx",
      "headers": {
        "Content-Type": "application/json"
      },
      "body": "{\"appToken\":\"AT\",\"content\":\"\\u003ch1\\u003e任务\\u0026lt;通知\\u0026gt;\\u003c/h1\\u003e\\u003cbr/\\u003e\\u003cdiv style='white-space: pre-wrap;'\\u003e第一行 \\u0026lt;b\\u0026gt;加粗\\u0026lt;/b\\u0026gt; \\u0026amp; \\u0026#34;引号\\u0026#34;\\n第二行\\t尾\\u003c/div\\u003e\",\"contentType\":2,\"summary\":\"任务\\u003c通知\\u003e\",\"uids\":[\"U1\"]}"
    }
  ],
  "wxpusher_3": [
    {
      "method": "POST",
      "uri": "/wx",
      "headers": {
        "Content-Type": "application/json"
      },
      "body": "{\"appToken\":\"AT\",\"content\":\"## 任务\\u003c通知\\u003e\\n\\n第一行 \\u003cb\\u003e加粗\\u003c/b\\u003e \\u0026 \\\"引号\\\"\\n第二行\\t尾\",\"contentType\":3,\"summary\":\"任务\\u003c通知\\u003e\",\"uids\":[\"U1\"]}"
    }
  ]
}`
