package handler_test

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"daidai-panel/database"
	"daidai-panel/model"
	"daidai-panel/router"
	"daidai-panel/testutil"

	"github.com/gin-gonic/gin"
)

// 这组用例都走 router.Setup 装出的完整路由（与 mcp_test.go 同一做法）：
// 企业微信回调触发任务时会在进程内回放到真实的 /api/v1/tasks 接口，
// 只有整套路由都在，才能验证 scope 校验、调用审计这些「白拿」的能力真的生效了。

// 企业微信官方文档给出的示例凭据，pkg/wxcrypt 的用例也用同一组。
const (
	wecomTestToken          = "QDG6eK"
	wecomTestEncodingAESKey = "jWmYm7qr5nMoAUwZRjGtBxmz3KA1tkAj3ykkR6q2B2C"
	wecomTestCorpID         = "wx5823bf96d3bd56c7"

	wecomSampleMsgSignature = "5c45ff5e21c57e6ad56bac8758b79b1d9ac89fd3"
	wecomSampleTimestamp    = "1409659589"
	wecomSampleNonce        = "263014780"
	wecomSampleEchostr      = "P9nAzCzyDtyTWESHep1vC5X9xho/qYX3Zpb4yKa9SKld1DsH3Iyt3tP3zNdtp+4RPcs8TgAE7OaBO+FZXvnaqQ=="
	wecomSampleEchoPlain    = "1616140317555161061"
)

func newWecomTestEngine(t *testing.T) *gin.Engine {
	t.Helper()
	testutil.SetupTestEnv(t)
	// testutil 的建表清单里没有这张新表（那个文件不归本组改），用例自己补一次。
	if err := database.DB.AutoMigrate(&model.WecomTrigger{}); err != nil {
		t.Fatalf("migrate wecom_triggers: %v", err)
	}
	engine := gin.New()
	router.Setup(engine)
	return engine
}

func setWecomSwitches(t *testing.T, enabled, allowRun bool) {
	t.Helper()
	if err := model.SetConfig(model.WecomTriggerEnabledConfigKey, strconv.FormatBool(enabled)); err != nil {
		t.Fatalf("set wecom_trigger_enabled: %v", err)
	}
	if err := model.SetConfig(model.WecomTriggerAllowRunConfigKey, strconv.FormatBool(allowRun)); err != nil {
		t.Fatalf("set wecom_trigger_allow_run: %v", err)
	}
}

// mustCreateWecomTrigger 建一条接入配置，并顺手建好它要用的开放 API 应用。
func mustCreateWecomTrigger(t *testing.T, whitelist string) *model.WecomTrigger {
	t.Helper()
	app := testutil.MustCreateOpenApp(t, "wecom-app", "tasks")
	trigger := &model.WecomTrigger{
		Name:           "企业微信",
		CorpID:         wecomTestCorpID,
		AgentID:        "1000002",
		CallbackToken:  wecomTestToken,
		EncodingAESKey: wecomTestEncodingAESKey,
		OpenAppID:      app.ID,
		TaskWhitelist:  whitelist,
		Enabled:        true,
	}
	if err := database.DB.Create(trigger).Error; err != nil {
		t.Fatalf("create wecom trigger: %v", err)
	}
	return trigger
}

func mustCreateWecomTask(t *testing.T, name string) *model.Task {
	t.Helper()
	task := &model.Task{
		Name:           name,
		Command:        "echo hi",
		CronExpression: "0 0 * * *",
		TaskType:       model.TaskTypeCron,
		Status:         model.TaskStatusEnabled,
	}
	if err := database.DB.Create(task).Error; err != nil {
		t.Fatalf("create task: %v", err)
	}
	return task
}

// wecomTestSeq 给每条测试消息发一个不重复的 nonce / MsgId。
// 回调的去重表是包级、跨用例共享的（v3.4.4 防重放），写死同一个值的话，
// 后面的用例（以及 -count=2 的第二轮）会被当成重放直接忽略。
var wecomTestSeq atomic.Int64

