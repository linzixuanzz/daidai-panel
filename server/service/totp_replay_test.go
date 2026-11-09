package service

import (
	"sync"
	"testing"

	"daidai-panel/database"
	"daidai-panel/model"
	"daidai-panel/pkg/crypto"
	"daidai-panel/testutil"

	"golang.org/x/crypto/bcrypt"
)

// enableTOTPForReplayTest 设置并直接在库里打开 2FA，不经过 VerifyAndEnableTwoFactor，
// 这样不会提前用掉当前时间步，用例自己决定第一个码用在哪。
func enableTOTPForReplayTest(t *testing.T, userID uint) string {
	t.Helper()
	secret, _, err := SetupTwoFactor(userID)
	if err != nil {
		t.Fatalf("setup 2fa: %v", err)
	}
	if err := database.DB.Model(&model.TwoFactorAuth{}).Where("user_id = ?", userID).Update("enabled", true).Error; err != nil {
		t.Fatalf("enable 2fa: %v", err)
	}
	return secret
}

// M-3：同一个码成功用过一次就不能再用；下一个时间步的新码照常可用，用过后同样作废。
func TestValidateUserTOTPRejectsReplayAndAcceptsNextStep(t *testing.T) {
	testutil.SetupTestEnv(t)
	user := testutil.MustCreateUser(t, "totp-replay-user", "admin")
	secret := enableTOTPForReplayTest(t, user.ID)

	code := GenerateCurrentTOTPForTest(secret)
	if !ValidateUserTOTP(user.ID, code) {
		t.Fatalf("first use of a fresh code should pass")
	}
	if ValidateUserTOTP(user.ID, code) {
		t.Fatalf("the same code must be rejected the second time")
	}

	next := GenerateNextTOTPForTest(secret)
	if !ValidateUserTOTP(user.ID, next) {
		t.Fatalf("the next time step's code should still pass")
	}
	if ValidateUserTOTP(user.ID, next) {
		t.Fatalf("the next code must also be single-use")
	}
}

// 启用 2FA 时输入的那个码同样记为已用，不能立刻再拿去过登录 / 关闭 2FA 的校验。
func TestVerifyAndEnableTwoFactorConsumesCode(t *testing.T) {
	testutil.SetupTestEnv(t)
	user := testutil.MustCreateUser(t, "totp-enable-consume", "admin")
	secret, _, err := SetupTwoFactor(user.ID)
	if err != nil {
		t.Fatalf("setup 2fa: %v", err)
	}

	code := GenerateCurrentTOTPForTest(secret)
	if err := VerifyAndEnableTwoFactor(user.ID, code); err != nil {
		t.Fatalf("enable 2fa: %v", err)
	}
	if ValidateUserTOTP(user.ID, code) {
		t.Fatalf("the code used to enable 2FA must not be accepted again")
	}
}

// 重新设置 2FA 会换密钥，必须清掉旧的已用记录：否则新密钥当前时间步的码会被当成重放，用户扫完码永远启用不了。
func TestSetupTwoFactorClearsReplayRecord(t *testing.T) {
	testutil.SetupTestEnv(t)
	user := testutil.MustCreateUser(t, "totp-resetup", "admin")
	secret := enableTOTPForReplayTest(t, user.ID)
	if !ValidateUserTOTP(user.ID, GenerateNextTOTPForTest(secret)) {
		t.Fatalf("expected next-step code to pass")
	}

	newSecret, _, err := SetupTwoFactor(user.ID)
	if err != nil {
		t.Fatalf("re-setup 2fa: %v", err)
	}
	if err := VerifyAndEnableTwoFactor(user.ID, GenerateCurrentTOTPForTest(newSecret)); err != nil {
		t.Fatalf("enabling with the new secret's current code should pass after re-setup: %v", err)
	}
}

