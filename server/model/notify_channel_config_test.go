package model_test

import (
	"encoding/json"
	"strings"
	"testing"

	"daidai-panel/model"
)

// TestNormalizeNotifyChannelConfigCoercesScalarValues 锁死这条修复的核心行为。
//
// 真实事故：APP 把 smtp_ssl 写成了 JSON 布尔 false，服务端 sendToChannel 里
// json.Unmarshal 到 map[string]string 直接失败，整个邮件渠道的所有通知（含测试按钮）
// 全挂，报的还是一句用户看不懂的 cannot unmarshal bool into Go value of type string。
func TestNormalizeNotifyChannelConfigCoercesScalarValues(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  map[string]string
	}{
		{
			name:  "布尔归一成字符串（就是 APP 写坏 smtp_ssl 的那种）",
			input: `{"smtp_ssl":false,"smtp_host":"smtp.qq.com"}`,
			want:  map[string]string{"smtp_ssl": "false", "smtp_host": "smtp.qq.com"},
		},
		{
			name:  "整数归一成字符串",
			input: `{"timeout":30,"priority":5}`,
			want:  map[string]string{"timeout": "30", "priority": "5"},
		},
		{
			name:  "大整数不能被科学计数法毁掉",
			input: `{"max_size":102400000}`,
			want:  map[string]string{"max_size": "102400000"},
		},
		{
			name:  "小数原样保留",
			input: `{"ratio":0.5}`,
			want:  map[string]string{"ratio": "0.5"},
		},
		{
			name:  "null 视为没填",
			input: `{"proxy":null}`,
			want:  map[string]string{"proxy": ""},
		},
		{
			name:  "本来就是字符串的原样保留",
			input: `{"url":"https://example.com/webhook?a=1&b=2"}`,
			want:  map[string]string{"url": "https://example.com/webhook?a=1&b=2"},
		},
		{
			name:  "空串补成空对象",
			input: "",
			want:  map[string]string{},
		},
		{
			name:  "只有空白也补成空对象",
			input: "   \n\t ",
			want:  map[string]string{},
		},
		{
			name:  "custom 渠道的 headers/body 是 JSON 文本字符串，必须原样保留",
			input: `{"headers":"{\"Authorization\":\"Bearer xxx\"}","body":"{\"title\":\"{{title}}\"}"}`,
			want: map[string]string{
				"headers": `{"Authorization":"Bearer xxx"}`,
				"body":    `{"title":"{{title}}"}`,
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			normalized, err := model.NormalizeNotifyChannelConfig(tc.input)
			if err != nil {
				t.Fatalf("不应报错，实际: %v", err)
			}

			// 归一结果必须能被服务端真正的消费方式读出来，这才是这条修复的全部意义。
			var cfg map[string]string
			if err := json.Unmarshal([]byte(normalized), &cfg); err != nil {
				t.Fatalf("归一后仍无法反序列化成 map[string]string: %v\n  结果: %s", err, normalized)
			}

			if len(cfg) != len(tc.want) {
				t.Fatalf("键数量不符\n  期望: %#v\n  实际: %#v", tc.want, cfg)
			}
			for key, want := range tc.want {
				if cfg[key] != want {
					t.Errorf("键 %q\n  期望: %q\n  实际: %q", key, want, cfg[key])
				}
			}
		})
	}
}

