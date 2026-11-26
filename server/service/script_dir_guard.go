package service

import (
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"daidai-panel/config"
)

// quarantinedScriptDirNames 记录启动期需要自动隔离的异常脚本目录名，也是隔离逻辑唯一认的名单。
// 这些目录不属于正常脚本文件树，一旦混入会污染脚本管理、备份恢复和统计结果。
// ⚠️ 只放真正的污染目录：命中即 os.Rename 物理搬走。node_modules、__pycache__、.git 都不能加进来（#156）。
var quarantinedScriptDirNames = map[string]bool{
	"%systemdrive%": true,
}

// ShouldIgnoreScriptEntryName 判断脚本目录中的某个顶级/子级名称是否应该在展示、统计、备份等场景忽略。
// 这里保留统一出口，避免 handler / service / backup 各写一套判断逻辑。
// 它只管「忽略」，不管「搬走」：启动期隔离只认 quarantinedScriptDirNames。
func ShouldIgnoreScriptEntryName(name string) bool {
	name = strings.TrimSpace(name)
	if name == "" {
		return false
	}

	switch strings.ToLower(name) {
	case "node_modules", "__pycache__":
		return true
	}

	return quarantinedScriptDirNames[strings.ToLower(name)]
}

// hiddenScriptTreeNames 记录必须对脚本管理链路隐藏、并禁止读写的版本控制元数据目录。
// 其中 .git 里的 config 保存着订阅 Token 鉴权注入的 remote URL（内含 PAT），
// 一旦能被读出来就等于把访问令牌交给任何能读脚本的主体，因此这里硬编码、刻意不做可配置 ——
// 只要可配置，用户清空配置就重新打开了凭据读取路径。
var hiddenScriptTreeNames = map[string]bool{
	".git": true,
	".svn": true,
	".hg":  true,
	".bzr": true,
}

// ShouldHideScriptTreeEntryName 判断某个名称是否应该在脚本树展示与访问链路里被隐藏 / 拒绝。
// ⚠️ 它刻意与 ShouldIgnoreScriptEntryName 分成两套语义：后者还决定备份打包 / 恢复跳过哪些路径，
// 把 .git 加进去，订阅仓库的 git 元数据就进不了备份；更不能加进启动期隔离名单 quarantinedScriptDirNames，
// 那份名单命中即 os.Rename 物理搬走，脚本根目录本身是 git 仓库时整个仓库会被搬走。
func ShouldHideScriptTreeEntryName(name string) bool {
	name = strings.TrimSpace(name)
	if name == "" {
		return false
	}

	if ShouldIgnoreScriptEntryName(name) {
		return true
	}

	return hiddenScriptTreeNames[strings.ToLower(name)]
}

// scriptTreeCacheDirNames 是包管理器 / 工具链的缓存目录，里面动辄几十万个小文件（#183：.npm-cache 让
// GET /scripts/tree 递归超过前端 30 秒超时，脚本页打不开）。
// ⚠️ 这是第四套名单，语义最窄：只在「脚本树 / 脚本列表的展示」里跳过，别的地方一概不管——
//   - 不进 ShouldIgnoreScriptEntryName：那份还决定备份跳过哪些路径，缓存目录要不要进备份不归这里改；
//   - 不进 hiddenScriptTreeNames：那份会让 safePath 拒绝读写，缓存目录没有凭据，不需要锁；
//   - 更不能进 quarantinedScriptDirNames：那份命中即物理搬走，搬走缓存目录会让脚本运行时重新下载一遍。
var scriptTreeCacheDirNames = map[string]bool{
	".npm-cache":  true,
	".npm":        true,
	".cache":      true,
	".pnpm-store": true,
	".yarn-cache": true,
}

// IsScriptTreeCacheDirName 判断某个目录名是不是只在脚本树 / 列表展示里跳过的缓存目录（见 scriptTreeCacheDirNames）。
func IsScriptTreeCacheDirName(name string) bool {
	return scriptTreeCacheDirNames[strings.ToLower(strings.TrimSpace(name))]
}

