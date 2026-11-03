package handler

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"daidai-panel/pkg/response"
	"daidai-panel/service"

	"github.com/gin-gonic/gin"
)

var defaultSponsorFeedURL = "https://dumblist.linzixuan.top/"

const sponsorFeedLogInterval = 10 * time.Minute

var sponsorFeedLogState struct {
	mu       sync.Mutex
	lastWarn time.Time
}

type SponsorHandler struct{}

type sponsorFeedItem struct {
	ID        uint    `json:"id,omitempty"`
	Name      string  `json:"name"`
	Amount    float64 `json:"amount"`
	AvatarURL string  `json:"avatar_url"`
	Initial   string  `json:"initial,omitempty"`
	CreatedAt string  `json:"created_at,omitempty"`
	UpdatedAt string  `json:"updated_at,omitempty"`
}

type sponsorFeedSummary struct {
	Sponsors    []sponsorFeedItem `json:"sponsors"`
	Count       int               `json:"count"`
	TotalAmount float64           `json:"total_amount"`
	UpdatedAt   string            `json:"updated_at,omitempty"`
	Unavailable bool              `json:"unavailable,omitempty"`
}

// 赞助列表的进程内缓存：这个接口不用登录，以前每来一个请求就回源一次，
// 外人刷这个接口就等于借面板去刷赞助站。现在成功结果缓存 10 分钟；回源失败时有旧缓存就继续用旧的；
// 同一时刻的并发请求只回源一次，其余的等这一次的结果。
const sponsorFeedCacheTTL = 10 * time.Minute

// sponsorFeedCall 是一次进行中的回源，等它的请求在 done 关闭后直接取 summary / err。
type sponsorFeedCall struct {
	feedURL string
	done    chan struct{}
	summary sponsorFeedSummary
	err     error
}

var sponsorFeedCache struct {
	mu sync.Mutex
	// feedURL：缓存属于哪个地址，地址不同（测试里会换成本地服务）就当没有缓存。
	feedURL   string
	summary   sponsorFeedSummary
	fetchedAt time.Time
	inflight  *sponsorFeedCall
}

// loadSponsorFeed 按上面的缓存规则取赞助列表，返回 err 表示既没拉到、也没有可用的旧缓存。
func loadSponsorFeed(feedURL string) (sponsorFeedSummary, error) {
	cache := &sponsorFeedCache
	cache.mu.Lock()
	hasCache := cache.feedURL == feedURL && !cache.fetchedAt.IsZero()
	if hasCache && time.Since(cache.fetchedAt) < sponsorFeedCacheTTL {
		summary := cache.summary
		cache.mu.Unlock()
		return summary, nil
	}
	if call := cache.inflight; call != nil && call.feedURL == feedURL {
		// 已经有人在回源：等它结束，用它的结果（含失败时的旧缓存兜底），不再重复回源。
		cache.mu.Unlock()
		<-call.done
		return call.summary, call.err
	}
	call := &sponsorFeedCall{feedURL: feedURL, done: make(chan struct{})}
	cache.inflight = call
	cache.mu.Unlock()

	summary, err := fetchSponsorFeed(feedURL)

	cache.mu.Lock()
	if err == nil {
		cache.feedURL, cache.summary, cache.fetchedAt = feedURL, summary, time.Now()
	} else if cache.feedURL == feedURL && !cache.fetchedAt.IsZero() {
		// 回源失败但有旧缓存：返回旧的。fetchedAt 不动，下一个请求还会再试着回源。
		summary, err = cache.summary, nil
	}
	call.summary, call.err = summary, err
	if cache.inflight == call {
		cache.inflight = nil
	}
	cache.mu.Unlock()
	close(call.done)
	return summary, err
}

func NewSponsorHandler() *SponsorHandler {
	return &SponsorHandler{}
}

func (h *SponsorHandler) List(c *gin.Context) {
	c.Header("Cache-Control", "no-store")

	feedURL := defaultSponsorFeedURL

	summary, err := loadSponsorFeed(feedURL)
	if err != nil {
		logSponsorFeedUnavailable()
		fallback := emptySponsorFeed()
		fallback.Unavailable = true
		response.Success(c, gin.H{"data": fallback})
		return
	}
	response.Success(c, gin.H{"data": summary})
}

func (h *SponsorHandler) RegisterRoutes(r *gin.RouterGroup) {
	r.GET("/sponsors", h.List)
}

