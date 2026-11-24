// Command gen-demo-fixtures 把服务端的两张静态注册表导出成在线演示 Demo 用的 JSON fixture。
//
// 跑法（cwd 在 server/ 或仓库根目录都可以）：
//
//	$env:PATH = "D:\软件目录\Go\bin;" + $env:PATH
//	cd server
//	go run ./cmd/gen-demo-fixtures
//
// 产物（路径相对仓库根）：
//
//	web/src/demo/fixtures/notification-types.json   <- model.NotifyChannelDefinitions()
//	web/src/demo/fixtures/configs.json              <- model.SystemConfigDefinitions()
//
// 为什么必须生成、不能手写：
// .trellis/spec/frontend/index.md 明确规定这两类知识的唯一真源在服务端
// （server/model/notify_channel_registry.go 与 server/model/system_config_registry.go）。
// 通知渠道字段历史上在仓库里存在过四份副本，并且已经漂移过一次
// （web/src/views/api-docs/apiData.ts 的 wecom_app 漏了 mpnews）。
// 在 web/src/demo/fixtures/ 里手写这两个文件 = 制造第五份副本，直接违反规范。
// 用生成器导出则是「后端演进时 Demo 自动跟上」，结构上不可能再分叉。
//
// ⚠️ 这是一个独立的 main 包，绝不能被 server 主二进制（server/main.go）或任何非 main 包引用。
// 它只在开发者本地或 CI 的生成步骤里跑，不参与面板运行时。
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"daidai-panel/model"
)

const (
	notificationTypesFileName = "notification-types.json"
	configsFileName           = "configs.json"
)

func main() {
	outDir := flag.String("out", "", "fixture 输出目录；留空时自动定位到 <仓库根>/web/src/demo/fixtures")
	flag.Parse()

	if err := run(*outDir); err != nil {
		fmt.Fprintf(os.Stderr, "gen-demo-fixtures 失败：%v\n", err)
		os.Exit(1)
	}
}

func run(outDir string) error {
	dir, err := resolveOutDir(outDir)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("创建输出目录 %s: %w", dir, err)
	}

	// ---- GET /api/notifications/types ---------------------------------------
	// 响应体见 server/handler/notification.go:312：response.Success(c, gin.H{"data": ...})，
	// 而 response.Success 就是 c.JSON(200, data)，没有再包一层信封。
	channels := model.NotifyChannelDefinitions()
	channelsPath := filepath.Join(dir, notificationTypesFileName)
	if err := writeJSONFile(channelsPath, map[string]any{"data": channels}); err != nil {
		return err
	}

	// ---- GET /api/configs ---------------------------------------------------
	configItems := buildFreshInstallConfigItems()
	if err := assertConfigItemsCoverDefinition(configItems); err != nil {
		return err
	}
	configsPath := filepath.Join(dir, configsFileName)
	if err := writeJSONFile(configsPath, map[string]any{"data": configItems}); err != nil {
		return err
	}

	fmt.Printf("已生成 %s（%d 个渠道 / %d 个字段槽）\n", channelsPath, len(channels), countChannelFields(channels))
	fmt.Printf("已生成 %s（%d 项配置）\n", configsPath, len(configItems))
	return nil
}

// buildFreshInstallConfigItems 复刻 GET /api/configs 在「全新安装、system_configs 表里一行都没有」
// 时的响应体。
//
// 逐字段对齐 server/handler/config.go 的 buildConfigResponseItem（:47-96）在 cfg == nil 分支下的行为：
//
//	:48-51  registered 先置 false、updated_at 置 nil
//	:57-60  cfg == nil ⇒ value 与 description 先写空串
//	:63     def != nil ⇒ registered 改成 true
//	:64-67  default_value / value_type / group，并用 def.Description 覆盖上面那个空串
//	:73-75  label / group_label / order
//	:80     secret（无论真假都写，不是 omitempty）
//	        web_only 仅在注册表标了 true 时才有（网页请求口径；APP 请求时 List 整项跳过）
//	:81-86  min / max 仅在注册表给了区间时才有
//	:87-89  cfg == nil ⇒ value 回落 def.DefaultValue —— 这就是「全新安装态」，Demo 要的正是它
//	:90-92  options 仅在注册表给了枚举项时才有
//
// handler 里 List(:120-123) 还会把「库里有行但注册表没有」的历史遗留键补在后面（def == nil、
// registered:false）。全新安装态下这类键不存在，所以这里没有对应分支。
//
// 返回 map 而不是有序结构是刻意的：真实接口返回的就是 map（key -> item），客户端按 order 字段排序渲染
// （见 web/src/api/system.ts:174 与 .trellis/spec/frontend/index.md）。
// encoding/json 序列化 map 时会按键名字典序输出，所以产物依然是稳定的，重复生成不会产生噪音 diff。
func buildFreshInstallConfigItems() map[string]any {
	defs := model.SystemConfigDefinitions()
	items := make(map[string]any, len(defs))

	for _, def := range defs {
		item := map[string]any{
			"registered":    true,
			"updated_at":    nil,
			"value":         def.DefaultValue,
			"description":   def.Description,
			"default_value": def.DefaultValue,
			"value_type":    def.ValueType,
			"group":         def.Group,
			"group_label":   def.GroupLabel,
			"label":         def.Label,
			"order":         def.Order,
			"secret":        def.Secret,
		}
		// web_only 只在为 true 时才写（buildConfigResponseItem 同样只在 true 时下发）。
		// Demo 是网页端，按网页请求的口径复刻：web_only 项照常出现，APP 请求时整项不下发的分支这里用不上。
		if def.WebOnly {
			item["web_only"] = true
		}
		if def.Min != nil {
			item["min"] = *def.Min
		}
		if def.Max != nil {
			item["max"] = *def.Max
		}
		if len(def.Options) > 0 {
			item["options"] = def.Options
		}
		items[def.Key] = item
	}

	return items
}

