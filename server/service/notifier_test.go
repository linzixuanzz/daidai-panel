package service

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/smtp"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"testing"

	"daidai-panel/model"
	"daidai-panel/testutil"
)

func TestSplitNotificationTargets(t *testing.T) {
	got := splitNotificationTargets("uid-a; uid-b,\nuid-c\tuid-d")
	want := []string{"uid-a", "uid-b", "uid-c", "uid-d"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected targets: got %v want %v", got, want)
	}
}

func TestSplitNotificationIntTargets(t *testing.T) {
	got, err := splitNotificationIntTargets("101; 102,103")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := []int{101, 102, 103}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected int targets: got %v want %v", got, want)
	}
}

func TestSplitNotificationIntTargetsRejectsInvalidValue(t *testing.T) {
	if _, err := splitNotificationIntTargets("101;abc"); err == nil {
		t.Fatal("expected invalid topic id to return an error")
	}
}

func TestSendEmailSelectsSSLMode(t *testing.T) {
	oldPlain := smtpSendMail
	oldTLS := smtpSendMailWithImplicitTLS
	defer func() {
		smtpSendMail = oldPlain
		smtpSendMailWithImplicitTLS = oldTLS
	}()

	cases := []struct {
		name string
		port string
		ssl  string
		want string
	}{
		{name: "auto 465 uses implicit TLS", port: "465", want: "tls"},
		{name: "explicit true uses implicit TLS", port: "587", ssl: "true", want: "tls"},
		{name: "explicit false keeps plain smtp", port: "465", ssl: "false", want: "plain"},
		{name: "auto non-465 keeps plain smtp", port: "587", ssl: "auto", want: "plain"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			calls := []string{}
			assertPayload := func(addr, host, from string, to []string, msg []byte) {
				if addr != "smtp.example.com:"+tc.port {
					t.Fatalf("unexpected addr: %s", addr)
				}
				if host != "smtp.example.com" {
					t.Fatalf("unexpected host: %s", host)
				}
				if from != "sender@example.com" {
					t.Fatalf("unexpected from: %s", from)
				}
				wantTo := []string{"one@example.com", "two@example.com"}
				if !reflect.DeepEqual(to, wantTo) {
					t.Fatalf("unexpected recipients: got %v want %v", to, wantTo)
				}
				body := string(msg)
				if !strings.Contains(body, "Subject: 标题") || !strings.Contains(body, "正文") {
					t.Fatalf("unexpected message body: %q", body)
				}
			}
			smtpSendMail = func(addr string, auth smtp.Auth, from string, to []string, msg []byte) error {
				calls = append(calls, "plain")
				assertPayload(addr, "smtp.example.com", from, to, msg)
				return nil
			}
			smtpSendMailWithImplicitTLS = func(addr, host string, auth smtp.Auth, from string, to []string, msg []byte) error {
				calls = append(calls, "tls")
				assertPayload(addr, host, from, to, msg)
				return nil
			}

			cfg := map[string]string{
				"smtp_host": "smtp.example.com",
				"smtp_port": tc.port,
				"smtp_user": "sender@example.com",
				"smtp_pass": "secret",
				"from":      "sender@example.com",
				"to":        "one@example.com, two@example.com",
			}
			if tc.ssl != "" {
				cfg["smtp_ssl"] = tc.ssl
			}
			if err := sendEmail(cfg, "标题", "正文"); err != nil {
				t.Fatalf("send email: %v", err)
			}
			if !reflect.DeepEqual(calls, []string{tc.want}) {
				t.Fatalf("unexpected send mode calls: got %v want %v", calls, []string{tc.want})
			}
		})
	}
}

func TestRenderNotificationTemplateWithContext(t *testing.T) {
	got := renderNotificationTemplateWithContext(
		"任务 {{task_name}} 在 {{ended_at}} {{status_text}}，退出码 {{exit_code}}",
		"标题",
		"正文",
		"",
		map[string]string{
			"task_name":   "签到任务",
			"ended_at":    "2026-03-22 12:00:00.000",
			"status_text": "失败",
			"exit_code":   "2",
		},
	)

	want := "任务 签到任务 在 2026-03-22 12:00:00.000 失败，退出码 2"
	if got != want {
		t.Fatalf("unexpected rendered template: got %q want %q", got, want)
	}
}

func TestBuildTelegramMessagesSplitsLongContent(t *testing.T) {
	content := strings.Repeat("日志内容\n", 900)
	messages := buildTelegramMessages("任务执行失败", content)
	if len(messages) < 2 {
		t.Fatalf("expected long telegram content to be split, got %d message(s)", len(messages))
	}

	for i, message := range messages {
		if !strings.Contains(message, "任务执行失败") {
			t.Fatalf("expected message %d to contain title, got %q", i, message)
		}
		if len([]rune(message)) > 3600 {
			t.Fatalf("expected telegram message %d to stay under safe limit, got %d runes", i, len([]rune(message)))
		}
	}
}

func TestSendDingtalkText(t *testing.T) {
	testutil.SetupTestEnv(t)

	var body map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	err := sendDingtalk(map[string]string{
		"webhook":  server.URL,
		"msg_type": "Text",
	}, "标题", "第一行\n第二行")
	if err != nil {
		t.Fatalf("send dingtalk text: %v", err)
	}

	if got := body["msgtype"]; got != "text" {
		t.Fatalf("unexpected msgtype: %#v", got)
	}
	textBody, ok := body["text"].(map[string]interface{})
	if !ok {
		t.Fatalf("unexpected text payload: %#v", body["text"])
	}
	if got := textBody["content"]; got != "标题\n第一行\n第二行" {
		t.Fatalf("unexpected text content: %#v", got)
	}
	if _, exists := body["markdown"]; exists {
		t.Fatalf("text message should not include markdown payload")
	}
}

func TestSendDingtalkMarkdownFallback(t *testing.T) {
	testutil.SetupTestEnv(t)

	cases := []struct {
		name    string
		msgType string
	}{
		{name: "explicit markdown", msgType: "markdown"},
		{name: "empty falls back to markdown", msgType: ""},
		{name: "unknown falls back to markdown", msgType: "html"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var body map[string]interface{}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Fatalf("decode body: %v", err)
				}
				w.WriteHeader(http.StatusOK)
			}))
			defer server.Close()

			cfg := map[string]string{"webhook": server.URL}
			if tc.msgType != "" {
				cfg["msg_type"] = tc.msgType
			}

			if err := sendDingtalk(cfg, "标题", "第一行\n第二行"); err != nil {
				t.Fatalf("send dingtalk markdown: %v", err)
			}

			if got := body["msgtype"]; got != "markdown" {
				t.Fatalf("unexpected msgtype: %#v", got)
			}
			markdown, ok := body["markdown"].(map[string]interface{})
			if !ok {
				t.Fatalf("unexpected markdown payload: %#v", body["markdown"])
			}
			if got := markdown["title"]; got != "标题" {
				t.Fatalf("unexpected markdown title: %#v", got)
			}
			if got := markdown["text"]; got != "### 标题  \n第一行  \n第二行" {
				t.Fatalf("unexpected markdown text: %#v", got)
			}
			if _, exists := body["text"]; exists {
				t.Fatalf("markdown message should not include text payload")
			}
		})
	}
}

func TestSendToChannelPrefixesPanelLabel(t *testing.T) {
	testutil.SetupTestEnv(t)

	if err := model.SetConfig("notify_panel_label", "家里NAS"); err != nil {
		t.Fatalf("set notify_panel_label: %v", err)
	}

	var body map[string]string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	ch := model.NotifyChannel{
		Type:   "webhook",
		Config: fmt.Sprintf(`{"url":%q}`, server.URL),
	}
	if err := sendToChannel(ch, "原始标题", "正文", nil); err != nil {
		t.Fatalf("send to channel: %v", err)
	}

	if got := body["title"]; got != "【家里NAS】原始标题" {
		t.Fatalf("unexpected prefixed title: %q", got)
	}
	if got := body["content"]; got != "正文" {
		t.Fatalf("unexpected content: %q", got)
	}
}

func TestSendToChannelKeepsTitleWhenPanelLabelEmpty(t *testing.T) {
	testutil.SetupTestEnv(t)

	var body map[string]string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	ch := model.NotifyChannel{
		Type:   "webhook",
		Config: fmt.Sprintf(`{"url":%q}`, server.URL),
	}
	if err := sendToChannel(ch, "原始标题", "正文", nil); err != nil {
		t.Fatalf("send to channel: %v", err)
	}

	if got := body["title"]; got != "原始标题" {
		t.Fatalf("expected title unchanged when label empty, got %q", got)
	}
}

