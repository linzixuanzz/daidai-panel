package handler_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"daidai-panel/database"
	"daidai-panel/model"
	"daidai-panel/testutil"
)

func mustNotificationAdminHeaders(t *testing.T, username string) map[string]string {
	t.Helper()

	admin := testutil.MustCreateUser(t, username, "admin")
	token := testutil.MustCreateAccessToken(t, admin.Username, admin.Role)
	return map[string]string{"Authorization": "Bearer " + token}
}

// TestNotificationTypesExposesFieldSchema 断言 /notifications/types 下发字段定义，
// 且老客户端依赖的 type / name 两个键一个没少（纯可加）。
func TestNotificationTypesExposesFieldSchema(t *testing.T) {
	testutil.SetupTestEnv(t)

	engine := newProtectedRouter()
	rec := performRequest(engine, http.MethodGet, "/api/v1/notifications/types",
		mustNotificationAdminHeaders(t, "notify-schema-admin"))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	payload := decodeJSONMap(t, rec)
	items, ok := payload["data"].([]interface{})
	if !ok || len(items) == 0 {
		t.Fatalf("expected non-empty data array, got %#v", payload["data"])
	}

	expectedCount := len(model.NotifyChannelDefinitions())
	if len(items) != expectedCount {
		t.Fatalf("渠道数量与注册表不一致：接口 %d 个，注册表 %d 个", len(items), expectedCount)
	}

	totalSlots := 0
	for _, item := range items {
		channel, ok := item.(map[string]interface{})
		if !ok {
			t.Fatalf("渠道项应当是对象，实际 %#v", item)
		}
		channelType, _ := channel["type"].(string)
		if channelType == "" {
			t.Errorf("渠道项缺少 type: %#v", channel)
		}
		if name, _ := channel["name"].(string); name == "" {
			t.Errorf("渠道 %s 缺少 name", channelType)
		}

		fields, ok := channel["fields"].([]interface{})
		if !ok || len(fields) == 0 {
			t.Errorf("渠道 %s 没有下发 fields，APP 会回落本地冻结快照", channelType)
			continue
		}
		totalSlots += len(fields)
	}

	if totalSlots == 0 {
		t.Fatal("所有渠道加起来一个字段都没有")
	}
}

// TestNotificationTypesCoversWecomConditionalFields 单独盯住条件字段这块，
// 它是整份 schema 里唯一有分支语义的部分，也是最容易在翻译时漏掉的部分。
func TestNotificationTypesCoversWecomConditionalFields(t *testing.T) {
	testutil.SetupTestEnv(t)

	engine := newProtectedRouter()
	rec := performRequest(engine, http.MethodGet, "/api/v1/notifications/types",
		mustNotificationAdminHeaders(t, "notify-wecom-admin"))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	payload := decodeJSONMap(t, rec)
	items, _ := payload["data"].([]interface{})

	var wecom map[string]interface{}
	for _, item := range items {
		channel, _ := item.(map[string]interface{})
		if channel["type"] == "wecom" {
			wecom = channel
			break
		}
	}
	if wecom == nil {
		t.Fatal("响应里没有 wecom 渠道")
	}

	fields, _ := wecom["fields"].([]interface{})
	conditions := make(map[string][]string)
	for _, item := range fields {
		field, _ := item.(map[string]interface{})
		showWhen, ok := field["show_when"].(map[string]interface{})
		if !ok {
			continue
		}
		if showWhen["key"] != "msg_type" {
			t.Errorf("wecom 的条件字段应当都按 msg_type 分支，实际 %#v", showWhen["key"])
		}
		values, _ := showWhen["values"].([]interface{})
		key, _ := field["key"].(string)
		for _, value := range values {
			text, _ := value.(string)
			conditions[key] = append(conditions[key], text)
		}
	}

	for _, key := range []string{
		"content_template", "mentioned_list", "mentioned_mobile_list",
		"image_base64", "image_md5", "news_articles", "template_card_payload",
	} {
		if len(conditions[key]) == 0 {
			t.Errorf("wecom 的条件字段 %s 缺少 show_when", key)
		}
	}

	// content_template 声明了两次（text 一次、markdown 系一次），三个值都要能命中。
	joined := strings.Join(conditions["content_template"], ",")
	for _, value := range []string{"text", "markdown", "markdown_v2"} {
		if !strings.Contains(joined, value) {
			t.Errorf("content_template 应当在 msg_type=%s 时显示，实际条件为 %s", value, joined)
		}
	}
}

