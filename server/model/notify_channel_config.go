package model

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// NormalizeNotifyChannelConfig 把通知渠道的 config 归一成服务端唯一能消费的形态：
// 顶层是 JSON 对象，且每个值都是字符串。
//
// 为什么必须有这个函数：
// service.sendToChannel 里是 `var cfg map[string]string; json.Unmarshal(...)`。
// 只要 config 里出现一个非字符串值，Unmarshal 就返回 UnmarshalTypeError，
// 于是这个渠道的**所有**通知（包括「测试」按钮）从此全部失败，错误信息还是
// 一句用户完全看不懂的 `invalid config: json: cannot unmarshal bool into Go
// value of type string`。这个坑真实发生过：APP 把 smtp_ssl 写成了 JSON 布尔
// false，整个邮件渠道直接哑掉，而 Web 端读回来原样写回去，连修都修不好。
//
// 归一规则，以及为什么这么分：
//
//   - 字符串              -> 原样保留。
//   - 布尔 / 数字 / null   -> 转成字符串。这几种是**安全可逆**的：服务端消费侧
//     本来就是 notificationConfigBool / notificationConfigInt 这类字符串解析器，
//     "false" 和 false 表达的是同一个意思，转换不丢信息。
//     这条同时让老客户端写坏的记录「一编辑就自愈」，不需要额外做数据迁移。
//   - 对象 / 数组          -> 直接报错。这类**不可逆**：fmt.Sprint 出来是
//     `map[Authorization:Bearer xxx]` 这种 Go 语法垃圾，没有任何 cfg[...] 的消费者
//     能解析它，静默存下去等于把「发不出去」换成「发出去的是垃圾」。
//     而且这恰恰是 custom 渠道最容易踩的坑：headers / body 的值本身是 JSON 文本，
//     但在 config 里必须是**字符串**，用户在原始 JSON 编辑框里写成嵌套对象就会命中这条。
//
// 数字用 json.Decoder + UseNumber 解析，不能用默认的 interface{} 反序列化：
// 默认会得到 float64，fmt.Sprint(float64(1000000)) 是 "1e+06"，直接把用户填的
// 超时时间之类的整数毁掉。
//
// 注意：返回值的键顺序由 encoding/json 按字典序重排，与输入顺序无关。
// JSON 对象本来就无序，客户端都是按 schema 顺序渲染表单的，不受影响。
func NormalizeNotifyChannelConfig(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "{}", nil
	}

	decoder := json.NewDecoder(strings.NewReader(trimmed))
	decoder.UseNumber()

	var decoded interface{}
	if err := decoder.Decode(&decoded); err != nil {
		return "", fmt.Errorf("通知渠道配置不是合法的 JSON: %s", err.Error())
	}
	// Decode 只消费第一个 JSON 值，后面还有内容说明整体是坏的（例如 `{} {}`）。
	if decoder.More() {
		return "", fmt.Errorf("通知渠道配置不是合法的 JSON: 结尾存在多余内容")
	}

	object, ok := decoded.(map[string]interface{})
	if !ok {
		return "", fmt.Errorf(`通知渠道配置必须是 JSON 对象，例如 {"url":"https://example.com/webhook"}`)
	}

	normalized := make(map[string]string, len(object))
	for key, value := range object {
		text, reject := notifyChannelConfigValueToString(value)
		if reject != "" {
			return "", fmt.Errorf("通知渠道配置项 %q %s", key, reject)
		}
		normalized[key] = text
	}

	// 用 Encoder 并关掉 HTML 转义。默认的 json.Marshal 会把 URL query 里常见的
	// 尖括号和与号转成 Unicode 转义序列，用户下次打开原始 JSON 编辑框会看到一串
	// 不认识的 \u00xx，误以为配置被改坏了。
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(normalized); err != nil {
		return "", fmt.Errorf("通知渠道配置序列化失败: %s", err.Error())
	}

	return strings.TrimRight(buf.String(), "\n"), nil
}

// notifyConfigValueValidators 是通知渠道 config 的「按键」保存期校验表（#123）。
//
// 只登记「值的格式错了会被发送链路静默吞掉」的键，目前只有 proxy：
// service.NewHTTPClientWithProxy 遇到 url.Parse 失败的地址（最常见的是漏写 scheme 的 127.0.0.1:7890）
// 不报错、不打日志，静默回落进程环境代理或直连。企业微信「企业可信 IP」场景下，这表现为一个
// 没有任何提示的 60020。口径直接复用系统设置 proxy_url 的 normalizeProxyURL，
// 保证「系统设置里存不进去的代理地址，渠道里也存不进去」。
//
// 校验只作用于该渠道类型**声明过**的键：webhook 这类没声明 proxy 的渠道，config 里即使带着垃圾 proxy
// 也放行 —— 服务端根本不读那个值，拦下它只会让用户莫名其妙地存不进去。
//
// 错误文案不回显值本身：代理地址可能带账号密码。
var notifyConfigValueValidators = map[string]func(string) error{
	"proxy": func(value string) error {
		if _, err := normalizeProxyURL(value); err != nil {
			return fmt.Errorf("%s，请填写完整地址，例如 http://127.0.0.1:7890 或 socks5://127.0.0.1:1080", err.Error())
		}
		return nil
	},
}

