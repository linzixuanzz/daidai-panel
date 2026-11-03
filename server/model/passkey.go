package model

import (
	"encoding/json"
	"time"
)

// Passkey 是用户添加的一枚通行密钥（WebAuthn 可发现凭据，issue #175）。
//
// 每枚凭据绑定添加时访问的域名（RPID），换个域名访问面板就用不了这一枚，要在新域名下再加一枚。
// UserHandle 是这个用户的随机句柄（32 字节 base64url）：第一次添加时生成，之后复用他已有任一行的句柄；
// 登录时认证器回传的 userHandle 必须与这一行一致。
// Credential 存 go-webauthn 的 webauthn.Credential 整个 JSON（公钥、flags、sign count、transports、认证器信息），
// 登录校验与回写都靠它。
//
// Origin 是添加时的完整来源（scheme://主机[:端口]）。同一个域名换个端口或协议访问也算另一个来源：
// 登录时这一行的 Origin 必须与本次会话的来源完全相同，否则按「通行密钥验证失败」处理。
//
// 公钥、句柄、凭据 JSON 都不对外输出：ToDict 只给 id / name / rp_id / origin / 时间 / backup_state。
type Passkey struct {
	ID           uint       `gorm:"primarykey" json:"id"`
	UserID       uint       `gorm:"index;not null" json:"user_id"`
	Name         string     `gorm:"size:64" json:"name"`
	RPID         string     `gorm:"column:rp_id;size:255;index" json:"rp_id"`
	Origin       string     `gorm:"size:255" json:"origin"`
	CredentialID string     `gorm:"size:512;uniqueIndex" json:"-"`
	UserHandle   string     `gorm:"size:128" json:"-"`
	Credential   string     `gorm:"type:text" json:"-"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
	LastUsedAt   *time.Time `json:"last_used_at"`
}

func (Passkey) TableName() string {
	return "passkeys"
}

// BackupState 从凭据 JSON 里读 flags.backupState（凭据是否已被同步 / 备份，比如 iCloud 钥匙串、谷歌密码管理器）。
// 只解这一个字段，model 包不依赖 webauthn 库；JSON 读不出来就当 false。
func (p *Passkey) BackupState() bool {
	var parsed struct {
		Flags struct {
			BackupState bool `json:"backupState"`
		} `json:"flags"`
	}
	if err := json.Unmarshal([]byte(p.Credential), &parsed); err != nil {
		return false
	}
	return parsed.Flags.BackupState
}

func (p *Passkey) ToDict() map[string]interface{} {
	return map[string]interface{}{
		"id":           p.ID,
		"name":         p.Name,
		"rp_id":        p.RPID,
		"origin":       p.Origin,
		"created_at":   p.CreatedAt,
		"last_used_at": p.LastUsedAt,
		"backup_state": p.BackupState(),
	}
}
