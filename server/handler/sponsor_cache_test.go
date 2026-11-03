package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"daidai-panel/testutil"
)

// sponsorCacheTestFeed 起一个计数的本地赞助站；fail 为 true 时回 500。
func sponsorCacheTestFeed(t *testing.T, hits *int32, fail *atomic.Bool, delay time.Duration) {
	t.Helper()
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(hits, 1)
		if delay > 0 {
			time.Sleep(delay)
		}
		if fail != nil && fail.Load() {
			http.Error(w, "down", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"sponsors":[{"name":"缓存用户","amount":1}],"count":1}}`))
	}))
	t.Cleanup(remote.Close)

	originalFeedURL := defaultSponsorFeedURL
	defaultSponsorFeedURL = remote.URL + "/"
	t.Cleanup(func() {
		defaultSponsorFeedURL = originalFeedURL
	})
}

func requestSponsorList(t *testing.T) (count int, unavailable bool) {
	t.Helper()
	rec := httptest.NewRecorder()
	newSponsorRouter().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/sponsors", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var payload struct {
		Data struct {
			Count       int  `json:"count"`
			Unavailable bool `json:"unavailable"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode sponsor response: %v", err)
	}
	return payload.Data.Count, payload.Data.Unavailable
}

// 成功结果缓存 10 分钟：连续请求只回源一次。
func TestSponsorListCachesSuccessfulFeed(t *testing.T) {
	testutil.SetupTestEnv(t)
	var hits int32
	sponsorCacheTestFeed(t, &hits, nil, 0)

	for i := 0; i < 3; i++ {
		if count, unavailable := requestSponsorList(t); count != 1 || unavailable {
			t.Fatalf("unexpected sponsor response: count=%d unavailable=%v", count, unavailable)
		}
	}
	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Fatalf("expected one upstream fetch, got %d", got)
	}
}

// 同一时刻的并发请求只回源一次。
func TestSponsorListCoalescesConcurrentFetches(t *testing.T) {
	testutil.SetupTestEnv(t)
	var hits int32
	sponsorCacheTestFeed(t, &hits, nil, 200*time.Millisecond)

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			summary, err := loadSponsorFeed(defaultSponsorFeedURL)
			if err != nil || summary.Count != 1 {
				t.Errorf("unexpected concurrent result: %+v err=%v", summary, err)
			}
		}()
	}
	wg.Wait()
	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Fatalf("expected concurrent requests to share one upstream fetch, got %d", got)
	}
}

// 缓存过期后回源失败：返回旧缓存，而不是「暂不可用」。
func TestSponsorListServesStaleCacheWhenFeedFails(t *testing.T) {
	testutil.SetupTestEnv(t)
	var hits int32
	var fail atomic.Bool
	sponsorCacheTestFeed(t, &hits, &fail, 0)

	if count, unavailable := requestSponsorList(t); count != 1 || unavailable {
		t.Fatalf("unexpected first response: count=%d unavailable=%v", count, unavailable)
	}

	// 让缓存过期，再让上游挂掉。
	sponsorFeedCache.mu.Lock()
	sponsorFeedCache.fetchedAt = time.Now().Add(-sponsorFeedCacheTTL - time.Minute)
	sponsorFeedCache.mu.Unlock()
	fail.Store(true)

	if count, unavailable := requestSponsorList(t); count != 1 || unavailable {
		t.Fatalf("expected stale cache served, got count=%d unavailable=%v", count, unavailable)
	}
	if got := atomic.LoadInt32(&hits); got != 2 {
		t.Fatalf("expected expired cache to retry upstream once, got %d fetches", got)
	}
}
