package model_test

import (
	"strings"
	"testing"

	"daidai-panel/model"
)

// v4.0 主题工坊的 7 个全站默认键（design §1.3）：都在 branding 分组、说明末尾写「仅网页端生效，APP 不受影响」，
// 默认值与 design 一致。键名是前后端契约，改名会让网页端静默拿不到值。
func TestPanelAppearanceConfigsAreRegistered(t *testing.T) {
	wantDefaults := map[string]string{
		"panel_theme_preset":      "aurora",
		"panel_accent_color":      "",
		"panel_background_effect": "theme",
		"panel_background_image":  "",
		"panel_glass_blur":        "",
		"panel_density":           "cozy",
		"panel_custom_css":        "",
		"panel_shape_style":       "theme",
	}
	for key, want := range wantDefaults {
		def, ok := model.GetSystemConfigDefinition(key)
		if !ok {
			t.Fatalf("%s 应当是已注册配置", key)
		}
		if def.DefaultValue != want {
			t.Errorf("%s 默认值应为 %q，实际 %q", key, want, def.DefaultValue)
		}
		if def.Group != "branding" {
			t.Errorf("%s 应归入 branding 分组，实际 %s", key, def.Group)
		}
		if !strings.HasSuffix(def.Description, "仅网页端生效，APP 不受影响。") {
			t.Errorf("%s 的说明末尾应写「仅网页端生效，APP 不受影响」，实际 %q", key, def.Description)
		}
	}
}

func TestPanelAppearanceEnumOptions(t *testing.T) {
	cases := map[string][]string{
		"panel_theme_preset":      {"classic", "aurora", "mist", "terminal", "paper", "daidai", "clean"},
		"panel_background_effect": {"theme", "plain", "aurora", "grid", "image"},
		"panel_density":           {"compact", "cozy", "roomy"},
		"panel_shape_style":       {"theme", "square", "rounded"},
	}
	for key, values := range cases {
		def, _ := model.GetSystemConfigDefinition(key)
		if def.ValueType != model.SystemConfigTypeEnum || len(def.Options) != len(values) {
			t.Fatalf("%s 应为 %d 个选项的枚举，实际 type=%s options=%#v", key, len(values), def.ValueType, def.Options)
		}
		for i, value := range values {
			if def.Options[i].Value != value {
				t.Errorf("%s 第 %d 个选项应为 %q，实际 %q", key, i, value, def.Options[i].Value)
			}
		}
		if _, err := model.NormalizeSystemConfigValue(key, "not-a-valid-option"); err == nil {
			t.Errorf("%s 应拒绝未知取值", key)
		}
	}
}

func TestPanelAccentColorValidation(t *testing.T) {
	for _, value := range []string{"", "  "} {
		if got, err := model.NormalizeSystemConfigValue("panel_accent_color", value); err != nil || got != "" {
			t.Errorf("留空应表示跟随主题，输入 %q 得到 %q err=%v", value, got, err)
		}
	}
	if got, err := model.NormalizeSystemConfigValue("panel_accent_color", " #7C5CFF "); err != nil || got != "#7c5cff" {
		t.Errorf("#rrggbb 应被接受并转小写，得到 %q err=%v", got, err)
	}
	for _, value := range []string{"#fff", "7c5cff", "#7c5cfg", "rgb(1,2,3)", "red", "#7c5cff;"} {
		if _, err := model.NormalizeSystemConfigValue("panel_accent_color", value); err == nil {
			t.Errorf("%q 不是 #rrggbb，应被拒绝", value)
		}
	}
}

func TestPanelGlassBlurValidation(t *testing.T) {
	accept := map[string]string{"": "", "0": "0", "40": "40", " 12 ": "12"}
	for input, want := range accept {
		if got, err := model.NormalizeSystemConfigValue("panel_glass_blur", input); err != nil || got != want {
			t.Errorf("输入 %q 应得到 %q，实际 %q err=%v", input, want, got, err)
		}
	}
	for _, value := range []string{"-1", "41", "1.5", "abc", "12px"} {
		if _, err := model.NormalizeSystemConfigValue("panel_glass_blur", value); err == nil {
			t.Errorf("%q 应被拒绝（只接受空或 0~40 的整数）", value)
		}
	}
}

// 自定义 CSS 的每条拒绝规则各一个用例（design §1.6），外加允许的写法。
func TestPanelCustomCSSValidation(t *testing.T) {
	rejected := map[string]string{
		"@import":            `@import "https://evil.example/x.css";`,
		"@IMPORT 大小写":        `@IMPORT url(a.css);`,
		"url(http:)":         `body{background:url(http://evil.example/a.png)}`,
		"url(https:) 带引号与空白": `body{background:URL( 'https://evil.example/a.png' )}`,
		"url(//) 协议相对":       `body{background:url("//evil.example/a.png")}`,
		"expression(":        `div{width:Expression(alert(1))}`,
		"javascript:":        `a{background:url(JavaScript:alert(1))}`,
		"behavior:":          `div{BEHAVIOR: url(x.htc)}`,
		"</style":            `body{}</STYLE><script>alert(1)</script>`,
	}
	for name, css := range rejected {
		if _, err := model.NormalizeSystemConfigValue("panel_custom_css", css); err == nil {
			t.Errorf("[%s] 应被拒绝：%s", name, css)
		}
	}

	tooLong := strings.Repeat("a", model.PanelCustomCSSMaxBytes+1)
	if _, err := model.NormalizeSystemConfigValue("panel_custom_css", tooLong); err == nil {
		t.Error("超过 32768 字节应被拒绝")
	}
	atLimit := strings.Repeat("a", model.PanelCustomCSSMaxBytes)
	if _, err := model.NormalizeSystemConfigValue("panel_custom_css", atLimit); err != nil {
		t.Errorf("正好 32768 字节应被接受，err=%v", err)
	}

	allowed := ".card{border:1px solid #f0f;}\n.title{background:url(data:image/png;base64,iVBORw0KGgo=)}\r\n.x{background:url('/local.png')}"
	got, err := model.NormalizeSystemConfigValue("panel_custom_css", "  "+allowed+"\n")
	if err != nil {
		t.Fatalf("data: 地址与站内相对地址应被允许，err=%v", err)
	}
	if got != allowed {
		t.Fatalf("只应去掉首尾空白、正文（含换行）原样保存，得到 %q", got)
	}
}
