package service

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"log"
	"net"
	"net/http"
	"net/smtp"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"daidai-panel/database"
	"daidai-panel/model"
)

var (
	wecomAppTokenURL = "https://qyapi.weixin.qq.com/cgi-bin/gettoken"
	wecomAppSendURL  = "https://qyapi.weixin.qq.com/cgi-bin/message/send"

	smtpSendMail                = smtp.SendMail
	smtpSendMailWithImplicitTLS = sendSMTPMailWithImplicitTLS
)

type NotificationDispatchOptions struct {
	ChannelIDs []uint
	Context    map[string]string
}

type NotificationDispatchResult struct {
	SentCount    int
	FailedCount  int
	ChannelNames []string
	Errors       []string
}

func SendNotification(title, content string) {
	SendNotificationWithOptions(title, content, NotificationDispatchOptions{})
}

func SendNotificationWithOptions(title, content string, options NotificationDispatchOptions) {
	channels, err := loadEnabledNotificationChannels(options.ChannelIDs)
	if err != nil {
		log.Printf("load notification channels failed: %v", err)
		return
	}

	if len(channels) == 0 {
		if len(options.ChannelIDs) > 0 {
			log.Printf("notification skipped: no enabled channels matched ids=%v", options.ChannelIDs)
		} else {
			// 广播 0 命中在这之前是完全静默的：这条分支上的调用方（资源告警、登录通知、
			// 静默更新结果、未绑定渠道的任务通知）全都不看返回值，也没有任何日志。
			// 加了 bound 语义后，用户只要把所有渠道都设成「绑定推送」，系统通知就会全部人间蒸发
			// 且零线索，所以这里必须留一行 warn 作为唯一可查的痕迹。
			log.Printf("warn: notification broadcast skipped: no channel with push_scope=default is enabled (title=%q)", title)
		}
		return
	}

	for _, ch := range channels {
		go dispatchNotificationToChannel(ch, title, content, options.Context)
	}
}

func SendNotificationToChannel(channel *model.NotifyChannel, title, content string) error {
	return sendToChannel(*channel, title, content, nil)
}

func SendNotificationSyncWithOptions(title, content string, options NotificationDispatchOptions) (NotificationDispatchResult, error) {
	result := NotificationDispatchResult{}

	channels, err := loadEnabledNotificationChannels(options.ChannelIDs)
	if err != nil {
		return result, err
	}
	if len(channels) == 0 {
		if len(options.ChannelIDs) > 0 {
			// 这句被 handler 的回归测试逐字断言，改文案会挂，也会让老客户端的错误匹配失效。
			return result, fmt.Errorf("未找到已启用的通知渠道")
		}
		return result, fmt.Errorf("暂无参与广播的默认推送渠道")
	}

	for _, ch := range channels {
		if err := sendToChannel(ch, title, content, options.Context); err != nil {
			result.FailedCount++
			result.Errors = append(result.Errors, fmt.Sprintf("%s: %v", ch.Name, err))
			continue
		}
		result.SentCount++
		result.ChannelNames = append(result.ChannelNames, ch.Name)
	}

	return result, nil
}

func dispatchNotificationToChannel(ch model.NotifyChannel, title, content string, context map[string]string) {
	if err := sendToChannel(ch, title, content, context); err != nil {
		log.Printf("send notification via channel %d(%s) failed: %v", ch.ID, ch.Name, err)
	}
}

// loadEnabledNotificationChannels 是全后端唯一的渠道筛选点，两种语义在这里分叉：
//
//   - 定向（channelIDs 非空）：按 ID 精确命中，**完全忽略 push_scope**。
//     「绑定推送」渠道存在的意义就是只在被显式指定时才推，这里再叠一层 push_scope 过滤
//     会让它永远发不出去，等于把功能做废。
//   - 广播（channelIDs 为空）：只命中「默认推送」渠道。
func loadEnabledNotificationChannels(channelIDs []uint) ([]model.NotifyChannel, error) {
	var channels []model.NotifyChannel
	query := database.DB.Where("enabled = ?", true)
	if ids := uniqueNotificationChannelIDs(channelIDs); len(ids) > 0 {
		query = query.Where("id IN ?", ids)
	} else {
		// 必须写「不等于 bound」而不是「等于 default」。
		// 这一列的语义是「空即默认」，只有明确写着 bound 才排除出广播。
		// 老库补列、手工改库、以及未来任何忘了填这一列的写入路径，都可能留下空串或 NULL；
		// 写成等值比较，这些行会静默退出广播，表现成「升级之后突然一条通知都收不到」，
		// 而且没有任何报错可查 —— 方向反了的默认值是这类功能最贵的 bug。
		// 用 COALESCE 兜住 NULL：SQL 里 `NULL <> 'bound'` 求值为 NULL（不成立），
		// 不兜的话 NULL 行照样会被漏掉。
		query = query.Where("COALESCE(push_scope, '') <> ?", model.NotifyPushScopeBound)
	}
	if err := query.Order("created_at DESC, id DESC").Find(&channels).Error; err != nil {
		return nil, err
	}
	return channels, nil
}

func uniqueNotificationChannelIDs(ids []uint) []uint {
	if len(ids) == 0 {
		return nil
	}

	seen := make(map[uint]struct{}, len(ids))
	result := make([]uint, 0, len(ids))
	for _, id := range ids {
		if id == 0 {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		result = append(result, id)
	}
	return result
}

func recordNotificationSend(channelID uint, sentAt time.Time) {
	if channelID == 0 || database.DB == nil {
		return
	}

	todayKey := sentAt.Format("2006-01-02")
	var channel model.NotifyChannel
	if err := database.DB.Select("id", "today_send_count", "today_send_date").First(&channel, channelID).Error; err != nil {
		log.Printf("load notification channel send stats failed: %v", err)
		return
	}

	nextCount := 1
	if channel.TodaySendDate == todayKey {
		nextCount = channel.TodaySendCount + 1
	}

	if err := database.DB.Model(&model.NotifyChannel{}).
		Where("id = ?", channelID).
		Updates(map[string]interface{}{
			"today_send_count": nextCount,
			"today_send_date":  todayKey,
		}).Error; err != nil {
		log.Printf("update notification channel send stats failed: %v", err)
	}
}

func sendToChannel(ch model.NotifyChannel, title, content string, context map[string]string) error {
	var cfg map[string]string
	if err := json.Unmarshal([]byte(ch.Config), &cfg); err != nil {
		return fmt.Errorf("invalid config: %w", err)
	}

	// 多面板共用同一通知渠道时，可在标题前缀附带面板名称以便区分；留空则不附带。
	if label := strings.TrimSpace(model.GetRegisteredConfig("notify_panel_label")); label != "" {
		title = "【" + label + "】" + title
	}

	var err error
	switch ch.Type {
	case "webhook":
		err = sendWebhook(cfg, title, content)
	case "email":
		err = sendEmail(cfg, title, content)
	case "telegram":
		err = sendTelegram(cfg, title, content)
	case "dingtalk":
		err = sendDingtalk(cfg, title, content)
	case "wecom":
		err = sendWecomWithContext(cfg, title, content, context)
	case "wecom_app":
		err = sendWecomAppWithContext(cfg, title, content, context)
	case "bark":
		err = sendBarkWithContext(cfg, title, content, context)
	case "pushplus":
		err = sendPushplus(cfg, title, content)
	case "serverchan":
		err = sendServerchan(cfg, title, content)
	case "feishu":
		err = sendFeishu(cfg, title, content)
	case "gotify":
		err = sendGotify(cfg, title, content)
	case "pushdeer":
		err = sendPushdeer(cfg, title, content)
	case "pushme":
		err = sendPushMe(cfg, title, content)
	case "chanify":
		err = sendChanify(cfg, title, content)
	case "igot":
		err = sendIgot(cfg, title, content)
	case "qmsg":
		err = sendQmsg(cfg, title, content)
	case "pushover":
		err = sendPushover(cfg, title, content)
	case "discord":
		err = sendDiscord(cfg, title, content)
	case "slack":
		err = sendSlack(cfg, title, content)
	case "ntfy":
		err = sendNtfy(cfg, title, content)
	case "wxpusher":
		err = sendWxPusher(cfg, title, content)
	case "custom":
		err = sendCustomWebhook(cfg, title, content)
	default:
		err = fmt.Errorf("未知的通知渠道类型: %s", ch.Type)
	}

	if err != nil {
		return err
	}

	recordNotificationSend(ch.ID, time.Now())
	return nil
}

// notifyResultCheck 判断厂商是否在 HTTP 200 里返回了业务失败。
//
// 大部分推送服务在参数错误、额度不足、渠道未开通时仍然回 200，只在响应体里带一个业务
// 错误码。面板以前只看 HTTP 状态码，这类失败会被一律显示成「发送成功」。
//
// 口径刻意保守：只有能【确定】是失败时才返回 error。响应体不是 JSON 对象、
// 缺少约定字段、或字段类型对不上时一律放行，保证这层校验不会把本来能用的渠道改红。
// 只给已经核对过官方响应契约的渠道挂校验器，没核对过的保持原样。
type notifyResultCheck func(body []byte) error

func httpPost(url string, body interface{}, headers map[string]string) error {
	return httpPostWithClient(NewHTTPClient(10*time.Second), url, body, headers)
}

func httpPostWithClient(client *http.Client, url string, body interface{}, headers map[string]string) error {
	return httpPostCheckedWithClient(client, url, body, headers, nil)
}

func httpPostChecked(url string, body interface{}, headers map[string]string, check notifyResultCheck) error {
	return httpPostCheckedWithClient(NewHTTPClient(10*time.Second), url, body, headers, check)
}

func httpPostCheckedWithClient(client *http.Client, url string, body interface{}, headers map[string]string, check notifyResultCheck) error {
	data, err := json.Marshal(body)
	if err != nil {
		return err
	}

	req, err := http.NewRequest("POST", url, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	// 限长读取：推送接口的响应体都很小，这里既避免异常大响应吃内存，
	// 也让连接能被正常复用（原来成功路径完全不读 body）。
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	if resp.StatusCode >= 400 {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(respBody))
	}
	if check != nil {
		return check(respBody)
	}
	return nil
}

// parseNotifyResponseObject 只接受 JSON 对象；纯文本、数组、空响应一律当作「无法判定」。
func parseNotifyResponseObject(body []byte) (map[string]interface{}, bool) {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return nil, false
	}
	payload := map[string]interface{}{}
	if err := json.Unmarshal(trimmed, &payload); err != nil {
		return nil, false
	}
	return payload, true
}

