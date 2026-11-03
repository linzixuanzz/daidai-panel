package service

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"daidai-panel/database"
	"daidai-panel/model"
	"daidai-panel/testutil"
)

// resetPasskeyCeremonies 清空挑战会话表并在用例结束时恢复时钟与表
func resetPasskeyCeremonies(t *testing.T) {
	t.Helper()
	passkeyCeremonies.Lock()
	passkeyCeremonies.items = map[string]*passkeyCeremony{}
	passkeyCeremonies.Unlock()
	t.Cleanup(func() {
		passkeyNow = time.Now
		passkeyCeremonies.Lock()
		passkeyCeremonies.items = map[string]*passkeyCeremony{}
		passkeyCeremonies.Unlock()
	})
}

func TestPasskeyRPFromOrigin(t *testing.T) {
	cases := []struct {
		origin string
		want   string
		ok     bool
	}{
		{"https://panel.example.com", "panel.example.com", true},
		{"https://Panel.Example.COM:8443", "panel.example.com", true},
		{"HTTPS://panel.example.com", "panel.example.com", true},
		{"http://localhost", "localhost", true},
		{"http://localhost:5700", "localhost", true},
		{"https://localhost:5700", "localhost", true},
		{"", "", false},
		{"null", "", false},
		{"   ", "", false},
		{"http://panel.example.com", "", false},
		{"http://192.168.1.2:5700", "", false},
		{"https://192.168.1.2", "", false},
		{"https://[::1]:8443", "", false},
		{"https://[fe80::1]", "", false},
		{"ws://panel.example.com", "", false},
		{"panel.example.com", "", false},
		{"https://", "", false},
		{"https://panel.example.com/path", "", false},
		{"https://panel.example.com?x=1", "", false},
		{"https://user@panel.example.com", "", false},
		{"https://localhost:1:2", "", false},
	}
	for _, tc := range cases {
		got, err := PasskeyRPFromOrigin(tc.origin)
		if tc.ok {
			if err != nil || got != tc.want {
				t.Errorf("%q: got (%q, %v), want %q", tc.origin, got, err, tc.want)
			}
			continue
		}
		if err != ErrPasskeyUnsupportedOrigin {
			t.Errorf("%q: got (%q, %v), want ErrPasskeyUnsupportedOrigin", tc.origin, got, err)
		}
	}
}

func TestPasskeyCeremonyIsOneTimeAndExpires(t *testing.T) {
	resetPasskeyCeremonies(t)

	id, err := storePasskeyCeremony(&passkeyCeremony{purpose: PasskeyPurposeLogin, rpID: "a.example.com"})
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	if got, err := takePasskeyCeremony(id, PasskeyPurposeLogin); err != nil || got.rpID != "a.example.com" {
		t.Fatalf("first take: %#v %v", got, err)
	}
	if _, err := takePasskeyCeremony(id, PasskeyPurposeLogin); err != ErrPasskeySessionExpired {
		t.Fatalf("second take must fail as expired, got %v", err)
	}

	// 用途不对：拒绝，而且会话照样被消耗掉
	id, _ = storePasskeyCeremony(&passkeyCeremony{purpose: PasskeyPurposeRegister})
	if _, err := takePasskeyCeremony(id, PasskeyPurposeLogin); err != ErrPasskeySessionExpired {
		t.Fatalf("wrong purpose must fail, got %v", err)
	}
	if _, err := takePasskeyCeremony(id, PasskeyPurposeRegister); err != ErrPasskeySessionExpired {
		t.Fatalf("wrong-purpose take must still consume the session, got %v", err)
	}

	// 5 分钟后过期
	base := time.Now()
	passkeyNow = func() time.Time { return base }
	id, _ = storePasskeyCeremony(&passkeyCeremony{purpose: PasskeyPurposeLogin})
	passkeyNow = func() time.Time { return base.Add(5*time.Minute + time.Second) }
	if _, err := takePasskeyCeremony(id, PasskeyPurposeLogin); err != ErrPasskeySessionExpired {
		t.Fatalf("expired session must fail, got %v", err)
	}
	passkeyCeremonies.Lock()
	left := len(passkeyCeremonies.items)
	passkeyCeremonies.Unlock()
	if left != 0 {
		t.Fatalf("taken sessions must be removed, %d left", left)
	}
}

