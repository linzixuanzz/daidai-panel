package handler

import (
	"net/http"
	"testing"

	"daidai-panel/database"
	"daidai-panel/model"
	"daidai-panel/testutil"
)

// 以 - 开头的依赖名会被 pip / npm 当成选项（--registry、--index-url 换安装源），创建时直接跳过：
// 不建记录、不安装；同一批里的正常包名照常提交。
func TestDependencyCreateSkipsOptionLikeNames(t *testing.T) {
	testutil.SetupTestEnv(t)

	originalRunner := dependencyInstallRunner
	defer func() {
		dependencyInstallRunner = originalRunner
	}()
	// 不真的安装：只关心哪些名字被登记成依赖记录。
	dependencyInstallRunner = func(id uint, depType, name string) {}

	engine := newDepsTestRouter()
	token := testutil.MustCreateAccessToken(t, "admin", "admin")
	rec := performDepsJSONRequest(engine, http.MethodPost, "/api/v1/deps", map[string]any{
		"type":  model.DepTypeNodeJS,
		"names": []string{"--registry=https://evil.example.com", "-g", "axios"},
	}, map[string]string{
		"Authorization": "Bearer " + token,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}

	var stored []model.Dependency
	if err := database.DB.Where("type = ?", model.DepTypeNodeJS).Find(&stored).Error; err != nil {
		t.Fatalf("reload dependencies: %v", err)
	}
	if len(stored) != 1 || stored[0].Name != "axios" {
		t.Fatalf("expected only axios to be recorded, got %+v", stored)
	}
}
