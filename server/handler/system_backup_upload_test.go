package handler_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"daidai-panel/config"
	"daidai-panel/testutil"

	"github.com/gin-gonic/gin"
)

// buildTestBackupArchive 造一个 gzip+tar 包，files 的键是包内路径。
func buildTestBackupArchive(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var raw bytes.Buffer
	gzipWriter := gzip.NewWriter(&raw)
	tarWriter := tar.NewWriter(gzipWriter)
	for name, content := range files {
		header := &tar.Header{Name: name, Mode: 0o644, Size: int64(len(content)), Typeflag: tar.TypeReg}
		if err := tarWriter.WriteHeader(header); err != nil {
			t.Fatalf("write tar header: %v", err)
		}
		if _, err := tarWriter.Write([]byte(content)); err != nil {
			t.Fatalf("write tar body: %v", err)
		}
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}
	return raw.Bytes()
}

func performBackupUpload(t *testing.T, engine *gin.Engine, token, filename string, content []byte) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := part.Write(content); err != nil {
		t.Fatalf("write form file: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/system/backup/upload", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	return rec
}

const testBackupManifest = `{"format":"daidai-panel-backup","version":"3.4.2","source":"daidai-panel","selection":{"tasks":true},"data":{}}`

// 备份 scope 的应用令牌：创建 / 列表 / 下载 / 恢复进度照常可用；上传 / 恢复 / 删除一律 403，
// 这三项会覆盖或销毁数据，只允许管理员账号本人。管理员用户令牌全部可用。
func TestBackupRoutesAppTokenCannotUploadRestoreOrDelete(t *testing.T) {
	testutil.SetupTestEnv(t)
	engine := newProtectedRouter()

	appToken := testutil.MustCreateAppToken(t, "backup-app", "backup")
	admin := testutil.MustCreateUser(t, "backup-perm-admin", "admin")
	adminToken := testutil.MustCreateAccessToken(t, admin.Username, admin.Role)

	backupDir := filepath.Join(config.C.Data.Dir, "backups")
	if err := os.MkdirAll(backupDir, 0o755); err != nil {
		t.Fatalf("create backup dir: %v", err)
	}
	archive := buildTestBackupArchive(t, map[string]string{"manifest.json": testBackupManifest})
	if err := os.WriteFile(filepath.Join(backupDir, "keep.tgz"), archive, 0o644); err != nil {
		t.Fatalf("write backup: %v", err)
	}

	appAuth := map[string]string{"Authorization": "Bearer " + appToken}

	// 应用令牌被拒的三项
	if rec := performBackupUpload(t, engine, appToken, "evil.tgz", archive); rec.Code != http.StatusForbidden {
		t.Fatalf("应用令牌上传备份应当 403，实际 %d %s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(filepath.Join(backupDir, "evil.tgz")); !os.IsNotExist(err) {
		t.Fatal("被拒的上传不应落盘")
	}
	if rec := performJSONRequest(engine, http.MethodPost, "/api/v1/system/restore", `{"filename":"keep.tgz"}`, appAuth, ""); rec.Code != http.StatusForbidden {
		t.Fatalf("应用令牌恢复备份应当 403，实际 %d %s", rec.Code, rec.Body.String())
	}
	if rec := performRequest(engine, http.MethodDelete, "/api/v1/system/backup?filename=keep.tgz", appAuth); rec.Code != http.StatusForbidden {
		t.Fatalf("应用令牌删除备份应当 403，实际 %d %s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(filepath.Join(backupDir, "keep.tgz")); err != nil {
		t.Fatalf("被拒的删除不应删掉文件: %v", err)
	}
	if rec := performRequest(engine, http.MethodGet, "/api/v1/system/config-script", appAuth); rec.Code != http.StatusForbidden {
		t.Fatalf("应用令牌读取 config.sh 应当 403，实际 %d", rec.Code)
	}

	// 应用令牌保留的四项
	if rec := performRequest(engine, http.MethodGet, "/api/v1/system/backups", appAuth); rec.Code != http.StatusOK {
		t.Fatalf("应用令牌列出备份应当可用，实际 %d %s", rec.Code, rec.Body.String())
	}
	if rec := performRequest(engine, http.MethodGet, "/api/v1/system/backup/download?filename="+url.QueryEscape("keep.tgz"), appAuth); rec.Code != http.StatusOK {
		t.Fatalf("应用令牌下载备份应当可用，实际 %d %s", rec.Code, rec.Body.String())
	}
	if rec := performRequest(engine, http.MethodGet, "/api/v1/system/backup/download/keep.tgz", appAuth); rec.Code != http.StatusOK {
		t.Fatalf("应用令牌按路径下载备份应当可用，实际 %d %s", rec.Code, rec.Body.String())
	}
	if rec := performRequest(engine, http.MethodGet, "/api/v1/system/restore/progress", appAuth); rec.Code != http.StatusOK {
		t.Fatalf("应用令牌查看恢复进度应当可用，实际 %d %s", rec.Code, rec.Body.String())
	}
	if rec := performJSONRequest(engine, http.MethodPost, "/api/v1/system/backup", `{"name":"by-app"}`, appAuth, ""); rec.Code == http.StatusForbidden {
		t.Fatalf("应用令牌创建备份应当可用，实际 403 %s", rec.Body.String())
	}

	// 管理员账号全部可用
	if rec := performBackupUpload(t, engine, adminToken, "admin.tgz", archive); rec.Code != http.StatusOK {
		t.Fatalf("管理员上传备份应当成功，实际 %d %s", rec.Code, rec.Body.String())
	}
	adminAuth := map[string]string{"Authorization": "Bearer " + adminToken}
	// 恢复一个不存在的文件：只证明过了权限这一关（会因找不到文件失败，但不是 403）。
	if rec := performJSONRequest(engine, http.MethodPost, "/api/v1/system/restore", `{"filename":"missing.tgz"}`, adminAuth, ""); rec.Code == http.StatusForbidden {
		t.Fatalf("管理员恢复备份不应 403: %s", rec.Body.String())
	}
	if rec := performRequest(engine, http.MethodDelete, "/api/v1/system/backup?filename=admin.tgz", adminAuth); rec.Code != http.StatusOK {
		t.Fatalf("管理员删除备份应当成功，实际 %d %s", rec.Code, rec.Body.String())
	}
	if rec := performRequest(engine, http.MethodGet, "/api/v1/system/config-script", adminAuth); rec.Code == http.StatusForbidden {
		t.Fatalf("管理员读取 config.sh 不应 403: %s", rec.Body.String())
	}
}

func TestUploadBackupValidatesContent(t *testing.T) {
	testutil.SetupTestEnv(t)
	engine := newProtectedRouter()
	admin := testutil.MustCreateUser(t, "backup-upload-admin", "admin")
	token := testutil.MustCreateAccessToken(t, admin.Username, admin.Role)
	backupDir := filepath.Join(config.C.Data.Dir, "backups")

	validArchive := buildTestBackupArchive(t, map[string]string{"manifest.json": testBackupManifest})
	qinglongArchive := buildTestBackupArchive(t, map[string]string{
		"data/config/config.sh":   "export A=1\n",
		"data/db/database.sqlite": "x",
	})
	legacyJSON := []byte(`{"version":"1.0","tasks":[],"env_vars":[],"subscriptions":[],"system_configs":[]}`)

	accepted := []struct {
		name    string
		content []byte
	}{
		{"panel.tgz", validArchive},
		{"panel.tar.gz", validArchive},
		{"qinglong.tgz", qinglongArchive},
		{"legacy.json", legacyJSON},
		// .enc 无密码验不了内容，只验长度；新旧两种格式的最短长度都要能过。
		{"old.enc", bytes.Repeat([]byte{0x42}, 28)},
		{"new.enc", append([]byte("DDPENC02"), bytes.Repeat([]byte{0x42}, 16+12+16)...)},
	}
	for _, item := range accepted {
		rec := performBackupUpload(t, engine, token, item.name, item.content)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s 应当上传成功，实际 %d %s", item.name, rec.Code, rec.Body.String())
		}
		if _, err := os.Stat(filepath.Join(backupDir, item.name)); err != nil {
			t.Fatalf("%s 应当落盘: %v", item.name, err)
		}
	}

	rejected := []struct {
		name    string
		content []byte
	}{
		{"random.tgz", []byte("this is not a gzip archive at all")},
		{"no-manifest.tgz", buildTestBackupArchive(t, map[string]string{"readme.txt": "hi"})},
		{"bad-manifest.tgz", buildTestBackupArchive(t, map[string]string{"manifest.json": "{not json"})},
		{"empty-selection.tgz", buildTestBackupArchive(t, map[string]string{"manifest.json": `{"selection":{}}`})},
		{"escape.tgz", buildTestBackupArchive(t, map[string]string{"manifest.json": testBackupManifest, "../evil.sh": "x"})},
		{"truncated.tgz", validArchive[:len(validArchive)-6]},
		{"empty-object.json", []byte(`{}`)},
		{"not-json.json", []byte("hello")},
		{"short.enc", []byte("short")},
		{"short-new.enc", append([]byte("DDPENC02"), bytes.Repeat([]byte{0x42}, 20)...)},
	}
	for _, item := range rejected {
		rec := performBackupUpload(t, engine, token, item.name, item.content)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s 应当被拒（400），实际 %d %s", item.name, rec.Code, rec.Body.String())
		}
		if _, err := os.Stat(filepath.Join(backupDir, item.name)); !os.IsNotExist(err) {
			t.Fatalf("%s 校验失败不应落盘", item.name)
		}
	}

	// 失败的上传不能留下临时文件，列表里也不能出现半成品。
	entries, err := os.ReadDir(backupDir)
	if err != nil {
		t.Fatalf("read backup dir: %v", err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".upload-") {
			t.Fatalf("上传结束后不应残留临时文件: %s", entry.Name())
		}
	}
}

func TestUploadBackupRenamesInsteadOfOverwriting(t *testing.T) {
	testutil.SetupTestEnv(t)
	engine := newProtectedRouter()
	admin := testutil.MustCreateUser(t, "backup-rename-admin", "admin")
	token := testutil.MustCreateAccessToken(t, admin.Username, admin.Role)
	backupDir := filepath.Join(config.C.Data.Dir, "backups")

	first := buildTestBackupArchive(t, map[string]string{"manifest.json": testBackupManifest})
	second := buildTestBackupArchive(t, map[string]string{"manifest.json": testBackupManifest, "files/scripts/a.py": "print(1)"})

	cases := []struct {
		upload  string
		content []byte
		want    string
	}{
		{"daily.tgz", first, "daily.tgz"},
		{"daily.tgz", second, "daily-1.tgz"},
		{"daily.tgz", second, "daily-2.tgz"},
		{"night.tar.gz", first, "night.tar.gz"},
		{"night.tar.gz", second, "night-1.tar.gz"},
	}
	for _, item := range cases {
		rec := performBackupUpload(t, engine, token, item.upload, item.content)
		if rec.Code != http.StatusOK {
			t.Fatalf("上传 %s 应当成功，实际 %d %s", item.upload, rec.Code, rec.Body.String())
		}
		data, _ := decodeJSONMap(t, rec)["data"].(map[string]interface{})
		if got, _ := data["filename"].(string); got != item.want {
			t.Fatalf("上传 %s 应当落成 %s，实际响应 filename=%q", item.upload, item.want, got)
		}
		saved, err := os.ReadFile(filepath.Join(backupDir, item.want))
		if err != nil || !bytes.Equal(saved, item.content) {
			t.Fatalf("%s 的内容应当是本次上传的文件: %v", item.want, err)
		}
	}

	// 第一次上传的 daily.tgz 没有被后来的同名上传覆盖。
	if saved, _ := os.ReadFile(filepath.Join(backupDir, "daily.tgz")); !bytes.Equal(saved, first) {
		t.Fatal("同名上传不应覆盖已有备份")
	}
}
