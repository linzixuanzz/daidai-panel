package service

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"daidai-panel/config"
	"daidai-panel/testutil"
)

// #124「删除任务时可选同时删除脚本」：命令 → 脚本文件的解析。判定与执行的用例在 task_script_cleanup_test.go。

func tstWriteFile(t *testing.T, rel string) string {
	t.Helper()
	full := filepath.Join(config.C.Data.ScriptsDir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir for %s: %v", rel, err)
	}
	if err := os.WriteFile(full, []byte("echo ok\n"), 0o644); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
	return full
}

func tstBase(t *testing.T) scriptsBase {
	t.Helper()
	base, err := resolveScriptsBase(config.C.Data.ScriptsDir)
	if err != nil {
		t.Fatalf("resolve scripts base: %v", err)
	}
	return base
}

// tstSymlinkOrSkip 建软链接；Windows 没有权限时跳过（沿用 pkg/pathutil/pathutil_test.go 的写法）。
// 这类用例必须另外在 Linux（WSL）上跑一次，确认没有被跳过。
func tstSymlinkOrSkip(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
}

func tstContains(list []string, want string) bool {
	for _, item := range list {
		if item == want {
			return true
		}
	}
	return false
}

// ScriptToken 记录命令里被认作脚本的那段原始文本；FullPath 仍是解析过软链接的绝对路径，与改动前完全一致。
func TestParseCommandExecutionPlanRecordsScriptToken(t *testing.T) {
	testutil.SetupTestEnv(t)
	scriptsDir := config.C.Data.ScriptsDir
	spaced := tstWriteFile(t, "demo folder/my script.py")
	plain := tstWriteFile(t, "a.py")
	tstWriteFile(t, "b.js")
	mine := tstWriteFile(t, "my script.py")

	cases := []struct {
		command   string
		wantToken string
		wantFile  string // 空串表示没有脚本文件（托管命令 / python -m）
	}{
		{`task -m 5m demo folder/my script.py now -- -u x`, "demo folder/my script.py", spaced},
		{`task ./a.py`, "./a.py", plain},
		{`task a.py b.js`, "a.py", plain},
		{`desi a.py JD_COOKIE 1`, "a.py", plain},
		{`python3 my script.py arg`, "my script.py", mine},
		{`python3 a.py`, "a.py", plain},
		{`dailycheckin --help`, "", ""},
		{`task dailycheckin now`, "", ""},
		{`python3 -m http.server`, "", ""},
	}
	for _, tc := range cases {
		plan, err := ParseCommandExecutionPlan(tc.command, scriptsDir)
		if err != nil {
			t.Fatalf("%s: parse: %v", tc.command, err)
		}
		if plan.ScriptToken != tc.wantToken {
			t.Fatalf("%s: expected ScriptToken %q, got %q", tc.command, tc.wantToken, plan.ScriptToken)
		}
		if tc.wantFile == "" {
			if plan.FullPath != "" {
				t.Fatalf("%s: expected no script file, got %q", tc.command, plan.FullPath)
			}
			continue
		}
		wantFull, err := filepath.EvalSymlinks(tc.wantFile)
		if err != nil {
			t.Fatalf("eval %s: %v", tc.wantFile, err)
		}
		if abs, err := filepath.Abs(wantFull); err == nil {
			wantFull = abs
		}
		if plan.FullPath != wantFull {
			t.Fatalf("%s: FullPath must stay the resolved real path, want %q got %q", tc.command, wantFull, plan.FullPath)
		}
	}
}

