package handler_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"daidai-panel/database"
	"daidai-panel/model"
	"daidai-panel/service"
	"daidai-panel/testutil"
)

func TestConfigListIncludesRegistryMetadata(t *testing.T) {
	testutil.SetupTestEnv(t)

	admin := testutil.MustCreateUser(t, "config-admin", "admin")
	token := testutil.MustCreateAccessToken(t, admin.Username, admin.Role)
	database.DB.Where("`key` = ?", "proxy_url").Delete(&model.SystemConfig{})

	engine := newProtectedRouter()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/configs", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body=%s", rec.Code, rec.Body.String())
	}

	payload := decodeJSONMap(t, rec)
	data, ok := payload["data"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected data map, got %T", payload["data"])
	}

	autoInstall, ok := data["auto_install_deps"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected auto_install_deps entry, got %T", data["auto_install_deps"])
	}
	if got, _ := autoInstall["value"].(string); got != "true" {
		t.Fatalf("expected auto_install_deps default true, got %q", got)
	}
	if got, _ := autoInstall["value_type"].(string); got != string(model.SystemConfigTypeBool) {
		t.Fatalf("expected auto_install_deps value_type bool, got %q", got)
	}
	if got, _ := autoInstall["registered"].(bool); !got {
		t.Fatalf("expected auto_install_deps to be marked registered")
	}

	proxyCfg, ok := data["proxy_url"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected proxy_url entry, got %T", data["proxy_url"])
	}
	if got, _ := proxyCfg["registered"].(bool); !got {
		t.Fatalf("expected proxy_url to be marked registered")
	}
	if got, _ := proxyCfg["group"].(string); got != "network" {
		t.Fatalf("expected proxy_url group network, got %q", got)
	}

	updateMirrorCfg, ok := data["update_image_mirror"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected update_image_mirror entry, got %T", data["update_image_mirror"])
	}
	if got, _ := updateMirrorCfg["registered"].(bool); !got {
		t.Fatalf("expected update_image_mirror to be marked registered")
	}
	if got, _ := updateMirrorCfg["group"].(string); got != "network" {
		t.Fatalf("expected update_image_mirror group network, got %q", got)
	}
}