// postWecomMessage 把一段业务 XML 按企业微信的规矩加密、算签名，再打到回调路由上。
func postWecomMessage(t *testing.T, engine http.Handler, path string, trigger *model.WecomTrigger, receiveID, innerXML string) *httptest.ResponseRecorder {
	t.Helper()

	encrypted := encryptWecomMessage(t, trigger.EncodingAESKey, receiveID, innerXML)
	// 时间戳必须是「现在」：v3.4.4 起回调会拒掉与服务器时间相差超过 300 秒的报文。
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	nonce := strconv.FormatInt(wecomTestSeq.Add(1), 10)
	signature := wecomSignature(trigger.CallbackToken, timestamp, nonce, encrypted)

	body := fmt.Sprintf(`<xml><ToUserName><![CDATA[%s]]></ToUserName><AgentID><![CDATA[%s]]></AgentID><Encrypt><![CDATA[%s]]></Encrypt></xml>`,
		trigger.CorpID, trigger.AgentID, encrypted)

	query := url.Values{}
	query.Set("msg_signature", signature)
	query.Set("timestamp", timestamp)
	query.Set("nonce", nonce)

	req := httptest.NewRequest(http.MethodPost, path+"?"+query.Encode(), strings.NewReader(body))
	req.Header.Set("Content-Type", "text/xml")
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	return rec
}

func wecomTextMessage(content string) string {
	return fmt.Sprintf(`<xml><ToUserName><![CDATA[%s]]></ToUserName><FromUserName><![CDATA[zhangsan]]></FromUserName>`+
		`<CreateTime>1700000000</CreateTime><MsgType><![CDATA[text]]></MsgType><Content><![CDATA[%s]]></Content>`+
		`<MsgId>%d</MsgId><AgentID>1000002</AgentID></xml>`, wecomTestCorpID, content, 1234567890123456+wecomTestSeq.Add(1))
}

func wecomMenuEvent(eventKey string) string {
	return fmt.Sprintf(`<xml><ToUserName><![CDATA[%s]]></ToUserName><FromUserName><![CDATA[zhangsan]]></FromUserName>`+
		`<CreateTime>1700000000</CreateTime><MsgType><![CDATA[event]]></MsgType><Event><![CDATA[click]]></Event>`+
		`<EventKey><![CDATA[%s]]></EventKey><AgentID>1000002</AgentID></xml>`, wecomTestCorpID, eventKey)
}

// apiCallEndpoints 把本次测试期间产生的调用审计取出来。
// 它是「回放真的经过了 JWTAuth → OpenAPIAccess」的唯一外部可观测证据：
// 回调对企业微信一律回 200 空串，从响应体上看不出到底跑没跑。
func apiCallEndpoints(t *testing.T) []string {
	t.Helper()
	var logs []model.ApiCallLog
	database.DB.Order("id ASC").Find(&logs)
	endpoints := make([]string, 0, len(logs))
	for _, item := range logs {
		endpoints = append(endpoints, item.Method+" "+item.Endpoint)
	}
	return endpoints
}

func assertNoDispatch(t *testing.T) {
	t.Helper()
	if endpoints := apiCallEndpoints(t); len(endpoints) != 0 {
		t.Fatalf("不应触发任何任务接口，实际回放了: %v", endpoints)
	}
}

// ---------------------------------------------------------------------------
// GET /wecom/callback/:id —— 企业微信后台保存配置时的 URL 验签
// ---------------------------------------------------------------------------

