package model

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	_ "time/tzdata"

	panelcron "daidai-panel/pkg/cron"
	"daidai-panel/pkg/netutil"
)

type SystemConfigValueType string

const (
	SystemConfigTypeString SystemConfigValueType = "string"
	SystemConfigTypeInt    SystemConfigValueType = "int"
	SystemConfigTypeBool   SystemConfigValueType = "bool"
	SystemConfigTypeEnum   SystemConfigValueType = "enum"
)

type SystemConfigOption struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

// SystemConfigDefinition 是面板对「一项系统配置长什么样」的唯一声明。
// 除服务端自身使用外，它还通过 GET /api/configs 原样下发给 Web 和 APP，
// 让客户端可以完全按 schema 动态渲染设置页，而不用各自再抄一份表单定义。
//
// 字段只允许新增，不允许改名或改语义：老客户端拿到多出来的键应当无感。
type SystemConfigDefinition struct {
	Key string `json:"key"`
	// Label 是输入框标题用的短词。Description 是长句说明（个别项有三行），
	// 只能当 hint 用，不能直接拿来当标题。
	Label        string                `json:"label"`
	DefaultValue string                `json:"default_value"`
	Description  string                `json:"description"`
	ValueType    SystemConfigValueType `json:"value_type"`
	Group        string                `json:"group"`
	// GroupLabel 是 Group 这个英文 slug 对应的中文分组名，由 systemConfigGroupLabels 统一给出。
	GroupLabel string `json:"group_label"`
	// Order 是注册顺序。/api/configs 返回的是 map 而不是 list，本身没有顺序，
	// 客户端要按声明顺序渲染就只能靠这个字段。
	Order int `json:"order"`
	// Secret 标记该项是凭据类配置，客户端渲染时应当打码（密码框）。
	// 注意：这里只是渲染提示，服务端目前仍然明文下发，详见 handler.buildConfigResponseItem 的说明。
	Secret bool `json:"secret,omitempty"`
	// Min / Max 只对 int 类型有值，来自 newIntConfig 的取值区间，
	// 供客户端做前端校验（服务端仍然会独立校验一次）。
	Min     *int                 `json:"min,omitempty"`
	Max     *int                 `json:"max,omitempty"`
	Options []SystemConfigOption `json:"options,omitempty"`
}

type systemConfigSpec struct {
	def       SystemConfigDefinition
	normalize func(string) (string, error)
}

const (
	PanelTimezoneConfigKey = "timezone"
	DefaultPanelTimezone   = "Asia/Shanghai"
)

// 内置 MCP 服务（issue #128）的两个开关。HTTP 模式（handler/mcp.go）与 stdio 模式（ddp mcp）
// 都按这两个键读，收成常量，避免两边各写一份字面量。
const (
	MCPEnabledConfigKey        = "mcp_enabled"
	MCPAllowMutationsConfigKey = "mcp_allow_mutations"
)

// 企业微信拉起任务（issue #145，v3.3.2）的两级开关，与上面 MCP 那两项一一对应：
// 总开关关着时回调路由直接 403（连验签都不做），总开关开着但不允许触发执行时，
// 企业微信那边配回调 URL 能配通、指令却不会真的跑任务 —— 用来分两步上线。
const (
	WecomTriggerEnabledConfigKey  = "wecom_trigger_enabled"
	WecomTriggerAllowRunConfigKey = "wecom_trigger_allow_run"
)

// WecomTriggerLinkGenerationConfigKey 是菜单触发链接的「代次」，管理员点「作废全部链接」时 +1。
//
// 它**刻意不进注册表**：这是面板运行时自己写的状态（同 auto_update_pending_version 那一类），
// 不是给人填的配置项。注册了反而有害 —— 它会出现在设置页的兜底区，用户随手改一个数字
// 就把所有链接作废了，而且 GET /api/configs 只下发 registered=true 的项给设置页渲染，
// 不注册就天然不会被误改（web/src/views/settings/systemConfigSchema.ts 的 parseSystemConfigItems）。
const WecomTriggerLinkGenerationConfigKey = "wecom_trigger_link_generation"

const (
	// DefaultRepoFileExtensions 是订阅扫描任务候选时认的脚本后缀。
	// LegacyRepoFileExtensions 是 v3.0.5 及之前的默认值，它漏了 mjs。
	// 两个常量都要留着：升级时靠「库里存的正好等于旧默认」来判断用户没改过这项配置，
	// 只有这种情况才把它抬到新默认，用户自己填过的值一律不动。
	DefaultRepoFileExtensions = "py js mjs ts sh"
	LegacyRepoFileExtensions  = "py js sh ts"
)

