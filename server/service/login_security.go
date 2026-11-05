package service

import (
	"fmt"
	"log"
	"strings"
	"time"
	"unicode/utf8"

	"sort"

	"daidai-panel/config"
	"daidai-panel/database"
	"daidai-panel/model"
	"daidai-panel/pkg/netutil"

	"gorm.io/gorm"
)

const (
	MaxLoginAttempts = 5
	LockDuration     = 15 * time.Minute

	// 登录日志 / 失败计数里用户名、客户端名、UA 的长度上限（按字符数）。
	// 这几项都来自免登录的 /auth/login 请求，原样落库就能被人用超长字段把表撑大。
	// 真实用户名最长 32 位，客户端名与 UA 正常也远小于这个长度，截断不影响排查。
	loginFieldMaxRunes     = 128
	loginUserAgentMaxRunes = 512

	// LoginLogRetentionDays：登录日志只留 90 天，周期清理里删更早的行。
	LoginLogRetentionDays = 90
)

// truncateRunes 按字符截断，不会把多字节的中文切成半个乱码。
func truncateRunes(value string, maxRunes int) string {
	if utf8.RuneCountInString(value) <= maxRunes {
		return value
	}
	runes := []rune(value)
	return string(runes[:maxRunes])
}

// loginAttemptUsername 是失败计数表里用户名的统一口径：写入和查询都要截成同一个值，
// 否则超长用户名写进去是截断后的，查的时候用原值就对不上。
func loginAttemptUsername(username string) string {
	return truncateRunes(username, loginFieldMaxRunes)
}

func RecordLoginLog(userID uint, username, ip, clientName, userAgent string, status int, message string) {
	RecordLoginLogWithMethod(userID, username, ip, clientName, userAgent, status, message, "")
}

// RecordLoginLogWithMethod 同 RecordLoginLog，额外写「登录方式」列（设置页登录日志的「登录方式」）。
// method 为空时不赋值，落库走列默认值「密码登录」，与 RecordLoginLog 原来的行为一致；通行密钥登录传「通行密钥」。
func RecordLoginLogWithMethod(userID uint, username, ip, clientName, userAgent string, status int, message, method string) {
	log := model.LoginLog{
		UserID:     userID,
		Username:   truncateRunes(username, loginFieldMaxRunes),
		IP:         ip,
		ClientName: truncateRunes(clientName, loginFieldMaxRunes),
		UserAgent:  truncateRunes(userAgent, loginUserAgentMaxRunes),
		Method:     method,
		Status:     status,
		Message:    message,
	}
	database.DB.Create(&log)
}

