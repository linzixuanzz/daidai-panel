package validator

import (
	"strings"
	"testing"
)

func TestValidateNewPassword(t *testing.T) {
	cases := []struct {
		password string
		want     bool
	}{
		// 长度边界：6 位旧规则能过、新规则太短；8 位与 72 字节能过；73 字节超长（bcrypt 上限）
		{"abc123", false},
		{"abcd123", false},
		{"abcd1234", true},
		{strings.Repeat("a1", 36), true},
		{strings.Repeat("a1", 36) + "a", false},
		{strings.Repeat("a1", 64), false},
		// 上限按字节算：中文一个字 3 字节，23 个字（69 字节）+ 3 个数字 = 72 字节能过，+ 4 个数字 = 73 字节超长
		{strings.Repeat("密", 23) + "123", true},
		{strings.Repeat("密", 23) + "1234", false},
		// 只有 27 个字符也会因为字节超长被拒（24 个中文 = 72 字节，再加 3 个数字 = 75 字节）
		{strings.Repeat("密", 24) + "123", false},
		// 只有一类
		{"abcdefgh", false},
		{"12345678", false},
		{"!@#$%^&*", false},
		{"ABCDEFGH", false},
		// 任意两类
		{"abcdefg!", true},
		{"1234567!", true},
		{"Abcdefg1", true},
		// 三类
		{"Abc123!@", true},
		// 中文按字符数算长度、算作字母
		{"密码密码密码12", true},
		{"密码密码密码密码", false},
	}
	for _, tc := range cases {
		if got := ValidateNewPassword(tc.password); got != tc.want {
			t.Errorf("ValidateNewPassword(%q) = %v, want %v", tc.password, got, tc.want)
		}
	}
}
