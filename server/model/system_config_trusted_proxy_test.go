package model_test

import (
	"strings"
	"testing"

	"daidai-panel/database"
	"daidai-panel/model"
	"daidai-panel/pkg/netutil"
	"daidai-panel/testutil"
)

// deleteTrustedProxyMigratedMarker 删掉迁移标记，模拟「v3.4.3 及之前升级上来、还没迁移过」的存量实例。
// SetupTestEnv 跑 InitDefaultConfigs 时会按新装实例写上标记，存量场景要先把它拿掉。
func deleteTrustedProxyMigratedMarker(t *testing.T) {
	t.Helper()
	if err := database.DB.Where("`key` = ?", model.TrustedProxyCIDRsMigratedConfigKey).Delete(&model.SystemConfig{}).Error; err != nil {
		t.Fatalf("delete marker: %v", err)
	}
}

func trustedProxyMarkerExists(t *testing.T) bool {
	t.Helper()
	var count int64
	if err := database.DB.Model(&model.SystemConfig{}).Where("`key` = ?", model.TrustedProxyCIDRsMigratedConfigKey).Count(&count).Error; err != nil {
		t.Fatalf("count marker: %v", err)
	}
	return count > 0
}

// 审计 H-2：新装实例的可信代理默认只有本机地址，并且直接写上迁移标记（以后不再判断旧默认）。
func TestTrustedProxyCIDRsDefaultIsLoopbackOnly(t *testing.T) {
	testutil.SetupTestEnv(t)

	want := "127.0.0.1/32\n::1/128"
	if got := model.GetRegisteredConfig("trusted_proxy_cidrs"); got != want {
		t.Fatalf("fresh install trusted_proxy_cidrs should be %q, got %q", want, got)
	}
	if got := readSystemConfigValue(t, "trusted_proxy_cidrs"); got != want {
		t.Fatalf("fresh install stored trusted_proxy_cidrs should be %q, got %q", want, got)
	}
	if got := readSystemConfigValue(t, model.TrustedProxyCIDRsMigratedConfigKey); got != "1" {
		t.Fatalf("fresh install should write migration marker \"1\", got %q", got)
	}
}

// 存量实例：库里存的还是 v3.4.3 及之前的旧默认（含全部私网段），且没有迁移标记 → 抬到新默认并写标记。
// 同一组网段换了顺序或写法也算旧默认（信任范围完全一样）。
func TestInitDefaultConfigsMigratesLegacyTrustedProxyCIDRs(t *testing.T) {
	legacyValues := map[string]string{
		"stored canonical": strings.Join(netutil.LegacyDefaultTrustedProxyCIDRs(), "\n"),
		"reordered":        "192.168.0.0/16\n172.16.0.0/12\n10.0.0.0/8\n::1/128\n127.0.0.1/32",
	}
	for name, legacy := range legacyValues {
		t.Run(name, func(t *testing.T) {
			testutil.SetupTestEnv(t)
			deleteTrustedProxyMigratedMarker(t)

			setRawSystemConfigValue(t, "trusted_proxy_cidrs", legacy)
			model.InitDefaultConfigs()

			want := strings.Join(netutil.DefaultTrustedProxyCIDRs(), "\n")
			if got := readSystemConfigValue(t, "trusted_proxy_cidrs"); got != want {
				t.Fatalf("legacy trusted_proxy_cidrs should migrate to %q, got %q", want, got)
			}
			if !trustedProxyMarkerExists(t) {
				t.Fatal("migration should write the marker")
			}
		})
	}
}

// 用户自己增删过的列表一律不动 —— 哪怕里面还留着私网段，那也是用户自己定的信任范围。
// 没有标记的存量实例也一样：值不是旧默认就不改，只补上标记。
func TestInitDefaultConfigsKeepsCustomTrustedProxyCIDRs(t *testing.T) {
	customValues := []string{
		strings.Join(netutil.LegacyDefaultTrustedProxyCIDRs(), "\n") + "\n203.0.113.0/24",
		"127.0.0.1/32\n::1/128\n172.17.0.1/32",
		"10.0.0.0/8\n172.16.0.0/12\n192.168.0.0/16",
	}
	for _, custom := range customValues {
		t.Run(custom, func(t *testing.T) {
			testutil.SetupTestEnv(t)
			deleteTrustedProxyMigratedMarker(t)

			setRawSystemConfigValue(t, "trusted_proxy_cidrs", custom)
			model.InitDefaultConfigs()

			if got := readSystemConfigValue(t, "trusted_proxy_cidrs"); got != custom {
				t.Fatalf("custom trusted_proxy_cidrs must be preserved, want %q got %q", custom, got)
			}
			if !trustedProxyMarkerExists(t) {
				t.Fatal("marker should be written even when nothing was migrated")
			}
		})
	}
}

// 迁移过之后，管理员主动把那几段私网加回来（值又等于旧默认）：之后反复启动都不能再被改回本机。
func TestInitDefaultConfigsKeepsReAddedLegacyTrustedProxyCIDRsAfterMigration(t *testing.T) {
	testutil.SetupTestEnv(t)
	deleteTrustedProxyMigratedMarker(t)
	legacy := strings.Join(netutil.LegacyDefaultTrustedProxyCIDRs(), "\n")

	// 第一次启动：旧默认被迁移、标记写上
	setRawSystemConfigValue(t, "trusted_proxy_cidrs", legacy)
	model.InitDefaultConfigs()
	if got := readSystemConfigValue(t, "trusted_proxy_cidrs"); got != strings.Join(netutil.DefaultTrustedProxyCIDRs(), "\n") {
		t.Fatalf("first start should migrate legacy value, got %q", got)
	}

	// 管理员在设置页里把私网段加回来，然后重启几次
	if err := model.SetConfig("trusted_proxy_cidrs", legacy); err != nil {
		t.Fatalf("re-add legacy ranges: %v", err)
	}
	for i := 0; i < 3; i++ {
		model.InitDefaultConfigs()
		if got := readSystemConfigValue(t, "trusted_proxy_cidrs"); got != legacy {
			t.Fatalf("restart #%d must keep the admin's re-added ranges %q, got %q", i+1, legacy, got)
		}
	}
}

// 迁移必须幂等：旧默认迁移后再跑一次不变；本来就是新默认的实例跑几次都不变。
func TestInitDefaultConfigsTrustedProxyMigrationIsIdempotent(t *testing.T) {
	testutil.SetupTestEnv(t)
	deleteTrustedProxyMigratedMarker(t)
	want := strings.Join(netutil.DefaultTrustedProxyCIDRs(), "\n")

	setRawSystemConfigValue(t, "trusted_proxy_cidrs", strings.Join(netutil.LegacyDefaultTrustedProxyCIDRs(), "\n"))
	model.InitDefaultConfigs()
	first := readSystemConfigValue(t, "trusted_proxy_cidrs")
	model.InitDefaultConfigs()
	second := readSystemConfigValue(t, "trusted_proxy_cidrs")
	if first != want || second != want {
		t.Fatalf("migration is not idempotent: first=%q second=%q want=%q", first, second, want)
	}

	setRawSystemConfigValue(t, "trusted_proxy_cidrs", want)
	model.InitDefaultConfigs()
	if got := readSystemConfigValue(t, "trusted_proxy_cidrs"); got != want {
		t.Fatalf("new default must stay unchanged after re-run, got %q", got)
	}
}
