//go:build windows

package service

import (
	"encoding/binary"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"

	"daidai-panel/config"
)

// tscMakeJunction 用 cmd /c mklink /J 建 NTFS 目录联接（junction）。
// mklink /J 不需要管理员权限，所以这里出错就 Fatalf，绝不 t.Skip（联接判定是 A1 的核心防护）。
func tscMakeJunction(t *testing.T, link, target string) {
	t.Helper()
	out, err := exec.Command("cmd", "/c", "mklink", "/J", link, target).CombinedOutput()
	if err != nil {
		t.Fatalf("mklink /J %q %q failed: %v (%s)", link, target, err, out)
	}
}

// A1：Windows 目录联接（junction）从 Go 1.23 起不再报 os.ModeSymlink、EvalSymlinks 也不解析它，
// 于是任务命令能「穿过」联接指向脚本目录外，或绕过订阅目录 / node_modules 保护。
// 逐段 Lstat + FILE_ATTRIBUTE_REPARSE_POINT 补这道闸：命中一律判 symlink，文件全部保留。
// 覆盖三种情形：指向目录外、指向 git 订阅目录（带 .git）、指向 node_modules。
func TestTaskScriptJunctionKept(t *testing.T) {
	env := tscSetup(t)
	scriptsDir := config.C.Data.ScriptsDir

	// 1) 指向脚本目录外。
	outsideDir := filepath.Join(filepath.Dir(scriptsDir), "external")
	if err := os.MkdirAll(outsideDir, 0o755); err != nil {
		t.Fatalf("mkdir outside: %v", err)
	}
	outsideVictim := filepath.Join(outsideDir, "victim.py")
	if err := os.WriteFile(outsideVictim, []byte("keep"), 0o644); err != nil {
		t.Fatalf("write outside victim: %v", err)
	}
	tscMakeJunction(t, filepath.Join(scriptsDir, "linkOutside"), outsideDir)

	// 2) 指向 git 订阅目录（带 .git）。直接写 task repo/x.js 会被判 subscription_managed（对照），
	//    但穿过联接写 task linkGit/x.js 在没有 A1 时会被判可删。
	repoFile := tscWriteFile(t, "repo/x.js", "keep")
	tscMkdir(t, "repo/.git")
	tscMakeJunction(t, filepath.Join(scriptsDir, "linkGit"), filepath.Join(scriptsDir, "repo"))

	// 3) 指向 node_modules。直接写 task node_modules/lib.js 会被判 hidden_path（对照）。
	nmFile := tscWriteFile(t, "node_modules/lib.js", "keep")
	tscMakeJunction(t, filepath.Join(scriptsDir, "linkNM"), filepath.Join(scriptsDir, "node_modules"))

	cases := []struct {
		script string
		full   string
	}{
		{"linkOutside/victim.py", outsideVictim},
		{"linkGit/x.js", repoFile},
		{"linkNM/lib.js", nmFile},
	}
	var ids []uint
	for _, tc := range cases {
		ids = append(ids, tscCreateTask(t, "junction-"+tc.script, "task "+tc.script).ID)
	}

	preview := PreviewTaskScriptDeletion(ids, env)
	for _, tc := range cases {
		item := tscItem(t, preview.Scripts, tc.script)
		tscAssertKept(t, item, TaskScriptReasonSymlink)
	}

	// 执行阶段：一个都不删；联接目标与目录里的文件都还在。
	cleanup := CollectTaskScriptTargets(ids, env)
	tscDeleteTaskRows(t, ids...)
	result := cleanup.Execute(nil)
	if len(result.Deleted) != 0 {
		t.Fatalf("junction paths must never be deleted, got %#v", result.Deleted)
	}
	for _, tc := range cases {
		tscAssertExists(t, tc.full)
	}
}