// notifyResponseNumber 兼容错误码被写成字符串的情况（部分服务返回 "code": "0"）。
func notifyResponseNumber(value interface{}) (float64, bool) {
	switch v := value.(type) {
	case float64:
		return v, true
	case string:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		if err != nil {
			return 0, false
		}
		return parsed, true
	default:
		return 0, false
	}
}

func notifyResponseMessage(payload map[string]interface{}, keys ...string) string {
	for _, key := range keys {
		if text, ok := payload[key].(string); ok {
			if trimmed := strings.TrimSpace(text); trimmed != "" {
				return trimmed
			}
		}
	}
	return ""
}

// checkNotifyCodeField 适用于「HTTP 200 + JSON 里一个数字业务码」的响应。
func checkNotifyCodeField(codeKey string, successValues []float64, messageKeys ...string) notifyResultCheck {
	return func(body []byte) error {
		payload, ok := parseNotifyResponseObject(body)
		if !ok {
			return nil
		}
		raw, exists := payload[codeKey]
		if !exists {
			return nil
		}
		code, ok := notifyResponseNumber(raw)
		if !ok {
			return nil
		}
		for _, success := range successValues {
			if code == success {
				return nil
			}
		}
		if message := notifyResponseMessage(payload, messageKeys...); message != "" {
			return fmt.Errorf("推送服务返回失败（%s=%v）：%s", codeKey, raw, message)
		}
		return fmt.Errorf("推送服务返回失败（%s=%v）", codeKey, raw)
	}
}

// combineNotifyChecks 依次执行，返回第一个确定的失败。
// 用于同一个厂商存在多种响应体形态的情况（例如飞书的 code 与 StatusCode 两代字段）。
func combineNotifyChecks(checks ...notifyResultCheck) notifyResultCheck {
	return func(body []byte) error {
		for _, check := range checks {
			if err := check(body); err != nil {
				return err
			}
		}
		return nil
	}
}

// checkTelegramResult：Telegram Bot API 固定返回 {"ok":true|false,"description":"..."}。
func checkTelegramResult(body []byte) error {
	payload, ok := parseNotifyResponseObject(body)
	if !ok {
		return nil
	}
	okValue, exists := payload["ok"].(bool)
	if !exists || okValue {
		return nil
	}
	if message := notifyResponseMessage(payload, "description"); message != "" {
		return fmt.Errorf("Telegram 返回失败：%s", message)
	}
	return fmt.Errorf("Telegram 返回失败")
}

var (
	// errcode 是微信系（企业微信机器人 / 企业微信应用 / 钉钉）统一的业务码字段。
	checkWecomStyleResult = checkNotifyCodeField("errcode", []float64{0}, "errmsg")
	// 飞书自定义机器人新老两代响应字段并存，两个都查。
	checkFeishuResult = combineNotifyChecks(
		checkNotifyCodeField("code", []float64{0}, "msg"),
		checkNotifyCodeField("StatusCode", []float64{0}, "StatusMessage"),
	)
	checkPushplusResult   = checkNotifyCodeField("code", []float64{200}, "msg")
	checkServerchanResult = checkNotifyCodeField("code", []float64{0}, "message", "msg")
	checkBarkResult       = checkNotifyCodeField("code", []float64{200}, "message")
)

func sendWebhook(cfg map[string]string, title, content string) error {
	webhookURL := cfg["url"]
	if webhookURL == "" {
		return fmt.Errorf("Webhook URL 为空")
	}
	body := map[string]string{"title": title, "content": content}
	return httpPost(webhookURL, body, nil)
}

func sendEmail(cfg map[string]string, title, content string) error {
	host := strings.TrimSpace(cfg["smtp_host"])
	port := strings.TrimSpace(cfg["smtp_port"])
	user := strings.TrimSpace(cfg["smtp_user"])
	pass := cfg["smtp_pass"]
	to := strings.TrimSpace(cfg["to"])
	from := strings.TrimSpace(cfg["from"])
	if from == "" {
		from = user
	}
	if host == "" {
		return fmt.Errorf("SMTP 主机为空")
	}
	if port == "" {
		return fmt.Errorf("SMTP 端口为空")
	}
	recipients := splitEmailRecipients(to)
	if len(recipients) == 0 {
		return fmt.Errorf("收件人为空")
	}
	if from == "" {
		return fmt.Errorf("发件人为空")
	}

	addr := net.JoinHostPort(host, port)
	auth := smtp.PlainAuth("", user, pass, host)

	msg := fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: %s\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n%s",
		from, to, title, content)

	if smtpImplicitSSLEnabled(cfg, port) {
		return smtpSendMailWithImplicitTLS(addr, host, auth, from, recipients, []byte(msg))
	}
	return smtpSendMail(addr, auth, from, recipients, []byte(msg))
}

func sendSMTPMailWithImplicitTLS(addr, host string, auth smtp.Auth, from string, to []string, msg []byte) error {
	conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 10 * time.Second}, "tcp", addr, &tls.Config{
		ServerName: host,
		MinVersion: tls.VersionTLS12,
	})
	if err != nil {
		return err
	}

	client, err := smtp.NewClient(conn, host)
	if err != nil {
		_ = conn.Close()
		return err
	}
	defer client.Close()

	if auth != nil {
		if ok, _ := client.Extension("AUTH"); !ok {
			return fmt.Errorf("smtp: server doesn't support AUTH")
		}
		if err := client.Auth(auth); err != nil {
			return err
		}
	}
	if err := client.Mail(from); err != nil {
		return err
	}
	for _, recipient := range to {
		if err := client.Rcpt(recipient); err != nil {
			return err
		}
	}
	writer, err := client.Data()
	if err != nil {
		return err
	}
	if _, err := writer.Write(msg); err != nil {
		_ = writer.Close()
		return err
	}
	if err := writer.Close(); err != nil {
		return err
	}
	return client.Quit()
}

func smtpImplicitSSLEnabled(cfg map[string]string, port string) bool {
	for _, key := range []string{"smtp_ssl", "smtp_use_ssl", "use_ssl", "enable_ssl", "ssl"} {
		raw, exists := cfg[key]
		if !exists {
			continue
		}
		raw = strings.TrimSpace(raw)
		if raw == "" || strings.EqualFold(raw, "auto") {
			return strings.TrimSpace(port) == "465"
		}
		return notificationConfigBool(raw, false)
	}
	return strings.TrimSpace(port) == "465"
}

