package main

import (
	"bytes"
	"compress/gzip"
	"io"
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
)

// 静态前端托管：二进制 / Windows / Magisk 三种内嵌部署共用这一份代码（入口是 main.go 的
// setupStaticFrontend）；Docker 部署走 nginx，对应规则在 docker/nginx.conf，两边口径保持一致。
//
// 缓存与 404 契约（#126）。以前这里一个 Cache-Control 都不下发，浏览器按启发式规则把旧 index.html
// 当新鲜内容用上一段时间：升级后继续跑旧前端、去请求已被删掉的旧 hash 资源；这些请求又被 SPA 回退
// 成 200 + text/html，叠加全局的 X-Content-Type-Options: nosniff，JS/CSS 全部被浏览器拒收：
//   - index.html（/ 与 SPA 深链回退）→ no-cache：可以存，但每次都要回源校验（Last-Modified 命中即 304）；
//   - /assets/ 下带 Vite 内容哈希的文件 → public, max-age=31536000, immutable；
//   - 其余静态文件（/assets 下不带哈希的如 optimization.css、/fonts/*、根路径白名单文件、/sponsor-portal/*）→ no-cache；
//   - 缺失的静态资源 → 纯文本 404 + no-store，不再回退 index.html；
//   - /api/ 下没命中的路由 → 仍是 JSON 404。
//
// 压缩：只有 /assets/ 下的可压缩类型（js/css/svg/json/map/ttf）在客户端接受 gzip 时回 gzip，
// 压缩结果按 (路径, 大小, 修改时间) 缓存在内存里，每个文件每进程只压一次。
// 压缩只写在这几个静态路由自己的处理函数里，不是全局中间件：SSE、下载等 API 一律不经过这里。

const (
	staticCacheNoCache   = "no-cache"
	staticCacheImmutable = "public, max-age=31536000, immutable"
	staticCacheNoStore   = "no-store"

	// 面板图标。这是单文件白名单：换文件名（如 .svg -> .webp）时必须同步改这里和
	// web/index.html 的 <link rel="icon">，否则浏览器拿到的是 404。
	staticFaviconFile = "favicon-512.webp"

	// PWA（v4.0）的清单与 Service Worker，都必须挂在站点根路径：
	// Service Worker 的默认作用域是它自己所在的目录，放进 /assets/ 就管不到整个面板。
	staticManifestFile      = "manifest.webmanifest"
	staticServiceWorkerFile = "sw.js"

	// 小于 1 KiB 的文件压缩收益抵不过 gzip 头和 CPU，直接回原文（与 nginx.conf 的 gzip_min_length 同口径）。
	gzipAssetMinSize = 1 << 10
	// 单个文件超过这个大小就不压缩、直接回原文，防止一个异常大的文件占住内存。
	// 目前最大的产物是 monaco-*.js（约 3.8 MiB 未压缩）。
	gzipAssetMaxFileSize = 32 << 20
	// 压缩缓存的总字节上限：只防「前端目录换过但进程没重启」时旧条目越堆越多，正常远远用不到。
	gzipAssetCacheMaxBytes = 64 << 20
)

// staticFrontendDirs 是白名单：只有这几个子目录会按文件托管。
// 不在列表里的子目录会掉进 NoRoute：扩展名像静态资源的（见 isStaticAssetRequestPath）回 404，
// 其余的被当成 SPA 深链回 index.html。所以新增前端静态目录时必须同步加到这里。
//
// "fonts" 是自托管 Web 字体（web/public/fonts/），Docker 部署走 nginx 的
// try_files 不受影响，但内嵌二进制部署（无 nginx）依赖这一条。
var staticFrontendDirs = []string{"assets", "fonts", "sponsor-portal"}

// staticRootFiles 是根路径下按单个文件托管的白名单（来自 web/public/ 根目录，Vite 原样拷进产物）。
// 全部 no-cache、不压缩；文件不存在时回纯文本 404，不回退 index.html。
// 不在这里的根路径文件会掉进 NoRoute：清单被回成 HTML 会解析失败，sw.js 被回成 HTML 会因 MIME 不对注册失败，
// 所以新增根路径文件必须加到这里。
//
// sw.js 必须 no-cache：浏览器检查 Service Worker 更新时会参考 HTTP 缓存，被缓存住就一直跑旧的 SW。
// Docker 部署的对应规则在 docker/nginx.conf。
var staticRootFiles = []string{
	staticFaviconFile,
	staticManifestFile,
	staticServiceWorkerFile,
	"pwa-192.png",
	"pwa-512.png",
	"pwa-maskable-512.png",
}