func TestSendWecomTextWithMentions(t *testing.T) {
	testutil.SetupTestEnv(t)

	var body map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	err := sendWecom(map[string]string{
		"webhook":               server.URL,
		"msg_type":              "text",
		"content_template":      "{{title}}\n{{content}}",
		"mentioned_list":        "wangqing,@all",
		"mentioned_mobile_list": "13800001111",
	}, "告警标题", "告警内容")
	if err != nil {
		t.Fatalf("send wecom text: %v", err)
	}

	if got := body["msgtype"]; got != "text" {
		t.Fatalf("unexpected msgtype: %#v", got)
	}

	textBody, ok := body["text"].(map[string]interface{})
	if !ok {
		t.Fatalf("unexpected text payload: %#v", body["text"])
	}
	if got := textBody["content"]; got != "告警标题\n告警内容" {
		t.Fatalf("unexpected text content: %#v", got)
	}

	mentionedList, ok := textBody["mentioned_list"].([]interface{})
	if !ok || len(mentionedList) != 2 {
		t.Fatalf("unexpected mentioned_list: %#v", textBody["mentioned_list"])
	}
}

func TestSendWecomTemplateCard(t *testing.T) {
	testutil.SetupTestEnv(t)

	var body map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	err := sendWecom(map[string]string{
		"webhook":  server.URL,
		"msg_type": "template_card",
		"template_card_payload": `{
			"card_type":"text_notice",
			"main_title":{"title":"{{title}}","desc":"{{content}}"}
		}`,
	}, "系统通知", "任务执行完成")
	if err != nil {
		t.Fatalf("send wecom template card: %v", err)
	}

	if got := body["msgtype"]; got != "template_card" {
		t.Fatalf("unexpected msgtype: %#v", got)
	}
	cardBody, ok := body["template_card"].(map[string]interface{})
	if !ok {
		t.Fatalf("unexpected template_card payload: %#v", body["template_card"])
	}
	mainTitle, ok := cardBody["main_title"].(map[string]interface{})
	if !ok {
		t.Fatalf("unexpected main_title: %#v", cardBody["main_title"])
	}
	if got := mainTitle["title"]; got != "系统通知" {
		t.Fatalf("unexpected template title: %#v", got)
	}
	if got := mainTitle["desc"]; got != "任务执行完成" {
		t.Fatalf("unexpected template desc: %#v", got)
	}
}

func TestSendWecomAppMarkdown(t *testing.T) {
	testutil.SetupTestEnv(t)

	var (
		tokenRequested bool
		messageBody    map[string]interface{}
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/cgi-bin/gettoken":
			tokenRequested = true
			if got := r.URL.Query().Get("corpid"); got != "ww-demo" {
				t.Fatalf("unexpected corp id: %s", got)
			}
			if got := r.URL.Query().Get("corpsecret"); got != "secret-demo" {
				t.Fatalf("unexpected corp secret: %s", got)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"errcode":0,"errmsg":"ok","access_token":"token-demo"}`))
		case "/cgi-bin/message/send":
			if got := r.URL.Query().Get("access_token"); got != "token-demo" {
				t.Fatalf("unexpected access_token: %s", got)
			}
			if err := json.NewDecoder(r.Body).Decode(&messageBody); err != nil {
				t.Fatalf("decode message body: %v", err)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"errcode":0,"errmsg":"ok"}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	oldTokenURL := wecomAppTokenURL
	oldSendURL := wecomAppSendURL
	wecomAppTokenURL = server.URL + "/cgi-bin/gettoken"
	wecomAppSendURL = server.URL + "/cgi-bin/message/send"
	defer func() {
		wecomAppTokenURL = oldTokenURL
		wecomAppSendURL = oldSendURL
	}()

	err := sendWecomApp(map[string]string{
		"corp_id":  "ww-demo",
		"secret":   "secret-demo",
		"agent_id": "1000001",
		"to_user":  "@all",
		"msg_type": "markdown",
	}, "标题", "正文")
	if err != nil {
		t.Fatalf("send wecom app: %v", err)
	}
	if !tokenRequested {
		t.Fatal("expected token endpoint to be requested")
	}
	if got := messageBody["msgtype"]; got != "markdown" {
		t.Fatalf("unexpected msgtype: %#v", got)
	}
	if got := messageBody["touser"]; got != "@all" {
		t.Fatalf("unexpected touser: %#v", got)
	}

	markdown, ok := messageBody["markdown"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected markdown payload, got %#v", messageBody["markdown"])
	}
	if got := markdown["content"]; got != "**标题**\n正文" {
		t.Fatalf("unexpected markdown content: %#v", got)
	}
}

func TestSendWecomAppTextWithAdvancedOptions(t *testing.T) {
	testutil.SetupTestEnv(t)

	var (
		tokenRequested bool
		messageBody    map[string]interface{}
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/cgi-bin/gettoken":
			tokenRequested = true
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"errcode":0,"errmsg":"ok","access_token":"token-demo"}`))
		case "/cgi-bin/message/send":
			if err := json.NewDecoder(r.Body).Decode(&messageBody); err != nil {
				t.Fatalf("decode message body: %v", err)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"errcode":0,"errmsg":"ok"}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	oldTokenURL := wecomAppTokenURL
	oldSendURL := wecomAppSendURL
	wecomAppTokenURL = server.URL + "/cgi-bin/gettoken"
	wecomAppSendURL = server.URL + "/cgi-bin/message/send"
	defer func() {
		wecomAppTokenURL = oldTokenURL
		wecomAppSendURL = oldSendURL
	}()

	err := sendWecomApp(map[string]string{
		"corp_id":                  "ww-demo",
		"secret":                   "secret-demo",
		"agent_id":                 "1000001",
		"to_user":                  "zhangsan|lisi",
		"msg_type":                 "text",
		"content_template":         "{{title}}\n{{content}}",
		"safe":                     "1",
		"enable_id_trans":          "1",
		"enable_duplicate_check":   "1",
		"duplicate_check_interval": "7200",
	}, "标题", "正文")
	if err != nil {
		t.Fatalf("send wecom app text: %v", err)
	}

	if !tokenRequested {
		t.Fatal("expected token endpoint to be requested")
	}
	if got := messageBody["msgtype"]; got != "text" {
		t.Fatalf("unexpected msgtype: %#v", got)
	}
	if got := messageBody["touser"]; got != "zhangsan|lisi" {
		t.Fatalf("unexpected touser: %#v", got)
	}
	if got := messageBody["safe"]; got != float64(1) {
		t.Fatalf("unexpected safe: %#v", got)
	}
	if got := messageBody["enable_id_trans"]; got != float64(1) {
		t.Fatalf("unexpected enable_id_trans: %#v", got)
	}
	if got := messageBody["enable_duplicate_check"]; got != float64(1) {
		t.Fatalf("unexpected enable_duplicate_check: %#v", got)
	}
	if got := messageBody["duplicate_check_interval"]; got != float64(7200) {
		t.Fatalf("unexpected duplicate_check_interval: %#v", got)
	}

	textBody, ok := messageBody["text"].(map[string]interface{})
	if !ok {
		t.Fatalf("unexpected text payload: %#v", messageBody["text"])
	}
	if got := textBody["content"]; got != "标题\n正文" {
		t.Fatalf("unexpected text content: %#v", got)
	}
}