// LegacyLogBackgroundColor 是 v1.8.0 ~ v2.2.3 的日志底色默认值（固定深色）。
// v2.2.4 已把注册表默认改成「留空跟随主题」，但老实例库里存的还是这份旧默认、升级不会自愈；
// 靠「库里存的正好等于旧默认」判断用户没改过，只有这种情况才抬到新默认。
const LegacyLogBackgroundColor = "#0f172a"

// defaultBackupScheduleSelection 同时给注册表默认值和 normalizeBackupScheduleSelectionValue 使用。
//
// 这两处历史上是各写各的字面量，注册表那份少了 task_views：
// newValidatedStringConfig 把 DefaultValue 原样存进 definition、注册时不过 normalize，
// 于是 /api/configs 报出去的 default_value、InitDefaultConfigs 首次建行写入的值、
// GetRegisteredConfig 在库里没记录时的回退值，全都是缺了任务视图的那份，
// 表现为「从未保存过备份设置的实例，定时备份不含任务视图」。收成一个常量避免再次分叉。
const defaultBackupScheduleSelection = "configs,tasks,subscriptions,env_vars,logs,scripts,dependencies,task_views"

// systemConfigGroupLabels 是分组 slug -> 中文分组名。
// 新增分组时必须在这里补一条，TestEveryRegisteredConfigGroupHasLabel 会兜底拦截漏补。
var systemConfigGroupLabels = map[string]string{
	"tasks":        "任务运行",
	"network":      "网络代理",
	"security":     "安全",
	"branding":     "面板与运行时",
	"backup":       "定时备份",
	"alerts":       "告警通知",
	"subscription": "订阅拉取",
	"mcp":          "MCP 服务",
	"wecom":        "企业微信触发",
}

// finalizeSystemConfigSpecs 补齐无法在单条声明里写死的元信息：注册顺序和分组中文名。
//
// 必须在 registeredSystemConfigSpecs 这个变量的初始化表达式里调用，不能挪到 init()：
// registeredSystemConfigMap 是按值拷贝存 spec 的，init() 里再改切片，map 里那份就成了旧数据。
func finalizeSystemConfigSpecs(specs []systemConfigSpec) []systemConfigSpec {
	for i := range specs {
		specs[i].def.Order = i
		if label, exists := systemConfigGroupLabels[specs[i].def.Group]; exists {
			specs[i].def.GroupLabel = label
		} else {
			// 漏配中文名时退化成 slug，保证这个字段永远不为空串。
			specs[i].def.GroupLabel = specs[i].def.Group
		}
	}
	return specs
}