// TestNormalizeNotifyChannelConfigRejectsUnrecoverableValues 断言不可逆的值必须报错，
// 而不是被 fmt.Sprint 成 map[a:1] 这种 Go 语法垃圾静默存下去。
//
// 这正是 custom 渠道原始 JSON 编辑框最容易踩的坑：headers 的值本身是 JSON 文本，
// 但在 config 里必须是字符串，写成嵌套对象就会命中这里。
func TestNormalizeNotifyChannelConfigRejectsUnrecoverableValues(t *testing.T) {
	cases := []struct {
		name      string
		input     string
		wantInMsg string
	}{
		{
			name:      "值是嵌套对象",
			input:     `{"headers":{"Authorization":"Bearer xxx"}}`,
			wantInMsg: `"headers"`,
		},
		{
			name:      "值是数组",
			input:     `{"uids":["u1","u2"]}`,
			wantInMsg: `"uids"`,
		},
		{
			name:      "顶层不是对象",
			input:     `["a","b"]`,
			wantInMsg: "必须是 JSON 对象",
		},
		{
			name:      "顶层是裸字符串",
			input:     `"just a string"`,
			wantInMsg: "必须是 JSON 对象",
		},
		{
			name:      "非法 JSON",
			input:     `{"url":`,
			wantInMsg: "不是合法的 JSON",
		},
		{
			name:      "结尾有多余内容",
			input:     `{"url":"https://a"} {"url":"https://b"}`,
			wantInMsg: "不是合法的 JSON",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := model.NormalizeNotifyChannelConfig(tc.input)
			if err == nil {
				t.Fatal("应当报错，实际通过了")
			}
			if !strings.Contains(err.Error(), tc.wantInMsg) {
				t.Errorf("错误信息应包含 %q，实际: %s", tc.wantInMsg, err.Error())
			}
			// 错误信息是直接展示给用户的，必须是中文而不是 Go 的原始类型错误。
			if strings.Contains(err.Error(), "cannot unmarshal") {
				t.Errorf("不应把 Go 原始错误透给用户: %s", err.Error())
			}
		})
	}
}

// TestNormalizeNotifyChannelConfigIsIdempotent 保证「保存 -> 读回 -> 再保存」不会越改越歪。
// Web 和 APP 都是把读回来的 config 原样再提交一次，不幂等会导致值在多次编辑后漂移。
func TestNormalizeNotifyChannelConfigIsIdempotent(t *testing.T) {
	inputs := []string{
		`{"smtp_ssl":false,"smtp_port":465,"smtp_host":"smtp.qq.com"}`,
		`{"url":"https://example.com/hook?a=1&b=2","body":"{\"title\":\"{{title}}\"}"}`,
		`{}`,
		"",
	}

	for _, input := range inputs {
		once, err := model.NormalizeNotifyChannelConfig(input)
		if err != nil {
			t.Fatalf("首次归一失败: %v", err)
		}
		twice, err := model.NormalizeNotifyChannelConfig(once)
		if err != nil {
			t.Fatalf("二次归一失败: %v", err)
		}
		if once != twice {
			t.Errorf("归一不幂等\n  输入: %s\n  一次: %s\n  两次: %s", input, once, twice)
		}
	}
}

// TestNormalizeNotifyChannelConfigKeepsUrlCharactersReadable 断言不做 HTML 转义。
// 默认的 json.Marshal 会把 URL query 里的与号转成 Unicode 转义序列，用户下次打开
// 原始 JSON 编辑框会看到一串不认识的字符，误以为配置被改坏了。
func TestNormalizeNotifyChannelConfigKeepsUrlCharactersReadable(t *testing.T) {
	normalized, err := model.NormalizeNotifyChannelConfig(`{"url":"https://a.com/x?a=1&b=2"}`)
	if err != nil {
		t.Fatalf("不应报错: %v", err)
	}
	if strings.Contains(normalized, "\\u0026") {
		t.Errorf("URL 里的与号被转义了: %s", normalized)
	}
}

// ---- #123 保存期校验：渠道代理地址 ----

// notifyProxyConfigJSON 构造只含 proxy 的 config JSON。用 json.Marshal 而不是手拼字符串，
// 避免 %zz 这类测试值里的特殊字符把 JSON 本身拼坏，测到的就不再是代理校验了。
func notifyProxyConfigJSON(t *testing.T, proxy string) string {
	t.Helper()
	data, err := json.Marshal(map[string]string{"proxy": proxy})
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	return string(data)
}