// staticAssetPathPrefixes 下的路径没命中文件时一律回 404，不回退 index.html。
// "/monaco/" 是 v3.1.1 及以前 AMD 版 Monaco 的目录（v3.2.0 删掉）：浏览器缓存里的旧前端还会来要
// /monaco/vs/loader.js，必须给它一个明确的 404，而不是一段会被当成脚本的 HTML。
var staticAssetPathPrefixes = []string{"/assets/", "/fonts/", "/sponsor-portal/", "/monaco/"}

// staticAssetExts 是「看扩展名就知道不是页面」的请求：路径不在白名单目录里也回 404 而不是 index.html。
// 前端路由（web/src/router/index.ts）没有任何一条带扩展名，所以不会误伤深链。
var staticAssetExts = map[string]struct{}{
	".js": {}, ".mjs": {}, ".css": {}, ".map": {}, ".json": {},
	".woff": {}, ".woff2": {}, ".ttf": {}, ".otf": {},
	".png": {}, ".jpg": {}, ".jpeg": {}, ".gif": {}, ".svg": {}, ".webp": {}, ".avif": {}, ".ico": {},
	".wasm": {}, ".webmanifest": {},
}

// gzipCompressibleExts 是 /assets/ 下值得 gzip 的类型。woff2 / webp / png 本身已经压缩过，刻意不列。
var gzipCompressibleExts = map[string]struct{}{
	".js": {}, ".mjs": {}, ".css": {}, ".svg": {}, ".json": {}, ".map": {}, ".ttf": {},
}

// staticContentTypes 固定常见前端产物的 Content-Type，不完全依赖宿主机的 MIME 配置：
// Windows 上 mime.TypeByExtension 会读注册表，第三方软件可能把 .css / .mjs 之类改成 text/plain
// （Go 标准库只对 .js 做了兜底），叠加 nosniff 就会被浏览器拒收。表里没有的扩展名再交给 mime 包。
var staticContentTypes = map[string]string{
	".html":  "text/html; charset=utf-8",
	".js":    "text/javascript; charset=utf-8",
	".mjs":   "text/javascript; charset=utf-8",
	".css":   "text/css; charset=utf-8",
	".json":  "application/json",
	".map":   "application/json",
	".svg":   "image/svg+xml",
	".png":   "image/png",
	".jpg":   "image/jpeg",
	".jpeg":  "image/jpeg",
	".gif":   "image/gif",
	".webp":  "image/webp",
	".avif":  "image/avif",
	".ico":   "image/x-icon",
	".ttf":   "font/ttf",
	".otf":   "font/otf",
	".woff":  "font/woff",
	".woff2": "font/woff2",
	".wasm":  "application/wasm",
	// PWA 清单：Go 的 mime 包内置表和很多系统的 MIME 表里都没有这个扩展名，不写死会拿到空 Content-Type。
	".webmanifest": "application/manifest+json",
}

// viteHashedAssetExts 与 docker/nginx.conf 里「带哈希产物」那条 location 的扩展名部分逐字相同。
const viteHashedAssetExts = `(?:(?:js|mjs|css)(?:\.map)?|json|wasm|svg|png|jpe?g|gif|webp|avif|ico|ttf|otf|woff2?)`

// viteHashedAssetPattern 匹配 Vite / Rollup 默认产物名 <name>-<hash>.<ext> 的形态：
// hash 是 8 位 base64url（A-Z a-z 0-9 _ -），所以既可能以 - 开头也可能以 - 结尾，例如
// index-CZqAZ7Ga.js、ansi--b05i_G0.js、ExecutionTrendChart-CunqJMw-.js、codicon-Brq4_Ui5.ttf。
// 捕获组是那 8 位哈希片段，再交给 looksLikeContentHash 排除人起的名字。
var viteHashedAssetPattern = regexp.MustCompile(`^.+-([A-Za-z0-9_-]{8})\.` + viteHashedAssetExts + `$`)