func splitEmailRecipients(raw string) []string {
	fields := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ';' || r == '\n' || r == '\r' || r == '\t'
	})

	result := make([]string, 0, len(fields))
	for _, field := range fields {
		field = strings.TrimSpace(field)
		if field != "" {
			result = append(result, field)
		}
	}
	return result
}

func sendTelegram(cfg map[string]string, title, content string) error {
	token := cfg["token"]
	chatID := cfg["chat_id"]
	if token == "" || chatID == "" {
		return fmt.Errorf("Telegram token 或 chat_id 为空")
	}
	apiHost := "https://api.telegram.org"
	if v := cfg["api_host"]; v != "" {
		apiHost = strings.TrimRight(v, "/")
	}
	apiURL := fmt.Sprintf("%s/bot%s/sendMessage", apiHost, token)
	client := NewHTTPClientWithProxy(10*time.Second, strings.TrimSpace(cfg["proxy"]))

	messages := buildTelegramMessages(title, content)
	for _, message := range messages {
		body := map[string]interface{}{
			"chat_id": chatID,
			"text":    message,
		}
		if v := strings.TrimSpace(cfg["message_thread_id"]); v != "" {
			if threadID, err := strconv.Atoi(v); err == nil {
				body["message_thread_id"] = threadID
			}
		}
		if err := httpPostCheckedWithClient(client, apiURL, body, nil, checkTelegramResult); err != nil {
			var uerr *url.Error
			if errors.As(err, &uerr) {
				// apiURL 的路径里带着 bot token（/bot<token>/sendMessage）。网络错误是 *url.Error，原样返回会把
				// 整条 URL 连同 token 带进测试按钮回显、/notifications/send 响应与日志（#123）。
				// 剥掉 URL 之后只剩「dial tcp …」「unsupported protocol scheme ""」这类原因，用户看不出是哪个请求、
				// 该改哪个输入框，所以补一层前缀：只留 scheme://host（api_host 可能是带鉴权路径的反代地址）。
				where := redactProxyURL(apiHost)
				if where == redactedUnparsableURL {
					// 占位句自带括号，直接套进前缀会变成「（（地址无法解析））」；点名 api_host，用户才知道该改哪里。
					where = "api_host 地址无法解析"
				}
				return fmt.Errorf("请求 Telegram API（%s）失败: %w", where, stripRequestURL(err))
			}
			// 业务错误（checkTelegramResult）与 HTTP 状态错误不加前缀。但 HTTP≥400 的正文可能是正向代理的错误页，
			// 里面回显着带 bot token 的完整请求 URL（api_host 为 http:// 且配了代理时），所以要脱敏 ——
			// 只替换「/bot」后面那段，不碰状态码和业务文案（理由见 redactTelegramBotPath）。
			// 自建网关或反代的业务错误 description 也可能回显请求路径（HTTP 200，走不到 HTTP≥400 这条），
			// 所以业务错误同样要脱敏，不能收窄成只处理 HTTP 状态错误。
			// 文本没变就原样返回，不重新包一层。
			if redacted := redactTelegramBotPath(err.Error(), token); redacted != err.Error() {
				return errors.New(redacted)
			}
			return err
		}
	}
	return nil
}

func sendDingtalk(cfg map[string]string, title, content string) error {
	webhook := cfg["webhook"]
	if webhook == "" {
		return fmt.Errorf("钉钉 Webhook URL 为空")
	}
	if secret := cfg["secret"]; secret != "" {
		timestamp := fmt.Sprintf("%d", time.Now().UnixMilli())
		stringToSign := timestamp + "\n" + secret
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write([]byte(stringToSign))
		sign := url.QueryEscape(base64.StdEncoding.EncodeToString(mac.Sum(nil)))
		sep := "&"
		if !strings.Contains(webhook, "?") {
			sep = "?"
		}
		webhook = webhook + sep + "timestamp=" + timestamp + "&sign=" + sign
	}

	var body map[string]interface{}
	if strings.ToLower(strings.TrimSpace(cfg["msg_type"])) == "text" {
		body = map[string]interface{}{
			"msgtype": "text",
			"text": map[string]string{
				"content": title + "\n" + content,
			},
		}
	} else {
		mdContent := strings.ReplaceAll(content, "\n", "  \n")
		body = map[string]interface{}{
			"msgtype": "markdown",
			"markdown": map[string]string{
				"title": title,
				"text":  fmt.Sprintf("### %s  \n%s", title, mdContent),
			},
		}
	}
	return httpPostChecked(webhook, body, nil, checkWecomStyleResult)
}

func sendWecom(cfg map[string]string, title, content string) error {
	return sendWecomWithContext(cfg, title, content, nil)
}

func sendWecomWithContext(cfg map[string]string, title, content string, context map[string]string) error {
	webhook := cfg["webhook"]
	if webhook == "" {
		return fmt.Errorf("企业微信机器人 Webhook URL 为空")
	}

	msgType := strings.ToLower(strings.TrimSpace(cfg["msg_type"]))
	if msgType == "" {
		msgType = "text"
	}

	body := map[string]interface{}{"msgtype": msgType}
	switch msgType {
	case "text":
		textBody := map[string]interface{}{
			"content": renderNotificationTemplateWithContext(cfg["content_template"], title, content, "{{title}}\n{{content}}", context),
		}
		if mentioned := splitNotificationTargets(cfg["mentioned_list"]); len(mentioned) > 0 {
			textBody["mentioned_list"] = mentioned
		}
		if mobiles := splitNotificationTargets(cfg["mentioned_mobile_list"]); len(mobiles) > 0 {
			textBody["mentioned_mobile_list"] = mobiles
		}
		body["text"] = textBody
	case "markdown", "markdown_v2":
		body[msgType] = map[string]string{
			"content": renderNotificationTemplateWithContext(cfg["content_template"], title, content, "**{{title}}**\n{{content}}", context),
		}
	case "image":
		base64Data := strings.TrimSpace(cfg["image_base64"])
		md5Value := strings.TrimSpace(cfg["image_md5"])
		if base64Data == "" || md5Value == "" {
			return fmt.Errorf("企业微信机器人图片消息需要 image_base64 和 image_md5")
		}
		body["image"] = map[string]string{
			"base64": base64Data,
			"md5":    md5Value,
		}
	case "news":
		articles, err := parseNotificationJSONTemplateWithContext(cfg["news_articles"], title, content, context)
		if err != nil {
			return fmt.Errorf("企业微信机器人图文消息配置无效: %w", err)
		}
		articleList, ok := articles.([]interface{})
		if !ok || len(articleList) == 0 {
			return fmt.Errorf("企业微信机器人图文消息需要至少一条 articles")
		}
		body["news"] = map[string]interface{}{
			"articles": articleList,
		}
	case "template_card":
		cardPayload, err := parseNotificationJSONTemplateWithContext(cfg["template_card_payload"], title, content, context)
		if err != nil {
			return fmt.Errorf("企业微信机器人模版卡片配置无效: %w", err)
		}
		cardBody, ok := cardPayload.(map[string]interface{})
		if !ok || len(cardBody) == 0 {
			return fmt.Errorf("企业微信机器人模版卡片配置不能为空对象")
		}
		body["template_card"] = cardBody
	default:
		return fmt.Errorf("不支持的企业微信机器人消息类型: %s", msgType)
	}

	return httpPostChecked(webhook, body, nil, checkWecomStyleResult)
}

func sendWecomApp(cfg map[string]string, title, content string) error {
	return sendWecomAppWithContext(cfg, title, content, nil)
}

