package cron

import (
	"fmt"
	"testing"
	"time"
)

// legacyWindowTimes 是 v4.0.1 /tasks/upcoming 的旧算法：先固定往后推 limit 个点，再丢掉 >= to 的。
// 新函数必须与它逐点一致。
func legacyWindowTimes(raw string, from, to time.Time, limit int) []time.Time {
	result := make([]time.Time, 0)
	for _, t := range NextRunTimesForExpressionsFrom(raw, limit, from) {
		if !t.Before(to) {
			break
		}
		result = append(result, t)
	}
	return result
}

func TestNextRunTimesForExpressionsBetweenMatchesLegacy(t *testing.T) {
	from := time.Date(2026, 11, 24, 10, 0, 30, 0, time.Local)
	cases := []struct {
		name string
		raw  string
	}{
		{"每天一次", "0 9 * * *"},
		{"每分钟", "* * * * *"},
		{"每分钟（6 段，超过单任务上限）", "0 * * * * *"},
		{"6 段带秒", "*/10 * * * * *"},
		{"多行 cron", "30 10 * * *\n15 11 * * *\n0 */2 * * *"},
		{"多行重复同一时刻", "0 12 * * *\n0 12 * * *"},
		{"工作日时段", "0 */10 9-22 * * 1-5"},
		{"永不触发", "0 0 30 2 *"},
		{"一行非法一行合法", "bad cron\n0 9 * * *"},
		{"空串", ""},
	}
	for _, hours := range []int{1, 3, 24, 48} {
		to := from.Add(time.Duration(hours) * time.Hour)
		for _, tc := range cases {
			for _, limit := range []int{1, 5, 1440} {
				want := legacyWindowTimes(tc.raw, from, to, limit)
				got := NextRunTimesForExpressionsBetween(tc.raw, from, to, limit)
				if len(want) != len(got) {
					t.Fatalf("%s hours=%d limit=%d：个数不一致 旧=%d 新=%d", tc.name, hours, limit, len(want), len(got))
				}
				for i := range want {
					if !want[i].Equal(got[i]) {
						t.Fatalf("%s hours=%d limit=%d：第 %d 个不一致 旧=%v 新=%v", tc.name, hours, limit, i, want[i], got[i])
					}
				}
			}
		}
	}
}

func TestNextRunTimesForExpressionsBetweenEmptyWindow(t *testing.T) {
	from := time.Date(2026, 11, 24, 10, 0, 0, 0, time.Local)
	if got := NextRunTimesForExpressionsBetween("* * * * *", from, from, 10); len(got) != 0 {
		t.Fatalf("空窗口应返回空，实际 %d 个", len(got))
	}
	if got := NextRunTimesForExpressionsBetween("* * * * *", from, from.Add(time.Hour), 0); len(got) != 0 {
		t.Fatalf("limit=0 应返回空，实际 %d 个", len(got))
	}
}

// benchmarkExpressions：300 个每天一次的任务 + 5 个每分钟一次的任务，模拟「几百个签到脚本」的实例。
func benchmarkExpressions() []string {
	expressions := make([]string, 0, 305)
	for i := 0; i < 300; i++ {
		expressions = append(expressions, fmt.Sprintf("%d %d * * *", i%60, (i/60)%24))
	}
	for i := 0; i < 5; i++ {
		expressions = append(expressions, "* * * * *")
	}
	return expressions
}

func BenchmarkUpcomingWindowLegacy(b *testing.B) {
	expressions := benchmarkExpressions()
	from := time.Date(2026, 11, 24, 10, 0, 30, 0, time.Local)
	to := from.Add(24 * time.Hour)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, raw := range expressions {
			legacyWindowTimes(raw, from, to, 1440)
		}
	}
}

func BenchmarkUpcomingWindowBetween(b *testing.B) {
	expressions := benchmarkExpressions()
	from := time.Date(2026, 11, 24, 10, 0, 30, 0, time.Local)
	to := from.Add(24 * time.Hour)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, raw := range expressions {
			NextRunTimesForExpressionsBetween(raw, from, to, 1440)
		}
	}
}