var registeredSystemConfigSpecs = finalizeSystemConfigSpecs([]systemConfigSpec{
	newIntConfig("max_concurrent_tasks", "定时任务并发数", "5", "定时任务最大并发数", "tasks", 1, 128),
	newIntConfig("log_retention_days", "日志保留天数", "7", "日志保留天数", "tasks", 1, 3650),
	newIntConfig("max_log_content_size", "日志内容上限", "102400000", "任务日志内容最大保留字节数", "tasks", 1024, 524288000),
	// 下限刻意不给到 1 分钟：填 1 会让几乎所有安装都秒失败，看起来像面板坏了。
	// 上限 720 分钟（12 小时）足够覆盖 ARM 设备上现场编译 opencv 这类极端情况。
	newIntConfig("dependency_install_timeout_minutes", "依赖安装超时(分钟)", "20", "单个依赖安装/卸载的最长执行时间。安装 opencv 等需要现场编译的大包时可调大", "tasks", 5, 720),
	// 系统命令行的单条命令超时。与依赖安装分开是因为语义不同：这里跑的是用户临时敲的命令，
	// 挂死时没人会盯着，必须有个上限；下限给到 1 分钟（敲错命令时不用等）。
	newIntConfig("console_timeout_minutes", "系统命令行超时(分钟)", "30", "系统命令行里单条命令的最长执行时间，超时会终止整个进程组", "tasks", 1, 720),
	newBoolConfig("auto_update_enabled", "静默更新", "false", "静默更新开关（每 24 小时自动检查并在有新版本时尝试更新）", "network"),
	newTrimmedStringConfig("auto_update_last_checked_at", "上次检查更新时间", "", "上次自动检查更新时间", "network"),
	newIntConfig("random_delay", "随机延迟最大秒数", "0", "任务执行前随机延迟最大秒数", "tasks", 0, 86400),
	newTrimmedStringConfig("random_delay_extensions", "延迟文件后缀", "", "随机延迟仅对指定脚本后缀生效", "tasks"),
	newBoolConfig("auto_install_deps", "自动安装缺失依赖", "true", "脚本缺依赖时自动尝试安装", "tasks"),
	newBoolConfig("detect_silent_exit", "检测脚本半路静默结束", "true", "Node 任务因 Promise 永不完成而提前退出时判定为失败，而不是记成成功", "tasks"),
	// #159 修复 A 的总开关：定时 / 手动任务的主命令结束后，结束它留在进程组里的后台进程（开机任务按类型豁免）。
	// 默认开：库里没有这一行时 GetRegisteredConfigBool 回落到 true，存量用户升级后默认生效。
	// runTask 每次执行现读一次，不需要进 handler.reloadRuntimeConfigKeys。
	// ⚠️ APP 的系统设置页按 schema 渲染，这个开关会自动出现在 APP 里并能保存，所以说明文字必须对两端都适用，
	// 不能写「仅网页端生效」之类的措辞。
	newBoolConfig("cleanup_leftover_processes", "任务结束后清理残留进程", "true", "定时、手动任务的主命令结束后，结束它留在后台的进程（如 nohup、& 启动的），避免越积越多占用内存；开机任务不清理。需要常驻的进程请用 setsid nohup … & 启动，或关闭此项。可在侧栏『后台进程』查看仍在运行的进程", "tasks"),
	newEnumConfig(
		"python_default_version",
		"默认 Python 版本",
		"3.12",
		"默认 Python 运行版本",
		"tasks",
		[]SystemConfigOption{
			{Value: "3.10", Label: "Python 3.10"},
			{Value: "3.11", Label: "Python 3.11"},
			{Value: "3.12", Label: "Python 3.12"},
		},
	),
	newIntConfig("cpu_warn", "CPU 阈值 (%)", "80", "CPU 告警阈值（%）", "alerts", 1, 100),
	newIntConfig("memory_warn", "内存阈值 (%)", "80", "内存告警阈值（%）", "alerts", 1, 100),
	newIntConfig("disk_warn", "磁盘阈值 (%)", "90", "磁盘告警阈值（%）", "alerts", 1, 100),
	// 这两项自 v3.2.6 起是「默认值」而不是「总开关」：订阅可以用 auto_add_task_mode /
	// auto_del_task_mode 单独强制开或强制关，只有留在 inherit 的订阅才回落到这里。
	newBoolConfig("auto_add_cron", "自动添加定时任务（默认）", "true", "订阅拉取后自动为新脚本创建定时任务（未单独设置的订阅使用此默认值）", "subscription"),
	newBoolConfig("auto_del_cron", "自动删除失效任务（默认）", "true", "订阅里脚本消失后自动删除对应定时任务（未单独设置的订阅使用此默认值）", "subscription"),
	// 这项只管 git 工作区里已跟踪文件的覆盖方式（reset --hard）：已跟踪文件会被重置成远端状态、
	// 远端已删除的也会跟着删掉，但不会删除未跟踪的本地新增文件（日志里「本地新增的文件已保留」说的就是这个）。
	// 唯一会跑 git clean -fd（连未跟踪文件一起清）的是「目录已存在但不是 git 仓库、原地 init 接管」那条分支，
	// 它无条件执行、根本不看这个开关，所以说明里不能再写「清理多余文件」。
	newBoolConfig("subscription_force_overwrite", "覆盖拉取（默认）", "true", "订阅拉取时覆盖本地修改（未单独设置的订阅使用此默认值）", "subscription"),
	// 留空不是「每天 0 点」：v3.2.9 起未声明 cron 的脚本在留空时不建任务（#134），描述要把留空的真实行为写出来。
	newValidatedStringConfig("default_cron_rule", "默认 Cron 规则", "", "订阅脚本未声明 cron 时使用；留空则不为这类脚本建定时任务", "subscription", normalizeDefaultCronRule),
	// 默认值必须含 mjs：ESM 脚本（.mjs）在青龙生态里很常见，
	// 旧默认 LegacyRepoFileExtensions 漏了它，表现为「仓库拉取成功、.mjs 却一个任务都不建」。
	// 存量实例的补齐见 system_config.go 的 InitDefaultConfigs。
	newTrimmedStringConfig("repo_file_extensions", "拉取文件后缀", DefaultRepoFileExtensions, "订阅自动识别任务时扫描的脚本后缀", "subscription"),
	newBoolConfig("notify_on_resource_warn", "资源超限发送通知", "false", "资源超限发送通知", "alerts"),
	newTrimmedStringConfig("notify_panel_label", "通知面板名称", "", "通知标题前缀的面板名称（多面板区分用，留空不附带）", "alerts"),
	newBoolConfig("notify_on_login", "登录成功发送通知", "false", "登录成功发送通知", "security"),
	newValidatedStringConfig("proxy_url", "代理地址", "", "出站请求代理地址", "network", normalizeProxyURL),
	newValidatedStringConfig("update_image_mirror", "系统更新镜像源", "", "旧 Docker Socket 更新链路使用的可选镜像源（Watchtower 部署请通过 DAIDAI_PANEL_IMAGE 配置仓库）", "network", normalizeUpdateImageMirror),
	newValidatedStringConfig("binary_update_proxy", "二进制更新加速源", "", "二进制更新下载加速源（留空直连 GitHub Release）", "network", normalizeBinaryUpdateProxy),
	// #177：匿名访问 GitHub API 按出口 IP 每小时只有 60 次，共用代理出口时常被别人用光，检查更新就会报限流。
	// 填了 Token 后只在请求 https://api.github.com 时带上（见 handler/system_release.go 的 applyGitHubAPIToken），
	// 绝不带到 github.com 网页、Release 下载地址和加速代理。审计只记键名，不会记录它的值。
	newSecretStringConfig("github_token", "GitHub Token", "", "只用于检查更新时访问 GitHub API，提高限流额度；填一个不勾任何权限的 fine-grained token 即可", "network"),
	newValidatedStringConfig(
		"trusted_proxy_cidrs",
		"可信代理 CIDR",
		strings.Join(netutil.DefaultTrustedProxyCIDRs(), "\n"),
		"可信代理 CIDR/IP 列表（逗号、空格或换行分隔）",
		"network",
		normalizeTrustedProxyCIDRs,
	),
	newTrimmedStringConfig("panel_title", "面板标题", "呆呆面板", "面板标题", "branding"),
	newValidatedStringConfig(PanelTimezoneConfigKey, "面板时区", DefaultPanelTimezone, "面板时区（影响日志、定时任务日期判断和脚本 TZ）", "branding", normalizeTimezoneValue),
	newTrimmedStringConfig("panel_icon", "面板图标 (SVG)", "", "面板图标（SVG data URL）", "branding"),
	newTrimmedStringConfig("editor_background_color", "编辑器背景颜色", "", "脚本编辑器背景颜色（留空使用默认样式）", "branding"),
	newTrimmedStringConfig("log_background_color", "日志背景颜色", "", "日志视图背景颜色（留空跟随当前主题）", "branding"),
	newTrimmedStringConfig("log_background_image", "日志背景图片", "", "日志视图背景图片（data URL）", "branding"),
	// 界面圆角开关：网页端把全站圆角刻度化成 --dd-radius-* 三档令牌，这一项决定取直角(全 0)还是圆角。
	// 默认 square 是为了保持 v3.0.0 以来「扁平工具型」的既有观感，升级的实例不会因为加了这项而变样。
	//
	// ⚠️ 这项纯粹由客户端消费，服务端没有任何运行时状态依赖它，所以不需要进
	// handler.reloadRuntimeConfigKeys 的热加载分支。
	// ⚠️ 注册表是原样下发给 Web 和 APP 的（见本文件顶部 SystemConfigDefinition 的说明），
	// APP 的 schema 驱动设置页也会渲染出这个开关但点了不会有效果，
	// 所以 description 里必须写明「仅网页端生效」，避免被当成 APP 的 bug。
	newEnumConfig(
		"panel_shape_style",
		"界面圆角",
		"square",
		"网页端界面的圆角风格。直角=扁平工具型（默认）；圆角=各控件与卡片带圆角。仅网页端生效，APP 不受影响。",
		"branding",
		[]SystemConfigOption{
			{Value: "square", Label: "直角"},
			{Value: "rounded", Label: "圆角"},
		},
	),
	newBoolConfig("backup_schedule_enabled", "启用定时备份", "false", "启用定时备份", "backup"),
	newEnumConfig(
		"backup_schedule_frequency",
		"备份频率",
		"daily",
		"定时备份频率",
		"backup",
		[]SystemConfigOption{
			{Value: "daily", Label: "每天"},
			{Value: "weekly", Label: "每周"},
			{Value: "monthly", Label: "每月"},
		},
	),
	newValidatedStringConfig("backup_schedule_time", "执行时间", "03:00", "定时备份执行时间（24 小时制 HH:MM）", "backup", normalizeBackupScheduleTimeValue),
	newEnumConfig(
		"backup_schedule_weekday",
		"每周执行日",
		"1",
		"每周备份执行日（0=周日，1=周一）",
		"backup",
		[]SystemConfigOption{
			{Value: "0", Label: "周日"},
			{Value: "1", Label: "周一"},
			{Value: "2", Label: "周二"},
			{Value: "3", Label: "周三"},
			{Value: "4", Label: "周四"},
			{Value: "5", Label: "周五"},
			{Value: "6", Label: "周六"},
		},
	),
	newIntConfig("backup_schedule_monthday", "每月执行日", "1", "每月备份执行日", "backup", 1, 28),
	newTrimmedStringConfig("backup_schedule_name", "文件名前缀", "", "定时备份文件名前缀", "backup"),
	newSecretStringConfig("backup_schedule_password", "加密密码", "", "定时备份加密密码", "backup"),
	newValidatedStringConfig(
		"backup_schedule_selection",
		"备份内容",
		defaultBackupScheduleSelection,
		"定时备份包含的内容（逗号分隔）",
		"backup",
		normalizeBackupScheduleSelectionValue,
	),
	newEnumConfig(
		"panel_runtime_mode",
		"运行时日志输出",
		"auto",
		"二进制运行时日志输出策略：auto=Docker 输出到 stdout，裸机输出到 panel.log；stdout=同时输出到 stdout 和 panel.log；file=仅写 panel.log",
		"branding",
		[]SystemConfigOption{
			{Value: "auto", Label: "自动"},
			{Value: "stdout", Label: "输出到 stdout"},
			{Value: "file", Label: "仅写文件"},
		},
	),
	newEnumConfig(
		"panel_service_manager",
		"守护方式",
		"none",
		"面板二进制守护方式；启用后更新流程会尝试先停止守护再启动守护",
		"branding",
		[]SystemConfigOption{
			{Value: "none", Label: "无"},
			{Value: "systemd", Label: "systemd"},
		},
	),
	newTrimmedStringConfig("panel_service_name", "systemd 服务名", "daidai-panel", "systemd 服务名称", "branding"),
	newIntConfig("max_web_sessions", "网页端会话上限", "1", "同一用户最大网页端会话数（多设备同时在线）", "security", 1, 20),
	newIntConfig("max_app_sessions", "APP 端会话上限", "1", "同一用户最大 APP 端会话数（多设备同时在线）", "security", 1, 20),
	newBoolConfig("captcha_enabled", "启用极验验证码", "false", "极验验证码开关（开启后每次登录触发）", "security"),
	newTrimmedStringConfig("captcha_id", "Captcha ID", "", "验证码平台 ID", "security"),
	newSecretStringConfig("captcha_key", "Captcha Key", "", "验证码平台密钥（服务端 Key）", "security"),
	newEnumConfig(
		"captcha_fail_mode",
		"上游异常策略",
		"open",
		"验证码上游异常策略：open=放行，strict=严格拦截",
		"security",
		[]SystemConfigOption{
			{Value: "open", Label: "宽松放行"},
			{Value: "strict", Label: "严格拦截"},
		},
	),
	// 内置 MCP 服务（issue #128），两项都默认关闭；只开 mcp_enabled 时 AI 只能用查询类工具。
	// 这两项对网页、APP 与所有 MCP 客户端都生效，所以说明里不能写「仅网页端生效」。
	// 服务端每次 HTTP 请求（ddp mcp 则是每次工具调用）都现读数据库，不需要进 reloadRuntimeConfigKeys。
	newBoolConfig(
		MCPEnabledConfigKey,
		"启用 MCP 服务",
		"false",
		"开启后，AI 客户端可以通过 POST /api/v1/mcp（HTTP）或容器内的 ddp mcp（stdio）连接面板，用开放 API 应用凭据（app_key / app_secret）或登录令牌鉴权；能访问哪些模块由应用的权限范围决定",
		"mcp",
	),
	newBoolConfig(
		MCPAllowMutationsConfigKey,
		"允许写入与执行工具",
		"false",
		"开启后，AI 可以运行、停止、启用或禁用任务，增删改环境变量，保存与运行脚本，拉取订阅；关闭时只提供查询类工具。注意：应用拥有 scripts 权限就等于可以执行任意代码",
		"mcp",
	),
	// 企业微信拉起任务（issue #145），两项都默认关闭，口径与上面 MCP 的两级开关完全一致：
	// 路由本身是公开的，但总开关关着时 handler 第一行就 403，连验签都不做。
	// 服务端每次 HTTP 请求都现读数据库，管理员改完立即生效，不需要重启。
	newBoolConfig(
		WecomTriggerEnabledConfigKey,
		"启用企业微信触发",
		"false",
		"开启后，企业微信自建应用可以通过回调（/api/v1/wecom/callback/:id）或菜单链接（/api/v1/open-api/trigger）拉起面板任务。要求企业微信服务器能访问到面板（公网可达），建议使用 80/443 端口，且在企业微信后台配好回调 URL、Token、EncodingAESKey 与可信 IP",
		"wecom",
	),
	newBoolConfig(
		WecomTriggerAllowRunConfigKey,
		"允许企业微信触发执行",
		"false",
		"开启后，企业微信侧的指令与菜单链接才会真的运行任务；关闭时回调仍可验签联通（方便先在企业微信后台把配置保存成功），但不会执行任何任务。可触发的任务范围还受每条接入配置的任务白名单限制",
		"wecom",
	),
})