// TestValidateNotifyChannelConfigProxy 锁住渠道代理的保存期口径（#123）：与系统设置 proxy_url 完全一致，
// 且 telegram 与 wecom_app 两个声明了 proxy 的渠道都受约束。
//
// 拒绝列表里每一项都是实测过「在发送链路上静默失效或报错难懂」的输入（research/123-recon.md 回退语义实测表）：
// 漏写 scheme 与非法转义会让 url.Parse 失败、静默直连；localhost:7890 报 dial tcp :0；
// ftp:// 会被当成 HTTP 代理使用；只有 scheme 的地址报 dial :80 / :1080。
func TestValidateNotifyChannelConfigProxy(t *testing.T) {
	accepted := []struct {
		name  string
		value string
	}{
		{"空值（回落系统代理）", ""},
		{"只有空白视同空值", "   "},
		{"http", "http://127.0.0.1:7890"},
		{"https", "https://proxy.example.com:8443"},
		{"socks5 带账号密码", "socks5://user:pass@127.0.0.1:1080"},
		{"socks5h", "socks5h://127.0.0.1:1080"},
		{"大写 scheme", "HTTP://127.0.0.1:7890"},
		{"首尾空白", "  http://127.0.0.1:7890  "},
	}
	rejected := []struct {
		name  string
		value string
	}{
		{"漏写 scheme（最常见的误填，发送链路会静默直连）", "127.0.0.1:7890"},
		{"localhost:端口（被解析成 scheme=localhost、host 为空）", "localhost:7890"},
		{"非法转义", "%zz"},
		{"不支持的 scheme（发送链路会把它当 HTTP 代理用）", "ftp://127.0.0.1:21"},
		{"不支持的 scheme 且带密码（错误信息不得回显）", "ftp://user:secret-pass-xyz@127.0.0.1:21"},
		{"只有 http://", "http://"},
		{"只有 socks5://", "socks5://"},
	}

	for _, channelType := range []string{"telegram", "wecom_app"} {
		definition, ok := model.GetNotifyChannelDefinition(channelType)
		if !ok {
			t.Fatalf("%s 应当是已注册渠道", channelType)
		}
		label := ""
		for _, field := range definition.Fields {
			if field.Key == "proxy" {
				label = field.Label
			}
		}
		if label == "" {
			t.Fatalf("%s 没有声明 proxy 字段，保存期校验对它不会生效", channelType)
		}

		for _, tc := range accepted {
			t.Run(channelType+"/放行/"+tc.name, func(t *testing.T) {
				if err := model.ValidateNotifyChannelConfig(channelType, notifyProxyConfigJSON(t, tc.value)); err != nil {
					t.Fatalf("合法的代理地址 %q 不应被拦下，实际: %v", tc.value, err)
				}
			})
		}
		for _, tc := range rejected {
			t.Run(channelType+"/拒绝/"+tc.name, func(t *testing.T) {
				err := model.ValidateNotifyChannelConfig(channelType, notifyProxyConfigJSON(t, tc.value))
				if err == nil {
					t.Fatalf("非法的代理地址 %q 应当被拦下，实际通过了", tc.value)
				}
				msg := err.Error()
				// 必须点名字段：Web / APP 原样展示这句，用户要能对上是哪个输入框。
				if !strings.Contains(msg, "(proxy)") || !strings.Contains(msg, label) {
					t.Errorf("错误信息应同时包含字段名 (proxy) 与标签 %q，实际: %s", label, msg)
				}
				if strings.Contains(msg, "secret-pass-xyz") {
					t.Errorf("错误信息不得回显代理地址里的密码，实际: %s", msg)
				}
			})
		}
	}
}