// TestConfigListExposesRenderSchema 锁住「按 schema 动态渲染设置页」依赖的那几个新增字段。
// 这些字段是纯新增的：老客户端只读原有键，不会受影响；
// 但一旦被误删或改名，Web / APP 的通用设置页会直接退化成没有标题、没有顺序。
func TestConfigListExposesRenderSchema(t *testing.T) {
	testutil.SetupTestEnv(t)

	admin := testutil.MustCreateUser(t, "config-schema-admin", "admin")
	token := testutil.MustCreateAccessToken(t, admin.Username, admin.Role)

	engine := newProtectedRouter()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/configs", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body=%s", rec.Code, rec.Body.String())
	}

	payload := decodeJSONMap(t, rec)
	data, ok := payload["data"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected data map, got %T", payload["data"])
	}

	// 每一项已注册配置都必须带齐通用渲染需要的元信息，
	// 且 order 要能还原出注册表里的声明顺序（本接口返回 map，自身不保序）。
	seenOrders := make(map[int]string)
	for _, def := range model.SystemConfigDefinitions() {
		item, ok := data[def.Key].(map[string]interface{})
		if !ok {
			t.Fatalf("expected entry for %s, got %T", def.Key, data[def.Key])
		}

		if got, _ := item["label"].(string); got != def.Label || got == "" {
			t.Fatalf("expected %s label %q, got %q", def.Key, def.Label, got)
		}
		if got, _ := item["group_label"].(string); got != def.GroupLabel || got == "" {
			t.Fatalf("expected %s group_label %q, got %q", def.Key, def.GroupLabel, got)
		}

		rawOrder, exists := item["order"].(float64)
		if !exists {
			t.Fatalf("expected %s to carry order, got %T", def.Key, item["order"])
		}
		order := int(rawOrder)
		if order != def.Order {
			t.Fatalf("expected %s order %d, got %d", def.Key, def.Order, order)
		}
		if dup, clash := seenOrders[order]; clash {
			t.Fatalf("order %d is shared by %s and %s", order, dup, def.Key)
		}
		seenOrders[order] = def.Key

		if got, hasSecret := item["secret"].(bool); !hasSecret || got != def.Secret {
			t.Fatalf("expected %s secret %v, got %v", def.Key, def.Secret, item["secret"])
		}

		// min/max 只对整数项下发，其余项不应出现这两个键。
		if def.ValueType == model.SystemConfigTypeInt {
			minValue, hasMin := item["min"].(float64)
			maxValue, hasMax := item["max"].(float64)
			if !hasMin || !hasMax {
				t.Fatalf("expected %s to carry min/max, got min=%T max=%T", def.Key, item["min"], item["max"])
			}
			if int(minValue) != *def.Min || int(maxValue) != *def.Max {
				t.Fatalf("expected %s range %d-%d, got %d-%d", def.Key, *def.Min, *def.Max, int(minValue), int(maxValue))
			}
		} else if _, hasMin := item["min"]; hasMin {
			t.Fatalf("expected non-int config %s to omit min", def.Key)
		}

		// options 及其元素的 value / label 键名同样要钉死。
		// 客户端（Web 的下拉、APP 的选择器）读的就是这三个字符串，改 json tag
		// 不会让任何 Go 侧断言变红，但枚举项会静默退化成一个自由输入的文本框。
		if len(def.Options) == 0 {
			if _, hasOptions := item["options"]; hasOptions {
				t.Fatalf("expected %s without registry options to omit options", def.Key)
			}
			continue
		}
		rawOptions, ok := item["options"].([]interface{})
		if !ok || len(rawOptions) != len(def.Options) {
			t.Fatalf("expected %s to carry %d options, got %#v", def.Key, len(def.Options), item["options"])
		}
		for i, want := range def.Options {
			option, ok := rawOptions[i].(map[string]interface{})
			if !ok {
				t.Fatalf("expected %s option %d to be an object, got %#v", def.Key, i, rawOptions[i])
			}
			if got, _ := option["value"].(string); got != want.Value {
				t.Fatalf("expected %s option %d value %q, got %#v", def.Key, i, want.Value, option["value"])
			}
			if got, _ := option["label"].(string); got != want.Label {
				t.Fatalf("expected %s option %d label %q, got %#v", def.Key, i, want.Label, option["label"])
			}
		}
	}

	// 凭据类配置必须带 secret 标记，客户端据此用密码框渲染。
	for _, key := range []string{"captcha_key", "backup_schedule_password"} {
		item, ok := data[key].(map[string]interface{})
		if !ok {
			t.Fatalf("expected entry for %s, got %T", key, data[key])
		}
		if got, _ := item["secret"].(bool); !got {
			t.Fatalf("expected %s to be marked secret", key)
		}
	}

	// 兼容性兜底：老客户端在读的键必须原样保留，类型也不能变。
	concurrency, ok := data["max_concurrent_tasks"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected max_concurrent_tasks entry, got %T", data["max_concurrent_tasks"])
	}
	for _, key := range []string{"value", "default_value", "description", "value_type", "group", "registered"} {
		if _, exists := concurrency[key]; !exists {
			t.Fatalf("expected legacy field %q to stay in the config item", key)
		}
	}
	if got, _ := concurrency["value_type"].(string); got != string(model.SystemConfigTypeInt) {
		t.Fatalf("expected max_concurrent_tasks value_type int, got %q", got)
	}
	if got, _ := concurrency["default_value"].(string); got != "5" {
		t.Fatalf("expected max_concurrent_tasks default_value 5, got %q", got)
	}
}