var registeredSystemConfigMap = buildSystemConfigSpecMap(registeredSystemConfigSpecs)

func buildSystemConfigSpecMap(specs []systemConfigSpec) map[string]systemConfigSpec {
	result := make(map[string]systemConfigSpec, len(specs))
	for _, spec := range specs {
		result[spec.def.Key] = spec
	}
	return result
}

func newTrimmedStringConfig(key, label, defaultValue, description, group string) systemConfigSpec {
	return systemConfigSpec{
		def: SystemConfigDefinition{
			Key:   key,
			Label: label,
			// 这里存 TrimSpace 之后的值，保证 DefaultValue 与下面 normalize("") 的结果始终相等，
			// 不依赖「调用方碰巧没写多余空格」。
			DefaultValue: strings.TrimSpace(defaultValue),
			Description:  description,
			ValueType:    SystemConfigTypeString,
			Group:        group,
		},
		normalize: func(value string) (string, error) {
			value = strings.TrimSpace(value)
			if value == "" {
				return strings.TrimSpace(defaultValue), nil
			}
			return value, nil
		},
	}
}

// newSecretStringConfig 与 newTrimmedStringConfig 行为完全一致，只是额外标记 Secret=true，
// 让客户端知道这是凭据类配置、渲染时要用密码框而不是普通输入框。
func newSecretStringConfig(key, label, defaultValue, description, group string) systemConfigSpec {
	spec := newTrimmedStringConfig(key, label, defaultValue, description, group)
	spec.def.Secret = true
	return spec
}

