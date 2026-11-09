package service

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"

	"daidai-panel/database"
	"daidai-panel/model"

	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const (
	TOTPDigits = 6
	TOTPPeriod = 30
	TOTPIssuer = "DaiDaiPanel"
)

func silentDB() *gorm.DB {
	return database.DB.Session(&gorm.Session{Logger: database.DB.Logger.LogMode(logger.Silent)})
}

func GenerateTOTPSecret() string {
	secret := make([]byte, 20)
	rand.Read(secret)
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(secret)
}

func GenerateTOTPURI(username, secret string) string {
	return fmt.Sprintf("otpauth://totp/%s:%s?secret=%s&issuer=%s&digits=%d&period=%d",
		TOTPIssuer, username, secret, TOTPIssuer, TOTPDigits, TOTPPeriod)
}

// GenerateCurrentTOTPForTest returns a currently-valid 6-digit TOTP code for
// the supplied base32 secret. Exported so end-to-end tests can exercise the
// 2FA flows without re-implementing the HMAC-SHA1 code generation.
func GenerateCurrentTOTPForTest(secret string) string {
	counter := uint64(time.Now().Unix() / int64(TOTPPeriod))
	return generateTOTPCode(secret, counter)
}

// GenerateNextTOTPForTest 返回「下一个时间步」的验证码，等价于用户等验证器跳到下一个码再输入。
// 同一个时间步的码成功用过一次就不能再用（防重放），测试里要「再验证一次」时用它，
// 不用去清进程内的防重放记录，生产校验口径保持不变。下一个时间步仍在 ±1 窗口内，校验照样能过。
func GenerateNextTOTPForTest(secret string) string {
	counter := uint64(time.Now().Unix()/int64(TOTPPeriod) + 1)
	return generateTOTPCode(secret, counter)
}

// ValidateTOTP 是纯校验：只看 code 在不在 ±1 窗口里，不记、也不查「用过没有」。
// 保留给只需要判断码对不对的调用方与测试；校验用户的码必须走 ValidateUserTOTP / VerifyAndEnableTwoFactor。
func ValidateTOTP(secret, code string) bool {
	_, ok := matchTOTPStep(secret, code, math.MinInt64)
	return ok
}

// matchTOTPStep 在 ±1 窗口里找与 code 匹配、且时间步大于 after 的那一步，返回该时间步。
// 三个窗口都算完、用常量时间比较，不因为在第几个窗口命中而提前返回，避免按耗时猜码。
func matchTOTPStep(secret, code string, after int64) (int64, bool) {
	now := time.Now().Unix() / int64(TOTPPeriod)
	var matched int64
	found := false
	for _, offset := range []int64{-1, 0, 1} {
		step := now + offset
		generated := generateTOTPCode(secret, uint64(step))
		// 密钥解不开时 generateTOTPCode 返回空串，空串和空串比也算「相等」，这里必须排除，不能让空码通过
		if generated == "" {
			continue
		}
		// 已经用过（step <= after）的时间步即使码对也不算：极小概率下相邻两步的码相同，
		// 这时继续往后找，让下一个时间步的同一个码仍能正常使用
		if subtle.ConstantTimeCompare([]byte(generated), []byte(code)) == 1 && step > after && !found {
			matched, found = step, true
		}
	}
	return matched, found
}

// totpReplay 记每个用户最后一次成功使用的 TOTP 时间步，用来拒绝同一个码在 90 秒窗口内被重复使用。
// 只放进程内、不落库：重启后清空最多放过一次「重启前刚用过的码」，换来不加表不加列、不碰备份恢复的格式。
var totpReplay = struct {
	mu       sync.Mutex
	lastUsed map[uint]int64
}{lastUsed: make(map[uint]int64)}

// consumeUserTOTP 校验 code 并把匹配的时间步记为已用。校验与记录在同一把锁里完成：
// 两个并发请求拿同一个码来，只会有一个看到「还没用过」，另一个必然被拒。
func consumeUserTOTP(userID uint, secret, code string) bool {
	totpReplay.mu.Lock()
	defer totpReplay.mu.Unlock()

	step, ok := matchTOTPStep(secret, code, totpReplay.lastUsed[userID])
	if !ok {
		return false
	}
	totpReplay.lastUsed[userID] = step
	return true
}

