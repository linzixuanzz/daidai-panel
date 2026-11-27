package handler

import (
	"encoding/base64"
	"mime"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"daidai-panel/pkg/response"
	"daidai-panel/service"

	"github.com/gin-gonic/gin"
)

func (h *ScriptHandler) List(c *gin.Context) {
	dir := scriptsDir()
	var files []map[string]interface{}

	filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			// 缓存目录（.npm-cache 等）只在展示里跳过，根目录自己不跳（#183）
			if shouldSkipScriptTreeDir(info.Name()) || (path != dir && service.IsScriptTreeCacheDirName(info.Name())) {
				return filepath.SkipDir
			}
			return nil
		}
		if service.ShouldHideScriptTreePath(dir, path) {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(info.Name()))
		if !allowedExtensions[ext] && ext != "" {
			return nil
		}
		rel := relPath(path)
		files = append(files, map[string]interface{}{
			"path":  rel,
			"name":  info.Name(),
			"size":  info.Size(),
			"mtime": float64(info.ModTime().Unix()),
		})
		return nil
	})

	if files == nil {
		files = []map[string]interface{}{}
	}

	sort.Slice(files, func(i, j int) bool {
		return files[i]["path"].(string) < files[j]["path"].(string)
	})

	response.Success(c, gin.H{"data": files, "total": len(files)})
}

// 脚本树懒加载的两道阈值（#183）。只在调用方带 lazy=1 时生效：超过时，超出部分的目录节点不再递归，
// 只给出 lazy:true、children 为空，前端展开它时再用 GET /scripts/tree?path=<该目录>&lazy=1 去取（同样的阈值，一层层往下取）。
const (
	// scriptTreeLazyDirEntries：某个目录的直接子项超过这个数，它下面的子目录一律不递归展开
	scriptTreeLazyDirEntries = 500
	// scriptTreeLazyTotalNodes：这一次请求已经放进树里这么多节点后，后面遇到的目录都不再递归展开。
	// v4.0.3 从 5000 调到 2000：慢文件系统上每个节点都是一次系统调用，首屏少取一些（#183 补充反馈「还是很慢」）
	scriptTreeLazyTotalNodes = 2000
)

// Tree GET /scripts/tree[?path=<相对目录>][&lazy=1]：不带 path 时返回整棵树，带 path 时只返回这个目录下的子项。
// path 与其它脚本接口一样走 safePath 校验。
//
// 懒加载必须由调用方显式开启（lazy=1，网页端会带）：不认 lazy 字段的老调用方（Dumb-Panel-APP、MCP get_script_tree）
// 会把懒加载目录当成空目录，所以不带时保持 v4.0.1 的完整递归。#183 的超时主要靠跳过缓存目录解决，两种模式都跳。
func (h *ScriptHandler) Tree(c *gin.Context) {
	prefix := ""
	if raw := strings.TrimSpace(c.Query("path")); raw != "" {
		full, err := safePath(raw, true)
		if err != nil {
			response.BadRequest(c, err.Error())
			return
		}
		if info, statErr := os.Stat(full); statErr != nil || !info.IsDir() {
			response.BadRequest(c, "当前路径不是目录")
			return
		}
		// safePath 校验通过时这里不会再出错；节点 key 要用规范化后的相对路径，和整棵树里的 key 对得上
		prefix, _ = normalizeScriptRelativePath(raw)
	}
	// nodeCount 为 nil 表示不懒加载（见 buildTree）
	var nodeCount *int
	if c.Query("lazy") == "1" {
		nodeCount = new(int)
	}
	tree := buildTree(scriptsDir(), prefix, nodeCount)
	response.Success(c, gin.H{"data": tree})
}

// shouldSkipScriptTreeDir 同时覆盖脚本树与文件列表：
// 除了历史的异常目录（node_modules / __pycache__ / 隔离目录），
// 还要挡住 .git 这类版本控制元数据目录（其 config 里存着订阅注入的访问令牌）。
func shouldSkipScriptTreeDir(name string) bool {
	return service.ShouldHideScriptTreeEntryName(name)
}