func TestPasskeyCeremonyStoreIsBounded(t *testing.T) {
	resetPasskeyCeremonies(t)

	base := time.Now()
	passkeyNow = func() time.Time { return base }
	// 每个 IP 最多挂 10 个，要刷满全局 1000 得用 100 个不同 IP
	for i := 0; i < passkeyCeremonyLimit; i++ {
		ip := fmt.Sprintf("10.0.%d.1", i/passkeyLoginCeremoniesPerIP)
		if _, err := storePasskeyCeremony(&passkeyCeremony{purpose: PasskeyPurposeLogin, limitKey: passkeyLoginLimitKey(ip)}); err != nil {
			t.Fatalf("store #%d: %v", i, err)
		}
	}
	// 满了、也没有过期的 → 新 IP 被拒，表不再变大
	if _, err := storePasskeyCeremony(&passkeyCeremony{purpose: PasskeyPurposeLogin, limitKey: passkeyLoginLimitKey("192.0.2.1")}); err != ErrPasskeyTooManySessions {
		t.Fatalf("expected ErrPasskeyTooManySessions, got %v", err)
	}
	// 免登录的 begin 被刷满时同样回 429 语义的错误
	if _, _, err := BeginPasskeyLogin("https://panel.example.com", "192.0.2.2"); err != ErrPasskeyTooManySessions {
		t.Fatalf("BeginPasskeyLogin should be rejected when full, got %v", err)
	}
	passkeyCeremonies.Lock()
	full := len(passkeyCeremonies.items)
	passkeyCeremonies.Unlock()
	if full != passkeyCeremonyLimit {
		t.Fatalf("rejected stores must not grow the table, size=%d", full)
	}

	// 时间过去 5 分钟，全部过期：下一次存入时先清掉过期的再放
	passkeyNow = func() time.Time { return base.Add(passkeyCeremonyTTL) }
	if _, err := storePasskeyCeremony(&passkeyCeremony{purpose: PasskeyPurposeLogin, limitKey: passkeyLoginLimitKey("192.0.2.1")}); err != nil {
		t.Fatalf("store after expiry: %v", err)
	}
	passkeyCeremonies.Lock()
	size := len(passkeyCeremonies.items)
	passkeyCeremonies.Unlock()
	if size != 1 {
		t.Fatalf("expired sessions should be purged, size=%d", size)
	}
}

// ceremonyCount 数表里某个限额键下还在的挑战会话
func ceremonyCount(limitKey string) int {
	passkeyCeremonies.Lock()
	defer passkeyCeremonies.Unlock()
	count := 0
	for _, item := range passkeyCeremonies.items {
		if item.limitKey == limitKey {
			count++
		}
	}
	return count
}

func TestPasskeyLoginLimitKey(t *testing.T) {
	cases := map[string]string{
		"198.51.100.7":          "ip:198.51.100.7",
		" 198.51.100.7 ":        "ip:198.51.100.7",
		"::ffff:198.51.100.7":   "ip:198.51.100.7",
		"2001:db8:1:2::1":       "ip:2001:db8:1:2::/64",
		"2001:db8:1:2:abcd::ff": "ip:2001:db8:1:2::/64",
		"2001:db8:1:3::1":       "ip:2001:db8:1:3::/64",
		"not-an-ip":             "ip:not-an-ip",
	}
	for in, want := range cases {
		if got := passkeyLoginLimitKey(in); got != want {
			t.Errorf("passkeyLoginLimitKey(%q) = %q, want %q", in, got, want)
		}
	}
}

