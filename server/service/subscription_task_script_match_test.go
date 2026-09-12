package service

import (
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"

	"daidai-panel/config"
	"daidai-panel/testutil"
)

// 「按脚本认任务」（#125）的纯函数单测：字典、归一化、求键、删除分支的判定。
// 同步整条链路的行为在 subscription_task_sync_existing_test.go。

// stmIndex 用相对脚本目录的正斜杠路径造候选；命令与 collect 的产物同形（本机分隔符）。
func stmIndex(t *testing.T, rels ...string) subscriptionScriptIndex {
	t.Helper()
	candidates := make(map[string]subscriptionTaskCandidate, len(rels))
	for _, rel := range rels {
		command := "task " + filepath.FromSlash(rel)
		candidates[command] = subscriptionTaskCandidate{Name: rel, Command: command}
	}
	return newSubscriptionScriptIndex(config.C.Data.ScriptsDir, candidates)
}

func stmWriteFile(t *testing.T, rel string) string {
	t.Helper()
	full := filepath.Join(config.C.Data.ScriptsDir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte("//cron: 1 1 * * *\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return full
}

type stmKeyCase struct {
	name    string
	command string
	want    string // 空串 = 不该有键
}

func stmAssertKeys(t *testing.T, idx subscriptionScriptIndex, cases []stmKeyCase) {
	t.Helper()
	for _, tc := range cases {
		got, ok := idx.scriptKeyOf(tc.command)
		if tc.want == "" {
			if ok {
				t.Errorf("%s: %q want no key, got %q", tc.name, tc.command, got)
			}
			continue
		}
		if !ok || got != tc.want {
			t.Errorf("%s: %q want %q, got %q ok=%v", tc.name, tc.command, tc.want, got, ok)
		}
	}
}

func TestSubscriptionScriptIndexScriptKeyOf(t *testing.T) {
	testutil.SetupTestEnv(t)
	scriptsDir := config.C.Data.ScriptsDir
	idx := stmIndex(t, "repo/biz.js", "repo/sub dir/my script.js", "repo/tool.py", "repo/run.sh")

	abs, err := filepath.Abs(filepath.Join(scriptsDir, "repo", "biz.js"))
	if err != nil {
		t.Fatal(err)
	}
	outside, err := filepath.Abs(filepath.Join(scriptsDir, "..", "repo", "biz.js"))
	if err != nil {
		t.Fatal(err)
	}

	cases := []stmKeyCase{
		{"plain", "task repo/biz.js", "repo/biz.js"},
		{"now", "task repo/biz.js now", "repo/biz.js"},
		{"double and trailing spaces", "task  repo/biz.js  now  ", "repo/biz.js"},
		{"desi after path", "task repo/biz.js desi JD_COOKIE", "repo/biz.js"},
		{"desi with range", "task repo/biz.js desi JD_COOKIE 1-3", "repo/biz.js"},
		{"conc", "task repo/biz.js conc JD_COOKIE", "repo/biz.js"},
		{"desi first", "desi repo/biz.js JD_COOKIE 1", "repo/biz.js"},
		{"-m", "task -m 30m repo/biz.js now", "repo/biz.js"},
		{"-l", "task -l repo/biz.js", "repo/biz.js"},
		{"-m -l --", "task -m 5m -l repo/biz.js -- --x", "repo/biz.js"},
		{"-- cuts script args", "task repo/biz.js -- repo/tool.py", "repo/biz.js"},
		{"-- before path", "task -- repo/biz.js", ""},
		{"-m without value", "task -m", ""},
		{"spaces in path", "task repo/sub dir/my script.js now", "repo/sub dir/my script.js"},
		{"double-quoted path", `task "repo/sub dir/my script.js" now`, "repo/sub dir/my script.js"},
		{"single-quoted path", `task 'repo/biz.js' now`, "repo/biz.js"},
		{"dot slash", "task ./repo/biz.js now", "repo/biz.js"},
		{"inner dot", "task repo/./biz.js", "repo/biz.js"},
		{"absolute inside", "task " + abs + " now", "repo/biz.js"},
		{"absolute inside, forward slashes", "task " + filepath.ToSlash(abs) + " now", "repo/biz.js"},
		{"absolute outside", "task " + outside, ""},
		{"parent", "task ../repo/biz.js", ""},
		{"climbs out", "task repo/../../x/repo/biz.js", ""},
		{".bak is not a script", "task repo/biz.js.bak", ""},
		{"not a candidate", "task repo/other.js", ""},
		{"unclosed quote", `task "repo/biz.js`, ""},
		{"apostrophe opens a quote", "task repo/it's.js", ""},
		{"empty", "", ""},
		{"bare task", "task", ""},
		{"interpreter node", "node repo/biz.js", "repo/biz.js"},
		{"interpreter flag", "node --max-old-space-size=4096 repo/biz.js", "repo/biz.js"},
		{"interpreter python3", "python3 repo/tool.py", "repo/tool.py"},
		{"interpreter bash", "bash repo/run.sh", "repo/run.sh"},
		{"python -m is a module", "python3 -m repo/tool.py", ""},
		{"managed command", "mytool repo/biz.js --flag", "repo/biz.js"},
		{"managed command with flag", "mytool --config repo/biz.js", "repo/biz.js"},
		{"unsupported first word", "/usr/bin/node repo/biz.js", ""},
	}
	if runtime.GOOS == "windows" {
		cases = append(cases,
			stmKeyCase{"windows backslash", `task repo\biz.js now`, "repo/biz.js"},
			stmKeyCase{"windows dot backslash", `task .\repo\biz.js`, "repo/biz.js"},
			stmKeyCase{"windows case", "task REPO/BIZ.JS now", "repo/biz.js"},
			stmKeyCase{"windows case desi", "desi Repo/Biz.js JD_COOKIE", "repo/biz.js"},
		)
	} else {
		// Linux 上反斜杠是文件名字符、大小写敏感，与执行器一致：认不出。
		cases = append(cases,
			stmKeyCase{"linux backslash", `task repo\biz.js now`, ""},
			stmKeyCase{"linux case", "task REPO/BIZ.JS now", ""},
		)
	}
	stmAssertKeys(t, idx, cases)
}

func TestSubscriptionScriptIndexNormalize(t *testing.T) {
	testutil.SetupTestEnv(t)
	idx := stmIndex(t)
	type nc struct{ in, want string }
	cases := []nc{
		{"repo/biz.js", "repo/biz.js"},
		{"./repo/biz.js", "repo/biz.js"},
		{"repo//biz.js", "repo/biz.js"},
		{"", ""},
		{".", ""},
		{"..", ""},
		{"../x.js", ""},
	}
	if runtime.GOOS == "windows" {
		cases = append(cases, nc{`repo\Biz.js`, "repo/biz.js"}, nc{`\repo\biz.js`, "repo/biz.js"})
	} else {
		cases = append(cases, nc{`repo\Biz.js`, `repo\Biz.js`})
	}
	for _, tc := range cases {
		got, ok := idx.normalize(tc.in)
		if tc.want == "" {
			if ok {
				t.Errorf("normalize(%q) want no key, got %q", tc.in, got)
			}
			continue
		}
		if !ok || got != tc.want {
			t.Errorf("normalize(%q) want %q, got %q ok=%v", tc.in, tc.want, got, ok)
		}
	}

	if prefix, ok := idx.saveDirKeyPrefix("repo"); !ok || prefix != "repo/" {
		t.Errorf("saveDirKeyPrefix(repo) = %q,%v", prefix, ok)
	}
	if prefix, ok := idx.saveDirKeyPrefix("a/../b"); !ok || prefix != "b/" {
		t.Errorf("saveDirKeyPrefix(a/../b) = %q,%v", prefix, ok)
	}
	if prefix, ok := idx.saveDirKeyPrefix(""); !ok || prefix != "" {
		t.Errorf("saveDirKeyPrefix(\"\") should be the scripts dir itself, got %q,%v", prefix, ok)
	}
	if _, ok := idx.saveDirKeyPrefix(".."); ok {
		t.Error("saveDirKeyPrefix(..) must be outside")
	}
}

// Windows 下仅大小写不同的两个候选归一到同一键：两者都不参与认领；但这个键仍用于删除分支的「保留」。
func TestSubscriptionScriptIndexCandidateKeyCollision(t *testing.T) {
	testutil.SetupTestEnv(t)
	idx := stmIndex(t, "repo/A.js", "repo/a.js")
	upper := "task " + filepath.Join("repo", "A.js")
	lower := "task " + filepath.Join("repo", "a.js")
	_, okUpper := idx.candidateKey(upper)
	_, okLower := idx.candidateKey(lower)
	key, keyOK := idx.scriptKeyOf(lower + " now")
	if runtime.GOOS == "windows" {
		if okUpper || okLower {
			t.Fatal("windows: case-colliding candidates must not be used for claiming")
		}
		if !keyOK || key != "repo/a.js" {
			t.Fatalf("windows: the ambiguous key must still be recognised for keeping, got %q,%v", key, keyOK)
		}
		return
	}
	if !okUpper || !okLower {
		t.Fatal("linux: case-distinct candidates are different scripts")
	}
	if !keyOK || key != "repo/a.js" {
		t.Fatalf("linux: got %q,%v", key, keyOK)
	}
}

func TestSubscriptionTaskCommandScriptRefs(t *testing.T) {
	testutil.SetupTestEnv(t)
	idx := stmIndex(t)
	displays := func(command string) []string {
		var out []string
		for _, ref := range idx.taskCommandScriptRefs(command) {
			out = append(out, ref.display)
		}
		return out
	}
	assert := func(command string, want ...string) {
		t.Helper()
		got := displays(command)
		if len(got) != len(want) {
			t.Fatalf("%q: want %v, got %v", command, want, got)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("%q: want %v, got %v", command, want, got)
			}
		}
	}
	assert("task -l -m 5m repo/a.js x.js -- y.js", "repo/a.js x.js", "repo/a.js")
	assert("desi repo/a.js JD_COOKIE", "repo/a.js")
	assert("task repo/gone.js now", "repo/gone.js")
	assert("task ../x.js")
	assert("node repo/a.js")
	assert(`task "repo/a.js`)
}

// 扫描漏读（目录联接、NAS / Magisk 读目录异常）：人为从「读到的文件」里拿掉一个文件，
// 它还在当前订阅目录里、文件也在 → 保留并给出路径；被扫描看到却不是候选（被规则排除）、文件不在、
// 在订阅目录外的一律删。
func TestSubscriptionStaleTaskJudgeKeepsUnscannedFile(t *testing.T) {
	testutil.SetupTestEnv(t)
	scriptsDir := config.C.Data.ScriptsDir
	keep := stmWriteFile(t, "repo/keep.js")
	stmWriteFile(t, "repo/sub/b.js")
	excluded := stmWriteFile(t, "repo/excluded.js")
	stmWriteFile(t, "other/c.js")

	idx := stmIndex(t, "repo/keep.js")
	candidates := map[string]subscriptionTaskCandidate{"task " + filepath.Join("repo", "keep.js"): {}}
	// 故意缺 repo/sub/b.js：模拟扫描没读到那个子目录。
	seen := subscriptionScannedFileKeys(idx, scriptsDir, []string{keep, excluded})
	judge := newSubscriptionStaleTaskJudge(idx, candidates, "repo", seen)

	type jc struct {
		command  string
		want     subscriptionStaleTaskVerdict
		wantPath string
	}
	cases := []jc{
		{"task " + filepath.Join("repo", "keep.js"), staleTaskKeep, ""},
		{"task repo/keep.js now", staleTaskKeep, ""},
		{"task repo/sub/b.js desi JD_COOKIE", staleTaskKeepUnscanned, "repo/sub/b.js"},
		{"task repo/sub/b.js", staleTaskKeepUnscanned, "repo/sub/b.js"},
		// 执行器跑的是「最长的、真实存在的前缀」：x.js 只是参数。
		{"task repo/sub/b.js extra.js", staleTaskKeepUnscanned, "repo/sub/b.js"},
		{"task repo/excluded.js now", staleTaskDelete, ""},
		{"task repo/gone.js now", staleTaskDelete, ""},
		{"task other/c.js", staleTaskDelete, ""},
		{"task ../outside.js", staleTaskDelete, ""},
	}
	for _, tc := range cases {
		got, path, _ := judge.judge(tc.command)
		if got != tc.want || path != tc.wantPath {
			t.Errorf("%q: want (%d,%q), got (%d,%q)", tc.command, tc.want, tc.wantPath, got, path)
		}
	}

	// 同一个文件出现在「读到的文件」里就不再算漏读：它不是候选，只能是被规则排除了。
	seenAll := subscriptionScannedFileKeys(idx, scriptsDir, []string{keep, excluded, filepath.Join(scriptsDir, "repo", "sub", "b.js")})
	if got, _, _ := newSubscriptionStaleTaskJudge(idx, candidates, "repo", seenAll).judge("task repo/sub/b.js"); got != staleTaskDelete {
		t.Fatalf("a scanned non-candidate must be deleted, got %d", got)
	}
}

// stmStubStat 让删除判定的 Stat 对路径（正斜杠形式）含 match 的返回 *PathError{Err: errFor}，其余照常；测试结束恢复。
func stmStubStat(t *testing.T, match string, errFor error) {
	t.Helper()
	orig := subscriptionScriptStat
	t.Cleanup(func() { subscriptionScriptStat = orig })
	subscriptionScriptStat = func(name string) (fs.FileInfo, error) {
		if strings.Contains(filepath.ToSlash(name), match) {
			return nil, &fs.PathError{Op: "stat", Path: name, Err: errFor}
		}
		return orig(name)
	}
}

// review-r1 F2 / review-r2 R2-1 / verify-r3 R3-1：只有「不存在」类（ENOENT / ENOTDIR / ErrNotExist）与名字类（ENAMETOOLONG /
// ELOOP / EINVAL，Windows 的 123 / 161 / 206 / 267 / 1921）Stat 错误算「不是脚本」，按删除规则删；其余错误（EACCES / EIO / ESTALE，
// Windows 网络盘的 59 / 64 / 121 / 1117，Linux 的 EHOSTDOWN / ECONNRESET）在脚本位于当前订阅目录内时一律保留，并带上错误原文；
// 订阅目录外照删；更长的前缀报错、更短的前缀真实存在时按更短的那个判（与执行器同口径）。
// 真实的 chmod 000 场景见 TestSyncSubscriptionTasksUnreadableSubdirKeepsTask（只在非 root 的 Linux 上跑）。
func TestSubscriptionStaleTaskJudgeStatErrorIsNotAbsence(t *testing.T) {
	testutil.SetupTestEnv(t)
	scriptsDir := config.C.Data.ScriptsDir
	keep := stmWriteFile(t, "repo/keep.js")
	stmWriteFile(t, "repo/sub/b.js")
	excluded := stmWriteFile(t, "repo/excluded.js")
	stmWriteFile(t, "other/c.js")

	idx := stmIndex(t, "repo/keep.js")
	candidates := map[string]subscriptionTaskCandidate{"task " + filepath.Join("repo", "keep.js"): {}}
	// 子目录读不了，扫描也就没读到 repo/sub 下的文件。
	seen := subscriptionScannedFileKeys(idx, scriptsDir, []string{keep, excluded})
	judge := newSubscriptionStaleTaskJudge(idx, candidates, "repo", seen)

	type jc struct {
		command    string
		want       subscriptionStaleTaskVerdict
		wantScript string
		wantErr    string
	}
	check := func(t *testing.T, cases ...jc) {
		t.Helper()
		for _, tc := range cases {
			got, script, statErr := judge.judge(tc.command)
			if got != tc.want || script != tc.wantScript || statErr != tc.wantErr {
				t.Errorf("%q: want (%d,%q,%q), got (%d,%q,%q)", tc.command, tc.want, tc.wantScript, tc.wantErr, got, script, statErr)
			}
		}
	}

	for _, tc := range []struct {
		name  string
		errno syscall.Errno
	}{{"EACCES", syscall.EACCES}, {"EIO", syscall.EIO}, {"ESTALE", syscall.ESTALE}} {
		t.Run("unsure_"+tc.name, func(t *testing.T) {
			stmStubStat(t, "/repo/sub/", tc.errno)
			msg := tc.errno.Error()
			check(t,
				jc{"task repo/sub/b.js", staleTaskKeepStatError, "repo/sub/b.js", msg},
				jc{"task repo/sub/b.js desi JD_COOKIE", staleTaskKeepStatError, "repo/sub/b.js", msg},
				// 目录进不去时，连「真的不存在」也分辨不出来，同样保留。
				jc{"task repo/sub/gone.js", staleTaskKeepStatError, "repo/sub/gone.js", msg},
			)
		})
	}

	for _, tc := range []struct {
		name string
		err  error
	}{{"ENOENT", syscall.ENOENT}, {"ENOTDIR", syscall.ENOTDIR}, {"ErrNotExist", fs.ErrNotExist}} {
		t.Run("missing_"+tc.name, func(t *testing.T) {
			stmStubStat(t, "/repo/sub/", tc.err)
			check(t, jc{"task repo/sub/b.js", staleTaskDelete, "", ""})
		})
	}

	t.Run("outside_save_dir", func(t *testing.T) {
		stmStubStat(t, "/other/", syscall.EACCES)
		check(t, jc{"task other/c.js", staleTaskDelete, "", ""})
	})

	t.Run("shorter_prefix_exists", func(t *testing.T) {
		// 更长的前缀（文件名带空格）报 EACCES、更短的前缀真实存在：执行器也退到更短的那个
		// （resolveCommandScriptPath → ResolveWithinBase(mustExist=true) 对 EACCES 同样报错，findTaskScriptTarget
		// 只保留能解析成功的最长前缀），judge 与它同口径，按更短的判。
		stmStubStat(t, " x.js", syscall.EACCES)
		check(t,
			jc{"task repo/sub/b.js x.js", staleTaskKeepUnscanned, "repo/sub/b.js", ""},
			jc{"task repo/excluded.js x.js", staleTaskDelete, "", ""},
		)
	})

	t.Run("real_enotdir", func(t *testing.T) {
		// 真实文件系统：路径中间某一段是普通文件。Linux 上 Stat 报 ENOTDIR（不是 ENOENT），也算不在。
		check(t, jc{"task repo/excluded.js/b.js", staleTaskDelete, "", ""})
	})

	// review-r2 R2-1：名字类错误（命令参数里带 URL / 盘符 / ? * | < >、超长段、软链接环）说明这个前缀根本不可能是一个文件，
	// 与「不存在」类同路按「不是脚本」处理，退到更短的前缀；都不在则按删除规则删。
	for _, tc := range []struct {
		name  string
		errno syscall.Errno
	}{{"ENAMETOOLONG", syscall.ENAMETOOLONG}, {"ELOOP", syscall.ELOOP}, {"EINVAL", syscall.EINVAL}} {
		t.Run("not_a_script_"+tc.name, func(t *testing.T) {
			stmStubStat(t, "/repo/sub/", tc.errno)
			check(t, jc{"task repo/sub/b.js", staleTaskDelete, "", ""})
		})
	}
	// Windows 的名字类错误码：参数带 URL / 盘符时 os.Stat 真实报的是 123；Go 在 Windows 上的 ENAMETOOLONG / ELOOP 是自造值，不会出现。
	for _, tc := range []struct {
		name  string
		errno syscall.Errno
	}{
		{"ERROR_INVALID_NAME", 123}, {"ERROR_BAD_PATHNAME", 161}, {"ERROR_FILENAME_EXCED_RANGE", 206},
		{"ERROR_DIRECTORY", 267}, {"ERROR_CANT_RESOLVE_FILENAME", 1921},
	} {
		t.Run("not_a_script_windows_"+tc.name, func(t *testing.T) {
			if runtime.GOOS != "windows" {
				t.Skip("Windows 错误码的数值在 Unix 上是另一个 errno（123 在 Linux 上是 ENOMEDIUM），只在 Windows 上有意义")
			}
			stmStubStat(t, "/repo/sub/", tc.errno)
			check(t, jc{"task repo/sub/b.js", staleTaskDelete, "", ""})
		})
	}

	// verify-r3 R3-1：判定是反过来的，只有「不存在」类与名字类算「不是脚本」，其余 Stat 错误不靠任何列表、一律保留。
	// fix-r2 的白名单在 Windows 上几乎是空的：Go 为 Windows 定义的 syscall.EIO / ESTALE / ETIMEDOUT / ENOTCONN 是自造值，
	// os.Stat 永远不会返回；网络盘、SMB 断连时真实报的是下面这些 Windows 错误码，曾被判成「不是脚本」、任务连日志删掉。
	unsureCheck := func(t *testing.T, errno syscall.Errno) {
		t.Helper()
		stmStubStat(t, "/repo/sub/", errno)
		msg := errno.Error()
		check(t,
			jc{"task repo/sub/b.js", staleTaskKeepStatError, "repo/sub/b.js", msg},
			jc{"task repo/sub/b.js desi JD_COOKIE", staleTaskKeepStatError, "repo/sub/b.js", msg},
		)
	}
	for _, tc := range []struct {
		name  string
		errno syscall.Errno
	}{{"ERROR_UNEXP_NET_ERR", 59}, {"ERROR_NETNAME_DELETED", 64}, {"ERROR_SEM_TIMEOUT", 121}, {"ERROR_IO_DEVICE", 1117}} {
		t.Run("unsure_windows_"+tc.name, func(t *testing.T) {
			if runtime.GOOS != "windows" {
				t.Skip("Windows 错误码的数值在 Unix 上是另一个 errno（64 在 Linux 上是 ENONET），只在 Windows 上有意义；Unix 的同类场景见 unsure_unix_*")
			}
			unsureCheck(t, tc.errno)
		})
	}
	// Linux 上 CIFS / NFS 断连同理：EHOSTDOWN（Host is down）、ECONNRESET 不在任何列表里，也要保留。
	for _, tc := range []struct {
		name  string
		errno syscall.Errno
	}{{"EHOSTDOWN", syscall.EHOSTDOWN}, {"ECONNRESET", syscall.ECONNRESET}} {
		t.Run("unsure_unix_"+tc.name, func(t *testing.T) {
			if runtime.GOOS == "windows" {
				t.Skip("Go 在 Windows 上的 syscall.EHOSTDOWN / ECONNRESET 是自造值，os.Stat 永远不会返回；Windows 的同类场景见 unsure_windows_*")
			}
			unsureCheck(t, tc.errno)
		})
	}

	// 名字类的前缀按「不是脚本」跳过，不会顶掉后面真正的脚本：最长前缀（参数带 URL）报名字类错误、脚本本身报 IO 错误时，
	// 保留提示里给出的是脚本本身的路径与错误。
	t.Run("bad_name_prefix_then_unsure_script", func(t *testing.T) {
		orig := subscriptionScriptStat
		t.Cleanup(func() { subscriptionScriptStat = orig })
		subscriptionScriptStat = func(name string) (fs.FileInfo, error) {
			slashed := filepath.ToSlash(name)
			switch {
			case strings.Contains(slashed, "https:"):
				return nil, &fs.PathError{Op: "stat", Path: name, Err: syscall.EINVAL}
			case strings.Contains(slashed, "/repo/sub/"):
				return nil, &fs.PathError{Op: "stat", Path: name, Err: syscall.EIO}
			}
			return orig(name)
		}
		check(t, jc{"task repo/sub/b.js https://cdn.example.com/lib/x.js", staleTaskKeepStatError, "repo/sub/b.js", syscall.EIO.Error()})
	})
}

// Docker 青龙兼容层把 /ql/data/scripts 等软链到脚本目录（docker/entrypoint.sh）。
// 命令写别名的绝对路径时，要对 Real(ScriptsDir) 再求一次；只解析所在目录，文件软链接保持自己的键。
func TestSubscriptionScriptIndexResolvesScriptsDirAlias(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Docker 青龙别名是 Linux 软链接场景，在 WSL 里跑交叉编译的测试二进制覆盖")
	}
	testutil.SetupTestEnv(t)
	scriptsDir := config.C.Data.ScriptsDir
	stmWriteFile(t, "repo/a.js")
	real := stmWriteFile(t, "repo/real.js")
	if err := os.Symlink(real, filepath.Join(scriptsDir, "repo", "link.js")); err != nil {
		t.Fatalf("symlink link.js: %v", err)
	}
	alias := filepath.Join(filepath.Dir(scriptsDir), "qlalias")
	if err := os.Symlink(scriptsDir, alias); err != nil {
		t.Fatalf("symlink alias: %v", err)
	}
	idx := stmIndex(t, "repo/a.js", "repo/link.js", "repo/real.js")
	stmAssertKeys(t, idx, []stmKeyCase{
		{"alias desi", "task " + filepath.Join(alias, "repo", "a.js") + " desi JD_COOKIE", "repo/a.js"},
		{"alias node", "node " + filepath.Join(alias, "repo", "a.js"), "repo/a.js"},
		{"alias keeps file symlink key", "task " + filepath.Join(alias, "repo", "link.js") + " now", "repo/link.js"},
		{"alias to missing candidate", "task " + filepath.Join(alias, "repo", "gone.js"), ""},
		{"dangling dir", "task " + filepath.Join(filepath.Dir(scriptsDir), "nope", "repo", "a.js"), ""},
	})

	refs := idx.taskCommandScriptRefs("task " + filepath.Join(alias, "repo", "a.js") + " desi X")
	if len(refs) != 1 || refs[0].key != "repo/a.js" || refs[0].full != filepath.Join(alias, "repo", "a.js") {
		t.Fatalf("alias ref should key to repo/a.js and keep the literal path for Stat, got %#v", refs)
	}
}
