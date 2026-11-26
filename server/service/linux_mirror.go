package service

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"strings"
)

// 本文件是 Linux 系统包镜像源的读写与默认加速逻辑，v3.3.1 从 handler/deps_package_manager.go 整体搬来（#142）。
//
// 搬迁原因：容器重建后的自动重装（backup_runtime.go 的 reinstallDependency）在 service 层，
// 拿不到 handler 里的 ensureDefaultLinuxMirror，只能传 nil，于是重建后的重装一律走 deb.debian.org，
// 国内要慢很多。现在网页安装与重启重装共用这里的 EnsureDefaultLinuxMirror，handler 只留薄封装。
//
// 除了下面单独注明的 debian-security 修正，其余行为与搬迁前逐行一致。

// ReadLinuxMirror 读当前系统包管理器正在用的镜像源；不支持镜像设置的包管理器返回空串。
func ReadLinuxMirror(manager LinuxPackageManager) (string, error) {
	switch manager.Name {
	case "apk":
		return readAPKMirror()
	case "apt":
		return readAPTMirror()
	default:
		return "", nil
	}
}

// SetLinuxMirror 把系统包镜像源改成 mirror；mirror 为空或是官方源时改成默认加速源。
//
// 第一个返回值表示「磁盘上的源文件真的被改写了」。v3.3.2 起把它一路透出来（issue #146）：
// 换完源必须让 apt 索引作废，否则下一次安装仍拿旧源的索引，报 E: Unable to locate package。
func SetLinuxMirror(manager LinuxPackageManager, distribution, mirror string) (bool, error) {
	switch manager.Name {
	case "apk":
		return writeAPKMirror(EffectiveLinuxMirror(manager, distribution, mirror))
	case "apt":
		return writeAPTMirror(distribution, EffectiveLinuxMirror(manager, distribution, mirror))
	default:
		return false, fmt.Errorf("当前系统使用 %s，暂不支持镜像设置", manager.Binary)
	}
}

// EnsureDefaultLinuxMirror 在装系统包之前调用：当前源为空或是官方源时换成默认加速源，
// 用户自己设过的源一律不动。返回值同 SetLinuxMirror，表示源文件是否真被改写。
func EnsureDefaultLinuxMirror(manager LinuxPackageManager, distribution string) (bool, error) {
	switch manager.Name {
	case "apk", "apt":
	default:
		return false, nil
	}

	// v4.0.2（#184、#185）：先修回被旧版改坏的 nginx.org 源。坏掉的机器上 debian.sources 已经是加速源，
	// 下面会直接 return 不再改写，所以修复必须放在这里先做，否则 apt-get update 会一直报
	// 「Conflicting values set for option Signed-By」，所有 Linux 依赖都装不上。
	repaired := false
	if manager.Name == "apt" {
		var err error
		repaired, err = repairAPTSourceFiles(distribution)
		if err != nil {
			return false, err
		}
	}

	current, err := ReadLinuxMirror(manager)
	if err != nil {
		return repaired, err
	}
	// 第三个条件是 v3.3.2 的旧默认源一次性迁移（issue #146）：存量用户的 sources.list 里存的是上一版
	// 默认的阿里云，而阿里云不是官方源，原来的判定会把它当成「用户自己选的」而永远不动。
	// 迁移由 legacyDefaultMirrorMigrationPending() 兜住一次性，用户显式保存过镜像源之后就整体失效。
	if current != "" &&
		!isOfficialLinuxMirror(manager, distribution, current) &&
		!(isLegacyDefaultLinuxMirror(manager, distribution, current) && legacyDefaultMirrorMigrationPending()) {
		return repaired, nil
	}

	changed, err := SetLinuxMirror(manager, distribution, "")
	return changed || repaired, err
}

// EffectiveLinuxMirror 返回实际生效的镜像源：空或官方源时回落到默认加速源。
func EffectiveLinuxMirror(manager LinuxPackageManager, distribution, current string) string {
	current = strings.TrimRight(strings.TrimSpace(current), "/")
	if current == "" || isOfficialLinuxMirror(manager, distribution, current) {
		return strings.TrimRight(defaultLinuxMirror(manager, distribution), "/")
	}
	return current
}