// 同一个 IP 刷满 10 个后再来：挤掉它自己最旧的那个，照样能存；别的 IP 的会话一个不少。
func TestPasskeyLoginCeremonyPerIPEvictsOldest(t *testing.T) {
	resetPasskeyCeremonies(t)
	base := time.Now()
	passkeyNow = func() time.Time { return base }

	attacker := passkeyLoginLimitKey("2001:db8:1:2::1")
	victim := passkeyLoginLimitKey("198.51.100.9")
	var victimIDs []string
	for i := 0; i < 3; i++ {
		id, err := storePasskeyCeremony(&passkeyCeremony{purpose: PasskeyPurposeLogin, limitKey: victim})
		if err != nil {
			t.Fatalf("victim store: %v", err)
		}
		victimIDs = append(victimIDs, id)
	}

	var attackerIDs []string
	for i := 0; i < passkeyLoginCeremoniesPerIP; i++ {
		// 同一个 /64 里换着地址刷，也算同一个键
		key := passkeyLoginLimitKey(fmt.Sprintf("2001:db8:1:2::%x", i+1))
		id, err := storePasskeyCeremony(&passkeyCeremony{purpose: PasskeyPurposeLogin, limitKey: key})
		if err != nil {
			t.Fatalf("attacker store #%d: %v", i, err)
		}
		attackerIDs = append(attackerIDs, id)
	}
	if got := ceremonyCount(attacker); got != passkeyLoginCeremoniesPerIP {
		t.Fatalf("attacker should hold %d sessions, got %d", passkeyLoginCeremoniesPerIP, got)
	}

	// 第 11 个：成功，最旧的那个被挤掉，总数仍是 10
	newest, err := storePasskeyCeremony(&passkeyCeremony{purpose: PasskeyPurposeLogin, limitKey: attacker})
	if err != nil {
		t.Fatalf("11th store from the same IP must succeed by evicting its oldest, got %v", err)
	}
	if got := ceremonyCount(attacker); got != passkeyLoginCeremoniesPerIP {
		t.Fatalf("attacker should still hold %d sessions, got %d", passkeyLoginCeremoniesPerIP, got)
	}
	if _, err := takePasskeyCeremony(attackerIDs[0], PasskeyPurposeLogin); err != ErrPasskeySessionExpired {
		t.Fatalf("the oldest session should be evicted, got %v", err)
	}
	if _, err := takePasskeyCeremony(attackerIDs[1], PasskeyPurposeLogin); err != nil {
		t.Fatalf("the second oldest must survive: %v", err)
	}
	if _, err := takePasskeyCeremony(newest, PasskeyPurposeLogin); err != nil {
		t.Fatalf("the newest must be usable: %v", err)
	}

	// 不同 IP 互不影响：受害者的 3 个都还能用
	for _, id := range victimIDs {
		if _, err := takePasskeyCeremony(id, PasskeyPurposeLogin); err != nil {
			t.Fatalf("another IP's session must not be evicted: %v", err)
		}
	}

	// 先清过期的：时间过去 5 分钟后再来，过期的直接清掉，不需要挤
	passkeyNow = func() time.Time { return base.Add(passkeyCeremonyTTL) }
	if _, err := storePasskeyCeremony(&passkeyCeremony{purpose: PasskeyPurposeLogin, limitKey: attacker}); err != nil {
		t.Fatalf("store after expiry: %v", err)
	}
	if got := ceremonyCount(attacker); got != 1 {
		t.Fatalf("expired sessions of the key should be purged, got %d", got)
	}
}

// 注册会话按用户限 5 个、挤掉自己最旧的；不占登录的全局 1000 名额。
func TestPasskeyRegisterCeremonyQuotaIsSeparate(t *testing.T) {
	resetPasskeyCeremonies(t)
	base := time.Now()
	passkeyNow = func() time.Time { return base }

	// 登录会话刷到 999 个
	for i := 0; i < passkeyCeremonyLimit-1; i++ {
		ip := fmt.Sprintf("10.1.%d.1", i/passkeyLoginCeremoniesPerIP)
		if _, err := storePasskeyCeremony(&passkeyCeremony{purpose: PasskeyPurposeLogin, limitKey: passkeyLoginLimitKey(ip)}); err != nil {
			t.Fatalf("login store #%d: %v", i, err)
		}
	}

	// 用户 1 加 6 次：都成功，只留最新 5 个；用户 2 的不受影响
	alice := passkeyRegisterLimitKey(1)
	bob := passkeyRegisterLimitKey(2)
	bobID, err := storePasskeyCeremony(&passkeyCeremony{purpose: PasskeyPurposeRegister, userID: 2, limitKey: bob})
	if err != nil {
		t.Fatalf("bob store: %v", err)
	}
	var aliceIDs []string
	for i := 0; i < passkeyRegisterCeremoniesPerUser+1; i++ {
		id, err := storePasskeyCeremony(&passkeyCeremony{purpose: PasskeyPurposeRegister, userID: 1, limitKey: alice})
		if err != nil {
			t.Fatalf("register store #%d must succeed even with login sessions near full: %v", i, err)
		}
		aliceIDs = append(aliceIDs, id)
	}
	if got := ceremonyCount(alice); got != passkeyRegisterCeremoniesPerUser {
		t.Fatalf("alice should hold %d register sessions, got %d", passkeyRegisterCeremoniesPerUser, got)
	}
	if _, err := takePasskeyCeremony(aliceIDs[0], PasskeyPurposeRegister); err != ErrPasskeySessionExpired {
		t.Fatalf("alice's oldest register session should be evicted, got %v", err)
	}
	if _, err := takePasskeyCeremony(bobID, PasskeyPurposeRegister); err != nil {
		t.Fatalf("bob's register session must survive: %v", err)
	}

	// 注册会话不占登录名额：表里登录 999 + 注册 5，第 1000 个登录会话照样放得进去
	if _, err := storePasskeyCeremony(&passkeyCeremony{purpose: PasskeyPurposeLogin, limitKey: passkeyLoginLimitKey("192.0.2.10")}); err != nil {
		t.Fatalf("register sessions must not take login quota: %v", err)
	}
	// 登录满 1000 后新 IP 被拒，但注册照常
	if _, err := storePasskeyCeremony(&passkeyCeremony{purpose: PasskeyPurposeLogin, limitKey: passkeyLoginLimitKey("192.0.2.11")}); err != ErrPasskeyTooManySessions {
		t.Fatalf("expected ErrPasskeyTooManySessions once login sessions are full, got %v", err)
	}
	if _, err := storePasskeyCeremony(&passkeyCeremony{purpose: PasskeyPurposeRegister, userID: 3, limitKey: passkeyRegisterLimitKey(3)}); err != nil {
		t.Fatalf("register must not be blocked by a full login table: %v", err)
	}
}

