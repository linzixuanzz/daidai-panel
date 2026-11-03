package service

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"daidai-panel/database"
	"daidai-panel/model"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
	"gorm.io/gorm"
)

// 通行密钥（WebAuthn，issue #175）。
//
// 只做网页端：浏览器只在安全上下文（HTTPS 域名，或 http://localhost）里提供 WebAuthn，RP ID 也必须是域名。
// 每次 begin 都按请求头 Origin 现建一个 webauthn.WebAuthn{RPID, RPOrigins:[origin]}，不要求 Origin 与 Host 一致
// （反代会改 Host）。安全性靠三层：finish 时库校验 clientData.origin 与 rpIdHash；数据库里这条凭据的 RPID 必须等于
// 本次会话的 RP ID；挑战会话是服务端内存里的一次性记录，客户端只拿到一个随机 session_id。

const (
	PasskeyPurposeLogin    = "login"
	PasskeyPurposeRegister = "register"

	// PasskeyMaxPerUser 每个账号最多添加的通行密钥数
	PasskeyMaxPerUser = 20

	passkeyRPDisplayName = "呆呆面板"
	passkeyDefaultName   = "通行密钥"
	passkeyNameMaxRunes  = 64
	passkeyCeremonyTTL   = 5 * time.Minute
	// 登录 begin 是免登录接口，登录挑战会话必须有总量上限，不能被刷爆内存。
	// 只算登录会话：注册会话要先登录、还按用户限额，不占这个名额。
	passkeyCeremonyLimit = 1000
	// 每个客户端 IP（IPv6 按 /64 前缀）最多同时挂着的登录挑战会话
	passkeyLoginCeremoniesPerIP = 10
	// 每个用户最多同时挂着的注册挑战会话
	passkeyRegisterCeremoniesPerUser = 5
)

var (
	ErrPasskeyUnsupportedOrigin = errors.New("当前访问地址不支持通行密钥，请通过 HTTPS 域名访问面板")
	ErrPasskeySessionExpired    = errors.New("通行密钥验证已过期，请重试")
	ErrPasskeyTooManySessions   = errors.New("请求过于频繁，请稍后再试")
	ErrPasskeyInvalid           = errors.New("通行密钥验证失败，请重试或改用密码登录")
	ErrPasskeyRegisterFailed    = errors.New("通行密钥添加失败，请重试")
	ErrPasskeyLimitReached      = errors.New("每个账号最多添加 20 个通行密钥")
	ErrPasskeyDuplicate         = errors.New("这个通行密钥已经添加过了")
	ErrPasskeyNotFound          = errors.New("通行密钥不存在")
)

// passkeyCeremony 是一次 begin 到 finish 之间留在服务端的挑战会话。
type passkeyCeremony struct {
	session   webauthn.SessionData
	rpID      string
	origin    string
	purpose   string
	userID    uint   // 注册时是谁在加；登录时为 0
	handle    []byte // 注册时这个用户的句柄；登录时为空
	limitKey  string // 按键限额用：登录是 "ip:<IP 或 /64 前缀>"，注册是 "user:<用户 ID>"
	seq       uint64 // 存入顺序，挤掉「最旧」时按它比（测试里时钟固定，过期时间分不出先后）
	expiresAt time.Time
}

var passkeyCeremonies = struct {
	sync.Mutex
	items map[string]*passkeyCeremony
	seq   uint64
}{items: map[string]*passkeyCeremony{}}

// passkeyLoginLimitKey 登录挑战会话的限额键：IPv4（含 IPv4 映射的 IPv6）取完整地址，IPv6 取 /64 前缀
// （一个家庭宽带通常就分到一整个 /64，按单个地址计等于不限）。解析不了就原样当键。
func passkeyLoginLimitKey(clientIP string) string {
	clientIP = strings.TrimSpace(clientIP)
	parsed := net.ParseIP(clientIP)
	if parsed == nil {
		return "ip:" + clientIP
	}
	if v4 := parsed.To4(); v4 != nil {
		return "ip:" + v4.String()
	}
	return "ip:" + parsed.Mask(net.CIDRMask(64, 128)).String() + "/64"
}

