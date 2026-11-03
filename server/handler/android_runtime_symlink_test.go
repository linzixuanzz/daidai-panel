package handler

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestAndroidSymlinkTargetWithin(t *testing.T) {
	root := filepath.Join(t.TempDir(), "node")
	bin := filepath.Join(root, "bin")
	lib := filepath.Join(root, "lib", "python3.12", "config")

	cases := []struct {
		name     string
		linkDir  string
		linkname string
		want     bool
	}{
		// 官方包里真实存在的包内相对链接：必须照常放行。
		{"node 的 npm", bin, "../lib/node_modules/npm/bin/npm-cli.js", true},
		{"python3 -> python3.12", bin, "python3.12", true},
		{"同目录带 ./", bin, "./python3.12", true},
		{"回到根目录下的库", lib, "../../libpython3.12.a", true},
		{"指向根目录本身", bin, "..", true},

		// 越界：绝对路径、往上跳出根目录、中途夹着 ..
		{"绝对路径", bin, "/etc/passwd", false},
		{"反斜杠开头", bin, `\etc\passwd`, false},
		{"往上跳出根目录", bin, "../../outside", false},
		{"往上跳很多层", bin, "../../../../../../etc", false},
		{"中途的 ..", bin, "sub/../../..", false},
		{"中途的 .. 哪怕字符串算回了根内", bin, "self/../python3.12", false},
		{"空目标", bin, "", false},
	}
	for _, item := range cases {
		if got := androidSymlinkTargetWithin(root, item.linkDir, item.linkname); got != item.want {
			t.Errorf("%s: androidSymlinkTargetWithin(%q) = %v，期望 %v", item.name, item.linkname, got, item.want)
		}
	}
}

func TestAndroidPathWithin(t *testing.T) {
	root := filepath.Join(t.TempDir(), "python")
	if !androidPathWithin(root, root) {
		t.Fatal("根目录本身应当算在内")
	}
	if !androidPathWithin(root, filepath.Join(root, "bin", "python3")) {
		t.Fatal("子路径应当算在内")
	}
	if androidPathWithin(root, filepath.Dir(root)) {
		t.Fatal("父目录不应算在内")
	}
	// 前缀相同的兄弟目录（python 与 python-evil）不能被当成子目录。
	if androidPathWithin(root, root+"-evil") {
		t.Fatal("同前缀的兄弟目录不应算在内")
	}
}

func TestAndroidRealPathWithinWalksUpToExistingAncestor(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "node")
	if err := os.MkdirAll(filepath.Join(root, "bin"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatalf("eval root: %v", err)
	}
	// 还不存在的深层路径：按最深的已存在祖先（root/bin）判断。
	if !androidRealPathWithin(realRoot, filepath.Join(root, "bin", "a", "b", "c")) {
		t.Fatal("根内尚不存在的路径应当放行")
	}
	if androidRealPathWithin(realRoot, filepath.Join(base, "elsewhere", "x")) {
		t.Fatal("根外的路径不应放行")
	}

	// 真实软链：指向根外的目录不能被当作父目录使用。Windows 非管理员建不了软链，跳过这一段。
	outside := filepath.Join(base, "outside")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatalf("mkdir outside: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "evil")); err != nil {
		t.Skipf("当前环境不支持创建软链，跳过: %v", err)
	}
	if androidRealPathWithin(realRoot, filepath.Join(root, "evil", "x")) {
		t.Fatal("经软链落到根外的路径不应放行")
	}
}

type androidTestTarEntry struct {
	name     string
	typeflag byte
	linkname string
	body     string
}

