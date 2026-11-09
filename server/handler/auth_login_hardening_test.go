package handler_test

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"daidai-panel/config"
	"daidai-panel/database"
	"daidai-panel/handler"
	"daidai-panel/model"
	"daidai-panel/service"
	"daidai-panel/testutil"
)

// H-1：用户名首尾带空白的登录与去掉空白后的是同一个账号，失败计数和锁定也必须是同一行。
// 用各种空白变体输错 5 次后，原名和任意空白变体（哪怕密码正确）都要被 429 锁住。
func TestLoginLockCoversWhitespaceVariantsOfUsername(t *testing.T) {
	testutil.SetupTestEnv(t)

	password := "Password123!"
	user := testutil.MustCreateLoginUser(t, "lock-trim-user", "admin", password)
	clientIP := "198.51.100.140"

	// 登录接口每个 IP 每分钟限 5 次，失败的 5 次用一个路由，锁定检查换一个新路由（新限流器），
	// 避免把限流的 429 误当成锁定的 429
	failEngine := newProtectedRouter()
	for _, name := range []string{"lock-trim-user ", " lock-trim-user", "lock-trim-user  ", "  lock-trim-user ", "lock-trim-user"} {
		rec := performJSONRequest(failEngine, http.MethodPost, "/api/v1/auth/login",
			`{"username":"`+name+`","password":"wrong-password"}`, nil, clientIP)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("username %q wrong password: expected 401, got %d body=%s", name, rec.Code, rec.Body.String())
		}
	}

	if got := service.GetLoginAttemptCount(clientIP, user.Username); got != 5 {
		t.Fatalf("expected all whitespace variants to share one failure counter, got %d", got)
	}

	checkEngine := newProtectedRouter()
	for _, name := range []string{"lock-trim-user", "lock-trim-user ", " lock-trim-user"} {
		rec := performJSONRequest(checkEngine, http.MethodPost, "/api/v1/auth/login",
			`{"username":"`+name+`","password":"`+password+`"}`, nil, clientIP)
		if rec.Code != http.StatusTooManyRequests {
			t.Fatalf("username %q after lock: expected 429, got %d body=%s", name, rec.Code, rec.Body.String())
		}
		if got, _ := decodeJSONMap(t, rec)["code"].(string); got != handler.LoginCodeAccountLocked {
			t.Fatalf("username %q after lock: expected code %q, got %q", name, handler.LoginCodeAccountLocked, got)
		}
	}

	// 登录日志也只记去掉空白后的用户名，不再出现一堆只差空格的「不同用户」
	var stray int64
	database.DB.Model(&model.LoginLog{}).Where("username <> ?", user.Username).Count(&stray)
	if stray != 0 {
		t.Fatalf("expected every login log to use the trimmed username, found %d stray rows", stray)
	}
}

// M-3：同一个两步验证码第二次登录回 401「两步验证码错误」；下一个时间步的新码可以登录。
func TestLoginRejectsReusedTOTPCode(t *testing.T) {
	testutil.SetupTestEnv(t)

	password := "Password123!"
	user := testutil.MustCreateLoginUser(t, "totp-reuse-user", "admin", password)
	secret := mustEnableTwoFactor(t, user.ID)
	engine := newProtectedRouter()
	clientIP := "198.51.100.141"

	code := service.GenerateCurrentTOTPForTest(secret)
	body := `{"username":"` + user.Username + `","password":"` + password + `","totp_code":"` + code + `"}`

	first := performJSONRequest(engine, http.MethodPost, "/api/v1/auth/login", body, nil, clientIP)
	if first.Code != http.StatusOK {
		t.Fatalf("first login with fresh code: expected 200, got %d body=%s", first.Code, first.Body.String())
	}

	replay := performJSONRequest(engine, http.MethodPost, "/api/v1/auth/login", body, nil, clientIP)
	if replay.Code != http.StatusUnauthorized {
		t.Fatalf("replayed code: expected 401, got %d body=%s", replay.Code, replay.Body.String())
	}
	payload := decodeJSONMap(t, replay)
	if got, _ := payload["error"].(string); got != "两步验证码错误" {
		t.Fatalf("replayed code: expected error 两步验证码错误, got %q", got)
	}
	if got, _ := payload["code"].(string); got != handler.LoginCodeInvalidTOTP {
		t.Fatalf("replayed code: expected code %q, got %q", handler.LoginCodeInvalidTOTP, got)
	}

	next := performJSONRequest(engine, http.MethodPost, "/api/v1/auth/login",
		`{"username":"`+user.Username+`","password":"`+password+`","totp_code":"`+service.GenerateNextTOTPForTest(secret)+`"}`,
		nil, clientIP)
	if next.Code != http.StatusOK {
		t.Fatalf("next time step's code: expected 200, got %d body=%s", next.Code, next.Body.String())
	}
}