// TestCreateNotificationChannelCoercesNonStringConfigValues 是 §2.3 的核心回归：
// APP 曾把 smtp_ssl 写成 JSON 布尔，导致整个渠道的通知全挂。
func TestCreateNotificationChannelCoercesNonStringConfigValues(t *testing.T) {
	testutil.SetupTestEnv(t)

	engine := newProtectedRouter()
	headers := mustNotificationAdminHeaders(t, "notify-coerce-admin")

	rec := performJSONRequest(
		engine,
		http.MethodPost,
		"/api/v1/notifications",
		`{"name":"邮件通知","type":"email","config":"{\"smtp_ssl\":false,\"smtp_port\":465,\"smtp_host\":\"smtp.qq.com\"}"}`,
		headers,
		"",
	)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}

	var stored model.NotifyChannel
	if err := database.DB.Where("name = ?", "邮件通知").First(&stored).Error; err != nil {
		t.Fatalf("load created channel: %v", err)
	}

	// 关键断言：落库的 config 必须能被 service.sendToChannel 的读法解出来。
	var cfg map[string]string
	if err := json.Unmarshal([]byte(stored.Config), &cfg); err != nil {
		t.Fatalf("落库的 config 仍然无法反序列化成 map[string]string: %v\n  config: %s", err, stored.Config)
	}
	if cfg["smtp_ssl"] != "false" {
		t.Errorf("smtp_ssl 应归一成字符串 \"false\"，实际 %q", cfg["smtp_ssl"])
	}
	if cfg["smtp_port"] != "465" {
		t.Errorf("smtp_port 应归一成字符串 \"465\"，实际 %q", cfg["smtp_port"])
	}
	if cfg["smtp_host"] != "smtp.qq.com" {
		t.Errorf("smtp_host 应原样保留，实际 %q", cfg["smtp_host"])
	}
}

// TestCreateNotificationChannelRejectsNestedConfigValues 断言不可逆的值被 400 拦掉，
// 而不是被 fmt.Sprint 成 Go 语法垃圾静默存进库。
func TestCreateNotificationChannelRejectsNestedConfigValues(t *testing.T) {
	testutil.SetupTestEnv(t)

	engine := newProtectedRouter()
	headers := mustNotificationAdminHeaders(t, "notify-reject-admin")

	rec := performJSONRequest(
		engine,
		http.MethodPost,
		"/api/v1/notifications",
		`{"name":"自定义通知","type":"custom","config":"{\"headers\":{\"Authorization\":\"Bearer xxx\"}}"}`,
		headers,
		"",
	)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "headers") {
		t.Errorf("错误信息应当指出是哪个键出了问题，实际: %s", body)
	}

	var count int64
	database.DB.Model(&model.NotifyChannel{}).Where("name = ?", "自定义通知").Count(&count)
	if count != 0 {
		t.Errorf("被拒绝的请求不应落库，实际存在 %d 条", count)
	}
}

