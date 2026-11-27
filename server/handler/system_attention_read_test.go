package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"daidai-panel/database"
	"daidai-panel/model"
	"daidai-panel/testutil"

	"github.com/gin-gonic/gin"
)

// 「需要关注」已读（#186，v4.0.3）。同用 package handler，理由同 system_attention_test.go：要替换 attentionDiskUsage。

// attentionDo 用指定令牌发一次请求；body 为空串时不带请求体。
func attentionDo(t *testing.T, engine *gin.Engine, method, path, token, body string) (*httptest.ResponseRecorder, map[string]interface{}) {
	t.Helper()
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, path, nil)
	} else {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	payload := map[string]interface{}{}
	_ = json.Unmarshal(rec.Body.Bytes(), &payload)
	return rec, payload
}

// attentionUserToken 建一个用户并签一枚登录令牌
func attentionUserToken(t *testing.T, username, role string) string {
	t.Helper()
	user := testutil.MustCreateUser(t, username, role)
	return testutil.MustCreateAccessToken(t, user.Username, user.Role)
}

// attentionReadLogID：本文件建的日志用这个往上递增的 ID，不用自增 ID。
// service 里 TaskLogAttentionDetail 按日志 ID 做进程级缓存，每个用例的库都从 1 开始自增，
// 本文件先跑、缓存下「ID 3 → 空 detail」，会让后面 system_attention_test.go 同 ID 的日志拿到错的 detail。
var attentionReadLogID uint = 1_000_000

// createAttentionLog 用独立的高位 ID 写一条已结束日志
func createAttentionLog(t *testing.T, taskID uint, status int, startedAt time.Time) {
	t.Helper()
	attentionReadLogID++
	endedAt := startedAt.Add(30 * time.Second)
	if err := database.DB.Create(&model.TaskLog{ID: attentionReadLogID, TaskID: taskID, Status: &status, StartedAt: startedAt, EndedAt: &endedAt}).Error; err != nil {
		t.Fatalf("create log: %v", err)
	}
}

// mkFailingTask 建一个启用中、最近 3 次都失败的任务（满足连续失败阈值）
func mkFailingTask(t *testing.T, name string) *model.Task {
	t.Helper()
	failed := model.RunFailed
	task := &model.Task{Name: name, Command: "echo", CronExpression: "0 * * * *", TaskType: model.TaskTypeCron, Status: model.TaskStatusEnabled, LastRunStatus: &failed}
	if err := database.DB.Create(task).Error; err != nil {
		t.Fatalf("create task: %v", err)
	}
	base := time.Now().Add(-time.Hour)
	for i := 0; i < 3; i++ {
		createAttentionLog(t, task.ID, model.LogStatusFailed, base.Add(time.Duration(i)*time.Minute))
	}
	return task
}

// attentionReadMap 把 GET 返回的条目整理成 key → read，顺带返回按下发顺序的 key 列表
func attentionReadMap(t *testing.T, payload map[string]interface{}) (map[string]bool, []string) {
	t.Helper()
	items, ok := payload["items"].([]interface{})
	if !ok {
		t.Fatalf("expected items array, got %#v", payload)
	}
	result := map[string]bool{}
	keys := make([]string, 0, len(items))
	for _, raw := range items {
		item := raw.(map[string]interface{})
		key := item["key"].(string)
		read, ok := item["read"].(bool)
		if !ok {
			t.Fatalf("条目 %s 缺少 read 字段：%#v", key, item)
		}
		result[key] = read
		keys = append(keys, key)
	}
	return result, keys
}

// attentionSeenItems 从 GET 结果里挑出指定 key 的条目，原样拼成 POST 已读的请求体（模拟客户端回传它看到的版本）
func attentionSeenItems(t *testing.T, payload map[string]interface{}, keys ...string) string {
	t.Helper()
	items, ok := payload["items"].([]interface{})
	if !ok {
		t.Fatalf("expected items array, got %#v", payload)
	}
	wanted := map[string]bool{}
	for _, key := range keys {
		wanted[key] = true
	}
	seen := make([]map[string]interface{}, 0, len(keys))
	for _, raw := range items {
		item := raw.(map[string]interface{})
		if wanted[item["key"].(string)] {
			seen = append(seen, map[string]interface{}{"key": item["key"], "at": item["at"], "level": item["level"]})
		}
	}
	if len(seen) != len(keys) {
		t.Fatalf("清单里找不到全部 key %v：%#v", keys, items)
	}
	body, _ := json.Marshal(map[string]interface{}{"items": seen})
	return string(body)
}

// addFailedLog 给任务再补一条更新的失败日志：条目的 at 随之变化
func addFailedLog(t *testing.T, taskID uint, startedAt time.Time) {
	t.Helper()
	createAttentionLog(t, taskID, model.LogStatusFailed, startedAt)
}