// buildTree 列出 prefix 目录下的子项并递归展开子目录。nodeCount 是这一次请求已经放进树里的节点数，
// 与 scriptTreeLazyDirEntries 一起决定哪些目录不再递归、改成 lazy 节点（#183）；传 nil 就不懒加载、整棵递归。
func buildTree(baseDir, prefix string, nodeCount *int) []map[string]interface{} {
	dir := filepath.Join(baseDir, prefix)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return []map[string]interface{}{}
	}

	var dirs, files []map[string]interface{}

	sorted := make([]os.DirEntry, len(entries))
	copy(sorted, entries)
	sort.Slice(sorted, func(i, j int) bool {
		return strings.ToLower(sorted[i].Name()) < strings.ToLower(sorted[j].Name())
	})

	for _, entry := range sorted {
		name := entry.Name()

		rel := name
		if prefix != "" {
			rel = prefix + "/" + name
		}

		// 名字判定必须放在目录/文件分支之外：
		// submodule 与 worktree 场景下 .git 是一个内容为 "gitdir: ..." 的**文件**，
		// 只在 IsDir 分支里判断会让它整个漏出来。
		if shouldSkipScriptTreeDir(name) {
			continue
		}
		// .npm-cache 这类缓存目录只在展示里跳过（#183），见 service.IsScriptTreeCacheDirName
		if entry.IsDir() && service.IsScriptTreeCacheDirName(name) {
			continue
		}

		// 每个条目只做一次元数据系统调用（#183），按平台实现见 script_btime_*.go
		mtime, size, btime, hasBtime := scriptEntryStat(filepath.Join(dir, name), entry)

		if nodeCount != nil {
			*nodeCount++
		}
		var node map[string]interface{}
		if entry.IsDir() {
			node = map[string]interface{}{
				"key":    rel,
				"title":  name,
				"isLeaf": false,
				"type":   "directory",
				"mtime":  mtime,
			}
			// 本目录子项太多，或整棵树已经够大：不再往下递归，标成懒加载，前端展开时再取
			if nodeCount != nil && (len(sorted) > scriptTreeLazyDirEntries || *nodeCount >= scriptTreeLazyTotalNodes) {
				node["children"] = []map[string]interface{}{}
				node["lazy"] = true
			} else {
				node["children"] = buildTree(baseDir, rel, nodeCount)
			}
			dirs = append(dirs, node)
		} else {
			node = map[string]interface{}{
				"key":       rel,
				"title":     name,
				"isLeaf":    true,
				"type":      "file",
				"extension": strings.ToLower(filepath.Ext(name)),
				"size":      size,
				"mtime":     mtime,
			}
			files = append(files, node)
		}
		// 创建时间拿不到就不带 btime 键，前端按 mtime 兜底排序
		if hasBtime {
			node["btime"] = btime
		}
	}

	result := make([]map[string]interface{}, 0, len(dirs)+len(files))
	result = append(result, dirs...)
	result = append(result, files...)
	return result
}

func (h *ScriptHandler) GetContent(c *gin.Context) {
	path := c.Query("path")
	full, err := safePath(path, true)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	if info, statErr := os.Stat(full); statErr == nil && info.IsDir() {
		response.BadRequest(c, "当前路径是目录，不能作为脚本文件打开")
		return
	}

	ext := strings.ToLower(filepath.Ext(full))
	if binaryExtensions[ext] {
		data, err := os.ReadFile(full)
		if err != nil {
			response.InternalError(c, "读取文件失败")
			return
		}
		mimeType := mime.TypeByExtension(ext)
		if mimeType == "" {
			mimeType = "application/octet-stream"
		}
		response.Success(c, gin.H{
			"data": gin.H{
				"path":      path,
				"content":   base64.StdEncoding.EncodeToString(data),
				"binary":    true,
				"is_binary": true,
				"mime":      mimeType,
			},
		})
		return
	}

	data, err := os.ReadFile(full)
	if err != nil {
		response.InternalError(c, "读取文件失败")
		return
	}

	response.Success(c, gin.H{
		"data": gin.H{
			"path":      path,
			"content":   string(data),
			"binary":    false,
			"is_binary": false,
		},
	})
}

func (h *ScriptHandler) Download(c *gin.Context) {
	path := c.Query("path")
	full, err := safePath(path, true)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	c.Header("Cache-Control", "no-store, no-cache, must-revalidate")
	c.Header("Pragma", "no-cache")
	c.Header("Expires", "0")
	c.FileAttachment(full, filepath.Base(full))
}
