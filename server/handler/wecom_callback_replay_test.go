package handler_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"daidai-panel/model"
)

// 安全审计 M-2（v3.4.4）：企业微信回调防重放 —— 时间窗 + 报文指纹去重 + MsgId 去重。
// 复用 wecom_callback_test.go 里的测试辅助（同一个 handler_test 包）。
// 去重表是包级共享的，所以这里的 nonce / MsgId 一律取自 wecomTestSeq，保证跨用例、跨 -count 轮次都不撞。

// postWecomRaw 与 postWecomMessage 相同，只是 timestamp / nonce 由调用方指定，用来构造重放与过期报文。
func postWecomRaw(t *testing.T, engine http.Handler, path string, trigger *model.WecomTrigger, timestamp, nonce, innerXML string) *httptest.ResponseRecorder {
	t.Helper()

	encrypted := encryptWecomMessage(t, trigger.EncodingAESKey, trigger.CorpID, innerXML)
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

// wecomTextMessageWithID 构造一条带指定 MsgId 的文字消息。
func wecomTextMessageWithID(content string, msgID int64) string {
	return fmt.Sprintf(`<xml><ToUserName><![CDATA[%s]]></ToUserName><FromUserName><![CDATA[zhangsan]]></FromUserName>`+
		`<CreateTime>%d</CreateTime><MsgType><![CDATA[text]]></MsgType><Content><![CDATA[%s]]></Content>`+
		`<MsgId>%d</MsgId><AgentID>1000002</AgentID></xml>`, wecomTestCorpID, time.Now().Unix(), content, msgID)
}

func nextWecomNonce() string {
	return strconv.FormatInt(wecomTestSeq.Add(1), 10)
}

func nowWecomTimestamp(offsetSeconds int64) string {
	return strconv.FormatInt(time.Now().Unix()+offsetSeconds, 10)
}

// TestWecomCallbackIgnoresExactReplay：同一份报文（同 timestamp / nonce / 密文）再投一次，200 空串、不再触发。
func TestWecomCallbackIgnoresExactReplay(t *testing.T) {
	engine := newWecomTestEngine(t)
	setWecomSwitches(t, true, true)
	trigger := mustCreateWecomTrigger(t, "")
	task := mustCreateWecomTask(t, "签到任务")
	path := fmt.Sprintf("/api/v1/wecom/callback/%d", trigger.ID)

	timestamp, nonce := nowWecomTimestamp(0), nextWecomNonce()
	// 菜单事件没有 MsgId，只能靠外层报文指纹认出重放。
	inner := wecomMenuEvent(fmt.Sprintf("run %d", task.ID))

	for i := 1; i <= 3; i++ {
		rec := postWecomRaw(t, engine, path, trigger, timestamp, nonce, inner)
		// 重放也回 200 空串：它可能是企业微信自己的重试，回非 200 只会招来更多重试。
		if rec.Code != http.StatusOK || rec.Body.Len() != 0 {
			t.Fatalf("第 %d 次期望 200 空串，实际 %d：%q", i, rec.Code, rec.Body.String())
		}
	}
	if endpoints := apiCallEndpoints(t); len(endpoints) != 1 {
		t.Fatalf("同一份报文只应触发一次，实际回放了: %v", endpoints)
	}
}

// TestWecomCallbackIgnoresSameMsgIdWithNewNonce：企业微信超时重试会换 nonce 重发同一条消息，靠 MsgId 认出来。
func TestWecomCallbackIgnoresSameMsgIdWithNewNonce(t *testing.T) {
	engine := newWecomTestEngine(t)
	setWecomSwitches(t, true, true)
	trigger := mustCreateWecomTrigger(t, "")
	task := mustCreateWecomTask(t, "签到任务")
	path := fmt.Sprintf("/api/v1/wecom/callback/%d", trigger.ID)

	inner := wecomTextMessageWithID(fmt.Sprintf("run %d", task.ID), 9000000000000000+wecomTestSeq.Add(1))
	for i := 1; i <= 2; i++ {
		rec := postWecomRaw(t, engine, path, trigger, nowWecomTimestamp(0), nextWecomNonce(), inner)
		if rec.Code != http.StatusOK || rec.Body.Len() != 0 {
			t.Fatalf("第 %d 次期望 200 空串，实际 %d：%q", i, rec.Code, rec.Body.String())
		}
	}
	if endpoints := apiCallEndpoints(t); len(endpoints) != 1 {
		t.Fatalf("同一个 MsgId 只应触发一次，实际回放了: %v", endpoints)
	}
}

// TestWecomCallbackRejectsStaleTimestamp：时间戳超出 ±300 秒一律 401、不执行；窗口内照常执行。
func TestWecomCallbackRejectsStaleTimestamp(t *testing.T) {
	engine := newWecomTestEngine(t)
	setWecomSwitches(t, true, true)
	trigger := mustCreateWecomTrigger(t, "")
	task := mustCreateWecomTask(t, "签到任务")
	path := fmt.Sprintf("/api/v1/wecom/callback/%d", trigger.ID)
	command := fmt.Sprintf("run %d", task.ID)

	// 留出几秒余量，免得用例恰好跨秒时落在边界上。
	for name, offset := range map[string]int64{"过去 310 秒": -310, "未来 310 秒": 310} {
		rec := postWecomRaw(t, engine, path, trigger, nowWecomTimestamp(offset), nextWecomNonce(),
			wecomTextMessageWithID(command, 9100000000000000+wecomTestSeq.Add(1)))
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s的报文期望 401，实际 %d：%s", name, rec.Code, rec.Body.String())
		}
	}
	assertNoDispatch(t)

	rec := postWecomRaw(t, engine, path, trigger, nowWecomTimestamp(-200), nextWecomNonce(),
		wecomTextMessageWithID(command, 9200000000000000+wecomTestSeq.Add(1)))
	if rec.Code != http.StatusOK {
		t.Fatalf("窗口内（200 秒前）的报文期望 200，实际 %d：%s", rec.Code, rec.Body.String())
	}
	if endpoints := apiCallEndpoints(t); len(endpoints) != 1 {
		t.Fatalf("窗口内的报文应当触发一次，实际: %v", endpoints)
	}
}