func TestWecomCallbackVerifyOfficialSample(t *testing.T) {
	engine := newWecomTestEngine(t)
	setWecomSwitches(t, true, true)
	trigger := mustCreateWecomTrigger(t, "")

	query := url.Values{}
	query.Set("msg_signature", wecomSampleMsgSignature)
	query.Set("timestamp", wecomSampleTimestamp)
	query.Set("nonce", wecomSampleNonce)
	query.Set("echostr", wecomSampleEchostr)

	for _, prefix := range []string{"/api/v1", "/api"} {
		// v1 与 legacy 必须成对注册：漏了 legacy，老客户端与按 /api 配好的回调会 404。
		path := fmt.Sprintf("%s/wecom/callback/%d?%s", prefix, trigger.ID, query.Encode())
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		engine.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("%s 期望 200，实际 %d：%s", prefix, rec.Code, rec.Body.String())
		}
		if rec.Body.String() != wecomSampleEchoPlain {
			t.Fatalf("%s 必须原样回明文 echostr\n  期望: %q\n  实际: %q", prefix, wecomSampleEchoPlain, rec.Body.String())
		}
	}
}

func TestWecomCallbackVerifyRejectsBadSignature(t *testing.T) {
	engine := newWecomTestEngine(t)
	setWecomSwitches(t, true, true)
	trigger := mustCreateWecomTrigger(t, "")

	query := url.Values{}
	query.Set("msg_signature", strings.Repeat("0", 40))
	query.Set("timestamp", wecomSampleTimestamp)
	query.Set("nonce", wecomSampleNonce)
	query.Set("echostr", wecomSampleEchostr)

	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/wecom/callback/%d?%s", trigger.ID, query.Encode()), nil)
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("签名不对时期望 401，实际 %d：%s", rec.Code, rec.Body.String())
	}
}

// TestWecomCallbackGateClosedByDefault 锁定「默认关」这条安全基线：
// 什么都不配的实例，这条公网路由必须是 403。
func TestWecomCallbackGateClosedByDefault(t *testing.T) {
	engine := newWecomTestEngine(t)
	trigger := mustCreateWecomTrigger(t, "")

	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/wecom/callback/%d?echostr=x", trigger.ID), nil)
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("总开关未开时期望 403，实际 %d：%s", rec.Code, rec.Body.String())
	}
}