func readAPKMirror() (string, error) {
	data, err := os.ReadFile("/etc/apk/repositories")
	if err != nil {
		return "", err
	}

	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "/v", 2)
		if len(parts) > 0 {
			return strings.TrimRight(parts[0], "/"), nil
		}
	}

	return "", nil
}

// writeAPKMirror 的返回值与 writeAPTMirror 对齐（issue #146）：只有内容真变了才算 changed。
// apk 这边原来是无条件整文件覆写，拿不到「有没有变」这个信号，对齐之后调用方不用区分包管理器。
func writeAPKMirror(mirror string) (bool, error) {
	mirror = strings.TrimSpace(mirror)
	if mirror == "" {
		mirror = defaultLinuxMirror(LinuxPackageManager{Name: "apk", Binary: "apk"}, "")
	}
	if !isHTTPMirror(mirror) {
		return false, errors.New("Linux 镜像源必须以 http:// 或 https:// 开头")
	}

	mirror = strings.TrimRight(mirror, "/")
	out, err := exec.Command("cat", "/etc/alpine-release").Output()
	ver := "3.19"
	if err == nil {
		parts := strings.Split(strings.TrimSpace(string(out)), ".")
		if len(parts) >= 2 {
			ver = parts[0] + "." + parts[1]
		}
	}

	content := fmt.Sprintf("%s/v%s/main\n%s/v%s/community\n", mirror, ver, mirror, ver)
	if existing, readErr := os.ReadFile("/etc/apk/repositories"); readErr == nil && string(existing) == content {
		return false, nil
	}
	if err := os.WriteFile("/etc/apk/repositories", []byte(content), 0o644); err != nil {
		return false, err
	}
	return true, nil
}

func readAPTMirror() (string, error) {
	files, err := listAPTSourceFiles()
	if err != nil {
		return "", err
	}

	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			continue
		}

		var mirror string
		if strings.HasSuffix(file, ".sources") {
			mirror = extractMirrorFromAPTSources(string(data))
		} else {
			mirror = extractMirrorFromAPTList(string(data))
		}
		if mirror != "" {
			return strings.TrimRight(mirror, "/"), nil
		}
	}

	return "", nil
}

// writeAPTMirror 把 apt 源改写成 mirror，返回「是否真的改写了文件」。
//
// v3.3.2 起把「一个条目都没识别出来」与「识别出来了但已经是目标源」分开（issue #146）：
// 前者是真异常，后者是成功。原来两种情况统一报「未找到可更新的 apt 软件源条目」，
// 于是用户只改 pip、Linux 那栏原样提交时必然拿到 400，而 pip / npm 其实已经写进去了。
func writeAPTMirror(distribution, mirror string) (bool, error) {
	files, err := listAPTSourceFiles()
	if err != nil {
		return false, err
	}
	if len(files) == 0 {
		return false, errors.New("未找到 apt 软件源配置文件")
	}
	if mirror != "" && !isHTTPMirror(mirror) {
		return false, errors.New("Linux 镜像源必须以 http:// 或 https:// 开头")
	}

	changedAny := false
	matchedAny := false
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			return false, err
		}

		var (
			updated string
			changed bool
		)
		isSources := strings.HasSuffix(file, ".sources")
		// 先修回被旧版改坏的 nginx.org 源（#184、#185），再改写发行版条目
		content, repaired := repairBrokenNginxAPTContent(string(data), distribution, isSources)
		if isSources {
			// 能提取出 URIs / deb 行就说明这个文件里有面板认得的条目，与它是否需要改写无关。
			matchedAny = matchedAny || extractMirrorFromAPTSources(content) != ""
			updated, changed = rewriteAPTSourcesContent(content, distribution, mirror)
		} else {
			matchedAny = matchedAny || extractMirrorFromAPTList(content) != ""
			updated, changed = rewriteAPTListContent(content, distribution, mirror)
		}
		changed = changed || repaired

		if !changed {
			continue
		}

		if err := os.WriteFile(file, []byte(updated), 0o644); err != nil {
			return false, err
		}
		changedAny = true
	}

	if !matchedAny {
		return false, errors.New("未找到可更新的 apt 软件源条目")
	}

	return changedAny, nil
}