func TestAttentionReadMarkAndReappearWhenAtChanges(t *testing.T) {
	testutil.SetupTestEnv(t)
	engine := newInsightTestRouter()
	token := attentionUserToken(t, "attn-viewer", "viewer")

	task := mkFailingTask(t, "签到")
	other := mkFailingTask(t, "领奖")
	taskKey := "task_failing:" + itoa(task.ID)
	otherKey := "task_failing:" + itoa(other.ID)

	// 没标记前全部未读
	_, payload := attentionDo(t, engine, http.MethodGet, "/api/v1/system/attention", token, "")
	reads, _ := attentionReadMap(t, payload)
	if reads[taskKey] || reads[otherKey] {
		t.Fatalf("未标记前应全部未读：%v", reads)
	}

	// 回传看到的那条，外加一个不在清单里的 key（忽略，不计入 marked）
	seen := attentionSeenItems(t, payload, taskKey)
	body := strings.Replace(seen, `"items":[`, `"items":[{"key":"task_failing:999999","at":"2026-11-27T22:00:00+08:00","level":"err"},`, 1)
	rec, payload := attentionDo(t, engine, http.MethodPost, "/api/v1/system/attention/read", token, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("标记已读应 200，实际 %d %s", rec.Code, rec.Body.String())
	}
	if payload["marked"] != float64(1) {
		t.Fatalf("marked 应为 1，实际 %v", payload["marked"])
	}
	_, payload = attentionDo(t, engine, http.MethodGet, "/api/v1/system/attention", token, "")
	reads, _ = attentionReadMap(t, payload)
	if !reads[taskKey] || reads[otherKey] {
		t.Fatalf("只有标记的那条已读：%v", reads)
	}

	// 再失败一次：at 变了，回到未读
	addFailedLog(t, task.ID, time.Now().Add(time.Minute))
	_, payload = attentionDo(t, engine, http.MethodGet, "/api/v1/system/attention", token, "")
	reads, _ = attentionReadMap(t, payload)
	if reads[taskKey] {
		t.Fatalf("at 变化后应重新未读：%v", reads)
	}
}

// 客户端回传的是它看到的旧版本（两次轮询之间又失败了一次）：不写入，新失败保持未读
func TestAttentionReadSkipsStaleClientVersion(t *testing.T) {
	testutil.SetupTestEnv(t)
	engine := newInsightTestRouter()
	token := attentionUserToken(t, "attn-stale", "viewer")

	task := mkFailingTask(t, "签到")
	taskKey := "task_failing:" + itoa(task.ID)

	_, payload := attentionDo(t, engine, http.MethodGet, "/api/v1/system/attention", token, "")
	oldSeen := attentionSeenItems(t, payload, taskKey)

	addFailedLog(t, task.ID, time.Now().Add(time.Minute))

	rec, payload := attentionDo(t, engine, http.MethodPost, "/api/v1/system/attention/read", token, oldSeen)
	if rec.Code != http.StatusOK || payload["marked"] != float64(0) {
		t.Fatalf("旧版本不应写入，实际 %d %s", rec.Code, rec.Body.String())
	}
	_, payload = attentionDo(t, engine, http.MethodGet, "/api/v1/system/attention", token, "")
	reads, _ := attentionReadMap(t, payload)
	if reads[taskKey] {
		t.Fatalf("新失败应保持未读：%v", reads)
	}
	var count int64
	database.DB.Model(&model.AttentionRead{}).Count(&count)
	if count != 0 {
		t.Fatalf("不应写入已读记录，实际 %d 条", count)
	}

	// at 写成乱码：视为对不上
	rec, payload = attentionDo(t, engine, http.MethodPost, "/api/v1/system/attention/read", token,
		fmt.Sprintf(`{"items":[{"key":"%s","at":"not-a-time","level":"err"}]}`, taskKey))
	if rec.Code != http.StatusOK || payload["marked"] != float64(0) {
		t.Fatalf("解析不了的 at 不应写入，实际 %d %s", rec.Code, rec.Body.String())
	}
}

