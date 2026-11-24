package main

import (
	"bytes"
	"compress/gzip"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"daidai-panel/middleware"

	"github.com/gin-gonic/gin"
)

var staticTestModTime = time.Date(2026, 9, 1, 8, 0, 0, 0, time.UTC)

var (
	staticTestIndexHTML = "<!doctype html><html><head><title>呆呆面板</title></head><body><div id=\"app\"></div></body></html>"
	staticTestHashedJS  = strings.Repeat("export const panel = 'dumb panel';\n", 64)
	staticTestCSS       = strings.Repeat(".dd-optimization{contain:content}\n", 64)
	staticTestGzip      = map[string]string{"Accept-Encoding": "gzip, deflate, br"}
)

const staticTestHashedJSPath = "/assets/index-CZqAZ7Ga.js"

// staticTestRealBuildAssets 是 2026-09-12 发布版构建（web/dist/assets）里全部带哈希的文件名，
// 原样抄录（只少了不带哈希的 optimization.css），用来钉住「真实产物形态」而不是凭想象写的样例。
const staticTestRealBuildAssets = `
ansi--b05i_G0.js apiData-ClqEAsP1.js app-core-C2Luajsp.js badges-CjtivnLl.js base-BhyKBIh7.css
clipboard-C-KXg_3D.js CodeDiffEditor-CyVbkBni.js CodeEditor-DLX7ibA4.css
CodeEditor.vue_vue_type_script_setup_true_lang-DG0XGR0R.js codemirror-D-TbyGDy.js
CodeMirrorDiffEditor-DtLp69vl.js CodeMirrorDiffEditor-Kq63wMbx.css codicon-Brq4_Ui5.ttf datetime-Cih0HjT6.js
DdBadge-DhQKPzgJ.css DdBadge-DTCDnWaR.js DdDateRangePicker-CshKmNif.js DdDateRangePicker-DUIOkMzy.css
DdSplitButton-BrCpCwka.css DdSplitButton-mVgQIDAK.js deps-Df2BH_W3.js directive-Bv8FYb79.js
duration-D9jqJjNI.js echarts-CTuoCbUZ.js editor.worker-CRaYpIdo.js el-alert-B5Y5BJm_.js el-alert-B6UgayC7.css
el-button-5dKIjbuV.js el-button-ZSOkJyjJ.css el-card-BAyktVMS.css el-card-CL0nwnDT.js el-col-Be80ifQd.js
el-col-BWEC13sA.css el-dialog-BYDi3v72.js el-dialog-DUbGnqO_.css el-divider-B0pQhKGi.css el-divider-ClUzAwSS.js
el-drawer-4RO73nWn.css el-drawer-C_PuX8Qu.js el-dropdown-item-BeIfAqTO.css el-dropdown-item-T0W5loBz.js
el-empty-DnKO2Sm9.css el-empty-DyyUbjOh.js el-form-item-2CbgUHna.js el-form-item-BUYvSc9V.css
el-input-number-CpIHIifW.js el-input-number-DExoAqKP.css el-input-xaztPnzw.css el-overlay-M-mVclUo.js
el-overlay-Wv38huFO.css el-pagination-BgshM9p7.js el-pagination-CDeFZDGO.css el-popper-BwaH4aZZ.js
el-popper-C8-vPSoj.css el-radio-button-BDEDkXh7.css el-radio-group-hrelhGmJ.css el-radio-group-xB-Nl5W1.js
el-select-BJdO_3i8.js el-select-CK58rX4v.css el-switch-boUyY8S0.js el-switch-DpR9-s4E.css
el-tab-pane-C5lGmld3.css el-tab-pane-DLQKWwsa.js el-tag-BEfECeMa.css el-tag-CTTfw4nx.js el-tooltip-tn0RQdqM.css
el-upload-BhjUZTm8.js el-upload-C6r6ku1d.css error-C2xGiLmp.js ExecutionTrendChart-CunqJMw-.js
ExecutionTrendChart-pGhfi1oq.css favicon-512-c1tktHzg.js index-arXbE6Zs.js index-B-OIG4Ov.js index-B2vHP4ye.js
index-B9ziVlLL.css index-BBbwbcaZ.css index-BF3jyCxC.css index-BfdoxXMi.js index-BjV66Jpm.js index-BM2eyY0q.js
index-BoPfYoRz.css index-BoQHMH69.css index-BT5bCCSs.js index-BTmGU8gp.css index-BubgINuB.css index-ByqV0vy9.js
index-C_qGmJV_.js index-C-5Ug2Fq.js index-CaWmZzXE.js index-CFYK8YE6.js index-CjhA0S5N.css index-CJztiYYR.js
index-CLetXH3q.js index-CVmp7pGy.css index-CW65f5qm.js index-CZqAZ7Ga.js index-D9R-9dsG.js index-DAlTogkR.css
index-DGcffx3Z.js index-DIfEs4QI.js index-DNG45GKt.css index-Dvr64gv6.js index-Dw6olLBS.css index-G0u4raJP.css
index-gNvC34Ql.css index-hKa5v8oM.css index-NFU1_tFO.css index-uOQUzyMa.css index-zPg5o3mZ.js
json.worker-C5PEkLU0.js MainLayout-Bgql4V8V.js MainLayout-BqmXe5zU.css monaco-BMhh22P7.js monaco-CKol-9S0.css
MonacoDiffEditor-CDTZ3wP3.js MonacoDiffEditor-DYlmejs3.css MonacoEditor-AvoM8L1W.js MonacoEditor-BkKDIeTq.css
monacoEngine-DCh9_tQH.js notification-Ds5Xy6MI.js qrcode-CHZCNKhv.js qrcode-CVuUpM6R.js
rawLogDownload-D5oZNNDg.js refs-D6FW0B1r.js roles-okKJRo9c.js security-BQXBZ3Lc.js sortablejs-DWxD2osx.js
sse-CUePpMiD.js strings-DkCrGWt8.js system-CYF0I-qz.js task-croRV-Co.js theme-yqtPIegQ.js toast-Db6kXPO2.js
usePageActivity-BqLSnaHv.js useResponsive-BDUIy-7k.js vendor-CknvNf05.js zrender--J4QzM5z.js
`