func sendWecomAppWithContext(cfg map[string]string, title, content string, context map[string]string) error {
	corpID := strings.TrimSpace(cfg["corp_id"])
	secret := strings.TrimSpace(cfg["secret"])
	agentID := strings.TrimSpace(cfg["agent_id"])
	if corpID == "" || secret == "" || agentID == "" {
		return fmt.Errorf("企业微信应用 corp_id、secret 或 agent_id 为空")
	}

	agentIDInt, err := strconv.Atoi(agentID)
	if err != nil || agentIDInt <= 0 {
		return fmt.Errorf("企业微信应用 agent_id 无效")
	}

	// 渠道级正向代理（#123）。必须写成 cfg["proxy"] 字面量：schema 绑定用例的 AST 扫描只认这种写法，
	// 挪进参数不叫 cfg 的 helper 就会从扫描器视野里消失。
	proxy := strings.TrimSpace(cfg["proxy"])
	if proxy != "" {
		// 必须在发出任何请求之前显式失败：NewHTTPClientWithProxy 遇到解析失败的地址（最常见的是漏写 scheme 的
		// 127.0.0.1:7890）会静默回落进程环境代理或直连，可信 IP 场景下表现为一个没有任何提示的 60020。
		// 口径与系统设置 proxy_url 相同。存量记录、备份恢复与青龙导入进来的值不经过保存期校验，全靠这里兜底。
		// 错误信息不回显地址本身：代理地址可能带账号密码。
		if _, err := model.NormalizeSystemConfigValue("proxy_url", proxy); err != nil {
			return fmt.Errorf("企业微信应用代理地址无效：%v（请填写完整地址，如 http://127.0.0.1:7890 或 socks5://127.0.0.1:1080）", err)
		}
	}

	tokenURL := fmt.Sprintf(
		"%s?corpid=%s&corpsecret=%s",
		resolveWecomAppEndpoint(cfg, wecomAppTokenURL, "/cgi-bin/gettoken"),
		url.QueryEscape(corpID),
		url.QueryEscape(secret),
	)
	// 取 token 与发消息共用这一个 client，所以两次请求都经过渠道代理。渠道代理为空时回落系统设置 proxy_url，
	// 再空走进程环境变量 HTTP(S)_PROXY / 直连 —— 与 telegram 同一套优先级（见 http_client.go）。
	// base_url 与 proxy 可以叠加：先按 base_url 拼出地址，再经 proxy 发出。
	client := NewHTTPClientWithProxy(10*time.Second, proxy)
	tokenResp, err := client.Get(tokenURL)
	if err != nil {
		// tokenURL 的 query 里带着 corpsecret，不能让 *url.Error 把整条 URL 带进错误信息。
		return fmt.Errorf("获取企业微信应用 access_token 失败: %w", stripRequestURL(err))
	}
	defer tokenResp.Body.Close()

	tokenBody, _ := io.ReadAll(tokenResp.Body)
	if tokenResp.StatusCode >= 400 {
		// 正文可能是正向代理的错误页（Squid 默认模板带 %U），里面回显着带 corpsecret 的完整请求 URL。
		// stripRequestURL 只管得到 *url.Error，这里的正文要单独脱敏。
		return fmt.Errorf("获取企业微信应用 access_token 失败: HTTP %d: %s", tokenResp.StatusCode,
			redactSecrets(strings.TrimSpace(string(tokenBody)), secret))
	}

	var tokenPayload struct {
		ErrCode     int    `json:"errcode"`
		ErrMsg      string `json:"errmsg"`
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(tokenBody, &tokenPayload); err != nil {
		return fmt.Errorf("解析企业微信应用 access_token 响应失败: %w", err)
	}
	if tokenPayload.ErrCode != 0 {
		// 60020 时追加「企业可信 IP」排查提示，其它 errcode 提示为空串、原有文案一字不变。
		// errmsg 也按 corpsecret 脱敏：企业微信官方的 errmsg 不回显请求参数，但 base_url 指向的自建网关或反代
		// 可能把带 corpsecret 的请求行写进 errmsg（HTTP 200，走不到上面 HTTP≥400 的脱敏）。
		// secret 是正常长度时官方 errmsg 里没有它、也远不到截断长度，文案原样保留（TestSendWecomAppReturnsEnterpriseError 逐字锁住）。
		// 已知取舍：secret 误填成 1–2 个字符这类极短值时，errmsg 里相同的字符也会被换成 ***（如 secret 为 "in" 时
		// 「invalid credential」变成「***valid credential」）。仍保留整条替换、不改成只锚 corpsecret= 后面那段：
		// 网关以别的形态回显密钥时锚定就兜不住了，这里宁可改坏极端配置下的文案，也不削弱脱敏。
		return fmt.Errorf("获取企业微信应用 access_token 失败: %s%s", redactSecrets(tokenPayload.ErrMsg, secret),
			wecomAppTrustedIPHint(tokenPayload.ErrCode, proxy, cfg["base_url"]))
	}
	if strings.TrimSpace(tokenPayload.AccessToken) == "" {
		return fmt.Errorf("企业微信应用 access_token 为空")
	}

	sendURL := fmt.Sprintf(
		"%s?access_token=%s",
		resolveWecomAppEndpoint(cfg, wecomAppSendURL, "/cgi-bin/message/send"),
		url.QueryEscape(tokenPayload.AccessToken),
	)
	msgType := strings.ToLower(strings.TrimSpace(cfg["msg_type"]))
	if msgType == "" {
		msgType = "text"
	}

	receivers := map[string]string{
		"touser":  strings.TrimSpace(cfg["to_user"]),
		"toparty": strings.TrimSpace(cfg["to_party"]),
		"totag":   strings.TrimSpace(cfg["to_tag"]),
	}
	if receivers["touser"] == "" && receivers["toparty"] == "" && receivers["totag"] == "" {
		receivers["touser"] = "@all"
	}

	enableDuplicateCheck := notificationConfigInt(cfg["enable_duplicate_check"], 0)
	duplicateCheckInterval := notificationConfigInt(cfg["duplicate_check_interval"], 1800)
	if duplicateCheckInterval <= 0 {
		duplicateCheckInterval = 1800
	}
	if duplicateCheckInterval > 4*3600 {
		duplicateCheckInterval = 4 * 3600
	}

	body := map[string]interface{}{
		"msgtype":                  msgType,
		"agentid":                  agentIDInt,
		"touser":                   receivers["touser"],
		"toparty":                  receivers["toparty"],
		"totag":                    receivers["totag"],
		"enable_duplicate_check":   enableDuplicateCheck,
		"duplicate_check_interval": duplicateCheckInterval,
	}

	switch msgType {
	case "text":
		body["safe"] = notificationConfigInt(cfg["safe"], 0)
		body["enable_id_trans"] = notificationConfigInt(cfg["enable_id_trans"], 0)
		body["text"] = map[string]string{
			"content": renderNotificationTemplateWithContext(cfg["content_template"], title, content, "{{title}}\n{{content}}", context),
		}
	case "markdown":
		body["markdown"] = map[string]string{
			"content": renderNotificationTemplateWithContext(cfg["content_template"], title, content, "**{{title}}**\n{{content}}", context),
		}
	case "image", "file", "video":
		body["safe"] = notificationConfigInt(cfg["safe"], 0)
		mediaID := strings.TrimSpace(cfg["media_id"])
		if mediaID == "" {
			return fmt.Errorf("企业微信应用 %s 消息需要 media_id", msgType)
		}
		body[msgType] = map[string]string{
			"media_id": mediaID,
		}
	case "news":
		articles, err := parseNotificationJSONTemplateWithContext(cfg["news_articles"], title, content, context)
		if err != nil {
			return fmt.Errorf("企业微信应用图文消息配置无效: %w", err)
		}
		articleList, ok := articles.([]interface{})
		if !ok || len(articleList) == 0 {
			return fmt.Errorf("企业微信应用图文消息需要至少一条 articles")
		}
		body["news"] = map[string]interface{}{
			"articles": articleList,
		}
	case "mpnews":
		body["safe"] = notificationConfigInt(cfg["safe"], 0)
		body["enable_id_trans"] = notificationConfigInt(cfg["enable_id_trans"], 0)
		articles, err := parseNotificationJSONTemplateWithContext(cfg["mpnews_articles"], title, content, context)
		if err != nil {
			return fmt.Errorf("企业微信应用 mpnews 配置无效: %w", err)
		}
		articleList, ok := articles.([]interface{})
		if !ok || len(articleList) == 0 {
			return fmt.Errorf("企业微信应用 mpnews 需要至少一条 articles")
		}
		// mpnews 正文按 HTML 渲染，纯 \n 不会换行；仅把 content 键的换行替换为 <br>，
		// 一次处理 \r\n 与 \n 避免残留孤立 \r。不触碰 digest/title（纯文本路径，\n 正常）。
		contentLineBreakReplacer := strings.NewReplacer("\r\n", "<br>", "\n", "<br>")
		for _, item := range articleList {
			article, ok := item.(map[string]interface{})
			if !ok {
				continue
			}
			if raw, ok := article["content"].(string); ok {
				article["content"] = contentLineBreakReplacer.Replace(raw)
			}
		}
		body["mpnews"] = map[string]interface{}{
			"articles": articleList,
		}
	case "template_card":
		body["enable_id_trans"] = notificationConfigInt(cfg["enable_id_trans"], 0)
		cardPayload, err := parseNotificationJSONTemplateWithContext(cfg["template_card_payload"], title, content, context)
		if err != nil {
			return fmt.Errorf("企业微信应用模版卡片配置无效: %w", err)
		}
		cardBody, ok := cardPayload.(map[string]interface{})
		if !ok || len(cardBody) == 0 {
			return fmt.Errorf("企业微信应用模版卡片配置不能为空对象")
		}
		body["template_card"] = cardBody
	default:
		return fmt.Errorf("不支持的企业微信应用消息类型: %s", msgType)
	}

	data, err := json.Marshal(body)
	if err != nil {
		return err
	}

	req, err := http.NewRequest(http.MethodPost, sendURL, bytes.NewReader(data))
	if err != nil {
		// 解析失败同样是 *url.Error，带着含 access_token 的完整 URL。
		return stripRequestURL(err)
	}
	req.Header.Set("Content-Type", "application/json")

	sendResp, err := client.Do(req)
	if err != nil {
		// sendURL 的 query 里带着 access_token，不能让 *url.Error 把整条 URL 带进错误信息。
		return fmt.Errorf("发送企业微信应用消息失败: %w", stripRequestURL(err))
	}
	defer sendResp.Body.Close()

	sendBody, _ := io.ReadAll(sendResp.Body)
	if sendResp.StatusCode >= 400 {
		// 同取 token：代理错误页回显的是带 access_token 的 message/send 地址。
		// 这一步的 URL 里没有 secret，顺带把它也脱掉没有代价（反代服务器的错误页回显什么不由我们决定）。
		return fmt.Errorf("发送企业微信应用消息失败: HTTP %d: %s", sendResp.StatusCode,
			redactSecrets(strings.TrimSpace(string(sendBody)), tokenPayload.AccessToken, secret))
	}

	var sendPayload struct {
		ErrCode        int    `json:"errcode"`
		ErrMsg         string `json:"errmsg"`
		InvalidUser    string `json:"invaliduser"`
		InvalidParty   string `json:"invalidparty"`
		InvalidTag     string `json:"invalidtag"`
		UnlicensedUser string `json:"unlicenseduser"`
	}
	if err := json.Unmarshal(sendBody, &sendPayload); err != nil {
		return fmt.Errorf("解析企业微信应用发送响应失败: %w", err)
	}
	if sendPayload.ErrCode != 0 {
		var details []string
		if v := strings.TrimSpace(sendPayload.InvalidUser); v != "" {
			details = append(details, "invaliduser="+v)
		}
		if v := strings.TrimSpace(sendPayload.InvalidParty); v != "" {
			details = append(details, "invalidparty="+v)
		}
		if v := strings.TrimSpace(sendPayload.InvalidTag); v != "" {
			details = append(details, "invalidtag="+v)
		}
		if v := strings.TrimSpace(sendPayload.UnlicensedUser); v != "" {
			details = append(details, "unlicenseduser="+v)
		}
		// 60020 时追加「企业可信 IP」排查提示（两个分支都挂），其它 errcode 提示为空串、原有文案一字不变。
		hint := wecomAppTrustedIPHint(sendPayload.ErrCode, proxy, cfg["base_url"])
		// errmsg 脱敏的理由同取 token。这一步的请求行带的是 access_token，secret 顺带也脱掉（同 HTTP≥400 分支）。
		errMsg := redactSecrets(sendPayload.ErrMsg, tokenPayload.AccessToken, secret)
		if len(details) > 0 {
			return fmt.Errorf("发送企业微信应用消息失败: %s (%s)%s", errMsg, strings.Join(details, ", "), hint)
		}
		return fmt.Errorf("发送企业微信应用消息失败: %s%s", errMsg, hint)
	}

	return nil
}

// stripRequestURL 去掉 *url.Error 里的完整请求 URL，只留底层原因（#123）。
//
// 为什么需要：client.Get / client.Do / http.NewRequest 失败时返回 *url.Error，其 Error() 形如
// `Get "<完整 URL>": <原因>`。企业微信应用 gettoken 的 query 带 corpsecret、message/send 带 access_token，
// telegram 的路径带 bot token。这段文字会流到测试按钮回显、/notifications/send 的响应
// （operator 角色与 Open API 可见，而渠道配置本身只有 admin 能读）、托管脚本日志与面板日志。
//
// 剩下的原因只含主机与端口，例如 `proxyconnect tcp: dial tcp 127.0.0.1:7890: connect: connection refused`，
// 代理地址里的账号密码不会出现在里面。只减少信息、不改变成败。
//
// 应当作用在原始错误上、再用 fmt.Errorf 包装。若传入的是已经包过一层的错误，errors.As 仍能找到里面的
// *url.Error，但返回的只是它的底层原因，外层前缀会一起丢掉（依然不含 URL）。
func stripRequestURL(err error) error {
	var uerr *url.Error
	if !errors.As(err, &uerr) {
		return err
	}
	if uerr.Err == nil {
		return fmt.Errorf("%s 请求失败", uerr.Op)
	}
	return uerr.Err
}

// notifyErrorBodyEchoLimit 是错误信息里回显响应正文的上限（字节）。
// 代理 / 反代的错误页往往是整页 HTML，原样拼进错误会把测试按钮回显和日志撑得很长；512 字节足够看清状态与原因。
const notifyErrorBodyEchoLimit = 512

// redactSecrets 把 s 里出现的密钥换成 ***，再截断到 notifyErrorBodyEchoLimit 字节（#123）。
//
// 为什么需要：HTTP≥400 时会把响应正文拼进错误信息，而正向代理的错误页会回显完整请求 URL ——
// 企业微信应用 gettoken 带 corpsecret、message/send 带 access_token，telegram 的路径带 bot token。
// stripRequestURL 只处理 *url.Error，管不到这条路。https 目标走 CONNECT，Go 只取状态行，不受影响。
// 企业微信应用 errcode 分支的 errmsg 也走这里：自建网关或反代可能把请求行写进 errmsg。
// telegram 不直接用它，见 redactTelegramBotPath。
//
// 每个密钥按 secretForms 列出的四种形态替换。先去掉首尾空白再求形态：转义是逐字符的，核心部分的转义一定是
// 整串转义的子串，所以无论两端的空白被转成了什么，核心都会被替换掉。空密钥必须跳过：ReplaceAll 的 old 为空串时
// 会在每个字符之间都插入 ***。
//
// 两个约束决定了顺序：
//   - 先长后短：一个密钥是另一个的子串时，先替换短的会把长的拆开，留下一截明文；
//   - 先替换再截断：先截断的话，恰好跨在截断点上的密钥只剩前半截，匹配不上，前半截原样漏出去。
//
// 截断退到 UTF-8 字符边界，不留半个汉字。不传密钥时只做截断。
func redactSecrets(s string, secrets ...string) string {
	var cores []string
	for _, secret := range secrets {
		if core := strings.TrimSpace(secret); core != "" {
			cores = append(cores, core)
		}
	}
	for _, form := range secretForms(cores...) {
		s = strings.ReplaceAll(s, form, "***")
	}

	if len(s) <= notifyErrorBodyEchoLimit {
		return s
	}
	cut := notifyErrorBodyEchoLimit
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "…（已截断）"
}

// secretForms 列出每个密钥在文本里可能出现的形态，去重后先长后短（理由见 redactSecrets）。
//
// 形态有四种：
//   - 原文；
//   - url.QueryEscape：query 里的写法（企业微信的 corpsecret、access_token）；
//   - EscapedPath：Go 写请求行时路径里的写法（encodePath，不转义 / ; ,），telegram 的 /bot<token> 就以这种形态出现在请求行里；
//   - url.PathEscape：按段转义的回显（/ ; , 也会被转义）。它与 EscapedPath 恰好在这几个字符上不同，
//     token 同时含这类字符和需要转义的字符（如 "123:ab/c d"）时，只有 EscapedPath 对得上请求行，所以两种都要。
//
// 只转义、不 TrimSpace：传原文还是去掉首尾空白的核心，由调用方决定。空串跳过，理由同 redactSecrets。
func secretForms(secrets ...string) []string {
	var forms []string
	seen := make(map[string]bool)
	for _, secret := range secrets {
		if secret == "" {
			continue
		}
		for _, form := range []string{secret, url.QueryEscape(secret), (&url.URL{Path: secret}).EscapedPath(), url.PathEscape(secret)} {
			if !seen[form] {
				seen[form] = true
				forms = append(forms, form)
			}
		}
	}
	sort.SliceStable(forms, func(i, j int) bool { return len(forms[i]) > len(forms[j]) })
	return forms
}

// redactTelegramBotPath 把 s 里「/bot」后面紧跟的 bot token 换成 ***，再截断到 notifyErrorBodyEchoLimit 字节（#123）。
//
// 为什么不像企业微信那样对整条文本做 redactSecrets：token 被误填成 "4"、"Not" 这种短值时，整条替换会把
// 「HTTP 404」改成「HTTP ***0***」、把 description 改成「*** Found」，状态码和业务文案都看不懂了。
// bot token 只出现在请求路径 /bot<token>/sendMessage 里，所以只替换跟在「/bot」后面的那段，别处不碰。
//
// 覆盖两类回显：
//   - 「/bot」原样、后面的 token 是 secretForms 四种形态之一：请求行原样回显（EscapedPath），或按 query、
//     按段转义了路径（如 "/bot123456%3Atok-xyz"）。替换成 /bot***。
//   - 「/bot」连同 token 整串又被转义了一遍：拦截页把整条原 URL 转义后塞进 query 参数或 mailto 正文时，
//     「/bot」变成了 %2Fbot，上一类锚不住。所以再对 "/bot"+token 整串求形态，替换时保留该形态里「/bot」的写法
//     （原文、EscapedPath 是 /bot，QueryEscape、PathEscape 是 %2Fbot），后接 ***。
//
// 仍不覆盖：token 脱离「/bot」单独回显（例如 description 里只写了 token）时不脱敏。这是锚定「/bot」换来的代价，
// 官方 Bot API 不会这样回显。
//
// apiURL 拼的是没去空白的 token，所以原文和去掉首尾空白的核心都要求形态：开头带空白时请求行里是 /bot%20<核心>，
// 只按核心匹配的话「/bot」锚不住，整段原样漏出去。核心那一组兜住「结尾空白被写成别的样子（如 +）」和
// 「两端空白在回显里被整个去掉」；开头空白被写成 + 等其它字符时，夹在 /bot 与核心之间，锚不住。
func redactTelegramBotPath(s, token string) string {
	for _, form := range secretForms(token, strings.TrimSpace(token)) {
		s = strings.ReplaceAll(s, "/bot"+form, "/bot***")
	}
	// 整串形态。token 全是空白时核心为空，不能拼出光秃秃的 "/bot" 去替换：那样每个 /bot 后面都会被插一个 ***。
	var whole []string
	for _, t := range []string{token, strings.TrimSpace(token)} {
		if t != "" {
			whole = append(whole, "/bot"+t)
		}
	}
	for _, form := range secretForms(whole...) {
		prefix := "/bot"
		if strings.HasPrefix(form, "%2Fbot") {
			prefix = "%2Fbot"
		}
		s = strings.ReplaceAll(s, form, prefix+"***")
	}
	return redactSecrets(s)
}

// wecomAppTrustedIPErrCode 是企业微信「企业可信 IP」拦截的错误码：
// 2022-06 之后新建的自建应用，请求出口 IP 不在该应用的「企业可信 IP」名单内时返回它（#123）。
const wecomAppTrustedIPErrCode = 60020

// wecomAppTrustedIPHint 在 errcode 60020 时给出排查提示，其它错误码返回空串。
//
// 文案刻意不断言请求实际走了哪条路：面板没配代理时请求仍可能走进程环境变量 HTTP(S)_PROXY；
// base_url 可能指向反代服务器（此时企业微信看到的是它的出口 IP），也可能填的就是官方地址（根本没有反代）。
// 所以只罗列本次生效的配置，实际出口 IP 以 errmsg 里的 from ip 为准。
//
// 只收字符串、不收 cfg：让所有 cfg 读取都留在 sendWecomAppWithContext 里，
// schema 绑定用例（AST 只认名为 cfg 的标识符）才能完整看见。
// 取 token 与发消息两处都挂：gettoken 是否也会返回 60020 没有找到官方原文，两处都挂没有副作用。
func wecomAppTrustedIPHint(errCode int, channelProxy, baseURL string) string {
	if errCode != wecomAppTrustedIPErrCode {
		return ""
	}

	var route []string
	if p := strings.TrimSpace(channelProxy); p != "" {
		route = append(route, "渠道代理 "+redactProxyURL(p))
	} else if p := strings.TrimSpace(model.GetRegisteredConfig("proxy_url")); p != "" {
		route = append(route, "系统代理 "+redactProxyURL(p))
	} else {
		route = append(route, "未配置面板代理（可能直连，也可能走进程环境变量 HTTP(S)_PROXY）")
	}
	if b := strings.TrimSpace(baseURL); b != "" {
		if isWecomAppOfficialHost(b) {
			// 用户照 placeholder「留空使用 https://qyapi.weixin.qq.com」把官方地址原样填进来时，请求直达企业微信，
			// 根本没有反代；再说「经反代转发」会把排查引向一台不存在的服务器。
			route = append(route, "base_url 为企业微信官方地址（未经反代）")
		} else {
			// 只说「若」：面板无从知道这个地址背后是不是真的反代服务器，实际出口以 from ip 为准。
			route = append(route, "经 base_url 反代 "+redactProxyURL(b)+" 转发（若该地址是反代服务器，企业微信看到的是它的出口 IP）")
		}
	}

	return "；企业微信返回 60020：出口 IP 不在该应用的「企业可信 IP」名单内，实际出口 IP 以 errmsg 里的 from ip 为准。" +
		"请在管理后台「应用管理 → 本应用 → 企业可信 IP」加入该 IP，或在本渠道填写「代理地址」经可信服务器转发。本次配置：" +
		strings.Join(route, "；")
}

// wecomAppOfficialHost 是企业微信 API 的官方主机名，与 wecomAppTokenURL / wecomAppSendURL 的默认值一致。
// 单独写成常量而不是从那两个变量里解析：测试会把它们改指 loopback 源站。
const wecomAppOfficialHost = "qyapi.weixin.qq.com"

// isWecomAppOfficialHost 判断 base_url 是否就是企业微信官方地址（主机名不区分大小写，端口与路径不管）。
func isWecomAppOfficialHost(baseURL string) bool {
	u, err := url.Parse(baseURL)
	return err == nil && strings.EqualFold(u.Hostname(), wecomAppOfficialHost)
}

// redactedUnparsableURL 是 redactProxyURL 解析不了时的占位句。调用方要改写它时按这个常量比较，别抄字面量。
const redactedUnparsableURL = "（地址无法解析）"

// redactProxyURL 只保留 scheme://host，丢掉 userinfo、路径与 query：代理地址可能带账号密码，
// 反代地址的路径里也可能藏着鉴权片段。解析不了时不回显原文，免得把整串（含密码）原样吐出来。
func redactProxyURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return redactedUnparsableURL
	}
	return u.Scheme + "://" + u.Host
}