// passkeyRegisterLimitKey 注册挑战会话的限额键：按用户 ID
func passkeyRegisterLimitKey(userID uint) string {
	return fmt.Sprintf("user:%d", userID)
}

// passkeyNow 测试里替换它来模拟会话过期
var passkeyNow = time.Now

// passkeyUser 把面板用户包成 webauthn.User，只在一次 begin / finish 里临时用。
type passkeyUser struct {
	handle      []byte
	name        string
	credentials []webauthn.Credential
}

func (u *passkeyUser) WebAuthnID() []byte                         { return u.handle }
func (u *passkeyUser) WebAuthnName() string                       { return u.name }
func (u *passkeyUser) WebAuthnDisplayName() string                { return u.name }
func (u *passkeyUser) WebAuthnCredentials() []webauthn.Credential { return u.credentials }

// PasskeyRPFromOrigin 从请求头 Origin 推 RP ID：必须是 https://<域名>[:端口]，或 http://localhost[:端口]。
// 主机名转小写、去掉端口就是 RP ID。Origin 缺失、scheme 不对、主机名为空或是 IP 字面量，一律 ErrPasskeyUnsupportedOrigin。
func PasskeyRPFromOrigin(origin string) (string, error) {
	origin = strings.TrimSpace(origin)
	if origin == "" || origin == "null" {
		return "", ErrPasskeyUnsupportedOrigin
	}
	u, err := url.Parse(origin)
	if err != nil || u.User != nil || u.Opaque != "" || u.RawQuery != "" || u.Fragment != "" {
		return "", ErrPasskeyUnsupportedOrigin
	}
	// Origin 头本来就不带路径；带了就不是浏览器发的标准 Origin，直接拒
	if u.Path != "" {
		return "", ErrPasskeyUnsupportedOrigin
	}
	host := strings.ToLower(u.Hostname())
	if host == "" || net.ParseIP(host) != nil {
		return "", ErrPasskeyUnsupportedOrigin
	}
	switch strings.ToLower(u.Scheme) {
	case "https":
	case "http":
		// 浏览器只把 http://localhost 当安全上下文
		if host != "localhost" {
			return "", ErrPasskeyUnsupportedOrigin
		}
	default:
		return "", ErrPasskeyUnsupportedOrigin
	}
	if protocol.ValidateRPID(host) != nil {
		return "", ErrPasskeyUnsupportedOrigin
	}
	return host, nil
}

// newPasskeyWebAuthn 按本次请求的 RP ID 与来源现建一个实例：只认这一个来源。
func newPasskeyWebAuthn(rpID, origin string) (*webauthn.WebAuthn, error) {
	return webauthn.New(&webauthn.Config{
		RPID:                  rpID,
		RPDisplayName:         passkeyRPDisplayName,
		RPOrigins:             []string{origin},
		AttestationPreference: protocol.PreferNoAttestation,
		AuthenticatorSelection: protocol.AuthenticatorSelection{
			ResidentKey:        protocol.ResidentKeyRequirementRequired,
			RequireResidentKey: protocol.ResidentKeyRequired(),
			UserVerification:   protocol.VerificationRequired,
		},
		// 浏览器侧的超时与服务端会话有效期都是 5 分钟；服务端过期由挑战会话表自己判
		Timeouts: webauthn.TimeoutsConfig{
			Login:        webauthn.TimeoutConfig{Timeout: passkeyCeremonyTTL, TimeoutUVD: passkeyCeremonyTTL},
			Registration: webauthn.TimeoutConfig{Timeout: passkeyCeremonyTTL, TimeoutUVD: passkeyCeremonyTTL},
		},
	})
}

func randomPasskeyToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// storePasskeyCeremony 把挑战会话放进内存表，返回随机 session_id。
//
// 两层限额：
//  1. 按键：同一个 limitKey（登录按 IP、注册按用户）未过期的会话到了上限（登录 10、注册 5），
//     先清这个键过期的，还满就挤掉这个键最旧的一个。刷接口的人只能挤掉自己的，正常用户重试不会被拒。
//  2. 全局：只算登录会话，满 1000 先清全表过期的，还满就拒（ErrPasskeyTooManySessions，接口回 429）。
func storePasskeyCeremony(ceremony *passkeyCeremony) (string, error) {
	sessionID, err := randomPasskeyToken()
	if err != nil {
		return "", err
	}

	passkeyCeremonies.Lock()
	defer passkeyCeremonies.Unlock()

	now := passkeyNow()
	perKeyLimit := passkeyLoginCeremoniesPerIP
	if ceremony.purpose == PasskeyPurposeRegister {
		perKeyLimit = passkeyRegisterCeremoniesPerUser
	}

	// 第一层：数这个键还活着的会话，顺手清掉它过期的
	var sameKey []string
	for id, item := range passkeyCeremonies.items {
		if item.purpose != ceremony.purpose || item.limitKey != ceremony.limitKey {
			continue
		}
		if !now.Before(item.expiresAt) {
			delete(passkeyCeremonies.items, id)
			continue
		}
		sameKey = append(sameKey, id)
	}
	// 还超就从最旧的开始挤，直到给新会话腾出一个位置
	for len(sameKey) >= perKeyLimit {
		oldest := 0
		for i := range sameKey {
			if passkeyCeremonies.items[sameKey[i]].seq < passkeyCeremonies.items[sameKey[oldest]].seq {
				oldest = i
			}
		}
		delete(passkeyCeremonies.items, sameKey[oldest])
		sameKey = append(sameKey[:oldest], sameKey[oldest+1:]...)
	}

	// 第二层：全局上限只管登录会话
	if ceremony.purpose == PasskeyPurposeLogin {
		countLogin := func() int {
			count := 0
			for _, item := range passkeyCeremonies.items {
				if item.purpose == PasskeyPurposeLogin {
					count++
				}
			}
			return count
		}
		if countLogin() >= passkeyCeremonyLimit {
			for id, item := range passkeyCeremonies.items {
				if !now.Before(item.expiresAt) {
					delete(passkeyCeremonies.items, id)
				}
			}
			if countLogin() >= passkeyCeremonyLimit {
				return "", ErrPasskeyTooManySessions
			}
		}
	}

	passkeyCeremonies.seq++
	ceremony.seq = passkeyCeremonies.seq
	ceremony.expiresAt = now.Add(passkeyCeremonyTTL)
	passkeyCeremonies.items[sessionID] = ceremony
	return sessionID, nil
}

// takePasskeyCeremony 取出并删除挑战会话：一次性，无论后面校验成败都不能再用第二次。
// 不存在、已过期、用途不对，一律 ErrPasskeySessionExpired。
func takePasskeyCeremony(sessionID, purpose string) (*passkeyCeremony, error) {
	passkeyCeremonies.Lock()
	ceremony := passkeyCeremonies.items[sessionID]
	delete(passkeyCeremonies.items, sessionID)
	passkeyCeremonies.Unlock()

	if ceremony == nil || ceremony.purpose != purpose || !passkeyNow().Before(ceremony.expiresAt) {
		return nil, ErrPasskeySessionExpired
	}
	return ceremony, nil
}

// passkeyCredentialFromRow 解出一行里存的 webauthn.Credential
func passkeyCredentialFromRow(row *model.Passkey) (webauthn.Credential, error) {
	var credential webauthn.Credential
	err := json.Unmarshal([]byte(row.Credential), &credential)
	return credential, err
}

// BeginPasskeyLogin 免登录发起通行密钥登录：可发现凭据（allowCredentials 留空），必须用户验证。
// clientIP 是 middleware.ResolveClientIP 解出的客户端 IP，只用来给挑战会话按 IP 限额。
func BeginPasskeyLogin(origin, clientIP string) (string, protocol.PublicKeyCredentialRequestOptions, error) {
	rpID, err := PasskeyRPFromOrigin(origin)
	if err != nil {
		return "", protocol.PublicKeyCredentialRequestOptions{}, err
	}
	wa, err := newPasskeyWebAuthn(rpID, origin)
	if err != nil {
		return "", protocol.PublicKeyCredentialRequestOptions{}, err
	}
	assertion, session, err := wa.BeginDiscoverableLogin(webauthn.WithUserVerification(protocol.VerificationRequired))
	if err != nil {
		return "", protocol.PublicKeyCredentialRequestOptions{}, err
	}
	sessionID, err := storePasskeyCeremony(&passkeyCeremony{
		session:  *session,
		rpID:     rpID,
		origin:   origin,
		purpose:  PasskeyPurposeLogin,
		limitKey: passkeyLoginLimitKey(clientIP),
	})
	if err != nil {
		return "", protocol.PublicKeyCredentialRequestOptions{}, err
	}
	return sessionID, assertion.Response, nil
}