// nginxHashedAssetLookaheads 是 looksLikeContentHash 在 PCRE 里的等价写法（RE2 不支持 (?=…)，
// Go 侧只能写成函数）：第一个断言「8 位里至少一个字母」，第二个断言「至少一个数字 / _ / -，
// 或者首字符以外有大写字母」。
const nginxHashedAssetLookaheads = `(?=[A-Za-z0-9_-]{0,7}[A-Za-z])(?=[A-Za-z0-9_-]{0,7}[0-9_-]|[A-Za-z0-9_-]{1,7}[A-Z])`

func writeStaticTestFile(t *testing.T, root, rel, content string, modTime time.Time) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", rel, err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
	if err := os.Chtimes(full, modTime, modTime); err != nil {
		t.Fatalf("chtimes %s: %v", rel, err)
	}
}

type staticTestEnv struct {
	engine *gin.Engine
	sf     *staticFrontend
	root   string
}

func newStaticTestEnv(t *testing.T) *staticTestEnv {
	t.Helper()
	gin.SetMode(gin.TestMode)
	root := t.TempDir()
	for rel, content := range map[string]string{
		"index.html":                       staticTestIndexHTML,
		"favicon-512.webp":                 "RIFF\x24\x00\x00\x00WEBPVP8 favicon",
		"assets/index-CZqAZ7Ga.js":         staticTestHashedJS,
		"assets/ansi--b05i_G0.js":          staticTestHashedJS,
		"assets/monaco-CKol-9S0.css":       staticTestCSS,
		"assets/codicon-Brq4_Ui5.ttf":      strings.Repeat("\x00\x01\x00\x00codicon", 256),
		"assets/tiny-C_qGmJV_.js":          "export{};\n",
		"assets/favicon-512-c1tktHzg.webp": "RIFF\x24\x00\x00\x00WEBPVP8 hashed",
		"assets/optimization.css":          staticTestCSS,
		"fonts/fonts.css":                  staticTestCSS,
		"fonts/inter-latin-400.woff2":      "wOF2 inter",
		"secret.txt":                       "top secret",
	} {
		writeStaticTestFile(t, root, rel, content, staticTestModTime)
	}

	engine := gin.New()
	engine.Use(middleware.SecurityHeaders())
	// 模拟 CORS 中间件写的 Vary: Origin，用来断言 gzip 分支追加 Vary 而不是覆盖。
	engine.Use(func(c *gin.Context) {
		c.Header("Vary", "Origin")
		c.Next()
	})
	// 两条假的「流式 / 下载」API：证明压缩不是全局中间件，API 一律不经过它。
	engine.GET("/api/v1/tasks/:id/stream", func(c *gin.Context) {
		c.Header("Content-Type", "text/event-stream")
		c.Header("Cache-Control", "no-cache")
		c.String(http.StatusOK, "data: "+strings.Repeat("x", 4096)+"\n\n")
	})
	engine.GET("/api/v1/logs/:id/download", func(c *gin.Context) {
		c.Header("Content-Disposition", `attachment; filename="task.log"`)
		c.Data(http.StatusOK, "text/plain; charset=utf-8", []byte(strings.Repeat("log line\n", 1024)))
	})

	sf := setupStaticFrontend(engine, root)
	if sf == nil {
		t.Fatal("expected static frontend to be mounted")
	}
	return &staticTestEnv{engine: engine, sf: sf, root: root}
}

func serveStatic(engine *gin.Engine, method, target string, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	return rec
}

func gunzipString(t *testing.T, body []byte) string {
	t.Helper()
	zr, err := gzip.NewReader(bytes.NewReader(body))
	if err != nil {
		t.Fatalf("response is not valid gzip: %v", err)
	}
	defer zr.Close()
	raw, err := io.ReadAll(zr)
	if err != nil {
		t.Fatalf("gunzip failed: %v", err)
	}
	return string(raw)
}

func headerHasToken(h http.Header, key, token string) bool {
	for _, v := range h.Values(key) {
		for _, part := range strings.Split(v, ",") {
			if strings.EqualFold(strings.TrimSpace(part), token) {
				return true
			}
		}
	}
	return false
}

func TestSetupStaticFrontendSkipsWithoutIndex(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	if sf := setupStaticFrontend(engine, t.TempDir()); sf != nil {
		t.Fatal("web dir without index.html must not be mounted")
	}
	if rec := serveStatic(engine, http.MethodGet, "/", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("expected gin default 404 when nothing is mounted, got %d", rec.Code)
	}
}

func TestStaticFrontendIndexAndDeepLinksAreNoCache(t *testing.T) {
	env := newStaticTestEnv(t)
	lastMod := staticTestModTime.Format(http.TimeFormat)
	for _, target := range []string{"/", "/scripts", "/tasks", "/config-file", "/admin/settings", "/index.html"} {
		rec := serveStatic(env.engine, http.MethodGet, target, staticTestGzip)
		h := rec.Header()
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status=%d, want 200", target, rec.Code)
		}
		if ct := h.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
			t.Fatalf("%s: content-type=%q, want text/html", target, ct)
		}
		if cc := h.Get("Cache-Control"); cc != staticCacheNoCache {
			t.Fatalf("%s: cache-control=%q, want no-cache", target, cc)
		}
		if lm := h.Get("Last-Modified"); lm != lastMod {
			t.Fatalf("%s: last-modified=%q, want %q", target, lm, lastMod)
		}
		if ce := h.Get("Content-Encoding"); ce != "" {
			t.Fatalf("%s: index.html must not be gzipped, got content-encoding=%q", target, ce)
		}
		if rec.Body.String() != staticTestIndexHTML {
			t.Fatalf("%s: unexpected body %q", target, rec.Body.String())
		}
	}

	rec := serveStatic(env.engine, http.MethodHead, "/", nil)
	if rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != staticCacheNoCache || rec.Body.Len() != 0 {
		t.Fatalf("HEAD /: status=%d cache-control=%q body=%d bytes", rec.Code, rec.Header().Get("Cache-Control"), rec.Body.Len())
	}
}

