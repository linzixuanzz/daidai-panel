package model_test

import (
	"encoding/json"
	"testing"

	"daidai-panel/model"
)

// TestNotifyChannelRegistryHasNoStructuralDefects 兜住注册表里「写错了但编译得过」的那些形态问题。
// 这些错误单看代码不明显，但会让客户端渲染出空标题、空下拉、或者永远显示不出来的字段。
func TestNotifyChannelRegistryHasNoStructuralDefects(t *testing.T) {
	channels := model.NotifyChannelDefinitions()
	if len(channels) == 0 {
		t.Fatal("渠道注册表不应为空")
	}

	allowedWidgets := map[model.NotifyFieldWidget]bool{
		model.NotifyWidgetInput:    true,
		model.NotifyWidgetPassword: true,
		model.NotifyWidgetTextarea: true,
		model.NotifyWidgetSelect:   true,
	}

	seenTypes := make(map[string]bool, len(channels))
	for _, channel := range channels {
		if channel.Type == "" || channel.Name == "" {
			t.Errorf("渠道 %#v 缺少 type 或 name", channel)
			continue
		}
		if seenTypes[channel.Type] {
			t.Errorf("渠道类型 %s 重复注册", channel.Type)
		}
		seenTypes[channel.Type] = true

		if len(channel.Fields) == 0 {
			t.Errorf("渠道 %s 没有声明任何字段，客户端会渲染出一个空表单", channel.Type)
			continue
		}

		fieldKeys := make(map[string]bool, len(channel.Fields))
		for _, field := range channel.Fields {
			fieldKeys[field.Key] = true
		}

		for _, field := range channel.Fields {
			label := channel.Type + "." + field.Key

			if field.Key == "" || field.Label == "" {
				t.Errorf("渠道 %s 有字段缺少 key 或 label: %#v", channel.Type, field)
				continue
			}
			if !allowedWidgets[field.Widget] {
				t.Errorf("字段 %s 的 widget=%q 不在允许的四种之内", label, field.Widget)
			}

			if field.Widget == model.NotifyWidgetSelect && len(field.Options) == 0 {
				t.Errorf("字段 %s 是 select 但没有 options，客户端会渲染出一个空下拉", label)
			}
			if field.Widget != model.NotifyWidgetSelect && len(field.Options) > 0 {
				t.Errorf("字段 %s 不是 select 却带了 options，客户端不会渲染它们", label)
			}

			// select 的默认值必须真的在选项里，否则客户端预填出一个选不中的值。
			if field.Widget == model.NotifyWidgetSelect && field.Default != "" {
				matched := false
				for _, option := range field.Options {
					if option.Value == field.Default {
						matched = true
						break
					}
				}
				if !matched {
					t.Errorf("字段 %s 的默认值 %q 不在 options 里", label, field.Default)
				}
			}

			for _, option := range field.Options {
				if option.Value == "" && option.Label == "" {
					t.Errorf("字段 %s 存在完全为空的 option", label)
				}
			}

			if field.ShowWhen == nil {
				continue
			}
			if field.ShowWhen.Key == "" || len(field.ShowWhen.Values) == 0 {
				t.Errorf("字段 %s 的 show_when 不完整: %#v", label, field.ShowWhen)
				continue
			}
			// 条件引用的字段必须在同一个渠道里，否则这个字段永远不会显示。
			if !fieldKeys[field.ShowWhen.Key] {
				t.Errorf("字段 %s 的 show_when 引用了本渠道不存在的字段 %q，该字段将永远不可见",
					label, field.ShowWhen.Key)
			}
			if field.ShowWhen.Key == field.Key {
				t.Errorf("字段 %s 的 show_when 引用了它自己", label)
			}
		}
	}
}