// M-3：启用 2FA 时输入的那个码，不能立刻再拿来登录。
func TestLoginRejectsCodeJustUsedToEnableTwoFactor(t *testing.T) {
	testutil.SetupTestEnv(t)

	password := "Password123!"
	user := testutil.MustCreateLoginUser(t, "totp-enable-reuse", "admin", password)
	secret, _, err := service.SetupTwoFactor(user.ID)
	if err != nil {
		t.Fatalf("setup 2fa: %v", err)
	}
	// 码只生成一次、启用和登录用的是同一个值：不管中间有没有跨过时间步，结论都不变
	code := service.GenerateCurrentTOTPForTest(secret)
	if err := service.VerifyAndEnableTwoFactor(user.ID, code); err != nil {
		t.Fatalf("enable 2fa: %v", err)
	}

	engine := newProtectedRouter()
	rec := performJSONRequest(engine, http.MethodPost, "/api/v1/auth/login",
		`{"username":"`+user.Username+`","password":"`+password+`","totp_code":"`+code+`"}`, nil, "198.51.100.142")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("login with the enable code: expected 401, got %d body=%s", rec.Code, rec.Body.String())
	}
	if got, _ := decodeJSONMap(t, rec)["code"].(string); got != handler.LoginCodeInvalidTOTP {
		t.Fatalf("login with the enable code: expected code %q, got %q", handler.LoginCodeInvalidTOTP, got)
	}
}

// M-3：登录、关闭 2FA、添加通行密钥共用同一份已用记录——登录用过的码，在另外两条路径上也作废。
func TestTOTPCodeUsedForLoginIsRejectedOnDisableAndPasskeyPaths(t *testing.T) {
	testutil.SetupTestEnv(t)

	password := "Password123!"
	user := testutil.MustCreateLoginUser(t, "totp-shared-user", "admin", password)
	token := testutil.MustCreateAccessToken(t, user.Username, user.Role)
	secret := mustEnableTwoFactor(t, user.ID)
	engine := newProtectedRouter()
	clientIP := "198.51.100.143"

	code := service.GenerateCurrentTOTPForTest(secret)
	login := performJSONRequest(engine, http.MethodPost, "/api/v1/auth/login",
		`{"username":"`+user.Username+`","password":"`+password+`","totp_code":"`+code+`"}`, nil, clientIP)
	if login.Code != http.StatusOK {
		t.Fatalf("login: expected 200, got %d body=%s", login.Code, login.Body.String())
	}

	disable := performJSONRequest(engine, http.MethodDelete, "/api/v1/security/2fa", `{"code":"`+code+`"}`,
		map[string]string{"Authorization": "Bearer " + token}, clientIP)
	if disable.Code != http.StatusBadRequest {
		t.Fatalf("disable 2FA with a used code: expected 400, got %d body=%s", disable.Code, disable.Body.String())
	}
	if !service.IsTwoFactorEnabled(user.ID) {
		t.Fatalf("2FA must stay enabled when the disable code was already used")
	}

	passkey := passkeyRequest(engine, http.MethodPost, "/api/v1/security/passkeys/register/begin",
		map[string]string{"password": password, "totp_code": code}, token, passkeyTestOrigin, clientIP)
	if passkey.Code != http.StatusBadRequest {
		t.Fatalf("passkey register with a used code: expected 400, got %d body=%s", passkey.Code, passkey.Body.String())
	}
	if got, _ := decodeJSONMap(t, passkey)["code"].(string); got != handler.LoginCodeInvalidTOTP {
		t.Fatalf("passkey register with a used code: expected code %q, got %q", handler.LoginCodeInvalidTOTP, got)
	}
}

