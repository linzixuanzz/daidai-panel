package handler

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"daidai-panel/testutil"
)

func TestDownloadBinaryUpdateAssetVerifiesSHA256(t *testing.T) {
	testutil.SetupTestEnv(t)

	payload := []byte("fake panel update package")
	sum := sha256.Sum256(payload)
	goodDigest := "sha256:" + hex.EncodeToString(sum[:])

	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_, _ = w.Write(payload)
	}))
	defer server.Close()

	dir := t.TempDir()

	// digest 匹配：下载成功，文件内容就是服务端给的。大写十六进制也要认。
	okPath := filepath.Join(dir, "ok.tar.gz")
	if err := downloadBinaryUpdateAsset(server.URL+"/ok", okPath, strings.ToUpper(goodDigest)); err != nil {
		t.Fatalf("digest 匹配时应当下载成功: %v", err)
	}
	if saved, err := os.ReadFile(okPath); err != nil || string(saved) != string(payload) {
		t.Fatalf("下载内容不对: %v", err)
	}

	// digest 不匹配：报错并删掉已下载的文件。
	otherSum := sha256.Sum256([]byte("something else"))
	badPath := filepath.Join(dir, "bad.tar.gz")
	err := downloadBinaryUpdateAsset(server.URL+"/bad", badPath, "sha256:"+hex.EncodeToString(otherSum[:]))
	if err == nil || !strings.Contains(err.Error(), "校验失败") {
		t.Fatalf("digest 不匹配应当报校验失败，实际 %v", err)
	}
	if _, statErr := os.Stat(badPath); !os.IsNotExist(statErr) {
		t.Fatal("校验失败的更新包应当被删除")
	}

	// digest 缺失或格式不对：直接中止，连下载都不发起。
	before := hits.Load()
	for _, digest := range []string{"", "   ", "md5:abcdef", "sha256:not-hex", "sha256:" + strings.Repeat("a", 10)} {
		missingPath := filepath.Join(dir, "missing.tar.gz")
		err := downloadBinaryUpdateAsset(server.URL+"/missing", missingPath, digest)
		if err == nil || !strings.Contains(err.Error(), "中止更新") {
			t.Fatalf("digest=%q 应当中止更新，实际 %v", digest, err)
		}
		if _, statErr := os.Stat(missingPath); !os.IsNotExist(statErr) {
			t.Fatalf("digest=%q 时不应留下更新包文件", digest)
		}
	}
	if hits.Load() != before {
		t.Fatal("没有合法校验值时不应发起下载")
	}
}

// GitHub Release API 的 assets[].digest 要能解析进 panelReleaseAsset，并一路带进更新计划。
func TestPanelReleaseAssetDecodesDigestIntoPlan(t *testing.T) {
	testutil.SetupTestEnv(t)

	assetName, _, err := resolveBinaryReleaseTarget(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		t.Skipf("平台 %s/%s 没有二进制更新包: %v", runtime.GOOS, runtime.GOARCH, err)
	}
	digest := "sha256:" + strings.Repeat("ab", 32)
	raw := `{"tag_name":"v9.9.9","assets":[{"name":"` + assetName + `","browser_download_url":"https://example.com/a","size":3,"digest":"` + digest + `"}]}`
	var release panelReleaseInfo
	if err := json.Unmarshal([]byte(raw), &release); err != nil {
		t.Fatalf("decode release: %v", err)
	}
	asset, ok := release.findAsset(assetName)
	if !ok || asset.Digest != digest {
		t.Fatalf("应当解析出 digest，实际 %#v", asset)
	}

	plan, err := buildBinaryPanelUpdatePlan(&release)
	if err != nil {
		t.Fatalf("build plan: %v", err)
	}
	if plan.AssetDigest != digest {
		t.Fatalf("更新计划应当带上附件的 digest，实际 %q", plan.AssetDigest)
	}
}