// R2-2：脚本目录的「上级」是 NTFS 目录联接（mklink /J 把 data 目录挪到别的盘）时，Go 的 EvalSymlinks 会在这一段报错。
// resolveScriptsBase 要退回 Real=Abs（与执行侧 resolveExistingPath 同口径），删脚本功能照常可用，而不是所有脚本恒判
// check_failed；脚本目录内指向外部的联接仍由 pathHasLinkSegment 拦住，根目录的全局钩子仍按 managed_helper 保留。
func TestTaskScriptJunctionAboveScriptsDir(t *testing.T) {
	tscSetup(t)
	dataDir := filepath.Dir(config.C.Data.ScriptsDir)
	realParent := filepath.Join(dataDir, "real-parent")
	if err := os.MkdirAll(filepath.Join(realParent, "scripts"), 0o755); err != nil {
		t.Fatalf("mkdir real parent: %v", err)
	}
	linkParent := filepath.Join(dataDir, "link-parent")
	tscMakeJunction(t, linkParent, realParent)
	scriptsDir := filepath.Join(linkParent, "scripts")
	// tscWriteFile / tscMkdir 按 config 里的脚本目录写文件，改成经过联接的这条路径。
	config.C.Data.ScriptsDir = scriptsDir
	env := TaskScriptCleanupEnv{ScriptsDir: scriptsDir}

	// 前提：EvalSymlinks 确实会在上级联接这一段报错（Go 1.23 起默认 winsymlink=1）；否则测不到 R2-2 的退回分支。
	if _, err := filepath.EvalSymlinks(scriptsDir); err == nil {
		t.Fatalf("premise broken: EvalSymlinks(%q) no longer fails under a junction parent; revisit resolveScriptsBase and this test", scriptsDir)
	}
	base, err := resolveScriptsBase(scriptsDir)
	if err != nil {
		t.Fatalf("a junction above the scripts dir must not fail resolveScriptsBase: %v", err)
	}
	if base.Real != base.Abs {
		t.Fatalf("expected Real to fall back to Abs, got %#v", base)
	}

	plain := tscWriteFile(t, "a.py", "x")
	hook := tscWriteFile(t, "task_before.sh", "echo hook\n")
	outsideDir := filepath.Join(dataDir, "external")
	if err := os.MkdirAll(outsideDir, 0o755); err != nil {
		t.Fatalf("mkdir outside: %v", err)
	}
	victim := filepath.Join(outsideDir, "victim.py")
	if err := os.WriteFile(victim, []byte("keep"), 0o644); err != nil {
		t.Fatalf("write outside victim: %v", err)
	}
	tscMakeJunction(t, filepath.Join(scriptsDir, "linkOutside"), outsideDir)
	if hit, err := pathHasLinkSegment(base, filepath.Join(scriptsDir, "linkOutside", "victim.py")); err != nil || !hit {
		t.Fatalf("a junction inside the scripts dir must still be detected, got hit=%v err=%v", hit, err)
	}

	plainTask := tscCreateTask(t, "plain", "task a.py")
	hookTask := tscCreateTask(t, "hook", "task task_before.sh")
	junctionTask := tscCreateTask(t, "junction", "task linkOutside/victim.py")
	ids := []uint{plainTask.ID, hookTask.ID, junctionTask.ID}

	preview := PreviewTaskScriptDeletion(ids, env)
	tscAssertDeletable(t, tscItem(t, preview.Scripts, "a.py"))
	tscAssertKept(t, tscItem(t, preview.Scripts, "task_before.sh"), TaskScriptReasonManagedHelper)
	tscAssertKept(t, tscItem(t, preview.Scripts, "linkOutside/victim.py"), TaskScriptReasonSymlink)

	cleanup := CollectTaskScriptTargets(ids, env)
	tscDeleteTaskRows(t, ids...)
	result := cleanup.Execute(nil)
	if len(result.Deleted) != 1 || result.Deleted[0].Path != "a.py" {
		t.Fatalf("only a.py may be deleted, got %#v", result)
	}
	tscAssertGone(t, plain)
	tscAssertExists(t, hook)
	tscAssertExists(t, victim)
}

const (
	tscFsctlSetReparsePoint = 0x000900A4
	tscIOReparseTagDedup    = 0x80000013
)

// tscTagDedup 用 FSCTL_SET_REPARSE_POINT 给一个普通文件打上 IO_REPARSE_TAG_DEDUP，模拟 Windows Server
// 重复数据删除（Data Deduplication）优化作业转换过的文件。普通用户即可，不需要去重驱动。
func tscTagDedup(path string) error {
	name, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	handle, err := syscall.CreateFile(name, syscall.GENERIC_READ|syscall.GENERIC_WRITE, 0, nil, syscall.OPEN_EXISTING,
		syscall.FILE_FLAG_OPEN_REPARSE_POINT|syscall.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return err
	}
	defer syscall.CloseHandle(handle)
	// REPARSE_DATA_BUFFER：ReparseTag(4) + ReparseDataLength(2) + Reserved(2) + 数据。没有驱动会解释这段数据，内容无所谓。
	const payload = 16
	buf := make([]byte, 8+payload)
	binary.LittleEndian.PutUint32(buf[0:4], tscIOReparseTagDedup)
	binary.LittleEndian.PutUint16(buf[4:6], payload)
	var returned uint32
	return syscall.DeviceIoControl(handle, tscFsctlSetReparsePoint, &buf[0], uint32(len(buf)), nil, 0, &returned, nil)
}

// R2-4：Windows Server 的重复数据删除会随时把普通文件转成 IO_REPARSE_TAG_DEDUP 重解析点，Go 刻意把它当普通文件。
// pathHasLinkSegment 末级不查 reparse 属性，这类脚本不能被误判成「经过软链接」，照常可删。
func TestTaskScriptDedupReparseFileDeletable(t *testing.T) {
	env := tscSetup(t)
	full := tscWriteFile(t, "dedup.py", "x")
	if err := tscTagDedup(full); err != nil {
		t.Skipf("cannot set a DEDUP reparse tag on this volume: %v", err)
	}
	info, err := os.Lstat(full)
	if err != nil {
		t.Fatalf("lstat tagged file: %v", err)
	}
	// 前提：文件确实带 reparse 属性，而 Go 仍把它当普通文件——末级也查 isReparsePoint 的旧实现会在这里误判。
	if !isReparsePoint(info) || !info.Mode().IsRegular() {
		t.Fatalf("premise broken: want a regular file carrying FILE_ATTRIBUTE_REPARSE_POINT, got mode=%v reparse=%v", info.Mode(), isReparsePoint(info))
	}
	if hit, err := pathHasLinkSegment(tstBase(t), full); err != nil || hit {
		t.Fatalf("a DEDUP file must not count as a link segment, got hit=%v err=%v", hit, err)
	}

	task := tscCreateTask(t, "dedup", "task dedup.py")
	tscAssertDeletable(t, tscItem(t, PreviewTaskScriptDeletion([]uint{task.ID}, env).Scripts, "dedup.py"))

	cleanup := CollectTaskScriptTargets([]uint{task.ID}, env)
	tscDeleteTaskRows(t, task.ID)
	result := cleanup.Execute(nil)
	if len(result.Deleted) != 1 || result.Deleted[0].Path != "dedup.py" {
		t.Fatalf("expected the DEDUP file to be deleted, got %#v", result)
	}
	tscAssertGone(t, full)
}
