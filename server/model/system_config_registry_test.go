package model_test

import (
	"strconv"
	"testing"

	"daidai-panel/model"
)

// TestEveryRegisteredConfigDefaultMatchesNormalizedEmpty 锁死注册表里最容易悄悄分叉的一条不变量：
// 「声明的默认值」必须等于「实际生效的默认值」。
//
// 为什么会分叉：newValidatedStringConfig 把 DefaultValue 原样存进 definition，注册时不过 normalize。
// 于是同一项配置存在两份默认值 —— 一份写在注册表里，一份写在 normalize 函数的空值分支里。
// 只要有人改了其中一份，另一份不会报错、不会告警，就这么静默错开。
//
// 被这条分叉影响的路径（三条都吃注册表那份，而不是 normalize 那份）：
//   - GET /api/configs 报出去的 default_value —— 客户端按 schema 渲染时显示的默认值；
//   - InitDefaultConfigs() 首次建行时写进 system_configs 的初始值；
//   - GetRegisteredConfig() 在库里查不到记录时的回退值。
//
// 真实事故：backup_schedule_selection 的注册表默认值少了 task_views，
// 导致从未保存过备份设置的实例，定时备份永远不含任务视图，且 Web 上那个勾选框默认是没勾的。
func TestEveryRegisteredConfigDefaultMatchesNormalizedEmpty(t *testing.T) {
	for _, def := range model.SystemConfigDefinitions() {
		normalized, err := model.NormalizeSystemConfigValue(def.Key, "")
		if err != nil {
			t.Errorf("配置 %s: normalize(\"\") 不应返回错误，实际: %v", def.Key, err)
			continue
		}
		if normalized != def.DefaultValue {
			t.Errorf(
				"配置 %s: 声明的默认值与实际生效的默认值不一致\n  注册表 DefaultValue = %q\n  normalize(\"\")      = %q",
				def.Key, def.DefaultValue, normalized,
			)
		}
	}
}

// TestEveryRegisteredConfigDefaultIsCanonical 断言默认值本身就是一个合法且已归一化的值。
// 这条能抓住：枚举默认值不在 options 里、整数默认值越出 min/max、字符串默认值带多余空白等情况。
func TestEveryRegisteredConfigDefaultIsCanonical(t *testing.T) {
	for _, def := range model.SystemConfigDefinitions() {
		normalized, err := model.NormalizeSystemConfigValue(def.Key, def.DefaultValue)
		if err != nil {
			t.Errorf("配置 %s: 默认值 %q 无法通过自身校验: %v", def.Key, def.DefaultValue, err)
			continue
		}
		if normalized != def.DefaultValue {
			t.Errorf(
				"配置 %s: 默认值不是归一化形式\n  DefaultValue          = %q\n  normalize(DefaultValue) = %q",
				def.Key, def.DefaultValue, normalized,
			)
		}
	}
}

// TestBackupScheduleSelectionDefaultIncludesTaskViews 针对上面那次真实漂移留一条具名回归。
func TestBackupScheduleSelectionDefaultIncludesTaskViews(t *testing.T) {
	def, exists := model.GetSystemConfigDefinition("backup_schedule_selection")
	if !exists {
		t.Fatal("backup_schedule_selection 应当是已注册配置")
	}

	const expected = "configs,tasks,subscriptions,env_vars,logs,scripts,dependencies,task_views"
	if def.DefaultValue != expected {
		t.Fatalf("定时备份默认内容项不完整\n  期望: %q\n  实际: %q", expected, def.DefaultValue)
	}
}

// TestEveryRegisteredConfigHasRenderMetadata 断言通用渲染必需的元信息都齐全。
// 缺 Label 客户端只能拿长句 Description 当标题，缺 Order 则完全没有顺序可言
// （GET /api/configs 返回的是 map，本身不保序）。
func TestEveryRegisteredConfigHasRenderMetadata(t *testing.T) {
	defs := model.SystemConfigDefinitions()
	if len(defs) == 0 {
		t.Fatal("注册表不应为空")
	}

	seenKeys := make(map[string]bool, len(defs))
	for index, def := range defs {
		if def.Key == "" {
			t.Fatalf("第 %d 项配置的 Key 为空", index)
		}
		if seenKeys[def.Key] {
			t.Errorf("配置 %s 被重复注册", def.Key)
		}
		seenKeys[def.Key] = true

		if def.Label == "" {
			t.Errorf("配置 %s: Label 为空，客户端会退化成用长句 Description 当输入框标题", def.Key)
		}
		if def.Group == "" {
			t.Errorf("配置 %s: Group 为空", def.Key)
		}
		if def.GroupLabel == "" {
			t.Errorf("配置 %s: GroupLabel 为空", def.Key)
		}
		if def.Order != index {
			t.Errorf("配置 %s: Order 应等于注册下标 %d，实际 %d", def.Key, index, def.Order)
		}
	}
}

