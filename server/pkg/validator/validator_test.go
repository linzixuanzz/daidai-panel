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
		// 长度边界：6 位旧规则能过、新规则太短；8 位与 128 位能过；129 位超长
		{"abc123", false},
		{"abcd123", false},
		{"abcd1234", true},
		{strings.Repeat("a1", 64), true},
		{strings.Repeat("a1", 64) + "a", false},
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
