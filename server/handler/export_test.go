package handler

import (
	"testing"
	"time"
)

// SetTaskTriggerClockForTest 让 handler_test 包里的用例拨触发冷却用的时钟，
// 验证冷却边界时不用真的睡 10 秒。用例结束自动还原成 time.Now。
// 文件名以 _test.go 结尾，只在跑测试时编译，不会进生产二进制。
func SetTaskTriggerClockForTest(t *testing.T, now func() time.Time) {
	t.Helper()
	previous := taskTriggerNow
	taskTriggerNow = now
	t.Cleanup(func() { taskTriggerNow = previous })
}