func newValidatedStringConfig(key, label, defaultValue, description, group string, normalize func(string) (string, error)) systemConfigSpec {
	return systemConfigSpec{
		def: SystemConfigDefinition{
			Key:          key,
			Label:        label,
			DefaultValue: defaultValue,
			Description:  description,
			ValueType:    SystemConfigTypeString,
			Group:        group,
		},
		normalize: normalize,
	}
}

func newHTTPBaseURLConfig(key, label, defaultValue, description, group string) systemConfigSpec {
	return newValidatedStringConfig(key, label, defaultValue, description, group, func(value string) (string, error) {
		return normalizeHTTPBaseURLValue(value, defaultValue)
	})
}

func newAIEndpointURLConfig(key, label, defaultValue, description, group string) systemConfigSpec {
	return newValidatedStringConfig(key, label, defaultValue, description, group, func(value string) (string, error) {
		return normalizeAIEndpointURLValue(value, defaultValue)
	})
}

func newBoolConfig(key, label, defaultValue, description, group string) systemConfigSpec {
	return systemConfigSpec{
		def: SystemConfigDefinition{
			Key:          key,
			Label:        label,
			DefaultValue: normalizeBoolDefault(defaultValue),
			Description:  description,
			ValueType:    SystemConfigTypeBool,
			Group:        group,
		},
		normalize: func(value string) (string, error) {
			if strings.TrimSpace(value) == "" {
				return normalizeBoolDefault(defaultValue), nil
			}

			parsed, ok := parseBoolString(value)
			if !ok {
				return "", fmt.Errorf("配置 %s 需要布尔值", key)
			}
			return strconv.FormatBool(parsed), nil
		},
	}
}

