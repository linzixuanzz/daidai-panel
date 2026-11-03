package main

import (
	"fmt"
	"testing"

	"daidai-panel/config"
	"daidai-panel/database"
	"daidai-panel/model"
	"daidai-panel/pkg/crypto"
	"daidai-panel/testutil"
)

// ddp reset-password 与网页改密同一口径：重置密码时一并清掉该用户的全部通行密钥，别人的不动。
func TestCLIResetPasswordClearsPasskeys(t *testing.T) {
	testutil.SetupTestEnv(t)
	user := testutil.MustCreateLoginUser(t, "cli_reset_pk", "admin", "Password123!")
	other := testutil.MustCreateUser(t, "cli_reset_pk_other", "operator")
	for i, owner := range []uint{user.ID, user.ID, other.ID} {
		if err := database.DB.Create(&model.Passkey{
			UserID: owner, Name: "k", RPID: "panel.example.com", Origin: "https://panel.example.com",
			CredentialID: fmt.Sprintf("cli-reset-%d", i), UserHandle: "aA", Credential: "{}",
		}).Error; err != nil {
			t.Fatalf("seed passkey: %v", err)
		}
	}

	rt := &cliRuntime{cfg: config.C}
	if err := runResetPassword(rt, []string{user.Username, "NewPassword456!"}); err != nil {
		t.Fatalf("ddp reset-password: %v", err)
	}

	var reloaded model.User
	database.DB.First(&reloaded, user.ID)
	if !crypto.CheckPassword("NewPassword456!", reloaded.Password) {
		t.Fatalf("new password should be written")
	}
	var own, others int64
	database.DB.Model(&model.Passkey{}).Where("user_id = ?", user.ID).Count(&own)
	database.DB.Model(&model.Passkey{}).Where("user_id = ?", other.ID).Count(&others)
	if own != 0 || others != 1 {
		t.Fatalf("expected own passkeys cleared and others kept, got %d / %d", own, others)
	}
}
