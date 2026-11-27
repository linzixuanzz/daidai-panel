package handler

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// 脚本树的目录与文件节点都要带 mtime；Windows 一定能拿到创建时间，必须带 btime。
func TestBuildTreeIncludesMtimeAndBtime(t *testing.T) {
	base := t.TempDir()
	if err := os.MkdirAll(filepath.Join(base, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "a.py"), []byte("print(1)"), 0o644); err != nil {
		t.Fatal(err)
	}

	tree := buildTree(base, "", new(int))
	if len(tree) != 2 {
		t.Fatalf("期望 2 个节点，实际 %d", len(tree))
	}
	for _, node := range tree {
		mtime, ok := node["mtime"].(float64)
		if !ok || mtime <= 0 {
			t.Fatalf("节点 %v 缺少 mtime", node["key"])
		}
		btime, has := node["btime"]
		if runtime.GOOS == "windows" && !has {
			t.Fatalf("Windows 下节点 %v 应带 btime", node["key"])
		}
		if has {
			if v, ok := btime.(float64); !ok || v <= 0 {
				t.Fatalf("节点 %v 的 btime 非法：%v", node["key"], btime)
			}
		}
	}
}

// #183 把每个条目的元数据改成一次系统调用（Linux 走 statx）后，mtime / size 必须与 lstat 的口径完全一致。
func TestBuildTreeEntryStatMatchesLstat(t *testing.T) {
	base := t.TempDir()
	if err := os.MkdirAll(filepath.Join(base, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "a.py"), []byte("print(12345)"), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, node := range buildTree(base, "", nil) {
		info, err := os.Lstat(filepath.Join(base, node["key"].(string)))
		if err != nil {
			t.Fatal(err)
		}
		if got, want := node["mtime"], float64(info.ModTime().Unix()); got != want {
			t.Fatalf("节点 %v 的 mtime 应为 %v，实际 %v", node["key"], want, got)
		}
		if node["type"] == "file" {
			if got, want := node["size"], info.Size(); got != want {
				t.Fatalf("节点 %v 的 size 应为 %v，实际 %v", node["key"], want, got)
			}
		}
	}
}
