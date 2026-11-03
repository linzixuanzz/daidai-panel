package handler_test

import (
	"net/http"
	"strconv"
	"testing"

	"daidai-panel/database"
	"daidai-panel/model"
	"daidai-panel/testutil"
)

// 新建订阅：越界的保存目录 / 别名在落库前就 400，正常的多级子目录与中文目录照常创建。
func TestSubscriptionCreateValidatesSaveDirAndAlias(t *testing.T) {
	testutil.SetupTestEnv(t)

	operator := testutil.MustCreateUser(t, "subscription-path-operator", "operator")
	token := testutil.MustCreateAccessToken(t, operator.Username, operator.Role)
	engine := newProtectedRouter()
	headers := map[string]string{"Authorization": "Bearer " + token}

	rejected := []string{
		`{"name":"a","type":"git-repo","url":"https://github.com/example/a.git","save_dir":"../escape"}`,
		`{"name":"b","type":"git-repo","url":"https://github.com/example/b.git","save_dir":"..\\escape"}`,
		`{"name":"c","type":"git-repo","url":"https://github.com/example/c.git","save_dir":"/etc"}`,
		`{"name":"d","type":"git-repo","url":"https://github.com/example/d.git","save_dir":"C:\\Windows"}`,
		`{"name":"e","type":"git-repo","url":"https://github.com/example/e.git","alias":"../escape"}`,
		`{"name":"f","type":"git-repo","url":"https://github.com/example/f/"}`,
		`{"name":"g","type":"single-file","url":"https://example.com/raw/g.js","alias":"sub/g.js"}`,
		`{"name":"h","type":"single-file","url":"https://example.com/raw/h.js","alias":".."}`,
		`{"name":"i","type":"git-repo","url":"https://github.com/example/i.git","save_dir":"a\u0000b"}`,
	}
	for _, body := range rejected {
		rec := performJSONRequest(engine, http.MethodPost, "/api/v1/subscriptions", body, headers, "")
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 for %s, got %d body=%s", body, rec.Code, rec.Body.String())
		}
	}
	var count int64
	database.DB.Model(&model.Subscription{}).Count(&count)
	if count != 0 {
		t.Fatalf("expected no subscription persisted, got %d", count)
	}

	accepted := []string{
		`{"name":"ok1","type":"git-repo","url":"https://github.com/example/ok1.git","save_dir":"libs/utils"}`,
		`{"name":"ok2","type":"git-repo","url":"https://github.com/example/ok2.git","save_dir":"中文目录"}`,
		`{"name":"ok3","type":"single-file","url":"https://example.com/raw/ok3.js","save_dir":"报表","alias":"日报.py"}`,
	}
	for _, body := range accepted {
		rec := performJSONRequest(engine, http.MethodPost, "/api/v1/subscriptions", body, headers, "")
		if rec.Code != http.StatusCreated {
			t.Fatalf("expected 201 for %s, got %d body=%s", body, rec.Code, rec.Body.String())
		}
	}
}

// 更新订阅：改成越界值被拒；库里已有的存量值原样回传（App 每次保存都会这样）不拦，只改名字照常成功。
func TestSubscriptionUpdateValidatesOnlyChangedSaveDir(t *testing.T) {
	testutil.SetupTestEnv(t)

	operator := testutil.MustCreateUser(t, "subscription-path-editor", "operator")
	token := testutil.MustCreateAccessToken(t, operator.Username, operator.Role)
	engine := newProtectedRouter()
	headers := map[string]string{"Authorization": "Bearer " + token}

	sub := model.Subscription{Name: "legacy", Type: model.SubTypeGitRepo, URL: "https://github.com/example/legacy.git", SaveDir: "/legacy-abs", Enabled: true}
	if err := database.DB.Create(&sub).Error; err != nil {
		t.Fatalf("create subscription: %v", err)
	}
	path := "/api/v1/subscriptions/" + strconv.FormatUint(uint64(sub.ID), 10)

	rec := performJSONRequest(engine, http.MethodPut, path, `{"name":"legacy-renamed","save_dir":"/legacy-abs"}`, headers, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected unchanged legacy save_dir to pass, got %d body=%s", rec.Code, rec.Body.String())
	}

	rec = performJSONRequest(engine, http.MethodPut, path, `{"save_dir":"../escape"}`, headers, "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for escaping save_dir, got %d body=%s", rec.Code, rec.Body.String())
	}
	rec = performJSONRequest(engine, http.MethodPut, path, `{"type":"single-file","alias":"a/b.js"}`, headers, "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for single-file alias with separator, got %d body=%s", rec.Code, rec.Body.String())
	}

	var reloaded model.Subscription
	if err := database.DB.First(&reloaded, sub.ID).Error; err != nil {
		t.Fatalf("reload subscription: %v", err)
	}
	if reloaded.SaveDir != "/legacy-abs" || reloaded.Type != model.SubTypeGitRepo || reloaded.Name != "legacy-renamed" {
		t.Fatalf("unexpected subscription after rejected updates: %#v", reloaded)
	}
}