func CheckLoginLock(ip, username string) (bool, time.Duration) {
	username = loginAttemptUsername(username)
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

func GetLoginAttemptCount(ip, username string) int {
	username = loginAttemptUsername(username)
	var attempt model.LoginAttempt
	err := database.DB.Where("ip = ? AND username = ?", ip, username).
		Take(&attempt).Error
	if err != nil {
		return 0
	}
	return attempt.Count
}

func RecordFailedLogin(ip, username string) int {
	username = loginAttemptUsername(username)
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
	username = loginAttemptUsername(username)
	database.DB.Where("ip = ? AND username = ?", ip, username).Delete(&model.LoginAttempt{})
}

// LoginAttemptRetention：失败计数行过期后再保留这么久才删。
const LoginAttemptRetention = 7 * 24 * time.Hour

// CleanExpiredAttempts 删掉过期超过 LoginAttemptRetention 的失败计数行（由周期清理调用）。
// 不能锁一过期就删：锁定时长按 Count 递增（第几次被锁就锁几倍时长），
// 行一删 Count 就归零，解锁后接着猜的人又从 15 分钟起步，渐进式加长锁定就失效了。
func CleanExpiredAttempts() int64 {
	if database.DB == nil {
		return 0
	}
	cutoff := time.Now().Add(-LoginAttemptRetention)
	return database.DB.Where("expires_at < ?", cutoff).Delete(&model.LoginAttempt{}).RowsAffected
}

// CleanOldLoginLogs 删掉 LoginLogRetentionDays 天之前的登录日志（由周期清理调用）。
func CleanOldLoginLogs() int64 {
	if database.DB == nil {
		return 0
	}
	cutoff := time.Now().AddDate(0, 0, -LoginLogRetentionDays)
	return database.DB.Where("created_at < ?", cutoff).Delete(&model.LoginLog{}).RowsAffected
}

// CreateSessionWithRefresh 落一行登录会话。返回的 error 调用方必须处理：会话行落不下去就不能发 token，
// 否则签出的是「有会话号、没有会话行」的登录令牌——撤销全部会话、改密、禁用、删除都遍历不到它，永远撤销不掉。
func CreateSessionWithRefresh(userID uint, username, accessJTI, refreshJTI, clientType, clientName, ip, userAgent string, accessExpiresAt, refreshExpiresAt time.Time) error {
	var refreshExpiryPtr *time.Time
	if !refreshExpiresAt.IsZero() {
		refreshExpiryPtr = &refreshExpiresAt
	}

	normalizedClientType := NormalizeSessionClientType(clientType)
	configKey := "max_web_sessions"
	if normalizedClientType == SessionClientApp {
		configKey = "max_app_sessions"
	}
	maxSessions := model.GetConfigInt(configKey, 1)
	if maxSessions < 1 {
		maxSessions = 1
	}
	revokeExcessSessionsByClientType(userID, normalizedClientType, maxSessions)

	session := model.UserSession{
		UserID:           userID,
		Username:         username,
		JTI:              accessJTI,
		RefreshJTI:       refreshJTI,
		ClientType:       normalizedClientType,
		ClientName:       clientName,
		IP:               ip,
		UserAgent:        userAgent,
		ExpiresAt:        accessExpiresAt,
		RefreshExpiresAt: refreshExpiryPtr,
	}
	return database.DB.Create(&session).Error
}

func CreateSession(userID uint, username, jti, clientType, clientName, ip, userAgent string, expiresAt time.Time) error {
	return CreateSessionWithRefresh(userID, username, jti, "", clientType, clientName, ip, userAgent, expiresAt, time.Time{})
}

func effectiveSessionClientType(session model.UserSession) string {
	return DetectSessionClientType(session.ClientType, "", session.UserAgent)
}

func revokeExcessSessionsByClientType(userID uint, clientType string, maxSessions int) int64 {
	targetType := NormalizeSessionClientType(clientType)
	if maxSessions < 1 {
		maxSessions = 1
	}

	var sessions []model.UserSession
	database.DB.Where("user_id = ?", userID).Find(&sessions)

	var matched []model.UserSession
	for i := range sessions {
		if effectiveSessionClientType(sessions[i]) != targetType {
			continue
		}
		matched = append(matched, sessions[i])
	}

	// keep (maxSessions - 1) newest, revoke the rest (the new session will fill the last slot)
	keepCount := maxSessions - 1
	if len(matched) <= keepCount {
		return 0
	}

	sort.Slice(matched, func(i, j int) bool {
		return matched[i].CreatedAt.After(matched[j].CreatedAt)
	})

	toRevoke := matched[keepCount:]
	var ids []uint
	for i := range toRevoke {
		BlockSessionTokens(&toRevoke[i])
		ids = append(ids, toRevoke[i].ID)
	}

	if len(ids) == 0 {
		return 0
	}

	result := database.DB.Delete(&model.UserSession{}, ids)
	return result.RowsAffected
}

func blockToken(jti, tokenType string, userID *uint, expiresAt time.Time) {
	if jti == "" {
		return
	}
	if expiresAt.IsZero() {
		expiresAt = time.Now().Add(24 * time.Hour)
	}

	var existing model.TokenBlocklist
	if err := database.DB.Where("jti = ?", jti).First(&existing).Error; err == nil {
		// 同一个 jti 已经拉黑过：到期只延不缩，守住「黑名单行的到期 ≥ 它拦截的每一枚 token 的到期」。
		// 后写的那次不一定更长（比如退出的兜底只按令牌自己的到期算），也不一定更短，
		// 所以既不能直接覆盖（缩短了会被清理任务提前删掉），也不能像以前那样一律跳过。
		if existing.ExpiresAt.Before(expiresAt) {
			database.DB.Model(&existing).Update("expires_at", expiresAt)
		}
		return
	}

	database.DB.Create(&model.TokenBlocklist{
		JTI:       jti,
		TokenType: tokenType,
		UserID:    userID,
		RevokedAt: time.Now(),
		ExpiresAt: expiresAt,
	})
}

// BlockSessionTokens 把一个登录会话拉黑：会话号一行（拦住这个会话签出的所有 access）+ refresh 一行。
//
// 不变式：每条黑名单行的到期 ≥ 它拦截的每一枚 token 的到期。黑名单行过期就会被 6 小时一次的清理删掉，
// 早于 token 到期删掉，这枚 token 就复活了。
func BlockSessionTokens(session *model.UserSession) {
	if session == nil {
		return
	}
	userID := session.UserID
	// 会话号行必须活到这个会话最后一枚 access 自然过期。会话的 access 只能在 refresh 到期前签出，
	// 所以「refresh 到期 + access 有效期」是结构上界，与撤销和续期谁先谁后无关；
	// 再与会话记录的最晚 access 到期取大，兜住「签发之后管理员调短了有效期」。
	accessExpiry := session.ExpiresAt
	if session.RefreshExpiresAt != nil {
		if bound := session.RefreshExpiresAt.Add(config.C.JWT.AccessTokenExpire); bound.After(accessExpiry) {
			accessExpiry = bound
		}
	}
	blockToken(session.JTI, "access", &userID, accessExpiry)
	if session.RefreshExpiresAt != nil {
		blockToken(session.RefreshJTI, "refresh", &userID, *session.RefreshExpiresAt)
	}
}

// RevokeSession 按会话号撤销整个登录会话（退出登录用）：用续期得来的 token 退出，
// 同一会话的其它 access 与 refresh 也一起作废。tokenExpiresAt 是发起退出的那枚 token 的到期。
func RevokeSession(sessionID string, tokenExpiresAt time.Time) {
	var session model.UserSession
	if err := database.DB.Where("jti = ?", sessionID).First(&session).Error; err == nil {
		BlockSessionTokens(&session)
		database.DB.Delete(&session)
		return
	}

	// 会话行已经不在（已被撤销或清理）：直接拉黑会话号。
	// 🔴 到期不能写死 24 小时（那正是「退出后清理任务一跑、token 复活」的根因），至少盖住这枚 token 自己的到期。
	expiresAt := time.Now().Add(config.C.JWT.AccessTokenExpire)
	if tokenExpiresAt.After(expiresAt) {
		expiresAt = tokenExpiresAt
	}
	blockToken(sessionID, "access", nil, expiresAt)
}

// RevokeAllUserSessions 撤销该用户全部登录会话（含每个会话续期出的 access）。
//
// 🔴 硬约束：任何用户状态变更（改密、重置密码、改角色、禁用、删除、改用户名）都必须调它，命令行也不例外。
// 登录令牌每请求只查黑名单、不查用户表（单连接数据库不能再加查询），漏调一处，
// 被禁用 / 删除 / 降权的人手里的令牌就还能一直用到自然过期。
func RevokeAllUserSessions(userID uint) int64 {
	var sessions []model.UserSession
	database.DB.Where("user_id = ?", userID).Find(&sessions)
	var ids []uint
	for i := range sessions {
		BlockSessionTokens(&sessions[i])
		ids = append(ids, sessions[i].ID)
	}

	// 只删已经拉黑过的这几行，不能按 user_id 整批删：查完之后才落库的新会话行（并发登录）没进黑名单，
	// 整批删会把它一起删掉，之后任何撤销都遍历不到它，它签出的令牌就再也撤不掉了。留着它，下一次撤销还能找到。
	if len(ids) == 0 {
		return 0
	}
	result := database.DB.Delete(&model.UserSession{}, ids)
	return result.RowsAffected
}

// RevokeOtherUserSessions 撤销该用户除当前会话之外的所有登录会话。
// currentSessionID 是会话号（JWTAuth 设的 sid），不是 token 的 jti：
// 用续期得来的 token 发起时按 jti 排除会连自己的会话一起撤掉，当场把自己踢下线。
func RevokeOtherUserSessions(userID uint, currentSessionID string) int64 {
	var sessions []model.UserSession
	database.DB.Where("user_id = ? AND jti != ?", userID, currentSessionID).Find(&sessions)
	var ids []uint
	for i := range sessions {
		BlockSessionTokens(&sessions[i])
		ids = append(ids, sessions[i].ID)
	}

	// 同 RevokeAllUserSessions：只删拉黑过的这几行，查完之后才落库的新会话不能被顺手删掉
	if len(ids) == 0 {
		return 0
	}
	result := database.DB.Delete(&model.UserSession{}, ids)
	return result.RowsAffected
}

// CleanExpiredSessions 只删「最晚 access 与 refresh 都已过期」的会话行。
// 🔴 不能只看 refresh：refresh 过期后，它之前续期出的 access 还能用最多一个 access 有效期。
// 那时删掉会话行，改密、禁用、删除遍历会话时就找不到它，那枚 access 就漏拉黑了。
func CleanExpiredSessions() {
	now := time.Now()
	database.DB.Where("expires_at < ? AND (refresh_expires_at IS NULL OR refresh_expires_at < ?)", now, now).Delete(&model.UserSession{})
}

// CleanExpiredTokenBlocklist 删除 expires_at 已过的黑名单行。
//
// 黑名单只在「token 本身还没到期、但要提前作废」这段窗口里有意义：expires_at 一过，
// JWT 校验自己就会因为 exp 过期而拒绝这枚 token，黑名单行留着不再改变任何鉴权结果，
// 只是磁盘与运维负担。而每跑一次任务 / 一次 ddp 交互会话 / 一次调试运行都会写一行，
// 不清理就是无界增长。
//
// 返回删除行数；database.DB 未初始化或删除失败时返回 0 与对应错误。
func CleanExpiredTokenBlocklist() (int64, error) {
	if database.DB == nil {
		return 0, nil
	}

	result := database.DB.Where("expires_at < ?", time.Now()).Delete(&model.TokenBlocklist{})
	if result.Error != nil {
		return 0, result.Error
	}
	return result.RowsAffected, nil
}

func IsIPWhitelisted(ip string) bool {
	ip = strings.TrimSpace(ip)
	if ip == "" {
		return false
	}

	var whitelist []model.IPWhitelist
	database.DB.Order("id ASC").Find(&whitelist)
	if len(whitelist) == 0 {
		return true
	}

	for _, entry := range whitelist {
		if netutil.MatchIPWhitelistEntry(entry.IP, ip) {
			return true
		}
	}

	return false
}

// 安全审计的 action 取值。英文标识落库、长期稳定（网页按它筛选并翻成中文，见 web 的 AuditLogsCard.vue），
// 已经发出去的标识不要改名；新增操作就新增一个，并同步网页的中文映射与 apiData.ts 的取值说明。
const (
	AuditPasswordChange     = "password.change"
	AuditUsernameChange     = "username.change"
	AuditTwoFactorEnable    = "2fa.enable"
	AuditTwoFactorDisable   = "2fa.disable"
	AuditPasskeyAdd         = "passkey.add"
	AuditPasskeyDelete      = "passkey.delete"
	AuditUserCreate         = "user.create"
	AuditUserUpdate         = "user.update"
	AuditUserDelete         = "user.delete"
	AuditUserPasswordReset  = "user.password_reset"
	AuditBackupCreate       = "backup.create"
	AuditBackupUpload       = "backup.upload"
	AuditBackupDownload     = "backup.download"
	AuditBackupRestore      = "backup.restore"
	AuditBackupDelete       = "backup.delete"
	AuditConsoleRun         = "console.run"
	AuditConfigUpdate       = "config.update"
	AuditConfigDelete       = "config.delete"
	AuditConfigScriptUpdate = "config_script.update"
	AuditOpenAppCreate      = "open_app.create"
	AuditOpenAppDelete      = "open_app.delete"
	AuditOpenAppResetSecret = "open_app.reset_secret"
	AuditOpenAppScopeChange = "open_app.scope_change"
	AuditIPWhitelistAdd     = "ip_whitelist.add"
	AuditIPWhitelistRemove  = "ip_whitelist.remove"
	AuditLoginLogsClear     = "login_logs.clear"
	AuditSSHKeyCreate       = "ssh_key.create"
	AuditSSHKeyDelete       = "ssh_key.delete"
	AuditSystemUpdate       = "system.update"
	AuditSystemRestart      = "system.restart"
	AuditSystemStop         = "system.stop"
	AuditProcessKill        = "process.kill"
)

// 审计字段的长度上限（按字符数），与 security_audits 表的列宽一致。
// 应用名、配置键列表、命令这类内容长度不可控，超长就截断，宁可少记几个字也不能让写入失败。
const (
	auditUsernameMaxRunes = 64
	auditDetailMaxRunes   = 512
)

// RecordSecurityAudit 写一条安全审计。调用方在敏感操作「成功之后」调用；
// 写入失败只打日志、不返回错误——审计是旁路留痕，不能因为它把已经完成的主操作变成失败。
func RecordSecurityAudit(userID *uint, username, action, detail, ip string) {
	if database.DB == nil {
		return
	}
	audit := model.SecurityAudit{
		UserID:   userID,
		Username: truncateRunes(username, auditUsernameMaxRunes),
		Action:   action,
		Detail:   truncateRunes(detail, auditDetailMaxRunes),
		IP:       ip,
	}
	if err := database.DB.Create(&audit).Error; err != nil {
		log.Printf("warn: 写安全审计失败（action=%s）: %v", action, err)
	}
}

func GetLoginStats(days int) map[string]interface{} {
	since := time.Now().AddDate(0, 0, -days)

	var totalLogins int64
	database.DB.Model(&model.LoginLog{}).Where("created_at > ?", since).Count(&totalLogins)

	var successLogins int64
	database.DB.Model(&model.LoginLog{}).Where("created_at > ? AND status = 0", since).Count(&successLogins)

	var failedLogins int64
	database.DB.Model(&model.LoginLog{}).Where("created_at > ? AND status = 1", since).Count(&failedLogins)

	// 活跃会话 = access 或 refresh 仍有效（与会话管理列表同一口径）：登录超过 access 有效期、还能续期的也算
	now := time.Now()
	var activeSessions int64
	database.DB.Model(&model.UserSession{}).
		Where("expires_at > ? OR (refresh_expires_at IS NOT NULL AND refresh_expires_at > ?)", now, now).
		Count(&activeSessions)

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