// TestEveryRegisteredConfigGroupHasLabel 拦截「新增了一个分组 slug 但忘了配中文名」的情况。
// 漏配时 GroupLabel 会退化成英文 slug，这里把它当失败处理。
func TestEveryRegisteredConfigGroupHasLabel(t *testing.T) {
	for _, def := range model.SystemConfigDefinitions() {
		if def.GroupLabel == def.Group {
			t.Errorf("分组 %s 缺少中文分组名，请在 systemConfigGroupLabels 中补一条（配置项: %s）", def.Group, def.Key)
		}
	}
}

// TestRegisteredIntConfigsExposeMinMax 断言整数配置的取值区间确实被下发出去了。
// 这个区间原本只活在 newIntConfig 的 normalize 闭包里，客户端拿不到，
// 只能等用户填了越界值再被服务端 400 打回。
func TestRegisteredIntConfigsExposeMinMax(t *testing.T) {
	intConfigCount := 0
	for _, def := range model.SystemConfigDefinitions() {
		if def.ValueType != model.SystemConfigTypeInt {
			if def.Min != nil || def.Max != nil {
				t.Errorf("配置 %s: 非整数类型不应带 min/max", def.Key)
			}
			continue
		}

		intConfigCount++
		if def.Min == nil || def.Max == nil {
			t.Errorf("配置 %s: 整数配置必须下发 min/max", def.Key)
			continue
		}
		if *def.Min > *def.Max {
			t.Errorf("配置 %s: min(%d) 不应大于 max(%d)", def.Key, *def.Min, *def.Max)
		}

		parsed, err := strconv.Atoi(def.DefaultValue)
		if err != nil {
			t.Errorf("配置 %s: 整数配置的默认值 %q 不是整数", def.Key, def.DefaultValue)
			continue
		}
		if parsed < *def.Min || parsed > *def.Max {
			t.Errorf("配置 %s: 默认值 %d 不在 %d-%d 之间", def.Key, parsed, *def.Min, *def.Max)
		}
	}

	if intConfigCount == 0 {
		t.Fatal("没有扫描到任何整数配置，用例可能在空转")
	}
}

// TestRegisteredIntConfigMinMaxMatchesValidation 断言下发的 min/max 与服务端实际的校验边界一致，
// 否则客户端按 schema 放行的值会被服务端 400 打回（或反过来，客户端拦住了服务端本可接受的值）。
func TestRegisteredIntConfigMinMaxMatchesValidation(t *testing.T) {
	for _, def := range model.SystemConfigDefinitions() {
		if def.ValueType != model.SystemConfigTypeInt || def.Min == nil || def.Max == nil {
			continue
		}

		if _, err := model.NormalizeSystemConfigValue(def.Key, strconv.Itoa(*def.Min)); err != nil {
			t.Errorf("配置 %s: 下界 %d 应当被接受，实际被拒: %v", def.Key, *def.Min, err)
		}
		if _, err := model.NormalizeSystemConfigValue(def.Key, strconv.Itoa(*def.Max)); err != nil {
			t.Errorf("配置 %s: 上界 %d 应当被接受，实际被拒: %v", def.Key, *def.Max, err)
		}
		if _, err := model.NormalizeSystemConfigValue(def.Key, strconv.Itoa(*def.Min-1)); err == nil {
			t.Errorf("配置 %s: 小于下界的 %d 应当被拒绝", def.Key, *def.Min-1)
		}
		if _, err := model.NormalizeSystemConfigValue(def.Key, strconv.Itoa(*def.Max+1)); err == nil {
			t.Errorf("配置 %s: 大于上界的 %d 应当被拒绝", def.Key, *def.Max+1)
		}
	}
}

// TestRegisteredSecretConfigsAreMarked 锁定凭据类配置的名单。
// 新增凭据类配置时必须同步更新这里，避免密钥被当成普通文本框明文显示在客户端上。
func TestRegisteredSecretConfigsAreMarked(t *testing.T) {
	expected := map[string]bool{
		"captcha_key":              true,
		"backup_schedule_password": true,
		"github_token":             true,
	}

	actual := make(map[string]bool)
	for _, def := range model.SystemConfigDefinitions() {
		if def.Secret {
			actual[def.Key] = true
		}
	}

	for key := range expected {
		if !actual[key] {
			t.Errorf("配置 %s 是凭据，必须标记 Secret=true", key)
		}
	}
	for key := range actual {
		if !expected[key] {
			t.Errorf("配置 %s 新标记了 Secret=true，请同步更新本用例的名单", key)
		}
	}
}

// TestRegisteredEnumConfigsHaveOptions 断言枚举配置一定带选项，否则客户端渲染不出下拉框。
func TestRegisteredEnumConfigsHaveOptions(t *testing.T) {
	for _, def := range model.SystemConfigDefinitions() {
		if def.ValueType != model.SystemConfigTypeEnum {
			continue
		}
		if len(def.Options) == 0 {
			t.Errorf("配置 %s: 枚举配置必须带 options", def.Key)
			continue
		}

		matched := false
		for _, option := range def.Options {
			if option.Value == def.DefaultValue {
				matched = true
			}
			if option.Label == "" {
				t.Errorf("配置 %s: 选项 %q 缺少显示文案", def.Key, option.Value)
			}
		}
		if !matched {
			t.Errorf("配置 %s: 默认值 %q 不在 options 里", def.Key, def.DefaultValue)
		}
	}
}