// L-1：被禁用的账号输错密码，回的是通用的「用户名或密码错误」，不能凭禁用提示判断账号存在；密码对才回 403 禁用。
func TestLoginDisabledUserWrongPasswordGetsGenericError(t *testing.T) {
	testutil.SetupTestEnv(t)

	password := "Password123!"
	user := testutil.MustCreateLoginUser(t, "disabled-generic-user", "operator", password)
	if err := database.DB.Model(&model.User{}).Where("id = ?", user.ID).Update("enabled", false).Error; err != nil {
		t.Fatalf("disable user: %v", err)
	}
	engine := newProtectedRouter()
	clientIP := "198.51.100.144"

	wrong := performJSONRequest(engine, http.MethodPost, "/api/v1/auth/login",
		`{"username":"`+user.Username+`","password":"wrong-password"}`, nil, clientIP)
	if wrong.Code != http.StatusUnauthorized {
		t.Fatalf("disabled + wrong password: expected 401, got %d body=%s", wrong.Code, wrong.Body.String())
	}
	payload := decodeJSONMap(t, wrong)
	if got, _ := payload["error"].(string); got != "用户名或密码错误" {
		t.Fatalf("disabled + wrong password: expected generic error, got %q", got)
	}
	if got, _ := payload["code"].(string); got != handler.LoginCodeInvalidCredentials {
		t.Fatalf("disabled + wrong password: expected code %q, got %q", handler.LoginCodeInvalidCredentials, got)
	}

	right := performJSONRequest(engine, http.MethodPost, "/api/v1/auth/login",
		`{"username":"`+user.Username+`","password":"`+password+`"}`, nil, clientIP)
	if right.Code != http.StatusForbidden {
		t.Fatalf("disabled + correct password: expected 403, got %d body=%s", right.Code, right.Body.String())
	}
}

// uploadAvatarForTest 以 multipart 上传一个头像文件。
func uploadAvatarForTest(t *testing.T, filename string, content []byte, token string) *httptest.ResponseRecorder {
	t.Helper()

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("avatar", filename)
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := part.Write(content); err != nil {
		t.Fatalf("write form file: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/avatar", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	newProtectedRouter().ServeHTTP(rec, req)
	return rec
}

// I-1：.webp 头像要核对文件头 RIFF????WEBP，内容不符的直接拒，不落盘；真 webp 头照常接受。
func TestUploadAvatarRejectsWebpWithoutWebpHeader(t *testing.T) {
	testutil.SetupTestEnv(t)

	user := testutil.MustCreateUser(t, "avatar-webp-user", "operator")
	token := testutil.MustCreateAccessToken(t, user.Username, user.Role)
	avatarDir := filepath.Join(config.C.Data.Dir, "avatars")

	for name, content := range map[string][]byte{
		"plain text":    []byte("<html><script>alert(1)</script></html>"),
		"too short":     []byte("RIFF"),
		"riff not webp": []byte("RIFF\x24\x00\x00\x00WAVEfmt "),
	} {
		rec := uploadAvatarForTest(t, "avatar.webp", content, token)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: expected 400, got %d body=%s", name, rec.Code, rec.Body.String())
		}
		if entries, _ := os.ReadDir(avatarDir); len(entries) != 0 {
			t.Fatalf("%s: rejected upload must not leave a file behind, found %d", name, len(entries))
		}
	}

	valid := append([]byte("RIFF\x24\x00\x00\x00WEBPVP8 "), make([]byte, 16)...)
	rec := uploadAvatarForTest(t, "avatar.webp", valid, token)
	if rec.Code != http.StatusOK {
		t.Fatalf("webp with a valid header: expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	if entries, _ := os.ReadDir(avatarDir); len(entries) != 1 {
		t.Fatalf("expected the valid webp to be saved, found %d files", len(entries))
	}
}