// TestValidateNotifyChannelConfigIgnoresUndeclaredOrUnknown 断言校验只作用于「该渠道声明过的键」。
func TestValidateNotifyChannelConfigIgnoresUndeclaredOrUnknown(t *testing.T) {
	junk := notifyProxyConfigJSON(t, "127.0.0.1:7890")

	// webhook 没声明 proxy，服务端根本不读这个值；拦下它只会让用户莫名其妙地存不进去。
	if err := model.ValidateNotifyChannelConfig("webhook", junk); err != nil {
		t.Errorf("webhook 未声明 proxy，不应校验它，实际: %v", err)
	}
	// 未知类型没有字段声明可依，保存期不在这里另立规则。
	if err := model.ValidateNotifyChannelConfig("no-such-channel", junk); err != nil {
		t.Errorf("未知渠道类型应放行，实际: %v", err)
	}
	// 调用约定是先归一再校验；坏 JSON 由归一那一步报错，不该在这里变成另一种错误。
	if err := model.ValidateNotifyChannelConfig("telegram", `{"proxy":`); err != nil {
		t.Errorf("config 解不开时应放行（由归一那一步负责报错），实际: %v", err)
	}
}

// TestValidateNotifyChannelConfigChangeOnlyChecksNewValues 锁住 Update 的「只拦新值」口径（#123 决策）。
//
// Web 与 APP 保存时都整份回传 config。本校验上线前存进去的非法代理地址如果每次都被校验，
// 用户改任何别的字段都会 400 —— 违反「坏记录必须能被编辑保存」。所以只有值变了才校验。
func TestValidateNotifyChannelConfigChangeOnlyChecksNewValues(t *testing.T) {
	legacy := `{"chat_id":"c","proxy":"127.0.0.1:7890","token":"t"}`
	// 库里的旧值两侧带空白：NormalizeNotifyChannelConfig 不 trim 值，Web 编辑时又原样回传，这种存量真实存在。
	// 只有旧值一侧也 TrimSpace，它才算「未改动」；否则这条记录每次编辑都会 400。
	legacyPadded := `{"chat_id":"c","proxy":" 127.0.0.1:7890 ","token":"t"}`
	cases := []struct {
		name     string
		previous string
		next     string
		wantErr  bool
	}{
		{"存量非法值原样回传、只改了别的字段：放行", legacy, `{"chat_id":"c-new","proxy":"127.0.0.1:7890","token":"t"}`, false},
		{"只差首尾空白视为未改动：放行", legacy, `{"chat_id":"c","proxy":"  127.0.0.1:7890 ","token":"t"}`, false},
		{"旧值两侧带空白、原样回传：放行", legacyPadded, legacyPadded, false},
		{"旧值两侧带空白、回传的是 trim 后的值：放行", legacyPadded, legacy, false},
		{"改成另一个非法值：拦下", legacy, `{"chat_id":"c","proxy":"localhost:7890","token":"t"}`, true},
		{"改成合法值：放行", legacy, `{"chat_id":"c","proxy":"http://127.0.0.1:7890","token":"t"}`, false},
		{"清空：放行", legacy, `{"chat_id":"c","proxy":"","token":"t"}`, false},
		{"没有旧值（previous 为空串）：按新值校验", "", `{"proxy":"127.0.0.1:7890"}`, true},
		{"旧值为空、新填了非法值：拦下", `{"proxy":""}`, `{"proxy":"127.0.0.1:7890"}`, true},
		{"旧 config 别的键是坏的（嵌套对象 / 布尔），proxy 照常参与比较", `{"extra":{"a":1},"flag":false,"proxy":"127.0.0.1:7890"}`, `{"proxy":"127.0.0.1:7890"}`, false},
		{"旧 config 整体不是合法 JSON：视为没有旧值", `{"proxy":`, `{"proxy":"127.0.0.1:7890"}`, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := model.ValidateNotifyChannelConfigChange("telegram", tc.next, tc.previous)
			if tc.wantErr && err == nil {
				t.Fatal("应当拦下，实际通过了")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("应当放行，实际: %v", err)
			}
		})
	}
}