// TestConfigListReportsCompleteBackupScheduleSelectionDefault 锁死那次真实漂移的对外表现：
// 注册表的默认值曾少了 task_views，导致按 default_value 渲染的客户端把「任务视图」显示成未勾选，
// 从未保存过备份设置的实例也就一直在跑不含任务视图的定时备份。
func TestConfigListReportsCompleteBackupScheduleSelectionDefault(t *testing.T) {
	testutil.SetupTestEnv(t)

	admin := testutil.MustCreateUser(t, "backup-selection-admin", "admin")
	token := testutil.MustCreateAccessToken(t, admin.Username, admin.Role)
	// 清掉 InitDefaultConfigs 建出来的记录，模拟「从未保存过备份设置」的实例。
	database.DB.Where("`key` = ?", "backup_schedule_selection").Delete(&model.SystemConfig{})

	engine := newProtectedRouter()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/configs", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body=%s", rec.Code, rec.Body.String())
	}

	payload := decodeJSONMap(t, rec)
	data, ok := payload["data"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected data map, got %T", payload["data"])
	}
	item, ok := data["backup_schedule_selection"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected backup_schedule_selection entry, got %T", data["backup_schedule_selection"])
	}

	const expected = "configs,tasks,subscriptions,env_vars,logs,scripts,dependencies,task_views"
	if got, _ := item["default_value"].(string); got != expected {
		t.Fatalf("expected default_value %q, got %q", expected, got)
	}
	if got, _ := item["value"].(string); got != expected {
		t.Fatalf("expected fallback value %q, got %q", expected, got)
	}
	if got := model.GetRegisteredConfig("backup_schedule_selection"); got != expected {
		t.Fatalf("expected GetRegisteredConfig fallback %q, got %q", expected, got)
	}
}

// TestConfigListExposesPanelShapeStyle 钉死界面圆角开关的对外契约。
//
// 这一项是纯客户端消费的：网页端拿它决定 --dd-radius-* 三档令牌取 0 还是取非零像素。
// 键名、默认值、两个枚举值任何一个改了，Web 都会静默回落到「按未知值处理」——
// 表现为用户切了没反应，而不是报错，所以必须有一条具名断言拦住。
func TestConfigListExposesPanelShapeStyle(t *testing.T) {
	testutil.SetupTestEnv(t)

	admin := testutil.MustCreateUser(t, "shape-style-admin", "admin")
	token := testutil.MustCreateAccessToken(t, admin.Username, admin.Role)
	// 清掉 InitDefaultConfigs 建出来的记录，验证「库里没这行」时也能回落到注册表默认值。
	database.DB.Where("`key` = ?", "panel_shape_style").Delete(&model.SystemConfig{})

	engine := newProtectedRouter()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/configs", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body=%s", rec.Code, rec.Body.String())
	}

	payload := decodeJSONMap(t, rec)
	data, ok := payload["data"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected data map, got %T", payload["data"])
	}

	item, ok := data["panel_shape_style"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected panel_shape_style entry, got %T", data["panel_shape_style"])
	}
	if got, _ := item["registered"].(bool); !got {
		t.Fatalf("expected panel_shape_style to be marked registered")
	}
	if got, _ := item["value_type"].(string); got != string(model.SystemConfigTypeEnum) {
		t.Fatalf("expected panel_shape_style value_type enum, got %q", got)
	}
	if got, _ := item["group"].(string); got != "branding" {
		t.Fatalf("expected panel_shape_style group branding, got %q", got)
	}
	// v4.0 起默认改为 theme（跟随主题），见 design §1.3 / §1.4。
	if got, _ := item["default_value"].(string); got != "theme" {
		t.Fatalf("expected panel_shape_style default_value theme, got %q", got)
	}
	// 库里没记录时 value 必须回落成默认值，否则 Web 首帧会拿到空串。
	if got, _ := item["value"].(string); got != "theme" {
		t.Fatalf("expected panel_shape_style fallback value theme, got %q", got)
	}

	// 选项必须恰好是 theme / square / rounded 三项（v4.0 新增 theme）：多一项 Web 渲染得出来但没有对应刻度，
	// 少一项则用户切不回去。
	rawOptions, ok := item["options"].([]interface{})
	if !ok || len(rawOptions) != 3 {
		t.Fatalf("expected panel_shape_style to carry 3 options, got %#v", item["options"])
	}
	wantOptions := []struct{ value, label string }{
		{"theme", "跟随主题"},
		{"square", "直角"},
		{"rounded", "圆角"},
	}
	for i, want := range wantOptions {
		option, ok := rawOptions[i].(map[string]interface{})
		if !ok {
			t.Fatalf("expected panel_shape_style option %d to be an object, got %#v", i, rawOptions[i])
		}
		if got, _ := option["value"].(string); got != want.value {
			t.Fatalf("expected panel_shape_style option %d value %q, got %#v", i, want.value, option["value"])
		}
		if got, _ := option["label"].(string); got != want.label {
			t.Fatalf("expected panel_shape_style option %d label %q, got %#v", i, want.label, option["label"])
		}
	}

	// 非法值必须被注册表 normalize 拒掉，避免脏值流到 Web 变成「不方不圆」。
	if _, err := model.NormalizeSystemConfigValue("panel_shape_style", "pill"); err == nil {
		t.Fatalf("expected panel_shape_style to reject unknown value")
	}
	if got, err := model.NormalizeSystemConfigValue("panel_shape_style", " ROUNDED "); err != nil || got != "rounded" {
		t.Fatalf("expected panel_shape_style to normalize \" ROUNDED \" to rounded, got %q err=%v", got, err)
	}
}