func TestStaticFrontendCacheControlByAssetKind(t *testing.T) {
	env := newStaticTestEnv(t)
	cases := []struct {
		target       string
		cacheControl string
		contentType  string
	}{
		{"/assets/index-CZqAZ7Ga.js", staticCacheImmutable, "text/javascript; charset=utf-8"},
		{"/assets/ansi--b05i_G0.js", staticCacheImmutable, "text/javascript; charset=utf-8"},
		{"/assets/monaco-CKol-9S0.css", staticCacheImmutable, "text/css; charset=utf-8"},
		{"/assets/codicon-Brq4_Ui5.ttf", staticCacheImmutable, "font/ttf"},
		{"/assets/favicon-512-c1tktHzg.webp", staticCacheImmutable, "image/webp"},
		// 不带哈希：web/public/assets/ 原样拷来，升级后同名换内容，绝不能 immutable
		{"/assets/optimization.css", staticCacheNoCache, "text/css; charset=utf-8"},
		{"/fonts/fonts.css", staticCacheNoCache, "text/css; charset=utf-8"},
		{"/fonts/inter-latin-400.woff2", staticCacheNoCache, "font/woff2"},
		{"/favicon-512.webp", staticCacheNoCache, "image/webp"},
	}
	for _, tc := range cases {
		rec := serveStatic(env.engine, http.MethodGet, tc.target, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status=%d, want 200", tc.target, rec.Code)
		}
		if cc := rec.Header().Get("Cache-Control"); cc != tc.cacheControl {
			t.Fatalf("%s: cache-control=%q, want %q", tc.target, cc, tc.cacheControl)
		}
		if ct := rec.Header().Get("Content-Type"); ct != tc.contentType {
			t.Fatalf("%s: content-type=%q, want %q", tc.target, ct, tc.contentType)
		}
		want, err := os.ReadFile(filepath.Join(env.root, filepath.FromSlash(strings.TrimPrefix(tc.target, "/"))))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(rec.Body.Bytes(), want) {
			t.Fatalf("%s: body mismatch", tc.target)
		}
	}
}

// PWA（v4.0）的根路径文件：清单要拿到 application/manifest+json、sw.js 要拿到 JavaScript 类型，
// 全部 no-cache、不压缩。回成 index.html 的话，清单解析失败、Service Worker 因 MIME 不对注册失败。
func TestStaticFrontendServesPWARootFiles(t *testing.T) {
	env := newStaticTestEnv(t)
	// 路由按白名单注册、不看启动时文件在不在，所以环境建好之后再写文件也能命中
	files := map[string]string{
		"manifest.webmanifest": `{"name":"呆呆面板","start_url":"/","display":"standalone"}` + strings.Repeat(" ", 2048),
		"sw.js":                "self.addEventListener('notificationclick', () => {});\n" + strings.Repeat("// pad\n", 400),
		"pwa-192.png":          "\x89PNG\r\n\x1a\n192",
		"pwa-512.png":          "\x89PNG\r\n\x1a\n512",
		"pwa-maskable-512.png": "\x89PNG\r\n\x1a\nmaskable",
	}
	for rel, content := range files {
		writeStaticTestFile(t, env.root, rel, content, staticTestModTime)
	}

	cases := []struct {
		target      string
		contentType string
	}{
		{"/manifest.webmanifest", "application/manifest+json"},
		{"/sw.js", "text/javascript; charset=utf-8"},
		{"/pwa-192.png", "image/png"},
		{"/pwa-512.png", "image/png"},
		{"/pwa-maskable-512.png", "image/png"},
	}
	for _, tc := range cases {
		// 带 gzip 请求头：根路径文件也不该被压缩（压缩只给 /assets/）
		rec := serveStatic(env.engine, http.MethodGet, tc.target, staticTestGzip)
		h := rec.Header()
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status=%d, want 200", tc.target, rec.Code)
		}
		if ct := h.Get("Content-Type"); ct != tc.contentType {
			t.Fatalf("%s: content-type=%q, want %q", tc.target, ct, tc.contentType)
		}
		if cc := h.Get("Cache-Control"); cc != staticCacheNoCache {
			t.Fatalf("%s: cache-control=%q, want no-cache", tc.target, cc)
		}
		if ce := h.Get("Content-Encoding"); ce != "" {
			t.Fatalf("%s: root files must not be gzipped, got %q", tc.target, ce)
		}
		if rec.Body.String() != files[strings.TrimPrefix(tc.target, "/")] {
			t.Fatalf("%s: body mismatch (fell back to index.html?)", tc.target)
		}
	}

	if rec := serveStatic(env.engine, http.MethodHead, "/sw.js", nil); rec.Code != http.StatusOK || rec.Body.Len() != 0 ||
		rec.Header().Get("Cache-Control") != staticCacheNoCache {
		t.Fatalf("HEAD /sw.js: status=%d cache-control=%q body=%d bytes", rec.Code, rec.Header().Get("Cache-Control"), rec.Body.Len())
	}
	// 条件请求：sw.js 没变时回 304，浏览器的 SW 更新检查靠的就是这个
	rec := serveStatic(env.engine, http.MethodGet, "/sw.js", map[string]string{"If-Modified-Since": staticTestModTime.Format(http.TimeFormat)})
	if rec.Code != http.StatusNotModified {
		t.Fatalf("GET /sw.js with If-Modified-Since: status=%d, want 304", rec.Code)
	}
}

