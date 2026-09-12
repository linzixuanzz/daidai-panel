package model

import "sort"

// ============================================================================
// 通知渠道字段注册表
//
// 这份表是面板对「每个通知渠道有哪些配置字段」的唯一声明，通过
// GET /api/notifications/types 原样下发给 Web 和 APP，让两端都能按 schema
// 动态渲染表单，而不用各自再抄一份。
//
// 为什么要有这个文件：
// 这份知识以前在仓库里有四份副本 —— server/service/notifier.go 里的
// cfg["..."] 字面量（权威，但不是声明式）、web/src/views/notifications/index.vue
// 的 configFields（声明式，但长在前端）、server/service/backup_qinglong.go 的青龙
// 导入映射、web/src/views/api-docs/apiData.ts 的文档字符串。四份靠人手同步，
// 已经漂移过一次（apiData.ts 的 wecom_app 消息类型漏了 mpnews，而 notifier.go
// 和 index.vue 都有）。APP 那份只覆盖 24 个键，导致 31 个面板已支持的配置项在
// APP 里根本没有输入框。
//
// 改这个文件前必须先读的三条硬约束：
//
//  1. notifier.go 是权威，这张表向它对齐，不是反过来。
//     要加字段，先在 notifier.go 里真的读它，再在这里声明。
//  2. 键集合与 notifier.go 由 service.TestNotifySchemaCoversAllConfigKeysReadByNotifier
//     双向绑死：这里多一个假字段（用户填了服务端不读）或少一个真字段
//     （服务端读但用户填不了）都会让那条用例变红。
//  3. 字段只允许新增，不允许改名或改语义。老客户端拿到多出来的键应当无感。
// ============================================================================

// NotifyFieldWidget 是客户端渲染该字段时该用哪种控件。
//
// 只有这四种，不要再加。客户端渲染器遇到不认识的 widget 一律降级成 input
// （绝不隐藏字段），所以新增 widget 等于让所有老客户端把该字段渲染错，
// 收益远小于代价。
type NotifyFieldWidget string

const (
	NotifyWidgetInput    NotifyFieldWidget = "input"
	NotifyWidgetPassword NotifyFieldWidget = "password"
	NotifyWidgetTextarea NotifyFieldWidget = "textarea"
	NotifyWidgetSelect   NotifyFieldWidget = "select"
)

// NotifyFieldCondition 是字段的显隐条件，语义固定为「单键等值命中」：
// 同一渠道内 Key 这个字段的当前值命中 Values 之一时才显示本字段。
//
// 这个表达力刚好覆盖现有全部条件字段（只有 wecom 和 wecom_app 两个渠道，
// 都是按 msg_type 分支）。不要把它扩成表达式引擎 —— 一旦支持任意表达式，
// 服务端就得为「客户端怎么求值」负责，而每个客户端的求值器又会各自漂移。
type NotifyFieldCondition struct {
	Key    string   `json:"key"`
	Values []string `json:"values"`
}

// NotifyFieldDefinition 描述渠道 config 里的一个字段。
type NotifyFieldDefinition struct {
	Key         string            `json:"key"`
	Label       string            `json:"label"`
	Widget      NotifyFieldWidget `json:"widget"`
	Placeholder string            `json:"placeholder,omitempty"`
	// Required 的口径是严格的：当且仅当 notifier.go 对该字段单独判空并直接返回错误时才为 true。
	//
	// 刻意不包含这两类，完整理由见下方「required 口径」注释块：
	//   - 「二选一」约束（email 的 from/smtp_user、wxpusher 的 uids/topic_ids）；
	//   - notifier.go 完全不校验的 8 个渠道（serverchan/pushdeer/chanify/igot/pushover/discord/slack/custom）。
	Required bool `json:"required,omitempty"`
	// Default 是 notifier.go 在该字段为空时实际使用的回退值，从函数体里挖出来的。
	// 客户端可以拿它预填输入框：填进去和留空的最终行为完全一致。
	//
	// 「留空 = 完全不发送这个参数」的字段（bark 的 sound/group/icon/level、
	// ntfy 的 priority 等）不写 Default，因为它们没有回退值可言。
	Default  string                `json:"default,omitempty"`
	Options  []SystemConfigOption  `json:"options,omitempty"`
	ShowWhen *NotifyFieldCondition `json:"show_when,omitempty"`
}