// TestNotifyChannelDuplicateKeysAreMutuallyExclusive 允许同一个键在一个渠道里出现多次
// （wecom 的 content_template 在 text 和 markdown 分支下文案不同，就是这么写的），
// 但要求这些声明必须互斥，否则客户端会同时渲染出两个绑定同一个 key 的输入框，
// 用户改一个另一个不跟着变，保存时后写的覆盖先写的。
func TestNotifyChannelDuplicateKeysAreMutuallyExclusive(t *testing.T) {
	for _, channel := range model.NotifyChannelDefinitions() {
		grouped := make(map[string][]model.NotifyFieldDefinition)
		for _, field := range channel.Fields {
			grouped[field.Key] = append(grouped[field.Key], field)
		}

		for key, fields := range grouped {
			if len(fields) < 2 {
				continue
			}

			// 条件键必须一致，否则「互斥」无从谈起。
			conditionKey := ""
			seenValues := make(map[string]bool)
			for _, field := range fields {
				if field.ShowWhen == nil {
					t.Errorf("渠道 %s 的字段 %s 声明了 %d 次，但其中一条没有 show_when，会和其它条同时显示",
						channel.Type, key, len(fields))
					break
				}
				if conditionKey == "" {
					conditionKey = field.ShowWhen.Key
				} else if conditionKey != field.ShowWhen.Key {
					t.Errorf("渠道 %s 的字段 %s 多次声明用了不同的 show_when 键（%s / %s），无法保证互斥",
						channel.Type, key, conditionKey, field.ShowWhen.Key)
					break
				}
				for _, value := range field.ShowWhen.Values {
					if seenValues[value] {
						t.Errorf("渠道 %s 的字段 %s 在 %s=%s 时会同时显示两个输入框",
							channel.Type, key, conditionKey, value)
					}
					seenValues[value] = true
				}
			}

			// 互斥只保证「同一时刻只渲染一个输入框」，保证不了「客户端按哪一条取默认值」。
			//
			// 客户端的表单状态是按 **key** 存的，不是按声明下标：APP 的
			// buildNotifyFieldSeeds 用 `{for field in fields: field.key: ... ?? field.defaultValue}`
			// 这种 map 推导式，重复 key 后写覆盖先写，于是种子值恒等于**最后一条**声明的
			// Default，与此刻显示的是哪一条无关。Web 端 configData[key] 同理。
			//
			// 现在两条 content_template 的 Default 都是空串，所以看不出问题。
			// 一旦有人只给其中一条加 withDefault，用户在另一个分支下就会看到一个
			// 不属于该分支的预填值 —— 不报错、不崩、只是填错。
			// 同理 Required / Widget 也必须一致：客户端的必填校验和控件分派虽然读的是
			// 当前可见的那条，但一旦两条不一致，两个分支的行为就会不对称到没人预期得到。
			//
			// 要表达「不同分支不同默认值」，正确做法是拆成两个不同的 key。
			first := fields[0]
			for _, field := range fields[1:] {
				if field.Default != first.Default {
					t.Errorf("渠道 %s 的字段 %s 多次声明给了不同的 Default（%q / %q）。\n"+
						"客户端按 key 存表单状态，重复 key 的默认值会互相覆盖，取到哪一条与当前分支无关。\n"+
						"要按分支给不同默认值，请拆成两个不同的 key。",
						channel.Type, key, first.Default, field.Default)
				}
				if field.Required != first.Required {
					t.Errorf("渠道 %s 的字段 %s 多次声明的 Required 不一致（%v / %v），两个分支的校验行为会不对称",
						channel.Type, key, first.Required, field.Required)
				}
				if field.Widget != first.Widget {
					t.Errorf("渠道 %s 的字段 %s 多次声明用了不同的 widget（%s / %s），切分支时控件会换一种，已填内容的去向没有定义",
						channel.Type, key, first.Widget, field.Widget)
				}
			}
		}
	}
}

