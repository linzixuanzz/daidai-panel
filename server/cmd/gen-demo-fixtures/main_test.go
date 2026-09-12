package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCommittedDemoFixturesMatchRegistry 让 CI 抓得住「改了注册表、忘了重新生成演示站 fixture」。
//
// web/src/demo/fixtures/ 下的两个 JSON 是在线演示站的数据源，由本生成器从服务端注册表导出后提交进仓库。
// 生成器不在任何构建链上，此前也没有任何一步比对：注册表加了字段而 fixture 没跟上时，
// 演示站只是静默少一个输入框，没人会发现（#123 给 wecom_app 加 proxy 时正好碰上这个缺口）。
//
// 做法：把生成器跑到 t.TempDir()，与仓库里提交的文件逐字比对。比对前把 CRLF 归一成 LF ——
// 仓库是 `* text=auto`，Windows 上开了 core.autocrlf 的检出是 CRLF，而生成器写的是 LF，二者内容相同。
// 不直接写回仓库目录：测试必须只读，不能有「跑一遍 go test 工作区就变了」的副作用。
func TestCommittedDemoFixturesMatchRegistry(t *testing.T) {
	generatedDir := t.TempDir()
	if err := run(generatedDir); err != nil {
		t.Fatalf("生成 fixture 失败: %v", err)
	}

	// resolveOutDir("") 从当前目录向上找仓库根；go test 的工作目录是本包目录，完整检出的仓库里一定找得到。
	committedDir, err := resolveOutDir("")
	if err != nil {
		t.Fatalf("定位仓库里的 fixture 目录失败: %v", err)
	}

	for _, name := range []string{notificationTypesFileName, configsFileName} {
		generated := readFixtureWithLF(t, filepath.Join(generatedDir, name))
		committed := readFixtureWithLF(t, filepath.Join(committedDir, name))
		if generated == committed {
			continue
		}
		line, want, got := firstDifferentLine(generated, committed)
		t.Errorf("%s 与服务端注册表不同步，第 %d 行起不同：\n  生成器输出: %s\n  仓库里的:   %s\n"+
			"修法：在 server/ 下运行 go run ./cmd/gen-demo-fixtures，再提交 web/src/demo/fixtures/ 的改动（不要手改 JSON）。",
			name, line, want, got)
	}
}

func readFixtureWithLF(t *testing.T, path string) string {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取 %s: %v", path, err)
	}
	return strings.ReplaceAll(string(data), "\r\n", "\n")
}

// firstDifferentLine 返回第一处不同的行号（从 1 开始）与两边该行的内容，只为让失败信息能直接定位。
// 某一边行数不够时，那一边返回「<文件已结束>」。
func firstDifferentLine(generated, committed string) (int, string, string) {
	left := strings.Split(generated, "\n")
	right := strings.Split(committed, "\n")
	for i := 0; i < len(left) || i < len(right); i++ {
		a, b := "<文件已结束>", "<文件已结束>"
		if i < len(left) {
			a = left[i]
		}
		if i < len(right) {
			b = right[i]
		}
		if a != b {
			return i + 1, strings.TrimSpace(a), strings.TrimSpace(b)
		}
	}
	return 0, "", ""
}
