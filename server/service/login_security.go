package service

import (
	"fmt"
	"time"

	"daidai-panel/database"
	"daidai-panel/model"

	"gorm.io/gorm"
)

const (
	MaxLoginAttempts = 5
	LockDuration     = 15 * time.Minute
)

func RecordLoginLog(userID uint, username, ip, userAgent string, status int, message string) {
	log := model.LoginLog{
		UserID:    userID,
		Username:  username,
		IP:        ip,
		UserAgent: userAgent,
		Status:    status,
		Message:   message,
	}
	database.DB.Create(&log)
}

func CheckLoginLock(ip, username string) (bool, time.Duration) {
	var attempt model.LoginAttempt
	err := database.DB.Where("ip = ? AND username = ?", ip, username).
		Take(&attempt).Error

	if err != nil {
		return false, 0
	}

	if attempt.Count >= MaxLoginAttempts && attempt.LockedAt != nil {
		remaining := attempt.ExpiresAt.Sub(time.Now())
		if remaining > 0 {
			return true, remaining
		}
	}

	return false, 0
}

func RecordFailedLogin(ip, username string) int {
	var attempt model.LoginAttempt
	err := database.DB.Where("ip = ? AND username = ?", ip, username).
		Take(&attempt).Error

	if err != nil {
		if err == gorm.ErrRecordNotFound {
			attempt = model.LoginAttempt{
				IP:        ip,
				Username:  username,
				Count:     1,
				ExpiresAt: time.Now().Add(LockDuration),
			}
			database.DB.Create(&attempt)
			return 1
		}
		return 0
	}

	attempt.Count++
	if attempt.Count >= MaxLoginAttempts {
		now := time.Now()
		attempt.LockedAt = &now
		lockTimes := attempt.Count - MaxLoginAttempts + 1
		attempt.ExpiresAt = now.Add(time.Duration(lockTimes) * LockDuration)
	}
	database.DB.Save(&attempt)
	return attempt.Count
}

func ClearLoginAttempts(ip, username string) {
	database.DB.Where("ip = ? AND username = ?", ip, username).Delete(&model.LoginAttempt{})
}

func CleanExpiredAttempts() {
	database.DB.Where("expires_at < ?", time.Now()).Delete(&model.LoginAttempt{})
}

func CreateSession(userID uint, username, jti, ip, userAgent string, expiresAt time.Time) {
	session := model.UserSession{
		UserID:    userID,
		Username:  username,
		JTI:       jti,
		IP:        ip,
		UserAgent: userAgent,
		ExpiresAt: expiresAt,
	}
	database.DB.Create(&session)
}

func RevokeSession(jti string) {
	database.DB.Where("jti = ?", jti).Delete(&model.UserSession{})
}

func RevokeAllUserSessions(userID uint) int64 {
	result := database.DB.Where("user_id = ?", userID).Delete(&model.UserSession{})
	return result.RowsAffected
}

func CleanExpiredSessions() {
	database.DB.Where("expires_at < ?", time.Now()).Delete(&model.UserSession{})
}

func IsIPWhitelisted(ip string) bool {
	var count int64
	database.DB.Model(&model.IPWhitelist{}).Count(&count)
	if count == 0 {
		return true
	}

	var match int64
	database.DB.Model(&model.IPWhitelist{}).Where("ip = ?", ip).Count(&match)
	return match > 0
}

func RecordSecurityAudit(userID *uint, username, action, detail, ip string) {
	audit := model.SecurityAudit{
		UserID:   userID,
		Username: username,
		Action:   action,
		Detail:   detail,
		IP:       ip,
	}
	database.DB.Create(&audit)
}

func GetLoginStats(days int) map[string]interface{} {
	since := time.Now().AddDate(0, 0, -days)

	var totalLogins int64
	database.DB.Model(&model.LoginLog{}).Where("created_at > ?", since).Count(&totalLogins)

	var successLogins int64
	database.DB.Model(&model.LoginLog{}).Where("created_at > ? AND status = 0", since).Count(&successLogins)

	var failedLogins int64
	database.DB.Model(&model.LoginLog{}).Where("created_at > ? AND status = 1", since).Count(&failedLogins)

	var activeSessions int64
	database.DB.Model(&model.UserSession{}).Where("expires_at > ?", time.Now()).Count(&activeSessions)

	var lockedAccounts int64
	database.DB.Model(&model.LoginAttempt{}).
		Where("count >= ? AND expires_at > ?", MaxLoginAttempts, time.Now()).Count(&lockedAccounts)

	return map[string]interface{}{
		"total_logins":    totalLogins,
		"success_logins":  successLogins,
		"failed_logins":   failedLogins,
		"active_sessions": activeSessions,
		"locked_accounts": lockedAccounts,
		"period_days":     days,
	}
}

func IsSuspiciousLogin(ip, username string) (bool, string) {
	var lastLog model.LoginLog
	err := database.DB.Where("username = ? AND status = 0", username).
		Order("created_at DESC").Take(&lastLog).Error

	if err != nil {
		return false, ""
	}

	if lastLog.IP != ip {
		return true, fmt.Sprintf("IP 从 %s 变更为 %s", lastLog.IP, ip)
	}

	return false, ""
}
