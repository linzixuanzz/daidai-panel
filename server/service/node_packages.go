package service

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"daidai-panel/config"
)

var nodePackageOperationMu sync.Mutex

var nodeRequireCompatiblePackageSpecs = map[string]string{
	"uuid":                   "uuid@8.3.2",
	"axios":                  "axios@0.27.2",
	"node-fetch":             "node-fetch@2.7.0",
	"got":                    "got@11.8.6",
	"chalk":                  "chalk@4.1.2",
	"ora":                    "ora@5.4.1",
	"execa":                  "execa@5.1.1",
	"nanoid":                 "nanoid@3.3.7",
	"p-limit":                "p-limit@3.1.0",
	"p-queue":                "p-queue@6.6.2",
	"p-retry":                "p-retry@4.6.2",
	"p-timeout":              "p-timeout@4.1.0",
	"quick-lru":              "quick-lru@5.1.1",
	"yocto-queue":            "yocto-queue@0.1.0",
	"is-stream":              "is-stream@2.0.1",
	"is-port-reachable":      "is-port-reachable@3.1.0",
	"make-dir":               "make-dir@3.1.0",
	"find-up":                "find-up@5.0.0",
	"locate-path":            "locate-path@6.0.0",
	"path-exists":            "path-exists@4.0.0",
	"camelcase":              "camelcase@6.3.0",
	"decamelize":             "decamelize@4.0.0",
	"supports-color":         "supports-color@8.1.1",
	"file-type":              "file-type@16.5.4",
	"mime":                   "mime@3.0.0",
	"strip-ansi":             "strip-ansi@6.0.1",
	"string-width":           "string-width@4.2.3",
	"wrap-ansi":              "wrap-ansi@7.0.0",
	"cli-truncate":           "cli-truncate@2.1.0",
	"boxen":                  "boxen@5.1.2",
	"open":                   "open@8.4.2",
	"del":                    "del@6.1.1",
	"globby":                 "globby@11.1.0",
	"cheerio":                "cheerio@1.0.0-rc.12",
	"undici":                 "undici@5.28.5",
	"ws":                     "ws@7.5.10",
	"tough-cookie":           "tough-cookie@4.1.4",
	"form-data":              "form-data@4.0.0",
	"https-proxy-agent":      "https-proxy-agent@5.0.1",
	"http-proxy-agent":       "http-proxy-agent@5.0.0",
	"socks-proxy-agent":      "socks-proxy-agent@7.0.0",
	"hpagent":                "hpagent@1.2.0",
	"tunnel":                 "tunnel@0.0.6",
	"tunnel-agent":           "tunnel-agent@0.6.0",
	"request":                "request@2.88.2",
	"request-promise":        "request-promise@4.2.6",
	"request-promise-native": "request-promise-native@1.0.9",
	"crypto-js":              "crypto-js@4.2.0",
	"md5":                    "md5@2.3.0",
	"js-md5":                 "js-md5@0.7.3",
	"qs":                     "qs@6.11.2",
	"query-string":           "query-string@7.1.3",
	"querystring":            "querystring@0.2.1",
	"moment":                 "moment@2.29.4",
	"dayjs":                  "dayjs@1.11.10",
	"lodash":                 "lodash@4.17.21",
	"dotenv":                 "dotenv@16.4.5",
	"yaml":                   "yaml@2.3.4",
	"js-yaml":                "js-yaml@4.1.0",
	"adm-zip":                "adm-zip@0.5.10",
	"node-rsa":               "node-rsa@1.1.1",
	"rsa-pem-from-mod-exp":   "rsa-pem-from-mod-exp@0.8.5",
	"iconv-lite":             "iconv-lite@0.6.3",
	"date-fns":               "date-fns@2.30.0",
	"csv-parse":              "csv-parse@5.5.6",
	"fast-xml-parser":        "fast-xml-parser@4.3.6",
	"xml2js":                 "xml2js@0.6.2",
	"jsonwebtoken":           "jsonwebtoken@9.0.2",
	"jimp":                   "jimp@0.22.12",
	"fs-extra":               "fs-extra@11.2.0",
	"data-uri-to-buffer":     "data-uri-to-buffer@3.0.1",
	"fetch-blob":             "fetch-blob@2.1.2",
	"formdata-polyfill":      "formdata-polyfill@4.0.10",
}

