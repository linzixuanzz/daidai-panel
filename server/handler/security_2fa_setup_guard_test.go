package handler_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"daidai-panel/database"
	"daidai-panel/model"
	"daidai-panel/service"
	"daidai-panel/testutil"
)

// 已启用两步验证时，POST /security/2fa/setup 必须拒绝：
// 否则它会把记录重置成「新密钥 + 未启用」，等于只凭登录会话、不用动态码就关掉了 2FA，绕过 Disable2FA 的验码。
func TestSetup2FARejectsWhenAlreadyEnabled(t *testing.T) {
	testutil.SetupTestEnv(t)

	user := testutil.MustCreateUser(t, "twofa-setup-enabled", "admin")
	token := testutil.MustCreateAccessToken(t, user.Username, user.Role)
	secret := seedEnabled2FA(t, user.ID)

	engine := newProtectedRouter()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/security/2fa/setup", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 when 2FA already enabled, got %d body=%s", rec.Code, rec.Body.String())
	}

	// 只看状态码不够：要确认库里的密钥和启用标记都没被动过，旧验证器依然能用
	var stored model.TwoFactorAuth
	if err := database.DB.Where("user_id = ?", user.ID).First(&stored).Error; err != nil {
		t.Fatalf("load two_factor_auths row: %v", err)
	}
	if stored.Secret != secret {
		t.Fatalf("secret should stay unchanged after rejected setup")
	}
	if !stored.Enabled {
		t.Fatalf("2FA should stay enabled after rejected setup")
	}
}

// 未启用时照常返回新密钥和二维码地址，网页与 APP 的首次绑定流程不受影响。
func TestSetup2FAStillWorksWhenDisabled(t *testing.T) {
	testutil.SetupTestEnv(t)

	user := testutil.MustCreateUser(t, "twofa-setup-disabled", "admin")
	token := testutil.MustCreateAccessToken(t, user.Username, user.Role)

	engine := newProtectedRouter()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/security/2fa/setup", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 when 2FA disabled, got %d body=%s", rec.Code, rec.Body.String())
	}

	var body struct {
		Data struct {
			Secret string `json:"secret"`
			URI    string `json:"uri"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v body=%s", err, rec.Body.String())
	}
	if body.Data.Secret == "" || body.Data.URI == "" {
		t.Fatalf("expected secret and uri in response, got body=%s", rec.Body.String())
	}
	if service.IsTwoFactorEnabled(user.ID) {
		t.Fatalf("setup alone must not enable 2FA")
	}
}
