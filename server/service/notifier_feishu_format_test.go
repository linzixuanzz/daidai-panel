package service

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"reflect"
	"strconv"
	"testing"
	"time"

	"daidai-panel/testutil"
)

// issue #172：飞书渠道按 msg_type 发文本 / 富文本 / 卡片。
// 双向绑定护栏（notifier_schema_binding_test.go）是全渠道并集，msg_type 早被别的渠道读了，查不出飞书漏读，
// 所以这里逐种类型断言完整报文结构，顺带钉住「老配置没有 msg_type 时报文不变」与加签。

func feishuConfig(extra map[string]string) func(string) map[string]string {
	return func(serverURL string) map[string]string {
		cfg := map[string]string{"webhook": serverURL + "/feishu"}
		for key, value := range extra {
			cfg[key] = value
		}
		return cfg
	}
}

// feishuPayload 发一次并把唯一那条请求的报文解成 map；顺带确认只发了一次、路径对。
func feishuPayload(t *testing.T, extra map[string]string, title, content, contentType string) map[string]interface{} {
	t.Helper()
	got := sendCapturedWithFormat(t, "feishu", feishuConfig(extra), title, content, contentType)
	if len(got) != 1 {
		t.Fatalf("expected one feishu request, got %#v", got)
	}
	if got[0].URI != "/feishu" {
		t.Fatalf("unexpected feishu uri %q", got[0].URI)
	}
	return decodeCapturedJSON(t, got[0])
}

// normalizeJSON 把期望值也走一遍 JSON 往返，和解出来的报文用同一套类型比较。
func normalizeJSON(t *testing.T, value interface{}) map[string]interface{} {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal expected: %v", err)
	}
	out := map[string]interface{}{}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal expected: %v", err)
	}
	return out
}

