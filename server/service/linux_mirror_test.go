package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 下面三条是从 handler/deps_package_manager_test.go 原样搬来的（实现 v3.3.1 搬到了 service），断言一字未改。

// 后半段断言的是「已经是默认加速源时不再改写」，所以前半段显式传的这个源必须跟着默认源一起走。
// v3.3.2 默认源从阿里云换成腾讯云（issue #146），这里只改期望值，断言本身一字未动。
func TestRewriteAPTListLine(t *testing.T) {
	line := "deb [arch=amd64] http://archive.ubuntu.com/ubuntu jammy main restricted"
	updated, changed := rewriteAPTListLine(line, "ubuntu", "https://mirrors.cloud.tencent.com/ubuntu")
	if !changed {
		t.Fatalf("expected apt list line to change")
	}
	if updated != "deb [arch=amd64] https://mirrors.cloud.tencent.com/ubuntu jammy main restricted" {
		t.Fatalf("unexpected updated line: %s", updated)
	}

	defaulted, changed := rewriteAPTListLine(updated, "ubuntu", "")
	if changed {
		t.Fatalf("expected apt list line already using default accelerated mirror to remain unchanged")
	}
	if defaulted != "deb [arch=amd64] https://mirrors.cloud.tencent.com/ubuntu jammy main restricted" {
		t.Fatalf("unexpected defaulted line: %s", defaulted)
	}
}

func TestRewriteAPTSourcesContent(t *testing.T) {
	content := "Types: deb\nURIs: http://archive.ubuntu.com/ubuntu/\nSuites: noble noble-updates\nComponents: main restricted\n"
	updated, changed := rewriteAPTSourcesContent(content, "ubuntu", "https://mirrors.aliyun.com/ubuntu")
	if !changed {
		t.Fatalf("expected apt sources content to change")
	}
	if !strings.Contains(updated, "URIs: https://mirrors.aliyun.com/ubuntu") {
		t.Fatalf("unexpected rewritten sources content: %s", updated)
	}
}

// v3.3.2 默认源换成腾讯云（issue #146）：下面四处只改期望值。
// 最后一条刻意仍用阿里云 —— EffectiveLinuxMirror 不参与旧默认源迁移，
// 用户显式选的阿里云必须被原样保留（迁移只发生在 EnsureDefaultLinuxMirror 那一侧）。
func TestEffectiveLinuxMirrorFallsBackToDefaultAcceleratedMirror(t *testing.T) {
	apkManager := LinuxPackageManager{Name: "apk", Binary: "apk"}
	if got := EffectiveLinuxMirror(apkManager, "", ""); got != "https://mirrors.cloud.tencent.com/alpine" {
		t.Fatalf("expected apk default mirror, got %q", got)
	}
	if got := EffectiveLinuxMirror(apkManager, "", "https://dl-cdn.alpinelinux.org/alpine"); got != "https://mirrors.cloud.tencent.com/alpine" {
		t.Fatalf("expected apk official mirror to fall back to accelerated mirror, got %q", got)
	}

	aptManager := LinuxPackageManager{Name: "apt", Binary: "apt-get"}
	if got := EffectiveLinuxMirror(aptManager, "ubuntu", ""); got != "https://mirrors.cloud.tencent.com/ubuntu" {
		t.Fatalf("expected ubuntu default mirror, got %q", got)
	}
	if got := EffectiveLinuxMirror(aptManager, "debian", "http://deb.debian.org/debian"); got != "https://mirrors.cloud.tencent.com/debian" {
		t.Fatalf("expected debian official mirror to fall back to accelerated mirror, got %q", got)
	}
	if got := EffectiveLinuxMirror(aptManager, "ubuntu", "https://mirrors.aliyun.com/ubuntu"); got != "https://mirrors.aliyun.com/ubuntu" {
		t.Fatalf("expected custom ubuntu mirror to be preserved, got %q", got)
	}
}

// node:*-bookworm-slim（面板 Debian 镜像的底座）自带的 debian.sources 原文。
// 注意 URIs 写在 Suites 前面 —— 逐行读的老实现在处理 URIs 时还没读到 Suites，security 段永远认不出来。
const bookwormDebianSources = "Types: deb\n" +
	"# http://snapshot.debian.org/archive/debian/20230612T000000Z\n" +
	"URIs: http://deb.debian.org/debian\n" +
	"Suites: bookworm bookworm-updates\n" +
	"Components: main\n" +
	"Signed-By: /usr/share/keyrings/debian-archive-keyring.gpg\n" +
	"\n" +
	"Types: deb\n" +
	"# http://snapshot.debian.org/archive/debian-security/20230612T000000Z\n" +
	"URIs: http://deb.debian.org/debian-security\n" +
	"Suites: bookworm-security\n" +
	"Components: main\n" +
	"Signed-By: /usr/share/keyrings/debian-archive-keyring.gpg\n"