// ValidateNotifyChannelConfig 校验一份「全新」的 config（Create 用），在 NormalizeNotifyChannelConfig 之后调用：
// 渠道声明过、且在 notifyConfigValueValidators 登记了校验器的键，只要值非空就校验。
//
// 等价于 previous 为空串的 ValidateNotifyChannelConfigChange，放行规则见那里。
func ValidateNotifyChannelConfig(channelType, normalized string) error {
	return ValidateNotifyChannelConfigChange(channelType, normalized, "")
}

// ValidateNotifyChannelConfigChange 是 Update 用的版本：只校验相对 previous（库里现存的 config）**变了**的键。
//
// 为什么只拦新值：Web 与 APP 保存时都整份回传 config。若无条件校验，存量里的非法值（例如 telegram
// 在本校验上线前就存进去的 127.0.0.1:7890）会让用户改任何别的字段都存不进去，违反
// 「坏记录必须能被编辑保存」（TestUpdateNotificationChannelHealsLegacyBrokenConfig 锁定的原则）。
// 存量非法值由发送期兜底：wecom_app 发送时会显式报错；telegram 发送期保持原样（静默回落），另议。
//
// 「变了」按去掉首尾空白后的值比较：校验器与发送链路都会先 TrimSpace，只差空白的两个值行为完全相同。
//
// previous 传空串表示「没有旧值」，所有非空值都校验 —— Create、以及 Update 切换了渠道类型时都这样传
// （旧值在新类型下从没被接受过，不能算「存量」）。
// previous 的解析是容错的：库里的旧 config 可能被老客户端写坏（布尔 / 数字 / 嵌套对象）甚至不是合法 JSON，
// 能读出字符串形态的键照常参与比较，读不出的键视为「没有旧值」。
//
// 以下一律放行（返回 nil）：未知渠道类型、键未被该渠道声明、值为空、normalized 解不开。
// normalized 解不开在调用约定下不会发生（归一那一步已经保证是「顶层对象 + 值全是字符串」），放行只是防御。
func ValidateNotifyChannelConfigChange(channelType, normalized, previous string) error {
	definition, ok := GetNotifyChannelDefinition(channelType)
	if !ok {
		return nil
	}

	var cfg map[string]string
	if err := json.Unmarshal([]byte(normalized), &cfg); err != nil {
		return nil
	}
	previousValues := notifyChannelConfigStringValues(previous)

	checked := make(map[string]bool, len(definition.Fields))
	for _, field := range definition.Fields {
		// 同一渠道允许同键多次声明（wecom 的 content_template 按 msg_type 拆成两条），只校验一次。
		if checked[field.Key] {
			continue
		}
		checked[field.Key] = true

		validate, has := notifyConfigValueValidators[field.Key]
		if !has {
			continue
		}
		value := strings.TrimSpace(cfg[field.Key])
		if value == "" || value == strings.TrimSpace(previousValues[field.Key]) {
			continue
		}
		if err := validate(value); err != nil {
			// 点名字段：Web / APP 都原样展示这句，用户要能直接对上表单里是哪个输入框。
			return fmt.Errorf("通知渠道配置项「%s」(%s) 无效：%s", field.Label, field.Key, err.Error())
		}
	}
	return nil
}

// notifyChannelConfigStringValues 尽力把库里的旧 config 读成「键 -> 字符串值」，只给「值有没有变」的比较用。
//
// 标量的转换规则与 NormalizeNotifyChannelConfig 相同（布尔 / 数字 / null 转字符串），
// 但对坏数据是容错而不是报错：顶层不是对象或 JSON 非法 -> 空表；个别键是对象 / 数组 -> 跳过该键。
// 这里一旦报错，就等于让「库里有坏值」的记录连未改动的字段都要重新过校验，正是要避免的情形。
func notifyChannelConfigStringValues(raw string) map[string]string {
	values := make(map[string]string)
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return values
	}

	decoder := json.NewDecoder(strings.NewReader(trimmed))
	decoder.UseNumber()
	var object map[string]interface{}
	if err := decoder.Decode(&object); err != nil {
		return values
	}
	for key, value := range object {
		if text, reject := notifyChannelConfigValueToString(value); reject == "" {
			values[key] = text
		}
	}
	return values
}

// notifyChannelConfigValueToString 归一单个值。
// 第二个返回值是拒绝原因（中文句子片段，供上层拼上键名）；为空串表示归一成功。
func notifyChannelConfigValueToString(value interface{}) (string, string) {
	switch typed := value.(type) {
	case string:
		return typed, ""
	case json.Number:
		return typed.String(), ""
	case bool:
		if typed {
			return "true", ""
		}
		return "false", ""
	case nil:
		// null 视为「没填」，落成空串，与前端清空输入框的效果一致。
		return "", ""
	case map[string]interface{}:
		return "", "的值必须是字符串，当前是 JSON 对象。需要传 JSON 内容时请把它整体转成字符串再填写"
	case []interface{}:
		return "", "的值必须是字符串，当前是 JSON 数组。需要传 JSON 内容时请把它整体转成字符串再填写"
	default:
		return "", "的值必须是字符串，当前类型无法识别"
	}
}