// 前端产物里还没有 PWA 文件时（例如旧前端目录），这几个路径回纯文本 404，绝不回退成 index.html。
func TestStaticFrontendMissingPWARootFilesAre404(t *testing.T) {
	env := newStaticTestEnv(t)
	for _, target := range []string{"/manifest.webmanifest", "/sw.js", "/pwa-192.png", "/pwa-512.png", "/pwa-maskable-512.png"} {
		rec := serveStatic(env.engine, http.MethodGet, target, nil)
		h := rec.Header()
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s: status=%d, want 404", target, rec.Code)
		}
		if ct := h.Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
			t.Fatalf("%s: content-type=%q, want text/plain (never text/html)", target, ct)
		}
		if cc := h.Get("Cache-Control"); cc != staticCacheNoStore {
			t.Fatalf("%s: cache-control=%q, want no-store", target, cc)
		}
	}
	// 不在白名单里的 .webmanifest 也按静态资源 404，而不是回 index.html
	if rec := serveStatic(env.engine, http.MethodGet, "/other.webmanifest", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("/other.webmanifest: status=%d, want 404", rec.Code)
	}
}

func TestStaticFrontendMissingStaticFilesAre404NotHTML(t *testing.T) {
	env := newStaticTestEnv(t)
	targets := []string{
		"/assets/not-exist.js",
		"/assets/index-DUE4mkXD.js", // #126 截图里旧前端的入口：hash 形态，但文件已被升级删掉
		"/assets/",
		"/monaco/vs/loader.js", // v3.1.1 及以前的 AMD Monaco
		"/fonts/missing.woff2",
		"/sponsor-portal/app.js", // 目录不存在：走 NoRoute 的前缀规则
		"/favicon.ico",
		"/static/js/app.js", // 不在白名单目录，但扩展名是静态资源
		"/assets/../secret.txt",
		"/assets/..%5csecret.txt",
	}
	for _, target := range targets {
		for _, headers := range []map[string]string{nil, staticTestGzip} {
			rec := serveStatic(env.engine, http.MethodGet, target, headers)
			h := rec.Header()
			if rec.Code != http.StatusNotFound {
				t.Fatalf("%s: status=%d, want 404", target, rec.Code)
			}
			if ct := h.Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
				t.Fatalf("%s: content-type=%q, want text/plain (never text/html)", target, ct)
			}
			if cc := h.Get("Cache-Control"); cc != staticCacheNoStore {
				t.Fatalf("%s: cache-control=%q, want no-store", target, cc)
			}
			if ce := h.Get("Content-Encoding"); ce != "" {
				t.Fatalf("%s: 404 must not be gzipped, got %q", target, ce)
			}
			if h.Get("X-Content-Type-Options") != "nosniff" {
				t.Fatalf("%s: expected nosniff from SecurityHeaders", target)
			}
			body := rec.Body.String()
			if strings.Contains(strings.ToLower(body), "<!doctype") || strings.Contains(body, "top secret") {
				t.Fatalf("%s: unexpected body %q", target, body)
			}
		}
	}
}

func TestStaticFrontendUnknownAPIRoutesStayJSON404(t *testing.T) {
	env := newStaticTestEnv(t)
	for _, target := range []string{"/api/xxx", "/api/v1/not-exist.js", "/api/v1/scripts/app.css"} {
		rec := serveStatic(env.engine, http.MethodGet, target, staticTestGzip)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s: status=%d, want 404", target, rec.Code)
		}
		if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
			t.Fatalf("%s: content-type=%q, want application/json", target, ct)
		}
		if body := rec.Body.String(); !strings.Contains(body, `"error":"route not found"`) {
			t.Fatalf("%s: unexpected body %q", target, body)
		}
		if ce := rec.Header().Get("Content-Encoding"); ce != "" {
			t.Fatalf("%s: API 404 must not be gzipped, got %q", target, ce)
		}
	}
}

func TestStaticFrontendGzipNegotiation(t *testing.T) {
	env := newStaticTestEnv(t)
	for _, ae := range []string{"gzip", "gzip, deflate, br", "br;q=1.0, gzip;q=0.8", "GZIP", "x-gzip", "*"} {
		rec := serveStatic(env.engine, http.MethodGet, staticTestHashedJSPath, map[string]string{"Accept-Encoding": ae})
		h := rec.Header()
		if rec.Code != http.StatusOK || h.Get("Content-Encoding") != "gzip" {
			t.Fatalf("Accept-Encoding %q: status=%d content-encoding=%q, want 200 gzip", ae, rec.Code, h.Get("Content-Encoding"))
		}
		if ct := h.Get("Content-Type"); ct != "text/javascript; charset=utf-8" {
			t.Fatalf("Accept-Encoding %q: content-type=%q, want the original type", ae, ct)
		}
		if cc := h.Get("Cache-Control"); cc != staticCacheImmutable {
			t.Fatalf("Accept-Encoding %q: cache-control=%q", ae, cc)
		}
		if !headerHasToken(h, "Vary", "Accept-Encoding") || !headerHasToken(h, "Vary", "Origin") {
			t.Fatalf("Accept-Encoding %q: vary=%q, want both Origin and Accept-Encoding", ae, h.Values("Vary"))
		}
		if cl := h.Get("Content-Length"); cl != strconv.Itoa(rec.Body.Len()) {
			t.Fatalf("Accept-Encoding %q: content-length=%q, body=%d", ae, cl, rec.Body.Len())
		}
		if ar := h.Get("Accept-Ranges"); ar != "" {
			t.Fatalf("Accept-Encoding %q: gzip response must not advertise ranges, got %q", ae, ar)
		}
		if got := gunzipString(t, rec.Body.Bytes()); got != staticTestHashedJS {
			t.Fatalf("Accept-Encoding %q: gunzipped body mismatch", ae)
		}
	}

	for _, ae := range []string{"", "br", "identity", "gzip;q=0", "gzip;q=0.000", "*;q=0", "gzip;q=0, *", "gzip;q=abc"} {
		var headers map[string]string
		if ae != "" {
			headers = map[string]string{"Accept-Encoding": ae}
		}
		rec := serveStatic(env.engine, http.MethodGet, staticTestHashedJSPath, headers)
		h := rec.Header()
		if rec.Code != http.StatusOK || h.Get("Content-Encoding") != "" {
			t.Fatalf("Accept-Encoding %q: status=%d content-encoding=%q, want identity", ae, rec.Code, h.Get("Content-Encoding"))
		}
		if rec.Body.String() != staticTestHashedJS {
			t.Fatalf("Accept-Encoding %q: identity body mismatch", ae)
		}
		if !headerHasToken(h, "Vary", "Accept-Encoding") {
			t.Fatalf("Accept-Encoding %q: identity response of a negotiable asset must still carry Vary", ae)
		}
	}

	rec := serveStatic(env.engine, http.MethodHead, staticTestHashedJSPath, staticTestGzip)
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Encoding") != "gzip" || rec.Body.Len() != 0 {
		t.Fatalf("HEAD gzip: status=%d content-encoding=%q body=%d", rec.Code, rec.Header().Get("Content-Encoding"), rec.Body.Len())
	}
	if cl, _ := strconv.Atoi(rec.Header().Get("Content-Length")); cl <= 0 {
		t.Fatalf("HEAD gzip: expected the gzip content-length, got %q", rec.Header().Get("Content-Length"))
	}
}

