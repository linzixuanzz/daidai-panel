package handler

import (
	"regexp"
	"testing"
)

// run_id 是「可读前缀 + 16 位十六进制随机串」，不再是能按时间猜出来的毫秒时间戳。
func TestNewDebugRunIDIsRandomWithReadablePrefix(t *testing.T) {
	pattern := regexp.MustCompile(`^console_[0-9a-f]{16}$`)
	seen := map[string]bool{}
	for i := 0; i < 50; i++ {
		id := newDebugRunID("console")
		if !pattern.MatchString(id) {
			t.Fatalf("unexpected run id format: %q", id)
		}
		if seen[id] {
			t.Fatalf("run id repeated: %q", id)
		}
		seen[id] = true
	}
}
