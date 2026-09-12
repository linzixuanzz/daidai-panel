//go:build !linux

package service

import (
	"os"
	"time"
)

// fileChangeTime 在非 Linux 平台不提供 ctime（Windows 的 Win32FileAttributeData 没有 inode 变更时间，
// 而发布目标平台只有 linux 与 windows，见 release.yml）。返回 ok=false，sameFileIdentity 退回
// os.SameFile + size + mtime 比对；Windows 另有 Collect 时固化文件身份的兜底（见 A6）。
func fileChangeTime(os.FileInfo) (time.Time, bool) {
	return time.Time{}, false
}
