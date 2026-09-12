//go:build linux

package service

import (
	"os"
	"path/filepath"
	"testing"

	"daidai-panel/config"
	"daidai-panel/testutil"
)

// 本文件的用例靠 os.Symlink 构造场景，只在 Linux 上编译运行（CI 是 ubuntu-latest）；建软链接出错一律 Fatalf，
// 绝不跳过。Windows 上建软链接要特权，只能由 tstSymlinkOrSkip 的同类用例在有权限时覆盖。

// R2-1：Docker 版青龙兼容层会建 /ql/data/scripts 这类指向脚本目录的软链接（entrypoint.sh），迁移用户的命令常写成
// 别名的绝对路径。解析器把它解析到脚本目录内，字面路径却在目录外——这只可能是经过了软链接，应报 symlink；
// A1 的逐段检查曾把它退化成 check_failed，而且不留日志。经别名落到 node_modules 的仍报 hidden_path。
func TestTaskScriptAliasAbsolutePathKeptAsSymlink(t *testing.T) {
	env := tscSetup(t)
	scriptsDir := config.C.Data.ScriptsDir
	alias := filepath.Join(filepath.Dir(scriptsDir), "qlalias")
	if err := os.Symlink(scriptsDir, alias); err != nil {
		t.Fatalf("symlink scripts alias: %v", err)
	}
	signFile := tscWriteFile(t, "jd_sign.js", "x")
	nmFile := tscWriteFile(t, "node_modules/lib.js", "x")
	signCommand := "task " + filepath.Join(alias, "jd_sign.js")
	nmCommand := "task " + filepath.Join(alias, "node_modules", "lib.js")

	target := ResolveTaskScriptTarget(signCommand, tstBase(t))
	if target.Kind != taskScriptKindScript || target.RelPath != "jd_sign.js" || target.Direct || target.LinkCheckFailed {
		t.Fatalf("an alias absolute path must resolve to an indirect script without a link check failure, got %#v", target)
	}

	signTask := tscCreateTask(t, "alias-sign", signCommand)
	nmTask := tscCreateTask(t, "alias-nm", nmCommand)
	ids := []uint{signTask.ID, nmTask.ID}

	preview := PreviewTaskScriptDeletion(ids, env)
	tscAssertKept(t, tscItem(t, preview.Scripts, "jd_sign.js"), TaskScriptReasonSymlink)
	tscAssertKept(t, tscItem(t, preview.Scripts, "node_modules/lib.js"), TaskScriptReasonHiddenPath)

	cleanup := CollectTaskScriptTargets(ids, env)
	tscDeleteTaskRows(t, ids...)
	result := cleanup.Execute(nil)
	if len(result.Deleted) != 0 || len(result.Skipped) != 2 {
		t.Fatalf("scripts reached through a scripts-dir alias must not be deleted, got %#v", result)
	}
	tscAssertKept(t, tscItem(t, result.Skipped, "jd_sign.js"), TaskScriptReasonSymlink)
	tscAssertKept(t, tscItem(t, result.Skipped, "node_modules/lib.js"), TaskScriptReasonHiddenPath)
	tscAssertExists(t, signFile)
	tscAssertExists(t, nmFile)
}

// R2-02：脚本目录本身挂在软链接下（Docker / Magisk），命令写真实目录的绝对路径、而文件已经不存在时，HintPath 要能
// 对 Real 求出相对路径（与 LiteralRel、normalizeScriptReference 同口径）。否则 script_path 为空，前端认不出
// 「确认过的文件已经不存在」，照旧弹 warning 和「去处理」。两个目录都够不着的路径仍然留空，不回显绝对路径。
func TestTaskScriptHintPathUnderSymlinkedScriptsDir(t *testing.T) {
	testutil.SetupTestEnv(t)
	root := filepath.Dir(config.C.Data.ScriptsDir)
	realScripts := filepath.Join(root, "real-scripts")
	if err := os.MkdirAll(filepath.Join(realScripts, "sub"), 0o755); err != nil {
		t.Fatalf("mkdir real scripts: %v", err)
	}
	linkScripts := filepath.Join(root, "link-scripts")
	if err := os.Symlink(realScripts, linkScripts); err != nil {
		t.Fatalf("symlink scripts dir: %v", err)
	}
	base, err := resolveScriptsBase(linkScripts)
	if err != nil {
		t.Fatalf("resolve base: %v", err)
	}
	if base.Abs == base.Real {
		t.Fatalf("expected the symlinked scripts dir to have distinct Abs and Real, got %#v", base)
	}

	realMissing := "task " + filepath.Join(base.Real, "sub", "missing.py")
	cases := []struct {
		command string
		hint    string
	}{
		{realMissing, "sub/missing.py"},
		{"task " + filepath.Join(base.Abs, "sub", "missing.py"), "sub/missing.py"},
		{"task sub/missing.py", "sub/missing.py"},
		{"task " + filepath.Join(root, "elsewhere", "missing.py"), ""},
	}
	for _, tc := range cases {
		target := ResolveTaskScriptTarget(tc.command, base)
		if target.Kind != taskScriptKindNotFound || target.HintPath != tc.hint {
			t.Fatalf("%s: expected not_found with hint %q, got kind=%q hint=%q", tc.command, tc.hint, target.Kind, target.HintPath)
		}
	}

	// 走一遍预览：tasks[].script_path 与 note 都用上这个相对路径。
	task := tscCreateTask(t, "missing-real", realMissing)
	info := tscTaskInfo(t, PreviewTaskScriptDeletion([]uint{task.ID}, TaskScriptCleanupEnv{ScriptsDir: linkScripts}).Tasks, task.ID)
	if info.ScriptStatus != taskScriptStatusNotFound || info.ScriptPath != "sub/missing.py" || info.Note != "命令里的脚本 sub/missing.py 已经不存在，只删除任务。" {
		t.Fatalf("unexpected task info: %#v", info)
	}
}