func TestStaticFrontendGzipScope(t *testing.T) {
	env := newStaticTestEnv(t)
	cases := []struct {
		target   string
		wantVary bool
	}{
		{"/assets/tiny-C_qGmJV_.js", true},           // 小于 1 KiB 不压，但同一类型仍带 Vary
		{"/assets/favicon-512-c1tktHzg.webp", false}, // 已压缩格式
		{"/fonts/fonts.css", false},                  // 只有 /assets 压缩
		{"/", false},
	}
	for _, tc := range cases {
		rec := serveStatic(env.engine, http.MethodGet, tc.target, staticTestGzip)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status=%d", tc.target, rec.Code)
		}
		if ce := rec.Header().Get("Content-Encoding"); ce != "" {
			t.Fatalf("%s: must not be gzipped, got %q", tc.target, ce)
		}
		if got := headerHasToken(rec.Header(), "Vary", "Accept-Encoding"); got != tc.wantVary {
			t.Fatalf("%s: vary accept-encoding=%v, want %v", tc.target, got, tc.wantVary)
		}
	}

	for _, target := range []string{"/api/v1/tasks/1/stream", "/api/v1/logs/1/download"} {
		rec := serveStatic(env.engine, http.MethodGet, target, staticTestGzip)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status=%d", target, rec.Code)
		}
		if ce := rec.Header().Get("Content-Encoding"); ce != "" {
			t.Fatalf("%s: API responses must never be compressed by the static layer, got %q", target, ce)
		}
		if headerHasToken(rec.Header(), "Vary", "Accept-Encoding") {
			t.Fatalf("%s: API responses must not get the static Vary", target)
		}
	}
	stream := serveStatic(env.engine, http.MethodGet, "/api/v1/tasks/1/stream", staticTestGzip)
	if ct := stream.Header().Get("Content-Type"); ct != "text/event-stream" || !strings.HasPrefix(stream.Body.String(), "data: ") {
		t.Fatalf("SSE route must pass through untouched: content-type=%q", ct)
	}
}

func TestStaticFrontendConditionalRequests(t *testing.T) {
	env := newStaticTestEnv(t)
	lastMod := staticTestModTime.Format(http.TimeFormat)
	cases := []struct {
		name     string
		target   string
		headers  map[string]string
		wantCC   string
		wantVary bool
	}{
		{"gzip", staticTestHashedJSPath, map[string]string{"Accept-Encoding": "gzip", "If-Modified-Since": lastMod}, staticCacheImmutable, true},
		{"identity", staticTestHashedJSPath, map[string]string{"If-Modified-Since": lastMod}, staticCacheImmutable, true},
		{"non-hashed asset", "/assets/optimization.css", map[string]string{"If-Modified-Since": lastMod}, staticCacheNoCache, true},
		{"font", "/fonts/fonts.css", map[string]string{"If-Modified-Since": lastMod}, staticCacheNoCache, false},
		{"index", "/", map[string]string{"If-Modified-Since": lastMod}, staticCacheNoCache, false},
		{"deep link", "/scripts", map[string]string{"If-Modified-Since": lastMod}, staticCacheNoCache, false},
		{"gzip if-none-match *", staticTestHashedJSPath, map[string]string{"Accept-Encoding": "gzip", "If-None-Match": "*"}, staticCacheImmutable, true},
	}
	for _, tc := range cases {
		rec := serveStatic(env.engine, http.MethodGet, tc.target, tc.headers)
		h := rec.Header()
		if rec.Code != http.StatusNotModified {
			t.Fatalf("%s: status=%d, want 304", tc.name, rec.Code)
		}
		if rec.Body.Len() != 0 {
			t.Fatalf("%s: 304 must not carry a body", tc.name)
		}
		if ce := h.Get("Content-Encoding"); ce != "" {
			t.Fatalf("%s: 304 must not carry content-encoding, got %q", tc.name, ce)
		}
		if cc := h.Get("Cache-Control"); cc != tc.wantCC {
			t.Fatalf("%s: cache-control=%q, want %q", tc.name, cc, tc.wantCC)
		}
		if got := headerHasToken(h, "Vary", "Accept-Encoding"); got != tc.wantVary {
			t.Fatalf("%s: vary accept-encoding=%v, want %v", tc.name, got, tc.wantVary)
		}
	}

	older := staticTestModTime.Add(-time.Hour).Format(http.TimeFormat)
	for name, headers := range map[string]map[string]string{
		"file changed after If-Modified-Since": {"Accept-Encoding": "gzip", "If-Modified-Since": older},
		"If-None-Match overrides IMS":          {"Accept-Encoding": "gzip", "If-Modified-Since": lastMod, "If-None-Match": `"abc"`},
	} {
		rec := serveStatic(env.engine, http.MethodGet, staticTestHashedJSPath, headers)
		if rec.Code != http.StatusOK || rec.Header().Get("Content-Encoding") != "gzip" {
			t.Fatalf("%s: status=%d content-encoding=%q, want 200 gzip", name, rec.Code, rec.Header().Get("Content-Encoding"))
		}
	}
}

