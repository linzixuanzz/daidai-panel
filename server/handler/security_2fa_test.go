package handler_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"daidai-panel/service"
	"daidai-panel/testutil"
)

func seedEnabled2FA(t *testing.T, userID uint) string {
	t.Helper()
	secret, _, err := service.SetupTwoFactor(userID)
	if err != nil {
		t.Fatalf("setup 2fa: %v", err)
	}
	code := service.GenerateCurrentTOTPForTest(secret)
	if err := service.VerifyAndEnableTwoFactor(userID, code); err != nil {
		t.Fatalf("enable 2fa: %v", err)
	}
	if !service.IsTwoFactorEnabled(userID) {
		t.Fatalf("expected 2fa enabled after verify")
	}
	return secret
}

func TestDisable2FARejectsMissingCode(t *testing.T) {
	testutil.SetupTestEnv(t)

	user := testutil.MustCreateUser(t, "twofa-user-missing", "admin")
	token := testutil.MustCreateAccessToken(t, user.Username, user.Role)
	seedEnabled2FA(t, user.ID)

	engine := newProtectedRouter()
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/security/2fa", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 without TOTP code, got %d body=%s", rec.Code, rec.Body.String())
	}
	if !service.IsTwoFactorEnabled(user.ID) {
		t.Fatalf("2FA should remain enabled after failed disable")
	}
}

func TestDisable2FARejectsInvalidCode(t *testing.T) {
	testutil.SetupTestEnv(t)

	user := testutil.MustCreateUser(t, "twofa-user-invalid", "admin")
	token := testutil.MustCreateAccessToken(t, user.Username, user.Role)
	seedEnabled2FA(t, user.ID)

	engine := newProtectedRouter()
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/security/2fa", strings.NewReader(`{"code":"000000"}`))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 with invalid code, got %d body=%s", rec.Code, rec.Body.String())
	}
	if !service.IsTwoFactorEnabled(user.ID) {
		t.Fatalf("2FA should remain enabled when invalid code provided")
	}
}

func TestDisable2FAAcceptsValidCode(t *testing.T) {
	testutil.SetupTestEnv(t)

	user := testutil.MustCreateUser(t, "twofa-user-valid", "admin")
	token := testutil.MustCreateAccessToken(t, user.Username, user.Role)
	secret := seedEnabled2FA(t, user.ID)

	// seedEnabled2FA 启用时已经用掉了当前时间步的码，同一个码不能再用（防重放），
	// 这里取下一个时间步的码，相当于用户等验证器跳到新码再来关闭
	code := service.GenerateNextTOTPForTest(secret)
	body := fmt.Sprintf(`{"code":"%s"}`, code)

	engine := newProtectedRouter()
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/security/2fa", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 with valid code, got %d body=%s", rec.Code, rec.Body.String())
	}
	if service.IsTwoFactorEnabled(user.ID) {
		t.Fatalf("expected 2FA disabled after valid code")
	}
}

func TestDisable2FAIdempotentWhenAlreadyOff(t *testing.T) {
	testutil.SetupTestEnv(t)

	user := testutil.MustCreateUser(t, "twofa-user-noop", "admin")
	token := testutil.MustCreateAccessToken(t, user.Username, user.Role)
	// Do NOT enable 2FA — disabling should be a no-op success.

	engine := newProtectedRouter()
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/security/2fa", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 when 2FA already off, got %d body=%s", rec.Code, rec.Body.String())
	}
}