// ShouldHideScriptTreeRelativePath 逐段遍历相对路径，任意一段命中隐藏规则即返回 true。
// 与只判第一段的 ShouldIgnoreScriptRelativePath 不同，这里能拦住 SmallWorld/.git/config
// 这类嵌套路径（顺带修掉“子目录里的 node_modules 仍被计入脚本数”这个既有缺陷）。
func ShouldHideScriptTreeRelativePath(relPath string) bool {
	relPath = strings.TrimSpace(filepath.ToSlash(relPath))
	if relPath == "" || relPath == "." || relPath == "/" {
		return false
	}

	relPath = strings.TrimPrefix(relPath, "/")

	for _, segment := range strings.Split(relPath, "/") {
		segment = strings.TrimSpace(segment)
		if segment == "" || segment == "." || segment == ".." {
			continue
		}
		if ShouldHideScriptTreeEntryName(segment) {
			return true
		}
	}

	return false
}

// ShouldHideScriptTreePath 判断脚本目录内的某个绝对路径是否命中隐藏规则（逐段遍历）。
func ShouldHideScriptTreePath(scriptsDir, targetPath string) bool {
	scriptsDir = strings.TrimSpace(scriptsDir)
	targetPath = strings.TrimSpace(targetPath)
	if scriptsDir == "" || targetPath == "" {
		return false
	}

	absScriptsDir, err := filepath.Abs(scriptsDir)
	if err != nil {
		return false
	}
	absTargetPath, err := filepath.Abs(targetPath)
	if err != nil {
		return false
	}

	relPath, err := filepath.Rel(absScriptsDir, absTargetPath)
	if err != nil {
		return false
	}

	relPath = filepath.ToSlash(relPath)
	if relPath == "." || relPath == ".." || strings.HasPrefix(relPath, "../") {
		return false
	}

	return ShouldHideScriptTreeRelativePath(relPath)
}

// ShouldIgnoreScriptPath 判断某个脚本目录内的绝对路径是否命中异常隔离规则。
// 只要相对路径第一段命中黑名单，就认为这条路径不该继续参与脚本管理链路。
func ShouldIgnoreScriptPath(scriptsDir, targetPath string) bool {
	scriptsDir = strings.TrimSpace(scriptsDir)
	targetPath = strings.TrimSpace(targetPath)
	if scriptsDir == "" || targetPath == "" {
		return false
	}

	absScriptsDir, err := filepath.Abs(scriptsDir)
	if err != nil {
		return false
	}
	absTargetPath, err := filepath.Abs(targetPath)
	if err != nil {
		return false
	}

	relPath, err := filepath.Rel(absScriptsDir, absTargetPath)
	if err != nil {
		return false
	}

	relPath = filepath.ToSlash(relPath)
	if relPath == "." || strings.HasPrefix(relPath, "../") || relPath == ".." {
		return false
	}

	firstSegment := relPath
	if slashIndex := strings.Index(firstSegment, "/"); slashIndex >= 0 {
		firstSegment = firstSegment[:slashIndex]
	}

	return ShouldIgnoreScriptEntryName(firstSegment)
}

// ShouldIgnoreScriptRelativePath 用于还原备份、导入脚本等“目标路径尚未落盘”的场景。
// 只检查相对路径第一段，避免把异常目录重新写回脚本根目录。
func ShouldIgnoreScriptRelativePath(relPath string) bool {
	relPath = strings.TrimSpace(filepath.ToSlash(relPath))
	if relPath == "" || relPath == "." || relPath == "/" {
		return false
	}

	if strings.HasPrefix(relPath, "/") {
		relPath = strings.TrimPrefix(relPath, "/")
	}

	firstSegment := relPath
	if slashIndex := strings.Index(firstSegment, "/"); slashIndex >= 0 {
		firstSegment = firstSegment[:slashIndex]
	}

	return ShouldIgnoreScriptEntryName(firstSegment)
}

