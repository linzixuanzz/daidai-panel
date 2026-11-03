package service

import (
	"net/smtp"
	"strings"
	"testing"
)

// 标题 / 发件人 / 收件人里的换行不能变成新的邮件头（如 Bcc），纯文本与 HTML 两种报文都要挡住。
func TestSendEmailStripsCRLFFromHeaders(t *testing.T) {
	oldPlain := smtpSendMail
	defer func() { smtpSendMail = oldPlain }()

	for _, format := range []string{"", NotifyContentHTML} {
		var captured string
		smtpSendMail = func(addr string, auth smtp.Auth, from string, to []string, msg []byte) error {
			captured = string(msg)
			return nil
		}
		cfg := map[string]string{
			"smtp_host": "smtp.example.com",
			"smtp_port": "587",
			"smtp_user": "sender@example.com",
			"smtp_pass": "secret",
			"from":      "Panel\r\nBcc: from-evil@example.com <sender@example.com>",
			"to":        "one@example.com\nBcc: to-evil@example.com",
		}
		if err := sendEmailWithFormat(cfg, "日报\r\nBcc: subject-evil@example.com", "正文", format); err != nil {
			t.Fatalf("send email (format=%q): %v", format, err)
		}

		headerEnd := strings.Index(captured, "\r\n\r\n")
		if headerEnd < 0 {
			t.Fatalf("message has no header separator: %q", captured)
		}
		header := captured[:headerEnd]
		for _, line := range strings.Split(header, "\r\n") {
			if strings.HasPrefix(line, "Bcc:") {
				t.Fatalf("injected header line found (format=%q): %q", format, header)
			}
		}
		if strings.ContainsAny(strings.ReplaceAll(header, "\r\n", ""), "\r\n") {
			t.Fatalf("bare CR/LF left in header (format=%q): %q", format, header)
		}
		if !strings.Contains(header, "Subject: 日报 Bcc: subject-evil@example.com") {
			t.Fatalf("expected subject kept on one line (format=%q): %q", format, header)
		}
	}
}