func TestConfigBatchSetUsesRegistryValidation(t *testing.T) {
	testutil.SetupTestEnv(t)

	admin := testutil.MustCreateUser(t, "config-admin", "admin")
	token := testutil.MustCreateAccessToken(t, admin.Username, admin.Role)
	engine := newProtectedRouter()

	body := `{"configs":{"auto_install_deps":"0","captcha_fail_mode":" strict ","update_image_mirror":"https://docker.1ms.run/","binary_update_proxy":"gh-proxy.org"}}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/configs/batch", bytes.NewBufferString(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body=%s", rec.Code, rec.Body.String())
	}
	if got := model.GetRegisteredConfigBool("auto_install_deps"); got {
		t.Fatalf("expected auto_install_deps false after batch set")
	}
	if got := model.GetRegisteredConfig("captcha_fail_mode"); got != "strict" {
		t.Fatalf("expected captcha_fail_mode strict, got %q", got)
	}
	if got := model.GetRegisteredConfig("update_image_mirror"); got != "docker.1ms.run" {
		t.Fatalf("expected update_image_mirror docker.1ms.run, got %q", got)
	}
	if got := model.GetRegisteredConfig("binary_update_proxy"); got != "https://gh-proxy.org/" {
		t.Fatalf("expected binary_update_proxy https://gh-proxy.org/, got %q", got)
	}

	trustedProxyBody := `{"configs":{"trusted_proxy_cidrs":"127.0.0.1,203.0.113.0/24"}}`
	trustedProxyReq := httptest.NewRequest(http.MethodPut, "/api/v1/configs/batch", bytes.NewBufferString(trustedProxyBody))
	trustedProxyReq.Header.Set("Authorization", "Bearer "+token)
	trustedProxyReq.Header.Set("Content-Type", "application/json")
	trustedProxyRec := httptest.NewRecorder()
	engine.ServeHTTP(trustedProxyRec, trustedProxyReq)

	if trustedProxyRec.Code != http.StatusOK {
		t.Fatalf("expected trusted proxy request to return 200, got %d, body=%s", trustedProxyRec.Code, trustedProxyRec.Body.String())
	}
	if got := model.GetRegisteredConfig("trusted_proxy_cidrs"); got != "127.0.0.1/32\n203.0.113.0/24" {
		t.Fatalf("expected canonical trusted_proxy_cidrs, got %q", got)
	}

	invalidBody := `{"configs":{"default_cron_rule":"invalid cron"}}`
	invalidReq := httptest.NewRequest(http.MethodPut, "/api/v1/configs/batch", bytes.NewBufferString(invalidBody))
	invalidReq.Header.Set("Authorization", "Bearer "+token)
	invalidReq.Header.Set("Content-Type", "application/json")
	invalidRec := httptest.NewRecorder()
	engine.ServeHTTP(invalidRec, invalidReq)

	if invalidRec.Code != http.StatusBadRequest {
		t.Fatalf("expected invalid config request to return 400, got %d", invalidRec.Code)
	}

	var invalidPayload map[string]interface{}
	if err := json.Unmarshal(invalidRec.Body.Bytes(), &invalidPayload); err != nil {
		t.Fatalf("decode invalid response: %v", err)
	}
	if got, _ := invalidPayload["error"].(string); got == "" {
		t.Fatalf("expected validation error message, got %v", invalidPayload)
	}

	invalidMirrorBody := `{"configs":{"update_image_mirror":"https://docker.1ms.run/path"}}`
	invalidMirrorReq := httptest.NewRequest(http.MethodPut, "/api/v1/configs/batch", bytes.NewBufferString(invalidMirrorBody))
	invalidMirrorReq.Header.Set("Authorization", "Bearer "+token)
	invalidMirrorReq.Header.Set("Content-Type", "application/json")
	invalidMirrorRec := httptest.NewRecorder()
	engine.ServeHTTP(invalidMirrorRec, invalidMirrorReq)

	if invalidMirrorRec.Code != http.StatusBadRequest {
		t.Fatalf("expected invalid update_image_mirror request to return 400, got %d", invalidMirrorRec.Code)
	}
}

func TestConfigBatchSetTimezoneAppliesImmediately(t *testing.T) {
	oldLocal := time.Local
	oldTZ, hadTZ := os.LookupEnv("TZ")
	oldName := service.CurrentPanelTimezone()
	t.Cleanup(func() {
		_ = service.ApplyPanelTimezone(oldName)
		time.Local = oldLocal
		if hadTZ {
			_ = os.Setenv("TZ", oldTZ)
		} else {
			_ = os.Unsetenv("TZ")
		}
	})

	testutil.SetupTestEnv(t)

	admin := testutil.MustCreateUser(t, "timezone-admin", "admin")
	token := testutil.MustCreateAccessToken(t, admin.Username, admin.Role)
	engine := newProtectedRouter()

	body := `{"configs":{"timezone":"UTC"}}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/configs/batch", bytes.NewBufferString(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body=%s", rec.Code, rec.Body.String())
	}
	if got := service.CurrentPanelTimezone(); got != "UTC" {
		t.Fatalf("expected runtime timezone UTC after save, got %q", got)
	}
	if got := os.Getenv("TZ"); got != "UTC" {
		t.Fatalf("expected process TZ=UTC after save, got %q", got)
	}
	if got := time.Local.String(); got != "UTC" {
		t.Fatalf("expected time.Local UTC after save, got %q", got)
	}

	invalidBody := `{"configs":{"timezone":"Bad/Zone"}}`
	invalidReq := httptest.NewRequest(http.MethodPut, "/api/v1/configs/batch", bytes.NewBufferString(invalidBody))
	invalidReq.Header.Set("Authorization", "Bearer "+token)
	invalidReq.Header.Set("Content-Type", "application/json")
	invalidRec := httptest.NewRecorder()
	engine.ServeHTTP(invalidRec, invalidReq)

	if invalidRec.Code != http.StatusBadRequest {
		t.Fatalf("expected invalid timezone request to return 400, got %d", invalidRec.Code)
	}
}