func sendBark(cfg map[string]string, title, content string) error {
	return sendBarkWithContext(cfg, title, content, nil)
}

func sendBarkWithContext(cfg map[string]string, title, content string, context map[string]string) error {
	server := cfg["server"]
	key := cfg["key"]
	if key == "" {
		return fmt.Errorf("Bark Key 为空")
	}
	if server == "" {
		server = "https://api.day.app"
	}
	apiURL := fmt.Sprintf("%s/%s", strings.TrimRight(server, "/"), key)
	body := map[string]string{
		"title": title,
		"body":  content,
	}
	if v := cfg["sound"]; v != "" {
		body["sound"] = v
	}
	if v := cfg["group"]; v != "" {
		body["group"] = v
	}
	if v := cfg["icon"]; v != "" {
		body["icon"] = v
	}
	if v := cfg["level"]; v != "" {
		body["level"] = v
	}
	jumpURL := strings.TrimSpace(context["url"])
	if jumpURL == "" {
		jumpURL = cfg["url"]
	}
	if jumpURL != "" {
		body["url"] = jumpURL
	}
	return httpPostChecked(apiURL, body, nil, checkBarkResult)
}

func sendPushplus(cfg map[string]string, title, content string) error {
	token := cfg["token"]
	if token == "" {
		return fmt.Errorf("PushPlus Token 为空")
	}
	// 走 https：同一个请求分别打 https 和 http 实测过，两边都是 HTTP 200 且响应体逐字节相同
	// （{"code":903,...}），说明 https 通道可用。而 http 会让用户的 PushPlus token 明文过网，
	// 没有任何理由继续用。
	apiURL := "https://www.pushplus.plus/send"
	body := map[string]string{
		"token":   token,
		"title":   title,
		"content": content,
	}
	if v := cfg["topic"]; v != "" {
		body["topic"] = v
	}
	if v := cfg["template"]; v != "" {
		body["template"] = v
	}
	// channel 留空时不发这个参数，由 PushPlus 按账号默认渠道（微信公众号）处理，
	// 保证老渠道配置的行为与新增该字段之前完全一致。
	if v := cfg["channel"]; v != "" {
		body["channel"] = v
	}
	// option 是 PushPlus 对原 webhook 参数的改名，含义随 channel 变：webhook 渠道填 webhook 编码，
	// cp 渠道填企业微信自定义应用编码，qq 渠道填目标 QQ 群的配置编码（发给个人时留空）。
	// 其余渠道不需要。
	if v := cfg["option"]; v != "" {
		body["option"] = v
	}
	return httpPostChecked(apiURL, body, nil, checkPushplusResult)
}

