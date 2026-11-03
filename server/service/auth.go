package service

import (
	"errors"
	"strings"
	"time"

	"daidai-panel/database"
	"daidai-panel/middleware"
	"daidai-panel/model"
	"daidai-panel/pkg/crypto"
	"daidai-panel/pkg/validator"
)

var (
	ErrUserNotFound     = errors.New("用户不存在")
	ErrInvalidPassword  = errors.New("密码错误")
	ErrUserDisabled     = errors.New("账号已被禁用")
	ErrUserExists       = errors.New("用户名已存在")
	ErrInvalidUsername  = errors.New("用户名格式无效")
	ErrPasswordTooShort = errors.New("密码过短")
	ErrTOTPRequired     = errors.New("需要两步验证码")
	ErrInvalidTOTP      = errors.New("两步验证码错误")
	ErrSessionRevoked   = errors.New("登录会话已失效")
)

type AuthService struct{}

func NewAuthService() *AuthService {
	return &AuthService{}
}

func (s *AuthService) NeedInit() bool {
	var count int64
	database.DB.Model(&model.User{}).Count(&count)
	return count == 0
}

func (s *AuthService) InitAdmin(username, password string) (*model.User, error) {
	if !s.NeedInit() {
		return nil, errors.New("系统已初始化")
	}

	if !validator.ValidateUsername(username) {
		return nil, ErrInvalidUsername
	}
	if !validator.ValidatePassword(password) {
		return nil, ErrPasswordTooShort
	}

	hash, err := crypto.HashPassword(password)
	if err != nil {
		return nil, err
	}

	user := &model.User{
		Username: username,
		Password: hash,
		Role:     "admin",
		Enabled:  true,
	}
	if err := database.DB.Create(user).Error; err != nil {
		return nil, err
	}
	return user, nil
}

func (s *AuthService) Login(username, password, totpCode string) (*model.User, string, string, *middleware.TokenInfo, *middleware.TokenInfo, error) {
	username = validator.SanitizeString(username)
	totpCode = strings.TrimSpace(totpCode)

	var user model.User
	if err := database.DB.Where("username = ?", username).First(&user).Error; err != nil {
		return nil, "", "", nil, nil, ErrUserNotFound
	}

	if !user.Enabled {
		return nil, "", "", nil, nil, ErrUserDisabled
	}

	if !crypto.CheckPassword(password, user.Password) {
		return nil, "", "", nil, nil, ErrInvalidPassword
	}

	if IsTwoFactorEnabled(user.ID) {
		if totpCode == "" {
			return nil, "", "", nil, nil, ErrTOTPRequired
		}
		if !ValidateUserTOTP(user.ID, totpCode) {
			return nil, "", "", nil, nil, ErrInvalidTOTP
		}
	}

	return s.IssueLoginTokens(&user)
}

// IssueLoginTokens 是登录成功后的签发部分：写 last_login_at、签 access / refresh。
// 密码登录（Login 校验通过后）与通行密钥登录（FinishPasskeyLogin 校验通过后）共用这一段，两边签出的令牌完全同形。
func (s *AuthService) IssueLoginTokens(user *model.User) (*model.User, string, string, *middleware.TokenInfo, *middleware.TokenInfo, error) {
	// 只写 last_login_at 这一列（GORM 会顺带刷新 updated_at，与原来一致）。
	// 🔴 不能用 Save(&user)：它把开头读到的整行（用户名、密码哈希、角色、启用状态……）原样写回。
	// 管理员的禁用、改角色、重置密码只要恰好提交在「读用户」与这次写回之间，就会被静默改回去；
	// 删号落在这个窗口里更糟：Save 更新到 0 行时会退化成插入，把刚删掉的账号又建回来。
	now := time.Now()
	user.LastLoginAt = &now
	database.DB.Model(user).Update("last_login_at", now)

	tokenInfo, err := middleware.GenerateAccessTokenInfo(user.Username, user.Role)
	if err != nil {
		return nil, "", "", nil, nil, err
	}

	refreshInfo, err := middleware.GenerateRefreshTokenInfo(user.Username, user.Role)
	if err != nil {
		return nil, "", "", nil, nil, err
	}

	return user, tokenInfo.Token, refreshInfo.Token, tokenInfo, refreshInfo, nil
}