// TestUpdateNotificationChannelHealsLegacyBrokenConfig 是向后兼容的关键用例。
//
// 库里可能已经存在被老客户端写坏的记录（smtp_ssl 是 JSON 布尔）。加了校验之后，
// 用户编辑这条记录时提交的仍然是那份坏 config —— 如果直接 400，这条记录就永远改不了。
// 这里断言：坏值走的是「归一自愈」而不是「拒绝」。
func TestUpdateNotificationChannelHealsLegacyBrokenConfig(t *testing.T) {
	testutil.SetupTestEnv(t)

	broken := &model.NotifyChannel{
		Name: "历史坏记录",
		Type: "email",
		// 模拟老 APP 直接写进库的形态：smtp_ssl 是 JSON 布尔。
		Config:  `{"smtp_host":"smtp.qq.com","smtp_port":"465","smtp_ssl":false}`,
		Enabled: true,
	}
	if err := database.DB.Create(broken).Error; err != nil {
		t.Fatalf("create legacy channel: %v", err)
	}

	// 先确认这条记录在修复前确实是发不出去的。
	var probe map[string]string
	if err := json.Unmarshal([]byte(broken.Config), &probe); err == nil {
		t.Fatal("前置条件不成立：这份 config 本应无法反序列化成 map[string]string")
	}

	engine := newProtectedRouter()
	headers := mustNotificationAdminHeaders(t, "notify-heal-admin")

	// 用户原样把读回来的坏 config 再提交一次（Web 和 APP 都是这个行为）。
	rec := performJSONRequest(
		engine,
		http.MethodPut,
		"/api/v1/notifications/"+jsonNumber(broken.ID),
		`{"name":"历史坏记录","config":"{\"smtp_host\":\"smtp.qq.com\",\"smtp_port\":\"465\",\"smtp_ssl\":false}"}`,
		headers,
		"",
	)
	if rec.Code != http.StatusOK {
		t.Fatalf("坏记录必须能被编辑保存，否则用户永远改不了它。got %d: %s", rec.Code, rec.Body.String())
	}

	var healed model.NotifyChannel
	if err := database.DB.First(&healed, broken.ID).Error; err != nil {
		t.Fatalf("reload channel: %v", err)
	}

	var cfg map[string]string
	if err := json.Unmarshal([]byte(healed.Config), &cfg); err != nil {
		t.Fatalf("保存后 config 仍然是坏的: %v\n  config: %s", err, healed.Config)
	}
	if cfg["smtp_ssl"] != "false" {
		t.Errorf("smtp_ssl 应被自愈成字符串 \"false\"，实际 %q", cfg["smtp_ssl"])
	}
}

// TestUpdateNotificationChannelRejectsNonStringConfigField 断言 config 字段本身
// 必须是 JSON 字符串。Update 收的是 map[string]interface{}，客户端把 config 写成
// 对象会被 GORM 写成一段无法解析的内容。
func TestUpdateNotificationChannelRejectsNonStringConfigField(t *testing.T) {
	testutil.SetupTestEnv(t)

	channel := &model.NotifyChannel{
		Name:    "形态检查",
		Type:    "webhook",
		Config:  `{"url":"https://example.com/webhook"}`,
		Enabled: true,
	}
	if err := database.DB.Create(channel).Error; err != nil {
		t.Fatalf("create channel: %v", err)
	}

	engine := newProtectedRouter()
	headers := mustNotificationAdminHeaders(t, "notify-shape-admin")

	rec := performJSONRequest(
		engine,
		http.MethodPut,
		"/api/v1/notifications/"+jsonNumber(channel.ID),
		`{"config":{"url":"https://example.com/webhook"}}`,
		headers,
		"",
	)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}

	var reloaded model.NotifyChannel
	if err := database.DB.First(&reloaded, channel.ID).Error; err != nil {
		t.Fatalf("reload channel: %v", err)
	}
	if reloaded.Config != channel.Config {
		t.Errorf("被拒绝的请求不应改动已有 config，实际变成了 %s", reloaded.Config)
	}
}

// ---- #123 保存期代理地址校验 ----
//
// 口径（prd.md「决策」）：Create 总是校验；Update 只校验「新值」—— proxy 与库里现存值相同时不校验，
// 否则 Web / APP 整份回传 config 时，本校验上线前存进去的非法值会让用户改任何字段都存不进去。