func TestSendWecomAppTemplateCard(t *testing.T) {
	testutil.SetupTestEnv(t)

	var messageBody map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/cgi-bin/gettoken":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"errcode":0,"errmsg":"ok","access_token":"token-demo"}`))
		case "/cgi-bin/message/send":
			if err := json.NewDecoder(r.Body).Decode(&messageBody); err != nil {
				t.Fatalf("decode message body: %v", err)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"errcode":0,"errmsg":"ok"}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	oldTokenURL := wecomAppTokenURL
	oldSendURL := wecomAppSendURL
	wecomAppTokenURL = server.URL + "/cgi-bin/gettoken"
	wecomAppSendURL = server.URL + "/cgi-bin/message/send"
	defer func() {
		wecomAppTokenURL = oldTokenURL
		wecomAppSendURL = oldSendURL
	}()

	err := sendWecomApp(map[string]string{
		"corp_id":  "ww-demo",
		"secret":   "secret-demo",
		"agent_id": "1000001",
		"to_user":  "@all",
		"msg_type": "template_card",
		"template_card_payload": `{
			"card_type":"text_notice",
			"main_title":{"title":"{{title}}","desc":"{{content}}"}
		}`,
	}, "系统通知", "任务执行完成")
	if err != nil {
		t.Fatalf("send wecom app template card: %v", err)
	}

	if got := messageBody["msgtype"]; got != "template_card" {
		t.Fatalf("unexpected msgtype: %#v", got)
	}
	cardBody, ok := messageBody["template_card"].(map[string]interface{})
	if !ok {
		t.Fatalf("unexpected template_card payload: %#v", messageBody["template_card"])
	}
	mainTitle, ok := cardBody["main_title"].(map[string]interface{})
	if !ok {
		t.Fatalf("unexpected main_title: %#v", cardBody["main_title"])
	}
	if got := mainTitle["title"]; got != "系统通知" {
		t.Fatalf("unexpected template title: %#v", got)
	}
	if got := mainTitle["desc"]; got != "任务执行完成" {
		t.Fatalf("unexpected template desc: %#v", got)
	}
}

func TestSendWecomAppMpnews(t *testing.T) {
	testutil.SetupTestEnv(t)

	var messageBody map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/cgi-bin/gettoken":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"errcode":0,"errmsg":"ok","access_token":"token-demo"}`))
		case "/cgi-bin/message/send":
			if err := json.NewDecoder(r.Body).Decode(&messageBody); err != nil {
				t.Fatalf("decode message body: %v", err)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"errcode":0,"errmsg":"ok"}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	oldTokenURL := wecomAppTokenURL
	oldSendURL := wecomAppSendURL
	wecomAppTokenURL = server.URL + "/cgi-bin/gettoken"
	wecomAppSendURL = server.URL + "/cgi-bin/message/send"
	defer func() {
		wecomAppTokenURL = oldTokenURL
		wecomAppSendURL = oldSendURL
	}()

	content := "任务执行完成\n第二行输出"

	err := sendWecomApp(map[string]string{
		"corp_id":         "ww-demo",
		"secret":          "secret-demo",
		"agent_id":        "1000001",
		"to_user":         "@all",
		"msg_type":        "mpnews",
		"safe":            "2",
		"enable_id_trans": "1",
		"mpnews_articles": `[
			{
				"title":"{{title}}",
				"thumb_media_id":"MEDIA_ID",
				"author":"Author",
				"content_source_url":"https://example.com/article",
				"content":"<p>{{content}}</p>",
				"digest":"{{content}}"
			}
		]`,
	}, "系统通知", content)
	if err != nil {
		t.Fatalf("send wecom app mpnews: %v", err)
	}

	if got := messageBody["msgtype"]; got != "mpnews" {
		t.Fatalf("unexpected msgtype: %#v", got)
	}
	if got := messageBody["safe"]; got != float64(2) {
		t.Fatalf("unexpected safe: %#v", got)
	}
	if got := messageBody["enable_id_trans"]; got != float64(1) {
		t.Fatalf("unexpected enable_id_trans: %#v", got)
	}
	mpnews, ok := messageBody["mpnews"].(map[string]interface{})
	if !ok {
		t.Fatalf("unexpected mpnews payload: %#v", messageBody["mpnews"])
	}
	articles, ok := mpnews["articles"].([]interface{})
	if !ok || len(articles) != 1 {
		t.Fatalf("unexpected mpnews articles: %#v", mpnews["articles"])
	}
	article, ok := articles[0].(map[string]interface{})
	if !ok {
		t.Fatalf("unexpected article payload: %#v", articles[0])
	}
	if got := article["title"]; got != "系统通知" {
		t.Fatalf("unexpected article title: %#v", got)
	}
	if got := article["thumb_media_id"]; got != "MEDIA_ID" {
		t.Fatalf("unexpected thumb_media_id: %#v", got)
	}
	// mpnews content 走 HTML 渲染，换行需转成 <br> 才能生效。
	if got := article["content"]; got != "<p>任务执行完成<br>第二行输出</p>" {
		t.Fatalf("unexpected article content: %#v", got)
	}
	// digest 是纯文本路径，同样的 {{content}} 渲染结果不应被换行转换影响，保持原始 \n。
	if got := article["digest"]; got != content {
		t.Fatalf("unexpected article digest: %#v", got)
	}
}

func TestSendWecomAppUsesReverseProxyBaseURL(t *testing.T) {
	testutil.SetupTestEnv(t)

	var (
		tokenRequested bool
		sendRequested  bool
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/proxy-qyapi/cgi-bin/gettoken":
			tokenRequested = true
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"errcode":0,"errmsg":"ok","access_token":"token-demo"}`))
		case "/proxy-qyapi/cgi-bin/message/send":
			sendRequested = true
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"errcode":0,"errmsg":"ok"}`))
		default:
			t.Fatalf("unexpected reverse proxy path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	err := sendWecomApp(map[string]string{
		"corp_id":  "ww-demo",
		"secret":   "secret-demo",
		"agent_id": "1000001",
		"to_user":  "@all",
		"msg_type": "text",
		"base_url": server.URL + "/proxy-qyapi",
	}, "标题", "正文")
	if err != nil {
		t.Fatalf("send wecom app via reverse proxy: %v", err)
	}
	if !tokenRequested || !sendRequested {
		t.Fatalf("expected both reverse proxy endpoints to be used, token=%v send=%v", tokenRequested, sendRequested)
	}
}

func TestSendWxPusherIncludesOptionalFields(t *testing.T) {
	testutil.SetupTestEnv(t)

	var payload map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode payload: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":1000,"msg":"处理成功","success":true}`))
	}))
	defer server.Close()

	err := sendWxPusher(map[string]string{
		"app_token":       "AT_demo",
		"uids":            "UID_demo",
		"content_type":    "3",
		"url":             "https://example.com/detail",
		"verify_pay_type": "2",
		"server":          server.URL,
	}, "标题", "正文")
	if err != nil {
		t.Fatalf("send wxpusher: %v", err)
	}

	if got := payload["url"]; got != "https://example.com/detail" {
		t.Fatalf("unexpected wxpusher url: %#v", got)
	}
	if got := payload["verifyPayType"]; got != float64(2) {
		t.Fatalf("unexpected verifyPayType: %#v", got)
	}
	if got := payload["contentType"]; got != float64(3) {
		t.Fatalf("unexpected contentType: %#v", got)
	}
}

func TestSendWecomAppReturnsEnterpriseError(t *testing.T) {
	testutil.SetupTestEnv(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/cgi-bin/gettoken":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"errcode":0,"errmsg":"ok","access_token":"token-demo"}`))
		case "/cgi-bin/message/send":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"errcode":40003,"errmsg":"invalid user","invaliduser":"zhangsan|lisi"}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	oldTokenURL := wecomAppTokenURL
	oldSendURL := wecomAppSendURL
	wecomAppTokenURL = server.URL + "/cgi-bin/gettoken"
	wecomAppSendURL = server.URL + "/cgi-bin/message/send"
	defer func() {
		wecomAppTokenURL = oldTokenURL
		wecomAppSendURL = oldSendURL
	}()

	err := sendWecomApp(map[string]string{
		"corp_id":  "ww-demo",
		"secret":   "secret-demo",
		"agent_id": "1000001",
		"to_user":  "@all",
		"msg_type": "text",
	}, "标题", "正文")
	if err == nil {
		t.Fatal("expected enterprise error")
	}
	if got := err.Error(); got != "发送企业微信应用消息失败: invalid user (invaliduser=zhangsan|lisi)" {
		t.Fatalf("unexpected error: %s", got)
	}
}

// ---- #123 企业微信应用正向代理 ----

// wecomAppRecorder 是 #123 代理用例的公共设施：一个会记录请求的 httptest 服务，
// 既可以当企业微信「源站」（base_url 指向它），也可以当 HTTP 正向代理（proxy 指向它）。
//
// 当代理用时，Go 的 Transport 发来的是 absolute-form 请求行（GET http://<源站>/cgi-bin/gettoken?... HTTP/1.1）：
// r.RequestURI 是那条完整 URL，r.URL.Path 仍是 /cgi-bin/...。所以它按路径后缀直接应答、不真的转发，
// 用例要证明的只是「请求进了哪台服务」。
//
// 源站与代理都用 loopback：http.ProxyURL 不对 loopback 做豁免（豁免只存在于 ProxyFromEnvironment），
// 所以两边的命中计数都是确定的。不要改用 .invalid 之类的域名：本机 fake-ip 会把它应答成 HTTP 503，
// 用例红了也看不出到底是不是直连。
type wecomAppRecorder struct {
	*httptest.Server

	mu   sync.Mutex
	uris []string
}