func (s *AuthService) RefreshToken(tokenStr string) (string, error) {
	claims, err := middleware.ParseToken(tokenStr)
	if err != nil {
		return "", errors.New("刷新令牌无效")
	}

	if claims.TokenType != "refresh" {
		return "", errors.New("不是刷新令牌")
	}

	if middleware.IsTokenBlocked(claims.ID) {
		return "", errors.New("刷新令牌已被撤销")
	}

	// refresh 必须还能按 refresh_jti 找到它所属的登录会话：退出、撤销、改密、禁用、删除、改角色、
	// 被新登录顶替都会删掉会话行，删了就不能再续期。refresh_jti 从面板 v1.8.0 起就有，现存有效的 refresh 都找得到。
	var session model.UserSession
	if err := database.DB.Where("refresh_jti = ?", claims.ID).First(&session).Error; err != nil {
		return "", ErrSessionRevoked
	}

	var user model.User
	if err := database.DB.Where("username = ?", claims.Username).First(&user).Error; err != nil {
		return "", ErrUserNotFound
	}

	if !user.Enabled {
		return "", ErrUserDisabled
	}

	// 会话必须属于这个用户：删号后再建一个同名账号时，旧设备的 refresh 不能续到新账号上
	if session.UserID != user.ID {
		return "", ErrSessionRevoked
	}

	// 续期出的 access 沿用原会话号，撤销这个会话时它会一起作废
	info, err := middleware.GenerateSessionAccessTokenInfo(user.Username, user.Role, session.JTI)
	if err != nil {
		return "", err
	}

	// expires_at 从此记「这个会话签出的最晚一枚 access 的到期」：撤销时会话号那行黑名单要活到它，
	// 会话清理也要等它过期才删行。在 Go 里比较取大，不在 SQL 里比时间（时间列按带偏移的文本存）；
	// 管理员调短有效期后新到期可能更早，这时保持原值。
	newExpiresAt := session.ExpiresAt
	if info.ExpiresAt.After(newExpiresAt) {
		newExpiresAt = info.ExpiresAt
	}
	result := database.DB.Model(&model.UserSession{}).Where("id = ?", session.ID).Update("expires_at", newExpiresAt)
	if result.Error != nil || result.RowsAffected == 0 {
		// 会话恰好在这一刻被撤销：不下发。两端都认定「续期成功 ⇒ 新 access 立即可用」，
		// 发出一枚马上就被拒的 access，网页会陷入「续期成功、重放仍 401」却永远不回登录页。
		return "", ErrSessionRevoked
	}

	return info.Token, nil
}

func (s *AuthService) GetUser(username string) (*model.User, error) {
	var user model.User
	if err := database.DB.Where("username = ?", username).First(&user).Error; err != nil {
		return nil, ErrUserNotFound
	}
	return &user, nil
}

// ChangePassword 校验旧密码后改成新密码，并在同一事务里清掉这个用户的全部通行密钥。
// 返回清掉的通行密钥个数，handler 用它决定提示文案；撤销会话由 handler 在这之后做。
func (s *AuthService) ChangePassword(username, oldPassword, newPassword string) (int64, error) {
	var user model.User
	if err := database.DB.Where("username = ?", username).First(&user).Error; err != nil {
		return 0, ErrUserNotFound
	}

	if !crypto.CheckPassword(oldPassword, user.Password) {
		return 0, ErrInvalidPassword
	}

	if !validator.ValidatePassword(newPassword) {
		return 0, ErrPasswordTooShort
	}

	hash, err := crypto.HashPassword(newPassword)
	if err != nil {
		return 0, err
	}

	return SetPasswordAndClearPasskeys(user.ID, hash)
}