// debian-security 段必须改到 <镜像>/debian-security，不能跟主仓库一起改成 <镜像>/debian：
// 镜像站的 /debian 下没有 bookworm-security，apt-get update 会报 E:，安全更新源静默失效。
func TestRewriteAPTSourcesKeepsDebianSecuritySuffix(t *testing.T) {
	updated, changed := rewriteAPTSourcesContent(bookwormDebianSources, "debian", "")
	if !changed {
		t.Fatal("官方源应被改写成默认加速源")
	}
	if !strings.Contains(updated, "URIs: https://mirrors.cloud.tencent.com/debian\n") {
		t.Fatalf("主仓库应改到默认加速源的 /debian，实际：\n%s", updated)
	}
	if !strings.Contains(updated, "URIs: https://mirrors.cloud.tencent.com/debian-security\n") {
		t.Fatalf("security 段应改到默认加速源的 /debian-security，实际：\n%s", updated)
	}
	// 其余行（注释、Suites、Signed-By、段间空行）一个字节都不能动。
	for _, keep := range []string{
		"# http://snapshot.debian.org/archive/debian-security/20230612T000000Z\n",
		"Suites: bookworm-security\n",
		"Signed-By: /usr/share/keyrings/debian-archive-keyring.gpg\n\nTypes: deb\n",
	} {
		if !strings.Contains(updated, keep) {
			t.Fatalf("改写不应碰到 %q，实际：\n%s", keep, updated)
		}
	}

	// 幂等：再跑一遍不应再有改动（EnsureDefaultLinuxMirror 每装一个包都会走一次）。
	// 这里显式传的必须与上一步用默认源改出来的结果一致，v3.3.2 起是腾讯云（issue #146）。
	if again, changedAgain := rewriteAPTSourcesContent(updated, "debian", "https://mirrors.cloud.tencent.com/debian"); changedAgain || again != updated {
		t.Fatalf("已是目标镜像时不应再改写，实际：\n%s", again)
	}
}

// 用户在页面上指定了别的镜像（例如清华）时，security 段同样要跟到 <镜像>-security。
func TestRewriteAPTSourcesSecurityFollowsRequestedMirror(t *testing.T) {
	updated, _ := rewriteAPTSourcesContent(bookwormDebianSources, "debian", "https://mirrors.tuna.tsinghua.edu.cn/debian/")
	if !strings.Contains(updated, "URIs: https://mirrors.tuna.tsinghua.edu.cn/debian\n") {
		t.Fatalf("主仓库应改到清华 /debian，实际：\n%s", updated)
	}
	if !strings.Contains(updated, "URIs: https://mirrors.tuna.tsinghua.edu.cn/debian-security\n") {
		t.Fatalf("security 段应改到清华 /debian-security，实际：\n%s", updated)
	}
}

// 单行 .list 格式（Debian 11 及更早的 sources.list）走同一套判据。
func TestRewriteAPTListLineKeepsDebianSecuritySuffix(t *testing.T) {
	cases := []struct {
		name string
		line string
		want string
	}{
		{
			name: "路径以 -security 结尾",
			line: "deb http://security.debian.org/debian-security bullseye-security main",
			want: "deb https://mirrors.cloud.tencent.com/debian-security bullseye-security main",
		},
		{
			// buster 及更早的写法：路径不带 -security，靠 Suites 的 /updates 认出来。
			name: "buster 的 /updates 写法",
			line: "deb http://security.debian.org buster/updates main",
			want: "deb https://mirrors.cloud.tencent.com/debian-security buster/updates main",
		},
		{
			name: "普通主仓库不加后缀",
			line: "deb http://deb.debian.org/debian bullseye-updates main",
			want: "deb https://mirrors.cloud.tencent.com/debian bullseye-updates main",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, changed := rewriteAPTListLine(tc.line, "debian", "")
			if !changed || got != tc.want {
				t.Fatalf("期望 %q，实际 %q（changed=%v）", tc.want, got, changed)
			}
		})
	}
}