func TestWecomCallbackUnknownTriggerReturns404(t *testing.T) {
	engine := newWecomTestEngine(t)
	setWecomSwitches(t, true, true)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/wecom/callback/999?echostr=x", nil)
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("接入配置不存在时期望 404，实际 %d：%s", rec.Code, rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// POST /wecom/callback/:id —— 收消息并触发
// ---------------------------------------------------------------------------

func TestWecomCallbackRunsTaskByID(t *testing.T) {
	engine := newWecomTestEngine(t)
	setWecomSwitches(t, true, true)
	trigger := mustCreateWecomTrigger(t, "")
	task := mustCreateWecomTask(t, "签到任务")

	path := fmt.Sprintf("/api/v1/wecom/callback/%d", trigger.ID)
	rec := postWecomMessage(t, engine, path, trigger, wecomTestCorpID, wecomTextMessage(fmt.Sprintf("run %d", task.ID)))

	// 企业微信要求 5 秒内响应，非 200 会被重试三次 —— 同一条指令就会跑三遍，所以这里必须是 200 空串。
	if rec.Code != http.StatusOK {
		t.Fatalf("期望 200，实际 %d：%s", rec.Code, rec.Body.String())
	}
	if rec.Body.Len() != 0 {
		t.Fatalf("回包必须是空串，实际: %q", rec.Body.String())
	}

	// 回放接口的状态码刻意不断言：测试环境没有装调度器，入队那一步一定会失败。
	// 这里要证明的是「请求确实经 JWTAuth + OpenAPIAccess 回放到了真实的运行接口」，
	// api_call_logs 里那一行就是唯一的外部证据（endpoint 记的是路由模板）。
	const want = "PUT /api/v1/tasks/:id/run"
	if endpoints := apiCallEndpoints(t); len(endpoints) != 1 || endpoints[0] != want {
		t.Fatalf("期望回放 %s，实际: %v", want, endpoints)
	}
}

func TestWecomCallbackRunsTaskByNameFromMenuEvent(t *testing.T) {
	engine := newWecomTestEngine(t)
	setWecomSwitches(t, true, true)
	trigger := mustCreateWecomTrigger(t, "")
	mustCreateWecomTask(t, "签到任务")

	path := fmt.Sprintf("/api/v1/wecom/callback/%d", trigger.ID)
	rec := postWecomMessage(t, engine, path, trigger, wecomTestCorpID, wecomMenuEvent("运行 签到任务"))

	if rec.Code != http.StatusOK {
		t.Fatalf("期望 200，实际 %d：%s", rec.Code, rec.Body.String())
	}
	if endpoints := apiCallEndpoints(t); len(endpoints) != 1 || endpoints[0] != "POST /api/v1/tasks/run-by-name" {
		t.Fatalf("菜单事件应当回放按名运行接口，实际: %v", endpoints)
	}
}

// TestWecomCallbackRejectsForeignCorpID 是这条公网路由最关键的一条：
// 密文本身完全合法（别的企业用同一把 AESKey 签出来的），但尾部 receiveid 不是本企业的，
// 必须在验签之后、触发之前被拦下来。漏掉 = 任何人都能拉起面板任务。
func TestWecomCallbackRejectsForeignCorpID(t *testing.T) {
	engine := newWecomTestEngine(t)
	setWecomSwitches(t, true, true)
	trigger := mustCreateWecomTrigger(t, "")
	task := mustCreateWecomTask(t, "签到任务")

	path := fmt.Sprintf("/api/v1/wecom/callback/%d", trigger.ID)
	rec := postWecomMessage(t, engine, path, trigger, "wx_someone_else", wecomTextMessage(fmt.Sprintf("run %d", task.ID)))

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("receiveid 不匹配时期望 401，实际 %d：%s", rec.Code, rec.Body.String())
	}
	assertNoDispatch(t)
}

func TestWecomCallbackRejectsTamperedSignature(t *testing.T) {
	engine := newWecomTestEngine(t)
	setWecomSwitches(t, true, true)
	trigger := mustCreateWecomTrigger(t, "")
	task := mustCreateWecomTask(t, "签到任务")

	encrypted := encryptWecomMessage(t, trigger.EncodingAESKey, wecomTestCorpID, wecomTextMessage(fmt.Sprintf("run %d", task.ID)))
	body := fmt.Sprintf(`<xml><Encrypt><![CDATA[%s]]></Encrypt></xml>`, encrypted)
	path := fmt.Sprintf("/api/v1/wecom/callback/%d?msg_signature=%s&timestamp=1700000000&nonce=123456",
		trigger.ID, strings.Repeat("0", 40))

	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("签名不对时期望 401，实际 %d：%s", rec.Code, rec.Body.String())
	}
	assertNoDispatch(t)
}

// TestWecomCallbackRespectsAllowRunSwitch 覆盖两级开关的第二级：
// 总开关开着（企业微信后台能把回调配置保存成功），但没允许执行时一条任务都不能跑。
func TestWecomCallbackRespectsAllowRunSwitch(t *testing.T) {
	engine := newWecomTestEngine(t)
	setWecomSwitches(t, true, false)
	trigger := mustCreateWecomTrigger(t, "")
	task := mustCreateWecomTask(t, "签到任务")

	path := fmt.Sprintf("/api/v1/wecom/callback/%d", trigger.ID)
	rec := postWecomMessage(t, engine, path, trigger, wecomTestCorpID, wecomTextMessage(fmt.Sprintf("run %d", task.ID)))

	if rec.Code != http.StatusOK {
		t.Fatalf("期望 200（不重试），实际 %d：%s", rec.Code, rec.Body.String())
	}
	assertNoDispatch(t)
}