// isViteHashedAssetName 判断 /assets/ 下的文件名（不含目录）是不是 Vite 带内容哈希的产物。
// 只有它为 true 的文件才能拿 immutable：内容变了文件名一定跟着变。判错的代价不对称 ——
// 把哈希文件当成普通文件只是多一次 304 校验；把普通文件（如 web/public/assets/ 原样拷来的
// optimization.css）当成哈希文件，浏览器会把它钉死一年。所以规则只往保守的方向放。
// docker/nginx.conf 有一条逐条对应的正则，改这里必须同步改那里（static_frontend_test.go 会比对）。
func isViteHashedAssetName(base string) bool {
	m := viteHashedAssetPattern.FindStringSubmatch(base)
	return m != nil && looksLikeContentHash(m[1])
}

// looksLikeContentHash 排除「长得像单词或日期」的 8 位片段：至少要有一个字母，并且至少有一个
// 数字 / _ / -，或者首字符以外的大写字母。my-reallong.css、user-Settings.css、backup-20260915.css
// 都会被排除；真哈希被误排除的概率约千分之一点五，代价只是那个文件改走 no-cache。
func looksLikeContentHash(seg string) bool {
	hasLetter, hasMark := false, false
	for i := 0; i < len(seg); i++ {
		switch ch := seg[i]; {
		case ch >= 'a' && ch <= 'z':
			hasLetter = true
		case ch >= 'A' && ch <= 'Z':
			hasLetter = true
			if i > 0 {
				hasMark = true
			}
		default: // 0-9 _ -（字符集已由正则保证）
			hasMark = true
		}
	}
	return hasLetter && hasMark
}

// isStaticAssetRequestPath 判断 NoRoute 里的路径是不是「在要静态文件」：是的话回 404，不回退 index.html。
func isStaticAssetRequestPath(p string) bool {
	for _, prefix := range staticAssetPathPrefixes {
		if strings.HasPrefix(p, prefix) || p == strings.TrimSuffix(prefix, "/") {
			return true
		}
	}
	_, ok := staticAssetExts[strings.ToLower(path.Ext(p))]
	return ok
}

func isGzipCompressibleAsset(name string) bool {
	_, ok := gzipCompressibleExts[strings.ToLower(path.Ext(name))]
	return ok
}

func staticContentType(name string) string {
	ext := strings.ToLower(filepath.Ext(name))
	if ct, ok := staticContentTypes[ext]; ok {
		return ct
	}
	return mime.TypeByExtension(ext)
}

// staticFrontend 托管一个前端目录。除了 gzip 缓存外没有可变状态，可以并发使用。
type staticFrontend struct {
	root      string
	indexPath string
	gzip      *gzipAssetCache
}

func newStaticFrontend(root string) *staticFrontend {
	return &staticFrontend{
		root:      root,
		indexPath: filepath.Join(root, "index.html"),
		gzip:      newGzipAssetCache(gzipAssetCacheMaxBytes),
	}
}

// register 挂上 /、根路径白名单文件、白名单目录与 NoRoute。路由集合与改造前（StaticFile + Static）一致
// （v4.0 起根路径白名单多了 PWA 那几个文件），只是换成自己的处理函数，好控制缓存头、404 与压缩。
func (sf *staticFrontend) register(engine *gin.Engine) {
	handleGetAndHead(engine, "/", sf.serveIndex)
	for _, name := range staticRootFiles {
		// 与目录白名单不同，这里不看文件在不在都注册：文件缺失时由 serveLocalFile 回纯文本 404，
		// 而不是掉进 NoRoute 被回退成 index.html。
		fullPath := filepath.Join(sf.root, name)
		handleGetAndHead(engine, "/"+name, func(c *gin.Context) {
			sf.serveLocalFile(c, fullPath)
		})
	}
	for _, sub := range staticFrontendDirs {
		subDir := filepath.Join(sf.root, sub)
		if info, err := os.Stat(subDir); err == nil && info.IsDir() {
			handleGetAndHead(engine, "/"+sub+"/*filepath", sf.serveDir(sub, subDir))
		}
	}
	engine.NoRoute(sf.handleNoRoute)
}

func handleGetAndHead(engine *gin.Engine, relativePath string, handler gin.HandlerFunc) {
	engine.GET(relativePath, handler)
	engine.HEAD(relativePath, handler)
}

func (sf *staticFrontend) serveIndex(c *gin.Context) {
	sf.serveLocalFile(c, sf.indexPath)
}