func newIntConfig(key, label, defaultValue, description, group string, minValue, maxValue int) systemConfigSpec {
	// 取值区间原本只被下面的 normalize 闭包捕获，从不写进 def，客户端拿不到、只能等服务端 400。
	// 这里额外拷两份出来挂到 def 上：用拷贝而不是 &minValue，
	// 是为了避免调用方拿到 def 后改 *def.Min 反过来把校验逻辑也改了。
	minBound, maxBound := minValue, maxValue
	return systemConfigSpec{
		def: SystemConfigDefinition{
			Key:          key,
			Label:        label,
			DefaultValue: defaultValue,
			Description:  description,
			ValueType:    SystemConfigTypeInt,
			Group:        group,
			Min:          &minBound,
			Max:          &maxBound,
		},
		normalize: func(value string) (string, error) {
			value = strings.TrimSpace(value)
			if value == "" {
				return defaultValue, nil
			}

			parsed, err := strconv.Atoi(value)
			if err != nil {
				return "", fmt.Errorf("配置 %s 需要整数值", key)
			}
			if parsed < minValue || parsed > maxValue {
				return "", fmt.Errorf("配置 %s 需在 %d-%d 之间", key, minValue, maxValue)
			}
			return strconv.Itoa(parsed), nil
		},
	}
}