// clearUserTOTPReplay 删掉该用户的已用记录：重新设置或关闭 2FA 后密钥会变，旧的时间步记录没有意义了。
func clearUserTOTPReplay(userID uint) {
	totpReplay.mu.Lock()
	delete(totpReplay.lastUsed, userID)
	totpReplay.mu.Unlock()
}

func generateTOTPCode(secret string, counter uint64) string {
	secretBytes, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.ToUpper(secret))
	if err != nil {
		return ""
	}

	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, counter)

	mac := hmac.New(sha1.New, secretBytes)
	mac.Write(buf)
	hash := mac.Sum(nil)

	offset := hash[len(hash)-1] & 0x0f
	truncated := binary.BigEndian.Uint32(hash[offset:offset+4]) & 0x7fffffff

	code := truncated % 1000000
	return fmt.Sprintf("%06d", code)
}

func SetupTwoFactor(userID uint) (string, string, error) {
	secret := GenerateTOTPSecret()

	var existing model.TwoFactorAuth
	err := silentDB().Where("user_id = ?", userID).First(&existing).Error
	if err == nil {
		// 这条分支才是「重新设置 2FA」的常走路径（库里已经有 two_factor_auths 行）。
		// 和下面的 Create 一样必须接 .Error：写库失败时如果照样往下走，
		// 函数会把**新** secret 和二维码返回给前端，而库里存的还是**旧** secret，
		// 用户扫完码输入的验证码永远对不上，且没有任何线索能看出发生了什么。
		if err := database.DB.Model(&existing).Updates(map[string]interface{}{
			"secret":  secret,
			"enabled": false,
		}).Error; err != nil {
			return "", "", err
		}
	} else {
		tfa := model.TwoFactorAuth{
			UserID:  userID,
			Secret:  secret,
			Enabled: false,
		}
		// 原来这行没接 .Error：two_factor_auths.user_id 是唯一索引，重复/并发的开启请求
		// 会撞唯一约束而插入失败，却被静默吞掉 —— 用户扫完二维码怎么都验证不过，
		// 因为库里压根没有这条 secret。这里把错误抛给 handler，由它统一返回「设置 2FA 失败」。
		if err := database.DB.Create(&tfa).Error; err != nil {
			return "", "", err
		}
	}
	// 换了新密钥，上一把密钥留下的已用时间步不再适用；不清的话，启用时输入的第一个码可能被误判成重放
	clearUserTOTPReplay(userID)

	var user model.User
	database.DB.First(&user, userID)
	uri := GenerateTOTPURI(user.Username, secret)

	return secret, uri, nil
}

func VerifyAndEnableTwoFactor(userID uint, code string) error {
	var tfa model.TwoFactorAuth
	if err := silentDB().Where("user_id = ?", userID).First(&tfa).Error; err != nil {
		return fmt.Errorf("2FA 尚未设置")
	}

	// 启用时用掉的码同样记为已用：否则偷看到这个码的人能立刻拿它再去登录一次
	if !consumeUserTOTP(userID, tfa.Secret, code) {
		return fmt.Errorf("验证码无效")
	}

	database.DB.Model(&tfa).Update("enabled", true)
	return nil
}

func DisableTwoFactor(userID uint) {
	database.DB.Where("user_id = ?", userID).Delete(&model.TwoFactorAuth{})
	clearUserTOTPReplay(userID)
}

func IsTwoFactorEnabled(userID uint) bool {
	var tfa model.TwoFactorAuth
	err := silentDB().Where("user_id = ? AND enabled = ?", userID, true).First(&tfa).Error
	return err == nil
}

func ValidateUserTOTP(userID uint, code string) bool {
	var tfa model.TwoFactorAuth
	if err := silentDB().Where("user_id = ? AND enabled = ?", userID, true).First(&tfa).Error; err != nil {
		return false
	}
	// 登录、关闭 2FA、添加通行密钥都走这里，共用同一份已用记录：一个码在哪条路径上用过，其它路径也不能再用
	return consumeUserTOTP(userID, tfa.Secret, code)
}