// aptConfigDir 是 apt 源配置所在目录，单测里换成临时目录，才能真的跑一遍 Set / EnsureDefault 的读写。
var aptConfigDir = "/etc/apt"

func listAPTSourceFiles() ([]string, error) {
	files := []string{}

	mainList := filepath.Join(aptConfigDir, "sources.list")
	if _, err := os.Stat(mainList); err == nil {
		files = append(files, mainList)
	}

	patterns := []string{
		filepath.Join(aptConfigDir, "sources.list.d", "*.list"),
		filepath.Join(aptConfigDir, "sources.list.d", "*.sources"),
	}
	for _, pattern := range patterns {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			return nil, err
		}
		files = append(files, matches...)
	}

	slices.Sort(files)
	return files, nil
}

func extractMirrorFromAPTList(content string) string {
	for _, line := range strings.Split(content, "\n") {
		if mirror := extractMirrorFromAPTListLine(line); mirror != "" {
			return mirror
		}
	}
	return ""
}

func extractMirrorFromAPTListLine(line string) string {
	fields := parseAPTListLineFields(line)
	if len(fields) == 0 {
		return ""
	}
	uriIndex := aptURIFieldIndex(fields)
	if uriIndex < 0 || uriIndex >= len(fields) {
		return ""
	}
	// 第三方源（nginx.org、docker 等）不算「当前镜像源」，读的时候同样跳过（#184、#185）
	if !isDistroAPTEntry(fields[uriIndex], aptListSignedBy(fields, uriIndex)) {
		return ""
	}
	return fields[uriIndex]
}

func parseAPTListLineFields(line string) []string {
	content := strings.TrimSpace(strings.SplitN(line, "#", 2)[0])
	if content == "" {
		return nil
	}

	fields := strings.Fields(content)
	if len(fields) == 0 {
		return nil
	}
	if fields[0] != "deb" && fields[0] != "deb-src" {
		return nil
	}

	return fields
}

func aptURIFieldIndex(fields []string) int {
	if len(fields) < 3 {
		return -1
	}

	idx := 1
	if strings.HasPrefix(fields[idx], "[") {
		for idx < len(fields) && !strings.HasSuffix(fields[idx], "]") {
			idx++
		}
		idx++
	}
	if idx >= len(fields) {
		return -1
	}
	return idx
}

// extractMirrorFromAPTSources 取 deb822 文件里第一个发行版条目的 URIs。
// v4.0.2 起按段（空行分隔）看 Signed-By 与主机，第三方段跳过（#184、#185）。
func extractMirrorFromAPTSources(content string) string {
	lines := strings.Split(content, "\n")
	for start := 0; start < len(lines); {
		end := start
		for end < len(lines) && strings.TrimSpace(lines[end]) != "" {
			end++
		}
		uris := aptSourcesField(lines[start:end], "uris")
		if uris != "" && isDistroAPTEntry(uris, aptSourcesField(lines[start:end], "signed-by")) {
			return uris
		}
		start = end + 1
	}
	return ""
}

// aptSourcesField 取 deb822 一段里某个字段（不区分大小写）的值，没有时返回空串。
// Signed-By 可能是多行的内联公钥，这里只取第一行，足够判断「是不是官方 keyring 路径」。
func aptSourcesField(stanza []string, name string) string {
	for i, line := range stanza {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(strings.ToLower(trimmed), name+":") {
			continue
		}
		value := strings.TrimSpace(trimmed[len(name)+1:])
		// 内联公钥的写法是「Signed-By:」后面留空、下一行缩进开始写 -----BEGIN PGP…，取续行的第一行
		if value == "" && i+1 < len(stanza) && (strings.HasPrefix(stanza[i+1], " ") || strings.HasPrefix(stanza[i+1], "\t")) {
			value = strings.TrimSpace(stanza[i+1])
		}
		return value
	}
	return ""
}