// serveLocalFile 回一个固定路径的文件（index.html、根路径白名单文件），一律 no-cache、不压缩。
func (sf *staticFrontend) serveLocalFile(c *gin.Context, fullPath string) {
	f, err := os.Open(fullPath)
	if err != nil {
		writeStaticNotFound(c)
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || info.IsDir() {
		writeStaticNotFound(c)
		return
	}
	sf.serveOpenedFile(c, f, info, staticCacheNoCache, "")
}

// serveDir 回白名单目录下的文件。路径经 http.Dir 解析（先 path.Clean，并拒绝不安全的路径，
// 例如 Windows 上的反斜杠），跳不出这个目录；目录本身、缺失的文件一律 404。
func (sf *staticFrontend) serveDir(sub, dir string) gin.HandlerFunc {
	fsys := http.Dir(dir)
	isAssets := sub == "assets"
	return func(c *gin.Context) {
		name := path.Clean("/" + c.Param("filepath"))
		f, err := fsys.Open(name)
		if err != nil {
			writeStaticNotFound(c)
			return
		}
		defer f.Close()
		info, err := f.Stat()
		if err != nil || info.IsDir() {
			writeStaticNotFound(c)
			return
		}

		cacheControl, gzipKey := staticCacheNoCache, ""
		if isAssets {
			if isViteHashedAssetName(path.Base(name)) {
				cacheControl = staticCacheImmutable
			}
			if isGzipCompressibleAsset(name) {
				gzipKey = "/assets" + name
			}
		}
		sf.serveOpenedFile(c, f, info, cacheControl, gzipKey)
	}
}

// handleNoRoute：/api/ 回 JSON 404；看起来是在要静态文件的回纯文本 404；
// 其余当成 SPA 深链回 index.html，交给前端 vue-router 处理。
func (sf *staticFrontend) handleNoRoute(c *gin.Context) {
	p := c.Request.URL.Path
	if strings.HasPrefix(p, "/api/") {
		c.JSON(http.StatusNotFound, gin.H{"error": "route not found"})
		return
	}
	if isStaticAssetRequestPath(p) {
		writeStaticNotFound(c)
		return
	}
	sf.serveIndex(c)
}

// serveOpenedFile 按缓存策略回一个已打开的文件。gzipKey 非空表示这个文件允许 gzip。
func (sf *staticFrontend) serveOpenedFile(c *gin.Context, f http.File, info os.FileInfo, cacheControl, gzipKey string) {
	h := c.Writer.Header()
	if ct := staticContentType(info.Name()); ct != "" {
		h.Set("Content-Type", ct)
	}
	h.Set("Cache-Control", cacheControl)

	if gzipKey != "" {
		// 同一个 URL 既可能回 gzip 也可能回原文，两种响应都要带 Vary，中间缓存才不会把 gzip 版本
		// 交给不支持的客户端。用 Add 不用 Set：CORS 中间件可能已经写过 Vary: Origin。
		h.Add("Vary", "Accept-Encoding")
		size := info.Size()
		if size >= gzipAssetMinSize && size <= gzipAssetMaxFileSize && acceptsGzip(c.Request.Header) {
			if body, ok := sf.gzip.load(gzipKey, f, info); ok {
				writeGzipAsset(c, body, info.ModTime())
				return
			}
			// 没压成（读文件出错，或读的过程中文件被改写）就回原文。f 可能已经读过一部分，先倒回开头。
			if _, err := f.Seek(0, io.SeekStart); err != nil {
				writeStaticPlain(c, http.StatusInternalServerError, "500 internal server error")
				return
			}
		}
	}

	// 原文交给 net/http：If-Modified-Since → 304、Range → 206、HEAD 都由它处理。
	http.ServeContent(errorSafeCacheWriter{c.Writer}, c.Request, info.Name(), info.ModTime(), f)
}

// errorSafeCacheWriter 让上面预设的 Cache-Control 只跟着 200 / 206 / 304 出去，其余状态码一律改成 no-store。
// http.ServeContent 自己的错误出口（如 416）会删掉预设的缓存头，但条件请求不满足的 412
// （If-Match、If-Unmodified-Since）是直接 WriteHeader 出去的，会原样带上 "public, max-age=31536000, immutable"：
// 共享缓存（CDN、带缓存的反代）可以把这个 412 存一年、回给所有人，那个资源就一直加载失败。
// nginx 那边 add_header 不带 always，本来就不作用于 412，这里保持同一口径。
type errorSafeCacheWriter struct {
	http.ResponseWriter
}

func (w errorSafeCacheWriter) WriteHeader(code int) {
	switch code {
	case http.StatusOK, http.StatusPartialContent, http.StatusNotModified:
	default:
		w.Header().Set("Cache-Control", staticCacheNoStore)
	}
	w.ResponseWriter.WriteHeader(code)
}

// writeGzipAsset 回 gzip 后的内容。支持 If-Modified-Since → 304；不支持 Range：
// 不声明 Accept-Ranges，请求里带了 Range 也回完整的 200（服务端可以忽略 Range）。
func writeGzipAsset(c *gin.Context, body []byte, modTime time.Time) {
	h := c.Writer.Header()
	if !isZeroModTime(modTime) {
		h.Set("Last-Modified", modTime.UTC().Format(http.TimeFormat))
	}
	if notModifiedSince(c.Request, modTime) {
		// 与 net/http 的 writeNotModified 同口径：304 不带表示层的元数据。
		h.Del("Content-Type")
		h.Del("Content-Length")
		h.Del("Content-Encoding")
		c.Status(http.StatusNotModified)
		return
	}
	h.Set("Content-Encoding", "gzip")
	h.Set("Content-Length", strconv.Itoa(len(body)))
	c.Status(http.StatusOK)
	if c.Request.Method == http.MethodHead {
		return
	}
	_, _ = c.Writer.Write(body)
}

// notModifiedSince 与 net/http 的条件请求判定同口径。我们不下发 ETag，所以 If-None-Match 只有 *
// 能命中；带了 If-None-Match 就不再看 If-Modified-Since（RFC 9110 §13.2.2）。
func notModifiedSince(r *http.Request, modTime time.Time) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	if inm := r.Header.Get("If-None-Match"); inm != "" {
		for _, tag := range strings.Split(inm, ",") {
			if strings.TrimSpace(tag) == "*" {
				return true
			}
		}
		return false
	}
	ims := r.Header.Get("If-Modified-Since")
	if ims == "" || isZeroModTime(modTime) {
		return false
	}
	t, err := http.ParseTime(ims)
	if err != nil {
		return false
	}
	// Last-Modified 只有秒级精度，比较前先截掉亚秒部分。
	return !modTime.Truncate(time.Second).After(t)
}