func sendServerchan(cfg map[string]string, title, content string) error {
	key := cfg["key"]
	apiURL := fmt.Sprintf("https://sctapi.ftqq.com/%s.send", key)
	body := map[string]string{
		"title": title,
		"desp":  content,
	}
	return httpPostChecked(apiURL, body, nil, checkServerchanResult)
}

func sendFeishu(cfg map[string]string, title, content string) error {
	webhook := cfg["webhook"]
	if webhook == "" {
		return fmt.Errorf("飞书 Webhook URL 为空")
	}
	body := map[string]interface{}{
		"msg_type": "text",
		"content":  map[string]string{"text": fmt.Sprintf("%s\n%s", title, content)},
	}
	if secret := cfg["secret"]; secret != "" {
		timestamp := time.Now().Unix()
		stringToSign := fmt.Sprintf("%d\n%s", timestamp, secret)
		mac := hmac.New(sha256.New, []byte(stringToSign))
		sign := base64.StdEncoding.EncodeToString(mac.Sum(nil))
		body["timestamp"] = fmt.Sprintf("%d", timestamp)
		body["sign"] = sign
	}
	return httpPostChecked(webhook, body, nil, checkFeishuResult)
}

func sendGotify(cfg map[string]string, title, content string) error {
	server := cfg["server"]
	token := cfg["token"]
	if server == "" || token == "" {
		return fmt.Errorf("Gotify 服务器地址或 Token 为空")
	}
	apiURL := fmt.Sprintf("%s/message", strings.TrimRight(server, "/"))
	priority := 5
	if v := cfg["priority"]; v != "" {
		if p, err := strconv.Atoi(v); err == nil {
			priority = p
		}
	}
	body := map[string]interface{}{
		"title":    title,
		"message":  content,
		"priority": priority,
	}
	return httpPost(apiURL, body, map[string]string{"X-Gotify-Key": token})
}