// aptListSignedBy 从单行格式的 `[arch=amd64 signed-by=/path]` 选项里取 signed-by 的值。
func aptListSignedBy(fields []string, uriIndex int) string {
	options := strings.Trim(strings.TrimSpace(strings.Join(fields[1:uriIndex], " ")), "[]")
	for _, option := range strings.Fields(options) {
		if strings.HasPrefix(strings.ToLower(option), "signed-by=") {
			return strings.TrimSpace(option[len("signed-by="):])
		}
	}
	return ""
}

// isDistroAPTEntry 判断一条 apt 源是不是发行版自己的仓库（Debian / Ubuntu 主仓库、security 仓库），
// 只有这类条目才允许被换成镜像站（v4.0.2，#184、#185）。
//
// 以前所有 deb 行都会被改写：Debian 镜像里 nginx.org 那条（Dockerfile.debian）被换成 <镜像>/debian 后，
// 与 debian.sources 同 URI、同 suite 但 Signed-By 不同，apt-get update 直接报
// 「Conflicting values set for option Signed-By」，所有 Linux 依赖都装不上。现在只按两条判第三方：
//  1. 带了 signed-by 时，必须指向 debian / ubuntu 官方 keyring（debian-archive-keyring.gpg 这类）；
//     指向别的 keyring、内联公钥、指纹的，一律视为第三方；
//  2. 主机是 nginx.org 的一律视为第三方（老写法可能没带 signed-by）。
//
// 刻意不看 URI 路径形态：多段路径的自定义镜像（https://nexus.corp/repository/debian、
// apt-cacher 的 http://host:3142/deb.debian.org/debian）、Kali（http.kali.org/kali）、Deepin 这类系统
// 都是正常的发行版源，按路径判会被当成第三方，读出空串后 writeAPTMirror 报「未找到可更新的 apt 软件源条目」。
// 没有 signed-by 的条目回到 v4.0.1 的行为，当作发行版条目（官方空路径的 security 源也就自然照旧）。
func isDistroAPTEntry(uri, signedBy string) bool {
	if parsed, err := url.Parse(strings.TrimSpace(uri)); err == nil && strings.EqualFold(parsed.Hostname(), "nginx.org") {
		return false
	}
	for _, item := range strings.FieldsFunc(strings.TrimSpace(signedBy), func(r rune) bool { return r == ',' || r == ' ' || r == '\t' }) {
		base := strings.ToLower(path.Base(item))
		if !strings.HasPrefix(base, "debian-archive-") &&
			!strings.HasPrefix(base, "debian-ports-archive-") &&
			!strings.HasPrefix(base, "ubuntu-archive-") {
			return false
		}
	}
	return true
}

// nginxOfficialAPTURI 是 Dockerfile.debian 里 nginx.org 源的原始地址。
const nginxOfficialAPTURI = "https://nginx.org/packages/debian"

// nginxImageKeyring 是 Dockerfile.debian 给 nginx 源用的 keyring 路径（.asc），用来认出「我们镜像里的那一条」。
const nginxImageKeyring = "/usr/share/keyrings/nginx-archive-keyring.asc"

// isBrokenNginxAPTEntry 判断是不是被 v3.4.5 ~ v4.0.1 改坏的 nginx 源。只认旧版改坏的确切形态：
// 发行版是 debian、signed-by 正好是 Dockerfile.debian 的 nginx-archive-keyring.asc、URI 路径是单段 /debian
// （旧版把 nginx.org/packages/debian 整个换成了 <镜像>/debian）。
// 用户自配的 nginx 镜像（<镜像>/nginx/debian）、Ubuntu 主机上的 nginx 源（<镜像>/ubuntu noble）都不碰。
func isBrokenNginxAPTEntry(distribution, uri, signedBy string) bool {
	if !strings.EqualFold(strings.TrimSpace(distribution), "debian") || strings.TrimSpace(signedBy) != nginxImageKeyring {
		return false
	}
	parsed, err := url.Parse(strings.TrimSpace(uri))
	if err != nil {
		return false
	}
	return strings.Trim(parsed.Path, "/") == "debian"
}