// 改密 / 重置密码：新密码与清通行密钥同一事务，只清这个用户的，返回清掉的个数。
func TestSetPasswordAndClearPasskeys(t *testing.T) {
	testutil.SetupTestEnv(t)

	alice := testutil.MustCreateUser(t, "pw-clear-alice", "admin")
	bob := testutil.MustCreateUser(t, "pw-clear-bob", "operator")
	for i, owner := range []uint{alice.ID, alice.ID, bob.ID} {
		if err := database.DB.Create(&model.Passkey{UserID: owner, Name: "k", RPID: "panel.example.com", Origin: "https://panel.example.com", CredentialID: fmt.Sprintf("pw-clear-%d", i), UserHandle: "aA", Credential: "{}"}).Error; err != nil {
			t.Fatalf("seed passkey: %v", err)
		}
	}

	removed, err := SetPasswordAndClearPasskeys(alice.ID, "new-hash")
	if err != nil || removed != 2 {
		t.Fatalf("expected 2 removed, got %d (%v)", removed, err)
	}
	var reloaded model.User
	database.DB.First(&reloaded, alice.ID)
	if reloaded.Password != "new-hash" {
		t.Fatalf("password hash not written: %q", reloaded.Password)
	}
	var aliceKeys, bobKeys int64
	database.DB.Model(&model.Passkey{}).Where("user_id = ?", alice.ID).Count(&aliceKeys)
	database.DB.Model(&model.Passkey{}).Where("user_id = ?", bob.ID).Count(&bobKeys)
	if aliceKeys != 0 || bobKeys != 1 {
		t.Fatalf("expected alice's passkeys removed and bob's kept, got %d / %d", aliceKeys, bobKeys)
	}

	// 没有通行密钥时照常改密，返回 0
	if removed, err := SetPasswordAndClearPasskeys(alice.ID, "newer-hash"); err != nil || removed != 0 {
		t.Fatalf("expected 0 removed, got %d (%v)", removed, err)
	}
}

func TestNormalizePasskeyName(t *testing.T) {
	long := ""
	for i := 0; i < 70; i++ {
		long += "钥"
	}
	cases := map[string]string{
		"":                 "通行密钥",
		"   ":              "通行密钥",
		" 我的手机 ":           "我的手机",
		long:               long[:64*len("钥")],
		"Windows · Chrome": "Windows · Chrome",
	}
	for in, want := range cases {
		if got := NormalizePasskeyName(in); got != want {
			t.Errorf("NormalizePasskeyName(%q) = %q, want %q", in, got, want)
		}
	}
}