func newEnumConfig(key, label, defaultValue, description, group string, options []SystemConfigOption) systemConfigSpec {
	allowed := make(map[string]bool, len(options))
	normalizedOptions := make([]SystemConfigOption, len(options))
	for i, option := range options {
		value := strings.ToLower(strings.TrimSpace(option.Value))
		normalizedOptions[i] = SystemConfigOption{
			Value: value,
			Label: option.Label,
		}
		allowed[value] = true
	}

	defaultValue = strings.ToLower(strings.TrimSpace(defaultValue))

	return systemConfigSpec{
		def: SystemConfigDefinition{
			Key:          key,
			Label:        label,
			DefaultValue: defaultValue,
			Description:  description,
			ValueType:    SystemConfigTypeEnum,
			Group:        group,
			Options:      normalizedOptions,
		},
		normalize: func(value string) (string, error) {
			value = strings.ToLower(strings.TrimSpace(value))
			if value == "" {
				return defaultValue, nil
			}
			if !allowed[value] {
				return "", fmt.Errorf("配置 %s 的值无效", key)
			}
			return value, nil
		},
	}
}

func normalizeDefaultCronRule(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	if !panelcron.Parse(value).Valid {
		return "", fmt.Errorf("默认 Cron 规则无效")
	}
	return value, nil
}

func normalizeTimezoneValue(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		value = DefaultPanelTimezone
	}
	if value == "Local" {
		return "", fmt.Errorf("面板时区不能使用 Local，请填写明确的 IANA 时区名，例如 Asia/Shanghai")
	}
	if _, err := time.LoadLocation(value); err != nil {
		return "", fmt.Errorf("面板时区无效，请填写有效 IANA 时区名，例如 Asia/Shanghai")
	}
	return value, nil
}

func normalizeProxyURL(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}

	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("代理地址格式无效")
	}

	switch strings.ToLower(parsed.Scheme) {
	case "http", "https", "socks5", "socks5h":
		return value, nil
	default:
		return "", fmt.Errorf("代理地址仅支持 http/https/socks5/socks5h")
	}
}

func normalizeUpdateImageMirror(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}

	if !strings.Contains(value, "://") {
		value = "https://" + value
	}

	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" {
		return "", fmt.Errorf("系统更新镜像源格式无效")
	}

	switch strings.ToLower(parsed.Scheme) {
	case "http", "https":
	default:
		return "", fmt.Errorf("系统更新镜像源仅支持 http/https")
	}

	if path := strings.Trim(parsed.Path, "/"); path != "" {
		return "", fmt.Errorf("系统更新镜像源暂不支持附带路径，请只填写主机名")
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("系统更新镜像源不能带查询参数或片段")
	}

	return parsed.Host, nil
}

func normalizeBinaryUpdateProxy(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}

	if !strings.Contains(value, "://") {
		value = "https://" + value
	}

	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" {
		return "", fmt.Errorf("二进制更新加速源格式无效")
	}

	switch strings.ToLower(parsed.Scheme) {
	case "http", "https":
	default:
		return "", fmt.Errorf("二进制更新加速源仅支持 http/https")
	}

	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("二进制更新加速源不能带查询参数或片段")
	}

	pathValue := strings.TrimRight(parsed.EscapedPath(), "/")
	normalized := parsed.Scheme + "://" + parsed.Host
	if pathValue != "" {
		normalized += pathValue
	}
	return normalized + "/", nil
}

func normalizeHTTPBaseURLValue(value, defaultValue string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		value = strings.TrimSpace(defaultValue)
	}
	if value == "" {
		return "", nil
	}

	if !strings.Contains(value, "://") {
		value = "https://" + value
	}

	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" {
		return "", fmt.Errorf("API Base URL 格式无效")
	}

	switch strings.ToLower(parsed.Scheme) {
	case "http", "https":
	default:
		return "", fmt.Errorf("API Base URL 仅支持 http/https")
	}

	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("API Base URL 不能带查询参数或片段")
	}

	pathValue := strings.TrimRight(parsed.EscapedPath(), "/")
	normalized := parsed.Scheme + "://" + parsed.Host
	if pathValue != "" {
		normalized += pathValue
	}
	return normalized, nil
}