// FinishPasskeyLogin 校验通行密钥登录。返回值里的 username 是「能认出是谁就写」，给登录日志用。
//
// 校验顺序：会话（一次性）→ 按 credential id 找行 → 行的 RPID、来源都等于会话的 → userHandle 等于行的句柄
// → 用户存在 → 库校验签名（userVerification 必须为真）→ 用户启用 → 回写 sign count / flags 与最近使用时间。
// 用户禁用放在签名校验之后：没证明持有这枚密钥之前，不透露「这个账号被禁用了」。
func FinishPasskeyLogin(sessionID string, credentialJSON []byte) (*model.User, string, error) {
	ceremony, err := takePasskeyCeremony(sessionID, PasskeyPurposeLogin)
	if err != nil {
		return nil, "", err
	}

	parsed, err := protocol.ParseCredentialRequestResponseBytes(credentialJSON)
	if err != nil {
		return nil, "", ErrPasskeyInvalid
	}

	var row model.Passkey
	credentialID := base64.RawURLEncoding.EncodeToString(parsed.RawID)
	if err := database.DB.Where("credential_id = ?", credentialID).First(&row).Error; err != nil {
		return nil, "", ErrPasskeyInvalid
	}
	// 凭据绑定的域名必须就是这次会话的域名：在 A 域名下加的密钥不能拿到 B 域名的会话里用
	if row.RPID != ceremony.rpID {
		return nil, "", ErrPasskeyInvalid
	}
	// 来源（含协议与端口）也必须与添加时完全一致：同域名换个端口跑的可能是另一个服务，
	// RP ID 相同、认证器也会照常签名，只有这条绑定拦得住
	if row.Origin != ceremony.origin {
		return nil, "", ErrPasskeyInvalid
	}
	handle, err := base64.RawURLEncoding.DecodeString(row.UserHandle)
	if err != nil || len(handle) == 0 || !bytes.Equal(handle, parsed.Response.UserHandle) {
		return nil, "", ErrPasskeyInvalid
	}

	var user model.User
	if err := database.DB.First(&user, row.UserID).Error; err != nil {
		return nil, "", ErrPasskeyInvalid
	}

	stored, err := passkeyCredentialFromRow(&row)
	if err != nil {
		return nil, user.Username, ErrPasskeyInvalid
	}
	wa, err := newPasskeyWebAuthn(ceremony.rpID, ceremony.origin)
	if err != nil {
		return nil, user.Username, ErrPasskeyInvalid
	}
	// 只把这一行交给库：库会再核一遍 userHandle 与 credential id，并按会话里的 required 校验 UV
	lookup := func(rawID, userHandle []byte) (webauthn.User, error) {
		return &passkeyUser{handle: handle, name: user.Username, credentials: []webauthn.Credential{stored}}, nil
	}
	_, validated, err := wa.ValidatePasskeyLogin(lookup, ceremony.session, parsed)
	if err != nil {
		return nil, user.Username, ErrPasskeyInvalid
	}

	if !user.Enabled {
		return nil, user.Username, ErrUserDisabled
	}

	// 回写 sign count / flags：下次登录校验要用最新的计数与备份状态
	updatedJSON, err := json.Marshal(validated)
	if err != nil {
		return nil, user.Username, ErrPasskeyInvalid
	}
	now := passkeyNow()
	if err := database.DB.Model(&model.Passkey{}).Where("id = ?", row.ID).Updates(map[string]interface{}{
		"credential":   string(updatedJSON),
		"last_used_at": now,
	}).Error; err != nil {
		return nil, user.Username, err
	}
	return &user, user.Username, nil
}