func TestWecomCallbackRespectsTaskWhitelist(t *testing.T) {
	engine := newWecomTestEngine(t)
	setWecomSwitches(t, true, true)
	// 白名单只放行「签到任务」，另一条指令要被挡住。
	trigger := mustCreateWecomTrigger(t, "签到任务")
	mustCreateWecomTask(t, "签到任务")
	mustCreateWecomTask(t, "清理任务")

	path := fmt.Sprintf("/api/v1/wecom/callback/%d", trigger.ID)

	rec := postWecomMessage(t, engine, path, trigger, wecomTestCorpID, wecomTextMessage("运行 清理任务"))
	if rec.Code != http.StatusOK {
		t.Fatalf("期望 200，实际 %d：%s", rec.Code, rec.Body.String())
	}
	assertNoDispatch(t)

	rec = postWecomMessage(t, engine, path, trigger, wecomTestCorpID, wecomTextMessage("运行 签到任务"))
	if rec.Code != http.StatusOK {
		t.Fatalf("期望 200，实际 %d：%s", rec.Code, rec.Body.String())
	}
	if endpoints := apiCallEndpoints(t); len(endpoints) != 1 || endpoints[0] != "POST /api/v1/tasks/run-by-name" {
		t.Fatalf("白名单内的任务应当被触发，实际: %v", endpoints)
	}
}

// TestWecomCallbackIgnoresUnknownText 保证随口一句话不会被当成指令，也不会回任何东西。
func TestWecomCallbackIgnoresUnknownText(t *testing.T) {
	engine := newWecomTestEngine(t)
	setWecomSwitches(t, true, true)
	trigger := mustCreateWecomTrigger(t, "")
	mustCreateWecomTask(t, "签到任务")

	path := fmt.Sprintf("/api/v1/wecom/callback/%d", trigger.ID)
	for _, text := range []string{"你好", "running late", "runner", "运行", "run"} {
		rec := postWecomMessage(t, engine, path, trigger, wecomTestCorpID, wecomTextMessage(text))
		if rec.Code != http.StatusOK {
			t.Fatalf("文本 %q 期望 200，实际 %d：%s", text, rec.Code, rec.Body.String())
		}
	}
	assertNoDispatch(t)
}

// ---------------------------------------------------------------------------
// POST /tasks/run-by-name —— 重名必须显式报冲突，不能闷头跑第一条
// ---------------------------------------------------------------------------