func sendPushdeer(cfg map[string]string, title, content string) error {
	server := cfg["server"]
	key := cfg["key"]
	if server == "" {
		server = "https://api2.pushdeer.com"
	}
	apiURL := fmt.Sprintf("%s/message/push", strings.TrimRight(server, "/"))
	body := map[string]string{
		"pushkey": key,
		"text":    title,
		"desp":    content,
	}
	return httpPost(apiURL, body, nil)
}

func sendPushMe(cfg map[string]string, title, content string) error {
	server := strings.TrimSpace(cfg["server"])
	if server == "" {
		server = "https://push.i-i.me"
	}

	pushKey := strings.TrimSpace(cfg["key"])
	if pushKey == "" {
		return fmt.Errorf("PushMe push_key 为空")
	}

	form := url.Values{}
	form.Set("push_key", pushKey)
	form.Set("title", title)
	form.Set("content", content)
	if messageType := strings.TrimSpace(cfg["message_type"]); messageType != "" {
		form.Set("type", messageType)
	}

	req, err := http.NewRequest(http.MethodPost, strings.TrimRight(server, "/"), strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	client := NewHTTPClient(10 * time.Second)
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}

	responseText := strings.TrimSpace(string(body))
	if responseText != "" && responseText != "success" && !strings.HasPrefix(responseText, "{") {
		return fmt.Errorf("PushMe 返回异常: %s", responseText)
	}

	return nil
}

func sendChanify(cfg map[string]string, title, content string) error {
	server := cfg["server"]
	token := cfg["token"]
	if server == "" {
		server = "https://api.chanify.net"
	}
	apiURL := fmt.Sprintf("%s/v1/sender/%s", strings.TrimRight(server, "/"), token)
	body := map[string]string{
		"title": title,
		"text":  content,
	}
	return httpPost(apiURL, body, nil)
}

func sendIgot(cfg map[string]string, title, content string) error {
	key := cfg["key"]
	apiURL := fmt.Sprintf("https://push.hellyw.com/%s", key)
	body := map[string]string{
		"title":   title,
		"content": content,
	}
	return httpPost(apiURL, body, nil)
}