// 版本按 UTC 比较：客户端回传同一时刻的其它时区写法也算对得上
func TestAttentionReadComparesAtInUTC(t *testing.T) {
	testutil.SetupTestEnv(t)
	engine := newInsightTestRouter()
	token := attentionUserToken(t, "attn-utc", "viewer")

	task := mkFailingTask(t, "签到")
	taskKey := "task_failing:" + itoa(task.ID)

	_, payload := attentionDo(t, engine, http.MethodGet, "/api/v1/system/attention", token, "")
	var at string
	for _, raw := range payload["items"].([]interface{}) {
		item := raw.(map[string]interface{})
		if item["key"] == taskKey {
			at = item["at"].(string)
		}
	}
	parsed, err := time.Parse(time.RFC3339, at)
	if err != nil {
		t.Fatalf("at 不是 RFC3339：%q", at)
	}
	// 换成 UTC-05:00 的写法，同一时刻
	otherZone := parsed.In(time.FixedZone("UTC-5", -5*3600)).Format(time.RFC3339)
	if otherZone == at {
		t.Fatalf("测试前提：换时区后的写法应与原写法不同")
	}

	rec, payload := attentionDo(t, engine, http.MethodPost, "/api/v1/system/attention/read", token,
		fmt.Sprintf(`{"items":[{"key":"%s","at":"%s","level":"err"}]}`, taskKey, otherZone))
	if rec.Code != http.StatusOK || payload["marked"] != float64(1) {
		t.Fatalf("同一时刻换时区写法应写入，实际 %d %s", rec.Code, rec.Body.String())
	}
	_, payload = attentionDo(t, engine, http.MethodGet, "/api/v1/system/attention", token, "")
	reads, _ := attentionReadMap(t, payload)
	if !reads[taskKey] {
		t.Fatalf("应已读：%v", reads)
	}
}

func TestAttentionReadMultipleAndUserIsolation(t *testing.T) {
	testutil.SetupTestEnv(t)
	engine := newInsightTestRouter()
	tokenA := attentionUserToken(t, "attn-a", "viewer")
	tokenB := attentionUserToken(t, "attn-b", "viewer")

	first := mkFailingTask(t, "任务一")
	second := mkFailingTask(t, "任务二")
	firstKey := "task_failing:" + itoa(first.ID)
	secondKey := "task_failing:" + itoa(second.ID)

	_, payload := attentionDo(t, engine, http.MethodGet, "/api/v1/system/attention", tokenA, "")
	rec, payload := attentionDo(t, engine, http.MethodPost, "/api/v1/system/attention/read", tokenA, attentionSeenItems(t, payload, firstKey, secondKey))
	if rec.Code != http.StatusOK || payload["marked"] != float64(2) {
		t.Fatalf("应标记 2 条，实际 %d %s", rec.Code, rec.Body.String())
	}

	_, payload = attentionDo(t, engine, http.MethodGet, "/api/v1/system/attention", tokenA, "")
	reads, _ := attentionReadMap(t, payload)
	if !reads[firstKey] || !reads[secondKey] {
		t.Fatalf("用户 A 应全部已读：%v", reads)
	}

	// 用户 B 不受影响
	_, payload = attentionDo(t, engine, http.MethodGet, "/api/v1/system/attention", tokenB, "")
	reads, _ = attentionReadMap(t, payload)
	if reads[firstKey] || reads[secondKey] {
		t.Fatalf("用户 B 不应继承 A 的已读：%v", reads)
	}
}

func TestAttentionReadDiskHighVersionByLevel(t *testing.T) {
	testutil.SetupTestEnv(t)
	previousDisk := attentionDiskUsage
	usage := 90.0
	attentionDiskUsage = func() float64 { return usage }
	t.Cleanup(func() { attentionDiskUsage = previousDisk })
	engine := newInsightTestRouter()
	token := attentionUserToken(t, "attn-admin", "admin")

	markDisk := func() {
		t.Helper()
		_, payload := attentionDo(t, engine, http.MethodGet, "/api/v1/system/attention", token, "")
		rec, _ := attentionDo(t, engine, http.MethodPost, "/api/v1/system/attention/read", token, attentionSeenItems(t, payload, "disk_high"))
		if rec.Code != http.StatusOK {
			t.Fatalf("标记 disk_high 应 200，实际 %d %s", rec.Code, rec.Body.String())
		}
	}
	diskRead := func() bool {
		t.Helper()
		_, payload := attentionDo(t, engine, http.MethodGet, "/api/v1/system/attention", token, "")
		reads, _ := attentionReadMap(t, payload)
		return reads["disk_high"]
	}

	markDisk()
	// disk_high 的 at 每次都是 now，但版本按 level：同为 warn 仍是已读
	time.Sleep(1100 * time.Millisecond)
	if !diskRead() {
		t.Fatalf("warn 级已读后 at 变化不应回到未读")
	}

	// 升到 err：重新未读
	usage = 96
	if diskRead() {
		t.Fatalf("升到 err 后应重新未读")
	}

	// 回到 warn 再标记；磁盘降下去（条目消失）时 GET 清掉旧记录，再涨回 warn 就是未读
	usage = 90
	markDisk()
	usage = 50
	attentionDo(t, engine, http.MethodGet, "/api/v1/system/attention", token, "")
	var count int64
	database.DB.Model(&model.AttentionRead{}).Where("`key` = ?", "disk_high").Count(&count)
	if count != 0 {
		t.Fatalf("磁盘降下去后 GET 应清掉 disk_high 的已读记录，实际还剩 %d 条", count)
	}
	usage = 90
	if diskRead() {
		t.Fatalf("磁盘降下去再涨回来应重新未读")
	}
}

