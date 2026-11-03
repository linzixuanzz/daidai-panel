package handler

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"daidai-panel/database"
	"daidai-panel/model"
	"daidai-panel/service"
	"daidai-panel/testutil"

	"github.com/gin-gonic/gin"
)

// 这一组用例守住安全审计接入敏感操作：成功才记、失败不记、操作者认得出、机密不进审计表。

func newAuditTestEngine() *gin.Engine {
	engine := gin.New()
	api := engine.Group("/api/v1")
	NewAuthHandler().RegisterRoutes(api)
	NewSystemHandler().RegisterRoutes(api)
	NewConfigHandler().RegisterRoutes(api)
	return engine
}

func auditJSONRequest(engine *gin.Engine, method, path, token, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	return rec
}

func auditRows(t *testing.T, action string) []model.SecurityAudit {
	t.Helper()
	var rows []model.SecurityAudit
	if err := database.DB.Where("action = ?", action).Order("id ASC").Find(&rows).Error; err != nil {
		t.Fatalf("query security audits: %v", err)
	}
	return rows
}

// 改密码：旧密码不对的那次不记；改成功记一条，操作者与用户 ID 对得上，审计里不出现密码。
func TestSecurityAuditRecordsPasswordChange(t *testing.T) {
	testutil.SetupTestEnv(t)
	engine := newAuditTestEngine()

	user := testutil.MustCreateLoginUser(t, "audit-pwd-user", "admin", "Old-pass-123")
	token := testutil.MustCreateAccessToken(t, user.Username, user.Role)

	wrong := auditJSONRequest(engine, http.MethodPut, "/api/v1/auth/password", token,
		`{"old_password":"not-the-password-1","new_password":"New-pass-456"}`)
	if wrong.Code != http.StatusBadRequest {
		t.Fatalf("旧密码错误应返回 400，实际 %d %s", wrong.Code, wrong.Body.String())
	}
	if rows := auditRows(t, service.AuditPasswordChange); len(rows) != 0 {
		t.Fatalf("失败的改密不应写审计，实际 %d 条", len(rows))
	}

	ok := auditJSONRequest(engine, http.MethodPut, "/api/v1/auth/password", token,
		`{"old_password":"Old-pass-123","new_password":"New-pass-456"}`)
	if ok.Code != http.StatusOK {
		t.Fatalf("改密应成功，实际 %d %s", ok.Code, ok.Body.String())
	}
	rows := auditRows(t, service.AuditPasswordChange)
	if len(rows) != 1 {
		t.Fatalf("改密成功应写 1 条审计，实际 %d 条", len(rows))
	}
	if rows[0].Username != user.Username || rows[0].UserID == nil || *rows[0].UserID != user.ID {
		t.Fatalf("审计的操作者应是 %s(#%d)，实际 %+v", user.Username, user.ID, rows[0])
	}
	if strings.Contains(rows[0].Detail, "Old-pass-123") || strings.Contains(rows[0].Detail, "New-pass-456") {
		t.Fatalf("审计里不能出现密码，实际 detail=%q", rows[0].Detail)
	}
}

// 创建 / 恢复备份：成功各记一条；恢复不存在的文件不记；应用令牌发起的操作要看得出是哪个应用。
func TestSecurityAuditRecordsBackupCreateAndRestore(t *testing.T) {
	testutil.SetupTestEnv(t)
	engine := newAuditTestEngine()

	admin := testutil.MustCreateUser(t, "audit-backup-admin", "admin")
	adminToken := testutil.MustCreateAccessToken(t, admin.Username, admin.Role)
	appToken := testutil.MustCreateAppToken(t, "audit-backup-app", "backup")

	created := auditJSONRequest(engine, http.MethodPost, "/api/v1/system/backup", appToken,
		`{"name":"audit-by-app","selection":{"tasks":true}}`)
	if created.Code != http.StatusOK {
		t.Fatalf("应用令牌创建备份应成功，实际 %d %s", created.Code, created.Body.String())
	}
	data, _ := decodeConsoleJSON(t, created)["data"].(map[string]interface{})
	backupPath, _ := data["path"].(string)
	if backupPath == "" {
		t.Fatalf("创建备份的响应里缺少 path：%s", created.Body.String())
	}
	createRows := auditRows(t, service.AuditBackupCreate)
	if len(createRows) != 1 {
		t.Fatalf("创建备份应写 1 条审计，实际 %d 条", len(createRows))
	}
	if want := "应用:audit-backup-app（audit-backup-app）"; createRows[0].Username != want {
		t.Fatalf("应用令牌的操作者应显示为 %q，实际 %q", want, createRows[0].Username)
	}

	missing := auditJSONRequest(engine, http.MethodPost, "/api/v1/system/restore", adminToken, `{"filename":"missing.tgz"}`)
	if missing.Code == http.StatusOK {
		t.Fatalf("恢复不存在的备份不应成功：%s", missing.Body.String())
	}
	if rows := auditRows(t, service.AuditBackupRestore); len(rows) != 0 {
		t.Fatalf("失败的恢复不应写审计，实际 %d 条", len(rows))
	}

	filename := filepath.Base(backupPath)
	restored := auditJSONRequest(engine, http.MethodPost, "/api/v1/system/restore", adminToken, `{"filename":"`+filename+`"}`)
	if restored.Code != http.StatusOK {
		t.Fatalf("恢复备份应成功，实际 %d %s", restored.Code, restored.Body.String())
	}
	restoreRows := auditRows(t, service.AuditBackupRestore)
	if len(restoreRows) != 1 {
		t.Fatalf("恢复成功应写 1 条审计，实际 %d 条", len(restoreRows))
	}
	if restoreRows[0].Username != admin.Username || !strings.Contains(restoreRows[0].Detail, filename) {
		t.Fatalf("恢复审计应记下操作者与备份文件名，实际 %+v", restoreRows[0])
	}
}

