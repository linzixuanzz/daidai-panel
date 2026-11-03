package service

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"daidai-panel/database"
	"daidai-panel/model"
	"daidai-panel/testutil"
)

// 登录日志与失败计数里的超长字段按字符截断，不切坏中文。
func TestLoginRecordsTruncateOversizedFields(t *testing.T) {
	testutil.SetupTestEnv(t)

	longName := strings.Repeat("用", 300)
	longUA := strings.Repeat("u", 2000)
	RecordLoginLog(0, longName, "203.0.113.1", strings.Repeat("客", 300), longUA, 1, "登录失败")

	var logRow model.LoginLog
	if err := database.DB.Order("id DESC").First(&logRow).Error; err != nil {
		t.Fatalf("load login log: %v", err)
	}
	if got := utf8.RuneCountInString(logRow.Username); got != 128 || !utf8.ValidString(logRow.Username) {
		t.Fatalf("expected username truncated to 128 runes, got %d (valid=%v)", got, utf8.ValidString(logRow.Username))
	}
	if got := utf8.RuneCountInString(logRow.ClientName); got != 128 {
		t.Fatalf("expected client name truncated to 128 runes, got %d", got)
	}
	if got := utf8.RuneCountInString(logRow.UserAgent); got != 512 {
		t.Fatalf("expected UA truncated to 512 runes, got %d", got)
	}

	// 失败计数：写入和查询用同一个截断口径，超长用户名照样能累计、能锁定、能清除
	for i := 0; i < MaxLoginAttempts; i++ {
		RecordFailedLogin("203.0.113.1", longName)
	}
	if got := GetLoginAttemptCount("203.0.113.1", longName); got != MaxLoginAttempts {
		t.Fatalf("expected %d attempts for long username, got %d", MaxLoginAttempts, got)
	}
	if locked, _ := CheckLoginLock("203.0.113.1", longName); !locked {
		t.Fatalf("expected long username to be locked after %d failures", MaxLoginAttempts)
	}
	var attempt model.LoginAttempt
	database.DB.First(&attempt)
	if utf8.RuneCountInString(attempt.Username) != 128 {
		t.Fatalf("expected stored attempt username truncated to 128 runes, got %d", utf8.RuneCountInString(attempt.Username))
	}
	ClearLoginAttempts("203.0.113.1", longName)
	if got := GetLoginAttemptCount("203.0.113.1", longName); got != 0 {
		t.Fatalf("expected attempts cleared, got %d", got)
	}
}

// 周期清理只删过期超过 7 天的失败计数（过期不足 7 天的留着，渐进式锁定还要读 Count）与 90 天前的登录日志，其余不动。
func TestPeriodicCleanupRemovesExpiredAttemptsAndOldLoginLogs(t *testing.T) {
	testutil.SetupTestEnv(t)

	now := time.Now()
	database.DB.Create(&model.LoginAttempt{IP: "203.0.113.2", Username: "long-expired", Count: 1, ExpiresAt: now.Add(-LoginAttemptRetention - time.Hour)})
	database.DB.Create(&model.LoginAttempt{IP: "203.0.113.2", Username: "recently-expired", Count: 6, ExpiresAt: now.Add(-time.Hour)})
	database.DB.Create(&model.LoginAttempt{IP: "203.0.113.2", Username: "active", Count: 1, ExpiresAt: now.Add(time.Hour)})
	database.DB.Create(&model.LoginLog{Username: "old", Message: "登录成功", CreatedAt: now.AddDate(0, 0, -(LoginLogRetentionDays + 1))})
	database.DB.Create(&model.LoginLog{Username: "recent", Message: "登录成功", CreatedAt: now.AddDate(0, 0, -(LoginLogRetentionDays - 1))})

	runPeriodicCleanup()

	var attempts []model.LoginAttempt
	database.DB.Order("username ASC").Find(&attempts)
	if len(attempts) != 2 || attempts[0].Username != "active" || attempts[1].Username != "recently-expired" {
		t.Fatalf("expected only the attempt expired over 7 days to be removed, got %#v", attempts)
	}
	if attempts[1].Count != 6 {
		t.Fatalf("expected recently expired attempt to keep its count, got %d", attempts[1].Count)
	}
	var logs []model.LoginLog
	database.DB.Find(&logs)
	if len(logs) != 1 || logs[0].Username != "recent" {
		t.Fatalf("expected only the recent login log to remain, got %#v", logs)
	}
}