// Ubuntu 的 noble-security 就在 /ubuntu 下（官方 security.ubuntu.com/ubuntu），绝不能加 -security 后缀，
// 否则会指到一个不存在的 ubuntu-security 路径。Suites 这条判据因此只对 Debian 生效。
func TestRewriteAPTSourcesLeavesUbuntuSecurityUnderUbuntu(t *testing.T) {
	content := "Types: deb\nURIs: http://security.ubuntu.com/ubuntu/\nSuites: noble-security\nComponents: main restricted\n"
	updated, changed := rewriteAPTSourcesContent(content, "ubuntu", "")
	if !changed {
		t.Fatal("官方 security 源应被改写成默认加速源")
	}
	if !strings.Contains(updated, "URIs: https://mirrors.cloud.tencent.com/ubuntu\n") {
		t.Fatalf("Ubuntu 的 security 段应仍指向 /ubuntu，实际：\n%s", updated)
	}
	if strings.Contains(updated, "ubuntu-security") {
		t.Fatalf("Ubuntu 不应被加上 -security 后缀，实际：\n%s", updated)
	}
}

// #184、#185：Debian 镜像里 nginx.org 那条（Dockerfile.debian）以前也被换成 <镜像>/debian，
// 与 debian.sources 同 URI、同 suite 但 Signed-By 不同，apt-get update 直接报 Conflicting values。
// 第三方源一律不动，发行版主仓库与 security 仓库照常改写。
func TestRewriteAPTListLineSkipsThirdPartySources(t *testing.T) {
	keep := []string{
		"deb [signed-by=/usr/share/keyrings/nginx-archive-keyring.asc] https://nginx.org/packages/debian bookworm nginx",
		"deb [arch=amd64 signed-by=/etc/apt/keyrings/docker.asc] https://download.docker.com/linux/debian bookworm stable",
		"deb [signed-by=/usr/share/keyrings/nodesource.gpg] https://deb.nodesource.com/node_20.x nodistro main",
		// 没有 signed-by 的老写法：主机是 nginx.org 也一律算第三方
		"deb https://nginx.org/packages/debian bookworm nginx",
		// 单段路径但 signed-by 指向第三方 keyring
		"deb [signed-by=/usr/share/keyrings/vendor.gpg] https://vendor.example.com/debian bookworm main",
	}
	for _, line := range keep {
		if got, changed := rewriteAPTListLine(line, "debian", ""); changed || got != line {
			t.Fatalf("第三方源不应被改写：%q → %q", line, got)
		}
		if mirror := extractMirrorFromAPTListLine(line); mirror != "" {
			t.Fatalf("读当前镜像源时应跳过第三方条目 %q，实际读到 %q", line, mirror)
		}
	}

	rewrite := []struct{ line, want string }{
		{
			"deb [signed-by=/usr/share/keyrings/debian-archive-keyring.gpg] http://deb.debian.org/debian bookworm main",
			"deb [signed-by=/usr/share/keyrings/debian-archive-keyring.gpg] https://mirrors.cloud.tencent.com/debian bookworm main",
		},
		{
			"deb http://deb.debian.org/debian-security bookworm-security main",
			"deb https://mirrors.cloud.tencent.com/debian-security bookworm-security main",
		},
	}
	for _, tc := range rewrite {
		if got, changed := rewriteAPTListLine(tc.line, "debian", ""); !changed || got != tc.want {
			t.Fatalf("发行版条目应照常改写：期望 %q，实际 %q（changed=%v）", tc.want, got, changed)
		}
	}
}

// 整个文件的读写：nginx.list 与 sources.list 混在一起时，只读到 / 只改写发行版条目。
func TestAPTListContentWithNginxLine(t *testing.T) {
	content := "deb http://deb.debian.org/debian bookworm main\n" +
		"deb [signed-by=/usr/share/keyrings/nginx-archive-keyring.asc] https://nginx.org/packages/debian bookworm nginx\n"
	updated, changed := rewriteAPTListContent(content, "debian", "")
	if !changed {
		t.Fatal("Debian 主仓库应被改写")
	}
	want := "deb https://mirrors.cloud.tencent.com/debian bookworm main\n" +
		"deb [signed-by=/usr/share/keyrings/nginx-archive-keyring.asc] https://nginx.org/packages/debian bookworm nginx\n"
	if updated != want {
		t.Fatalf("期望：\n%s\n实际：\n%s", want, updated)
	}
	// 只有 nginx 一行的 nginx.list：读不到镜像源（不能把 nginx.org 当成当前 Debian 源）
	if mirror := extractMirrorFromAPTList("deb [signed-by=/usr/share/keyrings/nginx-archive-keyring.asc] https://nginx.org/packages/debian bookworm nginx\n"); mirror != "" {
		t.Fatalf("nginx.list 不应被当成当前镜像源，实际 %q", mirror)
	}
}