func TestResolveTaskScriptTargetClassifiesCommands(t *testing.T) {
	testutil.SetupTestEnv(t)
	tstWriteFile(t, "a.py")
	tstWriteFile(t, "sub/a.py")
	base := tstBase(t)

	outsideDir := filepath.Join(filepath.Dir(config.C.Data.ScriptsDir), "outside")
	if err := os.MkdirAll(outsideDir, 0o755); err != nil {
		t.Fatalf("mkdir outside: %v", err)
	}
	outsideReal, err := filepath.EvalSymlinks(outsideDir)
	if err != nil {
		t.Fatalf("eval outside: %v", err)
	}
	if err := os.WriteFile(filepath.Join(outsideReal, "evil.py"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write outside file: %v", err)
	}
	// 绝对路径一律取 EvalSymlinks 之后的形式：Windows 的短文件名（RUNNER~1）含 ~，会被解析器当成危险字符。
	absInside := filepath.ToSlash(filepath.Join(base.Real, "a.py"))
	absOutsideExisting := filepath.ToSlash(filepath.Join(outsideReal, "evil.py"))
	absOutsideMissing := filepath.ToSlash(filepath.Join(outsideReal, "nope.py"))

	t.Run("script forms resolve to the same file", func(t *testing.T) {
		cases := []struct {
			command    string
			relPath    string
			literalRel string
		}{
			{"task a.py", "a.py", "a.py"},
			{"task ./a.py", "a.py", "a.py"},
			{"python3 a.py", "a.py", "a.py"},
			{"task a.py now", "a.py", "a.py"},
			{"task " + absInside, "a.py", "a.py"},
			{"task sub/a.py", "sub/a.py", "sub/a.py"},
		}
		if runtime.GOOS == "windows" {
			cases = append(cases, struct {
				command    string
				relPath    string
				literalRel string
			}{"task A.PY", "a.py", "A.PY"})
		}
		for _, tc := range cases {
			target := ResolveTaskScriptTarget(tc.command, base)
			if target.Kind != taskScriptKindScript {
				t.Fatalf("%s: expected script kind, got %q", tc.command, target.Kind)
			}
			if target.RelPath != tc.relPath || target.LiteralRel != tc.literalRel || !target.Direct {
				t.Fatalf("%s: unexpected paths rel=%q literal=%q direct=%v", tc.command, target.RelPath, target.LiteralRel, target.Direct)
			}
			if len(target.TextCandidates) != 0 {
				t.Fatalf("%s: resolved scripts must not carry text candidates, got %#v", tc.command, target.TextCandidates)
			}
		}
	})

	t.Run("commands without a script file", func(t *testing.T) {
		managed := ResolveTaskScriptTarget("dailycheckin --help", base)
		if managed.Kind != taskScriptKindManaged || managed.ManagedCommand != "dailycheckin" || managed.RealPath != "" {
			t.Fatalf("unexpected managed target: %#v", managed)
		}
		module := ResolveTaskScriptTarget("python3 -m http.server", base)
		if module.Kind != taskScriptKindModule || module.PythonModule != "http.server" || module.RealPath != "" {
			t.Fatalf("unexpected module target: %#v", module)
		}
	})

	// 失败分类依赖解析器的错误文案（检测到路径穿越 / 危险字符: ..），文案一改这里就会变红。
	t.Run("failure classes pin parser error texts", func(t *testing.T) {
		cases := []struct {
			command string
			kind    string
			hint    string
		}{
			{"task missing.py", taskScriptKindNotFound, "missing.py"},
			{"task sub/missing.py now", taskScriptKindNotFound, "sub/missing.py"},
			{"task " + absOutsideMissing, taskScriptKindNotFound, ""},
			{"task " + absOutsideExisting, taskScriptKindOutside, ""},
			{"task ../x.py", taskScriptKindOutside, ""},
			{"task a&b.js", taskScriptKindUnresolved, ""},
			{"task a.py now extra", taskScriptKindUnresolved, ""},
			{"task -m 5x a.py", taskScriptKindUnresolved, ""},
			{`task "a.py`, taskScriptKindUnresolved, ""},
		}
		for _, tc := range cases {
			target := ResolveTaskScriptTarget(tc.command, base)
			if target.Kind != tc.kind {
				t.Fatalf("%s: expected kind %q, got %q", tc.command, tc.kind, target.Kind)
			}
			if target.HintPath != tc.hint {
				t.Fatalf("%s: expected hint %q, got %q", tc.command, tc.hint, target.HintPath)
			}
			if filepath.IsAbs(target.HintPath) || strings.Contains(target.HintPath, filepath.ToSlash(outsideReal)) {
				t.Fatalf("%s: hint path must never echo an absolute path, got %q", tc.command, target.HintPath)
			}
			if target.RealPath != "" {
				t.Fatalf("%s: failed parses must not produce a deletable path, got %q", tc.command, target.RealPath)
			}
		}
		// 跑不起来但文本指向 a.py 的命令，要能认出 a.py（用于共用判定，宁多勿少）。
		for _, command := range []string{"task a.py now extra", "task -m 5x a.py", `task "a.py`} {
			target := ResolveTaskScriptTarget(command, base)
			if !tstContains(target.TextCandidates, "a.py") {
				t.Fatalf("%s: expected text candidate a.py, got %#v", command, target.TextCandidates)
			}
		}
	})

	t.Run("managed commands expose script-like arguments", func(t *testing.T) {
		cases := []struct {
			command string
			want    string
		}{
			{"python3.13 a.py", "a.py"},
			{"tsx a.ts", "a.ts"},
			{"python3.13 -u ./sub/a.py", "sub/a.py"},
			{"python3.13 " + absInside, "a.py"},
		}
		for _, tc := range cases {
			target := ResolveTaskScriptTarget(tc.command, base)
			if target.Kind != taskScriptKindManaged {
				t.Fatalf("%s: expected managed kind, got %q", tc.command, target.Kind)
			}
			if !tstContains(target.TextCandidates, tc.want) {
				t.Fatalf("%s: expected text candidate %q, got %#v", tc.command, tc.want, target.TextCandidates)
			}
		}
	})
}

// 非 script 的 Kind 也要给出 TextCandidates（A3 module、A4 not_found/outside），用于扩大共用判定。
func TestResolveTaskScriptTargetTextCandidatesForNonScriptKinds(t *testing.T) {
	testutil.SetupTestEnv(t)
	tstWriteFile(t, "a.py")
	base := tstBase(t)

	t.Run("python -m maps to module files and parent package inits", func(t *testing.T) {
		target := ResolveTaskScriptTarget("python3 -m jd.sign", base)
		if target.Kind != taskScriptKindModule {
			t.Fatalf("expected module kind, got %q", target.Kind)
		}
		for _, want := range []string{"jd/sign.py", "jd/sign/__main__.py", "jd/sign/__init__.py", "jd/__init__.py"} {
			if !tstContains(target.TextCandidates, want) {
				t.Fatalf("expected module candidate %q, got %#v", want, target.TextCandidates)
			}
		}
	})

	t.Run("single-segment module maps to top-level file", func(t *testing.T) {
		target := ResolveTaskScriptTarget("python3 -m a", base)
		if target.Kind != taskScriptKindModule || !tstContains(target.TextCandidates, "a.py") {
			t.Fatalf("expected module candidate a.py, got kind=%q candidates=%#v", target.Kind, target.TextCandidates)
		}
	})

	t.Run("broken commands still expose their text candidates", func(t *testing.T) {
		cases := map[string]string{
			"python3 -u a.py":    "a.py", // not_found（"-u a.py" 这个候选路径不存在）
			"task a.py now b.py": "a.py", // not_found（remainder 非法）
			"node --flag a.js":   "a.js", // not_found，文本指向 a.js
		}
		for command, want := range cases {
			target := ResolveTaskScriptTarget(command, base)
			if target.Kind == taskScriptKindScript {
				t.Fatalf("%s: expected a non-script kind, got script", command)
			}
			if !tstContains(target.TextCandidates, want) {
				t.Fatalf("%s: expected text candidate %q, got kind=%q %#v", command, want, target.Kind, target.TextCandidates)
			}
		}
	})
}

// R2-05：直接钉住 pathHasLinkSegment（此前测试里没有任何直接调用，把它整个短路成 return false, nil 在 Linux 上也全绿）。
// 软链接用 os.Symlink 构造：Linux 上必跑；Windows 没有权限时只跳过软链接那几条，目录联接由
// task_script_cleanup_windows_test.go 兜底。
func TestPathHasLinkSegment(t *testing.T) {
	testutil.SetupTestEnv(t)
	scriptsDir := config.C.Data.ScriptsDir
	tstWriteFile(t, "plain/dir/ok.py")
	tstWriteFile(t, "realdir/x.py")
	tstWriteFile(t, "a.py")
	base := tstBase(t)

	expectHit := func(t *testing.T, literalAbs string, want bool) {
		t.Helper()
		hit, err := pathHasLinkSegment(base, literalAbs)
		if err != nil {
			t.Fatalf("%s: unexpected error: %v", literalAbs, err)
		}
		if hit != want {
			t.Fatalf("%s: expected hit=%v, got %v", literalAbs, want, hit)
		}
	}
	expectErr := func(t *testing.T, literalAbs string) {
		t.Helper()
		hit, err := pathHasLinkSegment(base, literalAbs)
		if err == nil || hit {
			t.Fatalf("%s: expected an error and no hit, got hit=%v err=%v", literalAbs, hit, err)
		}
	}

	t.Run("plain nested file", func(t *testing.T) {
		expectHit(t, filepath.Join(base.Abs, "plain", "dir", "ok.py"), false)
	})
	t.Run("empty literal path", func(t *testing.T) {
		expectHit(t, "", false)
	})
	t.Run("missing middle segment is an error", func(t *testing.T) {
		expectErr(t, filepath.Join(base.Abs, "missing", "ok.py"))
	})
	t.Run("path outside the scripts dir is an error", func(t *testing.T) {
		expectErr(t, filepath.Join(filepath.Dir(base.Abs), "x.py"))
	})
	t.Run("symlinked middle directory", func(t *testing.T) {
		tstSymlinkOrSkip(t, filepath.Join(scriptsDir, "realdir"), filepath.Join(scriptsDir, "linkdir"))
		expectHit(t, filepath.Join(base.Abs, "linkdir", "x.py"), true)
	})
	t.Run("symlinked last segment", func(t *testing.T) {
		tstSymlinkOrSkip(t, filepath.Join(scriptsDir, "a.py"), filepath.Join(scriptsDir, "link.py"))
		expectHit(t, filepath.Join(base.Abs, "link.py"), true)
	})
	// 逐段检查只从脚本目录往下查：脚本目录本身挂在软链接下（Docker / Magisk）不计，字面路径写 Abs 或 Real 都一样。
	t.Run("symlinked scripts dir itself is not counted", func(t *testing.T) {
		root := filepath.Dir(scriptsDir)
		realScripts := filepath.Join(root, "real-scripts")
		if err := os.MkdirAll(realScripts, 0o755); err != nil {
			t.Fatalf("mkdir real scripts: %v", err)
		}
		if err := os.WriteFile(filepath.Join(realScripts, "a.py"), []byte("x"), 0o644); err != nil {
			t.Fatalf("write script: %v", err)
		}
		linkScripts := filepath.Join(root, "link-scripts")
		tstSymlinkOrSkip(t, realScripts, linkScripts)
		linkBase, err := resolveScriptsBase(linkScripts)
		if err != nil {
			t.Fatalf("resolve base: %v", err)
		}
		for _, literal := range []string{filepath.Join(linkBase.Abs, "a.py"), filepath.Join(linkBase.Real, "a.py")} {
			if hit, err := pathHasLinkSegment(linkBase, literal); err != nil || hit {
				t.Fatalf("%s: the scripts dir's own symlink must not count, got hit=%v err=%v", literal, hit, err)
			}
		}
	})
}

// 末级是软链接：真实相对路径是链接目标，字面路径是链接本身，Direct=false。
func TestResolveTaskScriptTargetSymlinkLastSegment(t *testing.T) {
	testutil.SetupTestEnv(t)
	scriptsDir := config.C.Data.ScriptsDir
	tstWriteFile(t, "a.py")
	tstSymlinkOrSkip(t, filepath.Join(scriptsDir, "a.py"), filepath.Join(scriptsDir, "link.py"))

	target := ResolveTaskScriptTarget("task link.py", tstBase(t))
	if target.Kind != taskScriptKindScript || target.RelPath != "a.py" || target.LiteralRel != "link.py" || target.Direct {
		t.Fatalf("unexpected target for symlinked script: %#v", target)
	}
}

// 中间某一段目录是软链接：同样 Direct=false。
func TestResolveTaskScriptTargetSymlinkMiddleDir(t *testing.T) {
	testutil.SetupTestEnv(t)
	scriptsDir := config.C.Data.ScriptsDir
	tstWriteFile(t, "realdir/x.py")
	tstSymlinkOrSkip(t, filepath.Join(scriptsDir, "realdir"), filepath.Join(scriptsDir, "linkdir"))

	target := ResolveTaskScriptTarget("task linkdir/x.py", tstBase(t))
	if target.Kind != taskScriptKindScript || target.RelPath != "realdir/x.py" || target.LiteralRel != "linkdir/x.py" || target.Direct {
		t.Fatalf("unexpected target for script under a symlinked dir: %#v", target)
	}
}

// 脚本目录本身挂在软链接下（Docker / Magisk）：相对路径必须是 a.py，不能是 ../real-scripts/a.py，
// 也不能因此被误判成「经过了软链接」。
func TestResolveTaskScriptTargetSymlinkScriptsDir(t *testing.T) {
	testutil.SetupTestEnv(t)
	root := filepath.Dir(config.C.Data.ScriptsDir)
	realScripts := filepath.Join(root, "real-scripts")
	if err := os.MkdirAll(realScripts, 0o755); err != nil {
		t.Fatalf("mkdir real scripts: %v", err)
	}
	if err := os.WriteFile(filepath.Join(realScripts, "a.py"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write script: %v", err)
	}
	linkScripts := filepath.Join(root, "link-scripts")
	tstSymlinkOrSkip(t, realScripts, linkScripts)

	base, err := resolveScriptsBase(linkScripts)
	if err != nil {
		t.Fatalf("resolve base: %v", err)
	}
	if samePathText(base.Abs, base.Real) {
		t.Fatalf("expected symlinked scripts dir to have distinct Abs and Real, got %#v", base)
	}

	commands := []string{
		"task a.py",
		"task " + filepath.ToSlash(filepath.Join(base.Abs, "a.py")),
		"task " + filepath.ToSlash(filepath.Join(base.Real, "a.py")),
	}
	for _, command := range commands {
		target := ResolveTaskScriptTarget(command, base)
		if target.Kind != taskScriptKindScript || target.RelPath != "a.py" || target.LiteralRel != "a.py" || !target.Direct {
			t.Fatalf("%s: unexpected target under symlinked scripts dir: %#v", command, target)
		}
	}
}