// LockNodePackageOperation 串行化 npm install / uninstall。
// npm 会同时改 package.json / package-lock.json，并发执行时很容易把 JSON 写坏。
func LockNodePackageOperation() func() {
	nodePackageOperationMu.Lock()
	return nodePackageOperationMu.Unlock
}

func NewNpmInstallCommand(packageName string) (*exec.Cmd, error) {
	// 以 - 开头的包名会被 npm 当成选项（如 --registry），见 rejectOptionLikePackageName。
	if err := rejectOptionLikePackageName(packageName); err != nil {
		return nil, err
	}
	nodeDir := filepath.Join(config.C.Data.Dir, "deps", "nodejs")
	if err := ensureNodePackageManifest(nodeDir); err != nil {
		return nil, err
	}

	installSpec := ResolveNodeInstallPackageSpec(packageName)
	cmd := exec.Command("npm", "install", "--prefix", nodeDir, installSpec)
	cmd.Env = NpmInstallEnv(AppendProxyEnv(os.Environ()), CurrentNpmMirror())
	return cmd, nil
}

func ResolveNodeInstallPackageSpec(packageName string) string {
	packageName = strings.TrimSpace(packageName)
	if packageName == "" || nodePackageSpecHasExplicitVersionOrSource(packageName) {
		return packageName
	}

	// 常见 npm 包的新版本已经切到 ESM-only，旧脚本用 require() 会直接 ERR_REQUIRE_ESM。
	// 这里仅对裸包名做兼容版本钉定；用户明确写版本、tag、URL、Git 或本地路径时保持原样。
	normalized := NormalizeNodeDependencyPackageName(packageName)
	if pinned, ok := nodeRequireCompatiblePackageSpecs[normalized]; ok {
		return pinned
	}
	return packageName
}

func NodeInstallCompatibilityNotice(packageName string) string {
	packageName = strings.TrimSpace(packageName)
	if packageName == "" {
		return ""
	}

	if nodePackageSpecHasExplicitVersionOrSource(packageName) {
		return fmt.Sprintf("[Node.js 依赖] 已按你指定的版本或来源安装：%s", packageName)
	}

	installSpec := ResolveNodeInstallPackageSpec(packageName)
	if installSpec != packageName {
		return fmt.Sprintf("[Node.js 依赖] %s 已命中 CommonJS 兼容映射，将安装：%s", packageName, installSpec)
	}

	// 没有命中映射时必须提前说清楚，避免用户误以为面板仍然会自动降级到 CJS 旧版。
	return fmt.Sprintf("[Node.js 依赖] %s：该包未在兼容映射中，将按 npm 默认版本安装。", packageName)
}

func nodePackageSpecHasExplicitVersionOrSource(spec string) bool {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return false
	}

	lower := strings.ToLower(spec)
	for _, prefix := range []string{
		"file:", "link:", "workspace:", "npm:",
		"http://", "https://",
		"git+", "git://", "ssh://",
		"github:", "gitlab:", "bitbucket:",
	} {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}

	if strings.HasPrefix(spec, "@") {
		parts := strings.SplitN(spec, "/", 2)
		if len(parts) != 2 {
			return false
		}
		return strings.LastIndex(parts[1], "@") > 0
	}

	return strings.LastIndex(spec, "@") > 0
}

func NewNpmUninstallCommand(packageName string, force bool) (*exec.Cmd, error) {
	nodeDir := filepath.Join(config.C.Data.Dir, "deps", "nodejs")
	if err := ensureNodePackageManifest(nodeDir); err != nil {
		return nil, err
	}

	args := []string{"uninstall", "--prefix", nodeDir}
	if force {
		args = append(args, "--force")
	}
	args = append(args, packageName)

	cmd := exec.Command("npm", args...)
	cmd.Env = NpmInstallEnv(AppendProxyEnv(os.Environ()), CurrentNpmMirror())
	return cmd, nil
}