func normalizeAIEndpointURLValue(value, defaultValue string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		value = strings.TrimSpace(defaultValue)
	}
	if value == "" {
		return "", nil
	}

	if !strings.Contains(value, "://") {
		value = "https://" + value
	}

	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" {
		return "", fmt.Errorf("API 地址格式无效")
	}

	switch strings.ToLower(parsed.Scheme) {
	case "http", "https":
	default:
		return "", fmt.Errorf("API 地址仅支持 http/https")
	}

	if parsed.Fragment != "" {
		return "", fmt.Errorf("API 地址不能带片段")
	}

	pathValue := strings.TrimRight(parsed.EscapedPath(), "/")
	normalized := parsed.Scheme + "://" + parsed.Host
	if pathValue != "" {
		normalized += pathValue
	}
	if parsed.RawQuery != "" {
		normalized += "?" + parsed.RawQuery
	}
	return normalized, nil
}

func normalizeTrustedProxyCIDRs(value string) (string, error) {
	return netutil.NormalizeTrustedProxyCIDRs(value)
}

func normalizeBackupScheduleTimeValue(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "03:00", nil
	}

	parts := strings.Split(value, ":")
	if len(parts) != 2 {
		return "", fmt.Errorf("备份执行时间格式无效，应为 HH:MM")
	}

	hour, err := strconv.Atoi(strings.TrimSpace(parts[0]))
	if err != nil || hour < 0 || hour > 23 {
		return "", fmt.Errorf("备份执行时间小时无效")
	}
	minute, err := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err != nil || minute < 0 || minute > 59 {
		return "", fmt.Errorf("备份执行时间分钟无效")
	}

	return fmt.Sprintf("%02d:%02d", hour, minute), nil
}

func normalizeBackupScheduleSelectionValue(value string) (string, error) {
	allowed := map[string]bool{
		"configs":       true,
		"tasks":         true,
		"subscriptions": true,
		"env_vars":      true,
		"logs":          true,
		"scripts":       true,
		"dependencies":  true,
		"task_views":    true,
	}

	value = strings.TrimSpace(value)
	if value == "" {
		return defaultBackupScheduleSelection, nil
	}

	seen := make(map[string]bool)
	result := make([]string, 0, len(allowed))
	for _, token := range strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == ';' || r == '\n' || r == '\r' || r == '\t' || r == ' '
	}) {
		token = strings.ToLower(strings.TrimSpace(token))
		if token == "" {
			continue
		}
		if !allowed[token] {
			return "", fmt.Errorf("备份内容项无效: %s", token)
		}
		if seen[token] {
			continue
		}
		seen[token] = true
		result = append(result, token)
	}

	if len(result) == 0 {
		return "", fmt.Errorf("请至少选择一个定时备份内容项")
	}

	return strings.Join(result, ","), nil
}

func normalizeBoolDefault(value string) string {
	parsed, ok := parseBoolString(value)
	if !ok {
		return "false"
	}
	return strconv.FormatBool(parsed)
}

func parseBoolString(value string) (bool, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on":
		return true, true
	case "0", "false", "no", "off":
		return false, true
	default:
		return false, false
	}
}

func SystemConfigDefinitions() []SystemConfigDefinition {
	result := make([]SystemConfigDefinition, 0, len(registeredSystemConfigSpecs))
	for _, spec := range registeredSystemConfigSpecs {
		result = append(result, spec.def)
	}
	return result
}

func GetSystemConfigDefinition(key string) (SystemConfigDefinition, bool) {
	spec, exists := registeredSystemConfigMap[key]
	if !exists {
		return SystemConfigDefinition{}, false
	}
	return spec.def, true
}

func NormalizeSystemConfigValue(key, value string) (string, error) {
	spec, exists := registeredSystemConfigMap[key]
	if !exists {
		return value, nil
	}
	return spec.normalize(value)
}

func GetRegisteredConfig(key string) string {
	def, exists := GetSystemConfigDefinition(key)
	if !exists {
		return GetConfig(key, "")
	}
	return GetConfig(key, def.DefaultValue)
}

func GetRegisteredConfigInt(key string) int {
	def, exists := GetSystemConfigDefinition(key)
	if !exists {
		return GetConfigInt(key, 0)
	}

	defaultValue, err := strconv.Atoi(def.DefaultValue)
	if err != nil {
		defaultValue = 0
	}
	return GetConfigInt(key, defaultValue)
}

func GetRegisteredConfigBool(key string) bool {
	def, exists := GetSystemConfigDefinition(key)
	if !exists {
		return GetConfigBool(key, false)
	}

	defaultValue, _ := parseBoolString(def.DefaultValue)
	return GetConfigBool(key, defaultValue)
}

func SortedSystemConfigKeys() []string {
	keys := make([]string, 0, len(registeredSystemConfigSpecs))
	for _, spec := range registeredSystemConfigSpecs {
		keys = append(keys, spec.def.Key)
	}
	return keys
}