// QuarantineUnexpectedScriptEntriesOnStartup 会在启动时把脚本目录顶层的已知污染目录（quarantinedScriptDirNames，
// 例如 %SystemDrive%）隔离到 quarantine 子目录，并清掉面板自己建、目标已经不在了的 node_modules 软链。
// 这样能先把当前污染从用户视野和备份链路里移走，同时保留原始证据方便后续排查根因。
//
// node_modules、__pycache__ 只是在展示、统计、备份里被忽略，这里不再搬走（#156）：
// 脚本根目录的 __pycache__ 是被 import 的模块（例如面板自己放的 notify.py）每次运行都会重新生成的；
// 用户自己 npm install 出来的真 node_modules 被搬走后 require 直接失败，还每次启动在 quarantine 里多攒一份副本。
func QuarantineUnexpectedScriptEntriesOnStartup() {
	if config.C == nil {
		return
	}

	scriptsDir := strings.TrimSpace(config.C.Data.ScriptsDir)
	if scriptsDir == "" {
		return
	}

	entries, err := os.ReadDir(scriptsDir)
	if err != nil {
		log.Printf("scan scripts dir failed: %v", err)
		return
	}

	for _, entry := range entries {
		if !quarantinedScriptDirNames[strings.ToLower(strings.TrimSpace(entry.Name()))] {
			continue
		}

		sourcePath := filepath.Join(scriptsDir, entry.Name())
		quarantineRoot := filepath.Join(config.C.Data.Dir, "quarantine", "scripts")
		if err := os.MkdirAll(quarantineRoot, 0o755); err != nil {
			log.Printf("create script quarantine dir failed: %v", err)
			continue
		}

		targetPath := filepath.Join(quarantineRoot, entry.Name())
		targetPath = uniqueQuarantinePath(targetPath)

		if err := os.Rename(sourcePath, targetPath); err != nil {
			log.Printf("quarantine unexpected script entry failed: %s -> %s: %v", sourcePath, targetPath, err)
			continue
		}

		log.Printf("unexpected script entry quarantined: %s -> %s", sourcePath, targetPath)
	}

	removeLeftoverManagedNodeModulesLinks(scriptsDir, filepath.Join(config.C.Data.Dir, "deps", "nodejs", "node_modules"))
}