// notifyProxyRequestBody 先把 config 序列化成字符串再塞进请求体，免得手写两层转义。
// config 为 nil 时请求体里不带 config 键。
func notifyProxyRequestBody(t *testing.T, fields map[string]interface{}, config map[string]string) string {
	t.Helper()

	body := make(map[string]interface{}, len(fields)+1)
	for key, value := range fields {
		body[key] = value
	}
	if config != nil {
		raw, err := json.Marshal(config)
		if err != nil {
			t.Fatalf("marshal config: %v", err)
		}
		body["config"] = string(raw)
	}
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal request body: %v", err)
	}
	return string(data)
}

func mustCreateNotifyChannelRow(t *testing.T, name, channelType, config string) *model.NotifyChannel {
	t.Helper()

	channel := &model.NotifyChannel{Name: name, Type: channelType, Config: config, Enabled: true}
	if err := database.DB.Create(channel).Error; err != nil {
		t.Fatalf("create channel: %v", err)
	}
	return channel
}

func mustReloadNotifyChannel(t *testing.T, id uint) model.NotifyChannel {
	t.Helper()

	var channel model.NotifyChannel
	if err := database.DB.First(&channel, id).Error; err != nil {
		t.Fatalf("reload channel: %v", err)
	}
	return channel
}

func mustDecodeStoredNotifyConfig(t *testing.T, channel model.NotifyChannel) map[string]string {
	t.Helper()

	var cfg map[string]string
	if err := json.Unmarshal([]byte(channel.Config), &cfg); err != nil {
		t.Fatalf("stored config is not map[string]string: %v\n  config: %s", err, channel.Config)
	}
	return cfg
}

// TestCreateNotificationChannelRejectsMalformedProxy：新建渠道时非法代理地址 400、点名字段、不落库；
// 合法值照常创建（校验只拦格式错误，不误伤）。
func TestCreateNotificationChannelRejectsMalformedProxy(t *testing.T) {
	testutil.SetupTestEnv(t)

	engine := newProtectedRouter()
	headers := mustNotificationAdminHeaders(t, "notify-proxy-create-admin")
	fields := map[string]interface{}{"name": "企业微信应用", "type": "wecom_app"}
	config := map[string]string{
		"corp_id":  "ww-demo",
		"secret":   "secret-demo",
		"agent_id": "1000001",
		"proxy":    "127.0.0.1:7890",
	}

	rec := performJSONRequest(engine, http.MethodPost, "/api/v1/notifications",
		notifyProxyRequestBody(t, fields, config), headers, "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("非法代理地址应当 400，实际 %d: %s", rec.Code, rec.Body.String())
	}
	if body := rec.Body.String(); !strings.Contains(body, "(proxy)") || !strings.Contains(body, "代理地址") {
		t.Errorf("错误信息应当点名 proxy 字段，实际: %s", body)
	}
	var count int64
	database.DB.Model(&model.NotifyChannel{}).Where("name = ?", "企业微信应用").Count(&count)
	if count != 0 {
		t.Fatalf("被拒绝的请求不应落库，实际存在 %d 条", count)
	}

	config["proxy"] = "http://127.0.0.1:7890"
	rec = performJSONRequest(engine, http.MethodPost, "/api/v1/notifications",
		notifyProxyRequestBody(t, fields, config), headers, "")
	if rec.Code != http.StatusCreated {
		t.Fatalf("合法代理地址应当创建成功，实际 %d: %s", rec.Code, rec.Body.String())
	}
}

