package service

import (
	"strings"
	"testing"

	"daidai-panel/database"
	"daidai-panel/model"
	"daidai-panel/pkg/netutil"
	"daidai-panel/testutil"
)

// trusted_proxy_cidrs 的旧默认迁移只做一次（审计 H-2，v3.4.4）。恢复备份会整表替换 system_configs：
//   - 老备份（不带迁移标记）里的旧默认要被抬到新默认，并补上标记；
//   - 新备份（带标记）里即使是那几段私网，也是管理员自己选的，必须原样保留。
func restoreTrustedProxyConfigsForTest(t *testing.T, configs []model.SystemConfig) {
	t.Helper()
	manifest := BackupManifest{
		Format:    "daidai-panel-backup",
		Version:   "0.4.0",
		Source:    "daidai-panel",
		Selection: BackupSelection{Configs: true},
		Data: BackupPayload{
			Configs: BackupConfigBundle{SystemConfigs: configs},
		},
	}
	if err := restoreBackupManifest(manifest, t.TempDir()); err != nil {
		t.Fatalf("restore backup manifest: %v", err)
	}
}

func readTrustedProxyConfigRow(t *testing.T, key string) (string, bool) {
	t.Helper()
	var cfg model.SystemConfig
	if err := database.DB.Where("`key` = ?", key).First(&cfg).Error; err != nil {
		return "", false
	}
	return cfg.Value, true
}

func TestRestoreOldBackupMigratesLegacyTrustedProxyCIDRs(t *testing.T) {
	testutil.SetupTestEnv(t)
	legacy := strings.Join(netutil.LegacyDefaultTrustedProxyCIDRs(), "\n")

	restoreTrustedProxyConfigsForTest(t, []model.SystemConfig{
		{Key: "trusted_proxy_cidrs", Value: legacy},
	})

	want := strings.Join(netutil.DefaultTrustedProxyCIDRs(), "\n")
	if got, _ := readTrustedProxyConfigRow(t, "trusted_proxy_cidrs"); got != want {
		t.Fatalf("old backup's legacy trusted_proxy_cidrs should migrate to %q, got %q", want, got)
	}
	if got, ok := readTrustedProxyConfigRow(t, model.TrustedProxyCIDRsMigratedConfigKey); !ok || got != "1" {
		t.Fatalf("marker should be present after restoring an old backup, got %q (exists=%v)", got, ok)
	}
}

func TestRestoreNewBackupKeepsReAddedLegacyTrustedProxyCIDRs(t *testing.T) {
	testutil.SetupTestEnv(t)
	legacy := strings.Join(netutil.LegacyDefaultTrustedProxyCIDRs(), "\n")

	restoreTrustedProxyConfigsForTest(t, []model.SystemConfig{
		{Key: "trusted_proxy_cidrs", Value: legacy},
		{Key: model.TrustedProxyCIDRsMigratedConfigKey, Value: "1"},
	})

	if got, _ := readTrustedProxyConfigRow(t, "trusted_proxy_cidrs"); got != legacy {
		t.Fatalf("backup with marker must keep the admin's trusted_proxy_cidrs %q, got %q", legacy, got)
	}
	if got, ok := readTrustedProxyConfigRow(t, model.TrustedProxyCIDRsMigratedConfigKey); !ok || got != "1" {
		t.Fatalf("marker from the backup should be restored, got %q (exists=%v)", got, ok)
	}
}

// 新备份导出时要带上标记，否则拿它恢复会被当成老备份，又把管理员加回的私网段改掉。
func TestBackupSnapshotIncludesTrustedProxyMigratedMarker(t *testing.T) {
	testutil.SetupTestEnv(t)

	bundle, err := snapshotConfigBundle()
	if err != nil {
		t.Fatalf("snapshot config bundle: %v", err)
	}
	for _, cfg := range bundle.SystemConfigs {
		if cfg.Key == model.TrustedProxyCIDRsMigratedConfigKey {
			return
		}
	}
	t.Fatalf("backup snapshot should include %s", model.TrustedProxyCIDRsMigratedConfigKey)
}