// NotifyChannelDefinition 描述一个通知渠道。
type NotifyChannelDefinition struct {
	Type string `json:"type"`
	Name string `json:"name"`
	// Icon 是语义名（如 "mail"/"telegram"），不是图片地址。
	// 客户端各自维护「语义名 -> 本地图标」的映射（Flutter 的 IconData 是编译期常量，
	// 只能这么做），遇到不认识的名字回落默认图标。
	Icon   string                  `json:"icon,omitempty"`
	Fields []NotifyFieldDefinition `json:"fields"`
}

// ---- 声明用的小工具函数，风格对齐 system_config_registry.go 的 newIntConfig 等 ----

func notifyField(key, label string, widget NotifyFieldWidget, placeholder string) NotifyFieldDefinition {
	return NotifyFieldDefinition{Key: key, Label: label, Widget: widget, Placeholder: placeholder}
}

func notifyInput(key, label, placeholder string) NotifyFieldDefinition {
	return notifyField(key, label, NotifyWidgetInput, placeholder)
}

func notifyPassword(key, label, placeholder string) NotifyFieldDefinition {
	return notifyField(key, label, NotifyWidgetPassword, placeholder)
}

func notifyTextarea(key, label, placeholder string) NotifyFieldDefinition {
	return notifyField(key, label, NotifyWidgetTextarea, placeholder)
}

func notifySelect(key, label, placeholder string, options []SystemConfigOption) NotifyFieldDefinition {
	field := notifyField(key, label, NotifyWidgetSelect, placeholder)
	field.Options = options
	return field
}

// notifyChannelProxyPlaceholder 是渠道级正向代理字段（telegram / wecom_app 的 proxy）共用的提示文案。
// 两个渠道语义一致（留空回落系统设置 proxy_url），文案也保持一致，免得用户以为两边规则不同。
const notifyChannelProxyPlaceholder = "http/socks5 代理，如 http://127.0.0.1:7890；留空使用系统设置中的代理"

// required 标记「留空时 notifier.go 会直接返回错误」。加这个标记前先去 notifier.go 里确认。
func (f NotifyFieldDefinition) required() NotifyFieldDefinition {
	f.Required = true
	return f
}

// withDefault 记录 notifier.go 在该字段为空时实际使用的回退值。
func (f NotifyFieldDefinition) withDefault(value string) NotifyFieldDefinition {
	f.Default = value
	return f
}

// showWhen 只支持单键等值命中，见 NotifyFieldCondition。
func (f NotifyFieldDefinition) showWhen(key string, values ...string) NotifyFieldDefinition {
	f.ShowWhen = &NotifyFieldCondition{Key: key, Values: values}
	return f
}