// TestUpdateNotificationChannelRejectsMalformedProxyWithoutType：只带 config、不带 type 的 PUT
// （老调用就是这样）按库里的类型校验；改成非法值 400 且原 config 不变，改成合法值 200。
func TestUpdateNotificationChannelRejectsMalformedProxyWithoutType(t *testing.T) {
	testutil.SetupTestEnv(t)

	channel := mustCreateNotifyChannelRow(t, "企业微信应用", "wecom_app",
		`{"agent_id":"1000001","corp_id":"ww-demo","secret":"secret-demo"}`)
	engine := newProtectedRouter()
	headers := mustNotificationAdminHeaders(t, "notify-proxy-update-admin")
	path := "/api/v1/notifications/" + jsonNumber(channel.ID)
	config := map[string]string{
		"corp_id":  "ww-demo",
		"secret":   "secret-demo",
		"agent_id": "1000001",
		"proxy":    "127.0.0.1:7890",
	}

	rec := performJSONRequest(engine, http.MethodPut, path,
		notifyProxyRequestBody(t, map[string]interface{}{"name": "企业微信应用"}, config), headers, "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("非法代理地址应当 400，实际 %d: %s", rec.Code, rec.Body.String())
	}
	if body := rec.Body.String(); !strings.Contains(body, "(proxy)") {
		t.Errorf("错误信息应当点名 proxy 字段，实际: %s", body)
	}
	if reloaded := mustReloadNotifyChannel(t, channel.ID); reloaded.Config != channel.Config {
		t.Fatalf("被拒绝的请求不应改动已有 config，实际变成了 %s", reloaded.Config)
	}

	config["proxy"] = "socks5://user:pass@127.0.0.1:1080"
	rec = performJSONRequest(engine, http.MethodPut, path,
		notifyProxyRequestBody(t, map[string]interface{}{"name": "企业微信应用"}, config), headers, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("合法代理地址应当保存成功，实际 %d: %s", rec.Code, rec.Body.String())
	}
	if got := mustDecodeStoredNotifyConfig(t, mustReloadNotifyChannel(t, channel.ID))["proxy"]; got != config["proxy"] {
		t.Errorf("合法代理地址应当原样落库，实际 %q", got)
	}
}

// TestUpdateNotificationChannelValidatesProxyAgainstEffectiveType 断言 Update 按「保存之后的类型」校验：
// 请求带了非空字符串 type 就用它，否则（不带、或不是字符串）用库里的类型。
func TestUpdateNotificationChannelValidatesProxyAgainstEffectiveType(t *testing.T) {
	testutil.SetupTestEnv(t)

	engine := newProtectedRouter()
	headers := mustNotificationAdminHeaders(t, "notify-proxy-type-admin")

	t.Run("type 改成 telegram 同时带非法 proxy", func(t *testing.T) {
		channel := mustCreateNotifyChannelRow(t, "改类型-1", "webhook", `{"url":"https://example.com/webhook"}`)
		rec := performJSONRequest(engine, http.MethodPut, "/api/v1/notifications/"+jsonNumber(channel.ID),
			notifyProxyRequestBody(t,
				map[string]interface{}{"name": "改类型-1", "type": "telegram"},
				map[string]string{"token": "t", "chat_id": "c", "proxy": "127.0.0.1:7890"}),
			headers, "")
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("按新类型 telegram 校验，非法代理地址应当 400，实际 %d: %s", rec.Code, rec.Body.String())
		}
		reloaded := mustReloadNotifyChannel(t, channel.ID)
		if reloaded.Type != "webhook" || reloaded.Config != channel.Config {
			t.Fatalf("被拒绝的请求不应改动类型与 config，实际 type=%q config=%s", reloaded.Type, reloaded.Config)
		}
	})

	t.Run("旧 config 里原本就带着垃圾 proxy，切到 telegram 也算新值", func(t *testing.T) {
		// webhook 不声明 proxy，这个值从来没被校验过；切到 telegram 后它第一次生效，不能算「存量」。
		channel := mustCreateNotifyChannelRow(t, "改类型-2", "webhook",
			`{"proxy":"127.0.0.1:7890","url":"https://example.com/webhook"}`)
		rec := performJSONRequest(engine, http.MethodPut, "/api/v1/notifications/"+jsonNumber(channel.ID),
			notifyProxyRequestBody(t,
				map[string]interface{}{"name": "改类型-2", "type": "telegram"},
				map[string]string{"token": "t", "chat_id": "c", "proxy": "127.0.0.1:7890", "url": "https://example.com/webhook"}),
			headers, "")
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("切换类型后旧值应按新值校验，实际 %d: %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("type 不是字符串时按库里的类型校验", func(t *testing.T) {
		channel := mustCreateNotifyChannelRow(t, "改类型-3", "wecom_app",
			`{"agent_id":"1000001","corp_id":"ww-demo","secret":"secret-demo"}`)
		rec := performJSONRequest(engine, http.MethodPut, "/api/v1/notifications/"+jsonNumber(channel.ID),
			notifyProxyRequestBody(t,
				map[string]interface{}{"name": "改类型-3", "type": 123},
				map[string]string{"corp_id": "ww-demo", "secret": "secret-demo", "agent_id": "1000001", "proxy": "127.0.0.1:7890"}),
			headers, "")
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("type 不是字符串时应按库里的 wecom_app 校验，实际 %d: %s", rec.Code, rec.Body.String())
		}
		if reloaded := mustReloadNotifyChannel(t, channel.ID); reloaded.Type != "wecom_app" {
			t.Fatalf("被拒绝的请求不应改动类型，实际 %q", reloaded.Type)
		}
	})
}

