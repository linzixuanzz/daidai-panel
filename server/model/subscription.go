package model

import (
	"strings"
	"time"
)

const (
	SubTypeSingleFile = "single-file"
	SubTypeGitRepo    = "git-repo"
	SubAuthTypeSSH    = "ssh"
	SubAuthTypeToken  = "token"

	// 覆盖拉取策略三态：inherit 跟随全局开关，force 强制覆盖，preserve 强制保留本地。
	// inherit 是默认值，存量订阅补列后一律落在这一档，升级前后行为完全一致。
	SubOverwriteInherit  = "inherit"
	SubOverwriteForce    = "force"
	SubOverwritePreserve = "preserve"

	// 任务同步策略三态（自动添加定时任务 / 自动删除失效任务各一份）：
	// inherit 跟随全局默认值，enabled 这条订阅强制开，disabled 这条订阅强制关。
	// 刻意不复用上面的 force / preserve —— 那两个词说的是「工作区文件怎么合并」，
	// 和「建不建任务」不是一回事，混用会让两套三态在阅读时互相污染。
	SubTaskSyncInherit  = "inherit"
	SubTaskSyncEnabled  = "enabled"
	SubTaskSyncDisabled = "disabled"
)

type Subscription struct {
	ID          uint       `gorm:"primarykey" json:"id"`
	Name        string     `gorm:"size:128;not null" json:"name"`
	Type        string     `gorm:"size:32;not null;default:'git-repo'" json:"type"`
	URL         string     `gorm:"size:512;not null" json:"url"`
	Branch      string     `gorm:"size:128;default:''" json:"branch"`
	Schedule    string     `gorm:"size:64;default:''" json:"schedule"`
	Whitelist   string     `gorm:"size:512;default:''" json:"whitelist"`
	Blacklist   string     `gorm:"size:512;default:''" json:"blacklist"`
	DependOn    string     `gorm:"size:512;default:''" json:"depend_on"`
	// PreScript 是「拉取前指令」，在 git 拉取之前执行；HookScript 是「拉取后钩子」，
	// 在拉取成功之后、同步定时任务之前执行。两者共用同一套环境变量与超时。
	PreScript   string     `gorm:"type:text;default:''" json:"pre_script"`
	HookScript  string     `gorm:"type:text;default:''" json:"hook_script"`
	AutoAddTask bool       `gorm:"default:false" json:"auto_add_task"`
	AutoDelTask bool       `gorm:"default:false" json:"auto_del_task"`
	// AutoAddTaskMode / AutoDelTaskMode 是「自动添加定时任务 / 自动删除失效任务」的三态开关：
	// inherit=跟随全局 auto_add_cron / auto_del_cron，enabled=这条订阅强制开，disabled=强制关。
	// 上面那两个布尔列是 v3.2.6 之前的旧写法，语义是「订阅开 OR 全局开」——全局默认为 true，
	// 于是它们几乎恒等于空转，更要命的是 OR 表达不了「全局开着、但这一条订阅别建任务」。
	//
	// 它们现在只做**只读输出**：不再参与任何判定，也不再有任何写入路径能把它们写成 true。
	// 所有写入点（Create / 青龙导入 / 备份还原）都走 ResolveSubscriptionTaskSyncModeInput，
	// 把「老客户端只发了布尔 true」当场翻译成 mode='enabled'，这两列恒写 false；
	// Update 的白名单里也不再有这两个键。恒 false 是启动回填（migrateLegacySubscriptionTaskSyncFlags）
	// 幂等性的前提 —— 只要还有路径能写回 1，用户显式选的 inherit 就会在下次重启被提回 enabled。
	AutoAddTaskMode string `gorm:"size:16;not null;default:'inherit'" json:"auto_add_task_mode"`
	AutoDelTaskMode string `gorm:"size:16;not null;default:'inherit'" json:"auto_del_task_mode"`
	Enabled     bool       `gorm:"default:true" json:"enabled"`
	Status      int        `gorm:"default:0" json:"status"`
	LastPullAt  *time.Time `json:"last_pull_at"`
	SaveDir     string     `gorm:"size:512;default:''" json:"save_dir"`
	SSHKeyID    *uint      `json:"ssh_key_id"`
	AuthType    string     `gorm:"size:16;default:''" json:"auth_type"`
	AuthUsername string     `gorm:"size:128;default:''" json:"auth_username"`
	AuthToken   string     `gorm:"type:text;default:''" json:"-"`
	SubPath     string     `gorm:"size:512;default:''" json:"sub_path"`
	Alias       string     `gorm:"size:128;default:''" json:"alias"`
	ForceOverwrite *bool   `gorm:"default:true" json:"force_overwrite"`
	// OverwriteMode 覆盖拉取策略：inherit=跟随全局 / force=强制覆盖 / preserve=强制保留本地。
	// 作用域只有 git 工作区文件（reset --hard vs stash），与任务表无关：
	// 订阅同步从不修改已有任务的名称与定时（#125，见 service.syncSubscriptionTasks）。
	// 上面那个 ForceOverwrite 是 v2.2.15 之后就没人维护的旧列，只做只读兼容，不再参与判定。
	OverwriteMode string `gorm:"size:16;not null;default:'inherit'" json:"overwrite_mode"`
	// FullCheckout 完整检出：开启后放弃 sparse-checkout，把整个仓库拉下来。
	// 默认 false = 继续走既有的 sparse 逻辑，存量订阅补列后行为逐字节不变。
	// 存在的理由：青龙生态里 BiliBiliToolPro 这类脚本会去读仓库自己的 src/ 源码编译，
	// 只检出白名单命中的那几个 .sh 根本跑不起来。代价是整仓体积，所以默认关。
	FullCheckout bool `gorm:"not null;default:false" json:"full_checkout"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

func (Subscription) TableName() string {
	return "subscriptions"
}

func (s *Subscription) ToDict() map[string]interface{} {
	return map[string]interface{}{
		"id":              s.ID,
		"name":            s.Name,
		"type":            s.Type,
		"url":             s.URL,
		"branch":          s.Branch,
		"schedule":        s.Schedule,
		"whitelist":       s.Whitelist,
		"blacklist":       s.Blacklist,
		"depend_on":       s.DependOn,
		"pre_script":      s.PreScript,
		"hook_script":     s.HookScript,
		// auto_add_task / auto_del_task 是 v3.2.6 之前的旧布尔字段，保留输出只为老客户端和 APP 不炸；
		// 真正生效的是下面两个三态字段，前端读的也是它们（同 force_overwrite 与 overwrite_mode 的并存做法）。
		"auto_add_task":      s.AutoAddTask,
		"auto_del_task":      s.AutoDelTask,
		"auto_add_task_mode": NormalizeSubscriptionTaskSyncMode(s.AutoAddTaskMode),
		"auto_del_task_mode": NormalizeSubscriptionTaskSyncMode(s.AutoDelTaskMode),
		"enabled":         s.Enabled,
		"status":          s.Status,
		"last_pull_at":    s.LastPullAt,
		"sub_path":        s.SubPath,
		"save_dir":        s.SaveDir,
		"ssh_key_id":      s.SSHKeyID,
		"auth_type":       s.EffectiveAuthType(),
		"auth_username":   s.AuthUsername,
		"has_auth_token":  s.HasAuthToken(),
		"alias":           s.Alias,
		// force_overwrite 是 v2.2.15 之前的旧字段，保留输出只为老客户端不炸；
		// 真正生效的是下面的 overwrite_mode，前端读的也是它。
		"force_overwrite": s.ForceOverwrite == nil || *s.ForceOverwrite,
		"overwrite_mode":  NormalizeSubscriptionOverwriteMode(s.OverwriteMode),
		"full_checkout":   s.FullCheckout,
		"created_at":      s.CreatedAt,
		"updated_at":      s.UpdatedAt,
	}
}

func NormalizeSubscriptionAuthType(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "":
		return ""
	case SubAuthTypeSSH:
		return SubAuthTypeSSH
	case SubAuthTypeToken:
		return SubAuthTypeToken
	default:
		return ""
	}
}

// NormalizeSubscriptionOverwriteMode 把覆盖拉取策略归一到三个合法值之一。
// 空串、老库补列前读出来的 NULL、以及直接改库塞进去的脏值，一律按 inherit（跟随全局）处理，
// 这样任何异常输入都只会退回「和升级前一样」的行为，不会静默把某个订阅切到覆盖或保留。
func NormalizeSubscriptionOverwriteMode(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case SubOverwriteForce:
		return SubOverwriteForce
	case SubOverwritePreserve:
		return SubOverwritePreserve
	default:
		return SubOverwriteInherit
	}
}

// NormalizeSubscriptionTaskSyncMode 把「自动添加定时任务 / 自动删除失效任务」的三态开关
// 归一到三个合法值之一。空串、老库补列前读出来的 NULL、直接改库塞进去的脏值，
// 一律按 inherit（跟随全局默认）处理。
//
// 这里走「静默归一」而不是 NormalizeNotifyPushScope 那种返回 (值, ok) 让调用方报 400 的口径：
// 推送范围拼错会把用户的隔离意图反向执行，必须拦；而这一项拼错只是回落到全局默认值，
// 也就是「和升级前一样」，不会产生语义反转。
func NormalizeSubscriptionTaskSyncMode(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case SubTaskSyncEnabled:
		return SubTaskSyncEnabled
	case SubTaskSyncDisabled:
		return SubTaskSyncDisabled
	default:
		return SubTaskSyncInherit
	}
}

// ResolveSubscriptionTaskSyncModeInput 是「旧布尔 → 三态」唯一的翻译点，
// 所有写入路径（handler.Create / 青龙导入 / 备份还原）都必须用它算出要落库的 mode，
// 并把旧布尔列恒写成 false。
//
// 为什么翻译要放在写入点而不是只靠启动回填：回填靠「清 0 源列」保证幂等，
// 只要还有一条路径能把源列写回 1，用户显式选的 inherit 就会在下次重启被静默提成 enabled
// —— 设置消失、且没有任何提示。写入点翻译之后，源列恒为 false，
// 回填能看到的 1 只可能来自升级前的存量数据，天然只跑一次。
//
// 口径：
//   - 给了合法三态（inherit / enabled / disabled）就以三态为准。用户在界面上选了「跟随全局设置」，
//     不能被同一个请求里顺带回传的旧布尔 true 顶成「强制开启」——这正是本函数存在的理由。
//   - 没给三态（空串）或给了脏值时，才让旧布尔说话：老客户端 / APP 心里的语义是
//     「开就是开，不看全局」，所以 true 翻成 enabled，false 落回 inherit（跟随全局默认）。
func ResolveSubscriptionTaskSyncModeInput(mode string, legacyEnabled bool) string {
	normalized := NormalizeSubscriptionTaskSyncMode(mode)
	if !legacyEnabled || normalized != SubTaskSyncInherit {
		return normalized
	}
	// Normalize 会把空串、脏值和显式 inherit 一起归成 inherit，三者在这里必须区分开，
	// 所以回看原始输入：只有原始输入压根没表达过三态意图时，旧布尔才有发言权。
	if strings.EqualFold(strings.TrimSpace(mode), SubTaskSyncInherit) {
		return SubTaskSyncInherit
	}
	return SubTaskSyncEnabled
}

func (s *Subscription) HasAuthToken() bool {
	if s == nil {
		return false
	}
	return strings.TrimSpace(s.AuthToken) != ""
}

func (s *Subscription) EffectiveAuthType() string {
	if s == nil {
		return ""
	}
	if normalized := NormalizeSubscriptionAuthType(s.AuthType); normalized != "" {
		return normalized
	}
	if s.HasAuthToken() {
		return SubAuthTypeToken
	}
	if s.SSHKeyID != nil && *s.SSHKeyID > 0 {
		return SubAuthTypeSSH
	}
	return ""
}

type SubLog struct {
	ID             uint      `gorm:"primarykey" json:"id"`
	SubscriptionID uint      `gorm:"index;not null" json:"subscription_id"`
	Status         int       `gorm:"default:0" json:"status"`
	Content        string    `gorm:"type:text" json:"content"`
	Duration       float64   `gorm:"default:0" json:"duration"`
	CreatedAt      time.Time `json:"created_at"`

	Subscription *Subscription `gorm:"foreignKey:SubscriptionID" json:"-"`
}

func (SubLog) TableName() string {
	return "sub_logs"
}

func (l *SubLog) ToDict() map[string]interface{} {
	result := map[string]interface{}{
		"id":              l.ID,
		"subscription_id": l.SubscriptionID,
		"status":          l.Status,
		"content":         l.Content,
		"duration":        l.Duration,
		"created_at":      l.CreatedAt,
	}
	if l.Subscription != nil {
		result["subscription_name"] = l.Subscription.Name
	}
	return result
}