// passkeyUserHandle 返回这个用户的句柄：已有通行密钥就复用其中任一行的，否则新生成 32 字节随机句柄。
func passkeyUserHandle(userID uint) ([]byte, error) {
	var existing model.Passkey
	if err := database.DB.Where("user_id = ? AND user_handle <> ''", userID).Order("id ASC").Limit(1).Find(&existing).Error; err != nil {
		return nil, err
	}
	if existing.ID != 0 {
		if handle, err := base64.RawURLEncoding.DecodeString(existing.UserHandle); err == nil && len(handle) > 0 {
			return handle, nil
		}
	}
	handle := make([]byte, 32)
	if _, err := rand.Read(handle); err != nil {
		return nil, err
	}
	return handle, nil
}

// BeginPasskeyRegistration 给已登录用户发起添加通行密钥（调用方已校验过当前密码）。
// excludeCredentials 只列这个用户在本 RP ID 下已有的，免得同一个认证器重复添加。
func BeginPasskeyRegistration(user *model.User, origin string) (string, protocol.PublicKeyCredentialCreationOptions, error) {
	rpID, err := PasskeyRPFromOrigin(origin)
	if err != nil {
		return "", protocol.PublicKeyCredentialCreationOptions{}, err
	}

	var count int64
	if err := database.DB.Model(&model.Passkey{}).Where("user_id = ?", user.ID).Count(&count).Error; err != nil {
		return "", protocol.PublicKeyCredentialCreationOptions{}, err
	}
	if count >= PasskeyMaxPerUser {
		return "", protocol.PublicKeyCredentialCreationOptions{}, ErrPasskeyLimitReached
	}

	handle, err := passkeyUserHandle(user.ID)
	if err != nil {
		return "", protocol.PublicKeyCredentialCreationOptions{}, err
	}

	var rows []model.Passkey
	if err := database.DB.Where("user_id = ? AND rp_id = ?", user.ID, rpID).Order("id ASC").Find(&rows).Error; err != nil {
		return "", protocol.PublicKeyCredentialCreationOptions{}, err
	}
	exclusions := make([]protocol.CredentialDescriptor, 0, len(rows))
	for i := range rows {
		credential, err := passkeyCredentialFromRow(&rows[i])
		if err != nil {
			continue
		}
		exclusions = append(exclusions, credential.Descriptor())
	}

	wa, err := newPasskeyWebAuthn(rpID, origin)
	if err != nil {
		return "", protocol.PublicKeyCredentialCreationOptions{}, err
	}
	creation, session, err := wa.BeginRegistration(
		&passkeyUser{handle: handle, name: user.Username},
		webauthn.WithExclusions(exclusions),
	)
	if err != nil {
		return "", protocol.PublicKeyCredentialCreationOptions{}, err
	}

	sessionID, err := storePasskeyCeremony(&passkeyCeremony{
		session:  *session,
		rpID:     rpID,
		origin:   origin,
		purpose:  PasskeyPurposeRegister,
		userID:   user.ID,
		handle:   handle,
		limitKey: passkeyRegisterLimitKey(user.ID),
	})
	if err != nil {
		return "", protocol.PublicKeyCredentialCreationOptions{}, err
	}
	return sessionID, creation.Response, nil
}

// NormalizePasskeyName 名称 trim 后截到 64 个字符，空则「通行密钥」
func NormalizePasskeyName(name string) string {
	name = strings.TrimSpace(name)
	if utf8.RuneCountInString(name) > passkeyNameMaxRunes {
		name = strings.TrimSpace(string([]rune(name)[:passkeyNameMaxRunes]))
	}
	if name == "" {
		return passkeyDefaultName
	}
	return name
}