func isZeroModTime(t time.Time) bool {
	return t.IsZero() || t.Equal(time.Unix(0, 0))
}

func writeStaticNotFound(c *gin.Context) {
	writeStaticPlain(c, http.StatusNotFound, "404 page not found")
}

// writeStaticPlain 回纯文本错误。no-store：缺失文件在升级过程中可能只是暂时不在，错误响应不能被缓存；
// 纯文本则保证浏览器不会把一段 HTML 当脚本、样式缓存到资源 URL 名下。
func writeStaticPlain(c *gin.Context, code int, msg string) {
	h := c.Writer.Header()
	h.Del("Content-Encoding")
	h.Del("Content-Length")
	h.Del("Last-Modified")
	h.Set("Content-Type", "text/plain; charset=utf-8")
	h.Set("Cache-Control", staticCacheNoStore)
	c.Status(code)
	_, _ = c.Writer.WriteString(msg)
}

// acceptsGzip 按 RFC 9110 §12.5.3 解析 Accept-Encoding：显式列出 gzip（或 x-gzip）时看它的 q 值，
// 否则看通配符 *；q=0 表示明确拒绝。解析不了或越界的 q 值按拒绝处理（宁可回原文）。
func acceptsGzip(header http.Header) bool {
	gzipQ, starQ := -1.0, -1.0
	for _, value := range header.Values("Accept-Encoding") {
		for _, item := range strings.Split(value, ",") {
			coding, q := parseAcceptEncodingItem(item)
			switch coding {
			case "gzip", "x-gzip":
				gzipQ = q
			case "*":
				starQ = q
			}
		}
	}
	if gzipQ >= 0 {
		return gzipQ > 0
	}
	return starQ > 0
}