// TestNotifyChannelTypesRemainBackwardCompatible 冻结 /api/notifications/types 的
// type / name / 顺序三项。
//
// handler 的 Types() 已经改成直接吐注册表，好处是渠道列表不可能再和字段表分叉；
// 代价是「改注册表」现在会连带改公开接口。这条用例保证那次改造以及后续改动都是
// **纯可加**的：老客户端只读 type 和 name，多出来的 icon / fields 对它们无感。
func TestNotifyChannelTypesRemainBackwardCompatible(t *testing.T) {
	expected := [][2]string{
		{"webhook", "Webhook"},
		{"email", "邮件"},
		{"telegram", "Telegram"},
		{"dingtalk", "钉钉"},
		{"wecom", "企业微信机器人"},
		{"wecom_app", "企业微信应用"},
		{"bark", "Bark"},
		{"pushplus", "PushPlus"},
		{"serverchan", "Server酱"},
		{"feishu", "飞书"},
		{"gotify", "Gotify"},
		{"pushdeer", "PushDeer"},
		{"pushme", "PushMe"},
		{"chanify", "Chanify"},
		{"igot", "iGot"},
		{"qmsg", "Qmsg"},
		{"pushover", "Pushover"},
		{"discord", "Discord"},
		{"slack", "Slack"},
		{"ntfy", "ntfy"},
		{"wxpusher", "WxPusher / ClawBot(iLink)"},
		{"custom", "自定义"},
	}

	channels := model.NotifyChannelDefinitions()
	if len(channels) != len(expected) {
		t.Fatalf("渠道数量变了：期望 %d 个，实际 %d 个。\n新增渠道请同步更新本用例的基线。",
			len(expected), len(channels))
	}
	for i, want := range expected {
		if channels[i].Type != want[0] || channels[i].Name != want[1] {
			t.Errorf("第 %d 个渠道与基线不符\n  期望: {type:%q name:%q}\n  实际: {type:%q name:%q}",
				i, want[0], want[1], channels[i].Type, channels[i].Name)
		}
	}
}

// TestPushplusQQChannelStaysWiredToOptionField 钉住 PushPlus 的 QQ 机器人渠道。
//
// 这个渠道要能用，两件事必须**同时**成立，而它们是分开声明的：
//
//   - channel 下拉里有 qq，否则用户根本选不到 QQ 机器人；
//   - option 的 show_when 命中 qq，否则选了 qq 之后「群配置编码」那个输入框不出现，
//     用户只能发个人消息，群消息连填的地方都没有。
//
// 缺任何一半都不会报错、不会崩，只是功能少一块 —— 正是最容易在后续改动里被顺手删掉
// 又没人发现的那种。所以钉在这里，而不是靠人记着两处同步。
func TestPushplusQQChannelStaysWiredToOptionField(t *testing.T) {
	channel, exists := model.GetNotifyChannelDefinition("pushplus")
	if !exists {
		t.Fatal("pushplus 应当是已注册渠道")
	}

	var channelField, optionField *model.NotifyFieldDefinition
	for i := range channel.Fields {
		switch channel.Fields[i].Key {
		case "channel":
			channelField = &channel.Fields[i]
		case "option":
			optionField = &channel.Fields[i]
		}
	}
	if channelField == nil || optionField == nil {
		t.Fatal("pushplus 应当同时声明 channel 和 option 两个字段")
	}

	hasQQ := false
	for _, option := range channelField.Options {
		if option.Value == "qq" {
			hasQQ = true
			break
		}
	}
	if !hasQQ {
		t.Error("pushplus 的 channel 下拉里没有 qq，用户选不到官方的 QQ 机器人渠道")
	}

	if optionField.ShowWhen == nil || optionField.ShowWhen.Key != "channel" {
		t.Fatalf("pushplus 的 option 应当按 channel 显隐，实际 %#v", optionField.ShowWhen)
	}
	matchedQQ := false
	for _, value := range optionField.ShowWhen.Values {
		if value == "qq" {
			matchedQQ = true
			break
		}
	}
	if !matchedQQ {
		t.Errorf("pushplus 的 option 在 channel=qq 时不显示，QQ 群配置编码没有输入框可填。当前命中值：%v",
			optionField.ShowWhen.Values)
	}
}