type wecomAppRecorderOptions struct {
	tokenBody string // 为空时返回成功的 access_token 响应
	sendBody  string // 为空时返回 errcode=0
	dropSend  bool   // message/send 不应答、直接断开连接，让客户端拿到一个带完整请求 URL 的 *url.Error
	// echoURIOn 是路径后缀：命中时按 writeProxyErrorPageEchoingURI 回 503。当代理用时，回显的是 absolute-form
	// 的完整 URL，query 里带着 corpsecret / access_token —— 这是 HTTP 状态错误，stripRequestURL 管不到。
	echoURIOn string
}

// writeProxyErrorPageEchoingURI 模拟正向代理连不上目标时的错误页：503，正文回显请求行 URI（Squid 默认模板的 %U）。
// 故意不做 HTML 转义，取最坏情况：密钥在请求行里是什么形态，在正文里就是什么形态。
func writeProxyErrorPageEchoingURI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusServiceUnavailable)
	_, _ = fmt.Fprintf(w, "<html><body><h1>ERROR</h1><p>The requested URL could not be retrieved: %s</p></body></html>", r.RequestURI)
}

func newWecomAppRecorder(t *testing.T, opts wecomAppRecorderOptions) *wecomAppRecorder {
	t.Helper()

	tokenBody := opts.tokenBody
	if tokenBody == "" {
		tokenBody = `{"errcode":0,"errmsg":"ok","access_token":"token-demo"}`
	}
	sendBody := opts.sendBody
	if sendBody == "" {
		sendBody = `{"errcode":0,"errmsg":"ok"}`
	}

	rec := &wecomAppRecorder{}
	rec.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.mu.Lock()
		rec.uris = append(rec.uris, r.RequestURI)
		rec.mu.Unlock()

		if opts.echoURIOn != "" && strings.HasSuffix(r.URL.Path, opts.echoURIOn) {
			writeProxyErrorPageEchoingURI(w, r)
			return
		}

		switch {
		case strings.HasSuffix(r.URL.Path, "/cgi-bin/gettoken"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(tokenBody))
		case strings.HasSuffix(r.URL.Path, "/cgi-bin/message/send"):
			if opts.dropSend {
				if hijacker, ok := w.(http.Hijacker); ok {
					if conn, _, err := hijacker.Hijack(); err == nil {
						_ = conn.Close()
					}
				}
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(sendBody))
		default:
			// 不在 handler 协程里调 t.Fatalf（FailNow 只能在测试协程里调）；404 会让发送方返回错误，用例照样红。
			http.Error(w, "unexpected path "+r.URL.Path, http.StatusNotFound)
		}
	}))
	t.Cleanup(rec.Server.Close)
	return rec
}

// requestURIs 返回收到的请求行 URI 的副本。
func (r *wecomAppRecorder) requestURIs() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.uris...)
}

// hostPort 是 127.0.0.1:<端口>，拼 absolute-form URI 断言和代理地址都用它。
func (r *wecomAppRecorder) hostPort() string {
	return r.Listener.Addr().String()
}

// wecomAppProxyTestConfig 是最小可发送配置；baseURL 指向 loopback 源站时，经 HTTP 代理也不会走 CONNECT。
func wecomAppProxyTestConfig(baseURL, proxy string) map[string]string {
	return map[string]string{
		"corp_id":  "ww-demo",
		"secret":   "secret-demo",
		"agent_id": "1000001",
		"msg_type": "text",
		"base_url": baseURL,
		"proxy":    proxy,
	}
}

// assertWecomAppRequestsWentThrough 断言 proxy 恰好收到 gettoken 与 message/send 两条 absolute-form 请求，
// 且目标都是 origin —— 也就是「两次请求确实都是经它发出去的」。
func assertWecomAppRequestsWentThrough(t *testing.T, name string, proxy, origin *wecomAppRecorder) {
	t.Helper()

	uris := proxy.requestURIs()
	if len(uris) != 2 {
		t.Fatalf("%s 应当恰好收到 2 条请求（取 token + 发消息），实际 %d 条: %v", name, len(uris), uris)
	}
	prefix := "http://" + origin.hostPort()
	if !strings.HasPrefix(uris[0], prefix+"/cgi-bin/gettoken") {
		t.Errorf("%s 收到的第 1 条应当是发往源站的 gettoken（absolute-form），实际 %s", name, uris[0])
	}
	if !strings.HasPrefix(uris[1], prefix+"/cgi-bin/message/send") {
		t.Errorf("%s 收到的第 2 条应当是发往源站的 message/send（absolute-form），实际 %s", name, uris[1])
	}
}

func assertWecomAppRecorderUnused(t *testing.T, name string, rec *wecomAppRecorder) {
	t.Helper()

	if uris := rec.requestURIs(); len(uris) != 0 {
		t.Errorf("%s 不应收到任何请求，实际 %d 条: %v", name, len(uris), uris)
	}
}

// TestSendWecomAppUsesChannelProxy：渠道填了 proxy，取 token 与发消息两次请求都经它发出，源站 0 次直连。
// 修复前 wecom_app 完全不读 cfg["proxy"]，实测是源站 2 次、代理 0 次，确定性红。
func TestSendWecomAppUsesChannelProxy(t *testing.T) {
	testutil.SetupTestEnv(t)

	origin := newWecomAppRecorder(t, wecomAppRecorderOptions{})
	proxy := newWecomAppRecorder(t, wecomAppRecorderOptions{})

	if err := sendWecomApp(wecomAppProxyTestConfig(origin.URL, proxy.URL), "标题", "正文"); err != nil {
		t.Fatalf("经渠道代理发送企业微信应用消息失败: %v", err)
	}
	assertWecomAppRequestsWentThrough(t, "渠道代理", proxy, origin)
	assertWecomAppRecorderUnused(t, "源站（说明发生了直连）", origin)
}

// TestSendWecomAppChannelProxyOverridesGlobal：渠道代理与系统代理同时存在时用渠道代理（与 telegram 一致）。
func TestSendWecomAppChannelProxyOverridesGlobal(t *testing.T) {
	testutil.SetupTestEnv(t)

	origin := newWecomAppRecorder(t, wecomAppRecorderOptions{})
	channelProxy := newWecomAppRecorder(t, wecomAppRecorderOptions{})
	globalProxy := newWecomAppRecorder(t, wecomAppRecorderOptions{})
	if err := model.SetConfig("proxy_url", globalProxy.URL); err != nil {
		t.Fatalf("set proxy_url: %v", err)
	}

	if err := sendWecomApp(wecomAppProxyTestConfig(origin.URL, channelProxy.URL), "标题", "正文"); err != nil {
		t.Fatalf("发送企业微信应用消息失败: %v", err)
	}
	assertWecomAppRequestsWentThrough(t, "渠道代理", channelProxy, origin)
	assertWecomAppRecorderUnused(t, "系统代理", globalProxy)
	assertWecomAppRecorderUnused(t, "源站", origin)
}

// TestSendWecomAppFallsBackToGlobalProxy：渠道不填 proxy 时回落系统设置 proxy_url。
//
// 这是向后兼容的回归锁，修复前就是绿的（wecom_app 原本用 NewHTTPClient，本来就认系统代理）。
// 它证明不了渠道代理生效，突变验证也不能靠它 —— 那是 TestSendWecomAppUsesChannelProxy 的事。
func TestSendWecomAppFallsBackToGlobalProxy(t *testing.T) {
	testutil.SetupTestEnv(t)

	origin := newWecomAppRecorder(t, wecomAppRecorderOptions{})
	globalProxy := newWecomAppRecorder(t, wecomAppRecorderOptions{})
	if err := model.SetConfig("proxy_url", globalProxy.URL); err != nil {
		t.Fatalf("set proxy_url: %v", err)
	}

	if err := sendWecomApp(wecomAppProxyTestConfig(origin.URL, ""), "标题", "正文"); err != nil {
		t.Fatalf("发送企业微信应用消息失败: %v", err)
	}
	assertWecomAppRequestsWentThrough(t, "系统代理", globalProxy, origin)
	assertWecomAppRecorderUnused(t, "源站", origin)
}

