package service

import (
	"time"

	"daidai-panel/model"
)

type BackupSelection struct {
	Configs       bool `json:"configs"`
	Tasks         bool `json:"tasks"`
	Subscriptions bool `json:"subscriptions"`
	EnvVars       bool `json:"env_vars"`
	Logs          bool `json:"logs"`
	Scripts       bool `json:"scripts"`
	Dependencies  bool `json:"dependencies"`
	TaskViews     bool `json:"task_views"`
}

type BackupCreateOptions struct {
	Password  string
	Name      string
	Selection BackupSelection
}

type BackupUser struct {
	ID           uint       `json:"id"`
	Username     string     `json:"username"`
	PasswordHash string     `json:"password_hash"`
	Role         string     `json:"role"`
	Enabled      bool       `json:"enabled"`
	LastLoginAt  *time.Time `json:"last_login_at"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
}

type BackupOpenApp struct {
	ID        uint      `json:"id"`
	Name      string    `json:"name"`
	AppKey    string    `json:"app_key"`
	AppSecret string    `json:"app_secret"`
	Scopes    string    `json:"scopes"`
	Enabled   bool      `json:"enabled"`
	RateLimit int       `json:"rate_limit"`
	CallCount int64     `json:"call_count,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// BackupNotifyChannel 的 PushScope：default = 参与广播，bound = 只有被显式绑定时才推送。
// 老备份里没有这个键，反序列化后是空串，恢复时按 default 处理，与升级前一致。
type BackupNotifyChannel struct {
	ID        uint      `json:"id"`
	Name      string    `json:"name"`
	Type      string    `json:"type"`
	Config    string    `json:"config"`
	PushScope string    `json:"push_scope"`
	Enabled   bool      `json:"enabled"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type BackupSSHKey struct {
	ID         uint      `json:"id"`
	Name       string    `json:"name"`
	PrivateKey string    `json:"private_key"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

type BackupTwoFactorAuth struct {
	UserID    uint      `json:"user_id"`
	Secret    string    `json:"secret"`
	Enabled   bool      `json:"enabled"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// BackupPasskey 是用户的通行密钥（issue #175）。语义照抄 BackupTwoFactorAuth：跟着用户走、随「配置」一起导出；
// 恢复时与 2FA 一样保留当前面板的用户与通行密钥，不导入备份里的（restorePasskeys 与 restoreTwoFactorAuths 成对放着）。
// UserID 用来在用户恢复链路接上时经 userIDMap 映射，Username 方便人工核对是谁的。
// ⚠️ 凭据 JSON 里有公钥，备份文件不能外发（与 2FA 密钥、密码哈希同一口径）。
type BackupPasskey struct {
	UserID       uint       `json:"user_id"`
	Username     string     `json:"username"`
	Name         string     `json:"name"`
	RPID         string     `json:"rp_id"`
	Origin       string     `json:"origin"` // 添加时的完整来源（含端口），登录时必须一致
	CredentialID string     `json:"credential_id"`
	UserHandle   string     `json:"user_handle"`
	Credential   string     `json:"credential"`
	CreatedAt    time.Time  `json:"created_at"`
	LastUsedAt   *time.Time `json:"last_used_at,omitempty"`
}

// BackupUserPreference 是 per-user 的界面偏好（Editor、List 两组 JSON，各对应 model.UserPreference 的一列）。
// 和 BackupTwoFactorAuth 一样跟着用户走，所以刻意**不**给 BackupSelection 加新开关：
// 用户在恢复界面看到的仍然是原来那几项，勾「配置」就一起带上。
//
// ⚠️ 这是手写平铺的结构体，不是嵌入 model：TestBackupPayloadModelsHaveNoJSONHiddenFields 那道反射护栏
// 管不到它，model.UserPreference 以后每加一列偏好，这里、导出、restoreUserPreferences 三处都要手工跟上。
type BackupUserPreference struct {
	UserID uint   `json:"user_id"`
	Editor string `json:"editor"`
	// 列表页偏好（issue #143）。omitempty：v3.3.1 之前的备份没有这个键，反序列化成空串 = 一个键都没存过，与升级前一致。
	List      string    `json:"list,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// BackupEnvVar 用指针保存 enabled，区分「老备份缺少 enabled 字段」和「新备份明确 disabled」。
type BackupEnvVar struct {
	ID        uint      `json:"id"`
	Name      string    `json:"name"`
	Value     string    `json:"value"`
	Remarks   string    `json:"remarks"`
	Enabled   *bool     `json:"enabled,omitempty"`
	Position  float64   `json:"position"`
	SortOrder int       `json:"sort_order"`
	Group     string    `json:"group"`
	Important bool      `json:"important,omitempty"` // 「重要」标记（APP #16）；omitempty：普通变量不写这个键，老备份 / 青龙备份没有它，读出来都是 false
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type BackupDependency struct {
	Type          string `json:"type"`
	Name          string `json:"name"`
	PythonVersion string `json:"python_version,omitempty"`
}

type BackupTaskLog struct {
	TaskID    uint       `json:"task_id"`
	TaskName  string     `json:"task_name"`
	Content   string     `json:"content"`
	Status    *int       `json:"status"`
	Duration  *float64   `json:"duration"`
	LogPath   *string    `json:"log_path"`
	StartedAt time.Time  `json:"started_at"`
	EndedAt   *time.Time `json:"ended_at"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
}

// BackupTask = model.Task + 备份专用的 labels。
// model.Task.Labels 是 json:"-"，直接序列化 model.Task 会把标签整列丢掉 —— 这就是 issue #112。
// 外层 Labels 层级更浅会遮蔽被提升的同名字段（而且 json:"-" 的字段本来就不参与序列化），
// 所以清单里落的是数组形态，与 /api/tasks/export 和 ToDict() 口径一致。
// 老备份没有这个键 → 反序列化为 nil → SetLabelsFromSlice(nil) 得空串 = 升级前行为。
//
// 用嵌入而不是把字段一个个平铺：平铺以后 model.Task 每加一个字段都要手工跟，漏一个就是新的丢数据。
// 嵌入的已知代价是「以后再给 model 加 json:"-" 字段又会静默丢」，
// 由 TestBackupPayloadModelsHaveNoJSONHiddenFields 这条反射护栏兜住。
//
// ⚠️ 取值一律用外层的 Labels，不要用嵌入体提升上来的 GetLabels()：
// 后者读的是 model.Task.Labels 那个逗号串，反序列化出来的 BackupTask 里它恒为空。
// 要转回 model.Task 就走 modelTaskFromBackup。
type BackupTask struct {
	model.Task
	Labels []string `json:"labels"`
}

// BackupSubscription = model.Subscription + 备份专用的 auth_token。
// model.Subscription.AuthToken 同样是 json:"-"（接口响应只下发 has_auth_token，从不回显 PAT），
// 以前备份包里根本没有它，恢复后所有 token 鉴权的订阅都拉不动，得手工重填一遍。
// ⚠️ 代价：PAT 会以明文进入备份包，备份文件不能外发。
//
// ⚠️ 同 BackupTask：取值用外层的 AuthToken，不要用提升上来的 HasAuthToken()，
// 后者读的是嵌入体里那个恒为空的字段。要转回 model.Subscription 走 modelSubscriptionFromBackup。
type BackupSubscription struct {
	model.Subscription
	AuthToken string `json:"auth_token"`
}

type BackupConfigBundle struct {
	SystemConfigs     []model.SystemConfig      `json:"system_configs,omitempty"`
	OpenApps          []BackupOpenApp           `json:"open_apps,omitempty"`
	NotifyChannels    []BackupNotifyChannel     `json:"notify_channels,omitempty"`
	Users             []BackupUser              `json:"users,omitempty"`
	IPWhitelists      []model.IPWhitelist       `json:"ip_whitelists,omitempty"`
	TwoFactorAuths    []BackupTwoFactorAuth     `json:"two_factor_auths,omitempty"`
	Passkeys          []BackupPasskey           `json:"passkeys,omitempty"` // v3.4.2 起；老备份没有这个键 = 没有通行密钥
	UserPreferences   []BackupUserPreference    `json:"user_preferences,omitempty"`
	DependencyMirrors *DependencyMirrorSettings `json:"dependency_mirrors,omitempty"`
}

type BackupPayload struct {
	Configs           BackupConfigBundle       `json:"configs,omitempty"`
	Tasks             []BackupTask             `json:"tasks,omitempty"`
	EnvVars           []BackupEnvVar           `json:"env_vars,omitempty"`
	Subscriptions     []BackupSubscription     `json:"subscriptions,omitempty"`
	SSHKeys           []BackupSSHKey           `json:"ssh_keys,omitempty"`
	Dependencies      []BackupDependency       `json:"dependencies,omitempty"`
	TaskLogs          []BackupTaskLog          `json:"task_logs,omitempty"`
	TaskViews         []model.TaskView         `json:"task_views,omitempty"`
	TaskLogDailyStats []model.TaskLogDailyStat `json:"task_log_daily_stats,omitempty"` // 执行趋势里已删日志的按天计数（#158），跟着「日志」勾选项走；老备份没有这个键 = nil
}

type BackupManifest struct {
	Format    string          `json:"format"`
	Version   string          `json:"version"`
	Source    string          `json:"source"`
	CreatedAt time.Time       `json:"created_at"`
	Selection BackupSelection `json:"selection"`
	Data      BackupPayload   `json:"data"`
}

func defaultBackupSelection() BackupSelection {
	return BackupSelection{
		Configs:       true,
		Tasks:         true,
		Subscriptions: true,
		EnvVars:       true,
		Logs:          true,
		Scripts:       true,
		Dependencies:  true,
		TaskViews:     true,
	}
}

func (s BackupSelection) Any() bool {
	return s.Configs || s.Tasks || s.Subscriptions || s.EnvVars || s.Logs || s.Scripts || s.Dependencies || s.TaskViews
}

func (s BackupSelection) NormalizeDefaults() BackupSelection {
	if s.Any() {
		return s
	}
	return defaultBackupSelection()
}