// repairBrokenNginxAPTContent 把被改坏的 nginx 源改回 https://nginx.org/packages/debian（#184、#185），
// 其它内容一个字节都不动。isSources 为 true 时按 deb822 处理。
func repairBrokenNginxAPTContent(content, distribution string, isSources bool) (string, bool) {
	lines := strings.Split(content, "\n")
	changed := false
	if !isSources {
		for i, line := range lines {
			fields := parseAPTListLineFields(line)
			uriIndex := aptURIFieldIndex(fields)
			if uriIndex < 0 || uriIndex >= len(fields) {
				continue
			}
			if !isBrokenNginxAPTEntry(distribution, fields[uriIndex], aptListSignedBy(fields, uriIndex)) {
				continue
			}
			// 只替换 URI 这一个字段，选项、suite、注释原样保留
			lines[i] = strings.Replace(line, fields[uriIndex], nginxOfficialAPTURI, 1)
			changed = true
		}
		return strings.Join(lines, "\n"), changed
	}

	for start := 0; start < len(lines); {
		end := start
		for end < len(lines) && strings.TrimSpace(lines[end]) != "" {
			end++
		}
		signedBy := aptSourcesField(lines[start:end], "signed-by")
		for i := start; i < end; i++ {
			trimmed := strings.TrimSpace(lines[i])
			if !strings.HasPrefix(strings.ToLower(trimmed), "uris:") {
				continue
			}
			if !isBrokenNginxAPTEntry(distribution, strings.TrimSpace(trimmed[len("URIs:"):]), signedBy) {
				continue
			}
			leading := lines[i][:len(lines[i])-len(strings.TrimLeft(lines[i], " \t"))]
			lines[i] = leading + "URIs: " + nginxOfficialAPTURI
			changed = true
		}
		start = end + 1
	}
	return strings.Join(lines, "\n"), changed
}

// repairAPTSourceFiles 只做「修回被改坏的 nginx 源」，发行版条目一律不碰。返回是否真的改写了文件。
func repairAPTSourceFiles(distribution string) (bool, error) {
	files, err := listAPTSourceFiles()
	if err != nil {
		return false, err
	}
	changedAny := false
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			continue
		}
		updated, changed := repairBrokenNginxAPTContent(string(data), distribution, strings.HasSuffix(file, ".sources"))
		if !changed {
			continue
		}
		if err := os.WriteFile(file, []byte(updated), 0o644); err != nil {
			return changedAny, err
		}
		changedAny = true
	}
	return changedAny, nil
}

func rewriteAPTListContent(content, distribution, mirror string) (string, bool) {
	lines := strings.Split(content, "\n")
	changed := false

	for i, line := range lines {
		updated, lineChanged := rewriteAPTListLine(line, distribution, mirror)
		if lineChanged {
			lines[i] = updated
			changed = true
		}
	}

	return strings.Join(lines, "\n"), changed
}

func rewriteAPTListLine(line, distribution, mirror string) (string, bool) {
	fields := parseAPTListLineFields(line)
	if len(fields) == 0 {
		return line, false
	}

	uriIndex := aptURIFieldIndex(fields)
	if uriIndex < 0 || uriIndex >= len(fields) {
		return line, false
	}

	currentURI := fields[uriIndex]
	// 第三方源（nginx.org、docker、nodesource 等）一律不动（#184、#185）
	if !isDistroAPTEntry(currentURI, aptListSignedBy(fields, uriIndex)) {
		return line, false
	}
	suite := ""
	if uriIndex+1 < len(fields) {
		suite = fields[uriIndex+1]
	}
	targetURI := resolveAPTMirrorURI(distribution, mirror, currentURI, suite)
	if targetURI == "" || targetURI == currentURI {
		return line, false
	}

	fields[uriIndex] = targetURI
	comment := ""
	if parts := strings.SplitN(line, "#", 2); len(parts) == 2 {
		comment = "#" + parts[1]
	}

	updated := strings.Join(fields, " ")
	if comment != "" {
		updated += " " + strings.TrimSpace(comment)
	}
	return updated, true
}

