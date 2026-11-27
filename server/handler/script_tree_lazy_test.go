package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"daidai-panel/config"
	"daidai-panel/testutil"

	"github.com/gin-gonic/gin"
)

// #183：脚本目录里有 .npm-cache 这类海量小文件的缓存目录时，GET /scripts/tree 递归整棵树超过前端 30 秒超时。
// 这里覆盖四件事：缓存目录只在展示里跳过；带 lazy=1 时大目录 / 大树懒加载；不带 lazy 时保持完整递归
// （Dumb-Panel-APP、MCP 这些老调用方不认 lazy 字段）；?path= 按目录取子项且走 safePath 校验。

func newScriptTreeTestRouter(t *testing.T) (*gin.Engine, string) {
	t.Helper()
	engine := gin.New()
	NewScriptHandler().RegisterRoutes(engine.Group("/api/v1"))
	user := testutil.MustCreateUser(t, "script-tree-lazy", "operator")
	token := testutil.MustCreateAccessToken(t, user.Username, user.Role)
	return engine, token
}

func getScriptJSON(t *testing.T, engine *gin.Engine, token, path string) (int, map[string]interface{}) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	payload := map[string]interface{}{}
	_ = json.Unmarshal(rec.Body.Bytes(), &payload)
	return rec.Code, payload
}