// 已经被 v3.4.5 ~ v4.0.1 改坏的机器：nginx 那行的 URI 被换成了加速源，要改回 nginx.org。
func TestRepairBrokenNginxAPTContent(t *testing.T) {
	broken := "deb [signed-by=/usr/share/keyrings/nginx-archive-keyring.asc] https://mirrors.cloud.tencent.com/debian bookworm nginx\n"
	want := "deb [signed-by=/usr/share/keyrings/nginx-archive-keyring.asc] https://nginx.org/packages/debian bookworm nginx\n"
	repaired, changed := repairBrokenNginxAPTContent(broken, "debian", false)
	if !changed || repaired != want {
		t.Fatalf("被改坏的 nginx 行应修回 nginx.org：期望 %q，实际 %q（changed=%v）", want, repaired, changed)
	}
	// 幂等：已经正常的不再改
	if again, changedAgain := repairBrokenNginxAPTContent(repaired, "debian", false); changedAgain || again != repaired {
		t.Fatalf("正常的 nginx 行不应再被改写，实际 %q", again)
	}
	// 修完再走一遍镜像改写，nginx 行保持不动
	if after, _ := rewriteAPTListContent(repaired, "debian", ""); after != want {
		t.Fatalf("修回后的 nginx 行不应再被换源，实际 %q", after)
	}
	// 只认 nginx-archive-keyring：别的第三方源即使 URI 指到了镜像站也不去猜原地址
	other := "deb [signed-by=/usr/share/keyrings/vendor.gpg] https://mirrors.cloud.tencent.com/debian bookworm main\n"
	if got, changed := repairBrokenNginxAPTContent(other, "debian", false); changed || got != other {
		t.Fatalf("不是 nginx 的条目不应被修复逻辑碰到，实际 %q", got)
	}
	// Debian 自己的条目也不碰
	if got, changed := repairBrokenNginxAPTContent(bookwormDebianSources, "debian", true); changed || got != bookwormDebianSources {
		t.Fatalf("debian.sources 不应被修复逻辑碰到，实际：\n%s", got)
	}

	// 只修旧版改坏的确切形态，下面两种都不碰：
	// 用户自配的 nginx 镜像（路径不是单段 /debian）、Ubuntu 主机上的 nginx 源（发行版不是 debian）
	userMirror := "deb [signed-by=/usr/share/keyrings/nginx-archive-keyring.asc] https://mirrors.ustc.edu.cn/nginx/debian bookworm nginx\n"
	if got, changed := repairBrokenNginxAPTContent(userMirror, "debian", false); changed || got != userMirror {
		t.Fatalf("用户自配的 nginx 镜像不应被改回 nginx.org，实际 %q", got)
	}
	ubuntuNginx := "deb [signed-by=/usr/share/keyrings/nginx-archive-keyring.asc] https://mirrors.cloud.tencent.com/ubuntu noble nginx\n"
	if got, changed := repairBrokenNginxAPTContent(ubuntuNginx, "ubuntu", false); changed || got != ubuntuNginx {
		t.Fatalf("Ubuntu 主机上的 nginx 源不应被改成 nginx.org/packages/debian，实际 %q", got)
	}
	// 同一行放到 ubuntu 主机上（发行版不是 debian）也不碰
	if got, changed := repairBrokenNginxAPTContent(broken, "ubuntu", false); changed || got != broken {
		t.Fatalf("非 Debian 主机不应走 nginx 修复，实际 %q", got)
	}

	// deb822 形态的坏条目同样修回
	brokenSources := "Types: deb\nURIs: https://mirrors.cloud.tencent.com/debian\nSuites: bookworm\nComponents: nginx\nSigned-By: /usr/share/keyrings/nginx-archive-keyring.asc\n"
	fixedSources, changed := repairBrokenNginxAPTContent(brokenSources, "debian", true)
	if !changed || !strings.Contains(fixedSources, "URIs: https://nginx.org/packages/debian\n") {
		t.Fatalf("deb822 的坏 nginx 段应修回，实际：\n%s", fixedSources)
	}
}