func parseAcceptEncodingItem(item string) (string, float64) {
	parts := strings.Split(item, ";")
	coding := strings.ToLower(strings.TrimSpace(parts[0]))
	q := 1.0
	for _, param := range parts[1:] {
		key, value, found := strings.Cut(strings.TrimSpace(param), "=")
		if !found || !strings.EqualFold(strings.TrimSpace(key), "q") {
			continue
		}
		parsed, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
		if err != nil || !(parsed >= 0 && parsed <= 1) {
			parsed = 0
		}
		q = parsed
	}
	return coding, q
}

// gzipAssetCache 缓存 /assets/ 下文件的 gzip 结果，按 (路径, 大小, 修改时间) 失效：
// 文件被替换（升级）后大小或修改时间一变，下一次请求就重新压缩并顶掉旧条目，不会继续回旧内容。
// 同一个文件同时来多个请求时只有第一个真正去压，其余等它的结果。
type gzipAssetCache struct {
	mu       sync.Mutex
	entries  map[string]*gzipAssetEntry
	total    int64
	maxBytes int64
	// compressRuns 记录真正执行压缩的次数，只给测试观测「每个文件每进程只压一次」。
	compressRuns atomic.Int64
}

type gzipAssetEntry struct {
	size      int64
	modTime   int64 // UnixNano
	ready     chan struct{}
	data      []byte
	ok        bool
	accounted int64 // 已计入 total 的字节数；还在压或压失败时为 0
}

func newGzipAssetCache(maxBytes int64) *gzipAssetCache {
	return &gzipAssetCache{entries: make(map[string]*gzipAssetEntry), maxBytes: maxBytes}
}

// load 返回 key 对应文件的 gzip 结果。f 必须是 info 所描述的同一个已打开文件（info 来自 f.Stat()），
// 这样缓存键和实际读到的内容来自同一个文件句柄。ok=false 时调用方应回原文。
func (gc *gzipAssetCache) load(key string, f http.File, info os.FileInfo) ([]byte, bool) {
	size, modTime := info.Size(), info.ModTime().UnixNano()

	gc.mu.Lock()
	if e, found := gc.entries[key]; found && e.size == size && e.modTime == modTime {
		gc.mu.Unlock()
		<-e.ready
		return e.data, e.ok
	}
	e := &gzipAssetEntry{size: size, modTime: modTime, ready: make(chan struct{})}
	if old, found := gc.entries[key]; found {
		gc.total -= old.accounted
	}
	gc.entries[key] = e
	gc.mu.Unlock()

	data, ok := gc.compress(f, info)

	gc.mu.Lock()
	e.data, e.ok = data, ok
	if gc.entries[key] == e {
		if ok {
			e.accounted = int64(len(data))
			gc.total += e.accounted
			gc.evictLocked(key)
		} else {
			delete(gc.entries, key) // 压失败不留条目，下一次请求重试
		}
	}
	close(e.ready)
	gc.mu.Unlock()
	return data, ok
}

func (gc *gzipAssetCache) compress(f http.File, want os.FileInfo) ([]byte, bool) {
	gc.compressRuns.Add(1)
	raw, err := io.ReadAll(io.LimitReader(f, want.Size()+1))
	if err != nil || int64(len(raw)) != want.Size() {
		return nil, false
	}
	// 读完再对同一个文件句柄 stat 一次：读的过程中文件被原地改写（大小或修改时间变了）
	// 就不缓存这份可能前后不一致的内容。
	now, err := f.Stat()
	if err != nil || now.Size() != want.Size() || !now.ModTime().Equal(want.ModTime()) {
		return nil, false
	}
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(raw); err != nil {
		return nil, false
	}
	if err := zw.Close(); err != nil {
		return nil, false
	}
	return buf.Bytes(), true
}

// evictLocked 在总字节数超过上限时丢掉其它已完成的条目（刚放进去的 keep 保留）。
func (gc *gzipAssetCache) evictLocked(keep string) {
	for k, e := range gc.entries {
		if gc.total <= gc.maxBytes {
			return
		}
		if k == keep || e.accounted == 0 {
			continue
		}
		gc.total -= e.accounted
		delete(gc.entries, k)
	}
}

// snapshot 返回当前条目数与已计入的压缩字节数（测试用）。
func (gc *gzipAssetCache) snapshot() (entries int, total int64) {
	gc.mu.Lock()
	defer gc.mu.Unlock()
	return len(gc.entries), gc.total
}