// TestNotifyChannelDefinitionsReturnsDeepCopy 保证调用方改不坏全局注册表。
// Options 是切片、ShowWhen 是指针，浅拷贝挡不住。
func TestNotifyChannelDefinitionsReturnsDeepCopy(t *testing.T) {
	first := model.NotifyChannelDefinitions()
	for i := range first {
		for j := range first[i].Fields {
			first[i].Fields[j].Label = "被改坏了"
			for k := range first[i].Fields[j].Options {
				first[i].Fields[j].Options[k].Label = "被改坏了"
			}
			if first[i].Fields[j].ShowWhen != nil {
				first[i].Fields[j].ShowWhen.Key = "被改坏了"
				first[i].Fields[j].ShowWhen.Values = []string{"被改坏了"}
			}
		}
	}

	second := model.NotifyChannelDefinitions()
	for _, channel := range second {
		for _, field := range channel.Fields {
			if field.Label == "被改坏了" {
				t.Fatalf("渠道 %s 的字段 %s 被上一次调用改坏了，说明没有深拷贝", channel.Type, field.Key)
			}
			for _, option := range field.Options {
				if option.Label == "被改坏了" {
					t.Fatalf("渠道 %s 字段 %s 的 options 被改坏了", channel.Type, field.Key)
				}
			}
			if field.ShowWhen != nil && field.ShowWhen.Key == "被改坏了" {
				t.Fatalf("渠道 %s 字段 %s 的 show_when 被改坏了", channel.Type, field.Key)
			}
		}
	}
}

// TestNotifyChannelConfigKeysIsDeduplicatedUnion 是绑定用例的前置保障：
// 它取的键集合一旦算错，那条最重要的用例就会跟着失真。
func TestNotifyChannelConfigKeysIsDeduplicatedUnion(t *testing.T) {
	keys := model.NotifyChannelConfigKeys()
	if len(keys) == 0 {
		t.Fatal("键集合不应为空")
	}

	seen := make(map[string]bool, len(keys))
	for i, key := range keys {
		if seen[key] {
			t.Errorf("键 %q 重复出现", key)
		}
		seen[key] = true
		if i > 0 && keys[i-1] > key {
			t.Errorf("键集合未按字典序排序: %q 出现在 %q 之后", key, keys[i-1])
		}
	}

	for _, channel := range model.NotifyChannelDefinitions() {
		for _, field := range channel.Fields {
			if !seen[field.Key] {
				t.Errorf("渠道 %s 声明的字段 %s 没有出现在键集合里", channel.Type, field.Key)
			}
		}
	}
}