// deb822：Signed-By 指向第三方 keyring 的段整段不动，同一文件里的 Debian 段照常改写；读当前源时也跳过第三方段。
func TestRewriteAPTSourcesSkipsThirdPartySignedBy(t *testing.T) {
	thirdParty := "Types: deb\n" +
		"URIs: https://nginx.org/packages/debian\n" +
		"Suites: bookworm\n" +
		"Components: nginx\n" +
		"Signed-By: /usr/share/keyrings/nginx-archive-keyring.asc\n" +
		"\n" +
		"Types: deb\n" +
		"URIs: https://vendor.example.com/debian\n" +
		"Suites: bookworm\n" +
		"Components: main\n" +
		"Signed-By: /usr/share/keyrings/vendor.gpg\n"
	if got, changed := rewriteAPTSourcesContent(thirdParty, "debian", ""); changed || got != thirdParty {
		t.Fatalf("第三方 deb822 段不应被改写，实际：\n%s", got)
	}
	if mirror := extractMirrorFromAPTSources(thirdParty); mirror != "" {
		t.Fatalf("读当前镜像源时应跳过第三方段，实际 %q", mirror)
	}

	mixed := thirdParty + "\n" + bookwormDebianSources
	updated, changed := rewriteAPTSourcesContent(mixed, "debian", "")
	if !changed {
		t.Fatal("同一文件里的 Debian 段应照常改写")
	}
	if !strings.HasPrefix(updated, thirdParty) {
		t.Fatalf("第三方段一个字节都不能动，实际：\n%s", updated)
	}
	if !strings.Contains(updated, "URIs: https://mirrors.cloud.tencent.com/debian\n") ||
		!strings.Contains(updated, "URIs: https://mirrors.cloud.tencent.com/debian-security\n") {
		t.Fatalf("Debian 主仓库与 security 段应改到加速源，实际：\n%s", updated)
	}
	if mirror := extractMirrorFromAPTSources(mixed); mirror != "http://deb.debian.org/debian" {
		t.Fatalf("读当前镜像源应取第一个 Debian 段，实际 %q", mirror)
	}
}

// useTempAPTConfigDir 把 apt 配置目录换成临时目录，按 files 写好源文件，测试结束后还原。
func useTempAPTConfigDir(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "sources.list.d"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	previous := aptConfigDir
	aptConfigDir = dir
	t.Cleanup(func() { aptConfigDir = previous })
	return dir
}

// 多段路径的自定义镜像（Nexus 仓库代理）是正常的发行版源：用户 SetLinuxMirror 设上之后，
// EnsureDefaultLinuxMirror 必须认得它、不报错、也不改（v4.0.2 一度按路径形态把它判成第三方，所有 Linux 依赖都装不上）。
func TestEnsureDefaultLinuxMirrorKeepsMultiSegmentCustomMirror(t *testing.T) {
	apt := LinuxPackageManager{Name: "apt", Binary: "apt-get"}
	dir := useTempAPTConfigDir(t, map[string]string{
		"sources.list.d/debian.sources": bookwormDebianSources,
		"sources.list.d/nginx.list":     "deb [signed-by=/usr/share/keyrings/nginx-archive-keyring.asc] https://nginx.org/packages/debian bookworm nginx\n",
	})

	if _, err := SetLinuxMirror(apt, "debian", "https://nexus.corp/repository/debian"); err != nil {
		t.Fatalf("设置多段路径的自定义镜像不应报错：%v", err)
	}
	before, _ := os.ReadFile(filepath.Join(dir, "sources.list.d", "debian.sources"))
	if !strings.Contains(string(before), "URIs: https://nexus.corp/repository/debian\n") {
		t.Fatalf("自定义镜像应写进 debian.sources，实际：\n%s", before)
	}

	changed, err := EnsureDefaultLinuxMirror(apt, "debian")
	if err != nil || changed {
		t.Fatalf("用户设过的自定义镜像应原样保留：changed=%v err=%v", changed, err)
	}
	after, _ := os.ReadFile(filepath.Join(dir, "sources.list.d", "debian.sources"))
	if string(after) != string(before) {
		t.Fatalf("EnsureDefaultLinuxMirror 不应改动自定义镜像，实际：\n%s", after)
	}
	nginx, _ := os.ReadFile(filepath.Join(dir, "sources.list.d", "nginx.list"))
	if !strings.Contains(string(nginx), "https://nginx.org/packages/debian") {
		t.Fatalf("nginx.org 那行一直不应被碰，实际 %q", nginx)
	}
}

// Kali 这类衍生发行版：源没有 signed-by、路径是 /kali，当作用户自己的源，什么都不做、也不报错。
func TestEnsureDefaultLinuxMirrorLeavesKaliAlone(t *testing.T) {
	apt := LinuxPackageManager{Name: "apt", Binary: "apt-get"}
	kali := "deb http://http.kali.org/kali kali-rolling main contrib non-free non-free-firmware\n"
	dir := useTempAPTConfigDir(t, map[string]string{"sources.list": kali})

	changed, err := EnsureDefaultLinuxMirror(apt, "debian")
	if err != nil || changed {
		t.Fatalf("Kali 源应原样保留：changed=%v err=%v", changed, err)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "sources.list")); string(got) != kali {
		t.Fatalf("Kali 源不应被改写，实际 %q", got)
	}
}
