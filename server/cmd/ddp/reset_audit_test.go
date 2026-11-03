package main

import (
	"strings"
	"testing"

	"daidai-panel/config"
	"daidai-panel/database"
	"daidai-panel/model"
	"daidai-panel/service"
	"daidai-panel/testutil"
)

// ddp reset-password 成功后要留一条安全审计：IP 记 cli，详情里写明重置的是谁，网页审计日志里查得到。
func TestCLIResetPasswordWritesSecurityAudit(t *testing.T) {
	testutil.SetupTestEnv(t)
	user := testutil.MustCreateLoginUser(t, "cli_reset_audit", "admin", "Password123!")

	rt := &cliRuntime{cfg: config.C}
	if err := runResetPassword(rt, []string{user.Username, "NewPassword456!"}); err != nil {
		t.Fatalf("ddp reset-password: %v", err)
	}

	var rows []model.SecurityAudit
	database.DB.Where("action = ?", service.AuditUserPasswordReset).Find(&rows)
	if len(rows) != 1 {
		t.Fatalf("命令行重置密码应写 1 条审计，实际 %d 条", len(rows))
	}
	if rows[0].IP != "cli" || !strings.Contains(rows[0].Detail, user.Username) {
		t.Fatalf("审计应记 IP=cli 且写明被重置的用户，实际 %+v", rows[0])
	}
	if strings.Contains(rows[0].Detail, "NewPassword456!") {
		t.Fatalf("审计里不能出现密码，实际 detail=%q", rows[0].Detail)
	}
}