// 改配置：只记真正变了的键名，绝不记值；没动过的键不算「改了」。
func TestSecurityAuditConfigRecordsKeysNotValues(t *testing.T) {
	testutil.SetupTestEnv(t)
	engine := newAuditTestEngine()

	admin := testutil.MustCreateUser(t, "audit-config-admin", "admin")
	token := testutil.MustCreateAccessToken(t, admin.Username, admin.Role)

	const secret = "audit-super-secret-value"
	unchangedValue := model.GetRegisteredConfig("auto_install_deps")
	rec := auditJSONRequest(engine, http.MethodPut, "/api/v1/configs/batch", token,
		`{"configs":{"captcha_key":"`+secret+`","auto_install_deps":"`+unchangedValue+`"}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("批量保存配置应成功，实际 %d %s", rec.Code, rec.Body.String())
	}

	rows := auditRows(t, service.AuditConfigUpdate)
	if len(rows) != 1 {
		t.Fatalf("改配置应写 1 条审计，实际 %d 条", len(rows))
	}
	if !strings.Contains(rows[0].Detail, "captcha_key") {
		t.Fatalf("审计应记下改过的键名，实际 detail=%q", rows[0].Detail)
	}
	if strings.Contains(rows[0].Detail, secret) {
		t.Fatalf("审计里绝不能出现配置的值，实际 detail=%q", rows[0].Detail)
	}
	if strings.Contains(rows[0].Detail, "auto_install_deps") {
		t.Fatalf("值没变的键不应记成「改了」，实际 detail=%q", rows[0].Detail)
	}

	// 原样再保存一次：什么都没变，不写审计
	again := auditJSONRequest(engine, http.MethodPut, "/api/v1/configs/batch", token,
		`{"configs":{"captcha_key":"`+secret+`"}}`)
	if again.Code != http.StatusOK {
		t.Fatalf("重复保存应成功，实际 %d %s", again.Code, again.Body.String())
	}
	if rows := auditRows(t, service.AuditConfigUpdate); len(rows) != 1 {
		t.Fatalf("没有变化的保存不应再写审计，实际共 %d 条", len(rows))
	}
}

// 写审计失败（这里直接把审计表删掉）不能影响主操作：配置照常保存、照常回 200。
func TestSecurityAuditWriteFailureDoesNotBreakOperation(t *testing.T) {
	testutil.SetupTestEnv(t)
	engine := newAuditTestEngine()

	admin := testutil.MustCreateUser(t, "audit-broken-admin", "admin")
	token := testutil.MustCreateAccessToken(t, admin.Username, admin.Role)

	if err := database.DB.Migrator().DropTable(&model.SecurityAudit{}); err != nil {
		t.Fatalf("drop security_audits: %v", err)
	}

	rec := auditJSONRequest(engine, http.MethodPost, "/api/v1/configs", token, `{"key":"captcha_key","value":"after-drop"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("审计写不进去时主操作仍应成功，实际 %d %s", rec.Code, rec.Body.String())
	}
	if got := model.GetRegisteredConfig("captcha_key"); got != "after-drop" {
		t.Fatalf("配置应已保存，实际 %q", got)
	}
}

// 系统命令行：命令起来了记一条，只留前 200 个字符。
func TestSecurityAuditRecordsConsoleRunTruncated(t *testing.T) {
	requireBash(t)
	testutil.SetupTestEnv(t)

	engine := newConsoleTestEngine(t)
	token := mustConsoleAdminToken(t, "audit-console-admin")

	command := "echo " + strings.Repeat("长", 300)
	runID := mustStartConsoleCommand(t, engine, token, command)
	// 等命令跑完再断言、再返回：Windows 上进程占着临时目录当 cwd 会让清理失败
	awaitConsoleRunDone(t, engine, token, runID)

	rows := auditRows(t, service.AuditConsoleRun)
	if len(rows) != 1 {
		t.Fatalf("执行命令应写 1 条审计，实际 %d 条", len(rows))
	}
	detail := strings.TrimSuffix(rows[0].Detail, "…")
	if utf8.RuneCountInString(detail) != auditConsoleCommandMaxRunes {
		t.Fatalf("审计里的命令应截成 %d 个字符，实际 %d 个：%q", auditConsoleCommandMaxRunes, utf8.RuneCountInString(detail), detail)
	}
	if !strings.HasPrefix(command, detail) {
		t.Fatalf("审计里应是命令的开头部分，实际 %q", detail)
	}
	if rows[0].Username != "audit-console-admin" {
		t.Fatalf("操作者应是执行命令的管理员，实际 %q", rows[0].Username)
	}
}