func sendQmsg(cfg map[string]string, title, content string) error {
	key := strings.TrimSpace(cfg["key"])
	if key == "" {
		return fmt.Errorf("Qmsg Key 为空")
	}

	mode := strings.ToLower(strings.TrimSpace(cfg["mode"]))
	path := "send"
	if mode == "group" {
		path = "group"
	}

	apiURL := fmt.Sprintf("https://qmsg.zendee.cn/%s/%s", path, key)
	form := url.Values{}
	form.Set("msg", fmt.Sprintf("%s\n%s", title, content))
	if qq := strings.TrimSpace(cfg["qq"]); qq != "" {
		form.Set("qq", qq)
	}

	req, err := http.NewRequest(http.MethodPost, apiURL, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	client := NewHTTPClient(10 * time.Second)
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Success bool   `json:"success"`
		Reason  string `json:"reason"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return fmt.Errorf("Qmsg 返回无法解析: %s", strings.TrimSpace(string(body)))
	}
	if !result.Success {
		return fmt.Errorf("Qmsg 发送失败: %s", strings.TrimSpace(result.Reason))
	}

	return nil
}

func sendPushover(cfg map[string]string, title, content string) error {
	token := cfg["token"]
	user := cfg["user"]
	apiURL := "https://api.pushover.net/1/messages.json"
	body := map[string]string{
		"token":   token,
		"user":    user,
		"title":   title,
		"message": content,
	}
	return httpPost(apiURL, body, nil)
}

func sendDiscord(cfg map[string]string, title, content string) error {
	webhook := cfg["webhook"]
	body := map[string]interface{}{
		"embeds": []map[string]interface{}{
			{
				"title":       title,
				"description": content,
				"color":       3447003,
			},
		},
	}
	return httpPost(webhook, body, nil)
}

func sendSlack(cfg map[string]string, title, content string) error {
	webhook := cfg["webhook"]
	body := map[string]interface{}{
		"text": fmt.Sprintf("*%s*\n\n%s", title, content),
	}
	return httpPost(webhook, body, nil)
}

func sendNtfy(cfg map[string]string, title, content string) error {
	server := cfg["server"]
	topic := cfg["topic"]
	if topic == "" {
		return fmt.Errorf("ntfy Topic 为空")
	}
	if server == "" {
		server = "https://ntfy.sh"
	}
	apiURL := fmt.Sprintf("%s/%s", strings.TrimRight(server, "/"), topic)

	req, err := http.NewRequest("POST", apiURL, strings.NewReader(content))
	if err != nil {
		return err
	}
	req.Header.Set("Title", title)
	if v := cfg["priority"]; v != "" {
		req.Header.Set("Priority", v)
	}
	if v := cfg["token"]; v != "" {
		req.Header.Set("Authorization", "Bearer "+v)
	}

	client := NewHTTPClient(10 * time.Second)
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(respBody))
	}
	return nil
}

func sendWxPusher(cfg map[string]string, title, content string) error {
	appToken := strings.TrimSpace(cfg["app_token"])
	if appToken == "" {
		return fmt.Errorf("WxPusher appToken 为空")
	}

	uids := splitNotificationTargets(cfg["uids"])
	topicIDs, err := splitNotificationIntTargets(cfg["topic_ids"])
	if err != nil {
		return fmt.Errorf("WxPusher Topic ID 格式错误: %w", err)
	}
	if len(uids) == 0 && len(topicIDs) == 0 {
		return fmt.Errorf("WxPusher 至少需要一个 UID 或 Topic ID")
	}

	contentType := 1
	if raw := strings.TrimSpace(cfg["content_type"]); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil {
			contentType = parsed
		}
	}

	messageContent := fmt.Sprintf("%s\n%s", title, content)
	switch contentType {
	case 2:
		messageContent = fmt.Sprintf(
			"<h1>%s</h1><br/><div style='white-space: pre-wrap;'>%s</div>",
			html.EscapeString(title),
			html.EscapeString(content),
		)
	case 3:
		messageContent = fmt.Sprintf("## %s\n\n%s", title, content)
	}

	body := map[string]interface{}{
		"appToken":    appToken,
		"content":     messageContent,
		"summary":     title,
		"contentType": contentType,
	}
	if jumpURL := strings.TrimSpace(cfg["url"]); jumpURL != "" {
		body["url"] = jumpURL
	}
	if verifyPayType := strings.TrimSpace(cfg["verify_pay_type"]); verifyPayType != "" {
		if parsed, err := strconv.Atoi(verifyPayType); err == nil {
			body["verifyPayType"] = parsed
		}
	}
	if len(uids) > 0 {
		body["uids"] = uids
	}
	if len(topicIDs) > 0 {
		body["topicIds"] = topicIDs
	}

	apiURL := "https://wxpusher.zjiecode.com/api/send/message"
	if server := strings.TrimSpace(cfg["server"]); server != "" {
		apiURL = strings.TrimRight(server, "/")
	}

	client := NewHTTPClient(10 * time.Second)
	data, err := json.Marshal(body)
	if err != nil {
		return err
	}

	req, err := http.NewRequest(http.MethodPost, apiURL, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		Success bool   `json:"success"`
		Code    int    `json:"code"`
		Msg     string `json:"msg"`
	}
	if err := json.Unmarshal(respBody, &result); err == nil {
		if !result.Success && result.Code != 1000 {
			return fmt.Errorf("WxPusher 发送失败: %s", strings.TrimSpace(result.Msg))
		}
	}

	return nil
}

func resolveWecomAppEndpoint(cfg map[string]string, fallbackURL, path string) string {
	baseURL := strings.TrimSpace(cfg["base_url"])
	if baseURL == "" {
		return fallbackURL
	}

	baseURL = strings.TrimRight(baseURL, "/")
	if strings.HasSuffix(baseURL, "/cgi-bin") {
		return baseURL + strings.TrimPrefix(path, "/cgi-bin")
	}
	return baseURL + path
}

func splitNotificationTargets(raw string) []string {
	fields := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ';' || r == '\n' || r == '\r' || r == '\t' || r == ' '
	})

	result := make([]string, 0, len(fields))
	for _, field := range fields {
		field = strings.TrimSpace(field)
		if field != "" {
			result = append(result, field)
		}
	}
	return result
}

func splitNotificationIntTargets(raw string) ([]int, error) {
	fields := splitNotificationTargets(raw)
	result := make([]int, 0, len(fields))
	for _, field := range fields {
		value, err := strconv.Atoi(field)
		if err != nil {
			return nil, fmt.Errorf("无效整数 %q", field)
		}
		result = append(result, value)
	}
	return result, nil
}

func buildTelegramMessages(title, content string) []string {
	title = strings.TrimSpace(title)
	content = strings.TrimSpace(content)

	contentChunks := splitNotificationContentChunks(content, 3200)
	if len(contentChunks) == 0 {
		contentChunks = []string{""}
	}

	messages := make([]string, 0, len(contentChunks))
	total := len(contentChunks)
	for index, chunk := range contentChunks {
		header := title
		if total > 1 {
			header = fmt.Sprintf("%s (%d/%d)", title, index+1, total)
		}
		if strings.TrimSpace(chunk) == "" {
			messages = append(messages, header)
			continue
		}
		messages = append(messages, header+"\n"+chunk)
	}

	return messages
}

func splitNotificationContentChunks(content string, limit int) []string {
	content = strings.TrimSpace(content)
	if content == "" {
		return nil
	}
	if limit <= 0 {
		return []string{content}
	}

	runes := []rune(content)
	chunks := make([]string, 0, len(runes)/limit+1)
	for start := 0; start < len(runes); {
		end := start + limit
		if end >= len(runes) {
			chunks = append(chunks, strings.TrimSpace(string(runes[start:])))
			break
		}

		splitAt := end
		for idx := end; idx > start+limit/2; idx-- {
			if runes[idx-1] == '\n' {
				splitAt = idx
				break
			}
		}
		if splitAt <= start {
			splitAt = end
		}
		chunks = append(chunks, strings.TrimSpace(string(runes[start:splitAt])))
		start = splitAt
	}

	return chunks
}

func notificationConfigInt(raw string, defaultValue int) int {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return defaultValue
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return defaultValue
	}
	return value
}

func notificationConfigBool(raw string, defaultValue bool) bool {
	raw = strings.ToLower(strings.TrimSpace(raw))
	switch raw {
	case "1", "true", "yes", "on", "enable", "enabled":
		return true
	case "0", "false", "no", "off", "disable", "disabled":
		return false
	default:
		return defaultValue
	}
}

func renderNotificationTemplate(template, title, content, fallback string) string {
	return renderNotificationTemplateWithContext(template, title, content, fallback, nil)
}

func renderNotificationTemplateWithContext(template, title, content, fallback string, context map[string]string) string {
	template = strings.TrimSpace(template)
	if template == "" {
		template = fallback
	}
	template = strings.ReplaceAll(template, "{{title}}", title)
	template = strings.ReplaceAll(template, "{{content}}", content)
	for key, value := range context {
		placeholder := "{{" + strings.TrimSpace(key) + "}}"
		if placeholder == "{{}}" {
			continue
		}
		template = strings.ReplaceAll(template, placeholder, value)
	}
	return template
}

func parseNotificationJSONTemplate(raw, title, content string) (interface{}, error) {
	return parseNotificationJSONTemplateWithContext(raw, title, content, nil)
}

func parseNotificationJSONTemplateWithContext(raw, title, content string, context map[string]string) (interface{}, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("JSON 模板为空")
	}

	var payload interface{}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return nil, err
	}
	return renderNotificationJSONValueWithContext(payload, title, content, context), nil
}

func renderNotificationJSONValue(value interface{}, title, content string) interface{} {
	return renderNotificationJSONValueWithContext(value, title, content, nil)
}

func renderNotificationJSONValueWithContext(value interface{}, title, content string, context map[string]string) interface{} {
	switch v := value.(type) {
	case string:
		return renderNotificationTemplateWithContext(v, title, content, v, context)
	case []interface{}:
		items := make([]interface{}, 0, len(v))
		for _, item := range v {
			items = append(items, renderNotificationJSONValueWithContext(item, title, content, context))
		}
		return items
	case map[string]interface{}:
		result := make(map[string]interface{}, len(v))
		for key, item := range v {
			result[key] = renderNotificationJSONValueWithContext(item, title, content, context)
		}
		return result
	default:
		return value
	}
}

func sendCustomWebhook(cfg map[string]string, title, content string) error {
	webhookURL := cfg["url"]
	method := cfg["method"]
	if method == "" {
		method = "POST"
	}

	bodyTemplate := cfg["body"]
	if bodyTemplate == "" {
		bodyTemplate = `{"title":"{{title}}","content":"{{content}}"}`
	}
	bodyStr := strings.ReplaceAll(bodyTemplate, "{{title}}", title)
	bodyStr = strings.ReplaceAll(bodyStr, "{{content}}", content)

	req, err := http.NewRequest(method, webhookURL, strings.NewReader(bodyStr))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", cfg["content_type"])
	if req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}

	if headerStr := cfg["headers"]; headerStr != "" {
		var headers map[string]string
		if json.Unmarshal([]byte(headerStr), &headers) == nil {
			for k, v := range headers {
				req.Header.Set(k, v)
			}
		}
	}

	client := NewHTTPClient(10 * time.Second)
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return nil
}