func buildAndroidTestTarGz(t *testing.T, entries []androidTestTarEntry) []byte {
	t.Helper()
	var raw bytes.Buffer
	gzipWriter := gzip.NewWriter(&raw)
	tarWriter := tar.NewWriter(gzipWriter)
	for _, entry := range entries {
		header := &tar.Header{Name: entry.name, Typeflag: entry.typeflag, Linkname: entry.linkname, Mode: 0o755}
		if entry.typeflag == tar.TypeReg {
			header.Size = int64(len(entry.body))
			header.Mode = 0o644
		}
		if err := tarWriter.WriteHeader(header); err != nil {
			t.Fatalf("write header %s: %v", entry.name, err)
		}
		if entry.typeflag == tar.TypeReg {
			if _, err := tarWriter.Write([]byte(entry.body)); err != nil {
				t.Fatalf("write body %s: %v", entry.name, err)
			}
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

// 端到端：恶意包里越界的软链被跳过，借软链往外写文件也写不出去；包内相对链接照常创建。
func TestAndroidRuntimeInstallSkipsEscapingSymlinks(t *testing.T) {
	base := t.TempDir()
	// 先确认能建软链，建不了（Windows 非管理员）就没法验证真实行为。
	if err := os.Symlink("probe-target", filepath.Join(base, "probe-link")); err != nil {
		t.Skipf("当前环境不支持创建软链，跳过: %v", err)
	}

	binDir := filepath.Join(base, "bin")
	outside := filepath.Join(base, "outside")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatalf("mkdir outside: %v", err)
	}
	t.Setenv("DAIDAI_MAGISK_MODULE", "1")
	t.Setenv("DAIDAI_ANDROID_RUNTIME_BIN_DIR", binDir)

	archive := buildAndroidTestTarGz(t, []androidTestTarEntry{
		{name: "node-v1/bin/", typeflag: tar.TypeDir},
		{name: "node-v1/lib/node_modules/npm/bin/npm-cli.js", typeflag: tar.TypeReg, body: "// npm"},
		{name: "node-v1/bin/npm", typeflag: tar.TypeSymlink, linkname: "../lib/node_modules/npm/bin/npm-cli.js"},
		// 指向根外的目录软链，随后试图借它写文件
		{name: "node-v1/escape", typeflag: tar.TypeSymlink, linkname: "../../outside"},
		{name: "node-v1/escape/pwned.txt", typeflag: tar.TypeReg, body: "pwned"},
		// 绝对路径软链
		{name: "node-v1/abs", typeflag: tar.TypeSymlink, linkname: outside},
		{name: "node-v1/abs/pwned-abs.txt", typeflag: tar.TypeReg, body: "pwned"},
		// 先建一个指向根目录自身的合法软链，再借它用「中途的 ..」跳出去
		{name: "node-v1/self", typeflag: tar.TypeSymlink, linkname: "."},
		{name: "node-v1/hop", typeflag: tar.TypeSymlink, linkname: "self/../../outside"},
	})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(archive)
	}))
	defer server.Close()

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.POST("/install", NewAndroidRuntimeHandler().Install)
	body := `{"name":"node","url":"` + server.URL + `/node.tar.gz","strip_components":1}`
	req := httptest.NewRequest(http.MethodPost, "/install", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	output := rec.Body.String()

	targetDir := filepath.Join(binDir, "node")
	if link, err := os.Readlink(filepath.Join(targetDir, "bin", "npm")); err != nil || link != "../lib/node_modules/npm/bin/npm-cli.js" {
		t.Fatalf("包内相对链接应当照常创建，实际 %q %v\n%s", link, err, output)
	}
	if data, err := os.ReadFile(filepath.Join(targetDir, "bin", "npm")); err != nil || string(data) != "// npm" {
		t.Fatalf("npm 链接应当能读到 npm-cli.js: %v", err)
	}

	for _, name := range []string{"escape", "abs", "hop"} {
		if info, err := os.Lstat(filepath.Join(targetDir, name)); err == nil && info.Mode()&os.ModeSymlink != 0 {
			t.Fatalf("越界软链 %s 不应被创建\n%s", name, output)
		}
	}
	entries, err := os.ReadDir(outside)
	if err != nil {
		t.Fatalf("read outside: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("目标目录外不应被写入任何文件，实际 %d 个\n%s", len(entries), output)
	}
	if !strings.Contains(output, "跳过") {
		t.Fatalf("越界条目应当给出跳过提示\n%s", output)
	}
}