// 长缓存头只能跟着成功响应走。条件请求不满足时 net/http 回 412，而且不会替我们清掉预设的 Cache-Control；
// 一个带 public, max-age=31536000, immutable 的 412 可以被共享缓存存一年、回给所有人。
func TestStaticFrontendErrorResponsesNeverCarryLongCache(t *testing.T) {
	env := newStaticTestEnv(t)
	older := staticTestModTime.Add(-time.Hour).Format(http.TimeFormat)
	cases := []struct {
		name, target string
		headers      map[string]string
		wantStatus   int
	}{
		{"hashed If-Match", staticTestHashedJSPath, map[string]string{"If-Match": `"x"`}, http.StatusPreconditionFailed},
		{"hashed If-Unmodified-Since", staticTestHashedJSPath, map[string]string{"If-Unmodified-Since": older}, http.StatusPreconditionFailed},
		{"hashed unsatisfiable Range", staticTestHashedJSPath, map[string]string{"Range": "bytes=999999-"}, http.StatusRequestedRangeNotSatisfiable},
		{"non-hashed If-Match", "/assets/optimization.css", map[string]string{"If-Match": `"x"`}, http.StatusPreconditionFailed},
		{"index If-Match", "/", map[string]string{"If-Match": `"x"`}, http.StatusPreconditionFailed},
		{"deep link If-Match", "/scripts", map[string]string{"If-Match": `"x"`}, http.StatusPreconditionFailed},
	}
	for _, tc := range cases {
		rec := serveStatic(env.engine, http.MethodGet, tc.target, tc.headers)
		if rec.Code != tc.wantStatus {
			t.Fatalf("%s: status=%d, want %d", tc.name, rec.Code, tc.wantStatus)
		}
		if cc := rec.Header().Values("Cache-Control"); len(cc) != 1 || cc[0] != staticCacheNoStore {
			t.Fatalf("%s: cache-control=%q, want only %q", tc.name, cc, staticCacheNoStore)
		}
	}
}

func TestStaticFrontendGzipIgnoresRange(t *testing.T) {
	env := newStaticTestEnv(t)
	rec := serveStatic(env.engine, http.MethodGet, staticTestHashedJSPath, map[string]string{"Accept-Encoding": "gzip", "Range": "bytes=0-15"})
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Encoding") != "gzip" {
		t.Fatalf("gzip + Range: status=%d content-encoding=%q, want a full 200 gzip", rec.Code, rec.Header().Get("Content-Encoding"))
	}
	if got := gunzipString(t, rec.Body.Bytes()); got != staticTestHashedJS {
		t.Fatal("gzip + Range: expected the complete body")
	}

	rec = serveStatic(env.engine, http.MethodGet, staticTestHashedJSPath, map[string]string{"Range": "bytes=0-15"})
	if rec.Code != http.StatusPartialContent || rec.Body.String() != staticTestHashedJS[:16] {
		t.Fatalf("identity + Range: status=%d body=%q, want 206 with the first 16 bytes", rec.Code, rec.Body.String())
	}
	if cc := rec.Header().Get("Cache-Control"); cc != staticCacheImmutable {
		t.Fatalf("identity + Range: cache-control=%q", cc)
	}
}

func TestStaticFrontendGzipCacheFollowsFileReplacement(t *testing.T) {
	env := newStaticTestEnv(t)
	full := filepath.Join(env.root, "assets", "index-CZqAZ7Ga.js")
	fetch := func() string {
		t.Helper()
		rec := serveStatic(env.engine, http.MethodGet, staticTestHashedJSPath, staticTestGzip)
		if rec.Code != http.StatusOK || rec.Header().Get("Content-Encoding") != "gzip" {
			t.Fatalf("status=%d content-encoding=%q", rec.Code, rec.Header().Get("Content-Encoding"))
		}
		return gunzipString(t, rec.Body.Bytes())
	}

	if fetch() != staticTestHashedJS || fetch() != staticTestHashedJS {
		t.Fatal("unexpected initial content")
	}
	if runs := env.sf.gzip.compressRuns.Load(); runs != 1 {
		t.Fatalf("expected the file to be compressed once, got %d", runs)
	}

	// 升级：整个文件删掉重建，大小变了。
	v2 := strings.Repeat("export const panel = 'v2';\n", 90)
	if err := os.Remove(full); err != nil {
		t.Fatal(err)
	}
	writeStaticTestFile(t, env.root, "assets/index-CZqAZ7Ga.js", v2, staticTestModTime.Add(time.Hour))
	if got := fetch(); got != v2 {
		t.Fatalf("after replacement (size changed) the old gzip was served: %q...", got[:32])
	}

	// 大小不变、只有修改时间变了，同样不能再回旧内容。
	v3 := strings.Repeat("export const panel = 'v3';\n", 90)
	writeStaticTestFile(t, env.root, "assets/index-CZqAZ7Ga.js", v3, staticTestModTime.Add(2*time.Hour))
	if got := fetch(); got != v3 {
		t.Fatalf("after replacement (same size, new mtime) the old gzip was served: %q...", got[:32])
	}
	if got := fetch(); got != v3 {
		t.Fatal("cached content drifted")
	}
	if runs := env.sf.gzip.compressRuns.Load(); runs != 3 {
		t.Fatalf("expected exactly one compression per file version (3), got %d", runs)
	}
	if entries, _ := env.sf.gzip.snapshot(); entries != 1 {
		t.Fatalf("old versions must be replaced, not accumulated: %d entries", entries)
	}
}