func ensureNodePackageManifest(nodeDir string) error {
	if err := os.MkdirAll(nodeDir, 0o755); err != nil {
		return fmt.Errorf("创建 Node.js 依赖目录失败: %w", err)
	}

	packageJSONPath := filepath.Join(nodeDir, "package.json")
	data, err := os.ReadFile(packageJSONPath)
	if os.IsNotExist(err) {
		return writeNodePackageManifest(packageJSONPath, collectInstalledNodeDependencies(nodeDir))
	}
	if err != nil {
		return fmt.Errorf("读取 Node.js package.json 失败: %w", err)
	}

	var manifest map[string]any
	if err := json.Unmarshal(data, &manifest); err == nil && manifest != nil {
		if depsValue, exists := manifest["dependencies"]; exists {
			if _, ok := depsValue.(map[string]any); !ok {
				return backupAndRewriteNodePackageManifest(packageJSONPath, nodeDir)
			}
		}
		return nil
	}

	return backupAndRewriteNodePackageManifest(packageJSONPath, nodeDir)
}

func backupAndRewriteNodePackageManifest(packageJSONPath, nodeDir string) error {
	backupPath := packageJSONPath + ".broken-" + time.Now().Format("20060102150405")
	for index := 1; ; index++ {
		if _, err := os.Stat(backupPath); os.IsNotExist(err) {
			break
		}
		backupPath = fmt.Sprintf("%s.broken-%s-%d", packageJSONPath, time.Now().Format("20060102150405"), index)
	}

	// 先备份坏文件，方便用户后续排查；再根据 node_modules 重建一个 npm 能解析的最小 package.json。
	if err := os.Rename(packageJSONPath, backupPath); err != nil {
		return fmt.Errorf("备份损坏的 Node.js package.json 失败: %w", err)
	}
	if err := writeNodePackageManifest(packageJSONPath, collectInstalledNodeDependencies(nodeDir)); err != nil {
		return err
	}
	return nil
}

func writeNodePackageManifest(packageJSONPath string, dependencies map[string]string) error {
	manifest := map[string]any{
		"private":      true,
		"dependencies": dependencies,
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("生成 Node.js package.json 失败: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(packageJSONPath, data, 0o644); err != nil {
		return fmt.Errorf("写入 Node.js package.json 失败: %w", err)
	}
	return nil
}

func collectInstalledNodeDependencies(nodeDir string) map[string]string {
	dependencies := map[string]string{}
	nodeModulesDir := filepath.Join(nodeDir, "node_modules")
	entries, err := os.ReadDir(nodeModulesDir)
	if err != nil {
		return dependencies
	}

	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}

		if strings.HasPrefix(entry.Name(), "@") {
			scopeDir := filepath.Join(nodeModulesDir, entry.Name())
			scopeEntries, err := os.ReadDir(scopeDir)
			if err != nil {
				continue
			}
			for _, scopeEntry := range scopeEntries {
				if !scopeEntry.IsDir() || strings.HasPrefix(scopeEntry.Name(), ".") {
					continue
				}
				fallbackName := filepath.ToSlash(filepath.Join(entry.Name(), scopeEntry.Name()))
				addInstalledNodeDependency(dependencies, filepath.Join(scopeDir, scopeEntry.Name()), fallbackName)
			}
			continue
		}

		addInstalledNodeDependency(dependencies, filepath.Join(nodeModulesDir, entry.Name()), entry.Name())
	}

	return dependencies
}

func addInstalledNodeDependency(dependencies map[string]string, moduleDir, fallbackName string) {
	data, err := os.ReadFile(filepath.Join(moduleDir, "package.json"))
	if err != nil {
		return
	}

	var pkg struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	}
	if err := json.Unmarshal(data, &pkg); err != nil {
		return
	}

	name := strings.TrimSpace(pkg.Name)
	if name == "" {
		name = strings.TrimSpace(filepath.ToSlash(fallbackName))
	}
	if name == "" {
		return
	}

	version := strings.TrimSpace(pkg.Version)
	if version == "" {
		dependencies[name] = "*"
		return
	}
	dependencies[name] = "^" + version
}