// configItemKeyForDefinitionField 声明 SystemConfigDefinition 的每个 json 字段
// 在 /api/configs 单项响应里落到哪个键。空串表示「有意不进单项」。
//
// 它存在的唯一目的是给下面那条断言当依据：注册表加字段时，生成器要么跟着改，要么当场报错，
// 而不是默默产出一份缺字段的 fixture。
var configItemKeyForDefinitionField = map[string]string{
	// key 是响应 map 的键本身，handler 不会在单项里重复一遍
	"key":           "",
	"label":         "label",
	"default_value": "default_value",
	"description":   "description",
	"value_type":    "value_type",
	"group":         "group",
	"group_label":   "group_label",
	"order":         "order",
	"secret":        "secret",
	"min":           "min",
	"max":           "max",
	"options":       "options",
	"web_only":      "web_only",
}

// assertConfigItemsCoverDefinition 拦截「注册表演进了、生成器没跟上」这一类漂移。
//
// 检查两件事：
//  1. SystemConfigDefinition 上每个 json 字段都在 configItemKeyForDefinitionField 里有交代；
//  2. 有交代且应当下发的字段，至少在生成出来的某一项里真的出现了。
//
// min / max / options 是条件字段（只有 int 类型和枚举类型才有），所以量词是「至少一项」而不是「每一项」。
func assertConfigItemsCoverDefinition(items map[string]any) error {
	presentKeys := make(map[string]struct{})
	for key, raw := range items {
		item, ok := raw.(map[string]any)
		if !ok {
			return fmt.Errorf("配置项 %s 的 fixture 不是对象", key)
		}
		for itemKey := range item {
			presentKeys[itemKey] = struct{}{}
		}
	}

	var problems []string
	defType := reflect.TypeOf(model.SystemConfigDefinition{})
	for i := 0; i < defType.NumField(); i++ {
		name := jsonFieldName(defType.Field(i))
		if name == "" || name == "-" {
			continue
		}

		itemKey, declared := configItemKeyForDefinitionField[name]
		if !declared {
			problems = append(problems, fmt.Sprintf(
				"model.SystemConfigDefinition 多了 json 字段 %q，但生成器不知道它在 /api/configs 单项里叫什么。"+
					"请先看 server/handler/config.go 的 buildConfigResponseItem 有没有下发它，再补 configItemKeyForDefinitionField", name))
			continue
		}
		if itemKey == "" {
			continue
		}
		if _, ok := presentKeys[itemKey]; !ok {
			problems = append(problems, fmt.Sprintf(
				"json 字段 %q 应当以 %q 出现在响应体里，但生成出来的 %d 项配置中一项都没有它", name, itemKey, len(items)))
		}
	}

	if len(problems) > 0 {
		sort.Strings(problems)
		return errors.New("fixture 与 model.SystemConfigDefinition 已经漂移：\n  " + strings.Join(problems, "\n  "))
	}
	return nil
}

func jsonFieldName(field reflect.StructField) string {
	tag, ok := field.Tag.Lookup("json")
	if !ok {
		return field.Name
	}
	name, _, _ := strings.Cut(tag, ",")
	if name == "" {
		return field.Name
	}
	return name
}

func countChannelFields(channels []model.NotifyChannelDefinition) int {
	total := 0
	for _, channel := range channels {
		total += len(channel.Fields)
	}
	return total
}

// writeJSONFile 把 payload 写成带缩进的 JSON。
//
// 用 json.MarshalIndent（而不是关掉 HTML 转义的 Encoder）是刻意的：gin 的 c.JSON 内部走的就是
// json.Marshal，默认会把 < > & 转成 \u003c 这类形式。保持同一套转义规则，产物才和真实响应同构。
//
// 结尾补一个换行，符合文本文件惯例，也避免 git diff 出现 "\ No newline at end of file"。
func writeJSONFile(path string, payload any) error {
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化 %s: %w", filepath.Base(path), err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("写入 %s: %w", path, err)
	}
	return nil
}

// resolveOutDir 决定 fixture 落到哪。
//
// 不直接用 "../web/src/demo/fixtures" 这种相对路径：那样只有在 cwd 恰好是 server/ 时才对，
// 从仓库根跑 `go run ./server/cmd/gen-demo-fixtures` 就会把文件写到仓库外面去。
// 改成从 cwd 向上找「同时有 server/go.mod 和 web/package.json」的那一层，两种跑法都成立。
func resolveOutDir(override string) (string, error) {
	if strings.TrimSpace(override) != "" {
		return override, nil
	}

	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("读取当前目录: %w", err)
	}

	dir := cwd
	for {
		if isRepoRoot(dir) {
			return filepath.Join(dir, "web", "src", "demo", "fixtures"), nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("从 %s 一路向上都没找到仓库根（需要同时存在 server/go.mod 与 web/package.json），请用 -out 显式指定输出目录", cwd)
		}
		dir = parent
	}
}

func isRepoRoot(dir string) bool {
	for _, marker := range []string{
		filepath.Join(dir, "server", "go.mod"),
		filepath.Join(dir, "web", "package.json"),
	} {
		if _, err := os.Stat(marker); err != nil {
			return false
		}
	}
	return true
}