func emptySponsorFeed() sponsorFeedSummary {
	return sponsorFeedSummary{
		Sponsors:    []sponsorFeedItem{},
		Count:       0,
		TotalAmount: 0,
		UpdatedAt:   "",
	}
}

func logSponsorFeedUnavailable() {
	sponsorFeedLogState.mu.Lock()
	defer sponsorFeedLogState.mu.Unlock()

	now := time.Now()
	if !sponsorFeedLogState.lastWarn.IsZero() && now.Sub(sponsorFeedLogState.lastWarn) < sponsorFeedLogInterval {
		return
	}

	sponsorFeedLogState.lastWarn = now
	log.Printf("sponsor feed unavailable, using fallback response")
}

func fetchSponsorFeed(feedURL string) (sponsorFeedSummary, error) {
	client := service.NewHTTPClient(8 * time.Second)
	req, err := http.NewRequest(http.MethodGet, feedURL, nil)
	if err != nil {
		return sponsorFeedSummary{}, err
	}
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return sponsorFeedSummary{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return sponsorFeedSummary{}, fmt.Errorf("remote status %d", resp.StatusCode)
	}

	var root map[string]json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&root); err != nil {
		return sponsorFeedSummary{}, err
	}

	var summary sponsorFeedSummary
	if raw, ok := root["data"]; ok && len(raw) > 0 && string(raw) != "null" {
		if err := json.Unmarshal(raw, &summary); err != nil {
			return sponsorFeedSummary{}, err
		}
	} else if raw, ok := root["sponsors"]; ok && len(raw) > 0 && string(raw) != "null" {
		if err := json.Unmarshal(raw, &summary.Sponsors); err != nil {
			return sponsorFeedSummary{}, err
		}
		if rawCount, ok := root["count"]; ok && len(rawCount) > 0 && string(rawCount) != "null" {
			_ = json.Unmarshal(rawCount, &summary.Count)
		}
		if rawTotal, ok := root["total_amount"]; ok && len(rawTotal) > 0 && string(rawTotal) != "null" {
			_ = json.Unmarshal(rawTotal, &summary.TotalAmount)
		}
		if rawUpdatedAt, ok := root["updated_at"]; ok && len(rawUpdatedAt) > 0 && string(rawUpdatedAt) != "null" {
			_ = json.Unmarshal(rawUpdatedAt, &summary.UpdatedAt)
		}
	} else {
		body, err := json.Marshal(root)
		if err != nil {
			return sponsorFeedSummary{}, err
		}
		if err := json.Unmarshal(body, &summary); err != nil {
			return sponsorFeedSummary{}, err
		}
	}

	if summary.Sponsors == nil {
		summary.Sponsors = []sponsorFeedItem{}
	}
	if summary.Count == 0 {
		summary.Count = len(summary.Sponsors)
	}

	for i := range summary.Sponsors {
		summary.Sponsors[i].AvatarURL = resolveRemoteURL(feedURL, summary.Sponsors[i].AvatarURL)
		if strings.TrimSpace(summary.Sponsors[i].Initial) == "" {
			summary.Sponsors[i].Initial = sponsorInitial(summary.Sponsors[i].Name)
		}
	}

	return summary, nil
}

func resolveRemoteURL(feedURL, target string) string {
	target = strings.TrimSpace(target)
	if target == "" {
		return ""
	}
	lower := strings.ToLower(target)
	if strings.HasPrefix(lower, "data:") {
		return target
	}

	base, err := url.Parse(feedURL)
	if err != nil {
		return target
	}
	ref, err := url.Parse(target)
	if err != nil {
		return target
	}

	if ref.IsAbs() {
		return normalizeResolvedRemoteURL(base, ref)
	}

	return normalizeResolvedRemoteURL(base, base.ResolveReference(ref))
}

func normalizeResolvedRemoteURL(base, resolved *url.URL) string {
	if base == nil || resolved == nil {
		return ""
	}

	if strings.EqualFold(base.Scheme, "https") &&
		strings.EqualFold(resolved.Scheme, "http") &&
		strings.EqualFold(base.Hostname(), resolved.Hostname()) &&
		base.Hostname() != "" {
		cloned := *resolved
		cloned.Scheme = "https"
		return cloned.String()
	}

	return resolved.String()
}

func sponsorInitial(name string) string {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return "赞"
	}
	r, _ := utf8.DecodeRuneInString(trimmed)
	if r == utf8.RuneError {
		return "赞"
	}
	return string(r)
}
