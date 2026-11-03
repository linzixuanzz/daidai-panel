package middleware

import (
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

type visitor struct {
	count    int
	lastSeen time.Time
}

type RateLimiter struct {
	mu       sync.Mutex
	visitors map[string]*visitor
	limit    int
	window   time.Duration
}

func NewRateLimiter(limit int, window time.Duration) *RateLimiter {
	rl := &RateLimiter{
		visitors: make(map[string]*visitor),
		limit:    limit,
		window:   window,
	}
	go rl.cleanup()
	return rl
}

func (rl *RateLimiter) cleanup() {
	for {
		time.Sleep(rl.window)
		rl.mu.Lock()
		for ip, v := range rl.visitors {
			if time.Since(v.lastSeen) > rl.window {
				delete(rl.visitors, ip)
			}
		}
		rl.mu.Unlock()
	}
}

func (rl *RateLimiter) Allow(key string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	v, exists := rl.visitors[key]
	if !exists {
		rl.visitors[key] = &visitor{count: 1, lastSeen: time.Now()}
		return true
	}

	if time.Since(v.lastSeen) > rl.window {
		v.count = 1
		v.lastSeen = time.Now()
		return true
	}

	v.count++
	v.lastSeen = time.Now()
	return v.count <= rl.limit
}

// FailureLimiter 只数「失败」的限流器：同一个 key 在窗口内失败满 limit 次后，窗口结束前一律拒绝。
//
// 和 RateLimiter 分开写的原因：像 /open-api/token 这样的入口，本机跑的脚本都从同一个回环 IP 来取令牌，
// 按总请求数限流会误伤正常脚本；只数失败，才能挡住猜密钥又不影响凭据正确的调用方。
// 窗口从这一轮第一次失败算起，到点整轮清零（不像 RateLimiter 那样每次请求都顺延）。
type FailureLimiter struct {
	mu      sync.Mutex
	entries map[string]*failureEntry
	limit   int
	window  time.Duration
}

type failureEntry struct {
	count       int
	windowStart time.Time
}

func NewFailureLimiter(limit int, window time.Duration) *FailureLimiter {
	return &FailureLimiter{
		entries: make(map[string]*failureEntry),
		limit:   limit,
		window:  window,
	}
}

// Blocked 判断这个 key 当前是否已被封：窗口内失败次数已达上限。
func (l *FailureLimiter) Blocked(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	entry, ok := l.entries[key]
	if !ok {
		return false
	}
	if time.Since(entry.windowStart) > l.window {
		delete(l.entries, key)
		return false
	}
	return entry.count >= l.limit
}

// RecordFailure 记一次失败。
func (l *FailureLimiter) RecordFailure(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	// 不起后台清理协程：条目多起来时顺手扫一遍过期的，防止换着 IP 打把 map 撑大。
	if len(l.entries) >= 1024 {
		for k, entry := range l.entries {
			if now.Sub(entry.windowStart) > l.window {
				delete(l.entries, k)
			}
		}
	}

	entry, ok := l.entries[key]
	if !ok || now.Sub(entry.windowStart) > l.window {
		l.entries[key] = &failureEntry{count: 1, windowStart: now}
		return
	}
	entry.count++
}

func RateLimit(limit int, window time.Duration) gin.HandlerFunc {
	limiter := NewRateLimiter(limit, window)
	return func(c *gin.Context) {
		ip := ResolveClientIP(c)
		if !limiter.Allow(ip) {
			c.JSON(http.StatusTooManyRequests, gin.H{"error": "请求过于频繁，请稍后再试"})
			c.Abort()
			return
		}
		c.Next()
	}
}