// 截断：21 条未读 warn + 1 条已读 err，err 仍在且排在原位置（最前），未读留前 20 条
func TestAttentionReadTruncateKeepsReadErrInPlace(t *testing.T) {
	testutil.SetupTestEnv(t)
	previousDisk := attentionDiskUsage
	attentionDiskUsage = func() float64 { return 0 }
	t.Cleanup(func() { attentionDiskUsage = previousDisk })
	engine := newInsightTestRouter()
	token := attentionUserToken(t, "attn-trunc", "admin")

	task := mkFailingTask(t, "连续失败")
	taskKey := "task_failing:" + itoa(task.ID)
	for i := 0; i < attentionMaxItems+1; i++ {
		dep := &model.Dependency{Type: "nodejs", Name: fmt.Sprintf("dep-%d", i), Status: model.DepStatusFailed}
		if err := database.DB.Create(dep).Error; err != nil {
			t.Fatalf("create dependency: %v", err)
		}
	}

	_, payload := attentionDo(t, engine, http.MethodGet, "/api/v1/system/attention", token, "")
	rec, _ := attentionDo(t, engine, http.MethodPost, "/api/v1/system/attention/read", token, attentionSeenItems(t, payload, taskKey))
	if rec.Code != http.StatusOK {
		t.Fatalf("标记已读应 200，实际 %d %s", rec.Code, rec.Body.String())
	}

	_, payload = attentionDo(t, engine, http.MethodGet, "/api/v1/system/attention", token, "")
	reads, keys := attentionReadMap(t, payload)
	if len(keys) != attentionMaxItems+1 {
		t.Fatalf("应下发 20 条未读 + 1 条已读，实际 %d 条：%v", len(keys), keys)
	}
	if keys[0] != taskKey || !reads[taskKey] {
		t.Fatalf("已读的 err 应仍排在最前：%v", keys)
	}
	unread := 0
	for _, read := range reads {
		if !read {
			unread++
		}
	}
	if unread != attentionMaxItems {
		t.Fatalf("未读应截断到 %d 条，实际 %d", attentionMaxItems, unread)
	}
}

// GET 时清理：key 已不在当前清单里的旧记录被删掉，当前条目的保留
func TestAttentionReadGetCleansStaleRecords(t *testing.T) {
	testutil.SetupTestEnv(t)
	engine := newInsightTestRouter()
	token := attentionUserToken(t, "attn-clean", "viewer")

	task := mkFailingTask(t, "会恢复的任务")
	keep := mkFailingTask(t, "还在失败的任务")
	taskKey := "task_failing:" + itoa(task.ID)
	keepKey := "task_failing:" + itoa(keep.ID)

	_, payload := attentionDo(t, engine, http.MethodGet, "/api/v1/system/attention", token, "")
	attentionDo(t, engine, http.MethodPost, "/api/v1/system/attention/read", token, attentionSeenItems(t, payload, taskKey, keepKey))

	// 任务恢复成功：不再出现在清单里
	createAttentionLog(t, task.ID, model.LogStatusSuccess, time.Now())

	attentionDo(t, engine, http.MethodGet, "/api/v1/system/attention", token, "")

	var stale int64
	database.DB.Model(&model.AttentionRead{}).Where("`key` = ?", taskKey).Count(&stale)
	if stale != 0 {
		t.Fatalf("已不在清单里的旧记录应被清理，实际还剩 %d 条", stale)
	}
	var kept int64
	database.DB.Model(&model.AttentionRead{}).Where("`key` = ?", keepKey).Count(&kept)
	if kept != 1 {
		t.Fatalf("当前条目的已读记录应保留，实际 %d 条", kept)
	}
}

func TestAttentionReadRejectsEmptyAndAppToken(t *testing.T) {
	testutil.SetupTestEnv(t)
	engine := newInsightTestRouter()
	token := attentionUserToken(t, "attn-empty", "viewer")

	for _, body := range []string{`{}`, `{"items":[]}`} {
		rec, _ := attentionDo(t, engine, http.MethodPost, "/api/v1/system/attention/read", token, body)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("空请求 %s 应 400，实际 %d %s", body, rec.Code, rec.Body.String())
		}
	}

	appToken := testutil.MustCreateAppToken(t, "attention-app", "system")
	rec, _ := attentionDo(t, engine, http.MethodPost, "/api/v1/system/attention/read", appToken,
		`{"items":[{"key":"disk_high","at":"2026-11-27T22:00:00+08:00","level":"warn"}]}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("应用令牌应 403，实际 %d %s", rec.Code, rec.Body.String())
	}
}