// rewriteAPTSourcesContent 改写 deb822 格式（*.sources）里的 URIs。
//
// v3.3.1 起按「段」（空行分隔的 stanza）处理：先扫完整段拿到 Suites，再改这一段的 URIs。
// 原来是逐行往下读、遇到 URIs 时用「已经读到的 Suites」，可 Debian 官方的 debian.sources
// 里 URIs 恰恰写在 Suites 前面，那时拿到的永远是空串 —— security 段根本认不出来。
func rewriteAPTSourcesContent(content, distribution, mirror string) (string, bool) {
	lines := strings.Split(content, "\n")
	changed := false

	for start := 0; start < len(lines); {
		end := start
		for end < len(lines) && strings.TrimSpace(lines[end]) != "" {
			end++
		}

		suites := ""
		for _, line := range lines[start:end] {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(strings.ToLower(trimmed), "suites:") {
				suites = strings.TrimSpace(trimmed[len("Suites:"):])
			}
		}
		// 第三方段（Signed-By 不是官方 keyring，或主机是 nginx.org）整段跳过（#184、#185）
		if !isDistroAPTEntry(aptSourcesField(lines[start:end], "uris"), aptSourcesField(lines[start:end], "signed-by")) {
			start = end + 1
			continue
		}

		for i := start; i < end; i++ {
			line := lines[i]
			trimmed := strings.TrimSpace(line)
			if !strings.HasPrefix(strings.ToLower(trimmed), "uris:") {
				continue
			}

			currentURI := strings.TrimSpace(trimmed[len("URIs:"):])
			targetURI := resolveAPTMirrorURI(distribution, mirror, currentURI, suites)
			if targetURI == "" || targetURI == currentURI {
				continue
			}

			leading := line[:len(line)-len(strings.TrimLeft(line, " \t"))]
			lines[i] = leading + "URIs: " + targetURI
			changed = true
		}

		start = end + 1
	}

	return strings.Join(lines, "\n"), changed
}

func resolveAPTMirrorURI(distribution, requestedMirror, currentURI, suites string) string {
	target := strings.TrimRight(strings.TrimSpace(requestedMirror), "/")
	if target == "" {
		target = strings.TrimRight(defaultLinuxMirror(LinuxPackageManager{Name: "apt", Binary: "apt-get"}, distribution), "/")
	}
	if target == "" {
		return ""
	}

	// Debian 的安全更新是单独的 debian-security 仓库（官方 deb.debian.org/debian-security、
	// security.debian.org/debian-security），阿里、清华、腾讯等镜像站也都是 <镜像>/debian-security。
	// 原来这一段也被改成 <镜像>/debian，而那下面没有 bookworm-security 的 Release，
	// apt-get update 会报 E:，安全更新源静默失效（安装脚本用 ; 串联，install 仍会继续，所以一直没人发现）。
	if isDebianSecurityAPTSource(distribution, currentURI, suites) && !strings.HasSuffix(target, "-security") {
		return target + "-security"
	}
	return target
}

// isDebianSecurityAPTSource 判断这一条源是不是 Debian 的安全更新仓库。
//
// 两条判据：URI 的路径以 -security 结尾（与发行版无关，路径本身就说明了一切）；
// 或者发行版是 Debian 且 Suites 里有 *-security（bookworm-security 这类）/ */updates（buster 及更早的写法）。
// Suites 这条刻意只认 Debian：Ubuntu 的 noble-security 就在 /ubuntu 下（官方 security.ubuntu.com/ubuntu），
// 加上 -security 后缀反而会指到一个不存在的路径。
func isDebianSecurityAPTSource(distribution, currentURI, suites string) bool {
	if strings.HasSuffix(strings.TrimRight(strings.TrimSpace(currentURI), "/"), "-security") {
		return true
	}
	if !strings.EqualFold(strings.TrimSpace(distribution), "debian") {
		return false
	}
	for _, suite := range strings.Fields(suites) {
		if strings.HasSuffix(suite, "-security") || strings.HasSuffix(suite, "/updates") {
			return true
		}
	}
	return false
}