func TestSendFeishuMessageTypes(t *testing.T) {
	testutil.SetupTestEnv(t)

	legacyText := map[string]interface{}{
		"msg_type": "text",
		"content":  map[string]interface{}{"text": "标题\n第一行\n第二行"},
	}

	t.Run("old config without msg_type stays text", func(t *testing.T) {
		got := feishuPayload(t, nil, "标题", "第一行\n第二行", "")
		if want := normalizeJSON(t, legacyText); !reflect.DeepEqual(got, want) {
			t.Fatalf("payload = %#v, want %#v", got, want)
		}
	})

	t.Run("explicit text", func(t *testing.T) {
		got := feishuPayload(t, map[string]string{"msg_type": "text"}, "标题", "第一行\n第二行", "")
		if want := normalizeJSON(t, legacyText); !reflect.DeepEqual(got, want) {
			t.Fatalf("payload = %#v, want %#v", got, want)
		}
	})

	t.Run("unknown value falls back to text", func(t *testing.T) {
		got := feishuPayload(t, map[string]string{"msg_type": "markdown"}, "标题", "第一行\n第二行", "")
		if want := normalizeJSON(t, legacyText); !reflect.DeepEqual(got, want) {
			t.Fatalf("payload = %#v, want %#v", got, want)
		}
	})

	t.Run("text ignores declared markdown", func(t *testing.T) {
		// 飞书的消息类型是用户在渠道里选的，调用方声明的格式不覆盖它
		got := feishuPayload(t, nil, "标题", "第一行\n第二行", NotifyContentMarkdown)
		if want := normalizeJSON(t, legacyText); !reflect.DeepEqual(got, want) {
			t.Fatalf("payload = %#v, want %#v", got, want)
		}
	})

	t.Run("post splits lines and keeps blank lines", func(t *testing.T) {
		// msg_type 带空白与大写也要认；\r\n 先归一；空行保留成空段落
		got := feishuPayload(t, map[string]string{"msg_type": " Post "}, "日报", "第一行\r\n\r\n第三行 *不渲染*", "")
		want := normalizeJSON(t, map[string]interface{}{
			"msg_type": "post",
			"content": map[string]interface{}{
				"post": map[string]interface{}{
					"zh_cn": map[string]interface{}{
						"title": "日报",
						"content": []interface{}{
							[]interface{}{map[string]string{"tag": "text", "text": "第一行"}},
							[]interface{}{map[string]string{"tag": "text", "text": ""}},
							[]interface{}{map[string]string{"tag": "text", "text": "第三行 *不渲染*"}},
						},
					},
				},
			},
		})
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("payload = %#v, want %#v", got, want)
		}
	})

	cardPayload := func(title, tag, content string) map[string]interface{} {
		return normalizeJSON(t, map[string]interface{}{
			"msg_type": "interactive",
			"card": map[string]interface{}{
				"config": map[string]interface{}{"wide_screen_mode": true},
				"header": map[string]interface{}{
					"title":    map[string]string{"tag": "plain_text", "content": title},
					"template": "blue",
				},
				"elements": []interface{}{
					map[string]interface{}{"tag": "div", "text": map[string]string{"tag": tag, "content": content}},
				},
			},
		})
	}

	t.Run("interactive defaults to plain_text", func(t *testing.T) {
		got := feishuPayload(t, map[string]string{"msg_type": "interactive"}, "日报", "a *b* <c> [d]", "")
		if want := cardPayload("日报", "plain_text", "a *b* <c> [d]"); !reflect.DeepEqual(got, want) {
			t.Fatalf("payload = %#v, want %#v", got, want)
		}
	})

	t.Run("interactive declared text stays plain_text", func(t *testing.T) {
		got := feishuPayload(t, map[string]string{"msg_type": "interactive"}, "日报", "正文", NotifyContentText)
		if want := cardPayload("日报", "plain_text", "正文"); !reflect.DeepEqual(got, want) {
			t.Fatalf("payload = %#v, want %#v", got, want)
		}
	})

	t.Run("interactive declared markdown uses lark_md", func(t *testing.T) {
		got := feishuPayload(t, map[string]string{"msg_type": "interactive"}, "日报", "**加粗**", NotifyContentMarkdown)
		if want := cardPayload("日报", "lark_md", "**加粗**"); !reflect.DeepEqual(got, want) {
			t.Fatalf("payload = %#v, want %#v", got, want)
		}
	})

	t.Run("interactive html is stripped and stays plain_text", func(t *testing.T) {
		got := feishuPayload(t, map[string]string{"msg_type": "interactive"}, "日报", "<b>正文</b>", NotifyContentHTML)
		if want := cardPayload("日报", "plain_text", "正文"); !reflect.DeepEqual(got, want) {
			t.Fatalf("payload = %#v, want %#v", got, want)
		}
	})

	t.Run("interactive empty title falls back", func(t *testing.T) {
		got := feishuPayload(t, map[string]string{"msg_type": "interactive"}, "  ", "正文", "")
		if want := cardPayload("通知", "plain_text", "正文"); !reflect.DeepEqual(got, want) {
			t.Fatalf("payload = %#v, want %#v", got, want)
		}
	})
}

// TestSendFeishuSignedForEveryMessageType：三种类型都在报文顶层带 timestamp / sign，算法与改动前一致
// （以 "timestamp\nsecret" 为 HMAC-SHA256 的密钥、对空消息求值，再 base64）。
func TestSendFeishuSignedForEveryMessageType(t *testing.T) {
	testutil.SetupTestEnv(t)

	for _, msgType := range []string{"", "text", "post", "interactive"} {
		t.Run("msg_type="+msgType, func(t *testing.T) {
			extra := map[string]string{"secret": "s3cret"}
			if msgType != "" {
				extra["msg_type"] = msgType
			}
			before := time.Now().Unix()
			got := feishuPayload(t, extra, "标题", "正文", "")
			after := time.Now().Unix()

			wantType := msgType
			if wantType == "" {
				wantType = "text"
			}
			if got["msg_type"] != wantType {
				t.Fatalf("msg_type = %#v, want %q", got["msg_type"], wantType)
			}

			timestampText, _ := got["timestamp"].(string)
			timestamp, err := strconv.ParseInt(timestampText, 10, 64)
			if err != nil || timestamp < before || timestamp > after {
				t.Fatalf("timestamp = %#v, want a unix second in [%d, %d]", got["timestamp"], before, after)
			}
			mac := hmac.New(sha256.New, []byte(timestampText+"\n"+"s3cret"))
			if want := base64.StdEncoding.EncodeToString(mac.Sum(nil)); got["sign"] != want {
				t.Fatalf("sign = %#v, want %q", got["sign"], want)
			}
		})
	}
}
