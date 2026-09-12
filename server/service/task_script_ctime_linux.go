//go:build linux

package service

import (
	"os"
	"syscall"
	"time"
)

// fileChangeTime 返回文件的 inode 变更时间（ctime）。Linux/Android 走这里。
//
// ctime 在文件被重建、被 rename 覆盖、被 chmod 时必变，且无法用 utimes/os.Chtimes 还原，
// 是比 size+mtime 更强的「同一个文件、没变过」判据：sameFileIdentity 拿它兜住「同尺寸 + 还原 mtime」
// 的替换（见 A6）。取不到 Stat_t 时 ok 为 false，调用方退回 size+mtime 比对。
func fileChangeTime(fi os.FileInfo) (time.Time, bool) {
	stat, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return time.Time{}, false
	}
	return time.Unix(stat.Ctim.Unix()), true
}