// ---- required 口径（整份表统一遵守，不要逐条另立标准）----
//
// Required = true 当且仅当 notifier.go 对该字段**单独判空并直接返回错误**。
// 除此之外一律不标，即使凭直觉「这个明显是必填」。
//
// 为什么定这么死：Required 是唯一一个会**阻止用户保存**的标记。口径一旦变成
// 「看着像必填就标」，就没有任何客观依据可以复核，下一个人加字段时只能靠猜，
// 而猜错的方向是「本来能存的配置突然存不进去」——这比少一个红星严重得多。
//
// 由此产生两类刻意的「看起来该必填但没标」，都在对应渠道就近写了注释：
//
//  1. 二选一约束（单键 Required 表达不了 OR）：
//     email 的 smtp_user / from、wxpusher 的 uids / topic_ids。
//  2. notifier.go 完全不校验的渠道，共 8 个：
//     serverchan / pushdeer / chanify / igot / pushover / discord / slack / custom。
//     要让它们变成必填，正确做法是先去 notifier.go 补判空（那是改发送行为，
//     需要单独评估），补完再回来加 required —— 而不是先在表单上拦。
//
// ---- 渠道声明 ----
//
// 顺序即 GET /api/notifications/types 的返回顺序，必须与改造前 handler 里那份
// 硬编码列表一致（Web 的类型下拉直接按这个顺序渲染）。
var registeredNotifyChannels = []NotifyChannelDefinition{
	{
		Type: "webhook", Name: "Webhook", Icon: "webhook",
		Fields: []NotifyFieldDefinition{
			notifyInput("url", "Webhook URL", "https://example.com/webhook").required(),
		},
	},
	{
		Type: "email", Name: "邮件", Icon: "mail",
		// smtp_user 和 from 是「二选一」关系：notifier.go 里 from 为空时回落成 smtp_user，
		// 两者都空才报「发件人为空」。单键 Required 表达不了 OR，所以两个都不标 required，
		// 由 label 里的「(可选)」提示用户。
		//
		// smtp_pass 同样不标 required：smtp.SendMail 只在服务端广告 AUTH 时才认证，
		// 内网无认证中继填空密码是能正常发信的，标成必填会把这类用户挡在保存之外。
		Fields: []NotifyFieldDefinition{
			notifyInput("smtp_host", "SMTP 主机", "smtp.qq.com").required(),
			notifyInput("smtp_port", "SMTP 端口", "465").required(),
			// smtp_ssl 的值必须是字符串 "auto"/"true"/"false"，不能是 JSON 布尔。
			// 服务端把整份 config 反序列化成 map[string]string，出现 JSON bool 会让
			// 整个渠道的所有通知（含测试按钮）直接挂掉。写入侧由
			// NormalizeNotifyChannelConfig 兜底归一。
			notifySelect("smtp_ssl", "SSL 连接", "自动：465 端口启用", []SystemConfigOption{
				{Value: "auto", Label: "自动 (465 启用)"},
				{Value: "true", Label: "启用 SSL"},
				{Value: "false", Label: "关闭 SSL"},
			}).withDefault("auto"),
			notifyInput("smtp_user", "邮箱账号", "user@example.com"),
			notifyPassword("smtp_pass", "邮箱密码/授权码", "SMTP 授权码"),
			notifyInput("to", "收件人", "多个收件人用逗号分隔").required(),
			notifyInput("from", "发件人 (可选)", "留空则使用邮箱账号"),
		},
	},
	{
		Type: "telegram", Name: "Telegram", Icon: "telegram",
		Fields: []NotifyFieldDefinition{
			notifyInput("token", "Bot Token", "从 @BotFather 获取").required(),
			notifyInput("chat_id", "Chat ID", "聊天/群组 ID").required(),
			notifyInput("message_thread_id", "Topic ID (可选)", "群组话题 ID，留空则发到默认话题"),
			notifyInput("api_host", "API 地址 (可选)", "自定义 API 地址，留空使用官方").
				withDefault("https://api.telegram.org"),
			// placeholder 与 wecom_app 的 proxy 统一成同一句（#123）：两者语义完全一致
			// （留空回落系统设置 proxy_url），只改文案、不改语义。
			// 不写 Default：留空是「回落」而不是某个固定值，理由同 wecom_app。
			notifyInput("proxy", "代理地址 (可选)", notifyChannelProxyPlaceholder),
		},
	},
	{
		Type: "dingtalk", Name: "钉钉", Icon: "dingtalk",
		Fields: []NotifyFieldDefinition{
			notifyInput("webhook", "Webhook URL", "https://oapi.dingtalk.com/robot/send?access_token=xxx").required(),
			notifyInput("secret", "加签秘钥 (可选)", "安全设置中的 SEC 开头的秘钥"),
			// 注意与 wecom 相反：钉钉在 notifier.go 里是「只有显式等于 text 才发文本，
			// 其余一律走 markdown」，所以留空的实际效果是 markdown 而不是 text。
			notifySelect("msg_type", "消息类型", "选择钉钉机器人消息类型", []SystemConfigOption{
				{Value: "text", Label: "文本"},
				{Value: "markdown", Label: "Markdown"},
			}).withDefault("markdown"),
		},
	},
	{
		Type: "wecom", Name: "企业微信机器人", Icon: "wecom",
		Fields: []NotifyFieldDefinition{
			notifyInput("webhook", "Webhook URL", "https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=xxx").required(),
			notifySelect("msg_type", "消息类型", "选择企业微信机器人消息类型", []SystemConfigOption{
				{Value: "text", Label: "文本"},
				{Value: "markdown", Label: "Markdown"},
				{Value: "markdown_v2", Label: "Markdown V2"},
				{Value: "image", Label: "图片"},
				{Value: "news", Label: "图文"},
				{Value: "template_card", Label: "模版卡片"},
			}).withDefault("text"),
			// content_template 在 text 和 markdown 分支下文案不同，所以拆成两条声明。
			// 同键重复是允许的，但两条的 show_when 必须互斥 —— 由
			// TestNotifyChannelDuplicateKeysAreMutuallyExclusive 兜底。
			notifyTextarea("content_template", "文本模板", "支持 {{title}} 和 {{content}}，留空默认 {{title}}\\n{{content}}").
				showWhen("msg_type", "text"),
			notifyTextarea("mentioned_list", "提醒成员 (可选)", "多个成员用逗号、分号或换行分隔，可填 @all").
				showWhen("msg_type", "text"),
			notifyTextarea("mentioned_mobile_list", "提醒手机号 (可选)", "多个手机号用逗号、分号或换行分隔，可填 @all").
				showWhen("msg_type", "text"),
			notifyTextarea("content_template", "内容模板", "支持 {{title}} 和 {{content}} 占位符").
				showWhen("msg_type", "markdown", "markdown_v2"),
			notifyTextarea("image_base64", "图片 Base64", "填写图片的 Base64 内容").
				showWhen("msg_type", "image").required(),
			notifyInput("image_md5", "图片 MD5", "填写图片内容对应的 MD5 值").
				showWhen("msg_type", "image").required(),
			notifyTextarea("news_articles", "图文 Articles(JSON)",
				`[{"title":"{{title}}","description":"{{content}}","url":"https://example.com","picurl":"https://example.com/demo.png"}]`).
				showWhen("msg_type", "news").required(),
			notifyTextarea("template_card_payload", "卡片配置(JSON)",
				`{"card_type":"text_notice","main_title":{"title":"{{title}}","desc":"{{content}}"}}`).
				showWhen("msg_type", "template_card").required(),
		},
	},
	{
		Type: "wecom_app", Name: "企业微信应用", Icon: "wecom",
		Fields: []NotifyFieldDefinition{
			notifyInput("corp_id", "企业 ID", "企业微信 CorpID").required(),
			notifyPassword("secret", "应用 Secret", "应用 Secret").required(),
			notifyInput("agent_id", "Agent ID", "应用 AgentId").required(),
			notifyInput("base_url", "反代基础地址 (可选)", "留空使用 https://qyapi.weixin.qq.com，也可填你的 Nginx 反代地址"),
			// proxy 是正向代理（http/https/socks5），与上面的 base_url（反向代理基础地址）是两件事，可以叠加：
			// 先按 base_url 拼出请求地址，再经 proxy 发出。取 token 与发消息两次请求都走它。
			// 留空回落系统设置 proxy_url，再空走进程环境变量 HTTP(S)_PROXY / 直连 —— 与 telegram 完全一致。
			//
			// 主要场景是企业微信「企业可信 IP」（#123，2022-06 之后新建的自建应用都受限）：
			// 让请求从已加入可信 IP 的机器出网，避免 errcode 60020。
			//
			// 不写 Default：留空的语义是「回落」，没有固定的回退值（口径见 NotifyFieldDefinition.Default）。
			// 不标 required：notifier.go 不对它判空。非法值在保存期（ValidateNotifyChannelConfig）
			// 与发送期（sendWecomAppWithContext）都会显式报错，不会静默直连。
			notifyInput("proxy", "代理地址 (可选)", notifyChannelProxyPlaceholder),
			notifyInput("to_user", "成员账号 (可选)", "多个成员用 | 分隔，留空默认 @all"),
			notifyInput("to_party", "部门 ID (可选)", "多个部门用 | 分隔"),
			notifyInput("to_tag", "标签 ID (可选)", "多个标签用 | 分隔"),
			notifySelect("msg_type", "消息类型", "选择企业微信应用消息类型", []SystemConfigOption{
				{Value: "text", Label: "文本"},
				{Value: "markdown", Label: "Markdown"},
				{Value: "image", Label: "图片"},
				{Value: "file", Label: "文件"},
				{Value: "video", Label: "视频"},
				{Value: "news", Label: "图文"},
				{Value: "mpnews", Label: "图文消息 (mpnews)"},
				{Value: "template_card", Label: "模版卡片"},
			}).withDefault("text"),
			notifyTextarea("content_template", "内容模板", "支持 {{title}} 和 {{content}} 占位符").
				showWhen("msg_type", "text", "markdown"),
			notifyInput("media_id", "Media ID", "调用上传临时素材接口后得到的 media_id").
				showWhen("msg_type", "image", "file", "video").required(),
			notifyTextarea("news_articles", "图文 Articles(JSON)",
				`[{"title":"{{title}}","description":"{{content}}","url":"https://example.com","picurl":"https://example.com/demo.png"}]`).
				showWhen("msg_type", "news").required(),
			notifyTextarea("mpnews_articles", "图文消息 Articles(JSON)",
				`[{"title":"{{title}}","thumb_media_id":"MEDIA_ID","author":"Author","content_source_url":"https://example.com","content":"<p>{{content}}</p>","digest":"Digest description"}]`).
				showWhen("msg_type", "mpnews").required(),
			notifyTextarea("template_card_payload", "卡片配置(JSON)",
				`{"card_type":"text_notice","main_title":{"title":"{{title}}","desc":"{{content}}"}}`).
				showWhen("msg_type", "template_card").required(),
			// Web 上「仅企业内分享 (2)」只在 msg_type=mpnews 时出现在选项里。
			// 本 schema 刻意不支持「条件 options」（那要么得引入表达式引擎，要么得让每个
			// 客户端各写一份联动逻辑），改成第 3 个选项常驻。
			// notifier.go 这里只是 notificationConfigInt 透传给企业微信，服务端不做取值校验，
			// 所以常驻不会产生服务端行为差异；用错值由企业微信返回错误码告知。
			notifySelect("safe", "保密消息", "默认 0", []SystemConfigOption{
				{Value: "0", Label: "否 (0)"},
				{Value: "1", Label: "是 (1)"},
				{Value: "2", Label: "仅企业内分享 (2)"},
			}).withDefault("0"),
			notifySelect("enable_id_trans", "ID 转译", "默认 0", []SystemConfigOption{
				{Value: "0", Label: "关闭 (0)"},
				{Value: "1", Label: "开启 (1)"},
			}).withDefault("0"),
			notifySelect("enable_duplicate_check", "重复检查", "默认 0", []SystemConfigOption{
				{Value: "0", Label: "关闭 (0)"},
				{Value: "1", Label: "开启 (1)"},
			}).withDefault("0"),
			notifyInput("duplicate_check_interval", "去重间隔(秒)", "默认 1800，最大 14400").withDefault("1800"),
		},
	},
	{
		Type: "bark", Name: "Bark", Icon: "bark",
		Fields: []NotifyFieldDefinition{
			notifyInput("key", "Device Key", "打开 Bark App 复制推送地址中的 Key，如 https://api.day.app/xxxxxx 中的 xxxxxx").required(),
			notifyInput("server", "服务器 (可选)", "默认 https://api.day.app").withDefault("https://api.day.app"),
			notifyInput("sound", "推送声音 (可选)", "如 birdsong，留空使用默认"),
			notifyInput("group", "推送分组 (可选)", "消息分组名称"),
			notifyInput("icon", "图标 URL (可选)", "https://example.com/icon.png"),
			notifySelect("level", "时效性 (可选)", "推送优先级", []SystemConfigOption{
				{Value: "active", Label: "默认 (active)"},
				{Value: "timeSensitive", Label: "时效性 (timeSensitive)"},
				{Value: "passive", Label: "被动 (passive)"},
			}),
			notifyInput("url", "跳转 URL (可选)", "点击通知后跳转的链接"),
		},
	},
	{
		Type: "pushplus", Name: "PushPlus", Icon: "pushplus",
		Fields: []NotifyFieldDefinition{
			notifyInput("token", "Token", "PushPlus 用户 Token").required(),
			notifyInput("topic", "群组编码 (可选)", "一对多推送时的群组编码"),
			// 只放这 4 个通用排版模板。PushPlus 官方现在还有 cloudMonitor / jenkins /
			// route / pay / form / doc / excel，**刻意不补齐**，别看到「少了」就往里加：
			//   - cloudMonitor / jenkins / route / pay 是特定场景的专用排版，面板发的是
			//     任务执行结果，套上去只会把消息排得面目全非；
			//   - form / doc / excel 还必须额外带一个 pushId 参数，而面板没有「表单/文档」
			//     这个概念，也就没地方产出 pushId —— 摆进下拉框等于给用户挖坑：选了必失败。
			notifySelect("template", "模板 (可选)", "消息模板", []SystemConfigOption{
				{Value: "html", Label: "默认 (html)"},
				{Value: "json", Label: "JSON"},
				{Value: "txt", Label: "纯文本"},
				{Value: "markdown", Label: "Markdown"},
			}),
			// 不写 Default：留空等于完全不发送 channel 参数，由 PushPlus 按账号默认
			// （微信公众号）处理，没有可言的回退值。写了 Default 会让老渠道编辑保存一次
			// 就凭空多出一个键，行为反而变了。
			//
			// 选项顺序：免费渠道在前，消耗积分的 sms / voice 垫底，避免用户顺手选中要花钱的。
			// qq 是官方新增的免费渠道，排在同样需要填 option 的 webhook / cp 之后、mail 之前，
			// 让「选了它下面会多出一个编码输入框」的三个渠道在下拉里挨着。
			notifySelect("channel", "发送渠道 (可选)", "留空按 PushPlus 账号默认（微信公众号）；QQ 机器人建议配 txt 或 markdown 模板，单条约 2000 字", []SystemConfigOption{
				{Value: "wechat", Label: "微信公众号 (wechat)"},
				{Value: "app", Label: "App (app)"},
				{Value: "extension", Label: "浏览器扩展 (extension)"},
				{Value: "webhook", Label: "第三方 Webhook (webhook)"},
				{Value: "clawbot", Label: "微信 ClawBot (clawbot)"},
				{Value: "cp", Label: "企业微信应用 (cp)"},
				{Value: "qq", Label: "QQ 机器人 (qq)"},
				{Value: "mail", Label: "邮箱 (mail)"},
				{Value: "sms", Label: "短信 (sms，消耗 10 积分/条)"},
				{Value: "voice", Label: "语音 (voice，消耗 30 积分/条)"},
			}),
			// option 是 PushPlus 对原 webhook 参数的改名，同一个键在三种渠道下含义完全不同：
			//   webhook -> PushPlus 后台的 webhook 编码（是编码，不是 URL）
			//   cp      -> 企业微信自定义应用编码
			//   qq      -> 目标 QQ 群的「配置编码」（先在 PushPlus 渠道配置里为该群建一条群配置）；
			//              发给**个人**时留空即可，填了反而会被当成群消息。
			// 其余渠道不需要这个参数，所以用 show_when 收起来，避免误填。
			//
			// 官方还规定 QQ 群消息不能同时带 topic —— 但 show_when 只能单键等值命中，
			// 表达不了「channel=qq 且 option 非空时隐藏 topic」，所以这条只在文案里提示，
			// 不在 schema 里硬拦（硬拦要么扩表达力、要么把 topic 对 QQ 一刀切禁掉，两者代价都更大）。
			notifyInput("option", "渠道编码 (按所选渠道填)", "webhook 填 webhook 编码；企业微信应用填自定义应用编码；QQ 填群配置编码（发给个人留空，且群消息不能再填群组编码）").showWhen("channel", "webhook", "cp", "qq"),
		},
	},
	{
		Type: "serverchan", Name: "Server酱", Icon: "serverchan",
		Fields: []NotifyFieldDefinition{
			// notifier.go 的 sendServerchan 不做任何判空，直接把 key 拼进 URL。
			// 按本文件的 required 口径，这里不标 required。
			notifyInput("key", "SendKey", "Server酱的 SendKey (SCT...)"),
		},
	},
	{
		Type: "feishu", Name: "飞书", Icon: "feishu",
		Fields: []NotifyFieldDefinition{
			notifyInput("webhook", "Webhook URL", "https://open.feishu.cn/open-apis/bot/v2/hook/xxx").required(),
			notifyInput("secret", "加签秘钥 (可选)", "安全设置中的签名校验秘钥"),
		},
	},
	{
		Type: "gotify", Name: "Gotify", Icon: "gotify",
		Fields: []NotifyFieldDefinition{
			notifyInput("server", "服务器地址", "https://gotify.example.com").required(),
			notifyInput("token", "App Token", "Gotify 应用 Token").required(),
			notifyInput("priority", "优先级 (可选)", "0-10，默认 5").withDefault("5"),
		},
	},
	{
		Type: "pushdeer", Name: "PushDeer", Icon: "pushdeer",
		Fields: []NotifyFieldDefinition{
			notifyInput("key", "PushKey", "PushDeer 的 PushKey"),
			notifyInput("server", "服务器 (可选)", "默认 https://api2.pushdeer.com").
				withDefault("https://api2.pushdeer.com"),
		},
	},
	{
		Type: "pushme", Name: "PushMe", Icon: "pushme",
		Fields: []NotifyFieldDefinition{
			notifyInput("key", "PushMe Key", "PushMe 的 push_key").required(),
			notifyInput("server", "接口地址 (可选)", "默认 https://push.i-i.me").
				withDefault("https://push.i-i.me"),
			notifyInput("message_type", "消息类型 (可选)", "按 PushMe 支持的 type 值填写"),
		},
	},
	{
		Type: "chanify", Name: "Chanify", Icon: "chanify",
		Fields: []NotifyFieldDefinition{
			notifyInput("token", "Token", "Chanify 设备 Token"),
			notifyInput("server", "服务器 (可选)", "默认 https://api.chanify.net").
				withDefault("https://api.chanify.net"),
		},
	},
	{
		Type: "igot", Name: "iGot", Icon: "igot",
		Fields: []NotifyFieldDefinition{
			notifyInput("key", "Key", "iGot 推送 Key"),
		},
	},
	{
		Type: "qmsg", Name: "Qmsg", Icon: "qmsg",
		Fields: []NotifyFieldDefinition{
			notifyInput("key", "Qmsg Key", "Qmsg 酱的 Key").required(),
			// notifier.go 只判断 mode 是否等于 group，其余一律走 send。
			notifySelect("mode", "发送模式", "选择 send 或 group", []SystemConfigOption{
				{Value: "send", Label: "私聊/默认 (send)"},
				{Value: "group", Label: "群发 (group)"},
			}).withDefault("send"),
			notifyInput("qq", "QQ 号/群号 (可选)", "留空则按 Qmsg 端默认配置发送"),
		},
	},
	{
		Type: "pushover", Name: "Pushover", Icon: "pushover",
		Fields: []NotifyFieldDefinition{
			notifyInput("token", "API Token", "应用 API Token"),
			notifyInput("user", "User Key", "用户 Key"),
		},
	},
	{
		Type: "discord", Name: "Discord", Icon: "discord",
		Fields: []NotifyFieldDefinition{
			notifyInput("webhook", "Webhook URL", "https://discord.com/api/webhooks/..."),
		},
	},
	{
		Type: "slack", Name: "Slack", Icon: "slack",
		Fields: []NotifyFieldDefinition{
			notifyInput("webhook", "Webhook URL", "https://hooks.slack.com/services/..."),
		},
	},
	{
		Type: "ntfy", Name: "ntfy", Icon: "ntfy",
		Fields: []NotifyFieldDefinition{
			notifyInput("topic", "Topic", "订阅主题名称").required(),
			notifyInput("server", "服务器 (可选)", "默认 https://ntfy.sh").withDefault("https://ntfy.sh"),
			notifyInput("token", "Token (可选)", "访问令牌，用于私有主题"),
			// 留空时 notifier.go 完全不发 Priority 头，没有服务端回退值，所以不写 Default。
			notifySelect("priority", "优先级 (可选)", "消息优先级", []SystemConfigOption{
				{Value: "1", Label: "最低 (1)"},
				{Value: "2", Label: "低 (2)"},
				{Value: "3", Label: "默认 (3)"},
				{Value: "4", Label: "高 (4)"},
				{Value: "5", Label: "紧急 (5)"},
			}),
		},
	},
	{
		Type: "wxpusher", Name: "WxPusher / ClawBot(iLink)", Icon: "wxpusher",
		// uids 和 topic_ids 是「至少填一个」的关系，单键 Required 表达不了 OR，
		// 两个都不标 required，由 label 的「(可选)」和服务端错误提示兜底。
		Fields: []NotifyFieldDefinition{
			notifyInput("app_token", "App Token", "WxPusher 的 appToken").required(),
			notifyTextarea("uids", "UID 列表 (可选)", "多个 UID 可用分号、逗号或换行分隔"),
			notifyTextarea("topic_ids", "Topic ID 列表 (可选)", "多个 Topic ID 可用分号、逗号或换行分隔"),
			notifySelect("content_type", "内容类型 (可选)", "默认文本消息", []SystemConfigOption{
				{Value: "1", Label: "文本 (1)"},
				{Value: "2", Label: "HTML (2)"},
				{Value: "3", Label: "Markdown (3)"},
			}).withDefault("1"),
			notifyInput("url", "原文链接 (可选)", "消息详情页跳转地址"),
			// 留空时不发 verifyPayType 字段，没有服务端回退值，所以不写 Default。
			notifySelect("verify_pay_type", "付费校验 (可选)", "默认不校验", []SystemConfigOption{
				{Value: "0", Label: "不校验 (0)"},
				{Value: "1", Label: "仅付费用户 (1)"},
				{Value: "2", Label: "仅未订阅/已过期 (2)"},
			}),
			notifyInput("server", "接口地址 (可选)", "默认 https://wxpusher.zjiecode.com/api/send/message").
				withDefault("https://wxpusher.zjiecode.com/api/send/message"),
		},
	},
	{
		Type: "custom", Name: "自定义", Icon: "custom",
		// 「自定义」不等于「没有固定字段」：notifier.go 的 sendCustomWebhook 恰好只读
		// 下面这 5 个键。headers 和 body 的值本身是 JSON 文本，但在 config 里必须是
		// 字符串，不能写成嵌套 JSON 对象 —— 那会让整份 config 反序列化失败。
		Fields: []NotifyFieldDefinition{
			// sendCustomWebhook 全程不判空（url 为空时是 http.NewRequest 自己报的错，
			// 不是服务端的显式判断），按本文件的 required 口径不标 required。
			notifyInput("url", "URL", "https://example.com/api/notify"),
			notifySelect("method", "Method", "请求方法", []SystemConfigOption{
				{Value: "POST", Label: "POST"},
				{Value: "GET", Label: "GET"},
				{Value: "PUT", Label: "PUT"},
			}).withDefault("POST"),
			notifyInput("content_type", "Content-Type", "默认 application/json").withDefault("application/json"),
			notifyTextarea("headers", "Headers (JSON)", `{"Authorization": "Bearer xxx"}`),
			notifyTextarea("body", "Body 模板", "使用 {{title}} 和 {{content}} 作为占位符").
				withDefault(`{"title":"{{title}}","content":"{{content}}"}`),
		},
	},
}