func TestRunByNameRejectsDuplicateNames(t *testing.T) {
	engine := newWecomTestEngine(t)
	token := testutil.MustCreateAccessToken(t, "admin", "admin")
	mustCreateWecomTask(t, "签到任务")
	mustCreateWecomTask(t, "签到任务")

	rec := doWecomJSON(t, engine, http.MethodPost, "/api/v1/tasks/run-by-name",
		map[string]any{"name": "签到任务"}, map[string]string{"Authorization": "Bearer " + token})

	if rec.Code != http.StatusConflict {
		t.Fatalf("重名时期望 409，实际 %d：%s", rec.Code, rec.Body.String())
	}
	var payload struct {
		Error      string `json:"error"`
		Candidates []struct {
			ID   uint   `json:"id"`
			Name string `json:"name"`
		} `json:"candidates"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if len(payload.Candidates) != 2 {
		t.Fatalf("期望列出 2 个候选，实际: %+v", payload)
	}
}

func TestRunByNameReturns404WhenMissing(t *testing.T) {
	engine := newWecomTestEngine(t)
	token := testutil.MustCreateAccessToken(t, "admin", "admin")

	rec := doWecomJSON(t, engine, http.MethodPost, "/api/v1/tasks/run-by-name",
		map[string]any{"name": "不存在的任务"}, map[string]string{"Authorization": "Bearer " + token})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("期望 404，实际 %d：%s", rec.Code, rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// 方案 B：菜单触发链接
// ---------------------------------------------------------------------------

func TestTaskTriggerLinkIssueAndRevoke(t *testing.T) {
	engine := newWecomTestEngine(t)
	setWecomSwitches(t, true, true)
	app := testutil.MustCreateOpenApp(t, "wecom-link-app", "tasks")
	task := mustCreateWecomTask(t, "签到任务")
	token := testutil.MustCreateAccessToken(t, "admin", "admin")
	headers := map[string]string{"Authorization": "Bearer " + token}

	rec := doWecomJSON(t, engine, http.MethodPost, fmt.Sprintf("/api/v1/open-api/apps/%d/task-trigger-link", app.ID),
		map[string]any{"task_id": task.ID}, headers)
	if rec.Code != http.StatusOK {
		t.Fatalf("签发链接期望 200，实际 %d：%s", rec.Code, rec.Body.String())
	}
	var issued struct {
		Data struct {
			URL  string `json:"url"`
			Path string `json:"path"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &issued); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if !strings.HasPrefix(issued.Data.URL, "http://") || !strings.Contains(issued.Data.URL, issued.Data.Path) {
		t.Fatalf("应当返回完整 URL，实际: %+v", issued.Data)
	}
	if !strings.HasPrefix(issued.Data.Path, "/api/v1/open-api/trigger?") {
		t.Fatalf("触发路径应当跟着当前请求前缀走，实际: %s", issued.Data.Path)
	}

	// 免鉴权打开链接：验票通过（任务名会出现在回包里）。
	// 这里不断言「已触发」：测试环境没装调度器，入队一定失败，那一步由 task_control 的语义保证。
	page := doWecomGET(t, engine, issued.Data.Path, nil)
	if page.Code != http.StatusOK {
		t.Fatalf("凭票访问期望 200，实际 %d：%s", page.Code, page.Body.String())
	}
	if !strings.Contains(page.Body.String(), "签到任务") {
		t.Fatalf("回包里应当带上任务名，实际: %s", page.Body.String())
	}
	if ct := page.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("企业微信内置浏览器会直接打开这个链接，必须回 HTML，实际 Content-Type: %s", ct)
	}

	// 作废全部链接：代次 +1，刚才那张票立刻失效。
	rec = doWecomJSON(t, engine, http.MethodPost, "/api/v1/open-api/task-trigger-links/revoke", nil, headers)
	if rec.Code != http.StatusOK {
		t.Fatalf("作废期望 200，实际 %d：%s", rec.Code, rec.Body.String())
	}
	page = doWecomGET(t, engine, issued.Data.Path, nil)
	if page.Code != http.StatusUnauthorized {
		t.Fatalf("作废后旧链接期望 401，实际 %d：%s", page.Code, page.Body.String())
	}
}