func TestStaticFrontendGzipCompressesOncePerFile(t *testing.T) {
	env := newStaticTestEnv(t)
	const workers = 16
	recs := make([]*httptest.ResponseRecorder, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			recs[i] = serveStatic(env.engine, http.MethodGet, staticTestHashedJSPath, staticTestGzip)
		}(i)
	}
	wg.Wait()
	for i, rec := range recs {
		if rec.Code != http.StatusOK || rec.Header().Get("Content-Encoding") != "gzip" {
			t.Fatalf("worker %d: status=%d content-encoding=%q", i, rec.Code, rec.Header().Get("Content-Encoding"))
		}
		if gunzipString(t, rec.Body.Bytes()) != staticTestHashedJS {
			t.Fatalf("worker %d: body mismatch", i)
		}
	}
	if runs := env.sf.gzip.compressRuns.Load(); runs != 1 {
		t.Fatalf("%d concurrent requests should share one compression, got %d", workers, runs)
	}

	serveStatic(env.engine, http.MethodGet, "/assets/ansi--b05i_G0.js", staticTestGzip)
	if runs := env.sf.gzip.compressRuns.Load(); runs != 2 {
		t.Fatalf("a different file should be compressed separately, got %d runs", runs)
	}
}

func TestGzipAssetCacheStaysBounded(t *testing.T) {
	env := newStaticTestEnv(t)
	env.sf.gzip.maxBytes = 1 // 放进第二个条目就必须把前一个挤掉
	for _, target := range []string{staticTestHashedJSPath, "/assets/ansi--b05i_G0.js", "/assets/monaco-CKol-9S0.css"} {
		rec := serveStatic(env.engine, http.MethodGet, target, staticTestGzip)
		if rec.Code != http.StatusOK || rec.Header().Get("Content-Encoding") != "gzip" {
			t.Fatalf("%s: status=%d content-encoding=%q", target, rec.Code, rec.Header().Get("Content-Encoding"))
		}
	}
	entries, total := env.sf.gzip.snapshot()
	if entries != 1 || total <= 0 {
		t.Fatalf("expected eviction down to the newest entry, got entries=%d total=%d", entries, total)
	}
}

func TestIsViteHashedAssetNameMatchesRealBuild(t *testing.T) {
	names := strings.Fields(staticTestRealBuildAssets)
	if len(names) != 138 {
		t.Fatalf("real build sample changed size: %d", len(names))
	}
	for _, name := range names {
		if !isViteHashedAssetName(name) {
			t.Errorf("real hashed asset not recognised: %s", name)
		}
	}
	// 同一套命名下的 sourcemap 与其它可能出现的类型
	for _, name := range []string{"index-CZqAZ7Ga.js.map", "monaco-CKol-9S0.css.map", "chunk-Ab1_x-Yz.mjs", "logo-B2vHP4ye.svg", "Inter-DGcffx3Z.woff2"} {
		if !isViteHashedAssetName(name) {
			t.Errorf("hash-shaped asset not recognised: %s", name)
		}
	}
}

func TestIsViteHashedAssetNameRejectsNonHashNames(t *testing.T) {
	for _, name := range []string{
		"optimization.css", "fonts.css", "index.html", "favicon-512.webp",
		"inter-latin-400.woff2", "jetbrains-mono-latin-ext-500.woff2",
		"my-reallong.css", "user-Settings.css", "theme-darkmode.css", "backup-20260915.css",
		"-CZqAZ7Ga.js", "index-CZqAZ7G.js", "index-CZqAZ7Gaa.js", "index-CZq.Z7Ga.js",
		"index-CZqAZ7Ga.JS", "index-CZqAZ7Ga.html", "index-CZqAZ7Ga.txt", "index-CZqAZ7Ga.js.gz",
		"index-________.js", "index-12345678.js",
	} {
		if isViteHashedAssetName(name) {
			t.Errorf("non-hash name treated as hashed (would be pinned for a year): %s", name)
		}
	}
}