// FinishPasskeyRegistration 校验注册响应并落库。会话必须是 register 用途且属于 userID，否则按「已过期」处理。
func FinishPasskeyRegistration(userID uint, sessionID, name string, credentialJSON []byte) (*model.Passkey, error) {
	ceremony, err := takePasskeyCeremony(sessionID, PasskeyPurposeRegister)
	if err != nil {
		return nil, err
	}
	if ceremony.userID != userID {
		return nil, ErrPasskeySessionExpired
	}

	var user model.User
	if err := database.DB.First(&user, userID).Error; err != nil {
		return nil, ErrUserNotFound
	}

	parsed, err := protocol.ParseCredentialCreationResponseBytes(credentialJSON)
	if err != nil {
		return nil, ErrPasskeyRegisterFailed
	}
	wa, err := newPasskeyWebAuthn(ceremony.rpID, ceremony.origin)
	if err != nil {
		return nil, ErrPasskeyRegisterFailed
	}
	credential, err := wa.CreateCredential(&passkeyUser{handle: ceremony.handle, name: user.Username}, ceremony.session, parsed)
	if err != nil {
		return nil, ErrPasskeyRegisterFailed
	}

	credentialID := base64.RawURLEncoding.EncodeToString(credential.ID)
	var duplicates int64
	database.DB.Model(&model.Passkey{}).Where("credential_id = ?", credentialID).Count(&duplicates)
	if duplicates > 0 {
		return nil, ErrPasskeyDuplicate
	}
	// begin 之后可能又在别处加满了，落库前再核一次上限
	var count int64
	database.DB.Model(&model.Passkey{}).Where("user_id = ?", userID).Count(&count)
	if count >= PasskeyMaxPerUser {
		return nil, ErrPasskeyLimitReached
	}

	credentialJSONText, err := json.Marshal(credential)
	if err != nil {
		return nil, err
	}
	row := model.Passkey{
		UserID:       userID,
		Name:         NormalizePasskeyName(name),
		RPID:         ceremony.rpID,
		Origin:       ceremony.origin,
		CredentialID: credentialID,
		UserHandle:   base64.RawURLEncoding.EncodeToString(ceremony.handle),
		Credential:   string(credentialJSONText),
	}
	if err := database.DB.Create(&row).Error; err != nil {
		// credential_id 是唯一索引：并发重复提交时由数据库兜住
		var again int64
		database.DB.Model(&model.Passkey{}).Where("credential_id = ?", credentialID).Count(&again)
		if again > 0 {
			return nil, ErrPasskeyDuplicate
		}
		return nil, err
	}
	return &row, nil
}

// ListPasskeys 列出这个用户自己的通行密钥，按创建时间倒序
func ListPasskeys(userID uint) ([]model.Passkey, error) {
	var rows []model.Passkey
	err := database.DB.Where("user_id = ?", userID).Order("created_at DESC").Order("id DESC").Find(&rows).Error
	return rows, err
}

// RenamePasskey 只改自己的；不是自己的（或不存在）一律 ErrPasskeyNotFound
func RenamePasskey(userID, passkeyID uint, name string) error {
	result := database.DB.Model(&model.Passkey{}).
		Where("id = ? AND user_id = ?", passkeyID, userID).
		Update("name", NormalizePasskeyName(name))
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrPasskeyNotFound
	}
	return nil
}

// SetPasswordAndClearPasskeys 在同一个事务里写入新密码哈希、删掉这个用户的全部通行密钥，返回删掉的个数。
// 改密 / 重置密码就是重置登录凭据，与「改密即撤销全部会话」同一口径：通行密钥也要一起作废，
// 否则密码泄露后改了密，偷偷加上的通行密钥还能继续登录。
// 调用方要在这之后再调 RevokeAllUserSessions（先落库、再撤销，见 handler.commitLoginSession 的注释）。
func SetPasswordAndClearPasskeys(userID uint, passwordHash string) (int64, error) {
	var removed int64
	err := database.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&model.User{}).Where("id = ?", userID).Update("password", passwordHash).Error; err != nil {
			return err
		}
		result := tx.Where("user_id = ?", userID).Delete(&model.Passkey{})
		if result.Error != nil {
			return result.Error
		}
		removed = result.RowsAffected
		return nil
	})
	if err != nil {
		return 0, err
	}
	return removed, nil
}

// DeletePasskey 只删自己的；不是自己的（或不存在）一律 ErrPasskeyNotFound
func DeletePasskey(userID, passkeyID uint) error {
	result := database.DB.Where("id = ? AND user_id = ?", passkeyID, userID).Delete(&model.Passkey{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrPasskeyNotFound
	}
	return nil
}