// TestNotifyChannelDefinitionsSerializeWithStableJSONKeys 锁死下发给客户端的 JSON 键名。
// 客户端是照着这些键名解析的，改名等于让所有已发布的 APP 集体失明。
func TestNotifyChannelDefinitionsSerializeWithStableJSONKeys(t *testing.T) {
	channel, exists := model.GetNotifyChannelDefinition("wecom")
	if !exists {
		t.Fatal("wecom 应当是已注册渠道")
	}

	raw, err := json.Marshal(channel)
	if err != nil {
		t.Fatalf("序列化失败: %v", err)
	}

	var decoded map[string]interface{}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("反序列化失败: %v", err)
	}
	for _, key := range []string{"type", "name", "icon", "fields"} {
		if _, ok := decoded[key]; !ok {
			t.Errorf("渠道 JSON 缺少键 %q", key)
		}
	}

	fields, ok := decoded["fields"].([]interface{})
	if !ok || len(fields) == 0 {
		t.Fatalf("fields 应当是非空数组，实际: %#v", decoded["fields"])
	}

	// 找一条带全部可选属性的字段做断言：wecom 的 image_base64 同时有
	// required 和 show_when，msg_type 同时有 default 和 options。
	byKey := make(map[string]map[string]interface{})
	for _, item := range fields {
		field, ok := item.(map[string]interface{})
		if !ok {
			t.Fatalf("字段应当是对象，实际: %#v", item)
		}
		byKey[field["key"].(string)] = field
	}

	// label / placeholder 的键名同样要钉死。
	// 上面 TestNotifyChannelRegistryHasNoStructuralDefects 断言的是 Go 结构体字段
	// （field.Label != ""），改 json tag 它一条都不会红；而客户端读的是 JSON 键名
	// （Dart 侧 json['label'] / json['placeholder']），改名后表单会静默退化成
	// 「标题变成 key、hint 消失」，没有任何报错。
	//
	// 按**下标**配对而不是按 key 查表：wecom 的 content_template 声明了两次
	// （text 分支和 markdown 分支文案不同），按 key 查表只能拿到后声明的那条。
	// 比对的是 Go 值本身而不是写死的中文，所以改文案不会误报，只有改键名才会红。
	if len(fields) != len(channel.Fields) {
		t.Fatalf("序列化后的字段数与注册表不一致：JSON %d 条，注册表 %d 条",
			len(fields), len(channel.Fields))
	}
	for i, field := range channel.Fields {
		encoded, ok := fields[i].(map[string]interface{})
		if !ok {
			t.Fatalf("第 %d 个字段应当是对象，实际: %#v", i, fields[i])
		}
		if got, _ := encoded["key"].(string); got != field.Key {
			t.Fatalf("第 %d 个字段的 key 键名或取值不对\n  期望: %q\n  实际: %#v",
				i, field.Key, encoded["key"])
		}
		if got, _ := encoded["label"].(string); got != field.Label {
			t.Errorf("字段 %s 的 label 键名或取值不对\n  期望: %q\n  实际: %#v",
				field.Key, field.Label, encoded["label"])
		}
		if field.Placeholder == "" {
			continue
		}
		if got, _ := encoded["placeholder"].(string); got != field.Placeholder {
			t.Errorf("字段 %s 的 placeholder 键名或取值不对\n  期望: %q\n  实际: %#v",
				field.Key, field.Placeholder, encoded["placeholder"])
		}
	}

	msgType := byKey["msg_type"]
	if msgType["widget"] != "select" {
		t.Errorf("msg_type 的 widget 应为 select，实际 %#v", msgType["widget"])
	}
	if msgType["default"] != "text" {
		t.Errorf("msg_type 的 default 应为 text，实际 %#v", msgType["default"])
	}
	options, ok := msgType["options"].([]interface{})
	if !ok || len(options) == 0 {
		t.Fatalf("msg_type 应当带非空 options，实际 %#v", msgType["options"])
	}
	// 选项元素的键名也要钉死：客户端读的是 item['value'] / item['label']，
	// 读不到 value 的选项会被整条丢弃，下拉退化成空、再退化成普通输入框。
	// 这两个键来自 SystemConfigOption，系统配置的 enum 下拉共用同一个结构。
	firstOption, ok := options[0].(map[string]interface{})
	if !ok {
		t.Fatalf("options 元素应当是对象，实际 %#v", options[0])
	}
	if got, _ := firstOption["value"].(string); got != "text" {
		t.Errorf("options 元素的 value 键名或取值不对，实际 %#v", firstOption["value"])
	}
	if got, _ := firstOption["label"].(string); got == "" {
		t.Errorf("options 元素的 label 键名不对或为空，实际 %#v", firstOption["label"])
	}

	imageBase64 := byKey["image_base64"]
	if imageBase64["required"] != true {
		t.Errorf("image_base64 应当是必填，实际 %#v", imageBase64["required"])
	}
	showWhen, ok := imageBase64["show_when"].(map[string]interface{})
	if !ok {
		t.Fatalf("image_base64 应当带 show_when，实际 %#v", imageBase64["show_when"])
	}
	if showWhen["key"] != "msg_type" {
		t.Errorf("show_when.key 应为 msg_type，实际 %#v", showWhen["key"])
	}
	// show_when.values 是数组：客户端按「当前值落在这个数组里」判显隐，
	// 键名或类型变了会让条件字段永远显示不出来。
	if values, ok := showWhen["values"].([]interface{}); !ok || len(values) == 0 {
		t.Errorf("show_when.values 应当是非空数组，实际 %#v", showWhen["values"])
	}

	// url 这类普通字段不应该带 required / show_when / default 这些空值键，
	// 否则老客户端可能把 "required": false 误读成存在性信号。
	webhook, exists := model.GetNotifyChannelDefinition("webhook")
	if !exists {
		t.Fatal("webhook 应当是已注册渠道")
	}
	rawWebhook, err := json.Marshal(webhook.Fields[0])
	if err != nil {
		t.Fatalf("序列化失败: %v", err)
	}
	var webhookField map[string]interface{}
	if err := json.Unmarshal(rawWebhook, &webhookField); err != nil {
		t.Fatalf("反序列化失败: %v", err)
	}
	for _, key := range []string{"show_when", "options", "default"} {
		if _, ok := webhookField[key]; ok {
			t.Errorf("webhook.url 不该带空的 %q 键", key)
		}
	}
}