var registeredNotifyChannelMap = buildNotifyChannelMap(registeredNotifyChannels)

func buildNotifyChannelMap(channels []NotifyChannelDefinition) map[string]NotifyChannelDefinition {
	result := make(map[string]NotifyChannelDefinition, len(channels))
	for _, channel := range channels {
		result[channel.Type] = channel
	}
	return result
}

// NotifyChannelDefinitions 返回全部渠道声明的深拷贝。
// 深拷贝是必要的：Options 是切片、ShowWhen 是指针，直接返回注册表里那份会让任何调用方
// 都能改坏全局状态。这个接口只在管理端 HTTP 请求里调用，拷贝开销可以忽略。
func NotifyChannelDefinitions() []NotifyChannelDefinition {
	result := make([]NotifyChannelDefinition, 0, len(registeredNotifyChannels))
	for _, channel := range registeredNotifyChannels {
		result = append(result, cloneNotifyChannelDefinition(channel))
	}
	return result
}

// GetNotifyChannelDefinition 按类型取单个渠道声明。
func GetNotifyChannelDefinition(channelType string) (NotifyChannelDefinition, bool) {
	channel, exists := registeredNotifyChannelMap[channelType]
	if !exists {
		return NotifyChannelDefinition{}, false
	}
	return cloneNotifyChannelDefinition(channel), true
}

// NotifyChannelConfigKeys 返回所有渠道声明过的 config 键并集（去重、字典序）。
// 主要给 service 包那条「schema 与 notifier.go 双向绑死」的用例用。
func NotifyChannelConfigKeys() []string {
	seen := make(map[string]struct{})
	for _, channel := range registeredNotifyChannels {
		for _, field := range channel.Fields {
			seen[field.Key] = struct{}{}
		}
	}

	keys := make([]string, 0, len(seen))
	for key := range seen {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func cloneNotifyChannelDefinition(channel NotifyChannelDefinition) NotifyChannelDefinition {
	fields := make([]NotifyFieldDefinition, 0, len(channel.Fields))
	for _, field := range channel.Fields {
		copied := field
		if len(field.Options) > 0 {
			copied.Options = append([]SystemConfigOption(nil), field.Options...)
		}
		if field.ShowWhen != nil {
			condition := *field.ShowWhen
			condition.Values = append([]string(nil), field.ShowWhen.Values...)
			copied.ShowWhen = &condition
		}
		fields = append(fields, copied)
	}
	channel.Fields = fields
	return channel
}