// web/public 下的文件会被 Vite 原样拷进 dist，名字里不带哈希；其中任何一个被判成哈希，
// 都会被浏览器以 immutable 钉死一年。新增 public 文件时这条会先红。
func TestWebPublicAssetsAreNeverTreatedAsHashed(t *testing.T) {
	publicDir := filepath.Join("..", "web", "public")
	if _, err := os.Stat(publicDir); os.IsNotExist(err) {
		t.Skip("web/public not found")
	}
	checked := 0
	err := filepath.WalkDir(publicDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		checked++
		if isViteHashedAssetName(d.Name()) {
			t.Errorf("web/public file would be served as immutable: %s", p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if checked == 0 {
		t.Fatal("expected to check at least one web/public file")
	}
}

func TestIsStaticAssetRequestPath(t *testing.T) {
	for _, p := range []string{
		"/", "/login", "/dashboard", "/scripts", "/tasks", "/envs", "/config-file", "/subscriptions",
		"/logs", "/deps", "/admin/settings", "/admin/open-api", "/docs/api", "/dev/motion",
		"/index.html", "/some/unknown/page", "/api", "/assetsx",
	} {
		if isStaticAssetRequestPath(p) {
			t.Errorf("SPA path must fall back to index.html: %s", p)
		}
	}
	for _, p := range []string{
		"/assets", "/assets/", "/assets/x", "/fonts/a", "/sponsor-portal/x", "/monaco/vs/loader.js",
		"/favicon.ico", "/x/y.JS", "/a.woff2", "/a.map", "/a.json", "/a.mjs",
	} {
		if !isStaticAssetRequestPath(p) {
			t.Errorf("static-looking path must get a real 404: %s", p)
		}
	}
}

func TestAcceptsGzip(t *testing.T) {
	cases := map[string]bool{
		"":                        false,
		"gzip":                    true,
		"GZIP":                    true,
		"x-gzip":                  true,
		"gzip;q=0.5":              true,
		"gzip; q=1":               true,
		"gzip, deflate, br, zstd": true,
		"gzip;q=0":                false,
		"gzip;q=0.0":              false,
		"gzip;q=abc":              false,
		"gzip;q=NaN":              false,
		"gzip;q=2":                false,
		"br":                      false,
		"identity":                false,
		"deflate, br":             false,
		"*":                       true,
		"*;q=0":                   false,
		"gzip;q=0, *":             false,
		"br, *;q=0.1":             true,
	}
	for ae, want := range cases {
		h := http.Header{}
		if ae != "" {
			h.Set("Accept-Encoding", ae)
		}
		if got := acceptsGzip(h); got != want {
			t.Errorf("acceptsGzip(%q)=%v, want %v", ae, got, want)
		}
	}
	h := http.Header{}
	h.Add("Accept-Encoding", "br")
	h.Add("Accept-Encoding", "gzip")
	if !acceptsGzip(h) {
		t.Error("gzip listed on a second Accept-Encoding line must count")
	}
}

// docker/nginx.conf 与这里是同一套缓存口径的两份实现（Docker 走 nginx，其余三种部署走 Go）。
// nginx 的正则没法在 Go 里执行（PCRE 的 (?=…) 不被 RE2 支持），这里退而求其次逐字比对，
// 让「改了一边忘了另一边」在 CI 里先红。
func TestNginxStaticCacheRulesMatchGo(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "docker", "nginx.conf"))
	if os.IsNotExist(err) {
		t.Skip("docker/nginx.conf not found")
	}
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		lines = append(lines, line)
	}
	indexOf := func(header string) int { return slices.Index(lines, header) }
	blockOf := func(header string) []string {
		i := indexOf(header)
		if i < 0 {
			return nil
		}
		for j := i + 1; j < len(lines); j++ {
			if lines[j] == "}" {
				return lines[i+1 : j]
			}
		}
		return nil
	}

	const robots = `add_header X-Robots-Tag "noindex, nofollow, noarchive, nosnippet" always;`
	const indexHeader = `location = /index.html {`
	hashedHeader := `location ~ "^/assets/(?:[^/]+/)*[^/]+-` + nginxHashedAssetLookaheads + `[A-Za-z0-9_-]{8}\.` + viteHashedAssetExts + `$" {`
	const genericHeader = `location ~* \.(js|mjs|css|map|json|png|jpg|jpeg|gif|ico|svg|webp|avif|woff2?|ttf|otf|wasm)$ {`

	for _, tc := range []struct{ header, cacheControl string }{
		{indexHeader, `add_header Cache-Control "no-cache";`},
		{hashedHeader, `add_header Cache-Control "public, max-age=31536000, immutable";`},
		{genericHeader, `add_header Cache-Control "no-cache";`},
	} {
		body := blockOf(tc.header)
		if body == nil {
			t.Fatalf("nginx.conf is missing the location %s", tc.header)
		}
		if !slices.Contains(body, tc.cacheControl) {
			t.Fatalf("%s must contain %s, got %q", tc.header, tc.cacheControl, body)
		}
		// location 里写了 add_header 就不再继承 server 级的 add_header，X-Robots-Tag 必须重写一遍。
		if !slices.Contains(body, robots) {
			t.Fatalf("%s must repeat %s (add_header is not inherited)", tc.header, robots)
		}
	}
	// PWA 根路径文件（v4.0），与 staticRootFiles 同口径：sw.js 与清单都 no-cache，清单还要显式补类型映射
	// （基础镜像的 mime.types 不一定有 webmanifest，没有就回成 application/octet-stream）。
	swBody := blockOf(`location = /sw.js {`)
	if !slices.Contains(swBody, `add_header Cache-Control "no-cache";`) || !slices.Contains(swBody, robots) {
		t.Fatalf("nginx.conf must serve /sw.js with no-cache and X-Robots-Tag, got %q", swBody)
	}
	manifestBody := blockOf(`location = /manifest.webmanifest {`)
	if !slices.Contains(manifestBody, `types { application/manifest+json webmanifest; }`) ||
		!slices.Contains(manifestBody, `add_header Cache-Control "no-cache";`) || !slices.Contains(manifestBody, robots) {
		t.Fatalf("nginx.conf must serve /manifest.webmanifest as application/manifest+json with no-cache, got %q", manifestBody)
	}
	for _, name := range []string{staticServiceWorkerFile, staticManifestFile} {
		if !slices.Contains(staticRootFiles, name) {
			t.Fatalf("staticRootFiles must contain %s", name)
		}
	}
	if !slices.Contains(lines, robots) {
		t.Fatal("server-level X-Robots-Tag is missing")
	}
	// 正则 location 按书写顺序先中先用：带哈希的必须写在兜底的前面。
	if indexOf(hashedHeader) > indexOf(genericHeader) {
		t.Fatal("the hashed-asset location must come before the generic static location")
	}
	immutableLines := 0
	for _, line := range lines {
		if strings.Contains(line, "immutable") {
			immutableLines++
		}
	}
	if immutableLines != 1 {
		t.Fatalf("immutable may only appear in the hashed-asset location, found %d lines", immutableLines)
	}
}
