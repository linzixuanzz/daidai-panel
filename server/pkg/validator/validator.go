package validator

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

var usernameRegex = regexp.MustCompile(`^[\p{L}\p{N}_]{1,32}$`)

func ValidateUsername(username string) bool {
	return usernameRegex.MatchString(username)
}

func ValidatePassword(password string) bool {
	return len(password) >= 6 && len(password) <= 128
}

// NewPasswordRuleMessage 是设置新密码不合规时回给客户端的文案。
// APP 没有本地校验、直接显示服务端文案，所以这句话要把规则说全。
const NewPasswordRuleMessage = "密码需 8-72 位，且字母、数字、符号至少包含两种"

// newPasswordMaxBytes：bcrypt 只接受不超过 72 字节的密码，超长会在哈希时报错（接口变成 500）。
const newPasswordMaxBytes = 72

// ValidateNewPassword 校验「设置新密码」时的复杂度：至少 8 个字符、且 UTF-8 编码不超过 72 字节，
// 字母、数字、其他符号三类里至少含两类。
// 下限按字符数（中文密码 8 个字也算够长），上限按字节数（这是 bcrypt 的硬限制，中文一个字占 3 字节）。
//
// 只用在设置新密码的地方（初始化、改密、建用户、重置密码、命令行重置），
// 登录绝不能用它：老用户当初设的弱密码照样要能登录。
func ValidateNewPassword(password string) bool {
	if utf8.RuneCountInString(password) < 8 || len(password) > newPasswordMaxBytes {
		return false
	}

	hasLetter, hasDigit, hasOther := false, false, false
	for _, r := range password {
		switch {
		case unicode.IsLetter(r):
			hasLetter = true
		case unicode.IsDigit(r):
			hasDigit = true
		default:
			hasOther = true
		}
	}

	kinds := 0
	for _, has := range []bool{hasLetter, hasDigit, hasOther} {
		if has {
			kinds++
		}
	}
	return kinds >= 2
}

func SanitizeString(s string) string {
	return strings.TrimSpace(s)
}