// removeLeftoverManagedNodeModulesLinks 删掉脚本目录树里面板自己建、目标已经不在了的 node_modules 软链（Windows 上是 junction）。
//
// 来源：Node / TS 任务运行时，工作目录（脚本所在目录）下没有 node_modules 时，ensureManagedNodeModulesAccess
// 会建一个指向托管依赖目录的软链，任务结束再删；面板被强杀时来不及删就一直留着，
// 而 ensureManagedNodeModulesAccess 只在不存在时才建、从不修已有的。以前靠隔离把顶层那个搬走「顺手自愈」，
// 现在不再隔离 node_modules，改为在这里清理。
//
// 只删「悬空的面板式软链」，下面几条同时满足才删，其余一律不动：
//   - 名字是 node_modules 的软链 / junction，Readlink 得到绝对路径（面板只建绝对路径的，相对路径的算用户自己的）；
//   - 目标 Clean 后等于当前托管依赖目录，或以 <分隔符>deps<分隔符>nodejs<分隔符>node_modules 结尾
//     （数据目录搬家、改过 data.dir 之前建的，指向旧数据目录）；
//   - 目标已经不存在（os.Stat 报不存在）。
//
// 为什么只删悬空的：悬空的留着有害——ensureManagedNodeModulesAccess 看到同名项就不再建，ESM 的 import 又没有 NODE_PATH 兜底，
// 那个目录里的脚本会一直报找不到模块。不悬空的面板软链与面板现在会建的等价，留着无害；而且启动时也不是真没人在用：
// ddp task run 是另一个进程，二进制 / Magisk 部署下被强杀的面板还可能留下仍在跑的孤儿任务，删掉它们正在用的软链，
// 正在 require 的依赖会突然消失。真目录、指向别处的软链（悬空的也算）都是用户自己的。
func removeLeftoverManagedNodeModulesLinks(scriptsDir, managedNodeModules string) {
	// 脚本目录本身是软链 / junction（把脚本放到别的盘、NAS 上常这么做）时，WalkDir 只看根这一项、不会进到链接里面，
	// 整棵树一个都清不到。所以先解析成真实目录再遍历：Linux 的软链、Windows 的目录软链交给 EvalSymlinks；
	// Windows 的 junction 它不解析（Go 1.23 起 junction 不再按软链处理，EvalSymlinks 原样返回），再用 Readlink 补一层。
	// 解析失败就用原值。
	root := scriptsDir
	if resolved, err := filepath.EvalSymlinks(scriptsDir); err == nil {
		root = resolved
	}
	if info, err := os.Lstat(root); err == nil && !info.IsDir() {
		if target, err := os.Readlink(root); err == nil && filepath.IsAbs(target) {
			root = target
		}
	}

	panelLinkSuffix := string(filepath.Separator) + filepath.Join("deps", "nodejs", "node_modules")
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || path == root {
			// 读不了的目录跳过，不影响其它目录。
			return nil
		}
		if strings.EqualFold(d.Name(), "node_modules") {
			target, readErr := os.Readlink(path)
			if readErr != nil {
				// 不是软链。真的 node_modules 目录不进去：任务不会在它里面跑，里面也就不会有面板建的软链，动辄上千个目录。
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			cleanTarget := filepath.Clean(target)
			panelStyle := cleanTarget == managedNodeModules || strings.HasSuffix(cleanTarget, panelLinkSuffix)
			if runtime.GOOS == "windows" {
				// Windows 路径不分大小写，同一个目录可能写成不同大小写。
				panelStyle = strings.EqualFold(cleanTarget, managedNodeModules) ||
					strings.HasSuffix(strings.ToLower(cleanTarget), strings.ToLower(panelLinkSuffix))
			}
			// 相对路径必须在 Stat 之前放过：os.Stat 会按面板进程的工作目录去解析它，判不准是不是悬空。
			if !filepath.IsAbs(target) || !panelStyle {
				return nil
			}
			if _, statErr := os.Stat(target); !os.IsNotExist(statErr) {
				// 目标还在（或者查不了）：留着，理由见函数注释。
				return nil
			}
			if removeErr := os.Remove(path); removeErr != nil {
				log.Printf("remove leftover node_modules link failed: %s: %v", path, removeErr)
			} else {
				log.Printf("leftover node_modules link removed: %s -> %s", path, target)
			}
			return nil
		}
		// .git、__pycache__ 这类目录同理，整棵跳过。
		if d.IsDir() && ShouldHideScriptTreeEntryName(d.Name()) {
			return filepath.SkipDir
		}
		return nil
	})
}

// uniqueQuarantinePath 避免隔离目录重名导致历史证据被覆盖。
func uniqueQuarantinePath(targetPath string) string {
	if _, err := os.Stat(targetPath); os.IsNotExist(err) {
		return targetPath
	}

	dir := filepath.Dir(targetPath)
	ext := filepath.Ext(targetPath)
	base := strings.TrimSuffix(filepath.Base(targetPath), ext)

	for index := 1; index < 1000; index++ {
		candidate := filepath.Join(dir, base+".duplicate-"+strconv.Itoa(index)+ext)
		if _, err := os.Stat(candidate); os.IsNotExist(err) {
			return candidate
		}
	}

	return filepath.Join(dir, base+".duplicate-overflow"+ext)
}