func isHTTPMirror(value string) bool {
	value = strings.TrimSpace(value)
	return strings.HasPrefix(value, "http://") || strings.HasPrefix(value, "https://")
}

// defaultLinuxMirror v3.3.2 起从阿里云换成腾讯云（issue #146）：用户实测阿里云限速不到 100KB/s。
// 三条路径形态与阿里云完全一致（<站点>/alpine、/debian、/ubuntu），security 段由 resolveAPTMirrorURI
// 自动拼 -security，腾讯云的 mirrors.cloud.tencent.com/debian-security 是存在的。
// 阿里云只是不再当默认，仍留在前端候选清单里。
func defaultLinuxMirror(manager LinuxPackageManager, distribution string) string {
	switch manager.Name {
	case "apk":
		return "https://mirrors.cloud.tencent.com/alpine"
	case "apt":
		switch strings.ToLower(strings.TrimSpace(distribution)) {
		case "debian":
			return "https://mirrors.cloud.tencent.com/debian"
		default:
			return "https://mirrors.cloud.tencent.com/ubuntu"
		}
	default:
		return ""
	}
}

// isLegacyDefaultLinuxMirror 判断当前源是不是 v3.3.2 之前的默认加速源（阿里云）。
//
// 只给 EnsureDefaultLinuxMirror 的一次性迁移用，不参与 EffectiveLinuxMirror：
// 后者同时负责「页面上显示当前生效的源」与「存盘前归一化」，掺进迁移会出现
// 「磁盘写着阿里云、页面显示腾讯云」这种读写不对称，也会让用户选不回阿里云。
//
// debian 档要把 debian-security 一并认上：readAPTMirror 返回的是第一个匹配到的条目，
// 在只剩 security 段还指向阿里云的机器上，读出来的就是 <站点>/debian-security。
func isLegacyDefaultLinuxMirror(manager LinuxPackageManager, distribution, current string) bool {
	currentLower := strings.ToLower(strings.TrimRight(strings.TrimSpace(current), "/"))
	switch manager.Name {
	case "apk":
		return currentLower == "https://mirrors.aliyun.com/alpine"
	case "apt":
		switch strings.ToLower(strings.TrimSpace(distribution)) {
		case "debian":
			return currentLower == "https://mirrors.aliyun.com/debian" ||
				currentLower == "https://mirrors.aliyun.com/debian-security"
		default:
			return currentLower == "https://mirrors.aliyun.com/ubuntu"
		}
	default:
		return false
	}
}

func isOfficialLinuxMirror(manager LinuxPackageManager, distribution, current string) bool {
	currentLower := strings.ToLower(strings.TrimRight(strings.TrimSpace(current), "/"))
	switch manager.Name {
	case "apk":
		return currentLower == "https://dl-cdn.alpinelinux.org/alpine" || currentLower == "http://dl-cdn.alpinelinux.org/alpine"
	case "apt":
		switch distribution {
		case "debian":
			return currentLower == "http://deb.debian.org/debian" ||
				currentLower == "https://deb.debian.org/debian" ||
				currentLower == "http://security.debian.org/debian-security" ||
				currentLower == "https://security.debian.org/debian-security"
		default:
			return currentLower == "http://archive.ubuntu.com/ubuntu" ||
				currentLower == "https://archive.ubuntu.com/ubuntu" ||
				currentLower == "http://security.ubuntu.com/ubuntu" ||
				currentLower == "https://security.ubuntu.com/ubuntu"
		}
	default:
		return false
	}
}