// TestUpdateNotificationChannelKeepsUnchangedLegacyProxy 是「只拦新值」的核心回归：
// 本校验上线前就存进库的非法代理地址，原样回传时必须能保存；改成另一个非法值才拦。
func TestUpdateNotificationChannelKeepsUnchangedLegacyProxy(t *testing.T) {
	testutil.SetupTestEnv(t)

	// 模拟升级前的 telegram 渠道：proxy 漏写了 scheme（当时发送链路静默直连，用户未必察觉）。
	channel := mustCreateNotifyChannelRow(t, "TG 存量", "telegram",
		`{"chat_id":"c","proxy":"127.0.0.1:7890","token":"t"}`)
	engine := newProtectedRouter()
	headers := mustNotificationAdminHeaders(t, "notify-proxy-legacy-admin")
	path := "/api/v1/notifications/" + jsonNumber(channel.ID)

	// Web 与 APP 保存时都整份回传 config 并带上 type；这里只改了 chat_id。
	rec := performJSONRequest(engine, http.MethodPut, path,
		notifyProxyRequestBody(t,
			map[string]interface{}{"name": "TG 存量", "type": "telegram"},
			map[string]string{"token": "t", "chat_id": "c-new", "proxy": "127.0.0.1:7890"}),
		headers, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("未改动的存量代理地址不应挡住别的字段保存，实际 %d: %s", rec.Code, rec.Body.String())
	}
	cfg := mustDecodeStoredNotifyConfig(t, mustReloadNotifyChannel(t, channel.ID))
	if cfg["chat_id"] != "c-new" || cfg["proxy"] != "127.0.0.1:7890" {
		t.Fatalf("应当保存 chat_id 且原样保留存量 proxy，实际 %#v", cfg)
	}

	// 不带 type 的老调用同样放行。
	rec = performJSONRequest(engine, http.MethodPut, path,
		notifyProxyRequestBody(t,
			map[string]interface{}{"name": "TG 存量"},
			map[string]string{"token": "t", "chat_id": "c-newer", "proxy": "127.0.0.1:7890"}),
		headers, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("不带 type 时未改动的存量代理地址同样应放行，实际 %d: %s", rec.Code, rec.Body.String())
	}

	// 改成另一个非法值就是新值：拦下，库里保持上一次成功保存的结果。
	before := mustReloadNotifyChannel(t, channel.ID)
	rec = performJSONRequest(engine, http.MethodPut, path,
		notifyProxyRequestBody(t,
			map[string]interface{}{"name": "TG 存量", "type": "telegram"},
			map[string]string{"token": "t", "chat_id": "c-newer", "proxy": "localhost:7890"}),
		headers, "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("改成另一个非法代理地址应当 400，实际 %d: %s", rec.Code, rec.Body.String())
	}
	if after := mustReloadNotifyChannel(t, channel.ID); after.Config != before.Config {
		t.Fatalf("被拒绝的请求不应改动已有 config，实际从 %s 变成了 %s", before.Config, after.Config)
	}
}