// TestNotifyChannelProxyFieldDeclared 单独锁住「telegram 与 wecom_app 都声明了 proxy」（#123）。
//
// 为什么需要单独一条：TestNotifySchemaCoversAllConfigKeysReadByNotifier 比的是全渠道键的并集。
// telegram 早就读取并声明了 proxy，所以 wecom_app 这一侧「读了没声明」（用户没有输入框可填）
// 或「声明了没读」（假字段）都不会让它变红。读取那一半由 service 包的 TestSendWecomApp*Proxy* 行为用例兜住，
// 声明这一半靠这里。
func TestNotifyChannelProxyFieldDeclared(t *testing.T) {
	var labels, placeholders []string
	for _, channelType := range []string{"telegram", "wecom_app"} {
		channel, exists := model.GetNotifyChannelDefinition(channelType)
		if !exists {
			t.Fatalf("%s 应当是已注册渠道", channelType)
		}

		proxyIndex, baseURLIndex, count := -1, -1, 0
		for i, field := range channel.Fields {
			switch field.Key {
			case "proxy":
				proxyIndex = i
				count++
			case "base_url":
				baseURLIndex = i
			}
		}
		if count != 1 {
			t.Fatalf("%s 应当恰好声明一次 proxy，实际 %d 次", channelType, count)
		}

		field := channel.Fields[proxyIndex]
		if field.Widget != model.NotifyWidgetInput {
			t.Errorf("%s.proxy 应当是 input，实际 %q", channelType, field.Widget)
		}
		// notifier.go 不对 proxy 判空（留空就回落系统代理），按注册表的 required 口径不能标必填。
		if field.Required {
			t.Errorf("%s.proxy 不应标 required", channelType)
		}
		// 留空的语义是「回落系统设置 proxy_url」，没有固定的回退值；写了 Default 会让客户端预填出一个假值。
		if field.Default != "" {
			t.Errorf("%s.proxy 不应带 Default，实际 %q", channelType, field.Default)
		}
		// 代理与消息类型无关，任何分支下都得能填。
		if field.ShowWhen != nil {
			t.Errorf("%s.proxy 不应带 show_when，实际 %#v", channelType, field.ShowWhen)
		}
		// wecom_app 的 proxy 紧跟在 base_url 后面：两者都管「请求从哪儿出去」，放在一起用户才看得出二者可以叠加。
		if channelType == "wecom_app" && proxyIndex != baseURLIndex+1 {
			t.Errorf("wecom_app.proxy 应当紧跟在 base_url 之后，实际 base_url=%d proxy=%d", baseURLIndex, proxyIndex)
		}

		labels = append(labels, field.Label)
		placeholders = append(placeholders, field.Placeholder)
	}

	// 两个渠道的 proxy 语义完全一致，文案也必须一致，免得用户以为两边规则不同。
	if labels[0] != labels[1] || placeholders[0] != placeholders[1] {
		t.Errorf("telegram 与 wecom_app 的 proxy 文案应当一致，实际 label=%q placeholder=%q", labels, placeholders)
	}
}