// 备份：通行密钥随配置导出（带用户名），恢复语义同 2FA——保留当前面板的，不导入备份里的；老备份没有这个键照常恢复。
func TestBackupPasskeysExportAndRestoreSemantics(t *testing.T) {
	testutil.SetupTestEnv(t)

	user := testutil.MustCreateUser(t, "backup-passkey-user", "admin")
	usedAt := time.Now().Add(-time.Hour).Truncate(time.Second)
	current := model.Passkey{
		UserID: user.ID, Name: "当前的", RPID: "panel.example.com", Origin: "https://panel.example.com:8443", CredentialID: "current-cred",
		UserHandle: "aGFuZGxl", Credential: `{"flags":{"backupState":true}}`, LastUsedAt: &usedAt,
	}
	if err := database.DB.Create(&current).Error; err != nil {
		t.Fatalf("seed passkey: %v", err)
	}

	bundle, err := snapshotConfigBundle()
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if len(bundle.Passkeys) != 1 {
		t.Fatalf("expected 1 exported passkey, got %#v", bundle.Passkeys)
	}
	exported := bundle.Passkeys[0]
	if exported.UserID != user.ID || exported.Username != user.Username || exported.Name != "当前的" || exported.RPID != "panel.example.com" ||
		exported.Origin != "https://panel.example.com:8443" ||
		exported.CredentialID != "current-cred" || exported.UserHandle != "aGFuZGxl" || exported.Credential != current.Credential ||
		exported.LastUsedAt == nil || !exported.LastUsedAt.Equal(usedAt) {
		t.Fatalf("unexpected exported passkey %#v", exported)
	}

	// JSON 往返：键名稳定
	raw, _ := json.Marshal(bundle)
	var decoded BackupConfigBundle
	if err := json.Unmarshal(raw, &decoded); err != nil || len(decoded.Passkeys) != 1 || decoded.Passkeys[0].CredentialID != "current-cred" ||
		decoded.Passkeys[0].Origin != "https://panel.example.com:8443" {
		t.Fatalf("round trip failed: %v %#v", err, decoded.Passkeys)
	}

	// 老备份没有 passkeys 键 → nil，照常恢复
	var legacy BackupConfigBundle
	if err := json.Unmarshal([]byte(`{"system_configs":[{"key":"panel_title","value":"老备份"}]}`), &legacy); err != nil || legacy.Passkeys != nil {
		t.Fatalf("legacy bundle should decode without passkeys: %v %#v", err, legacy.Passkeys)
	}

	manifest := BackupManifest{
		Format: "daidai-panel-backup", Version: "0.4.0", Source: "daidai-panel",
		Selection: BackupSelection{Configs: true},
		Data: BackupPayload{Configs: BackupConfigBundle{
			SystemConfigs: []model.SystemConfig{{Key: "panel_title", Value: "来自备份"}},
			Users:         []BackupUser{{ID: 99, Username: "backup-admin", PasswordHash: "x", Role: "admin", Enabled: true}},
			Passkeys:      []BackupPasskey{{UserID: 99, Username: "backup-admin", Name: "备份里的", RPID: "panel.example.com", CredentialID: "backup-cred", UserHandle: "eA", Credential: "{}"}},
		}},
	}
	if err := restoreBackupManifest(manifest, t.TempDir()); err != nil {
		t.Fatalf("restore: %v", err)
	}
	var rows []model.Passkey
	database.DB.Order("id ASC").Find(&rows)
	if len(rows) != 1 || rows[0].CredentialID != "current-cred" || rows[0].UserID != user.ID {
		t.Fatalf("restore must keep current passkeys and not import backup ones, got %#v", rows)
	}

	// 老备份（没有 passkeys）恢复同样不动现有通行密钥
	manifest.Data.Configs.Passkeys = nil
	if err := restoreBackupManifest(manifest, t.TempDir()); err != nil {
		t.Fatalf("restore legacy: %v", err)
	}
	var count int64
	database.DB.Model(&model.Passkey{}).Count(&count)
	if count != 1 {
		t.Fatalf("legacy restore must keep current passkeys, got %d", count)
	}
}

// restorePasskeys 与 restoreTwoFactorAuths 同构：只恢复能映射到新用户 ID 的行
func TestRestorePasskeysMapsUserIDs(t *testing.T) {
	testutil.SetupTestEnv(t)

	user := testutil.MustCreateUser(t, "restore-passkey-user", "admin")
	items := []BackupPasskey{
		{UserID: 7, Name: "能映射", RPID: "a.example.com", Origin: "https://a.example.com", CredentialID: "c1", UserHandle: "h", Credential: "{}"},
		{UserID: 8, Name: "映射不到", RPID: "a.example.com", CredentialID: "c2", UserHandle: "h", Credential: "{}"},
	}
	if err := restorePasskeys(database.DB, items, map[uint]uint{7: user.ID}); err != nil {
		t.Fatalf("restorePasskeys: %v", err)
	}
	var rows []model.Passkey
	database.DB.Find(&rows)
	if len(rows) != 1 || rows[0].UserID != user.ID || rows[0].CredentialID != "c1" || rows[0].Origin != "https://a.example.com" {
		t.Fatalf("unexpected restored rows %s", fmt.Sprintf("%#v", rows))
	}
}