// TestSendWecomAppRejectsMalformedProxy：渠道代理地址非法时发送期显式报错，且源站与代理都 0 命中。
//
// 修复前（或删掉发送期校验后）这些值都不会报「代理地址」：漏写 scheme 与非法转义让 url.Parse 失败，
// NewHTTPClientWithProxy 静默直连源站；localhost:7890 报 dial tcp :0；ftp:// 被当成 HTTP 代理用、
// 请求照样发进代理。可信 IP 场景下前两种就是「填了代理却 60020、没有任何提示」。
func TestSendWecomAppRejectsMalformedProxy(t *testing.T) {
	testutil.SetupTestEnv(t)

	origin := newWecomAppRecorder(t, wecomAppRecorderOptions{})
	proxy := newWecomAppRecorder(t, wecomAppRecorderOptions{})

	cases := []struct {
		name  string
		value string
	}{
		{"漏写 scheme", "127.0.0.1:7890"},
		{"非法转义", "%zz"},
		{"localhost:端口", "localhost:7890"},
		// 指向一台真的能应答的记录代理：不做校验时请求会被当成 HTTP 代理发进去，代理计数就不再是 0。
		{"不支持的 scheme", "ftp://" + proxy.hostPort()},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := sendWecomApp(wecomAppProxyTestConfig(origin.URL, tc.value), "标题", "正文")
			if err == nil {
				t.Fatalf("非法代理地址 %q 应当显式报错，实际发送成功", tc.value)
			}
			if !strings.Contains(err.Error(), "代理地址") {
				t.Errorf("错误信息应当指出是代理地址的问题，实际: %v", err)
			}
		})
	}
	assertWecomAppRecorderUnused(t, "源站（说明非法代理被静默忽略、直连了）", origin)
	assertWecomAppRecorderUnused(t, "记录代理（说明 ftp:// 被当成 HTTP 代理用了）", proxy)
}

// TestSendWecomAppNetworkErrorsDoNotLeakSecrets：网络错误不得回显请求 URL。
//
// gettoken 的 query 带 corpsecret、message/send 带 access_token；*url.Error 原样拼进错误时会整条带出来，
// 流到测试按钮、/notifications/send 响应（operator 与 Open API 可见）、脚本日志与面板日志。
// 代理地址里的账号密码同样不能出现。
func TestSendWecomAppNetworkErrorsDoNotLeakSecrets(t *testing.T) {
	testutil.SetupTestEnv(t)

	t.Run("取 token 时代理连不上", func(t *testing.T) {
		// 127.0.0.1:1 上没有服务：proxyconnect 失败正是 #123 之后最常见的失败形态。
		cfg := wecomAppProxyTestConfig("http://127.0.0.1:1", "http://u:proxy-pass-xyz@127.0.0.1:1")
		cfg["secret"] = "corp-secret-xyz"

		err := sendWecomApp(cfg, "标题", "正文")
		if err == nil {
			t.Fatal("代理连不上时应当报错")
		}
		msg := err.Error()
		if !strings.Contains(msg, "获取企业微信应用 access_token 失败") {
			t.Fatalf("应当在取 token 这一步失败，实际: %s", msg)
		}
		for _, secret := range []string{"corp-secret-xyz", "proxy-pass-xyz", "corpsecret="} {
			if strings.Contains(msg, secret) {
				t.Errorf("错误信息泄露了 %q: %s", secret, msg)
			}
		}
	})

	t.Run("发消息时连接被断开", func(t *testing.T) {
		origin := newWecomAppRecorder(t, wecomAppRecorderOptions{
			tokenBody: `{"errcode":0,"errmsg":"ok","access_token":"access-token-xyz"}`,
			dropSend:  true,
		})

		err := sendWecomApp(wecomAppProxyTestConfig(origin.URL, ""), "标题", "正文")
		if err == nil {
			t.Fatal("连接被断开时应当报错")
		}
		msg := err.Error()
		if !strings.Contains(msg, "发送企业微信应用消息失败") {
			t.Fatalf("应当在发消息这一步失败，实际: %s", msg)
		}
		for _, secret := range []string{"access-token-xyz", "access_token="} {
			if strings.Contains(msg, secret) {
				t.Errorf("错误信息泄露了 %q: %s", secret, msg)
			}
		}
	})

	// 以下两例是 HTTP≥400 分支：正向代理连不上目标时回 503，错误页回显完整请求 URL（Squid 默认模板带 %U）。
	// base_url 是 http:// 时经 HTTP 代理发的是 absolute-form 请求，URL 会原样进代理的错误页；https 目标走 CONNECT 不受影响。
	// 密钥里特意带 / + =：URL 里出现的是 QueryEscape 之后的形态，只替换原文兜不住。
	t.Run("取 token 时代理回 503、错误页回显请求 URL", func(t *testing.T) {
		const secret = "corp-secret-xyz/+="
		origin := newWecomAppRecorder(t, wecomAppRecorderOptions{})
		proxy := newWecomAppRecorder(t, wecomAppRecorderOptions{echoURIOn: "/cgi-bin/gettoken"})
		cfg := wecomAppProxyTestConfig(origin.URL, proxy.URL)
		cfg["secret"] = secret

		err := sendWecomApp(cfg, "标题", "正文")
		if err == nil {
			t.Fatal("代理回 503 时应当报错")
		}
		// 先证明用例没有空转：代理收到的请求行里确实带着转义后的 secret，错误页会把它原样回显。
		if uris := proxy.requestURIs(); len(uris) != 1 || !strings.Contains(uris[0], "corpsecret="+url.QueryEscape(secret)) {
			t.Fatalf("代理应当恰好收到 1 条带 corpsecret 的 gettoken 请求，实际: %v", uris)
		}
		msg := err.Error()
		for _, want := range []string{"获取企业微信应用 access_token 失败", "HTTP 503", "corpsecret=***"} {
			if !strings.Contains(msg, want) {
				t.Errorf("错误信息应当包含 %q，实际: %s", want, msg)
			}
		}
		// corp-secret-xyz 是原文与转义形态的公共前缀，哪种漏出来都会命中。
		if strings.Contains(msg, "corp-secret-xyz") {
			t.Errorf("错误信息泄露了 corpsecret: %s", msg)
		}
	})

	t.Run("发消息时代理回 503、错误页回显请求 URL", func(t *testing.T) {
		const accessToken = "access-token-xyz/+="
		origin := newWecomAppRecorder(t, wecomAppRecorderOptions{})
		proxy := newWecomAppRecorder(t, wecomAppRecorderOptions{
			tokenBody: `{"errcode":0,"errmsg":"ok","access_token":"` + accessToken + `"}`,
			echoURIOn: "/cgi-bin/message/send",
		})

		err := sendWecomApp(wecomAppProxyTestConfig(origin.URL, proxy.URL), "标题", "正文")
		if err == nil {
			t.Fatal("代理回 503 时应当报错")
		}
		if uris := proxy.requestURIs(); len(uris) != 2 || !strings.Contains(uris[1], "access_token="+url.QueryEscape(accessToken)) {
			t.Fatalf("代理应当收到 gettoken 与带 access_token 的 message/send 两条请求，实际: %v", uris)
		}
		msg := err.Error()
		for _, want := range []string{"发送企业微信应用消息失败", "HTTP 503", "access_token=***"} {
			if !strings.Contains(msg, want) {
				t.Errorf("错误信息应当包含 %q，实际: %s", want, msg)
			}
		}
		if strings.Contains(msg, "access-token-xyz") {
			t.Errorf("错误信息泄露了 access_token: %s", msg)
		}
	})

	// errcode 分支：HTTP 200，errmsg 原样拼进错误。企业微信官方的 errmsg 不回显请求参数，但 base_url 指向的
	// 自建网关或反代可能把请求行写进 errmsg。wecomAppRecorder 的应答是固定串、没法回显 RequestURI，所以单独起一个网关。
	t.Run("网关返回 errcode、errmsg 回显请求 URL", func(t *testing.T) {
		const secret = "corp-secret-xyz/+="
		const accessToken = "access-token-xyz/+="
		cases := []struct {
			name        string
			failOn      string // 命中这个路径后缀时回 errcode 40001，errmsg 回显请求行 URI
			invalidUser string // 非空时应答带 invaliduser，走发消息「带明细」的那个分支
			echoed      string // 网关收到的请求行里会带着的密钥片段（证明用例没有空转）
			wants       []string
		}{
			{"取 token", "/cgi-bin/gettoken", "", "corpsecret=" + url.QueryEscape(secret),
				[]string{"获取企业微信应用 access_token 失败: gateway rejected ", "corpsecret=***"}},
			{"发消息", "/cgi-bin/message/send", "", "access_token=" + url.QueryEscape(accessToken),
				[]string{"发送企业微信应用消息失败: gateway rejected ", "access_token=***"}},
			{"发消息且带 invaliduser", "/cgi-bin/message/send", "zhangsan", "access_token=" + url.QueryEscape(accessToken),
				[]string{"发送企业微信应用消息失败: gateway rejected ", "access_token=***", "(invaliduser=zhangsan)"}},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				var mu sync.Mutex
				var uris []string
				gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					mu.Lock()
					uris = append(uris, r.RequestURI)
					mu.Unlock()

					payload := map[string]interface{}{"errcode": 0, "errmsg": "ok", "access_token": accessToken}
					if strings.HasSuffix(r.URL.Path, tc.failOn) {
						payload = map[string]interface{}{"errcode": 40001, "errmsg": "gateway rejected " + r.RequestURI}
						if tc.invalidUser != "" {
							payload["invaliduser"] = tc.invalidUser
						}
					}
					w.Header().Set("Content-Type", "application/json")
					_ = json.NewEncoder(w).Encode(payload)
				}))
				defer gateway.Close()

				cfg := wecomAppProxyTestConfig(gateway.URL, "")
				cfg["secret"] = secret
				err := sendWecomApp(cfg, "标题", "正文")
				if err == nil {
					t.Fatal("网关返回 errcode 时应当报错")
				}
				mu.Lock()
				got := append([]string(nil), uris...)
				mu.Unlock()
				if len(got) == 0 || !strings.Contains(got[len(got)-1], tc.echoed) {
					t.Fatalf("网关收到的最后一条请求应当带着 %q（errmsg 会把它回显出来），实际: %v", tc.echoed, got)
				}
				msg := err.Error()
				for _, want := range tc.wants {
					if !strings.Contains(msg, want) {
						t.Errorf("错误信息应当包含 %q，实际: %s", want, msg)
					}
				}
				// 两个串分别是 corpsecret、access_token 原文与转义形态的公共前缀，哪种漏出来都会命中。
				for _, notWant := range []string{"corp-secret-xyz", "access-token-xyz"} {
					if strings.Contains(msg, notWant) {
						t.Errorf("错误信息泄露了 %q: %s", notWant, msg)
					}
				}
			})
		}
	})
}