func mustWriteScriptFile(t *testing.T, rel string) {
	t.Helper()
	full := filepath.Join(config.C.Data.ScriptsDir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte("console.log(1)"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// flattenTreeNodes 把树摊平成 key → 节点。
func flattenTreeNodes(nodes []interface{}, out map[string]map[string]interface{}) {
	for _, raw := range nodes {
		node := raw.(map[string]interface{})
		out[node["key"].(string)] = node
		if children, ok := node["children"].([]interface{}); ok {
			flattenTreeNodes(children, out)
		}
	}
}

func TestScriptTreeAndListSkipCacheDirs(t *testing.T) {
	testutil.SetupTestEnv(t)
	engine, token := newScriptTreeTestRouter(t)

	mustWriteScriptFile(t, "main.js")
	mustWriteScriptFile(t, "jd/task.js")
	for _, cacheDir := range []string{".npm-cache", ".npm", ".cache", ".pnpm-store", ".yarn-cache", "jd/.npm-cache"} {
		mustWriteScriptFile(t, cacheDir+"/_cacache/index.js")
	}

	code, payload := getScriptJSON(t, engine, token, "/api/v1/scripts/tree")
	if code != http.StatusOK {
		t.Fatalf("tree 应返回 200，实际 %d %v", code, payload)
	}
	nodes := map[string]map[string]interface{}{}
	flattenTreeNodes(payload["data"].([]interface{}), nodes)
	for _, key := range []string{"main.js", "jd", "jd/task.js"} {
		if nodes[key] == nil {
			t.Fatalf("脚本树应包含 %s，实际 %v", key, nodes)
		}
	}
	for key := range nodes {
		for _, cacheDir := range []string{".npm-cache", ".npm", ".cache", ".pnpm-store", ".yarn-cache"} {
			if key == cacheDir || filepath.Base(key) == cacheDir {
				t.Fatalf("脚本树不应包含缓存目录 %s", key)
			}
		}
	}

	code, payload = getScriptJSON(t, engine, token, "/api/v1/scripts")
	if code != http.StatusOK {
		t.Fatalf("list 应返回 200，实际 %d", code)
	}
	if total := payload["total"].(float64); total != 2 {
		t.Fatalf("脚本列表应只有 main.js、jd/task.js 两个，实际 %v：%v", total, payload["data"])
	}

	// 只在展示里跳过：缓存目录里的文件照样能按路径读（不进 safePath 的拒绝名单）
	code, _ = getScriptJSON(t, engine, token, "/api/v1/scripts/content?path=.npm-cache/_cacache/index.js")
	if code != http.StatusOK {
		t.Fatalf("缓存目录里的文件仍应可读，实际 %d", code)
	}
}

func TestScriptTreeLazyLoadsLargeDirectories(t *testing.T) {
	testutil.SetupTestEnv(t)
	engine, token := newScriptTreeTestRouter(t)

	// big 目录的直接子项超过阈值：它下面的子目录 big/sub 不递归，标成 lazy
	for i := 0; i <= scriptTreeLazyDirEntries; i++ {
		mustWriteScriptFile(t, fmt.Sprintf("big/f%04d.js", i))
	}
	mustWriteScriptFile(t, "big/sub/deep.js")
	// 普通小目录照常整棵展开
	mustWriteScriptFile(t, "small/sub/deep.js")

	code, payload := getScriptJSON(t, engine, token, "/api/v1/scripts/tree?lazy=1")
	if code != http.StatusOK {
		t.Fatalf("tree 应返回 200，实际 %d", code)
	}
	nodes := map[string]map[string]interface{}{}
	flattenTreeNodes(payload["data"].([]interface{}), nodes)
	if nodes["big/f0000.js"] == nil {
		t.Fatal("大目录自己的直接子项仍应列出")
	}
	sub := nodes["big/sub"]
	if sub == nil || sub["lazy"] != true || len(sub["children"].([]interface{})) != 0 {
		t.Fatalf("big/sub 应是 children 为空的懒加载节点，实际 %v", sub)
	}
	if nodes["big/sub/deep.js"] != nil {
		t.Fatal("懒加载目录下的文件不应出现在整棵树里")
	}
	if nodes["small/sub/deep.js"] == nil || nodes["small/sub"]["lazy"] != nil {
		t.Fatalf("小目录应整棵展开、不带 lazy，实际 %v", nodes["small/sub"])
	}

	// 不带 lazy：保持 v4.0.1 的完整递归，一个 lazy 节点都没有
	code, payload = getScriptJSON(t, engine, token, "/api/v1/scripts/tree")
	if code != http.StatusOK {
		t.Fatalf("tree 应返回 200，实际 %d", code)
	}
	full := map[string]map[string]interface{}{}
	flattenTreeNodes(payload["data"].([]interface{}), full)
	if full["big/sub/deep.js"] == nil {
		t.Fatal("不带 lazy 时应整棵递归，big/sub/deep.js 应出现在树里")
	}
	for key, node := range full {
		if node["lazy"] != nil {
			t.Fatalf("不带 lazy 时不应出现懒加载节点，实际 %s 带了 lazy", key)
		}
	}

	// 按目录取子项：key 与整棵树里的口径一致
	code, payload = getScriptJSON(t, engine, token, "/api/v1/scripts/tree?lazy=1&path=big/sub")
	if code != http.StatusOK {
		t.Fatalf("?path= 应返回 200，实际 %d %v", code, payload)
	}
	children := map[string]map[string]interface{}{}
	flattenTreeNodes(payload["data"].([]interface{}), children)
	if len(children) != 1 || children["big/sub/deep.js"] == nil {
		t.Fatalf("big/sub 的子项应只有 big/sub/deep.js，实际 %v", children)
	}
	// 反斜杠、./ 前缀按后端口径规范化
	if code, payload = getScriptJSON(t, engine, token, "/api/v1/scripts/tree?lazy=1&path=./big%5Csub"); code != http.StatusOK {
		t.Fatalf("规范化后的路径应可取，实际 %d %v", code, payload)
	}
	children = map[string]map[string]interface{}{}
	flattenTreeNodes(payload["data"].([]interface{}), children)
	if children["big/sub/deep.js"] == nil {
		t.Fatalf("规范化后的 key 应是 big/sub/deep.js，实际 %v", children)
	}
}

func TestScriptTreeLazyByTotalNodes(t *testing.T) {
	testutil.SetupTestEnv(t)

	base := t.TempDir()
	// 7 个目录 × 450 个文件：前 5 个目录展开后节点数（2255）已超过总数阈值（2000），后面 2 个目录应懒加载；每个目录的直接子项都没超过单目录阈值
	for d := 0; d < 7; d++ {
		for f := 0; f < 450; f++ {
			full := filepath.Join(base, fmt.Sprintf("d%02d", d), fmt.Sprintf("f%03d.js", f))
			if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(full, nil, 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	count := 0
	tree := buildTree(base, "", &count)
	lazy := 0
	for _, node := range tree {
		if node["lazy"] == true {
			lazy++
		}
	}
	if len(tree) != 7 || lazy != 2 {
		t.Fatalf("7 个目录都应列出，且超过总数阈值后的最后 2 个目录应是懒加载，实际 %d 个、lazy %d 个", len(tree), lazy)
	}
	if count > scriptTreeLazyTotalNodes+scriptTreeLazyDirEntries {
		t.Fatalf("节点总数应被阈值兜住，实际 %d", count)
	}
}

func TestScriptTreePathParamIsValidated(t *testing.T) {
	testutil.SetupTestEnv(t)
	engine, token := newScriptTreeTestRouter(t)
	mustWriteScriptFile(t, "a/b.js")
	mustWriteScriptFile(t, "repo/.git/config")

	for _, path := range []string{"../", "a/../../x", "repo/.git", "a/b.js", "missing"} {
		code, payload := getScriptJSON(t, engine, token, "/api/v1/scripts/tree?path="+path)
		if code != http.StatusBadRequest {
			t.Fatalf("path=%q 应返回 400，实际 %d %v", path, code, payload)
		}
	}
}

// 不懒加载（nodeCount 传 nil）时，超过两道阈值也照样整棵递归
func TestBuildTreeWithoutLazyRecursesFully(t *testing.T) {
	base := t.TempDir()
	for i := 0; i <= scriptTreeLazyDirEntries; i++ {
		full := filepath.Join(base, "big", fmt.Sprintf("f%04d.js", i))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	deep := filepath.Join(base, "big", "sub", "deep.js")
	if err := os.MkdirAll(filepath.Dir(deep), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(deep, nil, 0o644); err != nil {
		t.Fatal(err)
	}

	nodes := map[string]map[string]interface{}{}
	var raw []interface{}
	for _, node := range buildTree(base, "", nil) {
		raw = append(raw, node)
	}
	// buildTree 返回的是 []map[string]interface{}，children 也是同类型；先过一遍 JSON 统一成 interface{}
	data, _ := json.Marshal(raw)
	var decoded []interface{}
	_ = json.Unmarshal(data, &decoded)
	flattenTreeNodes(decoded, nodes)
	if nodes["big/sub/deep.js"] == nil || nodes["big/sub"]["lazy"] != nil {
		t.Fatalf("不懒加载时 big/sub 应整棵展开，实际 %v", nodes["big/sub"])
	}
}