// TestTaskTriggerLinkCannotBeMovedToAnotherTask 锁定「票据绑死单个任务」这条性质：
// 把 URL 上的 task_id 改成另一个任务，验签必然失败。
func TestTaskTriggerLinkCannotBeMovedToAnotherTask(t *testing.T) {
	engine := newWecomTestEngine(t)
	setWecomSwitches(t, true, true)
	app := testutil.MustCreateOpenApp(t, "wecom-link-app", "tasks")
	task := mustCreateWecomTask(t, "签到任务")
	other := mustCreateWecomTask(t, "清理任务")
	headers := map[string]string{"Authorization": "Bearer " + testutil.MustCreateAccessToken(t, "admin", "admin")}

	rec := doWecomJSON(t, engine, http.MethodPost, fmt.Sprintf("/api/v1/open-api/apps/%d/task-trigger-link", app.ID),
		map[string]any{"task_id": task.ID}, headers)
	if rec.Code != http.StatusOK {
		t.Fatalf("签发链接期望 200，实际 %d：%s", rec.Code, rec.Body.String())
	}
	var issued struct {
		Data struct {
			Path string `json:"path"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &issued); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}

	moved := strings.Replace(issued.Data.Path,
		"task_id="+strconv.FormatUint(uint64(task.ID), 10),
		"task_id="+strconv.FormatUint(uint64(other.ID), 10), 1)
	page := doWecomGET(t, engine, moved, nil)
	if page.Code != http.StatusUnauthorized {
		t.Fatalf("挪到别的任务上期望 401，实际 %d：%s", page.Code, page.Body.String())
	}
}

func TestTaskTriggerRejectedWhenSwitchOff(t *testing.T) {
	engine := newWecomTestEngine(t)
	// 总开关默认关：连票都不看，直接 403。
	page := doWecomGET(t, engine, "/api/v1/open-api/trigger?task_id=1&ticket=whatever", nil)
	if page.Code != http.StatusForbidden {
		t.Fatalf("总开关未开时期望 403，实际 %d：%s", page.Code, page.Body.String())
	}
}

// ---------------------------------------------------------------------------
// 接入配置管理接口
// ---------------------------------------------------------------------------

func TestWecomTriggerCrudNeverLeaksCredentials(t *testing.T) {
	engine := newWecomTestEngine(t)
	app := testutil.MustCreateOpenApp(t, "wecom-app", "tasks")
	headers := map[string]string{"Authorization": "Bearer " + testutil.MustCreateAccessToken(t, "admin", "admin")}

	rec := doWecomJSON(t, engine, http.MethodPost, "/api/v1/wecom/triggers", map[string]any{
		"name":             "企业微信",
		"corp_id":          wecomTestCorpID,
		"agent_id":         "1000002",
		"callback_token":   wecomTestToken,
		"encoding_aes_key": wecomTestEncodingAESKey,
		"open_app_id":      app.ID,
	}, headers)
	if rec.Code != http.StatusCreated {
		t.Fatalf("创建期望 201，实际 %d：%s", rec.Code, rec.Body.String())
	}
	// Token 与 EncodingAESKey 只允许写入、不允许读回：它们泄漏任意一个都等于把公网入口交出去。
	if strings.Contains(rec.Body.String(), wecomTestToken) || strings.Contains(rec.Body.String(), wecomTestEncodingAESKey) {
		t.Fatalf("响应体不得回显凭据：%s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "/api/v1/wecom/callback/") {
		t.Fatalf("响应体应当带上拼好的回调地址：%s", rec.Body.String())
	}

	var created model.WecomTrigger
	if err := database.DB.Order("id DESC").First(&created).Error; err != nil {
		t.Fatalf("load created trigger: %v", err)
	}

	// 按键更新：只传 name，凭据必须原样保留。
	rec = doWecomJSON(t, engine, http.MethodPut, fmt.Sprintf("/api/v1/wecom/triggers/%d", created.ID),
		map[string]any{"name": "改个名"}, headers)
	if rec.Code != http.StatusOK {
		t.Fatalf("更新期望 200，实际 %d：%s", rec.Code, rec.Body.String())
	}
	// 凭据传空串同样当「不改」：前端密码框回显不出原值，保存时带的就是空串。
	rec = doWecomJSON(t, engine, http.MethodPut, fmt.Sprintf("/api/v1/wecom/triggers/%d", created.ID),
		map[string]any{"callback_token": "", "encoding_aes_key": ""}, headers)
	if rec.Code != http.StatusOK {
		t.Fatalf("空串更新期望 200，实际 %d：%s", rec.Code, rec.Body.String())
	}

	var after model.WecomTrigger
	if err := database.DB.First(&after, created.ID).Error; err != nil {
		t.Fatalf("reload trigger: %v", err)
	}
	if after.Name != "改个名" || after.CallbackToken != wecomTestToken || after.EncodingAESKey != wecomTestEncodingAESKey {
		t.Fatalf("按键更新不应动到没传的字段，实际: %+v", after)
	}
}

// TestWecomTriggerRejectsBadConfig 把「配错了要等企业微信验签失败才发现」的两件事提前到保存时。
func TestWecomTriggerRejectsBadConfig(t *testing.T) {
	engine := newWecomTestEngine(t)
	noScope := testutil.MustCreateOpenApp(t, "wecom-noscope", "envs")
	ok := testutil.MustCreateOpenApp(t, "wecom-ok", "tasks")
	headers := map[string]string{"Authorization": "Bearer " + testutil.MustCreateAccessToken(t, "admin", "admin")}

	base := map[string]any{
		"name":             "企业微信",
		"corp_id":          wecomTestCorpID,
		"callback_token":   wecomTestToken,
		"encoding_aes_key": wecomTestEncodingAESKey,
		"open_app_id":      ok.ID,
	}

	shortKey := map[string]any{}
	for k, v := range base {
		shortKey[k] = v
	}
	shortKey["encoding_aes_key"] = "tooshort"
	if rec := doWecomJSON(t, engine, http.MethodPost, "/api/v1/wecom/triggers", shortKey, headers); rec.Code != http.StatusBadRequest {
		t.Fatalf("EncodingAESKey 长度不对时期望 400，实际 %d：%s", rec.Code, rec.Body.String())
	}

	missingScope := map[string]any{}
	for k, v := range base {
		missingScope[k] = v
	}
	missingScope["open_app_id"] = noScope.ID
	if rec := doWecomJSON(t, engine, http.MethodPost, "/api/v1/wecom/triggers", missingScope, headers); rec.Code != http.StatusBadRequest {
		t.Fatalf("应用没有 tasks 权限时期望 400，实际 %d：%s", rec.Code, rec.Body.String())
	}
}

// TestWecomTriggerManagementRequiresAdmin 确认管理接口没有跟着公开回调一起变成免鉴权。
func TestWecomTriggerManagementRequiresAdmin(t *testing.T) {
	engine := newWecomTestEngine(t)

	rec := doWecomGET(t, engine, "/api/v1/wecom/triggers", nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("不带令牌期望 401，实际 %d：%s", rec.Code, rec.Body.String())
	}

	viewer := map[string]string{"Authorization": "Bearer " + testutil.MustCreateAccessToken(t, "viewer", "viewer")}
	rec = doWecomGET(t, engine, "/api/v1/wecom/triggers", viewer)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("非管理员期望 403，实际 %d：%s", rec.Code, rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// 测试辅助
// ---------------------------------------------------------------------------

func doWecomJSON(t *testing.T, engine http.Handler, method, path string, body any, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		reader = bytes.NewReader(data)
	} else {
		reader = bytes.NewReader([]byte("{}"))
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Content-Type", "application/json")
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	return rec
}

func doWecomGET(t *testing.T, engine http.Handler, path string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	return rec
}

// wecomSignature / encryptWecomMessage 是企业微信那套算法的「发送端」，
// 只给用例构造合法请求用。生产代码不需要加密能力（回包一律回空串），
// 所以 pkg/wxcrypt 刻意没有导出 Encrypt，这里就地写一份镜像实现。
func wecomSignature(token, timestamp, nonce, encrypt string) string {
	// 与 wxcrypt.Signature 同一口径：四个串排序后直接拼接取 sha1 十六进制。
	parts := []string{token, timestamp, nonce, encrypt}
	sort.Strings(parts)
	sum := sha1.Sum([]byte(strings.Join(parts, "")))
	return hex.EncodeToString(sum[:])
}

func encryptWecomMessage(t *testing.T, encodingAESKey, receiveID, msg string) string {
	t.Helper()

	key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encodingAESKey) + "=")
	if err != nil || len(key) != 32 {
		t.Fatalf("decode EncodingAESKey: %v", err)
	}

	var buf []byte
	buf = append(buf, []byte(strings.Repeat("a", 16))...)
	var length [4]byte
	binary.BigEndian.PutUint32(length[:], uint32(len(msg)))
	buf = append(buf, length[:]...)
	buf = append(buf, []byte(msg)...)
	buf = append(buf, []byte(receiveID)...)

	// 企业微信的填充按 32 字节对齐，不是 AES 的 16。
	pad := 32 - len(buf)%32
	for i := 0; i < pad; i++ {
		buf = append(buf, byte(pad))
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatalf("new cipher: %v", err)
	}
	out := make([]byte, len(buf))
	cipher.NewCBCEncrypter(block, key[:aes.BlockSize]).CryptBlocks(out, buf)
	return base64.StdEncoding.EncodeToString(out)
}