// TestSendTelegramNetworkErrorDoesNotLeakBotToken：telegram 的 bot token 在 URL 路径里（/bot<token>/sendMessage），
// 任何失败形态的错误信息都不得带出它。只改错误文案、不改变成败（telegram 发送期对代理地址的宽松行为保持原样）。
//
// 网络错误（*url.Error）剥掉 URL 后补「请求 Telegram API（scheme://host）失败」前缀，否则用户看不出是哪个请求、
// 该改哪个输入框；HTTP 状态错误与业务错误不加前缀，但正文按 token 脱敏。
func TestSendTelegramNetworkErrorDoesNotLeakBotToken(t *testing.T) {
	testutil.SetupTestEnv(t)

	const token = "123456:telegram-bot-token-xyz"
	send := func(apiHost, proxy string) error {
		return sendTelegram(map[string]string{
			"token":    token,
			"chat_id":  "10001",
			"api_host": apiHost,
			"proxy":    proxy,
		}, "标题", "正文")
	}
	assertMessage := func(t *testing.T, err error, wants, notWants []string) {
		t.Helper()
		if err == nil {
			t.Fatal("应当报错")
		}
		msg := err.Error()
		for _, want := range wants {
			if !strings.Contains(msg, want) {
				t.Errorf("错误信息应当包含 %q，实际: %s", want, msg)
			}
		}
		for _, notWant := range append([]string{"telegram-bot-token-xyz"}, notWants...) {
			if strings.Contains(msg, notWant) {
				t.Errorf("错误信息不应包含 %q，实际: %s", notWant, msg)
			}
		}
	}
	// recordingServer 起一个 httptest 服务：先记下请求行（r.RequestURI）再交给 respond 应答。
	// 回显类子例都先用它证明请求行里确实带着 token，再断言错误信息里没有，避免用例空转。
	recordingServer := func(t *testing.T, respond http.HandlerFunc) (serverURL string, received func() []string) {
		t.Helper()
		var mu sync.Mutex
		var uris []string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			uris = append(uris, r.RequestURI)
			mu.Unlock()
			respond(w, r)
		}))
		t.Cleanup(server.Close)
		return server.URL, func() []string {
			mu.Lock()
			defer mu.Unlock()
			return append([]string(nil), uris...)
		}
	}

	t.Run("连接被断开", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if hijacker, ok := w.(http.Hijacker); ok {
				if conn, _, err := hijacker.Hijack(); err == nil {
					_ = conn.Close()
				}
			}
		}))
		defer server.Close()

		assertMessage(t, send(server.URL, ""), []string{"请求 Telegram API（" + server.URL + "）失败: "}, nil)
	})

	t.Run("api_host 漏写 scheme", func(t *testing.T) {
		// 以前整条 URL 至少能指明是 api_host 的问题（代价是带出 token）；剥掉 URL 后靠前缀点名 api_host。
		assertMessage(t, send("api.telegram.org", ""),
			[]string{"请求 Telegram API（api_host 地址无法解析）失败: ", "unsupported protocol scheme"},
			[]string{"（（"})
	})

	t.Run("api_host 带账号密码与鉴权路径", func(t *testing.T) {
		// 前缀只留 scheme://host。Go 会把 URL 里的 userinfo 转成 Basic 认证头，带账号密码的 api_host 是能用的配置，
		// 反代地址的路径里也可能藏着鉴权片段。上面「连接被断开」的 api_host 是 httptest 地址，脱敏前后字面相同，
		// 兜不住「前缀直接用 api_host 原文」这种改法。127.0.0.1:1 上没有服务，直接 connection refused。
		assertMessage(t, send("http://u:pw-xyz@127.0.0.1:1/secret-path-xyz", ""),
			[]string{"请求 Telegram API（http://127.0.0.1:1）失败: "},
			[]string{"pw-xyz", "secret-path-xyz"})
	})

	t.Run("代理回 503、错误页回显请求 URL", func(t *testing.T) {
		// api_host 是 http:// 时经 HTTP 代理发的是 absolute-form 请求，代理错误页会回显带 token 的整条 URL。
		// 目标用 loopback 的空端口：代理只应答不转发，而代理若没生效，请求会直连它、拿到 connection refused，用例照样红。
		proxyURL, received := recordingServer(t, writeProxyErrorPageEchoingURI)
		err := send("http://127.0.0.1:1", proxyURL)
		// 先证明用例没有空转：代理收到的请求行里确实带着 token。
		if got := received(); len(got) != 1 || !strings.Contains(got[0], "/bot"+token+"/sendMessage") {
			t.Fatalf("代理应当恰好收到 1 条带 bot token 的请求，实际: %v", got)
		}
		// HTTP 状态错误不是 *url.Error：不加网络错误的前缀，只脱敏。
		assertMessage(t, err, []string{"HTTP 503", "/bot***/sendMessage"}, []string{"请求 Telegram API"})
	})

	t.Run("token 开头带空白、代理错误页回显请求 URL", func(t *testing.T) {
		// 复制粘贴常把空白带进 token，sendTelegram 不去空白、保存配置也原样写库，apiURL 的路径是 /bot<空格><核心>，
		// 请求行里写成 /bot%20<核心>。脱敏必须按 token 原文求形态：调用点若改传 strings.TrimSpace(token)，
		// 「/bot」后面紧跟的是 %20，按核心锚不住，整段原样漏出去。helper 层的用例直接传原文，拦不住调用点这种改法。
		proxyURL, received := recordingServer(t, writeProxyErrorPageEchoingURI)
		err := sendTelegram(map[string]string{
			"token":    " " + token,
			"chat_id":  "10001",
			"api_host": "http://127.0.0.1:1",
			"proxy":    proxyURL,
		}, "标题", "正文")
		// 先证明用例没有空转：开头的空白确实以 %20 进了请求行。
		want := "/bot%20" + token + "/sendMessage"
		if got := received(); len(got) != 1 || !strings.Contains(got[0], want) {
			t.Fatalf("代理应当恰好收到 1 条请求行带 %q 的请求，实际: %v", want, got)
		}
		assertMessage(t, err, []string{"HTTP 503", "/bot***/sendMessage"}, []string{"请求 Telegram API", "%20123456"})
	})

	t.Run("业务错误回显请求行", func(t *testing.T) {
		// 自建 Bot API 网关或反代可能在 HTTP 200 的业务错误 description 里回显请求路径，走不到 HTTP≥400 那条。
		// 锁住「业务错误同样脱敏」：只对 HTTP 状态错误脱敏、或让业务错误提前原样返回的改法，这里会把 token 带出去。
		gatewayURL, received := recordingServer(t, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"ok": false, "description": "gateway rejected " + r.RequestURI})
		})
		err := send(gatewayURL, "")
		// 先证明用例没有空转：网关收到的请求行里确实带着 token（description 会把它回显出来）。
		if got := received(); len(got) != 1 || !strings.Contains(got[0], "/bot"+token+"/sendMessage") {
			t.Fatalf("网关应当恰好收到 1 条带 bot token 的请求，实际: %v", got)
		}
		// 业务错误不是 *url.Error：不加网络错误的前缀，只脱敏。
		assertMessage(t, err, []string{"Telegram 返回失败：gateway rejected /bot***/sendMessage"}, []string{"请求 Telegram API"})
	})

	t.Run("token 很短时不改坏状态码与业务文案", func(t *testing.T) {
		// 只替换「/bot」后面的 token。对整条文本做替换的话，token 误填成 "4" 会把「HTTP 404」改成「HTTP ***0***」，
		// 误填成 "Not" 会把 description 改成「*** Found」，用户反而看不懂错误。
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"ok":false,"error_code":404,"description":"Not Found"}`))
		}))
		defer server.Close()

		for _, shortToken := range []string{"4", "Not"} {
			t.Run("token="+shortToken, func(t *testing.T) {
				err := sendTelegram(map[string]string{
					"token":    shortToken,
					"chat_id":  "10001",
					"api_host": server.URL,
				}, "标题", "正文")
				assertMessage(t, err, []string{"HTTP 404", `"error_code":404`, `"description":"Not Found"`}, []string{"***"})
			})
		}
	})
}

// TestSendWecomAppTrustedIPHint：errcode 60020（企业可信 IP）时追加排查提示，取 token 与发消息两处都挂。
//
// 提示只罗列本次生效的配置（渠道代理 / 系统代理 / 未配置、base_url 反代），不自行断言出口路径，
// 实际出口以 errmsg 里的 from ip 为准。其它 errcode 的原有文案一字不变，由
// TestSendWecomAppReturnsEnterpriseError 的逐字断言锁住。
func TestSendWecomAppTrustedIPHint(t *testing.T) {
	const errmsg = "not allow to access from your ip, hint: [x], from ip: 1.2.3.4"
	trustedIPBody := `{"errcode":60020,"errmsg":"` + errmsg + `"}`
	trustedIPSendBody := `{"errcode":60020,"errmsg":"` + errmsg + `","invaliduser":"zhangsan"}`

	// 不配 base_url 时让包级端点指向 loopback 源站（http 目标，经 HTTP 代理时不走 CONNECT）。
	useDefaultEndpoints := func(t *testing.T, origin *wecomAppRecorder) {
		t.Helper()
		oldTokenURL, oldSendURL := wecomAppTokenURL, wecomAppSendURL
		wecomAppTokenURL = origin.URL + "/cgi-bin/gettoken"
		wecomAppSendURL = origin.URL + "/cgi-bin/message/send"
		t.Cleanup(func() {
			wecomAppTokenURL, wecomAppSendURL = oldTokenURL, oldSendURL
		})
	}

	assertHint := func(t *testing.T, err error, stage string, wants, notWants []string) {
		t.Helper()
		if err == nil {
			t.Fatal("errcode 60020 应当返回错误")
		}
		msg := err.Error()
		for _, want := range append([]string{stage, errmsg, "60020", "企业可信 IP", "from ip"}, wants...) {
			if !strings.Contains(msg, want) {
				t.Errorf("错误信息应当包含 %q，实际: %s", want, msg)
			}
		}
		for _, notWant := range notWants {
			if strings.Contains(msg, notWant) {
				t.Errorf("错误信息不应包含 %q，实际: %s", notWant, msg)
			}
		}
	}

	t.Run("取 token 返回 60020，经带密码的渠道代理", func(t *testing.T) {
		testutil.SetupTestEnv(t)
		origin := newWecomAppRecorder(t, wecomAppRecorderOptions{})
		proxy := newWecomAppRecorder(t, wecomAppRecorderOptions{tokenBody: trustedIPBody})
		useDefaultEndpoints(t, origin)

		err := sendWecomApp(wecomAppProxyTestConfig("", "http://u:proxy-pass-xyz@"+proxy.hostPort()), "标题", "正文")
		assertHint(t, err, "获取企业微信应用 access_token 失败",
			[]string{"渠道代理 http://" + proxy.hostPort()},
			[]string{"proxy-pass-xyz", "反代"})
	})

	t.Run("发消息返回 60020，经渠道代理 + base_url 反代", func(t *testing.T) {
		testutil.SetupTestEnv(t)
		origin := newWecomAppRecorder(t, wecomAppRecorderOptions{})
		proxy := newWecomAppRecorder(t, wecomAppRecorderOptions{sendBody: trustedIPBody})

		err := sendWecomApp(wecomAppProxyTestConfig(origin.URL+"/proxy-qyapi", proxy.URL), "标题", "正文")
		// 反代地址只保留 scheme://host，路径里可能藏着鉴权片段，不回显。
		// 面板无从知道 base_url 背后是不是真的反代服务器，出口 IP 只能说「若」，不能断言。
		assertHint(t, err, "发送企业微信应用消息失败",
			[]string{"渠道代理 http://" + proxy.hostPort(), "反代 http://" + origin.hostPort(),
				"若该地址是反代服务器，企业微信看到的是它的出口 IP"},
			[]string{"/proxy-qyapi", "企业微信看到的是反代服务器的出口 IP", "官方地址"})
	})

	t.Run("发消息返回 60020 且带 invaliduser，经系统代理", func(t *testing.T) {
		testutil.SetupTestEnv(t)
		origin := newWecomAppRecorder(t, wecomAppRecorderOptions{})
		globalProxy := newWecomAppRecorder(t, wecomAppRecorderOptions{sendBody: trustedIPSendBody})
		if err := model.SetConfig("proxy_url", globalProxy.URL); err != nil {
			t.Fatalf("set proxy_url: %v", err)
		}
		useDefaultEndpoints(t, origin)

		err := sendWecomApp(wecomAppProxyTestConfig("", ""), "标题", "正文")
		assertHint(t, err, "发送企业微信应用消息失败",
			[]string{"(invaliduser=zhangsan)", "系统代理 http://" + globalProxy.hostPort()},
			[]string{"渠道代理", "反代"})
	})

	t.Run("取 token 返回 60020，面板没配任何代理", func(t *testing.T) {
		testutil.SetupTestEnv(t)
		origin := newWecomAppRecorder(t, wecomAppRecorderOptions{tokenBody: trustedIPBody})
		useDefaultEndpoints(t, origin)

		err := sendWecomApp(wecomAppProxyTestConfig("", ""), "标题", "正文")
		// 不能说「直连」：没配面板代理时请求仍可能走进程环境变量 HTTP(S)_PROXY。
		assertHint(t, err, "获取企业微信应用 access_token 失败",
			[]string{"未配置面板代理", "HTTP(S)_PROXY"},
			[]string{"渠道代理", "系统代理", "反代"})
	})

	t.Run("取 token 返回 60020，base_url 填的是官方地址", func(t *testing.T) {
		// 用户照 placeholder「留空使用 https://qyapi.weixin.qq.com」把官方地址原样填进 base_url：请求直达企业微信，
		// 没有反代，提示不能再说「经 base_url 反代转发」。
		// 请求经 loopback 记录代理发出：http 目标走 absolute-form、不走 CONNECT，代理按路径应答、不转发，碰不到真实的企业微信。
		cases := []struct {
			name    string
			baseURL string
		}{
			{"小写", "http://qyapi.weixin.qq.com"},
			{"主机名大小写混写且带 /cgi-bin", "HTTP://QYAPI.Weixin.QQ.com/cgi-bin"},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				testutil.SetupTestEnv(t)
				proxy := newWecomAppRecorder(t, wecomAppRecorderOptions{tokenBody: trustedIPBody})

				err := sendWecomApp(wecomAppProxyTestConfig(tc.baseURL, proxy.URL), "标题", "正文")
				if uris := proxy.requestURIs(); len(uris) != 1 ||
					!strings.Contains(strings.ToLower(uris[0]), "qyapi.weixin.qq.com/cgi-bin/gettoken") {
					t.Fatalf("取 token 应当经记录代理发往官方地址，实际: %v", uris)
				}
				assertHint(t, err, "获取企业微信应用 access_token 失败",
					[]string{"渠道代理 http://" + proxy.hostPort(), "base_url 为企业微信官方地址"},
					[]string{"经 base_url 反代", "若该地址是反代服务器"})
			})
		}
	})

	t.Run("取 token 返回 60020，官方域名只出现在 base_url 的路径里", func(t *testing.T) {
		// 国内常见的通用反代写法把官方地址拼在反代地址的路径里：请求实际发往 gh-proxy.example.com，企业微信看到的是
		// 它的出口 IP。判断必须看主机名；按子串匹配会把它当成官方地址、提示「未经反代」，把排查引离真实存在的反代。
		// 同样经 loopback 记录代理发出（http 目标走 absolute-form），不解析、也不连 example.com。
		testutil.SetupTestEnv(t)
		const baseURL = "http://gh-proxy.example.com/https://qyapi.weixin.qq.com"
		proxy := newWecomAppRecorder(t, wecomAppRecorderOptions{tokenBody: trustedIPBody})

		err := sendWecomApp(wecomAppProxyTestConfig(baseURL, proxy.URL), "标题", "正文")
		if uris := proxy.requestURIs(); len(uris) != 1 || !strings.HasPrefix(uris[0], baseURL+"/cgi-bin/gettoken") {
			t.Fatalf("取 token 应当经记录代理发往反代地址，实际: %v", uris)
		}
		// 反代地址只回显 scheme://host，路径里的官方域名也不该出现。
		assertHint(t, err, "获取企业微信应用 access_token 失败",
			[]string{"渠道代理 http://" + proxy.hostPort(), "经 base_url 反代 http://gh-proxy.example.com 转发",
				"若该地址是反代服务器"},
			[]string{"官方地址", "qyapi.weixin.qq.com"})
	})
}

// TestRedactSecrets 锁住 HTTP≥400 正文脱敏 helper 的几条约束（#123）。端到端用例只覆盖得到常见形态，
// 替换顺序、空密钥、截断点这几处出错时端到端用例未必会红。
func TestRedactSecrets(t *testing.T) {
	const secret = "corp-secret-xyz/+="
	const truncated = "…（已截断）"

	cases := []struct {
		name    string
		in      string
		secrets []string
		want    string
	}{
		// PathEscape 不转义 + 与 =，所以这三种形态各不相同；EscapedPath 对这个密钥等于原文（/ + = 都不转义），由下一例覆盖。
		{"原文、QueryEscape、PathEscape 三种形态都替换",
			"a=corp-secret-xyz/+= b=corp-secret-xyz%2F%2B%3D c=corp-secret-xyz%2F+=", []string{secret}, "a=*** b=*** c=***"},
		// Go 写请求行用的是 EscapedPath：不转义 /、会转义空格，与 PathEscape（123:ab%2Fc%20d）、
		// QueryEscape（123%3Aab%2Fc+d）都对不上。telegram 的 token 就是以这种形态进代理错误页的。
		{"EscapedPath 形态（请求行路径里的写法）也替换",
			"u=/bot123:ab/c%20d/sendMessage", []string{"123:ab/c d"}, "u=/bot***/sendMessage"},
		{"空密钥跳过（否则每个字符之间都会插入 ***）", "abc", []string{"", "  "}, "abc"},
		{"密钥两端的空白不影响匹配", "k=abc", []string{"  abc "}, "k=***"},
		{"一个密钥是另一个的子串时先替换长的", "x-abcdef-x", []string{"abc", "abcdef"}, "x-***-x"},
		{"没有命中、没超长：原样返回", "HTTP 503: bad gateway", []string{secret}, "HTTP 503: bad gateway"},
		// 先截断再替换的话，跨在截断点上的密钥只剩前半截「corp-」，匹配不上，原样漏出去。
		{"先替换再截断：跨在截断点上的密钥不漏前半截",
			strings.Repeat("a", notifyErrorBodyEchoLimit-5) + secret + strings.Repeat("b", 100), []string{secret},
			strings.Repeat("a", notifyErrorBodyEchoLimit-5) + "***bb" + truncated},
		// 「中」占 3 字节，截断点落在字符中间时要退到字符边界，不留半个汉字。
		{"截断退到 UTF-8 字符边界",
			strings.Repeat("中", notifyErrorBodyEchoLimit), nil,
			strings.Repeat("中", notifyErrorBodyEchoLimit/3) + truncated},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := redactSecrets(tc.in, tc.secrets...); got != tc.want {
				t.Errorf("redactSecrets 结果不对\n实际: %q\n期望: %q", got, tc.want)
			}
		})
	}
}

// TestRedactTelegramBotPath 锁住 telegram 错误脱敏只动「/bot」后面那段（#123）。
// 端到端用例只覆盖请求行原样回显（含开头带空白的 token）与业务错误回显；各种转义形态、回显里两端空白的变体、
// 超长截断在这里逐条锁住，这几处出错时端到端用例未必会红。
func TestRedactTelegramBotPath(t *testing.T) {
	const truncated = "…（已截断）"

	cases := []struct {
		name  string
		in    string
		token string
		want  string
	}{
		{"只替换 /bot 后面那段：短 token 不碰状态码与业务文案",
			`POST /bot4/sendMessage HTTP 404: {"error_code":404}`, "4",
			`POST /bot***/sendMessage HTTP 404: {"error_code":404}`},
		{"EscapedPath 形态", "u=/bot123:ab/c%20d/sendMessage", "123:ab/c d", "u=/bot***/sendMessage"},
		// apiURL 拼的是没去空白的 token，开头的空白在请求行里是 %20：只按核心匹配时「/bot」锚不住。
		{"token 开头带空白", "u=/bot%20123:abc/sendMessage", " 123:abc", "u=/bot***/sendMessage"},
		// 核心那一组兜住的两种回显：开头空白在回显里被整个去掉，结尾空白被写成 +（夹在核心与 /sendMessage 之间）。
		{"开头空白在回显里被去掉", "u=/bot123:abc/sendMessage", " 123:abc", "u=/bot***/sendMessage"},
		{"结尾空白回显成 +", "u=/bot123:abc+/sendMessage", "123:abc ", "u=/bot***+/sendMessage"},
		// 按 query 转义了路径（「:」转义、「/」不转义，如 Python 的 quote）：「/bot」还在，靠 token 的 QueryEscape 形态锚住。
		{"按 query 转义路径（quote 形态）", "u=/bot123456%3Atok-xyz/sendMessage", "123456:tok-xyz", "u=/bot***/sendMessage"},
		// 拦截页把整条原 URL 再转义一遍塞进 query 参数：「/bot」变成 %2Fbot，要靠整串形态锚住，并保留 %2Fbot 的写法。
		{"整条 URL 被再转义一遍（/bot 变成 %2Fbot）",
			"login?url=http%3A%2F%2F127.0.0.1%3A1%2Fbot123456%3Atok-xyz%2FsendMessage", "123456:tok-xyz",
			"login?url=http%3A%2F%2F127.0.0.1%3A1%2Fbot***%2FsendMessage"},
		{"超长时截断", strings.Repeat("a", notifyErrorBodyEchoLimit+10), "123:abc",
			strings.Repeat("a", notifyErrorBodyEchoLimit) + truncated},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := redactTelegramBotPath(tc.in, tc.token); got != tc.want {
				t.Errorf("redactTelegramBotPath 结果不对\n实际: %q\n期望: %q", got, tc.want)
			}
		})
	}
}