// 并发拿同一个码来校验，只能有一个成功（校验与记录在同一把锁里）。
func TestValidateUserTOTPConcurrentSameCodeOnlyOnceSucceeds(t *testing.T) {
	testutil.SetupTestEnv(t)
	user := testutil.MustCreateUser(t, "totp-concurrent", "admin")
	secret := enableTOTPForReplayTest(t, user.ID)
	code := GenerateCurrentTOTPForTest(secret)

	const workers = 8
	var wg sync.WaitGroup
	var mu sync.Mutex
	passed := 0
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if ValidateUserTOTP(user.ID, code) {
				mu.Lock()
				passed++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	if passed != 1 {
		t.Fatalf("expected exactly one concurrent use to pass, got %d", passed)
	}
}

// ValidateTOTP 仍是纯函数：不记已用，同一个码反复校验都通过；空码与解不开的密钥一律不通过。
func TestValidateTOTPStaysPure(t *testing.T) {
	secret := GenerateTOTPSecret()
	code := GenerateCurrentTOTPForTest(secret)
	if !ValidateTOTP(secret, code) || !ValidateTOTP(secret, code) {
		t.Fatalf("pure ValidateTOTP should accept the same valid code repeatedly")
	}
	if ValidateTOTP(secret, "") {
		t.Fatalf("empty code must not pass")
	}
	if ValidateTOTP("!!not-base32!!", "") {
		t.Fatalf("an undecodable secret with an empty code must not pass")
	}
}

// H-1：失败计数的用户名先去首尾空白再截断，「bob」「 bob」「bob\t」落在同一行。
func TestLoginAttemptUsernameTrimsWhitespace(t *testing.T) {
	testutil.SetupTestEnv(t)
	ip := "203.0.113.150"

	for _, name := range []string{"bob", " bob", "bob ", "\tbob", " bob \n"} {
		RecordFailedLogin(ip, name)
	}
	if got := GetLoginAttemptCount(ip, "bob"); got != 5 {
		t.Fatalf("expected all whitespace variants to share one counter, got %d", got)
	}
	for _, name := range []string{"bob", "bob ", "  bob"} {
		if locked, _ := CheckLoginLock(ip, name); !locked {
			t.Fatalf("expected %q to be locked once the trimmed name is locked", name)
		}
	}

	ClearLoginAttempts(ip, " bob ")
	if got := GetLoginAttemptCount(ip, "bob"); got != 0 {
		t.Fatalf("expected clearing with a padded name to clear the trimmed row, got %d", got)
	}
}

// L-1：用户不存在时也做一次 bcrypt，占位哈希的 cost 必须与真实密码哈希一致，耗时才对得上。
func TestLoginUnknownUserUsesSameBcryptCostPlaceholder(t *testing.T) {
	testutil.SetupTestEnv(t)

	if _, _, _, _, _, err := NewAuthService().Login("no-such-user", "whatever", ""); err != ErrUserNotFound {
		t.Fatalf("expected ErrUserNotFound, got %v", err)
	}
	if dummyPasswordHash == "" {
		t.Fatalf("expected the placeholder hash to be generated on the unknown-user path")
	}

	realHash, err := crypto.HashPassword("Password123!")
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	wantCost, err := bcrypt.Cost([]byte(realHash))
	if err != nil {
		t.Fatalf("read real cost: %v", err)
	}
	gotCost, err := bcrypt.Cost([]byte(dummyPasswordHash))
	if err != nil {
		t.Fatalf("read placeholder cost: %v", err)
	}
	if gotCost != wantCost {
		t.Fatalf("placeholder bcrypt cost %d != real cost %d", gotCost, wantCost)
	}
}

// L-1：禁用判断挪到密码校验之后——密码错回 ErrInvalidPassword，密码对才回 ErrUserDisabled。
func TestLoginDisabledUserChecksPasswordFirst(t *testing.T) {
	testutil.SetupTestEnv(t)
	password := "Password123!"
	user := testutil.MustCreateLoginUser(t, "disabled-order-user", "operator", password)
	if err := database.DB.Model(&model.User{}).Where("id = ?", user.ID).Update("enabled", false).Error; err != nil {
		t.Fatalf("disable user: %v", err)
	}

	svc := NewAuthService()
	if _, _, _, _, _, err := svc.Login(user.Username, "wrong-password", ""); err != ErrInvalidPassword {
		t.Fatalf("disabled user with wrong password: expected ErrInvalidPassword, got %v", err)
	}
	if _, _, _, _, _, err := svc.Login(user.Username, password, ""); err != ErrUserDisabled {
		t.Fatalf("disabled user with correct password: expected ErrUserDisabled, got %v", err)
	}
}
